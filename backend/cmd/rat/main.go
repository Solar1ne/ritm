// Command rat runs fixed synthetic acceptance cases and a measured local timing
// experiment. It does not claim that technical checks establish user retention.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"rhythm/internal/rat"
	"rhythm/internal/rhythm"
)

type caseResult struct {
	Name             string `json:"name"`
	Critical         bool   `json:"critical"`
	Expected         string `json:"expected"`
	Actual           string `json:"actual"`
	ExpectedSessions int    `json:"expectedSessions"`
	ActualSessions   int    `json:"actualSessions"`
	ExpectedMinutes  int    `json:"expectedMinutes"`
	ActualMinutes    int    `json:"actualMinutes"`
	Passed           bool   `json:"passed"`
}
type report struct {
	GeneratedAt    string       `json:"generatedAt"`
	Scope          string       `json:"scope"`
	GoVersion      string       `json:"goVersion"`
	Platform       string       `json:"platform"`
	Iterations     int          `json:"iterations"`
	Cases          []caseResult `json:"cases"`
	PassedCases    int          `json:"passedCases"`
	TotalCases     int          `json:"totalCases"`
	CriticalPassed int          `json:"criticalPassed"`
	CriticalTotal  int          `json:"criticalTotal"`
	ErrorCalls     int          `json:"errorCalls"`
	P50MS          float64      `json:"p50Ms"`
	P95MS          float64      `json:"p95Ms"`
	MaxMS          float64      `json:"maxMs"`
	Thresholds     struct {
		SuccessRatio  float64 `json:"successRatio"`
		CriticalRatio float64 `json:"criticalRatio"`
		P95MS         float64 `json:"p95Ms"`
	} `json:"thresholds"`
	Passed bool `json:"passed"`
}

func main() {
	jsonOutput := flag.Bool("json", false, "write machine-readable JSON")
	iterations := flag.Int("iterations", 5000, "measured calls (minimum 5000)")
	flag.Parse()
	if *iterations < 5000 || *iterations > 1000000 {
		fmt.Fprintln(os.Stderr, "iterations must be between 5000 and 1000000")
		os.Exit(2)
	}
	// Fixed fixture week makes the input reproducible across execution dates.
	fixtureNow := time.Date(2026, 10, 17, 12, 0, 0, 0, time.FixedZone("Moscow", 3*3600))
	cases := rat.Cases(fixtureNow)
	r := report{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Scope: "Synthetic deterministic rule engine + input validation, in process; excludes HTTP, storage, network and user outcomes.", GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Iterations: *iterations, TotalCases: len(cases), Cases: make([]caseResult, 0, len(cases))}
	r.Thresholds.SuccessRatio = .90
	r.Thresholds.CriticalRatio = 1
	r.Thresholds.P95MS = 50
	for _, tc := range cases {
		projection, err := rhythm.Preview(tc.Profile, tc.Entries, fixtureNow)
		result := caseResult{Name: tc.Name, Critical: tc.Critical, Expected: tc.Mode, ExpectedSessions: tc.Sessions, ExpectedMinutes: tc.Minutes, Actual: "error"}
		if err == nil {
			result.Actual = projection.Decision.Mode
			result.ActualSessions = len(projection.NextPlan.Sessions)
			result.ActualMinutes = projection.NextPlan.TotalMinutes
			result.Passed = result.Actual == result.Expected && result.ActualSessions == result.ExpectedSessions && result.ActualMinutes == result.ExpectedMinutes
		}
		if result.Passed {
			r.PassedCases++
		}
		if result.Critical {
			r.CriticalTotal++
			if result.Passed {
				r.CriticalPassed++
			}
		}
		r.Cases = append(r.Cases, result)
	}
	for i := 0; i < 200; i++ {
		tc := cases[i%len(cases)]
		if _, err := rhythm.Preview(tc.Profile, tc.Entries, fixtureNow); err != nil {
			fmt.Fprintln(os.Stderr, "warmup failed:", err)
			os.Exit(1)
		}
	}
	samples := make([]time.Duration, *iterations)
	for i := range samples {
		tc := cases[i%len(cases)]
		start := time.Now()
		_, err := rhythm.Preview(tc.Profile, tc.Entries, fixtureNow)
		samples[i] = time.Since(start)
		if err != nil {
			r.ErrorCalls++
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	r.P50MS = ms(samples[(*iterations*50+99)/100-1])
	r.P95MS = ms(samples[(*iterations*95+99)/100-1])
	r.MaxMS = ms(samples[len(samples)-1])
	r.Passed = float64(r.PassedCases)/float64(r.TotalCases) >= r.Thresholds.SuccessRatio && r.CriticalPassed == r.CriticalTotal && r.P95MS < r.Thresholds.P95MS && r.ErrorCalls == 0
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, "write report:", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("Ритм · локальная синтетическая RAT-проверка\nКейсы: %d/%d · критичные: %d/%d · вызовы: %d · ошибки: %d\nP50 %.6f ms · P95 %.6f ms · max %.6f ms\nПороги: ≥90%% кейсов, 100%% критичных, P95 <50 ms\nРезультат: %t\nОбласть: %s\n", r.PassedCases, r.TotalCases, r.CriticalPassed, r.CriticalTotal, r.Iterations, r.ErrorCalls, r.P50MS, r.P95MS, r.MaxMS, r.Passed, r.Scope)
	}
	if !r.Passed {
		os.Exit(1)
	}
}
