// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The one thing a write owes the panes that show what it changed: the scopes it
// touches, cleared and read again (R2, the reconciliation of F17;
// `docs/console-improvements/reconciliation.md`).
//
// **Why this is not a method on `resultCache`.** A scope is not a cache. The
// operator presses a setting in the Project tab and what is stale is that tab:
// the view the session cached, the figures `showProjects` already drew, and the
// read that fills them -- and the read is the entry's own door, which no cache
// knows about. So a screen registers **what it holds** and **how to read it
// again**, once, and a write names the scopes; the clearing and the re-reading
// happen in one place rather than at each of the three write sites.
//
// **A write names scopes, never caches.** The names are the tabs the console
// shows, so a site that says `invalidate(['projects'])` says the true thing --
// the Project tab's panes show the row this write changed -- and a scope is a
// closed union rather than a `string`, because a write that named a scope no
// screen registered would clear nothing and look exactly like a write that had.
//
// **What is *not* here matters as much.** This is the console's only invalidator
// and its only caller is a write: the freshness rule (`CACHE_FRESH_MS`) and the
// Refresh control each read one view again on purpose, and neither goes through
// here. A scope nobody has registered is not an error -- a console whose version
// hides a tab has no screen to register it, and the write that names it is still
// the truth about what it changed.

/** CacheScope is the set of panes a write may name. They are the tabs of the
 * navigation, because that is the unit the operator sees: one write, the panes
 * that show its row. */
export type CacheScope = 'live' | 'graph' | 'projects' | 'spend' | 'coordinator'

/**
 * ScopeRead is one scope as the screen that owns it registers it.
 *
 * `clear` drops everything the scope's read answered with -- the session cache
 * of the views it read and whatever the screen painted out of them -- and
 * `reread` makes that read again. They are separate because a scope the
 * operator is **not** looking at is cleared and not read: nothing is on screen
 * to paint a busy mark into, the next visit reads the tab on entry anyway, and
 * a read for a tab nobody is on would spawn a child for it.
 */
export interface ScopeRead {
  clear(): void
  reread(): void
}

export interface CacheInvalidator {
  /** remember registers a scope, replacing the screen's earlier registration of
   * it: a tab registers on every visit, and the closures it registers then are
   * the ones that read the tab as it is now open. */
  remember(scope: CacheScope, read: ScopeRead): void
  /** invalidate clears each named scope and reads it again, once per scope even
   * if a caller names it twice. The scope's `reread` decides whether the tab in
   * front is the one being read. */
  invalidate(scopes: readonly CacheScope[]): void
  /** forget clears every registered scope and reads nothing: it is the
   * installation switch, where the console is about to close every read it
   * holds (`forgetLive`), and the first visit to a tab reads it. */
  forget(): void
}

/**
 * cacheInvalidator makes one invalidator.
 *
 * It is a factory rather than a module-level singleton for the reason the
 * screens are functions of a surface: two consoles in one process -- a unit
 * test's two surfaces, a window and a reload -- are two installations, and a
 * shared registry would let one window's write clear the other's caches.
 */
export function cacheInvalidator(): CacheInvalidator {
  const scopes = new Map<CacheScope, ScopeRead>()
  return {
    remember(scope: CacheScope, read: ScopeRead): void {
      scopes.set(scope, read)
    },
    invalidate(named: readonly CacheScope[]): void {
      const done = new Set<CacheScope>()
      for (const scope of named) {
        if (done.has(scope)) continue
        done.add(scope)
        const read = scopes.get(scope)
        // Cleared before it is read: a read that answers late must not find the
        // old view still in the cache and roll it forward.
        if (read === undefined) continue
        read.clear()
        read.reread()
      }
    },
    forget(): void {
      for (const read of scopes.values()) read.clear()
    },
  }
}
