// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
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
//     scrub-vocabulary.test.ts and the README). The scan's own draws are the
//     exception, and are seeded here: a test that draws its own randomness can
//     only fail on a machine that was unlucky, which is what this file did
//     (CLOCK_SEED).

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import { loadFixture } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { normalFixture } from '../../tools/scrub-fixture/index'
import {
  CONNECTION_REASONS, INVENTED_CONNECTION_REASON, QUEUED_NOTE_SHAPE, VOCAB, isKnownVerifySummary, scrubDocument,
  timeShiftFromDraw,
} from '../../tools/scrub-fixture/scrub'

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

/**
 * DERIVED_TOKEN is the shape of a token the scrub DRAWS rather than copies out
 * of the recording: the clock it shifts -- the header's `recorded`, the
 * envelope's `at`, the document's `now` and `day`, and `kill.at` -- the run IDs
 * it stamps from that shifted clock, and the SHA-256 digest of the body it
 * wrote.
 *
 * The removed-values rule below is a SUBSTRING rule, and a substring rule
 * cannot tell "this text was copied out of the recording" from "this text fell
 * out of the arithmetic by coincidence". Inside a drawn token only the second
 * is possible -- nothing of the recording is in there, the token is a function
 * of the shift and of the invented document -- so the rule is not run over
 * them. The exact-match rule is not exempt: a drawn token that equals a removed
 * value whole is still a failure, and that is a real case rather than a
 * hypothetical one -- a timestamp does exactly that at the offset zero, one of
 * the 2R + 1 = 69,120,000,001 offsets the draw can produce.
 *
 * The coincidence is rare and it is what this file flaked on (CI run
 * 39231293918, `iteration 252`): the recording's `day` is a removed value as
 * soon as the drawn offset moves the clock off that date, and its `kill.at`
 * sits one hour before its `now` -- so an offset between 22 and 23 hours puts
 * the shifted `kill.at` back on the recording's own date while the shifted
 * `now` lands on the next one, and the scan read the removed day inside
 * `kill.at`. One draw in 19200, so about one run in sixty of the 300 draws
 * below; CLOCK_SEED's walk holds one of that kind, and the regression case
 * below names that one (CI's own offset was never recorded, so no seed can
 * name it).
 *
 * The exemption is the shape of the value the FIXTURE CARRIES, never which
 * removed value it is: a removed day that survives where a day does not belong
 * -- in a title, a name -- is still a removed value, and the case in "nothing
 * real is in it" holds that.
 *
 * The pseudonym shapes (`r-...`, `slug-...`) are NOT exempt: those are keyed
 * hashes of values that do not move with the clock, so they are the same every
 * draw and a collision there is fixed rather than a fresh draw -- it would fail
 * on the first run of this file, not on one run in sixty.
 */
const DERIVED_TOKEN = /^(\d{4}-\d{2}-\d{2}|\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z|\d{8}-\d{6}-(ee|ff)\d{2}|[0-9a-f]{64})$/

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
  // A connection's reason: the two sentences internal/watch/state.go writes
  // itself, plus the one the scrub puts in place of the transport's own error
  // text (free text, replaced rather than kept). The list is the scrub's own
  // `CONNECTION_REASONS`, not a copy of it, and the round-trip test below walks
  // every value of it -- so a sentence added there and not carried here fails
  // rather than making a recording the scrub accepts uncommittable.
  new RegExp(`^(${[...CONNECTION_REASONS, INVENTED_CONNECTION_REASON].join('|')})$`),
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
 * run's own removed values are given, any of them -- by substring, except inside
 * a token the scrub drew rather than copied (DERIVED_TOKEN). */
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
        if (!DERIVED_TOKEN.test(raw)) {
          for (const r of removed) if (r.length >= 4 && n.includes(normalise(r))) found.push('a removed value')
        }
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
    // The exemption from the removed-value rule is the SHAPE of the value the
    // fixture carries, not which removed value it is: a removed day standing
    // where a day does not belong is still a removed value. Exempting the
    // removed value's own shape instead -- the shorter way to write the same
    // fix, and the one that would let a recording's day ride out in any field
    // -- passes the regression case below and fails here.
    // The line is a verify line because its name slot is the one the allow-list
    // leaves open (junit.go copies an ID verbatim): without the removed-value
    // rule the line is clean, which the first expectation holds.
    const carried = line(`fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: ${RAW_DAY})`)
    expect(scan(carried), 'without the rule the line is clean').toEqual([])
    expect(scan(carried, [RAW_DAY])).toEqual(['a removed value'])
  })
})

/**
 * RAW_DAY is the day this file's synthetic recording carries, and RAW_KILL is
 * its kill timestamp -- an hour before its `now`. Both are removed values in
 * every draw that moves the clock off that date, and the hour between them is
 * what makes the scan's removed-value rule fire by coincidence (DERIVED_TOKEN).
 */
const RAW_DAY = '2026-10-19'
const RAW_KILL = '2026-10-19T01:00:00Z'

/** CLOCK_SEED seeds every draw this file makes. It is committed, and it is not
 * a nice number on purpose: it was found by walking seeds until the 300 draws
 * held both draws the walk is for -- a digest with twelve digits in it, and the
 * offset that puts the shifted kill.at back on the recording's day -- and 0xf0
 * is one of them, with the crossing early (iteration 13) rather than at the
 * end. A seed whose walk stops covering one of those fails the
 * walk's own two counts, so this cannot quietly become a walk of 300 draws that
 * test nothing. */
const CLOCK_SEED = 0xf0
/** CROSSING_ITERATION is where CLOCK_SEED's walk crosses the date, and the
 * named regression case holds it: a draw of the KIND CI run 39231293918 failed
 * on, not that draw -- CI's offset was drawn at random and never recorded (its
 * message printed the iteration alone), so no seed can name it. */
const CROSSING_ITERATION = 13

/**
 * clockShifts is `count` clock offsets drawn from a committed seed: mulberry32,
 * which is here for one property only -- the same 300 offsets on every run, on
 * every machine. Every other draw in this file is deterministic (the scrub's
 * offsets are passed in), so this file has no uncommitted randomness left.
 */
function clockShifts(seed: number, count: number): number[] {
  const out: number[] = []
  let state = seed >>> 0
  for (let i = 0; i < count; i++) {
    state = (state + 0x6d2b79f5) >>> 0
    let t = Math.imul(state ^ (state >>> 15), state | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    out.push(timeShiftFromDraw(((t ^ (t >>> 14)) >>> 0) / 2 ** 32))
  }
  return out
}

/** digestOf is the SHA-256 the fixture's header carries. */
function digestOf(text: string): string {
  return /[0-9a-f]{64}/.exec(text.split('\n')[0]!)?.[0] ?? ''
}

describe('the scrub', () => {
  const raw = (): Record<string, unknown> => ({
    project: 'real-project', mode: 'enforce', day: RAW_DAY, now: '2026-10-19T02:00:00Z',
    connection: { state: 'live', age_seconds: 0.1 },
    total: {
      counted_usd: 9.37, spent_usd: 9.37, notional_usd: 0, daily_cap_usd: 77.5, percent: 11, burn: { known: false, usd_per_min: 0, fast: false },
      kill: { by: 'Real Person', at: RAW_KILL, reason: 'because real' }, runs: 1, run_hours: 1,
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
      by: 'redacted@example.invalid', at: `${new Date(Date.parse(RAW_KILL) + SHIFT).toISOString().slice(0, 19)}Z`,
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
    // SHIFT is passed here and in the cases below: the scrub's own draw is held
    // (its randomness is held elsewhere -- see the walk), so a scan of a whole
    // scrub run has nothing left in it that varies between runs.
    let removed: readonly string[] = []
    const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r }, SHIFT)
    for (const real of ['real-project', 'real-org/real-repo', '# Task: real secret plan', '9.37', '77.5', 'real@person.example', 'Real Person']) {
      expect(removed).toContain(real)
    }
    expect(scan(text, removed)).toEqual([])
  })

  it('commits a recording whose connection carries a reason, kept or invented', () => {
    // The rule that accepts `connection.reason` is worth nothing if the scan
    // that gates a commit then refuses what the rule emits: the scrub passes,
    // and the recording the rule exists for still cannot be committed. Every
    // branch of internal/watch/state.go goes through the whole tool -- scrub,
    // redactor, writer -- and the text a commit would carry is scanned.
    const cases: Array<[string, string]> = [
      // [what the CLI wrote, what the committed fixture carries]
      ...CONNECTION_REASONS.map((reason): [string, string] => [reason, reason]),
      ['dial tcp 10.0.0.7:443: connect: connection refused', INVENTED_CONNECTION_REASON],
    ]
    for (const [wrote, committed] of cases) {
      const doc = raw()
      at(doc, 'connection')['state'] = 'offline'
      at(doc, 'connection')['reason'] = wrote
      const { text, scrubbed } = normalFixture(doc, '0.8.6', undefined, SHIFT)
      const out = at(scrubbed as unknown as Record<string, unknown>, 'connection')
      expect(out['reason'], wrote).toBe(committed)
      if (committed !== wrote) expect(text, wrote).not.toContain(wrote)
      expect(scan(text), wrote).toEqual([])
    }
  })

  it('commits every value its vocabularies can copy, so no field it keeps is uncommittable', () => {
    // The finding above is one instance of a property the whole tool has to
    // hold: every value the scrub copies out of a CLI vocabulary -- and every
    // one it invents in place of a real value, which the document carries at
    // the same time -- must survive the scan that gates a commit. Each value of
    // each list in VOCAB goes through the whole tool and the text is scanned.
    const fields: Array<[string, readonly string[], (d: Record<string, unknown>, v: string) => void]> = [
      ['mode', VOCAB.mode, (d, v) => { d['mode'] = v }],
      ['connection.state', VOCAB.connectionState, (d, v) => { at(d, 'connection')['state'] = v }],
      ['runs[].stage', VOCAB.stage, (d, v) => { at(d, 'runs', 0)['stage'] = v }],
      ['runs[].auth', VOCAB.auth, (d, v) => { at(d, 'runs', 0)['auth'] = v }],
      ['runs[].health', VOCAB.health, (d, v) => { at(d, 'runs', 0)['health'] = v }],
      ['runs[].deadline', VOCAB.deadline, (d, v) => { at(d, 'runs', 0)['deadline'] = v }],
      ['finished[].status', VOCAB.status, (d, v) => { at(d, 'finished', 0)['status'] = v }],
      ['finished[].outcome', VOCAB.outcome, (d, v) => { at(d, 'finished', 0)['outcome'] = v }],
    ]
    for (const [what, values, set] of fields) {
      for (const value of values) {
        const label = `${what} = ${value}`
        const doc = raw()
        set(doc, value)
        const { text } = normalFixture(doc, '0.8.6', undefined, SHIFT)
        // ...and the value really is in what would be committed, so the scan
        // below is reading it and not passing on a document without it.
        expect(text, label).toContain(JSON.stringify(value))
        expect(scan(text), label).toEqual([])
      }
    }
  })

  it('scans 300 drawn clocks, and trips neither the digit rule nor the removed-values rule', () => {
    // 300 draws, because one draw is not a test of either rule: with a bare
    // /\d{12}/ the scan read twelve digits inside the hex digest as a leaked
    // number about 7% of the time (145 of 2000 scrubs, measured), and the
    // removed-values rule fires on a coincidence of the offset about one draw
    // in 19200 (DERIVED_TOKEN) -- neither is visible in a single scrub.
    //
    // The offsets come from CLOCK_SEED, not from the scrub's own draw, and that
    // is the fix this test needed: it used to draw its own randomness, so it
    // failed on the machines that were unlucky and passed on the rest, and CI
    // run 39231293918 is one of the unlucky ones. The scrub's draw is still
    // random, and the cases that hold that are `scrub-vocabulary.test.ts` (the
    // offset is an integer inside the range) and `is deterministic once the
    // shift is given, and draws a fresh one when it is not` above.
    const shifts = clockShifts(CLOCK_SEED, 300)
    let digitRuns = 0
    let crossings = 0
    for (const [i, shift] of shifts.entries()) {
      let removed: readonly string[] = []
      const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r }, shift)
      // what the walk is made of, so that a seed that stopped covering the two
      // draws above fails here rather than passing quietly:
      //   - a digest carrying twelve digits, counted with the bare rule the
      //     scan used to apply (the draw DIGIT_RUN is written for),
      //   - the shifted kill.at landing back on the recording's own day (the
      //     draw DERIVED_TOKEN is written for).
      if (/\d{12}/.test(digestOf(text))) digitRuns += 1
      if (removed.includes(RAW_DAY) && text.includes(RAW_DAY)) crossings += 1
      expect(scan(text, removed), `iteration ${i} (shift ${shift})`).toEqual([])
    }
    expect(digitRuns, 'the walk draws digests with a twelve-digit run').toBeGreaterThan(0)
    expect(crossings, 'the walk draws the shifted kill.at onto the recording day').toBeGreaterThan(0)
  })

  it('does not read the recording day, inside the shifted kill.at, as a removed value', () => {
    // A draw of the kind CI run 39231293918 failed on (`iteration 252: expected
    // [ "a removed value" ] to deeply equal []`): the offset puts the shifted
    // kill.at back on the recording's own day while `now` lands on the next
    // one, so the recording's day is a removed value AND a substring of a
    // timestamp the scrub drew. CI's own offset is not recoverable -- it was
    // drawn from the crypto random source and only the iteration was printed --
    // so what this holds is the CLASS of draw (any of the offsets in the
    // window), the walk above carries one, and this case names that one, so the
    // rule that reads it right is held by a case that fails when the rule is
    // restored to the substring-only one.
    const shift = clockShifts(CLOCK_SEED, 300)[CROSSING_ITERATION]!
    const shifted = (iso: string): string => new Date(Date.parse(iso) + shift).toISOString().slice(0, 19) + 'Z'
    // the premise, so this case cannot pass on some other draw: `now` is off the
    // recording's day and `kill.at`, an hour behind it, is on it.
    expect(shifted(raw()['now'] as string).slice(0, 10)).not.toBe(RAW_DAY)
    expect(shifted(RAW_KILL).slice(0, 10)).toBe(RAW_DAY)
    let removed: readonly string[] = []
    const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r }, shift)
    expect(text).toContain(JSON.stringify(shifted(RAW_KILL)))
    expect(removed).toContain(RAW_DAY)
    expect(scan(text, removed)).toEqual([])
  })

  it('the scan on the scrub output catches a scrub that keeps a real title or a real amount', () => {
    // A held clock, so the text this doctors is the same text every run.
    let removed: readonly string[] = []
    const { text } = normalFixture(raw(), '0.8.6', (r) => { removed = r }, SHIFT)
    const title = text.replace('# Task: example task 1', '# Task: real secret plan')
    expect(scan(title, removed).length).toBeGreaterThan(0)
    const amount = text.replace('"spent_usd":0.15', '"spent_usd":9.37')
    expect(scan(amount, removed)).toContain('a removed number')
  })
})
