// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
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
/**
 * The two native config paths a build resolves, as a **sample**: the files
 * themselves are never here (plan 4.3), and a build machine's own paths are
 * what `app.config.js` reads off the environment and never writes down.
 */
const NATIVE_ENV = Object.freeze({
  [GOOGLE_SERVICES_VARIABLES.android]: '/build/secrets/google-services.json',
  [GOOGLE_SERVICES_VARIABLES.ios]: '/build/secrets/GoogleService-Info.plist',
});

const DECLARED = Object.freeze({
  ...CONFIG.expo,
  plugins: [...CONFIG.expo.plugins, ...FIREBASE_PLUGINS, ...BUILD_PROPERTIES_PLUGINS],
  ios: { ...CONFIG.expo.ios, infoPlist: { ITSAppUsesNonExemptEncryption: false } },
  android: { ...CONFIG.expo.android, blockedPermissions: [...BLOCKED_PERMISSIONS] },
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
    ...NATIVE_ENV,
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
  delete ios.googleServicesFile;
  delete android.googleServicesFile;
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
    resolveConfig(CONFIG.expo, { [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE, ...NATIVE_ENV }),
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
    ['a build', { EAS_BUILD: 'true', [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE, ...NATIVE_ENV }],
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

// --- the Firebase config plugin, the native files and the ad-ID promise ------
// (mobile telemetry plan, Task P7.1; D254, D255, D259, D260; §4.2, §4.3, §7.4)
//
// Phase 7 puts a Firebase SDK in the app, and the plan's §4.3 says the two
// native config files are never committed while a build cannot be made without
// them; §4.2 and §7.4 say the app carries no advertising identifier, which is
// the sentence the website's privacy page publishes. Both are facts about
// files that are not in this repository - the two config files, and the native
// project a build generates from them - so they are held from the source side
// (the plugin entries and `mobile/firebase.json`, both always) and from the
// generated side (the files a prebuild writes, **when they are there**: on a
// machine that has never run one, the test says which half it could not see
// rather than passing quietly). Every rule has a planted control, in the shape
// the rest of the tree uses.

import { spawnSync } from 'node:child_process';

import {
  BLOCKED_PERMISSIONS,
  BUILD_PROPERTIES_PLUGINS,
  FIREBASE_PLUGINS,
  GOOGLE_SERVICES_VARIABLES,
  NATIVE_CONFIG_README_SECTION,
  nativeConfigMissing,
} from '../app.config.js';

const APP_ROOT = join(HERE, '..');
const NATIVE_FILES = ['google-services.json', 'GoogleService-Info.plist'];
const FIREBASE_JSON = JSON.parse(readFileSync(join(APP_ROOT, 'firebase.json'), 'utf8'));
const APP_CONFIG_RAW = readFileSync(join(APP_ROOT, 'app.config.js'), 'utf8');

/** A git command, run in the app directory. Nothing here reaches the network. */
function git(args, env = {}) {
  return spawnSync('git', args, {
    cwd: APP_ROOT,
    encoding: 'utf8',
    env: { ...process.env, ...env },
  });
}

/** The paths of `paths` that git's index holds. */
function tracked(paths) {
  const result = git(['ls-files', '--', ...paths]);
  assert.equal(result.status, 0, `git ls-files failed: ${result.stderr}`);
  return result.stdout.split('\n').filter(Boolean);
}

/** The paths of `paths` the app's ignore rules match (`--no-index`: the index is not consulted). */
function ignored(paths) {
  const result = git(['check-ignore', '--no-index', '--', ...paths]);
  assert.equal(
    [0, 1].includes(result.status),
    true,
    `git check-ignore failed: ${result.stderr}`,
  );
  return result.stdout.split('\n').filter(Boolean);
}

test('the native config files are untracked, and the app ignores them', () => {
  // The control first: a path git DOES hold has to come back, or "neither file
  // is tracked" would be what a broken walk says too. `app.json` is that path -
  // committed, in this repository's own index, and not one of the two the rule
  // is about - so it cannot pass by being the answer the walk wanted. (The
  // plan's wording is a planted entry in a temp index; a real committed path
  // proves the same property - the walk tells tracked from untracked - and a
  // temp `GIT_INDEX_FILE` is read as empty in this repository, which would make
  // the control prove nothing.)
  assert.deepEqual(
    tracked([...NATIVE_FILES, 'app.json']),
    ['app.json'],
    'the tracked-file walk can fail: a path the index holds comes back',
  );

  // ... and the same walk over the paths that matter, in the real index.
  for (const name of NATIVE_FILES) {
    assert.deepEqual(
      tracked([name]),
      [],
      `${name} is tracked by git: it names the telemetry project and carries its API key, so it is never committed (plan 4.3)`,
    );
    assert.deepEqual(
      ignored([name]),
      [name],
      `${name} is not ignored: mobile/.gitignore has to ignore it, and .gitignore is the only thing standing between that file and a commit`,
    );
  }
  // The other direction of the ignore walk, as its control: a file the app DOES
  // commit is not reported as ignored, so `ignored()` is not a function that
  // says yes to everything.
  assert.deepEqual(ignored(['app.json']), []);
});

test('the build-time guard refuses a build without the two native config paths', () => {
  const [android, ios] = [GOOGLE_SERVICES_VARIABLES.android, GOOGLE_SERVICES_VARIABLES.ios];
  assert.deepEqual(nativeConfigMissing({}), [android, ios], 'a build with neither variable set is missing both');
  assert.deepEqual(nativeConfigMissing({ [android]: '' }), [android, ios], 'an empty variable is not a path');
  assert.deepEqual(nativeConfigMissing({ [android]: '/build/secrets/g-services.json' }), [ios]);
  assert.deepEqual(
    nativeConfigMissing({ [android]: '/build/secrets/g-services.json', [ios]: '/build/secrets/g-plist' }),
    [],
    'both present is the only state a build accepts',
  );
  // The control that matters, because the profiles declare the two names in
  // their own `env` blocks (see `src/eas-config.test.js`): a value that is
  // still the variable's own name is a declaration EAS did not replace with
  // the file's path, and a build reading it would look for a file called
  // `GOOGLE_SERVICES_JSON`. It is refused here.
  assert.deepEqual(
    nativeConfigMissing({ [android]: android, [ios]: ios }),
    [android, ios],
    'a value equal to its own variable name is a declaration, not a path',
  );

  // ... and the call the guard makes: an EAS build without them stops, and the
  // message names each missing variable and the README section, and repeats no
  // value. The positive control is the same call with both variables set, which
  // must NOT throw.
  const SAMPLE = 'com.example.sample';
  const identity = { [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE };
  assert.throws(
    () => resolveConfig(CONFIG.expo, { ...identity, EAS_BUILD: 'true' }),
    (error) => {
      assert.equal(error.message.includes(android), true, 'the message names the Android variable');
      assert.equal(error.message.includes(ios), true, 'and the iOS one');
      assert.equal(
        error.message.includes(NATIVE_CONFIG_README_SECTION),
        true,
        'and the README section that says how the owner sets them',
      );
      assert.equal(error.message.includes(SAMPLE), false, 'and repeats no value');
      return true;
    },
  );
  const resolved = resolveConfig(CONFIG.expo, {
    ...identity,
    EAS_BUILD: 'true',
    [android]: '/build/secrets/g-services.json',
    [ios]: '/build/secrets/g-plist',
  });
  assert.equal(resolved.android.googleServicesFile, '/build/secrets/g-services.json');
  assert.equal(resolved.ios.googleServicesFile, '/build/secrets/g-plist');
  // A dev/lint run still resolves: the guard is a build's, not the suite's.
  assert.equal('googleServicesFile' in resolveConfig(CONFIG.expo, {}).android, false);
});

/**
 * The ad-ID findings, as a function of the four sources §7.4 names: the plugin
 * entries, `firebase.json`, and - when a prebuild has been run - the generated
 * `AndroidManifest.xml` and `Podfile.lock`. Each is a sentence about which
 * source enabled what, so a red run says what to fix rather than that a
 * boolean differed.
 *
 * The manifest's own values arrive as Gradle manifest placeholders
 * (`android:value="${firebaseJsonAdidEnabled}"`), because that is how
 * `@react-native-firebase/analytics`'s own manifest declares them and Gradle
 * resolves them from `firebase.json` at assemble time - which is why the
 * placeholder table below is part of this walk and not a detail of it.
 */
const MANIFEST_PLACEHOLDERS = Object.freeze({
  firebaseJsonAutoCollectionEnabled: 'analytics_auto_collection_enabled',
  firebaseJsonAdidEnabled: 'google_analytics_adid_collection_enabled',
  firebaseJsonPersonalizationEnabled: 'analytics_default_allow_ad_personalization_signals',
});

function adIdFindings({ plugins, firebaseJson, manifest, podfileLock }) {
  const found = [];
  const entries = (plugins ?? []).map((entry) => (Array.isArray(entry) ? { name: entry[0], options: entry[1] } : { name: entry }));
  const analytics = entries.find((entry) => entry.name === '@react-native-firebase/analytics');
  if (analytics === undefined) {
    found.push('app.config.js lists no @react-native-firebase/analytics plugin');
  } else if (analytics.options?.ios?.withoutAdIdSupport !== true) {
    found.push(
      'app.config.js does not select the ad-ID-free Analytics variant: ios.withoutAdIdSupport must be true, which writes $RNFirebaseAnalyticsWithoutAdIdSupport into the Podfile',
    );
  }

  const switches = firebaseJson?.['react-native'] ?? {};
  const wanted = {
    google_analytics_adid_collection_enabled: false,
    analytics_default_allow_ad_personalization_signals: false,
    analytics_auto_collection_enabled: true,
    crashlytics_auto_collection_enabled: true,
  };
  for (const [key, value] of Object.entries(wanted)) {
    if (switches[key] !== value) {
      found.push(`firebase.json's react-native block does not set ${key} to ${value} (found ${JSON.stringify(switches[key])})`);
    }
  }

  if (manifest !== undefined) {
    const value = (name) => {
      const tag = new RegExp(`<meta-data[^>]*android:name="${name}"[^>]*android:value="([^"]*)"`).exec(manifest);
      if (tag === null) return undefined;
      const placeholder = /^\$\{(\w+)\}$/.exec(tag[1]);
      return placeholder === null ? tag[1] : switches[MANIFEST_PLACEHOLDERS[placeholder[1]]];
    };
    for (const [name, wanted_] of [
      ['google_analytics_adid_collection_enabled', 'false'],
      ['google_analytics_default_allow_ad_personalization_signals', 'false'],
      ['firebase_analytics_collection_enabled', 'true'],
    ]) {
      const got = value(name);
      if (String(got) !== wanted_) {
        found.push(`the generated AndroidManifest.xml carries ${name}=${JSON.stringify(got)}, and ${wanted_} is required`);
      }
    }
    if (/<uses-permission[^>]*android:name="com\.google\.android\.gms\.permission\.AD_ID"/.test(manifest)) {
      found.push('the generated AndroidManifest.xml declares com.google.android.gms.permission.AD_ID, which Play reads as an advertising-id app, and the app removes it at prebuild');
    }
  }

  if (podfileLock !== undefined && /\bAdSupport\b/.test(podfileLock)) {
    found.push('the generated Podfile.lock links AdSupport, and the app links no advertising-identifier framework at all');
  }
  return found;
}

test('the app carries no advertising identifier, from the plugin options and firebase.json', (t) => {
  // The plugin entries the app actually resolves, not a list written here.
  const resolved = resolveConfig(CONFIG.expo, {}).plugins;
  assert.deepEqual(adIdFindings({ plugins: resolved, firebaseJson: FIREBASE_JSON }), []);

  // ... and the planted controls, one source at a time: each is the mutation
  // this walk exists to catch, and each has to come back as a finding.
  const planted = [
    [
      'the iOS option flipped off',
      { plugins: [['@react-native-firebase/analytics', { ios: { withoutAdIdSupport: false } }]] },
      /withoutAdIdSupport/,
    ],
    ['the plugin entry gone', { plugins: FIREBASE_PLUGINS.slice(0, 1) }, /lists no @react-native-firebase\/analytics/],
    [
      'the Android ad-ID switch back on',
      { firebaseJson: { 'react-native': { ...FIREBASE_JSON['react-native'], google_analytics_adid_collection_enabled: true } } },
      /google_analytics_adid_collection_enabled/,
    ],
    [
      'the ad-personalisation switch back on',
      { firebaseJson: { 'react-native': { ...FIREBASE_JSON['react-native'], analytics_default_allow_ad_personalization_signals: true } } },
      /analytics_default_allow_ad_personalization_signals/,
    ],
  ];
  for (const [what, override, expected] of planted) {
    const findings = adIdFindings({ plugins: resolved, firebaseJson: FIREBASE_JSON, ...override });
    assert.equal(findings.length > 0, true, `${what} must be a finding`);
    assert.match(findings.join('\n'), expected, what);
  }

  // The generated half, when a prebuild has been run: the merged manifest
  // resolves the placeholders from firebase.json, so the walk reads it the way
  // Gradle will. On a machine that has never run one, it says so.
  const manifestPath = join(APP_ROOT, 'android', 'app', 'src', 'main', 'AndroidManifest.xml');
  const lockPath = join(APP_ROOT, 'ios', 'Podfile.lock');
  for (const [path, key] of [
    [manifestPath, 'manifest'],
    [lockPath, 'podfileLock'],
  ]) {
    if (!existsSync(path)) {
      t.diagnostic(`${path} is not there: the generated half of this walk was not run (no prebuild on this machine)`);
      continue;
    }
    assert.deepEqual(
      adIdFindings({ plugins: resolved, firebaseJson: FIREBASE_JSON, [key]: readFileSync(path, 'utf8') }),
      [],
      `${path} enables ad-ID collection`,
    );
  }
  // ... and the manifest half still has to be able to fail, so it is run
  // against planted copies of the two things it looks for.
  const plantedManifest = [
    [
      'a manifest with AD_ID declared',
      '<uses-permission android:name="com.google.android.gms.permission.AD_ID"/>',
      /AD_ID/,
    ],
    [
      'a manifest whose ad-ID switch is true',
      '<meta-data android:name="google_analytics_adid_collection_enabled" android:value="true"/>',
      /google_analytics_adid_collection_enabled/,
    ],
    [
      'a manifest whose placeholder resolves to true',
      '<meta-data android:name="google_analytics_default_allow_ad_personalization_signals" android:value="${firebaseJsonPersonalizationEnabled}"/>',
      /google_analytics_default_allow_ad_personalization_signals/,
    ],
  ];
  for (const [what, fragment, expected] of plantedManifest) {
    const findings = adIdFindings({
      plugins: resolved,
      firebaseJson: what.includes('placeholder')
        ? { 'react-native': { ...FIREBASE_JSON['react-native'], analytics_default_allow_ad_personalization_signals: true } }
        : FIREBASE_JSON,
      manifest: `<manifest>${fragment}</manifest>`,
    });
    assert.match(findings.join('\n'), expected, what);
  }
  assert.match(
    adIdFindings({ plugins: resolved, firebaseJson: FIREBASE_JSON, podfileLock: '  - AdSupport (1.0)' }).join('\n'),
    /AdSupport/,
    'a Podfile.lock that links AdSupport is a finding',
  );
  // The Android permission removal is the app's, not the plugin's: Expo's own
  // `android.blockedPermissions` becomes the `tools:node="remove"` entry.
  assert.deepEqual(BLOCKED_PERMISSIONS, ['com.google.android.gms.permission.AD_ID']);
  assert.equal(resolveConfig(CONFIG.expo, {}).android.blockedPermissions.includes(BLOCKED_PERMISSIONS[0]), true);
});

/**
 * Every way the iOS build's framework linkage departs from what the pinned
 * Firebase packages accept, or [].
 *
 * `@react-native-firebase/app`'s SPM installer **raises on a static Podfile** -
 * `firebase_spm.rb`: *"SPM + static linkage is not supported"*, with the stock
 * Expo Podfile named among the static shapes it detects - and the fix it names
 * for this platform is `expo-build-properties` with
 * `ios.useFrameworks: "dynamic"`. So an iOS build needs the entry, that option,
 * and the package the entry names: the plugin is a config plugin, and a build
 * whose `plugins` list names a package `mobile/package.json` does not declare
 * cannot be resolved at all.
 */
function linkageFindings({ plugins, packageJson = MANIFEST }) {
  const names = plugins.map((entry) => (Array.isArray(entry) ? entry[0] : entry));
  const at = names.indexOf('expo-build-properties');
  if (at < 0) {
    return [
      'the resolved config lists no expo-build-properties entry, and the pinned @react-native-firebase/app refuses a static Podfile: its SPM installer raises "SPM + static linkage is not supported" and names ios.useFrameworks "dynamic" as the fix for an Expo build',
    ];
  }
  const found = [];
  const entry = plugins[at];
  const frameworks = Array.isArray(entry) ? entry[1]?.ios?.useFrameworks : undefined;
  if (frameworks !== 'dynamic') {
    found.push(
      `expo-build-properties resolves ios.useFrameworks ${JSON.stringify(frameworks)}, and the pinned Firebase SPM products need "dynamic"`,
    );
  }
  const declared = { ...(packageJson.dependencies ?? {}), ...(packageJson.devDependencies ?? {}) };
  if (!('expo-build-properties' in declared)) {
    found.push(
      'the plugin entry names expo-build-properties and mobile/package.json does not declare it, so a build cannot resolve the plugin',
    );
  }
  return found;
}

test('the iOS build resolves the dynamic frameworks the Firebase SPM products need', () => {
  assert.deepEqual(linkageFindings({ plugins: resolveConfig(CONFIG.expo, {}).plugins }), []);

  // The planted controls, one at a time: each is a state an iOS build would
  // stop on, and each has to come back as a finding.
  for (const [what, override, expected] of [
    ['the entry gone', { plugins: [...FIREBASE_PLUGINS] }, /no expo-build-properties entry/],
    [
      'the linkage static, which the installer refuses',
      { plugins: [['expo-build-properties', { ios: { useFrameworks: 'static' } }]] },
      /ios\.useFrameworks "static"/,
    ],
    [
      'the option absent',
      { plugins: [['expo-build-properties', { android: { minSdkVersion: 24 } }]] },
      /ios\.useFrameworks undefined/,
    ],
    ['the package undeclared', { plugins: [...BUILD_PROPERTIES_PLUGINS], packageJson: {} }, /does not declare it/],
  ]) {
    const findings = linkageFindings(override);
    assert.equal(findings.length > 0, true, `${what} must be a finding`);
    assert.match(findings.join('\n'), expected, what);
  }
});

test('app.config.js carries no credential and no installation either', () => {
  // The same walk as `app.json`'s, over the file a build reads: the plugin
  // entries and the two paths come from the environment, so a value pasted
  // here would be a credential in the tree (D111, D125).
  assert.equal(APP_CONFIG_RAW.includes('AIza'), false, 'no Firebase API key is written in JavaScript');
  assert.equal(APP_CONFIG_RAW.includes('fugaro.account.'), false);
  assert.equal(APP_CONFIG_RAW.includes('fugaro.settings.'), false);
  for (const [path, value] of stringsIn({
    plugins: resolveConfig(CONFIG.expo, {}).plugins,
  })) {
    assert.equal(secretShaped(value), null, `${path} is ${secretShaped(value)}`);
  }
});
