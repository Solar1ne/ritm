package rhythm

import (
	"slices"
	"time"

	"rhythm/internal/nutrition"
)

const (
	MaxMealSelections = 500
	MaxMealBatch      = 21
)

// MealSelection is an intended meal, never evidence that food was consumed.
// Catalog recipe IDs are portable, immutable, versioned references.
type MealSelection struct {
	Date     string `json:"date"`
	Slot     string `json:"slot"`
	RecipeID string `json:"recipeId"`
}

func validMeal(item MealSelection, now time.Time) error {
	if err := validateSavedDate(item.Date, now); err != nil {
		return err
	}
	if item.Date > now.AddDate(0, 0, 14).Format(time.DateOnly) {
		return invalid("План питания можно сохранить не более чем на 14 дней вперёд.")
	}
	if !nutrition.AllowsSlot(item.RecipeID, item.Slot) {
		return invalid("Выберите блюдо из каталога для завтрака, обеда или ужина.")
	}
	return nil
}

func SetMeal(r Record, revision uint64, selection MealSelection, now time.Time) (Record, error) {
	return SetMeals(r, revision, []MealSelection{selection}, now)
}

// SetMeals validates the whole batch before changing the aggregate. Repeating an
// already saved selection is a no-op; stale changes can never overwrite a plan.
func SetMeals(r Record, revision uint64, selections []MealSelection, now time.Time) (Record, error) {
	if r.Profile == nil {
		return Record{}, invalid("Сначала заполните профиль.")
	}
	if len(selections) == 0 || len(selections) > MaxMealBatch {
		return Record{}, invalid("Сохраняйте от 1 до 21 приёма пищи за один раз.")
	}
	seen := make(map[[2]string]bool, len(selections))
	changed := false
	for _, item := range selections {
		if err := validMeal(item, now); err != nil {
			return Record{}, err
		}
		key := [2]string{item.Date, item.Slot}
		if seen[key] {
			return Record{}, invalid("В одном запросе повторяется дата и приём пищи.")
		}
		seen[key] = true
		if !slices.Contains(r.MealSelections, item) {
			changed = true
		}
	}
	if !changed {
		return Clone(r), nil
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	for _, item := range selections {
		if item.Date < EditableFrom(now) {
			return Record{}, invalid("Изменять план питания можно за последние 90 дней и на 14 дней вперёд.")
		}
	}
	r = Clone(r)
	for _, item := range selections {
		index := slices.IndexFunc(r.MealSelections, func(old MealSelection) bool { return old.Date == item.Date && old.Slot == item.Slot })
		if index >= 0 {
			r.MealSelections[index] = item
		} else {
			if len(r.MealSelections) >= MaxMealSelections {
				return Record{}, ErrCapacity
			}
			r.MealSelections = append(r.MealSelections, item)
		}
	}
	r.Revision++
	return r, nil
}

func DeleteMeal(r Record, revision uint64, date, slot string) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	index := slices.IndexFunc(r.MealSelections, func(e MealSelection) bool { return e.Date == date && e.Slot == slot })
	if index < 0 {
		return Record{}, ErrNotFound
	}
	r = Clone(r)
	r.MealSelections = slices.Delete(r.MealSelections, index, index+1)
	r.Revision++
	return r, nil
}

func validateMeals(r Record, now time.Time) error {
	if len(r.MealSelections) > MaxMealSelections {
		return ErrCapacity
	}
	seen := make(map[[2]string]bool, len(r.MealSelections))
	for _, item := range r.MealSelections {
		if err := validMeal(item, now); err != nil {
			return err
		}
		key := [2]string{item.Date, item.Slot}
		if seen[key] {
			return invalid("Приём пищи повторяется в резервной копии.")
		}
		seen[key] = true
	}
	return nil
}
