import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { installView, initializeInstall, promptInstall } from '../static/install.js';

test('installation instructions expose both phone paths and online requirement', () => {
  assert.match(installView(),/Android · Chrome/); assert.match(installView(),/iPhone · Safari/);
  assert.match(installView(),/нужно подключение/); assert.doesNotMatch(installView(),/data-action="install-prompt"/);
});
test('native install prompt is optional and consumed once', async () => {
  const handlers={}; initializeInstall(()=>{}, {addEventListener:(name,fn)=>{handlers[name]=fn;}});
  let calls=0, prevented=false;
  handlers.beforeinstallprompt({preventDefault(){prevented=true;},prompt:async()=>{calls++;},userChoice:Promise.resolve({outcome:'dismissed'})});
  assert.equal(prevented,true); assert.match(installView(),/data-action="install-prompt"/);
  assert.equal(await promptInstall(),true); assert.equal(await promptInstall(),false); assert.equal(calls,1);
});
test('worker does not intercept APIs or cache live documents; failed navigation returns public offline page', async () => {
  const handlers={},writes=[],reads=[];
  const context={URL,self:{location:{origin:'https://rhythm.test'},addEventListener:(name,fn)=>{handlers[name]=fn;}},caches:{open:async()=>({addAll:async assets=>writes.push(...assets)}),match:async path=>{reads.push(path);return 'offline';}},fetch:async()=>{throw Error('offline');}};
  vm.runInNewContext(await readFile(new URL('../static/service-worker.js',import.meta.url),'utf8'),context);
  let install; handlers.install({waitUntil:value=>{install=value;}}); await install;
  assert.deepEqual(writes,['/offline.html','/app-icon.svg']);
  for(const path of ['/api/state','/api/export','/private.json','/other-document']) {
    let intercepted=false; handlers.fetch({request:{url:`https://rhythm.test${path}`,method:'GET',mode:'navigate'},respondWith:()=>{intercepted=true;}}); assert.equal(intercepted,false,path);
  }
  let result; handlers.fetch({request:{url:'https://rhythm.test/',method:'GET',mode:'navigate'},respondWith:value=>{result=value;}});
  assert.equal(await result,'offline'); assert.deepEqual(reads,['/offline.html']);
});
