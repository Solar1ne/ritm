// Read-only synthetic browser fixture. No database, credentials, or AI provider is used.
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(process.env.RHYTHM_STATIC_DIR || fileURLToPath(new URL('../frontend/static/', import.meta.url)));
const session = { id: '2026-10-22', date: '2026-10-22', day: 3, kind: 'strength', title: 'Силовая тренировка', minutes: 45, status: 'planned' };
const plan = { weekStart: '2026-10-19', totalMinutes: 45, sessions: [session] };
const state = { revision: 1, sessionBinding: 'synthetic-fixture', identity: 'guest', accountsEnabled: true, today: '2026-10-22', timeZone: 'Europe/Moscow', historyEditableFrom: '2026-07-24', currentWeekStart: '2026-10-19', profile: { name: 'Synthetic Date Fixture', goal: 'tone', experience: 'beginner', days: [3], minutes: 45 }, plan, nextPlan: { ...plan, weekStart: '2026-10-26', sessions: [{ ...session, id: '2026-10-29', date: '2026-10-29' }] }, entries: [{ id: 'sleep', date: '2026-10-22', status: 'wellbeing', sleep: 8 }], workouts: [], weeks: [], hrvEntries: [], bodyEntries: [], mealSelections: [], reminder: {}, ai: { configured: false } };
const server = createServer(async (request, response) => {
  if (request.url === '/api/state') { response.writeHead(200, { 'Content-Type': 'application/json' }); response.end(JSON.stringify(state)); return; }
  if (request.url.startsWith('/api/')) { response.writeHead(501); response.end('Synthetic read-only fixture'); return; }
  const path = resolve(root, request.url === '/' ? 'index.html' : '.' + request.url.split('?')[0]);
  if (!path.startsWith(root + '/')) { response.writeHead(404); response.end(); return; }
  try { const content = await readFile(path); response.writeHead(200, { 'Content-Type': ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.ttf': 'font/ttf' })[extname(path)] || 'application/octet-stream' }); response.end(content); }
  catch { response.writeHead(404); response.end(); }
});
await new Promise((resolve, reject) => { server.once('error', reject); server.listen(18085, '127.0.0.1', resolve); });
let browser;
try {
browser = await chromium.launch({ channel: 'chrome', headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = []; page.on('pageerror', e => errors.push(e.message));
await page.addInitScript(() => {
  const OriginalDate = Date;
  window.Date = class DeviceClock extends OriginalDate {
    constructor(...args) { super(...(args.length ? args : ['2026-10-23T10:00:00Z'])); }
    static now() { return new OriginalDate('2026-10-23T10:00:00Z').getTime(); }
  };
});
  await page.goto('http://127.0.0.1:18085', { waitUntil: 'networkidle' });
  await page.locator('[data-action=checkin]').first().click();
  assert.equal(await page.locator('#entry-form [name=date]').inputValue(), '2026-10-22', 'diary default must be the server day');
  assert.equal(await page.locator('#entry-form [name=date]').getAttribute('max'), '2026-10-22', 'diary must not allow future device dates');
  await page.getByRole('button', { name: 'Закрыть', exact: true }).click();
  await page.locator('.focus-actions [data-action=session]').click();
  await page.locator('#entry-form').waitFor();
  assert.equal(await page.locator('#entry-form [name=date]').inputValue(), '2026-10-22');
  await page.getByRole('button', { name: 'Закрыть', exact: true }).click();
  await page.goto('http://127.0.0.1:18085/#insights', { waitUntil: 'networkidle' });
  await page.locator('[data-action=history-day][data-id="2026-10-22"]').first().click();
  assert.equal(await page.getByRole('button', { name: 'Отметить сегодня', exact: true }).count(), 1, 'history details must recognize server today');
  assert.deepEqual(errors, []);
  console.log('PASS synthetic read-only browser: server-date diary defaults, allowed today session, history action; no runtime errors');
} finally {
  try { await browser?.close(); }
  finally { await new Promise(resolve => server.close(resolve)); }
}
