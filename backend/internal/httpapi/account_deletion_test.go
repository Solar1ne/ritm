package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountDeletionRequiresReviewedAccountAndPreservesUnsupportedFileStore(t *testing.T) {
	s, dir := setupAccounts(t)
	g := guest(t, s)
	current, cookie, _ := registerAccount(t, s, "owner", g, 0)
	ledgerBefore, err := os.ReadFile(filepath.Join(dir, ".accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{"password": accountPassword, "revision": current.Revision, "confirmDelete": true}
	for _, tc := range []struct {
		name string
		body map[string]any
		want int
		code string
	}{
		{"confirmation", map[string]any{"password": accountPassword, "revision": current.Revision}, 400, "account_delete_confirmation"},
		{"revision", map[string]any{"password": accountPassword, "confirmDelete": true}, 400, "account_delete_confirmation"},
		{"password", map[string]any{"password": "wrong password phrase", "revision": current.Revision, "confirmDelete": true}, 401, "credentials"},
		{"file storage", valid, 503, "accounts_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, s, "DELETE", "/api/account", tc.body, cookie)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("failed deletion changed session cookies")
			}
		})
	}
	ledgerAfter, err := os.ReadFile(filepath.Join(dir, ".accounts.json"))
	if err != nil || !bytes.Equal(ledgerBefore, ledgerAfter) {
		t.Fatalf("unsupported deletion changed ledger: %v", err)
	}
	if st := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); st.Identity != "account" {
		t.Fatal("account lost after rejection")
	}
	if w := request(t, s, "DELETE", "/api/account", valid, guest(t, s)); w.Code != 400 || !strings.Contains(w.Body.String(), "account_delete_unsupported") {
		t.Fatalf("guest deletion: %d %s", w.Code, w.Body.String())
	}
	tg := request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": signedTelegram(t, 123, testNow(), nil)}, nil)
	if w := request(t, s, "DELETE", "/api/account", valid, accountCookie(t, tg)); w.Code != 400 || !strings.Contains(w.Body.String(), "account_delete_unsupported") {
		t.Fatalf("Telegram deletion: %d %s", w.Code, w.Body.String())
	}
}

func TestAccountDeletionOriginBindingAndJSONBoundaries(t *testing.T) {
	s, _ := setupAccounts(t)
	_, cookie, _ := registerAccount(t, s, "owner", guest(t, s), 0)
	for _, tc := range []struct {
		name, origin, binding, contentType, body string
		want                                     int
	}{
		{"origin", "https://untrusted.invalid", "", "application/json", `{}`, 403},
		{"binding", testOrigin, "another-account", "application/json", `{"password":"four quiet mornings together","revision":0,"confirmDelete":true}`, 409},
		{"content type", testOrigin, "", "text/plain", `{}`, 415},
		{"unknown field", testOrigin, "", "application/json", `{"unexpected":true}`, 400},
		{"large body", testOrigin, "", "application/json", `{"password":"` + strings.Repeat("a", 4096) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodDelete, testOrigin+"/api/account", strings.NewReader(tc.body))
			r.AddCookie(cookie)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set(sessionBindingHeader, tc.binding)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("rejected request changed cookies")
			}
		})
	}
}
