# Fugaro mobile app spec

## Goal
An Expo (React Native) phone app giving a Fugaro Watch–style view of running jobs and, most importantly, hands-free voice conversation with the orchestrating agent, reusing the voice module planned for the Fugaro console.

## Principles
- No new backend: Firebase Auth + the existing RTDB only
- Phase 1: orchestrator runs on the owner's laptop (matches the current workflow; no regression)
- Phase 2: orchestrator moves to an always-on cloud service (small VM or Cloud Run); app unchanged

## Auth & access
- Firebase Auth against the project's existing Firebase project
- RTDB security rules restrict reads/writes to authorised users of that project

## Feature 1: Job list (secondary)
- Live list of running/recent jobs read directly from RTDB
- Per-job status, elapsed time, spend, PR link when ready
- Hide old completed jobs by default, with a toggle to show all (mirror dashboard behaviour)

## Feature 2: Voice with the orchestrator (primary)
- Hands-free, no push-to-talk, like Claude voice mode
- **RTDB mailbox pattern:**
  - Phone transcribes speech → writes message to an `inbox` node
  - Orchestrator listens on `inbox`, acts, writes reply to `outbox`
  - Phone reads `outbox` → speaks the reply via TTS
- **Summarising agent** sits beside the orchestrator: condenses task events into short spoken updates and filters noise
- Configurable interrupt switch (whether proactive updates can interrupt)
- Share the voice module code with the console where practical

## Presence
- Orchestrator writes a periodic heartbeat to RTDB
- App shows a clear "orchestrator offline" state rather than accepting messages into the void

## Open questions
- STT/TTS choice (on-device vs cloud) and latency targets
- Message retention and cleanup in RTDB
- Phase 2 hosting choice for the orchestrator


App should support multiple accounts with easy switching between them.
