# Reconciling the mobile app spec against this repository

**Status: plan only; nothing is implemented.** This document reconciles the owner's spec ([spec.md](spec.md), committed verbatim) against the code on `main`. It resolves what can be resolved by reading the tree, and it puts **everything that contradicts a ruling this repository has already recorded** in front of the owner as a numbered decision — with a recommendation, the alternative and its cost, and an **empty** answer column.

**Every decision below is unanswered.** This document does not answer any of them, and the plan does not start before they are answered and the plan's gate is passed.

**Decisions: D110–D129** (twenty-one rows: the twenty allocated, plus **D113b**, a sub-number added the way the repository's own **D54b** was — see D113b's row). **Owner-run checks: 80–89** (reserved; no row is written by this pull request). Both blocks are the owner's allocation of 2026-10-16 — see [README.md](README.md).

**Related:** [spec.md](spec.md) · [implementation-plan.md](implementation-plan.md) · [voice.md](../design/voice.md) (D39–D54b) · [desktop-console.md](../design/desktop-console.md) (D1–D20) · [desktop-console-v2.md](../design/desktop-console-v2.md) (D21–D38) · [managed-projects.md](../design/managed-projects.md) (D55–D78) · [project-centric.md](../design/project-centric.md) · [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §6.2, §6.4 · [console/docs/json-contract.md](../../console/docs/json-contract.md) · [SECURITY.md](../../SECURITY.md) · [gcp-live-checklist.md](../gcp-live-checklist.md)

---

## 0. What this document is, and how its claims were checked

**The baseline.** Every code claim below was checked against **`origin/main` at `76f6c59`**, which is the commit this branch is based on. Symbols are cited as `file` + **symbol**, never by line number, because line numbers move and symbols do not.

**What is claimed.** Each finding is classified:

| Class | Means |
|---|---|
| **Wrong** | The spec, or the brief that came with it, states something about this repository that is not true. The finding says what *is* true. |
| **Exists** | The spec asks for something the tree already has. The finding names it, so the plan reuses it rather than rebuilding it. |
| **Gap** | The spec asks for something the tree does not have and no ruling forbids. The finding says what has to be built. |
| **Rule** | A decision this repository has already recorded constrains the spec. The finding states the constraint and the toll it imposes. |
| **Open** | Only the owner can settle it. Every one of these becomes a D-row. |

**Every finding states its consequence for the design.** A finding without a consequence is trivia; the consequence is the sentence the plan is built from.

**What was not done.** No live Firebase project was queried, no phone was built, no rules were deployed, and no account was touched. The claims about the deployed rules are claims about the *template that generates them* and the tests that pin it, which is the strongest claim this repository can make without a live deploy. Where a claim needs a live project to confirm, it is in §5 UNVERIFIED, not in a finding.

**One structural note.** The earlier reconciliations ended with a §1.1 "where this work appears in an index — nowhere, and that is the convention" ([managed-projects.md](../design/managed-projects.md) §1.1). **This work departs from that convention on the owner's instruction**: the brief asks for `docs/mobile/README.md` as an index, and it is written. It is invisible to the test suite either way — see F33.

---

## 1. Corrections to the brief

The brief that came with the spec is accurate about the decisions it cites (D1, D2, D14, D40, D42, D44, D46, D47, D49, D50) — those were checked against [voice.md](../design/voice.md) and [desktop-console.md](../design/desktop-console.md) and they say what the brief says they say. Three of its statements about the *tree* are not true, and correcting them changes the plan.

### 1.1 There is no `firebase/` directory, no `database.rules.json` and no Firestore rules file

The brief says the budget rules are "`firebase/ database.rules.json`, firestore rules". **Neither exists.** There is no `firebase/` directory at the repository root, and no file named `database.rules.json` or `firestore.rules` anywhere in the tree.

The deployed Realtime Database rules are **generated from a template**: `internal/budget/rules/rules.json.tmpl` is expanded by `internal/budget/rules/rules.go`'s `Generate()`, which resolves the template's macros (`TOKEN`, `RUN`, `AMOUNT`, `STR`, `RBOK`, `DAYOK`, the `$path`/`$word` macros, `runDelta`, `perRunOK`, `fallback`, `tern`) into literal rules text. The expansion is pinned three ways: `internal/budget/rules/testdata/rules.golden.json` with `TestRulesGolden`, `internal/budget/rules/parity_test.go`'s `TestRulesParity` (template and golden cannot drift), and the emulator tests behind the `firebase` build tag (`harness_test.go`, `emulator_test.go`, `lease_emulator_test.go`) run against the pinned `firebase-tools` in `internal/budget/rules/emulator/package.json`.

**Consequence:** any rules change this work needs is a change to **the template**, with a regenerated golden, a parity check and new emulator cases — not an edit to a JSON file. And there is no Firestore rules file to reason about at all, so the brief's mention of Firestore rules has no referent.

### 1.2 `validateExternalUrl` does not exist, and `console/` holds no code

The brief says the PR link "must be opened only after the same URL validation the console uses (console `validateExternalUrl`: exact hosts, no userinfo/port)". **There is no such function anywhere in the tree**, and `console/` contains no source code — only `console/README.md` and `console/docs/json-contract.md`.

The rule the brief is describing exists as **design prose**: [desktop-console.md](../design/desktop-console.md) §5.1 rule 2 says the console "validates the URL is `https:` and on an allow-list of hosts", that "a host check is the second gate, never the first", and it names the provider constructions (github, bitbucket). The **exact host list**, and the userinfo/port rules, are written nowhere. §4.3 gap 7 of the same document records that no shape the console reads carries a provider field, so **no PR link is built at all today**.

**Consequence:** the app cannot "reuse" the validation, because it does not exist. It has to be **written once**, in the shared pure-logic package (D119), and both artifacts use it. This is a Gap (F27), not an Exists.

### 1.3 `docs/design/status.md` does not exist

The brief cites "`docs/design/status.md`" for the dashboard's hide-old-completed behaviour. That file does not exist. The behaviour is documented in **`plugin/skills/working/reference/status.md`**, and implemented in `internal/watch/filter.go`'s `Filter` and `internal/cli/watch.go`'s `watchOptions`.

**Consequence:** the citation in the plan and in the app's own docs is to the skill reference and to `internal/watch/filter.go`, not to a design document that is not there.

### 1.4 Everything else the brief asserts about decisions checks out

The brief's reading of D1 ("CLI sidecar (option A) … direct RTDB/Firestore access with the operator's credentials (option B) never"), D2 (strictly read-only), D14 ("treat every identity as a possible admin"), D40/D42/D54b (the helper process and its credential exception), D44/D46 (local-only STT/TTS, bake-off by measurement), D47 (no destructive voice commands), D49 (the mobile relay is not built and not scheduled) and D50 (the voice module lives in this repository inside the console's artifact) is **correct**, and those are the rulings the conflicts below are built on. No correction is needed there.

---

## 2. Findings

### 2a. What the tree already has, and what the plan must therefore reuse

#### F1 — The job feed is already a documented contract. **Exists.**

**Evidence.** `internal/cli/watch.go`'s `newWatchCmd` and `watchOptions` (`--json`, `--all`, `--keep`, `--keep-count`, `--repo`, `--once`); `internal/watch/json.go`'s `BuildJSON`, `JSONDoc`, `JSONRun`, `JSONFinished`, `JSONTotal`, `JSONRepo`, `JSONBurn`, `JSONKill`, `JSONConn`; the contract itself in `console/docs/json-contract.md` §1–§10; and `internal/cli/docs_desktop_console_test.go`, which pins the contract against the struct's fields.

**Consequence.** The job list is a **consumer of that contract**, not a new database reader. This is the single most consequential "Exists" in this document, because it is what makes D112's recommendation cheap: the data the spec wants already leaves the CLI as JSON, on a documented schedule, with documented reader rules.

#### F2 — Hide-old-completed is a *view* rule with a local acknowledgement store. **Exists.**

**Evidence.** `internal/watch/filter.go`'s `Filter` (`Keep`, `KeepCount`, `FailedKeep` — 24h, "not a flag") and `Apply`; `internal/watch/acks.go`'s `AckPath` (the acknowledgements file under `$XDG_STATE_HOME`); `plugin/skills/working/reference/status.md`; `internal/cli/watch.go`'s `watchOptions.Keep`/`KeepCount`.

**Consequence.** Two things the plan must get right. First, `--json` is **always unfiltered** — "hide old completed jobs by default" is the app's **own** filter over an unfiltered feed, not a flag it can lean on. Second, the acknowledgements live in a **local file on the machine running the CLI**, which a phone cannot share, so the app's "show all" toggle is a toggle over the unfiltered feed and its own (or no) ack state. The app must not promise the desktop's ack semantics.

#### F3 — The severity inputs a digest needs are already fields, and the tree's only alert is a *rate*. **Exists.**

**Evidence.** `internal/watch/json.go`'s `JSONKill`, `JSONFinished` (`Status`, `Outcome`), `JSONTotal.Percent`, `JSONRepo.Percent`, `JSONBurn.FastAlert` (`json:"fast"`); `internal/localcfg/localcfg.go`'s `BurnAlert()` reading `watch.burn_alert_usd_per_hour`; [desktop-console-v2.md](../design/desktop-console-v2.md) F29/D35 and [voice.md](../design/voice.md) F3/D48, which reached the same conclusion independently.

**Consequence.** The summariser/digester reads **documents**, not the database. A "cap is nearly reached" threshold is a **local setting**, not a server fact, so the phone cannot invent one and speak it as though the server said it. And "awaiting user input" — a state voice's D48 dropped because nothing in the tree produces it — still has no producer here.

#### F4 — The launch claim is the tree's only "two actors cannot do the same thing twice" mechanism. **Exists.**

**Evidence.** `internal/runstore/launch.go`'s `Claim`, `ClaimKey`, `ClaimTTL` (10 minutes), `(*Store).Claim`, `ReadClaim`; the take/release path in `internal/cli/run.go`; [desktop-console-v2.md](../design/desktop-console-v2.md) F11, which states plainly that this claim is the **only** reason two launchers cannot start the same run twice.

**Consequence.** The mailbox's consumer lease must be built **in this shape** — a holder, a timestamp, a TTL, and an "already-claimed" answer — because it is the only such shape in the tree and because the failure it prevents is exactly the failure a phone's mailbox has (two consumers, one message). This is what makes D110 answerable at all: the recommendation is not "invent a lease", it is "build the mailbox to the standard `runstore.Claim` already sets".

#### F5 — The tree's identity is a per-run custom token with claims, and the rules are written in terms of it. **Exists.**

**Evidence.** `internal/budget/token/mint.go`'s `Mint`, `IAMSigner`, `(*IAMSigner).SignJWT`, the claims `{fs, fr, fx, fp, rb}` and `ExpiryFX`; `internal/budget/model.go`'s path constants, including `PathProject = "fugaro/project"`; `internal/budget/rules/rules.json.tmpl`, where `TOKEN` and `RUN` are the macros every grant is built from.

**Consequence.** This finding decides the app's auth. The rules speak **run tokens**, not **users**. There is no user identity anywhere in the rules today (F12), so a phone signing in with Firebase Auth reads nothing until a grant is invented — which is D113.

#### F6 — The console answered "app reads the database directly" already, and the answer was no. **Exists / Rule.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) §3.1 (Option A, the CLI sidecar), §3.2 (Option B rejected), D1's answer ("CLI sidecar (option A) … direct RTDB/Firestore access with the operator's credentials (option B) **never**"), and §5.1 rules 1–10.

**Consequence.** The app's read path is the same question D1 answered, asked one process further out — a phone instead of an Electron window. A plan that answers it differently for the phone has to say why the argument changed, and the argument (a user credential that can read the whole installation) does not change with the form factor.

#### F7 — The console holds no credential, and the one exception this tree has was *written down as a decision*, not inferred. **Exists / Rule.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) D2, D14 and its Global Constraint 2; [voice.md](../design/voice.md) D42 (no credential value in the repository, in project config, in shared config or in console settings) and **D54b** (the helper-process exception, in the owner's words).

**Consequence.** A phone that signs in holds a credential, and that is an **exception** — which means it is a decision row (D111), not an implementation detail. D54b is the precedent for how to write one: state what is held, where, by whom, and what it can reach.

#### F8 — The console is read-only by an exact-argv allow-list, and that fence is the only write fence in the design. **Rule.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) §5.1 rule 6 (the allow-list and the never-table), D14's answer ("treat every identity as a possible admin, and give the read-only guarantee to the allow-list alone").

**Consequence.** An app that can write **one** RTDB path is an app holding a credential that can write whatever the rules let that credential write — which is D14's argument, unchanged. The fence has to be in the **rules**, not in the app's code, because the app's code runs on a device the operator does not control (D113, F21).

#### F9 — `console/` holds no code, and a boundary test keeps it that way. **Exists / Rule.**

**Evidence.** `console/README.md` and `console/docs/json-contract.md` only; `internal/cli/docs_console_boundary_test.go`'s `TestConsoleIsOutsideTheGoModule`, `TestGoModHasNoConsoleDependency`, `TestNoGoFileEmbedsFromConsole`, and `consoleBoundaryAllowList` (exactly one entry).

**Consequence.** The mobile app is a **second artifact outside the Go module**, and the shared voice module needs a home that both artifacts can reach without either importing the other (D119, D125). The boundary test's allow-list is the mechanism that will have to grow one entry, deliberately, in a reviewed change.

#### F10 — The voice module's home is already decided, and it is not built. **Exists / Rule.**

**Evidence.** [voice.md](../design/voice.md) D50 (a module in this repository, inside the console's artifact; **no npm package**; no `fugaro voice` command), D53 (post-console, unscheduled), and [2026-10-16-voice.md](../plans/2026-10-16-voice.md) (checks 64–66).

**Consequence.** "Share the voice module code with the console where practical" has an answer already — a module in this repository — and the app **depends on the console's work**, which is post-0.8.0 and unscheduled. The plan must therefore be explicit that the app's critical path runs *through* the console's voice module, and say what the app can build before that module exists (the mailbox and its semantics; D110).

### 2b. Where the spec and the tree disagree

#### F11 — "RTDB security rules restrict reads/writes to authorised users of that project" is wrong twice. **Wrong.**

**Evidence.** `internal/budget/rules/rules.json.tmpl` opens with a root `.read: false` and `.write: false`; every grant below it is a macro expansion of `TOKEN` or `RUN` (F5). There is no `firebase/database.rules.json` (1.1). `auth != null` **does** appear — 39 times in the golden, zero times in the template, because it is part of the two macros: `TOKEN` expands to `(auth != null && auth.token.fp != null && auth.token.fp == root.child('fugaro/project').val() && auth.token.fx > now)` and `RUN` adds the `fs`/`fr` pair (`internal/budget/rules/rules.go`). So the precise claim is not that the phrase is absent but that **no grant admits a signed-in user on its own**: every occurrence is one conjunct inside `TOKEN`, and every one of them also requires the `fp` claim — the per-run token's project fingerprint — and an unexpired `fx`. A Firebase Auth user carries neither, so it satisfies no grant in the tree.

**Consequence.** A phone signed in with Firebase Auth has **no read at all** today. The spec's "Auth & access" section describes a rules world that does not exist. This is not a detail to be fixed in passing: it is the reason the whole app depends on a rules change, and it is why D113 has to be answered before any app code is written.

#### F12 — The rules are *run-token* rules, and the token is minted per run by a launcher. **Wrong / Rule.**

**Evidence.** `internal/budget/rules/rules.json.tmpl`'s `TOKEN` requires `auth.token.fp` to equal `root.child('fugaro/project').val()` and `auth.token.fx` to be in the future; `RUN` adds the `fs`/`fr` slug-and-run pair. `internal/budget/token/mint.go`'s `Mint` is called by a launcher, for a job, with `ExpiryFX`.

**Consequence.** "An authorised user of that project" is **not an identity the database can express**. A user-facing grant is a new *shape* of grant, not a new branch of an existing one, and it has to be designed (what claim, minted by whom, expiring when) rather than copied from `TOKEN` — because a long-lived token carrying `TOKEN`'s claims would be a **run token in a phone**, which is strictly worse than a user grant (D113).

#### F13 — `/outcomes/**` carries no `.read` at all. **Exists / Rule.**

**Evidence.** `internal/budget/rules/rules.json.tmpl`'s `outcomes` node: `$day/$slug/$run` has `.write` and `.validate` and **no `.read` anywhere below it** (verified by grep: zero `.read` under the node). [voice.md](../design/voice.md) F11 records the same observation.

**Consequence.** A token-based reader **silently sees nothing** there. So a "recent jobs" view built on a direct database read would be missing the *finished* runs — the very rows the job list's "recent" half is made of. This is F14's point arrived at from the other side, and it is a second reason D112's recommendation is the cheap one.

#### F14 — "read directly from RTDB" for the job list is Option B, and half the rows are not in the database. **Wrong.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) §2.2's source table; `internal/cli/watch_queued.go`'s `openQueueBucket`, `queuedScanner` and `scanRun`, which read the **runs bucket** (Cloud Storage), not the database; `internal/cli/watch.go`'s `runWatchDegraded`, which reads run records when there is no budget backend at all.

**Consequence.** A direct RTDB reader **cannot produce the queued or finished lists**, because they come from the runs bucket. So the job list is either a `watch --json` consumer or a second implementation that is wrong about half its rows — and wrong *silently*, since a missing row looks like a row that does not exist. This is the concrete cost behind D112's alternative.

#### F15 — "the project's existing Firebase project" is not a thing this tree has a name for. **Wrong.**

**Evidence.** `internal/cli/init_firebase.go`; `internal/infra/firebasedb.go`'s `NewDB`; `deploy/terraform/gcp/modules/firebase/`; [project-centric.md](../design/project-centric.md); and the owner's standing rule that a **Fugaro project is an installation** and one Firebase project belongs to one installation.

**Consequence.** "Multiple accounts with easy switching" is **multiple installations**, not multiple Firebase projects under one account, and not multiple Auth users in one project. `belong` is one such installation, and no agent ever touches it (F31). The account model therefore has to be defined as an installation tuple plus an operator identity, with real isolation between entries (D114).

#### F16 — The orchestrator the spec talks to is not a Fugaro construct, and phase 1's "owner's laptop" is Voice D40's helper. **Exists.**

**Evidence.** [voice.md](../design/voice.md) F12, D40, D54b; `internal/agent/agent.go`'s `Args` — the tree's only `claude` invocation, headless, inside a container, on a cloud run.

**Consequence.** Phase 1 is **not a regression and not new work**: it *is* the helper process D40 already specifies, running on the operator's machine under the operator's own login. What is new is only the **channel** between the phone and that helper — which is the mailbox (F18). This is the finding that makes phase 1 a smaller piece of work than the spec's phrasing suggests.

#### F17 — Phase 2's always-on orchestrator needs a model credential in the cloud, and no design here has granted one. **Gap / Rule.**

**Evidence.** [voice.md](../design/voice.md) D40/D42/D54b — the credential is the operator's own login, resolved at runtime, **never stored**; [SECURITY.md](../../SECURITY.md); [managed-projects.md](../design/managed-projects.md) D55–D78.

**Consequence.** Phase 2 is not "the app unchanged" in the sense of "no new decisions" — it is a **new decision about who holds an LLM credential in the cloud**, and an always-on orchestrator holds `fugaro run` authority: it can spend against the budget caps, it is the other side of the launch claim (F4), and it is subject to the approval model. It also lands on managed-projects' D55–D78 gate. That is D115.

### 2c. The gaps — what has to be built and does not exist

#### F18 — There is no mailbox, no consumer, no heartbeat, and no `voice/sessions/{id}`. **Gap.**

**Evidence.** [voice.md](../design/voice.md) D49, which ruled the relay "not built, and not scheduled" and named the reasons; `internal/budget/model.go`'s path constants (no session, mailbox or heartbeat family); `internal/cli/root.go`'s command list (no poller, no consumer).

**Consequence.** The entire relay is new. The plan's first part is therefore the **specification** of it, not its implementation — and D110 is what authorises even that.

#### F19 — The mailbox's semantics have no home in the tree: no message id, no ordering, no idempotency, no replay protection, no ack. **Gap.**

**Evidence.** `internal/runstore/launch.go`'s `Claim`/`ClaimTTL`/`ClaimKey` is the tree's **only** claim shape (F4); `internal/runstore/runstore.go`'s `RequestCancel`/`CancelRequested` is a **marker**, not a message — it carries no id, no ordering and no acknowledgement.

**Consequence.** The mailbox must be specified before it is built. The shape that fits this tree is: **at-least-once delivery with an explicit message id and consumer-side dedupe**, a **consumer lease** in the shape of `runstore.Claim` with a TTL, an **ack** the consumer writes and the phone can see, and **replay protection** — because a phone that was offline for six hours will re-deliver everything it holds, and a database that cannot tell a replay from a new message will act on both.

#### F20 — The rules change's toll is fixed, and it includes an owner-run deploy. **Rule.**

**Evidence.** `internal/budget/rules/rules.go`'s `Generate()`; `internal/budget/rules/testdata/rules.golden.json` with `TestRulesGolden` and `TestGeneratedRulesParse`; `internal/budget/rules/parity_test.go`'s `TestRulesParity`; the `firebase`-tagged emulator tests (`harness_test.go`, `emulator_test.go`, `lease_emulator_test.go`) against `internal/budget/rules/emulator/package.json`'s pinned `firebase-tools`; `internal/infra/firebasedb.go`'s `DB.Apply` and `(*DB).RequireCapsForEnforce`; `internal/projectpolicy/paths.go`'s `PathRulesMarker()` (the node recording which generation of the rules is deployed) and `internal/projectpolicy/version.go`'s `RulesVersion`; [gcp-setup.md](../gcp-setup.md); the `rules` CI job; `scripts/release-gate.sh`'s `names="test terraform rules"`.

**Consequence.** Any inbox/outbox/heartbeat node costs **template + regenerated golden + parity + emulator cases + a `RulesVersion` bump + an owner-run `fugaro init --firebase` deploy**. No agent live-applies rules, ever. The plan carries this as a task with an explicit owner-run step, not as a footnote (D113).

#### F21 — No rule loosening may let a phone write anything but its own inbox messages. **Rule.**

**Evidence.** The template's write model has **two idioms**, and the plan's rules task must follow the right one:

1. **Leaf-level `.write`.** The budget, caps and lease families put a `.write` on each leaf — `config/caps/**`, the per-run spend and lease nodes — so each counter is individually writable and individually gated (`AMOUNT`, `STR`, `RBOK`, `MaxString`).
2. **Subtree-level `.write`, with `.validate`-only children.** `agents/$slug/$run` carries `".read": "RUN"` and `".write": "RUN"` **on the run node**, and every child below it — `repo`, `title`, `tokens`, `spent`, `updatedAt`, … — is gated by `.validate` **alone**, with `"$other": { ".validate": false }` closing the subtree to unknown fields. `outcomes/$day/$slug/$run` is the same shape: one `".write": "RUN && DAYOK && !data.exists()"`, `.validate`-only children, the same `$other` denial.

**Both idioms matter, and choosing the wrong one is the mistake this finding exists to prevent.** `agents/$slug/$run` is the template's closest analogue to a mailbox message — one node, owned by one writer, written whole, with a validated field set — and it is the **subtree** idiom, not the leaf one. (An earlier form of this finding said "every numeric and string field carries its own `.write`, no ancestor carries one", which is false of exactly the two families a mailbox resembles, and would have designed the rules change from the wrong half of the model.)

**Consequence.** The inbox follows idiom 2: **one `.write` on the message node**, whose predicate is the phone's own user claim, with `.validate`-only children, a size bound as a `.validate` on the body, and `"$other": { ".validate": false }` so an unknown field is refused rather than stored. The fence is the `.write` predicate plus that denial — not a per-leaf `.write`. And the phone's identity must be unable to write **any other path**. That is not an assertion to be made in prose: it is an emulator test case, asserting a *denial*, and it is the check that makes D113's recommendation safe to accept.

#### F22 — There is no user-facing read path, and there are exactly two ways to get one. **Gap.**

**Evidence.** `TOKEN`/`RUN` (F12); `internal/cli/root.go`'s command list; `console/docs/json-contract.md`, which is a **read contract over a child process**, not over the database.

**Consequence.** The app's reads are a decision (D112), and there are three real options rather than two, because the feed the CLI produces **cannot simply be carried to a phone**: it is produced by a process that authenticates with the operator's own application-default credentials (`internal/cli/cloud.go`'s `google.DefaultTokenSource` feeding `rtdb.New`), which is the credential class D111 forbids the phone to hold, and a phone cannot spawn the Go CLI in any case (F35). So the read path is either a **relay document** written into the database by an identity that *can* hold those credentials, a **LAN/VPN** route to the laptop that does, or a **direct read** with a new grant — and each has a cost D112 states. A permanent user-readable database surface has to be argued against D14 and paid for in D113.

#### F23 — There is no audio anywhere, and no on-device STT/TTS substrate. **Gap.**

**Evidence.** [voice.md](../design/voice.md) F15; the tree's Node packages are all `private: true` and none is an app (`internal/budget/rules/emulator/package.json`, `deploy/sandbox`, `images/base/harnesses`).

**Consequence.** The audio half is greenfield. It needs **interfaces and fakes first** (AudioIn, STT, TTS) so that the parts that are pure logic can be tested without a device (D123, D126).

#### F24 — There is no Expo/React Native anything, and no store or EAS path. **Gap.**

**Evidence.** `scripts/release.sh`, `.goreleaser.yaml`, `.github/workflows/release.yml`, `images.yml`, `ci.yml`; [release.md](../release.md) (binaries, SBOMs, cosign, the Homebrew cask, ghcr images) — **no mobile or store step**; [voice.md](../design/voice.md) F7.

**Consequence.** The app is a new artifact with a **new release path**, and the accounts, signing keys and submissions behind it are owner-run (D120, D125).

#### F25 — There is no push substrate, and the tree's only background network call is the update check. **Gap.**

**Evidence.** `internal/updatecheck/updatecheck.go`; [voice.md](../design/voice.md) D49, which lists "no push server" among the reasons the relay was not built.

**Consequence.** Push is a new service with a new token store and a new privacy surface, and it is a decision of its own (D121).

#### F26 — There is no retention or cleanup mechanism for any database node, and no redactor for speech. **Gap.**

**Evidence.** `internal/budget/sweep.go` (the tree's sweeper; it writes nothing under `/config`); the runs bucket's 90-day lifecycle is **bucket-side**, not database-side; [voice.md](../design/voice.md) F32, which records that a voice recording is a person's voice and that no existing redactor covers it.

**Consequence.** Retention is a decision with a privacy edge (D118), and the app must **never store or upload audio** — the on-device STT decision (D44) is what makes that possible, and it must be stated as a rule rather than left as an implication.

#### F27 — The PR link cannot be opened on this tree, and the console's rule is prose rather than code. **Wrong / Gap.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) §5.1 rule 2 (the URL rule, in prose); no `validateExternalUrl` anywhere; `console/` holds only `README.md` and `docs/json-contract.md` (1.2). And **two different CLI gaps**, which the console's §4.3 and `console/docs/json-contract.md` §7 settle separately and which must not be run together:

- **Gap 6 — no PR field on a run that has not finished.** `internal/watch/json.go`'s `JSONRun` carries `repo` and **no** `pr_url`/`pr_number`. They exist on `JSONFinished` (`json.go`'s `PRURL` and `PRNumber`, `json:"pr_url"`/`json:"pr_number"`), and `internal/runview`'s `Row` spells the pair `pr_url` + `pr`. So **the URL is present for a finished run** — which is exactly the run the spec's "PR link when ready" describes — and absent for a live one.
- **Gap 7 — no provider on any shape.** `JSONRun`, `JSONFinished` and `runview.Row` each carry `repo` and no provider or origin host. So although `JSONFinished` carries a `pr_url`, **no shape says which host it is on**, and a link rebuilt as `https://github.com/<repo>/pull/<n>` is not merely useless but **wrong** for a Bitbucket repository such as `edgeappinc/fugarosandbox`. That is why the console design says **no PR link at all**, and shows repository, number and URL as text rather than handing them to `openExternal`.

**Consequence.** The app inherits the *rule* (https, an exact host allow-list, no userinfo, no port), which exists nowhere in code and must be **implemented** in the shared package before either artifact opens anything (D124). What the app does **not** have to invent is the field: `JSONFinished.pr_url` is already in the feed, so the finished half of the job list can carry a link as soon as the validator and the host allow-list exist. The live half cannot, until gap 6 closes on the CLI side.

#### F28 — The summariser's spend is outside Fugaro's caps, and that is already decided and disclosed. **Rule.**

**Evidence.** [voice.md](../design/voice.md) D45 (summariser spend is on the operator's account, not in Fugaro's caps, and it is disclosed); `internal/budget/lease.go` (a lease is **per run**).

**Consequence.** The phone's summariser inherits D45 unchanged, and the disclosure has to be **on the phone's screen**, because a phone is exactly where a user would otherwise assume the caps cover it (D116).

#### F29 — The interrupt switch is a policy over the digest, and the digest is a pure function of documents. **Exists / Rule.**

**Evidence.** [voice.md](../design/voice.md) D48 (the digester's source and severity; "awaiting user input" dropped); `internal/watch/json.go`'s `JSONBurn.FastAlert`, `JSONKill`, `JSONFinished.Failed`; [desktop-console-v2.md](../design/desktop-console-v2.md) D35 (the cap-alert threshold is a local setting).

**Consequence.** The interrupt switch is a **local setting plus a pure function**, which means it is testable in CI with no phone, no microphone and no network (D127).

#### F30 — The behaviours that need a device are owner-run, because no test may need a live cloud or a real microphone. **Rule.**

**Evidence.** [gcp-live-checklist.md](../gcp-live-checklist.md)'s register and its numbering (checks to 58 on `main`); [voice.md](../design/voice.md) D52 (fakes from task one; four of six criteria in CI); [2026-10-16-managed-projects.md](../plans/2026-10-16-managed-projects.md)'s Global Constraint 12 (every task's verification runs without a live cloud).

**Consequence.** Checks **80–89** are owner-run, and the CI-assertable parts are pure functions over fakes. This is what keeps the plan honest: the parts a machine can prove are proven, and the parts it cannot are written down as checks the owner runs and pastes into the pull request (D126).

#### F31 — `belong` is never touched by any agent, and that includes a phone account for it. **Rule.**

**Evidence.** [project-centric.md](../design/project-centric.md)'s standing rule that no agent ever touches `belong`; [voice.md](../design/voice.md) D52 and its task V4.3; [2026-10-16-managed-projects.md](../plans/2026-10-16-managed-projects.md)'s Global Constraint 7.

**Consequence.** A `belong` account is added **by the owner by hand**, and no fixture is ever recorded from it. The account model must be able to hold an installation the app has never been pointed at by an agent (D114).

#### F32 — One tag releases everything, and the console is already the one artifact that stretches it. **Rule.**

**Evidence.** [release.md](../release.md); [desktop-console.md](../design/desktop-console.md) D11 (the console is its own artifact, published from the CLI's `vX.Y.Z` tag); [voice.md](../design/voice.md) F29/D50; `scripts/release-gate.sh`.

**Consequence.** A fourth artifact is a **release-train decision**, not a packaging detail: the store cannot be released by `goreleaser`, and the store credentials deliberately are not in the release gate's environment (D125).

#### F33 — The docs tests are per-document presence tests, so `docs/mobile/` is invisible to them. **Exists.**

**Evidence.** The `internal/cli/docs_*_test.go` files each read **named** files; `internal/cli/docs_bucket_iam_test.go` globs `../../docs/*.md`, `../../docs/design/*.md`, `../../README.md`, `../../SECURITY.md` and the skill files — a `docs/mobile/` subdirectory is matched by none of them.

**Consequence.** This pair of documents changes **no test**, and **no test will keep these documents true**. The plan's own tasks are the only thing that will, which is why each task carries its own verification. (It also means the plan may not claim a test protects anything here.)

#### F34 — The degraded mode is a second document shape, and one of its lines is not JSON. **Exists.**

**Evidence.** `internal/cli/watch.go`'s `runWatchDegraded`, `watchDegradedDoc`, `degradedNotice` ("no budget backend: showing run records only") and `degradedSince` (24h); `console/docs/json-contract.md` §10.8.

**Consequence.** The app's reader must **skip a non-JSON line** (the contract's reader rule 1) and render the degraded state honestly rather than showing zero spend — a missing field is `unavailable`, never `0`.

#### F35 — The document rules are written down, but the phone cannot run the child they were written for. **Wrong / Exists.**

**Evidence.** `console/docs/json-contract.md` §10.13's reader rules are about **documents**, and they carry over unchanged: skip a non-JSON line; newest is not accumulated; silence is not death; a changed `day` is a break; a missing field is `unavailable`, never `0`; `--repo` narrows via `watch.FilterRepo`; never `--follow`, never `--once` for the live feed. §10.11 ("Does option A need a supervisor process? Yes") is about **the console**, and its option A is a desktop sidecar on the same machine as the binary — `desktop-console.md` D13's installation tuple has a **binary path**, which is a thing a phone does not have and cannot get: there is no iOS or Android build of the Go CLI, and a phone cannot execute one if there were.

**And the feed is not merely a child to spawn — it is a child that needs credentials.** `fugaro watch` reaches the database and the runs bucket with the operator's **application-default credentials** (`internal/cli/cloud.go`'s `google.DefaultTokenSource` → `rtdb.New`), which is exactly the credential class D111 forbids the phone to hold. So "the app supervises one child per account, exactly as the console does" is **false twice over**: the phone cannot run the process, and could not authenticate it if it could.

**Consequence.** The app implements the contract's **document** rules and **no child-process supervisor**. The transport that carries those documents to the phone is D112's open decision, and it is the same question as the writer identity of D113b — a relay document needs a writer, and the writer needs a grant. What is already designed is the reader's behaviour over a document; what is not designed, and is now a decision rather than an assumption, is how the document arrives.

#### F36 — The app's "accounts" have no enumerator. **Gap.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) D13 (the installation tuple: project name, config path, binary path, optional checkout dir; "stores the tuples, never a credential") and §4.3 gap 5; `internal/cli/configcmd.go` (`config example|show|layer|init|publish`); `internal/cli/root.go`.

**Consequence.** The account list is **typed by the owner by hand**, and the phone cannot discover installations. The app's switcher is therefore a list the owner maintains, not a discovery result (D114).

#### F37 — The microphone is a permission the console's design denies, and the phone's OS asks for it too. **Rule.**

**Evidence.** [desktop-console.md](../design/desktop-console.md) §5.1 rule 9 (deny every web permission, including microphone); [voice.md](../design/voice.md) D43/D51.

**Consequence.** The phone's microphone is an **OS permission with a usage-description string**, and the difference from the console's rule is a difference of *kind* — a web view denying `getUserMedia` is not a native app requesting `RECORD_AUDIO` — so the plan states the difference rather than inheriting the rule as though it applied (D122).

---

## 3. Decisions for the owner (D110–D129)

**Twenty-one rows. None is answered. This document does not answer any of them, and the plan may not start before the ones marked as gating are answered.**

Each row: the question, **this work's recommendation**, the **alternative and what it costs**, and an empty column for the owner.

**How the brief's conflicts map to these rows:** (a) → D110 · (b) → D111, D112 · (c) → D113, D113b · (d) → D114 · (e) → D115 · (f) → D116, D127 · (g) → D117 · (h) → D118 · (i) → D119, D120, D121, D122, D123 · (j) → D124 · (k) → D125, D126, D128, D129. **Every labelled conflict is covered, and no row reverses a ruling on its own.**

| # | Question | Recommendation | The alternative, and what it costs | Owner's answer |
|---|---|---|---|---|
| **D110** | **Does this work supersede Voice D49?** D49 ruled the mobile relay "not built, and not scheduled" because a realtime-database command has no claim, no TTL and no ack. The spec's inbox/outbox **is** that relay. | **Supersede it, but only by meeting the standard D49 named** — not by relaxing it. The mailbox is specified before it is built: an explicit message id, at-least-once delivery with consumer-side dedupe, a consumer lease in the shape of `runstore.Claim` with a TTL and an `already-claimed` answer, an ack, and replay protection. D49's reasons are answered one by one in the plan, and the work stays unscheduled until this plan's gate passes. | **Keep D49** (do not build the mailbox): the app ships the job list only and voice is deferred indefinitely — the spec's **primary** feature does not exist, and this reconciliation becomes a plan for a secondary one. **Or build the relay without a claim, a TTL or an ack:** cost is exactly the failure [desktop-console-v2.md](../design/desktop-console-v2.md) F11 describes — two consumers acting on one message, and a replayed message launching a second run — on a device that can be offline for hours and re-deliver everything it holds. | |
| **D111** | **What may the app hold?** D1/D2/D14 (console) and D42 (voice) say the console holds no credential and no credential value is stored anywhere. A phone that signs in to Firebase Auth holds one. | **A Firebase Auth ID token, in platform secure storage (Keychain / Keystore), and nothing else.** Never a Fugaro or cloud admin credential, never an LLM key, never a service-account JSON, never a `fugaro` config file. The token is a **user** credential scoped to one installation, refreshed by the SDK, revocable by deleting the Auth user. D54b is the precedent for writing the exception down rather than inferring it. | **No credential in the app at all** (the app is a `watch --json` client over the LAN or a VPN, with no Firebase Auth): the phone holds nothing, but there is no remote use and the mailbox is impossible. **Or hold the operator's own `gcloud` or service-account credential:** D14's argument, on a device that is lost far more often than a laptop — and it is a **cloud-admin** credential, not a read-only one. | |
| **D112** | **Do the app's reads go through RTDB directly, or through a Fugaro-owned read path?** The spec says the job list reads RTDB directly. Console D1 answered the same question for the desktop and said direct database access **never**. | **No direct database reads** — and this recommendation **has no transport yet**, which the row states rather than assumes. The app consumes what the CLI already produces (`fugaro watch --json`, [json-contract.md](../../console/docs/json-contract.md)); **which of the three transports below carries it to the phone is the owner's answer, and P3.2 may not start until it is given.** The phone's database surface stays the **mailbox**: its own inbox messages, its own outbox (D113, D113b). | **Three real transports, each with a cost, and none of them free.** **(i) A relay/mirror document.** The laptop helper (or the phase-2 service under D115) writes what the phone needs into the database and the phone reads only that node. Cost: a **new write path with its own writer identity** (D113b), a new node family with a retention question (D118), and a copy of the feed that can go stale — but it is the only option that works with the phone away from the laptop, and it keeps the phone's credential read-only over one subtree. **(ii) LAN/VPN access to the laptop's own feed.** The phone calls the laptop's `fugaro watch --json` over the LAN or a VPN (D111's second alternative). Cost: no database grant at all and no new node, but the laptop must be awake and reachable — exactly what the spec's remote use is not — and a VPN is a new surface the owner runs. **(iii) Direct reads with a narrowly-scoped grant.** The rules would have to grow, named exactly: a **user claim** (the `fp`/`fs`/`fr` shape is per-run and cannot be reused — D113); a `.read` on `fugaro/agents/$slug/$run` (the subtree idiom already there); `.read` on the `config/caps/**` and `config/spend/$day/**` leaf families; `.read` on `config/kill/**`; and **a `.read` added to `/outcomes/$day/$slug/$run`, which has none today (F13)** — and it is still **wrong about the queued and finished rows** (F14), because they come from the runs bucket, not the database. That is a permanent user-readable database surface, argued against D14. | |
| **D113** | **The rules change: which node families, what may a phone write, and how does it ship?** | **One new subtree** for the mailbox, added to `internal/budget/rules/rules.json.tmpl`. Every grant goes through a **user claim** carried in the Auth token. The phone's writes are **exactly two, both inside its own account's mailbox subtree and nowhere else**: (1) it **writes its own inbox messages** — one `.write` on the message node, `.validate`-only children, `$other` denied, a size bound; and (2) it **acks a message it has spoken**, a `.write` on that message's own `acked` child and nothing beside it. The second is **one write more than "inbox messages only"**, so it is named here as a narrowing **the owner has to approve** (P3.6) rather than smuggled in as a consequence of the first. The **heartbeat is not the phone's write** — the spec has the orchestrator write it, and that writer's identity and grant are **D113b**. **No `.read` on any existing node is loosened.** Ship it the way the rules already ship: regenerated golden, parity test, **emulator cases asserting the denials**, a `RulesVersion` bump, and an **owner-run** `fugaro init --firebase` deploy — no agent live-applies. | **Reuse the per-run token grants for the phone.** Cost: per-run tokens are minted by a launcher, are per-run and expire (`ExpiryFX`), so a phone cannot hold one; a long-lived token with `TOKEN`'s claims is a **run token in a phone**, which is worse than a user grant. **Or the phone writes nothing at all** (no ack; the consumer re-delivers until its own TTL expires): cost is a mailbox that cannot tell "spoken" from "delivered", and the duplicate the phone then speaks. **Or no rules change at all** (a Fugaro-owned service the phone talks to over HTTPS): cost is the spec's own "no new backend" principle, plus a service to host, authenticate and pay for. | |
| **D113b** | **Who writes the outbox and the heartbeat, and how is that writer authenticated?** D113 fences the **phone**; the other side of the mailbox — the outbox the phone reads, the heartbeat D117 shows — is a writer the rules must name, and the spec has the **orchestrator** write the heartbeat. No such writer exists today: `fugaro watch` writes nothing to the database, and the only credential the CLI holds there is the operator's application-default one (`internal/cli/cloud.go`'s `google.DefaultTokenSource` feeding `rtdb.New`). *(Numbered **D113b**, not D130: D110–D129 were checked and **all twenty are used**, and a lettered sub-number beside an existing row is this repository's own precedent — **D54b**, where a decision was found after a block had been allocated.)* | **A named writer identity, separate from the phone's, with two writes and no more.** The **laptop helper** in phase 1 — the binary the operator already runs, under the operator's own credential, so **no new secret is created** — and the **phase-2 service identity** under D115. It may write the **outbox** and the **heartbeat** and nothing else: never the phone's inbox, never a run's budget nodes, never `/fugaro/**`. And the outbox is **authenticated to the pairing**, because an outbox anyone can write is an outbox that makes the phone **speak an attacker's text out loud**: a per-pairing key established when the account is added by hand, with a MAC over each message's id, text and timestamp, verified by the phone **before it speaks a word**. The key lives in the phone's secure storage (D111) and on the laptop; it is **not** a database credential and it is not the Auth token. *(One honesty note: an **admin** credential bypasses RTDB rules — the CLI writes this database as a project owner or editor today, which is what `internal/infra/firebasedb.go`'s `dbErr` says in so many words — so for the **laptop** helper "the outbox and the heartbeat and nothing else" is a rule in the helper's own code, not a fence the database enforces. The rules fence is what constrains the **phase-2 service identity** and every other token-bearing writer, and the pairing MAC is what protects the phone either way.)* | **Let the phone write its own outbox** (the orchestrator only reads the inbox). Cost: the phone then writes two node families, and the transcript becomes whatever the phone says it is — the consumer's D110 claim/replay reasoning has to be re-argued for a node the consumer also writes. **Or reuse the run token for the helper.** Cost: a run token is per-run and expires (`ExpiryFX`) and carries `fs`/`fr`, so an always-on helper has to re-mint one — a launcher, i.e. a second mechanism, for a node that has nothing to do with a run. **Or no authentication on the outbox**, trusting the rules alone. Cost: the spoofing path above — anyone who can write the outbox chooses what the phone says. | |
| **D114** | **The account model.** "Firebase Auth against the project's existing Firebase project" plus "multiple accounts with easy switching between them". | **An account is an installation:** one Firebase project + one operator identity (the Auth `uid` in that project) + one Fugaro project name. Several are stored as **separate, non-overlapping** entries — a per-account Keychain item, a per-account cache namespace, no shared token store — and the switcher **tears down the previous account's listeners and caches before it opens the next**. `belong` is one of them, added **by the owner by hand**; no agent ever touches it and no fixture is ever recorded from it. | **One Firebase project with several Auth users.** Cost: it contradicts one-Firebase-project-per-installation, and every user of that project would see every run — the isolation the spec wants is between **installations**, not between users. **Or a single account.** Cost: the spec's last line is unmet, and the switcher (a screen, an isolation rule, a whole test) is dropped. | |
| **D115** | **Phase 2 hosting: who holds the model credential?** Phase 2 puts the orchestrator in an always-on cloud service. D40/D42/D54b put the model credential on the **operator's own machine**, under the operator's own login, never stored. An always-on orchestrator also holds `fugaro run` authority. | **Phase 2 is its own decision, taken after phase 1 ships and after managed-projects' D55–D78 gate.** If it is taken: the model credential lives in a secret manager **in the installation's own project**, the service runs as a dedicated identity with a narrow IAM role, it is behind the **same launch claim** (`runstore.Claim`) and the same budget caps, and the app is unchanged only because it never sees any of it. | **Keep phase 1 forever** (the orchestrator is always the laptop): the phone works only while the laptop is awake — the status quo, and the spec's "no regression" is satisfied. **Or put an LLM key in the app, or in the phone's project:** D42 forbids the first, and a key on a device is extractable from a device. | |
| **D116** | **The summariser's spend, and its disclosure.** D45 already says summariser spend is on the operator's account and outside Fugaro's caps, disclosed. | **Inherit D45 unchanged, and put the disclosure on the phone's screen** — one line saying summaries are billed to the operator's own model account and not to Fugaro's caps. The phone is exactly where a user would otherwise assume the caps cover it. | **Bring the summariser inside Fugaro's caps.** Cost: it is not a Fugaro run, has no lease and no run record, and `internal/budget/lease.go` is **per run** — so this is a new budget model, not a flag. | |
| **D117** | **Presence and the heartbeat.** The spec asks for a periodic RTDB write and an "orchestrator offline" state. | **One heartbeat node per account**, written at a bounded interval with a timestamp and a boot id; the app treats it as **stale after N missed intervals** and shows "orchestrator offline". The **server's** timestamp is the authority, never the app's clock: a skew beyond the bound is shown as "clock differs", not as "offline". The interval is a setting **with a floor**, because RTDB bills per write. | **No heartbeat**, inferring offline from the absence of an outbox reply. Cost: indistinguishable from "the orchestrator is busy" — and the spec's whole point is that the app must not accept messages into the void. **Or `onDisconnect` alone:** it covers a dropped **connection**, not a hung **process**, and the spec's failure is a process. | |
| **D118** | **Retention, cleanup and privacy.** The spec's open question; spoken text can contain secrets. | **The app never stores or uploads audio** — STT is on-device (D44), so the audio never leaves the device. The mailbox holds **text**, and text is **redacted before it is written**, by the same redactor the digest uses. Retention is a **TTL on the mailbox node** plus a sweeper the owner runs, in the shape of `internal/budget/sweep.go`, with a stated window; the rules enforce a **size bound per message**; and there is a "forget this conversation" control that deletes the node. | **Keep everything forever.** Cost: a growing bill and a growing record of spoken secrets, in a database whose grants are per-run tokens today (F11) and whose mailbox rules this work is adding. **Or no redaction, relying on the user:** a spoken token or environment value is in the database before anyone notices. | |
| **D119** | **Where the shared code lives, and how it is tested without either runtime.** "Share the voice module code with the console where practical"; D50 already put the voice module in this repository, inside the console's artifact. **This row amends D50, and says so rather than departing from it quietly.** | **A pure-logic package in this repository, in its own top-level directory — and that is an amendment of D50, whose answer put the module inside the console's artifact.** D50's operative rulings — **in this repository**, **no npm package**, **no `fugaro voice` command** — are all kept; what changes is *where in the repository*, because there are now **two** consumers, the console and the app (D125). A module inside `console/` that the app also imports makes the app's build depend on the console's directory and on the console's release path. The package holds exactly what needs no runtime: the **message schema** and its validation, the **event digester / classifier**, the **redactor**, the **digest and severity rules**, and the **URL validator** (F27). Nothing in it imports Electron, React Native, Node or a database. It is tested by `go test` (or a runtime-free `npm test`), so **neither runtime is needed in CI**. | **Leave it inside `console/` exactly as D50 wrote it, and let the app import across.** Cost: the app's build reaches into the console's artifact — the coupling D125 rules out — and the shared module's tests run only when the console's do. **Or publish it on a registry.** Cost: D50 says **no npm package**, and publishing a Fugaro internal makes it a public API with a compatibility promise nobody asked for. **Or copy the logic into the app:** two digesters that disagree — and they disagree about **what the phone says out loud**. | |
| **D120** | **The Expo build and store release path.** EAS, TestFlight, the Play Console. | **Owner-run, always.** The Apple and Google accounts, the EAS project, the signing keys and the submissions are the owner's; **no agent runs `eas build` or `eas submit` and none touches a signing key or a store credential**. CI at most builds a **test** artifact with no store credential. The app is versioned with the CLI tag (D11's precedent) but released on the **stores' own cadence**. | **An agent runs the builds.** Cost: store credentials and signing keys in an agent's environment — the one thing this row recommends against — and a store submission is irreversible in the "it is public now" sense. | |
| **D121** | **Push notifications.** FCM/APNs is a new service; D49 listed "no push server" among the reasons the relay was not built. | **v1 ships without push.** The voice feature is a foreground activity, and the offline state comes from the heartbeat (D117). If push is wanted, it is a **separate decision** with its own service, its own device-token store and its own retention question. | **Add FCM/APNs in v1.** Cost: a new backend service the spec's "no new backend" principle forbids, device tokens stored somewhere (a new privacy surface), and Apple/Google push credentials to manage. | |
| **D122** | **OS-level hands-free audio.** The spec wants no push-to-talk; the console's design denies every web permission including the microphone (§5.1 rule 9). | **On the phone the microphone is an OS permission** with a usage-description string **the owner writes** and a packaged ask **the owner runs**. The audio session is configured for record-and-playback; the background audio mode is declared **only if barge-in needs it**; Bluetooth input goes through the platform's audio route; and the app **degrades to a visible "tap to speak" control** when the OS refuses background audio or the microphone. The console's deny-every-permission rule is **not inherited as-is** — it is about a web view, not a native app — and the difference is stated, not assumed. | **Push-to-talk** (drop hands-free). Cost: the spec's primary interaction is gone. **Or declare every background mode and permission** to make barge-in easy: store-review questions, and a permission prompt that asks for more than the app uses. | |
| **D123** | **On-device STT/TTS: the bake-off.** The spec's open question; D46 already said bake-off **by measurement**, local candidates only in v1, cloud only by a second explicit decision under a default-deny allow-list. | **Inherit D46.** A measurement-first bake-off with a **stated latency target** — first partial transcript, and end-of-speech to spoken-reply — measured on the **oldest supported device**, with the candidates being the platform's own on-device engines (Apple's and Android's) plus at most one bundled engine. The result is a **decision row**, not a preference. | **Pick a cloud STT/TTS now.** Cost: D44's default-deny allow-list and a second explicit decision, plus spoken content leaving the device (D118). **Or ship without a bake-off:** a latency target nobody measured, on the interaction the whole app exists for. | |
| **D124** | **The job list's source, and the PR link.** The spec says "read directly from RTDB" and "PR link when ready". | **The job list reuses the documented feed** (`console/docs/json-contract.md`, `fugaro watch --json`) and its **reader rules** (§10.13), not a new reader. "Hide old completed by default" is the **app's own** filter over an unfiltered feed, because the CLI's filter is a view with a **local ack file the phone cannot share** (F2). The PR link is opened **only after the console's validation** — `https:`, an exact host allow-list, no userinfo, no port — which today is prose (F27) and must be **written once, in the shared package** (D119), before either artifact opens a link. | **Open whatever string the feed carries.** Cost: a link from a database into a phone's browser is an exfiltration path, and the console already ruled that a host check is the **second** gate, never the first. **Or no PR link in v1:** the spec's per-job field is dropped — and note that only the **finished** document carries a URL (`JSONFinished`'s `pr_url`/`pr_number`; the live row has no PR field at all — gap 6), and that **no shape carries a provider**, so no link may be built from any of them until the host allow-list exists (gap 7). | |
| **D125** | **Release placement: is the app its own artifact?** | **Yes.** Its own artifact, its own directory **outside the Go module** (like `console/`), its own release path and cadence, and it **depends on** the shared voice module (D50, whose location D119 amends) rather than containing it. It is **not** part of the CLI tag's release gate. | **Fold the app into the CLI release** (one tag releases everything, as `release.md` mostly does). Cost: a store submission is not a `goreleaser` step and cannot be rolled back the way a release can — and the app's CI would need the store credentials `scripts/release-gate.sh` deliberately does not have. | |
| **D126** | **Testing without a live cloud, and which checks are owner-run.** | **Everything that can be a pure function is one:** the message schema, the digest, the redactor, the URL validator, the offline/staleness rule, the multi-account isolation rule and the mailbox state machine are tested against **fakes** (AudioIn, STT, TTS, the database, a deterministic mailbox simulator) with no runtime and no network. The rules change is tested in the **emulator** (`rules` CI job, `-tags firebase`). The live behaviours — a real phone writing a real inbox message, a real microphone, a real push — are **owner-run checks 80–89**, pasted into the pull request. | **Put the live behaviour in CI.** Cost: CI has no microphone, no phone and no store build; the test would be skipped and the check would be a lie. **Or no CI at all for this work:** the mailbox's semantics are the part most likely to be wrong, and they are exactly the part that is a pure function. | |
| **D127** | **The interrupt switch.** The spec wants a configurable switch for whether proactive updates can interrupt. | **A local setting on the phone**, gating a **pure function** of the digest (severity, plus whether the user is speaking). It is testable in CI, and the setting **never reaches the database**. | **An orchestrator-side setting.** Cost: the phone would have to write its preference to the database — another phone write and another grant (D113) — for a preference that is per-device. | |
| **D128** | **Destructive actions from the phone.** D47 ruled no destructive voice commands in v1. The mailbox is a channel into an agent that can run `fugaro cancel`, `fugaro budget kill` and `gh pr merge` — and [voice.md](../design/voice.md) F38 records that **no argv table fences that**. | **Inherit D47.** v1's mailbox is a **conversation** channel: the orchestrator's actions on it are the same ones the operator could take at the keyboard, **no new authority is granted by speaking**, and **no destructive command is recognised from a voice-originated message**. If a destructive action is wanted, it is a **typed confirmation on the phone** — or a second explicit decision — never a spoken one. | **Allow destructive voice commands.** Cost: a misheard word merges a pull request; D47 exists for exactly this. **Or fence the mailbox's consumer with an argv table** (the console's approach): the orchestrator is an **agent**, not an allow-list of argv, and voice F38 says the table does not exist and would not hold. | |
| **D129** | **The amendments this work is owed.** If D110, D112 and D119 are answered as recommended, **three** cells become stale: Voice **D49**'s answer cell (D110 supersedes it), Voice **D50**'s answer cell (**D119 amends where the shared module lives**), and the console design's statement of what the app reads (D112). | **A separate, later, owner-approved docs-only change** amends [voice.md](../design/voice.md)'s **D49** cell **and its D50 cell**, cross-references both from the console design, and records D112's transport in the console design's own terms. **This work records the supersessions as conditional on the owner's answers and edits nothing.** | **Amend them in this pull request.** Cost: it would **silently reverse a recorded ruling**, which the brief forbids. **Or never amend them:** two documents disagree about whether the mobile relay is scheduled and where the shared module lives, and the next reader cannot tell which is current. | |

---

## 4. Open for the owner

**Everything in this section is a D-row above. Nothing here is answered, and this work does not answer it.**

**The gating six — no app code may be written before these are answered.** They decide whether the work exists at all and what its substrate is:

1. **D110** — does this work supersede Voice D49, or is the mailbox not built?
2. **D111** — what may the app hold?
3. **D112** — direct database reads, the CLI's read path, or the transport that carries it — and which of the three.
4. **D113** — the rules change: what nodes, what a phone may write, and the owner-run deploy.
5. **D113b** — who writes the outbox and the heartbeat, and how that writer is authenticated.
6. **D114** — the account model, including that `belong` is added by hand.

**The rest, by the thing they gate:**

7. **D115** — phase 2 hosting, and who holds the model credential in the cloud. Gates nothing in phase 1; gates the spec's "app unchanged" claim.
8. **D116** — the summariser's spend and its on-screen disclosure.
9. **D117** — presence: the interval, the staleness bound, clock skew, and what "offline" means.
10. **D118** — retention, cleanup, redaction, and the never-store-audio rule.
11. **D119** — where the shared package lives (it **amends D50**) and how it is tested without either runtime.
12. **D120** — the EAS and store path, owner-run.
13. **D121** — push: v1 or later.
14. **D122** — OS hands-free audio: background mode, the microphone ask, Bluetooth, and the degradation path.
15. **D123** — the STT/TTS bake-off and its latency target.
16. **D124** — the job list's source and the PR link's validation.
17. **D125** — release placement.
18. **D126** — what CI proves and which checks are owner-run.
19. **D127** — the interrupt switch.
20. **D128** — destructive actions from the phone.
21. **D129** — the amendments owed to `voice.md` (D49 **and D50**) and the console design.

**What this work resolves without asking.** For completeness, these are findings, not decisions, and the plan acts on them: F1, F2, F3, F4, F5, F6, F7, F8, F9, F10, F13, F16, F18, F19, F20, F21, F23, F25, F26, F28, F29, F30, F31, F32, F33, F34, F35, F36, F37 — each states its own resolution, and none of them requires a ruling. Where a finding is classified **Wrong** (F11, F12, F14, F15, F27) the correction is a fact about the tree, and the *response* to it is the D-row it points at, not a decision of its own.

---

## 5. UNVERIFIED

Claims this work could not settle by reading the tree, with what would settle each. **None of these is load-bearing for a decision above; each is load-bearing for an implementation step, and the plan says which step.**

| # | The claim | Why it is unverified | What would settle it |
|---|---|---|---|
| U1 | The **deployed** rules in any live installation match `internal/budget/rules/testdata/rules.golden.json` | No live project was queried; the golden pins the *generated* text, and `projectpolicy.PathRulesMarker()` / `RulesVersion` exist precisely because a project can be behind | `fugaro doctor` (or the rules-marker read) against a live installation, owner-run |
| U2 | A Firebase Auth user in a live installation can read **nothing** today | The claim is about the template and the emulator, which is the strongest claim available here; the emulator is not the deployed rules | The owner-run rules deploy plus a signed-in read attempt (check 81) |
| U3 | The exact **host allow-list** for the PR link | The rule is design prose and names no list (F27); the providers are named, the hosts are not | The owner's answer to D124, then a written list in the shared package |
| U4 | Which **on-device STT/TTS** engines exist on the oldest supported device, and their latency | No device was measured; this is D123's whole point | The bake-off (check 85) |
| U5 | RTDB **write cost and bandwidth** for a heartbeat at a given interval | No live billing data was read | The owner's answer to D117, then a live measurement (check 84) |
| U6 | Whether Expo/EAS can build this app **without store credentials in CI** for a test artifact | No Expo project exists and no EAS run was attempted | The owner's answer to D120, then a build (check 89) |
| U7 | Whether the OS grants **background audio with barge-in** on both platforms | No device was tested | Check 86 |
| U8 | Whether `belong`'s installation has a Firebase project at all | **`belong` is never touched by any agent** (F31), so this was not looked up, on purpose | The owner, by hand |
