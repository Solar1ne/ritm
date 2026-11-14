import { request } from './api.js';
import { escapeHTML as esc, dateLabel } from './format.js';
import { icon } from './icons.js';

const errorBox = '<div id="form-error" class="form-error" role="alert"></div>';
const places = { home: 'Дома', outdoors: 'На улице', gym: 'В зале' };

export function createVariants(ctx) {
  const state = ctx.getState;
  let command = null, generation = 0, busy = false;
  function reset() { generation++; command = null; busy = false; }
  function open(sessionID) {
    const session = [...(state().plan?.sessions || []), ...(state().acceptedNextPlan?.sessions || [])].find(s => s.id === sessionID);
    if (!session) { ctx.toast('Обновите план и выберите занятие снова.'); return; }
    command = { sessionID, session, revision: state().revision, binding: state().sessionBinding };
    ctx.openDialog(`${ctx.dialogHeader('Подстроить под ваш день', `${dateLabel(session.date)} · ${session.title}`)}<p class="small-print">Исходный план: ${session.originalMinutes || session.minutes} мин. Сейчас: ${session.minutes} мин. Выберите условия — покажем подходящие варианты.</p><form id="variant-options-form"><label class="field"><span class="field-label">Сколько времени есть?</span><input name="minutes" type="number" min="15" max="${session.originalMinutes || session.minutes}" step="1" value="${session.minutes}" required></label><label class="field"><span class="field-label">Где удобно заниматься?</span><select name="environment">${Object.entries(places).map(([key, label]) => `<option value="${key}" ${key === (session.environment || 'home') ? 'selected' : ''}>${label}</option>`).join('')}</select></label><label class="consent-row"><input type="checkbox" name="lowImpact" ${session.lowImpact ? 'checked' : ''}><span>Без прыжков, бега и дополнительного веса</span></label>${errorBox}<div class="dialog-actions"><button class="button outline" type="button" data-action="close">Отмена</button><button class="button primary" type="submit">Показать варианты ${icon('arrow')}</button></div></form>${session.variantId ? `<form id="variant-revert-form"><p class="small-print">Можно вернуть исходное занятие, если оно соответствует текущим ограничениям восстановления.</p>${errorBox}<button class="button ghost" type="submit">Вернуть исходное занятие</button></form>` : ''}`);
  }
  function showOptions(preview) {
    ctx.openDialog(`${ctx.dialogHeader('Вариант, который впишется', `${dateLabel(preview.original.date)} · исходно ${preview.original.minutes} мин`)}${preview.reasons.length ? `<div class="notice"><span>${preview.reasons.map(esc).join(' ')}</span></div>` : ''}<form id="variant-apply-form"><div class="variant-options">${preview.options.map((option, index) => `<label class="variant-choice"><input type="radio" name="variantId" value="${esc(option.id)}" ${index === 0 ? 'checked' : ''} required><span><strong>${esc(option.title)} <b>${option.minutes} мин</b></strong><small>${esc(option.description)}</small><em>${places[option.environment]}${option.lowImpact ? ' · без прыжков и дополнительного веса' : ''}</em></span></label>`).join('')}</div><p class="small-print">Изменится только это занятие. Исходный план сохранится. Отметка о выполнении останется отдельным действием.</p>${errorBox}<div class="dialog-actions"><button class="button outline" type="button" data-action="variant-open" data-id="${esc(command.sessionID)}">Другие условия</button><button class="button primary" type="submit">Выбрать это занятие</button></div></form>`);
  }
  async function handleClick(action, button) {
    if (action !== 'variant-open') return false;
    if (!busy) open(button.dataset.id);
    return true;
  }
  async function handleSubmit(event) {
    const form = event.target;
    if (!['variant-options-form', 'variant-apply-form', 'variant-revert-form'].includes(form.id)) return false;
    event.preventDefault();
    if (busy || ctx.isBusy()) return true;
    const box = form.querySelector('#form-error'); box.textContent = '';
    try {
      if (!command || command.revision !== state().revision || command.binding !== state().sessionBinding) throw new Error('План изменился. Закройте окно и выберите занятие заново.');
      const data = new FormData(form);
      if (form.id === 'variant-options-form') {
        const id = ++generation, binding = state().sessionBinding;
        command.constraints = { availableMinutes: Number(data.get('minutes')), environment: data.get('environment'), lowImpact: data.has('lowImpact') };
        busy = true; const submit = form.querySelector('[type=submit]'); submit.disabled = true;
        try {
          const preview = await request('/api/sessions/variants', { revision: command.revision, sessionId: command.sessionID, constraints: command.constraints });
          if (id !== generation || binding !== state().sessionBinding || !form.isConnected || !ctx.dialogOpen()) return true;
          if (preview.revision !== state().revision) throw new Error('План изменился. Выберите занятие заново.');
          showOptions(preview);
        } finally { if (id === generation) busy = false; submit.disabled = false; }
      } else {
        const revert = form.id === 'variant-revert-form';
        await ctx.mutate('/api/sessions/variant', { revision: command.revision, sessionId: command.sessionID, ...(revert ? {} : { variantId: data.get('variantId'), constraints: command.constraints }) }, revert ? 'DELETE' : 'POST', revert ? 'Исходное занятие восстановлено.' : 'Вариант сохранён в вашем плане.');
      }
    } catch (error) { if (error.code === 'session_changed') ctx.onError(error); else box.textContent = error.message; }
    return true;
  }
  return { handleClick, handleSubmit, reset };
}
