package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func TestHTTPWeeklyAcceptanceHistoryAndCorrection(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	current := saveProfile(t, s, cookie, 0)
	if len(current.Weeks) != 1 || current.ExportVersion != 2 || current.Today != "2026-10-17" || current.CurrentWeekStart != "2026-10-12" || current.TimeZone != "Moscow" {
		t.Fatalf("missing lifecycle fields: %+v", current)
	}
	current = requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": current.Revision, "entry": rhythm.Entry{ID: "friday", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}}, cookie), 200)
	p := testProfile()
	p.Minutes = 90
	p.Days = []int{1}
	p.Goal = "strength"
	current = requireState(t, request(t, s, "POST", "/api/profile", map[string]any{"revision": current.Revision, "profile": p}, cookie), 200)
	if current.Plan.TotalMinutes != 135 || current.Plan.Sessions[2].Minutes != 45 || current.Plan.Sessions[2].Status != "done" {
		t.Fatal("profile edited a completed session")
	}
	accept := map[string]any{"revision": current.Revision, "weekStart": current.NextPlan.WeekStart, "nextPlanToken": current.NextPlanToken}
	current = requireState(t, request(t, s, "POST", "/api/plan/accept", accept, cookie), 200)
	if current.AcceptedNextPlan == nil || current.AcceptedNextPlan.TotalMinutes != 90 {
		t.Fatal("proposal not accepted")
	}
	retry := requireState(t, request(t, s, "POST", "/api/plan/accept", accept, cookie), 200)
	if retry.Revision != current.Revision {
		t.Fatal("double acceptance created another revision")
	}
	w := request(t, s, "GET", "/api/weeks?weekStart=2026-10-12", nil, cookie)
	if w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	var view rhythm.WeekView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Plan.Sessions[2].Status != "done" || !view.Editable {
		t.Fatal("history omitted completion or correction capability")
	}
	s.clock = func() time.Time { return testNow().AddDate(0, 0, 2) }
	current = requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if current.Plan.WeekStart != "2026-10-19" || current.Plan.TotalMinutes != 90 || current.AcceptedNextPlan != nil {
		t.Fatal("Monday did not promote accepted week")
	}
	requireState(t, request(t, s, "DELETE", "/api/plan/accept", map[string]any{"revision": current.Revision, "weekStart": "2026-10-19"}, cookie), 409)
	correction := rhythm.Entry{ID: "corrected-friday", Date: "2026-10-16", SessionID: "2026-10-16", Status: "missed"}
	current = requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": current.Revision, "entry": correction}, cookie), 200)
	if current.Decision.Mode != "start" {
		t.Fatal("historical correction changed current decision")
	}
	current = requireState(t, request(t, s, "DELETE", "/api/entries", map[string]any{"revision": current.Revision, "id": correction.ID}, cookie), 200)
	if len(current.Entries) != 0 || len(current.Weeks) != 2 || current.Plan.TotalMinutes != 90 {
		t.Fatal("deleting diary entry erased schedule")
	}
	requireState(t, request(t, s, "DELETE", "/api/entries", map[string]any{"revision": current.Revision, "id": correction.ID}, cookie), 404)
}

func TestHTTPAcceptCancellationAndStrictWeekSelection(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := saveProfile(t, s, cookie, 0)
	requireState(t, request(t, s, "POST", "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.NextPlan.WeekStart, "nextPlanToken": "stale"}, cookie), 409)
	st = requireState(t, request(t, s, "POST", "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.NextPlan.WeekStart, "nextPlanToken": st.NextPlanToken}, cookie), 200)
	st = requireState(t, request(t, s, "DELETE", "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.AcceptedNextPlan.WeekStart}, cookie), 200)
	if st.AcceptedNextPlan != nil || len(st.Weeks) != 1 {
		t.Fatal("cancel left future schedule")
	}
	for _, tc := range []struct {
		query  string
		status int
	}{{"", 400}, {"?weekStart=2026-10-13", 400}, {"?weekStart=2026-10-05", 404}, {"?weekStart=invalid", 400}} {
		if w := request(t, s, "GET", "/api/weeks"+tc.query, nil, cookie); w.Code != tc.status {
			t.Fatalf("week query %s: %d", tc.query, w.Code)
		}
	}
}

func TestHTTPBackupRestoresOnlyUserDataIntoEmptyAccount(t *testing.T) {
	s, _ := setup(t, "")
	source := guest(t, s)
	st := saveProfile(t, s, source, 0)
	st = requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": st.Revision, "entry": rhythm.Entry{ID: "fact", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}}, source), 200)
	exported := requireState(t, request(t, s, "GET", "/api/export", nil, source), 200)
	target := guest(t, s)
	before := requireState(t, request(t, s, "GET", "/api/state", nil, target), 200)
	exported.Revision = 99999
	exported.Identity = "telegram"
	exported.SessionBinding = "foreign"
	result := requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": before.Revision, "backup": exported}, target), 200)
	if result.Revision != 1 || result.Identity != "guest" || result.SessionBinding != before.SessionBinding || !reflect.DeepEqual(result.Entries, st.Entries) || !reflect.DeepEqual(result.Weeks, st.Weeks) {
		t.Fatalf("restore imported credentials or lost data: %+v", result)
	}
	requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": result.Revision, "backup": exported}, target), 400)
	third := guest(t, s)
	exported.Weeks[0].Plan.Sessions[0].Title = "<script>untrusted</script>"
	requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": exported}, third), 400)
	if empty := requireState(t, request(t, s, "GET", "/api/state", nil, third), 200); empty.Profile != nil || empty.Revision != 0 {
		t.Fatal("invalid restore partially committed")
	}
	if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, source), 200); unchanged.Revision != st.Revision || len(unchanged.Entries) != 1 {
		t.Fatal("restoring another account modified source")
	}
}

func TestHTTPLargeExportRoundTripsBeyondOrdinaryJSONLimit(t *testing.T) {
	s, _ := setup(t, "")
	s.clock = func() time.Time { return testNow().AddDate(0, 0, -7*156) }
	source := guest(t, s)
	saveProfile(t, s, source, 0)
	// Retain the same legitimate cookie principal while its persisted schedule
	// advances. Renew it before advancing the clock past the session lifetime.
	s.clock = testNow
	r := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
	// Test fixture signs the existing identity at the new time, as a user with
	// continuous activity would have obtained through sliding renewal.
	s.clock = func() time.Time { return testNow().AddDate(0, 0, -7*156) }
	r.AddCookie(source)
	id, _, err := s.principal(r, false)
	if err != nil {
		t.Fatal(err)
	}
	s.clock = testNow
	renew := httptest.NewRecorder()
	s.signCookie(renew, id)
	source = renew.Result().Cookies()[0]
	exported := requireState(t, request(t, s, "GET", "/api/export", nil, source), 200)
	if len(exported.Weeks) != 157 {
		t.Fatalf("history weeks=%d", len(exported.Weeks))
	}
	body, err := json.Marshal(map[string]any{"revision": 0, "backup": exported})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 64*1024 {
		t.Fatalf("fixture not large enough: %d", len(body))
	}
	target := guest(t, s)
	result := requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": exported}, target), 200)
	if len(result.Weeks) != 157 {
		t.Fatal("large backup history was truncated")
	}
}

func TestLifecycleRoutesRejectStaleIdentityBinding(t *testing.T) {
	s, _ := setup(t, "")
	a, b := guest(t, s), guest(t, s)
	stateA := saveProfile(t, s, a, 0)
	saveProfile(t, s, b, 0)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/weeks?weekStart=2026-10-12", nil},
		{"POST", "/api/plan/accept", map[string]any{"revision": 1, "weekStart": "2026-10-19", "nextPlanToken": stateA.NextPlanToken}},
		{"DELETE", "/api/plan/accept", map[string]any{"revision": 1, "weekStart": "2026-10-19"}},
		{"DELETE", "/api/entries", map[string]any{"revision": 1, "id": "a-fact"}},
		{"POST", "/api/restore", map[string]any{"revision": 1, "backup": stateA}},
	} {
		data, err := json.Marshal(tc.body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(tc.method, testOrigin+tc.path, bytes.NewReader(data))
		r.AddCookie(b)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(sessionBindingHeader, stateA.SessionBinding)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "session_changed") {
			t.Fatalf("%s %s switched identity: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestStateReadRenewsGuestSessionWithoutRevisionChurn(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	s.clock = func() time.Time { return testNow().AddDate(0, 0, 15) }
	w := request(t, s, "GET", "/api/state", nil, cookie)
	st := requireState(t, w, 200)
	renewed := w.Result().Cookies()
	if len(renewed) != 1 || !renewed[0].Expires.After(cookie.Expires) || st.Revision != 0 {
		t.Fatal("state did not renew session or wrote empty aggregate")
	}
	s.clock = func() time.Time { return testNow().AddDate(0, 0, 31) }
	requireState(t, request(t, s, "GET", "/api/state", nil, renewed[0]), 200)
}
