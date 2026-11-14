import test from 'node:test';
import assert from 'node:assert/strict';
import { activityCalendar, previousWeekSummary } from '../static/consistency.js';

const today = '2026-10-22';
const profile = { name: 'Ира', goal: 'tone', experience: 'beginner', days: [0, 2, 4], minutes: 90 };
const session = (date, extra = {}) => ({ id: date, date, day: 0, kind: 'strength', title: 'Занятие', minutes: 45, status: 'planned', detailsKnown: true, ...extra });
const week = (weekStart, sessions = [], extra = {}) => ({ weekStart, source: 'baseline', profile, plan: { weekStart, sessions, totalMinutes: sessions.reduce((sum, item) => sum + item.minutes, 0) }, ...extra });
const fact = (date, status, extra = {}) => ({ id: `${date}-${status}`, date, sessionId: date, status, ...extra });
const state = (extra = {}) => ({ today, currentWeekStart: '2026-10-19', profile, weeks: [], entries: [], workouts: [], sessionAdjustments: [], ...extra });
const dayAt = (value, date) => activityCalendar(value, 30).find(day => day.date === date);

test('calendar uses an inclusive 30 or 90 server-day window across year and leap boundaries', () => {
  for (const [through, days, from] of [
    [today, 30, '2026-09-23'], [today, 90, '2026-07-25'],
    ['2027-01-03', 30, '2026-12-05'], ['2027-01-03', 90, '2026-10-06'],
    ['2024-03-29', 30, '2024-02-29'],
  ]) {
    const calendar = activityCalendar(state({ today: through }), days);
    assert.equal(calendar.length, days);
    assert.equal(calendar[0].date, from);
    assert.equal(calendar.at(-1).date, through);
    assert.equal(new Set(calendar.map(day => day.date)).size, days);
  }
});

test('missing history and days before onboarding stay unknown without inferred misses', () => {
  const value = state({ weeks: [week('2026-10-19', [session('2026-10-21')])] });
  const calendar = activityCalendar(value);
  assert.equal(calendar.length, 30);
  assert.deepEqual(calendar[0], {
    date: '2026-09-23', status: 'unknown', scheduleKnown: false, sessions: [], workouts: [],
    logged: false, sleep: null, fatigue: null, completed: 0, missed: 0,
  });
  assert.equal(dayAt(value, '2026-10-18').status, 'unknown');
  assert.equal(dayAt(value, '2026-10-19').status, 'rest');
  assert.equal(dayAt(value, '2026-10-19').scheduleKnown, true);
  assert.equal(dayAt(value, '2026-10-21').status, 'unlogged');
  assert.equal(dayAt(value, '2026-10-21').missed, 0);
});

test('day status keeps done and explicit missed ahead of imported activity', () => {
  const value = state({
    weeks: [week('2026-10-19', ['2026-10-19', '2026-10-20', '2026-10-21', today].map(date => session(date)))],
    entries: [fact('2026-10-19', 'done'), fact('2026-10-20', 'missed')],
    workouts: [{ id: 'linked-done', date: '2026-10-19', sessionId: '2026-10-19', minutes: 60 }, { id: 'linked-missed', date: '2026-10-20', minutes: 30 }],
  });
  assert.equal(dayAt(value, '2026-10-19').status, 'done');
  assert.equal(dayAt(value, '2026-10-20').status, 'missed');
  assert.equal(dayAt(value, '2026-10-21').status, 'unlogged');
  assert.equal(dayAt(value, today).status, 'planned');
});

test('activity, known rest and wellbeing remain distinct states', () => {
  const value = state({
    weeks: [week('2026-10-19')],
    entries: [
      { date: '2026-10-18', status: 'wellbeing', sleep: 7 },
      { date: '2026-10-20', status: 'wellbeing', sleep: 8 },
    ],
    workouts: [{ id: 'off-plan', date: '2026-10-19', minutes: 20 }],
  });
  assert.equal(dayAt(value, '2026-10-19').status, 'activity');
  assert.equal(dayAt(value, '2026-10-20').status, 'rest');
  assert.equal(dayAt(value, '2026-10-18').status, 'checkin');
});

test('legacy observed-only schedules never invent rest or a complete denominator', () => {
  const value = state({ weeks: [week('2026-10-12', [session('2026-10-14', { detailsKnown: false, minutes: 0 })], { source: 'legacy', profile: null })], entries: [fact('2026-10-14', 'done')] });
  assert.equal(dayAt(value, '2026-10-13').status, 'unknown');
  assert.equal(dayAt(value, '2026-10-14').status, 'done');
  assert.equal(dayAt(value, '2026-10-14').scheduleKnown, false);
  const summary = previousWeekSummary(value);
  assert.equal(summary.hasSchedule, true);
  assert.equal(summary.completeSchedule, false);
  assert.equal(summary.scheduled, 1);
  assert.equal(summary.done, 1);
  assert.equal(summary.knownMinutes, 0);
  assert.equal(summary.unknownDurationCompletions, 1);
});

test('session facts require an exact saved id and date and ignore raw snapshot statuses', () => {
  const value = state({
    weeks: [week('2026-10-12', [session('2026-10-14', { status: 'done' }), session('2026-10-16', { status: 'missed' })])],
    entries: [fact('2026-10-13', 'done', { sessionId: '2026-10-14' }), fact('2026-10-16', 'done', { sessionId: 'wrong-id' })],
  });
  assert.equal(dayAt(value, '2026-10-14').status, 'unlogged');
  assert.equal(dayAt(value, '2026-10-14').completed, 0);
  assert.equal(dayAt(value, '2026-10-16').status, 'unlogged');
  const summary = previousWeekSummary(value);
  assert.equal(summary.done, 0);
  assert.equal(summary.missed, 0);
  assert.equal(summary.unlogged, 2);
});

test('duplicate and corrected facts count each scheduled session once with the last result', () => {
  const value = state({
    weeks: [week('2026-10-12', [session('2026-10-14'), session('2026-10-14'), session('2026-10-16')])],
    entries: [fact('2026-10-14', 'done'), fact('2026-10-14', 'done'), fact('2026-10-14', 'missed'), fact('2026-10-16', 'missed'), fact('2026-10-16', 'done')],
  });
  assert.equal(dayAt(value, '2026-10-14').sessions.length, 1);
  assert.equal(dayAt(value, '2026-10-14').missed, 1);
  assert.equal(dayAt(value, '2026-10-16').completed, 1);
  const summary = previousWeekSummary(value);
  assert.equal(summary.scheduled, 2);
  assert.equal(summary.done, 1);
  assert.equal(summary.missed, 1);
});

test('last independent wellbeing values count once per day and preserve recorded zero', () => {
  const value = state({ entries: [
    { date: '2026-10-14', status: 'wellbeing', sleep: 8, fatigue: 8 },
    { date: '2026-10-14', status: 'wellbeing', sleep: 0 },
    { date: '2026-10-14', status: 'wellbeing', fatigue: 2 },
    { date: '2026-10-15', status: 'wellbeing', sleep: 8 },
    { date: '2026-10-16', status: 'wellbeing', fatigue: 4 },
  ] });
  assert.equal(dayAt(value, '2026-10-14').sleep, 0);
  assert.equal(dayAt(value, '2026-10-14').fatigue, 2);
  const summary = previousWeekSummary(value);
  assert.equal(summary.loggedDays, 3);
  assert.equal(summary.sleepDays, 2);
  assert.equal(summary.averageSleep, 4);
  assert.equal(summary.fatigueDays, 2);
  assert.equal(summary.averageFatigue, 3);
});

test('future facts and workouts never enter the calendar or closed-week recap', () => {
  const value = state({
    weeks: [week('2026-10-19', [session('2026-10-23')])],
    entries: [fact('2026-10-23', 'done', { sleep: 20, fatigue: 10 })],
    workouts: [{ id: 'future', date: '2026-10-23', minutes: 100 }],
  });
  assert.equal(activityCalendar(value).filter(day => day.logged || day.workouts.length || day.completed).length, 0);
  assert.equal(previousWeekSummary(value).done, 0);
  assert.equal(previousWeekSummary(value).averageSleep, null);
});

test('current projection supplements only a missing current snapshot without inventing past weeks', () => {
  const plan = { weekStart: '2026-10-19', sessions: [session(today)], totalMinutes: 45 };
  const value = state({ plan });
  assert.equal(dayAt(value, today).status, 'planned');
  assert.equal(dayAt(value, '2026-10-20').status, 'unknown');
  assert.equal(previousWeekSummary(value).hasSchedule, false);
  const saved = state({ plan, weeks: [week('2026-10-19')] });
  assert.equal(dayAt(saved, today).status, 'rest', 'saved schedule wins over a stale projection');
  const oldProjection = state({ plan: { ...plan, weekStart: '2026-10-12', sessions: [session('2026-10-14')] } });
  assert.equal(dayAt(oldProjection, '2026-10-14').status, 'unknown');
});

test('recap always selects the last closed Monday through Sunday across a year boundary', () => {
  const value = state({ today: '2027-01-04', currentWeekStart: '2027-01-04', weeks: [week('2026-12-28', [session('2027-01-01')]), week('2027-01-04', [session('2027-01-04')])], entries: [fact('2027-01-01', 'done'), fact('2027-01-04', 'done')] });
  for (const currentWeekStart of ['2027-01-04', undefined]) {
    const summary = previousWeekSummary({ ...value, currentWeekStart });
    assert.equal(summary.weekStart, '2026-12-28');
    assert.equal(summary.weekEnd, '2027-01-03');
    assert.equal(summary.days.length, 7);
    assert.equal(summary.days[0].date, '2026-12-28');
    assert.equal(summary.days.at(-1).date, '2027-01-03');
    assert.equal(summary.done, 1);
  }
});

test('recap uses historical adjusted minutes, preserves unknown duration and avoids linked-import double counts', () => {
  const value = state({
    weeks: [week('2026-10-12', [session('2026-10-12'), session('2026-10-14', { minutes: 0, detailsKnown: false }), session('2026-10-16')], { source: 'accepted' })],
    entries: [fact('2026-10-12', 'done'), fact('2026-10-14', 'done'), fact('2026-10-16', 'missed')],
    workouts: [{ id: 'linked', date: '2026-10-12', sessionId: '2026-10-12', minutes: 80 }],
    sessionAdjustments: [
      { sessionId: '2026-10-12', date: '2026-10-12', minutes: 20, variantId: 'home-mobility-v1', environment: 'home', lowImpact: true },
      { sessionId: '2026-10-14', date: '2026-10-14', minutes: 15 },
      { sessionId: '2026-10-16', date: '2026-10-15', minutes: 25 },
    ],
  });
  const summary = previousWeekSummary(value);
  assert.equal(summary.completeSchedule, true);
  assert.equal(summary.scheduled, 3);
  assert.equal(summary.done, 2);
  assert.equal(summary.missed, 1);
  assert.equal(summary.knownMinutes, 20);
  assert.equal(summary.unknownDurationCompletions, 1);
  assert.equal(summary.days[0].sessions[0].originalMinutes, 45);
  assert.equal(summary.days[0].sessions[0].originalTitle, 'Занятие');
  assert.equal(summary.days[0].sessions[0].title, 'Занятие с заменой');
  assert.equal(summary.days[0].sessions[0].minutes, 20);
  assert.equal(summary.days[2].sessions[0].minutes, 0);
  assert.equal(summary.days[4].sessions[0].minutes, 45);
  assert.equal(summary.days[0].workouts.length, 1);
});

test('an empty recap contains honest missing measurements and no percentage', () => {
  const summary = previousWeekSummary(state());
  assert.deepEqual({ ...summary, days: undefined }, {
    weekStart: '2026-10-12', weekEnd: '2026-10-18', hasSchedule: false, completeSchedule: false,
    scheduled: 0, done: 0, missed: 0, unlogged: 0, knownMinutes: 0, unknownDurationCompletions: 0,
    sleepDays: 0, averageSleep: null, fatigueDays: 0, averageFatigue: null, loggedDays: 0, days: undefined,
  });
  assert.equal(summary.days.every(day => day.status === 'unknown'), true);
});

test('projections neither mutate input nor share mutable session and workout objects', () => {
  const value = state({ weeks: [week('2026-10-12', [session('2026-10-14')])], entries: [fact('2026-10-14', 'done')], workouts: [{ id: 'linked', date: '2026-10-14', sessionId: '2026-10-14', minutes: 30 }], sessionAdjustments: [{ sessionId: '2026-10-14', date: '2026-10-14', minutes: 20 }] });
  const original = structuredClone(value);
  const calendar = activityCalendar(value, 90);
  const summary = previousWeekSummary(value);
  const day = calendar.find(item => item.date === '2026-10-14');
  day.sessions[0].minutes = 99;
  day.workouts[0].minutes = 99;
  summary.days[2].sessions[0].title = 'Changed';
  summary.days[2].workouts[0].minutes = 120;
  assert.deepEqual(value, original);
});
