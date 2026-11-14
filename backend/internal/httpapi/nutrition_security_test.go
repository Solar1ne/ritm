package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"rhythm/internal/nutrition"
)

func TestNutritionEndpointsRequireBoundIdentity(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	initial := saveProfile(t, s, cookie, 0)
	other := guest(t, s)
	otherState := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, other), 200)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/nutrition/catalog"},
		{http.MethodPost, "/api/nutrition/preview"},
	} {
		t.Run(route.path, func(t *testing.T) {
			body := map[string]any{"startDate": "2026-10-17", "filters": map[string]any{}}
			if w := request(t, s, route.method, route.path, body, nil); w.Code != 401 {
				t.Fatalf("missing identity: %d %s", w.Code, w.Body.String())
			}
			for _, binding := range []string{"", strings.Repeat("0", 64), otherState.SessionBinding} {
				r := httptest.NewRequest(route.method, testOrigin+route.path, strings.NewReader(`{"startDate":"2026-10-17","filters":{}}`))
				r.AddCookie(cookie)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", testOrigin)
				r.Header.Set(sessionBindingHeader, binding)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != 409 || !strings.Contains(w.Body.String(), "session_changed") {
					t.Fatalf("wrong account binding reached handler: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
	for _, headers := range []map[string]string{
		{"Origin": "https://other.example"},
		{"Origin": testOrigin, "Sec-Fetch-Site": "cross-site"},
		{"Origin": testOrigin, "Sec-Fetch-Site": "same-site"},
	} {
		r := httptest.NewRequest(http.MethodPost, testOrigin+"/api/nutrition/preview", strings.NewReader(`{"startDate":"2026-10-17","filters":{}}`))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(sessionBindingHeader, initial.SessionBinding)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("cross-origin request reached preview: %d %s", w.Code, w.Body.String())
		}
	}
	if current := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(initial, current) {
		t.Fatal("unauthorized nutrition requests changed the aggregate")
	}
}

func TestNutritionPreviewIsReadOnlyAndKeepsAllConstraints(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	profile := testProfile()
	profile.Name = "private-nutrition-fixture"
	initial := requireState(t, request(t, s, http.MethodPost, "/api/profile", map[string]any{"revision": 0, "profile": profile}, cookie), 200)
	catalog := request(t, s, http.MethodGet, "/api/nutrition/catalog", nil, cookie)
	if catalog.Code != 200 || catalog.Header().Get("Cache-Control") != "no-store" || strings.Contains(catalog.Body.String(), profile.Name) || strings.Contains(catalog.Body.String(), initial.SessionBinding) {
		t.Fatalf("catalog leaked account data or lacked private cache policy: %d", catalog.Code)
	}
	var metadata struct {
		Recipes        []nutrition.Recipe `json:"recipes"`
		AllergenNotice string             `json:"allergenNotice"`
		RecipeNotice   string             `json:"recipeNotice"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.Recipes) == 0 || metadata.AllergenNotice == "" || metadata.RecipeNotice == "" {
		t.Fatal("catalog omitted recipes or the limits of ingredient filtering")
	}
	filters := nutrition.Filters{Diet: "vegan", MaxMinutes: 30, ExcludedAllergens: []string{"gluten", "soy", "sesame"}}
	for _, offset := range []int{0, 8} {
		start := testNow().AddDate(0, 0, offset)
		w := request(t, s, http.MethodPost, "/api/nutrition/preview", map[string]any{"startDate": start.Format(time.DateOnly), "filters": filters}, cookie)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("valid boundary preview: %d %s", w.Code, w.Body.String())
		}
		var preview struct {
			Selections   []nutrition.Selection    `json:"selections"`
			ShoppingList []nutrition.ShoppingItem `json:"shoppingList"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		if len(preview.Selections) != 21 || len(preview.ShoppingList) == 0 {
			t.Fatal("preview returned an incomplete week or empty shopping list")
		}
		seen := make(map[[2]string]bool)
		for _, item := range preview.Selections {
			key := [2]string{item.Date, item.Slot}
			if item.Date < start.Format(time.DateOnly) || item.Date > start.AddDate(0, 0, 6).Format(time.DateOnly) || seen[key] {
				t.Fatalf("invalid or duplicate weekly slot: %+v", item)
			}
			seen[key] = true
			recipe, ok := nutrition.Find(item.RecipeID)
			if !ok || !slices.Contains(recipe.Meals, item.Slot) || !slices.Contains(recipe.Diets, filters.Diet) || recipe.PrepMinutes > filters.MaxMinutes {
				t.Fatalf("preview relaxed a recipe constraint: %+v", item)
			}
			for _, excluded := range filters.ExcludedAllergens {
				if slices.Contains(recipe.Allergens, excluded) {
					t.Fatalf("preview included excluded ingredient %s in %s", excluded, recipe.ID)
				}
			}
		}
		if strings.Contains(w.Body.String(), profile.Name) || strings.Contains(w.Body.String(), initial.SessionBinding) {
			t.Fatal("preview exposed unrelated account identity")
		}
	}
	if current := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(initial, current) {
		t.Fatal("reviewing recipe suggestions persisted meal choices or changed revision")
	}
}

func TestNutritionPreviewRejectsMalformedAndUnfulfillableRequests(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	initial := saveProfile(t, s, cookie, 0)
	for _, tc := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"past", `{"startDate":"2026-10-16","filters":{}}`, 400, "validation"},
		{"after_eight_days", `{"startDate":"2026-10-26","filters":{}}`, 400, "validation"},
		{"invalid_date", `{"startDate":"2026-02-30","filters":{}}`, 400, "validation"},
		{"timestamp", `{"startDate":"2026-10-17T00:00:00Z","filters":{}}`, 400, "validation"},
		{"missing_filters", `{"startDate":"2026-10-17"}`, 400, "validation"},
		{"null_filters", `{"startDate":"2026-10-17","filters":null}`, 400, "validation"},
		{"unknown_outer", `{"startDate":"2026-10-17","filters":{},"save":true}`, 400, "json"},
		{"unknown_inner", `{"startDate":"2026-10-17","filters":{"ignoreAllergens":true}}`, 400, "json"},
		{"unknown_diet", `{"startDate":"2026-10-17","filters":{"diet":"private-invalid-diet"}}`, 400, "validation"},
		{"noncanonical_diet", `{"startDate":"2026-10-17","filters":{"diet":"Vegan"}}`, 400, "validation"},
		{"negative_time", `{"startDate":"2026-10-17","filters":{"maxMinutes":-1}}`, 400, "validation"},
		{"excess_time", `{"startDate":"2026-10-17","filters":{"maxMinutes":181}}`, 400, "validation"},
		{"unknown_allergen", `{"startDate":"2026-10-17","filters":{"excludedAllergens":["private-allergen"]}}`, 400, "validation"},
		{"noncanonical_allergen", `{"startDate":"2026-10-17","filters":{"excludedAllergens":[" milk"]}}`, 400, "validation"},
		{"duplicate_allergen", `{"startDate":"2026-10-17","filters":{"excludedAllergens":["milk","milk"]}}`, 400, "validation"},
		{"partial_week", `{"startDate":"2026-10-17","filters":{"meal":"breakfast"}}`, 400, "validation"},
		{"no_vegan_breakfast", `{"startDate":"2026-10-17","filters":{"diet":"vegan","maxMinutes":5}}`, 422, "no_recipes"},
		{"all_quick_breakfasts_excluded", `{"startDate":"2026-10-17","filters":{"diet":"vegetarian","maxMinutes":5,"excludedAllergens":["milk"]}}`, 422, "no_recipes"},
		{"trailing_json", `{"startDate":"2026-10-17","filters":{}} {}`, 400, "json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, testOrigin+"/api/nutrition/preview", strings.NewReader(tc.body))
			r.AddCookie(cookie)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", testOrigin)
			r.Header.Set(sessionBindingHeader, initial.SessionBinding)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("rejected input: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `"selections"`) || strings.Contains(w.Body.String(), "private-") || w.Body.Len() > 1000 {
				t.Fatal("invalid preview returned a partial plan or reflected private input")
			}
		})
	}
	if current := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(initial, current) {
		t.Fatal("rejected preview mutated the account")
	}
}

func TestNutritionPreviewRequestLimitAndServerLocalDates(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200)
	const payload = `{"startDate":"2026-10-17","filters":{}}`
	for _, tc := range []struct {
		bytes       int
		contentType string
		status      int
	}{
		{64 * 1024, "application/json", 200},
		{64*1024 + 1, "application/json", 400},
		{len(payload), "text/plain", 415},
	} {
		body := append([]byte(payload), bytes.Repeat([]byte(" "), tc.bytes-len(payload))...)
		r := httptest.NewRequest(http.MethodPost, testOrigin+"/api/nutrition/preview", bytes.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("Content-Type", tc.contentType)
		r.Header.Set(sessionBindingHeader, st.SessionBinding)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("bytes=%d media=%s: %d want=%d %s", tc.bytes, tc.contentType, w.Code, tc.status, w.Body.String())
		}
	}
	// UTC still says Saturday; the configured Moscow calendar is already Sunday.
	s.clock = func() time.Time { return time.Date(2026, 10, 17, 22, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		date   string
		status int
	}{{"2026-10-17", 400}, {"2026-10-18", 200}, {"2026-10-26", 200}, {"2026-10-27", 400}} {
		w := request(t, s, http.MethodPost, "/api/nutrition/preview", map[string]any{"startDate": tc.date, "filters": map[string]any{}}, cookie)
		if w.Code != tc.status {
			t.Fatalf("server-local date %s: %d want=%d %s", tc.date, w.Code, tc.status, w.Body.String())
		}
	}
}
