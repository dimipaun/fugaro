// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Both document shapes, and nothing else (desktop-console plan Task 2.0;
// design section 5.1 rule 3; console/docs/json-contract.md sections 2 and 5).
//
// The plan's four claims about this file, one describe block each:
//
//   * **both document shapes decode** -- an ordinary `JSONDoc` (every live
//     document of the committed fixture, decoded, so the corpus is exercised
//     rather than assumed) and a `watchDegradedDoc`;
//   * **a degraded document is not mistaken for a normal one** -- the
//     discriminator is the `degraded` key, `decodeSnapshot` refuses the
//     degraded shape by name, and a decoded degraded document has no `total`
//     and no `repos` for a screen to read a zero out of;
//   * **an unknown shape yields the skew state and not an empty screen** -- `{}`
//     and a document with a rogue key are refused by name, and the skew says
//     what the console could not read;
//   * **a document missing a field the screens read fails decoding by name** --
//     `total`, `total.counted_usd`, `repos[0].repo`, `runs[0].repo`,
//     `runs[0].title`, `runs[0].round`, `runs[0].deadline`, each refused with
//     the wire path in the refusal.
//
// Two more are this file's own, because a decoder is where a wire near-miss
// becomes a wrong screen: **the classification is total** (every string leaf of
// every document either has a text class or is one of the declared non-text
// keys, walked, with a planted key to prove the walk can fail) and **the null
// pointers are unavailable and not zero** (`daily_cap_usd`, `percent`,
// `spent_usd`, `age_seconds`, `kill`).

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK, formatReading, readNumber, unavailable } from '../../src/shared/availability'
import {
  COORDINATOR_COLLISION_KEYS,
  COORDINATOR_PR_KEYS,
  COORDINATOR_RECORD_KEYS,
  COORDINATOR_REPO_KEYS,
  COORDINATOR_RUN_KEYS,
  COORDINATOR_STALE_KEYS,
  COORDINATOR_STATUS_KEYS,
  COORDINATOR_UNREAD_KEYS,
  COORDINATOR_VIEW_KEYS,
  DEGRADED_KEYS,
  FINISHED_KEYS,
  LIVE_KEYS,
  PLAN_KEYS,
  PLAN_NODE_KEYS,
  PLAN_RELEASE_KEYS,
  PLAN_MAX_LEVEL,
  PLAN_STATES,
  PLAN_STATUS_KEYS,
  PLAN_STREAM_KEYS,
  PLAN_TOTALS_KEYS,
  RECORD_KEYS,
  REPO_KEYS,
  RUN_KEYS,
  TOTAL_KEYS,
  LIVE_MARKERS,
  NON_TEXT_STRING_KEYS,
  WIRE_TEXT_CLASSES,
  decodeCoordinatorStatus,
  decodeDocument,
  decodePlanStatus,
  decodeSnapshot,
  knownTopLevelKeys,
  wireTextClass,
} from '../../src/shared/schema'
import type { CoordinatorStatusView, PlanState, PlanStatusView } from '../../src/shared/schema'
import type { LiveDocument } from '../../src/shared/schema'
import type { JSONDoc } from '../../src/source/source'
import { loadFixture } from '../../src/source/fixture'

const FIXTURE = readFileSync(join('test', 'fixtures', 'synthetic.ndjson'), 'utf8')

/** fixture is the committed fixture, verified by the loader (a fixture whose
 * marker or digest does not verify never gets this far). */
const fixture = loadFixture(FIXTURE, 'the synthetic fixture')

/** documents is every document the fixture carries, in order: the notices are
 * not documents, and the reader that owns them is `src/main/spawn.ts`. */
const documents: readonly JSONDoc[] = fixture.records
  .filter((record): record is { at: string; document: JSONDoc } => 'document' in record)
  .map((record) => record.document)

/** first is the fixture's first document, which every test here starts from. */
const first: JSONDoc = documents[0] ?? {}

/**
 * PLAN_STATUS is `test/fixtures/plan-status.json`: one `plan status --json`
 * document, written from the Go structs (plan GV2.1). It is not a recording and
 * carries no redactor header -- the read is a one-shot document rather than a
 * feed, so it is JSON and not `.ndjson`, and the loader that verifies a
 * recording never sees it. Its keys are pinned against the structs below.
 */
const PLAN_STATUS: JSONDoc = JSON.parse(
  readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8'),
) as JSONDoc

/**
 * RUNVIEW_SOURCE is `internal/runview/runview.go`, read from this file's own
 * location (the console tree sits two levels inside the repository).
 *
 * It is read rather than transcribed because the transcriptions are exactly what
 * failed: `RECORD_KEYS` and the list this file compared it against were two
 * hand-written readings of the same struct, so both omitted `artifact_count` and
 * `artifact_bytes` at once and neither copy could notice. The struct is the
 * authority -- the console reads the CLI's own json tags -- so the tags are read
 * off it.
 */
const RUNVIEW_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../internal/runview/runview.go', import.meta.url)),
  'utf8',
)

/** WATCH_SOURCE is `internal/watch/json.go`, read the same way and for the same
 * reason: `RUN_KEYS` and `FINISHED_KEYS` -- the two lists the Live screen's own
 * feed is decoded against -- were unexported and pinned against nothing, so
 * 0.9.0's five new keys on `JSONRun`/`JSONFinished` went missing from both and
 * only a hand-read of the structs could notice. They are read off the structs
 * now, exactly as `RECORD_KEYS` is. */
const WATCH_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../internal/watch/json.go', import.meta.url)),
  'utf8',
)

/**
 * PLAN_SOURCE and STATE_SOURCE are the two Go files the plan status document's
 * shape is written in (plan GV2.1), read the same way and for the same reason:
 * the document is one command's own, and a key the CLI gains without this
 * console gaining it is a field a screen would silently draw nothing for.
 *
 * They are two files rather than one because the read is: `plan_status.go`
 * writes the envelope and its figures, and `internal/plan/state.go` writes the
 * plans, tasks, releases and streams the envelope carries.
 */
const PLAN_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../internal/cli/plan_status.go', import.meta.url)),
  'utf8',
)
const STATE_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../internal/plan/state.go', import.meta.url)),
  'utf8',
)

/**
 * COORDINATOR_STATE_SOURCE, COORDINATOR_VIEW_SOURCE and COORDINATOR_SWEEP_SOURCE
 * are the three Go files the stored project view is written in (Bitbucket
 * credentials T1.2), read the same way and for the same reason. They are three
 * rather than one because the view is: `state.go` writes the envelope and the
 * repository node, `view.go` writes the records and the runs behind them, and
 * `sweep.go` writes the two keys only the sweep fills in -- the stale marker and
 * the unread list. A key one of them gains and this console does not is a field
 * a screen silently draws nothing for.
 */
const COORDINATOR_STATE_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../ee/coordinator/state.go', import.meta.url)),
  'utf8',
)
const COORDINATOR_VIEW_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../ee/coordinator/view.go', import.meta.url)),
  'utf8',
)
const COORDINATOR_SWEEP_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../ee/coordinator/sweep.go', import.meta.url)),
  'utf8',
)

/** structTag is one field's `json:` tag: the key it writes, and whether the tag
 * carries `omitempty`. */
interface StructTag {
  readonly key: string
  readonly optional: boolean
}

/** structTags is every `json:"…"` tag of one struct in `source`, in declaration
 * order. A field tagged `json:"-"` writes no key at all, so it is not one of
 * these. It throws rather than answering an empty list: a pin that silently
 * finds nothing would pass while checking nothing. `file` is only the name the
 * throw carries, so a struct that is not there reads as the file it was looked
 * for in. */
function structTags(
  source: string,
  name: string,
  file = 'internal/runview/runview.go',
): readonly StructTag[] {
  const start = source.indexOf(`type ${name} struct {`)
  if (start < 0) throw new Error(`${file} has no \`type ${name} struct\``)
  const end = source.indexOf('\n}', start)
  if (end < 0) throw new Error(`the ${name} struct is not closed at column 0, so its tags cannot be read`)
  const tags: StructTag[] = []
  for (const match of source.slice(start, end).matchAll(/`json:"([^"]*)"/g)) {
    const [key, ...options] = (match[1] ?? '').split(',')
    if (key === undefined || key === '' || key === '-') continue
    tags.push({ key, optional: options.includes('omitempty') })
  }
  if (tags.length === 0) throw new Error(`the ${name} struct carries no json tags to read`)
  return tags
}

/** clone deep-copies a document so a test can move one field without touching
 * the corpus. */
function clone(document: JSONDoc): Record<string, unknown> {
  return structuredClone(document) as Record<string, unknown>
}

/** patched is `clone` with one field set, addressed by a path. */
function patched(document: JSONDoc, path: readonly (string | number)[], value: unknown): JSONDoc {
  const copy = clone(document)
  let at: unknown = copy
  for (const step of path.slice(0, -1)) {
    if (at === null || typeof at !== 'object') throw new Error(`no ${String(step)} in the path`)
    at = (at as Record<string | number, unknown>)[step]
  }
  const last = path[path.length - 1]
  if (last === undefined || at === null || typeof at !== 'object') throw new Error('path is empty')
  ;(at as Record<string | number, unknown>)[last] = value
  return copy
}

/** without is `patched` with the field removed: the absent-key case. */
function without(document: JSONDoc, path: readonly (string | number)[]): JSONDoc {
  const copy = clone(document)
  let at: unknown = copy
  for (const step of path.slice(0, -1)) {
    if (at === null || typeof at !== 'object') throw new Error(`no ${String(step)} in the path`)
    at = (at as Record<string | number, unknown>)[step]
  }
  const last = path[path.length - 1]
  if (last === undefined || at === null || typeof at !== 'object') throw new Error('path is empty')
  delete (at as Record<string | number, unknown>)[last]
  return copy
}

/** decoded is a document that must decode, or the test that called it fails
 * with the refusal's own sentence. */
function decoded(raw: unknown, where = 'the document'): LiveDocument {
  const result = decodeDocument(raw, where)
  if (!result.ok) throw new Error(result.skew.because)
  if (result.value.shape !== 'live') throw new Error(`decoded a ${result.value.shape} document`)
  return result.value
}

/** label is a field path in the console's own notation (`runs[0].round`), which
 * is the notation a refusal names -- the plain `join('.')` a test would
 * otherwise write is a different string, and this test is about the strings. */
function label(path: readonly (string | number)[]): string {
  let out = ''
  for (const step of path) {
    out = typeof step === 'number' ? `${out}[${String(step)}]` : out === '' ? step : `${out}.${step}`
  }
  return out
}

/** refusalFor is the refusal of a document that must be refused. */
/**
 * ignoring asserts the decoder's rule for a key it does not know (the owner's
 * ruling of 2026-10-30): the document with the key added decodes, and to
 * exactly the value the document without it decodes to -- so the key is
 * neither a refusal nor carried into anything a screen could draw.
 */
function ignoring(original: unknown, withKey: unknown, decode: (raw: unknown) => { ok: boolean; value?: unknown }): void {
  const before = decode(original)
  const after = decode(withKey)
  expect(before.ok, 'the original decodes').toBe(true)
  expect(after.ok, 'the document with an unknown key decodes').toBe(true)
  expect(after.value).toEqual(before.value)
}

function refusalFor(raw: unknown): { field: string; name: string; because: string } {
  const result = decodeDocument(raw, 'the live feed document')
  if (result.ok) throw new Error('expected a refusal')
  return { ...result.skew.refusal, because: result.skew.because }
}

/**
 * DEGRADED is `watchDegradedDoc`: `lsDoc` **embedded** plus `degraded: true`
 * and a `notice` (`internal/cli/watch.go:401`). It is built here rather than
 * taken from the corpus because the corpus is a recording of the *live* feed --
 * and because the discrimination is the thing under test, so the document that
 * tests it should be written by hand.
 */
const DEGRADED: JSONDoc = Object.freeze({
  project: '<project>',
  runs: [
    {
      run: '20261015-120000-aa01',
      run_id: '20261015-120000-aa01',
      repo: 'r-4c25cf20/r-f1aa9214',
      workflow: 'web',
      oauth_account: 'redacted@example.invalid',
      recipe: 'console-text-path',
      status: 'succeeded',
      stage: 'done',
      reason: '',
      branch: 'feat/console-fixtures',
      outcome: 'ready',
      pr_url: 'https://example.invalid/r-4c25cf20/r-f1aa9214/pull/412',
      requested_by: 'redacted@example.invalid',
      log_url: 'https://example.invalid/logs/20261015-120000-aa01',
      created: '2026-10-15T10:15:00Z',
      cost: {
        // `runstore.Cost` has no `model_notional_usd` (the notional split is on
        // `runview.Totals`, once per sum): an earlier revision of this corpus
        // carried one, which is how a reading that is permanently unavailable
        // in production passed for tested.
        model_usd: 12.5,
        compute_usd: 0.4,
        compute_estimated: true,
        total_usd: 12.9,
        estimate: true,
        model_basis: 'subscription',
        model_source: 'gateway',
        compute_source: 'n1-standard-4',
        model_by: { 'claude-sonnet-4-5-20250929': 12.5 },
        reported_usd: 12.4,
        unreconciled: 0.1,
        usage_unparsed: 2,
        catalog_stale: false,
      },
    },
  ],
  totals: {
    runs: 1,
    model_usd: 12.5,
    model_notional_usd: 3.25,
    compute_usd: 0.4,
    total_usd: 12.9,
    compute_not_estimated: 0,
  },
  warnings: ['the repository list came from the bucket'],
  degraded: true,
  notice: 'no budget backend: showing run records only',
})

/**
 * FULL_ROW is a `runview.Row` as `runs ls --json` writes one: **every** key the
 * struct can write, the four without `omitempty` (`terminal`, `settled`,
 * `pushed`, `follow_up`) among them, and the optional ones populated
 * (`internal/runview/runview.go:57`; contract section 5).
 *
 * It exists because the hand-built `DEGRADED` above is a *readable* document and
 * not a *realistic* one: it was written from the fields this decoder read, so it
 * could not catch a key the decoder's `closed()` did not know about -- and one
 * did not (eleven of the thirty-six), which made every real degraded document
 * refuse at `runs[0].terminal`. This is the same probe a reviewer would run.
 */
const FULL_ROW: JSONDoc = Object.freeze({
  run: '20261015-120000-aa01',
  repo: 'r-4c25cf20/r-f1aa9214',
  workflow: 'web',
  oauth_account: 'redacted@example.invalid',
  recipe: 'console-text-path',
  // `fugaro run --title` (0.8.5): the run's title, from the stored task.
  title: 'wire the console to the live feed',
  project_layer: { name: 'base', sha256: 'a'.repeat(64) },
  config_sha256: 'b'.repeat(64),
  run_id: '20261015-120000-aa01',
  status: 'failed',
  stage: 'done',
  reason: 'the Batch job was preempted: its Spot VM was reclaimed before the run finalized',
  // The read side's derivation, written on a row whose status was derived while
  // reading (`runview.DerivedPreempted`, the CLI's 2026-10-21 ruling): a Cloud
  // Batch job the platform preempted, on a run whose record was never
  // finalized. It sits beside `status` rather than replacing it, which is what
  // keeps the status vocabulary a reader switches on intact.
  derived: 'preempted',
  halt: { by: 'redacted@example.invalid', at: '2026-10-15T11:00:00Z', reason: 'budget kill <project>' },
  batch: '2026-10-15',
  requested_by: 'redacted@example.invalid',
  // Who started the run, as its own `launch.json` recorded it (0.9.0). The
  // key the struct's pin caught `RECORD_KEYS` missing: it sits between
  // `requested_by` and `pr_url`, and a real `runs ls --json` row carries it.
  launched_by: 'redacted@example.invalid',
  // The plan task the run says it works on (GV1.2, decision GD5), between
  // `launched_by` and `pr_url`: written on every row whose `launch.json`
  // recorded one, and absent for every run launched without `--plan-task`.
  plan_task: 'setup-wizard/W1.3',
  pr_url: 'https://example.invalid/r-4c25cf20/r-f1aa9214/pull/412',
  execution: 'fugaro-20261015-120000-aa01',
  log_url: 'https://example.invalid/logs/20261015-120000-aa01',
  created: '2026-10-15T10:15:00Z',
  cost: {
    model_usd: 12.5,
    compute_usd: 0.4,
    compute_estimated: true,
    total_usd: 12.9,
    estimate: false,
    model_basis: 'api-list',
  },
  image_age_s: 3600,
  terminal: true,
  settled: true,
  branch: 'feat/console-fixtures',
  outcome: 'ready',
  // The run's own record of the paths it changed (console v2 design D27, F17;
  // `runstore.ChangedFiles`): absent for a run that recorded none.
  changed_files: { paths: ['console/src/shared/schema.ts', 'internal/runview/runview.go'], total: 2 },
  // The mode the run resolved, the layer that decided it and the areas that
  // matched, each with the mode it sets and the moment it was applied at
  // (0.11.0, docs/rigor-modes D23/D24/D25; `runstore.RigorRecord`, between
  // `changed_files` and `artifact_count` in the struct). Accepted and not read
  // -- no screen draws a run's mode yet -- and carried here so the
  // classification walk sees its string keys: `from` is an enum of the
  // resolver's own layers, the areas' `mode` is the same enum as the block's,
  // and `revision` is a git ref.
  rigor: {
    mode: 'critical',
    from: 'area',
    areas: [
      { name: 'payments', mode: 'critical', at: 'bootstrap' },
      { name: 'docs', mode: 'poc', at: 'finalize' },
    ],
    at: 'finalize',
    revision: 'origin/main',
  },
  // What a run that delivered artifacts carries (`internal/runview/runview.go`,
  // between `outcome` and `task_pr`): the record's own count and byte total.
  // Absent for every run that opens a pull request, which is why the omission
  // from `RECORD_KEYS` survived this file -- `DEGRADED` and the committed
  // fixture are both rows of that kind.
  artifact_count: 3,
  artifact_bytes: 12_345_678,
  task_pr: 411,
  record_pr: 412,
  pr: 412,
  pushed: true,
  pushed_head: 'c'.repeat(40),
  started_at: '2026-10-15T10:20:00Z',
  base_branch: 'main',
  previous_run: '20261015-101500-aa02',
  follow_up: true,
  draft_fallback: false,
  pr_status_at: '2026-10-15T11:30:00Z',
  stale_draft: false,
})

/** DEGRADED_REAL is `watchDegradedDoc` carrying that row: the document the CLI
 * actually writes for a project with no budget backend. */
const DEGRADED_REAL: JSONDoc = Object.freeze({
  project: '<project>',
  runs: [FULL_ROW],
  totals: {
    runs: 1,
    model_usd: 12.5,
    model_notional_usd: 0,
    compute_usd: 0.4,
    total_usd: 12.9,
    compute_not_estimated: 0,
  },
  warnings: [],
  degraded: true,
  notice: 'no budget backend: showing run records only',
})

/** FULL_LIVE_RUN is a `JSONRun` as `watch --json` writes one: **every** key the
 * struct can write (`internal/watch/json.go`), 0.9.0's five and 0.10.0's two
 * among them. The committed fixture predates them all, so a document built from
 * it alone cannot catch a `RUN_KEYS` that is missing one -- which is how the
 * Live screen's own feed came to be refused whole, while this suite stayed
 * green. This row is `first`'s own first run plus the keys the recording
 * predates, and the test below holds it to that: the row's key set is compared
 * with `RUN_KEYS` itself, so a key added to the list and not to the row is a
 * failure rather than a fixture that quietly stopped covering it. */
const FULL_LIVE_RUN: JSONDoc = Object.freeze({
  run: '20261015-120000-aa01',
  slug: 'slug-6a7c7d06',
  repo: 'r-4c25cf20/r-f1aa9214',
  title: 'wire the console to the live feed',
  stage: 'implement',
  round: '2',
  verify: 'pending',
  models: 'claude-sonnet-4-5-20250929 / claude-opus-4-1',
  recipe: 'console-text-path',
  auth: 'oauth',
  notional: true,
  spent_usd: 12.5,
  age_seconds: 1761,
  health: 'lost',
  deadline: 'ok',
  halted: 'budget kill fixture-sandbox',
  queued: false,
  stuck: false,
  workflow: 'web',
  requested_by: 'redacted@example.invalid',
  // 0.9.0: the Cloud Run job the run is in, the (profile, provider, account)
  // its name was computed from, and who launched it (task 3.5; `launched_by`).
  job: 'fugaro-proj-web',
  profile: 'web',
  provider: 'github',
  account: 'redacted@example.invalid',
  launched_by: 'redacted@example.invalid',
  // GV1.2 (decision GD5): the plan task the run was launched to work on, as
  // its own `launch.json` recorded it. `JSONRun` and `JSONFinished` carry it;
  // the Live screen draws no column for it yet (GV2.3's graph is the reader).
  plan_task: 'setup-wizard/W1.3',
  // GV2.5: the pull request number the run has recorded, once it has one — a
  // draft PR opens early in a run, so this row carries it while the run is
  // still working. It is read, where `plan_task` beside it is only accepted
  // and not yet drawn.
  pr_number: 411,
  // 0.10.0 (credentials-service C1.21): the name of the credential the run's
  // cascade is spending on -- a name, never a value -- and the instant a run
  // held up for one expects to resume at. Both are `JSONRun`'s alone: a
  // finished run is spending on nothing and waiting for nothing.
  credential: 'acme-personal',
  waiting_until: '2026-10-15T12:15:00Z',
})

/** FULL_FINISHED_RUN is a `JSONFinished` as `watch --json` writes one: every key
 * of that struct, 0.9.0's five among them -- a reader that follows a run out of
 * `runs[]` into `finished[]` lands on this shape. */
const FULL_FINISHED_RUN: JSONDoc = Object.freeze({
  run: '20261015-113000-aa02',
  slug: 'slug-6a7c7d06',
  repo: 'r-4c25cf20/r-f1aa9214',
  title: 'rotate the fixture tokens',
  status: 'succeeded',
  outcome: 'ready',
  failed: false,
  pr_url: 'https://example.invalid/r-4c25cf20/r-f1aa9214/pull/411',
  pr_number: 411,
  age_seconds: 3600,
  job: 'fugaro-proj-web',
  profile: 'web',
  provider: 'github',
  account: 'redacted@example.invalid',
  launched_by: 'redacted@example.invalid',
  // GV1.2: a finished run does not stop having been asked to work on a task.
  plan_task: 'setup-wizard/W1.3',
})

/** LIVE_REAL is the committed fixture's first document carrying those two rows:
 * a live document with every key `JSONRun` and `JSONFinished` can write, which
 * is the document a 0.9.0 CLI actually writes and this suite had none of. */
const LIVE_REAL: JSONDoc = Object.freeze(
  patched(patched(first, ['runs'], [FULL_LIVE_RUN]), ['finished'], [FULL_FINISHED_RUN]),
)

describe('both document shapes decode', () => {
  it('decodes every document of the committed fixture', () => {
    expect(documents.length).toBeGreaterThan(0)
    for (const [index, document] of documents.entries()) {
      const value = decoded(document, `record ${String(index)}`)
      expect(value.shape, `record ${String(index)}`).toBe('live')
      expect(value.project, `record ${String(index)}`).toBe('<project>')
      expect(value.connection.state).toMatch(/^(live|stale|offline|refused|polling)$/)
    }
  })

  it('reads the fields the live screens read off the first record', () => {
    const value = decoded(first)
    expect(value.mode).toBe('observe')
    expect(value.day).toBe('2026-10-15')
    expect(value.now).toBe('2026-10-15T12:00:00Z')
    expect(value.repos).toHaveLength(1)
    expect(value.repos[0]?.repo).toBe('r-4c25cf20/r-f1aa9214')
    expect(value.runs[0]?.title).toBe('wire the console to the live feed')
    expect(value.runs[0]?.round).toBe('2')
    expect(value.runs[0]?.deadline).toBe('ok')
    expect(value.finished).toEqual([])
    expect(value.ready).toEqual([])
    // `queued_note` is `omitempty`: absent is the normal case, and it reads as
    // "the bucket was read", which is not the same as an empty sentence.
    expect(value.queuedNote).toBeNull()
    // `pr_number` is `omitempty` on a live run too, and this recording predates
    // the key (GV2.5): absent reads as "no pull request recorded", never as the
    // `0` no repository numbering from 1 ever issues.
    expect(value.runs[0]?.prNumber).toBeNull()
  })

  it('reads a queued run, whose round the CLI writes as a dash', () => {
    // The near-miss this corpus exists for: `round` is a string on this shape
    // and an int on `budget show --json`. A decoder written from the other one
    // refuses every queued row, and the row below is the one that catches it.
    const last = documents[documents.length - 1] ?? {}
    const value = decoded(last)
    const queued = value.runs.find((run) => run.queued)
    expect(queued).toBeDefined()
    expect(queued?.round).toBe('-')
    expect(queued?.verify).toBe('-')
    expect(queued?.models).toBe('-')
    expect(queued?.auth).toBe('-')
    // `spent_usd` is null on a queued run: nothing is recorded yet, and the
    // reading says so rather than reading as a spend of zero.
    expect(queued?.spentUsd.state).toBe('unavailable')
    expect(formatReading(queued?.spentUsd ?? readNumber(null, 'x'), 'usd')).toBe(UNAVAILABLE_MARK)
  })

  it('decodes the degraded shape, which is a different document and not a subset', () => {
    const result = decodeDocument(DEGRADED, 'the degraded feed document')
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.value.shape).toBe('degraded')
    if (result.value.shape !== 'degraded') return
    expect(result.value.notice).toBe('no budget backend: showing run records only')
    expect(result.value.warnings).toHaveLength(1)
    expect(result.value.runs[0]?.run).toBe('20261015-120000-aa01')
    expect(result.value.runs[0]?.cost.modelBasis).toBe('subscription')
    // The half the degraded shape cannot carry. A screen that read these off it
    // would draw zeros for a document that never had the numbers.
    expect(result.value).not.toHaveProperty('total')
    expect(result.value).not.toHaveProperty('repos')
    expect(result.value).not.toHaveProperty('connection')
  })

  it('decodes a degraded document carrying every key `runview.Row` can write', () => {
    // The finding this test exists for: `closed()` refuses a key the shape does
    // not list, so a `RECORD_KEYS` written from the fields the console reads
    // rather than from the struct refuses the very document this shape is for.
    const result = decodeDocument(DEGRADED_REAL, 'a degraded document')
    expect(result.ok, result.ok ? '' : result.skew.because).toBe(true)
    if (!result.ok || result.value.shape !== 'degraded') return
    const row = result.value.runs[0]
    expect(row?.run).toBe('20261015-120000-aa01')
    expect(row?.runId).toBe('20261015-120000-aa01')
    expect(row?.status).toBe('failed')
    expect(row?.cost.modelUsd.value).toBe(12.5)
    expect(row?.cost.modelBasis).toBe('api-list')
    // The optional keys it does not read are accepted rather than refused.
    expect(row?.workflow).toBe('web')
    expect(row?.prUrl).toBe('https://example.invalid/r-4c25cf20/r-f1aa9214/pull/412')
  })

  it('accepts the read side\'s derived status, which is additive', () => {
    // `derived` arrived with the read-side preemption derivation (2026-10-21):
    // a row whose record was never finalized and whose Cloud Batch job the
    // platform preempted reads `status: "failed"`, `derived: "preempted"`.
    // `closed()` refuses a key the shape does not list, so a `RECORD_KEYS`
    // without it turns every document carrying one preempted run into the skew
    // state -- `title`'s omission and `launched_by`'s, a third time.
    const result = decodeDocument(DEGRADED_REAL, 'a degraded document')
    expect(result.ok, result.ok ? '' : result.skew.because).toBe(true)
    // The key is optional, so the *shape* is unchanged for every row that is
    // not derived; and it is a string the CLI writes from its own table, which
    // is why it is exempt from the text classes rather than classified as one.
    expect(wireTextClass('derived')).toBeUndefined()
    expect(NON_TEXT_STRING_KEYS).toContain('derived')
    // And it is not the status: the vocabulary a reader switches on is intact.
    expect(RECORD_KEYS).toContain('status')
    expect(RECORD_KEYS).toContain('derived')
  })

  it('accepts a runs list whose rows carry plan_task, classed as a label', () => {
    // GV1.2: `runs ls --json` writes `plan_task` on a row whose launch.json
    // recorded a well-formed one. A list carrying it must decode, not read as
    // skew, and the key must sit beside `launched_by` and `rigor` in the shape.
    const withPlanTask = patched(DEGRADED_REAL, ['runs', 0, 'plan_task'], 'setup-wizard/W1.3')
    const result = decodeDocument(withPlanTask, 'a runs list with plan_task')
    expect(result.ok, result.ok ? '' : result.skew.because).toBe(true)
    expect(RECORD_KEYS).toContain('plan_task')
    expect(RECORD_KEYS).toContain('rigor')
    expect(RECORD_KEYS.indexOf('plan_task')).toBe(RECORD_KEYS.indexOf('launched_by') + 1)
    expect(wireTextClass('plan_task')).toBe('label')
  })

  it('decodes a live document carrying every key `JSONRun` and `JSONFinished` can write', () => {
    // The same finding, on the shape the console actually consumes: `runs[]`,
    // `finished[]` and `ready[]` are closed too, and 0.9.0's five new keys on
    // both structs were in neither list, so a real `watch --json` document was
    // refused whole -- the Live screen's only feed, drawn as the skew state.
    const result = decodeDocument(LIVE_REAL, 'a live document')
    expect(result.ok, result.ok ? '' : result.skew.because).toBe(true)
    if (!result.ok || result.value.shape !== 'live') return
    expect(result.value.runs[0]?.run).toBe('20261015-120000-aa01')
    expect(result.value.runs[0]?.requestedBy).toBe('redacted@example.invalid')
    // `launched_by` is decoded now, whole (owner request 2026-10-18): the
    // Started-by column's short form is the screen's cut, not the model's.
    expect(result.value.runs[0]?.launchedBy).toBe('redacted@example.invalid')
    // GV2.5's `pr_number` is read on a live row as it is on a finished one: the
    // number, where the key is absent it is `null` (the record above).
    expect(result.value.runs[0]?.prNumber).toBe(411)
    expect(result.value.finished[0]?.run).toBe('20261015-113000-aa02')
    expect(result.value.finished[0]?.prNumber).toBe(411)
  })

  it('lists every key of `JSONRun` and `JSONFinished`, the Live screen\'s own shapes', () => {
    // Pinned against the structs, as `RECORD_KEYS` and `REPO_KEYS` are. These
    // two were unexported and pinned against nothing, which is how they drifted:
    // 0.9.0 added `job`, `profile`, `provider`, `account` and `launched_by` to
    // both structs (`internal/watch/json.go`) and neither list had them. No
    // test noticed, because the committed fixture is a recording from before the
    // fields -- so the console was green while refusing every 0.9.0 document.
    // 0.10.0's `credential` and `waiting_until` (credentials-service C1.21) are
    // the second round of the same drift: this branch added them to `JSONRun`,
    // and both console jobs went red on the comparison below.
    for (const [name, list] of [
      ['JSONRun', RUN_KEYS],
      ['JSONFinished', FINISHED_KEYS],
    ] as const) {
      const fromTheStruct = structTags(WATCH_SOURCE, name).map((tag) => tag.key)
      expect([...list].sort(), name).toEqual([...fromTheStruct].sort())
      expect(list.length, name).toBe(fromTheStruct.length)
      // The five keys the 0.9.0 pin exists for, named: a struct that lost one
      // would otherwise make the comparison agree with a list that lost it too.
      for (const found of ['job', 'profile', 'provider', 'account', 'launched_by']) {
        expect(fromTheStruct, `${name} carries ${found}`).toContain(found)
      }
    }
    // GV1.2's `plan_task` is named too, on both shapes: it was the third key
    // this comparison could have agreed with two lists that lost it together,
    // and the shape's own contract row now carries it.
    for (const name of ['JSONRun', 'JSONFinished'] as const) {
      expect(structTags(WATCH_SOURCE, name).map((tag) => tag.key), `${name} carries plan_task`).toContain('plan_task')
    }
    // 0.10.0's two are named as well, and they are `JSONRun`'s alone -- a
    // finished run spends nothing and waits for nothing -- so they are asserted
    // here rather than in the loop above, which walks both structs. Naming them
    // is what keeps the comparison from agreeing with two lists that lost one
    // together, which is exactly what happened to the five.
    const runKeys = structTags(WATCH_SOURCE, 'JSONRun').map((tag) => tag.key)
    for (const found of ['credential', 'waiting_until']) {
      expect(runKeys, `JSONRun carries ${found}`).toContain(found)
      expect(FINISHED_KEYS, `JSONFinished does not carry ${found}`).not.toContain(found)
    }
    // The fixture above claims to carry every key of the shape, and a claim in a
    // comment is what let the drift happen twice: the row's own key set is held
    // to the list, so a key added to `RUN_KEYS` and forgotten here fails this
    // test instead of quietly falling out of the classification walk below,
    // which can only see the keys its documents carry.
    expect([...Object.keys(FULL_LIVE_RUN)].sort()).toEqual([...RUN_KEYS].sort())
    expect([...Object.keys(FULL_FINISHED_RUN)].sort()).toEqual([...FINISHED_KEYS].sort())
    // And a key neither struct writes -- a field a newer CLI added -- is
    // ignored on each of them: the row decodes as it would without it.
    for (const path of [
      ['runs', 0, 'rogue'],
      ['finished', 0, 'rogue'],
    ] as const) {
      ignoring(LIVE_REAL, patched(LIVE_REAL, path, 1), (raw) => decodeDocument(raw))
    }
  })

  it('lists every key of `runview.Row`, and refuses one the struct cannot write', () => {
    // The list, pinned against the struct it was read off -- **the struct
    // itself**, not a second copy of the list. This test used to hold a
    // hand-written copy of the same reading, so when `artifact_count` and
    // `artifact_bytes` were missing from `RECORD_KEYS` they were missing from
    // both sides and the comparison agreed: a real `runs ls --json` row from a
    // run that delivered artifacts was refused whole as `unknown-key`, and this
    // test passed.
    const tags = structTags(RUNVIEW_SOURCE, 'Row')
    const fromTheStruct = tags.map((tag) => tag.key)
    for (const found of ['artifact_count', 'artifact_bytes']) {
      expect(fromTheStruct, `${found} is the omission this pin exists for`).toContain(found)
    }
    // GV1.2's `plan_task` (decision GD5), named for the same reason: the
    // comparison below cannot catch a key both sides lost together.
    expect(fromTheStruct, 'plan_task is the omission this pin exists for').toContain('plan_task')
    expect([...RECORD_KEYS].sort()).toEqual([...fromTheStruct].sort())
    expect(RECORD_KEYS.length).toBe(fromTheStruct.length)
    // The keys without `omitempty` are the ones whose absence broke it, and they
    // are the struct's own reading as well: a required tag this fixture does not
    // carry would make `FULL_ROW` a document the CLI cannot write.
    const required = tags.filter((tag) => !tag.optional).map((tag) => tag.key)
    expect(required).toContain('terminal')
    for (const always of required) {
      expect(RECORD_KEYS, always).toContain(always)
      expect(Object.keys(FULL_ROW), always).toContain(always)
    }
    // And a key no version of `Row` writes yet -- a field a newer CLI added --
    // is ignored, and the row decodes as it would without it.
    ignoring(DEGRADED_REAL, patched(DEGRADED_REAL, ['runs', 0, 'rogue'], 1), (raw) => decodeDocument(raw))
  })

  it("lists every key of `JSONRepo`, and refuses the two a total carries alone", () => {
    // Review round 5 of PR 478. `REPO_KEYS` was `['slug', 'repo', ...TOTAL_KEYS]`,
    // and a `repos[]` entry is *not* a total: `runs` and `run_hours` are written
    // by `JSONTotal` alone (`internal/watch/json.go:42`). Spreading the total's
    // list in meant the console accepted those two on a repository -- keys no
    // version of the CLI can write there -- and decoded the document as
    // understood instead of becoming the skew state, which is the one thing the
    // live half of this module promises for a key it does not know.
    // The struct's own keys, read off `internal/watch/json.go` through
    // `structTags` as the sibling pins in this file read theirs -- the same
    // finding this test exists for was a hand-written copy agreeing with a
    // hand-written list, so the copy is gone.
    const fromTheStruct = structTags(WATCH_SOURCE, 'JSONRepo').map((tag) => tag.key)
    // `leased_usd` is a `JSONRepo` key now (`JSONRepo.LeasedUSD`), so the list
    // holds exactly the struct's keys: it was carried here as an approved
    // *addition* while no CLI wrote it, and the struct caught up. The recorded
    // entry below carries it too, which is what keeps this honest -- the list
    // and the document both come from the struct.
    expect([...REPO_KEYS].sort()).toEqual([...fromTheStruct].sort())
    expect(REPO_KEYS.length).toBe(fromTheStruct.length)
    expect(fromTheStruct).toEqual([
      'slug', 'repo', 'counted_usd', 'spent_usd', 'notional_usd', 'leased_usd',
      'daily_cap_usd', 'percent', 'burn', 'kill',
    ])
    for (const notARepository of ['runs', 'run_hours']) {
      expect(REPO_KEYS, notARepository).not.toContain(notARepository)
      // The same two keys are the total's, and the document above still writes
      // them there: dropping them from `repos[]` must not have dropped them
      // from `total`, where they are accepted (read as neither yet, which is
      // what `TotalsView` says) rather than refused.
      expect(Object.keys(first.total ?? {}), notARepository).toContain(notARepository)
      // On a repository they are keys the console does not read, and ignored.
      ignoring(first, patched(first, ['repos', 0, notARepository], 1), (raw) => decodeDocument(raw))
    }
    // And the entry the fixture does carry still decodes, key by key: the list
    // is what the struct writes, not merely what the test asserts.
    const value = decoded(first)
    const repo = value.repos[0]
    expect(repo).toBeDefined()
    const wireRepo = (first as { repos?: readonly Record<string, unknown>[] }).repos?.[0] ?? {}
    expect(Object.keys(wireRepo).sort()).toEqual([...fromTheStruct].sort())
    expect(repo?.repo).toBe('r-4c25cf20/r-f1aa9214')
    expect(repo?.countedUsd.value).toBeGreaterThan(0)
    expect(value.total.countedUsd.state).toBe('measured')
  })

  it("lists every key of `JSONTotal`, and only the struct's own", () => {
    // `TOTAL_KEYS` was unexported and pinned against nothing, so this is its
    // pin, read off the struct the same way as `REPO_KEYS` above: a key added
    // to `JSONTotal` and not to the list would make `closed()` refuse the whole
    // feed, which is the drift `REPO_KEYS` and `RECORD_KEYS` were each missing
    // once. `leased_usd` used to be the one entry the struct did not have
    // (`approvedAheadOfTheCLI` in `docs_desktop_console_test.go`, the same
    // allowance on the Go side); the CLI writes it now, so the two lists are
    // the struct's keys, exactly.
    const fromTheStruct = structTags(WATCH_SOURCE, 'JSONTotal').map((tag) => tag.key)
    expect(fromTheStruct).toEqual([
      'counted_usd', 'spent_usd', 'notional_usd', 'leased_usd', 'daily_cap_usd',
      'percent', 'max_parallel', 'burn', 'kill', 'runs', 'run_hours',
    ])
    expect([...TOTAL_KEYS].sort()).toEqual([...fromTheStruct].sort())
    expect(TOTAL_KEYS.length).toBe(fromTheStruct.length)
    // And the recorded total still carries exactly the struct's keys, so the
    // addition did not come at the cost of the document the fixture holds.
    //
    // `max_parallel` is the one key the fixture is *approved to lack*: the
    // synthetic documents are written by `synthetic.ts` and committed
    // byte-identical -- `fixture.test.ts` regenerates the file from it and
    // compares -- so putting the key in them is that module's own change and not
    // this pin's. It is the same allowance the Go side makes for the recording it
    // walks (`mayLackOldRecording`, `docs_desktop_console_test.go`): the list is
    // the struct's, whether or not the document the fixture holds is old enough
    // to carry every key.
    const approvedAheadOfTheFixture = ['max_parallel']
    const wireTotal = (first as { total?: Record<string, unknown> }).total ?? {}
    expect(Object.keys(wireTotal).sort()).toEqual(
      [...fromTheStruct].filter((key) => !approvedAheadOfTheFixture.includes(key)).sort(),
    )
    // ... and a document that does carry it is accepted rather than refused,
    // drawing nothing: the key is the struct's, and no screen reads it yet.
    ignoring(first, patched(first, ['total', 'max_parallel'], 4), (raw) => decodeDocument(raw))
  })

  it('carries notional as the wire wrote it, and never infers it from auth', () => {
    // `notional` is `e.Auth == "oauth"` on the way *out* of the CLI
    // (`internal/watch/build.go:394`). A decoder that recomputed it from `auth`
    // would be a second implementation of a rule the wire already decided --
    // and would disagree with the CLI the day the rule changes.
    const value = decoded(first)
    expect(value.runs[0]?.auth).toBe('oauth')
    expect(value.runs[0]?.notional).toBe(true)
    const disagreeing = decoded(patched(first, ['runs', 0, 'notional'], false))
    expect(disagreeing.runs[0]?.notional).toBe(false)
    expect(disagreeing.runs[0]?.auth).toBe('oauth')
    // And the two accounts are separate readings, so neither can be summed into
    // the other by accident.
    expect(value.total.countedUsd.state).toBe('measured')
    expect(value.total.notionalUsd.value).toBe(3.25)
    expect(value.total.countedUsd.value).not.toBe(value.total.notionalUsd.value)
  })

  it('names the keys of both shapes, and the markers that identify the live one', () => {
    expect(knownTopLevelKeys('live')).toEqual(LIVE_KEYS)
    expect(knownTopLevelKeys('degraded')).toEqual(DEGRADED_KEYS)
    // The markers are the live shape's own keys: a document carrying one of
    // them is the live feed, and the degraded document carries none.
    for (const marker of LIVE_MARKERS) {
      expect(LIVE_KEYS).toContain(marker)
      expect(DEGRADED_KEYS).not.toContain(marker)
    }
    expect(LIVE_MARKERS.length).toBeGreaterThan(0)
    // The corpus exercises every live key but `queued_note`, which is the one
    // key its shape may leave out -- asserted rather than assumed, because a
    // key added to `LIVE_KEYS` and never carried by a document is a key the
    // walk below would never see.
    const carried = new Set<string>()
    for (const document of documents) for (const key of Object.keys(document)) carried.add(key)
    expect([...carried].sort()).toEqual(LIVE_KEYS.filter((key) => key !== 'queued_note').sort())
  })
})

describe('a degraded document is not mistaken for a normal one', () => {
  it('is refused by the snapshot door, by name', () => {
    const result = decodeSnapshot(DEGRADED, 'the snapshot')
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.skew.name).toBe('skew')
    expect(result.skew.refusal.name).toBe('wrong-shape')
    expect(result.skew.refusal.field).toBe('degraded')
    expect(result.skew.because).toContain('the snapshot')
    expect(result.skew.because).toContain('wrong-shape')
  })

  it('is not admitted when its `degraded` key is not `true`', () => {
    // `watchDegradedDoc` always writes `true` (`internal/cli/watch.go:403`), so
    // `false` is a document no CLI writes. Reading it as a live document would
    // be a guess about what the writer meant; refusing it says so.
    for (const value of [false, 'true', 1, null, {}]) {
      const refusal = refusalFor(patched(DEGRADED, ['degraded'], value))
      expect(refusal.name, JSON.stringify(value)).toBe('degraded-not-true')
      expect(refusal.field).toBe('degraded')
    }
  })

  it('refuses a document that claims to be both shapes', () => {
    // The other direction, and the one a reader would not guess: a document
    // carrying `degraded: true` *and* the live feed's keys is neither shape.
    // No version of `watch` writes both, so the refusal is by the live marker
    // that makes the contradiction visible rather than by whichever key the
    // degraded shape happened to check first.
    const refusal = refusalFor(patched(first, ['degraded'], true))
    expect(refusal.name).toBe('wrong-shape')
    expect(LIVE_MARKERS).toContain(refusal.field)
  })
})

describe('an unknown shape yields the skew state', () => {
  it('refuses a document that is not an object at all', () => {
    for (const value of [null, undefined, 'a string', 42, true, [], ['a']]) {
      const refusal = refusalFor(value)
      expect(refusal.name, JSON.stringify(value)).toBe('not-an-object')
      expect(refusal.because).toContain('the live feed document')
    }
  })

  it('refuses a document with none of either shape`s keys, rather than reading it as empty', () => {
    // `{}` is not an empty feed. An empty feed is `repos: []` with a connection,
    // a total and a day -- this is a document the console cannot read, and the
    // difference is the whole reason Task 3.2 has a skew state.
    for (const value of [{}, { hello: 'world' }, { hello: 'world', project: '<project>' }]) {
      const refusal = refusalFor(value)
      expect(refusal.name, JSON.stringify(value)).toBe('unknown-shape')
      expect(refusal.because).toContain('unknown-shape')
    }
  })

  it('ignores a key it does not know, at every level, rather than refusing the document', () => {
    // The owner's ruling of 2026-10-30, red first: these documents carry the
    // kind of fields a newer CLI adds (CLI 0.13/0.14-style rigor, governance,
    // compute, authorship, A/B settings and pairing keys). Each decodes to
    // exactly what the document without them decodes to -- nothing a screen
    // could draw, sanitize or sum -- and the CLI's rule that a field's meaning
    // never changes (a re-meant field is a renamed one) is what makes ignoring
    // them safe.
    const credential = ['gh', 'p_', 'a'.repeat(36)].join('')
    const additions: readonly (readonly (string | number)[])[] = [
      ['rogue'],
      ['governance'],
      ['pair'],
      ['policy_version'],
      [credential],
      ['x'.repeat(2000)],
      ['connection', 'rogue'],
      ['total', 'compute_usd'],
      ['total', 'leased'],
      ['total', 'burn', 'window_s'],
      ['repos', 0, 'leased'],
      ['repos', 0, 'author'],
      ['runs', 0, 'rigor_mode'],
      ['runs', 0, 'ab'],
      ['runs', 0, 'settings'],
      ['runs', 0, 'author'],
      ['runs', 0, 'governance'],
    ]
    for (const path of additions) {
      ignoring(first, patched(first, path, { added: 'by a newer CLI', usd: 1 }), (raw) => decodeDocument(raw))
    }
    const all = additions.reduce<JSONDoc>((doc, path) => patched(doc, path, 1), first)
    ignoring(first, all, (raw) => decodeDocument(raw))
    // A key the console does not know never reaches the value: the decoded
    // document carries none of them, and none of the hostile names.
    expect(JSON.stringify(decoded(all))).not.toContain(credential)
    expect(JSON.stringify(decoded(all))).not.toContain('by a newer CLI')
  })

  it('still refuses a removed field and a retyped one, which is what an additive rule cannot cover', () => {
    const removed = clone(first) as Record<string, unknown>
    delete removed.connection
    expect(refusalFor(removed)).toMatchObject({ name: 'missing', field: 'connection' })
    expect(refusalFor(patched(first, ['runs', 0, 'title'], 7))).toMatchObject({
      name: 'not-a-string',
      field: 'runs[0].title',
    })
    expect(refusalFor(patched(first, ['repos'], {}))).toMatchObject({ name: 'not-an-array', field: 'repos' })
  })

  it('says what it could not read, in the console`s own words', () => {
    const result = decodeDocument(patched(first, ['now'], 1), 'the live feed document')
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.skew.because).toBe(
      'the live feed document is not one the console reads: now is not-a-string',
    )
    // And never the document itself: a skew banner that echoed a hostile
    // document would put it on screen, which is the failure this whole layer
    // exists to prevent.
    const hostile = ['<script>', 'alert(1)', '</script>'].join('')
    const withHostileValue = decodeDocument(
      patched(first, ['runs', 0, 'title'], [hostile]),
      'the live feed document',
    )
    expect(withHostileValue.ok).toBe(false)
    if (withHostileValue.ok) return
    expect(withHostileValue.skew.refusal.name).toBe('not-a-string')
    expect(withHostileValue.skew.because).not.toContain('<script>')
    expect(withHostileValue.skew.because).not.toContain('alert')
    expect(withHostileValue.skew.because).toContain('runs[0].title')
  })
})

describe('a document missing a field the screens read fails decoding by name', () => {
  it('refuses each field the screens read, naming it', () => {
    const cases: readonly (readonly (string | number)[])[] = [
      ['total'],
      ['connection'],
      ['now'],
      ['ready'],
      ['runs'],
      ['runs', 0, 'run'],
      ['repos', 0, 'repo'],
      ['repos', 0, 'slug'],
      ['runs', 0, 'repo'],
      ['runs', 0, 'title'],
      ['runs', 0, 'round'],
      ['runs', 0, 'verify'],
      ['runs', 0, 'deadline'],
      ['runs', 0, 'health'],
      ['runs', 0, 'stage'],
    ]
    for (const path of cases) {
      const refusal = refusalFor(without(first, path))
      expect(refusal.name, label(path)).toBe('missing')
      expect(refusal.field, label(path)).toBe(label(path))
    }
  })

  it('reads a missing number as unavailable, and does not refuse the document', () => {
    // The tolerant axis, and the contract's own convention: `*float64` makes an
    // absent or null number a normal value ("no cap set"), not a document the
    // console cannot read. This is the task's headline rule -- a missing field
    // is `n/a`, never `0` -- so it is asserted here as an *unavailable reading*
    // on a document that decoded, which is the distinction a screen depends on.
    const value = decoded(without(first, ['total', 'counted_usd']))
    expect(value.total.countedUsd.value).toBeNull()
    expect(value.total.countedUsd.state).toBe('unavailable')
    expect(value.total.burn.known).toBe(true)
    const gone = decoded(without(first, ['total', 'daily_cap_usd']))
    expect(gone.total.dailyCapUsd.value).toBeNull()
    expect(gone.total.countedUsd.value).toBe(12.5)
  })

  it('reads `leased_usd` on a total and on a repository, and refuses a wrong value', () => {
    // The CLI writes `leased_usd` on the `watch --json` `total` object and on
    // every `repos[]` entry now, so the console reads it there -- and reads it
    // as an *optional* money key all the same, because a document from an
    // older CLI carries no such key: absent is `n/a`, never a lease of
    // `$0.00`. The fixture carries it (12.5 = counted 12.5 - spent 0), and the
    // same document with the key taken out is the older CLI's shape.
    const written = decoded(first)
    expect(written.total.leasedUsd.state).toBe('measured')
    expect(written.total.leasedUsd.value).toBe(12.5)
    expect(written.repos[0]?.leasedUsd.state).toBe('measured')

    const absent = decoded(without(without(first, ['total', 'leased_usd']), ['repos', 0, 'leased_usd']))
    expect(absent.total.leasedUsd.value).toBeNull()
    expect(absent.total.leasedUsd.state).toBe('unavailable')
    expect(formatReading(absent.total.leasedUsd, 'usd')).toBe(UNAVAILABLE_MARK)
    expect(absent.repos[0]?.leasedUsd.state).toBe('unavailable')

    const present = decoded(
      patched(patched(first, ['total', 'leased_usd'], 3.5), ['repos', 0, 'leased_usd'], 1.25),
    )
    expect(present.total.leasedUsd.state).toBe('measured')
    expect(present.total.leasedUsd.value).toBe(3.5)
    expect(present.repos[0]?.leasedUsd.state).toBe('measured')
    expect(present.repos[0]?.leasedUsd.value).toBe(1.25)

    // A `null` is the contract's own "nothing to say" (`*float64`), the same
    // fact as an absent key and not a measured zero.
    const nulled = decoded(patched(first, ['total', 'leased_usd'], null))
    expect(nulled.total.leasedUsd.state).toBe('unavailable')
    expect(nulled.total.leasedUsd.value).toBeNull()

    // And a measured `0` is the other half of that rule: `0` is a lease of
    // nothing, which is a figure the document took, not the `n/a` an absent one
    // draws. It is the one value that separates the two, so it is asserted.
    const zero = decoded(
      patched(patched(first, ['total', 'leased_usd'], 0), ['repos', 0, 'leased_usd'], 0),
    )
    expect(zero.total.leasedUsd.state).toBe('measured')
    expect(zero.total.leasedUsd.value).toBe(0)
    expect(formatReading(zero.total.leasedUsd, 'usd')).toBe('$0.00')
    expect(zero.repos[0]?.leasedUsd.state).toBe('measured')
    expect(zero.repos[0]?.leasedUsd.value).toBe(0)

    // A value of the wrong shape, or a negative one, is refused by its wire
    // path rather than half-read: no money field of this contract can hold one,
    // so a document carrying one is a document this console does not
    // understand.
    for (const [path, value, field] of [
      [['total', 'leased_usd'], '3.5', 'total.leased_usd'],
      [['total', 'leased_usd'], -1, 'total.leased_usd'],
      [['repos', 0, 'leased_usd'], true, 'repos[0].leased_usd'],
      [['repos', 0, 'leased_usd'], -0.5, 'repos[0].leased_usd'],
    ] as const) {
      const refusal = refusalFor(patched(first, path, value))
      expect(refusal.name, `${field} = ${String(value)}`).toBe('not-a-number')
      expect(refusal.field, `${field} = ${String(value)}`).toBe(field)
    }

    // And a near-miss name is not read as the key: `leased` is a key no shape
    // here has, so it is ignored, and `leased_usd` stays what the document said.
    for (const path of [
      ['total', 'leased'],
      ['repos', 0, 'leased'],
    ] as const) {
      ignoring(first, patched(first, path, 1), (raw) => decodeDocument(raw))
    }
  })

  it('refuses a field that is there with the wrong type, naming it too', () => {
    const wrong: readonly { readonly path: readonly (string | number)[]; readonly value: unknown; readonly name: string }[] = [
      { path: ['project'], value: 12, name: 'not-a-string' },
      { path: ['mode'], value: null, name: 'not-a-string' },
      { path: ['repos'], value: 'not-an-array', name: 'not-an-array' },
      { path: ['runs'], value: {}, name: 'not-an-array' },
      { path: ['runs', 0, 'notional'], value: 'yes', name: 'not-a-boolean' },
      { path: ['connection'], value: 'live', name: 'not-an-object' },
      { path: ['total', 'burn'], value: [], name: 'not-an-object' },
      { path: ['runs', 0, 'title'], value: ['a', 'b'], name: 'not-a-string' },
    ]
    for (const { path, value, name } of wrong) {
      const refusal = refusalFor(patched(first, path, value))
      expect(refusal.name, label(path)).toBe(name)
      expect(refusal.field, label(path)).toBe(label(path))
    }
  })

  it('refuses a now that is not a timestamp, by name (review round 3 of PR 494)', () => {
    // An unparseable `now` used to decode, and the burn history cannot place a
    // sample with no time: pruning stopped and the history grew by one sample
    // per frame for as long as the console stayed open.
    for (const value of ['', 'not a time', 'yesterday', '2026-13-45T99:99:99Z', '1760000000', '2026-10-15']) {
      const refusal = refusalFor(patched(first, ['now'], value))
      expect(refusal.name, value).toBe('not-a-timestamp')
      expect(refusal.field, value).toBe('now')
    }
    expect(decoded(patched(first, ['now'], '2026-10-15T12:00:00.250+02:00')).now).toBe('2026-10-15T12:00:00.250+02:00')
  })

  it('refuses a run whose round is a number, which is the near-miss that matters', () => {
    // `budget show --json` carries `round` as an int. A console that typed this
    // shape's `round` as a number would pass every fixture until a queued run
    // arrived with `"-"`, and then refuse a document the CLI writes every day.
    const refusal = refusalFor(patched(first, ['runs', 0, 'round'], 2))
    expect(refusal.name).toBe('not-a-string')
    expect(refusal.field).toBe('runs[0].round')
  })
})

describe('the null pointers are unavailable, and never zero', () => {
  it('reads a null cap, percent, spend and age as unavailable', () => {
    const document = patched(first, ['total', 'daily_cap_usd'], null)
    const noPercent = patched(document, ['total', 'percent'], null)
    const noSpend = patched(noPercent, ['runs', 0, 'spent_usd'], null)
    const noAge = patched(noSpend, ['runs', 0, 'age_seconds'], null)
    const value = decoded(noAge)

    // The design's first UI rule, at the decoder: not one of these is drawn as
    // a zero, and each says which field it is that the document does not carry.
    for (const [reading, field] of [
      [value.total.dailyCapUsd, 'total.daily_cap_usd'],
      [value.total.percent, 'total.percent'],
      [value.runs[0]?.spentUsd, 'runs[0].spent_usd'],
      [value.runs[0]?.ageSeconds, 'runs[0].age_seconds'],
    ] as const) {
      expect(reading?.state, field).toBe('unavailable')
      expect(reading?.value, field).toBeNull()
      expect(reading?.because, field).toContain(field)
      expect(formatReading(reading ?? readNumber(null, field), 'usd'), field).toBe(UNAVAILABLE_MARK)
    }
  })

  it('reads a burn rate the document does not know as unavailable, not as zero', () => {
    // `JSONBurn` carries `known` and a `usd_per_min` that is a zero when the
    // rate is unknown (contract section 2: read the flag first). The zero is
    // what a chart would draw as a flat line at the bottom.
    const value = decoded(patched(first, ['total', 'burn'], { known: false, usd_per_min: 0, fast: false }))
    expect(value.total.burn.known).toBe(false)
    expect(value.total.burn.usdPerMin.state).toBe('unavailable')
    expect(formatReading(value.total.burn.usdPerMin, 'usd')).toBe(UNAVAILABLE_MARK)
  })

  it('reads a null kill switch as not killed, which is a measured fact', () => {
    const value = decoded(first)
    expect(value.total.kill).toBeNull()
    expect(value.repos[0]?.kill).toBeNull()
    // And when it is on, the operator, the time and the reason come through --
    // with the operator a person and the reason a sentence, so a screen that
    // draws them draws text.
    const killed = decoded(
      patched(first, ['total', 'kill'], {
        by: 'fixture-operator',
        at: '2026-10-15T12:00:00Z',
        reason: 'budget kill fixture-sandbox',
      }),
    )
    expect(killed.total.kill).toEqual({
      by: 'fixture-operator',
      at: '2026-10-15T12:00:00Z',
      reason: 'budget kill fixture-sandbox',
    })
  })

  it('leaves a finished run`s absent pull request absent', () => {
    // `pr_url` and `pr_number` are `omitempty` on `JSONFinished`: absent means
    // the run has no pull request. Design section 4.3's gap 7 is that the
    // console builds no link from them; this is the half that says the number
    // is not an invented one.
    const second = documents[1] ?? {}
    const value = decoded(second)
    expect(value.finished[0]?.prNumber).toBe(412)
    // The recorder redacts a URL on the way into a fixture, so the URL the
    // console actually reads is the marker -- and the number beside it is the
    // real one. That pair is design section 4.3's gap 7 in one line: the
    // console has a number and a URL, neither of which is a link it built.
    expect(value.finished[0]?.prUrl).toBe('[redacted-url]')
    const noPR = without(second, ['finished', 0, 'pr_url'])
    const value2 = decoded(without(noPR, ['finished', 0, 'pr_number']))
    expect(value2.finished[0]?.prUrl).toBeNull()
    expect(value2.finished[0]?.prNumber).toBeNull()
  })
})

describe('every wire string is classified', () => {
  /** Leaf is one string leaf: the key that holds it, the path to it, and the
   * value the walk found. */
  interface Leaf {
    readonly key: string
    readonly path: string
    readonly value: string
  }

  /** stringLeaves walks a document and returns every string leaf. Arrays keep
   * their parent key (`warnings[0]` is a `warnings`), because the class is
   * per-key and not per-element. */
  function stringLeaves(value: unknown, key = '', path = ''): readonly Leaf[] {
    if (typeof value === 'string') return [{ key, path, value }]
    if (Array.isArray(value)) {
      return value.flatMap((item, index) => stringLeaves(item, key, `${path}[${String(index)}]`))
    }
    if (value !== null && typeof value === 'object') {
      return Object.entries(value).flatMap(([name, child]) =>
        stringLeaves(child, name, path === '' ? name : `${path}.${name}`),
      )
    }
    return []
  }

  /** unclassified is the leaves with no class and no exemption. */
  function unclassified(document: unknown): readonly string[] {
    return stringLeaves(document)
      .filter((leaf) => wireTextClass(leaf.key) === undefined && !NON_TEXT_STRING_KEYS.includes(leaf.key))
      .map((leaf) => leaf.path)
  }

  it('classifies every string leaf of every document', () => {
    const all = [...documents, DEGRADED, DEGRADED_REAL, patched(first, ['total', 'kill'], {
      by: 'fixture-operator',
      at: '2026-10-15T12:00:00Z',
      reason: 'budget kill fixture-sandbox',
    }), patched(first, ['queued_note'], 'the bucket was read a minute ago'), LIVE_REAL, PLAN_STATUS]
    for (const document of all) {
      expect(unclassified(document), JSON.stringify(stringLeaves(document).map((l) => l.path))).toEqual([])
    }
    // Not vacuous: the walk found leaves, and it found the two halves of the
    // claim -- a text key and a non-text key.
    const leaves = stringLeaves(first)
    expect(leaves.length).toBeGreaterThan(10)
    expect(leaves.some((leaf) => wireTextClass(leaf.key) === 'sentence')).toBe(true)
    expect(leaves.some((leaf) => NON_TEXT_STRING_KEYS.includes(leaf.key))).toBe(true)
    expect(leaves.map((leaf) => leaf.path)).toContain('runs[0].title')
    // `queued_note` is the one live key the corpus omits, so it is the one key
    // this walk could not otherwise see: it is asserted separately, with a
    // document that carries it, because a map that is total over the corpus is
    // not the same as a map that is total over the wire.
    expect(wireTextClass('queued_note')).toBe('sentence')
    const withNote = stringLeaves(patched(first, ['queued_note'], 'a note'))
    expect(withNote.some((leaf) => leaf.key === 'queued_note')).toBe(true)
    expect(unclassified(patched(first, ['queued_note'], 'a note'))).toEqual([])
    // Every key of a realistic `runview.Row` is classified too.
    expect(unclassified(DEGRADED_REAL)).toEqual([])
    // And every key of a realistic `JSONRun`/`JSONFinished`: 0.9.0's `job`,
    // `profile`, `provider`, `account` and `launched_by` are in no document of
    // the corpus, so without this row the map could claim to be total over the
    // wire while missing exactly the keys a 0.9.0 CLI writes. 0.10.0's
    // `credential` and `waiting_until` are in no document of the corpus either,
    // for the same reason: the recording predates them.
    expect(stringLeaves(LIVE_REAL).map((leaf) => leaf.key)).toContain('launched_by')
    expect(stringLeaves(LIVE_REAL).map((leaf) => leaf.key)).toContain('credential')
    expect(stringLeaves(LIVE_REAL).map((leaf) => leaf.key)).toContain('waiting_until')
    expect(unclassified(LIVE_REAL)).toEqual([])
    // And the plan status document's own keys: `artifact`, `theme`, `names`,
    // `current`, `next`, `tag_prefix` and `mark` are in no other document, so
    // without this row the map could claim to be total over the wire while
    // missing exactly the keys this read writes.
    for (const key of ['artifact', 'theme', 'names', 'current', 'next', 'tag_prefix', 'mark']) {
      expect(stringLeaves(PLAN_STATUS).map((leaf) => leaf.key), key).toContain(key)
    }
    expect(unclassified(PLAN_STATUS)).toEqual([])
  })

  it('reports a key it has no class for, which is what makes the walk a test', () => {
    // The walk's own red step: the same function, over a document with a planted
    // key, must report it.
    const planted = { project: '<project>', rogue_key: 'a value' }
    expect(unclassified(planted)).toEqual(['rogue_key'])
    expect(unclassified(first)).toEqual([])
  })

  it('classifies the text keys by what they are, not by what they contain', () => {
    // The class of a key is a fact about the key, so a hostile value cannot
    // change it -- and a key with no class is refused rather than guessed at.
    expect(wireTextClass('title')).toBe('sentence')
    expect(wireTextClass('repo')).toBe('repository')
    expect(wireTextClass('branch')).toBe('branch')
    expect(wireTextClass('requested_by')).toBe('person')
    // 0.9.0's five keys on the live run shapes: `launched_by` and `account` are
    // the same kind of value as `requested_by` above -- a launcher's and a run's
    // own recorded identities, drawn as people -- and `job`, `profile` and
    // `provider` are identifiers a policy wrote, drawn as labels.
    expect(wireTextClass('launched_by')).toBe('person')
    expect(wireTextClass('account')).toBe('person')
    expect(wireTextClass('job')).toBe('label')
    expect(wireTextClass('profile')).toBe('label')
    expect(wireTextClass('provider')).toBe('label')
    // GV1.2's `plan_task` (decision GD5) is text a launcher wrote, so it is
    // classified rather than exempted even though no screen draws it yet: an id
    // the plan's document spells a node with, which is a label and neither a
    // person nor prose. It is `label` and not `sentence` because a value that
    // were prose must not be drawn as an identifier.
    expect(wireTextClass('plan_task')).toBe('label')
    expect(wireTextClass('plan_task')).not.toBe('sentence')
    expect(wireTextClass('plan_task')).not.toBe('person')
    // 0.10.0's two (credentials-service C1.21). `credential` is a **name** -- a
    // label, the class an identifier a policy or the service wrote carries, and
    // not `person`: it names an account's token, not a person. It goes through
    // the free-text rules all the same, so a value that is a value rather than a
    // name is drawn as the token marker, which is the direction this must fail
    // in: the contract's promise is the name and never the value, and this layer
    // is what keeps that promise true of a document some other writer produced.
    expect(wireTextClass('credential')).toBe('label')
    expect(wireTextClass('credential')).not.toBe('sentence')
    // `waiting_until` is not text at all: an RFC 3339 instant a reader compares
    // with `now` -- and whose **presence** is the state ("this run is held up"),
    // which is a fact to read and not a string to draw -- so it is a stamp like
    // `now` and `at`, and the recorder shifts it like one.
    expect(wireTextClass('waiting_until')).toBeUndefined()
    expect(NON_TEXT_STRING_KEYS).toContain('waiting_until')
    expect(wireTextClass('pr_url')).toBe('url')
    expect(wireTextClass('round')).toBe('label')
    expect(wireTextClass('round')).not.toBe('sentence')
    expect(wireTextClass('mode')).toBeUndefined()
    expect(wireTextClass('rogue_key')).toBeUndefined()
    // A commit digest is not text. Classed as a label it would have gone
    // through the free-text rules, and the entropy net draws a 40-character
    // hex sha as `[redacted-token]` -- which is the recorder's own stated
    // reason for keeping hex digests as written (`src/source/redact.ts:409`).
    expect(wireTextClass('pushed_head')).toBeUndefined()
    expect(NON_TEXT_STRING_KEYS).toContain('pushed_head')
    // The two lists do not overlap, and neither is empty: a key in both would
    // be a key whose class is two answers.
    for (const key of NON_TEXT_STRING_KEYS) expect(wireTextClass(key), key).toBeUndefined()
    expect(Object.keys(WIRE_TEXT_CLASSES).length).toBeGreaterThan(10)
    expect(NON_TEXT_STRING_KEYS.length).toBeGreaterThan(5)
  })
})

describe('an optional boolean is a boolean or absent (review round 2 of PR 494)', () => {
  it('refuses a present non-boolean by field name instead of reading it as false', () => {
    const wrong: readonly { readonly document: JSONDoc; readonly path: readonly (string | number)[]; readonly value: unknown }[] = [
      { document: first, path: ['runs', 0, 'queued'], value: 'true' },
      { document: first, path: ['runs', 0, 'stuck'], value: 1 },
      { document: DEGRADED_REAL, path: ['runs', 0, 'cost', 'compute_estimated'], value: 'true' },
      { document: DEGRADED_REAL, path: ['runs', 0, 'cost', 'compute_estimated'], value: {} },
    ]
    for (const { document, path, value } of wrong) {
      const refusal = refusalFor(patched(document, path, value))
      expect(refusal.name, label(path)).toBe('not-a-boolean')
      expect(refusal.field, label(path)).toBe(label(path))
    }
  })

  it('still reads absent and null as false', () => {
    expect(decodeDocument(patched(DEGRADED_REAL, ['runs', 0, 'cost', 'compute_estimated'], null)).ok).toBe(true)
  })
})

describe('the plan status document (plan GV2.1)', () => {
  /** planDecoded is a plan document that must decode, or the test fails with
   * the refusal's own sentence rather than on an assertion three lines later. */
  function planDecoded(raw: unknown, where = 'the plan status document'): PlanStatusView {
    const result = decodePlanStatus(raw, where)
    if (!result.ok) throw new Error(result.skew.because)
    return result.value
  }

  /** planRefusal is the refusal of a plan document that must be refused. */
  function planRefusal(raw: unknown): { field: string; name: string } {
    const result = decodePlanStatus(raw, 'the plan status document')
    if (result.ok) throw new Error('expected a refusal')
    return result.skew.refusal
  }

  it('decodes the committed fixture, and reads what the graph screen draws', () => {
    const document = planDecoded(PLAN_STATUS)
    expect(document.repo).toBe('example-owner/example-repo')
    expect(formatReading(document.totals.plans)).toBe('2')
    expect(formatReading(document.totals.tasks)).toBe('7')
    expect(document.plans.map((plan) => plan.id)).toEqual([
      'plans/2026-10-01-example-graph',
      'example-second',
    ])
    // A plan's own three figures are the sum of its release boxes plus the
    // tasks a plan gives no version, which is why the CLI states them and the
    // console does not add anything up.
    const [graph] = document.plans
    expect(formatReading(graph?.tasks ?? unavailable('no plan'))).toBe('6')
    expect(graph?.nodes.map((node) => node.state)).toEqual([
      'done',
      'running',
      'ready',
      'cancelled',
      'blocked',
      'not-started',
    ])
    expect(graph?.nodes[0]?.mark).toBe('merge unknown')
    expect(graph?.nodes[0]?.prNumber).toBe(101)
    expect(graph?.nodes[2]?.prNumber).toBeNull()
    // The node a read collapses still says so: `closed` is the field the read
    // counted it with, not a rule the screen re-derives from the state.
    expect(graph?.nodes[3]?.closed).toBe(true)
    expect(graph?.nodes[1]?.closed).toBe(false)
    // A node's `needs` is the plan's own edge, in the plan's own ids, and a node
    // that needs nothing carries an empty list rather than a missing one: the
    // graph screen walks this list for every node it draws.
    expect(graph?.nodes[1]?.needs).toEqual(['task-one'])
    expect(graph?.nodes[4]?.needs).toEqual(['task-three', 'task-two'])
    expect(graph?.nodes[0]?.needs).toEqual([])
    // A plan that names no stream has no stream, and a plan with no versioned
    // task has no release box at all -- both are facts about the document
    // rather than missing values.
    expect(document.plans[1]?.stream).toBe('')
    expect(document.plans[1]?.releases).toEqual([])
    expect(document.streams).toEqual([
      {
        name: 'example',
        theme: 'an invented theme',
        names: ['Examplefruit', 'Examplebird'],
        current: '1.4.0',
        next: '1.5.0',
        tagPrefix: 'v',
      },
    ])
    expect(document.warnings).toHaveLength(1)
  })

  it('reads a count as a reading and a level as a number', () => {
    // The availability rule is the whole of this test: a figure a header prints
    // is a `Reading`, so a document that does not carry one is drawn as `n/a`
    // rather than as a zero (`formatReading` is the only way to text it), while
    // a node's `level` is a place in a dependency chain and is either there as
    // a whole number or the document is not one this console can draw.
    const document = planDecoded(PLAN_STATUS)
    expect(formatReading(document.totals.open)).toBe('6')
    expect(typeof document.plans[0]?.nodes[0]?.level).toBe('number')
    // A totals object that carries no figure decodes as four unavailable
    // readings rather than as four zeros: the document is readable and it does
    // not know, which is the distinction the whole availability model exists
    // for.
    const noCounts = planDecoded(patched(PLAN_STATUS, ['totals'], {}))
    expect(noCounts.totals.plans).toEqual(expect.objectContaining({ state: 'unavailable' }))
    expect(formatReading(noCounts.totals.plans)).toBe(UNAVAILABLE_MARK)
    const fractional = patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'level'], 1.5)
    expect(planRefusal(fractional).name).toBe('not-a-number')
    expect(planRefusal(fractional).field).toBe('plans[0].nodes[0].level')
  })

  it('reads a pull request number only as a positive whole number, and a level only within its bound', () => {
    // A pull request number is an identifier: a fraction, a negative, a zero or
    // a numeral in a string is not one, and reading it as "no pull request"
    // would hide that the document is wrong.
    for (const bad of [-3.5, -3, 0, 2.5, '7', true]) {
      const refusal = planRefusal(patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'pr_number'], bad))
      expect(refusal.name, String(bad)).toBe('not-a-number')
      expect(refusal.field, String(bad)).toBe('plans[0].nodes[0].pr_number')
    }
    expect(planDecoded(patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'pr_number'], 7)).plans[0]?.nodes[0]?.prNumber).toBe(7)
    expect(planDecoded(patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'pr_number'], null)).plans[0]?.nodes[0]?.prNumber).toBeNull()
    // A level is a place in a dependency chain: whole, not negative, and not
    // larger than any plan this console would draw.
    for (const bad of [-1, PLAN_MAX_LEVEL + 1, 1e9]) {
      const refusal = planRefusal(patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'level'], bad))
      expect(refusal.name, String(bad)).toBe('not-a-number')
      expect(refusal.field, String(bad)).toBe('plans[0].nodes[0].level')
    }
    expect(planDecoded(patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'level'], PLAN_MAX_LEVEL)).plans[0]?.nodes[0]?.level).toBe(PLAN_MAX_LEVEL)
  })

  it('types a plan state as the six words and nothing else', () => {
    const known: PlanState = 'not-started'
    expect(PLAN_STATES).toContain(known)
    // @ts-expect-error 'paused' is not one of the six states
    const unknown: PlanState = 'paused'
    expect(PLAN_STATES).not.toContain(unknown)
  })

  it('refuses a state that is not one of the six, by name', () => {
    // The decoder's own red step, and the reason this document needs a decoder
    // at all: every field beside `state` is read in that word's light, so a
    // seventh word is a CLI whose rules this build does not know. It is refused
    // rather than drawn in whatever colour a screen keeps for "not one of mine".
    for (const state of ['in-review', 'RUNNING', '', 'done ']) {
      const refusal = planRefusal(patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'state'], state))
      expect(refusal.name, state).toBe('unknown-value')
      expect(refusal.field, state).toBe('plans[0].nodes[0].state')
    }
    // ...and the six it does know all pass, so the refusal above is not a
    // decoder that refuses every word.
    expect(PLAN_STATES).toHaveLength(6)
    for (const [index, state] of PLAN_STATES.entries()) {
      const accepted = patched(PLAN_STATUS, ['plans', 0, 'nodes', 0, 'state'], state)
      expect(planDecoded(accepted).plans[0]?.nodes[0]?.state, `${state} at ${String(index)}`).toBe(state)
    }
  })

  it('reads a node needs as a list of task ids, absent as none, and refuses anything else', () => {
    // `needs` is the plan's own edge: the ids of the tasks this one waits on.
    // The ids are the same kind of value as the node's own `id` -- read as any
    // non-empty string, since a plan names its tasks and this build does not
    // know the naming rules of every plan -- so the one thing refused here is a
    // member that could not be an id at all.
    const node = ['plans', 0, 'nodes', 0] as const
    expect(planDecoded(patched(PLAN_STATUS, [...node, 'needs'], ['task-three', 'task-two'])).plans[0]?.nodes[0]?.needs).toEqual([
      'task-three',
      'task-two',
    ])
    // Absent and null are the same fact: this task needs nothing. Both are an
    // empty list rather than `undefined`, so no screen has to guard.
    const withNeeds = ['plans', 0, 'nodes', 1] as const
    expect(planDecoded(without(PLAN_STATUS, [...withNeeds, 'needs'])).plans[0]?.nodes[1]?.needs).toEqual([])
    expect(planDecoded(patched(PLAN_STATUS, [...node, 'needs'], null)).plans[0]?.nodes[0]?.needs).toEqual([])
    expect(planDecoded(patched(PLAN_STATUS, [...node, 'needs'], [])).plans[0]?.nodes[0]?.needs).toEqual([])
    // A non-array, a non-string member and an empty member are three different
    // mistakes and are named as three different refusals.
    for (const bad of ['task-two', 7, {}, true]) {
      const refusal = planRefusal(patched(PLAN_STATUS, [...node, 'needs'], bad))
      expect(refusal.name, JSON.stringify(bad)).toBe('not-an-array')
      expect(refusal.field, JSON.stringify(bad)).toBe('plans[0].nodes[0].needs')
    }
    for (const [bad, index] of [
      [['task-two', 7], 1],
      [['task-two', ['task-three']], 1],
      [[null], 0],
    ] as const) {
      const refusal = planRefusal(patched(PLAN_STATUS, [...node, 'needs'], bad))
      expect(refusal.name, JSON.stringify(bad)).toBe('not-a-string')
      expect(refusal.field, JSON.stringify(bad)).toBe(`plans[0].nodes[0].needs[${String(index)}]`)
    }
    const empty = planRefusal(patched(PLAN_STATUS, [...node, 'needs'], ['task-two', '']))
    expect(empty.name).toBe('not-a-string')
    expect(empty.field).toBe('plans[0].nodes[0].needs[1]')
  })

  it('lists every key of the document and of each object in it, read off the structs', () => {
    // `structTags` is the reader the three pins above it use, and these are the
    // two Go files the plan read is written in: the envelope and its figures are
    // `plan_status.go`'s, the plans and everything under them are
    // `internal/plan/state.go`'s. Both directions, as above: a key the console
    // lists and the struct cannot write fails here, and so does a key the struct
    // writes and the console does not list.
    const pairs: readonly (readonly [readonly StructTag[], readonly string[], string])[] = [
      [structTags(PLAN_SOURCE, 'planStatusDoc', 'internal/cli/plan_status.go'), PLAN_STATUS_KEYS, 'the document'],
      [structTags(PLAN_SOURCE, 'planStatusTotals', 'internal/cli/plan_status.go'), PLAN_TOTALS_KEYS, 'totals'],
      [structTags(STATE_SOURCE, 'StatePlan', 'internal/plan/state.go'), PLAN_KEYS, 'plans[]'],
      [structTags(STATE_SOURCE, 'StateNode', 'internal/plan/state.go'), PLAN_NODE_KEYS, 'nodes[]'],
      [structTags(STATE_SOURCE, 'StateRelease', 'internal/plan/state.go'), PLAN_RELEASE_KEYS, 'releases[]'],
      [structTags(STATE_SOURCE, 'StateStream', 'internal/plan/state.go'), PLAN_STREAM_KEYS, 'streams[]'],
    ]
    for (const [tags, keys, what] of pairs) {
      expect(tags.map((tag) => tag.key), what).toEqual([...keys])
    }
    // ...and one step past the list: a key no struct of the read writes yet is
    // a field a newer CLI added, and it is ignored rather than refused.
    for (const [path, value] of [
      [['plans', 0, 'nodes', 0, 'colour'], 'red'],
      [['download'], 'https://example.invalid/x'],
    ] as const) {
      ignoring(PLAN_STATUS, patched(PLAN_STATUS, path, value), (raw) => decodePlanStatus(raw))
    }
  })

  it('refuses a feed document, and the feed refuses this one', () => {
    // The two documents carry no common marker, so neither door admits the
    // other's: a plan document handed to the feed's door is an unknown shape,
    // and the feed's own document is refused here for the keys it lacks. A
    // screen that read one as the other would draw an empty graph over a live
    // feed, or an empty feed over a graph.
    // The feed's own document is refused by this door for a key the plan
    // document must carry and the feed does not -- the two shapes share no key
    // at all.
    expect(decodeDocument(PLAN_STATUS).ok).toBe(false)
    const feed = decodePlanStatus(first)
    expect(feed.ok).toBe(false)
    if (!feed.ok) expect(feed.skew.refusal.name).toBe('missing')
    // The plan document's own door takes exactly the fixture, and an object
    // that is not one at all is refused before anything is read from it.
    expect(decodePlanStatus([]).ok).toBe(false)
  })
})

describe('the coordinator’s stored project view (T1.2)', () => {
  /**
   * STORED_VIEW is one repository the sweep reconciled, in this file's own
   * words rather than read from a fixture: the console has no committed
   * coordinator document, and the two states this read turns on -- no node at
   * all, and a repository the sweep marked stale -- are not in one stored view
   * anyway. It carries **every** key of every shape, because the pin below
   * reads the structs and this document the same way and a key left out here
   * would make the two disagree.
   */
  const STORED_VIEW = {
    repos: [
      {
        repo: 'owner/repo-a',
        stored: true,
        view: {
          repo: 'owner/repo-a',
          records: [
            {
              pr: {
                number: 7,
                url: 'https://example.invalid/owner/repo-a/pull/7',
                title: 'a title someone wrote',
                branch: 'feat/a-thing',
                draft: true,
                state: 'open',
              },
              runs: [
                {
                  id: '20261027-030000-aa01',
                  pr: 7,
                  status: 'running',
                  outcome: '',
                  terminal: false,
                  branch: 'feat/a-thing',
                  base_branch: 'main',
                  declared_by: 'person-1',
                  files: ['src/a.go'],
                  files_truncated: false,
                },
              ],
              branch: 'feat/a-thing',
              release: '1.4',
              in_flight: true,
              files: ['src/a.go'],
              services: ['service-a'],
            },
          ],
          collisions: [{ a: '#7', b: '20261027-030000-aa01', files: ['src/a.go'], services: [] }],
          unread: [
            {
              number: 9,
              reason: 'the host refused the read',
              carried: true,
              failures: 2,
              retry_after: '2026-10-27T04:00:00Z',
            },
          ],
          omitted: 1,
          omitted_collisions: 2,
        },
        stale: { since: '2026-10-27T02:00:00Z', reason: 'the repository’s runs could not be read' },
      },
    ],
  }

  /** coordinatorDecoded is a document that must decode, or the test fails with
   * the refusal's own sentence rather than on an assertion three lines later. */
  function coordinatorDecoded(raw: unknown): CoordinatorStatusView {
    const result = decodeCoordinatorStatus(raw)
    if (!result.ok) throw new Error(result.skew.because)
    return result.value
  }

  /** coordinatorRefusal is the refusal of a document that must be refused. */
  function coordinatorRefusal(raw: unknown): { field: string; name: string } {
    const result = decodeCoordinatorStatus(raw)
    if (result.ok) throw new Error('expected a refusal')
    return result.skew.refusal
  }

  it('decodes a stored view, and tells “no node” from “a repository with nothing in it”', () => {
    // The flag the whole read turns on. An absent database node arrives as
    // `stored: false` with the runs half in `view`; a repository the sweep
    // reconciled to nothing arrives as `stored: true` with an empty `records`.
    // Both have empty arrays and only `stored` tells them apart, which is why
    // the screen has one branch for each and not one branch for both.
    const stored = coordinatorDecoded(STORED_VIEW).repos[0]
    expect(stored?.repo).toBe('owner/repo-a')
    expect(stored?.stored).toBe(true)
    expect(stored?.stale?.since).toBe('2026-10-27T02:00:00Z')
    expect(stored?.view.records[0]?.pr?.number).toBe(7)
    expect(stored?.view.records[0]?.runs[0]?.declaredBy).toBe('person-1')
    expect(stored?.view.records[0]?.runs[0]?.baseBranch).toBe('main')
    expect(stored?.view.records[0]?.inFlight).toBe(true)
    expect(stored?.view.collisions[0]?.b).toBe('20261027-030000-aa01')
    expect(stored?.view.unread[0]?.failures).toBe(2)

    const reconciledToNothing = coordinatorDecoded({
      repos: [{ repo: 'owner/repo-b', stored: true, view: { repo: 'owner/repo-b', records: [], collisions: [] } }],
    }).repos[0]
    expect(reconciledToNothing?.stored).toBe(true)
    expect(reconciledToNothing?.view.records).toEqual([])

    const noNode = coordinatorDecoded({
      repos: [{ repo: 'owner/repo-b', stored: false, view: { repo: 'owner/repo-b', records: [], collisions: [] } }],
    }).repos[0]
    expect(noNode?.stored).toBe(false)
    // The sweep's three `omitempty` keys are absent from a view `status`
    // computes, and absent is the zero and the empty list -- "nothing was
    // dropped" -- never a gap a screen would have to draw as unknown.
    expect(noNode?.view.omitted).toBe(0)
    expect(noNode?.view.omittedCollisions).toBe(0)
    expect(noNode?.view.unread).toEqual([])
    // ...and a record's `pr` is the pull request or nothing, never a
    // placeholder the screen would have to test for.
    const noPullRequest = coordinatorDecoded({
      repos: [
        {
          repo: 'owner/repo-b',
          stored: false,
          view: {
            repo: 'owner/repo-b',
            records: [{ runs: [{ id: '20261027-031500-bb02', files: [] }], in_flight: true, files: [], services: [] }],
            collisions: [],
          },
        },
      ],
    }).repos[0]
    expect(noPullRequest?.view.records[0]?.pr).toBeNull()
    expect(noPullRequest?.view.records[0]?.runs[0]?.status).toBe('')
    expect(noPullRequest?.view.records[0]?.runs[0]?.filesTruncated).toBe(false)
  })

  it('ignores a key no struct of the read writes, and refuses a missing or retyped one', () => {
    // The rule the other documents are held to: a key the console has no field
    // for is a field a newer CLI added, and it is not carried past the decoder.
    const rogue = structuredClone(STORED_VIEW) as Record<string, unknown>
    const repos = rogue.repos as Record<string, unknown>[]
    ;(repos[0] as Record<string, unknown>).colour = 'red'
    ignoring(STORED_VIEW, rogue, (raw) => decodeCoordinatorStatus(raw))
    ignoring(STORED_VIEW, { ...STORED_VIEW, download: 'https://example.invalid/x' }, (raw) =>
      decodeCoordinatorStatus(raw),
    )

    // A key the document must carry is refused by name rather than read as
    // nothing, and a value of the wrong type is refused by the field it is at.
    const missing = structuredClone(STORED_VIEW) as Record<string, unknown>
    delete ((missing.repos as Record<string, unknown>[])[0] as Record<string, unknown>).stored
    expect(coordinatorRefusal(missing)).toMatchObject({ name: 'missing', field: 'repos[0].stored' })
    const wrongType = structuredClone(STORED_VIEW) as Record<string, unknown>
    ;(((wrongType.repos as Record<string, unknown>[])[0] as Record<string, unknown>).view as Record<
      string,
      unknown
    >).records = 'none'
    expect(coordinatorRefusal(wrongType)).toMatchObject({ name: 'not-an-array', field: 'repos[0].view.records' })

    // An object that is not one at all is refused before anything is read from
    // it, and the live feed's document is refused by this door for the key it
    // lacks: the two shapes share no key, so neither admits the other's.
    expect(decodeCoordinatorStatus([]).ok).toBe(false)
    const feed = decodeCoordinatorStatus({ conn: { state: 'ok' }, runs: [] })
    expect(feed.ok).toBe(false)
    if (!feed.ok) expect(feed.skew.refusal.name).toBe('missing')
  })

  it('lists every key of the document and of each object in it, read off the structs', () => {
    // `structTags` reads the Go as it read the plan document's: the same reader,
    // the same both-directions comparison. A key the console lists and no struct
    // writes fails here, and so does one a struct writes that the console does
    // not list -- which is the drift this pin exists for, because a key the CLI
    // gained and this console did not is a value the screen draws nothing for.
    const pairs: readonly (readonly [readonly StructTag[], readonly string[], string])[] = [
      [
        structTags(COORDINATOR_STATE_SOURCE, 'StoredProjectView', 'ee/coordinator/state.go'),
        COORDINATOR_STATUS_KEYS,
        'the document',
      ],
      [
        structTags(COORDINATOR_STATE_SOURCE, 'RepoView', 'ee/coordinator/state.go'),
        COORDINATOR_REPO_KEYS,
        'repos[]',
      ],
      [structTags(COORDINATOR_VIEW_SOURCE, 'View', 'ee/coordinator/view.go'), COORDINATOR_VIEW_KEYS, 'view'],
      [
        structTags(COORDINATOR_VIEW_SOURCE, 'Record', 'ee/coordinator/view.go'),
        COORDINATOR_RECORD_KEYS,
        'records[]',
      ],
      [structTags(COORDINATOR_VIEW_SOURCE, 'Run', 'ee/coordinator/view.go'), COORDINATOR_RUN_KEYS, 'runs[]'],
      [
        structTags(COORDINATOR_VIEW_SOURCE, 'PullRequest', 'ee/coordinator/view.go'),
        COORDINATOR_PR_KEYS,
        'pr',
      ],
      [
        structTags(COORDINATOR_VIEW_SOURCE, 'Collision', 'ee/coordinator/view.go'),
        COORDINATOR_COLLISION_KEYS,
        'collisions[]',
      ],
      [
        structTags(COORDINATOR_SWEEP_SOURCE, 'Unread', 'ee/coordinator/sweep.go'),
        COORDINATOR_UNREAD_KEYS,
        'unread[]',
      ],
      [
        structTags(COORDINATOR_SWEEP_SOURCE, 'Stale', 'ee/coordinator/sweep.go'),
        COORDINATOR_STALE_KEYS,
        'stale',
      ],
    ]
    for (const [tags, keys, what] of pairs) {
      expect(tags.map((tag) => tag.key), what).toEqual([...keys])
    }
    // ...and the document above is the other side of the pin, so this is a
    // comparison between the Go and a document rather than the Go twice: it
    // carries every key of every shape, in the spelling the wire uses, and it
    // decodes. `decodes` is asserted first because a document missing a key
    // would otherwise make the key lists agree with an incomplete object.
    coordinatorDecoded(STORED_VIEW)
    const wire = STORED_VIEW.repos[0]
    if (wire === undefined) throw new Error('the document lost its only repository')
    expect(Object.keys(wire).sort()).toEqual([...COORDINATOR_REPO_KEYS].sort())
    expect(Object.keys(wire.view).sort()).toEqual([...COORDINATOR_VIEW_KEYS].sort())
    const record = wire.view.records[0]
    if (record === undefined) throw new Error('the document lost its only record')
    expect(Object.keys(record).sort()).toEqual([...COORDINATOR_RECORD_KEYS].sort())
    expect(Object.keys(record.pr ?? {}).sort()).toEqual([...COORDINATOR_PR_KEYS].sort())
    const run = record.runs[0]
    if (run === undefined) throw new Error('the document lost its only run')
    expect(Object.keys(run).sort()).toEqual([...COORDINATOR_RUN_KEYS].sort())
  })
})
