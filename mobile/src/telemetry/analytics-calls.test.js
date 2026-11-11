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
//   1. `logEvent(` and every other helper the SDK sends an event through
//      (`logScreenView(`, `logSearch(`, `logSelectContent(`, any `logX(`).
//      §7.2's allow-list is seven names and no eighth, so every call site must
//      name one of them as a **literal** from the module's own list: a variable
//      would be a name this scan cannot read, and a rule it could only take on
//      trust. A helper that sends an event of its own is refused by the same
//      reader, because its argument is that helper's own object and not one of
//      the seven literals. **The parameters are read under the name**, because
//      §7.1's ban list is "no free text at all" and a name alone is only half
//      the claim: the second argument must be a single object literal carrying
//      exactly the keys that event's entry declares, each value a boolean or a
//      member of the closed set the entry names -- so
//      `logEvent('screen_shown', { screen: message.text })` is refused for its
//      value, `{ text: … }` for its key, and `{ screen }` for a shape the scan
//      cannot read. A second argument that is not an object literal is refused
//      with them, because `logEvent('screen_shown', message)` hands the SDK an
//      object holding the message's own fields all the same. The telemetry
//      module is where those seven call sites live, one per event, which is
//      what makes the allow-list non-vacuous rather than a promise -- and it is
//      the one file this rule allows, because the parameters that reach its
//      port are the ones `telemetryEvent` has already shaped and refused;
//      `events.test.js` is where that door is held instead.
//   2. `setUserId(`, `setUserProperty(`/`setUserProperties(`,
//      `setAttribute(`/`setAttributes(` and `setDefaultEventParameters(` --
//      refused at any call site, whatever they are passed. D256's answer
//      refuses free text outright, and these are the SDK's doors for a value the
//      app chose; the last of them is the one that rides on every event the app
//      sends afterwards.
//   3. `log(` on anything, and `recordError(` -- the crash path's breadcrumb.
//      The receiver is deliberately not read: `crashlytics().log(`, the SDK
//      object kept in a variable and logged through later, and the modular
//      API's `log(instance, ...)` are one door reached three ways, and the
//      fence is about the argument. §7.3 makes a breadcrumb a constant from the
//      module's own list, or a value that has been through `shared/digest`'s
//      redactor first, so a call site may pass exactly two things: a literal
//      named in `BREADCRUMBS`, and a call to **`breadcrumb` the file that calls
//      it binds from the telemetry module** -- not merely a function of that
//      name, not that name bound from anywhere else or renamed on the way in,
//      and not a file that gives the name a second meaning of its own: a nested
//      parameter or declaration shadows the import in a scope this text scan
//      cannot see into, so a file that declares a `breadcrumb` anywhere is
//      refused rather than read. Both must be the WHOLE argument:
//      `'screen_mailbox' + message.text` and
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
import { EVENT_NAMES, EVENT_PARAMETERS } from './events.js';

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
 * concatenation, no argument at all -- is a shape every rule refuses. `end` is
 * the index of the `,` or `)` that ends the argument, which is where a rule
 * that has to read the argument after it starts from.
 */
function firstArgument(body, at) {
  const i = afterSpace(body, at);
  if (i >= body.length || body[i] === ')') return { kind: 'none', end: i };
  const quote = body[i];
  if (quote === "'" || quote === '"') {
    const close = body.indexOf(quote, i + 1);
    const end = close === -1 ? -1 : argumentEnd(body, close + 1);
    if (end !== -1) return { kind: 'literal', value: body.slice(i + 1, close), end };
    return { kind: 'other', end: -1 };
  }
  const call = /^([A-Za-z_$][\w$]*)\s*\(/.exec(body.slice(i));
  if (call !== null) {
    const close = closeIndex(body, i + call[0].length - 1);
    const end = close === -1 ? -1 : argumentEnd(body, close + 1);
    if (end !== -1) return { kind: 'call', callee: call[1], end };
  }
  return { kind: 'other', end: -1 };
}

/** The index of the first character at or after `at` that is not a space. */
function afterSpace(body, at) {
  let i = at;
  while (i < body.length && /\s/.test(body[i])) i += 1;
  return i;
}

/** The index of the `,` or `)` that ends the argument starting at `at`, or -1
 * when the text continues the argument instead. */
function argumentEnd(body, at) {
  const i = afterSpace(body, at);
  return body[i] === ',' || body[i] === ')' ? i : -1;
}

/** Whether what follows `at` ends the argument rather than continuing it. */
function endsArgument(body, at) {
  return argumentEnd(body, at) !== -1;
}

/**
 * The index of the character that closes the `(` or `{` at `open`, or -1 when
 * the text holds none -- which is a shape the scan refuses rather than one it
 * guesses at. A quoted string is skipped whole, so a brace inside one is not
 * counted and a string that is never closed is a refusal too.
 */
function closeIndex(body, open) {
  const opener = body[open];
  const closer = opener === '{' ? '}' : ')';
  let depth = 0;
  for (let i = open; i < body.length; i += 1) {
    const character = body[i];
    if (character === "'" || character === '"') {
      const end = body.indexOf(character, i + 1);
      if (end === -1) return -1;
      i = end;
      continue;
    }
    if (character === opener) depth += 1;
    if (character === closer) {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/**
 * What the call site passes as the event's parameters: the second argument when
 * it is a single object literal of `name: value` entries, and the shape it is
 * otherwise. A parameter list this cannot read -- a shorthand property, a
 * spread, a nested object, a computed key, a call -- is `other`, which the rule
 * refuses: a value the scan cannot see is a value nobody reviewed.
 */
function parametersOf(body, at) {
  if (body[at] !== ',') return { kind: 'none' };
  const i = afterSpace(body, at + 1);
  if (body[i] !== '{') return { kind: 'other' };
  const close = closeIndex(body, i);
  if (close === -1 || argumentEnd(body, close + 1) === -1) return { kind: 'other' };
  const entries = literalEntries(body.slice(i + 1, close));
  return entries === null ? { kind: 'other' } : { kind: 'object', entries };
}

/**
 * An object literal's entries, each a plain `name: value` whose value is a
 * quoted string or a boolean -- or null when any entry is some other shape,
 * which is the refusal above. The name has to be the event's own key spelled
 * out: a value built at run time is exactly what §7.1's ban list is about.
 */
function literalEntries(text) {
  const entries = [];
  let i = 0;
  while (i < text.length) {
    while (i < text.length && /[\s,]/.test(text[i])) i += 1;
    if (i >= text.length) break;
    const name = /^([A-Za-z_$][\w$]*)\s*:\s*/.exec(text.slice(i));
    if (name === null) return null;
    i += name[0].length;
    const value = /^'(?:[^'\\]|\\.)*'|^"(?:[^"\\]|\\.)*"|^(?:true|false)\b/.exec(text.slice(i));
    if (value === null) return null;
    i += value[0].length;
    entries.push([name[1], value[0].replace(/^['"]|['"]$/g, '').replace(/\\(.)/g, '$1')]);
    const separator = /^\s*(?:,|$)/.exec(text.slice(i));
    if (separator === null) return null;
    i += separator[0].length;
  }
  return entries;
}

/**
 * Whether a call's parameters are the ones the event's own entry declares:
 * exactly its keys -- none missing, none besides -- and every value a boolean
 * or a member of that parameter's closed set. This is §7.1's "**no free text at
 * all**" held where it can be read: `logEvent('screen_shown', { screen:
 * message.text })` carries a message's text inside an allowed name, and it is
 * the parameters, not the name, that would send it.
 */
function declaredParameters(name, read) {
  const declared = EVENT_PARAMETERS[name];
  const keys = Object.keys(declared);
  if (read.kind === 'none') return keys.length === 0;
  if (read.kind !== 'object' || read.entries.length !== keys.length) return false;
  return read.entries.every(([key, value]) => {
    if (!Object.hasOwn(declared, key)) return false;
    const set = declared[key];
    return set === 'boolean' ? value === 'true' || value === 'false' : set.includes(value);
  });
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
 * The ways a file can give the name `breadcrumb` a meaning of its own: a
 * declaration, a destructured name, a parameter, an arrow's parameter. An
 * import binding cannot be redeclared in the same scope, but a nested scope can
 * shadow it -- a parameter, a block's `const`, a function inside the call's own
 * function -- and a text scan cannot tell which scope a call sits in, so a file
 * that gives the name a meaning anywhere is refused rather than trusted.
 */
const BREADCRUMB_DECLARATIONS = [
  /\bfunction\s*\*?\s*breadcrumb\b/,
  /\bclass\s+breadcrumb\b/,
  /\b(?:const|let|var)\s+breadcrumb\b/,
  /\b(?:const|let|var)\s*\{[^}]*\bbreadcrumb\b/,
  /\b(?:const|let|var)\s*\[[^\]]*\bbreadcrumb\b/,
  /\(\s*breadcrumb\s*[,)=]/,
  /,\s*breadcrumb\s*[,)=]/,
  // A name taken out of a parameter, which is a binding and not a value: `{
  // breadcrumb, other }` and `{ breadcrumb = fallback }` bind, while
  // `{ breadcrumb: 1 }` is only a property of an object somebody passes. The
  // whole-import clause is not this shape -- its `{` follows the word `import`,
  // not a `(` or a `,` -- so the legitimate binding is not what is refused.
  /[,(]\s*\{[^}]*\bbreadcrumb\b\s*[,}=]/,
  /\bbreadcrumb\s*=>/,
];

/**
 * Whether the `breadcrumb` a file calls is the module's own: an import that
 * binds that name from a specifier the file's own directory resolves to
 * `BREADCRUMBS_MODULE`, **and no other meaning of the name anywhere in the
 * file**. The name a call reaches is the import's only while nothing else in
 * the file has a `breadcrumb` of its own, and that is as far as a text scan
 * sees: a file that declares the name at all is refused, so a call in a nested
 * scope that shadowed the import cannot pass on the strength of an import
 * somewhere above it.
 */
function importsBreadcrumb(body, file) {
  if (BREADCRUMB_DECLARATIONS.some((pattern) => pattern.test(body))) return false;
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
    name: 'every event sent names the allow-list and carries the parameters its own entry declares',
    // Every helper that sends an event, not only the one the module calls: the
    // SDK's `logScreenView` and friends send an event of their own, and the
    // reader below refuses them for what they are passed.
    pattern: /\blog[A-Z]\w*\s*\(/g,
    // The module that holds the list is the one file whose parameters are not
    // read here: they are `telemetryEvent`'s own output, already refused for a
    // name, a key or a value the closed sets do not carry, which is the test
    // this exemption hands the claim to.
    allow: ['src/telemetry/events.js'],
    argument: (read, body, _file, at) =>
      read.kind === 'literal' &&
      EVENT_NAMES.includes(read.value) &&
      declaredParameters(read.value, parametersOf(body, at)),
    controls: [
      "analytics().logEvent('account_exported', { size: 1 });",
      "analytics().logEvent(name, parameters);",
      'analytics().logEvent(`screen_${screen}`, {});',
      'analytics().logEvent();',
      'analytics().logScreenView({ screen_name: message.text });',
      'analytics().logSearch({ search_term: message.text });',
      // The name is on the list and the value is not: the message's own text,
      // sent inside an event nobody would look at twice.
      "analytics().logEvent('screen_shown', { screen: message.text });",
      // The same, through the identifier a screen would hold.
      "analytics().logEvent('screen_shown', { screen });",
      // A key the event does not declare, and the one declared key spelled
      // under another name: the entry's shape, not only its values.
      "analytics().logEvent('account_added', { kind: 'manual', project: account.fugaroProject });",
      "analytics().logEvent('app_launch', { launch: true });",
      // A declared parameter left out.
      "analytics().logEvent('account_added', {});",
      // A value outside the parameter's closed set, a screen the app does not
      // have, and a boolean parameter given a string.
      "analytics().logEvent('command_refused', { reason: 'because' });",
      "analytics().logEvent('screen_shown', { screen: 'settings' });",
      "analytics().logEvent('app_launch', { first: 'yes' });",
      // Nothing at all where the event declares a parameter.
      "analytics().logEvent('screen_shown');",
      // The whole message as the parameters: the SDK would carry its fields.
      "analytics().logEvent('screen_shown', message);",
      // Shapes the reader cannot see into: a spread, and a nested object.
      "analytics().logEvent('app_launch', { ...parameters });",
      "analytics().logEvent('app_launch', { first: { ok: true } });",
    ],
    accepted: [
      { file: 'src/telemetry/allowed.js', source: "analytics().logEvent('app_launch', { first: true });" },
      { file: 'src/app/allowed.js', source: "analytics().logEvent('screen_shown', { screen: 'mailbox' });" },
      // The two events that carry nothing are sent with nothing.
      { file: 'src/app/allowed.js', source: "analytics().logEvent('account_switched');" },
    ],
    why: 'D256: §7.2 is a closed allow-list of seven names with the parameters each declares, and a name the app invents — or a value nobody reviewed — is a leak D256 refused',
  },
  {
    name: 'no identifier or free-text key is set, and no parameter is set as a default',
    pattern:
      /setUserId\s*\(|setUserPropert(?:y|ies)\s*\(|setAttributes?\s*\(|setDefaultEventParameters\s*\(/g,
    controls: [
      "crashlytics().setUserId(account.authUid);",
      "crashlytics().setUserProperty('project', account.fugaroProject);",
      "analytics().setUserProperties({ project: account.fugaroProject });",
      "crashlytics().setAttribute('screen', screen);",
      "crashlytics().setAttributes({ outcome });",
      "analytics().setDefaultEventParameters({ project: account.fugaroProject });",
    ],
    accepted: [],
    why: 'D256: a free value beside a report is a third channel off the device, and a default parameter rides on every event the app sends besides',
  },
  {
    name: 'a crash log names a breadcrumb from the module’s list, or comes from the redactor',
    // Any `log(`, not the one spelling of the receiver: the SDK object kept in a
    // variable and logged through later is the way a port is wired, and the
    // modular API's `log(instance, ...)` is the same door again. The argument is
    // what this rule is about, so the receiver is not read.
    pattern: /\blog\s*\(|recordError\s*\(/g,
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
      // The same door by another route: the SDK object in a variable, logged
      // through a line later -- the way a crash port actually gets wired.
      {
        file: 'src/app/offender.js',
        source: 'const c = crashlytics();\nc.log(message.text);',
      },
      // ... and the modular API, where the instance is the first argument.
      'log(getCrashlytics(), message.text);',
      // The crash path's other door: an error's own message is text the app
      // built, so it is held to the same rule as a breadcrumb.
      'crashlytics().recordError(new Error(message.text));',
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
      // The module's import, and a `breadcrumb` of the file's own beside it:
      // the import is real, but a nested scope shadows it, and the scan cannot
      // tell which one the call reaches. A file that gives the name a meaning
      // at all is refused.
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb } from '../telemetry/breadcrumbs.js';\n" +
          'export function f(message) {\n' +
          '  const breadcrumb = (text) => text;\n' +
          '  return crashlytics().log(breadcrumb(message.text));\n' +
          '}',
      },
      // The same shadow as a parameter ...
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb } from '../telemetry/breadcrumbs.js';\n" +
          'export function f(breadcrumb) {\n' +
          '  return crashlytics().log(breadcrumb(message.text));\n' +
          '}',
      },
      // ... and as a name taken out of a parameter, which binds it just as a
      // declaration does -- and which the parameter list's own `(` hides from
      // the spellings above.
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb } from '../telemetry/breadcrumbs.js';\n" +
          'export function f({ breadcrumb }, crashlytics, message) {\n' +
          '  return crashlytics().log(breadcrumb(message.text));\n' +
          '}',
      },
      // ... and as a block's own declaration.
      {
        file: 'src/app/offender.js',
        source:
          "import { breadcrumb } from '../telemetry/breadcrumbs.js';\n" +
          'export function f(message) {\n' +
          '  if (message.text) {\n' +
          '    let breadcrumb = (text) => text;\n' +
          '    crashlytics().log(breadcrumb(message.text));\n' +
          '  }\n' +
          '}',
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
      // The receiver is not what this rule reads: the same constant through a
      // held object is the same breadcrumb.
      { file: 'src/app/allowed.js', source: "const c = crashlytics();\nc.log('screen_mailbox');" },
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
      const read = firstArgument(body, match.index + match[0].length);
      if (!rule.argument(read, body, file, read.end)) {
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

test('the allow-list’s own file is exempt because it really does call the port', () => {
  // Rule 1 allows `events.js` because the parameters that reach its port have
  // been through `telemetryEvent`. That is an exemption about a file that
  // really does call the port, and the exemption is what stands between the
  // module and a refusal: take it away and the module is the offence.
  const [EVENTS_RULE] = RULES;
  assert.deepEqual(offences({ ...EVENTS_RULE, allow: [] }, tree()), ['src/telemetry/events.js']);
});

test('the breadcrumb list is not vacuous: every screen the app has is a breadcrumb', () => {
  // §7.3's constants are the module's own list, and a screen the app can reach
  // has one: a list that named no screen the app has would be a redaction rule
  // about a vocabulary nothing uses.
  for (const screen of ['overview', 'jobs', 'mailbox', 'voice', 'text']) {
    assert.ok(BREADCRUMBS.includes(`screen_${screen}`), `screen_${screen}`);
  }
});
