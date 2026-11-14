import test from 'node:test';
import assert from 'node:assert/strict';
import { dailyHistory, historySummary } from '../static/progress.js';
import { overview, insights } from '../static/views.js';
import { isoDate, monday, addDays } from '../static/format.js';

const today = '2026-10-17';
test('missing observations stay unknown rather than lowering the average', () => {
  const history = dailyHistory([{ date: today, sleep: 8 }], 7, today);
  assert.equal(history.length, 7);
  assert.equal(history[0].sleep, null);
  assert.equal(historySummary(history).averageSleep, 8);
  assert.equal(historySummary(history).sleepDays, 1);
  assert.equal(historySummary(history).averageFatigue, null);
});

test('multiple observations on one day count once with independent latest signals', () => {
  const entries = [
    { date: '2026-10-16', sleep: 8, fatigue: 2 },
    { date: today, sleep: 5, fatigue: 9 },
    { date: today, sleep: 7 },
    { date: today, fatigue: 4 },
    { date: '2026-10-14', status: 'done', sessionId: 'older' },
  ];
  const copy = structuredClone(entries);
  const history = dailyHistory(entries, 7, today);
  assert.deepEqual(history.at(-1), { date: today, logged: true, sleep: 7, fatigue: 4, completed: 0 });
  assert.equal(historySummary(history).averageSleep, 7.5);
  assert.equal(historySummary(history).averageFatigue, 3);
  assert.equal(historySummary(history).loggedDays, 3);
  assert.deepEqual(entries, copy, 'presentation never mutates persisted facts');
});

test('zero hours is a recorded value and never treated as missing', () => {
  const summary = historySummary(dailyHistory([{ date: today, sleep: 0 }], 7, today));
  assert.equal(summary.averageSleep, 0);
  assert.equal(summary.sleepDays, 1);
});

test('period boundaries exclude future and old records across month/year changes', () => {
  const entries = ['2026-12-27', '2026-12-28', '2027-01-03', '2027-01-04'].map(date => ({ date, sleep: 8 }));
  const history = dailyHistory(entries, 7, '2027-01-03');
  assert.equal(history[0].date, '2026-12-28');
  assert.equal(historySummary(history).loggedDays, 2);
  const month = dailyHistory(entries, 30, '2027-01-03');
  assert.equal(month[0].date, '2026-12-05');
  assert.equal(historySummary(month).loggedDays, 3);
});

test('corrected or repeated sessions do not inflate completed count', () => {
  const entries = [
    { date: today, sessionId: 'a', status: 'done' },
    { date: today, sessionId: 'a', status: 'missed' },
    { date: today, sessionId: 'b', status: 'done' },
    { date: today, sessionId: 'b', status: 'done' },
    { date: today, status: 'wellbeing', fatigue: 5 },
  ];
  assert.equal(historySummary(dailyHistory(entries, 7, today)).completed, 1);
});

test('empty account does not display invented history or progress', () => {
  const state = { profile: null, entries: [] };
  const html = insights(state, 30);
  assert.match(html, /нет данных/);
  assert.match(html, /Здесь появятся ваши отметки/);
  assert.match(overview(state), /Создать мой план/);
  assert.match(overview(state), /href="#week">Посмотреть пример/);
  assert.doesNotMatch(overview(state), /class="stats"|Пример недели:|135/);
  assert.doesNotMatch(overview(state), /Выполнено 3 из 3/);
});

test('today focus distinguishes rest, planned, completed and missed sessions', () => {
  const date = isoDate();
  const base = { profile: { name: '<img src=x>', goal: 'tone' }, entries: [], plan: { weekStart: monday(), totalMinutes: 45, sessions: [] } };
  assert.match(overview(base), /Сегодня — день отдыха/);
  for (const status of ['planned', 'done', 'missed']) {
    const state = { ...base, plan: { ...base.plan, sessions: [{ id: date, date, day: 0, kind: 'strength', title: 'Силовая тренировка', minutes: 45, status }] } };
    const html = overview(state);
    assert.match(html, status === 'planned' ? /Отметить занятие/ : /Изменить отметку/);
    assert.match(html, status === 'done' ? /Выполнено 1 из 1/ : /Выполнено 0 из 1/);
    assert.doesNotMatch(html, /<img src=x>/);
  }
});

test('month history never invents historical duration using the current profile', () => {
  const oldDate = addDays(isoDate(), -15);
  const html = insights({ entries: [{ date: oldDate, status: 'done', sessionId: oldDate }], profile: { minutes: 90 }, plan: null }, 30);
  assert.match(html, /1<span>за 30 дней/);
  assert.doesNotMatch(html, /90 минут/);
});
