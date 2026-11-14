import { addDays } from './format.js';

function validDate(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T12:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}

function validRun(workout) {
  return workout?.kind === 'run' && validDate(workout.date)
    && Number.isFinite(workout.minutes) && workout.minutes > 0 && workout.minutes <= 1440;
}

function knownDistance(workout) {
  return Number.isFinite(workout.distanceMeters) && workout.distanceMeters >= 0 && workout.distanceMeters <= 1000000;
}

// A missing or explicitly zero distance never produces a pace. Duration is the
// imported duration; the file does not establish moving time or physiological effort.
export function runPace(workout) {
  if (!validRun(workout) || !knownDistance(workout) || workout.distanceMeters === 0) return null;
  const pace = workout.minutes / (workout.distanceMeters / 1000);
  return Number.isFinite(pace) && pace > 0 ? pace : null;
}

// today is the server's calendar date. A missing date must not silently use the
// device clock, which may belong to another timezone or be incorrectly set.
export function runningMetrics(workouts = [], today, days = 30) {
  if (!Number.isInteger(days) || days < 1 || days > 366) throw new RangeError('Invalid running statistics period');
  const through = validDate(today) ? today : null;
  const summary = {
    days, from: through ? addDays(through, 1 - days) : null, through,
    totalRuns: 0, totalMinutes: 0,
    knownDistanceMeters: null, knownDistanceRuns: 0, missingDistanceRuns: 0,
    paceMinutesPerKm: null, paceRuns: 0,
  };
  if (!through || !Array.isArray(workouts)) return summary;
  let paceMinutes = 0, paceDistance = 0;
  for (const workout of workouts) {
    if (!validRun(workout) || workout.date < summary.from || workout.date > through) continue;
    summary.totalRuns++;
    summary.totalMinutes += workout.minutes;
    if (!knownDistance(workout)) {
      summary.missingDistanceRuns++;
      continue;
    }
    summary.knownDistanceRuns++;
    summary.knownDistanceMeters = (summary.knownDistanceMeters ?? 0) + workout.distanceMeters;
    if (workout.distanceMeters > 0) {
      paceMinutes += workout.minutes;
      paceDistance += workout.distanceMeters;
      summary.paceRuns++;
    }
  }
  if (paceDistance > 0) {
    const pace = paceMinutes / (paceDistance / 1000);
    if (Number.isFinite(pace) && pace > 0) summary.paceMinutesPerKm = pace;
  }
  return summary;
}

export function formatPace(value) {
  if (!Number.isFinite(value) || value <= 0) return '—';
  const seconds = Math.round(value * 60);
  if (!Number.isSafeInteger(seconds)) return '—';
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}
