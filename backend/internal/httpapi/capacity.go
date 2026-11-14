package httpapi

import "rhythm/internal/rhythm"

// collectionLimits describes domain collection capacity, not storage bytes.
// Keep this transport metadata derived from the same limits used for writes.
type collectionLimits struct {
	Entries        int `json:"entries"`
	Workouts       int `json:"workouts"`
	HRVEntries     int `json:"hrvEntries"`
	BodyEntries    int `json:"bodyEntries"`
	MealSelections int `json:"mealSelections"`
	Weeks          int `json:"weeks"`
}

func currentCollectionLimits() collectionLimits {
	return collectionLimits{
		Entries:        rhythm.MaxEntries,
		Workouts:       rhythm.MaxWorkouts,
		HRVEntries:     rhythm.MaxWellnessEntries,
		BodyEntries:    rhythm.MaxWellnessEntries,
		MealSelections: rhythm.MaxMealSelections,
		Weeks:          rhythm.MaxWeeks,
	}
}
