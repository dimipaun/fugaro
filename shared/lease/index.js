// The consumer lease (mobile plan, Task P1.2).
//
// Voice D49 refused the mobile relay because "a realtime-database command has
// no claim, no TTL and no ack", and D110 supersedes it **only by meeting the
// standard D49 named**. This module is the claim half of that standard, and
// its shape is not invented here: `internal/runstore/launch.go`'s
// Claim/ClaimTTL/ClaimKey/ReadClaim is the tree's only claim (F4), and this is
// that shape --
//
//   Claim{Holder, At}                        -> { holder, at }
//   const ClaimTTL = 10 * time.Minute        -> CLAIM_TTL
//   Store.Claim(ctx, holder, at)             -> takeLease(state, { holder, at })
//     (ok, existing *Claim, err)
//   Store.ReadClaim(ctx)                     -> readLease(state)
//   the CLI's releaseClaim (own holder only) -> releaseLease(state, { holder })
//
// -- so that the reason two launchers cannot start the same run twice becomes
// the reason two consumers cannot act on the same message twice. What the
// lease protects is one message: the consumer that holds it acts, and the one
// that does not waits, takes over a stale lease, or reports that it lost.
//
// **What this is not.** It is not a store, and it is not the message schema
// (P0.2) or the mailbox state machine (P1.1). It has no clock, no randomness
// and no I/O: a lease lives in a plain frozen **state value**, every function
// takes that value and returns a new one, and every instant arrives as an
// argument. The caller that owns the state -- the consumer (P4.1), the
// simulator (P1.5), the phone (P3.5) -- is where "now" comes from, and
// `Date.now` appears nowhere below. A test asserts both of those.
//
// **The TTL is the lease's, not the take's.** `emptyLeaseState(ttl)` fixes it
// and `CLAIM_TTL` is the default; a take request carries a holder and an
// instant and nothing else. This is the one place the module is deliberately
// not shaped like `Store.Claim`, which takes the CLI's package-level
// `claimTTL` because every launcher is the same binary: a per-take TTL would
// let a caller with a **shorter** one take over a lease its holder still
// considers fresh, and no per-take check can see that, because the two
// callers are not in the same call. One state carries one TTL for as long as
// it holds a claim, so two judgements about one lease cannot disagree.
//
// **Fail closed.** Every input is either exactly the shape documented here or
// a refusal carrying a code of its own: an unknown field, a missing one, a
// wrong type, a blank, padded, invisible, look-alike, non-NFC or oversize
// holder, a timestamp that is not a whole number of milliseconds from 1 to
// MAX_TIMESTAMP, a TTL that is not one from 1 to MAX_TTL, and a held claim
// this module cannot read. Every input object is read once (`snapshot`). Nothing is
// coerced, nothing is defaulted, and nothing is "close
// enough" -- a timestamp that arrives as a string is refused rather than
// parsed, because a guessed timestamp is how a lease goes stale early.
//
// **Timestamps are milliseconds.** `at` and the `ttl` are whole numbers of
// milliseconds since the epoch, which is the unit JS arithmetic is exact in;
// the wire encoding of a timestamp (RFC3339, the server's own clock) is the
// schema's and the presence rule's business, not this module's.

/**
 * How long a lease protects its holder, in milliseconds: `runstore.ClaimTTL`'s
 * ten minutes. It is the **default** for `emptyLeaseState`, not a constant a
 * caller is stuck with -- the plan makes the TTL a parameter, and a caller
 * that has measured its own work (P1.4's interval, D117's write cost) passes
 * its own.
 */
export const CLAIM_TTL = 10 * 60 * 1000;

/**
 * How far ahead of the caller's clock a held lease's timestamp may be and
 * still be honoured, in milliseconds: five minutes. Two clocks disagree, so a
 * lease a little ahead is not evidence of a dead holder; but a lease ahead by
 * more than this is treated as EXPIRED and may be taken over, because
 * "ahead" without a bound lets one holder (`at: MAX_TIMESTAMP`) block every
 * other until it chooses to release. An owner-reviewable decision; the
 * alternative is to refuse the far-future take instead (see README).
 */
export const MAX_CLOCK_SKEW = 5 * 60 * 1000;

/**
 * The latest instant a lease timestamp may name: 2100-01-01T00:00:00Z, in
 * milliseconds since the epoch. Anything later is refused as `bad-timestamp`.
 */
export const MAX_TIMESTAMP = Date.UTC(2100, 0, 1);

/** The longest TTL a lease state may carry: one day, in milliseconds. */
export const MAX_TTL = 24 * 60 * 60 * 1000;

/**
 * The longest holder this module accepts, in **Unicode code points** (not
 * UTF-16 units): so a holder is at most 1,024 bytes of UTF-8, whatever it is
 * written in. The CLI's bound is the bucket's
 * object-size limit (`runstore.ErrTooLarge`), which this module has no bucket
 * to borrow, so it states its own.
 */
export const MAX_HOLDER_LENGTH = 256;

/**
 * The refusal codes. Each is a distinct token rather than a shared "invalid":
 * a caller has to be able to tell "someone else holds this lease" from "the
 * lease is unreadable" from "you asked wrongly", because the three call for
 * three different actions.
 *
 * `ALREADY_CLAIMED` is the plan's own word for the answer a second holder
 * gets, and it is spelled the same here as it is there.
 */
export const LEASE_CODES = Object.freeze({
  UNKNOWN_FIELD: 'unknown-field',
  MISSING_FIELD: 'missing-field',
  BAD_TYPE: 'bad-type',
  BAD_HOLDER: 'bad-holder',
  BAD_TIMESTAMP: 'bad-timestamp',
  BAD_TTL: 'bad-ttl',
  OVERSIZE: 'oversize',
  UNREADABLE_CLAIM: 'unreadable-claim',
  NOT_HOLDER: 'not-holder',
  NO_LEASE: 'no-lease',
  ALREADY_CLAIMED: 'already-claimed',
});

// A lease record's fields, exactly: `runstore.Claim` has two and no more, and
// an unknown field is refused rather than stored, the way the rules template
// closes a subtree with `"$other": { ".validate": false }`. A state is this
// module's own container, so it carries the TTL beside the claim.
const LEASE_FIELDS = ['holder', 'at'];
const STATE_FIELDS = ['claim', 'ttl'];
const TAKE_FIELDS = ['holder', 'at'];
const RELEASE_FIELDS = ['holder'];

// A holder that is invisible or direction-changing is a way to make two
// different identities print as one; a padded one is a second identity that
// looks like the first.
//
// The set is `internal/pluginwire/wire.go`'s `Printable`, which is this tree's
// own answer to "what may be printed": **Cc** (which is the C0 controls *and*
// the C1 block, U+0080-U+009F, and DEL), **Cf** (zero-width and bidi-override
// characters, the soft hyphen, the BOM), and the two line separators, which
// are **Zl** and **Zp**. Two differences from `Printable`, both deliberate:
// an unpaired surrogate is refused too (**Cs** -- `Printable` escapes it as
// RuneError, and a JS string can hold one where a Go string cannot), and
// `Printable`'s tab is not exempt here. It has to print something readable;
// a holder is an identity and has nothing to print.
//
// Beyond `Printable`, so that two holders that print the same cannot differ:
// **every** whitespace code point (`\p{White_Space}`, which includes all of
// **Zs**, so a holder has no spaces at all), the default-ignorable code points
// (variation selectors, the combining grapheme joiner, the Hangul fillers),
// U+2800 BRAILLE PATTERN BLANK and the Hangul fillers by name (they print as
// blank and are letters or symbols, not spaces), **Co** (private use, which
// prints as whatever a font chooses) and **Cn** (unassigned, which prints as
// nothing in particular -- per the Unicode version of the running engine).
// A holder must also already be NFC: a non-NFC holder is refused, not
// normalized, since normalizing it would make two inputs one identity.
const NON_PRINTABLE =
  /[\p{Cc}\p{Cf}\p{Cs}\p{Co}\p{Cn}\p{Z}\p{White_Space}\p{Default_Ignorable_Code_Point}\u2800\u115F\u1160\u3164\uFFA0]/u;

function refusal(code, message, extra) {
  return Object.freeze({ ok: false, code, message, ...extra });
}

function isPlainObject(value) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/**
 * Read a plain object's fields **exactly once** each, into a frozen copy that
 * every later check and every result is built from. A getter (or a Proxy)
 * that answered one value to the validation and another to the comparison
 * would otherwise pass as one lease and act as another, so nothing below
 * reads an input object twice. Answers `{ ok: true, fields }`, or a refusal
 * for a non-object, an unknown field or a missing one.
 */
function snapshot(value, fields, what) {
  if (!isPlainObject(value)) return refusal(LEASE_CODES.BAD_TYPE, `${what} is a plain object`);
  const keys = Object.getOwnPropertyNames(value);
  for (const key of keys) {
    if (!fields.includes(key)) {
      // The key is the caller's (or an attacker's) and is never echoed: it
      // may be 100k characters or carry a bidi override, and a message is
      // something a caller logs and prints.
      return refusal(LEASE_CODES.UNKNOWN_FIELD, `${what} carries a field it does not define`);
    }
  }
  for (const key of fields) {
    if (!keys.includes(key)) return refusal(LEASE_CODES.MISSING_FIELD, `${what} has no ${key}`);
  }
  const copy = {};
  for (const key of fields) copy[key] = value[key];
  return Object.freeze({ ok: true, fields: Object.freeze(copy) });
}

/**
 * Is `holder` more than MAX_HOLDER_LENGTH code points? Answered without
 * walking a huge string: a code point is one or two UTF-16 units, so more
 * than twice the bound in units is over it, and anything shorter is counted
 * with an early exit.
 */
function isOversize(holder) {
  if (holder.length > 2 * MAX_HOLDER_LENGTH) return true;
  let points = 0;
  for (const _ of holder) {
    points += 1;
    if (points > MAX_HOLDER_LENGTH) return true;
  }
  return false;
}

// A holder that begins with a combining mark attaches it to whatever is
// printed before the holder.
const LEADING_MARK = /^\p{M}/u;

function validateHolder(holder) {
  if (typeof holder !== 'string') {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is a string');
  }
  // The length first, before any rule that walks the string or normalizes it:
  // a 10 MB holder is refused for its size, cheaply.
  if (isOversize(holder)) {
    return refusal(
      LEASE_CODES.OVERSIZE,
      `a lease holder is at most ${MAX_HOLDER_LENGTH} code points`,
    );
  }
  if (holder.trim() === '') {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is not blank');
  }
  if (holder !== holder.trim()) {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is not padded with whitespace');
  }
  if (NON_PRINTABLE.test(holder)) {
    return refusal(
      LEASE_CODES.BAD_HOLDER,
      'a lease holder carries no whitespace, control, format, ignorable, blank-looking, private-use or unassigned characters',
    );
  }
  if (LEADING_MARK.test(holder)) {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder does not start with a combining mark');
  }
  if (holder.normalize('NFC') !== holder) {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is in Unicode NFC form');
  }
  return Object.freeze({ ok: true, holder });
}

/**
 * A whole number of milliseconds from 1 to `max`, inclusive: a timestamp
 * (`max` is MAX_TIMESTAMP) and a TTL (`max` is MAX_TTL). Zero and -0 are
 * refused -- `Number.isSafeInteger(-0)` is true, and `-0 >= 1` is false.
 */
function isBoundedMilliseconds(value, max) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 1 && value <= max;
}

function validateTimestamp(at) {
  if (!isBoundedMilliseconds(at, MAX_TIMESTAMP)) {
    return refusal(
      LEASE_CODES.BAD_TIMESTAMP,
      `a lease timestamp is a whole number of milliseconds since the epoch, from 1 to ${MAX_TIMESTAMP}`,
    );
  }
  return Object.freeze({ ok: true, at });
}

/**
 * Is `value` a lease record, exactly? Answers `{ ok: true, lease }` for
 * `{ holder, at }` and a coded refusal for everything else, including an
 * object with a third field.
 */
export function validateLease(value) {
  const snap = snapshot(value, LEASE_FIELDS, 'a lease');
  if (!snap.ok) return snap;
  const holder = validateHolder(snap.fields.holder);
  if (!holder.ok) return holder;
  const at = validateTimestamp(snap.fields.at);
  if (!at.ok) return at;
  return Object.freeze({
    ok: true,
    lease: Object.freeze({ holder: holder.holder, at: at.at }),
  });
}

/**
 * The state a lease is taken from: no claim held, and the TTL that any claim
 * taken from it will be protected for. `ttl` defaults to `CLAIM_TTL`.
 *
 * The TTL is not judged here, and a state built with a malformed one is still
 * a state: every function that uses the state refuses it with `bad-ttl`, so a
 * caller cannot read its way past a TTL it set wrongly.
 *
 * Every state this module returns is a frozen value of this shape, so a
 * caller can hold one, pass it on and compare it without a hidden mutation.
 */
export function emptyLeaseState(ttl = CLAIM_TTL) {
  return Object.freeze({ claim: null, ttl });
}

/**
 * The claim and the TTL a state holds, each judged once: the shape, then the
 * TTL's value, then the claim's. A refusal from here is a refusal from every
 * function that reads a state, which is why they all agree about a malformed
 * one.
 */
function readState(state) {
  const snap = snapshot(state, STATE_FIELDS, 'a lease state');
  if (!snap.ok) return snap;
  const { claim, ttl } = snap.fields;
  if (!isBoundedMilliseconds(ttl, MAX_TTL)) {
    return refusal(
      LEASE_CODES.BAD_TTL,
      `a lease TTL is a whole number of milliseconds from 1 to ${MAX_TTL}`,
    );
  }
  if (claim === null) return Object.freeze({ ok: true, claim: null, ttl });

  const held = validateLease(claim);
  if (!held.ok) {
    // Deliberately stricter than the CLI. `takeClaim` treats an unreadable
    // claim as stale and takes it over, and it may: its read carries a
    // generation, so the takeover is conditional on the claim not having
    // changed since. A state value carries no generation, so an unreadable
    // claim here is an ambiguity -- and the answer to an ambiguity about who
    // holds the lease is a refusal, never a takeover. (A caller that has to
    // get past one resets the state explicitly, which is visible.)
    return refusal(
      LEASE_CODES.UNREADABLE_CLAIM,
      `the held claim is not a lease: ${held.message}`,
      { because: held.code },
    );
  }
  return Object.freeze({ ok: true, claim: held.lease, ttl });
}

/**
 * A refusal that hands back the state, so a caller can keep threading it --
 * and the state is the one this module READ (`held`, from the snapshot),
 * rebuilt and frozen, never the caller's own object, which may be unfrozen
 * or a Proxy. When the state itself was unreadable (`held` is null), there is
 * no state to hand back and the refusal carries none: the caller still has
 * whatever it passed, and every function refuses it the same way.
 */
function refusalWithState(result, held) {
  const out = { ok: false, code: result.code, message: result.message };
  if (result.because !== undefined) out.because = result.because;
  if (held !== null) out.state = stateOf(held);
  return Object.freeze(out);
}

/** A frozen state rebuilt from what `readState` read. */
function stateOf(held) {
  return Object.freeze({ claim: held.claim, ttl: held.ttl });
}

/**
 * The claim this state holds, or null. Answers `{ ok: true, lease }`, where
 * `lease` is null when nothing is held -- and a refusal, not a zero lease,
 * when the state holds something that is not one (`Store.ReadClaim` answers a
 * zero Claim in that case; a zero lease is a holder-less lease a caller would
 * then have to remember not to trust).
 */
export function readLease(state) {
  const held = readState(state);
  if (!held.ok) return held;
  return Object.freeze({ ok: true, lease: held.claim });
}

/**
 * Is the held lease `current` still fresh at the caller's instant `at`?
 *
 * Fresh is the CLI's rule (`takeClaim`: `now.Sub(cur.At) < claimTTL`) with
 * one bound added: a lease timestamped ahead of `at` by MORE than
 * MAX_CLOCK_SKEW is not fresh. Within the tolerance a lease ahead of the
 * caller is fresh, as before, and only until its own timestamp plus the TTL.
 * Both timestamps are bounded by MAX_TIMESTAMP, so the subtractions are exact.
 */
function isFresh(current, at, ttl) {
  if (current.at - at > MAX_CLOCK_SKEW) return false;
  return at - current.at < ttl;
}

/**
 * Take the lease, or be told who holds it.
 *
 * `request` is `{ holder, at }`: `at` is the caller's own reading of the
 * clock, injected and never read here, and it becomes the lease's timestamp
 * when the take succeeds, exactly as `Store.Claim` writes the `at` it was
 * given.
 *
 * Answers `{ ok: true, lease, state }` when the lease is now the caller's, and
 * `{ ok: false, code: 'already-claimed', existing, state }` when someone else
 * holds it and the state's TTL has not run out. A held lease is stale -- and
 * so takeable -- when the caller's `at` is at least the TTL past the held
 * one, which is the CLI's own boundary (`takeClaim`: fresh is `now.Sub(cur.At)
 * < claimTTL`). A held lease timestamped **ahead** of the caller's clock by
 * at most MAX_CLOCK_SKEW is fresh rather than stale -- a small negative age
 * is two clocks disagreeing, not a dead holder -- and one ahead by more than
 * that is treated as expired, so no timestamp can hold a lease forever.
 *
 * A holder that already holds the lease is answered `already-claimed` too,
 * whatever its holder string: `Store.Claim` answers ErrExists to a second
 * create, so a lease is renewed by releasing it, not by re-taking it.
 *
 * Nothing about a refused take touches the state: the claim it was refused by
 * is still the claim that is held, and the state handed back is a frozen copy
 * of the one read (none, when the state itself was unreadable).
 */
export function takeLease(state, request) {
  const held = readState(state);
  if (!held.ok) return refusalWithState(held, null);

  const snap = snapshot(request, TAKE_FIELDS, 'a take request');
  if (!snap.ok) return refusalWithState(snap, held);
  const holder = validateHolder(snap.fields.holder);
  if (!holder.ok) return refusalWithState(holder, held);
  const at = validateTimestamp(snap.fields.at);
  if (!at.ok) return refusalWithState(at, held);

  const mine = Object.freeze({ holder: holder.holder, at: at.at });
  const current = held.claim;
  if (current !== null && isFresh(current, mine.at, held.ttl)) {
    return Object.freeze({
      ok: false,
      code: LEASE_CODES.ALREADY_CLAIMED,
      message: `the lease is held by ${current.holder} at ${current.at}`,
      existing: current,
      state: stateOf(held),
    });
  }
  return Object.freeze({
    ok: true,
    lease: mine,
    state: Object.freeze({ claim: mine, ttl: held.ttl }),
  });
}

/**
 * Release the lease, if it is the caller's own. Answers
 * `{ ok: true, released, state }` with an empty state -- carrying the TTL on,
 * so the next take is protected the same way -- and a coded refusal when the
 * request names no holder, no lease is held, or the lease is held by someone
 * else: a release is never a takeover, so releasing another holder's lease is
 * refused rather than done. (`releaseClaim` in `internal/cli/run.go` deletes
 * only the exact object its own holder holds, and does nothing otherwise; the
 * refusal is the same rule, said out loud.)
 */
export function releaseLease(state, request) {
  const held = readState(state);
  if (!held.ok) return refusalWithState(held, null);

  const snap = snapshot(request, RELEASE_FIELDS, 'a release request');
  if (!snap.ok) return refusalWithState(snap, held);
  const holder = validateHolder(snap.fields.holder);
  if (!holder.ok) return refusalWithState(holder, held);

  const current = held.claim;
  if (current === null) {
    return refusalWithState(refusal(LEASE_CODES.NO_LEASE, 'no lease is held to release'), held);
  }
  if (current.holder !== holder.holder) {
    return refusalWithState(
      refusal(
        LEASE_CODES.NOT_HOLDER,
        `the lease is held by ${current.holder}, not by ${holder.holder}`,
      ),
      held,
    );
  }
  return Object.freeze({ ok: true, released: current, state: emptyLeaseState(held.ttl) });
}
