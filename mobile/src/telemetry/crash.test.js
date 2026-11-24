// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The crash-report switch's tests (review-levels-ab-jobmax T3.1; D731, D734,
// D738).
//
// **What they pin, and why each case is a policy the plan argues.**
//
//   * off until the session carries the policy: the switch is read from the
//     session's policy and from nothing else, so an app that is not paired yet
//     sends nothing -- and deletes what an earlier session left unsent;
//   * follows a later session's change: D731's opt-out is a switch, so a
//     project that publishes it off stops the next report and one that turns it
//     back on resumes -- the same call, a new session, the other answer;
//   * a project that publishes it false sends nothing: the one value that turns
//     collection off *by being read*, as against every value the app cannot read;
//   * an unreadable policy sends nothing and deletes unsent reports: the
//     fail-safe (G7). The reader answers off for a policy that is absent, is not
//     an object, or carries a `crash_reports` that is not a boolean -- it never
//     falls back to the published default `true`, which governs what a publish
//     writes and not what the app does with a switch it did not find;
//   * an installation whose policy predates the member keeps the session and
//     sends nothing: the skew the plan states. A session response from an older
//     installation does not carry the member, M3's assembly fills the fail-safe,
//     and the app must keep the session rather than refuse it -- refusing would
//     make the whole app unusable on every installation not yet updated.
//
// The SDK is a recorder here: the module never imports the package (`crash.js`
// takes it as an argument, and `write-path.test.js` rule 5 keeps the import to
// `mobile/index.js`), so these cases run on a machine with no native packages,
// which is also why the "deletes unsent reports" half is observable at all.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { CRASH_REASON, createCrashReports, crashReportsOf } from './crash.js';

/** A Crashlytics instance as the app wires it, recording what it was told. */
function recordingSdk() {
  return {
    calls: [],
    unsent: [],
    setCrashlyticsCollectionEnabled(enabled) {
      this.calls.push(enabled);
    },
    deleteUnsentReports() {
      this.unsent.push(true);
    },
  };
}

/** A session as the port describes it, with `crash_reports` as the policy's
 * only interesting member: the rest is there because a session is a whole
 * value, and this module must read one member of it and nothing else. */
function session(crashReports, extra = {}) {
  const policy = { costly_modes: [], biometric_on_costly: false, compaction_threshold: null };
  if (crashReports !== undefined) policy.crash_reports = crashReports;
  return { project_id: 'prj-opaque-7', endpoint: 'https://pairing.fugaro.example', role: 'operator', policy, ...extra };
}

test('crash: off until the session carries the policy', () => {
  const sdk = recordingSdk();
  const crash = createCrashReports(sdk);

  // Nothing at all: before pairing there is no session, so there is no policy
  // to read and nothing is sent (D734: nothing before the switch is read).
  const before = crash.apply(null);
  assert.deepEqual(before, { enabled: false, reason: CRASH_REASON.NO_SESSION });
  assert.deepEqual(sdk.calls, [false]);
  // ... and the reports an earlier state left unsent go with it (U2): a report
  // waiting on the device while the switch is off is the one thing "sends
  // nothing with the switch off" would otherwise leave behind.
  assert.equal(sdk.unsent.length, 1);

  // A session that says so is the only thing that turns it on.
  assert.deepEqual(crash.apply(session(true)), { enabled: true, reason: CRASH_REASON.ON });
  assert.deepEqual(sdk.calls, [false, true]);
  assert.equal(sdk.unsent.length, 1, 'nothing is deleted while the switch is on');
});

test('crash: follows a later session\'s change', () => {
  const sdk = recordingSdk();
  const crash = createCrashReports(sdk);

  assert.equal(crash.apply(session(true)).enabled, true);
  // The same session object, re-read after the project published the switch
  // off: the app follows the policy at its refresh rather than caching the
  // first answer it saw.
  const published = session(false);
  assert.deepEqual(crash.apply(published), { enabled: false, reason: CRASH_REASON.OFF });
  assert.deepEqual(sdk.calls, [true, false]);

  // ... and a project that turns it back on resumes.
  assert.deepEqual(crash.apply(session(true)), { enabled: true, reason: CRASH_REASON.ON });
  assert.deepEqual(sdk.calls, [true, false, true]);
});

test('crash: a project that publishes it false sends nothing', () => {
  const sdk = recordingSdk();
  const crash = createCrashReports(sdk);

  const verdict = crash.apply(session(false));
  assert.deepEqual(verdict, { enabled: false, reason: CRASH_REASON.OFF });
  assert.deepEqual(sdk.calls, [false]);
  assert.equal(sdk.unsent.length, 1, 'the switch being read off deletes the unsent reports');
});

test('crash: an unreadable policy sends nothing and deletes unsent reports', () => {
  const sdk = recordingSdk();
  const crash = createCrashReports(sdk);

  // Every shape of "there is a session and I cannot read its switch". The
  // control is the last of them: a value that is truthy but is not `true` must
  // not read as on, which is the one mistake that would send a report a project
  // did not admit.
  const unreadable = [
    {},
    { policy: null },
    { policy: 'the policy file is not the session' },
    { policy: [] },
    session(undefined),
    session('yes'),
    session(1),
    session(null),
  ];
  for (const value of unreadable) {
    const verdict = crash.apply(value);
    assert.equal(verdict.enabled, false, JSON.stringify(value));
    assert.equal(verdict.reason, CRASH_REASON.UNREADABLE, JSON.stringify(value));
  }
  assert.deepEqual(sdk.calls, unreadable.map(() => false));
  assert.equal(sdk.unsent.length, unreadable.length);

  // The reader on its own, so the two halves are pinned separately: an absent
  // member answers off, and it does not answer the published default.
  assert.deepEqual(crashReportsOf(session(undefined)), {
    enabled: false,
    reason: CRASH_REASON.UNREADABLE,
  });
});

test('crash: an installation whose policy predates the member keeps the session and sends nothing', () => {
  const sdk = recordingSdk();
  const crash = createCrashReports(sdk);

  // What the app holds after M3's assembly has mapped an older installation's
  // session response into the port value: the member is filled with the
  // fail-safe rather than left missing, because the port refuses a policy that
  // omits a listed member (`MISSING_MEMBER`) and refusing would make the whole
  // app unusable on every installation not yet updated. The session stands --
  // nothing here throws and nothing here refuses -- and crash reports are off
  // until the installation is updated and the switch is published.
  const older = session(false);
  assert.equal(older.policy.crash_reports, false);
  const verdict = crash.apply(older);
  assert.deepEqual(verdict, { enabled: false, reason: CRASH_REASON.OFF });
  assert.deepEqual(sdk.calls, [false]);

  // The raw response, read before any assembly has filled it, is the second
  // half of the same fact: a policy without the member reads off too, so the
  // fill is belt-and-braces rather than the only thing standing between an
  // unread switch and a sent report.
  assert.equal(crashReportsOf({ policy: { costly_modes: [] } }).enabled, false);
});

test('crash: the switch refuses an SDK that is not one, and applies nothing', () => {
  for (const sdk of [
    null,
    undefined,
    'the crashlytics package',
    {},
    { setCrashlyticsCollectionEnabled() {} },
    { deleteUnsentReports() {} },
    { setCrashlyticsCollectionEnabled: 'not-a-function', deleteUnsentReports() {} },
  ]) {
    assert.throws(() => createCrashReports(sdk), TypeError, JSON.stringify(sdk ?? null));
  }
  // The message names the two methods and never their call: §7.4's scan reads
  // call sites over the whole source, so a message spelling one out would be
  // read as a second call site.
  assert.throws(
    () => createCrashReports(null),
    (error) => !/setCrashlyticsCollectionEnabled\s*\(/.test(error.message),
  );
});

test('crash: the module reaches the SDK only through the argument it is given', () => {
  // The load-bearing half of "there is one switch and it is the project's": the
  // module holds no SDK of its own and no key of its own, so a second call site
  // cannot be grown here by importing the package. §7.4's scan is the app-wide
  // half of it; this is the module's own.
  const source = readFileSync(join(import.meta.dirname, 'crash.js'), 'utf8');
  assert.equal(/@react-native-firebase/.test(source), false, 'crash.js must not import the SDK');
  assert.equal(/(^|\n)\s*import\b/.test(source), false, 'the module needs no import at all');
});
