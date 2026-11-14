package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func capacityAuthServer(t *testing.T, kind string) (*Server, func() *httptest.ResponseRecorder) {
	t.Helper()
	var s *Server
	if kind == "telegram_signed" {
		s, _ = setup(t, testToken)
	} else {
		s, _ = setupAccounts(t)
	}
	if kind == "account" {
		registerAccount(t, s, "capacity.reader", guest(t, s), 0)
		return s, func() *httptest.ResponseRecorder {
			return request(t, s, "POST", "/api/auth/login", map[string]any{"login": "capacity.reader", "password": accountPassword}, nil)
		}
	}
	return s, func() *httptest.ResponseRecorder {
		return request(t, s, "POST", "/api/auth/telegram", map[string]any{"initData": signedTelegram(t, 123, s.now(), nil)}, nil)
	}
}

func seedCapacityAuthHistory(t *testing.T, s *Server, cookie *http.Cookie) (string, rhythm.Record) {
	t.Helper()
	req := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
	req.AddCookie(cookie)
	id, _, err := s.principal(req, false)
	if err != nil {
		t.Fatal(err)
	}
	first := testNow().AddDate(0, 0, -7*(rhythm.MaxWeeks-1))
	saved, err := rhythm.SetProfileAt(rhythm.Blank(), 0, testProfile(), first)
	if err != nil {
		t.Fatal(err)
	}
	saved, err = rhythm.EnsureWeeks(saved, testNow())
	if err != nil {
		t.Fatal(err)
	}
	saved, err = rhythm.AddEntry(saved, saved.Revision, rhythm.Entry{ID: "saved-fact", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}, testNow())
	if err != nil {
		t.Fatal(err)
	}
	saved, err = s.repo.Update(context.Background(), id, func(rhythm.Record) (rhythm.Record, error) { return saved, nil })
	if err != nil {
		t.Fatal(err)
	}
	return id, saved
}

func TestAuthenticationRemainsAvailableAtWeekCapacity(t *testing.T) {
	for _, kind := range []string{"account", "telegram_managed", "telegram_signed"} {
		t.Run(kind, func(t *testing.T) {
			s, authenticate := capacityAuthServer(t, kind)
			initial := authenticate()
			requireState(t, initial, 200)
			oldCookie := accountCookie(t, initial)
			id, saved := seedCapacityAuthHistory(t, s, oldCookie)
			s.clock = func() time.Time { return testNow().AddDate(0, 0, 60) }
			requireState(t, request(t, s, "GET", "/api/export", nil, oldCookie), 401)
			w := authenticate()
			got := requireState(t, w, 200)
			cookie := accountCookie(t, w)
			if got.Revision != saved.Revision || !reflect.DeepEqual(got.Weeks, saved.Weeks) || !reflect.DeepEqual(got.Entries, saved.Entries) {
				t.Fatal("authentication changed saved history or revision")
			}
			exported := requireState(t, request(t, s, "GET", "/api/export", nil, cookie), 200)
			if exported.Revision != saved.Revision || !reflect.DeepEqual(exported.Weeks, saved.Weeks) || !reflect.DeepEqual(exported.Entries, saved.Entries) {
				t.Fatal("fresh session cannot export the original history")
			}
			requireState(t, request(t, s, "POST", "/api/profile", map[string]any{"revision": saved.Revision, "profile": testProfile()}, cookie), 413)
			after, err := s.repo.Read(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, saved) {
				t.Fatal("authentication, export or rejected mutation changed the persisted aggregate")
			}
		})
	}
}

func TestAuthenticationCapacityFallbackDoesNotIssueCookieOnStorageFailure(t *testing.T) {
	for _, kind := range []string{"account", "telegram_managed", "telegram_signed"} {
		for _, failure := range []string{"update", "fallback_read"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				s, authenticate := capacityAuthServer(t, kind)
				repo := &capacityReadRepository{Repository: s.repo, updateErr: errors.New("storage unavailable")}
				if failure == "fallback_read" {
					repo.updateErr = rhythm.ErrCapacity
					repo.readErr = errors.New("storage unavailable")
					repo.reads = 1
				}
				s.repo = repo
				w := authenticate()
				requireState(t, w, 503)
				if len(w.Result().Cookies()) != 0 {
					t.Fatal("failed authentication response issued a session cookie")
				}
			})
		}
	}
}
