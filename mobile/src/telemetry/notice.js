// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's statement about its crash and usage reports, and the privacy link
// (mobile plan, Task P7.2; D255's answer, and D124's host list).
//
// **Why this module exists at all.** The owner answered D255 *always on*:
// collection is on in the builds that carry the SDKs, and there is no switch,
// no first-launch sheet and no stored choice -- nothing in the app turns the
// reports off. What is left of the disclosure half is that the app **says**
// what it does, and this file is that sentence: one fixed text, in the privacy
// page's own words, so a reader of the app and a reader of the page are told
// the same thing. It carries **no control**, deliberately: a control here would
// be the switch D255 says does not exist.
//
// **Where it is shown, and why here.** The app has no settings screen -- no
// phase of the mobile plan builds one -- so the only surface the statement has
// is the shell, and `mobile/src/app/root.js` renders this text as its third
// child (`SHELL.NOTICE`). It is not a gate, not a modal and not a first-launch
// prompt: it is part of the shell, drawn on every screen.
//
// **The link, and how it is built.** The statement carries the URL of the
// privacy page, and a URL leaves this app only through **P0.3's validator**
// (`shared/url/index.js`) -- never as a literal handed to the platform's link
// opener, which is the exfiltration path D124 and F27's gap 7 are about. So
// the URL is a *candidate*: `privacyLink` puts it through the validator and
// reports the verdict, and the caller may open it only when the verdict says
// so.
//
// **The verdict today is a refusal, and that is the expected state.**
// `DEFAULT_HOST_ALLOW_LIST` ships **empty** -- the list is the owner's answer
// to D124, and it has not been given -- so the URL is refused with
// `allow-list-empty` and the shell draws it as text a reader can read rather
// than as a link that opens nothing. When the owner's answer names the host,
// exactly one entry of the validator's own shape, this module opens the same
// URL with no edit: the verdict is the only thing that changes.
//
// **What is not here.** No consent state, no stored choice, no settings key
// and no call that would set collection state on either SDK: this module is
// the one that would carry such a call if the app had a control, and the
// section 7.4 scan (P7.3) refuses one anywhere in `mobile/`. `notice.test.js`
// reads this file for both call names, so the module this task writes cannot
// be the one that reintroduces the switch.

import { DEFAULT_HOST_ALLOW_LIST, validateExternalUrl } from '../../../shared/url/index.js';

/**
 * The statement the app shows, in the privacy page's own words: the reports
 * are sent by the builds that carry these SDKs, there is no switch inside the
 * app, and uninstalling it stops them.
 *
 * A constant and not a parameter: it is the app's answer to D255, and a caller
 * that composed its own sentence would be a second statement nobody checked
 * against the page.
 */
export const STATEMENT = 'The app versions that carry these reports send them. There is no switch '
  + 'inside the app to turn them off: uninstalling the app stops them, and not installing it '
  + 'avoids them.';

/** The privacy page, as the one URL this app may hand to a browser. */
export const PRIVACY_URL = 'https://fugaro.dev/privacy/';

/**
 * Whether the app may open the privacy page, and why not when it may not.
 *
 * This is the whole of the link: `mobile/src/app/root.js` draws the URL as
 * text whatever the answer, because this app carries no control over the
 * reports and a link that opens nothing is worse than the words it is made of
 * -- and a later task that grows the control reads `openable` rather than
 * calling the validator again.
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
