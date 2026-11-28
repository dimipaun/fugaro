// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The continuity module's door, asserted as an exact set (task K3).
//
// A screen names what it mounts: the paired list, the restore and the two
// refusal vocabularies. The request builders, the parsers and the entry-level
// helpers stay off the door, and the test says so -- a screen that could build
// a request could hand an identity's ID token to a URL of its own.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as continuity from './index.js';

test('the door exports the list, the restore and the two vocabularies, and no request builder', () => {
  assert.deepEqual(Object.keys(continuity).sort(), [
    'CONSUMER_KEY_BYTES',
    'PAIRED_LIST_MAX_BYTES',
    'PAIRED_LIST_MAX_ENTRIES',
    'PAIRED_LIST_PREFIX',
    'PAIRED_LIST_ROUTE',
    'PairedListStore',
    'RESTORE_CODES',
    'SYNC_CODES',
    'consumerKeyFingerprint',
    'isHttpsEndpoint',
    'isPairedProjectId',
    'recordPairing',
    'restoreAll',
    'restoreIdentity',
    'restoreProject',
  ]);
  // The egress belongs to the module that carries a token: a builder or a
  // parser on the door would be a second place a byte could leave from.
  for (const member of [
    'buildPairedListRead',
    'buildPairedListWrite',
    'buildChallengeRequest',
    'buildRestoreRequest',
    'parsePairedListResponse',
    'parseChallengeResponse',
    'parseRestoreResponse',
    'readPairedList',
    'writePairedList',
    'readEntries',
    'wireEntry',
    'wireEntries',
    'pairedListItem',
    'localEntry',
  ]) {
    assert.equal(member in continuity, false, member);
  }
});
