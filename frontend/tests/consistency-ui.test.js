import test from 'node:test';
import assert from 'node:assert/strict';
import { consistencyView, consistencyDayContent } from '../static/consistency-ui.js';

const today = '2026-10-22';
const session = (date, minutes = 45, extra = {}) => ({ id: date, date, day: new Date(`${date}T12:00:00Z`).getUTCDay() - 1, kind: 'strength', title: 'Силовая тренировка', minutes, detailsKnown: true, status: 'planned', ...extra });
function state() {
  return {
    today, currentWeekStart: '2026-10-19', profile: { goal: 'tone' },
    entries: [
      { id: 'done', date: '2026-10-12', sessionId: '2026-10-12', status: 'done', sleep: 0 },
      { id: 'missed', date: '2026-10-14', sessionId: '2026-10-14', status: 'missed', sleep: 8, fatigue: 4 },
    ],
    weeks: [{ weekStart: '2026-10-12', source: 'baseline', profile: { goal: 'tone', days: [0, 2, 4], minutes: 45 }, plan: { weekStart: '2026-10-12', sessions: [session('2026-10-12'), session('2026-10-14'), session('2026-10-16')], totalMinutes: 135 } }],
    plan: { weekStart: '2026-10-19', sessions: [session(today), session('2026-10-23')], totalMinutes: 90 },
    workouts: [{ id: 'run', date: '2026-10-12', startedAt: '2026-10-12T10:00:00Z', kind: 'run', source: 'tcx', minutes: 52, sessionId: '2026-10-12' }],
  };
}
const dayButtons = html => [...html.matchAll(/<button\b[^>]*data-action="activity-day"[^>]*>[\s\S]*?<\/button>/g)].map(match => match[0]);

test('calendar exposes the selected 30 or 90 server days as readable day buttons', () => {
  for (const [days, first] of [[30, '2026-09-23'], [90, '2026-07-25']]) {
    const html = consistencyView(state(), days), buttons = dayButtons(html);
    assert.equal(buttons.length, days);
    assert.match(buttons[0], new RegExp(`data-id="${first}"`));
    assert.match(buttons.at(-1), /data-id="2026-10-22"/);
    assert.doesNotMatch(html, /data-id="2026-10-23"/);
    assert.match(html, new RegExp(`data-action="activity-period" data-days="${days}" aria-pressed="true"`));
    assert.match(buttons.at(-1), /aria-current="date"/);
    assert.match(buttons.at(-1), /aria-label="[^"]*22 октября[^"]*По плану сегодня/);
    assert.match(buttons[0], />23<|>25</);
  }
});

test('calendar distinguishes completed, missed, unlogged, rest and unknown without color alone', () => {
  const html = consistencyView(state());
  for (const [date, label] of [['2026-10-12', 'Выполнено'], ['2026-10-14', 'Пропущено'], ['2026-10-16', 'Нет отметки'], ['2026-10-13', 'Отдых по плану'], ['2026-09-23', 'Расписание неизвестно']]) {
    const button = dayButtons(html).find(value => value.includes(`data-id="${date}"`));
    assert.match(button, new RegExp(`aria-label="[^"]*${label}`));
    assert.match(button, /class="consistency-day-symbol" aria-hidden="true">[^<]+</);
    assert.ok(html.includes(label), `visible legend explains ${label}`);
  }
  assert.match(html, /Пн[\s\S]*Вт[\s\S]*Ср[\s\S]*Чт[\s\S]*Пт[\s\S]*Сб[\s\S]*Вс/);
});

test('previous week recap separates planned minutes from imported minutes and observes zero sleep', () => {
  const html = consistencyView(state());
  assert.match(html, /Прошлая неделя/);
  assert.match(html, /12 — 18 октября/);
  assert.match(html, /Выполнено[\s\S]*?>1</);
  assert.match(html, /Пропущено[\s\S]*?>1</);
  assert.match(html, /Нет отметки[\s\S]*?>1</);
  assert.match(html, /45[\s\S]*плановых минут|плановых минут[\s\S]*45/);
  assert.match(html, /4[\s\S]*ч[\s\S]*2[\s\S]*отмет/);
  assert.doesNotMatch(html, /97|135[\s\S]*выполнено|100%/);
});

test('a complete weekly recap names the saved scheduled count without making rest a task', () => {
  const html = consistencyView(state());
  assert.match(html, /В сохранённом плане: 3 занятия/);
  assert.match(html, /Дни отдыха не входят в число занятий/);
});

test('a partial legacy schedule never looks like a complete plan or measured duration', () => {
  const value = state();
  value.weeks = [{ weekStart: '2026-10-12', source: 'legacy', plan: { weekStart: '2026-10-12', sessions: [session('2026-10-12', 0, { detailsKnown: false })], totalMinutes: 0 } }];
  const html = consistencyView(value);
  assert.match(html, /част|неполн/i);
  assert.match(html, /длительность[\s\S]*неизвестна|неизвестной длительностью/);
  assert.doesNotMatch(html, /\d+\s*%|1 из 1|0 плановых минут/);
});

test('missing previous schedule explains absence rather than inventing a plan', () => {
  const value = { today, entries: [], weeks: [], workouts: [] }, html = consistencyView(value);
  assert.match(html, /Прошлая неделя/);
  assert.match(html, /расписание[\s\S]*не сохранено|нет сохранённого расписания/i);
  assert.doesNotMatch(html, /\d+\s*%|0 из 0|135/);
});

test('read-only day details escape titles and keep imported activity separate from the plan', () => {
  const value = state();
  value.weeks[0].plan.sessions[0].title = '<img src=x onerror=alert(1)>';
  value.workouts[0].source = '<svg onload=alert(2)>';
  const html = consistencyDayContent(value, '2026-10-12');
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.match(html, /&lt;svg onload=alert\(2\)&gt;/);
  assert.doesNotMatch(html, /<img|<svg onload|<form|data-action=/);
  assert.match(html, /45[\s\S]*план|план[\s\S]*45/);
  assert.match(html, /Импортированные активности/);
  assert.match(html, /52[\s\S]*мин/);
  assert.match(html, /Сон[\s\S]*0 ч/);
  assert.match(html, /Усталость[\s\S]*Нет данных/);
  assert.doesNotMatch(html, /97/);
});

test('day details reject future, older and malformed dates outside the server window', () => {
  const value = state();
  for (const date of ['2026-10-23', '2026-07-24', 'not-a-date', '2026-02-30', '<img>']) assert.equal(consistencyDayContent(value, date), null);
  assert.equal(typeof consistencyDayContent(value, '2026-07-25'), 'string');
  assert.match(consistencyDayContent(value, '2026-09-23'), /Расписание неизвестно/);
});

test('historical substitutions identify the replacement and label the original title explicitly', () => {
  const value = state();
  value.weeks[0].plan.sessions[0].title = '<script>old</script>';
  value.sessionAdjustments = [{ date: '2026-10-12', sessionId: '2026-10-12', variantId: 'strength-mobility-internal-id', environment: 'home', minutes: 15 }];
  const html = consistencyDayContent(value, '2026-10-12');
  assert.match(html, /Занятие с заменой/);
  assert.match(html, /15 мин по плану/);
  assert.match(html, /Исходно: &lt;script&gt;old&lt;\/script&gt;/);
  assert.doesNotMatch(html, /strength-mobility-internal-id|<script>/);
});
