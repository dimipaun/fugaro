# Implementation plan: the Fugaro mobile app

**Goal.** An Expo (React Native) phone app that shows a Fugaro Watch–style view of running jobs and gives hands-free voice conversation with the orchestrating agent, reusing the voice module the console work plans — without weakening anything this repository has already ruled about credentials, data planes and the database.

**Status: plan only; nothing is implemented.** No app, no mailbox, no shared module and no rules change exists. **Every decision this plan depends on is unanswered** ([reconciliation.md](reconciliation.md) D110–D129), and the plan may not start before the gating six are answered and the gate at the end of this file is passed.

**Spec.** [spec.md](spec.md) (the owner's spec, verbatim). **Reconciliation.** [reconciliation.md](reconciliation.md) — findings F1–F37, decisions D110–D129, open items. **Index.** [README.md](README.md).

**Related.** [voice.md](../design/voice.md) · [2026-10-16-voice.md](../plans/2026-10-16-voice.md) · [desktop-console.md](../design/desktop-console.md) · [desktop-console-v2.md](../design/desktop-console-v2.md) · [managed-projects.md](../design/managed-projects.md) · [console/docs/json-contract.md](../../console/docs/json-contract.md) · [gcp-live-checklist.md](../gcp-live-checklist.md)

---

## Builds on merged code

This plan builds on what is already on `main`, and it **reuses** rather than rebuilds. Each item is a finding in the reconciliation.

| What exists | Where | Used by |
|---|---|---|
| The job feed as a documented contract | `internal/cli/watch.go`'s `newWatchCmd`/`watchOptions`, `internal/watch/json.go`'s `BuildJSON`/`JSONDoc`/`JSONRun`/`JSONFinished`/`JSONTotal`/`JSONRepo`/`JSONBurn`/`JSONKill`/`JSONConn`, `console/docs/json-contract.md` §1–§10 (F1) | P3.2, P3.3 |
| The reader rules for a long-lived child — **rules about documents, not a supervisor** | `console/docs/json-contract.md` §10.13 (F35); §10.11's "yes, a supervisor" is about the console's desktop sidecar, not the phone | P3.2 |
| The operator's application-default credential, and the fact the CLI holds no other | `internal/cli/cloud.go`'s `google.DefaultTokenSource` feeding `rtdb.New` (F22) | P3.2, P4.1 |
| The degraded shape, and the non-JSON line | `internal/cli/watch.go`'s `runWatchDegraded`/`watchDegradedDoc`/`degradedNotice`, §10.8 (F34) | P3.2 |
| The hide-old-completed view rule | `internal/watch/filter.go`'s `Filter`/`Apply`, `internal/watch/acks.go`'s `AckPath`, `plugin/skills/working/reference/status.md` (F2) | P3.3 |
| The launch claim, and its TTL | `internal/runstore/launch.go`'s `Claim`/`ClaimKey`/`ClaimTTL`/`ReadClaim` (F4) | P1.2 |
| The rules as a generated artifact | `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`'s `Generate()`, `testdata/rules.golden.json`, `parity_test.go`, the `firebase`-tagged emulator harness (F20) | P2.1, P2.2 |
| The rules marker and its version | `internal/projectpolicy/paths.go`'s `PathRulesMarker()`, `internal/projectpolicy/version.go`'s `RulesVersion` (F20) | P2.1 |
| The boundary between this module and a sibling artifact | `internal/cli/docs_console_boundary_test.go`'s `consoleBoundaryAllowList` (F9) | P3.1 |
| The sweeper shape | `internal/budget/sweep.go` (F26) | P4.1 |
| The check register | `docs/gcp-live-checklist.md` (F30) | P4.2 |

**No existing CLI command changes its behaviour.** The only Go this plan touches is `internal/projectpolicy/version.go` (the `RulesVersion` bump, P2.1), the rules template's **tests** (P2.1, P2.2 — the template itself is JSON, not Go) and the boundary allow-list's one deliberate entry (P3.1). **Where the consumer (P4.1) lives is D113c's open question**: in Voice D40's helper it is no Go at all; as a new `fugaro` command it is `internal/cli/mailbox.go` plus one registration line in `internal/cli/root.go` — and then, under D19, it is written in a CLI plan rather than here. Nothing here rewrites a command that exists.

---

## Phase summary

| Phase | What it is | Tasks | Mergeable independently? |
|---|---|---|---|
| **0 — The gate and the substrate** | The answered decisions written back; the shared package's skeleton; the message schema; the URL validator | P0.1–P0.4 | Yes — the skeleton is a package with no consumers, and the write-back is docs |
| **1 — The pure semantics** | The mailbox's state machine, the lease, the digest and redactor, the staleness rule, the simulator — all pure, all testable with no runtime | P1.1–P1.5 | Yes — each module is a directory with its own tests |
| **2 — The rules** | The template's new subtree, the golden and parity, the emulator's denial cases, and the owner-run deploy | P2.1–P2.3 | Yes, with one caveat — **P2.2 is a test file**; **P2.1 edits the production rules template**, which ships only when the owner deploys it (P2.3), and **P2.3 itself is a live deploy and is not mergeable by an agent** |
| **3 — The app** | The skeleton and account store, the feed reader, the job list, the audio interfaces, the mailbox client, the voice screen | P3.1–P3.6 | Yes — the app is its own artifact and merges without touching the CLI |
| **4 — The other end, the checks, the release** | The consumer beside the orchestrator, checks 80–89, the CI job, the store path | P4.1–P4.4, P4.2a | P4.1 yes, once **D113c** says where it lives; P4.2a and P4.2 write the register, and **the checks they write, P4.3 and P4.4 are owner-run** |
| **5 — Later, unscheduled** | Phase 2 hosting, push, the cloud STT/TTS allow-list | P5.1–P5.3 | Unscheduled by construction |

---

## Check numbers this plan takes (80–89)

**The owner allocated 80–89 to this work on 2026-10-16**, in advance. They are taken as allocated, **not** derived from the next free number in the register ([gcp-live-checklist.md](../gcp-live-checklist.md) numbers to 58 on `main`; 59–61 and 69a–69c belong to other lines of work). **A reserved number is never reused and never renumbered.**

**All ten are owner-run.** No agent performs any step of any of them, and none of them is a CI job.

| # | The check | Task | Why it cannot be a test |
|---|---|---|---|
| **80** | The mailbox end to end against a real installation: the phone writes an inbox message, the orchestrator acts, the reply reaches the outbox and is spoken | P4.2 | Needs a real phone, a real orchestrator and a real project |
| **81** | A signed-in user can write **only** its own inbox messages and the ack on a message it has spoken (D113's two writes), and can read **nothing** else — against the **deployed** rules | written by P4.2a; run at P2.3 and P4.2 | The emulator proves the template; only a deploy proves the project |
| **82** | The rules deploy (`fugaro init --firebase`), and the marker reads back the new `RulesVersion` | written by P4.2a; run at P2.3 | A live deploy; no agent applies rules |
| **83** | Replay and duplicate delivery: the same message delivered twice is acted on **once** | P4.2 | Needs a real network that can actually duplicate |
| **84** | Presence: the heartbeat's staleness, the offline state, and a clock skew shown as "clock differs" | P4.2 | Needs a real clock and a real database |
| **85** | The on-device STT/TTS bake-off, with its latency numbers, on the oldest supported device | P5.3, P4.2 | Needs real audio hardware |
| **86** | Hands-free audio: the background mode, the microphone ask, Bluetooth, and barge-in | P3.5, P4.2 | Needs a real device and a real headset |
| **87** | Multi-account switching with no cross-leakage | P3.1, P4.2 | Needs two real installations, one of them `belong` — **added by hand** |
| **88** | Push delivery — **only if D121 says yes** | P5.2 | Needs a real push service |
| **89** | A signed EAS build, and the store's internal track | written by P4.2a; run at P4.4 | Needs the owner's signing keys and store accounts |

**Checks 81, 82 and 89 are written into the register by P4.2a — before P2.3 and P4.4 run them — and checks 80 and 83–88 by P4.2; all ten are run by the owner.** A check is never run before its text is in the register. Until they are run, the app's live behaviour is **unverified**, and no task below may claim otherwise.

---

## Global Constraints

1. **Docs only until the gate passes.** This pull request adds `docs/mobile/*` — `README.md`, `spec.md`, `reconciliation.md` and `implementation-plan.md` — and nothing else. No app code, no rules change, no workflow file, no Go change.
2. **The gating six come first.** P0.1 onward starts only after the owner answers **D110, D111, D112, D113, D113b and D114** and passes the gate at the end of this file. The recommendations in the reconciliation are **not** answers.
3. **No ruling is reversed silently.** Any edit to [voice.md](../design/voice.md)'s D49 cell **or its D50 cell (the amendment D119 makes)**, to [desktop-console.md](../design/desktop-console.md), or to the console v2 pair is a **separate, owner-approved, docs-only change** (D129). If D110 is answered "no", this plan's phases 1, 3 and 4 are not built at all.
4. **The app holds exactly two items per account, both in platform secure storage, both never logged and never in a settings file: a Firebase Auth ID token, and — only if D113b's pairing-key question is answered (a) — the per-pairing MAC key** (D111's recommendation; the owner's answer governs). The pairing key is a **new secret**; the laptop keeps its copy in the **OS keychain**, and the consumer's settings hold only the keychain item's **name** (Voice D42(2)). Never a Fugaro or cloud admin credential, never an LLM key, never a service-account JSON, never a `fugaro` config file.
5. **No direct database reads by the app** (D112's recommendation; the owner's answer governs). The app's reads go through the CLI's documented feed — but **the transport that carries that feed to the phone is not chosen yet**: D112 puts a relay document, LAN/VPN access to the laptop, and a narrowly-scoped direct read in front of the owner, and **P3.2 may not start until one of them is chosen**.
6. **The phone writes two paths and no others, both inside its own account's mailbox subtree** (F21, D113): (a) its own **inbox messages** — one `.write` on the message node, in the subtree idiom the template already uses for `agents/$slug/$run`, with `.validate`-only children and `"$other"` denied — and (b) the **ack on a message it has already spoken**, which is **one write more than "inbox messages only" and needs the owner's explicit approval** (P3.6, D113). The **heartbeat is not the phone's write**: the spec has the orchestrator write it, and that writer's identity and grant are **D113b**. A rules change that would let a phone write any other path is not acceptable, and the emulator asserts the denial.
7. **Every rules change is owner-deployed**, with the regenerated golden, the parity test, a `RulesVersion` bump and the emulator's denial cases, and with the deploy performed by the owner through `fugaro init --firebase` (F20, D113).
8. **No agent edits `.github/workflows/`.** GitHub refuses Fugaro's push of workflow changes, so the CI job the shared package needs (P4.3) is **owner-made**, and this plan describes the change rather than making it.
9. **Store credentials and signing keys are owner-run.** No agent runs `eas build`, `eas submit`, or touches a signing key or a store credential (D120).
10. **No test may need a live cloud, a real device or a real microphone.** Those are checks 80–89, owner-run (F30). Every CI-assertable behaviour is a pure function over fakes.
11. **`belong` is never touched by an agent**, and no fixture is ever recorded from it (F31). The `belong` account is added by the owner by hand.
12. **The app never stores or uploads audio** (D118's recommendation). STT is on-device; the mailbox carries text.
13. **Every task's verification names the exact command**, and every verification runs without a live cloud, a device or a network.
14. **A task marked SECURITY-SENSITIVE gets an independent review** (an opus review on the diff, by a reviewer that did not write it) before it merges. The tasks marked below are: P0.3, P1.1, P1.2, P2.1, P2.2, P3.1, P3.6, P4.1.

---

## What may start before the decisions are answered

**Nothing.** The owner's instruction for the **website** work was to start as soon as we can; **no equivalent instruction exists for the mobile work**, and this plan does not borrow one. The spec arrived with eleven labelled conflicts against rulings this repository has already made, and every one of them is a decision row with an **empty** answer column (D110–D129). Until the owner answers:

- **No app code, no shared package, no rules change.** P0.1 and everything after it is gated (Global Constraint 2).
- **No task may treat a recommendation as an answer.** The recommendations exist so the owner can accept them quickly, not so a reader can assume them.
- **What is allowed now:** reading these four documents, reviewing this pull request, and answering the rows. **P0.4 is the only task that acts on an answer**, and it is itself gated on the answers arriving.

**The cheapest thing the owner can do is answer the gating six.** They decide whether the work exists at all (D110), what credential the app holds (D111), how the phone reads and by what transport (D112), what the database permits (D113), who writes the other side of the mailbox (D113b) and what an account is (D114). If the owner says "start", that is the gate passed for those rows and for nothing else.

---

## Review Focus

A reviewer of **this pull request** (docs only) should check:

- **That no ruling is reversed.** Every place the spec departs from [voice.md](../design/voice.md), [desktop-console.md](../design/desktop-console.md) or the console v2 pair appears as a **D-row with an empty answer** (D110–D129), and nothing in the two documents asserts a supersession as fact.
- **That the numbering is as allocated** — D110–D129, checks 80–89 — and that nothing is renumbered or reused.
- **That every finding's evidence is a file and a symbol that exist on `origin/main` at `76f6c59`**, and that the three corrections in §1 of the reconciliation (no `firebase/` directory, no `validateExternalUrl`, no `docs/design/status.md`) are true.
- **That no task claims a test protects something it does not.** `docs/mobile/` is invisible to the test suite (F33), so the documents' truth is the plan's job, not a test's.
- **That the plan's parallelism is real** — that no level contains two writers of one file, and that the critical path is the one the plan names.

A reviewer of a **task** should read that task's own "Reviewer tier" line. The SECURITY-SENSITIVE tasks are the rules change and its denials (P2.1, P2.2), the consumer (P4.1), the app's writes (P3.6), the account store (P3.1), the lease (P1.2), the dedupe and replay rule (P1.1), and the URL validator (P0.3) — because each of them is a boundary where a mistake is not a bug but an opening.

---

## File Structure

### Created

| Path | Task | What it is |
|---|---|---|
| `shared/package.json`, `shared/README.md`, the runner config | P0.1 | The shared pure-logic package's skeleton. **Its test script is a glob**, so no later task edits it (see "Shared files") |
| `shared/mailbox-schema/` | P0.2 | The message schema and its validation |
| `shared/url/` | P0.3 | The URL validator — `https:`, an exact host allow-list, no userinfo, no port (F27) |
| `shared/mailbox/` | P1.1 | The mailbox's state machine: message ids, at-least-once, dedupe, replay protection |
| `shared/lease/` | P1.2 | The consumer lease, in the shape of `runstore.Claim` |
| `shared/digest/` | P1.3 | The event digester, the severity rule, and the redactor |
| `shared/presence/` | P1.4 | The staleness and offline rule |
| `shared/mailbox-sim/` | P1.5 | The deterministic mailbox simulator the end-to-end tests run against |
| `internal/budget/rules/emulator_mailbox_test.go` | P2.2 | The emulator's denial cases |
| `mobile/` (the app: `package.json`, `app.json`, `src/`) | P3.1–P3.6 | The Expo app, its own artifact, outside the Go module |
| The consumer beside the orchestrator | P4.1 | **Decided by D113c (OPEN):** under (i) the helper's consumer module, outside the Go module, and no Go file; under (ii) `internal/cli/mailbox.go` and its tests — in a CLI plan (D19), not this one |

### Edited

| Path | Task | The edit |
|---|---|---|
| `docs/mobile/reconciliation.md`, `docs/mobile/implementation-plan.md`, `docs/mobile/README.md` | P0.4 | The owner's answers, written into the empty column, and the plan updated to match |
| `internal/budget/rules/rules.json.tmpl` | P2.1 | The new mailbox and heartbeat subtree |
| `internal/budget/rules/testdata/rules.golden.json` | P2.1 | Regenerated |
| `internal/projectpolicy/version.go` | P2.1 | `RulesVersion` bumped |
| `docs/design/m9-budget-and-dashboard.md` | P2.1 | The mailbox subtree and the heartbeat, in the data-model section (§6.2) and the security section (§6.4) — **the document that describes the database tree**, named rather than alluded to |
| `internal/cli/docs_console_boundary_test.go` | P3.1 | **One** deliberate allow-list entry, if the boundary test needs it |
| `internal/cli/root.go` | P4.1, **only if D113c is answered (ii)** | **One line**, registering the new command in the existing `root.AddCommand(...)` |
| `docs/gcp-live-checklist.md` | P4.2a, P4.2 | Checks 81, 82 and 89 (P4.2a); checks 80 and 83–88 (P4.2) |
| `.github/workflows/ci.yml` | **P4.3 — owner-made, no agent** | A job running the shared package's tests |

**Not touched by any task:** [voice.md](../design/voice.md), [desktop-console.md](../design/desktop-console.md), [desktop-console-v2.md](../design/desktop-console-v2.md) (D129), and everything under `docs/` that this plan does not name.

---

## PR groups

| Group | Tasks | One PR? |
|---|---|---|
| **P-A** | P0.1, P0.2, P0.3 | One PR — the shared package's skeleton and its first two modules |
| **P-B** | P1.1, P1.2, P1.3, P1.4, P1.5 | One PR, or five — the modules are independent directories |
| **P-C** | P2.1, P2.2 | **One PR, together** — a template change and the tests that pin it never split |
| **P-D** | P3.1, P3.2, P3.3, P3.4, P3.6 | One PR per task, or grouped by dependency |
| **P-E** | P3.5 | Its own PR — the voice screen is the feature |
| **P-F** | P4.1 | Its own PR, SECURITY-SENSITIVE |
| **P-G** | P0.4, P4.2a, P4.2 | Docs PRs; P0.4 is the only PR that writes answers |
| **P-H** | P2.3, P4.3, P4.4 | **No PR.** Owner-run |

---

## Parallel execution (the owner's explicit request)

### The edges, from the "Depends on" lines

```
P0.1 ──┬─► P0.2 ──► P1.1 ──┬─► P1.5
       │                   ├─► P3.6 ──► P3.5
       │                   └─► P4.1
       ├─► P0.3 ──► P3.3
       ├─► P1.2 ──┬─► P1.5
       │          ├─► P3.5
       │          └─► P4.1
       ├─► P1.3 ──┬─► P3.5
       │          ├─► P3.6
       │          └─► P4.1
       ├─► P1.4 ──┬─► P3.5
       │          └─► P3.6
       └─► P4.3

P2.1 ──► P2.2 ──► P4.2a ──┬─► P2.3
                          └─► P4.4
P2.1 ──────────────► P4.1

P3.1 ──┬─► P3.2 ──► P3.3
       ├─► P3.4 ──┐
       ├─► P3.6 ──┼─► P3.5
       └─► P4.4

every task a check covers ──► P4.2
```

**Every arrow is a "Depends on" line below. There is no edge that is not written down.**

### The shared files, from the full Files lists — no level contains two writers of one

| File | Written by | Why no other task may write it |
|---|---|---|
| `shared/package.json` and the runner config | **P0.1 only** | Its test script is a **glob** (`shared/**/*.test.js`), so every later module is picked up without an edit. This is deliberate: without the glob, five tasks would all edit one file and the level would collapse |
| `mobile/package.json` and its runner config | **P3.1 only** | Same rule, same reason |
| `internal/budget/rules/rules.json.tmpl` | **P2.1 only** | P2.2 adds a **new test file** and does not edit the template |
| `internal/budget/rules/testdata/rules.golden.json` | **P2.1 only** | Regenerated in the same commit as the template, as the parity test requires |
| `internal/projectpolicy/version.go` | **P2.1 only** | The bump is part of the same change |
| `docs/design/m9-budget-and-dashboard.md` | **P2.1 only** | It is the document that describes the database tree (§6.2, §6.4, §6.5); the template and its description move together |
| `internal/cli/docs_console_boundary_test.go` | **P3.1 only** | If the allow-list needs an entry, it is added once, in the change that creates the thing it admits |
| `docs/gcp-live-checklist.md` | **P4.2a** (level 2: checks 81, 82, 89) **and P4.2** (level 5: checks 80, 83–88) | Two writers at **different levels**, never the same one. P2.3 and P4.4 **run** checks 81, 82 and 89 and do not write the register — which is why P4.2a comes before them |
| `docs/mobile/*` | **P0.4 only** | It is the only task that writes an answer |
| `.github/workflows/ci.yml` | **P4.3, owner-made** | No agent writes it (Global Constraint 8) |

### Levels

| Level | Tasks | Approved? | Reviewer | Files touched |
|---|---|---|---|---|
| **0** | **P0.1** the shared skeleton · **P2.1** the rules template, golden, parity, version bump · **P3.1** the app skeleton and account store · **P0.4** the answered decisions, written back | **No** — the gate is not passed | P0.1 standard · **P2.1 SECURITY-SENSITIVE** · **P3.1 SECURITY-SENSITIVE** · P0.4 standard | `shared/package.json` · `internal/budget/rules/rules.json.tmpl`, `testdata/rules.golden.json`, `internal/projectpolicy/version.go`, `docs/design/m9-budget-and-dashboard.md` · `mobile/`, and `internal/cli/docs_console_boundary_test.go` if it needs its one entry · `docs/mobile/*` |
| **1** | **P0.2** the message schema · **P0.3** the URL validator · **P1.2** the lease · **P1.3** the digest and redactor · **P1.4** the staleness rule · **P2.2** the emulator's denial cases · **P3.2** the feed reader and its document rules · **P3.4** the audio interfaces and fakes · **P4.3** the CI job (owner-made) | No | P0.2 standard · **P0.3 SECURITY-SENSITIVE** · **P1.2 SECURITY-SENSITIVE** · P1.3 standard · P1.4 standard · **P2.2 SECURITY-SENSITIVE** · P3.2 standard · P3.4 standard · P4.3 owner | `shared/mailbox-schema/` · `shared/url/` · `shared/lease/` · `shared/digest/` · `shared/presence/` · `internal/budget/rules/emulator_mailbox_test.go` · `mobile/src/feed/` · `mobile/src/audio/` · `.github/workflows/ci.yml` |
| **2** | **P1.1** the mailbox state machine · **P3.3** the job list · **P4.2a** checks 81, 82 and 89, written into the register | No | **P1.1 SECURITY-SENSITIVE** · P3.3 standard · P4.2a standard | `shared/mailbox/` · `mobile/src/jobs/` · `docs/gcp-live-checklist.md` |
| **3** | **P1.5** the mailbox simulator · **P3.6** the app's mailbox client · **P4.1** the consumer · **P2.3** the owner-run deploy · **P4.4** the store path (owner-run) | No | P1.5 standard · **P3.6 SECURITY-SENSITIVE** · **P4.1 SECURITY-SENSITIVE** · P2.3 owner · P4.4 owner | `shared/mailbox-sim/` · `mobile/src/mailbox/` · the consumer's files, which **D113c** decides (the helper's module, or `internal/cli/mailbox.go` and one line of `internal/cli/root.go` in a CLI plan) · none (a live deploy) · none (the owner's accounts) |
| **4** | **P3.5** the voice screen | No | standard | `mobile/src/voice/` |
| **5** | **P4.2** checks 80 and 83–88, written into the register | No | standard | `docs/gcp-live-checklist.md` |
| **unscheduled** | **P5.1** phase 2 hosting · **P5.2** push · **P5.3** the cloud STT/TTS allow-list | **No, and not scheduled** | — | — |

**Six levels, and up to nine tasks in one level.** Level 1 is by far the widest — the shared package's modules are parallel with each other and with the app's own work — and the width is real rather than a scheduling wish: level 1's nine tasks write **nine different paths** and no two of them touch one file. The owner-run P2.3 and P4.4 sit at level 3 because each runs a check P4.2a must write first.

### The critical path

**P0.1 → P0.2 → P1.1 → P3.6 → P3.5 → P4.2.** Six tasks deep, and it is the **voice** path, which is what the spec calls the primary feature. Every other chain is shorter:

- The job list: P3.1 → P3.2 → P3.3 → P4.2 (four).
- The rules: P2.1 → P2.2 → P4.2a → P2.3 → P4.2 (five).
- The consumer: P0.1 → P0.2 → P1.1 → P4.1 → P4.2 (five).

**The critical path is shortened by exactly one thing and it is worth stating:** P0.2 (the message schema) sits on it, and it is a **small** task — a schema and its validation. If it is allowed to grow into "the schema and everything that validates against it", the critical path grows with it. P1.1's own validation belongs in P1.1.

**The critical path is not shortened by adding people to level 1 or 2.** They are already wide.

### What must be sequential, and why

1. **P0.1 before every other shared task.** The runner config exists once; a module that lands before it has no test to run.
2. **P2.1 before P2.2.** The emulator cases assert the denials of the *generated* rules; a denial case written against a template that is still moving is a test of a draft.
3. **P2.2 before P2.3.** The deploy ships rules that the emulator has already proven. Deploying first and testing after inverts the only guarantee the rules pipeline has.
3a. **P4.2a before P2.3 and P4.4.** A check is written into the register before anyone runs it; P2.3 runs checks 81 and 82, and P4.4 runs check 89.
4. **P1.1 before P3.6 and P4.1.** The phone and the consumer must agree on the message id, the dedupe rule and the ack; both ends of a protocol cannot be written against a protocol that does not exist.
5. **P1.2 before P4.1.** The consumer holds the lease. There is no consumer without it.
6. **P0.3 before P3.3.** The job list opens a link; the validator that gates the opening must exist before the code that opens.
7. **P3.4 before P3.5.** The voice screen is built against the audio interfaces; the fakes come first, which is also what makes P3.5's logic testable in CI at all.
8. **The owner's answers before everything.** D110 decides whether phases 1, 3 and 4 exist. D113 decides what P2.1 may grant. D114 decides P3.1's store. **This is not a scheduling preference; it is the gate.**

### What is *not* sequential, and a reviewer should confirm it stays that way

- **The rules work (P2.1, P2.2) does not depend on the app at all.** It can land, and be deployed, before a line of the app exists — and it should, because check 81 is what proves the phone's write is fenced.
- **The shared package's five modules do not depend on each other** except P1.1 → P0.2. The lease, the digest, the staleness rule and the URL validator are four independent directories.
- **The app's feed reader (P3.2) depends on nothing this plan builds** — except **D112's transport answer**, which is a decision, not a task. It consumes a contract that is already on `main`.
- **P0.4 (the write-back) depends on the owner, not on any task.** It can land first, last, or any time in between.

---

## Phase 0 — The gate and the substrate

### Task P0.1: The shared package's skeleton, with a glob test script

**Files.** `shared/package.json`, `shared/README.md`, the runner config, one trivial module with a test.
**Depends on.** D119 answered. Nothing else.
**Verification.** The runner runs the trivial test locally and it passes; `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` still passes, proving the new directory is invisible to the Go suite.
**Reviewer tier.** standard.

**Why it is first.** It is the one file every other shared task would otherwise have to edit, and the glob is what makes levels 1 and 2 wide. If the glob is dropped in review, the plan's parallelism drops with it.

### Task P0.2: The message schema and its validation

**Files.** `shared/mailbox-schema/`.
**Depends on.** P0.1.
**Verification.** The module's own tests: a message with a missing id, a wrong type, an oversized body and an unknown field is refused; a well-formed one is accepted. No runtime, no network.
**Reviewer tier.** standard.

**The schema is the protocol.** It is what P1.1 validates against, what P3.6 writes and what P4.1 reads. It is deliberately **small** — see the critical path.

### Task P0.3: The URL validator, written once

**Files.** `shared/url/`.
**Depends on.** P0.1.
**Verification.** Tests asserting: `https:` only; an exact host from the allow-list only; a URL with userinfo refused; a URL with a port refused; a URL whose host merely *ends with* an allow-listed host refused; a non-URL refused. Plus the negative control: a string the feed carries that is not a URL is never passed through.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**Why it exists as its own task.** The console's rule is design prose with no list and no code (F27), and the app must not open a link before it exists (Global Constraint 5's spirit, D124). Writing it here, once, means the console can adopt it later rather than reimplementing it. **The allow-list itself is the owner's answer to D124**, and until then the module ships with an empty list and a test that an empty list refuses everything.

### Task P0.4: The owner's answers, written back

**Files.** `docs/mobile/reconciliation.md` (the answer column), `docs/mobile/implementation-plan.md` (the tasks the answers unblock or cancel), `docs/mobile/README.md` (the status line, if it changes).
**Depends on.** The owner's answers. Not on any task.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.
**Reviewer tier.** standard.

**This is the only task in the plan that writes an answer**, and it writes the owner's, not its own. If D110 is answered "no", this task also deletes the phases that answer cancels, and says so in its commit message.

---

## Phase 1 — The pure semantics

*Five modules, each its own directory, each independently mergeable, each testable with no runtime and no network.*

### Task P1.1: The mailbox's state machine — message ids, at-least-once, dedupe, replay protection

**Files.** `shared/mailbox/`.
**Depends on.** P0.2.
**Verification.** Tests over the schema: a message carries an id; delivering the same id twice is **acted on once** and the second delivery is acknowledged as a duplicate, not re-acted; an id from a different sender is not deduped against; a message older than the window is refused as a replay; the state machine's transitions are total (every state × every event has a defined outcome, asserted by an exhaustive table test).
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**Why it is the load-bearing module.** F19 records that the tree has no message id, no ordering, no idempotency, no replay protection and no ack. This module is where all five are decided, and it is what answers D49's "no ack, no expiry, no dedupe" objection **in code rather than in prose**.

### Task P1.2: The consumer lease, in the shape of `runstore.Claim`

**Files.** `shared/lease/`.
**Depends on.** P0.1.
**Verification.** Tests asserting: a lease has a holder and a timestamp; a second holder within the TTL is refused with an `already-claimed` answer; a lease past its TTL can be taken; a lease released explicitly can be taken immediately; the TTL is a parameter with the shape of `ClaimTTL`'s ten minutes as its default.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**The shape is not invented here.** `internal/runstore/launch.go`'s `Claim`/`ClaimKey`/`ClaimTTL`/`ReadClaim` is the tree's only claim (F4), and this module is that shape, so that the reason two launchers cannot start the same run twice becomes the reason two consumers cannot act on the same message twice.

### Task P1.3: The digest, the severity rule and the redactor

**Files.** `shared/digest/`.
**Depends on.** P0.1.
**Verification.** Tests over the contract's documents (`JSONRun`, `JSONFinished`, `JSONTotal`, `JSONBurn`, `JSONKill`): a kill is the highest severity; a failed finish is next; a fast burn is next; nothing else interrupts; a missing field is `unavailable` and **never** spoken as `0`; the redactor removes token-shaped and key-shaped strings, and its tests include the shapes this tree actually uses. The severity rule is asserted as a **total order** over an exhaustive input table.
**Reviewer tier.** standard.

**Why the redactor is here and not in the app.** It is the same redaction D118 requires before anything is written, and D119 puts it in the shared package so that the console and the phone cannot disagree about what is safe to say out loud.

### Task P1.4: The staleness and offline rule

**Files.** `shared/presence/`.
**Depends on.** P0.1.
**Verification.** Tests: a heartbeat younger than N intervals is `live`; older is `stale`; far older is `offline`; a heartbeat whose timestamp is in the future beyond the bound is `clock differs`, **not** `offline`; a missing heartbeat is `offline`, never `live`. The rule is a pure function of (heartbeat, now, interval).
**Reviewer tier.** standard.

**Why the clock case is a test and not a comment.** D117's recommendation is that the **server's** timestamp is the authority; the test is what makes that true rather than aspirational.

### Task P1.5: The deterministic mailbox simulator

**Files.** `shared/mailbox-sim/`.
**Depends on.** P1.1, P1.2.
**Verification.** The end-to-end mailbox test runs against the simulator with no runtime and no network: a phone writes, a consumer leases, acts, acks and replies, the phone reads the reply; then the simulator **duplicates** a delivery and the outcome is unchanged; then it **reorders** two deliveries and the outcome is the same; then it **drops** the ack and the consumer re-delivers.
**Reviewer tier.** standard.

**This is what makes check 80 a formality rather than a discovery.** The live check confirms the simulator was right; it does not find out what the semantics are.

---

## Phase 2 — The rules

### Task P2.1: The template's new subtree, the golden, the parity and the version bump — SECURITY-SENSITIVE

**Files.** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`, `internal/projectpolicy/version.go`, and **the document that describes the database tree: `docs/design/m9-budget-and-dashboard.md`** — §6.2 ("Data model") for the tree, §6.4 ("Security: per-run tokens and database rules (D1)") for what the rules permit, §6.5 ("The agent registry without `onDisconnect`") for the heartbeat's nearest precedent.
**Depends on.** D113 and D113b answered.
**Verification.** `go test ./internal/budget/rules/...` — `TestRulesGolden`, `TestGeneratedRulesParse` and `TestRulesParity` — and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` for the document (it is a `docs/design/*.md` file, so the docs tests see it). **No emulator case here**; that is P2.2.
**Reviewer tier.** **SECURITY-SENSITIVE — MANDATORY independent opus review.** This is the widest change in the plan: it is the only one that changes what the database permits.

**What it adds.** One subtree, for the mailbox and the heartbeat, and **four writes in it, one per writer and no more**: the phone's inbox message, the phone's ack on a message it has spoken (D113), the consumer's outbox message and the consumer's heartbeat (D113b). The phone's grants go through a **user claim** carried in the Firebase Auth token — a claim the rules *read*, not one they mint. Its shape follows the template's **subtree idiom**, which is the one `agents/$slug/$run` and `outcomes/$day/$slug/$run` already use (F21): **one `.write` on the message node**, whose predicate is the phone's own claim; `.validate`-only children, so the validators (`STR`, `AMOUNT`, `RBOK`) gate the values and a bound gates the body's length; and `"$other": { ".validate": false }` so an unknown field is refused rather than stored. **Not** the leaf idiom — the budget and lease families put a `.write` on each leaf, which is the wrong shape for a node written whole by one writer.

**What it must not do.** Loosen any existing `.read` or `.write` (F11, F13). Grant a phone anything but the two writes D113 names — its own inbox messages and the ack on a message it has spoken (F21). Grant the **consumer** — D113b's writer, wherever D113c puts it — anything but the outbox and the heartbeat (D113b). Admit a signed-in user anywhere else (D14's argument, F8). Each of those is a review question and each has an emulator case in P2.2.

### Task P2.2: The emulator's denial cases — SECURITY-SENSITIVE

**Files.** `internal/budget/rules/emulator_mailbox_test.go`.
**Depends on.** P2.1.
**Verification.** `go test -tags firebase ./internal/budget/rules/...` against the pinned `firebase-tools`. The cases **assert denials**, and there are at least: a signed-in user writing outside its own inbox is refused; writing into another user's inbox is refused; reading `/config/**` is refused; reading `/fugaro/**` is refused; reading `/outcomes/**` is refused; a message over the size bound is refused; a message missing the schema's required fields is refused; **a write to the outbox or the heartbeat by anything other than D113b's writer identity is refused**; **an ack on a message that is not one's own is refused**; and the positive controls — writing one's own well-formed inbox message, and acking one's own message, both succeed.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**Why the denials are the point.** A rules test that only asserts what succeeds proves the feature works; it does not prove the fence holds. F21 is a claim about what a phone **cannot** do, and this is the only place it is tested.

### Task P2.3: The owner-run deploy — check 82

**Files.** None. **Owner-run.**
**Depends on.** P2.2, P4.2a (check 82's and check 81's text must be in the register before they are run).
**Verification.** The owner runs `fugaro init --firebase` against a real installation, the marker reads back the new `RulesVersion` (`internal/projectpolicy/paths.go`'s `PathRulesMarker()`), and check 82 is recorded with its `FACT:` lines. **No agent performs any step.**
**Reviewer tier.** owner.

**This task is not mergeable by an agent and produces no commit of its own** beyond the recorded check result. It exists in the plan because the rules change is not done when the test passes — it is done when the project is running the new rules.

---

## Phase 3 — The app

*The app is its own artifact, outside the Go module (D125), with its own release path.*

### Task P3.1: The app skeleton and the account store — SECURITY-SENSITIVE

**Files.** `mobile/` (its `package.json` with a glob test script, its app config, `src/`), and `internal/cli/docs_console_boundary_test.go` if the boundary test needs one allow-list entry.
**Depends on.** D114, D125 answered.
**Verification.** The app's own test runner runs a trivial test; `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` passes, and the boundary test — whichever way it is written — passes.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**What the account store is.** An account is an installation: one Firebase project, one operator identity, one Fugaro project name (D114). The store holds **several, non-overlapping**: a per-account secure-storage item, a per-account cache namespace, no shared token store, and a switcher that **tears down the previous account's listeners and caches before it opens the next**. The tests assert the isolation as a property: with two accounts configured, no read from one returns a value belonging to the other, and switching leaves no listener from the previous account alive.

**`belong` is added by hand** (Global Constraint 11). No test fixture is ever recorded from it.

### Task P3.2: The feed reader and the contract's document rules

**Files.** `mobile/src/feed/`.
**Depends on.** P3.1, **and D112's answer** — the transport that carries the feed to the phone.
**Verification.** Tests over recorded documents, with **no child process and no network**: a non-JSON line is skipped; a missing field becomes `unavailable` and never `0`; the newest document is not accumulated onto the previous one; a changed `day` is treated as a break; the degraded shape renders as degraded rather than as zero spend; and **a feed that stops arriving is not treated as a dead process** — silence is not death (§10.13).
**Reviewer tier.** standard.

**Every rule in this task is a rule the contract already states** (`console/docs/json-contract.md` §10.13, F35), and they are rules about **documents**, which is why they carry over to a phone. **The task builds no child-process supervisor**: §10.11's "does option A need a supervisor process? Yes" is about **the console** — a desktop sidecar beside a binary the operator has, `desktop-console.md` D13's binary path — and a phone has no such thing: there is no iOS or Android build of the Go CLI, and a phone could not execute one if there were. **What is *not* designed is how the document reaches the phone**, and that is **D112's open decision**; this task does not start until it is answered.

### Task P3.3: The job list

**Files.** `mobile/src/jobs/`.
**Depends on.** P3.2, P0.3.
**Verification.** Tests: a completed job older than the keep window is hidden by default and shown when the toggle is on; a **failed** job is kept by the failed-keep rule; a queued job is shown; the per-job fields the spec names are rendered when present and marked `unavailable` when not; a PR link is opened **only** through P0.3's validator, and a link the validator refuses is rendered as text and not as a link.
**Reviewer tier.** standard.

**Two things this task must not do.** It must not claim the desktop's acknowledgement semantics — the ack file is local to the machine running the CLI and a phone cannot share it (F2). And it must not build a link at all until P0.3's validator exists with a non-empty host allow-list: the **finished** document carries `pr_url`/`pr_number` (`JSONFinished`), but **no shape carries a provider**, so the host is unknown and a `github.com` guess is wrong for a Bitbucket repository (F27, gaps 6 and 7). The **live** row has no PR field at all, so it has no link to build until gap 6 closes on the CLI side.

### Task P3.4: The audio interfaces and their fakes

**Files.** `mobile/src/audio/`.
**Depends on.** P3.1.
**Verification.** The interfaces exist (AudioIn, STT, TTS) and their fakes are used by at least one test that exercises the voice pipeline's logic with **no device and no audio**: a fake transcript becomes a message; a fake reply becomes an utterance; an interrupted utterance is reported as interrupted.
**Reviewer tier.** standard.

**Fakes from task one** is Voice D52's rule, and it is what keeps the voice screen's logic in CI (Global Constraint 10).

### Task P3.6: The app's mailbox client — SECURITY-SENSITIVE

**Files.** `mobile/src/mailbox/`.
**Depends on.** P1.1, P1.3 (the redactor D118 requires before anything is written), P1.4, P3.1.
**Verification.** Tests over the simulator and the fakes: the client writes **one** message per utterance, with a fresh id; it writes **no other path**; it reads only its own outbox; **it acks a message only after it has spoken it** — and that ack is the plan's **second, explicitly-allowed phone write**, which the owner approves with D113 and P2.1's template grants on the message's own `acked` child and nowhere else; it re-delivers an unacked message after a reconnect and the duplicate is not acted on twice; and **it writes no heartbeat** — the heartbeat is the orchestrator's (D113b), and the app only **reads** it, through P1.4's rule.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**This is the only place in the app that writes to the database**, its test list is deliberately written as a list of things it does **not** do, and its write set is exactly **two paths**, both named in D113 and both granted in P2.1's template. The ack is the one that would otherwise be smuggled in: "the phone only writes its own inbox messages" and "the phone acks what it has spoken" are not the same sentence, and D113 is where the owner is asked to approve the second.

### Task P3.5: The voice screen — hands-free, barge-in, the interrupt switch, the offline state

**Files.** `mobile/src/voice/`.
**Depends on.** P3.4, P3.6, P1.2, P1.3 (the digest's severity), P1.4.
**Verification.** Tests over the fakes: with the interrupt switch off, a proactive update is queued and not spoken over the user; with it on, it is spoken at the next boundary and **never** over an in-progress utterance; the offline state is shown from P1.4 and the app **refuses to accept a message** while offline; a lease held by another consumer is surfaced rather than ignored; and the digest's highest severity is the only thing that interrupts when the switch is on.
**Reviewer tier.** standard.

**Why the offline refusal is a test.** The spec's own sentence — "rather than accepting messages into the void" — is the requirement, and it is testable without a device.

---

## Phase 4 — The other end, the checks, the release

### Task P4.1: The consumer beside the orchestrator — SECURITY-SENSITIVE

**Files.** **Decided by D113c, which is OPEN.** Under **(i)** the consumer is a module of Voice D40's helper, outside the Go module, with its tests, and **no Go file** is touched. Under **(ii)** it is `internal/cli/mailbox.go`, its tests and **one line in `internal/cli/root.go`** registering the command — and then, by D19's rule, this task is written and approved in **a CLI plan**, not here.
**Depends on.** P1.1, P1.2, P1.3 (the redactor D118 requires before anything is written), P2.1, and **D113c answered**.
**Verification.** Tests over the simulator: the consumer leases before it acts; a lease it does not hold means it does not act; it acks after it acts; **if D113b's pairing-key question is answered (a), every message it puts in the outbox carries the pairing MAC over its id, text and timestamp, with the key read from the OS keychain by the item name its settings hold and never logged**; a duplicate delivery is not acted on twice; a message it cannot parse is refused and **not** retried forever; and the consumer **never** runs a destructive command from a voice-originated message (D128's recommendation — the test asserts the refusal, and it is the test that makes D47's rule true on this channel).
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.**

**Why this is the most dangerous task in the plan.** The consumer acts on the operator's behalf, next to an orchestrator that can run `fugaro cancel`, `fugaro budget kill` and `gh pr merge` — and [voice.md](../design/voice.md) F38 records that **no argv table fences that**. D128 is the decision that governs it, and this task's tests are where the answer becomes a fence.

### Task P4.2: Checks 80–89, written into the register

**Files.** `docs/gcp-live-checklist.md`.
**Depends on.** Every task whose behaviour a check covers.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, and the owner's runs recorded with their `FACT:` lines.
**Reviewer tier.** standard for the writing; **owner** for the running.

**Writing the checks is a task; running them is the owner's.** The plan's rule is that no task claims a live behaviour works until its check has been run — and the checks are written so that the owner can run them without an agent.

### Task P4.2a: Checks 81, 82 and 89, written into the register before they are run

**Files.** `docs/gcp-live-checklist.md` — checks **81** (the deployed rules' fence), **82** (the rules deploy and the marker) and **89** (the signed EAS build): the register entries and the text of the steps the owner runs.
**Depends on.** P2.2 (check 81 is the live counterpart of its denial cases, and check 82 reads back the bumped `RulesVersion` those cases run against).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.
**Reviewer tier.** standard.

**Why it is split from P4.2.** P2.3 and P4.4 run these three checks, and they sit at level 3; P4.2 sits at level 5. A check that is run before its text is in the register is a check nobody can repeat.

### Task P4.3: The CI job for the shared package's tests — owner-made

**Files.** `.github/workflows/ci.yml`. **Owner-made; no agent edits it** (Global Constraint 8).
**Depends on.** P0.1.
**Verification.** The owner confirms the job runs and fails on a deliberately broken test.
**Reviewer tier.** owner.

**This task exists because of a hard constraint, not a preference.** GitHub refuses Fugaro's push of workflow changes, so the CI job the shared package needs cannot be written by an agent in this repository's current setup. The plan therefore records the change — a job that installs the shared package's dependencies and runs its test script, alongside the existing Go job and the `rules` job — and leaves it to the owner.

### Task P4.4: The EAS project, the signing keys and the store entries — owner-run

**Files.** None in this repository. **Owner-run.**
**Depends on.** P3.1, P4.2a (check 89's text must be in the register before it is run).
**Verification.** Check 89: a signed build reaches the store's internal track. **No agent performs any step**, and no signing key or store credential enters an agent's environment (Global Constraint 9).
**Reviewer tier.** owner.

---

## Phase 5 — Later, unscheduled

**None of these is approved, and none is scheduled.** Each is recorded so that a later reader finds the reasoning rather than rediscovering the question.

### Task P5.1: Phase 2 hosting — the always-on orchestrator — UNSCHEDULED

**Depends on.** D115 answered, and [managed-projects.md](../design/managed-projects.md) D55–D78. **Files.** None yet. **Verification.** None until scheduled. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled.

It holds a model credential in the cloud (which D40/D42/D54b deliberately kept on the operator's machine) and it holds `fugaro run` authority: budget caps, the launch claim, the approval model (F17).

### Task P5.2: Push — UNSCHEDULED

**Depends on.** D121 answered "yes". **Files.** None yet. **Verification.** Check 88. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled.

### Task P5.3: The cloud STT/TTS allow-list — UNSCHEDULED

**Depends on.** D123 answered, and Voice D44's **second explicit decision**. **Files.** None yet. **Verification.** Check 85 records the bake-off; a cloud decision would need its own check. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled.

---

## Cross-task dependencies

| Task | Depends on | And on these decisions |
|---|---|---|
| P0.1 | — | D119 |
| P0.2 | P0.1 | D110, D119 |
| P0.3 | P0.1 | D124 |
| P0.4 | — | **all of D110–D129, D113b and D113c included** |
| P1.1 | P0.2 | D110 |
| P1.2 | P0.1 | D110 |
| P1.3 | P0.1 | D110, D118 |
| P1.4 | P0.1 | D117 |
| P1.5 | P1.1, P1.2 | D110 |
| P2.1 | — | **D113, D113b** |
| P2.2 | P2.1 | D113 |
| P2.3 | P2.2, P4.2a | D113 |
| P3.1 | — | **D111, D112, D114, D125** |
| P3.2 | P3.1 | **D112** (the transport), D111 |
| P3.3 | P3.2, P0.3 | D124 |
| P3.4 | P3.1 | D122, D123 |
| P3.5 | P3.4, P3.6, P1.2, P1.3, P1.4 | D122, D127 |
| P3.6 | P1.1, P1.3, P1.4, P3.1 | D111, D113, **D113b**, D118 |
| P4.1 | P1.1, P1.2, P1.3, P2.1 | D110, D128, **D113b**, **D113c**, D118 |
| P4.2a | P2.2 | D113, D120, D126 |
| P4.2 | all | D126 |
| P4.3 | P0.1 | D119 |
| P4.4 | P3.1, P4.2a | D120, D125 |
| P5.1–P5.3 | — | D115, D121, D123 |

---

## Decisions this plan defers to the owner

**These are the plan's own provisional choices, distinct from the reconciliation's D-rows.** Each is made here so the plan can be read as a whole; **the owner may override any of them at the gate**, and an override changes the tasks named.

| # | The choice this plan makes | The alternative, and its cost |
|---|---|---|
| **C1** | The shared package is **JavaScript/TypeScript**, because both consumers are | **Go**, with the console and the app calling into a binary — cost: a process boundary between the console's voice module and the shared logic, which is where D50 put it, and which D119 amends to a top-level directory |
| **C2** | The app lives at `mobile/`, outside the Go module, like `console/` | **Inside the module** — cost: it would have to be Go, or the module would carry a non-Go artifact |
| **C3** | The mailbox's node path is chosen in P2.1, in the template | Naming it now — cost: a name in a plan is a name two tasks can disagree about |
| **C4** | The heartbeat's default interval and its floor are chosen in P1.4, from the measurement in check 84 | A fixed interval now — cost: an interval chosen without the write-cost measurement D117 asks for |
| **C5** | The retention window is **not** this plan's choice: it is the owner's answer to **D118**, and P1.3 takes it as a parameter with no default of its own | A default picked in P1.3 — cost: a privacy-relevant number chosen without the owner, in the row D118 asks the owner to answer |
| **C6** | The app is versioned with the CLI's tag (D11's precedent) but released on the stores' cadence | Its own version line — cost: two version lines to reconcile, for an artifact that ships with the CLI's features |
| **C7** | **Withdrawn — now the OPEN decision row D113c.** Where the consumer lives is a choice between following Voice D40 and D50 (the helper) and departing from them with a new `fugaro` command that console D19 sends to a CLI plan, so it is the owner's question, not this plan's | — |
| **C8** | The store's internal track is used for check 89, and no public release is implied | A public release — cost: a public app is not reversible, and the spec does not ask for one |

---

## Open items, listed rather than fixed

1. **Two PR-link gaps, and they are not the same gap.** `JSONRun` (`internal/watch/json.go`) carries no PR field, so a **live** row has no URL at all (console §4.3 **gap 6**); `JSONFinished` **does** carry `pr_url` and `pr_number`, so a **finished** run has one. But **no shape carries a provider** (**gap 7**), so the URL cannot be validated against a host allow-list and the console builds **no link at all** — repository, number and URL as text. Until the allow-list exists (D124, P0.3) the app does the same, and the live half of the list cannot carry a link until gap 6 closes on the CLI side. **Neither gap is closed by this plan.**
2. **The ack file is local** (`internal/watch/acks.go`'s `AckPath`). The phone cannot share the desktop's acknowledgements, so the app's "show all" toggle is over the unfiltered feed. Stated in F2 and in P3.3; **not solved**.
3. **"Awaiting user input" has no producer** anywhere in the tree (F3, and Voice D48 reached the same conclusion). The digest cannot speak a state nothing produces.
4. **The CI job is owner-made** (P4.3), because of a hard constraint on workflow files, not because the plan prefers it. Until it exists, the shared package's tests run **only where someone runs them**, and that is a real gap in this plan's coverage.
5. **Checks 80–89 are owner-run and none has been run.** Until they are, every live claim in the app is unverified.
6. **`belong` has not been looked up** — not whether it has a Firebase project, not what its installation tuple is — because no agent touches it (F31, U8).
7. **The console's voice module does not exist** (Voice D50/D53), and **D119 amends where the shared code lives without making that module exist**. P1.3 and P1.5 are written for the shared package, which the console would consume; until the console's side exists the shared package has **one** consumer, not two, and "shared" is a claim about the future.

---

## OWNER APPROVAL GATE

**Nothing in this plan is approved.** The steps below are taken **in this order**. No task may start — not P0.1, not the docs write-back — before step 1 is done; after that, a task starts only when **every step that names it** is done and **every decision row its "Cross-task dependencies" line names** is answered.

**1. Answer the gating six.** **D110** (does this supersede Voice D49?), **D111** (what may the app hold?), **D112** (direct database reads, or the CLI's read path — **and which transport carries it**), **D113** (the rules change, and the owner-run deploy), **D113b** (who writes the outbox and the heartbeat, and how that writer is authenticated), **D114** (the account model). **If D110 is answered "no", phases 1, 3 and 4 are not built**, and this plan reduces to the job list and the pure modules that survive. **If D113 is answered "no rules change", there is no mailbox**, because the phone has nothing to write to and nothing to read.

**2. Answer the rest, or accept the recommendations in writing.** D113c and D115–D129 are not part of step 1, so they do not hold back a task that does not name them; each holds back exactly the tasks whose "Cross-task dependencies" line names it (D113c holds back P4.1, which has no recommendation to accept), and D126 gates whether checks 80–89 are the plan's proof at all.

**3. Approve Phase 0 and Phase 1 as a block.** They are pure, they need no live anything, they touch no existing behaviour, and they are the cheapest possible way to find out whether the mailbox's semantics are right. **Approving them is not approving the app.**

**4. Approve Phase 2 separately, and only then.** **P2.2 is a test-only change and is safe to merge.** **P2.1 is not test-only**: it edits the **production** rules template, so the next owner who runs `fugaro init --firebase` from a binary built on it **deploys it** — merging P2.1 is approving the content of that deploy, even though it changes nothing until someone deploys. **P2.3 is the deploy itself and needs its own explicit go**, per Global Constraint 7. Approving Phase 2's code is not approving its deploy.

**5. Approve Phase 3 only after the shared package exists** (D119, P0.1) **and the console's voice module exists** (Voice D53), or say explicitly that the app builds the pure parts ahead of it. **D119 amends D50 by moving the shared module to a top-level directory both artifacts consume**; the app's critical path runs through that package, and this plan does not hide it.

**6. Approve P4.1 separately.** It is the task where a phone's message becomes an action on the operator's behalf, and D128's answer is what it is built from — and **D113c's** answer decides whether it is built here (the helper) or in a CLI plan (a `fugaro` command).

**7. Answer C1–C8 (C7 withdrawn into D113c), or let them stand.** They are the plan's own provisional choices, and each names the task it changes.

**The recommendations in [reconciliation.md](reconciliation.md) are not answers.** This plan is written so that either answer to each decision leaves a coherent plan; it is not written so that the recommendations can be treated as settled.

---

*Status of this document: plan only; nothing is implemented. D110–D129 are unanswered (twenty rows, plus D113b and D113c). Checks 80–89 are reserved and none has been run. Related: [spec.md](spec.md) · [reconciliation.md](reconciliation.md) · [README.md](README.md).*
