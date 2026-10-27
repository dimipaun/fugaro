// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The replay buffer: a bounded ring of what the console has been shown
// (desktop-console plan Task 2.3; design section 9.2).
//
// The design asks the Run detail screen for "a bounded in-memory ring of the
// last N minutes, scrubbable, exportable to a file". This module is that ring,
// and it is three decisions:
//
//   - **Bounded means every bound.** A ring that only aged records out grows
//     without limit on a feed faster than the contract's one-document-per-second
//     floor (section 10.1), and a ring that only counted grows without limit in
//     *time* -- a quiet run watched for a day keeps yesterday. A ring that
//     counted records but not their size holds 900 documents of whatever size
//     arrived -- 900 x 4 MiB is 3.6 GiB in a renderer. So there are three: a
//     window, a record cap and a byte cap, `REPLAY_WINDOW_SECONDS`,
//     `REPLAY_MAX_RECORDS` and `REPLAY_MAX_BYTES`. The record cap is the
//     window's length at the floor, so a feed at the floor is never cut short
//     by it. What left is counted rather than forgotten, because a screen that
//     drew a window as if it were the whole run would be implying a boundary it
//     does not have.
//   - **A record is a fixture record.** `{at, document}` or `{at, notice}`, the
//     envelope `src/source/fixture.ts` writes and reads -- so an export is a
//     fixture's *body* (design section 9.2: the export "doubles as the fixture
//     format for tests"). The header is not written here: it carries a SHA-256,
//     which is `node:crypto`, and this module is loaded by the renderer, which
//     has no Node builtins (design section 5.1). `writeFixture` in the main
//     process is what puts the header on.
//   - **An export is raw.** The ring holds the documents exactly as they
//     arrived, unredacted -- it is what the screen drew from, and the screen
//     sanitizes at drawing time, not at arrival. So `export()` is **not** a file
//     anyone may keep, share or commit: a saved recording is replayable and
//     shareable only once the main process has run it through the recorder's
//     redaction (`src/source/redact.ts`, the pass `record-fixture` uses) before
//     `writeFixture` puts the header on and the file is written. Nothing wires
//     the export to a file yet; whatever does must redact first, and must not
//     hand the raw body to the disk, a clipboard or an IPC reply.
//   - **Scrubbing answers the view, not the stream.** `scrub(at)` returns the
//     newest *document* at or before a moment -- what the console was showing
//     then -- so a scrubber cannot land on a notice and draw it as a frame. The
//     notices stay in the ring and in the export; they are what arrived, and
//     they are not a view.
//
// The clock is never read. A record's stamp is the console's own arrival stamp,
// handed in with the snapshot (`src/source/source.ts`), and the window is
// measured from the newest arrival in the ring -- so a test drives a replay
// across fifteen minutes without waiting, and a system clock that steps
// backwards cannot rewind the ring.
//
// **The ring is held in stamp order, and a late frame is filed where it
// belongs.** For a feed that arrives in order the two are the same thing, but
// an arrival can carry a stamp older than records already held -- a document
// from a moment the feed had already passed. Appending it would break the two
// properties this module promises at once: the export would no longer be a body
// `loadFixture` accepts (it refuses records stamped backwards,
// `src/source/fixture.ts`'s order check) and `scrub`'s walk from the newest
// record would stop at the late one and return an older frame than the newest
// at or before the moment. It is still a frame the console drew, so it is filed
// by its stamp rather than dropped; among records sharing a stamp, arrival order
// is kept.

import { stringifyDeep } from './stringify'

/**
 * ReplayDocument is one document as it arrived, with the console's own stamp.
 * It is `FixtureRecord`'s document arm, declared here rather than imported so
 * that this module -- which the renderer loads -- depends on no other module at
 * all.
 */
export type ReplayDocument = {
  readonly at: string
  readonly document: Readonly<Record<string, unknown>>
}

/** ReplayNotice is one line of stdout that was not a JSON object: the degraded
 * mode's sentence, which arrives on the same stream as the documents. */
export type ReplayNotice = {
  readonly at: string
  readonly notice: string
}

/** ReplayRecord is one frame of the ring: what arrived, and when. The two arms
 * are mutually exclusive, exactly as the fixture format's are. */
export type ReplayRecord = ReplayDocument | ReplayNotice

/** ReplayArrival is what the ring is fed: the part of a `Snapshot`
 * (`src/source/source.ts`) the buffer uses. A structural subset rather than an
 * import, for the reason above -- and because the buffer has no business with a
 * snapshot's sequence, day or rollover. */
export interface ReplayArrival {
  readonly document: Readonly<Record<string, unknown>>
  readonly receivedAt: string
  readonly notices: readonly { readonly text: string; readonly receivedAt: string }[]
}

/**
 * REPLAY_WINDOW_SECONDS is how much of the recent past the ring holds: fifteen
 * minutes. The design says "the last N minutes" and leaves N to the screen; a
 * run's stage is minutes long, the stale banner trips at 45 s (design section
 * 6.1 rule 3), and a window an order of magnitude larger is a window whose
 * oldest frames nobody scrubs to.
 */
export const REPLAY_WINDOW_SECONDS = 15 * 60

/**
 * REPLAY_MAX_RECORDS is the hard cap: one record per second over the window,
 * which is the contract's floor on how often `watch` writes a document (section
 * 10.1). At the floor the two bounds coincide, so the cap never cuts a
 * well-behaved feed short -- it bounds a misbehaving one.
 */
export const REPLAY_MAX_RECORDS = 900

/**
 * REPLAY_MAX_BYTES is the third bound: the sum of the held records' serialized
 * sizes (UTF-8 bytes of each export line, newlines not counted), 64 MiB. A
 * `watch` document is kilobytes, so 900 of them sit far below it and the bound
 * never binds a well-behaved feed; it is what stops a feed of very large
 * documents from holding 900 of them. A single record larger than the whole
 * bound is not kept, and is counted as dropped.
 */
export const REPLAY_MAX_BYTES = 64 * 1024 * 1024

/** ReplayLimits is the three bounds, for a caller that wants its own. */
export interface ReplayLimits {
  readonly windowSeconds: number
  readonly maxRecords: number
  readonly maxBytes: number
}

/** ReplayBounds is what the ring says about itself: its three bounds, the bytes
 * it holds, the span it
 * actually holds, and how many records have left it. `from` and `to` are the
 * oldest and newest stamps held, or null when the ring is empty -- which is not
 * a span of zero. */
export interface ReplayBounds {
  readonly windowSeconds: number
  readonly maxRecords: number
  readonly maxBytes: number
  readonly bytes: number
  readonly from: string | null
  readonly to: string | null
  readonly records: number
  readonly dropped: number
}

/**
 * ReplayBuffer is the ring. It is fed snapshots in arrival order and it keeps
 * the last `windowSeconds` of them, up to `maxRecords` and `maxBytes`.
 *
 * It is meant to be **kept**: whoever receives the arrivals holds one buffer
 * for the life of the screen and observes each arrival once, and the Run
 * screen's model reads that buffer (`RunInputs.replay`). Rebuilding a ring
 * from a list of arrivals on every render would make the list -- unbounded --
 * the thing the caller has to keep.
 *
 * Records are held **in stamp order** rather than in arrival order, for the
 * reason the module header gives: that is what makes an export loadable as a
 * fixture and `scrub` the newest frame at or before a moment. An arrival whose
 * own stamp is already outside the window is not stored at all -- it is a record
 * from before the window that arrived late, and a ring that kept it would hold
 * one frame no scrubber could reach.
 */
export class ReplayBuffer {
  readonly #windowMs: number
  readonly #maxRecords: number
  readonly #maxBytes: number
  #records: ReplayRecord[] = []
  /** `#lines[i]` is `#records[i]` serialized, and `#sizes[i]` its UTF-8 size:
   * the byte bound is measured on what an export would write, and the export
   * reuses the lines rather than serializing twice. */
  #lines: string[] = []
  #sizes: number[] = []
  #bytes = 0
  /** `#newestMs` is the newest stamp *seen*, kept even after the record holding
   * it is gone, so that the window is a window and not a function of the cap. */
  #newestMs = Number.NEGATIVE_INFINITY
  #dropped = 0

  constructor(limits: Partial<ReplayLimits> = {}) {
    this.#windowMs = (limits.windowSeconds ?? REPLAY_WINDOW_SECONDS) * 1000
    this.#maxRecords = limits.maxRecords ?? REPLAY_MAX_RECORDS
    this.#maxBytes = limits.maxBytes ?? REPLAY_MAX_BYTES
  }

  /** observe adds one arrival. Its notices are stored first, where they
   * arrived: they came on the stream before the document that carries them. */
  observe(arrival: ReplayArrival): void {
    for (const notice of arrival.notices) {
      this.#store({ at: notice.receivedAt, notice: notice.text })
    }
    this.#store({ at: arrival.receivedAt, document: arrival.document })
  }

  /** records is the ring, oldest first. It is a copy: the ring is the buffer's
   * state, and a caller that could mutate it could break the bound. */
  records(): readonly ReplayRecord[] {
    return this.#records.slice()
  }

  /** bounds is the ring's own account of itself. */
  bounds(): ReplayBounds {
    let from: string | null = null
    let to: string | null = null
    for (const record of this.#records) {
      const at = Date.parse(record.at)
      if (from === null || at < Date.parse(from)) from = record.at
      if (to === null || at > Date.parse(to)) to = record.at
    }
    return {
      windowSeconds: this.#windowMs / 1000,
      maxRecords: this.#maxRecords,
      maxBytes: this.#maxBytes,
      bytes: this.#bytes,
      from,
      to,
      records: this.#records.length,
      dropped: this.#dropped,
    }
  }

  /**
   * scrub returns the view in effect at a moment: the newest document at or
   * before `at`, or null when the moment is before the ring's own start -- which
   * a scrubber draws as a moment with nothing to show, not as the first frame.
   */
  scrub(at: string): ReplayDocument | null {
    const moment = Date.parse(at)
    for (let index = this.#records.length - 1; index >= 0; index -= 1) {
      const record = this.#records[index] as ReplayRecord
      if (!('document' in record)) continue
      if (Date.parse(record.at) <= moment) return record
    }
    return null
  }

  /**
   * export is the ring as NDJSON: one record per line, `{at, document}` or
   * `{at, notice}`, no header and no trailing newline -- byte for byte the body
   * `writeFixture` (`src/source/fixture.ts`) writes below its header. An empty
   * ring exports as an empty string, which is a body with no records and not a
   * fixture with none.
   *
   * **The body is raw and unredacted** (module header): it is the input to the
   * main process's redaction, never a file in its own right.
   */
  export(): string {
    return this.#lines.join('\n')
  }

  /** #store files a record where its stamp belongs if it is in the window, and
   * prunes after. */
  #store(record: ReplayRecord): void {
    const at = Date.parse(record.at)
    if (!Number.isFinite(at)) return
    // The fixture writer's serializer, with its key order (`at`, then the
    // document or the notice): a recursive `JSON.stringify` throws on a
    // document nested deep enough, which `writeFixture` does not.
    const line = stringifyDeep(
      'document' in record ? { at: record.at, document: record.document } : { at: record.at, notice: record.notice },
    )
    const size = utf8Length(line)
    if (size > this.#maxBytes) {
      // Larger than the whole bound: keeping it would mean holding nothing else,
      // and over the bound besides. It is counted as gone like any other.
      this.#dropped += 1
      return
    }
    if (at > this.#newestMs) this.#newestMs = at
    if (at < this.#newestMs - this.#windowMs) {
      // Older than the window that ends at the newest arrival: it is not kept,
      // and it is counted as gone like any other record that left.
      this.#dropped += 1
      return
    }
    // The ring is sorted, so the place is found by walking back from the end:
    // an arrival in order -- the normal case -- costs one comparison, and a late
    // one is filed under its own stamp instead of appending out of order. A
    // record sharing a stamp goes after the ones already held, so arrivals that
    // are indistinguishable by time stay in the order they arrived.
    let index = this.#records.length
    while (index > 0 && Date.parse((this.#records[index - 1] as ReplayRecord).at) > at) index -= 1
    this.#records.splice(index, 0, record)
    this.#lines.splice(index, 0, line)
    this.#sizes.splice(index, 0, size)
    this.#bytes += size
    const floor = this.#newestMs - this.#windowMs
    while (this.#records.length > 0 && Date.parse((this.#records[0] as ReplayRecord).at) < floor) this.#dropOldest()
    while (this.#records.length > this.#maxRecords || this.#bytes > this.#maxBytes) this.#dropOldest()
  }

  /** #dropOldest removes the oldest record, its line and its size, and counts it. */
  #dropOldest(): void {
    this.#records.shift()
    this.#lines.shift()
    this.#bytes -= this.#sizes.shift() ?? 0
    this.#dropped += 1
  }
}

/** utf8Length is a string's size in UTF-8 bytes, counted without encoding it:
 * this module loads in the renderer and in Node alike, and needs no buffer. */
function utf8Length(text: string): number {
  let bytes = 0
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index)
    if (code < 0x80) bytes += 1
    else if (code < 0x800) bytes += 2
    else if (code >= 0xd800 && code <= 0xdbff && index + 1 < text.length) {
      const next = text.charCodeAt(index + 1)
      if (next >= 0xdc00 && next <= 0xdfff) {
        bytes += 4
        index += 1
      } else bytes += 3
    } else bytes += 3
  }
  return bytes
}
