package rhythm_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"rhythm/internal/rhythm"
)

func activityFixture(id int, date string) rhythm.Workout {
	return rhythm.Workout{ID: fmt.Sprintf("%064x", id), Date: date, StartedAt: date + "T09:00:00+03:00", Kind: "run", Source: "csv", Minutes: 30, DistanceMeters: ptr(4000.0)}
}

func TestImportWorkoutsAtomicRetryAndExplicitCompletion(t *testing.T) {
	initial := createTimed(t, now())
	batch := []rhythm.Workout{activityFixture(1, "2026-10-14"), activityFixture(2, "2026-10-16")}
	saved, err := rhythm.ImportWorkouts(initial, initial.Revision, batch, false, now())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != initial.Revision+1 || len(saved.Workouts) != 2 || len(saved.Entries) != 0 {
		t.Fatal("import must commit one revision without implying plan completion")
	}
	if len(initial.Workouts) != 0 || len(initial.Entries) != 0 {
		t.Fatal("import mutated its input aggregate")
	}
	retry, err := rhythm.ImportWorkouts(saved, 0, batch, false, now())
	if err != nil || !reflect.DeepEqual(retry, saved) {
		t.Fatalf("unchanged retry was not idempotent: %v", err)
	}

	before := rhythm.Clone(saved)
	conflict := batch[0]
	conflict.Minutes++
	if _, err := rhythm.ImportWorkouts(saved, saved.Revision, []rhythm.Workout{activityFixture(3, "2026-10-12"), conflict}, true, now()); !errors.Is(err, rhythm.ErrEntryConflict) {
		t.Fatalf("mixed new/conflicting batch: %v", err)
	}
	if !reflect.DeepEqual(before, saved) {
		t.Fatal("failed batch leaked a workout, completion, receipt or revision")
	}
	if _, err := rhythm.ImportWorkouts(saved, 0, []rhythm.Workout{activityFixture(3, "2026-10-12")}, false, now()); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}

	completed, err := rhythm.ImportWorkouts(saved, saved.Revision, batch, true, now())
	if err != nil {
		t.Fatal(err)
	}
	if completed.Revision != saved.Revision+1 || len(completed.Entries) != 2 || len(completed.Receipts) != 2 {
		t.Fatal("two explicit completions were not committed as one operation")
	}
	for _, workout := range completed.Workouts {
		if workout.SessionID != workout.Date {
			t.Fatal("confirmed completion missing its schedule link")
		}
	}
	retry, err = rhythm.ImportWorkouts(completed, saved.Revision, batch, true, now())
	if err != nil || !reflect.DeepEqual(retry, completed) {
		t.Fatalf("completion retry duplicated facts: %v", err)
	}
	*batch[0].DistanceMeters = 1
	if *completed.Workouts[0].DistanceMeters != 4000 {
		t.Fatal("stored activity aliases the caller's distance pointer")
	}
}

func TestImportRespectsMissedAndDeletedFacts(t *testing.T) {
	r := createTimed(t, now())
	var err error
	r, err = rhythm.AddEntry(r, r.Revision, rhythm.Entry{ID: "manual-miss", Date: "2026-10-16", SessionID: "2026-10-16", Status: "missed"}, now())
	if err != nil {
		t.Fatal(err)
	}
	batch := []rhythm.Workout{activityFixture(1, "2026-10-14"), activityFixture(2, "2026-10-16")}
	r, err = rhythm.ImportWorkouts(r, r.Revision, batch, true, now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 2 || r.Workouts[1].SessionID != "" || r.Entries[0].Status != "missed" {
		t.Fatal("import overwrote an explicit missed decision")
	}
	completionID := r.Entries[1].ID
	r, err = rhythm.DeleteWorkout(r, r.Revision, batch[0].ID)
	if err != nil || len(r.Entries) != 2 {
		t.Fatalf("deleting an activity erased a separately confirmed diary fact: %v", err)
	}
	r, err = rhythm.DeleteEntry(r, r.Revision, completionID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = rhythm.ImportWorkouts(r, r.Revision, batch[:1], true, now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 1 || r.Workouts[1].SessionID != "" {
		t.Fatal("replaying a retained receipt resurrected a deleted fact or invented a link")
	}
}

func TestImportRejectsInvalidBatchWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*rhythm.Workout)
	}{
		{"id", func(w *rhythm.Workout) { w.ID = "../../other" }},
		{"invalid_date", func(w *rhythm.Workout) { w.Date = "2026-02-31" }},
		{"future_date", func(w *rhythm.Workout) { w.Date = "2026-10-18"; w.StartedAt = "2026-10-18T09:00:00+03:00" }},
		{"before_epoch", func(w *rhythm.Workout) { w.Date = "1969-12-31"; w.StartedAt = "1969-12-31T09:00:00+03:00" }},
		{"different_local_date", func(w *rhythm.Workout) { w.StartedAt = "2026-10-16T23:00:00Z" }},
		{"unknown_kind", func(w *rhythm.Workout) { w.Kind = "unknown" }},
		{"unknown_source", func(w *rhythm.Workout) { w.Source = "remote" }},
		{"zero_duration", func(w *rhythm.Workout) { w.Minutes = 0 }},
		{"too_long", func(w *rhythm.Workout) { w.Minutes = 1440.01 }},
		{"nan_duration", func(w *rhythm.Workout) { w.Minutes = math.NaN() }},
		{"infinite_duration", func(w *rhythm.Workout) { w.Minutes = math.Inf(1) }},
		{"negative_distance", func(w *rhythm.Workout) { w.DistanceMeters = ptr(-1.0) }},
		{"too_far", func(w *rhythm.Workout) { w.DistanceMeters = ptr(1000000.01) }},
		{"nan_distance", func(w *rhythm.Workout) { w.DistanceMeters = ptr(math.NaN()) }},
		{"caller_supplied_link", func(w *rhythm.Workout) { w.SessionID = w.Date }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := createTimed(t, now())
			before := rhythm.Clone(r)
			invalid := activityFixture(2, "2026-10-16")
			tc.mutate(&invalid)
			if _, err := rhythm.ImportWorkouts(r, r.Revision, []rhythm.Workout{activityFixture(1, "2026-10-14"), invalid}, true, now()); err == nil {
				t.Fatal("invalid batch was accepted")
			}
			if !reflect.DeepEqual(r, before) {
				t.Fatal("failed batch changed caller-owned data")
			}
		})
	}
}

func TestImportExactCapacityAndBatchLimits(t *testing.T) {
	r := createTimed(t, now())
	for i := 1; i < rhythm.MaxWorkouts; i++ {
		r.Workouts = append(r.Workouts, activityFixture(i, "2026-10-16"))
	}
	last := activityFixture(rhythm.MaxWorkouts, "2026-10-16")
	full, err := rhythm.ImportWorkouts(r, r.Revision, []rhythm.Workout{last}, false, now())
	if err != nil || len(full.Workouts) != rhythm.MaxWorkouts {
		t.Fatalf("exact capacity rejected: %v", err)
	}
	if _, err := rhythm.ImportWorkouts(full, full.Revision, []rhythm.Workout{activityFixture(rhythm.MaxWorkouts+1, "2026-10-16")}, false, now()); !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatalf("overflow should not silently prune: %v", err)
	}
	if retry, err := rhythm.ImportWorkouts(full, 0, []rhythm.Workout{last}, false, now()); err != nil || !reflect.DeepEqual(retry, full) {
		t.Fatalf("full aggregate cannot replay its last accepted import: %v", err)
	}
	for _, count := range []int{0, 100, 101} {
		batch := make([]rhythm.Workout, count)
		for i := range batch {
			batch[i] = activityFixture(i+1, "2026-10-16")
		}
		blank := createTimed(t, now())
		got, err := rhythm.ImportWorkouts(blank, blank.Revision, batch, false, now())
		if (err == nil) != (count == 100) || err == nil && len(got.Workouts) != count {
			t.Fatalf("batch size %d: count=%d err=%v", count, len(got.Workouts), err)
		}
	}
}

func TestReminderValidationRevisionAndRetry(t *testing.T) {
	r := createTimed(t, now())
	value := rhythm.ReminderSettings{Enabled: true, Time: "00:30", BeforeMinutes: 60}
	saved, err := rhythm.SetReminder(r, r.Revision, value)
	if err != nil || saved.Revision != r.Revision+1 || saved.Reminder != value {
		t.Fatalf("save reminder: %v", err)
	}
	if retry, err := rhythm.SetReminder(saved, 0, value); err != nil || !reflect.DeepEqual(retry, saved) {
		t.Fatalf("unchanged reminder retry: %v", err)
	}
	disabled := value
	disabled.Enabled = false
	if _, err := rhythm.SetReminder(saved, 0, disabled); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("stale consent change: %v", err)
	}
	for _, invalid := range []rhythm.ReminderSettings{{Enabled: true}, {Time: "24:00"}, {Time: "9:30"}, {Time: "12:00", BeforeMinutes: -5}, {Time: "12:00", BeforeMinutes: 10}} {
		if _, err := rhythm.SetReminder(saved, saved.Revision, invalid); err == nil {
			t.Fatalf("invalid reminder accepted: %+v", invalid)
		}
	}
	if _, err := rhythm.SetReminder(rhythm.Blank(), 0, value); err == nil {
		t.Fatal("reminder enabled without a profile")
	}
}
