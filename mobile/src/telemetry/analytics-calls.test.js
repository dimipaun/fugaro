// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What the app may send, held as a scan over its own source
// (mobile plan, Task P7.3; D256, D255; the telemetry plan's §7.4, rule 1).
//
// **Why a scan.** "Nothing that names a message, a mailbox or an account ever
// leaves the phone" is not a property any test of the telemetry module can
// hold: the module can be perfect and a screen four tasks from now can still
// reach the SDK itself, with a free-text key and a user id, and no test of the
// module would notice. The claim that does hold is the one `boundary.test.js`
// and `write-path.test.js` already make for the credentials and the write path:
// a token may appear in a named list of files and nowhere else, asserted over
// the app's source, with a positive control so a mistyped pattern cannot pass
// for a clean tree. A lint rule would be the same sentence in a config file and
// a rule nobody proves can fail.
//
// **The rules, and what each is for.**
//
//   1. `logEvent(` -- the SDK's own way to send an event. §7.2's allow-list is
//      seven names and no eighth, so every call site must name one of them as a
//      **literal** from the module's own list: a variable would be a name this
//      scan cannot read, and a rule it could only take on trust. The telemetry
//      module is where those seven call sites live, one per event, which is
//      what makes the allow-list non-vacuous rather than a promise.
//   2. `setUserId(`, `setUserProperty(` and `setAttribute(`/`setAttributes(` --
//      refused at any call site. D256's answer refuses free text outright, and
//      these are the SDK's doors for a value the app chose.
//   3. `crashlytics().log(` -- the crash path's breadcrumb. §7.3 makes a
//      breadcrumb a constant from the module's own list, or a value that has
//      been through `shared/digest`'s redactor first, so a call site may pass
//      exactly two things: a literal named in `BREADCRUMBS`, and a call to
//      **`breadcrumb` the file that calls it binds from the telemetry module**
//      -- not merely a function of that name, and not that name bound from
//      anywhere else or renamed on the way in. Both must be the WHOLE
//      argument: `'screen_mailbox' + message.text` and
//      `breadcrumb(text, secrets).trim()` are refused, because either would put
//      a runtime value beside a constant the list reviewed. Those two shapes are
//      what keeps §7.3's redaction reachable as well: a fence that refused every
//      argument but a constant would leave `breadcrumb` a function nothing may
//      pass to the only door a breadcrumb has, and a fence that took the name on
//      trust would let a local helper of that name send the text unredacted.
//   4. `setAnalyticsCollectionEnabled(` and `setCrashlyticsCollectionEnabled(`
//      -- refused at any call site. D255's answer is always-on: a call that sets
//      collection state IS the switch the plan says the app does not have,
//      whether it is called with `true` or with `false`. §6's "there is no
//      switch" is this rule rather than a sentence on a page.
//
// **What this scan does NOT claim.** It is about the app's own source. What the
// *native* SDK collects by itself -- its automatic events, its app-instance id,
// what a crash report carries by default -- is the build's and the console's,
// and only the plan's checks 120 and 121 can show it.
//
// Like its two siblings the scan reads CODE and not prose (the comment stripper
// below), it exempts the test files and itself by name, and every rule is
// proved against a planted line before the tree is judged -- including, for the
// two rules that read a call's first argument, a planted call that must be
// ACCEPTED, because a pattern that refused every call would pass a test that
// only planted offences.

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, sep } from 'node:path';
import test from 'node:test';

import { BREADCRUMBS } from './breadcrumbs.js';
import { EVENT_NAMES } from './events.js';

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

/** The code of a file, without its comments (the prose in this tree names the
 * very tokens the rules forbid). */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/**
 * The first argument a call site passes, as the scan can read it: a quoted
 * literal, or the name of the function the argument is a call to -- and either
 * only when it is the **whole** argument, so `'a' + b` and `f(x).trim()` are
 * read as the shapes they are rather than as the literal or the call they open
 * with. Everything else -- a template literal, a member expression, a
 * concatenation, no argument at all -- is a shape every rule refuses.
 */
function firstArgument(body, at) {
  let i = at;
  while (i < body.length && /\s/.test(body[i])) i += 1;
  if (i >= body.length || body[i] === ')') return { kind: 'none' };
  const quote = body[i];
  if (quote === "'" || quote === '"') {
    const end = body.indexOf(quote, i + 1);
    if (end !== -1 && endsArgument(body, end + 1)) return { kind: 'literal', value: body.slice(i + 1, end) };
    return { kind: 'other' };
  }
  const call = /^([A-Za-z_$][\w$]*)\s*\(/.exec(body.slice(i));
  if (call !== null) {
    const close = callEnd(body, i + call[0].length - 1);
    if (close !== -1 && endsArgument(body, close + 1)) return { kind: 'call', callee: call[1] };
  }
  return { kind: 'other' };
}

/** Whether what follows `at` ends the argument rather than continuing it. */
function endsArgument(body, at) {
  let i = at;
  while (i < body.length && /\s/.test(body[i])) i += 1;
  return body[i] === ',' || body[i] === ')';
}

/** The index of the `)` that closes the `(` at `open`, or -1 when the text
 * holds none -- which is a shape the scan refuses rather than one it guesses at.
 */
function callEnd(body, open) {
  let depth = 0;
  for (let i = open; i < body.length; i += 1) {
    const character = body[i];
    if (character === "'" || character === '"') {
      const end = body.indexOf(character, i + 1);
      if (end === -1) return -1;
      i = end;
      continue;
    }
    if (character === '(') depth += 1;
    if (character === ')') {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/** The telemetry module a breadcrumb call has to come from. */
const BREADCRUMBS_MODULE = 'src/telemetry/breadcrumbs.js';

/** The specifier `file` reaches `BREADCRUMBS_MODULE` through. */
function breadcrumbSpecifier(file) {
  const path = relative(dirname(file), BREADCRUMBS_MODULE).split(sep).join('/');
  return path.startsWith('.') ? path : `./${path}`;
}

/**
 * Whether an import clause binds the name `breadcrumb` itself, which is the
 * only thing that makes the call below it the module's own function: a named
 * specifier whose imported AND local names are `breadcrumb`. `{ breadcrumb as
 * redacted }` is not it -- the rename leaves `breadcrumb` free for a local
 * helper, and a helper of that name is not a redaction rule -- and neither is a
 * namespace or a default import, which bind some other name.
 */
function bindsBreadcrumb(clause) {
  const braces = /\{([^}]*)\}/.exec(clause);
  if (braces === null) return false;
  for (const entry of braces[1].split(',')) {
    const [imported, local] = entry.split(/\s+as\s+/).map((part) => part.trim());
    if (imported === 'breadcrumb' && (local === undefined || local === 'breadcrumb')) return true;
  }
  return false;
}

/**
 * Whether the `breadcrumb` a file calls is the module's own: an import that
 * binds that name from a specifier the file's own directory resolves to
 * `BREADCRUMBS_MODULE`. Binding the name is what closes the shadowing hole --
 * a module that imports `breadcrumb` cannot also declare one, so the name the
 * call reaches is the import's -- and a file that bound the name from anywhere
 * else, or declared its own, is not the app's one redaction rule.
 */
function importsBreadcrumb(body, file) {
  const specifier = breadcrumbSpecifier(file);
  const wanted = new Set([specifier, specifier.replace(/\.js$/, '')]);
  for (const match of body.matchAll(/\bimport\s+([^;]*?)\s+from\s*['"]([^'"]+)['"]/g)) {
    if (wanted.has(match[2]) && bindsBreadcrumb(match[1])) return true;
  }
  return false;
}

/**
 * The rules. `allow` lists the production files a token may appear in; test
 * files are always allowed (a fake must be able to call the method it stands in
 * for) and so is this file. `argument` marks the rules that read the first
 * argument of each call site instead of only finding the token; it is given what
 * `firstArgument` read, the file's code (a rule may have to read more of it than
 * the call) and the file's path (a rule may have to resolve an import against
 * it), and answers whether that shape is allowed.
 *
 * A `controls` entry is the offending line on its own, planted in a file that
 * imports nothing, or a `{ file, source }` pair when the offence is only an
 * offence in a particular file. An `accepted` entry is always a pair, because
 * what makes a call acceptable is sometimes what the file above it says.
 */
const RULES = [
  {
    name: 'every event sent is one the allow-list names',
    pattern: /logEvent\s*\(/g,
    argument: (read) => read.kind === 'literal' && EVENT_NAMES.includes(read.value),
    controls: [
      "analytics().logEvent('account_exported', { size: 1 });",
      "analytics().logEvent(name, parameters);",
      'analytics().logEvent(`screen_${screen}`, {});',
      'analytics().logEvent();',
    ],
    accepted: [
      { file: 'src/telemetry/allowed.js', source: "analytics().logEvent('app_launch', { first: true });" },
    ],
    why: 'D256: §7.2 is a closed allow-list of seven names, and a name the app invents is one nobody reviewed',
  },
  {
    name: 'no identifier or free-text key is set on the crash path',
    pattern: /setUserId\s*\(|setUserProperty\s*\(|setAttributes?\s*\(/g,
    controls: [
      "crashlytics().setUserId(account.authUid);",
      "crashlytics().setUserProperty('project', account.fugaroProject);",
      "crashlytics().setAttribute('screen', screen);",
      "crashlytics().setAttributes({ outcome });",
    ],
    accepted: [],
    why: 'D256: a free value beside a report is a third channel off the device, and the app has no use for one',
  },
  {
    name: 'a crash log names a breadcrumb from the module’s list, or comes from the redactor',
    pattern: /crashlytics\s*\(\s*\)\s*\.\s*log\s*\(/g,
    argument: (read, body, file) =>
      (read.kind === 'literal' && BREADCRUMBS.includes(read.value)) ||
      (read.kind === 'call' && read.callee === 'breadcrumb' && importsBreadcrumb(body, file)),
    controls: [
      "crashlytics().log(`opened ${message.text}`);",
      "crashlytics().log(text);",
      "crashlytics().log('opened the mailbox');",
      // A literal the list names, with something appended to it: the reader
      // takes the WHOLE argument, so this is not the constant it opens with.
      "crashlytics().log('screen_mailbox' + message.text);",
      // A call, but not the module's own redactor: a screen's own wrapper is
      // not the app's one redaction rule, and §7.3 says there is exactly one.
      "crashlytics().log(buildBreadcrumb(message.text));",
      // The module's own name, but not the module's own function: a file that
      // imports nothing has whatever `breadcrumb` of its own it likes.
      "crashlytics().log(breadcrumb(message.text, secrets));",
      // The module itself, imported under another name, which leaves the name
      // `breadcrumb` free for a helper that redacts nothing.
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb as redacted } from '../telemetry/breadcrumbs.js';\n" +
          'function breadcrumb(text) { return text; }\n' +
          'crashlytics().log(breadcrumb(message.text));',
      },
      // Its own name from somewhere else: `breadcrumb` is a name any module may
      // export, and one that does not redact is not the app's one rule.
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb } from './helpers.js';\n" +
          'crashlytics().log(breadcrumb(message.text, secrets));',
      },
      // ... and even the module's own redactor is refused when the call is not
      // the whole argument, which is the half the import cannot decide.
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb } from '../telemetry/breadcrumbs.js';\n" +
          'crashlytics().log(breadcrumb(message.text, secrets).trim());',
      },
      "crashlytics().log();",
    ],
    accepted: [
      { file: 'src/app/allowed.js', source: "crashlytics().log('screen_mailbox');" },
      {
        file: 'src/app/allowed.js',
        source:
          "import { breadcrumb } from '../telemetry/breadcrumbs.js';\n" +
          'crashlytics().log(breadcrumb(message.text, secrets));',
      },
      // The extension is Metro's to leave off, so both spellings are the import.
      {
        file: 'src/telemetry/allowed.js',
        source:
          "import { breadcrumb } from './breadcrumbs';\n" +
          'crashlytics().log(breadcrumb(message.text, secrets));',
      },
    ],
    why: 'D256, §7.3: a breadcrumb is a constant from the module’s list, or the module’s own redactor, and nothing else',
  },
  {
    name: 'nothing in the app switches collection on or off',
    pattern: /setAnalyticsCollectionEnabled\s*\(|setCrashlyticsCollectionEnabled\s*\(/g,
    controls: [
      'analytics().setAnalyticsCollectionEnabled(true);',
      'analytics().setAnalyticsCollectionEnabled(false);',
      'crashlytics().setCrashlyticsCollectionEnabled(true);',
    ],
    accepted: [],
    why: 'D255: collection is always on, so a call that sets its state is the switch the app does not have',
  },
];

/** Apply one rule to a map of path -> text, returning the offences, sorted. */
function offences(rule, files) {
  const found = [];
  for (const [file, text] of files) {
    if (file === SELF || isTest(file) || rule.allow?.includes(file)) continue;
    const body = code(text);
    if (rule.argument === undefined) {
      for (const _ of body.matchAll(rule.pattern)) {
        found.push(file);
        break;
      }
      continue;
    }
    for (const match of body.matchAll(rule.pattern)) {
      if (!rule.argument(firstArgument(body, match.index + match[0].length), body, file)) {
        found.push(file);
        break;
      }
    }
  }
  return found.sort();
}

/** A control as a file to plant it in and the code to plant: a bare line goes
 * into a file that imports nothing, which is the file most of the offences are
 * an offence in.
 */
function plant(control) {
  return typeof control === 'string'
    ? { file: 'src/app/offender.js', source: `function offence() { ${control} }\n` }
    : { file: control.file, source: `${control.source}\n` };
}

/** The tree, read once per test. */
function tree() {
  return new Map(sourceFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]));
}

test('the scan walks the app, and not an empty list', () => {
  const files = sourceFiles();
  assert.ok(files.length >= 10, `expected the app's source, found ${files.length} files`);
  for (const expected of [
    'index.js',
    'app.config.js',
    'src/telemetry/events.js',
    'src/telemetry/breadcrumbs.js',
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
    // code and in every shape the offence can be written in, or a green run
    // below would mean nothing. A rule may carry several, because the same
    // offence has more than one spelling that resolves to the same call.
    for (const control of rule.controls) {
      const { file, source } = plant(control);
      assert.deepEqual(offences(rule, new Map([[file, source]])), [file], JSON.stringify(control));
      // The planted line is not flagged when it is only prose.
      const prose = source
        .split('\n')
        .map((line) => `// ${line}`)
        .join('\n');
      assert.deepEqual(offences(rule, new Map([['src/app/prose.js', prose]])), [], JSON.stringify(control));
    }

    // ... and the calls the rule is FOR are not flagged, which is the half a
    // scan made only of offences could not tell from a pattern that matched
    // everything.
    for (const control of rule.accepted) {
      assert.deepEqual(
        offences(rule, new Map([[control.file, `${control.source}\n`]])),
        [],
        JSON.stringify(control),
      );
    }

    // ... and then the tree itself.
    assert.deepEqual(offences(rule, tree()), [], `${rule.name}: see the rule's allow-list above`);
  });
}

test('the allow-list is not vacuous: every event it names has exactly one call site', () => {
  // A rule whose allowed literals appear nowhere in the tree is a promise, and
  // this one is not: the telemetry module sends each of §7.2's names from its
  // own line, so the sorted literals of the tree's `logEvent(` calls are the
  // allow-list itself -- no name missing, and no name sent twice.
  const body = code(readFileSync(join(ROOT, 'src/telemetry/events.js'), 'utf8'));
  const sent = [...body.matchAll(/logEvent\s*\(\s*'([^']+)'/g)].map((match) => match[1]);
  assert.deepEqual([...sent].sort(), [...EVENT_NAMES].sort());
});

test('the breadcrumb list is not vacuous: every screen the app has is a breadcrumb', () => {
  // §7.3's constants are the module's own list, and a screen the app can reach
  // has one: a list that named no screen the app has would be a redaction rule
  // about a vocabulary nothing uses.
  for (const screen of ['overview', 'jobs', 'mailbox', 'voice', 'text']) {
    assert.ok(BREADCRUMBS.includes(`screen_${screen}`), `screen_${screen}`);
  }
});
