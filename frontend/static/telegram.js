export async function initializeTelegram(onBack, onTheme) {
  const isMini = location.pathname.startsWith('/mini') || location.hash.includes('tgWebAppData');
  if (!isMini && !window.Telegram?.WebApp?.initData) return { app: null, preview: false };
  if (!window.Telegram?.WebApp) {
    await new Promise((resolve, reject) => {
      const script = document.createElement('script');
      const timeout = setTimeout(() => { script.remove(); reject(new Error('Telegram SDK не загрузился. Попробуйте открыть приложение ещё раз.')); }, 8000);
      script.src = 'https://telegram.org/js/telegram-web-app.js?64';
      script.onload = () => { clearTimeout(timeout); resolve(); };
      script.onerror = () => { clearTimeout(timeout); reject(new Error('Не удалось подключиться к Telegram.')); };
      document.head.append(script);
    });
  }
  const app = window.Telegram?.WebApp;
  if (!app?.initData) {
    if (location.hash.includes('tgWebAppData')) throw new Error('Telegram не передал данные для входа. Откройте Mini App из меню бота ещё раз.');
    return { app: null, preview: true };
  }
  document.body.classList.add('telegram');
  const applyTheme = () => {
    document.documentElement.dataset.theme = app.colorScheme === 'dark' ? 'dark' : 'light';
    onTheme?.();
  };
  applyTheme();
  app.onEvent('themeChanged', applyTheme);
  app.BackButton.onClick(onBack);
  app.ready();
  app.expand();
  if (app.isVersionAtLeast('6.1')) app.setHeaderColor(app.colorScheme === 'dark' ? '#222521' : '#f6f5f0');
  return { app, preview: false };
}
