// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The two verdicts every port in `src/audio/` answers with (Task P3.4).
//
// Every module of this tree answers with a **value** rather than an exception:
// a refusal is `{ ok: false, code, field }`, frozen, and a caller branches on
// the code rather than on a caught error. That is the shape
// `shared/mailbox-schema/` and `src/accounts/` both use, and it is what lets a
// test assert *which* refusal happened -- "the OS refused the microphone" and
// "the engine could not transcribe" are different facts, and one code for both
// loses the second one.
//
// **A throw is for a programming error and nothing else.** A port that was
// handed a handler that is not a function, or a port that was constructed
// without the dependency it needs, has no runtime state to report: it is a
// caller that will never work, and it fails at the call rather than at the
// next message. A phone that says no to a permission prompt is a working
// phone, so a permission refusal is a value.

/**
 * A frozen refusal, in the shape this repository's modules use.
 *
 * @param {string} code one of the module's own codes
 * @param {string | null} field the field or dependency at fault, or null
 * @returns {{ ok: false, code: string, field: string | null }}
 */
export function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * A frozen acceptance carrying the values the caller asked for.
 *
 * @param {object} fields the accepted values, by name
 * @returns {{ ok: true }}
 */
export function accept(fields = {}) {
  return Object.freeze({ ok: true, ...fields });
}

/**
 * Whether `value` is a refusal this module could have produced: the shape, not
 * the code. A test helper and a guard, never a decision.
 *
 * @param {unknown} value a candidate verdict
 * @returns {boolean}
 */
export function isRefusal(value) {
  return (
    typeof value === 'object' &&
    value !== null &&
    value.ok === false &&
    typeof value.code === 'string' &&
    (value.field === undefined || value.field === null || typeof value.field === 'string')
  );
}
