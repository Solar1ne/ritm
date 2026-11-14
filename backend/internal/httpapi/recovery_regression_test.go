package httpapi

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func TestHTTPRecoveryRespectsSavedWeekThroughAcceptanceAndRestore(t *testing.T) {
	for _, tc := range []struct {
		name         string
		shorten      bool
		before, next int
	}{
		{name: "shortened session variants", shorten: true, before: 45, next: 30},
		{name: "increased future profile", before: 15, next: 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setup(t, "")
			now := testNow().AddDate(0, 0, -5) // Monday: all scheduled sessions are editable.
			s.clock = func() time.Time { return now }
			cookie := guest(t, s)
			profile := testProfile()
			if !tc.shorten {
				profile.Days, profile.Minutes = []int{0}, 15
			}
			st := requireState(t, request(t, s, http.MethodPost, "/api/profile", map[string]any{"revision": 0, "profile": profile}, cookie), 200)
			if tc.shorten {
				for _, session := range st.Plan.Sessions {
					st = requireState(t, request(t, s, http.MethodPost, "/api/sessions/variant", variantHTTPBody(st.Revision, session.ID, "home-mobility-v1", "home", 15, true), cookie), 200)
				}
			} else {
				profile.Days, profile.Minutes = []int{0, 1, 2, 3, 4}, 90
				st = requireState(t, request(t, s, http.MethodPost, "/api/profile", map[string]any{"revision": st.Revision, "profile": profile}, cookie), 200)
			}
			fatigue := 9
			entry := rhythm.Entry{ID: "recovery-regression", Date: now.Format(time.DateOnly), Status: "wellbeing", Fatigue: &fatigue}
			st = requireState(t, request(t, s, http.MethodPost, "/api/entries", map[string]any{"revision": st.Revision, "entry": entry}, cookie), 200)
			if st.Decision.Mode != "recover" || st.Plan.TotalMinutes != tc.before || st.NextPlan.TotalMinutes != tc.next {
				t.Fatalf("recovery: current=%d next=%d mode=%s; want current=%d next=%d", st.Plan.TotalMinutes, st.NextPlan.TotalMinutes, st.Decision.Mode, tc.before, tc.next)
			}
			savedWeeks := st.Weeks
			st = requireState(t, request(t, s, http.MethodPost, "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.NextPlan.WeekStart, "nextPlanToken": st.NextPlanToken}, cookie), 200)
			if st.AcceptedNextPlan == nil || st.AcceptedNextPlan.TotalMinutes != tc.next || !reflect.DeepEqual(savedWeeks[0], st.Weeks[0]) {
				t.Fatal("acceptance lost the recovery proposal or changed the current snapshot")
			}
			exported := requireState(t, request(t, s, http.MethodGet, "/api/export", nil, cookie), 200)
			target := guest(t, s)
			restored := requireState(t, request(t, s, http.MethodPost, "/api/restore", map[string]any{"revision": 0, "backup": exported}, target), 200)
			if restored.AcceptedNextPlan == nil || restored.AcceptedNextPlan.TotalMinutes != tc.next {
				t.Fatal("restore lost the accepted recovery plan")
			}
			now = now.AddDate(0, 0, 7)
			for _, account := range []*http.Cookie{cookie, target} {
				promoted := requireState(t, request(t, s, http.MethodGet, "/api/state", nil, account), 200)
				if promoted.Plan.TotalMinutes != tc.next || promoted.AcceptedNextPlan != nil {
					t.Fatal("Monday rollover did not preserve the accepted recovery plan")
				}
			}
		})
	}
}
