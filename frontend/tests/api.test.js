import { test } from 'node:test';
import assert from 'node:assert/strict';
import { request, resetSessionBinding, adoptSession, invalidatePendingRequests } from '../static/api.js';

test('only AI calls get time to outlast the bounded provider deadline',async()=>{
  const previous={fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout};
  const durations=[];
  globalThis.fetch=async()=>({ok:true,json:async()=>({})});
  globalThis.setTimeout=(_,ms)=>{durations.push(ms);return 1;};globalThis.clearTimeout=()=>{};
  try{await request('/api/state');await request('/api/ai/respond',{});assert.deepEqual(durations,[10000,45000]);}
  finally{Object.assign(globalThis,previous);resetSessionBinding();}
});

test('identity transitions invalidate older reads while keeping the binding for a guest claim', async () => {
  const original=globalThis.fetch;let release,seen;
  adoptSession({sessionBinding:'guest'});
  globalThis.fetch=async()=>new Promise(resolve=>{release=resolve;});
  const old=request('/api/state');invalidatePendingRequests();
  globalThis.fetch=async(_,options)=>{seen=options.headers['X-Rhythm-Session'];return {ok:true,json:async()=>({})};};
  await request('/api/auth/register',{revision:4});assert.equal(seen,'guest');
  adoptSession({sessionBinding:'account'});release({ok:true,json:async()=>({sessionBinding:'old'})});
  try {await assert.rejects(old,error=>error.code==='stale_session');await request('/api/state');assert.equal(seen,'account');}
  finally {globalThis.fetch=original;resetSessionBinding();}
});

test('a delayed response from a previous account cannot replace the current binding', async () => {
  const original = globalThis.fetch;
  let release;
  resetSessionBinding();
  globalThis.fetch = async () => new Promise(resolve => { release = resolve; });
  const old = request('/api/state');
  resetSessionBinding();
  globalThis.fetch = async () => ({ok:true,json:async()=>({sessionBinding:'new-account'})});
  await request('/api/state');
  release({ok:true,json:async()=>({sessionBinding:'old-account'})});
  await assert.rejects(old, error => error.code === 'stale_session');
  let sent;
  globalThis.fetch = async (_,options) => { sent = options.headers['X-Rhythm-Session']; return {ok:true,json:async()=>({})}; };
  try { await request('/api/profile',{revision:1}); assert.equal(sent,'new-account'); }
  finally { globalThis.fetch=original;resetSessionBinding(); }
});

test('a session reset during JSON decoding also discards the stale binding', async () => {
  const original=globalThis.fetch; let release, started;
  const decoding=new Promise(resolve=>{started=resolve;});
  resetSessionBinding();
  globalThis.fetch=async()=>({ok:true,json:()=>{started();return new Promise(resolve=>{release=resolve;});}});
  const old=request('/api/state');await decoding;resetSessionBinding();release({sessionBinding:'old'});
  try {await assert.rejects(old,error=>error.code==='stale_session');}
  finally {globalThis.fetch=original;resetSessionBinding();}
});

test('a failed calendar download preserves the structured authorization error', async () => {
  const original=globalThis.fetch;
  globalThis.fetch=async()=>({ok:false,status:409,json:async()=>({error:{code:'session_changed',message:'Сессия изменилась'}})});
  try {await assert.rejects(request('/api/calendar',undefined,'GET','blob'),error=>error.code==='session_changed'&&error.status===409);}
  finally {globalThis.fetch=original;resetSessionBinding();}
});

test('calendar content from a previous account is discarded while its body is loading', async () => {
  const original=globalThis.fetch;let release,started;
  const downloading=new Promise(resolve=>{started=resolve;});
  resetSessionBinding();
  globalThis.fetch=async()=>({ok:true,blob:()=>{started();return new Promise(resolve=>{release=resolve;});}});
  const old=request('/api/calendar',undefined,'GET','blob');await downloading;resetSessionBinding();release(new Blob(['old account']));
  try {await assert.rejects(old,error=>error.code==='stale_session');}
  finally {globalThis.fetch=original;resetSessionBinding();}
});
