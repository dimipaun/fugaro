// The manifest's dependencies, against the imports the app actually makes
// (the task that declared the Expo SDK; D125, Global Constraint 10).
//
// **Why this file exists.** `mobile/package.json` declared **no** runtime
// dependency until this task: the app was written and tested without the
// toolchain, every platform module was handed to `src/` as an argument, and the
// one file that imports the real ones -- `index.js` -- was never run (see
// `mobile/README.md`, "What is not verified here", and `src/skeleton/index.test.js`,
// which pinned the empty manifest as the red step's own claim). The owner ran
// `eas build --profile internal` and EAS could not detect an SDK at all, because
// a manifest with no `expo` in it names no SDK. The dependencies are declared
// now; this file is what keeps them honest.
//
// **What "honest" means, and why only a scan can say it.** A dependency list
// and an import graph are two files that drift apart silently: a new screen
// adds `import { Camera } from 'expo-camera'` and nothing in this repository
// notices until a bundle fails on a device -- which is the failure the owner
// already paid for once. Neither half is checkable on its own:
//
//   * **the three the SDK is made of.** `expo`, `react` and `react-native` are
//     not ordinary dependencies. `expo` is the SDK: its major version *is* the
//     SDK version, and `npx expo install` picks the other two from it. A
//     manifest missing any of the three is a manifest EAS cannot build (which
//     is how this task started), so each is asserted by name.
//   * **every bare import is declared.** A specifier that is not a path --
//     `expo-secure-store`, `@react-native-async-storage/async-storage`,
//     `react-native` -- resolves into `node_modules`, so it must be a package
//     `npm install` writes there. The scan reads **every** specifier in
//     `index.js` and under `src/`, tests excluded, takes the package name out
//     of it (`expo/status-bar` is the package `expo`; a scoped package keeps
//     its first two segments) and fails on the first one the manifest does not
//     declare. That is what makes "the list is complete" a fact rather than a
//     reading of five import lines.
//   * **the lockfile `npm ci` installs is the manifest's.** A `package.json`
//     edited without re-running `npm install` leaves a lockfile that no longer
//     matches it, and `npm ci` -- the install a build machine runs -- refuses
//     the pair rather than resolving it. The two root entries are compared
//     entry for entry, so the manifest and the lock cannot disagree here, and
//     `lockfileVersion` is 3.
//
// **The comments come out first, and that is not a convenience.** This tree's
// prose names packages the app deliberately does **not** use --
// `src/audio/index.js` explains the port shape by naming `expo-av` and
// `react-native-voice`, and `src/jobs/index.js` names `firebase`. A scan of the
// raw text would demand dependencies for modules the app refuses to have. So the code is
// taken out of its comments (the same `code()` shape `src/boundary.test.js`
// uses, and the same limitation: it is not a parser), and both halves are
// controlled -- the walk must flag a planted import, and must not flag the same
// line in prose -- before it is trusted against the tree.
//
// **The positive control is a real one.** `offences()` is a function of a
// manifest and a map of file -> text, and the first test hands it an undeclared
// import and an incomplete manifest and requires both to come back as
// offences. A green run against the tree below therefore means the checker can
// fail, which is the one thing a scan over a list can otherwise never say.
//
// **What this file cannot see**, stated rather than implied: that the versions
// are the ones the SDK wants (that is `npx expo install`'s job, and the PR body
// says which SDK was chosen), that the installed tree resolves them (that is
// `npx expo export`), and whether a module works on a device (no test here can
// -- README, "What is not verified here").

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, sep } from 'node:path';
import test from 'node:test';

const ROOT = join(import.meta.dirname, '..');
const MANIFEST_PATH = join(ROOT, 'package.json');
const LOCK_PATH = join(ROOT, 'package-lock.json');

const MANIFEST = JSON.parse(readFileSync(MANIFEST_PATH, 'utf8'));
const LOCK = JSON.parse(readFileSync(LOCK_PATH, 'utf8'));

/** The entry `npm ci` starts from, and the reason the lockfile exists at all. */
const LOCK_ROOT = LOCK.packages?.[''] ?? {};

/** Directories that are not this app's source (the same list the boundary scan keeps). */
const SKIP = new Set(['node_modules', '.git', '.expo', 'dist', 'android', 'ios']);

/**
 * Every `.js` file the app has, app-relative and sorted: the entry, then
 * whatever `src/` holds. **Tests are excluded** -- they import `node:test`,
 * `node:assert/strict` and each other, and a test's imports are not what a
 * bundle resolves.
 */
function sourceFiles(dir = ROOT, found = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!SKIP.has(entry.name)) sourceFiles(path, found);
      continue;
    }
    if (!entry.name.endsWith('.js') || entry.name.endsWith('.test.js')) continue;
    const name = relative(ROOT, path).split(sep).join('/');
    // The entry is `index.js` at the root of the package; everything else must
    // be under `src/`. Both halves are the app; nothing else here is.
    if (name === 'index.js' || name.startsWith('src/')) found.push(name);
  }
  return found;
}

/**
 * A file's code, without its comments -- the same shape `src/boundary.test.js`
 * uses, and for the same reason (see the header). Not a parser: a `//` inside a
 * string literal would strip the rest of its line.
 */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

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

/**
 * The package a bare specifier names: the whole thing, minus any subpath
 * (`expo/status-bar` is `expo`), and with both segments of a scoped name
 * (`@react-native-async-storage/async-storage` is not the package
 * `@react-native-async-storage`).
 *
 * `null` for what a package cannot answer for: a relative or absolute path is
 * resolved against the file that names it, and `node_modules` is not consulted
 * for it at all.
 *
 * A `node:` builtin is **not** given its own branch, and that is deliberate: it
 * takes the same path as any other bare specifier, no package in `package.json`
 * can answer for it (the runtime does), and a React Native bundle has no such
 * module -- so a production file that imported one is reported here, which is
 * the honest outcome. `mobile/README.md` records the app's own answer to the
 * same problem: `src/mailbox/sha256.js` is a pure-JS SHA-256 written because
 * `node:crypto` is not there.
 */
function packageOf(specifier) {
  if (specifier.startsWith('.') || specifier.startsWith('/')) return null;
  if (specifier.startsWith('@')) return specifier.split('/').slice(0, 2).join('/');
  return specifier.split('/')[0];
}

/** The three the SDK is made of, and what a build does without each. */
const FOUNDATION = ['expo', 'react', 'react-native'];

/**
 * Every offence in a manifest and a set of files: a foundation package that is
 * not declared, and every bare import no declared package answers for. A
 * function of its two arguments so that the test below can plant one of each
 * and watch both come back -- the positive control.
 *
 * `dependencies` alone answers for an import, and **not `devDependencies`**:
 * everything `index.js` and `src/` import ships in the app, and a package in
 * `devDependencies` is one a production install (`npm ci --omit=dev`, and the
 * install a build machine may make) does not put in `node_modules` -- so a
 * runtime import listed as a dev dependency is a bundle that fails after the
 * install, not before it.
 */
function offences(manifest, files) {
  const declared = new Set(Object.keys(manifest.dependencies ?? {}));
  const found = FOUNDATION.filter((name) => !declared.has(name)).map((name) => `declared: ${name}`);
  for (const [file, text] of Object.entries(files)) {
    for (const specifier of specifiers(text)) {
      const name = packageOf(specifier);
      if (name !== null && !declared.has(name)) found.push(`${file}: ${specifier}`);
    }
  }
  return found;
}

test('the checker flags a missing package, in code but not in prose', () => {
  // The positive control. Without it, "every import is declared" would be an
  // assertion a broken scan also satisfies -- which is exactly the state the
  // manifest was in when EAS could not find an SDK.
  const files = {
    'index.js': "import { Camera } from 'expo-camera';\n",
    'src/app/screen.js': "// import { Voice } from 'react-native-voice' in a comment is prose\nconst x = 1;\n",
    'src/feed/reader.js': "import { read } from './reader.js';\n",
  };
  // The three the SDK is made of, declared: what every other case below varies.
  const full = { dependencies: Object.fromEntries(FOUNDATION.map((name) => [name, '1.0.0'])) };
  assert.deepEqual(offences({ dependencies: { expo: '^58.0.16', react: '19.3.0' } }, files), [
    'declared: react-native',
    'index.js: expo-camera',
  ]);
  // ... and the same tree is clean once the manifest answers for both: the
  // prose is not read, and a relative path is not a package.
  assert.deepEqual(offences({ dependencies: { ...full.dependencies, 'expo-camera': '~17.0.0' } }, files), []);
  // ... a runtime import listed as a DEV dependency is still an offence: a
  // production install does not write `devDependencies` into `node_modules`.
  assert.deepEqual(
    offences({ dependencies: full.dependencies, devDependencies: { 'expo-camera': '~17.0.0' } }, {
      'src/camera.js': "import { Camera } from 'expo-camera';\n",
    }),
    ['src/camera.js: expo-camera'],
  );
  // ... a scoped name is one package, not its scope, and a subpath is not a
  // package of its own.
  assert.deepEqual(
    offences(full, {
      'src/x.js': "import A from '@react-native-async-storage/async-storage';\nimport B from 'expo/status-bar';\n",
    }),
    ['src/x.js: @react-native-async-storage/async-storage'],
  );
  // ... and a builtin is not a package this app can declare, so the same rule
  // reports it: no dependency can answer for `node:crypto` in a bundle that has
  // no Node runtime under it.
  assert.deepEqual(
    offences(full, {
      'src/y.js': "import { createHash } from 'node:crypto';\n",
    }),
    ['src/y.js: node:crypto'],
  );
});

test('the walk finds the app, and the entry is in it', () => {
  const files = sourceFiles();
  // A scan over an empty walk would pass for the wrong reason (the same reason
  // `src/boundary.test.js` asserts its walk before it trusts it).
  assert.ok(files.length >= 10, `expected the app's source, found ${files.length} files`);
  assert.ok(files.includes('index.js'), 'the entry is scanned: it is where the real SDKs are imported');
  assert.ok(files.includes('src/app/root.js'), 'and so is the shell');
  assert.ok(files.includes('src/skeleton/index.js'), 'and the skeleton');
  assert.equal(files.some((file) => file.includes('.test.js')), false, 'no test file is scanned');
  assert.equal(files.some((file) => file.startsWith('node_modules/')), false);
});

test('the SDK is declared: expo, react and react-native are all in the manifest', () => {
  const dependencies = MANIFEST.dependencies ?? {};
  for (const name of FOUNDATION) {
    assert.equal(typeof dependencies[name], 'string', `${name} must be a dependency: EAS reads the SDK from it`);
  }
  // `expo` is the SDK, so its major version is the SDK version and the SDK the
  // owner builds with is the one this manifest names. `npx expo install` picks
  // the rest from it; the count is here so a stray older or newer major in the
  // same file is visible.
  const sdk = Number(/^[\^~]?(\d+)\./.exec(dependencies.expo)?.[1]);
  assert.ok(Number.isInteger(sdk) && sdk > 0, `expo's version names an SDK: ${dependencies.expo}`);
  assert.equal(dependencies['expo-secure-store'] !== undefined, true, 'D111 stores both items through expo-secure-store');
  assert.match(
    dependencies['@react-native-async-storage/async-storage'] ?? '',
    /^\d/,
    'D114 keeps the installation tuple in AsyncStorage, so the package is declared',
  );
});

test('every bare import under index.js and src/ is a declared dependency', () => {
  const files = Object.fromEntries(sourceFiles().map((file) => [file, readFileSync(join(ROOT, file), 'utf8')]));
  assert.deepEqual(offences(MANIFEST, files), [], 'declare each import in package.json, or drop the import');
});

test('the lockfile is the manifest npm ci installs, and it is lockfileVersion 3', () => {
  assert.equal(LOCK.lockfileVersion, 3);
  assert.equal(LOCK.name, MANIFEST.name);
  assert.equal(LOCK_ROOT.name, MANIFEST.name);
  // Entry for entry, the two root records are the same list: a manifest edited
  // without `npm install` leaves a lockfile `npm ci` refuses.
  assert.deepEqual(LOCK_ROOT.dependencies ?? {}, MANIFEST.dependencies ?? {});
  // ... and the tree itself is in the lockfile: one entry per declared package,
  // so the lock is not an empty skeleton naming versions nothing resolves.
  const locked = LOCK.packages ?? {};
  for (const name of Object.keys(MANIFEST.dependencies ?? {})) {
    assert.ok(
      Object.keys(locked).some((path) => path === `node_modules/${name}`),
      `${name} is in the locked tree`,
    );
  }
});
