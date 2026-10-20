# `text/` — the text screen

The phone's text fallback (mobile plan, task **P6.9**; decisions **D167**,
**D164**, **D169**, **D162**, **D170**;
`docs/design/functional-spec-reconciliation.md` §3.4 and §3.5). D167's second
half is this directory in one sentence: *"a text screen listing pending prompts
and recent summaries per installation, accepting typed input and typed answers
— the same pipeline with a keyboard instead of STT."*

```js
import { createTypedInput, createTextScreen } from './index.js';

const typed = createTypedInput({
  mailbox,            // P3.6's client -- the SAME one the voice path is built on
  now,                // D117's server-anchored now, in ms
  newId,              // the ids the confirmation machine spends
  router: { aliases, projects, agents },   // P6.1's configuration
  confirm: { timeoutMs },                  // P6.2's, if the defaults are wrong
});

await typed.submit('ship the release notes');    // routed, then sent by the client
await typed.propose({ text: 'cancel run 42', verb: 'cancel', target: runTarget });
await typed.submit('Fugaro, confirm', { actionId });   // the typed phrase
typed.awaiting();                                // the read-backs still live

const TextScreen = createTextScreen({ React, View, Text, TextInput });
```

## It is the same pipeline, not a second one

D167's word is "same", and it is meant literally. Nothing in this directory
routes, signs, writes, times or decides anything for itself:

| The half of the pipeline | Whose it is | How it is reached |
| --- | --- | --- |
| routing an utterance | P6.1, `shared/command` | `routeUtterance` with the same aliases, projects and agents the voice path passes |
| sending agent text | P3.6, `src/mailbox` | `client.send` — the app's **one** write path, one signed inbox message per utterance |
| confirming a destructive command | P6.2, `shared/confirm` | `step` with the same `command`, `confirm` and `abort` events |
| the waiting questions | P6.5, `shared/prompts` | the caller's queue; the screen draws `pending`'s listing entries |

Every refusal a caller sees is the code of the module that refused — P6.1's
`not-understood` sentence, P3.6's `write-failed`, P6.2's `confirmation-expired`
— with `INPUT_CODES`' four additions for the faults that are this file's own
(`bad-text`, `bad-clock`, `unknown-route`, `no-action-write`). One fault, one
code, wherever it is found.

## The three routes, and what a typed line does with each

| P6.1's route | What happens | What comes back |
| --- | --- | --- |
| `agent-text` | the utterance goes to `client.send`, verbatim | `sent` with the client's id and `at` |
| `not-understood` | **nothing is sent** (D164's fail-closed rule) | `not-understood` with P6.1's own sentence |
| `app-command` | `confirm` and `cancel-that` are stepped through P6.2; `what's pending` is answered by the screen; the other four belong to P6.8's switcher and P3.3's list | `confirmed`, `discarded` / `nothing-pending`, `pending`, `elsewhere` |

A bare **"confirm"** is agent text and is sent to the agent: there is
deliberately no string-level predicate for a confirmation (P6.1 says so, and
D164 is why), so the fixed phrase — `Fugaro, confirm`, routed like anything
else — is the only spelling that reaches the machine.

**The cue is swapped where the sentence is built, not where it is drawn.** P6.2
builds one sentence *and* one spoken cue: every read-back it hands out ends
`… Say "Fugaro, confirm" to confirm.` (`shared/confirm`'s `CONFIRM_CUE`), which
is right for the spoken path and wrong for a keyboard. So `typedReadBack` — used
by `propose` and by `awaiting`, and exported for a caller that draws the same
read-back somewhere else — replaces that final instruction with `TYPE_CUE`
(`Type "Fugaro, confirm" to confirm.`) and touches nothing else: the sentence
naming the resolved target is P6.2's and survives character for character, and a
read-back that does not end in `CONFIRM_CUE` is returned **unmodified**, because
the only safe edit is one that provably touches nothing but the instruction. The
screen then draws **one** line, verbatim. Drawing P6.2's read-back on the screen
and putting `TYPE_CUE` under it — which this module did in its first round — put
a "Say" instruction and a "Type" instruction on the same screen, on the screen
that exists because the user cannot speak to it; that is the contradiction the
swap removes, and `screen.test.js` now pins the absence of it.

**The phrase needs no action id from the caller.** `submit('Fugaro, confirm')`
with no context confirms the one live read-back — the one the screen is drawing
— taking its id and re-stating its target; `submit('Fugaro, confirm', {
actionId, target })` is for the case where the app is holding a read-back past
its deadline or re-reading a merge's head, and the machine then judges the
deadline and the target. Two read-backs outstanding is the machine's
`ambiguous-pending`, never a guess between them.

**A wake word P6.2 cannot confirm with is refused as a wiring failure.** The
machine decides whether an utterance is the fixed phrase by routing it with the
router's *default* aliases (`isConfirmPhrase`), while this screen routes with
the caller's, so `aliases: ['Hey']` would route `Hey, confirm` as the command
and then have the machine refuse it `not-a-confirmation` — every command but
the one that authorises an action. That is fail-closed, and it is refused at
construction (`no configured wake word can confirm`) rather than discovered by
a person mid-action.

## The destructive target is the app's resolution, not a parse

`propose({ text, verb, target, actionId })` takes an action the **app** has
resolved — the run id it looked up, the repository, PR number and head it read.
That is the merged tree's own shape and not a shortcut:

* P6.1's grammar has **no destructive rows at all**, deliberately (its README:
  "free text cannot reach one"), so "Fugaro, cancel run 42" is
  `not-understood` and there is no typed spelling of a cancel to match;
* P6.2's machine takes the resolved target from its **caller** and builds the
  read-back from it and from nothing else, which is what makes the read-back
  name the run id that was resolved and never the transcript;
* nothing in the merged tree resolves a verb and a target from an utterance,
  and inventing one here would be exactly the paraphrase the read-back is
  forbidden to be.

The **typed** half of D169's rule is the phrase, and the phrase is typed here:
the read-back is drawn on the screen, the human types `Fugaro, confirm`, and
the machine — not this module — decides whether the phrase confirms anything,
whether it is inside its timeout, whether the target still matches and whether
the action id was already spent.

`actionId` is minted from `newId` for P6.2's destructive verbs (its exported
`DESTRUCTIVE_VERBS`, imported rather than respelled) and for nothing else: a
destructive command without an action id is `missing-field` to the machine,
while a **launch** carrying one is `verb-target-mismatch`, because a launch is
not read back at all. D162's launch therefore passes straight through and goes
to the agent as text, through the same client.

## What a confirmed action does NOT do yet, named

`propose` → typed `Fugaro, confirm` produces P6.3's **action record**, and the
record is **returned, not delivered**: every `confirmed` answer carries
`delivered: false` and `code: 'no-action-write'`.

The reason is a fact about the merged tree. The phone's one write path is
`src/mailbox/client.js`, and the client it ships has exactly one send —
`send(text)` — which writes `{ id, sender, text, at, mac }`: it has no way to
carry a `kind` and an `action`. Sending a destructive command as its own text
would be precisely what D169 forbids ("typed action records … not text to the
agent"), so this module writes nothing rather than writing the wrong thing.
`src/mailbox/` is P3.6's and P6.10's file — the plan's shared-files table gives
it to those two tasks and this one is forbidden to fork it — and **P6.10's own
verification ("an action record is never queued") is the clause that needs the
send this client does not have.** Until it lands, a confirmed action is a value
the app holds and can act on, and the screen shows it as confirmed with the
reason it was not sent. `input.test.js` asserts the gap rather than leaving it
to prose: a confirmation writes nothing at all to the store.

## The screen

`createTextScreen({ React, View, Text, TextInput })` is a **function of
react-native**, like `src/app/root.js` and for the same reason: the Expo
toolchain is not installed in this environment and no test here may need a
device (Global Constraint 10). `screen.test.js` walks the tree against a fake
React, which is also how "the screen draws no secret" stays a fact in CI.

`confirmation` is `{ readBack, deadline }` as `awaiting()` and `propose()`
return it, and it is drawn **verbatim and once**: the read-back already ends in
the typed cue (see above), so the screen adds no second instruction under it.

It takes `{ installations, confirmation, unsent, notice, draft, onChangeText }`
and draws, per installation, its id (D114's label), its waiting questions in
P6.5's listing order with the destructive ones marked, and its recent summaries
newest first and capped at `RECENT_SUMMARY_LIMIT`. **Per installation and never
summed** (D170): nothing counts across sections, and `screen.test.js` asserts
that no total appears on screen. P6.5's `stage` (the reminder escalation) is
**not** drawn — nothing here reads it, and the header says so rather than
implying it.

The unsent text from `client.pending()` is drawn too — D167's first half is
that it is *shown* rather than queued by the SDK, so a user who typed into a
dead connection can see it is still theirs — but **only `landed: false`**:
D167(a) and P3.6's `pending()` reserve that word for text the database has not
received, and drawing a landed message the same way would invite the owner to
retype a sentence the consumer is already acting on.

The row guards are the same shape as `recentSummaries`': an entry that is not a
P6.5 listing entry with a usable prompt is **dropped**, never drawn — a
malformed value would otherwise be a render crash (`null.prompt`) on the very
screen that exists for when the voice path is not working. A malformed
installation is skipped for the same reason.

The component is pure and holds no state: the draft and its handler are the
caller's, and there is no element in the tree that could write anything.

**What is not here.** No answer buttons and no launch button: matching a typed
line to the question it answers ("the active installation's oldest question",
§3.4) is the caller's, because it is state this screen does not hold.
