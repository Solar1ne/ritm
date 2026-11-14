// Isolated acceptance of first-run UX against the PostgreSQL QA server.
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base=process.env.RHYTHM_URL || 'http://127.0.0.1:18081';
assert.equal(new URL(base).hostname,'127.0.0.1');assert.equal(new URL(base).port,'18081');
const output=resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/postgresql/first-run');await mkdir(output,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const ctx=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'});
const page=await ctx.newPage(),errors=[],checks=[];page.on('pageerror',e=>errors.push(e.message));
const check=name=>{checks.push({name,passed:true});console.log(`PASS ${name}`);};
const noOverflow=async()=>assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
try {
 await page.goto(base,{waitUntil:'networkidle'});
 const health=await (await ctx.request.get(`${base}/api/health`)).json();assert.equal(health.storage,'postgresql');
 await page.locator('.welcome').waitFor();assert.equal(await page.locator('.stats').count(),0);assert.equal(await page.getByRole('button',{name:'Создать мой план',exact:true}).count(),1);
 await noOverflow();await page.screenshot({path:resolve(output,'welcome-desktop.png'),fullPage:true});check('PostgreSQL health and first visit with one primary action and no invented personal metrics');
 await page.getByRole('link',{name:'Посмотреть пример',exact:false}).click();await page.locator('.session-row').first().waitFor();assert.match(await page.locator('#main').innerText(),/Это пример недели/);check('Week example is explicitly labelled and opened separately');
 await page.goto(base,{waitUntil:'networkidle'});await page.getByRole('button',{name:'Создать мой план',exact:true}).click();
 await page.getByLabel('Как к вам обращаться?').fill('UI fixture Первый запуск');
 await page.locator('[name=days][value="4"]').uncheck();await page.locator('#minutes').fill('30');
 await page.getByText('2 × 30 мин · 60 мин / нед',{exact:true}).waitFor();assert.match(await page.locator('#profile-preview-days').innerText(),/Пн · Ср/);
 await page.screenshot({path:resolve(output,'plan-preview.png'),fullPage:true});
 await page.locator('#profile-form button[type=submit]').click();await page.locator('dialog').waitFor({state:'hidden'});
 let state=await (await ctx.request.get(`${base}/api/state`)).json();assert.equal(state.plan.totalMinutes,60);assert.deepEqual(state.profile.days,[0,2]);
 check('Live profile preview agrees with the plan actually saved in PostgreSQL');
 for(const width of [390,320]) {
  await page.setViewportSize({width,height:900});await noOverflow();
  await page.getByRole('button',{name:'Все разделы',exact:true}).click();await page.getByRole('navigation',{name:'Все разделы Ритма'}).waitFor();
  assert.equal(await page.locator('dialog').evaluate(e=>e.scrollWidth>e.clientWidth+1),false);
  for(const link of await page.locator('.section-menu a').all()){assert.ok((await link.boundingBox()).height>=44);}
  if(width===390) await page.screenshot({path:resolve(output,'mobile-menu.png'),fullPage:true});
  await page.locator('.section-menu a[href="#nutrition"]').click();await page.locator('dialog').waitFor({state:'hidden'});assert.equal(new URL(page.url()).hash,'#nutrition');
  await page.getByRole('button',{name:'Все разделы',exact:true}).click();await page.locator('.section-menu a[href="#nutrition"]').click();await page.locator('dialog').waitFor({state:'hidden'});
  await page.getByRole('button',{name:'Все разделы',exact:true}).click();await page.locator('.section-menu a[href="#assistant"]').click();assert.equal(new URL(page.url()).hash,'#assistant');
  await noOverflow();
 }
 check('320/390px menu exposes nutrition and assistant, closes on same-route navigation, and has 44px targets');
 await page.getByRole('button',{name:'Все разделы',exact:true}).click();await page.keyboard.press('Escape');await page.locator('dialog').waitFor({state:'hidden'});assert.equal(await page.getByRole('button',{name:'Все разделы',exact:true}).evaluate(e=>e===document.activeElement),true);
 check('Keyboard escape restores focus to the mobile menu trigger');
 await page.reload({waitUntil:'networkidle'});state=await (await ctx.request.get(`${base}/api/state`)).json();assert.equal(state.profile.name,'UI fixture Первый запуск');assert.equal(state.plan.totalMinutes,60);
 check('Reload preserves the new profile and selected schedule');
 const response=await ctx.request.delete(`${base}/api/data`,{headers:{'X-Rhythm-Session':state.sessionBinding},data:{revision:state.revision}});assert.equal(response.status(),200);
 await page.goto(base,{waitUntil:'networkidle'});await page.locator('.welcome').waitFor();await page.screenshot({path:resolve(output,'welcome-mobile.png'),fullPage:true});
 const themeFixture={...await (await ctx.request.get(`${base}/api/state`)).json(),identity:'telegram'};
 const dark=await browser.newContext({viewport:{width:390,height:900},reducedMotion:'reduce'});
 await dark.addInitScript(()=>{window.Telegram={WebApp:{initData:'synthetic-theme-only',colorScheme:'dark',ready(){},expand(){},isVersionAtLeast(){return false;},onEvent(){},BackButton:{onClick(){},show(){},hide(){}}}};});
 const darkPage=await dark.newPage();await darkPage.route('**/api/auth/telegram',r=>r.fulfill({status:200,contentType:'application/json',body:JSON.stringify(themeFixture)}));
 await darkPage.goto(`${base}/mini`,{waitUntil:'networkidle'});await darkPage.locator('.welcome').waitFor();assert.equal(await darkPage.locator('html').getAttribute('data-theme'),'dark');
 await darkPage.getByRole('button',{name:'Все разделы',exact:true}).click();
 assert.equal(await darkPage.locator('.section-menu a').first().evaluate(e=>getComputedStyle(e).color),'rgb(241, 236, 223)');
 assert.equal(await darkPage.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
 await darkPage.screenshot({path:resolve(output,'mobile-menu-dark-fixture.png'),fullPage:true});await dark.close();check('Dark Telegram SDK fixture keeps the new welcome and section menu readable');
 assert.deepEqual(errors,[]);check('No browser runtime errors; synthetic diary cleaned up');
 await writeFile(resolve(output,'browser.json'),JSON.stringify({scope:'Isolated local PostgreSQL + real browser, synthetic account',checks,errors},null,2));
} catch(error) {await page.screenshot({path:resolve(output,'failure.png'),fullPage:true});throw error;} finally {await browser.close();}
