// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The demo mode: the app, running on fixed sample data, for a reviewer who
// cannot pair anything (owner ruling of 2026-10-19).
//
// **What the owner ruled.** Google Play's reviewers cannot create an account
// and cannot pair a phone with an installation, so a build that only works
// after pairing is a build they cannot review. The alternative to handing out
// credentials is this: a built-in demo mode, entered from the first screen with
// no setup -- no account, no pairing, no network, no secret.
//
// **What it is made of, and what it therefore is not.** This module wires the
// app's REAL pieces to fixed sample data:
//
//   * the client is P3.6's `createMailboxClient`, the only place the app writes
//     to a database;
//   * the transport is P3.6's port double, with the consumer's side already
//     delivered into it (`transport.js`);
//   * the tags are made by the app's own MAC code, and checked by the app's own
//     verifier -- the client drops a message whose tag does not cover it;
//   * the question is P6.5's prompt queue, the read-back and the confirmation
//     are P6.2's state machine, and the presence rule is P1.4's, reached through
//     the client's own poll.
//
// So what a reviewer sees is the app's behaviour on sample data. The one thing
// it is not is a *second* implementation of any of it: there is no demo client,
// no demo verifier, no demo queue and no demo confirmation machine, and
// `index.test.js` is written to fail if the client stops being the client (it
// asserts that a reply the sample data tags wrongly is dropped as forged rather
// than spoken).
//
// **The three fences, and what each is for.** (1) No network: nothing under
// `src/demo/` imports or names a network or a database SDK, and the demo is
// entered in a test with `fetch` and `XMLHttpRequest` replaced by throwers.
// (2) No credential: no file here reaches platform secure storage, and entering
// the demo against the app's own `SecretStore` and `AccountStore` touches
// neither -- the Keychain port's methods see no call at all. (3) No secret:
// the demo's pairing key is a placeholder (`keys.js`), and the sample data is
// fictional in its names. All three are `safety.test.js`'s.
//
// **Where this joins the app.** The shell (`src/app/root.js`) renders the
// banner and the way out on every screen while `active()` is true, the first
// screen (`src/app/first-screen.js`) is where the demo is entered from, and
// `mobile/index.js` wires this controller to React. Later screens join by
// rendering inside the shell's body: they need to know nothing about the demo,
// because the shell is what draws the banner over them.

import { createMailboxClient, MemoryConnection } from '../mailbox/index.js';
import { initialState, step } from '../../../shared/confirm/index.js';
import { enqueue, initialQueue, pending as listPrompts } from '../../../shared/prompts/index.js';
import { demoKeySource } from './keys.js';
import {
  DEMO_ACCOUNT,
  DEMO_DESTRUCTIVE,
  DEMO_INSTALLATIONS,
  DEMO_INTERVAL_MS,
  DEMO_JOBS,
  DEMO_NOW,
  DEMO_PROMPT_SAFE_ANSWER,
  DEMO_UTTERANCES,
} from './sample.js';
import { createDemoTransport, demoIds } from './transport.js';

/** The sentence every screen carries while the demo is on (the owner's words). */
export const DEMO_BANNER = 'Demo mode: sample data';

/** The first screen's control: what a reviewer taps, with no setup at all. */
export const TRY_DEMO = 'Try the demo';

/** The way out, always visible: the banner carries it on every screen. */
export const LEAVE_DEMO = 'Leave demo mode';

/** What the state is before the demo has been entered, and after it is left. */
const INACTIVE = Object.freeze({
  active: false,
  account: null,
  installations: Object.freeze([]),
  jobs: Object.freeze([]),
  prompts: Object.freeze([]),
  messages: Object.freeze([]),
  sent: Object.freeze([]),
  spoken: Object.freeze([]),
  pending: Object.freeze([]),
  presence: null,
  destructive: null,
  problems: Object.freeze([]),
});

/**
 * Build the demo controller.
 *
 * It takes **no ports, no account store and no key source**: a demo that could
 * be handed a device's store would be a demo that could read a phone's
 * Keychain, which is the one thing this mode exists to make unnecessary. Every
 * argument it does take is a value a test may pin.
 *
 * @param {object} [options]
 * @param {number} [options.now] the demo's clock; `sample.js`'s fixed instant
 *   by default, so two runs of the demo are the same run
 * @returns {object} the frozen controller: `active()`, `state()`, `enter()`,
 *   `leave()`, `confirm()`, `subscribe(listener)`
 */
export function createDemoMode(options = {}) {
  if (options === null || typeof options !== 'object' || Array.isArray(options)) {
    throw new TypeError('createDemoMode: an options object is required');
  }
  const now = options.now === undefined ? DEMO_NOW : options.now;
  if (!Number.isSafeInteger(now) || now <= 0) {
    throw new TypeError('createDemoMode: the demo clock must be a positive integer');
  }

  /** Everything the demo owns while it is running; `null` when it is not. */
  let running = null;
  /**
   * The `enter()` still in flight, if any: `{ epoch, promise }`. `running` is
   * assigned only at the END of a run -- there are awaits in between -- so it
   * cannot be the guard against a double tap on its own: two taps in the same
   * tick would both find it null and both build a demo. This one is set
   * synchronously, before the first await.
   *
   * It carries the epoch the run started in, because a run the reviewer already
   * LEFT is not a run a later tap may join: joining it would answer that tap with
   * the inactive state and start nothing, which is a control that silently does
   * nothing. `leave` bumps the epoch, so the record goes stale by itself and a
   * new run may start while the abandoned one is still winding down.
   */
  let entering = null;
  /**
   * Bumped by `leave()`, so a run that is in flight across a leave is abandoned
   * rather than landing afterwards: a reviewer who leaves has asked for the app
   * without the demo, and a screen that put the banner back would be answering
   * the opposite.
   */
  let epoch = 0;
  /** The last snapshot handed out, so `state()` is a stable value. */
  let snapshot = INACTIVE;
  const listeners = new Set();

  const publish = (next) => {
    snapshot = Object.freeze(next);
    // Every listener is called, and a listener that throws does not stop the
    // others: this is a screen's re-render, and one broken screen must not take
    // the demo's state with it.
    for (const listener of [...listeners]) {
      try {
        listener();
      } catch {
        // deliberately swallowed: see above
      }
    }
  };

  /** The demo's screen state: everything it shows, and nothing it does not. */
  function buildState() {
    if (running === null) return INACTIVE;
    const { transport, client, poll, sent, problems, confirmed, refused, machine } = running;
    const dropped = new Map(poll.dropped.map((entry) => [entry.id, entry.code]));
    const spoken = new Set(poll.spoken);
    const acked = new Set(poll.acked);
    return {
      active: true,
      account: DEMO_ACCOUNT,
      installations: DEMO_INSTALLATIONS,
      jobs: DEMO_JOBS,
      prompts: Object.freeze(
        (listPrompts(running.queue, { now }).pending ?? []).map((entry) => Object.freeze({
          id: entry.prompt.id,
          text: entry.prompt.text,
          options: entry.prompt.options ?? null,
          at: entry.prompt.at,
          destructive: entry.destructive,
          safe: entry.safe,
          stage: entry.stage,
          due: entry.due,
        })),
      ),
      messages: Object.freeze(transport.messages.map((message) => Object.freeze({
        id: message.id,
        kind: message.kind ?? null,
        text: message.text,
        spoken: spoken.has(message.id),
        acked: acked.has(message.id),
        dropped: dropped.get(message.id) ?? null,
      }))),
      sent: Object.freeze(sent.map((entry) => Object.freeze({ ...entry }))),
      spoken: transport.speaker.spoken(),
      pending: client.pending(),
      presence: poll.presence,
      destructive: destructiveOf(machine, confirmed, refused),
      problems: Object.freeze([...problems]),
    };
  }

  /**
   * The destructive-command half of the screen: the command, its resolved
   * target, the machine's phase, **the read-back the machine built** (never a
   * string kept in the sample data), the record of the last confirmation that
   * authorised the action, and the last confirmation that was refused.
   *
   * `confirmed` and `refusal` are kept as two facts rather than one verdict,
   * because the machine's rules make them independent: a reviewer who confirms
   * the read-back and then taps Confirm again has authorised the action once and
   * been refused once, and a screen that showed only the last would be telling
   * them the action was never authorised.
   */
  function destructiveOf(machine, confirmed, refusal) {
    if (machine === null || !machine.ok) return null;
    return Object.freeze({
      command: DEMO_DESTRUCTIVE.command,
      verb: machine.verb,
      target: machine.target,
      phase: machine.phase,
      readBack: machine.readBack,
      confirmed,
      refusal,
    });
  }

  /** The demo's state, for a screen. Stable between changes (`subscribe`). */
  function state() {
    return snapshot;
  }

  /** Is the demo on? The one question the shell asks. */
  function active() {
    return running !== null;
  }

  /**
   * Enter the demo: build the app's real client over the demo's transport,
   * **poll first** (P6.10's gate needs an observation before the first send),
   * then send the sample utterances (real, signed writes), then verify, speak
   * and ack the consumer's messages that the look read, then put the
   * consumer's question in
   * P6.5's queue and read the destructive command back with P6.2's machine.
   *
   * Entering twice is the same demo, not a second one. There are two ways a
   * second call arrives and both are answered: once a run has finished, the
   * state it already has is returned; while a run is still going (a double tap,
   * which lands in the same tick -- `enter` awaits the client in between), the
   * second call JOINS the run in flight and resolves to the same state. Either
   * way the sample utterances are sent once.
   *
   * @returns {Promise<object>} the state, once everything above has run
   */
  async function enter() {
    if (running !== null) return snapshot;
    // Join the run in flight -- but only one started in the epoch we are in now.
    // A run the reviewer has left (see `leave`) is abandoned, and joining it
    // would answer this tap with the inactive state and start no demo.
    if (entering !== null && entering.epoch === epoch) return entering.promise;

    const mine = run(epoch);
    entering = Object.freeze({ epoch, promise: mine });
    try {
      return await mine;
    } finally {
      // Only this run's own record is cleared: a `leave()` and the run started
      // after it may be holding this slot by the time an abandoned run ends, and
      // clearing theirs would let the next tap start a third demo.
      if (entering !== null && entering.promise === mine) entering = null;
    }
  }

  /**
   * The body of one entry.
   *
   * @param {number} mine the epoch this run was started in; a `leave()` while it
   *   was in flight bumps the real one, and the run is then dropped whole rather
   *   than published
   * @returns {Promise<object>} the state, or the (inactive) state after a leave
   */
  async function run(mine) {
    const transport = createDemoTransport({ now });
    const client = createMailboxClient({
      account: DEMO_ACCOUNT,
      keys: demoKeySource(),
      store: transport.store,
      // P6.10's connection. Demo mode runs on the app's own fakes and never
      // touches a network (`./safety.test.js` holds that fence), so its phone is
      // connected by construction: the port is the one the suite uses, and the
      // gate is asked and answered rather than bypassed.
      connection: new MemoryConnection(),
      speaker: transport.speaker,
      now: () => now,
      newId: demoIds(),
      // P1.3's redaction list (D118). The demo holds no secret, so there is
      // nothing to redact -- and an empty list is the honest value rather than
      // a list borrowed from an installation nobody gave this mode.
      secrets: [],
      heartbeatIntervalMs: DEMO_INTERVAL_MS,
    });

    const problems = [];
    // **The pass starts with a poll, and P6.10 is why** (the offline gate's
    // second review, M2): the gate fails closed on a consumer the phone has not
    // observed, so the app's own first act is the look that says whether the
    // orchestrator is there at all -- and the demo does what the app does, in
    // the app's order. The transport has already delivered the sample heartbeat,
    // so this observes a live consumer, and the utterances below are sent by a
    // phone that has looked.
    //
    // **What moving the look to the front did change, said plainly.** It is one
    // state, and it is `pending()`. A verified reply is what retires a phone's
    // own utterance from its outbox, and the replies are read in THIS look --
    // before there is anything of the phone's for them to answer. So when the
    // pass ends, `pending()` holds the utterances this run has just written, all
    // three landed (`landed: true`: the store took them; the consumer has not
    // answered). With the look at the end it was empty, because the two answers
    // had already retired the two sentences they name. Both are real states of
    // the app -- a phone that sent and has not looked since, and a phone that
    // has -- and the demo runs one pass, so it shows the first. Nothing renders
    // `pending()` today; `index.test.js` asserts it rather than leaving it to be
    // discovered. Nothing else moved: sending speaks nothing, the id source
    // mints no id in a poll, and the words the reviewer hears are the same words
    // in the same order.
    const poll = await client.poll();
    if (!poll.ok) problems.push(Object.freeze({ at: 'poll', code: poll.code }));

    const sent = [];
    for (const utterance of [...DEMO_UTTERANCES, DEMO_DESTRUCTIVE.command]) {
      const verdict = await client.send(utterance);
      sent.push(Object.freeze({
        ok: verdict.ok,
        id: verdict.ok ? verdict.id : null,
        text: utterance,
        code: verdict.ok ? null : verdict.code,
      }));
      if (!verdict.ok) problems.push(Object.freeze({ at: 'send', code: verdict.code }));
    }

    // P6.5's queue, over the consumer's own message: the question sits there
    // until it is answered, and this is the app's queue rather than a list the
    // demo keeps.
    const opened = initialQueue({ now, account: DEMO_ACCOUNT.id });
    const queued = opened.ok
      ? enqueue(opened.queue, { now, prompt: transport.prompt, destructive: false, safe: DEMO_PROMPT_SAFE_ANSWER })
      : opened;
    if (!queued.ok) problems.push(Object.freeze({ at: 'queue', code: queued.code }));

    // P6.2's machine, over the utterance the client really sent: the read-back
    // it answers with is built from the resolved target, and the id it spends
    // is the id of the sentence that issued the command.
    const commandId = sent.find((entry) => entry.text === DEMO_DESTRUCTIVE.command)?.id ?? null;
    const machine = initialState({ now });
    let asked = null;
    if (machine.ok && commandId !== null) {
      asked = step(machine.state, {
        type: 'command',
        now,
        utteranceId: commandId,
        text: DEMO_DESTRUCTIVE.command,
        verb: DEMO_DESTRUCTIVE.verb,
        target: { ...DEMO_DESTRUCTIVE.target },
        actionId: DEMO_DESTRUCTIVE.actionId,
      });
      if (!asked.ok) problems.push(Object.freeze({ at: 'confirm-command', code: asked.code }));
    }

    // Left while this was in flight: the run is dropped whole rather than
    // published over a screen the reviewer has already dismissed.
    if (epoch !== mine) return snapshot;

    running = {
      transport,
      client,
      poll: poll.ok ? poll : { dropped: [], spoken: [], acked: [] },
      sent,
      problems,
      queue: queued.ok ? queued.queue : opened.queue,
      machine: asked,
      confirmed: null,
      refused: null,
    };
    publish(buildState());
    return snapshot;
  }

  /**
   * Confirm the destructive command the demo read back.
   *
   * The phrase is **sent like any other utterance** -- a real signed write by
   * the app's client -- and the id P6.2 spends is the id the client minted for
   * it, so the sentence that confirms is a different utterance from the
   * sentence that commanded, which is P6.2's rule and not a coincidence of two
   * fixed strings.
   *
   * The machine's state ADVANCES with every step, so a second tap is not a
   * second authorisation: the ledger holds the confirmation id the first tap
   * spent, the second step refuses, and the state shows the refusal *beside* the
   * record the first tap produced (`shared/confirm`'s rule; the demo shows it
   * rather than hiding it).
   *
   * @returns {Promise<object>} the state, with `destructive` carrying either
   *   the record or the refusal
   */
  async function confirm() {
    // The demo this confirmation belongs to, held for the whole call. The
    // module's `running` is NOT re-read after the await below: `leave()` sets it
    // to null while the phrase is in flight, and a screen that tapped Confirm and
    // then left would be answered with a TypeError instead of the inactive state.
    const run = running;
    if (run === null || run.machine === null || !run.machine.ok) {
      return snapshot;
    }
    const heard = await run.client.send(DEMO_DESTRUCTIVE.confirmation);
    // Left -- or left and entered again -- while the phrase was being sent: this
    // run's work is dropped whole rather than landing on a demo that is no longer
    // there (`running !== run` covers both, since a new run replaces it).
    if (running !== run) return snapshot;
    if (!heard.ok) {
      run.problems.push(Object.freeze({ at: 'confirm-send', code: heard.code }));
      publish(buildState());
      return snapshot;
    }
    const verdict = step(run.machine.state, {
      type: 'confirm',
      now,
      utteranceId: heard.id,
      text: DEMO_DESTRUCTIVE.confirmation,
      actionId: DEMO_DESTRUCTIVE.actionId,
      confirmationId: DEMO_DESTRUCTIVE.confirmationId,
      target: { ...DEMO_DESTRUCTIVE.target },
    });
    run.sent.push(Object.freeze({
      ok: true, id: heard.id, text: DEMO_DESTRUCTIVE.confirmation, code: null,
    }));
    if (verdict.ok) {
      // The state advances: the next step is judged against what this one spent.
      run.machine = verdict;
      run.confirmed = verdict.record;
      run.refused = null;
    } else {
      // A refusal carries no state (P6.2), so the machine keeps the one it had
      // -- which is the state that holds the spent confirmation id.
      run.refused = Object.freeze({ code: verdict.code, field: verdict.field });
    }
    publish(buildState());
    return snapshot;
  }

  /**
   * Leave the demo: everything it built is dropped -- the client, the
   * transport, the queue, the machine -- so the app is exactly as it was before
   * it was entered, and entering it again starts from the sample data rather
   * than from the last reviewer's session.
   *
   * Leaving also abandons an entry that is still in flight, and a confirmation
   * still being sent, so the way out is answered whatever the demo is doing --
   * and the next tap on "Try the demo" starts a whole new run rather than
   * joining the abandoned one.
   *
   * @returns {object} the inactive state
   */
  function leave() {
    epoch += 1;
    if (running === null) return snapshot;
    running = null;
    publish(INACTIVE);
    return snapshot;
  }

  /**
   * Watch the demo's state. A screen subscribes and re-renders; the returned
   * function unsubscribes (a React `useSyncExternalStore` subscription is
   * exactly this shape, and that is how `mobile/index.js` wires it).
   */
  function subscribe(listener) {
    if (typeof listener !== 'function') {
      throw new TypeError('subscribe: a listener function is required');
    }
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  return Object.freeze({
    ok: true,
    active,
    state,
    enter,
    leave,
    confirm,
    subscribe,
  });
}
