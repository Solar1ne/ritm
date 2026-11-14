package assistant

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) Context {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return Context{
		Now: time.Date(2026, 10, 17, 12, 0, 0, 0, loc),
		Plan: &Plan{WeekStart: "2026-10-12", Sessions: []Session{
			{ID: "2026-10-17", Date: "2026-10-17", Title: "Прогулка", Status: "planned", Minutes: 30, DetailsKnown: true},
			{ID: "2026-10-12", Date: "2026-10-12", Title: "Силовая", Status: "done", Minutes: 45, DetailsKnown: true},
			{ID: "2026-10-14", Date: "2026-10-14", Title: "Пробежка", Status: "missed", Minutes: 45, DetailsKnown: true},
		}},
		NextPlan: &Plan{WeekStart: "2026-10-19", Sessions: []Session{
			{ID: "2026-10-19", Date: "2026-10-19", Title: "Лёгкое занятие", Status: "planned", Minutes: 36, DetailsKnown: true},
			{ID: "2026-10-21", Date: "2026-10-21", Title: "Прогулка", Status: "planned", Minutes: 36, DetailsKnown: true},
		}},
		Decision: &Decision{Mode: "recover", Title: "Больше восстановления", Reasons: []string{"Усталость 9 из 10: нагрузка снижена."}, BeforeMinutes: 120, AfterMinutes: 72},
		Entries: []Entry{
			{ID: "old", Date: "2026-10-11", Sleep: ptr(1.0), Fatigue: ptr(10)},
			{ID: "sleep", Date: "2026-10-13", Sleep: ptr(6.0)},
			{ID: "fatigue", Date: "2026-10-14", Fatigue: ptr(9)},
			{ID: "partial", Date: "2026-10-15", Status: "done"},
			{ID: "future", Date: "2026-10-18", Sleep: ptr(2.0), Fatigue: ptr(10)},
		},
	}
}

func ptr[T any](value T) *T { return &value }

func TestUIPresetQuestions(t *testing.T) {
	// Exact preset strings from web/static/features.js are part of the API's
	// user-visible contract, including the optional personal subject.
	tests := []struct {
		question string
		intent   string
		fact     string
	}{
		{"Почему изменился план?", "why", "Усталость 9 из 10"},
		{"Что у меня сегодня?", "today", "2026-10-17 — Прогулка, 30 мин"},
		{"Какой у меня прогресс?", "progress", "выполнено 1 из 3"},
		{"Что с восстановлением?", "recovery", "сон — 6 ч (2026-10-13)"},
		{"  ЧТО   У МЕНЯ   СЕГОДНЯ?!  ", "today", "2026-10-17 — Прогулка, 30 мин"},
	}
	for _, tc := range tests {
		t.Run(tc.question, func(t *testing.T) {
			got, err := Answer(tc.question, fixture(t))
			if err != nil || got.Mode != "rules" || got.Intent != tc.intent || !strings.Contains(got.Text, tc.fact) || len(got.Evidence) == 0 {
				t.Fatalf("preset did not explain stored facts: %#v %v", got, err)
			}
		})
	}
}

func TestAnswersAreGroundedReadOnlyAndDeterministic(t *testing.T) {
	context := fixture(t)
	before, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		question string
		intent   string
		facts    []string
		source   string
	}{
		{"Почему изменился план?", "why", []string{"Больше восстановления", "Усталость 9 из 10", "не применяется автоматически"}, "decision"},
		{"Почему на следующей неделе больше занятий?", "why", []string{"Усталость 9 из 10"}, "decision"},
		{"Что сегодня?", "today", []string{"2026-10-17", "Прогулка", "30 мин", "запланировано"}, "plan"},
		{"Какой прогресс?", "progress", []string{"выполнено 1 из 3", "пропущено 1", "45 мин", "не измерение фактического времени"}, "plan"},
		{"Что с восстановлением?", "recovery", []string{"сон — 6 ч (2026-10-13)", "усталость — 9/10 (2026-10-14)"}, "diary"},
		{"Ближайшее занятие", "next", []string{"2026-10-17", "Прогулка"}, "plan"},
		{"Сколько занятий на следующей неделе?", "next", []string{"предложение", "2026-10-19", "2 занятий", "72 известных"}, "nextPlan"},
	}
	for _, tc := range tests {
		t.Run(tc.question, func(t *testing.T) {
			answer, err := Answer(tc.question, context)
			if err != nil {
				t.Fatal(err)
			}
			if answer.Mode != "rules" || answer.Intent != tc.intent || len(answer.Evidence) == 0 || answer.Evidence[0].Source != tc.source {
				t.Fatalf("wrong mode/intent/evidence: %#v", answer)
			}
			for _, fact := range tc.facts {
				if !strings.Contains(answer.Text, fact) {
					t.Errorf("missing fact %q in %q", fact, answer.Text)
				}
			}
			again, err := Answer(tc.question, context)
			if err != nil {
				t.Fatal(err)
			}
			a, err := json.Marshal(answer)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(again)
			if err != nil || !bytes.Equal(a, b) {
				t.Fatal("same context/question gave different answer")
			}
		})
	}
	after, err := json.Marshal(context)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("assistant changed caller-owned data")
	}
}

func TestAcceptedScheduleIsSeparateFromNewProposal(t *testing.T) {
	context := fixture(t)
	context.Now = context.Now.AddDate(0, 0, 1) // Sunday; tomorrow is next week.
	context.AcceptedNextPlan = &Plan{WeekStart: "2026-10-19", Sessions: []Session{
		{ID: "2026-10-19", Date: "2026-10-19", Title: "Принятая силовая", Status: "planned", Minutes: 45, DetailsKnown: true},
		{ID: "2026-10-21", Date: "2026-10-21", Title: "Принятая прогулка", Status: "planned", Minutes: 45, DetailsKnown: true},
		{ID: "2026-10-23", Date: "2026-10-23", Title: "Принятое занятие", Status: "planned", Minutes: 45, DetailsKnown: true},
	}}
	for _, question := range []string{"Что завтра?", "Ближайшее занятие"} {
		got, err := Answer(question, context)
		if err != nil || !strings.Contains(got.Text, "Принятая силовая") || !strings.Contains(got.Text, "45 мин") || !strings.Contains(got.Text, "принятое расписание") || got.Evidence[0].Source != "acceptedNextPlan" {
			t.Fatalf("accepted schedule lost: %#v %v", got, err)
		}
		if strings.Contains(got.Text, "36 мин") || strings.Contains(got.Text, "Лёгкое занятие") {
			t.Fatal("unaccepted proposal presented as tomorrow's schedule")
		}
	}
	week, err := Answer("Что на следующей неделе?", context)
	if err != nil || !strings.Contains(week.Text, "Принят план") || !strings.Contains(week.Text, "135 известных") || !strings.Contains(week.Text, "Отдельное текущее предложение") || !strings.Contains(week.Text, "72 известных") {
		t.Fatalf("accepted/proposal not distinguished: %#v %v", week, err)
	}
	why, err := Answer("Почему план изменился?", context)
	if err != nil || !strings.Contains(why.Text, "Уже принят план") || !strings.Contains(why.Text, "Новое предложение его не заменяет") {
		t.Fatalf("why rewrites accepted schedule: %#v %v", why, err)
	}
	context.AcceptedNextPlan = nil
	tomorrow, err := Answer("Что завтра?", context)
	if err != nil || !strings.Contains(tomorrow.Text, "нет предстоящего занятия") || strings.Contains(tomorrow.Text, "Лёгкое занятие") {
		t.Fatalf("proposal leaked into committed schedule: %#v %v", tomorrow, err)
	}
}

func TestUnknownLegacyDurationIsNotInvented(t *testing.T) {
	context := fixture(t)
	context.Plan.Sessions[0].Minutes = 0
	context.Plan.Sessions[0].DetailsKnown = false
	context.Plan.Sessions[0].Status = "done"
	context.NextPlan.Sessions[0].Minutes = 0
	context.NextPlan.Sessions[0].DetailsKnown = false
	for _, question := range []string{"Что сегодня?", "Какой прогресс?", "Что на следующей неделе?"} {
		got, err := Answer(question, context)
		if err != nil || !strings.Contains(got.Text, "длительность неизвестна") {
			t.Fatalf("legacy facts rejected or invented: %#v %v", got, err)
		}
		for _, evidence := range got.Evidence {
			if strings.Contains(evidence.Text, ", 0 мин,") {
				t.Fatal("unknown duration represented as measured zero")
			}
		}
		if question == "Какой прогресс?" && (!strings.Contains(got.Text, "выполнено 2 из 3") || !strings.Contains(got.Text, "45 мин") || !strings.Contains(got.Text, "не включены в сумму")) {
			t.Fatal("legacy completion totals are incorrect")
		}
	}
}

func TestRecoveryRetainsIndependentPartialMeasurements(t *testing.T) {
	context := fixture(t)
	context.Entries = append(context.Entries,
		Entry{ID: "old-backfill", Date: "2026-10-12", Sleep: ptr(3.0), Fatigue: ptr(2)},
		Entry{ID: "same-day-correction", Date: "2026-10-13", Sleep: ptr(7.5)},
	)
	got, err := Answer("Сон и усталость", context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "сон — 7.5 ч (2026-10-13)") || !strings.Contains(got.Text, "усталость — 9/10 (2026-10-14)") || got.Evidence[0].Reference != "same-day-correction" || got.Evidence[1].Reference != "fatigue" {
		t.Fatalf("independent latest metrics lost: %#v", got)
	}
	context.Entries = nil
	got, err = Answer("Восстановление", context)
	if err != nil || !strings.Contains(got.Text, "пока нет записей") || len(got.Evidence) != 0 {
		t.Fatal("missing data turned into a made-up measurement")
	}
}

func TestUnsupportedAndMissingProfile(t *testing.T) {
	for _, question := range []string{"Погода завтра в Москве?", "Какая у меня погода сегодня?", "Что у меня сегодня на работе?", "Почему болит колено?", "Поставь диагноз", "Напиши программу на Python"} {
		// Weather has an otherwise recognized tomorrow keyword; capability
		// filtering below must not pretend to answer a forecast question.
		got, err := Answer(question, fixture(t))
		if err != nil || got.Intent != "unsupported" || got.Mode != "rules" || len(got.Evidence) != 0 || !strings.Contains(got.Text, "Могу объяснить ваш план") {
			t.Fatalf("out-of-scope question treated as plan fact: %q %#v %v", question, got, err)
		}
	}
	context := fixture(t)
	context.Plan = nil
	got, err := Answer("Что сегодня?", context)
	if err != nil || !strings.Contains(got.Text, "Сначала заполните профиль") || len(got.Evidence) != 0 {
		t.Fatal("assistant invented a missing plan")
	}
}

func TestInvalidContextAndQuestion(t *testing.T) {
	for _, question := range []string{"", " ", "\xff", "hello\x00", strings.Repeat("a", 1001)} {
		if _, err := Answer(question, fixture(t)); err == nil {
			t.Fatalf("accepted invalid question %q", question)
		}
	}
	tests := map[string]func(*Context){
		"clock":        func(c *Context) { c.Now = time.Time{} },
		"week":         func(c *Context) { c.Plan.WeekStart = "bad" },
		"date":         func(c *Context) { c.Plan.Sessions[0].Date = "2026-10-26" },
		"duplicate":    func(c *Context) { c.Plan.Sessions = append(c.Plan.Sessions, c.Plan.Sessions[0]) },
		"minutes":      func(c *Context) { c.Plan.Sessions[0].Minutes = 0 },
		"unknown":      func(c *Context) { c.Plan.Sessions[0].DetailsKnown = false },
		"status":       func(c *Context) { c.Plan.Sessions[0].Status = "invalid" },
		"fatigue":      func(c *Context) { c.Entries[0].Fatigue = ptr(11) },
		"nan sleep":    func(c *Context) { c.Entries[0].Sleep = ptr(math.NaN()) },
		"infinite":     func(c *Context) { c.Entries[0].Sleep = ptr(math.Inf(1)) },
		"entry date":   func(c *Context) { c.Entries[0].Date = "bad" },
		"many entries": func(c *Context) { c.Entries = make([]Entry, 501) },
	}
	for name, modify := range tests {
		t.Run(name, func(t *testing.T) {
			context := fixture(t)
			modify(&context)
			if _, err := Answer("Что сегодня?", context); err == nil {
				t.Fatal("invalid supplied facts were accepted")
			}
		})
	}
}
