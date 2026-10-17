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
// wrong type, a blank, padded, control-character or oversize holder, a
// timestamp that is not a whole number of milliseconds, a TTL that is not a
// positive whole number of milliseconds, and a held claim this module cannot
// read. Nothing is coerced, nothing is defaulted, and nothing is "close
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
 * The longest holder this module accepts. The CLI's bound is the bucket's
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

// A control character in a holder is a way to make two different holders print
// as one; a padded one is a second identity that looks like the first.
const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f]/;

function refusal(code, message, extra) {
  return Object.freeze({ ok: false, code, message, ...extra });
}

function isPlainObject(value) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/** The first field of `value` that `allowed` does not name, or null. */
function unknownField(value, allowed) {
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) return key;
  }
  return null;
}

/** The first field of `required` that `value` does not carry, or null. */
function missingField(value, required) {
  for (const key of required) {
    if (!Object.hasOwn(value, key)) return key;
  }
  return null;
}

function validateHolder(holder) {
  if (typeof holder !== 'string') {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is a string');
  }
  if (holder.trim() === '') {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is not blank');
  }
  if (holder !== holder.trim()) {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder is not padded with whitespace');
  }
  if (CONTROL_CHARACTERS.test(holder)) {
    return refusal(LEASE_CODES.BAD_HOLDER, 'a lease holder carries no control characters');
  }
  if (holder.length > MAX_HOLDER_LENGTH) {
    return refusal(LEASE_CODES.OVERSIZE, `a lease holder is at most ${MAX_HOLDER_LENGTH} characters`);
  }
  return Object.freeze({ ok: true, holder });
}

/** A whole, positive number of milliseconds: a timestamp and a TTL. */
function isWholeMilliseconds(value) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

function validateTimestamp(at) {
  if (typeof at !== 'number' || !Number.isSafeInteger(at) || at < 0) {
    return refusal(
      LEASE_CODES.BAD_TIMESTAMP,
      'a lease timestamp is a whole number of milliseconds since the epoch',
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
  if (!isPlainObject(value)) {
    return refusal(LEASE_CODES.BAD_TYPE, 'a lease is a plain object with a holder and a timestamp');
  }
  const unknown = unknownField(value, LEASE_FIELDS);
  if (unknown !== null) {
    return refusal(LEASE_CODES.UNKNOWN_FIELD, `a lease has no field ${JSON.stringify(unknown)}`);
  }
  const missing = missingField(value, LEASE_FIELDS);
  if (missing !== null) {
    return refusal(LEASE_CODES.MISSING_FIELD, `a lease has no ${missing}`);
  }
  const holder = validateHolder(value.holder);
  if (!holder.ok) return holder;
  const at = validateTimestamp(value.at);
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
  if (!isPlainObject(state)) {
    return refusal(
      LEASE_CODES.BAD_TYPE,
      'a lease state is a plain object holding one claim and one TTL',
    );
  }
  const unknown = unknownField(state, STATE_FIELDS);
  if (unknown !== null) {
    return refusal(LEASE_CODES.UNKNOWN_FIELD, `a lease state has no field ${JSON.stringify(unknown)}`);
  }
  const missing = missingField(state, STATE_FIELDS);
  if (missing !== null) {
    return refusal(LEASE_CODES.MISSING_FIELD, `a lease state has no ${missing}`);
  }
  if (!isWholeMilliseconds(state.ttl)) {
    return refusal(
      LEASE_CODES.BAD_TTL,
      'a lease TTL is a positive whole number of milliseconds',
    );
  }
  if (state.claim === null) return Object.freeze({ ok: true, claim: null, ttl: state.ttl });

  const held = validateLease(state.claim);
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
  return Object.freeze({ ok: true, claim: held.lease, ttl: state.ttl });
}

/** Re-attach the state to a refusal, so a caller can keep threading it. */
function refusalWithState(result, state) {
  const out = { ok: false, code: result.code, message: result.message, state };
  if (result.because !== undefined) out.because = result.because;
  return Object.freeze(out);
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
 * < claimTTL`). A held lease timestamped **ahead** of the caller's clock is
 * therefore fresh rather than stale: a negative age is two clocks
 * disagreeing, not a dead holder.
 *
 * A holder that already holds the lease is answered `already-claimed` too,
 * whatever its holder string: `Store.Claim` answers ErrExists to a second
 * create, so a lease is renewed by releasing it, not by re-taking it.
 *
 * Nothing about a refused take touches the state: the claim it was refused by
 * is still the claim that is held.
 */
export function takeLease(state, request) {
  const held = readState(state);
  if (!held.ok) return refusalWithState(held, state);

  if (!isPlainObject(request)) {
    return refusalWithState(
      refusal(LEASE_CODES.BAD_TYPE, 'a take request is a plain object'),
      state,
    );
  }
  const unknown = unknownField(request, TAKE_FIELDS);
  if (unknown !== null) {
    return refusalWithState(
      refusal(LEASE_CODES.UNKNOWN_FIELD, `a take request has no field ${JSON.stringify(unknown)}`),
      state,
    );
  }
  const missing = missingField(request, TAKE_FIELDS);
  if (missing !== null) {
    return refusalWithState(
      refusal(LEASE_CODES.MISSING_FIELD, `a take request has no ${missing}`),
      state,
    );
  }
  const holder = validateHolder(request.holder);
  if (!holder.ok) return refusalWithState(holder, state);
  const at = validateTimestamp(request.at);
  if (!at.ok) return refusalWithState(at, state);

  const mine = Object.freeze({ holder: holder.holder, at: at.at });
  const current = held.claim;
  if (current !== null && mine.at - current.at < held.ttl) {
    return Object.freeze({
      ok: false,
      code: LEASE_CODES.ALREADY_CLAIMED,
      message: `the lease is held by ${current.holder} at ${current.at}`,
      existing: current,
      state,
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
  if (!held.ok) return refusalWithState(held, state);

  if (!isPlainObject(request)) {
    return refusalWithState(
      refusal(LEASE_CODES.BAD_TYPE, 'a release request is a plain object'),
      state,
    );
  }
  const unknown = unknownField(request, RELEASE_FIELDS);
  if (unknown !== null) {
    return refusalWithState(
      refusal(
        LEASE_CODES.UNKNOWN_FIELD,
        `a release request has no field ${JSON.stringify(unknown)}`,
      ),
      state,
    );
  }
  if (missingField(request, RELEASE_FIELDS) !== null) {
    return refusalWithState(
      refusal(LEASE_CODES.MISSING_FIELD, 'a release request has no holder'),
      state,
    );
  }
  const holder = validateHolder(request.holder);
  if (!holder.ok) return refusalWithState(holder, state);

  const current = held.claim;
  if (current === null) {
    return refusalWithState(refusal(LEASE_CODES.NO_LEASE, 'no lease is held to release'), state);
  }
  if (current.holder !== holder.holder) {
    return refusalWithState(
      refusal(
        LEASE_CODES.NOT_HOLDER,
        `the lease is held by ${current.holder}, not by ${holder.holder}`,
      ),
      state,
    );
  }
  return Object.freeze({ ok: true, released: current, state: emptyLeaseState(held.ttl) });
}
