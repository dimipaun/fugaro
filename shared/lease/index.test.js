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
//     one millisecond before it is not, and a claim timestamped in the
//     FUTURE is not stale. The last one is the fail-closed direction: a
//     negative age is a clock disagreement, not evidence that the holder
//     died, and the cost of getting it wrong is a second consumer.
//   * the fail-closed refusals -- unknown field, missing field, wrong type,
//     blank/padded/control-character/oversize holder, a timestamp that is not
//     a whole number of milliseconds, a TTL that is not positive, and a held
//     claim this module cannot read. Each has a code of its own, and the
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
  MAX_HOLDER_LENGTH,
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

test('a claim timestamped in the future is never stale', () => {
  // Two clocks can disagree. A negative age is not evidence that the holder
  // died, so it is refused rather than taken over: the failure mode of
  // getting this direction wrong is two consumers on one message.
  const ahead = takeLease(emptyLeaseState(), { holder: 'consumer-a', at: AT + 10 * CLAIM_TTL });
  assert.equal(ahead.ok, true);

  const now = takeLease(ahead.state, { holder: 'consumer-b', at: AT });
  assert.equal(now.ok, false);
  assert.equal(now.code, 'already-claimed');
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
    assert.equal(taken.state, state);
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
