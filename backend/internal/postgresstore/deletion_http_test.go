package postgresstore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rhythm/internal/httpapi"
)

func TestHTTPAccountDeletionSuccessAndRevisionConflict(t *testing.T) {
	s, _ := testDatabase(t, true)
	h, err := httpapi.New(httpapi.Config{Repository: s, Accounts: service(t, s), Storage: "postgresql", Secret: []byte(strings.Repeat("x", 32)), Location: time.UTC, PublicURL: "https://rhythm.test"})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body, binding string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://rhythm.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", "https://rhythm.test")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Rhythm-Session", binding)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	guestState := do("GET", "/api/state", "", "", nil)
	if guestState.Code != 200 {
		t.Fatal(guestState.Code)
	}
	g := guestState.Result().Cookies()[0]
	var state struct {
		SessionBinding string `json:"sessionBinding"`
	}
	if err := json.Unmarshal(guestState.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	registered := do("POST", "/api/auth/register", `{"login":"owner","password":"four quiet mornings together","revision":0,"keepGuestData":true}`, state.SessionBinding, g)
	if registered.Code != 200 {
		t.Fatalf("register: %d %s", registered.Code, registered.Body.String())
	}
	if err := json.Unmarshal(registered.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	var accountCookie *http.Cookie
	for _, c := range registered.Result().Cookies() {
		if c.Value != "" {
			accountCookie = c
		}
	}
	if accountCookie == nil {
		t.Fatal("missing account cookie")
	}
	conflict := do("DELETE", "/api/account", `{"password":"four quiet mornings together","revision":1,"confirmDelete":true}`, state.SessionBinding, accountCookie)
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "revision_conflict") || len(conflict.Result().Cookies()) != 0 {
		t.Fatalf("conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	deleted := do("DELETE", "/api/account", `{"password":"four quiet mornings together","revision":0,"confirmDelete":true}`, state.SessionBinding, accountCookie)
	if deleted.Code != 200 || strings.TrimSpace(deleted.Body.String()) != `{"accountDeleted":true}` {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	cookies := deleted.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected both cookie partitions cleared: %d", len(cookies))
	}
	for _, c := range cookies {
		if c.MaxAge != -1 || c.Value != "" {
			t.Fatal("deletion retained a cookie")
		}
	}
	if stale := do("GET", "/api/state", "", state.SessionBinding, accountCookie); stale.Code != 401 {
		t.Fatalf("revoked account session: %d", stale.Code)
	}
	if stale := do("GET", "/api/state", "", "", g); stale.Code != 401 {
		t.Fatalf("former guest session: %d", stale.Code)
	}
	if fresh := do("GET", "/api/state", "", "", nil); fresh.Code != 200 || !strings.Contains(fresh.Body.String(), `"identity":"guest"`) {
		t.Fatalf("fresh guest: %d %s", fresh.Code, fresh.Body.String())
	}
}
