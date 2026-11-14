import { activityCalendar, previousWeekSummary } from './consistency.js';
import { escapeHTML as esc, dateLabel, weekLabel, shortDays, plural } from './format.js';

const statuses = {
  done: { label: 'Выполнено', symbol: '✓' },
  missed: { label: 'Пропущено', symbol: '×' },
  unlogged: { label: 'Нет отметки', symbol: '○' },
  planned: { label: 'По плану сегодня', symbol: '→' },
  rest: { label: 'Отдых по плану', symbol: '–' },
  unknown: { label: 'Расписание неизвестно', symbol: '?' },
  activity: { label: 'Импортированная активность', symbol: '↗' },
  checkin: { label: 'Самочувствие', symbol: '◇' },
};
const kinds = { run: 'Бег', walk: 'Прогулка', cycling: 'Велосипед', swimming: 'Плавание', strength: 'Силовая тренировка', mobility: 'Подвижность', other: 'Активность' };
const number = value => value.toLocaleString('ru-RU', { maximumFractionDigits: 1 });
const observationCount = count => `${count} ${plural(count, ['отметка', 'отметки', 'отметок'])}`;

function dayLabel(day) {
  const detail = [dateLabel(day.date), statuses[day.status].label];
  if (day.workouts.length && day.status !== 'activity') detail.push(`импортированных активностей: ${day.workouts.length}`);
  if (day.sleep != null) detail.push(`сон ${number(day.sleep)} ч`);
  if (day.fatigue != null) detail.push(`усталость ${number(day.fatigue)} из 10`);
  return detail.join(' · ');
}

function calendarCard(state, days) {
  const history = activityCalendar(state, days);
  const first = history[0], last = history.at(-1);
  const leading = first ? (new Date(`${first.date}T12:00:00Z`).getUTCDay() + 6) % 7 : 0;
  const cells = history.map((day, index) => {
    const status = statuses[day.status], showMonth = index === 0 || day.date.endsWith('-01');
    return `<button type="button" class="consistency-day consistency-status-${day.status}" data-action="activity-day" data-id="${esc(day.date)}" aria-label="${esc(dayLabel(day))}"${day.date === state.today ? ' aria-current="date"' : ''}>
      <span class="consistency-day-number">${Number(day.date.slice(-2))}</span><span class="consistency-day-symbol" aria-hidden="true">${status.symbol}</span>${showMonth ? `<span class="consistency-day-month" aria-hidden="true">${dateLabel(day.date, { month: 'short' })}</span>` : ''}${day.workouts.length && day.status !== 'activity' ? '<span class="consistency-day-import" aria-hidden="true">↗</span>' : ''}</button>`;
  }).join('');
  return `<section class="consistency-card consistency-calendar" aria-labelledby="consistency-calendar-title">
    <div class="consistency-heading"><div><p class="consistency-kicker">ЗАНЯТИЯ И ОТДЫХ</p><h2 id="consistency-calendar-title">Календарь активности</h2><p>${first ? `${dateLabel(first.date)} — ${dateLabel(last.date)}` : 'История появится после получения даты сервера.'}</p></div>
      <div class="consistency-period" role="group" aria-label="Период календаря активности">${[30, 90].map(value => `<button type="button" data-action="activity-period" data-days="${value}" aria-pressed="${history.length === value}">${value} дней</button>`).join('')}</div></div>
    <p class="consistency-description">Нажмите на день, чтобы посмотреть план, отметки и импортированные активности.</p>
    ${history.length ? `<div class="consistency-calendar-scroll" tabindex="0" role="group" aria-label="Календарь по дням недели, при необходимости прокрутите по горизонтали"><div class="consistency-calendar-grid"><div class="consistency-weekdays" aria-hidden="true">${shortDays.map(day => `<span>${day}</span>`).join('')}</div>${'<span class="consistency-calendar-gap" aria-hidden="true"></span>'.repeat(leading)}${cells}</div></div>` : ''}
    <ul class="consistency-legend" aria-label="Обозначения календаря">${Object.entries(statuses).map(([key, status]) => `<li><span class="consistency-legend-symbol consistency-status-${key}" aria-hidden="true">${status.symbol}</span><span>${status.label}</span></li>`).join('')}</ul>
    <p class="consistency-note">Дни отдыха не требуют тренировки. «Нет отметки» не означает пропуск. Импорт показан отдельно и сам по себе не подтверждает выполнение плана.</p>
  </section>`;
}

function sleepRecap(summary) {
  return `<div class="consistency-sleep"><span>Средний сон</span><strong>${summary.averageSleep == null ? 'Нет данных' : `${number(summary.averageSleep)} <small>ч</small>`}</strong><span>${observationCount(summary.sleepDays)}${summary.sleepDays ? ' · дни без данных не учитываются' : ''}</span></div>`;
}

function recapCard(state) {
  const summary = previousWeekSummary(state);
  const counts = [['Выполнено', summary.done, 'done'], ['Пропущено', summary.missed, 'missed'], ['Нет отметки', summary.unlogged, 'unlogged']];
  let content = '<p class="consistency-description">За прошлую неделю расписание не сохранено. Отметки и импортированные активности доступны в календаре.</p>';
  if (summary.hasSchedule) {
    content = `${!summary.completeSchedule ? '<p class="consistency-notice">Расписание сохранено частично. Здесь только известные занятия; полноту недели оценить нельзя.</p>' : `<p class="consistency-description">В сохранённом плане: ${summary.scheduled} ${plural(summary.scheduled, ['занятие', 'занятия', 'занятий'])}. Дни отдыха не входят в число занятий.</p>`}
      <dl class="consistency-counts">${counts.map(([label, count, status]) => `<div><dt><span class="consistency-count-symbol consistency-status-${status}" aria-hidden="true">${statuses[status].symbol}</span>${label}</dt><dd>${count}</dd></div>`).join('')}</dl>
      <div class="consistency-minutes">${summary.knownMinutes > 0 ? `<strong>${number(summary.knownMinutes)} плановых минут</strong><span>в выполненных занятиях с известной длительностью</span>` : '<strong>Плановые минуты не подсчитаны</strong><span>Нет выполненных занятий с известной длительностью.</span>'}${summary.unknownDurationCompletions ? `<span>Выполненных занятий с неизвестной длительностью: ${summary.unknownDurationCompletions}.</span>` : ''}</div>
      <p class="consistency-note">Плановые минуты описывают расписание, а не измеренное время тренировки. Импортированные минуты не прибавляются к этому итогу.</p>`;
  }
  return `<section class="consistency-card consistency-recap" aria-labelledby="consistency-recap-title"><div class="consistency-heading"><div><p class="consistency-kicker">НЕДЕЛЯ ЗАВЕРШЕНА</p><h2 id="consistency-recap-title">Прошлая неделя</h2><p>${summary.weekStart ? weekLabel(summary.weekStart) : 'История появится после получения даты сервера.'}</p></div></div>${content}${sleepRecap(summary)}<p class="consistency-note">Это обзор записанных фактов, без оценки восстановления или медицинских выводов.</p></section>`;
}

export function consistencyView(state, days = 30) {
  return `<div class="consistency-layout">${calendarCard(state, days)}${recapCard(state)}</div>`;
}

export function consistencyDayContent(state, date) {
  const day = activityCalendar(state, 90).find(value => value.date === date);
  if (!day) return null;
  const plan = day.sessions.length ? `<ul class="consistency-detail-list">${day.sessions.map(session => `<li><strong>${esc(session.title || kinds[session.kind] || 'Занятие')}</strong><span>${session.detailsKnown === false ? 'Плановая длительность неизвестна' : `${number(session.minutes)} мин по плану`} · ${session.status === 'done' ? 'Выполнено' : session.status === 'missed' ? 'Пропущено' : day.date === state.today ? 'По плану сегодня' : 'Нет отметки'}</span>${session.variantId ? `<span>С учётом выбранной замены занятия${session.originalTitle ? `. Исходно: ${esc(session.originalTitle)}` : ''}</span>` : ''}</li>`).join('')}</ul>` : `<p class="consistency-description">${day.scheduleKnown ? 'Отдых по плану. Занятий на этот день нет.' : 'Расписание неизвестно. Сохранённого полного плана на этот день нет.'}</p>`;
  const workouts = day.workouts.length ? `<ul class="consistency-detail-list">${day.workouts.map(workout => `<li><strong>${esc(kinds[workout.kind] || 'Активность')}</strong><span>${number(workout.minutes)} мин из файла · ${esc(workout.source || 'Источник не указан')}${workout.distanceMeters == null ? '' : ` · ${number(workout.distanceMeters / 1000)} км`}</span><span>${workout.sessionId ? 'Связана с занятием плана' : 'Отдельная активность'}</span></li>`).join('')}</ul>` : '<p class="consistency-description">Импортированных активностей за этот день нет.</p>';
  return `<div class="consistency-day-detail"><section aria-label="Расписание дня"><h3>План дня</h3>${plan}</section><section aria-label="Самочувствие за день"><h3>Самочувствие</h3><dl class="consistency-wellbeing"><div><dt>Сон</dt><dd>${day.sleep == null ? 'Нет данных' : `${number(day.sleep)} ч`}</dd></div><div><dt>Усталость</dt><dd>${day.fatigue == null ? 'Нет данных' : `${number(day.fatigue)} из 10`}</dd></div></dl></section><section aria-label="Импорт за день"><h3>Импортированные активности</h3>${workouts}<p class="consistency-note">Время из файла показано отдельно от плановой длительности. Отметки выполнения не суммируются с импортом повторно.</p></section></div>`;
}
