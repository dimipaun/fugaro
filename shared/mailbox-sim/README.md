# `mailbox-sim/` — the deterministic mailbox simulator (P1.5)

A harness, not a rule. It puts the three merged pure modules together — the message
schema (`../mailbox-schema/`), the mailbox state machine (`../mailbox/`) and the consumer
lease (`../lease/`) — behind an in-memory model of the store and a wire that can duplicate,
reorder, drop and delay a delivery, so that the end-to-end mailbox test runs with **no
runtime, no network and no clock**, and so that the properties those modules promise can be
checked over many seeded runs instead of asserted in prose. The plan's P1.5 exists because
of check 80: this is what makes it a formality rather than a discovery.

```js
import { createSimulator, scheduleFromSeed } from './index.js';

const sim = createSimulator({ promptTtlMs: 72 * 3600 * 1000 });
sim.consumer('consumer-1');
sim.phone.writeInbox({ id: 'm1', text: 'turn on the kitchen light' });
sim.run({ ticks: 4, intervalMs: 1000 });
sim.report();   // the deliveries, the actings, the acks, the store, the phone
```

## What it composes, and what it must never re-implement

Every rule belongs to the module that owns it, and the simulator calls it rather than
copying it:

| the question | who answers it | how the simulator asks |
| --- | --- | --- |
| is this value a message? | `mailbox-schema` | `validateMessage`, on every write, including its own replies |
| is this delivery new, late, a duplicate, a replay? | `mailbox` | `step(state, { type: 'deliver', now, message, kind })` |
| what work is next, and is it stale? | `mailbox` | `step(state, { type: 'act', now })` |
| may this consumer act at all? | `lease` | `takeLease`, and `readLease` when the store is built |
| may the state be written? | the caller's duty (`../lease/README.md`) | `casCommit`, against a generation |

A refusal the modules make is propagated with **their** code, never renamed: a store built
with a 31-day prompt TTL is refused `invalid-prompt-ttl` by `mailbox.initialState`, and the
simulator hands that code back untouched. The same holds for the three values the modules
own rather than the harness — the prompt TTL, the start time and the lease TTL. This module
validates only what is its own (the field set of its options, its own schedule, its own
actor ids) and passes those three through, so a caller sees `invalid-now` from
`initialState` and `bad-ttl` from `readLease` rather than a second opinion from here. The
mutation proof has no mutation for them for that reason, and says so.

## Two rules the modules do not expose, and what the harness does instead

The plan needs two things the merged modules have no field for, and the honest answer is
to say so rather than to grow a rule here:

- **There is no `kind` field in the harness's own messages.** P6.3 names one in the schema,
  and P6.3 is proposed and **NOT APPROVED**; this harness was written against P0.2's field
  set and does not grow a rule the plan has not approved. The kind
  therefore travels **beside** the message, in the store's own record
  (`store.kinds`, written by `phone.writeInbox` and read by the consumer), and never inside
  the message. A test asserts that no message the harness writes carries a field the
  schema does not name.
- **Nothing links a reply to what it answers.** P6.3's `reply_to` is the same proposal. The
  harness uses a naming convention — `replyId(id) === 'r-' + id` — and says so. This is why
  the mutation proof has no mutation for `replyId`: the tests build their expectation by
  calling the same exported function, so a mutation there would move the expectation with
  the behaviour, and no test could catch it.

Both are the harness's own inventions and are named here rather than hidden. P6.3's field
set is what replaces them for a **signed** write, and this harness does not sign: see the
placeholder tag below.

## The model

**The clock is injected.** `manualClock(startMs)` by default: a number the caller moves with
`sim.advance(ms)`, `sim.run({ ticks, intervalMs })` or `sim.tick()`. Nothing here reads the
machine's time, so a simulated hour costs nothing and two runs of one schedule take the same
simulated time. A test patches `Date.now` and `Math.random`, runs a whole seeded sweep, and
asserts neither was touched.

**The randomness is a seeded PRNG written in this file** (`makeRng`, a mulberry32-style
generator over 32-bit integer arithmetic, so its sequence is identical in every engine).
There is no `Math.random` anywhere, and no `node:crypto`: nothing in this task's scope needs
cryptographic determinism, and a simulator must not hold a key. The tag the schema now
requires is written as `PLACEHOLDER_MAC` — the shape of a tag, never its meaning — and
nothing here computes or verifies one.

**The store** holds the account's inbox, its outbox, the consumer's heartbeat, the consumer
lease and the mailbox's ledger. The three message families are plain maps, written by
whoever writes them and keyed by message id, which is what makes a duplicated write
idempotent — and an inbox write is a **merge, not a replacement**: it never clears the `acked`
flag a consumer has already written into that record. That flag is not the phone's field, and
the phone re-sends its own message until the outbox answers it, so a retry can land after the
ack. A replacement would leave the store saying "unacked" about a message the ledger has
already consumed, and nothing would repair it — the ledger's ack step only acks a record it
has acted on — so the message would be redelivered for ever.

The **lease and the ledger are one persisted value**, replaced only by
compare-and-swap: a consumer reads a generation, decides against that snapshot, and commits
only if the generation has not moved. That is the caller's duty the lease's README states
("two consumers that both read the same state and both get `ok: true` from `takeLease` must
not both be able to write"), and it is the fence that makes at-most-once acting true when two
consumers race — not the ledger alone. A test proves both halves of it: two reads of one
empty lease both succeed, and the second commit is refused `cas-conflict` with nothing
written.

## The fault model, which is data

A **schedule** is the fault model, and it is a value: a seed, an op vocabulary, a per-actor
clock skew, a delay bound, and an explicit per-frame fault list. It is either **explicit**
(a non-empty `faults` list, `rate` 0) or **generated** (`rate` and `ops`), never both — a
frame's fault has one source, or a run would have two answers to what happened to it.

| fault | what it does |
| --- | --- |
| `duplicate` | the frame is delivered twice |
| `reorder` | the frame arrives after the frame beside it (in the same drain; a frame with no unreordered neighbour due with it arrives in place, and the fault is still recorded) |
| `drop` | the frame never arrives |
| `delay` | the frame arrives later, its `at` unchanged |

Clock skew is not in that list, because it is not a property of one frame: it is a property
of an actor's clock (`schedule.clocks`), and the same writer stamps every message it writes
with the same disagreement. `scheduleFromSeed` draws each actor's skew within
`±MAX_CLOCK_SKEW` — the tolerance the lease's rule and the mailbox's future bound are both
stated in. A hand-written schedule may name a larger skew on purpose (up to `MAX_SKEW_MS`,
one day), because that is how the boundary is tested: one millisecond beyond the tolerance
the mailbox refuses the delivery `future-timestamp`.

**Replayable from its seed.** `faultFor(schedule, frame)` is a **pure function of the
schedule and the frame's number** — an explicit schedule looks the frame up, a generated one
derives the decision from `(seed, frame)` — so asking for frame 9 before frame 3 gives frame
9 the same answer, and a run can be replayed by asking the same questions in the same order.
A run therefore records every fault it applied, and `sim.schedule()` hands back the
**expanded** schedule: the same seed with every drawn fault written out by frame number.
`sim.scheduleText()` is that as one line of JSON, and it is what a failing property prints,
beside the seed. It is **canonical** — fixed key order, `clocks` and `faults` sorted — so two
schedules that differ only in how they were written print the same line, which is what makes
the printed line a replay of the run that failed rather than a second schedule.
`parseScheduleText` reads it back, and a test replays a run from the seed and from the printed
text and asserts the two reports are equal.

## What the actors do

- **The phone** writes its own inbox messages, re-sends one until the outbox answers it, and
  reads (speaks) every reply it can see. It writes no other path.
- **A consumer** takes the lease; if it does not hold it, it writes nothing and acts on
  nothing. If it holds it, it writes the heartbeat, delivers every unacked inbox message to
  the ledger, acts on every un-acted delivery, oldest first (the ledger's own order), and
  then writes what it owes: a reply for
  every record it **performed** on, and the ack for every record the ledger holds as `acted`.
  The ledger's transition is committed **before** the action is performed, so a lost race
  performs nothing.

  Those two sets are not the same set, and the difference is the point. A stale record is
  `acted` in the ledger and was **not** performed, and the ledger cannot tell the two apart —
  the mailbox moves both to `acted` and reports the difference in the outcome alone, its
  README leaving "what late means" to the consumer. So a holder cannot re-derive "was this
  performed?" from the store, and the reply obligation lives with the consumer that performed,
  carried until the store shows the reply. Writing a reply for every consumed record instead
  would invent an answer to a prompt this harness has no rule for (below). The **ack** is the
  other half, and it *is* the ledger's to say: an `acted` record is one an ack is still owed
  for, so any holder owes it — for a stale record exactly as for a performed one, because the
  ack says the delivery was consumed, not that the work was done.
- **A retry re-sends the same message**, `at` included. That is the caller's duty the
  mailbox's README states ("never retry an id with a new `at`"), and it is why a duplicated
  or delayed frame is harmless: the second arrival is the same message, and the ledger
  answers it as a duplicate.

The harness's own inventions, stated rather than hidden: the retry-until-observed duty, the
`r-` reply-id convention, the out-of-band `kind`, and the ack being **one** write by the
consumer. The plan's P1.5 says "a consumer leases, acts, acks and replies"; **D113's
phone-written ack is P3.6's, and this simulator does not model two ack writers.** A consumer
writes the ack into the inbox, and the ledger's own ack step waits until the store shows it —
which is what makes a dropped ack a redelivery rather than a lost message.

## The properties the tests check

Over 120 seeded schedules, each with three messages, two consumers and twelve passes:

- **At most once acting per `(sender, id)`**, under any duplicate, reorder or drop schedule —
  counted from every act the harness performed, `stale-pending` included, since a stale
  record is consumed exactly once too.
- **At least once delivery**: every message the phone wrote was delivered at least once —
  accepted or answered as a duplicate, an outcome the *ledger* gave rather than a count the
  harness kept — and it is in the ledger, which is where "delivered" is recorded. A separate
  pass-by-pass test pins the delivery loop itself: a holder delivers every unacked message it
  can see in one pass, and never one that is already acked.
- **One lease holder at a time**: exactly one consumer wins the commit in a pass, and only a
  winner acts; no pass has two consumers acting in it. Under a hand-written schedule whose
  skews make a takeover available every pass, the property still holds — a takeover does not
  re-act a message, because the ledger is the mailbox's and not the consumer's.
- **At most one ledger ack per record**, and only for a record the store shows acked: every
  ack the ledger gives answers `acked`, never twice for one id.
- **The lease's skew rule, through the harness**: a lease stamped ahead of the reader by at
  most `MAX_CLOCK_SKEW` is held; one millisecond more and it is treated as expired and taken
  over; a lease stamped *behind* the reader by the same amount is held until its TTL, which
  pins the direction of the comparison as well as its bound.
- **The safe path for an expired prompt**: delivered after its TTL it is refused `expired`
  and never delivered at all; delivered in time and acted on too late the ledger consumes it
  exactly once and answers `stale-pending`, and the harness performs **nothing** with it — no
  reply, and the store is settled (every write the consumer owed has landed) before that is
  asserted, because a half-flushed wire is not the run's outcome. The ack is still written:
  the ledger holds the record as `acted`, and an `acted` record is one an ack is owed for.
  The phone is left waiting, because what a late prompt *delivers* is P6.5's and P6.7's rule,
  and those are proposed and **NOT APPROVED**, so this harness invents none.
- **A retry that lands after the ack does not clear it**: the store's ack survives the phone's
  own re-send, so the store and the ledger cannot come to disagree about whether a message was
  ever consumed.
- **A stale `at` beyond the window is refused** `replay` for a command, and nothing is acted
  on.
- And a **positive control**, so none of the above can pass vacuously: the seeded schedules
  really do fault, really do drop frames, and really do act.

## What it deliberately does not model

- **The MAC.** The harness writes the tag's **shape** and not its meaning: every message it
  writes carries `PLACEHOLDER_MAC`, an obviously fake 64-hex string, and nothing here
  computes a tag, verifies one, or holds a key — a simulator with the per-pairing key of
  D171 would be a simulator that could forge. P6.3's `macOf` and `verifyMac` live in
  `../mailbox-schema/mac.js` — Node-only, and deliberately not imported by that module's
  entry point, which imports nothing so the phone can load the validator — and the
  "fixed function of the MAC-verified message" that
  `../mailbox/README.md` requires `kind` to be is still the consumer's: the kind comes out
  of band here instead, and says so above.
- **The rules.** `database.rules.json`, the tree's `$other` denial and the write permissions
  are P2.1's. This is a simulator of the *modules*, not of the deployment: it models what the
  pure logic decides, and nothing about who is allowed to write what.
- **A real clock, a real network, a real store.** There is no wall clock, no socket, no
  filesystem and no third-party dependency — `node:test` and `node:assert` only. Time is a
  number the caller moves; the wire is an array of frames; the store is a map.
- **The phone's own ack (D113), the console's consumer, the mobile app.** Those are P3.6,
  P4.1 and phase 3's.

## Running it

```sh
cd shared && npm test                    # the package's tests, this module's included
shared/mailbox-sim/mutation-proof.sh     # the module's own guards, mutation-proved
```

`mutation-proof.sh` restores `index.js` with `git checkout` after each mutation, so it
refuses to run against an uncommitted module. It runs this module's tests rather than the
whole package's `npm test`, for the reason `../mailbox/mutation-proof.sh` gives: on the node
line `package.json` declares (22), `../url/index.test.js` has one pre-existing failure that
is not this module's, and a package-wide suite would make the positive control refuse to run
at all.

Where the reasoning lives: [implementation-plan.md](../../docs/mobile/implementation-plan.md)
(P1.5, and the properties its verification names), [mailbox/README.md](../mailbox/README.md)
(the guarantees and the caller duties this harness is written to honour),
[lease/README.md](../lease/README.md) (the compare-and-swap duty).
