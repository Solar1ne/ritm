// Browser acceptance for the full lifecycle. All accounts/data are synthetic,
// isolated to this run and cleaned up. No Telegram messages or cloud API calls.
import { createRequire } from 'node:module';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
import { isoDate, addDays } from '../frontend/static/format.js';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:8080';
assert.ok(['127.0.0.1','localhost'].includes(new URL(base).hostname));
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/full-product'); await mkdir(output, { recursive:true });
const browser = await chromium.launch({ channel:'chrome', headless:true });
const checks = [], errors = [], contexts = [];
const check = name => { checks.push({name,passed:true}); console.log(`PASS ${name}`); };
const snapshot = async ctx => (await ctx.request.get(`${base}/api/state`)).json();
async function cleanup(ctx) {
  const value = await snapshot(ctx);
  if (!value.profile?.name?.startsWith('UI fixture')) return;
  const response = await ctx.request.delete(`${base}/api/data`, {headers:{'X-Rhythm-Session':value.sessionBinding},data:{revision:value.revision}});
  assert.equal(response.status(),200);
}
async function makeContext() {
  const ctx = await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'});contexts.push(ctx);
  const page = await ctx.newPage();page.on('pageerror',error=>errors.push(error.message));page.setDefaultTimeout(12000);
  return {ctx,page};
}
const {ctx,page} = await makeContext();
const action = (name) => page.locator(`[data-action="${name}"]`).first();
const close = () => page.locator('dialog').waitFor({state:'hidden'});
async function importFile(content, filename = 'activities.csv') {
  await action('import-open').click();
  await page.locator('#import-preview-form input[type=file]').setInputFiles({name:filename,mimeType:filename.endsWith('tcx')?'application/xml':'text/csv',buffer:Buffer.from(content)});
  await page.locator('#import-preview-form button[type=submit]').click();
  await page.locator('#import-save-form').waitFor();
}
try {
  await page.goto(base,{waitUntil:'networkidle'});
  await page.getByRole('button',{name:'Создать мой план',exact:true}).click();
  assert.equal(await page.locator('#dialog-title').evaluate(e=>e===document.activeElement),true);
  await page.getByLabel('Как к вам обращаться?').fill('UI fixture Полный цикл');
  await page.locator('#profile-form button[type=submit]').click();await close();
  assert.equal(new URL(page.url()).search,'');
  let data = await snapshot(ctx);
  const currentPlan = structuredClone(data.plan), firstSession = data.plan.sessions[0];
  check('Native profile submit saves without query-string navigation; dialog focuses its heading');

  await page.goto(`${base}/#week`,{waitUntil:'networkidle'});
  await action('week-next').click();await action('plan-accept').click();
  await page.locator('#plan-form button[type=submit]').click();await close();
  data = await snapshot(ctx);assert.ok(data.acceptedNextPlan);
  const accepted = structuredClone(data.acceptedNextPlan);
  await page.reload({waitUntil:'networkidle'});await action('week-next').click();
  await page.getByText('Этот план начнётся в понедельник.',{exact:false}).waitFor();
  check('Explicit next-week acceptance persists across reload');

  await action('profile').click();
  await page.getByRole('slider',{name:'Длительность занятия',exact:false}).focus();
  await page.keyboard.press('Home'); for(let i=0;i<9;i++) await page.keyboard.press('ArrowRight');
  await page.locator('#profile-form button[type=submit]').click();await close();
  data = await snapshot(ctx);assert.deepEqual(data.plan,currentPlan);assert.deepEqual(data.acceptedNextPlan,accepted);assert.notEqual(data.nextPlan.totalMinutes,accepted.totalMinutes);
  await action('week-next').click();await action('plan-cancel').click();await page.locator('#plan-form button[type=submit]').click();await close();
  data = await snapshot(ctx);assert.equal(data.acceptedNextPlan,null);
  check('Profile changes preserve current and accepted snapshots; explicit cancel exposes the new proposal');

  // Stale confirmation must fail instead of silently accepting a different plan.
  await action('plan-accept').click();
  const response = await ctx.request.post(`${base}/api/entries`,{headers:{'X-Rhythm-Session':data.sessionBinding},data:{revision:data.revision,entry:{id:'fixture-conflict',date:isoDate(),status:'wellbeing',sleep:5,fatigue:9}}});
  assert.equal(response.status(),200);
  await page.locator('#plan-form button[type=submit]').click();
  await page.locator('#form-error').filter({hasText:'Данные обновлены'}).waitFor();
  assert.equal((await snapshot(ctx)).acceptedNextPlan,null);
  await action('close').click();await action('plan-accept').click();await page.locator('#plan-form button[type=submit]').click();await close();
  // The future profile is 3×60, but recovery is also capped by the saved
  // current 3×45 schedule: two sessions of36 minutes, rather than48.
  assert.equal((await snapshot(ctx)).acceptedNextPlan.totalMinutes,72);
  check('Stale plan confirmation is rejected and requires reviewing the current proposal');

  await page.goto(`${base}/#journal`,{waitUntil:'networkidle'});
  await action('entry-edit').click();assert.equal(await page.locator('input[name=date]').inputValue(),isoDate());
  await page.getByLabel('Сколько часов вы спали?').fill('8');
  await page.getByLabel('Усталость сейчас').selectOption('3');
  await page.locator('#entry-form button[type=submit]').click();await close();
  assert.equal((await snapshot(ctx)).entries.find(e=>e.status==='wellbeing').sleep,8);
  await action('entry-delete').click();await page.locator('#fact-delete-form button[type=submit]').click();await close();
  assert.equal((await snapshot(ctx)).entries.length,0);
  check('Journal supports editing and confirmed deletion of individual facts');

  await page.goto(`${base}/#activities`,{waitUntil:'networkidle'});
  const csv = `id,date,kind,minutes,distance\nwalk-fixture,${firstSession.date},walking,32,2400\nrun-fixture,${isoDate()},running,20,3000\n`;
  await importFile(csv);await page.locator('#import-save-form input[type=checkbox]').check();
  await page.locator('#import-save-form button[type=submit]').click();await close();
  data = await snapshot(ctx);assert.equal(data.workouts.length,2);assert.equal(data.workouts.find(w=>w.kind==='walk').sessionId,firstSession.date);assert.equal(data.plan.sessions[0].status,'done');
  const importedRevision=data.revision;
  await importFile(csv);await page.locator('#import-save-form input[type=checkbox]').check();await page.locator('#import-save-form button[type=submit]').click();await close();
  data=await snapshot(ctx);assert.equal(data.workouts.length,2);assert.equal(data.revision,importedRevision);
  await page.locator('#toast').waitFor({state:'hidden'});
  await page.screenshot({path:resolve(output,'activities.png'),fullPage:true});
  check('CSV preview, explicit plan linking and repeated-file idempotency work through the UI');

  await action('import-open').click();
  await page.locator('#import-preview-form input[type=file]').setInputFiles({name:'invalid.csv',mimeType:'text/csv',buffer:Buffer.from(`id,date,kind,minutes\nnew,${isoDate()},walking,20\nwalk-fixture,${firstSession.date},walking,99\n`)});
  await page.locator('#import-preview-form button[type=submit]').click();await page.locator('#form-error').filter({hasText:'идентификатор'}).waitFor();
  assert.equal((await snapshot(ctx)).revision,importedRevision);await action('close').click();
  const tcx=`<?xml version="1.0"?><TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"><Activities><Activity Sport="Running"><Id>${isoDate()}T01:00:00Z</Id><Lap StartTime="${isoDate()}T01:00:00Z"><TotalTimeSeconds>600</TotalTimeSeconds><DistanceMeters>1500</DistanceMeters></Lap></Activity></Activities></TrainingCenterDatabase>`;
  await importFile(tcx,'activity.tcx');await page.locator('#import-save-form button[type=submit]').click();await close();assert.equal((await snapshot(ctx)).workouts.length,3);
  await action('workout-delete').click();await page.locator('#fact-delete-form button[type=submit]').click();await close();assert.equal((await snapshot(ctx)).workouts.length,2);
  check('Conflicting import is atomic; TCX import and independent activity deletion work');

  await page.getByRole('button',{name:'Настройки и данные',exact:true}).click();await action('reminders').click();
  assert.equal(await page.locator('#dialog-title').evaluate(e=>e===document.activeElement),true);
  await page.locator('#reminder-form input[name=enabled]').check();await page.locator('#reminder-form input[name=time]').fill('18:30');await page.locator('#reminder-form select').selectOption('15');
  await page.locator('#reminder-form button[type=submit]').click();await close();
  await page.goto(`${base}/#week`,{waitUntil:'networkidle'});
  const downloadPromise=page.waitForEvent('download');await action('calendar-download').click();const downloaded=await downloadPromise;
  const calendar=await readFile(await downloaded.path(),'utf8');assert.match(calendar,/BEGIN:VCALENDAR/);assert.match(calendar,/TRIGGER:-PT15M/);assert.match(calendar,/T153000Z/);
  check('Reminder preferences persist and produce a real ICS with local-time conversion and alarm');

  const assistantRevision=(await snapshot(ctx)).revision;
  await page.goto(`${base}/#assistant`,{waitUntil:'networkidle'});
  await page.getByRole('button',{name:'Почему изменился план?',exact:true}).click();await page.locator('.assistant-answer').waitFor();assert.ok((await page.locator('.answer-text').textContent()).length>30);
  await page.locator('#assistant-form input').fill('Подскажи мой пароль');await page.locator('#assistant-form button').click();await page.locator('.assistant-answer').waitFor();
  assert.equal((await snapshot(ctx)).revision,assistantRevision);
  await page.screenshot({path:resolve(output,'assistant.png'),fullPage:true});
  for (const question of ['Что у меня сегодня?', 'Какой у меня прогресс?', 'Что с восстановлением?']) {
    await page.getByRole('button',{name:question,exact:true}).click();await page.locator('.assistant-answer').waitFor();
    assert.doesNotMatch(await page.locator('.answer-text').textContent(), /могу помочь только|не поддерживаю|не входит/i);
  }
  check('Assistant explains personal data, all four presets work, and unsupported questions are scoped');

  // Keep the exported archive realistic while adding a synthetic known past
  // week, so browser history navigation is exercised independently of the clock.
  data=await snapshot(ctx);
  const backupResponse=await ctx.request.get(`${base}/api/export`,{headers:{'X-Rhythm-Session':data.sessionBinding}});assert.equal(backupResponse.status(),200);
  const backup=await backupResponse.json(), oldWeek=structuredClone(backup.weeks.find(w=>w.weekStart===backup.currentWeekStart));
  oldWeek.weekStart=addDays(oldWeek.weekStart,-7);oldWeek.plan.weekStart=oldWeek.weekStart;
  oldWeek.plan.sessions=oldWeek.plan.sessions.map(s=>({...s,id:addDays(s.id,-7),date:addDays(s.date,-7)}));backup.weeks.unshift(oldWeek);
  const restored=await makeContext();await restored.page.goto(base,{waitUntil:'networkidle'});
  await restored.page.getByRole('button',{name:'Настройки и данные',exact:true}).click();await restored.page.locator('[data-action=restore]').click();
  await restored.page.locator('#restore-preview-form input').setInputFiles({name:'rhythm.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(backup))});
  await restored.page.locator('#restore-preview-form button[type=submit]').click();await restored.page.locator('#restore-save-form button[type=submit]').click();await restored.page.locator('dialog').waitFor({state:'hidden'});
  const recovered=await snapshot(restored.ctx);assert.equal(recovered.workouts.length,backup.workouts.length);assert.notEqual(recovered.sessionBinding,backup.sessionBinding);assert.equal(recovered.weeks.length,backup.weeks.length);
  await restored.page.goto(`${base}/#week`,{waitUntil:'networkidle'});await restored.page.locator('#week-select').selectOption(oldWeek.weekStart);
  await restored.page.getByText('Сохранённое расписание:',{exact:false}).waitFor();
  const past=oldWeek.plan.sessions[0];await restored.page.locator(`.session-row [data-id="${past.id}"]`).click();await restored.page.locator('#entry-form button[type=submit]').click();await restored.page.locator('dialog').waitFor({state:'hidden'});
  assert.equal((await snapshot(restored.ctx)).entries.find(e=>e.sessionId===past.id).status,'done');
  await restored.page.screenshot({path:resolve(output,'history.png'),fullPage:true});
  check('Backup restores into a different identity; historical snapshots can be viewed and backfilled');

  for(const width of [320,390,768,1440]) {
    await page.setViewportSize({width,height:900});
    for(const route of ['week','journal','activities','assistant']) {
      await page.goto(`${base}/#${route}`,{waitUntil:'networkidle'});
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${route} overflow at ${width}`);
    }
  }
  await page.setViewportSize({width:390,height:844});await page.goto(`${base}/#week`,{waitUntil:'networkidle'});await action('week-next').click();await page.locator('#toast').waitFor({state:'hidden'});await page.screenshot({path:resolve(output,'accepted-mobile.png'),fullPage:true});
  check('All new screens fit 320, 390, 768 and 1440px with real content');

  // An in-flight read must never replace an open form or its draft.
  await page.goto(base,{waitUntil:'networkidle'});await page.getByRole('button',{name:'Отметить день',exact:true}).click();await page.getByLabel('Сколько часов вы спали?').fill('6.5');
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));assert.equal(await page.getByLabel('Сколько часов вы спали?').inputValue(),'6.5');await page.keyboard.press('Escape');
  check('Visibility/focus refresh preserves an open diary form');
  assert.deepEqual(errors,[]);check('No uncaught JavaScript errors in the full-product flow');
  await writeFile(resolve(output,'full-product-browser.json'),JSON.stringify({generatedAt:new Date().toISOString(),scope:'Synthetic local Chromium browser acceptance; no live Telegram/device integration',checks,errors},null,2)+'\n');
} catch(error) {await page.screenshot({path:resolve(output,'full-product-failure.png'),fullPage:true});throw error;}
finally {for(const context of contexts) {await cleanup(context);await context.close();} await browser.close();}
