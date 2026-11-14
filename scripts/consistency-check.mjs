// Real local Go/PostgreSQL acceptance with synthetic accounts; no AI or Telegram.
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
import { addDays } from '../frontend/static/format.js';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:18081';
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname));
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/consistency-2026-10-22/browser');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const contexts = [], checks = [], errors = [], writes = [];
const check = name => { checks.push({ name, passed: true }); console.log(`PASS ${name}`); };
async function snapshot(context) {
  const response = await context.request.get(`${base}/api/state`);
  assert.equal(response.status(), 200);
  return response.json();
}
async function mutate(context, path, current, fields) {
  const response = await context.request.post(`${base}${path}`, {
    headers: { Origin: base, 'X-Rhythm-Session': current.sessionBinding },
    data: { revision: current.revision, ...fields },
  });
  assert.equal(response.status(), 200, await response.text());
  return response.json();
}
const source = await browser.newContext(); contexts.push(source);
const target = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' }); contexts.push(target);
const page = await target.newPage();
page.on('pageerror', error => errors.push(error.message));
const close = () => page.getByRole('button', { name: 'Закрыть просмотр', exact: true }).click();
try {
  let current = await snapshot(source);
  current = await mutate(source, '/api/profile', current, { profile: { name: 'UI fixture Календарь', goal: 'tone', experience: 'beginner', days: [0, 2, 4], minutes: 45 } });
  const exported = await source.request.get(`${base}/api/export`, { headers: { 'X-Rhythm-Session': current.sessionBinding } });
  assert.equal(exported.status(), 200);
  const backup = await exported.json();
  const previous = addDays(current.currentWeekStart, -7), older = addDays(current.currentWeekStart, -42);
  const makeWeek = weekStart => {
    const saved = structuredClone(backup.weeks[0]);
    saved.weekStart = saved.plan.weekStart = weekStart;
    saved.plan.sessions = saved.plan.sessions.map(session => ({ ...session, id: addDays(weekStart, session.day), date: addDays(weekStart, session.day) }));
    return saved;
  };
  backup.weeks.unshift(makeWeek(older), makeWeek(previous));
  backup.entries = [
    { id: 'older-done', date: older, sessionId: older, status: 'done' },
    { id: 'previous-done', date: previous, sessionId: previous, status: 'done', sleep: 0, fatigue: 4 },
    { id: 'previous-missed', date: addDays(previous, 2), sessionId: addDays(previous, 2), status: 'missed' },
    { id: 'previous-sunday', date: addDays(previous, 6), status: 'wellbeing', sleep: 8 },
  ];
  backup.sessionAdjustments = [{ sessionId: previous, date: previous, variantId: 'home-mobility-v1', minutes: 15, environment: 'home', lowImpact: true, updatedAt: `${previous}T12:00:00Z` }];
  let restored = await mutate(target, '/api/restore', await snapshot(target), { backup });
  restored = await mutate(target, '/api/import', restored, { format: 'csv', content: `id,date,kind,minutes\ncalendar-linked,${previous},walking,20\ncalendar-other,${addDays(previous, 1)},walking,25\n`, completePlanned: false });
  // Changing future defaults must not invent historical 90-minute completions.
  restored = await mutate(target, '/api/profile', restored, { profile: { ...restored.profile, minutes: 90 } });
  const before = await snapshot(target);
  page.on('request', request => {
    if (new URL(request.url()).origin === base && !['GET', 'HEAD'].includes(request.method())) writes.push({ method: request.method(), url: request.url() });
  });
  await page.goto(`${base}/#insights`, { waitUntil: 'networkidle' });
  await page.locator('.consistency-calendar').waitFor();
  assert.equal(await page.locator('[data-action=activity-day]').count(), 30);
  const recap = page.locator('.consistency-recap');
  assert.deepEqual(await recap.locator('.consistency-counts dd').allTextContents(), ['1', '1', '1']);
  assert.match(await recap.innerText(), /15 плановых минут/);
  assert.match(await recap.innerText(), /4.*ч/s);
  check('Closed-week recap counts done/missed/unlogged independently and uses saved adjusted minutes');

  await page.locator(`[data-action=activity-day][data-id="${previous}"]`).click();
  assert.match(await page.locator('dialog').innerText(), /Занятие с заменой/);
  assert.match(await page.locator('dialog').innerText(), /15 мин по плану/);
  assert.match(await page.locator('dialog').innerText(), /0 ч/);
  assert.match(await page.locator('dialog').innerText(), /20 мин из файла/);
  await close();
  check('Day detail preserves zero sleep and separates imported duration from completed plan minutes');

  await page.locator(`[data-action=activity-day][data-id="${addDays(previous, 4)}"]`).click();
  assert.match(await page.locator('dialog').innerText(), /Нет отметки/);
  assert.doesNotMatch(await page.locator('dialog').innerText(), /Пропущено/);
  await close();
  assert.match(await page.locator(`[data-action=activity-day][data-id="${addDays(previous, 6)}"]`).getAttribute('aria-label'), /Отдых по плану/);
  assert.match(await page.locator(`[data-action=activity-day][data-id="${addDays(previous, 1)}"]`).getAttribute('aria-label'), /Импортированная активность/);
  check('Rest, unmarked scheduled days and imported activity keep distinct accessible states');

  const ninety = page.locator('[data-action=activity-period][data-days="90"]');
  await ninety.click();
  assert.equal(await page.locator('[data-action=activity-day]').count(), 90);
  assert.equal(await ninety.getAttribute('aria-pressed'), 'true');
  assert.equal(await ninety.evaluate(element => element === document.activeElement), true);
  await page.locator(`[data-action=activity-day][data-id="${older}"]`).click();
  assert.match(await page.locator('dialog').innerText(), /45 мин по плану/);
  await close();
  check('90-day selection keeps focus and opens a day older than the existing 30-day wellbeing window');

  await page.locator('[data-action=history-period][data-days="30"]').click();
  assert.equal(await page.locator('.history-point').count(), 60);
  assert.equal(await page.locator('[data-action=activity-day]').count(), 90);
  await page.locator('[data-action=activity-period][data-days="30"]').click();
  assert.equal(await page.locator('.history-point').count(), 60);
  check('Activity and wellbeing periods are independent');

  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `page overflow at ${width}`);
    assert.equal(await page.locator('.consistency-day').evaluateAll(elements => elements.every(element => { const r = element.getBoundingClientRect(); return r.width >= 44 && r.height >= 44; })), true);
    if (width === 390) await page.locator('.consistency-layout').screenshot({ path: resolve(output, 'calendar-mobile.png') });
  }
  await page.locator('.consistency-layout').screenshot({ path: resolve(output, 'calendar-desktop.png') });
  await page.evaluate(() => { document.documentElement.dataset.theme = 'dark'; });
  await page.locator('.consistency-layout').screenshot({ path: resolve(output, 'calendar-dark.png') });
  check('Calendar fits 320/390/768/1440px with 44px targets, internal scrolling and dark-theme CSS');

  const after = await snapshot(target);
  assert.deepEqual(after, before);
  assert.deepEqual(writes, []);
  assert.deepEqual(errors, []);
  check('Browsing, periods and details are read-only and cause no runtime errors');
  await writeFile(resolve(output, 'browser.json'), JSON.stringify({ generatedAt: new Date().toISOString(), scope: 'Real local Go/PostgreSQL and Chrome, synthetic archives and CSV only; no live AI/Telegram', checks, errors, writes }, null, 2) + '\n');
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  try {
    for (const context of contexts) {
      const current = await snapshot(context);
      if (!current.profile?.name?.startsWith('UI fixture')) continue;
      const response = await context.request.delete(`${base}/api/data`, { headers: { 'X-Rhythm-Session': current.sessionBinding }, data: { revision: current.revision } });
      assert.equal(response.status(), 200, 'synthetic fixture cleanup');
    }
  } finally { await browser.close(); }
}
