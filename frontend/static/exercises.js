import { escapeHTML as esc } from './format.js';
import { EXERCISES, MUSCLE_GROUPS, EQUIPMENT } from './exercises-data.js';
import { exerciseDemoView, exerciseIllustration, mountExerciseDemo } from './exercise-demo.js';

const normalized = value => String(value ?? '').toLocaleLowerCase('ru').replaceAll('ё', 'е').trim();

// Return catalog references without changing the catalog or the caller's filters.
export function filterExercises(exercises = EXERCISES, filters = {}) {
  const query = normalized(filters.query);
  return exercises.filter(exercise => (!filters.muscle || exercise.muscles.includes(filters.muscle))
    && (!filters.equipment || exercise.equipment.includes(filters.equipment))
    && (!query || [exercise.name, ...exercise.aliases].some(value => normalized(value).includes(query))));
}

const tags = exercise => `<ul class="exercise-tags" aria-label="Мышцы и инвентарь">${[
  ...exercise.muscles.map(key => MUSCLE_GROUPS[key]),
  ...exercise.equipment.map(key => EQUIPMENT[key]),
].filter(Boolean).map(label => `<li>${esc(label)}</li>`).join('')}</ul>`;

function cards(exercises) {
  if (!exercises.length) return `<div class="exercise-empty"><h2>Пока ничего не нашлось</h2><p>Попробуйте другое название или уберите фильтры.</p><button type="button" class="button outline" data-action="exercise-clear">Сбросить фильтры</button></div>`;
  return `<ul class="exercise-grid" role="list">${exercises.map(exercise => `<li><article class="exercise-card">
    <div class="exercise-card-illustration" aria-hidden="true">${exerciseIllustration(exercise.id)}</div>
    <div class="exercise-card-copy"><h2>${esc(exercise.name)}</h2>${tags(exercise)}<p>${esc(exercise.summary)}</p>
    <button type="button" class="button outline" data-action="exercise-open" data-id="${esc(exercise.id)}" aria-haspopup="dialog" aria-label="${esc(`Показать технику: ${exercise.name}`)}">Смотреть технику <span aria-hidden="true">↗</span></button></div>
  </article></li>`).join('')}</ul>`;
}

function sourceURL(value) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password ? url.href : '';
  } catch { return ''; }
}

const list = items => items.map(item => `<li>${esc(item)}</li>`).join('');

// Body only: the app supplies its shared, escaped dialog heading and close button.
export function exerciseDetailView(exercise) {
  const sources = exercise.sources.map(source => ({ ...source, href: sourceURL(source.url) })).filter(source => source.href);
  return `<article class="exercise-detail">${tags(exercise)}<p class="exercise-detail-summary">${esc(exercise.summary)}</p>
    ${exerciseDemoView(exercise.id)}
    <p class="exercise-schematic-note">Схематичная анимация показывает основные фазы движения. Это иллюстрация, а не видеозапись исполнения.</p>
    <div class="exercise-technique"><section aria-labelledby="exercise-steps-title"><h3 id="exercise-steps-title">Как выполнять</h3><ol class="exercise-steps">${list(exercise.steps)}</ol></section>
      <section aria-labelledby="exercise-cues-title"><h3 id="exercise-cues-title">На что обратить внимание</h3><ul>${list(exercise.cues)}</ul></section></div>
    <section class="exercise-mistakes" aria-labelledby="exercise-mistakes-title"><h3 id="exercise-mistakes-title">Частые ошибки</h3><ul>${list(exercise.mistakes)}</ul></section>
    <section class="exercise-easier" aria-labelledby="exercise-easier-title"><h3 id="exercise-easier-title">Как упростить</h3><p>${esc(exercise.easier)}</p></section>
    <p class="exercise-comfort-note">Двигайтесь плавно, в комфортной амплитуде. Если появляется боль, остановитесь.</p>
    ${sources.length ? `<section class="exercise-sources" aria-labelledby="exercise-sources-title"><h3 id="exercise-sources-title">Источники техники</h3><ul>${sources.map(source => `<li><a href="${esc(source.href)}" target="_blank" rel="noopener noreferrer">${esc(source.title)} <span aria-hidden="true">↗</span></a><span>${esc(source.publisher)}</span></li>`).join('')}</ul></section>` : ''}
    <div class="dialog-actions"><button type="button" class="button outline" data-action="close">Закрыть</button></div>
  </article>`;
}

export function createExerciseLibrary(ctx) {
  let filters = { query: '', muscle: '', equipment: '' };
  let disposeDemo = null;

  function disposeDialog() {
    const dispose = disposeDemo;
    disposeDemo = null;
    if (dispose) dispose();
  }

  const options = (labels, selected) => Object.entries(labels).map(([key, label]) => `<option value="${esc(key)}"${key === selected ? ' selected' : ''}>${esc(label)}</option>`).join('');

  function view() {
    const matches = filterExercises(EXERCISES, filters);
    return `<section class="exercise-library" data-exercise-library aria-labelledby="exercise-library-title">
      <header class="page-heading exercise-heading"><div><p class="eyebrow">ТЕХНИКА ДВИЖЕНИЯ</p><h1 id="exercise-library-title">Упражнения</h1><p class="subtitle">Знакомые движения — шаг за шагом. Выберите упражнение и разберите технику в своём темпе.</p></div><span class="exercise-library-badge">${EXERCISES.length} базовых движений</span></header>
      <form id="exercise-filter-form" class="exercise-filters" role="search" aria-label="Поиск упражнений">
        <label class="field exercise-search"><span class="field-label">Найти упражнение</span><input id="exercise-search" name="query" type="search" placeholder="Например, приседания или plank" value="${esc(filters.query)}" autocomplete="off" aria-controls="exercise-results"></label>
        <label class="field"><span class="field-label" id="exercise-muscle-label">Группа мышц</span><select id="exercise-muscle" name="muscle" aria-labelledby="exercise-muscle-label" aria-controls="exercise-results"><option value="">Все мышцы</option>${options(MUSCLE_GROUPS, filters.muscle)}</select></label>
        <label class="field"><span class="field-label" id="exercise-equipment-label">Инвентарь</span><select id="exercise-equipment" name="equipment" aria-labelledby="exercise-equipment-label" aria-controls="exercise-results"><option value="">Любой инвентарь</option>${options(EQUIPMENT, filters.equipment)}</select></label>
        <button type="button" class="button outline exercise-clear" data-action="exercise-clear">Сбросить</button>
      </form>
      <div class="exercise-results-heading"><p id="exercise-count" role="status" aria-live="polite" aria-atomic="true">Найдено: ${matches.length}</p><p>Схема движения и понятные подсказки</p></div>
      <div id="exercise-results">${cards(matches)}</div>
    </section>`;
  }

  // Only replace the results: the active control, caret and selection stay intact.
  function updateResults(library) {
    const matches = filterExercises(EXERCISES, filters);
    const results = library?.querySelector('#exercise-results');
    const count = library?.querySelector('#exercise-count');
    if (results) results.innerHTML = cards(matches);
    if (count) count.textContent = `Найдено: ${matches.length}`;
  }

  function handleInput(event) {
    const target = event.target, library = target?.closest?.('[data-exercise-library]');
    if (!library || target.id !== 'exercise-search') return false;
    filters.query = target.value;
    updateResults(library);
    return true;
  }

  function handleChange(event) {
    const target = event.target, library = target?.closest?.('[data-exercise-library]');
    if (!library) return false;
    if (target.id === 'exercise-muscle') filters.muscle = target.value;
    else if (target.id === 'exercise-equipment') filters.equipment = target.value;
    else return false;
    updateResults(library);
    return true;
  }

  function handleClick(action, button) {
    if (action === 'exercise-open') {
      const exercise = EXERCISES.find(item => item.id === button?.dataset?.id);
      if (!exercise) return true;
      disposeDialog();
      ctx.openDialog(`${ctx.dialogHeader(exercise.name, 'Техника и схема движения')}${exerciseDetailView(exercise)}`);
      const demo = globalThis.document?.querySelector('#dialog [data-exercise-demo]');
      if (demo) disposeDemo = mountExerciseDemo(demo, exercise.id);
      return true;
    }
    if (action !== 'exercise-clear') return false;
    const library = button?.closest?.('[data-exercise-library]');
    if (!library) return true;
    filters = { query: '', muscle: '', equipment: '' };
    for (const id of ['exercise-search', 'exercise-muscle', 'exercise-equipment']) {
      const control = library.querySelector(`#${id}`);
      if (control) control.value = '';
    }
    updateResults(library);
    library.querySelector('#exercise-search')?.focus();
    return true;
  }

  function handleSubmit(event) {
    if (event.target?.id !== 'exercise-filter-form') return false;
    event.preventDefault();
    return true;
  }

  function reset() {
    disposeDialog();
    filters = { query: '', muscle: '', equipment: '' };
  }

  return { view, handleClick, handleInput, handleChange, handleSubmit, reset, disposeDialog };
}
