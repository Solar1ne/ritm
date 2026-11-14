package postgresstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rhythm/internal/httpapi"
)

func TestHTTPBootstrapDoesNotPersistEmptyDiaries(t *testing.T) {
	s, _ := testDatabase(t, true)
	h, err := httpapi.New(httpapi.Config{Repository: s, Accounts: service(t, s), Storage: "postgresql", Secret: []byte(strings.Repeat("x", 32)), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	var binding string
	for i := 0; i < 4; i++ {
		r := httptest.NewRequest("GET", "http://localhost/api/state", nil)
		if i == 3 {
			r.AddCookie(cookie) // Returning guests must also remain transient.
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("bootstrap %d: status %d", i, w.Code)
		}
		cookie = w.Result().Cookies()[0]
		var state struct {
			SessionBinding string `json:"sessionBinding"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		binding = state.SessionBinding
	}
	assertRows := func(want int) {
		t.Helper()
		var diaries, identities int
		if err := s.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM rhythm_diaries), (SELECT count(*) FROM rhythm_identities)").Scan(&diaries, &identities); err != nil {
			t.Fatal(err)
		}
		if diaries != want || identities != want {
			t.Fatalf("rows: diaries=%d identities=%d, want %d", diaries, identities, want)
		}
	}
	assertRows(0)
	r := httptest.NewRequest("POST", "http://localhost/api/profile", strings.NewReader(`{"revision":0,"profile":{"name":"Test","goal":"tone","experience":"beginner","days":[0,2,4],"minutes":45}}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Rhythm-Session", binding)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("first mutation: %d %s", w.Code, w.Body.String())
	}
	assertRows(1)
}
