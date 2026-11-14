// Package workoutimport reads bounded workout files. It never contacts a device,
// fetches an XML schema, persists records, or marks planned sessions complete.
package workoutimport

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Format string

const (
	CSV        Format = "csv"
	TCX        Format = "tcx"
	MaxBytes          = 1 << 20
	MaxRecords        = 100
	MaxDepth          = 32
	// An import is an individual workout, not a multi-day activity archive item.
	MaxDuration = 24 * time.Hour
)

var (
	ErrTooLarge = errors.New("файл превышает 1 МиБ")
	ErrTooMany  = errors.New("в одном файле допускается не более 100 тренировок")
	ErrConflict = errors.New("один внешний ID содержит разные данные тренировки")
)

// Workout owns its fields, including DistanceMeters. Kind is one of running,
// cycling, walking, swimming, strength, or other. Distance is measured in metres.
// ExternalID is stable across reimports; callers must also check conflicts
// against previously stored records before committing an entire batch.
type Workout struct {
	ExternalID     string
	StartedAt      time.Time
	Kind           string
	Duration       time.Duration
	DistanceMeters *float64
}

// Parse consumes at most MaxBytes+1 bytes and returns no partial results on error.
// Exact duplicates are collapsed, but count toward the input record limit.
// CSV requires id,date,kind,minutes columns; distance (metres) is optional.
// Dates are YYYY-MM-DD in location or RFC3339 with an explicit offset.
// TCX supports v2 Activities/Activity lap summaries, not multisport or courses.
// FIT and vendor CSV schemas are intentionally unsupported.
// Cancellation is checked between reads and records. The caller owns reader
// deadlines: an arbitrary blocking io.Reader cannot be interrupted by context.
func Parse(ctx context.Context, src io.Reader, format Format, location *time.Location) ([]Workout, error) {
	if ctx == nil || src == nil || location == nil {
		return nil, errors.New("для импорта нужны context, reader и часовой пояс")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limited := &io.LimitedReader{R: contextReader{ctx: ctx, r: src}, N: MaxBytes + 1}
	var workouts []Workout
	var err error
	switch format {
	case CSV:
		workouts, err = parseCSV(ctx, limited, location)
	case TCX:
		workouts, err = parseTCX(ctx, limited, location)
	default:
		return nil, errors.New("поддерживаются только CSV по шаблону Ритма и TCX v2; FIT не поддерживается")
	}
	if limited.N == 0 {
		return nil, ErrTooLarge
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(workouts) == 0 {
		return nil, errors.New("в файле нет тренировок")
	}
	return workouts, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

type batch struct {
	items []Workout
	seen  map[string]int
	count int
}

func (b *batch) add(w Workout) error {
	b.count++
	if b.count > MaxRecords {
		return ErrTooMany
	}
	if index, ok := b.seen[w.ExternalID]; ok {
		if !Equal(b.items[index], w) {
			return ErrConflict
		}
		return nil
	}
	if b.seen == nil {
		b.seen = make(map[string]int)
	}
	b.seen[w.ExternalID] = len(b.items)
	b.items = append(b.items, w)
	return nil
}

// Equal compares normalized content, including identity and the instant in time.
// It allows a persistence boundary to distinguish retry from ID conflict.
func Equal(a, b Workout) bool {
	if a.ExternalID != b.ExternalID || !a.StartedAt.Equal(b.StartedAt) || a.Kind != b.Kind || a.Duration != b.Duration {
		return false
	}
	if a.DistanceMeters == nil || b.DistanceMeters == nil {
		return a.DistanceMeters == nil && b.DistanceMeters == nil
	}
	return *a.DistanceMeters == *b.DistanceMeters
}

func parseDate(value string, loc *time.Location, dateOnly bool) (time.Time, error) {
	var parsed time.Time
	var err error
	if dateOnly && len(value) == len(time.DateOnly) {
		parsed, err = time.ParseInLocation(time.DateOnly, value, loc)
		if err == nil && parsed.Format(time.DateOnly) != value {
			err = errors.New("местная дата не существует")
		}
	} else {
		if !strictRFC3339(value) {
			return time.Time{}, errors.New("дата-время должна быть RFC3339 с часовым поясом и не более 9 знаков дробных секунд")
		}
		parsed, err = time.Parse(time.RFC3339Nano, value)
	}
	if err != nil || parsed.Year() < 1 || parsed.Year() > 9999 {
		return time.Time{}, errors.New("дата должна быть ГГГГ-ММ-ДД или RFC3339 с часовым поясом")
	}
	return parsed.In(loc), nil
}

// time.Parse accepts a comma fraction, some out-of-range numeric offsets, and
// silently truncates sub-nanosecond precision. Reject those before parsing so
// distinct external activity identifiers cannot silently collapse.
func strictRFC3339(value string) bool {
	if len(value) < 20 || value[10] != 'T' || value[13] != ':' || value[16] != ':' {
		return false
	}
	suffix := value[19:]
	if suffix[0] == '.' {
		i := 1
		for i < len(suffix) && suffix[i] >= '0' && suffix[i] <= '9' {
			i++
		}
		if i < 2 || i > 10 {
			return false
		}
		suffix = suffix[i:]
	}
	if suffix == "Z" {
		return true
	}
	if len(suffix) != 6 || (suffix[0] != '+' && suffix[0] != '-') || suffix[3] != ':' {
		return false
	}
	for _, i := range []int{1, 2, 4, 5} {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	hour := int(suffix[1]-'0')*10 + int(suffix[2]-'0')
	minute := int(suffix[4]-'0')*10 + int(suffix[5]-'0')
	return hour <= 23 && minute <= 59
}

func duration(seconds float64) (time.Duration, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > MaxDuration.Seconds() {
		return 0, errors.New("длительность должна быть положительной и не превышать 24 часа")
	}
	d := time.Duration(math.Round(seconds * float64(time.Second)))
	if d <= 0 {
		return 0, errors.New("длительность слишком мала")
	}
	return d, nil
}

func validateDistance(distance float64) error {
	if math.IsNaN(distance) || math.IsInf(distance, 0) || distance < 0 || distance > 1_000_000 {
		return errors.New("расстояние должно быть от 0 до 1 000 000 метров")
	}
	return nil
}

func validID(id string) bool {
	return id != "" && len(id) <= 128 && utf8.ValidString(id) && strings.IndexFunc(id, unicode.IsControl) < 0
}

func parseKind(kind string) (string, error) {
	switch kind {
	case "running", "cycling", "walking", "swimming", "strength", "other":
		return kind, nil
	default:
		return "", errors.New("неизвестный kind: используйте running, cycling, walking, swimming, strength или other")
	}
}
