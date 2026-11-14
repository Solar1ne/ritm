import { request, invalidatePendingRequests } from './api.js';
import { escapeHTML as esc } from './format.js';
import { icon } from './icons.js';

const errorBox = '<div id="form-error" class="form-error" role="alert"></div>';
const loginField = value => `<label class="field"><span class="field-label">Логин</span><input name="login" aria-label="Логин" type="text" autocomplete="username" autocapitalize="none" spellcheck="false" minlength="3" maxlength="32" value="${esc(value || '')}" placeholder="anna.ritm" required><span class="field-hint">3–32 латинских буквы, цифры, точка, дефис или подчёркивание.</span></label>`;
const passwordField = (name, label, fresh = false) => `<div class="field"><label class="field-label" for="auth-${name}">${label}</label><div class="password-field"><input id="auth-${name}" name="${name}" type="password" autocomplete="${fresh ? 'new-password' : 'current-password'}" ${fresh ? 'minlength="15"' : ''} maxlength="256" required><button class="password-toggle" type="button" data-action="account-password-toggle" aria-label="Показать ${label.toLowerCase()}" aria-pressed="false">Показать</button></div>${fresh ? '<p class="field-hint">15–128 символов. Подойдёт фраза из нескольких слов.</p>' : ''}</div>`;

export function createAccount(ctx) {
  const state = ctx.getState;
  let generation = 0, busy = false, info = null, shownCode = '', codeLogin = '', afterCode = 'account', formSnapshot = null;
  function reset() { generation++; busy = false; info = null; shownCode = ''; codeLogin = ''; formSnapshot = null; }
  function capture() { formSnapshot = { binding: state().sessionBinding, revision: state().revision }; }
  function openForm(mode, login = '') {
    shownCode = ''; capture();
    const register = mode === 'register', recover = mode === 'recover';
    const hasGuestData = state().identity === 'guest' && Boolean(state().profile);
    const title = register ? 'Ваш ритм — с вами' : recover ? 'Вернуть доступ' : 'С возвращением';
    const subtitle = register ? 'Создайте аккаунт, чтобы открыть свою историю на другом устройстве.' : recover ? 'Нужны логин, резервный код и новый пароль.' : 'Войдите, чтобы продолжить свой план и дневник.';
    ctx.openDialog(`${ctx.dialogHeader(title, subtitle)}<div class="auth-kicker">${icon('shield')} Личное пространство Ритма</div><form id="account-${mode}-form" class="auth-form">${loginField(login)}${recover ? '<label class="field"><span class="field-label">Резервный код</span><input type="text" name="recoveryCode" aria-label="Резервный код" autocomplete="off" autocapitalize="characters" spellcheck="false" maxlength="64" placeholder="XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX" required></label>' : ''}${passwordField(recover ? 'newPassword' : 'password', recover ? 'Новый пароль' : 'Пароль', register || recover)}${register || recover ? passwordField('confirmPassword', 'Повторите пароль', true) : ''}
      ${register ? `<label class="consent-row"><input name="keepGuestData" type="checkbox" required><span>${hasGuestData ? 'Привязать мой текущий план, дневник и измерения к этому аккаунту.' : 'Создать аккаунт для этого личного пространства.'}</span></label><p class="field-hint">После регистрации вы получите резервный код восстановления. Сохраните его отдельно от пароля.</p>` : !recover && hasGuestData ? '<div class="notice"><span>Вход откроет историю выбранного аккаунта. Текущий гостевой дневник автоматически не переносится.</span></div><button class="button ghost" type="button" data-action="export">Скачать гостевой дневник</button><label class="consent-row"><input type="checkbox" name="switchConfirmed" required><span>Перейти в аккаунт без переноса гостевого дневника</span></label>' : ''}
      ${recover ? '<p class="field-hint">Использованный код перестанет действовать, старые сеансы завершатся. После восстановления войдите с новым паролем.</p>' : ''}${errorBox}<div class="dialog-actions"><button type="submit" class="button primary auth-submit">${register ? 'Создать аккаунт' : recover ? 'Восстановить доступ' : 'Войти'} ${icon('arrow')}</button></div></form>
      <div class="auth-links">${register ? '<button class="button ghost" data-action="account-login">Уже есть аккаунт? Войти</button>' : `<button class="button ghost" data-action="account-register">Создать аккаунт</button><button class="button ghost" data-action="${recover ? 'account-login' : 'account-recover'}">${recover ? 'Вернуться ко входу' : 'Забыли пароль?'}</button>`}</div>${!ctx.isReady() ? '<button class="button ghost auth-guest" data-action="account-guest">Открыть новое гостевое пространство</button>' : ''}`);
  }
  function recoveryScreen(code, login, next = 'account') {
    shownCode = code; codeLogin = login; afterCode = next;
    ctx.openDialog(`${ctx.dialogHeader('Сохраните резервный код', 'Он поможет вернуть доступ, если вы забудете пароль.')}<div class="recovery-card"><span class="eyebrow">Ваш ключ восстановления</span><code>${esc(code)}</code><p>Код показывается один раз. Не отправляйте его другим людям.</p></div><button type="button" class="button outline auth-download" data-action="account-code-download">${icon('download')} Скачать код</button><p class="small-print">Если этот экран потеряется, войдите с паролем и получите новый код в настройках аккаунта.</p><form id="account-code-saved-form"><label class="consent-row"><input type="checkbox" name="saved" required><span>Я сохранил резервный код в надёжном месте</span></label><div class="dialog-actions"><button type="submit" class="button primary">${next === 'login' ? 'Перейти ко входу' : 'Продолжить'} ${icon('arrow')}</button></div></form>`);
  }
  async function showAccount() {
    if (state().identity !== 'account') { openForm('login'); return; }
    const id = ++generation, binding = state().sessionBinding;
    ctx.openDialog(`${ctx.dialogHeader('Мой аккаунт')}<p role="status">Загружаем сведения о входе…</p>`);
    try {
      const value = await request('/api/account');
      if (id !== generation || binding !== state().sessionBinding || !ctx.dialogOpen()) return;
      info = value;
      ctx.openDialog(`${ctx.dialogHeader('Мой аккаунт', `Вы вошли как ${value.login}`)}<div class="account-summary">${icon('shield')}<div><strong>История привязана к аккаунту</strong><p>Войдите с этим логином на другом устройстве, чтобы продолжить.</p></div></div><div class="settings-item"><div><h3>Пароль</h3><p>Смена завершит старые сеансы и обновит резервный код.</p></div><button class="button outline" data-action="account-password">Изменить</button></div><div class="settings-item"><div><h3>Резервный код</h3><p>Новый код заменит предыдущий.</p></div><button class="button outline" data-action="account-recovery-code">Обновить</button></div><h3 class="account-sessions-title">Активные сеансы · ${value.sessions.length}</h3><ul class="account-sessions">${value.sessions.map(session => `<li><span>${session.current ? 'Этот сеанс' : 'Другой сеанс'}</span><time>${esc(new Date(session.createdAt).toLocaleString('ru-RU', { day:'numeric', month:'short', hour:'2-digit', minute:'2-digit' }))}</time></li>`).join('')}</ul>${value.sessions.length > 1 ? '<button class="button outline" data-action="account-logout-others">Завершить другие сеансы</button>' : ''}<p class="small-print">Сеанс действует до 30 дней. Удаление дневника в настройках сохраняет аккаунт.</p><div class="settings-item"><div><h3>Удалить аккаунт</h3><p>Навсегда удалить аккаунт и весь дневник, завершить все сеансы.</p></div><button type="button" class="button outline" data-action="account-delete">Удалить аккаунт</button></div><div class="dialog-actions"><button class="button outline" data-action="settings">К настройкам</button><button class="button primary" data-action="account-logout">Выйти</button></div>`);
    } catch (error) {
      if (id !== generation || !ctx.dialogOpen()) return;
      if (error.code === 'session_changed') ctx.onError(error);
      else ctx.openDialog(`${ctx.dialogHeader('Не удалось загрузить аккаунт')}<p class="form-error" role="alert">${esc(error.message)}</p><button class="button outline" data-action="account-manage">Повторить</button>`);
    }
  }
  function credentialForm(mode) {
    capture();
    ctx.openDialog(`${ctx.dialogHeader(mode === 'password' ? 'Новый пароль' : 'Новый резервный код', mode === 'password' ? 'Все старые сеансы будут завершены. Этот браузер получит новый сеанс.' : 'Прежний резервный код перестанет действовать.')}<form id="account-${mode}-form"><input type="hidden" name="login" value="${esc(info?.login || '')}" autocomplete="username">${passwordField('password', 'Текущий пароль')}${mode === 'password' ? passwordField('newPassword', 'Новый пароль', true) + passwordField('confirmPassword', 'Повторите пароль', true) : ''}${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="account-manage">Назад</button><button type="submit" class="button primary">${mode === 'password' ? 'Сменить пароль' : 'Получить новый код'}</button></div></form>`);
  }
  function deletionForm(message = '') {
    if (state().identity !== 'account') return;
    capture();
    const value = state();
    ctx.openDialog(`${ctx.dialogHeader('Удалить аккаунт навсегда?', 'Это действие нельзя отменить.')}<p>Аккаунт ${esc(info?.login || '')} и все его данные будут удалены: профиль, планы, дневник, тренировки, измерения и выбранные блюда. Резервный код перестанет работать, все сеансы на других устройствах завершатся.</p><p>В текущем дневнике: отметок — ${value.entries?.length || 0}, тренировок — ${value.workouts?.length || 0}. Ранее скачанные вами файлы останутся у вас.</p>${message ? `<p class="notice" role="status">${esc(message)}</p>` : ''}<form id="account-delete-form"><input type="hidden" name="login" value="${esc(info?.login || '')}" autocomplete="username">${passwordField('password', 'Текущий пароль')}<label class="consent-row"><input type="checkbox" name="confirmDelete" required><span>Я понимаю, что аккаунт и все данные будут удалены навсегда</span></label>${errorBox}<div class="dialog-actions"><button type="button" class="button outline" data-action="account-manage">Отмена</button><button type="submit" class="button primary">Удалить аккаунт навсегда</button></div></form>`);
  }
  async function handleClick(action, button) {
    if (!action.startsWith('account-')) { generation++; shownCode = ''; return false; }
    if (busy || ctx.isBusy()) return true;
    if (action === 'account-password-toggle') {
      const input = button.parentElement.querySelector('input');
      const visible = input.type === 'password'; input.type = visible ? 'text' : 'password';
      button.textContent = visible ? 'Скрыть' : 'Показать'; button.setAttribute('aria-pressed', String(visible)); button.setAttribute('aria-label', visible ? 'Скрыть пароль' : 'Показать пароль');
      return true;
    }
    if (action === 'account-login') { generation++; openForm('login'); }
    if (action === 'account-register') {
      if (!ctx.isReady() || state().identity !== 'guest') { ctx.toast('Для регистрации откройте новое гостевое пространство или войдите в существующий аккаунт.'); return true; }
      generation++; openForm('register');
    }
    if (action === 'account-recover') { generation++; openForm('recover'); }
    if (action === 'account-manage') await showAccount();
    if (action === 'account-password') credentialForm('password');
    if (action === 'account-recovery-code') credentialForm('recovery-code');
    if (action === 'account-delete') deletionForm();
    if (action === 'account-code-download' && shownCode) {
      const file = new Blob([`Ритм — резервный код восстановления\nЛогин: ${codeLogin}\nКод: ${shownCode}\n\nХраните отдельно от пароля. Код заменяется после использования или обновления.\n`], { type:'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(file), link = document.createElement('a'); link.href=url; link.download='rhythm-recovery-code.txt'; link.click(); setTimeout(() => URL.revokeObjectURL(url),1000);
      ctx.toast('Резервный код сохранён в файл.');
    }
    if (['account-logout','account-logout-others','account-guest'].includes(action)) {
      capture();
      const others = action === 'account-logout-others';
      ctx.openDialog(`${ctx.dialogHeader(others ? 'Завершить другие сеансы?' : 'Выйти из пространства?', others ? 'Этот браузер останется в аккаунте.' : state().identity === 'account' ? 'История останется в аккаунте. Вернуться можно по логину и паролю.' : state().identity === 'telegram' ? 'Сеанс завершится. Новый запуск из Telegram снова подтвердит вашу учётную запись.' : 'Гостевая история доступна по текущей сессии. Сохраните экспорт, если она вам нужна.')}<form id="${others ? 'account-logout-others-form' : 'account-logout-form'}">${errorBox}<div class="dialog-actions"><button class="button outline" type="button" data-action="close">Отмена</button><button class="button primary" type="submit">${others ? 'Завершить сеансы' : 'Выйти и открыть гостевой режим'}</button></div></form>`);
    }
    return true;
  }
  async function handleSubmit(event) {
    const form = event.target;
    if (!form.id.startsWith('account-')) return false;
    event.preventDefault();
    if (busy || ctx.isBusy()) return true;
    if (form.id === 'account-code-saved-form') {
      const next = afterCode, login = codeLogin; shownCode = ''; codeLogin = '';
      if (next === 'login') openForm('login', login); else { ctx.closeDialog(true); ctx.toast('Аккаунт готов. Ваши данные сохранены.'); }
      return true;
    }
    const data = new FormData(form), box = form.querySelector('#form-error'); box.textContent = '';
    const mode = form.id.replace('account-','').replace('-form','');
    const password = String(data.get('password') || ''), newPassword = String(data.get('newPassword') || '');
    const login = String(data.get('login') || info?.login || '').trim();
    if (['register','recover','password'].includes(mode) && data.get('confirmPassword') !== (mode === 'register' ? password : newPassword)) { box.textContent = 'Пароли не совпадают. Проверьте повтор.'; return true; }
    if (mode === 'delete' && !data.has('confirmDelete')) { box.textContent = 'Подтвердите, что понимаете последствия удаления аккаунта.'; return true; }
    if (['register','password','recovery-code','logout','logout-others','delete'].includes(mode) && formSnapshot?.binding !== state().sessionBinding) { box.textContent = 'Сессия изменилась. Откройте форму заново.'; return true; }
    if (mode === 'delete' && (state().identity !== 'account' || !password || !formSnapshot)) { box.textContent = 'Откройте удаление аккаунта заново и введите текущий пароль.'; return true; }
    const controls = [...form.querySelectorAll('input,button')].map(element => [element,element.disabled]);
    controls.forEach(([element]) => { element.disabled=true; }); form.setAttribute('aria-busy','true');
    const id = generation;
    busy=true; ctx.setBusy(true);
    invalidatePendingRequests(); ctx.beforeAuth();
    try {
      if (mode === 'delete') {
        const result = await request('/api/account', {password,revision:formSnapshot.revision,confirmDelete:true}, 'DELETE');
        if (id !== generation) return true;
        if (result.accountDeleted !== true) throw new Error('Сервер не подтвердил удаление аккаунта. Проверьте состояние аккаунта.');
        shownCode=''; codeLogin=''; info=null; formSnapshot=null;
        await ctx.signedOut();
        ctx.toast('Аккаунт и все его данные удалены. Открыто новое гостевое пространство.');
      } else if (mode === 'logout' || mode === 'logout-others') {
        await request(`/api/auth/${mode}`, {});
        if (id !== generation) return true;
        if (mode === 'logout') { await ctx.signedOut(); shownCode=''; info=null; ctx.toast('Вы вышли. Открыто новое гостевое пространство.'); }
        else { ctx.closeDialog(true); ctx.toast('Другие сеансы завершены.'); }
      } else {
        const body = mode === 'register' ? { login,password,revision:formSnapshot.revision,keepGuestData:data.has('keepGuestData') } : mode === 'login' ? {login,password} : mode === 'recover' ? {login,recoveryCode:String(data.get('recoveryCode') || ''),newPassword} : mode === 'password' ? {password,newPassword} : {password};
        const result = await request(`/api/auth/${mode}`,body);
        if (id !== generation) return true;
        if (mode === 'recover') { ctx.recovered(); recoveryScreen(result.recoveryCode,login,'login'); }
        else if (mode === 'recovery-code') recoveryScreen(result.recoveryCode,login);
        else {
          const { recoveryCode,...next } = result;
          ctx.signedIn(next);
          if (recoveryCode) recoveryScreen(recoveryCode,login); else ctx.toast('Вы вошли. Ваша история на месте.');
        }
      }
    } catch (error) {
      if (id !== generation || error.code === 'stale_session') return true;
      if (error.code === 'session_changed') ctx.onError(error);
      else if (mode === 'delete' && error.status === 409) {
        formSnapshot = null;
        try {
          const current = await request('/api/state');
          if (id !== generation) return true;
          ctx.signedIn(current);
          deletionForm('Дневник изменился. Показаны обновлённые данные. Проверьте их, введите пароль и подтвердите удаление заново.');
        } catch (readError) {
          if (id !== generation || readError.code === 'stale_session') return true;
          if (readError.code === 'session_changed') ctx.onError(readError);
          else if (form.isConnected) box.textContent = 'Дневник изменился. Не удалось загрузить актуальные данные. Обновите страницу и проверьте их перед удалением.';
        }
      }
      else if (form.isConnected) { box.textContent = error.message; if (error.status === 0 && ['register','password','recover','recovery-code'].includes(mode)) box.textContent += ' Если действие успело сохраниться, войдите с новым паролем. Резервный код можно обновить в аккаунте.'; }
    } finally {
      if (id === generation) busy=false;
      ctx.setBusy(false); controls.forEach(([element,disabled])=>{element.disabled=disabled;}); form.removeAttribute('aria-busy');
    }
    return true;
  }
  function handleInput(event) {
    const form = event.target.closest?.('form');
    if (form?.id.startsWith('account-')) {
      const box = form.querySelector('#form-error'); if (box) box.textContent = '';
    }
  }
  return { handleClick,handleSubmit,handleInput,reset };
}
