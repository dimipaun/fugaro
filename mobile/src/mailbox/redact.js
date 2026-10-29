// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The redactor, wired for the wire (mobile plan, Task P3.6; decision D118).
//
// D118 requires that nothing leaves the app's mailbox unredacted, and P1.3 put
// the redactor in `shared/` "so that the console and the phone cannot disagree
// about what is safe to say out loud". P3.6 depends on it by name (the plan's
// "Depends on" line: "P1.3 (the redactor D118 requires before anything is
// written)"), so the client does not take a redactor as an option a caller
// could forget or replace: it takes the **secret list** and builds P1.3's
// redactor from it.
//
// **Redaction happens before the tag, not after.** The redacted text is what
// is signed and what is written, so a secret that was in the utterance is not
// in the message, not in the tag, and not in whatever the consumer writes down
// afterwards. That ordering is the reason this is a separate step in
// `client.js`'s `send` rather than something the database adapter does.
//
// **A redactor that threw is a refusal, not a pass-through.** P1.3's `redact`
// throws for text that is not text and for a secret list that is not a list of
// strings ("a redactor that silently did nothing would be worse than one that
// refused"). The list is validated when the client is built, so what is left
// here is the text -- and a throw is `redact-failed`, because a message that
// was not redacted must not be written by a path that then says it was.
//
// **What redaction is not.** It is best-effort, and P1.3's header says so: a
// secret split across two utterances, or one the user read out in a way no
// string match can see, is not caught. Nothing here claims otherwise, and no
// test here pretends to prove it.

import { redactFunc } from '../../../shared/digest/redact.js';

/** The one refusal this module can add to P1.3's. */
export const REDACT_CODES = Object.freeze({
  /** P1.3's redactor threw: the text was NOT redacted, so it is not written. */
  REDACT_FAILED: 'redact-failed',
});

/**
 * Build the redactor the phone's outbound text goes through.
 *
 * The secret list is P1.3's own input -- the strings that must not leave the
 * device -- and its forms are built once, here, because a message loop redacts
 * many texts with the same list.
 *
 * @param {unknown} secrets a list of strings (the owner's configured secrets)
 * @returns {(text: string) => { ok: true, text: string } | { ok: false, code: string, field: string }}
 * @throws {Error} P1.3's own error for a list that is not a list of strings:
 *   that is a wiring bug the client's constructor is the place to catch
 */
export function outboundRedactor(secrets) {
  const redact = redactFunc(secrets);
  return (text) => {
    try {
      return Object.freeze({ ok: true, text: redact(text) });
    } catch {
      // What P1.3 threw is not carried: an error from a redactor is an error
      // about text that may hold a secret.
      return Object.freeze({ ok: false, code: REDACT_CODES.REDACT_FAILED, field: 'text' });
    }
  };
}
