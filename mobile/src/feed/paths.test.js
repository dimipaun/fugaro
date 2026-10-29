// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The grant's fence (mobile plan, Task P3.2; D112's answer, tension (b1)).
//
// The plan's added test case for P3.2 is the first one here: **no request is
// ever made for `agents/**` or `outcomes/**`**. That is a statement about the
// gate, so it is tested at the gate: the reader's own test
// (`reader.test.js`) then watches a port that records every path it is handed,
// which is the only way to prove the request was never *made* rather than
// merely denied.
//
// The rest of the file is the four families spelled out, plus the shapes
// *inside* a granted family that are not granted (`config/caps/repos` itself,
// `config/caps/limits`, `spend/<day>/meta`) -- those are the ones a reader
// would reach for while enumerating, and the reason the reader takes a slug
// list instead.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DENIED_FAMILIES,
  FAMILIES,
  PATH_CODES,
  capsPaths,
  checkRepositories,
  dayDate,
  dayKey,
  dayOf,
  familyOf,
  isGranted,
  killPaths,
  mailboxPaths,
  request,
  spendPaths,
} from './paths.js';

const UID = 'AbC123def456';

test('no path outside the four families is granted, and the denied ones are named', () => {
  for (const path of [
    'agents/example-one-0123456789abcdef',
    'agents/example-one-0123456789abcdef/run-1',
    'outcomes/20401/example-one/run-1',
    'runs/example-one/run-1',
    'fugaro/project',
  ]) {
    const judged = request(path);
    assert.equal(judged.ok, false, path);
    assert.equal(judged.code, PATH_CODES.DENIED_FAMILY, path);
    assert.equal(judged.family, familyOf(path), path);
  }
  // Inside a granted family, a shape the grant does not name is `not-granted`:
  // the family is named, the node is not.
  for (const path of ['config/limits', 'config/mode', 'config/policy/repos/example-one', 'spend/20401/meta']) {
    const judged = request(path);
    assert.equal(judged.ok, false, path);
    assert.equal(judged.code, PATH_CODES.NOT_GRANTED, path);
    assert.equal(judged.family, familyOf(path), path);
  }
  // ... and the four families the grant names are the four this module names.
  assert.deepEqual([...FAMILIES].sort(), ['config/caps', 'config/kill', 'mailbox', 'spend']);
  assert.deepEqual([...DENIED_FAMILIES].sort(), ['agents', 'fugaro', 'outcomes', 'runs']);
});

test('every granted shape is granted, and nothing beside it is', () => {
  for (const path of [
    'config/caps/global',
    'config/caps/defaults',
    'config/caps/repos/example-one-0123456789abcdef',
    'config/kill/global',
    'config/kill/repos/example-one-0123456789abcdef',
    'spend/20401/global',
    'spend/20401/repos/example-one-0123456789abcdef',
    'spend/20401/runs/example-one-0123456789abcdef/run-1',
    `mailbox/${UID}/outbox`,
    `mailbox/${UID}/heartbeat`,
  ]) {
    assert.equal(isGranted(path), true, path);
  }

  for (const path of [
    // The parent of `$slug`: the grant is per child, so the phone cannot
    // enumerate repositories from the database. This is the one that makes the
    // reader take an owner-typed list instead.
    'config/caps/repos',
    'config/kill/repos',
    'spend/20401/repos',
    // A granted family, the wrong node.
    'config/caps/limits',
    'config/caps',
    'config/kill',
    'spend/20401',
    'spend/20401/global/extra',
    'spend/20401/meta',
    // The mailbox's inbox: the consumer, not the phone, collects what the phone
    // wrote, and the rules give the phone no read there.
    `mailbox/${UID}/inbox`,
    `mailbox/${UID}`,
    // Shapes that are not paths.
    '',
    '/config/caps/global',
    'config//caps/global',
    'config/caps/./global',
    'config/caps/../kill/global',
    'config/caps/global/',
    'agents',
  ]) {
    assert.equal(isGranted(path), false, path);
  }
});

test('a composed segment must be the shape the tree uses', () => {
  // A slug is `internal/task/task.go`'s: `[a-z0-9-]`, at most 63 characters.
  assert.equal(isGranted('config/caps/repos/Example-One'), false, 'a slug is lower-case');
  assert.equal(isGranted(`config/caps/repos/${'a'.repeat(64)}`), false, 'a slug is at most 63');
  assert.equal(isGranted(`config/caps/repos/${'a'.repeat(63)}`), true);
  assert.equal(request('config/caps/repos/Example-One').code, PATH_CODES.BAD_SLUG);

  // The uid names the mailbox node, and it is what the rules compare against
  // `auth.uid`: alphanumeric and bounded.
  assert.equal(isGranted(`mailbox/${'a'.repeat(129)}/outbox`), false);
  assert.equal(isGranted('mailbox/a-b/outbox'), false);
  assert.equal(isGranted('mailbox/a.b/outbox'), false);
  assert.equal(request('mailbox/a.b/outbox').code, PATH_CODES.BAD_UID);

  // The day is the tree's own key: digits, and no `%` or `-` an encoder could
  // have put there.
  assert.equal(isGranted('spend/020401/global'), false);
  assert.equal(isGranted('spend/-1/global'), false);
  assert.equal(isGranted('spend/20401.5/global'), false);
  assert.equal(request('spend/-1/global').code, PATH_CODES.BAD_DAY);
  assert.equal(isGranted('spend/20401/runs/example-one/run%2F1'), false);

  // A path that is not a string at all is refused, not coerced.
  for (const bad of [null, 7, {}, [], undefined]) {
    assert.equal(request(bad).code, PATH_CODES.NOT_A_PATH, String(bad));
  }
});

test('the day is the tree key, and its date spelling round-trips', () => {
  assert.equal(dayKey(0), 0);
  assert.equal(dayKey(86_400_000 - 1), 0);
  assert.equal(dayKey(86_400_000), 1);
  assert.equal(dayKey(-1), -1, 'the floor, not the truncation: -1ms is the day before');
  assert.equal(dayKey(-86_400_000), -1);
  assert.equal(dayKey(-86_400_001), -2);
  assert.equal(dayDate(0), '1970-01-01');
  assert.equal(dayDate(20401), '2025-11-09');
  assert.equal(dayOf('2025-11-09'), 20401);
  assert.equal(dayOf('1970-01-01'), 0);
  // A string the Date parser accepts but that is not that day back is not a
  // date: `2025-02-30` would otherwise become March 2nd.
  assert.equal(dayOf('2025-02-30'), null);
  assert.equal(dayOf('2025-11-1'), null);
  assert.equal(dayOf('not a date'), null);
  assert.equal(dayOf(20401), null);
  assert.throws(() => dayKey('now'), TypeError);
  assert.throws(() => dayDate('2025-11-10'), TypeError);
});

test('the composed paths are the granted shapes and nothing else', () => {
  const slug = 'example-one-0123456789abcdef';
  assert.deepEqual(capsPaths([slug]), ['config/caps/global', 'config/caps/defaults', `config/caps/repos/${slug}`]);
  assert.deepEqual(killPaths([slug]), ['config/kill/global', `config/kill/repos/${slug}`]);
  assert.deepEqual(spendPaths(20401, [slug]), ['spend/20401/global', `spend/20401/repos/${slug}`]);
  assert.deepEqual(mailboxPaths(UID), [`mailbox/${UID}/outbox`, `mailbox/${UID}/heartbeat`]);
  for (const path of [...capsPaths([slug]), ...killPaths([slug]), ...spendPaths(20401, [slug]), ...mailboxPaths(UID)]) {
    assert.equal(isGranted(path), true, path);
  }
  // The reader asks for nothing that is not in these four lists: no run node
  // (nothing enumerates a run id without `agents/**`) and no inbox.
  for (const path of [...spendPaths(20401, [slug]), ...mailboxPaths(UID)]) {
    assert.equal(/\/runs\//.test(path), false, path);
    assert.equal(/inbox/.test(path), false, path);
  }
});

test('the repository list is judged once, and a bad entry is refused not dropped', () => {
  const slug = 'example-one-0123456789abcdef';
  assert.deepEqual(checkRepositories([slug]).repositories, [slug]);
  assert.deepEqual(checkRepositories([]).repositories, []);
  assert.equal(checkRepositories([slug, slug]).ok, false, 'a duplicate is a doubled request');
  assert.equal(checkRepositories(['Example']).ok, false);
  assert.equal(checkRepositories([slug, '']).ok, false);
  assert.equal(checkRepositories('example').ok, false);
  assert.equal(checkRepositories(undefined).ok, false);
});
