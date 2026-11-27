# `shared/front-agent` — the spec-shaping turn and the go

Task **P5.4.4** of [the mobile plan](../../docs/mobile/implementation-plan.md), **SECURITY-SENSITIVE**.
Its design of record is **P5.4.1** ([`docs/mobile/design/front-agent.md`](../../docs/mobile/design/front-agent.md)),
and it is decided by **D161** (an agent is a conversational endpoint the installation's consumer
advertises), **D163** (the default agent is the last used in that project) and **D128**'s
amendment of 2026-10-17 (a confirmation is its **own** utterance, recognised as the fixed phrase,
and one confirmation authorises exactly one action).

One pure function of a conversation and the utterance that just arrived — `turn` — plus a
read-only view of a stored conversation, `conversation`, and the sentence a screen speaks before
the go, `readBack`.

```js
import { turn, conversation, PHASE, OUTCOME } from './index.js';

const answer = turn({
  transcript,                 // every turn of the conversation so far, oldest first
  reply,                      // the agent's answer to the newest turn, or null
  now: clock(),               // the caller's clock: this module reads none of its own
  options: { mode: 'pro' },   // the mode the launch would run in, plus the session's policy
});

// ok: false -> a refusal (the table below)
// ok: true, outcome: OUTCOME.SHAPING -> the conversation stands at answer.phase
// ok: true, outcome: OUTCOME.GO      -> answer.action is the one typed launch it authorised

const view = conversation({ transcript, now: clock(), options: { mode: 'pro' } });
// view.phase, view.target, view.brief, view.readBack, view.deadline, view.expired
```

**The whole point of the module** is that a "go" is `fugaro run` authority: the front agent shapes
a **brief** — a file whose first line is `# Task: <title>` — and the go authorises exactly one
launch of it. A wrong go is a run nobody asked for, which is why the decision is here, pure, and
reviewed as a security path.

## The four phases

| Phase | What the conversation holds | The go |
|---|---|---|
| `idle` | nothing has been said yet | refused: no resolved target and no brief |
| `shaping` | a reply is still owed, or no brief yet, or no repository | refused, naming what is missing |
| `ready` | a brief **and** a repository, both live | **taken**: it authorises one launch |
| `spent` | the go was taken | refused: `replayed-go` |

A phase is a **summary of the transcript, derived on every call and never stored**. A go is judged
against the phase of the conversation *before* it, so an utterance cannot make its own
authorisation: the newest turn is read, and the phase rule is read from the turns before it.

## The go reuses P6.2's rules rather than writing a second set

That is the claim the task's reviewer is asked to test, and it is four imported things:

| Reused | From | What it decides here |
|---|---|---|
| the **phrase**, through `isConfirmation` on a routed utterance | P6.1 + P6.2 | what counts as the go: "Fugaro, confirm" and "Figaro, confirm." are it; a bare "confirm" is agent text (D164 forwards it, and a go the agent also received is not a confirmation) |
| `carriesConfirmToken` | P6.2's rule, **restated** because P6.2 keeps it private | `command-and-go`: one misrecognition must not satisfy both halves of D47's Rule |
| `CONFIRM_CUE` | P6.2 | the sentence the read-back ends with, so the words the phone speaks and the phrase the recogniser accepts cannot drift |
| `costlyStart`, `DEFAULT_COSTLY_MODES`, `DEFAULT_BIOMETRIC_ON_COSTLY` | P6.2's policy (D2786) | what the start costs, and whether it asks for the biometric — the session's answer, never this module's |

**What is *not* reused is P6.2's machine**, and the reason is worth stating rather than leaving to
be discovered: P6.2 reads back only the destructive verbs and lets a launch pass through (D162),
because a launch needs no confirmation at all. The front agent's launch *does* need one — the brief
is the thing authorised, and it is not a value a router resolved out of the utterance — so the
read-back names the brief and the repository as well as the mode, and the machine that holds it is
this one. The **rules** are P6.2's; the **sentence** is this module's, because the thing read back
is not the same thing.

## The transcript is the state, and the ledger

P6.2 keeps a list of outstanding read-backs and a ledger of spent ids, because its state outlives
the utterance and is swept by a clock. Here there is nothing to sweep: the conversation **is** the
transcript, every call is handed the whole of it, and a go that was spent is a turn that is still
in it. So a replayed go needs no ledger — the phase it left behind (`spent`) refuses the second
one, and a repeated turn id is refused (`replayed-turn`) while the transcript is read. A module
that kept a second copy of what the transcript already holds would be a second answer to the same
question, and the second answer is the one that goes wrong.

**Nothing is parsed out of the go.** The action record is built from the conversation: the
repository is the one that was read back, the mode is the session's, the brief is the one shaped.
The go supplies only its own two ids and its own time, and a go carrying a `target` of its own is
refused rather than believed.

## The order the checks run in

At a go: the router's answer, then a reply beside it (`unexpected-reply`), then the phase
(`replayed-go`), then the target, then the brief, then the mode, then the deadline. At a turn:
`missing-reply` before `conversation-closed`, so the first thing a caller learns is the missing
answer rather than the closed conversation. An **input** refusal — a transcript that is not a
transcript, a field of the wrong type, a `now` outside its window — comes before all of them,
because it is refused before the phase can matter.

## Refusals

`REFUSAL` is closed and every code is reached by a probe in `bounds.test.js`, which also accounts
for all of them between `INPUT_REFUSALS` and the transition table's cells. The conversational ones:

| Code | When |
|---|---|
| `not-understood` | a wake-word utterance the grammar does not know — P6.1 never forwards it, and neither does this |
| `not-agent-text` | an app command: another screen's, not this conversation's |
| `command-and-go` | one utterance carrying a command **and** a confirm token |
| `missing-reply` / `unexpected-reply` | a turn with no answer yet / a reply where none is answered |
| `conversation-closed` | a turn after the go |
| `replayed-go` | a second go, whatever ids it carries |
| `unresolved-target` / `no-brief` | a go with nothing read back to launch |
| `missing-field` (`mode`) | a go the session gave no mode for: a launch runs in a mode and none is minted here |
| `go-expired` | a go past `startedAt + ttlMs` — at the deadline it is still live |
| `bad-repo` / `bad-brief` | a repository that is not `owner/name`, a brief whose first line is not `# Task: <title>` |
| `replayed-turn` / `unsorted-transcript` | one utterance, one turn; the order is the conversation |

A refusal is `{ ok: false, code, field }`, frozen, and carries **no transcript** — so a caller
cannot render a refused utterance as an answered one.

## The numbers

| Bound | Value | Where it comes from |
|---|---|---|
| `DEFAULT_TTL_MS` | 72 h | **P6.5's mailbox TTL** (`PROMPT_TTL_MS`, O5), imported: a brief that has sat longer than a prompt would have is stale for the same reason |
| `MIN_TTL_MS` / `MAX_TTL_MS` | 1 s / 30 days | P1.1's floor and ceiling, through P6.5 |
| `ID_MAX` / `TEXT_MAX` / `REPO_MAX` | 100 / 4096 / 200 | **P6.3's** action record, which the go's authorisation must fit |
| `MODE_MAX` | 64 | P6.2's `MAX_MODE_LENGTH` |
| `TIME_MAX` | 2100-01-01 | `shared/lease`'s `MAX_TIMESTAMP`: two modules must not disagree about a time |
| `DEFAULT_MAX_TURNS` | 64 (1…256) | a bound, not a policy; `ttlMs`, `maxTurns` and `maxBrief` are all options a caller may move inside their bounds |

## Who consumes it

- **P5.4.5** reimplements the turn in Go, because the consumer cannot call into JavaScript, and
  holds itself to `vectors.json` (below).
- **P5.4.6** turns a taken go into `fugaro run` authority, through P6.7's re-validation of the
  action record.
- **P5.4.7** is the screen that renders `conversation(...)`'s view and speaks `readBack(...)`.

Nothing in this tree imports the module yet.

## Tests

```sh
cd shared && npm test                     # the package's tests (the glob picks this up)
node --test "front-agent/*.test.js"       # this module's alone
shared/front-agent/mutation-proof.sh      # the guards, mutation-proved
```

`index.test.js` holds the plan's cases: the phases and what each says, the read-back, the go's one
action record, each near-miss as agent text, an app command, a wake word the grammar does not know,
the replayed go, the expired go (and the deadline edge), the command-and-go, the unresolved target,
the withdrawn brief, the cleared target, purity, and a transition table **written by hand** —
never imported — compared cell for cell with `TRANSITIONS` and then driven through every cell with
literal transcripts. A source-level scan holds both files to no clock, no randomness, no network
and no `process`.

`bounds.test.js` sweeps every `REFUSAL` code against a probe, proves a getter is refused **without
ever being called**, holds every bound from both sides, and reaches the encoding branches — an id's
control characters, an id's or a text's lone surrogate, and the line separators U+2028 and U+2029 —
**each by its own value** rather than through another field's refusal. The separators are refused
where P6.3's readers refuse them, because the two charsets must not disagree and because the title
goes into the read-back verbatim.

`mutation-proof.sh` plants 36 one-line bugs in `turn.js` — each disabling exactly one guard — and
requires every one to make this module's suite fail. It refuses to report at all if it cannot find
a working runner, if the pristine suite is not green with at least one test actually run (an empty
glob exits 0, see [the package README](../README.md)), or if a control mutation goes uncaught. Its
**first two guards are the task's own red step** — the phase rule that refuses a second go, and the
deadline — so the red step stays re-runnable rather than living only in a pull-request transcript.

`vectors.json` carries 34 accept-and-refuse cases as **data** (the conversation, the clock it is
judged at, and the exact answer) for P5.4.5's Go parity test; the constants travel with them so the
Go side reads no second set. `vectors.generate.mjs` asserts every case against the live module
before it writes, so the file cannot be minted from a module that disagrees with a case, and
`vectors.test.js` holds the file to the module **and** to the plan's verification list, claim by
claim, from a table written by hand.

## Honest limits

- **The phrase is P6.2's, so its breadth is P6.2's too.** What counts as "confirm" is settled in
  `shared/command` and `shared/confirm` and is not re-litigated here.
- **The read-back's sentence is not the machine's.** P6.2's read-back is for a destructive verb
  out of the router; this one names a shaped brief the router never saw, so the two sentences are
  different by construction even though the cue and the phrase behind them are the same rule's.
- **`conversation` does not sweep.** It expires a deadline and says so (`expired`); nothing here
  deletes, prunes or rewrites a stored conversation.
- **The transcript is not a proof of authorship.** It is the fence's input, not a MAC: whoever
  reads a conversation back in must be the one that wrote it (P5.4.2's schema and P6.7's
  re-validation are where that is decided).
- **`shared/README.md` is not edited by this task**, and the module therefore has no line in the
  package's own contract yet. That file is P0.1's by the plan's shared-files table ("no level holds
  two writers of one file"), so the line belongs to a later sweep of P0.1's — recorded here rather
  than taken.
- **No test value here is a secret, and none is secret-shaped.**
