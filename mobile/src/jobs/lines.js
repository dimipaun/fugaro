// The list as text: the one function that turns a list into what a screen
// shows (mobile plan, Task P3.3; resolved 2026-10-17).
//
// **Why this is a module and not a habit of the screen.** "A missing field is
// `unavailable` and never `0`" is a property of the *printing*, not of the
// number: `measured(0)` and `unavailable()` are two different figures, and the
// only way a screen can tell them apart is by asking the feed's `shown`. So
// every figure on this list is printed through `src/feed/index.js`'s `shown`,
// which is the function `availability.js` says exists for exactly this, and
// this file is the only place in the module that calls it. There is no
// `String(figure.value)`, no `?? 0`, and no template literal around a value:
// a figure is printed by the module that owns the five states.
//
// **The two things a broken series draws.** A `null` row -- a day break, a
// degraded feed, or a feed that has sent nothing -- prints the contract's
// **gap mark** (`GAP_MARK`, `—`) for every figure, and never `0`, `n/a` or a
// blank. `n/a` stays what it means: a field this document did not carry. The
// difference between the two is the whole of §10.7's "must not draw the zero",
// and it is visible on the screen rather than only in the state.
//
// **What is on a line, and what cannot be.** The labels come from
// `fields.js`'s table -- one line per field, in the table's order -- plus the
// project, the day, the series, the connection, the heartbeat and (only when
// the feed carries one) its static notice. A kill switch's `reason`, `by` and
// `at` are the kill node's own strings, cleaned by `document.js` the way
// `State.kill` cleans them, so a database-derived sentence is on this screen by
// the CLI's own rule and not by this module's. **No line can carry a run title
// or a repository**: there is no field for either, and `scope.test.js` refuses
// the tokens in this module's production files.
//
// **`lines` is the screen's projection, not the screen.** P3.4 and P3.5 build
// the audio fakes and the voice screen on P3.1's root; nothing in this task
// mounts a React tree, and the app's entry passes no React module here. What
// this function fixes is what the list *says*, so that "no run title is on
// screen" is a fact about a string list a test can read rather than a claim
// about a renderer nobody has run.

import { GAP_MARK, isEmpty, isFigure, shown } from '../feed/index.js';
import { HEARTBEAT_STATES, JOB_FIELDS, KILL_STATES, NOTHING } from './fields.js';

/** What the screen says about the series: §10.7's break, §10.8's degraded read,
 * or neither, in the list's own words. Not a figure and not an availability
 * state -- it is the series, not a number, that is broken or degraded.
 *
 * **Three words, and the third is why.** A degraded feed is not a broken
 * series: §10.8 is its own state, the feed's view reports `broken: false`
 * beside `degraded: true`, and a screen that drew "break" for it would tell the
 * reader the day's counter was missed when the truth is that part of the tree
 * could not be read. Both draw no row; only one is a break. */
export const SERIES = Object.freeze({ BROKEN: 'break', DEGRADED: 'degraded', OK: 'ok' });

/** What the screen says about a rate that cannot speak yet. The plan's own
 * phrase: "a recomputed burn rate ... is either correct against a fixture or
 * **not shown**". It is a sentence rather than a mark, because "the window has
 * not spanned the warmup" and "no counter has been read" are the same thing to
 * the reader and neither is an absence of a value it expected. */
export const NOT_SHOWN = 'not shown';

/** What a kill switch with no reason on it says. The switch's on-ness is the
 * load-bearing part (`nodes.js`), so a reason nobody wrote is not a blank. */
export const NO_REASON = 'no reason given';

/** Is this a list `jobList` built? A refusal is not one, and a caller that
 * hands one here has a bug rather than a list to draw. Private: the only caller
 * is `lines`, and a screen holding a refusal is a screen that did not read
 * `ok`. */
function isList(list) {
  return list !== null && typeof list === 'object' && list.ok === true && 'row' in list && 'heartbeat' in list;
}

/** A figure's text, or the gap mark when there is no figure to print. */
function figureText(figure) {
  if (!isFigure(figure)) return GAP_MARK;
  return shown(figure);
}

/** A field's text. `kill` and `burn` are the two that are not plain figures,
 * and each has its own sentence rather than a number. */
function fieldText(list, name) {
  if (list.row === null) return GAP_MARK;
  const value = list.row[name];
  if (name === 'kill') {
    // The two words come from the table's vocabulary rather than from two
    // literals here, so the screen's spelling of a state has one home.
    const [on, off] = KILL_STATES;
    if (value.on !== true) return off;
    return `${on} (${value.reason ?? NO_REASON})`;
  }
  if (name === 'burn') {
    if (value.shown !== true) return NOT_SHOWN;
    return value.fast === true ? `${figureText(value.usdPerMin)} (fast)` : figureText(value.usdPerMin);
  }
  // The cap fraction is a percentage: the mark is appended to a measurement
  // only, so `n/a` stays `n/a` and never becomes `n/a%`.
  if (name === 'percent' && isFigure(value) && !isEmpty(value)) return `${shown(value)}%`;
  return figureText(value);
}

/** The age, as the screen can state it -- or nothing.
 *
 * **A negative age is not a duration this phone can state.** P1.4 computes
 * `ageMs = now - at`, so a reading below zero is a heartbeat stamped *after* the
 * phone's own clock: `live` when the skew is inside its future tolerance
 * (P1.4's own test pins `ageMs = -60000`), and every `clock-differs` reading,
 * which is negative by construction -- that branch is `ageMs <
 * -futureToleranceMs` and nothing else, and it is the state D117 added for
 * exactly this skew. Printing the number with its sign -- `live (-60s ago)`,
 * `clock-differs (-600s ago)` -- would put a nonsense duration on the screen for
 * the one reading whose whole point is that the phone's clock is not the
 * authority. So the state is printed alone there: the state is the fact, and the
 * negative number is P1.4's arithmetic. `null` (nothing read yet) has no age
 * either. */
function ageText(ageMs) {
  if (ageMs === null || ageMs < 0) return '';
  return ` (${Math.round(ageMs / 1000)}s ago)`;
}

/** The heartbeat's text: its state, and how old the reading was when the
 * mailbox client read it. The age is P3.6's -- nothing here computes one -- and
 * `unknown` carries none, because nothing has been read. */
function heartbeatText(heartbeat) {
  if (!HEARTBEAT_STATES.includes(heartbeat.state)) return GAP_MARK;
  return `${heartbeat.state}${ageText(heartbeat.ageMs)}`;
}

/**
 * The screen's lines, in the order it draws them.
 *
 * Returns a frozen array of frozen `{ label, text }`. Throws on anything that
 * is not a list `jobList` built: this is the screen's edge, and a refusal
 * reaching it is a caller that did not read `ok`.
 */
export function lines(list) {
  if (!isList(list)) throw new TypeError('lines needs a list');
  const drawn = [
    { label: 'Project', text: list.project ?? NOTHING },
    { label: 'Day', text: list.day ?? NOTHING },
    // Degraded first: a degraded feed is not a break (§10.8), and the feed's
    // own view says so -- `broken: false` beside `degraded: true`.
    { label: 'Series', text: list.degraded ? SERIES.DEGRADED : list.broken ? SERIES.BROKEN : SERIES.OK },
    { label: 'Connection', text: list.connection },
  ];
  for (const field of JOB_FIELDS) {
    drawn.push({ label: field.label, text: fieldText(list, field.name) });
  }
  drawn.push({ label: 'Heartbeat', text: heartbeatText(list.heartbeat) });
  // The feed's notice is a static sentence (`view.js`'s `SKIP_NOTICE` /
  // `REFUSED_NOTICE`): a node's contents are not echoed back at the user, so
  // there is nothing here a database could have written.
  if (list.note !== null) drawn.push({ label: 'Note', text: list.note });
  return Object.freeze(drawn.map((line) => Object.freeze(line)));
}

/** The lines as one string, which is what a test that asks "is this on screen?"
 * reads. */
export function text(list) {
  return lines(list)
    .map((line) => `${line.label}: ${line.text}`)
    .join('\n');
}
