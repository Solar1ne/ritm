package rhythm_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"rhythm/internal/rat"
	"rhythm/internal/rhythm"
)

func now() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.FixedZone("Moscow", 3*3600)) }
func base() rhythm.Profile {
	return rhythm.Profile{Name: "Анна", Goal: "tone", Experience: "beginner", Days: []int{0, 2, 4}, Minutes: 45}
}
func ptr[T any](v T) *T { return &v }

func TestRATFixedScenarios(t *testing.T) {
	for _, tc := range rat.Cases(now()) {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := rhythm.Preview(tc.Profile, tc.Entries, now())
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision.Mode != tc.Mode || len(got.NextPlan.Sessions) != tc.Sessions || got.NextPlan.TotalMinutes != tc.Minutes {
				t.Fatalf("decision=%+v; want mode=%s sessions=%d minutes=%d", got.Decision, tc.Mode, tc.Sessions, tc.Minutes)
			}
			if got.Plan.TotalMinutes != 135 {
				t.Fatalf("baseline changed: %d", got.Plan.TotalMinutes)
			}
			if got.NextPlan.WeekStart != "2026-10-19" {
				t.Fatalf("proposal week=%s", got.NextPlan.WeekStart)
			}
		})
	}
}

func TestProfileValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*rhythm.Profile)
	}{
		{"duplicate_days", func(p *rhythm.Profile) { p.Days = []int{0, 0} }},
		{"negative_day", func(p *rhythm.Profile) { p.Days = []int{-1} }},
		{"invalid_day", func(p *rhythm.Profile) { p.Days = []int{7} }},
		{"no_days", func(p *rhythm.Profile) { p.Days = nil }},
		{"too_many_days", func(p *rhythm.Profile) { p.Days = []int{0, 1, 2, 3, 4, 5} }},
		{"short_duration", func(p *rhythm.Profile) { p.Minutes = 14 }},
		{"long_duration", func(p *rhythm.Profile) { p.Minutes = 91 }},
		{"unknown_goal", func(p *rhythm.Profile) { p.Goal = "weight-loss" }},
		{"unknown_experience", func(p *rhythm.Profile) { p.Experience = "athlete" }},
		{"long_unicode_name", func(p *rhythm.Profile) { p.Name = strings.Repeat("я", 41) }},
		{"control_in_name", func(p *rhythm.Profile) { p.Name = "Анна\nИмя" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			tc.mutate(&p)
			if rhythm.ValidateProfile(p) == nil {
				t.Fatal("accepted invalid profile")
			}
		})
	}
	p := base()
	p.Name = strings.Repeat("я", 40)
	if err := rhythm.ValidateProfile(p); err != nil {
		t.Fatal(err)
	}
}

func TestEntryValidation(t *testing.T) {
	valid := rhythm.Entry{ID: "entry-1", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}
	for _, tc := range []struct {
		name   string
		mutate func(*rhythm.Entry)
	}{
		{"future", func(e *rhythm.Entry) { e.Date = "2026-10-18"; e.SessionID = e.Date }},
		{"previous_week", func(e *rhythm.Entry) { e.Date = "2026-10-09"; e.SessionID = e.Date }},
		{"not_scheduled", func(e *rhythm.Entry) { e.Date = "2026-10-15"; e.SessionID = e.Date }},
		{"different_session", func(e *rhythm.Entry) { e.SessionID = "2026-10-14" }},
		{"no_session", func(e *rhythm.Entry) { e.SessionID = "" }},
		{"invalid_status", func(e *rhythm.Entry) { e.Status = "success" }},
		{"invalid_id", func(e *rhythm.Entry) { e.ID = "../record" }},
		{"no_id", func(e *rhythm.Entry) { e.ID = "" }},
		{"negative_sleep", func(e *rhythm.Entry) { e.Sleep = ptr(-1.0) }},
		{"excess_sleep", func(e *rhythm.Entry) { e.Sleep = ptr(24.1) }},
		{"low_fatigue", func(e *rhythm.Entry) { e.Fatigue = ptr(0) }},
		{"high_fatigue", func(e *rhythm.Entry) { e.Fatigue = ptr(11) }},
		{"wellbeing_no_metric", func(e *rhythm.Entry) { e.Status = "wellbeing"; e.SessionID = "" }},
		{"wellbeing_session", func(e *rhythm.Entry) { e.Status = "wellbeing"; e.Sleep = ptr(8.0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := valid
			tc.mutate(&e)
			if rhythm.ValidateEntry(e, base(), now(), false) == nil {
				t.Fatal("accepted invalid entry")
			}
		})
	}
	if err := rhythm.ValidateEntry(valid, base(), now(), false); err != nil {
		t.Fatal(err)
	}
	wellbeing := rhythm.Entry{ID: "well", Date: "2026-10-17", Status: "wellbeing", Sleep: ptr(0.0)}
	if err := rhythm.ValidateEntry(wellbeing, base(), now(), false); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotencyCorrectionAndRevision(t *testing.T) {
	r, err := rhythm.SetProfile(rhythm.Blank(), 0, base())
	if err != nil {
		t.Fatal(err)
	}
	e := rhythm.Entry{ID: "fact-1", Date: "2026-10-16", SessionID: "2026-10-16", Status: "missed"}
	r, err = rhythm.AddEntry(r, 1, e, now())
	if err != nil {
		t.Fatal(err)
	}
	retry, err := rhythm.AddEntry(r, 0, e, now().AddDate(0, 0, 14))
	if err != nil || retry.Revision != 2 {
		t.Fatalf("stale retry: %v %+v", err, retry)
	}
	conflict := e
	conflict.Status = "done"
	if _, err := rhythm.AddEntry(r, 2, conflict, now()); !errors.Is(err, rhythm.ErrEntryConflict) {
		t.Fatalf("expected ID conflict: %v", err)
	}
	conflict.ID = "fact-2"
	if _, err := rhythm.AddEntry(r, 1, conflict, now()); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("expected revision conflict: %v", err)
	}
	r, err = rhythm.AddEntry(r, 2, conflict, now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 1 || r.Entries[0].Status != "done" || r.Revision != 3 {
		t.Fatalf("correction doubled facts: %+v", r)
	}
	r, err = rhythm.AddEntry(r, 1, e, now())
	if err != nil || r.Entries[0].Status != "done" {
		t.Fatalf("retry reverted correction: %v %+v", err, r)
	}
	reset, err := rhythm.Reset(r, 3)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Profile != nil || len(reset.Entries) != 0 || len(reset.Receipts) != 0 || reset.Revision != 4 {
		t.Fatalf("reset failed: %+v", reset)
	}
}

func TestOwnershipAndScheduleChanges(t *testing.T) {
	p := base()
	r, err := rhythm.SetProfile(rhythm.Blank(), 0, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Days[0] = 6
	if r.Profile.Days[0] != 0 {
		t.Fatal("profile aliases input days")
	}
	e := rhythm.Entry{ID: "done", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done", Sleep: ptr(8.0), Fatigue: ptr(2)}
	r, err = rhythm.AddEntry(r, 1, e, now())
	if err != nil {
		t.Fatal(err)
	}
	*e.Sleep = 1
	if *r.Entries[0].Sleep != 8 {
		t.Fatal("record aliases input metric")
	}
	p = base()
	p.Days = []int{0, 2}
	r, err = rhythm.SetProfile(r, 2, p)
	if err != nil {
		t.Fatal(err)
	}
	got := rhythm.Project(r, now())
	if len(got.Plan.Sessions) != 3 || got.Plan.Sessions[2].Status != "done" || got.Plan.Sessions[2].Minutes != 45 {
		t.Fatal("profile change rewrote the saved current schedule")
	}
	one := rhythm.Project(r, now())
	two := rhythm.Project(r, now())
	if !reflect.DeepEqual(one, two) {
		t.Fatal("proposal accumulates across reads")
	}
}

func TestLatestWellbeingAndBounds(t *testing.T) {
	p := base()
	p.Days = []int{0}
	p.Minutes = 15
	entries := []rhythm.Entry{
		{ID: "early", Date: "2026-10-12", Status: "wellbeing", Fatigue: ptr(9)},
		{ID: "late", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(2), Sleep: ptr(8.0)},
	}
	projection, err := rhythm.Preview(p, entries, now())
	if err != nil {
		t.Fatal(err)
	}
	if projection.Decision.Mode != "maintain" {
		t.Fatal("old fatigue dominates latest wellbeing")
	}
	projection, err = rhythm.Preview(p, entries[:1], now())
	if err != nil {
		t.Fatal(err)
	}
	if projection.NextPlan.TotalMinutes != 15 || len(projection.NextPlan.Sessions) != 1 {
		t.Fatal("recovery violated minimum")
	}
	p.Minutes = 90
	complete := []rhythm.Entry{{ID: "done", Date: "2026-10-12", SessionID: "2026-10-12", Status: "done", Fatigue: ptr(2), Sleep: ptr(8.0)}}
	projection, err = rhythm.Preview(p, complete, now())
	if err != nil {
		t.Fatal(err)
	}
	if projection.NextPlan.TotalMinutes != 90 || projection.Decision.Mode != "maintain" {
		t.Fatal("progress exceeded maximum")
	}
	p.Minutes = 15
	projection, err = rhythm.Preview(p, complete, now())
	if err != nil {
		t.Fatal(err)
	}
	if projection.NextPlan.TotalMinutes != 15 || projection.Decision.Mode != "maintain" {
		t.Fatal("rounding a proposal to unchanged duration should not claim progress")
	}
}

func TestFuturePreviewOnlyAndDuplicateFacts(t *testing.T) {
	p := base()
	monday := now().AddDate(0, 0, -5)
	e := rhythm.Entry{ID: "future", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}
	if rhythm.ValidateEntry(e, p, monday, false) == nil {
		t.Fatal("future real fact accepted")
	}
	if _, err := rhythm.Preview(p, []rhythm.Entry{e, e, e}, monday); err != nil {
		t.Fatal(err)
	}
	r := rhythm.Record{Profile: &p, Entries: []rhythm.Entry{e, e, e}}
	if got := rhythm.Project(r, now()); got.Decision.Mode != "maintain" {
		t.Fatal("duplicate facts simulate full completion")
	}
}

func TestWeekStartAcrossYearAndTimezone(t *testing.T) {
	for _, tc := range []struct{ date, want string }{{"2027-01-01", "2026-12-28"}, {"2026-10-18", "2026-10-12"}, {"2026-10-19", "2026-10-19"}} {
		date, err := time.Parse(time.DateOnly, tc.date)
		if err != nil {
			t.Fatal(err)
		}
		if got := rhythm.WeekStart(date).Format(time.DateOnly); got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.date, got, tc.want)
		}
	}
}

func TestHistoryBoundedAndWellbeingCorrections(t *testing.T) {
	r, err := rhythm.SetProfile(rhythm.Blank(), 0, base())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rhythm.MaxEntries+5; i++ {
		e := rhythm.Entry{ID: strings.Repeat("x", 1) + time.Unix(int64(i), 0).Format("150405"), Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(3)}
		r, err = rhythm.AddEntry(r, r.Revision, e, now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Entries) != 1 || len(r.Receipts) != rhythm.MaxEntries {
		t.Fatalf("history unbounded or correction duplicated: %d %d", len(r.Entries), len(r.Receipts))
	}
}

func TestIndependentSignalsCannotMaskRecovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []rhythm.Entry
	}{
		{"same_day_partial_session", []rhythm.Entry{
			{ID: "sleep", Date: "2026-10-16", Status: "wellbeing", Sleep: ptr(5.0)},
			{ID: "session", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done", Fatigue: ptr(3)},
		}},
		{"later_day_partial_metric", []rhythm.Entry{
			{ID: "fatigue", Date: "2026-10-16", Status: "wellbeing", Fatigue: ptr(8)},
			{ID: "sleep", Date: "2026-10-17", Status: "wellbeing", Sleep: ptr(8.0)},
		}},
		{"backfill_cannot_replace_newer_sleep", []rhythm.Entry{
			{ID: "bad-later", Date: "2026-10-17", Status: "wellbeing", Sleep: ptr(5.0)},
			{ID: "backfill", Date: "2026-10-15", Status: "wellbeing", Sleep: ptr(8.0), Fatigue: ptr(3)},
		}},
		{"same_fact_partial_correction", []rhythm.Entry{
			{ID: "first", Date: "2026-10-17", Status: "wellbeing", Sleep: ptr(5.0)},
			{ID: "correction", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(3)},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rhythm.Preview(base(), tc.entries, now())
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision.Mode != "recover" || got.NextPlan.TotalMinutes != 72 {
				t.Fatalf("partial signal suppressed recovery: %+v", got.Decision)
			}
		})
	}
	// A newer actual value for the same signal can resolve the old alarm.
	entries := []rhythm.Entry{
		{ID: "old", Date: "2026-10-16", Status: "wellbeing", Sleep: ptr(5.0)},
		{ID: "new", Date: "2026-10-17", Status: "wellbeing", Sleep: ptr(8.0)},
	}
	got, err := rhythm.Preview(base(), entries, now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision.Mode != "maintain" {
		t.Fatal("new explicit sleep did not supersede old sleep")
	}
}

func TestPartialCorrectionRemainsIdempotent(t *testing.T) {
	r, err := rhythm.SetProfile(rhythm.Blank(), 0, base())
	if err != nil {
		t.Fatal(err)
	}
	first := rhythm.Entry{ID: "first", Date: "2026-10-17", Status: "wellbeing", Sleep: ptr(5.0)}
	r, err = rhythm.AddEntry(r, r.Revision, first, now())
	if err != nil {
		t.Fatal(err)
	}
	partial := rhythm.Entry{ID: "partial", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(3)}
	r, err = rhythm.AddEntry(r, r.Revision, partial, now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 1 || r.Entries[0].Sleep == nil || *r.Entries[0].Sleep != 5 {
		t.Fatal("partial correction erased sleep")
	}
	retried, err := rhythm.AddEntry(r, 1, partial, now())
	if err != nil || retried.Revision != r.Revision {
		t.Fatalf("retry of partial correction: %v", err)
	}
	if got := rhythm.Project(retried, now()); got.Decision.Mode != "recover" {
		t.Fatal("retry removed recovery")
	}
}
