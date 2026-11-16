// website/test/feature-matrix-stub.mjs — the stub's own test
// (docs/feature-matrix/implementation-plan.md, task C1; I1 deletes this file with the stub).
//
// The stub is the stand-in T3 and T4a render against, so what this test holds is exactly the one
// claim it makes: it answers with the contract's fixture, whatever it is handed. Nothing here
// reads status.md, because the stub does not either.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFixtureSnapshot, validateSnapshot } from '../src/lib/feature-matrix-model.mjs'
import { buildSnapshot } from '../src/lib/feature-matrix-stub.mjs'

test('the stub answers with the contract\'s fixture, whatever it is given', () => {
  const snapshot = buildSnapshot({
    statusMarkdown: 'not a status page, and the stub is not reading it',
    mapping: { version: 1, features: [] },
    previous: null,
    asOf: 'v9.9.9',
  })

  assert.deepEqual(snapshot, readFixtureSnapshot())
  validateSnapshot(snapshot)
})
