package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"rhythm/internal/assistant"
	"rhythm/internal/rhythm"
	"rhythm/internal/workoutimport"
)

const tcxImportFixture = `<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"><Activities><Activity Sport="Running"><Id>2026-10-16T08:00:00.123456789Z</Id><Lap StartTime="2026-10-16T08:00:00.123456789Z"><TotalTimeSeconds>1800</TotalTimeSeconds><DistanceMeters>4000</DistanceMeters></Lap></Activity></Activities></TrainingCenterDatabase>`

func TestHTTPImportPreviewNormalizeConsentAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name, format, content, kind, started string
	}{
		{"running", "csv", "id,date,kind,minutes,distance\nrun-1,2026-10-15T23:30:00.123456789Z,running,30,4000\n", "run", "2026-10-15T23:30:00.123456789Z"},
		{"walking", "csv", "id,date,kind,minutes,distance\nwalk-1,2026-10-16,walking,30,2000\n", "walk", "2026-10-15T21:00:00Z"},
		{"tcx_running", "tcx", tcxImportFixture, "run", "2026-10-16T08:00:00.123456789Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setup(t, "")
			cookie := guest(t, s)
			initial := saveProfile(t, s, cookie, 0)
			body := map[string]any{"format": tc.format, "content": tc.content}
			preview := request(t, s, "POST", "/api/import/preview", body, cookie)
			if preview.Code != 200 {
				t.Fatalf("preview=%d %s", preview.Code, preview.Body.String())
			}
			var inspected struct {
				Workouts []importItem `json:"workouts"`
				Revision uint64       `json:"revision"`
			}
			if err := json.Unmarshal(preview.Body.Bytes(), &inspected); err != nil {
				t.Fatal(err)
			}
			if inspected.Revision != initial.Revision || len(inspected.Workouts) != 1 {
				t.Fatal("preview omitted its snapshot or activity")
			}
			item := inspected.Workouts[0]
			if item.Kind != tc.kind || item.Date != "2026-10-16" || item.StartedAt != tc.started || item.Duplicate || !item.CanComplete {
				t.Fatalf("normalization or schedule availability: %+v", item)
			}
			unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
			if !reflect.DeepEqual(initial, unchanged) {
				t.Fatal("preview persisted changes")
			}
			body["revision"] = initial.Revision
			saved := requireState(t, request(t, s, "POST", "/api/import", body, cookie), 200)
			if len(saved.Workouts) != 1 || len(saved.Entries) != 0 || saved.Revision != initial.Revision+1 || saved.Workouts[0].SessionID != "" {
				t.Fatal("import completed a plan without explicit consent")
			}
			retried := requireState(t, request(t, s, "POST", "/api/import", body, cookie), 200)
			if !reflect.DeepEqual(saved, retried) {
				t.Fatal("repeat of accepted file changed data")
			}
			body["revision"], body["completePlanned"] = saved.Revision, true
			linked := requireState(t, request(t, s, "POST", "/api/import", body, cookie), 200)
			if len(linked.Workouts) != 1 || len(linked.Entries) != 1 || linked.Workouts[0].SessionID != "2026-10-16" || linked.Plan.Sessions[2].Status != "done" || linked.Revision != saved.Revision+1 {
				t.Fatal("explicit completion failed or duplicated the imported activity")
			}
			retry := requireState(t, request(t, s, "POST", "/api/import", body, cookie), 200)
			if !reflect.DeepEqual(retry, linked) {
				t.Fatal("completion retry changed state")
			}
		})
	}
}

func TestHTTPImportAtomicConflictAndFractionalIdentity(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	initial := saveProfile(t, s, cookie, 0)
	content := "id,date,kind,minutes\nexisting,2026-10-16T08:00:00.100Z,running,30\n"
	saved := requireState(t, request(t, s, "POST", "/api/import", map[string]any{"revision": initial.Revision, "format": "csv", "content": content}, cookie), 200)
	for _, tc := range []struct {
		name, content string
		status        int
	}{
		{"conflict_after_new_row", "id,date,kind,minutes\nnew,2026-10-14,walking,20\nexisting,2026-10-16T08:00:00.100Z,running,31\n", 409},
		{"fractional_timestamp_conflict", strings.Replace(content, ".100Z", ".900Z", 1), 409},
		{"future_after_new_row", "id,date,kind,minutes\nnew,2026-10-14,walking,20\nfuture,2026-10-18,running,30\n", 400},
		{"invalid_date_after_new_row", "id,date,kind,minutes\nnew,2026-10-14,walking,20\ninvalid,2026-02-31,running,30\n", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireState(t, request(t, s, "POST", "/api/import", map[string]any{"revision": saved.Revision, "format": "csv", "content": tc.content, "completePlanned": true}, cookie), tc.status)
			if current := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(current, saved) {
				t.Fatal("invalid import partially committed a workout or diary fact")
			}
		})
	}
	other := guest(t, s)
	otherState := saveProfile(t, s, other, 0)
	otherState = requireState(t, request(t, s, "POST", "/api/import", map[string]any{"revision": otherState.Revision, "format": "csv", "content": content}, other), 200)
	if len(otherState.Workouts) != 1 || otherState.SessionBinding == saved.SessionBinding {
		t.Fatal("import identities were not scoped to the current account")
	}
	if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, saved) {
		t.Fatal("another account's import changed its source")
	}
}

func TestHTTPImportExactFileAndRecordLimits(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	initial := saveProfile(t, s, cookie, 0)
	for _, extra := range []int{0, 1} {
		padding := strings.Repeat("x", workoutimport.MaxBytes-len(tcxImportFixture)-len("<!---->")+extra)
		content := "<!--" + padding + "-->" + tcxImportFixture
		status := 200
		if extra == 1 {
			status = 400
		}
		if w := request(t, s, "POST", "/api/import/preview", map[string]any{"format": "tcx", "content": content}, cookie); w.Code != status {
			t.Fatalf("file bytes=%d status=%d want=%d body=%s", len(content), w.Code, status, w.Body.String())
		}
	}
	for _, count := range []int{100, 101} {
		var content strings.Builder
		content.WriteString("id,date,kind,minutes\n")
		for i := range count {
			fmt.Fprintf(&content, "id-%d,2026-10-16,walking,30\n", i)
		}
		want := 200
		if count > workoutimport.MaxRecords {
			want = 400
		}
		if w := request(t, s, "POST", "/api/import/preview", map[string]any{"format": "csv", "content": content.String()}, cookie); w.Code != want {
			t.Fatalf("rows=%d status=%d want=%d body=%s", count, w.Code, want, w.Body.String())
		}
	}
	requireState(t, request(t, s, "POST", "/api/import", map[string]any{"format": "tcx", "content": tcxImportFixture}, cookie), 400)
	requireState(t, request(t, s, "POST", "/api/import/preview", map[string]any{"format": "fit", "content": "unsupported"}, cookie), 400)
	requireState(t, request(t, s, "POST", "/api/import/preview", map[string]any{"format": "tcx", "content": tcxImportFixture, "url": "https://example.invalid/private"}, cookie), 400)
	requireState(t, request(t, s, "POST", "/api/import/preview", map[string]any{"format": "csv", "content": strings.Repeat("x", 2*1024*1024)}, cookie), 400)
	malformed := `<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"><` + strings.Repeat("private_tag", 200) + `></mismatch></TrainingCenterDatabase>`
	w := request(t, s, "POST", "/api/import/preview", map[string]any{"format": "tcx", "content": malformed}, cookie)
	if w.Code != 400 || w.Body.Len() > 1000 || strings.Contains(w.Body.String(), "private_tag") {
		t.Fatal("malformed XML should have a bounded error without reflecting file contents")
	}
	if current := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(current, initial) {
		t.Fatal("validation/preview tests persisted input")
	}
}

func TestHTTPExtrasRequireCurrentIdentityBinding(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := saveProfile(t, s, cookie, 0)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/import/preview", map[string]any{"format": "tcx", "content": tcxImportFixture}},
		{"POST", "/api/import", map[string]any{"revision": st.Revision, "format": "tcx", "content": tcxImportFixture}},
		{"DELETE", "/api/workouts", map[string]any{"revision": st.Revision, "id": strings.Repeat("a", 64)}},
		{"POST", "/api/reminders", map[string]any{"revision": st.Revision, "reminder": rhythm.ReminderSettings{Enabled: true, Time: "18:00", BeforeMinutes: 15}}},
		{"GET", "/api/calendar", nil},
		{"POST", "/api/assistant", map[string]any{"question": "Почему изменился план?"}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			requireState(t, request(t, s, tc.method, tc.path, tc.body, nil), 401)
			for _, binding := range []string{"", strings.Repeat("0", 64)} {
				data, err := json.Marshal(tc.body)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(tc.method, testOrigin+tc.path, bytes.NewReader(data))
				r.AddCookie(cookie)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", testOrigin)
				r.Header.Set(sessionBindingHeader, binding)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != 409 || !strings.Contains(w.Body.String(), "session_changed") {
					t.Fatalf("missing/stale binding reached %s: %d %s", tc.path, w.Code, w.Body.String())
				}
				if tc.method != "GET" {
					r = httptest.NewRequest(tc.method, testOrigin+tc.path, bytes.NewReader(data))
					r.AddCookie(cookie)
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Origin", "https://other.example")
					r.Header.Set(sessionBindingHeader, st.SessionBinding)
					w = httptest.NewRecorder()
					s.ServeHTTP(w, r)
					if w.Code != 403 {
						t.Fatalf("cross-origin mutation reached %s: %d", tc.path, w.Code)
					}
				}
			}
		})
	}
	if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, st) {
		t.Fatal("unauthorized requests changed account state")
	}
}

func calendarUIDs(text string) []string {
	var ids []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n ", ""), "\r\n") {
		if strings.HasPrefix(line, "UID:") {
			ids = append(ids, line)
		}
	}
	return ids
}

func TestHTTPCalendarConsentTimezoneAndStableIdentity(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := saveProfile(t, s, cookie, 0)
	before := request(t, s, "GET", "/api/calendar", nil, cookie)
	if before.Code != 200 || !strings.HasPrefix(before.Header().Get("Content-Type"), "text/calendar") || before.Header().Get("Cache-Control") != "no-store" || !strings.Contains(before.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("calendar response: %d %s", before.Code, before.Body.String())
	}
	if strings.Contains(before.Body.String(), "VALARM") || !strings.Contains(before.Body.String(), "DTSTART:20261012T150000Z") || len(calendarUIDs(before.Body.String())) != 3 {
		t.Fatal("calendar should default to 18:00 Moscow without an alarm")
	}
	value := rhythm.ReminderSettings{Enabled: true, Time: "19:00", BeforeMinutes: 15}
	st = requireState(t, request(t, s, "POST", "/api/reminders", map[string]any{"revision": st.Revision, "reminder": value}, cookie), 200)
	after := request(t, s, "GET", "/api/calendar", nil, cookie)
	if after.Code != 200 || strings.Count(after.Body.String(), "BEGIN:VALARM") != 3 || strings.Count(after.Body.String(), "TRIGGER:-PT15M") != 3 || !strings.Contains(after.Body.String(), "DTSTART:20261012T160000Z") {
		t.Fatalf("consented calendar alarms/time: %s", after.Body.String())
	}
	if !reflect.DeepEqual(calendarUIDs(before.Body.String()), calendarUIDs(after.Body.String())) {
		t.Fatal("changing reminder time changed stable event identities")
	}
	value.Enabled = false
	st = requireState(t, request(t, s, "POST", "/api/reminders", map[string]any{"revision": st.Revision, "reminder": value}, cookie), 200)
	if disabled := request(t, s, "GET", "/api/calendar", nil, cookie); disabled.Code != 200 || strings.Contains(disabled.Body.String(), "VALARM") {
		t.Fatal("newly exported calendar retained a revoked alarm")
	}
	if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); unchanged.Revision != st.Revision {
		t.Fatal("calendar read changed revision")
	}
	other := guest(t, s)
	saveProfile(t, s, other, 0)
	otherCalendar := request(t, s, "GET", "/api/calendar", nil, other)
	if reflect.DeepEqual(calendarUIDs(after.Body.String()), calendarUIDs(otherCalendar.Body.String())) {
		t.Fatal("different accounts shared event identities")
	}
}

func TestHTTPBackupRoundTripsWorkoutsAndReminder(t *testing.T) {
	s, _ := setup(t, "")
	cookie := guest(t, s)
	st := saveProfile(t, s, cookie, 0)
	st = requireState(t, request(t, s, "POST", "/api/import", map[string]any{"revision": st.Revision, "format": "tcx", "content": tcxImportFixture, "completePlanned": true}, cookie), 200)
	st = requireState(t, request(t, s, "POST", "/api/reminders", map[string]any{"revision": st.Revision, "reminder": rhythm.ReminderSettings{Enabled: true, Time: "00:30", BeforeMinutes: 60}}, cookie), 200)
	exported := requireState(t, request(t, s, "GET", "/api/export", nil, cookie), 200)
	target := guest(t, s)
	restored := requireState(t, request(t, s, "POST", "/api/restore", map[string]any{"revision": 0, "backup": exported}, target), 200)
	if !reflect.DeepEqual(restored.Workouts, st.Workouts) || !reflect.DeepEqual(restored.Entries, st.Entries) || !reflect.DeepEqual(restored.Weeks, st.Weeks) || restored.Reminder != st.Reminder || restored.SessionBinding == st.SessionBinding || restored.Revision != 1 {
		t.Fatal("portable data or destination identity changed during restoration")
	}
}

func TestHTTPAssistantUsesAcceptedPlanAndUnknownHistory(t *testing.T) {
	t.Run("all_visible_question_presets_are_supported", func(t *testing.T) {
		s, _ := setup(t, "")
		cookie := guest(t, s)
		st := saveProfile(t, s, cookie, 0)
		for _, tc := range []struct{ question, intent string }{
			{"Почему изменился план?", "why"},
			{"Что у меня сегодня?", "today"},
			{"Какой у меня прогресс?", "progress"},
			{"Что с восстановлением?", "recovery"},
		} {
			w := request(t, s, "POST", "/api/assistant", map[string]any{"question": tc.question}, cookie)
			if w.Code != 200 {
				t.Fatalf("preset %q: %d %s", tc.question, w.Code, w.Body.String())
			}
			var answer assistant.Response
			if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
				t.Fatal(err)
			}
			if answer.Mode != "rules" || answer.Intent != tc.intent {
				t.Errorf("visible preset %q: intent=%q want=%q", tc.question, answer.Intent, tc.intent)
			}
		}
		if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, st) {
			t.Fatal("question presets changed account state")
		}
	})
	t.Run("accepted_plan_is_distinct_from_new_proposal", func(t *testing.T) {
		s, _ := setup(t, "")
		cookie := guest(t, s)
		st := saveProfile(t, s, cookie, 0)
		st = requireState(t, request(t, s, "POST", "/api/plan/accept", map[string]any{"revision": st.Revision, "weekStart": st.NextPlan.WeekStart, "nextPlanToken": st.NextPlanToken}, cookie), 200)
		fatigue := 9
		st = requireState(t, request(t, s, "POST", "/api/entries", map[string]any{"revision": st.Revision, "entry": rhythm.Entry{ID: "changed-signal", Date: "2026-10-17", Status: "wellbeing", Fatigue: &fatigue}}, cookie), 200)
		w := request(t, s, "POST", "/api/assistant", map[string]any{"question": "Какой план на следующую неделю?"}, cookie)
		if w.Code != 200 {
			t.Fatalf("assistant=%d %s", w.Code, w.Body.String())
		}
		var answer assistant.Response
		if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		accepted, proposed := 0, 0
		for _, evidence := range answer.Evidence {
			switch evidence.Source {
			case "acceptedNextPlan":
				accepted++
			case "nextPlan":
				proposed++
			}
		}
		if answer.Mode != "rules" || answer.Intent != "next" || accepted != 3 || proposed != 2 || !strings.Contains(answer.Text, "135") || !strings.Contains(answer.Text, "72") {
			t.Fatalf("accepted and proposed schedules were conflated: %+v", answer)
		}
		if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, st) {
			t.Fatal("read-only assistant changed the account")
		}
	})
	t.Run("legacy_minutes_remain_unknown", func(t *testing.T) {
		s, _ := setup(t, "")
		cookie := guest(t, s)
		req := httptest.NewRequest("GET", testOrigin+"/api/state", nil)
		req.AddCookie(cookie)
		id, _, err := s.principal(req, false)
		if err != nil {
			t.Fatal(err)
		}
		profile := testProfile()
		_, err = s.repo.Update(t.Context(), id, func(current rhythm.Record) (rhythm.Record, error) {
			return rhythm.Record{Revision: current.Revision + 1, Profile: &profile, Entries: []rhythm.Entry{{ID: "legacy-done", Date: "2026-10-16", SessionID: "2026-10-16", Status: "done"}}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		migrated := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200)
		w := request(t, s, "POST", "/api/assistant", map[string]any{"question": "Сколько занятий выполнено?"}, cookie)
		if w.Code != 200 {
			t.Fatalf("valid migrated history rejected: %d %s", w.Code, w.Body.String())
		}
		var answer assistant.Response
		if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Mode != "rules" || answer.Intent != "progress" || !strings.Contains(answer.Text, "неизвест") {
			t.Fatalf("assistant invented a historic duration: %+v", answer)
		}
		if unchanged := requireState(t, request(t, s, "GET", "/api/state", nil, cookie), 200); !reflect.DeepEqual(unchanged, migrated) {
			t.Fatal("assistant mutated migrated data")
		}
	})
}
