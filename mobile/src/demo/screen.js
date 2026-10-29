// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The demo's screen: what a reviewer reads while demo mode is on (owner ruling
// of 2026-10-19).
//
// **What this screen shows, and why each part is a fact rather than a mock.**
// It draws the demo's state and nothing else -- `createDemoMode().state()` --
// and every line of it is something the app's own code produced a moment
// earlier:
//
//   * the installations and jobs are the sample data, named as what they are;
//   * **Presence** is P1.4's reading of the orchestrator's signed heartbeat,
//     reached through the client's poll;
//   * **Waiting question** is P6.5's queue entry, with the answers the consumer
//     offered (and which answer is the safe one, which D127/D168 keep
//     phone-local);
//   * **Destructive command** is P6.2's read-back -- the machine's own string,
//     built from the resolved target -- its phase, and the record a
//     confirmation produced or the refusal it got;
//   * **Replies** are the messages the client verified, spoke and acked, with
//     those three facts shown separately, because "spoken but not acked" is
//     exactly the state a reviewer should be able to tell apart from "done".
//
// **Where the demo's own words are.** The banner and the way out are the
// SHELL's (`src/app/root.js`), not this screen's: the shell draws them over
// every screen, so a screen added later cannot forget them.
//
// **Why this is a function of React and react-native rather than JSX.** The
// same reason `src/app/root.js` is: the Expo toolchain is not installed here
// and no test may need a device (Global Constraint 10), so the primitives are
// arguments and `screen.test.js` asserts the tree against a fake React.

/** The element types this screen uses, in one place so a test can name them. */
export const DEMO_SCREEN = Object.freeze({
  ROOT: 'fugaro.demo',
  PRESENCE: 'fugaro.demo.presence',
  INSTALLATIONS: 'fugaro.demo.installations',
  JOB_LIST: 'fugaro.demo.jobs',
  QUESTION: 'fugaro.demo.question',
  DESTRUCTIVE: 'fugaro.demo.destructive',
  READ_BACK: 'fugaro.demo.read-back',
  CONFIRM: 'fugaro.demo.confirm-control',
  CONFIRMED: 'fugaro.demo.confirmed',
  REFUSAL: 'fugaro.demo.refusal',
  REPLIES: 'fugaro.demo.replies',
  SENT: 'fugaro.demo.sent',
  NOTE: 'fugaro.demo.note',
});

/** What the screen says about a section with nothing in it. */
export const NOTHING_YET = 'Nothing yet.';

/** The label on the control that confirms the read-back. */
export const CONFIRM_DESTRUCTIVE = 'Confirm the destructive command';

/** What a reply's three facts look like, each one its own word. */
function replyLine(message) {
  const states = [];
  if (message.dropped !== null) states.push(`dropped (${message.dropped})`);
  if (message.spoken) states.push('spoken');
  if (message.acked) states.push('acked');
  return `${message.text} [${states.length === 0 ? 'not yet spoken' : states.join(', ')}]`;
}

/**
 * Build the demo screen.
 *
 * @param {object} options
 * @param {object} options.React React, with `createElement`
 * @param {Function} options.View react-native's View
 * @param {Function} options.Text react-native's Text
 * @param {Function} options.Pressable react-native's Pressable
 * @param {Function} options.onConfirm called when the reviewer confirms the
 *   destructive command; the controller's `confirm`
 * @returns {(state: object) => object} the screen, as a function of the demo's
 *   state (the shell re-renders it whenever the state changes)
 */
export function createDemoScreen({ React, View, Text, Pressable, onConfirm }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createDemoScreen needs React (with createElement)');
  }
  for (const [name, part] of [['View', View], ['Text', Text], ['Pressable', Pressable]]) {
    if (typeof part !== 'function' && typeof part !== 'object') {
      throw new TypeError(`createDemoScreen needs react-native's ${name}`);
    }
  }
  if (typeof onConfirm !== 'function') {
    throw new TypeError('createDemoScreen needs what confirming does');
  }

  const text = (type, children, key) => React.createElement(Text, { type, key }, children);
  const view = (type, children, key) => React.createElement(View, { type, key }, ...children);

  return function DemoScreen(state) {
    const rows = [];

    // Presence: P1.4's reading of the heartbeat the orchestrator signed. The
    // agent names come from the heartbeat's own list, which is why the tag
    // covers `agents` (an unsigned heartbeat must not name who is listening).
    const agents = state.presence?.agents ?? null;
    rows.push(text(
      DEMO_SCREEN.PRESENCE,
      `Presence: ${state.presence?.state ?? 'unknown'}${agents === null ? '' : ` (${agents.map((agent) => agent.name).join(', ')})`}`,
    ));

    rows.push(view(
      DEMO_SCREEN.INSTALLATIONS,
      state.installations.map((entry, i) => text(
        DEMO_SCREEN.INSTALLATIONS,
        `Installation: ${entry.label} (${entry.account.id})`,
        `installation-${i}`,
      )),
    ));

    rows.push(view(
      DEMO_SCREEN.JOB_LIST,
      state.jobs.length === 0
        ? [text(DEMO_SCREEN.NOTE, NOTHING_YET)]
        : state.jobs.map((job, i) => text(
          DEMO_SCREEN.JOB_LIST,
          `Job: ${job.runId} on ${job.repo} -- ${job.title} -- ${job.state}`,
          `job-${i}`,
        )),
    ));

    // The question the consumer is holding: P6.5's queue, with the answers it
    // offered and the safe one named (D168/D173: the destructiveness of a
    // prompt is phone-local and never part of the message).
    rows.push(view(
      DEMO_SCREEN.QUESTION,
      state.prompts.length === 0
        ? [text(DEMO_SCREEN.NOTE, NOTHING_YET)]
        : state.prompts.map((prompt, i) => text(
          DEMO_SCREEN.QUESTION,
          `Waiting question: ${prompt.text} [answers: ${(prompt.options ?? []).join(', ')}; safe: ${prompt.safe}]`,
          `prompt-${i}`,
        )),
    ));

    // The destructive path: the command, the read-back the machine built, the
    // phase, and the control that confirms it. The control is NOT disabled
    // after a confirmation -- a second attempt is refused by the machine, and
    // the refusal is shown below, because that rule is part of what the app
    // does and the reviewer is here to see it.
    const destructive = state.destructive;
    if (destructive === null) {
      rows.push(view(DEMO_SCREEN.DESTRUCTIVE, [text(DEMO_SCREEN.NOTE, NOTHING_YET)]));
    } else {
      const parts = [
        text(DEMO_SCREEN.DESTRUCTIVE, `Destructive command: ${destructive.command}`),
        text(DEMO_SCREEN.READ_BACK, `Read back: ${destructive.readBack}`),
        text(DEMO_SCREEN.DESTRUCTIVE, `Phase: ${destructive.phase}`),
        React.createElement(
          Pressable,
          { type: DEMO_SCREEN.CONFIRM, key: 'confirm', onPress: onConfirm },
          CONFIRM_DESTRUCTIVE,
        ),
      ];
      if (destructive.confirmed !== null) {
        const record = destructive.confirmed;
        parts.push(text(
          DEMO_SCREEN.CONFIRMED,
          `Confirmed: ${record.verb} ${record.target.run ?? record.target.pr} (${record.confirmation.id})`,
        ));
      }
      if (destructive.refusal !== null) {
        parts.push(text(
          DEMO_SCREEN.REFUSAL,
          `Refused: ${destructive.refusal.code} (${destructive.refusal.field})`,
        ));
      }
      rows.push(view(DEMO_SCREEN.DESTRUCTIVE, parts));
    }

    rows.push(view(
      DEMO_SCREEN.REPLIES,
      state.messages.length === 0
        ? [text(DEMO_SCREEN.NOTE, NOTHING_YET)]
        : state.messages.map((message, i) => text(
          DEMO_SCREEN.REPLIES,
          `Reply: ${replyLine(message)}`,
          `reply-${i}`,
        )),
    ));

    rows.push(view(
      DEMO_SCREEN.SENT,
      state.sent.map((entry, i) => text(
        DEMO_SCREEN.SENT,
        `Sent: ${entry.text} [${entry.ok ? entry.id : entry.code}]`,
        `sent-${i}`,
      )),
    ));

    return view(DEMO_SCREEN.ROOT, rows);
  };
}
