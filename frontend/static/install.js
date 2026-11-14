let deferredPrompt = null;
export function installView() {
  return `<section class="install-guide"><p>Добавьте Ритм на главный экран, чтобы открывать его как приложение. Для дневника и сохранения записей нужно подключение к серверу.</p>${deferredPrompt ? '<button class="button primary" data-action="install-prompt">Установить Ритм</button>' : ''}<h3>Android · Chrome</h3><p>Откройте меню браузера ⋮ и выберите «Добавить на главный экран» или «Установить приложение».</p><h3>iPhone · Safari</h3><p>Откройте Ритм в Safari, нажмите «Поделиться», затем «На экран Домой» и «Добавить».</p><p class="small-print">Если пункта нет, откройте сайт в обычном браузере вместо встроенного окна Telegram. Значок не сохраняет дневник на телефоне для работы без сети.</p></section>`;
}
export async function promptInstall() {
  const prompt = deferredPrompt;
  if (!prompt) return false;
  deferredPrompt = null;
  try { await prompt.prompt(); await prompt.userChoice; return true; }
  catch { return false; }
}
export function initializeInstall(onUpdate, environment = globalThis) {
  environment.addEventListener('beforeinstallprompt', event => { event.preventDefault(); deferredPrompt = event; });
  environment.addEventListener('appinstalled', () => { deferredPrompt = null; });
  if (!environment.navigator?.serviceWorker || !environment.isSecureContext) return;
  environment.navigator.serviceWorker.register('/service-worker.js').then(registration => {
    if (registration.waiting) onUpdate();
    registration.addEventListener('updatefound', () => {
      const worker = registration.installing;
      worker?.addEventListener('statechange', () => {
        if (worker.state === 'installed' && environment.navigator.serviceWorker.controller) onUpdate();
      });
    });
  }).catch(() => { /* Installation is optional; the online application remains available. */ });
}
