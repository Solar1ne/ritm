import { request } from './api.js';
import { icon } from './icons.js';
import { runningMetrics, runPace, formatPace } from './activity-metrics.js';
import { escapeHTML as esc, isoDate, dateLabel, weekLabel, timeZone } from './format.js';

const kinds = { run: 'Бег', walk: 'Прогулка', cycling: 'Велосипед', swimming: 'Плавание', strength: 'Силовая', mobility: 'Подвижность', other: 'Активность' };
const heading = (title, text) => `<div class="page-heading"><div><p class="eyebrow">ВАШ РИТМ · ВАШИ ДАННЫЕ</p><h1>${title}</h1><p class="subtitle">${text}</p></div></div>`;
const errorBox = '<div id="form-error" class="form-error" role="alert"></div>';
export function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob), link = document.createElement('a');
  link.href = url; link.download = filename; document.body.append(link); link.click(); link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// One controller owns transient file previews and read-only assistant replies.
// Account state and writes stay owned by app.js's single mutation gate.
export function createFeatures(ctx) {
  let importFile = null, importPreview = null, importBinding = '', backup = null, planCommand = null, deleteCommand = null;
  let assistantReply = null, assistantQuestion = '', assistantBusy = false, assistantError = '';
  let assistantReplyRevision = null, assistantReplyDay = '';
  let readGeneration = 0, weekGeneration = 0, notificationConsent = false, notificationBinding = '', notified = new Set();
  const header = ctx.dialogHeader;
  const state = ctx.getState;
  const open = ctx.openDialog;
  const reset = () => {
    importFile = importPreview = backup = planCommand = deleteCommand = assistantReply = null;
    importBinding = '';
    assistantQuestion = assistantError = ''; assistantBusy = false; readGeneration++; weekGeneration++;
    notificationConsent = false; notificationBinding = ''; notified.clear();
  };

  function activities() {
    const current = state();
    const items = [...(current.workouts || [])].sort((a, b) => b.startedAt.localeCompare(a.startedAt));
    const metrics = runningMetrics(items, current.today);
    const number = (value, digits = 1) => value.toLocaleString('ru-RU', { maximumFractionDigits: digits });
    const stats = metrics.from ? `<div class="section-title"><h2>Бег за 30 дней <span class="meta">${dateLabel(metrics.from)} — ${dateLabel(metrics.through)}</span></h2></div>
      <section class="stats" aria-label="Записанные пробежки за последние 30 дней">
        <div class="stat"><p class="stat-label">${icon('run')}Пробежки</p><p class="stat-value">${metrics.totalRuns}<span>всего</span></p><p class="stat-foot">Записано минут: ${number(metrics.totalMinutes)}</p></div>
        <div class="stat"><p class="stat-label">${icon('chart')}Известная дистанция</p><p class="stat-value">${metrics.knownDistanceMeters == null ? '—' : number(metrics.knownDistanceMeters / 1000, 2)}<span>км</span></p><p class="stat-foot">${metrics.knownDistanceMeters == null ? 'Нет записанной дистанции' : metrics.missingDistanceRuns ? `${metrics.missingDistanceRuns} без дистанции` : `Записей с дистанцией: ${metrics.knownDistanceRuns}`}</p></div>
        <div class="stat"><p class="stat-label">${icon('clock')}Средний темп</p><p class="stat-value">${formatPace(metrics.paceMinutesPerKm)}</p><p class="stat-foot">${metrics.paceMinutesPerKm == null ? 'Темп появится с дистанцией' : `мин/км · записей: ${metrics.paceRuns}`}</p></div>
      </section><p class="small-print">Средний темп — сумма записанных минут пробежек с положительной дистанцией, делённая на их суммарную дистанцию. Время без движения отдельно не выделяется.</p>` : '<p class="small-print">Статистика появится после получения текущей даты сервера.</p>';
    return `${heading('Всё ваше движение', 'Добавьте занятия из файла и сохраните их рядом с планом.')}<div class="feature-toolbar"><button class="button primary" data-action="import-open">${icon('plus')} Импорт тренировок</button><button class="button outline" data-action="import-template">${icon('download')} Шаблон CSV</button></div>
      <p class="small-print">CSV по шаблону Ритма или TCX v2, до 100 тренировок и 1 МиБ за раз. Это импорт файла, без подключения к аккаунту часов. FIT не поддерживается.</p>
      ${stats}
      ${items.length ? `<div class="activity-list">${items.map(item => {
        const pace = runPace(item);
        const distance = item.distanceMeters == null ? 'Дистанция не указана' : `${number(item.distanceMeters / 1000, 2)} км`;
        return `<article class="activity-row"><span class="session-symbol">${icon(item.kind === 'run' ? 'run' : item.kind === 'strength' ? 'strength' : 'leaf')}</span><div><h3>${esc(kinds[item.kind] || 'Активность')}</h3><p>${dateLabel(item.date)} · ${number(item.minutes)} мин · ${distance}${pace == null ? '' : ` · ${formatPace(pace)} мин/км`}</p><small>${esc(item.source.toUpperCase())} · ${item.sessionId ? 'Связано с занятием в плане' : 'Отдельная активность'}</small></div><button class="icon-button" aria-label="Удалить тренировку за ${dateLabel(item.date)}" data-action="workout-delete" data-id="${esc(item.id)}">${icon('close')}</button></article>`;
      }).join('')}</div>` : '<div class="empty-state"><h2>Движение бывает разным</h2><p>Здесь появятся прогулки, пробежки и другие занятия из ваших файлов.</p></div>'}
      <p class="small-print">Импортированные минуты — наблюдения из файла. Плановые минуты показаны отдельно. Импорт не меняет сон и усталость; зачёт занятия в расписании требует вашего подтверждения.</p>`;
  }

  function assistant() {
    if (assistantReply && (assistantReplyRevision !== state().revision || assistantReplyDay !== state().today)) assistantReply = null;
    return `<div class="section-title rules-heading"><h2>Ответы по правилам · всегда рядом</h2></div><section class="assistant-intro panel"><span class="icon-badge">${icon('spark')}</span><div><h2>Помощник по вашему плану</h2><p class="panel-description">Объясняет сохранённые данные и правила Ритма. Ответы формируются по проверяемым сценариям; это не генеративная модель.</p></div></section>
      <div class="question-picks">${['Почему изменился план?', 'Что у меня сегодня?', 'Какой у меня прогресс?', 'Что с восстановлением?'].map(q => `<button class="button outline" data-action="assistant-ask" data-question="${esc(q)}" ${assistantBusy ? 'disabled' : ''}>${q}</button>`).join('')}</div>
      <form id="assistant-form" class="assistant-form"><label class="field"><span class="field-label">Ваш вопрос</span><input name="question" maxlength="500" required placeholder="Например: почему снизилась нагрузка?" value="${esc(assistantQuestion)}" ${assistantBusy ? 'disabled' : ''}></label><button type="submit" class="button primary" ${assistantBusy ? 'disabled' : ''}>${assistantBusy ? 'Проверяем данные…' : 'Спросить'} ${icon('arrow')}</button></form>
      ${assistantError ? `<div class="notice error" role="alert">${esc(assistantError)}</div>` : ''}
      ${assistantReply ? `<section class="panel assistant-answer" aria-live="polite"><p class="eyebrow">ПО ВАШИМ СОХРАНЁННЫМ ДАННЫМ</p><h2>${esc(assistantQuestion)}</h2><p class="answer-text">${esc(assistantReply.text)}</p>${assistantReply.evidence?.length ? `<ul class="evidence-list">${assistantReply.evidence.map(item => `<li>${esc(typeof item === 'string' ? item : item.label || item.text || JSON.stringify(item))}</li>`).join('')}</ul>` : ''}<a class="button ghost" href="#week">Открыть мой план ${icon('arrow')}</a></section>` : ''}`;
  }

  function importDialog() {
    if (!state().profile) { ctx.profileDialog(); return; }
    importFile = importPreview = null;
    open(`${header('Добавить тренировки', 'Сначала проверим файл и покажем, что будет добавлено.')}<form id="import-preview-form"><label class="field"><span class="field-label">Файл CSV или TCX</span><input name="file" type="file" accept=".csv,.tcx,text/csv,application/xml" required></label><p class="small-print">До 1 МиБ и 100 тренировок. CSV: id,date,kind,minutes,distance. Типы: running, walking, cycling, swimming, strength, other. Дистанция — в метрах.</p>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="import-template">Шаблон CSV</button><button type="submit" class="button primary">Проверить файл ${icon('arrow')}</button></div></form>`);
  }

  function showImportPreview() {
    const items = importPreview.workouts, count = items.filter(item => !item.duplicate).length;
    open(`${header('Проверьте тренировки', `${count} новых · ${items.length - count} уже сохранены`)}<div class="import-preview">${items.map(item => `<div><span>${dateLabel(item.date)} · ${esc(kinds[item.kind])}</span><strong>${Number(item.minutes).toLocaleString('ru-RU', { maximumFractionDigits: 1 })} мин</strong><small>${item.duplicate ? 'Уже есть' : item.canComplete ? 'Есть неотмеченное занятие в плане' : 'Отдельная активность'}</small></div>`).join('')}</div><form id="import-save-form"><label class="consent-row"><input type="checkbox" name="completePlanned"><span>Также отметить выполненными совпадающие по дате занятия моего плана. Уже отмеченные пропуски сохранятся.</span></label><p class="small-print">Отдельные активности останутся в истории движения. Повтор того же файла не создаст дубликаты.</p>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="import-open">Другой файл</button><button type="submit" class="button primary">Сохранить тренировки ${icon('check')}</button></div></form>`);
  }

  function reminderDialog() {
    if (!state().profile) { ctx.profileDialog(); return; }
    const value = state().reminder || {}, clock = value.time || '18:00';
    open(`${header('Время для себя', `Календарь использует часовой пояс ${state().timeZone || timeZone()}.`)}<form id="reminder-form"><label class="consent-row"><input type="checkbox" name="enabled" ${value.enabled ? 'checked' : ''}><span>Добавлять напоминания в календарь занятий</span></label><label class="field"><span class="field-label">Начало занятия</span><input name="time" type="time" value="${esc(clock)}" required></label><label class="field"><span class="field-label">Когда напомнить</span><select name="beforeMinutes">${[0, 5, 15, 30, 60].map(n => `<option value="${n}" ${(value.beforeMinutes || 0) === n ? 'selected' : ''}>${n ? `За ${n} минут` : 'В начале занятия'}</option>`).join('')}</select></label>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Закрыть</button><button type="submit" class="button primary">Сохранить настройки</button></div></form><div class="settings-item"><div><h3>Календарь недели</h3><p>Скачайте ICS и откройте в своём календаре.</p></div><button class="button outline" data-action="calendar-download">${icon('download')} Скачать</button></div><p class="small-print">Сначала сохраните настройки, затем скачайте календарь. ICS — отдельная копия: изменение плана или выключение напоминаний здесь не отменяет события в вашем календаре. Их нужно обновить или удалить там.</p><div class="settings-item"><div><h3>Пока Ритм открыт</h3><p>${notificationConsent ? 'Разрешены на время этой сессии.' : 'Дополнительно разрешите уведомления браузера на время этой сессии.'}</p></div><button class="button outline" data-action="notifications">${notificationConsent ? 'Выключить' : 'Разрешить'}</button></div><p class="small-print">Браузерные уведомления требуют сохранённого включённого напоминания и работают только при открытом приложении. Telegram WebView может их не поддерживать.</p>`);
  }

  function restoreDialog() {
    if (state().profile || state().entries.length || state().workouts?.length) {
      open(`${header('Восстановление в пустой профиль', 'Чтобы сохранить текущую историю, восстановление доступно только в пустом пространстве.')}<p class="panel-description">Откройте Ритм в другом браузере или сначала экспортируйте текущие данные. Очистка данных выполняется отдельно в настройках.</p><div class="dialog-actions"><button class="button outline" data-action="settings">К настройкам</button></div>`); return;
    }
    backup = null;
    open(`${header('Вернуть свой ритм', 'Выберите JSON, который вы скачали через «Скачать мои данные».')}<form id="restore-preview-form"><label class="field"><span class="field-label">Резервная копия Ритма</span><input name="file" type="file" accept=".json,application/json" required></label>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Отмена</button><button type="submit" class="button primary">Посмотреть копию</button></div></form>`);
  }

  async function loadWeek(value) {
    const generation = readGeneration, requestID = ++weekGeneration;
    try { const view = await request(`/api/weeks?weekStart=${encodeURIComponent(value)}`); if (generation === readGeneration && requestID === weekGeneration) ctx.setWeek(view); }
    catch (error) { ctx.onError(error); }
  }

  async function ask(question) {
    if (!state().profile) { ctx.profileDialog(); return; }
    if (assistantBusy) return;
    const generation = readGeneration;
    const revision = state().revision, today = state().today;
    assistantQuestion = question; assistantReply = null; assistantError = ''; assistantBusy = true; ctx.render();
    try {
      const reply = await request('/api/assistant', { question });
      if (generation === readGeneration) {
        if (state().revision === revision && state().today === today) { assistantReply = reply; assistantReplyRevision = revision; assistantReplyDay = today; }
        else assistantError = 'Дневник или дата изменились во время ответа. Задайте вопрос ещё раз, чтобы учесть новые данные.';
      }
    }
    catch (error) { if (generation === readGeneration) { assistantError = error.message; ctx.onError(error); } }
    finally { if (generation === readGeneration) { assistantBusy = false; ctx.render(); document.querySelector('.assistant-answer')?.scrollIntoView({ block: 'nearest' }); } }
  }

  async function handleClick(action, button) {
    switch (action) {
      case 'activities': case 'assistant': ctx.closeDialog(); ctx.navigate(action); return true;
      case 'import-open': importDialog(); return true;
      case 'import-template': downloadBlob(new Blob([`id,date,kind,minutes,distance\nmy-walk-1,${isoDate()},walking,30,2000\n`], { type: 'text/csv;charset=utf-8' }), 'rhythm-template.csv'); return true;
      case 'reminders': reminderDialog(); return true;
      case 'restore': restoreDialog(); return true;
      case 'assistant-ask': await ask(button.dataset.question); return true;
      case 'week-history': await loadWeek(button.dataset.week); return true;
      case 'plan-accept': case 'plan-cancel': {
        const next = action === 'plan-accept' ? state().nextPlan : state().acceptedNextPlan;
        if (!next) return true;
        planCommand = { weekStart: next.weekStart, nextPlanToken: state().nextPlanToken, revision: state().revision, cancel: action === 'plan-cancel' };
        open(`${header(action === 'plan-accept' ? 'Сохранить следующую неделю?' : 'Отменить принятый план?', weekLabel(next.weekStart))}<p class="panel-description">${action === 'plan-accept' ? `${next.sessions.length} занятия · ${next.totalMinutes} минут. Этот вариант начнётся в понедельник. Новые отметки не изменят его автоматически.` : 'Можно выбрать новое предложение до начала недели. Текущая неделя и прошлые записи сохранятся.'}</p><form id="plan-form">${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Назад</button><button type="submit" class="button primary">${action === 'plan-accept' ? 'Принять план' : 'Отменить принятие'}</button></div></form>`); return true;
      }
      case 'entry-delete': case 'workout-delete': {
        deleteCommand = { endpoint: action === 'entry-delete' ? '/api/entries' : '/api/workouts', id: button.dataset.id, revision: state().revision };
        open(`${header(action === 'entry-delete' ? 'Удалить отметку?' : 'Удалить тренировку?', action === 'entry-delete' ? 'Запись исчезнет из дневника. Предложение будет рассчитано по оставшимся фактам.' : 'Активность исчезнет из импорта. Отметка выполнения в плане, если она есть, сохранится отдельно.')}<form id="fact-delete-form">${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="close">Оставить</button><button type="submit" class="button danger">Удалить запись</button></div></form>`); return true;
      }
      case 'calendar-download': {
        button.disabled = true;
        try { const date = button.dataset.week || state().currentWeekStart; const blob = await request(`/api/calendar?weekStart=${encodeURIComponent(date)}`, undefined, 'GET', 'blob'); downloadBlob(blob, `rhythm-${date}.ics`); ctx.toast('Откройте файл в календаре, чтобы добавить занятия.'); }
        catch (error) { ctx.onError(error); } finally { button.disabled = false; } return true;
      }
      case 'notifications': {
        if (notificationConsent) { notificationConsent = false; reminderDialog(); return true; }
        if (!('Notification' in window)) { ctx.toast('Этот браузер не поддерживает уведомления. Используйте календарь.'); return true; }
        const generation = readGeneration, binding = state().sessionBinding;
        try { const permission = await Notification.requestPermission(); if (generation !== readGeneration || binding !== state().sessionBinding) return true; notificationConsent = permission === 'granted'; notificationBinding = binding; if (!notificationConsent) ctx.toast('Уведомления не разрешены. Календарь остаётся доступен.'); if (ctx.dialogOpen()) reminderDialog(); }
        catch { ctx.toast('Уведомления недоступны в этом браузере. Используйте календарь.'); } return true;
      }
      default: return false;
    }
  }

  async function handleSubmit(event) {
    const form = event.target, formID = form.id;
    if (!['import-preview-form', 'import-save-form', 'reminder-form', 'restore-preview-form', 'restore-save-form', 'plan-form', 'fact-delete-form', 'assistant-form'].includes(formID)) return false;
    event.preventDefault();
    if (ctx.isBusy()) return true;
    const data = new FormData(form), error = form.querySelector('#form-error');
    if (error) error.textContent = '';
    try {
      if (formID === 'assistant-form') { await ask(String(data.get('question')).trim()); return true; }
      if (formID === 'import-preview-form' || formID === 'restore-preview-form') {
        const file = data.get('file'), max = formID === 'import-preview-form' ? 1024 * 1024 : 4 * 1024 * 1024;
        if (!file?.size || file.size > max) throw new Error(`Выберите непустой файл до ${max / 1024 / 1024} МиБ.`);
        const submit = form.querySelector('[type=submit]'); submit.disabled = true;
        const generation = readGeneration, binding = state().sessionBinding;
        try {
          const content = await file.text();
          if (generation !== readGeneration || binding !== state().sessionBinding || !form.isConnected || !ctx.dialogOpen()) return true;
          if (formID === 'import-preview-form') {
            const format = file.name.toLowerCase().endsWith('.tcx') ? 'tcx' : 'csv';
            const preview = await request('/api/import/preview', { format, content });
            if (generation !== readGeneration || binding !== state().sessionBinding || !form.isConnected || !ctx.dialogOpen()) return true;
            importFile = { format, content }; importPreview = preview; importBinding = binding; showImportPreview();
          } else {
            const parsed = JSON.parse(content);
            if (!parsed || parsed.exportVersion !== 2 || !Array.isArray(parsed.entries)) throw new Error('Нужна резервная копия Ритма версии 2.');
            if (generation !== readGeneration || binding !== state().sessionBinding || !form.isConnected || !ctx.dialogOpen()) return true;
            backup = parsed;
            open(`${header('Восстановить эту копию?', 'Сервер проверит все записи до сохранения.')}<dl class="day-detail"><div><dt>Имя</dt><dd>${esc(parsed.profile?.name || 'Не указано')}</dd></div><div><dt>Отметок</dt><dd>${parsed.entries.length}</dd></div><div><dt>Сохранённых недель</dt><dd>${Array.isArray(parsed.weeks) ? parsed.weeks.length : 0}</dd></div><div><dt>Импортированных тренировок</dt><dd>${Array.isArray(parsed.workouts) ? parsed.workouts.length : 0}</dd></div><div><dt>Измерений HRV</dt><dd>${Array.isArray(parsed.hrvEntries) ? parsed.hrvEntries.length : 0}</dd></div><div><dt>Измерений состава тела</dt><dd>${Array.isArray(parsed.bodyEntries) ? parsed.bodyEntries.length : 0}</dd></div><div><dt>Выбранных блюд</dt><dd>${Array.isArray(parsed.mealSelections) ? parsed.mealSelections.length : 0}</dd></div></dl><form id="restore-save-form">${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="restore">Другой файл</button><button type="submit" class="button primary">Восстановить данные</button></div></form>`);
          }
        } finally { submit.disabled = false; }
      } else if (formID === 'import-save-form' && importFile) {
        if (importBinding !== state().sessionBinding || importPreview.revision !== state().revision) throw new Error('Дневник изменился. Закройте окно, обновите страницу и проверьте файл заново перед сохранением.');
        if (await ctx.mutate('/api/import', { ...importFile, revision: importPreview.revision, completePlanned: data.has('completePlanned') }, 'POST', 'Тренировки сохранены.')) { importFile = importPreview = null; importBinding = ''; ctx.navigate('activities'); }
      } else if (formID === 'reminder-form') {
        await ctx.mutate('/api/reminders', { revision: state().revision, reminder: { enabled: data.has('enabled'), time: data.get('time'), beforeMinutes: Number(data.get('beforeMinutes')) } }, 'POST', 'Настройки сохранены. Скачайте календарь с новыми параметрами.');
      } else if (formID === 'restore-save-form' && backup) {
        if (await ctx.mutate('/api/restore', { revision: state().revision, backup }, 'POST', 'Данные восстановлены в вашем пространстве.')) { reset(); ctx.navigate('overview'); }
      } else if (formID === 'plan-form' && planCommand) {
        const { cancel, ...input } = planCommand;
        if (cancel) delete input.nextPlanToken;
        await ctx.mutate('/api/plan/accept', input, cancel ? 'DELETE' : 'POST', cancel ? 'Принятый план отменён. Можно выбрать новое предложение.' : 'Следующая неделя сохранена.');
      } else if (formID === 'fact-delete-form' && deleteCommand) {
        await ctx.mutate(deleteCommand.endpoint, { revision: deleteCommand.revision, id: deleteCommand.id }, 'DELETE', 'Запись удалена.');
      }
    } catch (problem) { if (error?.isConnected) error.textContent = problem.message; ctx.onError(problem); }
    return true;
  }

  function tick() {
    const current = state();
    if (!notificationConsent || notificationBinding !== current.sessionBinding || !current.reminder?.enabled || !current.plan || Notification.permission !== 'granted') return;
    const now = new Date(), parts = new Intl.DateTimeFormat('en-GB', { timeZone: current.timeZone || timeZone(), hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(now);
    const minute = Number(parts.find(p => p.type === 'hour').value) * 60 + Number(parts.find(p => p.type === 'minute').value);
    const [hour, minutes] = current.reminder.time.split(':').map(Number), alarm = hour * 60 + minutes - current.reminder.beforeMinutes;
    const day = isoDate(now);
    const sessions = new Map([...current.plan.sessions, ...(current.acceptedNextPlan?.sessions || [])].map(session => [session.id, session]));
    for (const session of sessions.values()) {
      // An alarm before midnight belongs to the previous calendar day.
      const start = new Date(`${session.date}T12:00:00Z`); if (alarm < 0) start.setUTCDate(start.getUTCDate() - 1);
      const dueDay = start.toISOString().slice(0, 10), dueMinute = (alarm + 1440) % 1440;
      const key = `${session.id}:${current.reminder.time}:${current.reminder.beforeMinutes}`;
      if (session.status !== 'planned' || day !== dueDay || minute !== dueMinute || notified.has(key)) continue;
      notified.add(key);
      try { const notice = new Notification('Время для своего ритма', { body: 'В вашем плане скоро занятие. Откройте Ритм, чтобы посмотреть расписание.', tag: `rhythm-${session.id}` }); notice.onclick = () => { window.focus(); ctx.navigate('week'); notice.close(); }; }
      catch { notificationConsent = false; }
    }
  }

  return { activities, assistant, handleClick, handleSubmit, loadWeek, reset, tick };
}
