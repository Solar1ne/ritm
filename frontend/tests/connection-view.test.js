import { test } from 'node:test';
import assert from 'node:assert/strict';
import { shell } from '../static/views.js';
const state={identity:'guest',accountsEnabled:true,entries:[],today:'2026-10-22'};
test('stale state notice shows last successful refresh and explicit recovery', () => {
  const html=shell(state,'overview','<p>Retained snapshot</p>',{stale:'Соединение потеряно',lastSuccess:new Date('2026-10-22T10:00:00Z')});
  assert.match(html,/Retained snapshot/); assert.match(html,/Последнее обновление:/);
  assert.match(html,/data-action="retry">Обновить данные/);
});
test('guest preservation CTA requires saved records and enabled accounts', () => {
  assert.doesNotMatch(shell(state,'overview',''),/Сохранить доступ/);
  assert.match(shell({...state,entries:[{id:'saved'}]},'overview',''),/data-action="account-register">Сохранить доступ/);
  assert.doesNotMatch(shell({...state,accountsEnabled:false,entries:[{id:'saved'}]},'overview',''),/Сохранить доступ/);
  assert.doesNotMatch(shell({...state,identity:'account',entries:[{id:'saved'}]},'overview',''),/Сохранить доступ/);
});
