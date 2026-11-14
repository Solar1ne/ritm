// Run only against an isolated local data directory; creates synthetic accounts.
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
const require=createRequire(import.meta.url);
const { chromium }=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base=process.env.RHYTHM_URL || 'http://127.0.0.1:18081';
assert.ok(['127.0.0.1','localhost'].includes(new URL(base).hostname) && new URL(base).port==='18081','Use the isolated account QA server on port 18081');
const output=resolve(process.env.RHYTHM_EVIDENCE_DIR || 'docs/evidence/accounts');
await mkdir(output,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const contexts=[],checks=[],errors=[],fixtures=[];
const name=`qa${Date.now().toString(36)}`,password='four quiet mornings together',newPassword='another gentle week begins',finalPassword='a steady rhythm through autumn';
const check=label=>{checks.push({name:label,passed:true});console.log(`PASS ${label}`);};
async function client(width=1440){const ctx=await browser.newContext({viewport:{width,height:1000},reducedMotion:'reduce'});contexts.push(ctx);const page=await ctx.newPage();page.on('pageerror',e=>errors.push(e.message));await page.goto(base,{waitUntil:'networkidle'});return {ctx,page};}
async function snapshot(ctx){const r=await ctx.request.get(`${base}/api/state`);assert.equal(r.status(),200);return r.json();}
async function login(page,secret){await page.locator('.top-actions [data-action=account-login]').click();await page.getByLabel('Логин',{exact:true}).fill(name);await page.getByLabel('Пароль',{exact:true}).fill(secret);await page.locator('#account-login-form').getByRole('button',{name:'Войти',exact:true}).click();}
async function settings(page){await page.getByRole('button',{name:'Настройки и данные',exact:true}).click();await page.locator('[data-action=account-manage]').click();await page.getByRole('heading',{name:'Мой аккаунт',exact:true}).waitFor();await page.locator('.account-summary').waitFor();}
async function saveCode(page){const code=await page.locator('.recovery-card code').innerText();assert.equal(code.length,39);await page.locator('#account-code-saved-form input').check();await page.locator('#account-code-saved-form button[type=submit]').click();return code;}
async function noOverflow(page){assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.equal(await page.locator('dialog').evaluate(d=>d.scrollWidth>d.clientWidth+1),false);}
async function deleteFixture(fixture){
  const current=await fixture.ctx.request.get(`${base}/api/account`);
  if(current.status()!==200 || (await current.json()).login!==fixture.login){
    const loginResponse=await fixture.ctx.request.post(`${base}/api/auth/login`,{data:{login:fixture.login,password:fixture.password}});
    assert.equal(loginResponse.status(),200,'Synthetic account must be accessible for API cleanup');
  }
  const value=await snapshot(fixture.ctx);
  const deleted=await fixture.ctx.request.delete(`${base}/api/account`,{headers:{'X-Rhythm-Session':value.sessionBinding},data:{password:fixture.password,revision:value.revision,confirmDelete:true}});
  assert.equal(deleted.status(),200,'Synthetic account cleanup must succeed');assert.deepEqual(await deleted.json(),{accountDeleted:true});fixture.deleted=true;
}
let a,b,primaryFixture,runError;
try {
  a=await client();
  await a.page.getByRole('button',{name:'Создать мой план',exact:true}).click();
  await a.page.getByLabel('Как к вам обращаться?').fill('UI fixture Аккаунт');
  await a.page.locator('#profile-form button[type=submit]').click();await a.page.locator('dialog').waitFor({state:'hidden'});
  await a.page.getByRole('button',{name:'Устал',exact:true}).click();await a.page.getByLabel('Сколько часов вы спали?').fill('5');await a.page.getByRole('button',{name:'Сохранить отметку',exact:false}).click();await a.page.locator('dialog').waitFor({state:'hidden'});
  const before=await snapshot(a.ctx),legacy=await a.ctx.cookies();assert.equal(before.entries.length,1);
  await a.page.locator('.top-actions [data-action=account-login]').click();await a.page.locator('dialog [data-action=account-register]').click();
  await a.page.getByLabel('Логин',{exact:true}).fill(name);await a.page.getByLabel('Пароль',{exact:true}).fill(password);await a.page.getByLabel('Повторите пароль',{exact:true}).fill('this is a different password');await a.page.locator('[name=keepGuestData]').check();
  await a.page.locator('#account-register-form button[type=submit]').click();await a.page.getByText('Пароли не совпадают. Проверьте повтор.',{exact:true}).waitFor();
  check('Password mismatch remains in the form; guest history is unchanged');
  await a.page.getByLabel('Повторите пароль',{exact:true}).fill(password);
  await noOverflow(a.page);await a.page.screenshot({path:resolve(output,'registration-desktop.png'),fullPage:true});
  for(const width of [320,390]){await a.page.setViewportSize({width,height:900});await noOverflow(a.page);assert.ok(await a.page.locator('.password-toggle').first().evaluate(e=>e.getBoundingClientRect().height)>=44);}
  await a.page.screenshot({path:resolve(output,'registration-mobile.png'),fullPage:true});
  check('Registration at 320/390/1440px: no overflow, labelled inputs and 44px password controls');
  await a.page.setViewportSize({width:1440,height:1000});await a.page.locator('#account-register-form button[type=submit]').click();await a.page.getByRole('heading',{name:'Сохраните резервный код'}).waitFor();
  primaryFixture={ctx:a.ctx,login:name,password,deleted:false};fixtures.push(primaryFixture);
  const code=await a.page.locator('.recovery-card code').innerText();
  const downloadPromise=a.page.waitForEvent('download');await a.page.locator('[data-action=account-code-download]').click();const download=await downloadPromise;const downloaded=await readFile(await download.path(),'utf8');assert.ok(downloaded.includes(code)&&downloaded.includes(name));
  await saveCode(a.page);await a.page.locator('dialog').waitFor({state:'hidden'});
  const registered=await snapshot(a.ctx);assert.equal(registered.identity,'account');assert.deepEqual(registered.entries,before.entries);assert.equal(registered.profile.name,before.profile.name);assert.notEqual(registered.sessionBinding,before.sessionBinding);
  check('Registration claims the real guest diary and provides a downloadable recovery code');
  const stale=await browser.newContext();contexts.push(stale);await stale.addCookies(legacy);assert.equal((await stale.request.get(`${base}/api/state`)).status(),401);
  check('The previous guest cookie cannot access the claimed diary');
  b=await client(390);await login(b.page,'incorrect password phrase');await b.page.locator('#account-login-form .form-error').filter({hasText:'Не удалось подтвердить вход'}).waitFor();await b.page.getByLabel('Пароль',{exact:true}).fill(password);await b.page.locator('#account-login-form button[type=submit]').click();await b.page.locator('dialog').waitFor({state:'hidden'});
  primaryFixture.ctx=b.ctx;assert.deepEqual((await snapshot(b.ctx)).entries,before.entries);check('A separate browser signs in to the same saved diary; wrong password is rejected');
  await settings(a.page);await a.page.getByText('Активные сеансы · 2',{exact:true}).waitFor();await a.page.screenshot({path:resolve(output,'account-desktop.png'),fullPage:true});
  await a.page.locator('[data-action=account-logout-others]').click();await a.page.locator('#account-logout-others-form button[type=submit]').click();await a.page.locator('dialog').waitFor({state:'hidden'});
  assert.equal((await b.ctx.request.get(`${base}/api/state`)).status(),401);await snapshot(a.ctx);
  await b.page.reload({waitUntil:'networkidle'});assert.equal(await b.page.getByText('UI fixture Аккаунт',{exact:true}).count(),0);
  check('Ending other sessions revokes server access and clears stale private UI on reload');
  await b.page.locator('.top-actions [data-action=account-login]').click();await b.page.locator('[data-action=account-recover]').click();await b.page.getByLabel('Логин',{exact:true}).fill(name);await b.page.getByLabel('Резервный код',{exact:true}).fill(code);await b.page.getByLabel('Новый пароль',{exact:true}).fill(newPassword);await b.page.getByLabel('Повторите пароль',{exact:true}).fill(newPassword);
  await noOverflow(b.page);await b.page.locator('#account-recover-form button[type=submit]').click();await b.page.getByRole('heading',{name:'Сохраните резервный код'}).waitFor();primaryFixture.password=newPassword;const replacement=await saveCode(b.page);assert.notEqual(replacement,code);await b.page.getByLabel('Пароль',{exact:true}).fill(newPassword);await b.page.locator('#account-login-form button[type=submit]').click();await b.page.locator('dialog').waitFor({state:'hidden'});
  assert.equal((await a.ctx.request.get(`${base}/api/state`)).status(),401);assert.deepEqual((await snapshot(b.ctx)).entries,before.entries);
  check('Recovery rotates the code, revokes old sessions and preserves the diary');
  const reused=await b.ctx.request.post(`${base}/api/auth/recover`,{data:{login:name,recoveryCode:code,newPassword:password}});assert.equal(reused.status(),401);
  check('A consumed recovery code cannot be reused');
  await a.page.reload({waitUntil:'networkidle'});await login(a.page,newPassword);await a.page.locator('dialog').waitFor({state:'hidden'});
  await settings(b.page);await b.page.setViewportSize({width:320,height:900});await noOverflow(b.page);await b.page.screenshot({path:resolve(output,'account-mobile.png'),fullPage:true});
  await b.page.locator('[data-action=account-password]').click();await b.page.locator('#account-password-form').getByLabel('Текущий пароль',{exact:true}).fill(newPassword);await b.page.locator('#account-password-form').getByLabel('Новый пароль',{exact:true}).fill(finalPassword);await b.page.locator('#account-password-form').getByLabel('Повторите пароль',{exact:true}).fill(finalPassword);await b.page.locator('#account-password-form button[type=submit]').click();await b.page.getByRole('heading',{name:'Сохраните резервный код'}).waitFor();primaryFixture.password=finalPassword;await saveCode(b.page);await b.page.locator('dialog').waitFor({state:'hidden'});
  assert.equal((await a.ctx.request.get(`${base}/api/state`)).status(),401);await snapshot(b.ctx);check('Changing the password rotates the current session and revokes every old session');
  await b.page.evaluate(()=>{location.hash='nutrition';});await b.page.locator('.recipe-card').first().waitFor();
  const activeCookies=await b.ctx.cookies();await settings(b.page);await b.page.locator('[data-action=account-logout]').click();await b.page.locator('#account-logout-form button[type=submit]').click();await b.page.locator('dialog').waitFor({state:'hidden'});
  const guest=await snapshot(b.ctx);assert.equal(guest.identity,'guest');assert.equal(guest.profile,null);
  const replay=await browser.newContext();contexts.push(replay);await replay.addCookies(activeCookies);assert.equal((await replay.request.get(`${base}/api/state`)).status(),401);
  check('Logout revokes the saved bearer token and opens an empty guest space');
  await b.page.locator('.recipe-card').first().waitFor();
  await login(b.page,finalPassword);await b.page.locator('dialog').waitFor({state:'hidden'});assert.deepEqual((await snapshot(b.ctx)).entries,before.entries);check('Signing back in after logout restores the complete diary');
  await b.page.locator('.recipe-card').first().waitFor();assert.ok(b.page.url().endsWith('#nutrition'));check('Nutrition reloads after logout and login without leaving the current screen');
  const other=await client(),otherName=`${name}other`,otherGuest=await snapshot(other.ctx);
  const otherRegistration=await other.ctx.request.post(`${base}/api/auth/register`,{headers:{'X-Rhythm-Session':otherGuest.sessionBinding},data:{login:otherName,password,revision:otherGuest.revision,keepGuestData:true}});
  assert.equal(otherRegistration.status(),200);const otherFixture={ctx:other.ctx,login:otherName,password,deleted:false};fixtures.push(otherFixture);
  const otherBefore=await snapshot(other.ctx);
  const secondSession=await browser.newContext();contexts.push(secondSession);
  assert.equal((await secondSession.request.post(`${base}/api/auth/login`,{data:{login:name,password:finalPassword}})).status(),200);
  const deletedReplay=await browser.newContext();contexts.push(deletedReplay);await deletedReplay.addCookies(await b.ctx.cookies());
  await settings(b.page);await b.page.locator('[data-action=account-delete]').click();
  assert.equal(await b.page.locator('[name=confirmDelete]').isChecked(),false);
  await b.page.locator('#account-delete-form button[type=submit]').click();assert.equal((await snapshot(b.ctx)).identity,'account');
  for(const width of [320,390,1440]){await b.page.setViewportSize({width,height:1000});await noOverflow(b.page);}
  await b.page.screenshot({path:resolve(output,'account-delete-desktop.png'),fullPage:true});
  await b.page.setViewportSize({width:390,height:900});await b.page.screenshot({path:resolve(output,'account-delete-mobile.png'),fullPage:true});
  await b.page.locator('#account-delete-form').getByLabel('Текущий пароль',{exact:true}).fill('incorrect password phrase');await b.page.locator('[name=confirmDelete]').check();
  await b.page.locator('#account-delete-form button[type=submit]').click();await b.page.locator('#account-delete-form .form-error').filter({hasText:'Не удалось подтвердить вход'}).waitFor();
  assert.equal((await snapshot(b.ctx)).identity,'account');check('Permanent deletion requires explicit consent and the current password; 320/390/1440px review fits');
  await b.page.locator('#account-delete-form').getByLabel('Текущий пароль',{exact:true}).fill(finalPassword);await b.page.locator('#account-delete-form button[type=submit]').click();await b.page.locator('dialog').waitFor({state:'hidden'});primaryFixture.deleted=true;
  const afterDelete=await snapshot(b.ctx);assert.equal(afterDelete.identity,'guest');assert.equal(afterDelete.profile,null);assert.deepEqual(afterDelete.entries,[]);assert.deepEqual(afterDelete.workouts,[]);
  assert.equal(await b.page.getByText('UI fixture Аккаунт',{exact:true}).count(),0);
  assert.equal((await deletedReplay.request.get(`${base}/api/state`)).status(),401);assert.equal((await secondSession.request.get(`${base}/api/state`)).status(),401);
  assert.equal((await deletedReplay.request.post(`${base}/api/auth/login`,{data:{login:name,password:finalPassword}})).status(),401);
  check('Permanent deletion removes credentials and the diary, revokes all account sessions and boots an empty guest');
  assert.deepEqual(await snapshot(other.ctx),otherBefore);const otherInfo=await other.ctx.request.get(`${base}/api/account`,{headers:{'X-Rhythm-Session':otherBefore.sessionBinding}});assert.equal(otherInfo.status(),200);assert.equal((await otherInfo.json()).login,otherName);
  check('Deleting one account preserves a separate account and its active session');
  await deleteFixture(otherFixture);check('All disposable accounts are removed through the public API without database access');
  assert.deepEqual(errors,[]);
  await writeFile(resolve(output,'browser.json'),JSON.stringify({generatedAt:new Date().toISOString(),scope:'Isolated local Go server; synthetic credentials and diary; no external identity provider',checks,pageErrors:errors},null,2)+'\n');
} catch(error) {
  runError=error;
  await a?.page.screenshot({path:resolve(output,'failure.png'),fullPage:true}).catch(()=>{});
  await writeFile(resolve(output,'browser-failure.json'),JSON.stringify({checks,pageErrors:errors,error:String(error)},null,2)+'\n');throw error;
} finally {
  const cleanupErrors=[];
  for(const fixture of fixtures.filter(value=>!value.deleted)){
    try{await deleteFixture(fixture);}catch(error){cleanupErrors.push(String(error));}
  }
  for(const ctx of contexts)await ctx.close();await browser.close();
  if(cleanupErrors.length){await writeFile(resolve(output,'cleanup-failure.json'),JSON.stringify({errors:cleanupErrors},null,2)+'\n');if(!runError)throw new Error('Synthetic account cleanup failed; see cleanup-failure.json');}
}
