# The consumer lease

The mailbox's claim (mobile plan, Task **P1.2**; decisions **D110**, **D113b**; finding **F4**).

Voice **D49** ruled the mobile relay "not built, and not scheduled" because "a realtime-database
command has no claim, no TTL and no ack". **D110** supersedes it — **only by meeting the standard
D49 named** — and this directory is the claim. Its shape is not invented here:
`internal/runstore/launch.go`'s `Claim`/`ClaimTTL`/`ClaimKey`/`ReadClaim` is the tree's only claim
(F4), so the reason two launchers cannot start the same run twice becomes the reason two consumers
cannot act on the same message twice.

```js
import { CLAIM_TTL, emptyLeaseState, readLease, releaseLease, takeLease } from '../lease/index.js';

let state = emptyLeaseState();                       // the TTL defaults to CLAIM_TTL

const mine = takeLease(state, { holder: 'consumer-a', at: 1_700_000_000_000 });
if (!mine.ok) { /* mine.code, and mine.existing names the holder */ }
state = mine.state;

readLease(state);                                    // { ok: true, lease: { holder, at } }
releaseLease(state, { holder: 'consumer-a' });        // { ok: true, released, state }
```

## The shape, and where it comes from

| `internal/runstore/launch.go` | here |
|---|---|
| `Claim{Holder string; At time.Time}` | the lease record `{ holder, at }` |
| `const ClaimTTL = 10 * time.Minute` | `CLAIM_TTL`, the default TTL, in milliseconds |
| `Store.Claim(ctx, holder, at) (ok, existing, err)` | `takeLease(state, { holder, at })` → `ok: true` or `ok: false, code: 'already-claimed', existing` |
| `Store.ReadClaim(ctx)` | `readLease(state)` |
| `releaseClaim` (deletes only the object its own holder holds) | `releaseLease(state, { holder })` |
| `takeClaim`'s staleness rule: fresh is `now.Sub(cur.At) < claimTTL` | the same comparison, `at - claim.at < ttl` |

**`at` is a whole number of milliseconds since the epoch.** The wire encoding of a timestamp —
RFC3339, the server's own clock — belongs to the message schema (P0.2) and the presence rule
(P1.4), not here; this module's unit is the one JS arithmetic is exact in.

## The TTL belongs to the lease, not to the take

`emptyLeaseState(ttl)` fixes the TTL and `CLAIM_TTL` is the default. A take request carries a
holder and an instant and **nothing else**; a `ttl` on a take is an unknown field, refused.

This is the one place the module is deliberately not shaped like `Store.Claim`, which takes the
CLI's package-level `claimTTL` because every launcher is the same binary. The first version of
`index.test.js` passed a TTL per take, and the module that satisfied it let a caller with a
**shorter** TTL take over a lease its holder still considered fresh — two consumers on one message,
arrived at through the parameter that was supposed to prevent it. No per-take check can see that,
because the two callers are not in the same call. One state carries one TTL for as long as it holds
a claim, so two judgements about one lease cannot disagree. A takeover keeps the TTL it took over
under, and a release carries it on.

## What a lease is not

- **Not a store.** A lease lives in a plain **frozen state value**; every function takes one and
  returns a new one, and nothing is mutated. Where the state lives (a process, a simulator's map, a
  database node) is the caller's business — P1.5's simulator, P4.1's consumer, P3.5's phone.
- **Not a clock.** Every instant is an argument. `Date.now` and `Math.random` appear nowhere in the
  module, and a test patches both and asserts they are never read: a lease that read its own clock
  could not be tested at all, and one that minted its own holder could not be compared.
- **Not the message schema** (P0.2) and **not the mailbox state machine** (P1.1). It says nothing
  about message ids, dedupe or acks; it says who may act next on one thing, for how long.
- **Not a fence.** D113b is explicit that the laptop consumer writes with the operator's **admin**
  credential, which bypasses the rules, so for that writer "the lease" is a rule in the consumer's
  own code rather than something the database enforces. What this module buys is that the rule is
  one function, tested, rather than a habit.

## The refusals

Every input is either exactly the shape above or a refusal carrying a code of its own
(`LEASE_CODES`). Nothing is coerced and nothing is defaulted: a timestamp that arrives as a string
is refused rather than parsed, because a guessed timestamp is how a lease goes stale early.

| Code | When |
|---|---|
| `unknown-field` | a lease, a state, a take or a release carries a field this module does not define |
| `missing-field` | one of the fields above is absent |
| `bad-type` | the value is not a plain object at all (null, an array, a string, a class instance) |
| `bad-holder` | the holder is not a string, is blank, is padded with whitespace, or carries a control character |
| `oversize` | the holder is longer than `MAX_HOLDER_LENGTH` (256) |
| `bad-timestamp` | `at` is not a whole, non-negative number of milliseconds |
| `bad-ttl` | the state's TTL is not a positive whole number of milliseconds (zero included: a TTL of zero makes every claim stale the instant it is taken, which is the same as no lease at all) |
| `unreadable-claim` | the state holds something that is not a lease |
| `already-claimed` | a lease is held, by anyone, and the TTL has not run out |
| `not-holder` | a release names a holder other than the one holding the lease |
| `no-lease` | a release names a holder and nothing is held |

**Two fail-closed choices worth naming.**

- **A claim timestamped in the future is never stale.** The comparison is the CLI's, so a negative
  age is not evidence that the holder died — it is two clocks disagreeing, and the failure mode of
  getting the direction wrong is a second consumer on one message.
- **An unreadable claim is refused, never taken over.** `takeClaim` treats one as stale and takes it
  over, and it may: its read carries a generation, so its takeover is conditional on the claim not
  having changed since. A state value carries no generation, so an unreadable claim here is an
  ambiguity — and the answer to an ambiguity about who holds the lease is a refusal. The refusal
  carries `because`, the inner code, so a caller can say what was wrong with it.

## Running its tests

```sh
cd shared && npm test
```

`node:test` and `node:assert/strict` only: no dependency, no install, no network, no filesystem, no
database, and no secret-shaped literal — there is no secret in a lease. The package's CI job does
not exist yet (P4.3 is owner-made, Global Constraint 8), so these tests run where someone runs them.

Related: [implementation-plan.md](../../docs/mobile/implementation-plan.md) (P1.2, and the "shared
files" table that gives the package's manifest to P0.1 alone) ·
[reconciliation.md](../../docs/mobile/reconciliation.md) (D110, D113b, F4) ·
[voice.md](../../docs/design/voice.md) (D49, superseded by D110).
