// website/test/feature-matrix-markdown.mjs — FEATURES.md's renderer, and the command that writes
// it (docs/feature-matrix/implementation-plan.md, task T3).
//
// The fixture is the contract's, rendered here rather than built: what this file holds is the
// three-column shape D2440 chose, the block that comes back equal to the snapshot it was written
// from (R5), the leak check over the whole rendered document (R1, R8), the stamp's agreement
// with the release the command was asked for, and the command's promise that a refusal leaves
// the file as it was. The builder the command uses is passed in, so the
// refusal case needs no status.md that has one in it — and the real builder (T1) and the stub it
// replaces are both out of this file's reach.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import {
  FIXTURE_SPECS,
  PLANS,
  leakProblems,
  readFixtureSnapshot,
  readSnapshotBlock,
} from '../src/lib/feature-matrix-model.mjs'
import { renderMarkdown } from '../src/lib/feature-matrix-markdown.mjs'
import { main } from '../bin/feature-matrix.mjs'

const PLAN_HEADERS = ['Community', 'Team', 'Enterprise']

const STATUS_WORDS = { available: 'Available', 'coming-soon': 'Coming soon', planned: 'Planned' }

const MARK = '✓'
const NO_MARK = '—'

function fixture() {
  return readFixtureSnapshot()
}

/** The rendered document's one table: its header cells and its rows, in order. */
function table(markdown) {
  const lines = markdown.split('\n').filter((line) => line.startsWith('|'))
  assert.ok(lines.length >= 2, 'the rendered document carries no table')
  const cells = (line) =>
    line
      .replace(/^\|/, '')
      .replace(/\|$/, '')
      .split('|')
      .map((cell) => cell.trim())
  return { header: cells(lines[0]), rows: lines.slice(2).map(cells) }
}

/** A scratch tree the command can read and write: the two inputs it reads, and no FEATURES.md. */
function scratch() {
  const repo = mkdtempSync(path.join(tmpdir(), 'feature-matrix-'))
  mkdirSync(path.join(repo, 'docs', 'feature-matrix'), { recursive: true })
  writeFileSync(path.join(repo, 'docs', 'status.md'), '# Status\n')
  writeFileSync(path.join(repo, 'docs', 'feature-matrix', 'mapping.yaml'), 'version: 1\nfeatures: []\n')
  return repo
}

/** Everything written to stderr while `run` runs, so a refusal's own words can be read back. */
function captureStderr(run) {
  const write = process.stderr.write
  let text = ''
  process.stderr.write = (chunk) => {
    text += String(chunk)
    return true
  }
  try {
    run()
  } finally {
    process.stderr.write = write
  }
  return text
}

test('the table carries the three plan columns, and a mark under each per row', () => {
  const snapshot = fixture()
  const { header, rows } = table(renderMarkdown(snapshot))

  assert.deepEqual(header, ['Feature', 'What it does', 'Status', ...PLAN_HEADERS])
  assert.equal(rows.length, snapshot.rows.length, 'the table has one row per snapshot row')

  snapshot.rows.forEach((row, i) => {
    assert.equal(rows[i][0], row.name)
    assert.equal(rows[i][1], row.explanation)
    assert.equal(rows[i][2], STATUS_WORDS[row.status])
    assert.deepEqual(
      rows[i].slice(3),
      PLANS.map((plan) => (row.plans[plan] ? MARK : NO_MARK)),
    )
  })

  // Each of the three columns is exercised: a column that no row marks would pass the checks
  // above while telling a reader nothing, which is the two-column render this case is written
  // against.
  PLAN_HEADERS.forEach((plan, column) => {
    assert.ok(
      rows.some((cells) => cells[3 + column] === MARK),
      `no row is marked under ${plan}`,
    )
  })
})

test('the snapshot block reads back equal to the snapshot that was rendered', () => {
  const snapshot = fixture()
  assert.deepEqual(readSnapshotBlock(renderMarkdown(snapshot)), snapshot)
})

test('the whole rendered document carries no leak', () => {
  assert.deepEqual(leakProblems(renderMarkdown(fixture()), FIXTURE_SPECS), [])
})

test('the command writes FEATURES.md from the snapshot its builder answers with', () => {
  const repo = scratch()
  const code = main({ asOf: 'v0.24.0', builder: () => fixture(), repo })
  assert.equal(code, 0)
  assert.equal(readFileSync(path.join(repo, 'FEATURES.md'), 'utf8'), renderMarkdown(fixture()))
})

test('a refusal from the builder names what it refused and leaves FEATURES.md byte-identical', () => {
  const repo = scratch()
  const file = path.join(repo, 'FEATURES.md')
  const before = renderMarkdown(fixture())
  writeFileSync(file, before)

  const refusal = new Error('the feature matrix: status.md carries names the mapping does not: compute-backend, voice-module')
  const printed = captureStderr(() => {
    assert.notEqual(main({ asOf: 'v0.24.0', builder: () => {
      throw refusal
    }, repo }), 0)
  })

  assert.equal(readFileSync(file, 'utf8'), before)
  assert.match(printed, /compute-backend/)
  assert.match(printed, /voice-module/)
})

test('a snapshot stamped for another release is refused, naming both, and nothing is written', () => {
  const repo = scratch()
  const printed = captureStderr(() => {
    assert.notEqual(main({ asOf: 'v0.30.0', builder: () => fixture(), repo }), 0)
  })
  assert.match(printed, /"v0\.24\.0"/)
  assert.match(printed, /"v0\.30\.0"/)
  assert.ok(!existsSync(path.join(repo, 'FEATURES.md')), 'a refused run wrote FEATURES.md')
})

test('the command refuses without a release to stamp, and writes nothing', () => {
  const repo = scratch()
  const printed = captureStderr(() => {
    assert.notEqual(main({ builder: () => fixture(), repo }), 0)
  })
  assert.match(printed, /--as-of/)
  assert.ok(!existsSync(path.join(repo, 'FEATURES.md')), 'a refused run wrote FEATURES.md')
})
