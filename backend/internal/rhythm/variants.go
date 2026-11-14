package rhythm

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const MaxSessionAdjustments = MaxWeeks * 7

type SessionConstraints struct {
	AvailableMinutes int    `json:"availableMinutes"`
	Environment      string `json:"environment"`
	LowImpact        bool   `json:"lowImpact"`
}

// SessionAdjustment overlays one immutable scheduled session. Text and kind are
// always derived from the versioned-in-code catalog, never supplied by a model
// or restored as arbitrary exercise instructions. LowImpact stores the user's
// constraint; the selected variant may also be low-impact without that filter.
type SessionAdjustment struct {
	SessionID   string `json:"sessionId"`
	Date        string `json:"date"`
	VariantID   string `json:"variantId"`
	Minutes     int    `json:"minutes"`
	Environment string `json:"environment"`
	LowImpact   bool   `json:"lowImpact"`
	UpdatedAt   string `json:"updatedAt"`
}

type SessionVariant struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Environment string `json:"environment"`
	Minutes     int    `json:"minutes"`
	LowImpact   bool   `json:"lowImpact"`
}

type SessionVariantPreview struct {
	Original           Session            `json:"original"`
	Current            Session            `json:"current"`
	Adjustment         *SessionAdjustment `json:"adjustment,omitempty"`
	Options            []SessionVariant   `json:"options"`
	RecoveryRestricted bool               `json:"recoveryRestricted"`
	Reasons            []string           `json:"reasons"`
}

// A fresh slice keeps the catalog immutable without hidden mutable globals.
// "Low impact" is a product filter, not a medical or physiological guarantee.
// Existing catalog IDs are persisted: changing their meaning requires migration.
func variantCatalog() []SessionVariant {
	return []SessionVariant{
		{ID: "home-mobility-v1", Kind: "mobility", Title: "Мягкая подвижность дома", Description: "Спокойная разминка, движения в комфортном диапазоне и заминка. Без прыжков и дополнительного веса.", Environment: "home", LowImpact: true},
		{ID: "home-bodyweight-v1", Kind: "strength", Title: "Упражнения с собственным весом", Description: "Разминка, знакомые базовые упражнения с собственным весом и спокойная заминка. Без увеличения выбранной длительности.", Environment: "home", LowImpact: false},
		{ID: "outdoors-mobility-v1", Kind: "mobility", Title: "Мягкая подвижность на улице", Description: "Спокойные движения и разминка на удобной ровной площадке. Без прыжков и дополнительного веса.", Environment: "outdoors", LowImpact: true},
		{ID: "outdoors-walk-v1", Kind: "walk", Title: "Спокойная прогулка", Description: "Прогулка в удобном темпе по знакомому маршруту. Без беговых отрезков и дополнительного веса.", Environment: "outdoors", LowImpact: true},
		{ID: "outdoors-run-v1", Kind: "run", Title: "Бег с паузами на ходьбу", Description: "Знакомый спокойный темп с возможностью перейти на шаг. Без ускорений и увеличения длительности.", Environment: "outdoors", LowImpact: false},
		{ID: "gym-mobility-v1", Kind: "mobility", Title: "Мягкая подвижность в зале", Description: "Спокойная разминка и движения без дополнительного веса. Без прыжков и интенсивных отрезков.", Environment: "gym", LowImpact: true},
		{ID: "gym-walk-v1", Kind: "walk", Title: "Спокойная ходьба в зале", Description: "Ходьба в удобном темпе без беговых отрезков и дополнительного веса.", Environment: "gym", LowImpact: true},
		{ID: "gym-strength-v1", Kind: "strength", Title: "Знакомая силовая тренировка в зале", Description: "Разминка и знакомые упражнения. Без увеличения привычного веса, интенсивности или длительности.", Environment: "gym", LowImpact: false},
	}
}

func catalogVariant(id string) (SessionVariant, bool) {
	for _, variant := range variantCatalog() {
		if variant.ID == id {
			return variant, true
		}
	}
	return SessionVariant{}, false
}

// Alternative categories are deliberately conservative: mobility/walking are
// available everywhere they fit the environment, while running and strength
// must match the original category. This does not compare physiological load.
func compatibleVariant(original Session, variant SessionVariant) bool {
	return variant.Kind == "mobility" || variant.Kind == "walk" || variant.Kind == original.Kind
}

func validateConstraints(c SessionConstraints, original Session) error {
	if c.AvailableMinutes < 15 || c.AvailableMinutes > original.Minutes {
		return invalid(fmt.Sprintf("Доступное время — от 15 до %d минут исходного занятия.", original.Minutes))
	}
	if c.Environment != "home" && c.Environment != "outdoors" && c.Environment != "gym" {
		return invalid("Выберите место: дома, на улице или в зале.")
	}
	return nil
}

func adjustmentIndex(r Record, sessionID string) int {
	return slices.IndexFunc(r.SessionAdjustments, func(a SessionAdjustment) bool { return a.SessionID == sessionID })
}

func variantTarget(r Record, sessionID string, now time.Time) (Session, error) {
	date, err := time.ParseInLocation(time.DateOnly, sessionID, now.Location())
	if err != nil || date.Year() < 1 || sessionID < now.Format(time.DateOnly) {
		return Session{}, invalid("Менять вариант можно только для сегодняшнего или будущего занятия.")
	}
	weekID := WeekStart(date).Format(time.DateOnly)
	current := WeekStart(now)
	if weekID != current.Format(time.DateOnly) && weekID != current.AddDate(0, 0, 7).Format(time.DateOnly) {
		return Session{}, invalid("Варианты доступны в текущей и принятой следующей неделе.")
	}
	w := snapshot(r, weekID)
	if w == nil || weekID != current.Format(time.DateOnly) && w.Source != "accepted" {
		return Session{}, invalid("Сначала примите расписание этой недели: предложение само по себе не является планом.")
	}
	if w.Source == "accepted" {
		accepted, err := time.Parse(time.RFC3339, w.AcceptedAt)
		if err != nil || accepted.After(now) {
			return Session{}, invalid("Время выбора варианта не может предшествовать принятию плана.")
		}
	}
	index := slices.IndexFunc(w.Plan.Sessions, func(s Session) bool { return s.ID == sessionID && s.Date == sessionID })
	if index < 0 {
		return Session{}, ErrNotFound
	}
	original := w.Plan.Sessions[index]
	if !original.DetailsKnown || original.Minutes < 15 || original.Minutes > 90 {
		return Session{}, invalid("У этого занятия неизвестны исходные детали; вариант нельзя восстановить предположением.")
	}
	if slices.ContainsFunc(r.Entries, func(e Entry) bool { return e.SessionID == sessionID && (e.Status == "done" || e.Status == "missed") }) {
		return Session{}, invalid("Выполненное или пропущенное занятие нельзя заменять. Сначала исправьте отметку в дневнике.")
	}
	return original, nil
}

func recoveryRestriction(r Record, original Session, now time.Time) (bool, int) {
	ceiling := original.Minutes
	if original.Kind == "mobility" {
		return true, ceiling // An accepted recovery schedule is already reduced.
	}
	projection := Project(r, now)
	if projection.Decision != nil && projection.Decision.Mode == "recover" {
		return true, max(15, original.Minutes*80/100)
	}
	return false, ceiling
}

// PreviewSessionVariants performs no mutation. The caller must explicitly apply
// a returned canonical ID; Apply recalculates eligibility inside the repository
// transaction so an old preview cannot bypass a new diary/recovery signal.
func PreviewSessionVariants(input Record, sessionID string, constraints SessionConstraints, now time.Time) (SessionVariantPreview, error) {
	if input.Profile == nil {
		return SessionVariantPreview{}, invalid("Сначала создайте свой план.")
	}
	r, _, err := materialize(input, now)
	if err != nil {
		return SessionVariantPreview{}, err
	}
	original, err := variantTarget(r, sessionID, now)
	if err != nil {
		return SessionVariantPreview{}, err
	}
	if err := validateConstraints(constraints, original); err != nil {
		return SessionVariantPreview{}, err
	}
	recovery, ceiling := recoveryRestriction(r, original, now)
	preview := SessionVariantPreview{Original: original, Current: original, RecoveryRestricted: recovery, Options: []SessionVariant{}, Reasons: []string{
		fmt.Sprintf("Исходное занятие сохранено отдельно: %d мин. Вариант не увеличивает его длительность.", original.Minutes),
	}}
	if index := adjustmentIndex(r, sessionID); index >= 0 {
		adjustment := r.SessionAdjustments[index]
		preview.Adjustment = &adjustment
		preview.Current = adjustedSession(original, adjustment)
	}
	if recovery {
		preview.Reasons = append(preview.Reasons, fmt.Sprintf("Действует ограничение восстановления: только подвижность или спокойная ходьба, не более %d мин.", ceiling))
	}
	if constraints.LowImpact {
		preview.Reasons = append(preview.Reasons, "Выбраны варианты без прыжков и беговых отрезков.")
	}
	for _, variant := range variantCatalog() {
		if variant.Environment != constraints.Environment || !compatibleVariant(original, variant) || constraints.LowImpact && !variant.LowImpact || recovery && variant.Kind != "mobility" && variant.Kind != "walk" {
			continue
		}
		variant.Minutes = min(constraints.AvailableMinutes, ceiling)
		preview.Options = append(preview.Options, variant)
	}
	return preview, nil
}

// ApplySessionVariant returns an owned aggregate. Only an exact match to the
// currently applied choice bypasses a stale revision for lost-response retries;
// an older choice can never overwrite a later choice. Repository atomicity is
// owned by the caller, as with the other domain mutation functions.
func ApplySessionVariant(input Record, revision uint64, sessionID, variantID string, constraints SessionConstraints, now time.Time) (Record, error) {
	preview, err := PreviewSessionVariants(input, sessionID, constraints, now)
	if err != nil {
		return Record{}, err
	}
	index := slices.IndexFunc(preview.Options, func(v SessionVariant) bool { return v.ID == variantID })
	if index < 0 {
		return Record{}, invalid("Выбранный вариант больше не подходит условиям. Обновите варианты занятия.")
	}
	variant := preview.Options[index]
	choice := SessionAdjustment{SessionID: sessionID, Date: preview.Original.Date, VariantID: variant.ID, Minutes: variant.Minutes, Environment: constraints.Environment, LowImpact: constraints.LowImpact, UpdatedAt: now.UTC().Format(time.RFC3339Nano)}
	if old := preview.Adjustment; old != nil && sameAdjustmentChoice(*old, choice) {
		return Clone(input), nil
	}
	if revision != input.Revision {
		return Record{}, ErrConflict
	}
	r, _, err := materialize(input, now)
	if err != nil {
		return Record{}, err
	}
	if existing := adjustmentIndex(r, sessionID); existing >= 0 {
		r.SessionAdjustments[existing] = choice
	} else {
		if len(r.SessionAdjustments) >= MaxSessionAdjustments {
			return Record{}, ErrCapacity
		}
		r.SessionAdjustments = append(r.SessionAdjustments, choice)
		slices.SortFunc(r.SessionAdjustments, func(a, b SessionAdjustment) int { return strings.Compare(a.SessionID, b.SessionID) })
	}
	r.Revision = input.Revision + 1
	return r, nil
}

func sameAdjustmentChoice(a, b SessionAdjustment) bool {
	a.UpdatedAt, b.UpdatedAt = "", ""
	return a == b
}

// RevertSessionVariant restores the original schedule only by explicit request.
// Recovery guards also apply to reverting, so reset is not a route to restoring
// an intense/longer session after a new adverse diary signal.
func RevertSessionVariant(input Record, revision uint64, sessionID string, now time.Time) (Record, error) {
	r, _, err := materialize(input, now)
	if err != nil {
		return Record{}, err
	}
	original, err := variantTarget(r, sessionID, now)
	if err != nil {
		return Record{}, err
	}
	index := adjustmentIndex(r, sessionID)
	if index < 0 {
		return Clone(input), nil
	}
	if revision != input.Revision {
		return Record{}, ErrConflict
	}
	recovery, ceiling := recoveryRestriction(r, original, now)
	if recovery && (original.Minutes > ceiling || original.Kind != "mobility" && original.Kind != "walk") {
		return Record{}, invalid("Исходное занятие сейчас не соответствует ограничению восстановления. Выберите подвижность или спокойную ходьбу.")
	}
	r.SessionAdjustments = slices.Delete(r.SessionAdjustments, index, index+1)
	r.Revision = input.Revision + 1
	return r, nil
}

func adjustedSession(original Session, adjustment SessionAdjustment) Session {
	variant, ok := catalogVariant(adjustment.VariantID)
	if !ok || !compatibleVariant(original, variant) || variant.Environment != adjustment.Environment || adjustment.LowImpact && !variant.LowImpact || adjustment.Minutes < 15 || adjustment.Minutes > original.Minutes {
		return original // Invalid externally supplied data is rejected by validation.
	}
	result := original
	result.Kind, result.Title, result.Minutes = variant.Kind, variant.Title, adjustment.Minutes
	result.VariantID, result.Environment, result.LowImpact = variant.ID, variant.Environment, variant.LowImpact
	result.OriginalMinutes = original.Minutes
	return result
}

func applySessionAdjustments(plan Plan, adjustments []SessionAdjustment) Plan {
	plan = clonePlan(plan)
	plan.TotalMinutes = 0
	for i, original := range plan.Sessions {
		for _, adjustment := range adjustments {
			if adjustment.SessionID == original.ID && adjustment.Date == original.Date {
				plan.Sessions[i] = adjustedSession(original, adjustment)
				break
			}
		}
		plan.TotalMinutes += plan.Sessions[i].Minutes
	}
	return plan
}

func validateSessionAdjustments(r Record, now time.Time) error {
	if len(r.SessionAdjustments) > MaxSessionAdjustments {
		return ErrCapacity
	}
	seen := make(map[string]bool, len(r.SessionAdjustments))
	for _, adjustment := range r.SessionAdjustments {
		date, err := time.ParseInLocation(time.DateOnly, adjustment.Date, now.Location())
		if err != nil || date.Year() < 1 || adjustment.Date != adjustment.SessionID || seen[adjustment.SessionID] {
			return invalid("У варианта занятия неверная или повторная дата/идентификатор.")
		}
		seen[adjustment.SessionID] = true
		week := snapshot(r, WeekStart(date).Format(time.DateOnly))
		if week == nil {
			return invalid("Вариант занятия ссылается на отсутствующую сохранённую неделю.")
		}
		index := slices.IndexFunc(week.Plan.Sessions, func(s Session) bool { return s.ID == adjustment.SessionID && s.Date == adjustment.Date })
		if index < 0 || !week.Plan.Sessions[index].DetailsKnown {
			return invalid("Для варианта нужны известные исходные детали занятия.")
		}
		original := week.Plan.Sessions[index]
		variant, ok := catalogVariant(adjustment.VariantID)
		if !ok || !compatibleVariant(original, variant) || variant.Environment != adjustment.Environment || adjustment.LowImpact && !variant.LowImpact || adjustment.Minutes < 15 || adjustment.Minutes > original.Minutes {
			return invalid("Вариант занятия не соответствует каталогу, месту, ограничениям или исходной длительности.")
		}
		updated, err := time.Parse(time.RFC3339Nano, adjustment.UpdatedAt)
		if err != nil || updated.UTC().Format(time.RFC3339Nano) != adjustment.UpdatedAt || updated.After(now) || updated.In(now.Location()).Format(time.DateOnly) > adjustment.Date {
			return invalid("Время выбора варианта должно быть каноническим UTC и не позже дня занятия или текущего времени.")
		}
		updateWeek := WeekStart(updated.In(now.Location())).Format(time.DateOnly)
		if updateWeek != week.WeekStart {
			if week.Source != "accepted" || updateWeek != WeekStart(date).AddDate(0, 0, -7).Format(time.DateOnly) {
				return invalid("Вариант можно было выбрать только в текущей или принятой следующей неделе.")
			}
		}
		if week.Source == "accepted" {
			accepted, err := time.Parse(time.RFC3339, week.AcceptedAt)
			if err != nil || updated.Before(accepted) {
				return invalid("Вариант следующей недели нельзя выбрать раньше принятия плана.")
			}
		}
	}
	return nil
}
