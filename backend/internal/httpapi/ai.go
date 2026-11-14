package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"rhythm/internal/aicoach"
	"rhythm/internal/nutrition"
	"rhythm/internal/rhythm"
)

// AIGenerator is the optional external boundary. No domain mutation depends on it.
type AIGenerator interface {
	Generate(context.Context, aicoach.Request) (aicoach.Result, error)
}

type aiStatus struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
}

type aiContextView struct {
	Status       aiStatus        `json:"status"`
	Revision     uint64          `json:"revision"`
	Context      aicoach.Context `json:"context"`
	ContextToken string          `json:"contextToken"`
}

func (s *Server) aiStatus() aiStatus {
	return aiStatus{Configured: s.ai != nil, Provider: s.aiProvider, Model: s.aiModel}
}

func (s *Server) aiContext(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	task := aicoach.Task(r.URL.Query().Get("task"))
	if task != aicoach.TaskAsk && task != aicoach.TaskReview && task != aicoach.TaskCheckin {
		s.fail(w, 400, "validation", "Выберите вопрос, разбор недели или запись в дневник.")
		return
	}
	record, err := s.repo.Update(r.Context(), id, func(record rhythm.Record) (rhythm.Record, error) { return rhythm.EnsureWeeks(record, s.now()) })
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if record.Profile == nil {
		s.fail(w, 400, "validation", "Сначала создайте свой план.")
		return
	}
	view := aiContextView{Status: s.aiStatus(), Revision: record.Revision, Context: buildAIContext(record, task, s.now())}
	view.ContextToken = s.aiContextToken(id, record.Revision, task, view.Context, s.now().Add(5*time.Minute).Unix())
	s.json(w, 200, view)
}

// Consent is bound to the provider, model, exact server-built context and account.
// The HMAC is never used as an authentication credential.
func (s *Server) aiContextToken(id string, revision uint64, task aicoach.Task, value aicoach.Context, expires int64) string {
	mac := hmac.New(sha256.New, s.secret)
	// Context consists exclusively of validated finite values and strings.
	data, err := json.Marshal(struct {
		ID       string
		Revision uint64
		Task     aicoach.Task
		Context  aicoach.Context
		Expires  int64
		Provider string
		Model    string
	}{id, revision, task, value, expires, s.aiProvider, s.aiModel})
	if err != nil {
		return ""
	}
	_, _ = mac.Write(data)
	return strconv.FormatInt(expires, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) aiRespond(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision     *uint64      `json:"revision"`
		Task         aicoach.Task `json:"task"`
		Message      string       `json:"message"`
		Consent      bool         `json:"consent"`
		ContextToken string       `json:"contextToken"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if !input.Consent {
		s.fail(w, 400, "ai_consent", "Для этого запроса подтвердите передачу выбранных данных в "+s.aiProvider+".")
		return
	}
	if input.Revision == nil {
		s.fail(w, 400, "validation", "Нужно передать revision.")
		return
	}
	record, err := s.repo.Read(r.Context(), id)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if record.Profile == nil {
		s.fail(w, 400, "validation", "Сначала создайте свой план.")
		return
	}
	if *input.Revision != record.Revision {
		s.failure(w, r, rhythm.ErrConflict)
		return
	}
	value := buildAIContext(record, input.Task, s.now())
	req := aicoach.Request{Task: input.Task, Message: strings.TrimSpace(input.Message), Context: value}
	if err := aicoach.ValidateRequest(req); err != nil {
		s.fail(w, 400, "validation", "Проверьте тип запроса и текст: не более 2000 символов.")
		return
	}
	expiresText, _, found := strings.Cut(input.ContextToken, ".")
	expires, parseErr := strconv.ParseInt(expiresText, 10, 64)
	want := s.aiContextToken(id, record.Revision, input.Task, value, expires)
	if !found || parseErr != nil || expires <= s.now().Unix() || expires > s.now().Add(5*time.Minute).Unix() || want == "" || !hmac.Equal([]byte(want), []byte(input.ContextToken)) {
		s.fail(w, 409, "ai_context_changed", "Контекст изменился. Просмотрите данные и подтвердите передачу ещё раз.")
		return
	}
	if s.ai == nil {
		s.fail(w, 503, "ai_unavailable", "AI пока не подключён. Ответы по правилам и весь дневник доступны ниже.")
		return
	}
	ticket, allowed := s.aiQuota.acquire(id, s.now())
	if !allowed {
		w.Header().Set("Retry-After", "60")
		s.fail(w, 429, "ai_limit", "Лимит AI-запросов временно исчерпан. Попробуйте позже или используйте помощника по правилам.")
		return
	}
	defer func() { s.aiQuota.discard(ticket) }()
	ctx, cancel := context.WithTimeout(r.Context(), s.aiTimeout)
	defer cancel()
	started := time.Now()
	result, err := s.ai.Generate(ctx, req)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = aicoach.ValidateResult(req, result)
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		s.aiQuota.discard(ticket)
	} else {
		s.aiQuota.finish(ticket, err == nil, s.now())
	}
	if err != nil {
		outcome := "unavailable"
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = "timeout"
		} else if errors.Is(err, aicoach.ErrInvalidResult) {
			outcome = "invalid_result"
		}
		status := 0
		var providerError interface{ HTTPStatus() int }
		if errors.As(err, &providerError) && providerError.HTTPStatus() >= 100 && providerError.HTTPStatus() <= 599 {
			status = providerError.HTTPStatus()
		}
		s.logger.Info("ai request ended", "provider", s.aiProvider, "task", input.Task, "outcome", outcome, "provider_status", status, "duration_ms", time.Since(started).Milliseconds())
		s.fail(w, 503, "ai_unavailable", "AI не смог подготовить проверяемый ответ. Ваши данные не изменились. Используйте помощника по правилам или попробуйте позже.")
		return
	}
	// The provider may finish after a diary change in another tab. Never present
	// that answer as based on the current revision, and never apply its draft.
	latest, err := s.repo.Read(r.Context(), id)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if latest.Revision != record.Revision || s.now().Format(time.DateOnly) != value.Today {
		s.failure(w, r, rhythm.ErrConflict)
		return
	}
	s.logger.Info("ai request ended", "provider", s.aiProvider, "task", input.Task, "outcome", "success", "duration_ms", time.Since(started).Milliseconds())
	s.json(w, 200, struct {
		Mode     string         `json:"mode"`
		Provider string         `json:"provider"`
		Model    string         `json:"model"`
		Revision uint64         `json:"revision"`
		Result   aicoach.Result `json:"result"`
		Facts    []aicoach.Fact `json:"facts"`
	}{"ai", s.aiProvider, s.aiModel, record.Revision, result, value.Facts})
}

func buildAIContext(record rhythm.Record, task aicoach.Task, now time.Time) aicoach.Context {
	out := aicoach.Context{Today: now.Format(time.DateOnly), TimeZone: now.Location().String(), Facts: []aicoach.Fact{}, Days: []aicoach.Day{}}
	add := func(text string) {
		out.Facts = append(out.Facts, aicoach.Fact{ID: fmt.Sprintf("f%d", len(out.Facts)+1), Text: text})
	}
	start := now.AddDate(0, 0, -13).Format(time.DateOnly)
	for n := -13; n <= 0; n++ {
		date := now.AddDate(0, 0, n)
		view, err := rhythm.Week(record, rhythm.WeekStart(date).Format(time.DateOnly), now)
		if err != nil {
			continue
		}
		day := aicoach.Day{Date: date.Format(time.DateOnly)}
		for _, session := range view.Plan.Sessions {
			if session.Date == day.Date {
				day.SessionID = session.ID
			}
		}
		out.Days = append(out.Days, day)
	}
	add("Сегодня " + out.Today + ". Пустые дни означают отсутствие наблюдения, а не пропуск. Сведения вводит пользователь; медицинские выводы не проверены.")
	if task == aicoach.TaskCheckin {
		add("Распознаётся только текст этого запроса. Не дополняй сон, усталость или выполнение из прошлого дневника. Неоднозначные значения требуют уточнения.")
		return out
	}
	if record.Profile != nil {
		goals := map[string]string{"tone": "тонус", "strength": "сила", "endurance": "выносливость"}
		add(fmt.Sprintf("Цель: %s. Опыт: %s. Желаемая длительность занятия: %d мин.", goals[record.Profile.Goal], record.Profile.Experience, record.Profile.Minutes))
	}
	projection := rhythm.Project(record, now)
	planFact := func(label string, plan *rhythm.Plan) {
		if plan == nil {
			return
		}
		unknown := 0
		for _, session := range plan.Sessions {
			if !session.DetailsKnown {
				unknown++
			}
		}
		add(fmt.Sprintf("%s: неделя %s, %d занятий, сумма известных плановых минут %d (не фактических). Неизвестна длительность %d занятий.", label, plan.WeekStart, len(plan.Sessions), plan.TotalMinutes, unknown))
		for _, session := range plan.Sessions {
			if session.DetailsKnown {
				add(fmt.Sprintf("%s: %s, %s, %d плановых минут; статус %s.", label, session.Date, session.Title, session.Minutes, session.Status))
			} else {
				add(fmt.Sprintf("%s: %s, длительность и вид занятия неизвестны; статус %s.", label, session.Date, session.Status))
			}
		}
	}
	planFact("Текущий план", projection.Plan)
	planFact("Предложение, ещё не принятое", projection.NextPlan)
	planFact("Отдельно принятый следующий план", rhythm.AcceptedNextPlan(record, now))
	if d := projection.Decision; d != nil {
		add(fmt.Sprintf("Правила адаптации: %s. %s Новый недельный объём %d минут, исходная сумма известных плановых минут %d. Модель не меняет этот расчёт.", d.Title, strings.Join(d.Reasons, " "), d.AfterMinutes, d.BeforeMinutes))
	}
	// Latest non-null values per date match diary graph semantics. Do not double
	// count a night when both a workout and wellbeing entry mention it.
	for n := -13; n <= 0; n++ {
		date := now.AddDate(0, 0, n).Format(time.DateOnly)
		var sleep *float64
		var fatigue *int
		status := "нет отметки о занятии"
		seen := false
		for _, e := range record.Entries {
			if e.Date != date {
				continue
			}
			seen = true
			if e.Sleep != nil {
				sleep = e.Sleep
			}
			if e.Fatigue != nil {
				fatigue = e.Fatigue
			}
			if e.SessionID != "" {
				if e.Status == "done" {
					status = "занятие выполнено"
				} else if e.Status == "missed" {
					status = "занятие пропущено"
				}
			}
		}
		if !seen {
			continue
		}
		text := date + ": " + status
		if sleep != nil {
			text += fmt.Sprintf("; сон %.1f ч", *sleep)
		}
		if fatigue != nil {
			text += fmt.Sprintf("; усталость %d/10", *fatigue)
		}
		add(text)
	}
	count, knownDistances, minutes, meters := 0, 0, 0.0, 0.0
	for _, workout := range record.Workouts {
		if workout.Date < start || workout.Date > out.Today {
			continue
		}
		count++
		minutes += workout.Minutes
		if workout.DistanceMeters != nil {
			meters += *workout.DistanceMeters
			knownDistances++
		}
	}
	if count > 0 {
		distance := "дистанция не указана"
		if knownDistances > 0 {
			distance = fmt.Sprintf("%.0f метров известной дистанции у %d из %d активностей", meters, knownDistances, count)
		}
		add(fmt.Sprintf("Импорт за 14 дней: %d активностей, %.1f фактических минут; %s. Может совпадать с выполнением плана; не складывай как разные занятия.", count, minutes, distance))
	}
	addWellnessFacts(record, start, out.Today, add)
	for n := 0; n < 7; n++ {
		date := now.AddDate(0, 0, n).Format(time.DateOnly)
		var selected []string
		for _, item := range record.MealSelections {
			if item.Date == date {
				if recipe, ok := nutrition.Find(item.RecipeID); ok {
					selected = append(selected, item.Slot+": "+recipe.Title)
				}
			}
		}
		if len(selected) > 0 {
			slices.Sort(selected)
			add(fmt.Sprintf("Меню %s: %s. Это выбранные блюда, не факт употребления, не суточная норма и не сведения об аллергиях.", date, strings.Join(selected, "; ")))
		}
	}
	return out
}

// Only a bounded projection crosses the consent boundary. Source is free text
// (and can contain names), so local series labels replace it before transmission.
func addWellnessFacts(record rhythm.Record, from, through string, add func(string)) {
	hrv := slices.Clone(record.HRVEntries)
	slices.SortStableFunc(hrv, func(a, b rhythm.HRVEntry) int { return strings.Compare(b.Date, a.Date) })
	hrvSeries := map[[3]string]int{}
	hrvCount, hrvSent := 0, 0
	for _, entry := range hrv {
		if entry.Date < from || entry.Date > through {
			continue
		}
		hrvCount++
		if hrvSent >= 6 {
			continue
		}
		key := [3]string{entry.Metric, entry.Window, entry.Source}
		if hrvSeries[key] == 0 {
			hrvSeries[key] = len(hrvSeries) + 1
		}
		add(fmt.Sprintf("HRV-серия %d, %s: %s %g мс, окно %s. Другая серия означает другой прибор или условия; показатель не определяет здоровье или нагрузку.", hrvSeries[key], entry.Date, entry.Metric, entry.ValueMs, entry.Window))
		hrvSent++
	}
	if hrvCount > hrvSent {
		add(fmt.Sprintf("HRV: показаны последние %d из %d записей за 14 дней; остальная история в контекст не включена.", hrvSent, hrvCount))
	}
	body := slices.Clone(record.BodyEntries)
	slices.SortStableFunc(body, func(a, b rhythm.BodyEntry) int { return strings.Compare(b.Date, a.Date) })
	bodySeries := map[[2]string]int{}
	bodyCount, bodySent := 0, 0
	for _, entry := range body {
		if entry.Date < from || entry.Date > through {
			continue
		}
		bodyCount++
		if bodySent >= 6 {
			continue
		}
		key := [2]string{entry.Method, entry.Source}
		if bodySeries[key] == 0 {
			bodySeries[key] = len(bodySeries) + 1
		}
		weight, fat := "вес не указан", "доля жира не указана"
		if entry.WeightKg != nil {
			weight = fmt.Sprintf("вес %g кг", *entry.WeightKg)
		}
		if entry.BodyFatPercent != nil {
			fat = fmt.Sprintf("доля жира %g%%", *entry.BodyFatPercent)
		}
		add(fmt.Sprintf("Серия состава тела %d, %s, метод %s: %s; %s. Сравнимость ограничена одним методом, прибором и условиями. Медицинская интерпретация отсутствует.", bodySeries[key], entry.Date, entry.Method, weight, fat))
		bodySent++
	}
	if bodyCount > bodySent {
		add(fmt.Sprintf("Состав тела: показаны последние %d из %d записей за 14 дней; остальная история в контекст не включена.", bodySent, bodyCount))
	}
}
