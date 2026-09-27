# Fugaro — Cloud Agent Runner Specification

*A hosted, open-source system for running long, self-contained coding-agent loops (implement → test → review → fix → compile → open PR) in ephemeral cloud environments, instead of on a developer's laptop.*

## 1. Problem & Motivation

Running several coding agents locally saturates the machine: load averages over 100, thousands of threads, thermal throttling, and leaked build daemons (stray Gradle daemons) piling up in the background. Compiles and test suites are long-running (Android 10–20 min; server ~5 min compile plus ~20 min tests), and running agents in parallel makes everything crawl in a slow-hotter-slower spiral.

**Goal:** Offload well-defined, self-contained agent tasks to ephemeral cloud environments. Each task runs the full loop to completion and opens a PR. This frees the local machine and gives near-unlimited parallelism — run many tasks at once instead of having them fight over one throttled CPU.

**Initial scope:** Server (Java / Spring Boot) and Web (React / React Native + TypeScript). Android stays local for now (heavy compile and memory, lower priority to offload).

## 2. Product Shape: Open-Core

The system splits cleanly into two parts:

- **The engine (open-source, standalone GitHub project):** Generic and reusable, with no company- or repo-specific assumptions. Usable across any project — your own and the community's.
- **Per-repo configuration:** A small config file living inside each consuming repo, describing that repo's specifics: build type, build and test commands, required secrets, and git provider details.

**Guiding rule:** No secrets, tokens, service accounts, or internal assumptions ever live in the open-source engine. All of that stays on the config and secret-manager side. Open-sourcing enforces this discipline — you literally can't hardcode your own specifics.

This composes with team sharing rather than conflicting with it: the shared engine is open, and each repo carries only its own configuration.

## 3. Architecture Overview

### 3.1 Compute: Cloud Run Jobs (GCP)

Use **Cloud Run jobs** — run a container to completion, then stop — rather than a long-lived VM (Compute Engine). Automatic teardown makes the leaked-daemon problem evaporate, parallelism is effortless, and you pay only for the seconds a job runs. Task runtime (~30–50 min) sits comfortably within limits (a Cloud Run job task can run up to 24 hours).

### 3.2 Pre-baked Container Image

Bake in as much as possible so no run starts truly cold:

- JDK and build tools (server); Node and toolchain (web).
- A warm dependency cache.
- A recent checkout of the target repo (see the git-delta strategy below).
- The Bitbucket CLI, for creating and updating PRs.

Rebuild the image nightly or on dependency change, so it stays fresh without paying the full cost on every run.

### 3.3 Secrets: GCP Secret Manager

Never bake secrets into the image, and never pass them as plain env vars in the job spec. Put them in Secret Manager and have each job fetch them at startup via its own service-account identity.

- Each job runs as a **dedicated service account** with **least-privilege** access to only the secrets it needs — a server job cannot reach web-only secrets, and vice versa.
- Cloud Run can inject Secret Manager secrets directly as env vars or mounted files, so you often write no fetch code at all.
- Free benefits: secret rotation, plus audit logging of which run accessed which secret and when.

### 3.4 Caching (the crux)

A fresh ephemeral environment starts cold, so caching is what keeps it faster than local, not slower.

- **Dependencies:** Host a Gradle remote build cache; cache the node package store (npm/pnpm) in Cloud Storage, restored at startup. These two are the highest-leverage pieces.
- **Image:** Bake the JDK, build tools, and common dependencies into the image; rebuild periodically.
- **Startup flow:** restore cache from the bucket, run the task, then write the updated cache back on exit. This turns a cold ~30-minute run into a warm ~8-minute one.

### 3.5 Git Delta Strategy

The image ships with a recent checkout baked in. At startup, do a `git fetch` and then a hard reset to the exact target commit or branch — not a plain `pull` — so only the delta comes down, never a full clone. Consider a shallow clone if the history is large.

## 4. Task Lifecycle (Startup Sequence)

1. **Start** from the pre-baked image (JDK/tools, warm deps, recent repo checkout).
2. **Authenticate** via the service account — gaining access to Cloud Storage buckets, git, and Secret Manager.
3. **Restore** the freshest caches from the bucket (Gradle build cache, node package store), layered on top of what's baked in.
4. **Sync code** — `git fetch`, then a hard reset to the exact target commit or branch (delta only).
5. **Run the agent loop** — implement, tests, review round(s), fixes, recompile — mirroring the current local workflow, including multiple review rounds.
6. **Finish — always push, always PR:**
   - On **success**, push the branch and open a PR ready for review.
   - On **failure**, push the branch anyway and open a **draft PR** with the failure explanation, logs, and the stage it reached. Never discard the work — the PR becomes the debugging surface.
7. **Write back** the updated caches to the bucket; the job exits and Cloud Run tears everything down.

## 5. Task Spec (Hand-off Format)

A simple format describing a single task: the task description, the repo, the target branch, and which workflow (server or web). This is what gets handed off to launch a run. The local agent can construct this spec itself and trigger the job.

## 6. Launch & Observability

**Launch.** A simple entry point — "run this task on this repo at this branch" — executes the Cloud Run job with those parameters. Because it's just a job execution, you can fire off many at once and they run independently. This can be driven straight from the local agent: it constructs the task spec and calls gcloud to execute the job. That's the unlimited-parallelism goal, realized.

**Observability.**

- **Live logs:** Cloud Run streams everything to Cloud Logging automatically — tail a running job or watch them all in the console.
- **PR as durable record:** the (draft or ready) PR captures the diff, the reasoning, and any error in one place.
- **Status view:** a quick way to see what's running, what passed, and what failed — a simple dashboard or a command listing active and recent jobs.

**Guardrails.**

- A per-job timeout, so a runaway task can't burn hours silently.
- A billing budget alert, as a cost guardrail.

## 7. Local Agent Skills / Interface Layer

A set of documented commands or skills that teach the local agent how to operate the system:

- Launch a cloud task.
- List what's running.
- Fetch logs for a run.
- Diagnose a failure (surface the draft PR and its logs).

This is the interface that turns all the plumbing into something you just talk to.

## 8. Out of Scope (For Now)

- **The Android workflow stays local** — heavy compile and memory, and a lower priority to offload. Dropping it from v1 removes the worst of the cost and environment-fidelity problems, leaving the two workflows that convert cleanly.

## 9. Open Questions / Notes for Implementer

- Confirm Cloud Run job resource ceilings are sufficient for peak server build-plus-test memory.
- Decide the cache invalidation policy — how stale the baked-in deps and checkout may get before a rebuild is triggered.
- Define the exact per-repo config schema: build command, test command, secret references, git provider details.
- Keep everything Bitbucket- and company-specific strictly on the config side, out of the open-source engine.
