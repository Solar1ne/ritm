package httpapi

import (
	"context"
	"errors"

	"rhythm/internal/rhythm"
)

// calendarRecord advances saved calendar weeks when possible. A full archive
// must remain accessible through reads and authentication: reload the committed
// aggregate after a rejected rollover, and let state project current defaults
// without adding unsaved weeks to its history.
func (s *Server) calendarRecord(ctx context.Context, id string) (rhythm.Record, error) {
	record, err := s.repo.Update(ctx, id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.EnsureWeeks(current, s.now())
	})
	if errors.Is(err, rhythm.ErrCapacity) {
		return s.repo.Read(ctx, id)
	}
	return record, err
}
