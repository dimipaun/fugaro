// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The switch the app holds: the two settings and the consent record, over one
// account's storage, plus the badge they render (mobile plan, Task P5.3.6;
// D123, D127; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.1, §4.2 and §4.3).
//
// **One object per open account, over the settings store the app already
// builds.** `mobile/index.js` builds the AsyncStorage port and the app's
// `SettingsStore` over it; a voice switch is that store plus an account id, and
// it composes the one key `settings.js` owns. Two accounts on one store are two
// switches and two records -- D114's "separate, non-overlapping entries" --
// which is what makes an account switch a real switch rather than a change of
// what is displayed over one installation's stored preference.
//
// **The record is read on every question, and the caller's word is never the
// record.** `indicator` takes a direction, the hop's last project-status answer
// and this direction's last call -- and **not** a consent list: the consent it
// judges is the one this app recorded, read here, so an utterance cannot be
// made remote by a caller that asserts an agreement the device does not hold.
// That is the design's §4.3 division read literally: "the app refuses only what
// it can know by itself -- a provider for which it holds no recorded consent".
// The shared function still holds the rule; this file is where the app makes
// sure the only consent it can be handed is its own.
//
// **Nothing here writes the database, and nothing here holds a secret.** The
// whole write set of this module is one key of the non-secret settings store
// (`settings.test.js` asserts the key list, and the app's `write-path.test.js`
// still finds the mailbox client as the only writer of the database). No
// provider key is named anywhere in this directory: the design's point 5 puts
// it in the hop's Secret Manager, and `SECRET_ITEMS` stays the two items (D111,
// Global Constraint 4).

import { ENGINE, REFUSAL } from '../../../shared/voice-switch/index.js';
import { withConsent, withoutConsent } from './consent.js';
import { failClosedIndicator, renderIndicator } from './indicator.js';
import { DIRECTIONS, readVoiceRecord, voiceSettingsKey, writeVoiceRecord } from './settings.js';

/** The two engines, for the membership check `set` makes before it writes. */
const ENGINES = Object.freeze([ENGINE.LOCAL, ENGINE.REMOTE]);

/** The store methods this module calls: `read` needs the first, `set` the second. */
const STORE_METHODS = Object.freeze(['get', 'set']);

/**
 * The tail of each account's queue.
 *
 * Keyed by the settings store and then by the account id, and **not** held on
 * the instance: `createVoiceSwitch` is called where the app is wired, so two
 * switches over one store for one account are two doors onto one record, and a
 * queue per instance would let them interleave. The store is the WeakMap key so
 * a store that is dropped takes its queues with it, and the inner map holds one
 * entry per account id ever used over that store -- the app builds one store
 * and holds a handful of accounts, so it is bounded by the accounts, not by the
 * calls.
 */
const QUEUES = new WeakMap();

/**
 * Run `work` after every mutation queued for this account before it, and before
 * any queued after it -- the shape `AccountStore`'s own `#serial` has, for the
 * same reason (`../accounts/store.js`).
 *
 * **The three mutating methods await a store between reading the record and
 * writing it**, so two of them could each pass the other's read: `revokeConsent`
 * and `set` at once would write the record twice, the second write built on the
 * record the first one replaced, and the consent the user just took back would
 * come back with it -- `send` true for a provider the device no longer holds an
 * agreement for, which is the one thing this module exists to prevent. Running
 * them one at a time makes each read-and-write one step.
 *
 * `read` and `indicator` do not queue: they change nothing, and a read that
 * races a write sees one record or the other. What `work` answers -- a refusal
 * included -- is passed through unchanged, and a rejection of its own does not
 * stall the queue: the next mutation runs either way.
 */
function serialize(settings, accountId, work) {
  let byAccount = QUEUES.get(settings);
  if (byAccount === undefined) {
    byAccount = new Map();
    QUEUES.set(settings, byAccount);
  }
  const previous = byAccount.get(accountId) ?? Promise.resolve();
  const run = previous.then(work, work);
  byAccount.set(
    accountId,
    run.then(
      () => undefined,
      () => undefined,
    ),
  );
  return run;
}

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The three fields a caller owns, and no others.
 *
 * `settings` and `consent` are deliberately absent: they are read from the
 * record, and a context that carries either is not consulted -- an agreement
 * the device does not hold cannot be asserted into a badge, and a setting the
 * store does not hold cannot be asserted into one either. A field read through
 * a throwing getter leaves the fields after it undefined, which the shared
 * function refuses into the fail-closed badge like any other unreadable input.
 */
function ownedFields(context) {
  const asked = { direction: undefined, status: undefined, lastCallInDirection: undefined };
  if (context === null || typeof context !== 'object') return asked;
  try {
    asked.direction = context.direction;
    asked.status = context.status;
    asked.lastCallInDirection = context.lastCallInDirection;
  } catch {
    // Whatever was read before the throw stands; the refusal is upstream.
  }
  return asked;
}

/**
 * One account's switch: read it, set a direction, record or revoke a consent,
 * and render its badge.
 *
 * Constructed rather than exported, so the door hands a caller one thing
 * (`createVoiceSwitch`) and the store and the account id stay the two arguments
 * a test can choose. Both are required, and a missing or wrongly-shaped one
 * throws: a switch built without a store is a switch that fails at the first
 * thing the owner says, which is the rule `createMailboxClient` and
 * `createVoiceScreen` both hold. A bad account id throws too -- `settings.js`
 * composes the storage key from it, and an account whose key cannot be composed
 * must not reach the store as a switch that quietly reads elsewhere.
 */
class VoiceSwitch {
  #settings;
  #accountId;

  constructor({ settings, accountId }) {
    if (settings === null || typeof settings !== 'object') throw new TypeError('a settings store is required');
    for (const method of STORE_METHODS) {
      if (typeof settings[method] !== 'function') {
        throw new TypeError(`a settings store is required: it has no ${method}()`);
      }
    }
    voiceSettingsKey(accountId);
    this.#settings = settings;
    this.#accountId = accountId;
  }

  /** The two settings and the consent record, or the refusal that stopped them. */
  async read() {
    return readVoiceRecord(this.#settings, this.#accountId);
  }

  /**
   * Set one direction's engine, and leave the other alone.
   *
   * The direction and the engine are judged here rather than by a default: an
   * unset switch is local, but a switch *set* to something that is not one of
   * the two words is a caller bug, and writing the other direction's value
   * would be answering a question nobody asked.
   */
  async set(direction, engine) {
    if (!DIRECTIONS.includes(direction)) return refuse(REFUSAL.UNKNOWN_DIRECTION, 'direction');
    if (!ENGINES.includes(engine)) return refuse(REFUSAL.UNKNOWN_ENGINE, 'engine');
    return serialize(this.#settings, this.#accountId, async () => {
      const record = await this.read();
      if (!record.ok) return record;
      return writeVoiceRecord(this.#settings, this.#accountId, {
        settings: { ...record.settings, [direction]: engine },
        consent: record.consent,
      });
    });
  }

  /**
   * Record the user's agreement for one named provider, dated.
   *
   * The name and the date are judged by `consent.js` -- which is to say by
   * `shared/voice-switch`'s own rule -- before the record is written, and a
   * name the device has already agreed to keeps one entry with the new date.
   */
  async recordConsent(provider, at) {
    return serialize(this.#settings, this.#accountId, async () => {
      const record = await this.read();
      if (!record.ok) return record;
      const agreed = withConsent(record.consent, provider, at);
      if (!agreed.ok) return agreed;
      return writeVoiceRecord(this.#settings, this.#accountId, { settings: record.settings, consent: agreed.consent });
    });
  }

  /** Take one name back. A name the record does not hold is already revoked. */
  async revokeConsent(provider) {
    return serialize(this.#settings, this.#accountId, async () => {
      const record = await this.read();
      if (!record.ok) return record;
      const kept = withoutConsent(record.consent, provider);
      if (!kept.ok) return kept;
      return writeVoiceRecord(this.#settings, this.#accountId, { settings: record.settings, consent: kept.consent });
    });
  }

  /**
   * The badge for one direction, and the `send` field the egress path reads.
   *
   * The context is three fields and no more -- `direction`, `status` (the hop's
   * last project-status answer) and `lastCallInDirection` -- and the settings
   * and the consent are this module's own. A record that cannot be read is not
   * an exception and not a guess: the badge is the fail-closed one, with the
   * refusal in it, because the app still owes the owner a badge and may not owe
   * it silence.
   */
  async indicator(context) {
    const asked = ownedFields(context);
    const record = await this.read();
    if (!record.ok) return failClosedIndicator(asked, record);
    return renderIndicator({ ...asked, settings: record.settings, consent: record.consent });
  }
}

/**
 * Build one account's switch. `createVoiceSwitch({ settings, accountId })` --
 * the settings store the app already builds (`src/accounts/settings.js`) and
 * D114's account id.
 */
export function createVoiceSwitch({ settings, accountId }) {
  return new VoiceSwitch({ settings, accountId });
}
