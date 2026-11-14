import { test } from 'node:test';
import assert from 'node:assert/strict';
import { matchingRecipes, shoppingFor, quantityLabel } from '../static/nutrition-data.js';
const recipes = [
  { id: 'a', diets: ['balanced', 'vegetarian', 'vegan'], prepMinutes: 15, meals: ['breakfast'], allergens: ['nuts'], ingredients: [{ name: 'Яблоко', amount: '1 шт.' }] },
  { id: 'b', diets: ['balanced', 'vegetarian'], prepMinutes: 20, meals: ['breakfast'], allergens: ['milk'], ingredients: [{ name: 'Яблоко', amount: '100 г' }, { name: 'Йогурт', amount: '100 г' }] },
  { id: 'c', diets: ['balanced', 'vegetarian', 'vegan'], prepMinutes: 10, meals: ['lunch', 'dinner'], allergens: [], ingredients: [{ name: 'Яблоко', amount: '1 шт.' }] },
];
test('nutrition filters combine diet, cooking time, meal and every excluded allergen', () => {
  assert.deepEqual(matchingRecipes(recipes, { diet: 'vegan', maxMinutes: 15, meal: '', excludedAllergens: ['nuts', 'milk'] }).map(r => r.id), ['c']);
  assert.deepEqual(matchingRecipes(recipes, { diet: 'vegan', maxMinutes: 0, meal: 'breakfast', excludedAllergens: ['nuts'] }), []);
});
test('shopping list counts repeated servings without adding unlike quantities', () => {
  const items = shoppingFor([{ recipeId: 'a' }, { recipeId: 'a' }, { recipeId: 'b' }], recipes);
  const apple = items.find(item => item.name === 'Яблоко');
  assert.equal(quantityLabel(apple), '2 × 1 шт. + 100 г');
  assert.equal(recipes[0].ingredients[0].amount, '1 шт.');
});
test('unknown recipes fail explicitly rather than silently omitting shopping items', () => {
  assert.throws(() => shoppingFor([{ recipeId: 'missing' }], recipes), /неизвестный рецепт/);
  assert.deepEqual(shoppingFor([], recipes), []);
});
