// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The crash path's breadcrumbs: a constant from this module's list, or the
// shared redactor (mobile plan, Task P7.3; D256, D118; the telemetry plan's
// §7.3).
//
// **The rule, in two halves.** A call site passes a **constant from the list
// below**, or a call to `breadcrumb` itself -- the scan refuses every other
// argument at any log the crash path writes, whatever receiver it is written
// through -- and a value built at run time becomes a
// breadcrumb only through `breadcrumb` below, which is `shared/digest`'s
// `redact`: the same function the mailbox client redacts an utterance with
// before it is written or spoken (D118). The app therefore has **one**
// redaction rule and not two, which is D119's reason
// for putting the redactor in a shared package in the first place: two
// implementations of "what is safe to say" are two answers to that question.
//
// **Why the constants are the screens' own names.** A breadcrumb exists to say
// where a crash happened, and the only vocabulary the app has for "where" is
// §7.2's screen set. Deriving them from `SCREENS` rather than writing the names
// out again keeps one list for one fact: a screen added to `SCREENS` gets its
// breadcrumb here, and a screen the app does not have cannot be named in a
// crash report.
//
// **What this module is NOT.** It is not a second telemetry module: it holds a
// list and one function, and it never reaches the SDK. The call site is what
// `analytics-calls.test.js` fences -- a `log(` or a `recordError(` whose
// argument is neither a literal from `BREADCRUMBS` nor a call to `breadcrumb`
// is refused there.

import { DigestError, ERROR } from '../../../shared/digest/errors.js';
import { redact } from '../../../shared/digest/redact.js';

import { SCREENS } from './events.js';

/**
 * The constants a breadcrumb may be. Everything else goes through `breadcrumb`,
 * which redacts it first.
 */
export const BREADCRUMBS = Object.freeze([
  'app_launch',
  'app_resumed',
  ...SCREENS.map((screen) => `screen_${screen}`),
  'outbox_sent',
  'outbox_refused',
  'pairing_finished',
]);

/**
 * `text` as a breadcrumb: the constant itself when the list names it -- a
 * constant the list reviewed is not text the app built, so it is returned
 * without redacting and needs no secrets -- and otherwise the same text with
 * every form of every secret in `secrets` replaced.
 *
 * @param {unknown} text a constant from `BREADCRUMBS`, or text built at run time
 * @param {unknown} secrets the strings that must not leave the device (D118's list)
 * @returns {string}
 * @throws {Error} P1.3's own error for text that is not text, for a secret list
 *   that is not a list of strings, and for an **empty** one: a redactor that
 *   silently did nothing would be worse than one that refused
 */
export function breadcrumb(text, secrets) {
  if (BREADCRUMBS.includes(text)) return text;
  // An empty list is not a redaction. `redactForms([])` holds no form, so the
  // text would go out whole -- and a caller that reached this line with no
  // secrets is a caller that cannot redact what it is about to say, which is
  // the one thing this module exists to refuse.
  if (Array.isArray(secrets) && secrets.length === 0) {
    throw new DigestError(ERROR.SECRETS_NOT_A_LIST, 'the secrets');
  }
  return redact(text, secrets);
}
