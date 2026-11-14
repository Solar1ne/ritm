import { request, APIError, resetSessionBinding, adoptSession } from './api.js';
import { installView, initializeInstall, promptInstall } from './install.js';
import { productHelpView } from './product-help.js';
import { dataUsageView } from './data-usage.js';
import { createExerciseLibrary } from './exercises.js';
import { createAccount } from './account.js';
import { initializeTelegram } from './telegram.js';
import { icon } from './icons.js';
import { dailyHistory } from './progress.js';
import { consistencyDayContent } from './consistency-ui.js';
import { createFeatures } from './features.js';
import { createCoach } from './coach.js';
import { createVariants } from './variants.js';
import { createWellness } from './wellness.js';
import { createNutrition } from './nutrition.js';
import { escapeHTML as esc, shortDays, goals, isoDate, monday, dateLabel, samplePlan, setTimeZone } from './format.js';
import { shell, routes, overview, week, journal, insights, about, lab, scenarios, scenarioPayload, sectionMenu } from './views.js';

const root = document.querySelector('#app'), dialog = document.querySelector('#dialog');
const emptyState = () => ({ revision: 0, profile: null, plan: null, nextPlan: null, decision: null, entries: [], workouts: [], weeks: [], hrvEntries: [], bodyEntries: [], mealSelections: [], reminder: {}, identity: 'guest', accountsEnabled: true });
let state = emptyState();
let route = routeFromHash(), showNext = false, telegram = null, miniPreview = false, loadError = '', labResult = null, labBusy = false, toastTimer;
let pendingDraft = readDraft();
let pendingEntry = pendingDraft?.entry || null;
let entryID = '', activeSession = null;
let ready = false, lastSuccess = null, staleError = ''; 
let telegramContext = null;
let mutationBusy = false;
let progressDays = 7, activityDays = 30, journalFilter = 'all', historyView = null;
let dialogReturnFocus = null, refreshPending = false, refreshing = false, lifecycleTimer, stateEpoch = 0;
const features = createFeatures({
  getState: () => state, dialogHeader, openDialog, closeDialog, render, toast, navigate, profileDialog,
  isBusy: () => mutationBusy, dialogOpen: () => dialog.open,
  onError: error => { if (error.code === 'session_changed') blockChangedSession(); else if (error.code !== 'stale_session') toast(error.message); },
  setWeek: view => { historyView = view; showNext = false; render(); }, mutate: saveMutation,
});
const coach = createCoach({
  getState: () => state, dialogHeader, openDialog, closeDialog, render, toast, navigate, profileDialog,
  isBusy: () => mutationBusy, mutate: saveMutation,
  isActive: () => route === 'assistant', dialogOpen: () => dialog.open,
  reviewEntry: entry => {
    const saved = state.entries.find(e => entry.sessionId ? e.sessionId === entry.sessionId : e.date === entry.date && e.status === 'wellbeing');
    checkinDialog(entry.sessionId, null, { ...entry, id: crypto.randomUUID() });
    const text = saved ? `Сейчас в дневнике: ${saved.status === 'done' ? 'выполнено' : saved.status === 'missed' ? 'пропущено' : 'самочувствие'}, сон ${saved.sleep ?? 'не указан'}, усталость ${saved.fatigue ?? 'не указана'}.` : 'В дневнике пока нет такой отметки.';
    dialog.querySelector('#entry-form')?.insertAdjacentHTML('afterbegin', `<div class="notice"><span>${esc(text)} Ниже сохранены ваши исправленные поля. Проверьте их перед повторным сохранением.</span></div>`);
  },
  onError: error => { if (error.code === 'session_changed') blockChangedSession(); },
});
const variants = createVariants({
  getState: () => state, dialogHeader, openDialog, closeDialog, toast,
  isBusy: () => mutationBusy, dialogOpen: () => dialog.open, mutate: saveMutation,
  onError: error => { if (error.code === 'session_changed') blockChangedSession(); },
});
const wellness = createWellness({
  getState: () => state, dialogHeader, openDialog, closeDialog, render, toast, navigate, profileDialog,
  isBusy: () => mutationBusy, mutate: saveMutation,
});
const nutrition = createNutrition({
  getState: () => state, dialogHeader, openDialog, closeDialog, render, toast, navigate, profileDialog,
  isBusy: () => mutationBusy, mutate: saveMutation, dialogOpen: () => dialog.open,
  onError: error => { if (error.code === 'session_changed') blockChangedSession(); },
});
const exerciseLibrary = createExerciseLibrary({ dialogHeader, openDialog, closeDialog, toast });
const account = createAccount({
  getState: () => state, dialogHeader, openDialog, closeDialog, toast,
  isBusy: () => mutationBusy, isReady: () => ready, dialogOpen: () => dialog.open,
  setBusy: value => { mutationBusy = value; }, beforeAuth: () => { stateEpoch++; },
  onError: blockChangedSession,
  signedIn: value => { resetPrivateViews(); adoptSession(value); applyState(value); ready = true; loadError = ''; closeDialog(true); render(); if (route === 'nutrition') void nutrition.load(); },
  signedOut: async () => {
    resetPrivateViews(); resetSessionBinding(); ready = false; state = emptyState(); closeDialog(true); render();
    try { const value = await request('/api/state'); applyState(value); ready = true; loadError = ''; }
    catch (error) { loadError = error.message; }
    render();
    if (route === 'nutrition' && ready) void nutrition.load();
  },
  recovered: () => { resetPrivateViews(); resetSessionBinding(); ready = false; state = emptyState(); loadError = ''; render(); },
});
function resetPrivateViews() { lastSuccess = null; staleError = ''; stateEpoch++; features.reset(); coach.reset(); variants.reset(); wellness.reset(); nutrition.reset(); exerciseLibrary.reset(); historyView = null; showNext = false; clearDraft(); }
function applyState(value) { state = value; lastSuccess = new Date(); staleError = '';  if (value.timeZone) setTimeZone(value.timeZone); }
function displayedPlan() { return historyView?.plan || state.plan; }

function routeFromHash() { const value = location.hash.slice(1); return Object.hasOwn(routes, value) ? value : 'overview'; }
function readDraft() {
  try { const raw = sessionStorage.getItem('rhythm-pending-entry'); return raw ? JSON.parse(raw) : null; }
  catch { return null; }
}
function saveDraft(entry) {
  pendingEntry = entry;
  pendingDraft = { entry, binding: state.sessionBinding };
  try { sessionStorage.setItem('rhythm-pending-entry', JSON.stringify(pendingDraft)); } catch { /* Dialog retains the draft if session storage is unavailable. */ }
}
function clearDraft() { pendingEntry = null; pendingDraft = null; try { sessionStorage.removeItem('rhythm-pending-entry'); } catch { /* No durable draft exists when storage is unavailable. */ } }
function blockChangedSession() {
  account.reset();
  lastSuccess = null; staleError = '';
  stateEpoch++;
  ready = false; state = emptyState(); features.reset(); coach.reset(); variants.reset(); wellness.reset(); nutrition.reset(); exerciseLibrary.reset(); historyView = null; clearDraft(); resetSessionBinding(); closeDialog(true); dialog.innerHTML = '';
  loadError = 'Сессия изменилась в другой вкладке. Выполните вход заново перед работой с данными.';
  render();
}
function toast(message) {
  clearTimeout(toastTimer);
  const element = document.querySelector('#toast'); element.textContent = message; element.hidden = false;
  toastTimer = setTimeout(() => { element.hidden = true; }, 4500);
}
function render() {
  const content = !ready ? `<section class="connection-state" role="status"><span class="wordmark">ритм<span>.</span></span><h1>${loadError ? 'Вернёмся к вашему ритму' : 'Загружаем ваше пространство'}</h1><p>${loadError ? 'Восстановите соединение или войдите в аккаунт, чтобы продолжить.' : 'Проверяем сохранённый план и дневник.'}</p></section>` : ({ overview: () => overview(state), week: () => week(state, showNext, historyView), journal: () => journal(state, journalFilter), insights: () => insights(state, progressDays, activityDays), about, activities: features.activities, wellness: wellness.view, nutrition: nutrition.view, exercises: exerciseLibrary.view, assistant: () => coach.view() + `<div id="rules-helper">${features.assistant()}</div>`, lab: () => lab(labResult, labBusy) })[route]();
  root.innerHTML = shell(state, route, content, { preview: miniPreview, error: loadError, stale: staleError, lastSuccess, draft: ready && Boolean(pendingEntry) });
  // On narrow screens the latest observations should be visible first.
  root.querySelectorAll('.history-chart.month').forEach(chart => { chart.scrollLeft = chart.scrollWidth; });
  document.title = `${routes[route].title} · Ритм`;
  if (telegram) route === 'overview' ? telegram.BackButton.hide() : telegram.BackButton.show();
}
function navigate(next) { if (route === next) return; location.hash = next; }
function haptic() { if (telegram?.isVersionAtLeast('6.1')) telegram.HapticFeedback.notificationOccurred('success'); }
function openDialog(content) {
  exerciseLibrary.disposeDialog();
  if (!dialog.open) dialogReturnFocus = document.activeElement;
  dialog.innerHTML = content;
  if (!dialog.open) dialog.showModal();
  const heading = dialog.querySelector('#dialog-title');
  if (heading) { heading.tabIndex = -1; heading.focus({ preventScroll: true }); }
}
function closeDialog(force = false) { if (dialog.open && (!mutationBusy || force)) { exerciseLibrary.disposeDialog(); dialog.close(); } }
function dialogHeader(title, subtitle = '') { return `<div class="dialog-header"><div><h2 id="dialog-title">${esc(title)}</h2>${subtitle ? `<p>${esc(subtitle)}</p>` : ''}</div><button type="button" class="icon-button" data-action="close" aria-label="Закрыть">${icon('close')}</button></div>`; }

function metricPresets(field, current, options) {
  return `<div class="metric-presets" role="group" aria-label="${field === 'sleep' ? 'Быстрый выбор сна' : 'Быстрый выбор усталости'}">${options.map(([value, label]) => `<button type="button" data-action="metric-preset" data-field="${field}" data-value="${value}" aria-pressed="${current === value}">${label}</button>`).join('')}</div>`;
}

function historyDialog(date) {
  const day = dailyHistory(state.entries, 30, state.today).find(item => item.date === date);
  if (!day) return;
  openDialog(`${dialogHeader(dateLabel(date), 'Ваши сохранённые отметки за день.')}<dl class="day-detail"><div><dt>${icon('moon')} Сон</dt><dd>${day.sleep == null ? 'Нет отметки' : `${day.sleep} ч`}</dd></div><div><dt>${icon('bolt')} Усталость</dt><dd>${day.fatigue == null ? 'Нет отметки' : `${day.fatigue} / 10`}</dd></div><div><dt>${icon('check')} Занятий выполнено</dt><dd>${day.completed}</dd></div></dl><p class="small-print">Если за день было несколько записей, показан последний известный сон и последняя известная усталость.</p><div class="dialog-actions"><button class="button outline" data-action="close">Закрыть просмотр</button>${date === (state.today || isoDate()) ? '<button class="button primary" data-action="checkin">Отметить сегодня</button>' : ''}</div>`);
}

function activityDialog(date) {
  const content = consistencyDayContent(state, date);
  if (content == null) return;
  openDialog(`${dialogHeader(dateLabel(date), 'Факты из сохранённого расписания, дневника и импорта.')}${content}<div class="dialog-actions"><button class="button outline" data-action="close">Закрыть просмотр</button>${date === state.today ? '<button class="button primary" data-action="checkin">Отметить сегодня</button>' : ''}</div>`);
}

function profileDialog() {
  const profile = state.profile || { name: '', goal: 'tone', experience: 'beginner', days: [0, 2, 4], minutes: 45 };
  openDialog(`${dialogHeader(state.profile ? 'Настроить свой ритм' : 'Всё начинается с вас', 'Несколько деталей — и у вашей недели появится свой ритм.')}<form id="profile-form">${state.profile ? '<div class="notice"><span>Изменения попадут в новое предложение на следующую неделю. Текущее расписание и уже принятый план сохранятся. Чтобы заменить принятый план, сначала отмените его на странице недели.</span></div>' : ''}
    <label class="field"><span class="field-label">Как к вам обращаться?</span><input name="name" type="text" maxlength="40" autocomplete="given-name" placeholder="Имя, если хотите" value="${esc(profile.name)}"></label>
    <label class="field"><span class="field-label">Что сейчас важнее?</span><select name="goal">${Object.entries(goals).map(([key, label]) => `<option value="${key}" ${profile.goal === key ? 'selected' : ''}>${label}</option>`).join('')}</select></label>
    <fieldset class="field" style="border:0;padding:0"><legend class="field-label">Ваш опыт</legend><div class="segmented">${[['beginner', 'Начинаю'], ['regular', 'Занимаюсь регулярно']].map(([value, label]) => `<label class="segment"><input type="radio" name="experience" value="${value}" ${profile.experience === value ? 'checked' : ''}><span>${label}</span></label>`).join('')}</div></fieldset>
    <fieldset class="field" style="border:0;padding:0"><legend class="field-label">В какие дни вам удобно?</legend><div class="day-picks">${shortDays.map((day, i) => `<label class="day-pick"><input type="checkbox" name="days" value="${i}" ${profile.days.includes(i) ? 'checked' : ''}><span>${day}</span></label>`).join('')}</div><p class="field-hint">Выберите от 1 до 5 дней. Оставим время на восстановление.</p></fieldset>
    <label class="field" for="minutes"><span class="range-head"><span class="field-label">Длительность занятия</span><output id="minutes-output">${profile.minutes} мин</output></span><input type="range" name="minutes" id="minutes" min="15" max="90" step="5" value="${profile.minutes}" aria-describedby="minutes-hint"><span class="range-labels"><span>15 мин</span><span>90 мин</span></span><p class="field-hint" id="minutes-hint">Базовая длительность. Предложение на следующую неделю может немного отличаться.</p></label>
    <div class="profile-preview" role="status" aria-live="polite"><span>${icon('calendar')}Ритм недели</span><strong id="profile-preview-total"></strong><p id="profile-preview-days"></p></div><div class="form-error" id="form-error" role="alert"></div><div class="dialog-actions"><button type="button" class="button outline" data-action="close">Пока не сейчас</button><button class="button primary" type="submit">${state.profile ? 'Сохранить настройки' : 'Создать мой план'} ${icon('arrow')}</button></div>
    <p class="small-print">Демонстрационный план активности. Пороги адаптации пока не прошли экспертную проверку.</p></form>`);
  updateProfilePreview();
}

function updateProfilePreview() {
  const form = dialog.querySelector('#profile-form');
  if (!form) return;
  const days = [...form.querySelectorAll('[name=days]:checked')].map(input => Number(input.value));
  const minutes = Number(form.elements.minutes.value);
  const valid = days.length >= 1 && days.length <= 5;
  form.querySelector('#profile-preview-total').textContent = valid ? `${days.length} × ${minutes} мин · ${days.length * minutes} мин / нед` : 'Выберите от 1 до 5 дней';
  form.querySelector('#profile-preview-days').textContent = valid ? `${days.map(day => shortDays[day]).join(' · ')}. В остальные дни — восстановление.` : 'Подберите расписание, в котором останется время на отдых.';
}

function checkinDialog(sessionID = null, mood = null, draft = null, editing = null) {
  if (!state.profile) { profileDialog(); return; }
  const today = state.today || isoDate();
  const sessions = [...(state.plan?.sessions || []), ...(historyView?.plan.sessions || []), ...(state.weeks || []).flatMap(w => w.plan.sessions)];
  activeSession = sessionID ? sessions.find(s => s.id === sessionID) : null;
  if (sessionID && !activeSession) { toast('Сохранённое расписание этого занятия не найдено.'); return; }
  if (activeSession && (activeSession.date > today || activeSession.date < (state.historyEditableFrom || ''))) { sessionDialog(activeSession); return; }
  const existing = activeSession ? state.entries.find(e => e.sessionId === activeSession.id) : state.entries.find(e => e.date === (editing?.date || today) && e.status === 'wellbeing');
  const values = draft || editing || existing || {};
  entryID = draft?.id || crypto.randomUUID();
  const fatigue = mood ?? values.fatigue;
  const chosenStatus = values.status || (activeSession ? 'done' : 'wellbeing');
  openDialog(`${dialogHeader(activeSession ? activeSession.title : 'Как прошёл ваш день?', `${dateLabel(activeSession?.date || values.date || today)}${activeSession ? ` · ${activeSession.detailsKnown === false ? 'длительность не сохранена' : activeSession.minutes + ' минут'}` : ' · маленькая пауза для себя'}`)}<form id="entry-form">
    ${activeSession ? `<fieldset class="field" style="border:0;padding:0"><legend class="field-label">Как прошло занятие?</legend><div class="segmented"><label class="segment"><input type="radio" name="status" value="done" ${chosenStatus === 'done' ? 'checked' : ''}><span>✓ Выполнил</span></label><label class="segment"><input type="radio" name="status" value="missed" ${chosenStatus === 'missed' ? 'checked' : ''}><span>Пропустил</span></label></div></fieldset><input type="hidden" name="date" value="${activeSession.date}">` : `<label class="field"><span class="field-label">День отметки</span><input type="date" name="date" required min="${state.historyEditableFrom || monday(today)}" ${editing ? 'readonly' : ''} max="${today}" value="${esc(values.date || today)}"></label>`}
    <label class="field"><span class="field-label">Сколько часов вы спали?</span><input type="number" name="sleep" min="0" max="24" step="0.5" inputmode="decimal" placeholder="Например, 7.5" value="${values.sleep != null ? esc(values.sleep) : ''}"><p class="field-hint">${existing ? 'Пустое поле сохраняет ранее указанный показатель.' : 'Можно оставить пустым. Только то, чем хочется поделиться.'}</p></label>
    ${metricPresets('sleep', values.sleep, [[6, '6 ч'], [7, '7 ч'], [8, '8 ч'], [9, '9 ч']])}
    <label class="field"><span class="field-label">Усталость сейчас</span><select name="fatigue"><option value="">Пока не буду указывать</option>${Array.from({ length: 10 }, (_, i) => i + 1).map(value => `<option value="${value}" ${value === fatigue ? 'selected' : ''}>${value} / 10${value === 1 ? ' — полон сил' : value === 5 ? ' — умеренная усталость' : value === 10 ? ' — очень устал' : ''}</option>`).join('')}</select></label>
    ${metricPresets('fatigue', fatigue, [[2, '2 · Полон сил'], [4, '4 · Хорошо'], [6, '6 · Средне'], [9, '9 · Устал']])}
    <p class="small-print">${activeSession ? 'Отметка обновит предложение на следующую неделю. Можно изменить её позже.' : 'Укажите хотя бы сон или усталость. Эти данные помогут объяснить следующий план.'}</p><div class="form-error" id="form-error" role="alert"></div><div class="dialog-actions"><button type="button" class="button outline" data-action="close">Отмена</button><button class="button primary" type="submit">Сохранить отметку ${icon('check')}</button></div></form>`);
}

function sessionDialog(session) {
  const demo = !state.profile;
  openDialog(`${dialogHeader(session.title, `${dateLabel(session.date)} · ${session.detailsKnown === false ? 'длительность не сохранена' : session.minutes + ' минут'}`)}<p class="panel-description">${demo ? 'Это занятие из примера недели. Создайте свой план, чтобы отмечать выполнение и получать адаптацию.' : session.date > (state.today || isoDate()) ? 'Занятие запланировано на будущий день. Отметить выполнение или пропуск можно в день занятия или позже.' : 'Это занятие из сохранённой истории. Исправлять отметки можно за последние 90 дней.'}</p><div class="notice"><span>Ритм планирует тип и длительность активности. Конкретные упражнения выбирайте из знакомой вам программы.</span></div><div class="dialog-actions"><button class="button ${demo ? 'primary' : 'outline'}" data-action="${demo ? 'profile' : 'close'}">${demo ? 'Создать мой план' : 'Понятно'} ${icon('arrow')}</button></div>`);
}

function settingsDialog() {
  openDialog(`${dialogHeader('Ваше пространство', state.identity === 'telegram' ? 'Вход через подтверждённую учётную запись Telegram.' : state.identity === 'account' ? 'История связана с аккаунтом. Вы можете войти с другого устройства.' : 'Гостевой дневник связан с этим браузером. Создайте аккаунт, чтобы сохранить доступ.')} ${state.accountsEnabled ? `<div class="settings-item account-settings"><div><h3>${state.identity === 'account' ? 'Мой аккаунт' : state.identity === 'telegram' ? 'Сеанс Telegram' : 'Сохранить свой ритм'}</h3><p>${state.identity === 'account' ? 'Пароль, резервный код и активные сеансы' : state.identity === 'telegram' ? 'Ваш Telegram-профиль хранится отдельно' : 'Регистрация сохранит текущий дневник'}</p></div><button class="button primary" data-action="${state.identity === 'account' ? 'account-manage' : state.identity === 'telegram' ? 'account-logout' : 'account-register'}">${state.identity === 'account' ? 'Открыть' : state.identity === 'telegram' ? 'Выйти' : 'Регистрация'}</button></div>${state.identity === 'guest' ? '<button class="button ghost" data-action="account-login">Уже есть аккаунт? Войти</button>' : ''}` : ''}<div class="settings-item"><div><h3>Цель и расписание</h3><p>${state.profile ? esc(goals[state.profile.goal]) : 'Пока не настроены'}</p></div><button class="button outline" data-action="profile">Настроить</button></div>
    ${dataUsageView(state)}
    <div class="settings-item"><div><h3>Помощь и возможности</h3><p>С чего начать и что умеет Ритм</p></div><button class="button outline" data-action="product-help">Открыть помощь</button></div>
    <div class="settings-item"><div><h3>Ритм на телефоне</h3><p>Добавить значок на главный экран</p></div><button class="button outline" data-action="install">Как установить</button></div>
    <div class="settings-item"><div><h3>Скачать мои данные</h3><p>Профиль, планы, дневник, активности, измерения и меню в JSON</p></div><button class="button outline" data-action="export" ${!state.profile ? 'disabled' : ''}>${icon('download')} Скачать</button></div>
    <div class="settings-item"><div><h3>Восстановить копию</h3><p>Перенести сохранённые данные в пустой профиль</p></div><button class="button outline" data-action="restore">Восстановить</button></div>
    <div class="settings-item"><div><h3>Напоминания</h3><p>Время занятий и календарь с уведомлениями</p></div><button class="button outline" data-action="reminders">Настроить</button></div>
    <div class="settings-item"><div><h3>Моё движение</h3><p>Импорт тренировок CSV и TCX</p></div><button class="button outline" data-action="activities">Открыть</button></div>
    <div class="settings-item"><div><h3>Техника упражнений</h3><p>Демонстрации, шаги и частые ошибки</p></div><button class="button outline" data-action="exercise-library">Смотреть</button></div>
    <div class="settings-item"><div><h3>Измерения</h3><p>История HRV, веса и состава тела</p></div><button class="button outline" data-action="wellness-open">Открыть</button></div>
    <div class="settings-item"><div><h3>Питание</h3><p>Идеи блюд, меню и список продуктов</p></div><button class="button outline" data-action="nutrition-open">Открыть</button></div>
    <div class="settings-item"><div><h3>Помощник по плану</h3><p>Ответы на основе вашего дневника</p></div><button class="button outline" data-action="assistant">Спросить</button></div>
    <div class="settings-item"><div><h3>Лаборатория PoC</h3><p>Проверка адаптации на примерах</p></div><button class="button outline" data-action="lab-open">Открыть</button></div>
    <div class="settings-item"><div><h3>Очистить дневник</h3><p>Удалить профиль и записи. Аккаунт, пароль и активные сеансы останутся.</p></div><button class="button danger" data-action="delete-confirm" ${!state.profile ? 'disabled' : ''}>Очистить дневник</button></div>
    <div class="settings-item"><div><h3>Как работает Ритм</h3><p>Правила, объяснения и границы PoC</p></div><button class="button outline" data-action="about">Подробнее</button></div>
    <p class="small-print">План рассчитывается на сервере по правилам. Передача контекста в AI возможна только после отдельного согласия на каждый запрос. Часовой пояс календаря: ${esc(state.timeZone || 'Europe/Moscow')}.</p>`);
}

function deleteDialog() {
  openDialog(`${dialogHeader('Очистить дневник?', 'Профиль, планы и записи будут удалены с сервера. Аккаунт, пароль, резервный код и активные сеансы останутся. Сначала можно скачать копию в настройках.')}<form id="delete-form"><div class="form-error" id="form-error" role="alert"></div><div class="dialog-actions"><button type="button" class="button outline" data-action="settings">Вернуться</button><button type="submit" class="button danger">Да, очистить дневник</button></div></form>`);
}

function downloadJSON(data, filename) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2) + '\n'], { type: 'application/json' }));
  const link = document.createElement('a'); link.href = url; link.download = filename; document.body.append(link); link.click(); link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function runLab(id = null) {
  if (labBusy) return;
  labBusy = true; render();
  const selected = id ? scenarios.filter(s => s.id === id) : scenarios;
  const results = []; let lastDecision = null;
  try {
    for (const scenario of selected) {
      const start = performance.now();
      const response = await request('/api/preview', scenarioPayload(scenario));
      const elapsed = performance.now() - start;
      const modes = Array.isArray(scenario.mode) ? scenario.mode : [scenario.mode];
      results.push({ id: scenario.id, title: scenario.title, pass: modes.includes(response.decision.mode) && response.nextPlan.totalMinutes === scenario.total, elapsed, expectedMode: scenario.mode, expectedTotalMinutes: scenario.total, actual: response });
      lastDecision = response.decision;
    }
    labResult = { executedAt: new Date().toISOString(), scope: 'Synthetic cases; actual HTTP responses; no diary writes; not a load test', selected: id, results, decision: id ? lastDecision : null };
    haptic();
  } catch (error) { toast(error.message); if (results.length) labResult = { executedAt: new Date().toISOString(), results, error: error.message }; }
  finally { labBusy = false; render(); document.querySelector('.lab-result')?.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'center' }); }
}

document.addEventListener('click', async event => {
  const button = event.target.closest('[data-action]');
  if (!button || button.disabled || mutationBusy) return;
  const action = button.dataset.action;
  if (await account.handleClick(action, button)) return;
  if (exerciseLibrary.handleClick(action, button)) return;
  if (!ready && ['profile', 'checkin', 'mood', 'session', 'draft'].includes(action)) { toast('Сначала нужно восстановить соединение с сервером.'); return; }
  if (await coach.handleClick(action, button)) return;
  if (await variants.handleClick(action, button)) return;
  if (await wellness.handleClick(action, button)) return;
  if (await nutrition.handleClick(action, button)) return;
  if (await features.handleClick(action, button)) return;
  switch (action) {
    case 'close': closeDialog(); break;
    case 'profile': profileDialog(); break;
    case 'exercise-library': closeDialog(); navigate('exercises'); break;
    case 'settings': settingsDialog(); break;
    case 'menu': openDialog(`${dialogHeader('Ваш Ритм', 'Движение, восстановление и забота о себе.')} ${sectionMenu(route)}`); break;
    case 'menu-route': event.preventDefault(); closeDialog(); navigate(button.dataset.route); break;
    case 'checkin': checkinDialog(); break;
    case 'mood': checkinDialog(null, Number(button.dataset.value)); break;
    case 'draft': checkinDialog(pendingEntry?.sessionId, pendingEntry?.fatigue, pendingEntry); break;
    case 'session': {
      const session = (displayedPlan() || samplePlan(state.today)).sessions.find(s => s.id === button.dataset.id);
      if (session) state.profile ? checkinDialog(session.id) : sessionDialog(session);
      break;
    }
    case 'week-current': showNext = false; historyView = null; render(); document.querySelector('[data-action=week-current]')?.focus(); break;
    case 'week-next': showNext = true; historyView = null; render(); document.querySelector('[data-action=week-next]')?.focus(); break;
    case 'journal-filter': journalFilter = button.dataset.filter; render(); document.querySelector(`[data-filter="${journalFilter}"]`)?.focus(); break;
    case 'entry-edit': {
      const entry = state.entries.find(item => item.id === button.dataset.id);
      if (entry) checkinDialog(entry.sessionId, null, null, entry);
      break;
    }
    case 'lab-open': closeDialog(); navigate('lab'); break;
    case 'history-period':
      progressDays = Number(button.dataset.days) === 30 ? 30 : 7;
      render();
      document.querySelector(`[data-action="history-period"][data-days="${progressDays}"]`)?.focus({ preventScroll: true });
      break;
    case 'history-day': historyDialog(button.dataset.id); break;
    case 'activity-period':
      activityDays = Number(button.dataset.days) === 90 ? 90 : 30;
      render();
      document.querySelector(`[data-action="activity-period"][data-days="${activityDays}"]`)?.focus({ preventScroll: true });
      break;
    case 'activity-day': activityDialog(button.dataset.id); break;
    case 'metric-preset': {
      const field = button.dataset.field;
      if (!['sleep', 'fatigue'].includes(field)) break;
      const input = dialog.querySelector(`[name="${field}"]`);
      if (input) { input.value = button.dataset.value; input.dispatchEvent(new Event('input', { bubbles: true })); }
      break;
    }
    case 'retry': if (ready) await refreshState(true); else await boot(); break;
    case 'rules-helper': document.querySelector('#rules-helper')?.scrollIntoView({ block: 'start' }); document.querySelector('#assistant-form input')?.focus({ preventScroll: true }); break;
    case 'product-help': openDialog(`${dialogHeader('Помощь и возможности')}${productHelpView(state)}`); break;
    case 'install': openDialog(`${dialogHeader('Ритм на телефоне')}${installView()}`); break;
    case 'install-prompt': await promptInstall(); break;
    case 'about': closeDialog(); navigate('about'); break;
    case 'delete-confirm': deleteDialog(); break;
    case 'export':
      button.disabled = true;
      try { downloadJSON(await request('/api/export'), `rhythm-${isoDate()}.json`); toast('Экспорт готов.'); }
      catch (error) { if (error.code === 'session_changed') blockChangedSession(); else toast(error.message); }
      finally { button.disabled = false; }
      break;
    case 'lab-all': await runLab(); break;
    case 'lab-one': await runLab(button.dataset.id); break;
    case 'lab-download': if (labResult) downloadJSON(labResult, `rhythm-scenarios-${isoDate()}.json`); break;
  }
});

document.addEventListener('input', event => {
  account.handleInput(event);
  coach.handleInput(event);
  exerciseLibrary.handleInput(event);
  if (event.target.id === 'minutes') document.querySelector('#minutes-output').textContent = `${event.target.value} мин`;
  if (event.target.closest('#profile-form') && ['days', 'minutes'].includes(event.target.name)) updateProfilePreview();
  if (['sleep', 'fatigue'].includes(event.target.name)) {
    dialog.querySelectorAll(`[data-action="metric-preset"][data-field="${event.target.name}"]`).forEach(button => {
      button.setAttribute('aria-pressed', event.target.value !== '' && Number(event.target.value) === Number(button.dataset.value));
    });
  }
});
document.addEventListener('submit', async event => {
  // Cancel native navigation before the first async controller dispatch. This
  // also covers requestSubmit() used for an explicitly confirmed replacement.
  if (root.contains(event.target) || dialog.contains(event.target)) event.preventDefault();
  if (await account.handleSubmit(event)) return;
  if (exerciseLibrary.handleSubmit(event)) return;
  if (await coach.handleSubmit(event)) return;
  if (await variants.handleSubmit(event)) return;
  if (await wellness.handleSubmit(event)) return;
  if (await nutrition.handleSubmit(event)) return;
  if (await features.handleSubmit(event)) return;
  if (!['profile-form', 'entry-form', 'delete-form'].includes(event.target.id)) return;
  event.preventDefault();
  if (mutationBusy) return;
  const form = event.target, submit = form.querySelector('[type=submit]'), errorBox = form.querySelector('#form-error'), formData = new FormData(form);
  errorBox.textContent = '';
  let entry = null;
  if (form.id === 'profile-form') {
    const days = formData.getAll('days').map(Number);
    if (days.length < 1 || days.length > 5) { errorBox.textContent = 'Выберите от 1 до 5 дней для занятий.'; return; }
  }
  if (form.id === 'entry-form') {
    entry = { id: entryID, date: formData.get('date'), status: formData.get('status') || 'wellbeing' };
    if (activeSession) entry.sessionId = activeSession.id;
    if (formData.get('sleep') !== '') entry.sleep = Number(formData.get('sleep'));
    if (formData.get('fatigue') !== '') entry.fatigue = Number(formData.get('fatigue'));
    if (entry.status === 'wellbeing' && entry.sleep == null && entry.fatigue == null) { errorBox.textContent = 'Укажите сон или усталость, чтобы сохранить отметку.'; return; }
  }
  mutationBusy = true;
  const controls = [...form.elements].filter(element => 'disabled' in element).map(element => [element, element.disabled]);
  for (const [element] of controls) element.disabled = true;
  form.setAttribute('aria-busy', 'true');
  const oldContent = submit.innerHTML; submit.textContent = 'Сохраняем…';
  try {
    if (form.id === 'profile-form') {
      applyState(await request('/api/profile', { revision: state.revision, profile: { name: formData.get('name').trim(), goal: formData.get('goal'), experience: formData.get('experience'), days: formData.getAll('days').map(Number), minutes: Number(formData.get('minutes')) } }));
      clearDraft(); showNext = false; historyView = null; toast('Настройки сохранены. Уже созданные недели сохраняют своё расписание.');
    } else if (form.id === 'entry-form') {
      applyState(await request('/api/entries', { revision: state.revision, entry })); clearDraft(); toast('Отметка сохранена. Предложение на следующую неделю обновлено.');
    } else { applyState(await request('/api/data', { revision: state.revision }, 'DELETE')); features.reset(); coach.reset(); variants.reset(); wellness.reset(); nutrition.reset(); exerciseLibrary.reset(); historyView = null; clearDraft(); showNext = false; toast('Профиль и дневник удалены.'); }
    loadError = ''; closeDialog(true); render(); haptic(); if (historyView) await features.loadWeek(historyView.weekStart);
  } catch (error) {
    if (error.code === 'stale_session') return;
    if (error.code === 'session_changed') { blockChangedSession(); return; }
    errorBox.textContent = error.message;
    if (entry && (error.status === 0 || error.status >= 500)) { saveDraft(entry); render(); errorBox.textContent += ' Отметка осталась черновиком. Нажмите сохранить ещё раз после восстановления связи.'; }
    if (error instanceof APIError && error.status === 409) {
      try {
        applyState(await request('/api/state'));
        if (error.code === 'entry_conflict' && entry) {
          entryID = crypto.randomUUID();
          saveDraft({ ...entry, id: entryID });
          errorBox.textContent = 'Исходная отметка уже была принята сервером. Дневник обновлён. Чтобы сохранить исправленные поля как новую версию, нажмите «Сохранить отметку» ещё раз.';
        } else { errorBox.textContent = 'Данные изменились в другой вкладке. Текущие данные обновлены; проверьте свою запись и повторите сохранение.'; }
        render();
      } catch (readError) { if (readError.code === 'session_changed') blockChangedSession(); else errorBox.textContent += ' Не удалось загрузить свежие данные.'; }
    }
  } finally {
    mutationBusy = false;
    for (const [element, disabled] of controls) element.disabled = disabled;
    form.removeAttribute('aria-busy'); submit.innerHTML = oldContent;
  }
});

window.addEventListener('hashchange', () => { route = routeFromHash(); if (route !== 'week') historyView = null; closeDialog(); render(); if (route === 'nutrition' && ready) void nutrition.load(); window.scrollTo({ top: 0 }); document.querySelector('#main')?.focus({ preventScroll: true }); });
dialog.addEventListener('click', event => { if (event.target === dialog) { const box = dialog.getBoundingClientRect(); if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) closeDialog(); } });
dialog.addEventListener('cancel', event => { if (mutationBusy) event.preventDefault(); });

async function boot() {
  account.reset();
  lastSuccess = null; staleError = '';
  const epoch = ++stateEpoch;
  ready = false;
  state = emptyState(); features.reset(); coach.reset(); variants.reset(); wellness.reset(); nutrition.reset(); exerciseLibrary.reset(); historyView = null;
  loadError = '';
  resetSessionBinding();
  closeDialog();
  render();
  try {
    telegramContext ||= await initializeTelegram(() => { if (dialog.open) closeDialog(); else navigate('overview'); }, () => { if (ready) render(); });
    if (epoch !== stateEpoch) return;
    telegram = telegramContext.app; miniPreview = telegramContext.preview;
    if (telegram?.initData) {
      const value = await request('/api/auth/telegram', { initData: telegram.initData });
      if (epoch !== stateEpoch) return;
      applyState(value);
      // Trust only the authenticated server response, never initDataUnsafe.
      route = routeFromHash();
    } else {
      const value = await request('/api/state');
      if (epoch !== stateEpoch) return;
      applyState(value);
    }
    if (pendingDraft && pendingDraft.binding !== state.sessionBinding) clearDraft();
    ready = true;
  } catch (error) { if (epoch !== stateEpoch || error.code === 'stale_session') return; loadError = error.message; }
  render();
  if (route === 'nutrition' && ready) void nutrition.load();
}
async function saveMutation(path, body, method, message) {
  if (mutationBusy || !ready) return false;
  mutationBusy = true;
  const controls = [...dialog.querySelectorAll('button,input,select')].map(element => [element, element.disabled]);
  controls.forEach(([element]) => { element.disabled = true; });
  dialog.setAttribute('aria-busy','true');
  const errorBox = dialog.querySelector('#form-error');
  if (errorBox) errorBox.textContent = '';
  try {
    applyState(await request(path, body, method));
    closeDialog(true); loadError = ''; render(); toast(message); haptic();
    if (historyView) await features.loadWeek(historyView.weekStart);
    return true;
  } catch (error) {
    if (error.code === 'stale_session') return false;
    if (error.code === 'session_changed') { blockChangedSession(); return false; }
    if (errorBox) errorBox.textContent = error.message; else toast(error.message);
    if (error.status === 409) {
      try { applyState(await request('/api/state')); render(); if (errorBox) errorBox.textContent += ' Данные обновлены. Закройте окно и проверьте новый вариант перед повтором.'; }
      catch (readError) { if (readError.code === 'session_changed') blockChangedSession(); }
    }
    return false;
  } finally {
    mutationBusy = false; controls.forEach(([element, disabled]) => { element.disabled = disabled; }); dialog.removeAttribute('aria-busy');
  }
}

async function refreshState(manual = false) {
  if (!ready || refreshing || document.hidden || (staleError && !manual)) return;
  if (mutationBusy || dialog.open) { refreshPending = true; return; }
  refreshing = true; refreshPending = false;
  const epoch = stateEpoch;
  try {
    const next = await request('/api/state');
    if (epoch !== stateEpoch || !ready || next.revision < state.revision) return;
    // A form may have opened while the read was in flight. Do not change its
    // revision or snapshot behind the user's draft.
    if (mutationBusy || dialog.open) { refreshPending = true; return; }
    const changed = Boolean(staleError) || next.revision !== state.revision || next.today !== state.today;
    applyState(next);
    if (changed) { if (historyView) await features.loadWeek(historyView.weekStart); else render(); }
  } catch (error) {
    if (error.code === 'session_changed') blockChangedSession();
    else if (error.code !== 'stale_session' && epoch === stateEpoch) { staleError = 'Не удалось обновить данные. Показана последняя полученная версия. Для сохранения изменений нужно соединение.'; render(); }
  }
  finally { refreshing = false; }
}
function startLifecycle() {
  clearInterval(lifecycleTimer);
  lifecycleTimer = setInterval(() => {
    if (ready && !document.hidden && (state.today !== isoDate() || refreshPending)) void refreshState();
    if (ready) features.tick();
  }, 15000);
}
document.addEventListener('change', event => { exerciseLibrary.handleChange(event); if (event.target.id === 'week-select') void features.loadWeek(event.target.value); wellness.handleChange(event); nutrition.handleChange(event); });
document.addEventListener('visibilitychange', () => { if (!document.hidden) void refreshState(); });
window.addEventListener('focus', () => { void refreshState(); });
window.addEventListener('pagehide', () => { clearInterval(lifecycleTimer); });
window.addEventListener('pageshow', () => { startLifecycle(); void refreshState(); });
dialog.addEventListener('close', () => {
  if (!dialog.open) exerciseLibrary.disposeDialog();
  // Native dialogs restore focus themselves. Repair only a removed trigger;
  // the delayed close event must not steal focus from a later keyboard action.
  if (document.activeElement === document.body || dialog.contains(document.activeElement)) {
    if (dialogReturnFocus?.isConnected) dialogReturnFocus.focus({ preventScroll: true });
    else document.querySelector('#main')?.focus({ preventScroll: true });
  }
  if (refreshPending) setTimeout(() => { void refreshState(); }, 0);
});
initializeInstall(() => toast('Доступна новая версия. Она откроется при следующем запуске приложения.'));
startLifecycle();
boot();
