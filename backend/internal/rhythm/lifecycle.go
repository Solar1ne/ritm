package rhythm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	RecordVersion = 2
	ExportVersion = 2
	MaxWeeks      = 520
	HistoryDays   = 90
)

var (
	ErrNotFound     = errors.New("запись или сохранённая неделя не найдена")
	ErrPlanConflict = errors.New("предложение изменилось или неделя уже утверждена: обновите данные")
	ErrCapacity     = errors.New("достигнут лимит хранилища: экспортируйте историю и удалите ненужные записи")
)

// WeekSnapshot is an immutable schedule, not a completion log. Status is applied
// from Entries when reading a week. Legacy unknown durations stay unknown.
type WeekSnapshot struct {
	WeekStart     string   `json:"weekStart"`
	Source        string   `json:"source"`
	Profile       *Profile `json:"profile"`
	Plan          Plan     `json:"plan"`
	AcceptedToken string   `json:"acceptedToken,omitempty"`
	AcceptedAt    string   `json:"acceptedAt,omitempty"`
}

type WeekView struct {
	WeekStart    string   `json:"weekStart"`
	Source       string   `json:"source"`
	Profile      *Profile `json:"profile"`
	Plan         Plan     `json:"plan"`
	Entries      []Entry  `json:"entries"`
	Editable     bool     `json:"editable"`
	EditableFrom string   `json:"editableFrom"`
}

func clonePlan(p Plan) Plan { p.Sessions = slices.Clone(p.Sessions); return p }
func cloneProfile(p *Profile) *Profile {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Days = slices.Clone(p.Days)
	return &copy
}
func cloneWeeks(weeks []WeekSnapshot) []WeekSnapshot {
	out := make([]WeekSnapshot, len(weeks))
	for i, w := range weeks {
		out[i] = w
		out[i].Profile = cloneProfile(w.Profile)
		out[i].Plan = clonePlan(w.Plan)
	}
	return out
}
func snapshot(r Record, weekStart string) *WeekSnapshot {
	for i := range r.Weeks {
		if r.Weeks[i].WeekStart == weekStart {
			return &r.Weeks[i]
		}
	}
	return nil
}
func dayAt(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}
func EditableFrom(now time.Time) string {
	return dayAt(now).AddDate(0, 0, -HistoryDays).Format(time.DateOnly)
}

func appendWeek(r *Record, w WeekSnapshot) error {
	if len(r.Weeks) >= MaxWeeks {
		return ErrCapacity
	}
	r.Weeks = append(r.Weeks, w)
	slices.SortFunc(r.Weeks, func(a, b WeekSnapshot) int { return strings.Compare(a.WeekStart, b.WeekStart) })
	return nil
}

func baseline(p Profile, start time.Time, source string) WeekSnapshot {
	p.Days = slices.Clone(p.Days)
	slices.Sort(p.Days)
	return WeekSnapshot{WeekStart: start.Format(time.DateOnly), Source: source, Profile: cloneProfile(&p), Plan: makePlan(p, p.Days, p.Minutes, start, false)}
}

// materialize never guesses historic training details from today's profile.
// Old aggregates get observed-only past weeks. Once snapshots exist, unchanged
// future defaults can advance through missed calendar weeks deterministically.
func materialize(input Record, now time.Time) (Record, bool, error) {
	r := Clone(input)
	if r.Profile == nil {
		if len(r.Entries) > 0 || len(r.Weeks) > 0 || len(r.Workouts) > 0 || len(r.SessionAdjustments) > 0 || len(r.HRVEntries) > 0 || len(r.BodyEntries) > 0 || len(r.MealSelections) > 0 {
			return Record{}, false, invalid("История не может существовать без профиля.")
		}
		return r, false, nil
	}
	if err := ValidateProfile(*r.Profile); err != nil {
		return Record{}, false, err
	}
	changed := false
	current := WeekStart(now)
	currentID := current.Format(time.DateOnly)
	if r.Version < RecordVersion {
		// Preserve every actual fact; lack of old scheduling metadata is explicit.
		for _, e := range r.Entries {
			date, err := time.ParseInLocation(time.DateOnly, e.Date, now.Location())
			if err != nil {
				return Record{}, false, invalid("В старом дневнике повреждена дата.")
			}
			weekID := WeekStart(date).Format(time.DateOnly)
			if weekID >= currentID {
				continue
			}
			if snapshot(r, weekID) == nil {
				if err := appendWeek(&r, WeekSnapshot{WeekStart: weekID, Source: "legacy", Plan: Plan{WeekStart: weekID, Sessions: []Session{}}}); err != nil {
					return Record{}, false, err
				}
			}
			w := snapshot(r, weekID)
			if e.SessionID != "" && e.SessionID == e.Date && !slices.ContainsFunc(w.Plan.Sessions, func(s Session) bool { return s.ID == e.SessionID }) {
				w.Plan.Sessions = append(w.Plan.Sessions, Session{ID: e.SessionID, Date: e.Date, Day: (int(date.Weekday()) + 6) % 7, Kind: "walk", Title: "Занятие из прежнего дневника", Status: "planned", DetailsKnown: false})
				slices.SortFunc(w.Plan.Sessions, func(a, b Session) int { return strings.Compare(a.Date, b.Date) })
			}
		}
		if snapshot(r, currentID) == nil {
			if err := appendWeek(&r, baseline(*r.Profile, current, "legacy")); err != nil {
				return Record{}, false, err
			}
		}
		// A legacy profile may have changed days, type or duration after a fact
		// was logged. Even a matching date cannot prove that session's details.
		w := snapshot(r, currentID)
		for _, e := range r.Entries {
			if e.Date < currentID || e.Date >= current.AddDate(0, 0, 7).Format(time.DateOnly) || e.SessionID == "" || e.SessionID != e.Date {
				continue
			}
			date, err := time.ParseInLocation(time.DateOnly, e.Date, now.Location())
			if err != nil {
				return Record{}, false, invalid("В старом дневнике повреждена дата.")
			}
			unknown := Session{ID: e.SessionID, Date: e.Date, Day: (int(date.Weekday()) + 6) % 7, Kind: "walk", Title: "Занятие из прежнего дневника", Status: "planned", DetailsKnown: false}
			if index := slices.IndexFunc(w.Plan.Sessions, func(s Session) bool { return s.ID == e.SessionID }); index >= 0 {
				w.Plan.TotalMinutes -= w.Plan.Sessions[index].Minutes
				w.Plan.Sessions[index] = unknown
			} else {
				w.Plan.Sessions = append(w.Plan.Sessions, unknown)
			}
		}
		slices.SortFunc(w.Plan.Sessions, func(a, b Session) int { return strings.Compare(a.Date, b.Date) })
		r.Version = RecordVersion
		changed = true
	}
	if snapshot(r, currentID) != nil {
		return r, changed, nil
	}
	// Only already-known defaults are extended. No completed/missed facts appear.
	start := current
	for _, w := range r.Weeks {
		if w.WeekStart < currentID {
			date, err := time.ParseInLocation(time.DateOnly, w.WeekStart, now.Location())
			if err != nil {
				return Record{}, false, invalid("Повреждена сохранённая неделя.")
			}
			start = date.AddDate(0, 0, 7)
		}
	}
	for !start.After(current) {
		if snapshot(r, start.Format(time.DateOnly)) == nil {
			if err := appendWeek(&r, baseline(*r.Profile, start, "baseline")); err != nil {
				return Record{}, false, err
			}
			changed = true
		}
		start = start.AddDate(0, 0, 7)
	}
	return r, changed, nil
}

// EnsureWeeks is safe to call inside a repository transaction on every state
// read. Only a migration or calendar rollover advances the aggregate revision.
func EnsureWeeks(r Record, now time.Time) (Record, error) {
	result, changed, err := materialize(r, now)
	if err != nil {
		return Record{}, err
	}
	if changed {
		result.Revision++
	}
	return result, nil
}

// SetProfileAt freezes the current schedule before changing future defaults.
// The legacy SetProfile helper remains for pure preview/backward compatibility.
func SetProfileAt(r Record, revision uint64, p Profile, now time.Time) (Record, error) {
	if err := ValidateProfile(p); err != nil {
		return Record{}, err
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	first := r.Profile == nil
	var err error
	r, _, err = materialize(r, now)
	if err != nil {
		return Record{}, err
	}
	r, err = SetProfile(r, revision, p)
	if err != nil {
		return Record{}, err
	}
	r.Version = RecordVersion
	if first {
		if err := appendWeek(&r, baseline(*r.Profile, WeekStart(now), "baseline")); err != nil {
			return Record{}, err
		}
	}
	return r, nil
}

// ProposalToken covers the exact public proposal and explanation. Hash input is
// length-prefixed; strings cannot create ambiguous concatenations. hash.Hash
// writes never return errors, so no fallible serialization is needed here.
func ProposalToken(p Projection) string {
	if p.NextPlan == nil || p.Decision == nil {
		return ""
	}
	var b strings.Builder
	field := func(value string) { b.WriteString(strconv.Itoa(len(value))); b.WriteByte(':'); b.WriteString(value) }
	field(p.NextPlan.WeekStart)
	field(strconv.Itoa(p.NextPlan.TotalMinutes))
	for _, s := range p.NextPlan.Sessions {
		for _, v := range []string{s.ID, s.Date, strconv.Itoa(s.Day), s.Kind, s.Title, strconv.Itoa(s.Minutes)} {
			field(v)
		}
	}
	field(p.Decision.Mode)
	field(p.Decision.Title)
	for _, reason := range p.Decision.Reasons {
		field(reason)
	}
	for _, n := range []int{p.Decision.BeforeMinutes, p.Decision.AfterMinutes, p.Decision.BeforeSessions, p.Decision.AfterSessions} {
		field(strconv.Itoa(n))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func AcceptedNextPlan(r Record, now time.Time) *Plan {
	w := snapshot(r, WeekStart(now).AddDate(0, 0, 7).Format(time.DateOnly))
	if w == nil || w.Source != "accepted" {
		return nil
	}
	p := applySessionAdjustments(w.Plan, r.SessionAdjustments)
	return &p
}

func AcceptNextPlan(r Record, revision uint64, weekStart, token string, now time.Time) (Record, error) {
	if saved := snapshot(r, weekStart); saved != nil && saved.Source == "accepted" && token != "" && saved.AcceptedToken == token {
		return Clone(r), nil
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	if weekStart != WeekStart(now).AddDate(0, 0, 7).Format(time.DateOnly) {
		return Record{}, ErrPlanConflict
	}
	if snapshot(r, weekStart) != nil {
		return Record{}, ErrPlanConflict
	}
	var err error
	r, _, err = materialize(r, now)
	if err != nil {
		return Record{}, err
	}
	proposal := Project(r, now)
	if proposal.NextPlan == nil || token == "" || token != ProposalToken(proposal) {
		return Record{}, ErrPlanConflict
	}
	w := WeekSnapshot{WeekStart: weekStart, Source: "accepted", Profile: cloneProfile(r.Profile), Plan: clonePlan(*proposal.NextPlan), AcceptedToken: token, AcceptedAt: now.UTC().Format(time.RFC3339)}
	if err := appendWeek(&r, w); err != nil {
		return Record{}, err
	}
	r.Revision++
	return r, nil
}

func CancelAcceptedPlan(r Record, revision uint64, weekStart string, now time.Time) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	if weekStart != WeekStart(now).AddDate(0, 0, 7).Format(time.DateOnly) {
		return Record{}, ErrPlanConflict
	}
	w := snapshot(r, weekStart)
	if w == nil || w.Source != "accepted" {
		return Record{}, ErrNotFound
	}
	r = Clone(r)
	r.Weeks = slices.DeleteFunc(r.Weeks, func(w WeekSnapshot) bool { return w.WeekStart == weekStart })
	end := WeekStart(now).AddDate(0, 0, 14).Format(time.DateOnly)
	r.SessionAdjustments = slices.DeleteFunc(r.SessionAdjustments, func(a SessionAdjustment) bool { return a.Date >= weekStart && a.Date < end })
	r.Revision++
	return r, nil
}

func Week(r Record, weekStart string, now time.Time) (WeekView, error) {
	date, err := time.ParseInLocation(time.DateOnly, weekStart, now.Location())
	if err != nil || WeekStart(date).Format(time.DateOnly) != weekStart {
		return WeekView{}, invalid("Укажите дату понедельника в формате ГГГГ-ММ-ДД.")
	}
	w := snapshot(r, weekStart)
	if w == nil {
		return WeekView{}, ErrNotFound
	}
	view := WeekView{WeekStart: w.WeekStart, Source: w.Source, Profile: cloneProfile(w.Profile), Plan: applySessionAdjustments(w.Plan, r.SessionAdjustments), Entries: []Entry{}, EditableFrom: EditableFrom(now)}
	end := date.AddDate(0, 0, 7).Format(time.DateOnly)
	view.Editable = weekStart <= now.Format(time.DateOnly) && end > view.EditableFrom
	statuses := map[string]string{}
	for _, e := range r.Entries {
		if e.Date >= weekStart && e.Date < end {
			view.Entries = append(view.Entries, cloneEntries([]Entry{e})[0])
			if e.SessionID != "" {
				statuses[e.SessionID] = e.Status
			}
		}
	}
	for i := range view.Plan.Sessions {
		view.Plan.Sessions[i].Status = "planned"
		if status, ok := statuses[view.Plan.Sessions[i].ID]; ok {
			view.Plan.Sessions[i].Status = status
		}
	}
	return view, nil
}

func validateHistoricalEntry(e Entry, r Record, now time.Time) error {
	date, err := time.ParseInLocation(time.DateOnly, e.Date, now.Location())
	if err != nil {
		return invalid("Дата должна иметь формат ГГГГ-ММ-ДД.")
	}
	if e.Date < EditableFrom(now) {
		return invalid(fmt.Sprintf("Добавление и исправление записей доступны с %s (90 дней). Более ранние записи можно экспортировать или удалить.", EditableFrom(now)))
	}
	if e.Date > now.Format(time.DateOnly) {
		return invalid("Нельзя записать результат будущего дня.")
	}
	day := (int(date.Weekday()) + 6) % 7
	if err := ValidateEntry(e, Profile{Days: []int{day}}, date, true); err != nil {
		return err
	}
	w := snapshot(r, WeekStart(date).Format(time.DateOnly))
	if w == nil {
		return invalid("Для этой даты нет сохранённой недели. Нельзя задним числом выдумать расписание.")
	}
	if e.SessionID != "" && !slices.ContainsFunc(w.Plan.Sessions, func(s Session) bool { return s.ID == e.SessionID && s.Date == e.Date }) {
		return invalid("Для этой даты нет занятия в сохранённом расписании.")
	}
	return nil
}

func DeleteEntry(r Record, revision uint64, id string) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	if !slices.ContainsFunc(r.Entries, func(e Entry) bool { return e.ID == id }) {
		return Record{}, ErrNotFound
	}
	r = Clone(r)
	r.Entries = slices.DeleteFunc(r.Entries, func(e Entry) bool { return e.ID == id })
	r.Revision++
	return r, nil
}
