# `mailbox-schema` — the message schema and its validation

The mobile plan's **Task P0.2**, in the shared pure-logic package [P0.1](../README.md)
built, extended by **Task P6.3** with the fields D161, D167, D168 (as amended), D169
and D171 need and with the tag §3.5 puts on every message. It answers one question —
**is this value a mailbox message?** — and nothing else.

**The schema is the protocol.** P1.1's state machine validates against it, P3.6's app
writes it and P4.1's consumer reads it, so the three cannot disagree about what a
message is. The plan is explicit that this task sits on the **critical path** and must
stay small: the redactor is P1.3's, the dedupe table and the replay window are P1.1's,
the link rules are P0.3's, and *who* may write a message is P2.1's rules. Nothing of
theirs is built here — with one deliberate exception, the tag, because a tag is a
property of the message's own bytes and there is nowhere else it could live.

```sh
cd shared && npm test          # this module's tests, with no install and no network
```

```js
import { validateMessage, isMessage, canonicalBytes } from './mailbox-schema/index.js';
import { macOf, verifyMac } from './mailbox-schema/mac.js';   // Node only — see below

const message = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1700000000000, mac: '…64 hex…' };
validateMessage(message);
// => { ok: true, message: { …the same five fields, deep-frozen… } }

validateMessage({ …message, id: 'm.0001' });
// => { ok: false, code: 'bad-key', field: 'id' }

verifyMac({ …message, text: 'merge it' }, key, 'inbox');   // => false: the bytes no longer match the tag
```

## The message

| Field | Type | Required | Bound | Decided by |
|---|---|---|---|---|
| `id` | string | yes | 1–100 code units, and a **database key** | D110's explicit message id: P1.1 dedupes on `(sender, id)` |
| `sender` | string | yes | 1–200 code units | the writer's identity; P1.1 does not dedupe across senders, P2.1 binds it to the writer's claim |
| `text` | string | yes | 1–4096 code units | D118's size bound per message; **already redacted** when it arrives (P1.3) |
| `at` | number | yes | epoch ms, a safe integer ≥ 0 | the tree's own time unit (`budget.Day`, the token's `fx` claim) |
| `mac` | string | **yes** (P6.3) | exactly 64 lowercase hex characters | §3.5, D169, D171, O7 — see below |
| `acked` | boolean | no | — | D113's second phone write; **absent means not acked**, and it is **outside the tagged bytes** |
| `agent` | string | no | 1–64 code units, an **identity string** | D161: one mailbox per account, the agent travels in the message. The same shape the heartbeat advertises (D163) |
| `kind` | string | no | `message` · `prompt` · `answer` · `action`; absent means `message` | D167, D169, §3.2. P6.5/P6.7 map it to an acceptance window (D173) |
| `options` | string[] | on a `prompt`, optional | 1–8 entries, each 1–200 code units, distinct, single-line | §3.2: the app reads them aloud and the answer names one by position |
| `reply_to` | string | on an `answer`, optional on an `action` | a message id | D168 (as amended) for the answer; §3.5 for the action that settles a prompt |
| `action` | object | on an `action` | the record below | D169's typed destructive command |

A validated message comes back as a **deep-frozen copy**: a fresh, plain object — and
fresh objects all the way down — carrying exactly these fields, with the caller's
values **verbatim**. Nothing is trimmed, floored, stringified or dropped. The copy is
deep on purpose: `message.action.verb` is a field the consumer *executes*, so a nested
record still shared with the caller, or still editable, would be a record whose tag
proves nothing.

### The kinds, and what each may carry

| Kind | May carry | Must carry | What it is |
|---|---|---|---|
| `message` (or no `kind`) | — | — | something said to, or by, an agent (§3.1) — including **a launch**, which D162 sends with no read-back |
| `prompt` | `options` | — | a question the agent wants answered (§3.2), with or without choices |
| `answer` | — | `reply_to` | the answer to a prompt; the chosen words are `text`, because the answer is *spoken*, not selected |
| `action` | `reply_to` | `action` | D169's destructive command |

A field on the wrong kind is **`field-not-allowed`**, not an unknown field: two agents
disagreeing about whether a message *is* an action is exactly the ambiguity this module
exists to refuse.

### The action record

```js
{ id: 'a-0001',                       // the action's own id, a database key
  verb: 'cancel' | 'merge' | 'confirm',
  target: { run: '20261018-125859-d3c8' }            // cancel, confirm
        | { repo: 'owner/name', pr: 532 },           // merge, confirm
  head_sha: '…40 or 64 hex…',         // merge only: the head read back at confirmation
  confirmation: { id: 'c-0001', at: 1700000000000 } }
```

Everything in it is required, because everything in it is **executed**: the consumer
runs `fugaro cancel RUN` or `gh pr merge <n> --repo <owner/name>` from these fields and
nothing else. Two rules are security rules rather than tidiness:

- **A verb and its target must agree.** A cancel resolves to a run id, a merge to a
  repository and a pull request; `confirm` — the ruling's third verb, confirming an
  action — takes either, because what it confirms may be either.
- **No target can be read as a command-line flag.** The values are passed as ARGV
  elements, so a leading `-` or `.` is a flag or a path to whatever parser sees it, and
  a space, a slash where a slash does not belong, a newline or a NUL is not a run id or
  a repository at all. The two families are named separately — `bad-target` for the
  shape, `bad-character` for the alphabet — so a caller can say which of its inputs to
  fix. A merge's head SHA is re-read by the consumer before it merges: the record names
  a state of the world, not just an intention.

A launch is deliberately **not** a verb: D162 answered that "kick off …" goes through
without a read-back, so it travels as an ordinary `message`. Every other destructive
verb — the kill switch, budget writes, deletes — stays refused by voice and so has no
verb here at all (§3.1).

### Why there is no `expires_at`

D168 as amended: a prompt expires at **D118's mailbox TTL, computed by the consumer**
from `at` and its own clock. An expiry written by the phone would be the phone's clock
deciding — the clock D117 refuses to trust — so the field does not exist, and every
name for it (`expires_at`, `expiresAt`, `ttl`, `deadline`) is an unknown field. The
confirmation *timeout* is absent for the same reason: it is P6.2's phone-local state,
not a property of the message.

### Why there is no version field

**The field set is the version**, and with kinds it is the (kind, field) table. P2.1's
subtree idiom denies `$other`, and this validator refuses any field it does not name
and any field on the wrong kind, so a message from a future protocol that carries an
extra field is refused by the rules *and* here rather than being silently accepted with
the new field dropped. That is fail-closed version skew without a second number for two
artifacts — the app, which ships on the stores' cadence, and the CLI — to disagree
about.

## The tag, and why it is on *every* message

A launcher can mint a custom token for any uid (G13) and can read
`config/admin/devices` through `firebasedatabase.viewer`, so a minted token passes both
of the rules' identity conjuncts and can write into a paired account's mailbox. What it
cannot have is the **per-pairing key** of D171: that travels encrypted to a key the
phone holds (D165) and never through the database. So the tag over the message's own
bytes is the one thing that separates a message the phone really spoke from one a
launcher invented. It is required on **every** inbox message of every kind, not only on
action records: a plain `message` or `answer` saying "merge it" is exactly the shape an
impostor would use to answer the orchestrator's own "approve the merge?" prompt (§3.5).
It is required on the outbox too, and on the consumer's heartbeat (O7, D161) — an
unsigned or wrongly signed heartbeat is one the phone treats as **offline**, and whose
advertised agent list it ignores, because that list is what "list agents" reads out and
what D163 defaults to.

The tag covers **every field except `mac` and `acked`** — `acked` because the phone
writes it after the fact on an outbox message — **plus the direction**, over the
canonical bytes below.

### The direction is inside the bytes (owner ruling, 2026-10-18)

One per-pairing key signs the inbox (the phone), the outbox and the heartbeat (the
consumer). Without more, a phone-signed inbox message copied into the outbox by a minted
token would still verify there, and a consumer-signed outbox message copied into the
inbox would verify as the phone's — "approve the merge?" answered with the consumer's own
words. So `canonicalBytes(record, direction)` covers the **direction** — one of
`DIRECTIONS`: `inbox`, `outbox`, `heartbeat` — as the field `direction`, and a tag is
valid in one direction only. **This is one field beyond the covered list of
[§3.5](../../docs/mobile/functional-spec-reconciliation.md), by the owner's ruling of
2026-10-18 on PR #579**; §3.5 carries a dated note saying so.

The direction is **not a stored field**. The reader supplies it from the node it read
the record from (`verifyMac(record, key, 'inbox')` in the consumer, `'outbox'` or
`'heartbeat'` on the phone), so the writer never chooses it: a record carrying a
`direction` of its own is refused by the schema (`unknown-field`) and by
`canonicalBytes` (it throws), and a direction outside the three throws too. The vectors
pin it three ways: every case names its direction, `direction_pairs` holds one record
under two directions with different bytes and tags, and three refused cases are a
reflection (inbox read as outbox, outbox read as inbox, heartbeat read as outbox).

This module validates a tag's **shape** and never its value: it has no key, and
`verifyMac(record, key, direction)` is the separate, keyed question the consumer asks. A key is
never written down here, and the module never reads one from anywhere.

### `canonicalBytes` — the one serialisation the tag covers

JSON, keys sorted by **code point** at every depth, no insignificant whitespace, absent
fields **omitted** (never `null`), strings as UTF-8 with JSON's own escaping only,
numbers as integers, and the `direction` field added. One message in one direction, one byte string — which is the only thing a MAC can
mean across two languages.

**Code point, not UTF-16 code unit.** §3.5's word is "code point", and Go's bytewise
map-key order is the same order (UTF-8 byte order *is* code-point order); JavaScript's
own `Array.prototype.sort` compares UTF-16 code **units**, which for exactly one pair of
ranges — an astral key against a key in U+E000..U+FFFF — gives the opposite order, and
therefore a different tag for the same record. `canonicalBytes` sorts with a code-point
comparator, and a vectors case pins it with the two keys U+FFFF and U+1F600: the hazard
is narrow, which is exactly why a port that used `.sort()` would otherwise ship it.

That is the hard part of this task, because the two ends are JavaScript (the phone) and
Go (the consumer), and the Go end cannot call into JavaScript. It reimplements the
serialisation and **tests against [`vectors.json`](vectors.json)**: 24 cases — the four
kinds, the heartbeat, the argv-shaped targets, HTML characters Go escapes by default,
an emoji, an unknown field inside the bytes, the key order above, and the swaps a
token-holder without the key would try (text, options, option order, `reply_to`, answer,
agents, agent name, target, head SHA, confirmation, verb) and the three reflections across directions — each pinning the message,
the exact bytes, the tag and whether it verifies — three `direction_pairs`, and **22 schema refusals**
(`refusals`: an unsigned message of every kind and an unsigned heartbeat, a short, long,
uppercase or null tag, an unknown verb, a short or uppercase head SHA, a merge without
one, too many options, an oversize body, an unknown field, a parsed `__proto__`, a field
on the wrong kind, an answer without `reply_to`, a float time, and a stored `direction` on a message and on a heartbeat), each with the value, the
record it is judged as and the code and field this validator answers. The Go consumer
need not copy the codes' wording, but it must refuse every one of those values. A
disagreement about key order, whitespace or escaping fails on the side that is wrong,
at the byte level, rather than at three in the morning on a phone. Regenerate with
`node shared/mailbox-schema/vectors.generate.mjs`; the key in the file is a readable
placeholder, and a test asserts that neither the vectors nor the suite carries anything
that looks like a credential.

**The bytes are written, not rebuilt.** `canonicalBytes` writes the JSON itself, one
`JSON.stringify` per string, rather than building a sorted object and stringifying it,
because a JavaScript object cannot hold the order or the keys the bytes need: it
enumerates integer-like keys (`"9"`, `"10"`) numerically and before every other key,
whatever the insertion order — Go writes `"10"` first — and assigning an own `__proto__`
key (which `JSON.parse` produces) sets a prototype instead of a field, which dropped the
field from the bytes and gave two different records one tag. Both were found by the
independent review and are pinned by tests and mutations.

`canonicalBytes` **refuses, by throwing**:

- **`undefined` and `null`.** A dropped field is two different records with one
  canonical form: absent is omitted, present is covered, and there is no third state.
- **a number that is not a safe integer, and `-0`.** Go prints `1e-07` where JavaScript
  prints `1e-7` for the same double, so a float inside a signed record is a tag that
  verifies on one end and not the other; an integer past 2^53 is printed in exponent
  form by JavaScript (`1e+21`); and `-0` is written `0`, a value it is not. Every number
  the schema accepts is a safe integer (`at`, a confirmation's `at`, a pull request
  number). The one exception is `-0`, which the schema accepts as an `at` (P0.2's
  contract; P1.1 refuses it with its own `negative-zero`): it has no canonical form, so
  a record carrying it can be neither tagged nor verified.
- **an object that is not plain data** — a `Date`, a `Map`, a class instance — whose
  shape is decided by its methods (`toJSON`) rather than its fields. A null-prototype
  object is plain data and is accepted.
- **a string the two encoders spell differently** — an **unpaired surrogate**, U+2028,
  U+2029 — in a key exactly as in a value. The schema refuses all three in every string
  it validates (`bad-character`, `bad-key`), so a caller that validated first cannot
  reach this; it is the second fence, for the exported function called on a record that
  did not come through the validator.

And the schema itself refuses **U+2028 LINE SEPARATOR and U+2029 PARAGRAPH
SEPARATOR** in every string it validates, for the same class of reason: Go's
`encoding/json` escapes both *even with `SetEscapeHTML(false)`*, and JavaScript's
`JSON.stringify` leaves both as themselves, so a message carrying one would have two
canonical byte strings and one tag that verifies on one end only — an undiagnosable
refusal, for a character nobody can speak. (The converse hazard is the Go side's to
handle: it escapes `<`, `>`, `&` unless it disables HTML escaping, and the vectors pin
the raw bytes it must produce.)

**The one import, in its own file.** The tag is HMAC-SHA-256, and the only
implementation of it in a runtime with no dependencies is a runtime builtin — Node has
one, React Native's bundler resolves neither it nor a buffer type. So the tag lives in
[`mac.js`](mac.js), which imports `node:crypto` (a builtin, not a dependency: nothing
is installed, nothing is fetched, and the package stays `private` with no
`dependencies`, D119), and **`index.js` imports nothing at all**. That split is not
tidiness, it is what keeps the schema usable by both of this package's consumers
(D119): a `node:crypto` import in the entry point would not cost the phone the tag
alone — it would make `validateMessage`, `validateHeartbeat` and `canonicalBytes`
unimportable there too, and those are pure logic over values, the parts the phone needs
most. `mac.js` is the Node implementation: P4.1's CLI consumer, the console, the tests
and the vectors generator.

The phone imports `index.js` and **implements HMAC-SHA-256 itself** — SHA-256 and the
HMAC construction over bytes — testing it against `vectors.json`, which is the only
contract between the two implementations. That port is P3.6's, not this task's, and it
is named in the pull request as an honest gap rather than implied by a sentence about
"reimplementing HMAC over the vectors' key". `index.test.js` asserts that the entry
point has no import statement, no `from '…'` and no buffer type, so re-introducing one
is a failing test rather than a discovery a bundler makes on a phone.

### P6.3 is a breaking change to P0.2's messages

`mac` is **required**, so every message P0.2's schema accepted — `{id, sender, text,
at}` with an optional `acked` — is now refused as `missing-field` `mac`. That is
intended (§3.5: no unsigned message, of any kind, in either direction) and there is no
compatibility path: nothing has shipped that writes a mailbox message (P3.6 and P4.1
are not built), and the project's own rule is no backward compatibility before release.
What it costs inside the repository is stated rather than discovered: the simulator
(`shared/mailbox-sim`) writes a mac-shaped placeholder on every message it makes, the
mailbox module's tests carry one, and P2.1's rules template refused a write that
carried `mac` until P6.4 landed (see the gaps below). A stored message written before
P6.3 is refused on read, which is the fail-closed reading of version skew.

### For the Go consumer (P4.1, P6.7): three things the vectors cannot say alone

- **Decode case-sensitively, and refuse unknown fields.** `encoding/json` matches object
  keys to struct fields **case-insensitively**, and keeps the last of duplicate keys, so
  a stored message carrying both `text` and `Text` would decode to one field while this
  validator refuses `Text` as unknown. Verify and validate over the decoded **map** (or a
  case-sensitive decoder) with every key checked against this module's field set, and
  compute the canonical bytes from the same map, never from a re-marshalled struct.
- **Write HTML characters raw** (`SetEscapeHTML(false)`) and sort keys by byte; the
  vectors pin both.
- **Pass the direction from the node you read, never from the record.** The consumer
  verifies everything it reads from the inbox with `inbox`; the phone verifies the
  outbox with `outbox` and the heartbeat with `heartbeat`. The `direction` key is added
  to the covered map before sorting, and a stored record carrying one is refused.

## Fail closed, and each refusal is named

Every refusal carries a **distinct code**, so a caller can tell a replay-shaped problem
from a malformed write instead of seeing "no message" for both:

| Code | What it refuses |
|---|---|
| `not-an-object` | `null`, an array, a primitive, a function — a message, an action record, a confirmation, a target, an advertised agent |
| `unknown-field` | a field the schema does not name (`__proto__` from JSON included: it is an unknown field, not pollution), at any depth |
| `missing-field` | a required field that is absent, at any depth |
| `wrong-type` | a field of the wrong type — **including an explicit `undefined`**, which is present-but-ambiguous |
| `empty-string` | an empty `id`, `sender`, `text`, `reply_to`, `agent`, `boot`, an option or an agent's name |
| `bad-key` | an `id`, an action id, a confirmation id or a `reply_to` containing a character a database key may not contain |
| `too-long` | a string longer than its bound |
| `too-many` | more options (`OPTIONS_MAX`) or advertised agents (`AGENTS_MAX`) than the bound allows |
| `too-few` | a list that is **present and empty**: a claim that says nothing |
| `bad-character` | a control character (line breaks are allowed in `text`, not in a single-line field), DEL, an unpaired surrogate, U+2028/U+2029, or a character outside a run id's, a repository's or an identity string's alphabet |
| `bad-timestamp` | `at` is **a number but not a time**: a float, `NaN`, `Infinity`, `-1` |
| `unknown-kind` | a `kind` outside `message` · `prompt` · `answer` · `action` |
| `unknown-verb` | an action's verb outside `cancel` · `merge` · `confirm` |
| `field-not-allowed` | a field on a kind of message that does not carry it (a `reply_to` on a prompt, an `action` on an answer, a head SHA on a cancel) |
| `bad-target` | a target that is not one of the two shapes, or that its verb cannot resolve to |
| `bad-sha` | a head SHA that is not 40 or 64 **lowercase** hex characters |
| `bad-mac` | a tag that is not exactly 64 lowercase hex characters |
| `duplicate-entry` | two options with the same words, or two advertised agents under one id — a choice by position, or by name, with two right answers |

The boundary between two of these rows is worth stating, because a caller branching on
the code must not be surprised: a **numeric string** such as `'1700000000000'` is
refused as `wrong-type` (it is not a number at all), and only a value that *is* a
number and still not a time reaches `bad-timestamp`. The same shape of distinction runs
through the rest: a value outside a vocabulary is not a wrong type, a count bound is not
a length bound, and a refusal never carries the value it refused.

There is no coercion anywhere: `'1700000000000'` is not a time, `'true'` is not an ack,
a padded id is returned padded, and uppercase hex is not accepted as a tag or as a head
SHA — hex has two spellings for one value, and a comparison that accepted both would
depend on which end wrote it.

## What this module is *not*

Each of these is a different task's, and the tests pin the boundary rather than
assuming it:

- **Not a redactor.** D118 redacts spoken text **before** the write, with the digest's
  redactor, so a token-shaped body arrives here already redacted; the schema bounds it
  and returns it verbatim. A case asserts exactly that, so that a later task cannot
  grow a second, weaker redactor in the schema. (P1.3)
- **Not the lease, the dedupe table or the replay window.** The schema says an `at` is
  a time; whether it is *too old* is a function of `at` and an injected `now`. (P1.1,
  P1.2)
- **Not the key, and not the verifier.** Where the per-pairing key comes from (D165,
  D171) and when to check the tag (P4.1) are other tasks'. This module holds no key.
- **Not the confirmation machine.** The spoken read-back, the "confirm", its timeout and
  its ledger are P6.2's and P6.7's. The schema carries the confirmation *record* D169
  puts on the wire.
- **Not the expiry rule.** See above: D168 as amended leaves the TTL to the consumer.
- **Not the URL rules.** A message carries text, not links. (P0.3)
- **Not the rules.** The schema is what P2.1's template must agree with — the same
  fields, the byte-for-byte key rule on the id, `text`'s bound at `TEXT_MAX` — but who
  may write a message, and the ack's own fence, are the rules' business. **P6.3 changed
  the schema and P6.4 has since carried it into the template**: the mailbox subtree now
  gates this field set — `mac` required on every message of every kind and on the
  heartbeat, with `agent`, `kind`, `options`, `reply_to` and the action record beside it,
  and the (kind, field) table's per-kind clauses — and
  `TestMailboxRulesAgreeWithTheSharedSchema` asserts the two lists against each other,
  bound by bound. The rules also apply this module's own patterns (`HEX_PATTERN`,
  `IDENTITY_PATTERN`, `RUN_PATTERN`, `REPO_PATTERN`) to a `mac`, a head SHA, an agent or
  boot id and a target, held byte for byte by `TestMailboxRulesCheckTheSchemasCharacters`.
  They stay looser in one direction and it is stated there: the key rule on an id and the
  control characters of free text are this module's alone. What the rules never do is
  verify a tag — they hold no key (P6.7 does, over `verifyMac`).
- **Not the node's retention.** D118's TTL and the owner's sweeper are properties of
  the mailbox node, not of a message.

## The reader that constrains the exports

`internal/budget/rules/rules_test.go` reads this file with a **regex**, not a JS
parser, to compare the rules' bounds and field sets with the schema's
(`TestMailboxRulesAgreeWithTheSharedSchema`): `export const NAME = …;` on one line,
`NAME` in `[A-Z_]+`, and a value that is digits, `Number.MAX_SAFE_INTEGER`,
`Object.freeze({…})` or `Object.freeze([…])`. Anything else makes that test `t.Fatalf`
so the reader gets taught rather than silently skipped. The exports below keep that
shape, and `FIELDS`/`REQUIRED_FIELDS` are the two arrays it compares.

## Evidence, and how to reproduce it

The package has **no dependency** and one builtin import, in `mac.js` alone: nothing is
installed, nothing is fetched, and the entry point the phone loads imports nothing at
all. The tests are the plan's verification list (a missing
id, a wrong type, an oversize body and an unknown field refused; a well-formed message
accepted) plus the fail-closed families above and P6.3's kind, action, tag and
canonical-byte cases: 55 tests across the module's two test files — the 51 in
`index.test.js`, and the four in `bounds.test.js`, which replays
[`vectors.bounds.json`](vectors.bounds.json), the bound vectors **both** ends
consume (`internal/mailbox`'s `TestTheSharedBoundVectorsHold` is the Go half).

**Every guard is mutation-proved**, not merely covered — 96 mutations over `index.js`
and `mac.js`: one for most guards, and two for the bounds that can be missed in
either direction — a comparison narrowed by a code unit, and the same one widened:

```sh
shared/mailbox-schema/mutation-proof.sh
# control       the pristine suite passes, 480 tests, 0 failures
# killed        control: refuse() always succeeds (must be killed)
# killed        not-an-object guard (null/array/primitive)
# ...                                                        (95 more)
# --- control killed; 96 guards: 96 killed, 0 survived, 0 not applied
```

That block is the run's own output with the middle elided: the harness prints **97**
`killed` lines — the negative control's, then one per guard — of which the two above
are the first, so 95 follow. The script disables one guard at a time — the object check,
the unknown-field check, each type check, each bound, each character rule, the surrogate
and separator rules, the timestamp range, the ack's type check, the deep freeze and the
copy, then P6.3's: the kind vocabulary and its (kind, field) table, the agent and
option shapes, the action record's verbs and both target shapes, the argv alphabets and
the head SHA, the required tag, the canonical serialisation — including the code-point
sort and the strings it refuses — and, in `mac.js`, that the tag is computed over the
canonical bytes and that `verifyMac` answers `false` rather than throwing.

Section 6 is the group about the bounds. Most of its rows **move a comparison by
one** — `> MAX` to `>= MAX`, and to `> MAX + 1` — because that, rather than a
deleted check, is the mutation this module has actually been wrong about. Three
of the eighteen move nothing and drop a term instead — DEL from each of the two
character rules, and the second head-SHA length from the SHA rule, which is the
same class of mistake one step further from the check's edge.
`repo.length > TARGET_MAX` narrowed to `>= TARGET_MAX` refused a repository
of exactly the bound, and widened to `> TARGET_MAX + 1` it accepted one a code
unit past it, with every test in this package **and** every test on the Go side
green. A bound that can be missed in either direction therefore takes two mutants
here, and the row that kills each one lives in `bounds.test.js` (and in the Go
replay), not in `vectors.json`: that file sits on exactly two lengths by accident
and on no other bound.

Two of the guards are about the **boundary** rather than a refusal: that `index.js` carries no import and no buffer
type, because that is what keeps the schema importable by the phone and nothing else in
the suite would notice it breaking.

It runs `npm test`, and requires the suite to fail
each time. It restores the files with `git checkout` after each one and refuses to run
against an uncommitted module.

**"Killed" is only worth reading because of three things it does first**, since
otherwise it is a claim about a command that may never have run:

- it **finds a working runner or stops**: a working `node` and `npm` on PATH, else one
  lent by `mise` (`node@22`, the line `package.json`'s `engines` declares — this
  repository's base image keeps node off PATH, so the path to one is mise's to own, not
  this script's to hardcode), else exit 2 with a message instead of a count;
- the **positive control**: the suite must pass on the pristine module *with at least
  one test run*, which is what catches the empty-glob trap `node --test` sets — a
  missing test file exits 0 having run nothing, and would otherwise look exactly like a
  green suite; and
- the **negative control**: a mutation of `refuse()` that must be caught, before any
  real one is trusted. If the suite cannot fail, the harness says so and exits 2 rather
  than reporting mutants it never measured.

Both controls were reproduced as failures while writing this: with the runner removed
(`PATH=/usr/bin:/bin`) the script refuses; with `index.test.js` renamed the positive
control passes over the skeleton's two tests alone and the **negative** control catches
it, which is why there are two. P6.3's four new guards were each *seen* to fail before
they were trusted: the first run of the extended script reported four `APPLY-FAILED`
lines against patterns that no longer matched the module, which is exactly the failure
mode the harness exists to surface.

## Three honest gaps

- **No CI job runs these tests yet.** P4.3 is that job and it is **owner-made** (the
  plan's Global Constraint 8: no agent edits `.github/workflows/`). Until it exists the
  tests run only where someone runs them — the same gap [the package's
  README](../README.md) records.
- **The rules carry the fields and a tag's characters, never its value.** P6.4 has
  landed (level 3, after P2.2): `rules.json.tmpl`, `rules.go` and the golden plan now
  gate this field set, `mac` included, and the mailbox's bounds are this module's numbers
  bound for bound, patterns included. What remains outside them is exact rather than
  vague: an id's key rule and `text`'s control characters are this module's alone; and
  the rules check a tag's *shape*, never its value, because the per-pairing key (D171)
  never reaches the database. `fugaro mailbox` (P6.7) is where a tag is verified.
- **The heartbeat this schema signs is not the heartbeat `shared/presence` reads.**
  `validateHeartbeat` accepts `{at, boot, agents?, mac}` — `boot` is the field name
  P2.1's template already gates (`rules.json.tmpl`), and `mac` and `agents` are what P6.3
  and O7 added. P1.4's staleness rule accepts `{at, bootId}` and refuses **any other
  field** (`heartbeat-unknown-field`), so a heartbeat signed per §3.5 cannot be handed to
  the rule that decides whether the phone is offline without a mapping in between — and
  nothing landed or planned maps it: P6.4 has landed and its Files row is
  `rules.json.tmpl`, `rules.go`, the golden plan, the version and the emulator test, and
  it does not touch `presence`.
  The `boot`/`bootId` half is older than this task and the template sides with `boot`;
  the `mac` and `agents` halves are new here. This is a **conflict for the owner**, not a
  design decision this task may take: the fix is either a rename on one side, or a
  caller-side mapping (P3.6's read of the heartbeat) that is named and tested where it
  is written. It is recorded here because the tag's own paragraph says an unsigned
  heartbeat is treated as offline, and a consumer that cannot feed a *signed* heartbeat to
  the staleness rule would treat every heartbeat as offline instead.

Where the reasoning lives: [implementation-plan.md](../../docs/mobile/implementation-plan.md)
(P0.2, P6.3, the critical-path note that keeps this task small, and P6.4's own row),
[functional-spec-reconciliation.md](../../docs/mobile/functional-spec-reconciliation.md)
(§3.1, §3.2, §3.5, D161, D162, D163, D167, D168 as amended, D169, D171, D173),
[reconciliation.md](../../docs/mobile/reconciliation.md) (D110, D113, D117, D118, D119),
and the tree's own key rule, `internal/budget/model.go`'s `Key`.
