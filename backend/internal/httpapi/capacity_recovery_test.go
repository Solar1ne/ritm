package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func TestReadAndExportRemainAvailableAtWeekCapacity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		weeks       int
		advanceDays int
	}{
		{name: "full_history", weeks: rhythm.MaxWeeks, advanceDays: 7},
		{name: "rollover_exceeds_remaining_capacity", weeks: rhythm.MaxWeeks - 1, advanceDays: 14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setup(t, "")
			cookie := guest(t, s)
			r := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
			r.AddCookie(cookie)
			id, _, err := s.principal(r, false)
			if err != nil {
				t.Fatal(err)
			}
			firstWeek := testNow().AddDate(0, 0, -7*(tc.weeks-1))
			before, err := rhythm.SetProfileAt(rhythm.Blank(), 0, testProfile(), firstWeek)
			if err != nil {
				t.Fatal(err)
			}
			before, err = rhythm.EnsureWeeks(before, testNow())
			if err != nil {
				t.Fatal(err)
			}
			before, err = rhythm.AddEntry(before, before.Revision, rhythm.Entry{ID: "saved-fact", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}, testNow())
			if err != nil {
				t.Fatal(err)
			}
			if len(before.Weeks) != tc.weeks {
				t.Fatalf("fixture has %d weeks, want %d", len(before.Weeks), tc.weeks)
			}
			before, err = s.repo.Update(context.Background(), id, func(rhythm.Record) (rhythm.Record, error) { return before, nil })
			if err != nil {
				t.Fatal(err)
			}
			s.clock = func() time.Time { return testNow().AddDate(0, 0, tc.advanceDays) }
			for _, path := range []string{"/api/state", "/api/export"} {
				w := request(t, s, "GET", path, nil, cookie)
				got := requireState(t, w, 200)
				if got.Revision != before.Revision || !reflect.DeepEqual(got.Weeks, before.Weeks) || !reflect.DeepEqual(got.Entries, before.Entries) {
					t.Fatalf("%s changed saved history or revision", path)
				}
				if !reflect.DeepEqual(got.Projection, rhythm.Project(before, s.now())) || got.Plan.WeekStart != got.CurrentWeekStart {
					t.Fatalf("%s did not return the current read projection", path)
				}
				for _, week := range got.Weeks {
					if week.WeekStart == got.CurrentWeekStart {
						t.Fatalf("%s presented an unsaved current week as a saved snapshot", path)
					}
				}
				if path == "/api/export" && w.Header().Get("Content-Disposition") != `attachment; filename="rhythm-data.json"` {
					t.Fatal("export lost download headers")
				}
			}
			mutation := request(t, s, "POST", "/api/profile", map[string]any{"revision": before.Revision, "profile": testProfile()}, cookie)
			if mutation.Code != 413 {
				t.Fatalf("mutation status=%d want=413: %s", mutation.Code, mutation.Body.String())
			}
			var failure struct {
				Error struct{ Code string } `json:"error"`
			}
			if err := json.Unmarshal(mutation.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error.Code != "capacity" {
				t.Fatalf("mutation error=%q want=capacity", failure.Error.Code)
			}
			after, err := s.repo.Read(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatal("read, export or rejected mutation changed the persisted aggregate")
			}
		})
	}
}

type capacityReadRepository struct {
	Repository
	updateErr    error
	readErr      error
	reads        int
	beforeUpdate func(context.Context, string) error
}

func (r *capacityReadRepository) Read(ctx context.Context, id string) (rhythm.Record, error) {
	r.reads++
	if r.reads > 1 && r.readErr != nil {
		return rhythm.Record{}, r.readErr
	}
	return r.Repository.Read(ctx, id)
}

func (r *capacityReadRepository) Update(ctx context.Context, id string, _ func(rhythm.Record) (rhythm.Record, error)) (rhythm.Record, error) {
	if r.beforeUpdate != nil {
		if err := r.beforeUpdate(ctx, id); err != nil {
			return rhythm.Record{}, err
		}
	}
	return rhythm.Record{}, r.updateErr
}

func TestStateCapacityFallbackReadsLatestPersistedState(t *testing.T) {
	for _, path := range []string{"/api/state", "/api/export"} {
		t.Run(path, func(t *testing.T) {
			s, _ := setup(t, "")
			cookie := guest(t, s)
			before := saveProfile(t, s, cookie, 0)
			original := s.repo
			s.repo = &capacityReadRepository{
				Repository: original,
				updateErr:  rhythm.ErrCapacity,
				beforeUpdate: func(ctx context.Context, id string) error {
					// Another request resets the diary after the initial read.
					_, err := original.Update(ctx, id, func(current rhythm.Record) (rhythm.Record, error) {
						return rhythm.Reset(current, current.Revision)
					})
					return err
				},
			}
			got := requireState(t, request(t, s, "GET", path, nil, cookie), 200)
			if got.Revision != before.Revision+1 || got.Profile != nil || got.Plan != nil || len(got.Weeks) != 0 {
				t.Fatalf("fallback returned stale data after a concurrent reset: %+v", got)
			}
		})
	}
}

func TestStateCapacityFallbackPropagatesStorageFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		updateErr error
		readErr   error
	}{
		{name: "calendar_update_failed", updateErr: errors.New("storage unavailable")},
		{name: "fallback_read_failed", updateErr: rhythm.ErrCapacity, readErr: errors.New("storage unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setup(t, "")
			cookie := guest(t, s)
			saveProfile(t, s, cookie, 0)
			for _, path := range []string{"/api/state", "/api/export"} {
				original := s.repo
				s.repo = &capacityReadRepository{Repository: original, updateErr: tc.updateErr, readErr: tc.readErr}
				w := request(t, s, "GET", path, nil, cookie)
				s.repo = original
				if w.Code != 503 {
					t.Fatalf("%s status=%d want=503: %s", path, w.Code, w.Body.String())
				}
			}
		})
	}
}
