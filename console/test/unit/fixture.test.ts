// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The fixture format, its loader and the replay, held to their own contract
// (desktop-console plan Task 1.4).
//
// Three things are under test here, and each names the mutation it catches:
//
//   - **the loader verifies before it replays.** A fixture whose redaction
//     marker names an unknown redactor, whose digest does not match its body,
//     whose record count disagrees with its header, whose records are stamped
//     backwards, or whose records carry an address is refused by name -- so a
//     file edited after it was written cannot reach a screen.
//   - **the replay is the recording.** The recorded gaps are the replayed gaps
//     (160 s stays 160 s), the documents go through `FeedTracker`, and the
//     notices arrive where they arrived. A replay that fired its records
//     back-to-back passes nothing here, which is the point: the eight cases the
//     design asks the owner to record are *about* time.
//   - **the committed fixture is the code path's output.** The synthetic
//     fixture in `test/fixtures/` is regenerated from `test/unit/synthetic.ts`
//     and compared byte for byte, so a hand-edited fixture -- even one whose
//     digest was recomputed -- fails, and the file cannot drift from the
//     documents it claims to be made of.
//
// The fake clock below is the whole reason this file can be fast: `Timers` is an
// argument (`src/main/spawn.ts`), so a replay's schedule is observed instead of
// waited for, and the *recorded* delays are asserted directly.

import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { FeedTracker } from '../../src/main/spawn'
import type { Notice, Timers } from '../../src/main/spawn'
import { MAX_LIVE_FEEDS } from '../../src/source/cli'
import {
  FIXTURE_CASES,
  FIXTURE_DIRECTORY,
  FIXTURE_FORMAT_VERSION,
  FIXTURE_HEADER_KEY,
  FixtureRefusal,
  FixtureSource,
  MAX_REPLAY_FEEDS,
  SYNTHETIC_NOTE,
  loadFixture,
  writeFixture,
} from '../../src/source/fixture'
import type { FixtureRecord, LoadedFixture } from '../../src/source/fixture'
import { REDACTOR_VERSION } from '../../src/source/redact'
import type { Snapshot } from '../../src/source/source'
import { SYNTHETIC_LITERALS, SYNTHETIC_META, SYNTHETIC_SOURCE, SYNTHETIC_WHERE, syntheticFixture } from './synthetic'

/** theSource is the committed fixture's text, read from disk. */
function theSource(): string {
  return readFileSync(SYNTHETIC_WHERE, 'utf8')
}

/** loaded is the committed fixture, verified. */
function loaded(): LoadedFixture {
  return loadFixture(theSource(), SYNTHETIC_WHERE)
}

/**
 * FakeTimers is a `Timers` a test drives: it records what was scheduled and
 * when, and `advanceTo` runs everything due, in order. It is what lets the tests
 * below assert a *recorded* delay rather than wait for it.
 */
class FakeTimers implements Timers {
  #now = 0
  #next = 1
  readonly #due: { id: number; at: number; handler: () => void }[] = []

  get scheduled(): readonly number[] {
    return this.#due.map((timer) => timer.at)
  }

  setTimeout(handler: () => void, ms: number): ReturnType<typeof setTimeout> {
    const id = this.#next
    this.#next += 1
    this.#due.push({ id, at: this.#now + ms, handler })
    return id as unknown as ReturnType<typeof setTimeout>
  }

  clearTimeout(handle: ReturnType<typeof setTimeout>): void {
    const id = handle as unknown as number
    const index = this.#due.findIndex((timer) => timer.id === id)
    if (index >= 0) this.#due.splice(index, 1)
  }

  /** advanceTo runs every timer due at or before `at`, earliest first. */
  advanceTo(at: number): void {
    for (;;) {
      const index = this.#due.findIndex((timer) => timer.at <= at)
      if (index < 0) break
      const [timer] = this.#due.splice(index, 1)
      if (timer === undefined) break
      this.#now = timer.at
      timer.handler()
    }
    this.#now = Math.max(this.#now, at)
  }
}

/** sourceOver builds a `FixtureSource` over a text a test wrote. */
function sourceOver(text: string, timers: Timers, where = 'a test fixture'): FixtureSource {
  return new FixtureSource({ fixture: loadFixture(text, where), timers })
}

/** collected is the notices and snapshots a feed delivered, plus its end. */
interface Collected {
  readonly snapshots: Snapshot[]
  readonly notices: Notice[]
  readonly ended: { reason: string; sentence: string | null; snapshots: number }[]
}

function collect(feed: ReturnType<FixtureSource['subscribeLive']>): Collected {
  const snapshots: Snapshot[] = []
  const notices: Notice[] = []
  const ended: Collected['ended'] = []
  feed.onChange((snapshot) => snapshots.push(snapshot))
  feed.onNotice((notice) => notices.push(notice))
  feed.onEnded((end) => ended.push({ reason: end.reason, sentence: end.sentence, snapshots: end.snapshots }))
  return { snapshots, notices, ended }
}

describe('the fixture format and its loader', () => {
  it('refuses a file that is not a fixture at all', () => {
    for (const text of [
      '',
      'not json\n',
      '[]\n',
      '{"runs":[]}\n',
      '{"fugaro_fixture":{}}\n',
    ]) {
      expect(() => loadFixture(text, 'x.ndjson')).toThrow(FixtureRefusal)
    }
  })

  it('refuses a fixture whose redaction marker this build does not know', () => {
    const text = theSource()
    const withoutMarker = text.replace(JSON.stringify(REDACTOR_VERSION), '"fugaro-console-redact/0"')
    expect(() => loadFixture(withoutMarker)).toThrow(/does not know|redacted by/)
    // And a header that names no redactor at all is refused too: a fixture is
    // only as safe as the rules that made it, and a header must name them.
    const header = JSON.parse(text.slice(0, text.indexOf('\n'))) as Record<string, unknown>
    const inner = header[FIXTURE_HEADER_KEY] as Record<string, unknown>
    delete inner['redactor']
    const rebuilt = `${JSON.stringify(header)}\n${text.slice(text.indexOf('\n') + 1)}`
    expect(() => loadFixture(rebuilt)).toThrow(/names no redactor/)
  })

  it('refuses a fixture whose hash does not verify', () => {
    const text = theSource()
    const body = text.slice(text.indexOf('\n') + 1)
    // A body edited after the fact: the digest no longer describes it.
    const edited = text.replace(body, body.replace('"observe"', '"enforce"'))
    expect(edited).not.toBe(text)
    expect(() => loadFixture(edited)).toThrow(/does not verify/)
    // A header whose digest was recomputed by hand still has to survive the
    // audit and the count: this one claims a record that is not there.
    const lines = text.trimEnd().split('\n')
    const head = JSON.parse(lines[0] ?? '') as Record<string, unknown>
    const fields = head[FIXTURE_HEADER_KEY] as Record<string, unknown>
    fields['records'] = 999
    expect(() => loadFixture(`${JSON.stringify(head)}\n${lines.slice(1).join('\n')}\n`)).toThrow(
      /records; its header says/,
    )
  })

  it('refuses a fixture whose records are in the wrong order, or malformed', () => {
    const records: FixtureRecord[] = [
      { at: '2026-10-15T12:00:05Z', document: { day: '2026-10-15' } },
      { at: '2026-10-15T12:00:00Z', document: { day: '2026-10-15' } },
    ]
    const text = writeFixture(records, SYNTHETIC_META)
    expect(() => loadFixture(text)).toThrow(/before record 1/)
    // A record with neither shape, and one with both: the writer is handed a
    // deliberately broken record here, which is the only way to build one.
    const neither = writeFixture([{ at: '2026-10-15T12:00:00Z' } as unknown as FixtureRecord], SYNTHETIC_META)
    expect(() => loadFixture(neither)).toThrow(/exactly one of/)
    const both = writeFixture(
      [{ at: '2026-10-15T12:00:00Z', document: { a: 1 }, notice: 'x' } as unknown as FixtureRecord],
      SYNTHETIC_META,
    )
    expect(() => loadFixture(both)).toThrow(/exactly one of/)
    const noStamp = writeFixture([{ document: { a: 1 } } as unknown as FixtureRecord], SYNTHETIC_META)
    expect(() => loadFixture(noStamp)).toThrow(/arrival stamp/)
  })

  it('refuses a fixture whose records carry what the redactor removes', () => {
    // The digest can be right and the contents still wrong: this is the audit
    // doing work a hash cannot, and it is why a fixture is verified twice.
    const text = writeFixture(
      [
        { at: '2026-10-15T12:00:00Z', document: { runs: [{ title: 'ask ana@example.com' }] } },
        { at: '2026-10-15T12:00:01Z', notice: 'reading /Users/ana/src/fugaro' },
        { at: '2026-10-15T12:00:02Z', document: { project: 'fugaro' } },
      ],
      SYNTHETIC_META,
    )
    expect(() => loadFixture(text, 'planted.ndjson')).toThrow(/still holds an address/)
    const notice = writeFixture([{ at: '2026-10-15T12:00:00Z', notice: 'at /Users/ana' }], SYNTHETIC_META)
    expect(() => loadFixture(notice)).toThrow(/record 1 still holds a filesystem path/)
    const project = writeFixture(
      [{ at: '2026-10-15T12:00:00Z', document: { project: 'fugaro' } }],
      SYNTHETIC_META,
    )
    expect(() => loadFixture(project)).toThrow(/not this class's marker/)
  })

  it('refuses a header that carries what the redactor removes', () => {
    // **The digest covers the body, not the header**, so a header edited after
    // the recorder wrote it passes every check above this one -- and it is the
    // half a reader sees: `FixtureSource` hands a screen the header's `project`
    // as the installation's name. A header carrying identity means the file was
    // edited by hand, which is exactly what the audit is here to catch.
    const records: FixtureRecord[] = [{ at: '2026-10-15T12:00:00Z', document: { day: '2026-10-15' } }]
    const addressed = writeFixture(records, { ...SYNTHETIC_META, project: 'ana@example.com' })
    expect(() => loadFixture(addressed, 'edited.ndjson')).toThrow(
      /edited\.ndjson's header at project still holds an address/,
    )
    const pathy = writeFixture(records, { ...SYNTHETIC_META, note: 'a first look at /Users/ana/src/fugaro' })
    expect(() => loadFixture(pathy)).toThrow(/header at note still holds a filesystem path/)
    // A key in the header is audited too, on the same rules as a document's.
    // `writeFixture` writes only the fields it knows, so this one is hand-made:
    // which is the whole threat, a file edited after the recorder wrote it.
    const keyed = writeFixture(records, SYNTHETIC_META).replace('"synthetic":', '"acme/secret-repo":1,"synthetic":')
    expect(() => loadFixture(keyed)).toThrow(/is a key that carries a repository path/)
    // And the header this repository commits, written by the same redaction
    // path, is clean -- otherwise the fixture above would be a refusal.
    expect(() => { loadFixture(theSource(), SYNTHETIC_WHERE) }).not.toThrow()
  })

  it('verifies the committed synthetic fixture, and says it is synthetic', () => {
    const fixture = loaded()
    expect(fixture.header.version).toBe(FIXTURE_FORMAT_VERSION)
    expect(fixture.header.redactor).toBe(REDACTOR_VERSION)
    expect(fixture.header.synthetic).toBe(true)
    expect(fixture.header.note).toBe(SYNTHETIC_NOTE)
    expect(fixture.header.case).toBe('synthetic')
    expect(fixture.header.recorded).toBeUndefined()
    expect(fixture.records).toHaveLength(SYNTHETIC_SOURCE.length)
    // The eight recordings the design asks for are names here, and the README
    // says which are still owner-run. None of them is this file's case.
    expect(FIXTURE_CASES).toHaveLength(8)
    expect(FIXTURE_CASES).not.toContain('synthetic')
    // ...nor Task 3.1's degraded fixture, which is written here for the same
    // reason: a list that mixed the written fixtures with the recordings would
    // leave a reader working out which name was real.
    expect(FIXTURE_CASES).not.toContain('degraded')
  })

  it('quotes the digest the committed fixture actually carries (README drift)', () => {
    // The review of Task 3.2 found the README quoting `2c8a636f…` while the
    // committed header said `24cdc5f8…`: the fixture was rewritten by a later
    // task and the sentence quoting it was not. A reader who "fixed" the fixture
    // to match the README would break this file's byte-identity test, so the two
    // are held together here -- the README's own hex, against the header's.
    const readme = readFileSync(join(__dirname, '..', '..', 'README.md'), 'utf8')
    const quoted = /"sha256":"([0-9a-f]{64})"/.exec(readme)?.[1]
    expect(quoted).toBe(loaded().header.sha256)
    expect(readme).toContain(`"case":"${loaded().header.case}"`)
  })

  it('is exactly what the redaction path writes from the documents in source', () => {
    // Regenerated rather than trusted: a fixture edited by hand -- even one whose
    // sha256 was recomputed -- differs from this, and a fixture that stopped
    // matching the documents it claims to be made of is a fixture nobody can
    // reason about.
    expect(theSource()).toBe(syntheticFixture())
    expect(syntheticFixture()).toContain('<project>')
  })

  it('keeps the replay ceiling the live source uses', () => {
    expect(MAX_REPLAY_FEEDS).toBe(MAX_LIVE_FEEDS)
  })
})

describe('the replaying source', () => {
  it('replays the recording on the recording\'s own clock', () => {
    const timers = new FakeTimers()
    const source = sourceOver(theSource(), timers)
    const feed = source.subscribeLive({})
    collect(feed)
    // The recording's own offsets: 0 s, 5 s, 8 s, 100 s, 260 s, 261 s.
    expect(timers.scheduled).toEqual([0, 5_000, 8_000, 100_000, 260_000, 261_000])
  })

  it('delivers the same frames a live child would, gap and notices included', async () => {
    const timers = new FakeTimers()
    const source = sourceOver(theSource(), timers)
    const feed = source.subscribeLive({})
    const seen = collect(feed)

    expect(feed.latest).toBeNull()
    const first = feed.current()
    timers.advanceTo(0)
    const snapshot = await first
    expect(snapshot.sequence).toBe(1)
    expect(snapshot.receivedAt).toBe('2026-10-15T12:00:00Z')
    expect(snapshot.day).toBe('2026-10-15')
    expect(snapshot.rolled).toBe(false)

    // The degraded mode's sentence arrives between two documents, and travels
    // with the *next* snapshot -- which is what a screen's header warning needs.
    timers.advanceTo(8_000)
    expect(seen.notices.map((notice) => notice.text)).toEqual(['no budget backend: showing run records only'])
    expect(seen.snapshots).toHaveLength(2)

    // The recorded gap stays a gap: at 100 s no third document has arrived...
    timers.advanceTo(100_000)
    expect(seen.snapshots).toHaveLength(3)
    expect(seen.snapshots[2]?.notices).toHaveLength(1)
    // ...and the 160 s of nothing after it is 160 s of nothing.
    timers.advanceTo(259_000)
    expect(seen.snapshots).toHaveLength(3)
    timers.advanceTo(261_000)
    expect(seen.snapshots).toHaveLength(5)
    expect(seen.snapshots[4]?.document).toMatchObject({
      connection: { state: 'live' },
      repos: [{ percent: 100 }],
    })
    // The feed ends where the recording ends, and says how much it delivered.
    expect(seen.ended).toEqual([{ reason: 'stopped', sentence: null, snapshots: 5 }])
    expect(feed.latest?.sequence).toBe(5)
  })

  it('agrees with FeedTracker, which is what a real feed goes through', () => {
    const fixture = loaded()
    const tracker = new FeedTracker()
    const expected = fixture.records.flatMap((record) =>
      'document' in record ? [tracker.push(record.document, record.at)] : [],
    )
    const timers = new FakeTimers()
    const seen = collect(sourceOver(theSource(), timers).subscribeLive({}))
    timers.advanceTo(Number.MAX_SAFE_INTEGER)
    expect(seen.snapshots.map((snapshot) => [snapshot.sequence, snapshot.receivedAt, snapshot.day, snapshot.rolled])).toEqual(
      expected.map((frame) => [frame.sequence, frame.receivedAt, frame.day, frame.rolled]),
    )
    expect(seen.snapshots).toHaveLength(5)
  })

  it('answers a second subscription with the feed it has, and no second replay', () => {
    const timers = new FakeTimers()
    const source = sourceOver(theSource(), timers)
    const first = source.subscribeLive({})
    const scheduledOnce = timers.scheduled.length
    expect(source.subscribeLive({})).toBe(first)
    expect(timers.scheduled).toHaveLength(scheduledOnce)
    // A different argument set is a different subscription, as in `cli.ts`.
    expect(source.subscribeLive({ repo: 'r-1a2b3c4d/r-5e6f7a8b' })).not.toBe(first)
    // A feed that has ended is not live: the next call replays from the start.
    timers.advanceTo(Number.MAX_SAFE_INTEGER)
    expect(source.subscribeLive({})).not.toBe(first)
  })

  it('stops a replay in the middle, and answers current() honestly', async () => {
    const timers = new FakeTimers()
    const source = sourceOver(theSource(), timers)
    const feed = source.subscribeLive({})
    const seen = collect(feed)
    await feed.stop()
    expect(seen.ended).toEqual([{ reason: 'stopped', sentence: null, snapshots: 0 }])
    expect(seen.snapshots).toHaveLength(0)
    await expect(feed.current()).rejects.toThrow('the recording ended before its first document')
    // A stopped feed delivers nothing more, even if its timers were left behind.
    timers.advanceTo(Number.MAX_SAFE_INTEGER)
    expect(seen.snapshots).toHaveLength(0)
  })

  it('refuses a one-shot read the recording does not hold, and answers the ones it does', async () => {
    const fixture = loaded()
    const source = new FixtureSource({
      fixture,
      documents: { 'budget.show': { day: '2026-10-15', mode: 'observe' } },
      texts: { 'cli.version': 'fugaro 0.6.0-fixture' },
    })
    const budget = await source.showBudget({})
    expect(budget.document['mode']).toBe('observe')
    expect(budget.sequence).toBe(1)
    expect(budget.receivedAt).toBe('2026-10-15T12:04:21Z')
    // A missing read is refused by name rather than answered with the live
    // feed's last document, which would be another command's answer.
    await expect(source.listRuns({})).rejects.toThrow(/holds no runs.list document/)
    await expect(source.runLogLink({ run: '2026-10-15-aa01' })).rejects.toThrow(/holds no run.logLink text/)
    expect((await source.version()).text).toBe('fugaro 0.6.0-fixture')
    // The installation a fixture draws names the recording, not a machine: the
    // header's own project, and no path anywhere.
    expect(source.installation.project).toBe('<project>')
    expect(source.installation.binaryPath).toBe('(fixture)')
    expect(JSON.stringify(source.installation)).not.toContain('/Users/')
  })

  it('closes every feed it started', async () => {
    const timers = new FakeTimers()
    const source = sourceOver(theSource(), timers)
    const feed = source.subscribeLive({})
    const seen = collect(feed)
    await source.close()
    expect(seen.ended).toHaveLength(1)
  })
})

describe('the writer serialises with a stack, and says the same bytes', () => {
  /** written is one record as a fixture line, without the header: what the
   * writer produced for a document, which is where a serialiser bug would show
   * up as a changed file rather than as an error. */
  function written(value: unknown): string {
    // The cast is the test's point: `FixtureRecord` says a document is an
    // object, and these cases deliberately are not, because the serialiser is
    // what decides what a non-object becomes.
    const document = value as Readonly<Record<string, unknown>>
    const line = writeFixture([{ at: '2026-10-15T12:00:00Z', document }], SYNTHETIC_META)
    const newline = line.indexOf('\n')
    return line.slice(newline + 1).trimEnd()
  }

  it('produces byte for byte what `JSON.stringify` produces', () => {
    // The writer's own contract: it is `JSON.stringify` with an explicit stack
    // and nothing else. The cases below are the ones a hand-written serialiser
    // gets wrong -- an empty container, a value that drops out of an object, a
    // value that becomes `null` in an array, a non-finite number, a string that
    // needs escaping, and `toJSON`.
    const cases: readonly unknown[] = [
      null,
      true,
      0,
      -0,
      1.5,
      'plain',
      'quote " backslash \ newline \n tab \t',
      [],
      {},
      [1, 'a', true, null],
      { a: 1, b: 'two', c: false, d: null },
      { nested: { deeper: [{ x: [] }, {}] } },
      { gone: undefined, kept: 1 },
      [undefined, 1],
      { big: 1e21, small: 1e-7, neg: -12.5 },
      { stamped: new Date('2026-10-15T12:00:00Z') },
      { nestedJson: { toJSON: () => ({ a: 1, b: [2] }) } },
      { emptyJson: { toJSON: () => undefined }, after: 1 },
      { deep: { a: { b: { c: { d: [1, [2, [3]]] } } } } },
    ]
    for (const value of cases) {
      expect(written(value), JSON.stringify(value) ?? 'undefined').toBe(
        JSON.stringify({ at: '2026-10-15T12:00:00Z', document: value }),
      )
    }
  })

  it('serialises a document nested ten thousand deep instead of crashing', () => {
    // The hostiles-are-refusals-not-crashes rule, at the writer: V8's recursive
    // serialiser throws `RangeError: Maximum call stack size exceeded` around
    // four thousand levels, and a writer that dies there turns a document the
    // redactor and the loader both refuse into a lost process. The line is
    // written, and the *loader* is what refuses it -- by name.
    const depth = 10_000
    const deep = JSON.parse(`{"a":${'['.repeat(depth)}${']'.repeat(depth)}}`) as Readonly<Record<string, unknown>>
    const text = writeFixture([{ at: '2026-10-15T12:00:00Z', document: deep }], SYNTHETIC_META)
    expect(() => loadFixture(text, 'deep.ndjson')).toThrow(FixtureRefusal)
    // And the same document at an ordinary depth writes and loads as usual.
    const shallow = JSON.parse(`{"a":${'['.repeat(100)}${']'.repeat(100)}}`) as Readonly<Record<string, unknown>>
    const shallowText = writeFixture([{ at: '2026-10-15T12:00:00Z', document: shallow }], SYNTHETIC_META)
    expect(loadFixture(shallowText, 'shallow.ndjson').records).toHaveLength(1)
  })
})

describe('the committed synthetic fixture', () => {
  it("is redaction-clean, so the loader's audit is not doing the heavy lifting", () => {
    const text = theSource()
    expect(text).not.toContain('fixture-sandbox')
    expect(text).not.toContain('fixture-owner')
    expect(text).not.toContain('example.invalid/pull')
    expect(text).toContain('<project>')
    expect(text).toContain('<branch>')
    expect(text).toContain('[redacted-url]')
    expect(text).toContain(SYNTHETIC_NOTE)
  })

  it('loads every file in test/fixtures/, and refuses one that does not verify', () => {
    // **This is the directory the plan's eight OWNER-RUN recordings land in, and
    // the test above it checks only *names*.** A recording committed with a
    // planted address, a stale digest or a wrong record count would then replay
    // into a screen with a green suite -- the suite would be asserting the shape
    // of a directory rather than the contents of the files the owner adds.
    // So every fixture here goes through the loader a screen uses, which
    // verifies the redactor marker, the digest, the count, the record order and
    // the redaction audit, and refuses by name when any of them is wrong.
    const files = readdirSync(FIXTURE_DIRECTORY).filter((name) => name.endsWith('.ndjson'))
    expect(files).toContain('synthetic.ndjson')
    for (const name of files) {
      const path = join(FIXTURE_DIRECTORY, name)
      const text = readFileSync(path, 'utf8')
      // The throw carries the path and the reason (the loader's `where`), so a
      // failure here reads as which file and which check.
      expect(() => loadFixture(text, path)).not.toThrow()
    }
  })

  it('holds exactly the synthetic fixture plus, at most, one file per recorded case', () => {
    // The design's eight recordings are owner-run and do not exist yet, so this
    // directory holds the synthetic one and nothing else *today*. But the
    // owner's recordings land here next -- one file per case, named for the
    // case (`normal.ndjson`, `silent-then-lost.ndjson`, ...) -- so this test
    // must not be the thing that refuses them: an agent-written test that fails
    // on the owner's own follow-up work is a test that would have to be edited
    // before the plan's recordings could be committed. What it asserts instead
    // is what may be here at all: every file is either the synthetic fixture or
    // a case `FIXTURE_CASES` names. A stray file -- a hand-made copy, a
    // recording under a name no case holds -- fails it by name.
    // `degraded.ndjson` is the second agent-written fixture, and it is not one
    // of the eight cases above: the design's eight are *recordings of a live
    // installation*, and the degraded mode is a project with no budget backend
    // at all -- a state the sandbox every recording is made against cannot be
    // put into without changing what the installation is. Task 3.1 writes it
    // from invented documents instead (`console/src/renderer/screens/degraded.ts`
    // draws what the CLI's degraded shape carries, and `degraded.spec.ts` reads
    // this file), so it is named here rather than added to `FIXTURE_CASES`,
    // which is the list of the *recorder's* cases.
    // `bin` is not a case and not a recording: it is the directory holding the
    // stub `fugaro` the e2e specs install (`test/fixtures/bin/fugaro`, Task 3.3),
    // which lives under `test/fixtures/` because that is the directory of things
    // the specs read from disk. It is a directory, so the `.ndjson` sweep above
    // never looks inside it, and the stub itself is asserted where it is used
    // (`test/e2e/startup-skew.spec.ts`) rather than here.
    const allowed = new Set([
      'synthetic.ndjson',
      'degraded.ndjson',
      'bin',
      // `crash-init` is the other directory of things a spec reads from disk: the
      // Electron app `test/e2e/crash-init.spec.ts` launches, whose main process
      // drives the console's built crash wiring against the spec's loopback sink
      // (Task 2.1b fix round B). Like the stub under `bin/`, it is not a
      // recording and not a case: the `.ndjson` sweep above never looks inside
      // it, and what it is asserted at is the spec that runs it.
      'crash-init',
      // The `.license` siblings carry the two-line SPDX notice for the types
      // that have no comment syntax of their own (`.ndjson` and `.json`;
      // `internal/eeboundary`'s notice rule, counsel's text of 2026-10-27).
      // They are not fixtures and no case reads one: the `.ndjson` sweep above
      // matches on the `.ndjson` suffix and never sees one. The stub under
      // `bin/` carries its notice as a comment instead, because a shell script
      // has one.
      'synthetic.ndjson.license',
      'degraded.ndjson.license',
      'plan-status.json.license',
      'coordinator-status.json.license',
      ...FIXTURE_CASES.map((name) => `${name}.ndjson.license`),
      // `screenshots` is the owner-run screenshot tool's data
      // (`test/fixtures/screenshots/`, ruling of 2026-10-20): a directory of
      // documents an e2e-shaped run feeds the built console, so the `.ndjson`
      // sweep above never looks inside it and this is where it is named. Its
      // own README says what is in it and `test/unit/screenshots.test.ts` holds
      // every document in it to the console's decoders.
      'screenshots',
      // `plan-status.json` is not a recording either (plan GV2.1): it is one
      // `plan status --json` document, written from the structs
      // (`internal/cli/plan_status.go`, `internal/plan/state.go`) so the plan
      // graph's decoder and its string-leaf walk have a document to run over.
      // It is JSON and not `.ndjson`, so the loader sweep above never reads it,
      // and it carries no redactor header to verify -- it is invented rather
      // than recorded, and `test/unit/schema.test.ts` is where its keys are
      // held to the Go structs.
      'plan-status.json',
      // `coordinator-status.json` is the same kind of thing one document over
      // (Bitbucket credentials T1.2, D364): one `coordinator status
      // --from-store --all --json` document, invented from the structs
      // (`ee/coordinator/state.go`, `ee/coordinator/cli/coordinator_view.go`)
      // rather than recorded, because the two states the project view turns on
      // -- a repository the sweep never reconciled, and a stale marker -- are in
      // no recording this tree carries. The e2e stub answers that read with it
      // (`test/support/cliStub.ts`) and `test/unit/cli-stub.test.ts` holds it to
      // the console's own decoder.
      'coordinator-status.json',
      ...FIXTURE_CASES.map((name) => `${name}.ndjson`),
    ])
    const present = readdirSync(FIXTURE_DIRECTORY)
    expect(present.filter((name) => !allowed.has(name))).toEqual([])
    expect(present).toContain('synthetic.ndjson')
    // No assertion follows about the cases that are *missing*: the gap is
    // visible rather than implied (the directory shows which cases have no
    // file, and `console/README.md` names them and why), and a test that failed
    // while the gap was open would fail on the owner's own recordings' arrival —
    // an agent-written test that has to be deleted before the plan's follow-up
    // work can land.
  })
})
