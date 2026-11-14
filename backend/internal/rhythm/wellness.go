package rhythm

import (
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxWellnessEntries = 500

// HRVEntry describes a reported measurement, not an interpretation of health.
// Source identifies the device and conditions; it is part of series identity.
type HRVEntry struct {
	ID      string  `json:"id"`
	Date    string  `json:"date"`
	ValueMs float64 `json:"valueMs"`
	Metric  string  `json:"metric"`
	Window  string  `json:"window"`
	Source  string  `json:"source"`
}

type BodyEntry struct {
	ID             string   `json:"id"`
	Date           string   `json:"date"`
	WeightKg       *float64 `json:"weightKg,omitempty"`
	BodyFatPercent *float64 `json:"bodyFatPercent,omitempty"`
	Method         string   `json:"method"`
	Source         string   `json:"source"`
}

// SameHRVSeries excludes measurements calculated by different metrics, windows,
// devices or conditions. Equality alone is not evidence of clinical relevance.
func SameHRVSeries(a, b HRVEntry) bool {
	return a.Metric == b.Metric && a.Window == b.Window && a.Source == b.Source
}

func SameBodySeries(a, b BodyEntry) bool {
	return a.Method == b.Method && a.Source == b.Source
}

func cloneBodyEntries(items []BodyEntry) []BodyEntry {
	out := slices.Clone(items)
	for i := range out {
		if out[i].WeightKg != nil {
			value := *out[i].WeightKg
			out[i].WeightKg = &value
		}
		if out[i].BodyFatPercent != nil {
			value := *out[i].BodyFatPercent
			out[i].BodyFatPercent = &value
		}
	}
	return out
}

func validMeasurementID(id string) bool {
	return len(id) > 0 && len(id) <= 80 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) < 0
}

func validateMeasurement(id, date, source string, now time.Time) error {
	if !validMeasurementID(id) {
		return invalid("Некорректный идентификатор измерения.")
	}
	if !utf8.ValidString(source) || source == "" || source != strings.TrimSpace(source) || utf8.RuneCountInString(source) > 60 || strings.IndexFunc(source, unicode.IsControl) >= 0 {
		return invalid("Укажите источник измерения: до 60 символов без управляющих знаков.")
	}
	if err := validateSavedDate(date, now); err != nil {
		return err
	}
	if date > now.Format(time.DateOnly) {
		return invalid("Измерение не может относиться к будущему дню.")
	}
	return nil
}

func validateSavedDate(value string, now time.Time) error {
	date, err := time.ParseInLocation(time.DateOnly, value, now.Location())
	if err != nil || date.Year() < 1970 || date.Format(time.DateOnly) != value {
		return invalid("Дата должна существовать и иметь формат ГГГГ-ММ-ДД, начиная с 1970 года.")
	}
	return nil
}

func validHRV(e HRVEntry, now time.Time) error {
	if err := validateMeasurement(e.ID, e.Date, e.Source, now); err != nil {
		return err
	}
	if math.IsNaN(e.ValueMs) || math.IsInf(e.ValueMs, 0) || e.ValueMs <= 0 || e.ValueMs > 500 {
		return invalid("HRV должен быть больше 0 и не больше 500 мс.")
	}
	if e.Metric != "rmssd" && e.Metric != "sdnn" {
		return invalid("Укажите метод расчёта HRV: RMSSD или SDNN.")
	}
	if e.Window != "five_minutes" && e.Window != "overnight" && e.Window != "day" {
		return invalid("Укажите период измерения HRV: 5 минут, ночь или сутки.")
	}
	return nil
}

func validBody(e BodyEntry, now time.Time) error {
	if err := validateMeasurement(e.ID, e.Date, e.Source, now); err != nil {
		return err
	}
	if e.WeightKg == nil && e.BodyFatPercent == nil {
		return invalid("Укажите вес или долю жира.")
	}
	if e.WeightKg != nil && (math.IsNaN(*e.WeightKg) || math.IsInf(*e.WeightKg, 0) || *e.WeightKg < 20 || *e.WeightKg > 400) {
		return invalid("Вес должен быть от 20 до 400 кг.")
	}
	if e.BodyFatPercent != nil && (math.IsNaN(*e.BodyFatPercent) || math.IsInf(*e.BodyFatPercent, 0) || *e.BodyFatPercent <= 0 || *e.BodyFatPercent > 75) {
		return invalid("Доля жира должна быть больше 0 и не больше 75%.")
	}
	if e.Method != "bia" && e.Method != "dxa" && e.Method != "skinfold" && e.Method != "other" {
		return invalid("Укажите метод измерения состава тела.")
	}
	return nil
}

func AddHRV(r Record, revision uint64, e HRVEntry, now time.Time) (Record, error) {
	e.Source = strings.TrimSpace(e.Source)
	if err := validHRV(e, now); err != nil {
		return Record{}, err
	}
	for _, history := range [2][]HRVEntry{r.HRVReceipts, r.HRVEntries} {
		for _, previous := range history {
			if previous.ID == e.ID {
				if previous == e {
					return Clone(r), nil
				}
				return Record{}, ErrEntryConflict
			}
		}
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	if r.Profile == nil {
		return Record{}, invalid("Сначала заполните профиль.")
	}
	if e.Date < EditableFrom(now) {
		return Record{}, invalid("Добавляйте и исправляйте измерения за последние 90 дней.")
	}
	r = Clone(r)
	index := slices.IndexFunc(r.HRVEntries, func(old HRVEntry) bool { return old.Date == e.Date && SameHRVSeries(old, e) })
	if index >= 0 {
		r.HRVEntries[index] = e
	} else {
		if len(r.HRVEntries) >= MaxWellnessEntries {
			return Record{}, ErrCapacity
		}
		r.HRVEntries = append(r.HRVEntries, e)
	}
	r.HRVReceipts = append(r.HRVReceipts, e)
	if len(r.HRVReceipts) > MaxWellnessEntries {
		r.HRVReceipts = slices.Clone(r.HRVReceipts[len(r.HRVReceipts)-MaxWellnessEntries:])
	}
	r.Revision++
	return r, nil
}

func sameBody(a, b BodyEntry) bool {
	return a.ID == b.ID && a.Date == b.Date && a.Method == b.Method && a.Source == b.Source && sameOptional(a.WeightKg, b.WeightKg) && sameOptional(a.BodyFatPercent, b.BodyFatPercent)
}

// AddBody replaces the complete measurement for its date and series. An omitted
// component remains unknown; callers editing a fact must include both values.
func AddBody(r Record, revision uint64, e BodyEntry, now time.Time) (Record, error) {
	e.Source = strings.TrimSpace(e.Source)
	if err := validBody(e, now); err != nil {
		return Record{}, err
	}
	for _, history := range [2][]BodyEntry{r.BodyReceipts, r.BodyEntries} {
		for _, previous := range history {
			if previous.ID == e.ID {
				if sameBody(previous, e) {
					return Clone(r), nil
				}
				return Record{}, ErrEntryConflict
			}
		}
	}
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	if r.Profile == nil {
		return Record{}, invalid("Сначала заполните профиль.")
	}
	if e.Date < EditableFrom(now) {
		return Record{}, invalid("Добавляйте и исправляйте измерения за последние 90 дней.")
	}
	r = Clone(r)
	e = cloneBodyEntries([]BodyEntry{e})[0]
	index := slices.IndexFunc(r.BodyEntries, func(old BodyEntry) bool { return old.Date == e.Date && SameBodySeries(old, e) })
	if index >= 0 {
		r.BodyEntries[index] = e
	} else {
		if len(r.BodyEntries) >= MaxWellnessEntries {
			return Record{}, ErrCapacity
		}
		r.BodyEntries = append(r.BodyEntries, e)
	}
	r.BodyReceipts = append(r.BodyReceipts, cloneBodyEntries([]BodyEntry{e})[0])
	if len(r.BodyReceipts) > MaxWellnessEntries {
		r.BodyReceipts = slices.Clone(r.BodyReceipts[len(r.BodyReceipts)-MaxWellnessEntries:])
	}
	r.Revision++
	return r, nil
}

func DeleteHRV(r Record, revision uint64, id string) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	index := slices.IndexFunc(r.HRVEntries, func(e HRVEntry) bool { return e.ID == id })
	if index < 0 {
		return Record{}, ErrNotFound
	}
	r = Clone(r)
	r.HRVEntries = slices.Delete(r.HRVEntries, index, index+1)
	r.Revision++
	return r, nil
}

func DeleteBody(r Record, revision uint64, id string) (Record, error) {
	if revision != r.Revision {
		return Record{}, ErrConflict
	}
	index := slices.IndexFunc(r.BodyEntries, func(e BodyEntry) bool { return e.ID == id })
	if index < 0 {
		return Record{}, ErrNotFound
	}
	r = Clone(r)
	r.BodyEntries = slices.Delete(r.BodyEntries, index, index+1)
	r.Revision++
	return r, nil
}

func validateWellness(r Record, now time.Time) error {
	if len(r.HRVEntries) > MaxWellnessEntries || len(r.BodyEntries) > MaxWellnessEntries {
		return ErrCapacity
	}
	ids := make(map[string]bool, len(r.HRVEntries))
	hrvFacts := make(map[[4]string]bool, len(r.HRVEntries))
	for _, e := range r.HRVEntries {
		if err := validHRV(e, now); err != nil {
			return err
		}
		key := [4]string{e.Date, e.Metric, e.Window, e.Source}
		if ids[e.ID] || hrvFacts[key] {
			return invalid("Измерение HRV повторяется в резервной копии.")
		}
		ids[e.ID], hrvFacts[key] = true, true
	}
	ids = make(map[string]bool, len(r.BodyEntries))
	bodyFacts := make(map[[3]string]bool, len(r.BodyEntries))
	for _, e := range r.BodyEntries {
		if err := validBody(e, now); err != nil {
			return err
		}
		key := [3]string{e.Date, e.Method, e.Source}
		if ids[e.ID] || bodyFacts[key] {
			return invalid("Измерение состава тела повторяется в резервной копии.")
		}
		ids[e.ID], bodyFacts[key] = true, true
	}
	return nil
}
