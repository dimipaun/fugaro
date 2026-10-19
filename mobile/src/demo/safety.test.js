// The three fences around the demo (owner ruling of 2026-10-19, and the task's
// own requirement): it can never touch the network, it can never touch secure
// storage or a real credential, and every screen it draws says what it is.
//
// **Why fences and not rules.** Nobody can write a unit test for "this mode
// will never reach the network": that is a statement about a mode that does not
// exist yet, and a future task is exactly where it would stop being true. What
// can be written is that the tokens such a change needs do not appear in this
// directory today -- so the change has to be deliberate and visible in a diff --
// together with two behavioural proofs that do not depend on spelling:
//
//   1. the demo is entered with `fetch` and `XMLHttpRequest` replaced by
//      throwers, and it completes;
//   2. the demo is entered with the app's own `SecretStore` built over a
//      Keychain port that records and kills every call, and the port sees no
//      call at all -- because the demo's controller takes no store to reach,
//      which is the structural half of the same claim.
//
// **The controls, and the comments.** Every pattern has a planted example
// asserted before the tree (a typo in a pattern would otherwise turn this file
// green while checking nothing), and the scan reads each file's CODE with its
// comments removed -- because these files' headers discuss the very tokens the
// rules forbid, which is the explanation and not the offence.

import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { SECURE_STORE_CODES, SecretStore } from '../accounts/index.js';
import { createDemoMode } from './index.js';

const DIR = import.meta.dirname;
const SELF = 'safety.test.js';

/** Every `.js` file of the module, at any depth, sorted, `/`-separated. */
function demoFiles(dir = DIR, prefix = '') {
  const found = [];
  for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      found.push(...demoFiles(path, `${prefix}${entry.name}/`));
    } else if (entry.name.endsWith('.js')) {
      found.push(`${prefix}${entry.name}`);
    }
  }
  return found.sort();
}

/**
 * A file's code: comments removed, so the prose above is not read as an
 * offence. Strings are NOT removed -- a URL in a string is the offence this
 * file most wants to catch -- and the removal is a line-based best effort, not
 * a parser: `//` inside a string is stripped, which can only make the scan
 * quieter about that one line's remainder, never louder about prose.
 */
function code(source) {
  let inBlock = false;
  const kept = [];
  for (const raw of source.split('\n')) {
    let line = '';
    let rest = raw;
    while (rest !== '') {
      if (inBlock) {
        const end = rest.indexOf('*/');
        if (end === -1) break;
        inBlock = false;
        rest = rest.slice(end + 2);
        continue;
      }
      const open = rest.indexOf('/*');
      const slash = lineComment(rest);
      if (slash !== -1 && (open === -1 || slash < open)) {
        line += rest.slice(0, slash);
        break;
      }
      if (open === -1) {
        line += rest;
        break;
      }
      line += rest.slice(0, open);
      inBlock = true;
      rest = rest.slice(open + 2);
    }
    kept.push(line);
  }
  return kept.join('\n');
}

/**
 * Where a line's `//` comment starts, or -1.
 *
 * The one heuristic, and it exists for the rule this file most wants to catch:
 * `//` preceded by `:` is a URL's scheme and not a comment, so
 * `"https://example.invalid"` stays in the code the scan reads. Without it, the
 * network rule would be blind to the one spelling that matters most. A `//`
 * inside an ordinary string is still read as a comment start, which makes the
 * scan quieter about the rest of that line and never louder.
 */
function lineComment(rest) {
  for (let i = 0; i + 1 < rest.length; i++) {
    if (rest[i] === '/' && rest[i + 1] === '/' && rest[i - 1] !== ':') return i;
  }
  return -1;
}

/**
 * What may not appear in this module's code, and what each token would mean.
 * `production` is every non-test file of the directory.
 */
const RULES = [
  {
    name: 'the network',
    pattern: /(\bfetch\s*\(|XMLHttpRequest|WebSocket|sendBeacon|axios|@react-native-firebase|firebase\/database|databaseURL|firebaseio|googleapis|https?:\/\/)/,
    control: 'const r = await fetch("https://example.invalid/x");',
  },
  {
    name: 'platform storage',
    pattern: /(expo-secure-store|ItemAsync|AsyncStorage|localStorage|sessionStorage|Keychain|getItem|setItem|deleteItem)/,
    control: "import * as SecureStore from 'expo-secure-store';",
  },
  {
    name: 'a logger',
    pattern: /console\./,
    control: "console.log('the key is', key);",
  },
  {
    name: 'a clock, a timer or a random source',
    pattern: /(Date\.now|new Date|Math\.random|setInterval|setTimeout|process\.env)/,
    control: 'const now = Date.now();',
  },
  {
    name: 'the app\'s module loader',
    pattern: /(require\s*\(|import\s*\()/,
    control: "const x = require('./somewhere.js');",
  },
];

/** The files a rule judges: everything here that is not this file or a test. */
function production(dir = DIR, prefix = '') {
  return demoFiles(dir, prefix)
    .filter((file) => !file.endsWith('.test.js') && file !== SELF)
    .map((file) => [file, readFileSync(join(dir, file), 'utf8')]);
}

/** The files that break one rule, as `file: token` strings. */
function offenders(rule, files) {
  return files
    .filter(([, source]) => rule.pattern.test(code(source)))
    .map(([file]) => `${file}: ${rule.name}`);
}

/** A Keychain port that records every call and refuses to serve it. */
function instrumentedPort() {
  const calls = [];
  const trap = (name) => async () => {
    calls.push(name);
    throw new Error(`the demo reached secure storage: ${name}`);
  };
  return { calls, port: { getItem: trap('getItem'), setItem: trap('setItem'), deleteItem: trap('deleteItem') } };
}

test('the walk is recursive, so a file in a subdirectory is scanned too', () => {
  // The control for the walk: with a non-recursive one, every assertion below
  // still passes today, because there is no subdirectory yet.
  const root = mkdtempSync(join(tmpdir(), 'fugaro-demo-'));
  try {
    writeFileSync(join(root, 'top.js'), 'export const top = 1;\n');
    mkdirSync(join(root, 'deep', 'deeper'), { recursive: true });
    writeFileSync(join(root, 'deep', 'deeper', 'net.js'), 'await fetch("https://example.invalid");\n');
    assert.deepEqual(demoFiles(root), ['deep/deeper/net.js', 'top.js'], 'at every depth');
    assert.deepEqual(
      offenders(RULES[0], production(root)),
      ['deep/deeper/net.js: the network'],
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('the module is exactly these files, and every one of them is scanned', () => {
  // A new file here is a new thing the demo can do, so it is an edit to this
  // line and a diff somebody reads. `production` is asserted non-empty: an
  // empty list would make every rule below vacuous.
  assert.deepEqual(demoFiles(), [
    'index.js',
    'index.test.js',
    'keys.js',
    'safety.test.js',
    'sample.js',
    'sample.test.js',
    'screen.js',
    'screen.test.js',
    'transport.js',
    'transport.test.js',
  ]);
  assert.deepEqual(production().map(([file]) => file), [
    'index.js',
    'keys.js',
    'sample.js',
    'screen.js',
    'transport.js',
  ]);
});

test('the scan reads code and not prose: a token in a comment is not an offence', () => {
  // The non-vacuity control for `code()`: these headers name every forbidden
  // token, so a scan that read the raw file would fail on its own explanation.
  assert.equal(code('// await fetch("https://example.invalid")\n').trim(), '');
  assert.equal(code('/* fetch( */\n').trim(), '');
  assert.equal(code('const url = "https://example.invalid";\n').includes('https://'), true);
  assert.equal(RULES[0].pattern.test(code('// fetch( internal.invalid/x )\n')), false);
  for (const rule of RULES) assert.equal(rule.pattern.test(rule.control), true, rule.name);
});

test('nothing in the demo touches the network, storage, a logger, a clock or a random source', () => {
  assert.deepEqual(offenders(RULES[0], production()), []);
  assert.deepEqual(offenders(RULES[1], production()), []);
  assert.deepEqual(offenders(RULES[2], production()), []);
  assert.deepEqual(offenders(RULES[3], production()), []);
  assert.deepEqual(offenders(RULES[4], production()), []);
});

test('entering the demo with the network poisoned is still the whole demo', async () => {
  // The behavioural half: the fence above is about spelling, and this one is
  // about the run. A demo that reached the network at all would die here --
  // and this test would fail rather than pass quietly, because the assertions
  // below are about a demo that completed.
  const real = { fetch: globalThis.fetch, XMLHttpRequest: globalThis.XMLHttpRequest, WebSocket: globalThis.WebSocket };
  const boom = () => { throw new Error('the demo reached the network'); };
  globalThis.fetch = boom;
  globalThis.XMLHttpRequest = boom;
  globalThis.WebSocket = boom;
  try {
    const demo = createDemoMode();
    const state = await demo.enter();
    await demo.confirm();
    assert.equal(state.active, true);
    assert.equal(demo.state().messages.length, 3);
    assert.equal(demo.state().spoken.length, 3);
    assert.deepEqual(demo.state().problems, []);
  } finally {
    globalThis.fetch = real.fetch;
    globalThis.XMLHttpRequest = real.XMLHttpRequest;
    globalThis.WebSocket = real.WebSocket;
  }
});

test('entering the demo reads and writes nothing in secure storage, and opens no account', async () => {
  // The app's own `SecretStore`, over a port that kills the call and records
  // it. The demo is entered, confirmed and left with that store in the room.
  const keychain = instrumentedPort();
  const secrets = new SecretStore(keychain.port);

  const demo = createDemoMode();
  await demo.enter();
  await demo.confirm();
  demo.leave();
  await demo.enter();

  assert.deepEqual(keychain.calls, [], 'the Keychain port was never reached');

  // ... and a demo that DID ask for the key would hit the port through exactly
  // this door, which is what makes the empty list above a fact about the demo
  // rather than about the port being unreachable. The control is the app's own
  // store being asked directly: the port is called, and the store fails closed
  // (P3.1) rather than handing back what the port said.
  const direct = await secrets.get('demo-phone', 'macKey');
  assert.equal(direct.ok, false);
  assert.equal(direct.code, SECURE_STORE_CODES.STORE_FAILED);
  assert.deepEqual(keychain.calls, ['getItem']);
});

test('the demo\'s controller takes no store, no key source and no port', () => {
  // The structural half, and the honest way to say it: a controller that
  // accepted a `SecretStore` could read a phone's Keychain, so it accepts no
  // store at all. What it takes is one number -- its clock.
  assert.equal(createDemoMode.length, 0);
  const demo = createDemoMode({ now: 1_766_000_000_000 });
  assert.deepEqual(Object.keys(demo).sort(), ['active', 'confirm', 'enter', 'leave', 'ok', 'state', 'subscribe']);
  // Nothing in the module names the storage SDK, either (the token rule above,
  // stated as the reason it matters): `keys.js` imports P3.1's REFUSAL CODES,
  // which is a list of strings and not a way to read anything.
  const keys = readFileSync(join(DIR, 'keys.js'), 'utf8');
  assert.ok(keys.includes("from '../accounts/index.js'"));
  assert.equal(RULES[1].pattern.test(code(keys)), false);
});
