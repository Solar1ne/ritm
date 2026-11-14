package rhythm_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func mealFixture(date, slot string) rhythm.MealSelection {
	ids := map[string]string{"breakfast": "oat-berry-soy-v1", "lunch": "chickpea-quinoa-salad-v1", "dinner": "bean-brown-rice-pepper-v1"}
	return rhythm.MealSelection{Date: date, Slot: slot, RecipeID: ids[slot]}
}

func mealWeek() []rhythm.MealSelection {
	var result []rhythm.MealSelection
	for i := range 7 {
		for _, slot := range []string{"breakfast", "lunch", "dinner"} {
			result = append(result, mealFixture(now().AddDate(0, 0, i).Format(time.DateOnly), slot))
		}
	}
	return result
}

func TestMealWeekAtomicSaveRetryAndCorrection(t *testing.T) {
	r := createTimed(t, now())
	batch := mealWeek()
	saved, err := rhythm.SetMeals(r, r.Revision, batch, now())
	if err != nil || saved.Revision != r.Revision+1 || len(saved.MealSelections) != 21 || len(r.MealSelections) != 0 {
		t.Fatalf("weekly batch was not one isolated revision: %v", err)
	}
	if retry, err := rhythm.SetMeals(saved, 0, batch, now()); err != nil || !reflect.DeepEqual(retry, saved) {
		t.Fatalf("retry duplicated meals: %v", err)
	}
	batch[0].RecipeID = "buckwheat-apple-seed-v1"
	if _, err := rhythm.SetMeals(saved, 0, batch, now()); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("stale batch overwrote current plan: %v", err)
	}
	corrected, err := rhythm.SetMeal(saved, saved.Revision, batch[0], now())
	if err != nil || corrected.MealSelections[0].RecipeID != batch[0].RecipeID || saved.MealSelections[0].RecipeID != "oat-berry-soy-v1" {
		t.Fatalf("single correction failed or aliased old state: %v", err)
	}
	before := rhythm.Clone(corrected)
	batch[0].RecipeID = "oat-berry-soy-v1"
	batch[20].RecipeID = "missing-recipe"
	if _, err := rhythm.SetMeals(corrected, corrected.Revision, batch, now()); err == nil || !reflect.DeepEqual(corrected, before) {
		t.Fatal("invalid final meal committed preceding changes")
	}
	deleted, err := rhythm.DeleteMeal(corrected, corrected.Revision, batch[0].Date, batch[0].Slot)
	if err != nil || len(deleted.MealSelections) != 20 || len(corrected.MealSelections) != 21 {
		t.Fatalf("delete failed or changed caller: %v", err)
	}
	if _, err := rhythm.DeleteMeal(deleted, 0, batch[0].Date, batch[0].Slot); !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("delete CAS: %v", err)
	}
	if _, err := rhythm.DeleteMeal(deleted, deleted.Revision, batch[0].Date, batch[0].Slot); !errors.Is(err, rhythm.ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
}

func TestMealValidationBoundsAndArchive(t *testing.T) {
	for _, tc := range []struct {
		name  string
		batch func() []rhythm.MealSelection
	}{
		{"empty", func() []rhythm.MealSelection { return nil }},
		{"too_many", func() []rhythm.MealSelection { return append(mealWeek(), mealFixture("2026-10-24", "breakfast")) }},
		{"duplicate", func() []rhythm.MealSelection {
			e := mealFixture("2026-10-17", "breakfast")
			return []rhythm.MealSelection{e, e}
		}},
		{"invalid_date", func() []rhythm.MealSelection { return []rhythm.MealSelection{mealFixture("2026-02-31", "breakfast")} }},
		{"old", func() []rhythm.MealSelection {
			return []rhythm.MealSelection{mealFixture(now().AddDate(0, 0, -91).Format(time.DateOnly), "breakfast")}
		}},
		{"future", func() []rhythm.MealSelection {
			return []rhythm.MealSelection{mealFixture(now().AddDate(0, 0, 15).Format(time.DateOnly), "breakfast")}
		}},
		{"slot", func() []rhythm.MealSelection {
			return []rhythm.MealSelection{{Date: "2026-10-17", Slot: "snack", RecipeID: "oat-berry-soy-v1"}}
		}},
		{"recipe_wrong_slot", func() []rhythm.MealSelection {
			return []rhythm.MealSelection{{Date: "2026-10-17", Slot: "lunch", RecipeID: "oat-berry-soy-v1"}}
		}},
		{"recipe_missing", func() []rhythm.MealSelection {
			return []rhythm.MealSelection{{Date: "2026-10-17", Slot: "lunch", RecipeID: "custom"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := createTimed(t, now())
			if _, err := rhythm.SetMeals(r, r.Revision, tc.batch(), now()); err == nil {
				t.Fatal("invalid meal plan accepted")
			}
		})
	}
	for _, offset := range []int{-90, 0, 14} {
		r := createTimed(t, now())
		if _, err := rhythm.SetMeal(r, r.Revision, mealFixture(now().AddDate(0, 0, offset).Format(time.DateOnly), "breakfast"), now()); err != nil {
			t.Fatalf("valid date boundary %d: %v", offset, err)
		}
	}
	r := createTimed(t, now())
	r.MealSelections = []rhythm.MealSelection{mealFixture("2020-01-01", "breakfast")}
	restored, err := rhythm.Restore(rhythm.Blank(), 0, wellnessBackup(r), now())
	if err != nil || len(restored.MealSelections) != 1 {
		t.Fatalf("old planned meals lost on restore: %v", err)
	}
	if _, err := rhythm.DeleteMeal(restored, restored.Revision, "2020-01-01", "breakfast"); err != nil {
		t.Fatalf("archived meal cannot be removed: %v", err)
	}
	for _, edit := range []func(*rhythm.Record){
		func(r *rhythm.Record) { r.MealSelections[0].RecipeID = "unknown" },
		func(r *rhythm.Record) { r.MealSelections[0].Slot = "dinner" },
		func(r *rhythm.Record) { r.MealSelections = append(r.MealSelections, r.MealSelections[0]) },
		func(r *rhythm.Record) { r.MealSelections[0].Date = "2030-01-01" },
	} {
		copy := rhythm.Clone(r)
		edit(&copy)
		if _, err := rhythm.Restore(rhythm.Blank(), 0, wellnessBackup(copy), now()); err == nil {
			t.Fatal("malicious meal reference passed restore")
		}
	}
	if _, err := rhythm.SetMeal(rhythm.Blank(), 0, mealFixture("2026-10-17", "breakfast"), now()); err == nil {
		t.Fatal("meal saved without profile")
	}
}

func TestMealCapacityRejectsWholeBatchWithoutDroppingHistory(t *testing.T) {
	r := createTimed(t, now())
	for i := 1; i <= rhythm.MaxMealSelections-1; i++ {
		r.MealSelections = append(r.MealSelections, mealFixture(now().AddDate(0, 0, -i).Format(time.DateOnly), "breakfast"))
	}
	batch := []rhythm.MealSelection{mealFixture("2026-10-17", "breakfast"), mealFixture("2026-10-17", "lunch")}
	before := rhythm.Clone(r)
	if _, err := rhythm.SetMeals(r, r.Revision, batch, now()); !errors.Is(err, rhythm.ErrCapacity) || !reflect.DeepEqual(r, before) {
		t.Fatalf("capacity failure dropped old facts or saved half batch: %v", err)
	}
	full, err := rhythm.SetMeal(r, r.Revision, batch[0], now())
	if err != nil || len(full.MealSelections) != 500 {
		t.Fatalf("exact capacity: %v", err)
	}
	batch[0].RecipeID = "buckwheat-apple-seed-v1"
	if corrected, err := rhythm.SetMeal(full, full.Revision, batch[0], now()); err != nil || len(corrected.MealSelections) != 500 {
		t.Fatalf("cannot edit full plan: %v", err)
	}
}
