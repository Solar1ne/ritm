// Package httpapi adapts HTTP requests to the rhythm aggregate. The repository
// contract is owned here, by its consumer; domain code has no storage dependency.
package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"rhythm/internal/accounts"
	"rhythm/internal/rhythm"
)

type Repository interface {
	Read(context.Context, string) (rhythm.Record, error)
	Update(context.Context, string, func(rhythm.Record) (rhythm.Record, error)) (rhythm.Record, error)
	Check(context.Context) error
}

type Config struct {
	Repository    Repository
	Storage       string
	Secret        []byte
	BotToken      string
	PublicURL     string
	Location      *time.Location
	Static        fs.FS
	Logger        *slog.Logger
	Now           func() time.Time
	AI            AIGenerator
	AIProvider    string
	AIModel       string
	AITimeout     time.Duration
	AIHourlyLimit int
	Accounts      *accounts.Service
	// TrustedProxyCIDRs identifies peers allowed to replace X-Real-IP. The
	// ingress must overwrite this header, and the app must not be exposed directly.
	TrustedProxyCIDRs string
}

type Server struct {
	repo           Repository
	storage        string
	secret         []byte
	botToken       string
	origin         string
	secure         bool
	location       *time.Location
	static         http.Handler
	assets         fs.FS
	logger         *slog.Logger
	clock          func() time.Time
	ai             AIGenerator
	aiProvider     string
	aiModel        string
	aiTimeout      time.Duration
	aiQuota        aiLimiter
	accounts       *accounts.Service
	authQuota      authLimiter
	trustedProxies []netip.Prefix
}

func New(c Config) (*Server, error) {
	if c.Repository == nil || len(c.Secret) < 32 {
		return nil, errors.New("repository and 32-byte signing secret are required")
	}
	if c.Location == nil {
		return nil, errors.New("location is required")
	}
	if c.AIHourlyLimit < 0 || c.AIHourlyLimit > 10000 {
		return nil, errors.New("AI hourly limit must be between 1 and 10000, or zero for default")
	}
	if c.AIProvider == "" {
		c.AIProvider = "OpenAI"
	}
	if c.AIProvider != "OpenAI" && c.AIProvider != "DeepSeek" {
		return nil, errors.New("invalid AI provider")
	}
	if c.AITimeout == 0 {
		c.AITimeout = 3 * time.Second
	}
	if c.AITimeout < time.Second || c.AITimeout > 30*time.Second {
		return nil, errors.New("AI timeout must be between 1 and 30 seconds")
	}
	s := &Server{repo: c.Repository, secret: append([]byte(nil), c.Secret...), botToken: c.BotToken, location: c.Location, logger: c.Logger, clock: c.Now, ai: c.AI, aiModel: c.AIModel, aiQuota: aiLimiter{hourly: c.AIHourlyLimit}, accounts: c.Accounts}
	var err error
	s.trustedProxies, err = parseTrustedProxies(c.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	s.aiProvider, s.aiTimeout = c.AIProvider, c.AITimeout
	s.storage = c.Storage
	if s.storage == "" {
		s.storage = "file"
	}
	if s.storage != "file" && s.storage != "postgresql" {
		return nil, errors.New("invalid storage kind")
	}
	if s.logger == nil {
		s.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if c.Static != nil {
		s.static = http.FileServerFS(c.Static)
		s.assets = c.Static
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("PUBLIC_URL must be an http(s) origin without path or credentials")
		}
		if u.Scheme == "http" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
			return nil, errors.New("public deployments require HTTPS")
		}
		s.origin = u.Scheme + "://" + u.Host
		s.secure = u.Scheme == "https"
	}
	return s, nil
}

func (s *Server) now() time.Time { return s.clock().In(s.location) }

type state struct {
	Limits   collectionLimits `json:"limits"`
	Revision uint64           `json:"revision"`
	Profile  *rhythm.Profile  `json:"profile"`
	Entries  []rhythm.Entry   `json:"entries"`
	rhythm.Projection
	Identity            string                     `json:"identity"`
	AccountsEnabled     bool                       `json:"accountsEnabled"`
	SessionBinding      string                     `json:"sessionBinding"`
	ExportVersion       int                        `json:"exportVersion"`
	Today               string                     `json:"today"`
	TimeZone            string                     `json:"timeZone"`
	CurrentWeekStart    string                     `json:"currentWeekStart"`
	HistoryEditableFrom string                     `json:"historyEditableFrom"`
	Weeks               []rhythm.WeekSnapshot      `json:"weeks"`
	AcceptedNextPlan    *rhythm.Plan               `json:"acceptedNextPlan"`
	NextPlanToken       string                     `json:"nextPlanToken"`
	Workouts            []rhythm.Workout           `json:"workouts"`
	Reminder            rhythm.ReminderSettings    `json:"reminder"`
	AI                  aiStatus                   `json:"ai"`
	SessionAdjustments  []rhythm.SessionAdjustment `json:"sessionAdjustments,omitempty"`
	HRVEntries          []rhythm.HRVEntry          `json:"hrvEntries"`
	BodyEntries         []rhythm.BodyEntry         `json:"bodyEntries"`
	MealSelections      []rhythm.MealSelection     `json:"mealSelections"`
}

func (s *Server) state(r rhythm.Record, principal string) state {
	identity := "guest"
	if strings.HasPrefix(principal, "t_") {
		identity = "telegram"
	}
	if strings.HasPrefix(principal, "u_") {
		identity = "account"
	}
	now := s.now()
	projection := rhythm.Project(r, now)
	if r.Workouts == nil {
		r.Workouts = []rhythm.Workout{}
	}
	if r.Weeks == nil {
		r.Weeks = []rhythm.WeekSnapshot{}
	}
	if r.HRVEntries == nil {
		r.HRVEntries = []rhythm.HRVEntry{}
	}
	if r.BodyEntries == nil {
		r.BodyEntries = []rhythm.BodyEntry{}
	}
	if r.MealSelections == nil {
		r.MealSelections = []rhythm.MealSelection{}
	}
	return state{Limits: currentCollectionLimits(), Revision: r.Revision, Profile: r.Profile, Entries: r.Entries, Projection: projection, Identity: identity, AccountsEnabled: s.accounts != nil, SessionBinding: s.sessionBinding(principal), ExportVersion: rhythm.ExportVersion, Today: now.Format(time.DateOnly), TimeZone: s.location.String(), CurrentWeekStart: rhythm.WeekStart(now).Format(time.DateOnly), HistoryEditableFrom: rhythm.EditableFrom(now), Weeks: r.Weeks, AcceptedNextPlan: rhythm.AcceptedNextPlan(r, now), NextPlanToken: rhythm.ProposalToken(projection), Workouts: r.Workouts, Reminder: r.Reminder, AI: s.aiStatus(), SessionAdjustments: r.SessionAdjustments, HRVEntries: r.HRVEntries, BodyEntries: r.BodyEntries, MealSelections: r.MealSelections}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://telegram.org; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; img-src 'self' data:; font-src 'self' https://fonts.gstatic.com; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self' https://web.telegram.org https://*.telegram.org")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if s.secure {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.fail(w, http.StatusMethodNotAllowed, "method", "Метод не поддерживается.")
			return
		}
		if s.static == nil {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" {
			info, err := fs.Stat(s.assets, strings.TrimPrefix(path.Clean(r.URL.Path), "/"))
			if err != nil && path.Ext(r.URL.Path) == "" {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			} else if err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		s.static.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.sameOrigin(r) {
		s.fail(w, http.StatusForbidden, "origin", "Запрос с другого сайта отклонён.")
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/health":
		s.health(w, r)
	case "GET /api/state":
		s.getState(w, r, false)
	case "GET /api/export":
		s.getState(w, r, true)
	case "POST /api/profile":
		s.profile(w, r)
	case "POST /api/entries":
		s.entry(w, r)
	case "DELETE /api/entries":
		s.deleteEntry(w, r)
	case "GET /api/weeks":
		s.week(w, r)
	case "POST /api/plan/accept":
		s.acceptPlan(w, r, false)
	case "DELETE /api/plan/accept":
		s.acceptPlan(w, r, true)
	case "POST /api/restore":
		s.restore(w, r)
	case "DELETE /api/data":
		s.reset(w, r)
	case "POST /api/preview":
		s.preview(w, r)
	case "POST /api/auth/telegram":
		s.telegram(w, r)
	default:
		if s.serveAccounts(w, r) {
			return
		}
		if s.serveExtras(w, r) {
			return
		}
		s.fail(w, http.StatusNotFound, "not_found", "Такого адреса API нет.")
	}
}

func (s *Server) sameOrigin(r *http.Request) bool {
	fetch := r.Header.Get("Sec-Fetch-Site")
	if fetch != "" && fetch != "same-origin" && fetch != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	} // Non-browser clients still require JSON.
	expected := s.origin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	return origin == expected
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return s.decodeLimit(w, r, dst, 64*1024)
}

func (s *Server) decodeLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		s.fail(w, http.StatusUnsupportedMediaType, "content_type", "Отправьте данные в формате application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		s.fail(w, http.StatusBadRequest, "json", "Некорректный JSON, неизвестное поле или слишком большой запрос.")
		return false
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		s.fail(w, http.StatusBadRequest, "json", "В запросе должен быть один JSON-объект.")
		return false
	}
	return true
}

func (s *Server) authenticated(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, _, err := s.principal(r, false)
	if err != nil {
		if errors.Is(err, accounts.ErrUnavailable) {
			s.failure(w, r, err)
			return "", false
		}
		s.fail(w, http.StatusUnauthorized, "session", "Сессия недействительна. Откройте приложение заново.")
		return "", false
	}
	if !s.requireBinding(w, r, id) {
		return "", false
	}
	return id, true
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request, export bool) {
	id, fresh, err := s.principal(r, !export)
	if err != nil {
		if errors.Is(err, accounts.ErrUnavailable) {
			s.failure(w, r, err)
			return
		}
		s.fail(w, http.StatusUnauthorized, "session", "Сессия недействительна. Откройте приложение заново.")
		return
	}
	if (export || r.Header.Get(sessionBindingHeader) != "") && !s.requireBinding(w, r, id) {
		return
	}
	// Opening a page must not create a persistent diary for every crawler or
	// cookie-less request. The first mutation creates it transactionally.
	record, err := s.repo.Read(r.Context(), id)
	if err == nil && record.Profile != nil {
		record, err = s.calendarRecord(r.Context(), id)
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if fresh || !export && managedToken(r) == "" {
		s.signCookie(w, id)
	}
	if export {
		w.Header().Set("Content-Disposition", `attachment; filename="rhythm-data.json"`)
	}
	s.json(w, http.StatusOK, s.state(record, id))
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision *uint64         `json:"revision"`
		Profile  *rhythm.Profile `json:"profile"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.Profile == nil {
		s.fail(w, 400, "validation", "Нужны revision и profile.")
		return
	}
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	record, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.SetProfileAt(current, *input.Revision, *input.Profile, s.now())
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) entry(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision *uint64       `json:"revision"`
		Entry    *rhythm.Entry `json:"entry"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil || input.Entry == nil {
		s.fail(w, 400, "validation", "Нужны revision и entry.")
		return
	}
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	now := s.now()
	record, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
		return rhythm.AddEntry(current, *input.Revision, *input.Entry, now)
	})
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision *uint64 `json:"revision"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Revision == nil {
		s.fail(w, 400, "validation", "Нужно передать revision.")
		return
	}
	id, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	record, err := s.repo.Update(r.Context(), id, func(current rhythm.Record) (rhythm.Record, error) { return rhythm.Reset(current, *input.Revision) })
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Profile *rhythm.Profile `json:"profile"`
		Entries []rhythm.Entry  `json:"entries"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Profile == nil {
		s.fail(w, 400, "validation", "Нужно передать profile.")
		return
	}
	projection, err := rhythm.Preview(*input.Profile, input.Entries, s.now())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	s.json(w, 200, projection)
}

func (s *Server) telegram(w http.ResponseWriter, r *http.Request) {
	if s.botToken == "" {
		s.fail(w, 503, "telegram_unavailable", "Telegram-вход ещё не настроен. Веб-версия доступна в гостевом режиме.")
		return
	}
	var input struct {
		InitData string `json:"initData"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	userID, err := validateTelegram(input.InitData, s.botToken, s.now())
	if err != nil {
		s.fail(w, 401, "telegram_auth", "Не удалось подтвердить вход через Telegram. Откройте Mini App заново.")
		return
	}
	id := "t_" + hex.EncodeToString(mac(s.secret, "telegram:"+strconv.FormatInt(userID, 10)))
	record, err := s.calendarRecord(r.Context(), id)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if s.accounts != nil {
		grant, err := s.accounts.NewTelegramSession(r.Context(), id, s.now())
		if err != nil {
			s.accountFailure(w, r, err)
			return
		}
		s.signManagedCookie(w, grant)
	} else {
		s.signCookie(w, id)
	}
	s.json(w, 200, s.state(record, id))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.repo.Check(r.Context()); err != nil {
		s.failure(w, r, err)
		return
	}
	if s.accounts != nil {
		if err := s.accounts.Check(r.Context()); err != nil {
			s.failure(w, r, err)
			return
		}
	}
	s.json(w, 200, struct {
		Status             string `json:"status"`
		Storage            string `json:"storage"`
		TelegramConfigured bool   `json:"telegramConfigured"`
	}{"ok", s.storage, s.botToken != ""})
}

func (s *Server) failure(w http.ResponseWriter, r *http.Request, err error) {
	var validation *rhythm.ValidationError
	switch {
	case errors.Is(err, accounts.ErrUnauthorized):
		s.fail(w, 401, "session", "Сессия завершена. Войдите в аккаунт заново.")
	case errors.As(err, &validation):
		s.fail(w, 400, "validation", validation.Message)
	case errors.Is(err, rhythm.ErrConflict):
		s.fail(w, 409, "revision_conflict", err.Error())
	case errors.Is(err, rhythm.ErrEntryConflict):
		s.fail(w, 409, "entry_conflict", err.Error())
	case errors.Is(err, rhythm.ErrPlanConflict):
		s.fail(w, 409, "plan_conflict", err.Error())
	case errors.Is(err, rhythm.ErrNotFound):
		s.fail(w, 404, "not_found", err.Error())
	case errors.Is(err, rhythm.ErrCapacity):
		s.fail(w, 413, "capacity", err.Error())
	default:
		// Avoid logging payloads, names, dates, identity, cookies or auth data.
		s.logger.Error("storage operation failed", "route", r.URL.Path, "method", r.Method, "kind", "storage_unavailable")
		s.fail(w, 503, "storage_unavailable", "Хранилище временно недоступно. Действие не подтверждено; повторите запрос.")
	}
}

func (s *Server) fail(w http.ResponseWriter, status int, code, message string) {
	s.json(w, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message}})
}
func (s *Server) json(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.logger.Warn("response interrupted", "kind", "response_write")
	}
}
