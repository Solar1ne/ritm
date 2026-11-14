package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"rhythm/internal/accounts"
)

type accountInput struct {
	Login         string  `json:"login"`
	Password      string  `json:"password"`
	NewPassword   string  `json:"newPassword"`
	RecoveryCode  string  `json:"recoveryCode"`
	Revision      *uint64 `json:"revision"`
	KeepGuestData bool    `json:"keepGuestData"`
	ConfirmDelete bool    `json:"confirmDelete"`
}

func (s *Server) serveAccounts(w http.ResponseWriter, r *http.Request) bool {
	key := r.Method + " " + r.URL.Path
	switch key {
	case "POST /api/auth/register", "POST /api/auth/login", "POST /api/auth/recover", "POST /api/auth/logout", "POST /api/auth/password", "POST /api/auth/recovery-code", "POST /api/auth/logout-others", "GET /api/account", "DELETE /api/account":
	default:
		return false
	}
	if s.accounts == nil {
		s.fail(w, 503, "accounts_unavailable", "Вход по логину пока недоступен.")
		return true
	}
	if key == "GET /api/account" {
		if _, ok := s.authenticated(w, r); !ok {
			return true
		}
		info, err := s.accounts.Info(r.Context(), managedToken(r), s.now())
		if err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.json(w, 200, info)
		}
		return true
	}
	var input accountInput
	if !s.decodeLimit(w, r, &input, 4*1024) {
		return true
	}
	if key == "POST /api/auth/logout" {
		id, _, err := s.principal(r, false)
		if errors.Is(err, accounts.ErrUnavailable) {
			s.accountFailure(w, r, err)
			return true
		}
		if err == nil {
			if !s.requireBinding(w, r, id) {
				return true
			}
			if err := s.accounts.Logout(r.Context(), managedToken(r), id, s.now()); err != nil {
				s.accountFailure(w, r, err)
				return true
			}
		}
		s.clearCookies(w)
		s.json(w, 200, map[string]bool{"signedOut": true})
		return true
	}
	if key == "POST /api/auth/logout-others" {
		if _, ok := s.authenticated(w, r); !ok {
			return true
		}
		if err := s.accounts.LogoutOthers(r.Context(), managedToken(r), s.now()); err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.json(w, 200, map[string]bool{"signedOutOthers": true})
		}
		return true
	}
	peer := s.clientIP(r)
	login := strings.ToLower(strings.TrimSpace(input.Login))
	if login == "" || key == "DELETE /api/account" {
		login = s.sessionKey(r)
	}
	release, allowed := s.authQuota.acquire(accounts.TokenHash(peer), accounts.TokenHash(login), s.now())
	if !allowed {
		w.Header().Set("Retry-After", "60")
		s.fail(w, 429, "auth_limit", "Слишком много попыток входа. Подождите и попробуйте позже.")
		return true
	}
	defer release()
	switch key {
	case "DELETE /api/account":
		id, ok := s.authenticated(w, r)
		if !ok {
			return true
		}
		if !strings.HasPrefix(id, "u_") {
			s.fail(w, 400, "account_delete_unsupported", "Удалить можно аккаунт сайта. Для гостевого или Telegram-дневника используйте очистку данных.")
			return true
		}
		if input.Revision == nil || !input.ConfirmDelete {
			s.fail(w, 400, "account_delete_confirmation", "Подтвердите удаление аккаунта и всех данных дневника.")
			return true
		}
		if err := s.accounts.Delete(r.Context(), managedToken(r), input.Password, *input.Revision, s.now()); err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.clearCookies(w)
			s.json(w, 200, map[string]bool{"accountDeleted": true})
		}
	case "POST /api/auth/register":
		id, ok := s.authenticated(w, r)
		if !ok {
			return true
		}
		if input.Revision == nil || !input.KeepGuestData || !strings.HasPrefix(id, "g_") {
			s.fail(w, 400, "account_claim", "Подтвердите сохранение текущего гостевого дневника в новом аккаунте.")
			return true
		}
		grant, err := s.accounts.Register(r.Context(), id, *input.Revision, input.Login, input.Password, s.now())
		if err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.accountGrant(w, r, grant)
		}
	case "POST /api/auth/login":
		grant, err := s.accounts.Login(r.Context(), input.Login, input.Password, s.now())
		if err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.accountGrant(w, r, grant)
		}
	case "POST /api/auth/recover":
		code, err := s.accounts.Recover(r.Context(), input.Login, input.RecoveryCode, input.NewPassword, s.now())
		if err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.json(w, 200, map[string]string{"recoveryCode": code})
		}
	case "POST /api/auth/password":
		if _, ok := s.authenticated(w, r); !ok {
			return true
		}
		grant, err := s.accounts.ChangePassword(r.Context(), managedToken(r), input.Password, input.NewPassword, s.now())
		if err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.accountGrant(w, r, grant)
		}
	case "POST /api/auth/recovery-code":
		if _, ok := s.authenticated(w, r); !ok {
			return true
		}
		code, err := s.accounts.RotateRecovery(r.Context(), managedToken(r), input.Password, s.now())
		if err != nil {
			s.accountFailure(w, r, err)
		} else {
			s.json(w, 200, map[string]string{"recoveryCode": code})
		}
	}
	return true
}

func (s *Server) sessionKey(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return "anonymous"
}

func (s *Server) accountGrant(w http.ResponseWriter, r *http.Request, grant accounts.Grant) {
	record, err := s.calendarRecord(r.Context(), grant.Principal)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.signManagedCookie(w, grant)
	s.json(w, 200, struct {
		state
		RecoveryCode string `json:"recoveryCode,omitempty"`
	}{s.state(record, grant.Principal), grant.RecoveryCode})
}

func (s *Server) accountFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accounts.ErrUnauthorized):
		s.fail(w, 401, "credentials", "Не удалось подтвердить вход. Проверьте логин, пароль или резервный код.")
	case errors.Is(err, accounts.ErrLogin):
		s.fail(w, 400, "account_login", "Логин: от 3 до 32 латинских букв, цифр, точек, дефисов или подчёркиваний.")
	case errors.Is(err, accounts.ErrPassword):
		s.fail(w, 400, "account_password", "Пароль: от 15 до 128 символов. Используйте несколько разных слов; пробелы разрешены.")
	case errors.Is(err, accounts.ErrNameTaken):
		s.fail(w, 409, "account_login_unavailable", "Этот логин недоступен. Выберите другой или войдите в существующий аккаунт.")
	case errors.Is(err, accounts.ErrConflict):
		s.fail(w, 409, "account_conflict", "Гостевой дневник изменился. Обновите страницу и повторите регистрацию; если она уже завершилась, войдите по логину.")
	case errors.Is(err, accounts.ErrCapacity):
		s.fail(w, 503, "accounts_capacity", "Сервис входа временно достиг лимита. Попробуйте позже.")
	case errors.Is(err, accounts.ErrDeletionUnavailable):
		s.fail(w, 503, "accounts_unavailable", "Атомарное удаление аккаунта в этом хранилище недоступно. Аккаунт и дневник сохранены.")
	default:
		s.failure(w, r, err)
	}
}

type authWindow struct {
	start time.Time
	count int
}
type authLimiter struct {
	mu       sync.Mutex
	entries  map[string]authWindow
	global   authWindow
	inFlight int
}

func (l *authLimiter) acquire(peer, login string, now time.Time) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = make(map[string]authWindow)
	}
	for key, window := range l.entries {
		if now.Before(window.start) || now.Sub(window.start) >= 10*time.Minute {
			delete(l.entries, key)
		}
	}
	if now.Before(l.global.start) || now.Sub(l.global.start) >= time.Minute {
		l.global = authWindow{start: now}
	}
	pkey, lkey := "peer:"+peer, "login:"+login
	p, u := l.entries[pkey], l.entries[lkey]
	if l.inFlight >= 2 || l.global.count >= 60 || p.count >= 50 || u.count >= 10 || len(l.entries) >= 8190 {
		return nil, false
	}
	if p.start.IsZero() {
		p.start = now
	}
	if u.start.IsZero() {
		u.start = now
	}
	p.count++
	u.count++
	l.global.count++
	l.inFlight++
	l.entries[pkey], l.entries[lkey] = p, u
	var once sync.Once
	return func() { once.Do(func() { l.mu.Lock(); l.inFlight--; l.mu.Unlock() }) }, true
}
