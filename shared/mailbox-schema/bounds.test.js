// The JavaScript half of vectors.bounds.json, the shared bound vectors.
//
// vectors.json pins the tag, the direction and the refusals the schema has
// always named; it pins no BOUND, because no case in it sits on one. That gap
// was invisible until someone moved a comparison by one: `repo.length >
// TARGET_MAX` widened to `> TARGET_MAX + 1` accepted a 201-code-unit repository
// in this module with all 51 tests green, and narrowed to `>= TARGET_MAX` it
// refused a 200-code-unit one. The Go binding has its own rows for the same
// bounds; this file is what makes the JavaScript end hold them too, out of the
// same artifact, so the two ends cannot drift apart in silence.
//
// The file is not vectors.json and does not extend it: see its
// "not_vectors_json" note for why that artifact is P6.3's to change and this one
// is nobody's.
//
// Each row is asserted twice. An accepts row must be accepted; and an accepts
// row carrying a "past" entry must ALSO be refused when one code unit (or one
// list entry) is added at "at" — which is what pins the bound exactly rather
// than approximately, and what tells a narrowed bound from a widened one.

import { readFileSync } from 'node:fs';
import test from 'node:test';
import assert from 'node:assert/strict';

import { validateMessage, validateHeartbeat } from './index.js';

const bounds = JSON.parse(readFileSync(new URL('./vectors.bounds.json', import.meta.url), 'utf8'));

// at walks a dotted path — `action.target.repo`, `agents[0].name` — and returns
// the value it names, failing loudly rather than returning undefined: a path
// the file no longer has is a broken fixture, not a passing check.
function at(value, path) {
  let node = value;
  for (const step of path.split('.')) {
    const match = /^([^[]*)((?:\[\d+\])*)$/.exec(step);
    if (match[1] !== '') node = node[match[1]];
    for (const index of match[2].matchAll(/\[(\d+)\]/g)) node = node[Number(index[1])];
  }
  return node;
}

// parentOf resolves a dotted path — `action.target.repo`, `agents[0].name` — to
// the container the last step lives in and the key within it, which is what a
// writer needs. A path the file no longer has throws rather than inventing a
// property: a broken fixture must not look like a passing check.
function parentOf(value, path) {
  const parts = path.split('.');
  const leaf = parts.pop();
  let node = parts.length === 0 ? value : at(value, parts.join('.'));
  const match = /^([^[]*)((?:\[\d+\])*)$/.exec(leaf);
  const indices = [...match[2].matchAll(/\[(\d+)\]/g)].map((index) => Number(index[1]));
  if (match[1] !== '') {
    if (indices.length === 0) return { parent: node, key: match[1] };
    node = node[match[1]];
  }
  for (const index of indices.slice(0, -1)) node = node[index];
  const key = indices.at(-1);
  if (node == null || node[key] === undefined) throw new Error(`the fixture has no ${path}`);
  return { parent: node, key };
}

// grown returns a deep copy of value one step past the bound: one more code
// unit at spec.at, one more entry on the list at spec.at, or nought in the
// number at spec.at.
function grown(value, spec) {
  const copy = structuredClone(value);
  const { parent, key } = parentOf(copy, spec.at);
  if (spec.mode === 'append') parent[key].push(structuredClone(parent[key][0]));
  else if (spec.mode === 'zero') parent[key] = 0;
  else parent[key] = `${parent[key]}x`;
  return copy;
}

const verdictOf = (record, value) => (record === 'heartbeat' ? validateHeartbeat(value) : validateMessage(value));

test('vectors.bounds.json is the shape both ends read', () => {
  assert.equal(typeof bounds.format, 'string');
  assert.equal(typeof bounds.why, 'string');
  assert.ok(Array.isArray(bounds.accepts) && bounds.accepts.length > 0, 'the file carries accepts');
  assert.ok(Array.isArray(bounds.refusals) && bounds.refusals.length > 0, 'the file carries refusals');
  for (const row of [...bounds.accepts, ...bounds.refusals]) {
    assert.equal(typeof row.name, 'string', 'every row is named');
    assert.equal(typeof row.why, 'string', `${row.name}: every row says what it pins`);
    assert.ok(row.record === 'message' || row.record === 'heartbeat', `${row.name}: names its record`);
    assert.ok(row.value !== null && typeof row.value === 'object', `${row.name}: carries a value`);
  }
});

test('the bound vectors the module accepts are accepted', () => {
  for (const row of bounds.accepts) {
    const verdict = verdictOf(row.record, row.value);
    assert.equal(verdict.ok, true, `${row.name}: accepted (${row.why})`);
  }
});

test('every bound row is refused one code unit past its bound', () => {
  let checked = 0;
  for (const row of bounds.accepts) {
    if (!Object.hasOwn(row, 'past')) continue;
    checked += 1;
    const spec = row.past;
    const verdict = verdictOf(row.record, grown(row.value, spec));
    assert.equal(verdict.ok, false, `${row.name}: one past the bound is refused (${spec.at})`);
    assert.equal(verdict.code, spec.code, `${row.name}: the code one past the bound`);
    assert.equal(verdict.field, spec.at, `${row.name}: the field one past the bound`);
  }
  // The fixture is what is being counted: a file whose rows all lost their
  // "past" entries would otherwise pass this test by having nothing to check.
  assert.equal(checked, bounds.accepts.filter((row) => Object.hasOwn(row, 'past')).length);
  assert.ok(checked >= 13, `the file carries the bound rows, not ${checked}`);
});

test('the bound vectors the module refuses are refused with the code and the field', () => {
  for (const row of bounds.refusals) {
    const verdict = verdictOf(row.record, row.value);
    assert.equal(verdict.ok, false, `${row.name}: refused (${row.why})`);
    assert.equal(verdict.code, row.code, `${row.name}: the code`);
    assert.equal(verdict.field, row.field, `${row.name}: the field`);
  }
  // The two mutants this file exists for, named so that dropping either row
  // fails here rather than silently narrowing what the file checks.
  const names = bounds.refusals.map((row) => row.name).join(' | ');
  for (const required of ['a repository one code unit past its bound', 'a sender carrying U+001F']) {
    assert.ok(names.includes(required), `the file refuses "${required}": ${names}`);
  }
  const accepts = bounds.accepts.map((row) => row.name).join(' | ');
  for (const required of ['a repository of exactly TARGET_MAX', 'a body carrying a carriage return']) {
    assert.ok(accepts.includes(required), `the file accepts "${required}": ${accepts}`);
  }
});
