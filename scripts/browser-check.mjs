// Isolated synthetic browser fixtures. Requires Playwright and Chrome.
// PLAYWRIGHT_MODULE can point to a preinstalled Playwright package.
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';
import { isoDate } from '../frontend/static/format.js';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:8080';
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname), 'Browser fixtures only run against a local server');
const output = process.env.RHYTHM_EVIDENCE_DIR ? pathToFileURL(`${resolve(process.env.RHYTHM_EVIDENCE_DIR)}/`) : new URL('../docs/evidence/', import.meta.url);
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const checks = [], pageErrors = [];
const check = name => { checks.push({ name, passed: true }); console.log(`PASS ${name}`); };
const snapshot = async context => (await context.request.get(`${base}/api/state`)).json();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 1080 }, reducedMotion: 'reduce' });
const page = await ctx.newPage();
page.on('pageerror', error => pageErrors.push(error.message));
let ownCookies;
async function cleanup(context) {
  const record = await snapshot(context);
  if (!record.profile || !record.profile.name.startsWith('UI fixture')) return;
  const response = await context.request.delete(`${base}/api/data`, { headers: { 'X-Rhythm-Session': record.sessionBinding }, data: { revision: record.revision } });
  assert.equal(response.status(), 200, 'fixture cleanup');
}
try {
  await page.goto(base, { waitUntil: 'networkidle' });
  await page.getByRole('heading', { name: 'Начните с малого. Найдите свой ритм.', exact: true }).waitFor();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  await page.screenshot({ path: new URL('desktop.png', output).pathname, fullPage: true });
  check('Desktop 1440px: explicit example, no horizontal overflow');

  await page.getByRole('button', { name: 'Создать мой план', exact: true }).click();
  await page.getByLabel('Как к вам обращаться?').fill('UI fixture Алексей');
  await page.locator('#profile-form').getByRole('button', { name: 'Создать мой план', exact: false }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  let data = await snapshot(ctx);
  assert.equal(data.plan.sessions.length, 3); assert.equal(data.plan.totalMinutes, 135); assert.equal(data.entries.length, 0);
  check('Profile form creates real persisted baseline: 3 × 45 minutes');

  await page.getByRole('button', { name: 'Устал', exact: true }).click();
  await page.getByLabel('Сколько часов вы спали?').fill('5');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  data = await snapshot(ctx);
  assert.equal(data.decision.mode, 'recover'); assert.equal(data.nextPlan.totalMinutes, 72); assert.equal(data.plan.totalMinutes, 135);
  await page.reload({ waitUntil: 'networkidle' });
  assert.equal((await snapshot(ctx)).entries.length, 1);
  check('Check-in sleep5/fatigue9 persists after reload, proposes72 and preserves135 baseline');

  await page.locator('nav').getByRole('link', { name: 'План недели', exact: true }).click();
  await page.getByRole('button', { name: 'Предложение на следующую' }).click();
  await page.getByText('72 минут · 2 занятия', { exact: true }).waitFor();
  await page.getByRole('button', { name: 'Эта неделя', exact: true }).click();
  const first = (await snapshot(ctx)).plan.sessions[0];
  await page.locator(`.session-row [data-id="${first.id}"]`).click();
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  assert.equal((await snapshot(ctx)).plan.sessions[0].status, 'done');
  check('Current/next tabs and actual session completion work');

  await page.locator('nav').getByRole('link', { name: 'Лаборатория PoC', exact: true }).click();
  await page.getByRole('button', { name: 'Проверить 5 сценариев', exact: false }).click();
  await page.getByText('5/5 пройдено', { exact: true }).waitFor();
  assert.equal((await snapshot(ctx)).entries.length, 2);
  await page.screenshot({ path: new URL('laboratory.png', output).pathname, fullPage: true });
  check('Five live API laboratory scenarios pass without changing diary');

  for (const route of ['journal', 'insights', 'about']) {
    await page.goto(`${base}/#${route}`, { waitUntil: 'networkidle' });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  }
  check('Journal, progress and explanation routes render without overflow');

  await page.goto(`${base}/#insights`, { waitUntil: 'networkidle' });
  const beforeHistory = await snapshot(ctx);
  assert.equal(await page.locator('.history-point').count(), 14);
  await page.getByRole('group', { name: 'Период истории', exact: true }).getByRole('button', { name: '30 дней', exact: true }).click();
  assert.equal(await page.locator('.history-point').count(), 60);
  assert.equal(await page.getByRole('group', { name: 'Период истории', exact: true }).getByRole('button', { name: '30 дней', exact: true }).getAttribute('aria-pressed'), 'true');
  await page.locator(`.history-panel.sleep [data-id="${isoDate()}"]`).click();
  await page.locator('dialog dd').filter({ hasText: /^5 ч$/ }).waitFor();
  await page.locator('dialog dd').filter({ hasText: /^9 \/ 10$/ }).waitFor();
  await page.getByRole('button', { name: 'Закрыть просмотр', exact: true }).click();
  assert.equal((await snapshot(ctx)).revision, beforeHistory.revision);
  await page.getByRole('button', { name: '7 дней', exact: true }).focus();
  await page.keyboard.press('Enter');
  assert.equal(await page.locator('.history-point').count(), 14);
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  assert.equal(await page.locator('.history-point').count(), 60);
  check('7/30-day history and day details use saved facts without writing data');

  for (const width of [320, 390, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.getByRole('button', { name: '7 дней', exact: true }).click();
    await page.getByRole('group', { name: 'Период истории', exact: true }).getByRole('button', { name: '30 дней', exact: true }).click();
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `history overflow at ${width}`);
    assert.equal(await page.locator('.history-panel').evaluateAll(panels => panels.some(panel => panel.scrollWidth > panel.clientWidth + 1)), false, `history panel overflow at ${width}`);
    assert.equal(await page.locator('.history-chart.month').evaluateAll(charts => charts.every(chart => Math.abs(chart.scrollWidth - chart.clientWidth - chart.scrollLeft) < 2)), true, `latest day visible at ${width}`);
  }
  await page.locator('#toast').waitFor({ state: 'hidden' });
  await page.screenshot({ path: new URL('progress-month.png', output).pathname, fullPage: true });
  await page.getByRole('button', { name: '7 дней', exact: true }).click();
  await page.screenshot({ path: new URL('progress-week.png', output).pathname, fullPage: true });
  check('Progress fits320/390/768/1024/1440px, including30-day scrollable charts');

  await page.goto(`${base}/#overview`, { waitUntil: 'networkidle' });
  await page.route('**/api/entries', route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'storage', message: 'Хранилище временно недоступно.' } }) }));
  await page.getByRole('button', { name: 'Отметить день', exact: false }).click();
  await page.getByLabel('Усталость сейчас').selectOption('8');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('#form-error').filter({ hasText: 'черновиком' }).waitFor();
  assert.equal((await snapshot(ctx)).entries.find(e => e.status === 'wellbeing').fatigue, 9);
  await page.unroute('**/api/entries');
  await page.reload({ waitUntil: 'networkidle' });
  await page.getByRole('button', { name: 'Открыть черновик', exact: true }).click();
  assert.equal(await page.getByLabel('Усталость сейчас').inputValue(), '8');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  assert.equal((await snapshot(ctx)).entries.find(e => e.status === 'wellbeing').fatigue, 8);
  check('503 leaves server unchanged; scoped draft survives reload and retries');

  let dropped = false;
  await page.route('**/api/entries', async route => {
    if (!dropped) { dropped = true; await route.fetch(); await route.abort('failed'); }
    else await route.continue();
  });
  await page.getByRole('button', { name: 'Отметить день', exact: false }).click();
  await page.getByLabel('Усталость сейчас').selectOption('7');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('#form-error').filter({ hasText: 'черновиком' }).waitFor();
  assert.equal((await snapshot(ctx)).entries.find(e => e.status === 'wellbeing').fatigue, 7);
  await page.getByLabel('Усталость сейчас').selectOption('6');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('#form-error').filter({ hasText: 'уже была принята' }).waitFor();
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  assert.equal((await snapshot(ctx)).entries.find(e => e.status === 'wellbeing').fatigue, 6);
  await page.unroute('**/api/entries');
  check('Lost response reconciles idempotency conflict before explicit corrected write');

  let releaseWrite;
  const delayedWrite = new Promise(resolve => { releaseWrite = resolve; });
  await page.route('**/api/entries', async route => { const response = await route.fetch(); await delayedWrite; await route.fulfill({ response }); });
  await page.getByRole('button', { name: 'Отметить день', exact: false }).click();
  await page.getByLabel('Усталость сейчас').selectOption('5');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('form[aria-busy="true"]').waitFor();
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('dialog').isVisible(), true);
  assert.equal(await page.getByLabel('Усталость сейчас').isDisabled(), true);
  releaseWrite();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  await page.unroute('**/api/entries');
  assert.equal((await snapshot(ctx)).entries.find(e => e.status === 'wellbeing').fatigue, 5);
  check('Pending mutation prevents Escape, edits and concurrent submissions');

  await page.getByRole('button', { name: 'Отметить день', exact: true }).click();
  await page.getByRole('group', { name: 'Быстрый выбор сна', exact: true }).getByRole('button', { name: '8 ч', exact: true }).click();
  assert.equal(await page.getByLabel('Сколько часов вы спали?').inputValue(), '8');
  await page.getByLabel('Сколько часов вы спали?').fill('7.5');
  assert.equal(await page.getByRole('button', { name: '8 ч', exact: true }).getAttribute('aria-pressed'), 'false');
  await page.getByRole('group', { name: 'Быстрый выбор усталости', exact: true }).getByRole('button', { name: '4 · Хорошо', exact: true }).click();
  assert.equal(await page.getByLabel('Усталость сейчас').inputValue(), '4');
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  const presetEntry = (await snapshot(ctx)).entries.find(entry => entry.status === 'wellbeing');
  assert.equal(presetEntry.sleep, 7.5); assert.equal(presetEntry.fatigue, 4);
  check('Quick check-in presets and manual corrections persist explicit values');

  const focusContext = await browser.newContext({ viewport: { width: 390, height: 844 }, reducedMotion: 'reduce' });
  try {
    let focusState = await snapshot(focusContext);
    const day = (new Date(`${isoDate()}T12:00:00Z`).getUTCDay() + 6) % 7;
    const profile = { name: 'UI fixture Сегодня', goal: 'tone', experience: 'beginner', days: [day], minutes: 30 };
    const created = await focusContext.request.post(`${base}/api/profile`, { headers: { 'X-Rhythm-Session': focusState.sessionBinding }, data: { revision: focusState.revision, profile } });
    assert.equal(created.status(), 200);
    const focusPage = await focusContext.newPage();
    focusPage.on('pageerror', error => pageErrors.push(error.message));
    await focusPage.goto(base, { waitUntil: 'networkidle' });
    await focusPage.getByRole('button', { name: 'Отметить занятие', exact: true }).click();
    await focusPage.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
    await focusPage.locator('dialog').waitFor({ state: 'hidden' });
    await focusPage.getByRole('img', { name: 'Выполнено 1 из 1 запланированных занятий за неделю', exact: true }).waitFor();
    await focusPage.getByRole('heading', { name: 'На сегодня — готово', exact: true }).waitFor();
    await focusPage.locator('#toast').waitFor({ state: 'hidden' });
    await focusPage.screenshot({ path: new URL('today-completed-mobile.png', output).pathname, fullPage: true });
    await focusPage.getByRole('button', { name: 'Изменить отметку', exact: true }).click();
    await focusPage.getByLabel('Пропустил', { exact: true }).check();
    await focusPage.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
    await focusPage.locator('dialog').waitFor({ state: 'hidden' });
    await focusPage.getByRole('img', { name: 'Выполнено 0 из 1 запланированных занятий за неделю', exact: true }).waitFor();
    focusState = await snapshot(focusContext);
    const updated = await focusContext.request.post(`${base}/api/profile`, { headers: { 'X-Rhythm-Session': focusState.sessionBinding }, data: { revision: focusState.revision, profile: { ...profile, name: 'UI fixture ' + 'Я'.repeat(29), days: [(day + 1) % 7] } } });
    assert.equal(updated.status(), 200);
    await focusPage.reload({ waitUntil: 'networkidle' });
    const frozen = await snapshot(focusContext);
    assert.equal(frozen.plan.sessions[0].date, isoDate());
    assert.equal(frozen.plan.sessions[0].status, 'missed');
    assert.equal(frozen.plan.sessions[0].minutes, 30);
    await focusPage.getByRole('button', { name: 'Изменить отметку', exact: true }).waitFor();
    for (const width of [320, 390]) {
      await focusPage.setViewportSize({ width, height: 844 });
      assert.equal(await focusPage.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `today overflow at ${width}`);
    }
    check('Today focus completes/corrects sessions; profile edits preserve the saved current schedule');
  } finally { await cleanup(focusContext); await focusContext.close(); }

  await page.getByRole('button', { name: 'Настройки и данные', exact: true }).click();
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Скачать', exact: true }).click(),
  ]);
  assert.match(download.suggestedFilename(), /^rhythm-/);
  await page.getByRole('button', { name: 'Закрыть', exact: true }).click();
  check('JSON export downloads current own data');

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, deviceScaleFactor: 1, reducedMotion: 'reduce' });
  const mobilePage = await mobile.newPage();
  mobilePage.on('pageerror', error => pageErrors.push(error.message));
  await mobilePage.goto(base, { waitUntil: 'networkidle' });
  for (const route of ['overview', 'week', 'journal', 'insights', 'lab']) {
    await mobilePage.goto(`${base}/#${route}`, { waitUntil: 'networkidle' });
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `mobile overflow ${route}`);
  }
  await mobilePage.goto(base, { waitUntil: 'networkidle' });
  await mobilePage.screenshot({ path: new URL('mobile.png', output).pathname, fullPage: true });
  await mobilePage.getByRole('button', { name: 'Создать мой план', exact: true }).click();
  assert.equal(await mobilePage.evaluate(() => document.querySelector('dialog').getBoundingClientRect().right <= innerWidth), true);
  await mobilePage.keyboard.press('Escape');
  check('Mobile390px: all routes fit, bottom navigation and dialog work');
  await mobile.close();

  const telegramContext = await browser.newContext({ viewport: { width: 390, height: 844 }, reducedMotion: 'reduce' });
  await telegramContext.addInitScript(() => {
    window.Telegram = { WebApp: { initData: 'synthetic-current-user', colorScheme: 'dark', ready() {}, expand() {}, isVersionAtLeast() { return false; }, onEvent() {}, BackButton: { onClick() {}, show() {}, hide() {} } } };
  });
  const tgPage = await telegramContext.newPage();
  let stateReads = 0, authCalls = 0;
  await tgPage.route('**/api/state', route => { stateReads++; return route.continue(); });
  await tgPage.route('**/api/auth/telegram', route => { authCalls++; return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'telegram', message: 'Telegram auth fixture unavailable' } }) }); });
  await tgPage.goto(`${base}/mini`, { waitUntil: 'networkidle' });
  await tgPage.getByRole('button', { name: 'Повторить', exact: true }).click();
  await tgPage.getByText('Telegram auth fixture unavailable', { exact: true }).waitFor();
  assert.equal(authCalls, 2); assert.equal(stateReads, 0);
  check('Telegram auth failure/retry never falls back to another cookie identity (SDK fixture only)');
  assert.equal(await tgPage.locator('html').getAttribute('data-theme'), 'dark');
  assert.equal(await tgPage.getByRole('group', { name: 'Период истории', exact: true }).getByRole('button', { name: '30 дней', exact: true }).count(), 0, 'private views stay hidden after failed authentication');
  // The theme check uses an explicitly successful SDK fixture, separately from
  // the failed-auth case above. This is not a real Telegram login.
  const themeFixture = { ...await snapshot(ctx), identity: 'telegram' };
  await tgPage.unroute('**/api/auth/telegram');
  await tgPage.route('**/api/auth/telegram', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(themeFixture) }));
  await tgPage.goto(`${base}/mini#insights`, { waitUntil: 'networkidle' });
  await tgPage.reload({ waitUntil: 'networkidle' });
  await tgPage.getByRole('group', { name: 'Период истории', exact: true }).getByRole('button', { name: '30 дней', exact: true }).click();
  assert.equal(await tgPage.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  await tgPage.screenshot({ path: new URL('telegram-dark-fixture.png', output).pathname, fullPage: true });
  check('Telegram dark-theme fixture keeps30-day progress readable at390px');
  await telegramContext.close();

  ownCookies = await ctx.cookies();
  const other = await browser.newContext();
  const otherState = await snapshot(other);
  const profileResponse = await other.request.post(`${base}/api/profile`, { headers: { 'X-Rhythm-Session': otherState.sessionBinding }, data: { revision: otherState.revision, profile: { name: 'UI fixture B', goal: 'tone', experience: 'beginner', days: [0, 2, 4], minutes: 45 } } });
  assert.equal(profileResponse.status(), 200);
  const beforeOther = await snapshot(other);
  await ctx.addCookies(await other.cookies());
  await page.getByRole('button', { name: 'Отметить день', exact: false }).click();
  await page.getByRole('button', { name: 'Сохранить отметку', exact: false }).click();
  await page.getByText('Сессия изменилась в другой вкладке.', { exact: false }).waitFor();
  assert.equal((await snapshot(other)).revision, beforeOther.revision);
  await ctx.addCookies(ownCookies);
  await cleanup(other); await other.close();
  check('Shared cookie identity switch is blocked before cross-account write');

  await page.reload({ waitUntil: 'networkidle' });
  await page.getByRole('button', { name: 'Настройки и данные', exact: true }).click();
  await page.getByRole('button', { name: 'Очистить дневник', exact: true }).click();
  await page.getByRole('button', { name: 'Да, очистить дневник', exact: true }).click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  assert.equal((await snapshot(ctx)).profile, null);
  check('Explicit delete clears own profile and diary');
  assert.deepEqual(pageErrors, []);
  check('No uncaught browser JavaScript errors');
  await writeFile(new URL('browser.json', output), JSON.stringify({ generatedAt: new Date().toISOString(), scope: 'Isolated Chromium UI fixtures on local server; Telegram SDK mocked, not a live Telegram launch', date: isoDate(), checks, pageErrors }, null, 2) + '\n');
} catch (error) {
  await page.screenshot({ path: new URL('browser-failure.png', output).pathname, fullPage: true });
  throw error;
} finally {
  if (ownCookies) await ctx.addCookies(ownCookies);
  await cleanup(ctx);
  await browser.close();
}
