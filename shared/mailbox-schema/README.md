# `mailbox-schema` — the message schema and its validation

The mobile plan's **Task P0.2**, in the shared pure-logic package [P0.1](../README.md)
built. It answers one question — **is this value a mailbox message?** — and nothing
else.

**The schema is the protocol.** P1.1's state machine validates against it, P3.6's app
writes it and P4.1's consumer reads it, so the three cannot disagree about what a
message is. The plan is explicit that this task sits on the **critical path** and must
stay small: the redactor is P1.3's, the dedupe table and the replay window are P1.1's,
the link rules are P0.3's, and *who* may write a message is P2.1's rules. Nothing of
theirs is built here.

```sh
cd shared && npm test          # this module's tests, with no install and no network
```

```js
import { validateMessage, isMessage } from './mailbox-schema/index.js';

validateMessage({ id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1700000000000 });
// => { ok: true, message: { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1700000000000 } }

validateMessage({ id: 'm.0001', sender: 'phone', text: 'hi', at: 1700000000000 });
// => { ok: false, code: 'bad-key', field: 'id' }
```

## The message

| Field | Type | Required | Bound | Decided by |
|---|---|---|---|---|
| `id` | string | yes | 1–100 code units, and a **database key** | D110's explicit message id: P1.1 dedupes on `(sender, id)` |
| `sender` | string | yes | 1–200 code units | the writer's identity; P1.1 does not dedupe across senders, P2.1 binds it to the writer's claim |
| `text` | string | yes | 1–4096 code units | D118's size bound per message; **already redacted** when it arrives (P1.3) |
| `at` | number | yes | epoch ms, a safe integer ≥ 0 | the tree's own time unit (`budget.Day`, the token's `fx` claim) |
| `acked` | boolean | no | — | D113's second phone write; **absent means not acked** |

A validated message comes back as a **frozen copy**: a fresh, plain object carrying
exactly these fields, with the caller's values **verbatim**. Nothing is trimmed,
floored, stringified or dropped — the schema never repairs a value, because two
agents disagreeing about what a stored message says is the failure at-least-once
delivery cannot survive. `acked` is added only when it was present, so "not acked" is
the absence of the field rather than a false one.

### Why the id is a database key and the sender is not

A message is stored under **its own id**, in whichever inbox or outbox node P2.1's
template chooses (the plan's own choice **C3**), so the id is a Realtime Database node
key. The rule applied to it is the tree's own, ported rather
than invented: `internal/budget`'s `Key`/`Unkey` treat a string as a valid key exactly
when it contains none of `%` (the escape `Unkey` parses), `.`, `$`, `#`, `[`, `]`, `/`
and no control character including DEL. An id the database cannot hold is not an id,
and it is refused as `bad-key` rather than escaped into one. `sender` is a **value**,
not a path segment, so a perfectly ordinary identity such as `owner@example.com` is
accepted; D113's example of the contrary shape is `agents/$slug/$run`, where the
identity *is* a key.

### Why there is no version field

**The field set is the version.** P2.1's subtree idiom denies `$other`, and this
validator refuses any field it does not name, so a message from a future protocol that
carries an extra field is refused by the rules *and* here rather than being silently
accepted with the new field dropped. That is fail-closed version skew without a second
number for two artifacts — the app, which ships on the stores' cadence, and the CLI —
to disagree about.

## Fail closed, and each refusal is named

Every refusal carries a **distinct code**, so a caller can tell a replay-shaped
problem from a malformed write instead of seeing "no message" for both:

| Code | What it refuses |
|---|---|
| `not-an-object` | `null`, an array, a primitive, a function |
| `unknown-field` | a field the schema does not name (`__proto__` from JSON included: it is an unknown field, not pollution) |
| `missing-field` | a required field that is absent |
| `wrong-type` | a field of the wrong type — **including an explicit `undefined`**, which is present-but-ambiguous |
| `empty-string` | an empty `id`, `sender` or `text` |
| `bad-key` | an `id` containing a character a database key may not contain |
| `too-long` | a string longer than its bound |
| `bad-character` | a control character (line breaks are allowed in `text`, not in `sender`), DEL, or an **unpaired surrogate** — half a character that JSON cannot encode |
| `bad-timestamp` | `at` is **a number but not a time**: a float, `NaN`, `Infinity`, `-1` |

The boundary between the last two rows is the type check, and it is worth stating
because a caller branching on the code must not be surprised: a **numeric string** such as
`'1700000000000'` is refused earlier, as `wrong-type` — it is not a number at all — and only
a value that *is* a number and still not a time reaches `bad-timestamp`.

There is no coercion anywhere: `'1700000000000'` is not a time, `'true'` is not an
ack, and a padded id is returned padded.

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
- **Not the URL rules.** A message carries text, not links. (P0.3)
- **Not the rules.** The schema is what P2.1's template must agree with — the same
  fields, the byte-for-byte key rule on the id, and `text`'s bound at `TEXT_MAX` — but
  who may write a message, and the ack's own fence, are the rules' business.
- **Not the node's retention.** D118's TTL and the owner's sweeper are properties of
  the mailbox node, not of a message.

## Evidence, and how to reproduce it

The module has **no dependency**: it imports nothing, not even a node builtin. The
tests are the plan's verification list (a missing id, a wrong type, an oversize body
and an unknown field refused; a well-formed message accepted) plus the fail-closed
families above.

**Every guard in `index.js` is mutation-proved**, not merely covered:

```sh
shared/mailbox-schema/mutation-proof.sh
# control       the pristine suite passes, 18 tests, 0 failures
# killed        control: refuse() always succeeds (must be killed)
# killed        not-an-object guard (null/array/primitive)
# ...                                                        (17 more)
# --- control killed; 19 guards: 19 killed, 0 survived, 0 not applied
```

The script disables one guard at a time — the object check, the unknown-field check,
each type check, each bound, each character rule, the surrogate rule, the timestamp
range, the ack's type check, the freeze and the copy — runs `npm test`, and requires
the suite to fail each time. It restores the file with `git checkout` after each one
and refuses to run against an uncommitted module.

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
it, which is why there are two.

## Two honest gaps

- **No CI job runs these tests yet.** P4.3 is that job and it is **owner-made** (the
  plan's Global Constraint 8: no agent edits `.github/workflows/`). Until it exists the
  tests run only where someone runs them — the same gap [the package's
  README](../README.md) records.
- **Nothing consumes the schema yet.** P1.1, P3.6 and P4.1 are unbuilt, and no task of
  the plan is approved; this module is not evidence that one is.

Where the reasoning lives: [implementation-plan.md](../../docs/mobile/implementation-plan.md)
(P0.2, and the critical-path note that keeps this task small),
[reconciliation.md](../../docs/mobile/reconciliation.md) (D110, D113, D118), and the
tree's own key rule, `internal/budget/model.go`'s `Key`.
