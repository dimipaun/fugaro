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
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { after } from 'node:test';

import { secretShaped } from './accounts/index.js';
import { ID_VARIABLES, NATIVE_CONFIG_VARIABLES, resolveConfig } from '../app.config.js';

const HERE = import.meta.dirname;
const APP_JSON = join(HERE, '..', 'app.json');
const RAW = readFileSync(APP_JSON, 'utf8');
const CONFIG = JSON.parse(RAW);
const MANIFEST = JSON.parse(readFileSync(join(HERE, '..', 'package.json'), 'utf8'));

/**
 * A real file for each native config variable, in a temp directory: the
 * build-time guard (D260) asks whether the path a build resolved names a file,
 * and only the filesystem can answer that. The paths are synthetic and are
 * removed after the suite; neither is the owner's own file, which is never in a
 * checkout.
 */
const NATIVE_DIR = mkdtempSync(join(tmpdir(), 'fugaro-app-config-'));
const NATIVE_FILES = {
  [NATIVE_CONFIG_VARIABLES.android]: join(NATIVE_DIR, 'google-services.json'),
  [NATIVE_CONFIG_VARIABLES.ios]: join(NATIVE_DIR, 'GoogleService-Info.plist'),
};
writeFileSync(NATIVE_FILES[NATIVE_CONFIG_VARIABLES.android], '{}\n');
writeFileSync(NATIVE_FILES[NATIVE_CONFIG_VARIABLES.ios], '<plist/>\n');
after(() => rmSync(NATIVE_DIR, { recursive: true, force: true }));

/** `resolved` without the two native config paths: a comparison about what
 * something else added, not about the files a build carried (D260). */
function withoutNativeConfig(resolved) {
  const ios = { ...resolved.ios };
  const android = { ...resolved.android };
  delete ios.googleServicesFile;
  delete android.googleServicesFile;
  return { ...resolved, ios, android };
}

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
  // The Firebase wiring every resolution carries (P7.1a): the plugin entries
  // `app.config.js` appends to the two `app.json` already lists, and Android's
  // advertising-id permission removal. The entries are spelled out here rather
  // than taken from the file, for the reason above.
  plugins: [
    ...CONFIG.expo.plugins,
    ['expo-build-properties', { ios: { useFrameworks: 'dynamic' } }],
    '@react-native-firebase/app',
    ['@react-native-firebase/analytics', { ios: { withoutAdIdSupport: true } }],
    '@react-native-firebase/crashlytics',
  ],
  ios: { ...CONFIG.expo.ios, infoPlist: { ITSAppUsesNonExemptEncryption: false } },
  android: {
    ...CONFIG.expo.android,
    blockedPermissions: ['com.google.android.gms.permission.AD_ID'],
  },
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
    ...NATIVE_FILES,
  });
  assert.equal(resolved.ios.bundleIdentifier, SAMPLE);
  assert.equal(resolved.android.package, SAMPLE);
  // ... and they are the only difference the IDENTITIES make to the committed
  // file: strip them, and the two native config paths the build carried (D260;
  // the test below is the one about those), and what is left is the static
  // config plus the declaration every resolution carries (`DECLARED`, and the
  // test below owns it), field for field.
  assert.deepEqual(withoutNativeConfig(resolved), {
    ...DECLARED,
    ios: { ...DECLARED.ios, bundleIdentifier: SAMPLE },
    android: { ...DECLARED.android, package: SAMPLE },
  });

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
    withoutNativeConfig(
      resolveConfig(CONFIG.expo, { [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE }),
    ),
    withoutNativeConfig(resolved),
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
    ['a build', { EAS_BUILD: 'true', [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE, ...NATIVE_FILES }],
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

// --- the Firebase config plugin, and the two native files (P7.1a) ------------
//
// The app gains Firebase (the mobile plan's Phase 7): `@react-native-firebase/app`
// puts the SDK in the app, `analytics` and `crashlytics` are the two products it
// carries, and the native config file that names the Firebase project is the
// **owner's** -- registered in the console, delivered to the build as an EAS file
// environment variable, and never committed (D259, §4.3). Two things follow, and
// both are checkable from the tree alone:
//
//   * **the two paths are ignored.** `mobile/google-services.json` (Android) and
//     `mobile/GoogleService-Info.plist` (iOS) are where the console's own output
//     goes and where the plugin looks for them, so they are the two paths a
//     later `git add -A` would commit. `mobile/.gitignore` names them; this test
//     reads that through `git check-ignore` rather than by reading the file, so
//     it asserts the rule git actually applies -- and proves the question can
//     answer "no" with a path that is not ignored.
//   * **the resolved config wires the plugin.** `app.config.js` is the one file
//     the three packages' config plugins are registered in, and a resolution is
//     what a build reads. What the plugin *options* mean -- the ad-ID switches,
//     the iOS variant -- is the build-time walk's (§7.4, P7.1b); what is pinned
//     here is that the entries exist, in both modes, once each, and that the
//     plugins `app.json` already lists survive.

/** The two native config files, by the names the Firebase console writes. */
const NATIVE_CONFIG_FILES = Object.freeze(['google-services.json', 'GoogleService-Info.plist']);

/** The three packages, in the order the config registers them. */
const FIREBASE_PACKAGES = Object.freeze([
  '@react-native-firebase/app',
  '@react-native-firebase/analytics',
  '@react-native-firebase/crashlytics',
]);

/** The plugin's name, whether the entry is a bare string or a `[name, options]` pair. */
function pluginName(entry) {
  return Array.isArray(entry) ? entry[0] : entry;
}

/** The repository root, asked of git rather than walked up to. */
function repositoryRoot() {
  return execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim();
}

/** Is `path` (from the repository root) matched by an ignore rule? */
function ignoredByGit(path, root) {
  try {
    execFileSync('git', ['check-ignore', '-q', '--', path], { cwd: root, stdio: 'pipe' });
    return true;
  } catch {
    return false;
  }
}

test('the two native Firebase config files are untracked and ignored', () => {
  const root = repositoryRoot();
  // The control: the predicate below answers "no" for a file that is in the tree
  // and not ignored. Without it, an `ignoredByGit` that always returned true
  // would satisfy the assertions under it.
  assert.equal(ignoredByGit('mobile/app.json', root), false, 'the control: a tracked file is not ignored');

  for (const name of NATIVE_CONFIG_FILES) {
    const path = join('mobile', name);
    // Not tracked. `--error-unmatch` is the question "is this path in the index",
    // asked of git: an untracked file is what a checkout without the owner's
    // console output looks like, and a tracked one is the failure this rule is
    // about (a committed project id, a committed api key).
    assert.throws(
      () => execFileSync('git', ['ls-files', '--error-unmatch', '--', path], { cwd: root, stdio: 'pipe' }),
      `${path} is tracked: the native config file is the owner's, and it is never committed`,
    );
    // ... and ignored, so the next `git add -A` does not pick it up either.
    assert.equal(ignoredByGit(path, root), true, `${path} is not matched by mobile/.gitignore`);
  }
});

test('a resolved config registers the three @react-native-firebase plugins', () => {
  const SAMPLE = 'com.example.sample';
  const staticPlugins = (CONFIG.expo.plugins ?? []).map(pluginName);
  for (const [what, env] of [
    ['a build', { EAS_BUILD: 'true', [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE, ...NATIVE_FILES }],
    ['a dev/lint run', {}],
  ]) {
    const { plugins } = resolveConfig(CONFIG.expo, env);
    const names = plugins.map(pluginName);
    for (const pkg of FIREBASE_PACKAGES) {
      assert.equal(names.includes(pkg), true, `${what} does not register ${pkg}`);
    }
    // ... once each: `withPlugins` runs a plugin twice for two entries, and the
    // second run is a second application of the same prebuild edit.
    assert.equal(new Set(names).size, names.length, `${what} registers a plugin twice`);
    // ... and the plugins `app.json` already lists are still there: this file
    // appends, and an append that replaced the list would drop the app's own
    // secure-store and splash configuration.
    for (const name of staticPlugins) {
      assert.equal(names.includes(name), true, `${what} dropped the ${name} plugin app.json lists`);
    }
  }
});

// --- the two native config files, and the advertising-id settings (P7.1a) ----
//
// The tests above are about the *entries*: the plugins are registered and the
// two paths are ignored. These are about the **values** those entries carry,
// which is where the plan's structural promises live: the app's builds point at
// the owner's Firebase project through a path the environment hands over, and
// the app links no advertising-id support at all. The build-time walk of §7.4
// -- the generated `AndroidManifest.xml`, the `Podfile.lock`, the planted
// controls -- is P7.1b's and is not here.

test('the native config paths come from the environment, and only when set', () => {
  const SAMPLE = 'com.example.sample';
  // Two real files with synthetic names: this file holds no path the owner's
  // machine has, and the value is copied through unread anyway.
  const ANDROID_PATH = NATIVE_FILES[NATIVE_CONFIG_VARIABLES.android];
  const IOS_PATH = NATIVE_FILES[NATIVE_CONFIG_VARIABLES.ios];
  const build = {
    EAS_BUILD: 'true',
    [ID_VARIABLES.ios]: SAMPLE,
    [ID_VARIABLES.android]: SAMPLE,
    ...NATIVE_FILES,
  };
  const resolved = resolveConfig(CONFIG.expo, build);
  assert.equal(resolved.android.googleServicesFile, ANDROID_PATH);
  // ... the iOS file goes in `ios.googleServicesFile`, which is the field the
  // plugin reads -- not into `infoPlist`, where a path would be a plist value
  // nothing looks at.
  assert.equal(resolved.ios.googleServicesFile, IOS_PATH);
  assert.equal('googleServicesFile' in resolved.ios.infoPlist, false);
  // ... and the two are the only fields the two variables add: strip them --
  // and the identities, which the same test above is the one about -- and the
  // resolution is the one a build with no files makes, which is `DECLARED`.
  const ios = { ...resolved.ios };
  const android = { ...resolved.android };
  for (const field of ['googleServicesFile', 'bundleIdentifier']) delete ios[field];
  for (const field of ['googleServicesFile', 'package']) delete android[field];
  assert.deepEqual({ ...resolved, ios, android }, DECLARED);

  // Unset, and no switch: the field is **absent**, and nothing throws -- a
  // developer's `expo start`, this suite, and the local `eas-cli` resolution
  // all run on a machine that has neither file. Mutation: make the field
  // unconditional (or default it to a path) and the assertions below find one.
  for (const env of [{}, { [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE }]) {
    for (const platform of ['ios', 'android']) {
      assert.equal('googleServicesFile' in resolveConfig(CONFIG.expo, env)[platform], false, platform);
    }
  }
  // ... and an **empty** value is the same as an unset one in the dev/lint
  // state too: an EAS variable set to nothing is a variable nobody set, so the
  // field stays absent rather than becoming `''`. Mutation: test `=== undefined`
  // alone in `nativeConfigFile` and the assertions below find
  // `googleServicesFile: ''`.
  for (const env of [
    { [NATIVE_CONFIG_VARIABLES.android]: '', [NATIVE_CONFIG_VARIABLES.ios]: '' },
    { [ID_VARIABLES.ios]: SAMPLE, [ID_VARIABLES.android]: SAMPLE, [NATIVE_CONFIG_VARIABLES.android]: '', [NATIVE_CONFIG_VARIABLES.ios]: '' },
  ]) {
    for (const platform of ['ios', 'android']) {
      assert.equal('googleServicesFile' in resolveConfig(CONFIG.expo, env)[platform], false, `${platform} carries no empty path`);
    }
  }
  // ... while in a build the same unset-or-empty state is the stop D260 asks
  // for, not a resolution: prebuild would refuse a config whose
  // `googleServicesFile` is unset or `''`, from the plugin's own code and after
  // the upload, and this is the earlier stop on the same fact. That refusal is
  // `src/native-config-guard.test.js`'s own; what is pinned here is that this
  // file hands the guard nothing to pass on either value.
  for (const unset of [
    { [NATIVE_CONFIG_VARIABLES.android]: undefined, [NATIVE_CONFIG_VARIABLES.ios]: undefined },
    { [NATIVE_CONFIG_VARIABLES.android]: '', [NATIVE_CONFIG_VARIABLES.ios]: '' },
  ]) {
    assert.throws(() => resolveConfig(CONFIG.expo, { ...build, ...unset }));
  }
});

test('a resolved config links no advertising-id support, on both platforms', () => {
  const plugins = resolveConfig(CONFIG.expo, {}).plugins;
  const optionsOf = (name) => plugins.find((entry) => pluginName(entry) === name)?.[1];

  // iOS: the Analytics entry selects the variant with no ad-ID support, which
  // is the option the pinned plugin reads to write the Podfile flag -- the
  // whole of the iOS half of the ban, since the pod then links
  // `FirebaseAnalyticsCore` rather than the identity subspec. Mutation: drop
  // the option and this line goes red; it is also the one option §4.2's table
  // asked P7.1 to resolve against the SDK's own source rather than name from
  // memory.
  assert.deepEqual(optionsOf('@react-native-firebase/analytics'), { ios: { withoutAdIdSupport: true } });
  // Android: the permission Play reads as "this app uses the advertising id" is
  // **removed** from the merged manifest, through Expo's own
  // `android.blockedPermissions` -- which is the route, because the pinned
  // `@react-native-firebase/app` plugin has no Android option at all (§4.2's
  // other VERIFY item).
  assert.deepEqual(resolveConfig(CONFIG.expo, {}).android.blockedPermissions, [
    'com.google.android.gms.permission.AD_ID',
  ]);
  // ... once, and without dropping a permission a later base config blocked:
  // the list is appended to, not replaced. Mutation: assign the array instead
  // and the first entry is gone.
  const base = { ...CONFIG.expo, android: { ...CONFIG.expo.android, blockedPermissions: ['android.permission.RECORD_AUDIO'] } };
  assert.deepEqual(resolveConfig(base, {}).android.blockedPermissions, [
    'android.permission.RECORD_AUDIO',
    'com.google.android.gms.permission.AD_ID',
  ]);
  // ... and the iOS dynamic-frameworks option is present, because the pinned
  // `@react-native-firebase/app` plugin's own source says the Firebase Apple
  // SDK is Swift-Package-Manager-only and a project built from it must use
  // dynamic frameworks. It is the one dependency this task added beyond the
  // three packages, and this is what it is for.
  assert.deepEqual(optionsOf('expo-build-properties'), { ios: { useFrameworks: 'dynamic' } });
});

test('the Android switches firebase.json carries are the plan’s four', () => {
  // The Android collection and ad switches are read by the SDK at **build
  // time**, from `firebase.json` at the app root, under `react-native` -- the
  // Analytics library's own `build.gradle` turns each into a manifest
  // placeholder -- so this file, and not `app.config.js`, is where they live
  // (§4.2). The values are D255's answer: the two collection switches are ON
  // and the two ad switches are OFF. `src/app-config.ids.test.js`'s walk does
  // not read this file, and §7.4's build-time walk (P7.1b) reads it alongside
  // the generated native project; what is pinned here is the file itself.
  //
  // The four key names are the ones the SDK's build step reads, and they are
  // not uniformly prefixed: `analytics/android/build.gradle:113` reads
  // `analytics_default_allow_ad_personalization_signals`, while
  // `:95` reads `google_analytics_adid_collection_enabled`. The prefixed
  // `google_analytics_default_allow_ad_personalization_signals` is the
  // **AndroidManifest** meta-data name (`analytics/android/src/main/
  // AndroidManifest.xml:18`), not the `firebase.json` key, so writing it here
  // would leave the switch unreachable and ad personalisation on.
  const firebase = JSON.parse(readFileSync(join(HERE, '..', 'firebase.json'), 'utf8'));
  assert.deepEqual(firebase, {
    'react-native': {
      analytics_auto_collection_enabled: true,
      crashlytics_auto_collection_enabled: true,
      google_analytics_adid_collection_enabled: false,
      analytics_default_allow_ad_personalization_signals: false,
    },
  });
});
