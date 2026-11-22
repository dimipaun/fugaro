// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.

// The ad-ID walk (plan §7.4, build-time half; D731's ruling keeps this ban).
//
// The website's privacy page promises the app carries no advertising
// identifier, and a promise is only true while something holds it. Two
// structural facts hold this one, and this test walks both:
//
//   * `com.google.android.gms.permission.AD_ID` is in the resolved config's
//     `android.blockedPermissions`, so prebuild removes it from the merged
//     manifest -- and this is now the **whole** structural ban on that
//     platform, because the Analytics plugin entry that carried the second
//     half left the app with its package;
//   * the generated manifest, where a machine has one, does not declare the
//     permission.
//
// **What T3.1 removed from this walk, and why that is not the ban lapsing.**
// The ban used to be held on three sides: the two Android ad switches in
// `mobile/firebase.json`, the iOS `ios.withoutAdIdSupport` option on the
// Analytics plugin entry, and the blocked permission. D731 drops Analytics from
// the app, so the two switches and the plugin entry left with the package --
// those keys have no reader left, and the walk asserts what still has one. The
// Android removal (the manifest placeholder) and the absence of the package
// itself (`scan.test.js`'s rule, and `notice.test.js`'s clause that the app
// carries no advertising identifier) are what a build and a reader are held to
// now.
//
// The generated native project is checked **when it exists** -- it is not
// committed, and no test here runs `expo prebuild` -- so the manifest is its
// own case, skipped with the reason on a machine that has none rather than
// passing quietly. This walk deliberately
// does **not** assert `firebase_crashlytics_collection_enabled`: the
// Crashlytics library's own manifest hardcodes it `false` and RNFirebase's init
// provider turns collection on from `firebase.json`'s
// `crashlytics_auto_collection_enabled`, so the manifest value is the library's
// and not this app's answer (§4.2).

import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { resolveConfig } from '../app.config.js';

const HERE = import.meta.dirname;
const CONFIG = JSON.parse(readFileSync(join(HERE, '..', 'app.json'), 'utf8'));

const AD_ID_PERMISSION = 'com.google.android.gms.permission.AD_ID';

/** The generated Android manifest, when this machine has one (it is not
 * committed, and nothing here prebuilds). */
const MANIFEST_PATH = join(HERE, '..', 'android', 'app', 'src', 'main', 'AndroidManifest.xml');
const MANIFEST = existsSync(MANIFEST_PATH) ? readFileSync(MANIFEST_PATH, 'utf8') : undefined;

/**
 * Every way the build declares the permission, as a list of sentences. An empty
 * list is the promise kept; the text of each failure names the site, so a
 * planted control says which rule caught it.
 */
function adIdFailures({ config, manifest }) {
  const failures = [];
  if (!(config.android?.blockedPermissions ?? []).includes(AD_ID_PERMISSION)) {
    failures.push(`the resolved config does not block ${AD_ID_PERMISSION}`);
  }
  if (manifest !== undefined) {
    // A `tools:node="remove"` entry is the removal, not a declaration of use.
    for (const line of manifest.split('\n')) {
      if (line.includes(`android:name="${AD_ID_PERMISSION}"`) && !line.includes('tools:node="remove"')) {
        failures.push(`the generated manifest declares ${AD_ID_PERMISSION}`);
      }
    }
  }
  return failures;
}

/** The real inputs, and the resolved config a build reads. */
const RESOLVED = resolveConfig(CONFIG.expo, {});

test('the build enables no advertising-id collection: the source half holds', () => {
  // The half that is source -- the resolved config -- with the generated half
  // named as unread rather than left to be whatever this machine happens to
  // have.
  assert.deepEqual(adIdFailures({ config: RESOLVED, manifest: undefined }), []);
});

test('the generated Android manifest declares no ad-ID permission', {
  // A machine with no native project cannot answer this, and says so: nothing
  // in this repository prebuilds, so the manifest is absent in a checkout and
  // present only where the owner ran one (the same rule §4.3's tracked-file
  // test follows). Skipped rather than asserted away -- the assertion below is
  // the only thing that reads the generated file, and it is real where there is
  // one to read.
  skip:
    MANIFEST === undefined
      ? 'this machine has no generated native project, so the manifest half of the walk is unread'
      : false,
}, () => {
  assert.deepEqual(adIdFailures({ config: RESOLVED, manifest: MANIFEST }), []);
});

test('every rule of the walk can fail: one planted control each', () => {
  const real = { config: RESOLVED, manifest: MANIFEST };
  const planted = (change) => adIdFailures({ ...real, ...change });

  // ... the permission removal, planted by taking it out of the list.
  assert.deepEqual(
    planted({
      config: { ...RESOLVED, android: { ...RESOLVED.android, blockedPermissions: [] } },
    }),
    [`the resolved config does not block ${AD_ID_PERMISSION}`],
  );

  // ... and the generated manifest, planted as real text, so the half that is
  // skipped on this machine is still proved able to fail.
  assert.deepEqual(
    planted({
      manifest: `<manifest><uses-permission android:name="${AD_ID_PERMISSION}"/></manifest>`,
    }),
    [`the generated manifest declares ${AD_ID_PERMISSION}`],
  );
  // ... while the removal prebuild writes is not a declaration, so the planted
  // control is about the permission being USED and not about the string.
  assert.deepEqual(
    planted({
      manifest: `<manifest><uses-permission android:name="${AD_ID_PERMISSION}" tools:node="remove"/></manifest>`,
    }),
    [],
  );
});
