# `confirm` — the spoken-confirmation state machine

The machine that stands between a spoken destructive command and it actually happening
(mobile plan, Task **P6.2**; decisions **D169**, **D162**, and the owner's **D128 amendment of
2026-10-17**). It is pure logic: no clock, no network, no randomness, no filesystem, no
`Date`, and no dependency outside this package: it imports `lease/`'s timestamp bound,
`command/`'s confirmation recogniser (P6.1) and `mailbox-schema/`'s target bound (P6.3), each
of which imports nothing itself. `now` is a number the caller passes in, so every
timeout below is reproducible in a test and none of it can pass by accident at 3 a.m.

The shape is: a destructive command is **read back** first, and only a separate, later
utterance carrying the fixed phrase — P6.1's `confirm` app command, **"Fugaro, confirm"** —
turns that read-back into an action, and the action it authorises is **P6.3's action record**,
field for field.

```js
import { initialState, step, pending, readBack } from './index.js';

let machine = initialState({ now: 1_700_000_000_000 });          // and .state
const asked = step(machine.state, {
  type: 'command', now: 1_700_000_001_000, utteranceId: 'utt_1',
  text: 'cancel the docs refactor', verb: 'cancel', actionId: 'act_1',
  target: { kind: 'run', runId: 'run_8f21' },
});
// asked.ok, asked.outcome === 'read-back',
// asked.readBack === 'Cancel run run_8f21. Say "Fugaro, confirm" to confirm.'

const done = step(asked.state, {
  type: 'confirm', now: 1_700_000_005_000, utteranceId: 'utt_2', text: 'Fugaro, confirm',
  actionId: 'act_1', confirmationId: 'cnf_1',
  target: { kind: 'run', runId: 'run_8f21' },
});
// done.ok, done.outcome === 'confirmed', and done.record is P6.3's `message.action`:
// { id: 'act_1', verb: 'cancel', target: { run: 'run_8f21' },
//   confirmation: { id: 'cnf_1', at: 1_700_000_005_000 } }
```

## The rule, in one paragraph

A **destructive** command (`cancel`, `merge`, `confirm` — the closed set
`DESTRUCTIVE_VERBS`, spelled exactly as P6.3's `ACTIONS`; a cancel takes a run, a merge a
repository, PR and head, a confirm a run or a repository and PR) enters `pending` **only with a resolved target**. An ambiguous or
unresolved target is refused, cannot be read back, and therefore **cannot be confirmed**. The
read-back is built **from the resolved target**, never from the transcript: a run the speaker
*called* "the docs refactor" is read back as `Cancel run run_8f21.` — the exact run id, because
D47's Rule says the second, independent pass must hear the thing itself and not the paraphrase
that produced it. A confirmation must be a **separate utterance** recognised as the **fixed
phrase**; it has a **timeout**; **one confirmation authorises exactly one action**, which is
what the action id it carries and the confirmation id it spends are for; and **the utterance
that issued a command can never confirm it**. `launch` is **not** confirmed — D162 answered on
2026-10-17 that "kick off …" goes through, with the caps applying. Every other destructive verb
(the kill switch, the budget writes, the deletes) is **refused outright**.

## The phases

| Phase | What it means |
| --- | --- |
| `idle` | no read-back is outstanding |
| `pending` | exactly one read-back is outstanding: a confirmation can name it |
| `crowded` | more than one: nothing can be confirmed until one is answered or the speaker says "cancel that" |

`crowded` exists because "a confirm for a different target (the pending action changed, or a
**second pending action**) is refused" is only enforceable if the machine *knows* there is a
second one. A `confirm` in `crowded` is always `ambiguous-pending`, whatever it carries — and
that check runs before the replay ledger, so in `crowded` the `replayed-*` codes are
unreachable, which the declared table says and the table test checks.

## The events, and the whole transition table

Three events — `command`, `confirm`, `abort` ("cancel that") — and every phase × event cell is
declared in `TRANSITIONS`, exhaustively. `next` lists the phases an answer may leave the
machine in, **including the phase a refusal leaves behind**: a refusal carries no state at all,
so the machine stays exactly where it was.

| Phase | Event | May answer (`ok`) | May leave (`next`) | May refuse (`no`) |
| --- | --- | --- | --- | --- |
| `idle` | `command` | `pass-through`, `read-back` | `idle`, `pending` | the command set below |
| `idle` | `confirm` | — | `idle` | `not-a-confirmation`, `no-pending-action`, + the value codes |
| `idle` | `abort` | `nothing-pending` | `idle` | `command-and-confirm`, `replayed-utterance`, `ledger-full`, + the value codes |
| `pending` | `command` | `pass-through`, `read-back` | `pending`, `crowded` | the command set + `pending-full` |
| `pending` | `confirm` | `confirmed` | `idle`, `pending` | `not-a-confirmation`, `no-pending-action`, `same-utterance`, `replayed-utterance`, `replayed-confirmation`, `unresolved-target`, `verb-target-mismatch`, `bad-repo`, `bad-pr`, `bad-head-sha`, `different-target`, `confirmation-expired`, `ledger-full`, + the value codes (an `ambiguous-pending` here is impossible: it takes two outstanding read-backs, which is `crowded`) |
| `pending` | `abort` | `discarded`, `nothing-pending` | `idle`, `pending` | `command-and-confirm`, `replayed-utterance`, `ledger-full`, + the value codes |
| `crowded` | `command` | `pass-through`, `read-back` | `pending`, `crowded` | the command set + `pending-full` |
| `crowded` | `confirm` | — | `crowded` | `not-a-confirmation`, `ambiguous-pending`, + the value codes |
| `crowded` | `abort` | `discarded`, `nothing-pending` | `crowded`, `idle` | `command-and-confirm`, `replayed-utterance`, `ledger-full`, + the value codes |

The command set is `bad-head-sha`, `bad-pr`, `bad-repo`, `command-and-confirm`, `ledger-full`,
`refused-verb`, `replayed-action`, `replayed-utterance`, `unknown-verb`, `unresolved-target`,
`verb-target-mismatch`. The **value codes** — the ones about a malformed input rather than
about an answer — are `bad-character`, `bad-key`, `empty-string`, `missing-field`,
`too-long`, `unknown-field`, `wrong-type`, and they are declared in every cell because a
caller can hand any event any string.

`INPUT_REFUSALS` names the codes that are about the **input** rather than about the answer,
refused before the machine's phase can matter: `not-an-object`, `accessor-property`,
`internal-error`, `invalid-now`, `now-went-backwards`, `now-jumped`, `unknown-event`,
`unknown-state`, `invalid-state`, `invalid-timeout`, `invalid-max-pending`,
`invalid-max-ledger`, `invalid-pending`, `invalid-pending-entry`, `duplicate-pending-entry`,
`unsorted-pending`, `invalid-ledger`, `invalid-ledger-entry`, `duplicate-ledger-entry`,
`unsorted-ledger`, and — because a state read through them can refuse a *field* as well as an
event can — `missing-field` and `unknown-field`, which are also declared in every cell. The
vocabulary test uses this set only to check that no code is unaccounted for; each cell is still
checked for exact reachability, so a code being in both lists excuses nothing.

That split is what makes the table test able to check **both directions**: every cell's codes
must be declared *and reached* by a probe (so a rule nothing enforces shows up), and no code
outside the declared set may appear (so a rule the table does not admit shows up).

### The order the checks run in

`state` → event shape → `now`'s window → event fields → the phrase or the verb → the machine's
state → the replay ledger → the target, then the timeout → the ledger's room → accept.

The order is not cosmetic. It is why a misrecognised "confirm" is `not-a-confirmation` rather
than `no-pending-action`; why a confirm with two read-backs outstanding is `ambiguous-pending`
rather than a replay; why a `refused-verb` command is refused **before its target is read**, so
nothing about a kill switch is echoed or trusted; and why everything that can be said about
*this* confirmation (its phrase, its action id, its target, its deadline) is said **before**
the ledger's room, so a caller hears the real reason rather than "no space".

## Each refusal

| Code | When |
| --- | --- |
| `not-an-object` | a state or event that is not a plain data object (null, array, primitive, class instance) |
| `unknown-field` | a field this module does not name is present — the field set **is** the version |
| `missing-field` | a required field is absent, or explicitly `undefined` |
| `accessor-property` | a field read through a getter — refused **without ever calling it** |
| `internal-error` | the input threw while being read (a revoked or throwing `Proxy`); what was thrown is never echoed |
| `wrong-type` | a field holds the wrong type |
| `empty-string` | a required string is empty |
| `too-long` | a string past its bound (`ID_MAX` 100, `TEXT_MAX` 4096, `REPO_MAX` 200 — P6.3's `TARGET_MAX`) |
| `bad-key` | an id with a character a database key may not contain (`% . $ # [ ] /`), a control character, or a lone surrogate |
| `bad-character` | a text with a control character or a lone surrogate |
| `invalid-now` | a `now` that is not an epoch-millisecond integer in `1 .. MAX_TIMESTAMP` |
| `now-went-backwards` | a `now` earlier than the state's own: an expired read-back must not come back to life |
| `now-jumped` | a `now` more than `MAX_FORWARD_JUMP_MS` (366 days) ahead: one far-future clock reading must not poison every later honest one |
| `unknown-state` | a `phase` that is not one of the three |
| `invalid-state` | a declared phase that **disagrees with the pending read-backs** — refused rather than trusted |
| `unknown-event` | a `type` that is not one of the three |
| `invalid-timeout` | a `timeoutMs` outside 1 s .. 5 min |
| `invalid-max-pending` | a `maxPending` outside 1 .. 16 |
| `invalid-max-ledger` | a `maxLedger` outside 1 .. 100 000 |
| `invalid-pending` | a `pending` that is not a plain array, or is longer than the state's own cap |
| `invalid-pending-entry` | a pending record with a missing, unknown or malformed field, or one whose target does not match its verb |
| `duplicate-pending-entry` | two pending records for one action id |
| `unsorted-pending` | pending records not in the canonical order (`at`, then `actionId`) |
| `invalid-ledger` | a `ledger` that is not a plain array, or is longer than the state's own cap |
| `invalid-ledger-entry` | a ledger record with a missing, unknown or malformed field |
| `duplicate-ledger-entry` | two ledger records for one `(kind, id)` |
| `unsorted-ledger` | ledger records not in the canonical order (`at`, then `kind`, then `id`) |
| `unknown-verb` | a verb outside the closed set |
| `refused-verb` | the kill switch, the budget writes, the deletes — the D128 amendment's outright refusals |
| `unresolved-target` | a destructive verb with no resolved target: nothing to read back, so nothing to confirm |
| `verb-target-mismatch` | a verb handed an argument of a kind it does not take |
| `bad-repo` | a repository that is not `owner/name` with both segments present |
| `bad-pr` | a pull-request number that is not a positive integer inside `PR_MAX` |
| `bad-head-sha` | a head SHA that is not 40 or 64 lowercase hex characters |
| `not-a-confirmation` | a confirming utterance that is not the fixed phrase |
| `command-and-confirm` | one utterance carrying a command and the phrase |
| `no-pending-action` | a confirm whose action id names no outstanding read-back |
| `ambiguous-pending` | a confirm with more than one read-back outstanding: it names no one of them |
| `different-target` | a confirm asserting a target other than the one read back (the pending action changed, or the PR's head moved) |
| `confirmation-expired` | a confirm past the read-back's deadline |
| `same-utterance` | a confirming utterance that **is** the one that issued the command |
| `replayed-utterance` | an utterance id already spent |
| `replayed-action` | an action id already spent |
| `replayed-confirmation` | a confirmation id already spent |
| `pending-full` | a command with no room for another read-back |
| `ledger-full` | no room in the id ledger — and **nothing inside it is ever forgotten to make room** |

Every refusal is `{ ok: false, code, field }`, frozen, with `field` a **fixed path** the module
chose (`target.runId`, `confirmationId`, …). No refusal echoes a value, and no refusal names a
key the caller chose: a message that repeated the input would make the refusal itself a channel.

## The numbers

| Name | Value | Why |
| --- | --- | --- |
| `DEFAULT_CONFIRM_TIMEOUT_MS` | 60 s (min 1 s, max 5 min) | long enough to answer, short enough that a read-back is still about what the speaker heard |
| `DEFAULT_MAX_PENDING` | 4 (min 1, max 16) | the app speaks one read-back at a time; the cap is what makes a bounded state |
| `DEFAULT_MAX_LEDGER` | 1024 (min 1, max 100 000) | replay is permanent, so the ledger is what the machine refuses on rather than truncates |
| `MAX_FORWARD_JUMP_MS` | 366 days | see `now-jumped` |
| `ID_MAX` / `TEXT_MAX` | 100 / 4096 | the ids and transcripts this app produces |

The timeout is **inclusive at the deadline**: a confirmation at `at + timeoutMs` exactly is
still inside it, and one millisecond later is not. A read-back is dropped by the next `command`
or `abort` — **never** by a `confirm`, because a sweep there would answer `no-pending-action`
where the plan requires the timeout to be named.

## What this module is *not*

- **Not a router.** It never decides *which* run "the docs refactor" means. The router
  resolves against the configured lists and hands in a target, or hands in nothing — and
  nothing is `unresolved-target`, which cannot be confirmed.
- **Not a recogniser.** It does not do speech-to-text and does not repair one, and it has no
  grammar of its own: an utterance is the phrase exactly when `shared/command`'s router (P6.1,
  default wake-word aliases) routes it as the `confirm` app command. So "Fugaro, confirm" and
  "Figaro, confirm." are the phrase, while a bare `confirm` (agent text under D164), `conform`,
  `confirmed it`, `yes`, `sure`, `yes, merge it` (G10), "Fugaro, confirm it" and anything with
  an invisible format character are **not**. A caller that configures its own wake-word aliases
  in P6.1 must keep this module on the same set (today: the default).
- **Not the executor.** A `confirmed` answer carries P6.3's action record —
  `{ id, verb, target: {run} | {repo, pr}, head_sha (merge only), confirmation: { id, at } }`,
  built from the target that was **read back**, never from the one the confirmation asserted —
  and nothing else (a test wraps every record in a message and runs P6.3's `validateMessage`
  on it); re-validating
  and executing it — and the MAC that makes it unforgeable — is P6.7's, in Go
  (D169: a merge carries the PR's head SHA, so a merge confirmed against a moved head is
  refused here before it can be executed there).
- **Not the clock.** It has none. Everything time-shaped is the caller's `now`.
- **Not a mac.** It signs nothing and verifies nothing; it decides *whether* an action was
  authorised, not *whether the authorisation is authentic*.

## Evidence, and how to reproduce it

```sh
cd shared && npm test                  # 421 tests, 421 pass, 0 fail (Node 26; on Node 22.14 shared/url's one known failure)
shared/confirm/mutation-proof.sh       # 70 guards: 70 killed, 0 survived, 0 not applied
```

The suite includes the task's own list, each as its own test: a **misrecognised "confirm"**
("conform", "confirmed it", "yes") does not confirm; a **replayed confirm** (the same
confirmation id, or the same utterance id, twice) authorises nothing the second time; a
**confirm for a different target** (the pending action changed, or a second pending action) is
refused; a confirm **after the timeout** is refused, and at the deadline exactly it is not; a
**command and "confirm" in one utterance** is refused; the read-back for **"cancel the docs
refactor" names the resolved run id**; and the state machine's transitions are **total**
(an exhaustive table test, above, over every composition × every event in both directions with
more than 500 probes).

`mutation-proof.sh` disables one guard at a time and requires the suite to fail; it starts with
the plan's named red step — **the replayed confirm against a machine with no confirmation-id
ledger** — and it refuses to report anything unless the pristine suite passes with tests
actually run and a `refuse()` that always succeeds is caught first.

**The red step of the task itself**, recorded as it happened: with `index.js` absent,
`node --test confirm/index.test.js` fails at the import —

```
Error [ERR_MODULE_NOT_FOUND]: Cannot find module
'/work/repo/shared/confirm/index.js' imported from
'/work/repo/shared/confirm/index.test.js'
ℹ tests 1, pass 0, fail 1
```

— which is why the tests and `vectors.json` are the commit before the machine.

`vectors.json` carries the accept and refuse cases as **data** (`cases: [{ name, events,
expect }]`) for P6.7's Go parity test, and the JS suite replays it verbatim
(`the vectors file replays exactly: every accept and every refusal`), so the two readers cannot
drift without a test failing.

## Honest limits

- **The phrase is broad on purpose.** `command-and-confirm` refuses any command utterance that
  *contains* the word `confirm`, split on whitespace and punctuation. That means the `confirm` verb phrased as "confirm the
  deploy" is refused, and the speaker has to say "approve the deploy" (which the tests and the
  vectors use). The alternative — deciding which occurrence of the word is "the command" —
  is exactly the misrecognition this module exists to refuse.
- **`-0` is defended but not proved.** `isIntegerInRange` refuses `-0`; every field this module
  bounds has a minimum of 1, so removing that clause changes no answer and no mutation can
  kill it. `mutation-proof.sh` says so in place rather than counting it as a guard.
- **The timeout is only as good as the caller's clock.** A caller that passes a `now` far in
  the future expires read-backs; one that passes the same `now` twice makes a confirmation id
  the only thing standing between the action and a replay. Both are bounded (`now-jumped`,
  `replayed-confirmation`) and neither is a substitute for an honest clock.
- **Nothing here is wired to a device or a runner.** No task of the mobile plan is approved,
  and this module is not evidence that one is; it is logic with tests, and a consumer
  (P6.7 in Go, and the app's voice path) is still to come.
- **The Go parity test does not exist yet.** `vectors.json` is written for it (P6.7) and
  replayed by the JS suite today; until P6.7 lands, nothing proves the two implementations of
  D169 agree beyond the data being the same data.
