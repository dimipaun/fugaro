// An account is an installation (mobile plan, Task P3.1; decision D114).
//
// D114 answered "the account model" with an installation tuple rather than a
// user: **one Firebase project, one operator identity (the Auth `uid` in that
// project), one Fugaro project name**. Several are stored as separate,
// non-overlapping entries, so a tuple is also the thing the app must be able
// to tell apart -- `sameInstallation` below is the comparison the store
// refuses a duplicate on.
//
// **What a tuple is not.** It is not a credential. D111's two items (a
// Firebase Auth ID token and a per-pairing MAC key) live in platform secure
// storage and nowhere else; a tuple is the **address** of an installation, and
// it is the thing Voice D42(2) and console D13 already allow a settings
// document to hold -- "the app stores the tuples, never a credential".
//
// **The account id is a storage key segment.** It namespaces the secure-storage
// item (`fugaro.account.<id>.<item>`, see `secure-store.js`) and the cache
// namespace (`fugaro.cache.<id>`, see `resources.js`), so it is restricted to
// the alphabet `expo-secure-store` itself accepts in a key -- letters, digits,
// `_` and `-` -- and to 64 characters. `.` is deliberately NOT in the
// alphabet: the composed key uses `.` as its separator, so an id carrying one
// would make `fugaro.account.a.b.idToken` mean two different things. The other
// three fields are values, not keys, so they are ordinary bounded text.
//
// **Nothing is coerced and nothing is trimmed.** A tuple is owner-typed (D114:
// "the app's switcher is a list the owner maintains, not a discovery result"),
// so a padded or look-alike value is refused here rather than repaired: two
// tuples that print the same must not be two installations.

/**
 * The tuple's fields, in the order the store writes them. This is the field
 * set: an object carrying any other key is refused, because a field this
 * module does not know is a field the store would silently drop.
 */
export const ACCOUNT_FIELDS = Object.freeze([
  'id',
  'firebaseProjectId',
  'authUid',
  'fugaroProject',
]);

/** The longest account id, in code units: the storage key stays short. */
export const ACCOUNT_ID_MAX = 64;

/**
 * The longest value any other field may carry, in **code points** (not UTF-16
 * units). A Firebase project id and an Auth uid are ≤128 ASCII characters in
 * practice; 200 leaves room without letting a field be a container.
 */
export const ACCOUNT_TEXT_MAX = 200;

/**
 * The account id's alphabet and ceiling: what `expo-secure-store` accepts in a
 * key, minus the `.` the composed key uses as its separator, and no longer
 * than `ACCOUNT_ID_MAX`. The bound is in the predicate and not only in
 * `validateAccount`, because `isAccountId` is what the key composers call.
 */
const ACCOUNT_ID_RE = new RegExp(`^[A-Za-z0-9_-]{1,${ACCOUNT_ID_MAX}}$`);

/**
 * The characters a tuple's text fields may not carry: the Unicode categories
 * `internal/pluginwire/wire.go`'s `Printable` treats as unprintable -- **Cc**
 * (the C0 controls, DEL, the C1 block), **Cf** (zero-width and
 * direction-changing characters, the soft hyphen, the BOM) and the two line
 * separators **Zl** and **Zp** -- plus U+2800, the blank-looking braille
 * pattern, and every **whitespace** code point (`\p{White_Space}`, which
 * includes **Zs**): an interior space is how `uid 0001` and `uid0001` become
 * two names for one installation. Leading and trailing whitespace is caught
 * earlier, as `padded`.
 *
 * **Why this set and not the whole of `Printable`.** That predicate exists so
 * that two identities cannot print the same in a channel a human reads; the
 * mobile app's tuple is typed by the owner, stored, and used to address an
 * installation, and it is printed nowhere a collision could be arranged. The
 * categories above are the ones that would make *this* module lie -- a control
 * character that truncates a stored value, a bidi override that reverses one,
 * a blank that looks empty. The rest of `Printable`'s set (unassigned code
 * points, private use, the look-alike letters) is the shared package's rule
 * and belongs to the protocol (P0.2's `shared/mailbox-schema/`), which P3.6
 * imports rather than restates; this module never carries a protocol value.
 */
const UNSAFE_TEXT_RE = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\p{White_Space}⠀]/u;

/** The refusals this module and the store that uses it can return. */
export const ACCOUNT_CODES = Object.freeze({
  NOT_AN_OBJECT: 'not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  MISSING_FIELD: 'missing-field',
  BAD_TYPE: 'bad-type',
  EMPTY: 'empty',
  PADDED: 'padded',
  OVERSIZE: 'oversize',
  NOT_NFC: 'not-nfc',
  UNSAFE_TEXT: 'unsafe-text',
  BAD_ID: 'bad-id',
});

/** A frozen refusal, in the shape the shared package's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * Is this string usable as an account id? The predicate is exported because
 * `secure-store.js` and `resources.js` both compose a key from an id they did
 * not validate themselves, and a composition that cannot be checked is a
 * composition that can be made to point at another account's item.
 */
export function isAccountId(value) {
  return typeof value === 'string' && ACCOUNT_ID_RE.test(value);
}

/** Is every field of `a` the same installation as `b`? (D114's triple.) */
export function sameInstallation(a, b) {
  return (
    a.firebaseProjectId === b.firebaseProjectId &&
    a.authUid === b.authUid &&
    a.fugaroProject === b.fugaroProject
  );
}

/**
 * Judge one candidate tuple.
 *
 * Returns `{ ok: true, account }` with a **frozen copy** carrying exactly
 * `ACCOUNT_FIELDS`, or `{ ok: false, code, field }`. `field` names the field at
 * fault where one is at fault; a refusal about the object itself carries none.
 * The copy is the caller's values verbatim -- nothing is trimmed, floored or
 * stringified, because a repaired identity is a different installation from
 * the one the owner typed.
 */
export function validateAccount(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return refuse(ACCOUNT_CODES.NOT_AN_OBJECT);
  }
  // A class instance is an object too, and a tuple is a plain one: a
  // `Secret`, a Firebase client object or anything else with a prototype of
  // its own is refused here rather than having its enumerable fields read as
  // if they were this module's four.
  let proto;
  try {
    proto = Object.getPrototypeOf(value);
  } catch {
    return refuse(ACCOUNT_CODES.NOT_AN_OBJECT);
  }
  if (proto !== Object.prototype && proto !== null) {
    return refuse(ACCOUNT_CODES.NOT_AN_OBJECT);
  }
  // Read each field exactly once, into a plain object, before judging any of
  // them: a getter or a Proxy must not be able to answer one value to the
  // validation and another to the copy the caller keeps.
  const read = {};
  for (const key of Object.keys(value)) {
    if (!ACCOUNT_FIELDS.includes(key)) return refuse(ACCOUNT_CODES.UNKNOWN_FIELD, key);
    read[key] = value[key];
  }
  for (const field of ACCOUNT_FIELDS) {
    if (!(field in read)) return refuse(ACCOUNT_CODES.MISSING_FIELD, field);
  }
  for (const field of ACCOUNT_FIELDS) {
    const text = read[field];
    if (typeof text !== 'string') return refuse(ACCOUNT_CODES.BAD_TYPE, field);
    if (text.length === 0) return refuse(ACCOUNT_CODES.EMPTY, field);
    if (text !== text.trim()) return refuse(ACCOUNT_CODES.PADDED, field);
    if ([...text].length > (field === 'id' ? ACCOUNT_ID_MAX : ACCOUNT_TEXT_MAX)) {
      return refuse(ACCOUNT_CODES.OVERSIZE, field);
    }
    if (text.normalize('NFC') !== text) return refuse(ACCOUNT_CODES.NOT_NFC, field);
    if (field === 'id') {
      if (!isAccountId(text)) return refuse(ACCOUNT_CODES.BAD_ID, field);
    } else if (UNSAFE_TEXT_RE.test(text)) {
      return refuse(ACCOUNT_CODES.UNSAFE_TEXT, field);
    }
  }
  const account = Object.freeze({
    id: read.id,
    firebaseProjectId: read.firebaseProjectId,
    authUid: read.authUid,
    fugaroProject: read.fugaroProject,
  });
  return Object.freeze({ ok: true, account });
}
