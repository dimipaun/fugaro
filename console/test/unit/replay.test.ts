// The replay buffer: its two bounds, its scrub, and its export
// (desktop-console plan Task 2.3; design section 9.2).
//
// The design asks for "a bounded in-memory ring of the last N minutes,
// scrubbable, exportable to a file", and the plan's test list names three
// properties, each of which is a mutation the code below must not survive:
//
//   - **the ring's bound is enforced.** Both bounds are: the window (a record
//     older than N minutes behind the newest arrival is gone) and the hard cap
//     (a burst faster than the contract's one-document-per-second floor cannot
//     grow the buffer past `REPLAY_MAX_RECORDS`). A ring that only pruned by
//     time grows without limit on a fast feed; one that only pruned by count
//     keeps yesterday's documents. `dropped` counts what left, so a screen can
//     say the window is not the whole story rather than implying it is.
//   - **an export round-trips through the fixture source.** The bytes
//     `ReplayBuffer.export()` writes **are** the fixture format's body
//     (design section 9.2: "the replay buffer's export doubles as the fixture
//     format for tests"), so the round trip is asserted twice: byte for byte
//     against the committed fixture's own body, and then by loading the export
//     with `loadFixture` and replaying it through a `FixtureSource`, which is
//     the reader that decides whether a file is a fixture at all.
//   - **scrubbing to a moment reproduces that moment's view.** `scrub` returns
//     the record *in effect* at a moment -- the newest one at or before it --
//     and the view built from the frame it returns is the view the console drew
//     when that document arrived, which is asserted by building both models and
//     comparing them, and by showing the model one tick later is a *different*
//     one (a `scrub` that always returned the newest frame would pass the first
//     half of that and fail the second).
//
// It is `shared/replay.ts` that is under test rather than anything in
// `src/main/`: the buffer is the renderer's (it holds what the screen is
// showing), so it may not read a file, may not spawn, and may import no Node
// builtin -- which is why the fixture *writer* is not called from the module
// and why the test is what hands its output to `writeFixture`.

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import { buildLiveModel } from '../../src/renderer/screens/live'
import { decodeSnapshot } from '../../src/shared/schema'
import { REPLAY_MAX_BYTES, REPLAY_MAX_RECORDS, REPLAY_WINDOW_SECONDS, ReplayBuffer } from '../../src/shared/replay'
import type { ReplayRecord } from '../../src/shared/replay'
import { FixtureSource, loadFixture, writeFixture } from '../../src/source/fixture'
import type { FixtureMeta, LoadedFixture } from '../../src/source/fixture'
import type { Notice, Timers } from '../../src/main/spawn'
import type { JSONDoc, Snapshot } from '../../src/source/source'
import { SYNTHETIC_WHERE } from './synthetic'

/** theFixture is the committed synthetic fixture, verified by the real loader. */
function theFixture(): LoadedFixture {
  return loadFixture(readFileSync(SYNTHETIC_WHERE, 'utf8'), SYNTHETIC_WHERE)
}

/** bodyOf is a fixture file's body: every byte after the header's newline, with
 * the writer's final newline taken off -- which is exactly what `export()`
 * claims to be. */
function bodyOf(text: string): string {
  const newline = text.indexOf('\n')
  expect(newline).toBeGreaterThan(-1)
  const body = text.slice(newline + 1)
  return body.endsWith('\n') ? body.slice(0, -1) : body
}

/** noticeOf reads a record's notice text, or refuses to -- a test helper that
 * would rather fail than be handed the wrong half of the union. */
function noticeOf(record: ReplayRecord): string {
  if (!('notice' in record)) throw new Error('this record is a document, not a notice')
  return record.notice
}

/** documentOf is `noticeOf`'s other half. */
function documentOf(record: ReplayRecord): JSONDoc {
  if (!('document' in record)) throw new Error('this record is a notice, not a document')
  return record.document
}

/**
 * arrivals turns a recording into the snapshots a live feed would have handed a
 * screen: one per document, in order, with the notices that preceded it attached
 * -- which is the shape `FeedTracker` produces from the same lines
 * (`src/main/spawn.ts`), and the reason the round trip below can be byte-exact.
 */
function arrivals(fixture: LoadedFixture): readonly Snapshot[] {
  const out: Snapshot[] = []
  let pending: Notice[] = []
  let sequence = 0
  for (const record of fixture.records) {
    if ('notice' in record) {
      pending = [...pending, { text: record.notice, receivedAt: record.at }]
      continue
    }
    sequence += 1
    out.push({
      document: record.document,
      receivedAt: record.at,
      sequence,
      day: null,
      rolled: false,
      notices: pending,
    })
    pending = []
  }
  return out
}

/** anArrival is one snapshot of a synthetic feed, for the tests that need a
 * series of their own rather than the committed one. */
function anArrival(at: string, value: number, sequence = 1): Snapshot {
  return {
    document: { project: '<project>', day: '2026-10-15', counted_usd: value },
    receivedAt: at,
    sequence,
    day: '2026-10-15',
    rolled: false,
    notices: [],
  }
}

/** stamp is an arrival stamp `seconds` after a base, in the console's own shape. */
function stamp(base: string, seconds: number): string {
  return new Date(Date.parse(base) + seconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z')
}

const BASE = '2026-10-15T12:00:00Z'

/** FakeTimers is a `Timers` a test drives, as in `fixture.test.ts`: a replay is
 * observed rather than waited for. */
class FakeTimers implements Timers {
  #now = 0
  #next = 1
  readonly #due: { id: number; at: number; handler: () => void }[] = []

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

  /** advanceTo runs every timer due at or before `at`, earliest first, and is
   * the same clock `fixture.test.ts` drives a replay with. */
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

describe('the replay buffer', () => {
  it('bounds the ring by a window and a cap that covers it', () => {
    // Fifteen minutes is the design's "last N minutes" at the scale a run is
    // watched at; the cap is the contract's one-document-per-second floor
    // (section 10.1) over that window, so a feed at the floor is never cut short
    // by the cap and a faster one cannot grow past it.
    expect(REPLAY_WINDOW_SECONDS).toBe(15 * 60)
    expect(REPLAY_MAX_RECORDS).toBe(REPLAY_WINDOW_SECONDS)
    const bounds = new ReplayBuffer().bounds()
    expect(bounds.windowSeconds).toBe(REPLAY_WINDOW_SECONDS)
    expect(bounds.maxRecords).toBe(REPLAY_MAX_RECORDS)
    expect(bounds.from).toBeNull()
    expect(bounds.to).toBeNull()
    expect(bounds.records).toBe(0)
    expect(bounds.dropped).toBe(0)
  })

  it('forgets a record older than the window behind the newest arrival', () => {
    const buffer = new ReplayBuffer()
    // Forty minutes at one document a minute: half of it is outside the window,
    // and the window is measured from the newest arrival, not from a wall clock
    // the buffer would have to read.
    for (let minute = 0; minute < 40; minute += 1) {
      buffer.observe(anArrival(stamp(BASE, minute * 60), minute, minute + 1))
    }
    const bounds = buffer.bounds()
    expect(bounds.to).toBe(stamp(BASE, 39 * 60))
    expect(bounds.from).toBe(stamp(BASE, 24 * 60))
    expect(bounds.records).toBe(16)
    expect(bounds.dropped).toBe(24)
    expect(buffer.records()[0]?.at).toBe(stamp(BASE, 24 * 60))
    expect(buffer.records().at(-1)?.at).toBe(bounds.to)
  })

  it('drops the oldest arrivals when the cap binds, whatever the window says', () => {
    // The window is an hour and the cap is eight: a burst at one second apart
    // is inside the window the whole way, so only the cap can be what prunes.
    const buffer = new ReplayBuffer({ windowSeconds: 3600, maxRecords: 8 })
    for (let second = 0; second < 20; second += 1) {
      buffer.observe(anArrival(stamp(BASE, second), second, second + 1))
    }
    const bounds = buffer.bounds()
    expect(bounds.records).toBe(8)
    expect(bounds.dropped).toBe(12)
    expect(bounds.from).toBe(stamp(BASE, 12))
    expect(buffer.records().map((record) => record.at)).toEqual(
      Array.from({ length: 8 }, (_, index) => stamp(BASE, 12 + index)),
    )
  })

  it('bounds the ring by bytes as well as by records', () => {
    // The record cap alone lets 900 documents of 4 MiB each sit in a renderer.
    // A third bound, on the serialized size of what is held, is what stops it.
    expect(REPLAY_MAX_BYTES).toBe(64 * 1024 * 1024)
    expect(new ReplayBuffer().bounds().maxBytes).toBe(REPLAY_MAX_BYTES)
    // The padding is multi-byte on purpose (review round 5 of PR 478). The
    // module counts UTF-8 bytes with its own `utf8Length` -- it loads in the
    // renderer as well as in Node, so it cannot use `Buffer` -- and an ASCII
    // padding of `'x'.repeat(1000)` could not tell that count apart from
    // `String.length`, which made the assertions below hold for a
    // character-counting implementation. Two bytes for the accent, and an
    // astral character, which is a surrogate pair: the branch a naive count gets
    // wrong twice over.
    const padding = 'é'.repeat(400) + '𝄞'
    expect(Buffer.byteLength(padding)).toBeGreaterThan(padding.length)
    const sized = (at: string, value: number) => ({ ...anArrival(at, value), document: { padding, counted_usd: value } })
    const one = new ReplayBuffer({ windowSeconds: 3600 })
    one.observe(sized(stamp(BASE, 0), 0))
    const size = one.bounds().bytes
    // The real encoding of the line, not the buffer's own arithmetic: this is
    // where a size counted in characters fails, and where a surrogate pair
    // counted as two three-byte characters (six, not four) fails.
    expect(size).toBe(Buffer.byteLength(one.export()))
    expect(size).toBeGreaterThan(one.export().length)
    // Room for three and a half records: the fourth arrival pushes out the oldest.
    const buffer = new ReplayBuffer({ windowSeconds: 3600, maxRecords: 100, maxBytes: Math.floor(size * 3.5) })
    for (let second = 0; second < 10; second += 1) buffer.observe(sized(stamp(BASE, second), second))
    const bounds = buffer.bounds()
    expect(bounds.records).toBe(3)
    expect(bounds.dropped).toBe(7)
    expect(bounds.bytes).toBeLessThanOrEqual(bounds.maxBytes)
    // The held lines' own sizes, made from the public export rather than from
    // the buffer's running total: a `#dropOldest` that shifted a line without
    // subtracting its size would leave the two disagreeing, and a byte per line
    // that dropped or double-counted the join's newline would too.
    const lines = buffer.export().split('\n')
    expect(lines.length).toBe(bounds.records)
    expect(bounds.bytes).toBe(lines.reduce((total, line) => total + Buffer.byteLength(line), 0))
    expect(buffer.records().map((record) => record.at)).toEqual([7, 8, 9].map((s) => stamp(BASE, s)))
  })

  it('keeps no record that is larger than the whole byte bound, and counts it', () => {
    const buffer = new ReplayBuffer({ maxBytes: 200 })
    buffer.observe(anArrival(stamp(BASE, 0), 1))
    buffer.observe({ ...anArrival(stamp(BASE, 1), 2), document: { padding: 'x'.repeat(500) } })
    expect(buffer.bounds().records).toBe(1)
    expect(buffer.bounds().dropped).toBe(1)
    expect(buffer.records()[0]?.at).toBe(stamp(BASE, 0))
  })

  it('files a late arrival where its stamp belongs, so the export stays loadable', () => {
    const buffer = new ReplayBuffer()
    buffer.observe(anArrival(stamp(BASE, 0), 1, 1))
    buffer.observe(anArrival(stamp(BASE, 240), 2, 2))
    buffer.observe(anArrival(stamp(BASE, 600), 3, 3))
    // A frame from a moment the feed had already passed. It is inside the window
    // and it is a frame the console was shown, so it is kept -- but not appended:
    // appending it would leave the ring stamped backwards, and both of this
    // module's promises are about the ring as it is held.
    buffer.observe(anArrival(stamp(BASE, 120), 4, 4))
    expect(buffer.records().map((record) => record.at)).toEqual([0, 120, 240, 600].map((s) => stamp(BASE, s)))
    // `scrub` walks back from the newest record, so a ring holding the late
    // frame out of order would return the 240 s frame for a scrub to 200 s.
    expect(buffer.scrub(stamp(BASE, 200))?.at).toBe(stamp(BASE, 120))
    // And the export is still a body the real loader accepts: it refuses records
    // stamped backwards (`src/source/fixture.ts`), which is the property the
    // fixture-format half of this module rests on.
    const meta: FixtureMeta = { ...theFixture().header, synthetic: true }
    const loaded = loadFixture(writeFixture(buffer.records(), meta), 'the late arrival')
    expect(loaded.records).toEqual(buffer.records())
  })

  it('keeps the notices of a snapshot, stamped where they arrived', () => {
    const buffer = new ReplayBuffer()
    buffer.observe({
      ...anArrival(stamp(BASE, 5), 1, 1),
      notices: [{ text: 'reading the runs bucket through the API', receivedAt: stamp(BASE, 4) }],
    })
    expect(buffer.records().map((record) => record.at)).toEqual([stamp(BASE, 4), stamp(BASE, 5)])
    expect(noticeOf(buffer.records()[0] as ReplayRecord)).toBe('reading the runs bucket through the API')
    expect(documentOf(buffer.records()[1] as ReplayRecord)).toMatchObject({ counted_usd: 1 })
  })

  it('exports the fixture format body, byte for byte', () => {
    const fixture = theFixture()
    const buffer = new ReplayBuffer()
    for (const snapshot of arrivals(fixture)) buffer.observe(snapshot)
    // Nothing left the buffer, so the body it writes is the recording's own --
    // which is what makes an export a fixture rather than a file shaped like one.
    expect(buffer.export()).toBe(bodyOf(readFileSync(SYNTHETIC_WHERE, 'utf8')))
  })

  it('exports a document nested ten thousand deep instead of crashing, as writeFixture does', () => {
    // A recursive `JSON.stringify` throws `RangeError` at this depth; the
    // fixture writer serialises with an explicit stack for exactly this
    // document (`fixture.test.ts`), and an export that claims to be its body
    // byte for byte must survive the same input and write the same bytes.
    const depth = 10_000
    const deep = JSON.parse(`{"a":${'['.repeat(depth)}${']'.repeat(depth)}}`) as Readonly<Record<string, unknown>>
    const buffer = new ReplayBuffer()
    buffer.observe({ ...anArrival(stamp(BASE, 0), 1, 1), document: deep })
    buffer.observe(anArrival(stamp(BASE, 1), 2, 2))
    let exported = ''
    expect(() => {
      exported = buffer.export()
    }).not.toThrow()
    const meta: FixtureMeta = { ...theFixture().header, synthetic: true }
    expect(exported).toBe(bodyOf(writeFixture(buffer.records(), meta)))
  })

  it('exports a ring that is empty as empty, not as a fixture with no records', () => {
    expect(new ReplayBuffer().export()).toBe('')
  })

  it('round-trips an export through the fixture source', async () => {
    const fixture = theFixture()
    const buffer = new ReplayBuffer()
    const seen = arrivals(fixture)
    for (const snapshot of seen) buffer.observe(snapshot)

    // The writer is the main process's (it digests with `node:crypto`), so the
    // test is what joins the two halves: the buffer's records, the fixture's
    // paperwork, and then the real loader.
    const meta: FixtureMeta = { ...fixture.header, synthetic: true }
    const written = writeFixture(buffer.records(), meta)
    expect(written).toBe(readFileSync(SYNTHETIC_WHERE, 'utf8'))
    const loaded = loadFixture(written, 'the replay export')
    expect(loaded.records).toEqual(buffer.records())

    const timers = new FakeTimers()
    const source = new FixtureSource({ fixture: loaded, timers })
    const feed = source.subscribeLive({})
    const replayed: Snapshot[] = []
    feed.onChange((snapshot) => {
      replayed.push(snapshot)
    })
    timers.advanceTo(Number.MAX_SAFE_INTEGER)
    await source.close()

    // The same documents, in the same order, with the recorded gap intact: the
    // export is a recording the console can replay, not a list of documents.
    expect(replayed.map((snapshot) => snapshot.document)).toEqual(seen.map((snapshot) => snapshot.document))
    expect(replayed.map((snapshot) => snapshot.receivedAt)).toEqual(seen.map((snapshot) => snapshot.receivedAt))
    // The recording's own offsets, and the degraded sentence where it arrived:
    // 0 s, 5 s, 100 s (carrying the 8 s notice), 260 s, 261 s.
    expect(replayed.map((snapshot) => snapshot.receivedAt)).toEqual([0, 5, 100, 260, 261].map((s) => stamp(BASE, s)))
    expect(replayed[2]?.notices.map((notice: Notice) => notice.text)).toEqual([
      'no budget backend: showing run records only',
    ])
  })

  it('scrubs to the record in effect at a moment', () => {
    const fixture = theFixture()
    const buffer = new ReplayBuffer()
    for (const snapshot of arrivals(fixture)) buffer.observe(snapshot)
    const documents = buffer.records().filter((record) => 'document' in record)

    // A moment before the recording is not the recording's first document: it
    // is a moment with nothing to show, which a scrubber must draw as such.
    expect(buffer.scrub(stamp(BASE, -1))).toBeNull()
    // Between two documents, the view in effect is the earlier one.
    const second = documents[1] as ReplayRecord
    const third = documents[2] as ReplayRecord
    expect(Date.parse(third.at) - Date.parse(second.at)).toBeGreaterThan(1)
    expect(buffer.scrub(stamp(second.at, 1))).toEqual(second)
    // At a document's own stamp, it is that document.
    expect(buffer.scrub(third.at)).toEqual(third)
    // Past the end, the newest document -- the view that is still in effect.
    expect(buffer.scrub(stamp(BASE, 9 * 3600))).toEqual(documents.at(-1))
  })

  it('scrubbing to a moment reproduces that moment\'s view', () => {
    const fixture = theFixture()
    const buffer = new ReplayBuffer()
    const seen = arrivals(fixture)
    for (const snapshot of seen) buffer.observe(snapshot)

    const viewAt = (snapshot: Snapshot) => {
      const decoded = decodeSnapshot(snapshot.document)
      if (!decoded.ok) throw new Error('the recording is not a live document')
      return buildLiveModel(decoded.value)
    }

    // Two arrivals whose numbers differ, so that a scrub returning the wrong one
    // is visible: the first document and the one five seconds later.
    const [first, second] = seen
    expect(first).toBeDefined()
    expect(second).toBeDefined()
    expect(viewAt(first as Snapshot)).not.toEqual(viewAt(second as Snapshot))

    const scrubbed = buffer.scrub(second!.receivedAt)
    expect(scrubbed).not.toBeNull()
    const decoded = decodeSnapshot(documentOf(scrubbed as ReplayRecord))
    expect(decoded.ok).toBe(true)
    if (!decoded.ok) return
    expect(buildLiveModel(decoded.value)).toEqual(viewAt(second as Snapshot))
    expect(buildLiveModel(decoded.value)).not.toEqual(viewAt(first as Snapshot))
  })
})
