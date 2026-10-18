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

## Presence, and the one mapping between two modules

`poll()` returns `presence: { state, verified, reason, ageMs, agents }`. A
heartbeat that is signed and fresh is `live`; stale and offline follow P1.4's
bands. A heartbeat that is **unsigned, wrongly signed or signed for another
node** is `offline` with `verified: false` and **no agent list** -- which is why
the tag covers `agents` too: an orchestrator that could announce itself without
a tag could announce itself as the agent the owner talks to.

**The mapping is named and tested.** P1.4's rule reads `{ at, bootId }` and
refuses any other field; P6.3's schema writes `{ at, boot, agents?, mac }`.
`shared/mailbox-schema/README.md` records the difference as a **conflict for the
owner** and names a caller-side mapping as one resolution, so this module
exports `toPresenceHeartbeat(heartbeat)` -- `boot` to `bootId` and nothing else
-- and tests both sides of it: P1.4 refuses the schema's own fields, and the
same heartbeat is readable through the mapping. When the owner rules, this
function is the one place that changes.

## Retry, reconnect, and D118's TTL

`send` keeps the record it wrote. `flush` (and `poll`, which flushes first)
re-writes the **same record**, id and `at` included, until the outbox answers or
the message is older than `MAILBOX_TTL_MS` (72 h, the plan's O5 number). The
re-send being verbatim is the whole point: P1.1's ledger answers the second
arrival as a **duplicate** -- and a re-stamped record would be an `id-conflict`
instead, which `client.test.js` demonstrates with both. A message still pending
at the TTL is dropped and reported (`expired`), never retried for ever.

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

## The audio stub (P3.4 is not merged)

P3.4 (`mobile/src/audio/`, the AudioIn/STT/TTS interfaces and their fakes, PR
#592) is **not merged**, and this task may not depend on unmerged code. So
`speaker.js` is a two-method seam of this task's own -- `speak(text)` returning
`{ ok: true }` or `{ id, code }` -- with `MemorySpeaker` as its double. What
P3.6 needs from it is exactly one thing: **whether the utterance was spoken**,
because the ack is written only after it was. Voices, queues, barge-in,
interruption and latency are P3.4's and P3.5's, and none of them is here. When
P3.4 merges, `speaker.js` is the file that goes, and its `MemorySpeaker` is what
the P3.4 fakes replace.

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

## The mutation proof

```sh
mobile/src/mailbox/mutation-proof.sh     # 14 guards, one planted bug each
```

Every mutation disables exactly one guard in `client.js` and must make
`client.test.js` fail. It finds a working node rather than assuming one (this
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
  (`!data.exists()` on a message, `data.parent().exists()` on the ack) and
  nothing else -- no auth, no durability, no listeners, no quota. The deployed
  rules are checks **81/82**, owner-run, and none of them is run here.
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
