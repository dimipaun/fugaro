// The message schema's test (mobile plan, Task P0.2, extended by P6.3).
//
// Written BEFORE the module it imports, on purpose — the same red step P0.1
// took with `shared/skeleton/`: `npm test` from `shared/` discovers this file
// through the manifest's glob and fails with ERR_MODULE_NOT_FOUND until
// `shared/mailbox-schema/index.js` exists. A green run over a directory the
// glob never reached would prove nothing (shared/README.md says why).
//
// The schema is the protocol (plan P0.2, extended by P6.3): what P1.1's state
// machine validates against, what P3.6's client writes and what P4.1's
// consumer reads. Its rule is the plan's rule — **fail closed**: an unknown
// field, a wrong type, an oversize body and every other ambiguity is a REFUSAL
// with its own code, never a coercion. So the cases below are mostly refusals,
// and each one is grouped by the code it must carry, because a validator that
// refuses everything with one code cannot be told apart from one that refuses
// nothing it means to.
//
// P6.3 adds the fields D161, D167, D168, D169 and D171 need — `agent`, `kind`,
// `options`, `reply_to`, the action record, and a `mac` required on every
// message and on the heartbeat — and `canonicalBytes`, the one serialisation
// the MAC covers. The cases for those are at the end, and the two that carry
// the most weight are the ones a MAC exists for: a valid tag over a message
// whose `text`, `options`, `reply_to` or answer has been swapped must FAIL to
// verify, and the vectors file (`vectors.json`) must reproduce byte for byte,
// because the Go consumer reimplements this serialisation and tests against
// the same file.
//
// What this file deliberately does NOT test, because it is not this task's:
// the redactor (P1.3 — text is redacted before it is validated, and the case
// near the end pins that boundary rather than assuming it), the replay window
// and the dedupe table (P1.1), the URL rules (P0.3) and the ack's own rules
// (P2.1). The schema decides one thing: whether a value is a message.
//
// No network, no clock, no filesystem, no randomness, no dependency: node's
// own test runner, assert, and the JSON reader for the vectors file, over
// plain values. No secret-shaped literal is committed here or in the vectors:
// the test key is an obviously fake string, and one case asserts exactly that.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  ACTIONS,
  DIRECTIONS,
  AGENTS_MAX,
  AGENT_MAX,
  AGENT_NAME_MAX,
  AT_MAX,
  FIELDS,
  ID_MAX,
  KINDS,
  MAC_MAX,
  OPTIONS_MAX,
  OPTION_MAX,
  REFUSALS,
  REQUIRED_FIELDS,
  SENDER_MAX,
  TARGET_MAX,
  TEXT_MAX,
  canonicalBytes,
  isMessage,
  validateHeartbeat,
  validateMessage,
} from './index.js';
// The tag is a separate module on purpose: ./index.js imports nothing, so a
// bundler that resolves no node builtin (React Native's) can still load the
// validator and the canonical serialisation. The phone imports index.js and
// implements HMAC-SHA-256 itself against the vectors; this file imports the
// Node implementation of it, which is what the tests and the generator use.
import { macOf, verifyMac } from './mac.js';

// FAKE_MAC is mac-SHAPED and nothing more: 64 lowercase hex characters that no
// key produces. The schema checks a mac's SHAPE and never its value — it has
// no key to check it with — so a well-formed message only needs a well-formed
// tag; the vectors file is where a REAL HMAC is pinned, over an obviously fake
// key, and where a swapped field under a valid tag is refused.
const FAKE_MAC = 'f'.repeat(MAC_MAX);

// good returns a fresh well-formed message; overrides are merged in so a case
// can break exactly one thing.
function good(over = {}) {
  return {
    id: 'm-0001',
    sender: 'phone',
    text: 'run 42 finished',
    at: 1_700_000_000_000,
    mac: FAKE_MAC,
    ...over,
  };
}

// anAction returns a COMPLETE action record for its verb, so a case can break
// exactly one part of one. The default is a cancel of a run, which is the
// verb whose target is a run id; a merge's target is a repository and a pull
// request, and it alone carries the head SHA read back at confirmation.
function anAction(over = {}) {
  const verb = over.verb ?? 'cancel';
  const record = {
    id: 'a-0001',
    verb,
    target: { run: '20261018-125859-d3c8' },
    confirmation: { id: 'c-0001', at: 1_700_000_000_000 },
    ...over,
  };
  if (verb === 'merge') {
    record.target = over.target ?? { repo: 'dimipaun/fugaro', pr: 532 };
    if (!Object.hasOwn(record, 'head_sha')) record.head_sha = 'a'.repeat(40);
  }
  return record;
}

// withAction wraps an action record in a well-formed action message.
function withAction(over = {}) {
  return good({ kind: 'action', agent: 'orchestrator', action: anAction(over), text: 'cancel the run' });
}

// aHeartbeat returns a fresh well-formed heartbeat.
function aHeartbeat(over = {}) {
  return {
    at: 1_700_000_000_000,
    boot: 'boot-01',
    agents: [{ id: 'orchestrator', name: 'Orchestrator' }],
    mac: FAKE_MAC,
    ...over,
  };
}

// without returns a copy of a well-formed message with one field removed.
function without(field) {
  const value = good();
  delete value[field];
  return value;
}

// refusal asserts the verdict for value is a refusal carrying code, and that
// no message came with it.
function refusal(value, code, field) {
  const verdict = validateMessage(value);
  assert.equal(verdict.ok, false, `expected a refusal for ${JSON.stringify(value)}`);
  assert.equal(verdict.code, code, `code for ${JSON.stringify(value)}`);
  assert.equal(verdict.message, undefined, 'a refusal carries no message');
  if (field !== undefined) {
    assert.equal(verdict.field, field, `field for ${JSON.stringify(value)}`);
  }
  return verdict;
}

// heartbeatRefusal is refusal() for the heartbeat's own validator, which is a
// different function over a different field set (P6.3, O7).
function heartbeatRefusal(value, code, field) {
  const verdict = validateHeartbeat(value);
  assert.equal(verdict.ok, false, `expected a refusal for ${JSON.stringify(value)}`);
  assert.equal(verdict.code, code, `code for ${JSON.stringify(value)}`);
  assert.equal(verdict.heartbeat, undefined, 'a refusal carries no heartbeat');
  if (field !== undefined) {
    assert.equal(verdict.field, field, `field for ${JSON.stringify(value)}`);
  }
  return verdict;
}

test('a well-formed message is accepted, and comes back frozen', () => {
  const verdict = validateMessage(good());
  assert.equal(verdict.ok, true);
  assert.deepEqual(verdict.message, good());
  assert.equal(Object.isFrozen(verdict.message), true, 'a validated message is frozen');
});

test('the accepted message is a copy, not the caller\'s object', () => {
  const raw = good();
  const { message } = validateMessage(raw);
  assert.notEqual(message, raw);
  assert.equal(Object.getPrototypeOf(message), Object.prototype, 'a plain, unpolluted object');
  // And the schema never coerces: what the caller wrote is what the consumer
  // reads, for every field. A space is a legal database-key character, so a
  // padded id is a legal id — and it is the id that was written, not a
  // repaired one, because two agents disagreeing about an id is exactly the
  // failure at-least-once delivery cannot survive.
  const spaced = validateMessage(good({ id: ' m-0001 ', text: '  padded  ', sender: ' phone ' }));
  assert.equal(spaced.message.id, ' m-0001 ', 'id is not trimmed');
  assert.equal(spaced.message.text, '  padded  ', 'text is not trimmed');
  assert.equal(spaced.message.sender, ' phone ', 'sender is not trimmed');
});

test('the copy is deep: a nested record is copied and frozen too (P6.3)', () => {
  // P6.3 put records INSIDE a message — an action, its target, its
  // confirmation, a prompt's options, a heartbeat's agents — and a validated
  // record that could still be edited after its tag was checked would be a
  // record whose tag proves nothing. Copying the top level and freezing it is
  // not enough: `message.action.verb` is the field the consumer EXECUTES.
  const raw = withAction();
  const { message } = validateMessage(raw);
  assert.notEqual(message.action, raw.action, 'the action record is a copy');
  assert.notEqual(message.action.target, raw.action.target, 'and so is its target');
  assert.notEqual(message.action.confirmation, raw.action.confirmation, 'and its confirmation');
  assert.deepEqual(message.action, raw.action, 'with the same fields');
  for (const nested of [message.action, message.action.target, message.action.confirmation]) {
    assert.equal(Object.isFrozen(nested), true, 'frozen at every depth');
  }
  // The proof, not the property: changing the caller's record after validation
  // leaves the validated message saying what it said.
  raw.action.verb = 'merge';
  raw.action.target.run = '-x';
  raw.text = 'something else';
  assert.equal(message.action.verb, 'cancel');
  assert.equal(message.action.target.run, '20261018-125859-d3c8');
  assert.equal(message.text, 'cancel the run');
  // The same for the two lists, and for each advertised agent.
  const prompt = validateMessage(good({ kind: 'prompt', options: ['merge it'] }));
  assert.equal(Object.isFrozen(prompt.message.options), true);
  const beat = validateHeartbeat(aHeartbeat());
  assert.equal(Object.isFrozen(beat.heartbeat.agents), true);
  assert.equal(Object.isFrozen(beat.heartbeat.agents[0]), true);
});

test('an optional ack is accepted, present or absent, and absent means not acked', () => {
  assert.equal(validateMessage(good({ acked: true })).ok, true);
  assert.equal(validateMessage(good({ acked: false })).ok, true);
  const { message } = validateMessage(good());
  assert.equal(Object.hasOwn(message, 'acked'), false, 'an absent ack adds no field');
});

test('a value that is not an object is refused', () => {
  for (const value of [null, undefined, 7, 'message', true, [], [good()], () => {}]) {
    refusal(value, REFUSALS.NOT_AN_OBJECT);
  }
});

test('a missing required field is refused with the field named', () => {
  // REQUIRED_FIELDS, not a list written out here: the schema's own list is the
  // contract, and `mac` joined it in P6.3 (it is required on every outbox
  // message and every inbox message of every kind, D169 and §3.5).
  for (const field of REQUIRED_FIELDS) {
    const value = good();
    delete value[field];
    refusal(value, REFUSALS.MISSING_FIELD, field);
  }
  // A heartbeat's own required set is a different one, and it is checked the
  // same way: `at`, `boot` and `mac`, with `agents` optional.
  for (const field of ['at', 'boot', 'mac']) {
    const value = aHeartbeat();
    delete value[field];
    assert.equal(validateHeartbeat(value).code, REFUSALS.MISSING_FIELD, `heartbeat without ${field}`);
  }
});

test('an unknown field is refused, so the tree never stores what it did not validate', () => {
  refusal(good({ extra: 'anything' }), REFUSALS.UNKNOWN_FIELD, 'extra');
  refusal(good({ Acked: true }), REFUSALS.UNKNOWN_FIELD, 'Acked'); // the field names are exact
  refusal(good({ Text: 'x' }), REFUSALS.UNKNOWN_FIELD, 'Text');
  // A key arriving from JSON, where __proto__ is an own property: the schema
  // names its fields, so it is an unknown field, not pollution.
  const smuggled = JSON.parse(
    `{"id":"m-0001","sender":"phone","text":"hi","at":1,"__proto__":{"polluted":true}}`,
  );
  refusal(smuggled, REFUSALS.UNKNOWN_FIELD, '__proto__');
  assert.equal({}.polluted, undefined, 'nothing was polluted');
});

test('a wrong type is refused, never coerced', () => {
  refusal(good({ id: 1 }), REFUSALS.WRONG_TYPE, 'id');
  refusal(good({ id: {} }), REFUSALS.WRONG_TYPE, 'id');
  refusal(good({ sender: ['phone'] }), REFUSALS.WRONG_TYPE, 'sender');
  refusal(good({ text: null }), REFUSALS.WRONG_TYPE, 'text');
  refusal(good({ at: '1700000000000' }), REFUSALS.WRONG_TYPE, 'at');
  refusal(good({ acked: 'true' }), REFUSALS.WRONG_TYPE, 'acked');
  refusal(good({ acked: 0 }), REFUSALS.WRONG_TYPE, 'acked');
  // Present-but-undefined is its own ambiguity, and it is a refusal too: a
  // field that exists must hold a value.
  refusal(good({ id: undefined }), REFUSALS.WRONG_TYPE, 'id');
  refusal(good({ acked: undefined }), REFUSALS.WRONG_TYPE, 'acked');
});

test('an id is a database key: the tree\'s own forbidden characters are refused', () => {
  // The same predicate the CLI's budget.Key already applies (internal/budget),
  // because a message id is a node key under the mailbox the rules grant.
  for (const char of ['%', '.', '$', '#', '[', ']', '/']) {
    refusal(good({ id: `m${char}1` }), REFUSALS.BAD_KEY, 'id');
    refusal(good({ id: char }), REFUSALS.BAD_KEY, 'id');
  }
  refusal(good({ id: 'm\u00001' }), REFUSALS.BAD_KEY, 'id'); // a control character
  refusal(good({ id: 'm\u001F1' }), REFUSALS.BAD_KEY, 'id');
  refusal(good({ id: 'm\u007F1' }), REFUSALS.BAD_KEY, 'id');
  // The near misses the tree's other keys also refuse as characters: these are
  // ordinary characters and stay legal, so the rule is a character rule and
  // not a "reject anything unusual" rule.
  for (const id of ['m-0001', 'm_0001', 'M0001', '0', 'a'.repeat(ID_MAX)]) {
    assert.equal(validateMessage(good({ id })).ok, true, `id ${JSON.stringify(id)} is accepted`);
  }
});

test('an id is non-empty and bounded', () => {
  refusal(good({ id: '' }), REFUSALS.EMPTY_STRING, 'id');
  refusal(good({ id: 'a'.repeat(ID_MAX + 1) }), REFUSALS.TOO_LONG, 'id');
});

test('a sender is non-empty, bounded and a single line', () => {
  refusal(good({ sender: '' }), REFUSALS.EMPTY_STRING, 'sender');
  refusal(good({ sender: 'a'.repeat(SENDER_MAX + 1) }), REFUSALS.TOO_LONG, 'sender');
  refusal(good({ sender: 'phone\n' }), REFUSALS.BAD_CHARACTER, 'sender');
  refusal(good({ sender: 'phone\u0000' }), REFUSALS.BAD_CHARACTER, 'sender');
  assert.equal(validateMessage(good({ sender: 'a'.repeat(SENDER_MAX) })).ok, true);
  // A sender is a value, not a path segment: the database-key rule is the id's,
  // so an identity that carries a dot or an @ is still a sender.
  assert.equal(validateMessage(good({ sender: 'owner@example.com' })).ok, true);
});

test('the body is non-empty and carries the size bound', () => {
  refusal(good({ text: '' }), REFUSALS.EMPTY_STRING, 'text');
  refusal(good({ text: 'x'.repeat(TEXT_MAX + 1) }), REFUSALS.TOO_LONG, 'text');
  assert.equal(validateMessage(good({ text: 'x'.repeat(TEXT_MAX) })).ok, true, 'the bound itself');
  // A body is speech: it may be several lines, and a line break is not an
  // error the phone should have to strip before writing.
  assert.equal(validateMessage(good({ text: 'one\ntwo\tthree' })).ok, true);
  refusal(good({ text: 'one\u0000two' }), REFUSALS.BAD_CHARACTER, 'text');
  refusal(good({ text: 'one\u007Ftwo' }), REFUSALS.BAD_CHARACTER, 'text');
});

test('a string that cannot survive JSON is refused', () => {
  // An unpaired surrogate is not a character: it encodes to broken JSON, so a
  // value carrying one is not a message that can be written at all.
  const lone = '\uD800';
  refusal(good({ id: `m${lone}` }), REFUSALS.BAD_KEY, 'id');
  refusal(good({ sender: `p${lone}` }), REFUSALS.BAD_CHARACTER, 'sender');
  refusal(good({ text: `x${lone}y` }), REFUSALS.BAD_CHARACTER, 'text');
  assert.equal(validateMessage(good({ text: 'x😀y' })).ok, true, 'a paired surrogate is fine');
});

test('a timestamp is an epoch-millisecond safe integer, and nothing else', () => {
  assert.equal(validateMessage(good({ at: 0 })).ok, true);
  assert.equal(validateMessage(good({ at: AT_MAX })).ok, true);
  for (const at of [-1, -0.5, 1.5, NaN, Infinity, -Infinity, AT_MAX + 1, 2 ** 53]) {
    refusal(good({ at }), REFUSALS.BAD_TIMESTAMP, 'at');
  }
});

test('every refusal family has its own code', () => {
  // The codes are the contract P1.1, P3.6 and P4.1 branch on: two families
  // sharing one code would make "refused" indistinguishable from "refused for
  // the other reason", which is what a fail-closed schema exists to avoid.
  const codes = [
    refusal(null, REFUSALS.NOT_AN_OBJECT).code,
    refusal(good({ extra: 1 }), REFUSALS.UNKNOWN_FIELD).code,
    refusal(without('id'), REFUSALS.MISSING_FIELD).code,
    refusal(good({ id: 1 }), REFUSALS.WRONG_TYPE).code,
    refusal(good({ id: '' }), REFUSALS.EMPTY_STRING).code,
    refusal(good({ id: 'a.b' }), REFUSALS.BAD_KEY).code,
    refusal(good({ id: 'a'.repeat(ID_MAX + 1) }), REFUSALS.TOO_LONG).code,
    refusal(good({ text: '\u0000' }), REFUSALS.BAD_CHARACTER).code,
    refusal(good({ at: 1.5 }), REFUSALS.BAD_TIMESTAMP).code,
    // P6.3's families. A count bound is not a length bound, a value outside a
    // vocabulary is not a wrong type, a field on the wrong kind of message is
    // not an unknown one, and a shape that could smuggle an argv element is
    // not merely malformed:
    refusal(good({ kind: 'prompt', options: Array(OPTIONS_MAX + 1).fill('x') }), REFUSALS.TOO_MANY).code,
    refusal(good({ kind: 'prompt', options: [] }), REFUSALS.TOO_FEW).code,
    refusal(good({ kind: 'gossip' }), REFUSALS.UNKNOWN_KIND).code,
    refusal(withAction({ verb: 'launch' }), REFUSALS.UNKNOWN_VERB).code,
    refusal(good({ kind: 'message', options: ['one'] }), REFUSALS.FIELD_NOT_ALLOWED).code,
    refusal(withAction({ target: { run: '-x' } }), REFUSALS.BAD_TARGET).code,
    refusal(good({ mac: 'F'.repeat(MAC_MAX) }), REFUSALS.BAD_MAC).code,
    refusal(good({ kind: 'prompt', options: ['one', 'one'] }), REFUSALS.DUPLICATE_ENTRY).code,
    refusal(withAction({ verb: 'merge', head_sha: 'short' }), REFUSALS.BAD_SHA).code,
  ];
  assert.equal(new Set(codes).size, codes.length, `codes are distinct: ${codes.join(', ')}`);
  assert.deepEqual(new Set(codes), new Set(Object.values(REFUSALS)), 'and no code is unused');
  for (const code of codes) {
    assert.equal(typeof code, 'string');
    assert.notEqual(code, '', 'every refusal is named');
  }
});

test('isMessage agrees with validateMessage', () => {
  assert.equal(isMessage(good()), true);
  assert.equal(isMessage(good({ acked: true })), true);
  assert.equal(isMessage(good({ id: '' })), false);
  assert.equal(isMessage(null), false);
  assert.equal(isMessage(good({ extra: 1 })), false);
});

test('the schema is not the redactor: redaction happens before it (P1.3)', () => {
  // P0.2 is on the plan's critical path and is deliberately SMALL: the
  // redactor is P1.3's, and D118 redacts before the write, so by the time a
  // value reaches this schema it has already been through it. This case pins
  // that boundary — the schema neither redacts nor refuses a token-shaped
  // body, it only bounds it — so that a later task cannot quietly grow a
  // second, weaker redactor in here.
  const tokenShaped = ['ghp', 'x'.repeat(36)].join('_'); // built at run time: no secret-shaped literal is committed
  const verdict = validateMessage(good({ text: `the token is ${tokenShaped}` }));
  assert.equal(verdict.ok, true);
  assert.equal(verdict.message.text, `the token is ${tokenShaped}`, 'returned verbatim, unredacted');
});

// ---------------------------------------------------------------------------
// P6.3 — the fields D161, D167, D168, D169 and D171 need, and the MAC.
//
// The MAC is the part that carries the weight, and it is worth saying once
// what it is for. A launcher can mint a custom token for any uid (G13) and can
// read `config/admin/devices` through `firebasedatabase.viewer`, so a minted
// token passes the rules' device-node conjunct and can write into a paired
// account's mailbox. What it does NOT have is the per-pairing key (D171), which
// travels encrypted to a key the phone holds and never through the database —
// so the tag, and only the tag, is what tells a message the phone really spoke
// from one a launcher invented. That is why every inbox message of every kind
// carries one, why an action record's confirmation is inside the tagged bytes
// rather than beside them, and why the cases below are mostly "a valid tag over
// a swapped field must fail".
// ---------------------------------------------------------------------------

test('the kind vocabulary is closed, and an absent kind is the ordinary message', () => {
  assert.deepEqual([...KINDS], ['message', 'prompt', 'answer', 'action']);
  const { message } = validateMessage(good());
  assert.equal(Object.hasOwn(message, 'kind'), false, 'an absent kind adds no field');
  for (const kind of KINDS) {
    const value = good({ kind });
    if (kind === 'answer') value.reply_to = 'm-0000';
    if (kind === 'action') value.action = anAction();
    assert.equal(validateMessage(value).ok, true, `kind ${kind} is accepted`);
  }
  refusal(good({ kind: 'gossip' }), REFUSALS.UNKNOWN_KIND, 'kind');
  refusal(good({ kind: 'Message' }), REFUSALS.UNKNOWN_KIND, 'kind'); // the names are exact
  refusal(good({ kind: 1 }), REFUSALS.WRONG_TYPE, 'kind');
  refusal(good({ kind: undefined }), REFUSALS.WRONG_TYPE, 'kind');
  // `command` is P1.1's own vocabulary for an acceptance WINDOW, not a message
  // kind: P6.5 and P6.7 map a kind to a window, and that mapping is theirs.
  refusal(good({ kind: 'command' }), REFUSALS.UNKNOWN_KIND, 'kind');
});

test('a kind carries its own fields and no others', () => {
  // The field set is the version (P0.2), and with kinds it is the (kind, field)
  // table: a field on the wrong kind of message is refused rather than ignored,
  // because two agents disagreeing about whether a message is an action is the
  // ambiguity the whole module exists to refuse.
  refusal(good({ kind: 'message', options: ['one'] }), REFUSALS.FIELD_NOT_ALLOWED, 'options');
  refusal(good({ kind: 'prompt', reply_to: 'm-0000' }), REFUSALS.FIELD_NOT_ALLOWED, 'reply_to');
  refusal(good({ kind: 'answer', action: anAction() }), REFUSALS.FIELD_NOT_ALLOWED, 'action');
  refusal(good({ kind: 'message', action: anAction() }), REFUSALS.FIELD_NOT_ALLOWED, 'action');
  // An action record IS an action, and D162's launch is not one: "kick off …"
  // goes through without a read-back, so it travels as an ordinary message.
  refusal(good({ kind: 'action' }), REFUSALS.MISSING_FIELD, 'action');
  assert.equal(validateMessage(good({ text: 'kick off the fix' })).ok, true, 'a launch is a plain message');
});

test('an agent is optional, id-shaped, and the same value the heartbeat advertises', () => {
  const verdict = validateMessage(good({ agent: 'orchestrator' }));
  assert.equal(verdict.ok, true);
  assert.equal(verdict.message.agent, 'orchestrator');
  assert.equal(Object.hasOwn(validateMessage(good()).message, 'agent'), false, 'an absent agent adds no field');
  assert.equal(validateMessage(good({ agent: 'a'.repeat(AGENT_MAX) })).ok, true, 'the bound itself');
  refusal(good({ agent: '' }), REFUSALS.EMPTY_STRING, 'agent');
  refusal(good({ agent: 'a'.repeat(AGENT_MAX + 1) }), REFUSALS.TOO_LONG, 'agent');
  refusal(good({ agent: 'two\nlines' }), REFUSALS.BAD_CHARACTER, 'agent');
  refusal(good({ agent: 1 }), REFUSALS.WRONG_TYPE, 'agent');
  // The rule is the heartbeat's agent-id rule, deliberately: "talk to
  // <agent>" matches this value against the advertised list, so a value the
  // list could never carry is not an agent. D161 rejected one mailbox per
  // agent, so nothing is stored under it and it is not a database key either.
  refusal(good({ agent: 'agent@host' }), REFUSALS.BAD_CHARACTER, 'agent');
  refusal(good({ agent: 'agent/one' }), REFUSALS.BAD_CHARACTER, 'agent');
});

test('a prompt carries the options it will read aloud, bounded and unambiguous', () => {
  const prompt = good({ kind: 'prompt', options: ['merge it', 'leave it'] });
  const verdict = validateMessage(prompt);
  assert.equal(verdict.ok, true);
  assert.deepEqual(verdict.message.options, ['merge it', 'leave it']);
  assert.equal(validateMessage(good({ kind: 'prompt' })).ok, true, 'options are optional');
  // Options are answered by position ("option two"), so the list has to be one
  // a person can hold and every entry has to be distinguishable.
  refusal(good({ kind: 'prompt', options: [] }), REFUSALS.TOO_FEW, 'options');
  refusal(
    good({ kind: 'prompt', options: Array.from({ length: OPTIONS_MAX + 1 }, (_, i) => `option ${i}`) }),
    REFUSALS.TOO_MANY,
    'options',
  );
  assert.equal(
    validateMessage(good({ kind: 'prompt', options: Array.from({ length: OPTIONS_MAX }, (_, i) => `o${i}`) })).ok,
    true,
    'the bound itself',
  );
  refusal(
    good({ kind: 'prompt', options: ['merge it', 'merge it'] }),
    REFUSALS.DUPLICATE_ENTRY,
    'options[1]',
  );
  refusal(good({ kind: 'prompt', options: [''] }), REFUSALS.EMPTY_STRING, 'options[0]');
  refusal(good({ kind: 'prompt', options: ['x'.repeat(OPTION_MAX + 1)] }), REFUSALS.TOO_LONG, 'options[0]');
  refusal(good({ kind: 'prompt', options: ['two\nlines'] }), REFUSALS.BAD_CHARACTER, 'options[0]');
  refusal(good({ kind: 'prompt', options: [1] }), REFUSALS.WRONG_TYPE, 'options[0]');
  refusal(good({ kind: 'prompt', options: 'merge it' }), REFUSALS.WRONG_TYPE, 'options');
});

test('an answer names the prompt it answers, and only an answer may', () => {
  const answer = good({ kind: 'answer', reply_to: 'm-0000', text: 'option two' });
  const verdict = validateMessage(answer);
  assert.equal(verdict.ok, true);
  assert.equal(verdict.message.reply_to, 'm-0000');
  refusal(good({ kind: 'answer' }), REFUSALS.MISSING_FIELD, 'reply_to');
  // `reply_to` names a message, and a message is stored under its id, so it is
  // held to the id's own key rule.
  refusal(good({ kind: 'answer', reply_to: 'm/0000' }), REFUSALS.BAD_KEY, 'reply_to');
  refusal(good({ kind: 'answer', reply_to: '' }), REFUSALS.EMPTY_STRING, 'reply_to');
  refusal(good({ kind: 'answer', reply_to: 1 }), REFUSALS.WRONG_TYPE, 'reply_to');
  refusal(good({ kind: 'prompt', reply_to: 'm-0000', options: ['one'] }), REFUSALS.FIELD_NOT_ALLOWED, 'reply_to');
  // An action record answering a destructive prompt says which prompt it
  // answers: the consumer has to know which pending prompt it settles (§3.5).
  const answering = withAction({ verb: 'confirm' });
  answering.reply_to = 'm-0000';
  assert.equal(validateMessage(answering).ok, true, 'an action may answer a prompt');
});

test('there is no expiry field on a prompt', () => {
  // D168 as amended: a prompt expires at D118's mailbox TTL, which the CONSUMER
  // computes from `at` and its own clock. An `expires_at` written by the phone
  // would be the phone's clock deciding — the clock D117 refuses to trust — so
  // the field does not exist and every name for it is an unknown field.
  refusal(good({ kind: 'prompt', expires_at: 1_700_000_000_000 }), REFUSALS.UNKNOWN_FIELD, 'expires_at');
  refusal(good({ kind: 'prompt', expiresAt: 1 }), REFUSALS.UNKNOWN_FIELD, 'expiresAt');
  refusal(good({ kind: 'prompt', ttl: 1 }), REFUSALS.UNKNOWN_FIELD, 'ttl');
  refusal(good({ kind: 'prompt', deadline: 1 }), REFUSALS.UNKNOWN_FIELD, 'deadline');
});

test('an action record is complete or it is refused', () => {
  const verdict = validateMessage(withAction());
  assert.equal(verdict.ok, true);
  assert.deepEqual(verdict.message.action, anAction());
  assert.equal(validateMessage(withAction({ verb: 'merge' })).ok, true);
  assert.equal(validateMessage(withAction({ verb: 'confirm' })).ok, true);
  for (const field of ['id', 'verb', 'target', 'confirmation']) {
    const value = withAction();
    delete value.action[field];
    refusal(value, REFUSALS.MISSING_FIELD, `action.${field}`);
  }
  for (const field of ['id', 'at']) {
    const value = withAction();
    delete value.action.confirmation[field];
    refusal(value, REFUSALS.MISSING_FIELD, `action.confirmation.${field}`);
  }
  // A confirmation is a record, not a flag: an id that can be deduped and the
  // time it was given, which is what the consumer's 6-hour command window is
  // measured from (§3.5).
  refusal(withAction({ confirmation: 'yes' }), REFUSALS.NOT_AN_OBJECT, 'action.confirmation');
  refusal(withAction({ confirmation: { id: 'c-0001', at: 'now' } }), REFUSALS.WRONG_TYPE, 'action.confirmation.at');
  refusal(withAction({ confirmation: { id: 'c-0001', at: 1.5 } }), REFUSALS.BAD_TIMESTAMP, 'action.confirmation.at');
  refusal(withAction({ id: 'a/0001' }), REFUSALS.BAD_KEY, 'action.id');
  refusal(withAction({ confirmation: { id: 'c/0001', at: 0 } }), REFUSALS.BAD_KEY, 'action.confirmation.id');
  refusal(withAction({ extra: 'anything' }), REFUSALS.UNKNOWN_FIELD, 'action.extra');
  refusal(withAction({ action: 1 }), REFUSALS.UNKNOWN_FIELD, 'action.action');
  refusal(good({ kind: 'action', action: null }), REFUSALS.NOT_AN_OBJECT, 'action');
  refusal(good({ kind: 'action', action: [anAction()] }), REFUSALS.NOT_AN_OBJECT, 'action');
});

test('the verbs are the three the ruling names, and nothing else', () => {
  assert.deepEqual([...ACTIONS], ['cancel', 'merge', 'confirm']);
  for (const verb of ACTIONS) {
    assert.equal(validateMessage(withAction({ verb })).ok, true, `${verb} is accepted`);
  }
  // D162: a launch needs no confirmation and is not one of these. Every other
  // destructive verb — the kill switch, budget writes, deletes — stays refused
  // by voice, so it has no verb here either (§3.1).
  for (const verb of ['launch', 'Cancel', 'merge!', 'kill', 'budget', 'delete', 'approve']) {
    refusal(withAction({ verb }), REFUSALS.UNKNOWN_VERB, 'action.verb');
  }
  refusal(withAction({ verb: 1 }), REFUSALS.WRONG_TYPE, 'action.verb');
});

test('a verb and its target have to agree, and the target is one shape or the other', () => {
  // D169: the consumer runs `fugaro cancel RUN` itself and `gh pr merge <n>
  // --repo <owner/name>` itself. The record has to name exactly what will be
  // executed, so cancel takes a run id, merge takes a repository and a pull
  // request, and `confirm` — the third verb — takes either.
  const run = { run: '20261018-125859-d3c8' };
  const repo = { repo: 'dimipaun/fugaro', pr: 532 };
  assert.equal(validateMessage(withAction({ verb: 'cancel', target: run })).ok, true);
  assert.equal(validateMessage(withAction({ verb: 'confirm', target: run })).ok, true);
  assert.equal(validateMessage(withAction({ verb: 'confirm', target: repo })).ok, true);
  refusal(withAction({ verb: 'cancel', target: repo }), REFUSALS.BAD_TARGET, 'action.target');
  refusal(withAction({ verb: 'merge', target: run }), REFUSALS.BAD_TARGET, 'action.target');
  // Exactly one of the two shapes: a target that is both is a target the
  // consumer would have to choose between, which is the guess D169's read-back
  // exists to remove.
  refusal(withAction({ target: { ...run, ...repo } }), REFUSALS.BAD_TARGET, 'action.target');
  refusal(withAction({ target: {} }), REFUSALS.BAD_TARGET, 'action.target');
  refusal(withAction({ target: null }), REFUSALS.NOT_AN_OBJECT, 'action.target');
  refusal(withAction({ target: 'run-1' }), REFUSALS.NOT_AN_OBJECT, 'action.target');
  refusal(withAction({ target: { run: '' } }), REFUSALS.EMPTY_STRING, 'action.target.run');
  refusal(withAction({ target: { run: 1 } }), REFUSALS.WRONG_TYPE, 'action.target.run');
  refusal(withAction({ target: { repo: 'dimipaun/fugaro' } }), REFUSALS.MISSING_FIELD, 'action.target.pr');
  refusal(withAction({ target: { repo: 'dimipaun/fugaro', pr: 0 } }), REFUSALS.BAD_TARGET, 'action.target.pr');
  refusal(withAction({ target: { repo: 'dimipaun/fugaro', pr: 1.5 } }), REFUSALS.BAD_TARGET, 'action.target.pr');
  refusal(withAction({ target: { repo: 'dimipaun/fugaro', pr: '532' } }), REFUSALS.WRONG_TYPE, 'action.target.pr');
  refusal(withAction({ target: { repo: 'fugaro', pr: 1 } }), REFUSALS.BAD_TARGET, 'action.target.repo');
  refusal(withAction({ target: { repo: 'a/b/c', pr: 1 } }), REFUSALS.BAD_TARGET, 'action.target.repo');
  refusal(withAction({ target: { repo: '/fugaro', pr: 1 } }), REFUSALS.BAD_TARGET, 'action.target.repo');
  refusal(withAction({ target: { repo: 'dimipaun/fugaro', pr: 1, extra: 1 } }), REFUSALS.UNKNOWN_FIELD, 'action.target.extra');
});

test('no target can be read as a command-line flag', () => {
  // The consumer passes the run id to `fugaro cancel` and the repository to
  // `gh pr merge --repo` as ARGV elements. A value that begins with `-` is a
  // flag to every parser that sees it, and a value carrying a space, a slash or
  // a control character is not a run id at all; both are refused here rather
  // than left for the consumer's argv to decide.
  // A leading `-` is a flag and a leading `.` is a path, so both are the
  // SHAPE's fault; a character outside the alphabet is the CHARACTER's fault,
  // the same family a control character in a body belongs to. Two families,
  // two codes, because a caller that cannot tell them apart cannot say which
  // of its own inputs to fix.
  for (const run of ['-x', '--repo', '-', '.', '..', '-r']) {
    refusal(withAction({ target: { run } }), REFUSALS.BAD_TARGET, 'action.target.run');
  }
  for (const run of ['run one', 'run/one', './run', 'run\none', 'run\u0000', 'run"one']) {
    refusal(withAction({ target: { run } }), REFUSALS.BAD_CHARACTER, 'action.target.run');
  }
  for (const repo of ['-x/y', 'a/-y', '../fugaro', 'fugaro', 'a/b/c', '/fugaro', 'a//b']) {
    refusal(withAction({ target: { repo, pr: 1 } }), REFUSALS.BAD_TARGET, 'action.target.repo');
  }
  for (const repo of ['a b/c', 'a/b ', 'a/b\nc']) {
    refusal(withAction({ target: { repo, pr: 1 } }), REFUSALS.BAD_CHARACTER, 'action.target.repo');
  }
  refusal(withAction({ target: { repo: '', pr: 1 } }), REFUSALS.EMPTY_STRING, 'action.target.repo');
  // The near misses that are ordinary repositories and runs: the rule is a
  // shape rule, not a "reject anything unusual" rule.
  for (const target of [
    { run: '20261018-125859-d3c8' },
    { run: 'r.1_2:3' },
    { repo: 'dimipaun/fugaro', pr: 1 },
    { repo: 'owner.name/repo-name_x', pr: 99999 },
  ]) {
    assert.equal(validateMessage(withAction({ verb: 'confirm', target })).ok, true, JSON.stringify(target));
  }
  // A run id and a repository are bounded like every other registry string.
  refusal(withAction({ target: { run: 'r'.repeat(TARGET_MAX + 1) } }), REFUSALS.TOO_LONG, 'action.target.run');
  refusal(
    withAction({ target: { repo: `${'o'.repeat(TARGET_MAX)}/n`, pr: 1 } }),
    REFUSALS.TOO_LONG,
    'action.target.repo',
  );
});

test('a merge carries the head SHA it was confirmed against, and only a merge', () => {
  // §3.5 and D169: the read-back names the PR's head at confirmation time, and
  // the consumer refuses the merge if the head has moved. A record without one
  // is a record that cannot be checked, and a SHA on a cancel is a field the
  // record has no business carrying.
  assert.equal(validateMessage(withAction({ verb: 'merge', head_sha: 'b'.repeat(64) })).ok, true, 'SHA-256');
  assert.equal(validateMessage(withAction({ verb: 'merge', head_sha: 'b'.repeat(40) })).ok, true, 'SHA-1');
  const noSha = withAction({ verb: 'merge' });
  delete noSha.action.head_sha;
  refusal(noSha, REFUSALS.MISSING_FIELD, 'action.head_sha');
  refusal(withAction({ verb: 'cancel', head_sha: 'b'.repeat(40) }), REFUSALS.FIELD_NOT_ALLOWED, 'action.head_sha');
  refusal(withAction({ verb: 'confirm', head_sha: 'b'.repeat(40) }), REFUSALS.FIELD_NOT_ALLOWED, 'action.head_sha');
  for (const head_sha of ['', 'b'.repeat(39), 'b'.repeat(41), 'B'.repeat(40), 'z'.repeat(40), 1, null]) {
    refusal(withAction({ verb: 'merge', head_sha }), REFUSALS.BAD_SHA, 'action.head_sha');
  }
});

test('a mac is required on every message, and it is a SHA-256 tag in lowercase hex', () => {
  const verdict = validateMessage(good());
  assert.equal(verdict.ok, true);
  assert.equal(verdict.message.mac, FAKE_MAC);
  refusal(without('mac'), REFUSALS.MISSING_FIELD, 'mac');
  refusal(good({ mac: '' }), REFUSALS.EMPTY_STRING, 'mac');
  refusal(good({ mac: 1 }), REFUSALS.WRONG_TYPE, 'mac');
  refusal(good({ mac: undefined }), REFUSALS.WRONG_TYPE, 'mac');
  // Length and character set are one question here — is this a 32-byte tag? —
  // which is why they share one code rather than splitting into too-long and
  // bad-character: a tag of the wrong length is not a tag at all.
  for (const mac of ['a'.repeat(MAC_MAX - 1), 'a'.repeat(MAC_MAX + 1), 'A'.repeat(MAC_MAX), 'g'.repeat(MAC_MAX), ` ${'a'.repeat(MAC_MAX - 1)}`]) {
    refusal(good({ mac }), REFUSALS.BAD_MAC, 'mac');
  }
  // The canonical case matters: hex has two spellings for one tag, so accepting
  // both would let one message verify in two encodings and would make the
  // consumer's comparison depend on which end wrote it.
  refusal(good({ mac: FAKE_MAC.toUpperCase() }), REFUSALS.BAD_MAC, 'mac');
  // The heartbeat's tag is required in the same way (O7), and an absent one is
  // a MISSING field rather than a malformed one: a heartbeat with no tag at all
  // is the shape an unsigned impostor heartbeat has, and the phone has to be
  // able to tell it from one whose tag is merely wrong.
  const unsigned = aHeartbeat();
  delete unsigned.mac;
  heartbeatRefusal(unsigned, REFUSALS.MISSING_FIELD, 'mac');
  heartbeatRefusal(aHeartbeat({ mac: '' }), REFUSALS.EMPTY_STRING, 'mac');
  heartbeatRefusal(aHeartbeat({ mac: 'nope' }), REFUSALS.BAD_MAC, 'mac');
});

test('a character two encoders disagree about is refused, so one message has one canonical form', () => {
  // Go's encoding/json escapes U+2028 and U+2029 even with SetEscapeHTML(false)
  // set; JavaScript's JSON.stringify leaves them as themselves. A message
  // carrying one therefore has TWO canonical byte strings, one per language, and
  // a tag computed over the phone's does not verify against the consumer's —
  // a refusal nobody could diagnose, for a character nobody can speak. The
  // schema refuses it here, where the refusal can name itself.
  for (const char of [' ', ' ']) {
    refusal(good({ text: `x${char}y` }), REFUSALS.BAD_CHARACTER, 'text');
    refusal(good({ sender: `x${char}` }), REFUSALS.BAD_CHARACTER, 'sender');
    refusal(good({ agent: `x${char}` }), REFUSALS.BAD_CHARACTER, 'agent');
    refusal(good({ id: `x${char}` }), REFUSALS.BAD_KEY, 'id');
    refusal(good({ kind: 'prompt', options: [`x${char}`] }), REFUSALS.BAD_CHARACTER, 'options[0]');
  }
  // And the characters JSON escapes differently from Go's DEFAULT encoder are
  // still characters: `<`, `>` and `&` are ordinary, and the Go consumer is the
  // one that has to set SetEscapeHTML(false) — the vectors file pins the bytes
  // it must produce.
  assert.equal(validateMessage(good({ text: 'behind <b> & "quoted"' })).ok, true);
});

test('canonicalBytes is the one serialisation the tag covers', () => {
  // Sorted by code point, no insignificant whitespace, absent fields omitted,
  // and `mac` and `acked` out (§3.5). The exact bytes are pinned for the three
  // shapes the two ends share, because this string IS the protocol: the Go
  // consumer reimplements it, and one stray space is a MAC that does not verify.
  assert.equal(
    canonicalBytes(good(), 'inbox'),
    '{"at":1700000000000,"direction":"inbox","id":"m-0001","sender":"phone","text":"run 42 finished"}',
  );
  assert.equal(
    canonicalBytes(good({ mac: 'a'.repeat(MAC_MAX), acked: true }), 'inbox'),
    canonicalBytes(good(), 'inbox'),
    'neither the tag nor the ack changes the bytes',
  );
  // Absent fields are omitted, never null; present ones are included whether or
  // not this module happens to know them, because §3.5 covers "a field added
  // later" by default.
  assert.equal(canonicalBytes(good({ kind: 'prompt' }), 'inbox'), '{"at":1700000000000,"direction":"inbox","id":"m-0001","kind":"prompt","sender":"phone","text":"run 42 finished"}');
  // Nested objects are sorted too, at every depth: the action record, its
  // target and its confirmation are inside the tagged bytes (§3.5).
  assert.equal(
    canonicalBytes(withAction({ verb: 'merge' }), 'inbox'),
    '{"action":{"confirmation":{"at":1700000000000,"id":"c-0001"},"head_sha":"' +
      'a'.repeat(40) +
      '","id":"a-0001","target":{"pr":532,"repo":"dimipaun/fugaro"},"verb":"merge"},' +
      '"agent":"orchestrator","at":1700000000000,"direction":"inbox","id":"m-0001","kind":"action",' +
      '"sender":"phone","text":"cancel the run"}',
  );
  assert.equal(
    canonicalBytes(good({ kind: 'prompt', options: ['merge it', 'leave it'] }), 'inbox'),
    '{"at":1700000000000,"direction":"inbox","id":"m-0001","kind":"prompt","options":["merge it","leave it"],' +
      '"sender":"phone","text":"run 42 finished"}',
  );
  // An array keeps its order: an option's position is what "option two" means.
  assert.equal(
    canonicalBytes(good({ kind: 'prompt', options: ['b', 'a'] }), 'inbox'),
    '{"at":1700000000000,"direction":"inbox","id":"m-0001","kind":"prompt","options":["b","a"],' +
      '"sender":"phone","text":"run 42 finished"}',
  );
  // The key order of the value handed in is not part of the message.
  const reordered = { text: 'run 42 finished', sender: 'phone', id: 'm-0001', at: 1_700_000_000_000, mac: FAKE_MAC };
  assert.equal(canonicalBytes(reordered, 'inbox'), canonicalBytes(good(), 'inbox'));
  // Strings are UTF-8 with JSON's escaping only: an emoji stays itself.
  assert.equal(canonicalBytes(good({ text: 'x😀y' }), 'inbox'), '{"at":1700000000000,"direction":"inbox","id":"m-0001","sender":"phone","text":"x😀y"}');
});

test('canonicalBytes refuses a value JSON cannot carry, rather than dropping it', () => {
  // A dropped field is two different messages with one canonical form, and one
  // canonical form with one tag is exactly the ambiguity the tag exists to
  // remove. The schema never produces these values (an explicit undefined is
  // `wrong-type`, and a null is never written), so failing loudly here cannot
  // surprise a caller that validated first.
  assert.throws(() => canonicalBytes({ ...good(), extra: undefined }, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes({ ...good(), extra: null }, 'inbox'), TypeError);
  // And it says WHICH failure it is, not merely that something failed: an
  // absent-or-null field has no canonical form because omitting is not writing
  // null, and that reason is the module's own. (The reason is asserted because
  // it is also what makes the guard a guard: `null` and `undefined` would throw
  // a TypeError from somewhere else entirely if this branch were dropped, and
  // the difference between the two — one says why, the other accuses the
  // caller's object of not being an object — is the whole of the refusal.)
  assert.throws(() => canonicalBytes({ ...good(), extra: null }, 'inbox'), /absent fields are omitted/);
  assert.throws(() => canonicalBytes({ ...good(), extra: undefined }, 'inbox'), /absent fields are omitted/);
  assert.throws(() => canonicalBytes(good({ at: undefined }), 'inbox'), TypeError);
  assert.throws(() => canonicalBytes(null, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes('a message', 'inbox'), TypeError);
  assert.throws(() => canonicalBytes([], 'inbox'), TypeError);
  // And a number that is not an integer, for the reason that has nothing to do
  // with this module's taste: Go prints 1e-07 where JavaScript prints 1e-7
  // for the same double, so a float inside a signed record is a tag that
  // verifies on one end and not the other. Every number this schema accepts is
  // an integer (`at`, a confirmation's `at`, a pull request number), so nothing
  // valid is refused here.
  assert.throws(() => canonicalBytes(good({ at: 1.5 }), 'inbox'), TypeError);
  assert.throws(() => canonicalBytes(good({ at: Number.NaN }), 'inbox'), TypeError);
  assert.throws(() => canonicalBytes(good({ at: Number.POSITIVE_INFINITY }), 'inbox'), TypeError);
  // A nested one too: the walk is at every depth.
  assert.throws(() => canonicalBytes(withAction({ target: { repo: 'a/b', pr: 1.5 } }), 'inbox'), TypeError);
});

test('keys are ordered by code point, which is not UTF-16 code-unit order', () => {
  // §3.5 says "keys sorted by code point at every depth", and the Go consumer
  // sorts map keys bytewise, which for UTF-8 is the same order. JavaScript's own
  // `.sort()` is UTF-16 CODE UNIT order, and the two disagree for exactly one
  // pair of ranges: an astral key — first code unit D800..DBFF — sorts BEFORE a
  // key in U+E000..U+FFFF under `.sort()` and AFTER it by code point, so the two
  // ends would emit the keys of one record in opposite orders and compute two
  // different tags. Nothing validateMessage accepts can reach this (every field
  // name is ASCII), but canonicalBytes is exported and its whole contract is
  // that both languages emit the same bytes, so the order is pinned here and in
  // the vectors rather than left to the reader.
  const astral = '😀'; // U+1F600: encoded as the surrogate pair D83D DE00
  const bmp = '￿'; // the last BMP code point: U+FFFF < U+1F600, but 0xFFFF > 0xD83D
  assert.equal(canonicalBytes({ [astral]: 1, [bmp]: 2 }, 'inbox'), `{"direction":"inbox","${bmp}":2,"${astral}":1}`);
  assert.equal(
    canonicalBytes({ [bmp]: 2, [astral]: 1 }, 'inbox'),
    `{"direction":"inbox","${bmp}":2,"${astral}":1}`,
    'the caller\'s order is not the message\'s',
  );
  // A non-ASCII key that is not astral is unaffected: for the BMP, code-unit
  // order IS code-point order, which is why the hazard is this narrow.
  assert.equal(canonicalBytes({ é: 1, a: 2 }, 'inbox'), '{"a":2,"direction":"inbox","é":1}');
});

test('a string the two encoders spell differently has no canonical form', () => {
  // The same class of hazard as the U+2028 refusal above, one level down.
  // canonicalBytes is exported and is a general serialisation, so it cannot lean
  // on validateMessage having refused these values first: Go's encoder writes
  // U+FFFD for a lone surrogate and escapes U+2028/U+2029, while JavaScript
  // writes "\ud800" and leaves the separators as themselves. Two spellings, one
  // record, no canonical form — so the call throws, at every depth, for a key
  // exactly as for a value.
  for (const key of ['x\ud800y', ' ', ' ']) {
    assert.throws(() => canonicalBytes({ ...good(), [key]: 1 }, 'inbox'), TypeError, `key ${JSON.stringify(key)}`);
  }
  for (const value of ['x\ud800y', 'x y', 'x y']) {
    assert.throws(() => canonicalBytes(good({ text: value }), 'inbox'), TypeError, `text ${JSON.stringify(value)}`);
  }
  assert.throws(
    () => canonicalBytes(withAction({ target: { repo: 'a/\ud800', pr: 1 } }), 'inbox'),
    TypeError,
    'the walk is at every depth',
  );
  // And what it does NOT refuse: an astral character, a non-ASCII letter and the
  // characters JSON escapes identically to Go all have one canonical form.
  assert.equal(canonicalBytes(good({ text: 'x😀é y' }), 'inbox'), '{"at":1700000000000,"direction":"inbox","id":"m-0001","sender":"phone","text":"x😀é y"}');
});

test('the entry point imports nothing, so the phone can load it', () => {
  // React Native is one of this package's two designated consumers (D119), and
  // its bundler resolves no `node:` builtin while its engine has no buffer type.
  // So a static import of Node's crypto — or a bare use of that buffer type —
  // anywhere in this file would not merely cost the phone the tag: it would make
  // the WHOLE schema unimportable there, validateMessage and canonicalBytes
  // included, which are pure functions over values and the parts the phone needs
  // most. The tag therefore lives in ./mac.js, which imports Node's crypto, and
  // which this file never imports; the phone implements HMAC-SHA-256 itself and
  // tests it against vectors.json. This is the one property of the module that a
  // bundler, rather than the test runner, would otherwise be the first to find.
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');
  assert.equal(/^\s*import\b/m.test(source), false, 'no import statement in index.js');
  assert.equal(/\bfrom\s+['"]/.test(source), false, 'nothing is imported into index.js');
  assert.equal(/\bBuffer\b/.test(source), false, 'no Node buffer type in index.js');
});

// TEST_KEY is an obviously fake key, built here rather than committed: every
// value this module signs in a test is signed with a placeholder, and no
// secret-shaped literal is written into this file or the vectors file.
const TEST_KEY = ['not', 'a', 'secret', 'test', 'key', '0001'].join('-');

test('a tag verifies over exactly the covered fields, and a swap fails', () => {
  const message = good();
  const mac = macOf(message, TEST_KEY, 'inbox');
  assert.equal(mac, macOf(good(), TEST_KEY, 'inbox'), 'the tag is a function of the value alone');
  assert.equal(verifyMac({ ...message, mac }, TEST_KEY, 'inbox'), true);
  assert.equal(verifyMac({ ...message, mac }, `${TEST_KEY}-other`, 'inbox'), false, 'a different key does not verify');
  assert.equal(verifyMac({ ...message, mac }, 'not-a-secret-test-key-0002', 'inbox'), false);
  assert.equal(verifyMac(message, TEST_KEY, 'inbox'), false, 'an absent tag does not verify');
  assert.equal(verifyMac(null, TEST_KEY, 'inbox'), false);
  assert.equal(verifyMac({ ...message, mac: 'nope' }, TEST_KEY, 'inbox'), false, 'a malformed tag does not verify');
  // The ack is written by the phone AFTER the tag, so it is outside the covered
  // fields — and a message with one still verifies (§3.5).
  assert.equal(verifyMac({ ...message, acked: true, mac }, TEST_KEY, 'inbox'), true, 'the ack is outside the bytes');

  // Every covered field, one at a time. These are the swaps a launcher who
  // holds a minted token but not the key would try.
  const swaps = [
    { ...message, id: 'm-0002', mac },
    { ...message, sender: 'someone-else', mac },
    { ...message, text: 'merge the pull request', mac },
    { ...message, at: message.at + 1, mac },
    { ...message, agent: 'other-agent', mac },
    { ...message, kind: 'prompt', mac },
    { ...good({ kind: 'prompt', options: ['merge it'] }), mac },
    { ...good({ kind: 'prompt', options: ['merge it', 'leave it'] }), mac: macOf(good({ kind: 'prompt', options: ['merge it'] }), TEST_KEY, 'inbox') },
    { ...good({ kind: 'answer', reply_to: 'm-0000' }), mac: macOf(good({ kind: 'answer', reply_to: 'm-0001' }), TEST_KEY, 'inbox') },
    { ...good({ kind: 'answer', reply_to: 'm-0000', text: 'option one' }), mac: macOf(good({ kind: 'answer', reply_to: 'm-0000', text: 'option two' }), TEST_KEY, 'inbox') },
    { ...withAction({ verb: 'merge' }), mac: macOf(withAction({ verb: 'merge', target: { repo: 'dimipaun/fugaro', pr: 533 } }), TEST_KEY, 'inbox') },
    { ...withAction({ verb: 'merge' }), mac: macOf(withAction({ verb: 'merge', head_sha: 'b'.repeat(40) }), TEST_KEY, 'inbox') },
    { ...withAction(), mac: macOf(withAction({ verb: 'merge' }), TEST_KEY, 'inbox') },
  ];
  for (const swapped of swaps) {
    assert.equal(verifyMac(swapped, TEST_KEY, 'inbox'), false, `swapped: ${canonicalBytes(swapped, 'inbox')}`);
  }
});

test('a heartbeat is a different record: its own fields, and a tag over all of them', () => {
  const verdict = validateHeartbeat(aHeartbeat());
  assert.equal(verdict.ok, true);
  assert.deepEqual(verdict.heartbeat, aHeartbeat());
  assert.equal(Object.isFrozen(verdict.heartbeat), true, 'a validated heartbeat is frozen');
  assert.equal(verdict.heartbeat.agents[0].name, 'Orchestrator');
  // The list is optional — a consumer may advertise nothing — and an empty list
  // is refused as a claim that says nothing, exactly as an empty options list is.
  const bare = aHeartbeat();
  delete bare.agents;
  assert.equal(validateHeartbeat(bare).ok, true, 'no list at all is not an empty list');
  assert.equal(Object.hasOwn(validateHeartbeat(bare).heartbeat, 'agents'), false);
  heartbeatRefusal(aHeartbeat({ agents: [] }), REFUSALS.TOO_FEW, 'agents');
  heartbeatRefusal(
    aHeartbeat({ agents: Array.from({ length: AGENTS_MAX + 1 }, (_, i) => ({ id: `a${i}`, name: `Agent ${i}` })) }),
    REFUSALS.TOO_MANY,
    'agents',
  );
  // Two agents under one id make "talk to <agent>" ambiguous (D163 picks the
  // first advertised agent by that name).
  heartbeatRefusal(
    aHeartbeat({ agents: [{ id: 'a', name: 'One' }, { id: 'a', name: 'Two' }] }),
    REFUSALS.DUPLICATE_ENTRY,
    'agents[1].id',
  );
  heartbeatRefusal(aHeartbeat({ agents: [{ id: 'orchestrator' }] }), REFUSALS.MISSING_FIELD, 'agents[0].name');
  heartbeatRefusal(aHeartbeat({ agents: [{ name: 'Orchestrator' }] }), REFUSALS.MISSING_FIELD, 'agents[0].id');
  heartbeatRefusal(aHeartbeat({ agents: ['orchestrator'] }), REFUSALS.NOT_AN_OBJECT, 'agents[0]');
  heartbeatRefusal(aHeartbeat({ agents: [{ id: 'a/b', name: 'One' }] }), REFUSALS.BAD_CHARACTER, 'agents[0].id');
  heartbeatRefusal(aHeartbeat({ agents: [{ id: 'a', name: '' }] }), REFUSALS.EMPTY_STRING, 'agents[0].name');
  // An advertised agent is exactly its id and its spoken name: a field the
  // phone would silently ignore is one an impostor could hide a claim in.
  heartbeatRefusal(
    aHeartbeat({ agents: [{ id: 'a', name: 'One', role: 'root' }] }),
    REFUSALS.UNKNOWN_FIELD,
    'agents[0].role',
  );
  heartbeatRefusal(
    aHeartbeat({ agents: [{ id: 'a', name: 'n'.repeat(AGENT_NAME_MAX + 1) }] }),
    REFUSALS.TOO_LONG,
    'agents[0].name',
  );
  // The boot id is D117's: a short identity string, and the same shape as an
  // agent id (shared/presence carries the same rule for its own input).
  heartbeatRefusal(aHeartbeat({ boot: '' }), REFUSALS.EMPTY_STRING, 'boot');
  heartbeatRefusal(aHeartbeat({ boot: 'b'.repeat(65) }), REFUSALS.TOO_LONG, 'boot');
  heartbeatRefusal(aHeartbeat({ boot: 'boot one' }), REFUSALS.BAD_CHARACTER, 'boot');
  heartbeatRefusal(aHeartbeat({ boot: 1 }), REFUSALS.WRONG_TYPE, 'boot');
  heartbeatRefusal(aHeartbeat({ at: 1.5 }), REFUSALS.BAD_TIMESTAMP, 'at');
  heartbeatRefusal(aHeartbeat({ extra: 1 }), REFUSALS.UNKNOWN_FIELD, 'extra');
  // A heartbeat is not a message and a message is not a heartbeat: the two
  // records have different field sets on purpose.
  heartbeatRefusal(good(), REFUSALS.UNKNOWN_FIELD, 'id');
  assert.equal(validateHeartbeat(null).ok, false);
  assert.equal(validateHeartbeat([]).ok, false);
});

test('the heartbeat\'s tag covers at, boot and agents — the agent list feeds D163', () => {
  const heartbeat = aHeartbeat();
  const mac = macOf(heartbeat, TEST_KEY, 'heartbeat');
  assert.equal(verifyMac({ ...heartbeat, mac }, TEST_KEY, 'heartbeat'), true);
  assert.equal(
    canonicalBytes(heartbeat, 'heartbeat'),
    '{"agents":[{"id":"orchestrator","name":"Orchestrator"}],"at":1700000000000,"boot":"boot-01","direction":"heartbeat"}',
  );
  // An unsigned heartbeat is one the phone must treat as offline, and its agent
  // list is one it must ignore: without the tag, any minted `mc` token could
  // fake liveness and advertise itself as the agent D163 defaults to (§3.5, O7).
  const changed = [
    { ...heartbeat, at: heartbeat.at + 1, mac },
    { ...heartbeat, boot: 'boot-02', mac },
    { ...heartbeat, agents: [{ id: 'orchestrator', name: 'Almost' }], mac },
    { ...heartbeat, agents: [{ id: 'impostor', name: 'Orchestrator' }], mac },
    { ...heartbeat, agents: [], mac },
    { ...heartbeat, agents: [{ id: 'orchestrator', name: 'Orchestrator' }, { id: 'other', name: 'Other' }], mac },
  ];
  for (const swapped of changed) {
    assert.equal(verifyMac(swapped, TEST_KEY, 'heartbeat'), false, `swapped: ${canonicalBytes(swapped, 'heartbeat')}`);
  }
  assert.equal(verifyMac(heartbeat, TEST_KEY, 'heartbeat'), false, 'unsigned');
});

test('the vectors file is the contract both ends test against', () => {
  const vectors = JSON.parse(readFileSync(new URL('./vectors.json', import.meta.url), 'utf8'));
  assert.equal(typeof vectors.key, 'string', 'the vectors carry the fake test key');
  assert.ok(vectors.cases.length >= 8, `the vectors carry the cases, not a token one`);
  for (const c of vectors.cases) {
    assert.equal(typeof c.name, 'string');
    assert.equal(typeof c.why, 'string', `${c.name}: every case says what it pins`);
    assert.equal(canonicalBytes(c.message, c.direction), c.canonical, `${c.name}: the canonical bytes`);
    assert.equal(macOf(c.message, vectors.key, c.direction), c.mac, `${c.name}: the tag`);
    assert.equal(verifyMac(c.message, vectors.key, c.direction), c.verdict === 'verified', `${c.name}: the verdict`);
    // A refused case is refused because the message no longer matches the tag
    // it carries, which is the only thing a MAC can refuse.
    if (c.verdict === 'refused') {
      assert.notEqual(c.message.mac, c.mac, `${c.name}: the carried tag is the untampered one`);
    }
  }
  // The cases §3.5 and P6.3 name by hand, so that a later edit cannot quietly
  // drop one of them.
  const names = vectors.cases.map((c) => c.name).join(' | ');
  for (const required of ['text swapped', 'options swapped', 'reply_to swapped', 'answer swapped', 'agents changed']) {
    assert.ok(names.includes(required), `the vectors include "${required}": ${names}`);
  }
});

test('the vectors carry no secret-shaped literal', () => {
  // The brief for this task is explicit, and it is worth a test rather than a
  // promise: nothing that looks like a credential is committed — not in the
  // vectors, not here. The key in the file is a readable placeholder, and it is
  // asserted to be one, so a real key pasted in "just to debug" fails the suite.
  const text = readFileSync(new URL('./vectors.json', import.meta.url), 'utf8');
  const shapes = [
    /ghp_[A-Za-z0-9]{16}/,
    /github_pat_[A-Za-z0-9_]{16}/,
    /AIza[A-Za-z0-9_-]{16}/,
    /sk-[A-Za-z0-9]{16}/,
    /-----BEGIN/,
    /AKIA[A-Z0-9]{12}/,
    /xox[bp]-[A-Za-z0-9-]{10}/,
    /eyJ[A-Za-z0-9_-]{20}/,
  ];
  for (const shape of shapes) {
    assert.equal(shape.test(text), false, `the vectors carry no ${shape}`);
  }
  const vectors = JSON.parse(text);
  assert.match(vectors.key, /not-a-secret|fake|test-key|placeholder/i, 'the key is an obvious placeholder');
  assert.ok(vectors.key.length <= 64, 'and it is plainly not a generated key');
});

// ---------------------------------------------------------------------------
// Independent review of P6.3 (2026-10-18): the cases below pin defects the
// review found in canonicalBytes and the timestamp rule, and the properties the
// brief asks to be pinned by name (every field covered or explicitly excluded,
// the tag required on every kind, the vectors carrying schema refusals).
// ---------------------------------------------------------------------------

test('canonicalBytes covers an own __proto__ key instead of losing it to a prototype', () => {
  // JSON.parse makes `__proto__` an OWN property. Assigning it onto an ordinary
  // object sets the prototype instead, so a serialiser that rebuilds the record
  // that way drops the field from the bytes: two different records, one tag.
  // validateMessage refuses the field, but canonicalBytes and verifyMac are
  // exported and are called on records as they arrived.
  const raw = JSON.parse(
    '{"id":"m-0001","sender":"phone","text":"run 42 finished","at":1700000000000,"__proto__":{"x":1}}',
  );
  assert.equal(
    canonicalBytes(raw, 'inbox'),
    '{"__proto__":{"x":1},"at":1700000000000,"direction":"inbox","id":"m-0001","sender":"phone","text":"run 42 finished"}',
  );
  assert.notEqual(canonicalBytes(raw, 'inbox'), canonicalBytes(good(), 'inbox'));
  const mac = macOf(good(), TEST_KEY, 'inbox');
  assert.equal(verifyMac({ ...raw, mac }, TEST_KEY, 'inbox'), false, 'a smuggled __proto__ is inside the bytes');
});

test('integer-like keys are ordered by code point too, not by JavaScript\'s index order', () => {
  // A JavaScript object enumerates array-index keys ("9", "10") numerically and
  // BEFORE every other key, whatever order they were inserted in, so an object
  // rebuilt in sorted order and handed to JSON.stringify does not keep the sort.
  // Go sorts map keys bytewise: "10" before "9". One record, two byte strings.
  assert.equal(canonicalBytes({ 10: 1, 9: 2, a: 3 }, 'inbox'), '{"10":1,"9":2,"a":3,"direction":"inbox"}');
  assert.equal(canonicalBytes({ b: { 2: 'x', 10: 'y', '': 'z' } }, 'inbox'), '{"b":{"":"z","10":"y","2":"x"},"direction":"inbox"}');
});

test('negative zero, and an integer the two ends spell differently, have no canonical form', () => {
  // -0 passes Number.isSafeInteger and `>= 0`, so the schema accepts it as an
  // `at` (P0.2's contract, which P1.1 relies on: it refuses -0 itself with its
  // own `negative-zero` code). But JSON.stringify writes it as "0", so a record
  // carrying -0 would be signed as a value it is not: canonicalBytes refuses
  // it, and so a -0 record can be neither tagged nor verified. It refuses every
  // integer outside the exact-double range too, which JavaScript prints in
  // exponent form ("1e+21") and Go's integer types print in full or cannot hold.
  assert.equal(validateMessage(good({ at: -0 })).ok, true, 'the schema\'s contract is unchanged');
  assert.throws(() => macOf(good({ at: -0 }), TEST_KEY, 'inbox'), TypeError);
  assert.equal(verifyMac({ ...good({ at: -0 }), mac: macOf(good({ at: 0 }), TEST_KEY, 'inbox') }, TEST_KEY, 'inbox'), false);
  assert.throws(() => canonicalBytes({ ...good(), at: -0 }, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes({ ...good(), at: 2 ** 53 }, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes({ ...good(), at: 1e21 }, 'inbox'), TypeError);
  assert.equal(canonicalBytes({ n: Number.MAX_SAFE_INTEGER }, 'inbox'), `{"direction":"inbox","n":${Number.MAX_SAFE_INTEGER}}`);
  assert.equal(canonicalBytes({ n: -1 }, 'inbox'), '{"direction":"inbox","n":-1}', 'a negative integer is a canonical number');
});

test('canonicalBytes refuses a value that is not plain JSON data, rather than guessing at it', () => {
  // A Date, a Map or a class instance has no fields of its own to sort, and
  // JSON.stringify would ask it (toJSON) what it wants to be: a record whose
  // bytes depend on a method is not a canonical record.
  assert.throws(() => canonicalBytes({ ...good(), when: new Date(0) }, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes({ ...good(), m: new Map() }, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes({ ...good(), n: 1n }, 'inbox'), TypeError);
  // A null-prototype object is plain data, which is what a hardened parser gives.
  const bare = Object.assign(Object.create(null), { b: 1, a: 2 });
  assert.equal(canonicalBytes(bare, 'inbox'), '{"a":2,"b":1,"direction":"inbox"}');
  assert.equal(canonicalBytes({ t: true, f: false }, 'inbox'), '{"direction":"inbox","f":false,"t":true}');
});

test('every field the schema names is covered by the tag, or excluded by name', () => {
  // §3.5: "every field of the message except mac itself", acked excluded. A
  // field added to FIELDS must either appear in the canonical bytes of a
  // maximal message or be added to this list on purpose; the two fixtures below
  // must between them carry every field, so a new field also fails here until a
  // fixture carries it.
  const EXCLUDED = ['mac', 'acked'];
  const maximal = [
    good({ kind: 'prompt', agent: 'orchestrator', options: ['merge it', 'leave it'], acked: true }),
    { ...withAction({ verb: 'merge' }), reply_to: 'm-0000', acked: false },
  ];
  for (const m of maximal) assert.equal(validateMessage(m).ok, true, JSON.stringify(m));
  const carried = new Set(maximal.flatMap((m) => Object.keys(m)));
  for (const field of FIELDS) {
    assert.ok(carried.has(field), `a maximal fixture carries ${field}`);
  }
  const covered = new Set(maximal.flatMap((m) => Object.keys(JSON.parse(canonicalBytes(m, 'inbox')))));
  for (const field of FIELDS) {
    assert.equal(covered.has(field), !EXCLUDED.includes(field), `${field} is ${EXCLUDED.includes(field) ? 'excluded' : 'covered'}`);
  }
  // And a field no version of the schema names is covered by default.
  assert.match(canonicalBytes({ ...good(), later: 'x' }, 'inbox'), /"later":"x"/);
});

test('the tag is required on every kind, and a wrong-length one is refused on every kind', () => {
  const kinds = {
    message: good({ kind: 'message' }),
    prompt: good({ kind: 'prompt', options: ['merge it'] }),
    answer: good({ kind: 'answer', reply_to: 'm-0000' }),
    action: withAction({ verb: 'merge' }),
    absent: good(),
  };
  for (const [name, m] of Object.entries(kinds)) {
    assert.equal(validateMessage(m).ok, true, `${name} is well formed`);
    const unsigned = { ...m };
    delete unsigned.mac;
    refusal(unsigned, REFUSALS.MISSING_FIELD, 'mac');
    refusal({ ...m, mac: 'f'.repeat(MAC_MAX - 1) }, REFUSALS.BAD_MAC, 'mac');
    refusal({ ...m, mac: 'f'.repeat(MAC_MAX + 1) }, REFUSALS.BAD_MAC, 'mac');
    refusal({ ...m, mac: 'f'.repeat(40) }, REFUSALS.BAD_MAC, 'mac');
    refusal({ ...m, mac: null }, REFUSALS.WRONG_TYPE, 'mac');
  }
});

test('the vectors carry the schema refusals both ends must agree on', () => {
  const vectors = JSON.parse(readFileSync(new URL('./vectors.json', import.meta.url), 'utf8'));
  assert.ok(Array.isArray(vectors.refusals), 'the vectors have a refusals section');
  for (const c of vectors.refusals) {
    assert.equal(typeof c.name, 'string');
    assert.equal(typeof c.why, 'string', `${c.name}: every refusal says what it pins`);
    assert.ok(c.record === 'message' || c.record === 'heartbeat', `${c.name}: names its record`);
    const verdict = c.record === 'message' ? validateMessage(c.value) : validateHeartbeat(c.value);
    assert.equal(verdict.ok, false, `${c.name}: refused`);
    assert.equal(verdict.code, c.code, `${c.name}: the code`);
    assert.equal(verdict.field, c.field, `${c.name}: the field`);
  }
  const names = vectors.refusals.map((c) => c.name).join(' | ');
  for (const required of [
    'unsigned message', 'unsigned prompt', 'unsigned answer', 'unsigned action', 'unsigned heartbeat',
    'short mac', 'long mac', 'uppercase mac', 'unknown verb', 'short head SHA', 'uppercase head SHA',
    'too many options', 'unknown field', '__proto__ field',
  ]) {
    assert.ok(names.includes(required), `the vectors refuse "${required}": ${names}`);
  }
});

// ---------------------------------------------------------------------------
// The direction (owner ruling 2026-10-18 on PR #579). One per-pairing key signs
// the inbox, the outbox and the heartbeat, so without the direction in the
// bytes a phone-signed inbox message copied into the outbox by a minted token
// would still verify there, and the reverse. The direction is not a stored
// field: the reader supplies it from the node it read the record from, and
// canonicalBytes covers it as `direction`.
// ---------------------------------------------------------------------------

test('the direction is covered: the same record has different bytes in each direction', () => {
  assert.deepEqual([...DIRECTIONS], ['inbox', 'outbox', 'heartbeat']);
  const m = good();
  assert.equal(
    canonicalBytes(m, 'inbox'),
    '{"at":1700000000000,"direction":"inbox","id":"m-0001","sender":"phone","text":"run 42 finished"}',
  );
  assert.equal(
    canonicalBytes(m, 'outbox'),
    '{"at":1700000000000,"direction":"outbox","id":"m-0001","sender":"phone","text":"run 42 finished"}',
  );
  assert.notEqual(canonicalBytes(m, 'inbox'), canonicalBytes(m, 'outbox'));
  assert.notEqual(canonicalBytes(aHeartbeat(), 'heartbeat'), canonicalBytes(aHeartbeat(), 'outbox'));
});

test('a tag made for one direction does not verify in another', () => {
  const m = good({ kind: 'answer', reply_to: 'm-0000', text: 'merge it' });
  const asOutbox = { ...m, mac: macOf(m, TEST_KEY, 'outbox') };
  assert.equal(verifyMac(asOutbox, TEST_KEY, 'outbox'), true);
  assert.equal(verifyMac(asOutbox, TEST_KEY, 'inbox'), false, 'an outbox message reflected into the inbox fails');
  const asInbox = { ...m, mac: macOf(m, TEST_KEY, 'inbox') };
  assert.equal(verifyMac(asInbox, TEST_KEY, 'inbox'), true);
  assert.equal(verifyMac(asInbox, TEST_KEY, 'outbox'), false, 'an inbox message reflected into the outbox fails');
  const beat = aHeartbeat();
  const signedBeat = { ...beat, mac: macOf(beat, TEST_KEY, 'heartbeat') };
  assert.equal(verifyMac(signedBeat, TEST_KEY, 'heartbeat'), true);
  assert.equal(verifyMac(signedBeat, TEST_KEY, 'outbox'), false, 'a heartbeat tag is not an outbox tag');
  assert.equal(verifyMac({ ...m, mac: macOf(m, TEST_KEY, 'outbox') }, TEST_KEY, 'heartbeat'), false);
});

test('the direction is required, closed, and never taken from the record', () => {
  for (const bad of [undefined, null, '', 'Inbox', 'in', 'both', 1, {}]) {
    assert.throws(() => canonicalBytes(good(), bad), TypeError, `direction ${JSON.stringify(bad)}`);
    assert.throws(() => macOf(good(), TEST_KEY, bad), TypeError);
    assert.equal(verifyMac({ ...good(), mac: macOf(good(), TEST_KEY, 'inbox') }, TEST_KEY, bad), false);
  }
  // A record cannot carry its own direction: that would let the writer choose
  // the very field the reader is meant to supply. The schema refuses it as an
  // unknown field, and canonicalBytes refuses it rather than let one shadow the
  // other.
  refusal(good({ direction: 'inbox' }), REFUSALS.UNKNOWN_FIELD, 'direction');
  heartbeatRefusal(aHeartbeat({ direction: 'heartbeat' }), REFUSALS.UNKNOWN_FIELD, 'direction');
  assert.throws(() => canonicalBytes({ ...good(), direction: 'inbox' }, 'inbox'), TypeError);
  assert.throws(() => canonicalBytes({ ...good(), direction: 'outbox' }, 'inbox'), TypeError);
  assert.equal(FIELDS.includes('direction'), false, 'the direction is not a stored field');
});

test('the vectors pin the direction: same record, different bytes, and refusals across directions', () => {
  const vectors = JSON.parse(readFileSync(new URL('./vectors.json', import.meta.url), 'utf8'));
  for (const c of vectors.cases) {
    assert.ok(DIRECTIONS.includes(c.direction), `${c.name}: names its direction`);
  }
  const pairs = vectors.direction_pairs;
  assert.ok(Array.isArray(pairs) && pairs.length >= 2, 'the vectors carry direction pairs');
  for (const p of pairs) {
    assert.notEqual(p.a.direction, p.b.direction, `${p.name}: two directions`);
    assert.deepEqual(p.a.message, p.b.message, `${p.name}: the record is otherwise identical`);
    assert.notEqual(p.a.canonical, p.b.canonical, `${p.name}: different bytes`);
    assert.notEqual(p.a.mac, p.b.mac, `${p.name}: different tags`);
    for (const side of [p.a, p.b]) {
      assert.equal(canonicalBytes(side.message, side.direction), side.canonical, `${p.name}: bytes`);
      assert.equal(macOf(side.message, vectors.key, side.direction), side.mac, `${p.name}: tag`);
    }
  }
  const names = vectors.cases.filter((c) => c.verdict === 'refused').map((c) => c.name).join(' | ');
  for (const required of ['outbox tag read as inbox', 'inbox tag read as outbox', 'heartbeat tag read as outbox']) {
    assert.ok(names.includes(required), `the vectors refuse "${required}": ${names}`);
  }
});
