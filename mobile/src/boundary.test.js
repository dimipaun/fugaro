// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// Where a credential may be written, named as a list of files
// (mobile plan, Task P3.1; D111, D114, and reconciliation §4 tension (b2)
// clause (4), which asks this task for exactly this in the shape of D54b
// clause (3): a scan over the artifact's source, plus a named mutation that
// makes it red).
//
// **Why a scan and not a rule.** "No code path writes either item outside
// secure storage" is not a property any single test of the store can hold: the
// store could be perfect and a screen could still write a token into
// AsyncStorage, a log line or a fixture. What makes it true is that the set of
// files that CAN write one is small, named here, and does not grow without
// somebody editing this list. So each rule below is a statement of the form
// "this token appears in these files and no others", and the failure message
// is a path a reviewer can open.
//
// **The rules, and what each is for.**
//
//   1. `...ItemAsync(` -- the Keychain SDK's own surface. It may be called
//      from `expo-secure-store.js` and nowhere else, so the platform's
//      storage is reached through exactly one module, whose one decision (the
//      item's accessibility) has its own test. A screen that called the SDK
//      directly would bypass the port, the `Secret` check and the
//      accessibility.
//   2. `reveal(` -- the only way to the material (`secret.js`). In production
//      code it appears in exactly two files: `secret.js`, which defines it,
//      and `secure-store.js`, which is the one place material is handed to a
//      store. Everywhere else -- a screen, a hook, a reader -- it is an
//      offence, because a screen that reveals a token is a screen one refactor
//      away from rendering or logging it.
//   3. `fugaro.account.` -- the Keychain namespace. It may be composed in
//      `secure-store.js` and reserved against in `settings.js`, and nowhere
//      else. A second module that knows the namespace is a second module that
//      can address an item.
//   4. `console.` -- nothing in the app logs anything. D111 says the two items
//      are "never logged", and the cheapest way to keep that true is to have
//      no logger: a `Secret` prints as `[secret idToken]` (see
//      `secret.test.js`), but a value revealed into a template literal and
//      handed to `console.log` prints itself.
//
// **The self-exemption and the positive controls.** This file contains all
// four tokens, so it exempts itself by name -- once, visibly -- and every rule
// is asserted against a planted example as well as against the tree. Without
// the controls, a typo in a pattern would turn the whole file green while
// checking nothing, which is the one way a scan can lie.

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import test from 'node:test';

const ROOT = join(import.meta.dirname, '..');
const SELF = relative(ROOT, import.meta.filename).split(sep).join('/');

/** Directories that are not this app's source. */
const SKIP = new Set(['node_modules', '.git', '.expo', 'dist', 'android', 'ios']);

/** Every `.js` file under `mobile/`, app-relative, sorted. */
function sourceFiles(dir = ROOT, found = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!SKIP.has(entry.name)) sourceFiles(path, found);
      continue;
    }
    if (entry.name.endsWith('.js')) found.push(relative(ROOT, path).split(sep).join('/'));
  }
  return found;
}

const isTest = (file) => file.endsWith('.test.js');

/**
 * The rules. `allow` lists the production files the token may appear in; the
 * test files are always allowed (they hold fakes, and a fake must be able to
 * call the method it stands in for) and so is this file.
 */
const RULES = [
  {
    name: 'the Keychain is reached through one module',
    pattern: /\.\w*ItemAsync\s*\(/,
    allow: ['src/accounts/expo-secure-store.js'],
    why: 'a direct SDK call bypasses the port, the Secret check and the accessibility',
  },
  {
    name: 'the material is revealed in two files and nowhere else',
    pattern: /\breveal\s*\(/,
    allow: ['src/accounts/secret.js', 'src/accounts/secure-store.js'],
    why: 'a caller that reveals a token is one refactor from printing it',
  },
  {
    name: 'the keychain namespace is composed in one module',
    pattern: /fugaro\.account\./,
    allow: ['src/accounts/secure-store.js', 'src/accounts/settings.js'],
    why: 'a second module that knows the namespace can address an item',
  },
  {
    name: 'the app logs nothing',
    pattern: /\bconsole\s*\./,
    allow: [],
    why: 'D111: the two items are never logged, so there is no logger',
  },
];

/**
 * The code of a file, without its comments.
 *
 * The rules are about code paths, and this tree's comments discuss the very
 * tokens the rules forbid (`secret.js`'s header explains what a caller may do
 * with the material, in prose, with the method name in it). Scanning the prose
 * would mean allow-listing every file that documents the rule -- which is
 * every file -- so the comments come out first.
 *
 * The limitation, stated rather than hidden: this is not a parser. A `//`
 * inside a string literal would strip the rest of that line, and a rule could
 * then miss a real offence on it. Nothing under `mobile/` contains such a
 * string, the positive control below proves each pattern still matches real
 * code, and `secret.js`'s `reveal()` is the token a stripped line would hide.
 */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/** Apply one rule to a map of path -> text, returning the offences. */
function offences(rule, files) {
  return [...files]
    .filter(([file]) => file !== SELF && !isTest(file) && !rule.allow.includes(file))
    .filter(([, text]) => rule.pattern.test(code(text)))
    .map(([file]) => file);
}

test('the scan walks the app, and not an empty list', () => {
  const files = sourceFiles();
  // A scan that found nothing would pass every rule below for the wrong
  // reason, so the walk is asserted before it is trusted.
  assert.ok(files.length >= 10, `expected the app's source, found ${files.length} files`);
  for (const expected of [
    'index.js',
    'src/accounts/store.js',
    'src/accounts/secure-store.js',
    'src/accounts/expo-secure-store.js',
    'src/app/root.js',
  ]) {
    assert.ok(files.includes(expected), `${expected} is in the walk`);
  }
  assert.ok(files.includes(SELF), 'this file is in the walk, which is why it exempts itself');
  assert.equal(files.some((file) => file.startsWith('node_modules/')), false);
});

for (const rule of RULES) {
  test(`${rule.name} — ${rule.why}`, () => {
    // The positive control first: the rule must flag a planted example, in
    // real code, or a green run below would mean nothing.
    const line = control(rule);
    const example = { 'src/app/offender.js': `// ${line} in a comment is fine\nfunction offence() { ${line}; }\n` };
    assert.deepEqual(offences(rule, new Map(Object.entries(example))), ['src/app/offender.js'], line);
    // ... and the planted line is not flagged when it is only prose, which is
    // what lets this tree's comments keep explaining the rules.
    assert.deepEqual(offences(rule, new Map([['src/app/prose.js', `// ${line}\n`]])), [], line);

    // ... and then the tree itself.
    const files = sourceFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]);
    assert.deepEqual(offences(rule, new Map(files)), [], `${rule.name}: see the allow-list above`);
  });
}

/** A line of code the rule's own pattern matches. */
function control(rule) {
  switch (rule.name) {
    case RULES[0].name:
      return "SecureStore.setItemAsync('k', 'v')";
    case RULES[1].name:
      return 's.reveal()';
    case RULES[2].name:
      return "const KEY = 'fugaro.account.'";
    default:
      return "console.log('x')";
  }
}

// **Rule 5: the app imports nothing from the CLI or the console.** D125 makes
// the app its own artifact, and D119 gives it exactly one sibling it may reach:
// the top-level pure-logic package `shared/`. An import that resolves anywhere
// else outside `mobile/` -- `console/`, `internal/`, `cmd/`, `website/` -- is
// the build coupling D119 and D125 rule out, so every relative or absolute
// specifier is resolved and judged by where it lands.

/** Every module specifier a file's code names: static imports, re-exports,
 * side-effect imports, dynamic imports and `require`. */
function specifiers(text) {
  const src = code(text);
  const found = [];
  for (const re of [
    /\b(?:import|export)\s[^'"`;]*?\bfrom\s*['"]([^'"]+)['"]/g,
    /\bimport\s*['"]([^'"]+)['"]/g,
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
    /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  ]) {
    for (const match of src.matchAll(re)) found.push(match[1]);
  }
  return found;
}

/** The specifiers in `file` (app-relative) that leave `mobile/` for anywhere
 * but `shared/`. */
function escapes(file, text) {
  const repo = join(ROOT, '..');
  const bad = [];
  for (const spec of specifiers(text)) {
    const isPath = spec.startsWith('.') || spec.startsWith('/');
    if (!isPath) continue; // a package name resolves into node_modules
    const target = resolve(join(ROOT, dirname(file)), spec);
    const inApp = target === ROOT || target.startsWith(ROOT + sep);
    const sharedDir = join(repo, 'shared');
    const inShared = target === sharedDir || target.startsWith(sharedDir + sep);
    if (!inApp && !inShared) bad.push(`${file}: ${spec}`);
  }
  return bad;
}

test('the app imports nothing outside mobile/ but the shared package (D119, D125)', () => {
  // Positive controls: each forbidden shape is flagged, in each import form.
  for (const line of [
    "import { x } from '../console/src/main.js';",
    "export { x } from '../../internal/cli/x.js';",
    "import '../cmd/fugaro/main.js';",
    "const m = await import('../website/x.js');",
    "const m = require('/etc/x.js');",
  ]) {
    assert.deepEqual(escapes('index.js', `${line}\n`), [`index.js: ${line.match(/['"]([^'"]+)['"]/)[1]}`], line);
  }
  // ... and the allowed ones are not: the app's own tree, the shared package,
  // a package name, and a forbidden path that is only prose.
  assert.deepEqual(
    escapes(
      'src/accounts/x.js',
      "import a from './account.js';\nimport b from '../../../shared/url/index.js';\nimport c from 'expo';\n// import d from '../../../console/x.js';\n",
    ),
    [],
  );

  // Test files are scanned too (a test that imports the console couples the
  // app's CI to it just the same); this file exempts itself, once, because
  // its controls above are the forbidden specifiers written as strings.
  const offenders = sourceFiles()
    .filter((file) => file !== SELF)
    .flatMap((file) => escapes(file, readFileSync(join(ROOT, file), 'utf8')));
  assert.deepEqual(offenders, [], 'an import leaves the app for a sibling other than shared/');
});

// **Rule 6: the manifest declares no provider auth SDK.** D221 and D234 keep
// the provider half out of v1: a device holds no provider session, the account
// model stays D114's, and no per-project Firebase app instance exists. The
// dependency that would give the app one is the one thing that could hold it
// without a line of this app changing, so the rule is the plan's own sentence
// read literally -- `mobile/package.json` gains **no** Firebase, Google, Apple
// or Microsoft **auth** dependency (docs/mobile-auth, Task P4) -- and it is
// here rather than in `dependencies.test.js` because it is a boundary: a
// package that authenticates is a package with something to keep.
//
// **Why the three `@react-native-firebase` packages are not refused.** The
// manifest declares `app`, `analytics` and `crashlytics` today (P7.1a's
// telemetry), none of them carries a sign-in surface, and the rule is about
// auth rather than about the word Firebase. What it refuses is the shape of an
// auth SDK -- an `auth`, `authentication`, `oauth`, `msal`, `identity`,
// `sign-in` or `log-in` name, in whatever spelling separates the words -- plus
// the bare `firebase` package, whose `signInWithCredential` is the spec's step
// 4 in one call.
//
// **Every group a build installs, not just `dependencies`.** `npm ci` writes
// `dependencies`, `optionalDependencies` and `peerDependencies` into
// `node_modules`, so each of them is a way to ship a package the app holds
// without a line of this app changing -- and the first review of this rule
// found `optionalDependencies` open (the rule read `dependencies` alone, and
// nothing else in the tree reads that group either). `devDependencies` is the
// one that is not scanned: a production install (`npm ci --omit=dev`, and the
// install a build machine may make) does not write it, so a package there never
// reaches the binary and cannot hold a session in it.
//
// **Why the name is folded first.** A provider's name is spelled as its package
// spells it: the same Google package is `expo-google-sign-in`,
// `google_sign_in` and `.../google-signin`, and the second review of this rule
// found the hyphenated ones walking through a pattern that required a delimiter
// after its token. Folding every separator but `-` to `-` makes one spelling of
// the name for the pattern to judge, and the pattern then reads the words
// rather than an enumeration of the packages that happen to be named today.
//
// **Why the alias target is read too.** A key is not always the name that lands
// in `node_modules`: an npm alias installs one package under another name --
// `"fb": "npm:@react-native-firebase/auth@27.1.0"` puts the Firebase auth SDK
// in `node_modules/fb` -- and the third review of this rule found exactly that
// walking through a check that read keys alone, while the SDK it installs is
// the one the rule exists to refuse. So a version that is an alias is read for
// the package it points at, and the key and the target are judged by the same
// two patterns; a failure names both, because the alias is what hides it.
//
// **Why the specifier is read as well as the name.** A version that is not a
// registry range names a source for which the manifest spells no package at
// all: a tarball URL, a `git+ssh://` spec, `github:owner/repo`, `file:`/`link:`
// or a bare `owner/repo`. `"fb": "https://host/@react-native-firebase/auth/-/auth-27.1.0.tgz"`
// puts the Firebase auth SDK in `node_modules/fb` exactly as the alias above
// does -- the fourth review of this rule found it green -- and no name exists
// here for the two patterns to judge. Reading a package name out of a URL is a
// guess, and a dependency the rule cannot read is not one it can call clean, so
// it refuses the specifier instead. What a registry install is spelled with
// carries neither `/` nor `:`: a range (`^1.0.0`, `>=1.0.0 <2.0.0`, `1.x`,
// `*`) and a dist-tag (`latest`) all pass.

/** The dependency groups a build installs into the app, in the manifest's own
 * order: each one reaches `node_modules`, so each one is a dependency the app
 * really holds. */
const INSTALLED_GROUPS = Object.freeze(['dependencies', 'optionalDependencies', 'peerDependencies']);

/** An auth SDK, by the shape of its name, in the folded spelling `fold` makes.
 * The token's own separator is optional (`sign-?in`, so `signin` and `sign-in`
 * are one name) and a trailing number is part of it (`auth0`, `oauth2`), which
 * is what the packages carry. */
const PROVIDER_AUTH = /(?:^|-)(?:auth(?:entication|orization)?\d*|oauth\d*|msal\d*|identity\d*|sign-?in|log-?in)(?:$|-)/;

/** The Firebase SDK itself: what the spec's `signInWithCredential` needs. */
const FIREBASE_SDK = /^@?firebase(?:$|[/@_-])/;

/** One package name with every separator but `-` folded to it, so a spelling
 * that splits a provider's name differently is the same name here. */
function fold(name) {
  return name.toLowerCase().replace(/[@/._]+/g, '-');
}

/** The package an entry really installs: its key, or -- when the version is an
 * npm alias -- the name the alias points at. `npm:@scope/pkg@1.2.3` and
 * `npm:pkg` are both valid, and a scoped target keeps its own leading `@`, so
 * the version's separator is the LAST `@` after the first character. */
function installed(name, version) {
  if (typeof version !== 'string' || !version.startsWith('npm:')) return name;
  const target = version.slice('npm:'.length);
  const at = target.lastIndexOf('@');
  return at > 0 ? target.slice(0, at) : target;
}

/** A version that installs from somewhere other than the registry -- a tarball
 * URL, a `git+ssh://` or `github:owner/repo` spec, `file:`/`link:`, a bare
 * `owner/repo` -- and so puts a package in `node_modules` whose name the
 * manifest never spells. A registry range and a dist-tag carry neither `/` nor
 * `:` and an `npm:` alias carries its own target instead (read by `installed`),
 * so those two characters are the whole of the test. */
function offRegistry(version) {
  return typeof version === 'string' && !version.startsWith('npm:') && /[/:]/.test(version);
}

/** The declared dependencies that are a provider auth SDK, sorted, each named
 * with the group it sits in -- so a failure says where to look. An alias is
 * judged by the package it installs and reported with both names: the alias is
 * what would hide it. A version from another source is refused on the spec
 * alone, since the name to judge is not in the manifest. */
function providerAuth(manifest) {
  const found = [];
  for (const group of INSTALLED_GROUPS) {
    for (const [name, version] of Object.entries(manifest[group] ?? {})) {
      if (offRegistry(version)) {
        found.push(`${group}: ${name} -> ${version}`);
        continue;
      }
      const target = installed(name, version);
      if (PROVIDER_AUTH.test(fold(target)) || FIREBASE_SDK.test(target)) {
        found.push(target === name ? `${group}: ${name}` : `${group}: ${name} -> ${target}`);
      }
    }
  }
  return found.sort();
}

test('the manifest declares no provider auth SDK (D221, D234)', () => {
  // The positive control first: every provider the spec's table names, the
  // Firebase SDK that would sign the phone in, and the spellings the two
  // reviews of this rule found walking through it -- so a green run below is
  // about this manifest and not about a pattern that matches nothing.
  for (const name of [
    '@react-native-firebase/auth',
    '@firebase/auth',
    'firebase',
    'expo-auth-session',
    'expo-apple-authentication',
    '@react-native-google-signin/google-signin',
    'react-native-app-auth',
    '@azure/msal-react',
    'amazon-cognito-identity-js',
    'expo-google-sign-in',
    'google_sign_in',
    'react-native-auth0',
    'oauth2-client',
  ]) {
    assert.deepEqual(providerAuth({ dependencies: { [name]: '1.0.0' } }), [`dependencies: ${name}`], name);
  }
  // ... and an npm alias, which installs the SDK under a key of the author's
  // choosing: the name the alias points at is what is judged, and a failure
  // names both, because the alias is what would hide it. Mutations: read the
  // key alone (the reviewed defect) and these go red.
  assert.deepEqual(
    providerAuth({ dependencies: { fb: 'npm:@react-native-firebase/auth@27.1.0' } }),
    ['dependencies: fb -> @react-native-firebase/auth'],
  );
  // ... in any installed group, for the bare SDK as much as for a scoped auth
  // package, and whatever the version is spelled as (`npm:pkg`, no version at
  // all, is the same shape).
  assert.deepEqual(
    providerAuth({ optionalDependencies: { analytics: 'npm:firebase@10.0.0' } }),
    ['optionalDependencies: analytics -> firebase'],
    'a group that is scanned for a key is scanned for the alias target too',
  );
  assert.deepEqual(
    providerAuth({ peerDependencies: { auth0: 'npm:react-native-auth0@^3.0.0' } }),
    ['peerDependencies: auth0 -> react-native-auth0'],
  );
  // ... and an alias to a package that is not auth is not an offence, so the
  // rule is still about a session rather than about the word npm.
  assert.deepEqual(
    providerAuth({ dependencies: { store: 'npm:expo-secure-store@14.0.0', store2: 'npm:expo-secure-store' } }),
    [],
  );
  // ... and a version that installs from another source is refused on the spec
  // alone, whatever its key: a tarball URL or a git spec puts a package in
  // `node_modules` that this manifest never names, and a guess at the name
  // inside a URL is the thing the rule declines to make. Mutations: read the
  // key alone (the reviewed defect) and the first goes red, judge by the name
  // patterns only and both do.
  assert.deepEqual(
    providerAuth({
      dependencies: { fbx: 'https://registry.npmjs.org/@react-native-firebase/auth/-/auth-27.1.0.tgz' },
    }),
    ['dependencies: fbx -> https://registry.npmjs.org/@react-native-firebase/auth/-/auth-27.1.0.tgz'],
  );
  assert.deepEqual(
    providerAuth({ dependencies: { gx: 'github:invertase/react-native-firebase' } }),
    ['dependencies: gx -> github:invertase/react-native-firebase'],
  );
  // ... and every other spelling of "somewhere else" is the same shape, in each
  // of the groups a build installs: a `git+ssh://` spec, a bare `owner/repo`,
  // and the two local ones.
  for (const spec of [
    'git+ssh://git@github.com/invertase/react-native-firebase.git',
    'git://github.com/invertase/react-native-firebase.git',
    'invertase/react-native-firebase',
    'file:../firebase-auth',
    'link:../firebase-auth',
  ]) {
    for (const group of INSTALLED_GROUPS) {
      assert.deepEqual(
        providerAuth({ [group]: { fb: spec } }),
        [`${group}: fb -> ${spec}`],
        `${group}: ${spec}`,
      );
    }
  }
  // ... while a registry install spelled as a range or a dist-tag is not a
  // source at all: the rule refuses where a dependency comes from, so it must
  // not refuse how a registry version is written.
  assert.deepEqual(
    providerAuth({
      dependencies: {
        caret: '^1.0.0',
        tilde: '~1.2.3',
        compound: '>=1.0.0 <2.0.0',
        wildcard: '1.x',
        any: '*',
        tagged: 'latest',
        prerelease: '2.0.0-beta.1',
      },
    }),
    [],
  );
  // ... and the groups themselves are pinned against a written list: the loop
  // below moves with the constant it checks, and a loop that moves with what it
  // checks is exactly how `optionalDependencies` was left out of this rule in
  // the first place. The three are the groups `npm ci` writes into
  // `node_modules`.
  assert.deepEqual(
    [...INSTALLED_GROUPS],
    ['dependencies', 'optionalDependencies', 'peerDependencies'],
    'every group a build installs is scanned',
  );
  // ... and each of them is scanned, not `dependencies` alone:
  // `optionalDependencies` reaches the app's `node_modules` exactly as
  // `dependencies` does. Mutation: drop a group from `INSTALLED_GROUPS` and the
  // pin above goes red with it.
  for (const group of INSTALLED_GROUPS) {
    assert.deepEqual(
      providerAuth({ [group]: { '@react-native-firebase/auth': '27.1.0' } }),
      [`${group}: @react-native-firebase/auth`],
      group,
    );
  }
  // ... and `devDependencies` is deliberately not one of them (the header's
  // reason): a production install does not write it, so a package there is not
  // one the built app can hold.
  assert.deepEqual(
    providerAuth({ devDependencies: { '@react-native-firebase/auth': '27.1.0' } }),
    [],
    'devDependencies never reaches the bundle',
  );
  // ... and the manifest's own Firebase packages are not auth: refusing them
  // would refuse the telemetry the app has, and the rule would then be about a
  // word rather than about a session.
  assert.deepEqual(
    providerAuth({
      dependencies: {
        '@react-native-firebase/app': '27.1.0',
        '@react-native-firebase/analytics': '27.1.0',
        '@react-native-firebase/crashlytics': '27.1.0',
      },
    }),
    [],
  );
  // ... and the manifest itself. Mutation: add any name above to any group in
  // `INSTALLED_GROUPS` of `mobile/package.json` and this fails naming it, with
  // the group it was found in.
  const manifest = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'));
  assert.deepEqual(
    providerAuth(manifest),
    [],
    'D221: no provider session reaches a project, so the app declares no provider auth SDK',
  );
});
