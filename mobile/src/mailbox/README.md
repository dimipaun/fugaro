# `mailbox/` — the app's mailbox client

The phone's end of the mailbox (mobile plan, task **P3.6**, decisions **D111**,
**D113**, **D113b**, **D118**; `docs/design/functional-spec-reconciliation.md`
§3.5). `createMailboxClient` is **the only place in the app that writes to the
database**, and this directory is where the app's write set is decided and
tested.

```js
import { createMailboxClient } from './index.js';

const client = createMailboxClient({
  account,                    // D114's tuple: { id, firebaseProjectId, authUid, fugaroProject }
  keys,                       // a SecretStore (the Keychain port)
  store,                      // { write(path, value), read(path) } -- the database, behind a port
  speaker,                    // { speak(text) } -- P3.4's seam (see "The audio stub" below)
  now,                        // D117's server-anchored now, in ms
  newId,                      // a fresh message id per call
  secrets,                    // P1.3's secret list (D118)
  heartbeatIntervalMs,        // D117's interval; P1.4's floor and ceiling apply
});

await client.send('run 42 finished');   // ONE signed inbox message, fresh id
await client.flush();                   // re-deliver what the outbox has not answered
await client.poll();                    // reads, verifies, speaks, acks, reports presence
client.pending();                       // the utterances not yet answered (D167's unsent text)
```

## The write set is exactly two paths (D113)

| Path | When | Rule |
| --- | --- | --- |
| `mailbox/<uid>/inbox/<id>` | one write per utterance | `!data.exists()`: never an overwrite, and a denied re-write is read as **landed**, not as a failure |
| `mailbox/<uid>/outbox/<id>/acked` | after the message was **spoken** | exactly `true`, and only for a message that is there (`data.parent().exists()`) |

`<uid>` is the **Firebase Auth uid** -- the rules' `$mb == auth.uid` -- and not
the installation id from D114's tuple. The client takes the tuple and uses
both: `authUid` addresses the mailbox and is the message `sender` (P2.1's
template holds `sender == $mb`), `id` addresses the Keychain item.

**There is no third path, and the heartbeat is not one of them** (D113b): the
heartbeat is the orchestrator's write and this client only reads it.
`client.test.js` asserts the whole write set of a mixed scenario against this
table, rather than asserting each call's path on its own.

**The claim is enforced, not only observed** (the PR #604 review asked for
this). `write-path.test.js` is a scan over the app's source in the style of
P3.1's `src/boundary.test.js`: no file but `client.js` may call the store's
`write`, no file but the entry may import a `firebase` module or name a path
through `.ref(`. **What the controls actually are** (this paragraph named them
wrongly until the review's last round): every rule is proved against a planted
line in real code and against the same line as prose, and the not-vacuous
control -- "the allow-list is not empty for a pattern that matches nothing" --
covers the FIRST rule only, where `client.js` really does call `.write(`.
The other two rules match nothing in the tree at all today, which is the
test's own finding and not a claim of coverage: their allow-list names
`index.js`, the entry that will wire the adapter (P3.2/P3.3), and the test
asserts that emptiness rather than pretending it is a control. A second write
path added later fails this file rather than review.

## What every message carries

`{ id, sender, text, at, mac }` -- and nothing else. The text is **redacted
before it is signed** (D118) and before anything is written, so a secret in the
utterance is in neither the message nor the tag. The tag is P6.3's:
`hmacSha256(key, canonicalBytes(record, direction))` as lowercase hex, over the
record without `mac` and without `acked`, with the **direction** (`inbox` here)
inside the covered bytes, so a message reflected from another node does not
verify.

**The key is never held by this directory's code.** It is read out of platform
secure storage through `SecretStore.use(accountId, 'macKey', fn)`, used inside
`fn` and never returned: `use` refuses a callback whose *result* carries the
material (`leaked`). No code path here has a variable holding the key, and the
read path is therefore the same one P3.1 already fenced. Nothing in this
directory prints anything (`console.` appears in no production file, and
`src/boundary.test.js` keeps it that way), and `client.test.js` asserts that no
write, report, refusal or pending entry contains the key.

**No unsigned write is ever ATTEMPTED.** With no key in the store (`no-key`) or
an unreadable one (`key-unreadable`), `send` refuses *before* the store is
called: the test asserts the store's write log is empty, not merely that the
write failed.

**A text nobody can sign is not a key problem** (the PR #604 review's finding).
`canonicalBytes` throws for the characters the two languages spell differently
-- U+2028 and U+2029, which Go's encoder escapes and JavaScript's does not, and
an unpaired surrogate -- so no tag can be computed for them at all. Inside the
key's callback that throw is indistinguishable from a Keychain that would not
open, and the owner would be sent to re-pair over a character in their own
sentence. `send` therefore asks `canonicalBytes` **before the key is touched**
and refuses with `unsignable` and the field it is about; the test asserts the
key was never asked for, nothing was written, and no id was recorded as used.
The neighbouring case is the schema's and keeps the schema's code: a control
character (U+0007, DEL) *is* signable, so the tag is made and
`validateMessage` refuses the record as `bad-character`, which is the reason
the sentence cannot be sent.

## What it verifies before it speaks

Every outbox message and the heartbeat are verified under the pairing's key
before a word is spoken or an ack written (the (b) tension's resolution):

* a message whose tag does not cover it -- a changed `text`, `at`, `kind`,
  `options`, `reply_to` or `agent` -- is **dropped**: not spoken, not acked;
* a message with no tag, or a tag that is not the schema's shape, is dropped
  with the **schema's own code**, because it is not a message;
* a node whose key and whose record's `id` disagree is dropped
  (`mismatched-id`): the template holds them equal, and a disagreement is the
  shape of a renamed write;
* a record whose `sender` is another mailbox is dropped (`wrong-sender`), which
  the phone checks itself rather than trusting the rules to have done it;
* an **unverified `reply_to` does not clear** the message being re-delivered:
  otherwise a forger could make the phone give up by naming the id.

`acked: true` is deliberately **outside** the signed bytes (`canonicalBytes`
omits it), so it is not a tamper case: the rules grant that child to this
mailbox's own writer, and the phone reads it as "I have already spoken this",
including after a restart, when it has no memory of its own.

## The double's read: a node named `__proto__` is a node

`store.js` is the thing every rule above is asserted against, so it has to
answer the way the database does, and one legal key is where plain assignment
quietly stops doing that. `__proto__` is a legal Realtime Database key --
`isDatabaseKey('__proto__')` accepts it and the schema's `keyField` does too,
and `paths.test.js` holds those two rules to each other -- and a listener hands
a subtree back with it as an **own** key, because the value it hands over is
what `JSON.parse` builds. An accumulator that assigned the child would instead
write *through* the key: one level up the node lands on `Object.prototype`,
polluting the whole test process and vanishing from the read; as a leaf it sets
the result's **prototype**, so the map has no own keys, `Object.entries` sees no
message, and the phone would never speak one the database had returned. The
double now defines every child (`own()` in `store.js`) and replaces a slot that
does not hold a container, which is what the database does when a write puts a
child under a scalar. Two tests in `store.test.js` and the consequence test in
`client.test.js` (a message in a `__proto__` node is read, spoken and acked),
with three mutations in the proof.

## Presence, and the one mapping between two modules

`poll()` returns `presence: { state, verified, reason, ageMs, agents }`. A
heartbeat that is signed and fresh is `live`; stale and offline follow P1.4's
bands. A heartbeat that is **unsigned, wrongly signed or signed for another
node** is `offline` with `verified: false` and **no agent list** -- which is why
the tag covers `agents` too: an orchestrator that could announce itself without
a tag could announce itself as the agent the owner talks to.

Every `reason` is either P1.4's own or one of `PRESENCE_REASON`, and **P1.4's
refusal codes are neither** -- a caller that switches on `reason` never meets a
string the two lists do not contain. That is not a formality: `at: 0` is
schema-legal (`timestampField` refuses a *negative* timestamp and not zero) and
P1.4 refuses it as the shape of an unset counter, so a heartbeat this client has
validated and verified can still be one P1.4 will not judge. It is `offline`
(never `live`, and `verified: true`, because the tag is the orchestrator's) with
the reason `heartbeat-refused`. The last review round found the branch doing
that commented "unreachable" and handing out P1.4's `state.code`.

**The mapping is named and tested.** P1.4's rule reads `{ at, bootId }` and
refuses any other field; P6.3's schema writes `{ at, boot, agents?, mac }`.
`shared/mailbox-schema/README.md` records the difference as a **conflict for the
owner** and names a caller-side mapping as one resolution, so this module
exports `toPresenceHeartbeat(heartbeat)` -- `boot` to `bootId` and nothing else
-- and tests both sides of it: P1.4 refuses the schema's own fields, and the
same heartbeat is readable through the mapping. When the owner rules, this
function is the one place that changes.

## One pass at a time

`send`, `flush` and `poll` run on one queue, in call order, and never overlap.
The reason is the ack's own rule: the ack is written only **after** the speak, so
a second pass that starts while the first is still running reads the same
message unacked and would speak it again -- the owner hears the sentence twice,
which is the thing the order exists to prevent. A screen that polls on a timer
and again on a reconnect does that without meaning to. The queue is the client's
whole concurrency control and it carries no state of its own: a refusal comes
back to its own caller and leaves the queue settled.

## Retry, reconnect, and D118's TTL

`send` keeps the record it wrote. `flush` (and `poll`, which flushes first)
re-writes the **same record**, id and `at` included, until the outbox answers or
the message is older than `MAILBOX_TTL_MS` (72 h, the plan's O5 number). The
re-send being verbatim is the whole point: P1.1's ledger answers the second
arrival as a **duplicate** -- and a re-stamped record would be an `id-conflict`
instead, which `client.test.js` demonstrates with both. A message still pending
at the TTL is dropped and reported (`expired`), never retried for ever.

**The phone's 72 h is not the consumer's window** (corrected in the review's
last round, where this file and `client.js` both claimed the two were the same
number). `MAILBOX_TTL_MS` is D118's retention TTL and the phone's own bound on
how long it keeps trying. What the consumer **accepts** is P1.1's per-kind
window (D173): a prompt inside the prompt TTL (72 h), a command inside
`windowMs` -- the **6-hour replay window**, P1.1's own default. The records this
client writes carry no `kind`, so the consumer reads them as commands: a retry
made ten hours after the utterance is still attempted by the phone and refused
at the other end as a **replay**. The phone cannot know the consumer's window
(P1.1 takes it per state, and the phone is not its reader), which is why the
number here is the mailbox's and not the consumer's -- and why the test asserts
both numbers against their own modules rather than one window against the other.

**The id source's contract, stated because the client cannot enforce it across
runs.** Within one run an id that has already been minted is refused
(`id-conflict`) before anything is signed. Across runs (a restart, a second
client) the client cannot tell a re-used id from a lost acknowledgement: the
inbox is not readable by the phone, and the rules deny the re-write either way.
The write is then reported as landed and **the first message stands** -- so a
fresh id per utterance across restarts is the id source's job, and an id source
that repeats one silently loses the second utterance. That is a real limit, not
a footnote: the app's id source is D117/P3.2's, and this is the contract it must
meet.

## Two known limits, named rather than left to be found

**The phone does not check how old a message in its outbox is.** A message that
verifies under the pairing's key is spoken whenever it arrives, whatever its
`at` says -- a correctly signed outbox record stamped a year ago is read out
(probed, not assumed, in the PR #604 review). The client does not drop it, and
that is a decision rather than an oversight: `at` is D117's server-anchored
time; P1.1's ledger is the authority on what is too old for the consumer to act
on, and its window is per-kind (D173: `windowMs`, 6 h by default, for a
command; the prompt TTL for a prompt); and the mailbox's own retention is
D118's 72 h, which is `MAILBOX_TTL_MS` and this phone's retry bound rather than
anybody's acceptance window (the number this file and `client.js` both
mis-stated until the review's last round), so an age rule here would be a
number this module has no authority for: the phone reads what the consumer
wrote, and nothing in the mailbox says which window that record's own kind is
under. **What a replay actually requires is the consumer's identity**:
P2.1's template lets only `CONSUMER` write `outbox`, and only with
`!data.exists()`, so a message cannot be planted or re-planted there by anyone
who can read the phone's mailbox. If the owner wants the age rule on this side
as well, it is a small change (drop an outbox message older than
`MAILBOX_TTL_MS`) and it belongs to whoever owns D118's window next.

**`acked` is outside the signed bytes** (P6.3/D171: `canonicalBytes` omits
`mac` and `acked`, and the phone reads `acked: true` as "I have already spoken
this"). The rules grant that one boolean child to the mailbox's own writer, so
anyone who can mint a token for this uid -- the launcher, which by D114's design
holds the same operator identity -- can pre-ack a genuine message and the phone
will never speak it. **That is a limit of the design, and it is the shallow one
on purpose**: pre-acking *hides* a message; it cannot make the phone act, speak
a sentence of the attacker's, or make the consumer do anything, because every
record the phone reads is verified under the pairing's key and every write it
makes is signed by it. The alternative -- putting `acked` inside the tag -- is
not available: the ack is written after the tag was made, and a launcher that
could not ack could not release a prompt either.

## The audio stub (P3.4 has merged; this client is not wired to it yet)

P3.4 (`mobile/src/audio/`, the AudioIn/STT/TTS interfaces and their fakes, PR
#592) landed after this task: it had no directory in the tree when P3.6 was
written, and this task may not depend on code that is not on `main`. **It has
since merged with this change** -- `mobile/src/audio/` is the tree's now -- and
this client still takes this module's own port rather than P3.4's, because
nothing has wired the two together. So `speaker.js` is a one-method seam of this
task's own -- `speak(text)` returning
`{ ok: true }`, or `{ ok: false, code, field }` with a code from
`SPEAK_CODES` (`bad-text`, `speak-failed`) -- with `MemorySpeaker` as its
double. (The refusal shape was written as `{ id, code }` here until the
review's last round; that is the shape of the client's own `unspoken` list,
which names the message the refusal is about, not the port's.) What
P3.6 needs from it is exactly one thing: **whether the utterance was spoken**,
because the ack is written only after it was. Voices, queues, barge-in,
interruption and latency are P3.4's and P3.5's, and none of them is here.
`speaker.js` is the file that goes, and its `MemorySpeaker` is what the P3.4
fakes replace; the merge has happened and the wiring has not.

The **secure store is not stubbed**: P3.1 is merged and its `SecretStore` is
reused, with one addition (below).

## The one file outside this directory this task touched

`mobile/src/accounts/secure-store.js` gained `SecretStore.use(accountId, item,
fn)`. The reason is the boundary fence rather than convenience: `reveal(` is
allowed in exactly two files (`secret.js` and `secure-store.js`), so this
directory **cannot** read the MAC key itself, and widening that allow-list to a
file that signs messages would be the wrong trade. `use` is the narrow
alternative -- the material is handed over for the duration of one callback and
a result that carries it is refused (`leaked`), with seven tests of its own.
P3.6 is the task that needed it; the PR says so.

**What that fence counts as a leak was fixed in the last review round.** It
searched every result for the material as a substring, and the value this
directory's callback returns is the 64-hex tag it made *with* the key -- so a
short key's characters turn up inside it by chance and every send was refused
as `leaked`, which `keyRefusal` reports to the owner as `key-unreadable`. The
fence now searches for material of at least 16 characters anywhere and, below
that, only for an occurrence that stands on its own rather than inside a longer
word: a coincidence is always inside the run the value is made of. Both halves
are tested there, and the coincidence case again in `client.test.js`, where the
cost of the false refusal is a phone that cannot speak.

## The mutation proof

```sh
mobile/src/mailbox/mutation-proof.sh     # 34 guards, one planted bug each
```

Every mutation disables exactly one guard and must make that guard's own suite
fail: `client.js` and `client.test.js` for most of them, and -- since the fix
round for PR #604 -- `mac.js` with `mac.test.js` (the tag comparison),
`store.js` with `store.test.js` and, for the read's own-key rule, with
`client.test.js` (its consequence for the outbox), and `src/app/root.js` with
`write-path.test.js` (the one-write-path scan), each mutation naming its file
and its suite. It finds a working node rather than assuming one (this
repository's base image keeps node off PATH), runs a positive control (the
pristine suite passes, over tests that ran) and a negative control (a `refuse()`
that always succeeds must be caught) before any verdict is trusted, and asserts
each pattern matched exactly once so a stale script is `APPLY-FAILED` rather
than a claim.

| Mutation | Went red |
| --- | --- |
| `refuse()` always succeeds (negative control) | most of the file |
| the fresh-id guard drops (`minted.has(id)`) | `an id that has already been used is not used twice` |
| the tag is made for `outbox` instead of `inbox` | `the message it writes is exactly the five fields, tagged over the schema's bytes` |
| the tag is a placeholder `'f'.repeat(64)` | the same test |
| the text is signed **before** redaction | `the tag is over the REDACTED text` |
| the message is written to the outbox path | `one utterance is one message, with a fresh id each time` |
| `exists` is treated as a failure in `send` | `a send whose message is already there is landed, even after a restart` |
| `flush` re-stamps the record with `now` | `a write that does not land is kept, reported, and re-sent byte for byte` |
| D118's TTL check drops | `a message still pending at D118's TTL is dropped and reported` |
| the outbox tag is not verified | the six tamper cases and `a tag made for another node` |
| the heartbeat tag is not verified | `an unsigned heartbeat ... is offline with no agents` |
| the sender is not checked | `a record whose key and id disagree, or whose sender is another mailbox` |
| a failed speak still acks (the `continue` drops) | `it acks nothing it has not spoken` |
| the `spoken` set stops suppressing a second speak | `an ack that did not land is retried, and the message is not spoken a second time` |
| the `acked === true` guard drops | `a message this phone acked in an earlier run is not spoken again after a restart` |
| the queue is bypassed (`serial` calls the pass directly) | `two polls in flight together speak the message once, not twice` |
| the pre-key `unsignable` check drops | `a text nobody can sign is refused as unsignable, and never as key-unreadable` |
| the schema validation before the write drops | `a control character is refused with the schema's reason, and nothing is written` |
| a `raw` write function is added to the client object | `the client object is exactly its six names, and nothing else is on it` |
| `sameTag` compares only the first eight hex characters (in `mac.js`) | `a tag that differs only past the eighth character is refused too` |
| a read's node is assigned instead of defined (in `store.js`) | `a node named `__proto__` is an own key of the read, and pollutes nothing` -- and, run against `client.test.js`, `a message in a node named `__proto__` is read, spoken and acked like any other` |
| an intermediate container is assigned instead of defined (in `store.js`) | the same test (the pollution half of it) |
| a second write path is planted in `src/app/root.js` | the three rules of `write-path.test.js` |
| `flush`'s clock refusal drops (last review round) | `a clock that is not a time is refused by flush and by poll, and nothing is done first` |
| `poll`'s clock refusal drops | the same test |
| `poll`'s key refusal drops (`!verdicts.ok`) | `poll without a usable key is refused before it speaks or acks` |
| the `exists`-as-landed guard drops in `flushMutating` | `a message that is already there is a landed write, not a failure` (its planted-value half is what shows the re-write landed) |
| the read's `validateMessage` drops (a record the schema refuses is verified anyway) | `a message with no tag, and a tag of the wrong shape, are dropped` |
| the heartbeat mapping's field guard drops | `the one mapping between the schema's heartbeat and P1.4's rule is named and tested` |
| a P1.4 refusal is handed out as the reason (`state.code`) | `a signed heartbeat P1.4 will not judge is offline for this module's own reason` |
| the double's ack is classified by suffix alone (in `store.js`) | `only a message's own `acked` child is an ack: an id of `acked` is a message` -- and, run against `client.test.js`, `a message whose id is `acked` is a message in both directions` |
| the leak fence treats any occurrence as a leak (in `secure-store.js`) | `the fence catches the material coming out, not its characters coinciding` -- and, run against `client.test.js`, `a pairing key short enough to turn up inside its own tag still speaks` |

Two of those mutations **survived** when the proof was first run, and both were
fixed rather than argued away -- the two guards had no test that could tell them
from their absence:

* `exists`-as-landed in `send`: the case only arises after a **restart** (a
  fresh client with an empty `minted` set re-asking for an id whose write
  landed, which the rules then deny). That test now exists. Within one run the
  `minted` guard makes it unreachable, which is why the first pass of tests
  missed it;
* the `spoken` set: a message is only re-spoken in the same run when the ack
  write **failed**, since the ack is otherwise the `acked` flag the next poll
  reads. That test now exists too, and it is the one that says why the set is
  there at all -- the owner must not hear the same sentence twice.

## What is not verified here

* **No real database and no deployed rules.** Everything runs against the port's
  double (`store.js`), which models the two rules that a re-send meets
  (`!data.exists()` on a message, `data.parent().exists()` on the ack, at the
  one position the template has that rule -- `outbox/$msg/acked`, so an id of
  `acked` is an ordinary message) and
  nothing else -- no auth, no durability, no listeners, no quota. The deployed
  rules are checks **81/82**, owner-run, and none of them is run here.
* **No deployment, and the template's allow-list is newer than any deployed
  one.** This client signs every message because the plan's P3.6 entry requires
  it (P6.3 and P6.4 "require `mac` on every inbox message, so P3.6 must
  sign"), and `shared/mailbox-schema` is the merged authority for the record's
  shape. The template in `internal/budget/rules/rules.json.tmpl` now agrees
  with it: P6.4 merged on 2026-10-18 and its inbox and outbox message nodes
  require `id`, `sender`, `text`, `at` **and `mac`**, and carry `kind`,
  `agent`, `options`, `reply_to` and the action record with `$other: false`.
  What is *not* true is that anything is running it -- the template is a
  template, and the deployed rules are checks **81/82**, owner-run and not run
  here -- so a reader pointing this client at a project whose rules predate
  P6.4 would still see the write refused. That is a sequencing fact about a
  deployment, not a defect in this module. `store.js`'s double deliberately
  does not model a field allow-list either: the shape is the schema's rule,
  and P6.3's tests hold it.
* **The Realtime Database adapter does not exist yet.** `store.js` is the port
  and its double; the SDK adapter is `mobile/index.js`'s wiring (P3.2's and
  P6.10's territory), and there is no test for it because there is no file.
* **Metro, and the `shared/` imports.** This directory imports
  `../../../shared/mailbox-schema/index.js`, `../../../shared/presence/index.js`
  and `../../../shared/digest/redact.js` -- the app's own modules and the shared
  package, which `src/boundary.test.js`'s D119 rule allows. Node resolves them
  (the tests run), but **bundling the app across `mobile/` and `shared/` needs a
  Metro `watchFolders` entry that no task has added**: nothing here has been
  through `expo start`, and that wiring is unverified.
* **The simulator (P1.5) is not used as this client's other end.** P1.5 signs
  nothing (`PLACEHOLDER_MAC`) and sets no `reply_to` (`replyId(id) = 'r-' + id`),
  so it can neither produce a message this client will speak nor answer one it
  sent. Its ledger is used where it is the right authority -- P1.1's `step`, in
  the reconnect test -- and the outbox fixtures here are signed by this
  directory's own `macOf`, which is P6.3's implementation and vector-tested.
  **A message this client sends is therefore never answered end to end in CI**:
  the "clears the pending message" path is tested with a locally signed `answer`
  record, not with the consumer.
* **P3.4's audio and P3.5's voice screen are not here.** `speaker.js` is the
  stub described above -- the interface P3.6 needs and no more. Nothing in this
  directory records audio, transcribes, or decides when to speak.
* **The rendering.** `pending()` is D167's unsent text for a screen to show;
  the text screen (P6.9) and the offline gate (P6.10) are later tasks, and
  nothing here draws anything.
* **The key's own handling on a device.** That `expo-secure-store` returns the
  item, that the Keychain is locked when the app is backgrounded, and that a
  read fails then, are P3.1's port and P3.1's tests; here the key arrives
  through a `SecretStore`, and the "never logged, never in plain storage" claim
  is P3.1's, re-asserted at this seam (no write, report, refusal or pending
  entry carries it).
