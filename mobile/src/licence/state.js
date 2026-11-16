// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's lapse state (subscriptions plan S6.3; D436, T12, D523; the design
// is docs/design/lapse-read-only.md, §1, §4 and §5).
//
// **The question this file answers.** `verify.js` names three states of a KEY
// -- `valid`, `grace`, `expired` -- and refuses everything else. What the APP
// is in is a different question, and the design's §1 gives it five answers:
// **no key** (the store is empty: everything works, with a banner, §5.3),
// **`valid`** and **`grace`** (everything works; the grace carries the renewal
// banner), **lapsed** (past the grace: reads, cancel and the kill switch are
// kept, and launch and configuration are refused), and **a key this build
// cannot verify** (an id the keyring dropped, or a rollback: the no-key state's
// behaviour, with its own banner, §5.4). The fifth is the reason this module
// exists rather than a `switch` on `active()`'s answer: `latest` SKIPS a key
// that does not verify, so a store holding one is `null` -- the same answer as
// an empty one -- and §5.4 reads the two differently while §5.3 and §5.4 refuse
// the same nothing.
//
// **What a lapse refuses, and what it keeps.** T12's kept set, as a closed and
// NAMED class (`LAPSE_KEPT_WRITES`): ordinary chatter, a cancel, and the kill
// switch. Everything else the app's one write path can be asked to make is
// refused once the key has lapsed -- today the launch (D162's pass-through,
// which travels as an ordinary message and is the reason the classification has
// to reach the write path at all) and a configuration change, and the
// repository write `merge`, which is §4.2's own call: it is named neither by
// T12's kept set nor by the plan's test, and it is refused because the kept set
// is exactly what T12 kept, and a repository write is none of it. The class is
// closed on purpose and in the refusing direction: a write this table does not
// name is refused after a lapse, so a later task that adds a write class cannot
// ship one a lapse silently allows. The alternative -- a table of refused names
// -- would allow every write nobody had thought about yet.
//
// **The kill switch is a row here and a read in the app.** §4.1: the app READS
// the kill state (`src/feed/`) and draws it as a job field; `kill` and
// `kill-switch` are refused verbs in `shared/confirm`, so there is no kill
// action this app can send and nothing for a lapse to take away. Its row is
// here because T12's kept set names it: a refusal set that did not would be a
// set this module had narrowed on its own.
//
// **Reads are never gated, and this module is not on a read path.** The
// console's and the app's surfaces stay readable at every state, the paid views
// included (the design's §2 and §3.2, and the owner's open call in §8 item 6).
// Nothing here is asked by a read: the mailbox client's `send` is the app's one
// write path (D113) and its only caller, which is why "every read stays" needs
// no code and no test here.
//
// **Where the decision is made, and where it is not.** The design's §4.3 puts
// the check in the mailbox client's `send`, beside P6.10's connection gate and
// before anything is minted, signed or written; that client is the caller of
// this file. Nothing else in the app gains a check: the CLI refuses nothing by
// licence state (T12 and D436 keep it launching and cancelling, and §9.2 of the
// Terms undertakes it), and the console's own lapse state is S6.2's.
//
// **This file imports nothing at all.** That is the app's suite's rule
// (`mobile/README.md`: a fresh clone has no `node_modules`, so every module a
// test loads must import nothing) and it is why the mailbox client can import
// this file directly instead of through the licence door: `index.js` names
// `@noble/curves`, and a `client.js` that reached the door would make the whole
// mailbox suite need an install. The door re-exports this surface for the
// screens, which is the direction the fence runs in either way.
//
// **The clock is an argument.** Every read takes the moment it is asked about,
// as a `Date`, exactly as `verify.js` takes it: a state is a fact about a key
// and a moment, so this module reads no clock of its own -- and, one level up,
// so that a landscape where the clock was set back is a question a test can ask
// rather than a bug nothing can hold.

/** The five states a product can be in (§1). The three names `verify.js` uses
 * -- `valid`, `grace`, `expired` -- are the KEY's; the product's lapse is
 * spelled `lapsed` here so that a state of the app is never mistaken for a
 * state of the key when both are in one sentence. */
export const LICENCE_STATES = Object.freeze({
  /** The store is empty: everything works, with a banner (§5.3, F43). */
  NO_KEY: 'no-key',
  /** An installed key with `now <= expires`: everything works. */
  VALID: 'valid',
  /** `expires < now <= expires + 30 days`: everything works, and the renewal
   * banner names the day the grace ends (§5.1). */
  GRACE: 'grace',
  /** Past the grace: reads, cancel and the kill switch; not launch, not
   * configuration (T12, §5.2). */
  LAPSED: 'lapsed',
  /** A key this build cannot verify: the no-key state's behaviour, with its own
   * banner (§5.4). */
  UNVERIFIED: 'unverified',
});

/** The state's own refusal code. It is this module's and not `verify.js`'s
 * `LICENCE_CODES`, which is what a refused KEY answers with: a lapse is not a
 * refusal of a key -- the key verified, and its clock ran out. */
export const LICENCE_STATE_CODES = Object.freeze({
  LAPSED: 'licence-lapsed',
});

/**
 * The writes the app's one write path can be asked to make, by class.
 *
 * A class is what the utterance will DO rather than how it is spelled: the
 * client reads one out of the record it is about to write (an action record's
 * verb and target) or out of the verb a message was classified as, and asks
 * this table whether the state refuses it. Naming the classes rather than the
 * commands is what keeps the refusal honest when a command lands: the console's
 * design says the same thing for its own table (§3.2: "the gate must classify
 * operations by class, not by a list of names kept beside the table"), and a
 * check that asked "is this a launch?" would refuse the cancel the day it is
 * added.
 */
export const WRITES = Object.freeze({
  /** An ordinary message: chatter, an answer, and every read the app speaks. */
  MESSAGE: 'message',
  /** A cancel, and the confirmation of one: an action record about a run. */
  CANCEL: 'cancel',
  /** The kill switch. The app reads its state and sends nothing for it (§4.1);
   * the row is T12's kept set and not a write this app makes. */
  KILL_SWITCH: 'kill-switch',
  /** A change to the installation's configuration. The app has no such action
   * today and the class is named so that the day one lands it is §4.2's row and
   * not an oversight. */
  CONFIG: 'config',
  /** D162's launch: recognised and passed through as an ordinary message, which
   * is why the classification has to reach the write path (§4.3). */
  LAUNCH: 'launch',
  /** A repository write: merging a pull request, or confirming one. */
  MERGE: 'merge',
});

/** What a lapse KEEPS, whole: T12's set, and the only writes this module ever
 * allows after the grace. Everything else is refused -- see the header. */
export const LAPSE_KEPT_WRITES = Object.freeze([WRITES.MESSAGE, WRITES.CANCEL, WRITES.KILL_SWITCH]);

/** The write each verb the app classifies an utterance as makes. The verbs are
 * `shared/confirm`'s closed set -- its three destructive ones, D162's launch,
 * and the ones it refuses outright -- plus this module's own name for a
 * configuration change, which no command has yet and which §4.2 names as a row
 * of the lapse's table rather than as a command. */
const WRITE_OF_VERB = Object.freeze({
  cancel: WRITES.CANCEL,
  confirm: WRITES.CANCEL,
  merge: WRITES.MERGE,
  launch: WRITES.LAUNCH,
  config: WRITES.CONFIG,
  kill: WRITES.KILL_SWITCH,
  'kill-switch': WRITES.KILL_SWITCH,
});

/**
 * The write one utterance makes, as the classes above name it.
 *
 * It is here rather than in the client because the classification is the
 * design's table (§4.2) and the table belongs in one place: the client hands in
 * what the caller handed IT -- a verb for a message, an action record for the
 * three verbs that travel as records -- and gets back the class the lapse is
 * about. Nothing here is trusted: the record's fields are read and never
 * written, and the schema refuses a malformed record later in the same call.
 *
 * **Everything it cannot name is `null`, which is not a class this table keeps**
 * -- so after a lapse an utterance this module cannot classify is refused
 * rather than let through. A verb nobody wrote, an action record that is not an
 * object, an action verb that is not one of P6.3's three: each is refused at a
 * lapse and untouched at every other state, where this module has no opinion.
 *
 * @param {object} [utterance]
 * @param {unknown} [utterance.verb] the verb a message was classified as
 * @param {unknown} [utterance.action] the action record the write carries
 * @returns {string|null} one of `WRITES`, or `null` for a write it cannot name
 */
export function writeOf({ verb, action } = {}) {
  if (action !== undefined) return actionWriteOf(action);
  // No classification at all is ordinary chatter -- and every read the app
  // speaks -- which is the write a lapse keeps.
  if (verb === undefined || verb === null) return WRITES.MESSAGE;
  return Object.hasOwn(WRITE_OF_VERB, verb) ? WRITE_OF_VERB[verb] : null;
}

/** actionWriteOf reads D169's action record: its verb, and -- for a `confirm`,
 * which may name either -- the target it names. A merge, and a confirmation of
 * one, is a repository write; a cancel, and a confirmation of one, is not. The
 * record's own shape is P6.3's and is validated by the schema before it is
 * written, so this reads two fields and never trusts them. */
function actionWriteOf(action) {
  if (action === null || typeof action !== 'object' || Array.isArray(action)) return null;
  if (action.verb === 'merge') return WRITES.MERGE;
  if (action.verb === 'cancel') return WRITES.CANCEL;
  if (action.verb !== 'confirm') return null;
  const target = action.target;
  const repository =
    target !== null && typeof target === 'object' && !Array.isArray(target) &&
    (target.repo !== undefined || target.pr !== undefined);
  return repository ? WRITES.MERGE : WRITES.CANCEL;
}

/**
 * What the owner is told when a write is refused, one sentence per code.
 *
 * It is a table rather than a branch inside the client for `gate.js`'s reason:
 * two screens render it (the voice screen and the text screen) and a sentence
 * written twice is a sentence that ends up disagreeing with itself. The
 * sentence says the three things §5.7 asks for: the state, that nothing was
 * sent, and the way out -- the key screen, which is never gated (§5.6). It
 * carries no figure (D430, T22; S0.1 pins it repository-wide): the copy names a
 * state and a route, never an amount.
 */
export const LICENCE_SPEECH = Object.freeze({
  [LICENCE_STATE_CODES.LAPSED]:
    'The Fugaro subscription has lapsed. I have not sent that. Add a current licence key to launch a job or change settings.',
});

/** The sentence for one refusal code, or `null` for a code this table does not
 * carry -- `null` rather than a default sentence, because a caller that shows a
 * generic sentence for a reason nobody wrote is a caller telling the owner
 * something nobody decided (the rule `gate.js`'s `offlineSpeech` states). */
export function licenceSpeech(code) {
  return Object.hasOwn(LICENCE_SPEECH, code) ? LICENCE_SPEECH[code] : null;
}

/** The product state each state of a VERIFIED key puts the app in. The three
 * are the whole of what `verify.js` names. */
const PRODUCT_OF_KEY_STATE = Object.freeze({
  valid: LICENCE_STATES.VALID,
  grace: LICENCE_STATES.GRACE,
  expired: LICENCE_STATES.LAPSED,
});

/** The reading a store that will not answer gets: see `createLicenceState`. */
const NO_KEY_READING = Object.freeze({ state: LICENCE_STATES.NO_KEY, claims: null });

/**
 * The product's state, from the keys the store holds and the moment it is read
 * at.
 *
 * The keys are the TEXTS the store keeps, unverified: the store answers what is
 * installed without verifying it (its `keys` does, and says why), so the
 * decision "which of them applies" is made here, once, by the app's verifier.
 *
 * @param {object} input
 * @param {unknown} input.keys the installed key texts
 * @param {object} input.verifier the app's verifier: `verify`/`latest`
 * @param {Date} input.now the moment to read them at
 * @returns {{ state: string, claims: object|null }} frozen; `claims` is the
 *   applying key's, and `null` in the two states that have no verified key
 * @throws {TypeError} for keys that are not a list or a verifier that cannot
 *   read one -- a wiring bug, not a state
 */
export function licenceState({ keys, verifier, now } = {}) {
  if (!Array.isArray(keys)) throw new TypeError('licenceState: the installed keys must be an array');
  if (verifier === null || typeof verifier !== 'object' || typeof verifier.latest !== 'function') {
    throw new TypeError("licenceState: the app's licence verifier is required");
  }

  const latest = verifier.latest(keys, now);
  if (latest === null) {
    // No key applies: either the store is empty (§5.3) or it holds keys and
    // none of them verifies under this build's ring (§5.4). Both keep working
    // and neither is a lapse -- the two banners differ, and that is all.
    return Object.freeze({
      state: keys.length === 0 ? LICENCE_STATES.NO_KEY : LICENCE_STATES.UNVERIFIED,
      claims: null,
    });
  }
  // A verified key is in one of the verifier's three states, and a fourth name
  // -- a state a later verifier adds and this table has not learned -- is read
  // as the lapse: the refusal is the safe direction, and a key whose state
  // nobody can name is not a licence the app may spend.
  return Object.freeze({
    state: PRODUCT_OF_KEY_STATE[latest.state] ?? LICENCE_STATES.LAPSED,
    claims: latest.claims,
  });
}

/**
 * The refusal one write gets at one product state, or `null` when the write is
 * allowed.
 *
 * It is a function of the state and the write and of nothing else: no clock, no
 * store, no network. The lapse is the only state that refuses anything (§5.3,
 * §5.4 and §5.1 all keep everything), and the refusal is a shape the client's
 * refusal path already speaks -- `{ ok, code, field }` -- so that a licence
 * refusal is rendered by the same code as the gate's and neither the voice
 * screen nor the text screen needs a licence concept of its own.
 *
 * @param {string} state one of `LICENCE_STATES`
 * @param {string} write one of `WRITES`, or a class this table does not know
 * @returns {{ ok: false, code: string, field: string } | null} frozen
 */
export function lapseRefusal(state, write) {
  if (state !== LICENCE_STATES.LAPSED) return null;
  // `includes` over the KEPT set, so a write class this table has never heard
  // of is refused rather than allowed: the set is closed, and it closes in the
  // direction that cannot spend a licence.
  if (LAPSE_KEPT_WRITES.includes(write)) return null;
  return Object.freeze({ ok: false, code: LICENCE_STATE_CODES.LAPSED, field: 'licence' });
}

/**
 * The refusal one write gets, read from the port at one moment.
 *
 * It is the composition the write path needs -- classify, read, judge -- and it
 * is here rather than in the client for one reason: **a kept write is never read
 * at all.** Nothing has to be known about the state to know that a cancel or a
 * message is kept (the kept set says so at every state), so the phone does not
 * open the licence store to send an ordinary message. Splitting the two halves
 * across the two files would put that rule -- "which writes need a reading" --
 * somewhere other than beside the set it is about.
 *
 * The port is asked at a moment the caller supplies, as `verify.js` is, and its
 * answer says `null` when there is no port worth asking (`UNENFORCED_LICENCE`):
 * no port, no reading, no refusal, and the write goes on as it did before this
 * module existed.
 *
 * @param {object} port a licence port: `at(now: Date)`
 * @param {object} [utterance]
 * @param {Date} [utterance.now] the moment to read the state at
 * @param {string|null} [utterance.write] the class `writeOf` answered
 * @returns {Promise<{ ok: false, code: string, field: string } | null>} frozen
 */
export async function lapseRefusalAt(port, { now, write } = {}) {
  if (LAPSE_KEPT_WRITES.includes(write)) return null;
  const reading = await port.at(now);
  if (reading === null || reading === undefined) return null;
  return lapseRefusal(reading.state, write);
}

/**
 * The app's licence state as the mailbox client's port: the store it reads the
 * keys from, and the verifier it reads them with.
 *
 * The client takes this as its `licence` option and asks it one question per
 * write -- `at(now)` -- because the state is a fact about a file and a moment
 * and the file can change under the phone (a key pasted on the key screen, a
 * clock crossing the grace) between one utterance and the next. Building it
 * here rather than in the wiring is deliberate: the wiring is the one file with
 * no test (`mobile/index.js` says so), and a port that bound the wrong verifier
 * would be a port no test could catch.
 *
 * **A store that will not answer reads as the no-key state.** The design has
 * five states and none of them is "the store did not answer"; failing closed
 * would refuse the launch to an installation whose storage hiccuped, which is a
 * check no ruling has in the window D523 rules has none, while the honest
 * reading is the one this port can make: nothing is installed as far as the app
 * can tell. Its price is the one §5.3 states rather than closes -- a lapsed
 * user who deletes their key is back in the same state.
 *
 * @param {object} options
 * @param {object} options.store the licence store (`store.js`'s `LicenceStore`,
 *   or anything with a `keys()` that answers `{ ok: true, keys }`)
 * @param {object} options.verifier the app's verifier: `verify`/`latest`
 * @returns {{ at: (now: Date) => Promise<{ state: string, claims: object|null }> }} frozen
 * @throws {TypeError} for a store or a verifier that cannot be used
 */
export function createLicenceState({ store, verifier } = {}) {
  if (store === null || typeof store !== 'object' || typeof store.keys !== 'function') {
    throw new TypeError('createLicenceState: a licence store with keys() is required');
  }
  if (verifier === null || typeof verifier !== 'object' || typeof verifier.latest !== 'function') {
    throw new TypeError("createLicenceState: the app's licence verifier is required");
  }

  return Object.freeze({
    /**
     * The state at one moment.
     *
     * @param {Date} now the moment the utterance is being made at
     * @returns {Promise<{ state: string, claims: object|null }>} frozen
     */
    async at(now) {
      const held = await store.keys();
      if (held === null || typeof held !== 'object' || held.ok !== true) return NO_KEY_READING;
      // A store that answers something other than a list of texts is a store
      // that answers nothing: read as the empty store rather than thrown, for
      // the reason the refusal above is read that way.
      return licenceState({ keys: Array.isArray(held.keys) ? held.keys : [], verifier, now });
    },
  });
}

/**
 * The port a mailbox client built without one holds: **it refuses nothing.**
 *
 * It answers `null`, which the client reads as "no reading": no state, no
 * decision, and the write goes on to the gate and the schema as it did before
 * this module existed. That is the honour-based product the tree ships today
 * and D523 defers enforcement from -- no verifier, no key service and no launch
 * is ready -- and it is the state the app's own entry is in: the wiring that
 * hands a real port in is the release that enforces, and a release that had not
 * built it yet must not refuse a launch on a state it never read.
 */
export const UNENFORCED_LICENCE = Object.freeze({
  async at() {
    return null;
  },
});
