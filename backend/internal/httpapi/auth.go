package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"rhythm/internal/accounts"
)

const sessionCookie = "rhythm_session"
const sessionLifetime = 30 * 24 * time.Hour
const sessionBindingHeader = "X-Rhythm-Session"

func mac(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value)) // hash.Hash writes always succeed.
	return h.Sum(nil)
}

// The browser retains this binding with its current state. Shared cookies can
// change identity in another tab; a matching revision alone is not sufficient
// authorization to overwrite or export the newly signed-in account's data.
func (s *Server) sessionBinding(principal string) string {
	return hex.EncodeToString(mac(s.secret, "binding:"+principal))
}

func (s *Server) requireBinding(w http.ResponseWriter, r *http.Request, principal string) bool {
	if !hmac.Equal([]byte(r.Header.Get(sessionBindingHeader)), []byte(s.sessionBinding(principal))) {
		s.fail(w, http.StatusConflict, "session_changed", "Аккаунт изменился в другой вкладке. Откройте приложение заново перед продолжением.")
		return false
	}
	return true
}

func guestPrincipal() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate guest identity: %w", err)
	}
	return "g_" + hex.EncodeToString(bytes), nil
}

func validPrincipal(id string) bool {
	if len(id) != 66 || (id[:2] != "g_" && id[:2] != "t_" && id[:2] != "u_") {
		return false
	}
	b, err := hex.DecodeString(id[2:])
	return err == nil && len(b) == 32 && strings.ToLower(id) == id
}

func (s *Server) signCookie(w http.ResponseWriter, principal string) {
	expires := s.now().Add(sessionLifetime)
	value := principal + ":" + strconv.FormatInt(expires.Unix(), 10)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	value = encoded + "." + base64.RawURLEncoding.EncodeToString(mac(s.secret, encoded))
	cookie := &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(sessionLifetime.Seconds())}
	// Telegram Web embeds Mini Apps. Its verified session can use a partitioned
	// cross-site cookie over HTTPS; guest sessions retain SameSite=Lax. Unsafe
	// requests still require same-origin JSON and pass the Fetch Metadata check.
	if s.secure && strings.HasPrefix(principal, "t_") {
		// An unpartitioned guest cookie and a partitioned Telegram cookie with
		// the same name can otherwise coexist and yield ambiguous identities.
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
		cookie.SameSite = http.SameSiteNoneMode
		cookie.Partitioned = true
	}
	http.SetCookie(w, cookie)
}

func (s *Server) principal(r *http.Request, allowNew bool) (string, bool, error) {
	if len(r.CookiesNamed(sessionCookie)) > 1 {
		return "", false, errors.New("ambiguous session cookies")
	}
	c, err := r.Cookie(sessionCookie)
	if errors.Is(err, http.ErrNoCookie) && allowNew {
		id, err := guestPrincipal()
		return id, true, err
	}
	if err != nil {
		return "", false, errors.New("missing session")
	}
	if strings.HasPrefix(c.Value, "s2.") {
		if s.accounts == nil {
			return "", false, accounts.ErrUnauthorized
		}
		bearer, signature, ok := strings.Cut(strings.TrimPrefix(c.Value, "s2."), ".")
		sig, err := base64.RawURLEncoding.DecodeString(signature)
		if !ok || err != nil || !hmac.Equal(sig, mac(s.secret, "session2:"+bearer)) {
			return "", false, accounts.ErrUnauthorized
		}
		id, err := s.accounts.Resolve(r.Context(), bearer, s.now())
		return id, false, err
	}
	encoded, signature, ok := strings.Cut(c.Value, ".")
	if !ok {
		return "", false, errors.New("invalid session")
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(sig, mac(s.secret, encoded)) {
		return "", false, errors.New("invalid session signature")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false, errors.New("invalid session encoding")
	}
	id, expiry, ok := strings.Cut(string(decoded), ":")
	expires, err := strconv.ParseInt(expiry, 10, 64)
	if !ok || err != nil || !validPrincipal(id) || expires <= s.now().Unix() {
		return "", false, errors.New("expired or invalid session")
	}
	// A signed legacy cookie cannot impersonate an account, including a guest
	// diary that has just been claimed by its owner during registration.
	if strings.HasPrefix(id, "u_") {
		return "", false, accounts.ErrUnauthorized
	}
	if s.accounts != nil {
		if err := s.accounts.AllowLegacy(r.Context(), id, s.now()); err != nil {
			return "", false, err
		}
	}
	return id, false, nil
}

func managedToken(r *http.Request) string {
	if len(r.CookiesNamed(sessionCookie)) != 1 {
		return ""
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || !strings.HasPrefix(cookie.Value, "s2.") {
		return ""
	}
	bearer, _, ok := strings.Cut(strings.TrimPrefix(cookie.Value, "s2."), ".")
	if !ok {
		return ""
	}
	return bearer
}

func (s *Server) clearCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	if s.secure {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode, Partitioned: true, MaxAge: -1, Expires: time.Unix(1, 0)})
	}
}

func (s *Server) signManagedCookie(w http.ResponseWriter, grant accounts.Grant) {
	// Clear both partitions before rotating between website and Telegram identities.
	s.clearCookies(w)
	value := "s2." + grant.Token + "." + base64.RawURLEncoding.EncodeToString(mac(s.secret, "session2:"+grant.Token))
	cookie := &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode, Expires: grant.ExpiresAt, MaxAge: int(grant.ExpiresAt.Sub(s.now()).Seconds())}
	if s.secure && strings.HasPrefix(grant.Principal, "t_") {
		cookie.SameSite = http.SameSiteNoneMode
		cookie.Partitioned = true
	}
	http.SetCookie(w, cookie)
}

// validateTelegram implements the HMAC recipe in Telegram's Mini Apps docs:
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
// The raw user ID is used only to derive an opaque, stable principal.
func validateTelegram(initData, token string, now time.Time) (int64, error) {
	if len(initData) == 0 || len(initData) > 16*1024 {
		return 0, errors.New("invalid initData size")
	}
	values, err := url.ParseQuery(initData)
	if err != nil {
		return 0, errors.New("invalid initData encoding")
	}
	keys := make([]string, 0, len(values))
	for key, vals := range values {
		if len(vals) != 1 || key == "" || strings.ContainsAny(key, "\r\n=") {
			return 0, errors.New("duplicate or invalid initData field")
		}
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	received, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(received) != sha256.Size {
		return 0, errors.New("invalid initData hash")
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secret := mac([]byte("WebAppData"), token)
	if !hmac.Equal(received, mac(secret, strings.Join(lines, "\n"))) {
		return 0, errors.New("invalid initData signature")
	}
	authDate, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || authDate > now.Unix()+30 || authDate < now.Add(-time.Hour).Unix() {
		return 0, errors.New("expired initData")
	}
	var user struct {
		ID    int64 `json:"id"`
		IsBot bool  `json:"is_bot"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID <= 0 || user.ID > (1<<52)-1 || user.IsBot {
		return 0, errors.New("invalid Telegram user")
	}
	return user.ID, nil
}
