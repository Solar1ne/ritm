import { request } from './api.js';
import { escapeHTML as esc, dateLabel } from './format.js';
import { icon } from './icons.js';

const tasks = {
  ask: { title: 'Разобраться в плане', description: 'Свяжем ваш вопрос с планом и дневником.', placeholder: 'Что в моих отметках повлияло на следующую неделю?' },
  review: { title: 'Увидеть свою неделю', description: 'Что получилось, каких данных не хватает и с чего продолжить.', placeholder: 'Можно добавить контекст: например, на этой неделе много работы.' },
  checkin: { title: 'Записать одной фразой', description: 'Расскажите, как вы. Сначала покажем черновик.', placeholder: 'Сегодня спал 7,5 часов, усталость 4 из 10. Занятие выполнил.' },
};
const providerPolicies = {
  OpenAI: 'https://developers.openai.com/api/docs/guides/your-data',
  DeepSeek: 'https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html',
};
const formError = '<div id="form-error" class="form-error" role="alert"></div>';

// AI responses live only in this view. Consent is per request, not a sticky
// account preference. Revision and generation guard every async boundary.
export function createCoach(ctx) {
  const state = ctx.getState;
  let task = 'ask', message = '', reply = null, preview = null, error = '', busy = false, generation = 0, draftCommand = null, recovery = null;
  function reset() { generation++; task = 'ask'; message = ''; reply = preview = draftCommand = recovery = null; error = ''; busy = false; }
  function currentReply() {
    if (reply && (reply.revision !== state().revision || reply.binding !== state().sessionBinding || reply.contextToday !== state().today)) reply = null;
    return reply;
  }
  function view() {
    const response = currentReply(), status = state().ai || {}, selected = tasks[task];
    if (!status.configured) return `<div class="page-heading"><div><p class="eyebrow">РИТМ · ПОМОЩНИК</p><h1>Разобраться в своём ритме</h1><p class="subtitle">Ответы по правилам уже работают с вашим планом и дневником.</p></div></div><section class="panel"><h2>AI пока не подключён</h2><p>Можно задать вопрос помощнику по правилам ниже или самостоятельно записать день. Передача данных AI сейчас недоступна.</p><div class="dialog-actions"><button class="button outline" data-action="rules-helper">К ответам по правилам</button><button class="button primary" data-action="checkin">Записать день</button></div></section>`;
    return `<div class="page-heading"><div><p class="eyebrow">РИТМ · ПЕРСОНАЛЬНЫЙ ПОМОЩНИК</p><h1>Больше понимания. Меньше рутины.</h1><p class="subtitle">Ваши наблюдения, понятные объяснения и следующий небольшой шаг.</p></div></div>
      <section class="coach-hero panel"><div><span class="pill ${status.configured ? 'green' : 'neutral'}">${icon('spark')} ${status.configured ? `AI · ${esc(status.provider || 'OpenAI')}` : 'AI · подключение впереди'}</span><h2>От «как я себя чувствую»<br>к понятному действию</h2><p>Помощник видит только те данные, которые вы подтвердите. Изменения в дневнике всегда остаются вашим решением.</p></div><div class="coach-orbit" aria-hidden="true"><span>вы</span><i>контекст</i><b>маленький шаг</b></div></section>
      <div class="coach-tasks" role="group" aria-label="Задача AI-помощника">${Object.entries(tasks).map(([key, item], index) => `<button class="coach-task ${key === task ? 'selected' : ''}" data-action="coach-task" data-task="${key}" aria-pressed="${key === task}" ${busy ? 'disabled' : ''}><span class="coach-number">0${index + 1}</span><strong>${item.title}</strong><span>${item.description}</span></button>`).join('')}</div>
      ${!status.configured ? '<div class="notice"><span>AI-сервис ещё не подключён владельцем Ритма. Помощник по правилам ниже уже работает с вашим планом.</span></div>' : ''}
      <form id="coach-form" class="panel coach-form" aria-busy="${busy}"><label class="field"><span class="field-label">${task === 'checkin' ? 'Как прошёл ваш день?' : task === 'review' ? 'Что важно учесть? Необязательно' : 'Что хотите понять?'}</span><textarea name="message" rows="3" maxlength="2000" ${task === 'review' ? '' : 'required'} placeholder="${esc(selected.placeholder)}" ${busy ? 'disabled' : ''}>${esc(message)}</textarea></label><div class="coach-form-footer"><p class="small-print">Перед отправкой покажем контекст. Имя и Telegram ID в него не входят.</p><button class="button primary" type="submit" ${busy ? 'disabled' : ''}>${busy ? 'Готовим ответ…' : 'Проверить и отправить'} ${icon('arrow')}</button></div></form>
      ${error ? `<div class="notice error" role="alert"><span>${esc(error)}</span></div>` : ''}
      ${response ? answer(response) : ''}`;
  }
  function answer(response) {
    const r = response.result, facts = new Map(response.facts.map(f => [f.id, f.text]));
    const link = { journal: ['journal', 'Открыть дневник'], plan: ['week', 'Посмотреть план'], activities: ['activities', 'Моё движение'], wellness: ['wellness', 'Посмотреть измерения'], nutrition: ['nutrition', 'Открыть меню'] }[r.nextAction];
    return `<section class="panel coach-answer" aria-live="polite"><div class="section-title"><span class="pill green">AI · ${esc(response.provider)}</span><button class="button ghost" data-action="coach-clear">Убрать ответ</button></div><h2>${esc(r.title)}</h2><p class="answer-text">${esc(r.summary)}</p>${r.insights.map(insight => `<div class="coach-insight"><p>${esc(insight.text)}</p><details><summary>На какие данные опирается</summary><ul class="evidence-list">${insight.factIds.map(id => `<li>${esc(facts.get(id) || 'Источник недоступен')}</li>`).join('')}</ul></details></div>`).join('')}${r.draft ? `<div class="coach-draft"><h3>Черновик · ещё не сохранён</h3><p>${dateLabel(r.draft.date)} · ${r.draft.status === 'done' ? 'занятие выполнено' : r.draft.status === 'missed' ? 'занятие пропущено' : 'самочувствие'}${r.draft.sleep == null ? '' : ` · сон ${esc(r.draft.sleep)} ч`}${r.draft.fatigue == null ? '' : ` · усталость ${esc(r.draft.fatigue)}/10`}</p><button class="button green" data-action="coach-draft">Проверить запись ${icon('arrow')}</button></div>` : link ? `<a class="button outline" href="#${link[0]}">${link[1]} ${icon('arrow')}</a>` : ''}<p class="small-print">Ответ модели может содержать ошибки. Ссылки показывают исходные данные, но не доказывают выводы. План рассчитывается правилами Ритма.</p></section>`;
  }
  function showConsent() {
    const facts = preview.context.facts, provider = preview.status.provider || 'OpenAI';
    const policy = Object.hasOwn(providerPolicies, provider) ? providerPolicies[provider] : null;
    const configured = preview.status.configured && Boolean(policy);
    ctx.openDialog(`${ctx.dialogHeader('Что увидит AI', 'Проверьте сведения для этого запроса.')}<div class="ai-consent-preview"><p class="eyebrow">${esc(tasks[task].title)} · ${esc(provider)}</p><p class="answer-text">${esc(message || 'Разобрать последние 14 дней и предложить один небольшой следующий шаг.')}</p><details><summary>Контекст: ${facts.length} фактов и ${preview.context.days.length} допустимых дат</summary><ul class="evidence-list">${facts.map(f => `<li>${esc(f.text)}</li>`).join('')}</ul><p class="small-print">${preview.context.days.map(d => `${esc(d.date)}${d.sessionId ? ' · есть занятие' : ''}`).join('; ')}</p></details></div><form id="coach-consent-form"><label class="consent-row"><input name="consent" type="checkbox" required ${configured ? '' : 'disabled'}><span>Разрешаю передать этот текст и показанный контекст, включая сведения о самочувствии, в ${esc(provider)} для одного ответа.</span></label><p class="small-print">Ответ не сохраняется в истории Ритма. Отправленные данные обрабатываются внешним провайдером; <a href="${policy || '#'}" target="_blank" rel="noopener noreferrer">условия обработки данных</a>. Не добавляйте в текст лишние личные сведения.</p>${configured ? '' : '<p class="notice">AI-сервис пока не подключён. Ваши данные не отправляются.</p>'}${formError}<div class="dialog-actions"><button class="button outline" type="button" data-action="close">Отмена</button><button class="button primary" type="submit" ${configured ? '' : 'disabled'}>Отправить в AI</button></div></form>`);
  }
  async function prepare() {
    if (!state().ai?.configured) return;
    if (!state().profile) { ctx.profileDialog(); return; }
    if (busy) return;
    const id = ++generation, binding = state().sessionBinding, revision = state().revision;
    busy = true; error = ''; reply = preview = null; ctx.render();
    const ownerForm = document.querySelector('#coach-form');
    try {
      const value = await request(`/api/ai/context?task=${task}`);
      if (id !== generation || binding !== state().sessionBinding) return;
      if (ctx.isActive?.() === false || ctx.dialogOpen?.() || (ownerForm && !ownerForm.isConnected)) return;
      if (value.revision !== revision || revision !== state().revision) throw new Error('Дневник изменился. Обновите страницу и проверьте контекст снова.');
      preview = value; showConsent();
    } catch (e) { if (id === generation) { error = e.message; ctx.onError(e); } }
    finally { if (id === generation) { busy = false; ctx.render(); } }
  }
  async function send(form) {
    if (busy || !preview || !preview.status.configured || !Object.hasOwn(providerPolicies, preview.status.provider || 'OpenAI') || !new FormData(form).has('consent')) return;
    if (preview.revision !== state().revision || preview.context.today !== state().today) throw new Error('Дневник или дата изменились. Закройте окно и проверьте данные снова.');
    const id = ++generation, binding = state().sessionBinding, revision = preview.revision, contextToday = preview.context.today;
    const payload = { revision, task, message, consent: true, contextToken: preview.contextToken };
    busy = true; error = ''; ctx.closeDialog(); ctx.render();
    try {
      const value = await request('/api/ai/respond', payload);
      if (id !== generation || binding !== state().sessionBinding) return;
      if (revision !== state().revision || value.revision !== revision || contextToday !== state().today) throw new Error('Дневник изменился во время ответа. Отправьте запрос с актуальными данными.');
      reply = { ...value, binding, contextToday };
    } catch (e) { if (id === generation) { error = e.message; ctx.onError(e); } }
    finally { if (id === generation) { busy = false; preview = null; ctx.render(); document.querySelector('.coach-answer')?.scrollIntoView({ block: 'nearest' }); } }
  }
  function draftDialog() {
    const response = currentReply();
    if (!response?.result.draft) { ctx.toast('Данные изменились. Подготовьте новый черновик.'); return; }
    const draft = response.result.draft;
    draftCommand = { revision: response.revision, binding: state().sessionBinding, contextToday: response.contextToday, id: crypto.randomUUID(), draft };
    ctx.openDialog(`${ctx.dialogHeader('Проверьте запись', 'AI мог неверно понять фразу. Исправьте поля перед сохранением.')}<form id="coach-draft-form"><label class="field"><span class="field-label">Дата</span><input name="date" type="date" required min="${esc(state().historyEditableFrom)}" max="${esc(state().today)}" value="${esc(draft.date)}"></label><label class="field"><span class="field-label">Что отметить</span><select name="status">${[['wellbeing','Только самочувствие'],['done','Занятие выполнено'],['missed','Занятие пропущено']].map(([key,label]) => `<option value="${key}" ${draft.status === key ? 'selected' : ''}>${label}</option>`).join('')}</select></label><div class="form-grid"><label class="field"><span class="field-label">Сон, часов</span><input name="sleep" type="number" min="0" max="24" step="0.1" value="${draft.sleep ?? ''}" placeholder="Не указано"></label><label class="field"><span class="field-label">Усталость, 1–10</span><input name="fatigue" type="number" min="1" max="10" step="1" value="${draft.fatigue ?? ''}" placeholder="Не указано"></label></div><p class="small-print">Отметка за тот же день обновит существующую. Пустые поля сохраняют ранее записанные значения. Выполнение возможно только в день занятия из вашего плана.</p>${formError}<div class="dialog-actions"><button class="button outline" type="button" data-action="close">Не сохранять</button><button class="button primary" type="submit">Сохранить подтверждённую запись</button></div></form>`);
  }
  async function handleClick(action, button) {
    if (!action.startsWith('coach-')) return false;
    if (action === 'coach-task' && !busy && tasks[button.dataset.task]) { task = button.dataset.task; message = ''; reply = preview = null; error = ''; ctx.render(); document.querySelector(`[data-task="${task}"]`)?.focus(); }
    if (action === 'coach-clear') { generation++; busy = false; reply = preview = draftCommand = null; error = ''; ctx.render(); }
    if (action === 'coach-draft') draftDialog();
    if (action === 'coach-reconcile' && recovery?.binding === state().sessionBinding) { ctx.reviewEntry(recovery.entry); recovery = null; }
    if (action === 'coach-open') { ctx.closeDialog(); ctx.navigate('assistant'); }
    return true;
  }
  async function handleSubmit(event) {
    const form = event.target;
    if (!['coach-form', 'coach-consent-form', 'coach-draft-form'].includes(form.id)) return false;
    event.preventDefault();
    if (ctx.isBusy()) return true;
    const data = new FormData(form), errorBox = form.querySelector('#form-error');
    if (errorBox) errorBox.textContent = '';
    try {
      if (form.id === 'coach-form') { message = String(data.get('message') || '').trim(); await prepare(); }
      if (form.id === 'coach-consent-form') await send(form);
      if (form.id === 'coach-draft-form') {
        if (!draftCommand || draftCommand.revision !== state().revision || draftCommand.binding !== state().sessionBinding || draftCommand.contextToday !== state().today) throw new Error('Данные изменились. Подготовьте новый черновик.');
        const entry = { id: draftCommand.id, date: data.get('date'), status: data.get('status') };
        if (entry.status !== 'wellbeing') entry.sessionId = entry.date;
        if (data.get('sleep') !== '') entry.sleep = Number(data.get('sleep'));
        if (data.get('fatigue') !== '') entry.fatigue = Number(data.get('fatigue'));
        if (entry.status === 'wellbeing' && entry.sleep == null && entry.fatigue == null) throw new Error('Укажите сон или усталость.');
        const command = draftCommand;
        if (await ctx.mutate('/api/entries', { revision: command.revision, entry }, 'POST', 'Проверенная вами запись сохранена.')) { reply = draftCommand = null; ctx.navigate('journal'); }
        else if (command.binding === state().sessionBinding && command.revision !== state().revision && form.isConnected) {
          recovery = { entry, binding: command.binding };
          errorBox.textContent = 'Дневник уже обновился. Ваши исправления сохранены в этой форме. Сравните их с текущей записью перед повторным сохранением.';
          if (!form.querySelector('[data-action="coach-reconcile"]')) errorBox.insertAdjacentHTML('beforeend', '<button type="button" class="button outline" data-action="coach-reconcile">Проверить с текущим дневником</button>');
        }
      }
    } catch (e) { if (errorBox) errorBox.textContent = e.message; else { error = e.message; ctx.render(); } }
    return true;
  }
  function handleInput(event) { if (event.target.name === 'message' && event.target.closest('#coach-form')) message = event.target.value; }
  return { view, handleClick, handleSubmit, handleInput, reset };
}
