// Regenerates internal/mailbox/testdata/lease-parity.json, the consumer
// lease's parity corpus (mobile plan, Task P4.1 T3).
//
//   node shared/lease/parity.generate.mjs
//
// The Go consumer binds `shared/lease`'s rules — it cannot import a JavaScript
// module — so the binding is held to the module by **committed data**: this
// script reads the module's own answers for the inputs that decide who may act
// on a mailbox and writes them to the corpus, which
// `internal/mailbox/lease_test.go` (TestLeaseCorpusReplay) and
// `shared/lease/parity.test.js` both read. Ruling M9 of
// `docs/plans/2026-10-18-mobile-mailbox-consumer.md`: parity is proven by
// committed vectors where they exist and by a data corpus where they do not,
// and `shared/lease` carries no vector file.
//
// **This file is not a test and is not run by `npm test`** (which matches
// `**/*.test.js`) — the convention `shared/mailbox-schema/vectors.generate.mjs`
// set for its own artifact. It is the recipe for the corpus, kept beside it so
// the corpus can be re-derived rather than hand-edited, and **it is the only
// thing that writes the corpus**. `parity.test.js` reads it and re-derives
// every answer from the live module, so it neither needs nor performs a write:
// a mutation applied to `index.js` by
// `shared/lease/mutation-proof.sh` (which restores `index.js` and nothing else)
// turns the tests red without rewriting the committed corpus underneath them.
//
// **What the corpus carries, and what it deliberately does not.**
//
//   * The module's constants (`constants`) and its refusal vocabulary
//     (`codes`), so a bound or a code that moves on one side is a failing test
//     on the other.
//   * The decisions (`cases`): reading a state, taking the lease, releasing it
//     — with the freshness boundary, the `MAX_CLOCK_SKEW` boundary and its
//     one-millisecond neighbour, the ceilings on an instant and a TTL, zero, the
//     far-future lease that must not block a mailbox for ever, and every refusal
//     family.
//   * The corpus asks for a state that is not an object at all as `null`, which
//     the Go side passes as a nil record. It does **not** ask for the *array*
//     spelling of "not an object": Go's records are typed, so no Go caller can
//     hand one to the lease, and the Go replay says so rather than skipping it.
//   * **Not** the module's *holder* rules. The holder the Go consumer passes is
//     a heartbeat's `boot` id, and the merged schema (`shared/mailbox-schema`,
//     bound by `internal/mailbox/message.go` in T1) already fixes that shape —
//     an ASCII identity of at most `BOOT_MAX` characters, which is **stricter**
//     than the lease module's own holder rule (that one accepts, for instance, a
//     CJK name). A corpus case asking for a holder the module accepts and the
//     Go side refuses would be a parity test of a rule the tree does not have,
//     so the cases below use holders both ends accept (`boot-a`, `boot-b`), and
//     the Go side refuses anything else with the module's own `bad-holder`
//     code. The corpus's shape half — a state that is not an object, a missing
//     field, an unknown field, an unreadable claim — is carried in full, since
//     both ends read the same records.
//
// Everything here is injected data: no clock is read (`at` is always an
// argument), no file is written except the corpus itself, and nothing is
// recorded from a live project.

import { mkdirSync, writeFileSync } from 'node:fs';

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
} from './index.js';

// The corpus the Go replay and the test read. It lives in the Go package that
// binds the lease, beside the vectors a later task's corpus uses, because that
// is where the reader is.
export const CORPUS = new URL('../../internal/mailbox/testdata/lease-parity.json', import.meta.url);

const A = 'boot-a';
const B = 'boot-b';
const T = 1_700_000_000_000;

/** A lease record, as the module stores one. */
function lease(holder, at) {
  return { holder, at };
}

/** A state record: the shape every function below takes. */
function state(claim, ttl) {
  return { claim, ttl };
}

// The module's answers are frozen objects that carry a human-readable
// `message`. The corpus carries the DECISION and never the wording: a reworded
// refusal is not a protocol change and must not be a failing Go test, while a
// code, an outcome, a lease or a state is.
export function answer(result) {
  const out = { ok: result.ok };
  if (!result.ok) {
    out.code = result.code;
    if (result.because !== undefined) out.because = result.because;
  }
  if ('lease' in result) out.lease = result.lease === null ? null : leaseOf(result.lease);
  if ('existing' in result) out.existing = leaseOf(result.existing);
  if ('released' in result) out.released = leaseOf(result.released);
  if ('state' in result) out.state = stateOf(result.state);
  return out;
}

export function leaseOf(value) {
  return { holder: value.holder, at: value.at };
}

export function stateOf(value) {
  return { claim: value.claim === null ? null : leaseOf(value.claim), ttl: value.ttl };
}

function readCase(name, s) {
  return { name, op: 'read', state: s, expect: answer(readLease(s)) };
}

function takeCase(name, s, request) {
  return { name, op: 'take', state: s, request, expect: answer(takeLease(s, request)) };
}

function releaseCase(name, s, request) {
  return { name, op: 'release', state: s, request, expect: answer(releaseLease(s, request)) };
}

// The cases, in the order the plan's verification names them: the read, the
// take over an empty state and over a held one, the TTL boundary, the skew
// boundary and its neighbour, the far-future lease, the ceilings, and the
// refusals of each family.
export function cases() {
  const ttl = CLAIM_TTL;
  return [
    // The state, and what a state that is not one is answered.
    readCase('an empty state holds no lease', emptyLeaseState(ttl)),
    readCase('a held lease is read back', state(lease(A, T), ttl)),
    readCase('a claim that is not a lease is unreadable', state({ holder: A }, ttl)),
    readCase('a state with no claim field is refused', { ttl }),
    readCase('a state with a field the module does not define is refused', { claim: null, ttl, extra: 1 }),
    readCase('a state that is null is refused', null),
    readCase('a zero TTL is refused', state(null, 0)),
    readCase('a TTL past the ceiling is refused', state(null, MAX_TTL + 1)),
    readCase('a TTL at the ceiling is a state', state(null, MAX_TTL)),
    readCase('a fractional TTL is refused', state(null, ttl + 0.5)),

    // The take.
    takeCase('a take on an empty state holds the lease', emptyLeaseState(ttl), lease(A, T)),
    takeCase('a take on an unreadable state is refused, never taken over', state({ holder: A }, ttl), lease(A, T)),
    takeCase('a take on a state with a bad TTL is refused', state(null, 0), lease(A, T)),
    takeCase('a fresh lease held by another is not taken', state(lease(B, T), ttl), lease(A, T + 1)),
    takeCase('a holder cannot re-take its own fresh lease', state(lease(A, T), ttl), lease(A, T + 1)),
    takeCase('a lease is fresh one millisecond before its TTL', state(lease(B, T), ttl), lease(A, T + ttl - 1)),
    takeCase('a lease is stale at exactly its TTL', state(lease(B, T), ttl), lease(A, T + ttl)),
    takeCase('a lease exactly MAX_CLOCK_SKEW ahead is fresh', state(lease(B, T + MAX_CLOCK_SKEW), ttl), lease(A, T)),
    takeCase('a lease one millisecond past the skew is taken over', state(lease(B, T + MAX_CLOCK_SKEW + 1), ttl), lease(A, T)),
    takeCase('a lease ahead to the ceiling cannot block a mailbox for ever', state(lease(B, MAX_TIMESTAMP), ttl), lease(A, T)),
    takeCase('the take instant may be the ceiling', emptyLeaseState(ttl), lease(A, MAX_TIMESTAMP)),

    // The take's own refusals: the instant, then the request's shape.
    takeCase('a zero instant is refused', emptyLeaseState(ttl), lease(A, 0)),
    takeCase('a negative instant is refused', emptyLeaseState(ttl), lease(A, -1)),
    takeCase('an instant past the ceiling is refused', emptyLeaseState(ttl), lease(A, MAX_TIMESTAMP + 1)),
    takeCase('a fractional instant is refused', emptyLeaseState(ttl), lease(A, T + 0.5)),
    takeCase('an instant that arrives as a string is refused', emptyLeaseState(ttl), { holder: A, at: '1700000000000' }),
    takeCase('a take with no holder is refused', emptyLeaseState(ttl), { at: T }),
    takeCase('a take with no instant is refused', emptyLeaseState(ttl), { holder: A }),
    takeCase('a take carrying a TTL of its own is refused', emptyLeaseState(ttl), { holder: A, at: T, ttl }),
    takeCase('a take that is not an object is refused', emptyLeaseState(ttl), null),

    // The holder's own refusals: the classes the Go side and the module both
    // refuse, with the code the module uses (`bad-holder`, and `oversize` past
    // the module's own length bound). See the header for what is deliberately
    // not asked here: the Go side's holder is a heartbeat's `boot` id, whose
    // shape the merged schema already fixes.
    takeCase('a blank holder is refused', emptyLeaseState(ttl), { holder: '   ', at: T }),
    takeCase('a padded holder is refused', emptyLeaseState(ttl), { holder: ` ${A} `, at: T }),
    takeCase('a holder with a space inside is refused', emptyLeaseState(ttl), { holder: 'boot a', at: T }),
    // The holder is written as an ESCAPE, `cafe` + U+0301 (combining acute),
    // because the case is exactly that it is not in NFC: written as a literal it
    // is at the mercy of whatever normalises the file next, and a normalised
    // copy of this line silently turns the case into its own opposite (the
    // module accepts a precomposed `café`).
    takeCase('a holder that is not in NFC is refused', emptyLeaseState(ttl), { holder: 'café', at: T }),
    takeCase('a holder past the module’s length bound is refused', emptyLeaseState(ttl), {
      holder: 'a'.repeat(MAX_HOLDER_LENGTH + 1),
      at: T,
    }),
    releaseCase('a release with a blank holder is refused', state(lease(A, T), ttl), { holder: ' ' }),

    // The release, and its two refusals.
    releaseCase('a release by the holder ends the lease', state(lease(A, T), ttl), { holder: A }),
    releaseCase('a release by another holder is refused', state(lease(B, T), ttl), { holder: A }),
    releaseCase('a release with nothing held is refused', emptyLeaseState(ttl), { holder: A }),
    releaseCase('a release with no holder is refused', state(lease(A, T), ttl), {}),
  ];
}

/** The whole corpus, in the one shape the Go replay reads. */
export function corpus() {
  return {
    // Where the answers below came from: the Go side names the same file in its
    // own header, and the replay test fails if the two disagree.
    module: 'shared/lease/index.js',
    constants: {
      CLAIM_TTL,
      MAX_CLOCK_SKEW,
      MAX_TTL,
      MAX_TIMESTAMP,
      MAX_HOLDER_LENGTH,
    },
    codes: LEASE_CODES,
    cases: cases(),
  };
}

mkdirSync(new URL('./', CORPUS), { recursive: true });
writeFileSync(CORPUS, `${JSON.stringify(corpus(), null, 2)}\n`);
process.stdout.write(`wrote ${new URL('../../internal/mailbox/testdata/lease-parity.json', import.meta.url).pathname}\n`);
