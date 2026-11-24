// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The build-time config, against the two states it resolves in (task M0;
// Check 733).
//
// **Why the config is tested at all.** It is the one file of this spike that
// runs on EAS's build worker rather than on a phone, and the failures it can
// have are the expensive kind: a build that resolves no bundle identifier
// cannot be uploaded, a build that resolves somebody else's is a spike under the
// wrong name, and a message that echoed a value would put the owner's
// identifier in a build log. The three are cheap to pin here.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { VARIABLES, resolveConfig } from '../../app.config.js';

/** The static config Expo passes in, read from the committed `app.json`. */
const STATIC = JSON.parse(readFileSync(join(import.meta.dirname, '..', '..', 'app.json'), 'utf8')).expo;

/** A complete environment, as a build has after `eas env:set` for each name. */
const BUILD_ENV = Object.freeze({
  EAS_BUILD: '1',
  [VARIABLES.iosBundle]: 'invalid.example.spike733',
  [VARIABLES.androidPackage]: 'invalid_example_spike733',
  [VARIABLES.iosAppGroup]: 'group.invalid.example.spike733',
  [VARIABLES.iosExtension]: 'invalid.example.spike733.share',
});

/** The plugin entry the share route is registered through, or undefined. */
function sharePlugin(config) {
  return (config.plugins ?? []).find((entry) => Array.isArray(entry) && entry[0] === 'expo-share-intent');
}

test('the committed app.json carries no identity and no client id', () => {
  // The same fence the product app's own config is held to (D125): an identity
  // in a committed file is an identity in a public repository, and this file is
  // the one a reviewer reads.
  assert.equal(STATIC.ios?.bundleIdentifier, undefined);
  assert.equal(STATIC.android?.package, undefined);
  assert.deepEqual(STATIC.extra, {}, 'app.json carries no value; app.config.js is where the environment lands');
  assert.equal(JSON.stringify(STATIC).includes('ClientId'), false);
});

test('a dev run resolves without a store account, and adds no identity', () => {
  const config = resolveConfig(STATIC, {});
  assert.equal(config.ios?.bundleIdentifier, undefined);
  assert.equal(config.android?.package, undefined);
  assert.deepEqual(config.extra, {
    googleClientId: '',
    googleIosClientId: '',
    googleAndroidClientId: '',
    microsoftClientId: '',
  });
  // The three plugins are registered in both states: what differs between them
  // is the identities, not the native halves.
  for (const name of ['expo-secure-store', 'expo-apple-authentication', 'expo-share-intent']) {
    assert.ok((config.plugins ?? []).some((entry) => (Array.isArray(entry) ? entry[0] : entry) === name), name);
  }
  // ... and the share extension's two names are left to the plugin's own
  // defaults rather than written as empty strings, which prebuild would take as
  // an identifier.
  const options = sharePlugin(config)[1];
  assert.equal('iosAppGroupIdentifier' in options, false);
  assert.equal('iosShareExtensionBundleIdentifier' in options, false);
  assert.deepEqual(options.androidIntentFilters, ['text/*'], 'the Android half is text, which is U7');
});

test('a build resolves the four identities from the environment', () => {
  const config = resolveConfig(STATIC, BUILD_ENV);
  assert.equal(config.ios.bundleIdentifier, BUILD_ENV[VARIABLES.iosBundle]);
  assert.equal(config.android.package, BUILD_ENV[VARIABLES.androidPackage]);
  const options = sharePlugin(config)[1];
  assert.equal(options.iosAppGroupIdentifier, BUILD_ENV[VARIABLES.iosAppGroup]);
  assert.equal(options.iosShareExtensionBundleIdentifier, BUILD_ENV[VARIABLES.iosExtension]);
  assert.equal(options.iosActivationRules.NSExtensionActivationSupportsText, true, 'a shared text is the question');
});

test('a build without one of the four stops, names it, and repeats no value', () => {
  for (const [name, what] of [
    [VARIABLES.iosBundle, /iOS bundle identifier/],
    [VARIABLES.androidPackage, /Android package name/],
    [VARIABLES.iosAppGroup, /App Group/],
    [VARIABLES.iosExtension, /share extension bundle identifier/],
  ]) {
    const env = { ...BUILD_ENV };
    delete env[name];
    assert.throws(() => resolveConfig(STATIC, env), (error) => {
      assert.match(error.message, new RegExp(name));
      assert.match(error.message, what);
      // The value of a variable that *was* set is nowhere in the message: this
      // is a build log, and a mistyped variable is exactly what must not be
      // printed into one.
      for (const [key, value] of Object.entries(env)) {
        if (key === 'EAS_BUILD') continue;
        assert.equal(error.message.includes(value), false, `${key}'s value is not in the message`);
      }
      return true;
    }, name);
  }
  // ... and the same environment with nothing missing resolves, so the loop
  // above is about the one variable it removed.
  assert.ok(resolveConfig(STATIC, BUILD_ENV).ios.bundleIdentifier);
});

test('a variable that is set is read in a dev run too, and validated', () => {
  const config = resolveConfig(STATIC, { [VARIABLES.iosBundle]: 'invalid.example.spike733' });
  assert.equal(config.ios.bundleIdentifier, 'invalid.example.spike733');
  assert.equal(config.android?.package, undefined);
});

test('a caller that is not Expo is refused rather than resolved against nothing', () => {
  for (const value of [undefined, null, 'app.json', []]) {
    assert.throws(() => resolveConfig(value, BUILD_ENV), /static config/, String(value));
  }
});
