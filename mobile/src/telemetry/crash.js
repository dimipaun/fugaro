// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's one crash-report switch, decided from the project's published
// policy (review-levels-ab-jobmax T3.1; D731, D734, D738; the telemetry plan's
// amended §7.4).
//
// **What this module is.** The app's **only** call site that sets collection
// state on Crashlytics, and the whole of D731's opt-out on the phone's side: the
// switch is the project's published policy key `mobile_crash_reports` (a top-level
// key beside the `mobile` block), the
// app reaches it as `crash_reports` in the pairing session's policy, and this
// module reads that one member and turns collection on or off with it. Nothing
// else in the app touches collection state -- §7.4's scan admits exactly this
// call site and refuses any other, which is what keeps "there is one switch and
// it is the project's" a fact about the tree rather than a sentence in a plan.
//
// **The reader fails closed, and that is the load-bearing half (D734, G7).**
// Collection is on only for a policy that says, in so many words,
// `crash_reports: true`. Everything else -- no session at all, a policy that is
// missing or not an object, an installation whose answer predates the member, a
// value that is not a boolean -- reads **off**, and this module answers off
// rather than guessing at the published default `true`. That is the opposite of
// `mobile.enabled`'s absent-member rule in `internal/projectpolicy` (where a
// written block's missing member takes its default, on), and deliberately so:
// the default governs what a **publish** writes, and it must never govern what
// the app does with a switch it did not find. Nothing is sent before the switch
// has been read, and nothing is sent with the switch off, so a value the app
// cannot read is a value that sends nothing.
//
// **Why the fail-safe also covers the older installation.** The app and the
// installation release independently, and a session response from an
// installation older than this member does not carry it. M3's session assembly
// fills the member with this same fail-safe before the port sees it -- so the
// session stands and crash reports read off until the installation is updated
// and the switch is published (the plan records that as a requirement on that
// task, and this reader is the second half of it). Letting a missing member
// reach the app as a refusal would refuse every session on every installation
// not yet updated, which is the alternative the plan records and refuses.
//
// **What this module is NOT.** It does not import the Crashlytics package: the
// SDK arrives as the `sdk` argument, exactly as `events.js`'s port does, so the
// module loads on a machine with no native packages and no test here needs a
// device (`write-path.test.js` rule 5 also keeps the Firebase SDK packages to
// `mobile/index.js`, and `crash.test.js` reads this file for either spelling of
// the scope). It sends no report and shapes none: the payload is the
// native SDK's own, and the app hands it no error, no message and no key -- the
// breadcrumb rule in `analytics-calls.test.js` is what refuses one.

/**
 * Why the switch reads the way it does, so a caller that shows the answer can
 * say which case it is looking at instead of guessing. It is an enumerated
 * reason and never a value: a refusal that echoed what it read would be a path
 * for a policy value to travel.
 */
export const CRASH_REASON = Object.freeze({
  /** The session's policy carries `crash_reports: true` (D731's default). */
  ON: 'policy-on',
  /** The session's policy carries `crash_reports: false`. */
  OFF: 'policy-off',
  /** There is no session: the app is not paired yet. */
  NO_SESSION: 'no-session',
  /** There is a session and its policy cannot be read -- absent, not an object,
   * or carrying a `crash_reports` that is not a boolean, which is also what an
   * installation older than the member answers before M3 fills it. */
  UNREADABLE: 'policy-unreadable',
});

/**
 * The session whose policy decides.
 *
 * A session is the value `shared/mobile-session/port.js` describes; this module
 * reads exactly one member of it and nothing else, so a surface may hand over a
 * session from the port's own stub, from M3, or -- until that task lands -- any
 * object with the same shape.
 *
 * @param {unknown} session a session value, or nothing
 * @returns {{ enabled: boolean, reason: string }}
 *   `enabled` is true only for a policy that says so; `reason` is one of
 *   `CRASH_REASON`.
 */
export function crashReportsOf(session) {
  if (session === null || typeof session !== 'object' || Array.isArray(session)) {
    return Object.freeze({ enabled: false, reason: CRASH_REASON.NO_SESSION });
  }
  const policy = session.policy;
  if (policy === null || typeof policy !== 'object' || Array.isArray(policy)) {
    return Object.freeze({ enabled: false, reason: CRASH_REASON.UNREADABLE });
  }
  const value = policy.crash_reports;
  // Strictly `true`: a truthy string, a `1` or a missing member is a switch the
  // app did not read, and a switch it did not read sends nothing.
  if (value === true) return Object.freeze({ enabled: true, reason: CRASH_REASON.ON });
  if (value === false) return Object.freeze({ enabled: false, reason: CRASH_REASON.OFF });
  return Object.freeze({ enabled: false, reason: CRASH_REASON.UNREADABLE });
}

/**
 * The app's crash-report switch, over the SDK its caller wires.
 *
 * @param {{ setCrashlyticsCollectionEnabled: Function, deleteUnsentReports: Function }} sdk
 *   the Crashlytics instance `mobile/index.js` builds; the module never imports
 *   one itself
 * @returns {{ apply: (session: unknown) => { enabled: boolean, reason: string } }}
 * @throws {TypeError} for an SDK that is not one: that is a wiring bug, and the
 *   place to catch it is where the app is built. The message names the two
 *   methods and not their call, because §7.4's scan reads call sites over the
 *   whole source and a message spelling one out would be read as a second one.
 */
export function createCrashReports(sdk) {
  if (
    sdk === null
    || typeof sdk !== 'object'
    || typeof sdk.setCrashlyticsCollectionEnabled !== 'function'
    || typeof sdk.deleteUnsentReports !== 'function'
  ) {
    throw new TypeError(
      'createCrashReports needs an SDK with setCrashlyticsCollectionEnabled and deleteUnsentReports methods',
    );
  }
  return Object.freeze({
    /**
     * Follow the session's policy, and answer what it read.
     *
     * Called at startup with no session, and again at every session refresh
     * (D731: the app follows a later change): the switch is applied in both
     * directions, so a project that publishes it off stops the next report and a
     * project that turns it back on resumes. An off reading also **deletes the
     * reports already collected and not yet sent** (U2) -- a report waiting on
     * the device while the switch is off is the one thing "sends nothing with
     * the switch off" would otherwise leave behind. The deletion is asked for on
     * every off reading rather than once, because that is what the SDK's own
     * call means and because the app keeps no state of its own about it.
     *
     * @param {unknown} session the current session, or nothing before pairing
     * @returns {{ enabled: boolean, reason: string }} as `crashReportsOf`
     */
    apply(session) {
      const verdict = crashReportsOf(session);
      sdk.setCrashlyticsCollectionEnabled(verdict.enabled);
      if (!verdict.enabled) sdk.deleteUnsentReports();
      return verdict;
    },
  });
}
