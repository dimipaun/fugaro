# Fugaro — Simplified Setup & Skills System

## Goal

Make Fugaro trivial to adopt in a new project — a README "Getting started" of roughly two lines — and establish a versioned, Fugaro-owned skills system that teaches agents how to use Fugaro.

Target README:

```
1. Run `fugaro init`
2. Open your coding agent and run `/fugaro-setup`
```

Everything long-winded lives inside the skills (for the agent to read), not in human-facing docs.

---

## 1. Firebase as the umbrella

- **One Firebase project = one Fugaro environment.** A Firebase project *is* a GCP project, so run everything inside it: Cloud Run (jobs), Secret Manager (secrets), GCS (storage), RTDB (live budget counters, agent roster, kill switch), Firestore (spend history).
- Because the team already shares the Firebase project, all team members automatically share the infra, secrets and budget view. No separate GCP project to wire up.
- **Compute backend is pluggable.** Keep job execution behind a backend interface: *take a job, run it, report cost and status.*
  - GCP Cloud Run in the same Firebase project is the default and the only implementation for 1.0.
  - Firebase always remains the control plane (budget, dashboard, stats), regardless of compute backend.
  - An AWS backend is out of scope; the seam should allow a community contributor to add one later (e.g. AWS-centric teams who want compute billed in AWS).

## 2. Two-phase setup

### Phase 1 — `fugaro init` (CLI, mechanical, deterministic)

Does only the work that requires no judgment:

- Create a new Firebase project, or point at an existing one.
- Enable/provision the required services in that project (Cloud Run, Secret Manager, GCS, RTDB, Firestore) and seed the RTDB budget nodes.
- Set up secrets if this is the first person on the team (GitHub token, provider API keys → Secret Manager); detect and skip if already present.
- Install the Fugaro skills into the project (project-level skills).
- **Must be idempotent** — safe to re-run at any time. Re-running also refreshes skills (see §4).
- Does **not** create `fugaro.yaml` (not even a stub) or a Dockerfile.

### Phase 2 — `/fugaro-setup` (agent skill, investigative, project-specific)

Run by the user inside their coding agent. The skill instructs the agent to:

- Inspect the repo: language(s), versions (e.g. Java version), build tool, test commands.
- Identify required services (Postgres, Redis, Firebase emulator, etc.).
- Produce the project's Docker image definition.
- Author `fugaro.yaml` **from scratch** — build/test commands, model choices (coder / reviewer), budget caps, etc. — discussing open choices with the user where needed.
- Produce Dockerfile and `fugaro.yaml` together, since they inform each other.

## 3. Skills set

Separate skill files, so the agent loads only what's relevant:

1. **Setup** (`/fugaro-setup`) — one-time bootstrap described in §2.
2. **Working with Fugaro** (operational manual) — how to spin a job into the cloud, task definition shape, monitoring, reading the dashboard, killing/retrying jobs, interpreting results (draft PR vs ready-for-merge).
3. **Cloud vs local routing** (judgment) — what belongs in the cloud and what doesn't:
   - Self-contained coding tasks → cloud.
   - Anything needing local credentials or environment (investigating live GCP/AWS resources, tasks relying on secrets that exist only on the developer's machine) → run locally. Cloud jobs' keys deliberately don't grant that access.
4. **Parallelism mindset** — teach the agent that it can fan out far wider than it's used to: wide for independent work, narrow for coupled work, optionally redundant attempts for hard single tasks. (May be folded into #2 if it's short.)

## 4. Skill ownership & versioning

- **Skills are Fugaro-owned and must not be edited.** State this at the top of each skill file. Teams wanting custom behavior write their own separate skills, which they manage.
- Each skill file carries a **Fugaro version stamp** (the version it shipped with).
- The CLI knows its own version and detects when committed skills are behind (e.g. a warning on regular commands and/or a `fugaro doctor` check).
- Updating is a **clean overwrite** — no merge logic. Done via re-running `fugaro init` or a dedicated `fugaro update-skills`. Git shows the diff for review.

## 5. Validation

Dogfood it: use `fugaro init` + `/fugaro-setup` to bootstrap Fugaro's own repo. If that isn't smooth, onboarding isn't ready for 1.0.

## Out of scope for 1.0

- AWS (or any non-GCP) compute backend implementation.
- Customizable/mergeable Fugaro-owned skills.
