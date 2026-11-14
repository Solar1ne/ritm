package httpapi

import (
	"net/http"

	"rhythm/internal/assistant"
	"rhythm/internal/rhythm"
)

func assistantPlan(plan *rhythm.Plan) *assistant.Plan {
	if plan == nil {
		return nil
	}
	out := &assistant.Plan{WeekStart: plan.WeekStart, Sessions: make([]assistant.Session, 0, len(plan.Sessions))}
	for _, s := range plan.Sessions {
		out.Sessions = append(out.Sessions, assistant.Session{ID: s.ID, Date: s.Date, Title: s.Title, Status: s.Status, Minutes: s.Minutes, DetailsKnown: s.DetailsKnown})
	}
	return out
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Question string `json:"question"`
	}
	if !s.decode(w, r, &input) {
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
	projection := rhythm.Project(record, s.now())
	context := assistant.Context{Now: s.now(), Plan: assistantPlan(projection.Plan), NextPlan: assistantPlan(projection.NextPlan), Entries: make([]assistant.Entry, 0, len(record.Entries))}
	context.AcceptedNextPlan = assistantPlan(rhythm.AcceptedNextPlan(record, s.now()))
	if d := projection.Decision; d != nil {
		context.Decision = &assistant.Decision{Mode: d.Mode, Title: d.Title, Reasons: append([]string(nil), d.Reasons...), BeforeMinutes: d.BeforeMinutes, AfterMinutes: d.AfterMinutes, BeforeSessions: d.BeforeSessions, AfterSessions: d.AfterSessions}
	}
	for _, entry := range record.Entries {
		context.Entries = append(context.Entries, assistant.Entry{ID: entry.ID, Date: entry.Date, Status: entry.Status, Sleep: entry.Sleep, Fatigue: entry.Fatigue})
	}
	response, err := assistant.Answer(input.Question, context)
	if err != nil {
		s.fail(w, 400, "validation", err.Error())
		return
	}
	s.json(w, 200, response)
}
