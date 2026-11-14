import { test } from 'node:test';
import assert from 'node:assert/strict';
import { exerciseDemoView, exerciseIllustration, exercisePose, getDemoPhases, mountExerciseDemo } from '../static/exercise-demo.js';

const ids = ['squat', 'reverse-lunge', 'glute-bridge', 'push-up', 'wall-push-up', 'plank', 'bird-dog', 'dead-bug', 'calf-raise', 'dumbbell-row', 'biceps-curl', 'hip-hinge'];
const distance = (a, b) => Math.hypot(a[0] - b[0], a[1] - b[1]);

class Target {
  listeners = new Map();
  addEventListener(type, fn) { if (!this.listeners.has(type)) this.listeners.set(type, new Set()); this.listeners.get(type).add(fn); }
  removeEventListener(type, fn) { this.listeners.get(type)?.delete(fn); }
  emit(type, event = {}) { for (const fn of this.listeners.get(type) ?? []) fn(event); }
  count() { return [...this.listeners.values()].reduce((sum, set) => sum + set.size, 0); }
}
function fixture(id = 'squat', reduced = false) {
  const host = new Target(), doc = new Target(), media = new Target(), range = new Target();
  const stage = { innerHTML: '' }, phase = {}, note = {}, play = { attributes: {}, setAttribute(key, value) { this.attributes[key] = value; } };
  const nodes = { '[data-demo-stage]': stage, '[data-demo-phase]': phase, '[data-demo-range]': range, '[data-demo-action="play"]': play, '[data-demo-motion-note]': note };
  host.dataset = { exerciseId: id }; host.isConnected = true; host.ownerDocument = doc;
  host.matches = () => true; host.querySelector = key => nodes[key] ?? null; host.contains = node => node.owned !== false;
  range.value = '0'; range.attributes = {}; range.setAttribute = (key, value) => { range.attributes[key] = value; };
  media.matches = reduced; doc.hidden = false;
  let counter = 0;
  const frames = new Map(), canceled = [];
  const environment = { document: doc, matchMedia: () => media, requestAnimationFrame(fn) { frames.set(++counter, fn); return counter; }, cancelAnimationFrame(id) { canceled.push(id); frames.delete(id); } };
  const click = action => { const button = { dataset: { demoAction: action }, closest() { return this; } }; host.emit('click', { target: button }); };
  const advance = timestamp => { const entry = frames.entries().next().value; assert.ok(entry, 'expected a scheduled frame'); frames.delete(entry[0]); entry[1](timestamp); };
  return { host, doc, media, range, stage, phase, note, play, frames, canceled, click, advance, environment };
}

test('all 12 local previews and stages are accessible and contain no external media or duplicate SVG IDs', () => {
  const svgIDs = [];
  for (const id of ids) {
    const html = exerciseDemoView(id), preview = exerciseIllustration(id);
    assert.match(html, /data-exercise-demo/);
    assert.match(html, /role="img"/);
    assert.match(html, /<title>.+<\/title>/);
    assert.match(html, /<desc>.+<\/desc>/);
    assert.match(preview, /viewBox="0 0 400 280"/);
    assert.doesNotMatch(html + preview, /<video|<image|\ssrc=|<iframe|autoplay|data-action=|aria-live=/);
    svgIDs.push(...[...html.matchAll(/\sid="([^"]+)"/g)].map(match => match[1]));
    if (id !== 'plank') {
      assert.match(html, /data-demo-action="play"[^>]*aria-pressed="false"/);
      assert.match(html, /data-demo-range/);
    }
  }
  assert.equal(new Set(svgIDs).size, svgIDs.length);
});

test('all joint positions remain finite and inside the stage throughout interpolation', () => {
  for (const id of ids) for (const progress of [0, 0.125, 0.5, 0.875, 1]) {
    const joints = exercisePose(id, progress);
    for (const [joint, point] of Object.entries(joints)) {
      assert.equal(point.length, 2);
      assert.ok(point.every(Number.isFinite), `${id}: ${joint} finite`);
      assert.ok(point[0] >= 25 && point[0] <= 370 && point[1] >= 20 && point[1] <= 240, `${id}: ${joint} in viewBox`);
    }
    for (const [a, b] of [['shoulder', 'hip'], ['shoulder', 'elbow'], ['elbow', 'wrist'], ['hip', 'knee'], ['knee', 'ankle']]) {
      const length = distance(joints[a], joints[b]);
      assert.ok(length >= 20 && length <= 90, `${id}: plausible ${a}-${b}: ${length}`);
    }
  }
});

test('pose interpolation clamps inputs and returns owned coordinates', () => {
  const first = exercisePose('squat', 0), last = exercisePose('squat', 1);
  assert.deepEqual(exercisePose('squat', -1), first);
  assert.deepEqual(exercisePose('squat', NaN), first);
  assert.deepEqual(exercisePose('squat', 2), last);
  const middle = exercisePose('squat', 0.5);
  assert.equal(middle.hip[1], (first.hip[1] + last.hip[1]) / 2);
  first.hip[0] = -900; getDemoPhases('squat')[0] = 'changed';
  assert.notEqual(exercisePose('squat').hip[0], -900);
  assert.notEqual(getDemoPhases('squat')[0], 'changed');
});

test('isometric plank keeps the same body line; push-up torso and legs move together', () => {
  assert.deepEqual(exercisePose('plank', 0), exercisePose('plank', 1));
  assert.equal(getDemoPhases('plank').length, 1);
  assert.doesNotMatch(exerciseDemoView('plank'), /data-demo-action="play"/);
  for (const id of ['plank', 'push-up']) for (const t of [0, 0.5, 1]) {
    const p = exercisePose(id, t), a = p.shoulder, b = p.ankle;
    for (const key of ['hip', 'knee']) {
      const deviation = Math.abs((b[0] - a[0]) * (a[1] - p[key][1]) - (a[0] - p[key][0]) * (b[1] - a[1])) / distance(a, b);
      assert.ok(deviation < 4, `${id}: ${key} stays near the shoulder-to-ankle line`);
    }
  }
});

test('bird dog and dead bug extend opposite limbs while the supported pair stays still', () => {
  for (const id of ['bird-dog', 'dead-bug']) {
    const start = exercisePose(id, 0), end = exercisePose(id, 1);
    assert.deepEqual(end.hip, start.hip);
    assert.deepEqual(end.shoulder, start.shoulder);
    assert.deepEqual(end.wristFar, start.wristFar);
    assert.deepEqual(end.knee, start.knee);
    assert.notDeepEqual(end.wrist, start.wrist);
    assert.notDeepEqual(end.kneeFar, start.kneeFar);
    assert.match(exerciseDemoView(id), /Показана одна сторона; затем смените сторону/);
  }
  const bird = exercisePose('bird-dog', 1);
  assert.equal(bird.wrist[1], bird.shoulder[1]);
  assert.equal(bird.ankleFar[1], bird.hip[1]);
});

test('squat and hinge keep feet grounded; calf raise lifts heels and keeps toes grounded', () => {
  for (const id of ['squat', 'hip-hinge']) for (const t of [0, 0.5, 1]) {
    const p = exercisePose(id, t);
    assert.equal(p.heel[1], 238); assert.equal(p.toe[1], 238);
    assert.equal(p.heelFar[1], 238); assert.equal(p.toeFar[1], 238);
  }
  const start = exercisePose('calf-raise', 0), end = exercisePose('calf-raise', 1);
  assert.ok(end.heel[1] < start.heel[1]); assert.equal(end.toe[1], start.toe[1]);
  assert.ok(end.head[1] < start.head[1]);
  for (const id of ['biceps-curl', 'dumbbell-row']) {
    assert.deepEqual(exercisePose(id, 0).hip, exercisePose(id, 1).hip);
    assert.deepEqual(exercisePose(id, 0).shoulder, exercisePose(id, 1).shoulder);
  }
});

test('reverse lunge plants the front foot, lifts the back foot through the step, then lowers on planted toes', () => {
  const start = exercisePose('reverse-lunge', 0), landed = exercisePose('reverse-lunge', 0.5);
  assert.equal(getDemoPhases('reverse-lunge').length, 3);
  for (const t of [0, 0.125, 0.25, 0.375, 0.5, 0.75, 1]) {
    const p = exercisePose('reverse-lunge', t);
    for (const key of ['ankle', 'heel', 'toe']) assert.deepEqual(p[key], start[key], `${key}: fixed front support`);
    if (t > 0 && t < 0.5) {
      assert.ok(p.toeFar[1] < 238, 'swinging toes clear the ground');
      assert.ok(p.heelFar[1] < 238, 'swinging heel clears the ground');
      assert.ok(p.toeFar[0] < start.toeFar[0] && p.toeFar[0] > landed.toeFar[0], 'only the back foot steps backwards');
    }
    if (t >= 0.5) for (const key of ['ankleFar', 'heelFar', 'toeFar']) assert.deepEqual(p[key], landed[key], `${key}: planted before lowering`);
  }
  assert.ok(exercisePose('reverse-lunge', 1).hip[1] > landed.hip[1]);
});

test('wall hands and floor support points stay fixed; the curl forearm rotates without collapsing', () => {
  for (const id of ['wall-push-up', 'calf-raise']) {
    const start = exercisePose(id);
    for (const t of [0.25, 0.5, 0.75, 1]) {
      assert.deepEqual(exercisePose(id, t).wrist, start.wrist);
      assert.deepEqual(exercisePose(id, t).wristFar, start.wristFar);
    }
  }
  for (const t of [0, 0.25, 0.5, 0.75, 1]) {
    const p = exercisePose('push-up', t), start = exercisePose('push-up');
    for (const key of ['wrist', 'wristFar', 'toe', 'toeFar']) assert.deepEqual(p[key], start[key]);
    const curl = exercisePose('biceps-curl', t);
    assert.ok(distance(curl.elbow, curl.wrist) > 37);
  }
});

test('unknown identities are safe and do not install listeners', () => {
  for (const id of ['unknown', '__proto__', 'constructor', '<script>']) {
    assert.equal(exercisePose(id), null); assert.equal(exerciseIllustration(id), '');
    assert.deepEqual(getDemoPhases(id), []);
    assert.match(exerciseDemoView(id), /пока недоступна/);
    const f = fixture(id); mountExerciseDemo(f.host, id, f.environment)();
    assert.equal(f.host.count(), 0); assert.equal(f.frames.size, 0);
  }
  assert.doesNotThrow(() => mountExerciseDemo(null, 'squat')());
});

test('starts paused, manual controls work, playback can pause, and disposal removes every listener and frame', () => {
  const f = fixture(), dispose = mountExerciseDemo(f.host, 'squat', f.environment);
  assert.equal(f.frames.size, 0); assert.equal(f.play.attributes['aria-pressed'], 'false');
  f.click('next'); assert.equal(f.range.value, '100');
  f.click('previous'); assert.equal(f.range.value, '0');
  f.range.value = '37'; f.range.emit('input'); assert.equal(f.range.value, '37');
  f.click('reset'); assert.equal(f.range.value, '0');
  f.click('play'); assert.equal(f.frames.size, 1);
  f.advance(0); f.advance(80); assert.ok(Number(f.range.value) > 0);
  f.click('play'); assert.equal(f.frames.size, 0);
  f.click('play'); const lateFrame = f.frames.values().next().value;
  dispose(); dispose();
  const html = f.stage.innerHTML;
  lateFrame(160); f.click('next');
  assert.equal(f.stage.innerHTML, html); assert.equal(f.frames.size, 0);
  assert.equal(f.host.count() + f.range.count() + f.doc.count() + f.media.count(), 0);
});

test('reverse lunge manual controls visit the middle phase, clamp endpoints and navigate from between phases', () => {
  const f = fixture('reverse-lunge'), dispose = mountExerciseDemo(f.host, 'reverse-lunge', f.environment);
  for (const [action, expected] of [['next', '50'], ['next', '100'], ['next', '100'], ['previous', '50'], ['previous', '0'], ['previous', '0']]) {
    f.click(action);
    assert.equal(f.range.value, expected, `${action} selects the adjacent keyframe`);
    assert.equal(f.frames.size, 0);
    if (expected === '50') assert.equal(f.phase.textContent, 'Шаг назад');
  }
  for (const [from, action, expected] of [['37', 'next', '50'], ['37', 'previous', '0'], ['73', 'next', '100'], ['73', 'previous', '50'], ['50', 'reset', '0'], ['73', 'reset', '0']]) {
    f.range.value = from; f.range.emit('input'); f.click(action);
    assert.equal(f.range.value, expected, `${action} from ${from}% selects ${expected}%`);
  }
  f.range.value = '37'; f.range.emit('input'); f.click('play');
  assert.equal(f.frames.size, 1);
  f.click('next'); assert.equal(f.range.value, '50'); assert.equal(f.frames.size, 0);
  dispose();
});

test('reduced motion prohibits automatic playback and preserves manual positioning', () => {
  const f = fixture('squat', true), dispose = mountExerciseDemo(f.host, 'squat', f.environment);
  assert.equal(f.play.disabled, true);
  f.click('play'); assert.equal(f.frames.size, 0);
  f.click('next'); assert.equal(f.range.value, '100');
  f.range.value = '24'; f.range.emit('input'); assert.equal(f.range.value, '24');
  f.media.matches = false; f.media.emit('change'); assert.equal(f.play.disabled, false);
  f.click('play'); assert.equal(f.frames.size, 1);
  f.media.matches = true; f.media.emit('change'); assert.equal(f.frames.size, 0);
  dispose();
});

test('hidden pages pause and stay paused when visible; stale identity or detached host disposes', () => {
  const f = fixture(), dispose = mountExerciseDemo(f.host, 'squat', f.environment);
  f.click('play'); f.doc.hidden = true; f.doc.emit('visibilitychange');
  assert.equal(f.frames.size, 0);
  f.click('play'); assert.equal(f.frames.size, 0);
  f.doc.hidden = false; f.doc.emit('visibilitychange'); assert.equal(f.frames.size, 0);
  f.click('play'); f.host.dataset.exerciseId = 'hip-hinge'; f.advance(100);
  assert.equal(f.frames.size, 0); assert.equal(f.host.count() + f.doc.count() + f.range.count() + f.media.count(), 0);
  dispose();
  const detached = fixture(); mountExerciseDemo(detached.host, 'squat', detached.environment);
  detached.click('play'); detached.host.isConnected = false; detached.advance(100);
  assert.equal(detached.frames.size, 0); assert.equal(detached.host.count(), 0);
});
