# Fugaro Model Credentials Routing Spec (Nov 2026)

This replaces how model credentials are configured today. It supersedes any part of earlier specs that ties credential type to a repository.

## Problem

Today a repository is marked as either an "API key repo" or an "OAuth token repo". That is wrong. Credentials have nothing to do with repositories. How a model is accessed is a property of the model, configured at the project level.

## Three independent layers

1. **Recipes name abstract roles.** For example coder, first-line reviewer, senior reviewer and adversarial reviewer. Recipes never name credentials.
2. **The project maps roles to models.** For example coding uses model X and adversarial review uses model Y. This already exists and stays.
3. **The project defines credential routing per model.** This layer is new, separate from the other two, and lives **at the project level only**. It has no repo-level overrides.

## Routing table

Each entry matches a model name or pattern (for example `claude-*`, `deepseek-*`, `gemini-4`) and lists an **ordered chain of credential sources**. The first available, non-exhausted source wins.

Credential source types:

- **Launcher's pool**: the pool designated by whoever started the job.
- **Shared pool**: a named project-wide pool.
- **Anthropic API key** (project level).
- **OpenRouter API key** (project level).
- **Vertex** (the project's GCP).

Typical example:

- `deepseek-*` → OpenRouter key
- `claude-*` → launcher's pool
- `gemini-*` → Vertex

Complex example:

- `claude-*` → launcher's pool → shared pool → Anthropic API key → OpenRouter key → Vertex

## Pools

- **OAuth tokens live only in pools.** Raw standalone tokens are not supported. A single token still goes in a pool of one, so rules always apply the same way.
- **Pool rules** control usage, for example round robin versus a fixed order, or moving off a token once it reaches a usage threshold such as 80% of its limit.
- **Ownership.** A pool belongs to a principal: a human user, a service account or the project (a shared pool).
- **Designation.** Each principal designates their pool for the project. A project typically has 5 to 10 developers, each with their own pool.

## Principals and jobs without a human launcher

The "launcher" is whichever principal started the job. Jobs started by the coordinator, a cloud orchestrator or automation run as a **service account**, which has its own designated pool. The routing table needs no special case: "launcher's pool" resolves to the service account's pool.

## When credentials are resolved

- **Per step.** A job is a sequence of model calls (coder, then reviewers). Credentials are resolved fresh for each step, so a later step automatically falls through to the next source if an earlier one is exhausted. Nothing is frozen at the job level.
- **Per call, inside long steps.** A long coder session can hit a limit partway through. The LLM proxy owns the routing table and, on a rate-limit or quota error, transparently retries on the next eligible token or source. The agent never sees the switch.

## Edge-case decisions

- **Cost attribution.** A fallback to a pay-per-token source (Anthropic key, OpenRouter, Vertex) counts against project spending caps like any paid usage. The console flags the step as a fallback so people see that their pool ran dry.
- **Model equivalence.** A fallback is valid only if it serves the same model family and version (Opus 5.5 via Vertex can stand in for Opus 5.5 via a pool). There are no silent downgrades. A downgrade must be written explicitly in the chain, for example "then Sonnet via OpenRouter".
- **Session continuity.** Losing prompt cache on a mid-step switch is accepted and logged. The proxy keeps the same token for the rest of a step unless it actually hits a limit.
- **Pool privacy.** By default, a personal pool serves only jobs its owner launches. Teammates share only through the shared pool, which people contribute to deliberately. Admins see usage and headroom per pool, never the tokens.
- **All sources exhausted.** The job pauses rather than fails, waits for the earliest limit reset, and notifies in the console and on mobile. A project-level maximum wait applies. If the earliest reset falls beyond the maximum wait, the job fails fast immediately. Hotfix-rigor jobs fail fast.
- **Pause mechanism.** Never hold a proxy call open, because HTTP clients and Cloud Run would time out. Instead the job suspends itself: it saves the session and its state, marks itself paused in Firestore with a resume time, and exits. The scheduler starts a fresh job at the reset time and resumes the saved session. A paused job costs nothing while waiting.
- **Leftover repo-level config.** It is auto-migrated once into the project routing table. After that, the old fields are rejected with a clear error.

## Migration

- Remove the repo-level "API key versus OAuth" setting and its Terraform and config plumbing.
- Convert existing tokens into per-user pools, with a default routing table generated from current behaviour.
- Reconcile this spec with the credentials security spec and secrets addendum: the credential service and proxies enforce this routing.

## Open questions

- The exact syntax and precedence for pattern matching when several patterns match one model.
- Exactly what the console shows about which source served each step, for cost and debugging.
