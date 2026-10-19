// The synthetic fixture, and the only place its documents exist in source
// (desktop-console plan Task 1.4).
//
// **This is not a recording, and nothing here was recorded.** The plan's eight
// recordings are owner-run -- an agent may not run the recorder against a real
// installation, may not read `~/.config/fugaro` and may not handle a credential
// -- so this file stands in for them: invented documents, shaped like the ones
// `console/docs/json-contract.md` describes, put through **the same redaction
// path and the same writer a recording goes through** and committed as
// `test/fixtures/synthetic.ndjson`.
//
// Keeping the invented documents here rather than only in the committed file is
// what makes the synthetic claim checkable: `fixture.test.ts` regenerates the
// fixture with this module's own `syntheticFixture()` and asserts the committed
// bytes are exactly it, so a hand-edited fixture -- even one whose digest was
// recomputed by hand -- fails, and a reader can see which documents the file
// was made from without a decoder in their head.
//
// The documents cover the axes a replay has to reproduce, one per record:
//
//   1. a normal live document, with a repository, a running run and a finished
//      run that opened a pull request;
//   2. the same feed one tick later, with the numbers moved (the 1 s floor);
//   3. the degraded mode's sentence, which the CLI writes to stdout on the same
//      stream as the documents and which is a **notice** rather than a document;
//   4. a run that has gone silent, and a connection whose age says so;
//   5. a **gap**: the next document is 160 s later, and the run is lost (the
//      console's stale banner trips at 45 s, so the gap is visible on screen);
//   6. a run with fields the CLI omits -- a live run whose `recipe`, `halted`,
//      `workflow` and `requested_by` are `omitempty` and so are not there at
//      all, beside a **queued** run, whose round, verify, models and auth the
//      CLI writes as `"-"` and whose spend is `null` -- which is the
//      absent-field case, and a repository at its cap, which is the repo-at-cap
//      case in miniature.
//
// The values are invented: `fixture-sandbox`, `fixture-owner/fixture-repo`,
// `fixture-operator`. The literals list below is the recorder's own second half
// -- the names that have no shape for a pattern to catch -- and running these
// documents through it is what turns `fixture-sandbox` into `<project>` and
// leaves `feat/console-fixtures` as `<branch>`.

import { SYNTHETIC_NOTE, writeFixture } from '../../src/source/fixture'
import type { FixtureMeta, FixtureRecord } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { PROJECT_MARKER, createRedactor } from '../../src/source/redact'

/** SYNTHETIC_WHERE is what a message calls the committed fixture. */
export const SYNTHETIC_WHERE = 'test/fixtures/synthetic.ndjson'

/** SYNTHETIC_LITERALS is what the recorder would have been told about this
 * installation, had there been one: the project name, the operator's user name
 * and the path the names have no shape for. */
export const SYNTHETIC_LITERALS: readonly string[] = Object.freeze([
  'fixture-sandbox',
  'fixture-operator',
])

/** SYNTHETIC_PSEUDONYM_KEY keys the synthetic fixture's pseudonyms, so its
 * bytes are reproducible and the byte-identity test can hold them. **It is
 * public and it is only ever used here**: the names it keys are invented, and a
 * recording's key is random, drawn by `createRedactor`, and never written. */
export const SYNTHETIC_PSEUDONYM_KEY = 'synthetic-fixture-only-not-a-secret'

const REPO = 'fixture-owner/fixture-repo'
const SLUG = 'github-com-fixture-owner-fixture-repo'

/** runBlock is one live run as `watch --json` writes it (`internal/watch/json.go`
 * `JSONRun`); the overrides are where each record differs.
 *
 * Two corrections against the wire are worth naming, because this corpus was
 * written from the contract and both were wrong in the first revision:
 *
 *   - `round` is a **string** (`strconv.Itoa`, `internal/watch/build.go:403`),
 *     not a number. `budget show --json` carries an int under the same name,
 *     which is exactly the kind of near-miss a corpus is supposed to catch:
 *     a decoder written from the wrong one of the two would refuse every
 *     queued run, whose round the CLI writes as `"-"`.
 *   - `recipe`, `halted`, `queued`, `stuck`, `workflow` and `requested_by` are
 *     `omitempty`, so a live run with nothing to say about them **does not
 *     carry the key at all**. A corpus that wrote `recipe: ''` would teach the
 *     decoder that the key is always there.
 *
 * `spent_usd` and `age_seconds` are `*float64` with no `omitempty`: absent
 * spend is the key present and `null`, which is the contract's own way of
 * saying "not known", and is why the run below carries both. */
function runBlock(overrides: JSONDoc = {}): JSONDoc {
  return {
    run: '20261015-120000-aa01',
    slug: SLUG,
    repo: REPO,
    title: 'wire the console to the live feed',
    stage: 'implement',
    round: '2',
    verify: 'pending',
    models: 'claude-sonnet-4-5-20250929 / claude-opus-4-1',
    auth: 'oauth',
    notional: true,
    spent_usd: 12.5,
    age_seconds: 1500,
    health: 'ok',
    deadline: 'ok',
    ...overrides,
  }
}

/** repoBlock is one repository: a cap, a bar and an off kill switch. `kill`
 * is `null` and not an object, because `jsonKill` returns nil unless the switch
 * is on (`internal/watch/json.go:134`) -- "not killed" is the key present with
 * no value, and the console reads it as a measured fact. */
function repoBlock(overrides: JSONDoc = {}): JSONDoc {
  return {
    slug: SLUG,
    repo: REPO,
    counted_usd: 12.5,
    spent_usd: 0,
    notional_usd: 3.25,
    daily_cap_usd: 50,
    percent: 25,
    burn: { known: true, usd_per_min: 0.02, fast: false },
    kill: null,
    ...overrides,
  }
}

/** liveDocument is the whole document, with the fields each record moves. */
function liveDocument(at: string, overrides: JSONDoc = {}): JSONDoc {
  return {
    project: 'fixture-sandbox',
    mode: 'observe',
    day: at.slice(0, 10),
    now: at,
    connection: { state: 'live', age_seconds: 2 },
    total: {
      counted_usd: 12.5,
      spent_usd: 0,
      notional_usd: 3.25,
      daily_cap_usd: 50,
      percent: 25,
      burn: { known: true, usd_per_min: 0.02, fast: false },
      kill: null,
      runs: 1,
      run_hours: 0.42,
    },
    repos: [repoBlock()],
    runs: [runBlock()],
    finished: [],
    ready: [],
    ...overrides,
  }
}

/** SYNTHETIC_SOURCE is the invented feed: what the recorder would have read off
 * a child's stdout, before redaction. */
export const SYNTHETIC_SOURCE: readonly FixtureRecord[] = Object.freeze([
  { at: '2026-10-15T12:00:00Z', document: liveDocument('2026-10-15T12:00:00Z') },
  {
    at: '2026-10-15T12:00:05Z',
    document: liveDocument('2026-10-15T12:00:05Z', {
      connection: { state: 'live', age_seconds: 4 },
      total: {
        counted_usd: 12.54,
        spent_usd: 0,
        notional_usd: 3.29,
        daily_cap_usd: 50,
        percent: 25.08,
        burn: { known: true, usd_per_min: 0.03, fast: false },
        kill: null,
        runs: 1,
        run_hours: 0.43,
      },
      repos: [repoBlock({ counted_usd: 12.54, notional_usd: 3.29, percent: 25.08 })],
      runs: [runBlock({ spent_usd: 12.54, age_seconds: 1505, recipe: 'console-text-path' })],
      finished: [
        {
          run: '20261015-101500-aa02',
          slug: SLUG,
          repo: REPO,
          title: 'merge feat/console-fixtures for fixture-owner/fixture-repo',
          status: 'ready',
          outcome: 'ready',
          failed: false,
          pr_url: 'https://example.invalid/fixture-owner/fixture-repo/pull/412',
          pr_number: 412,
          age_seconds: 6400,
        },
      ],
    }),
  },
  { at: '2026-10-15T12:00:08Z', notice: 'no budget backend: showing run records only' },
  {
    at: '2026-10-15T12:01:40Z',
    document: liveDocument('2026-10-15T12:01:40Z', {
      connection: { state: 'live', age_seconds: 102 },
      runs: [runBlock({ age_seconds: 1600, health: 'silent' })],
    }),
  },
  {
    // The gap: 160 s after the record before it, which is longer than the 45 s
    // the console's stale banner waits for, and past the CLI's own 180 s
    // lost-after threshold.
    at: '2026-10-15T12:04:20Z',
    document: liveDocument('2026-10-15T12:04:20Z', {
      connection: { state: 'stale', reason: 'no document arrived for 160s', age_seconds: 160 },
      runs: [runBlock({ age_seconds: 1760, health: 'lost' })],
    }),
  },
  {
    // The absent-field case: a run the registry knows only part of, and a
    // repository that has reached its cap.
    at: '2026-10-15T12:04:21Z',
    document: liveDocument('2026-10-15T12:04:21Z', {
      connection: { state: 'live', age_seconds: 1 },
      total: {
        counted_usd: 50,
        spent_usd: 0,
        notional_usd: 3.29,
        daily_cap_usd: 50,
        percent: 100,
        burn: { known: true, usd_per_min: 0, fast: false },
        kill: null,
        runs: 2,
        run_hours: 0.73,
      },
      repos: [repoBlock({ counted_usd: 50, notional_usd: 3.29, percent: 100 })],
      runs: [
        runBlock({ age_seconds: 1761, health: 'lost' }),
        {
          // A queued run: known only from the runs bucket, so its stage, round
          // and models are the CLI's own dashes and its spend is `null`
          // (`queuedRow`, `internal/watch/queued.go:40`). `notional` is false
          // because `auth` is not `oauth` -- there is no account yet to be
          // notional against.
          run: '20261015-120400-aa03',
          slug: SLUG,
          repo: REPO,
          title: 'rotate the fixture tokens',
          stage: 'queued',
          round: '-',
          verify: '-',
          models: '-',
          auth: '-',
          notional: false,
          spent_usd: null,
          age_seconds: 21,
          health: 'ok',
          deadline: 'ok',
          queued: true,
          workflow: 'web',
          requested_by: 'fixture-operator@example.invalid',
        },
      ],
    }),
  },
])

/** SYNTHETIC_META is the header of the committed fixture: **synthetic, and
 * saying so in a sentence a person reads**. It has no `recorded` stamp because
 * it was not recorded anywhere, and no `cli` because no CLI printed it. */
export const SYNTHETIC_META: FixtureMeta = Object.freeze({
  case: 'synthetic',
  project: PROJECT_MARKER,
  synthetic: true,
  note: SYNTHETIC_NOTE,
})

/**
 * syntheticFixture is the committed file, built the way the recorder builds a
 * real one: every record through the redactor, then through `writeFixture`.
 * `redactDocument` audits its own output, so a document this module gets wrong
 * throws here rather than being written.
 */
export function syntheticFixture(): string {
  const redactor = createRedactor(SYNTHETIC_LITERALS, { pseudonymKey: SYNTHETIC_PSEUDONYM_KEY })
  const records: FixtureRecord[] = SYNTHETIC_SOURCE.map((record) => {
    if ('notice' in record) return { at: record.at, notice: redactor.redactText(record.notice) }
    return { at: record.at, document: redactor.redactDocument(record.document) as JSONDoc }
  })
  return writeFixture(records, SYNTHETIC_META)
}
