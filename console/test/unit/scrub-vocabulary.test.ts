// The scrub's vocabularies and its clock (owner review of PR #643, items 1 and 2).
//
// Three things are held here, and each names the mutation it catches:
//
//   - **the vocabularies are the CLI's.** Every value the Go source writes as a
//     stage, an auth or a mode is ACCEPTED, and a value outside the list is
//     refused. The mutation that matters is the one the review found: a scrub
//     that throws on an ordinary run (`bootstrap`, `review_first`, `finalize`,
//     `vertex`) fails here. The lists themselves are checked against the Go
//     source by `TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource`.
//   - **the verify shape is what verify.go writes**, exactly: every tail
//     `Record.Summary` can append, the `FAILED` spelling, the truncation the
//     registry makes, and a refusal of a line the writer cannot produce.
//   - **the clock shift is per run and drawn, not committed.** Nothing here
//     asserts a particular offset -- that is the point of the change -- so the
//     tests hold consistency (now, day, run IDs, kill.at agree with the ages)
//     and non-reproducibility, never a fixed value.
//
// Every input is a SYNTHETIC document written in this file. No recording of a
// real installation is read, and none is written.

import { describe, expect, it } from 'vitest'

import { isVerifySummary, randomTimeShiftMs, scrubDocument } from '../../tools/scrub-fixture/scrub'

/** raw is the smallest document the scrub accepts: one run, one repo, a total
 * that recomputes. Its values are invented here and identify nothing. */
function raw(): Record<string, unknown> {
  return {
    project: 'not-a-project', mode: 'enforce', day: '2026-03-01', now: '2026-03-01T12:00:00Z',
    connection: { state: 'live', age_seconds: 0.5 },
    total: {
      counted_usd: 1, spent_usd: 1, notional_usd: 0, daily_cap_usd: 10, percent: 10,
      burn: { known: false, usd_per_min: 0, fast: false }, kill: null, runs: 1, run_hours: 1,
    },
    repos: [{
      slug: 'x-slug', repo: 'x-org/x-repo', counted_usd: 1, spent_usd: 1, notional_usd: 0, daily_cap_usd: 10,
      percent: 10, burn: { known: false, usd_per_min: 0, fast: false }, kill: null,
    }],
    runs: [{
      run: '20260301-110000-zz01', slug: 'x-slug', repo: 'x-org/x-repo', title: '# Task: x', stage: 'implement',
      round: '-', verify: '-', models: '-', auth: 'vertex', notional: false, spent_usd: 1, age_seconds: 3600,
      health: 'ok', deadline: 'ok',
    }],
    finished: [],
    ready: [],
  }
}

function at(doc: Record<string, unknown>, ...path: Array<string | number>): Record<string, unknown> {
  let cur: unknown = doc
  for (const p of path) cur = (cur as Record<string | number, unknown>)[p]
  return cur as Record<string, unknown>
}

describe('the scrub: the CLI vocabulary it copies', () => {
  /**
   * STAGES is every value internal/runner or internal/watch writes as a stage,
   * listed with the site that writes it. It is the list review item 1 says was
   * incomplete; the Go drift test holds it against the source, so a stage added
   * there and not here fails rather than silently making the scrub unusable.
   */
  const STAGES: readonly string[] = [
    'bootstrap', // internal/runner/budget_session.go registryEntry, runner.go's record
    'queued', // internal/watch/queued.go: a runs-bucket row with no registry entry
    'provision', // internal/runner/provision.go's provisionStage
    'plan', // internal/runner/artifact.go, runner.go's r.stage(ctx, "plan")
    'implement', // internal/runner/runner.go's r.stage(ctx, "implement")
    'review_first', // internal/runner/runner.go's r.stage(ctx, "review_first")
    'review', // internal/runner/runner.go and reviewmode.go
    'fix', // internal/runner/runner.go's r.stage(ctx, "fix")
    'check', // internal/runner/runner.go's rec.Stage = "check"
    'render', // internal/runner/artifact.go
    'writeback', // internal/runner/lockcache.go
    'finalize', // internal/runner/runner.go's finalize
  ]

  it('accepts every stage the runner writes, so an ordinary run does not throw', () => {
    for (const stage of STAGES) {
      const doc = raw()
      at(doc, 'runs', 0)['stage'] = stage
      expect(() => scrubDocument(doc, 0), stage).not.toThrow()
      expect(at(scrubDocument(doc, 0), 'runs', 0)['stage']).toBe(stage)
    }
  })

  it('refuses a stage the runner does not write, rather than passing it through', () => {
    for (const stage of ['coding', 'tests', 'planning', 'IMPLEMENT', 'queued ', 'a real stage']) {
      const doc = raw()
      at(doc, 'runs', 0)['stage'] = stage
      expect(() => scrubDocument(doc, 0), stage).toThrow(/a value outside the CLI vocabulary/)
    }
  })

  it('accepts the config vocabulary auth can carry, vertex included, and the dash for none', () => {
    for (const auth of ['vertex', 'api-key', 'oauth', '-']) {
      const doc = raw()
      at(doc, 'runs', 0)['auth'] = auth
      expect(at(scrubDocument(doc, 0), 'runs', 0)['auth']).toBe(auth)
    }
    const doc = raw()
    at(doc, 'runs', 0)['auth'] = 'real-auth'
    expect(() => scrubDocument(doc, 0)).toThrow(/a value outside the CLI vocabulary/)
  })

  it('accepts the two modes the budget writes and refuses warn, which is not one', () => {
    for (const mode of ['enforce', 'observe']) {
      const doc = raw()
      doc['mode'] = mode
      expect(scrubDocument(doc, 0)['mode']).toBe(mode)
    }
    const doc = raw()
    doc['mode'] = 'warn'
    expect(() => scrubDocument(doc, 0)).toThrow(/a value outside the CLI vocabulary/)
  })
})

describe('the scrub: the verify line verify.go writes', () => {
  /**
   * LINES is one line per variant `Record.Summary` (internal/verify/verify.go)
   * can produce, with the writer's own spellings: the kinds, the rerun flag, the
   * two status words, the counts a test run carries, the failed and flaky name
   * lists, the timeout, the warning and the not-clean-tree note.
   */
  const LINES: ReadonlyArray<readonly [string, string]> = [
    ['the dash internal/watch/build.go writes for an empty verify', '-'],
    ['a build', 'fugaro verify build #1: passed (exit 0)'],
    ['a lint', 'fugaro verify lint #12: passed (exit 0)'],
    ['a test with counts', 'fugaro verify test #3: passed (exit 0, 41 tests, 0 failures)'],
    ['a rerun', 'fugaro verify test --rerun-failed #4: passed (exit 0, 41 tests, 0 failures)'],
    ['the upper-case failure word', 'fugaro verify test #5: FAILED (exit 1, 41 tests, 2 failures)'],
    ['failed names', 'fugaro verify test #6: FAILED (exit 1, 41 tests, 2 failures: TestA, TestB)'],
    ['flaky names', 'fugaro verify test #7: passed (exit 0, 41 tests, 0 failures; flaky: TestC, TestD)'],
    ['a timeout', 'fugaro verify build #8: FAILED (exit 124; timed out)'],
    ['a warning', 'fugaro verify test #9: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under **/junit*.xml)'],
    ['a dirty tree', 'fugaro verify lint #10: passed (exit 0; working tree not clean, so this run cannot mark the PR ready: commit first)'],
    ['every tail at once', 'fugaro verify test --rerun-failed #11: FAILED (exit 1, 41 tests, 2 failures: TestA; flaky: TestB; timed out; warning: no JUnit reports found under **/junit*.xml; working tree not clean, so this run cannot mark the PR ready: commit first)'],
  ]

  it('accepts one line per variant the writer can produce', () => {
    const doc = raw()
    for (const [what, line] of LINES) {
      const run = at(doc, 'runs', 0)
      run['verify'] = line
      expect(scrubDocument(doc, 0), what).toBeDefined()
      expect(isVerifySummary(line), what).toBe(true)
    }
  })

  it('accepts the clipped line the registry actually stores', () => {
    // internal/runner's verifySummaryRunes clips the summary to 120 runes, so a
    // long warning reaches the document cut in the middle of a word:
    // `... under **/junit*.xml, **/buil`.
    const full = 'fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under **/junit*.xml, **/build*.xml)'
    const clipped = [...full].slice(0, 120).join('')
    expect(clipped.length).toBe(120)
    expect(clipped.endsWith('**/buil')).toBe(true)
    expect(isVerifySummary(clipped)).toBe(true)
    const doc = raw()
    at(doc, 'runs', 0)['verify'] = clipped
    expect(() => scrubDocument(doc, 0)).not.toThrow()
    // Every cut at or after the writer's fixed head is one the clip could make
    // (the head is shorter than the clip, so a cut can never land inside it).
    const head = 'fugaro verify test #5: passed (exit 0'.length
    for (let n = head; n <= clipped.length; n++) {
      expect(isVerifySummary([...clipped].slice(0, n).join('')), `cut at ${n}`).toBe(true)
    }
  })

  it('refuses a line the writer cannot produce', () => {
    for (const line of [
      // the review's own case: a kind that is not one of verify.go's three
      'fugaro verify acme-payroll #5: passed',
      'fugaro verify test #5: passed', // no exit code, no parens
      'fugaro verify test #5: failed (exit 0, 1 tests, 0 failures)', // lower-case failure word
      'fugaro verify test #5: passed (exit zero)',
      'fugaro verify test #5: passed (exit 0, 41 tests)', // counts cut short in the writer's own grammar
      'fugaro verify lint #5: passed (exit 0, 41 tests, 0 failures)', // a lint carries no counts
      'fugaro verify test #5: passed (exit 0; whatever)', // no segment of the writer says this
      'fugaro verify test #5: passed (exit 0, 41 tests, 0 failures) extra',
      'fugaro verify  #5: passed (exit 0)',
      'some real looking verify line',
    ]) {
      expect(isVerifySummary(line), line).toBe(false)
      const doc = raw()
      at(doc, 'runs', 0)['verify'] = line
      expect(() => scrubDocument(doc, 0), line).toThrow(/a value outside the CLI vocabulary/)
    }
  })
})

describe('the scrub: the nullable Go fields', () => {
  it('accepts a null age_seconds and a null percent, which JSONRun and JSONTotal write', () => {
    const doc = raw()
    at(doc, 'runs', 0)['age_seconds'] = null
    at(doc, 'total')['percent'] = null
    at(doc, 'repos', 0)['percent'] = null
    const out = scrubDocument(doc, 0)
    expect(at(out, 'runs', 0)['age_seconds']).toBeNull()
    // A repository with no cap carries null in both (internal/watch/json.go's
    // usdPtr returns the pair together), and the recompute leaves the pair alone
    // rather than inventing a percent over a cap that is not there.
    at(doc, 'total')['daily_cap_usd'] = null
    at(doc, 'repos', 0)['daily_cap_usd'] = null
    const uncapped = scrubDocument(doc, 0)
    expect(at(uncapped, 'total')['percent']).toBeNull()
    expect(at(uncapped, 'total')['daily_cap_usd']).toBeNull()
    expect(at(uncapped, 'repos', 0)['percent']).toBeNull()
  })

  it('still refuses a value that is neither a number nor null', () => {
    for (const [path, holder] of [
      ['$.runs[0].age_seconds', (d: Record<string, unknown>) => at(d, 'runs', 0)],
      ['$.total.percent', (d: Record<string, unknown>) => at(d, 'total')],
    ] as const) {
      const doc = raw()
      holder(doc)[path.endsWith('age_seconds') ? 'age_seconds' : 'percent'] = 'many'
      expect(() => scrubDocument(doc, 0), path).toThrow(/not a number/)
    }
  })
})

describe('the scrub: the clock shift is drawn per run', () => {
  const START = '2026-03-01T12:00:00Z'

  it('draws a shift, applies it, and never reuses it', () => {
    const first = scrubDocument(raw())
    const second = scrubDocument(raw())
    expect(first['now']).not.toBe(START)
    expect(second['now']).not.toBe(START)
    // 2^32 offsets over a 800-day range: two runs landing on the same one is not
    // a thing a test can flake on.
    expect(first['now']).not.toBe(second['now'])
  })

  it('is deterministic once the shift is given, and stays consistent with itself', () => {
    for (const shiftMs of [0, -3600_000, 1234_567_890, -987_654_321]) {
      const a = scrubDocument(raw(), shiftMs)
      const b = scrubDocument(raw(), shiftMs)
      expect(JSON.stringify(a)).toBe(JSON.stringify(b))
      const now = a['now'] as string
      const shifted = new Date(Date.parse(START) + shiftMs).toISOString()
      expect(now).toBe(`${shifted.slice(0, 19)}Z`)
      expect(a['day']).toBe(shifted.slice(0, 10))
      // the run's own start is the shifted clock minus its age, so the age the
      // document reports is the difference between the two
      const run = at(a, 'runs', 0)
      const stamp = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})-/.exec(run['run'] as string)!
      const started = Date.parse(`${stamp[1]}-${stamp[2]}-${stamp[3]}T${stamp[4]}:${stamp[5]}:${stamp[6]}Z`)
      expect(Math.abs((Date.parse(now) - started) / 1000 - (run['age_seconds'] as number))).toBeLessThanOrEqual(1)
    }
  })

  it('moves kill.at by the same shift, and covers the whole range it claims', () => {
    const doc = raw()
    at(doc, 'total')['kill'] = { by: 'x', at: '2026-03-01T09:00:00Z', reason: 'y' }
    const shiftMs = -8 * 24 * 3600 * 1000
    const kill = at(scrubDocument(doc, shiftMs), 'total', 'kill')
    expect(kill['at']).toBe(`${new Date(Date.parse('2026-03-01T09:00:00Z') + shiftMs).toISOString().slice(0, 19)}Z`)
    // the drawn offset is inside the range scrub.ts documents
    const range = 400 * 24 * 3600 * 1000
    for (let i = 0; i < 64; i++) {
      const drawn = randomTimeShiftMs()
      expect(Number.isInteger(drawn)).toBe(true)
      expect(Math.abs(drawn)).toBeLessThanOrEqual(range)
    }
  })
})
