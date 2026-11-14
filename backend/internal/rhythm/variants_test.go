package rhythm_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func variantTime() time.Time { return now().AddDate(0, 0, -5) }

func variantConstraints(minutes int, environment string, lowImpact bool) rhythm.SessionConstraints {
	return rhythm.SessionConstraints{AvailableMinutes: minutes, Environment: environment, LowImpact: lowImpact}
}

func TestSessionVariantPreviewAndApplyPreserveOriginal(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	before := rhythm.Clone(r)
	constraints := variantConstraints(30, "home", true)
	preview, err := rhythm.PreviewSessionVariants(r, "2026-10-12", constraints, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Options) != 1 || preview.Options[0].ID != "home-mobility-v1" || preview.Options[0].Minutes != 30 || preview.Original.Minutes != 45 || preview.Current.Minutes != 45 || preview.Adjustment != nil {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if !reflect.DeepEqual(r, before) {
		t.Fatal("preview mutated record")
	}
	changed, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", preview.Options[0].ID, constraints, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Revision != r.Revision+1 || len(changed.SessionAdjustments) != 1 || changed.SessionAdjustments[0].UpdatedAt != at.UTC().Format(time.RFC3339Nano) || !reflect.DeepEqual(changed.Weeks, before.Weeks) || !reflect.DeepEqual(r, before) {
		t.Fatal("apply did not isolate adjustment from immutable snapshots and caller")
	}
	projected := rhythm.Project(changed, at)
	current := projected.Plan.Sessions[0]
	if current.Minutes != 30 || current.OriginalMinutes != 45 || current.Kind != "mobility" || current.VariantID != "home-mobility-v1" || current.Environment != "home" || !current.LowImpact || projected.Plan.TotalMinutes != 120 || projected.NextPlan.TotalMinutes != 135 {
		t.Fatalf("projection did not apply chosen variant independently: %+v %+v", current, projected)
	}
	preview, err = rhythm.PreviewSessionVariants(changed, "2026-10-12", constraints, at)
	if err != nil || preview.Adjustment == nil || preview.Current.Minutes != 30 || preview.Original.Minutes != 45 {
		t.Fatalf("applied preview lost original/current: %#v %v", preview, err)
	}
	preview.Adjustment.Minutes = 16
	preview.Options[0].Title = "caller edit"
	projected.Plan.Sessions[0].Title = "caller edit"
	if changed.SessionAdjustments[0].Minutes != 30 || rhythm.Project(changed, at).Plan.Sessions[0].Title == "caller edit" {
		t.Fatal("returned DTOs alias aggregate or catalog")
	}
	if err := rhythm.ValidateArchive(changed, at); err != nil {
		t.Fatalf("domain produced a non-restorable record: %v", err)
	}
}

func TestSessionVariantsAreContextualAndNeverLonger(t *testing.T) {
	for _, goal := range []string{"tone", "strength", "endurance"} {
		for _, environment := range []string{"home", "outdoors", "gym"} {
			for _, lowImpact := range []bool{false, true} {
				p := base()
				p.Goal = goal
				r, err := rhythm.SetProfileAt(rhythm.Blank(), 0, p, variantTime())
				if err != nil {
					t.Fatal(err)
				}
				preview, err := rhythm.PreviewSessionVariants(r, "2026-10-12", variantConstraints(15, environment, lowImpact), variantTime())
				if err != nil || len(preview.Options) == 0 {
					t.Fatalf("no usable option: %s/%s/%t %v", goal, environment, lowImpact, err)
				}
				for _, option := range preview.Options {
					if option.Environment != environment || option.Minutes != 15 || lowImpact && !option.LowImpact || option.Description == "" {
						t.Fatalf("option violates declared constraint: %+v", option)
					}
					if (option.Kind == "run" || option.Kind == "strength") && option.Kind != preview.Original.Kind {
						t.Fatal("variant raises an unrelated activity category")
					}
				}
			}
		}
	}
	r := createTimed(t, variantTime())
	for _, constraints := range []rhythm.SessionConstraints{
		variantConstraints(14, "home", false), variantConstraints(46, "home", false), variantConstraints(30, "unknown", false),
	} {
		if _, err := rhythm.PreviewSessionVariants(r, "2026-10-12", constraints, variantTime()); err == nil {
			t.Fatalf("invalid constraints accepted: %+v", constraints)
		}
	}
	for _, variant := range []string{"arbitrary AI exercise", "outdoors-run-v1", "gym-strength-v1"} {
		if _, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", variant, variantConstraints(30, "home", false), variantTime()); err == nil {
			t.Fatalf("noncanonical/incompatible choice accepted: %s", variant)
		}
	}
}

func TestSessionVariantCASRetryAndExplicitRevert(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	firstRevision := r.Revision
	first := variantConstraints(30, "home", false)
	chosen, err := rhythm.ApplySessionVariant(r, firstRevision, "2026-10-12", "home-bodyweight-v1", first, at)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := rhythm.ApplySessionVariant(chosen, firstRevision, "2026-10-12", "home-bodyweight-v1", first, at.Add(time.Minute))
	if err != nil || !reflect.DeepEqual(retry, chosen) {
		t.Fatal("exact current choice retry was not a no-op")
	}
	second := variantConstraints(20, "outdoors", true)
	updated, err := rhythm.ApplySessionVariant(chosen, chosen.Revision, "2026-10-12", "outdoors-walk-v1", second, at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rhythm.ApplySessionVariant(updated, firstRevision, "2026-10-12", "home-bodyweight-v1", first, at.Add(3*time.Minute)); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("older applied choice overwrote newer choice: %v", err)
	}
	if _, err := rhythm.RevertSessionVariant(updated, chosen.Revision, "2026-10-12", at); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("stale revert bypassed CAS: %v", err)
	}
	reverted, err := rhythm.RevertSessionVariant(updated, updated.Revision, "2026-10-12", at.Add(3*time.Minute))
	if err != nil || len(reverted.SessionAdjustments) != 0 || rhythm.Project(reverted, at).Plan.TotalMinutes != 135 || !reflect.DeepEqual(reverted.Weeks, r.Weeks) {
		t.Fatalf("explicit revert lost original: %#v %v", reverted, err)
	}
	replayed, err := rhythm.RevertSessionVariant(reverted, updated.Revision, "2026-10-12", at.Add(4*time.Minute))
	if err != nil || !reflect.DeepEqual(replayed, reverted) {
		t.Fatal("duplicate revert changed aggregate")
	}
	third, err := rhythm.ApplySessionVariant(reverted, reverted.Revision, "2026-10-12", "home-mobility-v1", first, at.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rhythm.RevertSessionVariant(third, updated.Revision, "2026-10-12", at.Add(6*time.Minute)); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatal("late retry of revert erased a newer choice")
	}
}

func TestSessionVariantsRespectRecoveryAndDoNotCompound(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	constraints := variantConstraints(45, "home", false)
	preview, err := rhythm.PreviewSessionVariants(r, "2026-10-12", constraints, at)
	if err != nil || !slices.ContainsFunc(preview.Options, func(v rhythm.SessionVariant) bool { return v.ID == "home-bodyweight-v1" }) {
		t.Fatal("normal strength option missing")
	}
	chosen, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-bodyweight-v1", constraints, at)
	if err != nil {
		t.Fatal(err)
	}
	r, err = rhythm.AddEntry(chosen, chosen.Revision, rhythm.Entry{ID: "bad-sleep", Date: "2026-10-12", Status: "wellbeing", Sleep: ptr(5.0)}, at)
	if err != nil {
		t.Fatal(err)
	}
	preview, err = rhythm.PreviewSessionVariants(r, "2026-10-12", constraints, at)
	if err != nil || !preview.RecoveryRestricted || len(preview.Options) != 1 || preview.Options[0].Minutes != 36 || preview.Current.Minutes != 45 {
		t.Fatalf("recovery must constrain new choices, preserve explicit old choice: %+v %v", preview, err)
	}
	if _, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-bodyweight-v1", constraints, at); err == nil {
		t.Fatal("old preview bypassed new recovery restriction")
	}
	if _, err := rhythm.RevertSessionVariant(r, r.Revision, "2026-10-12", at); err == nil {
		t.Fatal("revert bypassed recovery restriction")
	}
	r, err = rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-mobility-v1", constraints, at)
	if err != nil {
		t.Fatal(err)
	}
	again, err := rhythm.PreviewSessionVariants(r, "2026-10-12", constraints, at)
	if err != nil || again.Options[0].Minutes != 36 || again.Original.Minutes != 45 {
		t.Fatal("repeated preview compounded recovery reduction")
	}
	p := rhythm.Project(r, at)
	r, err = rhythm.AcceptNextPlan(r, r.Revision, p.NextPlan.WeekStart, rhythm.ProposalToken(p), at)
	if err != nil {
		t.Fatal(err)
	}
	// The next proposal respects this week's adjusted mean: (36+45+45)/3
	// reduced by 20%, rounded down once, is 33 minutes. Preview must keep
	// that already accepted duration instead of reducing it again.
	next, err := rhythm.PreviewSessionVariants(r, "2026-10-19", variantConstraints(33, "home", false), at)
	if err != nil || !next.RecoveryRestricted || next.Original.Kind != "mobility" || next.Original.Minutes != 33 || next.Options[0].Minutes != 33 {
		t.Fatal("already reduced accepted recovery plan was reduced a second time")
	}
}

func TestSessionVariantEligibilityAndHistoricImmutability(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	constraints := variantConstraints(30, "home", true)
	for _, date := range []string{"bad", "2026-10-11", "2026-10-13", "2026-10-19", "2026-10-26"} {
		if _, err := rhythm.PreviewSessionVariants(r, date, constraints, at); err == nil {
			t.Fatalf("unscheduled/past/unaccepted date accepted: %s", date)
		}
	}
	chosen, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-mobility-v1", constraints, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"done", "missed"} {
		completed, err := rhythm.AddEntry(chosen, chosen.Revision, rhythm.Entry{ID: status, Date: "2026-10-12", SessionID: "2026-10-12", Status: status}, at)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rhythm.ApplySessionVariant(completed, completed.Revision, "2026-10-12", "home-mobility-v1", constraints, at); err == nil {
			t.Fatalf("changed %s session", status)
		}
		if _, err := rhythm.RevertSessionVariant(completed, completed.Revision, "2026-10-12", at); err == nil {
			t.Fatalf("reverted %s session", status)
		}
		view, err := rhythm.Week(completed, "2026-10-12", at.AddDate(0, 0, 7))
		if err != nil || view.Plan.Sessions[0].Minutes != 30 || view.Plan.Sessions[0].Status != status || view.Plan.Sessions[0].VariantID == "" || view.Plan.TotalMinutes != 120 {
			t.Fatalf("historic applied details lost: %+v %v", view, err)
		}
	}
	if _, err := rhythm.RevertSessionVariant(chosen, chosen.Revision, "2026-10-12", at.AddDate(0, 0, 1)); err == nil {
		t.Fatal("past uncompleted choice was rewritten")
	}
	legacy := rhythm.Record{Profile: ptr(base()), Entries: []rhythm.Entry{{ID: "legacy", Date: "2026-10-12", SessionID: "2026-10-12", Status: "done"}}}
	legacy, err = rhythm.EnsureWeeks(legacy, at)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err = rhythm.DeleteEntry(legacy, legacy.Revision, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rhythm.PreviewSessionVariants(legacy, "2026-10-12", constraints, at); err == nil {
		t.Fatal("unknown legacy details became a guessed variant")
	}
}

func TestAcceptedSessionVariantRolloverCancellationAndNoGhost(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	var err error
	r, err = rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-mobility-v1", variantConstraints(30, "home", true), at)
	if err != nil {
		t.Fatal(err)
	}
	p := rhythm.Project(r, at)
	r, err = rhythm.AcceptNextPlan(r, r.Revision, p.NextPlan.WeekStart, rhythm.ProposalToken(p), at)
	if err != nil {
		t.Fatal(err)
	}
	originalNext := rhythm.Clone(r).Weeks[1]
	r, err = rhythm.ApplySessionVariant(r, r.Revision, "2026-10-19", "outdoors-walk-v1", variantConstraints(20, "outdoors", true), at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	accepted := rhythm.AcceptedNextPlan(r, at)
	if accepted.TotalMinutes != 110 || accepted.Sessions[0].Minutes != 20 || !reflect.DeepEqual(r.Weeks[1], originalNext) {
		t.Fatal("accepted projection or original snapshot changed incorrectly")
	}
	rolled, err := rhythm.EnsureWeeks(r, at.AddDate(0, 0, 7))
	if err != nil || rhythm.Project(rolled, at.AddDate(0, 0, 7)).Plan.Sessions[0].Minutes != 20 {
		t.Fatal("weekly rollover lost applied variant")
	}
	canceled, err := rhythm.CancelAcceptedPlan(r, r.Revision, "2026-10-19", at.Add(2*time.Minute))
	if err != nil || len(canceled.SessionAdjustments) != 1 || canceled.SessionAdjustments[0].SessionID != "2026-10-12" {
		t.Fatalf("cancel failed to remove only cancelled week's choices: %#v %v", canceled.SessionAdjustments, err)
	}
	p = rhythm.Project(canceled, at)
	reaccepted, err := rhythm.AcceptNextPlan(canceled, canceled.Revision, p.NextPlan.WeekStart, rhythm.ProposalToken(p), at.Add(3*time.Minute))
	if err != nil || rhythm.AcceptedNextPlan(reaccepted, at.Add(3*time.Minute)).Sessions[0].Minutes != 45 || len(reaccepted.SessionAdjustments) != 1 {
		t.Fatal("reaccept resurrected a cancelled adjustment")
	}
}

func TestAdjustedCompletionsDoNotUnlockProgress(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	r, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-mobility-v1", variantConstraints(15, "home", true), at)
	if err != nil {
		t.Fatal(err)
	}
	for i, date := range []string{"2026-10-12", "2026-10-14", "2026-10-16"} {
		r, err = rhythm.AddEntry(r, r.Revision, rhythm.Entry{ID: "completion-" + string(rune('a'+i)), Date: date, SessionID: date, Status: "done", Sleep: ptr(8.0), Fatigue: ptr(3)}, now())
		if err != nil {
			t.Fatal(err)
		}
	}
	p := rhythm.Project(r, now())
	if p.Decision.Mode != "maintain" || p.Plan.TotalMinutes != 105 || p.NextPlan.TotalMinutes != 135 || !strings.Contains(strings.Join(p.Decision.Reasons, " "), "замены занятий") {
		t.Fatalf("shortened completion produced a progress reward: %+v", p.Decision)
	}
}

func variantBackup(r rhythm.Record) rhythm.Backup {
	return rhythm.Backup{ExportVersion: rhythm.ExportVersion, Profile: r.Profile, Entries: r.Entries, Weeks: r.Weeks, Workouts: r.Workouts, Reminder: r.Reminder, SessionAdjustments: r.SessionAdjustments}
}

func TestSessionAdjustmentBackupValidationOwnershipAndNullCompatibility(t *testing.T) {
	at := variantTime()
	r := createTimed(t, at)
	r, err := rhythm.ApplySessionVariant(r, r.Revision, "2026-10-12", "home-mobility-v1", variantConstraints(30, "home", true), at)
	if err != nil {
		t.Fatal(err)
	}
	b := variantBackup(rhythm.Clone(r))
	restored, err := rhythm.Restore(rhythm.Blank(), 0, b, at.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(restored.SessionAdjustments, r.SessionAdjustments) || rhythm.Project(restored, at).Plan.Sessions[0].Minutes != 30 {
		t.Fatalf("adjustment backup failed: %#v %v", restored, err)
	}
	b.SessionAdjustments[0].Minutes = 20
	clone := rhythm.Clone(restored)
	clone.SessionAdjustments[0].Minutes = 15
	if restored.SessionAdjustments[0].Minutes != 30 {
		t.Fatal("clone/restore shares adjustment backing array")
	}
	plain := createTimed(t, at)
	if rhythm.Clone(plain).SessionAdjustments != nil {
		t.Fatal("clone invented an adjustment collection")
	}
	if got, err := rhythm.Restore(rhythm.Blank(), 0, variantBackup(plain), at); err != nil || got.SessionAdjustments != nil {
		t.Fatal("old version2 backup without optional adjustments was rejected")
	}
	tests := map[string]func(*rhythm.Record){
		"unknown id":     func(r *rhythm.Record) { r.SessionAdjustments[0].VariantID = "custom-model-text" },
		"environment":    func(r *rhythm.Record) { r.SessionAdjustments[0].Environment = "gym" },
		"low impact":     func(r *rhythm.Record) { r.SessionAdjustments[0].VariantID = "home-bodyweight-v1" },
		"too short":      func(r *rhythm.Record) { r.SessionAdjustments[0].Minutes = 14 },
		"too long":       func(r *rhythm.Record) { r.SessionAdjustments[0].Minutes = 46 },
		"different date": func(r *rhythm.Record) { r.SessionAdjustments[0].Date = "2026-10-14" },
		"missing session": func(r *rhythm.Record) {
			r.SessionAdjustments[0].Date = "2026-10-13"
			r.SessionAdjustments[0].SessionID = "2026-10-13"
		},
		"missing week": func(r *rhythm.Record) {
			r.SessionAdjustments[0].Date = "2026-11-16"
			r.SessionAdjustments[0].SessionID = "2026-11-16"
		},
		"duplicate": func(r *rhythm.Record) { r.SessionAdjustments = append(r.SessionAdjustments, r.SessionAdjustments[0]) },
		"future update": func(r *rhythm.Record) {
			r.SessionAdjustments[0].UpdatedAt = at.Add(time.Hour).UTC().Format(time.RFC3339Nano)
		},
		"after day": func(r *rhythm.Record) {
			r.SessionAdjustments[0].UpdatedAt = at.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano)
		},
		"early update": func(r *rhythm.Record) {
			r.SessionAdjustments[0].UpdatedAt = at.AddDate(0, 0, -7).UTC().Format(time.RFC3339Nano)
		},
		"noncanonical":     func(r *rhythm.Record) { r.SessionAdjustments[0].UpdatedAt = at.Format(time.RFC3339Nano) },
		"invalid time":     func(r *rhythm.Record) { r.SessionAdjustments[0].UpdatedAt = "bad" },
		"snapshot overlay": func(r *rhythm.Record) { r.Weeks[0].Plan.Sessions[0].VariantID = "home-mobility-v1" },
		"snapshot title":   func(r *rhythm.Record) { r.Weeks[0].Plan.Sessions[0].Title = "model-generated instruction" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			copy := rhythm.Clone(r)
			mutate(&copy)
			if _, err := rhythm.Restore(rhythm.Blank(), 0, variantBackup(copy), at); err == nil {
				t.Fatal("invalid adjustment archive accepted")
			}
		})
	}
	if _, err := rhythm.Restore(rhythm.Record{SessionAdjustments: r.SessionAdjustments}, 0, variantBackup(plain), at); err == nil {
		t.Fatal("restore overwrote target adjustment history")
	}
	if _, err := rhythm.EnsureWeeks(rhythm.Record{SessionAdjustments: r.SessionAdjustments}, at); err == nil {
		t.Fatal("adjustment history accepted without a profile")
	}
}
