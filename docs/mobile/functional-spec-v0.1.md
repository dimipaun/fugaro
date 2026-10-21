# Fugaro Mobile App — Functional Spec (v0.1)

*Status: draft, October 2026. Covers what the app does; architecture and backend wiring are out of scope for this revision.* **Amended 2026-10-20:** §4.1, §4.2, §5 and §6 carry dated amendment notes — the edits `docs/central-orchestrator/reconciliation.md` §4 lists for this file, plus the one the **owner's answer to that work's D6** adds — and **every sentence of the owner's text is kept**, as [functional-spec-reconciliation.md](functional-spec-reconciliation.md) §3.7 records. The D6 answer is **the owner's own word of 2026-10-20, relayed to the change that made these edits**, and the central orchestrator's documents **do not carry it** (§3.7 states the provenance in full).

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

> **Amended 2026-10-20 (edit 1 of `docs/central-orchestrator/reconciliation.md` §4) — pairing is discovery, not identity.** The sentence in step 2 is kept as written. Scanning the QR tells the client **which project and which orchestrator endpoint to connect to, and grants no access by itself**: the pairing token is a short-lived custom token admitted only to write its **own** pairing-request node (`config/pairing/<uid>/request`, under the `pu` claim and the `PAIRING` macro in `internal/budget/rules/rules.go`), and it reads and writes no project node. **The identity comes from sign-in (§4.2)**, and the access comes from the allowlist §4.2's amendment names.

> **Amended 2026-10-20 (edit 2).** The sentence that stood here — *"Only someone who already has access to the project (CLI or console) can generate a pairing code."* — **was moved to §4.2**, where it is an authorisation statement and does not re-import the discovery/identity conflation this section is removing.

### 4.2 Authentication and authorisation

- Sign-in uses **Firebase Authentication**.
- Because anyone can authenticate with Firebase, each project keeps an **allowlist of approved users**.
- Firebase security rules deny access to anyone not on the allowlist, regardless of successful sign-in.

> **Amended 2026-10-20 (edits 2, 3, 4 and 7).** The three bullets above are kept as written. What the owner's answers and the tree say instead:
>
> - **Who may pair (edit 2 — moved here from §4.1).** *Only someone who already has access to the project (CLI or console) can generate a pairing code.* — §4.1's sentence, word for word. In v1 that is the **CLI only** (the settled tension **T3**: the console is read-only by its exact-argv allow-list, D2/D14), and the commands are `fugaro pair`, `fugaro pair ls` and `fugaro pair revoke` (D166). **They are written nowhere in the tree today** — `internal/cli/pair.go` does not exist (plan task P6.6 is unbuilt) — so the sentence is a rule and not yet a mechanism.
> - **The premise is wrong, and sign-in is custom-tokens-only (edit 3).** "Because anyone can authenticate with Firebase" is false for a Fugaro installation: `internal/cli/init_firebase.go`'s `refusePublicSignUp` refuses an Identity Platform configuration with any public sign-in (email, anonymous, phone, any IdP) — `internal/infra/identityplatform.go`'s `PublicSignUp`, the list `refusePublicSignUp` refuses on: "The web API key is public, so only custom tokens signed by our signer may work" — and the web key is restricted to Identity Toolkit and Secure Token. So the phone signs in with a **custom token** from the project's signer (`internal/budget/token`'s `Mint`, the mechanism every run already uses), exactly as the settled tension **T5** and **D165** require. There is **no per-project sign-in-method policy**: the rule is custom tokens only, for every project.
> - **The allowlist is the paired device, not a list of users (edit 4).** `internal/budget/rules/rules.go`'s `userClaim` requires `config/admin/devices/<uid>` to exist (`PathDevices` = `config/admin/devices`, one child per paired device keyed by that device's Firebase Auth uid) **and** the token's `mu` claim to equal `auth.uid`. The rules therefore deny access to anyone without a device node, regardless of successful sign-in, and **deleting that node revokes at once** — which is what D166 chose a node over a claim for. The generated rules already carry this: the device node's own `$uid` child is `.read: false, .write: false`, so no client token holds a grant on it, and it is written over IAM.
> - **The entry carries an administered email, and an unverified email is refused at pairing (edit 7 — the owner's answer to the central orchestrator's D6, given on 2026-10-20).** The allowlist entry is the device node **plus an email the administrator writes** — an **administered field** on the entry, not a second list and not an email allowlist — and pairing **refuses an unverified email**, in the shape `internal/cli/init_members.go`'s `userEmail` (called by `authenticatedUser`) already enforces for the CLI: `email_verified` false is refused, and a service account's address is refused with it. The sign-in rule stays **custom tokens only**. *Nothing writes this field today:* it belongs to P6.6's `fugaro pair`, and the device node stays an IAM-written node. **Where the words are:** the answer is the owner's own, relayed to the change that made these edits, and **`docs/central-orchestrator/` carries none of it** — its D6 row holds the recommendation alone and that document says three times that nothing is answered ([functional-spec-reconciliation.md](functional-spec-reconciliation.md) §3.7 quotes them and states the provenance). The mobile reconciliation records the amendment in §3.7 and annotates D166's answer.

## 5. Summary of v1 decisions

- Purpose: talk directly to coding agents hands-free, removing the spec-copy-paste step.
- v1 commands are short and direct; long conversational spec-shaping is deferred.
- Agent prompts are voiced; queued by default, blocking issues interrupt.
- Multiple Fugaro installations connected simultaneously.
- Wake word "Fugaro" routes speech to the app; everything else to the active agent.
- Projects and agents switched by name.
- Projects added via QR pairing; no discovery.
- Firebase Auth plus per-project allowlist enforced by security rules.

> **Amended 2026-10-20 (edit 5).** Both bullets above are kept as written, and each gains the clause the edit asks for. *"Projects added via QR pairing; no discovery"* now reads: **pairing is discovery, not identity** — the QR names the project and its orchestrator endpoint and grants no access by itself; identity comes from sign-in and access from the allowlist. *"Firebase Auth plus per-project allowlist enforced by security rules"* now reads: Firebase Auth **with custom tokens only**, plus the per-project **device-node** allowlist the rules' `userClaim` requires (§4.2, edits 3, 4 and 7).

## 6. Open questions

- Full list of interrupt-worthy events beyond spending-cap hits.
- How the app chooses a default agent when switching to a project with several.
- Whether queued prompts expire, and what happens to a job if a prompt waits too long.
- How allowlist entries are managed (CLI command, console UI, both) and whether pairing auto-adds the scanning user.
- **Amended 2026-10-20 (edit 6):** the *mechanism* is answered — the entry is the **admin-only device node** `config/admin/devices/<uid>`, written and deleted over IAM by `fugaro pair`, CLI only in v1 (D166, T3, plan task P6.6, unbuilt) — and **pairing does add the scanner**: D165's confirming code is typed at the CLI for the key the phone holds, so the device the operator confirmed is the device added. What stays open is **who** administers it when the access set is a central orchestrator's rather than one phone's — re-asked as **D6** of `docs/central-orchestrator/reconciliation.md`, whose owner answer of 2026-10-20 (the owner's own word, relayed to this amendment; **that document does not carry it**, §3.7 of the reconciliation below) adds the administered email field and the pairing-time verification recorded in §4.2.
- Pairing-token lifetime and revocation of a paired device.
- Behaviour with no network or when a project's backend is unreachable.
- Whether the app offers a text/screen fallback for prompts and summaries.
- Timing and shape of the conversational front-agent (section 3.1).
