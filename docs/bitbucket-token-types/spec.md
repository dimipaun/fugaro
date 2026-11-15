# Fugaro: Bitbucket Token Types in the Credential Proxy

**Related:** Project-Level Shared Jobs as Default (Bitbucket unblock).

## Problem

Bitbucket Cloud offers three kinds of access token: **repository**, **project** and **workspace**. Project and workspace tokens are Premium features, so teams on lower plans may only have repo tokens. Fugaro can't require one particular token type. The credential service's Bitbucket proxy has to accept any mix of the three.

Bitbucket gives us no endpoint for asking a token what it can access. Scopes are set when the token is created, and the token can't be viewed afterwards. So the proxy has to record each token's coverage itself, while asking the user for as little as possible.

## Principles

- **Keep it light for users.** Ask only for the token and what it covers.
- **The proxy does the narrowing.** For each job, the proxy limits access to the job's repos and to read or write. We don't store or track the upstream token's scopes.
- **Check on the way in.** Confirm a token works at the time it's added, and give a clear error if it doesn't.

## Token record

Each stored token has:

- `type`: `workspace`, `project` or `repo`
- `coverage`, written as a pattern:
  - workspace: `workspace-slug/*`
  - repo: `workspace-slug/repo-slug`
  - project: `workspace-slug` plus the project UUID. The UUID is the stable identifier; project keys can be renamed, so the key is stored only for display. The repos in a project are never stored as part of the token, only cached.
- the secret itself, kept in Secret Manager. The record only holds a reference to it.
- `verifiedAt` and the result of the last check

Repos are already identified as `workspace-slug/repo-slug`, which is Bitbucket's own format.

## Required scopes (documented, not stored)

Publish one list of scopes to tick when creating the token. At minimum that's repository read and write (needed for clone, fetch and push) and pull requests read and write. Let users choose from a pick-list, or paste the token and pick its type. Then the proxy:

1. Makes a cheap read call within the stated coverage (for example, fetching the repo, or the project or workspace).
2. Checks that the scopes it needs work, using read-only or dry-run calls where possible.
3. Rejects the token with a precise message ("missing pull request write", "token does not cover workspace X") rather than failing later inside a job.

## Matching tokens to a job

For a job that needs a set of repos:

- **Repo token:** matches one exact `workspace/repo`.
- **Workspace token:** matches any repo whose workspace slug is the same.
- **Project token:** look up the repo's project UUID with the repository API and cache the result. The token matches if the project UUID is the same. Do the lookup per repo when it's needed, not by listing every repo in the project up front. That way it stays correct when repos are added or moved between projects.

Choosing a token: use the **narrowest** token that covers the job's repos, preferring repo, then project, then workspace. If no single token covers all of them, either use different tokens for different repos (the proxy can swap tokens per request), or fail clearly, naming the repos that aren't covered.

## Cache and re-verification

- Cache which project each repo belongs to, with a TTL. If Bitbucket returns a 403 or 404, clear the cache entry and look it up again.
- Re-check stored tokens from time to time, and immediately whenever an upstream call is refused. Mark tokens that are failing, and show that in the CLI or dashboard.
- Repo, project and workspace tokens can have expiry dates. Record the expiry if the user gives one, and warn as it gets close.

## Rate limits

- A single workspace or project token may be shared by many parallel jobs, so its Bitbucket rate limit becomes a fleet-wide limit.
- The proxy keeps track of how many requests each upstream token has made, queues requests as it nears the limit, and backs off when Bitbucket returns a 429 (using Retry-After when present).
- Show throttling on the dashboard so slow jobs can be explained.
- Where possible, cut down on API calls: cache read-only metadata and avoid polling.

## Token identity

- Bitbucket shows an access token as the "user" on PRs, comments and commits. Recommend naming tokens clearly (for example "Fugaro") so it's obvious which activity came from agents.

## Optional, later

- User-scoped Atlassian API tokens as a fourth type for small teams, with workspace-level coverage that depends on the user's own permissions.

## Out of scope

- Storing or enforcing upstream scopes beyond the check when a token is added.
- GitHub, which continues to use app installation tokens.
