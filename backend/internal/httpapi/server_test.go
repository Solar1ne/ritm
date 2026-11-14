package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"rhythm/internal/filestore"
	"rhythm/internal/rhythm"
)

const testOrigin = "https://rhythm.example"
const testToken = "12345:synthetic_test_token_never_use_in_deployment"

func testNow() time.Time {
	return time.Date(2026, 10, 17, 12, 0, 0, 0, time.FixedZone("Moscow", 3*3600))
}
func testProfile() rhythm.Profile {
	return rhythm.Profile{Name: "Анна", Goal: "tone", Experience: "beginner", Days: []int{0, 2, 4}, Minutes: 45}
}

func setup(t *testing.T, token string) (*Server, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	store, err := filestore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Repository: store, Secret: []byte(strings.Repeat("s", 32)), BotToken: token, PublicURL: testOrigin, Location: testNow().Location(), Now: testNow, Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Ритм</title>")}, "app.js": {Data: []byte("export const app = true;")}, "app.css": {Data: []byte("body { color: black }")}, "assets/icon.svg": {Data: []byte("<svg></svg>")}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func request(t *testing.T, s *Server, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, testOrigin+path, bytes.NewReader(data))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Origin", testOrigin)
	if cookie != nil {
		r.AddCookie(cookie)
		if id, _, err := s.principal(r, false); err == nil {
			r.Header.Set(sessionBindingHeader, s.sessionBinding(id))
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func requireState(t *testing.T, w *httptest.ResponseRecorder, status int) state {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	var result state
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func guest(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	w := request(t, s, "GET", "/api/state", nil, nil)
	state := requireState(t, w, 200)
	if state.Profile != nil || state.Plan != nil || state.NextPlan != nil || state.Decision != nil || state.Identity != "guest" || state.Entries == nil || len(state.Entries) != 0 {
		t.Fatalf("guest has fabricated data: %+v", state)
	}
	if len(state.SessionBinding) != 64 {
		t.Fatalf("missing session binding: %+v", state)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %v", cookies)
	}
	return cookies[0]
}

func saveProfile(t *testing.T, s *Server, cookie *http.Cookie, revision uint64) state {
	t.Helper()
	w := request(t, s, "POST", "/api/profile", map[string]any{"revision": revision, "profile": testProfile()}, cookie)
	return requireState(t, w, 200)
}

func TestFullFlowIsolationExportReset(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge <= 0 {
		t.Fatalf("insecure cookie: %+v", cookie)
	}
	current := saveProfile(t, s, cookie, 0)
	if current.Revision != 1 || current.Plan.TotalMinutes != 135 || current.Decision.Mode != "start" {
		t.Fatalf("profile result: %+v", current)
	}
	other := guest(t, s)
	if other.Value == cookie.Value {
		t.Fatal("guest identities reused")
	}
	entry := rhythm.Entry{ID: "fact-1", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}
	body := map[string]any{"revision": 1, "entry": entry}
	current = requireState(t, request(t, s, "POST", "/api/entries", body, cookie), 200)
	if current.Revision != 2 || len(current.Entries) != 1 || current.Plan.Sessions[2].Status != "done" {
		t.Fatalf("entry result: %+v", current)
	}
	retry := requireState(t, request(t, s, "POST", "/api/entries", body, cookie), 200)
	if retry.Revision != 2 || len(retry.Entries) != 1 {
		t.Fatal("retry duplicated data")
	}
	entry.Status = "missed"
	requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": 2, "entry": entry}, cookie), 409)
	requireState(t, request(t, s, "POST", "/api/profile", map[string]any{"revision": 1, "profile": testProfile()}, cookie), 409)
	isolated := requireState(t, request(t, s, "GET", "/api/state", nil, other), 200)
	if isolated.Profile != nil || len(isolated.Entries) != 0 {
		t.Fatal("guest data leaked")
	}
	export := request(t, s, "GET", "/api/export", nil, cookie)
	if got := requireState(t, export, 200); got.Revision != 2 || len(got.Entries) != 1 {
		t.Fatal("export differs from state")
	}
	if !strings.Contains(export.Header().Get("Content-Disposition"), "attachment") || export.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("export is not a private download")
	}
	reset := requireState(t, request(t, s, "DELETE", "/api/data", map[string]any{"revision": 2}, cookie), 200)
	if reset.Profile != nil || len(reset.Entries) != 0 || reset.Revision != 3 {
		t.Fatalf("reset: %+v", reset)
	}
	loaded := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if loaded.Profile != nil || loaded.Revision != 3 {
		t.Fatal("reset was not persisted")
	}
}

func TestPreviewIsSyntheticAndAllowsFutureWithinWeek(t *testing.T) {
	s, dir := setup(t, "")
	e := rhythm.Entry{ID: "preview", Date: "2026-10-18", Status: "wellbeing", Sleep: floatPointer(5)}
	w := request(t, s, "POST", "/api/preview", map[string]any{"profile": testProfile(), "entries": []rhythm.Entry{e}}, nil)
	if w.Code != 200 {
		t.Fatalf("preview: %s", w.Body.String())
	}
	var projection rhythm.Projection
	if err := json.Unmarshal(w.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Decision.Mode != "recover" || projection.NextPlan.TotalMinutes != 72 {
		t.Fatalf("preview: %+v", projection)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 || len(w.Result().Cookies()) != 0 {
		t.Fatal("preview persisted or created a session")
	}
	cookie := guest(t, s)
	saveProfile(t, s, cookie, 0)
	requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": 1, "entry": e}, cookie), 400)
}
func floatPointer(v float64) *float64 { return &v }

func TestStrictValidationAndCSRF(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	valid := `{"revision":0,"profile":{"name":"Анна","goal":"tone","experience":"beginner","days":[0,2,4],"minutes":45}}`
	for _, tc := range []struct {
		name, body, contentType, origin, fetch string
		status                                 int
	}{
		{"cross_origin", valid, "application/json", "https://evil.example", "", 403},
		{"opaque_origin", valid, "application/json", "null", "", 403},
		{"cross_site_fetch", valid, "application/json", testOrigin, "cross-site", 403},
		{"same_site_not_origin", valid, "application/json", testOrigin, "same-site", 403},
		{"text_plain", valid, "text/plain", testOrigin, "", 415},
		{"empty", "", "application/json", testOrigin, "", 400},
		{"null", "null", "application/json", testOrigin, "", 400},
		{"missing_revision", `{"profile":{"goal":"tone","experience":"beginner","days":[0],"minutes":30}}`, "application/json", testOrigin, "", 400},
		{"null_profile", `{"revision":0,"profile":null}`, "application/json", testOrigin, "", 400},
		{"unknown_field", strings.TrimSuffix(valid, "}") + `,"secret":true}`, "application/json", testOrigin, "", 400},
		{"trailing", valid + ` {}`, "application/json", testOrigin, "", 400},
		{"negative_revision", strings.Replace(valid, `"revision":0`, `"revision":-1`, 1), "application/json", testOrigin, "", 400},
		{"invalid_goal", strings.Replace(valid, `"tone"`, `"unknown"`, 1), "application/json", testOrigin, "", 400},
		{"large_body", strings.Repeat(" ", 65*1024) + valid, "application/json", testOrigin, "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", testOrigin+"/api/profile", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.fetch)
			r.AddCookie(cookie)
			id, _, err := s.principal(r, false)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set(sessionBindingHeader, s.sessionBinding(id))
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	r := httptest.NewRequest("POST", testOrigin+"/api/profile", strings.NewReader(valid))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.AddCookie(cookie)
	id, _, err := s.principal(r, false)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(sessionBindingHeader, s.sessionBinding(id))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	requireState(t, w, 200)
}

func TestAbsentAndTamperedSessions(t *testing.T) {
	s, _ := setup(t, "")
	requireState(t, request(t, s, "POST", "/api/profile", map[string]any{"revision": 0, "profile": testProfile()}, nil), 401)
	requireState(t, request(t, s, "GET", "/api/export", nil, nil), 401)
	cookie := guest(t, s)
	cookie.Value += "tamper"
	requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 401)
	cookie = guest(t, s)
	s.clock = func() time.Time { return testNow().Add(31 * 24 * time.Hour) }
	requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 401)
}

func TestStorageDegradationAndReadiness(t *testing.T) {
	s, dir := setup(t, "")
	health := request(t, s, "GET", "/api/health", nil, nil)
	if health.Code != 200 || len(health.Result().Cookies()) != 0 {
		t.Fatal("health creates session")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("health persists guest")
	}
	cookie := guest(t, s)
	saveProfile(t, s, cookie, 0)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/state", "/api/health", "/api/export"} {
		w := request(t, s, "GET", path, nil, cookie)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "storage_unavailable") {
			t.Fatalf("%s masks storage loss: %d %s", path, w.Code, w.Body.String())
		}
	}
	requireState(t, request(t, s, "POST", "/api/profile", map[string]any{"revision": 1, "profile": testProfile()}, cookie), 503)
}

func signedTelegram(t *testing.T, userID int64, date time.Time, extra url.Values) string {
	t.Helper()
	values := url.Values{"auth_date": {strconv.FormatInt(date.Unix(), 10)}, "user": {fmt.Sprintf(`{"id":%d,"first_name":"Синтетический"}`, userID)}, "query_id": {"fixture-query"}}
	for k, v := range extra {
		values[k] = v
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+values.Get(k))
	}
	keyHMAC := hmac.New(sha256.New, []byte("WebAppData"))
	keyHMAC.Write([]byte(testToken))
	dataHMAC := hmac.New(sha256.New, keyHMAC.Sum(nil))
	dataHMAC.Write([]byte(strings.Join(lines, "\n")))
	values.Set("hash", hex.EncodeToString(dataHMAC.Sum(nil)))
	return values.Encode()
}

func TestTelegramAuthStableIsolatedAndNoGuestMerge(t *testing.T) {
	s, _ := setup(t, testToken)
	guestCookie := guest(t, s)
	saveProfile(t, s, guestCookie, 0)
	initData := signedTelegram(t, 123456789, testNow(), nil)
	w := request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": initData}, guestCookie)
	tgState := requireState(t, w, 200)
	if tgState.Identity != "telegram" || tgState.Profile != nil {
		t.Fatal("Telegram silently merged guest or is not verified")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 || cookies[0].MaxAge != -1 || cookies[0].Partitioned {
		t.Fatalf("old unpartitioned cookie was not expired first: %+v", cookies)
	}
	tgCookie := cookies[1]
	if !tgCookie.Secure || !tgCookie.HttpOnly || tgCookie.SameSite != http.SameSiteNoneMode || !tgCookie.Partitioned {
		t.Fatalf("Telegram iframe cookie is not protected and partitioned: %+v", tgCookie)
	}
	if tgCookie.Value == guestCookie.Value {
		t.Fatal("identity did not rotate")
	}
	saveProfile(t, s, tgCookie, 0)
	second := request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": initData}, nil)
	if st := requireState(t, second, 200); st.Profile == nil || st.Revision != 1 {
		t.Fatal("Telegram identity not stable")
	}
	other := request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": signedTelegram(t, 987654321, testNow(), nil)}, nil)
	if st := requireState(t, other, 200); st.Profile != nil {
		t.Fatal("Telegram users share data")
	}
	if st := requireState(t, request(t, s, "GET", "/api/state", nil, guestCookie), 200); st.Identity != "guest" || st.Profile == nil {
		t.Fatal("guest data overwritten on Telegram auth")
	}
}

func TestTelegramRejectsInvalidSignaturesAndDates(t *testing.T) {
	s, _ := setup(t, testToken)
	valid := signedTelegram(t, 123, testNow(), nil)
	for _, tc := range []struct{ name, data string }{
		{"tampered", strings.Replace(valid, "fixture-query", "changed-query", 1)},
		{"expired", signedTelegram(t, 123, testNow().Add(-time.Hour-time.Second), nil)},
		{"future", signedTelegram(t, 123, testNow().Add(31*time.Second), nil)},
		{"duplicate", valid + "&auth_date=" + strconv.FormatInt(testNow().Unix(), 10)},
		{"zero_id", signedTelegram(t, 0, testNow(), nil)},
		{"negative_id", signedTelegram(t, -123, testNow(), nil)},
		{"huge_id", signedTelegram(t, 1<<53, testNow(), nil)},
		{"bot", signedTelegram(t, 123, testNow(), url.Values{"user": {`{"id":123,"is_bot":true}`}})},
		{"missing_hash", "auth_date=1&user=%7B%7D"},
		{"invalid_encoding", "%invalid"},
		{"oversized", strings.Repeat("x", 17*1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": tc.data}, nil)
			requireState(t, w, 401)
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("failed Telegram auth set session")
			}
		})
	}
	unconfigured, _ := setup(t, "")
	requireState(t, request(t, unconfigured, "POST", "/api/auth/telegram", map[string]any{"initData": valid}, nil), 503)
}

func TestConcurrentAPIUpdatesHaveOneWinner(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	var wg sync.WaitGroup
	codes := make(chan int, 10)
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			w := request(t, s, "POST", "/api/profile", map[string]any{"revision": 0, "profile": testProfile()}, cookie)
			codes <- w.Code
		})
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		switch code {
		case 200:
			success++
		case 409:
			conflict++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if success != 1 || conflict != 9 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestStaticSPASecurityHeadersAndAPINotFound(t *testing.T) {
	s, _ := setup(t, "")
	for _, path := range []string{"/", "/mini", "/some-client-route"} {
		w := request(t, s, "GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Ритм") {
			t.Fatalf("SPA %s: %d", path, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "https://telegram.org") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self' https://web.telegram.org https://*.telegram.org") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing security headers")
		}
	}
	for _, path := range []string{"/missing.js", "/assets", "/api/unknown"} {
		w := request(t, s, "GET", path, nil, nil)
		if w.Code != 404 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	if w := request(t, s, "GET", "/app.css", nil, nil); !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("CSS type=%s", w.Header().Get("Content-Type"))
	}
	if w := request(t, s, "GET", "/app.js", nil, nil); !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("JS type=%s", w.Header().Get("Content-Type"))
	}
}

func TestConfigurationValidation(t *testing.T) {
	store, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := Config{Repository: store, Secret: []byte(strings.Repeat("s", 32)), Location: time.UTC}
	for _, origin := range []string{"https://user:pass@example.com", "https://example.com/path", "https://example.com?token=x", "http://example.com", "javascript:alert(1)"} {
		c := base
		c.PublicURL = origin
		if _, err := New(c); err == nil {
			t.Fatalf("accepted origin %s", origin)
		}
	}
	for _, origin := range []string{"http://127.0.0.1:8080", "https://example.com/"} {
		c := base
		c.PublicURL = origin
		if _, err := New(c); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Check(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestSessionBindingBlocksCrossTabIdentityChanges(t *testing.T) {
	s, _ := setup(t, "")
	cookieA, cookieB := guest(t, s), guest(t, s)
	a := saveProfile(t, s, cookieA, 0)
	b := saveProfile(t, s, cookieB, 0)
	if a.Revision != b.Revision || a.SessionBinding == b.SessionBinding {
		t.Fatal("fixture must have same revisions and distinct bindings")
	}
	modified := testProfile()
	modified.Name = "Чужая вкладка"
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"profile", "POST", "/api/profile", map[string]any{"revision": 1, "profile": modified}},
		{"entry", "POST", "/api/entries", map[string]any{"revision": 1, "entry": rhythm.Entry{ID: "cross-account", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}}},
		{"reset", "DELETE", "/api/data", map[string]any{"revision": 1}},
		{"export", "GET", "/api/export", nil},
		{"state_refresh", "GET", "/api/state", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(tc.method, testOrigin+tc.path, bytes.NewReader(data))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", testOrigin)
			r.Header.Set(sessionBindingHeader, a.SessionBinding)
			r.AddCookie(cookieB)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"session_changed"`) {
				t.Fatalf("cross-account %s accepted: %d %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
	current := requireState(t, request(t, s, "GET", "/api/state", nil, cookieB), 200)
	if current.Profile.Name != testProfile().Name || current.Revision != 1 || len(current.Entries) != 0 {
		t.Fatalf("stale tab changed B: %+v", current)
	}
	for _, path := range []string{"/api/export", "/api/profile"} {
		method, body := "GET", ""
		if path == "/api/profile" {
			method = "POST"
			data, err := json.Marshal(map[string]any{"revision": 1, "profile": modified})
			if err != nil {
				t.Fatal(err)
			}
			body = string(data)
		}
		r := httptest.NewRequest(method, testOrigin+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookieB)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "session_changed") {
			t.Fatalf("missing binding accepted for %s", path)
		}
	}
	// Bootstrap is the only authenticated-state read that allows no binding.
	r := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
	r.AddCookie(cookieB)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if bootstrap := requireState(t, w, 200); bootstrap.SessionBinding != b.SessionBinding {
		t.Fatal("bootstrap did not bind current identity")
	}
}

func TestAmbiguousSessionCookiesAreRejected(t *testing.T) {
	s, _ := setup(t, "")
	a, b := guest(t, s), guest(t, s)
	r := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
	r.AddCookie(a)
	r.AddCookie(b)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	requireState(t, w, 401)
}
