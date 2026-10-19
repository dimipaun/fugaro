# Fugaro Coordinator Agent — Design Spec

*Status: draft, Oct 2026*

## 1. Problem

Today each developer runs a local orchestrator agent. On a solo project (like Fugaro itself) that agent has the whole picture: every patch, every dependency, every release. On a large team project (e.g. Rocket Core: 50+ microservices, 10+ developers), each local orchestrator only sees its own slice. Nobody — human or agent — holds the project-wide view of what's in flight, what collides, and what ships when.

## 2. Goal

Add a **coordinator agent**: a per-project service that aggregates what every local orchestrator is doing, owns project-level state, detects conflicts early, and suggests sequencing — without taking decisions away from people.

## 3. Principles

- **Advisory, not authoritative.** The coordinator flags conflicts and suggests ordering. It never blocks or reorders merges. Humans have business context (hotfixes, client commitments, pinned release dates) the coordinator doesn't.
- **Learns from overrides.** When a developer goes against a suggestion, they give a short reason ("client hotfix", "release pinned to Friday"). Reasons are stored and fed back into future suggestions.
- **Project state lives centrally; developer views are lenses.** Anything shared (PR dependency graph, version roadmap, PR→release targeting) is owned by the coordinator. A developer's view is a filtered slice of it, never a separate copy.
- **Self-hosted.** Each team runs its own coordinator in its own GCP project. There is no Fugaro-operated central service.

## 4. Deployment

- **One coordinator per Fugaro project**, deployed into that project's Firebase/GCP project.
- **Runtime:** Cloud Run service, scale-to-zero. It wakes on requests and events, does its work, and goes idle.
- **Provisioning:** part of project init's existing Terraform, alongside the scheduled image-rebuild jobs and other resources. No separate deploy path. Any CLI convenience command just runs that Terraform.
- **Access:** follows from the deployment. Whoever can reach the project's coordinator is in the project; no extra permission model in v1.

## 5. State

- **Store:** Firestore (no Cloud SQL; Firestore and RTDB are already available).
- **Contents:**
  - Active work: which developer/agent is working on which services and files, with stated intent
  - PR records and the PR dependency graph
  - Version roadmap: future releases and which PRs target which version
  - Conflict and sequencing advice issued
  - Override log with reasons
  - A coordinator-maintained summary (key decisions, standing context)
- Firestore's real-time listeners drive live updates in the console.

## 6. Inputs

- **GitHub** as ground truth: branches, PRs, labels, merges.
- **Intent signals from local orchestrators**, sent *before* code exists (e.g. "about to touch the auth service"), so conflicts are caught early.

## 7. Interface: MCP front door, Firestore behind

- The coordinator exposes an **MCP server over HTTP**. Local orchestrators call tools such as:
  - `report_intent` — declare upcoming work (services, files, target release)
  - `check_conflicts` — ask whether planned work collides with anything in flight
  - `get_project_state` / `get_release_status` — query the shared view
  - `record_override` — log a decision against advice, with a reason
- Calls are synchronous and answer immediately (e.g. "someone is already changing that API contract").
- Works with any MCP-capable agent, not only Claude.
- **Local agents never touch Firestore directly.** The coordinator writes everything behind the MCP interface, keeping the data model private and free to evolve.
- Plain HTTP requests mean scale-to-zero is no problem (no long-lived listener connections on the service side).

## 8. Models

- **Configurable per project.**
- **Two tiers:**
  - *Routine tier* (cheap, fast): event intake such as "PR updated", "agent started work", state bookkeeping.
  - *Reasoning tier* (stronger): conflict detection, release sequencing, answering open-ended questions.
- **Credentials:** reuses the project's existing key management and security service. No new secrets path.

## 9. Long-running behaviour

The coordinator is long-lived in purpose but not one endless conversation.

- **Rebuild context per wake-up** from Firestore: current project state plus the coordinator's own maintained summary. Structured state is preferred over relying on compaction.
- **Auto-compact on** as a safety net within any longer session.
- **Concurrency:** simultaneous events are handled with Firestore transactions or a per-project lock, so the coordinator never contradicts itself.
- **Idempotency:** the same event delivered twice must be harmless.
- **Drift reconciliation:** a scheduled job (via the same Terraform) periodically reconciles the coordinator's view against GitHub.
- **Cost limits:** a daily token-spend cap so a runaway loop can't drain the budget; counts toward the project's existing spend caps.

## 10. Console integration

The Electron console gains a project-wide view backed by the coordinator:

- **Project view:** who's working on what, patches in flight per service, the PR dependency graph, blockers, release readiness, version roadmap.
- **Developer view:** the same data filtered to "my PRs, what they depend on, the releases I feed into".
- **Ask the coordinator:** natural-language questions like "what's holding up the payments release?"

On a solo project both views coincide, which is why this distinction wasn't visible while building Fugaro itself.

## 11. Open questions

- Exact MCP tool set and schemas
- Firestore data model for the PR graph and roadmap
- How override reasons are weighted in later suggestions
- Default models for each tier
- Whether finer-grained permissions (e.g. per-service visibility) are ever needed
