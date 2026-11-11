// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What the delivery envelope carries, judged before anything is stored (mobile
// plan, Task P6.8; the pairing plan's §4.4, §7.2, D199 and D171).
//
// **The bytes are `token.MarshalPayload`'s and nothing else.** §4.4's table and
// `internal/budget/token`'s `deliveryWire` pin one document:
//
//     { "config": { "project", "gcp_project", "database_url", "api_key" },
//       "device": { "uid": "m~…", "token": "…" },
//       "mac_key": "…" }
//
// The `config` object is the **record**: the copy the app stores and uses from
// now on, not the QR's bootstrap copy (D199). `device.uid` is the Firebase Auth
// uid, `device.token` is the custom token minted for it, and `mac_key` is the
// per-pairing mailbox MAC key (D171) in `EncodeKey`'s one spelling. Those two
// are credentials and this module is where they stop being JSON: the record it
// answers is what `session.js` hands to secure storage, and nothing in this
// tree prints it (M6, M7).
//
// **Why a delivery is hostile input even though it is authenticated.** The tag
// proves the sender knew this phone's public key, and the phone's public key is
// on the request node for anyone with database read to see -- so a database
// admin who is not the CLI can seal a delivery this phone will open. That is
// §8's own "a capability, not a boundary" said from this end: the envelope
// removes the value of a photographed QR, it does not authenticate the CLI. So
// every field is bounded, typed and shaped here exactly as it is in
// `payload.js`, and the check is the same rule (`config.js`), because a
// delivery is the only thing that decides what the app stores.
//
// **Why the disagreement check is on the project and not on the URL.** §4.4:
// a delivery "whose contents disagree with what the QR said the project is" is
// refused whole. The project is `project` and `gcp_project` -- the two names
// the QR carried and the two the URL is judged against (`config.js` derives the
// database's host from the project) -- so a delivery that agreed on the project
// cannot carry a database belonging to another one: `judgeConfig` has already
// refused a URL that is not this project's. Comparing the URL string too would
// refuse a legitimate re-issue that spelled the same database regionally, which
// §4.4's rule does not ask for.

import { CONFIG_CODES, CONFIG_FIELDS, judgeConfig, judgeFieldSet } from './config.js';
import { decodeBase64url } from './base64url.js';
import { DEVICE_SECRET_SIZE } from './key.js';
import { isDeviceUID } from './uid.js';

/** The delivery's own fields, exactly (`deliveryWire`). */
export const DELIVERY_FIELDS = Object.freeze(['config', 'device', 'mac_key']);

/** The device object's fields, exactly (`deliveryDeviceWire`). */
export const DEVICE_FIELDS = Object.freeze(['uid', 'token']);

/** The longest the device token may be: a signed JWT carrying this device's
 * claims, which is longer than the pairing token but bounded all the same. It
 * is a bound rather than a length the CLI can reach. */
export const DEVICE_TOKEN_MAX_LENGTH = 4096;

/** How this module refuses. The configuration's codes are `config.js`'s, spread
 * rather than retyped, so a scan's refusal and a delivery's are comparable. */
export const DELIVERY_CODES = Object.freeze({
  ...CONFIG_CODES,
  NOT_BYTES: 'not-bytes',
  NOT_TEXT: 'not-text',
  TOO_LONG: 'too-long',
  BACKSLASH: 'backslash',
  NOT_JSON: 'not-json',
  DUPLICATE_FIELD: 'duplicate-field',
  BAD_DEVICE_UID: 'bad-device-uid',
  BAD_MAC_KEY: 'bad-mac-key',
  DISAGREES_WITH_SCAN: 'disagrees-with-scan',
});

// The plaintext is the CLI's own JSON: printable ASCII, and the longest a
// delivery can be. `MarshalPayload` writes no indentation, so the bound is
// generous by an order of magnitude.
const DELIVERY_MAX_LENGTH = 8192;

// Every field the CLI writes is printable ASCII, for `payload.js`'s reason.
const PRINTABLE_ASCII = /^[!-~]+$/;

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/**
 * Judge the plaintext an envelope opened, against the scan that started the
 * pairing.
 *
 * `plaintext` is `openDelivery`'s bytes and `scan` is `readScannedPayload`'s
 * record -- the bootstrap, read for the two names it is allowed to decide.
 * The answer's `delivery` is the record the app stores: the delivered
 * configuration (D199), the device uid and token, and the MAC key as bytes.
 */
export function readDelivery({ plaintext, scan }) {
  if (!(plaintext instanceof Uint8Array)) return refuse(DELIVERY_CODES.NOT_BYTES, 'delivery');
  if (plaintext.length > DELIVERY_MAX_LENGTH) return refuse(DELIVERY_CODES.TOO_LONG, 'delivery');
  if (scan === null || typeof scan !== 'object' || typeof scan.project !== 'string' || typeof scan.gcpProject !== 'string') {
    return refuse(DELIVERY_CODES.DISAGREES_WITH_SCAN, 'project');
  }

  // `fatal` because a delivery is one UTF-8 document: a byte sequence that is
  // not valid UTF-8 is not a mis-encoding of one, it is another document, and
  // `TextDecoder`'s default would replace it with U+FFFD and hand the judge a
  // string nobody wrote.
  let text;
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(plaintext);
  } catch {
    return refuse(DELIVERY_CODES.NOT_TEXT, 'delivery');
  }
  if (!PRINTABLE_ASCII.test(text)) return refuse(DELIVERY_CODES.NOT_TEXT, 'delivery');
  // The escape rule, for `payload.js`'s reason: the fields are base64url,
  // project names, a URL and a JWT, so `json.Marshal` escapes nothing in the
  // document it writes, and with no backslash a `"` cannot occur inside a
  // value -- which is what makes the "named once" check below exact. A future
  // field whose text could carry `<`, `>` or `&` would be escaped by Go and
  // refused here; that is a refusal a person can see and report, and it is the
  // trade this rule makes in both carriers.
  if (text.includes('\\')) return refuse(DELIVERY_CODES.BACKSLASH, 'delivery');
  // One field, one value, in every object the document carries -- the
  // configuration's four as much as the outer three and the device's two. A
  // field left out of this walk is a field `JSON.parse` resolves silently by
  // letting the last of two equal keys win, which is the one thing the
  // configuration may not do: it decides what the app stores (D199).
  for (const field of [...DELIVERY_FIELDS, ...DEVICE_FIELDS, ...CONFIG_FIELDS]) {
    if (occurrences(text, `"${field}"`) > 1) return refuse(DELIVERY_CODES.DUPLICATE_FIELD, field);
  }

  let raw;
  try {
    raw = JSON.parse(text);
  } catch {
    return refuse(DELIVERY_CODES.NOT_JSON, 'delivery');
  }
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
    return refuse(DELIVERY_CODES.NOT_AN_OBJECT, 'delivery');
  }

  // The three objects, each with exactly its own fields, before any value is
  // read: a delivery carrying a field this build does not know is refused
  // whole, and the configuration is refused before the credentials are looked
  // at, so a delivery that cannot be stored never reaches the token.
  const outer = judgeFieldSet(raw, DELIVERY_FIELDS, '');
  if (!outer.ok) return outer;
  const device = judgeFieldSet(raw.device, DEVICE_FIELDS, 'device.');
  if (!device.ok) return device;
  const config = judgeFieldSet(raw.config, CONFIG_FIELDS, 'config.');
  if (!config.ok) return config;

  const judged = judgeConfig(
    {
      project: config.value.project,
      gcpProject: config.value.gcp_project,
      databaseUrl: config.value.database_url,
      apiKey: config.value.api_key,
    },
    'config.',
  );
  if (!judged.ok) return judged;
  // §4.4: a delivery that disagrees with the scan about the project is refused
  // whole. The two names are compared, and the URL cannot disagree on its own
  // (the header's reason).
  if (judged.config.project !== scan.project || judged.config.gcpProject !== scan.gcpProject) {
    return refuse(DELIVERY_CODES.DISAGREES_WITH_SCAN, 'config.project');
  }

  const uid = device.value.uid;
  const token = device.value.token;
  if (typeof uid !== 'string') return refuse(DELIVERY_CODES.BAD_TYPE, 'device.uid');
  if (!isDeviceUID(uid)) return refuse(DELIVERY_CODES.BAD_DEVICE_UID, 'device.uid');
  if (typeof token !== 'string') return refuse(DELIVERY_CODES.BAD_TYPE, 'device.token');
  if (token.length > DEVICE_TOKEN_MAX_LENGTH) return refuse(DELIVERY_CODES.BAD_TEXT, 'device.token');
  if (!PRINTABLE_ASCII.test(token)) return refuse(DELIVERY_CODES.BAD_TEXT, 'device.token');

  const macKey = readMacKey(raw.mac_key);
  if (!macKey.ok) return macKey;

  return Object.freeze({
    ok: true,
    delivery: Object.freeze({
      config: judged.config,
      device: Object.freeze({ uid, token }),
      macKey: macKey.bytes,
    }),
  });
}

/**
 * The MAC key as bytes, or a refusal. It is `EncodeKey`'s spelling and it is
 * decoded **strictly** (`base64url.js`): 32 bytes is three whole groups and two
 * characters, so the last character carries two bits this encoding discards and
 * three other characters spell the same key. A MAC key with four spellings is
 * four values the app could hold where the mailbox expects one, so the
 * non-canonical ones are refused rather than normalised.
 */
function readMacKey(value) {
  if (typeof value !== 'string') return refuse(DELIVERY_CODES.BAD_TYPE, 'mac_key');
  const decoded = decodeBase64url(value, DEVICE_SECRET_SIZE);
  if (!decoded.ok) return refuse(DELIVERY_CODES.BAD_MAC_KEY, 'mac_key');
  return Object.freeze({ ok: true, bytes: decoded.bytes });
}

/** How many times `needle` occurs in `text` -- `payload.js`'s helper, kept
 * local because it is four lines and an import across two carriers of the same
 * rule would be the thing a reader has to chase. */
function occurrences(text, needle) {
  let count = 0;
  let at = text.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = text.indexOf(needle, at + needle.length);
  }
  return count;
}
