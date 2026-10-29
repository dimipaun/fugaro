// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two items the phone may hold (mobile plan, Task P3.1; decision D111).
//
// D111 answered "what may the app hold?" with a list of exactly two items per
// account, **both in platform secure storage (Keychain / Keystore), both never
// logged, never in a fixture and never in a settings file, and nothing else**:
//
//   1. a **Firebase Auth ID token** -- a user credential scoped to one
//      installation, refreshed by the SDK, revocable by deleting the Auth
//      user; and
//   2. the **per-pairing MAC key** D113b's option (a) creates -- a new secret
//      that authenticates the outbox and reaches no database and no cloud API
//      (confirmed 2026-10-17, reconciliation §4 tension (b2)).
//
// Never a Fugaro or cloud admin credential, never an LLM key, never a
// service-account JSON, never a `fugaro` config file. `SECRET_ITEMS` below is
// that list, and it is the whole of it: `secure-store.js` refuses an item name
// that is not on it, so a third secret cannot be stored by naming it.
//
// **This module is the "never logged" half of D111, and it is a value rather
// than a rule.** A bare string cannot be protected: `console.log(token)` is a
// single keystroke away in any file that holds one, and a `catch` that
// interpolates the failing value writes it to a crash reporter. So a secret
// here is never a string. It is a `Secret` -- a frozen object with the
// material in a **private field**, reachable only through `reveal()`, whose
// every other conversion (`String()`, a template literal, `JSON.stringify`,
// `util.inspect`, `Object.entries`, a spread) prints `[secret <item>]`
// instead. The boundary scan (`src/boundary.test.js`) then has something to
// grep for: in production code `reveal(` appears in this module and in
// `secure-store.js` -- the one place material is handed to a store -- and
// nowhere else under `mobile/`. That is the shape Voice D54b's clause (3)
// gives its renderer test, and reconciliation §4 tension (b2) clause (4) asks
// this task for the same thing.
//
// **The value is printable ASCII, and that is a platform rule.** Both items
// are ASCII by construction (a JWT is base64url and dots; a MAC key is base64
// or hex), and requiring it here buys two things: the length ceiling below is
// a **byte** ceiling, which is what `expo-secure-store` counts, without this
// module needing a UTF-8 encoder that React Native does not reliably have; and
// a value carrying a newline -- how one log line becomes two -- cannot be
// constructed at all.

/**
 * The two items of D111, and the whole list. The order is the order the store
 * reports them in.
 */
export const SECRET_ITEMS = Object.freeze(['idToken', 'macKey']);

/**
 * The longest secret value, in **bytes**: 2048, `expo-secure-store`'s own
 * ceiling on Android, which rejects a larger value rather than truncating it.
 * The ceiling is enforced here so the refusal is this module's, with a code,
 * instead of the platform's, mid-write. A real Firebase ID token is well
 * inside it; a token that is not is refused loudly rather than shortened,
 * because a shortened credential is a credential that fails somewhere else.
 */
export const SECRET_VALUE_MAX = 2048;

/** The refusal codes `secret()` returns. */
export const SECRET_CODES = Object.freeze({
  BAD_ITEM: 'bad-item',
  BAD_TYPE: 'bad-type',
  EMPTY: 'empty',
  NOT_PRINTABLE: 'not-printable',
  OVERSIZE: 'oversize',
});

/**
 * What every conversion of a `Secret` prints instead of the material. It
 * names the item, which is not itself a secret -- the point is that a log line
 * or a fixture can say *which* item was in hand without saying what it was.
 */
const PLACEHOLDER = (item) => `[secret ${item}]`;

/**
 * `util.inspect`'s well-known symbol, spelled as the symbol itself rather than
 * imported from `node:util`: this module is bundled into a React Native app,
 * where there is no `node:util` to import, and the symbol registry is global.
 */
const INSPECT = Symbol.for('nodejs.util.inspect.custom');

/** The characters a secret value may carry: printable ASCII, no space. */
const PRINTABLE_ASCII_RE = /^[\x21-\x7e]+$/;

/** Is `name` one of D111's two items? */
export function isSecretItem(name) {
  return SECRET_ITEMS.includes(name);
}

/** Is `value` a `Secret` this module built? */
export function isSecret(value) {
  return value instanceof Secret;
}

/**
 * A credential value that cannot be printed by accident.
 *
 * Construct it with `secret()` below, which refuses rather than throws; the
 * constructor itself throws, so a `Secret` in a bad shape cannot exist even
 * for a caller that went around the factory.
 */
export class Secret {
  #item;
  #value;

  constructor(item, value) {
    if (!isSecretItem(item)) throw new TypeError(`unknown secret item: ${String(item)}`);
    if (typeof value !== 'string' || value.length === 0) {
      throw new TypeError('a secret value is a non-empty string');
    }
    this.#item = item;
    this.#value = value;
    Object.freeze(this);
  }

  /** Which of D111's two items this is. Not a secret. */
  get item() {
    return this.#item;
  }

  /**
   * The material. The only way to it, and the reason the boundary scan can
   * allow-list the call sites: a file that reveals a secret says so in a word
   * a reviewer and a grep can both find.
   */
  reveal() {
    return this.#value;
  }

  toString() {
    return PLACEHOLDER(this.#item);
  }

  /** `JSON.stringify(secret)` prints the placeholder, never the material. */
  toJSON() {
    return PLACEHOLDER(this.#item);
  }

  /** `util.inspect(secret)`, and therefore most `console.log` calls. */
  [INSPECT]() {
    return PLACEHOLDER(this.#item);
  }

  /** `+secret`, `${secret}`, `secret + ''`: the placeholder, every time. */
  [Symbol.toPrimitive]() {
    return PLACEHOLDER(this.#item);
  }
}

/** A frozen refusal, in the shape the shared package's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * Judge one candidate item/value pair and, if it is one, wrap it.
 *
 * Returns `{ ok: true, secret }` or `{ ok: false, code, field }`. The value is
 * carried **verbatim**: nothing is trimmed, unquoted or unescaped, because a
 * repaired credential is a credential that authenticates as something else.
 */
export function secret(item, value) {
  if (!isSecretItem(item)) return refuse(SECRET_CODES.BAD_ITEM, 'item');
  if (typeof value !== 'string') return refuse(SECRET_CODES.BAD_TYPE, 'value');
  if (value.length === 0) return refuse(SECRET_CODES.EMPTY, 'value');
  // The byte ceiling first, so an enormous value is refused without being
  // walked by the character rule below.
  if (value.length > SECRET_VALUE_MAX) return refuse(SECRET_CODES.OVERSIZE, 'value');
  if (!PRINTABLE_ASCII_RE.test(value)) return refuse(SECRET_CODES.NOT_PRINTABLE, 'value');
  return Object.freeze({ ok: true, secret: new Secret(item, value) });
}
