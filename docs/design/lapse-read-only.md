# The read-only state after a lapse (D436, T12)

**Status 2026-11-08: a design, not built.** Nothing in the tree reads a licence key: `ee/licence/`
does not exist, neither the console nor the app carries a verifier or a key store, and no release
holds one ([licence-key.md](../licensing/licence-key.md) says the same of itself). So nothing
refuses anything today, and this note fixes what the two builds that follow it — the console's lapse
state (S6.2) and the app's (S6.3) — must do, not what anything does. **Enforcement is deferred**
(D523, [ee-reorg reconciliation](../ee-reorg/reconciliation.md)): no notice, warning or key check
appears anywhere until the verifier, the key-minting service and the launch are ready. The console
release carrying S6.2 and the app release carrying S6.3 each state the behaviour in their own notes,
and the plan lists counsel's sign-off step (S8.4) as a dependency of both — though the Terms' §9.1
already carries T12's cancel and counsel approved it on 2026-10-29 (the reconciliation's T12).

This is subscriptions **S6.1**. It continues the licence-key note's state table and answers the four
things that note hands it: what a lapsed installation shows and still allows, what an installation
with no key shows, what the console 0.112.0 upgrade leaves a user in, and what a key the build cannot
verify is. It is not the verifier (S2.3–S2.5), not key entry (S2.7, S2.8), not the key service (S3),
not seats (S4), and not the Terms (S0.4, S8.4).

**The ruling it works from.** D436 ends production use at a lapse with no perpetual fallback, and B17
puts a 30-day grace between the key's `expires` and the walk-back; **T12** rules what survives: the
console and the app **read and cancel**, and the kill switch stays, while they cannot launch or
change configuration. The Terms carry it expressly, because read-only use against a production
installation is still production use: §9.1's *Read-only use* grants it, and §9.2 leaves the
command-line tool untouched ([subscription-terms.md](../licensing/subscription-terms.md)). F37 in the
subscriptions reconciliation is why the grant had to be written
([reconciliation.md](../subscriptions/reconciliation.md)).

**Numbers.** This document holds none: it adds no decision and no check to the subscriptions work
(whose decisions are D430–D459 and whose checks are 170–179), and the two that measure it, Checks
176 and 177, are the plan's.

## 1. The states a product can be in

The verifier names three states of a **key** — `valid`, `grace`, `expired` — and refuses everything
else ([licence-key.md](../licensing/licence-key.md), "The check"). A **product** is in one of five
states, and only the fourth is a lapse:

| The product's state | What it is | What it allows |
| --- | --- | --- |
| no key | the store is empty | everything, with a banner (§5.3) |
| `valid` | an installed key with `now <= expires` | everything |
| `grace` | `expires < now <= expires + 30 days` | everything, with the renewal banner |
| lapsed | past the grace | reads, cancel and the kill switch; not launch, not configuration |
| a key this build cannot verify | an id the keyring dropped, a rollback | the no-key state's, with its own banner (§5.4) |

**A lapse is reached only through a key that verified.** It is a fact about the licence and the
clock, never about the network: the check is offline, so no outage on Fugaro's side can cause one
(D308, and the spec's lapse section as D436 leaves it, [spec.md](../subscriptions/spec.md)).

## 2. What a lapse never does

- **It never stops work.** A running fleet, a queued job and a hotfix are untouched: no image
  carries a key (D524, [ee-boundary-moves.md](ee-boundary-moves.md) §8), the runner reads none, and
  the licence reaches no run.
- **It never touches the MIT CLI.** `fugaro run`, `fugaro cancel RUN`
  (`internal/cli/cancel.go:73`) and every core command keep working at every state, which §9.2 of
  the Terms undertakes. The command line is where a cancel actually happens.
- **It never removes cancel or the kill switch** (T12). Both are core and neither is ever touched by
  the licence work (the ee-reorg reconciliation's R4). In the console and the app they are, today, a
  state the product reads and a route this design keeps open (§3.2, §4.2).
- **It never refuses a read.** Every screen, view and read stays (§3.2, §4.2).
- **It deletes nothing, signs nobody out, and locks no screen.** The key screen in particular is
  never gated: a state a user cannot walk out of is a state that traps them (§5.5).

## 3. The console

### 3.1 What the console has today

The console's whole surface is `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts:586`): thirty-four
operations, twenty-five of which spawn the CLI under an exact-argv allow-list
(`SPAWN_COMMANDS`, `console/src/main/spawn.ts:715`). Twenty-three of the twenty-five are reads; two
are writes.

| Class | Operations | Child |
| --- | --- | --- |
| reads | `live.watch`, `budget.show`, `spend.report`, `runs.list`, `runs.stats`, `runs.summary`, `plan.status`, `graph.read`, `run.diagnose`, `run.logs`, `run.logLink`, `secrets.inventory`, `accounts.inventory`, `models.list`, `settings.read`, `cli.version`, `environment.report`, `installation.list`, `coordinator.status`, `coordinator.show`, `coordinator.intents`, `managed.status`, `policy.show` | the CLI |
| the console's own | `startup.compatibility`, `installation.current`, `installation.choose`, `installation.checkout`, `installation.remove`, `installation.reveal`, `installation.open`, `link.openExternal`, `link.openPullRequest` | none |
| writes | **`run.launch`** (`run --task-file - --json`), **`settings.edit`** (`settings`) | the CLI |

`settings.edit` writes one setting of the installation's configuration document at the layer the read
named ("one key of one document", `console/src/main/ipc.ts:1144`), so it is the console's
configuration change and not its own bookkeeping.

The plan's F20 counted **fifteen read operations, and no launch or cancel**
([reconciliation.md](../subscriptions/reconciliation.md)): that was the tree of 2026-10-27. The
launch landed since — D22 answered "one write, launch", and the row is annotated "the launch, and the
console's only write" (`console/src/main/spawn.ts:1170`,
[desktop-console-v2.md](desktop-console-v2.md)) — and the settings write landed after it, saying of
itself in the same table that it is "the second write" (`console/src/main/ipc.ts:1144`). **Cancel and the
kill switch are still absent, and deliberately so**: D2 keeps v1 strictly read-only, D14 makes the
allow-list the whole fence, and D34 leaves cancel and retry unscheduled
([desktop-console.md](desktop-console.md), [desktop-console-v2.md](desktop-console-v2.md)); the CLI's
`budget kill` and `budget resume` are on `NEVER_REACHABLE` as "a write: it turns a kill switch on,
which halts running work" (`console/src/main/ipc.ts:1640`).

So the console is where T12's "cannot launch" has teeth today and "cancel" has none yet: the lapse
has exactly two operations to refuse, and reads that are easy to mistake for paid features to keep.

### 3.2 What the lapse keeps, and what it refuses

**Kept: every read, including the paid ones.** `spend.report` reads `report --json`, whose command
moves to `ee/` (T11); `coordinator.status`, `coordinator.show` and `coordinator.intents` read the
coordinator's commands, which move with their library (T10). Those are the paid views, and after a
lapse they stay readable. Three reasons:

- reading is what T12 keeps, and §9.1's *Read-only use* grants exactly this surface — "to view the
  Customer's installations, Jobs and their records" — so a state that hid the customer's own records
  would be narrower than the licence it comes from;
- the console cannot draw a line between "paid functionality" and "the customer's own data" here:
  Spend is the installation's spend and the project view is the project's state;
- the console already has a mechanism that hides views — `REDUCED_VIEWS` (`spend`, `caps`, `kill`)
  in `console/src/shared/version.ts:392` — and it is the wrong one to borrow: it hides a view because
  the **data is absent** (the CLI is too old to print the field), and a lapse has the data. A lapsed
  view reading "not licensed" where the console knows the figures would be the misreading that
  mechanism exists to prevent.

Seats, when S4.2 builds them, follow the same rule: the panel is a read, it stays, and the estimate is
warned and never enforced (D439).

**Refused: the two writes.** After a lapse `run.launch` and `settings.edit` are refused in the
console's main process. Those are the console's launches and its configuration changes — T12's words
for them.

**Kept: the console's own surface.** Which installation the rail points at, the rail's own list, the
reveal and open, and the two link operations are the console's bookkeeping and the desktop's shell.
`installation.remove` even says so in its own row ("it removes the console's record and nothing on
disk", `console/src/main/ipc.ts:1281`). Refusing them would strand a user away from their other
installations at the moment the rail is the only way to reach one of them, so this document reads
"configuration" as the installation's configuration and leaves the rail alone. The rule behind the
call is that the refused set is a closed, named class — never "anything that writes".

**Kept: the kill switch's state.** `budget.show` carries the caps and the kill state and the Kill
view draws it; both stay. The console cannot turn the switch on or off (`NEVER_REACHABLE` above), and
the lapse changes nothing there: it neither unlocks a write the allow-list has refused since v1 nor
hides the state an operator reads to see whether work is halted. The switch a person *uses* is the
CLI's `fugaro budget kill`, which is core and never gated.

**Kept, when it arrives: cancel.** D34 leaves the console's cancel unscheduled; when it lands it
joins the kept class — and the gate must classify **operations by class**, not by a list of names
kept beside the table. The launch's row and a cancel's row differ in that one field, and a check that
asked "is this `run.launch`?" would refuse the cancel the day it is added.

### 3.3 Where the check runs, and why there

**In the console's main process, at the operation dispatch, before any child is spawned.** The
renderer is a web page rendering untrusted, job-written text; the main process is where the whole
fence already lives — the exact-argv allow-list, the child-environment allow-list (`CHILD_ENV_KEYS`)
and the operation table — and a change to any of them is security-sensitive by the console's own
rules (`.claude/rules/console.md`): an independent review, its pinning tests in `console/test/unit/`,
and the design's §5.1 table updated in the same commit.

Three consequences the build inherits:

- **the renderer is not the gate.** A renderer that hid the launch button would be cosmetic: the IPC
  channels are derived from the operation table in the main process and from nothing else
  (`channelForOperation`), so a crafted call from the renderer meets the same refusal;
- **a refusal is a refusal like the others.** A section whose read is refused names its own command
  (the console's rendering rule), and this refusal names the licence state and the way out instead —
  never a bare error, and never a claim read out of a key (the licence-key note's rule for refusals);
- **the state itself is a read.** If a screen needs to know the state, that is a new operation in the
  same table with the same pinning tests — not a value the renderer computes or the preload guesses.

## 4. The app

### 4.1 What the app has today

The app's entire write set is **two Realtime Database paths** — its mailbox's inbox node and the
`acked` flag (`mobile/src/mailbox/paths.js:119`) — and every utterance that reaches them passes
through one function, the mailbox client's `send` (`mobile/src/mailbox/client.js:559`). The mailbox
client is the app's only write path (`.claude/rules/mobile.md`, and `write-path.test.js` is the scan
that holds it).

What the app can put in a message is fixed by the shared confirm module
(`shared/confirm/index.js:142`):

| Class | Verbs | Note |
| --- | --- | --- |
| destructive, confirmed | `cancel`, `confirm`, `merge` | they enter `pending` and need a confirmation |
| pass-through | `launch` | D162's launch travels as the owner's own words, so it is an ordinary message |
| refused outright | `budget`, `kill`, `kill-switch`, `delete`, `revoke`, … | a closed set: a verb that is neither is refused as unknown |

**The kill switch is a read in the app, and only a read.** The app reads `config/kill/**`
(`mobile/src/feed/paths.js:117`, `mobile/src/feed/nodes.js:157`) and draws the state as a job field;
`kill` and `kill-switch` are refused verbs, so there is no kill action to gate. The switch a person
uses is the CLI's, as above.

### 4.2 What the lapse keeps, and what it refuses

| What the app sends | After a lapse |
| --- | --- |
| `cancel`, a run | **kept** — an action record |
| `confirm`, completing a cancel | **kept** |
| `launch` | **refused** |
| `merge` | **refused** — a repository write, and T12's kept set is closed |
| `confirm`, completing a merge | **refused** |
| an action that changes the installation's configuration | **refused** (the app has none today) |
| `kill`, `budget`, `delete`, `revoke`, … | refused at every state, unchanged |
| ordinary chatter, and every read | **kept** |

Two calls in that table are this document's own and are flagged in §8: `merge` is named neither by
T12's kept set nor by the plan's test for S6.3, and it is refused here because the kept set is
exactly what T12 kept — reads, cancel, the kill switch — and a repository write is none of them; and
the confirmation of a kept action is kept because the client already knows which action is pending
("one confirmation authorises exactly one action", `mobile/src/voice/dialogue.js:487`).

The app keeps reading everything it reads today: the feed, the jobs, the kill state, the heartbeat. A
lapse removes no view — the app has no paid view to remove.

### 4.3 Where the check runs

**In the mailbox client's `send`, beside the connection gate, before anything is minted, signed or
written.** `send` is the one function every utterance passes through, and it already refuses in a
shape the app speaks: `{ok: false, code, field}` with a code from `CLIENT_CODES`
(`mobile/src/mailbox/client.js:145`, refused through `gateRefusal` at `:520`). A licence refusal is
one more code, so a screen and the voice path render it the way they render the offline refusal, and
neither needs a licence concept of its own. Nothing is written, no id is minted and the key is not
touched by a refused utterance, exactly as the existing ordering guarantees ("the gate is here,
before anything is minted or signed").

The refusal's text follows the existing rule: a gated utterance is kept only as unsent text the user
can resend (`unsent()`), never in the outbox, and the app never sends it on its own
(`mobile/src/mailbox/client.js:456`).

A **launch** is the one hard case here, because it is an ordinary message: the client cannot tell a
launch from chatter at the write path, so the verb an utterance was **classified** as must reach the
gate — the same classification that passes D162's launch through today. Whether that arrives as an
option on `send`, as a refusal in the dialogue before it, or as a shape on the message is S6.3's to
choose; what this design fixes is that **a lapsed launch never reaches the inbox**, and that the
refusal happens before the write.

### 4.4 Cancel must not ride on a paid agent

The app's cancel is a message in the mailbox, and the component that reads the mailbox is an
orchestrator agent — the only addressee the schema advertises
(`shared/mailbox-schema/vectors.json:75`), and one D435 puts in `ee/`, the business-licensed folder.
**That is the trap this section exists to close**: T12 keeps cancel after a lapse, so cancel may not
depend on a component a lapse can turn off, or the app loses it in practice while the design says it
keeps it (F20, and the reconciliation's own note on the app's cancel,
[reconciliation.md](../subscriptions/reconciliation.md)).

The rule, and it is a rule of this design rather than a property of today's code:

- **a cancel resolves to a core path.** The action record's validation and routing are core already —
  `internal/mailbox/message.go:1345` resolves a `cancel` to `fugaro cancel RUN`, and the record is
  checked so that it can be run as argv and nothing else — and that command is MIT. Whichever
  component acts on a cancel, the action it takes is the core command, and no licence state may sit
  between the phone's cancel and it;
- **a lapse may not be the reason the app hears nothing.** The mailbox's heartbeat is the consumer's
  write, and the app shows "the orchestrator is offline" after missed intervals
  (`mobile/src/mailbox/gate.js:96`). A consumer that stops because a subscription lapsed would make
  the phone say "offline" for a licence reason and lose cancel with it. So the component that carries
  cancel must not be the paid one, and the app's state is spoken as itself when the licence — not the
  network — is the reason.

**Which core component carries it is settled by no ruling, and this document does not invent one.**
The options are in §8. What holds in all of them: a lapsed installation can still cancel a run from
the phone, and no licence check stands on the path a cancel takes.

## 5. The other states

### 5.1 Within grace

Everything works, exactly as a valid key allows: the launch, the settings write, every read. The
console shows the renewal banner — the spec's "renewal reminder in the console"
([spec.md](../subscriptions/spec.md)) — naming the day the grace ends, and the app speaks the same
fact where it speaks. A renewal key landing during the grace changes nothing for the user (T26's
overlap), and nothing is refused for the length of the grace.

### 5.2 After grace

The state §3 and §4 define: reads and cancel kept, launch and configuration refused, one banner
naming the state and the key screen. **Nothing else changes**: no data is hidden or removed, no run is
stopped, and the MIT CLI is unaffected.

### 5.3 No key

**An installation with no key keeps working, and shows one banner.** This is the document's own call
(§8), and these are its reasons:

- **nothing refuses without a key.** The licence-key note's status line is the whole tree's, on
  purpose: no build reads a key and D523 defers every notice with it. A design that made the absence
  of a key a refusal would be adding a check no ruling has, in the window the owner ruled has none;
- **a lapse is expiry, not a missing file.** B17 puts the grace after `expires`; a state table whose
  other row is "no key" would put the lapse's refusal on an absence rather than on the licence's own
  clock, and would make a file's presence — not the subscription — the thing that matters;
- **the states it covers are the ordinary ones.** A console 0.112.0 upgraded to the release carrying
  S6.2 has none (F43): 0.112.0 is the last MIT console release and the licence changes at 0.113.0,
  and every release through today ships honour-based with no key check
  ([docs/releases/console/README.md](../releases/console/README.md), and 0.113.0's own notes). A
  fresh install has none, a free evaluator who never asked for a key has none (F39), and an
  installation that ran only the MIT CLI before installing the console has none;
- **the alternative costs a capability the licence grants.** Reading "no key" as read-only at the
  release that enforces would take the launch and the settings write away from an installation the
  user already has, on an upgrade, before any key was ever asked of them. That is a decision the owner
  can make — it is the first option in §8 — but it is not one this document makes for them.

**The hole, stated rather than hidden:** a lapsed user who deletes their key is back to this state.
The check is a licence instrument and not a security boundary — the source is public, the key is
offline, and nothing phones home (the licence-key note's accepted limits) — so the Terms, the audit
clause and the customer's own word are the enforcement, and a hole this size is the price of the
no-key state above. A design that closed it would close it against the 0.112.0 upgrade too.

### 5.4 A key this build cannot verify

A store can hold a key the **current** build does not verify: a key id the keyring dropped once its
grace passed, or a key stored under a build that knew it and read by one that does not (a rollback).
The licence-key note hands this case here for exactly this reason
("what such an installation shows is S6.1's design rather than a fourth state here").

**It reads as the no-key state, with its own banner:** the installation keeps working, and the banner
says the installed key is not one this build knows and points at the key screen. It is not a lapse,
because a refused key never had a state — the verifier refuses rather than naming one — and a refusal
a product turned into read-only would be a product taking a capability away on a fact the verifier
declined to judge. The ordinary case is benign: a dropped key id means the key it names passed its
grace, so the organisation's licence has lapsed or been renewed and pasting the current key is the
fix. **A key the verifier refuses at entry never becomes a state at all** — both entry paths refuse a
bad key as it is pasted (S2.7, S2.8) — so what the user sees then is the entry screen's refusal and
not a product state.

### 5.5 The key screen is never gated

The key screen (S2.7 in the console, S2.8 in the app) is reachable and writable at every state,
including the lapse. It is not a configuration change in §3.2's or §4.2's refused class, and it must
not become one: a lapse you cannot leave is a trap, and a refusal's copy always names it as the way
out. This is the same rule as "cancel and the kill switch stay", one level up — a lapse takes paid
capabilities away and never the customer's ability to fix their licence.

### 5.6 The banner, and a refusal's copy

- The **banner** is the console's. It is persistent, it never blocks a read, and it names three
  things: the state, the day it refers to (the key's own `expires`, from the key), and the key
  screen. Its states are the table's: no key (§5.3), a key this build cannot verify (§5.4), within
  grace (§5.1), lapsed (§5.2).
- A **refusal** names the same state and the same way out, and never a claim read out of a key: a
  refusal may name the key and the part that failed, never a claim from an unverified payload.
- **No figure appears in either** (D430, T22; S0.1 pins the repository-wide rule): the copy names a
  date and a route, never an amount, and the renewal reminder names the day rather than a price.
- The console's copy belongs to the release that carries S6.2 (the plan's release actions say so),
  and the app's is spoken where the app speaks: the offline refusals already have their own lines
  (`mobile/src/mailbox/gate.js:96`), and a licence refusal gets its own, naming the state rather
  than "the orchestrator is offline".

## 6. The two points, and the one that is not

**The console's main process** and **the app's mailbox client** are the two enforcement points, as
the plan says. Nothing else gains a check:

- **the CLI does not refuse by licence state.** T12 and D436 both keep it launching and cancelling,
  and a CLI that refused a command after a lapse would gate the work the grace exists to protect.
  When enforcement lands, the entitlement travels in the task as a claim the runner obeys (ee-reorg
  R6) and no key is mounted into a job (D524);
- **no read is gated**, in either product, at any state;
- the console's reduced-view mechanism is not a third point: it hides a view whose data the CLI is
  too old to print, and a licence state never hides one (§3.2).

## 7. What measures this

Checks **176** and **177** (owner-run, S6.4, not yet written into the register) are the plan's
measure of this document, one product each: within grace everything works; after grace the reads and
cancel remain while the launch and the configuration write are refused in the console's main process;
with no key the console behaves as §5.3 says; the app still cancels through the mailbox and still
shows the kill switch, and refuses launch and configuration; and the MIT CLI launches and cancels
throughout. Both are OWNER-RUN: an agent builds, and a person with a packed console and an app build
runs them.

## 8. Decisions for the owner

### Settled by ruling

- The lapse keeps cancel and the kill switch and refuses launch and configuration changes (T12).
- The grace is 30 days after `expires`, and there is no perpetual fallback (D436, B17).
- Seats are warned and never enforced (D439), and the app's key does not enter its two-item secret
  store (D111, D232; S2.8).
- Nothing refuses and nothing warns until enforcement ships (D523, T17).
- The lapse needs an express post-lapse grant in the Terms, which §9.1 carries (F37, S0.4; counsel
  approved it on 2026-10-29).

### This document's calls, reversible in review

1. **No key keeps working, with a banner (§5.3).** The alternative is that an installation with no
   key is read-only once enforcement ships, which would take the launch and the settings write away
   from an upgraded 0.112.0 console and from every honour-based installation. Recommendation: §5.3,
   with the hole stated.
2. **A key this build cannot verify reads as no key (§5.4).** The alternative is to read it as
   lapsed, on the argument that the store only ever held a key that verified when it was pasted.
3. **`merge` is refused after a lapse (§4.2).** The kept set is T12's closed one, and a merge is a
   repository write. The alternative is to keep it beside cancel, as the finishing move of a hotfix.
4. **The console's own rail is not "configuration" (§3.2).** The alternatives are to gate the rail's
   choose and remove as writes, or to name the refused class by command rather than by operation
   class.
5. **Open, and the owner's: which core component carries the app's cancel** when the paid
   orchestrator agent is not running (§4.4). The options: (a) a core consumer for the cancel action
   alone, so neither a lapse nor the orchestrator's absence can touch it; (b) the installation's
   launcher-side core process drains the inbox for the cancel verb; (c) the orchestrator keeps a
   cancel half that is core and is never gated. Recommendation: (a), the smallest thing that makes
   the cancel a core path. The constraint all three share is §4.4's rule.

## 9. Out of scope

The verifier in each product (S2.3–S2.5, S2.6), the two entry paths and where each product keeps its
key (S2.7, S2.8), the production public key (S2.9), the key service and its checks (S3), the seat
estimate and its panel (S4), the roles (S5), the Terms and counsel's sign-off (S0.4, S8.4), the
coordinator job's key and the images (S1.9, E1.4), and the release notes of the console and the app —
the release that carries S6.2 or S6.3 states the behaviour in its own notes.
