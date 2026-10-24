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

// promptTtlMs is D118's mailbox TTL, which has no number this module could default to.
let { state } = initialState({ now: 1_700_000_000_000, promptTtlMs: 24 * 60 * 60 * 1_000 });

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
| `deliver` | `now`, `message`, optional `kind` | `accepted`, `duplicate`, or a refusal |
| `act` | `now` | `acted`, `stale-pending`, or `nothing-to-act` |
| `ack` | `now` | `acked`, or `nothing-to-ack` |
| `close` | `now` | `closed` |

| Outcome | Means |
|---|---|
| `accepted` | the delivery is new; it is now in the ledger |
| `duplicate` | this `(sender, id)` is already known; answered, **not** acted on again |
| `acted` | the oldest un-acted delivery was acted on |
| `stale-pending` | the oldest un-acted delivery was acted on, and its kind's window had already passed it: the work is late, and the consumer decides what late means (for a prompt, the owner's ruling says it has expired) — it is surfaced, never dropped |
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
kind, status }` — in a **canonical order** (`at`, then `sender`, then `id`, by code
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

The cap (`maxSeen`, default `DEFAULT_MAX_SEEN`) and the per-sender quota
(`maxPerSender`) bound what the bookkeeping can hold. The rules that keep them
from becoming a hole in the fence:

- **nothing that is not acked is ever dropped**, at any age, to make room or
  for any other reason. An `accepted` record is work not yet done and an
  `acted` one is work an ack is still owed for: dropping the first loses the
  work silently, and dropping either makes room for the same id to be acted on
  twice (the round-1 bug the independent review found: deliver x, act, deliver
  x a window later with a fresh `at` — accepted, and acted on again);
- a record is **evicted by age only when it is `acked` and strictly older than
  the horizon**, `max(windowMs, promptTtlMs)` — one horizon for every record,
  whatever its kind (at exactly the horizon the message is still deliverable,
  so its record must still be there). A kind-dependent horizon was a hole the
  second review found: a command evicted after 6 hours came back by claiming
  to be a prompt (inside the prompt's 72-hour TTL) and was acted on twice, and
  a prompt with a 1-hour TTL came back as a command;
- **nothing inside the horizon is ever forgotten** — acked or not, there is no
  compaction and no early eviction (a design ruling of round 4, replacing round
  3's compaction, which the third review showed could push the watermark past
  `now`, let one sender's flood censor another, and act on the same id twice
  inside the window). **So inside the horizon the same `(sender, id)` is never
  acted on twice**: its record is there to call a redelivery a `duplicate` or
  an `id-conflict`;
- **the watermark** (`evictedThrough`, a state field the module sets): every
  horizon eviction raises it to the evicted record's `at`, and nothing else
  moves it. A delivery whose `at` is at or before it is refused (`replay`, or
  `expired` for a prompt) — after the ledger lookup, so a held record is still
  a duplicate or a conflict. Because it moves only by horizon age it is always
  at or before `now - horizon` (or 0), which `step` checks on every state it
  reads (`invalid-evicted-through`): it can never pass `now`, and no sender's
  traffic can move it, so one sender cannot censor another through it. A state
  handed back with a *wider* window or TTL than its watermark allows is
  refused rather than trusted (the second review's probe D);
- a record is **consulted whatever its age**: the same `(sender, id)` with the
  same `at` and kind is a `duplicate`; with a different `at` or kind it is
  `id-conflict` — the id is the message's identity, so a second message under
  it is an ambiguity, never a new delivery, while the ledger remembers the
  first;
- an `accepted` record the window has passed is kept, and `act` answers
  **`stale-pending`** for it instead of `acted`, so late work is surfaced
  rather than lost;
- **the quota counts every record a sender holds** inside the horizon —
  accepted, acted or acked. A sender at `maxPerSender` is **refused**
  (`sender-full`), so one sender can fill only its own share, never the whole
  mailbox. A full ledger is **refused** (`dedupe-full`). Neither evicts. The
  quota is a state field, default `max(1, floor(maxSeen / 4))`
  (`defaultMaxPerSender`).

**The lockout trade-off, stated rather than hidden.** With a quarter-of-the-cap
quota, four senders at their quota fill the ledger, and a fifth is refused
`dedupe-full` until records age past the horizon. The stall is **up to
`horizon + futureToleranceMs` after the record is acked**, not `horizon` after
it was written: eviction needs the record to be acked *and* strictly older than
the horizon, and a record whose `at` leads `now` by the future tolerance (5
minutes by default) reaches that age only 5 minutes later than its arrival — so
with a 72-hour prompt TTL a record dated `now + 5 min` is evicted 72 h 05 m
later, and its ack has to come first. **While the consumer does not ack, the
stall has no bound at all**: an un-acked record is never evicted, at any age, so
a mailbox whose consumer has stopped stays full until the consumer acts and
acks it, or an operator raises `maxSeen`. That is the price of never forgetting
a record inside the horizon. It is bounded by three things: **the quota only
means something if `sender` is tied to the writer's identity by P2.1's rules**
(a writer free to choose `sender` is as many senders as it likes); the
**recovery** is the consumer acting and acking and the records ageing past the
horizon; and an operator who expects more senders sets a **larger `maxSeen`**.
Nothing is recovered by a fresh `initialState`, which forgets the ledger and
the watermark and so reopens every replay still inside its window.

**The throughput ceiling the one horizon implies.** `maxSeen` is not a
per-window budget: a record leaves only by aging past the horizon
`max(windowMs, promptTtlMs)`, and with the defaults that is **`promptTtlMs` —
72 hours, not the command window's 6** — because one horizon covers both kinds,
so a *command* record is retained for 72 hours too. `maxSeen` is therefore a
budget **per 72 hours**, and P4.1 sizes it from the sustained volume it must
carry: the defaults (`maxSeen` 1024, quota `max(1, floor(maxSeen / 4))` = 256)
sustain about **14 deliveries per hour per mailbox** and about **3.5 per hour
per sender** (256 per 72 hours) before `dedupe-full` / `sender-full` refuse —
and only while the consumer keeps acking, since an un-acked record never leaves
at all (the stall above). A mailbox that must carry more has two levers and
they trade against each other: **a larger `maxSeen`** is a larger per-72-hour
budget and more ledger state for the caller to persist, and **a shorter
`promptTtlMs`** is fewer hours inside which a redelivery must still be
remembered — which shortens the retention of *commands* as well, because the
horizon is single. Sustained volume is `maxSeen / horizon`, so the second lever
moves it the other way round from the first — a **longer** `promptTtlMs` is a
**slower** mailbox, not a larger one: `MAX_PROMPT_TTL_MS` (30 days) is the
ceiling, and at a TTL that long the same `maxSeen` sustains `maxSeen` records
per 30 days — about **a tenth** of the volume the 72-hour default allows (the
default's 1024 records per 72 hours is ~10240 per 30 days, and the 30-day TTL
has ten times as long to hold the same 1024). P4.1 must size `maxSeen` for the
horizon it is actually configured with, not for the command window's 6 hours.

**The exact bound.** The ledger never holds more than `maxSeen` records
(refused on read, never grown on write), plus one number, the watermark; at
most `maxPerSender` of them belong to one sender.

## The kinds, and the owner's ruling on the window

**Owner ruling, 2026-10-17** (the replay window against D168): the six-hour
window applies to **commands** (cancel, merge, launch, confirm) unchanged; for
**prompts** the acceptance window is the prompt's own TTL — D118's mailbox
TTL — so a consumer offline longer than six hours but inside the TTL still
accepts a queued prompt. Prompts **do** expire at the TTL: the owner confirmed
the TTL holds, which supersedes D168's "never expire".

| Kind | Window | Older than the window |
|---|---|---|
| `command` | `windowMs` (default 6 hours) | `replay` |
| `prompt` | `promptTtlMs` (required; D118's TTL) | `expired` |

Acceptance is per kind; eviction is not. A record is evicted by age only past
the horizon `max(windowMs, promptTtlMs)`, so neither kind's record is evicted
while the other kind could still deliver it, and the watermark refuses it for
good once it is gone (`expired` for a prompt, `replay` for a command). The
per-sender quota counts both kinds together.

**Where the kind comes from.** P0.2's schema has no kind field (its `FIELDS`
are `id, sender, text, at, acked`), so today the kind travels on the `deliver`
event — the consumer's assertion about what it is delivering — and is written
into the ledger record. Absent, it is `command`, the stricter window. **P6.3**
(proposed, not approved) adds the prompt kind to the message itself (with its
options and reply link); then the event's `kind` goes and the kind is read from
the validated message.

**The caller's duty, until then: `kind` must be a fixed function of the
MAC-verified message.** Today the kind is the event's claim, and nothing in
the event is authenticated. The single horizon makes a *switched* kind
harmless to the replay guarantee (inside the horizon the record is kept
whatever kind a redelivery claims, so the same id is not acted on twice), but
the kind still chooses the acceptance window: a consumer that let a
writer pick it would let a command be judged by a prompt's TTL. P4.1 must
derive it from the message it has verified (D113b's MAC), never from anything
the writer can set beside it.

**Why `promptTtlMs` has no default.** D118 says the mailbox has a TTL "with a
stated window", and no document states the number. The consumer (P4.1) hands
in the TTL its sweeper enforces; this module refuses a missing one rather than
inventing it, and bounds it `MIN_WINDOW_MS .. MAX_PROMPT_TTL_MS` (30 days, a
ceiling on the ledger's memory, not a retention policy).

A `now` earlier than the state's own is refused (`now-went-backwards`), which is
what makes that argument sound: if the window could run in reverse, "outside the
window" would stop meaning anything. A `now` more than `MAX_FORWARD_JUMP_MS`
(366 days) ahead of the state's own is refused too (`now-jumped`): without it
one far-future `now` would freeze the mailbox until that time.

## The numbers, and where each came from

| Constant | Value | Why |
|---|---|---|
| `DEFAULT_WINDOW_MS` | 6 hours | F19's own number: a phone offline for six hours re-delivers everything it holds, and a shorter window would refuse the very redeliveries at-least-once delivery exists to make |
| `MIN_WINDOW_MS` / `MAX_WINDOW_MS` | 1 s / 7 days | a window that refuses ordinary latency, or one so long the cap could never be reached, is refused rather than clamped |
| `DEFAULT_MAX_SEEN` | 1024 | a bounded default an operator can move, not a measurement |
| `MIN_MAX_SEEN` / `MAX_MAX_SEEN` | 1 / 100 000 | the cap has a floor and a ceiling, both refusals |
| `SENDER_SHARE` / `defaultMaxPerSender` | 4 / `max(1, floor(maxSeen / 4))` | the per-sender quota's default; the field is validated `1 .. maxSeen` |
| `MAX_PROMPT_TTL_MS` | 30 days | the ceiling on `promptTtlMs` (required, no default: D118 states no number) |
| `TIME_MAX` | 2100-01-01T00:00:00Z | `shared/lease`'s `MAX_TIMESTAMP`, imported: the latest `now` or `at` this module reads |
| `MAX_FORWARD_JUMP_MS` | 366 days | how far one event's `now` may lead the state's; beyond it `now-jumped`. A consumer really offline longer starts from a new `initialState` — by then every acked record is past any horizon (at most 30 days) |
| `DEFAULT_FUTURE_TOLERANCE_MS` | 5 minutes | `shared/lease`'s `MAX_CLOCK_SKEW`, imported. Ordinary skew and latency; beyond it `future-timestamp`. A future `at` is inside the window by definition (its age is negative), so an unbounded future would be an entry no window could evict — a memory lever a writer could pull |

## Fail closed, and each refusal is named

Every refusal carries a **distinct code**, so a caller can tell a replay from a
malformed write from a bug in its own caller. A refusal names the **field** at
fault by a fixed path this module chose — `state`, `event`, `options`, `seen`,
`message`, or a field this module or the schema defines — and never the value
it refused or a key name the input chose (an unknown key `message.SECRET=…` is
reported as `message`), so a refusal can be logged as it stands.

**The shape differs from `shared/lease`'s, on purpose.** A lease refusal is
`{ ok: false, code, message }` with a human sentence; a mailbox refusal is
`{ ok: false, code, field }`, frozen, with a fixed path and no sentence. The
mailbox's caller branches on the code and logs the path; a sentence would be a
second, unversioned vocabulary. The time constants *are* shared (imported from
the lease), so the two modules cannot disagree about a timestamp.

| Code | What it refuses |
|---|---|
| `not-an-object` | a state, an event or a ledger record that is `null`, an array, a primitive, or an instance with a prototype of its own |
| `accessor-property` | a field read through a getter — refused **without ever calling it**, because a getter can answer differently each time |
| `unknown-field` | a field this module does not name, on a state, an event or a record (the field set is the version) |
| `missing-field` | a required field that is absent or explicitly `undefined` |
| `unknown-state` | a `phase` that is not one of the four |
| `unknown-event` | a `type` that is not one of the four |
| `invalid-state` | a declared phase that disagrees with the ledger |
| `invalid-now` | a `now` (on the state **or** the event) that is not an integer in `1 .. TIME_MAX`, `-0` included |
| `now-went-backwards` | a `now` earlier than the state's own |
| `now-jumped` | a `now` more than `MAX_FORWARD_JUMP_MS` ahead of the state's own |
| `invalid-evicted-through` | an `evictedThrough` watermark that is not an integer in `0 .. max(0, now - horizon)` — later than horizon eviction could have set it, which includes a state handed back with a window widened past its watermark |
| `internal-error` | an input that threw while being read (a revoked Proxy, a throwing trap); what was thrown is never echoed |
| `invalid-window` | `windowMs` outside `MIN_WINDOW_MS .. MAX_WINDOW_MS` |
| `invalid-max-seen` | `maxSeen` outside `MIN_MAX_SEEN .. MAX_MAX_SEEN` |
| `invalid-max-per-sender` | `maxPerSender` outside `1 .. maxSeen` |
| `invalid-prompt-ttl` | `promptTtlMs` outside `MIN_WINDOW_MS .. MAX_PROMPT_TTL_MS` |
| `unknown-kind` | a delivery's `kind` that is not `command` or `prompt` |
| `invalid-future-tolerance` | `futureToleranceMs` outside `0 .. MAX_FUTURE_TOLERANCE_MS` |
| `invalid-seen` | `seen` that is not a plain array (a subclass, an own `Symbol.iterator`, an extra property, a hole, an index accessor), or is longer than the state's own cap. It is read by index, never iterated, so an iterator cannot hide a record |
| `invalid-seen-entry` | a record with a field of the wrong shape (a kind outside the vocabulary, an `at` past `TIME_MAX`), a missing field, or an unknown field |
| `duplicate-seen-entry` | two records for the same `(sender, id)` |
| `unsorted-seen` | records not in the canonical order |
| `negative-zero` | a message whose `at` is `-0` — the schema accepts it (it *is* zero), and this module refuses it because it is the shape of a negated number, not a time |
| `zero-timestamp` | a message whose `at` is `0` — schema-valid (P0.2 allows any safe integer ≥ 0) and not a time this module will carry: the ledger's floor, the state's own `now` floor and D117's rule (a zero timestamp is the shape of an unset counter) all say a time is a **positive** integer. Admitting it would be worse than refusing it, because the record could not be read back |
| `replay` | a command older than the replay window (`windowMs`), or at or before the watermark |
| `expired` | a prompt older than its TTL (`promptTtlMs`), or at or before the watermark |
| `future-timestamp` | a message whose `at` is further ahead than the future tolerance, or past `TIME_MAX` |
| `id-conflict` | a known `(sender, id)` — whatever its record's age — carrying a different `at` or kind |
| `dedupe-full` | a delivery with no room in the ledger and nothing past the horizon to evict |
| `sender-full` | a delivery whose sender already holds `maxPerSender` records (acked or not) |
| `mailbox-closed` | any event at all, once the mailbox is closed |

### The schema's codes, propagated verbatim

A message is P0.2's to validate, and this module calls `validateMessage` rather
than growing a second, weaker copy of the protocol. The schema's codes arrive
unchanged, with a `message.` prefix on the field so a caller can tell a bad
message from a bad state: `wrong-type`, `empty-string`, `bad-key`, `too-long`,
`bad-character`, `bad-timestamp`, and the three this module shares with it —
`not-an-object`, `unknown-field`, `missing-field`.

P6.3 added a `mac` to every message and the four message kinds to the schema,
so nine of the schema's refusal codes reach the mailbox through a delivery,
unchanged and unprefixed but for the field: `too-many` and `too-few` (a
prompt's options and a heartbeat's agents are bounded lists), `unknown-verb`
(an action record's verb), `bad-target` and `bad-sha` (an action record that
could not be executed as it is written), `bad-mac` (a tag that is not a
32-byte HMAC-SHA-256 in lowercase hex), `duplicate-entry` (two options, or two
agents, that a spoken "option two" or "talk to <agent>" could not tell apart),
and `field-not-allowed` (a field on a kind of message that does not carry it) —
plus `unknown-kind` below. A delivery's refusal set is the schema's whole table
(`Object.values(REFUSALS)`), so a code added there reaches here without an edit
in this module.
`unknown-kind` is spelled the same in both modules and means a different
thing in each: the schema's is a message's `kind`, this module's is a
delivery's. They are told apart by the field, exactly as the three shared
codes above are.

A `mac` is checkable only with the key, and this module has none: it validates
the tag's SHAPE, never its value, and `verifyMac` is the consumer's question
(P4.1, D171). A delivery whose tag does not verify is one the consumer must
refuse before it calls this module at all — see `shared/mailbox-schema`'s
vectors for what "verifies" means byte for byte.

The prefix is the whole of this module's addition: a refusal is never rewritten,
never softened and never merged into a code of this module's own — except that
an unknown key is reported as `message`, never by its name (see above).

**Why the three shared codes are not renamed.** `not-an-object`,
`unknown-field` and `missing-field` are spelled the same in both modules, and
are told apart by the **field**, not the code: a message's refusal always
carries `message` or `message.<field>`, and this module's own never does.
Renaming them into a mailbox namespace would rewrite the schema's verdict,
which is the one thing propagation promises not to do.

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
zero-timestamp checks, the timestamp ceiling, the future bound, each kind's
window and its inclusive edge, `expired` against `replay`, the `(sender, id)`
dedupe and its consultation at every age, the duplicate branch that must not
touch the record, the conflict branch (`at` and kind), the cap, the per-sender
quota and what it counts, the eviction predicate's acked-only rule, its
boundary and its per-kind window, `stale-pending` and its boundary, the
ledger's index-only reader, the no-echo rule for key names, the two corrected
cells of the declared table, the closed row of the dispatch table, the freezes
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

The run that is recorded here (round 4, node 26 on PATH) is **108 guards, 108
killed, 0 survived, 0 not applied**, with the control killed first. Round 3
added the eviction horizon, the watermark, the forward-jump bound and the two
never-throw guards; round 4 removed compaction and the in-flight-only quota
with their guards, and added the watermark's `now - horizon` bound and the
quota's counting of acked records. One clause of the module is
deliberately *not* counted: `isPlainObject`'s `Array.isArray` check, which the
prototype check beside it subsumes (an array's prototype is neither
`Object.prototype` nor `null`). A mutation of it cannot be killed by any test,
so it is not claimed as a proved guard — the clause is kept for parity with the
schema's own `isPlainObject`, and the array case is proved by the prototype
mutation instead.

**The exhaustive table test does not trust the module's table.** It writes the
EXPECTED table by hand, asserts the module's `TRANSITIONS` equals it, and
drives 30 literal ledger compositions (empty, pending-only, acted-only,
acked-only, mixed, at the cap in each phase with and without acked records, a sender at its quota in each
phase, stale records of every status, prompts inside and past their TTL, future-dated acked records alone and in a
full ledger, and ledgers whose watermark is already set) in
both an open and a closed phase through every event — then checks the other
direction too: every outcome and next phase a cell declares is actually
reached, and every one of this module's delivery refusals is produced in every
open phase. Round 1's version sampled one state per phase and compared the code
against the module's own table, which is how `pending x ack` answering `acked`
slipped through.

**One deliberate difference from `mailbox-schema/mutation-proof.sh`:** this
script's suite is this module's tests (`node --test "mailbox/*.test.js"` from
`shared/`), not the whole package's `npm test`, because the module's own tests
are the ones that measure the thing the mutations are in, and another module's
failure can neither stop this harness's positive control nor hide a regression
here. That reason was first a concrete one: on the node line `package.json`
declares, the package suite had **one failure that was not this module's** —
`shared/url/index.test.js`'s sweep used `https://xn--github-9d0c.com/a`, which is
not valid punycode, so Node 22's parser threw and the module answered
`not-a-url` where the test expects `punycode-host`. That fixture was replaced on
2026-10-24 with the valid `https://xn--gthub-n2e.com/a`, and the package suite
passes on Node 22 and Node 26.

## Honest limits

- **The realm is trusted.** The ledger is read by index so that an array's
  *own* iterator cannot hide a record; a realm whose built-ins are replaced
  (`Array.prototype[Symbol.iterator]`, `Object.getOwnPropertyDescriptor`) is
  not defended against, and nothing pure could be.
- **After the horizon, an id with a later `at` is a new message.** Inside the
  horizon the same `(sender, id)` is never acted on twice. Once its record is
  acked *and* past the horizon it is evicted, and a message under the same
  `(sender, id)` with a **later** `at` is then a new message — that is the
  protocol's semantics, not a hole: the original's redelivery (same `at`) is
  still refused by the window and the watermark. **The caller's duty: the
  phone must never retry an id with a new `at`.** A retry re-sends the same
  message, `at` included; a new `at` is a new message and needs a new id.
- **Persisting the whole state is the caller's duty, the watermark
  included.** The machine is a function of the state it is handed. A caller
  that hands back a lower watermark (or 0), or a ledger with a record missing,
  re-admits what was evicted or forgotten. `step` refuses a watermark that is
  too *high* for the state; it cannot tell a lowered one from an honest one.
- **A `now` that leaps is bounded.** A `now` up to `MAX_FORWARD_JUMP_MS`
  ahead is accepted, and every later `now` must be at least that. The caller's
  `now` is the server's (D117), so a leap is a caller bug; it can freeze the
  mailbox for at most that long, never until `TIME_MAX`.
- **A Proxy can lie.** A plain-object check and own-descriptor reads stop a
  getter and a prototype chain; they cannot stop a Proxy that answers
  `getOwnPropertyDescriptor` dishonestly. Nothing in JavaScript can, and this
  module does not pretend otherwise. A Proxy that **throws** (or a revoked
  one) is caught: `step` and `initialState` answer `internal-error` and never
  throw.
- **The ledger's integrity is the caller's.** A caller that drops a record, or
  hands back a state with one missing, re-admits a replay for as long as the
  window lasts — the window is the bound on that mistake, and it is why the
  window exists as a rule rather than as an optimisation.
- **`kind` is the caller's to derive from the MAC-verified message** (see
  "The caller's duty" above); today it is the event's unauthenticated claim.
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
