# Fugaro — Credentials, Secrets and Security Service

Oct 17, 2026 · @Dimi

## Goal

Senior review should run on Claude (Opus by default, Sonnet optional) using the cheapest available credential, failing over automatically and transparently. Model choice is decoupled from credential routing.

## Credential tiers (order of preference)

1. **A — Claude OAuth tokens (subscriptions).** Project-level named tokens, referencing individuals' tokens made available to the project. Cheapest at the margin; subject to subscription usage limits.
2. **B — Direct Anthropic API key.** Project-level, optional. Pay per token.
3. **C — OpenRouter key.** Project-level, optional. Pay per token plus markup.

No tier is mandatory. If none is usable, the review fails with a clear error naming which credentials were tried and why each was skipped.

## Selection algorithm

- Try every named OAuth token not currently marked exhausted, then B if configured, then C if configured.
- Quota detection is reactive: when a call returns a usage-limit or rate-limit error, mark that credential exhausted until the reset time reported in the response (or a default backoff if none is given).
- Exhaustion state is shared across jobs in the project (e.g. stored in RTDB) so parallel jobs don't all rediscover the same exhausted token.
- Among available OAuth tokens, spread load (round-robin or least-recently-exhausted) rather than always draining the first one.

## Stickiness

- Once a job starts on a credential, it stays on it for the whole review where possible, to preserve prompt caching.
- Switching happens only on exhaustion or hard failure.

## Mid-review failover

- Reviews on Opus can run 30+ minutes, so restarting from scratch is not acceptable.
- The conversation is portable: resume the same session with the next credential (Claude Code session resume with different auth in the environment).
- **Wait-or-switch rule:** if the exhausted credential resets within a threshold (default 15 minutes), pause and wait; otherwise fail over to the next tier.
- The threshold is a project-level setting.
- Cost note: the first call on a new credential misses the cache and re-reads the full context. On B or C this is real money for large diffs, which is why waiting is preferred when the reset is near.

## Durable session state

- Persist the review session (transcript and agent state) to GCS periodically during long reviews.
- A resume must work even if the Cloud Run job itself dies, not only on credential exhaustion.

## Spending caps and accounting

- Only tiers B and C count against project and per-repo spending caps; OAuth usage is tracked separately (calls, tokens, exhaustion events) for visibility.
- Every credential switch is logged with job, from/to credential, reason, and estimated cache-miss cost.
- Dashboard / console shows which credential each job is on and where reviews ended up paying.

## Credential lifecycle and safety

- **OAuth refresh races:** token refresh is serialized per named token (lock in RTDB or Secret Manager versioning with compare-and-swap). The refreshed token is written back to Secret Manager atomically; jobs always read the latest version before use.
- **Revoked vs exhausted:** an auth failure (401, invalid or revoked token) marks the credential `broken`, not `exhausted`. Broken credentials are skipped with no retry until fixed, and the token's owner and project admins are alerted.
- **Owner headroom:** each shared OAuth token can carry a usage share set by its owner (e.g. Fugaro may use at most a set number of review sessions per window, or stop after hitting the limit once), so contributors keep quota for their own work.
- **Prompt-injection exposure:** review agents read untrusted PR content. Credentials must never be readable by the agent's tools (inject via the runtime only, scrub from env and logs, restrict network egress). Covered in detail in the SECURITY doc.

## Security service (credential proxy)

- **Shape:** a standalone Cloud Run service per project, with its own service account. It is the only identity with Secret Manager access to the OAuth tokens and API keys. Sidecar and same-container options were rejected: containers in one instance share the service account and metadata server, so a hostile agent could fetch secrets directly.
- **Job side:** the job's service account has no Secret Manager access. Claude Code is pointed at the proxy via its base URL and given a short-lived, per-job token that the proxy validates.
- **Proxy responsibilities:** validate the job token, run the credential cascade (A, then B, then C), swap in the real credential, forward to Anthropic or OpenRouter, handle exhaustion, broken-token alerts, OAuth refresh, wait-or-switch, and meter spend against caps.
- **State:** stateless instances; all cascade state (exhausted/broken marks, reset times, locks, usage shares) lives in RTDB.
- **Cost:** min instances 0 with request-based billing, so it costs nothing between reviews; expect about a second of cold start on the first call after idle.
- **To verify early:** that subscription OAuth traffic from Claude Code still works when the proxy swaps the auth header (OAuth-specific headers may need passing through).
- **Future:** a natural home for other always-available, credential-holding project functions.

### Scope: all job secrets, not just model keys

- The service is the single holder of every secret a job needs. Jobs hold only their short-lived per-job token.
- **GitHub:** use a GitHub App owned by the project. The service mints installation tokens per job, scoped to the single repo and the minimum permissions (contents read/write on the job branch, pull requests write), expiring within the hour. Optionally route Git push and PR creation through the service so the agent never holds a GitHub token at all.
- **Other third-party secrets** (package registries, test-environment keys, etc.): issued per job, narrowly scoped and short-lived where the provider supports it; otherwise proxied so the raw secret never enters the job.
- **Audit:** every secret issuance and proxied call is logged with job, repo, secret name and purpose.
- **Boundary:** the service stays secrets-only. Other always-on features (e.g. the phone client backend) go in sibling services that call it, keeping its attack surface minimal.

## Resolved decisions

- **Default backoff with no reset time:** mark the credential exhausted for 15 minutes, then probe it with a minimal call before routing real work to it. Each failed probe doubles the wait, capped at the subscription window (5 hours). A successful probe clears the state.
- **Paid fallback policy:** project-level setting `fallback`, values `auto` (default: A, then B, then C) or `oauth-only` (never use B or C; jobs wait for the earliest OAuth reset). With `oauth-only`, the wait-or-switch threshold is ignored and the dashboard shows the job as waiting, with the expected resume time.
