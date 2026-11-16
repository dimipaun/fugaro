// website/test/feature-matrix-model.mjs — the snapshot contract's own test
// (docs/feature-matrix/implementation-plan.md, task C1; the shapes it holds are written for a
// reader in docs/feature-matrix/contract.md).
//
// Three tasks are built against these shapes before any of them has run, so what this file holds
// is the part of the contract a later task would otherwise have to discover: the closed sets, the
// refusals (each naming the field), the block's round trip and the leak check's two rules — a
// hyphenated spec name is a leak, a single-word one is an ordinary word.
//
// The fixture is the contract's, not the stub's: T3 and T4a render it, and the stub goes when I1
// wires the real builder in.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import {
  FIXTURE_FILE,
  FIXTURE_SPECS,
  PLANS,
  STATUSES,
  leakProblems,
  readFixtureSnapshot,
  readSnapshotBlock,
  snapshotBlock,
  validateMapping,
  validateSnapshot,
} from '../src/lib/feature-matrix-model.mjs'

function fixture() {
  return readFixtureSnapshot()
}

function mapping() {
  return {
    version: 1,
    features: [
      {
        id: 'bitbucket-support',
        name: 'Bitbucket support',
        explanation: 'Runs your tasks on Bitbucket Cloud and opens the pull request there.',
        plan: 'community',
        specs: ['bitbucket-credentials'],
      },
      {
        id: 'team-roles',
        name: 'Team roles',
        explanation: 'Gives each person in your organisation their own access to runs and budgets.',
        plan: 'team',
        specs: ['task-governance'],
        hold: { status: 'coming-soon', reason: 'built, and not shipped on any stream yet' },
      },
    ],
    excluded: [{ spec: 'in-job-review', reason: 'internal to the loop, with nothing a reader buys' }],
    retired: ['old-feature'],
  }
}

test('the statuses and the plans are the closed sets the plan states', () => {
  assert.deepEqual(STATUSES, ['available', 'coming-soon', 'planned'])
  assert.deepEqual(PLANS, ['community', 'team', 'enterprise'])
})

test('the mapping of the contract\'s shape is accepted', () => {
  const source = mapping()
  assert.equal(validateMapping(source), source)
})

test('a status the page has no word for is refused, naming the field', () => {
  const snapshot = fixture()
  snapshot.rows[0].status = 'nearly-there'
  assert.throws(() => validateSnapshot(snapshot), /rows\[0\]\.status: "nearly-there" is not one of available, coming-soon, planned/)
})

test('a plan outside the three is refused, naming the field', () => {
  const source = mapping()
  source.features[1].plan = 'pro'
  assert.throws(() => validateMapping(source), /features\[1\]\.plan: "pro" is not one of community, team, enterprise/)
})

test('a hold is a coming-soon and nothing else', () => {
  const source = mapping()
  source.features[1].hold.status = 'planned'
  assert.throws(() => validateMapping(source), /features\[1\]\.hold\.status: "planned" is not one of coming-soon/)
})

test('an unknown key is refused, naming it and the field it sits in', () => {
  const top = mapping()
  top.tier = 'all plans'
  assert.throws(() => validateMapping(top), /the feature matrix mapping: unknown key "tier"/)

  const feature = mapping()
  feature.features[0].tier = 'community'
  assert.throws(() => validateMapping(feature), /features\[0\]: unknown key "tier"/)

  const excluded = mapping()
  excluded.excluded[0].note = 'why not'
  assert.throws(() => validateMapping(excluded), /excluded\[0\]: unknown key "note"/)

  const row = fixture()
  row.rows[0].specs = ['bitbucket-credentials']
  assert.throws(() => validateSnapshot(row), /rows\[0\]: unknown key "specs"/)

  const plans = fixture()
  plans.rows[0].plans.free = true
  assert.throws(() => validateSnapshot(plans), /rows\[0\]\.plans: unknown key "free"/)
})

test('a shape with a field missing is refused, naming the field', () => {
  const source = mapping()
  delete source.features[0].specs
  assert.throws(() => validateMapping(source), /features\[0\]\.specs: is missing/)

  const snapshot = fixture()
  delete snapshot.as_of
  assert.throws(() => validateSnapshot(snapshot), /the feature matrix snapshot: as_of: is missing/)
})

test('a row whose plans have a gap is refused', () => {
  const snapshot = fixture()
  snapshot.rows[0].plans.team = false
  assert.throws(() => validateSnapshot(snapshot), /rows\[0\]\.plans: team is not set while community is/)
})

test('the block round-trips through the markdown', () => {
  const snapshot = fixture()
  const block = snapshotBlock(snapshot)

  assert.equal(block.split('\n')[0], '```json')
  assert.equal(block.split('\n').at(-1), '```')
  assert.equal(snapshotBlock(snapshot), block)

  const markdown = `# Fugaro: what it does\n\n| Feature | Status |\n| --- | --- |\n| Bitbucket support | Available |\n\n${block}\n`
  assert.deepEqual(readSnapshotBlock(markdown), snapshot)
})

test('a document with no block, two blocks, an unclosed block or a broken one is refused', () => {
  const snapshot = fixture()

  assert.throws(() => readSnapshotBlock('# Fugaro: what it does\n'), /carries no ```json block/)
  assert.throws(() => readSnapshotBlock(`${snapshotBlock(snapshot)}\n${snapshotBlock(snapshot)}\n`), /carries 2 ```json blocks/)
  assert.throws(() => readSnapshotBlock('```json\n{\n'), /never closed/)
  assert.throws(() => readSnapshotBlock('```json\nnot json\n```\n'), /the snapshot block is not JSON/)
  assert.throws(() => readSnapshotBlock('```json\n{"version": 1}\n```\n'), /as_of: is missing/)
})

test('the fixture is a snapshot of the contract\'s shape, and covers every status and plan', () => {
  const snapshot = validateSnapshot(JSON.parse(readFileSync(FIXTURE_FILE, 'utf8')))

  assert.deepEqual(readFixtureSnapshot(), snapshot)
  for (const status of STATUSES) {
    assert.ok(snapshot.rows.some((row) => row.status === status), `no fixture row carries the status ${status}`)
  }
  for (const plan of PLANS) {
    assert.ok(snapshot.rows.some((row) => row.plans[plan]), `no fixture row is in the plan ${plan}`)
  }
})

test('a planted hyphenated spec name is a leak, and the word "mobile" is not', () => {
  assert.deepEqual(leakProblems('The run follows bitbucket-credentials today.', FIXTURE_SPECS), [
    'the spec name "bitbucket-credentials"',
  ])
  assert.deepEqual(leakProblems('The mobile app and the website share one snapshot.', FIXTURE_SPECS), [])
  assert.deepEqual(leakProblems('bitbucket-credentials-extra', FIXTURE_SPECS), [])
})

test('a section name, a task id and a date are leaks, and the stamp is not', () => {
  assert.deepEqual(leakProblems('Work in progress, and Parked beside it.', []), [
    "the status page's section name \"Work in progress\"",
    "the status page's section name \"Parked\"",
  ])
  assert.deepEqual(leakProblems('T4a renders the page.', []), ['a task id, "T4a"'])
  assert.deepEqual(leakProblems('Read as of 2026-11-15.', []), ['a date, "2026-11-15"'])
  assert.deepEqual(leakProblems('as_of: v0.24.0', []), [])
})

test('the fixture\'s own words are clean', () => {
  assert.deepEqual(leakProblems(JSON.stringify(fixture(), null, 2), FIXTURE_SPECS), [])
})
