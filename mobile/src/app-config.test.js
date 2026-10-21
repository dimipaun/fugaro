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
//
// **P4.4's half (added 2026-10-19).** "No identity in `app.json`" is only half
// a fence: a store build cannot exist without the two identities, so the file
// below it -- `mobile/app.config.js`, Expo's dynamic config -- resolves them
// from EAS environment variables at build time. The last test here reads the
// RESOLVED config through that file in both modes: a build carries the two
// identities and nothing else new, a dev/lint run gets the static config
// unchanged. `src/app-config.ids.test.js` owns the refusals, and
// `src/eas-config.test.js` owns the profiles the build is made from. What
// `app.json` must **point at** -- the two icons and the splash, the fields a
// build resolves and no test here reads -- is `src/app-config.icons.test.js`'s.

import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { secretShaped } from './accounts/index.js';
import { ID_VARIABLES, resolveConfig } from '../app.config.js';

const HERE = import.meta.dirname;
const APP_JSON = join(HERE, '..', 'app.json');
const RAW = readFileSync(APP_JSON, 'utf8');
const CONFIG = JSON.parse(RAW);
const MANIFEST = JSON.parse(readFileSync(join(HERE, '..', 'package.json'), 'utf8'));

/**
 * The static config as **a resolution** carries it: `app.json`'s `expo` object
 * plus the export-compliance declaration `app.config.js` merges into
 * `ios.infoPlist` (the owner's ruling of 2026-10-20, below). Written out field
 * for field rather than by calling the file's own merge, so this is a statement
 * about what a resolved config should carry and not a second copy of the code
 * that produces it. The identities are the only thing a resolution adds that is
 * not here, because they come from the environment and are per-installation.
 */
const DECLARED = Object.freeze({
  ...CONFIG.expo,
  ios: { ...CONFIG.expo.ios, infoPlist: { ITSAppUsesNonExemptEncryption: false } },
});

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

// --- ... and the one file the identities come from (P4.4, D120, D125) -------
//
// The three tests above are what keeps the identities OUT of `app.json`. This
// one is the other half of the same fence: a build still needs them, so
// `mobile/app.config.js` -- Expo's dynamic config, the `({ config }) => ...`
// form Expo calls with the `expo` object it read from `app.json` -- resolves
// them from the environment at build time, and from nowhere else. Read
// together: `app.json` carries no identity, a build carries both, and the two
// facts are exactly one file apart.
//
// `app.config.js` and its own tests (`src/app-config.ids.test.js`) own the
// refusals (a missing or malformed variable under `EAS_BUILD`); what is pinned
// here is the boundary -- that the static config is what a dev/lint run gets
// back, the two identities apart, and that resolving a build config changes
// those two fields and nothing else.
//
// **The export-compliance answer (added 2026-10-20).** `eas build` printed that
// it cannot write `ITSAppUsesNonExemptEncryption` into a **dynamic** config --
// `app.config.js` is a function, so there is no object in it for the CLI to
// edit -- and asked for it by hand. The owner ruled on 2026-10-20 ("write it,
// it's OK"): the app uses only standard/exempt encryption, so the answer is
// declared in the config a build reads, and the store submission no longer
// stops on the question. The test below is that declaration's, and it is here
// rather than in `app-config.ids.test.js` because it is the other half of this
// file's boundary: what `app.json` must NOT carry (an identity, a credential,
// an installation) and what the config a build reads must.

test('the identities are resolved through app.config.js, in both modes', () => {
  // A build: `EAS_BUILD` is what EAS's build servers set, and the two
  // variables are the ones the owner sets in the project's environments. The
  // value below is a **fictional sample** -- the owner's own identity is set in
  // the EAS project and is committed nowhere (the pin in `app-config.ids.test.js`
  // walks the tree for it).
  const SAMPLE = 'com.example.sample';
  const resolved = resolveConfig(CONFIG.expo, {
    EAS_BUILD: 'true',
    [ID_VARIABLES.ios]: SAMPLE,
    [ID_VARIABLES.android]: SAMPLE,
  });
  assert.equal(resolved.ios.bundleIdentifier, SAMPLE);
  assert.equal(resolved.android.package, SAMPLE);
  // ... and they are the ONLY difference a build makes to the committed file:
  // strip them and what is left is the static config plus the declaration
  // every resolution carries (`DECLARED`, and the test below owns it), field
  // for field.
  const ios = { ...resolved.ios };
  const android = { ...resolved.android };
  delete ios.bundleIdentifier;
  delete android.package;
  assert.deepEqual({ ...resolved, ios, android }, DECLARED);

  // A dev/lint run (a developer's `expo start`, and this suite): neither
  // switch is set, so the resolved config is the static one, with no identity
  // resolved onto it -- `npm test` needs no store account, and this is where
  // that is asserted rather than assumed. The declaration is there too: it is
  // the app's answer and not the environment's.
  assert.deepEqual(resolveConfig(CONFIG.expo, {}), DECLARED);
  assert.equal('bundleIdentifier' in CONFIG.expo.ios, false, 'and the static file never grew one');
  assert.equal('package' in CONFIG.expo.android, false);
  // ... a value that is set is used without being required: this is the state
  // `eas-cli` resolves the file in when it runs locally (`EAS_BUILD` is set by
  // the build worker, not by the CLI, and the CLI needs the identity to
  // configure credentials and to submit), so the switch decides whether the
  // identities are **required**, not whether they are read.
  assert.deepEqual(
    resolveConfig(CONFIG.expo, { [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE }),
    resolved,
  );
});

// --- the export-compliance answer the owner ruled on (2026-10-20) -----------

test('a resolved config declares that the app uses only exempt encryption', () => {
  // The plist key Apple asks about, and the answer the owner gave: `false` --
  // the app uses only standard/exempt encryption -- so a store submission does
  // not stop on the question. It is the **app's** answer and not the
  // environment's, so it is in **every** resolution: a build, and the dev/lint
  // run `eas-cli` makes locally before it uploads one.
  const SAMPLE = 'com.example.sample';
  for (const [what, env] of [
    ['a build', { EAS_BUILD: 'true', [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE }],
    ['a dev/lint run', {}],
  ]) {
    const { infoPlist } = resolveConfig(CONFIG.expo, env).ios;
    // The value is the **boolean** `false`, not the string `'false'`: a plist
    // value is typed, Apple reads a boolean, and a string is a different value
    // -- and a different answer. Mutation: write `'false'` in
    // `app.config.js`'s `EXPORT_COMPLIANCE` and the `typeof` line goes red.
    assert.equal(infoPlist.ITSAppUsesNonExemptEncryption, false, what);
    assert.equal(typeof infoPlist.ITSAppUsesNonExemptEncryption, 'boolean', what);
    // ... and it is the only key this file merges in, spelled as Apple spells
    // it. Mutation: delete the declaration from `app.config.js` and the first
    // assertion finds `undefined`; rename the key and the two assertions above
    // find `undefined` too, and this line is the one that says which key was
    // expected.
    assert.deepEqual(Object.keys(infoPlist), ['ITSAppUsesNonExemptEncryption'], what);
  }
  // The declaration is **merged** into `ios.infoPlist` and does not replace it:
  // `infoPlist` is a shared object -- the base config sets keys in it, and an
  // Expo config plugin writes its own into it at prebuild time -- so a key that
  // is already there survives. Mutation: assign `infoPlist` instead of spreading
  // it in `app.config.js`'s `declaring` and the first key below is gone.
  const base = {
    ...CONFIG.expo,
    ios: { ...CONFIG.expo.ios, infoPlist: { UIStatusBarHidden: false } },
  };
  assert.deepEqual(resolveConfig(base, {}).ios.infoPlist, {
    UIStatusBarHidden: false,
    ITSAppUsesNonExemptEncryption: false,
  });
  // ... and resolving does not write it back into the object it was handed: the
  // declaration lives on the resolution, so the fixture here keeps the one key
  // it came with. Mutation: mutate `section.infoPlist` in place instead of
  // returning a new object and this goes red.
  assert.deepEqual(Object.keys(base.ios.infoPlist), ['UIStatusBarHidden']);
  // ... and the declaration is not in `app.json`, which is the file a reviewer
  // reads and the file a build reads only through `app.config.js`: one site,
  // and it is the dynamic config. Mutation: add the key to `app.json` as well
  // (the other place `eas build` offered) and this goes red.
  assert.equal(
    RAW.includes('ITSAppUsesNonExemptEncryption'),
    false,
    'the declaration lives in app.config.js, not in the static config',
  );
});
