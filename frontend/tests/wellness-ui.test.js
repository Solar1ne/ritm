import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createWellness } from '../static/wellness.js';

function harness(mutate) {
  let state = { profile: {}, revision: 1, sessionBinding: 'a', today: '2026-10-17', historyEditableFrom: '2026-07-20', hrvEntries: [], bodyEntries: [] }, dialog = '', writes = [];
  const controller = createWellness({ getState: () => state, dialogHeader: () => '', openDialog: html => { dialog = html; }, closeDialog() {}, render() {}, toast() {}, navigate() {}, profileDialog() {}, isBusy: () => false, mutate: async (...args) => { writes.push(args); return mutate ? mutate(...args) : true; } });
  return { controller, get: () => state, set: next => { state = next; }, dialog: () => dialog, writes };
}
function form(id, values) {
  const box = { textContent: '', insertAdjacentHTML(_, html) { this.html = html; } };
  const event = { preventDefault() {}, target: { id, values, isConnected: true, querySelector: () => box } };
  return { event, box };
}
async function globals(fn) {
  const old = { FormData: globalThis.FormData, document: globalThis.document };
  globalThis.FormData = class { constructor(form) { this.values = form.values; } get(key) { return this.values[key] ?? null; } };
  globalThis.document = { querySelector: () => null };
  try { await fn(); } finally { Object.assign(globalThis, old); }
}
const body = { id: 'old', date: '2026-10-17', weightKg: 70, bodyFatPercent: 20, method: 'bia', source: 'Весы A' };
const values = { date: body.date, source: body.source, method: body.method, weightKg: '71', bodyFatPercent: '' };

test('new measurements require explicit metric/window/method and do not invent defaults', async () => globals(async () => {
  const h = harness(); await h.controller.handleClick('measurement-add', {});
  assert.match(h.dialog(), /name="metric" required/); assert.match(h.dialog(), /value="">Выберите метрику/);
  assert.doesNotMatch(h.dialog(), /value="rmssd" selected/);
  await h.controller.handleClick('measurement-tab', { dataset: { kind: 'body' } }); await h.controller.handleClick('measurement-add', {});
  assert.doesNotMatch(h.dialog(), /value="bia" selected/);
}));

test('adding an existing body measurement cannot silently erase its missing component', async () => globals(async () => {
  const h = harness(); h.set({ ...h.get(), bodyEntries: [body] });
  await h.controller.handleClick('measurement-tab', { dataset: { kind: 'body' } }); await h.controller.handleClick('measurement-add', {});
  const save = form('measurement-form', values);
  await h.controller.handleSubmit(save.event); assert.equal(h.writes.length, 0); assert.match(save.box.textContent, /20%/);
  let submitted;
  const owner = { requestSubmit() { submitted = h.controller.handleSubmit(save.event); } };
  await h.controller.handleClick('measurement-replace', { closest: () => owner }); await submitted;
  assert.equal(h.writes.length, 1); assert.equal(h.writes[0][1].entry.weightKg, 71); assert.equal(h.writes[0][1].entry.bodyFatPercent, undefined);
}));

test('replacement consent is bound to exactly the reviewed fields', async () => globals(async () => {
  const h = harness(); h.set({ ...h.get(), bodyEntries: [body] });
  await h.controller.handleClick('measurement-tab', { dataset: { kind: 'body' } }); await h.controller.handleClick('measurement-add', {});
  const save = form('measurement-form', { ...values }); await h.controller.handleSubmit(save.event);
  save.event.target.values.weightKg = '72'; let submitted;
  await h.controller.handleClick('measurement-replace', { closest: () => ({ requestSubmit() { submitted = h.controller.handleSubmit(save.event); } }) }); await submitted;
  assert.equal(h.writes.length, 0);
}));

test('account reset and stale revision cannot submit a retained measurement command', async () => globals(async () => {
  const h = harness(); await h.controller.handleClick('measurement-tab', { dataset: { kind: 'body' } }); await h.controller.handleClick('measurement-add', {});
  h.set({ ...h.get(), revision: 2 }); await h.controller.handleSubmit(form('measurement-form', values).event); assert.equal(h.writes.length, 0);
  h.controller.reset(); h.set({ ...h.get(), sessionBinding: 'b' }); await h.controller.handleSubmit(form('measurement-form', values).event); assert.equal(h.writes.length, 0);
}));

test('device labels are escaped and incompatible series never appear together', async () => globals(async () => {
  const h = harness(); h.set({ ...h.get(), hrvEntries: [{ id: 'x', date: body.date, valueMs: 43, metric: 'rmssd', window: 'overnight', source: '<script>name</script>' }] });
  assert.match(h.controller.view(), /&lt;script&gt;/); assert.doesNotMatch(h.controller.view(), /<script>name/);
}));
