// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's statement about its reports, and the privacy link (mobile plan,
// Task P7.2; D731's supersession of D255).
//
// **What this file is for.** What the app owes a reader is not a control but
// **the sentence that says what the reports are**, and this module is that
// sentence, in one place. That makes the module's text an app-visible fact
// rather than prose: it is what `mobile/src/app/root.js` renders as the shell's
// third child, and part of the words a store reviewer reads on the app's only
// surface.
//
// **Why the statement is the half both rulings leave standing.** D255 (always
// on) was superseded by the owner's ruling of 2026-10-30 (D731): crash reports
// on by default and opt-out, and Analytics dropped from the app. The ruling
// orders P7.2 amended before it is built (task T0.1, in
// `docs/review-levels-ab-jobmax/implementation-plan.md`) and the switch itself
// is T3.1's, so this module carries the facts the live privacy page states and
// that neither ruling moves -- and the two clauses D255 put in the statement
// are asserted **absent** below, so putting one back is a deliberate act.
//
// **The link, and the one thing this file must hold.** The statement carries
// the link to the privacy page, and the app may open a URL only through
// `shared/url`'s validator (P0.3; D124, and F27's gap 7 is why it exists at
// all). The validator's host list is the owner's answer to D124, which has not
// been given, so `DEFAULT_HOST_ALLOW_LIST` is **empty** and the URL is refused
// with `allow-list-empty` -- the app draws it as text rather than handing it to
// a browser. That refusal is therefore the assertion that passes today, and the
// case below with a list that names the host is what proves the link path is
// real rather than merely refused: when D124's answer arrives, the same call
// answers `openable` for the same URL with no edit here.
//
// **The negative half.** The amended section 7.4 refuses
// `setAnalyticsCollectionEnabled(` anywhere in the app, because D731 drops
// Analytics, and **admits exactly one** `setCrashlyticsCollectionEnabled(`
// call site -- T3.1's `crash.js`, read from the project's `settings` node.
// This module is neither of those: it carries no collection call at all, which
// is what the scan below holds, with a planted call as its control. The
// app-wide version of that scan lands with the call site it admits.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { URL_REFUSAL } from '../../../shared/url/index.js';
import { PRIVACY_URL, STATEMENT, notice, privacyLink } from './notice.js';

const SOURCE = readFileSync(join(import.meta.dirname, 'notice.js'), 'utf8');

/**
 * The page the statement quotes, read from the tree rather than retyped: the
 * app says what the page says, so the assertion that says so has to read the
 * page. It is a text file here and not a build artifact, because what this file
 * checks is wording, and `website/`'s own gate (`website/test/privacy.mjs`)
 * checks the wording the same way.
 */
const PAGE_SOURCE = readFileSync(
  join(import.meta.dirname, '..', '..', '..', 'website', 'src', 'pages', 'privacy.astro'),
  'utf8',
);

/** The host entry D124's answer is owed, written as the validator's own rule
 * states it: one exact host, no suffix and no wildcard. */
const OWN_HOST = Object.freeze(['fugaro.dev']);

/**
 * The page's clauses this statement is made of -- each one a fragment of
 * `website/src/pages/privacy.astro`, and each one asserted against both the
 * page and the statement below. A clause is a fragment rather than a whole
 * sentence because the page and the app address different numbers of versions
 * (the page says "sends two things", the app names the crash report), and the
 * point of the list is that neither side can be edited into disagreeing with
 * the other.
 *
 * The second clause is the one a statement needs before it is a statement: one
 * that says only where reports go leaves a reader unable to judge what a report
 * is, which is the whole reason a reader is shown words instead of a switch.
 *
 * When `website/src/pages/privacy.astro` is rewritten (task T3.1 moves its app
 * paragraph with the Analytics package), a clause that moves with it fails here
 * on purpose: the app's statement has to move in the same change.
 */
const PAGE_CLAUSES = [
  'to a Firebase project the Fugaro project operates, and not to your own',
  'A crash report holds the crash\'s logs, diagnostics and app performance data',
  'Never sent: the contents of any message, anything from your installation or its mailbox, your credentials and your keys',
  'The app carries no advertising identifier',
];

/** Whitespace is the page's line wrapping, not its wording. */
function flattened(text) {
  return text.replace(/\s+/g, ' ');
}

/** Whether the live page carries a clause, by the same reading this file uses
 * for the statement: one whitespace-insensitive substring. */
function pageCarries(clause) {
  return flattened(PAGE_SOURCE).includes(clause);
}

/** The clauses D255's answer put in the statement and the owner's ruling of
 * 2026-10-30 removes: the app is opt-out now, so neither is the app's to say. */
const SUPERSEDED_CLAUSES = [
  'no switch inside the app',
  'uninstalling the app stops them',
];

/** The members of the collection switches, as a scan can see them. */
const SWITCHES = ['setAnalyticsCollectionEnabled(', 'setCrashlyticsCollectionEnabled('];

/** Every collection switch a piece of text carries, as a list. */
function switchesIn(text) {
  return SWITCHES.filter((call) => text.includes(call));
}

test('the statement is the page\'s own facts, and the ruling\'s superseded clauses are gone', () => {
  // The control first, so that a green run below is the page agreeing with the
  // statement rather than the page failing to be read: a clause this file made
  // up is refused by the same reading that accepts the four real ones.
  assert.equal(
    pageCarries('the app never sends anything to anyone'),
    false,
    'the page reading must be able to refuse a clause the page does not carry',
  );

  for (const clause of PAGE_CLAUSES) {
    // Both halves: the statement carries the clause, and the page does too --
    // the app quotes a page that still says it. A sentence typed here and
    // nowhere else would pass the first assertion alone.
    assert.equal(
      pageCarries(clause),
      true,
      `website/src/pages/privacy.astro no longer carries ${JSON.stringify(clause)}, so the app's statement quotes a page that has moved: change both in the same task`,
    );
    assert.ok(STATEMENT.includes(clause), `the statement does not carry ${JSON.stringify(clause)}`);
  }
  // The supersession, asserted rather than described. D731 orders the statement
  // amended (T0.1) and the switch built (T3.1); until both land, the one thing
  // the app may not do is keep telling a reader that nothing turns the reports
  // off -- or that the only way to stop them is to uninstall the app.
  for (const clause of SUPERSEDED_CLAUSES) {
    assert.equal(
      STATEMENT.includes(clause),
      false,
      `the statement still carries ${JSON.stringify(clause)}: the owner's ruling of 2026-10-30 (D731) supersedes D255's always-on answer`,
    );
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
    text: STATEMENT,
    url: PRIVACY_URL,
    openable: false,
    refusal: URL_REFUSAL.ALLOW_LIST_EMPTY,
  });
  assert.deepEqual(notice(OWN_HOST), {
    text: STATEMENT,
    url: PRIVACY_URL,
    openable: true,
    refusal: null,
  });
});

test('the module carries no call that sets collection state', () => {
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
  // Neither belongs in this file: D731 drops Analytics outright, and the one
  // `setCrashlyticsCollectionEnabled(` call site the ruling allows is T3.1's
  // `crash.js`, which reads the project's `settings` node. A statement is not a
  // switch, and this module has to be able to pass its own scan.
  assert.deepEqual(switchesIn(SOURCE), [], 'the module this task writes carries no collection call');
});
