package httpapi

import (
	"net/http"

	"rhythm/internal/rhythm"
)

func (s *Server) sessionVariant(w http.ResponseWriter, r *http.Request, preview, revert bool) {
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision    *uint64                   `json:"revision"`
		SessionID   string                    `json:"sessionId"`
		VariantID   string                    `json:"variantId"`
		Constraints rhythm.SessionConstraints `json:"constraints"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.SessionID == "" {
		s.fail(w, 400, "validation", "Нужны revision и занятие.")
		return
	}
	now := s.now()
	if preview {
		record, err := s.repo.Read(r.Context(), id)
		if err != nil {
			s.failure(w, r, err)
			return
		}
		if record.Revision != *input.Revision {
			s.failure(w, r, rhythm.ErrConflict)
			return
		}
		value, err := rhythm.PreviewSessionVariants(record, input.SessionID, input.Constraints, now)
		if err != nil {
			s.failure(w, r, err)
			return
		}
		s.json(w, 200, struct {
			Revision uint64 `json:"revision"`
			rhythm.SessionVariantPreview
		}{record.Revision, value})
		return
	}
	updated, err := s.repo.Update(r.Context(), id, func(record rhythm.Record) (rhythm.Record, error) {
		if revert {
			return rhythm.RevertSessionVariant(record, *input.Revision, input.SessionID, now)
		}
		return rhythm.ApplySessionVariant(record, *input.Revision, input.SessionID, input.VariantID, input.Constraints, now)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(updated, id))
}
