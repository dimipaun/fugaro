// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two halves over one account: the switches, and the message that carries
// them (mobile plan, Task P5.2.8; the design is
// docs/mobile/design/push-notifications.md §3.5 (b) and §3.8).
//
// **Why a flip publishes, rather than only being stored.** §3.8's switches are
// honoured **server-side** -- a switch that never left the phone silences
// nothing -- and the app has no way to write the push node itself (§3.4,
// D166). So the only way a switch can mean anything is the mailbox message the
// token rides in, and that makes "the owner flipped a switch" and "the phone
// registered" one act: `setControl` stores the record and then registers, and
// answers both outcomes, so a screen can say "saved here, not yet sent"
// instead of claiming a silence the server has never heard of.
//
// **One object per open account, over the settings store the app already
// builds.** Two accounts are two doors and two records (D114's separate
// entries), and a door is dropped when the account is switched: the only state
// it holds is the last outcome, which belongs to the account that asked.
//
// **Nothing here decides anything.** The store, the switches, the token, the
// message and the refusal codes are all in the modules this composes; the door
// is the order they are asked in.

import { createPushControls } from './controls.js';
import { createPushRegistration } from './registration.js';
import { pushScreen } from './screen.js';

/**
 * The app's push door: the switches of one account, and the registration that
 * carries them.
 *
 * @param {object} options
 * @param {object} options.client the mailbox client (the app's one write path)
 * @param {object} options.settings the app's settings store
 * @param {string} options.accountId D114's installation id
 * @param {() => (string|null)} [options.zone] the device's IANA time zone
 * @param {object} options.token the token source
 * @param {string} options.platform `ios` or `android`
 * @returns {object} the frozen door
 * @throws {TypeError} from any of the three constructors below, for a port or
 *   an account id that cannot be used
 */
export function createPush({ client, settings, accountId, zone, token, platform } = {}) {
  const controls = createPushControls({ settings, accountId, zone });
  const registration = createPushRegistration({ client, controls, token, platform });

  /**
   * A stored change, published: the record's own answer, with the registration
   * that carried it. `ok` is true when the SWITCHES were stored -- `published`
   * is where "and the server has heard" is answered, because the two can
   * disagree and a screen that showed one for both would be lying.
   */
  async function publishing(stored) {
    if (!stored.ok) return stored;
    return Object.freeze({ ok: true, prefs: stored.prefs, published: await registration.register() });
  }

  return Object.freeze({
    ok: true,
    key: controls.key,
    prefs: () => controls.read(),
    register: () => registration.register(),
    state: () => registration.state(),
    setControl: async (name, value) => publishing(await controls.setControl(name, value)),
    setQuietHours: async (window) => publishing(await controls.setQuietHours(window)),
    clearQuietHours: async () => publishing(await controls.clearQuietHours()),
    reset: async () => publishing(await controls.reset()),
    screen: async () => {
      const switches = await controls.read();
      return pushScreen({
        outcome: registration.state(),
        ...(switches.ok ? { prefs: switches.prefs } : {}),
      });
    },
  });
}
