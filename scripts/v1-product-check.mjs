// Real local Go/PostgreSQL/Chrome acceptance. Capacity thresholds use a labelled
// read-only response fixture; no production account or external AI is used.
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:18081';
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname));
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || '/tmp/rhythm-v1-browser');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' });
const page = await context.newPage();
page.setDefaultTimeout(12000);
const errors = [], checks = [], writes = [];
page.on('pageerror', error => errors.push(error.message));
const check = name => { checks.push({ name, passed: true }); console.log(`PASS ${name}`); };
const snapshot = async () => {
  const response = await context.request.get(`${base}/api/state`);
  assert.equal(response.status(), 200);
  return response.json();
};
const settings = async () => { await page.getByRole('button', { name: 'Настройки и данные', exact: true }).click(); };
const close = async () => { await page.locator('dialog [data-action=close]').first().click(); await page.locator('dialog').waitFor({ state: 'hidden' }); };
let current;
try {
  current = await snapshot();
  const create = await context.request.post(`${base}/api/profile`, {
    headers: { Origin: base, 'X-Rhythm-Session': current.sessionBinding },
    data: { revision: current.revision, profile: { name: 'UI fixture Release', goal: 'tone', experience: 'beginner', days: [0, 2, 4], minutes: 45 } },
  });
  assert.equal(create.status(), 200); current = await create.json();
  const entry = await context.request.post(`${base}/api/entries`, {
    headers: { Origin: base, 'X-Rhythm-Session': current.sessionBinding },
    data: { revision: current.revision, entry: { id: 'release-ui-wellbeing', date: current.today, status: 'wellbeing', sleep: 7.5, fatigue: 4 } },
  });
  assert.equal(entry.status(), 200, await entry.text()); current = await entry.json();
  const baseline = await snapshot();
  page.on('request', request => {
    if (new URL(request.url()).origin === base && !['GET', 'HEAD'].includes(request.method())) writes.push(new URL(request.url()).pathname);
  });
  await page.goto(`${base}/#assistant`, { waitUntil: 'networkidle' });
  assert.equal(await page.locator('#coach-form').count(), 0);
  assert.equal(await page.locator('#coach-consent-form').count(), 0);
  await page.locator('[data-action=rules-helper]').click();
  assert.equal(await page.locator('#assistant-form input').evaluate(input => input === document.activeElement), true);
  await page.getByRole('button', { name: 'Сохранить доступ', exact: true }).waitFor();
  check('Unconfigured AI offers a working rules helper; saved guest records expose account preservation');

  await settings();
  const rows = page.locator('.data-usage-list > li');
  assert.equal(await rows.count(), 6);
  for (const key of ['entries', 'workouts', 'hrvEntries', 'bodyEntries', 'mealSelections', 'weeks']) {
    const meter = page.locator(`[data-collection="${key}"] meter`);
    assert.equal(Number(await meter.getAttribute('max')), baseline.limits[key]);
    assert.equal(Number(await meter.getAttribute('value')), baseline[key]?.length || 0);
  }
  await page.getByRole('button', { name: 'Очистить дневник', exact: true }).click();
  assert.match(await page.locator('dialog').innerText(), /Аккаунт, пароль, резервный код и активные сеансы останутся/);
  assert.equal(await page.getByRole('button', { name: 'Да, очистить дневник', exact: true }).count(), 1);
  await page.locator('[data-action=settings]').filter({ hasText: 'Вернуться' }).click();
  const download = page.waitForEvent('download');
  await page.locator('.data-usage [data-action=export]').click();
  assert.match((await download).suggestedFilename(), /^rhythm-.*\.json$/);
  check('Settings show six authoritative limits, export data and explain diary clearing precisely');

  await page.locator('[data-action=product-help]').click();
  await page.locator('.product-help').waitFor();
  for (const width of [320, 390, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `help page overflow at ${width}`);
    assert.equal(await page.locator('dialog').evaluate(element => element.scrollWidth > element.clientWidth + 1), false, `help dialog overflow at ${width}`);
  }
  await page.locator('.product-help summary').filter({ hasText: 'Почему новый профиль' }).click();
  await page.locator('.product-help a[href="#week"]').click();
  await page.locator('dialog').waitFor({ state: 'hidden' });
  assert.equal(new URL(page.url()).hash, '#week');
  check('Product help fits mobile and desktop, and navigation closes the dialog');

  await settings(); await page.locator('[data-action=install]').click();
  assert.match(await page.locator('dialog').innerText(), /iPhone · Safari/);
  assert.match(await page.locator('dialog').innerText(), /не сохраняет дневник на телефоне для работы без сети/);
  await close();
  const manifestResponse = await context.request.get(`${base}/manifest.webmanifest`);
  assert.equal(manifestResponse.status(), 200);
  const manifest = await manifestResponse.json(); assert.equal(manifest.display, 'standalone');
  assert.equal(manifest.scope, '/');
  check('Installation guide and manifest provide phone entry points with an explicit online requirement');

  let failedReads = 0;
  await page.route('**/api/state', route => { failedReads++; return route.abort('connectionfailed'); });
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.getByRole('alert').filter({ hasText: 'Не удалось обновить данные' }).waitFor();
  assert.match(await page.getByRole('alert').innerText(), /Последнее обновление:/);
  assert.match(await page.locator('body').innerText(), /UI fixture Release/);
  const readsAfterFailure = failedReads;
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  assert.equal(failedReads, readsAfterFailure, 'stale state pauses automatic repeated reads');
  await page.unroute('**/api/state');
  await page.getByRole('button', { name: 'Обновить данные', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'Не удалось обновить данные' }).waitFor({ state: 'hidden' });
  assert.match(await page.locator('body').innerText(), /UI fixture Release/);
  check('Network failure retains the last snapshot, marks it stale and recovers through explicit retry');

  // Lower limits in a labelled read fixture instead of writing thousands of records.
  await page.route('**/api/state', async route => {
    const response = await route.fetch(), data = await response.json();
    data.limits.entries = data.entries.length;
    data.limits.weeks = data.weeks.length;
    await route.fulfill({ response, json: data });
  });
  await page.reload({ waitUntil: 'networkidle' }); await settings();
  assert.equal(await page.locator('[data-collection=entries]').getAttribute('data-capacity-state'), 'full');
  assert.equal(await page.locator('[data-collection=weeks]').getAttribute('data-capacity-state'), 'full');
  assert.match(await page.locator('[data-collection=weeks]').innerText(), /Отдельное удаление сохранённых недель не поддерживается/);
  for (const width of [320, 390, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    assert.equal(await page.locator('dialog').evaluate(element => element.scrollWidth > element.clientWidth + 1), false, `settings overflow at ${width}`);
  }
  await page.locator('dialog').screenshot({ path: resolve(output, 'settings-capacity.png') });
  await close(); await page.unroute('**/api/state');
  check('Capacity fixture exposes full history without promising automatic deletion and fits 320–1440px');

  assert.deepEqual(await snapshot(), baseline);
  assert.deepEqual(writes, []);
  assert.deepEqual(errors, []);
  check('Help, installation, export, retry and capacity browsing do not mutate the diary');

  await page.evaluate(async () => { await navigator.serviceWorker.ready; });
  await page.reload({ waitUntil: 'networkidle' });
  const cachePaths = await page.evaluate(async () => {
    const paths = [];
    for (const key of await caches.keys()) for (const request of await (await caches.open(key)).keys()) paths.push(new URL(request.url).pathname);
    return paths.sort();
  });
  assert.deepEqual(cachePaths, ['/app-icon.svg', '/offline.html']);
  await context.setOffline(true);
  await page.goto(`${base}/`, { waitUntil: 'load' });
  assert.match(await page.locator('main').innerText(), /Для дневника нужно соединение/);
  assert.doesNotMatch(await page.locator('body').innerText(), /UI fixture Release/);
  await context.setOffline(false);
  await page.getByRole('link', { name: 'откройте Ритм снова' }).click();
  await page.getByRole('button', { name: 'Настройки и данные', exact: true }).waitFor();
  check('Service worker caches only two public assets; offline navigation cannot expose the private diary');
  await writeFile(resolve(output, 'browser.json'), JSON.stringify({ generatedAt: new Date().toISOString(), scope: 'Local Go/PostgreSQL and Chrome. Capacity display uses a labelled read-only fixture. No live Telegram or AI.', checks, errors }, null, 2) + '\n');
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await context.setOffline(false);
  try {
    current = await snapshot();
    if (current.profile?.name === 'UI fixture Release') {
      const response = await context.request.delete(`${base}/api/data`, { headers: { 'X-Rhythm-Session': current.sessionBinding }, data: { revision: current.revision } });
      assert.equal(response.status(), 200, 'synthetic diary cleanup');
    }
  } finally { await browser.close(); }
}
