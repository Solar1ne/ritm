// Package rat defines the fixed synthetic acceptance set shared by tests and
// the local RAT runner. These cases are engineering checks, not a user trial.
package rat

import (
	"fmt"
	"time"

	"rhythm/internal/rhythm"
)

type Case struct {
	Name     string
	Critical bool
	Profile  rhythm.Profile
	Entries  []rhythm.Entry
	Mode     string
	Sessions int
	Minutes  int
}

func floatPtr(n float64) *float64 { return &n }
func intPtr(n int) *int           { return &n }

// Cases creates a frozen 16-case rule set, aligned to a caller-supplied week.
func Cases(now time.Time) []Case {
	base := rhythm.Profile{Name: "Синтетический профиль", Goal: "tone", Experience: "beginner", Days: []int{0, 2, 4}, Minutes: 45}
	start := rhythm.WeekStart(now)
	logs := func(done, missed int, sleep *float64, fatigue *int) []rhythm.Entry {
		out := []rhythm.Entry{}
		for i := 0; i < done+missed; i++ {
			date := start.AddDate(0, 0, base.Days[i]).Format(time.DateOnly)
			status := "done"
			if i >= done {
				status = "missed"
			}
			out = append(out, rhythm.Entry{ID: fmt.Sprintf("session-%d", i), Date: date, SessionID: date, Status: status})
		}
		if sleep != nil || fatigue != nil {
			out = append(out, rhythm.Entry{ID: "wellbeing", Date: start.AddDate(0, 0, 5).Format(time.DateOnly), Status: "wellbeing", Sleep: sleep, Fatigue: fatigue})
		}
		return out
	}
	return []Case{
		{"01_no_history", false, base, nil, "start", 3, 135},
		{"02_one_completion", false, base, logs(1, 0, nil, nil), "maintain", 3, 135},
		{"03_fatigue_at_threshold", false, base, logs(0, 0, nil, intPtr(8)), "recover", 2, 72},
		{"04_sleep_below_threshold", false, base, logs(0, 0, floatPtr(5.5), nil), "recover", 2, 72},
		{"05_sleep_at_threshold", false, base, logs(0, 0, floatPtr(6), intPtr(7)), "maintain", 3, 135},
		{"06_two_missed", false, base, logs(0, 2, nil, nil), "simplify", 2, 90},
		{"07_one_missed", false, base, logs(0, 1, nil, nil), "maintain", 3, 135},
		{"08_complete_and_recovered", false, base, logs(3, 0, floatPtr(7), intPtr(4)), "progress", 3, 141},
		{"09_complete_missing_wellbeing", false, base, logs(3, 0, nil, nil), "maintain", 3, 135},
		{"10_complete_sleep_unknown", false, base, logs(3, 0, nil, intPtr(3)), "maintain", 3, 135},
		{"11_complete_fatigue_unknown", false, base, logs(3, 0, floatPtr(8), nil), "maintain", 3, 135},
		{"12_recovery_over_completion", true, base, logs(3, 0, floatPtr(8), intPtr(9)), "recover", 2, 72},
		{"13_sleep_over_completion", true, base, logs(3, 0, floatPtr(5), intPtr(2)), "recover", 2, 72},
		{"14_recovery_over_missed", true, base, logs(0, 2, floatPtr(8), intPtr(8)), "recover", 2, 72},
		{"15_sleep_over_missed", true, base, logs(1, 2, floatPtr(4), intPtr(2)), "recover", 2, 72},
		{"16_both_risks_over_completion", true, base, logs(3, 0, floatPtr(5), intPtr(10)), "recover", 2, 72},
	}
}
