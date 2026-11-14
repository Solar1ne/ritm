// Package rhythm owns the training-plan rules. It does not perform I/O.
package rhythm

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxEntries = 500

var (
	ErrConflict      = errors.New("данные изменились: обновите страницу и повторите действие")
	ErrEntryConflict = errors.New("этот идентификатор записи уже использован для других данных")
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(message string) error       { return &ValidationError{Message: message} }

type Profile struct {
	Name       string `json:"name"`
	Goal       string `json:"goal"`
	Experience string `json:"experience"`
	Days       []int  `json:"days"`
	Minutes    int    `json:"minutes"`
}

type Entry struct {
	ID        string   `json:"id"`
	Date      string   `json:"date"`
	SessionID string   `json:"sessionId,omitempty"`
	Status    string   `json:"status"`
	Sleep     *float64 `json:"sleep,omitempty"`
	Fatigue   *int     `json:"fatigue,omitempty"`
}

type Session struct {
	ID              string `json:"id"`
	Date            string `json:"date"`
	Day             int    `json:"day"`
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	Minutes         int    `json:"minutes"`
	Status          string `json:"status"`
	DetailsKnown    bool   `json:"detailsKnown"`
	VariantID       string `json:"variantId,omitempty"`
	Environment     string `json:"environment,omitempty"`
	LowImpact       bool   `json:"lowImpact,omitempty"`
	OriginalMinutes int    `json:"originalMinutes,omitempty"`
}
type Plan struct {
	WeekStart    string    `json:"weekStart"`
	Sessions     []Session `json:"sessions"`
	TotalMinutes int       `json:"totalMinutes"`
}
type Decision struct {
	Mode           string   `json:"mode"`
	Title          string   `json:"title"`
	Reasons        []string `json:"reasons"`
	BeforeMinutes  int      `json:"beforeMinutes"`
	AfterMinutes   int      `json:"afterMinutes"`
	BeforeSessions int      `json:"beforeSessions"`
	AfterSessions  int      `json:"afterSessions"`
}

// Record is a persisted aggregate. Receipts retain bounded idempotency history;
// they are deliberately not part of the public state response.
type Record struct {
	Version            int                 `json:"version,omitempty"`
	Revision           uint64              `json:"revision"`
	Profile            *Profile            `json:"profile"`
	Entries            []Entry             `json:"entries"`
	Receipts           []Entry             `json:"receipts,omitempty"`
	Weeks              []WeekSnapshot      `json:"weeks,omitempty"`
	Workouts           []Workout           `json:"workouts,omitempty"`
	Reminder           ReminderSettings    `json:"reminder"`
	SessionAdjustments []SessionAdjustment `json:"sessionAdjustments,omitempty"`
	HRVEntries         []HRVEntry          `json:"hrvEntries,omitempty"`
	HRVReceipts        []HRVEntry          `json:"hrvReceipts,omitempty"`
	BodyEntries        []BodyEntry         `json:"bodyEntries,omitempty"`
	BodyReceipts       []BodyEntry         `json:"bodyReceipts,omitempty"`
	MealSelections     []MealSelection     `json:"mealSelections,omitempty"`
}

type Projection struct {
	Plan     *Plan     `json:"plan"`
	NextPlan *Plan     `json:"nextPlan"`
	Decision *Decision `json:"decision"`
}

func Blank() Record { return Record{Entries: []Entry{}} }

func Clone(r Record) Record {
	if r.Profile != nil {
		p := *r.Profile
		p.Days = slices.Clone(p.Days)
		r.Profile = &p
	}
	r.Entries = cloneEntries(r.Entries)
	r.Receipts = cloneEntries(r.Receipts)
	r.Weeks = cloneWeeks(r.Weeks)
	r.Workouts = cloneWorkouts(r.Workouts)
	r.SessionAdjustments = slices.Clone(r.SessionAdjustments)
	r.HRVEntries = slices.Clone(r.HRVEntries)
	r.HRVReceipts = slices.Clone(r.HRVReceipts)
	r.BodyEntries = cloneBodyEntries(r.BodyEntries)
	r.BodyReceipts = cloneBodyEntries(r.BodyReceipts)
	r.MealSelections = slices.Clone(r.MealSelections)
	return r
}
func cloneEntries(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = e
		if e.Sleep != nil {
			v := *e.Sleep
			out[i].Sleep = &v
		}
		if e.Fatigue != nil {
			v := *e.Fatigue
			out[i].Fatigue = &v
		}
	}
	return out
}

func ValidateProfile(p Profile) error {
	if !utf8.ValidString(p.Name) || utf8.RuneCountInString(p.Name) > 40 || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return invalid("Имя должно содержать не более 40 символов без управляющих знаков.")
	}
	if p.Goal != "tone" && p.Goal != "strength" && p.Goal != "endurance" {
		return invalid("Выберите цель: тонус, сила или выносливость.")
	}
	if p.Experience != "beginner" && p.Experience != "regular" {
		return invalid("Выберите уровень подготовки.")
	}
	if len(p.Days) < 1 || len(p.Days) > 5 {
		return invalid("Выберите от одного до пяти дней в неделю.")
	}
	seen := [7]bool{}
	for _, day := range p.Days {
		if day < 0 || day > 6 || seen[day] {
			return invalid("Дни недели должны быть уникальными, от 0 до 6.")
		}
		seen[day] = true
	}
	if p.Minutes < 15 || p.Minutes > 90 {
		return invalid("Длительность занятия — от 15 до 90 минут.")
	}
	return nil
}

func WeekStart(now time.Time) time.Time {
	y, m, d := now.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

func ValidateEntry(e Entry, p Profile, now time.Time, preview bool) error {
	if len(e.ID) < 1 || len(e.ID) > 80 || strings.IndexFunc(e.ID, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) >= 0 {
		return invalid("Некорректный идентификатор записи.")
	}
	date, err := time.ParseInLocation(time.DateOnly, e.Date, now.Location())
	if err != nil {
		return invalid("Дата должна иметь формат ГГГГ-ММ-ДД.")
	}
	start := WeekStart(now)
	if date.Before(start) || !date.Before(start.AddDate(0, 0, 7)) {
		return invalid("Добавляйте записи за текущую неделю.")
	}
	if !preview && e.Date > now.Format(time.DateOnly) {
		return invalid("Нельзя записать результат будущего дня.")
	}
	if e.Sleep != nil && (math.IsNaN(*e.Sleep) || math.IsInf(*e.Sleep, 0) || *e.Sleep < 0 || *e.Sleep > 24) {
		return invalid("Сон должен быть от 0 до 24 часов.")
	}
	if e.Fatigue != nil && (*e.Fatigue < 1 || *e.Fatigue > 10) {
		return invalid("Усталость должна быть от 1 до 10.")
	}
	switch e.Status {
	case "wellbeing":
		if e.SessionID != "" {
			return invalid("Самочувствие не привязано к тренировке.")
		}
		if e.Sleep == nil && e.Fatigue == nil {
			return invalid("Укажите сон или усталость.")
		}
	case "done", "missed":
		if e.SessionID != e.Date {
			return invalid("Дата результата должна совпадать с тренировкой.")
		}
		if !slices.Contains(p.Days, (int(date.Weekday())+6)%7) {
			return invalid("В этот день нет тренировки в текущем плане.")
		}
	default:
		return invalid("Неизвестный статус записи.")
	}
	return nil
}

func SetProfile(r Record, revision uint64, p Profile) (Record, error) {
	if err := ValidateProfile(p); err != nil {
		return Record{}, err
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	r = Clone(r)
	p.Name = strings.TrimSpace(p.Name)
	p.Days = slices.Clone(p.Days)
	slices.Sort(p.Days)
	r.Profile = &p
	r.Revision++
	return r, nil
}

func SameEntry(a, b Entry) bool {
	return a.ID == b.ID && a.Date == b.Date && a.SessionID == b.SessionID && a.Status == b.Status && sameOptional(a.Sleep, b.Sleep) && sameOptional(a.Fatigue, b.Fatigue)
}
func sameOptional[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func AddEntry(r Record, revision uint64, e Entry, now time.Time) (Record, error) {
	for _, history := range [2][]Entry{r.Receipts, r.Entries} {
		for _, previous := range history {
			if previous.ID == e.ID {
				if SameEntry(previous, e) {
					return Clone(r), nil
				}
				return Record{}, ErrEntryConflict
			}
		}
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	if r.Profile == nil {
		return Record{}, invalid("Сначала заполните профиль.")
	}
	var err error
	r, _, err = materialize(r, now)
	if err != nil {
		return Record{}, err
	}
	if err := validateHistoricalEntry(e, r, now); err != nil {
		return Record{}, err
	}
	r = Clone(r)
	r.Entries = upsert(r.Entries, e)
	if len(r.Entries) > MaxEntries {
		return Record{}, invalid("Достигнут лимит 500 записей. Экспортируйте историю и удалите ненужные записи перед добавлением новых.")
	}
	r.Receipts = append(r.Receipts, cloneEntries([]Entry{e})[0])
	if len(r.Receipts) > MaxEntries {
		r.Receipts = slices.Clone(r.Receipts[len(r.Receipts)-MaxEntries:])
	}
	r.Revision++
	return r, nil
}

func Reset(r Record, revision uint64) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	blank := Blank()
	blank.Revision = r.Revision + 1
	return blank, nil
}

func upsert(entries []Entry, e Entry) []Entry {
	out := make([]Entry, 0, len(entries)+1)
	for _, previous := range entries {
		if e.SessionID != "" && previous.SessionID == e.SessionID || e.Status == "wellbeing" && previous.Status == "wellbeing" && previous.Date == e.Date {
			// An omitted/null metric is unknown, not a request to erase a
			// previously reported signal. Explicit replacement values win.
			if e.Sleep == nil {
				e.Sleep = previous.Sleep
			}
			if e.Fatigue == nil {
				e.Fatigue = previous.Fatigue
			}
			continue
		}
		out = append(out, previous)
	}
	out = append(out, cloneEntries([]Entry{e})[0])
	return out
}

// Preview validates synthetic examples without allowing them to become diary facts.
func Preview(p Profile, entries []Entry, now time.Time) (Projection, error) {
	if err := ValidateProfile(p); err != nil {
		return Projection{}, err
	}
	if len(entries) > MaxEntries {
		return Projection{}, invalid("Слишком много записей.")
	}
	unique := make(map[string]Entry, len(entries))
	normalized := []Entry{}
	for _, e := range entries {
		if err := ValidateEntry(e, p, now, true); err != nil {
			return Projection{}, err
		}
		if old, ok := unique[e.ID]; ok {
			if !SameEntry(old, e) {
				return Projection{}, ErrEntryConflict
			}
			continue
		}
		unique[e.ID] = e
		normalized = upsert(normalized, e)
	}
	return Project(Record{Profile: &p, Entries: normalized}, now), nil
}

// Project builds future defaults from the profile, with recovery also bounded
// by the known current schedule. A proposal is never compounded by reading it.
// Session completion counts distinct matching current-week session dates only.
func Project(r Record, now time.Time) Projection {
	if r.Profile == nil {
		return Projection{}
	}
	p := *r.Profile
	days := slices.Clone(p.Days)
	slices.Sort(days)
	start := WeekStart(now)
	plan := makePlan(p, days, p.Minutes, start, false)
	if saved := snapshot(r, start.Format(time.DateOnly)); saved != nil {
		plan = clonePlan(saved.Plan)
	}
	plan = applySessionAdjustments(plan, r.SessionAdjustments)
	statuses := make(map[string]string, len(plan.Sessions))
	for _, s := range plan.Sessions {
		statuses[s.ID] = "planned"
	}
	var latestSleep, latestFatigue *Entry
	hasCurrentHistory := false
	for i := range r.Entries {
		e := &r.Entries[i]
		if e.Date < plan.WeekStart || e.Date >= start.AddDate(0, 0, 7).Format(time.DateOnly) {
			continue
		}
		hasCurrentHistory = true
		if _, exists := statuses[e.SessionID]; exists && (e.Status == "done" || e.Status == "missed") {
			statuses[e.SessionID] = e.Status
		}
		if e.Sleep != nil && (latestSleep == nil || e.Date >= latestSleep.Date) {
			latestSleep = e
		}
		if e.Fatigue != nil && (latestFatigue == nil || e.Date >= latestFatigue.Date) {
			latestFatigue = e
		}
	}
	done, missed, plannedCount := 0, 0, 0
	adjusted := false
	for i := range plan.Sessions {
		s := &plan.Sessions[i]
		s.Status = statuses[s.ID]
		adjusted = adjusted || s.VariantID != ""
		if !s.DetailsKnown {
			continue
		}
		plannedCount++
		if s.Status == "done" {
			done++
		}
		if s.Status == "missed" {
			missed++
		}
	}
	d := Decision{Mode: "maintain", Title: "Сохраняем ритм", Reasons: []string{"Пока недостаточно оснований менять нагрузку. Продолжайте отмечать занятия и самочувствие."}, BeforeMinutes: plan.TotalMinutes, BeforeSessions: plannedCount}
	minutes, count := p.Minutes, len(days)
	if !hasCurrentHistory {
		d.Mode, d.Title = "start", "Начинаем с вашего ритма"
		d.Reasons = []string{"Истории пока нет. План построен по цели, опыту и выбранным дням; результаты не подставляются автоматически."}
	} else if latestFatigue != nil && *latestFatigue.Fatigue >= 8 || latestSleep != nil && *latestSleep.Sleep < 6 {
		d.Mode, d.Title = "recover", "Даём время восстановиться"
		d.Reasons = []string{}
		if latestFatigue != nil && *latestFatigue.Fatigue >= 8 {
			d.Reasons = append(d.Reasons, fmt.Sprintf("Последняя отметка усталости (%s) — %d из 10 (порог: 8).", latestFatigue.Date, *latestFatigue.Fatigue))
		}
		if latestSleep != nil && *latestSleep.Sleep < 6 {
			d.Reasons = append(d.Reasons, fmt.Sprintf("Последний сон (%s) — %g ч, меньше 6 часов.", latestSleep.Date, *latestSleep.Sleep))
		}
		d.Reasons = append(d.Reasons, "Восстановление имеет приоритет: на одно занятие меньше (минимум одно), базовая длительность на 20% меньше с округлением вниз (минимум 15 минут).")
		count, minutes = max(1, count-1), max(15, minutes*80/100)
		if plannedCount > 0 {
			// The profile can grow after this week's schedule was frozen, or
			// explicit variants can shorten it. Recovery must respect both
			// bases; unknown legacy durations are not treated as zero load.
			count = min(count, max(1, plannedCount-1))
			minutes = min(minutes, max(15, plan.TotalMinutes*80/(plannedCount*100)))
			d.Reasons = append(d.Reasons, "Предложение также ограничено числом занятий и средней известной длительностью сохранённой текущей недели с учётом выбранных замен.")
		} else {
			d.Reasons = append(d.Reasons, "Длительности занятий текущей недели неизвестны. Используем настройки профиля; сравнить предложение с текущим объёмом невозможно.")
		}
		if plannedCount > 0 && plannedCount < len(plan.Sessions) {
			d.Reasons = append(d.Reasons, "Занятия с неизвестной длительностью не участвуют в сравнении объёма.")
		}
		if minutes == 15 {
			d.Reasons = append(d.Reasons, "Минимальная длительность занятия — 15 минут: ниже этой границы дополнительно уменьшить длительность нельзя.")
		}
	} else if missed >= 2 {
		d.Mode, d.Title = "simplify", "Делаем неделю посильнее"
		d.Reasons = []string{fmt.Sprintf("За неделю отмечено пропусков: %d. Убираем одно занятие, сохраняя длительность остальных.", missed)}
		count = max(1, count-1)
	} else if !adjusted && plannedCount > 0 && done == plannedCount && latestSleep != nil && *latestSleep.Sleep >= 7 && latestFatigue != nil && *latestFatigue.Fatigue <= 4 && min(90, minutes*105/100) > minutes {
		d.Mode, d.Title = "progress", "Можно сделать небольшой шаг"
		d.Reasons = []string{"Все запланированные занятия выполнены; сон не меньше 7 часов, усталость не выше 4 из 10.", "Предлагаем +5% к длительности, с округлением вниз и пределом 90 минут. Это предложение на следующую неделю."}
		minutes = min(90, minutes*105/100)
	}
	if adjusted && d.Mode == "maintain" {
		d.Reasons = append(d.Reasons, "В этой неделе есть выбранные замены занятий. Их выполнение не используется как основание для повышения исходной нагрузки на 5%.")
	}
	next := makePlan(p, days[:count], minutes, start.AddDate(0, 0, 7), d.Mode == "recover")
	d.AfterMinutes, d.AfterSessions = next.TotalMinutes, len(next.Sessions)
	return Projection{Plan: &plan, NextPlan: &next, Decision: &d}
}

func makePlan(p Profile, days []int, minutes int, start time.Time, recovery bool) Plan {
	plan := Plan{WeekStart: start.Format(time.DateOnly), Sessions: make([]Session, 0, len(days))}
	for i, day := range days {
		kind, title := "strength", "Силовая тренировка"
		if p.Goal == "tone" && i%2 == 1 {
			kind, title = "walk", "Активная прогулка"
		}
		if p.Goal == "endurance" {
			kind, title = "run", "Бег в спокойном темпе"
			if p.Experience == "beginner" {
				title = "Чередование ходьбы и бега"
			}
		}
		if p.Experience == "beginner" && kind == "strength" {
			title = "Базовая тренировка всего тела"
		}
		if recovery {
			kind, title = "mobility", "Мягкая подвижность и прогулка"
		}
		date := start.AddDate(0, 0, day).Format(time.DateOnly)
		plan.Sessions = append(plan.Sessions, Session{ID: date, Date: date, Day: day, Kind: kind, Title: title, Minutes: minutes, Status: "planned", DetailsKnown: true})
		plan.TotalMinutes += minutes
	}
	return plan
}
