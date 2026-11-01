// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The scrub's vocabularies, its key tables and its clock (owner review of PR
// #643, items 1 and 2; review of PR #754).
//
// Four things are held here, and each names the mutation it catches:
//
//   - **the vocabularies are the CLI's.** Every value the Go source writes into
//     the field is ACCEPTED, and a value outside the list is refused. The
//     mutation that matters is the one the review found: a scrub that throws on
//     an ordinary run (`bootstrap`, `review_first`, `vertex`) fails here. The
//     lists themselves are the scrub's own -- this file holds no second copy of
//     one -- and `TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource` reads
//     them back out of `scrub.ts` and holds them against the Go source, in both
//     directions, so a list that is too long fails there as well as too short.
//   - **the verify shape is what verify.go writes**, exactly: every tail
//     `Record.Summary` can append, the `FAILED` spelling, the truncation the
//     registry makes, and a refusal of a line the writer cannot produce.
//   - **the run-key tables are the live shapes'.** A row carrying every key of
//     `RUN_KEYS` or `FINISHED_KEYS` is scrubbed, not refused as `an unknown
//     key`; the mutation is a table missing one of the seven keys the 0.9.0 and
//     0.10.0 releases added, which is what review of PR #754 found (and a
//     near-miss key is still refused, so the tables stay closed).
//   - **the clock shift is per run and drawn, not committed.** Nothing here
//     asserts a particular offset -- that is the point of the change -- so the
//     tests hold consistency (now, day, run IDs, kill.at and waiting_until agree
//     with the ages) and non-reproducibility, never a fixed value.
//
// Every input is a SYNTHETIC document written in this file. No recording of a
// real installation is read, and none is written.

import { describe, expect, it } from 'vitest'

import {
  CONNECTION_REASONS, INVENTED_CONNECTION_REASON, INVENTED_LEASE_USD, INVENTED_MAX_PARALLEL, TIME_SHIFT_RANGE_MS,
  VERIFY_CLIP_RUNES, VOCAB, isKnownVerifySummary, isVerifySummary, randomTimeShiftMs, scrubDocument,
  timeShiftFromDraw,
} from '../../tools/scrub-fixture/scrub'
import { FINISHED_KEYS, RUN_KEYS } from '../../src/shared/schema'

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
   * STAGES is the scrub's own stage list, not a second copy of it: the list is
   * `VOCAB.stage`, held against the Go source by
   * `TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource`. A copy here was
   * the third hand-kept list review found, and it could only ever agree with
   * itself -- a stage the runner writes and the scrub refuses is the failure
   * this loop is for, so it reads the scrub's list, and the drift test is what
   * says whether that list is the CLI's.
   */
  const STAGES: readonly string[] = VOCAB.stage

  it('accepts every stage in that list, so an ordinary run does not throw', () => {
    for (const stage of STAGES) {
      const doc = raw()
      at(doc, 'runs', 0)['stage'] = stage
      expect(() => scrubDocument(doc, 0), stage).not.toThrow()
      expect(at(scrubDocument(doc, 0), 'runs', 0)['stage']).toBe(stage)
    }
  })

  it('refuses a stage the runner does not write, rather than passing it through', () => {
    // The last five are stages the runner does write -- as the run record's own
    // stage and as provisioning's timing name -- but not into the registry
    // entry runs[].stage comes from, so a scrub that took them would pass a
    // value the CLI cannot put there.
    const outside = ['coding', 'tests', 'planning', 'IMPLEMENT', 'queued ', 'a real stage',
      'check', 'render', 'writeback', 'finalize', 'provision']
    for (const stage of outside) {
      const doc = raw()
      at(doc, 'runs', 0)['stage'] = stage
      expect(() => scrubDocument(doc, 0), stage).toThrow(/a value outside the CLI vocabulary/)
    }
  })

  it('accepts the config vocabulary auth can carry, vertex included, and the dash for none', () => {
    for (const auth of ['vertex', 'api-key', 'oauth', 'cascade', '-']) {
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

  it('keeps the connection reasons the CLI writes, and replaces the transport\'s own text', () => {
    for (const reason of CONNECTION_REASONS) {
      const doc = raw()
      at(doc, 'connection')['state'] = 'offline'
      at(doc, 'connection')['reason'] = reason
      expect(at(scrubDocument(doc, 0), 'connection')['reason']).toBe(reason)
    }
    // internal/watch/state.go's third branch is the transport's error, one-lined
    // and clipped there: free text, and ordinary in a recording of an offline
    // run. It is replaced, never refused -- a scrub with no rule for `reason`
    // threw on the whole document for it.
    const doc = raw()
    at(doc, 'connection')['state'] = 'offline'
    at(doc, 'connection')['reason'] = 'dial tcp 10.0.0.7:443: connect: connection refused'
    const scrubbed = at(scrubDocument(doc, 0), 'connection')
    expect(scrubbed['reason']).toBe(INVENTED_CONNECTION_REASON)
    expect(scrubbed['state']).toBe('offline')
    expect(JSON.stringify(scrubbed)).not.toContain('10.0.0.7')
  })

  it('refuses a connection reason that is not a string, rather than inventing one', () => {
    const doc = raw()
    at(doc, 'connection')['reason'] = 7
    expect(() => scrubDocument(doc, 0)).toThrow(/not a string/)
  })
})

describe('the scrub: the verify line verify.go writes', () => {
  /**
   * LINES is one line per variant `Record.Summary` (internal/verify/verify.go)
   * can produce, with the writer's own spellings: the kinds, the rerun flag, the
   * two status words, the counts a test run carries, the failed and flaky name
   * lists, the timeout, the warning and the not-clean-tree note. The exit codes
   * are the ones the writer can actually carry: `internal/procgroup/procgroup.go`
   * leaves `code` at -1 when the process never started or was killed by a signal
   * -- which is what its own timeout does -- and `verify.go` records that code as
   * `ExitCode` verbatim, so `exit -1` is an ordinary timed-out verify.
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
    ['a timeout', 'fugaro verify build #8: FAILED (exit -1; timed out)'],
    ['a test killed by its own timeout', 'fugaro verify test #8: FAILED (exit -1, 41 tests, 3 failures: TestA, TestB, TestC; timed out)'],
    ['a command killed by a signal without a timeout', 'fugaro verify lint #13: FAILED (exit -1)'],
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
    // internal/runner's verifySummaryRunes clips the summary to
    // VERIFY_CLIP_RUNES runes (the drift test holds the number to the source),
    // so a long warning reaches the document cut in the middle of a word:
    // `... under **/junit*.xml, **/buil`.
    const full = 'fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under **/junit*.xml, **/build/test-results/**/*.xml)'
    const clipped = [...full].slice(0, VERIFY_CLIP_RUNES).join('')
    expect(clipped.length).toBe(VERIFY_CLIP_RUNES)
    expect(clipped.endsWith('**/buil')).toBe(true)
    expect(isVerifySummary(clipped)).toBe(true)
    const doc = raw()
    at(doc, 'runs', 0)['verify'] = clipped
    expect(() => scrubDocument(doc, 0)).not.toThrow()
    // Every cut at or after the writer's counts is one the clip could make (the
    // head is shorter than the clip, so a cut can never land inside it -- and
    // neither can it land inside the counts of a test, which end far before it).
    const head = 'fugaro verify test #5: passed (exit 0'.length
    const afterCounts = 'fugaro verify test #5: passed (exit 0, 0 tests, 0 failures'.length
    for (let n = head; n < afterCounts; n++) {
      expect(isVerifySummary([...clipped].slice(0, n).join('')), `cut at ${n}`).toBe(false)
    }
    for (let n = afterCounts; n <= clipped.length; n++) {
      expect(isVerifySummary([...clipped].slice(0, n).join('')), `cut at ${n}`).toBe(true)
    }
  })

  it('refuses a line the writer cannot produce', () => {
    for (const line of [
      // the review's own case: a kind that is not one of verify.go's three,
      // complete line and all -- the kind is the whole guard, so a head that
      // took `[a-z][a-z-]*` would accept a verify nobody can run
      'fugaro verify acme-payroll #5: passed (exit 0)',
      'fugaro verify tests #5: passed (exit 0)',
      'fugaro verify Test #5: passed (exit 0)',
      'fugaro verify test #5: passed', // no exit code, no parens
      'fugaro verify test #5: failed (exit 0, 1 tests, 0 failures)', // lower-case failure word
      'fugaro verify test #5: passed (exit zero)',
      'fugaro verify test #5: passed (exit 0)', // the counts of a test run are not optional
      'fugaro verify test #5: passed (exit 0, 41 tests)', // ...nor can their tail be cut short
      'fugaro verify lint #5: passed (exit 0, 41 tests, 0 failures)', // a lint carries no counts
      'fugaro verify build #5: passed (exit 0, 41 tests, 0 failures)', // ...and neither does a build
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

  it('requires the counts of a test run, and refuses them on any other kind', () => {
    // `Record.Summary` appends `, %d tests, %d failures` under
    // `if r.Kind == KindTest` and nowhere else (the drift test pins that
    // condition, since its tail scan skips `if` lines). So for a test the
    // segment is not optional -- the writer always writes it, and the 120-rune
    // clip ends far after it -- and for a build or a lint it is a segment the
    // writer never writes.
    const withCounts = (rest: string): string => `fugaro verify test #5: passed (exit 0${rest})`
    for (const rest of ['', ', ', ', 41', ', 41 tests', ', 41 tests, ', ', 41 tests, 0']) {
      expect(isVerifySummary(withCounts(rest)), `test${JSON.stringify(rest)}`).toBe(false)
      expect(isKnownVerifySummary(withCounts(rest)), `known test${JSON.stringify(rest)}`).toBe(false)
    }
    for (const rest of [', 41 tests, 0 failures', ', 41 tests, 0 failures: TestA, TestB']) {
      expect(isVerifySummary(withCounts(rest)), `test${rest}`).toBe(true)
      expect(isKnownVerifySummary(withCounts(rest)), `known test${rest}`).toBe(true)
    }
    for (const kind of ['build', 'lint']) {
      expect(isVerifySummary(`fugaro verify ${kind} #5: passed (exit 0)`), kind).toBe(true)
      expect(isKnownVerifySummary(`fugaro verify ${kind} #5: passed (exit 0)`), kind).toBe(true)
      expect(isVerifySummary(`fugaro verify ${kind} #5: passed (exit 0, 41 tests, 0 failures)`), kind).toBe(false)
      expect(isVerifySummary(`fugaro verify ${kind} #5: passed (exit 0, 0 tests)`), kind).toBe(false)
      // ...while the segments a build or a lint does write are still there.
      expect(isVerifySummary(`fugaro verify ${kind} #5: FAILED (exit 1; timed out)`), kind).toBe(true)
    }
  })

  /**
   * The scrub takes any text in the writer's two open slots, because a real
   * recording's test names are the writer's own output and a vocabulary that
   * refused one would refuse an ordinary run. The fixture's scan cannot afford
   * that: it is the positive allow-list over a committed file, and an open slot
   * lets an identifying string ride in one. So the scan uses the pinned matcher,
   * and these are its two directions.
   */
  it('accepts every line the writer produces when the slots are pinned too', () => {
    for (const [what, line] of LINES) expect(isKnownVerifySummary(line), what).toBe(true)
    const full = 'fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: TestA, pkg/sub.TestB; warning: no JUnit reports found under **/junit*.xml)'
    const afterCounts = 'fugaro verify test #5: passed (exit 0, 2 tests, 0 failures'.length
    for (let n = afterCounts; n <= full.length; n++) {
      expect(isKnownVerifySummary([...full].slice(0, n).join('')), `cut at ${n}`).toBe(true)
    }
  })

  it('refuses, when pinned, the text the CLI does not write into a slot', () => {
    // The warning sentence is the CLI's own text, not a slot: `Record.Summary`
    // writes one sentence there (`no JUnit reports found under ` + the repo's
    // globs), so both matchers refuse a warning the CLI cannot write, and the
    // Go drift test is what fails first if verify.go ever gains a second one.
    const prose = 'fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: the Acme Payroll build was slow)'
    expect(isVerifySummary(prose)).toBe(false)
    expect(isKnownVerifySummary(prose)).toBe(false)
    const globs = 'fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under **/junit*.xml)'
    expect(isVerifySummary(globs)).toBe(true)
    expect(isKnownVerifySummary(globs)).toBe(true)
    // The name slots are NOT like this: a name is the report's own text, so
    // prose there is a name someone could have written, and the pinned matcher
    // takes it too. What guards that slot is the fixture scan's
    // forbidden-substring pass over the whole value -- see the test below and
    // `normal-fixture.test.ts`.
    const named = 'fugaro verify test #5: passed (exit 0, 2 tests, 0 failures: the Acme Payroll suite)'
    expect(isVerifySummary(named)).toBe(true)
    expect(isKnownVerifySummary(named)).toBe(true)
  })

  it('takes the # go-junit-report puts on a duplicated subtest name', () => {
    // A recording from a repository whose runner reports a duplicated subtest
    // carries `TestFoo#01` in the name list. The scrub takes it either way; the
    // fixture's scan must too, or a legitimate recording could not be committed.
    const line = 'fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: TestFoo#01, TestFoo#02)'
    expect(isVerifySummary(line)).toBe(true)
    expect(isKnownVerifySummary(line)).toBe(true)
    const cut = 'fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: TestFoo#'
    expect(isKnownVerifySummary(cut)).toBe(true)
  })

  it('takes the test IDs junit.go builds verbatim, not just Go-style names', () => {
    // internal/verify/junit.go builds every ID as `classname + "." + name`,
    // straight out of the JUnit XML, so the name list is the report's own text:
    // a pytest parametrisation carries brackets and an `=`, and a name may carry
    // a space, a bare comma or the escape pluginwire.Printable writes in place of
    // a bidi character. A vocabulary narrow enough to look like test names
    // refuses real ones -- and a recording the scan refuses cannot be committed,
    // which is why the name slots are open in both matchers.
    for (const names of [
      'test_mod.py::test_add[1-2]',
      'test_mod.py::test_add[a=1, b=2]',
      'TestFoo/sub test with spaces',
      'pkg.TestX.\\u202e', // a bidi character as Printable escapes it
      'TestA, pkg/sub.TestB_and-more:2',
    ]) {
      const line = `fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: ${names})`
      expect(isKnownVerifySummary(line), names).toBe(true)
      const flaky = `fugaro verify test #5: passed (exit 0, 2 tests, 0 failures; flaky: ${names}; timed out)`
      expect(isKnownVerifySummary(flaky), names).toBe(true)
      // and a clip landing inside one is still a prefix
      const cut = `fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: ${names}`.slice(0, 60)
      expect(isKnownVerifySummary(cut), names).toBe(true)
    }
    // The one thing the slot still refuses is a control character, which
    // internal/runner's Printable never emits raw.
    expect(isKnownVerifySummary('fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: TestA\u0007)')).toBe(false)
    expect(isKnownVerifySummary('fugaro verify test #5: FAILED (exit 1, 2 tests, 1 failures: TestA\u007f)')).toBe(false)
  })

  it('pins the warning globs to the vocabulary the CLI writes there', () => {
    const line = (globs: string): string =>
      `fugaro verify test #5: passed (exit 0, 0 tests, 0 failures; warning: no JUnit reports found under ${globs})`
    // The two `commands.reports` globs internal/config/defaults.go installs, in
    // the order internal/verify/reports.go joins them, and each on its own.
    for (const globs of ['**/junit*.xml', '**/build/test-results/**/*.xml', '**/junit*.xml, **/build/test-results/**/*.xml']) {
      expect(isVerifySummary(line(globs)), globs).toBe(true)
      expect(isKnownVerifySummary(line(globs)), globs).toBe(true)
    }
    for (const globs of ['**/acme-payroll/*.xml', 'reports/*.json', 'AcmePayroll', '**/junit*.xml, **/acme-payroll/*.xml']) {
      // The scrub keeps its open rule -- a repository may configure any globs
      // and a recording of it must still scrub -- and the scan is what refuses
      // a glob the CLI's own vocabulary does not carry.
      expect(isVerifySummary(line(globs)), globs).toBe(true)
      expect(isKnownVerifySummary(line(globs)), globs).toBe(false)
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

  it('replaces a leased_usd with the invented lease, the money a lease is holding', () => {
    // `leased_usd` is on `total` and on every `repos[]` entry, written by the
    // CLI now (counted minus spent), so a recording carries it and the scrub
    // has to name it: the failure without a rule is the whole document --
    // `applyRules` refuses a key it has none for, as `an unknown key at
    // $.total.leased_usd`.
    //
    // The amount is invented, not copied: a lease is what the installation
    // currently holds, so it is a real dollar figure like any other, and the
    // header's rule for those is an invented constant. Copying it through put a
    // real amount in the committed fixture -- and `removedValues` could not
    // catch it, since a value that survives is by definition not a removed one.
    const doc = raw()
    at(doc, 'total')['leased_usd'] = 7.25
    at(doc, 'repos', 0)['leased_usd'] = 7.25
    const out = scrubDocument(doc, 0)
    expect(at(out, 'total')['leased_usd']).toBe(INVENTED_LEASE_USD)
    expect(at(out, 'repos', 0)['leased_usd']).toBe(INVENTED_LEASE_USD)
    expect(JSON.stringify(out)).not.toContain('7.25')
  })

  it('accepts a null leased_usd, the shape an older recording could leave', () => {
    const doc = raw()
    at(doc, 'total')['leased_usd'] = null
    at(doc, 'repos', 0)['leased_usd'] = null
    const out = scrubDocument(doc, 0)
    expect(at(out, 'total')['leased_usd']).toBeNull()
    expect(at(out, 'repos', 0)['leased_usd']).toBeNull()
  })

  it('refuses a leased_usd that is neither a number nor null, like the other pointers', () => {
    const doc = raw()
    at(doc, 'total')['leased_usd'] = 'many'
    expect(() => scrubDocument(doc, 0)).toThrow(/not a number at \$\.total\.leased_usd/)
  })

  it('replaces a total max_parallel with the invented cap, and keeps a null one', () => {
    // `max_parallel` -- the concurrent-run cap a launch is checked against --
    // is a key `JSONTotal` gained after the released consoles and the committed
    // fixtures, so until the scrub names it every recording the new CLI writes
    // is refused **whole**, as `an unknown key at $.total.max_parallel`. The
    // amount is the operator's own configuration, so it is invented like the
    // daily cap rather than copied through.
    const doc = raw()
    at(doc, 'total')['max_parallel'] = 3
    const out = scrubDocument(doc, 0)
    expect(at(out, 'total')['max_parallel']).toBe(INVENTED_MAX_PARALLEL)
    expect(JSON.stringify(out)).not.toContain('"max_parallel":3')
    // `null` is the field's own "no cap could be read" (`*int` with no
    // `omitempty`), so it is a value to keep, not a fault.
    at(doc, 'total')['max_parallel'] = null
    expect(at(scrubDocument(doc, 0), 'total')['max_parallel']).toBeNull()
    // Anything else is refused where it sits, like the other pointers.
    at(doc, 'total')['max_parallel'] = 'many'
    expect(() => scrubDocument(doc, 0)).toThrow(/not a number at \$\.total\.max_parallel/)
  })

  it('still refuses a max_parallel on a repos[] entry, which no CLI writes', () => {
    // A cap bounds the *project's* concurrent launches, so `JSONRepo` carries
    // no such key: the rule is the total's own, and a repository that grew one
    // is a document this reader does not know.
    const doc = raw()
    at(doc, 'repos', 0)['max_parallel'] = 3
    expect(() => scrubDocument(doc, 0)).toThrow(/an unknown key at \$\.repos\[0\]\.max_parallel/)
  })
})

describe('the scrub: the keys the live run shapes write', () => {
  /**
   * NEWER_KEYS is the keys whose rule this block is about: the five 0.9.0 keys
   * (`job`, `profile`, `provider`, `account`, `launched_by`) and the two 0.10.0
   * ones (`credential`, `waiting_until`). A recording that carries them was
   * refused **whole**, as `an unknown key at $.runs[0].credential`, because
   * `applyRules` copies a key only through its own rule and the scrub's two run
   * tables had none of the seven -- so the documented record -> scrub -> commit
   * workflow could not be run at all on a current recording, and the key lists
   * in `src/shared/schema.ts`, which carry all seven, could not say so.
   *
   * The six text keys must be *replaced*: `account` and `launched_by` are
   * people and `credential` names a token's account, so a rule that passed the
   * value through would publish it. `waiting_until` is a stamp and must be
   * shifted with the rest of the clock, which is what the `src/shared/schema.ts`
   * comment beside it claims.
   */
  const NEWER_TEXT_KEYS = ['job', 'profile', 'provider', 'account', 'launched_by', 'credential'] as const
  const NEWER_STAMP_KEYS = ['waiting_until'] as const
  const SHIFT = 30 * 60 * 1000
  const WAITING = '2026-03-01T13:00:00Z'

  /**
   * LIVE_VALUES is one plausible wire value for every key of `RUN_KEYS` and
   * `FINISHED_KEYS` that `raw()` does not already carry, typed the way
   * `internal/watch/json.go` types it: `queued`, `stuck` and `failed` are
   * bools, `pr_number` an int, `waiting_until` an instant, the rest strings.
   * `fill` refuses a key with no value here, so the test cannot pass by having
   * nothing to say about one.
   */
  const LIVE_VALUES: Readonly<Record<string, unknown>> = {
    recipe: 'example-recipe', halted: 'example halt reason', queued: true, stuck: true,
    workflow: 'example-workflow', requested_by: 'redacted@example.invalid',
    job: 'x-job', profile: 'x-profile', provider: 'x-provider', account: 'x-account',
    launched_by: 'x-launcher', credential: 'x-credential', waiting_until: WAITING,
    plan_task: 'x-plan/x-task',
    status: 'succeeded', outcome: 'ready', failed: false,
    pr_url: 'https://example.invalid/example-owner/example-repo/pull/1', pr_number: 1,
  }

  /** fill gives a row every key of `keys`, keeping the value it already has and
   * inventing the rest from LIVE_VALUES. */
  function fill(row: Record<string, unknown>, keys: readonly string[]): void {
    for (const key of keys) {
      if (key in row) continue
      if (!(key in LIVE_VALUES)) throw new Error(`this test has no value for ${key}`)
      row[key] = LIVE_VALUES[key]
    }
  }

  it('scrubs a run carrying every key of JSONRun, rather than refusing one as unknown', () => {
    const doc = raw()
    const row = at(doc, 'runs', 0)
    fill(row, RUN_KEYS)
    const out = at(scrubDocument(doc, SHIFT), 'runs', 0)
    // every key the shape writes comes out the other side: a rule the scrub has
    // not got is a key it refuses, and one it has but the shape does not is a
    // key this assertion would see as extra
    expect(Object.keys(out).sort()).toEqual([...RUN_KEYS].sort())
    for (const key of NEWER_TEXT_KEYS) expect(out[key], key).not.toBe(row[key])
    expect(out['waiting_until']).toBe('2026-03-01T13:30:00Z')
  })

  it('scrubs a finished run carrying every key of JSONFinished', () => {
    const doc = raw()
    // one finished row, built out of the run row for the keys the two shapes
    // share (`run`, `slug`, `repo`, `title`, `age_seconds`), so the row carries
    // no key of its own that JSONFinished does not have
    const run = at(doc, 'runs', 0)
    const finished: Record<string, unknown> = {}
    for (const key of FINISHED_KEYS) if (key in run) finished[key] = run[key]
    doc['finished'] = [finished]
    fill(finished, FINISHED_KEYS)
    const out = at(scrubDocument(doc, SHIFT), 'finished', 0)
    expect(Object.keys(out).sort()).toEqual([...FINISHED_KEYS].sort())
    for (const key of ['job', 'profile', 'provider', 'account', 'launched_by']) {
      expect(out[key], key).not.toBe(finished[key])
    }
  })

  it('still refuses a run key neither shape writes', () => {
    // the other direction, and the one the rules must not have cost: the tables
    // are closed, so a near miss is refused by name rather than copied through
    const doc = raw()
    at(doc, 'runs', 0)['credential_name'] = 'x'
    expect(() => scrubDocument(doc, SHIFT)).toThrow(/an unknown key at \$\.runs\[0\]\.credential_name/)
  })
})

describe('the scrub: the clock shift is drawn per run', () => {
  const START = '2026-03-01T12:00:00Z'

  it('draws a shift, applies it, and never reuses it', () => {
    const first = scrubDocument(raw())
    const second = scrubDocument(raw())
    expect(first['now']).not.toBe(START)
    expect(second['now']).not.toBe(START)
    // 2^53 offsets over a 800-day range: two runs landing on the same one is not
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
    for (let i = 0; i < 64; i++) {
      const drawn = randomTimeShiftMs()
      expect(Number.isInteger(drawn)).toBe(true)
      expect(Math.abs(drawn)).toBeLessThanOrEqual(TIME_SHIFT_RANGE_MS)
    }
  })

  it('maps the draw onto the range end to end, without overshooting it', () => {
    // The ends, deterministically: no fraction is smaller than 0 or larger than
    // the largest one below 1, so these are the extremes the draw can reach.
    const LAST = 1 - 2 ** -53
    expect(timeShiftFromDraw(0)).toBe(-TIME_SHIFT_RANGE_MS)
    expect(timeShiftFromDraw(LAST)).toBe(TIME_SHIFT_RANGE_MS)
    expect(timeShiftFromDraw(0)).toBeLessThan(0)
    expect(timeShiftFromDraw(LAST)).toBeGreaterThan(0)
    // ...and nothing between them overshoots, which is what a rounding `Math.round`
    // over `2R + 1` did at the top: it returned the range plus one.
    for (const unit of [LAST, 1 - 2 ** -52, 0.9999999999999999, 0.75, 0.5, 0.25, 2 ** -53, 0]) {
      const shift = timeShiftFromDraw(unit)
      expect(Number.isInteger(shift), `${unit}`).toBe(true)
      expect(Math.abs(shift), `${unit}`).toBeLessThanOrEqual(TIME_SHIFT_RANGE_MS)
    }
    // the mapping spans the range rather than piling up at one end: the middle
    // of the unit interval is the middle of the range, which for an odd span is
    // the offset 0
    expect(timeShiftFromDraw(0.5)).toBe(0)
  })
})
