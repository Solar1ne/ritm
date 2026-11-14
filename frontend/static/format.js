export const dayNames = ['Понедельник', 'Вторник', 'Среда', 'Четверг', 'Пятница', 'Суббота', 'Воскресенье'];
export const shortDays = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];
export const goals = { tone: 'Чувствовать себя лучше', strength: 'Стать сильнее', endurance: 'Больше выносливости' };
export function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}
let calendarTimeZone = 'Europe/Moscow';
export function setTimeZone(value) {
  // Only accept a real IANA zone received from the server.
  new Intl.DateTimeFormat('en', { timeZone: value }).format();
  calendarTimeZone = value;
}
export function timeZone() { return calendarTimeZone; }
export function isoDate(date = new Date()) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: calendarTimeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(date);
  return ['year', 'month', 'day'].map(key => parts.find(p => p.type === key).value).join('-');
}
export function addDays(iso, count) {
  const date = new Date(`${iso}T12:00:00Z`);
  date.setUTCDate(date.getUTCDate() + count);
  return date.toISOString().slice(0, 10);
}
export function monday(iso = isoDate()) {
  const weekday = new Date(`${iso}T12:00:00Z`).getUTCDay();
  return addDays(iso, -((weekday + 6) % 7));
}
export function dateLabel(iso, options = { day: 'numeric', month: 'long' }) {
  return new Intl.DateTimeFormat('ru-RU', { ...options, timeZone: 'UTC' }).format(new Date(`${iso}T12:00:00Z`));
}
export function weekLabel(weekStart) {
  return `${dateLabel(weekStart, { day: 'numeric' })} — ${dateLabel(addDays(weekStart, 6), { day: 'numeric', month: 'long' })}`;
}
export function plural(number, forms) {
  const mod10 = number % 10, mod100 = number % 100;
  return forms[mod100 >= 11 && mod100 <= 14 ? 2 : mod10 === 1 ? 0 : mod10 >= 2 && mod10 <= 4 ? 1 : 2];
}
export function samplePlan(today = isoDate()) {
  const weekStart = monday(today);
  return { weekStart, totalMinutes: 135, sessions: [0, 2, 4].map((day, i) => ({ id: addDays(weekStart, day), date: addDays(weekStart, day), day, kind: i === 1 ? 'walk' : 'strength', title: i === 1 ? 'Лёгкая активность' : 'Силовая тренировка', minutes: 45, status: 'planned' })) };
}
export function latestWellbeing(entries = [], field = null) {
  return entries.reduce((latest, entry) => (field ? entry[field] != null : entry.sleep != null || entry.fatigue != null) && (!latest || entry.date >= latest.date) ? entry : latest, undefined);
}
export function sessionIcon(kind) { return kind === 'strength' ? 'strength' : kind === 'run' ? 'run' : 'leaf'; }
