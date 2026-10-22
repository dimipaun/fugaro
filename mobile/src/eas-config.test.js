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
//   * **a profile a store is submitted from increments its build number.** The
//     build number lives on the EAS project (`cli.appVersionSource: remote`),
//     and a project-held number does not move by itself: `autoIncrement: true`
//     on the submitted profile is what moves it, and without it the second
//     build and submit is refused (a redundant binary on App Store Connect, a
//     duplicate version code on Play). It is on that profile alone -- the
//     internal-distribution profiles reach no store, and a build number spent
//     on one of them is spent for nothing.
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
//     holds the same fence on `app.json`). **One key of the iOS submit section
//     is an exception, and it is the app's, not an identity and not a
//     credential**: `ascAppId`, the number App Store Connect gives the app's
//     own record there (EAS printed it -- `ASC App ID: 6824584935` -- when the
//     owner's first submission of 2026-10-20 created that record, and EAS never
//     asks for it again). It reaches exactly one field of the config the
//     submission is made from, it is public the way the project id is, and the
//     section may carry **that key and no other**: the identities stay in the
//     environment, and the Apple ID, the team id, an App Store Connect API key
//     and every path to one stay out of this repository entirely.
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

/**
 * The one key the iOS submit section may carry, and the shape its value must
 * have: **the digits** App Store Connect's app record carries. EAS reads the
 * field as a string (`ascAppId` is spelled as a string in its own schema), so a
 * string of digits is what is admitted and a bare JSON number is not -- and the
 * digit fence is doing real work at the value as well as at the key: a bundle
 * identifier, an Android package, an Apple team id and an App Store Connect API
 * key id are all **not** digits, so a value pasted into this key is refused
 * here rather than at the store, which is what makes it safe to commit a number
 * that reaches a submission.
 */
const ASC_APP_ID = /^[0-9]+$/;

/**
 * Every setting of the iOS submit section this file refuses, as the path it was
 * found at. The section is the one place a submission of the app names
 * something on the other end, so it is an **allow-list** and not a list of
 * forbidden names: `ascAppId` (with a digit value) is the only entry that may
 * be here, and everything else -- the bundle identifier and the package name
 * (which come from the EAS environment, above), the Apple account and the team
 * id (which are the owner's), an App Store Connect API key, its issuer id or a
 * path to one (which is a credential and lives on the EAS project or the
 * owner's machine) -- is a finding, whatever it is called: a name-based list
 * would admit the next spelling of the same thing, and this section's whole
 * content is "which keys may reach a store submission".
 *
 * A section that is not an object is the same finding (there is nothing to
 * allow-list), so a caller who wrote a string where the profile goes gets a
 * path back rather than a silent pass.
 */
function iosSubmitFindings(value, path = '$.submit.internal.ios') {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return [path];
  const found = [];
  for (const [key, nested] of Object.entries(value)) {
    if (key !== 'ascAppId') found.push(`${path}.${key}`);
    else if (typeof nested !== 'string' || !ASC_APP_ID.test(nested)) found.push(`${path}.ascAppId`);
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

test('a profile a store is submitted from increments its remote build number', () => {
  // `appVersionSource: remote` puts the build number on the EAS project, and a
  // project-held number does not move by itself: without `autoIncrement` every
  // build of the profile reuses the same `expo.ios.buildNumber` /
  // `expo.android.versionCode`, so the first build and submit is accepted and
  // the second is refused -- App Store Connect rejects a build number it
  // already has (a redundant binary) and Play rejects a duplicate version code.
  // Check 89 step 2 builds and step 3 submits; a check that can be run once is
  // not a check.
  //
  // The property is stated over the **submit** side, because that is the set of
  // profiles that reach a store: each submitted profile's build profile has to
  // increment. Mutation: drop `build.internal.autoIncrement` and this goes red.
  assert.equal(EAS.cli?.appVersionSource, 'remote', 'the premise: the build number is the project\'s, so a profile has to ask for the increment');
  const submitted = Object.keys(EAS.submit ?? {});
  assert.equal(submitted.length > 0, true, 'the submit side names the profiles this property is about');
  for (const name of submitted) {
    assert.equal(name in EAS.build, true, `submit.${name} submits a build profile that exists`);
    assert.equal(
      EAS.build[name].autoIncrement,
      true,
      `build.${name} is submitted to a store, so its remote build number has to be incremented -- without it the second build is refused as a redundant binary or a duplicate version code`,
    );
  }
  // ... and it is on those alone: `development` and `preview` reach no store
  // (the README says why), so a build number is not spent on them.
  assert.deepEqual(
    Object.entries(EAS.build).filter(([, profile]) => profile.autoIncrement === true).map(([name]) => name),
    ['internal'],
    'only a store-submitted profile increments: the build numbers are the release\'s, and an internal-distribution build nobody submits spends one for nothing',
  );
});

test('the submit profile lands on the internal track, which is C8', () => {
  // The one value Check 89 step 1 reads with `jq '.submit.internal.android.track'`.
  assert.equal(EAS.submit.internal.android?.track, 'internal');
  // The iOS side carries the app's own record on the other end and nothing
  // else: `ascAppId` is what EAS prints when it creates that record and what it
  // would otherwise ask for on every submission afterwards, and it is the one
  // key here (the allow-list above is the fence; this is the value of it).
  assert.deepEqual(iosSubmitFindings(EAS.submit.internal.ios), []);
  assert.deepEqual(Object.keys(EAS.submit.internal.ios), ['ascAppId'], 'the App Store Connect app id, and nothing beside it');
  assert.match(EAS.submit.internal.ios.ascAppId, ASC_APP_ID, 'the digits App Store Connect gave the app record');
  // No credential and no credential path: the API key, its issuer id and any
  // path to one are the owner's, and Check 89 step 5 greps the tree for them.
  for (const forbidden of ['ascApiKeyId', 'ascApiKeyIssuerId', 'ascApiKeyPath']) {
    assert.equal(RAW.includes(forbidden), false, forbidden);
  }
  // No submit entry names a public track. (`production` is the name of the
  // *environment* the `internal` profile builds with, which is a different
  // thing from a track: the fence is over `submit`, where a track would live.)
  for (const [path, value] of stringsIn(EAS.submit)) {
    assert.equal(['production', 'beta', 'alpha'].includes(value), false, `${path} names the ${value} track`);
  }
});

test('the iOS submit section admits the app record’s id, and refuses every other entry', () => {
  // The positive control: the one key, with the digits Apple's record carries,
  // is admitted -- so a green run below means the fence can refuse, rather than
  // meaning it refuses everything. (An empty section is admitted too: it is the
  // state this key replaced, and a fence that refused it would be a fence
  // against reverting.)
  assert.deepEqual(iosSubmitFindings({ ascAppId: '6824584935' }), []);
  assert.deepEqual(iosSubmitFindings({}), []);

  // The boundary, one planted entry at a time -- each of these is a value that
  // would otherwise reach a store submission from this repository, and each is
  // the mutation this test exists to catch. The three below the ids are the
  // same finding under the one key that IS allowed: the fence is on the value
  // as well as on the name, which is what keeps `ascAppId` from becoming a
  // place to put an identity.
  const planted = [
    ['the iOS identity', { bundleIdentifier: 'com.example.app' }],
    ['the Android package', { package: 'com.example.app' }],
    ['the Apple account', { appleId: 'owner@example.com' }],
    ['the Apple team id', { appleTeamId: 'AB12XYZ34S' }],
    ['an App Store Connect API key id', { ascApiKeyId: '2X9R4HXF34' }],
    ['its issuer id', { ascApiKeyIssuerId: '69a6de70-05c2-47e3-8f5f-4c3b2a1d0e9f' }],
    ['a path to an API key', { ascApiKeyPath: './asc-api-key.p8' }],
    ['an identity pasted into the allowed key', { ascAppId: 'com.example.app' }],
    ['a team id pasted into it', { ascAppId: 'AB12XYZ34S' }],
    ['a bare JSON number, which is not the string EAS reads', { ascAppId: 6824584935 }],
    ['a section that is not an object at all', 'com.example.app'],
  ];
  for (const [what, section] of planted) {
    const expected = typeof section === 'object' && !Array.isArray(section)
      ? [`$.submit.internal.ios.${Object.keys(section)[0]}`]
      : ['$.submit.internal.ios'];
    assert.deepEqual(iosSubmitFindings(section), expected, `${what} must be refused in the iOS submit section`);
  }
  // ... and the same walk over what the file actually carries, so a section
  // that grew a second key is this test's failure too and not only the
  // allow-list assertion above.
  assert.deepEqual(iosSubmitFindings({ ...EAS.submit.internal.ios, appleId: 'owner@example.com' }), [
    '$.submit.internal.ios.appleId',
  ]);

  // The README tells the owner what this key is and what it replaced: the iOS
  // submit side is no longer left to EAS's prompt, and a README that still says
  // so sends the owner looking for a question nobody asks any more.
  const readme = readFileSync(new URL('../README.md', import.meta.url), 'utf8');
  assert.equal(
    readme.includes('The iOS submit side names nothing at all'),
    false,
    'the README still says the iOS submit side names nothing: it names the App Store Connect app id now',
  );
  assert.match(readme, /`ascAppId`/, 'the README says which key carries the app id, where the owner reads the profiles');
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
  // `appVersionSource` is where the **build number** is kept --
  // `expo.ios.buildNumber` and `expo.android.versionCode` -- and **not** the
  // version the stores show or the app reports: that one is `expo.version` in
  // `app.json`, the line C6 ties to the release tag, and check 89 step 4 is
  // the step that compares the installed app's version with the tag. Read the
  // other way round it tells a reader to leave `app.json` alone, and step 4
  // then fails on an app that reports `0.0.0`.
  //
  // And `remote` is where the number is *stored*, not a promise that it moves:
  // the increment is a profile's own (`autoIncrement`, asserted below), so
  // this line does not stand on its own.
  assert.equal(EAS.cli?.appVersionSource, 'remote', 'the build number is EAS\'s to hold, and to move only where a profile says so: `remote` alone leaves it standing still');
  assert.equal(EAS.cli?.requireCommit, true, 'a store build records the commit it was made from');
  // ... and the file a reader trusts about it says the same thing. Mutation:
  // put "the store version is EAS's to move, not a file's" back into the
  // README's paragraph on this key and the first two lines below go red; drop
  // `autoIncrement` from the paragraph and the third goes red (a README that
  // says EAS "increments it" without the flag is the claim that builds a
  // second time and is refused).
  const readme = readFileSync(new URL('../README.md', import.meta.url), 'utf8');
  const at = readme.indexOf('`cli.appVersionSource`');
  assert.notEqual(at, -1, 'the README explains `cli.appVersionSource` where the owner reads the profiles');
  const rest = readme.slice(at);
  const end = rest.indexOf('\n\n');
  const about = end < 0 ? rest : rest.slice(0, end);
  assert.match(about, /build number/, 'the README says which number `appVersionSource` holds');
  assert.match(about, /expo\.version/, 'and names the version line that is the tree\'s to set (C6, check 89 step 4)');
  assert.match(about, /autoIncrement/, 'and names the flag that actually moves the remote build number');
  assert.match(about, /reach no\s+store/, 'and says why it is on the submitted profile alone: the profiles that reach no store do not spend a build number');
  assert.equal(about.includes("the store version is EAS's to move"), false, 'the README does not hand the store version to EAS');
});
