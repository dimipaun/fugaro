// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's offline licence-key verifier (subscriptions plan S2.5; the design
// is docs/licensing/licence-key.md, the S2.1 note, and S2.3's Go verifier and
// the console's S2.4 are this module's siblings).
//
// One Ed25519 signature over a JSON claim set, in three base64url parts, in
// this order:
//
//     <key id>.<payload>.<signature>
//
// Four rules of the design shape the code below, and each is a case in
// `verify.test.js` rather than a reading of this comment:
//
//   1. **The key id leads, in the clear.** The verifier has to choose the public
//      key before it can verify anything, so an id the keyring does not hold is
//      refused *before either other part is decoded* -- which is what makes a
//      compiled-in public key rotate, and what refuses a staging key in a
//      production build rather than half-accepting one. The id is the one part
//      **nothing decodes**: it is compared, and looked up, as the text it is
//      written as, so a ring is a table of ids and not of decoded names. Its
//      spelling is still held to the base64url alphabet with the shape, because
//      the design says the id is spelled in it -- a text outside the alphabet is
//      not an id this build simply does not hold.
//   2. **The raw bytes before the claims.** The signature is verified over the
//      payload's bytes as spelled, and only then are they read as a claim set.
//      Re-wrapping, re-ordering or re-encoding a claim set is a different byte
//      string, so it fails the signature instead of verifying as the same
//      claims; and no claim is read out of a payload whose signature did not
//      verify, because a claim from an unverified payload is text an attacker
//      chose. A refusal here is therefore a code and the part that failed, and
//      never a claim or a sentence with one in it -- the app has no logger and
//      no sanitiser in this module, and a screen turns the code into its own
//      sentence (S6.3).
//   3. **Three states, and a bound.** Against `now`: `valid` to `expires`,
//      `grace` for the 30 days after it, `expired` past that. A key outside all
//      three -- a bad signature, an unknown id, a term longer than its tier
//      allows -- is refused rather than given a state. A key past its grace is
//      still *read*, because the lapse is a state the app draws (S6.1) and not
//      an error.
//   4. **Two keys may be installed, and the later `expires` applies** (T26):
//      the next key issues 30 days before the current one expires, so a renewal
//      landing early changes nothing for the user. Which of several installed
//      keys applies is the store's choice (the design says so); `latest` is that
//      choice as a function of the texts, so the store (S2.8) has no rule of its
//      own to get wrong.
//
// **The Ed25519 primitive is a port, and that is what keeps the app's suite
// install-free.** `mobile/README.md` promises `npm test` runs in a fresh clone
// with no `node_modules`, so no module under `src/` may name a package: the
// primitive arrives as `{ ed25519 }` from `index.js`, the one file here that
// names the app's dependency, exactly as the mailbox's store arrives from
// `mobile/index.js`. This file **imports nothing at all**, which is the same
// rule `mailbox/sha256.js` keeps and for a second reason here: a verifier that
// never phones home is a fact about its imports, and a scan in the test file
// reads them back out and holds them to it.
//
// **What is deliberately not here.** No store and no file: where the app keeps
// the key is S2.8's. No screen and no lapse state: what the app still allows
// after the grace is S6.1's design and S6.3's build. No production key:
// `PRODUCTION_KEYRING` below is where S2.9 puts the owner's published public
// half, once S3.8 has created it, and until then nothing verifies in a real
// build -- which is the honest state of an app that carries no key enforcement.
// No vectors: one set of vectors for all three verifiers is S2.6's.
//
// **One reading is stricter than the CLI's, on purpose and in the safe
// direction.** The six claims are all required and all typed, `issued`,
// `expires` and `seats` are whole non-negative numbers, and a claim this
// verifier does not know is refused rather than ignored. The last of those is
// the Go verifier's rule and the shared vectors carry a `covers_until` case for
// it; the rest is the console's reading (S2.4) and not Go's, which takes a
// missing claim as its zero value. Every key a minter writes carries all six
// with values inside those bounds, so the two readings differ only on keys no
// minter writes, and where they differ this one refuses instead of reading a
// figure the CLI would read differently. S2.6, the shared-vectors task, is where
// the three are held to one answer.

/** GRACE_DAYS is the grace that follows `expires` before the paid side lapses
 * (B17, D436). */
export const GRACE_DAYS = 30;

const DAY_SECONDS = 86_400;

/** TIER_MAX_LIFETIME_DAYS is the longest term each tier's key may be minted
 * for: the tier's term plus the 30-day issue lead the renewal above needs,
 * because that is the longest key the design mints -- an annual renewal is
 * minted 30 days before the key it follows expires, so a ceiling of the bare
 * term would refuse the very renewal the overlap exists for. The maxima are the
 * S2.1 note's numbers and not the Terms': a tier can be granted a longer one
 * here, in a decision, without a change to the key format. */
export const TIER_MAX_LIFETIME_DAYS = Object.freeze({
  community: 365 + GRACE_DAYS,
  team: 365 + GRACE_DAYS,
  enterprise: 365 + GRACE_DAYS,
});

/** LICENCE_CODES is what a refusal answers with, and the five names are the
 * design's: the shared vectors file spells each of them, so a code is a word
 * three verifiers agree on and not a local error string. */
export const LICENCE_CODES = Object.freeze({
  /** The key text is not three non-empty base64url parts, or a part does not
   * decode canonically. */
  MALFORMED: 'malformed',
  /** The id is not one this build's keyring holds, which includes a staging
   * key's and an entry that is not an Ed25519 public key. */
  UNKNOWN_KEY_ID: 'unknown-key-id',
  /** The signature does not verify over the payload's raw bytes. */
  SIGNATURE: 'signature',
  /** A verified payload this verifier cannot read as a claim set. */
  CLAIMS: 'claims',
  /** A term longer than its tier allows. */
  LIFETIME: 'lifetime',
});

/** PRODUCTION_KEYRING is the ring the shipped app verifies against, keyed by
 * the id the key text carries in the clear and holding each public key as
 * unpadded base64url of its 32 raw bytes -- the spelling the vectors file
 * carries, so one string moves between the three verifiers unchanged. It is
 * empty today because the owner's signing keys do not exist yet: S3.8 creates
 * them, S2.9 puts the published production public half in here. */
export const PRODUCTION_KEYRING = Object.freeze({});

/** TIERS is the closed list the `tier` claim is checked against. */
const TIERS = Object.freeze(['community', 'team', 'enterprise']);

/** BASE64URL is the spelling every part uses: the alphabet, unpadded. A part
 * with a character outside it -- a `+`, a `/`, a pad -- is not a part this
 * verifier reads. */
const BASE64URL = /^[A-Za-z0-9_-]+$/;

/** ALPHABET is that alphabet in value order, so a character's index is its six
 * bits. The decoder is this table rather than a platform function, for the
 * reason the mailbox's SHA-256 is the app's own arithmetic: Hermes is the engine
 * the app runs on, and a primitive the format depends on is one the app should
 * not have to hope for. */
const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/** VALUES is the table above, read backwards: -1 for a character outside the
 * alphabet, and `undefined` for a code point past the table. */
const VALUES = (() => {
  const table = new Int16Array(128).fill(-1);
  for (let i = 0; i < ALPHABET.length; i++) table[ALPHABET.charCodeAt(i)] = i;
  return table;
})();

/** ED25519_PUBLIC_KEY_BYTES and ED25519_SIGNATURE_BYTES are the sizes the
 * primitive takes. They are checked here rather than left to the port, because
 * a ring entry or a signature of another size is a refusal in this module's
 * vocabulary -- and a port handed a 31-byte key is a port that throws, which is
 * a value a screen cannot branch on. */
const ED25519_PUBLIC_KEY_BYTES = 32;
const ED25519_SIGNATURE_BYTES = 64;

/** refuse is the shape every refusal in this directory has. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * decodeBase64url reads one part, canonically or not at all.
 *
 * The leftover-bits check is the rule and not a formality: a decoder ignores the
 * bits a part's length leaves over, so two spellings can decode to the same
 * bytes. A verifier that took the second spelling would accept a signature the
 * minter never wrote -- and the shared vectors carry exactly that case, a
 * payload spelled with its padding bits set.
 *
 * @param {unknown} text a part as it is written
 * @returns {Uint8Array | null} the bytes, or null for a spelling that is not
 *   base64url or is not the canonical one
 */
function decodeBase64url(text) {
  if (typeof text !== 'string' || !BASE64URL.test(text) || text.length % 4 === 1) return null;
  const bytes = new Uint8Array(Math.floor((text.length * 3) / 4));
  let at = 0;
  let bits = 0;
  let carry = 0;
  for (let i = 0; i < text.length; i++) {
    const value = VALUES[text.charCodeAt(i)];
    if (value === undefined || value < 0) return null;
    carry = (carry << 6) | value;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes[at++] = (carry >>> bits) & 0xff;
    }
  }
  if (bits > 0 && (carry & ((1 << bits) - 1)) !== 0) return null;
  return bytes;
}

/**
 * decodePublicKey reads one keyring entry: the 32 raw bytes of an Ed25519
 * public key, or nothing.
 *
 * The ring is the build's own and S2.9 hand-edits it, so an entry that is not a
 * public key is a defect in this app rather than in the key -- and it is still a
 * refusal, because a screen cannot catch a throw: an entry the port would throw
 * on never reaches the port.
 *
 * @param {unknown} entry the ring's value for one id
 * @returns {Uint8Array | null}
 */
function decodePublicKey(entry) {
  const bytes = decodeBase64url(entry);
  return bytes !== null && bytes.length === ED25519_PUBLIC_KEY_BYTES ? bytes : null;
}

/**
 * textOf decodes the UTF-8 bytes of a payload, or nothing.
 *
 * Strict, like the console's `TextDecoder` with `fatal`: an overlong form, a
 * surrogate, a truncated sequence or a byte that cannot appear at all is not
 * text, and a payload that is not text is not a claim set. It is written here
 * rather than taken from the platform for the same reason the base64 decoder is.
 *
 * It is also total: every byte string is text or nothing, so a refusal stays a
 * value. The bound on the last form matters for that -- `F4 90 80 80` decodes to
 * U+110000, and `String.fromCodePoint` throws on it rather than answering.
 *
 * @param {Uint8Array} bytes
 * @returns {string | null}
 */
function textOf(bytes) {
  let out = '';
  for (let i = 0; i < bytes.length; ) {
    const first = bytes[i];
    let codePoint;
    let length;
    if (first < 0x80) {
      codePoint = first;
      length = 1;
    } else if (first >= 0xc2 && first <= 0xdf) {
      codePoint = first & 0x1f;
      length = 2;
    } else if (first >= 0xe0 && first <= 0xef) {
      codePoint = first & 0x0f;
      length = 3;
    } else if (first >= 0xf0 && first <= 0xf4) {
      codePoint = first & 0x07;
      length = 4;
    } else {
      // 0x80..0xc1 and 0xf5..0xff start no sequence: the first is a
      // continuation byte or an overlong form, the second is past U+10FFFF.
      return null;
    }
    if (i + length > bytes.length) return null;
    for (let j = 1; j < length; j++) {
      const next = bytes[i + j];
      if (next < 0x80 || next > 0xbf) return null;
      codePoint = (codePoint << 6) | (next & 0x3f);
    }
    // The forms whose first byte admits a shorter encoding of the same code
    // point, and the ones the standard puts outside the range at all: an overlong
    // sequence, a surrogate, and the 4-byte forms past U+10FFFF (F4 90.. and
    // above, which is the shape a bound on the first byte alone lets through).
    if (length === 3 && codePoint < 0x800) return null;
    if (length === 4 && codePoint < 0x10000) return null;
    if (codePoint > 0x10ffff) return null;
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) return null;
    out += String.fromCodePoint(codePoint);
    i += length;
  }
  return out;
}

/** unixSeconds is the shape the two time claims have: a whole number of
 * seconds, never negative. */
function unixSeconds(value) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

/**
 * claimsOf reads the claim set out of a payload whose signature has already
 * verified.
 *
 * A claim this verifier does not know is **refused**, and that is the Go
 * verifier's rule rather than the console's: adding a claim is how the format
 * grows, and the shared vectors carry a key with `covers_until` that all three
 * verifiers must refuse (O4 dropped it). A claim it knows and cannot read is the
 * whole payload refused, because a key whose seats or organisation is a string
 * is not a key any decision here can be made about.
 *
 * @param {string} payload the decoded payload text
 * @returns {object | null} the claims, frozen, or null
 */
function claimsOf(payload) {
  let parsed;
  try {
    parsed = JSON.parse(payload);
  } catch {
    return null; // not JSON at all, or bytes after the object
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null;

  // The object must carry exactly the six claims: a seventh name is a claim from
  // a format this verifier does not know and is refused here rather than
  // silently dropped, and each of the six is required by the type check under
  // its own name. `Object.keys` lists an own `__proto__` a payload carries, so
  // it is refused like any other name.
  if (Object.keys(parsed).length !== 6) return null;

  const tier = parsed['tier'];
  if (typeof tier !== 'string' || !TIERS.includes(tier)) return null;

  const features = parsed['features'];
  if (!Array.isArray(features)) return null;
  const list = [];
  for (const feature of features) {
    if (typeof feature !== 'string') return null;
    list.push(feature);
  }

  const seats = parsed['seats'];
  if (!Number.isSafeInteger(seats) || seats < 0) return null;

  const organisation = parsed['organisation'];
  if (typeof organisation !== 'string' || organisation === '') return null;

  const issued = parsed['issued'];
  const expires = parsed['expires'];
  if (!unixSeconds(issued) || !unixSeconds(expires)) return null;

  return Object.freeze({
    tier,
    features: Object.freeze(list),
    seats,
    organisation,
    issued,
    expires,
  });
}

/** stateOf is the state table: `valid` to `expires`, `grace` for the 30 days
 * after it, `expired` past that. */
function stateOf(expires, nowSeconds) {
  if (nowSeconds <= expires) return 'valid';
  if (nowSeconds <= expires + GRACE_DAYS * DAY_SECONDS) return 'grace';
  return 'expired';
}

/** secondsOf is a `Date` as the Unix seconds the claims are written in. */
function secondsOf(now) {
  return Math.floor(now.getTime() / 1000);
}

/**
 * verifyOne reads one key text against one keyring at one moment.
 *
 * The order below is the design's and not an implementation choice: the id is
 * looked up before the other two parts are decoded, the signature is verified
 * over the payload's raw bytes before any of them is read as a claim set, and
 * the tier's lifetime bound is applied before the state is read, so a key
 * outside the three states is refused rather than given one.
 */
function verifyOne(raw, now, keyring, ed25519) {
  // A key arrives from a paste or from a file's line, and neither adds meaning
  // to the whitespace around it; whitespace inside one is still a part that is
  // not base64url.
  const parts = typeof raw === 'string' ? raw.trim().split('.') : [];
  if (parts.length !== 3 || parts[0] === '' || parts[1] === '' || parts[2] === '') {
    return refuse(LICENCE_CODES.MALFORMED, null);
  }
  const id = parts[0];
  if (!BASE64URL.test(id)) return refuse(LICENCE_CODES.MALFORMED, null);

  // `Object.hasOwn` and not a plain index: a keyring is a plain object, an id is
  // text an operator pasted, and `keyring['constructor']` is not a public key
  // this build holds. The id is looked up as the text it is, never decoded.
  if (!Object.hasOwn(keyring, id)) return refuse(LICENCE_CODES.UNKNOWN_KEY_ID, 'id');
  const publicKey = decodePublicKey(keyring[id]);
  if (publicKey === null) return refuse(LICENCE_CODES.UNKNOWN_KEY_ID, 'id');

  const payload = decodeBase64url(parts[1]);
  if (payload === null) return refuse(LICENCE_CODES.MALFORMED, 'payload');

  const signature = decodeBase64url(parts[2]);
  if (signature === null) return refuse(LICENCE_CODES.MALFORMED, 'signature');
  if (signature.length !== ED25519_SIGNATURE_BYTES) return refuse(LICENCE_CODES.SIGNATURE, 'signature');

  if (ed25519(signature, payload, publicKey) !== true) return refuse(LICENCE_CODES.SIGNATURE, 'signature');

  const text = textOf(payload);
  if (text === null) return refuse(LICENCE_CODES.CLAIMS, 'claims');
  const claims = claimsOf(text);
  if (claims === null) return refuse(LICENCE_CODES.CLAIMS, 'claims');

  if (claims.expires - claims.issued > TIER_MAX_LIFETIME_DAYS[claims.tier] * DAY_SECONDS) {
    return refuse(LICENCE_CODES.LIFETIME, 'expires');
  }

  return Object.freeze({ ok: true, claims, state: stateOf(claims.expires, secondsOf(now)) });
}

/**
 * createLicenceVerifier builds the app's verifier over the Ed25519 primitive it
 * is handed and the keyring it trusts.
 *
 * The port is `ed25519(signature, message, publicKey)` and answers whether the
 * signature verifies, both keys as the raw bytes the design's vectors carry.
 * The ring is the build's, and the default is the production one: a caller that
 * verifies against another ring has to name it in as many words.
 *
 * @param {{ ed25519: Function, keyring?: object }} ports
 * @returns {{ verify: Function, latest: Function }} frozen
 */
export function createLicenceVerifier({ ed25519, keyring = PRODUCTION_KEYRING } = {}) {
  if (typeof ed25519 !== 'function') {
    throw new TypeError('createLicenceVerifier: an ed25519 port is required');
  }
  if (typeof keyring !== 'object' || keyring === null) {
    throw new TypeError('createLicenceVerifier: a keyring is required');
  }

  /** nowOf is the clock as an argument, and a caller that did not pass one is a
   * caller that will never work: every state below is a fact about a key and a
   * moment, so this module reads no clock of its own. */
  const nowOf = (now) => {
    if (!(now instanceof Date) || Number.isNaN(now.getTime())) {
      throw new TypeError('the licence verifier takes a Date as its clock');
    }
    return now;
  };

  return Object.freeze({
    /**
     * verify reads one key text: its claims and the state they put the
     * installation in, or the refusal that says which part failed.
     *
     * @param {unknown} raw the key text, as pasted or as a file's line
     * @param {Date} now the moment to read it at
     * @returns {{ ok: true, claims: object, state: string } | { ok: false, code: string, field: string | null }}
     */
    verify(raw, now) {
      return verifyOne(raw, nowOf(now), keyring, ed25519);
    },

    /**
     * latest picks the key that applies out of the ones installed, or `null`
     * when none of them does.
     *
     * Two keys may be installed (T26), and the one with the later `expires` is
     * the one that applies: a renewal minted 30 days early has the later expiry,
     * so an issuing outage only bites once it lasts past 60 days. A key that is
     * refused is skipped rather than counted, and `null` covers both the empty
     * store and a store whose every key was refused -- neither is a fourth state
     * here, because what an installation with no usable key shows is S6.1's
     * design.
     *
     * @param {unknown} keys the installed key texts
     * @param {Date} now the moment to read them at
     * @returns {{ ok: true, claims: object, state: string } | null}
     */
    latest(keys, now) {
      const at = nowOf(now);
      if (!Array.isArray(keys)) throw new TypeError('latest: an array of key texts is required');
      let best = null;
      for (const key of keys) {
        const read = verifyOne(key, at, keyring, ed25519);
        if (!read.ok) continue;
        if (best === null || read.claims.expires > best.claims.expires) best = read;
      }
      return best;
    },
  });
}
