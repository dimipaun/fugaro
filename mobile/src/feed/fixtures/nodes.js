// Recorded nodes for the feed's tests (mobile plan, Task P3.2; D112's answer,
// tension (b1)).
//
// The plan's verification line is "tests over **recorded documents**, with no
// child process and no network", and this file is the recording: the four
// granted families' nodes, in the shapes `internal/budget/model.go` writes
// them, as a value per path -- including the paths the database answers with
// **nothing**, because "the node is not there" is one of the two ways a field
// can be absent and it is the one §10.7's rollover turns on.
//
// **Every value here is synthetic.** No fixture in this repository was recorded
// from a real installation, and none may be: `belong` is added by hand and
// never touched by an agent (Global Constraint 11), and a fixture copied out of
// a live project is that project's spend, its repository names and its account
// uid, sitting in every clone. The uid below is a made-up string with the
// *shape* a Firebase Auth uid has (alphanumeric, bounded), because the shape is
// what `paths.js` judges and the shape is all this test needs.
//
// **Why the day is a parameter.** `/spend/<day>/**` is keyed by the UTC epoch
// day, so the recording of a day's counters cannot be a constant: a fixture
// with a baked-in day would go stale at the next midnight and would say nothing
// about a rollover. `recordedNodes({ day })` returns the whole recording for
// the day it is given, and the day-change test records two.
//
// **The grown fields are deliberate.** The spend nodes carry `calls` and
// `byModel` and the cap node carries `repo` -- fields the phone does not read,
// which `nodes.js` ignores and `document.js` would refuse on a *document*. They
// are here so the leniency is a property of the recorded tree rather than of a
// comment: a fixture that carried only the fields this reader happens to use
// could not tell "ignores what it does not read" from "has never met a field it
// did not expect".

import { MICROS_PER_USD } from '../nodes.js';

/** The project name a document carries. Not a project id, not a URL. */
export const FIXTURE_PROJECT = 'fugaro';

/** A made-up account uid with a real uid's shape. */
export const FIXTURE_UID = 'AbC123def456';

/** A made-up repository slug with a real slug's shape (`[a-z0-9-]`, ≤63). */
export const FIXTURE_SLUG = 'example-one-0123456789abcdef';

/** The project's day cap: 50 USD. */
export const FIXTURE_GLOBAL_CAP_USD = 50;

/** The repository's own day cap: 5 USD, which beats the 10 USD default. */
export const FIXTURE_REPO_CAP_USD = 5;

/**
 * One day's recording of the four granted families.
 *
 * @param options.day the UTC epoch day the spend nodes are keyed by
 *   (`paths.js`'s `dayKey`). Required: a recording that did not say which day
 *   it was of would be a recording of no day in particular.
 * @param options.uid the account's Auth uid, which names the mailbox.
 * @param options.slug the repository slug.
 * @param options.atMs the instant the heartbeat advertises.
 * @returns a frozen path -> node map. A `null` value is the database's own
 *   answer for a node that is not there.
 */
export function recordedNodes({
  day,
  uid = FIXTURE_UID,
  slug = FIXTURE_SLUG,
  atMs = 0,
} = {}) {
  if (!Number.isInteger(day) || day < 0) throw new TypeError('recordedNodes needs the UTC epoch day');
  if (!Number.isInteger(atMs) || atMs < 0) throw new TypeError('recordedNodes needs an instant in ms');
  return Object.freeze({
    'config/caps/global': Object.freeze({
      dailyMicros: FIXTURE_GLOBAL_CAP_USD * MICROS_PER_USD,
      perRunMicros: 2 * MICROS_PER_USD,
    }),
    'config/caps/defaults': Object.freeze({
      repoDailyMicros: 10 * MICROS_PER_USD,
      repoPerRunMicros: MICROS_PER_USD,
    }),
    [`config/caps/repos/${slug}`]: Object.freeze({
      dailyMicros: FIXTURE_REPO_CAP_USD * MICROS_PER_USD,
      repo: 'example/one',
    }),
    // Not killed: the switch node is not there, and "not killed" is a fact.
    'config/kill/global': null,
    [`config/kill/repos/${slug}`]: null,
    [`spend/${day}/global`]: Object.freeze({
      counted: 12_500_000,
      spent: 12_000_000,
      notional: 40 * MICROS_PER_USD,
      calls: 9,
      byModel: Object.freeze({ 'a-model': Object.freeze({ micros: 1_000_000, in: 2_000, out: 500 }) }),
    }),
    [`spend/${day}/repos/${slug}`]: Object.freeze({
      counted: 3_000_000,
      spent: 2_900_000,
      notional: 8 * MICROS_PER_USD,
      calls: 3,
    }),
    // The phone's own mailbox. The inbox is not granted and is not recorded.
    [`mailbox/${uid}/outbox`]: null,
    [`mailbox/${uid}/heartbeat`]: Object.freeze({ at: atMs, boot: 'b1', agents: Object.freeze([Object.freeze({ id: 'a' })]) }),
  });
}

/** The paths a recording covers, in the order the reader plans them. */
export function recordedPaths({ day, uid = FIXTURE_UID, slug = FIXTURE_SLUG } = {}) {
  return Object.freeze([
    'config/caps/global',
    'config/caps/defaults',
    `config/caps/repos/${slug}`,
    'config/kill/global',
    `config/kill/repos/${slug}`,
    `spend/${day}/global`,
    `spend/${day}/repos/${slug}`,
    `mailbox/${uid}/outbox`,
    `mailbox/${uid}/heartbeat`,
  ]);
}
