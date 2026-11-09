// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The licence module's one door (subscriptions plan S2.5; D308, D303 as
// amended).
//
// A screen imports `src/licence/index.js` and gets the app's verifier, a way to
// build the store the key is kept in (S2.8, below) and the vocabulary it answers
// in -- the codes, the states' names and the numbers behind them. It does not
// get `verify.js`'s factory, and that is the point of this file rather than a
// tidiness: the factory takes a keyring, and a caller that could build a
// verifier could build one that trusts a key this build does not hold, which is
// the whole of what the compiled-in ring fences. The store's class is withheld
// for the same reason and not for symmetry: its constructor takes a verifier, so
// handing out the class would hand out the choice the factory takes away.
//
// **This is the one file under `src/licence/` that names a package**, and the
// reason is `mobile/README.md`'s: the suite runs in a fresh clone with no
// `node_modules`, so every module a test loads must import nothing. The Ed25519
// primitive arrives here and is handed to the verifier as a port, exactly as the
// mailbox's Realtime Database store arrives from `mobile/index.js`. The test for
// this file therefore reads it as text (`verify.test.js`) instead of loading it:
// it asserts that the one package named is the Ed25519 dependency, that nothing
// here reaches a network, and that no environment is read -- which is what keeps
// the promise that a key can only ever arrive as an argument.
//
// **`@noble/curves` and the option it is called with.** The dependency is the
// owner's one yes for this task (S2.5's Files line), it is Ed25519 with SHA-512
// wired in and no second package behind it, and it is called with
// `{ zip215: false }` -- RFC 8032, not the library's default, which follows
// ZIP-215. That default is the permissive reading: it accepts a signature over a
// non-canonical point encoding, and over a small-order key such as the all-zero
// one, where the CLI's `ed25519.Verify` and the console's OpenSSL are RFC 8032
// and refuse them. Three readers of one wire format have to agree on what a
// signature is, so the app takes the strict reading -- and it is pinned as text
// rather than by a vector, because no shared vector can reach this option: the
// padding-bits vector is refused inside `decodeBase64url`, before the primitive
// is called. Nothing else about the primitive is configured, and no secret ever
// passes through here: a public key is a public key.

import { ed25519 } from '@noble/curves/ed25519.js';

import { LicenceStore } from './store.js';
import { GRACE_DAYS, LICENCE_CODES, PRODUCTION_KEYRING, TIER_MAX_LIFETIME_DAYS, createLicenceVerifier } from './verify.js';

/** ed25519Port is the dependency as `verify.js` takes it: the three arguments in
 * the order the design names them, and RFC 8032 rather than ZIP-215, so this
 * reader refuses what the CLI's and the console's refuse. */
const ed25519Port = (signature, message, publicKey) => ed25519.verify(signature, message, publicKey, { zip215: false });

/** LICENCE_VERIFIER is the app's verifier: `verify.js`'s check over the app's own
 * Ed25519 and the ring this build ships. It is built once, here, and everything
 * that needs to read a key reads it through this object. */
export const LICENCE_VERIFIER = createLicenceVerifier({ ed25519: ed25519Port });

export { GRACE_DAYS, LICENCE_CODES, PRODUCTION_KEYRING, TIER_MAX_LIFETIME_DAYS };

/** createLicenceStore is the app's store for the key: `store.js`'s store over
 * the port it is given and this build's verifier, which is why a factory is
 * what the door hands out rather than the class. `new LicenceStore({ port,
 * verifier })` is a caller choosing the verifier, and that is the one capability
 * this door exists to withhold -- a store handed a keyring this build does not
 * hold would verify a key the app is not meant to trust. The store's own file
 * still exports the class, for the test that has to choose a test keyring; the
 * door the app imports does not. */
export function createLicenceStore(port) {
  return new LicenceStore({ port, verifier: LICENCE_VERIFIER });
}

// The store is reached through the door and not built beside the screen that
// reads it, for the reason above. The screen S2.1's table names is the caller:
// it takes the pasted text, calls `install`, and reads `keys` and `active` to
// say what is installed.
export { LICENCE_KEYS_ENTRY, LICENCE_KEYS_MAX, LICENCE_KEY_PREFIX, LICENCE_STORE_CODES } from './store.js';
