package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"rhythm/internal/rhythm"
)

func TestStateCollectionLimits(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	want := map[string]int{"entries": rhythm.MaxEntries, "workouts": rhythm.MaxWorkouts, "hrvEntries": rhythm.MaxWellnessEntries, "bodyEntries": rhythm.MaxWellnessEntries, "mealSelections": rhythm.MaxMealSelections, "weeks": rhythm.MaxWeeks}
	for _, path := range []string{"/api/state", "/api/export"} {
		t.Run(path, func(t *testing.T) {
			w := request(t, s, "GET", path, nil, cookie)
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			var got map[string]int
			if err := json.Unmarshal(response["limits"], &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("limits=%v want=%v", got, want)
			}
			for _, key := range []string{"revision", "profile", "entries", "workouts", "weeks", "exportVersion"} {
				if _, ok := response[key]; !ok {
					t.Errorf("missing existing key %s", key)
				}
			}
		})
	}
}

func TestRestoreIgnoresDerivedCollectionLimits(t *testing.T) {
	s, _ := setup(t, "")
	source := guest(t, s)
	saveProfile(t, s, source, 0)
	exported := request(t, s, "GET", "/api/export", nil, source)
	var backup map[string]any
	if err := json.Unmarshal(exported.Body.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	for _, includeMetadata := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_export", true: "derived_metadata"}[includeMetadata], func(t *testing.T) {
			if includeMetadata {
				backup["limits"] = map[string]int{"entries": 1, "weeks": 1}
			} else {
				delete(backup, "limits")
			}
			target := guest(t, s)
			restored := requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": backup}, target), 200)
			if restored.Profile == nil || restored.Profile.Name != testProfile().Name {
				t.Fatal("profile not restored")
			}
			if restored.Limits != currentCollectionLimits() {
				t.Fatalf("backup changed server limits: %+v", restored.Limits)
			}
		})
	}
}
