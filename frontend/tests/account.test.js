import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createAccount } from '../static/account.js';
import { resetSessionBinding, adoptSession, request } from '../static/api.js';

const password = 'four quiet mornings together';
function harness() {
  let state={identity:'guest',profile:{name:'Synthetic'},revision:4,sessionBinding:'guest'},dialog='',busy=false,signIns=[],signOuts=0,errors=[];
  const account=createAccount({getState:()=>state,dialogHeader:()=>'',openDialog:value=>{dialog=value;},closeDialog(){},toast(){},dialogOpen:()=>true,isBusy:()=>busy,isReady:()=>true,setBusy:value=>{busy=value;},beforeAuth(){},onError:error=>{errors.push(error);},signedIn:value=>{signIns.push(value);state=value;adoptSession(value);},signedOut:async()=>{signOuts++;state={identity:'guest',profile:null,revision:0,sessionBinding:'new-guest'};adoptSession(state);},recovered(){}});
  return {account,state:()=>state,set:value=>{state=value;},dialog:()=>dialog,signIns,signOuts:()=>signOuts,errors};
}
function event(id,values={}) {
  const box={textContent:''};
  return {preventDefault(){},target:{id,values,isConnected:true,querySelector:()=>box,querySelectorAll:()=>[],setAttribute(){},removeAttribute(){}},box};
}
const ok = value => ({ok:true,json:async()=>value});
async function globals(fn) {
  const old={fetch:globalThis.fetch,FormData:globalThis.FormData};
  globalThis.FormData=class{constructor(form){this.values=form.values;}get(key){return this.values[key]??null;}has(key){return Object.hasOwn(this.values,key);}};
  resetSessionBinding();
  try {await fn();} finally {Object.assign(globalThis,old);resetSessionBinding();}
}

test('registration uses the reviewed guest revision and does not keep recovery secrets in application state',async()=>globals(async()=>{
  const h=harness(),calls=[];await h.account.handleClick('account-register',{});
  assert.match(h.dialog(),/Привязать мой текущий план/);assert.doesNotMatch(h.dialog(),/type="checkbox"[^>]*checked/);
  globalThis.fetch=async(path,options)=>{calls.push({path,body:JSON.parse(options.body)});return ok({identity:'account',revision:4,sessionBinding:'owner',recoveryCode:'SYNTHETIC-RECOVERY'});};
  await h.account.handleSubmit(event('account-register-form',{login:'Owner',password,confirmPassword:password,keepGuestData:'on'}));
  assert.deepEqual(calls[0].body,{login:'Owner',password,revision:4,keepGuestData:true});
  assert.equal(h.signIns.length,1);assert.equal(h.signIns[0].recoveryCode,undefined);assert.match(h.dialog(),/SYNTHETIC-RECOVERY/);
}));

test('password mismatch and a switched guest identity cannot submit credentials',async()=>globals(async()=>{
  const h=harness();let calls=0;globalThis.fetch=async()=>{calls++;return ok({});};
  await h.account.handleClick('account-register',{});
  const mismatch=event('account-register-form',{password,confirmPassword:'different'});
  await h.account.handleSubmit(mismatch);assert.match(mismatch.box.textContent,/не совпадают/);
  h.set({...h.state(),sessionBinding:'another'});
  const stale=event('account-register-form',{password,confirmPassword:password});
  await h.account.handleSubmit(stale);assert.match(stale.box.textContent,/Сессия изменилась/);assert.equal(calls,0);
}));

test('late account details cannot replace a different dialog',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account'});let release;
  globalThis.fetch=async()=>new Promise(resolve=>{release=resolve;});
  const pending=h.account.handleClick('account-manage',{});
  await h.account.handleClick('settings',{});
  release(ok({login:'old',sessions:[]}));await pending;
  assert.doesNotMatch(h.dialog(),/Вы вошли как old|История привязана/);
}));

test('reset during login ignores the delayed identity response',async()=>globals(async()=>{
  const h=harness();let release;
  globalThis.fetch=async()=>new Promise(resolve=>{release=resolve;});
  await h.account.handleClick('account-login',{});
  const pending=h.account.handleSubmit(event('account-login-form',{login:'owner',password}));
  h.account.reset();resetSessionBinding();release(ok({identity:'account',sessionBinding:'late'}));await pending;
  assert.equal(h.signIns.length,0);
}));

test('revoked sessions block private state while incorrect credentials stay a form error',async()=>globals(async()=>{
  globalThis.fetch=async()=>({ok:false,status:401,json:async()=>({error:{code:'session',message:'Session expired'}})});
  await assert.rejects(request('/api/state'),error=>error.code==='session_changed');
  globalThis.fetch=async()=>({ok:false,status:401,json:async()=>({error:{code:'credentials',message:'Wrong credentials'}})});
  await assert.rejects(request('/api/auth/login',{login:'owner',password}),error=>error.code==='credentials');
}));

test('account management separates permanent deletion from clearing the diary',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account'});
  globalThis.fetch=async()=>ok({login:'owner',sessions:[]});
  await h.account.handleClick('account-manage',{});
  assert.match(h.dialog(),/Удаление дневника в настройках сохраняет аккаунт/);
  assert.match(h.dialog(),/data-action="account-delete"/);
  await h.account.handleClick('account-delete',{});
  assert.match(h.dialog(),/все сеансы на других устройствах завершатся/);
  assert.match(h.dialog(),/type="password"/);
  assert.match(h.dialog(),/name="confirmDelete" required/);
  assert.doesNotMatch(h.dialog(),/type="checkbox"[^>]*checked/);
}));

test('permanent deletion needs consent, password and the originally reviewed session',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account'});let calls=0;
  globalThis.fetch=async()=>{calls++;return ok({accountDeleted:true});};
  await h.account.handleClick('account-delete',{});
  const noConsent=event('account-delete-form',{password});await h.account.handleSubmit(noConsent);
  assert.match(noConsent.box.textContent,/Подтвердите/);
  const noPassword=event('account-delete-form',{confirmDelete:'on'});await h.account.handleSubmit(noPassword);
  assert.match(noPassword.box.textContent,/текущий пароль/);
  h.set({...h.state(),sessionBinding:'different-account'});
  const switched=event('account-delete-form',{password,confirmDelete:'on'});await h.account.handleSubmit(switched);
  assert.match(switched.box.textContent,/Сессия изменилась/);assert.equal(calls,0);
}));

test('permanent deletion sends the reviewed revision once and boots a guest without applying the deletion result',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account',sessionBinding:'owner'});adoptSession(h.state());
  await h.account.handleClick('account-delete',{});h.set({...h.state(),revision:5});
  let release;const calls=[];
  globalThis.fetch=async(path,options)=>{calls.push({path,...options});return new Promise(resolve=>{release=resolve;});};
  const pending=h.account.handleSubmit(event('account-delete-form',{password,confirmDelete:'on'}));
  await h.account.handleSubmit(event('account-delete-form',{password,confirmDelete:'on'}));
  assert.equal(calls.length,1);assert.equal(calls[0].path,'/api/account');assert.equal(calls[0].method,'DELETE');
  assert.equal(calls[0].headers['X-Rhythm-Session'],'owner');assert.deepEqual(JSON.parse(calls[0].body),{password,revision:4,confirmDelete:true});
  release(ok({accountDeleted:true}));await pending;
  assert.equal(h.signOuts(),1);assert.equal(h.signIns.length,0);assert.equal(h.state().profile,null);assert.equal(h.state().identity,'guest');
}));

test('a deletion conflict refreshes the review and requires a fresh explicit submission',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account',entries:[]});await h.account.handleClick('account-delete',{});
  const calls=[];let conflict=true;
  globalThis.fetch=async(path,options)=>{
    calls.push({path,...options});
    if(path==='/api/state')return ok({...h.state(),revision:9,entries:[{id:'new-entry'}]});
    if(conflict)return {ok:false,status:409,json:async()=>({error:{code:'revision_conflict',message:'Changed'}})};
    return ok({accountDeleted:true});
  };
  await h.account.handleSubmit(event('account-delete-form',{password,confirmDelete:'on'}));
  assert.equal(calls.length,2);assert.equal(calls[1].path,'/api/state');assert.equal(h.signOuts(),0);assert.equal(h.state().revision,9);
  assert.match(h.dialog(),/Дневник изменился/);assert.match(h.dialog(),/отметок — 1/);assert.doesNotMatch(h.dialog(),/type="checkbox"[^>]*checked/);
  const noConsent=event('account-delete-form',{password});await h.account.handleSubmit(noConsent);assert.equal(calls.length,2);
  conflict=false;await h.account.handleSubmit(event('account-delete-form',{password,confirmDelete:'on'}));
  assert.equal(JSON.parse(calls[2].body).revision,9);assert.equal(h.signOuts(),1);
}));

test('incorrect deletion password remains a form error and a revoked session blocks private state',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account'});await h.account.handleClick('account-delete',{});
  globalThis.fetch=async()=>({ok:false,status:401,json:async()=>({error:{code:'credentials',message:'Wrong password'}})});
  const wrong=event('account-delete-form',{password,confirmDelete:'on'});await h.account.handleSubmit(wrong);
  assert.equal(wrong.box.textContent,'Wrong password');assert.equal(h.errors.length,0);assert.equal(h.signOuts(),0);
  globalThis.fetch=async()=>({ok:false,status:401,json:async()=>({error:{code:'session',message:'Expired'}})});
  await h.account.handleSubmit(event('account-delete-form',{password,confirmDelete:'on'}));
  assert.equal(h.errors[0].code,'session_changed');assert.equal(h.signOuts(),0);
}));

test('a deletion reply from a reset session cannot clear the newly active identity',async()=>globals(async()=>{
  const h=harness();h.set({...h.state(),identity:'account'});await h.account.handleClick('account-delete',{});let release;
  globalThis.fetch=async()=>new Promise(resolve=>{release=resolve;});
  const pending=h.account.handleSubmit(event('account-delete-form',{password,confirmDelete:'on'}));
  h.account.reset();resetSessionBinding();h.set({...h.state(),sessionBinding:'other'});
  release(ok({accountDeleted:true}));await pending;assert.equal(h.signOuts(),0);assert.equal(h.state().sessionBinding,'other');
}));
