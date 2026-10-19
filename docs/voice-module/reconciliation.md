# The voice module spec, reconciled against the tree

*Status: 2026-10-19 · owner: Dimi Paun · **reconciliation only; nothing is implemented.** This document reads the owner's [spec.md](spec.md) — the voice module spec of 2026-10-18, committed verbatim and alone in its own commit — against the code, the designs and the open pull requests this repository already carries. It builds nothing, approves nothing, and writes no plan: **the owner's answers to §5 come first and are the gate.***

Baseline: **`origin/main` at `82ba600d984a420c595cf06417af6d781c18535a`** ("console: frame the golden screenshots on the screen, not the window (#619)", 2026-10-18). Every claim about "the tree" below was checked against that commit; every path exists there and every symbol named was read there. The open pull requests were read from their own branches, which were fetched read-only and **not** modified:

| PR | Branch | Tip | Read for |
|---|---|---|---|
| #592 | `fugaro/20261018-132817-67ec` | `f36cc429` | P3.4 — the audio interfaces, their fakes and the pipeline |
| #590 | `fugaro/20261018-132637-e66c` | `125e1650` | P3.2 — the phone's feed reader |
| #604 | `fugaro/20261018-151705-dc83` | `bd8e439b` | P3.6 — the phone's mailbox client |
| #602 | `fugaro/20261018-151658-6559` | `130ce03d` | P6.5 — the prompt queue and the interrupt policy |
| #633 | `fugaro/20261019-001448-8569` | `9b5cc838` | P5.3 — the cloud STT/TTS allow-list (the design of the switch) |
| #634 | `fugaro/20261019-002947-618d` | `9fa0bb57` | the console styling design |
| #584 | `fugaro/20261018-130633-f104` | `04ac88f8` | the credentials service, Slice 0 and Task C0 |

**Three paths cited below are not on `main` and their links will not resolve until their pull requests merge** — [docs/mobile/design/cloud-stt-tts.md](../mobile/design/cloud-stt-tts.md) (⊂ #633), [docs/design/console-styling.md](../design/console-styling.md) (⊂ #634) and [docs/design/project-named-oauth-tokens.md](../design/project-named-oauth-tokens.md) (⊂ #584). Everything else this document links to exists at the baseline commit. All three are cited anyway, because each is load-bearing — the first two for §1, the third for the "carrier is a stopgap" line in #32 — and none has a `main` equivalent to cite instead.

A warning about PR #604, because the brief that asked for this document names it as "the phone's Phase 3 voice screen": **it is not.** #604 is **P3.6**, the phone's mailbox client and the app's one write path. The voice screen is **P3.5** (`mobile/src/voice/`), it has **no pull request**, and `mobile/implementation-plan.md` lists it as level 4 with "Its own PR — the voice screen is the feature". Nothing below reads #604 as the voice screen.

## 0. What this document is, and how its claims were checked

The owner's spec was written **without seeing this repository**, and it shows in a specific way: it is not wrong about the shape of the feature — a transcript-in/text-out pipeline with swappable backends, task events classified and summarised rather than spoken raw, and a security story for provider keys — but **every one of those shapes is already a document here**, and the tree has already made, and in places already built, the decisions the spec reopens.

**What the spec is, in this tree's terms.** It is a **revision of [voice-spec.md](../design/voice-spec.md)** — the owner's own voice spec of 2026-10-15, which the repository committed verbatim the same way this one is committed, and reconciled in [voice.md](../design/voice.md) as findings **F1–F38** and **decisions D39–D54b, all answered by the owner on 2026-10-16**. The two documents share a goal, an architecture, a non-goal list and a security story. The 2026-10-18 text drops the earlier one's `AgentBridge`/`Summarizer`/`EventDigester` component names, adds named vendors and a runtime engine toggle, and adds a wake word that the 2026-10-15 text listed as a **non-goal for v1**. Reading it as a fresh spec is what would produce a duplicate; reading it as a revision is what produces §1's table and this document's findings.

**Two further documents decide most of what follows, and both are newer than the reconciliation in voice.md:**

- **[docs/mobile/](../mobile/)** — the phone app: decisions **D110–D129** (answered 2026-10-17), the functional spec v0.1 reconciled as **G1–G22, T1–T6, O1–O7 and D160–D173**, and an implementation plan whose Phases 0, 1 and parts of 3 are **merged on `main`** as the pure-logic packages under `shared/`.
- **[docs/credentials-service/](../credentials-service/)** — the "security service" the spec's *Keys and security* section names: findings F1–F37, **decisions D130–D154, answered 2026-10-17**, an implementation plan in slices. Its own status line reads: *"**plan only; nothing is implemented** … no task approved"*.

**Where the plan documents and this brief differ, the brief's rulings win**, and one of them matters here: the brief records that the owner **approved the local/remote evaluation switch on 2026-10-19 as seven points** — default local, a persistent indicator, a named provider allow-list with terms recorded, no remote audio during a confirmation or on destructive paths, no credential in the app binary, local fallback, and Check 85 as the bake-off. Those seven points are written in [docs/mobile/design/cloud-stt-tts.md](../mobile/design/cloud-stt-tts.md) on PR #633, whose own "Open for the owner" section still carries **Q1–Q6 unanswered**. §1 reconciles that state with this brief rather than silently picking one.

**How the claims were checked.** Every finding cites a **path and a symbol** (a Go function, a TypeScript export, a constant, a decision number), never a bare line number, and each was read at the baseline above. The provider claims in the spec's *Speech-to-text* and *Keys and security* sections were checked against the **providers' own public documentation on 2026-10-19**, read-only and with no credential of any kind: the result is §6's verification table, and it changes three of the spec's sentences. **Anything not verifiable is marked UNVERIFIED and is never asserted** (§7).

**Nothing here is approved, and nothing here is a plan.** There is no task list, no phase, no check written and no number allocated. §5's questions are the gate.

## 1. How this spec and P5.3's seven points unify — one decision, two documents

The brief's ruling is that the spec's **"engine toggle and experiments"** and **"keys and security"** sections and the design on **PR #633** are **the same decision seen from two sides**: the owner's, which asks "may this leave the machine at all, and under what conditions", and the module's, which asks "what does the switch do". They are one decision and this document treats them as one. The recommendation, which §5 Q1 turns into a question for the owner, is:

> **[cloud-stt-tts.md](../mobile/design/cloud-stt-tts.md) (PR #633) is authoritative for *what is permitted*: its seven points are conditions on any cloud voice work in this repository, console or phone, and this spec may not be built in a way that breaks one of them. [spec.md](spec.md) is authoritative for *what is built*: where it answers one of P5.3's open questions, its answer supersedes that question; where it does not, the question stays open and is not re-asked here.**

Applying that rule to P5.3's six open questions:

| P5.3's question (PR #633) | Does this spec answer it? | Where |
|---|---|---|
| **Q1** — the second explicit egress decision itself: take it or not? | **No — and it presupposes it.** The spec says "Deepgram and AssemblyAI behind the cloud toggle" and "Cartesia (low-latency streaming)" as though the decision were taken. It records no egress terms, names no retention reading, and takes no decision. **Q1 stays open and is the gate** (§5 Q1). | spec *Speech-to-text*, *Text-to-speech* |
| **Q2** — where the allow-list lives, and how the phone learns it | **No.** The spec's "cloud icon on the voice screen" is a **runtime choice by a person**, which is not the allow-list and cannot be substituted for it (§3 finding #8). Q2 stays open; this spec adds a question about the two together (§5 Q3). | spec *Engine toggle* |
| **Q3** — where the provider credential lives | **Yes, and differently from P5.3's recommendation.** P5.3 recommended a Fugaro-hosted **proxy** with a short-lived per-device token — the key stays in the cloud but **the audio passes through Fugaro's own service**. The spec says the service **mints short-lived temporary provider tokens per session** and the **clients stream directly to providers**, which keeps Fugaro's service out of the audio path entirely. That is a third shape and it supersedes Q3 (§3 findings #28–#32). | spec *Keys and security* |
| **Q4** — which check number the cloud half takes | **No.** The spec asks to "test both" providers but writes no check. Q4 stays open and is inherited unchanged. | — |
| **Q5** — where the provider register lives, and who keeps it current | **No.** P5.3 §4.3's register — the table recording each provider's retention, training and the date the owner read it — is not addressed by the spec, and this document treats it as **a condition, not an option**: the spec's §6 verification is an instance of exactly that table (§3 finding #36). | — |
| **Q6** — a cloud transcript, and the confirmation | **Yes.** The spec is silent on destructive commands, so P5.3's §5 rule 2 freeze — nothing at all is sent while a confirmation is pending — stands unmodified, and this module adds no exception to it. | spec *Filtering and summarising* (silence) |

**Consequence for the folder.** `docs/voice-module/` does not restate P5.3's seven points; it cites them. A reader who wants the conditions reads `cloud-stt-tts.md`; a reader who wants to know what the module does reads this document. §5 Q1 is where the owner takes the one decision both documents are waiting on.

## 2. The table: what the spec asks for, and what the tree already has

One row per concept in the spec, in the spec's own order. "The tree" means `origin/main` at the baseline above; "⊂ PR #n" means the artifact is on that pull request's branch and not on `main`.

| Spec concept | What the tree has | Finding |
|---|---|---|
| Hands-free voice interface for the Fugaro orchestrator | [voice-spec.md](../design/voice-spec.md) §1–§3 — the same goal, 2026-10-15, committed verbatim the same way | #1 |
| Pipeline, not speech-to-speech: STT → orchestrator/LLM → TTS | `voice-spec.md` §4's diagram and component table; `voice.md` D39 | #1 |
| One common interface per direction; backends swappable | `voice.md` **D52** (four ports: `AudioIn`, `SpeechToText`, `TextToSpeech`, `AudioOut`); ⊂ PR #592, `mobile/src/audio/` ports and their fakes | #2 |
| "a separable module **in the Electron console**" | `voice.md` **D50**, amended by mobile **D119**: a pure-logic package at the repo root (`shared/`), because there are two consumers | #3 |
| App stays in the foreground, no background listening | mobile **D122** and Check 86 plan a **background audio mode** | #4 |
| Apple SpeechAnalyzer, OS 26, via a native Swift helper from Electron | nothing; the console spawns exactly one binary (`console/src/main/spawn.ts`) | #5, #23 |
| Android on-device SpeechRecognizer | mobile **D123**: "the platform's own on-device engines", bake-off first | #20 |
| whisper.cpp on Linux/Windows, as a Mac baseline | nothing | #20 |
| Deepgram and AssemblyAI "behind the cloud toggle" | **D44** local-only for v1 + a second explicit decision; ⊂ PR #633 the design of that decision | #6 |
| Silero VAD so only speech is transcribed | nothing; VAD is folded into `AudioIn`'s `speech-start`/`speech-end` with no engine named | #21 |
| Wake word "Fugaro" via openWakeWord or Porcupine | `shared/command`'s `WAKE_ALIASES` — a **transcript prefix test**, not an acoustic engine (D164, P6.1) | #7, #43 |
| Kokoro on all platforms; Apple voices as fallback | nothing | #20 |
| Cartesia; ElevenLabs later | nothing | #6, #30 |
| A cloud icon on the voice screen switches local/cloud | ⊂ PR #633 §4.1's allow-list and §4.4's **non-dismissible indicator**; no voice screen exists in any artifact | #8, #25 |
| Record raw session audio to replay clips through each engine | mobile **D118**: "the app never stores or uploads audio"; `voice.md` F32: audio fixtures are **synthesised, not captured** | #22 |
| Log latency to first partial/final, CPU and battery | nothing | #41 |
| Provider keys at project level in the per-project security service | `docs/credentials-service/` — plan only, and its scope is **jobs**, not clients | #31 |
| Short-lived temporary tokens minted per session, clients stream direct | verified per provider (§6); **no Fugaro mechanism mints any of them**; D130–D154 mint nothing of this kind | #32, #37 |
| Rules-based classifier: urgent / mention / silent | `shared/digest/severity.js`'s `SEVERITY` (`kill`, `failed`, `fast_burn`, `info`) — merged on `main` | #9 |
| Debounce window batches bursts into one utterance | `shared/digest`'s batching input; `voice-spec.md` §6 | #10 |
| A small fast LLM (Haiku) turns a batch into one spoken line | **D45** and `voice-spec.md` §6 — designed, unbuilt; the `Summarizer` symbol does not exist | #11, #33 |
| On-demand digest: "what's going on?" | `voice-spec.md` §6; `shared/digest` | #10 |
| Interrupt switch: cut in now or wait for a pause | `shared/prompts` ⊂ PR #602: `SWITCH` (`narrow`/`default`/`widen`) and its `INTERRUPT` set | #12 |
| Echo cancellation; TTS through the pipeline the canceller monitors | `voice.md` acceptance 5 (~300 ms), plan **V5.2**, Check 86 | #38, #39 |
| Phone: routing, interruptions, wake lock, handoffs, model size | mobile **D122**; ⊂ PR #633 §4.6 fail-closed-to-local; the **wake lock has no home** | #42 |
| "No destructive voice commands" (the owner's ruling) | **D47**, amended 2026-10-17; mobile **D128**; `shared/confirm` — merged on `main` | #15, #45 |

## 3. Findings

Each finding is classified **Wrong** (the spec, or a document here, says something the code contradicts), **Exists** (the tree has it and the spec should be read as naming it), **Gap** (nothing in the tree does it), **Rule** (not a fact but a constraint the tree's existing decisions impose on how this may be built), or **Open** (a question this document cannot answer from the tree alone).

### (a) The mobile voice work, the shared module, and the switch

**#1 — The spec is a second edition of a document this repository already committed and already reconciled. (Exists)**

[voice-spec.md](../design/voice-spec.md) is the owner's voice spec of 2026-10-15, committed verbatim; [voice.md](../design/voice.md) reads it against the console and the mobile work and carries **F1–F38** and **D39–D54b**, every one answered by the owner on 2026-10-16. The shared text is substantial: both have the goal ("hands-free"), both name the pipeline and reject speech-to-speech, both list multi-user voice and voice-driven code editing as non-goals, and `voice-spec.md` §4's component table (`AudioIn`, `SpeechToText`, `TextToSpeech`, `AgentBridge`, `Summarizer`, `EventDigester`, `VoiceController`) is the architecture the new text describes in prose. **What the new text changes** is the vendor list, the runtime engine toggle, the wake word (a non-goal in 2026-10-15, in scope in 2026-10-18), and the divorce of the interfaces from the `AgentBridge` naming.

**Consequence.** The two texts must not both be authoritative. This document treats the 2026-10-18 spec as the **later** one for what the module *does*, and voice.md's D39–D54b as standing until the owner supersedes a row of it — the way mobile D129 requires supersessions to be recorded. **Nothing in voice.md is reversed here**; §5 Q9 asks the owner for the rule.

**#2 — "One common interface per direction" already exists, twice, and the two disagree about how many ports there are. (Exists)**

`voice.md` **D52** names **four**: `AudioIn`, `SpeechToText`, `TextToSpeech` and `AudioOut` — the fourth being what plays speech out, which matters because §g's echo-cancellation rule needs a *writable* audio path, not just a readable one. ⊂ **PR #592** (`mobile/src/audio/`) implements **three** — `isAudioIn`, `isSpeechToText`, `isTextToSpeech` in `mobile/src/audio/{audio-in,stt,tts}.js` — and folds output into `TextToSpeech` itself. `mobile/implementation-plan.md`'s P3.4 verification says "The interfaces exist (AudioIn, STT, TTS)"; `AudioOut` is not in it.

Two further facts about those ports decide how the spec's "audio frames in → partial/final transcripts out" maps onto them. First, **they are shape predicates, not TypeScript interfaces**: *"There is no TypeScript here and no `interface` keyword: a port is an object with methods … and `isAudioIn` is that check."* Second, **audio does not cross any port**: `AUDIO_IN_EVENT_FIELDS` is `['kind']` — the events carry no samples — and `stt.js` states the rule, *"Text crosses this port. Audio never does."* That is D118's answer, and it is why the spec's "audio frames in" has no existing carrier on the phone side.

**Consequence.** The module's one-interface-per-direction claim is satisfiable, but it is satisfied by **the ports that exist**, and the spec's frame-level phrasing needs a decision about which process holds frames (see #21 and #39). The `AudioOut` gap is real: whichever document is authoritative for the port list, one of them is wrong about the fourth.

**#3 — The spec puts the module in the console; the tree moved it out, deliberately. (Wrong)**

The spec's first sentence — "starting as a separable module in the Electron console, later in the phone app" — is the placement `voice.md` **D50** originally chose, and it was **amended** by mobile **D119**: *"A pure-logic package in this repository, in its own top-level directory — and that is an amendment of D50"*, because there are now two consumers and `console/` is outside the Go module with its own boundary tests. On `main` the result is visible: `shared/` holds `command`, `confirm`, `digest`, `lease`, `mailbox`, `mailbox-schema`, `mailbox-sim`, `presence`, `skeleton` and `url`.

**Consequence.** Building the module *inside* `console/` would place it on the wrong side of a boundary the repository tests for (`internal/cli/docs_console_boundary_test.go`: `TestConsoleIsOutsideTheGoModule`, `TestGoModHasNoConsoleDependency`). The module belongs in `shared/`; the console and the phone are its two consumers.

**#4 — "Stays in the foreground, no background listening" collides with the mobile work's background audio mode. (Open)**

The spec's *Goal* states it twice ("App stays in the foreground while talking (no background listening)"). The mobile plan's **Check 86** is "Hands-free audio: **the background mode**, the microphone ask, Bluetooth, and barge-in", reserved for P3.5/P4.2, and `mobile/reconciliation.md`'s **U7** asks *"Whether the OS grants background audio with barge-in on both platforms"* — i.e. the tree assumes a background audio mode the spec's sentence excludes. On iOS "background mode" and "stays in the foreground" are not the same axis, so the two can be reconciled — but not by a reader guessing. §5 Q4 asks.

**#5 — The Swift helper the spec asks for has no carrier, and the console is built to have exactly one child. (Gap)**

`console/src/main/spawn.ts` opens: *"The console spawns exactly one binary — the installed `fugaro` — and never reads a database, a config file or a credential itself."* `console/package.json`'s `build.files` lists `dist/main/**`, two preload files, `dist/shared/**`, `dist/source/**` and five `dist/bundle/` files — **no second executable** — and there is no `extraResources`, `extraFiles`, `asarUnpack` or `binaries` key (asserted absent by the console's own tests). The design's answer to "the app needs a process" already exists though, and it is not the console's own: `voice.md` **D40** puts the Agent SDK in a **separate helper process outside main and renderer**, supervised by the console, using the operator's own Claude login. The spec's Swift helper is the same shape for a different job.

**Consequence.** A Swift audio helper is a **nested, separately signed, separately notarised binary inside the bundle** — a new packaging and release surface, not a new file. See #23 for what that costs and §5 Q6 for whether it should be a second helper or the same one.

**#6 — The spec's cloud engines presuppose the one decision the tree has deliberately not taken. (Rule)**

`voice.md` **D44** is answered, verbatim: *"**Local-only for v1** … Cloud STT/TTS is out of v1; it happens only by a **second, explicit owner decision**"*, with *"absent means nothing leaves the machine"* and a default-deny allow-list whose defaults are *"the opposite of `allow_data_to`'s"*. ⊂ **PR #633** is the design of that second decision, written and not built, and its **Q1** is the gate: *"The second explicit egress decision itself. Take it, or not?"*, recommending **not** taking it until Check 85 reports a measurement.

**Consequence.** The spec's *Speech-to-text*, *Text-to-speech* and *Engine toggle* sections are **conditioned on a decision that has not been taken**. This document does not take it, and does not read the spec as taking it. §5 Q1.

**#7 — The wake word already exists as a transcript prefix test, not an acoustic engine. (Exists / Rule)**

The spec asks for an acoustic detector (openWakeWord or Porcupine) that "routes speech to the app vs the active agent". The tree already has the routing, and it is by text: `shared/command/index.js` exposes `WAKE_ALIASES` — `Object.freeze(['fugaro', 'figaro'])` — and `routeUtterance`, whose rule is that an utterance whose first token is in the closed alias set is an app command and any other utterance is agent text. `mobile/functional-spec-v0.1.md` §3.4 states it ("Any utterance starting with 'Fugaro' is handled by the app"), `functional-spec-reconciliation.md` **F5/D164** reconciles it, and P6.1's verification pins it: *"an unmatched 'Fugaro …' yields 'not understood' and is never forwarded"*, plus *"a test pins that 'yes' is not an alias of 'confirm'"*.

Note the cost the tree already recorded: the reconciliation found that the spec's **"never misroutes"** claim **is not true**, which is why the recognition rate is proposed live Check 102.

**Consequence.** Two different mechanisms wearing one word. An acoustic engine firing on the wake word is a **second, independent** trigger whose failures are not the transcript test's failures — and it would necessarily run before any transcript exists, which is exactly the audio path #21 says has no carrier. §5 Q5.

**#8 — A runtime "cloud icon" is not the same object as the allow-list, and the tree has rules about both. (Rule / Open)**

The spec's *Engine toggle* section — "Cloud icon on the voice screen switches local/cloud (three-way on Mac: Apple, whisper.cpp, cloud)" — is a **per-session choice by a person**. ⊂ **PR #633** separates three things that the spec's one icon merges: the **allow-list** (§4.1: admitted *by name*, "not by URL", default-deny, *"absent, empty, malformed, or unreadable all mean local only"*), the **persistent indicator** (§4.4: *"an always-visible indicator, and it must not be dismissible"*, three states — local / cloud / **cloud requested but refused**), and the **fail-closed ladder** (§4.6: *"Every one of these is fail-closed to local"*, including *"the network is down | local only; nothing is queued for later, because a queued utterance is a recording"*).

**Consequence.** A toggle that a person can flip to cloud is exactly the "cloud requested but refused" state's reason to exist: it may only ever **narrow** what the allow-list permits, never widen it. The spec's three-way distinction on Mac also implies the toggle must name *which* local engine, which the allow-list's shape (a list of provider names) does not express. §5 Q3.

**#9 — The classifier the spec asks for exists, with a different and sharper taxonomy. (Exists)**

`shared/digest/severity.js` (merged on `main`) exports `SEVERITY` — `kill`, `failed`, `fast_burn`, `info` — `SEVERITY_ORDER` and `severityRank`, with the rule that *"a kill is the highest severity; a failed finish is next; a fast burn is next; nothing else interrupts."* `shared/digest/index.js`'s `digest(input)` maps the feed's shapes onto them: `JSONKill` → kill, `JSONFinished.Failed`/`.Status` → failed, `JSONBurn.FastAlert` → fast_burn, everything else → info. `shared/prompts` ⊂ PR #602 carries the second half: `evaluateInterrupt(input)` with the `INTERRUPT` set (`kill`, `cap-reached`, `cap-near`, `offline`, `prompt`).

**Consequence.** The spec's three tiers (urgent / mention / silent) are **finer on the low end** (the tree's ladder has no "mention" rung — everything below `info` is silence, which is the spec's "silent" tier by default) and **coarser on the high end** (the tree has two urgent reasons the spec does not name: `fast_burn` and `offline`). §4 recommends keeping the tree's ladder and adding one rung, rather than replacing it.

**#10 — The debounce window and the on-demand digest are designed but not built, and only one side of the batching exists. (Gap / Exists)**

`voice-spec.md` §6 designs the digester and the on-demand digest; `voice.md` **D39** is the data-plane answer; `shared/digest` implements the *classification* half. What does **not** exist anywhere is the **debounce window** — nothing holds a burst open — and the **"what's going on?" query**, which has no command, no wire shape and no reply. `shared/prompts` ⊂ PR #602 has the closest thing (a queue with `announcement` and `waitingSentence`), and it is a queue of *prompts*, not a window over *events*.

**Consequence.** The spec's §1–§2 are buildable on `shared/digest`; §3–§4 need a new component and a new wire shape, and §4 needs a route from a spoken question to an answer, which is a mailbox message (see #28) rather than anything that exists.

**#11 — The summariser is designed, unbuilt, and its model call is outside every Fugaro cap. (Exists / Rule)**

`voice-spec.md` §6: *"Model: a fast, cheap model (Haiku-class), configurable"*, with a config shape `summarizer: { model: <haiku-class>, max_sentences: 3 }`. `voice.md` **D45** is the answer that matters: the summariser's model call is **spend on the operator's own login** — D40's mechanism — and *"`fugaro budget show` will not show it and no Fugaro cap bounds it."* No `Summarizer` symbol exists in `internal/`, `shared/`, `console/src` or `mobile/src`. The only place "haiku" appears in code is the runner's **background role**: `internal/agent/env.go` sets `ANTHROPIC_DEFAULT_HAIKU_MODEL` from the per-repo `background` model — a run-time concept, not the voice summariser.

**Consequence.** "Whose credential, capped how" has an answer already: **the operator's, and not capped** — disclosed by D45 and by mobile **D116** (the summariser's spend is disclosed but sits outside Fugaro's caps). The spec's *Keys and security* section must not be read as proposing a Fugaro-managed summariser key; see #33 for why the credentials service cannot become one, and §5 Q7 for whether the owner wants an app-side cap anyway.

**#12 — The interrupt switch exists, with three positions rather than two. (Exists)**

`shared/prompts` ⊂ PR #602 (P6.5) exports `SWITCH` (`NARROW`, `DEFAULT`, `WIDEN`) and `INTERRUPT` (`KILL`, `CAP_REACHED`, `CAP_NEAR`, `OFFLINE`, `PROMPT`), with `evaluateInterrupt` returning `{ ok, interrupt, reason, pending }`. Its README states the default — *"**Queue, do not interrupt** is the spec's own default (§3.2)"* — the escalation ladder (re-announced after **one hour**, then once a day), and that the switch is a **local** setting: *"never written to the database."* Mobile **D127** is the decision row behind it.

**Consequence.** The spec's "urgent items cut in immediately or wait for a pause" is the two-position version of an already-designed three-position switch, and the design's extra position (`widen` → a pending `prompt` may interrupt) is the one the spec's own "agent asking a question" trigger needs. Build on `shared/prompts`; do not write a second switch.

### (b) The Electron console

**#13 — The console has no audio code at all, of any kind. (Gap)**

Grepping `console/src` for `getUserMedia`, `mediaDevices`, `AudioContext`, `MediaRecorder`, `microphone`, `audio`, `speech` and `voice` returns **nothing**. The only two hits for permission machinery are `console/src/main/window.ts`'s `denyEveryPermission(session: Session)`, whose two handlers are `setPermissionRequestHandler((_c, _p, cb) => cb(false))` and `setPermissionCheckHandler(() => false)`. The screens that exist are `live`, `projects`, `run`, `spend` and `degraded`.

**Consequence.** Every console-side claim in the spec — the Swift helper, the voice screen, the cloud icon — is greenfield, and it is greenfield in a window designed to deny everything.

**#14 — `connect-src 'none'` is the load-bearing directive, and the spec's "clients stream directly to providers" requires loosening it. (Rule)**

`console/src/main/protocol.ts` exports `CONTENT_SECURITY_POLICY`, byte-identical to the `<meta http-equiv>` in `console/src/renderer/index.html`:

```text
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'
```

The comment above it is explicit: *"`connect-src 'none'` is the load-bearing one: the renderer …"*. There is **no `media-src`** directive (so it falls back to `default-src 'none'`), no `'unsafe-eval'` and no `'wasm-unsafe-eval'` — the console's own tests assert both absences (`window.test.ts` refuses `unsafe-inline`/`unsafe-eval`; `csp-document.test.ts` also refuses `unsafe-hashes`, `strict-dynamic`, `*` and `http:`). `console/README.md` lists `connect-src` relaxed to `'self'` as a **mutation** its frozen tests catch.

**Consequence.** A renderer that opens a WebSocket to `api.deepgram.com` or `api.cartesia.ai` needs `connect-src` to name those origins — a **loosening in the one window this repository hardens hardest**, and one the console's tests are written to refuse. The alternatives, with their real costs, are §5 Q2's options. The same sentence covers Kokoro: ONNX/WASM in the renderer needs `'wasm-unsafe-eval'` added to `script-src`, which is a second loosening for a different reason (#39).

**#15 — Capture is outside the renderer by ruling, and the renderer holds no permission. (Rule)**

`voice.md` **D43** is answered: *"the renderer gains no network path and no permission, and capture happens **outside** it"*, and §5.1 rule 9 of the console design *"is not moved by this work"* — the owner **refused** the one-window amendment. `denyEveryPermission` is the code that makes it true. **D51** adds: the minimum platform entitlement only in the phase that ships audio, with `NSMicrophoneUsageDescription` mandatory and `com.apple.security.device.audio-input` *"only if the chosen capture mechanism needs it"*, and the prompt tied to a **visible session start**, not to launch.

**Consequence.** `getUserMedia` in the console renderer is not merely unimplemented; it is **ruled out**. The spec's Chromium `echoCancellation` design (#38) therefore cannot live where the spec implies — see #39.

**#16 — The macOS bundle grants exactly one entitlement, and not the audio one. (Exists)**

`console/entitlements.mac.plist` grants one key: `com.apple.security.cs.allow-jit`. Its own prose withholds `disable-library-validation`, `allow-unsigned-executable-memory`, `allow-dyld-environment-variables`, `debugger`/`get-task-allow`, `app-sandbox` **and every device and network entitlement including `com.apple.security.device.audio-input`**, saying why: *"the renderer reaches no network and no device, every permission request is denied by the main process rather than granted (design section 5.1 rule 9)"*. `console/test/unit/entitlements.test.ts` parses the file and asserts the exact set, one named test per withheld key. It is unverified until the owner runs **Check 59a**.

**Consequence.** A helper that captures audio needs `com.apple.security.device.audio-input` on **that binary**, which means the entitlements story becomes two files with two different rules — and the test that pins "one key" must be extended rather than edited quietly.

**#17 — The universal build, the hardened runtime and the release workflow already exist; the nested binary does not. (Exists / Gap)**

`console/package.json`: `"package": "npm run build && electron-builder --mac --universal --publish never"`; `mac.hardenedRuntime: true`; `mac.entitlements` and `mac.entitlementsInherit` both `entitlements.mac.plist`; `appId io.github.dimipaun.fugaro-console`, `asar: true`. There is **no** `afterSign`, `notarize`, `extraResources` or `binaries` key — signing and notarisation live in `.github/workflows/console-release.yml`, which is **STAGED, NOT APPLIED** (also in `docs/pending-workflows/`).

**Consequence.** A nested Swift helper must be **built for both arm64 and x86_64** (the app is universal), **signed with its own entitlements**, and **notarised as part of the bundle** — and `docs/pending-workflows/` is the only place a workflow change may be written in this repository (`.github/workflows/` may not be edited by an agent here). That is a real, unpriced piece of work, and §5 Q6 asks whether it is worth buying.

**#18 — The console's write fence is unchanged by a talking interface, and it bites the moment the module acts. (Rule)**

`desktop-console.md` **D2** — *"Yes, strictly read-only"* — and v2 **D22** — *"One: launching, through `fugaro run`, behind the CLI's own budget pre-check and the app's own confirmation … Everything else stays read-only"* — with **D14**'s reasoning: *"Treat every identity as a possible admin, and give the read-only guarantee to the allow-list alone."* Voice's **D47**, as amended on 2026-10-17 with mobile **D128**, allows **cancel**, **approve/merge** and **confirming an action** by voice **only with a spoken read-back and an explicit "confirm"**, and refuses every other destructive verb (kill switch, budget writes, deletes) outright. Console launches require the confirmation (v2 D22 stands, stricter than the phone's D162).

**Consequence.** A module that only *talks* touches none of this, which is why §4 treats "the module speaks and never acts" as a resolution rather than a question. The moment a spoken command reaches `fugaro`, the module is a **third path to a confirmed action** and must go through `shared/confirm` — never around it (#45).

**#19 — The console's style sheet does not exist, and a voice screen's UI depends on it. (Gap)**

⊂ **PR #634**'s `docs/design/console-styling.md` §1.1 states the situation: *"There is **no `.css` file anywhere under `console/`**"*, `index.html` is script-only, `style-src 'self'` *"is already the directive a stylesheet needs, and it is not being used"*, and the design *"adds nothing to the CSP and removes nothing from it."* §1.3: the golden frames are *"pictures of unstyled markup … a picture of an absence."* The design is unapproved (Phase 6).

**Consequence.** The spec's "cloud icon on the voice screen" is an icon on a screen whose styling has no design and no implementation, in a window that refuses inline style (`style-src 'self'`, no `'unsafe-inline'`) and whose element factory deliberately has *"no style and no event handler"*. Any voice screen UI is downstream of #634.

### (c) The orchestrator, and where text goes in and out

**#20 — "The orchestrator/LLM" is not a Fugaro service, and no engine is chosen. (Wrong / Gap)**

The spec's pipeline names a middle box that the tree does not have as a service. `voice.md` **F16** states it: a **Fugaro run** is a cloud container per run and is *"not an orchestrator"*; the thing the earlier spec called "the Fugaro orchestrating agent (a Claude Code session)" is a **Claude Code session**, *"which is not a Fugaro construct at all"*; the tree's only `claude` invocation is `internal/agent/agent.go`'s `Args` — headless, one prompt per stage, in a container. The console's bridge to a session is **D40**, designed and unbuilt: the Agent SDK in a separate supervised helper process on the operator's own login.

Separately, **no STT or TTS engine is chosen anywhere**: mobile **D123** prescribes *"a measurement-first bake-off with a stated latency target"* whose candidates are *"the platform's own on-device engines (Apple's and Android's) plus at most one bundled engine"*, recorded by **Check 85**, which has not been run and whose text is not in the register.

**Consequence.** The spec's vendored resume (SpeechAnalyzer, whisper.cpp, Kokoro, Deepgram, AssemblyAI, Cartesia, ElevenLabs, Silero, openWakeWord, Porcupine) **pre-empts D123's bake-off**. §4 resolves that the spec's list is a *candidate list for the bake-off*, not a selection; §7 records which of the vendor claims are verifiable.

**#21 — VAD has no interface and no engine, and the spec's "audio frames in" has no carrier. (Gap)**

⊂ PR #592's ports carry **no samples** — `AUDIO_IN_EVENT_FIELDS = ['kind']`, with `speech-start`, `speech-end` and `barge-in` — and its comment records why: D118's answer is that the app never stores or uploads audio. P3.4 names **no** VAD interface and no VAD engine; `docs/mobile/` does not contain the word "Silero" at all. `voice-spec.md` §5 has echo cancellation and VAD, and `voice.md`'s finding on them is that they are greenfield.

**Consequence.** "Silero VAD so only speech is transcribed" is new, and it is new in the one place the tree has an explicit architectural instruction: a VAD runs on **frames**, and frames exist only where capture happens — which D43 puts outside the renderer (#15). §5 Q2's decision chooses where frames live and therefore which VAD is possible at all.

**#22 — "Record raw session audio" is refused today, twice, in writing. (Rule)**

Mobile **D118**, answered: *"**The app never stores or uploads audio** — STT is on-device (D44), so the audio never leaves the device."* `voice.md` **F32**: audio fixtures are *"**synthesised, not captured**"*. ⊂ PR #633 §4.6 applies the same rule to a queue: *"nothing is queued for later, because a queued utterance is a recording (D118: no audio is stored)."*

**Consequence.** The spec's *Engine toggle and experiments* bullet — "Record raw session audio to replay the same clips through each engine" — is not a small experiment; it is the one thing two documents forbid, and it is the only way to run the bake-off the spec wants. A bake-off needs **comparable inputs**: without recorded audio every engine is measured on different utterances. That tension is real and §5 Q8 asks the owner to resolve it, with synthesised clips as the default answer F32 already gives.

**#23 — Where "text in / text out" attaches, in both directions. (Exists / Gap)**

- **Out (text to speak).** The feed is a **document**, not a subscription: `fugaro watch --json` emits one `JSONDoc` per change (`internal/watch/json.go`'s `BuildJSON`), and its consumer contract is written down on `main` in `console/docs/json-contract.md` §10 — thirteen facts, including §10.11 (*"Does option A need a supervisor process? Yes"*) and §10.13's reader rules, pinned by `internal/cli/docs_desktop_console_test.go`'s `TestDocsDesktopConsoleContractNamesEveryWatchField`. The console's reader is `FugaroSource` (`desktop-console.md` §8.3). **The phone does not read this**: ⊂ PR #590's reader subscribes to **database nodes** (`mobile/src/feed/index.js`: *"The phone reads database **nodes**, not the console's `watch --json` document"*), over a closed family list (`FAMILIES = ['config/caps', 'config/kill', 'spend', 'mailbox']`).
- **In (text from the person).** For the phone this is the mailbox, and it exists on `main`: `shared/mailbox-schema` (with `validateMessage`, `macOf`/`verifyMac` and pinned `vectors.json`) and `shared/mailbox`. The `kind` field is `message` · `prompt` · `answer` · `action`; `text` is capped at 4096 code units and is **already redacted**; the command/replay window is `DEFAULT_WINDOW_MS = 6 * 60 * 60 * 1000` bounded by `MAX_WINDOW_MS = 7 days`; the prompt TTL is the owner's **O5**, `PROMPT_TTL_MS = 72 hours` ⊂ PR #602. For the console it is **D40**'s helper, unbuilt, and the tree's only precedent for talking to Claude Code is the one-way, one-prompt-per-stage `internal/agent/agent.go` — which has **no `--input-format` flag at all**.

**Consequence.** "One common interface per direction" maps cleanly onto **an existing interface out (the feed, twice over, with two different sources) and an existing interface in (the mailbox for the phone; nothing for the console)**. The honest statement is that the *out* direction has one interface per consumer, not one interface overall, and the *in* direction has a working one only on the phone.

### (d) Event classification, summarising, and what the summariser sees

**#24 — The spec's four "silent" and "urgent" triggers against the feed's actual shapes. (Exists / Gap)**

From `internal/watch/json.go` and the run store:

| Spec trigger | The tree | Where |
|---|---|---|
| job failed | **Exists** | `JSONFinished.Failed`, `.Status`; `internal/watch/finished.go`'s `isFailure` covers `failed`/`halted`/`infra_error` |
| spending cap hit | **Partial** | `JSONTotal.Kill`/`JSONRepo.Kill` (`JSONKill{By, At, Reason}`); a cap halt is the run store's `Status=halted` with a `HaltReason` (`run_cap`, `repo_daily_cap`, `global_daily_cap`, `token_cap`) — there is **no "cap reached" event shape** of its own; `Percent` is the fraction, and the tree's one alert is a **rate** (`JSONBurn.FastAlert`, from `watch.burn_alert_usd_per_hour`), not a fraction |
| PR ready | **Exists** | `JSONFinished.Outcome == "ready"`, `JSONDoc.Ready`; `internal/watch/prstate.go`'s `PRState` |
| task done | **Exists** | `JSONFinished` with `Status=succeeded` |
| **agent asking a question** | **Nothing** | #25 |
| edits, tool calls, retries | **Nothing** | no field, no event; the watch feed has no such concept, and `PRState`'s internal retry/backoff emits nothing |

**Consequence.** Two of the spec's three "silent" examples name events that **do not exist**, which makes the "silent" tier the *default* rather than a decision — which is what `shared/digest` already does. The "spending cap hit" trigger needs a producer or a re-reading (see #25's shape).

**#25 — "Agent asking a question" has no state, and the queue that now holds questions does not reach the feed. (Gap)**

`voice.md` **F3**: *"**The fourth — 'anything awaiting user input' — has no state anywhere in this tree.** Nothing carries it, nothing computes it, and no word for it exists in any shape."* **D48**: the owner ruled that trigger **"dropped until something defines it."** Two things have moved since, and neither closes it for the console:

1. `internal/runstore/runstore.go`'s `Status` (`Running`, `Succeeded`, `Failed`, `InfraError`, `Cancelled`, `Halted`) and `HaltReason` still mean nothing of the kind; `JSONRun.Halted` is `omitempty` and carries a halt *reason*.
2. `shared/prompts` ⊂ PR #602 **is** the first thing that holds a question — its own header says so — but it holds it **inside a consumer's local queue**, and its `INTERRUPT.PROMPT` is a reason the *switch* may speak something; it is not a field in any `watch --json` document, and D127 requires the switch to stay local (*"never written to the database"*).

The only artifact that ever named a producer for this is `voice.md` **D54**'s `onAwaitingInput` — the Agent SDK's permission callback, implemented *"only if U7 finds it can surface a prompt"*, otherwise *"`onAwaitingInput` has no implementation"*. No such symbol exists.

**Consequence.** The spec's most useful urgent trigger is the one with no producer on the console side. It is a legitimate thing to want; it needs a **producer decision**, and §5 Q10 asks for it rather than assuming one.

**#26 — What the summariser sees is run titles and task text leaving to a model provider, and the tree has no key that governs it. (Rule)**

`voice.md` **F9** is the finding: `allow_data_to` is a real, enforced key — `config.ModelProvider.AllowDataTo []string` (`internal/config/providers.go`), validated by `config.ValidateModelProviders` and mounted per repository — but it *"governs model providers and the OAuth accounts and project secrets that ride with them — nothing else."* `voice.md` line 151 puts the question exactly: *"there is no `allow_data_to`-shaped answer to 'may this repository's conversation go to Deepgram'"*, because the unit is wrong twice. `voice.md` **F23** records the consequence: voice's STT/TTS providers are not model providers, so no existing key covers them.

**Consequence.** The spec's *Keys and security* section assumes a governance layer for provider egress that does not exist, and ⊂ PR #633 §4.1/§4.3 designs the nearest thing (a **named** allow-list with a register of retention and training terms, *"It records a reading, and it does not verify it"*). **That register is the instrument §6 exercises.** §5 Q3 asks where the allow-list lives; Q11 asks whether the summariser's own egress is governed by it or by `allow_data_to`.

**#27 — Redaction exists, is ported, and is applied before the MAC — but it is best-effort. (Exists / Rule)**

`internal/agent/redact.go` provides `Redactor` (an `io.Writer` that replaces secret values with `[REDACTED]` line by line), `NewRedactor`, `Redact(s, secrets)` and `RedactFunc(secrets)`, covering raw, JSON-escaped, per-line (PEM) and encoded forms (base64 std/raw/url, percent-encoding, wrapped lines). It is **ported to JavaScript** as `shared/digest/redact.js` (`REDACTED`, `redact`, `redactFunc`, `redactForms`, *"ported from `internal/agent/redact.go`"*). The mobile mailbox applies it **before** the tag: ⊂ PR #604's `mobile/src/mailbox/redact.js` wraps `redactFunc` with the rule *"Redaction happens before the tag, not after"*, and `shared/mailbox-schema`'s README describes `text` as *"≤4096 cu, **already redacted**"*. The honest limit is written into the Go source: *"Anything published from agent output is best-effort redacted, not guaranteed clean."* `internal/safetext` is a **different** mechanism (neutralising hostile strings for rendering, not credential redaction).

**Consequence.** "Summarising spoken lines must never include secrets" has a mechanism to reuse and an interface to reuse it through (`RedactFunc` / `redactFunc`), and it must be applied **to the text that leaves**, at the boundary, before the model sees it — not to the audio and not to the transcript only. The spec says nothing about this; §4 makes it a resolution.

### (e) The security service, and the temporary-token story

**#28 — The "per-project security service" is plan-only, and its scope is jobs, not clients. (Wrong / Exists)**

The spec says provider keys are stored *"at project level in the per-project security service"*. What exists is [docs/credentials-service/](../credentials-service/): README's status line *"**plan only; nothing is implemented**"*; a spec for a **standalone Cloud Run credential proxy** per project that is *"the only identity with Secret Manager access to the OAuth tokens and API keys"*; and an implementation plan in slices, of which the owner approved **C0 only, on 2026-10-18** (⊂ PR #584, whose Slice 0 is *"docs-only work, and one piece of rules groundwork that needs no rules change and no run-token grant"*).

Two of its decisions close the spec's reading. **Its scope is jobs**: ⊂ PR #633 §4.5 quotes the service's own boundary — *"the service stays secrets-only. Other always-on features (**e.g. the phone client backend**) go in sibling services that call it"* — and concludes *"Its own scope is jobs."* And **D147** is opt-in per installation, with **D143** refusing a fallback: *"if the service is unreachable the run halts like `budget_unavailable` (M9 §12), **never falls back to an in-job key**."* Nothing in D130–D154 discusses storing or minting an STT/TTS provider credential.

**Consequence.** The spec's *Keys and security* section names a component for a job the component does not have: a **voice-provider key store**. Building one is a **new scope** for either the credentials service or a sibling, and the P5.3 design deliberately refused to assume it (#32).

**#29 — What exists today for provider keys, and what "project level" currently means. (Exists)**

- **Secret Manager, two scopes.** `internal/cli/secrets.go` implements `fugaro secrets` with `--project-wide` (*"stores or lists the secret once per Fugaro project rather than per repository"*, `docs/design/project-secrets.md` §4), with `gcp.SecretID(slug, logical)` and `gcp.ProjectSecretID(fugaroProject, logical)` (`internal/backend/gcp/names.go`) deriving every ID, and the reader rule *"Only a job's own service account, granted `secretAccessor` **per secret, per job**"*.
- **Three credential tiers.** [credentials-service/spec.md](../credentials-service/spec.md): **A** project-level named Claude OAuth tokens, **B** a direct Anthropic API key, **C** an OpenRouter key. **D130**/**D131** keep A, B and C where they are and make the service *"the only identity with Secret Manager access"*.
- **Named OAuth tokens** ([named-oauth-tokens.md](../design/named-oauth-tokens.md), shipped in 0.6.5): `config.ReservedSecrets["claude-oauth-token"] = "CLAUDE_CODE_OAUTH_TOKEN"` — the name is not configurable — mounted per repository.
- **The in-runner gateway**: `internal/gateway` is *"a loopback, in-runner proxy that swaps a short-lived token for the real key"* (`Server.Token()`; `agent.GatewayVars` gives the agent *"never the real model credential"*).

**Consequence.** "Project level" is a **real and implemented** scope (`--project-wide`), so that half of the spec's sentence is right. The half that is wrong is *where* it lives: today it is Secret Manager reached by a job's own service account, not a "security service".

**#30 — The spec's per-session token minting does not exist, and the tree has three other minting mechanisms. (Gap / Exists)**

Nothing mints a Deepgram, AssemblyAI or Cartesia token. What the tree *does* mint, and these are the precedents a new minter would be modelled on:

- **The per-run budget token**: `internal/budget/token/mint.go`'s `Mint`/`MintAt` sign a Firebase custom token through IAM Credentials `signJwt` as a service account that holds no roles, with claims `fs`/`fr`/`fx`/`fp`/`rb`; `exchange.go`'s `Exchange` trades it for an ID token (*"minted more than an hour ago; launch the run again"*); `object.go`'s `PutObject` at `ObjectKey(slug, run)` and `token.TakeObject`, which *"deletes as it reads, so a token can be used once"*.
- **The gateway's per-run token**: `internal/gateway/gateway.go`'s `Server.Token()`.
- **GitHub App installation tokens**: `github.MintInstallationToken`, called from `internal/cli/gitcred.go` and `imagecheck.go`.

And one refusal worth reading first: `docs/design/m9-budget-and-dashboard.md` records that mint-on-start was considered and rejected, because making it safe *"needs **a minting endpoint that checks the caller's ID token and execution**, which is exactly the budget service D1 rejected."*

**Consequence.** The spec's minting idea is **architecturally familiar here and concretely absent**, and its safety hinges on the one thing the tree refused once already: a minting endpoint that authenticates a caller. The credentials-service work gives that caller a shape for **jobs** (**D134**: the run's existing budget identity; **D135**: reached through the in-runner gateway with a Cloud Run IAM invoker token for the service) — but **not for a laptop or a phone**. §5 Q12 asks which identity a voice client would mint as; it is the question D134/D135 deliberately did not answer.

**#31 — The provider temporary tokens are real, and they are materially weaker per-session credentials than the spec implies. (Verified — see §6)**

All three providers support what the spec claims, and the details change the design:

- **Deepgram** — `POST /v1/auth/grant` returns a JWT with a **30-second default TTL** (`ttl_seconds` configurable), scope `usage::write` *"for core voice APIs"*, *"requiring an API key with Member or higher authorization"*, and *"Tokens created with this endpoint will not work with the Manage APIs."* Their own guide explains the pattern the spec wants: *"Direct access to the Deepgram API is particularly useful for client-side/untrusted applications that are more latency-sensitive"*, and instructs: *"Create a backend service to generate temporary tokens … Use tokens immediately after generation."*
- **AssemblyAI** — `GET /v3/token` takes **two** separate parameters whose distinction the spec's "per session" elides: `expires_in_seconds` (1–600) is the *"Token redemption window … the time the client has to use this token to open a WebSocket"*, and `max_session_duration_seconds` (60–10800, default 10800) *"does NOT cap the duration of the resulting streaming session"* — that is what does. And it is **not single-use**: *"until this window elapses, the same token can open multiple sessions, including concurrent sessions."*
- **Cartesia** — `POST /access-token` with granular **grants** (`tts`, `stt`, `agent`), each mapping to named endpoints, and `expires_in` **capped at one hour** (*"The maximum is 1 hour (3600 seconds)"*), generated server-side from an API key, with the client passing it as a query parameter on the browser WebSocket.

**Consequence.** Four things the spec's *Keys and security* section should say and does not: a token's **TTL is short (30 s to 1 h), not session-length**, so a long conversation re-mints and the client must handle the exchange mid-session; AssemblyAI's token is **reusable within its window**, so "one session" is a property of the *session duration parameter*, not of the token; the **scope is per-capability**, so the minter must grant `tts` and not `stt` to a client that only speaks; and **minting requires a powerful credential** (Deepgram demands Member or higher), which is precisely the long-lived key the spec says must never be on a device — so the minter is a service that *holds* it. §5 Q12.

**#32 — The interim the brief asks about is not free, and the tree has already refused its closest shape once. (Rule)**

The brief asks "what happens with **no** security service yet: an interim: a Secret Manager secret and a small token-minting function". The tree's record:

- **D130**'s alternative column names an interim and the owner **did not take it**: *"**Keep B as the interim** (token in the reviewer stage of the job) until the service exists"*.
- **D131**'s answer keeps a refusal in place: *"Task 3.4a's refusal stays until the service exists."*
- `docs/design/project-named-oauth-tokens.md` (⊂ PR #584, and on neither that branch's base nor `main`) calls the current carrier exactly that: *"**The carrier is a stopgap** … *because it is temporary*: it will be replaced by the planned **security service**."*
- `docs/design/m9-budget-and-dashboard.md` §592 records that a naive minting endpoint was **considered and rejected**.

**Consequence.** A small minting function is not a neutral placeholder: it holds a **Member-or-higher provider key** in a new service, is **always-on** (a Deepgram token lasts 30 seconds, so the function must be reachable at every connect), and needs the caller authentication D134/D135 defined for jobs and not for clients. §5 Q12 offers the thinner interim — **no cloud voice at all until the decision is taken** — as the recommended option, because it is the option that adds no credential.

### (f) Engines and licences

**#33 — Apple SpeechAnalyzer requires OS 26, which sets a platform floor the console does not have. (Verified)**

Apple's own documentation for `SpeechAnalyzer` (the framework's `Speech` module) declares availability **iOS 26.0+, iPadOS 26.0+, macCatalyst 26.0+, macOS 26.0+, tvOS 26.0+, visionOS 26.0+**. The supporting classes are `SpeechTranscriber` (the module that *"performs speech-to-text transcriptions"*) and `AssetInventory` (*"ensures that the system has the assets necessary to support those classes"*).

**Consequence.** Three consequences the spec does not state: the spec's "OS 26" is right, and it is a **floor**, so `fugaro` on an older Mac falls to whisper.cpp (which the spec anticipates: "also shipped on Mac as a baseline"); a **helper process inherits the floor** — a Swift helper using SpeechAnalyzer cannot run on macOS 25 at all, so the console gains a second, *higher* minimum than the app; and `AssetInventory` means **the first use downloads assets**, which is a first-run network event in a helper, not in the renderer, and belongs in whatever states the module shows a person.

**#34 — The open-source engine licences are permissive — except the one the brief suspected. (Verified / Rule)**

| Engine | Code licence (verified) | Model/weights licence (verified) | Note |
|---|---|---|---|
| whisper.cpp | **MIT** | n/a (weights are OpenAI's Whisper, downloaded separately) | — |
| Kokoro | **Apache-2.0** | **Apache-2.0** — the model card says *"With Apache-licensed weights, Kokoro can be deployed anywhere"* | 82 million parameters |
| Silero VAD | **MIT** | — | repo licence read from the API |
| openWakeWord | **Apache-2.0** | **CC-BY-NC-SA 4.0** — non-commercial | see below |
| Porcupine | **Apache-2.0** (the SDK) | not a model the project ships | needs a Picovoice AccessKey; see #35 |

**openWakeWord is the trap the brief warned about, and it is confirmed verbatim in the project's own README:** *"All of the code in this repository is licensed under the **Apache 2.0** license. All of the included pre-trained models are licensed under the [Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International] license due to the inclusion of datasets with unknown or restrictive licensing as part of the training data. If you are interested in pre-trained models with more permissive licensing, please raise an issue and we will try to add them to a future release."*

**Consequence for an MIT tool.** Fugaro's `LICENSE` is **MIT** (`Copyright (c) 2026 Dimi Paun`), and it ships under that in its release archives (`.goreleaser.yaml` bundles `LICENSE` and `README.md`). Shipping an openWakeWord **pre-trained** model would put a non-commercial-licensed artifact in a permissively licensed product — and, separately, **the pre-trained set contains no "Fugaro" model anyway** (the shipped set is `alexa`, `hey mycroft`, `hey jarvis`, `hey rhasspy`, `current weather`, `timers`), so the project would have to **train its own** from synthetic speech on the frozen feature backbone (which is a Google TFHub model under Apache-2.0, re-implemented). A model the project trains and owns is a different object from the shipped set — so the route exists, and it is a **training project**, not an integration. §5 Q5.

**#35 — Porcupine needs a per-developer AccessKey and a vendor-trained keyword. (Rule / UNVERIFIED)**

Picovoice's own documentation states the requirement without a tier attached: *"Anyone who is using Picovoice needs to have a valid AccessKey. AccessKey is your authentication and authorization token for using Picovoice. It also verifies that your usage is within the limits of your account. **You must keep your AccessKey secret!**"* A custom wake word (`Fugaro`) requires a `.ppn` keyword file *"generated by Picovoice Console"*, and the SDK README states that additional languages and some limits *"available for commercial customers on a case-by-case basis"*.

**UNVERIFIED:** Picovoice's **plan tiers, usage limits and commercial terms** could not be read — `picovoice.ai/pricing/` serves a JavaScript-only page (1.2 kB of HTML, no readable terms) and the docs pages carry no tier table. **The free plan's commercial-use status is therefore UNVERIFIED and must not be assumed either way** (§7).

**Consequence.** Even on the permissive reading, the shape is: **a secret AccessKey per developer** and a **keyword file trained by a vendor's service** — in a repository that ships no keys, mounts no credential into a client, and whose console *"never reads a database, a config file or a credential itself"*. §5 Q5.

**#36 — There is no dependency-licence policy to hang any of this on. (Gap)**

There is no licence allow-list, no deny-list, no `THIRD_PARTY_NOTICES`, no `go-licenses`/`license-checker`/FOSSA step, and no policy in `CONTRIBUTING.md` (checked: no hit for licence/third-party/dependency/SBOM/vendored). The repository produces a **per-archive SBOM** in the release (`.goreleaser.yaml`: *"One SBOM per archive; needs the syft binary on PATH"*) and signs `checksums.txt` with cosign — artifacts, not gates. No files ending in `.onnx`, `.gguf`, `.tflite`, `.wasm`, `.so`, `.dylib` or `.a` are tracked anywhere, and there is no `models/` directory: the tree **ships no binary blob** except image assets under `brand/` and `website/public/`.

**Consequence.** The spec's engines and models would be the **first third-party binaries or weights this project ships**, and there is nothing to check them against. §5 Q5's answer should create the policy rather than assume one.

### (g) Barge-in, echo cancellation, and where the audio graph lives

**#37 — Barge-in is already specified with a number, and echo cancellation is already known to be greenfield. (Exists)**

`voice.md`'s acceptance criterion 5: *"Speaking over the assistant stops playback within ~300 ms."* `voice-spec.md` §5: *"Echo cancellation so the assistant's own speech isn't transcribed"* and *"Barge-in: user speech during TTS stops playback immediately and starts a new turn."* The plan's **V5.2** is the task that must either implement it or be honest: *"echo cancellation, or an honest statement that there is none … if there is none, **suppresses capture while playing and says so**."* ⊂ PR #592's `AudioIn` already carries a `barge-in` event kind. **Check 86** reserves the owner-run measurement.

**Consequence.** The spec's *Barge-in* section is a **restatement plus one new requirement** — "report how much of the reply was actually heard and pass that to the agent" — which is new: nothing in the tree records an interrupted utterance's extent beyond "interrupted". ⊂ PR #592's `TextToSpeech.speak` resolves either `{ ok: true, spoken: true }` or `interrupted`, with no measure. §4 makes "the extent is recorded" a resolution.

**#38 — The spec's echo-cancellation design is right, and it collides with both D43 and the CSP. (Rule)**

The spec's rule — *"TTS must play through the same audio pipeline the canceller monitors (not a separate native player)"* — is correct echo-cancellation engineering, and in this tree it is a **cross-process** problem. `getUserMedia({ audio: { echoCancellation: true } })` is a renderer API; **D43 puts capture outside the renderer** (#15); and the renderer's CSP has `connect-src 'none'` and no `media-src` (#14). So "the same pipeline" cannot be the renderer's Web Audio graph unless the renderer gains both capture and network — which is the one-window amendment the owner refused.

**Consequence.** The spec's architecture has a **split-brain**: the canceller must see both the microphone and the loudspeaker, and this tree's decisions put the microphone in a helper and (for Kokoro, #39) the synthesis wherever it lands. The workable shapes are all consequences of §5 Q2: **(i)** both capture and playback in one helper process, with the renderer out of the audio path entirely — the only shape that keeps `connect-src 'none'`; **(ii)** an OS-level AEC the helper can use, which is exactly the spec's own Apple/Android sentence, leaving the renderer out; or **(iii)** audio in the renderer with both loosened, refused by D43.

**#39 — Kokoro in the renderer needs `'wasm-unsafe-eval'`, which the CSP does not have. (Rule)**

A WASM/ONNX Kokoro runs in the renderer only if the CSP allows WebAssembly compilation: `script-src 'self'` does not, and `'wasm-unsafe-eval'` is absent — as the console's own code notes twice (`bundle.test.ts`: *"the CSP has no `'unsafe-eval'`"*; `tools/bundle-renderer/index.ts` the same). Adding it is a **second, independent loosening** from #14's, bought for a different reason.

**Consequence.** Kokoro most naturally runs **in the helper**, next to the capture and the provider socket, which also resolves #38's split-brain — at the cost of model weights (82 M parameters; "tens to hundreds of MB" in the spec's own words) becoming a **bundled or downloaded asset of a nested binary** (#17, #36, #42).

**#40 — What the phone can do about AEC is an OS audio-session question the tree has already parked. (Open)**

The phone's audio stack is ⊂ PR #592's `AudioIn` — `start`, `stop`, `running`, `on`, with `MICROPHONE_DENIED` and `BACKGROUND_AUDIO_DENIED` refusal codes — over whatever Expo/React Native provides. The mobile work's answer to the platform half is **D122** (OS-level hands-free audio) plus **Check 86**, and `mobile/reconciliation.md`'s **U7** records the honest state: *"Whether the OS grants background audio with barge-in on both platforms | No device was tested."*

**Consequence.** The spec's Android `voice-communication` AEC and Apple voice-processing mode are **plausible and unmeasured here**. Nothing in this document should be read as asserting they work on the app's minimum OS; §7 records it as UNVERIFIED.

### (h) Logging, metrics, and the phone's remaining items

**#41 — Nothing logs voice latency, CPU or battery, and transcripts must not enter a log. (Gap / Rule)**

There is no metrics sink for the module. What exists to constrain one: the console's spawn log rule (**D20**: the app's own argv only, capped, *"never read back by the app, never rotated, never deleted, never transmitted"*); the redaction machinery (#27); and the mobile work's **U3**-class honesty about unmeasured numbers. `voice.md`'s acceptance criteria are explicitly **owner-run** (Check 64, step 3), because *"the real device latency is owner-run"*.

**Consequence.** "Log latency to first partial, latency to final, and CPU/battery load" is buildable and must be built **locally and aggregate-only**: counts, durations and percentiles are safe; a first-partial log line that quotes the partial is a transcript in a file, which is what #22 and #27 exist to prevent. §4 makes this a resolution; it is not a question.

**#42 — The phone items, mapped onto the decisions that already cover them. (Exists / Gap)**

| Spec item | Where it already lives | Verdict |
|---|---|---|
| Audio routing: speaker, AirPods, car Bluetooth; headset mics degrade transcription | mobile **D122**; Check 86 | covered by P3.5's scope; the *degradation* claim is **UNVERIFIED** here (no device tested) |
| Interruptions: calls, Siri, another app taking the audio session | **D122**; ⊂ PR #592's `BACKGROUND_AUDIO_DENIED` | covered |
| Keep the screen awake on the voice screen | **nothing anywhere** — no hit in `docs/mobile/`, `shared/`, or the plans | **Gap** |
| Network handoffs; reconnect cloud streams or fall back to local mid-conversation | ⊂ PR #633 §4.6, verbatim: *"Every one of these is fail-closed to local"*; *"the network is down | local only; nothing is queued for later"* | covered, and stricter than the spec — the design **refuses** to reconnect a cloud stream rather than risking a queued recording |
| Model size; download on demand rather than bundling | ⊂ PR #633 §8's A2 (TTS-out only if Check 85 fails); the app's release path is **D120**/EAS, and Check 89 is unwritten | **Gap**, with a named owner (release path) |

**Consequence.** Of the spec's five phone bullets, three are already designed, one has no home at all (the wake lock), and one is a release-path decision. §5 Q4 covers the foreground/background tension; the wake lock needs no decision, only a task, so §4 records it as a resolution.

### (i) The confirmation boundary

**#43 — The confirmation machine exists on `main`, and this module must never be a second path to it. (Exists / Rule)**

`shared/confirm` is merged. Its rules, verbatim from its README: *"A confirmation must be a **separate utterance** recognised as the **fixed phrase**; it has a **timeout**; **one confirmation authorises exactly one action**, which is … **the utterance that issued a command can never confirm it**."* Its constants: `DESTRUCTIVE_VERBS = ['cancel', 'confirm', 'merge']`, `PASS_THROUGH_VERBS = ['launch']` (D162: a launch is **not** confirmed on the phone), `REFUSED_VERBS` — a closed list including `budget`, `budget-kill`, `kill-switch`, `delete`, `revoke`, `purge` — `CONFIRM_PHRASES = CONFIRMATION_PHRASES` (imported from P6.1, *"so the router and this module cannot disagree"*), `DEFAULT_CONFIRM_TIMEOUT_MS = 60 * 1000` (bounds 1 s to 5 min), and `CONFIRM_CUE = 'Say "Fugaro, confirm" to confirm.'` The phrase set is deliberately **not** `yes`: *"'yes', 'sure', 'ok', 'yeah', 'confirmed' and 'confirmed it' are deliberately not in it."*

**Consequence.** This is the answer to the brief's item (k), and it is a **Rule with no options**: a voice interface that summarises events **speaks**; it never recognises a confirmation. A spoken "yes" confirms nothing here, and a module that accepted one would be a second path around `shared/confirm`'s four properties (separate utterance, fixed phrase, timeout, one-action-per-confirmation). This module's own barge-in and interrupt paths must also never *appear* to confirm — see §4's resolution on the wake word and the cue.

**#44 — Check numbering: what is written, what is held, and what a new check would take. (Exists / Rule)**

The register `docs/gcp-live-checklist.md` states the discipline: *"a reserved number is never reused or renumbered."* For this work: **64** (console voice, owner-run, written by V8.2 — **not run**), **65** (signed-build microphone check, held), **66** (the cloud-provider check, *"that can exist only if the owner takes the second egress decision"*, held), **85** (the on-device STT/TTS bake-off — *"not run, and its text is not in the register"*, written by P4.2), **86** (background audio and barge-in), **102** (wake-word recognition rate, proposed, Phase 6 not approved), **103** (a spoken cancel and merge on the sandbox repository). Checks 90–99 belong to the credentials service Slice 0 (⊂ PR #584, written and not run); ⊂ PR #633 names **110** as the next free number.

**Consequence.** A **console** voice bake-off has no number, and D44's "test both" cannot be recorded against 66 without the egress decision (66 is held *for* that decision) or 85 (85 is on-device, phone). This document **allocates nothing** — §5 Q13 asks whether the console's multi-engine comparison joins 66, joins 85, or takes a new number, because the owner owns the register.

## 4. Resolutions

Findings with one defensible answer. They need no decision from the owner; they need to be written into the design, and each already has a citation that makes it true.

**R1 — The module's interfaces are the ports that exist, not new ones.** Build on `shared/command` (routing), `shared/confirm` (confirmations), `shared/digest` (severity + redaction) and, for text in, `shared/mailbox`/`shared/mailbox-schema`; ⊂ PR #592's three ports are the audio-side surface. Where a port is missing (`AudioOut`, a VAD, a metrics sink), add it **to that set** and not beside it. (#2, #21, #23, #41)

**R2 — The classifier is `shared/digest`'s ladder, extended by one rung.** `SEVERITY` (`kill`, `failed`, `fast_burn`, `info`) already exists and already carries two urgent reasons the spec does not name (`fast_burn`, `offline`). The spec's "mention" rung is the single addition; the spec's "silent" tier is the ladder's default and needs no code. (#9, #12)

**R3 — The interrupt switch is `shared/prompts`' `SWITCH`, not a new toggle.** Three positions, local-only, never written to the database, with `INTERRUPT.PROMPT` covering the spec's "agent asking a question" the moment a producer exists. (#12, #25)

**R4 — Redaction happens at the boundary, before the model and before the speaker.** Apply `RedactFunc`/`redactFunc` to every string that leaves — the summariser's input, the spoken line, any log line — and never to the audio or only to the transcript. Reuse the ported `shared/digest/redact.js`; do not write a second redactor. (#27)

**R5 — Metrics are local, aggregate-only and textless.** Counts, durations, percentiles and battery readings may be logged; a partial transcript, a summary, a title or a spawn argv beyond D20's four fields may not. Owner-run, on the operator's own machine, like Check 64's step 3. (#41)

**R6 — The module speaks and never acts.** Nothing in the spec's sections proposes an action, and the tree's fence (D2, v2 D22, D14) grants exactly one write to the console under the CLI's own pre-check. A talking module touches none of it; an acting one goes through `shared/confirm` and nothing else. This is the module's answer to item (k). (#18, #43)

**R7 — The wake lock is a task, not a decision.** "Keep the screen awake on the voice screen" has no home in the mobile work; it belongs to P3.5's scope and needs no ruling. (#42)

**R8 — `docs/voice-module/` cites P5.3's seven points and does not restate them.** Conditions are read at `docs/mobile/design/cloud-stt-tts.md`; this folder owns what the module does. (§1)

**R9 — The spec's vendor list is a candidate list for the bake-off, not a selection.** D123/D46 prescribe a measurement-first bake-off with a stated latency target; naming engines before Check 85 reports is the thing D123 exists to prevent. (#20)

**R10 — The vetted replacement for `speak(text)`'s "interrupted" is an extent.** ⊂ PR #592's `TextToSpeech.speak` resolves `{ ok: true, spoken: true }` or `interrupted`; the spec's "record how much of the reply was actually heard and pass that to the agent" needs a third field on that resolve (or its mirror in the console), and the interruption path must set it. (#37)

**R11 — No binary blob enters the tree without a licence policy.** The tree tracks no `.onnx`/`.gguf`/`.tflite`/`.wasm` and has no licence gate (#36). Whichever engines survive §5 Q5, the commit that adds the first weights adds the policy with them. (#36)

**R12 — Four of the spec's claims are corrected by the providers' own words**, and the corrections are in §6: Deepgram's token is 30 seconds by default; AssemblyAI's is a redemption window plus a separate session cap and is **not** single-use; Cartesia's is capped at one hour; and all three require a **powerful credential** to mint. The spec's *Keys and security* section should carry these four corrections. (#31)

## 5. Open for the owner

Each question has 2–4 options, with the recommendation first and why, then its consequences, then the part of the spec it changes. **No D-numbers are allocated here**: this repository's decision numbers are an allocation the owner makes (§1 of mobile/README.md's numbering rule). **None of these is answered by this document, and none is a plan.**

### Q1 — Does this spec take the second explicit egress decision? *This is the gate.*

`voice.md` **D44** left cloud STT/TTS to *"a second, explicit owner decision"*; ⊂ PR #633's **Q1** asks it and recommends against, until Check 85 measures the local engines. The 2026-10-18 spec writes cloud engines into the design without recording a decision, terms or a retention reading.

- **(1) No — the spec does not take it, and Q1 stays open; cloud voice stays unbuilt until the owner takes it explicitly, with P5.3's seven points as its conditions.** *(Recommended.)* It is the only option that keeps D44's default-deny intact, keeps ⊂ PR #633's register the place the terms were read, and lets Check 85 (or a console comparison) come first — which is the sequence D123/D46 prescribe. **Consequence:** the spec's cloud rows are read as *candidates*, R9 applies, and the engine toggle ships as the local/refused pair ⊂ PR #633 §4.4 already designs. **Changes:** *Speech-to-text*, *Text-to-speech*, *Keys and security*, *Engine toggle* — the whole cloud half.
- **(2) Yes, for TTS only (Cartesia), because a voice that cannot speak is not a voice.** ⊂ PR #633 §8's A2 is exactly this shape if Check 85 fails the bar: synthesis out, recognition in-device. **Consequence:** one provider credential, one grant (`tts` only, per #31), and the confirmation path stays entirely local — which is what makes this the least-bad cloud option. **Changes:** *Text-to-speech*, *Keys and security*; leaves recognition local.
- **(3) Yes, both directions.** **Consequence:** microphone audio leaves the machine; #31's four corrections all become live (30-second re-minting, a reusable AssemblyAI token, per-capability grants, a Member-or-higher key in a new service); and ⊂ PR #633 §5's rules 1–6 must hold, including *"nothing at all while a confirmation is pending."* **Changes:** all four cloud-adjacent sections.

### Q2 — Where do microphone frames and the echo canceller live, given D43 and `connect-src 'none'`?

The spec's `echoCancellation` design needs the canceller to see the microphone and the loudspeaker; D43 puts capture outside the renderer (#15), and the renderer's CSP is `connect-src 'none'` with no `media-src` and no `wasm-unsafe-eval` (#14, #39).

- **(1) Both capture and playback in the helper process; the renderer is not in the audio path at all.** *(Recommended.)* It is the only shape that changes **nothing** in the CSP, keeps D43's ruling and §5.1 rule 9 unmoved, makes the canceller trivially "the same pipeline" (the spec's own requirement), and is where a provider socket can live without loosening `connect-src`. **Consequence:** the spec's "Chromium WebRTC `echoCancellation` in Electron" is **not** what gets built — the helper uses the OS's AEC instead (#38); the voice screen becomes a **display** of state that the helper owns, which is also what ⊂ PR #633 §4.4's non-dismissible indicator wants. **Changes:** *Barge-in*'s Electron sentence, and *Architecture*'s "in the Electron console".
- **(2) Add the named provider origins to `connect-src` and run capture in the renderer.** **Consequence:** two CSP loosenings in the hardened window (provider origins, and `'wasm-unsafe-eval'` if Kokoro runs there), a reversal of D43 that the owner explicitly refused on 2026-10-16, and the console's own frozen CSP tests must be rewritten to permit it. **Changes:** *Barge-in*, *Keys and security*, and the console's security posture.
- **(3) Keep capture in a helper but let the provider socket live in the renderer.** **Consequence:** the canceller still works, but the CSP is loosened anyway, so it buys nothing over (1) and splits the credential surface. **Changes:** *Keys and security* only.

### Q3 — What is the relationship between the allow-list, the toggle, and the indicator?

⊂ PR #633 already designs three objects (#8); the spec adds a person-operable icon that can select cloud.

- **(1) The allow-list decides; the toggle may only narrow; the indicator is always visible and must not be dismissible.** *(Recommended.)* It preserves ⊂ PR #633's §4.1 (default-deny, admitted by name), §4.4 (three states including *"cloud requested but refused"*) and §4.6 (fail-closed to local) as the conditions, and makes the spec's icon an honest *request* rather than a permission. **Consequence:** the icon's cloud state can be refused, and the spec's "three-way on Mac" becomes "the local engine the allow-list names, or local-only" — the third arm needs a way to *name a local engine*, which the allow-list's shape does not currently express. **Changes:** *Engine toggle and experiments*; confirms P5.3 Q2 as still open.
- **(2) No toggle: the allow-list alone decides, and the person cannot choose cloud.** **Consequence:** simplest and safest; loses the spec's explicit cloud-on-demand use ("Decide on accuracy and latency" is then an owner-run test, not a user choice). **Changes:** remove the toggle bullet.
- **(3) The toggle *is* the allow-list's only control (a switch, not a list).** **Consequence:** a per-session person-choice is a weaker instrument than a named list with recorded terms, and it has no way to express "this provider, not that one". **Changes:** *Engine toggle* and *Keys and security*.

### Q4 — "Foreground only" or the background audio mode Check 86 measures?

- **(1) Foreground only, as the spec says; Check 86 measures the app's *audio session* behaviour while the app is open, and its "background mode" item is dropped or reworded.** *(Recommended.)* It is what the spec's *Goal* says twice, it removes a whole class of privacy and battery risk, and it makes Check 86's question smaller and answerable. **Consequence:** Check 86 must be edited when it is written (it is unwritten), and the mobile work loses the "keep talking while the phone is in your pocket" case — call it out explicitly rather than by accident. **Changes:** *Goal* — it becomes a stated limitation.
- **(2) Background audio allowed while a voice session is open, as Check 86 assumes.** **Consequence:** contradicts the spec's sentence, needs a mode entitlement on both platforms, and reopens `mobile/reconciliation.md`'s U7. **Changes:** *Goal* and *Phone-specific considerations*.

### Q5 — Which wake word, given that one already exists and the acoustic ones have licence strings attached?

- **(1) Keep the transcript prefix test (`shared/command`'s `WAKE_ALIASES`) as the only wake word; do not add an acoustic engine.** *(Recommended.)* It is merged, tested, pinned by P6.1's verification (including *"a test pins that 'yes' is not an alias of 'confirm'"*), needs no model, no AccessKey and no licence policy, and adds no audio-processing path before capture — which is what #21 says does not exist. **Consequence:** the spec's openWakeWord/Porcupine bullet is dropped, one planned live check (102, recognition rate) keeps its meaning, and the module gains no false-accept surface. **Changes:** *Speech-to-text*'s last bullet.
- **(2) Add openWakeWord, training a project-owned "Fugaro" model from synthetic speech.** **Consequence:** avoids the CC-BY-NC-SA trap (#34) but buys a training project, a new binary/weights surface (#36) and a *second* wake path whose false accepts are independent of the alias set's — two ways to wake a device that must agree about what "confirm" means. **Changes:** *Speech-to-text*; adds a licence policy (R11).
- **(3) Use Porcupine with a vendor-trained keyword.** **Consequence:** a per-developer AccessKey (#35) in a tool that ships none, vendor terms whose tiers are UNVERIFIED (§7), a `.ppn` the project cannot regenerate offline, and the same double-wake risk as (2). **Changes:** *Speech-to-text*; and a credential story the repository does not have (#28).

### Q6 — One helper process or two?

⊂ **PR #592** and D40 give the console a supervised helper for the Agent SDK; the spec wants a native Swift helper for audio.

- **(1) One helper, extended; the Agent SDK and the audio live in the same supervised process.** *(Recommended.)* It keeps the console's child count at one (#5), keeps the entitlement story to a single nested binary (#16), and is the only shape in which the *same* process can see the transcript and the spoken line — which R4 (redaction at the boundary) and R10 (the heard-extent) both need. **Consequence:** the helper becomes the app's largest security surface, so its permission-callback rules (D54) extend to the audio path, and its failure mode is now "the conversation is gone", not "the summary is gone". **Changes:** *Speech-to-text*'s "via a small native Swift helper process from Electron"; tightens what D40 must be.
- **(2) Two helpers — one for the SDK (D40), one for audio.** **Consequence:** the audio helper can be signed, entitled and notarised with only what audio needs, but the console now supervises two children, the audio helper needs the provider socket and the extent/redaction boundary crosses a process, and the packaging work doubles. **Changes:** as (1), plus the console's process model.
- **(3) The Swift helper for Apple only, and whisper.cpp in the existing `fugaro` process for everything else.** **Consequence:** narrows Apple's role to the Mac and removes the nested-binary problem for Linux/Windows — but leaves the *first* scan requiring macOS 26 (#33) in the helper anyway, and mixes a capture path into the CLI's process. **Changes:** *Speech-to-text*.

### Q7 — Should the summariser's spend have an app-side cap, given D45 says nothing bounds it?

- **(1) Keep D45 as it is — the operator's own login, disclosed, unbounded — and make the app show a running count of the summaries it has asked for.** *(Recommended.)* D45 is an answered decision; the honest fix for "uncapped" is **visibility**, not a new mechanism, and a count is a metric (R5) the module already needs. **Consequence:** the spec's *Keys and security* section must say plainly that the summariser is not a Fugaro-managed credential and is not in `fugaro budget show`. **Changes:** *Filtering and summarising* §3, *Keys and security*.
- **(2) Add a local per-session cap the app enforces (N summaries, then on-demand only).** **Consequence:** bounded cost, but a new local policy, a new failure mode mid-conversation, and a knob whose default nobody knows. **Changes:** *Filtering and summarising* §3.
- **(3) Move the summariser behind the credentials service.** **Consequence:** contradicted today — the service is plan-only and **jobs-scoped** (#28), and D143 refuses fallbacks; this would be a new scope for it. **Changes:** *Keys and security* — and the credentials-service documents.

### Q8 — The "record raw session audio" experiment, against D118 and F32.

- **(1) No recording: run the bake-off on synthesised clips (F32's existing rule) plus live owner-run comparisons of the same read script.** *(Recommended.)* It keeps D118's answer intact, needs no storage decision, and still gives the bake-off *comparable* inputs — a recorded script played aloud is comparable without the module storing anything. **Consequence:** an owner-run procedure (a fixed script, four engines, the same microphone and room) rather than an in-app recorder; the spec's "record raw session audio" bullet is rewritten. **Changes:** *Engine toggle and experiments*.
- **(2) A local-only, dev-only switch on the console that records to a scratch directory, never in the repo, never in a bucket, deleted at session end, never backed up, documented in the register.** **Consequence:** the most faithful measurement, and the first recording in a tree that forbids them — it needs a written decision, an owner-run check, an exclusion from every backup path, and a rule that the console's own processes cannot read it (the console "never reads a … credential itself" is a posture worth extending to audio). **Changes:** *Engine toggle and experiments*; adds a check.
- **(3) Record on the phone.** **Consequence:** directly contradicts D118's *"the app never stores or uploads audio"* and ⊂ PR #633 §4.6's *"a queued utterance is a recording"*. **Not recommended.**

### Q9 — Which document is authoritative, and what happens to voice.md's D39–D54b?

- **(1) This spec supersedes voice.md where it addresses the same question, and P5.3's seven points constrain both; the supersessions are recorded in voice.md keeping its original text visible (mobile D129's rule).** *(Recommended.)* It is the wording the brief recommends, it makes one document authoritative per question instead of two, and it uses the amendment convention this repository already applies to `voice.md` (D40, D42, D49 and D50 were amended that way on 2026-10-17). **Consequence:** a docs-only follow-up amends voice.md's cells for the engine list, the module's placement (D50, already amended once by D119) and the wake word; this folder changes nothing in `voice.md` today. **Changes:** the *whole* spec's status, from "a second opinion" to "authoritative for what is built". Also answers **P5.3 Q1's framing**: this spec is **not** the second egress decision (Q1).
- **(2) voice.md's D39–D54b stand and this spec is read as commentary on them.** **Consequence:** the vendor list and the wake word have no standing, the folder is documentation rather than design, and the owner must answer the same questions twice. **Changes:** nothing binding.
- **(3) Neither supersedes: the owner re-decides the contested rows fresh.** **Consequence:** cleanest for the two documents that now disagree twice (`AudioOut`/port count #2, wake word #7), at the cost of re-opening rows the owner already answered on 2026-10-16. **Changes:** voice.md's D44, D46, D47, D50, D52.

### Q10 — "Agent asking a question" needs a producer. Does the owner want one?

D48 dropped this trigger *"until something defines it"* (#25); ⊂ PR #602 now holds questions **inside a local queue** and nothing puts one in the feed.

- **(1) No new producer for now: speak a question only when the phone's prompt queue raises `INTERRUPT.PROMPT` (⊂ PR #602), and keep the console's feed unchanged.** *(Recommended.)* It uses the one producer that exists, keeps D127's local-only switch honest, and adds no field to a contract that thirteen documented facts already pin (`console/docs/json-contract.md` §10). **Consequence:** the console cannot speak "the agent is asking you something" at all — it must say so on screen, where the question actually lives. **Changes:** *Filtering and summarising* §1's "urgent" list.
- **(2) Add an awaiting-input state to the feed** (a new field on `JSONRun`, or a new `kind`), produced by whatever the bridge learns. **Consequence:** a new contract field, which means `console/docs/json-contract.md`, its pinning test (`TestDocsDesktopConsoleContractNamesEveryWatchField`) and the phone's feed reader (⊂ PR #590's document rules) all move together; and the producer still does not exist until D40/D54's `onAwaitingInput` is answered (U7). **Changes:** *Filtering and summarising* §1; the console's JSON contract.
- **(3) Speak the run store's `Status=halted` as if it were a question.** **Consequence:** it is not one — `HaltReason` is a budget fact (`run_cap`, `kill_switch`, …), and F3 exists to say so. **Not recommended.**

### Q11 — Is the summariser's egress governed by the voice allow-list or by `allow_data_to`?

- **(1) The voice allow-list of ⊂ PR #633, extended to name the summariser's model endpoint when it is not the operator's own login; `allow_data_to` keeps governing *runs* and is not stretched to cover voice.** *(Recommended.)* `allow_data_to`'s unit is `<owner>/<repo>` and its subject is model providers receiving **code** (voice.md F9); voice's subject is a **conversation**, and voice.md already recorded that no `allow_data_to`-shaped answer exists for it (#26). **Consequence:** the provider register (⊂ PR #633 §4.3) gains the summariser's endpoint as a row with terms and a date read; and the answer to "what data does it see" becomes a written sentence rather than an inference. **Changes:** *Filtering and summarising* §3, *Keys and security*.
- **(2) Stretch `allow_data_to` to cover the conversation, per repository.** **Consequence:** reuses an enforced key, but the unit is wrong twice (there is no repository for a laptop conversation), and it would change a validated config key's meaning in `internal/config/providers.go`. **Changes:** the config layer, plus the spec.
- **(3) No governance: the summariser is the operator's own session and inherits its terms.** **Consequence:** true today and defensible for v1, but it leaves the spec's privacy question unanswered for the cloud case, and it cannot be written as a rule. **Changes:** none.

### Q12 — With no security service, what is acceptable for a provider credential?

#32 records the tree's refusals; #31 records that minting needs a Member-or-higher key.

- **(1) Nothing: cloud voice stays unbuilt until a minter exists, and the minter is designed as a sibling service with a caller identity (D134/D135's shape for clients, which does not exist yet).** *(Recommended.)* It is the only option that adds no credential, holds no long-lived provider key anywhere new, and matches Q1's recommended answer — which makes it free if Q1 is answered "no" and necessary if it is answered "yes". **Consequence:** the cloud half of the spec is deferred behind a design that must be written, and ⊂ PR #633 Q3 stays open. **Changes:** *Keys and security* entirely.
- **(2) An interim: a Secret Manager secret plus a small token-minting function on Cloud Run, reached by an authenticated console/phone identity.** **Consequence:** it works, and it is a **new always-on service holding a powerful provider key** — a Deepgram token lives 30 seconds, so the function must be reachable at every connect; the caller identity must be invented (D134/D135 defined one for jobs, not clients); and M9 §592's recorded rejection of a minting endpoint applies. **Changes:** *Keys and security*; and a new Terraform/deploy surface, which is a task this document does not write.
- **(3) A provider key in the client's secure storage, on the phone only.** **Consequence:** the third exception to "never a key on the device", and ⊂ PR #633 §5 rule 8 names it as exactly that; it also contradicts the spec's own *"No long-lived keys on devices"*. **Not recommended.**

### Q13 — Where does the console's multi-engine comparison get recorded?

#44 records that 66 is held for the cloud decision, 85 is the on-device bake-off and 110 is ⊂ PR #633's next free number.

- **(1) The console's comparison is a **table in this folder's design**, and a live check is only written once the owner takes the egress decision — at which point it joins 66 if the decision covers both platforms, or takes 110 for the phone half.** *(Recommended.)* It allocates no number today, keeps *"a reserved number is never reused"* true, and separates the measurement (which can be owner-run immediately) from the egress decision (which cannot). **Consequence:** the spec's "test both … Decide on accuracy and latency" gets a procedure in this folder and no register entry until it needs one. **Changes:** *Speech-to-text*'s cloud bullet; adds a design artifact to this folder later, not now.
- **(2) The console's comparison joins Check 85.** **Consequence:** 85 is written by P4.2 as the phone's on-device bake-off; folding the console in makes one check span two platforms and two artifact trees, which is the shape the register avoids elsewhere. **Changes:** *Speech-to-text*; the mobile plan.
- **(3) Take 110 now, for the console.** **Consequence:** ⊂ PR #633 already names 110 for the phone's cloud half; two claims on one number is exactly what the register's rule forbids. **Not recommended.**

## 6. Verification: the providers' public documentation, 2026-10-19

Read-only, from the providers' own published documentation, with no credential and no live call. Where the spec's sentence and the provider's sentence differ, the provider's is quoted. **Prices are list prices read on 2026-10-19 and are promotional-sensitive.**

| The spec says | The provider documents | Verdict |
|---|---|---|
| Deepgram and AssemblyAI support short-lived temporary tokens | **Deepgram**: `POST /v1/auth/grant` → *"Generates a temporary JSON Web Token (JWT) with a 30-second (by default) TTL and usage::write permission for core voice APIs, requiring an API key with Member or higher authorization. Tokens created with this endpoint will not work with the Manage APIs."* Body `ttl_seconds` is optional. **AssemblyAI**: `GET /v3/token` → `expires_in_seconds` (1–600) is the *"Token redemption window"*; `max_session_duration_seconds` (60–10800, default 10800) is what caps the session, and *"This does NOT cap the duration of the resulting streaming session"*. **Cartesia**: `POST /access-token` with `grants` (`tts`/`stt`/`agent`) and `expires_in`, *"The maximum is 1 hour (3600 seconds)"* | **TRUE, with four corrections** (#31): short TTL, not session-length; AssemblyAI's is reusable within its window (*"not single-use … the same token can open multiple sessions, including concurrent sessions"*); grants are per-capability; minting needs a powerful key |
| "clients stream directly to providers" | **Deepgram** endorses exactly this: *"Direct access to the Deepgram API is particularly useful for client-side/untrusted applications that are more latency-sensitive"*, with *"Generate tokens only when needed (e.g., at connection start)"* and *"Use tokens immediately after generation"*. **Cartesia**: browser clients pass the access token as a **query parameter** on `wss://api.cartesia.ai/tts/websocket` | **TRUE**; and the pattern's cost is the minter service of #30/#32 |
| AssemblyAI "roughly $0.15–0.45/hr streaming" | AssemblyAI's pricing page: **Universal-Streaming** `$0.15/hr`, **Universal-Streaming Multilingual** `$0.15/hr`, and a realtime Pro model at **`$0.45/hr`** | **TRUE** — the range is the model spread, not a single model's |
| Deepgram "~$0.46/hr" | Deepgram's pricing page: **Nova-3 Monolingual streaming** regular price **`$0.0077/min`** (= **$0.462/hr**), currently discounted to `$0.0048/min`; **Flux English** (voice agents) regular `$0.0077/min`, current `$0.0065/min`; **Nova-3 Multilingual** regular `$0.0092/min` (=$0.552/hr) | **TRUE for the regular Nova-3 rate**; the page carries a *"Limited-time promotional rates on streaming"* notice, so $0.46 is the list price, not today's |
| "Deepgram offers $200 free credits" | The pricing page's own FAQ asks *"What is included in the $200 free credit?"* and the Free tier reads *"$200 Credit then pay-as-you-go"* | **TRUE** |
| "vs $50" (AssemblyAI) | AssemblyAI's page describes a **free tier in hours**, not dollars: *"up to 185 hours of pre-recorded transcription and up to 333 hours of streaming transcription"* | **UNVERIFIED as a dollar figure.** The number is consistent with 333 h × $0.15/hr ≈ $50, and that is a **reading**, not a quote |
| (not said, and it matters) data retention and model training | **Deepgram**: *"Requests participate in the Model Improvement Program (MIP) by default, and Deepgram retains them… Opt out per request with `mip_opt_out=true`. Opted-out requests are not retained."* **AssemblyAI**: *"Only certain files submitted to the API, as permitted by the applicable contract, are used for model training"*, with an opt-out and European servers as exclusions | **A gap in the spec**: as written, *"clients stream directly to providers"* with default parameters makes the audio **training data by default**. Any cloud call must carry `mip_opt_out=true` (Deepgram) and the equivalent opt-out (AssemblyAI) — an **enforced** parameter on the client's own connection, which belongs in ⊂ PR #633 §4.3's register row and in the minted session's configuration |
| Apple SpeechAnalyzer, "on-device, OS 26" | Apple's `SpeechAnalyzer` declares **iOS 26.0+, iPadOS 26.0+, macCatalyst 26.0+, macOS 26.0+, tvOS 26.0+, visionOS 26.0+** | **TRUE**, and it is a **floor** with an asset-download step (`AssetInventory`) — see #33 |
| Kokoro, Silero, whisper.cpp, openWakeWord, Porcupine licences | whisper.cpp **MIT**; Kokoro **Apache-2.0** (code and weights; "82 million parameters"; *"With Apache-licensed weights, Kokoro can be deployed anywhere"*); Silero VAD **MIT**; openWakeWord code **Apache-2.0** and **pre-trained models CC-BY-NC-SA 4.0** (verbatim in #34); Porcupine SDK **Apache-2.0** with a mandatory Picovoice **AccessKey** | **TRUE except openWakeWord's models** — the trap the brief anticipated, confirmed in the project's own README (#34). Porcupine's **plan tiers and commercial terms are UNVERIFIED** (#35) |

## 7. UNVERIFIED

Asserted nowhere above; listed so nothing is mistaken for a checked claim.

1. **Picovoice's plan tiers, usage limits and commercial-use terms** — `picovoice.ai/pricing/` is JavaScript-only (1,176 bytes of HTML, no readable terms) and the Porcupine docs carry no tier table. Only the AccessKey requirement and the Console-generated `.ppn` are verified.
2. **AssemblyAI's "$50" as a dollar figure** — the page states hours, not dollars (§6).
3. **Any latency number for any engine.** No device was measured here, Check 85 has not run and its text is not in the register. `voice.md`'s ~300 ms barge-in budget and `mobile/reconciliation.md`'s U7 (background audio with barge-in) are both **unmeasured** by their own documents.
4. **The real-time factor of Kokoro, whisper.cpp or Silero VAD on any target machine.** No benchmark was run and none is published for these models in this repository.
5. **Kokoro's model file sizes as shipped in any particular format.** The parameter count (82 M) is verified; "tens to hundreds of MB" is the spec's own estimate and is **not** verified per format or quantisation.
6. **Kokoro's language coverage as the spec uses it** — the model card's version table lists "8 langs & 54 voices" for v1.0 while its front-matter declares `language: en`. The discrepancy is recorded rather than resolved.
7. **Cartesia's retention and training terms.** Its authentication documentation was read; no retention or training statement was found and none is asserted.
8. **Bluetooth headset microphone degradation** (the spec's claim) — plausible and untested here.
9. **Apple voice-processing mode and Android `voice-communication` AEC** as available to a helper process on the target OS versions.
10. **Whether Electron's renderer on this console's pinned version supports `getUserMedia` at all under `sandbox: true`** — moot by D43 (#15), and not run.

## 8. What this document does not do

- **No plan.** No task, phase, file, ordering or dependency. §5's answers are the gate, and what follows them is a separate document.
- **No decision.** No D-number is allocated, none is answered, and nothing here supersedes a decision row. §5 Q9 is the question about supersession, not the supersession.
- **No number allocated** in the live-check register (#44, Q13).
- **No file changed outside this folder.** `docs/design/voice.md`, `docs/mobile/`, `docs/credentials-service/` and `console/` are cited and not touched; the amendment of `voice.md` that Q9 might take is a **separate, owner-approved, docs-only change** — which is mobile D129's own rule.
- **No claim about the code without a commit.** Everything is against `82ba600d…` and the seven branches of §0's table; where a fact lives only on a branch, the finding says "⊂ PR #n" and never reads it as `main`.

## 9. Related

[spec.md](spec.md) (the owner's spec, verbatim, committed alone) · [README.md](README.md) (this folder's index) · [voice-spec.md](../design/voice-spec.md) and [voice.md](../design/voice.md) (the 2026-10-15 spec and its reconciliation, D39–D54b) · [2026-10-16-voice.md](../plans/2026-10-16-voice.md) (its plan, unapproved) · [docs/mobile/](../mobile/) (the phone: D110–D129, D160–D173, P3.4/P3.5/P5.3/P6.x) · [docs/mobile/design/cloud-stt-tts.md](../mobile/design/cloud-stt-tts.md) (⊂ PR #633 — the switch and its seven points) · [docs/credentials-service/](../credentials-service/) (the "security service", D130–D154, plan only) · [desktop-console.md](../design/desktop-console.md) and [desktop-console-v2.md](../design/desktop-console-v2.md) (the console's security model and its one write) · [console-styling.md](../design/console-styling.md) (⊂ PR #634) · [console/docs/json-contract.md](../../console/docs/json-contract.md) (the feed contract) · [gcp-live-checklist.md](../gcp-live-checklist.md) (the check register) · [SECURITY.md](../../SECURITY.md) · [LICENSE](../../LICENSE) (MIT).
