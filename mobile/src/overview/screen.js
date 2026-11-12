// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview screen (mobile plan, Task P6.8; D170, D114).
//
// **One section per installation, and nothing above or below them.** D170's
// negative half is the reason this screen is shaped the way it is: there is no
// header, no footer and no summary row, so there is nowhere a summed total
// could be added without a new element being built for it. The sections are
// built in the order `overviewTiles` returned -- the store's own order -- so
// the rows do not move between refreshes.
//
// **What a section says.** The installation's label (the owner's, D114), the
// Fugaro project it is in, how alive its feed is and how old the reading is,
// the day those figures belong to, and the four figures themselves -- printed
// by `feed/availability.js`'s `shown()`, which is the one function in this app
// that turns a figure into text, so a value nothing measured is `n/a` and this
// screen cannot print the zero §10.7 forbids.
//
// **And the day break is a mark, not a number.** `feed/view.js` makes
// `drawable` `null` on a day break and on a degraded document -- "rather than
// as a figure, because there is no number to carry and a figure is a number's
// shape" -- so there is no figure for this screen to print, and the tile's
// figures are `unavailable()` for exactly that reason. What the screen prints
// there is `GAP_MARK`, the contract's broken-series mark, chosen from the
// tile's own `broken`/`degraded` flags. It is the screen's call rather than the
// tile's because the feed's door offers the mark and deliberately not the
// `gap()` figure: one producer of a gap, and it is the view's `drawable: null`.
// A feed that has read nothing yet is *not* a gap -- there is no series to
// break -- so `no-document` prints `n/a`, which is what the tile says.
//
// **Why the label and not the project name is the heading.** Two installations
// may be in the same Fugaro project (a second laptop, a phone paired again),
// so the project name is not an identity. The label the owner typed is, and it
// is also what they say to switch to it (`switcher.js`), so the row and the
// utterance name the same thing.
//
// **The switch is a press, not a voice command, and it goes through the same
// door.** `onSwitch(id)` is the screen's only outward call; the caller routes
// it to `switcher.switchTo`, so a press and a spoken "switch to phone" tear
// down the previous account the same way, in the same place. The screen holds
// no account state: which row is current is `current`, passed in.
//
// React and react-native are not installed in this environment, so the four
// primitives are injected and the tests walk the tree this repository builds.

import { GAP_MARK, shown } from '../feed/index.js';

/** The named elements of this screen's tree. A test looks elements up by these
 * names rather than by position, so a row can move without rewriting it. */
export const SCREEN = Object.freeze({
  TITLE: 'overview-title',
  TILE: 'overview-tile',
  NAME: 'overview-name',
  PROJECT: 'overview-project',
  FRESHNESS: 'overview-freshness',
  DAY: 'overview-day',
  FIGURE: 'overview-figure',
  NOTE: 'overview-note',
  SWITCH: 'overview-switch',
  SWITCH_LABEL: 'overview-switch-label',
  EMPTY: 'overview-empty',
  REFUSAL: 'overview-refusal',
});

/** The one heading. It says what the screen is, not how many rows it has: a
 * count in the title would be the first step towards a total. */
export const OVERVIEW_TITLE = 'Your installations';

/** What an owner with no installation reads: where the list comes from, which
 * is the pairing screen (`mobile/src/pairing/`). */
export const EMPTY_SENTENCE = 'No installation yet. Pair one to see it here.';

/** What a list that could not be read says. **One** sentence for all three
 * codes, deliberately: the person holding the phone cannot act on the
 * difference between a list that was not a list and a snapshot that was not a
 * snapshot, and a refusal reaching this screen is a bug rather than their
 * mistake. The code is still carried on the element, for a test and a
 * developer. */
export const REFUSAL_SENTENCE = 'The installation list could not be read.';

/** The word on the switch control. The label of the installation is what it
 * switches to, so the control says what the press does rather than repeating
 * the row's heading. */
export const SWITCH_WORD = 'Switch to this';

/** What a tile with no project name says. The tile's `project` is `null` only
 * for an account whose tuple did not carry one, which is a store this build
 * does not write. */
export const UNKNOWN_PROJECT = 'project not known';

/** What a tile with no day yet says: the feed has read nothing for a day. */
export const NO_DAY = 'no day yet';

/** How the figures are labelled, and the suffix a percentage carries. The
 * order is the order they are drawn in, and it is one list so the screen and
 * its test cannot disagree about what is shown. */
export const FIGURES = Object.freeze([
  Object.freeze({ field: 'spend', label: 'spend', suffix: '' }),
  Object.freeze({ field: 'dailyCap', label: 'daily cap', suffix: '' }),
  Object.freeze({ field: 'percent', label: 'of the cap', suffix: '%' }),
  Object.freeze({ field: 'runs', label: 'runs today', suffix: '' }),
]);

/** What each liveness state says, in the app's own words. Every state
 * `feed/view.js` can answer is here: a state with no sentence would be a row
 * that says nothing about itself, and the test walks `view.js`'s own `LIVENESS`
 * list to prove none is missing. */
const LIVENESS_SENTENCES = Object.freeze({
  'no-document': 'nothing read yet',
  live: 'live',
  stale: 'quiet',
  offline: 'not reachable',
  refused: 'this token may not read it',
  polling: 'connecting',
  degraded: 'a degraded document',
});

/** What a state this build does not know says. It is not an empty string: a
 * row with no words looks like a row with no problem. */
export const UNKNOWN_LIVENESS_SENTENCE = 'state not known';

/**
 * How long ago, as a phone screen says it -- or nothing.
 *
 * The age is `feed/view.js`'s (`ageMs`, from the phone's own clock) and is
 * printed here, never computed: a second computation would be a second answer
 * to "how long has this been quiet". `null` means nothing has been read yet,
 * which has no age; and a negative or non-finite one is not a duration this
 * phone can state, which is `jobs/lines.js`'s rule at length -- a negative age
 * is a document stamped after this phone's clock, and printing it with its sign
 * would put a nonsense duration on the one reading whose whole point is that
 * the phone's clock is not the authority.
 *
 * The unit grows with the number, because an installation can be quiet for
 * hours and `jobs/lines.js`'s seconds-only form is for a line that is always
 * fresh.
 */
export function ageText(ageMs) {
  if (!Number.isFinite(ageMs) || ageMs < 0) return '';
  const seconds = Math.round(ageMs / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.round(minutes / 60)}h ago`;
}

/** The freshness line: the state, and how old the reading is when there is an
 * age to state. */
export function freshnessSentence(tile) {
  const state = tile === null || typeof tile !== 'object' ? undefined : tile.liveness;
  const words = Object.prototype.hasOwnProperty.call(LIVENESS_SENTENCES, state)
    ? LIVENESS_SENTENCES[state]
    : UNKNOWN_LIVENESS_SENTENCE;
  const age = tile === null || typeof tile !== 'object' ? '' : ageText(tile.ageMs);
  return age === '' ? words : `${words}, read ${age}`;
}

/** Whether this tile's figures are a broken series: the day has turned and its
 * counter has not arrived, or the document is not a measurement at all. A feed
 * that has delivered nothing is neither -- nothing broke, nothing was read --
 * and it is `liveness` that says which. */
function isBrokenSeries(tile) {
  if (tile === null || typeof tile !== 'object') return false;
  if (tile.liveness === 'no-document') return false;
  return tile.broken === true || tile.degraded === true;
}

/** One figure's text: the contract's mark for a broken series, `shown()`'s text
 * otherwise, plus this screen's suffix. `shown` is the app's only figure
 * formatter, so `n/a` stays `n/a`, and the mark is checked before the suffix so
 * that a `—` never becomes `—%`. */
function figureText(tile, figure, suffix) {
  const text = isBrokenSeries(tile) ? GAP_MARK : shown(figure);
  if (suffix === '' || text === 'n/a' || text === GAP_MARK) return text;
  return `${text}${suffix}`;
}

/**
 * The screen, over the four React primitives.
 *
 * Props: `{ overview, current, onSwitch }`. `overview` is `overviewTiles`'s
 * answer, `current` is the open account's id or `null`, and `onSwitch` is
 * called with a tile's id. All three are optional: a screen with none is still
 * a tree, which is what the entry's first render is.
 */
export function createOverviewScreen({ React, View, Text, Pressable }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createOverviewScreen needs React');
  }
  for (const [name, primitive] of [['View', View], ['Text', Text], ['Pressable', Pressable]]) {
    if (typeof primitive !== 'function') throw new TypeError(`createOverviewScreen needs ${name}`);
  }

  /** One figure row. */
  function figure(tileValue, spec) {
    return React.createElement(
      Text,
      { key: spec.field, type: SCREEN.FIGURE, field: spec.field },
      `${spec.label} ${figureText(tileValue, tileValue[spec.field], spec.suffix)}`,
    );
  }

  /** One installation's section. `current` (the open account's id, or `null`)
   * and `onSwitch` are passed down rather than closed over, so the section is a
   * function of its arguments and nothing in it can read a screen's props it
   * was not given. */
  function tile(tileValue, { current, onSwitch }) {
    const isCurrent = current !== undefined && current !== null && current === tileValue.id;
    const children = [
      React.createElement(Text, { key: 'name', type: SCREEN.NAME }, isCurrent ? `${tileValue.id} (current)` : tileValue.id),
      React.createElement(Text, { key: 'project', type: SCREEN.PROJECT }, tileValue.project === null ? UNKNOWN_PROJECT : tileValue.project),
      React.createElement(Text, { key: 'freshness', type: SCREEN.FRESHNESS }, freshnessSentence(tileValue)),
      React.createElement(Text, { key: 'day', type: SCREEN.DAY }, tileValue.day === null ? NO_DAY : tileValue.day),
      ...FIGURES.map((spec) => figure(tileValue, spec)),
    ];
    if (tileValue.note !== null) {
      children.push(React.createElement(Text, { key: 'note', type: SCREEN.NOTE }, tileValue.note));
    }
    // The current installation's own row has no switch control: pressing it
    // would ask for a teardown of the account that is already open, which
    // `store.switchTo` correctly does nothing for -- so the control is not
    // offered rather than offered and ignored.
    if (!isCurrent) {
      children.push(
        React.createElement(
          Pressable,
          {
            key: 'switch',
            type: SCREEN.SWITCH,
            accountId: tileValue.id,
            ...(typeof onSwitch === 'function' ? { onPress: () => onSwitch(tileValue.id) } : {}),
          },
          React.createElement(Text, { key: 'switch-word', type: SCREEN.SWITCH_LABEL }, SWITCH_WORD),
        ),
      );
    }
    return React.createElement(View, { key: tileValue.id, type: SCREEN.TILE, accountId: tileValue.id }, ...children);
  }

  return function OverviewScreen(props = {}) {
    const overview = props.overview;
    const children = [React.createElement(Text, { key: 'title', type: SCREEN.TITLE }, OVERVIEW_TITLE)];
    if (overview === null || overview === undefined || overview.ok !== true) {
      const code = overview === null || overview === undefined ? undefined : overview.code;
      children.push(
        React.createElement(
          Text,
          { key: 'refusal', type: SCREEN.REFUSAL, ...(typeof code === 'string' ? { code } : {}) },
          REFUSAL_SENTENCE,
        ),
      );
      return React.createElement(View, { type: 'overview' }, ...children);
    }
    if (overview.tiles.length === 0) {
      children.push(React.createElement(Text, { key: 'empty', type: SCREEN.EMPTY }, EMPTY_SENTENCE));
      return React.createElement(View, { type: 'overview' }, ...children);
    }
    for (const one of overview.tiles) children.push(tile(one, props));
    return React.createElement(View, { type: 'overview' }, ...children);
  };
}
