# `shared/presence` — the staleness and offline rule

Task **P1.4** of [the mobile plan](../../docs/mobile/implementation-plan.md), decided by **D117**
(answered 2026-10-17). One pure function, `evaluatePresence`, over a heartbeat, the current
time and the heartbeat's interval. Nothing here reads a clock, a file, a database or a random
source: the inputs are the whole input, which is what D126 asks of everything that can be a
pure function.

```js
import { evaluatePresence, STATE } from './index.js';

const { ok, state } = evaluatePresence({ heartbeat, now: serverNow, intervalMs: 60_000 });
// ok: false -> a refusal (see below); ok: true -> state is live | stale | offline | clock-differs
```

## The rule

| The heartbeat | The state | Why |
|---|---|---|
| younger than N intervals (N = 3) | `live` | the orchestrator is writing on schedule |
| at or past N intervals, short of the offline band | `stale` | it may be busy or hung; the app can say so without calling it dead |
| at or past the offline band (12 intervals) | `offline` | minutes of silence, not one dropped write |
| in the future beyond the tolerance (one interval) | `clock-differs` | the phone's clock, not the orchestrator, is what is wrong |
| absent (`null`) | `offline` | "we have heard nothing" must not render as "everything is fine" — and it is **never** `live` |

**The order of those branches is the rule.** The future check runs first, so a skewed clock can
never be read as `live` (a negative age) and never as `offline` (an age that "makes no sense").
D117 asks for exactly one answer for a skew, and it is `clock-differs`. The bands are inclusive
on the stale and offline side: a heartbeat exactly N intervals old is `stale`, and the test
suite pins both boundaries one millisecond either way.

**The honest limit, which no test can close:** a skew in the *past* — a phone clock behind the
server's — is not detectable from one heartbeat. A heartbeat that looks far too old may be a
dead orchestrator or a slow clock, and a function of one heartbeat cannot tell them apart. That
is why D117 makes the **server's** timestamp the authority and why the caller must hand this
module a server-anchored `now` (Firebase's `.info/serverTimeOffset`, the CLI's
`json-contract.md` §10.13 discipline of trusting the document over the local clock). This
module cannot verify that the caller did, and says so here rather than implying otherwise.

## The parameters, and the one number that is provisional

| Name | Value | What it is |
|---|---|---|
| `DEFAULT_INTERVAL_MS` | 60 000 | how often D117's writer is expected to write — **provisional** |
| `MIN_INTERVAL_MS` | 30 000 | **D117's floor**: RTDB bills per write, and a setting below the floor is refused, never clamped |
| `MAX_INTERVAL_MS` | 86 400 000 | a ceiling, so the band arithmetic cannot leave the safe-integer range |
| `DEFAULT_MISSED_INTERVALS` | 3 | P1.4's "N intervals" |
| `DEFAULT_OFFLINE_INTERVALS` | 12 | four times the stale band |
| `DEFAULT_FUTURE_TOLERANCE_MS` | one interval | jitter and latency are not a clock problem |
| `MAX_BAND`, `MAX_BOOT_ID_LENGTH` | 1 000, 64 | bounds on the inputs' size |

**The default interval is provisional and this is not a hedge.** The plan's C4 gives this task
the interval and its floor and says what the choice costs: an interval chosen without the
write-cost measurement D117 asks for. That measurement is **check 84**, it is owner-run, and it
has not been run. Until it is, the number is a bounded default an operator can move through the
setting — and check 84's result may move it.

## Refusals: fail closed, never coerce

Every way an input can fail to be this rule's input has its own code, exported as `REFUSAL`. A
refusal returns `{ ok: false, code, field, detail }` and carries **no state**, so a caller
cannot render a bug as "offline" by accident. Every code the module can return is here, and a
test in `index.test.js` fails if this table and `REFUSAL` ever drift apart:

| Code | When |
|---|---|
| `input-not-an-object` | the input is not a plain object (a string, a number, an array, `null`, a class instance) |
| `unknown-field` | the input carries a field this rule does not read |
| `invalid-heartbeat` | the heartbeat is not a plain object — a `Date` instance, an array, a string, a number |
| `heartbeat-missing-field` | the heartbeat lacks its `at` or its `bootId` (D117's node carries both) |
| `heartbeat-unknown-field` | the heartbeat carries a field beyond `at` and `bootId` |
| `invalid-heartbeat-timestamp` | `at` is not a **positive safe integer** — a numeric string, a float, `NaN`, `Infinity`, `0` or a negative number. `at: 0` is the shape of an *unset* counter, and silently reading it as 1970 would hide a writer bug behind the state it resembles |
| `invalid-boot-id` | the boot id is empty, oversize (> 64), or outside `[A-Za-z0-9._:-]` |
| `invalid-now` | `now` is not a positive safe integer |
| `invalid-interval` | the interval is not a **positive safe integer at all** — a numeric string, a float, `0`, a negative number or `NaN`; refused before the floor and the ceiling are consulted |
| `interval-below-floor` | the interval is a positive integer but below D117's floor (30 000 ms) |
| `interval-above-ceiling` | the interval is above the ceiling (86 400 000 ms) |
| `invalid-band` | a band is not an integer in `1..MAX_BAND` |
| `bands-not-increasing` | **two bands that do not nest** (`offlineIntervals <= missedIntervals`) — an ambiguity is a refusal, because the alternative is picking one band silently and calling the other a bug later |
| `invalid-future-tolerance` | the future tolerance is negative or not an integer |

## The boot id

D117's heartbeat carries a boot id beside its timestamp, and this module validates it, returns
it, and **never lets it decide anything**. The state is a function of the timestamp and the
bands alone, so a writer that reuses a boot id cannot make an old heartbeat look fresh. The
charset is narrow on purpose: the boot id reaches a screen (P3.5), and an id that can carry
control characters or directionality is a rendering hazard for no benefit. A UUID fits.

**This module's field name is `bootId`, and P6.3's signed heartbeat says `boot`.** The
rule here accepts `{at, bootId}` and refuses any other field
(`heartbeat-unknown-field`); `shared/mailbox-schema`'s `validateHeartbeat` accepts
`{at, boot, agents?, mac}`, which is the shape P2.1's template already gates
(`internal/budget/rules/rules.json.tmpl` uses `boot`) plus P6.3's `mac` and `agents`. So a
heartbeat signed per §3.5 cannot be handed to `evaluatePresence` without a mapping, and
no task writes one: P6.4 has landed (it touched the rules template, `rules.go` and the
golden plan) and it does not touch this module. The `boot`/`bootId` half is older than
P6.3 and the template sides with `boot`; the `mac` and `agents` halves are new. This is
recorded as a **conflict for the owner** in `shared/mailbox-schema/README.md`'s gaps, not
resolved here: renaming a field on either side, or naming and testing the caller-side
mapping in P3.6, is the owner's call, and either way the phone must not end up treating a
*signed* heartbeat as offline because the rule could not read it.

## Who consumes it

- **P3.5** (the voice screen) shows the offline state from this rule and refuses to accept a
  message while offline — the spec's "rather than accepting messages into the void".
- **P3.6** (the app's mailbox client) reads the heartbeat through it; the heartbeat is the
  **orchestrator's** write (D113b), so the app only ever reads it.

Nothing else in the plan consumes it, and this module consumes nothing.

## Tests

```sh
cd shared && npm test
```

`index.test.js` holds the plan's cases (the four states, the boundaries, missing never live),
the refusals, and two properties worth naming: the rule is a function of the values it is
handed — the same input twice returns equal results, and only the interval changed moves the
verdict — and it reads the **injected** `now`, using a `now` fixed in 2023 so a module that
quietly called `Date.now()` would call every heartbeat old.

Every guard above was mutation-proved when it was written: broken one at a time, the suite
failed on the test that names it, and the file was restored (recorded in the task's pull
request). No test value here is a secret, and none is secret-shaped.
