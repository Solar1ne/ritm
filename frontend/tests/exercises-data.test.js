import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EXERCISES, MUSCLE_GROUPS, EQUIPMENT } from '../static/exercises-data.js';

test('catalogue covers every demonstration exactly once with usable filter tags', () => {
  const ids = ['squat', 'reverse-lunge', 'glute-bridge', 'push-up', 'wall-push-up', 'plank', 'bird-dog', 'dead-bug', 'calf-raise', 'dumbbell-row', 'biceps-curl', 'hip-hinge'];
  assert.deepEqual(EXERCISES.map(exercise => exercise.id).sort(), ids.sort());
  for (const exercise of EXERCISES) {
    for (const [field, labels] of [['muscles', MUSCLE_GROUPS], ['equipment', EQUIPMENT]]) {
      assert.ok(exercise[field].length > 0, `${exercise.id}: missing ${field}`);
      assert.equal(new Set(exercise[field]).size, exercise[field].length);
      for (const value of exercise[field]) assert.ok(Object.hasOwn(labels, value), `${exercise.id}: unknown ${field} ${value}`);
    }
    assert.ok(exercise.steps.length >= 3, `${exercise.id}: needs setup, movement and return`);
    for (const field of ['aliases', 'cues', 'mistakes']) assert.ok(exercise[field].length > 0, `${exercise.id}: empty ${field}`);
    for (const value of [exercise.name, exercise.summary, exercise.easier, ...exercise.aliases, ...exercise.steps, ...exercise.cues, ...exercise.mistakes]) {
      assert.equal(typeof value, 'string');
      assert.ok(value.trim().length > 0);
      assert.doesNotMatch(value, /[<>]|javascript:|\bTODO\b/i, `${exercise.id}: content must be finished plain text`);
    }
  }
});

test('every movement links directly to a named primary publisher over HTTPS', () => {
  const publishers = new Map([
    ['www.acefitness.org', 'American Council on Exercise (ACE)'],
    ['www.nhs.uk', 'NHS'],
    ['www.ouh.nhs.uk', 'Oxford University Hospitals NHS Foundation Trust'],
    ['www.newcastle-hospitals.nhs.uk', 'Newcastle Hospitals NHS Foundation Trust'],
    ['www.southtees.nhs.uk', 'South Tees Hospitals NHS Foundation Trust'],
    ['www.mayoclinic.org', 'Mayo Clinic'],
  ]);
  for (const exercise of EXERCISES) {
    assert.ok(exercise.sources.length > 0, `${exercise.id}: missing sources`);
    for (const source of exercise.sources) {
      const url = new URL(source.url);
      assert.equal(url.protocol, 'https:');
      assert.equal(url.username + url.password + url.search, '');
      assert.notEqual(url.pathname, '/');
      assert.equal(source.publisher, publishers.get(url.hostname));
      assert.ok(source.title.trim());
      assert.doesNotMatch(source.title + source.publisher, /[<>]/);
    }
  }
});

test('equipment filters distinguish optional floor padding from required support or weights', () => {
  const byID = new Map(EXERCISES.map(exercise => [exercise.id, exercise]));
  for (const id of ['glute-bridge', 'push-up', 'plank', 'bird-dog', 'dead-bug']) {
    assert.deepEqual(byID.get(id).equipment, ['none', 'mat']);
    assert.match(byID.get(id).summary, /Коврик необязателен/);
  }
  for (const id of ['wall-push-up', 'calf-raise']) assert.deepEqual(byID.get(id).equipment, ['wall']);
  for (const id of ['dumbbell-row', 'biceps-curl']) assert.deepEqual(byID.get(id).equipment, ['dumbbells']);
});

test('consumers cannot change shared instructions, tags or source destinations', () => {
  const first = EXERCISES[0];
  const name = first.name, step = first.steps[0], url = first.sources[0].url;
  assert.throws(() => EXERCISES.pop(), TypeError);
  assert.throws(() => { first.name = 'changed'; }, TypeError);
  assert.throws(() => { first.steps[0] = 'changed'; }, TypeError);
  assert.throws(() => first.equipment.push('wall'), TypeError);
  assert.throws(() => { first.sources[0].url = 'https://example.com/'; }, TypeError);
  assert.throws(() => { MUSCLE_GROUPS.core = 'changed'; }, TypeError);
  assert.throws(() => { EQUIPMENT.none = 'changed'; }, TypeError);
  assert.equal(first.name, name);
  assert.equal(first.steps[0], step);
  assert.equal(first.sources[0].url, url);
});
