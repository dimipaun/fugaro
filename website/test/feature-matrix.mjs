// website/test/feature-matrix.mjs — the feature matrix's builder, and the refusals that make it
// a gate (docs/feature-matrix/implementation-plan.md, task T1; the spec as
// docs/feature-matrix/reconciliation.md amends it, with the owner's answers D2443–D2445).
//
// `website/src/lib/feature-matrix.mjs` is the repository's one reader of `docs/status.md` (R3),
// and I1's generator is its one caller: it runs at release, and what it refuses is what the
// notes PR repairs before the matrix is written. Three disagreements stop it — a name status.md
// carries that the mapping neither gives a feature nor excludes, a mapped name status.md no
// longer carries, and a row the previous FEATURES.md had Available that this snapshot would take
// back (F13 criterion 1, R7) — and a line it cannot read stops it at that line rather than being
// guessed at (F8).
//
// **The page this file reads is a fixture, not `docs/status.md`.** R2 keeps `npm run check` —
// which runs this file — off the real page: status.md changes several times a day through status
// sweeps, and a gate that read it would turn red on a sweep no website change can fix. So every
// case below reads `website/test/fixtures/feature-matrix/status.md`, and the cases that need a
// page the builder cannot read, or a page whose specs moved, edit that text.
//
// The statuses each case pins, in one place: Available is every contributing spec in Done and no
// `hold`; Coming soon is anything else that has started; Planned is nothing started; all-parked
// is left off; and a parked spec counts for nothing beside one that is not (D2443, D2444, R6).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { buildSnapshot } from '../src/lib/feature-matrix.mjs'
import { STATUSES, validateSnapshot } from '../src/lib/feature-matrix-model.mjs'

/** The page every case here reads, and the one page the builder never reads in production. */
const STATUS = readFileSync(new URL('./fixtures/feature-matrix/status.md', import.meta.url), 'utf8')

/** The release the cases stamp, which the contract's `as_of` accepts as written. */
const AS_OF = 'v9.9.9'

/**
 * One feature of the test's own mapping. Its words are made up and its specs are the fixture's
 * invented names: nothing here is a name the matrix may show (R1).
 */
function feature(id, plan, specs, hold) {
  const row = {
    id,
    name: id.replace(/-/g, ' '),
    explanation: 'One sentence on what this feature lets a reader do.',
    plan,
    specs,
  }
  if (hold) row.hold = hold
  return row
}

function features() {
  return [
    feature('shipped', 'community', ['done-spec', 'shipped-spec']),
    feature('in-flight', 'team', ['working-spec']),
    feature('nearly', 'community', ['started-spec']),
    feature('later', 'enterprise', ['promised-spec', 'idle-spec']),
    feature('just-merged', 'community', ['merged-spec']),
    feature('parked-only', 'community', ['parked-alone']),
    feature('parked-alongside', 'community', ['parked-spec', 'done-spec']),
    feature('held', 'community', ['held-spec'], { status: 'coming-soon', reason: 'Built, and not shipped yet.' }),
  ]
}

function mapping(extra = {}) {
  return {
    version: 1,
    features: features(),
    excluded: [{ spec: 'idle-other', reason: 'Not a spec: no plan and nothing to build.' }],
    ...extra,
  }
}

function snapshotOf(statusMarkdown = STATUS, { mapping: parsed = mapping(), previous = null, asOf = AS_OF } = {}) {
  return buildSnapshot({ statusMarkdown, mapping: parsed, previous, asOf })
}

function byId(snapshot) {
  return new Map(snapshot.rows.map((row) => [row.id, row]))
}

/** The fixture with one string replaced; every case that needs another page starts here. */
function edited(text, replacement) {
  assert.ok(STATUS.includes(text), `the fixture no longer carries ${JSON.stringify(text)}, and the case below edits it`)
  return STATUS.replace(text, replacement)
}

/** The 1-based line of the page carrying `text`, so a refusal is held to the line it names. */
function lineOf(markdown, text) {
  const at = markdown.split('\n').findIndex((line) => line.includes(text))
  assert.ok(at >= 0, `${JSON.stringify(text)} is not in the page this case built`)
  return at + 1
}

/** Where a parse refusal names a line: the page's path and the line number, as a grep prints it. */
function at(markdown, text) {
  return `docs/status.md:${lineOf(markdown, text)}`
}

function refusal(run) {
  try {
    run()
  } catch (err) {
    return err
  }
  return assert.fail('the builder returned a snapshot where this case expects a refusal')
}

test('a feature every one of whose specs is Done is Available', () => {
  const snapshot = snapshotOf()
  validateSnapshot(snapshot)

  assert.equal(snapshot.as_of, AS_OF)
  const shipped = byId(snapshot).get('shipped')
  assert.equal(shipped.status, 'available', 'both contributing specs are in the Done table')
  assert.deepEqual(
    shipped.plans,
    { community: true, team: true, enterprise: true },
    'a community feature is on its own plan and on every plan above it (D2440)',
  )
})

test('one contributing spec in Work in progress makes the row Coming soon', () => {
  const row = byId(snapshotOf()).get('in-flight')

  assert.equal(row.status, 'coming-soon', 'a spec in WIP has started and is not Done, so the row promises nothing yet')
  assert.deepEqual(
    row.plans,
    { community: false, team: true, enterprise: true },
    'the row names its lowest plan, and every plan above it is checked',
  )
})

test('a feature whose specs are all in To do with nothing merged is Planned', () => {
  const row = byId(snapshotOf()).get('later')

  assert.equal(row.status, 'planned', 'neither spec has merged a task, so nothing of the feature has started (D2443)')
  assert.deepEqual(row.plans, { community: false, team: false, enterprise: true })
})

test('a spec with merged tasks in To do or parked has started', () => {
  const snapshot = snapshotOf()

  assert.equal(
    byId(snapshot).get('just-merged').status,
    'coming-soon',
    'the State cell reports 2/5 merged, so the spec has started: Planned is only for specs nothing has merged',
  )
  assert.equal(byId(snapshot).get('nearly').status, 'coming-soon', 'a spec in Nearly There has started as well')
})

test('a feature whose specs are all parked is left off the matrix', () => {
  const snapshot = snapshotOf()

  assert.ok(!byId(snapshot).has('parked-only'), 'a parked spec is not a promise, and a row of nothing but parked specs is not a row (D2443)')
  assert.deepEqual(
    snapshot.rows.map((row) => row.id),
    ['shipped', 'in-flight', 'nearly', 'later', 'just-merged', 'parked-alongside', 'held'],
    'the rows are the mapping\'s own order, less the one feature left off',
  )
})

test('a parked spec counts for nothing beside a spec that is Done (R6)', () => {
  const row = byId(snapshotOf()).get('parked-alongside')

  assert.equal(row.status, 'available', 'the status comes from the specs that are not parked, and one of them is Done')
})

test('a hold in the mapping wins over the specs (D2444)', () => {
  const row = byId(snapshotOf()).get('held')

  assert.equal(row.status, 'coming-soon', 'its spec is Done, and the hold is what a built-but-unshipped feature carries')
})

test('the snapshot carries the three public statuses and none of status.md\'s own states', () => {
  const snapshot = snapshotOf()

  for (const row of snapshot.rows) {
    assert.ok(STATUSES.includes(row.status), `${row.id} carries ${JSON.stringify(row.status)}, which is not one of ${STATUSES.join(', ')}`)
  }
  assert.ok(
    !snapshot.rows.some((row) => ['done', 'started', 'todo', 'parked'].includes(row.status)),
    'a status.md state reached a row: the matrix shows capabilities, and progress through a plan is not one of them (D2443)',
  )
})

test('a name in status.md the mapping neither gives a feature nor excludes is refused, with its line', () => {
  const page = edited(
    '| promised-spec | 0/6: plan only, no task approved |',
    '| promised-spec | 0/6: plan only, no task approved |\n| iota-spec, kappa-spec | no plan or not a spec |',
  )

  const err = refusal(() => snapshotOf(page))
  assert.match(err.message, /iota-spec/, 'the refusal names every unmapped name, not the first')
  assert.match(err.message, /kappa-spec/, 'a second name in one cell is a name of its own (the Spec cell is split on commas)')
  assert.ok(err.message.includes(`line ${lineOf(page, 'iota-spec')}`), `the refusal does not name the line of the row it read: ${err.message}`)
  assert.ok(err.message.includes(`line ${lineOf(page, 'kappa-spec')}`), `the refusal does not name the line of the row it read: ${err.message}`)
})

test('a mapped name status.md does not carry is refused', () => {
  const withoutSpec = edited('| shipped-spec | none |\n', '')
  const err = refusal(() => snapshotOf(withoutSpec))
  assert.match(err.message, /shipped-spec/, 'the refusal names the mapping\'s name that the page lost')
  assert.match(err.message, /shipped/, 'and the feature that has no status to derive without it')

  const withoutExcluded = edited('| idle-spec, idle-other | no plan or not a spec |', '| idle-spec | no plan or not a spec |')
  const excluded = refusal(() => snapshotOf(withoutExcluded))
  assert.match(excluded.message, /idle-other/, 'an excluded name the page lost is drift in the mapping as well')
})

test('a row that was Available and comes back Coming soon is refused, with the row and the spec (D2445)', () => {
  const previous = snapshotOf()
  assert.equal(byId(previous).get('shipped').status, 'available', 'the case starts from a row the previous snapshot had Available')

  const page = edited(
    '| working-spec | 3/8 | two tasks, not started |',
    '| working-spec | 3/8 | two tasks, not started |\n| shipped-spec | 1/4 | one task left |',
  ).replace('| shipped-spec | none |\n', '')

  const err = refusal(() => snapshotOf(page, { previous }))
  assert.match(err.message, /shipped: available in the previous snapshot/, `the refusal names the row: ${err.message}`)
  assert.match(err.message, /shipped-spec/, `the refusal names the spec that caused it: ${err.message}`)
})

test('a hold added to the row is the one deliberate exception to that refusal (R7)', () => {
  const previous = snapshotOf()
  const page = edited(
    '| working-spec | 3/8 | two tasks, not started |',
    '| working-spec | 3/8 | two tasks, not started |\n| shipped-spec | 1/4 | one task left |',
  ).replace('| shipped-spec | none |\n', '')

  const held = mapping({
    features: features().map((row) => (row.id === 'shipped' ? { ...row, hold: { status: 'coming-soon', reason: 'Held on purpose.' } } : row)),
  })
  const snapshot = snapshotOf(page, { mapping: held, previous })

  assert.equal(byId(snapshot).get('shipped').status, 'coming-soon', 'the hold is what the row was held back by, and it is not a regression')
})

test('a row that was Available and is now missing is refused unless the id is retired (R7)', () => {
  const previous = snapshotOf(STATUS, { mapping: mapping({ features: [...features(), feature('dropped', 'community', ['done-spec'])] }) })
  assert.equal(byId(previous).get('dropped').status, 'available', 'the case starts from an Available row the next snapshot leaves off')

  const err = refusal(() => snapshotOf(STATUS, { previous }))
  assert.match(err.message, /dropped/, `the refusal names the id the snapshot lost: ${err.message}`)
  assert.match(err.message, /retire/, `and says what makes dropping a row deliberate: ${err.message}`)

  const retired = snapshotOf(STATUS, { mapping: mapping({ retired: ['dropped'] }), previous })
  assert.ok(!byId(retired).has('dropped'), 'a retired id is gone from the matrix, and that is what the mapping said it wanted')
})

test('with no previous snapshot the regression check does not run', () => {
  const page = edited(
    '| working-spec | 3/8 | two tasks, not started |',
    '| working-spec | 3/8 | two tasks, not started |\n| shipped-spec | 1/4 | one task left |',
  ).replace('| shipped-spec | none |\n', '')

  const snapshot = snapshotOf(page)

  assert.equal(byId(snapshot).get('shipped').status, 'coming-soon', 'the first generation has nothing to regress against (R7)')
})

test('a previous snapshot that cannot be read is refused', () => {
  const noBlock = refusal(() => snapshotOf(STATUS, { previous: 'a FEATURES.md with no fenced block in it' }))
  assert.match(noBlock.message, /json block/, `the block reader\'s own refusal is the one a caller sees: ${noBlock.message}`)

  const notASnapshot = refusal(() => snapshotOf(STATUS, { previous: { version: 1, as_of: 'v9.9.9' } }))
  assert.match(notASnapshot.message, /rows/, `the snapshot shape\'s own refusal names the missing field: ${notASnapshot.message}`)
})

test('a renamed table heading is refused, with its line', () => {
  const page = edited('## To do or parked', '## To do and parked')

  const err = refusal(() => snapshotOf(page))
  assert.ok(err.message.includes(at(page, '## To do and parked')), `the refusal names the line it stopped at: ${err.message}`)
  assert.match(err.message, /To do and parked/, 'and the heading it cannot read')
})

test('a section heading the page lost is refused', () => {
  const page = edited('## Work in progress (WIP)\n\n', '')

  const err = refusal(() => snapshotOf(page))
  assert.match(err.message, /Work in progress \(WIP\)/, `the refusal names the table it cannot find: ${err.message}`)
})

test('a row the builder cannot read is refused, with its line', () => {
  const short = edited('| shipped-spec | none |', '| shipped-spec |')
  const cells = refusal(() => snapshotOf(short))
  assert.ok(cells.message.includes(at(short, '| shipped-spec |')), `the refusal names the row: ${cells.message}`)

  const empty = edited('| merged-spec | 2/5 merged, the rest deferred |', '| merged-spec | |')
  const state = refusal(() => snapshotOf(empty))
  assert.ok(state.message.includes(at(empty, '| merged-spec |')), `the refusal names the row: ${state.message}`)

  const spaced = edited('| shipped-spec | none |', '| shipped spec | none |')
  const name = refusal(() => snapshotOf(spaced))
  assert.ok(name.message.includes(at(spaced, '| shipped spec |')), `the refusal names the row: ${name.message}`)
})

test('a name in two rows is refused', () => {
  const page = edited('| shipped-spec | none |', '| shipped-spec | none |\n| done-spec | none |')

  const err = refusal(() => snapshotOf(page))
  assert.match(err.message, /done-spec/, `the refusal names the name in both rows: ${err.message}`)
  assert.ok(err.message.includes(at(page, '| done-spec | none |')), `and where the second one is: ${err.message}`)
})
