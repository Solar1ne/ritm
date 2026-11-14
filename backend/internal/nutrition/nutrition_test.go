package nutrition

import (
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func TestCatalogContractAndIngredients(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 18 || len(Allergens()) != 14 {
		t.Fatalf("catalog=%d allergens=%d", len(catalog), len(Allergens()))
	}
	ids := make(map[string]bool)
	mealCounts := make(map[string]int)
	validID := regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}-v1$`)
	checkText := func(value string, limit int) {
		t.Helper()
		if !utf8.ValidString(value) || strings.TrimSpace(value) != value || value == "" || len(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			t.Fatalf("invalid bounded text: %q", value)
		}
	}
	for _, recipe := range catalog {
		if ids[recipe.ID] || !validID.MatchString(recipe.ID) || !ValidRecipeID(recipe.ID) {
			t.Fatalf("invalid or duplicate durable ID: %q", recipe.ID)
		}
		ids[recipe.ID] = true
		checkText(recipe.Title, 180)
		if recipe.Servings != 1 || recipe.PrepMinutes < 5 || recipe.PrepMinutes > 180 || len(recipe.Meals) != 1 || !AllowsSlot(recipe.ID, recipe.Meals[0]) {
			t.Fatalf("invalid recipe bounds: %+v", recipe)
		}
		mealCounts[recipe.Meals[0]]++
		if !slices.Equal(recipe.Components, []string{"vegetables", "wholegrain", "protein"}) || !slices.Contains(recipe.Diets, "balanced") {
			t.Fatalf("missing food group or default pattern: %s", recipe.ID)
		}
		if len(recipe.Ingredients) < 3 || len(recipe.Ingredients) > 8 || len(recipe.Steps) < 2 || len(recipe.Steps) > 5 {
			t.Fatalf("unbounded recipe detail: %s", recipe.ID)
		}
		for _, ingredient := range recipe.Ingredients {
			checkText(ingredient.Name, 180)
			checkText(ingredient.Amount, 80)
		}
		for _, step := range recipe.Steps {
			checkText(step, 650)
		}
		if err := validateFilters(Filters{ExcludedAllergens: recipe.Allergens}); err != nil {
			t.Fatalf("bad allergen metadata for %s: %v", recipe.ID, err)
		}
		if slices.Contains(recipe.Diets, "vegan") {
			if !slices.Contains(recipe.Diets, "vegetarian") {
				t.Fatalf("vegan recipe lost vegetarian compatibility: %s", recipe.ID)
			}
			for _, allergen := range []string{"eggs", "milk", "fish", "crustaceans", "molluscs"} {
				if slices.Contains(recipe.Allergens, allergen) {
					t.Fatalf("animal ingredient tagged vegan: %s", recipe.ID)
				}
			}
		}
	}
	for _, slot := range Meals() {
		if mealCounts[slot.ID] != 6 {
			t.Fatalf("meal %s has %d examples", slot.ID, mealCounts[slot.ID])
		}
	}
	if ValidRecipeID("invented-v1") || AllowsSlot("oat-berry-soy-v1", "dinner") || AllowsSlot("oat-berry-soy-v1", "Breakfast") {
		t.Fatal("unknown ID or incompatible slot was accepted")
	}
	if _, ok := Find("invented-v1"); ok {
		t.Fatal("unknown recipe was fabricated")
	}
}

func TestFilterCombinationsDoNotRelaxConstraints(t *testing.T) {
	for _, tc := range []struct {
		name    string
		filters Filters
		count   int
	}{
		{"default", Filters{}, 18},
		{"vegetarian", Filters{Diet: "vegetarian"}, 14},
		{"vegan", Filters{Diet: "vegan"}, 10},
		{"vegan_without_gluten_or_soy", Filters{Diet: "vegan", ExcludedAllergens: []string{"gluten", "soy"}}, 5},
		{"vegetarian_without_eggs_or_milk", Filters{Diet: "vegetarian", ExcludedAllergens: []string{"eggs", "milk"}}, 10},
		{"quick_vegetarian_breakfast", Filters{Diet: "vegetarian", Meal: "breakfast", MaxMinutes: 5}, 2},
		{"quick_vegan_breakfast_has_none", Filters{Diet: "vegan", Meal: "breakfast", MaxMinutes: 5}, 0},
		{"safe_empty_result_is_not_fallback", Filters{Diet: "vegetarian", Meal: "breakfast", MaxMinutes: 5, ExcludedAllergens: []string{"milk"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Filter(tc.filters)
			if err != nil || got == nil || len(got) != tc.count {
				t.Fatalf("got=%v count=%d err=%v", got, len(got), err)
			}
			for _, recipe := range got {
				if tc.filters.Diet != "" && !slices.Contains(recipe.Diets, tc.filters.Diet) || tc.filters.Meal != "" && !slices.Contains(recipe.Meals, tc.filters.Meal) || tc.filters.MaxMinutes > 0 && recipe.PrepMinutes > tc.filters.MaxMinutes {
					t.Fatalf("relaxed filter: %+v", recipe)
				}
				for _, excluded := range tc.filters.ExcludedAllergens {
					if slices.Contains(recipe.Allergens, excluded) {
						t.Fatalf("excluded ingredient returned: %s %s", recipe.ID, excluded)
					}
				}
			}
		})
	}
}

func TestRejectNonCanonicalFilters(t *testing.T) {
	for _, filters := range []Filters{
		{Diet: "keto"}, {Diet: "Vegan"}, {Diet: " vegan"}, {Meal: "snack"}, {Meal: "Lunch"},
		{MaxMinutes: -1}, {MaxMinutes: 181}, {ExcludedAllergens: []string{"Milk"}},
		{ExcludedAllergens: []string{"milk "}}, {ExcludedAllergens: []string{"unknown"}},
		{ExcludedAllergens: []string{"milk", "milk"}}, {ExcludedAllergens: make([]string, 15)},
	} {
		if got, err := Filter(filters); err == nil || got != nil {
			t.Fatalf("invalid filter was relaxed: %+v, result=%v err=%v", filters, got, err)
		}
	}
}

func TestCatalogOwnershipAndConcurrentReads(t *testing.T) {
	original := Catalog()
	mutate := func(recipe *Recipe) {
		recipe.ID, recipe.Title = "mutated", "mutated"
		recipe.Meals[0], recipe.Diets[0] = "mutated", "mutated"
		recipe.Ingredients[0].Name, recipe.Ingredients[0].Amount = "mutated", "mutated"
		recipe.Steps[0], recipe.Components[0], recipe.Allergens[0] = "mutated", "mutated", "mutated"
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 20 {
				catalog := Catalog()
				mutate(&catalog[0])
				recipe, ok := Find("oat-berry-soy-v1")
				if !ok {
					t.Error("recipe disappeared")
					return
				}
				mutate(&recipe)
				filtered, err := Filter(Filters{Meal: "breakfast"})
				if err != nil {
					t.Error(err)
					return
				}
				mutate(&filtered[0])
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(original, Catalog()) {
		t.Fatal("returned data exposed the canonical catalogue")
	}
	allergens := Allergens()
	allergens[0].ID = "mutated"
	if Allergens()[0].ID != "gluten" {
		t.Fatal("returned options exposed mutable state")
	}
}

func TestSuggestedWeekDeterministicValidAndOwned(t *testing.T) {
	filters := Filters{Diet: "vegan", MaxMinutes: 30, ExcludedAllergens: []string{"gluten", "soy", "sesame"}}
	plan, err := SuggestWeek("2026-12-29", filters)
	if err != nil || len(plan) != 21 {
		t.Fatalf("plan=%v err=%v", plan, err)
	}
	again, err := SuggestWeek("2026-12-29", filters)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatal("same input produced a different preview")
	}
	eligible, err := Filter(filters)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 12, 29, 0, 0, 0, 0, time.UTC)
	for i, selection := range plan {
		if selection.Date != start.AddDate(0, 0, i/3).Format(time.DateOnly) || selection.Slot != Meals()[i%3].ID || !AllowsSlot(selection.RecipeID, selection.Slot) {
			t.Fatalf("invalid weekly slot: %+v", selection)
		}
		if !slices.ContainsFunc(eligible, func(recipe Recipe) bool { return recipe.ID == selection.RecipeID }) {
			t.Fatalf("plan bypassed filters: %+v", selection)
		}
	}
	if plan[1].RecipeID == plan[4].RecipeID {
		t.Fatal("available lunch alternatives were not rotated")
	}
	plan[0].RecipeID = "mutated"
	if again[0].RecipeID == "mutated" {
		t.Fatal("plans share mutable backing data")
	}
}

func TestSuggestedWeekNeverReturnsPartialOrForbiddenFallback(t *testing.T) {
	for _, filters := range []Filters{
		{Diet: "vegan", MaxMinutes: 5}, {Diet: "vegetarian", MaxMinutes: 5, ExcludedAllergens: []string{"milk"}},
	} {
		plan, err := SuggestWeek("2026-10-17", filters)
		if !errors.Is(err, ErrNoCandidates) || plan != nil {
			t.Fatalf("empty candidates need explicit failure: plan=%v err=%v", plan, err)
		}
	}
	for _, date := range []string{"", "2026-02-30", "2026-1-01", "1969-12-31", "9999-12-26"} {
		if plan, err := SuggestWeek(date, Filters{}); err == nil || plan != nil {
			t.Fatalf("invalid calendar range accepted: %q", date)
		}
	}
	if plan, err := SuggestWeek("2026-10-17", Filters{Meal: "breakfast"}); err == nil || plan != nil {
		t.Fatal("a partial meal filter silently created a full week")
	}
}

func TestShoppingListCountsRepeatedRecipesWithoutInventingUnitConversions(t *testing.T) {
	ids := []string{"oat-berry-soy-v1", "oat-berry-soy-v1", "buckwheat-apple-seed-v1"}
	list, err := ShoppingList(ids)
	if err != nil {
		t.Fatal(err)
	}
	seeds := slices.IndexFunc(list, func(item ShoppingItem) bool { return item.Name == "Тыквенные семечки" })
	if seeds < 0 || !reflect.DeepEqual(list[seeds].Quantities, []ShoppingQuantity{{"15 г", 2}, {"20 г", 1}}) {
		t.Fatalf("quantity occurrences were lost or converted: %+v", list)
	}
	if !slices.IsSortedFunc(list, func(a, b ShoppingItem) int { return strings.Compare(a.Name, b.Name) }) {
		t.Fatal("shopping list order is not stable")
	}
	again, err := ShoppingList(ids)
	if err != nil || !reflect.DeepEqual(list, again) {
		t.Fatal("repeated list calculation differs")
	}
	list[seeds].Quantities[0].Amount = "mutated"
	if again[seeds].Quantities[0].Amount != "15 г" {
		t.Fatal("shopping lists share mutable quantities")
	}
	if list, err := ShoppingList(nil); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty selections: %v, %v", list, err)
	}
	if list, err := ShoppingList([]string{"oat-berry-soy-v1", "unknown"}); err == nil || list != nil {
		t.Fatal("invalid input returned a partial shopping list")
	}
	if list, err := ShoppingList(make([]string, MaxShoppingRecipes+1)); err == nil || list != nil {
		t.Fatal("unbounded shopping list accepted")
	}
}
