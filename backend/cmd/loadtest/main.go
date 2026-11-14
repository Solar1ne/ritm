// Command loadtest exercises Rhythm's real HTTP write path with isolated,
// synthetic guests. It is a bounded smoke test, not a capacity estimate.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"
)

const requestTimeout = 2 * time.Second

var errCriteria = errors.New("load-test criteria failed; inspect the JSON report")

type config struct {
	URL         string
	RPS         int
	Duration    time.Duration
	Workers     int
	AllowRemote bool
	Output      string
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var c config
	flags := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&c.URL, "url", "http://127.0.0.1:8080", "Rhythm origin; loopback only unless -allow-remote")
	flags.IntVar(&c.RPS, "rps", 10, "scheduled write requests per second (1..1000)")
	flags.DurationVar(&c.Duration, "duration", 30*time.Second, "measurement window (1s..10m)")
	flags.IntVar(&c.Workers, "workers", 4, "isolated guest workers (1..32)")
	flags.BoolVar(&c.AllowRemote, "allow-remote", false, "explicitly allow a non-loopback target")
	flags.StringVar(&c.Output, "output", "", "optional JSON report file; stdout always receives the report")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}
	u, err := c.validate()
	if err != nil {
		return config{}, err
	}
	c.URL = u.String()
	return c, nil
}

func (c config) validate() (*url.URL, error) {
	if c.RPS < 1 || c.RPS > 1000 || c.Duration < time.Second || c.Duration > 10*time.Minute || c.Workers < 1 || c.Workers > 32 {
		return nil, errors.New("require rps 1..1000, duration 1s..10m and workers 1..32")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("url must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("url port must be 1..65535")
		}
	}
	if !c.AllowRemote && !isLoopbackHost(u.Hostname()) {
		return nil, errors.New("non-loopback targets require explicit -allow-remote")
	}
	u.Path = ""
	return u, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type percentiles struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	MaxMS float64 `json:"max_ms"`
}

type criteria struct {
	MaximumP95MS           float64 `json:"maximum_p95_ms"`
	MinimumAchievedRPS     float64 `json:"minimum_achieved_rps"`
	RequestLatencyPassed   bool    `json:"request_latency_passed"`
	ScheduledLatencyPassed bool    `json:"scheduled_latency_passed"`
	NoErrors               bool    `json:"no_errors"`
	AllRequestsAccounted   bool    `json:"all_requests_accounted"`
	AllPlannedSucceeded    bool    `json:"all_planned_succeeded"`
	TargetRateAchieved     bool    `json:"target_rate_achieved"`
}

type report struct {
	SchemaVersion    int            `json:"schema_version"`
	StartedAt        time.Time      `json:"started_at"`
	TargetURL        string         `json:"target_url"`
	LoopbackTarget   bool           `json:"loopback_target"`
	Scope            string         `json:"scope"`
	TargetRPS        int            `json:"target_rps"`
	DurationSeconds  float64        `json:"duration_seconds"`
	ElapsedSeconds   float64        `json:"elapsed_seconds"`
	Workers          int            `json:"workers"`
	QueueCapacity    int            `json:"queue_capacity"`
	Planned          int            `json:"planned_requests"`
	Scheduled        int            `json:"scheduled_requests"`
	Requests         int            `json:"requests"`
	Succeeded        int            `json:"succeeded"`
	Errors           int            `json:"errors"`
	Dropped          int            `json:"dropped"`
	SetupErrors      int            `json:"setup_errors"`
	CleanupErrors    int            `json:"cleanup_errors"`
	Interrupted      bool           `json:"interrupted"`
	AchievedRPS      float64        `json:"achieved_rps"`
	SuccessfulRPS    float64        `json:"successful_rps"`
	Latency          percentiles    `json:"request_latency"`
	ScheduledLatency percentiles    `json:"scheduled_latency"`
	DispatchLag      percentiles    `json:"dispatch_lag"`
	FailureCounts    map[string]int `json:"failure_counts"`
	Environment      environment    `json:"environment"`
	Criteria         criteria       `json:"criteria"`
	Passed           bool           `json:"passed"`
}

type environment struct {
	GoVersion  string `json:"go_version"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	CPUs       int    `json:"cpus"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	TimeZone   string `json:"time_zone"`
}

// These DTOs deliberately do not import the server's domain: the black-box
// oracle must check externally visible behavior independently of its code.
type profile struct {
	Name       string `json:"name"`
	Goal       string `json:"goal"`
	Experience string `json:"experience"`
	Days       []int  `json:"days"`
	Minutes    int    `json:"minutes"`
}

type entry struct {
	ID      string  `json:"id"`
	Date    string  `json:"date"`
	Status  string  `json:"status"`
	Sleep   float64 `json:"sleep"`
	Fatigue int     `json:"fatigue"`
}

type apiState struct {
	Revision       *uint64  `json:"revision"`
	Identity       string   `json:"identity"`
	SessionBinding string   `json:"sessionBinding"`
	Profile        *profile `json:"profile"`
	Entries        []entry  `json:"entries"`
	Plan           *struct {
		TotalMinutes int `json:"totalMinutes"`
	} `json:"plan"`
	NextPlan *struct {
		TotalMinutes int `json:"totalMinutes"`
	} `json:"nextPlan"`
	Decision *struct {
		Mode string `json:"mode"`
	} `json:"decision"`
}

func syntheticProfile() profile {
	return profile{Name: "Load test", Goal: "tone", Experience: "beginner", Days: []int{0, 2, 4}, Minutes: 45}
}

func expectedProfile(p *profile) bool {
	want := syntheticProfile()
	return p != nil && p.Name == want.Name && p.Goal == want.Goal && p.Experience == want.Experience && p.Minutes == want.Minutes && slices.Equal(p.Days, want.Days)
}

type checkError struct {
	kind   string
	detail string
}

func (e *checkError) Error() string { return e.kind + ": " + e.detail }

func contractError(detail string) error {
	return &checkError{kind: "response_contract", detail: detail}
}

func failureKind(err error) string {
	var check *checkError
	if errors.As(err, &check) {
		return check.kind
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "transport"
}

type worker struct {
	client   *http.Client
	origin   string
	location *time.Location
	prefix   string
	binding  string
	revision uint64
	sequence uint64
	owned    bool
}

func (w *worker) request(ctx context.Context, method, route string, input any) (apiState, error) {
	var data []byte
	var err error
	if input != nil {
		data, err = json.Marshal(input)
		if err != nil {
			return apiState{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, w.origin+route, bytes.NewReader(data))
	if err != nil {
		return apiState{}, err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Rhythm-Session", w.binding)
	}
	response, err := w.client.Do(req)
	if err != nil {
		return apiState{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return apiState{}, errors.Join(readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK {
		return apiState{}, &checkError{kind: "http_status", detail: strconv.Itoa(response.StatusCode)}
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(body) > 1024*1024 {
		return apiState{}, contractError("invalid response type or size")
	}
	var result apiState
	if err := json.Unmarshal(body, &result); err != nil {
		return apiState{}, contractError("invalid JSON response")
	}
	return result, nil
}

func (w *worker) setup(ctx context.Context) error {
	initial, err := w.request(ctx, http.MethodGet, "/api/state", nil)
	if err != nil {
		return err
	}
	if initial.Revision == nil || *initial.Revision != 0 || initial.Profile != nil || initial.Entries == nil || len(initial.Entries) != 0 || initial.Identity != "guest" || initial.SessionBinding == "" {
		return contractError("setup requires a fresh, empty guest")
	}
	w.binding, w.owned = initial.SessionBinding, true
	created, err := w.request(ctx, http.MethodPost, "/api/profile", struct {
		Revision uint64  `json:"revision"`
		Profile  profile `json:"profile"`
	}{Profile: syntheticProfile()})
	if err != nil {
		return err
	}
	if created.Revision == nil || *created.Revision != 1 || created.Identity != "guest" || created.SessionBinding != w.binding || !expectedProfile(created.Profile) || created.Plan == nil || created.Plan.TotalMinutes != 135 || created.NextPlan == nil || created.NextPlan.TotalMinutes != 135 || created.Decision == nil || created.Decision.Mode != "start" {
		return contractError("profile setup did not create the expected baseline")
	}
	w.revision = *created.Revision
	return nil
}

func (w *worker) write(ctx context.Context) error {
	w.sequence++
	input := struct {
		Revision uint64 `json:"revision"`
		Entry    entry  `json:"entry"`
	}{Revision: w.revision, Entry: entry{
		ID: w.prefix + strconv.FormatUint(w.sequence, 10), Date: time.Now().In(w.location).Format(time.DateOnly),
		Status: "wellbeing", Sleep: 5, Fatigue: 9,
	}}
	state, err := w.request(ctx, http.MethodPost, "/api/entries", input)
	if err != nil {
		return err
	}
	if state.Revision == nil || *state.Revision != w.revision+1 || state.Identity != "guest" || state.SessionBinding != w.binding || state.NextPlan == nil || state.NextPlan.TotalMinutes != 72 || state.Decision == nil || state.Decision.Mode != "recover" {
		return contractError("entry response has incorrect revision, identity or recovery plan")
	}
	found := 0
	for _, e := range state.Entries {
		if e == input.Entry {
			found++
			continue
		}
		// A measurement can cross Moscow midnight. Prior days may remain,
		// but another fact for today's date or another worker is invalid.
		if !strings.HasPrefix(e.ID, w.prefix) || e.Date >= input.Entry.Date || e.Status != "wellbeing" || e.Sleep != 5 || e.Fatigue != 9 {
			return contractError("entry response contains an unexpected synthetic fact")
		}
	}
	if found != 1 {
		return contractError("entry response does not contain the written synthetic fact")
	}
	w.revision = *state.Revision
	return nil
}

func (w *worker) cleanup(ctx context.Context) error {
	if !w.owned {
		return nil
	}
	// A timed-out write may have committed. Read back before using revision,
	// and never delete state that fails the synthetic-data ownership checks.
	state, err := w.request(ctx, http.MethodGet, "/api/state", nil)
	if err != nil {
		return err
	}
	if state.Revision == nil || state.Identity != "guest" || state.SessionBinding != w.binding {
		return contractError("cleanup refused an unrecognized guest")
	}
	if state.Profile == nil && len(state.Entries) == 0 {
		return nil
	}
	if !expectedProfile(state.Profile) {
		return contractError("cleanup refused a non-synthetic profile")
	}
	for _, e := range state.Entries {
		if !strings.HasPrefix(e.ID, w.prefix) || e.Status != "wellbeing" || e.Sleep != 5 || e.Fatigue != 9 {
			return contractError("cleanup refused non-synthetic entries")
		}
	}
	cleared, err := w.request(ctx, http.MethodDelete, "/api/data", struct {
		Revision uint64 `json:"revision"`
	}{Revision: *state.Revision})
	if err != nil {
		return err
	}
	if cleared.Revision == nil || *cleared.Revision != *state.Revision+1 || cleared.Identity != "guest" || cleared.SessionBinding != w.binding || cleared.Profile != nil || len(cleared.Entries) != 0 || cleared.Plan != nil || cleared.NextPlan != nil || cleared.Decision != nil {
		return contractError("cleanup did not confirm an empty guest")
	}
	return nil
}

func newTransport(c config) *http.Transport {
	dialer := &net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}
	dial := dialer.DialContext
	if !c.AllowRemote {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !isLoopbackHost(host) {
				return nil, errors.New("refusing non-loopback connection")
			}
			if !strings.EqualFold(host, "localhost") {
				return dialer.DialContext(ctx, network, address)
			}
			// Pin localhost to loopback addresses, independent of DNS or proxies.
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			if err == nil {
				return connection, nil
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort("::1", port))
		}
	}
	return &http.Transport{
		Proxy: nil, DialContext: dial, ForceAttemptHTTP2: true,
		MaxIdleConns: c.Workers, MaxIdleConnsPerHost: c.Workers, MaxConnsPerHost: c.Workers,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: requestTimeout, ResponseHeaderTimeout: requestTimeout,
	}
}

type task struct{ scheduled time.Time }
type outcome struct {
	latency, scheduledLatency, lag time.Duration
	err                            error
}
type scheduleResult struct {
	scheduled, dropped int
	interrupted        bool
}

func plannedRequests(c config) int {
	return int((int64(c.Duration)*int64(c.RPS) + int64(time.Second) - 1) / int64(time.Second))
}

// schedule uses a bounded open-workload queue: a slow server cannot silently
// reduce the offered rate. Missing ticker notifications are accounted for by
// the fixed start time; every due slot is enqueued or explicitly dropped.
func schedule(ctx context.Context, c config, start time.Time, tasks chan<- task) scheduleResult {
	defer close(tasks)
	ticker := time.NewTicker(time.Second / time.Duration(c.RPS))
	defer ticker.Stop()
	deadline := time.NewTimer(time.Until(start.Add(c.Duration)))
	defer deadline.Stop()
	var result scheduleResult
	planned := plannedRequests(c)
	emitDue := func(now time.Time) {
		for result.scheduled < planned {
			due := start.Add(time.Duration(int64(result.scheduled) * int64(time.Second) / int64(c.RPS)))
			if due.After(now) {
				return
			}
			result.scheduled++
			select {
			case tasks <- task{scheduled: due}:
			default:
				result.dropped++
			}
		}
	}
	emitDue(start)
	for {
		select {
		case <-ctx.Done():
			result.interrupted = true
			return result
		case <-deadline.C:
			emitDue(start.Add(c.Duration))
			return result
		case now := <-ticker.C:
			emitDue(now)
		}
	}
}

func measure(ctx context.Context, c config, workers []*worker, r *report) {
	tasks := make(chan task, c.Workers)
	results := make(chan outcome, c.Workers)
	var group sync.WaitGroup
	for _, w := range workers {
		group.Add(1)
		go func(w *worker) {
			defer group.Done()
			for t := range tasks {
				started := time.Now()
				err := w.write(ctx)
				finished := time.Now()
				results <- outcome{latency: finished.Sub(started), scheduledLatency: finished.Sub(t.scheduled), lag: started.Sub(t.scheduled), err: err}
			}
		}(w)
	}
	started := time.Now()
	r.StartedAt = started.UTC()
	scheduling := make(chan scheduleResult, 1)
	go func() { scheduling <- schedule(ctx, c, started, tasks) }()
	go func() { group.Wait(); close(results) }()
	latencies := make([]time.Duration, 0, r.Planned)
	scheduledLatencies := make([]time.Duration, 0, r.Planned)
	lags := make([]time.Duration, 0, r.Planned)
	for result := range results {
		r.Requests++
		latencies = append(latencies, result.latency)
		scheduledLatencies = append(scheduledLatencies, result.scheduledLatency)
		lags = append(lags, result.lag)
		if result.err == nil {
			r.Succeeded++
		} else {
			r.Errors++
			r.FailureCounts[failureKind(result.err)]++
		}
	}
	r.ElapsedSeconds = time.Since(started).Seconds()
	scheduled := <-scheduling
	r.Scheduled, r.Dropped, r.Interrupted = scheduled.scheduled, scheduled.dropped, scheduled.interrupted
	r.Errors += r.Dropped
	if r.Dropped > 0 {
		r.FailureCounts["queue_drop"] = r.Dropped
	}
	r.AchievedRPS = float64(r.Requests) / r.ElapsedSeconds
	r.SuccessfulRPS = float64(r.Succeeded) / r.ElapsedSeconds
	r.Latency, r.ScheduledLatency, r.DispatchLag = summarize(latencies), summarize(scheduledLatencies), summarize(lags)
}

func summarize(values []time.Duration) percentiles {
	if len(values) == 0 {
		return percentiles{}
	}
	slices.Sort(values)
	percentile := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(values)))) - 1
		return float64(values[max(0, index)]) / float64(time.Millisecond)
	}
	return percentiles{P50MS: percentile(0.50), P95MS: percentile(0.95), P99MS: percentile(0.99), MaxMS: percentile(1)}
}

func cleanupWorkers(parent context.Context, workers []*worker, r *report) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	errors := make(chan error, len(workers))
	for _, w := range workers {
		go func(w *worker) { errors <- w.cleanup(ctx) }(w)
	}
	for range workers {
		if err := <-errors; err != nil {
			r.CleanupErrors++
			r.FailureCounts["cleanup_"+failureKind(err)]++
		}
	}
}

func evaluate(r *report) {
	r.Criteria = criteria{
		MaximumP95MS: 450, MinimumAchievedRPS: float64(r.TargetRPS) * .95,
		RequestLatencyPassed:   r.Requests > 0 && r.Latency.P95MS <= 450,
		ScheduledLatencyPassed: r.Requests > 0 && r.ScheduledLatency.P95MS <= 450,
		NoErrors:               r.Errors == 0 && r.SetupErrors == 0 && r.CleanupErrors == 0 && !r.Interrupted,
		AllRequestsAccounted:   r.Requests+r.Dropped == r.Scheduled,
		AllPlannedSucceeded:    r.Succeeded == r.Planned && r.Scheduled == r.Planned && r.Dropped == 0,
		TargetRateAchieved:     r.AchievedRPS >= float64(r.TargetRPS)*.95,
	}
	c := r.Criteria
	r.Passed = c.RequestLatencyPassed && c.ScheduledLatencyPassed && c.NoErrors && c.AllRequestsAccounted && c.AllPlannedSucceeded && c.TargetRateAchieved
}

func runLoad(ctx context.Context, c config) (report, error) {
	u, err := c.validate()
	if err != nil {
		return report{}, err
	}
	c.URL = u.String()
	r := report{
		SchemaVersion: 1, StartedAt: time.Now().UTC(), TargetURL: c.URL, LoopbackTarget: isLoopbackHost(u.Hostname()),
		Scope:     "HTTP write smoke for Rhythm Go and the configured storage adapter; record /api/health alongside this report to identify the backend. Setup/cleanup excluded; no browser rendering, mobile-network or public-capacity claim. Request latency includes response validation; scheduled latency includes generator/queue lag. Errors include queue drops. Fractional windows schedule ceil(duration*rps) requests.",
		TargetRPS: c.RPS, DurationSeconds: c.Duration.Seconds(), Workers: c.Workers, QueueCapacity: c.Workers, Planned: plannedRequests(c),
		FailureCounts: make(map[string]int),
		Environment:   environment{GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), TimeZone: "Europe/Moscow"},
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return r, fmt.Errorf("load time zone: %w", err)
	}
	transport := newTransport(c)
	defer transport.CloseIdleConnections()
	workers := make([]*worker, 0, c.Workers)
	bindings := make(map[string]struct{}, c.Workers)
	for i := range c.Workers {
		jar, err := cookiejar.New(nil)
		if err != nil {
			cleanupWorkers(ctx, workers, &r)
			return r, fmt.Errorf("create isolated cookie jar: %w", err)
		}
		w := &worker{origin: c.URL, location: location, prefix: fmt.Sprintf("load-%d-", i+1), client: &http.Client{
			Transport: transport, Jar: jar, Timeout: requestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}}
		workers = append(workers, w)
		if err := w.setup(ctx); err != nil {
			r.SetupErrors++
			r.FailureCounts["setup_"+failureKind(err)]++
			cleanupWorkers(ctx, workers, &r)
			evaluate(&r)
			return r, fmt.Errorf("setup worker %d: %w", i+1, err)
		}
		if _, exists := bindings[w.binding]; exists {
			r.SetupErrors++
			r.FailureCounts["setup_shared_identity"]++
			cleanupWorkers(ctx, workers, &r)
			evaluate(&r)
			return r, errors.New("workers did not receive isolated guest identities")
		}
		bindings[w.binding] = struct{}{}
	}
	measure(ctx, c, workers, &r)
	cleanupWorkers(ctx, workers, &r)
	evaluate(&r)
	if !r.Passed {
		return r, errCriteria
	}
	return r, nil
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	c, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	r, runErr := runLoad(ctx, c)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errors.Join(runErr, err)
	}
	data = append(data, '\n')
	if c.Output != "" {
		if err := os.MkdirAll(filepath.Dir(c.Output), 0755); err != nil {
			return errors.Join(runErr, fmt.Errorf("create report directory: %w", err))
		}
		if err := os.WriteFile(c.Output, data, 0644); err != nil {
			return errors.Join(runErr, fmt.Errorf("write report: %w", err))
		}
	}
	if _, err := stdout.Write(data); err != nil {
		return errors.Join(runErr, fmt.Errorf("write report to stdout: %w", err))
	}
	return runErr
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
