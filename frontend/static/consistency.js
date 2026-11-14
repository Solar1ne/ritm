import { addDays, isoDate, monday } from './format.js';

const sessionKey = (id, date) => `${id}\0${date}`;

function effectiveSession(original, adjustment, result) {
  const session = { ...original, status: result || 'planned' };
  // A legacy duration cannot become known through a later overlay. The API
  // validates variants; presentation only needs their saved duration/metadata.
  if (adjustment && original.detailsKnown !== false && Number.isFinite(adjustment.minutes)
    && adjustment.minutes >= 15 && adjustment.minutes <= original.minutes) {
    session.originalMinutes = original.originalMinutes ?? original.minutes;
    session.originalTitle = original.title;
    session.title = 'Занятие с заменой';
    session.minutes = adjustment.minutes;
    session.variantId = adjustment.variantId;
    session.environment = adjustment.environment;
    session.lowImpact = adjustment.lowImpact;
  }
  return session;
}

function calendarRange(state, from, length, today) {
  const days = Array.from({ length }, (_, index) => ({
    date: addDays(from, index), status: 'unknown', scheduleKnown: false,
    sessions: [], workouts: [], logged: false, sleep: null, fatigue: null, completed: 0, missed: 0,
  }));
  const byDate = new Map(days.map(day => [day.date, day]));
  const weeks = new Map((state.weeks || []).map(week => [week.weekStart, week]));
  const results = new Map();
  for (const entry of state.entries || []) {
    const day = byDate.get(entry.date);
    if (!day || entry.date > today) continue;
    day.logged = true;
    if (entry.sleep != null) day.sleep = entry.sleep;
    if (entry.fatigue != null) day.fatigue = entry.fatigue;
    if (entry.sessionId && (entry.status === 'done' || entry.status === 'missed')) {
      results.set(sessionKey(entry.sessionId, entry.date), entry.status);
    }
  }
  for (const workout of state.workouts || []) {
    const day = byDate.get(workout.date);
    if (day && workout.date <= today) day.workouts.push({ ...workout });
  }
  const adjustments = new Map((state.sessionAdjustments || []).map(item => [sessionKey(item.sessionId, item.date), item]));
  const currentWeekStart = monday(today);
  for (const day of days) {
    if (day.date > today) continue;
    const weekStart = monday(day.date), saved = weeks.get(weekStart);
    day.scheduleKnown = Boolean(saved?.profile && (saved.source === 'baseline' || saved.source === 'accepted'));
    const plan = saved?.plan || (!saved && weekStart === currentWeekStart && state.plan?.weekStart === currentWeekStart ? state.plan : null);
    const unique = new Map();
    for (const original of plan?.sessions || []) {
      if (!original.id || original.date !== day.date) continue;
      const key = sessionKey(original.id, original.date);
      unique.set(key, effectiveSession(original, adjustments.get(key), results.get(key)));
    }
    day.sessions = [...unique.values()];
    day.completed = day.sessions.filter(session => session.status === 'done').length;
    day.missed = day.sessions.filter(session => session.status === 'missed').length;
    day.status = day.completed ? 'done' : day.missed ? 'missed' : day.workouts.length ? 'activity'
      : day.sessions.length ? day.date === today ? 'planned' : 'unlogged'
        : day.scheduleKnown ? 'rest' : day.logged ? 'checkin' : 'unknown';
  }
  return days;
}

// A calendar contains persisted facts and schedules only. Missing schedules do
// not become historical rest days using the current profile's preferred days.
export function activityCalendar(state, days = 30) {
  const today = state.today || isoDate(), length = days === 90 ? 90 : 30;
  return calendarRange(state, addDays(today, 1 - length), length, today);
}

// A recap always covers a closed calendar week; current-week observations must
// not be compared with an entire past week. Minutes describe the saved plan.
export function previousWeekSummary(state) {
  const today = state.today || isoDate();
  const weekStart = addDays(state.currentWeekStart || monday(today), -7);
  const days = calendarRange(state, weekStart, 7, today);
  const sessions = days.flatMap(day => day.sessions);
  const completed = sessions.filter(session => session.status === 'done');
  const known = completed.filter(session => session.detailsKnown !== false && Number.isFinite(session.minutes) && session.minutes > 0);
  const sleep = days.filter(day => day.sleep != null), fatigue = days.filter(day => day.fatigue != null);
  return {
    weekStart, weekEnd: addDays(weekStart, 6),
    hasSchedule: (state.weeks || []).some(week => week.weekStart === weekStart),
    completeSchedule: days.every(day => day.scheduleKnown),
    scheduled: sessions.length, done: completed.length,
    missed: sessions.filter(session => session.status === 'missed').length,
    unlogged: sessions.filter(session => session.status === 'planned').length,
    knownMinutes: known.reduce((sum, session) => sum + session.minutes, 0),
    unknownDurationCompletions: completed.length - known.length,
    sleepDays: sleep.length,
    averageSleep: sleep.length ? sleep.reduce((sum, day) => sum + day.sleep, 0) / sleep.length : null,
    fatigueDays: fatigue.length,
    averageFatigue: fatigue.length ? fatigue.reduce((sum, day) => sum + day.fatigue, 0) / fatigue.length : null,
    loggedDays: days.filter(day => day.logged).length,
    days,
  };
}
