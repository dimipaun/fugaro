// `mobile/eas.json`'s tests (mobile plan, Task P4.4; D120, D125; Check 89).
//
// `eas.json` is the file the store path is built and submitted from, and it is
// the second file in this artifact (after `app.json`) that a reviewer is
// unlikely to read twice -- while Check 89, the owner-run check that a signed
// build reaches the stores' internal tracks only, is decided by what is in it.
// Four properties, and each one is a sentence from the register's own text:
//
//   * **the profiles exist and are named as the check names them** --
//     `development` (a dev client, internal distribution), `preview`, and
//     `internal`, which is the one Check 89 builds with;
//   * **`internal` is the store profile and nothing else**: `distribution:
//     store` (an `internal`-distribution build is an ad-hoc build with no
//     store submission at all), an Android `app-bundle` (the Play app-signing
//     path), and `submit.internal.android.track` **exactly `internal`** --
//     C8's whole content is that one value, because an internal release is not
//     a public one and cannot be withdrawn the way a public one can;
//   * **no credential path and no credential value anywhere in it.** A path to
//     a key file in `eas.json` is the finding Check 89's step 5 exists to
//     catch: the credentials belong to the EAS project (server-side, under the
//     owner's account) or to the owner's own machine, never to this
//     repository. The walk refuses any key whose name suggests a credential
//     (`serviceAccountKeyPath`, `ascApiKeyPath`, `keystorePath`, `token`,
//     `secret`, ...) whose value is a string, and it asks the same
//     `secretShaped` question of every string that `app-config.test.js` asks
//     of `app.json` -- one list of credential shapes, in one place;
//   * **no identity value is committed here either.** The bundle identifier
//     and the package name come from EAS environment variables at build time
//     (`app.config.js`), so `eas.json` names neither variable and carries no
//     reverse-DNS string: a value committed here would be a value every copy
//     of the app built from this tree ships, and the identity is the owner's
//     (D125's release path, which Check 89 is owner-run; `app-config.test.js`
//     holds the same fence on `app.json`).
//   * **each profile names the EAS environment it builds with.** A profile
//     that does not is resolved against EAS's default environment, and the
//     variable the profile needs may not be there: `development`'s dev-client
//     build reads the `development` environment, and the variables have to
//     exist in whatever environment a profile resolves to. The `env` fence
//     below is about **values**, not about this name -- `eas.json` may say
//     which environment holds them, and may not hold them.
//
// The credential walk carries a positive control (a planted path it must flag),
// so a green run means the walk can fail rather than meaning it found nothing.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { secretShaped } from './accounts/index.js';
import { ID_VARIABLES, isIdentifier } from '../app.config.js';

const RAW = readFileSync(new URL('../eas.json', import.meta.url), 'utf8');
const EAS = JSON.parse(RAW);

/** Every string in a JSON value, with the path it was found at. */
function stringsIn(value, path = '$') {
  if (typeof value === 'string') return [[path, value]];
  if (Array.isArray(value)) return value.flatMap((item, i) => stringsIn(item, `${path}[${i}]`));
  if (value !== null && typeof value === 'object') {
    return Object.entries(value).flatMap(([key, nested]) => stringsIn(nested, `${path}.${key}`));
  }
  return [];
}

/** The keys a credential would hide UNDER, whatever its value looks like. */
const CREDENTIAL_NAMES = /key|secret|token|path/i;

/** The files a credential would BE: what a path in `eas.json` points at. */
const CREDENTIAL_FILES = /\.(json|p8|p12|pfx|pem|key|cer|keystore|jks|mobileprovision)$/i;

/**
 * Every setting that is a credential or points at one: a key whose NAME
 * suggests a credential and whose value is a string, or any string value that
 * is a credential-shaped FILE (whatever its key is called -- a
 * `serviceAccount: "./google-play-service-account.json"` is the same finding
 * under a different name).
 */
function credentialPaths(value, path = '$', found = []) {
  if (Array.isArray(value)) {
    value.forEach((item, i) => credentialPaths(item, `${path}[${i}]`, found));
    return found;
  }
  if (value !== null && typeof value === 'object') {
    for (const [key, nested] of Object.entries(value)) {
      const names = CREDENTIAL_NAMES.test(key);
      const points = typeof nested === 'string' && CREDENTIAL_FILES.test(nested);
      if (typeof nested === 'string' && (names || points)) found.push(`${path}.${key}`);
      credentialPaths(nested, `${path}.${key}`, found);
    }
  }
  return found;
}

test('the walk finds a planted credential path, so the rest of this file can fail', () => {
  const planted = {
    submit: {
      internal: {
        android: { serviceAccountKeyPath: './google-play-service-account.json', track: 'internal' },
        ios: { ascApiKeyPath: './asc-api-key.p8', ascAppId: '1234567890' },
      },
    },
    build: {
      internal: {
        credentialsSource: 'local', // a SOURCE, not a credential: not a finding
        credentials: './credentials.json', // ... but a file path is, under any name
        keystorePath: './release.keystore',
      },
    },
  };
  assert.deepEqual(credentialPaths(planted), [
    '$.submit.internal.android.serviceAccountKeyPath',
    '$.submit.internal.ios.ascApiKeyPath',
    '$.build.internal.credentials',
    '$.build.internal.keystorePath',
  ]);
  // ... and the planted strings are refused by the shape list too (`ascAppId`
  // is a public id, so it is the NAME half of the fence that has to catch it).
  assert.equal(secretShaped('1234567890'), null, 'an id is not a credential by shape');
});

test('the three profiles are the ones the check names', () => {
  assert.deepEqual(Object.keys(EAS.build).sort(), ['development', 'internal', 'preview']);
  assert.deepEqual(Object.keys(EAS.submit).sort(), ['internal']);
  // Mutation: rename or drop a profile and this fails, which is what makes a
  // later "the check uses the `internal` profile" line mean something.
  assert.equal('internal' in EAS.build, true);
});

test('development is a dev client on internal distribution, preview is internal', () => {
  assert.equal(EAS.build.development.developmentClient, true);
  assert.equal(EAS.build.development.distribution, 'internal');
  assert.equal(EAS.build.preview.distribution, 'internal');
});

test('the `internal` profile is the store profile: store distribution, an app bundle', () => {
  const profile = EAS.build.internal;
  assert.equal(profile.distribution, 'store');
  assert.equal(
    profile.android?.buildType,
    'app-bundle',
    'a store build is an AAB signed with the upload key; an APK is not what Play takes',
  );
  // Mutation: `distribution: internal` (the ad-hoc distribution) passes every
  // "there is a profile" assertion above and makes Check 89 meaningless, which
  // is why this value is asserted rather than the profile's existence.
  assert.notEqual(profile.distribution, 'internal');
});

test('the submit profile lands on the internal track, which is C8', () => {
  // The one value Check 89 step 1 reads with `jq '.submit.internal.android.track'`.
  assert.equal(EAS.submit.internal.android?.track, 'internal');
  // The iOS side is left to EAS's own prompts: an entry exists, and it names
  // no key, no key path and no App Store Connect id -- all of which are the
  // owner's, and all of which Check 89 step 5 greps the tree for.
  assert.deepEqual(Object.keys(EAS.submit.internal.ios ?? {}), [], 'no ios submit key path, and no ascAppId');
  for (const forbidden of ['ascAppId', 'ascApiKeyId', 'ascApiKeyIssuerId']) {
    assert.equal(RAW.includes(forbidden), false, forbidden);
  }
  // No submit entry names a public track. (`production` is the name of the
  // *environment* the `internal` profile builds with, which is a different
  // thing from a track: the fence is over `submit`, where a track would live.)
  for (const [path, value] of stringsIn(EAS.submit)) {
    assert.equal(['production', 'beta', 'alpha'].includes(value), false, `${path} names the ${value} track`);
  }
});

test('no credential path, no credential-shaped name and no credential-shaped value', () => {
  // Mutation: add `serviceAccountKeyPath` to the submit profile and this is the
  // test that goes red -- the finding Check 89 step 5 exists to catch.
  assert.deepEqual(credentialPaths(EAS), []);
  for (const [path, value] of stringsIn(EAS)) {
    assert.equal(secretShaped(value), null, `${path} is ${secretShaped(value)}`);
  }
});

test('every profile says which EAS environment it builds with', () => {
  // The variables have to exist in the environment a profile resolves to, and
  // EAS has a default when a profile names none -- so each profile names its
  // own, and the README sets the two variables in all three. Mutation: drop
  // `build.development.environment` and a dev-client build resolves an
  // environment whose variables the README does not create.
  assert.deepEqual(
    Object.fromEntries(Object.entries(EAS.build).map(([name, profile]) => [name, profile.environment])),
    { development: 'development', preview: 'preview', internal: 'production' },
  );
});

test('no identity is committed here: the variables are EAS environment variables', () => {
  // Mutation: paste the identifier into an `env` block and both halves go red.
  for (const variable of Object.values(ID_VARIABLES)) {
    assert.equal(RAW.includes(variable), false, `${variable} belongs to the EAS project's environment variables`);
  }
  assert.equal('env' in EAS, false, 'and no top-level env block names one');
  // The fence is over *values*: `environment` above names where the values
  // live, which is the opposite of writing one down.
  for (const [name, profile] of Object.entries(EAS.build)) {
    assert.equal('env' in profile, false, `build.${name} sets no environment value of its own`);
  }
  for (const [path, value] of stringsIn(EAS)) {
    assert.equal(isIdentifier(value, 'ios') && value.includes('.'), false, `${path} is ${value}`);
  }
});

test('the build number is EAS’s, the version is the tree’s, and a build is a committed tree', () => {
  // `appVersionSource` is where the **build number** lives and increments --
  // `expo.ios.buildNumber` and `expo.android.versionCode` -- and **not** the
  // version the stores show or the app reports: that one is `expo.version` in
  // `app.json`, the line C6 ties to the release tag, and check 89 step 4 is
  // the step that compares the installed app's version with the tag. Read the
  // other way round it tells a reader to leave `app.json` alone, and step 4
  // then fails on an app that reports `0.0.0`.
  assert.equal(EAS.cli?.appVersionSource, 'remote', 'the build number is EAS\'s to hold and increment, not a file\'s');
  assert.equal(EAS.cli?.requireCommit, true, 'a store build records the commit it was made from');
  // ... and the file a reader trusts about it says the same thing. Mutation:
  // put "the store version is EAS's to move, not a file's" back into the
  // README's paragraph on this key and the first two lines below go red.
  const readme = readFileSync(new URL('../README.md', import.meta.url), 'utf8');
  const at = readme.indexOf('`cli.appVersionSource`');
  assert.notEqual(at, -1, 'the README explains `cli.appVersionSource` where the owner reads the profiles');
  const rest = readme.slice(at);
  const end = rest.indexOf('\n\n');
  const about = end < 0 ? rest : rest.slice(0, end);
  assert.match(about, /build number/, 'the README says which number `appVersionSource` moves');
  assert.match(about, /expo\.version/, 'and names the version line that is the tree\'s to set (C6, check 89 step 4)');
  assert.equal(about.includes("the store version is EAS's to move"), false, 'the README does not hand the store version to EAS');
});
