import test from 'node:test';
import assert from 'node:assert/strict';
import { dataUsageView } from '../static/data-usage.js';

const limits = { entries: 10, workouts: 10, hrvEntries: 10, bodyEntries: 10, mealSelections: 10, weeks: 20 };
test('capacity renders exact server limits and counts without mutating state', () => {
  const state = { limits: Object.freeze(limits), entries: Object.freeze([{}]), weeks: Object.freeze([{}, {}]) };
  const before = JSON.stringify(state);
  const html = dataUsageView(Object.freeze(state));
  assert.match(html, /1 из 10/);
  assert.match(html, /2 из 20/);
  assert.equal((html.match(/data-capacity-state=/g) || []).length, 6);
  assert.equal(JSON.stringify(state), before);
});
test('near and full limits show warnings and safe existing actions', () => {
  for (const [count, status] of [[7, 'available'], [8, 'near'], [10, 'full'], [11, 'full']]) {
    const html = dataUsageView({ limits, entries: Array(count).fill({}) });
    assert.match(html, new RegExp(`data-collection="entries" data-capacity-state="${status}"`));
    assert.match(html, /data-action="export"/);
    assert.match(html, /data-action="menu-route" data-route="journal"/);
    assert.match(html, /Экспорт не освобождает место/);
    assert.doesNotMatch(html, /data-action="delete/);
  }
});
test('missing or invalid limits never invent capacity or interpolate untrusted metadata', () => {
  for (const value of [undefined, 0, -1, Infinity, '500', '<img src=x onerror=alert(1)>']) {
    const html = dataUsageView({ limits: { entries: value }, entries: [{ note: '<script>private</script>' }], profile: { name: 'private' } });
    assert.match(html, /Лимит не получен/);
    assert.doesNotMatch(html, /<script>|<img|private|NaN|Infinity|МБ|MiB/);
  }
});
