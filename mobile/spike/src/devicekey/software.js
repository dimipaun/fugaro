// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The software Ed25519 key, and the report U5 is read from (task M0; Check
// 733's U5; the reconciliation's U5 and the plan's M3).
//
// **What U5 asks, and what this file answers.** Whether this stack can hold a
// signing key that is hardware-backed and non-exportable -- Android Keystore
// (StrongBox), iOS Secure Enclave -- or whether the key is software and the
// plan has to say so. **No stock Expo package generates an asymmetric key in
// either platform's hardware**: `expo-secure-store` stores *items* (and stores
// them well -- its Android side is a Keystore-backed encrypted store), and a
// non-exportable key needs a native module whose Android side calls
// `KeyPairGenerator` with `setIsStrongBoxBacked` or whose iOS side calls
// `SecKeyCreateRandomKey` with `kSecAttrTokenIDSecureEnclave`. Naming that as
// the finding is the whole point of U5.
//
// **So the fallback is the one the plan names.** `@noble/curves` is already the
// app's Ed25519 dependency (mobile plan S2.5, the licence verifier), it is pure
// JavaScript with no native side, and a key it mints is a **software key**: its
// bytes are in the JavaScript heap, so it is exportable by definition and
// "hardware-backed" is a plain `no`. The report says exactly that, so a reading
// of U5 cannot be a claim the platform does not support.
//
// **The private bytes are never rendered.** They live in the returned key
// because a signature needs them; `report` copies the public half, a signature
// and a verification, and nothing else.

/** The one algorithm this route can offer, named as the plan names it. */
export const ALGORITHM = 'Ed25519';

/**
 * A fixed message, because a signature over a random one is a signature nobody
 * can compare: the draft records the message beside the signature, and the same
 * message on both phones makes two readings comparable.
 */
export const FIXED_MESSAGE = 'Fugaro Check 733 device-key probe';

/**
 * A string as UTF-8 bytes. Written out rather than reached for, for the reason
 * the app's own base64url decoder is: Hermes is the engine on the phone, and
 * `TextEncoder` is not a primitive this spike should have to hope for.
 */
export function utf8Bytes(text) {
  const bytes = [];
  for (const character of String(text)) {
    const code = character.codePointAt(0);
    if (code < 0x80) {
      bytes.push(code);
    } else if (code < 0x800) {
      bytes.push(0xc0 | (code >> 6), 0x80 | (code & 0x3f));
    } else if (code < 0x10000) {
      bytes.push(0xe0 | (code >> 12), 0x80 | ((code >> 6) & 0x3f), 0x80 | (code & 0x3f));
    } else {
      bytes.push(
        0xf0 | (code >> 18),
        0x80 | ((code >> 12) & 0x3f),
        0x80 | ((code >> 6) & 0x3f),
        0x80 | (code & 0x3f),
      );
    }
  }
  return Uint8Array.from(bytes);
}

/**
 * A fresh software key pair over `ed25519` -- the caller passes
 * `@noble/curves/ed25519.js`'s export, so this module has no dependency of its
 * own and a test can hand it the real one. `algorithm`, `hardwareBacked` and
 * `exportable` are the fallback's own answers, and they are constants of this
 * function rather than arguments: nothing in this spike can mint a key that is
 * any of those three things.
 */
export function generate(ed25519) {
  const privateKey = ed25519.utils.randomSecretKey();
  return {
    algorithm: ALGORITHM,
    hardwareBacked: false,
    exportable: true,
    privateKey,
    publicKey: ed25519.getPublicKey(privateKey),
  };
}

/** A signature over `message`, and whether this key verifies it. */
export function signAndVerify(ed25519, key, message = FIXED_MESSAGE) {
  const bytes = utf8Bytes(message);
  const signature = ed25519.sign(bytes, key.privateKey);
  return { message, signature, verified: ed25519.verify(signature, bytes, key.publicKey) };
}

/** Hex, lower case, no prefix: the spelling a fingerprint is compared in. */
export function hex(bytes) {
  let text = '';
  for (const byte of bytes) text += byte.toString(16).padStart(2, '0');
  return text;
}

/** How much of a public key or a signature a screen shows. */
export const SHOWN_BYTES = 16;

/** `hex`, cut for display, with the whole length kept: a truncation is visible. */
export function short(bytes, { bytes: count = SHOWN_BYTES } = {}) {
  const full = hex(bytes);
  return full.length <= count * 2 ? full : `${full.slice(0, count * 2)}… (${bytes.length} bytes)`;
}

/**
 * The U5 report: exactly what the draft's U5 row asks for, and the proof that
 * the key signs and verifies. `hardwareBacked` and `exportable` are the key's
 * own answers, so a screen that one day found a hardware route would report
 * that route's values without this function changing.
 */
export function report(key, proof) {
  return {
    algorithm: key.algorithm,
    hardwareBacked: key.hardwareBacked,
    exportable: key.exportable,
    publicKey: short(key.publicKey),
    message: proof.message,
    signature: short(proof.signature),
    signatureBytes: proof.signature.length,
    verified: proof.verified,
  };
}

/**
 * What the probe looked for and did not find, as the screen and the draft say
 * it. There is no stock Expo module to call, so the answer is a sentence and
 * not a call -- and it is written here, once, so the screen and the draft
 * cannot disagree about what was tried.
 */
export const HARDWARE_PROBE = Object.freeze({
  tried: 'searched the installed Expo modules for a key generator (Android Keystore / StrongBox, iOS Secure Enclave)',
  found: false,
  why:
    'no stock Expo package generates an asymmetric key in platform hardware: expo-secure-store stores items, and a ' +
    'non-exportable key needs a native module calling Android\'s KeyPairGenerator or iOS\'s SecKeyCreateRandomKey',
});
