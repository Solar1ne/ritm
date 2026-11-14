// Local browser acceptance using fresh synthetic accounts and real HTTP writes.
// No responses are mocked; no LLM, device account or Telegram API is contacted.
// Run only after rebuilding/restarting the app with the wellness/nutrition code:
// PLAYWRIGHT_MODULE=/absolute/path/to/playwright RHYTHM_URL=http://127.0.0.1:8080 node scripts/wellness-nutrition-check.mjs
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import assert from 'node:assert/strict';
import { addDays } from '../frontend/static/format.js';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const target = new URL(process.env.RHYTHM_URL || 'http://127.0.0.1:8080');
assert.ok(['http:', 'https:'].includes(target.protocol) && ['127.0.0.1', 'localhost', '[::1]'].includes(target.hostname), 'Acceptance is restricted to loopback hosts');
assert.ok(!target.username && !target.password && !target.search && !target.hash && target.pathname === '/', 'Use a plain local origin');
const base = target.origin;
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/ai-product/wellness');
await mkdir(output, { recursive: true });
const started = new Date();
const marker = `WN synthetic ${randomUUID().slice(0, 8)}`;
const checks = [], errors = [], apiFailures = [], layout = [], screenshots = [], teardown = [], contexts = [];
let browser, page, failure = null, finalCounts = null, today = '';

const check = (name, details = {}) => { checks.push({ name, passed: true, ...details }); console.log(`PASS ${name}`); };
const action = (name, on = page) => on.locator(`[data-action="${name}"]`).first();
const closed = (on = page) => on.locator('dialog').waitFor({ state: 'hidden' });
async function snapshot(context) {
  const response = await context.request.get(`${base}/api/state`, { timeout: 10000 });
  assert.equal(response.status(), 200, 'GET state succeeds');
  return response.json();
}
function collections(value) {
  return { hrvEntries: value.hrvEntries, bodyEntries: value.bodyEntries, mealSelections: value.mealSelections };
}
async function mutate(context, path, data, status = 200, method = 'POST') {
  const current = await snapshot(context);
  const response = await context.request.fetch(`${base}${path}`, {
    method, timeout: 10000, headers: { 'X-Rhythm-Session': current.sessionBinding, Origin: base }, data,
  });
  assert.equal(response.status(), status, `${method} ${path} status`);
  return response.json();
}
async function makeContext() {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, reducedMotion: 'reduce', acceptDownloads: true });
  const record = { context, owned: false };
  contexts.push(record);
  const on = await context.newPage();
  on.setDefaultTimeout(12000);
  on.on('pageerror', error => errors.push(error.message));
  on.on('response', response => {
    const url = new URL(response.url());
    if (url.origin === base && url.pathname.startsWith('/api/') && response.status() >= 400) {
      apiFailures.push({ method: response.request().method(), path: url.pathname, status: response.status() });
    }
  });
  const empty = await snapshot(context);
  assert.equal(empty.profile, null, 'Fresh context must have no profile');
  for (const key of ['entries', 'workouts', 'hrvEntries', 'bodyEntries', 'mealSelections']) {
    assert.ok(Array.isArray(empty[key]) && empty[key].length === 0, `Server readiness and fresh account: ${key}=[]`);
  }
  record.owned = true;
  return { context, page: on, initial: empty };
}
async function screenshot(name, fullPage = true, on = page) {
  await on.screenshot({ path: resolve(output, name), fullPage });
  screenshots.push(name);
}
async function downloadText(download) {
  const stream = await download.createReadStream();
  assert.ok(stream, 'Browser download produces a readable file');
  const chunks = []; let length = 0;
  for await (const chunk of stream) {
    length += chunk.length;
    assert.ok(length <= 4 * 1024 * 1024, 'Synthetic download stays bounded');
    chunks.push(chunk);
  }
  assert.equal(await download.failure(), null, 'Browser download completes');
  return Buffer.concat(chunks).toString('utf8');
}
async function selectSeries(text, on = page) {
  const options = await on.locator('#measurement-series option').evaluateAll(nodes => nodes.map(node => ({ label: node.textContent, value: node.value })));
  const choice = options.find(option => option.label.includes(text));
  assert.ok(choice, `Series exists: ${text}`);
  await on.locator('#measurement-series').selectOption(choice.value);
}
async function addHRV(date, value, metric = 'rmssd') {
  await action('measurement-add').click();
  const form = page.locator('#measurement-form');
  assert.equal(await form.locator('[name=metric]').inputValue(), '', 'A new HRV metric must be chosen explicitly');
  assert.equal(await form.locator('[name=window]').inputValue(), '', 'A new HRV window must be chosen explicitly');
  assert.equal(await form.locator('[name=valueMs]').inputValue(), '', 'No invented HRV starting value');
  await form.locator('[name=date]').fill(date);
  await form.locator('[name=valueMs]').fill(String(value));
  await form.locator('[name=metric]').selectOption(metric);
  await form.locator('[name=window]').selectOption('overnight');
  await form.locator('[name=source]').fill('WN fixture watch');
  await form.locator('[type=submit]').click();
  await closed();
}
async function fillBody({ date, weight = '', fat = '', method = 'bia' }) {
  const form = page.locator('#measurement-form');
  await form.locator('[name=date]').fill(date);
  await form.locator('[name=weightKg]').fill(String(weight));
  await form.locator('[name=bodyFatPercent]').fill(String(fat));
  await form.locator('[name=method]').selectOption(method);
  await form.locator('[name=source]').fill('WN fixture scale');
  return form;
}
async function applyFilters({ diet = 'balanced', maxMinutes = 30, meal = '', excluded = [] } = {}) {
  const form = page.locator('#nutrition-filter-form');
  await form.locator('[name=diet]').selectOption(diet);
  await form.locator('[name=maxMinutes]').selectOption(String(maxMinutes));
  await form.locator('[name=meal]').selectOption(meal);
  if (await form.locator('details').getAttribute('open') === null) await form.locator('summary').click();
  for (const checkbox of await form.locator('input[name=allergen]').all()) {
    await checkbox.setChecked(excluded.includes(await checkbox.inputValue()));
  }
  await form.locator('[type=submit]').click();
}
async function weeklyPreview() {
  await action('nutrition-week').click();
  await page.locator('#nutrition-week-form [name=startDate]').fill(today);
  await page.locator('#nutrition-week-form [type=submit]').click();
  await page.locator('#nutrition-confirm-form').waitFor();
}

try {
  browser = await chromium.launch({ channel: 'chrome', headless: true });
  const primary = await makeContext(), ctx = primary.context;
  page = primary.page;
  today = primary.initial.today;
  assert.match(today, /^\d{4}-\d{2}-\d{2}$/, 'Dates must come from the server');
  const weekday = (new Date(`${today}T12:00:00Z`).getUTCDay() + 6) % 7;
  await mutate(ctx, '/api/profile', { revision: primary.initial.revision, profile: { name: marker, goal: 'tone', experience: 'beginner', days: [weekday], minutes: 45 } });
  await page.goto(`${base}/#wellness`, { waitUntil: 'networkidle' });
  await page.getByText('Ваша история начнётся с одной записи', { exact: true }).waitFor();
  assert.equal(await page.locator('.measurement-point').count(), 0);
  const originalPlan = (await snapshot(ctx)).plan;
  await addHRV(addDays(today, -3), 50);
  await addHRV(today, 60);
  await addHRV(today, 42, 'sdnn');
  let data = await snapshot(ctx);
  assert.equal(data.hrvEntries.length, 3);
  assert.deepEqual(data.plan, originalPlan, 'HRV observations alone do not alter the plan');
  await page.reload({ waitUntil: 'networkidle' });
  assert.equal(await page.locator('#measurement-series option').count(), 2);
  await selectSeries('RMSSD');
  assert.equal(await page.locator('.measurement-point').count(), 2, 'Missing days are not zero-valued points');
  assert.equal(await page.locator('.measurement-row').count(), 2);
  assert.match(await page.locator('.stats').innerText(), /\+10/);
  await screenshot('hrv-desktop.png');
  await selectSeries('SDNN');
  assert.equal(await page.locator('.measurement-point').count(), 1, 'Different HRV metrics stay in separate series');
  check('HRV requires explicit metric/window; same-series history and separate metrics survive reload');

  await page.locator('[data-action=measurement-tab][data-kind=body]').click();
  await action('measurement-add').click();
  assert.equal(await page.locator('#measurement-form [name=method]').inputValue(), '');
  assert.equal(await page.locator('#measurement-form [name=weightKg]').inputValue(), '');
  assert.equal(await page.locator('#measurement-form [name=bodyFatPercent]').inputValue(), '');
  let form = await fillBody({ date: addDays(today, -2), weight: 80, fat: 20 });
  await form.locator('[type=submit]').click(); await closed();
  const bodyOriginal = (await snapshot(ctx)).bodyEntries[0];
  await page.locator(`[data-action=measurement-edit][data-id="${bodyOriginal.id}"]`).click();
  await page.locator('#measurement-form [name=weightKg]').fill('81');
  await page.locator('#measurement-form [name=bodyFatPercent]').fill('21');
  await page.locator('#measurement-form [type=submit]').click(); await closed();
  assert.equal((await snapshot(ctx)).bodyEntries.length, 1);
  assert.equal((await snapshot(ctx)).bodyEntries[0].weightKg, 81);
  await action('measurement-add').click();
  form = await fillBody({ date: today, weight: 79.5 });
  await form.locator('[type=submit]').click(); await closed();
  await page.locator('#measurement-field').selectOption('bodyFatPercent');
  assert.equal(await page.locator('.measurement-point').count(), 1, 'Missing fat does not become zero');
  await page.getByText('79,5 кг · Доля жира не указана', { exact: true }).waitFor();
  const beforeReplacement = await snapshot(ctx);
  await action('measurement-add').click();
  form = await fillBody({ date: today, fat: 22 });
  await form.locator('[type=submit]').click();
  await action('measurement-replace').waitFor();
  assert.equal((await snapshot(ctx)).revision, beforeReplacement.revision, 'Replacement requires a separate explicit action');
  await action('measurement-replace').click(); await closed();
  data = await snapshot(ctx);
  const replaced = data.bodyEntries.find(entry => entry.date === today);
  assert.equal(data.bodyEntries.length, 2);
  assert.equal(replaced.weightKg ?? null, null, 'Replacing the whole observation clears the omitted weight');
  assert.equal(replaced.bodyFatPercent, 22);
  await page.locator('#measurement-field').selectOption('weightKg');
  assert.equal(await page.locator('.measurement-point').count(), 1, 'Cleared weight does not become zero');
  await action('measurement-add').click();
  form = await fillBody({ date: today, weight: 82, fat: 24, method: 'dxa' });
  await form.locator('[type=submit]').click(); await closed();
  assert.equal(await page.locator('#measurement-series option').count(), 2);
  await selectSeries('Биоимпеданс');
  assert.equal(await page.locator('.measurement-row').count(), 2, 'DXA cannot contaminate the BIA series');
  await page.setViewportSize({ width: 390, height: 844 });
  await screenshot('body-mobile.png');
  await page.setViewportSize({ width: 1440, height: 1100 });
  check('Body edit, explicit whole-record replacement, unknown fields and separate methods preserve observed facts');

  await page.goto(`${base}/#nutrition`, { waitUntil: 'networkidle' });
  await page.locator('#nutrition-filter-form').waitFor();
  const catalogResponse = await ctx.request.get(`${base}/api/nutrition/catalog`, { headers: { 'X-Rhythm-Session': (await snapshot(ctx)).sessionBinding } });
  assert.equal(catalogResponse.status(), 200);
  const catalog = await catalogResponse.json();
  assert.equal(catalog.recipes.length, 18);
  await applyFilters({ diet: 'vegan', maxMinutes: 15, meal: 'breakfast', excluded: ['gluten'] });
  assert.equal(await page.locator('.recipe-card').count(), 0);
  await page.getByText('Таких сочетаний пока нет в каталоге', { exact: true }).waitFor();
  const beforeEmpty = await snapshot(ctx);
  await action('nutrition-week').click();
  await page.locator('#nutrition-week-form [type=submit]').click();
  await page.locator('#nutrition-week-form #form-error').filter({ hasText: 'нет подходящих блюд' }).waitFor();
  assert.equal((await snapshot(ctx)).revision, beforeEmpty.revision);
  assert.equal((await snapshot(ctx)).mealSelections.length, 0);
  await action('close').click();
  await applyFilters({ diet: 'vegan', excluded: ['gluten', 'soy'] });
  const visibleIDs = await page.locator('.recipe-card [data-action=nutrition-recipe]').evaluateAll(nodes => nodes.map(node => node.dataset.id));
  assert.equal(visibleIDs.length, 5);
  for (const id of visibleIDs) {
    const recipe = catalog.recipes.find(item => item.id === id);
    assert.ok(recipe.diets.includes('vegan') && !recipe.allergens.includes('gluten') && !recipe.allergens.includes('soy'));
  }
  check('Diet/time/allergen filters are strict; an empty menu fails explicitly without persistence');

  const beforeMenu = await snapshot(ctx);
  await weeklyPreview();
  assert.equal(await page.locator('dialog .meal-slot').count(), 21);
  assert.equal((await snapshot(ctx)).revision, beforeMenu.revision);
  assert.equal((await snapshot(ctx)).mealSelections.length, 0);
  await screenshot('nutrition-preview-desktop.png', false);
  let batchWrites = 0;
  const countBatch = request => { if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/meals/batch') batchWrites++; };
  page.on('request', countBatch);
  await page.locator('#nutrition-confirm-form [type=submit]').click(); await closed();
  page.off('request', countBatch);
  data = await snapshot(ctx);
  assert.equal(batchWrites, 1, 'One confirmation creates one batch write');
  assert.equal(data.revision, beforeMenu.revision + 1);
  assert.equal(data.mealSelections.length, 21);
  assert.ok(data.mealSelections.every(item => visibleIDs.includes(item.recipeId)));
  const savedMenu = structuredClone(data.mealSelections), savedRevision = data.revision;
  const retried = await mutate(ctx, '/api/meals/batch', { revision: beforeMenu.revision, selections: savedMenu });
  assert.equal(retried.revision, savedRevision, 'An exact lost-response retry is idempotent');
  await page.reload({ waitUntil: 'networkidle' });
  await page.locator('#nutrition-filter-form').waitFor();
  assert.equal(await page.locator('.nutrition-plan [data-action=nutrition-remove]').count(), 21);
  await screenshot('nutrition-menu-desktop.png');
  check('Weekly preview writes nothing; one explicit save commits 21 choices atomically and survives retry/reload');

  await page.locator('.recipe-card [data-action=nutrition-recipe][data-id=yogurt-oat-pear-v1]').click();
  await page.locator('#recipe-save-form [name=date]').fill(today);
  await page.locator('#recipe-save-form [type=submit]').click(); await closed();
  data = await snapshot(ctx);
  assert.equal(data.mealSelections.length, 21);
  assert.equal(data.mealSelections.find(item => item.date === today && item.slot === 'breakfast').recipeId, 'yogurt-oat-pear-v1');
  assert.equal(data.revision, savedRevision + 1);
  await page.locator(`.nutrition-plan [data-action=nutrition-remove][data-date="${today}"][data-slot=lunch]`).click();
  await page.locator('#meal-remove-form [type=submit]').click(); await closed();
  data = await snapshot(ctx);
  assert.equal(data.mealSelections.length, 20);
  assert.ok(!data.mealSelections.some(item => item.date === today && item.slot === 'lunch'));
  check('A saved meal can be explicitly replaced or removed without changing other dates');

  await weeklyPreview();
  const staleBaseline = await snapshot(ctx);
  const concurrent = await mutate(ctx, '/api/hrv', { revision: staleBaseline.revision, entry: { id: `concurrent-${randomUUID()}`, date: addDays(today, -4), metric: 'rmssd', window: 'overnight', valueMs: 55, source: 'WN fixture watch' } });
  const staleResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/meals/batch');
  await page.locator('#nutrition-confirm-form [type=submit]').click();
  assert.equal((await staleResponse).status(), 409);
  await page.locator('#nutrition-confirm-form #form-error').filter({ hasText: 'Данные обновлены' }).waitFor();
  data = await snapshot(ctx);
  assert.equal(data.revision, concurrent.revision);
  assert.deepEqual(data.mealSelections, staleBaseline.mealSelections, 'A stale week cannot overwrite a newer revision');
  await action('close').click();
  check('Concurrent changes reject a stale weekly confirmation and preserve the saved menu');

  await action('nutrition-shopping').click();
  assert.ok(await page.locator('.shopping-list input[type=checkbox]').count() > 0);
  await page.locator('.shopping-list input[type=checkbox]').first().check();
  const shoppingDownload = page.waitForEvent('download');
  await action('nutrition-download').click();
  const shoppingText = await downloadText(await shoppingDownload);
  assert.match(shoppingText, /Ритм · продукты/);
  assert.match(shoppingText, /Натуральный йогурт без сахара: 150 г/);
  assert.match(shoppingText, /\d+ × /);
  assert.match(shoppingText, /не гарантирует отсутствие следов аллергенов/);
  await writeFile(resolve(output, 'shopping-list.txt'), shoppingText);
  await action('close').click();
  check('Shopping download reflects the saved replacement and retains quantity multiplicity and allergen notice');

  await page.getByRole('button', { name: 'Настройки и данные', exact: true }).click();
  const exportDownload = page.waitForEvent('download');
  await action('export').click();
  const backup = JSON.parse(await downloadText(await exportDownload));
  data = await snapshot(ctx);
  assert.deepEqual(collections(backup), collections(data));
  const restored = await makeContext();
  await restored.page.goto(base, { waitUntil: 'networkidle' });
  await restored.page.getByRole('button', { name: 'Настройки и данные', exact: true }).click();
  await action('restore', restored.page).click();
  await restored.page.locator('#restore-preview-form input[type=file]').setInputFiles({ name: 'wellness-synthetic.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(backup)) });
  await restored.page.locator('#restore-preview-form [type=submit]').click();
  await restored.page.locator('#restore-save-form').waitFor();
  assert.equal((await snapshot(restored.context)).profile, null, 'Reading the backup preview must not restore it');
  await restored.page.locator('#restore-save-form [type=submit]').click(); await closed(restored.page);
  const recovered = await snapshot(restored.context);
  assert.deepEqual(collections(recovered), collections(backup));
  assert.notEqual(recovered.sessionBinding, backup.sessionBinding, 'Restored data cannot import the original identity');
  assert.equal(recovered.revision, 1);
  await mutate(restored.context, '/api/body', { revision: recovered.revision, id: recovered.bodyEntries[0].id }, 200, 'DELETE');
  assert.deepEqual(collections(await snapshot(ctx)), collections(data), 'Deleting from the restored copy cannot alter the source account');
  await action('close').click();
  finalCounts = { hrv: data.hrvEntries.length, body: data.bodyEntries.length, meals: data.mealSelections.length };
  await writeFile(resolve(output, 'backup-summary.json'), JSON.stringify({ exportVersion: backup.exportVersion, restoredInIndependentContext: true, sourceUnchanged: true, ...finalCounts }, null, 2) + '\n');
  check('Real browser export/restore preserves all new collections under a separate identity');

  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    for (const route of ['wellness', 'nutrition']) {
      await page.goto(`${base}/#${route}`, { waitUntil: 'networkidle' });
      if (route === 'nutrition') await page.locator('#nutrition-filter-form').waitFor();
      const measured = await page.evaluate(() => ({
        pageWidth: document.documentElement.scrollWidth, viewport: innerWidth,
        overflowingElements: [...document.querySelectorAll('.page-heading > *, .history-toolbar > *, .nutrition-filter-grid > *, .feature-toolbar > *')].filter(element => {
          const rect = element.getBoundingClientRect();
          return rect.width > 0 && (rect.right > innerWidth + 1 || rect.left < -1 || element.scrollWidth > element.clientWidth + 1);
        }).map(element => ({ tag: element.tagName, className: element.className, action: element.dataset.action || '', scrollWidth: element.scrollWidth, clientWidth: element.clientWidth })),
        overflowingCards: [...document.querySelectorAll('.stat,.recipe-card,.meal-day,.measurement-row')].filter(element => {
          const rect = element.getBoundingClientRect();
          return rect.width > 0 && (rect.right > innerWidth + 1 || rect.left < -1 || element.scrollWidth > element.clientWidth + 1);
        }).map(element => element.className),
      }));
      if (route === 'wellness' && width <= 520) {
        const target = await action('measurement-add').boundingBox();
        assert.ok(target && target.width >= 44 && target.height >= 44, 'Mobile add control retains a 44px touch target beside the heading');
        measured.addTouchTarget = { width: target.width, height: target.height };
      }
      layout.push({ route, width, ...measured });
      if (measured.pageWidth > width + 1 || measured.overflowingCards.length > 0 || measured.overflowingElements.length > 0) await screenshot(`overflow-${route}-${width}.png`);
    }
  }
  assert.deepEqual(layout.filter(item => item.pageWidth > item.width + 1 || item.overflowingCards.length > 0 || item.overflowingElements.length > 0), [], 'Every route must fit its page, controls and cards at all four widths');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('.nutrition-plan').scrollIntoViewIfNeeded();
  await screenshot('nutrition-menu-mobile.png', false);
  check('Wellness and nutrition fit 320, 390, 768 and 1440px with real saved content');

  assert.deepEqual(errors, [], 'No uncaught browser errors');
  assert.deepEqual(apiFailures, [
    { method: 'POST', path: '/api/nutrition/preview', status: 422 },
    { method: 'POST', path: '/api/meals/batch', status: 409 },
  ], 'Only the two intentional negative requests fail');
  assert.equal((await snapshot(ctx)).today, today, 'Server day changed during the run; repeat the acceptance for one consistent day');
  check('No uncaught JavaScript errors or unexpected API failures');
} catch (error) {
  failure = { name: error.name, message: error.message, stack: error.stack };
  if (page && !page.isClosed()) await screenshot('failure.png', true).catch(() => {});
} finally {
  for (const { context, owned } of contexts) {
    try {
      for (const open of context.pages()) await open.close();
      if (owned) {
        const data = await snapshot(context);
        assert.ok(data.profile === null || data.profile.name === marker, 'Cleanup only owns this run\'s synthetic profile');
        const cleared = await mutate(context, '/api/data', { revision: data.revision }, 200, 'DELETE');
        assert.equal(cleared.profile, null);
        for (const key of ['entries', 'workouts', 'weeks', 'sessionAdjustments', 'hrvEntries', 'bodyEntries', 'mealSelections']) assert.equal((cleared[key] || []).length, 0, `Cleanup: ${key}`);
        teardown.push({ account: teardown.length + 1, passed: true });
      }
    } catch (error) {
      teardown.push({ account: teardown.length + 1, passed: false, error: error.message });
      failure ||= { name: 'CleanupError', message: error.message };
    } finally { await context.close().catch(() => {}); }
  }
  const browserVersion = browser?.version() || null;
  if (browser) await browser.close().catch(error => { failure ||= { name: 'BrowserCloseError', message: error.message }; });
  await writeFile(resolve(output, 'browser.json'), JSON.stringify({
    startedAt: started.toISOString(), completedAt: new Date().toISOString(), elapsedMs: Date.now() - started.getTime(),
    scope: 'Fresh synthetic local Chromium accounts; real application, HTTP persistence and file downloads. No live device, Telegram or LLM integration. All created records are cleared during teardown.',
    base, browserVersion, serverToday: today, passed: failure === null, checks, errors, apiFailures, layout, screenshots, finalCounts, teardown, failure,
  }, null, 2) + '\n');
}
if (failure) { console.error(`${failure.name}: ${failure.message}`); process.exitCode = 1; }
