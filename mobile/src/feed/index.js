// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The feed's one door (mobile plan, Task P3.2; D112's answer, tension (b1)).
//
// Everything a screen needs from this directory, re-exported here and nowhere
// else, so that P3.3's job list imports `src/feed/index.js` and gets the reader,
// the view and the vocabulary -- not `nodes.js`'s raw readers or `paths.js`'s
// gate, which are this directory's own business.
//
// **What is deliberately NOT on the door.**
//
//   * **A port.** There is no database client in this directory, and there must
//     not be: `reader.js` takes a `{ subscribe(path, { onValue, onError }) }`
//     and hands the handles to the account's `ResourceRegistry`. The one file
//     that knows what a device is is `mobile/index.js` (P3.1's entry), which
//     builds the account store and mounts the root component; nothing anywhere
//     constructs a `FeedReader` or a database client yet, and that wiring is
//     **P3.3's** (the job list is the first screen that needs a feed). A screen
//     that could import a port could open a second subscription to the same
//     project, authenticated as whichever account was last signed in, which is
//     D114's "no cross-leakage" undone by an import.
//   * **The path composers, and the path judgements.** `capsPaths`,
//     `killPaths`, `spendPaths` and `mailboxPaths` are how the plan is built; a
//     screen that composed a path itself would be composing a read. `request`
//     (the gate) and `isGranted` (the same judgement as a boolean) stay off the
//     door for that reason and for the header's own: a screen that can **judge**
//     a path is one refactor from composing one, and the judgement is the
//     reader's to make, once, before anything is asked for.
//   * **The `--repo` flag and a repository list built by discovery.** The
//     repository list is the owner's (`reader.js`'s constructor), because the
//     grant does not cover `config/caps/repos` and the phone cannot enumerate.
//
// **What a caller may do.** Read `snapshot()` and draw `drawable`, or say
// `liveness` -- never `document.total.counted.value` off a snapshot whose
// `drawable` is `null`, which is the whole point of `drawable` existing: the
// day-break and the degraded document both make it `null`, so a screen cannot
// draw the zero §10.7 forbids by forgetting a check.

// `shown` is on the door for P3.3: the job list is the first screen that prints
// a figure, and `availability.js` says the one function a screen uses is this
// one -- so a screen reaches it here rather than through a second reading of
// the five states. Nothing else about the vocabulary moves.
export { AVAILABILITY_CODES, GAP_MARK, STATE, isFigure, isEmpty, measured, notional, shown, unavailable } from './availability.js';

export { FAMILIES, PATH_CODES, dayDate, dayKey, familyOf } from './paths.js';

export {
  CONNECTION_STATES,
  DOCUMENT_CODES,
  MODES,
  TEXT_MAX,
  decode,
  figures,
  isDocument,
  isUnavailable,
  validateDocument,
} from './document.js';

export { MICROS_PER_USD, NODE_CODES, usd } from './nodes.js';

export { BURN_WARMUP_MS, BURN_WINDOW_MS, DEFAULT_BURN_HOURS, BurnSampler, fast, percent, recompute } from './recompute.js';

export {
  LIVENESS,
  REFUSED_NOTICE,
  SKIP_NOTICE,
  STALE_AFTER_MS,
  FeedView,
} from './view.js';

export { CONNECTION_REASONS, MAX_PATHS, READER_CODES, FeedReader, isPermissionError, isPort } from './reader.js';
