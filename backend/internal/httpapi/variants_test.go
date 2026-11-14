package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"rhythm/internal/assistant"
	"rhythm/internal/rhythm"
)

func variantHTTPProfile(t *testing.T, s *Server, cookie *http.Cookie) state {
	t.Helper()
	profile := testProfile()
	profile.Days = []int{0, 2, 5} // testNow is Saturday; today is a known, open session.
	return requireState(t, request(t, s, http.MethodPost, "/api/profile", map[string]any{"revision": 0, "profile": profile}, cookie), http.StatusOK)
}

func variantHTTPBody(revision uint64, sessionID, variantID, environment string, minutes int, lowImpact bool) map[string]any {
	return map[string]any{"revision": revision, "sessionId": sessionID, "variantId": variantID, "constraints": rhythm.SessionConstraints{AvailableMinutes: minutes, Environment: environment, LowImpact: lowImpact}}
}

func previewHTTPVariant(t *testing.T, s *Server, cookie *http.Cookie, body any) rhythm.SessionVariantPreview {
	t.Helper()
	w := request(t, s, http.MethodPost, "/api/sessions/variants", body, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	var result rhythm.SessionVariantPreview
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHTTPVariantPreviewApplyRetryCASAndRevert(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	initial := variantHTTPProfile(t, s, cookie)
	body := variantHTTPBody(initial.Revision, "2026-10-17", "home-mobility-v1", "home", 30, true)
	preview := previewHTTPVariant(t, s, cookie, body)
	if len(preview.Options) != 1 || preview.Options[0].ID != "home-mobility-v1" || preview.Options[0].Minutes != 30 || preview.Original.Minutes != 45 || preview.Current.Minutes != 45 || preview.Adjustment != nil {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if unchanged := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, initial) {
		t.Fatal("variant preview persisted changes")
	}
	first := requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", body, cookie), 200)
	if first.Revision != initial.Revision+1 || first.Plan.TotalMinutes != 120 || len(first.SessionAdjustments) != 1 || first.Plan.Sessions[2].VariantID != "home-mobility-v1" || first.Plan.Sessions[2].OriginalMinutes != 45 || !reflect.DeepEqual(first.Weeks, initial.Weeks) {
		t.Fatalf("apply lost snapshot or effective projection: %+v", first)
	}
	if retry := requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", body, cookie), 200); !reflect.DeepEqual(retry, first) {
		t.Fatal("lost-response retry changed the applied choice or revision")
	}
	preview = previewHTTPVariant(t, s, cookie, variantHTTPBody(first.Revision, "2026-10-17", "", "home", 30, true))
	if preview.Adjustment == nil || preview.Current.Minutes != 30 || preview.Original.Minutes != 45 {
		t.Fatal("preview did not preserve current and original separately")
	}
	secondBody := variantHTTPBody(first.Revision, "2026-10-17", "outdoors-walk-v1", "outdoors", 20, true)
	second := requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", secondBody, cookie), 200)
	if second.Plan.TotalMinutes != 110 || second.SessionAdjustments[0].VariantID != "outdoors-walk-v1" {
		t.Fatal("second explicit variant was not applied")
	}
	requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", body, cookie), 409)
	requireState(t, request(t, s, http.MethodPost, "/api/sessions/variants", body, cookie), 409)
	requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", map[string]any{"revision": first.Revision, "sessionId": "2026-10-17"}, cookie), 409)
	resetBody := map[string]any{"revision": second.Revision, "sessionId": "2026-10-17"}
	reverted := requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", resetBody, cookie), 200)
	if len(reverted.SessionAdjustments) != 0 || reverted.Plan.TotalMinutes != 135 || reverted.Plan.Sessions[2].VariantID != "" || !reflect.DeepEqual(reverted.Weeks, initial.Weeks) {
		t.Fatal("explicit reset did not restore original schedule")
	}
	if retry := requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", resetBody, cookie), 200); !reflect.DeepEqual(retry, reverted) {
		t.Fatal("duplicate reset was not idempotent")
	}
	third := requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(reverted.Revision, "2026-10-17", "home-mobility-v1", "home", 15, true), cookie), 200)
	requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", resetBody, cookie), 409)
	if got := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(got, third) {
		t.Fatal("stale reset erased a later choice")
	}
}

func variantICSEvent(text, start string) string {
	text = strings.ReplaceAll(text, "\r\n ", "")
	for _, chunk := range strings.Split(text, "BEGIN:VEVENT\r\n") {
		if strings.Contains(chunk, "DTSTART:"+start+"\r\n") {
			event, _, _ := strings.Cut(chunk, "END:VEVENT")
			return event
		}
	}
	return ""
}

func TestHTTPVariantsReachCalendarHistoryRulesAssistantAndAIContext(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	before := variantHTTPProfile(t, s, cookie)
	beforeICS := request(t, s, http.MethodGet, "/api/calendar", nil, cookie)
	if beforeICS.Code != 200 {
		t.Fatal(beforeICS.Body.String())
	}
	current := requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(before.Revision, "2026-10-17", "home-mobility-v1", "home", 30, true), cookie), 200)
	ics := request(t, s, http.MethodGet, "/api/calendar", nil, cookie)
	if ics.Code != 200 || !reflect.DeepEqual(calendarUIDs(ics.Body.String()), calendarUIDs(beforeICS.Body.String())) {
		t.Fatal("calendar rejected variant or duplicated event identity")
	}
	event := variantICSEvent(ics.Body.String(), "20261017T150000Z")
	if !strings.Contains(event, "DTEND:20261017T153000Z\r\n") || !strings.Contains(event, "SUMMARY:Мягкая подвижность дома\r\n") || strings.Contains(event, "DTEND:20261017T154500Z") {
		t.Fatalf("calendar exported original instead of applied session: %s", event)
	}
	w := request(t, s, http.MethodGet, "/api/weeks?weekStart=2026-10-12", nil, cookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var history rhythm.WeekView
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.Plan.TotalMinutes != 120 || history.Plan.Sessions[2].Minutes != 30 || history.Plan.Sessions[2].VariantID == "" {
		t.Fatal("week view ignored the applied variant")
	}
	w = request(t, s, http.MethodPost, "/api/assistant", map[string]any{"question": "Что у меня сегодня?"}, cookie)
	var answer assistant.Response
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Mode != "rules" || !strings.Contains(answer.Text, "Мягкая подвижность дома, 30 мин") || strings.Contains(answer.Text, "45 мин") {
		t.Fatalf("rules assistant received stale baseline: %+v", answer)
	}
	w = request(t, s, http.MethodGet, "/api/ai/context?task=ask", nil, cookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var aiView aiContextView
	if err := json.Unmarshal(w.Body.Bytes(), &aiView); err != nil {
		t.Fatal(err)
	}
	if aiView.Revision != current.Revision || aiView.ContextToken == "" {
		t.Fatal("AI context was not tied to current revision")
	}
	found := false
	for _, fact := range aiView.Context.Facts {
		if strings.HasPrefix(fact.Text, "Текущий план: 2026-10-17,") {
			found = true
			if !strings.Contains(fact.Text, "Мягкая подвижность дома, 30 плановых минут") || strings.Contains(fact.Text, "45 плановых минут") {
				t.Fatalf("AI received original instead of chosen session: %s", fact.Text)
			}
		}
	}
	if !found {
		t.Fatal("AI context omitted today's chosen session")
	}
	if after := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(after, current) {
		t.Fatal("calendar, history or assistant context read changed persisted data")
	}
}

func TestHTTPVariantExportRestoreAcrossAccountsAndTamperRejection(t *testing.T) {
	s, _ := setup(t, "")
	source := guest(t, s)
	st := variantHTTPProfile(t, s, source)
	st = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "home", 30, true), source), 200)
	exported := requireState(t, request(t, s, http.MethodGet, "/api/export", nil, source), 200)
	if len(exported.SessionAdjustments) != 1 || exported.Weeks[0].Plan.Sessions[2].Minutes != 45 || exported.Plan.Sessions[2].Minutes != 30 {
		t.Fatal("export lost immutable original or applied adjustment")
	}
	target := guest(t, s)
	restored := requireState(t, request(t, s, http.MethodPost, "/api/restore", map[string]any{"revision": 0, "backup": exported}, target), 200)
	if restored.Revision != 1 || restored.SessionBinding == exported.SessionBinding || !reflect.DeepEqual(restored.SessionAdjustments, exported.SessionAdjustments) || !reflect.DeepEqual(restored.Plan, exported.Plan) || !reflect.DeepEqual(restored.Weeks, exported.Weeks) {
		t.Fatal("restore imported foreign revision/identity or lost effective schedule")
	}
	changed := requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", map[string]any{"revision": restored.Revision, "sessionId": "2026-10-17"}, target), 200)
	if changed.Plan.TotalMinutes != 135 {
		t.Fatal("restored account could not independently revert")
	}
	if unchanged := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, source), 200); !reflect.DeepEqual(unchanged, st) {
		t.Fatal("reverting restored account changed source account")
	}
	for _, mutate := range []func(*state){
		func(s *state) { s.SessionAdjustments[0].VariantID = "injected-ai-exercise" },
		func(s *state) { s.SessionAdjustments[0].Minutes = 90 },
		func(s *state) { s.SessionAdjustments[0].Environment = "gym" },
		func(s *state) { s.Weeks[0].Plan.Sessions[2].VariantID = "home-mobility-v1" },
	} {
		backup := requireState(t, request(t, s, http.MethodGet, "/api/export", nil, source), 200)
		mutate(&backup)
		empty := guest(t, s)
		requireState(t, request(t, s, http.MethodPost, "/api/restore", map[string]any{"revision": 0, "backup": backup}, empty), 400)
		if unchanged := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, empty), 200); unchanged.Profile != nil || unchanged.Revision != 0 || len(unchanged.SessionAdjustments) != 0 {
			t.Fatal("invalid backup partially committed")
		}
	}
}

func TestHTTPAcceptedVariantCancellationRemovesOnlyThatWeek(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := variantHTTPProfile(t, s, cookie)
	st = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "home", 30, true), cookie), 200)
	st = requireState(t, request(t, s, http.MethodPost, "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.NextPlan.WeekStart, "nextPlanToken": st.NextPlanToken}, cookie), 200)
	st = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-19", "outdoors-walk-v1", "outdoors", 20, true), cookie), 200)
	if st.AcceptedNextPlan.Sessions[0].Minutes != 20 || len(st.SessionAdjustments) != 2 || st.NextPlan.Sessions[0].Minutes != 45 {
		t.Fatal("accepted next variant corrupted proposal or was not applied")
	}
	ics := request(t, s, http.MethodGet, "/api/calendar?weekStart=2026-10-19", nil, cookie)
	if ics.Code != 200 || !strings.Contains(variantICSEvent(ics.Body.String(), "20261019T150000Z"), "DTEND:20261019T152000Z") {
		t.Fatal("accepted calendar did not use applied duration")
	}
	st = requireState(t, request(t, s, http.MethodDelete, "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": "2026-10-19"}, cookie), 200)
	if st.AcceptedNextPlan != nil || len(st.SessionAdjustments) != 1 || st.SessionAdjustments[0].SessionID != "2026-10-17" {
		t.Fatal("cancel retained orphan adjustments or erased current week")
	}
	st = requireState(t, request(t, s, http.MethodPost, "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.NextPlan.WeekStart, "nextPlanToken": st.NextPlanToken}, cookie), 200)
	if st.AcceptedNextPlan.Sessions[0].Minutes != 45 || st.AcceptedNextPlan.Sessions[0].VariantID != "" || len(st.SessionAdjustments) != 1 {
		t.Fatal("reaccept resurrected cancelled variant")
	}
}

func TestHTTPVariantsRejectInvalidInputRecoveryBypassAndCompletedChanges(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := variantHTTPProfile(t, s, cookie)
	for _, body := range []any{
		map[string]any{"sessionId": "2026-10-17"},
		map[string]any{"revision": st.Revision},
		map[string]any{"revision": st.Revision, "sessionId": "2026-10-17", "constraints": nil},
		variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "home", 14, true),
		variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "home", 46, true),
		variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "unknown", 30, true),
		variantHTTPBody(st.Revision, "2026-10-14", "home-mobility-v1", "home", 30, true),
		variantHTTPBody(st.Revision, "2026-10-19", "home-mobility-v1", "home", 30, true),
		variantHTTPBody(st.Revision, "2026-10-17", "arbitrary-instruction", "home", 30, true),
	} {
		requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", body, cookie), 400)
	}
	if unchanged := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, st) {
		t.Fatal("invalid input partially changed account")
	}
	st = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-17", "home-bodyweight-v1", "home", 45, false), cookie), 200)
	fatigue := 9
	st = requireState(t, request(t, s, http.MethodPost, "/api/entries", map[string]any{"revision": st.Revision, "entry": rhythm.Entry{ID: "recovery", Date: "2026-10-17", Status: "wellbeing", Fatigue: &fatigue}}, cookie), 200)
	preview := previewHTTPVariant(t, s, cookie, variantHTTPBody(st.Revision, "2026-10-17", "", "home", 45, false))
	if !preview.RecoveryRestricted || len(preview.Options) != 1 || preview.Options[0].Minutes != 36 {
		t.Fatal("HTTP preview bypassed recovery constraints")
	}
	requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-17", "home-bodyweight-v1", "home", 45, false), cookie), 400)
	requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", map[string]any{"revision": st.Revision, "sessionId": "2026-10-17"}, cookie), 400)
	st = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "home", 45, false), cookie), 200)
	if st.Plan.Sessions[2].Minutes != 36 {
		t.Fatal("canonical recovery duration was not applied")
	}
	st = requireState(t, request(t, s, http.MethodPost, "/api/entries", map[string]any{"revision": st.Revision, "entry": rhythm.Entry{ID: "done-today", Date: "2026-10-17", SessionID: "2026-10-17", Status: "done"}}, cookie), 200)
	requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, "2026-10-17", "home-mobility-v1", "home", 15, true), cookie), 400)
	requireState(t, request(t, s, http.MethodDelete, "/api/sessions/variant", map[string]any{"revision": st.Revision, "sessionId": "2026-10-17"}, cookie), 400)
	if unchanged := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, st) {
		t.Fatal("completed-session rejection changed effective history")
	}
}

func TestHTTPVariantsRequireIdentityBindingAndPreserveIsolation(t *testing.T) {
	s, _ := setup(t, "")
	a, b := guest(t, s), guest(t, s)
	stateA, stateB := variantHTTPProfile(t, s, a), variantHTTPProfile(t, s, b)
	body := `{"revision":1,"sessionId":"2026-10-17","variantId":"home-mobility-v1","constraints":{"availableMinutes":30,"environment":"home","lowImpact":true}}`
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/sessions/variants"},
		{http.MethodPost, "/api/sessions/variant"},
		{http.MethodDelete, "/api/sessions/variant"},
	} {
		for _, tc := range []struct {
			name    string
			cookie  *http.Cookie
			binding string
			origin  string
			body    string
			status  int
		}{
			{"unauthenticated", nil, "", testOrigin, body, 401},
			{"missing_binding", b, "", testOrigin, body, 409},
			{"wrong_account_binding", b, stateA.SessionBinding, testOrigin, body, 409},
			{"cross_origin", b, stateB.SessionBinding, "https://attacker.invalid", body, 403},
			{"malformed_json", b, stateB.SessionBinding, testOrigin, "{", 400},
			{"unknown_field", b, stateB.SessionBinding, testOrigin, strings.TrimSuffix(body, "}") + `,"injected":"ignored?"}`, 400},
		} {
			t.Run(route.method+route.path+"/"+tc.name, func(t *testing.T) {
				r := httptest.NewRequest(route.method, testOrigin+route.path, strings.NewReader(tc.body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", tc.origin)
				r.Header.Set(sessionBindingHeader, tc.binding)
				if tc.cookie != nil {
					r.AddCookie(tc.cookie)
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
				}
			})
		}
	}
	stateA = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(stateA.Revision, "2026-10-17", "home-mobility-v1", "home", 30, true), a), 200)
	if unchanged := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, b), 200); !reflect.DeepEqual(unchanged, stateB) || len(unchanged.SessionAdjustments) != 0 || unchanged.Plan.Sessions[2].Minutes != 45 {
		t.Fatal("variant operations crossed account boundary")
	}
	if stateA.Plan.Sessions[2].Minutes != 30 {
		t.Fatal("authorized account mutation did not work")
	}
}
