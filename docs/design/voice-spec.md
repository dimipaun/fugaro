# Fugaro Voice — Spec

Status: draft · 2026-10-15

## 1. Summary

A hands-free voice interface for talking to the Fugaro orchestrating agent (a Claude Code session). You speak naturally, with no push-to-talk. The agent's replies are condensed into short spoken summaries, and the stream of cloud-task events is kept out of the conversation and delivered as on-demand digests, or as interruptions based on a configurable setting.

It ships first inside the Fugaro console (Electron). It is built as a standalone module (`@fugaro/voice`) so it can later run against plain Claude Code and power a phone client.

## 2. Problem

- Typing is painful to impossible for long sessions; voice is the primary input.
- Claude Code's voice input requires holding a key to talk.
- The orchestrator's output mixes two things: the conversation with the user, and a stream of task events (started, finished, failed). The more tasks run, the more the conversation gets buried. Replies scroll off screen before they can be read, and answers land 10 to 15 messages after the thing they answer.
- Claude Code output is written for the screen (verbose, code blocks, paths, tables). It can't be read aloud as-is.

## 3. Goals

1. Fully hands-free conversation: always listening, natural turn-taking, barge-in.
2. Separate **conversation** from **task events**; never speak raw events inline.
3. Every agent reply is spoken as a short summary (target 1–3 sentences, ~15 seconds max), with full detail still on screen.
4. Task events are batched into digests, spoken on request or by interrupt policy.
5. Same core works in the console, standalone against Claude Code, and from a phone.

Non-goals for v1: wake word, multi-user voice, voice-driven code editing, offline-only operation.

## 4. Architecture

```
 mic ─► VAD + turn detection ─► STT ─► Voice Controller ─► Agent Bridge ─► Claude Code session
                                          │    ▲                 │
                                          │    └── replies ◄─────┘
                                          ▼
                                     Summarizer ─► TTS ─► speaker
                                          ▲
                      Event Digester ◄────┴──── Fugaro task events (RTDB)
```

Components, each behind an interface so providers can be swapped:

| Component | Responsibility |
|---|---|
| `AudioIn` | Mic capture, voice activity detection, end-of-turn detection, barge-in signal |
| `SpeechToText` | Streaming transcription |
| `TextToSpeech` | Streaming speech output, cancellable mid-utterance |
| `AgentBridge` | Send user turns to the agent; receive structured agent messages |
| `Summarizer` | Turn agent replies and event digests into speakable text |
| `EventDigester` | Collect task events, classify severity, build digests |
| `VoiceController` | State machine tying it together; owns interrupt policy and voice commands |

### 4.1 Agent Bridge

Two implementations, same interface:

- **SDK bridge (primary, console):** the console runs the orchestrator through the Claude Agent SDK (or `claude -p` with `--input-format stream-json --output-format stream-json`). User turns go in as structured messages; assistant text, tool use and results come out as typed events. No terminal scraping.
- **Hooks bridge (standalone, attach to an existing interactive session):** a small hook script registered for `UserPromptSubmit`, `Stop` and `Notification`. On `Stop`, read the transcript path from the hook input and extract the final assistant message. Messages are forwarded to the voice process over a local socket. Input is injected through the same mechanism the SDK bridge uses where possible; otherwise the hooks bridge is output-only in v1.

```ts
interface AgentBridge {
  send(text: string): Promise<void>;
  onMessage(cb: (m: AgentMessage) => void): Unsubscribe;
  onAwaitingInput(cb: (reason: string) => void): Unsubscribe; // permission prompts, questions
}
type AgentMessage =
  | { kind: "reply"; text: string; turnId: string }       // final assistant text for a turn
  | { kind: "progress"; text: string; turnId: string }    // intermediate assistant text
  | { kind: "tool"; name: string; summary: string };
```

### 4.2 Separating conversation from events

Task events do **not** come from parsing the agent's text. The `EventDigester` subscribes to the Fugaro RTDB agents/jobs nodes directly (the same source as `fugaro watch`). Agent output is then split:

- `reply` messages answering the user's latest turn → conversation channel → summarize and speak.
- Assistant text that only relays task status (e.g. "task X finished") → classified as event noise and dropped from speech; it already appears in the digest. Classification is done by the summarizer with a cheap yes/no pass, plus a rule that text produced in response to a task-completion callback (not a user turn) is never spoken directly.
- `progress` and `tool` messages → never spoken; shown on screen only.

## 5. Voice input

- Always-on listening once the session is started; no key held.
- VAD detects speech start/stop; end-of-turn uses silence plus semantic turn detection so mid-sentence pauses ("so, um…") don't cut the user off. Default trailing silence ~800 ms, extended when the transcript looks incomplete.
- Barge-in: user speech during TTS stops playback immediately and starts a new turn.
- Echo cancellation so the assistant's own speech isn't transcribed.
- Mute toggle (UI button, global shortcut, and voice command "mute" / "stop listening"). Unmute by button/shortcut, or by voice if wake-phrase support is enabled later.
- Transcript of each user turn shown on screen before sending; a configurable short grace period (default 0, i.e. send immediately) allows "cancel that".

## 6. Summarization

- Model: a fast, cheap model (Haiku-class), configurable.
- Input: the agent's final reply for the turn, the user's turn it answers, and a small rolling context.
- Output rules (system prompt):
  - 1–3 short spoken sentences; lead with the answer or outcome.
  - No code, file paths, URLs, hashes, or tables read aloud; refer to them ("I've put the diff on screen", "the config file").
  - Numbers rounded and spoken naturally.
  - If the agent asked a question or needs a decision, end with that question, stated plainly.
  - Never invent anything not in the reply.
- Follow-up commands: "more detail" re-summarizes the same reply at ~3× length; "read it" reads the reply's prose (still skipping code blocks).

## 7. Event digests and interrupt policy

The `EventDigester` keeps a rolling window of task events since the last digest, grouped by project.

Digest example: "Since your last check: five tasks finished, two are still running, one failed on Belong during tests."

Interrupt modes (setting, switchable by voice: "go quiet", "only urgent", "tell me everything"):

| Mode | Behaviour |
|---|---|
| `quiet` | Never interrupts. Digests only on request ("status", "what's happening"). |
| `important` (default) | Interrupts only for failures, budget-cap warnings, kill-switch events, and anything awaiting user input. Batched: at most one interruption per 60 s. |
| `all` | Speaks a short digest every N completed events or every few minutes, whichever comes first. |

Rules: never interrupt the user while they're speaking; wait for a gap. Interruptions are a one-line headline ("One failure on Belong — want details?").

## 8. Voice commands

Handled by the controller, not sent to the agent: `status`, `repeat`, `more detail`, `read it`, `stop` (stop speaking), `mute`, interrupt-mode switches. Anything else goes to the agent as a normal turn.

Destructive actions (kill switch, cancel jobs, merge) issued by voice go to the agent as normal, but the controller requires a spoken read-back confirmation before the agent executes them.

## 9. Mobile (phase 3)

- The agent keeps running on the laptop or in the cloud; the phone is a thin client.
- STT and TTS run on the phone side; only **text** crosses the relay, keeping it simple and cheap.
- Relay: Firebase (same Fugaro project). A `voice/sessions/{id}` node with `inbox` (user turns) and `outbox` (summaries, digests, prompts). The console/agent host listens to the inbox; no inbound ports opened on the laptop.
- Auth: Firebase Auth, restricted to the project owner(s); security rules scoped per session.
- Client: start as a PWA for speed; move to native if background audio or lock-screen use needs it.
- Same interrupt policy, delivered as spoken interruptions while the app is active, push notifications otherwise.

## 10. Configuration

```yaml
voice:
  stt: { provider: <tbd>, language: en }
  tts: { provider: <tbd>, voice: <tbd>, rate: 1.0 }
  summarizer: { model: <haiku-class>, max_sentences: 3 }
  turn: { trailing_silence_ms: 800, semantic_turn_detection: true }
  interrupts: { mode: important, min_interval_s: 60 }
  confirm_destructive: true
```

Lives in the console's local settings (per user), not in project config.

## 11. Phases

1. **Console MVP:** SDK bridge, hands-free input with VAD and barge-in, reply summarization, RTDB event digests, `quiet`/`important` modes, on-screen transcript.
2. **Standalone:** extract `@fugaro/voice`; hooks bridge for plain Claude Code sessions; small CLI (`fugaro voice`) to start it.
3. **Mobile:** Firebase relay, PWA client, push notifications.

## 12. Acceptance criteria (phase 1)

- A full 30-minute session with 20+ concurrent tasks can be run without touching the keyboard.
- No task-status message is ever spoken inline unless the interrupt policy allows it.
- Spoken replies average ≤ 15 seconds; the agent's full reply is always visible on screen.
- Pauses of up to ~1.5 s mid-sentence don't end the user's turn.
- Speaking over the assistant stops playback within ~300 ms.
- Destructive voice commands always get a spoken confirmation first.

## 13. Open questions

- STT/TTS providers: cloud streaming vs local (latency, cost, accuracy on technical vocabulary like repo names). Evaluate 2–3 candidates in phase 1.
- Custom vocabulary for project, repo and recipe names to improve transcription.
- How the hooks bridge injects input into an already-running interactive session, or whether standalone mode is output-only.
- Whether the summarizer should also be able to answer simple questions ("how much have we spent today?") directly from RTDB without going to the agent.
- Bilingual support (English/Romanian) — needed in v1 or later?
