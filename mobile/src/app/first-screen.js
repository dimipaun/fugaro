// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The app's first screen, and the demo's front door (owner ruling of
// 2026-10-19).
//
// **Why this file exists at all.** The owner's ruling is that a reviewer --
// who cannot create an account and cannot pair a phone -- must be able to
// reach everything the app shows, and that the way in is a "Try the demo"
// control on the first screen that works with no setup. The screen that ruling
// means (P6.8's pairing/overview, where an installation is typed and paired)
// is **not on `main` yet**, so this is the smallest honest entry screen that
// can carry the control: it says what the app is waiting for, it says what the
// demo is, and it holds the place where P6.8's screen goes. This is stated in
// the pull request as well, because a reader of this file is not necessarily a
// reader of that.
//
// **The seam, and why it is a slot rather than a comment.** P6.8 will bring the
// pairing control, and it must arrive ABOVE the demo control rather than
// instead of it: the demo is the path for somebody who cannot pair, so a
// pairing screen that replaced it would break the ruling it was added beside.
// `pairingScreen` is that seam -- a function the shell's wiring passes today as
// nothing, and P6.8 passes its screen to. The slot is rendered either way, so a
// test can assert the place exists while it is empty.
//
// **What the screen is allowed to say.** The same rule the shell lives by: the
// title, a sentence about what is missing, and the two sentences about the
// demo. Nothing from an account, because there is no account here yet.

import { TRY_DEMO } from '../demo/index.js';

/** The element types this screen uses, in one place so a test can name them. */
export const FIRST_SCREEN = Object.freeze({
  ROOT: 'fugaro.first',
  PAIRING_SLOT: 'fugaro.first.pairing-slot',
  TRY_DEMO: 'fugaro.first.try-demo',
  HINT: 'fugaro.first.demo-hint',
});

/**
 * What the screen says is missing. Deliberately not "something went wrong":
 * nothing has, and a new install is the normal case (D114's list starts empty).
 */
export const NO_INSTALLATION = 'No installation yet. Pairing a phone to an installation is not part of this build yet, so there is nothing real to show here.';

/** What the reviewer is about to enter, in one sentence, before they tap. */
export const DEMO_HINT = 'Built-in sample data: no account, no sign-in and no network.';

/**
 * Build the first screen.
 *
 * @param {object} options
 * @param {object} options.React React, with `createElement`
 * @param {Function} options.View react-native's View
 * @param {Function} options.Text react-native's Text
 * @param {Function} options.Pressable react-native's Pressable
 * @param {Function} options.onTryDemo called when the control is pressed; the
 *   demo controller's `enter`
 * @param {Function} [options.pairingScreen] **the seam for P6.8**: a function
 *   returning the pairing screen's element, or `null` while that task is not
 *   merged. It is rendered above the demo control, never instead of it.
 * @returns {(state: object) => object} the screen, as a function of the demo
 *   state the shell passes it (inactive here: pressing the control is what
 *   changes that)
 */
export function createFirstScreen({ React, View, Text, Pressable, onTryDemo, pairingScreen = null }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createFirstScreen needs React (with createElement)');
  }
  for (const [name, part] of [['View', View], ['Text', Text], ['Pressable', Pressable]]) {
    if (typeof part !== 'function' && typeof part !== 'object') {
      throw new TypeError(`createFirstScreen needs react-native's ${name}`);
    }
  }
  if (typeof onTryDemo !== 'function') {
    throw new TypeError('createFirstScreen needs what the demo control does');
  }
  if (pairingScreen !== null && typeof pairingScreen !== 'function') {
    throw new TypeError('createFirstScreen: the pairing seam is a function or nothing');
  }

  return function FirstScreen() {
    return React.createElement(
      View,
      { type: FIRST_SCREEN.ROOT },
      // The place P6.8's pairing screen will occupy. Empty today, and rendered
      // anyway: an empty slot a test can see is a seam, and a `null` a reader
      // has to trust is a comment.
      React.createElement(
        View,
        { type: FIRST_SCREEN.PAIRING_SLOT },
        pairingScreen === null ? null : pairingScreen(),
      ),
      React.createElement(Text, { type: FIRST_SCREEN.HINT }, NO_INSTALLATION),
      React.createElement(Text, { type: FIRST_SCREEN.HINT }, DEMO_HINT),
      React.createElement(
        Pressable,
        { type: FIRST_SCREEN.TRY_DEMO, onPress: onTryDemo },
        // The one copy of the owner's words (the demo module's `TRY_DEMO`), so
        // the control a reviewer taps and the string the tests name cannot
        // drift apart.
        TRY_DEMO,
      ),
    );
  };
}
