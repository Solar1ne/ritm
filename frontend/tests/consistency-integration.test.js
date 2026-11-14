import test from 'node:test';
import assert from 'node:assert/strict';
import { insights } from '../static/views.js';

const state = {
  today: '2026-10-22', currentWeekStart: '2026-10-19',
  profile: { name: 'Пример', days: [0], minutes: 90 },
  entries: [], weeks: [], workouts: [], sessionAdjustments: [],
};

test('progress adds a thirty-day activity calendar without replacing wellbeing charts', () => {
  const html = insights(state, 7);
  assert.equal((html.match(/data-action="activity-day"/g) || []).length, 30);
  assert.match(html, /Прошлая неделя/);
  assert.equal((html.match(/class="history-point /g) || []).length, 14);
  assert.match(html, /aria-label="Период календаря активности"/);
});

test('activity period is independent of the existing wellbeing period', () => {
  const before = structuredClone(state);
  const html = insights(state, 30, 90);
  assert.equal((html.match(/data-action="activity-day"/g) || []).length, 90);
  assert.equal((html.match(/class="history-point /g) || []).length, 60);
  assert.match(html, /data-days="90" aria-pressed="true"/);
  assert.deepEqual(state, before);
});
