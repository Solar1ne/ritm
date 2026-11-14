import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createCoach } from '../static/coach.js';
import { resetSessionBinding } from '../static/api.js';

function harness(overrides={}) {
  let state = { profile: {}, revision: 2, sessionBinding: 'a', today: '2026-10-17', ai: { configured: true } }, dialog = '', writes = [];
  const coach = createCoach({getState:()=>state, dialogHeader:()=>'', openDialog:html=>{dialog=html;},closeDialog:()=>{},render:()=>{},toast:()=>{},navigate:()=>{},profileDialog:()=>{},onError:()=>{},isBusy:()=>false,mutate:async(...args)=>{writes.push(args);return true;},...overrides});
  return {coach, state:()=>state, set:value=>{state=value;},dialog:()=>dialog,writes};
}
const event = (id, values = {}) => ({preventDefault(){},target:{id,values,querySelector:()=>({textContent:''})}});
const context = {status:{configured:true},revision:2,contextToken:'signed',context:{today:'2026-10-17',facts:[{id:'f1',text:'Наблюдение'}],days:[{date:'2026-10-17',sessionId:''}]}};
const response = {mode:'ai',provider:'OpenAI',revision:2,facts:context.context.facts,result:{title:'Разбор',summary:'Новый ответ',insights:[{text:'Есть наблюдение',factIds:['f1']}],nextAction:'journal',draft:null}};

async function globals(fn) {
  const old={fetch:globalThis.fetch,FormData:globalThis.FormData,document:globalThis.document};
  globalThis.FormData=class{constructor(form){this.values=form.values;}get(key){return this.values[key]??null;}has(key){return Object.hasOwn(this.values,key);}};
  globalThis.document={querySelector:()=>null};resetSessionBinding();
  try {await fn();} finally {Object.assign(globalThis,old);resetSessionBinding();}
}
const ok = payload => ({ok:true,json:async()=>payload});

test('DeepSeek is named in status and consent with its own data policy', async()=>globals(async()=>{
  const h=harness();
  h.set({...h.state(),ai:{configured:true,provider:'DeepSeek',model:'deepseek-flash'}});
  globalThis.fetch=async()=>ok({...context,status:{configured:true,provider:'DeepSeek'}});
  assert.match(h.coach.view(),/AI · DeepSeek/);
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));
  assert.match(h.dialog(),/в DeepSeek для одного ответа/);
  assert.match(h.dialog(),/https:\/\/cdn\.deepseek\.com\/policies\/en-US\/deepseek-privacy-policy\.html/);
  assert.doesNotMatch(h.dialog(),/OpenAI/);
}));

test('unknown provider cannot enable consent or inject a policy URL',async()=>globals(async()=>{
  const h=harness();let calls=0;
  globalThis.fetch=async()=>{calls++;return ok({...context,status:{configured:true,provider:'<img src=x onerror=alert(1)>'}});};
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));
  assert.match(h.dialog(),/checkbox[^>]*disabled/);assert.doesNotMatch(h.dialog(),/<img src=x/);
  await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));
  assert.equal(calls,1);
}));

test('AI context preview does not call provider until separate checked consent', async()=>globals(async()=>{
  const h=harness(), calls=[];
  globalThis.fetch=async(path,options)=>{calls.push({path,options});return ok(path.includes('/context')?context:response);};
  await h.coach.handleSubmit(event('coach-form',{message:'Почему изменился план?'}));
  assert.equal(calls.length,1);assert.match(h.dialog(),/Разрешаю передать/);assert.doesNotMatch(h.dialog(),/type="checkbox"[^>]* checked/);
  await h.coach.handleSubmit(event('coach-consent-form',{}));assert.equal(calls.length,1);
  await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));assert.equal(calls.length,2);
  assert.deepEqual(JSON.parse(calls[1].options.body),{revision:2,task:'ask',message:'Почему изменился план?',consent:true,contextToken:'signed'});
  assert.match(h.coach.view(),/Новый ответ/);assert.equal(h.writes.length,0);
}));

test('late AI response is discarded after account reset; all model strings are escaped',async()=>globals(async()=>{
  const h=harness();let release;
  globalThis.fetch=async path=>path.includes('/context')?ok(context):new Promise(resolve=>{release=resolve;});
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));
  const pending=h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));
  h.coach.reset();h.set({...h.state(),sessionBinding:'b'});release(ok(response));await pending;
  assert.doesNotMatch(h.coach.view(),/Новый ответ/);
  globalThis.fetch=async path=>ok(path.includes('/context')?context:{...response,result:{...response.result,title:'<img src=x onerror=alert(1)>',summary:'<script>unsafe</script>'}});
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));
  assert.match(h.coach.view(),/&lt;img/);assert.doesNotMatch(h.coach.view(),/<script>unsafe/);
  h.set({...h.state(),revision:3});assert.doesNotMatch(h.coach.view(),/&lt;img/);
}));

test('AI draft is never saved automatically and confirmation retains reviewed revision',async()=>globals(async()=>{
  const h=harness();
  const draftResponse={...response,result:{...response.result,draft:{date:'2026-10-17',status:'wellbeing',sleep:7,fatigue:4}}};
  globalThis.fetch=async path=>ok(path.includes('/context')?context:draftResponse);
  await h.coach.handleClick('coach-task',{dataset:{task:'checkin'}});
  await h.coach.handleSubmit(event('coach-form',{message:'Спал семь часов, усталость четыре'}));await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));
  assert.equal(h.writes.length,0);await h.coach.handleClick('coach-draft',{});
  await h.coach.handleSubmit(event('coach-draft-form',{date:'2026-10-17',status:'wellbeing',sleep:'8',fatigue:'3'}));
  assert.equal(h.writes.length,1);assert.equal(h.writes[0][1].revision,2);assert.equal(h.writes[0][1].entry.sleep,8);
}));

test('a stale AI draft cannot write over a changed diary',async()=>globals(async()=>{
  const h=harness();globalThis.fetch=async path=>ok(path.includes('/context')?context:{...response,result:{...response.result,draft:{date:'2026-10-17',status:'wellbeing',sleep:7,fatigue:4}}});
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));await h.coach.handleClick('coach-draft',{});
  h.set({...h.state(),revision:3});await h.coach.handleSubmit(event('coach-draft-form',{date:'2026-10-17',status:'wellbeing',sleep:'8',fatigue:'3'}));assert.equal(h.writes.length,0);
}));

test('late context preview cannot replace another dialog or a different page',async()=>globals(async()=>{
  let resolve, active=true, dialogOpen=false, opened=0;
  const coach=createCoach({getState:()=>({profile:{},revision:2,sessionBinding:'a',ai:{configured:true}}),isActive:()=>active,dialogOpen:()=>dialogOpen,render:()=>{},openDialog:()=>opened++,dialogHeader:()=>'',onError:()=>{},isBusy:()=>false});
  globalThis.fetch=async()=>new Promise(done=>{resolve=done;});
  const pending=coach.handleSubmit(event('coach-form',{message:'Вопрос'}));active=false;resolve(ok(context));await pending;assert.equal(opened,0);
  active=true;const pending2=coach.handleSubmit(event('coach-form',{message:'Вопрос'}));dialogOpen=true;resolve(ok(context));await pending2;assert.equal(opened,0);
}));

test('a new date invalidates yesterday’s AI answer even without a revision change',async()=>globals(async()=>{
  const h=harness();globalThis.fetch=async path=>ok(path.includes('/context')?context:response);
  await h.coach.handleSubmit(event('coach-form',{message:'Что сегодня?'}));await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));assert.match(h.coach.view(),/Новый ответ/);
  h.set({...h.state(),today:'2026-10-18'});assert.doesNotMatch(h.coach.view(),/Новый ответ/);
}));

test('after a conflicted draft, retained edits can be reviewed with the reconciled diary without another AI call',async()=>globals(async()=>{
  let reviewed=null,calls=0,writes=0;
  const h=harness({mutate:async()=>{writes++;h.set({...h.state(),revision:3});return false;},reviewEntry:entry=>{reviewed=entry;}});
  globalThis.fetch=async path=>{calls++;return ok(path.includes('/context')?context:{...response,result:{...response.result,draft:{date:'2026-10-17',status:'wellbeing',sleep:7,fatigue:4}}});};
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));await h.coach.handleSubmit(event('coach-consent-form',{consent:'on'}));await h.coach.handleClick('coach-draft',{});
  const save=event('coach-draft-form',{date:'2026-10-17',status:'wellbeing',sleep:'8',fatigue:'3'});save.target.isConnected=true;
  await h.coach.handleSubmit(save);assert.equal(reviewed,null);assert.equal(writes,1);
  await h.coach.handleClick('coach-reconcile',{});assert.equal(reviewed.sleep,8);assert.equal(reviewed.fatigue,3);assert.equal(calls,2);assert.equal(writes,1);
}));

test('unconfigured AI offers working rules and day entry without a dead form', async()=>globals(async()=>{
  const h=harness(); h.set({...h.state(),ai:{configured:false}});
  const html=h.coach.view();
  assert.doesNotMatch(html,/textarea|id="coach-form"|data-action="coach-task"/);
  assert.match(html,/data-action="rules-helper"/); assert.match(html,/data-action="checkin"/);
  let calls=0; globalThis.fetch=async()=>{calls++;return ok(context);};
  await h.coach.handleSubmit(event('coach-form',{message:'Вопрос'}));
  assert.equal(calls,0); assert.equal(h.dialog(),'');
}));
