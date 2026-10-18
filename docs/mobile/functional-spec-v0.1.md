# Fugaro Mobile App — Functional Spec (v0.1)

*Status: draft, October 2026. Covers what the app does; architecture and backend wiring are out of scope for this revision.*

## 1. Purpose

The core reason for the app is **hands-free, conversational work with Fugaro's coding agents**, in the same way one talks to Claude in voice mode today.

Today's workflow has a painful middle step: discuss an idea with a chat assistant, have it produce a Markdown spec, download it, then paste it into the agent. The app removes that step by letting the user speak directly to the orchestrator (and other agents) of any Fugaro project.

Secondary goals:

- Answer orchestrator prompts by voice instead of at a keyboard.
- Monitor and steer several Fugaro installations from one place.

## 2. Relationship to existing plans

- This app is the **phone client** of the hands-free voice interface already planned for the orchestrator (no push-to-talk, concise spoken summaries, filtering of task-event noise, configurable interrupt switch).
- The voice module is first built inside the **Fugaro console** (Electron) as a separable component; the phone app reuses it.
- Always-on needs of the app may later be served by a sibling service next to the per-project Cloud Run security service.

## 3. Interactions

### 3.1 Direct commands to agents

Version one focuses on **short, direct commands** rather than long design conversations, because the orchestrator is busy and its output is noisy. Examples:

- "Kick off the fix for the login redirect bug."
- "What's blocking job twelve?"
- "Cancel the docs refactor."
- "Summarise what finished in the last hour."

The app speaks back concise summaries, not raw task events.

*Future option (not v1):* a dedicated conversational agent in front of the orchestrator that shapes a spec with the user in a longer back-and-forth and hands a clean task to the orchestrator only on an explicit "go".

### 3.2 Voice prompts from agents

When an agent needs a decision (choose an option, approve a merge, confirm an action), the app reads the question and its options aloud and accepts a spoken answer ("option two", "yes, merge it").

**Queueing policy:**

- **Default: queue.** Prompts are held rather than interrupting the user. Only the job waiting on the answer pauses; all other work continues.
- At a natural pause the app announces pending items, e.g. "Two questions waiting." The user can also ask "What's pending?" at any time.
- **Blocking issues interrupt immediately.** Example: a spending cap being hit. Other interrupt-worthy categories are to be defined (see Open questions).
- The planned interrupt switch can widen or narrow what counts as interrupt-worthy.

### 3.3 Multiple installations

The app connects to **several Fugaro installations at once**. Each Fugaro project has its own Firebase project, and the app holds a session with each paired one.

- An overview shows activity across all connected projects (mirroring the console's cross-project overview).
- A project can have one or more agents; the user picks which one to talk to.

### 3.4 Addressing the app vs. the agent

Two channels are needed: speech meant for the app itself and speech meant for an agent.

- **Wake word: "Fugaro".** Any utterance starting with "Fugaro" is handled by the app. Everything else goes to the currently active agent.
- The wake word is chosen over automatic intent detection for v1 because it is predictable and never misroutes.

App-level commands (initial set):

| Command | Effect |
|---|---|
| "Fugaro, switch to *project*" | Make *project* active (switch by spoken name) |
| "Fugaro, talk to *agent*" | Direct speech to a specific agent in the active project |
| "Fugaro, what project am I on?" | Report active project and agent |
| "Fugaro, list projects" / "list agents" | Read out connected projects or agents in the active project |
| "Fugaro, what's pending?" | Read out queued prompts across projects |

Project and agent switching must be fast and possible entirely by voice.

## 4. Pairing and access

### 4.1 Adding a project: QR pairing

There is **no discoverable list of projects**. Discovery would leak information and does not scale for other adopters.

Instead, the user pairs each project explicitly:

1. Run a pairing command in the CLI or console (e.g. `fugaro pair`).
2. It displays a QR code containing the project's Firebase configuration and a short-lived, one-time pairing token.
3. The phone scans the code; the project is added to the app's list.

Only someone who already has access to the project (CLI or console) can generate a pairing code.

### 4.2 Authentication and authorisation

- Sign-in uses **Firebase Authentication**.
- Because anyone can authenticate with Firebase, each project keeps an **allowlist of approved users**.
- Firebase security rules deny access to anyone not on the allowlist, regardless of successful sign-in.

## 5. Summary of v1 decisions

- Purpose: talk directly to coding agents hands-free, removing the spec-copy-paste step.
- v1 commands are short and direct; long conversational spec-shaping is deferred.
- Agent prompts are voiced; queued by default, blocking issues interrupt.
- Multiple Fugaro installations connected simultaneously.
- Wake word "Fugaro" routes speech to the app; everything else to the active agent.
- Projects and agents switched by name.
- Projects added via QR pairing; no discovery.
- Firebase Auth plus per-project allowlist enforced by security rules.

## 6. Open questions

- Full list of interrupt-worthy events beyond spending-cap hits.
- How the app chooses a default agent when switching to a project with several.
- Whether queued prompts expire, and what happens to a job if a prompt waits too long.
- How allowlist entries are managed (CLI command, console UI, both) and whether pairing auto-adds the scanning user.
- Pairing-token lifetime and revocation of a paired device.
- Behaviour with no network or when a project's backend is unreachable.
- Whether the app offers a text/screen fallback for prompts and summaries.
- Timing and shape of the conversational front-agent (section 3.1).
