import test from 'node:test';
import assert from 'node:assert/strict';
import { productHelpView } from '../static/product-help.js';

test('help distinguishes guest access, Telegram identity and explicit diary clearing', () => {
  const html = productHelpView({ identity: 'guest', timeZone: 'Europe/Moscow' });
  assert.match(html, /гостевой браузер/);
  assert.match(html, /отдельный дневник/);
  assert.match(html, /логин и сеансы сохраняются/);
  assert.match(html, /data-action="export"/);
  assert.match(html, /href="#week"/);
  assert.match(html, /не делает дневник автономным/);
});

test('help escapes server-supplied timezone and stays independent of private history', () => {
  const state = { identity: 'account', timeZone: '<img src=x onerror=alert(1)>', entries: [{ secret: 'private diary fact' }] };
  const before = structuredClone(state);
  const html = productHelpView(state);
  assert.doesNotMatch(html, /<img|private diary fact/);
  assert.match(html, /&lt;img/);
  assert.deepEqual(state, before);
});
