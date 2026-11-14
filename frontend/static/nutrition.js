import { request } from './api.js';
import { escapeHTML as esc, addDays, dateLabel, weekLabel } from './format.js';
import { icon } from './icons.js';
import { downloadBlob } from './features.js';
import { matchingRecipes, shoppingFor, quantityLabel, mealLabels } from './nutrition-data.js';

const errorBox = '<div id="form-error" class="form-error" role="alert"></div>';
const allergenNote = 'Фильтр исключает указанные ингредиенты, но не гарантирует отсутствие следов аллергенов. Проверьте упаковки и условия приготовления.';
const readFilters = form => {
  const data = new FormData(form);
  return { diet: data.get('diet'), maxMinutes: Number(data.get('maxMinutes')), meal: data.get('meal'), excludedAllergens: data.getAll('allergen') };
};

export function createNutrition(ctx) {
  const state = ctx.getState;
  let catalog = null, busy = false, error = '', generation = 0;
  let filters = { diet: 'balanced', maxMinutes: 30, excludedAllergens: [], meal: '' };
  let menuStart = '', command = null, shoppingText = '';
  function reset() { generation++; catalog = null; busy = false; error = ''; filters = { diet: 'balanced', maxMinutes: 30, excludedAllergens: [], meal: '' }; menuStart = ''; command = null; shoppingText = ''; }
  async function load() {
    if (catalog || busy || !state().sessionBinding) return;
    const id = ++generation, binding = state().sessionBinding;
    busy = true; error = ''; ctx.render();
    try {
      const result = await request('/api/nutrition/catalog');
      if (id !== generation || binding !== state().sessionBinding) return;
      catalog = result;
    } catch (problem) { if (id === generation) { error = problem.message; if (problem.code === 'session_changed') ctx.onError(problem); } }
    finally { if (id === generation) { busy = false; ctx.render(); } }
  }
  const recipeByID = id => catalog?.recipes.find(recipe => recipe.id === id);
  const labelAllergens = ids => ids.map(id => catalog.allergens.find(item => item.id === id)?.label || id).join(', ');
  const menuEntries = () => (state().mealSelections || []).filter(selection => selection.date >= menuStart && selection.date <= addDays(menuStart, 6));

  function menuMarkup(selections, start, interactive) {
    return `<div class="meal-week">${Array.from({ length: 7 }, (_, index) => addDays(start, index)).map(date => `<section class="meal-day"><h3>${dateLabel(date, { weekday: 'short', day: 'numeric', month: 'short' })}${date === state().today ? '<span>сегодня</span>' : ''}</h3>${Object.entries(mealLabels).map(([slot, label]) => {
      const selection = selections.find(item => item.date === date && item.slot === slot), recipe = selection && recipeByID(selection.recipeId);
      return `<div class="meal-slot"><small>${label}</small>${recipe ? `<${interactive ? 'button' : 'p'} ${interactive ? `data-action="nutrition-recipe" data-id="${esc(recipe.id)}" data-date="${date}" data-slot="${slot}"` : ''}>${esc(recipe.title)}</${interactive ? 'button' : 'p'}>${interactive ? `<button class="meal-remove" data-action="nutrition-remove" data-date="${date}" data-slot="${slot}" aria-label="Убрать ${label.toLowerCase()} за ${dateLabel(date)}">${icon('close')}</button>` : ''}` : '<p class="meal-unselected">Ещё не выбран</p>'}</div>`;
    }).join('')}</section>`).join('')}</div>`;
  }

  function view() {
    if (!menuStart) menuStart = state().today || '';
    const heading = '<div class="page-heading"><div><p class="eyebrow">ПРОСТЫЕ РЕШЕНИЯ НА КАЖДЫЙ ДЕНЬ</p><h1>Питание в вашем ритме</h1><p class="subtitle">Идеи блюд, понятное меню и список продуктов — без ручной сборки по разным заметкам.</p></div></div>';
    if (!catalog) return `${heading}<div class="empty-state">${icon('plate')}<h2>${busy ? 'Собираем идеи блюд…' : 'Подберите блюда под свой день'}</h2><p>${error ? esc(error) : 'Рецепты с составом, шагами приготовления и фильтрами предпочтений.'}</p>${busy ? '' : '<button class="button primary" data-action="nutrition-load">Загрузить рецепты</button>'}</div>`;
    const recipes = matchingRecipes(catalog.recipes, filters), selections = menuStart ? menuEntries() : [];
    const historyFrom = (state().mealSelections || []).reduce((first, item) => item.date < first ? item.date : first, state().historyEditableFrom || state().today);
    return `${heading}<section class="nutrition-hero panel"><div><span class="pill green">ПЛАН ПИТАНИЯ</span><h2>Меньше решений.<br>Больше разнообразия.</h2><p>Начните с привычных продуктов. Овощи и фрукты, цельнозерновые и источник белка помогают собрать тарелку; состав и размер порции выбираете вы.</p><a class="text-link" href="https://www.canada.ca/en/health-canada/services/food-guide/eating-support/cooking/make-healthy-meals-plate.html" target="_blank" rel="noopener noreferrer">Ориентир Health Canada ${icon('arrow')}</a></div><div class="food-plate" role="img" aria-label="Ориентир состава тарелки: половина овощи и фрукты, четверть цельнозерновые, четверть белковые продукты"><span>½<small>овощи и фрукты</small></span><span>¼<small>цельнозерновые</small></span><span>¼<small>белковые продукты</small></span></div></section>
      <section class="panel nutrition-filters"><h2>Что подойдёт вам?</h2><form id="nutrition-filter-form"><div class="nutrition-filter-grid"><label class="field"><span class="field-label">Предпочтения</span><select name="diet">${catalog.diets.map(item => `<option value="${esc(item.id)}" ${filters.diet === item.id ? 'selected' : ''}>${esc(item.label)}</option>`).join('')}</select></label><label class="field"><span class="field-label">Время на готовку</span><select name="maxMinutes">${[[15, 'До 15 минут'], [30, 'До 30 минут'], [60, 'До часа'], [0, 'Без ограничения']].map(([value, label]) => `<option value="${value}" ${filters.maxMinutes === value ? 'selected' : ''}>${label}</option>`).join('')}</select></label><label class="field"><span class="field-label">Приём пищи</span><select name="meal"><option value="">Любой</option>${Object.entries(mealLabels).map(([id, label]) => `<option value="${id}" ${filters.meal === id ? 'selected' : ''}>${label}</option>`).join('')}</select></label></div><details class="allergen-picker"><summary>Исключить ингредиенты · ${filters.excludedAllergens.length} групп</summary><fieldset><legend class="sr-only">Исключаемые аллергены</legend>${catalog.allergens.map(item => `<label><input type="checkbox" name="allergen" value="${esc(item.id)}" ${filters.excludedAllergens.includes(item.id) ? 'checked' : ''}>${esc(item.label)}</label>`).join('')}</fieldset><p>${allergenNote}</p></details><div class="feature-toolbar"><button type="submit" class="button primary">Подобрать блюда ${icon('arrow')}</button><button type="button" class="button outline" data-action="nutrition-week">Собрать меню на 7 дней</button></div></form><p class="small-print">Подбор учитывает выбранные предпочтения, время и исключения. Это идеи блюд, не индивидуальный расчёт калорий или лечебный рацион.</p></section>
      <div class="section-title nutrition-section-title"><h2>Идеи для вашего меню <span class="meta">${recipes.length} вариантов</span></h2></div>
      ${recipes.length ? `<div class="recipe-grid">${recipes.map(recipe => `<article class="recipe-card"><div class="recipe-visual tone-${recipe.meals.includes('breakfast') ? 'breakfast' : recipe.diets.includes('vegan') ? 'plant' : 'dinner'}">${icon(recipe.diets.includes('vegan') ? 'leaf' : 'plate')}<span>${recipe.prepMinutes} мин</span></div><div class="recipe-copy"><p class="eyebrow">${recipe.meals.map(meal => mealLabels[meal]).join(' · ')}</p><h3>${esc(recipe.title)}</h3><p>${recipe.ingredients.slice(0, 3).map(item => esc(item.name)).join(' · ')}</p><small>${recipe.allergens.length ? 'Содержит: ' + esc(labelAllergens(recipe.allergens)) : 'В рецепте не отмечены перечисленные аллергены'}</small><button class="button ghost" data-action="nutrition-recipe" data-id="${esc(recipe.id)}">Рецепт и выбор в меню ${icon('arrow')}</button></div></article>`).join('')}</div>` : '<div class="empty-state"><h2>Таких сочетаний пока нет в каталоге</h2><p>Посмотрите другие приёмы пищи или время готовки. Ваши исключения сохранятся.</p></div>'}
      <section class="nutrition-plan"><div class="section-title"><div><h2>Ваше сохранённое меню</h2><p class="small-print">Выбранные блюда — план, а не отметка о том, что вы съели.</p></div><button class="button outline" data-action="nutrition-shopping" ${selections.length ? '' : 'disabled'}>${icon('book')} Список продуктов</button></div><label class="meal-date-picker"><span>Показать 7 дней начиная с</span><input type="date" id="meal-week-start" min="${historyFrom}" max="${addDays(state().today, 8)}" value="${menuStart}"></label>${menuMarkup(selections, menuStart, true)}</section>
      <p class="small-print">Рецепты — авторские примеры сочетаний продуктов. Количества указаны для приготовления ${catalog.recipes.every(recipe => recipe.servings === 1) ? 'одной порции' : 'указанного числа порций'}, а не как персональная суточная норма. ${allergenNote}</p>`;
  }

  function recipeDialog(id, date = '', slot = '') {
    const recipe = recipeByID(id); if (!recipe) return;
    command = { revision: state().revision, binding: state().sessionBinding, recipeID: id };
    ctx.openDialog(`${ctx.dialogHeader(recipe.title, `${recipe.prepMinutes} минут · ${recipe.servings} порция`)}<div class="recipe-detail"><h3>Что понадобится</h3><ul class="ingredient-list">${recipe.ingredients.map(item => `<li><span>${esc(item.name)}</span><strong>${esc(item.amount)}</strong></li>`).join('')}</ul><h3>Как приготовить</h3><ol class="recipe-steps">${recipe.steps.map(step => `<li>${esc(step)}</li>`).join('')}</ol><p class="small-print">${recipe.allergens.length ? 'Содержит: ' + esc(labelAllergens(recipe.allergens)) + '. ' : ''}${allergenNote}</p></div>${state().profile ? `<form id="recipe-save-form"><div class="two-col"><label class="field"><span class="field-label">Дата в меню</span><input type="date" name="date" min="${state().historyEditableFrom}" max="${addDays(state().today, 14)}" value="${date || state().today}" required></label><label class="field"><span class="field-label">Приём пищи</span><select name="slot">${recipe.meals.map(meal => `<option value="${meal}" ${meal === slot ? 'selected' : ''}>${mealLabels[meal]}</option>`).join('')}</select></label></div><p class="small-print">Если на эту дату и приём пищи уже выбрано блюдо, оно заменится после сохранения.</p>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Закрыть</button><button type="submit" class="button primary">Сохранить выбор ${icon('check')}</button></div></form>` : '<div class="dialog-actions"><button class="button primary" data-action="profile">Создать профиль для своего меню</button></div>'}`);
  }

  function weeklyDialog() {
    if (!state().profile) { ctx.profileDialog(); return; }
    command = { revision: state().revision, binding: state().sessionBinding, filters: { ...filters, excludedAllergens: [...filters.excludedAllergens], meal: '' } };
    const start = menuStart >= state().today ? menuStart : state().today;
    ctx.openDialog(`${ctx.dialogHeader('Собрать неделю питания', 'Подготовим завтрак, обед и ужин на каждый день. Вы увидите всё меню перед сохранением.')}<p class="panel-description">${esc(catalog.diets.find(item => item.id === filters.diet)?.label)} · ${filters.maxMinutes ? 'до ' + filters.maxMinutes + ' мин' : 'любое время готовки'} · исключений: ${filters.excludedAllergens.length}. Фильтр конкретного приёма пищи здесь не применяется: нужны все три.</p><form id="nutrition-week-form"><label class="field"><span class="field-label">Первый день меню</span><input type="date" name="startDate" min="${state().today}" max="${addDays(state().today, 8)}" value="${start}" required></label>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Отмена</button><button type="submit" class="button primary">Посмотреть меню</button></div></form>`);
  }

  async function handleClick(action, button) {
    if (!action.startsWith('nutrition-')) return false;
    if (action === 'nutrition-open') { ctx.closeDialog(); ctx.navigate('nutrition'); await load(); }
    if (action === 'nutrition-load') await load();
    if (action === 'nutrition-recipe') recipeDialog(button.dataset.id, button.dataset.date, button.dataset.slot);
    if (action === 'nutrition-week') { const form = button.closest?.('#nutrition-filter-form'); if (form) { filters = readFilters(form); ctx.render(); } weeklyDialog(); }
    if (action === 'nutrition-remove') {
      command = { revision: state().revision, binding: state().sessionBinding, date: button.dataset.date, slot: button.dataset.slot };
      ctx.openDialog(`${ctx.dialogHeader('Убрать блюдо из меню?', `${dateLabel(command.date)} · ${mealLabels[command.slot]}`)}<form id="meal-remove-form">${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Оставить</button><button type="submit" class="button danger">Убрать из меню</button></div></form>`);
    }
    if (action === 'nutrition-shopping') {
      try {
        const items = shoppingFor(menuEntries(), catalog.recipes);
        shoppingText = `Ритм · продукты на ${weekLabel(menuStart)}\n\n${items.map(item => `□ ${item.name}: ${quantityLabel(item)}`).join('\n')}\n\n${allergenNote}\n`;
        ctx.openDialog(`${ctx.dialogHeader('Список продуктов', `${weekLabel(menuStart)} · по вашему сохранённому меню`)}<p class="small-print">Количество повторов указано перед порцией: например, 2 × 100 г. Отметки списка действуют до закрытия окна.</p><div class="shopping-list">${items.map(item => `<label><input type="checkbox"><span>${esc(item.name)}<small>${esc(quantityLabel(item))}</small></span></label>`).join('')}</div><div class="dialog-actions"><button class="button outline" data-action="close">Закрыть</button><button class="button primary" data-action="nutrition-download">${icon('download')} Скачать список</button></div>`);
      } catch (problem) { ctx.toast(problem.message); }
    }
    if (action === 'nutrition-download' && shoppingText) downloadBlob(new Blob([shoppingText], { type: 'text/plain;charset=utf-8' }), `rhythm-products-${menuStart}.txt`);
    return true;
  }

  async function handleSubmit(event) {
    const form = event.target;
    if (!['nutrition-filter-form', 'recipe-save-form', 'nutrition-week-form', 'nutrition-confirm-form', 'meal-remove-form'].includes(form.id)) return false;
    event.preventDefault(); if (ctx.isBusy()) return true;
    const data = new FormData(form), box = form.querySelector('#form-error'); if (box) box.textContent = '';
    if (form.id === 'nutrition-filter-form') {
      filters = readFilters(form);
      ctx.render(); document.querySelector('.nutrition-section-title')?.scrollIntoView({ block: 'start' }); return true;
    }
    try {
      if (!command || command.binding !== state().sessionBinding || command.revision !== state().revision) throw new Error('Меню или дневник изменились. Закройте окно и проверьте свежие данные перед сохранением.');
      if (form.id === 'recipe-save-form') {
        await ctx.mutate('/api/meals', { revision: command.revision, selection: { date: data.get('date'), slot: data.get('slot'), recipeId: command.recipeID } }, 'POST', 'Блюдо сохранено в вашем меню.');
      } else if (form.id === 'meal-remove-form') {
        await ctx.mutate('/api/meals', { revision: command.revision, date: command.date, slot: command.slot }, 'DELETE', 'Блюдо убрано из меню.');
      } else if (form.id === 'nutrition-week-form') {
        const id = ++generation, current = command, submit = form.querySelector('[type=submit]'); submit.disabled = true;
        try {
          const preview = await request('/api/nutrition/preview', { startDate: data.get('startDate'), filters: current.filters });
          if (id !== generation || current.binding !== state().sessionBinding || !form.isConnected || !ctx.dialogOpen()) return true;
          if (current.revision !== state().revision) throw new Error('История обновилась. Соберите меню ещё раз.');
          command = { ...current, selections: preview.selections, startDate: data.get('startDate') };
          const replacing = (state().mealSelections || []).filter(item => preview.selections.some(next => next.date === item.date && next.slot === item.slot)).length;
          ctx.openDialog(`${ctx.dialogHeader('Ваша неделя на рассмотрение', `${weekLabel(command.startDate)} · ${preview.selections.length} выбранных блюд`)}<p class="panel-description">${replacing ? `После подтверждения заменится ранее выбранных блюд: ${replacing}. ` : ''}Просмотрите меню. После сохранения любое блюдо можно заменить отдельно через каталог.</p>${menuMarkup(preview.selections, command.startDate, false)}<p class="small-print">${allergenNote}</p><form id="nutrition-confirm-form">${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Пока не сохранять</button><button type="submit" class="button primary">Сохранить меню на неделю ${icon('check')}</button></div></form>`);
        } finally { submit.disabled = false; }
      } else if (form.id === 'nutrition-confirm-form' && command.selections) {
        const savedStart = command.startDate;
        if (await ctx.mutate('/api/meals/batch', { revision: command.revision, selections: command.selections }, 'POST', 'Меню на неделю сохранено.')) { menuStart = savedStart; ctx.render(); }
      }
    } catch (problem) { if (problem.code === 'session_changed') ctx.onError(problem); else if (box?.isConnected) box.textContent = problem.message; }
    return true;
  }
  function handleChange(event) {
    if (event.target.id !== 'meal-week-start' || !event.target.value || !event.target.checkValidity()) return;
    menuStart = event.target.value; ctx.render(); document.querySelector('#meal-week-start')?.focus();
  }
  return { view, load, handleClick, handleSubmit, handleChange, reset };
}
