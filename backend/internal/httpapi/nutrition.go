package httpapi

import (
	"errors"
	"net/http"
	"time"

	"rhythm/internal/nutrition"
)

func (s *Server) nutritionCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticated(w, r); !ok {
		return
	}
	s.json(w, http.StatusOK, struct {
		Recipes        []nutrition.Recipe `json:"recipes"`
		Allergens      []nutrition.Option `json:"allergens"`
		Diets          []nutrition.Option `json:"diets"`
		Meals          []nutrition.Option `json:"meals"`
		Sources        []nutrition.Source `json:"sources"`
		PlateGuidance  string             `json:"plateGuidance"`
		AllergenNotice string             `json:"allergenNotice"`
		RecipeNotice   string             `json:"recipeNotice"`
	}{nutrition.Catalog(), nutrition.Allergens(), nutrition.Diets(), nutrition.Meals(), nutrition.Sources(), nutrition.PlateGuidance, nutrition.AllergenNotice, nutrition.RecipeNotice})
}

func (s *Server) nutritionPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticated(w, r); !ok {
		return
	}
	var input struct {
		StartDate string             `json:"startDate"`
		Filters   *nutrition.Filters `json:"filters"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Filters == nil {
		s.fail(w, http.StatusBadRequest, "validation", "Нужны startDate и filters; пустой объект filters оставляет все варианты.")
		return
	}
	now := s.now()
	date, err := time.ParseInLocation(time.DateOnly, input.StartDate, s.location)
	if err != nil || input.StartDate < now.Format(time.DateOnly) || input.StartDate > now.AddDate(0, 0, 8).Format(time.DateOnly) || date.Year() < 1970 {
		s.fail(w, http.StatusBadRequest, "validation", "Начало меню — от сегодняшней даты до восьми дней вперёд.")
		return
	}
	selections, err := nutrition.SuggestWeek(input.StartDate, *input.Filters)
	if err != nil {
		status, code := http.StatusBadRequest, "validation"
		if errors.Is(err, nutrition.ErrNoCandidates) {
			status, code = http.StatusUnprocessableEntity, "no_recipes"
		}
		s.fail(w, status, code, err.Error())
		return
	}
	ids := make([]string, len(selections))
	for i, selection := range selections {
		ids[i] = selection.RecipeID
	}
	shopping, err := nutrition.ShoppingList(ids)
	if err != nil {
		// IDs here came exclusively from the fixed catalogue; a failure is an
		// internal contract violation, not a reason to save a partial menu.
		s.failure(w, r, err)
		return
	}
	s.json(w, http.StatusOK, struct {
		Selections   []nutrition.Selection    `json:"selections"`
		ShoppingList []nutrition.ShoppingItem `json:"shoppingList"`
	}{selections, shopping})
}
