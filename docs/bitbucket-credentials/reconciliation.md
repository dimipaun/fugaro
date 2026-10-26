# Bitbucket support through the credentials service: reconciliation

**Draft. The decisions below are open: the owner has answered none of them.** This document is
checked against `main` at `ce991d88d` and against Bitbucket's own documentation as read on
2026-10-26. Where it disagrees with [spec.md](spec.md), this document wins.

## 1. Summary

The spec asks for two things. The coordinator should read the user's own Bitbucket as well as
their own GitHub. And the credentials service should hold one general Bitbucket credential and
mint narrower ones from it, per repository and per run.

The first half can be built. The second half cannot be built the way the spec imagines it,
because Bitbucket Cloud has no credential that mints a token narrowed to one repository (§3).
The only Bitbucket credential that mints short-lived tokens through an API is an OAuth consumer
using the `client_credentials` grant. Its tokens carry the consumer's fixed scopes across
everything the consumer owner's account can reach, and Bitbucket refuses a per-request `scope`.
So a token the service minted "for one run" would still be valid on every repository in the
workspace. The service could choose which caller gets a token. It could not limit what that
token does once the caller holds it.

That is acceptable for the coordinator, whose reads are read-only and project-wide by design. It
is not acceptable for a run. A run's git credential reaches its agent (F7). So a minted
workspace-wide token would widen what a hostile agent can write from one repository to the whole
workspace. Today that agent can write to one repository only.

The recommended shape:

- **The coordinator reads Bitbucket** through one read-only credential, held by the coordinator's
  own job the way it already holds the GitHub App key (D361, D362).
- **Runs keep their per-repository access tokens unchanged** (D363).
- **The console's project view reads what the coordinator job already stored**, so neither the
  console nor a person's CLI needs any key (D364). That read needs no credential at all, and it
  is the smallest useful first slice (§6).

## 2. What the spec names, and what the repository already has

| Spec concept | What exists | Where |
| --- | --- | --- |
| The coordinator reads the user's own GitHub, not Fugaro's | Yes. A read-only installation token is minted per repository from the **user's** App (`FUGARO_GITHUB_APP_ID` plus the user's key), cached, and revoked at the end | `internal/cli/coordinator.go` `coordinatorReader`, `coordinatorTokens`, `coordinatorAppCredential`; D193 |
| The coordinator refuses Bitbucket | Yes, `coordinator.ErrBitbucket`. The sweep records such a repository as `StaleUnsupported` | `internal/coordinator/project.go` `supported`; `internal/coordinator/sweep.go` |
| A server-side coordinator read | Yes: the aggregation job `fugaro coordinator sweep`, run as `fugaro-coordinator`, with `secretAccessor` on the project's App key container. Its key is mounted through `secret_key_ref` | `deploy/terraform/gcp/modules/installation/coordinator.tf` `coordinator_app_key`; `internal/cli/coordinator.go` `envCoordinatorRepos` |
| The stored project view | Yes: `coordinator/repos/<slug>/` in the installation's RTDB (P3, D195), readable by a signed-in reader. **No CLI command reads it**: `coordinator.Store.Read` has no caller in `internal/cli` | `internal/coordinator/state.go` |
| The service holds the GitHub App key and mints from it | Yes, for **runs only**: `POST /v1/github/installation-token` (`credsvc.MintPath`), audited (`PurposeMint`), fail-closed (`ErrMintNoAppKey`, `ErrMintUnaudited`), with no repository field | `internal/credsvc/github.go` |
| Who may call the service | Only an installation's **per-repository job** service account presenting a **live run identity**. Shared project jobs are refused (Global Constraint 6) | `internal/credsvc/auth.go` `Authenticator.Verify`, `verifyJob`, `verifyRun`, `checkLive` |
| The Bitbucket credential | One repository access token per repository: `bitbucket-token`, mounted as `FUGARO_BITBUCKET_TOKEN` | `internal/gitprov/providers/providers.go` `EnvBitbucketToken`; `internal/config/project_secrets.go` `PerRepositorySecret` |
| A project-wide Bitbucket token | Possible only through an explicit `project_secrets.bitbucket-token` allow-list entry in the local config, for an operator who holds a workspace or project token. Shared jobs never mount it | `docs/git-providers.md` §Bitbucket Cloud; `docs/design/project-centric.md` §4.5 |

## 3. Bitbucket Cloud credentials, as Atlassian documents them (read 2026-10-26)

Every row cites Atlassian's own page. Anything a page does not say is marked **UNVERIFIED**. The
developer.atlassian.com pages render their content with JavaScript, so the REST reference was read
from `https://api.bitbucket.org/swagger.json` (cited below as "swagger").

| Credential | Reach | Mints narrower tokens? | Plan | Expiry | Source |
| --- | --- | --- | --- | --- | --- |
| **Repository access token** | One repository: *"can't be used to manage or interact with workspaces, projects, or any other repository"* | **No.** There is no API to create one, so it is made in the UI only, and it cannot create others | No plan requirement is stated. That Free and Standard plans may create them is **UNVERIFIED** | Set at creation. An admin may require one, otherwise "No expiry" is offered. No maximum is stated | support.atlassian.com/bitbucket-cloud/docs/repository-access-tokens/, …/create-a-repository-access-token/, …/access-tokens/ |
| **Project access token** | *"a single project and any repositories in that project"* | No | **Premium** | Optional | …/project-access-tokens/ |
| **Workspace access token** | *"a single workspace and any projects/repositories under that workspace"* | No | **Premium** | Optional | …/workspace-access-tokens/ |
| **OAuth consumer, `client_credentials` grant** | The token represents *"the owner of the client/consumer"*. *"Scopes are defined on the client/consumer instance"*, and the per-request `scope` parameter is not supported | **Yes, short-lived**, at `POST https://bitbucket.org/site/oauth2/access_token`. **Not narrowable** to a repository | No plan requirement is stated (**UNVERIFIED**) | **1 hour** (swagger) or **2 hours** (…/use-oauth-on-bitbucket-cloud/): **the two pages conflict** | swagger auth text; …/use-oauth-on-bitbucket-cloud/ |
| **API token** (Atlassian account) | Tied to one person's account. It can be narrowed to a workspace, never to a repository | No | Not applicable | Required at creation, at most 1 year | …/api-tokens/, …/using-api-tokens/, swagger |
| **App password** | Not applicable | Not applicable | Not applicable | Creation stopped 2025-09-09. Existing ones stopped working after **2026-07-28** | atlassian.com/blog/bitbucket/bitbucket-cloud-enters-phase-2-of-app-password-deprecation; support.atlassian.com/bamboo/kb/… |

Further verified facts:

- Each repository, project and workspace has a limit of 25 access tokens, and the limit cannot be
  raised (…/access-tokens/).
- Access tokens are *"tied to a repository, not a user's account"*. Commits show a
  `{botid}@bots.bitbucket.org` author (…/using-access-tokens/). Git uses the username
  `x-token-auth`, and the REST API uses a bearer header.
- An access token *"can't … manipulate or query repository, project, or workspace permissions"*
  (swagger). The REST definition has **no endpoint that creates an access token**. Its only token
  endpoint is OAuth's.
- Connect apps reach end of support at the end of January 2027 (community.developer.atlassian.com,
  2026-01-30). Whether a Forge app gets a token narrowed to a repository is **UNVERIFIED**.
- Bitbucket Data Center has personal, project and repository HTTP access tokens, and their expiry
  is set at creation (confluence.atlassian.com/bitbucketserver/http-access-tokens-939515499.html).

**Answer to the spec's central question.** No Bitbucket Cloud credential narrows a token to one
repository at mint time, so there is nothing like GitHub's `{"repositories":[repo]}` installation
token. Only an OAuth consumer mints at all, and its token reaches everything the consumer owner can
reach, within the consumer's scopes. A service that minted from one would have to enforce
per-repository policy itself, and it can enforce that only on **which caller receives a token**.
Once a caller holds the token, nothing limits its use to the caller's own repository until the
token expires an hour or two later. The precise consequence is stated in F6 and F7.

## 4. Findings

**F1, Exists.** The coordinator already reads the user's own GitHub through the user's own App.
`coordinatorAppCredential` reads `FUGARO_GITHUB_APP_ID` and the user's key, and Fugaro owns no App.
The spec's first sentence is already true for GitHub. *Consequence:* the work is Bitbucket, plus the
read paths that hold no key (F9, F10).

**F2, Gap.** With the credentials service on, the **local** `coordinator status` refuses GitHub too.
`coordinatorAppCredential` returns a user error whenever `FUGARO_CREDENTIALS_SERVICE_URL` is set,
because the service mints only a run's token. *Consequence:* "store these credentials in the
service as well" does not yet work for GitHub's coordinator read either. Moving Bitbucket into the
service would not give the local read a Bitbucket path without a **new kind of caller** at the
service (F11). → D362, D364.

**F3, Exists.** The coordinator's server-side read already holds a project credential of its own.
`coordinator.tf`'s `coordinator_app_key` grants the coordinator account `secretAccessor` on the App
key, and the sweep job mounts it. That grant is separate from `config.ServiceHeldSecretNames` and
from `modules/workflow/sa.tf`'s `service_held_secret_names`, so the planned drop of the App key from
jobs does not touch it. *Consequence:* once an installation turns the service on and the drop lands,
the service is still not the only holder of the App key. That may be fine, because the sweep runs
no agent. But it should be stated rather than discovered. → D365.

**F4, Rule.** The owner's ruling of 2026-10-15 says a Bitbucket credential cannot be scoped to one
Fugaro project's repositories, so Bitbucket stays per repository. It is recorded in
`docs/git-providers.md` (§Bitbucket Cloud), in `docs/design/project-centric.md` (D5 as amended,
§Task 3.2) and in `config.PerRepositorySecret`. The spec's "more generic Bitbucket credential" is
exactly a workspace-reach or project-reach credential (§3). *Consequence:* the spec reopens that
ruling, and the owner has to reopen it explicitly. The recommendation reopens it **for read-only
use by the coordinator only**. → D360.

**F5, Rule.** D145 (`docs/credentials-service/reconciliation.md`) excluded Bitbucket because
*"repository access tokens cannot be minted per job"*. §3 confirms the premise: no Bitbucket
credential can be minted per repository. *Consequence:* D145's exclusion stands for runs. The
spec's "per run" half has no Bitbucket mechanism to stand on. → D363.

**F6, Open (security).** Suppose the service held an OAuth consumer and minted from it for runs. The
service would decide which job gets a token, but every minted token would be valid on every
repository the consumer owner can reach, with the consumer's scopes, for 1 to 2 hours.
Per-repository policy would live **only** at the moment the token is handed out. Nothing at
Bitbucket would enforce it. → D363.

**F7, Gap (security).** A run's git credential reaches its agent. The agent gets
`FUGARO_GIT_TOKEN` and the git credential helper (`docs/git-providers.md`, "What the agent gets").
On Bitbucket that is the long-lived repository access token itself, and even a follow-up run's
agent can read the runner's `/proc`. *Consequence:* under F6, a hostile or prompt-injected agent
could push to, open pull requests on, or merge in **any repository of the workspace** for the
token's life. Today it can do so on one repository, though for as long as the token lives. A
short-lived workspace token handed to a run is therefore **a widening of reach in exchange for a
shortening of lifetime**, and it is a bad trade for an agent-held credential. The only shape that
narrows a run's Bitbucket reach below today's is a **proxy**: the service performs the push and
the pull-request calls and the token never leaves it. D145 deferred that shape ("proxied push/PR
creation: later, optional"), and it is a large build: a git smart-HTTP proxy plus a REST proxy.
→ D363.

**F8, Gap.** The Bitbucket token has more consumers than the runner. The repository's Cloud Build
account reads it as a build secret (`modules/repo/build.tf` `google_secret_manager_secret_iam_member.build`),
and `internal/cli/imagecheck.go` uses it with `bitbucket.GitUsername`. *Consequence:* any move of
the run token into the service must also cover image builds, or leave them on the mounted secret.
Under D363's recommendation nothing moves.

**F9, Gap.** On a person's machine, the CLI has no Bitbucket credential to read with. Operators hold
`secretVersionAdder` and `viewer` on repository secrets, not `secretAccessor`
(`modules/repo/secrets.tf`), so they can write a token but not read one back. *Consequence:* a
local `coordinator status` on a Bitbucket project would need the person's own Bitbucket
credential, which is person-owned like D2's Claude token. The clean local path is to read the view
the sweep already stored. → D364.

**F10, Gap.** The console cannot run `coordinator status`, and P6 is not built. A console child gets
exactly the 11 keys in `CHILD_ENV_KEYS` (`console/src/main/spawn.ts`). Rule 8 of
`docs/design/desktop-console.md` §5.1 says no secret goes in an environment variable the app
writes, so no App key reaches the child. `console/src` names no coordinator operation, and the
console 0.112.0 notes say the project view *"is not in this release"*. *Consequence:* coordinator
P6 depends on a read that needs no key. The sweep's stored view (`coordinator/`, readable by a
signed-in reader) is that read, but no CLI command reads it yet (`coordinator.Store.Read` has no CLI
caller). Building that read makes the console view the same for both providers, and Bitbucket then
becomes purely the sweep's concern. → D364.

**F11, Gap (security).** The service's front door admits runs only. `Authenticator.Verify`
requires a per-repository job's account **and** a live run identity (`verifyRun`, `checkLive`). The
coordinator account, a person, and the coordinator's planned resident service (coordinator P7) are
none of these. *Consequence:* any "the service mints for the coordinator" design adds a second
kind of caller to the service's most security-sensitive file. That caller needs its own
allow-list, its own read-only permission set and its own audit purpose. → D362.

**F12, Exists.** The service already has a pattern for holding a new credential: one bundle secret
(Q6), a `CredentialKey` kind (`internal/credsvc/bundle.go`), `fugaro creds set <name>`
(`internal/cli/creds.go`) and the `creds-bundle` schema (`schemas/creds-bundle.schema.json`). A
`bitbucket-oauth-consumer` entry would follow `github-app-key`'s path. *Consequence:* if D362 picks
the service, the storage half is small. The admission half (F11) is not small.

**F13, Gap.** The credentials service is optional and dormant by default. The App key drop is held,
because `config.ServiceHeldSecretNames` is empty, and Check 96 is written and has not been run.
*Consequence:* a Bitbucket coordinator read that **requires** the service would reach no
installation until the owner opts in and runs those checks. A read held by the coordinator job
(D362's recommendation) works on any installation that has the coordinator on.

**F14, Gap.** A credential's identity is visible to Bitbucket users, and part of the code depends
on it. A `client_credentials` token acts as **the consumer owner's account**, while an access token
acts as a bot. `internal/gitprov/bitbucket/comments.go` (`selfUser`, `Self`) recognizes Fugaro's own
comments by the token's own user (`GET /user`). *Consequence:* this does not matter for the
coordinator's read-only use. It would matter for runs, which is one more reason to keep runs on
access tokens. Owning a consumer through a person's account also ties the read to that person: if
the person leaves, the read stops. → D367.

**F15, Rule / scope.** Fugaro supports Bitbucket **Cloud** only. `bitbucket.DefaultBaseURL` is
`https://api.bitbucket.org/2.0`, and the adapter speaks the 2.0 REST shapes.
`FUGARO_BITBUCKET_API_URL` overrides only the API root, and Data Center's `/rest/api/1.0` is a
different API. *Consequence:* Data Center is out of scope. → D366.

**F16, Gap.** Revocation differs from GitHub. The coordinator revokes each GitHub token it minted
(`github.RevokeInstallationToken`). For Bitbucket, the swagger documents no revocation of a single
`client_credentials` access token (**UNVERIFIED** that none exists). What can be revoked is the
consumer itself: deleting it, or regenerating its secret, in workspace settings. *Consequence:* a
leaked minted token lives out its 1 to 2 hours. A leaked consumer secret lives until an admin
rotates it. That is a manual step for the runbook.

**F17, Exists.** The coordinator already lists Bitbucket repositories.
`internal/infra/firebase.go` carries every provider into `FUGARO_COORDINATOR_REPOS`, Bitbucket
included, and the sweep marks those repositories `StaleUnsupported`. *Consequence:* Bitbucket
support in the sweep needs no inventory change. A Bitbucket repository moves from
`StaleUnsupported` to read once a credential exists.

**F18, Gap.** App passwords no longer work. Since 2026-07-28 every app password has stopped
working (§3). The code never used them: `git grep -i "app password"` under `internal/` finds
nothing. *Consequence:* the docs must never offer app passwords as an option. API tokens are their
replacement, but they are person-owned and cannot be narrowed below a workspace, so they are not a
platform credential either.

## 5. Decisions (open, recommendation first)

| D | Question | Recommendation, and why | Alternative, and what it costs | Owner's answer |
| --- | --- | --- | --- | --- |
| **D360** | Reopen the 2026-10-15 "Bitbucket stays per repository" ruling? (F4) | **Only for read-only use by the coordinator.** A project-wide view needs a project-wide read, and a read-only credential cannot push | **(b) Reopen it for runs too.** That is F7's widening of an agent's reach. **(c) Keep it, and never support Bitbucket in the coordinator.** That contradicts the spec's "for sure" | |
| **D361** | Which Bitbucket credential does the coordinator read with? | **An OAuth consumer created with only `repository` and `pullrequest` read scopes, using the `client_credentials` grant.** It is read-only and short-lived (1 to 2 hours), no plan requirement is stated, and it is the spec's "general credential, specific tokens through it" | **(b) A read-only workspace or project access token.** It needs Premium, lives long, and gives the same read reach. **(c) Grant the coordinator account `secretAccessor` on every repository's `bitbucket-token`.** It needs nothing new from the user, but the coordinator then holds **write** tokens, and the grants cross from the installation root into each repository root | |
| **D362** | Where is the coordinator's Bitbucket credential held? | **Held by the coordinator job itself**, as one project secret mounted only on `fugaro-coordinator`, exactly as `coordinator_app_key` holds the App key. It works without the service (F13) and adds no caller to the service (F11) | **(b) Held in the service bundle**, with a new non-run caller class: a coordinator account allow-list, a read-only mint and a new audit purpose. That is a security-sensitive change to `auth.go`, it works only where the service is on, and the coordinator then depends on the service | |
| **D363** | How do runs get their Bitbucket credential? (F5 to F8) | **Unchanged: one repository access token per repository, mounted as today.** No Bitbucket credential can be narrowed per run, so minting adds nothing a run can safely use | **(b) The service holds each repository's token and hands the run its own.** That is a vault only: the job loses its Secret Manager grant, but the reach and lifetime are the same and the token still reaches the agent. **(c) The service mints from the consumer for runs.** It gives the agent workspace-wide write for 1 to 2 hours (F7). **(d) A push and pull-request proxy in the service.** It is the only shape that narrows below today's reach, and it is a large build. Deferred, as D145 deferred it | |
| **D364** | How do the console and a person's CLI read the project view with no key? (F2, F9, F10) | **A new CLI read of the view the sweep stored** (`coordinator/repos/<slug>/`, signed-in reader), with a runs-only fallback when the coordinator is off. Coordinator P6 is rewritten onto it. It needs no key and is the same for GitHub and Bitbucket | **(b) A runs-only `coordinator status`, with no pull-request half.** It is simpler, but the console shows no pull requests. **(c) The console waits for the coordinator's resident service (P7, P8).** That is later, and it needs a front-door credential | |
| **D365** | With the service on, is the coordinator job's own App key grant kept? (F3) | **Kept, and documented as the second holder** (the sweep runs no agent, so its mount is not an agent-reachable copy). The local `coordinator status` stops needing a key under D364 | **(b) The coordinator mints through the service.** That is D362(b)'s new caller class, for GitHub as well | |
| **D366** | Is Bitbucket Data Center in scope? (F15) | **No.** The code is Cloud-only, and Data Center is a different API | **(b) Include it.** That needs a second adapter for the 1.0 REST API | |
| **D367** | Whose account owns the OAuth consumer? (F14) | **A dedicated workspace member account kept for Fugaro**, so that the read does not stop when a person leaves. Whether that account costs a seat is **UNVERIFIED** | **(b) The installing person's own account.** It is free, but the read stops if that person leaves or loses access | |

The spec's own open question ("maybe … more specific ones per repository, per run") is answered by
§3 and D363: Bitbucket offers no such narrowing, so the specific tokens can exist only for the
coordinator's read-only reach.

## 6. Smallest useful first slice

1. **S1, no credential (D364).** Add a CLI read of the stored view, wire coordinator P6's console
   operation to it, and add a runs-only fallback. This unblocks the console project view for every
   installation with the coordinator on, and it removes the local key requirement. Bitbucket
   repositories show their recorded `StaleUnsupported` state until S2.
2. **S2, Bitbucket in the sweep (D360 to D362, D367).** Add the consumer secret as a project
   secret, the coordinator grant and mount, a `client_credentials` token source in
   `internal/gitprov/bitbucket` for read-only use, and `supported` admitting Bitbucket for the
   sweep's reader only. Docs and a runbook entry cover creating the consumer and rotating it. An
   owner-run live check proves the read on a sandbox workspace.
3. **Later, only if the owner asks:** S3, the service bundle path (D362(b)). S4, the push and
   pull-request proxy (D363(d)).

**Migration.** Under the recommendations, nothing existing moves. Per-repository tokens stay as
they are. The consumer is a new, optional project secret, and an installation without it keeps
today's `StaleUnsupported` rows.

**Threat model (recommended shape).**

- **What a leak gives:** the consumer secret gives read access to every repository and pull
  request its owner can see in the workspace.
- **Who holds it:** the coordinator account, through one `secretAccessor`, and the sweep container
  that mounts it. The sweep runs no agent and no model.
- **Who can ask for a token:** nobody else. There is no endpoint.
- **How it is revoked:** a workspace admin deletes the consumer or regenerates its secret, after
  which a new secret version is written. A minted token expires within 1 to 2 hours.
- **What changes for a hostile agent:** nothing. The consumer never enters a run's job.

## 7. Open tensions and items for the owner

- **T1, the spec against F7.** The spec wants the service to mint per-run Bitbucket tokens, and §3
  shows that any such token is workspace-wide. Ruling for the spec as written means ruling for
  D363(c), which accepts F7's widening of an agent's reach.
- **T2, D362(b) against F13.** Holding the credential in the service makes Bitbucket support depend
  on an optional, dormant component that has not been checked live (Checks 94 to 99 not run).
- **T3, D364 against coordinator Q8(A).** Q8(A) has the console reach data only through CLI reads.
  D364 keeps that rule, but it changes which read P6 uses, so P6's plan text has to be amended.

## 8. Numbers this work holds

**D360–D389** and **Checks 145–154**, taken above the highest on `main` (task governance's
D320–D359 and Checks 135–144) and above every open pull request's docs, none of which writes D334
or higher, or Check 135 or higher. **D360–D367** are written above and open. **D368–D389** and
**Checks 145–154** are reserved and unused. The likely first check is the S2 live read.

## 9. UNVERIFIED

- **U1.** Whether an OAuth token lasts one hour (swagger) or two hours (support page).
- **U2.** Whether the `client_credentials` grant returns a refresh token, and whether a single
  minted token can be revoked.
- **U3.** Whether a `client_credentials` token reaches only the consumer's own workspace or every
  workspace its owner can reach.
- **U4.** Whether OAuth consumers and repository access tokens need any paid plan.
- **U5.** Whether an access token or a consumer's owner uses a paid seat.
- **U6.** Whether a project access token covers repositories added after it was created.
- **U7.** Whether a Forge app can get a token narrowed to a repository.
- **U8.** Whether IP allowlisting applies to access tokens.
- **U9.** Whether a console child can make the D364 read through the operator's own Google
  sign-in, given only `CHILD_ENV_KEYS`. The runs reads the console already makes suggest it can,
  but this is not checked.
