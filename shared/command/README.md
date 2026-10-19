# `shared/command` — the wake-word router and the app-command grammar

Task **P6.1** of [the mobile plan](../../docs/mobile/implementation-plan.md), decided by **D164**
(wake-word routing), **D161** (an agent is an endpoint the consumer advertises) and **D163** (the
default agent). Two pure functions over values — `routeUtterance` and `defaultAgent` — plus
`isConfirmation`. Nothing here reads a clock, a file, a database, a network or a random source,
and the module imports nothing at all: the inputs are the whole input, which is what D126 asks of
everything that can be a pure function.

```js
import { routeUtterance, defaultAgent, isConfirmation, ROUTE } from './index.js';

const result = routeUtterance({
  utterance: transcript,                       // verbatim, from the on-device recogniser
  projects: [{ id: 'belong', spokenName: 'Belong' }],
  agents:   [{ id: 'orchestrator', spokenName: 'orchestrator' }],
  // aliases: WAKE_ALIASES                     // the default, when omitted
});

// ok: false -> a refusal (see the table below)
// ok: true  -> one of exactly three routes
switch (result.route) {
  case ROUTE.APP_COMMAND:     /* result.command, plus result.project | result.agent | result.list */ break;
  case ROUTE.AGENT_TEXT:      /* result.text, verbatim: hand it to the agent, unread */             break;
  case ROUTE.NOT_UNDERSTOOD:  /* result.reply, a fixed sentence; NEVER forward this one */          break;
}
```

## The rule, and the half of it that is load-bearing

| The utterance | The route | Forwarded? |
|---|---|---|
| its **first token** is a wake-word alias, and the rest matches a command row | `app-command` | no |
| its **first token** is a wake-word alias, and the rest matches nothing | `not-understood` | **never** |
| anything else — including a bare `confirm` | `agent-text` | yes, verbatim |

**"Never forwarded" is the load-bearing half.** `Fugaro` is not a dictionary word, so an
on-device engine (D44, D46, D123 — local engines only) will hear `Figaro`, `Fugar oh`, or drop it
altogether. The spec's claim that the wake word "never misroutes" is therefore not true of a
transcript, and G5 of [the reconciliation](../../docs/mobile/functional-spec-reconciliation.md)
records it. The owner's answer in D164 is **fail closed**: an utterance that begins with a wake
word has exactly two possible answers here, and forwarding is not one of them, because a misheard
app command must not become agent input. The other direction cannot be fixed here — a missed wake
word makes an app command into agent text — and the measured rate of that is **check 102**, which
is owner-run.

## The grammar is closed, small, and data

Seven command kinds: the five §3.4 commands plus D169's two. Every accepted spelling is a row in
one frozen table, and a row is a list of tokens with at most one name slot, always last:

| Command | Spoken | Name slot |
|---|---|---|
| `switch-project` | "Fugaro, switch to ⟨name⟩" | a **project**, matched only against `projects` |
| `talk-to-agent` | "Fugaro, talk to ⟨name⟩" | an **agent**, matched only against `agents` |
| `where-am-i` | "Fugaro, what project am I on" | — |
| `list` | "Fugaro, list projects" / "Fugaro, list agents" | — (`result.list` is the closed pair) |
| `whats-pending` | "Fugaro, what's pending" / "Fugaro, what’s pending" / "Fugaro, whats pending" | — |
| `confirm` | "Fugaro, confirm" | — |
| `cancel-that` | "Fugaro, cancel that" | — |

There is **no intent detection, no prefix match, no scoring and no "did you mean"**. A row either
equals the utterance's tokens (after the wake word) or it does not. That is why:

- **`what's pending` has three rows**: an on-device engine renders the apostrophe as `'` or `’`,
  or drops it, and that is the only spelling variance the grammar admits.
- **Words are folded by case only.** An utterance and every configured name and alias are split
  on whitespace and on the clause marks `, ; : … – —`, and punctuation (Unicode
  category P) is stripped from both ends of each word, so "Fugaro: confirm", "Fugaro,confirm" and
  a quoted transcript all begin with the wake word. Internal punctuation stays (`con-firm` is not
  `confirm`), and letters are never folded beyond lowercase: no accent stripping, no compatibility
  normalisation, no look-alike mapping. A look-alike wake word (`Fug\u0430ro`, with a Cyrillic `a`) is
  therefore a missed wake word — agent text, D164's other direction — and never a confirmation.
- **A leading filler is a missed wake word.** "Um, Fugaro, confirm" is agent text under the
  first-token rule; stripping fillers would widen what may precede the phrase.
- **A name is matched against the configured list only**, exactly, never as a prefix: `Belong`
  and `Belong dev` are two different names, and `Bel` matches neither. A name that is not
  configured is `not-understood`, with the name echoed in its folded form — never the raw
  transcript — and it is still not forwarded.
- **A destructive verb is not in this grammar.** "Fugaro, delete the docs project" is
  `not-understood`. Destructive commands are D173's windows and the confirmation flow (P6.2,
  P6.7), and free text cannot reach one.

## What counts as a confirmation

This is the module's security-relevant decision, so it is stated rather than left to be read off
the code.

- The phrase is **`confirm`**, and it is recognised **only as an app command** — which under D164's
  first-token rule means it carries the wake word: **"Fugaro, confirm"**. A bare `confirm` is
  agent text. This is a consequence of D164 rather than a choice made here, and it is the reason
  there is no string-level `isConfirmation('confirm')`: the only way to ask the question is to
  route the utterance first, so a confirmation that was never routed cannot be manufactured.
- The phrase must be the **whole** command. "Fugaro, confirm it" is `not-understood`, not a
  confirmation.
- Every near miss is agent text: `yes`, `yeah`, `yep`, `ok`, `okay`, `sure`, `yes please`,
  `yes, merge it`, `merge it`, `go ahead`, `do it`, `approve`, `approved`, `confirmed`,
  `confirmed it`, `conform`. The list is exported as `NOT_CONFIRMATIONS` so the test that pins
  "none of these confirms" reads as the list G10 and P6.2 name, rather than as a table nobody can
  audit. **The module has no branch on any of them**: they are refused by the grammar's closure,
  not by a blocklist, and the list exists to be tested against.
- `isConfirmation(result)` takes a **result**, not a string, and requires all of: `ok === true`,
  `route === 'app-command'`, `command === 'confirm'`, `forwarded === false`. Each half is
  load-bearing — the command kind alone would make `cancel that` a confirmation, and the
  forwarded flag alone would let a hand-built object stand in for a routed utterance.
- `cancel that` is a **command**, not a confirmation: it abandons the pending action. It is not a
  job cancel, which is not in this grammar at all.

**The honest limit:** `ALIAS_COLLIDES_WITH_CONFIRMATION` refuses a wake-word set that contains a
confirmation word or a yes-word, because a wake word of `yes` would make "yes confirm" a
confirmation. That keeps the default set, and any set this module checked, disjoint from the
vocabularies it knows. A caller that passes a wake word this module has never heard of can still
widen what may precede the phrase, and no pure function of one utterance can catch that.

## Refusals: fail closed, never coerce

Every way an input can fail to be this router's input has its own code, exported as `REFUSAL`. A
refusal returns `{ ok: false, code, field, detail }`, frozen, carrying **no route** — so a caller
cannot render a bug as "the app heard nothing" by accident. Every code the module can return is
here, and a test sweeps the module's refusals and fails if this table and `REFUSAL` ever drift
apart:

| Code | When |
|---|---|
| `input-not-an-object` | the input is not a plain object (a string, an array, `null`, a class instance) |
| `unknown-field` | the input carries a field this router does not read |
| `utterance-not-a-string` | the transcript is not a string |
| `utterance-blank` | the transcript has no words in it (whitespace, punctuation) |
| `utterance-too-long` | the transcript exceeds `MAX_UTTERANCE_LENGTH` (4096, P0.2's `TEXT_MAX`) |
| `utterance-control-character` | the transcript carries a control character |
| `utterance-bad-character` | the transcript carries an unpaired surrogate — text that cannot be encoded |
| `utterance-format-character` | the transcript carries an invisible format character (Unicode Cf: zero-width, BOM, soft hyphen, bidi controls), which would let the text shown differ from the text routed |
| `invalid-aliases` | the alias set is not an array, is empty, is over `MAX_ALIASES` (8), or carries a malformed alias |
| `invalid-projects` / `invalid-agents` | the configured list is not an array, or is over `MAX_ENTRIES` (32) |
| `invalid-project` / `invalid-agent` | an entry is not a plain object, carries an unknown field, lacks an id or a spoken name, has an id outside `[A-Za-z0-9_-]{1,64}`, has a spoken name over 64 characters or carrying a control or format character, or has a spoken name with no words in it |
| `ambiguous-spoken-name` | two entries fold to the same spoken name, so neither can be addressed |
| `alias-collides-with-confirmation` | the wake-word set contains a confirmation word or a yes-word |
| `invalid-project-id` | (`defaultAgent`) the project id is not a database key |
| `invalid-last-used` | (`defaultAgent`) the local store is not a plain object of project id → agent id |
| `no-agents-advertised` | (`defaultAgent`) the consumer advertises no agent, so there is no default to name |

Two of the aliases' failures deserve their reason: an **empty** alias set is refused rather than
honoured, because "no wake word" would otherwise read as "forward everything"; and a **malformed**
alias is refused rather than skipped, because a set that silently loses a member is not the closed
set D164 asks for.

## D163's default agent

`defaultAgent({ project, lastUsedByProject, agents })` answers which agent a project switch should
address. The owner's rule: the last agent used in that project on this phone; on first use, the
consumer's first advertised agent. It returns `{ ok: true, agent, source }`, where `source` is
`last-used` or `first-advertised` so the app can say which it did — D163 requires the choice to be
**always announced**, and that announcement is P6.12's, not this module's.

`lastUsedByProject` is the app's **local** store (project id → agent id). D163 keeps it on the
phone and never in the database (D127's reasoning), which is why this function takes it as an
argument rather than reading it.

One case D163's answer does not name, decided here and stated rather than hidden: a remembered
agent the consumer **no longer advertises**. It cannot be addressed — an agent is an endpoint the
consumer advertises (D161), and nothing else — so the rule falls back to the first advertised
agent and says so through `source`. Refusing instead would leave the phone unable to switch to a
project whose remembered agent was renamed, against D163's own "switching must be fast".

## Who consumes it

- **P6.2** (the confirmation flow) and **P6.7** (the wire format's re-validation, in Go) are what
  decide whether an action record may run. This module decides only what counts as `confirm`; it
  does not decide what a confirmation is *for*.
- **P6.12** speaks D163's announcement and D164's "I didn't catch that" (`NOT_UNDERSTOOD_REPLY`,
  exported so the sentence and the refusal to forward cannot drift apart).
- The app's voice screen is where `routeUtterance` is called. Nothing else in the plan consumes it.

## Tests

```sh
cd shared && npm test                                  # the package's tests (the glob picks this up)
node --test "command/*.test.js"                        # this module's alone
shared/command/mutation-proof.sh                       # the guards, mutation-proved
```

`index.test.js` holds the plan's cases (the three routes, each of the five commands, the near-miss
table, "confirm" and "cancel that" recognised only as app commands, the alias set as data with
"yes" pinned as not an alias of "confirm"), the refusals, D163's default-agent rule including the
vanished-agent fallback, and three properties worth naming: the router is a function of the values
it is handed (the same input twice returns equal results, and it never mutates or hands back the
caller's objects), `isConfirmation` says no to two hand-built objects that claim to be
confirmations, and a source-level check that `index.js` imports nothing and reaches for no clock,
no random source, no network, no timer and no process handle.

**The red step** is P6.1's own: the "an unmatched Fugaro utterance is not forwarded" test, written
against a stub router that forwards, failing on it before the real branch existed. It is
`mutation-proof.sh`'s first guard, so it stays re-runnable rather than living only in a
pull-request transcript.

`mutation-proof.sh` plants 29 one-line bugs in `index.js` — each disabling exactly one guard — and
requires every one to make this module's suite fail. It refuses to report at all if it cannot find
a working runner, if the pristine suite is not green with at least one test actually run (an empty
glob exits 0, see [the package README](../README.md)), or if a control mutation goes uncaught. It
runs the module's own tests rather than the package's `npm test` because of a pre-existing,
unrelated failure in `shared/url` on the node line the package pins; the script's header says why,
and the task's pull request reports it.

One guard it found was not a guard at all: a second copy of each configured entry, re-copying an
object `checkEntries` had already frozen. Replacing it with `return entry` left all 50 tests
green, which is what a guard no test can defend looks like. It was removed and the copy site
became the mutation's anchor — the fix the harness exists to force.

Every guard above was mutation-proved when it was written. No test value here is a secret, and none
is secret-shaped.
