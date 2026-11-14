package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"rhythm/internal/calendar"
	"rhythm/internal/rhythm"
	"rhythm/internal/workoutimport"
)

func (s *Server) serveExtras(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "POST /api/import/preview":
		s.importWorkouts(w, r, true)
	case "POST /api/import":
		s.importWorkouts(w, r, false)
	case "DELETE /api/workouts":
		s.deleteWorkout(w, r)
	case "POST /api/reminders":
		s.reminder(w, r)
	case "GET /api/calendar":
		s.calendar(w, r)
	case "POST /api/assistant":
		s.answer(w, r)
	case "GET /api/ai/context":
		s.aiContext(w, r)
	case "POST /api/ai/respond":
		s.aiRespond(w, r)
	case "POST /api/sessions/variants":
		s.sessionVariant(w, r, true, false)
	case "POST /api/sessions/variant":
		s.sessionVariant(w, r, false, false)
	case "DELETE /api/sessions/variant":
		s.sessionVariant(w, r, false, true)
	case "POST /api/hrv":
		s.saveHRV(w, r)
	case "DELETE /api/hrv":
		s.deleteMeasurement(w, r, true)
	case "POST /api/body":
		s.saveBody(w, r)
	case "DELETE /api/body":
		s.deleteMeasurement(w, r, false)
	case "POST /api/meals":
		s.saveMeals(w, r, false)
	case "POST /api/meals/batch":
		s.saveMeals(w, r, true)
	case "DELETE /api/meals":
		s.deleteMeal(w, r)
	case "GET /api/nutrition/catalog":
		s.nutritionCatalog(w, r)
	case "POST /api/nutrition/preview":
		s.nutritionPreview(w, r)
	default:
		return false
	}
	return true
}

type importInput struct {
	Revision        *uint64 `json:"revision"`
	Format          string  `json:"format"`
	Content         string  `json:"content"`
	CompletePlanned bool    `json:"completePlanned"`
}
type importItem struct {
	rhythm.Workout
	Duplicate   bool `json:"duplicate"`
	CanComplete bool `json:"canComplete"`
}

func (s *Server) importWorkouts(w http.ResponseWriter, r *http.Request, preview bool) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input importInput
	if !s.decodeLimit(w, r, &input, 2*1024*1024) {
		return
	}
	if !preview && input.Revision == nil {
		s.fail(w, 400, "validation", "Нужно передать revision.")
		return
	}
	format := workoutimport.Format(input.Format)
	parsed, err := workoutimport.Parse(r.Context(), strings.NewReader(input.Content), format, s.location)
	if err != nil {
		s.fail(w, 400, "import_invalid", err.Error())
		return
	}
	workouts := make([]rhythm.Workout, len(parsed))
	for i, item := range parsed {
		sum := sha256.Sum256([]byte(item.ExternalID))
		kind := item.Kind
		if kind == "running" {
			kind = "run"
		}
		if kind == "walking" {
			kind = "walk"
		}
		workouts[i] = rhythm.Workout{ID: hex.EncodeToString(sum[:]), Date: item.StartedAt.In(s.location).Format(time.DateOnly), StartedAt: item.StartedAt.UTC().Format(time.RFC3339Nano), Kind: kind, Source: input.Format, Minutes: item.Duration.Minutes(), DistanceMeters: item.DistanceMeters}
	}
	now := s.now()
	if preview {
		current, err := s.repo.Read(r.Context(), id)
		if err != nil {
			s.failure(w, r, err)
			return
		}
		if _, err := rhythm.ImportWorkouts(current, current.Revision, workouts, input.CompletePlanned, now); err != nil {
			s.failure(w, r, err)
			return
		}
		items := make([]importItem, len(workouts))
		for i, item := range workouts {
			items[i] = importItem{Workout: item, Duplicate: slices.ContainsFunc(current.Workouts, func(old rhythm.Workout) bool { return old.ID == item.ID })}
			if item.Date >= rhythm.EditableFrom(now) {
				date, _ := time.ParseInLocation(time.DateOnly, item.Date, s.location)
				view, err := rhythm.Week(current, rhythm.WeekStart(date).Format(time.DateOnly), now)
				if err == nil {
					items[i].CanComplete = slices.ContainsFunc(view.Plan.Sessions, func(session rhythm.Session) bool { return session.ID == item.Date && session.Status == "planned" })
				}
			}
		}
		s.json(w, 200, struct {
			Workouts []importItem `json:"workouts"`
			Revision uint64       `json:"revision"`
		}{items, current.Revision})
		return
	}
	record, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.ImportWorkouts(current, *input.Revision, workouts, input.CompletePlanned, now)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) deleteWorkout(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision *uint64 `json:"revision"`
		ID       string  `json:"id"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.ID == "" {
		s.fail(w, 400, "validation", "Нужны revision и id.")
		return
	}
	record, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.DeleteWorkout(current, *input.Revision, input.ID)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) reminder(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision *uint64                  `json:"revision"`
		Reminder *rhythm.ReminderSettings `json:"reminder"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.Reminder == nil {
		s.fail(w, 400, "validation", "Нужны revision и reminder.")
		return
	}
	record, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.SetReminder(current, *input.Revision, *input.Reminder)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	record, err := s.repo.Read(r.Context(), id)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	weekStart := r.URL.Query().Get("weekStart")
	if weekStart == "" {
		weekStart = rhythm.WeekStart(s.now()).Format(time.DateOnly)
	}
	view, err := rhythm.Week(record, weekStart, s.now())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	var sessions []calendar.Session
	for _, session := range view.Plan.Sessions {
		if !session.DetailsKnown {
			continue
		}
		sessions = append(sessions, calendar.Session{ID: session.ID, Date: session.Date, Title: session.Title, Minutes: session.Minutes, Description: "Ритм · занятие из сохранённого плана. После занятия отметьте результат в приложении."})
	}
	if len(sessions) == 0 {
		s.fail(w, 400, "validation", "В этой неделе нет занятий с сохранённой длительностью.")
		return
	}
	clock := record.Reminder.Time
	if clock == "" {
		clock = "18:00"
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		s.fail(w, 400, "validation", "Сначала сохраните время в настройках напоминаний.")
		return
	}
	var alarm *time.Duration
	if record.Reminder.Enabled {
		duration := time.Duration(record.Reminder.BeforeMinutes) * time.Minute
		alarm = &duration
	}
	namespace := hex.EncodeToString(mac(s.secret, "calendar:"+id))
	content, err := calendar.Export(sessions, calendar.Options{Location: s.location, Hour: parsed.Hour(), Minute: parsed.Minute(), ReminderBefore: alarm, CalendarName: "Ритм · план недели", UIDNamespace: namespace, GeneratedAt: s.now(), Sequence: record.Revision})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rhythm-`+weekStart+`.ics"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	if _, err := w.Write(content); err != nil {
		s.logger.Warn("response interrupted", "kind", "calendar_write")
	}
}
