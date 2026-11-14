package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConfigRestrictsTargetsAndWorkload(t *testing.T) {
	base := config{URL: "http://127.0.0.1:8080", RPS: 10, Duration: time.Second, Workers: 4}
	for _, tc := range []struct {
		name   string
		change func(*config)
		valid  bool
	}{
		{"IPv4", func(*config) {}, true},
		{"IPv6", func(c *config) { c.URL = "http://[::1]:8080/" }, true},
		{"localhost", func(c *config) { c.URL = "http://localhost:8080" }, true},
		{"remote", func(c *config) { c.URL = "https://example.com" }, false},
		{"explicit_remote", func(c *config) { c.URL = "https://example.com"; c.AllowRemote = true }, true},
		{"credentials", func(c *config) { c.URL = "http://user:secret@localhost:8080" }, false},
		{"query", func(c *config) { c.URL += "?secret=value" }, false},
		{"empty_query", func(c *config) { c.URL += "?" }, false},
		{"fragment", func(c *config) { c.URL += "#secret" }, false},
		{"path", func(c *config) { c.URL += "/api" }, false},
		{"scheme", func(c *config) { c.URL = "file:///tmp/data" }, false},
		{"invalid_port", func(c *config) { c.URL = "http://localhost:65536" }, false},
		{"no_rate", func(c *config) { c.RPS = 0 }, false},
		{"max_rate", func(c *config) { c.RPS = 1000 }, true},
		{"excess_rate", func(c *config) { c.RPS = 1001 }, false},
		{"short_window", func(c *config) { c.Duration = time.Second - time.Nanosecond }, false},
		{"max_window", func(c *config) { c.Duration = 10 * time.Minute }, true},
		{"long_window", func(c *config) { c.Duration = 10*time.Minute + time.Nanosecond }, false},
		{"no_workers", func(c *config) { c.Workers = 0 }, false},
		{"excess_workers", func(c *config) { c.Workers = 33 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.change(&c)
			_, err := c.validate()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

// fakeAPI is a stateful external contract fixture, not an implementation of
// the domain rules. It enforces a separate revision and binding for each jar.
type fakeRecord struct {
	revision uint64
	binding  string
	profile  *profile
	entry    *entry
}

type fakeAPI struct {
	mu             sync.Mutex
	records        map[string]*fakeRecord
	profiles       int
	writes         int
	deletes        int
	badBindings    int
	conflicts      int
	delay          time.Duration
	entryStarted   chan struct{}
	corruptEntry   func(map[string]any)
	corruptProfile bool
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	api := &fakeAPI{records: make(map[string]*fakeRecord)}
	server := httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(server.Close)
	return api, server
}

func (a *fakeAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var record *fakeRecord
	if cookie, err := r.Cookie("test_guest"); err == nil {
		record = a.records[cookie.Value]
	}
	if record == nil {
		if r.Method != "GET" || r.URL.Path != "/api/state" {
			http.Error(w, "missing guest", http.StatusUnauthorized)
			return
		}
		id := fmt.Sprintf("guest-%d", len(a.records)+1)
		record = &fakeRecord{binding: "binding-" + id}
		a.records[id] = record
		http.SetCookie(w, &http.Cookie{Name: "test_guest", Value: id, Path: "/", HttpOnly: true})
	}
	if r.Method != "GET" && r.Header.Get("X-Rhythm-Session") != record.binding {
		a.badBindings++
		http.Error(w, "bad binding", http.StatusForbidden)
		return
	}
	var corrupt bool
	switch r.Method + " " + r.URL.Path {
	case "GET /api/state":
	case "POST /api/profile":
		var input struct {
			Revision uint64  `json:"revision"`
			Profile  profile `json:"profile"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != record.revision || !expectedProfile(&input.Profile) {
			a.conflicts++
			http.Error(w, "profile contract", http.StatusConflict)
			return
		}
		a.profiles++
		record.profile, record.revision = &input.Profile, record.revision+1
	case "POST /api/entries":
		var input struct {
			Revision uint64 `json:"revision"`
			Entry    entry  `json:"entry"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != record.revision || record.profile == nil || input.Entry.Status != "wellbeing" || input.Entry.Sleep != 5 || input.Entry.Fatigue != 9 {
			a.conflicts++
			http.Error(w, "entry contract", http.StatusConflict)
			return
		}
		if a.entryStarted != nil {
			select {
			case a.entryStarted <- struct{}{}:
			default:
			}
		}
		if a.delay > 0 {
			timer := time.NewTimer(a.delay)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
		}
		a.writes++
		record.entry, record.revision = &input.Entry, record.revision+1
		corrupt = true
	case "DELETE /api/data":
		var input struct {
			Revision uint64 `json:"revision"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != record.revision {
			a.conflicts++
			http.Error(w, "delete revision", http.StatusConflict)
			return
		}
		a.deletes++
		record.profile, record.entry, record.revision = nil, nil, record.revision+1
	default:
		http.NotFound(w, r)
		return
	}
	state := fakeState(record)
	if corrupt && a.corruptEntry != nil {
		a.corruptEntry(state)
	}
	if a.corruptProfile && r.URL.Path == "/api/profile" {
		state["revision"] = 999
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(state); err != nil {
		return // Client cancellation is expected in the cancellation test.
	}
}

func fakeState(record *fakeRecord) map[string]any {
	entries := []entry{}
	state := map[string]any{
		"revision": record.revision, "identity": "guest", "sessionBinding": record.binding,
		"profile": record.profile, "entries": entries, "plan": nil, "nextPlan": nil, "decision": nil,
	}
	if record.profile != nil {
		state["plan"] = map[string]any{"totalMinutes": 135}
		state["nextPlan"] = map[string]any{"totalMinutes": 135}
		state["decision"] = map[string]any{"mode": "start"}
	}
	if record.entry != nil {
		state["entries"] = []entry{*record.entry}
		state["nextPlan"] = map[string]any{"totalMinutes": 72}
		state["decision"] = map[string]any{"mode": "recover"}
	}
	return state
}

func testWorker(t *testing.T, origin string) *worker {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return &worker{client: &http.Client{Jar: jar, Timeout: requestTimeout}, origin: origin, location: location, prefix: "load-1-"}
}

func TestHTTP200MustContainCorrectWrittenResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(map[string]any)
	}{
		{"revision", func(s map[string]any) { s["revision"] = 42 }},
		{"plan", func(s map[string]any) { s["nextPlan"] = map[string]any{"totalMinutes": 135} }},
		{"decision", func(s map[string]any) { s["decision"] = map[string]any{"mode": "progress"} }},
		{"binding", func(s map[string]any) { s["sessionBinding"] = "another-user" }},
		{"missing_fact", func(s map[string]any) { s["entries"] = []entry{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, server := newFakeAPI(t)
			api.corruptEntry = tc.corrupt
			w := testWorker(t, server.URL)
			if err := w.setup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := w.write(context.Background()); err == nil || failureKind(err) != "response_contract" {
				t.Fatalf("invalid HTTP 200 was accepted: %v", err)
			}
			if w.revision != 1 {
				t.Fatal("unverified revision was trusted")
			}
			if err := w.cleanup(context.Background()); err != nil {
				t.Fatalf("cleanup must read back the actually committed revision: %v", err)
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.deletes != 1 || api.conflicts != 0 || api.badBindings != 0 {
				t.Fatal("cleanup lost revision or session binding")
			}
		})
	}
}

func TestFullRunUsesIsolatedWorkersAndCleansOnlyTheirData(t *testing.T) {
	api, server := newFakeAPI(t)
	c := config{URL: server.URL, RPS: 20, Duration: time.Second, Workers: 3}
	r, err := runLoad(context.Background(), c)
	if err != nil {
		t.Fatalf("run: %v; report=%+v", err, r)
	}
	if !r.Passed || r.Requests != 20 || r.Succeeded != 20 || r.Scheduled != 20 || r.Errors != 0 || r.Dropped != 0 || r.SetupErrors != 0 || r.CleanupErrors != 0 {
		t.Fatalf("incorrect accounting: %+v", r)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.records) != 3 || api.profiles != 3 || api.writes != 20 || api.deletes != 3 || api.conflicts != 0 || api.badBindings != 0 {
		t.Fatalf("workers shared state or measured setup/cleanup: %+v", api)
	}
	for _, record := range api.records {
		if record.profile != nil || record.entry != nil {
			t.Fatal("synthetic user data was not cleaned")
		}
	}
}

func TestWriteAllowsPriorDayWhenMeasurementCrossesMidnight(t *testing.T) {
	api, server := newFakeAPI(t)
	api.corruptEntry = func(state map[string]any) {
		entries := state["entries"].([]entry)
		previous := entries[0]
		date, err := time.Parse(time.DateOnly, previous.Date)
		if err != nil {
			t.Error(err)
			return
		}
		previous.Date = date.AddDate(0, 0, -1).Format(time.DateOnly)
		previous.ID = "load-1-previous"
		state["entries"] = append([]entry{previous}, entries...)
	}
	w := testWorker(t, server.URL)
	if err := w.setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.write(context.Background()); err != nil {
		t.Fatalf("a valid previous day must not fail the write oracle: %v", err)
	}
	if err := w.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSlowServerDropsAreCountedAndFailCriteria(t *testing.T) {
	api, server := newFakeAPI(t)
	api.delay = 40 * time.Millisecond
	r, err := runLoad(context.Background(), config{URL: server.URL, RPS: 100, Duration: time.Second, Workers: 1})
	if !errors.Is(err, errCriteria) || r.Passed || r.Dropped == 0 || r.Errors < r.Dropped || r.Requests+r.Dropped != r.Scheduled || r.Scheduled != r.Planned || r.CleanupErrors != 0 {
		t.Fatalf("lost or hidden queue drops: err=%v report=%+v", err, r)
	}
	if r.Criteria.NoErrors || r.Criteria.AllPlannedSucceeded || !r.Criteria.AllRequestsAccounted {
		t.Fatalf("wrong criteria for overloaded target: %+v", r.Criteria)
	}
}

func TestCancellationStopsWorkersAndStillCleansTheirData(t *testing.T) {
	api, server := newFakeAPI(t)
	api.delay = 5 * time.Second
	api.entryStarted = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		report report
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := runLoad(ctx, config{URL: server.URL, RPS: 10, Duration: time.Second, Workers: 2})
		done <- result{r, err}
	}()
	select {
	case <-api.entryStarted:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("request never started")
	}
	select {
	case got := <-done:
		if got.err == nil || got.report.Passed || !got.report.Interrupted || got.report.CleanupErrors != 0 || got.report.Requests+got.report.Dropped != got.report.Scheduled {
			t.Fatalf("cancellation/cleanup: err=%v report=%+v", got.err, got.report)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("workers did not stop after context cancellation")
	}
}

func TestCleanupRefusesUnexpectedPersonalData(t *testing.T) {
	api, server := newFakeAPI(t)
	w := testWorker(t, server.URL)
	if err := w.setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, record := range api.records {
		record.profile.Name = "Personal data"
	}
	api.mu.Unlock()
	if err := w.cleanup(context.Background()); err == nil {
		t.Fatal("cleanup accepted unexpected personal data")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.deletes != 0 {
		t.Fatal("cleanup deleted data that was not recognized as synthetic")
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var called bool
	var mu sync.Mutex
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called = true
		mu.Unlock()
		http.Error(w, "must not be called", 500)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer redirect.Close()
	r, err := runLoad(context.Background(), config{URL: redirect.URL, RPS: 1, Duration: time.Second, Workers: 1})
	mu.Lock()
	defer mu.Unlock()
	if err == nil || called || r.SetupErrors != 1 || r.Requests != 0 {
		t.Fatalf("redirect escaped target validation: err=%v called=%v report=%+v", err, called, r)
	}
}

func TestCLIStoresFailedReportWithoutSessionSecrets(t *testing.T) {
	api, server := newFakeAPI(t)
	api.corruptProfile = true
	path := filepath.Join(t.TempDir(), "evidence", "failed.json")
	var stdout, stderr bytes.Buffer
	err := runCLI(context.Background(), []string{"-url", server.URL, "-rps", "1", "-duration", "1s", "-workers", "1", "-output", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("CLI succeeded despite incorrect setup response")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(data, stdout.Bytes()) || strings.Contains(string(data), "binding-guest") || strings.Contains(string(data), "test_guest") {
		t.Fatal("report differs from stdout or exposes session credentials")
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil || r.Passed || r.SetupErrors != 1 || r.CleanupErrors != 0 {
		t.Fatalf("failed run is not honestly recorded: %v %+v", err, r)
	}
}
