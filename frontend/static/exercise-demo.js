// Original local vector demonstrations. Coordinates are a schematic side view,
// not a model for prescribing an individual's range of motion.
const standing = {
  shoulder: [190, 78], hip: [190, 139], head: [190, 52],
  elbow: [203, 112], wrist: [205, 148], knee: [199, 186], ankle: [193, 233],
  elbowFar: [178, 112], wristFar: [180, 148], kneeFar: [175, 186], ankleFar: [169, 233],
};
const pose = (changes) => ({ ...standing, ...changes });
const definitions = {
  squat: { name: 'Приседание', phases: ['Исходное положение', 'Таз назад и вниз'], poses: [pose({}), pose({ shoulder: [177, 111], hip: [153, 164], head: [187, 88], elbow: [216, 109], wrist: [252, 110], elbowFar: [210, 117], wristFar: [246, 118], knee: [205, 187], kneeFar: [179, 188] })] },
  'reverse-lunge': { name: 'Выпад назад', note: 'Показана одна сторона; затем смените сторону.', phases: ['Стойка', 'Шаг назад', 'Опускание таза'], poses: [
    pose({ knee: [211, 186], ankle: [211, 233], kneeFar: [188, 186], ankleFar: [188, 233] }),
    pose({ shoulder: [182, 80], hip: [182, 141], head: [182, 54], elbow: [195, 114], wrist: [197, 150], elbowFar: [170, 114], wristFar: [172, 150], knee: [207, 187], ankle: [211, 233], kneeFar: [141, 182], ankleFar: [94, 214], heelFar: [89, 219], toeFar: [99, 238] }),
    pose({ shoulder: [168, 105], hip: [168, 166], head: [168, 79], elbow: [183, 139], wrist: [181, 175], elbowFar: [154, 139], wristFar: [151, 174], knee: [211, 187], ankle: [211, 233], kneeFar: [146, 218], ankleFar: [94, 214], heelFar: [89, 219], toeFar: [99, 238] }),
  ] },
  'glute-bridge': { name: 'Ягодичный мост', floor: true, phases: ['Таз на коврике', 'Подъём таза'], poses: [pose({ head: [81, 221], shoulder: [108, 226], hip: [169, 227], elbow: [144, 229], wrist: [180, 230], elbowFar: [139, 232], wristFar: [174, 233], knee: [210, 182], ankle: [251, 233], kneeFar: [202, 188], ankleFar: [243, 233] }), pose({ head: [81, 221], shoulder: [108, 226], hip: [166, 201], elbow: [144, 229], wrist: [180, 230], elbowFar: [139, 232], wristFar: [174, 233], knee: [211, 182], ankle: [251, 233], kneeFar: [203, 188], ankleFar: [243, 233] })] },
  'push-up': { name: 'Отжимание от пола', floor: true, phases: ['Упор на ладонях', 'Опускание корпуса'], poses: [pose({ head: [119, 123], shoulder: [137, 147], hip: [202, 172], knee: [256, 194], ankle: [305, 216], heel: [310, 217], toe: [320, 238], elbow: [130, 190], wrist: [128, 233], elbowFar: [142, 190], wristFar: [140, 233], kneeFar: [250, 198], ankleFar: [297, 219], heelFar: [302, 222], toeFar: [312, 238] }), pose({ head: [109, 183], shoulder: [136, 192], hip: [204, 205], knee: [258, 215], ankle: [307, 224], heel: [312, 227], toe: [320, 238], elbow: [164, 218], wrist: [128, 233], elbowFar: [175, 218], wristFar: [140, 233], kneeFar: [250, 218], ankleFar: [299, 227], heelFar: [304, 230], toeFar: [312, 238] })] },
  'wall-push-up': { name: 'Отжимание от стены', wall: true, phases: ['Ладони на стене', 'Приближение к стене'], poses: [pose({ shoulder: [208, 91], hip: [186, 146], head: [218, 67], knee: [168, 189], ankle: [151, 233], kneeFar: [157, 189], ankleFar: [140, 233], elbow: [248, 92], wrist: [289, 92], elbowFar: [249, 102], wristFar: [289, 102] }), pose({ shoulder: [253, 110], hip: [216, 154], head: [269, 89], knee: [183, 194], ankle: [151, 233], kneeFar: [172, 194], ankleFar: [140, 233], elbow: [286, 132], wrist: [289, 92], elbowFar: [287, 139], wristFar: [289, 102] })] },
  plank: { name: 'Планка на предплечьях', floor: true, note: 'Статичное удержание. Дышите спокойно; корпус сохраняет прямую линию.', phases: ['Удержание на предплечьях'], poses: [pose({ head: [108, 177], shoulder: [131, 194], hip: [200, 205], knee: [254, 214], ankle: [304, 222], heel: [309, 221], toe: [319, 238], elbow: [127, 233], wrist: [89, 233], elbowFar: [139, 233], wristFar: [101, 233], kneeFar: [248, 217], ankleFar: [296, 225], heelFar: [301, 224], toeFar: [311, 238] })] },
  'bird-dog': { name: 'Bird dog', floor: true, note: 'Показана одна сторона; затем смените сторону.', phases: ['На четвереньках', 'Противоположные рука и нога'], poses: [pose({ head: [117, 151], shoulder: [138, 169], hip: [205, 169], elbow: [138, 201], wrist: [138, 233], elbowFar: [149, 201], wristFar: [149, 233], knee: [206, 233], ankle: [259, 233], kneeFar: [218, 233], ankleFar: [271, 233] }), pose({ head: [117, 151], shoulder: [138, 169], hip: [205, 169], elbow: [104, 169], wrist: [70, 169], elbowFar: [149, 201], wristFar: [149, 233], knee: [206, 233], ankle: [259, 233], kneeFar: [264, 169], ankleFar: [319, 169], heelFar: [326, 169], toeFar: [342, 169] })] },
  'dead-bug': { name: 'Dead bug', floor: true, note: 'Показана одна сторона; затем смените сторону.', phases: ['Руки вверх, колени под 90°', 'Противоположные рука и нога'], poses: [pose({ head: [94, 221], shoulder: [121, 226], hip: [185, 226], elbow: [121, 188], wrist: [121, 150], elbowFar: [131, 188], wristFar: [131, 150], knee: [185, 173], ankle: [240, 173], kneeFar: [197, 173], ankleFar: [252, 173], heel: [246, 171], toe: [246, 151], heelFar: [258, 171], toeFar: [258, 151] }), pose({ head: [94, 221], shoulder: [121, 226], hip: [185, 226], elbow: [82, 218], wrist: [44, 211], elbowFar: [131, 188], wristFar: [131, 150], knee: [185, 173], ankle: [240, 173], heel: [246, 171], toe: [246, 151], kneeFar: [250, 215], ankleFar: [305, 222], heelFar: [311, 222], toeFar: [326, 215] })] },
  'calf-raise': { name: 'Подъём на носки', wall: true, phases: ['Пятки на полу', 'Подъём пяток'], poses: [pose({ shoulder: [220, 78], hip: [220, 139], head: [220, 52], elbow: [255, 100], wrist: [289, 104], elbowFar: [255, 110], wristFar: [289, 114], knee: [229, 186], ankle: [223, 233], kneeFar: [205, 186], ankleFar: [199, 233] }), pose({ shoulder: [220, 62], hip: [220, 123], head: [220, 36], elbow: [255, 85], wrist: [289, 104], elbowFar: [255, 95], wristFar: [289, 114], knee: [229, 170], ankle: [223, 217], kneeFar: [205, 170], ankleFar: [199, 217], heel: [214, 222], toe: [241, 238], heelFar: [190, 222], toeFar: [217, 238] })] },
  'dumbbell-row': { name: 'Тяга гантели в наклоне', weight: true, note: 'Показана одна сторона; затем смените сторону.', phases: ['Рука под плечом', 'Локоть вдоль корпуса'], poses: [pose({ shoulder: [235, 117], hip: [180, 147], head: [258, 106], knee: [209, 187], ankle: [199, 233], kneeFar: [172, 188], ankleFar: [162, 233], elbow: [236, 154], wrist: [237, 191], elbowFar: [210, 145], wristFar: [209, 181] }), pose({ shoulder: [235, 117], hip: [180, 147], head: [258, 106], knee: [209, 187], ankle: [199, 233], kneeFar: [172, 188], ankleFar: [162, 233], elbow: [202, 136], wrist: [225, 165], elbowFar: [210, 145], wristFar: [209, 181] })] },
  'biceps-curl': { name: 'Сгибание рук с гантелью', weight: true, note: 'Показана одна сторона; затем смените сторону.', phases: ['Рука опущена', 'Сгибание локтя'], poses: [pose({ elbow: [199, 115], wrist: [203, 152] }), pose({ elbow: [199, 115], wrist: [225, 88] })] },
  'hip-hinge': { name: 'Наклон от тазобедренных суставов', phases: ['Стойка', 'Таз назад, спина нейтральна'], poses: [pose({}), pose({ shoulder: [235, 116], hip: [180, 146], head: [258, 103], elbow: [245, 151], wrist: [243, 187], elbowFar: [234, 155], wristFar: [232, 191], knee: [202, 188], ankle: [193, 233], kneeFar: [178, 188], ankleFar: [169, 233] })] },
};
const escapeHTML = value => String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));
function definition(id) { return Object.hasOwn(definitions, id) ? definitions[id] : null; }

export function getDemoPhases(id) { return definition(id)?.phases.slice() ?? []; }

// Return fresh joint arrays: callers cannot mutate the stored demonstrations.
export function exercisePose(id, progress = 0) {
  const demo = definition(id);
  if (!demo) return null;
  const fraction = Number.isFinite(progress) ? Math.max(0, Math.min(1, progress)) : 0;
  const position = fraction * (demo.poses.length - 1);
  const segment = Math.min(Math.floor(position), Math.max(0, demo.poses.length - 2));
  const local = position - segment;
  const t = local * local * (3 - 2 * local);
  const first = demo.poses[segment], last = demo.poses[Math.min(segment + 1, demo.poses.length - 1)], result = {};
  for (const key of new Set([...Object.keys(first), ...Object.keys(last)])) {
    // Feet without explicit coordinates follow the ankle through the motion.
    const endpoint = (source) => source[key] ?? (key.startsWith('heel') ? [source[key.endsWith('Far') ? 'ankleFar' : 'ankle'][0] - 9, source[key.endsWith('Far') ? 'ankleFar' : 'ankle'][1] + 5] : [source[key.endsWith('Far') ? 'ankleFar' : 'ankle'][0] + 18, source[key.endsWith('Far') ? 'ankleFar' : 'ankle'][1] + 5]);
    const a = endpoint(first), b = endpoint(last);
    result[key] = a.map((value, axis) => value + (b[axis] - value) * t);
  }
  // Curl forearm rotates around the elbow instead of shrinking through it.
  if (id === 'biceps-curl') {
    const angle = Math.atan2(37, 4) + (Math.atan2(-27, 26) - Math.atan2(37, 4)) * t;
    const length = Math.hypot(4, 37) + (Math.hypot(26, -27) - Math.hypot(4, 37)) * t;
    result.wrist = [result.elbow[0] + Math.cos(angle) * length, result.elbow[1] + Math.sin(angle) * length];
  }
  for (const suffix of ['', 'Far']) {
    result[`heel${suffix}`] ??= [result[`ankle${suffix}`][0] - 9, result[`ankle${suffix}`][1] + 5];
    result[`toe${suffix}`] ??= [result[`ankle${suffix}`][0] + 18, result[`ankle${suffix}`][1] + 5];
  }
  if (id === 'reverse-lunge' && segment === 0) {
    // Step back through the air, plant the toes, then lower the pelvis.
    // Playback reverses this sequence to lift the body before stepping home.
    const lift = 18 * Math.sin(Math.PI * local);
    for (const key of ['ankleFar', 'heelFar', 'toeFar']) result[key][1] -= lift;
    result.kneeFar[1] -= lift * 0.3;
  }
  return result;
}
const point = p => p.map(n => Number(n.toFixed(2))).join(',');
function figure(p) {
  const chain = (names, className) => `<polyline class="${className}" points="${names.map(name => point(p[name])).join(' ')}"/>`;
  const limb = (suffix, className) => chain(['shoulder', `elbow${suffix}`, `wrist${suffix}`], className) + chain(['hip', `knee${suffix}`, `ankle${suffix}`, `heel${suffix}`, `toe${suffix}`], className);
  return limb('Far', 'demo-limb demo-far') + chain(['shoulder', 'hip'], 'demo-torso') + limb('', 'demo-limb') +
    chain(['shoulder', 'head'], 'demo-neck') + `<circle class="demo-head" cx="${p.head[0]}" cy="${p.head[1]}" r="12"/>` +
    ['shoulder', 'elbow', 'wrist', 'hip', 'knee', 'ankle'].map(name => `<circle class="demo-joint" cx="${p[name][0]}" cy="${p[name][1]}" r="3"/>`).join('');
}
function svg(id, progress = 0, preview = false) {
  const demo = definition(id), p = exercisePose(id, progress);
  if (!demo) return '';
  const equipment = (demo.floor ? '<rect class="demo-mat" x="32" y="238" width="322" height="7" rx="3"/>' : '') + (demo.wall ? '<path class="demo-wall" d="M296 43V241"/><path class="demo-wall-hatch" d="M302 49l9 -9m-9 35l9 -9m-9 35l9 -9m-9 35l9 -9m-9 35l9 -9m-9 35l9 -9m-9 35l9 -9m-9 35l9 -9"/>' : '');
  const weight = demo.weight ? `<g class="demo-weight" transform="translate(${point(p.wrist).replace(',', ' ')}) rotate(-12)"><path d="M-14 0h28"/><path d="M-11 -8v16M11 -8v16"/></g>` : '';
  return `<svg class="exercise-demo-svg" viewBox="0 0 400 280" role="img" aria-label="${escapeHTML(demo.name)}: схематичный вид сбоку" xmlns="http://www.w3.org/2000/svg"><title>${escapeHTML(demo.name)}</title><desc>Схематичный вид сбоку. ${escapeHTML(demo.note || 'Положение показано условно; двигайтесь в комфортной амплитуде.')}</desc><path class="demo-ground" d="M28 245H372"/>${equipment}${figure(p)}${weight}${preview ? '' : '<text class="demo-view-label" x="28" y="266">Схема · вид сбоку</text>'}</svg>`;
}
export function exerciseIllustration(id) { return svg(id, 0.75, true); }

export function exerciseDemoView(id) {
  const demo = definition(id);
  if (!demo) return '<p class="exercise-demo-unavailable">Демонстрация пока недоступна.</p>';
  const fixed = demo.poses.length === 1;
  return `<section class="exercise-demo" data-exercise-demo data-exercise-id="${escapeHTML(id)}" aria-label="Демонстрация упражнения"><div class="exercise-demo-stage" data-demo-stage>${svg(id)}</div><p class="exercise-demo-phase" data-demo-phase>${escapeHTML(demo.phases[0])}</p><p class="exercise-demo-note">${escapeHTML(demo.note || 'Двигайтесь плавно в комфортной амплитуде.')}</p>${fixed ? '<p class="exercise-demo-static">Статичное упражнение: сохраняйте показанное положение.</p>' : `<div class="exercise-demo-controls"><button type="button" data-demo-action="previous" aria-label="Предыдущее положение">←</button><button type="button" data-demo-action="play" aria-label="Запустить демонстрацию" aria-pressed="false">Воспроизвести</button><button type="button" data-demo-action="next" aria-label="Следующее положение">→</button><button type="button" data-demo-action="reset" aria-label="Вернуть исходное положение">Сначала</button></div><label class="exercise-demo-range">Положение движения<input data-demo-range type="range" min="0" max="100" step="1" value="0" aria-label="Положение движения" aria-valuetext="${escapeHTML(demo.phases[0])}"/></label><p class="exercise-demo-motion-note" data-demo-motion-note>Демонстрация на паузе. Можно переключать положения вручную.</p>`}</section>`;
}

// The owner must dispose before closing/replacing the dialog. No playback starts
// until requested; page hiding and reduced-motion changes stop playback.
export function mountExerciseDemo(root, id, environment = {}) {
  const demo = definition(id);
  const host = root?.matches?.('[data-exercise-demo]') ? root : root?.querySelector?.('[data-exercise-demo]');
  if (!demo || !host || host.dataset.exerciseId !== id || demo.poses.length === 1) return () => {};
  const doc = environment.document ?? host.ownerDocument;
  const view = doc?.defaultView ?? globalThis;
  const requestFrame = environment.requestAnimationFrame ?? view.requestAnimationFrame?.bind(view);
  const cancelFrame = environment.cancelAnimationFrame ?? view.cancelAnimationFrame?.bind(view);
  const media = (environment.matchMedia ?? view.matchMedia?.bind(view))?.('(prefers-reduced-motion: reduce)');
  const stage = host.querySelector('[data-demo-stage]'), phase = host.querySelector('[data-demo-phase]');
  const range = host.querySelector('[data-demo-range]'), play = host.querySelector('[data-demo-action="play"]');
  const note = host.querySelector('[data-demo-motion-note]');
  if (!stage || !phase || !range || !play || !note) return () => {};
  let disposed = false, playing = false, progress = 0, frameID = null, lastTime = null, direction = 1;
  const reduced = () => media?.matches === true;
  function render() {
    stage.innerHTML = svg(id, progress);
    const label = demo.phases[Math.round(progress * (demo.phases.length - 1))];
    phase.textContent = label;
    range.value = String(Math.round(progress * 100));
    range.setAttribute('aria-valuetext', `${Math.round(progress * 100)}% · ${label}`);
  }
  function renderControls() {
    play.textContent = playing ? 'Пауза' : 'Воспроизвести';
    play.setAttribute('aria-label', playing ? 'Приостановить демонстрацию' : 'Запустить демонстрацию');
    play.setAttribute('aria-pressed', String(playing));
    play.disabled = reduced() || !requestFrame || !cancelFrame;
    note.textContent = reduced() ? 'Уменьшение движения включено. Переключайте положения вручную.' : playing ? 'Демонстрация воспроизводится. Можно поставить на паузу.' : 'Демонстрация на паузе. Можно переключать положения вручную.';
  }
  function stop() {
    playing = false;
    if (frameID !== null) cancelFrame?.(frameID);
    frameID = null; lastTime = null;
    renderControls();
  }
  function tick(time) {
    frameID = null;
    if (disposed) return;
    if (host.isConnected === false || host.dataset.exerciseId !== id) { dispose(); return; }
    if (!playing || doc?.hidden || reduced()) { stop(); return; }
    if (lastTime !== null) {
      // Cap background/throttled deltas so resuming cannot jump across poses.
      progress += direction * Math.min(Math.max(time - lastTime, 0), 80) / 2200;
      if (progress >= 1) { progress = 1; direction = -1; }
      if (progress <= 0) { progress = 0; direction = 1; }
      render();
    }
    lastTime = time;
    frameID = requestFrame(tick);
  }
  function onClick(event) {
    const button = event.target?.closest?.('[data-demo-action]');
    if (disposed || !button || !host.contains(button)) return;
    const action = button.dataset.demoAction;
    if (action === 'play') {
      if (playing) stop();
      else if (!reduced() && !doc?.hidden && requestFrame && cancelFrame) { playing = true; lastTime = null; renderControls(); frameID = requestFrame(tick); }
      return;
    }
    if (!['previous', 'next', 'reset'].includes(action)) return;
    stop();
    const steps = demo.poses.length - 1;
    const phasePosition = progress * steps;
    const nextPhase = action === 'next' ? Math.floor(phasePosition) + 1 : Math.ceil(phasePosition) - 1;
    progress = action === 'reset' ? 0 : Math.max(0, Math.min(1, nextPhase / steps));
    direction = progress === 1 ? -1 : 1;
    render();
  }
  function onInput() {
    if (disposed) return;
    stop();
    const value = Number(range.value);
    progress = Number.isFinite(value) ? Math.max(0, Math.min(1, value / 100)) : 0;
    direction = progress === 1 ? -1 : 1;
    render();
  }
  function onVisibility() { if (doc?.hidden) stop(); }
  function onMotionChange() { stop(); }
  function dispose() {
    if (disposed) return;
    disposed = true; stop();
    host.removeEventListener('click', onClick);
    range.removeEventListener('input', onInput);
    doc?.removeEventListener('visibilitychange', onVisibility);
    media?.removeEventListener?.('change', onMotionChange);
  }
  host.addEventListener('click', onClick);
  range.addEventListener('input', onInput);
  doc?.addEventListener('visibilitychange', onVisibility);
  media?.addEventListener?.('change', onMotionChange);
  renderControls();
  return dispose;
}
