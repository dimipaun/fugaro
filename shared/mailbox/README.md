# `mailbox` — the state machine, the dedupe ledger and the replay window

The mobile plan's **Task P1.1**, in the shared pure-logic package
[P0.1](../README.md) built. It answers one question — **given what this mailbox
has already seen and one event, what happens next?** — and it is the half of
D110 that the schema cannot decide.

**Why it exists.** Voice D49 promised idempotent delivery, and F19 is the list
of what the tree does not have: no message id, no ordering, no idempotency, no
replay protection, no ack. D110 supersedes D49 — but only by meeting the
standard D49 named — and this module is where that standard becomes code: an
explicit message id, **at-least-once** delivery with **dedupe on the consumer's
side**, an **ack**, and **replay protection**. The consumer lease is P1.2's, the
schema is P0.2's, the redactor is P1.3's, and *who* may write a message is
P2.1's rules. Nothing of theirs is built here.

```sh
cd shared && npm test          # this module's tests, with no install and no network
```

```js
import { initialState, step } from './mailbox/index.js';

let { state } = initialState({ now: 1_700_000_000_000 });

const first = step(state, {
  type: 'deliver',
  now: 1_700_000_000_000,
  message: { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_699_999_999_000 },
});
// => { ok: true, outcome: 'accepted', phase: 'pending', id: 'm-0001', sender: 'phone', state }

const acted = step(first.state, { type: 'act', now: 1_700_000_000_000 });
// => { ok: true, outcome: 'acted', phase: 'acted', id: 'm-0001', ... }

const again = step(acted.state, {
  type: 'deliver',
  now: 1_700_000_000_000,
  message: { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_699_999_999_000 },
});
// => { ok: true, outcome: 'duplicate', phase: 'acted', ... }  -- answered, not acted on again
```

## The phases

A phase is a **summary of the ledger**, not a counter: `step` refuses a state
whose declared phase disagrees with what its ledger holds (`invalid-state`),
because a state that can be read two ways has two answers to every question.

| Phase | What it says | Ledger |
|---|---|---|
| `idle` | nothing is waiting | only `acked` records, or none |
| `pending` | a delivery has arrived that nobody has acted on | at least one `accepted` record |
| `acted` | every delivery has been acted on; an ack is still owed | `acted` records, no `accepted` ones |
| `closed` | the mailbox is done | whatever it held when it closed |

## The events, and what each answers with

Every event carries the **injected `now`** it is to be judged at. The machine
reads no clock of its own — D117's rule is that the server's timestamp is the
authority, so the caller hands it in.

| Event | Fields | Outcome |
|---|---|---|
| `deliver` | `now`, `message` | `accepted`, `duplicate`, or a refusal |
| `act` | `now` | `acted`, or `nothing-to-act` |
| `ack` | `now` | `acked`, or `nothing-to-ack` |
| `close` | `now` | `closed` |

| Outcome | Means |
|---|---|
| `accepted` | the delivery is new; it is now in the ledger |
| `duplicate` | this `(sender, id)` is already known; answered, **not** acted on again |
| `acted` | the oldest un-acted delivery was acted on |
| `acked` | the oldest acted delivery was acked |
| `nothing-to-act` | there was nothing un-acted to act on |
| `nothing-to-ack` | there was nothing acted-on to ack |
| `closed` | the mailbox closed, keeping its ledger |

`nothing-to-act` and `nothing-to-ack` are **outcomes, not refusals**: "there was
nothing to do" is a fact about the mailbox, where "I could not read what you
handed me" is a bug. A consumer that re-acts on a redelivery gets one of these,
which is what makes a redelivery safe to answer rather than to guess about.

## The ledger, its cap, and why the cap is not a hole

`state.seen` holds one frozen record per `(sender, id)` — `{ sender, id, at,
status }` — in a **canonical order** (`at`, then `sender`, then `id`, by code
unit, never by locale). Two deliveries of the same pair in either order
therefore leave the *identical* state, which is what makes a redelivery
answerable at all. The record holds `at` and a status, never the message's
text: a ledger that carried text would be a second copy of the mailbox, and
D113b's MAC is what protects a message's contents, not this table.

| Status | Means |
|---|---|
| `accepted` | delivered, not acted on |
| `acted` | acted on, not acked |
| `acked` | acted on and acked — the delivery is finished |

The cap (`maxSeen`, default `DEFAULT_MAX_SEEN`) is a bound on what the
bookkeeping can hold. Three rules keep it from becoming a hole in the fence:

- a delivery with no room is **refused** (`dedupe-full`) unless something is
  evictable. Nothing **inside the window** is ever dropped to make room:
  dropping an in-window record is exactly the "acted on twice" failure dedupe
  exists to prevent;
- a record is evictable only when it is **outside the replay window** *and*
  either **`acked`** — the delivery is finished, so forgetting it cannot lose
  work — **or the record for the very `(sender, id)` now arriving**, which is
  dropped even when it is un-acked, because the window has already ruled that
  its message is not the one arriving and the ledger holds one record per pair.
  Outside the window is the load-bearing half of the rule: a redelivery of an
  evicted message is refused as a replay by the window rule before the ledger is
  consulted, so **eviction cannot re-admit a replay**. The one thing that is
  never evicted is an in-flight record *inside* the window, or an in-flight
  record for any other pair — a delivery that was acted on and never acked is
  work this consumer still owes an ack for, and dropping it would make room for
  a second action;
- the same `(sender, id)` with a different `at` **inside** the window is
  `id-conflict`: the id is the message's identity, so a second message under it
  is an ambiguity rather than a duplicate.

A `now` earlier than the state's own is refused (`now-went-backwards`), which is
what makes that argument sound: if the window could run in reverse, "outside the
window" would stop meaning anything.

## The numbers, and where each came from

| Constant | Value | Why |
|---|---|---|
| `DEFAULT_WINDOW_MS` | 6 hours | F19's own number: a phone offline for six hours re-delivers everything it holds, and a shorter window would refuse the very redeliveries at-least-once delivery exists to make |
| `MIN_WINDOW_MS` / `MAX_WINDOW_MS` | 1 s / 7 days | a window that refuses ordinary latency, or one so long the cap could never be reached, is refused rather than clamped |
| `DEFAULT_MAX_SEEN` | 1024 | a bounded default an operator can move, not a measurement |
| `MIN_MAX_SEEN` / `MAX_MAX_SEEN` | 1 / 100 000 | the cap has a floor and a ceiling, both refusals |
| `DEFAULT_FUTURE_TOLERANCE_MS` | 5 minutes | ordinary skew and latency; beyond it `future-timestamp`. A future `at` is inside the window by definition (its age is negative), so an unbounded future would be an entry no window could evict — a memory lever a writer could pull |

## Fail closed, and each refusal is named

Every refusal carries a **distinct code**, so a caller can tell a replay from a
malformed write from a bug in its own caller. A refusal names the **field** at
fault and never the value it refused, so a refusal can be logged as it stands.

| Code | What it refuses |
|---|---|
| `not-an-object` | a state, an event or a ledger record that is `null`, an array, a primitive, or an instance with a prototype of its own |
| `accessor-property` | a field read through a getter — refused **without ever calling it**, because a getter can answer differently each time |
| `unknown-field` | a field this module does not name, on a state, an event or a record (the field set is the version) |
| `missing-field` | a required field that is absent or explicitly `undefined` |
| `unknown-state` | a `phase` that is not one of the four |
| `unknown-event` | a `type` that is not one of the four |
| `invalid-state` | a declared phase that disagrees with the ledger |
| `invalid-now` | a `now` (on the state **or** the event) that is not an integer in `1 .. 2^53-1`, `-0` included |
| `now-went-backwards` | a `now` earlier than the state's own |
| `invalid-window` | `windowMs` outside `MIN_WINDOW_MS .. MAX_WINDOW_MS` |
| `invalid-max-seen` | `maxSeen` outside `MIN_MAX_SEEN .. MAX_MAX_SEEN` |
| `invalid-future-tolerance` | `futureToleranceMs` outside `0 .. MAX_FUTURE_TOLERANCE_MS` |
| `invalid-seen` | `seen` that is not an array, or is longer than the state's own cap |
| `invalid-seen-entry` | a record with a field of the wrong shape, or an unknown field |
| `duplicate-seen-entry` | two records for the same `(sender, id)` |
| `unsorted-seen` | records not in the canonical order |
| `negative-zero` | a message whose `at` is `-0` — the schema accepts it (it *is* zero), and this module refuses it because it is the shape of a negated number, not a time |
| `zero-timestamp` | a message whose `at` is `0` — schema-valid (P0.2 allows any safe integer ≥ 0) and not a time this module will carry: the ledger's floor, the state's own `now` floor and D117's rule (a zero timestamp is the shape of an unset counter) all say a time is a **positive** integer. Admitting it would be worse than refusing it, because the record could not be read back |
| `replay` | a message older than the replay window |
| `future-timestamp` | a message whose `at` is further ahead than the future tolerance |
| `id-conflict` | a known `(sender, id)` carrying a different `at`, inside the window |
| `dedupe-full` | a delivery with no room in the ledger and nothing evictable |
| `mailbox-closed` | any event at all, once the mailbox is closed |

### The schema's codes, propagated verbatim

A message is P0.2's to validate, and this module calls `validateMessage` rather
than growing a second, weaker copy of the protocol. The schema's codes arrive
unchanged, with a `message.` prefix on the field so a caller can tell a bad
message from a bad state: `wrong-type`, `empty-string`, `bad-key`, `too-long`,
`bad-character`, `bad-timestamp`, and the three this module shares with it —
`not-an-object`, `unknown-field`, `missing-field`.

The prefix is the whole of this module's addition: a refusal is never rewritten,
never softened and never merged into a code of this module's own.

## What this module is *not*

- **Not the lease.** D110's consumer lease — a TTL and `already-claimed` — is
  P1.2's. This module has no opinion about who may act.
- **Not the consumer.** Reading a database, holding a socket and speaking are
  P4.1's. This module is a function over values.
- **Not the rules.** P2.1's template is what decides who may write a message
  and where; this module decides what a mailbox does with one.
- **Not a redactor, not the digest, not the URL validator.** P1.3's and P0.3's.
- **Not a store.** The state is a value the caller holds and hands back. This
  module never persists it, and never reads a clock to stamp it.

## Evidence, and how to reproduce it

```sh
cd shared && npm test                        # the whole package
shared/mailbox/mutation-proof.sh             # every guard in index.js, mutated one at a time
```

**Every guard in `index.js` is mutation-proved**, not merely covered: the script
disables one guard at a time — the plain-object check, the accessor check, the
unknown- and missing-field checks, the phase check, the ledger's shape,
duplicate and order rules, the monotonic-`now` check, the negative-zero and
zero-timestamp checks, the future bound, the window bound, the `(sender, id)` dedupe, the duplicate
branch that must not touch the record, the conflict branch, both halves of the
eviction predicate, the cap, the closed row of the dispatch table, the freeze
and the copy — runs the module's tests, and requires them to fail each time. It
restores the file with `git checkout` after each mutation and refuses to run
against an uncommitted module.

**"Killed" is only worth reading because of three things it does first**, since
otherwise it is a claim about a command that may never have run:

- it **finds a working runner or stops**: a working `node` and `npm` on PATH,
  else one lent by `mise` (`node@22`, the line `package.json`'s `engines`
  declares — this repository's base image keeps node off PATH, so the path to
  one is mise's to own, not this script's to hardcode), else exit 2 with a
  message instead of a count;
- the **positive control**: the suite must pass on the pristine module *with at
  least one test run*, which is what catches the empty-glob trap `node --test`
  sets — a missing test file exits 0 having run nothing, and would otherwise
  look exactly like a green suite; and
- the **negative control**: a mutation that must be caught, before any real one
  is trusted. If the suite cannot fail, the harness says so and exits 2 rather
  than reporting mutants it never measured.

Both controls were reproduced as failures while writing this: with the runner
removed (`PATH=/usr/bin:/bin`) the script refuses with exit 2 rather than
reporting mutants it never ran, and with `index.test.js` renamed the positive
control catches the empty glob — which is why there are two.

The run that is recorded here is **55 guards, 55 killed, 0 survived, 0 not
applied**, with the control killed first. One clause of the module is
deliberately *not* counted: `isPlainObject`'s `Array.isArray` check, which the
prototype check beside it subsumes (an array's prototype is neither
`Object.prototype` nor `null`). A mutation of it cannot be killed by any test,
so it is not claimed as a proved guard — the clause is kept for parity with the
schema's own `isPlainObject`, and the array case is proved by the prototype
mutation instead.

**One deliberate difference from `mailbox-schema/mutation-proof.sh`:** this
script's suite is this module's tests (`node --test "mailbox/*.test.js"` from
`shared/`), not the whole package's `npm test`. The reason is a real one and is
recorded rather than hidden: on the node line `package.json` declares, the
package suite has **one pre-existing failure that is not this module's** —
`shared/url/index.test.js` refuses `https://xn--github-9d0c.com/a` as
`not-a-url` where the test expects `punycode-host` (P0.3's module, untouched
here). A package-wide suite would make this harness's positive control refuse to
run at all, and treating that failure as "known" would let a real regression hide
behind it. Narrowing the suite to the module under test measures the thing the
mutations are in, and the pre-existing failure is reported in the pull request
instead.

## Honest limits

- **A Proxy can lie, and a revoked one throws.** A plain-object check and
  own-descriptor reads stop a getter and a prototype chain; they cannot stop a
  Proxy that answers `getOwnPropertyDescriptor` dishonestly. Nothing in
  JavaScript can, and this module does not pretend otherwise.
- **The ledger's integrity is the caller's.** A caller that drops a record, or
  hands back a state with one missing, re-admits a replay for as long as the
  window lasts — the window is the bound on that mistake, and it is why the
  window exists as a rule rather than as an optimisation.
- **The ledger records `at`, not the text.** Two deliveries of the same
  `(sender, id, at)` with *different* text are indistinguishable here and the
  second is answered as a duplicate. Detecting that would mean holding the text
  (a second copy of the mailbox, and D118's redaction applied twice), and the
  integrity of a message's contents is D113b's MAC — the phone's job, not this
  table's.
- **`nothing-to-act` is not a promise that the act happened.** This module
  records that a delivery was acted on; whether the action succeeded is the
  consumer's to know (P4.1), and it is what the ack is for.
- **No CI job runs these tests yet.** P4.3 is that job and it is **owner-made**
  (the plan's Global Constraint 8: no agent edits `.github/workflows/`), so
  until it exists these tests run only where someone runs them — the gap
  [the package's README](../README.md) already records.

Where the reasoning lives: [implementation-plan.md](../../docs/mobile/implementation-plan.md)
(P1.1), [reconciliation.md](../../docs/mobile/reconciliation.md) (D110, D111,
D113/D113b, D117, D118, D126, F19, F21), and [the schema](../mailbox-schema/README.md)
this module speaks.
