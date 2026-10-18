// The wire node shapes of the four granted families, read leniently
// (mobile plan, Task P3.2; D112's answer, tension (b1)).
//
// The phone reads database **nodes**, not the console's `watch --json`
// document, so this module is the one place where the tree's own types decide
// what a value means. They are the Go types of `internal/budget/model.go`, and
// two of them disagree about what "absent" says -- which is the whole reason
// this file exists rather than a single `number()` helper:
//
//   * **A cap is a pointer** (`GlobalCaps.DailyMicros *Micros`, and the
//     package comment says why: "a cap that is not set is nil, never zero,
//     because the rules read a missing node as null and null compares false").
//     So an absent or `null` cap is **unset**, which is `unavailable` -- never
//     a cap of zero, which would be a project allowed to spend nothing.
//   * **A counter is a plain field with `omitempty`** (`Counters.Counted
//     Micros`), so an absent counter is **zero**: a day whose counter node is
//     not there has counted nothing. `internal/watch/build.go`'s `counters()`
//     reads it the same way, and so does the CLI's document. The phone shows
//     the same zero rather than inventing a second meaning for the same tree.
//
// **Where the phone knows more than the CLI did.** `counters()` cannot tell an
// unreadable node from an empty one -- `if !ok || err != nil { return
// budget.Counters{} }` -- and reports zero for both. The phone *can* tell: a
// node that is present with a value it cannot read is `unavailable` here,
// because §10.13 rule 5 says a missing field is never `0`, and a zero the
// reader could not read is exactly the number that rule exists to keep off a
// screen. Absent stays zero; unreadable is `unavailable`. Both are tested.
//
// **Lenient about what it does not read.** `/spend/<day>/repos/<slug>` carries
// `byModel` (a map of per-model usage) and `calls`, `/config/caps/repos/<slug>`
// carries `repo`, and a kill switch carries fields a later phase may add. This
// module ignores what it does not use rather than refusing the node: a tree
// that grows a field must not break the feed. `document.js` is strict about the
// *document*, which is this app's own shape -- the two rules are different on
// purpose.
//
// Every amount is integer micro-dollars on the wire (`pricing.Micros`) and
// dollars here, divided once at this boundary by `usd()`.

/** The refusal codes of a node read. */
export const NODE_CODES = Object.freeze({
  NOT_AN_OBJECT: 'not-an-object',
  BAD_MICROS: 'bad-micros',
});

/** The wire's unit: every amount in the database is integer micro-dollars
 * (`pricing.Micros`), and the document's are dollars. */
export const MICROS_PER_USD = 1_000_000;

/** Integer micro-dollars to dollars, or `null` when the value is not an
 * amount. `pricing.Micros.USD()` is the same division. */
export function usd(micros) {
  if (typeof micros !== 'number' || !Number.isInteger(micros) || micros < 0) return null;
  return micros / MICROS_PER_USD;
}

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isNode(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/**
 * A cap, in micro-dollars: the field's value, or `null` when it is unset.
 *
 * Unset is an absent field, an explicit `null`, a node that is not there, and
 * a value that is not an integer amount. All four mean the same thing to a
 * reader -- there is no cap here to compare against -- and none of them is a
 * cap of zero. A *negative* cap is not an amount either and reads as unset
 * rather than as a bound in the wrong direction.
 */
export function capMicros(node, field) {
  if (!isNode(node)) return null;
  const value = node[field];
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) return null;
  return value;
}

/** The project's day cap: `/config/caps/global.dailyMicros`. */
export function globalDailyMicros(global) {
  return capMicros(global, 'dailyMicros');
}

/**
 * A repository's day cap, resolved the way `Caps.RepoDaily()` resolves it: the
 * repository's own node, else `/config/caps/defaults`, else nothing. `null`
 * means no cap is set anywhere, which is `unavailable` and not a cap of zero.
 */
export function repoDailyMicros(caps) {
  if (!isNode(caps)) return null;
  const own = capMicros(caps.repo, 'dailyMicros');
  if (own !== null) return own;
  return capMicros(caps.defaults, 'repoDailyMicros');
}

/**
 * Read the cap nodes of one repository (its own, the defaults, the project's)
 * into the two amounts the document carries.
 *
 * Only these two: the contract's `daily_cap_usd` for the project is
 * `Caps.GlobalDaily()` and for a repository is `Caps.RepoDaily()`. The tree's
 * per-run caps are **not read**, because no document field carries one --
 * reading a figure the phone would never draw would be a second meaning for
 * the same tree with nothing to check it against. Both amounts are dollars or
 * `null`.
 */
export function readCaps({ global, defaults, repo } = {}) {
  return Object.freeze({
    ok: true,
    caps: Object.freeze({
      globalDaily: usd(globalDailyMicros(global)),
      daily: usd(repoDailyMicros({ repo, defaults })),
    }),
  });
}

/**
 * A spend counter, in dollars: `/spend/<day>/global` or
 * `/spend/<day>/repos/<slug>`.
 *
 * The node is **not** judged for whether it is there: an absent node and an
 * empty one are both zero (see the header), and which of the two it was is a
 * fact about the *stream*, not about the tree -- §10.7's "the new pair delivers
 * its put of `/`" is a delivery, and `reader.js` tracks deliveries. A value
 * that is not an object at all is still refused: a counter node holding a
 * string is not a counter.
 */
export function readCounters(value) {
  if (value === undefined || value === null) {
    return Object.freeze({ ok: true, counters: Object.freeze({ counted: 0, spent: 0, notional: 0 }) });
  }
  if (!isNode(value)) return refuse(NODE_CODES.NOT_AN_OBJECT, 'counters');
  return Object.freeze({
    ok: true,
    counters: Object.freeze({
      counted: usd(counterMicros(value.counted)),
      spent: usd(counterMicros(value.spent)),
      notional: usd(counterMicros(value.notional)),
    }),
  });
}

/** A counter's micro-dollars: absent is zero (the tree's own `omitempty`), and
 * a value that is not an amount is `null`, which is `unavailable`. */
function counterMicros(value) {
  if (value === undefined || value === null) return 0;
  if (typeof value !== 'number' || !Number.isInteger(value)) return null;
  return Math.max(value, 0); // `nonneg` in internal/watch/build.go
}

/**
 * A kill switch: `/config/kill/global` or `/config/kill/repos/<slug>`.
 *
 * `null` is "not killed", which is a fact and not a missing value. A node that
 * is there but unreadable is **ON**: `internal/watch/build.go`'s `State.kill`
 * treats it that way on purpose ("runs treat it as ON"), because a switch that
 * cannot be read is a switch that might be on, and the safe direction for a
 * stop switch is to believe it. The reason says so, which is what the
 * contract's `kill.reason` is for.
 */
export const UNREADABLE_KILL = 'the switch could not be read';

export function readKill(value) {
  if (value === undefined || value === null) return Object.freeze({ ok: true, kill: null });
  if (!isNode(value) || !killReadable(value)) {
    return Object.freeze({
      ok: true,
      kill: Object.freeze({ on: true, by: null, at: null, reason: UNREADABLE_KILL }),
    });
  }
  if (value.on !== true) return Object.freeze({ ok: true, kill: null });
  return Object.freeze({
    ok: true,
    kill: Object.freeze({
      on: true,
      by: typeof value.by === 'string' && value.by.length > 0 ? value.by : null,
      // `at` is epoch milliseconds on the wire (`budget.Kill`), and the
      // document carries RFC 3339 (`jsonKill`). A bad instant is a missing
      // one, never the epoch.
      at: epochMs(value.at) === null ? null : new Date(value.at).toISOString(),
      reason: typeof value.reason === 'string' && value.reason.length > 0 ? value.reason : null,
    }),
  });
}

function epochMs(value) {
  if (typeof value !== 'number' || !Number.isInteger(value) || value <= 0) return null;
  return value;
}

/** Is this kill node one a reader can believe? A field of the wrong type is a
 * node `budget.Decode` would have errored on, which is `State.kill`'s
 * unreadable case -- not an `on: false` this app invented. */
function killReadable(node) {
  if (node.on !== undefined && typeof node.on !== 'boolean') return false;
  if (node.by !== undefined && typeof node.by !== 'string') return false;
  if (node.reason !== undefined && typeof node.reason !== 'string') return false;
  if (node.at !== undefined && (typeof node.at !== 'number' || !Number.isInteger(node.at))) return false;
  return true;
}

/**
 * The mailbox heartbeat, read as a node and **not** as a presence verdict.
 *
 * The phone's own mailbox is one of the four granted families, so the reader
 * subscribes to it and this reads what is there: the instant the consumer last
 * wrote, the boot id and the agent list it advertises (D113b, D161). What a
 * stale heartbeat *means* -- `live`, `stale`, `offline`, and the clock-skew
 * "clock differs" -- is the presence rule of check 84 (P4.2), which is a
 * different task with a different owner and a real clock. P3.2 reads the node
 * and draws no conclusion from it.
 */
export function readHeartbeat(value) {
  if (value === undefined || value === null) {
    return Object.freeze({ ok: true, heartbeat: Object.freeze({ present: false, at: null, boot: null, agents: null }) });
  }
  if (!isNode(value)) return refuse(NODE_CODES.NOT_AN_OBJECT, 'heartbeat');
  return Object.freeze({
    ok: true,
    heartbeat: Object.freeze({
      present: true,
      at: epochMs(value.at),
      boot: typeof value.boot === 'string' && value.boot.length > 0 ? value.boot : null,
      // The advertised agents are a list of records, and nothing in P3.2 draws
      // them; the count is kept so a later phase has the fact without the
      // contents.
      agents: Array.isArray(value.agents) ? value.agents.length : null,
    }),
  });
}
