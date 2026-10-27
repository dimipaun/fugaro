// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The voice session's door, and the scan that keeps it what it is
// (voice-session plan, Task VS0.3; D205, D206, F24, F9, D213, D113, D117).
//
// Two rules, and they are the two halves of "a pure reducer, over the spec's
// event stream": the door's exports are a **named list asserted against a
// literal**, so the controller VS1.1 adds -- or a native port arriving early,
// or a helper that reaches a device -- is an edit to this file rather than an
// import somewhere; and the directory's **code** is scanned for the things
// this module must not have: a package, a clock, a random source, a timer, the
// network, a device API, a file, a database, a buffer of samples, a log line,
// an environment variable.
//
// **Why a scan and not a rule.** Nobody can write a test that says "no file
// here will grow a clock" or "this module will never read the Keychain". What
// can be written is a test that says the tokens such a change would need do not
// appear here today, with a positive control proving the pattern matches real
// code -- so the change has to be deliberate and visible in a diff. That is
// what D205's "the machine is a function" needs in order to survive contact
// with VS1.1.
//
// **The self-exemption, the controls, and the comments.** This file names every
// forbidden token as a pattern and as a planted example, so it exempts itself
// by name, once. The controls are asserted before the tree: without them a typo
// in a pattern would turn the file green while checking nothing. And the scan
// reads each file's *code* (`code()` below), because these files' headers
// discuss the very tokens the rules forbid -- the prose is the explanation, and
// a rule that forbade its own explanation would be a rule nobody could document.
//
// **The two fences this task adds, and why they are here rather than in P3.4's
// scan.** F24 is about a paused session: the secure store is unreadable while
// the phone is locked, so a session that reached a credential on resume would
// fail on exactly the phone call it exists for. F9/D213 is about what the
// session may carry: the Live Activity and the notification are updated with a
// handful of scalars, never words the user said. Neither is a rule the audio
// or voice scans could state -- they do not know what a session is -- so they
// are asserted here, over this directory, where a session is the only thing
// there is.
//
// **There is no README rule table for this module**, and that is deliberate:
// VS0.3's Files line is `index.js` and `index.test.js`, and the rules below are
// P3.4's and P3.5's tokens verbatim (plus the file rule and the two fences), so
// a third table in `mobile/README.md` would be a third copy of the same list to
// keep in step. The tables that exist are compared to the scans that run, by
// `src/audio/index.test.js` and `src/voice/index.test.js`.

import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import test from 'node:test';

import * as events from './events.js';
import * as session from './index.js';
import * as machine from './machine.js';

const DIR = import.meta.dirname;
/** `mobile/`, which is what a relative import of this module must stay inside. */
const MOBILE = join(DIR, '..', '..');
/** The repository's `shared/`, the one sibling D119 lets the app reach. */
const SHARED = join(MOBILE, '..', 'shared');

/** `mobile/src/accounts/`, which is where the credential lives (F24). */
const ACCOUNTS = join(MOBILE, 'src', 'accounts');
/** `mobile/src/mailbox/`, which reads the credential into a network client (F24). */
const MAILBOX = join(MOBILE, 'src', 'mailbox');
const SELF = 'index.test.js';

/**
 * Every `.js` file of the module, **at any depth**, sorted, as this directory's
 * relative paths with `/` separators.
 *
 * Recursive on purpose, and the recursion is not a detail: a non-recursive walk
 * is a fence one directory deep. `src/voice-session/native/port.js` would never
 * enter the file list, the token rules or the no-package scan -- the native
 * module arriving before VS1.2, which is the one thing this file exists to
 * refuse. `src/audio/index.test.js` and `src/voice/index.test.js` walk the same
 * way and for the same reason.
 */
function sessionFiles(dir = DIR, prefix = '') {
  const found = [];
  for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      found.push(...sessionFiles(path, `${prefix}${entry.name}/`));
    } else if (entry.name.endsWith('.js')) {
      found.push(`${prefix}${entry.name}`);
    }
  }
  return found.sort();
}

const FILES = sessionFiles();

const isTest = (file) => file.endsWith('.test.js');

test('the walk is recursive, so a file in a subdirectory is part of the module', () => {
  // The control for the fence above, and the reason it is a test rather than a
  // comment: with a non-recursive walk every assertion below still passes today
  // -- there is no subdirectory yet -- so nothing else in this file can tell
  // the two walks apart.
  const root = mkdtempSync(join(tmpdir(), 'fugaro-session-'));
  try {
    writeFileSync(join(root, 'top.js'), 'export const top = 1;\n');
    mkdirSync(join(root, 'native', 'deep'), { recursive: true });
    writeFileSync(join(root, 'native', 'port.js'), "import Voice from 'react-native-voice';\n");
    writeFileSync(join(root, 'native', 'deep', 'codec.js'), 'export const codec = 1;\n');
    writeFileSync(join(root, 'native', 'notes.txt'), 'not a module\n');
    assert.deepEqual(sessionFiles(root), ['native/deep/codec.js', 'native/port.js', 'top.js'], 'at every depth');
    // ... and a nested file is scanned exactly like a sibling: the package
    // import that was invisible to the old walk is an offence to the rule.
    assert.deepEqual(
      production(root).map(([file]) => file),
      ['native/deep/codec.js', 'native/port.js', 'top.js'],
    );
    assert.deepEqual(
      strangers('native/port.js', readFileSync(join(root, 'native', 'port.js'), 'utf8'), root),
      ['native/port.js: react-native-voice'],
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('the module is exactly these files, at every depth', () => {
  // A new file here is a new thing the app can import, so it is a deliberate
  // edit to this list. That is what makes "the machine and the events, and
  // nothing else" checkable rather than a claim in a header: the session
  // controller arrives in VS1.1 as `session.js` plus `session.test.js`, and
  // this fails until that is written down -- at any depth, which is why the
  // walk is recursive.
  assert.deepEqual(FILES, [
    'events.js',
    'events.test.js',
    'index.js',
    'index.test.js',
    'machine.js',
    'machine.test.js',
  ]);
});

test('the door exports exactly this, and every name is the machine or the contract', () => {
  assert.deepEqual(Object.keys(session).sort(), [
    'AUDIO_ROUTES',
    'END_REASONS',
    'INITIAL_SESSION_STATE',
    'INTERRUPTION_SOURCES',
    'MACHINE_CODES',
    'MACHINE_EVENTS',
    'PAUSE_REASONS',
    'REMOTE_ACTIONS',
    'SERVICE_STOP_REASONS',
    'SESSION_EVENTS',
    'SESSION_EVENT_FIELDS',
    'SESSION_STATES',
    'isVoiceSessionEvent',
    'reduce',
  ]);
  // There is no class on this door, and that is the check for "is this a
  // native module?": a port would be a thing to construct, and P3.4's three
  // fakes are the only constructible objects the app has.
  const classes = Object.entries(session)
    .filter(([, value]) => typeof value === 'function' && /^class\b/.test(Function.prototype.toString.call(value)))
    .map(([name]) => name);
  assert.deepEqual(classes, []);
  // ... and every object this door hands out is frozen, so a caller cannot add
  // a seventh kind, a ninth state or an end reason to the module it imported.
  for (const [name, value] of Object.entries(session)) {
    if (value !== null && typeof value === 'object') assert.equal(Object.isFrozen(value), true, `${name} is frozen`);
  }
});

test('the door carries every export of the two files, unchanged, and nothing of its own', () => {
  // The other direction of the pin above: a constant added to `events.js` and
  // not re-exported is invisible to the app, which then imports the file
  // directly -- the second door this module exists to not have.
  const inner = { ...events, ...machine };
  assert.deepEqual(Object.keys(session).sort(), Object.keys(inner).sort());
  for (const [name, value] of Object.entries(inner)) assert.equal(session[name], value, name);
});

/**
 * The rules. Each is a name, the **tokens** it forbids, why it is here, and what
 * a token would mean.
 *
 * The tokens are the rule, and the pattern is built from them
 * (`forbiddenPattern`): a rule is "these words do not appear in this module's
 * code", and a regex written out beside them is a second copy of the same fact
 * that can drift from the first. They are the same tokens `src/audio/index.js`
 * and `src/voice/index.js` are scanned with -- a session is downstream of both,
 * so it may not have what they may not have -- plus `no file access`, which the
 * plan names for this task.
 *
 * The tokens are deliberately broad (`Date` rather than `Date.now`): the point
 * is that the *capability* is absent, and a capability is absent only if every
 * way of reaching it is.
 */
const RULES = [
  {
    name: 'no clock',
    tokens: ['Date', 'performance.now', 'hrtime'],
    sample: 'const at = Date.now()',
    why: 'D117: the clock is `ctx.now`, the server-anchored one the caller injects, so this module reads none',
  },
  {
    name: 'no random source',
    tokens: ['Math.random', 'crypto'],
    sample: 'const id = Math.random()',
    why: 'nothing here is identified or ordered by chance, and a random source would be one no test could pin',
  },
  {
    name: 'no timer',
    tokens: ['setTimeout', 'setInterval', 'setImmediate', 'requestAnimationFrame'],
    sample: 'setTimeout(() => tick(), 300)',
    why: '§8\'s three minutes is a `tick` the caller injects: a timer here would be a clock the machine owns and no test can move',
  },
  {
    name: 'no network',
    tokens: ['fetch(', 'WebSocket', 'XMLHttpRequest', 'EventSource'],
    sample: "fetch('https://example.invalid/session')",
    why: 'the session is driven by events and time; a request from here would be a second write path',
  },
  {
    name: 'no device API',
    tokens: ['getUserMedia', 'MediaRecorder', 'AudioContext', 'navigator', 'Permissions'],
    sample: 'navigator.mediaDevices.getUserMedia({ audio: true })',
    why: 'D122: the microphone is reached through P3.4\'s pipeline, and the OS permission through whoever implements the port -- never here',
  },
  {
    name: 'no file access',
    tokens: ['readFileSync', 'writeFileSync', 'readFile(', 'writeFile(', 'node:fs'],
    sample: "const text = readFileSync('/tmp/session', 'utf8')",
    why: 'the machine is a function of its state, its event and its ctx: a file would be state it cannot be handed in a test',
  },
  {
    name: 'no database of its own',
    tokens: ['firebase', 'Firestore', 'getDatabase', 'reference(', 'AsyncStorage'],
    sample: 'const db = getDatabase(app)',
    why: 'D113: the mailbox client is the app\'s only write path, and a session that could reach a database could be a second one',
  },
  {
    name: 'no samples, in any encoding',
    tokens: ['pcm', 'samples', 'ArrayBuffer', 'Float32Array', 'Uint8Array', 'base64', 'Blob'],
    sample: 'const chunk = new Float32Array(1024)',
    why: 'D118/F26: text crosses the ports and audio never does, so there is no buffer here to leak one through',
  },
  {
    name: 'no logging',
    tokens: ['console.'],
    sample: "console.log('the session paused')",
    why: 'a transcript in a log line is a transcript outside the mailbox (D118), and the app logs nothing',
  },
  {
    name: 'nothing is read from the environment',
    tokens: ['process.env'],
    sample: 'const timeout = process.env.RESUME_TIMEOUT',
    why: 'D201 is an open decision, and it is answered by the caller\'s `resumeTimeoutMs` -- not by a variable no test can see',
  },
];

/**
 * The pattern a rule's tokens make: each token escaped, and word-bounded where
 * it begins or ends with a word character, joined as alternatives, and matched
 * case-insensitively.
 *
 * The bounding is why this is derived rather than written: `fetch(` has to match
 * `fetch(` and not `myfetch(`, and a trailing `\b` after a `(` would match
 * nothing at all, because there is no word boundary between `(` and the quote
 * that follows it in real code.
 *
 * @param {string[]} tokens the rule's forbidden tokens
 * @returns {RegExp}
 */
function forbiddenPattern(tokens) {
  const bounded = (token) => {
    const escaped = token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return `${/^\w/.test(token) ? '\\b' : ''}${escaped}${/\w$/.test(token) ? '\\b' : ''}`;
  };
  return new RegExp(tokens.map(bounded).join('|'), 'i');
}

/**
 * A file's code, without its comments. The same helper `boundary.test.js` uses,
 * and for the same reason: the rules are about code paths, and this tree's
 * headers explain the rules in prose. Stated limitation: it is not a parser, so
 * a `//` inside a string literal would strip the rest of the line. No file here
 * has one, and the positive control below proves each pattern still matches
 * real code.
 */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/** The production files of this module -- at any depth -- as path -> code. */
function production(dir = DIR) {
  return sessionFiles(dir)
    .filter((file) => !isTest(file) && file !== SELF)
    .map((file) => [file, code(readFileSync(join(dir, file), 'utf8'))]);
}

/** Every file of this module but this one -- tests included -- as path -> code. */
function scanned(dir = DIR) {
  return sessionFiles(dir)
    .filter((file) => file !== SELF)
    .map((file) => [file, code(readFileSync(join(dir, file), 'utf8'))]);
}

test('the scan reads the module, and not an empty list', () => {
  const files = production();
  assert.deepEqual(files.map(([file]) => file), ['events.js', 'index.js', 'machine.js']);
  for (const [file, text] of files) {
    assert.ok(text.includes('export'), `${file} is real code, not a comment`);
  }
  // The fences below read the tests too, and they are not a smaller list: the
  // tests are where a fixture that reaches a credential would be written.
  assert.deepEqual(scanned().map(([file]) => file), [
    'events.js',
    'events.test.js',
    'index.js',
    'machine.js',
    'machine.test.js',
  ]);
});

for (const rule of RULES) {
  test(`${rule.name} — ${rule.why}`, () => {
    const pattern = forbiddenPattern(rule.tokens);
    // The positive control: the rule's pattern must match the line it claims to
    // forbid, in real code.
    assert.equal(pattern.test(code(`function offence() { ${rule.sample}; }\n`)), true, rule.sample);
    // ... and must not match prose, which is what lets these files explain
    // themselves.
    assert.equal(pattern.test(code(`// ${rule.sample} is forbidden here\n`)), false, rule.sample);
    // ... and then the module itself.
    const offenders = production().filter(([, text]) => pattern.test(text)).map(([file]) => file);
    assert.deepEqual(offenders, [], `${rule.name}: ${rule.why}`);
  });
}

// **The last rule is about imports, and it is not the same rule as
// `boundary.test.js`'s.** That scan resolves *path* specifiers and catches an
// import that leaves `mobile/` for a sibling other than `shared/`; it says
// nothing about `import Voice from 'react-native-voice'`, which is a package
// name and resolves into `node_modules`. For a directory whose claim is "the
// session is a pure function", that is the import that matters most, so it is
// checked here: **no file here imports any package at all**, and a relative
// import stays inside `mobile/` or `shared/`.
//
// That is `src/voice/index.test.js`'s rule rather than `src/audio/index.js`'s
// narrower one, and the plan says why: this module's tests drive P3.4's real
// pipeline (`listening ⇄ speaking` is D206's whole point), and VS1's controller
// sits above `src/audio/`, `src/voice/` and this directory at once.

/** Every module specifier a file's code names: static imports, re-exports,
 * side-effect imports, dynamic imports and `require`. */
function specifiers(text) {
  const found = [];
  for (const re of [
    /\b(?:import|export)\s[^'"`;]*?\bfrom\s*['"]([^'"]+)['"]/g,
    /\bimport\s*['"]([^'"]+)['"]/g,
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
    /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  ]) {
    for (const match of text.matchAll(re)) found.push(match[1]);
  }
  return found;
}

/**
 * The specifiers in `file` (relative to `dir`) that are not `mobile/` or
 * `shared/`. `dir` is a parameter because the walk is tested against a
 * temporary tree, where the module's root is not this directory; `node` is a
 * parameter because a test may use `node:` builtins and production code may not.
 */
function strangers(file, text, dir = DIR, { node = false } = {}) {
  return specifiers(code(text))
    .filter((spec) => {
      if (spec.startsWith('node:')) return !node; // a test's own tooling, and only a test's
      if (!spec.startsWith('.')) return true; // a package name: the offence this rule exists for
      const target = resolve(join(dir, dirname(file)), spec);
      const inside = (root) => target === root || target.startsWith(root + sep);
      return !inside(MOBILE) && !inside(SHARED);
    })
    .map((spec) => `${file}: ${spec}`);
}

test('the module imports the app and shared/, and no package at all', () => {
  // Positive controls: the shapes this rule exists to catch. The two assembled
  // paths are written that way because a literal path outside the app would be
  // an offence to `boundary.test.js` -- which scans this file too, and is right
  // to: a control is a string, but the scanner cannot know that.
  const intoTheCli = `${'../../../con'}sole/src/main.js`;
  const outsideTheApp = `${'../../../../else'}where/db.js`;
  for (const line of [
    "import Voice from 'react-native-voice';",
    "import { Audio } from 'expo-av';",
    "const port = require('react-native-voice');",
    "const sdk = await import('@google-cloud/speech');",
    `import db from '${outsideTheApp}';`,
    `import c from '${intoTheCli}';`,
  ]) {
    assert.deepEqual(strangers('index.js', `${line}\n`), [`index.js: ${line.match(/['"]([^'"]+)['"]/)[1]}`], line);
  }
  // ... and the allowed shapes are not flagged: a sibling file of this module,
  // P3.4's pipeline (which D206 requires the tests to drive), the shared
  // package, a `node:` builtin where a test may use one, and a package name
  // that is only prose.
  assert.deepEqual(
    strangers(
      'machine.test.js',
      "import { reduce } from './machine.js';\nimport { FakeTts, createVoicePipeline } from '../audio/index.js';\nimport { REFUSALS } from '../../../shared/mailbox-schema/index.js';\nimport test from 'node:test';\n// import Voice from 'react-native-voice';\n",
      DIR,
      { node: true },
    ),
    [],
  );
  // ... and `node:` is an offence in production code, which is what the flag is.
  assert.deepEqual(strangers('machine.js', "import { readFileSync } from 'node:fs';\n"), ['machine.js: node:fs']);

  // Production code: no import may leave `mobile/` and `shared/`.
  assert.deepEqual(
    production().flatMap(([file, text]) => strangers(file, text)),
    [],
    'a package import here is a native port, a device or a database, arriving quietly',
  );
  // The tests may use `node:` builtins and the app's own files, and nothing
  // else (no test framework, no fixture library: `node --test` and `assert`).
  const tests = FILES.filter((file) => isTest(file) && file !== SELF);
  assert.deepEqual(
    tests.flatMap((file) => {
      const text = readFileSync(join(DIR, file), 'utf8');
      return specifiers(code(text))
        .filter((spec) => !spec.startsWith('node:') && !spec.startsWith('.'))
        .map((spec) => `${file}: ${spec}`);
    }),
    [],
    'a test that imports a package is a CI that needs one',
  );
  // ... and the tests' relative imports are real: `mobile/` and `shared/`.
  assert.deepEqual(tests.flatMap((file) => strangers(file, readFileSync(join(DIR, file), 'utf8'), DIR, { node: true })), []);
});

// **F24: a paused session touches no credential.** The secure store is
// unreadable while the phone is locked on iOS, and the pause this module exists
// for is a phone call -- so a session that read a credential on resume would
// fail on exactly the call it is for. The fence is at the directory and it
// includes the tests: a fixture that reached the secure store would be the same
// coupling, written where it is hardest to see.
//
// **Two paths to a credential, so three rules.** `src/accounts/` is the
// credential. `src/mailbox/` is the module that carries it into a network
// client -- `mailbox/client.js` imports `accounts/account.js`,
// `accounts/secret.js` and `accounts/secure-store.js`, and F24's own
// consequence is that a paused session issues no mailbox call -- so a fence
// that resolved only into `accounts/` would let `import { send } from
// '../mailbox/index.js'` through every scan in this file. Both roots are
// therefore blocked, in every scanned file; and **production code here imports
// nothing outside this directory at all**, the same rule in its strongest form
// and the one a new import trips first: the session's own files reach no
// neighbour, so there is no path to a credential left to argue about. The tests
// are held to the two roots instead, because D206 requires them to drive P3.4's
// real pipeline -- `../audio/` is the one neighbour they may reach, and it
// imports itself and `shared/` and no credential.

/**
 * The specifiers in `file` that resolve into one of `roots` -- the credential,
 * and the module that carries it (F24). A package name is `strangers`' business
 * and not this function's: what F24 forbids is the app's own secure store being
 * reached from a session.
 */
function credentials(file, text, dir = DIR, roots = [ACCOUNTS, MAILBOX]) {
  return specifiers(code(text))
    .filter((spec) => {
      if (!spec.startsWith('.')) return false;
      const target = resolve(join(dir, dirname(file)), spec);
      return roots.some((root) => target === root || target.startsWith(root + sep));
    })
    .map((spec) => `${file}: ${spec}`);
}

/**
 * The relative specifiers in `file` that leave this directory -- `../`, and not
 * `./`. Only production code is held to this one (see above).
 */
function leavesTheModule(file, text) {
  return specifiers(code(text))
    .filter((spec) => spec.startsWith('.') && !spec.startsWith('./'))
    .map((spec) => `${file}: ${spec}`);
}

test('nothing here reaches a credential, directly or through the mailbox (F24)', () => {
  // Positive controls: each import form that lands in `mobile/src/accounts/`,
  // including a nested file, whose path has one more `..`...
  assert.deepEqual(
    credentials('machine.js', "import { reveal } from '../accounts/secure-store.js';\n"),
    ['machine.js: ../accounts/secure-store.js'],
  );
  assert.deepEqual(
    credentials('events.js', "import { Secret } from '../accounts/secret.js';\n"),
    ['events.js: ../accounts/secret.js'],
  );
  assert.deepEqual(
    credentials('events.js', "const store = await import('../accounts/expo-secure-store.js');\n"),
    ['events.js: ../accounts/expo-secure-store.js'],
  );
  // ... and each form that lands in the module that carries one: a static
  // import of the door, of the client that makes the call, and a `require`.
  assert.deepEqual(credentials('machine.js', "import { send } from '../mailbox/index.js';\n"), [
    'machine.js: ../mailbox/index.js',
  ]);
  assert.deepEqual(credentials('events.js', "import { poll } from '../mailbox/client.js';\n"), [
    'events.js: ../mailbox/client.js',
  ]);
  assert.deepEqual(credentials('machine.js', "const { send } = require('../mailbox/index.js');\n"), [
    'machine.js: ../mailbox/index.js',
  ]);
  // ... and the neighbours are not flagged: a sibling file, the pipeline (which
  // imports itself and `shared/` and no credential), and the shared package.
  assert.deepEqual(
    credentials(
      'machine.js',
      "import { isVoiceSessionEvent } from './events.js';\nimport { isVoicePipeline } from '../audio/index.js';\nimport { REFUSALS } from '../../../shared/mailbox-schema/index.js';\n// import { reveal } from '../accounts/secure-store.js';\n",
    ),
    [],
  );

  // The production rule, and its control: the session's own code reaches
  // nothing outside this directory -- not the mailbox, not the store, not a
  // screen.
  assert.deepEqual(leavesTheModule('machine.js', "import { send } from '../mailbox/index.js';\n"), [
    'machine.js: ../mailbox/index.js',
  ]);
  assert.deepEqual(leavesTheModule('machine.js', "import { reduce } from './machine.js';\n"), []);

  // The tree: no specifier lands in either root, no production file leaves this
  // directory, and no file's code names either path at all -- a string path
  // handed to a dynamic import is the same coupling.
  assert.deepEqual(scanned().flatMap(([file, text]) => credentials(file, text)), []);
  assert.deepEqual(production().flatMap(([file, text]) => leavesTheModule(file, text)), []);
  assert.deepEqual(
    scanned()
      .filter(([, text]) => /\baccounts\//.test(text) || /\bmailbox\//.test(text))
      .map(([file]) => file),
    [],
  );
});

// **F9/D213: no file here carries words the user said.** The Live Activity and
// the Android notification are updated with the machine's state -- a state
// name, a boolean, a timestamp, a title (F25) -- and never with a transcript,
// which is what makes "the app logs nothing" and "audio and text never leave
// the ports" checkable from the outside. The token is pinned rather than merely
// forbidden, because two refusal controls in VS0.1's test legitimately name it:
// one asserts the validator refuses a `transcript` field, the other that the
// field map cannot be extended with one. A third appearance is an edit here.

test('no file here builds anything from words the user said (F9, D213)', () => {
  const pattern = /transcript/i;
  // The control: the shape this fence exists to catch, in real code...
  assert.equal(pattern.test(code('const line = `heard: ${transcript}`;\n')), true);
  assert.equal(pattern.test(code("const payload = { transcript: text };\n")), true);
  // ... and not in prose, which is how the contract explains what it cannot
  // carry.
  assert.equal(pattern.test(code('// never a transcript, and never audio\n')), false);

  // The production files carry it nowhere at all.
  assert.deepEqual(production().filter(([, text]) => pattern.test(text)).map(([file]) => file), []);
  // ... and in the tests it appears exactly twice, both of them refusals: a
  // field the validator must say no to, and a field the frozen map must refuse
  // to grow. Nothing here reads one, and nothing builds a string from one.
  assert.deepEqual(scanned().filter(([, text]) => pattern.test(text)).map(([file]) => file), ['events.test.js']);
  const refusals = code(readFileSync(join(DIR, 'events.test.js'), 'utf8'))
    .split('\n')
    .filter((line) => pattern.test(line));
  assert.deepEqual(refusals.length, 2, 'the two refusal controls are the only occurrences');
  for (const line of refusals) assert.ok(/assert\.(equal|throws)\(/.test(line), line.trim());
});
