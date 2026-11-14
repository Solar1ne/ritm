export class APIError extends Error {
  constructor(message, status, code) { super(message); this.status = status; this.code = code; }
}
let sessionBinding = '';
let sessionGeneration = 0;
export function resetSessionBinding() { sessionBinding = ''; sessionGeneration++; }
export function invalidatePendingRequests() { sessionGeneration++; }
export function adoptSession(value) { resetSessionBinding(); sessionBinding = value.sessionBinding || ''; }
export async function request(path, body, method = 'POST', type = 'json') {
  const generation = sessionGeneration;
  const controller = new AbortController();
  // The AI server deadline is capped at 30s; ordinary diary calls stay at 10s.
  const timer = setTimeout(() => controller.abort(), path === '/api/ai/respond' ? 45000 : 10000);
  try {
    const headers = body === undefined ? {} : { 'Content-Type': 'application/json' };
    if (sessionBinding) headers['X-Rhythm-Session'] = sessionBinding;
    const response = await fetch(path, { method: body === undefined ? 'GET' : method, credentials: 'same-origin', headers, body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal });
    if (generation !== sessionGeneration) throw new APIError('Ответ относится к предыдущей сессии.', 0, 'stale_session');
    if (response.ok && type === 'blob') {
      const payload = await response.blob();
      if (generation !== sessionGeneration) throw new APIError('Ответ относится к предыдущей сессии.', 0, 'stale_session');
      return payload;
    }
    const payload = await response.json();
    if (generation !== sessionGeneration) throw new APIError('Ответ относится к предыдущей сессии.', 0, 'stale_session');
    if (!response.ok) throw new APIError(payload.error?.message || 'Не удалось выполнить запрос.', response.status, response.status === 401 && payload.error?.code === 'session' ? 'session_changed' : payload.error?.code);
    if (typeof payload.sessionBinding === 'string') sessionBinding = payload.sessionBinding;
    return payload;
  } catch (error) {
    if (error instanceof APIError) throw error;
    throw new APIError('Нет связи с сервером. Проверьте подключение и повторите попытку.', 0, 'network');
  } finally { clearTimeout(timer); }
}
