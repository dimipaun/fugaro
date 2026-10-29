// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The phone's paths, and the id rule that keeps them inside the mailbox
// (mobile plan, Task P3.6; D113, P2.1's template).
//
// Two things are tested here, and the second is the one with teeth:
//
//   * **the strings.** The five paths the plan names, composed from one id,
//     asserted literally. A reviewer can compare them with
//     `internal/budget/rules/rules.json.tmpl` by eye, which is the point of
//     asserting the literal rather than a pattern.
//   * **the rule that an id cannot leave the mailbox.** `isDatabaseKey` is a
//     port of the schema's key rule, and the test that matters is the one that
//     makes the two agree: for every candidate below, the schema's verdict on
//     a message id and this module's verdict on the same string must be the
//     same. A rule that was *stricter* would refuse an id the consumer accepts
//     -- a message that can be signed and never written; a rule that was
//     *looser* would let a path carry a separator out of the subtree, which is
//     the one thing composing a path can get wrong.
//
// The traversal cases are named as such: `../config/kill` is not a hypothetical
// (`config/kill` is the kill switch the phone can read), and neither is a `%`
// (`Unkey` parses it) or a control character (a key the database cannot hold,
// arriving through a field somebody pasted).

import assert from 'node:assert/strict';
import test from 'node:test';

import { ID_MAX, validateMessage } from '../../../shared/mailbox-schema/index.js';
import { MAILBOX_ROOT, isDatabaseKey, mailboxPaths } from './paths.js';

/** A mailbox id shaped like a Firebase uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';

test('the plan\'s five paths, composed from the account\'s uid', () => {
  // D113 (a) the message node, (b) the ack on a message it has spoken; the
  // reads are the outbox and the heartbeat. `mailbox/$mb` is the template's
  // subtree, and `$mb` is the Auth uid.
  const paths = mailboxPaths(UID);
  assert.equal(MAILBOX_ROOT, 'mailbox');
  assert.equal(paths.mailbox, `mailbox/${UID}`);
  assert.equal(paths.inbox, `mailbox/${UID}/inbox`);
  assert.equal(paths.outbox, `mailbox/${UID}/outbox`);
  assert.equal(paths.heartbeat, `mailbox/${UID}/heartbeat`);
  assert.equal(paths.inboxMessage('m-1'), `mailbox/${UID}/inbox/m-1`);
  assert.equal(paths.outboxMessage('r-1'), `mailbox/${UID}/outbox/r-1`);
  assert.equal(paths.acked('r-1'), `mailbox/${UID}/outbox/r-1/acked`);
  // The ack is a CHILD of the message, which is what the template's
  // `data.parent().exists()` rule is about: an ack for a message that is not
  // there is refused by the rules, so the path's shape is not cosmetic.
  assert.equal(paths.acked('r-1').startsWith(`${paths.outboxMessage('r-1')}/`), true);
});

test('the paths object is frozen, and so is every path in it', () => {
  const paths = mailboxPaths(UID);
  assert.equal(Object.isFrozen(paths), true);
  assert.equal(Object.isFrozen(paths.mailbox), true);
  // A component that could be rewritten after the client was built would be a
  // second answer to "where does this phone write", decided at run time.
  assert.throws(() => {
    'use strict';
    paths.inbox = 'mailbox/someone-else/inbox';
  }, TypeError);
});

test('the schema\'s key rule and this one agree, id for id', () => {
  // The agreement test. The schema validates a message's `id`, and this module
  // composes a path from it; if the two rules disagreed, the disagreement
  // would show up as a signed message the rules refuse or as a path that
  // escaped the mailbox. Candidates include the characters each language's
  // escaping differs on, because a key with one of those is a key the database
  // holds differently from the way the phone named it.
  const candidates = [
    'm-1', 'm_1', 'm.1', 'm:1', 'm/1', 'm#1', 'm[1]', 'm$1', 'm%1', 'm 1', 'm~1',
    'm-0001', 'A'.repeat(ID_MAX), 'A'.repeat(ID_MAX + 1), '', ' ', '\t', '\n',
    'm\u0000', 'm\u001f', 'm\u007f', 'm ', 'm ',
    'ид-1', '識別子', '🙂', 'm🙂', 'm\uD800', 'm\uDC00', '\uD800m',
    '../config/kill', '..', '.', '/', '//', 'a/b', 'config/kill',
  ];
  const message = (id) => ({ id, sender: 'phone', text: 'hello', at: 1_700_000_000_000, mac: 'f'.repeat(64) });
  for (const candidate of candidates) {
    const schemaSays = validateMessage(message(candidate)).ok;
    const thisSays = isDatabaseKey(candidate);
    assert.equal(thisSays, schemaSays, `${JSON.stringify(candidate)}: schema ${schemaSays}, paths ${thisSays}`);
    // ... and the composer agrees with both, rather than throwing for a value
    // the schema accepts.
    if (thisSays) {
      assert.equal(mailboxPaths(candidate).mailbox, `mailbox/${candidate}`);
      assert.equal(mailboxPaths(UID).inboxMessage(candidate), `mailbox/${UID}/inbox/${candidate}`);
    } else {
      assert.throws(() => mailboxPaths(candidate), TypeError);
      assert.throws(() => mailboxPaths(UID).inboxMessage(candidate), TypeError);
    }
  }
  // The control: the list must contain both verdicts, or the loop above is one
  // branch of a comparison that agrees by being empty.
  assert.ok(candidates.some((c) => isDatabaseKey(c)), 'some candidates are keys');
  assert.ok(candidates.some((c) => !isDatabaseKey(c)), 'some candidates are not');
});

test('an id that would leave the mailbox is refused, and the refusal says no value', () => {
  // The traversal cases by name, so a failure is readable. `%` is in the list
  // because `Unkey` parses it; `.` because the database reserves it; the
  // control character because a key with one cannot be written at all.
  for (const id of ['../config/kill', 'a/../../b', 'a%2Fb', 'a.b', 'a$b', 'a#b', 'a[b]', 'a/b', 'a\u0000b', ' ']) {
    assert.equal(isDatabaseKey(id), false, JSON.stringify(id));
    assert.throws(() => mailboxPaths(id), TypeError, JSON.stringify(id));
    assert.throws(() => mailboxPaths(UID).acked(id), TypeError, JSON.stringify(id));
  }
  // The message names the field and not the value: a hostile id is exactly the
  // value that must not be echoed into a crash report or a log line.
  try {
    mailboxPaths(UID).inboxMessage('../config/kill');
    assert.fail('the composer must refuse');
  } catch (error) {
    assert.match(error.message, /message id/);
    assert.equal(error.message.includes('config/kill'), false, error.message);
  }
  try {
    mailboxPaths('a/b');
    assert.fail('the composer must refuse');
  } catch (error) {
    assert.equal(error.message.includes('a/b'), false, error.message);
  }
});

test('a value that is not a string is refused rather than stringified', () => {
  // `String(undefined)` is `'undefined'`, which is a perfectly good database
  // key -- and a phone that composed a path from it would write a message into
  // a mailbox named `undefined`. Never coerce.
  for (const id of [undefined, null, 42, {}, [], ['m-1'], true, new Uint8Array(0)]) {
    assert.equal(isDatabaseKey(id), false, String(id));
    assert.throws(() => mailboxPaths(id), TypeError, String(id));
  }
});
