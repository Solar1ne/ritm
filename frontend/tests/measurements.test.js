import { test } from 'node:test';
import assert from 'node:assert/strict';
import { measurementGroups, observations, trend, bodyComposition } from '../static/measurements.js';

const today = '2026-10-17';
const hrv = (date, extra = {}) => ({ id: date, date, valueMs: 42, metric: 'rmssd', window: 'overnight', source: 'Часы A', ...extra });

test('HRV comparisons never merge different metrics, windows or sources', () => {
  const groups = measurementGroups([hrv(today), hrv(today, { metric: 'sdnn' }), hrv(today, { window: 'five_minutes' }), hrv(today, { source: 'Часы B' })], 'hrv', today);
  assert.equal(groups.length, 4);
  assert.ok(groups.every(group => group.entries.length === 1));
});

test('body series keeps measurement methods separate and owns its returned data', () => {
  const input = [{ date: today, weightKg: 70, method: 'bia', source: 'Весы' }, { date: '2026-10-16', weightKg: 71, method: 'dxa', source: 'Весы' }];
  const groups = measurementGroups(input, 'body', today);
  assert.equal(groups.length, 2);
  groups[0].entries[0].weightKg = 100;
  assert.equal(input[0].weightKg, 70);
});

test('a 30-day period includes its first day, excludes future and malformed dates', () => {
  const groups = measurementGroups([hrv('2026-09-18'), hrv('2026-09-17'), hrv('2026-10-18'), hrv('2026-02-30')], 'hrv', today);
  assert.equal(groups.length, 1);
  assert.deepEqual(groups[0].entries.map(entry => entry.date), ['2026-09-18']);
  assert.deepEqual(measurementGroups([hrv(today)], 'hrv', undefined), []);
  assert.equal(measurementGroups([hrv('2025-01-01')], 'hrv', today, 0)[0].entries.length, 1);
});

test('unknown and nonfinite values do not enter trends or become zero', () => {
  const entries = [{ date: '2026-10-15', weightKg: 75 }, { date: '2026-10-16', bodyFatPercent: 20 }, { date: today, weightKg: 74 }];
  const result = trend(observations(entries, 'weightKg'));
  assert.equal(result.count, 2); assert.equal(result.delta, -1); assert.equal(result.latest.value, 74);
  assert.deepEqual(observations([{ date: today, weightKg: null }, { date: today, weightKg: Infinity }], 'weightKg'), []);
  assert.equal(trend([]).latest, null); assert.equal(trend([{ date: today, value: 70 }]).delta, null);
});

test('body composition is derived only from one complete measurement, never stitched across dates', () => {
  assert.equal(bodyComposition([{ date: today, weightKg: 70 }, { date: '2026-10-16', bodyFatPercent: 20 }]), null);
  const result = bodyComposition([{ date: '2026-10-16', weightKg: 80, bodyFatPercent: 25 }, { date: today, weightKg: 79 }]);
  assert.deepEqual(result, { date: '2026-10-16', fatKg: 20, otherKg: 60 });
});

test('out-of-range records and missing series context cannot create misleading comparisons', () => {
  assert.deepEqual(measurementGroups([hrv(today, { valueMs: 0 }), hrv(today, { metric: 'unknown' }), hrv(today, { source: '' })], 'hrv', today), []);
  assert.deepEqual(measurementGroups([{ date: today, weightKg: -1, method: 'bia', source: 'Весы' }], 'body', today), []);
});
