// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The one-write-path rule, ENFORCED rather than observed
// (mobile plan, Task P3.6; D113, D113b, D119, D125; and the review of PR #604,
// which asked for this scan by name).
//
// **Why a scan, and not another test of the client.** "The app writes to the
// database in exactly one place" is not a property any test of the client can
// hold. The client can be perfect and a screen four tasks from now can still
// call the store itself -- with its own `at`, its own id rule, and no MAC,
// because the key is reached through `SecretStore.use` and a second caller has
// no reason to know that. The claim that holds is the same shape P3.1's
// `boundary.test.js` uses for the credentials: a token that may appear in a
// named list of files and nowhere else, asserted over the app's source, with a
// positive control so a mistyped pattern cannot pass for a clean tree.
//
// **The rules, and what each is for.**
//
//   1. `.write(` -- the store port's mutating call (`store.js` is the port: one
//      `write(path, value)`, one `read(path)`). `client.js` is the only file
//      that may call it. This is D113's two paths and D113b's "the heartbeat is
//      the orchestrator's write": a second caller is a second write set, and no
//      test of `client.js` would notice one.
//   2. A `firebase` module specifier -- the Realtime Database SDK. It belongs
//      to `mobile/index.js`, the entry that is allowed to know what a device
//      is and builds the port the client is handed. A screen that imported it
//      could address any node in the tree.
//   3. `.ref(` -- the SDK's own way to name a path, and the receiver every
//      mutating SDK method hangs off (`set`, `update`, `push`, `remove`,
//      `transaction`). Rule 2 catches the import; this catches the path, which
//      is the thing the rules' `$mb == auth.uid` fence is about. Nothing in the
//      tree names one, and the adapter that will is `index.js`.
//
// **What this scan does NOT claim.** It is about MOBILE's source. The consumer
// (Go) writes the outbox, the heartbeat and everything else; the rules in
// `internal/budget/rules/rules.json.tmpl` are what actually fence this, and they
// are checks 81/82, owner-run. This file is the app holding its own end of that
// fence -- the half a test can hold.
//
// Like its P3.1 sibling, the scan reads CODE and not prose (the comment stripper
// below), it exempts test files and itself by name, and every rule is proved
// against a planted line in real code before the tree is judged.

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import test from 'node:test';

const ROOT = join(import.meta.dirname, '..', '..');
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

/** The code of a file, without its comments (P3.1's `boundary.test.js` rule:
 * the prose in this tree names the very tokens the rules forbid). */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/**
 * The rules. `allow` lists the production files the token may appear in; test
 * files are always allowed (a fake must be able to call the method it stands in
 * for) and so is this file.
 */
const RULES = [
  {
    name: 'the store is written in one file',
    pattern: /\.write\s*\(/,
    allow: ['src/mailbox/client.js'],
    control: "await store.write('mailbox/u/inbox/m-1', record);",
    why: 'D113: the app writes its own inbox message and its own ack, and nothing else',
  },
  {
    name: 'the database SDK is imported in one file',
    pattern: /['"]@?firebase\/[a-z-]+['"]/,
    allow: ['index.js'],
    control: "import { getDatabase } from 'firebase/database';",
    why: 'D125: the entry wires the port; a screen that imported the SDK could address any node',
  },
  {
    name: 'no path is named through the SDK',
    pattern: /\.ref\s*\(/,
    allow: ['index.js'],
    control: "db.ref('mailbox/u/outbox').remove();",
    why: 'the path is what the rules fence on; only the adapter may compose one',
  },
];

/** Apply one rule to a map of path -> text, returning the offences. */
function offences(rule, files) {
  return [...files]
    .filter(([file]) => file !== SELF && !isTest(file) && !rule.allow.includes(file))
    .filter(([, text]) => rule.pattern.test(code(text)))
    .map(([file]) => file);
}

test('the scan walks the app, and not an empty list', () => {
  const files = sourceFiles();
  assert.ok(files.length >= 10, `expected the app's source, found ${files.length} files`);
  for (const expected of [
    'index.js',
    'src/mailbox/client.js',
    'src/mailbox/store.js',
    'src/app/root.js',
  ]) {
    assert.ok(files.includes(expected), `${expected} is in the walk`);
  }
  assert.ok(files.includes(SELF), 'this file is in the walk, which is why it exempts itself');
  assert.equal(files.some((file) => file.startsWith('node_modules/')), false);
});

for (const rule of RULES) {
  test(`${rule.name} — ${rule.why}`, () => {
    // The positive control first: the rule must flag a planted line, in real
    // code and in each of the import shapes the SDK can be reached through, or
    // a green run below would mean nothing.
    const example = { 'src/app/offender.js': `function offence() { ${rule.control} }\n` };
    assert.deepEqual(offences(rule, new Map(Object.entries(example))), ['src/app/offender.js'], rule.control);
    // The planted line is not flagged when it is only prose.
    assert.deepEqual(offences(rule, new Map([['src/app/prose.js', `// ${rule.control}\n`]])), [], rule.control);

    // ... and then the tree itself.
    const files = sourceFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]);
    assert.deepEqual(offences(rule, new Map(files)), [], `${rule.name}: see the allow-list above`);
  });
}

test('the scan is not vacuous: the writer itself matches the rule it is exempt from', () => {
  // An allow-list is only honest when the allowed file really does contain the
  // offence. `client.js` must be flagged when its exemption is removed --
  // otherwise the rule matches nothing anywhere and the three green runs above
  // would be three tests about a pattern that no longer exists.
  const files = sourceFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]);
  const [writer] = RULES;
  const offenders = offences({ ...writer, allow: [] }, new Map(files));
  assert.deepEqual(offenders, ['src/mailbox/client.js'], 'the store is written by exactly this file');
  // And the two SDK rules match nothing in the tree at all, which is why their
  // allow-list (`index.js`, the entry that will wire the adapter) is a promise
  // about a file that does not do it yet.
  for (const rule of RULES.slice(1)) {
    assert.deepEqual(offences({ ...rule, allow: [] }, new Map(files)), [], rule.name);
  }
});
