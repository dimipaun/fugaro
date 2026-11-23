// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The prewarm queue: after the first paint of the tab on screen, the console
// reads the primary section of each *other* tab into its session cache, one read
// at a time, so the operator's first switch to one of them draws what this
// console already read rather than waiting on a cold read
// (`src/renderer/prewarm.ts`; D2906 and F16 of
// `docs/console-improvements/reconciliation.md`).
//
// **Why the queue is a module of its own.** What it must not do is the whole of
// its contract, and none of it is about a screen: it must never read the tab the
// operator is on (`active`), never read in a console whose handshake was not
// `ok`, never start the second read before the first answered, and never read on
// once the view moved (`still`) -- the switch's own claim. `entry.ts` supplies
// the reads and the claim; this file supplies a fake of each and asserts the
// order and the stops.
//
// The reads are deferred rather than immediate so that "one at a time" is a fact
// about the queue and not about how fast a promise settles: a read that answered
// in the same turn would let a queue that fires everything at once pass.

import { describe, expect, it } from 'vitest'

import { prewarm } from '../../src/renderer/prewarm'
import type { PrewarmTab } from '../../src/renderer/prewarm'
import type { CliCompatibilityMode } from '../../src/shared/version'

/** deferred is a read the test settles by hand, so the queue's next start is
 * observable while this one is outstanding. */
const deferred = (): { start: () => Promise<void>; settle: () => void; fail: () => void } => {
  let settle: () => void = () => undefined
  let fail: () => void = () => undefined
  const promise = new Promise<void>((resolve, reject) => {
    settle = resolve
    fail = () => reject(new Error('read refused'))
  })
  return {
    start: () => promise,
    settle: () => {
      settle()
    },
    fail: () => {
      fail()
    },
  }
}

/** queue is the tabs list plus the log of what started, so a claim about the
 * order is a claim about the log. */
const queue = (ids: readonly string[], reads: readonly ReturnType<typeof deferred>[]): {
  tabs: readonly PrewarmTab[]
  started: string[]
} => {
  const started: string[] = []
  const tabs = ids.map((id, index) => ({
    id,
    read: () => {
      started.push(id)
      return reads[index]!.start()
    },
  }))
  return { tabs, started }
}

/** flush lets every microtask the queue chained run, which is more than one turn
 * because each read is awaited twice (the read, then the loop's own check). */
const flush = async (): Promise<void> => {
  for (let i = 0; i < 10; i += 1) await Promise.resolve()
}

describe('prewarm', () => {
  it('reads the other tabs in the order the navigation offers them, one at a time', async () => {
    const graph = deferred()
    const spend = deferred()
    const projects = deferred()
    const { tabs, started } = queue(['graph', 'spend', 'projects'], [graph, spend, projects])

    const done = prewarm({ mode: 'ok', active: 'live', tabs, still: () => true })
    await flush()
    expect(started).toEqual(['graph'])

    graph.settle()
    await flush()
    expect(started).toEqual(['graph', 'spend'])

    spend.settle()
    await flush()
    expect(started).toEqual(['graph', 'spend', 'projects'])

    projects.settle()
    await done
  })

  it('never reads the tab the operator is on', async () => {
    const graph = deferred()
    const spend = deferred()
    const projects = deferred()
    const { tabs, started } = queue(['graph', 'spend', 'projects'], [graph, spend, projects])

    const done = prewarm({ mode: 'ok', active: 'spend', tabs, still: () => true })
    await flush()
    expect(started).toEqual(['graph'])

    graph.settle()
    await flush()
    expect(started).toEqual(['graph', 'projects'])

    projects.settle()
    await done
  })

  it('reads nothing in a console whose handshake was not ok', async () => {
    for (const mode of ['reduced', 'refused'] as readonly CliCompatibilityMode[]) {
      const graph = deferred()
      const { tabs, started } = queue(['graph'], [graph])
      await prewarm({ mode, active: 'live', tabs, still: () => true })
      expect(started).toEqual([])
    }
  })

  it('reads nothing at all when there is no other tab to read', async () => {
    const { tabs, started } = queue([], [])
    await prewarm({ mode: 'ok', active: 'live', tabs, still: () => true })
    expect(started).toEqual([])
  })

  it('stops the queue when the view moves on, and the read in flight is the last one', async () => {
    const graph = deferred()
    const spend = deferred()
    let still = true
    const { tabs, started } = queue(['graph', 'spend'], [graph, spend])

    const done = prewarm({ mode: 'ok', active: 'live', tabs, still: () => still })
    await flush()
    expect(started).toEqual(['graph'])

    // The switch: `entry.ts`'s claim moves the generation, which is what `still`
    // reads. The answer to the read already out is not the next read's business.
    still = false
    graph.settle()
    await done
    expect(started).toEqual(['graph'])
  })

  it('stops the queue outright when the view has already moved on', async () => {
    const graph = deferred()
    const { tabs, started } = queue(['graph'], [graph])
    await prewarm({ mode: 'ok', active: 'live', tabs, still: () => false })
    expect(started).toEqual([])
  })

  it('goes on to the next tab when one read fails', async () => {
    const graph = deferred()
    const spend = deferred()
    const { tabs, started } = queue(['graph', 'spend'], [graph, spend])

    const done = prewarm({ mode: 'ok', active: 'live', tabs, still: () => true })
    await flush()
    graph.fail()
    await flush()
    expect(started).toEqual(['graph', 'spend'])

    spend.settle()
    await done
  })
})
