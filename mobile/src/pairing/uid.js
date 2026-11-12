// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two names a pairing puts on the wire (mobile plan, Task P6.8; the
// pairing plan's §2 (e), §7.1 step 6, §8 and M4).
//
// Two families, two prefixes, and neither may be mistaken for the other:
//
//   * a **pairing** uid is `p~` and 32 random bytes (`token.PairingPrefix`,
//     `pairing.go`'s own draw). It names `pairing/<uid>`, it is the first field
//     of the delivery envelope's AAD, and it is dead the moment the CLI deletes
//     the subtree.
//   * a **device** uid is `m~` and 16 random bytes (`deviceNewUID`). It is the
//     Firebase Auth uid, the RTDB key `config/admin/devices/$uid`, and the
//     claim `mu` of the device token -- never a pairing's name, and never a
//     run's (`r~`, M4).
//
// **Why the check is exact rather than "a prefix and some text".** §8's AAD is
// `"pairing/" + uid + 0x00 + key + 0x00 + version`, and what makes it
// unambiguous is that base64url spells no zero byte: the first zero after the
// literal is the uid's terminator and never part of the name. A uid carrying a
// zero byte would move that terminator, so two different AADs could be built
// for one pairing -- which is why the shape is refused rather than accepted
// leniently. The CLI refuses the same strings with `checkPairingUID`.
//
// The spelling is strict for `base64url.js`'s reason: 43 characters whose last
// one carries two discarded bits spell one 32-byte value four ways, and four
// names for one pairing is four AADs.

import { decodeBase64url } from './base64url.js';

/** A pairing session's name: `p~` and 32 bytes. */
export const PAIRING_UID_PREFIX = 'p~';
export const PAIRING_UID_BYTES = 32;

/** A device's name: `m~` and 16 bytes. */
export const DEVICE_UID_PREFIX = 'm~';
export const DEVICE_UID_BYTES = 16;

/** Is this a pairing uid, in the one spelling the AAD binds? */
export function isPairingUID(uid) {
  return isUID(uid, PAIRING_UID_PREFIX, PAIRING_UID_BYTES);
}

/** Is this a device uid, in the one spelling the CLI mints? */
export function isDeviceUID(uid) {
  return isUID(uid, DEVICE_UID_PREFIX, DEVICE_UID_BYTES);
}

function isUID(uid, prefix, bytes) {
  if (typeof uid !== 'string' || !uid.startsWith(prefix)) return false;
  return decodeBase64url(uid.slice(prefix.length), bytes).ok;
}
