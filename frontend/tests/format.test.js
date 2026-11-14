import test from 'node:test';
import assert from 'node:assert/strict';
import { escapeHTML, addDays, monday, isoDate, plural, latestWellbeing } from '../static/format.js';

test('names cannot insert HTML through text or attribute contexts', () => {
  assert.equal(escapeHTML('<img src=x onerror="alert(1)"> & \'Hi\''), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt; &amp; &#39;Hi&#39;');
});
test('calendar stays in Moscow across UTC midnight', () => {
  assert.equal(isoDate(new Date('2026-10-16T22:00:00Z')), '2026-10-17');
  assert.equal(monday('2026-10-18'), '2026-10-12');
  assert.equal(monday('2026-10-19'), '2026-10-19');
  assert.equal(addDays('2026-12-28', 6), '2027-01-03');
});
test('Russian plural includes teens and compound numbers', () => {
  const forms = ['отметка', 'отметки', 'отметок'];
  for (const [n, expected] of [[1, forms[0]], [2, forms[1]], [5, forms[2]], [11, forms[2]], [12, forms[2]], [21, forms[0]], [23, forms[1]]]) assert.equal(plural(n, forms), expected);
});
test('backfilled observations do not replace the latest wellbeing', () => {
  const newest = { date: '2026-10-17', sleep: 8 }, older = { date: '2026-10-12', sleep: 5 };
  assert.equal(latestWellbeing([newest, older]), newest);
  const corrected = { date: '2026-10-17', sleep: 6 };
  assert.equal(latestWellbeing([newest, older, corrected]), corrected);
  assert.equal(latestWellbeing([{ date: '2026-10-18', status: 'done' }]), undefined);
  assert.equal(latestWellbeing([newest, { date: '2026-10-17', fatigue: 3 }], 'sleep'), newest);
});
