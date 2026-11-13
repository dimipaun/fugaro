// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
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
// **What the screen is allowed to say.** The same rule the shell lives by: a
// sentence about who can add this phone, a sentence about what is missing, and
// the two sentences about the demo. Nothing from an account, because there is
// no account here yet.
//
// **The "ask your admin" sentence (docs/mobile-auth, Task P4; the spec's step
// 5).** The spec asks for one more thing, and this is the app's end of it: a
// person the installation will not have is told so, and told who can change it.
// It is drawn by the screen that has no installation, which is the state such a
// person is left in -- a pairing that did not complete stores nothing, so they
// are looking at this screen either way -- and a second screen saying the same
// thing in other words would be a second place for the sentence to drift. The
// plan's own owner-run check reads it that way: *"the first screen with no
// account ... the demo is reachable with no sign-in, and 'ask your admin'
// stands beside it"*.
//
// **Why it is not drawn from a refusal.** The plan reaches this state "from the
// pairing flow's refusal path", and **no refusal in this app carries that
// meaning**. The exchange's refusals are cause-blind -- `session.js` answers
// `sign-in-refused` for every failed sign-in, an offline phone and a dead or
// rotated token among them, and its own screen already draws *"The pairing token
// was not accepted"* for that code -- and v1's allowlist answer is the CLI's,
// before a QR exists at all (`internal/cli/pair.go` refuses an address the
// installation does not administer). A screen that read "not on the list" out of
// `sign-in-refused` would state a cause it cannot know, for causes it is not.
// So the sentence is the screen's own and it says what to do rather than what
// happened: why this phone has no installation is not something it can learn,
// and the person's next move is the same in every case. `createFirstScreen`
// refuses an option it does not know, so a later task cannot quietly re-wire
// this state to a code.
//
// **What this state is not**: it is not a check. Nothing here reads an
// allowlist, a token or a database -- an allowlist the phone could read would be
// a copy of the authority, not the authority.
//
// **What the sentence may never do.** It may not stand in front of the demo. The
// demo is the path for exactly the people this sentence is about -- a reviewer
// who cannot create an account and cannot pair a phone -- so it is drawn BESIDE
// the control and the control is drawn either way
// (`docs/mobile/play-review-access.md` promises Play's reviewers "no account,
// sign-in or network", and `src/demo/safety.test.js` holds that promise).

import { TRY_DEMO } from '../demo/index.js';

/** The element types this screen uses, in one place so a test can name them. */
export const FIRST_SCREEN = Object.freeze({
  ROOT: 'fugaro.first',
  PAIRING_SLOT: 'fugaro.first.pairing-slot',
  TRY_DEMO: 'fugaro.first.try-demo',
  HINT: 'fugaro.first.demo-hint',
  ASK_YOUR_ADMIN: 'fugaro.first.ask-your-admin',
});

/** The options this screen takes, so a caller cannot hand it one it does not use
 * and believe it wired something (the header's "Why it is not drawn from a
 * refusal": a pairing flow's refusal is exactly such an option). */
const OPTIONS = Object.freeze(['React', 'View', 'Text', 'Pressable', 'onTryDemo', 'pairingScreen']);

/**
 * What the spec's step 5 asks a phone to be told: who adds a phone to an
 * installation, and what to do. The words "ask your admin" are the spec's and
 * the owner-run check's, kept verbatim so the screen a person reads and the
 * sentence the check looks for cannot drift. What it deliberately does not say
 * is why this phone has no installation -- no sentence of this app's can know
 * that (the header's reason).
 */
export const ASK_YOUR_ADMIN = 'Only your admin can add this phone to an installation, so ask your admin to pair it.';

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
export function createFirstScreen(options) {
  if (options === null || typeof options !== 'object' || Array.isArray(options)) {
    throw new TypeError('createFirstScreen needs its options');
  }
  for (const name of Object.keys(options)) {
    if (!OPTIONS.includes(name)) throw new TypeError(`createFirstScreen: unknown option "${name}"`);
  }
  const { React, View, Text, Pressable, onTryDemo, pairingScreen = null } = options;
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
      // The spec's step 5, drawn FIRST and unconditionally: whoever has no
      // installation is owed the name of the person who can change that, and it
      // is above the demo's sentences so that it is read before them -- never
      // instead of the control below (the header's "What the sentence may never
      // do").
      React.createElement(Text, { type: FIRST_SCREEN.ASK_YOUR_ADMIN }, ASK_YOUR_ADMIN),
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
