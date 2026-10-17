// The skeleton's own test (mobile plan, Task P0.1).
//
// It is written BEFORE the module it imports, on purpose. That is what proves
// the test script's glob instead of assuming it: `npm test` from `shared/`
// discovers this file one directory below the package root and fails with
// ERR_MODULE_NOT_FOUND until `shared/skeleton/index.js` exists. A run that
// found no test at all would have exited 0 and proved nothing -- which is the
// one way this package's runner can lie, so the first test file is what makes
// the glob real.
//
// Both checks below are about the skeleton itself, not about logic:
//
//   * the module and the manifest name the same package, so a rename cannot
//     leave the two disagreeing; and
//   * the package is private, which is D119 keeping D50's "no npm package" --
//     nothing in this directory is ever published, so nothing here is a public
//     API with a compatibility promise.
//
// There is no third check. The modules that carry logic (P0.2's
// `shared/mailbox-schema/`, P0.3's `shared/url/`, then phase 1's five) arrive
// with their own tests beside them, and no later task edits this file.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { SKELETON } from './index.js';

const manifest = JSON.parse(
  readFileSync(new URL('../package.json', import.meta.url), 'utf8'),
);

test('the skeleton module names the package it belongs to', () => {
  assert.equal(SKELETON, manifest.name);
});

test('the package is private: D119 keeps D50 and never publishes it', () => {
  assert.equal(manifest.private, true);
});
