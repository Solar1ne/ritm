// Package calendar builds downloadable iCalendar files without network access.
// A file export does not synchronize calendars or revoke a previously imported
// event or alarm. The receiving calendar controls replacement by UID/SEQUENCE.
package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Session struct {
	ID          string
	Date        string // YYYY-MM-DD in Options.Location.
	Title       string
	Description string
	Minutes     int
}

type Options struct {
	Location       *time.Location
	Hour           int
	Minute         int
	ReminderBefore *time.Duration // nil disables alarms; 0 means at the start.
	CalendarName   string
	UIDNamespace   string // Stable, opaque scope per principal/calendar.
	GeneratedAt    time.Time
	Sequence       uint64 // Monotonic persisted revision, capped at RFC INTEGER max.
}

// Export implements RFC 5545 content lines, TEXT, UTC DATE-TIME, UID and DISPLAY
// alarms: https://datatracker.ietf.org/doc/html/rfc5545 . Dates and the configured
// local clock are converted to UTC, so no floating times or VTIMEZONE are needed.
// Caller-owned slices are never modified. All validation precedes serialization.
// Event identity depends only on UIDNamespace and Session.ID, not the reminder,
// description, export time or plan revision. Keep an ID when updating an event.
func Export(sessions []Session, options Options) ([]byte, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	if len(sessions) < 1 || len(sessions) > 7 {
		return nil, errors.New("недельный календарь должен содержать от 1 до 7 занятий")
	}
	starts := make([]time.Time, len(sessions))
	seen := make(map[string]bool, len(sessions))
	for i, session := range sessions {
		if err := validateText(session.ID, 256, false); err != nil || strings.TrimSpace(session.ID) == "" {
			return nil, errors.New("занятие требует непустой ID без управляющих знаков, до 256 байт")
		}
		if seen[session.ID] {
			return nil, errors.New("ID занятий календаря должны быть уникальными")
		}
		seen[session.ID] = true
		if err := validateText(session.Title, 512, true); err != nil || strings.TrimSpace(session.Title) == "" {
			return nil, errors.New("название занятия должно содержать 1–512 байт UTF-8")
		}
		if err := validateText(session.Description, 4096, true); err != nil {
			return nil, errors.New("описание занятия должно содержать до 4096 байт UTF-8")
		}
		if session.Minutes < 15 || session.Minutes > 90 {
			return nil, errors.New("занятие календаря должно длиться от 15 до 90 минут")
		}
		date, err := time.Parse(time.DateOnly, session.Date)
		if err != nil || date.Year() < 1 {
			return nil, errors.New("дата занятия должна иметь формат ГГГГ-ММ-ДД")
		}
		start := time.Date(date.Year(), date.Month(), date.Day(), options.Hour, options.Minute, 0, 0, options.Location)
		if start.Format(time.DateOnly) != session.Date || start.Hour() != options.Hour || start.Minute() != options.Minute {
			return nil, errors.New("выбранное местное время не существует из-за смены часового пояса")
		}
		// Reject an ambiguous clock reading rather than silently choosing a DST
		// occurrence. The product uses Europe/Moscow, which has no current DST.
		for _, delta := range []time.Duration{-2 * time.Hour, -time.Hour, -30 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour} {
			other := start.Add(delta)
			if other.Format("2006-01-02 15:04") == start.Format("2006-01-02 15:04") {
				return nil, errors.New("выбранное местное время неоднозначно из-за смены часового пояса")
			}
		}
		end := start.Add(time.Duration(session.Minutes) * time.Minute)
		if start.UTC().Year() < 1 || start.UTC().Year() > 9999 || end.UTC().Year() > 9999 {
			return nil, errors.New("время занятия выходит за поддерживаемый диапазон календаря")
		}
		starts[i] = start
	}
	var out strings.Builder
	line := func(value string) { writeFolded(&out, value) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Rhythm//Weekly Plan//RU")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:" + escapeText(options.CalendarName))
	for i, session := range sessions {
		line("BEGIN:VEVENT")
		line("UID:" + eventUID(options.UIDNamespace, session.ID))
		line("DTSTAMP:" + options.GeneratedAt.UTC().Format("20060102T150405Z"))
		line("SEQUENCE:" + strconv.FormatUint(options.Sequence, 10))
		line("DTSTART:" + starts[i].UTC().Format("20060102T150405Z"))
		line("DTEND:" + starts[i].Add(time.Duration(session.Minutes)*time.Minute).UTC().Format("20060102T150405Z"))
		line("SUMMARY:" + escapeText(session.Title))
		line("DESCRIPTION:" + escapeText(session.Description))
		if options.ReminderBefore != nil {
			line("BEGIN:VALARM")
			line("ACTION:DISPLAY")
			prefix := "-"
			if *options.ReminderBefore == 0 {
				prefix = ""
			}
			line("TRIGGER:" + prefix + "PT" + strconv.FormatInt(int64(*options.ReminderBefore/time.Minute), 10) + "M")
			line("DESCRIPTION:" + escapeText(session.Title))
			line("END:VALARM")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return []byte(out.String()), nil
}

func validateOptions(o Options) error {
	if o.Location == nil {
		return errors.New("для календаря нужен часовой пояс")
	}
	if o.Hour < 0 || o.Hour > 23 || o.Minute < 0 || o.Minute > 59 {
		return errors.New("время занятия должно быть в диапазоне 00:00–23:59")
	}
	if o.ReminderBefore != nil && (*o.ReminderBefore < 0 || *o.ReminderBefore > 7*24*time.Hour || *o.ReminderBefore%time.Minute != 0) {
		return errors.New("напоминание задаётся целым числом минут от 0 до 10080 перед занятием")
	}
	if err := validateText(o.CalendarName, 256, true); err != nil || strings.TrimSpace(o.CalendarName) == "" {
		return errors.New("название календаря должно содержать 1–256 байт UTF-8")
	}
	if err := validateText(o.UIDNamespace, 256, false); err != nil || strings.TrimSpace(o.UIDNamespace) == "" {
		return errors.New("для стабильных UID нужна непустая область календаря до 256 байт")
	}
	if o.GeneratedAt.IsZero() || o.GeneratedAt.UTC().Year() < 1 || o.GeneratedAt.UTC().Year() > 9999 {
		return errors.New("нужно корректное время формирования календаря")
	}
	if o.Sequence > 2_147_483_647 {
		return errors.New("ревизия календаря превышает диапазон iCalendar INTEGER")
	}
	return nil
}

func validateText(value string, limit int, multiline bool) error {
	if len(value) > limit || !utf8.ValidString(value) {
		return errors.New("некорректная длина или UTF-8")
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\r' || r == '\n' || r == '\t')) {
			return fmt.Errorf("недопустимый управляющий знак U+%04X", r)
		}
	}
	return nil
}

func eventUID(namespace, id string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + id))
	return hex.EncodeToString(sum[:]) + "@rhythm.local"
}

func escapeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n").Replace(text)
}

func writeFolded(out *strings.Builder, value string) {
	width := 0
	for _, r := range value {
		size := utf8.RuneLen(r)
		if width+size > 75 {
			out.WriteString("\r\n ")
			width = 1
		}
		out.WriteRune(r)
		width += size
	}
	out.WriteString("\r\n")
}
