import { escapeHTML as esc, addDays, dateLabel } from './format.js';
import { icon } from './icons.js';
import { measurementGroups, observations, trend, bodyComposition, hrvWindows, bodyMethods } from './measurements.js';

const format = value => Number(value).toLocaleString('ru-RU', { maximumSignificantDigits: 6 });
const errorBox = '<div id="form-error" class="form-error" role="alert"></div>';
const options = (values, current) => Object.entries(values).map(([id, label]) => `<option value="${id}" ${id === current ? 'selected' : ''}>${esc(label)}</option>`).join('');
const valueLabel = (entry, kind) => kind === 'hrv' ? `${format(entry.valueMs)} мс` : `${entry.weightKg == null ? 'Вес не указан' : format(entry.weightKg) + ' кг'} · ${entry.bodyFatPercent == null ? 'Доля жира не указана' : format(entry.bodyFatPercent) + '% жира'}`;

function chart(points, label, unit) {
  if (!points.length) return '<div class="measurement-empty">Для этого показателя пока нет измерений.</div>';
  const low = Math.min(...points.map(point => point.value)), high = Math.max(...points.map(point => point.value));
  const pad = Math.max((high - low) * .15, high * .03, .5), min = Math.max(0, low - pad), max = high + pad;
  const start = Date.parse(points[0].date), span = Math.max(86400000, Date.parse(points.at(-1).date) - start);
  const locations = points.map(point => ({ ...point, x: points.length === 1 ? 300 : 52 + (Date.parse(point.date) - start) / span * 520, y: 150 - (point.value - min) / (max - min) * 125 }));
  return `<div class="measurement-chart"><svg viewBox="0 0 600 185" role="img" aria-label="${esc(label)}. ${points.length} измерений. Числа доступны в списке ниже."><path d="M52 25H572M52 87.5H572M52 150H572" class="measurement-grid"/><text x="4" y="29">${format(max)}</text><text x="4" y="154">${format(min)}</text>${locations.length > 1 ? `<polyline points="${locations.map(p => `${p.x},${p.y}`).join(' ')}" class="measurement-line"/>` : ''}${locations.map(p => `<circle cx="${p.x}" cy="${p.y}" r="4" class="measurement-point"><title>${esc(dateLabel(p.date))}: ${format(p.value)} ${unit}</title></circle>`).join('')}<text x="52" y="178">${esc(dateLabel(points[0].date, { day: 'numeric', month: 'short' }))}</text>${points.length > 1 ? `<text x="572" y="178" text-anchor="end">${esc(dateLabel(points.at(-1).date, { day: 'numeric', month: 'short' }))}</text>` : ''}</svg></div>`;
}

function stat(label, result, unit, changeUnit = unit) {
  const delta = result.delta == null ? 'Нужно хотя бы два измерения' : `${result.delta > 0 ? '+' : ''}${format(result.delta)} ${changeUnit} с ${dateLabel(result.first.date)}`;
  return `<div class="stat"><p class="stat-label">${label}</p><p class="stat-value">${result.latest ? format(result.latest.value) : '—'}<span>${unit}</span></p><p class="stat-foot">${result.latest ? dateLabel(result.latest.date) : 'Нет измерения'}</p><p class="stat-foot">${delta}</p></div>`;
}

export function createWellness(ctx) {
  const state = ctx.getState;
  let kind = 'hrv', days = 30, seriesKey = '', bodyField = 'weightKg', command = null, recovery = null, replacement = null;
  function reset() { kind = 'hrv'; days = 30; seriesKey = ''; bodyField = 'weightKg'; command = recovery = replacement = null; }
  const entries = () => state()[kind === 'hrv' ? 'hrvEntries' : 'bodyEntries'] || [];
  function view() {
    const groups = measurementGroups(entries(), kind, state().today, days);
    const group = groups.find(item => item.key === seriesKey) || groups[0];
    seriesKey = group?.key || '';
    const observed = group?.entries || [], points = observations(observed, kind === 'hrv' ? 'valueMs' : bodyField);
    const count = `<div class="stat"><p class="stat-label">В этой серии</p><p class="stat-value">${observed.length}<span>измерений</span></p><p class="stat-foot">${days ? `За ${days} дней` : 'Вся история'} · один метод и источник</p></div>`;
    const composition = kind === 'body' ? bodyComposition(observed) : null;
    const hrv = kind === 'hrv';
    return `<div class="page-heading"><div><p class="eyebrow">НАБЛЮДЕНИЯ ВО ВРЕМЕНИ</p><h1>Картина вашего самочувствия</h1><p class="subtitle">Сохраните измерения и посмотрите свою историю в сопоставимых условиях.</p></div><button class="button primary" data-action="measurement-add" aria-label="Добавить измерение">${icon('plus')}<span class="button-label">Добавить измерение</span></button></div>
      <div class="tabs" role="group" aria-label="Вид измерений">${[['hrv', 'История HRV'], ['body', 'Состав тела']].map(([id, title]) => `<button class="tab ${id === kind ? 'active' : ''}" data-action="measurement-tab" data-kind="${id}" aria-pressed="${id === kind}">${title}</button>`).join('')}</div>
      <div class="measurement-intro"><span class="icon-badge">${icon(hrv ? 'heart' : 'scale')}</span><div><h2>${hrv ? 'Сравнивайте себя с собой' : 'Изменения, которые можно проследить'}</h2><p>${hrv ? 'HRV — вариабельность интервалов между ударами сердца. RMSSD и SDNN, короткую и ночную запись показываем отдельными сериями. Выбирайте один прибор и похожие условия.' : 'Вес и доля жира сохраняются как отдельные показатели. Процент жира — оценка выбранного метода; условия измерения, в том числе гидратация, могут влиять на результат.'}</p></div></div>
      <div class="history-toolbar"><label class="measurement-series"><span>Серия измерений</span><select id="measurement-series" ${groups.length ? '' : 'disabled'}>${groups.length ? groups.map(item => `<option value="${esc(item.key)}" ${item.key === seriesKey ? 'selected' : ''}>${esc(item.label)}</option>`).join('') : '<option>Пока нет записей</option>'}</select></label><div class="period-switch" role="group" aria-label="Период измерений">${[30, 90, 0].map(period => `<button data-action="measurement-period" data-days="${period}" aria-pressed="${period === days}" class="${period === days ? 'active' : ''}">${period ? period + ' дней' : 'Вся история'}</button>`).join('')}</div></div>
      ${observed.length ? `<section class="stats" aria-label="Последние измерения">${hrv ? stat('Последнее HRV', trend(points), 'мс') + `<div class="stat"><p class="stat-label">Диапазон записанных значений</p><p class="stat-value">${format(Math.min(...points.map(p => p.value)))}–${format(Math.max(...points.map(p => p.value)))}<span>мс</span></p><p class="stat-foot">Без оценки «хорошо» или «плохо»</p></div>` : stat('Последний вес', trend(observations(observed, 'weightKg')), 'кг') + stat('Последняя доля жира', trend(observations(observed, 'bodyFatPercent')), '%', 'п.п.')}${count}</section>
      <section class="panel"><div class="section-title"><h2>${hrv ? 'История HRV' : bodyField === 'weightKg' ? 'История веса' : 'История доли жира'}</h2>${!hrv ? `<label class="measurement-field"><span class="sr-only">Показатель графика</span><select id="measurement-field">${options({ weightKg: 'Вес, кг', bodyFatPercent: 'Доля жира, %' }, bodyField)}</select></label>` : ''}</div>${chart(points, hrv ? 'HRV' : bodyField === 'weightKg' ? 'Вес' : 'Доля жира', hrv ? 'мс' : bodyField === 'weightKg' ? 'кг' : '%')}<p class="small-print">Каждая точка — сохранённое измерение. Линия соединяет записи; значения между ними неизвестны. Эти показатели сами по себе не меняют тренировочный план.</p></section>
      ${composition ? `<div class="progress-note">${icon('scale')}<span>По одной полной записи от ${dateLabel(composition.date)}: расчётная жировая масса ${format(composition.fatKg)} кг, остальная масса ${format(composition.otherKg)} кг. Остальная масса включает воду, мышцы, кости и другие ткани; это не измерение мышечной массы.</span></div>` : ''}
      <div class="measurement-records"><h2>Записи этой серии</h2>${[...observed].reverse().map(entry => `<article class="measurement-row"><div><h3>${dateLabel(entry.date)}</h3><p>${valueLabel(entry, kind)}</p></div><div class="journal-actions">${entry.date >= state().historyEditableFrom ? `<button class="button ghost" data-action="measurement-edit" data-id="${esc(entry.id)}">Изменить</button>` : ''}<button class="icon-button" data-action="measurement-delete" data-id="${esc(entry.id)}" aria-label="Удалить измерение за ${dateLabel(entry.date)}">${icon('close')}</button></div></article>`).join('')}</div>` : `<div class="empty-state">${icon(hrv ? 'heart' : 'scale')}<h2>${entries().length ? 'В этом периоде нет измерений' : 'Ваша история начнётся с одной записи'}</h2><p>${hrv ? 'Перенесите показатель HRV из своего устройства. Метрику и длительность можно найти в его описании.' : 'Укажите вес или долю жира из измерения. Неизвестный показатель можно оставить пустым.'}</p><button class="button outline" data-action="measurement-add">Добавить измерение</button></div>`}
      <p class="small-print">${hrv ? 'Определения и условия сравнения: <a href="https://www.escardio.org/static-file/Escardio/Guidelines/Scientific-Statements/guidelines-Heart-Rate-Variability-FT-1996.pdf" target="_blank" rel="noopener noreferrer">ESC / NASPE</a>.' : 'Об условиях измерения: <a href="https://support.withings.com/hc/en-us/articles/218970667-Body-Body-Composition-Accuracy-Issues" target="_blank" rel="noopener noreferrer">Withings</a>; о сопоставлении методов — <a href="https://iscd.org/text-based-official-adult-positions/" target="_blank" rel="noopener noreferrer">ISCD</a>.'} Ритм показывает историю, а не медицинское заключение.</p>`;
  }

  function openEntry(existing = null) {
    if (!state().profile) { ctx.profileDialog(); return; }
    const hrv = kind === 'hrv', today = state().today;
    recovery = replacement = null;
    command = { kind, id: crypto.randomUUID(), revision: state().revision, binding: state().sessionBinding, existing };
    const field = (name, label, min, max) => `<label class="field"><span class="field-label">${label}</span><input name="${name}" type="number" min="${min}" max="${max}" step="any" inputmode="decimal" ${hrv ? 'required' : ''} value="${existing?.[name] ?? ''}"></label>`;
    ctx.openDialog(`${ctx.dialogHeader(existing ? 'Исправить измерение' : hrv ? 'Записать HRV' : 'Записать состав тела', 'Сохраняем только те значения, которые вы действительно измерили.')}<form id="measurement-form"><label class="field"><span class="field-label">Дата измерения</span><input name="date" type="date" min="${state().historyEditableFrom || addDays(today, -89)}" max="${today}" value="${existing?.date || today}" ${existing ? 'readonly' : ''} required></label>
      ${hrv ? `${field('valueMs', 'HRV, миллисекунды', .001, 500)}<div class="two-col"><label class="field"><span class="field-label">Метрика</span><select name="metric" required ${existing ? 'disabled' : ''}><option value="">Выберите метрику</option>${options({ rmssd: 'RMSSD', sdnn: 'SDNN' }, existing?.metric)}</select></label><label class="field"><span class="field-label">Длительность записи</span><select name="window" required ${existing ? 'disabled' : ''}><option value="">Выберите длительность</option>${options(hrvWindows, existing?.window)}</select></label></div><p class="field-hint">Перенесите метрику и длительность из описания вашего измерения. Если они неизвестны, не выбирайте их наугад.</p>` : `<div class="two-col">${field('weightKg', 'Вес, кг', 20, 400)}${field('bodyFatPercent', 'Доля жира, %', .001, 75)}</div><label class="field"><span class="field-label">Метод измерения</span><select name="method" required ${existing ? 'disabled' : ''}><option value="">Выберите метод</option>${options(bodyMethods, existing?.method)}</select></label><p class="field-hint">Укажите хотя бы одно значение. Пустое поле означает, что показатель неизвестен.</p>`}
      <label class="field"><span class="field-label">Прибор и условия</span><input name="source" type="text" maxlength="60" value="${esc(existing?.source || '')}" placeholder="${hrv ? 'Например, часы A · ночное измерение' : 'Например, весы A · утром'}" ${existing ? 'readonly' : ''} required><p class="field-hint">Используйте одинаковое название для сопоставимых измерений. Для нового прибора или других условий создайте отдельную запись.</p></label>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Отмена</button><button type="submit" class="button primary">Сохранить измерение ${icon('check')}</button></div></form>`);
  }

  async function handleClick(action, button) {
    if (!action.startsWith('measurement-') && action !== 'wellness-open') return false;
    if (action === 'wellness-open') { ctx.closeDialog(); ctx.navigate('wellness'); }
    if (action === 'measurement-tab' && ['hrv', 'body'].includes(button.dataset.kind)) { kind = button.dataset.kind; seriesKey = ''; ctx.render(); document.querySelector(`[data-action=measurement-tab][data-kind=${kind}]`)?.focus(); }
    if (action === 'measurement-period') { days = [0, 30, 90].includes(Number(button.dataset.days)) ? Number(button.dataset.days) : 30; ctx.render(); document.querySelector(`[data-action=measurement-period][data-days="${days}"]`)?.focus(); }
    if (action === 'measurement-add') openEntry();
    if (action === 'measurement-replace' && replacement && command?.binding === state().sessionBinding) {
      command.replacementKey = replacement;
      button.closest('form')?.requestSubmit();
    }
    if (action === 'measurement-review' && recovery && command?.binding === state().sessionBinding) {
      const saved = (state()[command.kind === 'hrv' ? 'hrvEntries' : 'bodyEntries'] || []).find(entry => entry.date === recovery.date && entry.source === recovery.source && (command.kind === 'hrv' ? entry.metric === recovery.metric && entry.window === recovery.window : entry.method === recovery.method));
      command = { ...command, id: crypto.randomUUID(), revision: state().revision };
      const box = button.closest('form')?.querySelector('#form-error');
      if (box) box.textContent = `${saved ? 'Сейчас сохранено: ' + valueLabel(saved, command.kind) + '.' : 'Такого измерения сейчас нет.'} Ваши поля сохранены ниже. Проверьте их и отдельно нажмите «Сохранить измерение».`;
      recovery = null;
    }
    if (action === 'measurement-edit') { const entry = entries().find(item => item.id === button.dataset.id); if (entry) openEntry(entry); }
    if (action === 'measurement-delete') {
      const entry = entries().find(item => item.id === button.dataset.id); if (!entry) return true;
      command = { kind, id: entry.id, revision: state().revision, binding: state().sessionBinding };
      ctx.openDialog(`${ctx.dialogHeader('Удалить измерение?', `${dateLabel(entry.date)} · ${valueLabel(entry, kind)}`)}<p class="panel-description">Измерение исчезнет из вашей истории и экспортируемых данных.</p><form id="measurement-delete-form">${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Оставить</button><button type="submit" class="button danger">Удалить измерение</button></div></form>`);
    }
    return true;
  }
  async function handleSubmit(event) {
    const form = event.target;
    if (!['measurement-form', 'measurement-delete-form'].includes(form.id)) return false;
    event.preventDefault(); if (ctx.isBusy()) return true;
    const box = form.querySelector('#form-error'); box.textContent = '';
    try {
      if (!command || command.binding !== state().sessionBinding || command.revision !== state().revision) throw new Error('История обновилась. Закройте окно и проверьте текущую запись перед сохранением.');
      const remove = form.id === 'measurement-delete-form', data = new FormData(form);
      const entry = { id: command.id, date: data.get('date'), source: String(data.get('source') || '').trim() };
      if (command.kind === 'hrv') Object.assign(entry, { valueMs: Number(data.get('valueMs')), metric: command.existing?.metric || data.get('metric'), window: command.existing?.window || data.get('window') });
      else {
        entry.method = command.existing?.method || data.get('method');
        for (const key of ['weightKg', 'bodyFatPercent']) if (data.get(key) !== '') entry[key] = Number(data.get(key));
        if (!remove && entry.weightKg == null && entry.bodyFatPercent == null) throw new Error('Укажите вес или долю жира.');
      }
      if (!remove && !command.existing) {
        const collision = (state()[command.kind === 'hrv' ? 'hrvEntries' : 'bodyEntries'] || []).find(item => item.date === entry.date && item.source === entry.source && (command.kind === 'hrv' ? item.metric === entry.metric && item.window === entry.window : item.method === entry.method));
        const key = JSON.stringify(entry);
        if (collision && collision.id !== command.id && command.replacementKey !== key) {
          replacement = key;
          box.textContent = `За этот день в этой серии уже сохранено: ${valueLabel(collision, command.kind)}. Новая запись заменит её целиком; пустое поле удалит прежнее значение.`;
          box.insertAdjacentHTML('beforeend', '<button type="button" class="button outline" data-action="measurement-replace">Заменить сохранённое измерение</button>');
          return true;
        }
      }
      const original = command;
      const saved = await ctx.mutate(`/api/${command.kind}`, { revision: command.revision, ...(remove ? { id: command.id } : { entry }) }, remove ? 'DELETE' : 'POST', remove ? 'Измерение удалено.' : 'Измерение сохранено.');
      if (!saved && !remove && original.binding === state().sessionBinding && original.revision !== state().revision && form.isConnected) {
        recovery = entry;
        box.textContent = 'История изменилась. Введённые поля остаются в этой форме.';
        box.insertAdjacentHTML('beforeend', '<button type="button" class="button outline" data-action="measurement-review">Сравнить с сохранённой записью</button>');
      }
    } catch (error) { box.textContent = error.message; }
    return true;
  }
  function handleChange(event) {
    if (event.target.id === 'measurement-series') { seriesKey = event.target.value; ctx.render(); document.querySelector('#measurement-series')?.focus(); }
    if (event.target.id === 'measurement-field') { bodyField = event.target.value === 'bodyFatPercent' ? 'bodyFatPercent' : 'weightKg'; ctx.render(); document.querySelector('#measurement-field')?.focus(); }
  }
  return { view, handleClick, handleSubmit, handleChange, reset };
}
