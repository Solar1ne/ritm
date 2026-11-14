// Synthetic browser acceptance. AI responses below are explicit route fixtures,
// never evidence of a live model call. Go adapter tests cover the HTTP contract.
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
import { isoDate } from '../frontend/static/format.js';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.RHYTHM_URL || 'http://127.0.0.1:8080';
assert.ok(['127.0.0.1','localhost'].includes(new URL(base).hostname));
const output = resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/ai-product'); await mkdir(output,{recursive:true});
const browser = await chromium.launch({channel:'chrome',headless:true});
const ctx = await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'});
const page = await ctx.newPage(), checks = [], errors = [];
page.setDefaultTimeout(12000);page.on('pageerror',e=>errors.push(e.message));
const snapshot = async()=> (await ctx.request.get(`${base}/api/state`)).json();
const action = name => page.locator(`[data-action="${name}"]`).first();
const closed = ()=>page.locator('dialog').waitFor({state:'hidden'});
const check = name=>{checks.push({name,passed:true});console.log(`PASS ${name}`);};
let aiCalls=0, aiFail=false, pauseContext=null, contextStarted=null;
try {
  let data=await snapshot();
  const today=isoDate(), weekday=(new Date(`${today}T12:00:00Z`).getUTCDay()+6)%7;
  const created=await ctx.request.post(`${base}/api/profile`,{headers:{'X-Rhythm-Session':data.sessionBinding},data:{revision:data.revision,profile:{name:'AI fixture synthetic',goal:'tone',experience:'beginner',days:[weekday],minutes:45}}});
  assert.equal(created.status(),200);data=await created.json();
  await page.goto(`${base}/#week`,{waitUntil:'networkidle'});
  await action('variant-open').click();await page.getByLabel('Сколько времени есть?').fill('20');
  await page.getByLabel('Где удобно заниматься?').selectOption('outdoors');await page.getByLabel('Без прыжков, бега и дополнительного веса').check();
  await page.locator('#variant-options-form button[type=submit]').click();await page.locator('#variant-apply-form').waitFor();
  assert.equal((await snapshot()).sessionAdjustments?.length||0,0,'preview never writes');
  await page.locator('input[value=outdoors-walk-v1]').check();await page.locator('#variant-apply-form button[type=submit]').click();await closed();
  data=await snapshot();assert.equal(data.plan.sessions[0].minutes,20);assert.equal(data.weeks.find(w=>w.weekStart===data.currentWeekStart).plan.sessions[0].minutes,45);
  await page.reload({waitUntil:'networkidle'});await page.getByText('Ваш вариант · исходно 45 мин').waitFor();
  await page.screenshot({path:resolve(output,'session-variant.png'),fullPage:true});
  check('Constraints preview, explicit apply, reload and immutable original schedule');

  await action('variant-open').click();await page.locator('#variant-revert-form button[type=submit]').click();await closed();
  assert.equal((await snapshot()).plan.sessions[0].minutes,45);
  check('Explicit revert restores the original session');

  await page.goto(`${base}/#activities`,{waitUntil:'networkidle'});await action('import-open').click();
  await page.locator('#import-preview-form input[type=file]').setInputFiles({name:'pace-fixture.csv',mimeType:'text/csv',buffer:Buffer.from(`id,date,kind,minutes,distance\npace-known,${today},running,20,3000\npace-unknown,${today},running,30,\n`)});
  await page.locator('#import-preview-form button[type=submit]').click();await page.locator('#import-save-form').waitFor();
  await page.locator('#import-save-form button[type=submit]').click();await closed();
  assert.equal((await snapshot()).workouts.length,2);
  await page.getByRole('region',{name:'Записанные пробежки за последние 30 дней'}).waitFor();
  assert.equal(await page.locator('.stats .stat-value').allTextContents().then(values=>values[2]),'6:40');
  await page.getByText('1 без дистанции',{exact:true}).waitFor();
  await page.setViewportSize({width:320,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.screenshot({path:resolve(output,'running-metrics-mobile.png'),fullPage:true});await page.setViewportSize({width:1440,height:1100});
  check('Real imported running metrics use only known distances and fit a 320px screen');

  await page.goto(`${base}/#assistant`,{waitUntil:'networkidle'});
  await page.getByRole('heading',{name:'AI пока не подключён',exact:true}).waitFor();
  assert.equal(await page.locator('#coach-form').count(),0);
  await action('rules-helper').click();
  assert.equal(await page.locator('#assistant-form input').evaluate(input=>input===document.activeElement),true);
  await page.locator('[data-action=assistant-ask]').first().click();await page.locator('.assistant-answer').waitFor();
  await page.screenshot({path:resolve(output,'assistant-unconfigured.png'),fullPage:true});
  check('Unconfigured provider is honest and deterministic helper remains functional');

  // Fixtures only: no real external model/network is used by this browser run.
  const configuredState = async route => {
    const result=await route.fetch(), body=await result.json();
    // Mutations return a complete state too. Preserve their real persistence
    // and errors while keeping this synthetic provider capability consistent.
    if(result.ok()) body.ai={configured:true,provider:'OpenAI',model:'synthetic-browser-fixture'};
    await route.fulfill({response:result,json:body});
  };
  await page.route('**/api/state',configuredState);
  await page.route('**/api/entries',configuredState);
  await page.route('**/api/ai/context?*',async route=>{const result=await route.fetch();const body=await result.json();body.status={configured:true,provider:'OpenAI',model:'synthetic-browser-fixture'};if(pauseContext){contextStarted?.();await pauseContext;}await route.fulfill({response:result,json:body});});
  await page.route('**/api/ai/respond',async route=>{
    aiCalls++;const input=route.request().postDataJSON();
    assert.equal(input.consent,true);assert.ok(input.contextToken);assert.equal(input.revision,(await snapshot()).revision);
    if(aiFail){await route.fulfill({status:503,json:{error:{code:'ai_unavailable',message:'Синтетический отказ AI. Помощник по правилам доступен.'}}});return;}
    const isCheckin=input.task==='checkin';
    await route.fulfill({json:{mode:'ai',provider:'OpenAI · синтетический тест',model:'fixture',revision:input.revision,facts:[{id:'f1',text:`Сегодня ${today}; один запланированный день.`}],result:{title:isCheckin?'Проверьте, верно ли я понял':'Ваша неделя: один понятный шаг',summary:'Синтетический ответ для проверки интерфейса. Это не результат живого вызова модели.',insights:[{text:'План — 45 минут. Отсутствие отметок не означает пропуск.',factIds:['f1']}],nextAction:isCheckin?'journal':'plan',draft:isCheckin?{date:today,status:'wellbeing',sessionId:'',sleep:7.5,fatigue:4}:null}}});
  });
  await page.reload({waitUntil:'networkidle'});
  await page.locator('[data-action=coach-task][data-task=checkin]').click();await page.locator('textarea[name=message]').fill('Сегодня спал 7,5 часов, усталость 4 из 10.');
  await page.locator('#coach-form button[type=submit]').click();await page.locator('#coach-consent-form').waitFor();
  assert.equal(aiCalls,0);assert.equal(await page.locator('input[name=consent]').isChecked(),false);
  await page.locator('#coach-consent-form button[type=submit]').click();assert.equal(aiCalls,0,'native validation blocks unchecked consent');
  await page.locator('input[name=consent]').check();await page.locator('#coach-consent-form button[type=submit]').click();await page.locator('.coach-answer').waitFor();
  assert.equal(aiCalls,1);assert.equal((await snapshot()).entries.length,0);
  await page.screenshot({path:resolve(output,'ai-draft-fixture.png'),fullPage:true});
  check('AI fixture is requested only after consent and a generated draft never autosaves');

  await action('coach-draft').click();await page.locator('#coach-draft-form input[name=sleep]').fill('8');await page.locator('#coach-draft-form button[type=submit]').click();await closed();
  data=await snapshot();assert.equal(data.entries.length,1);assert.equal(data.entries[0].sleep,8);
  check('User correction and separate confirmation save through real diary API');

  await page.goto(`${base}/#assistant`,{waitUntil:'networkidle'});await page.locator('[data-task=review]').click();await page.locator('#coach-form button[type=submit]').click();await page.locator('#coach-consent-form').waitFor({state:'visible'});await page.locator('input[name=consent]').check();await page.locator('#coach-consent-form button[type=submit]').click();await page.locator('.coach-answer').waitFor();
  await page.locator('.coach-insight summary').click();await page.getByText(`Сегодня ${today}; один запланированный день.`,{exact:true}).waitFor();
  await page.screenshot({path:resolve(output,'ai-review-fixture.png'),fullPage:true});
  check('Review fixture exposes the exact facts behind its observation');

  aiFail=true;await page.locator('[data-task=ask]').click();await page.locator('textarea[name=message]').fill('Почему?');await page.locator('#coach-form button[type=submit]').click();await page.locator('#coach-consent-form').waitFor({state:'visible'});await page.locator('input[name=consent]').check();await page.locator('#coach-consent-form button[type=submit]').click();await page.getByRole('alert').filter({hasText:'Синтетический отказ'}).waitFor();
  await page.locator('[data-action=assistant-ask]').first().click();await page.locator('.assistant-answer').waitFor();
  assert.equal((await snapshot()).entries.length,1);
  check('Provider failure preserves diary and the working rule-based path');

  let releaseContext;
  const started=new Promise(resolve=>{contextStarted=resolve;});pauseContext=new Promise(resolve=>{releaseContext=resolve;});
  await page.locator('textarea[name=message]').fill('Проверка запоздавшего контекста');await page.locator('#coach-form button[type=submit]').click();await started;
  await page.getByRole('button',{name:'Настройки и данные',exact:true}).click();await page.locator('#dialog-title').filter({hasText:'Ваше пространство'}).waitFor();
  releaseContext();await page.locator('#coach-form[aria-busy=false]').waitFor();pauseContext=null;
  assert.equal(await page.locator('#dialog-title').textContent(),'Ваше пространство');await action('close').click();
  check('Delayed AI context cannot overwrite a newly opened settings dialog');

  for(const width of [320,390,768,1440]) {
    await page.setViewportSize({width,height:900});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`no overflow at${width}`);
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:resolve(output,'assistant-mobile.png'),fullPage:true});
  assert.deepEqual(errors,[]);check('AI UI fits 320–1440px without uncaught browser errors');
  await writeFile(resolve(output,'browser.json'),JSON.stringify({executedAt:new Date().toISOString(),scope:'Synthetic local accounts. AI browser responses are labeled fixtures, NOT live model evidence. Real Go/API mutation and session-variant flows.',checks,errors,aiCalls},null,2));
} catch(error) {
  await page.screenshot({path:resolve(output,'failure.png'),fullPage:true}).catch(()=>{});throw error;
} finally {
  const data=await snapshot();
  if(data.profile?.name==='AI fixture synthetic')await ctx.request.delete(`${base}/api/data`,{headers:{'X-Rhythm-Session':data.sessionBinding},data:{revision:data.revision}});
  await browser.close();
}
