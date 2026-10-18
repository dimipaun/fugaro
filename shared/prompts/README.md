# `shared/prompts` — the prompt queue and the interrupt policy

Task **P6.5** of [the mobile plan](../../docs/mobile/implementation-plan.md), decided by **D168 as
amended on 2026-10-17**, **D173**, **D160**, **D127** and **D118**, with the spec's §3.2 (queueing
policy), §3.4 (reminder escalation, the pending cap, the app commands) and §3.6 (the windows) read
as the owner's ruling. It is two pure answers over the same inputs — what a consumer is holding,
and whether anything may be spoken over the user — built on the merged `shared/` modules: P6.3's
schema, P1.1's kinds and windows, P1.3's severity ladder and P1.4's presence states.

Nothing here reads a clock, a file, a database, a network or a random source, and it imports
nothing outside `shared/` and no `node:` builtin: every entry point takes its `now`.

```js
import {
  initialQueue, enqueue, expire, answer, announce, pending, pendingAcross,
  announcement, evaluateInterrupt, SWITCH, INTERRUPT, OUTCOME,
} from './index.js';

let { queue } = initialQueue({ now: serverNow, account: 'belong' });   // one installation

// The agent asks; the prompt queues rather than interrupting (§3.2's default).
queue = enqueue(queue, { now, prompt, destructive: false, safe: 'hold' }).queue;

// What's pending? — per installation, escalated first (§3.4, P6.9's list).
const listed = pending(queue, { now });

// A natural pause: the count across accounts, and the questions due to be read.
const spoke = announcement([queue, otherQueue], { now });
// spoke.text === 'Two questions waiting'   spoke.count === 2   spoke.due = [...]

// The phone's switch, the digest, the presence rule and the caps' recomputation.
const policy = evaluateInterrupt({ queue, digest, presence, caps, switch: SWITCH.DEFAULT, now });
```

## Queue by default, and what the queue holds

**Queue, do not interrupt** is the spec's own default (§3.2), and this module is the only thing in
the tree that holds a question for later — Voice D48 dropped "awaiting user input" for want of a
producer (G7), and the merged mailbox modules decide what a message *is* rather than keeping one.

A queue is one consumer's, for one installation (`account`), because the cap is per consumer and
"what's pending" is listed per installation. It holds entries of `{ prompt, destructive, safe,
announcedAt }`:

- `prompt` is a P6.3 message of kind `prompt`, re-validated by `validateMessage` on every read — a
  queue is a value a caller hands back, and this module will not answer from one the schema would
  refuse;
- `destructive` and `safe` are **local**: they say whether the prompt's answer is a confirmation
  (§3.6, D169) and what its non-destructive answer is. They are held beside the message and never
  in it, and the schema is what makes that structural rather than a convention: `validateMessage`
  refuses any field it does not name, so a classification cannot reach the bytes
  `canonicalBytes` covers, and it cannot reach the database. D127's reasoning — per-device state
  stays on the device — is the same argument;
- `announcedAt` is the phone's own record of when the user last **heard** the question, `null`
  until they have. It is local for the same reason, and it is what makes a redelivery of a *held*
  prompt harmless: a duplicate is answered from the queue it is already in, so the announcement
  stands and the agent cannot make the phone read the same outstanding question out twice. It is
  **not** a memory of answered questions: answering settles a prompt and drops its entry, so if
  the same id is delivered again afterwards it is a fresh question, queued unannounced and spoken
  at the next pause. Remembering that an id has been answered is **P6.7's ledger** — the same
  division of labour P1.1's dedupe ledger draws for delivery — and this module holds what is
  outstanding and nothing else. `index.test.js` pins both halves, because the difference is the
  kind of thing a reader would otherwise assume away.

The prompts are held in a **canonical order** (`at`, then the message id), so two consumers that
received the same prompts in different orders hold the same queue, exactly as P1.1's ledger is
ordered. A delivery is one of four outcomes: `queued`, `duplicate` (same id, sender and `at`),
`answered`, or `expired`.

## Expiry is a pure function, and it yields an answer (D168 as amended)

The owner first answered "never expire" and amended it the same day: a prompt's lifetime is
**D118's mailbox TTL**, which the owner set to **72 hours** on 2026-10-17 (O5) — this module's
default, `PROMPT_TTL_MS`, inside P1.1's 30-day ceiling.

```
expired(prompt, now)  ==  now - prompt.at > promptTtlMs
```

A function of the prompt's own `at`, the TTL and the caller's clock — **not** of when the
owner-run sweeper last ran, and not of the phone's own clock (D117: the server's timestamp is the
authority). Expiry **yields** something rather than dropping a question: the prompt's
non-destructive answer, or a **stop** when it has none (`EXPIRED_REPLY`, "no answer, expired").
Delivering that answer exactly once is the consumer's (P6.7), ledger-protected; this module decides
only what it is.

The boundary is `>` and is deliberately the same boundary P1.1 draws: a prompt of age exactly the
TTL is still alive here and still accepted by `step`. `index.test.js` asserts that agreement by
running P1.1 rather than by restating the arithmetic, which is what keeps the two from drifting —
this module's canonical order, its `now-went-backwards` refusal and its `future-timestamp` bound
are P1.1's for the same reason.

## Two windows, and which one an answer takes (D173, §3.6)

The owner ruled that the **six-hour replay window applies to commands** and that **a prompt's
answer takes the prompt's TTL** — a consumer offline longer than six hours but inside the TTL still
accepts a queued prompt — **except** that an answer to a **destructive** prompt is a confirmation
and takes the command window, because a confirmation must be fresh. Two consequences worth naming:

- The prompt's own life is judged from the **prompt's** `at`; a confirmation is judged from the
  **answer's** `at`. A prompt three days old confirmed a minute ago is accepted; the same prompt
  confirmed seven hours after the tap is not.
- This module does not re-implement either window: `acceptanceKind(destructive)` returns P1.1's own
  kind (`prompt` for a queued prompt, `command` for a confirmation) and the same kind is what
  P6.7's consumer passes to P1.1's `deliver`, so the two ends cannot pick different windows. The
  **refusal** follows the same mapping, and for the same reason: an answer older than the window
  its kind is judged by is refused with the code P1.1 answers for that kind — `expired` for a
  queued prompt's answer, `replay` for a confirmation — so this module never tells the caller a
  delivery is a replay that P1.1 will call expired, or the other way round.

An answer to a prompt that has already expired is refused `expired`, **with its expiry attached**:
the user's late answer is not what is handed over. An answer to an expired question is an answer to
a changed world, and D168's default is the non-destructive option. The other way to reach that
code is the answer's own age: one written before the prompt was, and so older than the TTL, while
the prompt is alive — refused `expired` too, with **no** expiry, because nothing has died. The two
are told apart by `field` (`prompt.at` against `answer.at`) and by the expiry's presence.

## Reminder escalation, and the pending cap (§3.4)

Both bounds were kept when D168 was amended, and they bound different things.

**Escalation bounds attention.** A waiting prompt is read out at the next natural pause, then
re-announced after **one hour** and once a **day** after that, and it is listed first in "what's
pending". The schedule is anchored to the prompt's own `at` — `reminderStage(at, now)` is a pure
function of the clock — while whether it has been **spoken** is the entry's `announcedAt`. An
announcement made late counts for the reminder it was late for, so a stage is never spoken twice.

**The cap bounds storage.** One consumer may hold `maxPending` prompts (16 by default, an
operator-movable number rather than a measurement). The prompt past the cap is **refused, never
evicted** — dropping a question to make room would answer nothing and lose the one that was dropped
— and the refusal carries D168's defined answer to the orchestrator, `QUEUE_FULL_REPLY` ("queue
full"), because the agent is the party that would otherwise wait for ever.

## The interruption table (D160, D127)

D160 fixed the set of events that may interrupt; everything else queues. D127's switch is **local**
— never written to the database — and it can only narrow the set or widen it to "any prompt":

| Event | `narrow` | `default` | `widen` |
|---|---|---|---|
| kill switch set (P1.3's ladder's top rung) | **kill** | **kill** | **kill** |
| daily cap reached | **cap-reached** | **cap-reached** | **cap-reached** |
| cap near, at a local threshold (off by default) | — | **cap-near** | **cap-near** |
| the active installation's consumer going live → offline | — | **offline** | **offline** |
| a prompt due to be read, not yet heard | — | — | **prompt** |

Two decisions in that table are this task's rather than the owner's, and are stated here rather
than left to be read off the code:

- **Which members narrowing keeps** is P6.5's own choice, because D160 fixes the set and not the
  subset. Narrowing keeps the two events that mean the work or the money has stopped — a kill
  switch and a reached cap — and drops the offline transition (a state the app *shows*) and the
  local cap-near threshold. The whole table is pinned by an exhaustive test rather than by
  examples, so no cell is left for a caller to interpret.
- **A first sighting of an offline consumer is not a transition.** D160's event is "going from live
  to offline", so an installation the app has never seen alive has not gone anywhere; interrupting
  on it would make the app speak over the user for a consumer that was never up. Fail-quiet, on
  purpose, and recorded here instead of being found later.

The order of the answers is D160's severity order, so two events at once report the one that
matters most.

**The honest limits.** Two inputs are recomputed on the phone because nothing else can: `caps`
(this phone cannot see per-run spend or halted runs — tension (b1) of the reconciliation — so
P3.2/P3.3 compute the fraction and this module decides what it means) and `presence.previous` (the
state the app last showed). And this module verifies **no MAC**: it has no key, and the rules only
require the field to be present. The outbox's tag is verified where the key lives; a queue is
phone-local state, which is why a queued prompt is trusted for its *shape* on every read.

## Refusals: fail closed, never coerce

Every way an input can fail to be this module's input has its own code, exported as `REFUSAL`; the
codes P6.3's validator raises are propagated with a `prompt.` or `answer.` prefix, because a stored
prompt is re-validated by that validator and the two must not disagree about what a prompt is.

| Code | When |
|---|---|
| `input-not-an-object` | a queue, an event or the policy's input is not a plain object |
| `unknown-field` | a value carries a field this module does not read — a value is never a bag |
| `accessor-property` | a field this module does read is an accessor, so it is refused without ever being called (P1.1's code, spelled the same) |
| `invalid-now` | `now` is not a positive safe integer |
| `now-went-backwards` | `now` is earlier than the queue's own, so expiry would run in reverse |
| `invalid-account` | the installation id is not 1..64 characters |
| `invalid-ttl` | the TTL is outside P1.1's `MIN_WINDOW_MS`..`MAX_PROMPT_TTL_MS` |
| `invalid-window` | the command window is outside P1.1's bounds |
| `invalid-max-pending` | the cap is outside 1..`MAX_MAX_PENDING` (1024) |
| `invalid-future-tolerance` | the future tolerance is outside P1.1's `0`..`MAX_FUTURE_TOLERANCE_MS` (24 hours), the bound that decides how far a prompt's `at` may lead the clock |
| `invalid-queue` | a handed-back queue has a field that is missing, mistyped or out of bounds |
| `invalid-prompts` | the prompts are not a list, or hold more entries than the cap allows |
| `invalid-entry` | an entry is malformed, its prompt fails the schema, or its prompt is not a prompt |
| `duplicate-entry` / `unsorted-prompts` | a handed-back queue holds one id twice, or is out of canonical order |
| `invalid-queues` | the cross-account view was handed two queues for one installation, or not a list |
| `not-a-prompt` / `not-an-answer` | the delivered message is not the kind the operation takes |
| `invalid-destructive` / `invalid-safe` | the local classification is not a boolean / a bounded single line |
| `safe-not-an-option` | the safe answer is not one of the prompt's own options, so the user could never have chosen it |
| `zero-timestamp` / `negative-zero` | the schema accepts `at: 0` and `at: -0`; both are the shape of an unset or negated counter, not a time |
| `future-timestamp` | the message is from beyond the future tolerance |
| `expired` | (answer) the answer is past the window its kind is judged by — a dead prompt (`field: prompt.at`, with the expiry it is owed) or an answer older than the TTL while the prompt lives (`field: answer.at`, with none) |
| `replay` | (answer) a confirmation older than the command window |
| `id-conflict` | a held id under a different sender or `at` |
| `queue-full` | the cap — carries `QUEUE_FULL_REPLY` for the orchestrator |
| `unknown-prompt` | the answer names a prompt this queue does not hold |
| `invalid-switch` / `invalid-severity` / `invalid-presence` / `invalid-caps` | the policy was handed a value outside the vocabulary it reads — an unknown severity is refused, never ranked as "not a kill" |

## Who consumes it

- **P6.7** (the consumer loop) is what calls all of this, holds the queue in the phone's local
  store, delivers an expiry's answer once and passes `acceptanceKind`'s kind to P1.1.
- **P6.9** (the text screen) renders `pending`; **P6.12** speaks `announcement`'s sentence and the
  policies' reasons; **P3.5**/**P3.3** supply the digest, the presence states and the caps.
- Nothing in the CLI or the Go tree consumes this module: the app is its own artifact.

## Tests

```sh
cd shared && npm test                            # the package's tests (the glob picks this up)
node --test "prompts/*.test.js"                  # this module's alone
shared/prompts/mutation-proof.sh                 # the guards, mutation-proved
```

`index.test.js` holds the plan's verification line case by case: prompts queue by default (`at`
order, the total tie-break by id, the classification held beside the message), the pause's count
across accounts and the two-question sentence, "what's pending" grouped per installation, the
expiry at D118's TTL with the answer it yields — asserted against P1.1's own `step` at the boundary
— the sweeper-independence of expiry, both windows of D173 (including a delivery P1.1 reads as a
replay and accepts as a prompt), the refusal code an over-age answer takes following P1.1's kind
mapping, the reminder schedule and a pause's three behaviours (speak, stay quiet, escalate), a
redelivery that cannot clear an announcement beside the answer that leaves the queue (the two
halves of one honest limit), the cap at its boundary, and the interrupt table exhaustively. One
test is not about values at all: it scans `index.js` and refuses a clock, a file, a network, a
process handle, a global, a `node:` builtin or an import that resolves outside `shared/`, because
the module's header claims all of that and a claim nothing checks is a comment.

**The red step** is P6.5's own: "an unanswered prompt expires at D118's mailbox TTL, as a pure
function of the prompt's `at`, the TTL and the consumer's clock", written against a stub consumer
that never expires anything and failing on it. It is `mutation-proof.sh`'s first guard, so it stays
re-runnable rather than living only in a pull-request transcript.

`mutation-proof.sh` plants 54 one-line bugs in `index.js` — each disabling exactly one guard — and
requires every one to make this module's suite fail. It refuses to report at all if it cannot find
a working runner, if the pristine suite is not green with at least one test actually run (an empty
glob exits 0, see [the package README](../README.md)), or if a control mutation goes uncaught. It
runs the module's own tests rather than the package's `npm test` because of a pre-existing,
unrelated failure in `shared/url` on the node line the package pins; the script's header says why,
and the task's pull request reports it.

One guard the first version of the harness could not kill, and what that turned out to mean. `isDue`
returned false explicitly for an entry at reminder stage 0, and no mutation of that line changed any
answer: at stage 0 the reminder time is `at - 23h`, and an announcement is made at the queue's
`now`, so the fall-through looked like it answered false on its own. It does not. A prompt's `at`
may lead the clock by up to the future tolerance (P1.1's 24 hours, and a hand-built queue may carry
any `announcedAt` the reader accepts), so for an `at` between 23 and 24 hours ahead the stage-0
reminder time is still in the future — and without the branch the question was reported due at
every pause, read out again each time, and kept interrupting under `widen`. The branch is back,
with the reason that holds written beside it, and the test that tells it apart is in
(`announce`, `pending` and `evaluateInterrupt` all asserted on it). A guard whose removal a test
cannot see is a guard no test defends — here it was the *premise* for removing it that was wrong,
which is the other thing the harness exists to force.

No test value here is a secret, and none is secret-shaped: the fixtures' `mac` is 64 `a`s, which is
a shape, not a key.
