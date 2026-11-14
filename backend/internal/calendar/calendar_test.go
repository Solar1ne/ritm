package calendar

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func calendarFixture(t *testing.T) ([]Session, Options) {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	reminder := 30 * time.Minute
	return []Session{{ID: "2026-10-19", Date: "2026-10-19", Title: "Силовая тренировка", Description: "Разминка и упражнения", Minutes: 45}}, Options{
		Location: loc, Hour: 19, Minute: 30, ReminderBefore: &reminder,
		CalendarName: "Ритм", UIDNamespace: "opaque-principal-calendar", GeneratedAt: time.Date(2026, 10, 17, 12, 0, 0, 0, loc), Sequence: 7,
	}
}

func unfold(t *testing.T, data []byte) string {
	t.Helper()
	text := string(data)
	if !strings.HasSuffix(text, "\r\n") {
		t.Fatal("iCalendar lacks final CRLF")
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\r\n"), "\r\n") {
		if len(line) > 75 || !utf8.ValidString(line) || strings.ContainsAny(line, "\r\n") {
			t.Fatalf("invalid physical content line (%d bytes): %q", len(line), line)
		}
	}
	return strings.ReplaceAll(text, "\r\n ", "")
}

func property(text, name string) string {
	for _, line := range strings.Split(text, "\r\n") {
		if strings.HasPrefix(line, name+":") {
			return strings.TrimPrefix(line, name+":")
		}
	}
	return ""
}

func TestExportTimezoneDurationAndAlarm(t *testing.T) {
	sessions, options := calendarFixture(t)
	before := append([]Session(nil), sessions...)
	got, err := Export(sessions, options)
	if err != nil {
		t.Fatal(err)
	}
	text := unfold(t, got)
	for name, want := range map[string]string{
		"VERSION": "2.0", "DTSTART": "20261019T163000Z", "DTEND": "20261019T171500Z",
		"DTSTAMP": "20261017T090000Z", "SEQUENCE": "7", "ACTION": "DISPLAY", "TRIGGER": "-PT30M",
	} {
		if actual := property(text, name); actual != want {
			t.Errorf("%s=%q want %q", name, actual, want)
		}
	}
	if !strings.Contains(text, "BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT30M\r\nDESCRIPTION:Силовая тренировка\r\nEND:VALARM") {
		t.Fatal("DISPLAY alarm must contain its own description")
	}
	if strings.Contains(text, "TZID") || strings.Contains(text, options.UIDNamespace) || strings.Contains(text, "ATTENDEE") {
		t.Fatal("export leaks principal or adds floating timezone/email semantics")
	}
	if !reflect.DeepEqual(sessions, before) {
		t.Fatal("Export mutated caller sessions")
	}
}

func TestStableUIDAcrossEditsAndReminderRemoval(t *testing.T) {
	sessions, options := calendarFixture(t)
	first, err := Export(sessions, options)
	if err != nil {
		t.Fatal(err)
	}
	uid := property(unfold(t, first), "UID")
	sessions[0].Title = "Новое название"
	sessions[0].Minutes = 30
	sessions[0].Date = "2026-10-20" // Caller explicitly preserves event identity.
	options.Hour = 8
	options.Sequence++
	options.GeneratedAt = options.GeneratedAt.Add(time.Hour)
	options.ReminderBefore = nil
	second, err := Export(sessions, options)
	if err != nil {
		t.Fatal(err)
	}
	text := unfold(t, second)
	if uid == "" || property(text, "UID") != uid || property(text, "SEQUENCE") != "8" || strings.Contains(text, "VALARM") {
		t.Fatal("editing a calendar event changed identity or retained disabled alarm")
	}
	options.UIDNamespace = "other-principal-calendar"
	third, err := Export(sessions, options)
	if err != nil {
		t.Fatal(err)
	}
	if property(unfold(t, third), "UID") == uid {
		t.Fatal("different principals share calendar event identity")
	}
	zero := time.Duration(0)
	options.ReminderBefore = &zero
	atStart, err := Export(sessions, options)
	if err != nil || property(unfold(t, atStart), "TRIGGER") != "PT0M" {
		t.Fatalf("at-start alarm malformed: %v", err)
	}
}

func TestTextEscapingFoldingAndPropertyInjection(t *testing.T) {
	sessions, options := calendarFixture(t)
	sessions[0].Title = strings.Repeat("Ёж🦔", 30)
	sessions[0].Description = "строка\\путь;список,часть\r\nATTENDEE:mailto:attacker@example.invalid\nBEGIN:VEVENT\rконец"
	options.CalendarName = "Ритм; будни, выходные"
	data, err := Export(sessions, options)
	if err != nil {
		t.Fatal(err)
	}
	text := unfold(t, data)
	if property(text, "SUMMARY") != sessions[0].Title {
		t.Fatal("UTF-8 folding lost title characters")
	}
	wantDescription := `строка\\путь\;список\,часть\nATTENDEE:mailto:attacker@example.invalid\nBEGIN:VEVENT\nконец`
	if property(text, "DESCRIPTION") != wantDescription {
		t.Fatalf("escaped description %q want %q", property(text, "DESCRIPTION"), wantDescription)
	}
	if property(text, "X-WR-CALNAME") != `Ритм\; будни\, выходные` || strings.Count(text, "\r\nBEGIN:VEVENT\r\n") != 1 || property(text, "ATTENDEE") != "" {
		t.Fatal("input escaped its TEXT property")
	}
}

func TestValidationIsAtomic(t *testing.T) {
	tests := map[string]func(*[]Session, *Options){
		"nil timezone":      func(_ *[]Session, o *Options) { o.Location = nil },
		"hour":              func(_ *[]Session, o *Options) { o.Hour = 24 },
		"minute":            func(_ *[]Session, o *Options) { o.Minute = -1 },
		"negative reminder": func(_ *[]Session, o *Options) { d := -time.Minute; o.ReminderBefore = &d },
		"long reminder":     func(_ *[]Session, o *Options) { d := 8 * 24 * time.Hour; o.ReminderBefore = &d },
		"fraction reminder": func(_ *[]Session, o *Options) { d := time.Second; o.ReminderBefore = &d },
		"no namespace":      func(_ *[]Session, o *Options) { o.UIDNamespace = "" },
		"bad namespace":     func(_ *[]Session, o *Options) { o.UIDNamespace = "a\x00b" },
		"zero timestamp":    func(_ *[]Session, o *Options) { o.GeneratedAt = time.Time{} },
		"sequence range":    func(_ *[]Session, o *Options) { o.Sequence = 1 << 31 },
		"bad calendar":      func(_ *[]Session, o *Options) { o.CalendarName = "\xff" },
		"empty":             func(s *[]Session, _ *Options) { *s = nil },
		"too many":          func(s *[]Session, _ *Options) { *s = make([]Session, 8) },
		"duplicate id":      func(s *[]Session, _ *Options) { *s = append(*s, (*s)[0]) },
		"empty id":          func(s *[]Session, _ *Options) { (*s)[0].ID = "" },
		"invalid date":      func(s *[]Session, _ *Options) { (*s)[0].Date = "2026-02-30" },
		"year zero":         func(s *[]Session, _ *Options) { (*s)[0].Date = "0000-01-01" },
		"short duration":    func(s *[]Session, _ *Options) { (*s)[0].Minutes = 14 },
		"long duration":     func(s *[]Session, _ *Options) { (*s)[0].Minutes = 91 },
		"empty title":       func(s *[]Session, _ *Options) { (*s)[0].Title = " " },
		"control":           func(s *[]Session, _ *Options) { (*s)[0].Description = "a\x00b" },
		"invalid utf8":      func(s *[]Session, _ *Options) { (*s)[0].Title = "\xff" },
		"large description": func(s *[]Session, _ *Options) { (*s)[0].Description = strings.Repeat("a", 4097) },
	}
	for name, modify := range tests {
		t.Run(name, func(t *testing.T) {
			sessions, options := calendarFixture(t)
			modify(&sessions, &options)
			got, err := Export(sessions, options)
			if err == nil || got != nil {
				t.Fatalf("invalid export not atomic: %q, %v", got, err)
			}
		})
	}
}

func TestDSTGapAndAmbiguityAreRejected(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		date string
		hour int
	}{{"2026-03-08", 2}, {"2026-11-01", 1}} {
		sessions, options := calendarFixture(t)
		sessions[0].Date = tc.date
		options.Location, options.Hour, options.Minute = loc, tc.hour, 30
		if got, err := Export(sessions, options); err == nil || got != nil {
			t.Fatalf("ambiguous/nonexistent local time accepted: %s %d:30", tc.date, tc.hour)
		}
	}
}
