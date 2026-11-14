// Acceptance against an owned local Go/PostgreSQL fixture; no external AI/media.
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
import { EXERCISES } from '../frontend/static/exercises-data.js';
import { getDemoPhases } from '../frontend/static/exercise-demo.js';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:18081';
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname));
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || '/tmp/rhythm-exercises-browser');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' });
await context.addInitScript(() => {
  const request = window.requestAnimationFrame.bind(window), cancel = window.cancelAnimationFrame.bind(window);
  const active = new Set();
  window.requestAnimationFrame = callback => {
    const id = request(time => { active.delete(id); callback(time); });
    active.add(id); return id;
  };
  window.cancelAnimationFrame = id => { active.delete(id); cancel(id); };
  window.exerciseQAActiveFrames = () => active.size;
});
const page = await context.newPage();
page.setDefaultTimeout(12000);
const checks = [], errors = [], writes = [], externalMedia = [], illustrations = [];
const check = name => { checks.push({ name, passed: true }); console.log(`PASS ${name}`); };
page.on('pageerror', error => errors.push(error.message));
const snapshot = async () => {
  const response = await context.request.get(`${base}/api/state`);
  assert.equal(response.status(), 200); return response.json();
};
const close = async () => { await page.locator('dialog [data-action=close]').first().click(); await page.locator('dialog').waitFor({ state: 'hidden' }); };
const open = async id => { await page.locator(`[data-action=exercise-open][data-id="${id}"]`).click(); await page.locator('[data-exercise-demo]').waitFor(); };
const frames = () => page.evaluate(() => window.exerciseQAActiveFrames());
try {
  let current = await snapshot();
  const created = await context.request.post(`${base}/api/profile`, {
    headers: { Origin: base, 'X-Rhythm-Session': current.sessionBinding },
    data: { revision: current.revision, profile: { name: 'Exercise QA fixture', goal: 'tone', experience: 'beginner', days: [0, 2, 4], minutes: 45 } },
  });
  assert.equal(created.status(), 200);
  await page.goto(`${base}/#week`, { waitUntil: 'networkidle' });
  await page.getByRole('link', { name: 'Техника упражнений', exact: true }).click();
  await page.locator('[data-exercise-library]').waitFor();
  const baseline = await snapshot();
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.origin === base && !['GET', 'HEAD'].includes(request.method())) writes.push(url.pathname);
    if (url.origin !== base && ['image', 'media'].includes(request.resourceType())) externalMedia.push(url.href);
  });
  assert.equal(await page.locator('.exercise-card').count(), 12);
  check('Week links to the complete 12-exercise catalog');

  const search = page.getByRole('searchbox', { name: 'Найти упражнение' });
  await search.pressSequentially('PLANK');
  assert.equal(await page.locator('.exercise-card').count(), 1);
  assert.equal(await search.evaluate(input => input === document.activeElement), true);
  assert.equal(await search.evaluate(input => input.selectionStart), 5);
  await search.fill('подъем на носки');
  assert.equal(await page.locator('.exercise-card').count(), 1);
  await search.fill('');
  await page.getByLabel('Группа мышц', { exact: true }).selectOption('arms');
  await page.getByLabel('Инвентарь', { exact: true }).selectOption('dumbbells');
  assert.equal(await page.locator('.exercise-card').count(), EXERCISES.filter(item => item.muscles.includes('arms') && item.equipment.includes('dumbbells')).length);
  await search.fill('неттакогоупражнения');
  assert.match(await page.locator('#exercise-results').innerText(), /Пока ничего не нашлось/);
  await page.getByRole('button', { name: 'Сбросить фильтры', exact: true }).click();
  assert.equal(await page.locator('.exercise-card').count(), 12);
  assert.equal(await search.inputValue(), '');
  await search.press('Enter');
  assert.equal(new URL(page.url()).hash, '#exercises');
  check('Search preserves focus/caret, normalizes aliases and ё, combines filters and clears an empty result');

  for (const [width, height] of [[320, 800], [390, 844], [768, 850], [1280, 640], [1440, 1000]]) {
    await page.setViewportSize({ width, height });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `catalog overflow ${width}`);
    for (const rect of await page.locator('.exercise-card button, .exercise-filters input, .exercise-filters select, .exercise-filters button').evaluateAll(elements => elements.map(element => element.getBoundingClientRect().height))) assert.ok(rect >= 44, `target ${rect}px at ${width}`);
    await page.screenshot({ path: resolve(output, `catalog-${width}.png`), fullPage: true });
  }
  check('Catalog fits 320–1440px with 44px controls and a scrollable desktop sidebar');

  for (const exercise of EXERCISES) {
    await open(exercise.id);
    const detail = page.locator('.exercise-detail');
    assert.equal(await detail.locator('.exercise-steps li').count(), exercise.steps.length);
    assert.match(await detail.innerText(), /Частые ошибки/);
    assert.match(await detail.innerText(), /Как упростить/);
    assert.match(await detail.innerText(), /иллюстрация, а не видеозапись/);
    const sources = await detail.locator('.exercise-sources a').evaluateAll(links => links.map(link => ({ href: link.href, target: link.target, rel: link.rel })));
    assert.equal(sources.length, exercise.sources.length);
    for (const source of sources) { assert.equal(new URL(source.href).protocol, 'https:'); assert.equal(source.target, '_blank'); assert.match(source.rel, /noopener/); assert.match(source.rel, /noreferrer/); }
    const poses = [];
    const range = page.locator('[data-demo-range]');
    if (exercise.id === 'plank') {
      assert.equal(await range.count(), 0);
      assert.equal(await detail.locator('[data-demo-action=play]').count(), 0);
      poses.push(await detail.locator('svg').evaluate(svg => svg.outerHTML));
    } else {
      assert.equal(await range.inputValue(), '0');
      assert.equal(await page.locator('[data-demo-action=play]').isDisabled(), true);
      assert.equal(await frames(), 0);
      poses.push(await detail.locator('svg').evaluate(svg => svg.outerHTML));
      const phases = getDemoPhases(exercise.id);
      for (let phase = 1; phase < phases.length; phase++) {
        await page.getByRole('button', { name: 'Следующее положение', exact: true }).click();
        assert.equal(await range.inputValue(), String(Math.round(100 * phase / (phases.length - 1))));
        assert.equal(await detail.locator('[data-demo-phase]').innerText(), phases[phase]);
        poses.push(await detail.locator('svg').evaluate(svg => svg.outerHTML));
      }
      await page.getByRole('button', { name: 'Вернуть исходное положение', exact: true }).click();
      assert.equal(await range.inputValue(), '0');
    }
    illustrations.push({ name: exercise.name, poses });
    for (const width of [320, 390, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      assert.equal(await page.locator('dialog').evaluate(dialog => dialog.scrollWidth > dialog.clientWidth + 1), false, `detail ${exercise.id} overflow ${width}`);
    }
    if (exercise.id === 'reverse-lunge') await page.locator('dialog').screenshot({ path: resolve(output, 'lunge-detail.png') });
    await close();
    assert.equal(await frames(), 0);
  }
  check('Every exercise has technique, mistakes, easier variant, valid sources and working manual phases; plank is static');

  await open('squat');
  for (const theme of ['dark', 'light']) {
    await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
    const ratios = await page.locator('.exercise-detail .exercise-tags li, [data-demo-phase], .exercise-demo-note, [data-demo-motion-note]').evaluateAll(elements => {
      const rgb = color => color.match(/[\d.]+/g).slice(0, 3).map(Number);
      const luminance = color => rgb(color).map(channel => { const value = channel / 255; return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4; }).reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0);
      return elements.map(element => {
        let ancestor = element, background = 'rgba(0, 0, 0, 0)';
        while (ancestor && background === 'rgba(0, 0, 0, 0)') { background = getComputedStyle(ancestor).backgroundColor; ancestor = ancestor.parentElement; }
        const foreground = luminance(getComputedStyle(element).color), surface = luminance(background);
        return { text: element.textContent, ratio: (Math.max(foreground, surface) + 0.05) / (Math.min(foreground, surface) + 0.05) };
      });
    });
    for (const item of ratios) assert.ok(item.ratio >= 4.5, `${theme} contrast ${item.ratio}: ${item.text}`);
    await page.locator('dialog').screenshot({ path: resolve(output, `detail-${theme}.png`) });
  }
  await close();
  check('Technique labels and controls retain readable text in light and dark themes');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Все разделы', exact: true }).click();
  await page.locator('.section-menu a[data-route=exercises]').click();
  assert.equal(await page.locator('dialog').isVisible(), false);
  await page.getByRole('button', { name: 'Настройки и данные', exact: true }).click();
  await page.locator('[data-action=exercise-library]').click();
  assert.equal(await page.locator('dialog').isVisible(), false);
  assert.equal(new URL(page.url()).hash, '#exercises');
  check('Mobile More and settings both open the catalog and close their dialogs');

  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await open('squat');
  assert.equal(await frames(), 0, 'no autoplay');
  await page.getByRole('button', { name: 'Запустить демонстрацию', exact: true }).click();
  await page.waitForFunction(() => Number(document.querySelector('[data-demo-range]').value) > 5);
  await page.getByRole('button', { name: 'Приостановить демонстрацию', exact: true }).click();
  const paused = await page.locator('[data-demo-range]').inputValue();
  await page.waitForTimeout(180);
  assert.equal(await page.locator('[data-demo-range]').inputValue(), paused);
  assert.equal(await frames(), 0);
  await page.getByRole('button', { name: 'Запустить демонстрацию', exact: true }).click();
  await page.keyboard.press('Escape');
  await page.locator('dialog').waitFor({ state: 'hidden' });
  // Native close dispatch is queued after the open attribute is removed.
  await page.waitForFunction(() => window.exerciseQAActiveFrames() === 0);
  assert.equal(await frames(), 0);
  await open('squat');
  await page.getByRole('button', { name: 'Запустить демонстрацию', exact: true }).click();
  // Exercise shared modal ownership when another app action replaces content.
  await page.evaluate(() => document.querySelector('[data-action=settings]').click());
  await page.locator('.data-usage').waitFor();
  assert.equal(await frames(), 0);
  await close();
  await open('reverse-lunge');
  await page.getByRole('button', { name: 'Запустить демонстрацию', exact: true }).click();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.waitForFunction(() => document.querySelector('[data-demo-action=play]').disabled);
  assert.equal(await frames(), 0);
  await close();
  check('Explicit play/pause works; Escape, modal replacement and reduced-motion changes cancel animation frames');

  assert.deepEqual(await snapshot(), baseline);
  assert.deepEqual(writes, []);
  assert.deepEqual(externalMedia, []);
  assert.deepEqual(errors, []);
  check('Browsing never mutates the diary, requests external media or raises browser errors');

  const sheet = await context.newPage();
  const demoCSS = await readFile(new URL('../frontend/static/exercise-demo.css', import.meta.url), 'utf8');
  await sheet.setViewportSize({ width: 1440, height: 1100 });
  for (let offset = 0; offset < illustrations.length; offset += 6) {
    const items = illustrations.slice(offset, offset + 6);
    await sheet.setContent(`<html lang="ru"><meta charset="utf-8"><style>:root{--green:#416653;--green-soft:#e7eee5;--ink:#252b29;--muted:#727770;--line:#e3e5dc}body{font:14px sans-serif;background:#f6f5f0;padding:20px}.sheet{display:grid;grid-template-columns:repeat(2,1fr);gap:20px}.item{background:#fffefa;padding:12px;border:1px solid #ddd;border-radius:12px}.poses{display:flex}.poses svg{width:100%;min-width:0;max-height:210px}h2{font-size:17px}${demoCSS}</style><div class="sheet">${items.map(item => `<section class="item"><h2>${item.name}</h2><div class="poses">${item.poses.join('')}</div></section>`).join('')}</div></html>`);
    await sheet.screenshot({ path: resolve(output, `movement-sheet-${1 + offset / 6}.png`), fullPage: true });
  }
  await sheet.close();
  await writeFile(resolve(output, 'result.json'), JSON.stringify({ passed: true, checks, browserErrors: errors, diaryWrites: writes, externalMedia }, null, 2) + '\n');
} catch (error) {
  await page.screenshot({ path: resolve(output, 'failure.png'), fullPage: true }).catch(() => {});
  await writeFile(resolve(output, 'result.json'), JSON.stringify({ passed: false, checks, error: String(error), browserErrors: errors }, null, 2) + '\n');
  throw error;
} finally { await context.close(); await browser.close(); }
