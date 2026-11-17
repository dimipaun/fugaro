// website/test/feature-matrix-stamp.mjs — the committed FEATURES.md, and the page built from it
// (docs/feature-matrix/implementation-plan.md, task I1; F13 criteria 2 and 4, R1, R2, R5, R8).
//
// I1 is the task that joins the real sides: the generator builds the matrix out of status.md and
// the mapping, and `loadFeatures()` reads the block FEATURES.md carries. This is the one file that
// holds the two joined ends to each other, and it holds four things, one per acceptance line:
//
//   1. **The stamp** (F13 criterion 4). `as_of` names an existing `docs/releases/vX.Y.Z.md` that is
//      not behind the newest notes file at or below the CLI stream's `current`; one release ahead
//      is allowed, because the notes for a release are written before `scripts/release.sh` moves
//      `current` (D2447, D2449). The rule lives here as a function of the stamp, `current` and the
//      notes the repository carries — not as a case about today's file — so a case below can hand
//      it a stamp one release ahead, one behind, and one the repository has no notes for.
//   2. **The block is a snapshot**, read back through the contract's own reader, and it is the only
//      fenced block the file carries (R5): the block under the table is the page's rows, and a
//      second fence would leave a reader deciding which one is the snapshot's.
//   3. **The page's rows are the block's rows** (F13 criterion 2), end to end: `loadFeatures()`
//      answers with the block, and the built page prints what the loader answered. This is the
//      case I1's red step is — it fails while `loadFeatures()` still reads the contract's stub,
//      because the page then renders the fixture while the repository carries the real matrix.
//   4. **Neither output carries what it may not** (R1, R8): `leakProblems` over the committed
//      FEATURES.md and over the built page, with the spec names read out of the mapping — its
//      `specs` and `excluded` lists are every spec name the matrix knows. The block carries the
//      rows' ids, so the file's own text is read with them.
//
// **It reads no part of `docs/status.md`** (R2): that page changes several times a day through
// status sweeps, and a gate that read it would turn red on a sweep no website change can fix. The
// generator's agreement with status.md is proved once, by the run that writes FEATURES.md, and is
// deliberately not a standing test (F11) — nor is a byte-exact freshness check, for the same
// reason: it would make every sweep regenerate the matrix.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { DIST, REPO, findAll, parse, textOf } from './lib/html.mjs'
import { loadFeatures, parseYAML } from '../src/content.ts'
import { PLANS, leakProblems, readSnapshotBlock } from '../src/lib/feature-matrix-model.mjs'

/** The document this file is about: the one the generator writes, at the repository root (D2446). */
const FEATURES_FILE = path.join(REPO, 'FEATURES.md')

/** The page the block's rows reach a reader on (T4a). */
const BUILT_PAGE = path.join(DIST, 'features', 'index.html')

/** Where the released version of each stream is written, and where a release's notes live. */
const STREAMS_FILE = 'docs/release-streams.yaml'
const RELEASES_DIR = path.join(REPO, 'docs', 'releases')

/** The mapping: the one place a feature's words and its specs are written (D2441, F1). */
const MAPPING_FILE = path.join(REPO, 'docs', 'feature-matrix', 'mapping.yaml')

/** A release note's name, and nothing else in that directory is one: `README.md` and the two
 * subfolders (`console/`, `mobile/`) are not notes for a CLI release. */
const NOTES_FILE = /^v\d+\.\d+\.\d+\.md$/

const RELEASE = /^v?(\d+)\.(\d+)\.(\d+)$/

/** A page's text as a reader reads it: one line, one space between two words. */
function flat(text) {
  return text.replace(/\s+/g, ' ').trim()
}

/** The document, or the reason this case cannot read it. */
function features() {
  assert.ok(
    existsSync(FEATURES_FILE),
    'FEATURES.md is not there: it is the file the generator writes and the page reads its rows from (`cd website && npm run features -- --as-of vX.Y.Z`), so this case would check nothing',
  )
  return readFileSync(FEATURES_FILE, 'utf8')
}

/** The built page, as the artifact a reader receives. */
function builtPage() {
  assert.ok(
    existsSync(BUILT_PAGE),
    `the built page is not there: ${path.relative(DIST, BUILT_PAGE)} is the artifact this case reads rather than the source, so run \`npm run check\`, which builds the site first`,
  )
  return readFileSync(BUILT_PAGE, 'utf8')
}

/** The CLI stream's released version, as the stamp spells it: `current` under the stream's tag prefix. */
function cliCurrent() {
  const file = path.join(REPO, STREAMS_FILE)
  const streams = parseYAML(readFileSync(file, 'utf8'), STREAMS_FILE).streams
  const cli = Array.isArray(streams) ? streams.find((stream) => stream?.name === 'cli') : undefined
  assert.ok(cli, `${STREAMS_FILE} carries no cli stream, and the stamp is read against the version that stream has released`)
  return `v${cli.current}`
}

/** Every release the repository has notes for, as the file names spell them (`vX.Y.Z`). */
function notes() {
  return readdirSync(RELEASES_DIR)
    .filter((name) => NOTES_FILE.test(name))
    .map((name) => name.replace(/\.md$/, ''))
    .sort(compare)
}

/** A release as three numbers, so two of them order. */
function numbers(release) {
  const found = RELEASE.exec(release)
  assert.ok(found, `${JSON.stringify(release)} is not a release of the form vX.Y.Z`)
  return found.slice(1).map(Number)
}

function compare(a, b) {
  const [left, right] = [numbers(a), numbers(b)]
  for (let at = 0; at < 3; at++) {
    if (left[at] !== right[at]) return left[at] - right[at]
  }
  return 0
}

/**
 * Every way `asOf` departs from F13 criterion 4, as lines, given the stream's `current` and the
 * releases the repository has notes for. An empty list is the pass.
 *
 * Three refusals, and each is one of the criterion's own clauses: a stamp behind the newest notes
 * file at or below `current`, a stamp more than one release ahead of it, and a stamp no notes file
 * exists for. "One release ahead" is the *next* release the repository has notes for, because
 * `release.sh` has not moved `current` yet when the notes are written: a version two ahead of that
 * is one no procedure produces.
 */
function stampProblems(asOf, { current, notes: releases }) {
  const out = []

  const newer = releases.filter((release) => compare(release, current) <= 0 && compare(release, asOf) > 0)
  if (newer.length > 0) {
    const newest = newer[newer.length - 1]
    out.push(
      `${asOf} is behind ${newest}, the newest notes file at or below ${current}: the stamp names a release the repository has moved past, so the matrix would read as a snapshot of a version older than the one it was built at (F13 criterion 4)`,
    )
  }

  const ahead = releases.filter((release) => compare(release, current) > 0)
  if (compare(asOf, current) > 0 && asOf !== ahead[0]) {
    out.push(
      `${asOf} is more than one release ahead of ${current}: the notes for a release are written before it is tagged, so the one release ahead that is allowed is ${ahead[0] ?? 'the next one'}, and a stamp past it is a version no procedure produces (F13 criterion 4, D2449)`,
    )
  }

  if (!releases.includes(asOf)) {
    out.push(
      `${asOf} names no notes file under docs/releases: the criterion's first clause is that the stamp names an existing \`vX.Y.Z.md\`, and a release nobody wrote notes for is one nothing in this repository attests to (F13 criterion 4)`,
    )
  }

  return out
}

/** The spec names the matrix knows: every feature's specs and every excluded name (R8). */
function specNames() {
  const mapping = parseYAML(readFileSync(MAPPING_FILE, 'utf8'), MAPPING_FILE)
  return [
    ...(mapping.features ?? []).flatMap((feature) => feature?.specs ?? []),
    ...(mapping.excluded ?? []).map((entry) => entry?.spec),
  ].filter((name) => typeof name === 'string')
}

/** One descendant of `el` carrying a class, or null when it carries none or several. */
function single(el, name) {
  const found = findAll(el, (one) => (one.attrs?.get('class') ?? '').split(/\s+/).includes(name))
  return found.length === 1 ? found[0] : null
}

/**
 * The built page's table as the rows a reader reads: each row's name, its explanation, its status
 * and the mark under each plan. Read here rather than shared with `features-page.mjs`, which holds
 * the same page to the loader: that file is a test file, and importing one registers its cases a
 * second time.
 */
function pageRows(html) {
  const tables = findAll(parse(html), (el) => el.name === 'table')
  assert.equal(tables.length, 1, `the built page carries ${tables.length} table(s), and the matrix is one: this case reads the rows of that one table`)
  const body = findAll(tables[0], (el) => el.name === 'tbody')[0]
  assert.ok(body, 'the built page\'s table carries no <tbody>, so it has no rows to read')

  return (body.children ?? [])
    .filter((child) => child.name === 'tr')
    .map((tr) => {
      const cells = tr.children.filter((child) => child.name === 'th' || child.name === 'td')
      const name = cells[0] === undefined ? null : single(cells[0], 'feat-name')
      const explanation = cells[0] === undefined ? null : single(cells[0], 'feat-explanation')
      const status = cells[1] === undefined ? null : single(cells[1], 'feat-status')
      return {
        name: name === null ? '' : flat(textOf(name)),
        explanation: explanation === null ? '' : flat(textOf(explanation)),
        status: status === null ? '' : (status.attrs.get('data-status') ?? ''),
        plans: Object.fromEntries(
          PLANS.map((plan, at) => [plan, findAll(cells[at + 2] ?? { children: [] }, (one) => (one.attrs?.get('class') ?? '').split(/\s+/).includes('feat-check')).length > 0]),
        ),
      }
    })
}

// ---- the gate --------------------------------------------------------------------------

test('the committed FEATURES.md is stamped for a release the repository has notes for', () => {
  const problems = stampProblems(readSnapshotBlock(features()).as_of, { current: cliCurrent(), notes: notes() })
  assert.deepEqual(problems, [], `FEATURES.md's stamp departs from F13 criterion 4:\n  ${problems.join('\n  ')}`)
})

test('the stamp rule takes one release ahead and refuses one behind, one too far and one with no notes', () => {
  // The case the criterion's own wording turns on: `current` is the stream's released version, the
  // notes for the next release are already written, and the stamp names that next release.
  const current = 'v0.25.0'
  const releases = ['v0.23.0', 'v0.24.0', 'v0.25.0', 'v0.26.0']
  assert.deepEqual(
    stampProblems('v0.26.0', { current, notes: releases }),
    [],
    'a stamp one release ahead of the stream\'s current must be accepted: the notes PR stamps the version before release.sh moves current (F13 criterion 4)',
  )
  assert.deepEqual(
    stampProblems('v0.25.0', { current, notes: releases }),
    [],
    'a stamp naming the stream\'s current release must be accepted',
  )

  const behind = stampProblems('v0.24.0', { current, notes: releases })
  assert.ok(
    behind.some((problem) => problem.includes('v0.24.0') && problem.includes('v0.25.0')),
    `a stamp behind the newest notes file at or below current must be refused, naming the stamp and the release it is behind: the failures this rule found were\n  ${behind.join('\n  ') || '(none)'}`,
  )

  const tooFar = stampProblems('v0.27.0', { current, notes: releases })
  assert.ok(
    tooFar.some((problem) => problem.includes('more than one release ahead')),
    `a stamp more than one release ahead must be refused: the failures this rule found were\n  ${tooFar.join('\n  ') || '(none)'}`,
  )

  const unwritten = stampProblems('v0.25.5', { current, notes: releases })
  assert.ok(
    unwritten.some((problem) => problem.includes('no notes file')),
    `a stamp naming a release no notes file exists for must be refused: the failures this rule found were\n  ${unwritten.join('\n  ') || '(none)'}`,
  )

  // The rule is a reading of the repository, not a shape test: a stamp the repository has notes for
  // and that is not behind `current` is the only thing it accepts, and a rule that accepted
  // everything would pass every line above but this one.
  assert.ok(
    stampProblems('v0.10.0', { current, notes: releases }).length > 0,
    'a stamp from long before the stream\'s current release must be refused, and a rule that accepted every stamp would not be one',
  )
})

test('the file carries one snapshot block, in the contract\'s shape, and one stamp line to grep', () => {
  const text = features()
  const snapshot = readSnapshotBlock(text)
  assert.ok(snapshot.rows.length > 0, 'the block carries no row, so the page built from it would carry an empty table')

  // R5: the block under the table is the file's only fenced block, and `readSnapshotBlock` refuses
  // a second one of its own kind (`json`). A fence of another language would slip past that reader
  // and leave a by-eye check deciding which fence is the snapshot's, which is why the count is here.
  const fences = text.split('\n').filter((line) => line.trim().startsWith('```')).length
  assert.equal(fences, 2, `FEATURES.md carries ${fences} fence line(s) and the snapshot's block is one of them, opened and closed (R5)`)

  // The stamp is a line of its own and the file carries one of them: `scripts/release.sh` has git
  // and grep and no node, so this line — not the block's own `as_of` — is the fact the release
  // procedure moves (D2447, D2449), and a second one would leave it two answers.
  assert.deepEqual(
    text.split('\n').filter((line) => line.startsWith('as_of:')),
    [`as_of: ${snapshot.as_of}`],
    'FEATURES.md carries no one line of its own reading `as_of: vX.Y.Z`',
  )
})

test('the loader answers with the block FEATURES.md carries', () => {
  const snapshot = readSnapshotBlock(features())
  assert.deepEqual(
    loadFeatures(),
    { asOf: snapshot.as_of, rows: snapshot.rows },
    'loadFeatures() does not answer with the committed FEATURES.md\'s block: the page reads its rows from that block and nowhere else (D2446), so a loader reading anything else is a page whose rows no file holds — the contract\'s fixture, while the stub is still there to answer with it',
  )
})

test('the built page prints the block\'s rows, row for row and cell for cell', () => {
  const snapshot = readSnapshotBlock(features())
  const printed = pageRows(builtPage())

  assert.equal(
    printed.length,
    snapshot.rows.length,
    `the built page prints ${printed.length} row(s) and the block carries ${snapshot.rows.length}: every row of the matrix is a row a reader reads (F13 criterion 2)`,
  )

  snapshot.rows.forEach((row, at) => {
    assert.deepEqual(
      printed[at],
      { name: row.name, explanation: row.explanation, status: row.status, plans: row.plans },
      `the built page's row ${at + 1} is not the block's row ${JSON.stringify(row.name)}: the page prints the matrix's own words, status and plan marks, and nothing of its own (F13 criterion 2)`,
    )
  })
})

test('neither FEATURES.md nor the built page carries a name, a section, a task id or a date', () => {
  const names = specNames()
  assert.ok(names.some((name) => name.includes('-')), 'the mapping names no hyphenated spec, so the leak check below would look for nothing')

  const file = leakProblems(features(), names)
  assert.deepEqual(file, [], `FEATURES.md carries what neither output may (R1, R8):\n  ${file.join('\n  ')}`)

  const page = leakProblems(flat(textOf(parse(builtPage()))), names)
  assert.deepEqual(page, [], `the built page carries what neither output may (R1, R8):\n  ${page.join('\n  ')}`)
})
