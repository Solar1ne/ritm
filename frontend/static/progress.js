import { addDays, isoDate } from './format.js';

// Read-only presentation of persisted facts. Missing days stay unknown; a
// second observation on the same date must not count as another night's sleep.
export function dailyHistory(entries = [], days = 7, today = isoDate()) {
  const length = days === 30 ? 30 : 7;
  const history = Array.from({ length }, (_, index) => ({
    date: addDays(today, index - length + 1), logged: false,
    sleep: null, fatigue: null, completed: 0,
  }));
  const byDate = new Map(history.map(day => [day.date, day]));
  const sessions = new Map();
  for (const entry of entries) {
    const day = byDate.get(entry.date);
    if (!day) continue;
    day.logged = true;
    if (entry.sleep != null) day.sleep = entry.sleep;
    if (entry.fatigue != null) day.fatigue = entry.fatigue;
    if (entry.sessionId) sessions.set(entry.sessionId, entry);
  }
  for (const entry of sessions.values()) {
    if (entry.status === 'done') byDate.get(entry.date).completed++;
  }
  return history;
}

export function historySummary(history) {
  const sleep = history.filter(day => day.sleep != null);
  const fatigue = history.filter(day => day.fatigue != null);
  return {
    loggedDays: history.filter(day => day.logged).length,
    completed: history.reduce((sum, day) => sum + day.completed, 0),
    sleepDays: sleep.length,
    fatigueDays: fatigue.length,
    averageSleep: sleep.length ? sleep.reduce((sum, day) => sum + day.sleep, 0) / sleep.length : null,
    averageFatigue: fatigue.length ? fatigue.reduce((sum, day) => sum + day.fatigue, 0) / fatigue.length : null,
  };
}
