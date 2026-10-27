# Fugaro Secrets and Credential Brokering

Brief for the coding agent.

## Principle

All secrets live exclusively behind the credential service. It is the only component with access to Secret Manager. Everything else gets credentials from it, under policy. Apply this rule uniformly, even where an exception would bring no benefit, so there is a single, clear policy.

## 1. Secrets audit and consolidation

- Audit the Fugaro codebase and its infrastructure config (Terraform, Cloud Run job and service definitions, env vars, mounted secrets) for every place that reads secrets other than via the credential service.
- For each exception found, document why it bypasses the service, then move it behind the credential service.
- Remove Secret Manager access from every service account except the credential service's.
- Add a CI/build check that fails if new code or config reads secrets directly.

## 2. Caller authentication (no bootstrap secrets)

- Callers authenticate to the credential service with workload identity (Google-signed identity tokens from the Cloud Run metadata server), not with stored secrets.
- The agent sandbox must never call the credential service directly. The runner fetches narrowly scoped credentials on the agent's behalf.

## 3. Git provider access

### GitHub (phase 1: direct)

- Keep the current model: the credential service uses the GitHub App to mint short-lived, repo-scoped installation tokens.

### Bitbucket (phase 1: proxied)

Bitbucket cannot mint short-lived or narrowed tokens; repo, project and workspace access tokens are used as-is. For customers with many repos (such as [a customer with 50 to 60 repositories]), workspace tokens are the only practical option, and they grant access to the whole workspace. So we enforce narrowing ourselves with a proxy.

- Build the proxy into the credential service so the real Bitbucket token never leaves that service.
- The agent/CLI points git (HTTPS) and Bitbucket API calls at the proxy URL, presenting a Fugaro-minted, short-lived token that is only valid at the proxy.
- For each request, the proxy validates the Fugaro token, identifies the task, and checks the target repo (and action, such as push or branch) against the task's allowlist.
- If allowed, it strips the Fugaro token, attaches the real workspace token, forwards to Bitbucket, and streams the response back. Otherwise it rejects.
- Log every proxied request (task, repo, action, outcome).
- The proxy must not be built into the CLI, since the CLI runs alongside the agent.

### Phase 2

- Once the proxy is proven, route GitHub through it too, for one path, branch-level rules (such as blocking pushes to main) and a unified audit log.

## 4. Token lifecycle

- Fugaro proxy tokens expire when the task ends and can be revoked early.
- Make rotation of long-lived upstream tokens (Bitbucket workspace/project tokens) easy and supported by the credential service.

### Proxy token design

- Opaque random tokens, not signed JWTs. Each record holds the task ID, allowed repos and actions, and expiry.
- Store them in Firebase Realtime Database (already used by Fugaro), keyed by a SHA-256 hash of the token. Never store the raw token.
- The proxy looks up the token on every request. It may cache lookups, but it subscribes to RTDB changes so a revocation takes effect immediately.
- Revoke by deleting or flagging the record. The runner deletes the token when the task ends.
- RTDB has no native TTL, so run a periodic sweep that removes expired records.

Latency: RTDB lookups are not fast, so the proxy keeps validated tokens in an in-memory cache and invalidates entries through the RTDB subscription. Normally only a task's first request hits RTDB. If latency still becomes a problem, fall back to short-lived signed JWTs (around 15 minutes, refreshed by the runner) plus a small deny list.

## 5. Model API keys

- Review how LLM provider keys (Anthropic, OpenAI and so on) reach the agent sandbox. Consider proxying model API calls through the same mechanism so real keys never enter the sandbox.

## 6. Availability and hardening

- The credential service is now on the critical path for every task: give it redundancy and health checks.
- Keep it small and single-purpose, and restrict who can deploy it, because it is the crown jewel.

## 7. fugaro doctor checks

Extend `fugaro doctor` to warn about credential problems:

- Credential service unreachable, unhealthy, or failing to authenticate the caller via workload identity.
- Any service account other than the credential service's that has Secret Manager access.
- Secrets found in env vars, mounted secrets or config that bypass the credential service.
- Upstream tokens (Bitbucket workspace/project tokens, GitHub App key) that are missing, invalid, near expiry or overdue for rotation.
- Bitbucket proxy unreachable, or Bitbucket repos configured to go direct instead of through the proxy.
- Model API keys reachable inside the agent sandbox.
