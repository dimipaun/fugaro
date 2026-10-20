# The hands-free voice session (`voice-session`)

**Status: plan only; nothing is implemented; decisions open.** The owner's spec
of 2026-10-20 for a hands-free voice session — what happens when the session
leaves the happy path: the app backgrounded, a call arriving, the headphones
unplugged, the process killed — committed **verbatim** and reconciled against
the code this repository already has. **No code, no config, no schema and no
manifest change follows from these documents, and nothing here is approved.**

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Base:** `main`
at `c92093b6` (the mobile tree at `12fbe60e`, mobile P3.5, #677).

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, **verbatim** — the owner's text copied unchanged, and nothing else. Committed alone in its own first commit (`aef225b3`). |
| [reconciliation.md](reconciliation.md) | What the spec asks for, read against the tree: findings **F1–F30**, each classified **Wrong / Exists / Gap / Rule / Open**, each with a path and a symbol and **the consequence for the design**; **25 rows of platform fact** read from Apple's and Google's own documentation on 2026-10-20, each with its URL and its verdict; **decisions D201–D220**, each with a recommendation and the alternative's cost; the owner-only steps (§6); items **O1–O8**; and the **UNVERIFIED** table. Where it disagrees with `spec.md`, **it wins**. |
| [implementation-plan.md](implementation-plan.md) | The plan: parts **VS0–VS7**, ordered so that each is mergeable alone and leaves `main` green, each with its files, its tests, a verification an agent can run and the reviewer tier that applies; the owner-only steps; the six checks; and the **OWNER APPROVAL GATE** at the end. **No part is approved.** |

## What this work is, in one paragraph

The spec was written **without seeing this repository**, so it reads greenfield:
it proposes a local Expo module (`modules/voice-session/`), a widget extension
(`targets/voice-session-widget/`), an eight-state machine, a shared event
contract and a set of iOS and Android behaviours as if none of it existed. Some
of it does exist, in a different shape — and the value of this folder is that
reconciliation, not the spec. **`voice-session` is a third layer:**
[docs/voice-module/](../../voice-module/) is the *pipeline* (STT → LLM → TTS,
console-first, foreground-only); [mobile/src/audio/](../../../mobile/src/audio/)
(P3.4) and [mobile/src/voice/](../../../mobile/src/voice/) (P3.5) are the *screen
and its ports*, merged; `voice-session` is **the audio session and the session
lifecycle** the pipeline runs inside. The two meet at four calls —
`pipeline.start()`, `pipeline.stop()`, `pipeline.speaking()`, `pipeline.turn()`.
That is finding **F1**, and everything else depends on it.

The one sentence this work **reverses** is
[docs/voice-module/spec.md](../../voice-module/spec.md)'s Goal — *"App stays in
the foreground while talking (no background listening)"*. That was the console's
ruling; the phone's spec asks for the opposite, and the reversal is named rather
than made silently (F2, decision D207).

## The numbers this work holds

| Block | State |
|---|---|
| **D201–D220** | this work's decisions, all written and **all open**. The spec's own four open questions are D201–D204 (the timeout, the "I'm back" cue, Hang Up opening the app, and 16.1 versus 17); the other sixteen are what the reconciliation exposed, including two the spec does not raise at all (**D216**, what the paused activity shows; **D209**, whether `READ_PHONE_STATE` is asked for). |
| **D193–D200** | **left alone.** [docs/coordinator/README.md](../../coordinator/README.md) says "D193 onward — free", and **at this work's base commit `c92093b6`** that command's highest hit under `docs/` is **D193** (`git grep -hoE '\bD[0-9]{2,3}\b' c92093b6 -- docs/ | sort -t D -k2 -n | tail -1`) — the coordinator table's own **free-block label** (*"D193 onward"*), not a written decision, the highest *written* one being **D192** — so D193–D200 are free by that reading. The anchor is the point: from the commit that carries this folder, the same command returns D220. The brief records **D200** as an already-taken ruling ("development builds only, no Expo Go") that **this tree cannot cite** (F20), so this work starts at **D201** rather than collide with a number the owner's own notes may hold. |
| **Checks 112–117** | this work's, **numbered but not written**. The register's written sections run to **89**; 90–99 are the credentials service's; 100–104 are the functional spec's, with 105–109 **reserved and unused**; **110** is named by the P5.3 design and never written; **111** is taken by [docs/setup-wizard/](../../setup-wizard/). Six are free and this work takes the first six. **None is written into the register**: the register's own rule is that *the phase that ships the check writes it*, and no phase here is approved. |
| **Check 86** | **not touched.** It already owns *"Hands-free audio: the background mode, the microphone ask, Bluetooth, and barge-in"* (P3.5/P4.2, owner-run) and its text is unwritten. It keeps its number and its subject; the new checks cover what it does not name (F17). |

## Why nothing here is decided

Every one of D201–D220 carries a **recommendation first, with its reason**, and
then **the alternative with its cost**. A recommendation is not a decision.
Answering a decision is not approving a task. The gate at the end of the
implementation plan is **not passed**, and the plan's first part is the only one
that touches anything an agent may run — a set of tests over the state machine
against a mocked event stream, which is what the spec's own §11 asks for and what
Global Constraint 10 allows CI to hold.

## Typographical corrections

**None.** The spec as received contains no typographical error that needed
fixing, so [spec.md](spec.md) is byte-for-byte the owner's text — the same
finding [docs/voice-module/README.md](../../voice-module/README.md) recorded for
its own spec. The delimiter lines that framed the text in the request that
carried it are that request's quoting convention and are not reproduced.

The spec's **platform claims are a different matter**, and they are corrected in
[reconciliation.md](reconciliation.md) §4 rather than in `spec.md`: the verbatim
file stays verbatim, and the corrections live where they can cite a source.

## Read in this order

1. [spec.md](spec.md) — the owner's text, unchanged.
2. [reconciliation.md](reconciliation.md) §1 — what `voice-session` is, and what
   it is not: the layer question, answered.
3. [reconciliation.md](reconciliation.md) §2 — the headline table: what the spec
   asks for, and what the tree already has.
4. [reconciliation.md](reconciliation.md) §3 — **F1–F30**, and the two that are
   most likely to be forgotten: **F24** (the secure store is unreadable while the
   phone is locked, so a paused session must need no credential) and **F26**
   (Android will not let a microphone service start from the background at all).
5. [reconciliation.md](reconciliation.md) §4 — the 25 platform facts, with URLs
   and verdicts. **Five of the spec's claims do not survive the check** — three
   are Wrong (P5, P7, P23) and two are wrong as written in a way that changes a
   design (P14's `POST_NOTIFICATIONS` parenthetical, P20's attribution of echo
   cancellation to `MODE_IN_COMMUNICATION`). Of the five, **P5's `staleDate` and
   P7's `.voiceChat` would have been built exactly as the spec writes them**,
   because each reads as a platform guarantee until the platform's own page is
   opened. P16 and P17 are Unverified and are asserted nowhere.
6. [reconciliation.md](reconciliation.md) §5 — **D201–D220**, each with its
   recommendation and its cost. **None is answered.**
7. [reconciliation.md](reconciliation.md) §6 — what each part costs the **owner**,
   by portal, and what a device must show that an emulator cannot.
8. [implementation-plan.md](implementation-plan.md) — the parts, and the
   **OWNER APPROVAL GATE**.

## Related

- [../README.md](../README.md) — the mobile work this belongs to (P3.4's
  `src/audio/`, P3.5's `src/voice/`, the Global Constraints, checks 80–89).
- [../implementation-plan.md](../implementation-plan.md) — the phases, the
  constraints this plan is written under, and the check table.
- [../../voice-module/](../../voice-module/) — the **pipeline** this module
  drives: the spec, and its reconciliation (Q1–Q13 answered 2026-10-19).
- [../design/voice-evaluation-switch.md](../design/voice-evaluation-switch.md) —
  P5.3: voice is local-only, with an in-app local/remote evaluation switch. It
  names **Check 110**.
- [../design/push-notifications.md](../design/push-notifications.md) — P5.2, and
  why this spec's local notification is **not** the push design (F12, D211).
- [../../gcp-live-checklist.md](../../gcp-live-checklist.md) — the register, its
  rules, and the checks this work takes (112–117).
- [../../coordinator/README.md](../../coordinator/README.md) — the decision
  register and its block table.
- [../../../mobile/README.md](../../../mobile/README.md) — the app: what is in
  the tree, and the owner's ruling that it ships **voiceless** (F7).
