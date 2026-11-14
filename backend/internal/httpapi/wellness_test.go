package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"rhythm/internal/filestore"
	"rhythm/internal/rhythm"
)

func httpHRV(id string) rhythm.HRVEntry {
	return rhythm.HRVEntry{ID: id, Date: "2026-10-17", ValueMs: 42.5, Metric: "rmssd", Window: "five_minutes", Source: "Ручной ввод"}
}

func httpBody(id string) rhythm.BodyEntry {
	return rhythm.BodyEntry{ID: id, Date: "2026-10-17", WeightKg: floatPointer(72.5), Method: "bia", Source: "Весы утром"}
}

func httpMeal(slot string) rhythm.MealSelection {
	ids := map[string]string{"breakfast": "oat-berry-soy-v1", "lunch": "chickpea-quinoa-salad-v1", "dinner": "bean-brown-rice-pepper-v1"}
	return rhythm.MealSelection{Date: "2026-10-17", Slot: slot, RecipeID: ids[slot]}
}

func TestHTTPWellnessMealsPersistenceIsolationExportRestoreReset(t *testing.T) {
	s, dir := setup(t, "")
	source := guest(t, s)
	blank := requireState(t, request(t, s, "GET", "/api/state", nil, source), 200)
	if blank.HRVEntries == nil || blank.BodyEntries == nil || blank.MealSelections == nil {
		t.Fatal("optional collections must be JSON arrays, not null")
	}
	current := saveProfile(t, s, source, 0)
	hrv := httpHRV("hrv-one")
	current = requireState(t, request(t, s, "POST", "/api/hrv", map[string]any{"revision": current.Revision, "entry": hrv}, source), 200)
	hrv.ValueMs, hrv.ID = 55, "hrv-corrected"
	current = requireState(t, request(t, s, "POST", "/api/hrv", map[string]any{"revision": current.Revision, "entry": hrv}, source), 200)
	current = requireState(t, request(t, s, "POST", "/api/body", map[string]any{"revision": current.Revision, "entry": httpBody("body-one")}, source), 200)
	if len(current.HRVEntries) != 1 || current.HRVEntries[0].ValueMs != 55 || current.BodyEntries[0].BodyFatPercent != nil {
		t.Fatal("correction or missing numeric component mishandled")
	}
	current = requireState(t, request(t, s, "POST", "/api/meals", map[string]any{"revision": current.Revision, "selection": httpMeal("breakfast")}, source), 200)
	batch := []rhythm.MealSelection{httpMeal("lunch"), httpMeal("dinner")}
	current = requireState(t, request(t, s, "POST", "/api/meals/batch", map[string]any{"revision": current.Revision, "selections": batch}, source), 200)
	if current.Revision != 6 || len(current.MealSelections) != 3 || len(current.Entries) != 0 || current.Decision.Mode != "start" {
		t.Fatal("wellness/nutrition fabricated diary facts or changed training rules")
	}
	replay := requireState(t, request(t, s, "POST", "/api/hrv", map[string]any{"revision": 1, "entry": httpHRV("hrv-one")}, source), 200)
	if replay.Revision != current.Revision || replay.HRVEntries[0].ValueMs != 55 {
		t.Fatal("old receipt replay rewrote current measurement")
	}
	other := guest(t, s)
	isolated := requireState(t, request(t, s, "GET", "/api/state", nil, other), 200)
	if len(isolated.HRVEntries)+len(isolated.BodyEntries)+len(isolated.MealSelections) != 0 {
		t.Fatal("another guest can see private wellness data")
	}
	// Reopen the persisted files with a fresh store, keeping the signed identity.
	reopened, err := filestore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.repo = reopened
	export := request(t, s, "GET", "/api/export", nil, source)
	exported := requireState(t, export, 200)
	if !reflect.DeepEqual(exported.HRVEntries, current.HRVEntries) || !reflect.DeepEqual(exported.BodyEntries, current.BodyEntries) || !reflect.DeepEqual(exported.MealSelections, current.MealSelections) {
		t.Fatal("restart/export lost new user data")
	}
	if strings.Contains(export.Body.String(), "hrvReceipts") || strings.Contains(export.Body.String(), "bodyReceipts") {
		t.Fatal("export leaked internal mutation receipts")
	}
	restored := requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": exported}, other), 200)
	if restored.Revision != 1 || !reflect.DeepEqual(restored.HRVEntries, current.HRVEntries) || !reflect.DeepEqual(restored.BodyEntries, current.BodyEntries) || !reflect.DeepEqual(restored.MealSelections, current.MealSelections) || restored.SessionBinding == exported.SessionBinding {
		t.Fatal("restore lost portable data or crossed identities")
	}
	current = requireState(t, request(t, s, "DELETE", "/api/hrv", map[string]any{"revision": current.Revision, "id": hrv.ID}, source), 200)
	current = requireState(t, request(t, s, "DELETE", "/api/body", map[string]any{"revision": current.Revision, "id": "body-one"}, source), 200)
	current = requireState(t, request(t, s, "DELETE", "/api/meals", map[string]any{"revision": current.Revision, "date": "2026-10-17", "slot": "lunch"}, source), 200)
	if len(current.HRVEntries)+len(current.BodyEntries) != 0 || len(current.MealSelections) != 2 {
		t.Fatal("specific deletion failed")
	}
	replay = requireState(t, request(t, s, "POST", "/api/hrv", map[string]any{"revision": 0, "entry": hrv}, source), 200)
	if replay.Revision != current.Revision || len(replay.HRVEntries) != 0 {
		t.Fatal("retry resurrected deleted measurement")
	}
	reset := requireState(t, request(t, s, "DELETE", "/api/data", map[string]any{"revision": current.Revision}, source), 200)
	if reset.HRVEntries == nil || reset.BodyEntries == nil || reset.MealSelections == nil || len(reset.HRVEntries)+len(reset.BodyEntries)+len(reset.MealSelections) != 0 {
		t.Fatal("reset failed to erase new data")
	}
	unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, other), 200)
	if len(unchanged.HRVEntries) != 1 || len(unchanged.BodyEntries) != 1 || len(unchanged.MealSelections) != 3 {
		t.Fatal("reset or deletion affected another principal")
	}
}

func TestHTTPWellnessMealsRejectPartialWritesAndMaliciousRestore(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	current := saveProfile(t, s, cookie, 0)
	for _, tc := range []struct {
		path string
		body any
	}{
		{"/api/hrv", map[string]any{"revision": 1, "entry": map[string]any{"id": "bad", "date": "2026-10-17", "valueMs": nil, "metric": "rmssd", "window": "overnight", "source": "Ручной ввод"}}},
		{"/api/hrv", map[string]any{"revision": 1, "entry": map[string]any{"id": "bad", "date": "2026-10-17", "valueMs": 40, "metric": "rmssd", "window": "overnight", "source": "Ручной ввод", "diagnosis": "unexpected"}}},
		{"/api/body", map[string]any{"revision": 1, "entry": map[string]any{"id": "bad", "date": "2026-10-17", "weightKg": nil, "bodyFatPercent": nil, "method": "bia", "source": "Весы"}}},
		{"/api/body", map[string]any{"revision": 1, "entry": map[string]any{"id": "bad", "date": "2026-10-17", "bodyFatPercent": 0, "method": "bia", "source": "Весы"}}},
		{"/api/meals/batch", map[string]any{"revision": 1, "selections": []rhythm.MealSelection{httpMeal("breakfast"), {Date: "2026-10-17", Slot: "dinner", RecipeID: "absent"}}}},
		{"/api/meals", map[string]any{"selection": httpMeal("breakfast")}},
	} {
		requireState(t, request(t, s, "POST", tc.path, tc.body, cookie), 400)
	}
	after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if after.Revision != current.Revision || len(after.HRVEntries)+len(after.BodyEntries)+len(after.MealSelections) != 0 {
		t.Fatal("rejected input caused partial writes")
	}
	for _, mutate := range []func(*state){
		func(b *state) { e := httpHRV("future"); e.Date = "2026-10-18"; b.HRVEntries = []rhythm.HRVEntry{e} },
		func(b *state) {
			e := httpBody("invalid")
			e.WeightKg = floatPointer(0)
			b.BodyEntries = []rhythm.BodyEntry{e}
		},
		func(b *state) {
			b.MealSelections = []rhythm.MealSelection{{Date: "2026-10-17", Slot: "lunch", RecipeID: "oat-berry-soy-v1"}}
		},
	} {
		target := guest(t, s)
		copy := current
		mutate(&copy)
		requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": copy}, target), 400)
		empty := requireState(t, request(t, s, "GET", "/api/state", nil, target), 200)
		if empty.Revision != 0 || empty.Profile != nil {
			t.Fatal("invalid imported measurement or recipe partially restored")
		}
	}
}

func TestHTTPWellnessMealMutationsRequireOriginBindingAndRevision(t *testing.T) {
	s, _ := setup(t, "")
	a, b := guest(t, s), guest(t, s)
	stateA := saveProfile(t, s, a, 0)
	stateB := saveProfile(t, s, b, 0)
	for _, tc := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/api/hrv", map[string]any{"revision": 1, "entry": httpHRV("one")}},
		{"POST", "/api/body", map[string]any{"revision": 1, "entry": httpBody("one")}},
		{"DELETE", "/api/hrv", map[string]any{"revision": 1, "id": "one"}},
		{"DELETE", "/api/body", map[string]any{"revision": 1, "id": "one"}},
		{"POST", "/api/meals", map[string]any{"revision": 1, "selection": httpMeal("breakfast")}},
		{"POST", "/api/meals/batch", map[string]any{"revision": 1, "selections": []rhythm.MealSelection{httpMeal("breakfast")}}},
		{"DELETE", "/api/meals", map[string]any{"revision": 1, "date": "2026-10-17", "slot": "breakfast"}},
	} {
		for _, guard := range []struct {
			name, origin, binding string
			cookie                *http.Cookie
			status                int
		}{
			{"no_auth", testOrigin, "", nil, 401},
			{"cross_origin", "https://other.example", stateA.SessionBinding, a, 403},
			{"missing_binding", testOrigin, "", a, 409},
			{"switched_identity", testOrigin, stateA.SessionBinding, b, 409},
		} {
			t.Run(tc.method+tc.path+"/"+guard.name, func(t *testing.T) {
				data, err := json.Marshal(tc.body)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(tc.method, testOrigin+tc.path, bytes.NewReader(data))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", guard.origin)
				r.Header.Set(sessionBindingHeader, guard.binding)
				if guard.cookie != nil {
					r.AddCookie(guard.cookie)
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != guard.status {
					t.Fatalf("status=%d want=%d: %s", w.Code, guard.status, w.Body.String())
				}
			})
		}
		tc.body["revision"] = 0
		requireState(t, request(t, s, tc.method, tc.path, tc.body, a), 409)
	}
	for _, item := range []struct {
		cookie   *http.Cookie
		revision uint64
	}{{a, stateA.Revision}, {b, stateB.Revision}} {
		current := requireState(t, request(t, s, "GET", "/api/state", nil, item.cookie), 200)
		if current.Revision != item.revision || len(current.HRVEntries)+len(current.BodyEntries)+len(current.MealSelections) != 0 {
			t.Fatal("rejected mutation affected a principal")
		}
	}
}

func TestHTTPConcurrentWellnessAndMealWritesHaveOneWinner(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	saveProfile(t, s, cookie, 0)
	results := make(chan int, 3)
	var workers sync.WaitGroup
	for _, operation := range []struct {
		path string
		body any
	}{
		{"/api/hrv", map[string]any{"revision": 1, "entry": httpHRV("concurrent")}},
		{"/api/body", map[string]any{"revision": 1, "entry": httpBody("concurrent")}},
		{"/api/meals", map[string]any{"revision": 1, "selection": httpMeal("breakfast")}},
	} {
		workers.Go(func() {
			results <- request(t, s, "POST", operation.path, operation.body, cookie).Code
		})
	}
	workers.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for code := range results {
		switch code {
		case 200:
			succeeded++
		case 409:
			conflicted++
		default:
			t.Fatalf("unexpected concurrent response %d", code)
		}
	}
	current := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if succeeded != 1 || conflicted != 2 || current.Revision != 2 || len(current.HRVEntries)+len(current.BodyEntries)+len(current.MealSelections) != 1 {
		t.Fatal("concurrent mutations bypassed atomic aggregate revision")
	}
}

func TestHTTPWellnessMealsStorageFailureDoesNotConfirmWrites(t *testing.T) {
	s, dir := setup(t, "")
	cookie := guest(t, s)
	before := saveProfile(t, s, cookie, 0)
	paused := dir + "-paused"
	if err := os.Rename(dir, paused); err != nil {
		t.Fatal(err)
	}
	needsRestore := true
	t.Cleanup(func() {
		if needsRestore {
			if err := os.Rename(paused, dir); err != nil {
				t.Errorf("restore test storage: %v", err)
			}
		}
	})
	for _, operation := range []struct {
		path string
		body any
	}{
		{"/api/hrv", map[string]any{"revision": 1, "entry": httpHRV("failure")}},
		{"/api/body", map[string]any{"revision": 1, "entry": httpBody("failure")}},
		{"/api/meals/batch", map[string]any{"revision": 1, "selections": []rhythm.MealSelection{httpMeal("breakfast"), httpMeal("lunch")}}},
	} {
		w := request(t, s, "POST", operation.path, operation.body, cookie)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "storage_unavailable") {
			t.Fatalf("write was confirmed while storage unavailable: %d %s", w.Code, w.Body.String())
		}
	}
	if err := os.Rename(paused, dir); err != nil {
		t.Fatal(err)
	}
	needsRestore = false
	current := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if current.Revision != before.Revision || len(current.HRVEntries)+len(current.BodyEntries)+len(current.MealSelections) != 0 {
		t.Fatal("failed write was later exposed as successful")
	}
}
