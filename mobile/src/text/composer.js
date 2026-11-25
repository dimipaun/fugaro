// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The composer: the draft that outlives the view
// (mobile identity and pairing plan, Task X2; the spec's "Long text input").
//
// **What it is.** One string -- what the owner has typed or pasted and not yet
// sent -- held outside the screen that draws it. That is the whole of it, and
// the reason it is a module rather than a `useState` in the text screen: D167's
// text screen is one of two views of one session, and a phone rebuilds a view
// for reasons that have nothing to do with the owner's draft -- a switch to the
// voice view and back, a rotation. A draft held in the view's own state is a
// draft the rebuild loses, and losing what somebody pasted is the failure the
// spec's sentence is about ("Pasting large blocks, especially specs for the
// spec-to-PR skill, must work smoothly").
//
// **No cap, on purpose.** The plan's default is *"long paste 100000 characters
// without loss"*, and `LONG_PASTE_CHARS` names that figure -- as the size the
// tests drive and the bar this module is held to, never as a limit. A limit
// would be the very failure the sentence names: a paste silently shortened at
// the boundary loses input, and a composer that answered "too long" to a spec
// being pasted would have refused the one thing it exists for. A string of that
// size is about a hundred kilobytes; the bound on a real paste is the platform
// text field's, not this store's, and a store that guessed a second one would
// be a store that drops what the owner pasted.
//
// **It writes nothing, and it sends nothing.** The draft is a value on the
// phone and nowhere else: the send is `src/mailbox`'s client (the app's one
// write path, D113), and this module holds no client, no store, no clock and no
// path. `clear()` is the caller's call **after** a send the client accepted,
// because only the caller saw the send -- the composer cannot see it and must
// not guess one happened. Redaction is the client's too (D118): what `text()`
// answers is what the owner put in, and what reaches the session is the
// client's business.
//
// **What it deliberately is not.** Not the send button, not the confirmation
// machine (P6.2's), not the typed pipeline (`input.js`, unchanged and still the
// only door that writes), and not a second place a line can enter the one
// conversation (X1's transcript). The whole wiring is two props: the text
// screen's `draft` is `text()` and its `onChangeText` is `change`. `subscribe`
// is for the view that wants to re-read on every change rather than drive a
// re-render from its own handler -- the same shape X1's transcript hands out,
// for the same reason: the store is the state, and a view that kept a second
// copy of it is the copy that loses a keystroke.

/** The refusal codes this module answers with. One fault, one code. */
export const COMPOSER_CODES = Object.freeze({
  /** The value handed to `change` is not a string at all. */
  BAD_TEXT: 'bad-text',
  /** A subscriber that is not a listener. */
  BAD_LISTENER: 'bad-listener',
});

/**
 * The paste size the plan's default names: 100000 characters, held whole. It is
 * the figure the tests drive and the bar the module is held to -- **not** a
 * limit, because a limit is the loss the sentence is about (see the header).
 */
export const LONG_PASTE_CHARS = 100000;

function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * Build one composer.
 *
 * No arguments: there is nothing to configure, and nothing here reads a clock,
 * a file, the network or a random source -- a draft needs none of them, and a
 * store that took a clock would be a store with an opinion about when a
 * keystroke happened, which is the view's business and not the draft's.
 *
 * @returns {object} the composer: `text`, `length`, `change`, `clear`,
 *   `subscribe`
 */
export function createComposer() {
  /** The draft, verbatim, and empty until the first change. */
  let draft = '';
  /** The subscribers, in the order they subscribed. */
  const listeners = new Set();

  /** Tell every subscriber, one at a time. */
  function announce() {
    const state = Object.freeze({ text: draft, length: draft.length });
    for (const listener of [...listeners]) {
      try {
        listener(state);
      } catch {
        // A subscriber that threw is not this store's failure, and the app has
        // no logger to hand it to (`boundary.test.js` rule 4). The others still
        // hear the change, which is what keeps one view's fault from stopping
        // the keyboard.
      }
    }
  }

  /**
   * Replace the draft with what the field now holds.
   *
   * `onChangeText` hands the whole value, so this is a replace and not an
   * append; a paste arrives here as one string of any length and is stored
   * verbatim -- no trim, no normalisation, no cap (see the header).
   *
   * A value that is not a string is refused and **the draft is kept**: a
   * caller that handed a number would otherwise have wiped what the owner
   * pasted, which is the loss this module exists to prevent.
   *
   * @param {unknown} value the field's whole value
   * @returns {{ok: true, length: number} | {ok: false, code: string, field: string}}
   */
  function change(value) {
    if (typeof value !== 'string') return refuse(COMPOSER_CODES.BAD_TEXT, 'text');
    draft = value;
    announce();
    return Object.freeze({ ok: true, length: draft.length });
  }

  /**
   * Empty the draft, after a send the caller saw accepted. The store cannot
   * tell a sent draft from an unsent one, so it never clears one by itself.
   *
   * @returns {{ok: true, length: number}}
   */
  function clear() {
    draft = '';
    announce();
    return Object.freeze({ ok: true, length: 0 });
  }

  /**
   * Hear every change from now on. The listener is handed the frozen
   * `{ text, length }`; the answer is the function that unsubscribes, as C2's
   * `on` answers.
   *
   * @param {unknown} listener
   * @returns {{ok: true, unsubscribe: () => void} | {ok: false, code: string, field: string}}
   */
  function subscribe(listener) {
    if (typeof listener !== 'function') return refuse(COMPOSER_CODES.BAD_LISTENER, 'listener');
    listeners.add(listener);
    return Object.freeze({
      ok: true,
      unsubscribe: () => {
        listeners.delete(listener);
      },
    });
  }

  return Object.freeze({
    /** The draft, verbatim: what the field should show. */
    text: () => draft,
    /** Its length, for a caller that shows a counter. */
    length: () => draft.length,
    change,
    clear,
    subscribe,
  });
}
