// Package aicoach defines the bounded, identity-free contract of an optional AI
// coach. It owns validation, not persistence, plan adaptation or provider I/O.
package aicoach

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Task string

const (
	TaskAsk         Task = "ask"
	TaskReview      Task = "review"
	TaskCheckin     Task = "checkin"
	MaxMessageRunes      = 2000
	MaxFacts             = 80
	MaxFactRunes         = 600
	MaxContextRunes      = 16000
	MaxDays              = 91
)

var (
	ErrInvalidRequest = errors.New("некорректные данные запроса AI")
	ErrInvalidResult  = errors.New("AI вернул ответ, не прошедший проверку")
)

type Request struct {
	Task    Task    `json:"task"`
	Message string  `json:"message"`
	Context Context `json:"context"`
}

// Context intentionally has no profile name, identity or credentials. The
// consumer supplies only facts the user has explicitly agreed to send.
type Context struct {
	Today    string `json:"today"`
	TimeZone string `json:"timeZone"`
	Facts    []Fact `json:"facts"`
	Days     []Day  `json:"days"`
}

type Fact struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Day is an allowlist for check-in drafts. Empty SessionID allows wellbeing
// only. A session reference is its ISO date, as in the saved schedule.
type Day struct {
	Date      string `json:"date"`
	SessionID string `json:"sessionId"`
}

type Result struct {
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Insights   []Insight `json:"insights"`
	NextAction string    `json:"nextAction"`
	Draft      *Draft    `json:"draft"`
}

type Insight struct {
	Text    string   `json:"text"`
	FactIDs []string `json:"factIds"`
}

// Draft is a proposal for a separate, user-confirmed diary operation. It has no
// entry ID or revision and cannot authorize a write. Nil metrics stay unknown.
type Draft struct {
	Date      string   `json:"date"`
	Status    string   `json:"status"`
	SessionID string   `json:"sessionId"`
	Sleep     *float64 `json:"sleep"`
	Fatigue   *int     `json:"fatigue"`
}

// ValidateRequest is safe to run before paying for a provider call. The caller
// retains ownership of every input; validation does not modify any value.
func ValidateRequest(req Request) error {
	if req.Task != TaskAsk && req.Task != TaskReview && req.Task != TaskCheckin {
		return requestError("неизвестная задача")
	}
	if !validText(req.Message, MaxMessageRunes, req.Task == TaskReview) {
		return requestError("нужен текст допустимой длины")
	}
	if len(req.Context.TimeZone) > 64 || req.Context.TimeZone == "" {
		return requestError("неверный часовой пояс")
	}
	location, err := time.LoadLocation(req.Context.TimeZone)
	if err != nil {
		return requestError("неверный часовой пояс")
	}
	today, err := time.ParseInLocation(time.DateOnly, req.Context.Today, location)
	if err != nil || today.Year() < 1970 {
		return requestError("неверная текущая дата")
	}
	if len(req.Context.Facts) > MaxFacts || len(req.Context.Days) > MaxDays {
		return requestError("слишком большой контекст")
	}
	factIDs := make(map[string]bool, len(req.Context.Facts))
	total := 0
	for _, fact := range req.Context.Facts {
		if !validID(fact.ID) || factIDs[fact.ID] || !validText(fact.Text, MaxFactRunes, false) {
			return requestError("неверный или повторяющийся факт")
		}
		factIDs[fact.ID] = true
		total += utf8.RuneCountInString(fact.Text)
	}
	if total > MaxContextRunes {
		return requestError("слишком большой контекст")
	}
	oldest := today.AddDate(0, 0, -90).Format(time.DateOnly)
	days := make(map[string]bool, len(req.Context.Days))
	for _, day := range req.Context.Days {
		if _, err := time.ParseInLocation(time.DateOnly, day.Date, location); err != nil || day.Date < oldest || day.Date > req.Context.Today || days[day.Date] {
			return requestError("неверная или повторяющаяся дата черновика")
		}
		if day.SessionID != "" && day.SessionID != day.Date {
			return requestError("занятие не соответствует дате")
		}
		days[day.Date] = true
	}
	return nil
}

// ValidateResult checks structure and reference membership. A valid reference
// is factual provenance, not proof that generated prose is semantically true.
func ValidateResult(req Request, result Result) error {
	if err := ValidateRequest(req); err != nil {
		return err
	}
	if !validText(result.Title, 120, false) || !validText(result.Summary, 1600, false) || len(result.Insights) > 6 {
		return resultError("неверная длина ответа")
	}
	switch result.NextAction {
	case "journal", "plan", "activities", "wellness", "nutrition", "none":
	default:
		return resultError("неизвестное следующее действие")
	}
	known := make(map[string]bool, len(req.Context.Facts))
	for _, fact := range req.Context.Facts {
		known[fact.ID] = true
	}
	for _, insight := range result.Insights {
		if !validText(insight.Text, 500, false) || len(insight.FactIDs) < 1 || len(insight.FactIDs) > 8 {
			return resultError("вывод без допустимого фактического основания")
		}
		seen := make(map[string]bool, len(insight.FactIDs))
		for _, id := range insight.FactIDs {
			if !known[id] || seen[id] {
				return resultError("неизвестная или повторяющаяся ссылка на факт")
			}
			seen[id] = true
		}
	}
	if result.Draft == nil {
		return nil
	}
	if req.Task != TaskCheckin {
		return resultError("черновик допустим только для записи дневника")
	}
	return validateDraft(req.Context.Days, *result.Draft)
}

func validateDraft(days []Day, draft Draft) error {
	var allowed *Day
	for i := range days {
		if days[i].Date == draft.Date {
			allowed = &days[i]
			break
		}
	}
	if allowed == nil {
		return resultError("дата черновика отсутствует в разрешённом списке")
	}
	if draft.Sleep != nil && (math.IsNaN(*draft.Sleep) || math.IsInf(*draft.Sleep, 0) || *draft.Sleep < 0 || *draft.Sleep > 24) {
		return resultError("неверное значение сна")
	}
	if draft.Fatigue != nil && (*draft.Fatigue < 1 || *draft.Fatigue > 10) {
		return resultError("неверное значение усталости")
	}
	switch draft.Status {
	case "done", "missed":
		if allowed.SessionID == "" || draft.SessionID != allowed.SessionID {
			return resultError("черновик не соответствует сохранённому занятию")
		}
	case "wellbeing":
		if draft.SessionID != "" || draft.Sleep == nil && draft.Fatigue == nil {
			return resultError("для самочувствия нужен показатель без ссылки на занятие")
		}
	default:
		return resultError("неверный статус черновика")
	}
	return nil
}

func validText(value string, maxRunes int, allowEmpty bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes || !allowEmpty && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func validID(value string) bool {
	if len(value) < 1 || len(value) > 80 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r) {
			continue
		}
		return false
	}
	return true
}

func requestError(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidRequest, reason) }
func resultError(reason string) error  { return fmt.Errorf("%w: %s", ErrInvalidResult, reason) }
