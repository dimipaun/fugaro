// website/src/lib/feature-matrix-stub.mjs — the snapshot the page renders until T1's builder
// lands (docs/feature-matrix/implementation-plan.md, task C1; I1 deletes this file and its test).
//
// It exists so that T3 (the markdown) and T4a (the page) can be written and reviewed against a
// snapshot before anything reads status.md: both are written against the shape, and the shape is
// what T1 has to produce. It answers with the contract's own fixture
// (`readFixtureSnapshot`, `website/test/fixtures/feature-matrix/snapshot.json`) and reads none of
// its arguments — a stand-in that guessed at status.md would be a second builder, and the one
// thing a stand-in must not be is wrong in a way that looks like work.
//
// Its signature is the one T1's real `buildSnapshot` takes, so I1's change is a deletion:
// `statusMarkdown` is status.md's text, `mapping` the parsed mapping, `previous` the snapshot
// block of the FEATURES.md already on `main` (or null), and `asOf` the release being stamped.

import { readFixtureSnapshot } from './feature-matrix-model.mjs'

export function buildSnapshot({ statusMarkdown, mapping, previous, asOf }) {
  return readFixtureSnapshot()
}
