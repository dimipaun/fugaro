// The app skeleton's own test (mobile plan, Task P3.1).
//
// It is written BEFORE the module it imports, on purpose, exactly as
// `shared/skeleton/index.test.js` is: that is this task's red step. `npm test`
// from `mobile/` discovers this file through the package's glob and fails with
// ERR_MODULE_NOT_FOUND until `mobile/src/skeleton/index.js` exists. A run that
// found no test at all would have exited 0 and proved nothing -- `node --test`
// exits 0 over a glob that matched nothing -- so the first test file is what
// makes the glob real rather than assumed. `mobile/README.md` says why the glob
// is rooted at `src/`.
//
// Four checks, all about the artifact rather than about logic:
//
//   * the module and the manifest name the same package, so a rename cannot
//     leave the two disagreeing;
//   * the package is private, which is D125's "its own artifact with its own
//     release path": nothing here is ever published to npm, so nothing here is
//     a public API with a compatibility promise (the same ruling D119 keeps
//     for `shared/`);
//   * the test script is a GLOB, which is the plan's "shared files" rule for
//     this manifest -- `mobile/package.json` is P3.1's alone, so every later
//     task (P3.2's `src/feed/`, P3.3's `src/jobs/`, P3.4's `src/audio/`,
//     P3.5's `src/voice/`, P3.6's `src/mailbox/`) lands its own tests without
//     editing this manifest. A script that named its test files would collapse
//     that parallelism, which is why the mutation below is a named one; and
//   * the app declares no runtime dependency yet, and this test is where that
//     is pinned rather than left to be noticed: P3.1's verification needs no
//     device and no install (Global Constraint 10), and a manifest declaring
//     Expo without a lockfile that `npm ci` could satisfy would be a claim
//     about a toolchain nobody in this repository has run. The task that first
//     needs the device runtime adds the dependencies, the lockfile and its own
//     reason here.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { SKELETON } from './index.js';

const manifest = JSON.parse(
  readFileSync(new URL('../../package.json', import.meta.url), 'utf8'),
);

test('the skeleton module names the package it belongs to', () => {
  assert.equal(SKELETON, manifest.name);
});

test('the app is private: D125 gives it its own release path, never npm', () => {
  assert.equal(manifest.private, true);
});

test('the test script is a glob, so no later task edits this manifest', () => {
  const script = manifest.scripts?.test;
  assert.equal(typeof script, 'string');
  assert.match(
    script,
    /\*\*\/\*\.test\.js/,
    'P3.1 owns mobile/package.json (the plan\'s "shared files" table): the script must discover every module\'s tests through a glob, so P3.2-P3.6 add files and never this manifest',
  );
});

test('the app declares no runtime dependency yet', () => {
  assert.equal(manifest.dependencies, undefined);
  assert.equal(manifest.devDependencies, undefined);
});
