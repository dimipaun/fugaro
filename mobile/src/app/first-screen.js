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
// **What the screen is allowed to say.** The same rule the shell lives by: the
// title, a sentence about what is missing, and the two sentences about the
// demo. Nothing from an account, because there is no account here yet.
//
// **The "ask your admin" state (docs/mobile-auth, Task P4; the spec's step 5).**
// The spec asks for one screen more, and this is it: a person the installation
// will not have is told so plainly and told who can change it. It is a STATE of
// this screen rather than a screen of its own, because a refused pairing leaves
// exactly what the first screen describes -- no installation -- and a second
// screen saying the same thing in different words is a second place for the
// sentence to drift.
//
// **How the state is reached, and which refusal it is.** The pairing flow hands
// its refusal here (`pairingRefusal`, the `{ code }` object `session.js`
// refuses with), and the one refusal that means "the installation would not have
// this phone" is the exchange's: in v1 the app holds no provider session, so the
// question "may this phone be paired" is answered by the project's Auth, and a
// phone the installation will not have is a phone whose pairing token is not
// accepted (`sign-in-refused`). Every other refusal is the payload's own
// reading, the network's, or the owner's list, each already has its sentence on
// the pairing screen, and none of them is a sentence about a person's standing.
// **What this state is not**: it is not a check. Nothing here reads an
// allowlist, a token or a database -- an allowlist the phone could read would be
// a copy of the authority, not the authority -- and the CLI checks an
// administered address before it mints a code at all (`internal/cli/pair.go`),
// so this is the half of the answer a phone can be told, not the whole of it.
//
// **What the state may never do.** It may not stand in front of the demo. The
// demo is the path for exactly the people this state is about -- a reviewer who
// cannot create an account and cannot pair a phone -- so the sentence is drawn
// BESIDE the control and the control is drawn either way
// (`docs/mobile/play-review-access.md` promises Play's reviewers "no account,
// sign-in or network", and `src/demo/safety.test.js` holds that promise).

import { TRY_DEMO } from '../demo/index.js';
import { SESSION_CODES } from '../pairing/index.js';

/** The element types this screen uses, in one place so a test can name them. */
export const FIRST_SCREEN = Object.freeze({
  ROOT: 'fugaro.first',
  PAIRING_SLOT: 'fugaro.first.pairing-slot',
  TRY_DEMO: 'fugaro.first.try-demo',
  HINT: 'fugaro.first.demo-hint',
  ASK_YOUR_ADMIN: 'fugaro.first.ask-your-admin',
});

/** The refusals that mean the installation would not have this phone. One code
 * today, and it is the exchange's: `session.js` refuses `sign-in-refused` when
 * the project's Auth will not take the pairing token, which is the app's end of
 * the allowlist's answer (the header's reason). */
const NOT_ADMITTED = Object.freeze([SESSION_CODES.SIGN_IN_REFUSED]);

/**
 * What the screen says when the installation refused this phone: the spec's
 * step 5, and who can change it. "The installation's list" is the allowlist by
 * the only name the person reading this screen can act on, and the words "ask
 * your admin" are the spec's and the owner-run check's, kept verbatim so the
 * screen a person reads and the sentence the check looks for cannot drift.
 */
export const ASK_YOUR_ADMIN = 'This phone is not on the installation\'s list yet, so ask your admin to pair it.';

/**
 * What the screen says is missing. Deliberately not "something went wrong":
 * nothing has, and a new install is the normal case (D114's list starts empty).
 */
export const NO_INSTALLATION = 'No installation yet. Pairing a phone to an installation is not part of this build yet, so there is nothing real to show here.';

/** What the reviewer is about to enter, in one sentence, before they tap. */
export const DEMO_HINT = 'Built-in sample data: no account, no sign-in and no network.';

/** Is `value` the shape the pairing flow refuses with (`session.js`'s `refuse`)?
 * A refusal is a frozen object carrying a code, and nothing else is one: a
 * caller that passed `true`, or a code as a bare string, would otherwise draw a
 * sentence about a refusal that never happened. */
function isRefusal(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    && typeof value.code === 'string' && value.code !== '';
}

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
 * @param {object} [options.pairingRefusal] the pairing flow's refusal -- the
 *   `{ code }` object `session.js` refuses with -- or `null` when the flow has
 *   not refused. Only the refusal that means the installation would not have
 *   this phone changes what the screen says (`NOT_ADMITTED`).
 * @returns {(state: object) => object} the screen, as a function of the demo
 *   state the shell passes it (inactive here: pressing the control is what
 *   changes that)
 */
export function createFirstScreen({ React, View, Text, Pressable, onTryDemo, pairingScreen = null, pairingRefusal = null }) {
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
  if (pairingRefusal !== null && !isRefusal(pairingRefusal)) {
    throw new TypeError('createFirstScreen: the pairing refusal is a refusal or nothing');
  }
  const refused = pairingRefusal !== null && NOT_ADMITTED.includes(pairingRefusal.code);

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
      // The state the refused pairing leaves the person in. It is drawn before
      // the demo's two sentences and the control is drawn after them: a person
      // who may not pair is still owed the way in that needs nothing.
      ...(refused ? [React.createElement(Text, { type: FIRST_SCREEN.ASK_YOUR_ADMIN }, ASK_YOUR_ADMIN)] : []),
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
