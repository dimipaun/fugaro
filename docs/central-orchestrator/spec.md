# Fugaro Central Orchestrator Spec

Oct 20, 2026 · @Dimi

## Purpose

Define central orchestrators: Fugaro orchestrators that run permanently in the cloud instead of on a developer's machine. This clears up the earlier confusion between "central orchestrator" and the coordinator agent.

## Roles (terminology)

- **Orchestrator** is a role, not a location. It launches cloud jobs, shepherds them, and carries PRs through to merge.
  - **Local orchestrator:** runs on a developer's machine, one per developer.
  - **Central orchestrator:** runs in the cloud, per project. A project can have zero, one or several.
- **Coordinator:** one per project, advisory only, never opens or merges PRs. It keeps a project-wide view and helps prevent conflicts (see fugaro-coordinator-spec.md).
- Every orchestrator, local or central, reports to the coordinator in the same way.

## Use cases

1. **Developer continuity.** The developer can't reach their local orchestrator (laptop asleep, away, travelling). If they're authorised, they keep working through a central orchestrator, for example from the phone app.
2. **Event-driven work with no local agent.** Production signals like Sentry exceptions and other alerts go to an agent. That agent reads the logs, analyses the problem, proposes a fix, and asks a central orchestrator to launch cloud jobs. This fits the hotfix rigor mode.

## Optionality and cost

- Off by default. A project chooses whether to run central orchestrators at all.
- Auto-takeover is a separate switch, possibly per developer.
- Prefer scale-to-zero on Cloud Run, like the security service: wake on events (alert, expired lease, phone request) and don't pay for idle time.

## Job ownership

- Every job has an owner, which determines the spending cap, the credentials (OAuth token or key reference) and the reviewer.
- Jobs a developer starts stay owned by that developer, even when a central orchestrator shepherds them.
- Alert-triggered jobs belong to a project service identity with its own cap and credentials, held by the security service.

## Handoff

- **Lease model.** The orchestrator shepherding a job holds a lease in Firebase and renews it on a heartbeat (about every minute).
- **Automatic takeover (optional).** If renewals stop and a grace period passes (for example 10 minutes), a central orchestrator claims the job.
- **Explicit takeover.** When auto-takeover is off, a handoff only happens when the developer asks (for example from the phone).
- **Return.** When the local orchestrator comes back, it sees it lost the lease and can take the job back or leave it with the central one.
- **Invariant.** One job, one shepherd: two orchestrators never act on the same PR at the same time.
- **Prerequisite.** All shepherding state must live in Firebase, not in the local agent's context, so another orchestrator can pick up the thread. Open item: check how much state the orchestrator persists today.

## Access control

- Per-project allowlist of who may use which central orchestrator, matching the phone app's pairing and allowlist model.

## Management commands

- Project-level subcommands: `fugaro orchestrator add`, `fugaro orchestrator rm`, `fugaro orchestrator ls`.
- Flags for auto-takeover and grace period; an attach command for interactive sessions (see below).

## Interaction channel

- **Decision:** don't use Claude Code Remote Control. It's tied to one person's claude.ai login, needs a full interactive login (no setup token, no API key), and needs an always-on process. A central orchestrator has to be shared and have its own identity.
- **Rejected:** a stopped VM woken on demand. Wake-up takes about a minute, which is too slow.
- **Rejected:** Firebase as the chat bus. It works but is clunky.
- **Approach:** the orchestrator runs its agent through the Agent SDK with project-level keys or OAuth references from the security service. It runs on Cloud Run, cold-starts in seconds, stays up while clients are connected, and scales to zero afterwards.
- Clients connect directly over WebSocket: the CLI (`fugaro orchestrator attach`), the Fugaro console and the phone app all use the same protocol. Fugaro builds and owns the chat experience.
- Event sources like Sentry webhooks hit the same service through an HTTP endpoint.

## Multi-user access

- A central orchestrator can be reached by several people, gated by the per-project allowlist.
- Every message and command carries the sender's identity, for permissions, job ownership and an audit trail.
- Session model: one shared session (see Shared session).

## Shared session

- **Decision:** one shared, public session per central orchestrator. Everyone allowed in writes into the same conversation, which resolves the private-vs-shared question above.
- **Conversation ordering:** no hard locks. Messages are queued and the agent handles them in order, each tagged with its sender.
- **Presence:** a light "who is talking" indicator so people don't talk over each other.
- **Action locks:** consequential actions on a job (approving a merge, cancelling, raising a cap) need the job's owner, or an explicit override by an authorised person, recorded in the audit trail.

## Agent prompts and escalation

- When the agent needs input (a question, a permission or a decision), it raises a prompt tied to the relevant job.
- **Routing:** for jobs a person submits, the prompt goes to that person first and escalates after a timeout. For jobs an agent or automated process submits, it goes to the callback URL if one was provided, otherwise straight to humans with no waiting.
- **Escalation:** if nobody answers within a configurable timeout, it is shown to everyone on the project allowlist through Fugaro's own notifications.
- **Notifications:** phone app push and desktop notifications in the Fugaro console, plus a visible marker in the shared session.
- **Claiming:** the first authorised person to answer claims the prompt. Everyone else sees it as handled, with who answered and what they said.
- While a prompt waits, the job pauses only where it must. Other work keeps going.
- Scope: the orchestrator does not page anyone. Its prompts go back to whoever submitted the job (a person or an upstream agent like the Sentry router). The submitter decides whether to escalate, for example through PagerDuty. Escalation timeouts depend on rigor mode (see Resolved questions).

## Agent submitter callbacks

- **Optional:** an automated submitter can include a callback webhook URL when it submits a job.
- **Prompt delivery:** the orchestrator POSTs each prompt to the callback, signed so it can't be forged and carrying a prompt ID and the job context.
- **Answering:** the submitter replies through the orchestrator's HTTP endpoint, quoting the prompt ID. This suits stateless submitters like Cloud Run jobs or a Sentry router, which don't need a persistent connection.
- **Long-running agents** can instead stay attached over the WebSocket, just like a person does.
- **No callback URL:** the prompt escalates to humans immediately.
- **Callback fails or times out** (short timeout): the prompt escalates to humans.
- Whatever path a prompt takes, the answer and who gave it are recorded in the shared session and the audit trail.

## Alert storm protection

- **De-duplication:** group incoming alerts by issue fingerprint (for example Sentry's issue ID). If a job is already open for that issue, new events attach to it as extra context and don't start a new job.
- **Rate limit:** a per-project cap on how many automated jobs can start per hour (configurable).
- **Budget:** automated jobs have their own spending cap, separate from developers' caps.
- **Circuit breaker:** when the rate limit or budget is hit, the orchestrator stops launching automated jobs and sends humans one summary notification, not one per alert. Launches resume after a human resets the breaker or the window rolls over.

## Prompt injection safeguards

- **Untrusted input:** all alert content (logs, stack traces, exception messages, request data) is untrusted. It's clearly marked as data in the agent's context, never as instructions.
- **Reduced permissions:** alert-triggered jobs get a restricted toolset. They see only the secrets the fix needs and can't change CI config, infrastructure or permissions.
- **No auto-merge:** automated jobs always stop at an open PR and wait for human approval, even in hotfix mode.
- **Later improvement:** pre-build a hotfix (cut the branch, trigger the build) before approval, so the human only has to confirm the deploy. Out of scope for v1.

## Session memory

- **Storage:** the shared conversation is saved to Firestore after every turn, so a scaled-down instance loses nothing.
- **Resume:** on cold start, the orchestrator reloads the compacted summary plus recent messages.
- **Compaction:** auto-compact is on, as with the coordinator, since the session is long-running.
- **Archive:** raw history is periodically archived to the project's Git repo for the full record.
- **Garbage collection:** each Firestore message gets an expiry, 30 days by default, overridable per project, enforced by a Firestore TTL policy. Messages are only eligible once archived. The compacted summary lives as long as the session does.

## Submission auth

- **People:** authenticate as themselves, with the same identity used by the project allowlist.
- **Automated submitters:** each one (for example the Sentry router) gets its own named credential, issued and revocable through the security service, and scoped to what it may do (for example only open alert-triggered jobs).
- **Inbound webhooks:** verified with the provider's signing secret (for example Sentry's) before any job is created.
- Every submission records the authenticated submitter, which becomes the job's owner for routing, caps and audit.

## Client identity: pairing vs. authentication

- **Pairing is discovery, not identity.** Scanning the QR code only tells the client (phone app, console, CLI) which project and orchestrator endpoint to connect to. It grants no access by itself.
- **Authentication:** clients sign in with Firebase Auth. Google SSO and Microsoft SSO are supported, and email and password is allowed only with mandatory email verification. Unverified accounts are rejected.
- **Authorisation:** the orchestrator checks the verified email against the project allowlist on every connection and every submission.
- **Flow:** scan the QR code to find the project, sign in to prove identity, then the allowlist grants or denies access.
- **Follow-up:** update fugaro-mobile-app-spec.md so QR pairing is described as discovery only, with Firebase Auth as the identity layer.
- **Per-project auth policy:** each project sets which sign-in methods it accepts. For example, Rocket Core allows Google SSO only, with no email and password. Connections from any other method are rejected, even if the email is on the allowlist.
- **Email domain restriction (optional):** a project can limit sign-ins to specific email domains, such as the company's Google Workspace domain. Addresses outside those domains are rejected, even if they appear on the allowlist.

## Resolved questions

- **Auto-takeover policy (resolved):** set at the project level using RFC 2119 terms. **Must** means takeover is always on and developers can't opt out. **Should** means allowed and on by default. **May** means allowed and off by default. **Never** means not allowed. Under should or may, each developer can change their own setting.
- **Grace period (resolved):** 10 minutes without a heartbeat by default, configurable per project. A slow laptop keeps sending heartbeats, so only an orchestrator that has gone silent loses its lease.
- **Alert triggering (resolved):** a Sentry webhook into the orchestrator's HTTP endpoint, verified with the signing secret and de-duplicated by issue fingerprint (see Alert storm protection and Submission auth).
- **Instances (resolved):** one Cloud Run service per central orchestrator, shared by everyone, with one shared session (see Shared session).
- **Escalation timeouts (resolved):** set by rigor mode. Hotfix waits 5 minutes, critical 15 minutes, and the other modes 1 hour, before a human-submitted job's prompt escalates.
