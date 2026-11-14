import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createFeatures } from '../static/features.js';
import { resetSessionBinding } from '../static/api.js';

function controller(getState, overrides={}) {
  return createFeatures({getState,dialogHeader:()=>'',openDialog:()=>{},closeDialog:()=>{},dialogOpen:()=>true,isBusy:()=>false,render:()=>{},toast:()=>{},navigate:()=>{},profileDialog:()=>{},onError:error=>{throw error;},setWeek:()=>{},...overrides});
}

test('notifications require separate consent and include a next-week alarm on Sunday', async () => {
  const originalDate=globalThis.Date, originalWindow=globalThis.window, originalNotification=globalThis.Notification;
  const notices=[];
  class Clock extends originalDate {constructor(...args){super(...(args.length?args:['2026-10-18T20:30:00Z']));}}
  class Notification {static permission='granted';static async requestPermission(){return 'granted';}constructor(title,options){notices.push({title,options});}}
  globalThis.Date=Clock;globalThis.Notification=Notification;globalThis.window={Notification};
  const state={sessionBinding:'account-a',timeZone:'Europe/Moscow',profile:{},reminder:{enabled:true,time:'00:30',beforeMinutes:60},plan:{sessions:[]},acceptedNextPlan:{sessions:[{id:'2026-10-19',date:'2026-10-19',status:'planned'}]}};
  const feature=controller(()=>state);
  try {
    feature.tick();assert.equal(notices.length,0,'existing browser permission alone is not consent');
    await feature.handleClick('notifications',{});feature.tick();feature.tick();
    assert.equal(notices.length,1,'Sunday alarm for accepted Monday session, no duplicate');
    assert.equal(notices[0].options.tag,'rhythm-2026-10-19');
    feature.reset();feature.tick();assert.equal(notices.length,1,'reset revokes in-app consent');
  } finally {globalThis.Date=originalDate;globalThis.window=originalWindow;globalThis.Notification=originalNotification;}
});

test('permission result arriving after account reset cannot enable reminders for the new account', async () => {
  const originalWindow=globalThis.window,originalNotification=globalThis.Notification;let release,opened=0;
  class Notification {static permission='granted';static requestPermission(){return new Promise(resolve=>{release=resolve;});}}
  globalThis.Notification=Notification;globalThis.window={Notification};
  let state={sessionBinding:'account-a',profile:{}};
  const feature=controller(()=>state,{openDialog:()=>{opened++;}});
  try {const pending=feature.handleClick('notifications',{});feature.reset();state={...state,sessionBinding:'account-b'};release('granted');await pending;assert.equal(opened,0);}
  finally {globalThis.window=originalWindow;globalThis.Notification=originalNotification;}
});

test('reading week history does not leave an in-flight assistant question permanently busy', async () => {
  const originalFetch=globalThis.fetch,originalDocument=globalThis.document;let release;
  globalThis.document={querySelector:()=>null};resetSessionBinding();
  globalThis.fetch=async path=>path==='/api/assistant'?new Promise(resolve=>{release=resolve;}):{ok:true,json:async()=>({weekStart:'2026-10-05'})};
  let revision=1;
  const feature=controller(()=>({profile:{},sessionBinding:'account-a',revision}));
  try {
    const pending=feature.handleClick('assistant-ask',{dataset:{question:'Что у меня сегодня?'}});
    await feature.loadWeek('2026-10-05');
    release({ok:true,json:async()=>({mode:'rules',text:'Сегодня отдых',evidence:[]})});await pending;
    const html=feature.assistant();assert.match(html,/Сегодня отдых/);assert.doesNotMatch(html,/Проверяем данные…/);
    revision++;assert.doesNotMatch(feature.assistant(),/Сегодня отдых/,'a new diary revision invalidates the previous answer');
  } finally {globalThis.fetch=originalFetch;globalThis.document=originalDocument;resetSessionBinding();}
});

test('a new server day invalidates a rules answer even when the diary revision stays the same', async () => {
  const originalFetch = globalThis.fetch, originalDocument = globalThis.document;
  globalThis.document = { querySelector: () => null };
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ text: 'Сегодня силовая тренировка', evidence: [] }) });
  resetSessionBinding();
  let state = { profile: {}, sessionBinding: 'account-a', revision: 1, today: '2026-10-21' };
  const feature = controller(() => state);
  try {
    await feature.handleClick('assistant-ask', { dataset: { question: 'Что у меня сегодня?' } });
    assert.match(feature.assistant(), /Сегодня силовая тренировка/);
    state = { ...state, today: '2026-10-22' };
    assert.doesNotMatch(feature.assistant(), /Сегодня силовая тренировка/);
  } finally { globalThis.fetch = originalFetch; globalThis.document = originalDocument; resetSessionBinding(); }
});

test('a rules answer arriving after the server day changes cannot describe yesterday as today', async () => {
  const originalFetch = globalThis.fetch, originalDocument = globalThis.document;
  globalThis.document = { querySelector: () => null };
  let release;
  globalThis.fetch = async () => new Promise(resolve => { release = resolve; });
  resetSessionBinding();
  let state = { profile: {}, sessionBinding: 'account-a', revision: 1, today: '2026-10-21' };
  const feature = controller(() => state);
  try {
    const pending = feature.handleClick('assistant-ask', { dataset: { question: 'Что у меня сегодня?' } });
    state = { ...state, today: '2026-10-22' };
    release({ ok: true, json: async () => ({ text: 'Сегодня силовая тренировка', evidence: [] }) });
    await pending;
    assert.doesNotMatch(feature.assistant(), /Сегодня силовая тренировка/);
    assert.match(feature.assistant(), /Задайте вопрос ещё раз/);
  } finally { globalThis.fetch = originalFetch; globalThis.document = originalDocument; resetSessionBinding(); }
});

function importForm(id, file) {
  const error = { textContent: '', isConnected: true }, submit = { disabled: false };
  return { id, isConnected: true, values: new Map(file ? [['file', file]] : [['completePlanned', 'on']]), error, querySelector: selector => selector === '#form-error' ? error : submit };
}

test('import confirmation cannot use a newer diary revision than the reviewed preview', async () => {
  const previous = { fetch: globalThis.fetch, FormData: globalThis.FormData };
  globalThis.FormData = class { constructor(form) { this.values = form.values; } get(key) { return this.values.get(key); } has(key) { return this.values.has(key); } };
  const preview = { revision: 7, workouts: [{ id: 'synthetic-run', date: '2026-10-22', kind: 'run', minutes: 30, duplicate: false, canComplete: true }] };
  globalThis.fetch = async () => ({ ok: true, json: async () => preview });
  resetSessionBinding();
  let state = { profile: {}, revision: 7, sessionBinding: 'account-a' }, writes = 0;
  const feature = controller(() => state, { mutate: async () => { writes++; return true; }, onError: () => {} });
  try {
    const file = { name: 'synthetic-fixture.csv', size: 100, text: async () => 'id,date,kind,minutes,distance\nsynthetic-run,2026-10-22,running,30,3000' };
    await feature.handleSubmit({ target: importForm('import-preview-form', file), preventDefault() {} });
    state = { ...state, revision: 8 };
    const form = importForm('import-save-form');
    await feature.handleSubmit({ target: form, preventDefault() {} });
    assert.equal(writes, 0, 'the user must review the changed diary before completing planned sessions');
    assert.match(form.error.textContent, /изменил|изменён|изменен/i);
  } finally { Object.assign(globalThis, previous); resetSessionBinding(); }
});

test('an import file that finishes reading after an account reset is never sent under the new account', async () => {
  const previous = { fetch: globalThis.fetch, FormData: globalThis.FormData };
  globalThis.FormData = class { constructor(form) { this.values = form.values; } get(key) { return this.values.get(key); } };
  let sent = 0, release;
  globalThis.fetch = async () => { sent++; return { ok: true, json: async () => ({ revision: 1, workouts: [] }) }; };
  resetSessionBinding();
  let state = { profile: {}, revision: 1, sessionBinding: 'account-a' };
  const feature = controller(() => state);
  try {
    const file = { name: 'synthetic-fixture.csv', size: 100, text: () => new Promise(resolve => { release = resolve; }) };
    const pending = feature.handleSubmit({ target: importForm('import-preview-form', file), preventDefault() {} });
    feature.reset(); state = { ...state, sessionBinding: 'account-b' };
    release('synthetic private fixture content');
    await pending;
    assert.equal(sent, 0);
  } finally { Object.assign(globalThis, previous); resetSessionBinding(); }
});
