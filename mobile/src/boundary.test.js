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
