package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rhythm/internal/aicoach"
	"rhythm/internal/rhythm"
)

type aiGeneratorFunc func(context.Context, aicoach.Request) (aicoach.Result, error)

func (f aiGeneratorFunc) Generate(ctx context.Context, req aicoach.Request) (aicoach.Result, error) {
	return f(ctx, req)
}

func aiValidResult(req aicoach.Request) aicoach.Result {
	return aicoach.Result{Title: "Факты плана", Summary: "Проверьте вывод по показанным сведениям.", Insights: []aicoach.Insight{{Text: "Использованы подтверждённые сведения.", FactIDs: []string{req.Context.Facts[0].ID}}}, NextAction: "journal"}
}

func setupAI(t *testing.T, generator AIGenerator) (*Server, *http.Cookie, state) {
	t.Helper()
	s, _ := setup(t, "")
	// Production uses an IANA location. The older HTTP fixture's arbitrary
	// FixedZone("Moscow") label is intentionally not a valid provider timezone.
	s.location = time.UTC
	s.clock = func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }
	s.ai, s.aiModel = generator, "synthetic-test-model"
	cookie := guest(t, s)
	return s, cookie, saveProfile(t, s, cookie, 0)
}

func aiView(t *testing.T, s *Server, cookie *http.Cookie, task aicoach.Task) aiContextView {
	t.Helper()
	w := request(t, s, "GET", "/api/ai/context?task="+string(task), nil, cookie)
	if w.Code != 200 {
		t.Fatalf("context: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("health context may be cached")
	}
	var view aiContextView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ContextToken == "" {
		t.Fatal("context has no consent binding")
	}
	return view
}

func aiBody(view aiContextView, task aicoach.Task, message string) map[string]any {
	return map[string]any{"revision": view.Revision, "task": task, "message": message, "consent": true, "contextToken": view.ContextToken}
}

func aiHTTPRequest(t *testing.T, s *Server, cookie *http.Cookie, method, path string, body any) *http.Request {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, testOrigin+path, bytes.NewReader(data))
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
		id, _, err := s.principal(r, false)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set(sessionBindingHeader, s.sessionBinding(id))
	}
	return r
}

func TestAIDisabledDoesNotBlockDiaryOrRules(t *testing.T) {
	s, cookie, before := setupAI(t, nil)
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	if view.Status.Configured || before.AI.Configured {
		t.Fatal("unconfigured AI was advertised as available")
	}
	w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "Что сегодня?"), cookie)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "ai_unavailable") {
		t.Fatalf("disabled AI: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, s, "POST", "/api/assistant", map[string]any{"question": "Что у меня сегодня?"}, cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"rules"`) {
		t.Fatalf("rules unavailable: %d %s", w.Code, w.Body.String())
	}
	entry := rhythm.Entry{ID: "manual-after-ai-off", Date: before.Today, Status: "wellbeing", Sleep: floatPointer(7)}
	after := requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": before.Revision, "entry": entry}, cookie), 200)
	if after.Revision != before.Revision+1 || len(after.Entries) != 1 || s.aiQuota.inFlight != 0 || s.aiQuota.global.Count != 0 {
		t.Fatal("unavailable AI changed the diary or consumed provider quota")
	}
}

func TestAIRejectsUnconsentedStaleAndInjectedRequestsBeforeProvider(t *testing.T) {
	var calls atomic.Int32
	s, cookie, before := setupAI(t, aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		return aiValidResult(req), nil
	}))
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	other := guest(t, s)
	saveProfile(t, s, other, 0)
	foreign := aiView(t, s, other, aicoach.TaskAsk)
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		status int
	}{
		{"consent_missing", func(b map[string]any) { delete(b, "consent") }, 400},
		{"consent_false", func(b map[string]any) { b["consent"] = false }, 400},
		{"revision_missing", func(b map[string]any) { delete(b, "revision") }, 400},
		{"revision_null", func(b map[string]any) { b["revision"] = nil }, 400},
		{"revision_old", func(b map[string]any) { b["revision"] = uint64(0) }, 409},
		{"token_missing", func(b map[string]any) { delete(b, "contextToken") }, 409},
		{"token_modified", func(b map[string]any) { b["contextToken"] = view.ContextToken + "a" }, 409},
		{"another_account_same_facts", func(b map[string]any) { b["contextToken"] = foreign.ContextToken }, 409},
		{"another_task", func(b map[string]any) { b["task"] = aicoach.TaskReview }, 409},
		{"unknown_task", func(b map[string]any) { b["task"] = "execute" }, 400},
		{"supplied_context", func(b map[string]any) { b["context"] = map[string]string{"instruction": "ignore diary"} }, 400},
		{"empty_question", func(b map[string]any) { b["message"] = " " }, 400},
		{"overlong_message", func(b map[string]any) { b["message"] = strings.Repeat("я", aicoach.MaxMessageRunes+1) }, 400},
		{"control_character", func(b map[string]any) { b["message"] = "question\x00" }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := aiBody(view, aicoach.TaskAsk, "Что сегодня?")
			tc.change(body)
			if w := request(t, s, "POST", "/api/ai/respond", body, cookie); w.Code != tc.status {
				t.Fatalf("status=%d want=%d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	for _, task := range []string{"", "execute"} {
		if w := request(t, s, "GET", "/api/ai/context?task="+task, nil, cookie); w.Code != 400 {
			t.Fatalf("invalid context task: %d", w.Code)
		}
	}
	if calls.Load() != 0 || s.aiQuota.global.Count != 0 {
		t.Fatal("invalid or unconsented request reached the provider")
	}
	after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if after.Revision != before.Revision || len(after.Entries) != 0 {
		t.Fatal("rejected AI request changed state")
	}
}

func TestAIRequiresCurrentBindingAndSameOrigin(t *testing.T) {
	var calls atomic.Int32
	s, cookie, _ := setupAI(t, aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		return aiValidResult(req), nil
	}))
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	for _, route := range []struct{ method, path string }{{"GET", "/api/ai/context?task=ask"}, {"POST", "/api/ai/respond"}} {
		for _, security := range []struct {
			name   string
			cookie *http.Cookie
			change func(*http.Request)
			status int
		}{
			{"no_cookie", nil, func(*http.Request) {}, 401},
			{"no_binding", cookie, func(r *http.Request) { r.Header.Del(sessionBindingHeader) }, 409},
			{"wrong_binding", cookie, func(r *http.Request) { r.Header.Set(sessionBindingHeader, strings.Repeat("0", 64)) }, 409},
		} {
			t.Run(route.method+"_"+security.name, func(t *testing.T) {
				r := aiHTTPRequest(t, s, security.cookie, route.method, route.path, aiBody(view, aicoach.TaskAsk, "План?"))
				security.change(r)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != security.status {
					t.Fatalf("%d want %d: %s", w.Code, security.status, w.Body.String())
				}
			})
		}
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Origin", "https://unrelated.example") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	} {
		r := aiHTTPRequest(t, s, cookie, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"))
		change(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("cross-site AI request: %d", w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request incurred a provider call")
	}
}

func TestAIConsentExpiresAndDoesNotSurviveRevisionOrDate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *Server, *http.Cookie, state)
	}{
		{"provider", func(_ *testing.T, s *Server, _ *http.Cookie, _ state) { s.aiProvider = "DeepSeek" }},
		{"model", func(_ *testing.T, s *Server, _ *http.Cookie, _ state) { s.aiModel = "another-model" }},
		{"expiry", func(_ *testing.T, s *Server, _ *http.Cookie, _ state) {
			now := s.now()
			s.clock = func() time.Time { return now.Add(5*time.Minute + time.Second) }
		}},
		{"date", func(_ *testing.T, s *Server, _ *http.Cookie, _ state) {
			now := s.now()
			s.clock = func() time.Time { return now.AddDate(0, 0, 1) }
		}},
		{"revision", func(t *testing.T, s *Server, c *http.Cookie, st state) {
			requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": st.Revision, "entry": rhythm.Entry{ID: "changed", Date: st.Today, Status: "wellbeing", Sleep: floatPointer(7)}}, c), 200)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s, cookie, st := setupAI(t, aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
				calls.Add(1)
				return aiValidResult(req), nil
			}))
			view := aiView(t, s, cookie, aicoach.TaskAsk)
			tc.change(t, s, cookie, st)
			if w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"), cookie); w.Code != 409 {
				t.Fatalf("stale consent accepted: %d %s", w.Code, w.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatal("stale consent reached provider")
			}
		})
	}
}

func TestAIContextMinimizesIdentityHistoryAndCheckinFacts(t *testing.T) {
	var captured []aicoach.Request
	s, _ := setup(t, "")
	s.location = time.UTC
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return now }
	s.ai = aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
		captured = append(captured, req)
		return aiValidResult(req), nil
	})
	cookie := guest(t, s)
	saveProfile(t, s, cookie, 0)
	now = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	st := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	for _, e := range []rhythm.Entry{
		{ID: "private-old-entry", Date: "2026-10-03", Status: "wellbeing", Sleep: floatPointer(3.3)},
		{ID: "private-first-entry", Date: "2026-10-04", Status: "wellbeing", Sleep: floatPointer(6.4)},
		{ID: "private-today-entry", Date: st.Today, Status: "wellbeing", Sleep: floatPointer(7.5)},
	} {
		st = requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": st.Revision, "entry": e}, cookie), 200)
	}
	st = requireState(t, request(t, s, "POST", "/api/import", map[string]any{"revision": st.Revision, "format": "csv", "content": "id,date,kind,minutes,distance\nwatch-private-old,2026-10-03,running,77,7000\nwatch-private-new,2026-10-04,walking,25,2000\n"}, cookie), 200)
	for _, task := range []aicoach.Task{aicoach.TaskAsk, aicoach.TaskReview, aicoach.TaskCheckin} {
		view := aiView(t, s, cookie, task)
		if w := request(t, s, "POST", "/api/ai/respond", aiBody(view, task, "Сегодня спал 7 часов."), cookie); w.Code != 200 {
			t.Fatalf("%s: %d %s", task, w.Code, w.Body.String())
		}
		got := captured[len(captured)-1].Context
		if !reflect.DeepEqual(got, view.Context) {
			t.Fatal("provider saw different facts from the consent preview")
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"Анна", "private-old-entry", "private-first-entry", "private-today-entry", cookie.Value, st.SessionBinding, "watch-private", st.Workouts[0].ID, st.Workouts[1].ID, "2026-10-03", "сон 3.3"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%s context leaked %q", task, forbidden)
			}
		}
		if len(got.Days) != 14 || got.Days[0].Date != "2026-10-04" || got.Days[13].Date != st.Today {
			t.Fatalf("context dates outside fourteen days: %+v", got.Days)
		}
		facts := string(encoded)
		if task == aicoach.TaskCheckin {
			if len(got.Facts) != 2 || strings.Contains(facts, "сон 7.5") || strings.Contains(facts, "сон 6.4") || strings.Contains(facts, "Импорт за") || strings.Contains(facts, "Цель:") {
				t.Fatal("checkin sent unrelated diary facts")
			}
		} else if !strings.Contains(facts, "сон 6.4") || !strings.Contains(facts, "сон 7.5") || !strings.Contains(facts, "1 активност") || !strings.Contains(facts, "25.0 фактических минут") || !strings.Contains(facts, "2000") {
			t.Fatalf("recent facts missing or double-counted: %s", facts)
		}
	}
	after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if after.Revision != st.Revision || !reflect.DeepEqual(after.Entries, st.Entries) {
		t.Fatal("reading AI context or generating response changed the diary")
	}
}

func TestAIProviderFailureAndInvalidOutputAreSanitizedAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   aiGeneratorFunc
	}{
		{"provider_error", func(context.Context, aicoach.Request) (aicoach.Result, error) {
			return aicoach.Result{}, errors.New("secret-key-private-prompt")
		}},
		{"empty", func(context.Context, aicoach.Request) (aicoach.Result, error) { return aicoach.Result{}, nil }},
		{"invented_reference", func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
			r := aiValidResult(req)
			r.Insights[0].FactIDs = []string{"other-account"}
			return r, nil
		}},
		{"unrequested_draft", func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
			r := aiValidResult(req)
			r.Draft = &aicoach.Draft{Date: req.Context.Today, Status: "wellbeing", Sleep: floatPointer(7)}
			return r, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s, cookie, before := setupAI(t, aiGeneratorFunc(func(ctx context.Context, req aicoach.Request) (aicoach.Result, error) {
				calls.Add(1)
				return tc.fn(ctx, req)
			}))
			var logs bytes.Buffer
			s.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			view := aiView(t, s, cookie, aicoach.TaskAsk)
			w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "private-user-message"), cookie)
			if w.Code != 503 || calls.Load() != 1 || s.aiQuota.inFlight != 0 {
				t.Fatalf("failure response=%d calls=%d slots=%d", w.Code, calls.Load(), s.aiQuota.inFlight)
			}
			for _, secret := range []string{"secret-key-private-prompt", "private-user-message", cookie.Value, before.SessionBinding, "Анна"} {
				if strings.Contains(w.Body.String()+logs.String(), secret) {
					t.Fatalf("response or log leaked %q", secret)
				}
			}
			after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
			if after.Revision != before.Revision || len(after.Entries) != 0 {
				t.Fatal("failed AI response changed state")
			}
		})
	}
}

func TestAIResponseRejectsChangedRevisionWithoutHoldingRepositoryLock(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s, cookie, before := setupAI(t, aiGeneratorFunc(func(ctx context.Context, req aicoach.Request) (aicoach.Result, error) {
		close(started)
		select {
		case <-release:
			return aiValidResult(req), nil
		case <-ctx.Done():
			return aicoach.Result{}, ctx.Err()
		}
	}))
	defer close(release)
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	r := aiHTTPRequest(t, s, cookie, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"))
	w := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() { s.ServeHTTP(w, r); close(finished) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}
	write := aiHTTPRequest(t, s, cookie, "POST", "/api/entries", map[string]any{"revision": before.Revision, "entry": rhythm.Entry{ID: "during-ai", Date: before.Today, Status: "wellbeing", Sleep: floatPointer(7)}})
	writeResult := httptest.NewRecorder()
	written := make(chan struct{})
	go func() { s.ServeHTTP(writeResult, write); close(written) }()
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("optional provider call blocked diary persistence")
	}
	after := requireState(t, writeResult, 200)
	// Let the delayed call return without relying on its timeout.
	release <- struct{}{}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("AI handler did not finish")
	}
	if w.Code != 409 || after.Revision != before.Revision+1 || len(after.Entries) != 1 || s.aiQuota.inFlight != 0 || s.aiQuota.failures != 0 {
		t.Fatalf("stale AI response=%d; diary or quota state changed", w.Code)
	}
}

func TestAIProviderDeadlineCancelsCallAndReleasesSlot(t *testing.T) {
	var calls atomic.Int32
	deadlineSeen := make(chan time.Duration, 1)
	s, cookie, before := setupAI(t, aiGeneratorFunc(func(ctx context.Context, _ aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		deadline, ok := ctx.Deadline()
		if ok {
			deadlineSeen <- time.Until(deadline)
		} else {
			deadlineSeen <- -1
		}
		<-ctx.Done()
		return aicoach.Result{}, ctx.Err()
	}))
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	started := time.Now()
	w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"), cookie)
	deadline := <-deadlineSeen
	if deadline <= 0 || deadline > 3*time.Second || time.Since(started) > 6*time.Second {
		t.Fatalf("provider deadline=%v elapsed=%v", deadline, time.Since(started))
	}
	if w.Code != 503 || calls.Load() != 1 || s.aiQuota.inFlight != 0 || s.aiQuota.failures != 1 {
		t.Fatal("timeout was retried, leaked a slot, or missed breaker evidence")
	}
	after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if after.Revision != before.Revision {
		t.Fatal("timeout mutated the diary")
	}
}

func TestDeepSeekIdentityConsentAndConfiguredDeadline(t *testing.T) {
	var deadline time.Duration
	s, cookie, _ := setupAI(t, aiGeneratorFunc(func(ctx context.Context, req aicoach.Request) (aicoach.Result, error) {
		until, ok := ctx.Deadline()
		if !ok {
			t.Error("missing deadline")
		}
		deadline = time.Until(until)
		return aiValidResult(req), nil
	}))
	s.aiProvider, s.aiModel, s.aiTimeout = "DeepSeek", "deepseek-flash", 20*time.Second
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	if view.Status.Provider != "DeepSeek" || view.Status.Model != "deepseek-flash" {
		t.Fatal("wrong consent recipient")
	}
	body := aiBody(view, aicoach.TaskAsk, "Что известно?")
	body["consent"] = false
	w := request(t, s, "POST", "/api/ai/respond", body, cookie)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "DeepSeek") || strings.Contains(w.Body.String(), "OpenAI") {
		t.Fatal("incorrect consent message")
	}
	body["consent"] = true
	w = request(t, s, "POST", "/api/ai/respond", body, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"provider":"DeepSeek"`) {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	if deadline < 19*time.Second || deadline > 20*time.Second {
		t.Fatalf("deadline %v", deadline)
	}
}

func TestAIConfigurationRejectsUnknownProviderAndUnboundedTimeout(t *testing.T) {
	s, _ := setup(t, "")
	for _, tc := range []struct {
		provider string
		timeout  time.Duration
	}{{"Other", time.Second}, {"DeepSeek", -time.Second}, {"DeepSeek", 31 * time.Second}} {
		_, err := New(Config{Repository: s.repo, Secret: s.secret, Location: s.location, AIProvider: tc.provider, AITimeout: tc.timeout})
		if err == nil {
			t.Fatal("invalid AI configuration accepted")
		}
	}
}

func TestAICheckinDraftRequiresSeparateValidatedSave(t *testing.T) {
	s, cookie, before := setupAI(t, aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
		r := aiValidResult(req)
		r.Draft = &aicoach.Draft{Date: req.Context.Today, Status: "wellbeing", Sleep: floatPointer(5)}
		return r, nil
	}))
	view := aiView(t, s, cookie, aicoach.TaskCheckin)
	w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskCheckin, "Сегодня спал пять часов."), cookie)
	if w.Code != 200 {
		t.Fatalf("draft: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Mode     string         `json:"mode"`
		Revision uint64         `json:"revision"`
		Result   aicoach.Result `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Mode != "ai" || response.Revision != before.Revision || response.Result.Draft == nil || response.Result.Draft.Fatigue != nil {
		t.Fatal("draft provenance or unknown metric changed")
	}
	still := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if still.Revision != before.Revision || len(still.Entries) != 0 {
		t.Fatal("AI draft saved itself")
	}
	edited := rhythm.Entry{ID: "user-confirmed-draft", Date: response.Result.Draft.Date, Status: "wellbeing", Sleep: floatPointer(7)}
	saved := requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": response.Revision, "entry": edited}, cookie), 200)
	if len(saved.Entries) != 1 || saved.Entries[0].Sleep == nil || *saved.Entries[0].Sleep != 7 || saved.Revision != before.Revision+1 {
		t.Fatal("separate user edit was not persisted through ordinary CAS")
	}
}

func TestAICallerCancellationDoesNotOpenGlobalCircuit(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	s, cookie, _ := setupAI(t, aiGeneratorFunc(func(ctx context.Context, _ aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return aicoach.Result{}, ctx.Err()
	}))
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	for range 3 {
		r := aiHTTPRequest(t, s, cookie, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"))
		ctx, cancel := context.WithCancel(r.Context())
		w := httptest.NewRecorder()
		finished := make(chan struct{})
		go func() { s.ServeHTTP(w, r.WithContext(ctx)); close(finished) }()
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("cancelled test request did not reach provider")
		}
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("provider ignored request cancellation")
		}
		if w.Code != 503 || s.aiQuota.inFlight != 0 || s.aiQuota.failures != 0 || s.aiQuota.circuit != aiCircuitClosed {
			t.Fatal("client cancellation changed provider availability for other users")
		}
	}
	if calls.Load() != 3 || s.aiQuota.global.Count != 3 {
		t.Fatal("cancelled calls were retried or refunded their cost quota")
	}
	s.ai = aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) { return aiValidResult(req), nil })
	if w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"), cookie); w.Code != 200 {
		t.Fatalf("cancellation disabled subsequent AI: %d %s", w.Code, w.Body.String())
	}
}

func TestAIGlobalConcurrentQuotaRejectsBeforeProvider(t *testing.T) {
	started, released := make(chan struct{}, 4), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(released) })
	var calls atomic.Int32
	s, cookie, _ := setupAI(t, aiGeneratorFunc(func(ctx context.Context, req aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-released:
			return aiValidResult(req), nil
		case <-ctx.Done():
			return aicoach.Result{}, ctx.Err()
		}
	}))
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	var finished sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 4)
	for i := range responses {
		r := aiHTTPRequest(t, s, cookie, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "План?"))
		responses[i] = httptest.NewRecorder()
		finished.Add(1)
		go func() { defer finished.Done(); s.ServeHTTP(responses[i], r) }()
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("four permitted calls did not reach provider")
		}
	}
	w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "Пятый запрос"), cookie)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || calls.Load() != 4 {
		t.Fatal("global concurrency exceeded four calls or returned no retry signal")
	}
	release.Do(func() { close(released) })
	finished.Wait()
	for _, response := range responses {
		if response.Code != 200 {
			t.Fatalf("authorized response=%d %s", response.Code, response.Body.String())
		}
	}
	if s.aiQuota.inFlight != 0 || s.aiQuota.global.Count != 4 {
		t.Fatal("rejected attempt consumed quota or successful requests leaked slots")
	}
}

func TestAIHTTPBreakerUsesOnlyAuthorizedRecoveryProbes(t *testing.T) {
	var calls atomic.Int32
	s, cookie, _ := setupAI(t, aiGeneratorFunc(func(context.Context, aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		return aicoach.Result{}, errors.New("provider offline")
	}))
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	body := aiBody(view, aicoach.TaskAsk, "План?")
	for range 3 {
		if w := request(t, s, "POST", "/api/ai/respond", body, cookie); w.Code != 503 {
			t.Fatalf("provider failure=%d", w.Code)
		}
	}
	if w := request(t, s, "POST", "/api/ai/respond", body, cookie); w.Code != 429 || calls.Load() != 3 {
		t.Fatal("open circuit made a fourth paid call")
	}
	now := s.now()
	s.clock = func() time.Time { return now.Add(time.Minute) }
	s.ai = aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
		calls.Add(1)
		return aiValidResult(req), nil
	})
	recoveryCookie := guest(t, s)
	saveProfile(t, s, recoveryCookie, 0)
	recoveryView := aiView(t, s, recoveryCookie, aicoach.TaskReview)
	recoveryBody := aiBody(recoveryView, aicoach.TaskReview, "")
	for i := range 5 {
		if w := request(t, s, "POST", "/api/ai/respond", recoveryBody, recoveryCookie); w.Code != 200 {
			t.Fatalf("authorized recovery %d: %d %s", i, w.Code, w.Body.String())
		}
		if i < 4 && s.aiQuota.circuit != aiCircuitHalfOpen {
			t.Fatal("fewer than five probes closed circuit")
		}
	}
	if s.aiQuota.circuit != aiCircuitClosed || calls.Load() != 8 || s.aiQuota.global.Count != 8 {
		t.Fatal("circuit recovery made background calls or ignored quota")
	}
}

func TestAIResponseRejectsDateRolloverDuringProviderCall(t *testing.T) {
	var now atomic.Int64
	s, cookie, before := setupAI(t, aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
		now.Add(int64(24 * time.Hour))
		return aiValidResult(req), nil
	}))
	now.Store(s.now().UnixNano())
	s.clock = func() time.Time { return time.Unix(0, now.Load()) }
	view := aiView(t, s, cookie, aicoach.TaskAsk)
	w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskAsk, "Что сегодня?"), cookie)
	if w.Code != 409 || strings.Contains(w.Body.String(), `"mode":"ai"`) {
		t.Fatalf("yesterday's answer presented as current: %d %s", w.Code, w.Body.String())
	}
	after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
	if after.Revision != before.Revision || len(after.Entries) != 0 {
		t.Fatal("date rollover response changed diary")
	}
}
