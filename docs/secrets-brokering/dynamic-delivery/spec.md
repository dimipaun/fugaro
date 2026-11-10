# Fugaro Secrets Addendum

Addendum to fugaro-credentials-security-spec.md. Replaces the current Terraform-based secret mounting with dynamic, runtime secret delivery through the per-project credential service.

**Note for the implementing agent:** before starting, find the existing secrets/credentials brokering spec (most likely fugaro-credentials-security-spec.md) and any related task. If their scope overlaps, merge this addendum into it as one unified spec rather than keeping two. Where they conflict, this addendum's decisions are newer; flag every conflict explicitly for review rather than resolving it silently.

## Problem

Today a secret (Anthropic API key, OpenRouter key, Claude OAuth token) is stored in Secret Manager by a Fugaro command, then `fugaro update` runs Terraform to mount it into the Cloud Run job.

- Slow and awkward: every secret add or rotation requires a Terraform run.
- Insecure for teams: every member who adds a secret must be able to run Terraform, which effectively grants owner-level access to the whole GCP project.
- Still wired per repo, contrary to the project-centric redesign.

## Design

### Storage

- Secrets live in Secret Manager as `fugaro-u-<gcp-account-id>-<name>` (per user) and `fugaro-p-<name>` (project-shared). The prefix uses the numeric Google account ID from the caller's identity token, not the email (secret names disallow `@` and `.`; the ID is stable across email changes). Firebase IDs are not used.
- IAM conditions cannot scope secret creation by name (the secret does not exist yet at create time), so users do not write to Secret Manager directly.
- A separate, tiny intake function (Cloud Run, IAM-authenticated: project members with invoker rights only) accepts secrets from users and writes them under the caller's prefix.
- The intake function can only create secrets and add versions: no read access, no IAM rights. It cannot leak anything because it cannot read anything.
- No pre-created per-user slots, no admin step per user, no Terraform per secret.

### Credential service as sole reader

- The per-project credential service is the only principal with read access to Secret Manager, and it has no write access (least privilege).
- Jobs never read Secret Manager directly and have no secrets mounted.
- Infrastructure wiring is set up once at project level; secrets become fully dynamic afterwards.

#### Job grants (slips)

1. The launcher asks the credential service for a grant: what the job is and which credentials it needs.
2. The credential service checks the requester's rights, then writes the slip (owner, allowed credentials, max runtime) to a Firestore collection only it can write. It returns the slip ID to the launcher. No signing needed, since nobody else can forge or edit a slip.
3. The launcher starts the job and passes it the slip ID.
4. The job's first request claims the slip and binds its Cloud Run execution ID to it (the execution ID does not exist until launch).
5. Every request carries the slip ID plus the job's Cloud Run identity token; the service verifies both against the slip. The slip ID alone is not a bearer ticket.
6. Issued credentials expire at the job's max runtime and are revoked when the job finishes.

#### Caching

- In-memory slip cache keyed by slip ID, 5-minute lifetime, no eviction. Matters mostly for the LLM proxy, which every model call goes through.
- Cache is per instance and lost on scale-to-zero; acceptable.
- Worst case: a finished or revoked job keeps access for up to 5 minutes; accepted as non-exploitable.

### Narrowing

- Where the provider supports scoping (e.g. GitHub tokens), the credential service mints narrowed, short-lived tokens per job.
- Where it does not, the credential service fronts the provider with a proxy that enforces narrowing. Same pattern as the planned Bitbucket proxy (workspace access keys cannot be narrowed).

### LLM proxy

- Anthropic and OpenRouter keys are all-or-nothing, so they are proxied rather than handed to jobs.
- Jobs point at the proxy (Claude Code via `ANTHROPIC_BASE_URL`; OpenRouter via its OpenAI-compatible base URL) using a short-lived per-job token as the key. The proxy swaps in the real key.
- The proxy is the natural enforcement point for project spending caps, model allowlists and metering.
- Must handle streaming responses correctly and keep cold-start latency acceptable on a scale-to-zero service.

### Claude OAuth tokens and pools

- Fugaro provides mechanism, not policy.
- Token pools are supported: e.g. a user pooling their own four tokens from different projects for failover.
- Every token carries an owner, and every job records which token it ran on.
- This lets teams layer policy on top (e.g. owner-only: a job only runs on its owner's tokens) without redesign. Note Claude subscription tokens are not meant to be shared between people; enforcing that is a policy choice left to the team.

### Lifecycle

- The intake function can also list (names and dates only, never values) and delete secrets under the caller's own prefix. It still has no read access.
- Offboarding: an admin command revokes the user's invoker rights and deletes their prefix.
- Pools are resolved at slip-issue time, so pools referencing a removed user's tokens simply lose those members.

### Shared secrets

- Only project admins, listed in the project config, may write to the `fugaro-p-` prefix. The intake function checks the caller against that list.

### Pool authorisation

- Each pool has an owner and an optional list of allowed users.
- Before writing a slip that names a token or pool, the credential service checks the launcher's identity against that list. Default: owner only. Mechanism, with teams setting policy.

### Resilience and audit

- Credentials a job has already fetched survive a credential-service outage; the LLM proxy is the real dependency.
- Jobs retry credential and proxy requests with backoff.
- Keep one warm instance whenever jobs are running.
- Every issued credential and every proxied call is logged to Cloud Logging with slip ID, owner and token used.

## Removed

- Terraform-based secret mounting on jobs.
- `fugaro update` as a step in adding or rotating secrets.
- Mandatory Anthropic/OpenRouter keys (consistent with the project-centric redesign).

## Open questions

All resolved:

- Prefix naming: Google account ID based, written via the intake function (Storage).
- Per-job grant: service-written slips, claim on first use, 5-minute cache (Credential service).
- LLM proxy placement: inside the credential service as its own route; split out later only if load demands.
