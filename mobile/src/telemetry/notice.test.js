// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The always-on statement and the privacy link (mobile plan, Task P7.2).
//
// **What this file is for.** D255 was answered *always on*: there is no
// consent switch, no first-launch sheet and no stored choice anywhere in the
// app, so what the app owes a reader is not a control but **the sentence that
// says what it does** -- and this module is that sentence, in one place. That
// makes the module's text an app-visible fact rather than prose: it is what
// `mobile/src/app/root.js` renders as the shell's third child, and the words a
// store reviewer reads on the app's only surface.
//
// **The link, and the one thing this file must hold.** The statement carries
// the link to the privacy page, and the app may open a URL only through
// `shared/url`'s validator (P0.3; D124, and F27's gap 7 is why it exists at
// all). The validator's host list is the owner's answer to D124, which has not
// been given, so `DEFAULT_HOST_ALLOW_LIST` is **empty** and the URL is refused
// with `allow-list-empty` -- the app must render it as text rather than hand it
// to a browser. That refusal is therefore the assertion that passes today, and
// the case below with a list that names the host is what proves the link path
// is real rather than merely refused: when D124's answer arrives, the same
// call opens the same URL with no edit here.
//
// **The negative half.** A call that sets collection state *is* the switch
// D255's answer says does not exist, whether it is passed `true` or `false`, so
// this module -- the one that would carry such a call if the app had a control
// -- is read for one. The whole-app version of that scan is the telemetry
// plan's section 7.4, which P7.3 lands; here it is the module this task writes,
// with a planted control so a green run below means the check can fail.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { URL_REFUSAL } from '../../../shared/url/index.js';
import { PRIVACY_URL, STATEMENT, notice, privacyLink } from './notice.js';

const SOURCE = readFileSync(join(import.meta.dirname, 'notice.js'), 'utf8');

/** The host entry D124's answer is owed, written as the validator's own rule
 * states it: one exact host, no suffix and no wildcard. */
const OWN_HOST = Object.freeze(['fugaro.dev']);

/** The statement the page states, and the three facts it has to carry. */
const PAGE_WORDS = 'The app versions that carry these reports send them. There is no switch '
  + 'inside the app to turn them off: uninstalling the app stops them, and not installing it '
  + 'avoids them.';

/** The members of the collection switches, as a scan can see them: a call that
 * sets collection state is the switch D255's answer says does not exist. */
const SWITCHES = ['setAnalyticsCollectionEnabled(', 'setCrashlyticsCollectionEnabled('];

/** Every collection switch a piece of text carries, as a list. */
function switchesIn(text) {
  return SWITCHES.filter((call) => text.includes(call));
}

test('the statement is the page\'s own three facts, as one fixed text', () => {
  // The text and the page's sentence are the same sentence, deliberately: a
  // reader who opens the app and a reader who opens the privacy page are owed
  // one statement, not two readings of one decision.
  assert.equal(STATEMENT, PAGE_WORDS);
  for (const fact of [
    'carry these reports send them',
    'no switch inside the app',
    'uninstalling the app stops them',
  ]) {
    assert.ok(STATEMENT.includes(fact), `the statement does not carry ${JSON.stringify(fact)}`);
  }
});

test('the module holds one URL, and it is the privacy page', () => {
  assert.equal(PRIVACY_URL, 'https://fugaro.dev/privacy/');
  // One URL, and no second one to disagree with it: the module is the app's
  // only source for a link that leaves it.
  assert.deepEqual(SOURCE.match(/https?:\/\/[^\s'"`)]+/g) ?? [], [PRIVACY_URL]);
});

test('the shipped host list refuses the link, and the refusal is the validator\'s own', () => {
  const link = privacyLink();
  assert.equal(link.openable, false);
  // The code is read from the validator rather than retyped, so a refusal this
  // module reports is one `shared/url` actually produced.
  assert.equal(link.refusal, URL_REFUSAL.ALLOW_LIST_EMPTY);
  assert.equal(link.url, PRIVACY_URL, 'a refused URL is still the URL, drawn as text');
});

test('a host list naming the host opens the link, and another host does not', () => {
  // The positive control, and the state D124's answer produces: the same call,
  // the same URL, no edit in this module.
  const opened = privacyLink(OWN_HOST);
  assert.equal(opened.openable, true);
  assert.equal(opened.refusal, null);
  assert.equal(opened.url, PRIVACY_URL);

  // The list is exact -- never a suffix, never a wildcard -- so a host that
  // merely looks like the page's is refused rather than followed.
  for (const list of [['github.com'], ['fugaro.dev.example'], ['example.com', 'evil.example']]) {
    const refused = privacyLink(list);
    assert.equal(refused.openable, false, list.join(', '));
    assert.equal(refused.refusal, URL_REFUSAL.HOST_NOT_ALLOWED, list.join(', '));
  }
});

test('the notice is the statement and the link\'s verdict, from one source', () => {
  // The shell renders this and nothing else about the reports: the text and the
  // link's state come back together, so a screen cannot draw one without the
  // other's answer.
  assert.deepEqual(notice(), {
    text: PAGE_WORDS,
    url: PRIVACY_URL,
    openable: false,
    refusal: URL_REFUSAL.ALLOW_LIST_EMPTY,
  });
  assert.deepEqual(notice(OWN_HOST), {
    text: PAGE_WORDS,
    url: PRIVACY_URL,
    openable: true,
    refusal: null,
  });
});

test('the module carries no switch that turns collection off', () => {
  // The control first: the scan must flag a planted call, or the green run
  // below would mean nothing. It is asserted against the call names rather
  // than against a pattern, because these are the two names that exist.
  assert.deepEqual(
    switchesIn('await analytics().setAnalyticsCollectionEnabled(false);'),
    ['setAnalyticsCollectionEnabled('],
  );
  assert.deepEqual(
    switchesIn('crashlytics().setCrashlyticsCollectionEnabled(true);'),
    ['setCrashlyticsCollectionEnabled('],
  );
  assert.deepEqual(switchesIn(SOURCE), [], 'the module this task writes has to be able to pass its own scan');
});
