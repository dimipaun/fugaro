// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The feed's one door: its test (mobile plan, Task P3.2).
//
// The surface is asserted as an **exact set**, not as "these names are
// present", for the reason `src/accounts/index.test.js` gives: a re-export
// added without a thought is how a path composer, a raw node reader or a port
// ends up reachable from a screen. Here that matters twice over, because the
// gate (`paths.js`'s `request`) and the port are the two things that decide
// what the phone asks the database for -- so both are asserted **absent** by
// name rather than trusted to stay off the list.
//
// The last test is the door in use: a reader built from nothing but what this
// module exports, fed the recorded nodes, drawn. If a name a caller needs were
// missing, that test would not compile.

import assert from 'node:assert/strict';
import test from 'node:test';

import { ResourceRegistry } from '../accounts/resources.js';
import { recordedNodes, recordedPaths, FIXTURE_SLUG, FIXTURE_UID } from './fixtures/nodes.js';
import * as feed from './index.js';

const T0 = Date.parse('2025-11-09T12:00:00Z');
const DAY = feed.dayKey(T0);

test('the door exports the reader, the view, the document rules and the vocabulary', () => {
  assert.deepEqual(Object.keys(feed).sort(), [
    'AVAILABILITY_CODES',
    'BURN_WARMUP_MS',
    'BURN_WINDOW_MS',
    'BurnSampler',
    'CONNECTION_REASONS',
    'CONNECTION_STATES',
    'DEFAULT_BURN_HOURS',
    'DOCUMENT_CODES',
    'FAMILIES',
    'FeedReader',
    'FeedView',
    'GAP_MARK',
    'LIVENESS',
    'MAX_PATHS',
    'MICROS_PER_USD',
    'MODES',
    'NODE_CODES',
    'PATH_CODES',
    'READER_CODES',
    'REFUSED_NOTICE',
    'SKIP_NOTICE',
    'STALE_AFTER_MS',
    'STATE',
    'TEXT_MAX',
    'dayDate',
    'dayKey',
    'decode',
    'familyOf',
    'fast',
    'figures',
    'isDocument',
    'isEmpty',
    'isFigure',
    'isPermissionError',
    'isPort',
    'isUnavailable',
    'measured',
    'notional',
    'percent',
    'recompute',
    // Added by P3.3: `availability.js` names `shown` as the one function a
    // screen prints a figure with, and the job list is the first screen that
    // prints one -- so it reaches it here rather than reading the five states a
    // second time. Nothing else about the door moves for that task.
    'shown',
    'unavailable',
    'usd',
    'validateDocument',
  ]);
});

test('the door carries no port, no path composer and no path judgement', () => {
  // A screen that could compose a path could compose a read: the plan is the
  // reader's to build, and it is judged before the port sees it. `isGranted` is
  // the gate's own answer as a boolean, and it is off the door for the header's
  // reason: a screen that can **judge** a path is one refactor from composing
  // one, and this module's own header said it stayed off while the export was
  // still there.
  for (const name of ['capsPaths', 'killPaths', 'spendPaths', 'mailboxPaths', 'request', 'isGranted', 'checkRepositories', 'slugOf']) {
    assert.equal(name in feed, false, name);
  }
  // ... and there is no client on the door, because there is none in this
  // directory at all: the port arrives as an argument (`reader.js`).
  for (const name of ['port', 'client', 'database', 'rtdb', 'firebase', 'subscribe']) {
    assert.equal(name in feed, false, name);
  }
});

test('the vocabulary the door hands a screen has no word for a dead feed', () => {
  assert.deepEqual(feed.LIVENESS, ['no-document', 'live', 'stale', 'offline', 'refused', 'polling', 'degraded']);
  for (const word of feed.LIVENESS) {
    assert.equal(/dead|exited|stopped|killed/i.test(word), false, word);
  }
  // The five connection states a *document* may carry, and the two modes it may
  // name -- neither of which the phone can read, since `/config/mode` is not in
  // the grant (so a document this app assembles carries `mode: null`).
  assert.deepEqual(feed.CONNECTION_STATES, ['live', 'stale', 'offline', 'refused', 'polling']);
  assert.deepEqual(feed.MODES, ['observe', 'enforce']);
});

test('the door is enough to read the recorded nodes and draw them', () => {
  const nodes = recordedNodes({ day: DAY, atMs: T0 });
  const registry = new ResourceRegistry();
  const account = { id: 'acct-1', firebaseProjectId: 'fugaro-dev', authUid: FIXTURE_UID, fugaroProject: 'fugaro' };
  registry.open(account.id);
  const asked = [];
  const listeners = new Map();
  const port = {
    subscribe(path, { onValue }) {
      asked.push(path);
      listeners.set(path, onValue);
      return () => listeners.delete(path);
    },
  };
  const reader = new feed.FeedReader({ port, account, repositories: [FIXTURE_SLUG], registry, now: () => T0 });
  assert.equal(reader.start(T0).ok, true);
  // The recording is the plan, path for path -- and the day is the one the
  // clock named, which is the day the spend nodes are keyed by.
  assert.deepEqual(asked, [...recordedPaths({ day: DAY })]);

  // A Realtime Database listener delivers the node's current value the moment
  // it is attached, so the recording is delivered path by path: a node that is
  // not there arrives as `null`, which is an answer and not a silence.
  for (const path of asked) listeners.get(path)(nodes[path] ?? null, T0);

  const snapshot = reader.snapshot({ nowMs: T0 });
  assert.equal(snapshot.liveness, 'live');
  assert.equal(snapshot.broken, false);
  assert.equal(snapshot.degraded, false);
  assert.equal(snapshot.day, feed.dayDate(DAY));
  assert.equal(snapshot.drawable.total.counted.value, 12.5);
  assert.equal(snapshot.drawable.total.dailyCap.value, 50);
  assert.equal(snapshot.drawable.total.percent.value, 25);
  assert.equal(snapshot.drawable.repos[0].dailyCap.value, 5);
  // Every figure the door hands over is a figure, and none of them is a bare
  // number: a screen cannot add one to anything or print it without its state.
  for (const figure of snapshot.drawable.figures) {
    assert.equal(feed.isFigure(figure), true, JSON.stringify(figure));
    assert.ok(figure.value === null || Number.isFinite(figure.value), JSON.stringify(figure));
  }
  // The recorded heartbeat is one of the granted nodes and is delivered like
  // any other; what it *means* is P4.2's (check 84), not this task's.
  assert.equal(asked.includes(`mailbox/${FIXTURE_UID}/heartbeat`), true);
});
