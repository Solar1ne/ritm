package rhythm_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func createTimed(t *testing.T, at time.Time) rhythm.Record {
	t.Helper()
	r, err := rhythm.SetProfileAt(rhythm.Blank(), 0, base(), at)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWeeklyRolloverAndProfileIsolation(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted_%t", accepted), func(t *testing.T) {
			r := createTimed(t, now())
			e := rhythm.Entry{ID: "fatigue", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(9)}
			var err error
			r, err = rhythm.AddEntry(r, r.Revision, e, now())
			if err != nil {
				t.Fatal(err)
			}
			if accepted {
				p := rhythm.Project(r, now())
				r, err = rhythm.AcceptNextPlan(r, r.Revision, p.NextPlan.WeekStart, rhythm.ProposalToken(p), now())
				if err != nil {
					t.Fatal(err)
				}
			}
			original := rhythm.Clone(r).Weeks[0]
			changed := base()
			changed.Days = []int{1, 3}
			changed.Minutes = 30
			changed.Goal = "strength"
			r, err = rhythm.SetProfileAt(r, r.Revision, changed, now())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, r.Weeks[0]) {
				t.Fatal("editing profile rewrote current snapshot")
			}
			if got := rhythm.Project(r, now()); got.Plan.TotalMinutes != 135 {
				t.Fatal("editing defaults changed current duration")
			}
			if accepted && rhythm.AcceptedNextPlan(r, now()).TotalMinutes != 72 {
				t.Fatal("editing defaults rewrote approved next week")
			}
			before := r.Revision
			sunday := time.Date(2026, 10, 18, 23, 59, 59, 0, now().Location())
			r, err = rhythm.EnsureWeeks(r, sunday)
			if err != nil {
				t.Fatal(err)
			}
			if r.Revision != before {
				t.Fatal("ordinary read advanced revision")
			}
			monday := sunday.Add(time.Second)
			r, err = rhythm.EnsureWeeks(r, monday)
			if err != nil {
				t.Fatal(err)
			}
			projection := rhythm.Project(r, monday)
			want := 60
			if accepted {
				want = 72
			}
			if projection.Plan.WeekStart != "2026-10-19" || projection.Plan.TotalMinutes != want {
				t.Fatalf("rollover=%+v want %d", projection.Plan, want)
			}
			if len(r.Weeks) != 2 || !reflect.DeepEqual(original, r.Weeks[0]) {
				t.Fatal("rollover changed old schedule")
			}
			again, err := rhythm.EnsureWeeks(r, monday.Add(12*time.Hour))
			if err != nil || again.Revision != r.Revision {
				t.Fatal("repeated read wrote another revision")
			}
			if _, err := rhythm.CancelAcceptedPlan(r, r.Revision, "2026-10-19", monday); !errors.Is(err, rhythm.ErrPlanConflict) {
				t.Fatalf("started week was cancelable: %v", err)
			}
		})
	}
}

func TestAcceptProposalStalenessIdempotencyAndCancellation(t *testing.T) {
	r := createTimed(t, now())
	proposal := rhythm.Project(r, now())
	token := rhythm.ProposalToken(proposal)
	for _, tc := range []struct {
		name        string
		revision    uint64
		week, token string
	}{
		{"old_revision", 0, proposal.NextPlan.WeekStart, token},
		{"wrong_week", r.Revision, "2026-10-26", token},
		{"wrong_token", r.Revision, proposal.NextPlan.WeekStart, "bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rhythm.AcceptNextPlan(r, tc.revision, tc.week, tc.token, now()); err == nil {
				t.Fatal("accepted stale proposal")
			}
		})
	}
	accepted, err := rhythm.AcceptNextPlan(r, r.Revision, proposal.NextPlan.WeekStart, token, now())
	if err != nil {
		t.Fatal(err)
	}
	retry, err := rhythm.AcceptNextPlan(accepted, 0, proposal.NextPlan.WeekStart, token, now())
	if err != nil || retry.Revision != accepted.Revision {
		t.Fatal("exact repeat was not idempotent")
	}
	if _, err := rhythm.AcceptNextPlan(accepted, accepted.Revision, proposal.NextPlan.WeekStart, "different", now()); !errors.Is(err, rhythm.ErrPlanConflict) {
		t.Fatal("approved schedule replaced without cancellation")
	}
	cancelled, err := rhythm.CancelAcceptedPlan(accepted, accepted.Revision, proposal.NextPlan.WeekStart, now())
	if err != nil {
		t.Fatal(err)
	}
	if rhythm.AcceptedNextPlan(cancelled, now()) != nil || len(cancelled.Weeks) != 1 {
		t.Fatal("cancellation kept future snapshot")
	}
	if _, err := rhythm.AcceptNextPlan(cancelled, r.Revision, proposal.NextPlan.WeekStart, token, now()); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatal("old request reaccepted canceled plan")
	}
	if _, err := rhythm.AcceptNextPlan(cancelled, cancelled.Revision, proposal.NextPlan.WeekStart, token, now()); err != nil {
		t.Fatal(err)
	}
	// A changed signal invalidates a proposal token even with the newest revision.
	entry := rhythm.Entry{ID: "changed", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(9)}
	changed, err := rhythm.AddEntry(r, r.Revision, entry, now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rhythm.AcceptNextPlan(changed, changed.Revision, proposal.NextPlan.WeekStart, token, now()); !errors.Is(err, rhythm.ErrPlanConflict) {
		t.Fatal("accepted an outdated explanation/plan")
	}
}

func TestHistoricEntriesUseSavedSchedule(t *testing.T) {
	oldNow := now().AddDate(0, 0, -14)
	r := createTimed(t, oldNow)
	changed := base()
	changed.Days = []int{1}
	changed.Minutes = 20
	var err error
	r, err = rhythm.SetProfileAt(r, r.Revision, changed, oldNow)
	if err != nil {
		t.Fatal(err)
	}
	r, err = rhythm.EnsureWeeks(r, now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Weeks) != 3 {
		t.Fatalf("gap not materialized: %d", len(r.Weeks))
	}
	e := rhythm.Entry{ID: "historic", Date: "2026-10-02", SessionID: "2026-10-02", Status: "done"}
	r, err = rhythm.AddEntry(r, r.Revision, e, now())
	if err != nil {
		t.Fatal(err)
	}
	view, err := rhythm.Week(r, "2026-09-28", now())
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Sessions[2].Status != "done" || view.Plan.Sessions[2].Minutes != 45 {
		t.Fatal("past completion used new duration")
	}
	if rhythm.Project(r, now()).Decision.Mode != "start" {
		t.Fatal("old diary altered current-week decision")
	}
	wrong := rhythm.Entry{ID: "wrong", Date: "2026-09-29", SessionID: "2026-09-29", Status: "done"}
	if _, err := rhythm.AddEntry(r, r.Revision, wrong, now()); err == nil {
		t.Fatal("new profile days rewrote old schedule validation")
	}
	e.ID = "correction"
	e.Status = "missed"
	r, err = rhythm.AddEntry(r, r.Revision, e, now())
	if err != nil {
		t.Fatal(err)
	}
	view, err = rhythm.Week(r, "2026-09-28", now())
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Sessions[2].Status != "missed" || len(view.Entries) != 1 {
		t.Fatal("historic correction duplicated a session")
	}
	r, err = rhythm.DeleteEntry(r, r.Revision, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err = rhythm.Week(r, "2026-09-28", now())
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Sessions[2].Status != "planned" || len(view.Entries) != 0 {
		t.Fatal("delete did not restore planned state")
	}
	retry, err := rhythm.AddEntry(r, 0, e, now())
	if err != nil || len(retry.Entries) != 0 {
		t.Fatal("retry resurrected a deleted fact")
	}
}

func TestHistoryWindowAndNoSilentFactPruning(t *testing.T) {
	r := createTimed(t, now().AddDate(0, 0, -91))
	var err error
	r, err = rhythm.EnsureWeeks(r, now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		days    int
		allowed bool
	}{{-91, false}, {-90, true}, {0, true}, {1, false}} {
		e := rhythm.Entry{ID: fmt.Sprintf("day%d", -tc.days+1), Date: now().AddDate(0, 0, tc.days).Format(time.DateOnly), Status: "wellbeing", Sleep: ptr(8.0)}
		_, err := rhythm.AddEntry(r, r.Revision, e, now())
		if (err == nil) != tc.allowed {
			t.Fatalf("days=%d allowed=%t err=%v", tc.days, tc.allowed, err)
		}
	}
	full := rhythm.Clone(r)
	full.Entries = make([]rhythm.Entry, rhythm.MaxEntries)
	for i := range full.Entries {
		full.Entries[i] = rhythm.Entry{ID: fmt.Sprintf("old-%d", i), Date: now().AddDate(0, 0, -i-200).Format(time.DateOnly), Status: "wellbeing", Sleep: ptr(8.0)}
	}
	before := rhythm.Clone(full)
	if _, err := rhythm.AddEntry(full, full.Revision, rhythm.Entry{ID: "new", Date: now().Format(time.DateOnly), Status: "wellbeing", Sleep: ptr(8.0)}, now()); err == nil {
		t.Fatal("overflow silently pruned a fact")
	}
	if !reflect.DeepEqual(before, full) {
		t.Fatal("failed overflow mutated caller")
	}
	deleted, err := rhythm.DeleteEntry(full, full.Revision, "old-0")
	if err != nil || len(deleted.Entries) != rhythm.MaxEntries-1 {
		t.Fatal("old history cannot be explicitly deleted")
	}
}

func TestLegacyMigrationPreservesUnknownHistory(t *testing.T) {
	p := base()
	p.Days = []int{0, 2}
	legacy := rhythm.Record{Revision: 4, Profile: &p, Entries: []rhythm.Entry{
		{ID: "past", Date: "2026-10-09", SessionID: "2026-10-09", Status: "done"},
		{ID: "unmatched-current", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"},
	}}
	r, err := rhythm.EnsureWeeks(legacy, now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 5 || len(r.Entries) != 2 || len(r.Weeks) != 2 {
		t.Fatalf("migration lost history: %+v", r)
	}
	past, err := rhythm.Week(r, "2026-10-05", now())
	if err != nil {
		t.Fatal(err)
	}
	if past.Profile != nil || len(past.Plan.Sessions) != 1 || past.Plan.Sessions[0].DetailsKnown || past.Plan.Sessions[0].Minutes != 0 {
		t.Fatal("migration fabricated old training details")
	}
	current := rhythm.Project(r, now())
	if current.Plan.TotalMinutes != 90 || current.Plan.Sessions[2].DetailsKnown || current.Decision.Mode != "maintain" {
		t.Fatalf("unknown observed session influenced baseline: %+v", current)
	}
	if err := rhythm.ValidateArchive(r, now()); err != nil {
		t.Fatalf("migrated record cannot be exported/restored: %v", err)
	}
	again, err := rhythm.EnsureWeeks(r, now())
	if err != nil || again.Revision != r.Revision {
		t.Fatal("migration is not idempotent")
	}
	if len(legacy.Weeks) != 0 || legacy.Revision != 4 {
		t.Fatal("migration mutated legacy caller")
	}
}

func TestSnapshotOwnership(t *testing.T) {
	r := createTimed(t, now())
	copy := rhythm.Clone(r)
	copy.Weeks[0].Profile.Days[0] = 6
	copy.Weeks[0].Plan.Sessions[0].Title = "mutated"
	if r.Weeks[0].Profile.Days[0] != 0 || r.Weeks[0].Plan.Sessions[0].Title == "mutated" {
		t.Fatal("cloned snapshot aliases source")
	}
	view, err := rhythm.Week(r, r.Weeks[0].WeekStart, now())
	if err != nil {
		t.Fatal(err)
	}
	view.Profile.Days[0] = 6
	view.Plan.Sessions[0].Minutes = 90
	if r.Weeks[0].Profile.Days[0] != 0 || r.Weeks[0].Plan.Sessions[0].Minutes != 45 {
		t.Fatal("week view aliases aggregate")
	}
	projection := rhythm.Project(r, now())
	projection.Plan.Sessions[0].Minutes = 90
	if r.Weeks[0].Plan.Sessions[0].Minutes != 45 {
		t.Fatal("projection aliases snapshot")
	}
}

func TestLegacyMatchingDateDoesNotInventOldDurationOrKind(t *testing.T) {
	p := base()
	p.Goal = "endurance"
	p.Minutes = 90
	legacy := rhythm.Record{Profile: &p, Entries: []rhythm.Entry{{ID: "previously-45-strength", Date: "2026-10-12", SessionID: "2026-10-12", Status: "done"}}}
	r, err := rhythm.EnsureWeeks(legacy, now())
	if err != nil {
		t.Fatal(err)
	}
	current := rhythm.Project(r, now())
	first := current.Plan.Sessions[0]
	if first.DetailsKnown || first.Minutes != 0 || first.Title != "Занятие из прежнего дневника" || first.Status != "done" {
		t.Fatalf("migration invented facts from new profile: %+v", first)
	}
	if current.Plan.TotalMinutes != 180 || !current.Plan.Sessions[1].DetailsKnown {
		t.Fatal("unobserved baseline sessions should remain known")
	}
	if err := rhythm.ValidateArchive(r, now()); err != nil {
		t.Fatalf("mixed legacy snapshot cannot round-trip: %v", err)
	}
	backup := rhythm.Backup{ExportVersion: 2, Profile: r.Profile, Entries: r.Entries, Weeks: r.Weeks}
	restored, err := rhythm.Restore(rhythm.Blank(), 0, backup, now())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Weeks[0].Plan.Sessions[0].DetailsKnown {
		t.Fatal("restore invented unknown details")
	}
}

func TestBackupRoundTripValidationAndDestinationOwnership(t *testing.T) {
	r := createTimed(t, now())
	var err error
	r, err = rhythm.AddEntry(r, r.Revision, rhythm.Entry{ID: "fact", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done", Sleep: ptr(8.0)}, now())
	if err != nil {
		t.Fatal(err)
	}
	proposal := rhythm.Project(r, now())
	r, err = rhythm.AcceptNextPlan(r, r.Revision, proposal.NextPlan.WeekStart, rhythm.ProposalToken(proposal), now())
	if err != nil {
		t.Fatal(err)
	}
	backup := rhythm.Backup{ExportVersion: 2, Profile: r.Profile, Entries: r.Entries, Weeks: r.Weeks}
	target := rhythm.Blank()
	target.Revision = 19
	restored, err := rhythm.Restore(target, 19, backup, now())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != 20 || !reflect.DeepEqual(restored.Entries, r.Entries) || !reflect.DeepEqual(restored.Weeks, r.Weeks) {
		t.Fatal("round trip changed user data or imported foreign revision")
	}
	backup.Profile.Days[0] = 6
	backup.Weeks[0].Plan.Sessions[0].Minutes = 90
	if restored.Profile.Days[0] != 0 || restored.Weeks[0].Plan.Sessions[0].Minutes != 45 {
		t.Fatal("restore aliases caller backup")
	}
	if _, err := rhythm.Restore(restored, restored.Revision, backup, now()); err == nil {
		t.Fatal("restore overwrote nonempty target")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*rhythm.Backup)
	}{
		{"wrong_version", func(b *rhythm.Backup) { b.ExportVersion = 1 }},
		{"bad_total", func(b *rhythm.Backup) { b.Weeks[0].Plan.TotalMinutes++ }},
		{"html_title", func(b *rhythm.Backup) { b.Weeks[0].Plan.Sessions[0].Title = "<img src=x onerror=alert(1)>" }},
		{"unknown_kind", func(b *rhythm.Backup) { b.Weeks[0].Plan.Sessions[0].Kind = "untrusted" }},
		{"fake_done", func(b *rhythm.Backup) { b.Weeks[0].Plan.Sessions[0].Status = "done" }},
		{"duplicate_fact", func(b *rhythm.Backup) { b.Entries = append(b.Entries, b.Entries[0]) }},
		{"unmatched_fact", func(b *rhythm.Backup) { b.Entries[0].Date = "2026-10-15"; b.Entries[0].SessionID = "2026-10-15" }},
		{"accepted_in_future", func(b *rhythm.Backup) { b.Weeks[1].AcceptedAt = now().Add(time.Hour).Format(time.RFC3339) }},
		{"legacy_known_without_profile", func(b *rhythm.Backup) {
			b.Weeks[0].Source = "legacy"
			b.Weeks[0].Profile = nil
			b.Weeks[0].Plan.Sessions[0].Title = "untrusted"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := rhythm.Clone(restored)
			b := rhythm.Backup{ExportVersion: 2, Profile: copy.Profile, Entries: copy.Entries, Weeks: copy.Weeks}
			tc.mutate(&b)
			if _, err := rhythm.Restore(rhythm.Blank(), 0, b, now()); err == nil {
				t.Fatal("invalid backup accepted")
			}
		})
	}
}

func TestRestoreRejectsRolloverBeyondCapacityBeforeReturningRecord(t *testing.T) {
	old := now().AddDate(0, 0, -7*rhythm.MaxWeeks)
	r := createTimed(t, old)
	backup := rhythm.Backup{ExportVersion: rhythm.ExportVersion, Profile: r.Profile, Entries: r.Entries, Weeks: r.Weeks}
	target := rhythm.Blank()
	target.Revision = 9
	if _, err := rhythm.Restore(target, target.Revision, backup, now()); !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatalf("restore must reject an aggregate that cannot reach the current week: %v", err)
	}
	if target.Revision != 9 || target.Profile != nil || len(target.Weeks) != 0 || len(backup.Weeks) != 1 {
		t.Fatal("failed restore mutated destination or backup")
	}
}
