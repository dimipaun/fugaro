// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The compact button and the idle-only automatic compaction (mobile identity
// and pairing plan, task Z2; D2789).
//
// **What this module is.** The decision behind the two ways the context screen
// compacts: the button beside the gauge, which compacts when the person taps
// it, and the automatic compaction at the project's own threshold, which
// compacts a session nobody is waiting on. Both answer one thing -- the command
// to put in front of the agent -- and neither sends it: the app's one write
// path is the mailbox client (D113, `src/mailbox/write-path.test.js`), and this
// file imports nothing but the session port and the gauge beside it.
//
// **Idle only, and that is the whole of the red step.** Z0 (Check 732,
// `docs/mobile-identity-pairing/context-spike.md`) found a `/compact` injected
// while the session was idle honoured at once, and *another* slash command
// injected while idle queued until the next turn -- so a command sent while the
// agent is working lands in the turn that is running, which is the interruption
// the spec forbids ("sent only when the agent is idle so it never interrupts
// work"). Both triggers therefore refuse an agent that is not idle, with
// `agent-busy`, and the refusal is what a screen shows the person: a drop that
// says nothing would leave a tap looking as if it had compacted.
//
// **Why the agent's state is an argument.** Nothing the port carries says
// whether a turn is running: C2's session is the project, its endpoint, its
// role, its policy and its three functions, and the busy fact belongs to the
// side that talks to the agent (`src/voice-session`, `src/mailbox/client.js`).
// The caller hands it in, in the closed vocabulary below, so a caller with no
// reading of its own refuses `bad-agent` rather than sending -- absence is
// never `idle`, which is the one mistake that would interrupt work.
//
// **The threshold is the project's, and the button does not read it.** The
// automatic trigger compacts when the session's own policy says to
// (`session.policy.compaction_threshold`, the fraction of the window C2
// validates: `null` for a project that set none, otherwise above zero and below
// one), and a project that set none never compacts by itself at all. The button
// ignores both the threshold and the reading: a person who tapped it asked for
// a compaction now, and the only thing that can refuse them is an agent that is
// busy.
//
// **The comparison is made on the reading's own tokens.** A threshold of 40%
// against a gauge of `39.6%` must not fire, and `gauge.percent` is that 39.6
// rounded to `40` -- which is exactly why Z1 keeps `used` and `total` on the
// gauge beside it. So the rule is `used / total >= threshold`, which asks the
// question directly -- how much of the window is used, against the fraction the
// project wrote -- rather than multiplying the fraction out into tokens, where
// the rounding of `0.1 * 3` would put the boundary a fraction off.

import { assertSessionPort } from '../../../shared/mobile-session/port.js';
import { GAUGE_STATES, isGauge } from './gauge.js';

/**
 * The command that compacts the session's context, as Z0 read it: an injected
 * `/compact` is honoured at once while the session is idle, which is what makes
 * the one tap of the button a compaction and not a request for one.
 */
export const COMPACT_COMMAND = '/compact';

/**
 * What the agent on the other end is doing, as the caller's own source read it.
 * Two values and nothing else: a value outside them is refused, because the
 * state that must never be guessed is which one permits a send.
 */
export const AGENT_STATES = Object.freeze({
  /** No turn is running: a command sent now is honoured at once. */
  IDLE: 'idle',
  /** A turn is running: a command sent now is queued into it. */
  BUSY: 'busy',
});

/**
 * The two states a decision has. `hold` is the automatic trigger's answer when
 * nothing asks for a compaction; it is never the refusal of one, which is
 * `ok: false` -- a screen draws the two differently.
 */
export const COMPACTION_STATES = Object.freeze({
  /** Send the command: the agent is idle and this session should compact. */
  COMPACT: 'compact',
  /** Send nothing: nothing about this session asks for a compaction. */
  HOLD: 'hold',
});

/**
 * Why a decision holds. One value per distinct reason, and no value which says
 * "the agent is busy": that is a refusal and not a hold, because a caller that
 * treated it as one would swallow the one thing the person needs to be told.
 */
export const COMPACTION_REASONS = Object.freeze({
  /** The project sets no automatic compaction: nothing can fire by itself. */
  NO_THRESHOLD: 'no-threshold',
  /** No reading exists (Z1's `unavailable`), so nothing was compared. */
  NO_READING: 'no-reading',
  /** The reading is under the project's threshold. */
  BELOW_THRESHOLD: 'below-threshold',
});

/**
 * The refusals, one code per distinct way a decision cannot be made. `field`
 * names the part that failed (`agent`), and no code carries a value.
 */
export const COMPACTION_CODES = Object.freeze({
  /** The agent's state is not one of the two above, absent included. */
  BAD_AGENT: 'bad-agent',
  /** The agent is not idle, so no command may be sent to it. */
  AGENT_BUSY: 'agent-busy',
  /** A gauge was handed in that the gauge module did not build. */
  BAD_GAUGE: 'bad-gauge',
});

/** The fields a decision carries, and no others -- `isCompaction` checks exactly these. */
const COMPACTION_FIELDS = Object.freeze(['state', 'project_id', 'command', 'reason']);

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isNode(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/**
 * Judge the agent's state: a refusal for one that is busy, a refusal for one
 * this module cannot read, and `null` for the one that permits a send. The two
 * refusals are separate codes because they ask different things of the caller --
 * `agent-busy` is "later", `bad-agent` is "you never read it".
 */
function judgeAgent(agent) {
  if (agent === AGENT_STATES.BUSY) return refuse(COMPACTION_CODES.AGENT_BUSY, 'agent');
  if (agent !== AGENT_STATES.IDLE) return refuse(COMPACTION_CODES.BAD_AGENT, 'agent');
  return null;
}

/** A decision, frozen, in the one shape this module answers with. */
function decide(state, project, reason) {
  return Object.freeze({
    ok: true,
    compaction: Object.freeze({
      state,
      project_id: project,
      command: state === COMPACTION_STATES.COMPACT ? COMPACT_COMMAND : null,
      reason,
    }),
  });
}

/**
 * The compact button's decision: the command to send for an agent that is idle,
 * and the refusal of one that is busy. The person asked, so neither the reading
 * nor the project's threshold is consulted -- a session at 5% compacts when
 * they tap, which is their call and not this module's.
 *
 * The session is asserted first and its refusal is the port's own
 * (`SessionPortError`), as Z1's gauge does: a caller that mounted this screen
 * with something that is not a session built the screen wrong, and this module
 * does not re-label another module's refusal.
 */
export function compactButton({ session, agent } = {}) {
  const project = assertSessionPort(session).project_id;
  const refused = judgeAgent(agent);
  if (refused !== null) return refused;
  return decide(COMPACTION_STATES.COMPACT, project, null);
}

/**
 * The automatic decision for one reading: compact when the session's own policy
 * says so and the agent is idle, hold when the project sets no threshold or the
 * reading is under it, and refuse while the agent is busy.
 *
 * A missing `gauge` is the `no-reading` hold -- Z1's own rule, that a caller
 * with no reading hands none and never a zero -- while a gauge that is present
 * and unusable is refused `bad-gauge`, because a gauge this module did not
 * build is a defect in the wiring and not a session nobody measured. The
 * threshold is asked before the reading is: a project that compacts nothing
 * automatically has nothing to say about how full a session is.
 */
export function autoCompaction({ session, gauge = null, agent } = {}) {
  const port = assertSessionPort(session);
  const refused = judgeAgent(agent);
  if (refused !== null) return refused;
  const reading = gauge === null || gauge === undefined ? null : gauge;
  if (reading !== null && !isGauge(reading)) return refuse(COMPACTION_CODES.BAD_GAUGE, 'gauge');
  const threshold = port.policy.compaction_threshold;
  if (threshold === null) return decide(COMPACTION_STATES.HOLD, port.project_id, COMPACTION_REASONS.NO_THRESHOLD);
  if (reading === null || reading.state === GAUGE_STATES.UNAVAILABLE) {
    return decide(COMPACTION_STATES.HOLD, port.project_id, COMPACTION_REASONS.NO_READING);
  }
  if (reading.used / reading.total < threshold) {
    return decide(COMPACTION_STATES.HOLD, port.project_id, COMPACTION_REASONS.BELOW_THRESHOLD);
  }
  return decide(COMPACTION_STATES.COMPACT, port.project_id, null);
}

/**
 * Judge a value somebody else built as a decision: the fields above, each of
 * the kind above, and no others. `compactionCommand` refuses what this refuses,
 * so the one function a screen sends with cannot be handed a value it would
 * send as something this module never answered.
 */
export function isCompaction(value) {
  if (!isNode(value)) return false;
  // A symbol-keyed member has no name to check and would travel with the value:
  // the port refuses one for the same reason.
  if (Object.getOwnPropertySymbols(value).length > 0) return false;
  if (Object.getOwnPropertyNames(value).length !== COMPACTION_FIELDS.length) return false;
  if (!COMPACTION_FIELDS.every((field) => Object.hasOwn(value, field))) return false;
  if (typeof value.project_id !== 'string' || value.project_id.length === 0) return false;
  if (value.state === COMPACTION_STATES.COMPACT) {
    return value.command === COMPACT_COMMAND && value.reason === null;
  }
  if (value.state !== COMPACTION_STATES.HOLD) return false;
  return value.command === null && Object.values(COMPACTION_REASONS).includes(value.reason);
}

/**
 * The command to send for a decision, or `null` when it holds. One function, so
 * that a screen never reads `command` off a value this module did not build --
 * and so the two states are never sent as each other.
 */
export function compactionCommand(compaction) {
  if (!isCompaction(compaction)) throw new TypeError('not a compaction decision');
  return compaction.command;
}
