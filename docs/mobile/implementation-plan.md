# Implementation plan: the Fugaro mobile app

**Goal.** An Expo (React Native) phone app that shows a Fugaro Watch–style view of running jobs and gives hands-free voice conversation with the orchestrating agent, reusing the voice module the console work plans — without weakening anything this repository has already ruled about credentials, data planes and the database.

**Status: plan only; nothing is implemented.** No app, no mailbox, no shared module and no rules change exists. **The owner answered every decision this plan depends on on 2026-10-17** ([reconciliation.md](reconciliation.md) D110–D129, D113b and D113c) — D112 **against** the recommendation (direct RTDB reads) and D113c as option (ii) (a new `fugaro` command) — and the answers are written back below (Task P0.4). **Answering a decision is not approving a task: nothing in this plan is approved**, the gate at the end of this file is not passed, and three tensions the answers create are open ([reconciliation.md](reconciliation.md) §4, "Open tensions").

**Status as of 2026-10-17, later (added by the functional-spec v0.1 reconciliation; the paragraph above is kept as written).** Some of this plan now exists: **P0.1, P0.2, P0.3, P1.2, P1.3 and P1.4 are merged on `main`** (`shared/`), the owner **approved Phase 2 (P2.1, P2.2) on 2026-10-17**, and **P2.1 is draft PR #532**. Nothing else is built. **Phase 6 below is proposed by [functional-spec-reconciliation.md](functional-spec-reconciliation.md) and was approved by the owner on 2026-10-18** — this clause said the opposite until then, and is corrected rather than left to read as a live bar — and its decisions D160–D172 were **answered by the owner on 2026-10-17** (D162 and D168 against the recommendation). Its tasks may start, each still waiting on the decision rows its line names; its rules changes landed **after #532** (O2). The owner's **amendment of D128 (2026-10-17)** — cancel, approve/merge and confirming an action allowed on the phone with a spoken read-back and an explicit "confirm" — is what P6.2 and P6.7 would build; recording it approves neither.

**Status as of 2026-10-19 (the demo mode; appended, like the paragraph above).** The owner **ruled on 2026-10-19** that the app carries a built-in **demo mode**: Google Play's reviewers cannot create an account and cannot pair a phone, so the app needs a way in that needs no setup at all. It is **not a task of this plan and takes no P-number** — it arrived beside the plan as a ruling, and its decisions are the ruling's, not D110–D129's — and it is implemented in `mobile/src/demo/` (the fixed sample data, the controller and the screen) with the entry at `mobile/src/app/first-screen.js`, the banner in the shell (`mobile/src/app/root.js`), the instructions in `mobile/README.md` ("Demo mode: how to enter it, and how to leave it") and the Play form text in [play-review-access.md](play-review-access.md). **P6.8 is the task it sits beside**: the pairing screen goes into the seam `src/app/first-screen.js` holds, above the "Try the demo" control rather than instead of it. The demo runs the app's own client, verification, prompt queue and confirmation machine on fixed sample data, and can never touch the network, secure storage or a credential (`mobile/src/demo/safety.test.js`). Recording the ruling approves no task of this plan.

**Status as of 2026-10-22 (the telemetry phase; appended, like the paragraphs above).** The owner **approved a seventh phase on 2026-10-22, on one condition: that its plan is merged first.** [../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md) is the plan for **Firebase Crashlytics and Firebase Analytics in the app**, reporting to a Firebase project **Fugaro operates and not an installation's**; it takes the decision block **D253–D260** (all eight answered by the owner on 2026-10-19, **D255 always on** against the plan's recommendation and **D259 accepted**) and **checks 120, 121 and 122**, and it is registered below as **Phase 7** and in the gate's **step 7b**. **Nothing of the phase is built** — no `@react-native-firebase` dependency, no native config file, no telemetry module — and **approval is permission to start, not a claim that anything exists**. It is the **replacement for the draft on PR #693**, which asked for **D193–D200** and **checks 111–113**: those were taken by other works before it merged (D193–D197 by the coordinator, 111–113 by the setup wizard and the website zoom-diagrams plan), so the rebuilt plan reads the highest numbers on `main` and takes the ones that were free. **The phase amends D111's record from two items per account to four**, and the amendment is carried as a dated note in **Global Constraint 4** above, in [reconciliation.md](reconciliation.md)'s **D111** row, and in the two design documents and the functional-spec reconciliation that repeat the sentence.

**Status as of 2026-10-30 (telemetry Task P7.1a; appended, like the paragraphs above).** **The first half of Task P7.1 is built**: `mobile/package.json` carries `@react-native-firebase/app`, `@react-native-firebase/analytics` and `@react-native-firebase/crashlytics` (`27.1.0`, exact) plus `expo-build-properties` (the iOS build needs dynamic frameworks, which its own plugin source says), `mobile/app.config.js` registers their config plugins, writes `googleServicesFile` from `GOOGLE_SERVICES_JSON` / `GOOGLE_SERVICE_INFO_PLIST` **only when each is set** and blocks `com.google.android.gms.permission.AD_ID`, `mobile/firebase.json` carries the four Android switches, and `mobile/.gitignore` ignores `google-services.json` and `GoogleService-Info.plist` so neither is committed. **The 2026-10-22 paragraph's "nothing of the phase is built" is the state of that date, kept as written**; **superseded 2026-10-30 by Task P7.1b**, which landed the build-time guard (**D260**) and the ad-ID walk, so **P7.1 is built** — and unbuilt remain **P7.2–P7.5**: the always-on statement, the event allow-list and its scan, `mobile/src/telemetry/`, and the store labels and the privacy paragraph. **(`shared/telemetry-consent/`, listed here as unbuilt in the first version of this paragraph, is not a task at all: the plan's §6 removed it with the consent gate D255's answer dropped — amended 2026-10-31.)** **D259 as amended on 2026-10-29**: this half writes no secure-store item and touches no file under `mobile/src/accounts/`.

**Spec.** [spec.md](spec.md) (the owner's spec, verbatim); [functional-spec-v0.1.md](functional-spec-v0.1.md) (the owner's functional spec, verbatim), reconciled in [functional-spec-reconciliation.md](functional-spec-reconciliation.md). **Reconciliation.** [reconciliation.md](reconciliation.md) — findings F1–F37, decisions D110–D129, open items. **Index.** [README.md](README.md).

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

**No existing CLI command changes its behaviour.** The only Go this plan touches is `internal/projectpolicy/version.go` (the `RulesVersion` bump, P2.1), the rules template's **tests** (P2.1, P2.2 — the template itself is JSON, not Go) and the boundary allow-list's one deliberate entry (P3.1). **Where the consumer (P4.1) lives is D113c's answer of 2026-10-17: a new `fugaro` command**, so it is Go under `internal/cli` — a new file (`internal/cli/mailbox.go`, the file of **`fugaro mailbox`**, named by the owner on 2026-10-17, tension (c)) plus one registration line in `internal/cli/root.go` — and under console D19 it is written in **a CLI plan** rather than here. Nothing here rewrites a command that exists.

---

## Phase summary

| Phase | What it is | Tasks | Mergeable independently? |
|---|---|---|---|
| **0 — The gate and the substrate** | The answered decisions written back; the shared package's skeleton; the message schema; the URL validator | P0.1–P0.4 | Yes — the skeleton is a package with no consumers, and the write-back is docs |
| **1 — The pure semantics** | The mailbox's state machine, the lease, the digest and redactor, the staleness rule, the simulator — all pure, all testable with no runtime | P1.1–P1.5 | Yes — each module is a directory with its own tests |
| **2 — The rules** | The template's new subtree, the golden and parity, the emulator's denial cases, and the owner-run deploy | P2.1–P2.3 | Yes, with one caveat — **P2.2 is a test file**; **P2.1 edits the production rules template**, which ships only when the owner deploys it (P2.3), and **P2.3 itself is a live deploy and is not mergeable by an agent** |
| **3 — The app** | The skeleton and account store, the feed reader, the job list, the audio interfaces, the mailbox client, the voice screen | P3.1–P3.6 | Yes — the app is its own artifact and merges without touching the CLI |
| **4 — The other end, the checks, the release** | The consumer beside the orchestrator, checks 80–89, the CI job, the store path | P4.1–P4.4, P4.2a | P4.1 yes — **D113c** answered it a new `fugaro` command, so it merges from a CLI plan (D19); P4.2a and P4.2 write the register, and **the checks they write, P4.3 and P4.4 are owner-run** |
| **5 — Later, unscheduled** | Phase 2 hosting, push, the local/remote voice evaluation switch, the conversational front agent | P5.1–P5.4 | Unscheduled by construction |
| **7 — APPROVED 2026-10-22 (telemetry, on merge of its plan)** | Firebase Crashlytics and Firebase Analytics in the app, reporting to a Firebase project **Fugaro operates and not an installation's**: the config plugin and the never-committed native files, the **always-on statement** and no consent gate (**D255**), the closed event allow-list and the scan that holds it, the store labels and the website's paragraph, checks 120–122 | P7.1–P7.5 | Yes, each — **the owner approved the phase on 2026-10-22, on the condition that its plan is merged first**, and each task still waits on the decision rows its line names — **all eight answered on 2026-10-19, D259's acceptance last** — so every "Depends on" line names a ruling. **D254 is an owner step** in a project that already exists (**`fugaro-prod`**, the owner's answer). **Approval is permission to start and nothing is built.** Its plan is [../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md) |
| **6 — APPROVED 2026-10-18 (functional spec v0.1)** | Wake-word routing and the app-command grammar, the spoken-confirmation machine, the schema and rules for agents/prompts/action records/the MAC, the prompt queue, `fugaro pair`, the consumer's enforcement, pairing and overview in the app, the text screen, the offline gate, checks 100–104 | P6.1–P6.13 | Yes, each — **the owner approved all of Phase 6 on 2026-10-18**, and each still waits on the D160–D172 rows its line names |

---

## Check numbers this plan takes (80–89)

**The owner allocated 80–89 to this work on 2026-10-16**, in advance. They are taken as allocated, **not** derived from the next free number in the register ([gcp-live-checklist.md](../gcp-live-checklist.md) numbers to 58 on `main`; 59–61 and 69a–69c belong to other lines of work). **A reserved number is never reused and never renumbered.**

**All ten are owner-run.** No agent performs any step of any of them, and none of them is a CI job.

| # | The check | Task | Why it cannot be a test |
|---|---|---|---|
| **80** | The mailbox end to end against a real installation: the phone writes an inbox message, the orchestrator acts, the reply reaches the outbox and is spoken | P4.2 | Needs a real phone, a real orchestrator and a real project |
| **81** | A signed-in user can write **only** its own inbox messages and the ack on a message it has spoken (D113's two writes), and can read **nothing** else *(amended 2026-10-17: nothing beyond the families D112's user-claim read grant names, tension (b))* — against the **deployed** rules | written by P4.2a; run at P2.3 and P4.2 | The emulator proves the template; only a deploy proves the project |
| **82** | The rules deploy (`fugaro init --firebase`), and the marker reads back the new `RulesVersion` | written by P4.2a; run at P2.3 | A live deploy; no agent applies rules |
| **83** | Replay and duplicate delivery: the same message delivered twice is acted on **once** | P4.2 | Needs a real network that can actually duplicate |
| **84** | Presence: the heartbeat's staleness, the offline state, and a clock skew shown as "clock differs" | P4.2 | Needs a real clock and a real database |
| **85** | The on-device STT/TTS bake-off, with its latency numbers, on the oldest supported device **— scope amended 2026-10-19: the same fixed prompts through the local engine and through each admitted remote provider, recording first partial, end-of-speech-to-reply, name error rate and cost per hour per engine** | P5.3, P4.2 | Needs real audio hardware |
| **86** | Hands-free audio: the background mode, the microphone ask, Bluetooth, and barge-in | P3.5, P4.2 | Needs a real device and a real headset |
| **87** | Multi-account switching with no cross-leakage | P3.1, P4.2 | Needs two real installations, one of them `belong` — **added by hand** |
| **88** | Push delivery — **only on the owner's later yes to push (P5.2)** (D121 was answered "no push in v1" on 2026-10-17) | P5.2 | Needs a real push service |
| **89** | A signed EAS build, and the store's internal track | written by P4.2a; run at P4.4 | Needs the owner's signing keys and store accounts |

**Checks 100–109 (approved 2026-10-18).** The functional-spec reconciliation took a second block, **100–109**, after reading every number in use on `main` and in every open pull request (credentials-service holds 90–99). Phase 6 proposes **100** (QR pairing end to end, with the code confirmation), **101** (revocation: deleting the device node denies the next read; the Auth user's token timing), **102** (the wake word's recognition rate on the oldest supported device), **103** (a spoken cancel and a spoken merge on the **sandbox repository only**, each with its read-back and "confirm", and the refusals: a misheard confirm, a replayed confirm, a confirm for another target), **104** (offline: no command is sent or queued while disconnected). **105–109 are reserved and unused** — **P6.11 writes 100–104 and is open as PR #667** — and the telemetry plan below **takes none of them** (*an unused number is not a free number*). All are owner-run; P6.11 writes 100–104 into the register.

**Checks 120, 121 and 122 (approved 2026-10-22).** The telemetry plan ([../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md), Phase 7) takes **three** numbers — **120** (the reports arrive, they arrive from the **first launch**, and they arrive at **`fugaro-prod`** and no other project, on a device, with the Firebase console as the second half of the proof — and the negative halves: no switch in the app turns them off, and the build carries no `AD_ID` permission) — **121** (a real crash on a freshly installed device reaches the Fugaro-operated project with **no advertising identifier** and nothing that names a message, a mailbox or an account in it, and the control is **the FIRST build, which carries neither SDK (D258), producing no report at all**) and **122** (the two store forms match what the app sends, category by category, and the live privacy statement carries the app's paragraph **sentence by sentence** on the day the first SDK-carrying build is submitted — the build does not ship until the two agree). **D255's answer is what 120 and 121 are written around**: there is no consent state to observe, so the proof is that the reports arrive from the first launch rather than that they wait for one. **They are the first free numbers after every claim, and not the first gap in the register**: 105–109 are **reserved and unused** — an unused number is not a free number, the rule this work applies to D79–D89 and D174–D179 — **110** is claimed by the always-on orchestrator design (open PR #632), which [design/voice-evaluation-switch.md](design/voice-evaluation-switch.md) (P5.3) also names and nobody writes, **111, 112 and 113 are written into the register** (the setup wizard's and the website zoom-diagrams plan's), **114–118 are the voice-session work's** ([voice-session/reconciliation.md](voice-session/reconciliation.md), its D219) and **119 is proposed by the console-on-Windows work** ([../console-windows/README.md](../console-windows/README.md), its D12(a)). **The plan takes nothing out of 100–109** — that block is what **P6.11 writes, open as PR #667** — and **nothing above 122**: **123 onward stays free**. All three are **owner-run** — no agent performs any step of any of them — and they are **written by P7.5** after the phase is built, and a check is never run before its text is in the register. **The draft on PR #693 asked for 111–113**, which the wizard and the website work had already written; this registration takes the numbers that were free when the plan was rebuilt. The amendment of the 100–109 row above is recorded there as a **dated note**, so the original reading stays visible.

**Checks 81, 82 and 89 are written into the register by P4.2a — before P2.3 and P4.4 run them — and checks 80 and 83–88 by P4.2; all ten are run by the owner.** A check is never run before its text is in the register. Until they are run, the app's live behaviour is **unverified**, and no task below may claim otherwise.

---

## Global Constraints

1. **Docs only until the gate passes.** This pull request adds `docs/mobile/*` — `README.md`, `spec.md`, `reconciliation.md` and `implementation-plan.md` — and nothing else. No app code, no rules change, no workflow file, no Go change.
2. **The gating six come first.** P0.1 onward starts only after the owner answers **D110, D111, D112, D113, D113b and D114** and passes the gate at the end of this file. The recommendations in the reconciliation are **not** answers. *(2026-10-17: the owner answered all six, and the rest; the gate is not passed, so no task may start.)*
3. **No ruling is reversed silently.** Any edit to [voice.md](../design/voice.md)'s D49 cell **or its D50 cell (the amendment D119 makes)**, to [desktop-console.md](../design/desktop-console.md), or to the console v2 pair is a **separate, owner-approved, docs-only change** (D129) — made on 2026-10-17 by the change that wrote these answers back, which amended Voice D40, D42, D49 and D50 keeping their original text visible and recorded D112 in the two console designs. If D110 is answered "no", this plan's phases 1, 3 and 4 are not built at all.
4. **The app holds exactly two items per account, both in platform secure storage, both never logged and never in a settings file: a Firebase Auth ID token, and — only if D113b's pairing-key question is answered (a) — the per-pairing MAC key** (**D111, answered 2026-10-17 as recommended**; the pairing key presupposes D113b's option (a), which D113b's answer does not state — tension (b)). The pairing key is a **new secret**; the laptop keeps its copy in the **OS keychain**, and the consumer's settings hold only the keychain item's **name** (Voice D42(2)). Never a Fugaro or cloud admin credential, never an LLM key, never a service-account JSON, never a `fugaro` config file. **Amended 2026-10-22 for the telemetry plan's D259 (which the owner accepted on 2026-10-19), and again 2026-10-29: the owner ruled that D232 stands** — the sentence above stays as written (D129's habit; **Global Constraint 12** is the same shape), and the list it closes stays **two items (idToken, macKey) plus the pairing-delivered configuration (the installation's database URL and web API key, ordinary configuration, not secret)**. D259's original four-item list is kept visible in the telemetry plan's D259 row, marked amended. The configuration arrives **inside the encrypted pairing payload** from the user's own installation (the CLI records both; G18 reads them for the QR), because it differs per installation and is not known at build time; **where the app stores it is the choice of task P6.8** of [../plans/2026-10-22-mobile-pairing-cli.md](../plans/2026-10-22-mobile-pairing-cli.md). What is **built into the app** instead is the *telemetry* project's two native config files, which are the same for every installation and are delivered as **EAS file secrets**, never committed. `SettingsStore`'s `SECRET_SHAPES` and `FORBIDDEN_KEY_SEGMENTS` are **not** relaxed. **Nothing is widened:** the code still allows only the two `SECRET_ITEMS` in `mobile/src/accounts/secret.js`, and `website/test/privacy.mjs` still requires exactly `idToken,macKey`. The record is [../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md) §4.4, and [reconciliation.md](reconciliation.md)'s **D111** row carries the same dated note.
5. ~~**No direct database reads by the app** (D112's recommendation; the owner's answer governs).~~ **Amended 2026-10-17 by D112's answer: the app reads the database directly** (the row's alternative (iii)), signed in with Firebase Auth, under a **user-claim read grant** the rules change adds (P2.1) and the emulator scopes (P2.2). This reverses console D1's "option B never" **for the mobile app only**. **Which families the grant covers, and whether the job list is read from them or from a mirror of the documented feed (D124), were open** — tensions (a) and (b) in [reconciliation.md](reconciliation.md) §4 — **and were resolved 2026-10-17: direct reads under the feed's reader rules, and a grant of exactly `config/caps/**`, `spend/$day/**`, `config/kill/**` and the phone's own mailbox (outbox, heartbeat) — not `agents/$slug/$run`, no new `.read` on `outcomes/**`.**
6. **The phone writes two paths and no others, both inside its own account's mailbox subtree** (F21, D113): (a) its own **inbox messages** — one `.write` on the message node, in the subtree idiom the template already uses for `agents/$slug/$run`, with `.validate`-only children and `"$other"` denied — and (b) the **ack on a message it has already spoken**, which is **one write more than "inbox messages only" and needs the owner's explicit approval** (P3.6, D113). The **heartbeat is not the phone's write**: the spec has the orchestrator write it, and that writer's identity and grant are **D113b**. A rules change that would let a phone write any other path is not acceptable, and the emulator asserts the denial. **Under D112's answer the phone also reads**, and its reads are fenced the same way: only the families the user-claim read grant names (tension (b)), with the emulator asserting the **read** denials too.
7. **Every rules change is owner-deployed**, with the regenerated golden, the parity test, a `RulesVersion` bump and the emulator's denial cases, and with the deploy performed by the owner through `fugaro init --firebase` (F20, D113).
8. **No agent edits `.github/workflows/`.** GitHub refuses Fugaro's push of workflow changes, so the CI job the shared package needs (P4.3) is **owner-made**, and this plan describes the change rather than making it.
9. **Store credentials and signing keys are owner-run.** No agent runs `eas build`, `eas submit`, or touches a signing key or a store credential (D120).
10. **No test may need a live cloud, a real device or a real microphone.** Those are checks 80–89, owner-run (F30). Every CI-assertable behaviour is a pure function over fakes.
11. **`belong` is never touched by an agent**, and no fixture is ever recorded from it (F31). The `belong` account is added by the owner by hand.
12. **The app never stores or uploads audio** (D118, answered 2026-10-17 as recommended). STT is on-device; the mailbox carries text. **Amended 2026-10-19, for the evaluation switch only** (the owner's answer to **Q0**, option (a), in [design/voice-evaluation-switch.md](design/voice-evaluation-switch.md) §1a, which states the scope in full and is the authority for it): **audio may be uploaded, only to a provider on the project's allow-list, only with the owner's remote switch on and consent recorded for that provider, never while a confirmation is pending and never on a destructive-command path, and never stored by the app beyond the live stream — no buffer, no spool, no retry queue, no file.** The sentence above is kept as written: **what it says about *storing* is not relaxed at all**, and it remains the rule for everything except the evaluation switch. Voice D44 stays the rule for what ships, and **adopting cloud voice is still a later decision**.
13. **Every task's verification names the exact command**, and every verification runs without a live cloud, a device or a network.
14. **A task marked SECURITY-SENSITIVE gets an independent review** (an opus review on the diff, by a reviewer that did not write it) before it merges. The tasks marked below are: P0.3, P1.1, P1.2, P2.1, P2.2, P3.1, P3.6, P4.1 — and, in Phase 6 (approved 2026-10-18), P6.1, P6.2, P6.3, P6.4, P6.6, P6.7, P6.8, P6.10, P6.12, P6.13.

---

## What may start before the decisions are answered

**Still nothing — as of 2026-10-17, for a different reason.** The owner answered every row on 2026-10-17, so the paragraph below no longer describes the state of the decisions; it is kept as written. What holds every task back now is the **approval gate**, which is not passed. The three tensions ([reconciliation.md](reconciliation.md) §4) that held back P2.1, P2.2, P3.1, P3.2, P3.3, P3.6 and P4.1 were **resolved by the owner on 2026-10-17**; resolving them approves no task. The one task the answers themselves perform is **P0.4**, the write-back.

*As written before the answers:* **Nothing.** The owner's instruction for the **website** work was to start as soon as we can; **no equivalent instruction exists for the mobile work**, and this plan does not borrow one. The spec arrived with eleven labelled conflicts against rulings this repository has already made, and every one of them is a decision row with an **empty** answer column (D110–D129). Until the owner answers:

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
| The consumer beside the orchestrator | P4.1 | **D113c answered (ii), 2026-10-17:** a new `fugaro` command, **`fugaro mailbox`** — `internal/cli/mailbox.go` (named by the owner 2026-10-17, tension (c)) and its tests — written in a CLI plan (D19), not this one |

**Created by Phase 6 (approved 2026-10-18):** `shared/command/` (P6.1) · `shared/confirm/` (P6.2) · `shared/prompts/` (P6.5) · `internal/budget/rules/emulator_pairing_test.go` (P6.4) · `internal/cli/pair.go` and its tests (P6.6, in a CLI plan per console D19) · `internal/budget/token/device.go` and its test, `internal/budget/sweep_device_test.go` (P6.6) · `mobile/src/text/` (P6.9) · `mobile/src/pairing/`, `mobile/src/overview/` (P6.8).

### Edited

| Path | Task | The edit |
|---|---|---|
| `shared/mailbox-schema/` | **P6.3 (approved 2026-10-18)** | The fields D161, D167, D168, D169 and D171 need: `agent`, `kind`, `options`, `reply_to`, the action record, `mac` (no `expires_at` field: under D168 as amended a prompt expires at D118's TTL, computed by the consumer from `at`) |
| `internal/budget/rules/rules.json.tmpl`, `rules.go`, `testdata/rules.golden.json`, `internal/projectpolicy/version.go`, `docs/design/m9-budget-and-dashboard.md` | **P6.4 (approved 2026-10-18)** | The same fields in the mailbox subtree, and the device-node conjunct in `USER` (D166) — after P2.1, never beside it |
| `internal/cli/mailbox.go`, `internal/cli/root.go`, `internal/budget/sweep.go` | **P6.7, P6.6, P6.13 (approved 2026-10-18)** | The consumer's enforcement (P6.7) and its sweeper rule for waiting prompts (P6.13); one registration line for `fugaro pair` and the sweep's `m~` rule (P6.6; no other task writes `sweep.go`) |
| `mobile/src/mailbox/`, `mobile/src/voice/` | **P6.10, P6.12 (approved 2026-10-18)** | The offline gate; the spoken confirmation and routing in the voice screen |
| `docs/gcp-live-checklist.md` | **P6.11 (approved 2026-10-18)** | Checks 100–104 |
| `docs/mobile/reconciliation.md`, `docs/mobile/implementation-plan.md`, `docs/mobile/README.md` | P0.4 | The owner's answers, written into the empty column, and the plan updated to match |
| `internal/budget/rules/rules.json.tmpl` | P2.1 | The new mailbox and heartbeat subtree |
| `internal/budget/rules/testdata/rules.golden.json` | P2.1 | Regenerated |
| `internal/projectpolicy/version.go` | P2.1 | `RulesVersion` bumped |
| `docs/design/m9-budget-and-dashboard.md` | P2.1 | The mailbox subtree and the heartbeat, in the data-model section (§6.2) and the security section (§6.4) — **the document that describes the database tree**, named rather than alluded to |
| `internal/cli/docs_console_boundary_test.go` | P3.1 | **One** deliberate allow-list entry, if the boundary test needs it |
| `internal/cli/root.go` | P4.1 (**D113c answered (ii)**; in the CLI plan) | **One line**, registering the new command in the existing `root.AddCommand(...)` |
| `docs/gcp-live-checklist.md` | P4.2a, P4.2 | Checks 81, 82 and 89 (P4.2a); checks 80 and 83–88 (P4.2) |
| `.github/workflows/ci.yml` | **P4.3 — owner-made, no agent** | A job running the shared package's tests |

**Not touched by any task:** [voice.md](../design/voice.md), [desktop-console.md](../design/desktop-console.md), [desktop-console-v2.md](../design/desktop-console-v2.md) (D129 — they were amended on 2026-10-17 by the separate docs-only change D129 names, not by a task of this plan), and everything under `docs/` that this plan does not name.

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
| **P-H** | P2.3, P4.3, P4.4 | **No PR for the owner-run steps.** P4.4's `mobile/eas.json` and `mobile/app.config.js` are committed code and do come in a PR; the EAS project, the signing keys and the store entries are the owner's and come in none |

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

Phase 6 — APPROVED 2026-10-18:
P0.1 ──┬─► P6.1 ──┬─► P6.8
       │          └─► P6.12
       └─► P6.2 ──┬─► P6.7
                  └─► P6.12
P6.3 ──► P3.6          (P3.6 signs inbox messages; added 2026-10-17)
P0.2 ──► P6.3 ──┬─► P6.4 ──┬─► P6.6 ──► P6.8
                │          ├─► P6.7
                │          └─► P6.11
                ├─► P6.5 ──┬─► P6.9
                │          └─► P6.12
                ├─► P6.7
                └─► P6.10
P2.2 ──► P6.4
P4.1 ──┬─► P6.6          (shared file: internal/cli/root.go)
       └─► P6.7          (shared file: internal/cli/mailbox.go)
P1.3, P1.4 ──► P6.5
P3.1 ──► P6.8
P3.6 ──┬─► P6.9
       └─► P6.10         (shared directory: mobile/src/mailbox/)
P3.5 ──► P6.12           (shared directory: mobile/src/voice/)
P6.7 ──► P6.13           (shared file: internal/cli/mailbox.go)
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
| **Phase 6 (approved 2026-10-18) adds these writers, each at a different level from the earlier writer of the same file:** | | |
| `shared/mailbox-schema/` | **P0.2** (level 1, merged) **and P6.3** (level 2) | P6.3 makes `mac` **required on every inbox and outbox message and on the heartbeat**, and adds the canonical serialisation; P1.1 (level 2, `shared/mailbox/`) validates messages and P3.6 (level 3) writes the inbox, so P3.6 depends on P6.3 and signs; P1.1 does not write the schema |
| `internal/budget/rules/rules.json.tmpl`, `rules.go`, `testdata/rules.golden.json`, `internal/projectpolicy/version.go`, `docs/design/m9-budget-and-dashboard.md` | **P2.1** (level 0) **and P6.4** (level 3) | P6.4 starts only after P2.2 has merged; if the owner folds P6.4's changes into draft #532 instead (open item O2), P6.4 shrinks to its emulator file |
| `internal/cli/root.go` | **P4.1** (level 3) **and P6.6** (level 4) | One registration line each; the explicit edge P4.1 → P6.6 keeps them apart |
| `internal/cli/mailbox.go` | **P4.1** (level 3), **P6.7** (level 4) **and P6.13** (level 5) | P6.7 extends the consumer P4.1 creates; P6.13 changes only its sweeper rule, after P6.7 |
| `mobile/src/mailbox/` | **P3.6** (level 3) **and P6.10** (level 4) | The offline gate wraps P3.6's client |
| `mobile/src/voice/` | **P3.5** (level 4) **and P6.12** (level 5) | The confirmation dialogue sits on P3.5's screen |
| `docs/gcp-live-checklist.md` | **P4.2a** (level 2), **P6.11** (level 4) **and P4.2** (level 5) | Three writers at three levels |

### Levels

| Level | Tasks | Approved? | Reviewer | Files touched |
|---|---|---|---|---|
| **0** | **P0.1** the shared skeleton · **P2.1** the rules template, golden, parity, version bump · **P3.1** the app skeleton and account store · **P0.4** the answered decisions, written back | **No** — the gate is not passed | P0.1 standard · **P2.1 SECURITY-SENSITIVE** · **P3.1 SECURITY-SENSITIVE** · P0.4 standard | `shared/package.json` · `internal/budget/rules/rules.json.tmpl`, `testdata/rules.golden.json`, `internal/projectpolicy/version.go`, `docs/design/m9-budget-and-dashboard.md` · `mobile/`, and `internal/cli/docs_console_boundary_test.go` if it needs its one entry · `docs/mobile/*` |
| **1** | **P0.2** the message schema · **P0.3** the URL validator · **P1.2** the lease · **P1.3** the digest and redactor · **P1.4** the staleness rule · **P2.2** the emulator's denial cases · **P3.2** the feed reader and its document rules · **P3.4** the audio interfaces and fakes · **P4.3** the CI job (owner-made) | No | P0.2 standard · **P0.3 SECURITY-SENSITIVE** · **P1.2 SECURITY-SENSITIVE** · P1.3 standard · P1.4 standard · **P2.2 SECURITY-SENSITIVE** · P3.2 standard · P3.4 standard · P4.3 owner | `shared/mailbox-schema/` · `shared/url/` · `shared/lease/` · `shared/digest/` · `shared/presence/` · `internal/budget/rules/emulator_mailbox_test.go` · `mobile/src/feed/` · `mobile/src/audio/` · `.github/workflows/ci.yml` |
| **2** | **P1.1** the mailbox state machine · **P3.3** the job list · **P4.2a** checks 81, 82 and 89, written into the register | No | **P1.1 SECURITY-SENSITIVE** · P3.3 standard · P4.2a standard | `shared/mailbox/` · `mobile/src/jobs/` · `docs/gcp-live-checklist.md` |
| **3** | **P1.5** the mailbox simulator · **P3.6** the app's mailbox client · **P4.1** the consumer · **P2.3** the owner-run deploy · **P4.4** the store path (owner-run) | No | P1.5 standard · **P3.6 SECURITY-SENSITIVE** · **P4.1 SECURITY-SENSITIVE** · P2.3 owner · P4.4 owner | `shared/mailbox-sim/` · `mobile/src/mailbox/` · the consumer's files — **D113c answered (ii)**: `internal/cli/mailbox.go` (placeholder name) and one line of `internal/cli/root.go`, in a CLI plan · none (a live deploy) · `mobile/eas.json` and `mobile/app.config.js` (the store path's two committed files — the EAS project, the signing keys, the store entries and the identity values are the owner's, and come in none) |
| **4** | **P3.5** the voice screen | No | standard | `mobile/src/voice/` |
| **5** | **P4.2** checks 80 and 83–88, written into the register | No | standard | `docs/gcp-live-checklist.md` |
| **unscheduled** | **P5.1** phase 2 hosting · **P5.2** push · **P5.3** the local/remote voice evaluation switch · **P5.4** the conversational front agent | **No, and not scheduled** | — | — |

**Phase 6 — APPROVED 2026-10-18 — placed on the same levels** (a task is on the level after its deepest dependency; no level gains a second writer of a file, per the shared-files table):

| Level | Phase 6 tasks | Approved? | Reviewer | Files touched |
|---|---|---|---|---|
| **1** | **P6.1** wake-word router and app-command grammar · **P6.2** the spoken-confirmation machine | **No** | **P6.1 SECURITY-SENSITIVE** · **P6.2 SECURITY-SENSITIVE** | `shared/command/` · `shared/confirm/` |
| **2** | **P6.3** the schema extension | **No** | **SECURITY-SENSITIVE** | `shared/mailbox-schema/` |
| **3** | **P6.4** the rules for the new fields and the device node · **P6.5** the prompt queue and interrupt policy | **No** | **P6.4 SECURITY-SENSITIVE (mandatory)** · P6.5 standard | the rules files and `internal/budget/rules/emulator_pairing_test.go` · `shared/prompts/` |
| **4** | **P6.6** `fugaro pair` · **P6.7** the consumer's enforcement · **P6.9** the text screen · **P6.10** the offline gate · **P6.11** checks 100–104 written | **No** | **P6.6, P6.7, P6.10 SECURITY-SENSITIVE** · P6.9, P6.11 standard | `internal/cli/pair.go`, one line of `internal/cli/root.go`, `internal/budget/token/device.go`, `internal/budget/sweep.go`, `internal/budget/sweep_device_test.go` · `internal/cli/mailbox.go` · `mobile/src/text/` · `mobile/src/mailbox/` · `docs/gcp-live-checklist.md` |
| **5** | **P6.8** pairing and the overview in the app · **P6.12** the confirmation and routing in the voice screen · **P6.13** the real sweeper's prompt rule | **No** | **P6.8, P6.12, P6.13 SECURITY-SENSITIVE** | `mobile/src/pairing/`, `mobile/src/overview/` · `mobile/src/voice/` · `internal/cli/mailbox.go` (the sweeper part) and its test |

**Phase 6 adds no level.** Its longest chain is **P0.1 → P0.2 → P6.3 → P6.4 → P6.6 → P6.8**, six deep — the same depth as the plan's critical path — of which P0.1 and P0.2 are already merged; the spoken-confirmation chain is **P0.1 → P6.2 → P6.12**, gated in practice by P3.5.

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
- **The app's feed reader (P3.2) depends on nothing this plan builds** — except the resolution of **tension (a)** (D112's direct reads against D124's feed), which is a decision, not a task. ~~It consumes a contract that is already on `main`.~~ *Amended 2026-10-17:* under D112's answer it reads the database directly, and the reader rules it applies are the contract's (§10.13); whether its documents are the database's own nodes or a mirror of the feed is tension (a).
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

**Performed 2026-10-17**, by the docs-only change D129 names (the owner: "Start the doc amendments PR"). D110 was answered "supersede", so no phase is deleted. Writing the answers back **approves no task**, this one included beyond its own write-back.

---

## Phase 1 — The pure semantics

*Five modules, each its own directory, each independently mergeable, each testable with no runtime and no network.*

### Task P1.1: The mailbox's state machine — message ids, at-least-once, dedupe, replay protection

**Files.** `shared/mailbox/`.
**Depends on.** P0.2.
**Verification.** Tests over the schema: a message carries an id; delivering the same id twice is **acted on once** and the second delivery is acknowledged as a duplicate, not re-acted; an id from a different sender is not deduped against; a message older than the window is refused as a replay *(D173, owner ruling 2026-10-17: the 6-hour window applies to commands — cancel, merge, launch, confirm; for prompts the acceptance window is the prompt's TTL, D118. P1.1 ships a **kind-parameterised window API** — the caller passes the kind or the TTL — over the kinds that exist; P6.3 adds the `kind` field; `MAX_WINDOW_MS` must be at least D118's TTL, whose value the owner set on 2026-10-17 at **72 hours** — inside the 7-day cap — so the consumer passes `promptTtlMs` = 259 200 000; [functional-spec-reconciliation.md](functional-spec-reconciliation.md) §3.6, O5)*; the state machine's transitions are total (every state × every event has a defined outcome, asserted by an exhaustive table test).
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
**Depends on.** D113 and D113b answered (2026-10-17), **and D112's answer — which adds read grants — with tensions (a) and (b) resolved (both resolved 2026-10-17)**, because they decide which families the user claim may read.
**Verification.** `go test ./internal/budget/rules/...` — `TestRulesGolden`, `TestGeneratedRulesParse` and `TestRulesParity` — and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` for the document (it is a `docs/design/*.md` file, so the docs tests see it). **No emulator case here**; that is P2.2.
**Reviewer tier.** **SECURITY-SENSITIVE — MANDATORY independent opus review.** This is the widest change in the plan: it is the only one that changes what the database permits.

**What it adds.** One subtree, for the mailbox and the heartbeat, and **four writes in it, one per writer and no more**: the phone's inbox message, the phone's ack on a message it has spoken (D113), the consumer's outbox message and the consumer's heartbeat (D113b). The phone's grants go through a **user claim** carried in the Firebase Auth token — a claim the rules *read*, not one they mint. Its shape follows the template's **subtree idiom**, which is the one `agents/$slug/$run` and `outcomes/$day/$slug/$run` already use (F21): **one `.write` on the message node**, whose predicate is the phone's own claim; `.validate`-only children, so the validators (`STR`, `AMOUNT`, `RBOK`) gate the values and a bound gates the body's length; and `"$other": { ".validate": false }` so an unknown field is refused rather than stored. **Not** the leaf idiom — the budget and lease families put a `.write` on each leaf, which is the wrong shape for a node written whole by one writer.

**What D112's answer adds (2026-10-17).** The phone reads the database directly, so the user claim also needs **`.read` grants**: on its own account's mailbox subtree (the outbox and the heartbeat) and on exactly the families tension (b) settles — D112's row named `agents/$slug/$run`, `config/caps/**`, `spend/$day/**`, `config/kill/**`, and a `.read` on `outcomes/$day/$slug/$run`, which has none today (F13). Each grant is scoped by the user claim to the user's own installation, and none is wider than the owner's ruling.

**Resolved 2026-10-17 (tension (b1)) — the read grants are exactly these.** `.read` for the user claim on **`config/caps/**`**, **`spend/$day/**`**, **`config/kill/**`** and **its own mailbox (the outbox and the heartbeat)** — and on **nothing else**. **No `.read` on `agents/$slug/$run`** (the list above names it only as D112's row did, and the owner struck it) and **no new `.read` on `outcomes/$day/$slug/$run`** (it keeps having none, F13). The job list therefore has **no run titles** until a separate owner decision. The golden (`rules.golden.json`) and `TestRulesParity` pin that set, and a grant on any other family fails them. The MAC key of tension (b2) adds nothing to the template: it reaches no database.

**Resolved 2026-10-17 (tension (b1)) — the read grants are exactly these.** `.read` for the user claim on **`config/caps/**`**, **`spend/$day/**`**, **`config/kill/**`** and **its own mailbox (the outbox and the heartbeat)** — and on **nothing else**. **No `.read` on `agents/$slug/$run`** (the list above names it only as D112's row did, and the owner struck it) and **no new `.read` on `outcomes/$day/$slug/$run`** (it keeps having none, F13). The job list therefore has **no run titles** until a separate owner decision. The golden (`rules.golden.json`) and `TestRulesParity` pin that set, and a grant on any other family fails them. The MAC key of tension (b2) adds nothing to the template: it reaches no database.

**What it must not do.** Loosen any existing `.write`, or any `.read` beyond the families the owner names under tension (b) (F11, F13). *(Amended 2026-10-17: the original sentence, "Loosen any existing `.read` or `.write`", held before D112 was answered direct reads.)* Grant a phone anything but the two writes D113 names — its own inbox messages and the ack on a message it has spoken (F21). Grant the **consumer** — D113b's writer, wherever D113c puts it — anything but the outbox and the heartbeat (D113b). Admit a signed-in user anywhere else (D14's argument, F8). Each of those is a review question and each has an emulator case in P2.2.

### Task P2.2: The emulator's denial cases — SECURITY-SENSITIVE

**Files.** `internal/budget/rules/emulator_mailbox_test.go`.
**Depends on.** P2.1.
**Verification.** `go test -tags firebase ./internal/budget/rules/...` against the pinned `firebase-tools`. The cases **assert denials**, and there are at least: a signed-in user writing outside its own inbox is refused; writing into another user's inbox is refused; ~~reading `/config/**` is refused; reading `/outcomes/**` is refused;~~ *(amended 2026-10-17: under D112's answer these become "reading any family the user-claim read grant does not name is refused", the list being fixed by tension (b));* reading `/fugaro/**` is refused; **(resolved 2026-10-17, tension (b1): a read of every family the grant does not name is refused by an explicit case each — `agents/$slug/$run`, `outcomes/$day/$slug/$run`, the rest of `config/**` beside `config/caps/**` and `config/kill/**`, and `/fugaro/**` — and the four named families are read-allowed only for the claim they are scoped to)**; **reading another account's mailbox subtree is refused**; **a read by a user claim of another installation is refused**; a message over the size bound is refused; a message missing the schema's required fields is refused; **a write to the outbox or the heartbeat by anything other than D113b's writer identity is refused**; **an ack on a message that is not one's own is refused**; and the positive controls — writing one's own well-formed inbox message, and acking one's own message, both succeed, and **each granted read succeeds for the claim it is scoped to and for no other**. **Read scoping per user claim is part of this task's denials** (D112's answer): a rules test that proves only the writes are fenced leaves the reads open.
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
**Depends on.** P3.1, and D112's answer (2026-10-17: **direct RTDB reads**) **with tension (a) resolved** — whether the reader's documents are the database's own nodes or a mirror of the documented feed.
**Verification.** Tests over recorded documents, with **no child process and no network**: a non-JSON line is skipped; a missing field becomes `unavailable` and never `0`; the newest document is not accumulated onto the previous one; a changed `day` is treated as a break; the degraded shape renders as degraded rather than as zero spend; and **a feed that stops arriving is not treated as a dead process** — silence is not death (§10.13).
**Reviewer tier.** standard.

**Every rule in this task is a rule the contract already states** (`console/docs/json-contract.md` §10.13, F35), and they are rules about **documents**, which is why they carry over to a phone. **The task builds no child-process supervisor**: §10.11's "does option A need a supervisor process? Yes" is about **the console** — a desktop sidecar beside a binary the operator has, `desktop-console.md` D13's binary path — and a phone has no such thing: there is no iOS or Android build of the Go CLI, and a phone could not execute one if there were. ~~**What is *not* designed is how the document reaches the phone**, and that is **D112's open decision**; this task does not start until it is answered.~~ **Amended 2026-10-17:** D112 is answered **direct RTDB reads**, so the document reaches the phone through a Firebase listener under the user-claim read grant (P2.1), per account, torn down on switch (P3.1). What was still open was **which nodes it reads** — tension (a) — and this task did not start until that was resolved. Its tests add one case either way: **a node the grant does not cover is never requested**.

**Resolved 2026-10-17 (tension (a), with (b1)).** The reader reads **database nodes directly**, as validated documents under §10.13's rules, from the four granted families only (`config/caps/**`, `spend/$day/**`, `config/kill/**`, the phone's own mailbox). D124 is kept as the reader rules and the document shapes, not the `watch --json` transport. **The queued and finished rows are in the runs bucket and the phone does not see them.** **The burn rate and the cap fractions are recomputed on the phone from `spend/$day/**` and `config/caps/**`, or not shown** (the choice is P3.2's, stated in its PR). Added test cases: **no request is ever made for `agents/**` or `outcomes/**`** (the grant denies them, and the reader does not try); a missing field is `unavailable`, never `0`, for every recomputed figure. **Depends on, additionally:** P2.1's grant set as resolved.

### Task P3.3: The job list

**Files.** `mobile/src/jobs/`.
**Depends on.** P3.2, P0.3.
**Resolved 2026-10-17 (tensions (a) and (b1)) — this task's scope changes; the verification paragraph below is kept as written and is superseded where it conflicts:** the phone reads only `config/caps/**`, `spend/$day/**`, `config/kill/**` and its own mailbox, so **the job list shows numbers and states only — spend, caps, the kill state and the heartbeat — with no run titles, no repositories, no queued rows, no finished rows and no PR link.** Its tests become: every figure is rendered from a granted family only; a missing field is `unavailable` and never `0`; a recomputed burn rate or cap fraction is either correct against a fixture or not shown; **no run title is on screen and no code path asks for one** (a grep-style test over `mobile/src/jobs/` for `agents` and `outcomes` paths); and a changed `day` is a break. The completed/failed/queued-row, keep-window and PR-link cases below do not apply until a separate decision gives the phone those rows; P0.3's validator is still built (it gates any link the app ever opens) and stays a dependency.
**Verification.** Tests: a completed job older than the keep window is hidden by default and shown when the toggle is on; a **failed** job is kept by the failed-keep rule; a queued job is shown; the per-job fields the spec names are rendered when present and marked `unavailable` when not; a PR link is opened **only** through P0.3's validator, and a link the validator refuses is rendered as text and not as a link.
**Reviewer tier.** standard.

**Two things this task must not do.** It must not claim the desktop's acknowledgement semantics — the ack file is local to the machine running the CLI and a phone cannot share it (F2). And it must not build a link at all until P0.3's validator exists with a non-empty host allow-list: the **finished** document carries `pr_url`/`pr_number` (`JSONFinished`), but **no shape carries a provider**, so the host is unknown and a `github.com` guess is wrong for a Bitbucket repository (F27, gaps 6 and 7). The **live** row has no PR field at all, so it has no link to build until gap 6 closes on the CLI side. *Note 2026-10-27:* the **live** row (`JSONRun`) now carries `pr_number` (console graph GV2.5): the draft PR's number, parsed from the run's PR URL with the watch package's anchored GitHub URL pattern, and absent for any URL that pattern does not read. It still carries no `pr_url` and no provider, so the live half still has no link to build.

### Task P3.4: The audio interfaces and their fakes

**Files.** `mobile/src/audio/`.
**Depends on.** P3.1.
**Verification.** The interfaces exist (AudioIn, STT, TTS) and their fakes are used by at least one test that exercises the voice pipeline's logic with **no device and no audio**: a fake transcript becomes a message; a fake reply becomes an utterance; an interrupted utterance is reported as interrupted.
**Reviewer tier.** standard.

**Fakes from task one** is Voice D52's rule, and it is what keeps the voice screen's logic in CI (Global Constraint 10).

### Task P3.6: The app's mailbox client — SECURITY-SENSITIVE

**Files.** `mobile/src/mailbox/`.
**Depends on.** P1.1, P1.3 (the redactor D118 requires before anything is written), P1.4, P3.1, **and P6.3** *(added 2026-10-17 by the functional-spec review: P6.3/P6.4 require `mac` on every inbox message, so P3.6 must sign — with P6.3's canonical serialisation and the key from secure storage — and could not start until P6.3, approved 2026-10-18, existed; P3.6 never writes an unsigned inbox message)*.
**Resolved 2026-10-17 (tension (b)):** the app's reads are exactly its own outbox and the heartbeat under the (b1) grant, and **the per-pairing MAC key is confirmed (b2)**, so the client **verifies the MAC over each outbox message's id, text and timestamp before it speaks a word or acks**, with the key read from platform secure storage and never logged; a message that fails verification is dropped and not acked. Added tests: a tampered text, id or timestamp is refused; a message with no MAC is refused; the key never appears in a log line or fixture.
**Verification.** Tests over the simulator and the fakes: the client writes **one** message per utterance, with a fresh id; it writes **no other path**; it reads only its own outbox; **every inbox message it writes carries a `mac` over P6.3's canonical bytes with the key from secure storage, and a test proves an unsigned write is never attempted**; the outbox and heartbeat MACs are verified over every field (§3.5 of the functional-spec reconciliation), and an unsigned heartbeat is treated as offline; **it acks a message only after it has spoken it** — and that ack is the plan's **second, explicitly-allowed phone write**, which the owner approves with D113 and P2.1's template grants on the message's own `acked` child and nowhere else; it re-delivers an unacked message after a reconnect and the duplicate is not acted on twice; and **it writes no heartbeat** — the heartbeat is the orchestrator's (D113b), and the app only **reads** it, through P1.4's rule. Under D112's answer the outbox and the heartbeat are **direct reads** under the user-claim read grant (P2.1), scoped to the phone's own account.
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

**Files.** **D113c answered (ii), 2026-10-17: a new `fugaro` command, not Voice D40's helper.** The consumer is Go code under `internal/cli` — a new command file (`internal/cli/mailbox.go`, the file of **`fugaro mailbox`** — **named by the owner on 2026-10-17, tension (c), no longer a placeholder**; the command does not exist yet), its tests, and **one line in `internal/cli/root.go`'s `root.AddCommand(...)`** registering it. By console **D19**'s rule this task is written and approved in **a CLI plan**, not here; this entry stays as the cross-reference. *(Option (i), the helper's consumer module with no Go file, is the one not chosen; the helper stays for voice.)*
**Depends on.** P1.1, P1.2, P1.3 (the redactor D118 requires before anything is written), P2.1, **D113c answered (2026-10-17)**, the command's **name** (resolved 2026-10-17: `fugaro mailbox`), and **the CLI plan** D19 requires.
**Verification.** Tests over the simulator: the consumer leases before it acts; a lease it does not hold means it does not act; it acks after it acts; **D113b's pairing-key question is answered (a) — confirmed 2026-10-17, tension (b2) — so every message it puts in the outbox carries the pairing MAC over its id, text and timestamp, with the key read from the OS keychain by the item name its settings hold and never logged**; a duplicate delivery is not acted on twice; a message it cannot parse is refused and **not** retried forever; and the consumer **never** runs a destructive command from a voice-originated message (D128's recommendation — the test asserts the refusal, and it is the test that makes D47's rule true on this channel).
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

**Files.** `mobile/eas.json` (the EAS build and submit profiles: `development`, `preview`, and `internal` — the store profile, `distribution: store`, an Android `app-bundle` and `submit.internal.android.track: internal`, which is C8's one value) and `mobile/app.config.js` (Expo's dynamic config, which resolves the two store identities — the iOS bundle identifier and the Android package name — from EAS environment variables at build time, so that neither is committed to `app.json`; `src/app-config.ids.test.js` and `src/eas-config.test.js` are their tests). **The EAS project itself, the signing keys, the store entries and the two identity values are the owner's**, and no identity value, no key and no key path is committed in either file — the identities are set with `eas env:set`, per environment, by the owner (`mobile/README.md`, "Building for internal testing"). **Owner-run.**
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

**Depends on.** The owner's **later, separate yes to push** — and, in the plan's own task terms, **P6.4** (the rules generation the `config/admin/push/$uid` sibling hangs off: nothing reads it before generation 3, which is the design's Q2, "push cannot ship before P6.4") and **P6.6** (the `fugaro pair` and `fugaro pair revoke` that write and delete the device node and delete the sibling), **both approved 2026-10-18** — P6.4 is **merged** (#601: rules generation 3, the device node and the pairing nodes are in the tree now), P6.6 is approved and not built; the Cross-task dependencies table below records both. **Plus two the design's §3.5 (b) names and this task does not own:** **P6.7** — the consumer (`internal/cli/mailbox.go`, P4.1's file; the command does not exist yet) is the **only** path that can carry a phone's registration token, because D165's pairing request node admits only the phone's public key and its timestamp (`rules.json.tmpl` generation 3, `internal/budget/rules/emulator_pairing_test.go` denies a request carrying any other field), so `fugaro pair` writes no push node at all — and **a mailbox-schema and rules change** for the message that carries the token: `shared/mailbox-schema/index.js`'s `FIELDS`/`KINDS` and `rules.json.tmpl`'s inbox field set close the message to any field they do not name (`"$other": { ".validate": false }`), so a bounded **registration kind with a required `token`** — the app's registration-and-preferences message, since §3.8's switches travel the same way — is a later rules generation and its own task (the design refuses the alternative, the token as a message's `text`). **D121 was answered "no push in v1" on 2026-10-17** (reconciliation.md), and the answer defers push to "a separate decision with its own service, its own device-token store and its own retention question" — which is this task. Its **design is written**: [design/push-notifications.md](design/push-notifications.md), **design only; nothing is implemented**, whose **Q1–Q10** are open for the owner and which allocates no decision number.
**Files.** The design and its documentation test `internal/cli/docs_mobile_push_test.go` — **docs only: no service, no token store, no rules change, no app code**. A later, scheduled implementation would touch a sender in `internal/cli` — the design's recommendation is (B), **an extension of the history sweep job that derives its own events, with no producer write and no pending-push node of its own** (its retry state is a row in the push node's own ledger, the design's §3.6/§6) (`internal/cli/history.go`, `deploy/terraform/gcp/modules/installation/history.tf`; option (D) is #632's orchestrator, if P5.1 lands), the **push node `config/admin/push/$uid`** beside P6.4's device node `config/admin/devices/$uid` (`internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`, `internal/projectpolicy/version.go`) — the sibling shape, so only `fugaro pair` and `fugaro pair revoke` ever write `devices/**`, the app's registration path under `mobile/src/`, and a disclosure in `SECURITY.md` (which today says nothing about the phone).
**Verification.** **Check 88** — the number this plan allocated to this work (80–89, taken as allocated and never renumbered) and assigned to this task's behaviour in the check table; the design names it and does not write it. **The register text is written by Task P4.2, which is scheduled while this task is not** — the plan's own allocation above is "checks 80 and 83–88 by P4.2" — and the check is run by the owner only if the owner builds push. The register (`docs/gcp-live-checklist.md`) carries no `## Check 88` section and no `| 88 |` row today. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled.

### Task P5.3: The local/remote voice evaluation switch — UNSCHEDULED

**Depends on.** D123 answered, the owner's ruling of **2026-10-19**, which approves an in-app switch between local and remote STT/TTS **for evaluation only** and **relaxes Voice D44 for testing**, while leaving D44's **second explicit decision** (adopting remote voice) owed and untaken — and **Q0**, the owner's acknowledgement that **Global Constraint 12 is amended for the evaluation switch only**, which this plan's own constraint 12 needs before any of the remote half is built. **Q0 was answered by the owner on 2026-10-19 — option (a), yes as described** (with **Q1–Q4**, each taking its recommended option), so **that gate is passed**, constraint 12 above carries its dated note, and each document the design's §1a lists carries one too. **The answers are acknowledgements and not approvals**: **this task stays UNSCHEDULED, nothing starts until the plan's own approval gate passes, and Check 85's measurement is still outstanding** (the design's §1a states the amendment and lists every document that states **or relies on** the old rule). **The design is written** — [design/voice-evaluation-switch.md](design/voice-evaluation-switch.md) (renamed from `design/cloud-stt-tts.md`, which `§11` records), which is a design and not a task: it takes no decision, allocates no decision number, writes no check, and **amends exactly one ruling, stated in full in its §1a** — this plan's **Global Constraint 12, for the evaluation switch only** — and it adds nothing to any approved phase. **Files.** [design/voice-evaluation-switch.md](design/voice-evaluation-switch.md) is the design (this change); no code, no rules change, no Terraform and no workflow. If it is ever scheduled, its files are the rules template's `config/policy/voice/**` subtree (`providers`, `direction`, `force_local`) with its golden, parity test and `RulesVersion` bump (`internal/budget/rules/`, P2.1's shape), the pure switch-and-indicator function in `shared/` with vectors and a mutation proof (the design's only new pure logic, shaped like `shared/command` and `shared/confirm`), the two device-local settings and the consent sheet in the app, **the server-side hop** (a new service holding the provider key in the installation's Secret Manager, which also answers the app's project-status query — the app's only source for "blocked by project policy", since it cannot read `config/policy/**`; **Q2 was answered 2026-10-19: a new minimal service, not the credentials service**, and **Q3's `force_local` is enforced there**), and the provider clients V7 already names for the console, which the design recommends be reused from `shared/` rather than written twice. **Verification.** **Check 85 — the bake-off — has not been run**, and its text is not in the register; under this ruling its scope is **the same fixed prompts through the local engine and through each admitted remote provider** (**Q1 answered 2026-10-19: the three providers in the design's §7**), with first partial, end-of-speech-to-reply, name error rate and cost per hour recorded per engine. No latency or cost number exists to write a task against. The remote half would need **Check 110** — the next free number after reading the register (the design's §13 works the reading out name by name: 1–18 summary rows, 19–47 written, 48–58 the project-centric, artifact-runs and compute-backend rows, 59 the console, **60–61 owner-allocated (the budget-token channel and the monorepo folder export)**, 62–63 the console v2 pair, **64–66 this voice work with 65 and 66 held**, **67–68 managed projects**, 69a–69c and 70a–70c the website, **71–79 unclaimed**, 80–89 this work in full, 90–99 the credentials service's, 100–109 the functional spec's, and nothing above 109 claimed) — and **this plan writes neither check**: the phase that ships a check writes it, and a check is never run before its text is in the register. The voice work already holds **check 66** for the console's own cloud-provider check; **Q4 was answered 2026-10-19: the app's surface takes 110**, and 66 stays the console's — **110 is named and not written**. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled — **mandatory independent review**, for the policy node (a rules change), the hop (a new internet-facing secret holder, the only place the project policy is enforced, and the check on the device node that is the revocation bound), the credential question (the key stays server-side so that the app holds no third item), and because the failure mode of a mistake here is speech leaving a device the owner's ruling said it would not.

### Task P5.4: The conversational front agent — UNSCHEDULED

**Depends on.** D172 answered (recommended: decided with D115, after phase 1 ships). **Files.** None yet. **Verification.** None until scheduled. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled. *(Added by the functional-spec v0.1 reconciliation; not approved.)*

---

## Phase 6 — APPROVED by the owner on 2026-10-18 (proposed by the functional spec v0.1)

*Every task here came from [functional-spec-reconciliation.md](functional-spec-reconciliation.md), and **the owner approved all of Phase 6 on 2026-10-18** — the status line this section carried until then said the opposite, and it is corrected here rather than left to be read as a live bar. Each task still waits on the decision rows its "Depends on" names, and the SECURITY-SENSITIVE ones still need their independent review before they merge. The conventions of this plan apply unchanged (Global Constraints 1–14): no live cloud in a test, no secret-shaped literal, `belong` never touched, no agent edits a workflow, owner-run checks only.*

### Task P6.1: Wake-word routing and the app-command grammar — SECURITY-SENSITIVE

**Files.** `shared/command/` (new). **Depends on.** P0.1; **D164**, D161, D163.
**Verification.** `npm test` in `shared/` (the glob picks the module up). Tests: an utterance whose first token is in the closed alias set is an app command, any other utterance is agent text; each of the five §3.4 commands parses with a spoken project or agent name matched only against the configured lists; an unmatched "Fugaro …" yields "not understood" and **is never forwarded**; "confirm" and "cancel that" are recognised only as app commands; the alias set is data, and a test pins that "yes" is not an alias of "confirm".
**Red step.** The "unmatched Fugaro utterance is not forwarded" test, failing against a stub router that forwards. **Reviewer tier.** SECURITY-SENSITIVE — independent opus review (it decides what counts as "confirm").

### Task P6.2: The spoken-confirmation state machine — SECURITY-SENSITIVE

**Files.** `shared/confirm/` (new), including `shared/confirm/vectors.json` — the accept/refuse cases as data, for P6.7's Go parity test. **Depends on.** P0.1; **D169**, D162, and the owner's D128 amendment (2026-10-17).
**What it is.** Pure logic with an **injected clock**. A destructive command (cancel; merge/approve; confirm-an-action — **not launch**: D162 answered 2026-10-17 that "kick off …" goes through without confirmation, the caps applying) enters `pending` only with a **resolved target** (a run id, or repository + PR number); an ambiguous or unresolved target is refused and **cannot be read back, so cannot be confirmed**. The read-back text is built **from the resolved target**, never from the transcript. A confirmation must be a **separate utterance** recognised as the fixed phrase; it has a **timeout**; **one confirmation authorises exactly one action** (it carries the action id); **the utterance that issued a command can never confirm it**. Every other destructive verb (the kill switch, budget writes, deletes) is refused outright.
**Verification.** `npm test` in `shared/`. Tests, at least: a **misrecognised "confirm"** ("conform", "confirmed it", "yes") does not confirm; a **replayed confirm** (the same confirmation id or utterance id twice) authorises nothing the second time; a **confirm for a different target** (the pending action changed, or a second pending action) is refused; a confirm after the timeout is refused; a command and "confirm" in one utterance is refused; the read-back for "cancel the docs refactor" names the resolved run id; the state machine's transitions are total (an exhaustive table test).
**Red step.** The replayed-confirm test against a machine without an action-id check. **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.3: The schema extension — SECURITY-SENSITIVE

**Files.** `shared/mailbox-schema/`. **Depends on.** P0.2; **D161, D167, D168, D169, D171**.
**What it adds.** Optional fields: `agent` (D161); `kind` (`message` · `prompt` · `answer` · `action`); `options`, `reply_to` (no expiry field: under D168 as amended, prompts expire at D118's TTL, computed by the consumer from `at`); an **action record** (verb from a closed set, resolved target, action id, confirmation id and time, and for a merge the PR's **head SHA** read back at confirmation) (D169); `canonicalBytes(message)`, the one pure serialisation of every field except `mac` (and `acked`) that the MAC covers, with `vectors.json`; `mac`, **required on every outbox message and on every inbox message of every kind**, with the covered fields and canonical serialisation of [functional-spec-reconciliation.md](functional-spec-reconciliation.md) §3.5, fixed by a vectors file both ends test against (tension (b2), D169, D171; O3); and on the **heartbeat**, an `agents` list (id and spoken name, bounded count and length) and a `mac` (O7) so that "list agents" and D163's "first advertised agent" have a source (D161). Any inbox message without `mac`, an `action` without a complete confirmation record, a merge action without a head SHA, an outbox message without `mac`, and an over-long `agents` list are refused; **the vectors include a valid MAC with `text`, `options`, `reply_to` or the answer swapped, each refused**, and a heartbeat with its `agents` changed under a valid MAC, refused.
**Verification.** `npm test` in `shared/`; the existing `mutation-proof.sh` extended to the new refusals. **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.4: The rules for the new fields, and the device node — SECURITY-SENSITIVE

**Files.** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`, `internal/budget/rules/testdata/rules.golden.json`, `internal/projectpolicy/version.go`, `docs/design/m9-budget-and-dashboard.md`, `internal/budget/rules/emulator_pairing_test.go` (new). **Depends on.** P2.2 (P2.1 merged first), P6.3; **D161, D166, D169, D171**. *(If the owner folds these changes into draft #532 — open item O2 — this task shrinks to its emulator file.)*
**What it adds.** The P6.3 fields as `.validate`-only children (the subtree idiom, F21), including **`mac`, required on the outbox and on every inbox message** (O3, D169; the rules check presence, the consumer verifies), the merge record's head SHA, and the heartbeat's bounded **`agents`** child and its `mac` (D161, O7; draft #532's heartbeat admits only `at` and `boot`, so this lands after #532, O2); in `USER`, the conjunct `root.child('config/admin/devices/' + auth.uid).exists()` (D166); and the **pairing nodes** of D165 — a one-shot request node the `p~` pairing uid may write once (its public key) and a delivery node it may read (the encrypted payload), both denied to every other identity. No existing grant is loosened.
**Verification.** `go test ./internal/budget/rules/...` and `go test -tags firebase ./internal/budget/rules/...`: a revoked device (node deleted) is denied every read and write it had; a session whose device node was never written is denied everything; a `p~` pairing uid can write its request node once and read only its own delivery node; an `action` message without a confirmation record is refused by `.validate`; an outbox message without `mac` and an inbox message of any kind without `mac` are refused; a heartbeat `agents` list over its bound is refused; positive controls for each new field. **Reviewer tier.** SECURITY-SENSITIVE — **mandatory** independent opus review. **The deploy is P2.3's shape: owner-run.**

### Task P6.5: The prompt queue and the interrupt policy

**Files.** `shared/prompts/` (new). **Depends on.** P1.3, P1.4, P6.3; **D160, D168 (amended: prompts expire at D118's TTL), D173**, D127.
**Verification.** `npm test` in `shared/`: prompts queue by default; at a natural pause the announcement counts pending prompts across every account ("Two questions waiting"); "what's pending" lists them per installation; **an unanswered prompt expires at D118's mailbox TTL** (D168 as amended 2026-10-17), decided as a pure function of the prompt's `at`, the TTL and the consumer's clock, and the expiry yields the prompt's non-destructive answer, or a stop if it has none (the consumer, P6.7, delivers it once); a prompt answer is **accepted within its TTL, not P1.1's 6-hour command replay window**, except that an answer to a **destructive** prompt is a confirmation and takes the command window (D173; §3.6, O6 confirmed 2026-10-17; TTL 72 hours, O5); P1.1's kind-parameterised window API implements the windows and this module passes the kind; **reminder escalation**, before expiry — a waiting prompt is re-announced at the next natural pause after 1 hour, then once a day, and is listed first in "what's pending" (the bounded alternative of [functional-spec-reconciliation.md](functional-spec-reconciliation.md) §3.4, adopted with Phase 6); a **hard cap on pending prompts per consumer**: at the maximum count a new prompt is refused with a defined answer to the orchestrator ("queue full"), tested at the boundary; reminder escalation bounds attention, the cap bounds storage;  only D160's set interrupts, and the local switch narrows or widens it; a pure function of (queue, digest, presence, switch, now).
**Reviewer tier.** standard.

### Task P6.6: `fugaro pair` — SECURITY-SENSITIVE

**Files.** `internal/cli/pair.go` (new) and tests, `internal/budget/token/device.go` (new) and test, `internal/budget/sweep_device_test.go` (new), `internal/budget/sweep.go` (the `m~` rule), **one line** in `internal/cli/root.go`. Written in **a CLI plan** (console D19), as P4.1 is. **Depends on.** P6.4, P4.1 (the shared `root.go` line); **D165, D166, D171**.
**What it does.** Mints a **pairing** token (one-shot `p~<random>` uid, `exp` ≤ 5 minutes) and shows the QR (Firebase config from what `init` recorded, and that token — **no key**); waits for the phone's public key in its pairing-request node and for the operator to type the code (the key's fingerprint) the phone shows; only then mints the **device** token (`m~<random>`, `mu = uid`), generates the per-pairing MAC key into the OS keychain (the settings hold the item's name only, Voice D42(2)), writes `config/admin/devices/$uid`, and delivers token and key encrypted to the phone's key (D165, D171). `ls` and `revoke` per D166; extends the history sweep to remove `m~` users whose device node is gone. **Needs two separate role sets, and states both:** the `token_minter` custom role on the signer (launchers and operators, `iam.tf`'s `minter`) to mint either token, **and** `roles/firebasedatabase.admin` (admins and budget admins) to write or delete the device node; it refuses clearly, naming the missing one, without either.
**Carried over from P6.4.** The deployed rules put **no time limit on a pairing session**: the `p~` uid may write its `pairing/<uid>/request` once and read `pairing/<uid>/delivery` for as long as its Auth user exists, and the node outlives the five-minute token. So P6.6 **deletes the `p~` Auth user and the `pairing/<uid>` node once the delivery has been read** — the session is one-shot by construction (one request, one delivery) but not by time, and nothing else removes either. A pairing session that is never completed leaves both behind, and that is the sweep's job, not the rules'.
**Verification.** `go test ./internal/budget/token/... ./internal/budget/... ./internal/cli -run 'Pair|Sweep'` with the fakes (`internal/gcpfake/identitytoolkit.go`): the minted uids never start with `r~`; the sweeper never deletes an `m~` user whose device node exists and does delete one whose node is gone; a wrong typed code writes nothing; the delivered payload is unreadable without the phone's private key (a fake key pair); `exp` is at most five minutes; no device node is written before the typed code matches; the key never appears in output, a log line or a fixture; revoke deletes the node before the Auth user; **a caller without the minter role is refused with a message naming it, and a caller without database admin is refused with a message naming it**, each before any side effect (two tests, fakes). **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.7: The consumer's enforcement of the D128 amendment — SECURITY-SENSITIVE

**Files.** `internal/cli/mailbox.go` and its tests (extending P4.1's consumer; CLI plan per D19). **Depends on.** P4.1, P6.2, P6.3, P6.4; **D169**, D162, D168.
**What it does.** Re-validates every action record with a **Go implementation** of `shared/confirm`'s rules (the consumer is Go; the JS module cannot be imported), held to them by a parity test over `shared/confirm/vectors.json`: (verb in the closed set, target resolved, confirmation present, inside the 6-hour command window from the confirmation time — **the confirmation timeout itself is checked on the phone (P6.2)**, because the pending time is phone-local state — not already used; `at` and confirmation time at most 5 minutes ahead of the consumer's clock), and **first verifies the MAC of every inbox message, of every kind,** with the per-pairing key read from the OS keychain by the item name (D169, D171; §3.5's covered fields and serialisation) — an unsigned or wrongly signed message is refused before anything else; accepts an answer to a **destructive** prompt only as a MAC-verified action record with a confirmation (never a plain `answer` or text); keeps a **ledger of used message, action and confirmation ids, and of prompts expired or answered, that survives a restart** for at least the 72-hour TTL plus one sweep interval (or until the stored message is gone, whichever is later); **persists its pending prompts locally (id, `at`, safe answer)** so a sweep of the stored prompt does not lose the safe answer; after a `fugaro pair revoke`, drops that pairing's pending prompts (safe answer once) and refuses messages signed with the revoked key; signs every outbox message and the heartbeat; for a merge, re-reads the PR's head and **refuses if it differs from the head SHA in the record**; **refuses a destructive command that arrives without a valid confirmation record**; executes cancel through the CLI's own cancel path and merge as D169 says; refuses destructive free text it recognises; produces prompts that **expire at D118's TTL** (D168 as amended 2026-10-17) — **the consumer expires them on its own clock**, so expiry does not depend on when the owner-run sweeper runs, and at expiry it hands the orchestrator the prompt's non-destructive answer, or tells it to stop if there is none, **exactly once** (recorded in the same restart-surviving ledger) — up to P6.5's pending-prompt cap (beyond it, it answers "queue full"); applies the 6-hour replay window to commands and the TTL to prompts (D173); advertises its agents in the heartbeat's `agents` list (D161); a launch ("kick off …") is passed through without a confirmation record (D162), and the caps apply.
**Verification.** `go test ./internal/cli -run 'Mailbox'` over the simulator, including `TestConfirmParityWithShared` over every vector in `shared/confirm/vectors.json`: an action with no confirmation record is refused and nothing runs; **a forged record — unsigned, or signed with the wrong key — is refused and nothing runs**, including one whose confirmation id and time are well-formed (the launcher-as-`m~` case of D169); **an unsigned `message`, an unsigned `answer`, and a plain-text answer to a destructive prompt are each refused and nothing runs**; a validly signed record with a **tampered target or head SHA** is refused; a signed record **re-sent under a new message id** is refused (action-id dedupe), also after a consumer restart; **a restart after a prompt expired does not deliver its safe answer a second time**; **a stored prompt deleted by the sweeper is still expired from the local copy with its safe answer**; an `at` more than 5 minutes ahead is refused; a message signed with a revoked pairing's key is refused; a merge whose PR head moved since the read-back is refused; the pending-prompt cap refuses the next prompt at the boundary; a **replayed** confirmation id is refused; a confirmation for **a different target** is refused; an expired confirmation is refused; a well-formed confirmed cancel calls the cancel path once (a fake); a launch with no confirmation record is **not** refused for lacking one (D162); an expired prompt gives the orchestrator its non-destructive answer, never a destructive one; a prompt answer older than 6 hours but within the TTL is accepted while a command older than 6 hours is refused (D173); the kill switch and budget writes are refused even when confirmed. **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.8: Pairing and the overview in the app — SECURITY-SENSITIVE

**Files.** `mobile/src/pairing/`, `mobile/src/overview/` (new). **Depends on.** P3.1, P6.1, P6.6; **D165, D170, D171**, D114.
**Verification.** The app's runner: a scanned payload is validated before anything is stored; the key pair is generated on the phone and the code shown is its fingerprint; the delivered device token and MAC key are decrypted and go only to secure storage (P3.1's rule); the code shown is derived per pairing; the overview renders one tile per installation with its freshness and no summed total (D170); switching by voice (P6.1's commands) tears down the previous account first (D114). **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.9: The text screen

**Files.** `mobile/src/text/` (new). **Depends on.** P3.6, P6.5; **D167**.
**Verification.** Tests: pending prompts and recent summaries render per installation; typed input goes through the same mailbox client; a typed destructive command goes through the same confirmation machine (typed "confirm"). **Reviewer tier.** standard.

### Task P6.10: The offline gate — SECURITY-SENSITIVE

**Files.** `mobile/src/mailbox/` (extending P3.6). **Depends on.** P3.6, P6.3; **D167**.
**Verification.** Tests over a fake connection state: with no connection, or a stale/offline consumer, nothing is written and the user is told; an action record is never queued; the client never relies on the SDK's offline queue (a test that pending writes are cancelled or never issued). **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.11: Checks 100–104, written into the register

**Files.** `docs/gcp-live-checklist.md`. **Depends on.** P6.4; D126. **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`. **Reviewer tier.** standard. **Running them is the owner's**, check 103 on the sandbox repository only.

### Task P6.12: The spoken confirmation and routing in the voice screen — SECURITY-SENSITIVE

**Files.** `mobile/src/voice/` (extending P3.5). **Depends on.** P3.5, P6.1, P6.2, P6.5; D162, D169.
**Verification.** Tests over the audio fakes: a destructive command produces the read-back from the resolved target and waits; a confirm in the same utterance is refused; a misheard confirm is refused; a timeout says so and drops the action; a read-back for a merge states the PR's **current** head at confirmation time (D168: a prompt can be answered days later); a queued prompt is not spoken over the user. **Reviewer tier.** SECURITY-SENSITIVE — independent opus review.

### Task P6.13: The real sweeper's rule for waiting prompts

**Files.** `internal/cli/mailbox.go` (the D118 mailbox sweeper P4.1 builds in `internal/budget/sweep.go`'s shape) and its test. **Depends on.** P6.7; D118, D168 (as amended 2026-10-17: prompts expire at the TTL).
**What it does.** Makes the real, owner-run sweeper **delete** stored prompts past D118's TTL — and nothing more: **the consumer, not the sweeper, expires a prompt and delivers its safe answer** (P6.7), because a sweep can run while the consumer is down. The sweeper never answers the conversation.
**Verification.** `go test ./internal/cli -run 'MailboxSweep'` with a fake clock: a stored prompt older than the TTL is deleted by the sweep; a prompt younger than the TTL survives; **a sweep run while the consumer is down deletes the stored prompt, and the consumer on restart still delivers the non-destructive answer (or a stop) exactly once** from its own clock and ledger; the sweeper itself never writes an answer; the pending-prompt cap (P6.5, P6.7) is the storage bound and a test pins that a full queue plus a sweep never exceeds it. **Red step.** The consumer-down test, against a consumer that only answers when it sees the sweeper delete. **Reviewer tier.** **SECURITY-SENSITIVE — opus + independent review** (it decides the default answer the orchestrator receives).

## Phase 7 — APPROVED by the owner on 2026-10-22 (telemetry, on merge of its plan)

**The owner approved this phase on 2026-10-22, on one condition: that its plan is merged
first.** It was proposed by the owner's own ruling of **2026-10-19** — *the app carries Firebase
Crashlytics and Firebase Analytics, reporting to a Firebase project that Fugaro (the project)
operates, and not the user's own installation* — which is recorded as **D253** and is the
premise of every task below. Its plan is
**[../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md)**, and
**that document is the authority for every task here**: the files, the flags, the allow-list,
the labels, the checks and the UNVERIFIED list all live there, and this section is its
registration. **Approval is permission to start and not a claim that anything exists**: at the approval
date of 2026-10-22 not one task of P7.1–P7.5 was built, and no `@react-native-firebase`
dependency, native config file or telemetry module was in the tree. **Amended 2026-10-30
(Task P7.1a):** the first half of P7.1 is built — the three `@react-native-firebase`
packages with `expo-build-properties` are in `mobile/package.json`, `mobile/app.config.js`
registers their config plugins, `mobile/firebase.json` exists, and `mobile/.gitignore`
ignores both native config files, so **no native config file is in the tree still**.
**Superseded 2026-10-30 by Task P7.1b:** the build-time guard (**D260**) and the
ad-ID walk landed, so **P7.1 is built**; unbuilt remain **P7.2–P7.5** — the
always-on statement, the event allow-list and its scan,
`mobile/src/telemetry/`, and the store labels and the privacy paragraph.
**Amended 2026-10-31:** `shared/telemetry-consent/` was listed here with the rest
and is dropped — §6 removes it with the consent gate D255's answer dropped, so
it is nothing a task builds.

**The numbers it takes.** **D253–D260** — the next free decision block, read from the tree
rather than remembered: **D252** is the highest number written anywhere in `docs/` (the website
zoom-diagrams work's), D193–D197 belong to the coordinator, D198–D200 are unwritten but sit
directly below the voice-session work's **D201–D220**, and **D221–D236** are the mobile-auth
work's. **The owner answered all eight on 2026-10-19**, D259 last: **D253** is the ruling the
phase is built on, reporting to **`fugaro-prod`**; **D254**, **D256**, **D257**, **D258** and
**D260** are answered as recommended; **D255 is answered "always on"** — no switch and no
first-launch sheet, **against** the plan's opt-in recommendation, which stays visible in the
row; and **D259 is accepted** — the telemetry project's native config is **built into the app**,
while each installation's own Firebase connection arrives in the **encrypted pairing payload**
and lives in **secure storage**, which, after the owner's ruling of 2026-10-29 that D232 stands, leaves **D111** at two items (idToken, macKey) plus the pairing-delivered configuration (the installation's database URL and web API key, ordinary configuration, not secret) (Global
Constraint 4). **Checks 120, 121 and 122** — the first free numbers after every claim, since
105–109 are reserved, 110 is claimed, **111–113 are written into the register**, 114–118 are the
voice-session work's and **119 is taken by the rigor-modes plan** ([../rigor-modes/implementation-plan.md](../rigor-modes/implementation-plan.md), its T8.1) **and proposed by the console-on-Windows work** — leaving **123 onward free**. **A reserved number is never
reused and never renumbered**, and this phase renumbers nothing. **The draft on PR #693 asked
for D193–D200 and checks 111–113**, which other works had taken before it merged; this
registration takes the numbers that were free when the plan was rebuilt on `main`.

**Three things this phase is not.** It is **not an approval of anything unbuilt** — the gate
below is passed for it as **step 7b**, dated 2026-10-22 and conditional on the plan's merge, and
**an answered row is not an approval**. It **changes no rules file, no Terraform, no `fugaro`
command and no workflow** — the only live actions in the whole phase are the owner
**registering the two Firebase apps in the project that exists** (`fugaro-prod`, D254's
answer — the project the website is already deployed into, so nothing is created for
telemetry) and uploading two EAS file secrets. And it **moves no existing task**: Phase
3, Phase 6 and Phase 7 are independent, and the voice-less MVP is unaffected (D258).

**Two findings the phase is built on**, both read from the tree and both stated in the plan's
§1: the app has **no Firebase SDK wired at all** today (`mobile/index.js` wires
`expo-secure-store`, `expo-async-storage`, `react` and `react-native` and nothing else), so
whichever project the native config files name becomes the app's **default** Firebase app —
which is why **D259** exists — the owner asked whether the Firebase config should not simply
be built into the app, **and accepted the answer on 2026-10-19**: **the telemetry project's
native config files are** (as EAS file secrets, D260) **but each installation's own Firebase
connection is not**, because it differs per installation and is unknown at build time, so it
arrives in the **pairing payload** and lives in **secure storage**; and the settings
store **refuses an `AIza…` value outright** (`mobile/src/accounts/settings.js`), so an
installation's web API key cannot live in the account tuple, which **narrows** the store's
refusal to API-key-shaped values generally while the plan's stronger sentence — that no such
value is stored anywhere — no longer holds. **D259's acceptance widens D111** from two
items to four, and the owner accepted that consequence with the answer.

### Task P7.1: The config plugin, the native files, and the development build — SECURITY-SENSITIVE

**Files.** `mobile/app.config.js` (**modified** — the plugin entry, the two native config paths
resolved from the environment, and the build-time guard), `mobile/native-config-guard.js` and
`mobile/src/native-config-guard.test.js` (**new** — the guard of **D260** and its test),
`mobile/eas.json` (**not modified — amended 2026-10-30, P7.1b**: the two `env` entries in each
of its three build profiles were the planned mechanism and are not the one used; an `env` block
there would **override** the EAS environment rather than deliver it, so the files arrive as EAS
environment variables with file visibility, and `mobile/src/eas-config.test.js` refuses an `env`
per profile), `mobile/firebase.json`, `mobile/.gitignore`, `mobile/package.json`,
`mobile/README.md`, `mobile/src/app-config.test.js` (the tracked-file and ignore rules and the
extension of the existing credential walk to `app.config.js`) and **`mobile/src/ad-id.test.js`**
(**new — the ad-ID test**: the two Android ad switches `false`, no `AD_ID` permission, the
Analytics plugin entry carrying `ios.withoutAdIdSupport`, with a planted control each — the
plan's §4.2 and §7.4, and the website's *the app carries no advertising identifier* sentence is
what it holds).
**Depends on.** **D260** (answered: the config is built into the app through EAS file secrets, the development build, Expo Go unsupported, and a build that cannot see them fails loudly), **D254** (answered: **`fugaro-prod`**, the project the owner creates the two apps in by hand) and **D259** (**accepted 2026-10-19**, and the last row this task waited on): the native config files are the telemetry project's, and what may not be built in is each **installation's** own Firebase connection (its Realtime Database URL and web API key), which arrives in the pairing payload and lives in secure storage. Both
store-path files are on `main` — **PR #675** merged them — so P7.1 modifies `app.config.js` in
place and waits on no pull request; `mobile/eas.json` is left as PR #675 wrote it, per the Files
line above.
**Verification.** `cd mobile && npm test` — both native config paths are untracked and
ignored; `app.config.js` carries no `AIza…`-shaped value (against the planted control the file
already has); the existing "no credential, no installation" walk still passes; and the ad-ID
walk — `mobile/src/ad-id.test.js` — passes. **Amended 2026-10-30 (P7.1b): the planned "a build
profile with no `GOOGLE_SERVICES_JSON` in its `env` is refused" is retired** with the `eas.json`
wiring above — the guard's subject is the **resolved file** the build's config carries, not a
profile's `env` key, so what is refused is a build whose platform resolves to no readable file.
**The plan's §4.2 marked two names VERIFY** — the iOS
pod/plugin option that links the ad-ID-free Analytics variant, and whether the Android
permission removal is the plugin's own option — **both resolved 2026-10-30** by reading the
pinned `@react-native-firebase` source: `ios.withoutAdIdSupport`, and Expo's own
`android.blockedPermissions`, the plugins offering no Android option.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It decides which project
the app's **default** Firebase app is, and it is the change that would put a native config
file in the tree if it were wrong.

### Task P7.2: The always-on statement, and the link — SECURITY-SENSITIVE

**Renamed 2026-10-19 by the owner's answer to D255.** It was *"The consent gate, the switch
and the link"*; the answer is **always on** — there is no switch, no first-launch sheet and no
consent state — so the gate this task was written around does not exist and is not built. The
task keeps its number, its place and its tier, and does the two things that remain: the app
**says** what it does, and the link to `/privacy/` is built the one safe way.
**Files.** `mobile/src/telemetry/notice.js` (new: the statement the app shows, and the
`/privacy/` URL), `mobile/src/app/root.js` (the shell renders the statement as **a third child**
— `SHELL.NOTICE` — because **the app has no settings screen** and the shell is the only surface
it has; **no sheet, no prompt and no control**), **`mobile/src/app/root.test.js`** (it pins the
shell's children to exactly `[SHELL.TITLE, SHELL.BODY]` and the rendered text of both states,
so adding the statement breaks it and it moves in the same commit — the task's own
`cd mobile && npm test` cannot pass without it), and `mobile/src/telemetry/notice.test.js`. **Nothing is created in `shared/`** — the consent
machine, the platform flag table and the settings key `telemetry.consent` the earlier version
of this task named are all deleted from the phase (§6 of the plan lists what the answer
removes from the tree).
**Depends on.** P7.1; **D255** (answered: always on); and **D124's host list, which the owner
still owes an answer to** — `shared/url`'s `DEFAULT_HOST_ALLOW_LIST` ships **empty** and D124's
answer names no hosts, so the `/privacy/` URL is refused with `allow-list-empty` and renders as
**text** until the owner names `fugaro.dev` in it. **Until that step, this task ships the
statement without a link**, and the plan's §6 says so rather than leaving it to be found on a
device.
**Verification.** `cd mobile && npm test`: the statement is rendered from a single source and
its text is the one §6 of the plan states; the `/privacy/` link is built through
`shared/url`'s validator, and a host the allow-list does not name renders as text, not as a
link — **and until the owner's host list names `fugaro.dev`, the text rendering is the
assertion that passes**; the shell carries **no switch** that turns collection off, and the
module makes **no `setAnalyticsCollectionEnabled(`/`setCrashlyticsCollectionEnabled(` call** — the
plan's §7.4 scan refuses one anywhere in the app, which is what makes "there is no switch" a
test rather than a sentence.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the only place a
link leaves the app, and it is where the app's own statement about the reports is worded.

### Task P7.3: The allow-list, the redaction rule, and the scan — SECURITY-SENSITIVE

**Files.** `mobile/src/telemetry/` (new: the event module, the closed enum sets, the constant
breadcrumbs), `mobile/src/telemetry/analytics-calls.test.js`, `mobile/src/telemetry/events.test.js`,
and **one deliberate entry** in `mobile/src/mailbox/write-path.test.js`'s `RULES` closing the
`@react-native-firebase/…` import gap that scan has today (`/['"]@?firebase\/[a-z-]+['"]/`
does **not** match it).
**Depends on.** P7.1; **D256**.
**Verification.** `cd mobile && npm test` — every rule of the plan's §7.4 with its planted
positive control, plus `write-path.test.js` and `boundary.test.js` still green, which is the
proof that the one new allow-list entry widened nothing else.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the fence that makes
"nothing that names a message leaves the phone" a test rather than a promise, and it widens an
existing scan's allow-list.

### Task P7.4: The labels and the website's paragraph

**Files.** `website/src/pages/privacy.astro`, and the `website/` gates the paragraph's word
count moves (`docs/website/`'s ceilings are the authority, D188).
**Depends on.** P7.3 (the list the labels are answered from); **D257** (answered as
recommended: the labels come from what the SDKs collect, and the website's words move with the
release that ships the SDKs — with the plan's §8.3 note that the page is **already ahead** of
them); and the website work — **PRs #689 and #705 are merged**, and the page they leave already
says the app's reports are always on (D255's answer) and that earlier builds send nothing.
**This task does not have to write those sentences**; it holds them true of the build that
ships, with `website/test/privacy.mjs`, which is on `main` and runs in `website/`'s own tests.
**Verification.** `node --test` in `website/` (the word ceilings, the origin gate, the copy
gate, and **`website/test/privacy.mjs`**, which holds the page's app sentences — *no
advertising identifier*, *no switch inside the app*, *uninstalling the app stops them*, *not
installing it avoids them*). **The two store forms are answered by the owner and recorded in the owner's notes** —
no Go test and no agent can see the Play console or App Store Connect.
**Reviewer tier.** **standard** for the website change; **owner** for the two store forms,
which no agent fills in.

### Task P7.5: Checks 120–122, written into the register

**Files.** `docs/gcp-live-checklist.md`. **Depends on.** P7.1, P7.2, P7.3.
**Verification.** `go test ./internal/cli -run 'Docs|Lint' -count=1`.
**Reviewer tier.** **standard** for the writing; **owner** for the running. **No agent runs
any step of 120, 121 or 122.**

### What holds Phase 7, and what each task cannot verify

**P7.1 → P7.3 → P7.4 → P7.5 is the critical path**, four deep, and **P7.2 and P7.3 are
parallel** (different directories, no shared file — `mobile/src/telemetry/` is split into two
files with two owners rather than one directory with two). **PR #675 has merged and left P7.1's
two store-path files on `main`** (`app.config.js`, `eas.json` — P7.1 modifies them in place),
and **PRs #689 and #705 have merged and left the page P7.4 lands against** — the page is ahead
of the SDKs by design, and P7.4 holds its sentences rather than writing them from scratch.
`mobile/` has **no CI job** (`.github/workflows/ci.yml` runs four jobs and none
of them runs the app's tests), so every `npm test` above runs **where someone runs it**, and a
job for the app would be a **new staged workflow** — **this phase stages none**, and the owner
decides whether one is wanted when the first task lands.

| The task | Verifiable with no device | Not verifiable |
|---|---|---|
| P7.1 | the tracked-file rules, the ignore rules, the `AIza…` walk, the guard's pure part | that the plugin applies or a development build is produced — `expo prebuild` and `eas build` need a toolchain this repository's CI does not have |
| P7.2 | the statement's text, the link's validator path, the shell's new child, and the absence of a switch and of any runtime collection call (the scan) | that the shell renders as intended on a device, and that the **native** SDK is what actually sends — only check 120 can show that |
| P7.3 | the scan, the never-send test, the existing fences | that the **native** SDK collects what the allow-list says and nothing else — only checks 120 and 121 can show that |
| P7.4 | the website's own gates | the two store forms (owner) |
| P7.5 | the register's own docs tests | the three checks (owner) |

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
| P2.1 | — | **D113, D113b, D112** (read grants; tensions (a), (b)) |
| P2.2 | P2.1 | D113, **D112** (read scoping) |
| P2.3 | P2.2, P4.2a | D113 |
| P3.1 | — | **D111, D112, D114, D125** |
| P3.2 | P3.1 | **D112** (answered: direct reads; tension (a)), D111, D124 |
| P3.3 | P3.2, P0.3 | D124 |
| P3.4 | P3.1 | D122, D123 |
| P3.5 | P3.4, P3.6, P1.2, P1.3, P1.4 | D122, D127 |
| P3.6 | P1.1, P1.3, P1.4, P3.1, **P6.3** (signing; approved 2026-10-18) | D111, D113, **D113b**, D118 |
| P4.1 | P1.1, P1.2, P1.3, P2.1 | D110, D128, **D113b**, **D113c** (answered (ii); named `fugaro mailbox`, 2026-10-17), D118 |
| P4.2a | P2.2 | D113, D120, D126 |
| P4.2 | all | D126 |
| P4.3 | P0.1 | D119 |
| P4.4 | P3.1, P4.2a | D120, D125 |
| P5.1 *(unscheduled)* | — | D115 |
| P5.2 *(unscheduled)* | **P6.4** (the rules generation the `config/admin/push/$uid` sibling hangs off — the design's Q2: push cannot ship before it; **approved 2026-10-18, merged** as #601), **P6.6** (the pairing and its revoke, which write and delete the device node and delete the sibling; **approved 2026-10-18**, not built), **P6.7** (the consumer — the design's §3.5 (b): it is the only path that can carry a phone's registration token, since D165's pairing request node admits only the public key and its timestamp, so `fugaro pair` writes no push node), **a mailbox-schema and rules change** for that message (a bounded registration kind with a required `token`, carrying §3.8's switches too: `shared/mailbox-schema/index.js` and `rules.json.tmpl`'s inbox field set refuse every field they do not name — a later rules generation, task not yet written) | D121, **D165, D166** (the two tokens; the device node as the allowlist) |
| P5.3 *(unscheduled)* | — | D123 |
| P5.4 *(unscheduled)* | — | D172 |
| P6.1 *(approved 2026-10-18)* | P0.1 | D164, D161, D163 |
| P6.2 *(approved 2026-10-18)* | P0.1 | D169, D162, D128 as amended 2026-10-17 |
| P6.3 *(approved 2026-10-18)* | P0.2 | D161, D167, D168, D169, D171 |
| P6.4 *(approved 2026-10-18)* | P2.2, P6.3 | D161, D166, D169, D171 |
| P6.5 *(approved 2026-10-18)* | P1.3, P1.4, P6.3 | D160, D168, D173, D127 |
| P6.6 *(approved 2026-10-18)* | P6.4, P4.1 | D165, D166, D171 |
| P6.7 *(approved 2026-10-18)* | P4.1, P6.2, P6.3, P6.4 | D169, D162, D168 |
| P6.8 *(approved 2026-10-18)* | P3.1, P6.1, P6.6 | D165, D170, D171, D114 |
| P6.9 *(approved 2026-10-18)* | P3.6, P6.5 | D167 |
| P6.10 *(approved 2026-10-18)* | P3.6, P6.3 | D167 |
| P6.11 *(approved 2026-10-18)* | P6.4 | D126 |
| P6.12 *(approved 2026-10-18)* | P3.5, P6.1, P6.2, P6.5 | D162, D169 |
| P6.13 *(approved 2026-10-18)* | P6.7 | D118, D168 (amended) |
| P7.1 *(approved 2026-10-22)* | none — **PR #675 merged `app.config.js` and `eas.json`, and P7.1 modifies them in place** | **D259 *(accepted 2026-10-19)***, D260 *(answered)*, D254 *(answered — the project is the owner's step)* |
| P7.2 *(approved 2026-10-22)* | P7.1 | **D255** *(answered: always on — the task is the statement and the link, not a gate)*, D124 |
| P7.3 *(approved 2026-10-22)* | P7.1 | **D256** *(answered)* |
| P7.4 *(approved 2026-10-22)* | P7.3 | **D257** *(answered)*, and the website work — **#689 and #705 merged on `main`** |
| P7.5 *(approved 2026-10-22)* | P7.1, P7.2, P7.3 | D126's rule (a check is never run before its text is in the register) |

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
| **C6** | The app is versioned with the CLI's tag (D11's precedent) but released on the stores' cadence — **superseded 2026-10-22: the owner gave the app its own tag, `mobile-vX.Y.Z`, and this plan's recommendation in the cost column is the road not taken** ([../release.md](../release.md), "Mobile releases") | Its own version line — cost: two version lines to reconcile, for an artifact that ships with the CLI's features |
| **C7** | **Withdrawn — now decision row D113c, answered 2026-10-17: a new `fugaro` command** (option (ii)), which console D19 sends to a CLI plan; the command is named `fugaro mailbox` (tension (c), resolved 2026-10-17). Where the consumer lives was a choice between following Voice D40 and D50 (the helper) and departing from them, so it was the owner's question, not this plan's | — |
| **C8** | The store's internal track is used for check 89, and no public release is implied | A public release — cost: a public app is not reversible, and the spec does not ask for one |

---

## Open items, listed rather than fixed

1. **Two PR-link gaps, and they are not the same gap.** `JSONRun` (`internal/watch/json.go`) carries no PR field, so a **live** row has no URL at all (console §4.3 **gap 6**); `JSONFinished` **does** carry `pr_url` and `pr_number`, so a **finished** run has one. But **no shape carries a provider** (**gap 7**), so the URL cannot be validated against a host allow-list and the console builds **no link at all** — repository, number and URL as text. Until the allow-list exists (D124, P0.3) the app does the same, and the live half of the list cannot carry a link until gap 6 closes on the CLI side. **Neither gap is closed by this plan.** *Note 2026-10-27:* the **live** row (`JSONRun`) now carries `pr_number` (console graph GV2.5): the draft PR's number, parsed from the run's PR URL with the watch package's anchored GitHub URL pattern, and absent for any URL that pattern does not read. It still carries no `pr_url` and no provider, so the live half still has no link to build.
2. **The ack file is local** (`internal/watch/acks.go`'s `AckPath`). The phone cannot share the desktop's acknowledgements, so the app's "show all" toggle is over the unfiltered feed. Stated in F2 and in P3.3; **not solved**.
3. **"Awaiting user input" has no producer** anywhere in the tree (F3, and Voice D48 reached the same conclusion). The digest cannot speak a state nothing produces.
4. **The CI job is owner-made** (P4.3), because of a hard constraint on workflow files, not because the plan prefers it. Until it exists, the shared package's tests run **only where someone runs them**, and that is a real gap in this plan's coverage.
5. **Checks 80–89 are owner-run and none has been run.** Until they are, every live claim in the app is unverified.
6. **`belong` has not been looked up** — not whether it has a Firebase project, not what its installation tuple is — because no agent touches it (F31, U8).
7. **The console's voice module does not exist** (Voice D50/D53), and **D119 amends where the shared code lives without making that module exist**. P1.3 and P1.5 are written for the shared package, which the console would consume; until the console's side exists the shared package has **one** consumer, not two, and "shared" is a claim about the future.
8. **The three tensions of 2026-10-17** ([reconciliation.md](reconciliation.md) §4): (a) D112's direct reads against D124's documented feed as the job list's source; (b) what D112's answer does to D113's grants, D111's token scope (and the pairing key D113b's answer leaves unstated) and D118's read-side exposure; (c) the name of D113c's new `fugaro` command. **All three were resolved by the owner on 2026-10-17** (direct reads under the reader rules; a four-family read grant with no titles; the MAC key confirmed with a D54b-shaped exception; `fugaro mailbox`), each with a dated "Resolved" block in §4. **Open item 1's finished-run PR link is now moot for v1: the phone does not see finished rows.** **No task is approved by this.**

9. **Phase 7 (telemetry) is approved and unbuilt, and its own plan carries the UNVERIFIED list.**
   Added 2026-10-22. **The owner approved the phase on 2026-10-22, on the condition that its
   plan is merged first** ([../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md)).
   The flag names it requires, the EAS file-secret mechanism and
   `@react-native-firebase`'s support for a second Firebase project in one binary are **read
   from memory of the SDKs' documentation and not from a page read today** — including the
   option that must switch ad-ID collection off on each platform (P7.1, and the plan's §14);
   `mobile/app.config.js` and `mobile/eas.json` are **PR #675's and are merged**, so P7.1
   modifies `app.config.js` in place and leaves `eas.json` as it stands; `<the dev host>` is the
   brief's name for the first internal build's environment and **is in no file** — *amended
   2026-10-31: this line spelled the host out while saying it was in no file, the one thing it
   must not do, so it writes the placeholder form `mobile/README.md` writes for the values that
   belong in the EAS project and not in this tree* — and the
   website's privacy page is stated as **PRs #689 and #705** leave it — **both merged**, so the
   page is read from this tree, but **`website/`'s own gates are not run by this repository's
   CI** and this change does not run them. Each is listed in the plan's §14 rather than smoothed
   over, and P7.1 confirms the first three against the SDK's own documentation and the generated
   native project when it starts.

---

## OWNER APPROVAL GATE

**Nothing in this plan is approved.** *(Status 2026-10-17: steps 1 and 2 are done — every decision is answered — and steps 3–7 are not taken, so no task is approved; the three tensions in [reconciliation.md](reconciliation.md) §4 were resolved by the owner on 2026-10-17, which approves nothing.)* The steps below are taken **in this order**. No task may start — not P0.1, not the docs write-back — before step 1 is done; after that, a task starts only when **every step that names it** is done and **every decision row its "Cross-task dependencies" line names** is answered.

**1. Answer the gating six.** **D110** (does this supersede Voice D49?), **D111** (what may the app hold?), **D112** (direct database reads, or the CLI's read path — **and which transport carries it**), **D113** (the rules change, and the owner-run deploy), **D113b** (who writes the outbox and the heartbeat, and how that writer is authenticated), **D114** (the account model). **If D110 is answered "no", phases 1, 3 and 4 are not built**, and this plan reduces to the job list and the pure modules that survive. **If D113 is answered "no rules change", there is no mailbox**, because the phone has nothing to write to and nothing to read.

**2. Answer the rest, or accept the recommendations in writing.** D113c and D115–D129 are not part of step 1, so they do not hold back a task that does not name them; each holds back exactly the tasks whose "Cross-task dependencies" line names it (D113c holds back P4.1, which has no recommendation to accept), and D126 gates whether checks 80–89 are the plan's proof at all.

**3. Approve Phase 0 and Phase 1 as a block.** They are pure, they need no live anything, they touch no existing behaviour, and they are the cheapest possible way to find out whether the mailbox's semantics are right. **Approving them is not approving the app.**

**4. Approve Phase 2 separately, and only then.** **P2.2 is a test-only change and is safe to merge.** **P2.1 is not test-only**: it edits the **production** rules template, so the next owner who runs `fugaro init --firebase` from a binary built on it **deploys it** — merging P2.1 is approving the content of that deploy, even though it changes nothing until someone deploys. **P2.3 is the deploy itself and needs its own explicit go**, per Global Constraint 7. Approving Phase 2's code is not approving its deploy.

**5. Approve Phase 3 only after the shared package exists** (D119, P0.1) **and the console's voice module exists** (Voice D53), or say explicitly that the app builds the pure parts ahead of it. **D119 amends D50 by moving the shared module to a top-level directory both artifacts consume**; the app's critical path runs through that package, and this plan does not hide it.

**6. Approve P4.1 separately.** It is the task where a phone's message becomes an action on the operator's behalf, and D128's answer is what it is built from — and **D113c's** answer decides whether it is built here (the helper) or in a CLI plan (a `fugaro` command).

**7. Answer C1–C8 (C7 withdrawn into D113c), or let them stand.** They are the plan's own provisional choices, and each names the task it changes.

**7b. The telemetry plan's block (D253–D260), and the phase it registers — added 2026-10-19, PASSED 2026-10-22, for Phase 7 only.** *(One owner step outside that block: **P7.2 cannot render the `/privacy/` link until D124's host list names the website's host.** `shared/url`'s `DEFAULT_HOST_ALLOW_LIST` ships empty and D124's answer names no hosts, so the URL is refused with `allow-list-empty` and renders as text; the plan's §6 states the step and check 120 names it.)* *(Dated note, 2026-10-19: the owner answered **seven of the eight rows** the same day — **D253 to D258, and D260**, D255 as **always on**, against the recommendation — and **D259 was the one row still open**. **Answering a row is not approving a task**.)* *(Dated note, 2026-10-19, later the same day: **the owner accepted D259** — the telemetry project's native config is **built into the app**, each installation's own Firebase connection arrives in the **encrypted pairing payload** and lives in **secure storage**, and **D111 stays at two items (idToken, macKey) plus the pairing-delivered configuration (the installation's database URL and web API key, ordinary configuration, not secret)** — the owner's ruling of 2026-10-29: D232 stands over D259's four-item list (Global Constraint 4). **Every row this step names is therefore answered**.)* **The step is PASSED, dated 2026-10-22, and it carries one condition: this plan is merged first.** On that condition **Phase 7 is APPROVED — tasks P7.1–P7.5 may start** — and **approval is permission to start, not a claim that anything exists**. **D254 is an owner step as well as a decision** — the project is **`fugaro-prod`**, it exists, and the owner creates the two Firebase apps in it **by hand**; nothing in this repository can do that. **Phase 7 changes no approved phase and holds back no existing task.** **The draft on PR #693 asked for D193–D200 and checks 111–113**, which other works had taken before it merged; the block this step passes is **D253–D260** and the checks are **120–122**.

**The recommendations in [reconciliation.md](reconciliation.md) are not answers.** This plan is written so that either answer to each decision leaves a coherent plan; it is not written so that the recommendations can be treated as settled.

---

*Status of this document: plan only; nothing is implemented. D110–D129 (twenty rows, plus D113b and D113c) were answered by the owner on 2026-10-17, and the three tensions between the answers were resolved the same day (reconciliation §4); no task is approved. Checks 80–89 are reserved and none has been run. Related: [spec.md](spec.md) · [reconciliation.md](reconciliation.md) · [README.md](README.md).*
