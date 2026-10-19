// The `normal` fixture: the shape of one real `fugaro watch --json` document
// (0.8.6), every value invented (owner decision 2026-10-19).
//
// Four things are held here, and each names the mutation it catches:
//
//   - **the shape is the real CLI's.** The key sequence and the JSON type of every
//     field at every level is compared with what 0.8.6 emitted (the Go side,
//     `TestDocsDesktopConsoleNormalFixtureMatchesTheGoTypes`, compares the same
//     file with `internal/watch/json.go`). An unknown key, a dropped key or a
//     retyped value fails.
//   - **the invented numbers agree with each other**, the way the CLI's do.
//   - **nothing real is in it.** Every key and string value of the decoded fixture
//     is checked, after NFKC and case folding, against forbidden substrings, and
//     against a positive allow-list of shapes. The scrub's own removed values are
//     checked in memory, on a scrub of a SYNTHETIC input, never from a file: a
//     file of hashes of removed values is a dictionary oracle and is not kept.
//   - **the scrub refuses what it does not know**, and is deterministic once its
//     clock shift is given -- the shift itself is drawn per run (see
//     scrub-vocabulary.test.ts and the README).

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import { loadFixture } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { normalFixture } from '../../tools/scrub-fixture/index'
import { QUEUED_NOTE_SHAPE, VOCAB, isKnownVerifySummary, scrubDocument } from '../../tools/scrub-fixture/scrub'

const FIXTURE = 'test/fixtures/normal.ndjson'

type Shape = { readonly [key: string]: string | Shape }

/** shapeOf is a value's keys in order and its JSON types, recursively; an array
 * is the shape of its first element under `[]`. */
function shapeOf(value: unknown): string | Shape {
  if (value === null) return 'null'
  if (Array.isArray(value)) return value.length === 0 ? 'array' : { '[]': shapeOf(value[0]) }
  if (typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, shapeOf(v)]))
  return typeof value
}

const BURN = { known: 'boolean', usd_per_min: 'number', fast: 'boolean' }
const REAL_0_8_6_SHAPE = {
  project: 'string',
  mode: 'string',
  day: 'string',
  now: 'string',
  connection: { state: 'string', age_seconds: 'number' },
  total: {
    counted_usd: 'number', spent_usd: 'number', notional_usd: 'number', daily_cap_usd: 'number', percent: 'number',
    burn: BURN, kill: 'null', runs: 'number', run_hours: 'number',
  },
  repos: {
    '[]': {
      slug: 'string', repo: 'string', counted_usd: 'number', spent_usd: 'number', notional_usd: 'number',
      daily_cap_usd: 'number', percent: 'number', burn: BURN, kill: 'null',
    },
  },
  runs: {
    '[]': {
      run: 'string', slug: 'string', repo: 'string', title: 'string', stage: 'string', round: 'string',
      verify: 'string', models: 'string', auth: 'string', notional: 'boolean', spent_usd: 'number',
      age_seconds: 'number', health: 'string', deadline: 'string',
    },
  },
  finished: 'array',
  ready: 'array',
  queued_note: 'string',
}

function document(): JSONDoc {
  const loaded = loadFixture(readFileSync(FIXTURE, 'utf8'), FIXTURE)
  const record = loaded.records[0]
  if (record === undefined || !('document' in record)) throw new Error('the fixture holds no document')
  return record.document
}

describe('the normal fixture: the real shape', () => {
  it('is one verified document, recorded with 0.8.6, and says it is scrubbed', () => {
    const loaded = loadFixture(readFileSync(FIXTURE, 'utf8'), FIXTURE)
    expect(loaded.header.case).toBe('normal')
    expect(loaded.header.cli).toBe('0.8.6')
    expect(loaded.header.note).toContain('every value is invented')
    expect(loaded.records).toHaveLength(1)
  })

  it('has the key sequence and the types 0.8.6 emitted, at every level', () => {
    expect(shapeOf(document())).toEqual(REAL_0_8_6_SHAPE)
    // toEqual ignores key order: hold the order too.
    expect(JSON.stringify(shapeOf(document()))).toBe(JSON.stringify(REAL_0_8_6_SHAPE))
  })

  it('keeps the CLI absences: no optional key a live run does not carry', () => {
    const run = (document()['runs'] as JSONDoc[])[0] as JSONDoc
    for (const key of ['recipe', 'halted', 'queued', 'stuck', 'workflow', 'requested_by']) expect(run).not.toHaveProperty(key)
  })

  it('has numbers that agree: totals are the sums of the rows, percent is over the cap, ages are the document clock', () => {
    const doc = document()
    const total = doc['total'] as Record<string, number>
    const runs = doc['runs'] as Array<Record<string, unknown>>
    const repos = doc['repos'] as Array<Record<string, number>>
    const sum = (rows: Array<Record<string, unknown>>, key: string): number =>
      Math.round(rows.reduce((a, r) => a + (r[key] as number), 0) * 1e6) / 1e6
    expect(total['runs']).toBe(runs.length)
    expect(total['spent_usd']).toBe(sum(runs, 'spent_usd'))
    expect(total['spent_usd']).toBe(sum(repos, 'spent_usd'))
    expect(total['counted_usd']).toBe(total['spent_usd']! + total['notional_usd']!)
    expect(total['percent']).toBeCloseTo((total['counted_usd']! / total['daily_cap_usd']!) * 100, 6)
    for (const repo of repos) expect(repo['percent']).toBeCloseTo((repo['counted_usd']! / repo['daily_cap_usd']!) * 100, 6)
    expect(total['run_hours']).toBeCloseTo(runs.reduce((a, r) => a + (r['age_seconds'] as number), 0) / 3600, 9)
    const now = Date.parse(doc['now'] as string)
    for (const run of runs) {
      const stamp = (run['run'] as string).match(/^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})-/)
      expect(stamp).not.toBeNull()
      const started = Date.parse(`${stamp![1]}-${stamp![2]}-${stamp![3]}T${stamp![4]}:${stamp![5]}:${stamp![6]}Z`)
      expect(Math.abs((now - started) / 1000 - (run['age_seconds'] as number))).toBeLessThanOrEqual(1)
    }
    expect(doc['day']).toBe((doc['now'] as string).slice(0, 10))
  })
})

/** FORBIDDEN is what no fixture string or key may contain, after normalisation. */
const FORBIDDEN = [
  'dimi', 'dimipaun', 'youkno', 'edgeapp', 'belong', 'edgeserver', 'edgeweb', 'fugaro-dev', 'fugaro-prod',
  'sk-', 'aiza', 'gh' + 'p_', 'xox',
]
/**
 * DIGIT_RUN is twelve or more decimal digits standing on their own: a leaked
 * number (an amount, an identifier, an epoch-millisecond stamp) that the
 * allow-list below would otherwise wave through as an invented value.
 *
 * "Standing on their own" is the whole of the rule, and it is what makes this
 * scan deterministic. The header carries a 64-character SHA-256 digest, which is
 * hex, so each of its characters is a digit five eighths of the time: twelve
 * consecutive digits occur inside one about 7% of the time (measured: 145 of
 * 2000 scrubs), and every run draws a fresh clock shift, so the digest is a
 * fresh draw too. A bare /\d{12}/ therefore failed this scan at random -- about
 * one run in fourteen -- while a run of digits with a hex digit beside it is
 * part of a longer hex token (a digest, a pseudonym, a commit) and not a number
 * the scan is for.
 *
 * `{12,}` and not `{12}`: the run is the whole maximal one, and a longer run has
 * digits on both sides of every twelve-character window inside it, so a fixed
 * twelve would match neither lookaround and a thirteen-digit stamp would pass
 * the scan untouched. The lookarounds exempt hex tokens, not long runs.
 */
const DIGIT_RUN = /(?<![0-9a-f])\d{12,}(?![0-9a-f])/

/** DIGEST_WITH_A_DIGIT_RUN is a well-formed 64-hex SHA-256 digest that carries
 * twelve consecutive decimal digits -- what a real digest carries about 7% of
 * the time, so one run in fourteen. */
const DIGEST_WITH_A_DIGIT_RUN = 'abcdef' + '123456789012' + 'ab'.repeat(23)

/** normalise is NFKC, lowercase, zero-width characters stripped. */
function normalise(text: string): string {
  return text.normalize('NFKC').replace(/[\u200b-\u200f\u2060\u2062-\u2064\ufeff\u00ad]/g, '').toLowerCase()
}

/** SHAPES is the positive allow-list: a string value must pass one of these.
 * The verify line is a matcher rather than a regular expression because it is a
 * PREFIX of what verify.go writes (the registry clips it); see scrub.ts. It is
 * the PINNED matcher, `isKnownVerifySummary`, not the scrub's own
 * `isVerifySummary`: the warning-globs slot is a finite vocabulary (the two
 * `commands.reports` defaults the CLI installs), so a glob the CLI was never
 * given cannot pass this list. The test-name slots are NOT pinned that way --
 * junit.go builds an ID verbatim from the report, so no vocabulary can hold it,
 * and the guard there is the FORBIDDEN pass below, which runs over the whole
 * value including the name list. */
const SHAPES: ReadonlyArray<(value: string) => boolean> = [
  /^(<project>|\[redacted-url\])$/,
  /^r-[0-9a-f]{8}\/r-[0-9a-f]{8}$/,
  /^slug-[0-9a-f]{8}$/,
  /^\d{8}-\d{6}-(ee|ff)\d{2}$/,
  /^# Task: example (finished )?task \d{1,3}$/,
  /^(claude-sonnet-4-5-20250929|-)$/,
  new RegExp(`^(${VOCAB.mode.join('|')})$`),
  /^\d{4}-\d{2}-\d{2}$/,
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/,
  new RegExp(`^(${VOCAB.connectionState.join('|')}|${VOCAB.stage.join('|')}|${VOCAB.auth.join('|')}|${VOCAB.health.join('|')}|${VOCAB.deadline.join('|')}|${VOCAB.status.join('|')}|${VOCAB.outcome.join('|')})$`),
  /^\d{1,2}$/,
  (value: string) => isKnownVerifySummary(value),
  (value: string) => QUEUED_NOTE_SHAPE.test(value),
  /^(redacted@example\.invalid|example reason|example halt reason|example-recipe|example-workflow)$/,
  /^https:\/\/example\.invalid\/example-owner\/example-repo\/pull\/\d+$/,
  /^(github-com-example-owner-example-repo|example-owner\/example-repo)(-\d+)?$/,
  // the header: the case, the version strings, the digest, the one-sentence note
  /^(normal|fugaro-console-redact\/\d+|\d+\.\d+\.\d+|[0-9a-f]{64})$/,
  /^normal: the shape of one real fugaro watch --json document; every value is invented \(see the scrub-fixture tool\)$/,
].map((shape) => (typeof shape === 'function' ? shape : (value: string) => shape.test(value)))
const KEYS = new Set([
  'at', 'document', 'project', 'mode', 'day', 'now', 'connection', 'state', 'age_seconds', 'total', 'counted_usd', 'spent_usd',
  'notional_usd', 'daily_cap_usd', 'percent', 'burn', 'known', 'usd_per_min', 'fast', 'kill', 'by', 'reason', 'runs',
  'run_hours', 'repos', 'slug', 'repo', 'run', 'title', 'stage', 'round', 'verify', 'models', 'recipe', 'auth', 'notional',
  'health', 'deadline', 'halted', 'queued', 'stuck', 'workflow', 'requested_by', 'finished', 'ready', 'status', 'outcome',
  'failed', 'pr_url', 'pr_number', 'queued_note',
  'fugaro_fixture', 'version', 'redactor', 'case', 'synthetic', 'cli', 'recorded', 'records', 'sha256', 'note',
])

/** stringsOf walks decoded JSON: every key and every string value, normalised. */
function stringsOf(value: unknown, into: Array<{ text: string; key: boolean }> = []): Array<{ text: string; key: boolean }> {
  if (typeof value === 'string') into.push({ text: value, key: false })
  else if (Array.isArray(value)) for (const v of value) stringsOf(v, into)
  else if (typeof value === 'object' && value !== null) {
    for (const [k, v] of Object.entries(value)) {
      into.push({ text: k, key: true })
      stringsOf(v, into)
    }
  }
  return into
}

/** scan decodes every line and returns what is not clean: a forbidden substring
 * (in a key or a string value, after NFKC, case and zero-width folding), a string
 * outside the positive allow-list of shapes, an unknown key, and, when the scrub
 * run's own removed values are given, any of them. */
function scan(text: string, removed: readonly string[] = []): string[] {
  const found: string[] = []
  for (const line of text.split('\n')) {
    if (line === '') continue
    let parsed: unknown
    try {
      parsed = JSON.parse(line)
    } catch {
      found.push('a line that is not JSON')
      continue
    }
    for (const { text: raw, key } of stringsOf(parsed)) {
      const n = normalise(raw)
      for (const word of FORBIDDEN) if (n.includes(word)) found.push(`forbidden substring ${word}`)
      if (DIGIT_RUN.test(n)) found.push('a run of 12 or more digits')
      if (key) {
        if (!KEYS.has(raw)) found.push('an unknown key')
      } else {
        if (!SHAPES.some((shape) => shape(raw))) found.push('a string outside the allow-list')
        for (const r of removed) if (r.length >= 4 && n.includes(normalise(r))) found.push('a removed value')
        if (removed.includes(raw)) found.push('a removed value')
      }
    }
    for (const r of removed) if (/^-?\d+(\.\d+)?$/.test(r) && JSON.stringify(parsed).includes(`:${r},`)) found.push('a removed number')
  }
  return [...new Set(found)]
}

const line = (value: unknown): string => `${JSON.stringify({ at: '2026-01-01T00:00:00Z', document: { run: value } })}\n`

describe('the normal fixture: nothing real is in it', () => {
  it('has no forbidden substring, token shape, unknown key or string outside the allow-list', () => {
    expect(scan(readFileSync(FIXTURE, 'utf8'))).toEqual([])
  })

  it('says it is synthetic, as its note says every value is invented', () => {
    const loaded = loadFixture(readFileSync(FIXTURE, 'utf8'), FIXTURE)
    expect(loaded.header.synthetic).toBe(true)
  })

  it('finds a planted name, token, confusable, JSON-escaped word, 12-digit run, unknown key and stray string', () => {
    for (const plant of [
      'dimipaun/fugaro', 'EdgeServer', 'me@youkno.ai', 'AIzaSyA', 'gh' + 'p_abc', 'sk-abcdef', 'xo' + 'xb-1', '123456789012', 'belong',
      'x-fugaro-prod', 'fugaro-dev',
    ]) {
      expect(scan(line(plant))).not.toEqual([])
    }
    // the folding itself, not just the allow-list, must see these
    expect(scan(line('ｂｅｌｏｎｇ'))).toContain('forbidden substring belong') // fullwidth confusable
    expect(scan(line('be\u200blong'))).toContain('forbidden substring belong') // zero-width split
    expect(scan(line('ＥＤＧＥＳＥＲＶＥＲ'))).toContain('forbidden substring edgeserver')
    // a JSON-escaped forbidden word: the raw text has no `belong`, the decoded value does
    const escaped = '{"at":"2026-01-01T00:00:00Z","document":{"run":"\\u0062elong"}}\n'
    expect(escaped).not.toContain('belong')
    expect(scan(escaped)).toContain('forbidden substring belong')
    expect(scan('{"at":"2026-01-01T00:00:00Z","document":{"b\\u0065long":"-"}}\n')).toContain('forbidden substring belong')
    expect(scan('{"at":"2026-01-01T00:00:00Z","document":{"surprise":"-"}}\n')).toContain('an unknown key')
    expect(scan(line('some real looking title'))).toContain('a string outside the allow-list')
    expect(scan(line('# Task: example task 1'))).toEqual([])
  })

  it('does not read twelve digits inside a hex digest as a leaked number', () => {
    // The other half of the digit rule, and the one that is deterministic: a
    // digest is the fixture's own header field, and a run of digits inside one
    // is hex, not the number the rule is for. The digest below is the shape the
    // header really carries (64 hex characters) with a twelve-digit run planted
    // in it, which a real digest produces at random -- see DIGIT_RUN.
    const header = readFileSync(FIXTURE, 'utf8').split('\n')[0]!
    const planted = header.replace(/[0-9a-f]{64}/, DIGEST_WITH_A_DIGIT_RUN)
    expect(planted).not.toBe(header)
    expect(planted).toContain('123456789012')
    expect(scan(`${planted}\n`)).toEqual([])
    // ...and a run that stands on its own is still caught, whatever its length:
    // the exemption is for digits inside a hex token, not for long runs. A
    // thirteen-digit epoch-millisecond stamp is the case a fixed `{12}` missed,
    // because every twelve-character window inside it has a digit on both sides.
    for (const run of ['123456789012', '1234567890123', '1750000000000', '12345678901234567890']) {
      expect(scan(line(run)), run).toContain('a run of 12 or more digits')
      expect(scan(line(`acme-${run}`)), `acme-${run}`).toContain('a run of 12 or more digits')
    }
    // ...and the hex exemption still holds for digits inside a hex token, which
    // is the digest case the scan would otherwise fail on at random.
    expect(scan(line('abcdef123456789012abcdef'))).not.toContain('a run of 12 or more digits')
  })

  it('refuses a verify line whose slots carry text the CLI does not write there', () => {
    // The verify line is the one value whose middle the CLI fills from the
    // repository: the test names its reports carry and the globs it reads. A
    // line shaped like the writer's is not enough for the glob slot -- what sits
    // there has to be one of the globs the CLI itself installs, or the positive
    // allow-list would wave through a pattern the CLI was never given.
    expect(scan(line('fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: the Acme Payroll build was slow)'))).toContain('a string outside the allow-list')
    expect(scan(line('fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under **/acme-payroll/*.xml)'))).toContain('a string outside the allow-list')
    // The NAME slot is different, and deliberately: internal/verify/junit.go
    // builds each ID as `classname + "." + name` verbatim from the report, so
    // there is no vocabulary to hold it to -- prose there is a name somebody
    // could have written, and refusing it would make a real recording
    // uncommittable. So a name-list slot passes the shapes even when it reads
    // like prose...
    expect(scan(line('fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: the Acme Payroll suite)'))).toEqual([])
    // ...and what guards that slot is the forbidden-substring pass, which runs
    // over the whole verify value, name list included. This is the case that
    // matters: an identifying name inside a legitimately shaped line.
    expect(scan(line('fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: dimipaun/fugaro)'))).toContain('forbidden substring dimipaun')
    expect(scan(line('fugaro verify test #5: passed (exit 0, 2 tests, 0 failures; flaky: belong)'))).toContain('forbidden substring belong')
    // ...and the slots the CLI really fills still pass, cut or whole.
    expect(scan(line('fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: TestA, pkg/sub.TestB)'))).toEqual([])
    expect(scan(line('fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: test_mod.py::test_add[1-2])'))).toEqual([])
    expect(scan(line('fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under **/junit*.xml, **/buil'))).toEqual([])
    // A test line that stops before its counts is one verify.go cannot produce
    // (it writes them unconditionally for a test), so the allow-list refuses it
    // rather than taking it as a clipped or a quiet line.
    expect(scan(line('fugaro verify test #5: passed (exit 0)'))).toContain('a string outside the allow-list')
    // The name slot is open in the allow-list -- junit.go copies an ID verbatim,
    // so no vocabulary can hold it -- which leaves the digit rule as the one
    // guard against a number riding in there as a "name". A test name carrying
    // an epoch-millisecond stamp is the case to hold: thirteen digits, so the
    // rule has to read the whole maximal run, not a fixed twelve.
    expect(scan(line('fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: TestA_1750000000000)')))
      .toContain('a run of 12 or more digits')
    expect(scan(line('fugaro verify test #5: passed (exit 0, 2 tests, 0 failures; flaky: 12345678901234567890)')))
      .toContain('a run of 12 or more digits')
  })

  it('finds a removed value with a changed prefix, and a removed number', () => {
    expect(scan(line('PREFIX real-title-x'), ['real-title-x'])).toContain('a removed value')
    expect(scan(`{"at":"x","document":{"spent_usd":9.37,"x":1}}\n`, ['9.37'])).toContain('a removed number')
  })
})

describe('the scrub', () => {
  const raw = (): Record<string, unknown> => ({
    project: 'real-project', mode: 'enforce', day: '2026-10-19', now: '2026-10-19T02:00:00Z',
    connection: { state: 'live', age_seconds: 0.1 },
    total: {
      counted_usd: 9.37, spent_usd: 9.37, notional_usd: 0, daily_cap_usd: 77.5, percent: 11, burn: { known: false, usd_per_min: 0, fast: false },
      kill: { by: 'Real Person', at: '2026-10-19T01:00:00Z', reason: 'because real' }, runs: 1, run_hours: 1,
    },
    repos: [{
      slug: 'real-slug', repo: 'real-org/real-repo', counted_usd: 9.37, spent_usd: 9.37, notional_usd: 0, daily_cap_usd: 77.5,
      percent: 11, burn: { known: false, usd_per_min: 0, fast: false }, kill: null,
    }],
    runs: [{
      run: '20261019-000000-abcd', slug: 'real-slug', repo: 'real-org/real-repo', title: '# Task: real secret plan',
      stage: 'implement', round: '-', verify: '-', models: 'vendor/real-model', recipe: 'real-recipe', auth: 'api-key',
      notional: false, spent_usd: 9.37, age_seconds: 3600, health: 'ok', deadline: 'ok', workflow: 'real-wf',
      requested_by: 'real@person.example',
    }],
    finished: [{
      run: '20261018-000000-beef', slug: 'real-slug', repo: 'real-org/real-repo', title: '# Task: another real plan',
      status: 'succeeded', outcome: 'ready', failed: false, pr_url: 'https://github.com/real-org/real-repo/pull/9',
      pr_number: 9, age_seconds: 90000,
    }],
    ready: [],
    queued_note: 'queued runs unavailable: the runs bucket did not answer within 3s',
  })

  /** SHIFT is a shift the tests pass in so that the offset is held still. The
   * scrub draws its own, random, one when it is not given (scrub-vocabulary
   * .test.ts holds that); nothing HERE depends on a committed value, which is
   * the point of review item 2. */
  const SHIFT = -17 * 24 * 3600 * 1000 - 4242

  it('replaces every identity and amount, keeps the key order, and recomputes the totals', () => {
    const out = scrubDocument(raw(), SHIFT)
    const text = JSON.stringify(out)
    for (const real of ['real-', 'real secret plan', 'another real plan', 'Real Person', 'because real', 'person.example', 'github.com', 'abcd', 'beef', '77', '9.37']) {
      expect(text).not.toContain(real)
    }
    expect(Object.keys(out)).toEqual(Object.keys(raw()))
    const run = (out['runs'] as Array<Record<string, unknown>>)[0]!
    expect(Object.keys(run)).toEqual(Object.keys((raw()['runs'] as unknown[])[0] as object))
    // the clock is shifted by the offset given: now, day, the run ID and kill.at all follow it
    const shifted = new Date(Date.parse('2026-10-19T02:00:00Z') + SHIFT).toISOString()
    expect(out['now']).toBe(`${shifted.slice(0, 19)}Z`)
    expect(out['day']).toBe(shifted.slice(0, 10))
    expect(out['now']).not.toBe('2026-10-19T02:00:00Z')
    const start = new Date(Date.parse(out['now'] as string) - 3600 * 1000).toISOString()
    expect(run['run']).toBe(`${start.slice(0, 10).replaceAll('-', '')}-${start.slice(11, 19).replaceAll(':', '')}-ee01`)
    expect(run['run']).not.toContain('010000')
    expect((out['connection'] as Record<string, unknown>)['age_seconds']).toBe(0.1)
    const total = out['total'] as Record<string, unknown>
    expect(total['spent_usd']).toBe(run['spent_usd'])
    expect(total['percent']).toBeCloseTo(((total['counted_usd'] as number) / 50) * 100, 6)
    expect(total['kill']).toEqual({
      by: 'redacted@example.invalid', at: `${new Date(Date.parse('2026-10-19T01:00:00Z') + SHIFT).toISOString().slice(0, 19)}Z`,
      reason: 'example reason',
    })
  })

  it('is deterministic once the shift is given, and draws a fresh one when it is not', () => {
    expect(JSON.stringify(scrubDocument(raw(), SHIFT))).toBe(JSON.stringify(scrubDocument(raw(), SHIFT)))
    expect(scrubDocument(raw())['now']).not.toBe(scrubDocument(raw())['now'])
  })

  const at = (doc: Record<string, unknown>, ...path: Array<string | number>): Record<string, unknown> => {
    let cur: unknown = doc
    for (const p of path) cur = (cur as Record<string | number, unknown>)[p]
    return cur as Record<string, unknown>
  }

  it('refuses a key it does not know, at any level, rather than passing it through', () => {
    const top = raw()
    top['surprise'] = 'real'
    expect(() => scrubDocument(top)).toThrow(/unknown key at \$\.surprise/)
    const cases: Array<[string, (d: Record<string, unknown>) => Record<string, unknown>]> = [
      ['$.runs[0].surprise', (d) => at(d, 'runs', 0)],
      ['$.total.surprise', (d) => at(d, 'total')],
      ['$.repos[0].surprise', (d) => at(d, 'repos', 0)],
      ['$.finished[0].surprise', (d) => at(d, 'finished', 0)],
      ['$.total.kill.surprise', (d) => at(d, 'total', 'kill')],
      ['$.connection.surprise', (d) => at(d, 'connection')],
      ['$.total.burn.surprise', (d) => at(d, 'total', 'burn')],
      ['$.repos[0].burn.surprise', (d) => at(d, 'repos', 0, 'burn')],
    ]
    for (const [path, holder] of cases) {
      const doc = raw()
      holder(doc)['surprise'] = 'real'
      expect(() => scrubDocument(doc), path).toThrow(new RegExp(`unknown key at ${path.replaceAll(/[$[\].]/g, '\\$&')}`))
    }
  })

  it('refuses a value outside the CLI vocabulary or of the wrong type, in every field that is copied', () => {
    const bad: Array<[string, (d: Record<string, unknown>) => void]> = [
      ['stage', (d) => { at(d, 'runs', 0)['stage'] = 'a real stage' }],
      ['round', (d) => { at(d, 'runs', 0)['round'] = 'real round' }],
      ['verify', (d) => { at(d, 'runs', 0)['verify'] = 'fugaro verify real-thing for acme #5: passed' }],
      ['auth', (d) => { at(d, 'runs', 0)['auth'] = 'real-auth' }],
      ['health', (d) => { at(d, 'runs', 0)['health'] = 'real' }],
      ['deadline', (d) => { at(d, 'runs', 0)['deadline'] = 'real' }],
      ['notional', (d) => { at(d, 'runs', 0)['notional'] = 'real' }],
      ['age_seconds', (d) => { at(d, 'runs', 0)['age_seconds'] = 'real' }],
      ['mode', (d) => { d['mode'] = 'real' }],
      ['status', (d) => { at(d, 'finished', 0)['status'] = 'real' }],
      ['outcome', (d) => { at(d, 'finished', 0)['outcome'] = 'real' }],
      ['failed', (d) => { at(d, 'finished', 0)['failed'] = 'real' }],
      ['connection.state', (d) => { at(d, 'connection')['state'] = 'real' }],
      // Every branch internal/watch/state.go writes a reason in writes a
      // string, so only the type is refused here: the two sentences the CLI
      // writes itself are kept and the transport's own error text is replaced,
      // which `scrub-vocabulary.test.ts` holds.
      ['connection.reason', (d) => { at(d, 'connection')['reason'] = 7 }],
      ['burn.known', (d) => { at(d, 'total', 'burn')['known'] = 'real' }],
      ['queued_note', (d) => { d['queued_note'] = 'queued runs unavailable: real-org said no' }],
    ]
    for (const [name, mutate] of bad) {
      const doc = raw()
      mutate(doc)
      expect(() => scrubDocument(doc), name).toThrow(/scrub:/)
    }
  })

  it('hands the removed values to an injected callback, in memory, and writes no hash or file', () => {
    let removed: readonly string[] = []
    const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r })
    for (const real of ['real-project', 'real-org/real-repo', '# Task: real secret plan', '9.37', '77.5', 'real@person.example', 'Real Person']) {
      expect(removed).toContain(real)
    }
    expect(scan(text, removed)).toEqual([])
  })

  it('scans 300 scrubs with a fresh drawn clock each, and never trips the digit rule', () => {
    // The clock offset is drawn per run and never given here, so every iteration
    // writes a different header digest (and different run IDs, and a different
    // `recorded`). 300 of them is the flake this test is for: with a bare
    // /\d{12}/ the scan read twelve digits inside the hex digest as a leaked
    // number about 7% of the time (145 of 2000 scrubs, measured), so three
    // hundred draws caught it with near certainty while a single run of the test
    // above did not.
    for (let i = 0; i < 300; i++) {
      let removed: readonly string[] = []
      const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r })
      expect(scan(text, removed), `iteration ${i}`).toEqual([])
    }
  })

  it('the scan on the scrub output catches a scrub that keeps a real title or a real amount', () => {
    let removed: readonly string[] = []
    const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r })
    const title = text.replace('# Task: example task 1', '# Task: real secret plan')
    expect(scan(title, removed).length).toBeGreaterThan(0)
    const amount = text.replace('"spent_usd":0.15', '"spent_usd":9.37')
    expect(scan(amount, removed)).toContain('a removed number')
  })
})
