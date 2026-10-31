// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.

// The build-time guard for the two native config files (D260, plan Task P7.1b).
//
// The guard refuses an EAS build whose **resolved config carries no readable
// native config file**, and it checks the *file*, never a variable name: the
// names in `mobile/app.config.js`'s one constant are the plan's defaults, and
// the owner's EAS project may deliver the path under any name at all. So the
// red/green pairs below plant a **path that names no file** and a **file that
// exists**, and the "names are not enforced" case plants a path that arrived
// under a name the constant does not hold.

import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { after } from 'node:test';

import { ID_VARIABLES, NATIVE_CONFIG_VARIABLES, resolveConfig } from '../app.config.js';
import {
  GOOGLE_SERVICES_FIELD,
  NATIVE_CONFIG_README_SECTION,
  builtPlatforms,
  nativeConfigError,
  nativeConfigMissing,
} from '../native-config-guard.js';

const EXPO = JSON.parse(readFileSync(new URL('../app.json', import.meta.url), 'utf8')).expo;

// A fictional sample identity: one reverse-DNS name for both stores, the shape
// the patterns accept, and nobody's real value (the owner's lives in the EAS
// project and in no file of this repository).
const IOS = 'com.example.sample';
const ANDROID = 'com.example.sample';

// A real directory with two real files in it: the guard's question is "does the
// path name a file", and only the filesystem can answer it.
const dir = mkdtempSync(join(tmpdir(), 'fugaro-native-config-'));
const androidFile = join(dir, 'google-services.json');
const iosFile = join(dir, 'GoogleService-Info.plist');
writeFileSync(androidFile, '{}\n');
writeFileSync(iosFile, '<plist/>\n');
after(() => rmSync(dir, { recursive: true, force: true }));

/** An environment as EAS's build servers present it to `app.config.js`. */
const build = (extra = {}) => ({
  EAS_BUILD: 'true',
  [ID_VARIABLES.ios]: IOS,
  [ID_VARIABLES.android]: ANDROID,
  ...extra,
});

/** The message a call refuses with (see the ids test's `refusal`). */
function refusal(fn) {
  let thrown;
  assert.throws(fn, (error) => {
    thrown = error;
    return true;
  });
  return thrown.message;
}

test('nativeConfigMissing names exactly the platforms whose file is missing', () => {
  const both = {
    ios: { googleServicesFile: iosFile },
    android: { googleServicesFile: androidFile },
  };
  assert.deepEqual(nativeConfigMissing(both, ['android', 'ios']), []);

  // One planted control per failure shape the guard exists to catch.
  assert.deepEqual(
    nativeConfigMissing({ ios: { googleServicesFile: iosFile } }, ['android', 'ios']),
    ['android'],
    'an unset path is missing',
  );
  assert.deepEqual(
    nativeConfigMissing(
      { ios: { googleServicesFile: '' }, android: { googleServicesFile: androidFile } },
      ['android', 'ios'],
    ),
    ['ios'],
    'an empty path is missing',
  );
  assert.deepEqual(
    nativeConfigMissing({ android: { googleServicesFile: join(dir, 'never-written.json') } }, ['android']),
    ['android'],
    'a path that names no file is missing',
  );

  // The predicate is injected, so the "exists" half is provable on its own:
  // with everything reported absent, an existing file is still reported missing.
  assert.deepEqual(nativeConfigMissing(both, ['android', 'ios'], () => false), ['android', 'ios']);
});

test('a build whose resolved files exist resolves, and does not throw', () => {
  // The owner's builds: the two paths arrive in the environment and name files
  // that are there, so the guard has nothing to say.
  const resolved = resolveConfig(
    EXPO,
    build({
      [NATIVE_CONFIG_VARIABLES.android]: androidFile,
      [NATIVE_CONFIG_VARIABLES.ios]: iosFile,
    }),
  );
  assert.equal(resolved.android.googleServicesFile, androidFile);
  assert.equal(resolved.ios.googleServicesFile, iosFile);
});

test('a build whose file is absent refuses, and the message carries no value', () => {
  // Planted: a distinctive path that names no file. If the message echoed it,
  // the assertion below fails -- an error that repeats a build's environment is
  // an error that prints a path into a public log.
  const planted = join(dir, 'a-planted-path-that-must-not-be-printed.json');
  const message = refusal(() =>
    resolveConfig(
      EXPO,
      build({
        [NATIVE_CONFIG_VARIABLES.ios]: iosFile,
        [NATIVE_CONFIG_VARIABLES.android]: planted,
      }),
    ),
  );
  assert.match(message, /android/, 'the platform being built is named');
  assert.match(message, new RegExp(NATIVE_CONFIG_VARIABLES.android), 'the variable is named');
  assert.match(message, /mobile\/app\.config\.js/, 'where to change the name is named');
  assert.match(message, new RegExp(NATIVE_CONFIG_README_SECTION), "README's section is named");
  assert.equal(message.includes(planted), false, 'no path is echoed');
  assert.equal(message.includes(IOS), false, 'no identity is echoed');
  assert.equal(message.includes(NATIVE_CONFIG_VARIABLES.ios), false, 'the platform that was fine is not named');

  // The other failure shape: no path at all, with the iOS file present.
  const absent = refusal(() => resolveConfig(EXPO, build({ [NATIVE_CONFIG_VARIABLES.ios]: iosFile })));
  assert.match(absent, /android/);
  assert.match(absent, new RegExp(NATIVE_CONFIG_VARIABLES.android));
});

test('the names are not enforced: the guard reads the resolved file, not a variable name', () => {
  // A path that arrived under a name the constant does not hold still names a
  // file, and a build with a file is a build. The synthetic name is planted in
  // an environment where the plan's default is absent -- and that absence is
  // not itself a failure.
  const synthetic = 'FUGARO_SYNTHETIC_NATIVE_JSON';
  const env = { [synthetic]: androidFile };
  assert.equal(NATIVE_CONFIG_VARIABLES.android in env, false);
  assert.deepEqual(nativeConfigMissing({ android: { googleServicesFile: env[synthetic] } }, ['android']), []);

  // The message reads its variable name from whatever it is handed, so the ONE
  // constant in `app.config.js` is the single site a rename touches.
  assert.match(nativeConfigError('android', synthetic).message, new RegExp(synthetic));
  assert.match(nativeConfigError('android', NATIVE_CONFIG_VARIABLES.android).message, /GOOGLE_SERVICES_JSON/);
});

test('a path the environment did not carry still resolves: the guard reads the file', () => {
  // The end-to-end half of the same rule, and the shape the module-level case
  // above cannot reach: `resolveConfig` refuses on the **resolved** config, so
  // a static `googleServicesFile` on the config itself is a file like any
  // other even with the plan's variable unset. Mutation: refuse at the call
  // site instead -- `resolveConfig` throwing when a `NATIVE_CONFIG_VARIABLES`
  // entry is unset, which passes the case above -- and this resolution throws
  // rather than returning.
  const config = { ...EXPO, android: { ...EXPO.android, [GOOGLE_SERVICES_FIELD]: androidFile } };
  const env = build({ [NATIVE_CONFIG_VARIABLES.ios]: iosFile });
  assert.equal(NATIVE_CONFIG_VARIABLES.android in env, false, 'the plan’s Android variable is unset');

  const resolved = resolveConfig(config, env);
  assert.equal(resolved.android[GOOGLE_SERVICES_FIELD], androidFile);
});

test('EAS_BUILD_PLATFORM narrows the check to the platform being built', () => {
  assert.deepEqual(builtPlatforms({ EAS_BUILD_PLATFORM: 'android' }), ['android']);
  assert.deepEqual(builtPlatforms({ EAS_BUILD_PLATFORM: 'ios' }), ['ios']);
  assert.deepEqual(builtPlatforms({}), ['android', 'ios'], 'no platform named: check both');

  // Android is being built and the iOS file is nowhere: not this build's problem.
  const androidOnly = build({
    EAS_BUILD_PLATFORM: 'android',
    [NATIVE_CONFIG_VARIABLES.android]: androidFile,
  });
  assert.equal(resolveConfig(EXPO, androidOnly).android.googleServicesFile, androidFile);

  // ... and the reverse.
  const iosOnly = build({ EAS_BUILD_PLATFORM: 'ios', [NATIVE_CONFIG_VARIABLES.ios]: iosFile });
  assert.equal(resolveConfig(EXPO, iosOnly).ios.googleServicesFile, iosFile);
});

test('a platform name EAS does not use checks both, never one', () => {
  // The unrecognised value is treated as "none named" rather than as a
  // platform: an environment this file did not expect makes the guard check
  // more and never less. Mutation: take any non-empty value as a platform name
  // and the two shapes below become a check of one platform nobody builds.
  for (const named of ['web', 'Android', 'android,ios', 'true']) {
    assert.deepEqual(builtPlatforms({ EAS_BUILD_PLATFORM: named }), ['android', 'ios'], named);
  }

  // ... and the refusal that follows is real: only Android's file is there, so
  // the unrecognised platform name is still a build that would carry no iOS
  // config, and it is refused naming ios.
  const env = build({ EAS_BUILD_PLATFORM: 'web', [NATIVE_CONFIG_VARIABLES.android]: androidFile });
  assert.match(refusal(() => resolveConfig(EXPO, env)), /ios/);
});

test('nothing is required when neither switch is set', () => {
  // `npm test`, `expo start`, and `eas-cli`'s local resolution before upload --
  // which runs without `EAS_BUILD`, because EAS sets it on the build worker.
  assert.doesNotThrow(() => resolveConfig(EXPO, {}));
  assert.doesNotThrow(() => resolveConfig(EXPO, { EAS_BUILD: '', FUGARO_REQUIRE_IDS: '0' }));

  // The explicit local ask does require the file...
  assert.throws(() =>
    resolveConfig(EXPO, { FUGARO_REQUIRE_IDS: '1', [ID_VARIABLES.ios]: IOS, [ID_VARIABLES.android]: ANDROID }),
  );
  // ... and passes when both exist.
  const asked = resolveConfig(EXPO, {
    FUGARO_REQUIRE_IDS: '1',
    [ID_VARIABLES.ios]: IOS,
    [ID_VARIABLES.android]: ANDROID,
    [NATIVE_CONFIG_VARIABLES.android]: androidFile,
    [NATIVE_CONFIG_VARIABLES.ios]: iosFile,
  });
  assert.equal(asked.ios.googleServicesFile, iosFile);
});
