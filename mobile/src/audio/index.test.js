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
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import test from 'node:test';

import * as audio from './index.js';

const DIR = import.meta.dirname;
const MOBILE = join(DIR, '..', '..');
const SELF = 'index.test.js';

/** Every file in this directory, sorted -- the module's whole surface. */
const FILES = readdirSync(DIR)
  .filter((name) => name.endsWith('.js'))
  .sort();

const isTest = (file) => file.endsWith('.test.js');

test('the module is exactly these files', () => {
  // A new file here is a new thing the app can import, so it is a deliberate
  // edit to this list. That is what makes "no engine yet" checkable rather
  // than a claim in a header: an engine is `engine.js`, and this fails.
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
 * The rules. Each is a pattern, why it is here, and what it would mean.
 *
 * The patterns are deliberately broad (`\bDate\b` rather than `Date.now`): the
 * point is that the *capability* is absent, and a capability is absent only if
 * every way of reaching it is.
 */
const RULES = [
  {
    name: 'no clock',
    pattern: /\bDate\b|\bperformance\s*\.\s*now\b|\bhrtime\b/,
    sample: 'const at = Date.now()',
    why: 'D117: the clock is the server-anchored one the pipeline is handed, so this module reads none',
  },
  {
    name: 'no random source',
    pattern: /\bMath\s*\.\s*random\b|\bcrypto\b/,
    sample: 'const id = Math.random()',
    why: 'the message id is injected (`newId`), and a random source here would be one no test could pin',
  },
  {
    name: 'no timer',
    pattern: /\bsetTimeout\b|\bsetInterval\b|\bsetImmediate\b|\brequestAnimationFrame\b/,
    sample: 'setTimeout(() => {}, 300)',
    why: '§5\'s ~300 ms grace period and any silence timeout are measured by the engine, not by a timer this module owns',
  },
  {
    name: 'no network',
    pattern: /\bfetch\s*\(|\bWebSocket\b|\bXMLHttpRequest\b|\bEventSource\b/,
    sample: "fetch('https://example.invalid/audio')",
    why: 'D123/D46: on-device engines only; a cloud engine needs a second, explicit owner decision',
  },
  {
    name: 'no device API',
    pattern: /\bgetUserMedia\b|\bMediaRecorder\b|\bAudioContext\b|\bnavigator\b|\bPermissions\b/,
    sample: 'navigator.mediaDevices.getUserMedia({ audio: true })',
    why: 'D122: the microphone is reached through the AudioIn port, and the OS permission through whomever implements it -- never here',
  },
  {
    name: 'no audio samples, in any encoding',
    pattern: /\bpcm\b|\bsamples\b|\bArrayBuffer\b|\bFloat32Array\b|\bUint8Array\b|\bbase64\b|\bBlob\b/i,
    sample: 'const chunk = new Float32Array(1024)',
    why: 'D118/F26: text crosses these ports and audio never does, so there is no buffer here to leak one through',
  },
  {
    name: 'no logging',
    pattern: /\bconsole\s*\./,
    sample: "console.log('heard something')",
    why: 'a transcript in a log line is a transcript outside the mailbox (D118), and the app logs nothing',
  },
  {
    name: 'nothing is read from the environment',
    pattern: /\bprocess\s*\.\s*env\b/,
    sample: "const key = process.env.KEY",
    why: 'an engine chosen or configured by an environment variable is a configuration this module cannot see or test',
  },
];

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

/** The production files of this directory, as path -> code. */
function production() {
  return FILES.filter((file) => !isTest(file) && file !== SELF).map((file) => [file, code(readFileSync(join(DIR, file), 'utf8'))]);
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
    // The positive control: the rule's pattern must match the line it claims to
    // forbid, in real code.
    assert.equal(rule.pattern.test(code(`function offence() { ${rule.sample}; }\n`)), true, rule.sample);
    // ... and must not match prose, which is what lets these files explain
    // themselves.
    assert.equal(rule.pattern.test(code(`// ${rule.sample} is forbidden here\n`)), false, rule.sample);
    // ... and then the module itself.
    const offenders = production().filter(([, text]) => rule.pattern.test(text)).map(([file]) => file);
    assert.deepEqual(offenders, [], `${rule.name}: ${rule.why}`);
  });
}

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

/** The specifiers in `file` (this directory's name) that are not this
 * directory's own files or `shared/`. */
function strangers(file, text) {
  const shared = join(MOBILE, '..', 'shared');
  return specifiers(code(text))
    .filter((spec) => {
      if (spec.startsWith('node:')) return true; // a test's own tooling; the walk below excludes them
      if (!spec.startsWith('.')) return true; // a package name: the offence this rule exists for
      const target = resolve(join(DIR, dirname(file)), spec);
      const inModule = target === DIR || target.startsWith(DIR + sep); // a sibling file
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
