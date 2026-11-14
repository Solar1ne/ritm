import { escapeHTML as esc } from './format.js';

// Only collection counts are known here. Serialized client state is not a
// measurement of the server's storage budget or of free disk space.
const collections = [
  { key: 'entries', label: 'Отметки дневника', route: 'journal', action: 'Открыть дневник' },
  { key: 'workouts', label: 'Импортированные тренировки', route: 'activities', action: 'Открыть активности' },
  { key: 'hrvEntries', label: 'Измерения HRV', route: 'wellness', action: 'Открыть измерения' },
  { key: 'bodyEntries', label: 'Вес и состав тела', route: 'wellness', action: 'Открыть измерения' },
  { key: 'mealSelections', label: 'Блюда в меню', route: 'nutrition', action: 'Открыть меню' },
  { key: 'weeks', label: 'Сохранённые недели', route: 'week', action: 'Открыть план' },
];

export function dataUsageView(state = {}) {
  const rows = collections.map(({ key, label, route, action }) => {
    const count = Array.isArray(state[key]) ? state[key].length : 0;
    const limit = state.limits?.[key];
    const known = Number.isSafeInteger(limit) && limit > 0;
    const status = !known ? 'unknown' : count >= limit ? 'full' : count / limit >= 0.8 ? 'near' : 'available';
    const warning = status === 'full' ? 'Лимит достигнут.' : status === 'near' ? 'Приближается лимит.' : '';
    const guidance = key === 'weeks'
      ? 'Отдельное удаление сохранённых недель не поддерживается. Скачайте резервную копию.'
      : 'Можно просмотреть записи и удалить ненужные по одной в соответствующем разделе.';
    return `<li data-collection="${key}" data-capacity-state="${status}"><div class="section-title"><strong>${esc(label)}</strong><span>${known ? `${count} из ${limit}` : `${count} · Лимит не получен`}</span></div>${known ? `<meter min="0" max="${limit}" value="${Math.min(count, limit)}" aria-label="${esc(label)}: ${count} из ${limit}">${count} из ${limit}</meter>` : ''}${warning ? `<p class="field-hint">${warning} ${guidance}</p>` : ''}<a class="text-link" href="#${route}" data-action="menu-route" data-route="${route}">${esc(action)}</a></li>`;
  }).join('');
  return `<section class="data-usage" aria-labelledby="data-usage-title"><h3 id="data-usage-title">Объём данных</h3><p class="panel-description">Количество сохранённых записей и лимиты каждого раздела. Это не размер файлов и не свободное место на сервере.</p><ul class="data-usage-list">${rows}</ul><p class="small-print">Экспорт не освобождает место. Сначала сохраните копию, затем удаляйте только ненужные записи. Автоматического удаления нет. Изменение записей подчиняется обычным правилам раздела.</p><button type="button" class="button outline" data-action="export">Скачать копию данных</button></section>`;
}
