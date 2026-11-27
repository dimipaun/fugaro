// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The continuity module's one door (task K3; D2796, D2784, F15, O16).
//
// Everything a caller needs from this directory, re-exported here and nowhere
// else: the paired list the phone keeps and syncs, the silent restore that
// rebuilds sessions on a new device, and the two refusal vocabularies. The
// modules behind it are `sync.js` (the list) and `restore.js` (the restore).
//
// **What is deliberately NOT exported.** The request builders and parsers
// (`buildPairedListRead`, `buildPairedListWrite`, `buildChallengeRequest`,
// `buildRestoreRequest`, `parsePairedListResponse`, `parseChallengeResponse`,
// `parseRestoreResponse`, and the `send` behind them): a screen that could
// build a request could hand an identity's ID token to a URL of its own, which
// is the egress `src/boundary.test.js` allows a named few files and no more.
// The entry-level helpers (`readEntries`, `wireEntry`, `wireEntries`,
// `pairedListItem`) stay out for the same reason in miniature: they are how a
// body is composed, and the two functions below compose it.
//
// **`consumerKeyFingerprint` is here, and it is the one export with a second
// consumer in mind.** It is O16's derivation -- what the phone pins a project's
// consumer key by -- and M2's pairing asks the same question of the key a pair
// answer returns that this module asks of a restore answer. Until the
// derivation has a home in C1's shared package (it is a contract-level
// derivation, like `audienceOf` and `identityId`), this door is where the one
// implementation lives, and a second spelling of it anywhere is a phone that
// pins one fingerprint and checks another.

export {
  PAIRED_LIST_MAX_BYTES,
  PAIRED_LIST_MAX_ENTRIES,
  PAIRED_LIST_PREFIX,
  PAIRED_LIST_ROUTE,
  SYNC_CODES,
  PairedListStore,
  isHttpsEndpoint,
  isPairedProjectId,
  recordPairing,
} from './sync.js';

export {
  CONSUMER_KEY_BYTES,
  RESTORE_CODES,
  consumerKeyFingerprint,
  restoreAll,
  restoreIdentity,
  restoreProject,
} from './restore.js';
