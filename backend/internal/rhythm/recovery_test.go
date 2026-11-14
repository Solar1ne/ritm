package rhythm_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func TestRecoveryRespectsCurrentScheduleAndFutureProfile(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		currentDays, futureDays            []int
		currentMinutes, futureMinutes      int
		variants                           []int
		wantCount, wantMinutes, wantBefore int
	}{
		{name: "unchanged", currentDays: []int{0, 2, 4}, currentMinutes: 45, wantCount: 2, wantMinutes: 36, wantBefore: 135},
		{name: "shortened variants", currentDays: []int{0, 2, 4}, currentMinutes: 45, variants: []int{15, 15, 15}, wantCount: 2, wantMinutes: 15, wantBefore: 45},
		{name: "different variant durations", currentDays: []int{0, 2, 4}, currentMinutes: 45, variants: []int{15, 30, 45}, wantCount: 2, wantMinutes: 24, wantBefore: 90},
		{name: "average rounds once", currentDays: []int{0, 2, 4}, currentMinutes: 45, variants: []int{20, 45, 45}, wantCount: 2, wantMinutes: 29, wantBefore: 110},
		{name: "larger future profile", currentDays: []int{0}, currentMinutes: 15, futureDays: []int{0, 1, 2, 3, 4}, futureMinutes: 90, wantCount: 1, wantMinutes: 15, wantBefore: 15},
		{name: "smaller future profile", currentDays: []int{0, 2, 4}, currentMinutes: 45, futureDays: []int{1}, futureMinutes: 30, wantCount: 1, wantMinutes: 24, wantBefore: 135},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			p.Days, p.Minutes = tc.currentDays, tc.currentMinutes
			r, err := rhythm.SetProfileAt(rhythm.Blank(), 0, p, variantTime())
			if err != nil {
				t.Fatal(err)
			}
			original := rhythm.Clone(r).Weeks
			for i, minutes := range tc.variants {
				date := r.Weeks[0].Plan.Sessions[i].ID
				r, err = rhythm.ApplySessionVariant(r, r.Revision, date, "home-mobility-v1", variantConstraints(minutes, "home", true), variantTime())
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.futureDays != nil {
				p.Days, p.Minutes = tc.futureDays, tc.futureMinutes
				r, err = rhythm.SetProfileAt(r, r.Revision, p, variantTime())
				if err != nil {
					t.Fatal(err)
				}
			}
			r, err = rhythm.AddEntry(r, r.Revision, rhythm.Entry{ID: "fatigue", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(9)}, now())
			if err != nil {
				t.Fatal(err)
			}
			before := rhythm.Clone(r)
			got := rhythm.Project(r, now())
			if got.Decision.Mode != "recover" || got.Decision.BeforeMinutes != tc.wantBefore || len(got.NextPlan.Sessions) != tc.wantCount || got.NextPlan.TotalMinutes != tc.wantCount*tc.wantMinutes {
				t.Fatalf("recovery=%+v next=%+v; want before=%d, %d sessions of %d minutes", got.Decision, got.NextPlan, tc.wantBefore, tc.wantCount, tc.wantMinutes)
			}
			for i, session := range got.NextPlan.Sessions {
				if session.Kind != "mobility" || session.Minutes != tc.wantMinutes || session.Day != p.Days[i] {
					t.Fatalf("recovery session=%+v", session)
				}
			}
			if !reflect.DeepEqual(original, r.Weeks) || !reflect.DeepEqual(before, r) || !reflect.DeepEqual(got, rhythm.Project(r, now())) {
				t.Fatal("projection mutated or compounded the saved schedule")
			}
			if tc.wantBefore == 15 && !strings.Contains(strings.Join(got.Decision.Reasons, " "), "15 минут") {
				t.Fatal("minimum duration is not explained")
			}
			accepted, err := rhythm.AcceptNextPlan(r, r.Revision, got.NextPlan.WeekStart, rhythm.ProposalToken(got), now())
			if err != nil {
				t.Fatal(err)
			}
			if err := rhythm.ValidateArchive(accepted, now()); err != nil {
				t.Fatalf("accepted recovery cannot be exported/restored: %v", err)
			}
			backup := rhythm.Backup{ExportVersion: rhythm.ExportVersion, Profile: accepted.Profile, Entries: accepted.Entries, Weeks: accepted.Weeks, SessionAdjustments: accepted.SessionAdjustments}
			restored, err := rhythm.Restore(rhythm.Blank(), 0, backup, now())
			if err != nil {
				t.Fatal(err)
			}
			monday := time.Date(2026, 10, 19, 12, 0, 0, 0, now().Location())
			restored, err = rhythm.EnsureWeeks(restored, monday)
			if err != nil {
				t.Fatal(err)
			}
			if current := rhythm.Project(restored, monday); current.Plan.TotalMinutes != tc.wantCount*tc.wantMinutes || len(current.Plan.Sessions) != tc.wantCount {
				t.Fatalf("rollover lost accepted recovery: %+v", current.Plan)
			}
		})
	}
}

func TestRecoveryDoesNotTreatUnknownLegacyMinutesAsZeroLoad(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		observed                           []string
		wantBefore, wantCount, wantMinutes int
	}{
		{"all unknown", []string{"2026-10-12", "2026-10-14", "2026-10-16"}, 0, 2, 36},
		{"partly unknown", []string{"2026-10-12"}, 90, 1, 36},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			legacy := rhythm.Record{Profile: &p}
			for _, date := range tc.observed {
				legacy.Entries = append(legacy.Entries, rhythm.Entry{ID: date, Date: date, SessionID: date, Status: "done"})
			}
			legacy.Entries = append(legacy.Entries, rhythm.Entry{ID: "fatigue", Date: "2026-10-17", Status: "wellbeing", Fatigue: ptr(9)})
			r, err := rhythm.EnsureWeeks(legacy, now())
			if err != nil {
				t.Fatal(err)
			}
			got := rhythm.Project(r, now())
			if got.Decision.Mode != "recover" || got.Decision.BeforeMinutes != tc.wantBefore || len(got.NextPlan.Sessions) != tc.wantCount || got.NextPlan.TotalMinutes != tc.wantCount*tc.wantMinutes {
				t.Fatalf("legacy recovery=%+v next=%+v", got.Decision, got.NextPlan)
			}
			if !strings.Contains(strings.Join(got.Decision.Reasons, " "), "неизвестн") {
				t.Fatal("unknown current duration is hidden from the explanation")
			}
		})
	}
}

func TestFutureProfileRemainsBaselineOutsideRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, status, wantMode string
		sleep                  *float64
		fatigue                *int
		wantMinutes            int
	}{
		{name: "start", wantMode: "start", wantMinutes: 120},
		{name: "maintain", status: "wellbeing", sleep: ptr(6.5), fatigue: ptr(5), wantMode: "maintain", wantMinutes: 120},
		{name: "progress", status: "done", sleep: ptr(8.0), fatigue: ptr(3), wantMode: "progress", wantMinutes: 126},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			p.Days, p.Minutes = []int{0}, 15
			r, err := rhythm.SetProfileAt(rhythm.Blank(), 0, p, variantTime())
			if err != nil {
				t.Fatal(err)
			}
			future := base()
			future.Minutes = 40
			r, err = rhythm.SetProfileAt(r, r.Revision, future, variantTime())
			if err != nil {
				t.Fatal(err)
			}
			if tc.status != "" {
				e := rhythm.Entry{ID: "entry", Date: "2026-10-12", Status: tc.status, Sleep: tc.sleep, Fatigue: tc.fatigue}
				if e.Status == "done" {
					e.SessionID = e.Date
				}
				r, err = rhythm.AddEntry(r, r.Revision, e, now())
				if err != nil {
					t.Fatal(err)
				}
			}
			got := rhythm.Project(r, now())
			if got.Decision.Mode != tc.wantMode || got.Plan.TotalMinutes != 15 || got.NextPlan.TotalMinutes != tc.wantMinutes || len(got.NextPlan.Sessions) != 3 {
				t.Fatalf("future profile contract changed: %+v %+v", got.Decision, got.NextPlan)
			}
		})
	}
}
