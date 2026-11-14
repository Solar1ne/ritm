package httpapi

import (
	"net/http"

	"rhythm/internal/rhythm"
)

func (s *Server) week(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	current, err := s.repo.Update(r.Context(), id, func(record rhythm.Record) (rhythm.Record, error) { return rhythm.EnsureWeeks(record, s.now()) })
	if err != nil {
		s.failure(w, r, err)
		return
	}
	view, err := rhythm.Week(current, r.URL.Query().Get("weekStart"), s.now())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) acceptPlan(w http.ResponseWriter, r *http.Request, cancel bool) {
	var input struct {
		Revision      *uint64 `json:"revision"`
		WeekStart     string  `json:"weekStart"`
		NextPlanToken string  `json:"nextPlanToken,omitempty"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.WeekStart == "" {
		s.fail(w, 400, "validation", "Нужны revision и weekStart.")
		return
	}
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(record rhythm.Record) (rhythm.Record, error) {
		if cancel {
			return rhythm.CancelAcceptedPlan(record, *input.Revision, input.WeekStart, s.now())
		}
		return rhythm.AcceptNextPlan(record, *input.Revision, input.WeekStart, input.NextPlanToken, s.now())
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision *uint64 `json:"revision"`
		ID       string  `json:"id"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.ID == "" {
		s.fail(w, 400, "validation", "Нужны revision и id записи.")
		return
	}
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(record rhythm.Record) (rhythm.Record, error) {
		return rhythm.DeleteEntry(record, *input.Revision, input.ID)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	// A public export excludes the receipt ledger but includes a small derived
	// projection. This bound covers any valid 4 MiB persisted aggregate export.
	var input struct {
		Revision *uint64 `json:"revision"`
		Backup   *state  `json:"backup"`
	}
	if !s.decodeLimit(w, r, &input, 5*1024*1024) {
		return
	}
	if input.Revision == nil || input.Backup == nil {
		s.fail(w, 400, "validation", "Нужны revision и backup.")
		return
	}
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	backup := rhythm.Backup{ExportVersion: input.Backup.ExportVersion, Profile: input.Backup.Profile, Entries: input.Backup.Entries, Weeks: input.Backup.Weeks, Workouts: input.Backup.Workouts, Reminder: input.Backup.Reminder, SessionAdjustments: input.Backup.SessionAdjustments, HRVEntries: input.Backup.HRVEntries, BodyEntries: input.Backup.BodyEntries, MealSelections: input.Backup.MealSelections}
	updated, err := s.repo.Update(r.Context(), id, func(record rhythm.Record) (rhythm.Record, error) {
		return rhythm.Restore(record, *input.Revision, backup, s.now())
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}
