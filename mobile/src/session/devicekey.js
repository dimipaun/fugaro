// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The device key: generated on the phone, kept in platform secure storage,
// software because M0's U5 found no hardware key (task M3; O6, U5).
//
// **What the key is.** O6 replaces D171's per-pairing MAC key with a keypair
// the device generates and never discloses: the private half signs, the public
// half travels once at pairing and is what a refresh token is bound to, so a
// stolen token is useless without the device. The signature is over C1's
// `signatureMessage(purpose, value)` and it is Ed25519, because the service
// verifies it with the platform's own Ed25519 and a phone that signed with
// something else would be a phone whose every refresh is refused.
//
// **Software, and that is M0's answer.** U5 asked whether a stock Expo package
// can mint a hardware-backed key, and Check 733's spike found none
// (`mobile/spike/README.md`, `spike/src/devicekey/software.js`), so the plan's
// fallback is what this module implements: pure-JavaScript Ed25519 over a seed
// in the platform's own secure storage. A hardware key would be stronger, and
// the seed being exportable is the price of not having one -- named here rather
// than discovered later, because it is the one property a reviewer of this file
// has to know.
//
// **The primitive is a port, and the door takes it.** `@noble/curves` is the
// app's Ed25519 (`spike/src/devicekey/software.js`), and it is the one
// dependency this module would otherwise carry into every test: a test that
// loaded it would need `node_modules`, which this tree's tests deliberately do
// not have. So the four methods this module uses -- `randomSecretKey`,
// `getPublicKey`, `sign`, `verify` -- are handed in by the caller, and
// `devicekey.test.js` hands in the platform's own Ed25519 (`node:crypto`),
// which is an independent reader of the same wire format: the bytes this
// module's key produces are checked against Node's verifier rather than against
// the primitive that made them.
//
// **The seed is a `Secret`.** `secret.js` is what makes a credential
// unprintable, and the private seed is the one value in this module that must
// never reach a log line: it is wrapped on the way in and on the way out, and
// `reveal()` -- the one way to the material -- is called once, here, at the
// moment the seed is handed to the primitive. `src/boundary.test.js`'s rule 2
// carries this file on its allow-list for that one call, deliberately, the same
// way rule 6 carries `refresh.js` for the network.

import { SECURE_STORE_ITEMS } from '../../../shared/mobile-session/index.js';
import { DEVICE_KEY_ITEM, secret } from '../accounts/secret.js';
import { decodeBase64url, encodeBase64url } from '../pairing/base64url.js';

import { isProjectKeyId } from './refresh.js';

/** The algorithm, which the service verifies with: Ed25519 and nothing else. */
export const DEVICE_KEY_ALGORITHM = 'Ed25519';

/** The seed's length, which is what the app generates and stores. */
export const DEVICE_KEY_SEED_BYTES = 32;

/** The public key's length, which is what pairing discloses (C1's `DevicePublicKeySize`). */
export const DEVICE_PUBLIC_KEY_BYTES = 32;

/** The signature's length, which is what a refresh presents (C1's `DeviceSignatureSize`). */
export const DEVICE_SIGNATURE_BYTES = 64;

/** The prefix every device key's item carries, read from C1 rather than spelled again. */
export const DEVICE_KEY_PREFIX = SECURE_STORE_ITEMS.DEVICE_KEY;

/** The refusal codes this module returns. */
export const DEVICE_KEY_CODES = Object.freeze({
  /** A project id that cannot name a keychain item. */
  BAD_PROJECT: 'bad-project',
  /** The Ed25519 primitive is missing a method this module uses. */
  BAD_PRIMITIVE: 'bad-primitive',
  /** A key is already held for this project: a second one is a new device, not a rotation. */
  ALREADY_HELD: 'already-held',
  /** No key is held for this project. */
  ABSENT: 'absent',
  /** A stored seed is not one this module could have written. */
  UNREADABLE: 'unreadable',
  /** The platform store refused the write or the delete. */
  STORE_FAILED: 'store-failed',
  /** Something that is not bytes was handed to `sign`. */
  BAD_BYTES: 'bad-bytes',
  /** The primitive answered a signature that is not one. */
  BAD_SIGNATURE: 'bad-signature',
});

/** A frozen refusal, in the shape the tree's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** The four methods this module uses, and the check that all of them are there. */
function isPrimitive(ed25519) {
  return (
    ed25519 !== null &&
    typeof ed25519 === 'object' &&
    typeof ed25519.utils?.randomSecretKey === 'function' &&
    typeof ed25519.getPublicKey === 'function' &&
    typeof ed25519.sign === 'function'
  );
}

/**
 * The key one project's device key lives under. Throws on an id it cannot
 * compose, for the reason `projectSessionItem` does: the id becomes the second
 * half of a keychain key, and a key built from an unchecked id points at
 * another project's item.
 */
export function deviceKeyItem(projectId) {
  if (!isProjectKeyId(projectId)) throw new TypeError('not a project id');
  return `${DEVICE_KEY_PREFIX}${projectId}`;
}

/** The seed bytes `stored` names, or null when it is not that spelling. */
function seedOf(stored) {
  // The value is wrapped first: it came from the platform, and a value this
  // module could not have written is refused rather than decoded leniently.
  const wrapped = secret(DEVICE_KEY_ITEM, stored);
  if (!wrapped.ok) return null;
  const decoded = decodeBase64url(wrapped.secret.reveal(), DEVICE_KEY_SEED_BYTES);
  return decoded.ok ? decoded.bytes : null;
}

/**
 * The device's Ed25519 key for one project, in platform secure storage.
 *
 * The seed is stored in the one spelling this tree uses for a key, unpadded
 * base64url, and the public half is derived rather than stored: a stored public
 * key is a second source of a value that must match the private one, and the
 * two disagreeing is a device whose signatures verify nowhere.
 *
 * **A second `create` is refused rather than replacing the key.** Replacing a
 * key would leave every token already bound to the old one dead, and the phone
 * would look paired while every refresh failed; the caller that wants a new key
 * deletes the old one first, which is a decision it makes on purpose.
 */
export class DeviceKeyStore {
  #port;
  #ed25519;

  constructor({ port, ed25519 } = {}) {
    if (port === null || typeof port !== 'object') throw new TypeError('a secure-store port is required');
    if (!isPrimitive(ed25519)) throw new TypeError('an Ed25519 primitive is required');
    this.#port = port;
    this.#ed25519 = ed25519;
  }

  /** Read one project's seed back, as bytes -- or the refusal that explains why not. */
  async #seed(projectId) {
    let stored;
    try {
      stored = await this.#port.getItem(deviceKeyItem(projectId));
    } catch {
      return refuse(DEVICE_KEY_CODES.STORE_FAILED, 'projectId');
    }
    if (stored === null || stored === undefined) return refuse(DEVICE_KEY_CODES.ABSENT, 'projectId');
    const seed = seedOf(stored);
    if (seed === null) return refuse(DEVICE_KEY_CODES.UNREADABLE, 'projectId');
    return Object.freeze({ ok: true, seed });
  }

  /**
   * Generate a key for `projectId` and return its public half. The seed goes to
   * the keychain and nowhere else: what comes back is the public key, which is
   * what pairing discloses.
   */
  async create(projectId) {
    if (!isProjectKeyId(projectId)) return refuse(DEVICE_KEY_CODES.BAD_PROJECT, 'projectId');
    let existing;
    try {
      existing = await this.#port.getItem(deviceKeyItem(projectId));
    } catch {
      return refuse(DEVICE_KEY_CODES.STORE_FAILED, 'projectId');
    }
    if (existing !== null && existing !== undefined) return refuse(DEVICE_KEY_CODES.ALREADY_HELD, 'projectId');
    const seed = this.#ed25519.utils.randomSecretKey();
    if (!(seed instanceof Uint8Array) || seed.length !== DEVICE_KEY_SEED_BYTES) {
      return refuse(DEVICE_KEY_CODES.BAD_PRIMITIVE, 'seed');
    }
    const publicKey = this.#ed25519.getPublicKey(seed);
    if (!(publicKey instanceof Uint8Array) || publicKey.length !== DEVICE_PUBLIC_KEY_BYTES) {
      return refuse(DEVICE_KEY_CODES.BAD_PRIMITIVE, 'publicKey');
    }
    try {
      await this.#port.setItem(deviceKeyItem(projectId), encodeBase64url(seed));
    } catch {
      // Loud, and what the platform said is not carried: it may quote the value.
      return refuse(DEVICE_KEY_CODES.STORE_FAILED, 'projectId');
    }
    return Object.freeze({ ok: true, publicKey });
  }

  /** One project's public key, derived from the held seed. */
  async publicKey(projectId) {
    if (!isProjectKeyId(projectId)) return refuse(DEVICE_KEY_CODES.BAD_PROJECT, 'projectId');
    const held = await this.#seed(projectId);
    if (!held.ok) return held;
    const publicKey = this.#ed25519.getPublicKey(held.seed);
    if (!(publicKey instanceof Uint8Array) || publicKey.length !== DEVICE_PUBLIC_KEY_BYTES) {
      return refuse(DEVICE_KEY_CODES.BAD_PRIMITIVE, 'publicKey');
    }
    return Object.freeze({ ok: true, publicKey });
  }

  /**
   * Sign `message` with the key held for `projectId`.
   *
   * `message` must be bytes: the message a device key signs is C1's
   * `signatureMessage(...)`, which is bytes and never text, and a string handed
   * here would be signed as UTF-8 while the service signed it as something else.
   */
  async sign(projectId, message) {
    if (!isProjectKeyId(projectId)) return refuse(DEVICE_KEY_CODES.BAD_PROJECT, 'projectId');
    if (!(message instanceof Uint8Array)) return refuse(DEVICE_KEY_CODES.BAD_BYTES, 'message');
    const held = await this.#seed(projectId);
    if (!held.ok) return held;
    const signature = this.#ed25519.sign(message, held.seed);
    if (!(signature instanceof Uint8Array) || signature.length !== DEVICE_SIGNATURE_BYTES) {
      return refuse(DEVICE_KEY_CODES.BAD_SIGNATURE, 'signature');
    }
    return Object.freeze({ ok: true, signature });
  }

  /**
   * Forget one project's key. **The delete is never gated on a read**, for the
   * reason `ProjectSessionStore.delete` gives: a read fails exactly when the
   * device is locked, and skipping then would leave a key with no owner.
   */
  async delete(projectId) {
    if (!isProjectKeyId(projectId)) return refuse(DEVICE_KEY_CODES.BAD_PROJECT, 'projectId');
    try {
      await this.#port.deleteItem(deviceKeyItem(projectId));
    } catch {
      return refuse(DEVICE_KEY_CODES.STORE_FAILED, 'projectId');
    }
    return Object.freeze({ ok: true });
  }
}
