// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The audio module's door, and the scan that keeps it what it is
// (mobile plan, Task P3.4; D122, D123, D126, D118, D119).
//
// Two rules, and they are the two halves of "interfaces and fakes only": the
// door's exports are a **named list asserted against a literal**, so a fifth
// port, a "real" engine or a helper that reaches a device is an edit to this
// file rather than an import somewhere; and the directory's **code** is scanned
// for the things an engine would need and this module must not have -- a
// package, a clock, a random source, a timer, the network, a device API, or a
// buffer of samples.
//
// **Why a scan and not a rule.** Nobody can write a test that says "no file in
// this directory will grow a microphone". What can be written is a test that
// says the tokens such a change would need do not appear here today, with a
// positive control proving the pattern matches real code -- so the change has
// to be deliberate and visible in a diff, which is exactly what D123's
// on-device-only ruling needs to survive contact with a later task.
//
// **The self-exemption, the controls, and the comments.** This file names every
// forbidden token as a pattern and as a planted example, so it exempts itself
// by name, once. The controls are asserted before the tree: without them a typo
// in a pattern would turn the file green while checking nothing. And the scan
// reads each file's *code* (`code()` below), because these files' headers
// discuss the very tokens the rules forbid -- the prose is the explanation, and
// a rule that forbade its own explanation would be a rule nobody could document.

import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve, sep } from 'node:path';
import test from 'node:test';

import * as audio from './index.js';

const DIR = import.meta.dirname;
const MOBILE = join(DIR, '..', '..');
const SELF = 'index.test.js';

/**
 * Every `.js` file of the module, **at any depth**, sorted, as this directory's
 * relative paths with `/` separators.
 *
 * Recursive on purpose, and the recursion is not a detail: a non-recursive walk
 * is a fence one directory deep. `src/audio/native/engine.js` would never enter
 * the file list, the token rules or the no-package scan -- an engine arriving
 * quietly, which is the one thing this file exists to refuse. `boundary.test.js`
 * walks the same way and for the same reason; it does not close the gap either,
 * because its import rule judges *path* specifiers and says nothing about
 * `import voice from '@react-native-voice/voice'`.
 */
function audioFiles(dir = DIR, prefix = '') {
  const found = [];
  for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      found.push(...audioFiles(path, `${prefix}${entry.name}/`));
    } else if (entry.name.endsWith('.js')) {
      found.push(`${prefix}${entry.name}`);
    }
  }
  return found.sort();
}

const FILES = audioFiles();

const isTest = (file) => file.endsWith('.test.js');

test('the walk is recursive, so a file in a subdirectory is part of the module', () => {
  // The control for the fence above, and the reason it is a test rather than a
  // comment: with a non-recursive walk every assertion below still passes today
  // -- there is no subdirectory yet -- so nothing else in this file can tell
  // the two walks apart.
  const root = mkdtempSync(join(tmpdir(), 'fugaro-audio-'));
  try {
    writeFileSync(join(root, 'top.js'), 'export const top = 1;\n');
    mkdirSync(join(root, 'native', 'deep'), { recursive: true });
    writeFileSync(join(root, 'native', 'engine.js'), "import Voice from 'react-native-voice';\n");
    writeFileSync(join(root, 'native', 'deep', 'codec.js'), 'export const codec = 1;\n');
    writeFileSync(join(root, 'native', 'notes.txt'), 'not a module\n');
    assert.deepEqual(audioFiles(root), ['native/deep/codec.js', 'native/engine.js', 'top.js'], 'at every depth');
    // ... and a nested file is scanned exactly like a sibling: the package
    // import that was invisible to the old walk is an offence to the rule.
    assert.deepEqual(
      production(root).map(([file]) => file),
      ['native/deep/codec.js', 'native/engine.js', 'top.js'],
    );
    assert.deepEqual(
      strangers('native/engine.js', readFileSync(join(root, 'native', 'engine.js'), 'utf8'), root),
      ['native/engine.js: react-native-voice'],
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('the module is exactly these files, at every depth', () => {
  // A new file here is a new thing the app can import, so it is a deliberate
  // edit to this list. That is what makes "no engine yet" checkable rather
  // than a claim in a header: an engine is `engine.js`, and this fails -- at
  // any depth, which is why the walk above is recursive.
  assert.deepEqual(FILES, [
    'audio-in.js',
    'audio-in.test.js',
    'index.js',
    'index.test.js',
    'message.js',
    'message.test.js',
    'pipeline.js',
    'pipeline.test.js',
    'refusal.js',
    'refusal.test.js',
    'stt.js',
    'stt.test.js',
    'tts.js',
    'tts.test.js',
  ]);
});

test('the door exports exactly this, and every name is one of the three ports, a fake or a rule', () => {
  assert.deepEqual(Object.keys(audio).sort(), [
    'AUDIO_IN_CODES',
    'AUDIO_IN_EVENTS',
    'AUDIO_IN_EVENT_FIELDS',
    'DRAFT_FIELDS',
    'FakeAudioIn',
    'FakeStt',
    'FakeTts',
    'PIPELINE_EVENTS',
    'PIPELINE_EVENT_FIELDS',
    'STT_CODES',
    'STT_EVENTS',
    'STT_EVENT_FIELDS',
    'TTS_CODES',
    'createVoicePipeline',
    'isAudioIn',
    'isRefusal',
    'isSpeechToText',
    'isTextToSpeech',
    'isVoicePipeline',
    'messageDraft',
  ]);
  // The three classes on the door are the three fakes, and there is no fourth:
  // the check for "is this an engine?" is that there is nothing else exported
  // that could be constructed.
  const classes = Object.entries(audio)
    .filter(([, value]) => typeof value === 'function' && /^class\b/.test(Function.prototype.toString.call(value)))
    .map(([name]) => name)
    .sort();
  assert.deepEqual(classes, ['FakeAudioIn', 'FakeStt', 'FakeTts']);
  // ... and the list-valued constants are frozen, so a caller cannot add a
  // fourth event kind to the module it imported.
  for (const [name, value] of Object.entries(audio)) {
    if (value !== null && typeof value === 'object') assert.equal(Object.isFrozen(value), true, `${name} is frozen`);
  }
});

/**
 * The rules. Each is a name, the **tokens** it forbids, why it is here, and what
 * a token would mean.
 *
 * The tokens are the rule, and the pattern is built from them
 * (`forbiddenPattern`): a rule is "these words do not appear in this module's
 * code", and a regex written out beside them is a second copy of the same fact
 * that can drift from the first. The README's rule table carries the same tokens
 * in the same order, and `the README's rule table and the scan are the same
 * list` compares the two -- so the table a reader checks is the scan that runs.
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
    why: 'D117: the clock is the server-anchored one the pipeline is handed, so this module reads none',
  },
  {
    name: 'no random source',
    tokens: ['Math.random', 'crypto'],
    sample: 'const id = Math.random()',
    why: 'the message id is injected (`newId`), and a random source here would be one no test could pin',
  },
  {
    name: 'no timer',
    tokens: ['setTimeout', 'setInterval', 'setImmediate', 'requestAnimationFrame'],
    sample: 'setTimeout(() => {}, 300)',
    why: '§5\'s ~300 ms grace period and any silence timeout are measured by the engine, not by a timer this module owns',
  },
  {
    name: 'no network',
    tokens: ['fetch(', 'WebSocket', 'XMLHttpRequest', 'EventSource'],
    sample: "fetch('https://example.invalid/audio')",
    why: 'D123/D46: on-device engines only; a cloud engine needs a second, explicit owner decision',
  },
  {
    name: 'no device API',
    tokens: ['getUserMedia', 'MediaRecorder', 'AudioContext', 'navigator', 'Permissions'],
    sample: 'navigator.mediaDevices.getUserMedia({ audio: true })',
    why: 'D122: the microphone is reached through the AudioIn port, and the OS permission through whomever implements it -- never here',
  },
  {
    name: 'no samples, in any encoding',
    tokens: ['pcm', 'samples', 'ArrayBuffer', 'Float32Array', 'Uint8Array', 'base64', 'Blob'],
    sample: 'const chunk = new Float32Array(1024)',
    why: 'D118/F26: text crosses these ports and audio never does, so there is no buffer here to leak one through',
  },
  {
    name: 'no logging',
    tokens: ['console.'],
    sample: "console.log('heard something')",
    why: 'a transcript in a log line is a transcript outside the mailbox (D118), and the app logs nothing',
  },
  {
    name: 'nothing is read from the environment',
    tokens: ['process.env'],
    sample: "const key = process.env.KEY",
    why: 'an engine chosen or configured by an environment variable is a configuration this module cannot see or test',
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
  return audioFiles(dir)
    .filter((file) => !isTest(file) && file !== SELF)
    .map((file) => [file, code(readFileSync(join(dir, file), 'utf8'))]);
}

test('the scan reads the module, and not an empty list', () => {
  const files = production();
  assert.deepEqual(files.map(([file]) => file), ['audio-in.js', 'index.js', 'message.js', 'pipeline.js', 'refusal.js', 'stt.js', 'tts.js']);
  for (const [file, text] of files) {
    assert.ok(text.includes('export'), `${file} is real code, not a comment`);
  }
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

// **The README's table is this list, and this test is what makes that true.**
// The table a reader consults and the scan that runs were two hand-written lists
// of the same tokens, and they had drifted: the device-API row did not name
// `Permissions`, which the scan forbids, and the samples row did not name
// `Uint8Array` -- so a reader checking the code against the documentation would
// have checked a weaker rule than the one enforced. The rows are read out of
// `mobile/README.md` and compared to `RULES`, token for token and row for row.

/** The line that opens the README's rule table, in the P3.4 section. */
const RULE_TABLE_HEADER = '| Rule | Exactly what the scan forbids | Why |';

/**
 * The README's rule table, as `{ name, tokens }` per row: the row's first cell
 * is the rule's name and the backticked spans of its second cell are its tokens,
 * in the order the row writes them.
 *
 * @returns {{name: string, tokens: string[]}[]}
 */
function readmeRuleRows() {
  const lines = readFileSync(join(MOBILE, 'README.md'), 'utf8').split('\n');
  const start = lines.indexOf(RULE_TABLE_HEADER);
  assert.notEqual(start, -1, `mobile/README.md no longer carries the rule table (${RULE_TABLE_HEADER})`);
  const rows = [];
  for (const line of lines.slice(start + 2)) {
    if (!line.startsWith('|')) break;
    const cells = line.split('|').slice(1, -1);
    const name = cells[0].trim();
    const tokens = [...cells[1].matchAll(/`([^`]+)`/g)].map((match) => match[1]);
    rows.push({ name, tokens });
  }
  assert.notEqual(rows.length, 0, 'the rule table has no rows: an empty table agrees with an empty scan');
  return rows;
}

test("the README's rule table and the scan are the same list", () => {
  const rows = readmeRuleRows();
  // Row for row, in order: a rule the scan carries and the table drops is a
  // rule a reader cannot see, and a row the scan does not carry is a rule
  // nothing enforces.
  assert.deepEqual(
    rows.map((row) => row.name),
    RULES.map((rule) => rule.name),
  );
  for (const [index, rule] of RULES.entries()) {
    // Token for token, in order: this is the drift the test exists for.
    assert.deepEqual(rows[index].tokens, rule.tokens, `${rule.name}: the row and the scan list the same tokens`);
  }
  // And every token is a real pattern: a token that matches nothing would make
  // the row a promise the scan does not keep.
  for (const rule of RULES) {
    const pattern = forbiddenPattern(rule.tokens);
    for (const token of rule.tokens) {
      assert.equal(pattern.test(`function offence() { const x = ${token} ; }\n`), true, `${rule.name}: ${token}`);
    }
  }
});

// **The last rule is about imports, and it is not the same rule as
// `boundary.test.js`'s.** That scan resolves *path* specifiers and catches an
// import that leaves `mobile/` for a sibling other than `shared/`; it says
// nothing about `import voice from '@react-native-voice/voice'`, which is a
// package name and resolves into `node_modules`. For a directory whose entire
// claim is "no engine here", that is the import that matters most, so it is
// checked here: this module imports its own files and `shared/`, and nothing
// else at all.

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

/** The specifiers in `file` (relative to `dir`) that are not the module's own
 * files or `shared/`. `dir` is a parameter because the walk is tested against a
 * temporary tree, where the module's root is not this directory. */
function strangers(file, text, dir = DIR) {
  const shared = join(MOBILE, '..', 'shared');
  return specifiers(code(text))
    .filter((spec) => {
      if (spec.startsWith('node:')) return true; // a test's own tooling; the walk below excludes them
      if (!spec.startsWith('.')) return true; // a package name: the offence this rule exists for
      const target = resolve(join(dir, dirname(file)), spec);
      const inModule = target === dir || target.startsWith(dir + sep); // a file of this module
      const inShared = target === shared || target.startsWith(shared + sep);
      return !inModule && !inShared;
    })
    .map((spec) => `${file}: ${spec}`);
}

test('the module imports its own files and shared/, and no package at all', () => {
  // Positive controls: the shapes this rule exists to catch. The last one is
  // assembled rather than written out, because a literal path into the CLI
  // would be an offence to `boundary.test.js` -- which scans this file too, and
  // is right to: a control is a string, but the scanner cannot know that.
  const intoTheCli = `${'../../../con'}sole/src/main.js`;
  for (const line of [
    "import voice from '@react-native-voice/voice';",
    "import { Audio } from 'expo-av';",
    "const recognizer = require('react-native-speech-to-text');",
    "const sdk = await import('@google-cloud/speech');",
    "import '../mailbox/client.js';",
    `import c from '${intoTheCli}';`,
  ]) {
    assert.deepEqual(strangers('index.js', `${line}\n`), [`index.js: ${line.match(/['"]([^'"]+)['"]/)[1]}`], line);
  }
  // ... and the allowed shapes are not flagged: a sibling file, the shared
  // package, and a package name that is only prose.
  assert.deepEqual(
    strangers(
      'pipeline.js',
      "import { isAudioIn } from './audio-in.js';\nimport { REFUSALS } from '../../../shared/mailbox-schema/index.js';\n// import voice from '@react-native-voice/voice';\n",
    ),
    [],
  );

  // Production code: no import may leave for anything but `shared/`.
  assert.deepEqual(
    production().flatMap(([file, text]) => strangers(file, text)),
    [],
    'a package import here is an engine, or a device, arriving quietly',
  );
  // The tests may use `node:` builtins and this directory's files, and nothing
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
  // ... and the tests' relative imports are real: `shared/` and this directory.
  assert.deepEqual(
    tests.flatMap((file) => strangers(file, readFileSync(join(DIR, file), 'utf8')))
      .filter((offence) => !offence.includes('node:')),
    [],
  );
});
