// The dynamic config's tests (mobile plan, Task P4.4; D120, D125; Check 89).
//
// `mobile/app.config.js` is the one place the app's two release identities come
// from -- the iOS bundle identifier and the Android package name are read from
// EAS environment variables at build time, because P3.1's boundary test and
// D125 forbid committing them to `app.json`. That makes this file the second
// half of that fence, and it is written about the two failure modes a config
// file has:
//
//   * **a build that shipped without an identity, or with a typo'd one.** A
//     missing or malformed variable under `EAS_BUILD` (or an explicit
//     `FUGARO_REQUIRE_IDS=1`) is a thrown error naming the variable and the
//     fix -- never a config that resolves quietly. The Android package is the
//     one that cannot be corrected later: it is fixed the moment the app
//     exists on Play, so a digit-starting segment is refused rather than sent.
//   * **a value that leaked.** The messages describe the shape a value must
//     have and **never repeat the value**, and no environment value other than
//     the two identities reaches the resolved config -- the one thing a
//     mistyped variable might hold is something that must not be printed into
//     a build log or baked into an artifact.
//
// And the counter-case, which is what keeps the tests runnable at all: with
// neither switch set -- a developer's `expo start`, and `npm test` itself --
// the config comes back **without** the identities and nothing is required.
//
// Every refusal below is mutation-proven the same way the tree's other guards
// are: the property is stated as a table, the mutation that breaks it is named
// in the test's own comment, and the mutated run fails. The scans carry
// positive controls (a planted value the pattern must flag), so a green run
// means the walk can fail rather than meaning the walk found nothing.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import appConfig, {
  ID_PATTERNS,
  ID_VARIABLES,
  isIdentifier,
  requiresIdentifiers,
  resolveConfig,
} from '../app.config.js';

const APP_JSON = JSON.parse(readFileSync(new URL('../app.json', import.meta.url), 'utf8'));
const EXPO = APP_JSON.expo;
const SOURCE = readFileSync(new URL('../app.config.js', import.meta.url), 'utf8');

/** The owner's own value on 2026-10-19: one reverse-DNS name for both stores. */
const IOS = 'dev.fugaro.app';
const ANDROID = 'dev.fugaro.app';

/** An environment as EAS's build servers present it to `app.config.js`. */
const build = (extra = {}) => ({
  EAS_BUILD: 'true',
  [ID_VARIABLES.ios]: IOS,
  [ID_VARIABLES.android]: ANDROID,
  ...extra,
});

/** `config` without the two identity fields, so what is left can be compared. */
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

test('the pattern accepts a reverse-DNS name and refuses everything else', () => {
  // The accepted shapes, including the digit-starting segment an iOS bundle
  // identifier may have.
  for (const good of [IOS, 'a.b', 'x1.y2', 'dev.fugaro-app.io', 'dev.fugaro_app.io', 'dev.1app']) {
    assert.equal(isIdentifier(good, 'ios'), true, good);
  }
  // ... and the refused ones. Mutation: drop the `+` quantifier or the segment
  // class and 'dev.fugaro' or 'Dev.Fugaro.App' starts passing.
  for (const bad of [
    'devfugaro', // one segment: not a reverse-DNS name
    'dev..app', // an empty segment
    '.dev.app',
    'dev.app.',
    'Dev.Fugaro.App', // upper case
    'dev fugaro.app',
    'dev.fugaro.app\n', // a newline an env var picked up on the way in
    'dev/fugaro.app',
    '',
    'dev.fugaro.app:443',
  ]) {
    assert.equal(isIdentifier(bad, 'ios'), false, JSON.stringify(bad));
  }
  // The Android package is stricter by exactly one rule: Play refuses a segment
  // that starts with a digit, and this package can never be changed later.
  assert.equal(isIdentifier('dev.1app', 'android'), false, 'Play refuses a digit-starting segment');
  assert.equal(isIdentifier('1dev.fugaro', 'android'), false, '... in the first segment as well as the last');
  assert.equal(isIdentifier('dev.fugaro.app', 'android'), true);
  assert.equal(ID_PATTERNS.android.source !== ID_PATTERNS.ios.source, true);
  assert.equal(isIdentifier(IOS, 'windows'), false, 'an unknown platform is not an identity');
  assert.equal(isIdentifier(7, 'ios'), false, 'a non-string is not an identity');
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
    assert.match(message, /eas env:create/, 'and says where to set it');
  });

  test(`a build refuses a malformed ${variable}, and the message never repeats the value`, () => {
    // Each of these is a value somebody could paste: a display name, a
    // one-segment name, and a secret-looking string. Mutation: echo the value
    // into the message and the last assertion goes red -- which is the point,
    // because a build log is the last place a mistyped variable should print.
    for (const bad of ['Fugaro', 'devfugaro', 'not a package name', 'sk-notAKeyButItLooksLikeOne123456']) {
      const env = build({ [variable]: bad });
      const message = refusal(() => resolveConfig(EXPO, env));
      assert.match(message, new RegExp(variable), 'the message names the variable');
      assert.match(message, /reverse-DNS/, 'and the shape the value must have');
      assert.equal(message.includes(bad), false, `the message repeats ${JSON.stringify(bad)}`);
    }
  });
}

test('a build with both variables resolves them onto the static config, and changes nothing else', () => {
  const resolved = resolveConfig(EXPO, build());
  assert.equal(resolved.ios.bundleIdentifier, IOS);
  assert.equal(resolved.android.package, ANDROID);
  // The identities are the ONLY difference: every other field of the static
  // config survives untouched (`supportsTablet`, `edgeToEdgeEnabled`, the
  // name, the slug and the version included).
  assert.deepEqual(withoutIdentities(resolved), EXPO);
  // ... and the static object itself is not mutated by resolving it.
  assert.equal('bundleIdentifier' in EXPO.ios, false);
  assert.equal('package' in EXPO.android, false);
});

test('a dev/lint run gets the config without the identities, whatever the environment holds', () => {
  // The counter-case: `expo start` and `npm test` have neither switch set, and
  // they must keep working without a store account.
  assert.deepEqual(resolveConfig(EXPO, {}), EXPO);
  // ... including when the variables are set but garbage: nothing is required,
  // so nothing is validated and nothing is added. Mutation: validate in local
  // mode too, and this throws.
  assert.deepEqual(resolveConfig(EXPO, { [ID_VARIABLES.ios]: 'Fugaro', [ID_VARIABLES.android]: 'x' }), EXPO);
  // ... and a value that got into the static config by hand is REMOVED here
  // rather than passed through, so `app.json` cannot quietly become a second
  // site for the identities. Mutation: return `config` as-is and this fails.
  const planted = { ...EXPO, ios: { ...EXPO.ios, bundleIdentifier: 'com.example.app' }, android: { ...EXPO.android, package: 'com.example.app' } };
  const resolved = resolveConfig(planted, {});
  assert.equal('bundleIdentifier' in resolved.ios, false);
  assert.equal('package' in resolved.android, false);
  assert.equal(planted.ios.bundleIdentifier, 'com.example.app', 'the static object is not mutated either');
});

test('no environment value but the two identities reaches the config', () => {
  // Mutation: spread `env` into `extra` (or read a second variable) and this
  // goes red. The secret-shaped names below are values, not credentials: they
  // exist to prove that a variable this file does not know is a variable it
  // does not carry.
  const polluted = build({
    FIREBASE_TOKEN: 'not-a-real-token',
    FUGARO_SETTINGS_KEY: 'not-a-real-key',
    [ID_VARIABLES.ios + '_EXTRA']: 'dev.fugaro.extra',
    NODE_ENV: 'production',
  });
  assert.deepEqual(resolveConfig(EXPO, polluted), resolveConfig(EXPO, build()));
});

test('app.config.js names exactly the two identity variables, as literals', () => {
  // The positive control: a planted third variable is flagged, so the walk
  // below is known to be able to fail.
  assert.deepEqual(namedIn("const v = process.env['FIREBASE_TOKEN'];\n"), ['FIREBASE_TOKEN']);
  // ... and the file itself names the two, and nothing else. A third variable
  // read here would be a third thing a build depends on, which is the reading
  // this test exists to make deliberate.
  assert.deepEqual(namedIn(SOURCE), ['FUGARO_ANDROID_PACKAGE', 'FUGARO_IOS_BUNDLE_ID']);
  // The two switches are read off `env` by name and are not values of their
  // own; the point above still reads them out of the source, so the property
  // is "the only names written as literals are the identities".
  assert.match(code(SOURCE), /requiresIdentifiers/);
});

test('the resolved config carries the identities only when the run asks for them', () => {
  // The whole file in one line: same env, same base, one switch apart.
  const asked = resolveConfig(EXPO, { EAS_BUILD: 'true', ...build() });
  const notAsked = resolveConfig(EXPO, { [ID_VARIABLES.ios]: IOS, [ID_VARIABLES.android]: ANDROID });
  assert.equal('bundleIdentifier' in asked.ios, true);
  assert.equal('bundleIdentifier' in notAsked.ios, false);
});

test("Expo's entry reads process.env, and refuses a call with no static config", () => {
  const saved = {};
  const keys = ['EAS_BUILD', 'FUGARO_REQUIRE_IDS', ID_VARIABLES.ios, ID_VARIABLES.android];
  for (const key of keys) saved[key] = Object.prototype.hasOwnProperty.call(process.env, key) ? process.env[key] : undefined;
  try {
    for (const key of keys) delete process.env[key];
    // Local mode: the entry resolves over the real `process.env`, and adds
    // nothing. Mutation: `appConfig` passing a frozen empty env, or ignoring
    // `process.env`, and the build-mode assertion below cannot go green.
    assert.deepEqual(appConfig({ config: EXPO }), EXPO);
    process.env.EAS_BUILD = 'true';
    process.env[ID_VARIABLES.ios] = IOS;
    process.env[ID_VARIABLES.android] = ANDROID;
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
