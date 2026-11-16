// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview's switcher: "Fugaro, switch to <installation>" (mobile plan,
// Task P6.8; D114, and P6.1's commands).
//
// **What this module is, and what it deliberately is not.** P6.1's router
// already owns the grammar: `routeUtterance` decides that "switch to aurora"
// is the `switch-project` app command and resolves the spoken name against the
// owner's list, refusing a name that is not configured rather than guessing
// one. `accounts/store.js` already owns the teardown: `switchTo` closes the
// previous account's listeners and caches **before** it opens the next, and
// refuses to open at all if the close failed. Both of those modules say so in
// their own headers, and both are tested.
//
// So there is exactly one thing left for this module to do, and it is the
// reason it exists: **join the two, in the order D114 requires**, from the
// app's own list. The list the router matches against is built here, from
// `store.list()` -- never from a caller's argument and never from a screen's
// state -- so a spoken name resolves to an account the store actually holds, at
// the moment of the switch. A name that resolves to nothing is refused before
// `switchTo` is reached, and nothing is opened or closed on the way.
//
// **Why the two steps are one call and not two.** A caller that could route an
// utterance and then choose what to do with the result could switch without the
// teardown, or hold the route's project id while the list changed underneath
// it. `switchTo` takes the routed result and does both, and its answer is the
// store's own -- so "the previous account was torn down first" is
// `store.switchTo`'s tested property rather than a second implementation of it.
// This module adds no close, no open and no listener of its own: it is a
// translation, and `switcher.test.js`'s registry trace is what keeps it one --
// a switch this module refuses leaves the trace empty.
//
// **A switch is spoken, not typed, and the difference is not a formality.** The
// router requires the wake word (D164), so an utterance that is not the app's
// is agent text and never reaches here; and this module never forwards
// anything. Neither is left to the router: `switchTo`'s guard refuses every
// result that is not the app's own switch command with `forwarded: false`, and
// `switcher.test.js` hands it one field of a real switch at a time, so each
// clause of that guard is load-bearing on its own -- a switch that left the app
// would be a sentence about an installation sent to an agent.
//
// **Where the account ids come from.** D114: the list is the owner's, and the
// id is the label the owner typed when they paired the installation (`P6.8`'s
// session takes it, `accounts/account.js` judges it). That label is also what
// the owner says out loud, so the router's `spokenName` **is** the account id
// and there is no second name to keep in step. `projects()` is the one place
// that mapping is written.

import { COMMAND, MAX_ENTRIES, REFUSAL, ROUTE, foldSpokenName, routeUtterance } from '../../../shared/command/index.js';

/** How this module refuses. Every code here is about the *switcher's* own
 * inputs: the router's refusals and the store's are carried unchanged, because
 * a caller that gets `name-not-configured` should be able to act on the
 * router's own vocabulary rather than on a translation of it. */
export const SWITCH_CODES = Object.freeze({
  NOT_A_ROUTE: 'not-a-route',
  NOT_A_SWITCH: 'not-a-switch',
  NO_SUCH_ACCOUNT: 'no-such-account',
  /** Two held labels that the voice router folds into one name. It is the
   * router's own code string for the same condition, so a caller reading either
   * vocabulary sees one word for one thing. */
  AMBIGUOUS_SPOKEN_NAME: REFUSAL.AMBIGUOUS_SPOKEN_NAME,
  /** More held labels than the router will read at once. The router's own code
   * for the same list, and taken from it for the same reason as the name above:
   * the two are one condition, so they are one word. */
  TOO_MANY_PROJECTS: REFUSAL.INVALID_PROJECTS,
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/**
 * The switcher, over an `AccountStore`.
 *
 * It holds the store and nothing else: no cache, no current account of its
 * own, no listener. "Which account is open" is asked of the store every time,
 * because a copy here would be a second answer to the question D114's teardown
 * exists to keep singular.
 */
export function createSwitcher({ store } = {}) {
  if (store === null || typeof store !== 'object' || typeof store.list !== 'function' || typeof store.switchTo !== 'function') {
    throw new TypeError('createSwitcher needs an account store');
  }

  /**
   * The router's `projects` list, from the store: D114's owner-typed labels,
   * in the store's own order.
   *
   * A list that cannot be read is a refusal rather than an empty list, and the
   * distinction is the security property: an empty list would make every
   * spoken name "not configured", which is the right answer for the wrong
   * reason and hides an unreadable store behind a router sentence.
   *
   * **And the names are the ones the router can match.** The router folds a
   * name (lowercased, edge punctuation dropped) to the key it is matched by and
   * refuses a **whole list** for one entry whose key is empty, or for two
   * entries sharing one (`shared/command`'s `checkEntries`) -- so one such label
   * in the store would leave every installation unreachable by voice, and the
   * `where-am-i` route with it. `session.js` refuses to store one
   * (`spokenRefusal`), and this is the same rule at the reading end, for a list
   * written before that check existed:
   *
   *   * an id whose key is empty (`_`, `--`) is left out, because it can
   *     neither be spoken nor matched -- nothing that could have been routed is
   *     lost, and the installation is still in the overview and still
   *     switchable by press;
   *   * two ids with one key are a refusal rather than a choice, which is the
   *     router's own rule: an ambiguity is not this module's to resolve, and
   *     the entry that loses would be unreachable by voice for ever with no
   *     sign that it was ever there.
   *
   * **And the list is not longer than the router will read.** `checkEntries`
   * refuses a list of more than `MAX_ENTRIES` entries **whole** as well, so the
   * same failure comes back through the list's *length*: a store holding one
   * account more than the bound would leave every installation unreachable by
   * voice, `where-am-i` with it. The bound is the router's own constant, taken
   * from there rather than restated, and it is checked on the list this module
   * is about to hand over -- the ids left out above are not the router's
   * business and do not count against it.
   *
   * Refused, and not trimmed to the first `MAX_ENTRIES`: which installations
   * lost their voice would then be decided by the store's own sort by id, so an
   * installation would go silent because of where its label falls in the
   * alphabet, and a later pairing could silently take another one's place. The
   * empty-key rule above can leave an entry out because nothing that could have
   * been routed is lost -- an entry past the bound is speakable and matchable,
   * and is lost only because the list is too long. A refusal says which thing
   * is wrong; a trimmed list says "not configured" about installations that are.
   */
  async function projects() {
    const listed = await store.list();
    if (!listed.ok) return listed;
    const projects = [];
    const byName = new Set();
    for (const account of listed.accounts) {
      const key = foldSpokenName(account.id);
      if (key === '') continue;
      if (byName.has(key)) return refuse(SWITCH_CODES.AMBIGUOUS_SPOKEN_NAME, 'projects');
      byName.add(key);
      projects.push(Object.freeze({ id: account.id, spokenName: account.id }));
    }
    if (projects.length > MAX_ENTRIES) return refuse(SWITCH_CODES.TOO_MANY_PROJECTS, 'projects');
    return Object.freeze({ ok: true, projects: Object.freeze(projects) });
  }

  /**
   * One routed utterance, switched.
   *
   * `result` is `routeUtterance`'s answer, given the list `projects()` built.
   * Anything that is not the `switch-project` app command is refused rather
   * than ignored: the screens that route their own transcripts hand this module
   * only the commands that are its own (`text/input.js` and `voice/dialogue.js`
   * both say so), so a caller that hands it something else has a bug this
   * refusal names at the seam instead of at the screen.
   */
  async function switchTo(result) {
    if (result === null || typeof result !== 'object') return refuse(SWITCH_CODES.NOT_A_ROUTE, 'route');
    if (result.ok !== true) {
      // The router's refusal, carried unchanged -- including its `field`, so
      // the caller can see which part of the utterance it was.
      return result;
    }
    if (result.route !== ROUTE.APP_COMMAND || result.command !== COMMAND.SWITCH_PROJECT || result.forwarded !== false) {
      return refuse(SWITCH_CODES.NOT_A_SWITCH, 'command');
    }
    const named = result.project;
    if (named === null || typeof named !== 'object' || typeof named.id !== 'string') {
      return refuse(SWITCH_CODES.NOT_A_SWITCH, 'project');
    }
    // The router resolved the name against the list this module built, so the
    // id is one the store held a moment ago. It is still read back before the
    // switch, because the list can change between the two calls -- an
    // installation removed in between must be a refusal, not a teardown of the
    // current account followed by a failed open.
    const held = await store.account(named.id);
    if (!held.ok) return refuse(SWITCH_CODES.NO_SUCH_ACCOUNT, 'project');
    // The store's own answer, unchanged: `{ ok: true, changed, account }`, or
    // its refusal when the teardown failed. Nothing is added to it and nothing
    // is done with it here -- a switch that reported success while the store
    // had no current account would be exactly the lie `store.switchTo`
    // documents itself against.
    return store.switchTo(named.id);
  }

  /**
   * "Fugaro, what project am I on": the current account, or `null`.
   *
   * The `where-am-i` route is this module's too (`text/input.js` and
   * `voice/dialogue.js` both list it beside `switch-project`), and it is the
   * other half of a switch: a switch the owner cannot check is a switch they
   * have to take on trust.
   */
  async function whereAmI() {
    const id = store.current();
    if (id === null || id === undefined) return Object.freeze({ ok: true, account: null });
    const held = await store.account(id);
    if (!held.ok) return held;
    return Object.freeze({ ok: true, account: held.account });
  }

  /**
   * The commands this module answers, as the router spells them: the two the
   * screens hand over when they see them. Exported as data so a screen's
   * dispatch and this module's own tests cannot drift from each other.
   */
  return Object.freeze({
    projects,
    switchTo,
    whereAmI,
    handles: Object.freeze([COMMAND.SWITCH_PROJECT, COMMAND.WHERE_AM_I]),
  });
}
