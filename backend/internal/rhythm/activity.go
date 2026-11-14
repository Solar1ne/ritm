package rhythm

import (
	"math"
	"slices"
	"strings"
	"time"
)

const MaxWorkouts = 500

// Workout is an observed activity, independent of the intended schedule.
// SessionID records an explicit link; deleting an activity never erases a
// separately confirmed diary entry.
type Workout struct {
	ID             string   `json:"id"`
	Date           string   `json:"date"`
	StartedAt      string   `json:"startedAt"`
	Kind           string   `json:"kind"`
	Source         string   `json:"source"`
	Minutes        float64  `json:"minutes"`
	DistanceMeters *float64 `json:"distanceMeters,omitempty"`
	SessionID      string   `json:"sessionId,omitempty"`
}

type ReminderSettings struct {
	Enabled       bool   `json:"enabled"`
	Time          string `json:"time"`
	BeforeMinutes int    `json:"beforeMinutes"`
}

func cloneWorkouts(items []Workout) []Workout {
	out := slices.Clone(items)
	for i := range out {
		if out[i].DistanceMeters != nil {
			value := *out[i].DistanceMeters
			out[i].DistanceMeters = &value
		}
	}
	return out
}

func validWorkout(w Workout, now time.Time) error {
	if len(w.ID) != 64 || strings.IndexFunc(w.ID, func(c rune) bool { return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') }) >= 0 {
		return invalid("Некорректный идентификатор тренировки.")
	}
	date, err := time.ParseInLocation(time.DateOnly, w.Date, now.Location())
	if err != nil || date.Year() < 1970 || w.Date > now.Format(time.DateOnly) {
		return invalid("Дата тренировки должна существовать и не быть будущей.")
	}
	start, err := time.Parse(time.RFC3339, w.StartedAt)
	if err != nil || start.In(now.Location()).Format(time.DateOnly) != w.Date {
		return invalid("Время тренировки не соответствует её дате.")
	}
	if !slices.Contains([]string{"run", "walk", "cycling", "swimming", "strength", "mobility", "other"}, w.Kind) {
		return invalid("Неизвестный тип тренировки.")
	}
	if w.Source != "csv" && w.Source != "tcx" {
		return invalid("Неизвестный источник тренировки.")
	}
	if math.IsNaN(w.Minutes) || math.IsInf(w.Minutes, 0) || w.Minutes <= 0 || w.Minutes > 1440 {
		return invalid("Длительность тренировки — больше нуля и не больше суток.")
	}
	if w.DistanceMeters != nil && (math.IsNaN(*w.DistanceMeters) || math.IsInf(*w.DistanceMeters, 0) || *w.DistanceMeters < 0 || *w.DistanceMeters > 1000000) {
		return invalid("Некорректная дистанция тренировки.")
	}
	if w.SessionID != "" && w.SessionID != w.Date {
		return invalid("Связь с планом должна относиться к дню тренировки.")
	}
	return nil
}

func ValidateReminder(value ReminderSettings) error {
	if value.Time == "" && !value.Enabled && value.BeforeMinutes == 0 {
		return nil
	} // Legacy empty settings.
	if _, err := time.Parse("15:04", value.Time); err != nil || len(value.Time) != 5 {
		return invalid("Укажите время в формате ЧЧ:ММ.")
	}
	if !slices.Contains([]int{0, 5, 15, 30, 60}, value.BeforeMinutes) {
		return invalid("Напоминание возможно в начале занятия или за 5, 15, 30, 60 минут.")
	}
	return nil
}

// ValidateExtras is also used for backup restoration; it never trusts linked
// session IDs from the imported document.
func ValidateExtras(r Record, now time.Time) error {
	if err := validateWellness(r, now); err != nil {
		return err
	}
	if err := validateMeals(r, now); err != nil {
		return err
	}
	if err := ValidateReminder(r.Reminder); err != nil {
		return err
	}
	if err := validateSessionAdjustments(r, now); err != nil {
		return err
	}
	if len(r.Workouts) > MaxWorkouts {
		return ErrCapacity
	}
	seen := make(map[string]struct{}, len(r.Workouts))
	for _, w := range r.Workouts {
		if err := validWorkout(w, now); err != nil {
			return err
		}
		if _, ok := seen[w.ID]; ok {
			return invalid("Повтор идентификатора тренировки в резервной копии.")
		}
		seen[w.ID] = struct{}{}
		if w.SessionID != "" {
			found := false
			for _, week := range r.Weeks {
				if slices.ContainsFunc(week.Plan.Sessions, func(s Session) bool { return s.ID == w.SessionID && s.Date == w.Date }) {
					found = true
					break
				}
			}
			if !found {
				return invalid("Связанное занятие отсутствует в истории расписаний.")
			}
		}
	}
	return nil
}

func SetReminder(r Record, revision uint64, value ReminderSettings) (Record, error) {
	if err := ValidateReminder(value); err != nil {
		return Record{}, err
	}
	if r.Profile == nil {
		return Record{}, invalid("Сначала создайте свой план.")
	}
	if value == r.Reminder {
		return Clone(r), nil
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	r = Clone(r)
	r.Reminder = value
	r.Revision++
	return r, nil
}

func sameWorkout(a, b Workout) bool {
	// A link is a separately confirmed action, not part of source-file identity.
	return a.ID == b.ID && a.Date == b.Date && a.StartedAt == b.StartedAt && a.Kind == b.Kind && a.Source == b.Source && a.Minutes == b.Minutes && sameOptional(a.DistanceMeters, b.DistanceMeters)
}

// ImportWorkouts validates the whole batch before returning an updated copy.
// The repository commits that copy once. Replaying unchanged data is a no-op,
// including when a prior HTTP response was lost.
func ImportWorkouts(input Record, revision uint64, workouts []Workout, completePlanned bool, now time.Time) (Record, error) {
	if input.Profile == nil {
		return Record{}, invalid("Сначала создайте свой план.")
	}
	if len(workouts) == 0 || len(workouts) > 100 {
		return Record{}, invalid("Выберите файл с 1–100 тренировками.")
	}
	r, _, err := materialize(input, now)
	if err != nil {
		return Record{}, err
	}
	changed := false
	for _, incoming := range workouts {
		if incoming.SessionID != "" {
			return Record{}, invalid("Связь тренировки с планом определяется только после подтверждения.")
		}
		if err := validWorkout(incoming, now); err != nil {
			return Record{}, err
		}
		index := slices.IndexFunc(r.Workouts, func(w Workout) bool { return w.ID == incoming.ID })
		if index >= 0 {
			if !sameWorkout(r.Workouts[index], incoming) {
				return Record{}, ErrEntryConflict
			}
		} else {
			if len(r.Workouts) >= MaxWorkouts {
				return Record{}, ErrCapacity
			}
			r.Workouts = append(r.Workouts, cloneWorkouts([]Workout{incoming})[0])
			index = len(r.Workouts) - 1
			changed = true
		}
		if completePlanned && r.Workouts[index].SessionID == "" && incoming.Date >= EditableFrom(now) {
			date, _ := time.ParseInLocation(time.DateOnly, incoming.Date, now.Location()) // validated above
			w := snapshot(r, WeekStart(date).Format(time.DateOnly))
			if w != nil && slices.ContainsFunc(w.Plan.Sessions, func(s Session) bool { return s.ID == incoming.Date }) {
				// Respect an explicit existing missed/done decision. Import never
				// silently changes an already recorded status.
				entry := slices.IndexFunc(r.Entries, func(e Entry) bool { return e.SessionID == incoming.Date })
				if entry >= 0 && r.Entries[entry].Status == "missed" {
					continue
				}
				if entry < 0 {
					e := Entry{ID: "import-" + incoming.ID, Date: incoming.Date, SessionID: incoming.Date, Status: "done"}
					r, err = AddEntry(r, r.Revision, e, now)
					if err != nil {
						return Record{}, err
					}
					// A retained receipt may deliberately suppress a replay after
					// the user deleted this diary fact. Never invent a link then.
					if !slices.ContainsFunc(r.Entries, func(e Entry) bool { return e.SessionID == incoming.Date && e.Status == "done" }) {
						continue
					}
				}
				r.Workouts[index].SessionID = incoming.Date
				changed = true
			}
		}
	}
	if !changed {
		return Clone(input), nil
	}
	if input.Revision != revision {
		return Record{}, ErrConflict
	}
	r.Revision = input.Revision + 1
	return r, nil
}

func DeleteWorkout(r Record, revision uint64, id string) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	index := slices.IndexFunc(r.Workouts, func(w Workout) bool { return w.ID == id })
	if index < 0 {
		return Clone(r), nil
	}
	r = Clone(r)
	r.Workouts = slices.Delete(r.Workouts, index, index+1)
	r.Revision++
	return r, nil
}
