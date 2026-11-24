// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The prewarm queue: after the tab on screen has been painted, the console reads
// the primary section of each **other** tab into that tab's session cache, one
// read at a time, so the operator's first switch to one of them draws a stamped
// view this session already read instead of a cold one (PW1 of
// `docs/console-improvements/implementation-plan.md`; D2906 and F16 of
// `docs/console-improvements/reconciliation.md`).
//
// **Why a queue and not three parallel reads.** Every spawn this console makes
// is one child process of the CLI, and the reads a tab's first visit makes are
// seconds each (the report is the longest at about twenty). A first paint that
// fired all of them at once would spend three children on a window where the
// operator is reading one tab, and would make the read *they* asked for -- the
// tab they switch to -- queue behind two nobody is waiting for. One at a time in
// the navigation's own order is the shape the plan asks for, and it is the shape
// that gives the tab switched to first the read that is already out.
//
// **What this queue is not.** It is not Live: the Live screen is the tab the
// first paint belongs to, its model is the poll's own read, and a feed read into
// a cache would be a second reader of one installation's job list (`prewarm`
// takes the tabs to read, and `entry.ts` names no feed for Live). It is not a
// write: every read it starts is a query, and the cache it fills is the
// renderer's own (`section.ts`, R2) -- nothing here can change an installation.
// And it is not a read a console draws: nothing this queue reads is painted, and
// a tab that is entered while its read is out draws its own visit's read, whose
// answer replaces whatever the queue put there.
//
// **The stop is the switch.** `still` is the caller's claim on the view that
// started the queue (`entry.ts`'s generation): a tab switch or an installation
// switch moves it, and the queue then reads **nothing** further -- the read in
// flight is the last one, and even its answer is not cached, because the tab it
// was read for is not the tab the operator chose. That is the whole of the
// cancellation: no read is killed mid-flight (a child is not this module's to
// kill), and no answer is written after the view moved.

import type { CliCompatibilityMode } from '../shared/version'

/** PrewarmTab is one other tab and the one read that fills its cache: `id` is
 * the tab's name in the navigation (the same string `show` switches on), and
 * `read` is the tab's own primary read, which caches its answer as its first
 * visit would (`entry.ts` builds both). */
export interface PrewarmTab {
  readonly id: string
  readonly read: () => Promise<void>
}

/** PrewarmOptions is the queue's whole input: the handshake's mode, the tab on
 * screen, the other tabs in the order the navigation offers them, and the claim
 * that says the view has not moved. */
export interface PrewarmOptions {
  /** the handshake's mode: a console that is not `ok` has views it must not
   * read -- a CLI whose shape it does not know (`reduced`) or one it refuses to
   * drive at all (`refused`) -- so it prewarms nothing. */
  readonly mode: CliCompatibilityMode
  /** the tab on screen, which is never prewarmed: it either has its model
   * already (the paint this queue follows) or is reading it now. */
  readonly active: string
  readonly tabs: readonly PrewarmTab[]
  /** whether the view the queue was started for is still the view on screen.
   * It is asked before every read and never during one. */
  readonly still: () => boolean
}

/**
 * prewarm runs the queue to its end: each other tab's read in order, one at a
 * time, and nothing at all once the view has moved.
 *
 * A read that refuses is not this queue's sentence to draw: the tab's own visit
 * draws it, from its own read, when the operator gets there. So a throw is
 * swallowed and the queue goes on to the next tab -- the one read that failed
 * costs that tab its warm view and must not cost the others theirs.
 */
export const prewarm = async (options: PrewarmOptions): Promise<void> => {
  if (options.mode !== 'ok') return
  for (const tab of options.tabs) {
    if (tab.id === options.active) continue
    // Before the read, never during one: the read already out is the last one
    // the queue started, and its own answer is dropped by the `read` this tab
    // was built with (it asks the same claim before it caches anything).
    if (!options.still()) return
    try {
      await tab.read()
    } catch {
      // The tab's own section carries the refusal (see above).
    }
  }
}
