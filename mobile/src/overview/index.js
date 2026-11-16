// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview module's one door (mobile plan, Task P6.8; D170, D114).
//
// Everything a screen or the entry needs from this directory, re-exported here
// and nowhere else, so a caller imports `src/overview/index.js` and cannot
// reach the pieces in an order the contract does not allow: `overviewTiles`
// builds the tiles from snapshots the caller already took, `createSwitcher`
// owns the one way an account changes, and the screen draws what those two
// produced.
//
// **What `createOverview` is for, and what it deliberately does not do.** It
// binds the `AccountStore` to the switcher and the four React primitives to the
// screen, so `mobile/index.js` (the one file that knows what a device is) can
// build the feature in one call. It holds **no feed view and no database
// client**: the snapshots come from the per-account `FeedView`s the caller
// already owns (P3.3's wiring), and a second subscription built here would be
// the cross-leakage D114's teardown exists to prevent -- one installation's
// feed, read while another account is open.
//
// It also names **no dependency of its own**: no port, no clock, no camera.
// The switcher's store arrives from the entry and a missing one is a
// `TypeError` at construction rather than at the first utterance.
//
// `mobile/src/app/first-screen.js` and the shell's screen list are where this
// screen gets wired; nothing wires it yet, and that line is the entry's.

import { createOverviewScreen } from './screen.js';
import { createSwitcher } from './switcher.js';

export { TILE_CODES, TILE_FIELDS, TILE_FIGURES, hasFigure, overviewTiles, tileOf } from './tiles.js';

export { SWITCH_CODES, createSwitcher } from './switcher.js';

export {
  EMPTY_SENTENCE,
  FIGURES,
  NO_DAY,
  OVERVIEW_TITLE,
  REFUSAL_SENTENCE,
  SCREEN,
  SWITCH_WORD,
  UNKNOWN_LIVENESS_SENTENCE,
  UNKNOWN_PROJECT,
  ageText,
  createOverviewScreen,
  freshnessSentence,
} from './screen.js';

/**
 * The whole overview feature, over the account store and the four React
 * primitives.
 *
 * `store` is `accounts/store.js`'s `AccountStore` -- the list, the current
 * account and the teardown. `React`, `View`, `Text` and `Pressable` are the
 * primitives `screen.js` takes. All five are required: a caller that forgot one
 * is told at construction.
 */
export function createOverview({ store, React, View, Text, Pressable }) {
  const switcher = createSwitcher({ store });
  const Screen = createOverviewScreen({ React, View, Text, Pressable });
  return Object.freeze({ switcher, Screen });
}
