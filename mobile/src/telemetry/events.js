// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The events the app may send, and the closed sets their parameters come from
// (mobile plan, Task P7.3; D256, D255; the telemetry plan's §7.2).
//
// **Why a list rather than a call the app makes freely.** D256's answer is a
// **closed allow-list** of seven event names, and the reason is the app's whole
// security model: a phone holds two credential items and writes two paths
// (D111, D113), and a telemetry SDK that can carry free text is a third channel
// off the device -- the only one that leaves the user's own infrastructure. The
// seven names are the questions the app exists to answer (does it start, is an
// account added, which screens are reached, does pairing work, does a crash
// arrive), and every parameter is a **boolean or a member of an enum**, so a
// value that names a message, a mailbox, an account or a repository has nowhere
// to be written.
//
// **The eighth name is gone, and that is D255's answer.** The list had eight
// while this plan recommended opt-in: `consent_set` (`granted: bool`) recorded
// which way the first-launch sheet was answered. The owner answered
// **always-on**, so there is no sheet and nothing for the event to record, and
// the list is one name shorter rather than carrying a parameter nothing can
// produce.
//
// **Why each event is sent from its own line.** The scan that holds this
// allow-list (`analytics-calls.test.js`) reads call sites in the source, so a
// table that sent everything through one `logEvent(name, parameters)` would be
// a call it could not read and a rule nobody could hold. Every event therefore
// has its own entry below, naming itself as a literal -- which is also what
// makes the scan's allow-list non-vacuous rather than a promise about a file
// that does not send anything yet.
//
// **What is deliberately NOT here: the port.** The module never reaches the
// SDK. It writes to a port shaped like the SDK's own surface (`logEvent(name,
// parameters)`), and `mobile/index.js` -- the one file that knows what a device
// is -- is where the port is wired, exactly as the mailbox's store is. A module
// that imported the SDK itself could be loaded by a test only on a machine with
// the native packages, and no test of it would need a device.

/** §7.2's allow-list: seven names, and no eighth without a change to it. */
export const EVENTS = Object.freeze({
  APP_LAUNCH: 'app_launch',
  ACCOUNT_ADDED: 'account_added',
  ACCOUNT_SWITCHED: 'account_switched',
  SCREEN_SHOWN: 'screen_shown',
  COMMAND_REFUSED: 'command_refused',
  PAIRING_FINISHED: 'pairing_finished',
  CRASH_REPORTED: 'crash_reported',
});

export const EVENT_NAMES = Object.freeze(Object.values(EVENTS));

/** §7.2's screen set, which is the whole set: **`settings` is not in it**, because
 * the app has no settings screen and no phase of the plan builds one. */
export const SCREENS = Object.freeze(['overview', 'jobs', 'mailbox', 'voice', 'text']);

/** How an account was added -- by hand, or by pairing. */
export const ACCOUNT_KINDS = Object.freeze(['manual', 'pairing']);

/** Why a command was refused. The only signal about voice that may leave,
 * because it names no command. */
export const REFUSAL_REASONS = Object.freeze(['offline', 'unconfirmed', 'not_understood', 'refused']);

/** How pairing ended, and how it failed. */
export const PAIRING_OUTCOMES = Object.freeze(['ok', 'code_mismatch', 'expired', 'denied']);

/**
 * What each event may carry, as data so the list can be read and extended in
 * one place: `'boolean'`, or the frozen array of values an enum parameter is
 * one of.
 */
export const EVENT_PARAMETERS = Object.freeze({
  app_launch: Object.freeze({ first: 'boolean' }),
  account_added: Object.freeze({ kind: ACCOUNT_KINDS }),
  account_switched: Object.freeze({}),
  screen_shown: Object.freeze({ screen: SCREENS }),
  command_refused: Object.freeze({ reason: REFUSAL_REASONS }),
  pairing_finished: Object.freeze({ outcome: PAIRING_OUTCOMES }),
  crash_reported: Object.freeze({}),
});

export const TELEMETRY_CODES = Object.freeze({
  /** The name is not one of the seven. */
  UNKNOWN_EVENT: 'unknown-event',
  /** The event does not carry this parameter. */
  UNKNOWN_PARAMETER: 'unknown-parameter',
  /** The event carries a parameter the call did not pass. */
  MISSING_PARAMETER: 'missing-parameter',
  /** The value is neither a boolean nor a member of the parameter's set. */
  PARAMETER_VALUE: 'parameter-value',
});

/** A refusal names the FIELD and never the value: a refusal that echoed what it
 * refused would be the leak it exists to stop. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The event `name` with `parameters`, or the refusal that stops it.
 *
 * The refusals are the closed sets themselves: a name outside the allow-list, a
 * parameter the event does not declare, a declared parameter that was not
 * passed, and a value that is neither a boolean nor a member of its set. A
 * `Secret` is refused by the last of the four without being recognised as one,
 * because it is an object.
 *
 * @param {unknown} name an event name
 * @param {unknown} parameters the parameters that event declares, and no others
 * @returns {{ ok: true, event: { name: string, parameters: object } }
 *   | { ok: false, code: string, field: string }}
 */
export function telemetryEvent(name, parameters = {}) {
  if (typeof name !== 'string' || !Object.hasOwn(EVENT_PARAMETERS, name)) {
    return refuse(TELEMETRY_CODES.UNKNOWN_EVENT, 'name');
  }
  if (parameters === null || typeof parameters !== 'object' || Array.isArray(parameters)) {
    return refuse(TELEMETRY_CODES.PARAMETER_VALUE, 'parameters');
  }
  const declared = EVENT_PARAMETERS[name];
  for (const key of Object.keys(parameters)) {
    if (!Object.hasOwn(declared, key)) return refuse(TELEMETRY_CODES.UNKNOWN_PARAMETER, key);
  }
  const shaped = {};
  for (const [key, set] of Object.entries(declared)) {
    if (!Object.hasOwn(parameters, key)) return refuse(TELEMETRY_CODES.MISSING_PARAMETER, key);
    const value = parameters[key];
    const ok = set === 'boolean' ? typeof value === 'boolean' : set.includes(value);
    if (!ok) return refuse(TELEMETRY_CODES.PARAMETER_VALUE, key);
    shaped[key] = value;
  }
  return Object.freeze({ ok: true, event: Object.freeze({ name, parameters: Object.freeze(shaped) }) });
}

/**
 * One sender per allowed event, each naming its event on its own line so that
 * the scan that holds the allow-list can read it.
 */
export const SENDERS = Object.freeze({
  app_launch: (port, parameters) => port.logEvent('app_launch', parameters),
  account_added: (port, parameters) => port.logEvent('account_added', parameters),
  account_switched: (port, parameters) => port.logEvent('account_switched', parameters),
  screen_shown: (port, parameters) => port.logEvent('screen_shown', parameters),
  command_refused: (port, parameters) => port.logEvent('command_refused', parameters),
  pairing_finished: (port, parameters) => port.logEvent('pairing_finished', parameters),
  crash_reported: (port, parameters) => port.logEvent('crash_reported', parameters),
});

/**
 * The app's telemetry, over the port the entry wires to the SDK.
 *
 * @param {{ logEvent: (name: string, parameters: object) => void }} port
 * @returns {{ record: (name: unknown, parameters?: unknown) => object }}
 * @throws {TypeError} for a port that is not one: that is a wiring bug, and the
 *   place to catch it is where the app is built
 */
export function createTelemetry(port) {
  if (port === null || typeof port !== 'object' || typeof port.logEvent !== 'function') {
    // The message names the method and not its call: the scan that holds the
    // allow-list reads call sites over the whole source, so a message spelling
    // the call out would be read as one -- and refused.
    throw new TypeError('createTelemetry needs a port with a logEvent method');
  }
  return Object.freeze({
    record(name, parameters) {
      const made = telemetryEvent(name, parameters);
      if (!made.ok) return made;
      SENDERS[made.event.name](port, made.event.parameters);
      return made;
    },
  });
}
