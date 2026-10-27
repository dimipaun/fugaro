// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The five availability states of a feed number (mobile plan, Task P3.2;
// console/docs/json-contract.md §1, §10.13 rule 5).
//
// §1's first rule is the design's first UI rule: **every number carries an
// availability state, and a missing value is never an implicit zero.** The
// contract lists the states and says which wire shape needs which one:
//
//   * **measured** -- a counter the CLI (here: the reader) read.
//   * **notional** -- a subscription's list price: never billed, never capped,
//     never summed into a real total (`JSONTotal.NotionalUSD`'s own comment).
//   * **estimated** -- compute, an estimate by design (`JSONLine.ComputeEstimated`).
//   * **unavailable** -- the document says the value is not known: a nil
//     pointer, an absent key, `[]`.
//   * **gap** -- the stream stopped; never interpolated across (§10.7).
//
// **Why this is a module and not a convention.** The bug the contract keeps
// naming -- `0` where a value is unknown -- is not a rendering bug, it is a
// missing state, and no arithmetic can tell the two apart: `measured(0)` and
// `unavailable()` add to the same thing. So the state is carried in a value
// that cannot hold both at once (an `unavailable` or `gap` figure refuses to
// carry a number at all), and a screen has exactly one function -- `shown` --
// that turns a figure into something printable, with `n/a` for the missing one
// and a bare number only for a measurement.
//
// **What this module does not do.** It does not read the wire, decide which
// field is which state, or compute anything: `document.js` reads the wire and
// `reader.js`'s `#assemble` builds the document `document.js` then judges. This
// file knows only the five states and the two ways a figure can be wrong (a
// number of the wrong shape, and a value on a state that has none).
//
// The `estimated` state is part of the contract's §1 and has no producer in
// this task: the contract's only estimated field is `report`'s
// `compute_estimated`, and the feed document the phone reads has none. It is
// here because the state set is §1's, and `document.test.js` asserts the feed
// never marks a figure estimated -- a state this app could not produce would
// otherwise be a state nobody ever checked.
//
// **`gap` has no producer either, and it is kept for the same reason.**
// `document.test.js` asserts the feed never marks a figure `gap`; the day break
// `gap` names (§10.7) is drawn as `drawable: null` by `view.js` rather than as
// a figure, because there is no number to carry and a figure is a number's
// shape. `STATE.GAP`, `GAP_MARK`, `shown` and `isEmpty` are load-bearing -- the
// mark and the two functions are how a gap would be printed if one ever
// existed -- while the `gap()` constructor has no caller in P3.2. It is here so
// the state set stays §1's five, and so a later task does not have to add a
// constructor to a frozen vocabulary in order to use it.

/**
 * The five states, spelled as the contract spells them. Nothing else is a
 * state: a caller that renders an unlisted string is rendering something this
 * module never returns.
 */
export const STATE = Object.freeze({
  MEASURED: 'measured',
  NOTIONAL: 'notional',
  ESTIMATED: 'estimated',
  UNAVAILABLE: 'unavailable',
  GAP: 'gap',
});

/** The states that carry a number. The other two carry nothing. */
const NUMERIC = Object.freeze([STATE.MEASURED, STATE.NOTIONAL, STATE.ESTIMATED]);

/** The states that carry no number at all. */
const EMPTY = Object.freeze([STATE.UNAVAILABLE, STATE.GAP]);

/**
 * What a broken series renders as. The contract draws a gap as "a broken line
 * or hollow bar"; the phone's feed is text, and this is the mark that keeps a
 * gap distinguishable from `n/a` (a value that was never there) and from `0`.
 */
export const GAP_MARK = '—';

/** The refusal vocabulary of `check`: one code per distinct way a value can
 * fail to be a figure. A caller that inspects a value it did not build gets
 * one of these rather than a `false`, because "this is not a figure" and "this
 * figure carries a value it may not carry" are different facts. */
export const AVAILABILITY_CODES = Object.freeze({
  NOT_AN_OBJECT: 'not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  UNKNOWN_STATE: 'unknown-state',
  NOT_A_NUMBER: 'not-a-number',
  NEGATIVE: 'negative',
  BAD_VALUE: 'bad-value',
});

/**
 * A figure: a frozen `{ state, value }`.
 *
 * Throws rather than repairing, for the reason `account.js` refuses rather
 * than trimming: a figure invented by defaulting is a number on a screen that
 * nothing measured. The three numeric states need a finite, non-negative
 * number; `unavailable` and `gap` need `null`, because a value on either of
 * them is precisely the lie this module exists to stop.
 */
export function figure(value, state) {
  if (!Object.values(STATE).includes(state)) throw new TypeError(`not an availability state: ${String(state)}`);
  if (NUMERIC.includes(state)) {
    if (typeof value !== 'number' || !Number.isFinite(value)) throw new TypeError('a numeric figure needs a finite number');
    if (value < 0) throw new TypeError('a spend is never negative');
  } else if (value !== null) {
    throw new TypeError(`${state} carries no value`);
  }
  return Object.freeze({ state, value });
}

/** A counter this reader read -- a real `0` included: that is a measurement. */
export function measured(value) {
  return figure(value, STATE.MEASURED);
}

/** A subscription's list price: never billed, never capped. */
export function notional(value) {
  return figure(value, STATE.NOTIONAL);
}

/** Compute, an estimate by design. */
export function estimated(value) {
  return figure(value, STATE.ESTIMATED);
}

/** Not known: an absent key, a nil pointer, a field the document omits. */
export function unavailable() {
  return figure(null, STATE.UNAVAILABLE);
}

/** The series broke here: a changed `day`, a document that is not a measurement. */
export function gap() {
  return figure(null, STATE.GAP);
}

/**
 * Judge a value somebody else built. Returns `{ ok: true, figure }` or
 * `{ ok: false, code }` from `AVAILABILITY_CODES`, and `isFigure` is this
 * function's `ok`. Every field is judged -- a figure with an extra key is a
 * figure somebody else made, and `state` is checked against the five before
 * `value` is trusted -- and **the state is returned as it was given**: a
 * `notional` figure comes back `notional`, never re-labelled `measured`.
 */
export function check(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return Object.freeze({ ok: false, code: AVAILABILITY_CODES.NOT_AN_OBJECT });
  }
  const keys = Object.keys(value);
  if (keys.length !== 2 || !keys.includes('state') || !keys.includes('value')) {
    return Object.freeze({ ok: false, code: AVAILABILITY_CODES.UNKNOWN_FIELD });
  }
  if (!Object.values(STATE).includes(value.state)) {
    return Object.freeze({ ok: false, code: AVAILABILITY_CODES.UNKNOWN_STATE });
  }
  if (NUMERIC.includes(value.state)) {
    if (typeof value.value !== 'number' || !Number.isFinite(value.value)) {
      return Object.freeze({ ok: false, code: AVAILABILITY_CODES.NOT_A_NUMBER });
    }
    if (value.value < 0) return Object.freeze({ ok: false, code: AVAILABILITY_CODES.NEGATIVE });
    // The state comes back as it went in: `measured` is not a default, it is a
    // claim about who read the number, and a notional or estimated figure
    // returned as `measured` would be this module silently promoting a list
    // price to a bill for every caller that reads `.figure`.
    return Object.freeze({ ok: true, figure: figure(value.value, value.state) });
  }
  if (value.value !== null) return Object.freeze({ ok: false, code: AVAILABILITY_CODES.BAD_VALUE });
  return Object.freeze({ ok: true, figure: figure(null, value.state) });
}

/** Is this a figure, and one this module could have built? */
export function isFigure(value) {
  return check(value).ok;
}

/**
 * How a figure is printed. The one function a screen uses, so that "never an
 * implicit zero" is a property of this function rather than a habit of every
 * caller:
 *
 *   measured   -> `0`, `12.5`      the number, and only a measurement gets one
 *   notional   -> `NOTIONAL~12.5`  a subscription's list price, marked
 *   estimated  -> `~12.5`          the number, marked
 *   unavailable-> `n/a`
 *   gap        -> `—`
 */
export function shown(fig) {
  if (!isFigure(fig)) throw new TypeError('not a figure');
  switch (fig.state) {
    case STATE.MEASURED:
      return String(fig.value);
    case STATE.NOTIONAL:
      return `NOTIONAL~${fig.value}`;
    case STATE.ESTIMATED:
      return `~${fig.value}`;
    case STATE.UNAVAILABLE:
      return 'n/a';
    default:
      return GAP_MARK;
  }
}

/** The states that carry no number, for a caller that must ask. */
export function isEmpty(fig) {
  if (!isFigure(fig)) throw new TypeError('not a figure');
  return EMPTY.includes(fig.state);
}
