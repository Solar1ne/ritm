package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rhythm/internal/accounts"
	"rhythm/internal/filestore"
	"rhythm/internal/rhythm"
)

const accountPassword = "four quiet mornings together"
const changedPassword = "another gentle week begins"

func setupAccounts(t *testing.T) (*Server, string) {
	t.Helper()
	s, dir := setup(t, testToken)
	service, err := accounts.New(s.repo.(accounts.Repository))
	if err != nil {
		t.Fatal(err)
	}
	s.accounts = service
	return s, dir
}

func accountCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Value != "" && c.MaxAge > 0 {
			return c
		}
	}
	t.Fatal("no active session cookie")
	return nil
}

func registerAccount(t *testing.T, s *Server, login string, guestCookie *http.Cookie, revision uint64) (state, *http.Cookie, string) {
	t.Helper()
	w := request(t, s, "POST", "/api/auth/register", map[string]any{"login": login, "password": accountPassword, "revision": revision, "keepGuestData": true}, guestCookie)
	current := requireState(t, w, 200)
	var response struct {
		RecoveryCode string `json:"recoveryCode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.RecoveryCode) != 39 || current.Identity != "account" || !current.AccountsEnabled {
		t.Fatal("registration did not return a real account and recovery code")
	}
	return current, accountCookie(t, w), response.RecoveryCode
}

func loginAccount(t *testing.T, s *Server, login, password string) (state, *http.Cookie) {
	t.Helper()
	w := request(t, s, "POST", "/api/auth/login", map[string]any{"login": login, "password": password}, nil)
	return requireState(t, w, 200), accountCookie(t, w)
}

func TestAccountRegistrationClaimsDiaryAtomically(t *testing.T) {
	s, dir := setupAccounts(t)
	g := guest(t, s)
	before := saveProfile(t, s, g, 0)
	oldRequest := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
	oldRequest.AddCookie(g)
	oldID, _, err := s.principal(oldRequest, false)
	if err != nil {
		t.Fatal(err)
	}
	stale := request(t, s, "POST", "/api/auth/register", map[string]any{"login": "owner", "password": accountPassword, "revision": 0, "keepGuestData": true}, g)
	requireState(t, stale, 409)
	current, cookie, code := registerAccount(t, s, "Owner", g, before.Revision)
	if current.Profile == nil || current.Profile.Name != before.Profile.Name || current.Revision != before.Revision || current.SessionBinding == before.SessionBinding {
		t.Fatal("claim lost the diary or retained guest binding")
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Partitioned || !strings.HasPrefix(cookie.Value, "s2.") {
		t.Fatal("account cookie protection")
	}
	requireState(t, request(t, s, "GET", "/api/state", nil, g), 401)
	// Even an already-admitted guest request cannot write after the claim.
	_, err = s.repo.Update(context.Background(), oldID, func(r rhythm.Record) (rhythm.Record, error) { r.Revision++; return r, nil })
	if !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("old owner can update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "u_"+oldID[2:]+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("claim copied private diary into a second file")
	}
	value := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if value.Revision != before.Revision {
		t.Fatal("old request changed claimed diary")
	}
	ledger, err := os.ReadFile(filepath.Join(dir, ".accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{accountPassword, code, strings.TrimPrefix(cookie.Value, "s2.")} {
		if bytes.Contains(ledger, []byte(secret)) {
			t.Fatal("plaintext credential in storage")
		}
	}
	stat, err := os.Stat(filepath.Join(dir, ".accounts.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("ledger permissions")
	}
	exported := request(t, s, "GET", "/api/export", nil, cookie)
	requireState(t, exported, 200)
	for _, forbidden := range []string{"recoveryCode", "recoveryDigest", "password", "salt", "digest"} {
		if bytes.Contains(exported.Body.Bytes(), []byte(`"`+forbidden+`"`)) {
			t.Fatalf("export leaked %s", forbidden)
		}
	}
	other := guest(t, s)
	var backup any
	if err := json.Unmarshal(exported.Body.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	restored := requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": backup}, other), 200)
	if restored.Identity != "guest" || restored.Profile == nil {
		t.Fatal("backup transferred account identity or lost profile")
	}
}

func TestAccountsLoginPersistenceIsolationAndRevocation(t *testing.T) {
	s, dir := setupAccounts(t)
	g := guest(t, s)
	saveProfile(t, s, g, 0)
	_, first, _ := registerAccount(t, s, "owner", g, 1)
	unknown := request(t, s, "POST", "/api/auth/login", map[string]any{"login": "unknown", "password": "incorrect password phrase"}, nil)
	wrong := request(t, s, "POST", "/api/auth/login", map[string]any{"login": "owner", "password": "incorrect password phrase"}, nil)
	if unknown.Code != 401 || wrong.Code != 401 || unknown.Body.String() != wrong.Body.String() {
		t.Fatal("login error distinguishes account existence")
	}
	secondState, second := loginAccount(t, s, " OWNER ", accountPassword)
	if first.Value == second.Value || secondState.Profile == nil {
		t.Fatal("login reused token or lost data")
	}
	if c := request(t, s, "GET", "/api/state", nil, second).Result().Cookies(); len(c) != 0 {
		t.Fatal("state silently renewed managed session")
	}
	info := request(t, s, "GET", "/api/account", nil, second)
	if info.Code != 200 {
		t.Fatal(info.Code)
	}
	var accountInfo accounts.Info
	if err := json.Unmarshal(info.Body.Bytes(), &accountInfo); err != nil {
		t.Fatal(err)
	}
	if accountInfo.Login != "owner" || len(accountInfo.Sessions) != 2 || !accountInfo.Sessions[0].Current {
		t.Fatal("session list inaccurate")
	}
	requireState(t, request(t, s, "POST", "/api/auth/logout-others", map[string]any{}, second), 200)
	requireState(t, request(t, s, "GET", "/api/state", nil, first), 401)
	requireState(t, request(t, s, "GET", "/api/state", nil, second), 200)
	store, err := filestore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := accounts.New(store)
	if err != nil {
		t.Fatal(err)
	}
	s.repo, s.accounts = store, restarted
	if st := requireState(t, request(t, s, "GET", "/api/state", nil, second), 200); st.Profile == nil {
		t.Fatal("restart lost account data")
	}
	otherGuest := guest(t, s)
	_, other, _ := registerAccount(t, s, "another", otherGuest, 0)
	if st := requireState(t, request(t, s, "GET", "/api/state", nil, other), 200); st.Profile != nil {
		t.Fatal("other account read owner diary")
	}
	w := request(t, s, "POST", "/api/auth/logout", map[string]any{}, second)
	requireState(t, w, 200)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge != -1 {
			t.Fatal("logout did not clear cookies")
		}
	}
	requireState(t, request(t, s, "GET", "/api/state", nil, second), 401)
	st, _ := loginAccount(t, s, "owner", accountPassword)
	if st.Profile == nil {
		t.Fatal("logout deleted data")
	}
}

func TestAccountRecoveryPasswordRotationAndSingleUse(t *testing.T) {
	s, _ := setupAccounts(t)
	_, first, code := registerAccount(t, s, "owner", guest(t, s), 0)
	_, second := loginAccount(t, s, "owner", accountPassword)
	reset := map[string]any{"login": "owner", "recoveryCode": code, "newPassword": changedPassword}
	w := request(t, s, "POST", "/api/auth/recover", reset, nil)
	requireState(t, w, 200)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("recovery silently logged in")
	}
	var replacement struct {
		RecoveryCode string `json:"recoveryCode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.RecoveryCode == code || len(replacement.RecoveryCode) != 39 {
		t.Fatal("recovery code not rotated")
	}
	for _, cookie := range []*http.Cookie{first, second} {
		requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 401)
	}
	requireState(t, request(t, s, "POST", "/api/auth/recover", reset, nil), 401)
	requireState(t, request(t, s, "POST", "/api/auth/login", map[string]any{"login": "owner", "password": accountPassword}, nil), 401)
	_, current := loginAccount(t, s, "owner", changedPassword)
	w = request(t, s, "POST", "/api/auth/password", map[string]any{"password": "incorrect current password", "newPassword": accountPassword}, current)
	requireState(t, w, 401)
	w = request(t, s, "POST", "/api/auth/password", map[string]any{"password": changedPassword, "newPassword": accountPassword}, current)
	requireState(t, w, 200)
	rotated := accountCookie(t, w)
	requireState(t, request(t, s, "GET", "/api/state", nil, current), 401)
	requireState(t, request(t, s, "GET", "/api/state", nil, rotated), 200)
	reset["recoveryCode"] = replacement.RecoveryCode
	requireState(t, request(t, s, "POST", "/api/auth/recover", reset, nil), 401)
	_, another := loginAccount(t, s, "owner", accountPassword)
	w = request(t, s, "POST", "/api/auth/recovery-code", map[string]any{"password": accountPassword}, another)
	requireState(t, w, 200)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("code rotation unexpectedly changes session")
	}
}

func TestAccountConcurrentClaimHasOneWinner(t *testing.T) {
	s, _ := setupAccounts(t)
	g := guest(t, s)
	r := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
	r.AddCookie(g)
	principal, _, err := s.principal(r, false)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.accounts.Register(context.Background(), principal, 0, name, accountPassword, s.now())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, accounts.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
}

func TestAccountOriginBindingAndStorageFailure(t *testing.T) {
	s, dir := setupAccounts(t)
	g := guest(t, s)
	r := httptest.NewRequest("POST", testOrigin+"/api/auth/login", strings.NewReader(`{"login":"owner","password":"four quiet mornings together"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://untrusted.invalid")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	requireState(t, w, 403)
	requireState(t, request(t, s, "POST", "/api/auth/register", map[string]any{"login": "owner", "password": accountPassword, "revision": 0, "keepGuestData": false}, g), 400)
	_, cookie, _ := registerAccount(t, s, "owner", g, 0)
	r = httptest.NewRequest("POST", testOrigin+"/api/auth/logout", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", testOrigin)
	r.Header.Set(sessionBindingHeader, "another-tab")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	requireState(t, w, 409)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("stale tab cleared current account")
	}
	if err := os.WriteFile(filepath.Join(dir, ".accounts.json"), []byte(`{"broken":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/state", "/api/export", "/api/health"} {
		w = request(t, s, "GET", path, nil, cookie)
		requireState(t, w, 503)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("storage failure issued cookie")
		}
	}
	w = request(t, s, "POST", "/api/auth/login", map[string]any{"login": "owner", "password": accountPassword}, nil)
	requireState(t, w, 503)
	w = request(t, s, "POST", "/api/auth/logout", map[string]any{}, cookie)
	requireState(t, w, 503)
}

func TestConcurrentRecoveryConsumesCodeOnce(t *testing.T) {
	s, _ := setupAccounts(t)
	_, _, code := registerAccount(t, s, "owner", guest(t, s), 0)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.accounts.Recover(context.Background(), "owner", code, changedPassword, s.now())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners, rejected := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, accounts.ErrUnauthorized) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || rejected != 1 {
		t.Fatalf("winners=%d rejected=%d", winners, rejected)
	}
}

func TestGuestLogoutAndManagedTelegramSessions(t *testing.T) {
	s, _ := setupAccounts(t)
	g := guest(t, s)
	requireState(t, request(t, s, "POST", "/api/auth/logout", map[string]any{}, g), 200)
	requireState(t, request(t, s, "GET", "/api/state", nil, g), 401)
	tg := request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": signedTelegram(t, 123, testNow(), nil)}, nil)
	if st := requireState(t, tg, 200); st.Identity != "telegram" {
		t.Fatal("Telegram identity changed")
	}
	cookie := accountCookie(t, tg)
	if !cookie.Partitioned || cookie.SameSite != http.SameSiteNoneMode {
		t.Fatal("managed Telegram cookie cannot embed")
	}
	requireState(t, request(t, s, "POST", "/api/auth/logout", map[string]any{}, cookie), 200)
	requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 401)
	tg = request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": signedTelegram(t, 123, testNow(), nil)}, nil)
	requireState(t, tg, 200)
	s.clock = func() time.Time { return testNow().Add(31 * 24 * time.Hour) }
	requireState(t, request(t, s, "GET", "/api/state", nil, accountCookie(t, tg)), 401)
}

func TestManagedCookieSignatureAndSecretRotation(t *testing.T) {
	s, _ := setupAccounts(t)
	_, cookie, _ := registerAccount(t, s, "owner", guest(t, s), 0)
	for _, value := range []string{cookie.Value + "tampered", strings.Join(strings.Split(cookie.Value, ".")[:2], ".")} {
		changed := *cookie
		changed.Value = value
		requireState(t, request(t, s, "GET", "/api/state", nil, &changed), 401)
	}
	s.secret = []byte(strings.Repeat("r", 32))
	requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 401)
	// Account ownership remains stable; logging in again issues a newly signed cookie.
	_, renewed := loginAccount(t, s, "owner", accountPassword)
	requireState(t, request(t, s, "GET", "/api/state", nil, renewed), 200)
}

func TestCanceledAccountLookupDoesNotConfirmLogout(t *testing.T) {
	s, _ := setupAccounts(t)
	_, cookie, _ := registerAccount(t, s, "owner", guest(t, s), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, path := range []string{"/api/state", "/api/auth/logout"} {
		method := http.MethodGet
		if path == "/api/auth/logout" {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, path, strings.NewReader("{}")).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		requireState(t, w, 503)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("failed lookup must not clear or replace cookies")
		}
	}
	// The failed attempt was not presented as a completed session revocation.
	requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
}

func TestAuthLimiterBoundsConcurrencyWindowsAndHTTP(t *testing.T) {
	var limiter authLimiter
	now := testNow()
	one, ok := limiter.acquire("peer", "owner", now)
	if !ok {
		t.Fatal("first attempt rejected")
	}
	two, ok := limiter.acquire("peer", "another", now)
	if !ok {
		t.Fatal("second attempt rejected")
	}
	if _, ok := limiter.acquire("peer", "third", now); ok {
		t.Fatal("unbounded KDF concurrency")
	}
	one()
	one()
	two()
	if limiter.inFlight != 0 {
		t.Fatal("release not idempotent")
	}
	for range 9 {
		done, ok := limiter.acquire("peer", "owner", now)
		if !ok {
			t.Fatal("early login quota")
		}
		done()
	}
	if _, ok := limiter.acquire("different-peer", "owner", now); ok {
		t.Fatal("login quota bypass")
	}
	done, ok := limiter.acquire("peer", "owner", now.Add(10*time.Minute))
	if !ok {
		t.Fatal("window did not expire")
	}
	done()
	s, _ := setupAccounts(t)
	s.authQuota.global = authWindow{start: s.now(), count: 60}
	w := request(t, s, "POST", "/api/auth/login", map[string]any{"login": "owner", "password": accountPassword}, nil)
	requireState(t, w, 429)
	if w.Header().Get("Retry-After") == "" || len(w.Result().Cookies()) != 0 {
		t.Fatal("rate-limit contract")
	}
}
