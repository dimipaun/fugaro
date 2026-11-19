# Spec: messaging a running run

**Owner: the owner (Dimi Paun). Date: 2026-11-18.** Drafted by Claude from the owner's conversation of 2026-11-18 ("Our runs are long lived, like 3h or so. Would it make sense to be able to message with them?" — "Yes let's do it"), written before the repository was checked.

## Problem

A run lasts up to about three hours. When it heads the wrong way early, the person who launched it has two choices today: cancel it and launch again, losing the work, or let it finish and follow up on the pull request, paying the rest of the run first. A third choice is missing: tell the running agent something — "use the existing helper in X", "skip the console part", "that flaky test is known", "wrap up now" — and keep the progress it has made. The same channel would let an owner's ruling reach a run before it halts on `owner_decision`.

## What it is

1. **Inbound only.** A person (later the console, the mobile app or the orchestrator daemon) sends a short text message to a run that is going. The run never asks a question over this channel and never waits for a message: a run that receives none behaves exactly as today. This keeps the no-prompt policy whole.
2. **A command.** `fugaro runs say RUN "text"` (name to be settled) writes the message. It returns at once with the message's sequence number; it does not wait for delivery. `--json` reports the same.
3. **Trusted senders only.** The run's launcher, and the people `followup.trusted` lists. The message reaches the agent marked as that person's text, with the same standing as a follow-up's trusted comment: data from a trusted person, not a system instruction.
4. **Authenticated.** A launcher can write any run's objects under `runs/` (the L3 gap), so a message object alone does not prove who wrote it. The design must make a message's sender verifiable, or state why the existing trust model already covers it.
5. **Delivery at the next safe point.** By default the runner picks messages up and puts them into the next stage's prompt (a fix, a review, or a resumed implement). With `--now`, the runner delivers the message inside the current stage: interrupt the agent and resume its session with the message, or an equivalent mechanism that does not lose the session.
6. **The reviewer sees them.** Every review stage after a message is delivered is told the messages as part of the brief, so it does not flag a change the person asked for as a deviation.
7. **Recorded.** Every message — sent, delivered (when, into which stage), or never delivered (the run ended first) — is in the run record, in `report.md`, and shown by `fugaro diagnose` and `fugaro runs show`. Whoever reviews the pull request sees what steered it.
8. **Bounded.** A message has a size cap and a run a message-count cap; the text is redacted and cleaned like any other text the agent reads.
9. **Wrap up.** A message may ask the run to finish: the agent is told to commit what it has and stop, the run goes to its review and finalize as usual. Whether this is a separate flag or ordinary text is open.

## Out of scope

- The run talking back (questions to the person). Questions keep travelling the existing way (`needs_owner`, the pull request's "Waiting on you").
- Changing a run's caps, model or configuration by message.
- Messages between runs.

## Later surfaces

The console, the mobile app (through its existing mailbox) and the orchestrator daemon send through the same channel once it exists. The first slice is the CLI and the runner.

## Done means

- A message sent during an implement stage is in the next stage's prompt, and with `--now` in the current stage within a minute or two, without losing the session.
- A message from someone who is neither the launcher nor trusted never reaches the agent, and the report says it was dropped.
- The review after delivery sees the message.
- The record, the report and `diagnose` show every message and its fate.
- A run that receives no message behaves byte for byte as before.
