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
    const allowed = new Set(['synthetic.ndjson', ...FIXTURE_CASES.map((name) => `${name}.ndjson`)])
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
