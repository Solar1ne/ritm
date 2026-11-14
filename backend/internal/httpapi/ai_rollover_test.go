package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"rhythm/internal/aicoach"
	"rhythm/internal/rhythm"
)

func TestAIContextMaterializesMondayBeforeConsent(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted_%t", accepted), func(t *testing.T) {
			s, _ := setup(t, "")
			s.location = time.UTC
			now := time.Date(2026, 10, 18, 23, 59, 0, 0, time.UTC)
			s.clock = func() time.Time { return now }
			calls := 0
			s.ai = aiGeneratorFunc(func(_ context.Context, req aicoach.Request) (aicoach.Result, error) {
				calls++
				result := aiValidResult(req)
				sleep := 7.0
				result.Draft = &aicoach.Draft{Date: req.Context.Today, Status: "wellbeing", Sleep: &sleep}
				return result, nil
			})
			cookie := guest(t, s)
			before := saveProfile(t, s, cookie, 0)
			if accepted {
				before = requireState(t, request(t, s, "POST", "/api/plan/accept", map[string]any{
					"revision": before.Revision, "weekStart": before.NextPlan.WeekStart, "nextPlanToken": before.NextPlanToken,
				}, cookie), 200)
			}
			now = now.Add(time.Minute)
			// Do not bootstrap /api/state here: context must advance the calendar
			// itself before presenting the facts for consent and a possible draft.
			view := aiView(t, s, cookie, aicoach.TaskCheckin)
			monday := now.Format(time.DateOnly)
			if view.Context.Today != monday {
				t.Fatalf("wrong consent date: %s", view.Context.Today)
			}
			found := false
			for _, day := range view.Context.Days {
				if day.Date == monday {
					found = true
					if day.SessionID != monday {
						t.Fatalf("Monday's saved session is absent: %+v", day)
					}
				}
			}
			if !found {
				t.Fatal("new Monday cannot be drafted without an unrelated state read")
			}
			wantRevision := before.Revision
			if !accepted {
				wantRevision++
			}
			if view.Revision != wantRevision {
				t.Fatalf("calendar materialization revision=%d, want %d", view.Revision, wantRevision)
			}
			again := aiView(t, s, cookie, aicoach.TaskCheckin)
			if again.Revision != view.Revision || calls != 0 {
				t.Fatal("repeated context read changed revision or called the provider")
			}
			after := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
			if after.Revision != view.Revision || after.Plan.WeekStart != monday || len(after.Entries) != 0 {
				t.Fatal("context did not persist the calendar atomically, or invented diary facts")
			}
			w := request(t, s, "POST", "/api/ai/respond", aiBody(view, aicoach.TaskCheckin, "Сегодня спал 7 часов."), cookie)
			if w.Code != 200 || calls != 1 {
				t.Fatalf("Monday consent did not authorize a valid draft: %d %s", w.Code, w.Body.String())
			}
			final := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
			if final.Revision != view.Revision || len(final.Entries) != 0 {
				t.Fatal("AI draft saved a fact without separate confirmation")
			}
		})
	}
}

func TestAIContextKeepsUnknownDurationAndDistanceExplicit(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, unknownSessions := range []int{1, 3} {
		for _, knownDistances := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("unknown_sessions_%d_known_distances_%d", unknownSessions, knownDistances), func(t *testing.T) {
				profile := rhythm.Profile{Goal: "tone", Experience: "regular", Days: []int{0, 2, 4}, Minutes: 45}
				legacy := rhythm.Record{Profile: &profile}
				for i := range unknownSessions {
					date := rhythm.WeekStart(now).AddDate(0, 0, i*2).Format(time.DateOnly)
					legacy.Entries = append(legacy.Entries, rhythm.Entry{ID: fmt.Sprintf("legacy%d", i), Date: date, SessionID: date, Status: "done"})
				}
				record, err := rhythm.EnsureWeeks(legacy, now)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					workout := rhythm.Workout{ID: fmt.Sprintf("workout%d", i), Date: now.Format(time.DateOnly), StartedAt: now.Format(time.RFC3339), Kind: "walk", Source: "csv", Minutes: 30}
					if i < knownDistances {
						distance := 2000.0
						workout.DistanceMeters = &distance
					}
					record.Workouts = append(record.Workouts, workout)
				}
				value := buildAIContext(record, aicoach.TaskReview, now)
				if err := aicoach.ValidateRequest(aicoach.Request{Task: aicoach.TaskReview, Context: value}); err != nil {
					t.Fatalf("context no longer fits the provider protocol: %v", err)
				}
				var planFact, decisionFact, importFact string
				unknownFacts := 0
				for _, fact := range value.Facts {
					switch {
					case strings.HasPrefix(fact.Text, "Текущий план: неделя"):
						planFact = fact.Text
					case strings.HasPrefix(fact.Text, "Правила адаптации:"):
						decisionFact = fact.Text
					case strings.HasPrefix(fact.Text, "Импорт за 14 дней:"):
						importFact = fact.Text
					case strings.HasPrefix(fact.Text, "Текущий план:") && strings.Contains(fact.Text, "длительность и вид занятия неизвестны"):
						unknownFacts++
					}
				}
				knownMinutes := (3 - unknownSessions) * 45
				if !strings.Contains(planFact, fmt.Sprintf("сумма известных плановых минут %d", knownMinutes)) || !strings.Contains(planFact, fmt.Sprintf("Неизвестна длительность %d занятий", unknownSessions)) || unknownFacts != unknownSessions {
					t.Fatalf("legacy duration presented as complete: %q, unknown facts=%d", planFact, unknownFacts)
				}
				if !strings.Contains(decisionFact, "известных плановых минут") {
					t.Fatalf("decision repeats a partial total as a complete original load: %q", decisionFact)
				}
				if knownDistances == 0 {
					if !strings.Contains(importFact, "дистанция не указана") || strings.Contains(importFact, "0 метров") {
						t.Fatalf("missing distances became measured zero: %q", importFact)
					}
				} else if !strings.Contains(importFact, fmt.Sprintf("%d метров известной дистанции у %d из 2 активностей", knownDistances*2000, knownDistances)) {
					t.Fatalf("distance coverage is not explicit: %q", importFact)
				}
			})
		}
	}
}
