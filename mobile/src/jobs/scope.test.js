// The job list's scope, ENFORCED rather than observed (mobile plan, Task P3.3;
// resolved 2026-10-17, tensions (a) and (b1); D119, D124, D125).
//
// **What this scan is for.** The plan's own added test case for this task is
// "no run title is on screen and no code path asks for one (a grep-style test
// over `mobile/src/jobs/` for `agents` and `outcomes` paths)", and the same
// sentence's resolution lists what else the list does not show: repositories,
// queued rows, finished rows, a PR link. None of those is a property any
// behavioural test can hold on its own -- a screen can be perfect and one line
// can reach for the document's repository block tomorrow -- so they are held
// the way P3.1's boundary scan holds the credentials: as **tokens that may
// appear in a named list of files and nowhere else**, asserted over the
// module's source, with a positive control per rule so a mistyped pattern
// cannot pass for a clean tree.
//
// **The rules, and what each is for.**
//
//   1. `agents` / `outcomes` -- the two families the read grant does not name
//      (tension (b1)). A path, a family comparison or a field keyed by either
//      is a request the rules would deny, and P3.2's own case is that such a
//      request is **never made** rather than made and refused.
//   2. `repos` -- the document's repository block. `config/caps/repos/$slug`
//      and `spend/$day/repos/$slug` *are* granted, so this token is not a
//      permission question: it is the resolution's "no repositories", and a
//      later task that wants those rows edits this list on purpose.
//   3. The PR fields, and every way of opening a link. The runs bucket carries
//      `pr_url`/`pr_number` and the grant does not cover it; and P0.3's
//      validator (`shared/url/index.js`) is the gate for any link the app ever
//      opens, so a link built here would be a link built around the gate.
//   4. The feed is reached through its **door** (`src/feed/index.js`), which is
//      that module's own rule for screens: `paths.js`'s gate and `nodes.js`'s
//      raw readers are its business, and a screen that composed a path would be
//      composing a read.
//
// **What the rules judge: the module's production files.** Test files are
// exempt (a test that proves a denied family is never requested has to name it,
// and this file's controls are the forbidden tokens written as strings), and so
// is this file. The behavioural half of the same claims is `list.test.js`'s,
// which watches the port rather than the source.
//
// The scan reads CODE and not prose (`code()` below), the same rule P3.1's and
// P3.6's scans use: this tree's comments discuss the very tokens the rules
// forbid -- this header does.

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import test from 'node:test';

const ROOT = join(import.meta.dirname, '..', '..');
const SELF = relative(ROOT, import.meta.filename).split(sep).join('/');
const JOBS = 'src/jobs/';

/** Every `.js` file under `src/jobs/`, app-relative, sorted. */
function moduleFiles() {
  const dir = join(ROOT, 'src', 'jobs');
  return readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith('.js'))
    .map((entry) => JOBS + entry.name)
    .sort();
}

const isTest = (file) => file.endsWith('.test.js');

/** The code of a file, without its comments: the rules are about code paths,
 * and every file here explains them in prose. Not a parser, and the limitation
 * is P3.1's `boundary.test.js`'s: a `//` inside a string literal would strip the
 * rest of its line, and the controls below prove each pattern still matches real
 * code. */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/** Every module specifier a file's code names. */
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

/** The rules. `allow` lists the production files the token may appear in;
 * test files are always allowed, and so is this file. */
const RULES = [
  {
    name: 'no run is named, and neither family the grant denies is asked for',
    pattern: /\b(agents|outcomes)\b/,
    allow: [],
    control: "const p = `agents/${slug}/${run}`;",
    why: 'tension (b1): the user claim reads neither, so no path, key or family here may name one',
  },
  {
    name: 'no repository row and no repository name',
    pattern: /\brepos\b/,
    control: "const slug = document.repos[0].slug;",
    allow: [],
    why: 'the resolution of 2026-10-17: the list shows numbers and states only, and repositories are not among them',
  },
  {
    name: 'no PR link is built, and no link is opened',
    pattern: /\b(pr_url|prUrl|pr_number|prNumber|Linking|openURL|openExternal|WebBrowser)\b/,
    control: "const href = run.pr_url;",
    allow: [],
    why: 'the rows that carry one live in the runs bucket, and P0.3\'s validator is the gate for any link the app ever opens',
  },
];

/** Apply one rule to a map of path -> text, returning the offences. */
function offences(rule, files) {
  return [...files]
    .filter(([file]) => file !== SELF && !isTest(file) && !rule.allow.includes(file))
    .filter(([, text]) => rule.pattern.test(code(text)))
    .map(([file]) => file);
}

/** The module's files, as path -> text. */
function sources() {
  return moduleFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]);
}

test('the scan walks the module, and not an empty list', () => {
  const files = moduleFiles();
  // A scan that found nothing would pass every rule below for the wrong reason.
  assert.ok(files.length >= 5, `expected the module's source, found ${files.length} files`);
  for (const expected of [
    'src/jobs/index.js',
    'src/jobs/fields.js',
    'src/jobs/row.js',
    'src/jobs/heartbeat.js',
    'src/jobs/list.js',
    'src/jobs/lines.js',
  ]) {
    assert.ok(files.includes(expected), `${expected} is in the walk`);
  }
  assert.ok(files.includes(SELF), 'this file is in the walk, which is why it exempts itself');
  assert.equal(files.some((file) => file.startsWith(JOBS + 'node_modules')), false);
});

for (const rule of RULES) {
  test(`${rule.name} — ${rule.why}`, () => {
    // The positive control first: the rule must flag a planted example, in real
    // code, or a green run below would mean nothing.
    const example = { 'src/jobs/offender.js': `// ${rule.control} in a comment is fine\nfunction offence() { ${rule.control} }\n` };
    assert.deepEqual(offences(rule, new Map(Object.entries(example))), ['src/jobs/offender.js'], rule.control);
    // ... and the planted line is not flagged when it is only prose, which is
    // what lets this tree's comments keep explaining the rules.
    assert.deepEqual(offences(rule, new Map([['src/jobs/prose.js', `// ${rule.control}\n`]])), [], rule.control);
    // ... and a test file is exempt, which is what lets `list.test.js` name the
    // denied families in order to prove they are never requested.
    assert.deepEqual(offences(rule, new Map([[`${JOBS}offender.test.js`, `function offence() { ${rule.control} }\n`]])), []);

    // ... and then the module itself.
    assert.deepEqual(offences(rule, new Map(sources())), [], `${rule.name}: see the allow-list above`);
  });
}

test('the feed is reached through its door, and nowhere else in it', () => {
  // Only the feed's own directory is judged: the door is `../feed/index.js`,
  // every other module of the feed's is an escape, and a specifier that is not
  // the feed's at all (a sibling, a builtin) is none of this rule's business.
  const escape = (file, spec) =>
    spec.startsWith('../feed/') && spec !== '../feed/index.js' ? [`${file}: ${spec}`] : [];
  // Positive controls: a sibling module of the feed's is an offence, in each
  // import form.
  for (const line of [
    "import { isGranted } from '../feed/paths.js';",
    "export { decode } from '../feed/document.js';",
    "const m = await import('../feed/nodes.js');",
    "const m = require('../feed/view.js');",
  ]) {
    const spec = line.match(/['"]([^'"]+)['"]/)[1];
    assert.deepEqual(escape('src/jobs/x.js', spec), [`src/jobs/x.js: ${spec}`], line);
  }
  assert.deepEqual(escape('src/jobs/x.js', '../feed/index.js'), []);
  assert.deepEqual(escape('src/jobs/x.js', './row.js'), []);
  assert.deepEqual(escape('src/jobs/x.js', 'node:test'), []);

  const offenders = sources()
    .filter(([file]) => file !== SELF && !isTest(file))
    .flatMap(([file, text]) => specifiers(text).filter((spec) => spec.startsWith('../feed/')).flatMap((spec) => escape(file, spec)));
  assert.deepEqual(offenders, [], "a module of the feed's is reached around its door");
  // The door itself is reached: the scan is not vacuous.
  assert.ok(
    sources().some(([, text]) => specifiers(text).includes('../feed/index.js')),
    'nothing imports the feed at all',
  );
});

test('the module\'s specifiers resolve inside the app', () => {
  // Every relative specifier lands under `mobile/` -- the app's own tree or the
  // shared package (D119, D125). P3.1's `boundary.test.js` asserts this over the
  // whole app; this is the module's own half, so a file added here is checked
  // even if that scan is ever narrowed.
  const app = ROOT;
  const sharedDir = resolve(join(ROOT, '..', 'shared'));
  const bad = [];
  for (const [file, text] of sources()) {
    for (const spec of specifiers(text)) {
      if (!spec.startsWith('.')) continue;
      const target = resolve(join(app, dirname(file)), spec);
      const inApp = target === app || target.startsWith(app + sep);
      const inShared = target === sharedDir || target.startsWith(sharedDir + sep);
      if (!inApp && !inShared) bad.push(`${file}: ${spec}`);
    }
  }
  assert.deepEqual(bad, []);
});

/** The names a file's `export { … } from '…'` blocks carry, sorted. */
function doorNames(text) {
  const names = new Set();
  for (const block of code(text).matchAll(/export\s*\{([^}]*)\}\s*from/g)) {
    for (const entry of block[1].split(',')) {
      const name = entry.trim().split(/\s+as\s+/).pop().trim();
      if (name.length > 0) names.add(name);
    }
  }
  return [...names].sort();
}

/** The names in `names` that no file other than the door uses. */
function deadNames(names, files, door = 'src/jobs/index.js') {
  const rest = [...files].filter(([file]) => file !== door);
  return names.filter((name) => !rest.some(([, text]) => new RegExp(`\\b${name}\\b`).test(code(text))));
}

test('the door carries the module\'s surface, and no dead name', () => {
  // The door's surface as an **exact set**, the rule P3.2's `index.test.js`
  // gives: a re-export added without a thought is how an internal helper ends
  // up reachable from a screen. This is the whole of it -- the field table and
  // its vocabularies, the row, the heartbeat, the list and its wiring, and what
  // the screen says.
  const files = sources();
  const door = files.find(([file]) => file === 'src/jobs/index.js')[1];
  assert.deepEqual(doorNames(door), [
    'HEARTBEAT_STATES',
    'JOB_FAMILIES',
    'JOB_FIELDS',
    'JobList',
    'KILL_STATES',
    'LIST_CODES',
    'NOTHING',
    'NOT_SHOWN',
    'NO_REASON',
    'READ_STATES',
    'ROW_FIELDS',
    'SERIES',
    'createJobList',
    'heartbeatRow',
    'jobField',
    'jobList',
    'killState',
    'lines',
    'projectRow',
    'text',
  ]);

  // ... and a positive control for the rule below, before it is applied: a name
  // on the door that nothing else uses is exactly what that rule is for.
  const planted = new Map([
    ['src/jobs/index.js', "export { GHOST } from './fields.js';\n"],
    ['src/jobs/fields.js', 'const x = 1;\n'],
  ]);
  assert.deepEqual(deadNames(['GHOST'], [...planted]), ['GHOST']);

  // Every name the door carries is used somewhere that is not the door. A dead
  // export is a name a reviewer has to check is still true: `PRINTED_FIELDS`
  // and `NO_VALUE` were both, and both are gone.
  assert.deepEqual(deadNames(doorNames(door), files), []);
});
