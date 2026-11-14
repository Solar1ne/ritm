import test from 'node:test';
import assert from 'node:assert/strict';
import { overview, week, insights, shell } from '../static/views.js';
import { samplePlan } from '../static/format.js';

const session = { id: '2026-10-22', date: '2026-10-22', day: 3, kind: 'strength', title: 'Силовая тренировка', minutes: 45, status: 'planned' };
const state = {
  today: '2026-10-22', currentWeekStart: '2026-10-19', historyEditableFrom: '2026-07-24',
  profile: { name: 'Fixture', goal: 'tone' }, entries: [{ id: 'sleep', date: '2026-10-22', status: 'wellbeing', sleep: 8 }],
  plan: { weekStart: '2026-10-19', totalMinutes: 45, sessions: [session] },
};

test('personal calendar follows the server day when the device clock is ahead', () => {
  const OriginalDate = globalThis.Date;
  class DeviceClock extends OriginalDate {
    constructor(...args) { super(...(args.length ? args : ['2026-10-23T10:00:00Z'])); }
  }
  globalThis.Date = DeviceClock;
  try {
    assert.match(overview(state), /Отметить занятие/, 'today focus must select the server day session');
    assert.match(overview(state), /class="day today kind-strength planned"/, 'calendar must highlight the same day');
    assert.match(week(state), /data-action="variant-open" data-id="2026-10-22"/, 'today session must remain adaptable');
    assert.match(shell(state, 'overview', ''), /четверг, 22 октября/, 'header must agree with the diary calendar');
    assert.doesNotMatch(insights(state), /data-id="2026-10-23"/, 'progress must not add a future device day');
    assert.match(insights(state), /data-id="2026-10-16"/, 'seven days must include the first server calendar day');
  } finally { globalThis.Date = OriginalDate; }
});

test('a future session stays read-only when the device clock is ahead', () => {
  const OriginalDate = globalThis.Date;
  class DeviceClock extends OriginalDate {
    constructor(...args) { super(...(args.length ? args : ['2026-10-23T10:00:00Z'])); }
  }
  globalThis.Date = DeviceClock;
  try {
    const future = { ...session, id: '2026-10-23', date: '2026-10-23', day: 4 };
    const html = week({ ...state, plan: { ...state.plan, sessions: [future] } });
    assert.match(html, /data-action="session" data-id="2026-10-23">Подробнее<\/button>/);
  } finally { globalThis.Date = OriginalDate; }
});

test('sample week can use the server calendar rather than the device clock', () => {
  assert.equal(samplePlan('2026-10-18').weekStart, '2026-10-12');
});
