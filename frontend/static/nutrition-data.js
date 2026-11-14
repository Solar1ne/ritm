export const mealLabels = Object.freeze({ breakfast: 'Завтрак', lunch: 'Обед', dinner: 'Ужин' });

export function matchingRecipes(recipes, filters) {
  return recipes.filter(recipe => recipe.diets.includes(filters.diet)
    && (!filters.maxMinutes || recipe.prepMinutes <= filters.maxMinutes)
    && (!filters.meal || recipe.meals.includes(filters.meal))
    && !(filters.excludedAllergens || []).some(allergen => recipe.allergens.includes(allergen)));
}

// Amounts are human-readable catalogue quantities, not interchangeable units.
// Multiplicity is retained instead of inventing arithmetic between g, ml and pieces.
export function shoppingFor(selections, recipes) {
  const byID = new Map(recipes.map(recipe => [recipe.id, recipe])), items = new Map();
  for (const selection of selections) {
    const recipe = byID.get(selection.recipeId);
    if (!recipe) throw new Error('В меню есть неизвестный рецепт. Обновите каталог перед составлением списка.');
    for (const ingredient of recipe.ingredients) {
      if (!items.has(ingredient.name)) items.set(ingredient.name, new Map());
      const quantities = items.get(ingredient.name);
      quantities.set(ingredient.amount, (quantities.get(ingredient.amount) || 0) + 1);
    }
  }
  return [...items.entries()].sort(([a], [b]) => a.localeCompare(b, 'ru')).map(([name, quantities]) => ({ name, quantities: [...quantities.entries()].map(([amount, count]) => ({ amount, count })) }));
}

export function quantityLabel(item) {
  return item.quantities.map(({ amount, count }) => count === 1 ? amount : `${count} × ${amount}`).join(' + ');
}
