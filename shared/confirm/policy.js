// The session policy the confirmation ruling reads (mobile identity and pairing
// plan, Task J3; decision **D2786**, which amends D162 for costly modes only).
//
// **What this module is.** The one decision D2786 adds to the spoken
// confirmation: a *start* in a mode the session calls **costly** is read back
// like a cancel, and with the biometric switch it also needs the biometric
// re-auth (P5.1g's path, the phone's). Every other start still goes straight
// through, which is D162's answer and stays it.
//
// **Where the values come from.** The two policy values are C2's session policy
// (`shared/mobile-session/port.js`'s `SESSION_POLICY_FIELDS`): `costly_modes`,
// the modes a project calls costly, and `biometric_on_costly`, whether one of
// them also asks for the biometric. The names here are the machine's own
// spellings of those two values, because a state's fields are camelCase
// throughout and a field set is a version; what the values *mean* is the port's,
// and the Go project policy behind them (P6: `mobile.costly_modes`, default
// `[pro, critical]`, and `mobile.biometric_on_costly`, default off) is the
// source of the defaults below.
//
// **Fail closed, and total.** This module never throws, never reads a clock and
// imports nothing. A `costlyModes` that is not an array at all -- a value this
// module cannot read -- makes **every** start costly rather than none, and a
// `biometricOnCostly` that is not exactly `false` asks for the biometric. The
// machine refuses a state carrying either as malformed before it gets here, so
// the lenient reading is only ever the one a caller that skipped the reader
// gets: an unreadable policy demands the stronger confirmation, never the
// weaker one.
//
// **What it is not.** It does not decide *which* mode a start is in: the router
// (P6.1) resolves the mode out of the utterance and hands it in, exactly as it
// resolves a target. Neither does it enforce anything: it answers what the
// ruling asks for, and the mailbox gate that refuses a client which skipped the
// read-back is J5a's (the plan's O10), because a launch is not a mailbox action
// yet -- until then the read-back is the phone's and the cost caps bound a
// client that ignores it.

/** The most costly modes a project may name: C2's own bound for the list. */
export const MAX_COSTLY_MODES = 32;

/** The longest mode name, in characters: C2's own bound, and a rigor mode is a short word. */
export const MAX_MODE_LENGTH = 64;

/**
 * The modes a project calls costly when its own policy names none, which is
 * P6's `mobile.costly_modes` default: the two modes the owner's ruling is about
 * (`internal/rigor`: `pro` and `critical`).
 */
export const DEFAULT_COSTLY_MODES = Object.freeze(['pro', 'critical']);

/**
 * Whether a costly start also asks for the biometric, when the session names no
 * switch, which is P6's `mobile.biometric_on_costly` default: off. A project
 * turns it on; the machine never does.
 */
export const DEFAULT_BIOMETRIC_ON_COSTLY = false;

/**
 * isCostlyStart answers whether a start in `mode` is the costly start D2786
 * names.
 *
 * @param {string} mode the mode the router resolved, already shape-checked
 * @param {ReadonlyArray<string>} costlyModes the session's list
 * @returns {boolean} true when this start is read back
 */
export function isCostlyStart(mode, costlyModes) {
  if (!Array.isArray(costlyModes)) return true;
  return costlyModes.includes(mode);
}

/**
 * costlyStart is the ruling's answer for one start: whether it needs a
 * read-back, and whether the biometric switch makes it also need the biometric.
 *
 * The biometric is asked for only on a costly start -- the switch is named
 * `biometric_on_costly` and a plain start takes the plain path -- and a switch
 * that is not exactly `false` asks for it, so an unreadable policy is the
 * stricter one.
 *
 * @param {string} mode the mode the router resolved
 * @param {ReadonlyArray<string>} costlyModes the session's list
 * @param {boolean} biometricOnCostly the session's switch
 * @returns {{ costly: boolean, biometric: boolean }} frozen
 */
export function costlyStart(mode, costlyModes, biometricOnCostly) {
  const costly = isCostlyStart(mode, costlyModes);
  return Object.freeze({ costly, biometric: costly && biometricOnCostly !== false });
}
