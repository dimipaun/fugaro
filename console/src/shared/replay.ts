// The replay buffer: a bounded ring of what the console has been shown
// (desktop-console plan Task 2.3; design section 9.2).
//
// The design asks the Run detail screen for "a bounded in-memory ring of the
// last N minutes, scrubbable, exportable to a file". This module is that ring,
// and it is three decisions:
//
//   - **Bounded means both bounds.** A ring that only aged records out grows
//     without limit on a feed faster than the contract's one-document-per-second
//     floor (section 10.1), and a ring that only counted grows without limit in
//     *time* -- a quiet run watched for a day keeps yesterday. So there are two:
//     a window and a hard cap, `REPLAY_WINDOW_SECONDS` and `REPLAY_MAX_RECORDS`,
//     and the cap is the window's length at the floor, so a feed at the floor is
//     never cut short by it. What left is counted rather than forgotten, because
//     a screen that drew a window as if it were the whole run would be implying
//     a boundary it does not have.
//   - **A record is a fixture record.** `{at, document}` or `{at, notice}`, the
//     envelope `src/source/fixture.ts` writes and reads -- so an export is a
//     fixture's *body*, and the file an operator saves is a recording the
//     console can replay (design section 9.2: the export "doubles as the fixture
//     format for tests"). The header is not written here: it carries a SHA-256,
//     which is `node:crypto`, and this module is loaded by the renderer, which
//     has no Node builtins (design section 5.1). `writeFixture` in the main
//     process is what puts the header on.
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

/** ReplayLimits is the two bounds, for a caller that wants its own. */
export interface ReplayLimits {
  readonly windowSeconds: number
  readonly maxRecords: number
}

/** ReplayBounds is what the ring says about itself: its two bounds, the span it
 * actually holds, and how many records have left it. `from` and `to` are the
 * oldest and newest stamps held, or null when the ring is empty -- which is not
 * a span of zero. */
export interface ReplayBounds {
  readonly windowSeconds: number
  readonly maxRecords: number
  readonly from: string | null
  readonly to: string | null
  readonly records: number
  readonly dropped: number
}

/**
 * ReplayBuffer is the ring. It is fed snapshots in arrival order and it keeps
 * the last `windowSeconds` of them, up to `maxRecords`.
 *
 * Arrival order is preserved: the ring is what the console was shown, in the
 * order it was shown it, which is what an export has to be if it is to load as
 * a fixture (the loader refuses records stamped backwards). An arrival whose own
 * stamp is already outside the window is not stored at all -- it is a record
 * from before the window that arrived late, and a ring that kept it would hold
 * one frame no scrubber could reach.
 */
export class ReplayBuffer {
  readonly #windowMs: number
  readonly #maxRecords: number
  #records: ReplayRecord[] = []
  /** `#newestMs` is the newest stamp *seen*, kept even after the record holding
   * it is gone, so that the window is a window and not a function of the cap. */
  #newestMs = Number.NEGATIVE_INFINITY
  #dropped = 0

  constructor(limits: Partial<ReplayLimits> = {}) {
    this.#windowMs = (limits.windowSeconds ?? REPLAY_WINDOW_SECONDS) * 1000
    this.#maxRecords = limits.maxRecords ?? REPLAY_MAX_RECORDS
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
   */
  export(): string {
    return this.#records.map((record) => JSON.stringify(record)).join('\n')
  }

  /** #store appends a record if it belongs in the window, and prunes after. */
  #store(record: ReplayRecord): void {
    const at = Date.parse(record.at)
    if (!Number.isFinite(at)) return
    if (at > this.#newestMs) this.#newestMs = at
    if (at < this.#newestMs - this.#windowMs) {
      // Older than the window that ends at the newest arrival: it is not kept,
      // and it is counted as gone like any other record that left.
      this.#dropped += 1
      return
    }
    this.#records.push(record)
    const floor = this.#newestMs - this.#windowMs
    while (this.#records.length > 0 && Date.parse((this.#records[0] as ReplayRecord).at) < floor) {
      this.#records.shift()
      this.#dropped += 1
    }
    while (this.#records.length > this.#maxRecords) {
      this.#records.shift()
      this.#dropped += 1
    }
  }
}
