package httpapi

import (
	"net/http"

	"rhythm/internal/rhythm"
)

func (s *Server) saveHRV(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision *uint64          `json:"revision"`
		Entry    *rhythm.HRVEntry `json:"entry"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.Entry == nil {
		s.fail(w, 400, "validation", "Нужны revision и entry.")
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.AddHRV(current, *input.Revision, *input.Entry, s.now())
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}

func (s *Server) saveBody(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision *uint64           `json:"revision"`
		Entry    *rhythm.BodyEntry `json:"entry"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.Entry == nil {
		s.fail(w, 400, "validation", "Нужны revision и entry.")
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.AddBody(current, *input.Revision, *input.Entry, s.now())
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}

func (s *Server) deleteMeasurement(w http.ResponseWriter, r *http.Request, hrv bool) {
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
	updated, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		if hrv {
			return rhythm.DeleteHRV(current, *input.Revision, input.ID)
		}
		return rhythm.DeleteBody(current, *input.Revision, input.ID)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}

func (s *Server) saveMeals(w http.ResponseWriter, r *http.Request, batch bool) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var revision *uint64
	var selections []rhythm.MealSelection
	if batch {
		var input struct {
			Revision   *uint64                `json:"revision"`
			Selections []rhythm.MealSelection `json:"selections"`
		}
		if !s.decode(w, r, &input) {
			return
		}
		revision, selections = input.Revision, input.Selections
	} else {
		var input struct {
			Revision  *uint64               `json:"revision"`
			Selection *rhythm.MealSelection `json:"selection"`
		}
		if !s.decode(w, r, &input) {
			return
		}
		revision = input.Revision
		if input.Selection != nil {
			selections = []rhythm.MealSelection{*input.Selection}
		}
	}
	if revision == nil || len(selections) == 0 {
		s.fail(w, 400, "validation", "Нужны revision и выбранные блюда.")
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.SetMeals(current, *revision, selections, s.now())
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}

func (s *Server) deleteMeal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision *uint64 `json:"revision"`
		Date     string  `json:"date"`
		Slot     string  `json:"slot"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.Date == "" || input.Slot == "" {
		s.fail(w, 400, "validation", "Нужны revision, date и slot.")
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.DeleteMeal(current, *input.Revision, input.Date, input.Slot)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}
