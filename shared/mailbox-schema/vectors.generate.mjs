// Regenerates vectors.json, the shared test vectors for the message tag
// (mobile plan, Task P6.3, §3.5).
//
//   node shared/mailbox-schema/vectors.generate.mjs
//
// The vectors are the CONTRACT between the two implementations of the
// canonical serialisation: this package, which the phone runs, and the Go
// consumer, which reimplements `canonicalBytes` because it cannot call into
// JavaScript. Neither end can test the other, so both test against this file:
// every case pins the message, the exact bytes the tag covers, the tag itself
// and whether it verifies. A disagreement about key order, whitespace, UTF-8
// escaping or the covered field set fails on the side that is wrong, at the
// byte level, rather than at three in the morning on a phone.
//
// The key is a READABLE PLACEHOLDER and never a generated one: `index.test.js`
// asserts that it matches /not-a-secret|fake|test-key|placeholder/ and that no
// case carries anything that looks like a credential, so a real key pasted in
// "just to debug" fails the suite rather than shipping.
//
// This file is not a test and is not run by `npm test` (which matches
// `**/*.test.js`); it is the recipe for the artifact, kept beside it so the
// artifact can be re-derived rather than hand-edited.

import { writeFileSync } from 'node:fs';
import { canonicalBytes, macOf } from './index.js';

const KEY = ['not', 'a', 'secret', 'test', 'key', '0001'].join('-');
const AT = 1_700_000_000_000;

const FAKE_MAC = 'f'.repeat(64);
const message = (over = {}) => ({ id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: AT, mac: FAKE_MAC, ...over });
const heartbeat = (over = {}) => ({ at: AT, boot: 'boot-01', agents: [{ id: 'orchestrator', name: 'Orchestrator' }], mac: FAKE_MAC, ...over });

// Every case starts life as a well-formed record with a placeholder mac. The
// tag is computed over the canonical bytes and written into the record, which
// is exactly what the phone does before writing to the outbox and what the
// consumer does before comparing.
function signed(m) {
  const mac = macOf(m, KEY);
  return { ...m, mac };
}

// tampered takes a signed case and a change to its covered fields, and keeps
// the ORIGINAL tag: the message no longer matches the tag it carries, which is
// the one thing a MAC can refuse and the whole reason the tag exists.
//
// `mac` is always the tag the canonical bytes PRODUCE, never the one the record
// carries: for a verified case the two are the same string, and for a refused
// case they differ by construction — which is what the case is for.
function tampered(name, why, original, changed) {
  const mac = macOf(changed, KEY);
  return {
    name,
    why,
    message: { ...changed, mac: original.mac },
    canonical: canonicalBytes(changed),
    mac,
    verdict: 'refused',
  };
}

const cases = [];
function verified(name, why, record) {
  const signedRecord = signed(record);
  cases.push({
    name,
    why,
    message: signedRecord,
    canonical: canonicalBytes(signedRecord),
    mac: signedRecord.mac,
    verdict: 'verified',
  });
  return signedRecord;
}

// --- the four kinds, and the heartbeat, each with a tag over its own fields ---
const plain = verified(
  'plain message',
  'the ordinary message of §3.1: four fields and a tag, and the acked flag the phone writes later is not covered',
  message({ acked: true }),
);

verified(
  'prompt with options',
  'a prompt reads its options aloud and the answer names one by position, so the array is covered in order (D167, §3.2)',
  message({ kind: 'prompt', options: ['merge it', 'leave it'], text: 'the pull request is green' }),
);

verified('answer', 'an answer says which prompt it settles (D168 amended: the TTL is the consumer\'s)', message({ kind: 'answer', reply_to: 'm-0000', text: 'option two' }));

verified(
  'action: cancel a run',
  'D169\'s typed destructive command: the confirmation id and time are inside the tagged bytes, never beside them',
  message({ kind: 'action', agent: 'orchestrator', text: 'cancel the run', action: { id: 'a-0001', verb: 'cancel', target: { run: '20261018-125859-d3c8' }, confirmation: { id: 'c-0001', at: AT } } }),
);

verified(
  'action: merge a pull request',
  'a merge carries the head SHA read back at confirmation, which the consumer re-checks before it merges',
  message({ kind: 'action', agent: 'orchestrator', text: 'merge the pull request', action: { id: 'a-0002', verb: 'merge', target: { repo: 'dimipaun/fugaro', pr: 532 }, head_sha: 'b'.repeat(40), confirmation: { id: 'c-0002', at: AT } } }),
);

verified('heartbeat', 'O7: the consumer\'s liveness record, whose agent list feeds D163 — an unsigned heartbeat is offline', heartbeat());

// --- the bytes, not the values: the two encoders must agree exactly ---
verified(
  'html characters stay raw',
  'Go escapes <, > and & unless SetEscapeHTML(false) is set; the tagged bytes are the raw characters',
  message({ text: 'behind <b> & "quoted"' }),
);

verified('non-ASCII is UTF-8', 'an emoji is its own characters, not a \\u escape, in both languages', message({ text: 'x😀y' }));

// Not a message the schema would accept — `landed_at` is an unknown field and
// is refused — but a serialisation rule the tag depends on: §3.5 covers every
// field except `mac` and `acked`, including one a later protocol adds, so an
// implementation that marshals only the fields it knows produces different
// bytes and fails here.
verified(
  'an unknown field is inside the bytes',
  '§3.5 covers a field added later by default, and omits an absent one rather than writing null',
  { ...message({ kind: 'prompt' }), landed_at: AT },
);

// --- the swaps a launcher with a minted token but no key would try (G13, D171) ---
cases.push(
  tampered('text swapped', 'the body under a tag made for another body: "merge it" spoken by an impostor', plain, { ...plain, text: 'merge the pull request' }),
);

const optionsOne = signed(message({ kind: 'prompt', options: ['merge it'], text: 'the pull request is green' }));
cases.push(
  tampered('options swapped', 'an option added under a tag made for the shorter list: "option two" must not change meaning', optionsOne, { ...optionsOne, options: ['merge it', 'leave it'] }),
);
cases.push(
  tampered('options swapped: order', 'a reordered list is a different set of positions, so it is different bytes', optionsOne, { ...optionsOne, options: ['leave it', 'merge it'] }),
);

const answerOne = signed(message({ kind: 'answer', reply_to: 'm-0000', text: 'option one' }));
cases.push(
  tampered('reply_to swapped', 'an answer moved to another prompt under its own tag', answerOne, { ...answerOne, reply_to: 'm-0002' }),
);
cases.push(
  tampered('answer swapped', 'the answer itself changed under a tag made for "option one"', answerOne, { ...answerOne, text: 'option two' }),
);

const beat = signed(heartbeat());
cases.push(
  tampered('agents changed', 'an impostor added to the advertised list under a tag made for the real one', beat, { ...beat, agents: [{ id: 'orchestrator', name: 'Orchestrator' }, { id: 'impostor', name: 'Orchestrator' }] }),
);
cases.push(tampered('agent renamed', 'the spoken name of an advertised agent changed under its tag', beat, { ...beat, agents: [{ id: 'orchestrator', name: 'Impostor' }] }));

const cancelOne = signed(message({ kind: 'action', agent: 'orchestrator', text: 'cancel the run', action: { id: 'a-0001', verb: 'cancel', target: { run: '20261018-125859-d3c8' }, confirmation: { id: 'c-0001', at: AT } } }));
cases.push(
  tampered('confirmation swapped', 'another confirmation id under a tag made for this one: the consumer\'s ledger must not be fooled', cancelOne, { ...cancelOne, action: { ...cancelOne.action, confirmation: { id: 'c-0009', at: AT } } }),
);
cases.push(
  tampered('verb swapped', 'a cancel relabelled as a merge under its own tag', cancelOne, { ...cancelOne, action: { ...cancelOne.action, verb: 'merge' } }),
);

const file = {
  format: 'fugaro mailbox message tag vectors',
  algorithm: 'HMAC-SHA-256 over canonicalBytes(message), lowercase hex (mobile plan §3.5, D171)',
  canonical: 'JSON, keys sorted by code point at every depth, no insignificant whitespace, absent fields omitted, JSON\'s own escaping only, integers only',
  covered: 'every field except mac and acked',
  key: KEY,
  key_note: 'a readable placeholder, not a credential: the real key is the per-pairing key of D171 and never appears in this repository',
  generated_by: 'node shared/mailbox-schema/vectors.generate.mjs',
  cases,
};

writeFileSync(new URL('./vectors.json', import.meta.url), `${JSON.stringify(file, null, 2)}\n`);
console.log(`wrote ${cases.length} cases`);
