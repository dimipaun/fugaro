// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The dynamic config's tests (mobile plan, Task P4.4; D120, D125; Check 89).
//
// `mobile/app.config.js` is the one place the app's two release identities come
// from -- the iOS bundle identifier and the Android package name are read from
// EAS environment variables at build time, because P3.1's boundary test
// (`src/app-config.test.js`) asserts that `app.json` carries no release
// identity: the app is its own artifact with its own release path (D125), and
// the identity is the owner's. That makes this file the second half of that
// fence, and it is written about the two failure modes a config file has:
//
//   * **a build that shipped without an identity, or with a typo'd one.** A
//     missing or malformed variable under `EAS_BUILD` (or an explicit
//     `FUGARO_REQUIRE_IDS=1`) is a thrown error naming the variable and the
//     fix -- never a config that resolves quietly. Each store's own rule is
//     the fence: the Android package is a Java package name (a letter starts
//     every segment, no hyphen), the iOS identifier is Apple's (no
//     underscore). The Android package is the one that cannot be corrected
//     later -- it is fixed the moment the app exists on Play -- so a value the
//     Play Console would refuse is refused here, while the fix is still free.
//   * **a value that leaked.** The messages describe the shape a value must
//     have and **never repeat the value**, and no environment value other than
//     the two identities reaches the resolved config -- the one thing a
//     mistyped variable might hold is something that must not be printed into
//     a build log or baked into an artifact.
//
// And the two cases that keep the file usable:
//
//   * **a value that is set is used.** `eas-cli` resolves this file **locally**
//     before it uploads a build, and `EAS_BUILD` is set by the build *worker*,
//     not by the CLI -- so the CLI's own resolution runs in the dev/lint state,
//     and it needs `ios.bundleIdentifier`/`android.package` to configure
//     credentials and to submit. A variable that is set is therefore read and
//     validated in every state; the switches add the *requirement*, not the
//     read.
//   * **nothing is required without a switch** -- a developer's `expo start`,
//     and `npm test` itself -- so the config resolves without a store account,
//     and a static identity planted in `app.json` is still dropped.
//
// And one thing this file adds that is **not** an environment value: the
// export-compliance declaration the owner ruled on 2026-10-20
// (`ios.infoPlist.ITSAppUsesNonExemptEncryption: false`, see `app.config.js`
// and `mobile/README.md`). It is a constant, not a third variable -- the same
// answer for every copy of the app -- and `resolveConfig` merges it in **both**
// states, a build and a dev/lint run alike, because it is a fact about the app
// rather than about the environment. So "the static config, unchanged" below
// means `app.json`'s `expo` object plus that one key: the constant `DECLARED`
// is what a resolution is compared with, and it is spelled out rather than
// built by the same merge the file under test does. The key's own tests are in
// `src/app-config.test.js`, beside the rest of the boundary between `app.json`
// and the config the build reads.
//
// And the fence around the file itself: **the identity is committed nowhere**.
// Every fixture here is the fictional `com.example.sample`, and the last test
// walks every tracked file under `mobile/` and `docs/mobile/` for the owner's
// own value, which belongs in the EAS project -- not in a test, a comment or a
// doc. The file that does tell the owner how to set it is `mobile/README.md`,
// and it is **inside** that walk: it writes `<the reverse-DNS identity>` where
// the value goes, and the pin checks that it does. Nothing under either root
// carries the value, and there is no exception in the tree today.
//
// Every refusal below is mutation-proven the same way the tree's other guards
// are: the property is stated as a table, the mutation that breaks it is named
// in the test's own comment, and the mutated run fails. The scans carry
// positive controls (a planted value the pattern must flag), so a green run
// means the walk can fail rather than meaning the walk found nothing.

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { after } from 'node:test';

import appConfig, {
  ID_PATTERNS,
  ID_VARIABLES,
  NATIVE_CONFIG_VARIABLES,
  isIdentifier,
  requiresIdentifiers,
  resolveConfig,
} from '../app.config.js';

const APP_JSON = JSON.parse(readFileSync(new URL('../app.json', import.meta.url), 'utf8'));
const EXPO = APP_JSON.expo;
const SOURCE = readFileSync(new URL('../app.config.js', import.meta.url), 'utf8');
const GUARD_SOURCE = readFileSync(new URL('../native-config-guard.js', import.meta.url), 'utf8');

/**
 * A real file for each native config variable, in a temp directory: the
 * build-time guard (D260) asks whether the path a build resolved names a file,
 * and only the filesystem can answer that. The paths are synthetic, they are
 * removed after the suite, and neither is the owner's own file -- which is
 * never in a checkout (see `src/app-config.test.js`).
 */
const NATIVE_DIR = mkdtempSync(join(tmpdir(), 'fugaro-app-config-ids-'));
const NATIVE_FILES = {
  [NATIVE_CONFIG_VARIABLES.android]: join(NATIVE_DIR, 'google-services.json'),
  [NATIVE_CONFIG_VARIABLES.ios]: join(NATIVE_DIR, 'GoogleService-Info.plist'),
};
writeFileSync(NATIVE_FILES[NATIVE_CONFIG_VARIABLES.android], '{}\n');
writeFileSync(NATIVE_FILES[NATIVE_CONFIG_VARIABLES.ios], '<plist/>\n');
after(() => rmSync(NATIVE_DIR, { recursive: true, force: true }));

/**
 * A **fictional** sample identity: one reverse-DNS name for both stores, of the
 * shape the two patterns accept. It is deliberately nobody's real value -- the
 * owner's is set in the EAS project (`eas env:set`) and is committed nowhere,
 * which the repository-wide pin at the bottom of this file enforces. A test
 * fixture that spells a real identity out is that identity written into the
 * tree by the back door.
 */
const IOS = 'com.example.sample';
const ANDROID = 'com.example.sample';

/**
 * The static config as **a resolution** carries it: `app.json`'s `expo` object
 * plus the one thing `app.config.js` merges into it in every state -- the
 * export-compliance declaration (the owner's ruling of 2026-10-20). Written
 * out field for field rather than by calling the file's own merge, so the
 * expectation is a statement about what the config should carry and not a
 * second copy of the code that produces it. `app.json`'s `ios` section sets no
 * `infoPlist` of its own: the merge has this key to add and no key to keep, and
 * the "a key that is already there survives the merge" case is
 * `src/app-config.test.js`'s (it needs a config that sets one).
 */
const DECLARED = {
  ...EXPO,
  // The Firebase wiring is the second thing a resolution carries in every state
  // (P7.1a): `app.config.js` appends the three `@react-native-firebase` plugins
  // and `expo-build-properties` to the list `app.json` already has, and removes
  // Android's advertising-id permission. Spelled out, like the declaration
  // below it, so this stays a statement about the config rather than a second
  // copy of the code that produces it.
  plugins: [
    ...EXPO.plugins,
    ['expo-build-properties', { ios: { useFrameworks: 'dynamic' } }],
    '@react-native-firebase/app',
    ['@react-native-firebase/analytics', { ios: { withoutAdIdSupport: true } }],
    '@react-native-firebase/crashlytics',
  ],
  ios: { ...EXPO.ios, infoPlist: { ITSAppUsesNonExemptEncryption: false } },
  android: { ...EXPO.android, blockedPermissions: ['com.google.android.gms.permission.AD_ID'] },
};

/** An environment as EAS's build servers present it to `app.config.js`. Both
 * native config variables name a real file: a build whose resolved config
 * carries no readable one is the stop D260 asks for (`src/native-config-guard.test.js`),
 * and every build fixture here is a build the guard lets through. */
const build = (extra = {}) => ({
  EAS_BUILD: 'true',
  [ID_VARIABLES.ios]: IOS,
  [ID_VARIABLES.android]: ANDROID,
  ...NATIVE_FILES,
  ...extra,
});

/** `resolved` without the two native config paths, so a comparison is about
 * what something ELSE added rather than about the files a build carried. */
function withoutNativeConfig(resolved) {
  const ios = { ...resolved.ios };
  const android = { ...resolved.android };
  delete ios.googleServicesFile;
  delete android.googleServicesFile;
  return { ...resolved, ios, android };
}

/** `resolved` without the two identity fields, so what is left can be compared
 * with `DECLARED` (the static config as a resolution carries it: the identities
 * and the two native config paths are the only fields this strips -- the
 * declaration is the other addition, and it is expected, so it stays in). */
function withoutIdentities(resolved) {
  const ios = { ...resolved.ios };
  const android = { ...resolved.android };
  delete ios.bundleIdentifier;
  delete android.package;
  return { ...resolved, ios, android };
}

/** The message a call refuses with, asserting that it refuses at all
 * (`assert.throws` returns nothing, so the error is captured here). */
function refusal(fn) {
  let thrown;
  assert.throws(fn, (error) => {
    thrown = error;
    return true;
  });
  return thrown.message;
}

/** The file's code, without its comments (this file's own header names the
 * variables, and a mention in prose is not a read). */
const code = (text) => text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

/** The environment-variable names a source file writes as string literals. */
function namedIn(source) {
  return [...code(source).matchAll(/['"]([A-Z][A-Z0-9_]{2,})['"]/g)].map((match) => match[1]).sort();
}

test('the two variables are the ones this task named, and a rename is deliberate', () => {
  assert.deepEqual({ ...ID_VARIABLES }, {
    ios: 'FUGARO_IOS_BUNDLE_ID',
    android: 'FUGARO_ANDROID_PACKAGE',
  });
});

test('each pattern accepts and refuses its own store’s shapes, and not the other’s', () => {
  // Every shape neither store accepts: one segment, an empty segment, a space,
  // a newline an env var picked up on the way in, a stray path character, a
  // port. Mutation: drop the `+` quantifier or the segment class and
  // 'com.example' starts passing.
  const refusedByBoth = [
    'comexample', // one segment: not a reverse-DNS name
    'com..example', // an empty segment
    '.com.example',
    'com.example.',
    'com example.sample', // a space
    'com.example.sample\n', // a newline an env var picked up on the way in
    'com/example.sample',
    '',
    'com.example.sample:443',
  ];
  for (const bad of refusedByBoth) {
    assert.equal(isIdentifier(bad, 'ios'), false, JSON.stringify(bad));
    assert.equal(isIdentifier(bad, 'android'), false, JSON.stringify(bad));
  }

  // Neither store requires lower case: an identifier in either case is a valid
  // one, and refusing it here would be this file inventing a rule neither
  // store has. Mutation: put the lower-case-only class back and every line
  // here goes red.
  for (const good of ['Com.Example.Sample', 'COM.EXAMPLE.SAMPLE', 'com.Example.sample']) {
    assert.equal(isIdentifier(good, 'ios'), true, good);
    assert.equal(isIdentifier(good, 'android'), true, good);
  }

  // iOS: Apple allows letters, digits, hyphens and periods, and **no
  // underscore**; a segment may start with a digit.
  for (const good of [IOS, 'a.b', 'x1.y2', 'com.example-sample.io', 'com.1sample']) {
    assert.equal(isIdentifier(good, 'ios'), true, good);
  }
  for (const bad of ['com.example_sample.io', 'com._sample.io']) {
    assert.equal(isIdentifier(bad, 'ios'), false, `${JSON.stringify(bad)}: Apple allows no underscore`);
  }

  // Android: the package is an application id -- a letter starts every
  // segment, an underscore is a Java identifier character, and a hyphen is
  // not. A digit-starting segment is what Play refuses, and this package can
  // never be changed once the app exists on Play, so it is refused here.
  for (const good of [ANDROID, 'a.b', 'x1.y2', 'com.example_sample.io']) {
    assert.equal(isIdentifier(good, 'android'), true, good);
  }
  for (const bad of [
    'com.1sample', // Play refuses a digit-starting segment ...
    '1com.example', // ... in the first segment as well as the last
    'com._sample.io', // a segment has to start with a letter
    'com.example-sample.io', // a hyphen is not a Java identifier character
  ]) {
    assert.equal(isIdentifier(bad, 'android'), false, JSON.stringify(bad));
  }

  // The two fences are different rules, not one rule with an exception: each
  // value below is the other platform's shape.
  assert.equal(ID_PATTERNS.android.source !== ID_PATTERNS.ios.source, true);
  assert.equal(isIdentifier('com.example-sample.io', 'android'), false);
  assert.equal(isIdentifier('com.example_sample.io', 'ios'), false);
  assert.equal(isIdentifier(IOS, 'windows'), false, 'an unknown platform is not an identity');
  assert.equal(isIdentifier(7, 'ios'), false, 'a non-string is not an identity');
});

test('a platform name inherited from Object.prototype is not an identity', () => {
  // These are not keys of ID_PATTERNS, but they ARE properties of every object
  // through `Object.prototype`: a lookup that only asked `pattern !== undefined`
  // read `Object`'s own constructor (or function) off the prototype and called
  // `.test` on it, so `isIdentifier('a.b', 'constructor')` threw a TypeError out
  // of a predicate whose whole job is to answer yes or no. Mutation: put the
  // prototype-chain lookup back and every line below throws instead of passing.
  for (const platform of ['constructor', 'toString', '__proto__', 'hasOwnProperty', 'valueOf']) {
    assert.equal(Object.hasOwn(ID_PATTERNS, platform), false, `${platform} is not a pattern this file has`);
    assert.equal(isIdentifier('com.example.sample', platform), false, platform);
    assert.equal(isIdentifier(undefined, platform), false, platform);
  }
  // ... and the two platforms it does have still answer.
  assert.equal(isIdentifier(IOS, 'ios'), true);
  assert.equal(isIdentifier(ANDROID, 'android'), true);
});

test('an EAS build, or the explicit local switch, is what asks for the identities', () => {
  // Mutation: `requiresIdentifiers` returning true unconditionally makes the
  // last four lines fail, which is the local-mode counter-case the whole file
  // depends on.
  assert.equal(requiresIdentifiers({ EAS_BUILD: 'true' }), true);
  assert.equal(requiresIdentifiers({ EAS_BUILD: '' }), false, 'an empty EAS_BUILD is not a build');
  assert.equal(requiresIdentifiers({ FUGARO_REQUIRE_IDS: '1' }), true);
  assert.equal(requiresIdentifiers({ FUGARO_REQUIRE_IDS: '0' }), false);
  assert.equal(requiresIdentifiers({ FUGARO_REQUIRE_IDS: 'true' }), false, 'the switch is `1`, exactly');
  assert.equal(requiresIdentifiers({}), false);
});

for (const [platform, variable] of Object.entries(ID_VARIABLES)) {
  const other = platform === 'ios' ? ID_VARIABLES.android : ID_VARIABLES.ios;

  test(`a build refuses a missing ${variable}, naming it and the fix`, () => {
    const env = build();
    delete env[variable];
    const message = refusal(() => resolveConfig(EXPO, env));
    // The variable's name is the FIRST thing the message says: a reader of a
    // build log has two variables to choose between, and "which one is
    // missing" is the whole answer. Mutation: a message that names the
    // variable only in the command it suggests (or names neither) is red here.
    assert.equal(message.startsWith(variable), true, `the message begins by naming ${variable}: ${message}`);
    assert.equal(message.includes(other), false, 'and not the other one');
    // The current EAS CLI command: `eas env:create` and `eas env:update` are
    // hidden and print a deprecation warning, and a message that sends the
    // owner to a command their CLI tells them not to use is a message that
    // wastes the stop it just made. Mutation: name the old command here.
    assert.match(message, /eas env:set/, 'and says where to set it, with the command the CLI has');
    assert.equal(/eas env:(create|update)\b/.test(message), false, 'and not the deprecated one');
  });

  test(`a build refuses a malformed ${variable}, and the message never repeats the value`, () => {
    // Each of these is a value somebody could paste: a display name, a
    // one-segment name, a name in the *other* store's shape, and a
    // secret-looking string. Mutation: echo the value into the message and the
    // last assertion goes red -- which is the point, because a build log is the
    // last place a mistyped variable should print.
    // The value the OTHER store accepts and this one does not: an underscore
    // for iOS, a hyphen for Android.
    const otherShape = platform === 'ios' ? 'com.example_sample.io' : 'com.example-sample.io';
    for (const bad of ['Fugaro', 'devfugaro', 'not a package name', otherShape, 'sk-notAKeyButItLooksLikeOne123456']) {
      const env = build({ [variable]: bad });
      const message = refusal(() => resolveConfig(EXPO, env));
      assert.match(message, new RegExp(variable), 'the message names the variable');
      assert.match(message, /reverse-DNS/, 'and the shape the value must have');
      assert.equal(message.includes(bad), false, `the message repeats ${JSON.stringify(bad)}`);
      assert.match(message, /eas env:set/, 'and names the command that fixes it');
    }
    // The shape sentence is this platform's rule, not the shared half of it:
    // an owner told "no hyphen" while fixing an iOS identifier is looking for
    // a rule their store does not have. Mutation: one SHAPE for both and one
    // of these two lines goes red.
    const wrongRule = platform === 'ios' ? /no hyphen/ : /no underscore/;
    assert.equal(wrongRule.test(refusal(() => resolveConfig(EXPO, build({ [variable]: 'Fugaro' })))), false);
  });
}

test('a build with both variables resolves them onto the static config, and changes nothing else', () => {
  const resolved = resolveConfig(EXPO, build());
  assert.equal(resolved.ios.bundleIdentifier, IOS);
  assert.equal(resolved.android.package, ANDROID);
  // The identities and the two native config paths are the only fields ADDED
  // here: every other field of the static config survives untouched
  // (`supportsTablet`, `edgeToEdgeEnabled`, the name, the slug and the version
  // included), and what a resolution carries besides them is the
  // export-compliance declaration `DECLARED` names.
  assert.deepEqual(withoutIdentities(withoutNativeConfig(resolved)), DECLARED);
  // ... and the static object itself is not mutated by resolving it.
  assert.equal('bundleIdentifier' in EXPO.ios, false);
  assert.equal('package' in EXPO.android, false);
});

test('a dev/lint run requires nothing, and gets the config unchanged with nothing set', () => {
  // `expo start` and `npm test` have neither switch set, and they must keep
  // working without a store account: with the variables unset the config is the
  // static one, field for field -- plus the declaration, which is the app's own
  // and not the environment's (`DECLARED`). Mutation: require them in local
  // mode (or add an empty identity) and this line goes red.
  assert.deepEqual(resolveConfig(EXPO, {}), DECLARED);
  assert.deepEqual(resolveConfig(EXPO, { FUGARO_REQUIRE_IDS: '0' }), DECLARED);
  assert.deepEqual(resolveConfig(EXPO, { EAS_BUILD: '' }), DECLARED);
  // ... and a value that got into the static config by hand is REMOVED here
  // rather than passed through, so `app.json` cannot quietly become a second
  // site for the identities. Mutation: return `config` as-is and this fails.
  const planted = { ...EXPO, ios: { ...EXPO.ios, bundleIdentifier: 'com.example.app' }, android: { ...EXPO.android, package: 'com.example.app' } };
  const resolved = resolveConfig(planted, {});
  assert.equal('bundleIdentifier' in resolved.ios, false);
  assert.equal('package' in resolved.android, false);
  assert.equal(planted.ios.bundleIdentifier, 'com.example.app', 'the static object is not mutated either');
});

test('a local eas-cli run gets the identities from the variables, and a typo is still refused', () => {
  // This is the state `eas-cli` resolves the file in: `EAS_BUILD` is set by the
  // build *worker*, not by the CLI, and the CLI needs the two identifiers
  // locally to configure credentials and to submit. A file that dropped them
  // whenever `EAS_BUILD` was unset made `eas build --profile internal` fail
  // before a build was ever uploaded. Mutation: return the sections without
  // reading the variables in this state and the first two lines fail.
  const local = resolveConfig(EXPO, { [ID_VARIABLES.ios]: IOS, [ID_VARIABLES.android]: ANDROID });
  assert.equal(local.ios.bundleIdentifier, IOS);
  assert.equal(local.android.package, ANDROID);
  assert.deepEqual(withoutIdentities(local), DECLARED, 'and nothing else moves');
  // A value that IS set has to be one in this state too -- a mistyped export
  // must not be baked into a dev build either. Mutation: skip validation when
  // the switches are unset and this throws nothing.
  assert.match(refusal(() => resolveConfig(EXPO, { [ID_VARIABLES.ios]: 'com.example_sample.io' })), /FUGARO_IOS_BUNDLE_ID/);
  // ... and one variable set does not drag the other one in.
  const onlyIos = resolveConfig(EXPO, { [ID_VARIABLES.ios]: IOS });
  assert.equal(onlyIos.ios.bundleIdentifier, IOS);
  assert.equal('package' in onlyIos.android, false);
  // The static identity is dropped here as well: the variable is the site.
  const planted = { ...EXPO, ios: { ...EXPO.ios, bundleIdentifier: 'com.example.app' } };
  assert.equal(resolveConfig(planted, { [ID_VARIABLES.ios]: IOS }).ios.bundleIdentifier, IOS);
});

test('no environment value but the two identities reaches the config', () => {
  // Mutation: spread `env` into `extra` (or read a second variable) and this
  // goes red. The secret-shaped names below are values, not credentials: they
  // exist to prove that a variable this file does not know is a variable it
  // does not carry.
  const polluted = build({
    FIREBASE_TOKEN: 'not-a-real-token',
    FUGARO_SETTINGS_KEY: 'not-a-real-key',
    [ID_VARIABLES.ios + '_EXTRA']: 'com.example.extra',
    NODE_ENV: 'production',
  });
  assert.deepEqual(resolveConfig(EXPO, polluted), resolveConfig(EXPO, build()));
});

test('the config resolution names exactly the variables it reads, as literals', () => {
  // The positive control: a planted third variable is flagged, so the walk
  // below is known to be able to fail.
  assert.deepEqual(namedIn("const v = process.env['FIREBASE_TOKEN'];\n"), ['FIREBASE_TOKEN']);
  // ... and the two files the resolution reads the environment through --
  // `app.config.js` and the guard module it imports -- together name the two
  // identities, the **two native config file** variables (P7.1a: the path of
  // `google-services.json` and of `GoogleService-Info.plist` on a build
  // worker), and the platform EAS is building for (P7.1b's guard), and nothing
  // else. A variable read in *either* file is a build dependency, so leaving
  // the guard outside the walk would make its read invisible to the pin whose
  // whole purpose is to make each one deliberate. Mutation: add a
  // `process.env['FIREBASE_TOKEN']` read to `native-config-guard.js` and this
  // goes red naming it.
  assert.deepEqual([...namedIn(SOURCE), ...namedIn(GUARD_SOURCE)].sort(), [
    'EAS_BUILD_PLATFORM',
    'FUGARO_ANDROID_PACKAGE',
    'FUGARO_IOS_BUNDLE_ID',
    'GOOGLE_SERVICES_JSON',
    'GOOGLE_SERVICE_INFO_PLIST',
  ]);
  // The two switches are read off `env` by name and are not values of their
  // own; the point above still reads them out of the source, so the property
  // is "the only names written as literals are the identities".
  assert.match(code(SOURCE), /requiresIdentifiers/);
});

test('the switches add the requirement, and only the requirement', () => {
  // The whole file in three lines: the same variables, the switch apart. With
  // the variables unset the switch is the difference between a refusal and a
  // config without an identity -- and with them set, both states resolve the
  // same identities, which is what the local eas-cli phase depends on.
  const asked = resolveConfig(EXPO, { EAS_BUILD: 'true', ...build() });
  const local = resolveConfig(EXPO, {
    [ID_VARIABLES.ios]: IOS,
    [ID_VARIABLES.android]: ANDROID,
    ...NATIVE_FILES,
  });
  assert.deepEqual(asked, local);
  assert.deepEqual(resolveConfig(EXPO, {}), DECLARED, 'no switch, no variables: nothing is required');
  assert.match(refusal(() => resolveConfig(EXPO, { EAS_BUILD: 'true' })), /FUGARO_IOS_BUNDLE_ID/);
});

test("Expo's entry reads process.env, and refuses a call with no static config", () => {
  const saved = {};
  const keys = [
    'EAS_BUILD',
    'FUGARO_REQUIRE_IDS',
    ID_VARIABLES.ios,
    ID_VARIABLES.android,
    NATIVE_CONFIG_VARIABLES.ios,
    NATIVE_CONFIG_VARIABLES.android,
  ];
  for (const key of keys) saved[key] = Object.prototype.hasOwnProperty.call(process.env, key) ? process.env[key] : undefined;
  try {
    for (const key of keys) delete process.env[key];
    // Local mode: the entry resolves over the real `process.env`, and adds
    // nothing but the app's own declaration. Mutation: `appConfig` passing a
    // frozen empty env, or ignoring `process.env`, and the build-mode assertion
    // below cannot go green.
    assert.deepEqual(appConfig({ config: EXPO }), DECLARED);
    process.env.EAS_BUILD = 'true';
    process.env[ID_VARIABLES.ios] = IOS;
    process.env[ID_VARIABLES.android] = ANDROID;
    // ... and the two native config paths name real files, because a build
    // whose resolved config carries none is the stop D260 asks for.
    process.env[NATIVE_CONFIG_VARIABLES.ios] = NATIVE_FILES[NATIVE_CONFIG_VARIABLES.ios];
    process.env[NATIVE_CONFIG_VARIABLES.android] = NATIVE_FILES[NATIVE_CONFIG_VARIABLES.android];
    assert.equal(appConfig({ config: EXPO }).ios.bundleIdentifier, IOS);
    assert.equal(appConfig({ config: EXPO }).android.package, ANDROID);
  } finally {
    for (const key of keys) {
      if (saved[key] === undefined) delete process.env[key];
      else process.env[key] = saved[key];
    }
  }
  // A caller that is not Expo gets a message about the config, not a config
  // built from an empty base: a nameless build is a bug nobody can read.
  for (const bad of [undefined, null, 'expo', []]) {
    const message = refusal(() => resolveConfig(bad, build()));
    assert.match(message, /static config/);
  }
  // ... and the entry reads `context?.config`, so a loader that calls it with
  // `null` gets that same sentence rather than a `TypeError` from
  // destructuring, which is what a build log would show without saying what is
  // wrong.
  for (const context of [undefined, null, {}, { config: null }]) {
    assert.match(refusal(() => appConfig(context)), /static config/);
  }
});

// --- the identity is committed nowhere --------------------------------------
//
// The two identities are the owner's, and the file above resolves them at build
// time precisely so that no copy of the app built from this tree ships with one
// baked in. A test fixture is the back door into that fence: a literal in a
// test is still a literal in the repository, and the next reader cannot tell a
// fixture from a value somebody meant to keep. So every identity-shaped literal
// in the tests is a **fictional sample** (`com.example.sample`), and this pin
// walks the tree for the owner's own value outside the places whose whole job is
// to tell the owner to set it.

/**
 * The owner's value, assembled from parts. It is written this way on purpose:
 * this file is inside the tree the pin walks, and a pin whose own source spells
 * the string out would flag itself (or need an allow-list entry that hides a
 * real occurrence next to it).
 */
const OWNER_IDENTITY = ['dev', 'fugaro', 'app'].join('.');

/**
 * The files that may carry it: **instructions, not configuration.**
 *
 * It is empty, and empty is the property rather than an oversight: no file
 * under `mobile/` or `docs/mobile/` carries the owner's value. The files that
 * tell the owner to set it are `mobile/README.md` -- **inside** this walk, and
 * it writes `<the reverse-DNS identity>` where the value goes -- and
 * `docs/gcp-live-checklist.md`, which is **outside** it and names no value
 * either (its guard prints whatever the owner's own environment resolved).
 *
 * An owner-run runbook under `docs/mobile/` whose `eas env:set` commands have
 * to spell the value out is the one case this set exists for; it would add its
 * path here, in the change that adds the file. `identityLiterals` takes the set
 * as an argument so that exception's effect is proven by a control below
 * instead of by a path that is not in the tree.
 */
const IDENTITY_ALLOWED = new Set();

/** The files under the two roots whose text carries the owner's identity. */
function identityLiterals(files, allowed = IDENTITY_ALLOWED) {
  return files.filter(({ path, text }) => text.includes(OWNER_IDENTITY) && !allowed.has(path)).map(({ path }) => path);
}

/** Every tracked (and untracked-but-not-ignored) file under `mobile/` and
 * `docs/mobile/`, read from the repository root. */
function scannedFiles() {
  const root = execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim();
  const listed = execFileSync(
    'git',
    ['ls-files', '-z', '-c', '-o', '--exclude-standard', '--', 'mobile', 'docs/mobile'],
    { cwd: root, encoding: 'utf8' },
  );
  return listed
    .split('\0')
    .filter(Boolean)
    .map((path) => ({ path, text: readFileSync(join(root, path), 'utf8') }));
}

test('the owner’s identity is committed nowhere under mobile/ or docs/mobile/', () => {
  // The positive control: the walk flags a planted file, and it does not flag
  // one that carries a fictional sample. Mutation: make `identityLiterals`
  // return an empty list and the first line goes red.
  assert.deepEqual(identityLiterals([{ path: 'mobile/src/x.js', text: `const id = '${OWNER_IDENTITY}';` }]), ['mobile/src/x.js']);
  assert.deepEqual(identityLiterals([{ path: 'mobile/src/x.js', text: 'com.example.sample' }]), []);
  // ... and the exception mechanism itself, exercised with the set an owner-run
  // runbook would add: it is empty in this tree, so this is the only place the
  // mechanism can be shown to work -- and it is proven here rather than by a
  // path that does not exist (mutation: ignore the `allowed` argument and this
  // line goes red).
  assert.equal(IDENTITY_ALLOWED.size, 0, 'nothing under these two roots may carry the value: the set is the exception list, and there is no exception today');
  assert.deepEqual(identityLiterals([{ path: 'docs/mobile/eas-signing-runbook.md', text: OWNER_IDENTITY }], new Set(['docs/mobile/eas-signing-runbook.md'])), []);

  // ... and the real tree: the fixtures here and in `app-config.test.js` are
  // fictional samples, and nothing else under the app or its docs carries the
  // owner's value. Mutation: paste the owner's identity into any file under
  // either root and this fails naming it.
  const files = scannedFiles();
  assert.equal(files.length > 50, true, `the walk found ${files.length} files: it is looking at the tree, not at nothing`);
  assert.deepEqual(identityLiterals(files), [], 'the owner’s identity is set in the EAS project, not committed');
  // The two files this task owns are among them, so the walk is not skipping
  // the app's own source.
  for (const path of ['mobile/app.config.js', 'mobile/eas.json', 'mobile/src/app-config.ids.test.js']) {
    assert.equal(files.some((file) => file.path === path), true, `${path} is in the walk`);
  }
  // ... and the file that tells the owner how to set the value is in the walk
  // too, writing the placeholder where the value goes. That is the whole of the
  // answer to "where does the owner read it": the README is instructions and is
  // inside the fence, and it carries `<the reverse-DNS identity>`, not the
  // identity. Mutation: paste the owner's value into the README's example
  // commands and this goes red (the pin above) while this line says why the
  // README is not an exception.
  const readme = files.find((file) => file.path === 'mobile/README.md');
  assert.equal(readme === undefined, false, 'the walk covers mobile/README.md, the file that tells the owner how to set the value');
  assert.equal(readme.text.includes('<the reverse-DNS identity>'), true, 'the README writes the placeholder where the value goes');
  assert.equal(readme.text.includes(OWNER_IDENTITY), false, 'and not the value itself');
});
