import { addDays } from './format.js';

export const hrvWindows = Object.freeze({ five_minutes: '5 минут', overnight: 'Ночное измерение', day: '24-часовая запись / агрегат' });
export const bodyMethods = Object.freeze({ bia: 'Биоимпеданс', dxa: 'DXA', skinfold: 'Калипер', other: 'Другой метод' });

function validDate(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T12:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}
const validValue = (value, min, max) => Number.isFinite(value) && value >= min && value <= max;

export function measurementGroups(entries, kind, today, days = 30) {
  if (!validDate(today) || !Array.isArray(entries) || ![0, 30, 90].includes(days)) return [];
  const first = days ? addDays(today, 1 - days) : '', groups = new Map();
  for (const entry of entries) {
    if (!entry || !validDate(entry.date) || entry.date < first || entry.date > today || typeof entry.source !== 'string' || !entry.source.trim()) continue;
    let key, label;
    if (kind === 'hrv') {
      if (!['rmssd', 'sdnn'].includes(entry.metric) || !Object.hasOwn(hrvWindows, entry.window) || !validValue(entry.valueMs, Number.MIN_VALUE, 500)) continue;
      key = JSON.stringify([entry.metric, entry.window, entry.source]);
      label = `${entry.metric.toUpperCase()} · ${hrvWindows[entry.window]} · ${entry.source}`;
    } else if (kind === 'body') {
      if (!Object.hasOwn(bodyMethods, entry.method)) continue;
      if (entry.weightKg != null && !validValue(entry.weightKg, 20, 400) || entry.bodyFatPercent != null && !validValue(entry.bodyFatPercent, Number.MIN_VALUE, 75)) continue;
      if (entry.weightKg == null && entry.bodyFatPercent == null) continue;
      key = JSON.stringify([entry.method, entry.source]);
      label = `${bodyMethods[entry.method]} · ${entry.source}`;
    } else continue;
    if (!groups.has(key)) groups.set(key, { key, label, entries: [] });
    groups.get(key).entries.push({ ...entry });
  }
  return [...groups.values()].map(group => ({ ...group, entries: group.entries.sort((a, b) => a.date.localeCompare(b.date)) }))
    .sort((a, b) => b.entries.at(-1).date.localeCompare(a.entries.at(-1).date) || a.label.localeCompare(b.label, 'ru'));
}

export function observations(entries, field) {
  return entries.filter(entry => validDate(entry.date) && Number.isFinite(entry[field]))
    .map(entry => ({ date: entry.date, value: entry[field] })).sort((a, b) => a.date.localeCompare(b.date));
}

export function trend(points) {
  const first = points[0] || null, latest = points.at(-1) || null;
  return { count: points.length, first, latest, delta: points.length > 1 ? latest.value - first.value : null };
}

export function bodyComposition(entries) {
  const complete = entries.filter(entry => validDate(entry.date) && validValue(entry.weightKg, 20, 400) && validValue(entry.bodyFatPercent, Number.MIN_VALUE, 75))
    .sort((a, b) => a.date.localeCompare(b.date)).at(-1);
  if (!complete) return null;
  const fatKg = complete.weightKg * complete.bodyFatPercent / 100;
  return { date: complete.date, fatKg, otherKg: complete.weightKg - fatKg };
}
