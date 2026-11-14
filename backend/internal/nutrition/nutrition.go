// Package nutrition provides a small, deterministic catalogue of ordinary meal
// ideas. It does not calculate energy needs, nutrients or therapeutic diets.
// Selection is separate from a record of what a person actually ate.
package nutrition

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

const (
	PlateGuidance      = "Ориентир тарелки: половина — овощи и фрукты, четверть — цельнозерновые продукты, четверть — белковые продукты. Чаще выбирайте растительные источники белка. Количества в рецептах — пример одной порции, а не индивидуальная норма."
	AllergenNotice     = "Фильтр исключает указанные ингредиенты каталога, но не гарантирует отсутствие аллергенов или их следов. Проверяйте состав и предупреждения на каждой упаковке; состав готовых продуктов может отличаться."
	RecipeNotice       = "Это авторские идеи обычных блюд по группам продуктов, а не проверенный индивидуальный рацион. Время приготовления ориентировочное; готовые крупы и белковые продукты указаны в ингредиентах."
	MaxShoppingRecipes = 93
)

type Ingredient struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
}

type Recipe struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Meals       []string     `json:"meals"`
	Diets       []string     `json:"diets"`
	PrepMinutes int          `json:"prepMinutes"`
	Servings    int          `json:"servings"`
	Ingredients []Ingredient `json:"ingredients"`
	Steps       []string     `json:"steps"`
	Allergens   []string     `json:"allergens"`
	Components  []string     `json:"components"`
}

type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Filters struct {
	Diet              string   `json:"diet"`
	MaxMinutes        int      `json:"maxMinutes"` // 0 means no preparation-time limit.
	ExcludedAllergens []string `json:"excludedAllergens"`
	Meal              string   `json:"meal"` // Empty accepts all meal slots.
}

type Selection struct {
	Date     string `json:"date"`
	Slot     string `json:"slot"`
	RecipeID string `json:"recipeId"`
}

type ShoppingQuantity struct {
	Amount string `json:"amount"`
	Count  int    `json:"count"`
}

type ShoppingItem struct {
	Name       string             `json:"name"`
	Quantities []ShoppingQuantity `json:"quantities"`
}

var ErrNoCandidates = errors.New("для одного из приёмов пищи нет подходящих блюд; измените фильтры")

func Sources() []Source {
	return []Source{
		{"Health Canada: ориентир сбалансированной тарелки", "https://www.canada.ca/en/health-canada/services/food-guide/eating-support/cooking/make-healthy-meals-plate.html"},
		{"European Commission: 14 групп аллергенов и маркировка", "https://food.ec.europa.eu/food-safety/campaign-2026/allergies_en"},
	}
}

func Diets() []Option {
	return []Option{{"balanced", "Без ограничения животных продуктов"}, {"vegetarian", "Вегетарианский"}, {"vegan", "Растительный"}}
}

func Meals() []Option {
	return []Option{{"breakfast", "Завтрак"}, {"lunch", "Обед"}, {"dinner", "Ужин"}}
}

// The tokens describe the EU's 14 declared allergen groups, not a claim that a
// recipe or an ingredient from a particular manufacturer is allergy-safe.
func Allergens() []Option {
	return []Option{
		{"gluten", "Злаки, содержащие глютен"}, {"crustaceans", "Ракообразные"},
		{"eggs", "Яйца"}, {"fish", "Рыба"}, {"peanuts", "Арахис"},
		{"soy", "Соя"}, {"milk", "Молоко"}, {"nuts", "Орехи"},
		{"celery", "Сельдерей"}, {"mustard", "Горчица"}, {"sesame", "Кунжут"},
		{"sulphites", "Диоксид серы и сульфиты"}, {"lupin", "Люпин"}, {"molluscs", "Моллюски"},
	}
}

func cloneRecipe(recipe Recipe) Recipe {
	recipe.Meals = slices.Clone(recipe.Meals)
	recipe.Diets = slices.Clone(recipe.Diets)
	recipe.Ingredients = slices.Clone(recipe.Ingredients)
	recipe.Steps = slices.Clone(recipe.Steps)
	recipe.Allergens = append([]string{}, recipe.Allergens...)
	recipe.Components = slices.Clone(recipe.Components)
	return recipe
}

// Catalog returns owned data; callers cannot change later filtering or persisted
// recipe validation by modifying a returned recipe or one of its nested slices.
func Catalog() []Recipe {
	result := make([]Recipe, len(recipes))
	for i, recipe := range recipes {
		result[i] = cloneRecipe(recipe)
	}
	return result
}

func Find(id string) (Recipe, bool) {
	for _, recipe := range recipes {
		if recipe.ID == id {
			return cloneRecipe(recipe), true
		}
	}
	return Recipe{}, false
}

func ValidRecipeID(id string) bool {
	return slices.ContainsFunc(recipes, func(recipe Recipe) bool { return recipe.ID == id })
}

func AllowsSlot(id, slot string) bool {
	return slices.ContainsFunc(recipes, func(recipe Recipe) bool { return recipe.ID == id && slices.Contains(recipe.Meals, slot) })
}

func validateFilters(filters Filters) error {
	if filters.Diet != "" && !slices.ContainsFunc(Diets(), func(option Option) bool { return option.ID == filters.Diet }) {
		return errors.New("неизвестный тип питания")
	}
	if filters.Meal != "" && !slices.ContainsFunc(Meals(), func(option Option) bool { return option.ID == filters.Meal }) {
		return errors.New("неизвестный приём пищи")
	}
	if filters.MaxMinutes < 0 || filters.MaxMinutes > 180 {
		return errors.New("время приготовления — от 1 до 180 минут; 0 снимает ограничение")
	}
	if len(filters.ExcludedAllergens) > 14 {
		return errors.New("можно исключить не больше 14 групп аллергенов")
	}
	seen := make(map[string]bool, len(filters.ExcludedAllergens))
	for _, value := range filters.ExcludedAllergens {
		if seen[value] || !slices.ContainsFunc(Allergens(), func(option Option) bool { return option.ID == value }) {
			return errors.New("неизвестная или повторная группа аллергенов")
		}
		seen[value] = true
	}
	return nil
}

// Filter never silently relaxes preferences. No candidates is a valid empty
// result; consumers can show an explicit empty state instead of a substitute.
func Filter(filters Filters) ([]Recipe, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	diet := filters.Diet
	if diet == "" {
		diet = "balanced"
	}
	result := make([]Recipe, 0, len(recipes))
	for _, recipe := range recipes {
		if !slices.Contains(recipe.Diets, diet) || (filters.Meal != "" && !slices.Contains(recipe.Meals, filters.Meal)) || (filters.MaxMinutes > 0 && recipe.PrepMinutes > filters.MaxMinutes) {
			continue
		}
		if slices.ContainsFunc(recipe.Allergens, func(allergen string) bool { return slices.Contains(filters.ExcludedAllergens, allergen) }) {
			continue
		}
		result = append(result, cloneRecipe(recipe))
	}
	return result, nil
}

// SuggestWeek offers 21 selections in date/meal order. Rotation is deterministic
// for a start date, so a preview can be reviewed before explicit persistence.
// It does not claim complete nutrition or adapt food intake to exercise load.
func SuggestWeek(startDate string, filters Filters) ([]Selection, error) {
	start, err := time.Parse(time.DateOnly, startDate)
	if err != nil || start.Year() < 1970 || start.AddDate(0, 0, 6).Year() > 9999 {
		return nil, errors.New("дата начала должна иметь формат ГГГГ-ММ-ДД и начинаться не раньше 1970 года")
	}
	if filters.Meal != "" {
		return nil, errors.New("для меню на неделю нужны все три приёма пищи")
	}
	filtered, err := Filter(filters)
	if err != nil {
		return nil, err
	}
	meals := Meals()
	byMeal := make([][]Recipe, len(meals))
	for i, meal := range meals {
		for _, recipe := range filtered {
			if slices.Contains(recipe.Meals, meal.ID) {
				byMeal[i] = append(byMeal[i], recipe)
			}
		}
		if len(byMeal[i]) == 0 {
			return nil, fmt.Errorf("%s: %w", meal.Label, ErrNoCandidates)
		}
	}
	result := make([]Selection, 0, 21)
	rotation := int(start.Unix() / 86400)
	for day := range 7 {
		date := start.AddDate(0, 0, day).Format(time.DateOnly)
		for i, meal := range meals {
			options := byMeal[i]
			result = append(result, Selection{Date: date, Slot: meal.ID, RecipeID: options[(rotation+day)%len(options)].ID})
		}
	}
	return result, nil
}

// ShoppingList preserves each quantity as written and counts repetitions. It
// does not add grams to pieces, scale a personal serving or invent conversions.
// The same recipe ID may occur on several days; each occurrence contributes.
func ShoppingList(recipeIDs []string) ([]ShoppingItem, error) {
	if len(recipeIDs) > MaxShoppingRecipes {
		return nil, errors.New("список покупок ограничен 93 выбранными блюдами")
	}
	grouped := make(map[string]map[string]int)
	for _, id := range recipeIDs {
		recipe, ok := Find(id)
		if !ok {
			return nil, errors.New("в списке покупок есть неизвестный рецепт")
		}
		for _, ingredient := range recipe.Ingredients {
			if grouped[ingredient.Name] == nil {
				grouped[ingredient.Name] = make(map[string]int)
			}
			grouped[ingredient.Name][ingredient.Amount]++
		}
	}
	result := make([]ShoppingItem, 0, len(grouped))
	for name, quantities := range grouped {
		item := ShoppingItem{Name: name, Quantities: make([]ShoppingQuantity, 0, len(quantities))}
		for amount, count := range quantities {
			item.Quantities = append(item.Quantities, ShoppingQuantity{Amount: amount, Count: count})
		}
		sort.Slice(item.Quantities, func(i, j int) bool { return item.Quantities[i].Amount < item.Quantities[j].Amount })
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
