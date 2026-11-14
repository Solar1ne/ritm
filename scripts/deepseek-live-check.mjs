// Explicit paid smoke: three model calls against a separate local QA server.
// Only this script's synthetic profile is used or removed. No API key is read.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

if (process.env.RHYTHM_LIVE_AI !== '1') throw new Error('Set RHYTHM_LIVE_AI=1 to authorize up to three paid calls.');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:18081';
assert.equal(base, 'http://127.0.0.1:18081', 'Use the separate local QA database/server on port 18081.');
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/deepseek/live');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 1080 }, reducedMotion: 'reduce' });
const page = await ctx.newPage(), checks = [], calls = [], errors = [];
page.setDefaultTimeout(12000);
page.on('pageerror', error => errors.push(error.message));
let paidRequests = 0;
page.on('request', request => { if (new URL(request.url()).pathname === '/api/ai/respond') paidRequests++; });
const snapshot = async () => {
  const response = await ctx.request.get(`${base}/api/state`);
  assert.equal(response.status(), 200);
  return response.json();
};
const check = name => { checks.push({ name, passed: true }); console.log(`PASS ${name}`); };
try {
  let state = await snapshot();
  assert.equal(state.profile, null, 'Fresh browser session must have no existing profile.');
  assert.equal(state.ai.provider, 'DeepSeek'); assert.equal(state.ai.configured, true);
  const day = (new Date(`${state.today}T12:00:00Z`).getUTCDay() + 6) % 7;
  const profile = await ctx.request.post(`${base}/api/profile`, {
    headers: { 'X-Rhythm-Session': state.sessionBinding },
    data: { revision: state.revision, profile: { name: 'DeepSeek synthetic check', goal: 'tone', experience: 'beginner', days: [day], minutes: 30 } },
  });
  assert.equal(profile.status(), 200);
  await page.goto(`${base}/#assistant`, { waitUntil: 'networkidle' });
  await page.locator('.coach-hero').getByText('AI · DeepSeek', { exact: false }).waitFor();
  for (const [task, message] of [
    ['checkin', 'Сегодня только самочувствие: спал 7,5 часов, усталость 4 из 10. О выполнении занятия не сообщаю.'],
    ['review', 'Кратко перечисли сохранённые наблюдения и каких данных пока не хватает.'],
    ['ask', 'Сколько минут запланировано на текущее занятие? Не считай план выполненной тренировкой.'],
  ]) {
    await page.locator(`[data-action=coach-task][data-task=${task}]`).click();
    await page.locator('textarea[name=message]').fill(message);
    await page.locator('#coach-form button[type=submit]').click();
    await page.locator('#coach-consent-form').waitFor();
    assert.match(await page.locator('#coach-consent-form').innerText(), /в DeepSeek для одного ответа/);
    assert.equal(await page.locator('input[name=consent]').isChecked(), false);
    assert.equal(paidRequests, calls.length, 'Preview must not make a paid call.');
    if (task === 'checkin') await page.screenshot({ path: resolve(output, 'consent.png'), fullPage: true });
    const before = await snapshot();
    await page.locator('input[name=consent]').check();
    const pending = page.waitForResponse(response => new URL(response.url()).pathname === '/api/ai/respond', { timeout: 45000 });
    const started = performance.now();
    await page.locator('#coach-consent-form button[type=submit]').click();
    const response = await pending;
    const body = await response.json();
    assert.equal(response.status(), 200, JSON.stringify(body));
    assert.equal(body.provider, 'DeepSeek'); assert.equal(body.mode, 'ai');
    await page.locator('.coach-answer').waitFor();
    const after = await snapshot();
    assert.equal(after.revision, before.revision, 'AI cannot mutate the diary.');
    assert.deepEqual(after.entries, before.entries);
    calls.push({ task, durationMs: Math.round(performance.now() - started), model: body.model, result: body.result, facts: body.facts });
    await page.screenshot({ path: resolve(output, `${task}.png`), fullPage: true });
    check(`Live DeepSeek ${task}: validated response, explicit consent, no automatic writes`);
    if (task === 'checkin') {
      assert.equal(body.result.draft.status, 'wellbeing');
      assert.equal(body.result.draft.sleep, 7.5); assert.equal(body.result.draft.fatigue, 4);
      assert.equal(body.result.draft.sessionId, ''); assert.equal(body.result.draft.date, after.today);
      await page.locator('[data-action=coach-draft]').click();
      await page.locator('#coach-draft-form input[name=sleep]').fill('8');
      await page.locator('#coach-draft-form button[type=submit]').click();
      await page.locator('dialog').waitFor({ state: 'hidden' });
      state = await snapshot();
      assert.equal(state.entries.length, 1); assert.equal(state.entries[0].sleep, 8);
      await page.reload({ waitUntil: 'networkidle' });
      assert.equal((await snapshot()).entries[0].sleep, 8);
      check('User correction and separate confirmation persist in PostgreSQL after reload');
      await page.goto(`${base}/#assistant`, { waitUntil: 'networkidle' });
    }
  }
  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `overflow at ${width}`);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: resolve(output, 'mobile.png'), fullPage: true });
  assert.deepEqual(errors, []); assert.equal(paidRequests, 3);
  check('Live answers fit mobile and desktop; no browser errors; exactly three paid calls');
  await writeFile(resolve(output, 'results.json'), JSON.stringify({ executedAt: new Date().toISOString(), scope: 'Live DeepSeek via Go API and PostgreSQL. Synthetic isolated profile only; no response fixtures.', checks, calls, errors, paidRequests }, null, 2));
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failure.png'), fullPage: true }).catch(() => {});
  await writeFile(resolve(output, 'failure.json'), JSON.stringify({ message: error.message, checks, calls, paidRequests, errors }, null, 2));
  throw error;
} finally {
  try {
    const state = await snapshot();
    if (state.profile?.name === 'DeepSeek synthetic check') {
      const response = await ctx.request.delete(`${base}/api/data`, { headers: { 'X-Rhythm-Session': state.sessionBinding }, data: { revision: state.revision } });
      assert.equal(response.status(), 200, 'Synthetic profile cleanup failed.');
    }
  } finally { await browser.close(); }
}
