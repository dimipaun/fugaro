// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The telemetry plan's §7.4 scan, as the owner's ruling of 2026-10-30 (D731)
// amends it (review-levels-ab-jobmax T3.1; D734, D738; and T0.1, which records
// the amendment).
//
// **What §7.4 said, and what it says now.** The plan's own text was written for
// D255's always-on answer: "no call that sets collection state", because a call
// that can set it *is* the switch the app was said not to have. D731 supersedes
// that answer -- crash reports are on by default and **opt-out**, and Analytics
// is **dropped from the app** -- so the scan is two rules now:
//
//   1. **`setAnalyticsCollectionEnabled(` is refused anywhere**, exactly as
//      before. It is not "the app may not turn Analytics off": there is no
//      Analytics left to turn off, and the call is refused because reaching it
//      would mean the package came back.
//   2. **`setCrashlyticsCollectionEnabled(` is admitted at exactly one call
//      site** -- `src/telemetry/crash.js`'s -- and refused anywhere else. One is
//      the whole of D731's opt-out on the phone: the switch is the project's
//      policy member, read in that one file, and a second call site would be a
//      second place that decides what the app sends. Nothing is sent before the
//      switch is read, so "one call site" is also the claim that nothing on a
//      screen can turn collection on for itself.
//   3. **No Analytics package, and no import of one.** The app's manifest must
//      not declare `@react-native-firebase/analytics` and no file may import it
//      -- that import is what this task's red step finds on the tree it starts
//      from, and the whole of "Analytics is dropped" is that both are absent.
//
// `notice.test.js` (P7.2) holds the first rule over `notice.js` alone; the
// rest of §7.4 is here, beside it, because P7.3's `analytics-calls.test.js` --
// the scan the plan expected to carry this rule -- was written to D255 and is
// being re-scoped to the Analytics half in the same change. Like its siblings
// this scan reads CODE and not prose, exempts the test files and itself, and
// proves every rule against a planted line before it judges the tree.

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

/** The code of a file, without its comments: this tree's prose names the very
 * tokens the rules forbid. */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/** The one call site the crash switch is allowed, and the file it is in. */
const CRASH_CALL = 'setCrashlyticsCollectionEnabled';
const CRASH_CALL_FILE = 'src/telemetry/crash.js';

/** The name whose reappearance anywhere would be Analytics coming back. */
const ANALYTICS_CALL = 'setAnalyticsCollectionEnabled';

/** The package D731 drops, in the manifest and in an import alike. */
const ANALYTICS_PACKAGE = '@react-native-firebase/analytics';

/** The call sites of `name` in one file's code. */
function callSites(name, text) {
  return [...code(text).matchAll(new RegExp(`\\b${name}\\s*\\(`, 'g'))].length;
}

/**
 * Every way the amended §7.4 is broken by a map of path -> text, as a list of
 * sentences. An empty list is the posture D731 answers; each sentence names the
 * file and the rule, so a planted control says which rule caught it.
 */
function scanFailures(files) {
  const out = [];
  for (const [file, text] of files) {
    if (file === SELF || isTest(file)) continue;
    const body = code(text);
    if (callSites(ANALYTICS_CALL, body) > 0) {
      out.push(`${file} calls ${ANALYTICS_CALL}: D731 drops Analytics from the app, and a call that sets its collection state is the package reaching the build again`);
    }
    if (body.includes(ANALYTICS_PACKAGE)) {
      out.push(`${file} names ${ANALYTICS_PACKAGE}: D731 drops Analytics from the app, so neither the manifest nor an import may carry it`);
    }
    const crashCalls = callSites(CRASH_CALL, body);
    if (file === CRASH_CALL_FILE) {
      if (crashCalls !== 1) {
        out.push(`${file} carries ${crashCalls} ${CRASH_CALL} call sites: the app's crash-report switch is decided in exactly one place, and the file that decides it is this one`);
      }
    } else if (crashCalls > 0) {
      out.push(`${file} calls ${CRASH_CALL}: the one call site that sets the app's collection state is ${CRASH_CALL_FILE}, so a second one is a second place that decides what the app sends`);
    }
  }
  return out;
}

/** The tree, read once per test. */
function tree() {
  return new Map(sourceFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]));
}

test('scan: exactly one setCrashlyticsCollectionEnabled call site, no analytics import', () => {
  // The controls first, so a green run below is the tree passing rather than
  // the scan matching nothing: each rule is planted in the shape it refuses.
  assert.deepEqual(
    scanFailures(new Map([['src/app/offender.js', 'crashlytics().setCrashlyticsCollectionEnabled(true);\n']])),
    ['src/app/offender.js calls setCrashlyticsCollectionEnabled: the one call site that sets the app\'s collection state is src/telemetry/crash.js, so a second one is a second place that decides what the app sends'],
  );
  assert.deepEqual(
    scanFailures(new Map([[CRASH_CALL_FILE, "import crashlytics from '@react-native-firebase/crashlytics';\n"]])) .filter((line) => line.includes('call sites')),
    [`${CRASH_CALL_FILE} carries 0 ${CRASH_CALL} call sites: the app's crash-report switch is decided in exactly one place, and the file that decides it is this one`],
  );
  assert.deepEqual(
    scanFailures(new Map([['src/app/offender.js', 'analytics().setAnalyticsCollectionEnabled(false);\n']])),
    [`src/app/offender.js calls ${ANALYTICS_CALL}: D731 drops Analytics from the app, and a call that sets its collection state is the package reaching the build again`],
  );
  assert.deepEqual(
    scanFailures(new Map([['src/app/offender.js', `import analytics from '${ANALYTICS_PACKAGE}';\n`]])),
    [`src/app/offender.js names ${ANALYTICS_PACKAGE}: D731 drops Analytics from the app, so neither the manifest nor an import may carry it`],
  );
  // ... and the planted line is not flagged when it is only prose, because the
  // tree's own comments name every one of these tokens.
  for (const planted of [
    `// crashlytics().${CRASH_CALL}(true);\n`,
    `// import analytics from '${ANALYTICS_PACKAGE}';\n`,
  ]) {
    assert.deepEqual(scanFailures(new Map([['src/app/prose.js', planted]])), [], planted);
  }
  // The control that the two crash rules are not one rule: the admitted file
  // with its one call site passes.
  assert.deepEqual(
    scanFailures(new Map([[CRASH_CALL_FILE, `sdk.${CRASH_CALL}(verdict.enabled);\n`]])),
    [],
  );

  // ... and then the tree itself.
  assert.deepEqual(scanFailures(tree()), []);
});

test('scan: the walk reads the app, and the file the one call site is admitted in', () => {
  const files = sourceFiles();
  assert.ok(files.length >= 10, `expected the app's source, found ${files.length} files`);
  for (const expected of [
    'index.js',
    'app.config.js',
    CRASH_CALL_FILE,
    'src/telemetry/notice.js',
    'src/telemetry/events.js',
  ]) {
    assert.ok(files.includes(expected), `${expected} is in the walk`);
  }
  assert.ok(files.includes(SELF), 'this file is in the walk, which is why it exempts itself');
  assert.equal(files.some((file) => file.startsWith('node_modules/')), false);
  // The admitted file really does carry the ONE call site the rule allows: a
  // scan whose admitted file had none would admit nothing and prove nothing.
  assert.equal(callSites(CRASH_CALL, readFileSync(join(ROOT, CRASH_CALL_FILE), 'utf8')), 1);
});

test('scan: the Analytics package is gone from the manifest as well as the source', () => {
  // The red step of this task, read from the two places a package lives: the
  // app's own dependency list, and the lockfile `npm ci` installs from. The
  // source half is the rule above; this is the half a bundler resolves.
  const manifest = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'));
  const declared = [
    ...Object.keys(manifest.dependencies ?? {}),
    ...Object.keys(manifest.devDependencies ?? {}),
  ];
  assert.ok(declared.length > 5, `package.json declares ${declared.length} dependencies: this scan is reading the wrong file`);
  assert.equal(declared.includes(ANALYTICS_PACKAGE), false, `mobile/package.json still declares ${ANALYTICS_PACKAGE}`);
  assert.ok(declared.includes('@react-native-firebase/crashlytics'), 'the crash reporter is what is left, so this scan is about a real removal');
  const lock = JSON.parse(readFileSync(join(ROOT, 'package-lock.json'), 'utf8'));
  assert.equal(
    Object.hasOwn(lock.packages, `node_modules/${ANALYTICS_PACKAGE}`),
    false,
    `mobile/package-lock.json still carries ${ANALYTICS_PACKAGE}: npm ci would install what the manifest no longer declares`,
  );
  assert.deepEqual(
    lock.packages[''].dependencies?.[ANALYTICS_PACKAGE],
    undefined,
    'the lockfile root entry still declares the Analytics package',
  );
});
