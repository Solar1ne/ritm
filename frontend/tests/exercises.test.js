import test from 'node:test';
import assert from 'node:assert/strict';
import { createExerciseLibrary, exerciseDetailView, filterExercises } from '../static/exercises.js';
import { EXERCISES, MUSCLE_GROUPS, EQUIPMENT } from '../static/exercises-data.js';
import { escapeHTML } from '../static/format.js';

const fixture = [
  { id: 'a', name: 'Подъём', aliases: ['Calf raise', 'На носки'], muscles: ['legs'], equipment: ['wall'] },
  { id: 'b', name: 'Приседание', aliases: ['Squat'], muscles: ['legs', 'glutes'], equipment: ['none'] },
  { id: 'c', name: 'Тяга', aliases: ['Row'], muscles: ['back', 'arms'], equipment: ['dumbbells'] },
];

test('exercise search normalizes Russian ё, case and whitespace and searches aliases literally', () => {
  assert.deepEqual(filterExercises(fixture, { query: '  ПОДЪЕМ  ' }).map(item => item.id), ['a']);
  assert.deepEqual(filterExercises(fixture, { query: 'CALF' }).map(item => item.id), ['a']);
  assert.deepEqual(filterExercises(fixture, { query: 'носКи' }).map(item => item.id), ['a']);
  for (const query of ['[', '.*', '(', '<img>']) assert.deepEqual(filterExercises(fixture, { query }), []);
});

test('muscle, equipment and text filters intersect without mutating their inputs', () => {
  const before = structuredClone(fixture), filters = Object.freeze({ query: 'squat', muscle: 'glutes', equipment: 'none' });
  assert.deepEqual(filterExercises(fixture, filters).map(item => item.id), ['b']);
  assert.deepEqual(filterExercises(fixture, { muscle: 'legs', equipment: 'dumbbells' }), []);
  assert.deepEqual(filterExercises(fixture, { muscle: 'unknown' }), []);
  assert.deepEqual(filterExercises(fixture, {}), fixture);
  assert.deepEqual(fixture, before);
});

function harness() {
  const dialogs = [], results = { innerHTML: '' }, count = { textContent: '' };
  let focused = '';
  const controls = Object.fromEntries(['exercise-search', 'exercise-muscle', 'exercise-equipment'].map(id => [id, { id, value: '', selectionStart: 3, selectionEnd: 3, focus() { focused = id; }, closest: () => library }]));
  const elements = { ...controls, 'exercise-results': results, 'exercise-count': count };
  const library = { querySelector: selector => elements[selector.slice(1)] ?? null };
  Object.defineProperty(library, 'innerHTML', { set() { assert.fail('filtering must not replace the library controls'); } });
  const controller = createExerciseLibrary({ openDialog: html => dialogs.push(html), dialogHeader: title => `<h2>${escapeHTML(title)}</h2>`, closeDialog() {} });
  return { controller, dialogs, results, count, controls, library, focused: () => focused };
}

test('public catalog exposes labelled search, both filters, live count and all exercise dialogs', () => {
  const { controller } = harness(), html = controller.view();
  assert.match(html, /<h1[^>]*>Упражнения<\/h1>/);
  assert.match(html, /role="search"/);
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /aria-controls="exercise-results"/);
  assert.equal((html.match(/data-action="exercise-open"/g) || []).length, EXERCISES.length);
  for (const label of [...Object.values(MUSCLE_GROUPS), ...Object.values(EQUIPMENT)]) assert.ok(html.includes(label));
  assert.doesNotMatch(html, /data-action="profile"|<iframe|<video|<form[^>]*action=/);
});

test('typing updates only result nodes and preserves the input value and selection', () => {
  const h = harness(), search = h.controls['exercise-search'];
  search.value = 'zz-no-matches';
  assert.equal(h.controller.handleInput({ target: search }), true);
  assert.equal(h.count.textContent, 'Найдено: 0');
  assert.match(h.results.innerHTML, /Пока ничего не нашлось/);
  assert.equal(h.controls['exercise-search'], search);
  assert.equal(search.value, 'zz-no-matches');
  assert.equal(search.selectionStart, 3);
  assert.equal(search.selectionEnd, 3);
  assert.ok(h.controller.view().includes('value="zz-no-matches"'));
});

test('changing selects combines filters and clear restores visible controls and results', () => {
  const h = harness();
  h.controls['exercise-muscle'].value = 'back';
  h.controls['exercise-equipment'].value = 'dumbbells';
  h.controller.handleChange({ target: h.controls['exercise-muscle'] });
  h.controller.handleChange({ target: h.controls['exercise-equipment'] });
  const expected = filterExercises(EXERCISES, { muscle: 'back', equipment: 'dumbbells' });
  assert.equal(h.count.textContent, `Найдено: ${expected.length}`);
  assert.equal((h.results.innerHTML.match(/data-action="exercise-open"/g) || []).length, expected.length);
  assert.equal(h.controller.handleClick('exercise-clear', { closest: () => h.library }), true);
  assert.equal(h.count.textContent, `Найдено: ${EXERCISES.length}`);
  for (const control of Object.values(h.controls)) assert.equal(control.value, '');
  assert.equal(h.focused(), 'exercise-search');
});

test('detail contains all technique sections and source links without writing diary state', () => {
  const h = harness(), exercise = EXERCISES[0];
  assert.equal(h.controller.handleClick('exercise-open', { dataset: { id: exercise.id } }), true);
  assert.equal(h.dialogs.length, 1);
  const html = h.dialogs[0];
  for (const text of [exercise.name, exercise.summary, exercise.easier, ...exercise.steps, ...exercise.cues, ...exercise.mistakes]) assert.ok(html.includes(escapeHTML(text)));
  assert.match(html, /Как выполнять/);
  assert.match(html, /Частые ошибки/);
  assert.match(html, /Если появляется боль, остановитесь/);
  assert.match(html, /не видеозапись/);
  assert.match(html, /rel="noopener noreferrer"/);
  assert.doesNotMatch(html, /<iframe|data-action="(entry|save|checkin|profile)"/);
  h.controller.disposeDialog();
  h.controller.disposeDialog();
});

test('details escape content and reject executable, insecure, relative and credential source URLs', () => {
  const attack = '<img src=x onerror="alert(1)">';
  const exercise = {
    id: 'unknown', name: attack, muscles: ['legs'], equipment: ['none'], summary: attack,
    steps: [attack], cues: [attack], mistakes: [attack], easier: attack,
    sources: ['javascript:alert(1)', 'data:text/html,evil', '//example.org', 'http://example.org', 'https://user:secret@example.org', 'https://example.org/technique?q="<tag>'].map(url => ({ title: attack, publisher: attack, url })),
  };
  const html = exerciseDetailView(exercise);
  assert.doesNotMatch(html, /<img|onerror="|href="javascript:|href="data:|href="http:|user:secret|<tag>/);
  assert.match(html, /&lt;img/);
  assert.equal((html.match(/target="_blank"/g) || []).length, 1);
  assert.match(html, /href="https:\/\/example.org\/technique/);
});

test('unknown IDs and unrelated events do not open dialogs or consume demo controls', () => {
  const h = harness();
  assert.equal(h.controller.handleClick('exercise-open', { dataset: { id: '__proto__' } }), true);
  assert.equal(h.controller.handleClick('exercise-open', {}), true);
  assert.equal(h.controller.handleClick('play', {}), false);
  assert.equal(h.controller.handleInput({ target: { id: 'other' } }), false);
  assert.equal(h.controller.handleChange({ target: { id: 'other', closest: () => h.library } }), false);
  assert.equal(h.controller.handleSubmit({ target: { id: 'other' } }), false);
  assert.equal(h.dialogs.length, 0);
});

test('search form submit is local and reset forgets filters', () => {
  const h = harness();
  h.controls['exercise-search'].value = '<unsafe>';
  h.controller.handleInput({ target: h.controls['exercise-search'] });
  assert.match(h.controller.view(), /value="&lt;unsafe&gt;"/);
  let prevented = false;
  assert.equal(h.controller.handleSubmit({ target: { id: 'exercise-filter-form' }, preventDefault() { prevented = true; } }), true);
  assert.equal(prevented, true);
  h.controller.reset();
  assert.match(h.controller.view(), /id="exercise-search"[^>]*value=""/);
  assert.match(h.controller.view(), new RegExp(`Найдено: ${EXERCISES.length}`));
});

test('opening another exercise, closing and resetting dispose the mounted demo listeners', () => {
  const previousDocument = globalThis.document;
  const eventTarget = () => ({
    listeners: new Map(),
    addEventListener(type, listener) { this.listeners.set(type, listener); },
    removeEventListener(type, listener) { if (this.listeners.get(type) === listener) this.listeners.delete(type); },
    setAttribute() {},
  });
  const doc = { ...eventTarget(), defaultView: {} };
  let mounted;
  doc.querySelector = () => mounted;
  const mountedDemos = [];
  const controller = createExerciseLibrary({ dialogHeader: () => '', closeDialog() {}, openDialog(html) {
    const id = html.match(/data-exercise-id="([^"]+)"/)[1];
    const elements = Object.fromEntries(['[data-demo-stage]', '[data-demo-phase]', '[data-demo-range]', '[data-demo-action="play"]', '[data-demo-motion-note]'].map(selector => [selector, eventTarget()]));
    mounted = { ...eventTarget(), dataset: { exerciseId: id }, ownerDocument: doc, matches: () => true, querySelector: selector => elements[selector] };
    mountedDemos.push(mounted);
  } });
  globalThis.document = doc;
  try {
    controller.handleClick('exercise-open', { dataset: { id: 'squat' } });
    assert.equal(mountedDemos[0].listeners.size, 1);
    controller.handleClick('exercise-open', { dataset: { id: 'reverse-lunge' } });
    assert.equal(mountedDemos[0].listeners.size, 0);
    assert.equal(mountedDemos[0].querySelector('[data-demo-range]').listeners.size, 0);
    assert.equal(mountedDemos[1].listeners.size, 1);
    controller.disposeDialog();
    controller.disposeDialog();
    assert.equal(mountedDemos[1].listeners.size, 0);
    assert.equal(doc.listeners.size, 0);
    controller.handleClick('exercise-open', { dataset: { id: 'squat' } });
    assert.equal(mountedDemos[2].listeners.size, 1);
    controller.reset();
    assert.equal(mountedDemos[2].listeners.size, 0);
    assert.equal(doc.listeners.size, 0);
  } finally {
    controller.disposeDialog();
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
});
