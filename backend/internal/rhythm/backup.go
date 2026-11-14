package rhythm

import (
	"encoding/hex"
	"reflect"
	"slices"
	"time"
)

// Backup contains only portable user data. Authentication, aggregate revisions,
// and operation receipts belong to the destination and are never imported.
type Backup struct {
	ExportVersion      int                 `json:"exportVersion"`
	Profile            *Profile            `json:"profile"`
	Entries            []Entry             `json:"entries"`
	Weeks              []WeekSnapshot      `json:"weeks"`
	Workouts           []Workout           `json:"workouts"`
	Reminder           ReminderSettings    `json:"reminder"`
	SessionAdjustments []SessionAdjustment `json:"sessionAdjustments,omitempty"`
	HRVEntries         []HRVEntry          `json:"hrvEntries,omitempty"`
	BodyEntries        []BodyEntry         `json:"bodyEntries,omitempty"`
	MealSelections     []MealSelection     `json:"mealSelections,omitempty"`
}

func Restore(target Record, revision uint64, backup Backup, now time.Time) (Record, error) {
	if revision != target.Revision {
		return Record{}, ErrConflict
	}
	if target.Profile != nil || len(target.Entries) > 0 || len(target.Weeks) > 0 || len(target.Workouts) > 0 || len(target.SessionAdjustments) > 0 || len(target.HRVEntries) > 0 || len(target.BodyEntries) > 0 || len(target.MealSelections) > 0 {
		return Record{}, invalid("Восстановление доступно только в пустом аккаунте. Текущие данные не будут заменены.")
	}
	if backup.ExportVersion != ExportVersion {
		return Record{}, invalid("Нужна резервная копия Ритма версии 2 из раздела экспорта.")
	}
	if backup.Profile == nil {
		return Record{}, invalid("В резервной копии нет профиля.")
	}
	r := Clone(Record{Version: RecordVersion, Profile: backup.Profile, Entries: backup.Entries, Weeks: backup.Weeks, Workouts: backup.Workouts, Reminder: backup.Reminder, SessionAdjustments: backup.SessionAdjustments, HRVEntries: backup.HRVEntries, BodyEntries: backup.BodyEntries, MealSelections: backup.MealSelections})
	if err := ValidateArchive(r, now); err != nil {
		return Record{}, err
	}
	var err error
	r, _, err = materialize(r, now)
	if err != nil {
		return Record{}, err
	}
	r.Revision = target.Revision + 1
	// Current facts establish only bounded retry protection in this account;
	// no external mutation ledger or session credential is trusted.
	r.Receipts = cloneEntries(r.Entries)
	r.HRVReceipts = slices.Clone(r.HRVEntries)
	r.BodyReceipts = cloneBodyEntries(r.BodyEntries)
	return r, nil
}

// ValidateArchive checks a complete exported graph, including totals, canonical
// schedule contents and every diary/workout reference, before any persistence.
func ValidateArchive(r Record, now time.Time) error {
	if r.Profile == nil {
		return invalid("В истории отсутствует профиль.")
	}
	if err := ValidateProfile(*r.Profile); err != nil {
		return err
	}
	if len(r.Entries) > MaxEntries || len(r.Weeks) > MaxWeeks {
		return ErrCapacity
	}
	if len(r.Weeks) == 0 {
		return invalid("В резервной копии нет сохранённого расписания.")
	}
	weeks := make(map[string]struct{}, len(r.Weeks))
	for _, w := range r.Weeks {
		if _, exists := weeks[w.WeekStart]; exists {
			return invalid("Неделя повторяется в резервной копии.")
		}
		weeks[w.WeekStart] = struct{}{}
		if err := validateSnapshot(w, now); err != nil {
			return err
		}
	}
	ids := make(map[string]struct{}, len(r.Entries))
	facts := make(map[string]struct{}, len(r.Entries))
	for _, e := range r.Entries {
		date, err := time.ParseInLocation(time.DateOnly, e.Date, now.Location())
		if err != nil || e.Date > now.Format(time.DateOnly) {
			return invalid("В резервной копии есть неверная или будущая дата дневника.")
		}
		if err := ValidateEntry(e, Profile{Days: []int{(int(date.Weekday()) + 6) % 7}}, date, true); err != nil {
			return err
		}
		if _, exists := ids[e.ID]; exists {
			return invalid("Идентификатор записи повторяется в резервной копии.")
		}
		ids[e.ID] = struct{}{}
		fact := "wellbeing:" + e.Date
		if e.SessionID != "" {
			fact = "session:" + e.SessionID
		}
		if _, exists := facts[fact]; exists {
			return invalid("Для одного дня или занятия есть конфликтующие записи.")
		}
		facts[fact] = struct{}{}
		w := snapshot(r, WeekStart(date).Format(time.DateOnly))
		if w == nil {
			return invalid("У записи дневника нет сохранённой недели.")
		}
		if e.SessionID != "" && !slices.ContainsFunc(w.Plan.Sessions, func(s Session) bool { return s.ID == e.SessionID && s.Date == e.Date }) {
			return invalid("Запись дневника ссылается на отсутствующее занятие.")
		}
	}
	return ValidateExtras(r, now)
}

func validateSnapshot(w WeekSnapshot, now time.Time) error {
	start, err := time.ParseInLocation(time.DateOnly, w.WeekStart, now.Location())
	if err != nil || WeekStart(start).Format(time.DateOnly) != w.WeekStart || w.Plan.WeekStart != w.WeekStart {
		return invalid("Некорректное начало сохранённой недели.")
	}
	current := WeekStart(now)
	if start.After(current) && (w.Source != "accepted" || !start.Equal(current.AddDate(0, 0, 7))) {
		return invalid("Будущее расписание может быть только утверждённой следующей неделей.")
	}
	if w.Source != "baseline" && w.Source != "accepted" && w.Source != "legacy" {
		return invalid("Неизвестный источник сохранённой недели.")
	}
	if w.Source == "accepted" {
		token, err := hex.DecodeString(w.AcceptedToken)
		if err != nil || len(token) != 32 {
			return invalid("У принятого плана некорректный идентификатор предложения.")
		}
		accepted, err := time.Parse(time.RFC3339, w.AcceptedAt)
		if err != nil || accepted.After(now) || !accepted.Before(start) {
			return invalid("Время принятия плана должно предшествовать неделе и не быть будущим.")
		}
	} else if w.AcceptedAt != "" || w.AcceptedToken != "" {
		return invalid("Только принятый план может содержать данные подтверждения.")
	}
	if len(w.Plan.Sessions) > 7 {
		return invalid("В неделе не может быть больше семи занятий.")
	}
	total := 0
	seen := map[string]bool{}
	for _, s := range w.Plan.Sessions {
		if s.VariantID != "" || s.Environment != "" || s.LowImpact || s.OriginalMinutes != 0 {
			return invalid("Выбранные варианты должны храниться отдельно от исходного расписания.")
		}
		if s.Day < 0 || s.Day > 6 || s.Date != start.AddDate(0, 0, s.Day).Format(time.DateOnly) || s.ID != s.Date || seen[s.ID] {
			return invalid("Дата, день или идентификатор занятия не соответствуют неделе.")
		}
		seen[s.ID] = true
		if s.Status != "planned" {
			return invalid("Статусы выполнения должны храниться в дневнике, а не в расписании.")
		}
		if s.DetailsKnown {
			if s.Minutes < 15 || s.Minutes > 90 {
				return invalid("Длительность сохранённого занятия должна быть 15–90 минут.")
			}
		} else if w.Source != "legacy" || s.Minutes != 0 || s.Kind != "walk" || s.Title != "Занятие из прежнего дневника" {
			return invalid("Неизвестные детали допустимы только для старой наблюдаемой истории.")
		}
		total += s.Minutes
	}
	if total != w.Plan.TotalMinutes {
		return invalid("Общее время недели не совпадает с занятиями.")
	}
	if w.Profile == nil {
		if w.Source != "legacy" {
			return invalid("У сохранённого расписания отсутствуют настройки профиля.")
		}
		if slices.ContainsFunc(w.Plan.Sessions, func(s Session) bool { return s.DetailsKnown }) {
			return invalid("Известные детали занятия требуют сохранённых настроек профиля.")
		}
		return nil
	}
	if err := ValidateProfile(*w.Profile); err != nil {
		return err
	}
	comparison := clonePlan(w.Plan)
	if w.Source == "legacy" {
		comparison.Sessions = slices.DeleteFunc(comparison.Sessions, func(s Session) bool { return !s.DetailsKnown })
	}
	p := *w.Profile
	days := slices.Clone(p.Days)
	slices.Sort(days)
	possible := []Plan{makePlan(p, days, p.Minutes, start, false)}
	if w.Source == "accepted" {
		possible = append(possible, makePlan(p, days[:max(1, len(days)-1)], p.Minutes, start, false), makePlan(p, days, min(90, p.Minutes*105/100), start, false))
		// Recovery may be lower than the profile-derived ceiling because it
		// also respects the known current schedule and explicit variants.
		// Rebuild the canonical plan to keep dates, kind and titles trusted.
		count := len(w.Plan.Sessions)
		if count > 0 && count <= max(1, len(days)-1) {
			minutes := w.Plan.Sessions[0].Minutes
			if minutes >= 15 && minutes <= max(15, p.Minutes*80/100) {
				possible = append(possible, makePlan(p, days[:count], minutes, start, true))
			}
		}
	}
	// Canonical plans also validate kind/title without accepting arbitrary HTML
	// or caller-supplied exercise descriptions from a backup.
	for _, expected := range possible {
		if w.Source == "legacy" {
			expected.Sessions = slices.DeleteFunc(expected.Sessions, func(s Session) bool {
				return slices.ContainsFunc(w.Plan.Sessions, func(observed Session) bool { return observed.ID == s.ID && !observed.DetailsKnown })
			})
			expected.TotalMinutes = 0
			for _, s := range expected.Sessions {
				expected.TotalMinutes += s.Minutes
			}
		}
		if reflect.DeepEqual(expected, comparison) {
			return nil
		}
	}
	return invalid("Состав сохранённого плана не соответствует его настройкам и правилам Ритма.")
}
