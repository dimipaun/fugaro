// The app config's tests (mobile plan, Task P3.1; D111, D114, D125).
//
// `app.json` is the one file in the app that is read by a build machine rather
// than by the app, and it is the one file a reviewer is least likely to read
// twice. Two things must be true of it, and neither is checkable by looking:
//
//   * **no credential is in it.** Not a token, not a key, not a service
//     account, not a credential-shaped value under any name. The check is the
//     same `secretShaped` list `settings.js` refuses with, so the config file
//     and the settings file cannot disagree about what a credential looks
//     like -- one list, in one place, asked about both.
//   * **no installation is in it.** D114's account is typed by the owner into
//     the app; an app that shipped with a Firebase project baked into its
//     config would not be "the list is owner-maintained, not discovered" -- it
//     would be an installation every build of the app has, including one
//     somebody else builds. The check is that no field of the tuple appears,
//     and that the release identities (the iOS bundle identifier and the
//     Android package) are absent too: those are the owner's, and they differ
//     per installation (D125's release path, check 89 is owner-run).
//
// The property the checks are walked with is tested against a planted secret
// first, so a green run means the walk can fail.

import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { secretShaped } from './accounts/index.js';

const HERE = import.meta.dirname;
const APP_JSON = join(HERE, '..', 'app.json');
const RAW = readFileSync(APP_JSON, 'utf8');
const CONFIG = JSON.parse(RAW);
const MANIFEST = JSON.parse(readFileSync(join(HERE, '..', 'package.json'), 'utf8'));

/** Every string in a JSON value, with the path it was found at. */
function stringsIn(value, path = '$') {
  if (typeof value === 'string') return [[path, value]];
  if (Array.isArray(value)) return value.flatMap((item, i) => stringsIn(item, `${path}[${i}]`));
  if (value !== null && typeof value === 'object') {
    return Object.entries(value).flatMap(([key, nested]) => stringsIn(nested, `${path}.${key}`));
  }
  return [];
}

/** Every key path in a JSON value. */
function keyPaths(value, path = '$') {
  if (Array.isArray(value)) return value.flatMap((item, i) => keyPaths(item, `${path}[${i}]`));
  if (value !== null && typeof value === 'object') {
    return Object.entries(value).flatMap(([key, nested]) => [
      `${path}.${key}`,
      ...keyPaths(nested, `${path}.${key}`),
    ]);
  }
  return [];
}

/** The keys a credential might hide UNDER, whatever its value looks like. */
const CREDENTIAL_NAMES = /(token|secret|password|credential|privatekey|apikey|serviceaccount)/i;

test('the walk finds a planted credential, so the rest of this file can fail', () => {
  // The positive control. Without it, "no secret-shaped value in app.json"
  // would be an assertion that a broken walk also satisfies.
  const planted = {
    expo: { extra: { firebase: { apiKey: `AI${'za'}${'y'.repeat(35)}` } } },
    ios: { bundleIdentifier: 'com.example.app' },
  };
  const found = stringsIn(planted).filter(([, value]) => secretShaped(value) !== null);
  assert.deepEqual(found.map(([path, value]) => [path, secretShaped(value)]), [
    ['$.expo.extra.firebase.apiKey', 'google-api-key'],
  ]);
});

test('no credential-shaped value, and no credential-shaped name, in app.json', () => {
  for (const [path, value] of stringsIn(CONFIG)) {
    assert.equal(secretShaped(value), null, `${path} is ${secretShaped(value)}`);
    // A value that looks like nothing but sits under `apiKey` is still a
    // credential by intent; the name list is the second half of the fence.
    assert.equal(CREDENTIAL_NAMES.test(path), false, path);
  }
  assert.equal(RAW.includes('fugaro.account.'), false, 'the secure store\'s namespace is not config');
  assert.equal(RAW.includes('fugaro.settings.'), false, 'the settings namespace is not config either');
});

test('the config ships with no installation in it', () => {
  // D114: the account is typed by the owner in the app. A project id, an
  // operator uid or a Fugaro project name in the build config would be an
  // installation baked into every copy of the app.
  // Against the KEYS, not the raw text: `android` contains `id`, and a
  // substring search over the JSON would fail on the word for the platform.
  const keys = keyPaths(CONFIG);
  for (const field of ['id', 'firebaseProjectId', 'authUid', 'fugaroProject']) {
    assert.equal(keys.some((key) => key.split('.').at(-1) === field), false, field);
  }
  // ... and the release identities are the owner's (check 89 is owner-run):
  // they are per-installation and per-store, and nothing here guesses one.
  assert.equal('bundleIdentifier' in (CONFIG.expo.ios ?? {}), false);
  assert.equal('package' in (CONFIG.expo.android ?? {}), false);
  assert.equal(RAW.includes('com.'), false, 'no reverse-DNS identity is committed here');
});

test('the config is a config, and the entry it needs exists', () => {
  assert.equal(typeof CONFIG.expo, 'object');
  assert.equal(CONFIG.expo.name, 'Fugaro');
  assert.equal(CONFIG.expo.slug, 'fugaro');
  assert.equal(CONFIG.expo.version, MANIFEST.version, 'the config and the manifest agree on the version');
  // Expo resolves the app's entry through the manifest's `main`, so the two
  // files have to agree: a config pointing at a file that is not there is an
  // app that fails to start on a device, which no test here could see.
  assert.equal(MANIFEST.main, 'index.js');
  assert.equal(existsSync(join(HERE, '..', MANIFEST.main)), true, 'the entry exists');
  assert.equal(existsSync(join(HERE, 'app', 'root.js')), true, 'the root the entry imports exists');
});
