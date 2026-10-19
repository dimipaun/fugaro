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
//   - **nothing real is in it.** A scan for the real names and token shapes, and
//     for the hash of every value the scrub removed (the values themselves are
//     not committed: they are what the scrub is for).
//   - **the scrub refuses what it does not know**, and is deterministic.

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import { loadFixture } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { hashOf, leafValues, scrubDocument } from '../../tools/scrub-fixture/scrub'

const FIXTURE = 'test/fixtures/normal.ndjson'
const HASHES = 'tools/scrub-fixture/normal.scrubbed.json'

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
    expect(loaded.header.synthetic).toBe(false)
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

const REAL_NAME = /dimipaun|edgeappinc|belong|edgeserver|edgeweb|dimi@|youkno|fugaro-dev|fugaro-runs|AIza|gh[p]_|sk-[A-Za-z0-9]|xox[bp]-|\b\d{12}\b/i
const RUN_ID = /\b\d{8}-\d{6}-[0-9a-f]{4}\b/g

/** scan returns what in `text` looks real: a real name or token shape, a value
 * the scrub removed (by hash), or a run ID the scrub removed. */
function scan(text: string, scrubbed: ReadonlySet<string>): string[] {
  const found: string[] = []
  const name = text.match(REAL_NAME)
  if (name !== null) found.push(`real name or token shape (${name[0].length} chars)`)
  for (const line of text.split('\n')) {
    if (line === '') continue
    let parsed: unknown
    try {
      parsed = JSON.parse(line)
    } catch {
      continue
    }
    for (const leaf of leafValues(parsed)) if (scrubbed.has(hashOf(leaf))) found.push('a scrubbed value')
  }
  for (const id of text.match(RUN_ID) ?? []) if (scrubbed.has(hashOf(id))) found.push('a scrubbed run ID')
  return found
}

function scrubbedSet(): ReadonlySet<string> {
  return new Set(JSON.parse(readFileSync(HASHES, 'utf8')) as string[])
}

describe('the normal fixture: nothing real is in it', () => {
  it('has a list of scrubbed values to check against: the run IDs, titles, repo, slug and amounts', () => {
    expect(scrubbedSet().size).toBeGreaterThanOrEqual(25)
  })

  it('carries no real name, token shape, or scrubbed value', () => {
    expect(scan(readFileSync(FIXTURE, 'utf8'), scrubbedSet())).toEqual([])
  })

  it('finds a planted real name, token, scrubbed value and scrubbed run ID', () => {
    const set = new Set([hashOf('# Task: a planted title'), hashOf('20260101-000000-abcd'), hashOf('7.123456')])
    const line = (value: unknown): string => `${JSON.stringify({ at: 'x', document: { v: value } })}\n`
    for (const plant of ['dimipaun/fugaro', 'EdgeServer', 'me@youkno.ai', 'AIzaSyA', 'gh' + 'p_abc', 'sk-abcdef', '123456789012', 'belong']) {
      expect(scan(line(plant), set)).not.toEqual([])
    }
    expect(scan(line('# Task: a planted title'), set)).toEqual(['a scrubbed value'])
    expect(scan(line(7.123456), set)).toEqual(['a scrubbed value'])
    expect(scan(line('x 20260101-000000-abcd y'), set)).toEqual(['a scrubbed run ID'])
    expect(scan(line('an invented title'), set)).toEqual([])
  })
})

describe('the scrub', () => {
  const raw = (): Record<string, unknown> => ({
    project: 'real-project', mode: 'enforce', day: '2026-10-19', now: '2026-10-19T02:00:00Z',
    connection: { state: 'live', age_seconds: 0.1 },
    total: {
      counted_usd: 9, spent_usd: 9, notional_usd: 0, daily_cap_usd: 77, percent: 11, burn: { known: false, usd_per_min: 0, fast: false },
      kill: { by: 'Real Person', at: '2026-10-19T01:00:00Z', reason: 'because real' }, runs: 1, run_hours: 1,
    },
    repos: [{
      slug: 'real-slug', repo: 'real-org/real-repo', counted_usd: 9, spent_usd: 9, notional_usd: 0, daily_cap_usd: 77,
      percent: 11, burn: { known: false, usd_per_min: 0, fast: false }, kill: null,
    }],
    runs: [{
      run: '20261019-000000-abcd', slug: 'real-slug', repo: 'real-org/real-repo', title: '# Task: real secret plan',
      stage: 'implement', round: '-', verify: '-', models: 'vendor/real-model', recipe: 'real-recipe', auth: 'api-key',
      notional: false, spent_usd: 9, age_seconds: 3600, health: 'ok', deadline: 'ok', workflow: 'real-wf',
      requested_by: 'real@person.example',
    }],
    finished: [{
      run: '20261018-000000-beef', slug: 'real-slug', repo: 'real-org/real-repo', title: '# Task: another real plan',
      status: 'succeeded', outcome: 'ready', failed: false, pr_url: 'https://github.com/real-org/real-repo/pull/9',
      pr_number: 9, age_seconds: 90000,
    }],
    ready: [],
  })

  it('replaces every identity and amount, keeps the key order, and recomputes the totals', () => {
    const out = scrubDocument(raw())
    const text = JSON.stringify(out)
    for (const real of ['real-', 'Real Person', 'because real', 'person.example', 'github.com', 'abcd', 'beef', '77']) {
      expect(text).not.toContain(real)
    }
    expect(Object.keys(out)).toEqual(Object.keys(raw()))
    const run = (out['runs'] as Array<Record<string, unknown>>)[0]!
    expect(Object.keys(run)).toEqual(Object.keys((raw()['runs'] as unknown[])[0] as object))
    expect(run['run']).toBe('20261019-010000-ee01') // document.now minus the run's own age
    const total = out['total'] as Record<string, unknown>
    expect(total['spent_usd']).toBe(run['spent_usd'])
    expect(total['percent']).toBeCloseTo(((total['counted_usd'] as number) / 50) * 100, 6)
    expect(total['kill']).toEqual({ by: 'redacted@example.invalid', at: '2026-10-19T01:00:00Z', reason: 'example reason' })
  })

  it('is deterministic', () => {
    expect(JSON.stringify(scrubDocument(raw()))).toBe(JSON.stringify(scrubDocument(raw())))
  })

  it('refuses a key it does not know, at any level, rather than passing it through', () => {
    const top = raw()
    top['surprise'] = 'real'
    expect(() => scrubDocument(top)).toThrow(/unknown key at \$\.surprise/)
    const nested = raw()
    ;((nested['runs'] as Array<Record<string, unknown>>)[0] as Record<string, unknown>)['surprise'] = 'real'
    expect(() => scrubDocument(nested)).toThrow(/unknown key at \$\.runs\[0\]\.surprise/)
    const total = raw()
    ;(total['total'] as Record<string, unknown>)['surprise'] = 1
    expect(() => scrubDocument(total)).toThrow(/unknown key at \$\.total\.surprise/)
  })
})
