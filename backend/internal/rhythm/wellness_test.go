package rhythm_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func hrvFixture(id string) rhythm.HRVEntry {
	return rhythm.HRVEntry{ID: id, Date: now().Format(time.DateOnly), ValueMs: 45, Metric: "rmssd", Window: "five_minutes", Source: "Датчик утром"}
}

func bodyFixture(id string) rhythm.BodyEntry {
	return rhythm.BodyEntry{ID: id, Date: now().Format(time.DateOnly), WeightKg: ptr(72.5), BodyFatPercent: ptr(20.5), Method: "bia", Source: "Весы утром"}
}

func wellnessBackup(r rhythm.Record) rhythm.Backup {
	return rhythm.Backup{ExportVersion: rhythm.ExportVersion, Profile: r.Profile, Entries: r.Entries, Weeks: r.Weeks, HRVEntries: r.HRVEntries, BodyEntries: r.BodyEntries, MealSelections: r.MealSelections}
}

func TestHRVCorrectionReceiptsAndHomogeneousSeries(t *testing.T) {
	r := createTimed(t, now())
	original := hrvFixture("first")
	saved, err := rhythm.AddHRV(r, r.Revision, original, now())
	if err != nil || len(r.HRVEntries) != 0 || saved.Revision != r.Revision+1 {
		t.Fatalf("save changed input or failed: %v", err)
	}
	corrected := original
	corrected.ID, corrected.ValueMs = "corrected", 55
	saved, err = rhythm.AddHRV(saved, saved.Revision, corrected, now())
	if err != nil || len(saved.HRVEntries) != 1 || saved.HRVEntries[0] != corrected {
		t.Fatalf("correction did not replace matching fact: %v", err)
	}
	if replay, err := rhythm.AddHRV(saved, 0, original, now().AddDate(0, 0, 100)); err != nil || !reflect.DeepEqual(replay, saved) {
		t.Fatalf("old retry overwrote correction or expired: %v", err)
	}
	conflict := original
	conflict.ValueMs++
	if _, err := rhythm.AddHRV(saved, saved.Revision, conflict, now()); !errors.Is(err, rhythm.ErrEntryConflict) {
		t.Fatalf("reused ID changed content: %v", err)
	}
	conflict.ID = "stale-new-command"
	if _, err := rhythm.AddHRV(saved, 0, conflict, now()); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("stale correction accepted: %v", err)
	}
	for i, modify := range []func(*rhythm.HRVEntry){func(e *rhythm.HRVEntry) { e.Metric = "sdnn" }, func(e *rhythm.HRVEntry) { e.Window = "overnight" }, func(e *rhythm.HRVEntry) { e.Source = "Другой датчик" }} {
		other := original
		other.ID = fmt.Sprintf("series-%d", i)
		modify(&other)
		if rhythm.SameHRVSeries(original, other) {
			t.Fatal("different measurement protocols were declared comparable")
		}
		saved, err = rhythm.AddHRV(saved, saved.Revision, other, now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(saved.HRVEntries) != 4 {
		t.Fatal("heterogeneous series overwrote one another")
	}
	deleted, err := rhythm.DeleteHRV(saved, saved.Revision, corrected.ID)
	if err != nil || len(saved.HRVEntries) != 4 || len(deleted.HRVEntries) != 3 {
		t.Fatalf("delete aliased prior state: %v", err)
	}
	if replay, err := rhythm.AddHRV(deleted, 0, corrected, now()); err != nil || !reflect.DeepEqual(replay, deleted) {
		t.Fatalf("retry resurrected a deleted fact: %v", err)
	}
	if _, err := rhythm.DeleteHRV(deleted, 0, "missing"); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("delete did not check CAS: %v", err)
	}
	if _, err := rhythm.DeleteHRV(deleted, deleted.Revision, "missing"); !errors.Is(err, rhythm.ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
}

func TestBodyNullableReplacementAndDeepOwnership(t *testing.T) {
	r := createTimed(t, now())
	e := bodyFixture("body-1")
	saved, err := rhythm.AddBody(r, r.Revision, e, now())
	if err != nil {
		t.Fatal(err)
	}
	*e.WeightKg, *e.BodyFatPercent = 99, 30
	if *saved.BodyEntries[0].WeightKg != 72.5 || *saved.BodyReceipts[0].BodyFatPercent != 20.5 {
		t.Fatal("saved data aliases caller pointers")
	}
	cloned := rhythm.Clone(saved)
	*cloned.BodyEntries[0].WeightKg = 100
	*cloned.BodyReceipts[0].BodyFatPercent = 31
	if *saved.BodyEntries[0].WeightKg != 72.5 || *saved.BodyReceipts[0].BodyFatPercent != 20.5 {
		t.Fatal("Clone aliases measurements or receipts")
	}
	correction := bodyFixture("body-2")
	correction.WeightKg = nil
	corrected, err := rhythm.AddBody(saved, saved.Revision, correction, now())
	if err != nil || len(corrected.BodyEntries) != 1 || corrected.BodyEntries[0].WeightKg != nil || *corrected.BodyEntries[0].BodyFatPercent != 20.5 {
		t.Fatalf("missing weight fabricated or old value retained: %v", err)
	}
	if retry, err := rhythm.AddBody(corrected, 0, bodyFixture("body-1"), now()); err != nil || !reflect.DeepEqual(retry, corrected) {
		t.Fatalf("receipt replay undid correction: %v", err)
	}
	reused := bodyFixture("body-1")
	reused.BodyFatPercent = nil
	if _, err := rhythm.AddBody(corrected, corrected.Revision, reused, now()); !errors.Is(err, rhythm.ErrEntryConflict) {
		t.Fatalf("conflicting reuse accepted: %v", err)
	}
	other := bodyFixture("different-method")
	other.Method = "dxa"
	if rhythm.SameBodySeries(other, correction) {
		t.Fatal("different body measurement methods were compared")
	}
	corrected, err = rhythm.AddBody(corrected, corrected.Revision, other, now())
	if err != nil || len(corrected.BodyEntries) != 2 {
		t.Fatalf("different method replaced measurement: %v", err)
	}
	deleted, err := rhythm.DeleteBody(corrected, corrected.Revision, correction.ID)
	if err != nil || len(deleted.BodyEntries) != 1 {
		t.Fatalf("delete: %v", err)
	}
	if replay, err := rhythm.AddBody(deleted, 0, correction, now()); err != nil || !reflect.DeepEqual(replay, deleted) {
		t.Fatalf("deleted body fact was resurrected: %v", err)
	}
}

func TestWellnessValidationAndDateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*rhythm.HRVEntry)
	}{
		{"id", func(e *rhythm.HRVEntry) { e.ID = "../id" }},
		{"long_id", func(e *rhythm.HRVEntry) { e.ID = strings.Repeat("a", 81) }},
		{"invalid_date", func(e *rhythm.HRVEntry) { e.Date = "2026-02-31" }},
		{"future", func(e *rhythm.HRVEntry) { e.Date = "2026-10-18" }},
		{"too_old", func(e *rhythm.HRVEntry) { e.Date = now().AddDate(0, 0, -91).Format(time.DateOnly) }},
		{"epoch", func(e *rhythm.HRVEntry) { e.Date = "1969-12-31" }},
		{"zero", func(e *rhythm.HRVEntry) { e.ValueMs = 0 }},
		{"negative", func(e *rhythm.HRVEntry) { e.ValueMs = -1 }},
		{"above_limit", func(e *rhythm.HRVEntry) { e.ValueMs = 500.1 }},
		{"nan", func(e *rhythm.HRVEntry) { e.ValueMs = math.NaN() }},
		{"infinite", func(e *rhythm.HRVEntry) { e.ValueMs = math.Inf(1) }},
		{"metric", func(e *rhythm.HRVEntry) { e.Metric = "unknown" }},
		{"window", func(e *rhythm.HRVEntry) { e.Window = "unknown" }},
		{"empty_source", func(e *rhythm.HRVEntry) { e.Source = "  " }},
		{"long_source", func(e *rhythm.HRVEntry) { e.Source = strings.Repeat("я", 61) }},
		{"control_source", func(e *rhythm.HRVEntry) { e.Source = "device\x00" }},
		{"invalid_utf8", func(e *rhythm.HRVEntry) { e.Source = string([]byte{0xff}) }},
	} {
		t.Run("hrv_"+tc.name, func(t *testing.T) {
			r := createTimed(t, now())
			before := rhythm.Clone(r)
			e := hrvFixture("bad")
			tc.edit(&e)
			if _, err := rhythm.AddHRV(r, r.Revision, e, now()); err == nil {
				t.Fatal("invalid measurement accepted")
			}
			if !reflect.DeepEqual(r, before) {
				t.Fatal("validation mutated input")
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*rhythm.BodyEntry)
	}{
		{"missing_both", func(e *rhythm.BodyEntry) { e.WeightKg, e.BodyFatPercent = nil, nil }},
		{"zero_weight", func(e *rhythm.BodyEntry) { e.WeightKg = ptr(0.0) }},
		{"low_weight", func(e *rhythm.BodyEntry) { e.WeightKg = ptr(19.9) }},
		{"high_weight", func(e *rhythm.BodyEntry) { e.WeightKg = ptr(400.1) }},
		{"nan_weight", func(e *rhythm.BodyEntry) { e.WeightKg = ptr(math.NaN()) }},
		{"inf_weight", func(e *rhythm.BodyEntry) { e.WeightKg = ptr(math.Inf(-1)) }},
		{"zero_fat", func(e *rhythm.BodyEntry) { e.BodyFatPercent = ptr(0.0) }},
		{"high_fat", func(e *rhythm.BodyEntry) { e.BodyFatPercent = ptr(75.1) }},
		{"nan_fat", func(e *rhythm.BodyEntry) { e.BodyFatPercent = ptr(math.NaN()) }},
		{"inf_fat", func(e *rhythm.BodyEntry) { e.BodyFatPercent = ptr(math.Inf(1)) }},
		{"unknown_method", func(e *rhythm.BodyEntry) { e.Method = "unknown" }},
		{"future", func(e *rhythm.BodyEntry) { e.Date = "2026-10-18" }},
		{"too_old", func(e *rhythm.BodyEntry) { e.Date = now().AddDate(0, 0, -91).Format(time.DateOnly) }},
		{"source", func(e *rhythm.BodyEntry) { e.Source = "" }},
	} {
		t.Run("body_"+tc.name, func(t *testing.T) {
			r := createTimed(t, now())
			e := bodyFixture("bad")
			tc.edit(&e)
			if _, err := rhythm.AddBody(r, r.Revision, e, now()); err == nil {
				t.Fatal("invalid measurement accepted")
			}
		})
	}
	r := createTimed(t, now())
	e := hrvFixture("boundary")
	e.Date, e.ValueMs, e.Source = rhythm.EditableFrom(now()), 500, "  "+strings.Repeat("я", 60)+"  "
	if saved, err := rhythm.AddHRV(r, r.Revision, e, now()); err != nil || saved.HRVEntries[0].Source != strings.TrimSpace(e.Source) {
		t.Fatalf("valid boundary failed: %v", err)
	}
	for _, weight := range []float64{20, 400} {
		e := bodyFixture("boundary")
		e.Date, e.WeightKg, e.BodyFatPercent = rhythm.EditableFrom(now()), ptr(weight), ptr(75.0)
		if _, err := rhythm.AddBody(r, r.Revision, e, now()); err != nil {
			t.Fatalf("body boundary: %v", err)
		}
	}
	if _, err := rhythm.AddHRV(rhythm.Blank(), 0, hrvFixture("blank"), now()); err == nil {
		t.Fatal("HRV without profile")
	}
	if _, err := rhythm.AddBody(rhythm.Blank(), 0, bodyFixture("blank"), now()); err == nil {
		t.Fatal("body without profile")
	}
}

func TestWellnessCapacityRetainsFactsAndBoundsReceipts(t *testing.T) {
	r := createTimed(t, now())
	for i := 0; i < rhythm.MaxWellnessEntries; i++ {
		h, b := hrvFixture(fmt.Sprintf("h-%d", i)), bodyFixture(fmt.Sprintf("b-%d", i))
		h.Source, b.Source = fmt.Sprintf("source-%d", i), fmt.Sprintf("source-%d", i)
		r.HRVEntries, r.BodyEntries = append(r.HRVEntries, h), append(r.BodyEntries, b)
		r.HRVReceipts, r.BodyReceipts = append(r.HRVReceipts, h), append(r.BodyReceipts, b)
	}
	before := rhythm.Clone(r)
	if _, err := rhythm.AddHRV(r, r.Revision, hrvFixture("overflow"), now()); !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatalf("HRV overflow: %v", err)
	}
	if _, err := rhythm.AddBody(r, r.Revision, bodyFixture("overflow"), now()); !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatalf("body overflow: %v", err)
	}
	if !reflect.DeepEqual(r, before) {
		t.Fatal("overflow pruned history")
	}
	h, b := hrvFixture("replacement-h"), bodyFixture("replacement-b")
	h.Source, b.Source = "source-0", "source-0"
	var err error
	r, err = rhythm.AddHRV(r, r.Revision, h, now())
	if err != nil {
		t.Fatal(err)
	}
	r, err = rhythm.AddBody(r, r.Revision, b, now())
	if err != nil || len(r.HRVEntries) != 500 || len(r.BodyEntries) != 500 || len(r.HRVReceipts) != 500 || len(r.BodyReceipts) != 500 {
		t.Fatalf("replacement at capacity: %v", err)
	}
}

func TestWellnessRestoreOldHistoryAndRejectTampering(t *testing.T) {
	r := createTimed(t, now())
	h, b := hrvFixture("historic-h"), bodyFixture("historic-b")
	h.Date, b.Date = "2020-01-01", "2020-01-01"
	b.WeightKg = nil
	r.HRVEntries, r.BodyEntries = []rhythm.HRVEntry{h}, []rhythm.BodyEntry{b}
	backup := wellnessBackup(r)
	restored, err := rhythm.Restore(rhythm.Blank(), 0, backup, now())
	if err != nil || restored.BodyEntries[0].WeightKg != nil || len(restored.HRVReceipts) != 1 || len(restored.BodyReceipts) != 1 {
		t.Fatalf("old portable history failed: %v", err)
	}
	*backup.BodyEntries[0].BodyFatPercent = 10
	if *restored.BodyEntries[0].BodyFatPercent != 20.5 {
		t.Fatal("restored aggregate aliases backup")
	}
	if _, err := rhythm.DeleteHRV(restored, restored.Revision, h.ID); err != nil {
		t.Fatalf("cannot delete archived measurement: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*rhythm.Record)
	}{
		{"duplicate_hrv_id", func(r *rhythm.Record) { r.HRVEntries = append(r.HRVEntries, r.HRVEntries[0]) }},
		{"duplicate_hrv_fact", func(r *rhythm.Record) { e := r.HRVEntries[0]; e.ID = "other"; r.HRVEntries = append(r.HRVEntries, e) }},
		{"invalid_hrv", func(r *rhythm.Record) { r.HRVEntries[0].ValueMs = math.NaN() }},
		{"duplicate_body_id", func(r *rhythm.Record) { r.BodyEntries = append(r.BodyEntries, r.BodyEntries[0]) }},
		{"duplicate_body_fact", func(r *rhythm.Record) {
			e := r.BodyEntries[0]
			e.ID = "other"
			r.BodyEntries = append(r.BodyEntries, e)
		}},
		{"invalid_body", func(r *rhythm.Record) { r.BodyEntries[0].BodyFatPercent = ptr(0.0) }},
		{"future", func(r *rhythm.Record) { r.HRVEntries[0].Date = "2026-10-18" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := rhythm.Clone(restored)
			tc.edit(&copy)
			if _, err := rhythm.Restore(rhythm.Blank(), 0, wellnessBackup(copy), now()); err == nil {
				t.Fatal("invalid wellness backup accepted")
			}
		})
	}
	legacy := wellnessBackup(createTimed(t, now()))
	if _, err := rhythm.Restore(rhythm.Blank(), 0, legacy, now()); err != nil {
		t.Fatalf("previous exports without optional measurements stopped working: %v", err)
	}
	for _, partial := range []rhythm.Record{{HRVEntries: []rhythm.HRVEntry{h}}, {BodyEntries: []rhythm.BodyEntry{b}}} {
		if _, err := rhythm.Restore(partial, 0, legacy, now()); err == nil {
			t.Fatal("restore overwrote existing profile-less measurements")
		}
		if _, err := rhythm.EnsureWeeks(partial, now()); err == nil {
			t.Fatal("profile-less measurement accepted by materialization")
		}
	}
}
