import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createNutrition } from '../static/nutrition.js';
import { resetSessionBinding } from '../static/api.js';

const recipe = { id: 'oat-v1', title: 'Каша', meals: ['breakfast'], diets: ['balanced', 'vegetarian', 'vegan'], prepMinutes: 10, servings: 1, allergens: ['gluten'], ingredients: [{ name: 'Овёс', amount: '50 г' }], steps: ['Приготовить.'] };
const catalog = { recipes: [recipe], diets: [{ id: 'balanced', label: 'Любые продукты' }, { id: 'vegan', label: 'Растительные' }], allergens: [{ id: 'milk', label: 'Молоко' }] };
function harness() {
  let state = { profile: {}, revision: 1, sessionBinding: 'a', today: '2026-10-17', historyEditableFrom: '2026-07-20', mealSelections: [] }, dialog = '', writes = [];
  const controller = createNutrition({ getState: () => state, render() {}, openDialog: html => { dialog = html; }, dialogHeader: () => '', closeDialog() {}, dialogOpen: () => true, toast() {}, navigate() {}, profileDialog() {}, onError() {}, isBusy: () => false, mutate: async (...args) => { writes.push(args); return true; } });
  return { controller, get: () => state, set: next => { state = next; }, dialog: () => dialog, writes };
}
const event = (id, values) => ({ preventDefault() {}, target: { id, values, isConnected: true, querySelector: () => ({ textContent: '', isConnected: true }) } });
const ok = body => ({ ok: true, json: async () => body });
async function globals(fn) {
  const old = { FormData: globalThis.FormData, document: globalThis.document, fetch: globalThis.fetch };
  globalThis.FormData = class { constructor(form) { this.values = form.values; } get(key) { return this.values[key] ?? null; } getAll(key) { return this.values[key] || []; } };
  globalThis.document = { querySelector: () => null }; resetSessionBinding();
  try { await fn(); } finally { Object.assign(globalThis, old); resetSessionBinding(); }
}

test('weekly preview uses visible unsaved filters and never persists without confirmation', async () => globals(async () => {
  const h = harness(), calls = [];
  const selections = [{ date: h.get().today, slot: 'breakfast', recipeId: recipe.id }];
  globalThis.fetch = async (path, options) => { calls.push({ path, options }); return ok(path.includes('catalog') ? catalog : { selections }); };
  await h.controller.load();
  await h.controller.handleClick('nutrition-week', { closest: () => ({ values: { diet: 'vegan', maxMinutes: '15', meal: 'breakfast', allergen: ['milk'] } }) });
  await h.controller.handleSubmit(event('nutrition-week-form', { startDate: h.get().today }));
  assert.deepEqual(JSON.parse(calls[1].options.body).filters, { diet: 'vegan', maxMinutes: 15, meal: '', excludedAllergens: ['milk'] });
  assert.equal(h.writes.length, 0); assert.match(h.dialog(), /Сохранить меню на неделю/);
  await h.controller.handleSubmit(event('nutrition-confirm-form', {}));
  assert.equal(h.writes.length, 1); assert.deepEqual(h.writes[0][1], { revision: 1, selections });
}));

test('stale weekly consent and changed identity cannot save the prepared menu', async () => globals(async () => {
  const h = harness(); globalThis.fetch = async path => ok(path.includes('catalog') ? catalog : { selections: [{ date: h.get().today, slot: 'breakfast', recipeId: recipe.id }] });
  await h.controller.load(); await h.controller.handleClick('nutrition-week', {});
  await h.controller.handleSubmit(event('nutrition-week-form', { startDate: h.get().today }));
  h.set({ ...h.get(), revision: 2 }); await h.controller.handleSubmit(event('nutrition-confirm-form', {})); assert.equal(h.writes.length, 0);
  h.controller.reset(); h.set({ ...h.get(), sessionBinding: 'b' }); await h.controller.handleSubmit(event('nutrition-confirm-form', {})); assert.equal(h.writes.length, 0);
}));

test('late preview cannot overwrite a different dialog or an account reset', async () => globals(async () => {
  const h = harness(); let release;
  globalThis.fetch = async path => path.includes('catalog') ? ok(catalog) : new Promise(resolve => { release = resolve; });
  await h.controller.load(); await h.controller.handleClick('nutrition-week', {});
  const form = event('nutrition-week-form', { startDate: h.get().today }); const pending = h.controller.handleSubmit(form);
  form.target.isConnected = false; release(ok({ selections: [] })); await pending; assert.doesNotMatch(h.dialog(), /Ваша неделя на рассмотрение/);
}));
