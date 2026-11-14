import test from 'node:test';
import assert from 'node:assert/strict';
import { runningMetrics, runPace, formatPace } from '../static/activity-metrics.js';
import { createFeatures } from '../static/features.js';

const today = '2026-10-17';
const run = (extra = {}) => ({ id: 'run', date: today, startedAt: `${today}T08:00:00+03:00`, kind: 'run', source: 'csv', minutes: 30, ...extra });

test('running summary includes every recorded minute but weights pace only by positive measured distance', () => {
  const workouts = [
    run({ id: 'slow', minutes: 20, distanceMeters: 2000 }),
    run({ id: 'fast', minutes: 30, distanceMeters: 6000 }),
    run({ id: 'unknown', minutes: 40 }),
    run({ id: 'zero', minutes: 10, distanceMeters: 0 }),
    run({ id: 'walk', kind: 'walk', minutes: 70, distanceMeters: 10000 }),
  ];
  const original = structuredClone(workouts);
  assert.deepEqual(runningMetrics(workouts, today), {
    days: 30, from: '2026-09-18', through: today,
    totalRuns: 4, totalMinutes: 100,
    knownDistanceMeters: 8000, knownDistanceRuns: 3, missingDistanceRuns: 1,
    paceMinutesPerKm: 6.25, paceRuns: 2,
  });
  assert.deepEqual(workouts, original, 'summarising never mutates imported facts');
  assert.equal(formatPace(runningMetrics(workouts, today).paceMinutesPerKm), '6:15');
});

test('missing, null and recorded zero distances remain distinct from measured pace', () => {
  for (const extra of [{}, { distanceMeters: null }]) {
    const summary = runningMetrics([run(extra)], today);
    assert.equal(summary.totalRuns, 1);
    assert.equal(summary.totalMinutes, 30);
    assert.equal(summary.knownDistanceMeters, null);
    assert.equal(summary.knownDistanceRuns, 0);
    assert.equal(summary.missingDistanceRuns, 1);
    assert.equal(summary.paceMinutesPerKm, null);
    assert.equal(runPace(run(extra)), null);
  }
  const zero = runningMetrics([run({ distanceMeters: 0 })], today);
  assert.equal(zero.knownDistanceMeters, 0);
  assert.equal(zero.knownDistanceRuns, 1);
  assert.equal(zero.missingDistanceRuns, 0);
  assert.equal(zero.paceRuns, 0);
  assert.equal(zero.paceMinutesPerKm, null);
  assert.equal(runPace(run({ distanceMeters: 0 })), null);
});

test('30 calendar days are inclusive and use the supplied server day across year and leap boundaries', () => {
  const workouts = ['2026-12-04', '2026-12-05', '2027-01-03', '2027-01-04'].map(date => run({ date, distanceMeters: 1000 }));
  const summary = runningMetrics(workouts, '2027-01-03');
  assert.equal(summary.from, '2026-12-05');
  assert.equal(summary.through, '2027-01-03');
  assert.equal(summary.totalRuns, 2);
  assert.equal(summary.totalMinutes, 60);
  assert.equal(summary.knownDistanceMeters, 2000);
  const leap = runningMetrics(['2024-02-28', '2024-02-29', '2024-03-29', '2024-03-30'].map(date => run({ date })), '2024-03-29');
  assert.equal(leap.from, '2024-02-29');
  assert.equal(leap.totalRuns, 2);
});

test('an unavailable server date never falls back to the device clock', () => {
  for (const value of [undefined, null, '', '2026-02-30', '2026-1-7', 'not-a-date']) {
    const summary = runningMetrics([run()], value);
    assert.equal(summary.from, null);
    assert.equal(summary.through, null);
    assert.equal(summary.totalRuns, 0);
    assert.equal(summary.paceMinutesPerKm, null);
  }
});

test('invalid observations and other activity types cannot introduce NaN, Infinity or fake running totals', () => {
  const invalid = [
    null, {}, run({ kind: 'walking' }), run({ kind: 'cycling' }), run({ date: '2026-09-31' }),
    ...[0, -1, 1441, NaN, Infinity, '30', null].map(minutes => run({ minutes, distanceMeters: 1000 })),
  ];
  assert.equal(runningMetrics(invalid, today).totalRuns, 0);
  for (const workout of invalid) assert.equal(runPace(workout), null);
  for (const distanceMeters of [-1, 1000001, NaN, Infinity, '5000']) {
    const summary = runningMetrics([run({ distanceMeters })], today);
    assert.equal(summary.totalRuns, 1);
    assert.equal(summary.knownDistanceMeters, null);
    assert.equal(summary.paceMinutesPerKm, null);
    assert.equal(runPace(run({ distanceMeters })), null);
  }
});

test('per-run pace and display use recorded minutes and round seconds without a 60-second component', () => {
  assert.equal(runPace(run({ minutes: 27.5, distanceMeters: 5000 })), 5.5);
  assert.equal(formatPace(5.5), '5:30');
  assert.equal(formatPace(6 + 59.8 / 60), '7:00');
  assert.equal(formatPace(75.25), '75:15');
  for (const value of [undefined, null, NaN, Infinity, 0, -1, '5.5', Number.MAX_VALUE]) assert.equal(formatPace(value), '—');
});

test('activities render a labelled 30-day summary and per-run pace without inventing missing distance', () => {
  const state = { today, workouts: [
    run({ id: 'measured', distanceMeters: 5000 }),
    run({ id: 'missing', minutes: 20, distanceMeters: null }),
    run({ id: 'zero', minutes: 10, distanceMeters: 0 }),
    run({ id: 'old', date: '2026-09-17', startedAt: '2026-09-17T08:00:00+03:00', distanceMeters: 2000 }),
    run({ id: 'cycling', kind: 'cycling', distanceMeters: 20000 }),
  ] };
  const feature = createFeatures({ getState: () => state });
  const html = feature.activities();
  assert.match(html, /Бег за 30 дней/);
  assert.match(html, /18 сентября — 17 октября/);
  assert.match(html, /3<span>всего/);
  assert.match(html, /Записано минут: 60/);
  assert.match(html, /5<span>км/);
  assert.match(html, /6:00<\/p><p class="stat-foot">мин\/км · записей: 1/);
  assert.match(html, /1 без дистанции/);
  assert.match(html, /6:00 мин\/км/);
  assert.match(html, /Дистанция не указана/);
  assert.match(html, /0 км/);
  assert.match(html, /data-id="old"/, 'the summary period does not remove older imported activities');
  const cyclingRow = html.match(/<article class="activity-row">(?:(?!<\/article>)[\s\S])*data-id="cycling"[\s\S]*?<\/article>/)?.[0];
  assert.ok(cyclingRow);
  assert.doesNotMatch(cyclingRow, /мин\/км/);
  assert.doesNotMatch(html, /NaN|Infinity|undefined/);
});

test('empty and zero-distance activity summaries show honest missing pace', () => {
  const empty = createFeatures({ getState: () => ({ today, workouts: [] }) }).activities();
  assert.match(empty, /0<span>всего/);
  assert.match(empty, /Нет записанной дистанции/);
  assert.match(empty, /Темп появится с дистанцией/);
  const zero = createFeatures({ getState: () => ({ today, workouts: [run({ distanceMeters: 0 })] }) }).activities();
  assert.match(zero, /0<span>км/);
  assert.match(zero, /—<\/p><p class="stat-foot">Темп появится с дистанцией/);
});
