package workoutimport

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func parseCSV(ctx context.Context, src io.Reader, loc *time.Location) ([]Workout, error) {
	r := csv.NewReader(src)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("заголовок CSV: %w", err)
	}
	if len(header) < 4 || len(header) > 5 {
		return nil, errors.New("CSV должен содержать 4 обязательных столбца и необязательный distance")
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		name = strings.TrimSpace(name)
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		switch name {
		case "id", "date", "kind", "minutes", "distance":
		default:
			return nil, errors.New("CSV должен содержать id,date,kind,minutes и необязательный distance")
		}
		if _, exists := columns[name]; exists {
			return nil, errors.New("в заголовке CSV повторяется столбец")
		}
		columns[name] = i
	}
	for _, name := range []string{"id", "date", "kind", "minutes"} {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("в CSV отсутствует столбец %s", name)
		}
	}
	var result batch
	for line := 2; ; line++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return result.items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("запись CSV %d: %w", line, err)
		}
		w, err := csvWorkout(record, columns, loc)
		if err != nil {
			return nil, fmt.Errorf("запись CSV %d: %w", line, err)
		}
		if err := result.add(w); err != nil {
			return nil, fmt.Errorf("запись CSV %d: %w", line, err)
		}
	}
}

func csvWorkout(record []string, columns map[string]int, loc *time.Location) (Workout, error) {
	field := func(name string) string { return strings.TrimSpace(record[columns[name]]) }
	id := field("id")
	if !validID(id) {
		return Workout{}, errors.New("id должен содержать 1–128 байт UTF-8 без управляющих знаков")
	}
	date, err := parseDate(field("date"), loc, true)
	if err != nil {
		return Workout{}, err
	}
	kind, err := parseKind(field("kind"))
	if err != nil {
		return Workout{}, err
	}
	minutes, err := strconv.ParseFloat(field("minutes"), 64)
	if err != nil {
		return Workout{}, errors.New("minutes должен быть числом")
	}
	d, err := duration(minutes * 60)
	if err != nil {
		return Workout{}, err
	}
	w := Workout{ExternalID: "csv:" + id, StartedAt: date, Kind: kind, Duration: d}
	if _, exists := columns["distance"]; exists && field("distance") != "" {
		distance, err := strconv.ParseFloat(field("distance"), 64)
		if err != nil {
			return Workout{}, errors.New("distance должен быть числом метров")
		}
		if err := validateDistance(distance); err != nil {
			return Workout{}, err
		}
		w.DistanceMeters = &distance
	}
	return w, nil
}
