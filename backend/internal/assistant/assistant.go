// Package assistant explains supplied plan facts using deterministic templates.
// It has no model provider, side effects, medical inference or training rules.
package assistant

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Session struct {
	ID, Date, Title, Status string
	Minutes                 int
	DetailsKnown            bool // False requires Minutes=0: migrated facts lack a duration.
}

type Plan struct {
	WeekStart string
	Sessions  []Session
}

type Decision struct {
	Mode, Title                   string
	Reasons                       []string
	BeforeMinutes, AfterMinutes   int
	BeforeSessions, AfterSessions int
}

type Entry struct {
	ID, Date, Status string
	Sleep            *float64
	Fatigue          *int
}

type Context struct {
	Now              time.Time // Caller supplies the user's configured timezone.
	Plan             *Plan
	NextPlan         *Plan // Current proposal, never an implicit accepted schedule.
	AcceptedNextPlan *Plan
	Decision         *Decision
	Entries          []Entry
}

type Evidence struct {
	Source    string `json:"source"`    // plan, nextPlan, acceptedNextPlan, decision, or diary.
	Reference string `json:"reference"` // Session ID, entry ID, or week start.
	Text      string `json:"text"`
}

type Response struct {
	Mode     string     `json:"mode"`
	Intent   string     `json:"intent"`
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}

// Answer recognizes four intent families: why, today/next, progress, recovery.
// It only explains the supplied current projection. Unsupported questions get an
// explicit capability boundary. Callers retain ownership of all context values.
func Answer(question string, context Context) (Response, error) {
	if strings.TrimSpace(question) == "" || !validText(question, 1000) {
		return Response{}, errors.New("вопрос должен содержать от 1 до 1000 байт UTF-8 без управляющих знаков")
	}
	if err := validateContext(context); err != nil {
		return Response{}, err
	}
	q := strings.ToLower(strings.TrimSpace(question))
	result := Response{Mode: "rules", Evidence: []Evidence{}}
	scope := containsAny(q, "план", "занят", "тренир", "нагруз", "plan", "workout", "session")
	switch {
	case containsAny(q, "диагноз", "лекар", "травм", "пульс", "диет", "калори", "погод", "weather", "python", "новост") || containsWord(q, "болит", "болят", "боль", "боли", "болью", "болей"):
		return unsupported(result), nil
	case containsAny(q, "почему", "причин", "измен", "адапт", "объяс", "why", "reason") && (scope || bareQuestion(q, "почему", "why")):
		result.Intent = "why"
	case containsAny(q, "восстанов", "устал", "сон", "спал", "recovery", "sleep", "fatigue"):
		result.Intent = "recovery"
	case containsAny(q, "следующ", "ближайш", "завтра", "next", "tomorrow") && (scope || bareQuestion(q, "что завтра", "что делать завтра", "завтра", "что на следующей неделе", "что на следующую неделю", "next", "tomorrow", "what is next", "what's next")):
		result.Intent = "next"
	case containsAny(q, "сегодня", "today") && (scope || bareQuestion(q, "сегодня", "что сегодня", "что делать сегодня", "today", "what today", "what's today")):
		result.Intent = "today"
	case containsAny(q, "прогресс", "выполн", "итог", "сколько", "progress", "completed") && (scope || bareQuestion(q, "прогресс", "какой прогресс", "как прогресс", "итоги", "итоги недели", "итог", "progress", "completed")):
		result.Intent = "progress"
	default:
		return unsupported(result), nil
	}
	if context.Plan == nil {
		result.Text = "Сначала заполните профиль и создайте план. Тогда я смогу объяснить занятия, прогресс и причины изменений по вашим записям."
		return result, nil
	}
	switch result.Intent {
	case "why":
		return explainDecision(result, context), nil
	case "recovery":
		return explainRecovery(result, context), nil
	case "progress":
		return explainProgress(result, context), nil
	case "today":
		return explainToday(result, context), nil
	default:
		return explainNext(result, context, q), nil
	}
}

func unsupported(result Response) Response {
	result.Intent = "unsupported"
	result.Text = "Могу объяснить ваш план: что запланировано сегодня или дальше, сколько занятий выполнено, какие записи повлияли на изменение и что известно о сне и усталости. Я отвечаю по правилам и вашим данным; другие вопросы не оцениваю."
	return result
}

func explainDecision(result Response, context Context) Response {
	if context.Decision == nil {
		result.Text = "Для этого плана пока нет сохранённого объяснения изменения."
		return result
	}
	d := context.Decision
	result.Text = d.Title
	result.Evidence = append(result.Evidence, Evidence{Source: "decision", Reference: d.Mode, Text: d.Title})
	for _, reason := range d.Reasons {
		result.Text += " " + reason
		result.Evidence = append(result.Evidence, Evidence{Source: "decision", Reference: d.Mode, Text: reason})
	}
	if context.NextPlan != nil {
		result.Text += " Это текущее предложение на следующую неделю; оно не применяется автоматически."
	}
	if context.AcceptedNextPlan != nil {
		result.Text += " Уже принят план на неделю с " + context.AcceptedNextPlan.WeekStart + ": " + planSummary(*context.AcceptedNextPlan) + ". Новое предложение его не заменяет."
		for _, session := range context.AcceptedNextPlan.Sessions {
			result.Evidence = append(result.Evidence, sessionEvidence("acceptedNextPlan", session))
		}
	}
	return result
}

func explainProgress(result Response, context Context) Response {
	done, missed, minutes, unknown := 0, 0, 0, 0
	for _, session := range context.Plan.Sessions {
		switch session.Status {
		case "done":
			done++
			if session.DetailsKnown {
				minutes += session.Minutes
			} else {
				unknown++
			}
		case "missed":
			missed++
		}
		result.Evidence = append(result.Evidence, sessionEvidence("plan", session))
	}
	result.Text = fmt.Sprintf("Неделя с %s: выполнено %d из %d занятий, пропущено %d. По плановой длительности выполненных занятий — %d мин; это не измерение фактического времени тренировки.", context.Plan.WeekStart, done, len(context.Plan.Sessions), missed, minutes)
	if unknown > 0 {
		result.Text += fmt.Sprintf(" Ещё у %d выполненных занятий длительность неизвестна: они не включены в сумму минут.", unknown)
	}
	return result
}

func explainToday(result Response, context Context) Response {
	today := context.Now.Format(time.DateOnly)
	var descriptions []string
	for _, session := range context.Plan.Sessions {
		if session.Date == today {
			evidence := sessionEvidence("plan", session)
			descriptions = append(descriptions, evidence.Text)
			result.Evidence = append(result.Evidence, evidence)
		}
	}
	if len(descriptions) == 0 {
		result.Text = "На сегодня, " + today + ", в текущем плане нет занятия."
		result.Evidence = append(result.Evidence, Evidence{Source: "plan", Reference: context.Plan.WeekStart, Text: "В расписании нет занятия на " + today})
		return result
	}
	result.Text = "Сегодня: " + strings.Join(descriptions, "; ") + "."
	return result
}

func explainNext(result Response, context Context, question string) Response {
	if containsAny(question, "недел", "week") {
		if context.NextPlan == nil && context.AcceptedNextPlan == nil {
			result.Text = "Предложения на следующую неделю пока нет."
			return result
		}
		if context.AcceptedNextPlan != nil {
			result.Text = "Принят план на неделю с " + context.AcceptedNextPlan.WeekStart + ": " + planSummary(*context.AcceptedNextPlan) + "."
			for _, session := range context.AcceptedNextPlan.Sessions {
				result.Evidence = append(result.Evidence, sessionEvidence("acceptedNextPlan", session))
			}
		}
		if context.NextPlan != nil {
			if result.Text != "" {
				result.Text += " "
			}
			result.Text += "Отдельное текущее предложение на неделю с " + context.NextPlan.WeekStart + ": " + planSummary(*context.NextPlan) + ". Оно не заменяет принятое расписание автоматически."
			for _, session := range context.NextPlan.Sessions {
				result.Evidence = append(result.Evidence, sessionEvidence("nextPlan", session))
			}
		}
		return result
	}
	date := context.Now.Format(time.DateOnly)
	tomorrow := containsAny(question, "завтра", "tomorrow")
	if tomorrow {
		date = context.Now.AddDate(0, 0, 1).Format(time.DateOnly)
	}
	var selected *Session
	source := "plan"
	for _, candidate := range []struct {
		plan   *Plan
		source string
	}{{context.Plan, "plan"}, {context.AcceptedNextPlan, "acceptedNextPlan"}} {
		if candidate.plan == nil {
			continue
		}
		for i := range candidate.plan.Sessions {
			session := &candidate.plan.Sessions[i]
			if session.Date < date || session.Status == "done" || session.Status == "missed" || tomorrow && session.Date != date {
				continue
			}
			if selected == nil || session.Date < selected.Date {
				selected = session
				source = candidate.source
			}
		}
	}
	if selected == nil {
		if tomorrow {
			result.Text = "На завтра, " + date + ", в текущем и принятом расписании нет предстоящего занятия."
		} else {
			result.Text = "В текущем плане больше нет предстоящих занятий. Предложение на следующую неделю можно посмотреть отдельно."
		}
		result.Evidence = append(result.Evidence, Evidence{Source: "plan", Reference: context.Plan.WeekStart, Text: "Проверены даты и статусы занятий текущего плана"})
		return result
	}
	evidence := sessionEvidence(source, *selected)
	result.Text = "Ближайшее занятие: " + evidence.Text + "."
	if source == "acceptedNextPlan" {
		result.Text += " Это принятое расписание следующей недели."
	}
	result.Evidence = append(result.Evidence, evidence)
	return result
}

func explainRecovery(result Response, context Context) Response {
	var sleep, fatigue *Entry
	today := context.Now.Format(time.DateOnly)
	week, _ := time.Parse(time.DateOnly, context.Plan.WeekStart) // Validated by Answer.
	weekEnd := week.AddDate(0, 0, 7).Format(time.DateOnly)
	for i := range context.Entries {
		entry := &context.Entries[i]
		if entry.Date < context.Plan.WeekStart || entry.Date >= weekEnd || entry.Date > today {
			continue
		}
		if entry.Sleep != nil && (sleep == nil || entry.Date >= sleep.Date) {
			sleep = entry
		}
		if entry.Fatigue != nil && (fatigue == nil || entry.Date >= fatigue.Date) {
			fatigue = entry
		}
	}
	var facts []string
	if sleep != nil {
		fact := fmt.Sprintf("сон — %g ч (%s)", *sleep.Sleep, sleep.Date)
		facts = append(facts, fact)
		result.Evidence = append(result.Evidence, Evidence{Source: "diary", Reference: sleep.ID, Text: fact})
	}
	if fatigue != nil {
		fact := fmt.Sprintf("усталость — %d/10 (%s)", *fatigue.Fatigue, fatigue.Date)
		facts = append(facts, fact)
		result.Evidence = append(result.Evidence, Evidence{Source: "diary", Reference: fatigue.ID, Text: fact})
	}
	if len(facts) == 0 {
		result.Text = "За текущую неделю пока нет записей о сне и усталости."
		return result
	}
	result.Text = "Последние известные значения за неделю: " + strings.Join(facts, "; ") + "."
	if context.Decision != nil {
		result.Text += " Текущее решение правил: " + context.Decision.Title + "."
		result.Evidence = append(result.Evidence, Evidence{Source: "decision", Reference: context.Decision.Mode, Text: context.Decision.Title})
	}
	return result
}

func sessionEvidence(source string, session Session) Evidence {
	status := "запланировано"
	switch session.Status {
	case "done":
		status = "выполнено"
	case "missed":
		status = "пропущено"
	}
	duration := "длительность неизвестна"
	if session.DetailsKnown {
		duration = fmt.Sprintf("%d мин", session.Minutes)
	}
	return Evidence{Source: source, Reference: session.ID, Text: fmt.Sprintf("%s — %s, %s, %s", session.Date, session.Title, duration, status)}
}

func planSummary(plan Plan) string {
	minutes, unknown := 0, 0
	for _, session := range plan.Sessions {
		if session.DetailsKnown {
			minutes += session.Minutes
		} else {
			unknown++
		}
	}
	text := fmt.Sprintf("%d занятий, %d известных плановых мин", len(plan.Sessions), minutes)
	if unknown > 0 {
		text += fmt.Sprintf("; у %d занятий длительность неизвестна", unknown)
	}
	return text
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func containsWord(value string, needles ...string) bool {
	for _, word := range strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) }) {
		for _, needle := range needles {
			if word == needle {
				return true
			}
		}
	}
	return false
}

func bareQuestion(value string, questions ...string) bool {
	value = strings.TrimFunc(value, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	words := strings.Fields(value)
	normalized := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		// The subject is already the current principal. Treat the optional
		// standalone phrase "у меня" equally in preset and typed questions.
		if words[i] == "у" && i+1 < len(words) && words[i+1] == "меня" {
			i++
			continue
		}
		normalized = append(normalized, words[i])
	}
	value = strings.Join(normalized, " ")
	for _, question := range questions {
		if value == question {
			return true
		}
	}
	return false
}

func validText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validateContext(context Context) error {
	if context.Now.IsZero() || context.Now.Year() < 1 || context.Now.Year() > 9999 {
		return errors.New("для ответа нужно текущее время в часовом поясе пользователя")
	}
	for _, plan := range []*Plan{context.Plan, context.NextPlan, context.AcceptedNextPlan} {
		if plan == nil {
			continue
		}
		week, err := time.Parse(time.DateOnly, plan.WeekStart)
		if err != nil || len(plan.Sessions) > 7 {
			return errors.New("некорректный недельный план в контексте")
		}
		seen := make(map[string]bool, len(plan.Sessions))
		for _, session := range plan.Sessions {
			day, err := time.Parse(time.DateOnly, session.Date)
			durationValid := session.DetailsKnown && session.Minutes >= 15 && session.Minutes <= 90 || !session.DetailsKnown && session.Minutes == 0
			if err != nil || day.Before(week) || !day.Before(week.AddDate(0, 0, 7)) || session.ID == "" || !validText(session.ID, 256) || seen[session.ID] || !validText(session.Title, 512) || !durationValid {
				return errors.New("некорректное занятие в контексте")
			}
			if session.Status != "" && session.Status != "planned" && session.Status != "done" && session.Status != "missed" {
				return errors.New("некорректный статус занятия в контексте")
			}
			seen[session.ID] = true
		}
	}
	if context.Decision != nil {
		d := context.Decision
		if !validText(d.Mode, 80) || !validText(d.Title, 512) || len(d.Reasons) > 8 {
			return errors.New("некорректное объяснение в контексте")
		}
		for _, reason := range d.Reasons {
			if !validText(reason, 1024) {
				return errors.New("некорректная причина изменения в контексте")
			}
		}
	}
	if len(context.Entries) > 500 {
		return errors.New("слишком много записей в контексте")
	}
	for _, entry := range context.Entries {
		if _, err := time.Parse(time.DateOnly, entry.Date); err != nil || !validText(entry.ID, 256) {
			return errors.New("некорректная запись дневника в контексте")
		}
		if entry.Sleep != nil && (math.IsNaN(*entry.Sleep) || math.IsInf(*entry.Sleep, 0) || *entry.Sleep < 0 || *entry.Sleep > 24) {
			return errors.New("некорректное значение сна в контексте")
		}
		if entry.Fatigue != nil && (*entry.Fatigue < 1 || *entry.Fatigue > 10) {
			return errors.New("некорректное значение усталости в контексте")
		}
	}
	return nil
}
