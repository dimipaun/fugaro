// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's statement about its crash reports, and the privacy link
// (mobile plan, Task P7.2; D124's host list; D731's supersession of D255).
//
// **Why this module exists at all.** What the app owes a reader is not a
// control but **the sentence that says what the reports are**, and this file is
// that sentence: one fixed text, in the privacy page's own words, so a reader
// of the app and a reader of the page are told the same thing. The app **has no
// settings screen** -- no phase of the mobile plan builds one -- so the only
// surface the sentence has is the shell, and `mobile/src/app/root.js` renders
// it as its third child (`SHELL.NOTICE`). It is not a gate, not a modal and not
// a first-launch prompt: it is part of the shell, drawn on every screen.
//
// **The two rulings, and which half of the sentence each one leaves standing.**
// The owner answered **D255** on 2026-10-19 as *always on* -- no switch, no
// sheet, no stored choice -- and the statement was first written to it, in the
// page's own sentence for that answer (*"There is no switch inside the app to
// turn them off"*). **The owner's ruling of 2026-10-30 (D731) supersedes D255**:
// crash reports are **on by default and opt-out**, and Firebase Analytics is
// **dropped from the app** -- one posture everywhere, and the ruling **orders
// P7.2 and section 7.4 amended before they are built**. That amendment is task
// **T0.1**'s (`docs/review-levels-ab-jobmax/implementation-plan.md`, which
// rewrites section 6, P7.2 and the section 7.4 scan in
// `docs/plans/2026-10-19-mobile-telemetry.md`), and the build side is task
// **T3.1**'s: the app's one collection call site, read
// from the project's own `settings` node, and the page's paragraph beside it.
// Neither has landed, so this module carries **the half of the statement both
// rulings leave standing** -- the facts the live privacy page already states and
// that no ruling moves -- and makes **no claim about how collection is turned
// off**, in either direction: not D255's *"no switch inside the app"*, which
// D731 removes, and not an opt-out control, which the build does not have yet.
// The superseded clauses are pinned absent by `notice.test.js`, so putting one
// back is a deliberate act rather than a slip.
//
// **The link, and how it is built.** The statement carries the URL of the
// privacy page, and a URL leaves this app only through **P0.3's validator**
// (`shared/url/index.js`) -- never as a literal handed to the platform's link
// opener, which is the exfiltration path D124 and F27's gap 7 are about. So
// the URL is a *candidate*: `privacyLink` puts it through the validator and
// reports the verdict, which is the answer a caller that opens the URL reads.
//
// **The verdict today is a refusal, and that is the expected state.**
// `DEFAULT_HOST_ALLOW_LIST` ships **empty** -- the list is the owner's answer
// to D124, and it has not been given -- so the URL is refused with
// `allow-list-empty` and the shell draws it as text a reader can read rather
// than as a link that opens nothing. The list is the validator's **parameter**
// (it is per account), so the owner's answer is what turns the same URL into a
// link; the app has no link opener, and the task that grows one reads
// `openable` rather than calling the validator again.
//
// **What is not here.** No consent state, no stored choice, no settings key and
// no call that would set collection state on either SDK. Under D731 the app
// carries **exactly one** call site that sets collection state on Crashlytics,
// and it is T3.1's `crash.js`, not this module: this file is the one that would
// carry one if a statement were a control, so `notice.test.js` reads its source
// for both call names -- spelled with their parenthesis there, and named here
// without it so that the file can pass its own scan -- and the app-wide version
// is the section 7.4 scan T3.1 lands beside the call site.

import { DEFAULT_HOST_ALLOW_LIST, validateExternalUrl } from '../../../shared/url/index.js';

/**
 * The statement the app shows, in the privacy page's own sentences: the builds
 * that carry a crash report send it to the Fugaro project and not to the
 * reader's own, what a report holds, and the three things that never leave the
 * device.
 *
 * "What a report holds" is not decoration: a statement that says only where
 * reports go leaves a reader unable to judge what they are agreeing to, which
 * is the whole reason a reader is shown a sentence instead of a switch. The
 * four sentences are the page's own, and `notice.test.js` holds each of them to
 * that page, so a rewrite there fails here rather than leaving the app quoting
 * a page that no longer says this.
 *
 * A constant and not a parameter: it is the app's answer to what it reports,
 * and a caller that composed its own sentence would be a second statement
 * nobody checked against the page.
 */
export const STATEMENT = 'The app versions that carry crash reports send them to a Firebase '
  + 'project the Fugaro project operates, and not to your own. A crash report holds the crash\'s '
  + 'logs, diagnostics and app performance data. Never sent: the contents of any message, '
  + 'anything from your installation or its mailbox, your credentials and your keys. The app '
  + 'carries no advertising identifier.';

/** The privacy page, as the one URL this app may hand to a browser. */
export const PRIVACY_URL = 'https://fugaro.dev/privacy/';

/**
 * Whether the app may open the privacy page, and why not when it may not.
 *
 * This is the whole of the link: the app has no opener today, so
 * `mobile/src/app/root.js` draws the URL as text whatever the answer -- a link
 * that opens nothing is worse than the words it is made of -- and the task that
 * grows the opener calls this and reads `openable` rather than deciding for
 * itself which strings are safe.
 *
 * @param {readonly string[]} [hostAllowList] the hosts this installation may
 *   open, exactly (the validator's own rule: no suffix, no wildcard). Defaults
 *   to `shared/url`'s shipped list, which is **empty** until the owner answers
 *   D124.
 * @returns {{ openable: boolean, url: string, refusal: string|null }}
 *   `url` is the URL in both cases -- on success the validator returns the
 *   candidate verbatim, so the string that was checked is the string a caller
 *   opens -- and `refusal` is one of the validator's `URL_REFUSAL` codes, or
 *   `null` when the URL may be opened.
 */
export function privacyLink(hostAllowList = DEFAULT_HOST_ALLOW_LIST) {
  const verdict = validateExternalUrl(PRIVACY_URL, hostAllowList);
  return {
    openable: verdict.ok === true,
    url: PRIVACY_URL,
    refusal: verdict.ok === true ? null : verdict.code,
  };
}

/**
 * The notice the shell renders: the statement, and the link's verdict.
 *
 * The two come back together rather than as two calls, so a screen cannot draw
 * the sentence without the answer about the URL beside it.
 *
 * @param {readonly string[]} [hostAllowList] as `privacyLink`.
 * @returns {{ text: string, url: string, openable: boolean, refusal: string|null }}
 */
export function notice(hostAllowList = DEFAULT_HOST_ALLOW_LIST) {
  return { text: STATEMENT, ...privacyLink(hostAllowList) };
}
