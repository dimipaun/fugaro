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
import { canonicalBytes, validateHeartbeat, validateMessage } from './index.js';
import { macOf } from './mac.js';

const KEY = ['not', 'a', 'secret', 'test', 'key', '0001'].join('-');
const AT = 1_700_000_000_000;

const FAKE_MAC = 'f'.repeat(64);
const message = (over = {}) => ({ id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: AT, mac: FAKE_MAC, ...over });
const heartbeat = (over = {}) => ({ at: AT, boot: 'boot-01', agents: [{ id: 'orchestrator', name: 'Orchestrator' }], mac: FAKE_MAC, ...over });

// Every case starts life as a well-formed record with a placeholder mac. The
// tag is computed over the canonical bytes and written into the record, which
// is exactly what the phone does before writing to the outbox and what the
// consumer does before comparing.
//
// The direction is inside the bytes (owner ruling 2026-10-18): a heartbeat is
// signed for `heartbeat`, a prompt — the consumer's question — for `outbox`,
// and everything else here is the phone's, for `inbox`. Every case records its
// direction, and a reader must pass the node it READ from, never this field.
function dirOf(m) {
  if (Object.hasOwn(m, 'boot')) return 'heartbeat';
  return m.kind === 'prompt' ? 'outbox' : 'inbox';
}

function signed(m, direction = dirOf(m)) {
  const mac = macOf(m, KEY, direction);
  return { ...m, mac };
}

// tampered takes a signed case and a change to its covered fields, and keeps
// the ORIGINAL tag: the message no longer matches the tag it carries, which is
// the one thing a MAC can refuse and the whole reason the tag exists.
//
// `mac` is always the tag the canonical bytes PRODUCE, never the one the record
// carries: for a verified case the two are the same string, and for a refused
// case they differ by construction — which is what the case is for.
function tampered(name, why, original, changed, direction = dirOf(changed)) {
  const mac = macOf(changed, KEY, direction);
  return {
    name,
    why,
    direction,
    message: { ...changed, mac: original.mac },
    canonical: canonicalBytes(changed, direction),
    mac,
    verdict: 'refused',
  };
}

const cases = [];
function verified(name, why, record) {
  const direction = dirOf(record);
  const signedRecord = signed(record, direction);
  cases.push({
    name,
    why,
    direction,
    message: signedRecord,
    canonical: canonicalBytes(signedRecord, direction),
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

// Kept, not just pushed: the two halves of the merge the consumer must
// re-check before it merges anything — the pull request the read-back named and
// the head it was on — are pinned as swaps below (§3.5's list of what P6.3's
// vectors must refuse).
const mergeOne = verified(
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

// The key order, at the one pair of ranges where the two languages do not
// already agree. Go sorts map keys bytewise, which for UTF-8 is code-point
// order; JavaScript's default `.sort()` compares UTF-16 code units, which puts
// an astral key (first unit D800..DBFF) BEFORE a key in U+E000..U+FFFF — the
// opposite order, and therefore a different tag for the same record. The keys
// below are U+FFFF and U+1F600 for that reason and no other: a port that sorts
// with `.sort()` signs these bytes differently from the consumer, and this case
// is what tells it so.
verified(
  'keys are ordered by code point, not by UTF-16 code unit',
  '§3.5 sorts keys by code point at every depth; U+FFFF < U+1F600 by code point and after it by UTF-16 code unit',
  { ...message({ kind: 'prompt' }), '￿': 1, '😀': 2 },
);

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

cases.push(
  tampered('target swapped', 'another pull request under a tag made for this one: the read-back named a target, and the consumer must merge that one', mergeOne, { ...mergeOne, action: { ...mergeOne.action, target: { repo: 'dimipaun/fugaro', pr: 533 } } }),
);
cases.push(
  tampered('head SHA swapped', 'the branch moved after the read-back: the head in the record is the state of the world that was confirmed', mergeOne, { ...mergeOne, action: { ...mergeOne.action, head_sha: 'c'.repeat(40) } }),
);

const cancelOne = signed(message({ kind: 'action', agent: 'orchestrator', text: 'cancel the run', action: { id: 'a-0001', verb: 'cancel', target: { run: '20261018-125859-d3c8' }, confirmation: { id: 'c-0001', at: AT } } }));
cases.push(
  tampered('confirmation swapped', 'another confirmation id under a tag made for this one: the consumer\'s ledger must not be fooled', cancelOne, { ...cancelOne, action: { ...cancelOne.action, confirmation: { id: 'c-0009', at: AT } } }),
);
cases.push(
  tampered('verb swapped', 'a cancel relabelled as a merge under its own tag', cancelOne, { ...cancelOne, action: { ...cancelOne.action, verb: 'merge' } }),
);

// --- the direction: a tag made for one node does not verify in another ---
// A reflected message keeps every byte of its record and its tag, and only the
// node it is read from changes; `mac` is the tag the bytes PRODUCE for the
// reading direction, which differs from the one the record carries.
const answerIn = signed(message({ kind: 'answer', reply_to: 'm-0000', text: 'merge it' }), 'inbox');
const promptOut = signed(message({ kind: 'prompt', options: ['merge it', 'leave it'], text: 'approve the merge?' }), 'outbox');
cases.push(tampered('inbox tag read as outbox', 'a phone-signed answer copied into the outbox by a minted token: the tag was made for the inbox', answerIn, answerIn, 'outbox'));
cases.push(tampered('outbox tag read as inbox', 'a consumer-signed message copied into the inbox: "approve the merge?" answered with the consumer\'s own words', promptOut, promptOut, 'inbox'));
cases.push(tampered('heartbeat tag read as outbox', 'a signed heartbeat is not an outbox message', beat, beat, 'outbox'));

// direction_pairs: one record, two directions, and nothing else different —
// so the pair proves the direction alone changes the bytes and the tag.
const direction_pairs = [];
function pair(name, record, a, b) {
  const side = (direction) => ({ direction, message: record, canonical: canonicalBytes(record, direction), mac: macOf(record, KEY, direction) });
  direction_pairs.push({ name, a: side(a), b: side(b) });
}
pair('plain message, inbox and outbox', message(), 'inbox', 'outbox');
pair('answer, inbox and outbox', message({ kind: 'answer', reply_to: 'm-0000', text: 'merge it' }), 'inbox', 'outbox');
pair('heartbeat, heartbeat and outbox', heartbeat(), 'heartbeat', 'outbox');

// The schema's refusals, as data: what both ends must REFUSE before a tag is
// even compared. Each case is a value, the record it is judged as, and the
// code and field the JavaScript validator answers with; the generator asserts
// the verdict as it writes it, so the file cannot carry a case this module
// would accept. The Go consumer need not reproduce the codes' wording, but it
// must refuse every one of these values.
const refusals = [];
function refused(name, why, record, value) {
  const verdict = record === 'message' ? validateMessage(value) : validateHeartbeat(value);
  if (verdict.ok) throw new Error(`refusal vector "${name}" is accepted by the schema`);
  refusals.push({ name, why, record, value, code: verdict.code, field: verdict.field });
}
const unsigned = (m) => {
  const copy = { ...m };
  delete copy.mac;
  return copy;
};
const actionCancel = { id: 'a-0001', verb: 'cancel', target: { run: '20261018-125859-d3c8' }, confirmation: { id: 'c-0001', at: AT } };
const actionMerge = { id: 'a-0001', verb: 'merge', target: { repo: 'dimipaun/fugaro', pr: 532 }, head_sha: 'a'.repeat(40), confirmation: { id: 'c-0001', at: AT } };
refused('unsigned message', 'the tag is required on every inbox and outbox message (§3.5)', 'message', unsigned(message()));
refused('unsigned prompt', 'a prompt is signed like every other kind', 'message', unsigned(message({ kind: 'prompt', options: ['merge it', 'leave it'] })));
refused('unsigned answer', 'an unsigned answer is exactly what an impostor would send to "approve the merge?"', 'message', unsigned(message({ kind: 'answer', reply_to: 'm-0000' })));
refused('unsigned action', 'an action record without a tag is never executed', 'message', unsigned(message({ kind: 'action', action: actionCancel })));
refused('unsigned heartbeat', 'an unsigned heartbeat is offline, and its agent list is ignored (O7)', 'heartbeat', unsigned(heartbeat()));
refused('short mac', 'a tag is exactly 64 hex characters: 63 is not a tag', 'message', message({ mac: 'f'.repeat(63) }));
refused('long mac', '65 is not a tag either', 'message', message({ mac: 'f'.repeat(65) }));
refused('uppercase mac', 'hex has one spelling here, so one tag has one string', 'message', message({ mac: 'F'.repeat(64) }));
refused('null mac', 'absent is omitted, never null', 'message', message({ mac: null }));
refused('unknown verb', 'the verbs are cancel, merge and confirm, and nothing else', 'message', message({ kind: 'action', action: { ...actionCancel, verb: 'delete' } }));
refused('short head SHA', 'a head SHA is 40 or 64 hex characters: an abbreviation cannot be compared byte for byte', 'message', message({ kind: 'action', action: { ...actionMerge, head_sha: 'a'.repeat(12) } }));
refused('uppercase head SHA', 'a head SHA is lowercase, as the provider returns it', 'message', message({ kind: 'action', action: { ...actionMerge, head_sha: 'A'.repeat(40) } }));
refused('merge without head SHA', 'a merge carries the head it was confirmed against', 'message', message({ kind: 'action', action: (({ head_sha, ...rest }) => rest)(actionMerge) }));
refused('too many options', 'a prompt carries at most eight options', 'message', message({ kind: 'prompt', options: Array.from({ length: 9 }, (_, i) => `option ${i}`) }));
refused('too long text', 'a body is at most 4096 code units', 'message', message({ text: 'x'.repeat(4097) }));
refused('unknown field', 'a field the schema does not name is refused, so the field set is the version', 'message', message({ expires_at: AT }));
refused('__proto__ field', 'a parsed __proto__ is an unknown field, never a prototype', 'message', JSON.parse(JSON.stringify(message()).replace('{', '{"__proto__":{"kind":"action"},')));
refused('action on a plain message', 'a kind carries its own fields and no others', 'message', message({ action: actionCancel }));
refused('answer without reply_to', 'an answer names the prompt it answers', 'message', message({ kind: 'answer' }));
refused('float at', 'a time is an integer millisecond', 'message', message({ at: AT + 0.5 }));
refused('stored direction', 'the direction is the reader\'s to supply, never a field the writer stores', 'message', message({ direction: 'inbox' }));
refused('stored direction on a heartbeat', 'a heartbeat cannot claim its own direction either', 'heartbeat', heartbeat({ direction: 'heartbeat' }));
const file = {
  format: 'fugaro mailbox message tag vectors',
  algorithm: 'HMAC-SHA-256 over canonicalBytes(message, direction), lowercase hex (mobile plan §3.5, D171; direction by the owner ruling of 2026-10-18)',
  direction: 'the node the record is stored in (inbox, outbox or heartbeat), covered as the field "direction"; it is never stored in the record, and a reader passes the node it read from',
  canonical: 'JSON, keys sorted by code point at every depth, no insignificant whitespace, absent fields omitted, JSON\'s own escaping only, integers only',
  covered: 'every field except mac and acked, plus the direction',
  key: KEY,
  key_note: 'a readable placeholder, not a credential: the real key is the per-pairing key of D171 and never appears in this repository',
  generated_by: 'node shared/mailbox-schema/vectors.generate.mjs',
  cases,
  direction_pairs,
  refusals,
};

writeFileSync(new URL('./vectors.json', import.meta.url), `${JSON.stringify(file, null, 2)}\n`);
console.log(`wrote ${cases.length} cases and ${refusals.length} refusals`);
