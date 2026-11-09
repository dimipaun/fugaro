// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's offline licence-key verifier (subscriptions plan S2.5; the design
// is docs/licensing/licence-key.md, the S2.1 note, and the Go verifier S2.3 and
// the console's S2.4 are this module's siblings).
//
// S2.5's own words are "S2.3's cases in JavaScript", so the cases below are
// S2.3's, one `test` each, and each is held here for the reason it is held
// there:
//
//   - **the id leads** (VerifyUnknownKeyID, VerifyRefusesBeforeParsing). A
//     verifier chooses the public key before it can verify anything, so an id
//     the keyring does not hold is refused *before either other part is
//     decoded* -- which is what the second case proves: the payload and the
//     signature are not merely wrong, they are not base64url at all, and the
//     refusal is still the id's.
//   - **the raw bytes before the claims** (VectorsRoundTrip, and the negative
//     vectors). The signature is over the payload's bytes as spelled, so a
//     re-wrapped payload is a different byte string rather than the same
//     claims, and no claim is read out of a payload whose signature did not
//     verify -- the truncated-claims case is a *valid* signature over a payload
//     that is not a claim set, and the refusal names the part, never a claim.
//   - **the three states and their edges** (GraceIsThirtyDays,
//     ExpiredAfterGrace): `valid` to `expires`, `grace` for the 30 days after
//     it, `expired` past that. A key past its grace is still *read*, not
//     refused: the lapse is a state the app draws (S6.1, S6.3), not an error.
//   - **the tier's maximum** (KeyBeyondTheTierLifetimeIsRefused), from the
//     key's own `issued`, with the renewal the overlap exists for admitted at
//     the ceiling.
//   - **the overlap** (OverlappingKeysPickTheLatest): two keys may be
//     installed, and the later `expires` is the one that applies.
//   - **the two keyrings** (StagingKeyIDIsRefused, and the production ring's
//     half): a key id names the environment its signing key belongs to, and a
//     production build holds production ids only.
//   - **no network** (the module's own scan at the end), the app's half of
//     S2.3's `TestLicencePackageImportsNoNetwork`: a verifier that never phones
//     home is a fact about its imports, so they are read back out of the source
//     and held to an allow-list -- and the scan is shown a plant it must catch,
//     because a scan that cannot fail is not a scan.
//
// **Two of S2.3's cases are deliberately not here, and each is somebody
// else's.** `TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts` is the CLI
// store's rule (the CLI reads `FUGARO_LICENCE_KEY` once and unsets it) and the
// app spawns no child and keeps no such variable: the key arrives at S2.8's
// settings screen as a paste, so what is held here is the shape that rests on
// -- the verifier reads **no environment at all**. The production keyring's
// *contents* are S2.9's, once the owner has created the keys (S3.8) -- so what
// is held here is the rule that survives that task: no test id is in it, and
// every entry it does hold is a public key. And one set of vectors for all
// three verifiers is S2.6's.
//
// **Why Node's Ed25519 is handed in, and not the app's own.** The app's suite
// runs in a fresh clone with no `node_modules` (`mobile/README.md`, "Running
// the tests"), so the module under test imports no package: the Ed25519
// primitive is a **port** (`createLicenceVerifier({ ed25519 })`), the app's own
// dependency is plugged in by `index.js`, and this file hands in **Node's**
// Ed25519 instead. That is the differential the sibling tests use and not a
// convenience: the keys below are minted and verified by a second
// implementation, so a defect in the app's own wiring cannot agree with itself
// here -- and `index.js`, the file that names the dependency, is the one file
// under `src/licence/` no test loads, exactly as `mobile/index.js` is.
//
// **No key material is in this file.** Every key below is generated at run
// time, and a signature is computed from it; the design admits a public key and
// a signature vector in test data, never a private half, and a private key
// literal here would be a secret-shaped string in a public repository.
//
// The red step this file was written for: `a re-spelled payload is a different
// byte string` fails before src/licence/verify.js exists.

import assert from 'node:assert/strict';
import { createPublicKey, generateKeyPairSync, sign as edSign, verify as edVerify } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  GRACE_DAYS,
  LICENCE_CODES,
  PRODUCTION_KEYRING,
  TIER_MAX_LIFETIME_DAYS,
  createLicenceVerifier,
} from './verify.js';

const HERE = import.meta.dirname;
const DAY = 86_400;

/** ISSUED is the moment every key in this file is minted at: a fixed Unix
 * second rather than a clock, because every state below is a fact about a key
 * and a `now`, and a test that read the clock would be a test that changes. */
const ISSUED = 1_800_000_000;

/** TIERS is the closed list the `tier` claim may name. */
const TIERS = ['community', 'team', 'enterprise'];

/** ED25519_SPKI_PREFIX is the twelve bytes every Ed25519 public key's DER
 * spelling starts with, so a raw 32-byte key -- the spelling the design's
 * vectors file carries -- can be handed to Node. */
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');

/**
 * nodeEd25519 is the port this file hands the verifier: Node's own Ed25519,
 * over the same raw 32-byte public key the app's dependency takes.
 *
 * It is strict, like the app's own wiring: `crypto.verify` is OpenSSL, which
 * rejects the malleable spellings a lenient verifier would accept. It also
 * refuses a wrong-length key or signature by throwing, as the dependency
 * (`index.js`, `@noble/curves`) does and `crypto.verify` alone does not -- the
 * guard that keeps those lengths off the port is only pinned by a port that
 * would throw.
 */
function nodeEd25519(signature, message, publicKey) {
  if (Buffer.from(publicKey).length !== 32) {
    throw new RangeError('"publicKey" expected Uint8Array of length 32');
  }
  if (Buffer.from(signature).length !== 64) {
    throw new RangeError('"signature" expected Uint8Array of length 64');
  }
  const key = createPublicKey({
    key: Buffer.concat([ED25519_SPKI_PREFIX, Buffer.from(publicKey)]),
    format: 'der',
    type: 'spki',
  });
  return edVerify(null, Buffer.from(message), key, Buffer.from(signature));
}

/** verifier is the module under test, over one keyring. */
function verifier(keyring = PRODUCTION_KEYRING) {
  return createLicenceVerifier({ ed25519: nodeEd25519, keyring });
}

/** Signer is one test key: an Ed25519 pair generated at run time, its raw
 * public half as the keyring carries it, and the id its keys are spelled
 * under. */
function signer(id) {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const raw = publicKey.export({ type: 'spki', format: 'der' });
  return {
    id,
    privateKey,
    raw: new Uint8Array(raw.subarray(raw.length - 32)),
    keyring: { [id]: Buffer.from(raw.subarray(raw.length - 32)).toString('base64url') },
  };
}

/** keyringOf is one keyring from several signers, so a case can hold a key that
 * verifies and a key that does not. */
function keyringOf(...signers) {
  const ring = {};
  for (const one of signers) Object.assign(ring, one.keyring);
  return ring;
}

/** b64url is the spelling every part of a key uses: base64url, unpadded. */
function b64url(bytes) {
  return Buffer.from(bytes).toString('base64url');
}

/** spell joins the three parts, in the order the design fixes: the key id
 * first, **in the clear** -- it is the one part nothing decodes, so it is the
 * text the keyring is keyed by -- then the payload, then the signature over the
 * payload's bytes. */
function spell(id, payload, signature) {
  return [id, b64url(payload), b64url(signature)].join('.');
}

/** mint signs one claim set as it is spelled: the payload's bytes, and a
 * signature over exactly those bytes. `spellId` and `payloadText` exist so a
 * negative vector can spell an id the signature does not belong to, or a
 * payload the signature does not cover. */
function mint(s, claims, spellId = s.id, payloadText = JSON.stringify(claims)) {
  const payload = Buffer.from(payloadText, 'utf8');
  return spell(spellId, payload, edSign(null, payload, s.privateKey));
}

/** claimsFor is a claim set of the shape the design fixes, with the fields a
 * case cares about overridden. */
function claimsFor(overrides = {}) {
  return {
    tier: 'team',
    features: ['coordinator'],
    seats: 5,
    organisation: 'example.com',
    issued: ISSUED,
    expires: ISSUED + 365 * DAY,
    ...overrides,
  };
}

/** at is a `now` from a Unix second. */
function at(seconds) {
  return new Date(seconds * 1000);
}

/** refusalOf narrows a read to its refusal, failing the case if it verified. */
function refusalOf(read) {
  assert.equal(read.ok, false, 'the key verified and this case expects a refusal');
  return read;
}

/** verified narrows a read -- or an empty store's `null` -- to what it holds,
 * failing the case if no key applied. */
function verified(read) {
  assert.ok(read !== null && read.ok, 'no key verified and this case expects one');
  return read;
}

/** stateAt is the state one key is in at a Unix second, failing the case if the
 * key was refused rather than read. */
function stateAt(key, seconds, ring) {
  return verified(verifier(ring).verify(key, at(seconds))).state;
}

test('VectorsRoundTrip: a minted key reads back as the claims it was minted from', () => {
  const acme = signer('acme-2026');
  const claims = claimsFor();

  assert.deepEqual(verifier(acme.keyring).verify(mint(acme, claims), at(ISSUED + DAY)), {
    ok: true,
    claims,
    state: 'valid',
  });
});

test('VectorsRoundTrip: every tier and a key with no features read back whole', () => {
  const acme = signer('acme-2026');
  for (const tier of TIERS) {
    const claims = claimsFor({ tier, features: [] });
    const read = verifier(acme.keyring).verify(mint(acme, claims), at(ISSUED));
    assert.deepEqual(verified(read).claims, claims, tier);
  }
});

test('VerifyUnknownKeyID: an id the keyring does not hold is refused, and the refusal carries no text', () => {
  const acme = signer('acme-2026');
  const refusal = refusalOf(verifier(keyringOf()).verify(mint(acme, claimsFor()), at(ISSUED)));

  // The refusal is a code and the part at fault, and nothing else: a key id is
  // text an attacker chose, so no sentence here carries one.
  assert.deepEqual(refusal, { ok: false, code: LICENCE_CODES.UNKNOWN_KEY_ID, field: 'id' });
});

test('VerifyRefusesBeforeParsing: an unknown id is refused before either other part is decoded', () => {
  const acme = signer('acme-2026');
  // The other two parts are not base64url at all, and not JSON either: if the
  // id were looked up after them, this refusal would be about a part.
  const refusal = refusalOf(verifier(acme.keyring).verify('someone-else.!!!not-base64url!!!.????', at(ISSUED)));

  assert.deepEqual(refusal, { ok: false, code: LICENCE_CODES.UNKNOWN_KEY_ID, field: 'id' });
});

test('VerifyUnknownKeyID: a key id outside the alphabet is refused as malformed, before any lookup', () => {
  const acme = signer('acme-2026');
  // The id is the one part nothing decodes -- it is compared and refused as the
  // text it is written as -- so a text outside the alphabet is not an id this
  // build simply does not hold, and it is refused with no part named.
  const refusal = refusalOf(verifier(acme.keyring).verify(`a key id with spaces.${b64url('{}')}.${b64url('x')}`, at(ISSUED)));

  assert.deepEqual(refusal, { ok: false, code: LICENCE_CODES.MALFORMED, field: null });
});

test('StagingKeyIDIsRefused: a key minted under a staging id is refused by the production verifier', () => {
  const staging = signer('staging-2026');
  const key = mint(staging, claimsFor());

  assert.deepEqual(verifier(PRODUCTION_KEYRING).verify(key, at(ISSUED)), {
    ok: false,
    code: LICENCE_CODES.UNKNOWN_KEY_ID,
    field: 'id',
  });

  // The same key verifies under the keyring its id names, so the refusal is
  // the ring's and not the key's.
  assert.equal(verified(verifier(staging.keyring).verify(key, at(ISSUED))).state, 'valid');
});

test('TestKeyIDIsNotInTheProductionKeyring: the ring holds no test id, and every entry is a public key', () => {
  const test_ = signer('test-2026');

  assert.equal(Object.hasOwn(PRODUCTION_KEYRING, test_.id), false);
  // S2.9 adds the owner's published production key here; what must survive that
  // task is the shape of every entry: a base64url 32-byte public key.
  for (const [id, encoded] of Object.entries(PRODUCTION_KEYRING)) {
    const decoded = Buffer.from(encoded, 'base64url');
    assert.equal(decoded.length, 32, `${id} is not an Ed25519 public key`);
    assert.equal(decoded.toString('base64url'), encoded, `${id} is not spelled canonically`);
  }
});

test('GraceIsThirtyDays: valid to expires, grace for 30 days after it, expired past that', () => {
  const acme = signer('acme-2026');
  const expires = ISSUED + 365 * DAY;
  const key = mint(acme, claimsFor({ expires }));
  const ring = acme.keyring;

  assert.equal(stateAt(key, ISSUED, ring), 'valid');
  assert.equal(stateAt(key, expires, ring), 'valid');
  assert.equal(stateAt(key, expires + 1, ring), 'grace');
  assert.equal(stateAt(key, expires + GRACE_DAYS * DAY, ring), 'grace');
  assert.equal(stateAt(key, expires + GRACE_DAYS * DAY + 1, ring), 'expired');
});

test('ExpiredAfterGrace: a key past its grace is read as expired, not refused', () => {
  const acme = signer('acme-2026');
  const expires = ISSUED + 365 * DAY;
  const key = mint(acme, claimsFor({ expires }));

  assert.deepEqual(verifier(acme.keyring).verify(key, at(expires + (GRACE_DAYS + 1) * DAY)), {
    ok: true,
    claims: claimsFor({ expires }),
    state: 'expired',
  });
});

test('GraceIsThirtyDays: the grace is the design’s number, and every tier’s maximum is the term plus it', () => {
  assert.equal(GRACE_DAYS, 30);
  for (const tier of TIERS) {
    assert.equal(TIER_MAX_LIFETIME_DAYS[tier], 365 + GRACE_DAYS, `${tier}'s maximum`);
  }
});

test('KeyBeyondTheTierLifetimeIsRefused: the bound is the term plus the 30-day issue lead', () => {
  const acme = signer('acme-2026');

  for (const tier of TIERS) {
    const max = TIER_MAX_LIFETIME_DAYS[tier];
    // The longest key the ruled issuance mints: an annual renewal, minted 30
    // days before the key it follows expires. It is at the ceiling, and it must
    // be admitted -- a bound of the bare term would refuse it.
    const renewal = mint(acme, claimsFor({ tier, expires: ISSUED + max * DAY }));
    assert.equal(verifier(acme.keyring).verify(renewal, at(ISSUED)).ok, true, `${tier} at the ceiling`);

    const past = mint(acme, claimsFor({ tier, expires: ISSUED + max * DAY + 1 }));
    assert.equal(refusalOf(verifier(acme.keyring).verify(past, at(ISSUED))).code, LICENCE_CODES.LIFETIME, `${tier} past the ceiling`);
  }
});

test('KeyBeyondTheTierLifetimeIsRefused: the bound is measured from the key’s own issued', () => {
  // The hole the design lists rather than closes: `issued` is the minter's to
  // choose, so a key minted with a future `issued` passes the length check and
  // reads valid under the state table for as long as its minter wants. Held
  // here so that closing it would be a decision and not an accident.
  const acme = signer('acme-2026');
  const future = ISSUED + 10 * 365 * DAY;
  const key = mint(acme, claimsFor({ issued: future, expires: future + TIER_MAX_LIFETIME_DAYS.team * DAY }));

  assert.equal(verified(verifier(acme.keyring).verify(key, at(ISSUED))).state, 'valid');
});

test('OverlappingKeysPickTheLatest: the later expires is the key that applies, in any order', () => {
  const first = signer('acme-2025');
  const renewal = signer('acme-2026');
  const ring = keyringOf(first, renewal);
  const earlier = ISSUED + 365 * DAY;
  const later = ISSUED + 335 * DAY + 365 * DAY;
  const older = mint(first, claimsFor({ issued: ISSUED, expires: earlier }));
  const newer = mint(renewal, claimsFor({ issued: ISSUED + 335 * DAY, expires: later }));
  const now = at(ISSUED + 340 * DAY); // inside both terms: the overlap the design mints for

  for (const store of [
    [older, newer],
    [newer, older],
  ]) {
    const read = verified(verifier(ring).latest(store, now));
    assert.equal(read.claims.expires, later);
    assert.equal(read.state, 'valid');
  }
});

test('OverlappingKeysPickTheLatest: an expired key beside a valid one loses to the valid one', () => {
  const first = signer('acme-2025');
  const renewal = signer('acme-2026');
  const earlier = ISSUED + 365 * DAY;
  const later = ISSUED + 335 * DAY + 365 * DAY;
  const older = mint(first, claimsFor({ issued: ISSUED, expires: earlier }));
  const newer = mint(renewal, claimsFor({ issued: ISSUED + 335 * DAY, expires: later }));

  const read = verified(verifier(keyringOf(first, renewal)).latest([newer, older], at(earlier + (GRACE_DAYS + 1) * DAY)));
  assert.equal(read.claims.expires, later);
  assert.equal(read.state, 'valid');
});

test('OverlappingKeysPickTheLatest: a key that is refused is skipped, not chosen', () => {
  const first = signer('acme-2025');
  const renewal = signer('acme-2026');
  const earlier = ISSUED + 365 * DAY;
  const later = ISSUED + 335 * DAY + 365 * DAY;
  const older = mint(first, claimsFor({ issued: ISSUED, expires: earlier }));
  // The id names the renewal, but the signature is the first signer's: the
  // entry is in the ring, and the key still does not verify.
  const foreign = mint(first, claimsFor({ expires: later }), renewal.id);

  const read = verified(verifier(keyringOf(first, renewal)).latest([foreign, older], at(ISSUED + DAY)));
  assert.equal(read.claims.expires, earlier);
});

test('OverlappingKeysPickTheLatest: a store with no usable key is null, and a lapsed key is still read', () => {
  const acme = signer('acme-2026');
  const expires = ISSUED + 365 * DAY;
  const key = mint(acme, claimsFor({ expires }));

  // The empty store, and a store whose every key is refused: both are the
  // store's business (S6.1's no-key state), not a fourth state here.
  assert.equal(verifier(acme.keyring).latest([], at(ISSUED)), null);
  assert.equal(verifier(keyringOf()).latest(['not.a.key', key], at(ISSUED)), null);

  // Past the grace, the state is expired rather than nothing: the app knows it
  // has lapsed.
  const lapsed = verified(verifier(acme.keyring).latest([key], at(expires + (GRACE_DAYS + 1) * DAY)));
  assert.equal(lapsed.state, 'expired');
});

test('a re-spelled payload is a different byte string, and the signature refuses it', () => {
  const acme = signer('acme-2026');
  const claims = claimsFor();
  const canonical = JSON.stringify(claims);
  // The same claims, spelled in another order: a verifier that parsed first and
  // compared claims would accept it.
  const respelled = JSON.stringify({
    organisation: claims.organisation,
    seats: claims.seats,
    features: claims.features,
    tier: claims.tier,
    issued: claims.issued,
    expires: claims.expires,
  });
  assert.notEqual(respelled, canonical);

  const signed = Buffer.from(canonical, 'utf8');
  const key = spell(acme.id, Buffer.from(respelled, 'utf8'), edSign(null, signed, acme.privateKey));

  assert.equal(refusalOf(verifier(acme.keyring).verify(key, at(ISSUED))).code, LICENCE_CODES.SIGNATURE);
});

test('a non-canonical spelling of a part is refused, not decoded to the same bytes', () => {
  const acme = signer('acme-2026');
  const payload = Buffer.from(JSON.stringify(claimsFor()), 'utf8');
  const signature = edSign(null, payload, acme.privateKey);
  const part = signature.toString('base64url');

  // The last character of an 86-character signature carries four bits the
  // decoder ignores: flipping the lowest one decodes to the same bytes.
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
  const last = alphabet.indexOf(part.slice(-1));
  const malleable = part.slice(0, -1) + alphabet[last ^ 1];
  assert.equal(Buffer.from(malleable, 'base64url').equals(signature), true, 'the vector is not malleable');

  // Spelled by hand rather than through `spell`: re-encoding the bytes would put
  // the canonical spelling back and lose the vector.
  const canonical = spell(acme.id, payload, signature);
  const respelled = canonical.replace(/\.[^.]+$/, `.${malleable}`);
  assert.notEqual(respelled, canonical);
  assert.equal(refusalOf(verifier(acme.keyring).verify(respelled, at(ISSUED))).code, LICENCE_CODES.MALFORMED);

  // And the padded spelling, which a lenient decoder accepts: also refused.
  const padded = canonical.replace(/\.[^.]+$/, `.${part}=`);
  assert.equal(refusalOf(verifier(acme.keyring).verify(padded, at(ISSUED))).code, LICENCE_CODES.MALFORMED);

  // And the length that leaves exactly one character over -- a spelling no byte
  // string can have, which Go's `RawURLEncoding.Strict()` refuses as illegal
  // base64 and the console refuses by re-encoding. A decoder that ignored the
  // leftover character would read the canonical bytes back, so the claim set is
  // grown until its spelling is a multiple of four and one `A` -- six zero bits
  // -- is exactly that shape.
  const base = claimsFor();
  const grow = (3 - (Buffer.byteLength(JSON.stringify(base), 'utf8') % 3)) % 3;
  const claims = { ...base, organisation: base.organisation + 'x'.repeat(grow) };
  const grown = mint(acme, claims);
  const [grownId, grownPayload, grownSignature] = grown.split('.');
  assert.equal(grownPayload.length % 4, 0, 'the case needs a spelling with no leftover bits');
  assert.equal(stateAt(grown, ISSUED, acme.keyring), 'valid');
  assert.equal(
    refusalOf(verifier(acme.keyring).verify(`${grownId}.${grownPayload}A.${grownSignature}`, at(ISSUED))).code,
    LICENCE_CODES.MALFORMED,
  );
});

test('a foreign key id is refused: the id names a key the signature does not belong to', () => {
  const acme = signer('acme-2026');
  const other = signer('other-2026');
  const key = mint(acme, claimsFor(), other.id);

  assert.deepEqual(verifier(keyringOf(acme, other)).verify(key, at(ISSUED)), {
    ok: false,
    code: LICENCE_CODES.SIGNATURE,
    field: 'signature',
  });
});

test('truncated claims are refused after the signature verifies, and no claim is drawn', () => {
  const acme = signer('acme-2026');
  // A *valid* signature over a payload that is not a claim set: the refusal must
  // be about the part, because a claim from an unverified payload is
  // attacker-chosen text -- and here the payload is verified and still not read.
  const truncated = '{"tier":"team","features":["report';
  const key = spell(acme.id, Buffer.from(truncated, 'utf8'), edSign(null, Buffer.from(truncated, 'utf8'), acme.privateKey));

  assert.deepEqual(verifier(acme.keyring).verify(key, at(ISSUED)), {
    ok: false,
    code: LICENCE_CODES.CLAIMS,
    field: 'claims',
  });
});

test('the signature is checked before the payload is parsed, so an unparseable payload with a bad signature is refused as a signature', () => {
  const acme = signer('acme-2026');
  const other = signer('other-2026');
  // The design's rule and not an implementation order: the payload is verified
  // as bytes, so a payload that is not even a claim set is still refused as a
  // *signature* when the signature is what failed. Parsing first would answer
  // `claims` here, which is a statement about a payload nobody verified.
  const payload = Buffer.from('this is not a claim set', 'utf8');
  const key = spell(acme.id, payload, edSign(null, payload, other.privateKey));

  assert.deepEqual(verifier(keyringOf(acme, other)).verify(key, at(ISSUED)), {
    ok: false,
    code: LICENCE_CODES.SIGNATURE,
    field: 'signature',
  });
});

test('a payload that is JSON but not a claim set is refused: an array, a scalar, a missing, mistyped or unknown claim', () => {
  const acme = signer('acme-2026');
  for (const payload of [
    '[1,2,3]',
    '"nope"',
    'null',
    '{"tier":"team"}',
    JSON.stringify({ ...claimsFor(), tier: 'premium' }),
    JSON.stringify({ ...claimsFor(), seats: '5' }),
    JSON.stringify({ ...claimsFor(), seats: -1 }),
    JSON.stringify({ ...claimsFor(), seats: 1.5 }),
    JSON.stringify({ ...claimsFor(), issued: -1 }),
    JSON.stringify({ ...claimsFor(), organisation: '' }),
    JSON.stringify({ ...claimsFor(), features: ['coordinator', 7] }),
    // A claim this verifier does not know: adding a claim is how the format
    // grows, and it is the format's own decision -- the shared vectors file
    // (S2.6) carries a `covers_until` vector the three verifiers must all
    // refuse, so this is the Go verifier's rule and not the console's.
    JSON.stringify({ ...claimsFor(), covers_until: ISSUED }),
    // ... and bytes after the claim set are not a claim set either.
    `${JSON.stringify(claimsFor())}{}`,
    // A refused payload whose text is hostile: the refusal carries no text at
    // all, so nothing an attacker wrote can reach a screen through it.
    JSON.stringify({ ...claimsFor(), organisation: '<script>alert(1)</script>', seats: '5' }),
  ]) {
    const bytes = Buffer.from(payload, 'utf8');
    const key = spell(acme.id, bytes, edSign(null, bytes, acme.privateKey));
    const refusal = refusalOf(verifier(acme.keyring).verify(key, at(ISSUED)));

    assert.deepEqual(refusal, { ok: false, code: LICENCE_CODES.CLAIMS, field: 'claims' }, payload);
  }
});

test('a signature that is not 64 bytes is refused', () => {
  const acme = signer('acme-2026');
  const short = spell(acme.id, Buffer.from(JSON.stringify(claimsFor()), 'utf8'), Buffer.alloc(32, 7));

  // The port refuses wrong lengths by throwing, as the dependency does, so the
  // refusal below is the length guard's own: without the guard this case fails
  // with the port's throw instead of passing for the wrong reason.
  assert.throws(() => nodeEd25519(Buffer.alloc(32, 7), Buffer.alloc(0), acme.raw), RangeError);
  assert.equal(refusalOf(verifier(acme.keyring).verify(short, at(ISSUED))).code, LICENCE_CODES.SIGNATURE);
});

test('a payload that is not UTF-8 is refused as claims, and never thrown from', () => {
  const acme = signer('acme-2026');

  // The bytes go *inside a string value* of an otherwise valid claim set, signed
  // as it is spelled. Bare bytes would prove nothing about the decoder: a payload
  // that is not JSON is refused by the parser whatever the decoder did, and the
  // overlong and surrogate forms below decode to text a parser takes -- `A`, and
  // a lone surrogate -- so a claim set carrying them is one this verifier would
  // read if the decoder let them through.
  const PLACEHOLDER = 'organisation-here';
  const withRawOrganisation = (bytes) => {
    const json = JSON.stringify(claimsFor({ organisation: PLACEHOLDER }));
    const at = json.indexOf(PLACEHOLDER);
    return Buffer.concat([
      Buffer.from(json.slice(0, at), 'utf8'),
      Buffer.from(bytes),
      Buffer.from(json.slice(at + PLACEHOLDER.length), 'utf8'),
    ]);
  };

  for (const bytes of [
    // Overlong forms of a code point a shorter form carries, both of which decode
    // to `A` -- so an accepted payload here is a claim set read from bytes that
    // are not the text they claim to be.
    [0xe0, 0x81, 0x81],
    [0xf0, 0x80, 0x81, 0x81],
    [0xc0, 0xaf],
    // A surrogate, which a parser accepts as a lone code unit and no text is.
    [0xed, 0xa0, 0x80],
    // Past U+10FFFF: `F4 90 80 80` decodes to U+110000, which
    // `String.fromCodePoint` throws on -- the shape a bound on the first byte
    // alone lets through, so a refusal here is what keeps the module total.
    [0xf4, 0x90, 0x80, 0x80],
    [0xf4, 0xbf, 0xbf, 0xbf],
    // A first byte that starts no sequence at all.
    [0xf5, 0x80, 0x80, 0x80],
    [0x80],
    // A sequence cut short by the quote that closes the string.
    [0xe2, 0x82],
  ]) {
    const payload = withRawOrganisation(bytes);
    const key = spell(acme.id, payload, edSign(null, payload, acme.privateKey));

    // The signature is good and the refusal is the text's, not the signature's:
    // a failed signature would answer `signature` and never reach the decoder.
    assert.deepEqual(
      refusalOf(verifier(acme.keyring).verify(key, at(ISSUED))),
      { ok: false, code: LICENCE_CODES.CLAIMS, field: 'claims' },
      bytes.map((byte) => byte.toString(16)).join(' '),
    );
    assert.equal(verifier(acme.keyring).latest([key], at(ISSUED)), null);
  }

  // The control: the same splice with a sequence that *is* UTF-8 verifies and
  // reads back as the character it spells, so the refusals above are the bytes'
  // and not the splice's.
  const control = withRawOrganisation([0xe2, 0x82, 0xac]);
  const controlKey = spell(acme.id, control, edSign(null, control, acme.privateKey));
  assert.equal(verified(verifier(acme.keyring).verify(controlKey, at(ISSUED))).claims.organisation, '€');
});

test('a key that is not three parts is refused, and one surrounded by whitespace is read', () => {
  const acme = signer('acme-2026');
  for (const key of ['', 'a.b', 'a.b.c.d', null, 42]) {
    assert.equal(refusalOf(verifier(acme.keyring).verify(key, at(ISSUED))).code, LICENCE_CODES.MALFORMED, String(key));
  }

  // An empty part is a part that is not a part: `id..sig` is refused with no
  // part named, because the shape is what failed.
  const empty = `${acme.id}..${b64url('x')}`;
  assert.deepEqual(verifier(acme.keyring).verify(empty, at(ISSUED)), {
    ok: false,
    code: LICENCE_CODES.MALFORMED,
    field: null,
  });

  // A key arrives from a paste or from a file's line, and neither adds meaning
  // to the whitespace around it.
  assert.equal(verifier(acme.keyring).verify(`  ${mint(acme, claimsFor())}\n`, at(ISSUED)).ok, true);
});

test('an id naming a property of every object is refused: a keyring is a plain object', () => {
  const acme = signer('acme-2026');
  for (const id of ['constructor', 'toString', '__proto__', 'hasOwnProperty']) {
    const refusal = refusalOf(verifier(acme.keyring).verify(mint(acme, claimsFor(), id), at(ISSUED)));
    assert.deepEqual(refusal, { ok: false, code: LICENCE_CODES.UNKNOWN_KEY_ID, field: 'id' }, id);
  }

  // The guard and not a plain `keyring[id]`: the one ring that tells the two
  // apart is a ring whose *prototype* carries a real public key under the id.
  // That id is not an entry this ring holds, so the key is refused -- where a
  // plain lookup would find that key and verify a key minted by the other
  // keypair, which is no key this build holds at all.
  const inherited = signer('prod-inherited');
  const ring = Object.create(inherited.keyring);
  assert.equal(Object.hasOwn(ring, inherited.id), false);
  assert.deepEqual(verifier(ring).verify(mint(inherited, claimsFor()), at(ISSUED)), {
    ok: false,
    code: LICENCE_CODES.UNKNOWN_KEY_ID,
    field: 'id',
  });
});

test('a ring entry that is not a public key is refused, and never thrown from', () => {
  const acme = signer('acme-2026');
  // The ring is the build's own and S2.9 hand-edits it, so an entry that is not
  // an Ed25519 public key is a defect in this build rather than in the key --
  // and it is still a refusal, because a screen cannot catch a throw: a ring
  // entry the port would throw on never reaches the port.
  for (const entry of [
    'not base64url at all!',
    b64url(Buffer.alloc(31, 3)),
    b64url(Buffer.alloc(33, 3)),
    '',
    null,
    42,
  ]) {
    const id = 'prod-defect';
    const key = mint(acme, claimsFor(), id);
    assert.deepEqual(verifier({ [id]: entry }).verify(key, at(ISSUED)), {
      ok: false,
      code: LICENCE_CODES.UNKNOWN_KEY_ID,
      field: 'id',
    }, String(entry));
  }
});

test('the verifier reads no clock: a `now` that is not a Date is a caller that will never work', () => {
  const acme = signer('acme-2026');
  const key = mint(acme, claimsFor());
  for (const now of [undefined, null, ISSUED, 'now']) {
    assert.throws(() => verifier(acme.keyring).verify(key, now), TypeError, String(now));
  }
  // ... and a verifier built without the primitive it needs is the same kind of
  // caller: it fails at the call rather than answering something it cannot know.
  assert.throws(() => createLicenceVerifier({}), TypeError);
  assert.throws(() => createLicenceVerifier({ ed25519: 'nope' }), TypeError);
});

// --- the module itself -----------------------------------------------------

/** The two files of this module, as text: the scan below reads them rather than
 * importing them, because `index.js` names the Ed25519 dependency and this
 * suite runs with no `node_modules`. */
const VERIFY_SOURCE = readFileSync(join(HERE, 'verify.js'), 'utf8');
const INDEX_SOURCE = readFileSync(join(HERE, 'index.js'), 'utf8');

/** importSpecifiers lists the module specifiers one source file names: static
 * imports, re-exports, side-effect imports, dynamic imports and `require`. */
function importSpecifiers(text) {
  const found = [];
  for (const re of [
    /\b(?:import|export)\s[^'"`;]*?\bfrom\s*['"]([^'"]+)['"]/g,
    /\bimport\s*['"]([^'"]+)['"]/g,
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
    /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  ]) {
    for (const match of text.matchAll(re)) found.push(match[1]);
  }
  return found;
}

/** The modules a verifier could phone home through. */
const NETWORK_MODULES = ['node:http', 'node:http2', 'node:https', 'node:net', 'node:tls', 'node:dns', 'node:dgram'];

test('the primitive imports nothing at all, so it reaches no network, no clock and no environment', () => {
  // The property the app's suite rests on (`mobile/README.md`, "Running the
  // tests"): no test imports a package, so a single import here would need an
  // install to run the suite -- and the same absence is what makes "this
  // verifier never phones home" a fact rather than a promise. The patterns are
  // anchored to a statement rather than to the word, because this file's prose
  // discusses imports and a substring search over comments is how a scan comes
  // to flag the sentence that explains it.
  assert.equal(/^\s*import\s/m.test(VERIFY_SOURCE), false, 'verify.js must import nothing');
  assert.equal(/\brequire\s*\(/.test(VERIFY_SOURCE), false, 'verify.js must require nothing');
  assert.equal(/^\s*\}?\s*from\s+['"]/m.test(VERIFY_SOURCE), false, 'verify.js must import from nothing');

  // The rules that absence exists for, as the tokens a change would need. The
  // scan is shown a plant it must catch before the file is trusted.
  for (const [rule, plant] of [
    [/fetch\s*\(|XMLHttpRequest|WebSocket|EventSource/, "fetch('https://example.invalid/key')"],
    [/\bprocess\.env\b/, 'const key = process.env.FUGARO_LICENCE_KEY'],
    [/\bDate\.now\s*\(|new Date\s*\(/, 'const now = Date.now()'],
  ]) {
    assert.equal(rule.test(plant), true, `the scan must catch ${plant}`);
    assert.equal(rule.test(VERIFY_SOURCE), false, `verify.js carries ${rule}`);
  }
});

test('the door names one package and its own two files, and the package is the Ed25519 dependency', () => {
  const specifiers = importSpecifiers(INDEX_SOURCE);
  assert.ok(specifiers.length > 0, 'the scan found no imports: it no longer reads the file');

  // `index.js` is the one file under `src/licence/` that names a package, and
  // it names exactly one: the Ed25519 primitive the verifier takes as a port.
  // The suite never loads this file, so nothing here is a claim a test can make
  // by running it -- the scan is the claim.
  assert.deepEqual(specifiers.filter((specifier) => !specifier.startsWith('.')), ['@noble/curves/ed25519.js']);
  // The two files the door is the door TO: the verifier (S2.5) and the store
  // (S2.8), and each is named where it belongs -- the store once for the factory
  // and once for the names it re-exports. The set is exact on purpose -- a third
  // module reached through the door is a decision, and this is where it is made.
  assert.deepEqual(
    [...new Set(specifiers.filter((specifier) => specifier.startsWith('.')))].sort(),
    ['./store.js', './verify.js'],
  );
  assert.deepEqual(specifiers.filter((specifier) => NETWORK_MODULES.includes(specifier)), []);
  assert.equal(/\bprocess\.env\b/.test(INDEX_SOURCE), false, 'the door reads no environment either');

  // The one line this suite cannot run: the door is where the package is named,
  // and the suite has no `node_modules` to load it from, so the wiring is held
  // as text. It is the line that decides which of the two Ed25519 readings the
  // app takes -- RFC 8032, like the CLI's `ed25519.Verify` and the console's
  // OpenSSL, rather than ZIP-215, which is the library's default and accepts a
  // signature over a non-canonical encoding or a small-order key -- and the
  // argument order the design names. No shared vector can pin it: the
  // padding-bits vector is refused before the primitive is reached.
  assert.ok(
    INDEX_SOURCE.includes('ed25519.verify(signature, message, publicKey, { zip215: false })'),
    'the door calls the dependency with the design’s argument order and strict RFC 8032',
  );

  // The scan catches a planted network import, so a green run above means
  // something.
  assert.deepEqual(
    importSpecifiers("import { request } from 'node:https'\nimport { connect } from 'node:net'\n").filter((specifier) =>
      NETWORK_MODULES.includes(specifier),
    ),
    ['node:https', 'node:net'],
  );
});

test('the door hands out a store bound to this build’s verifier, and not the class', () => {
  // The door withholds two constructors, and for one reason: `verify.js`'s
  // factory takes a keyring and `store.js`'s class takes a verifier, so either
  // one handed out is a caller choosing the keys this app trusts. The store is
  // therefore reached as a factory over the port, with the verifier the door
  // holds -- asserted as the two lines that make that true, because the suite
  // cannot load this file (no `node_modules`) and because a later export of the
  // class would read as convenient rather than as the hole it is.
  assert.ok(
    INDEX_SOURCE.includes('export function createLicenceStore(port)'),
    'the door exports a store built over the port alone',
  );
  assert.ok(
    INDEX_SOURCE.includes('new LicenceStore({ port, verifier: LICENCE_VERIFIER })'),
    'and that factory is where this build’s verifier is bound',
  );

  // The scan catches a re-export of the class, so a green run above means
  // something. The pattern is anchored to an export statement: the door's prose
  // names `LicenceStore` on purpose, to say why it is not exported.
  const exportsTheClass = (text) => /(?:export\s*\{[^}]*\bLicenceStore\b[^}]*\}|export\s+class\s+LicenceStore\b)/.test(text);
  assert.equal(exportsTheClass("export { LICENCE_KEYS_MAX, LicenceStore } from './store.js';"), true, 'the scan must catch a re-export');
  assert.equal(exportsTheClass('export class LicenceStore {}'), true, 'and a class declared here');
  assert.equal(exportsTheClass(INDEX_SOURCE), false, 'the door exports no class that takes a verifier');
});
