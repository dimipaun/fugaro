// The consumer lease's test (mobile plan, Task P1.2).
//
// It is written BEFORE `shared/lease/index.js`, the way P0.1's skeleton test
// was: `npm test` from `shared/` fails with ERR_MODULE_NOT_FOUND until the
// module exists, and that failure is this task's red step. A test file that
// merely asserted nothing would pass, and the package's runner cannot tell
// "no test file" from "no test run" (P0.1's README says so) -- so the red run
// is what proves the glob reaches this directory at all.
//
// What it pins, and why each one is here:
//
//   * the shape -- a holder and a timestamp, and a TTL whose default is
//     `runstore.ClaimTTL`'s ten minutes. D110 supersedes Voice D49 "only by
//     meeting the standard D49 named", and F4 says the shape is not invented
//     here: `internal/runstore/launch.go`'s Claim/ClaimTTL is the tree's only
//     claim, so this module is that shape and these tests are what keep it
//     that shape.
//   * the TTL belongs to the lease's **state** and not to each take. The
//     first version of this file passed a TTL per take, and the module that
//     satisfied it let a caller with a shorter TTL take over a lease its
//     holder still considered fresh -- which is the two-consumers failure
//     below, arrived at through the parameter. One state carries one TTL for
//     as long as it holds a claim, so two judgements about one lease cannot
//     disagree; a test asserts that a takeover keeps the same TTL.
//   * the refusal -- a second holder inside the TTL is answered
//     `already-claimed`, with the holder it was refused by, and the refused
//     take does not disturb the claim. This is the whole reason the module
//     exists: two consumers acting on one message is the failure
//     desktop-console-v2's F11 describes for the launch claim.
//   * the boundaries -- exactly at the TTL the lease is stale and takeable,
//     one millisecond before it is not, and a claim timestamped ahead of the
//     caller is fresh only within MAX_CLOCK_SKEW: a small negative age is a
//     clock disagreement, but an unbounded one would let a far-future
//     timestamp hold the lease forever.
//   * the fail-closed refusals -- unknown field, missing field, wrong type,
//     blank/whitespace/invisible/look-alike/non-NFC/oversize holder, a
//     timestamp or TTL outside [1, its ceiling], and a held claim this
//     module cannot read -- each input read exactly once. Each has a code of its own, and the
//     tests assert the code rather than a message.
//   * no clock and no randomness of its own: every instant is an argument.
//     The count is taken rather than the call thrown, so that the runner's
//     own bookkeeping keeps working; the assertion is that the count is zero.
//
// There is no third-party dependency here and no runtime: node:test,
// node:assert/strict, and nothing else. No network, no filesystem, no
// database, and no secret-shaped literal -- there is no secret in a lease.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  CLAIM_TTL,
  LEASE_CODES,
  MAX_CLOCK_SKEW,
  MAX_HOLDER_LENGTH,
  MAX_TIMESTAMP,
  MAX_TTL,
  emptyLeaseState,
  readLease,
  releaseLease,
  takeLease,
  validateLease,
} from './index.js';

// Every instant below is a plain number of milliseconds, passed in by this
// test. The module has no clock of its own, which the last test asserts.
const AT = 1_700_000_000_000;

test('a lease has a holder and a timestamp, and its TTL defaults to ClaimTTL', () => {
  // `runstore.ClaimTTL` is `10 * time.Minute`; the default here is that
  // number in milliseconds, so that the reason two launchers cannot start one
  // run twice is the reason two consumers cannot act on one message twice.
  assert.equal(CLAIM_TTL, 10 * 60 * 1000);
  assert.equal(CLAIM_TTL, 600_000);

  const empty = emptyLeaseState();
  assert.equal(empty.ttl, CLAIM_TTL); // the default, and the only TTL a take uses
  assert.equal(emptyLeaseState(1_000).ttl, 1_000);
  assert.deepEqual(readLease(empty), { ok: true, lease: null });

  const taken = takeLease(empty, { holder: 'consumer-a', at: AT });
  assert.equal(taken.ok, true);
  assert.equal(taken.lease.holder, 'consumer-a');
  assert.equal(taken.lease.at, AT);
  assert.deepEqual(readLease(taken.state), {
    ok: true,
    lease: { holder: 'consumer-a', at: AT },
  });
});

test('a second holder within the TTL is refused with an already-claimed answer', () => {
  const first = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });

  const second = takeLease(first.state, { holder: 'consumer-b', at: AT + 1 });
  assert.equal(second.ok, false);
  assert.equal(second.code, 'already-claimed'); // the literal the plan names
  assert.equal(second.code, LEASE_CODES.ALREADY_CLAIMED);
  assert.deepEqual(second.existing, { holder: 'consumer-a', at: AT });

  // The refused take disturbed nothing: the claim it was refused by is still
  // the one that is held.
  assert.deepEqual(readLease(second.state).lease, { holder: 'consumer-a', at: AT });

  // The last millisecond inside the TTL is still inside it.
  const lastFresh = takeLease(first.state, { holder: 'consumer-b', at: AT + CLAIM_TTL - 1 });
  assert.equal(lastFresh.code, 'already-claimed');
});

test('a lease past its TTL can be taken', () => {
  const first = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });

  // Exactly at the TTL the lease is stale, which is the CLI's own boundary:
  // `takeClaim` takes a claim over when `now.Sub(cur.At) < claimTTL` is false.
  const atTTL = takeLease(first.state, { holder: 'consumer-b', at: AT + CLAIM_TTL });
  assert.equal(atTTL.ok, true);
  assert.equal(atTTL.lease.holder, 'consumer-b');
  assert.equal(atTTL.lease.at, AT + CLAIM_TTL);
  assert.deepEqual(readLease(atTTL.state).lease, {
    holder: 'consumer-b',
    at: AT + CLAIM_TTL,
  });

  // ...and long after it, too.
  const later = takeLease(atTTL.state, { holder: 'consumer-c', at: AT + 100 * CLAIM_TTL });
  assert.equal(later.ok, true);
  assert.equal(later.lease.holder, 'consumer-c');
});

test('a claim timestamped ahead of the caller is fresh only within the skew tolerance', () => {
  // Two clocks can disagree, so a lease a little ahead of the caller's clock
  // is not evidence that its holder died. But "ahead" without a bound is a
  // denial of service: a holder that writes `at: Number.MAX_SAFE_INTEGER`
  // (or any far-future instant) would block every other holder until it
  // chose to release. The ruling: ahead by more than MAX_CLOCK_SKEW is
  // treated as EXPIRED, and within it the lease is fresh only until its own
  // timestamp plus the TTL, as usual.
  assert.equal(MAX_CLOCK_SKEW, 5 * 60 * 1000);

  // Ahead by exactly the tolerance: still fresh.
  const atSkew = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT + MAX_CLOCK_SKEW });
  assert.equal(atSkew.ok, true);
  const refused = takeLease(atSkew.state, { holder: 'consumer-b', at: AT });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, 'already-claimed');

  // ...and fresh only until its own timestamp plus the TTL.
  const lastFresh = takeLease(atSkew.state, {
    holder: 'consumer-b',
    at: AT + MAX_CLOCK_SKEW + CLAIM_TTL - 1,
  });
  assert.equal(lastFresh.code, 'already-claimed');
  const stale = takeLease(atSkew.state, { holder: 'consumer-b', at: AT + MAX_CLOCK_SKEW + CLAIM_TTL });
  assert.equal(stale.ok, true);

  // Ahead by one millisecond more than the tolerance: expired, taken over.
  const beyond = takeLease(emptyLeaseState(), {
    holder: 'consumer-a',
    at: AT + MAX_CLOCK_SKEW + 1,
  });
  const takeover = takeLease(beyond.state, { holder: 'consumer-b', at: AT });
  assert.equal(takeover.ok, true);
  assert.equal(takeover.lease.holder, 'consumer-b');
  assert.equal(takeover.lease.at, AT);

  // The far-future lease (the DoS) is taken over, not obeyed.
  const far = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: MAX_TIMESTAMP });
  assert.equal(far.ok, true);
  assert.equal(takeLease(far.state, { holder: 'consumer-b', at: AT }).ok, true);
});

test('a timestamp and a TTL are bounded: finite whole milliseconds, at least 1, at most the ceiling', () => {
  // The ceiling is 2100-01-01T00:00:00Z in milliseconds, and the TTL's is a day.
  assert.equal(MAX_TIMESTAMP, Date.UTC(2100, 0, 1));
  assert.equal(MAX_TTL, 24 * 60 * 60 * 1000);

  for (const at of [0, -0, -1, MAX_TIMESTAMP + 1, Number.MAX_SAFE_INTEGER, Number.POSITIVE_INFINITY]) {
    assert.equal(
      validateLease({ holder: 'consumer-a', at }).code,
      LEASE_CODES.BAD_TIMESTAMP,
      `at ${Object.is(at, -0) ? '-0' : at} was accepted by validateLease`,
    );
    assert.equal(
      takeLease(emptyLeaseState(), { holder: 'consumer-a', at }).code,
      LEASE_CODES.BAD_TIMESTAMP,
      `at ${Object.is(at, -0) ? '-0' : at} was accepted by a take`,
    );
  }
  // The bounds themselves are inside.
  assert.equal(takeLease(emptyLeaseState(), { holder: 'consumer-a', at: 1 }).ok, true);
  assert.equal(takeLease(emptyLeaseState(), { holder: 'consumer-a', at: MAX_TIMESTAMP }).ok, true);

  for (const ttl of [-0, MAX_TTL + 1, Number.MAX_SAFE_INTEGER]) {
    assert.equal(
      takeLease(emptyLeaseState(ttl), { holder: 'consumer-a', at: AT }).code,
      LEASE_CODES.BAD_TTL,
    );
  }
  assert.equal(takeLease(emptyLeaseState(1), { holder: 'consumer-a', at: AT }).ok, true);
  assert.equal(takeLease(emptyLeaseState(MAX_TTL), { holder: 'consumer-a', at: AT }).ok, true);
});

test('a holder cannot renew its own lease by taking it again', () => {
  // `runstore.Store.Claim` answers ErrExists to a second create whatever the
  // holder string is. The mailbox's lease answers the same, so a lease is
  // renewed by releasing it, not by re-taking it.
  const first = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
  const again = takeLease(first.state, { holder: 'consumer-a', at: AT + 1 });
  assert.equal(again.ok, false);
  assert.equal(again.code, 'already-claimed');
  assert.deepEqual(again.existing, { holder: 'consumer-a', at: AT });
});

test('a lease released explicitly can be taken immediately', () => {
  const first = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });

  const released = releaseLease(first.state, { holder: 'consumer-a' });
  assert.equal(released.ok, true);
  assert.deepEqual(released.released, { holder: 'consumer-a', at: AT });
  assert.deepEqual(readLease(released.state), { ok: true, lease: null });

  // One millisecond later, with the TTL nowhere near over: an explicit
  // release is what a failed action needs, so a retry does not wait it out.
  const second = takeLease(released.state, { holder: 'consumer-b', at: AT + 1 });
  assert.equal(second.ok, true);
  assert.equal(second.lease.holder, 'consumer-b');
});

test('a lease is released only by its holder, and only when one is held', () => {
  const first = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });

  const notMine = releaseLease(first.state, { holder: 'consumer-b' });
  assert.equal(notMine.ok, false);
  assert.equal(notMine.code, LEASE_CODES.NOT_HOLDER);
  assert.deepEqual(readLease(notMine.state).lease, { holder: 'consumer-a', at: AT });

  const none = releaseLease(emptyLeaseState(), { holder: 'consumer-a' });
  assert.equal(none.ok, false);
  assert.equal(none.code, LEASE_CODES.NO_LEASE);
});

test('a release carries the TTL on: the next take is protected the same way', () => {
  const short = takeLease(emptyLeaseState(1_000), { holder: 'consumer-a', at: AT });
  const released = releaseLease(short.state, { holder: 'consumer-a' });
  assert.equal(released.ok, true);
  assert.equal(released.state.ttl, 1_000); // not reset to CLAIM_TTL
  const next = takeLease(released.state, { holder: 'consumer-b', at: AT + 1 });
  assert.equal(next.ok, true);
  assert.equal(takeLease(next.state, { holder: 'consumer-c', at: AT + 1_001 }).ok, true);
});

test('the TTL is a parameter of the lease, and ClaimTTL is its default', () => {
  const short = takeLease(emptyLeaseState(1_000), { holder: 'consumer-a', at: AT });
  assert.equal(short.ok, true);
  const insideShort = takeLease(short.state, { holder: 'consumer-b', at: AT + 999 });
  assert.equal(insideShort.code, 'already-claimed');

  // The takeover is protected for the same 1,000 ms: the TTL is the lease's,
  // so a second judgement about the same lease cannot use a different one.
  const pastShort = takeLease(short.state, { holder: 'consumer-b', at: AT + 1_000 });
  assert.equal(pastShort.ok, true);
  assert.equal(pastShort.state.ttl, 1_000);

  // A longer TTL protects longer: the same instant is stale under a second
  // and fresh under the default ten minutes.
  const long = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
  assert.equal(long.state.ttl, CLAIM_TTL);
  const underLong = takeLease(long.state, { holder: 'consumer-b', at: AT + 1_000 });
  assert.equal(underLong.code, 'already-claimed');
});

test('an unknown field is refused, not ignored', () => {
  const lease = validateLease({ holder: 'consumer-a', at: AT, note: 'x' });
  assert.equal(lease.ok, false);
  assert.equal(lease.code, LEASE_CODES.UNKNOWN_FIELD);

  const request = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT, note: 'x' });
  assert.equal(request.code, LEASE_CODES.UNKNOWN_FIELD);

  // A take carries a holder and an instant and nothing else: the TTL is the
  // lease's, so a per-take one is not a thing this module accepts.
  const perTake = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT, ttl: 1_000 });
  assert.equal(perTake.code, LEASE_CODES.UNKNOWN_FIELD);

  // A state carries its claim under `claim`, and nothing else.
  const taken = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
  const state = { ...taken.state, extra: 1 };
  assert.equal(readLease(state).code, LEASE_CODES.UNKNOWN_FIELD);
});

test('a missing field is refused', () => {
  assert.equal(validateLease({ holder: 'consumer-a' }).code, LEASE_CODES.MISSING_FIELD);
  assert.equal(validateLease({ at: AT }).code, LEASE_CODES.MISSING_FIELD);
  assert.equal(validateLease({}).code, LEASE_CODES.MISSING_FIELD);

  assert.equal(
    takeLease(emptyLeaseState(), { holder: 'consumer-a' }).code,
    LEASE_CODES.MISSING_FIELD,
  );
  assert.equal(
    takeLease(emptyLeaseState(), { at: AT }).code,
    LEASE_CODES.MISSING_FIELD,
  );

  const taken = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
  assert.equal(releaseLease(taken.state, {}).code, LEASE_CODES.MISSING_FIELD);
});

test('a wrong type is refused, never coerced', () => {
  // A timestamp that arrives as a string is a shape this module did not agree
  // to. `Number(...)` would parse it and a guessed timestamp is how a lease
  // goes stale early, so it is refused instead.
  assert.equal(
    validateLease({ holder: 'consumer-a', at: String(AT) }).code,
    LEASE_CODES.BAD_TIMESTAMP,
  );
  assert.equal(
    validateLease({ holder: 'consumer-a', at: AT + 0.5 }).code,
    LEASE_CODES.BAD_TIMESTAMP,
  );
  assert.equal(
    validateLease({ holder: 'consumer-a', at: -1 }).code,
    LEASE_CODES.BAD_TIMESTAMP,
  );
  assert.equal(
    validateLease({ holder: 'consumer-a', at: Number.NaN }).code,
    LEASE_CODES.BAD_TIMESTAMP,
  );
  assert.equal(
    validateLease({ holder: 'consumer-a', at: new Date(AT) }).code,
    LEASE_CODES.BAD_TIMESTAMP,
  );
  assert.equal(validateLease({ holder: 42, at: AT }).code, LEASE_CODES.BAD_HOLDER);
  assert.equal(validateLease({ holder: null, at: AT }).code, LEASE_CODES.BAD_HOLDER);

  for (const value of [null, 'a lease', 7, [], [{ holder: 'consumer-a', at: AT }]]) {
    assert.equal(validateLease(value).code, LEASE_CODES.BAD_TYPE);
  }

  assert.equal(
    takeLease(emptyLeaseState(), { holder: 'consumer-a', at: String(AT) }).code,
    LEASE_CODES.BAD_TIMESTAMP,
  );
  assert.equal(takeLease(emptyLeaseState(), null).code, LEASE_CODES.BAD_TYPE);
  assert.equal(takeLease(emptyLeaseState(), 'consumer-a').code, LEASE_CODES.BAD_TYPE);
});

test('a blank, padded, invisible or oversize holder is refused', () => {
  assert.equal(validateLease({ holder: '', at: AT }).code, LEASE_CODES.BAD_HOLDER);
  assert.equal(validateLease({ holder: '   ', at: AT }).code, LEASE_CODES.BAD_HOLDER);
  // Whitespace is refused, not trimmed: a holder is an identity, and
  // " consumer-a" is not the identity "consumer-a".
  assert.equal(validateLease({ holder: ' consumer-a', at: AT }).code, LEASE_CODES.BAD_HOLDER);
  assert.equal(validateLease({ holder: 'consumer-a\n', at: AT }).code, LEASE_CODES.BAD_HOLDER);

  // An invisible or direction-changing character is a way to make two
  // different identities print as one, so the set is the one
  // `internal/pluginwire/wire.go`'s Printable escapes: Cc (the C0 controls,
  // DEL, and the C1 block), Cf (zero-width and bidi overrides, the soft
  // hyphen, the BOM), and the two line separators. This test first covered
  // NUL alone, and the C1 block and every format character went through it.
  const invisible = [
    0x00, 0x07, 0x09, 0x1b, // Cc: NUL, BEL, TAB, ESC
    0x7f, // DEL
    0x85, 0x9b, // C1: NEL, CSI
    0xad, // Cf: soft hyphen
    0x200b, 0x200e, 0x202e, 0xfeff, // Cf: zero-width space, LRM, RLO, BOM
    0x2028, 0x2029, // Zl/Zp: the line separators
  ];
  for (const point of invisible) {
    const holder = `consumer${String.fromCodePoint(point)}a`;
    assert.equal(
      validateLease({ holder, at: AT }).code,
      LEASE_CODES.BAD_HOLDER,
      `U+${point.toString(16).padStart(4, '0')} was accepted in a holder`,
    );
    assert.equal(
      takeLease(emptyLeaseState(), { holder, at: AT }).code,
      LEASE_CODES.BAD_HOLDER,
      `U+${point.toString(16).padStart(4, '0')} was accepted by a take`,
    );
  }

  // Whitespace anywhere (Zs and every other White_Space code point), the
  // blank-looking letters (U+2800 BRAILLE PATTERN BLANK, the Hangul fillers),
  // default-ignorable code points, private-use and unassigned code points:
  // each is a way for two holders to print the same and differ.
  const lookalike = [
    0x20, 0xa0, 0x1680, 0x2003, 0x202f, 0x3000, // Zs
    0x0b, 0x0c, // other White_Space
    0x2800, // braille pattern blank
    0x115f, 0x1160, 0x3164, 0xffa0, // Hangul fillers
    0x034f, 0xfe0f, 0x180e, // default-ignorable: CGJ, VS16, Mongolian vowel separator
    0xe000, 0xf8ff, 0xf0000, 0x10fffd, // Co: private use
    0x0378, 0xfdd0, 0xffff, 0xe0080, // Cn: unassigned and noncharacters
    0x1d173, 0xe0001, // Cf outside the BMP
  ];
  for (const point of lookalike) {
    const holder = `consumer${String.fromCodePoint(point)}a`;
    const hex = `U+${point.toString(16).padStart(4, '0')}`;
    assert.equal(validateLease({ holder, at: AT }).code, LEASE_CODES.BAD_HOLDER, `${hex} was accepted in a holder`);
    assert.equal(
      takeLease(emptyLeaseState(), { holder, at: AT }).code,
      LEASE_CODES.BAD_HOLDER,
      `${hex} was accepted by a take`,
    );
  }

  // A holder must already be in NFC, and a non-NFC one is refused rather than
  // normalized: "e" + COMBINING ACUTE prints as the precomposed "é", so the
  // two would be two identities that print as one.
  const decomposed = 'konsume\u0301';
  assert.notEqual(decomposed, decomposed.normalize('NFC'));
  assert.equal(validateLease({ holder: decomposed, at: AT }).code, LEASE_CODES.BAD_HOLDER);
  assert.equal(takeLease(emptyLeaseState(), { holder: decomposed, at: AT }).code, LEASE_CODES.BAD_HOLDER);
  assert.equal(validateLease({ holder: 'konsum\u00e9', at: AT }).ok, true); // the NFC form

  // An unpaired surrogate too: a JS string can hold one where a Go string
  // cannot, and Printable escapes it as RuneError.
  const lone = `consumer${String.fromCharCode(0xd800)}a`;
  assert.equal(validateLease({ holder: lone, at: AT }).code, LEASE_CODES.BAD_HOLDER);

  // The positive control: an identity that is not ASCII is not thereby
  // refused. Only the invisible ones are.
  assert.equal(validateLease({ holder: 'konsumé', at: AT }).ok, true);
  assert.equal(validateLease({ holder: 'consumer-中', at: AT }).ok, true);

  const oversize = 'h'.repeat(MAX_HOLDER_LENGTH + 1);
  assert.equal(validateLease({ holder: oversize, at: AT }).code, LEASE_CODES.OVERSIZE);
  // The bound itself is inside it.
  const atBound = 'h'.repeat(MAX_HOLDER_LENGTH);
  assert.equal(validateLease({ holder: atBound, at: AT }).ok, true);
});

test('a malformed TTL is refused', () => {
  // A TTL of zero would make every claim stale the instant it was taken,
  // which is the same as having no lease at all: refused, not defaulted. The
  // malformed TTL is the state's, so every use of that state is refused by
  // it -- the state does not become usable by reading it.
  for (const ttl of [0, -1, Number.NaN, Number.POSITIVE_INFINITY, 0.5, '600000', null]) {
    const state = emptyLeaseState(ttl);
    const refused = takeLease(state, { holder: 'consumer-a', at: AT });
    assert.equal(refused.ok, false);
    assert.equal(refused.code, LEASE_CODES.BAD_TTL);
    assert.equal(readLease(state).code, LEASE_CODES.BAD_TTL);
    assert.equal(releaseLease(state, { holder: 'consumer-a' }).code, LEASE_CODES.BAD_TTL);
  }

  // A state with no TTL at all is not a state, either.
  const noTTL = { claim: null };
  assert.equal(takeLease(noTTL, { holder: 'consumer-a', at: AT }).code, LEASE_CODES.MISSING_FIELD);
});

test('a held claim that is not a lease is refused, never taken over', () => {
  // The CLI treats an unreadable claim as stale and takes it over
  // (`internal/cli/run.go`'s takeClaim), and it can: a bucket read carries a
  // generation, so its takeover is conditional on the claim not having
  // changed. This module's state carries no generation, so an unreadable
  // claim is an ambiguity -- and the answer to an ambiguity about who holds
  // the lease is a refusal, not a takeover.
  const unreadable = [
    { holder: 'consumer-a' },
    { holder: 'consumer-a', at: 'soon' },
    { holder: '', at: AT },
    { holder: 'consumer-a', at: AT, extra: 1 },
    7,
    'consumer-a',
  ];
  for (const claim of unreadable) {
    const state = { claim, ttl: CLAIM_TTL };
    const taken = takeLease(state, { holder: 'consumer-b', at: AT + 100 * CLAIM_TTL });
    assert.equal(taken.ok, false);
    assert.equal(taken.code, LEASE_CODES.UNREADABLE_CLAIM);
    // The state is unreadable, so there is no state to hand back: the
    // refusal carries none rather than the caller's own object.
    assert.equal(Object.hasOwn(taken, 'state'), false);
    assert.equal(readLease(state).code, LEASE_CODES.UNREADABLE_CLAIM);
    assert.equal(releaseLease(state, { holder: 'consumer-a' }).code, LEASE_CODES.UNREADABLE_CLAIM);
  }

  // A state that is not a state is refused the same way, by its own code.
  assert.equal(readLease({}).code, LEASE_CODES.MISSING_FIELD);
  assert.equal(readLease({ ttl: CLAIM_TTL }).code, LEASE_CODES.MISSING_FIELD);
  assert.equal(readLease(null).code, LEASE_CODES.BAD_TYPE);
  assert.equal(readLease({ claim: null, ttl: CLAIM_TTL, extra: 1 }).code, LEASE_CODES.UNKNOWN_FIELD);
});

test('the module reads no clock and no randomness of its own', () => {
  const realNow = Date.now;
  const realRandom = Math.random;
  let clockReads = 0;
  let randomReads = 0;
  Date.now = () => {
    clockReads += 1;
    return realNow();
  };
  Math.random = () => {
    randomReads += 1;
    return realRandom();
  };
  try {
    // This block is synchronous, so nothing else in the process can read the
    // clock while it is patched.
    const taken = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
    takeLease(taken.state, { holder: 'consumer-b', at: AT + 1 });
    takeLease(taken.state, { holder: 'consumer-b', at: AT + CLAIM_TTL });
    readLease(taken.state);
    releaseLease(taken.state, { holder: 'consumer-a' });
  } finally {
    Date.now = realNow;
    Math.random = realRandom;
  }
  assert.equal(clockReads, 0, 'a lease takes its clock as an argument');
  assert.equal(randomReads, 0, 'a lease takes its holder as an argument');
});

test('a state is a value: the input is unchanged and every result is frozen', () => {
  const empty = emptyLeaseState();
  const taken = takeLease(empty, { holder: 'consumer-a', at: AT });

  assert.deepEqual(readLease(empty), { ok: true, lease: null }); // the input did not change
  assert.equal(Object.isFrozen(empty), true);
  assert.equal(Object.isFrozen(taken), true);
  assert.equal(Object.isFrozen(taken.state), true);
  assert.equal(Object.isFrozen(taken.lease), true);
  assert.equal(Object.isFrozen(taken.state.claim), true);
  assert.equal(Object.isFrozen(validateLease({ holder: 'consumer-a', at: AT }).lease), true);
});

// A plain object whose fields are getters that count their reads and may
// answer differently on a later read.
function counted(fields, reads) {
  const out = {};
  for (const [key, values] of Object.entries(fields)) {
    let n = 0;
    Object.defineProperty(out, key, {
      enumerable: true,
      get() {
        reads[key] = (reads[key] ?? 0) + 1;
        const v = values[Math.min(n, values.length - 1)];
        n += 1;
        return v;
      },
    });
  }
  return out;
}

test('every field of a state, a claim and a request is read exactly once', () => {
  const stateReads = {};
  const claimReads = {};
  const takeReads = {};
  const claim = counted({ holder: ['consumer-a'], at: [AT] }, claimReads);
  const state = counted({ claim: [claim], ttl: [CLAIM_TTL] }, stateReads);
  const request = counted({ holder: ['consumer-b'], at: [AT + 1] }, takeReads);
  assert.equal(takeLease(state, request).code, 'already-claimed');
  assert.deepEqual(stateReads, { claim: 1, ttl: 1 });
  assert.deepEqual(claimReads, { holder: 1, at: 1 });
  assert.deepEqual(takeReads, { holder: 1, at: 1 });

  const releaseReads = {};
  const relState = counted({ claim: [{ holder: 'consumer-a', at: AT }], ttl: [CLAIM_TTL] }, (stateReads.r = {}));
  const relRequest = counted({ holder: ['consumer-a'] }, releaseReads);
  assert.equal(releaseLease(relState, relRequest).ok, true);
  assert.deepEqual(stateReads.r, { claim: 1, ttl: 1 });
  assert.deepEqual(releaseReads, { holder: 1 });

  const leaseReads = {};
  assert.equal(validateLease(counted({ holder: ['consumer-a'], at: [AT] }, leaseReads)).ok, true);
  assert.deepEqual(leaseReads, { holder: 1, at: 1 });
});

test('a getter that changes between reads cannot pass validation with one value and act with another', () => {
  // The TTL is validated as ten minutes and then, on a second read, is NaN:
  // `x < NaN` is false, so a module that read it twice would let consumer-b
  // take a fresh lease over.
  const held = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
  const flipTTL = counted({ claim: [held.state.claim], ttl: [CLAIM_TTL, Number.NaN] }, {});
  assert.equal(takeLease(flipTTL, { holder: 'consumer-b', at: AT + 1 }).code, 'already-claimed');

  // The held claim reads as a valid lease and then as something else.
  const flipClaim = counted(
    { claim: [{ holder: 'consumer-a', at: AT }, null], ttl: [CLAIM_TTL] },
    {},
  );
  assert.equal(takeLease(flipClaim, { holder: 'consumer-b', at: AT + 1 }).code, 'already-claimed');

  // A request whose holder validates and then changes is acted on as validated.
  const flipHolder = counted({ holder: ['consumer-b', ' consumer-b'], at: [AT] }, {});
  const taken = takeLease(emptyLeaseState(), flipHolder);
  assert.equal(taken.ok, true);
  assert.equal(taken.lease.holder, 'consumer-b');
});

test('a refusal never echoes a field name it refused', () => {
  // An unknown key is attacker-chosen: a bidi override or 100k characters
  // must not reach a message a caller may log or print.
  const evil = `‮${'k'.repeat(100_000)}`;
  const lease = validateLease({ holder: 'consumer-a', at: AT, [evil]: 1 });
  assert.equal(lease.code, LEASE_CODES.UNKNOWN_FIELD);
  assert.equal(lease.message.includes('k'.repeat(10)), false);
  assert.equal(lease.message.includes('‮'), false);
  assert.ok(lease.message.length < 200);

  // ...and not through the unreadable-claim refusal that wraps it, either.
  const state = { claim: { holder: 'consumer-a', at: AT, [evil]: 1 }, ttl: CLAIM_TTL };
  const taken = takeLease(state, { holder: 'consumer-b', at: AT });
  assert.equal(taken.code, LEASE_CODES.UNREADABLE_CLAIM);
  assert.equal(taken.because, LEASE_CODES.UNKNOWN_FIELD);
  assert.equal(taken.message.includes('‮'), false);
  assert.ok(taken.message.length < 200);

  const request = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT, [evil]: 1 });
  assert.equal(request.message.includes('‮'), false);
  assert.ok(request.message.length < 200);
});

test('every state a refusal hands back is a frozen copy, never the caller object', () => {
  const held = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT });
  // An unfrozen state with a readable claim, and a Proxy over one.
  const loose = { claim: { holder: 'consumer-a', at: AT }, ttl: CLAIM_TTL };
  const proxy = new Proxy({ ...held.state }, {});
  for (const input of [loose, proxy]) {
    const refusals = [
      takeLease(input, { holder: 'consumer-b', at: AT + 1 }), // already-claimed
      takeLease(input, { holder: '', at: AT }), // bad-holder
      takeLease(input, { holder: 'consumer-b', at: 0 }), // bad-timestamp
      takeLease(input, null), // bad-type
      releaseLease(input, { holder: 'consumer-b' }), // not-holder
      releaseLease(input, {}), // missing-field
    ];
    for (const r of refusals) {
      assert.equal(r.ok, false);
      assert.notEqual(r.state, input, `${r.code} handed back the caller's object`);
      assert.equal(Object.isFrozen(r.state), true, `${r.code} handed back an unfrozen state`);
      assert.equal(Object.isFrozen(r.state.claim), true);
      assert.deepEqual(r.state, { claim: { holder: 'consumer-a', at: AT }, ttl: CLAIM_TTL });
    }
  }
  const none = releaseLease({ claim: null, ttl: 1_000 }, { holder: 'consumer-a' });
  assert.equal(none.code, LEASE_CODES.NO_LEASE);
  assert.equal(Object.isFrozen(none.state), true);
  assert.deepEqual(none.state, { claim: null, ttl: 1_000 });
});

test('format characters outside the default-ignorable set are refused (Cf)', () => {
  // These are Cf and nothing else in the holder rule: deleting \p{Cf} would
  // let them through.
  for (const point of [0x0600, 0xfff9, 0x110bd]) {
    const holder = `consumer${String.fromCodePoint(point)}a`;
    assert.equal(validateLease({ holder, at: AT }).code, LEASE_CODES.BAD_HOLDER, `U+${point.toString(16)}`);
  }
});

test('a class instance is not a plain object', () => {
  class Lease {
    constructor() {
      this.holder = 'consumer-a';
      this.at = AT;
    }
  }
  assert.equal(validateLease(new Lease()).code, LEASE_CODES.BAD_TYPE);
  assert.equal(takeLease(emptyLeaseState(), new Lease()).code, LEASE_CODES.BAD_TYPE);
  class State {
    constructor() {
      this.claim = null;
      this.ttl = CLAIM_TTL;
    }
  }
  assert.equal(readLease(new State()).code, LEASE_CODES.BAD_TYPE);
});

test('a holder is bounded in code points, checked before anything else', () => {
  // 256 code points: 512 UTF-16 units of an astral letter is still inside,
  // and the bound is at most 1,024 bytes of UTF-8.
  const astral = '\u{1d49c}'; // MATHEMATICAL SCRIPT CAPITAL A, a letter
  assert.equal(validateLease({ holder: astral.repeat(MAX_HOLDER_LENGTH), at: AT }).ok, true);
  assert.equal(
    validateLease({ holder: astral.repeat(MAX_HOLDER_LENGTH + 1), at: AT }).code,
    LEASE_CODES.OVERSIZE,
  );
  assert.ok(Buffer.byteLength(astral.repeat(MAX_HOLDER_LENGTH), 'utf8') <= 4 * MAX_HOLDER_LENGTH);

  // A 10 MB holder is refused as oversize -- before the character rule, so
  // it is refused for its length even though it is also invisible.
  const huge = '​'.repeat(10 * 1024 * 1024);
  assert.equal(validateLease({ holder: huge, at: AT }).code, LEASE_CODES.OVERSIZE);
  const hugeBlank = ' '.repeat(10 * 1024 * 1024);
  assert.equal(takeLease(emptyLeaseState(), { holder: hugeBlank, at: AT }).code, LEASE_CODES.OVERSIZE);
});

test('a holder does not start with a combining mark', () => {
  // A leading mark attaches to whatever precedes the holder when printed.
  for (const holder of ['́consumer', '⃝a', 'िconsumer']) {
    assert.equal(holder, holder.normalize('NFC'));
    assert.equal(validateLease({ holder, at: AT }).code, LEASE_CODES.BAD_HOLDER, JSON.stringify(holder));
  }
  // A mark after a base character is fine (the NFC-stable Devanagari case).
  assert.equal(validateLease({ holder: 'kि', at: AT }).ok, true);
});
