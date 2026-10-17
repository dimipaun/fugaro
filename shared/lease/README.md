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
| `takeClaim`'s staleness rule: fresh is `now.Sub(cur.At) < claimTTL` | the same comparison, `at - claim.at < ttl`, plus one bound: a claim more than `MAX_CLOCK_SKEW` ahead of `at` is expired (below) |

**`at` is a whole number of milliseconds since the epoch, from 1 to `MAX_TIMESTAMP`**
(2100-01-01T00:00:00Z, `Date.UTC(2100, 0, 1)`); 0 and -0 are refused. A state's TTL is a whole
number of milliseconds from 1 to `MAX_TTL` (24 hours). Beyond either bound the value is refused,
not clamped. The wire encoding of a timestamp —
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
| `bad-holder` | the holder is not a string, is blank, carries any whitespace, an invisible, direction-changing or blank-looking character, a private-use or unassigned code point, or is not in NFC (below) |
| `oversize` | the holder is longer than `MAX_HOLDER_LENGTH` (256) |
| `bad-timestamp` | `at` is not a whole number of milliseconds from 1 to `MAX_TIMESTAMP` (0 and -0 included) |
| `bad-ttl` | the state's TTL is not a whole number of milliseconds from 1 to `MAX_TTL` (zero included: a TTL of zero makes every claim stale the instant it is taken, which is the same as no lease at all) |
| `unreadable-claim` | the state holds something that is not a lease |
| `already-claimed` | a lease is held, by anyone, and the TTL has not run out |
| `not-holder` | a release names a holder other than the one holding the lease |
| `no-lease` | a release names a holder and nothing is held |

**What "invisible" means, and where the set comes from.** An invisible or direction-changing
character is how two different identities print as one, so the set is the one this tree already
uses for "what may be printed": `internal/pluginwire/wire.go`'s `Printable` — **Cc** (the C0
controls, DEL, and the C1 block, U+0080–U+009F), **Cf** (zero-width and bidi-override characters,
the soft hyphen, the BOM) and the two line separators, **Zl** and **Zp**. Two differences from
`Printable`, both deliberate: an unpaired surrogate is refused too (**Cs** — `Printable` escapes
it as RuneError, and a JS string can hold one where a Go string cannot), and `Printable`'s tab is
not exempt, because it has to print something readable while a holder is an identity with nothing
to print.

On top of that set, so that **two holders that print the same cannot differ**: every whitespace code
point (`\p{White_Space}`, which includes all of **Zs** — a holder has no spaces at all), the
**default-ignorable** code points (variation selectors, the combining grapheme joiner), the
blank-looking letters **U+2800** (braille pattern blank) and the **Hangul fillers** (U+115F,
U+1160, U+3164, U+FFA0), **Co** (private use) and **Cn** (unassigned, as of the running engine's
Unicode version). And a holder must already be in **NFC**: a decomposed `e` + U+0301 prints as the
precomposed `é`, so a non-NFC holder is **refused rather than normalized** — normalizing would
quietly make two inputs one identity. A holder that is merely **not ASCII** — an accented or CJK name — is not refused, and a
test asserts that as the positive control, so the rule cannot quietly become "ASCII only".

**Every input is read once.** A state, a held claim and a request are each copied into a frozen
snapshot, one read per field, before anything is judged, so a getter or a Proxy cannot answer one
value to the validation and another to the comparison (a TTL that reads as ten minutes and then as
`NaN` would otherwise make `x < NaN` false and let a fresh lease be taken over). Tests count the
reads.

**Fail-closed choices worth naming.**

- **A claim timestamped ahead of the caller — the decision for the owner to review.** A claim up to
  `MAX_CLOCK_SKEW` (**5 minutes**, exported) ahead of the caller's `at` is fresh, and only until its
  own timestamp plus the TTL, exactly as the CLI's comparison has it: a small negative age is two
  clocks disagreeing, not a dead holder. A claim **more** than `MAX_CLOCK_SKEW` ahead is treated as
  **expired**, and may be taken over. The reason is a denial of service: without a bound, a holder
  that writes `at: MAX_TIMESTAMP` (or `Number.MAX_SAFE_INTEGER`, before the ceiling existed) blocks
  every other holder until it chooses to release. **The alternative** is to refuse such a take at
  `takeLease` (a claimant whose `at` is ahead of the held claim — or of some reference — by more
  than the tolerance is refused, and the far-future lease is never written). It was not chosen
  because the module has no clock of its own: at take time there is no "now" to compare a lone
  claimant against, so a far-future first take cannot be told from an honest one, and the
  take-over rule is the only place both instants are in hand. The cost of the chosen rule is that a
  holder whose clock runs more than five minutes fast can lose its lease to a slower-clocked
  claimant; the owner may prefer the refusal.
- **An unreadable claim is refused, never taken over.** `takeClaim` treats one as stale and takes it
  over, and it may: its read carries a generation, so its takeover is conditional on the claim not
  having changed since. A state value carries no generation, so an unreadable claim here is an
  ambiguity — and the answer to an ambiguity about who holds the lease is a refusal. The refusal
  carries `because`, the inner code, so a caller can say what was wrong with it.

## Running its tests

```sh
cd shared && npm test
```

`shared/lease/mutation-proof.sh` disables each guard above in turn (the skew bound and its
boundary, the timestamp and TTL ceilings, zero, each holder class, NFC, the single-read snapshot,
and the TTL a release carries on) and asserts that the suite catches every one; run it from a clean
tree.

`node:test` and `node:assert/strict` only: no dependency, no install, no network, no filesystem, no
database, and no secret-shaped literal — there is no secret in a lease. The package's CI job does
not exist yet (P4.3 is owner-made, Global Constraint 8), so these tests run where someone runs them.

Related: [implementation-plan.md](../../docs/mobile/implementation-plan.md) (P1.2, and the "shared
files" table that gives the package's manifest to P0.1 alone) ·
[reconciliation.md](../../docs/mobile/reconciliation.md) (D110, D113b, F4) ·
[voice.md](../../docs/design/voice.md) (D49, superseded by D110).
