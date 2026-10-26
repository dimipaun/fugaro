# Bitbucket support through the credentials service: reconciliation

**The owner answered decisions D360–D367 on 2026-10-26.** D360, D362, D363 and D367 were answered
against the recommendation. This document is checked against `main` at `ce991d88d` and against
Bitbucket's own documentation as read on 2026-10-26. Where it disagrees with [spec.md](spec.md),
this document wins.

- §1–§4 are the findings as first written, before the answers. F19–F22 were added when the answers
  were followed through.
- §5 holds the decisions with the owner's answers.
- §6 follows the answers through.

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

The shape this document recommended:

- **The coordinator reads Bitbucket** through one read-only credential, held by the coordinator's
  own job the way it already holds the GitHub App key (D361, D362).
- **Runs keep their per-repository access tokens unchanged** (D363).
- **The console's project view reads what the coordinator job already stored**, so neither the
  console nor a person's CLI needs any key (D364). That read needs no credential at all, and it
  is the smallest useful first slice (§6).

**The owner ruled otherwise on 2026-10-26** (§5):

- The credentials service holds the Bitbucket credentials (D362).
- The service mints run tokens as well (D363).
- A run's Bitbucket token is therefore accepted as workspace-wide for 1–2 hours (D360). The
  mitigation is policy and audit in the service (T1).
- The tokens come from OAuth consumers owned by the installing person (D367).

The key-free stored-view read (D364) stands as recommended, and it is still the first slice.

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

### Findings added when the answers were followed through (2026-10-26)

**F19, Gap (security). Comments from the installing person would be taken as Fugaro's own, and
the agent could write as that person.** This follows from D363 and D367 together.

- **What the code does.** `internal/gitprov/bitbucket/comments.go` (`selfUser`) treats every
  comment by the token's own user, read with `GET /user`, as Fugaro's (`gitprov.Comment.Self`).
  `internal/followup/trust.go` (`Trust.Allows`, `isSelf`) never lets a `Self` comment steer a
  follow-up, and `internal/followup/select.go` drops it as `omitSelf`.
- **First consequence.** A `client_credentials` token acts as the consumer's owner, which D367
  makes the installing person. Every comment that person writes by hand on a Bitbucket pull request
  becomes `Self`. **The installing person can then never steer a follow-up.** Every pull request
  Fugaro opens is also authored by that person (`Trust.PRAuthorID`).
- **Second consequence.** Today the agent's token is a bot's. Under D363 it is the person's, so a
  hostile or prompt-injected agent can write comments **as the installing person**.
- **Why the obvious fix is a vulnerability.** The obvious repair is to count a comment as `Self`
  only when it carries Fugaro's run marker. That would make the agent's unmarked comments look like
  the person's own trusted instructions to the next follow-up run. That is a lasting
  prompt-injection path that today's bot identity rules out.
- **Status.** No repair keeps both behaviours, so this is a product decision. It is open item **O1**,
  and plan task T3.3 is held on it.

**F20, Gap (security). The agent could approve and merge as a human, anywhere in the workspace.**

- **What the run needs.** The run consumer needs `pullrequest:write`. That scope gives *"access to
  create, comment, approve, decline, and merge pull requests"* (§3).
- **Consequence.** Under D360, D363 and D367, a run's agent can approve or merge **any** pull
  request in the workspace, **as the installing person**, for the token's life. Branch
  restrictions and merge checks that count that person's approval are satisfied by the agent.
- **How it compares with today.** A repository token can approve as well, but only on one
  repository and only as a bot.
- **Mitigations, outside Fugaro's code.** Do not make the consumer's owner a required approver, or
  a member of any merge-check group. Keep them out of default reviewers.
- **Where it goes.** SECURITY.md (T3.4) must say this plainly, and Check 147 shows it on the
  sandbox. It is part of open item **O1**.

**F21, Resolution. Two consumers are needed, not one.** Bitbucket fixes the scopes on the consumer
and ignores a per-request `scope` (§3). D361 gives the coordinator a **read** consumer, and D363
needs a **write** consumer for runs. One consumer cannot serve both without giving the coordinator
write reach. So the service holds two:

- `bitbucket-read-consumer`, used only for the coordinator's mint;
- `bitbucket-run-consumer`, used only for the run mint.

Both are owned by the installing person (D367). This is the only shape that keeps both answers.
Open item **O2** asks the owner to confirm it.

**F22, Gap (security). Until the per-repository token leaves the job, S3 widens and shortens
nothing.**

- **The token stays in the job.** The job's `bitbucket-token` mount, read by
  `internal/gitprov/providers/providers.go` (`EnvBitbucketToken`), stays in the job until it is
  dropped. The same is true of the App key: `config.ServiceHeldSecretNames` is empty, and the
  credentials-service rules file keeps it empty until the owner's checks pass.
- **Consequence.** A run on S3 holds **both** a long-lived token for one repository (through
  `/proc`) and a short-lived token for the whole workspace.
- **Why the drop is not enough.** The image build needs the per-repository token whatever happens
  (F8), so the token cannot simply be deleted.
- **What the plan does.** Task T3.6 writes the gated drop, and the owner flips it after Check 147.
  Open item **O3**.

## 5. Decisions, with the owner's answers

| D | Question | Recommendation, and why | Alternative, and what it costs | Owner's answer |
| --- | --- | --- | --- | --- |
| **D360** | Reopen the 2026-10-15 "Bitbucket stays per repository" ruling? (F4) | **Only for read-only use by the coordinator.** A project-wide view needs a project-wide read, and a read-only credential cannot push | **(b) Reopen it for runs too.** That is F7's widening of an agent's reach. **(c) Keep it, and never support Bitbucket in the coordinator.** That contradicts the spec's "for sure" | **2026-10-26: (b), reopened for runs too. Not the recommendation.** The owner wants one Bitbucket path through the service. Accepted consequence: a hostile agent's Bitbucket token reaches the whole workspace for 1–2 hours instead of one repository (F7, F20). Followed through in T1 and in S3 |
| **D361** | Which Bitbucket credential does the coordinator read with? | **An OAuth consumer created with only `repository` and `pullrequest` read scopes, using the `client_credentials` grant.** It is read-only and short-lived (1 to 2 hours), no plan requirement is stated, and it is the spec's "general credential, specific tokens through it" | **(b) A read-only workspace or project access token.** It needs Premium, lives long, and gives the same read reach. **(c) Grant the coordinator account `secretAccessor` on every repository's `bitbucket-token`.** It needs nothing new from the user, but the coordinator then holds **write** tokens, and the grants cross from the installation root into each repository root | **2026-10-26: the recommendation**, a read-scoped OAuth consumer. It is read-only and needs no Premium. Because D363 needs a second consumer with write scopes, see F21 |
| **D362** | Where is the coordinator's Bitbucket credential held? | **Held by the coordinator job itself**, as one project secret mounted only on `fugaro-coordinator`, exactly as `coordinator_app_key` holds the App key. It works without the service (F13) and adds no caller to the service (F11) | **(b) Held in the service bundle**, with a new non-run caller class: a coordinator account allow-list, a read-only mint and a new audit purpose. That is a security-sensitive change to `auth.go`, it works only where the service is on, and the coordinator then depends on the service | **2026-10-26: (b), held by the credentials service. Not the recommendation.** The answer reached this document labelled "recommended". It is recorded here as it was given, and it differs from §5's recommendation (open item O5). Why: one place holds every Bitbucket credential. Consequences: a new coordinator caller class at the service (F11, task T2.3), and a Bitbucket coordinator read only where the optional service is deployed (F13, tension T2) |
| **D363** | How do runs get their Bitbucket credential? (F5 to F8) | **Unchanged: one repository access token per repository, mounted as today.** No Bitbucket credential can be narrowed per run, so minting adds nothing a run can safely use | **(b) The service holds each repository's token and hands the run its own.** That is a vault only: the job loses its Secret Manager grant, but the reach and lifetime are the same and the token still reaches the agent. **(c) The service mints from the consumer for runs.** It gives the agent workspace-wide write for 1 to 2 hours (F7). **(d) A push and pull-request proxy in the service.** It is the only shape that narrows below today's reach, and it is a large build. Deferred, as D145 deferred it | **2026-10-26: (c), the service mints run tokens from the consumer. Not the recommendation.** Why: the spec's "per run" tokens, made from one general credential. Mitigation (T1): the service mints only for allow-listed repositories, writes an audit line for every mint, and SECURITY.md and the docs say plainly that a Bitbucket run token is workspace-wide for the consumer's owner. The run consumer's scopes are the minimum runs need, set in a task and proven by a check (T3.5, Check 147). Followed through in F19–F22 |
| **D364** | How do the console and a person's CLI read the project view with no key? (F2, F9, F10) | **A new CLI read of the view the sweep stored** (`coordinator/repos/<slug>/`, signed-in reader), with a runs-only fallback when the coordinator is off. Coordinator P6 is rewritten onto it. It needs no key and is the same for GitHub and Bitbucket | **(b) A runs-only `coordinator status`, with no pull-request half.** It is simpler, but the console shows no pull requests. **(c) The console waits for the coordinator's resident service (P7, P8).** That is later, and it needs a front-door credential | **2026-10-26: the recommendation.** The read is key-free for both providers. Console P6 is rebuilt on it: this folder's plan amends P6's plan text, and the coordinator's own files are not edited |
| **D365** | With the service on, is the coordinator job's own App key grant kept? (F3) | **Kept, and documented as the second holder** (the sweep runs no agent, so its mount is not an agent-reachable copy). The local `coordinator status` stops needing a key under D364 | **(b) The coordinator mints through the service.** That is D362(b)'s new caller class, for GitHub as well | **2026-10-26: the recommendation.** The grant is kept and documented as the second holder of the key |
| **D366** | Is Bitbucket Data Center in scope? (F15) | **No.** The code is Cloud-only, and Data Center is a different API | **(b) Include it.** That needs a second adapter for the 1.0 REST API | **2026-10-26: the recommendation.** Out of scope |
| **D367** | Whose account owns the OAuth consumer? (F14) | **A dedicated workspace member account kept for Fugaro**, so that the read does not stop when a person leaves. Whether that account costs a seat is **UNVERIFIED** | **(b) The installing person's own account.** It is free, but the read stops if that person leaves or loses access | **2026-10-26: (b), the installing person. Not the recommendation.** Recorded consequences: every token acts as that person, so pull requests, comments, approvals and merges show their name (F19, F20). Both consumers stop working when the person leaves or loses access. Rotation and ownership transfer must be a documented operator step and a task (T2.6, Check 148) |

The spec's own open question asked whether there could be "more specific ones per repository, per
run". §3 and D363 answer it. Bitbucket offers no such narrowing, so a "per-run" token is
per-run only in **who receives it and when**, which the service enforces and audits. It is not
per-run in **what it can reach**.

## 6. The answers followed through

**Shape.** The service's bundle holds two OAuth consumers (F21), both owned by the installing
person (D367). The service gives out two kinds of short-lived token:

- **Read tokens for the coordinator job.** This is a new caller class at the service, for the
  coordinator's account only (D362, F11).
- **Write tokens for runs.** These go only to an allow-listed repository's own job with a live run
  identity, and every mint is audited (D363, T1).

The console and the CLI read the stored view and hold no key (D364). The coordinator job keeps its
own App key grant for GitHub (D365).

**Slices.** The full plan is in [implementation-plan.md](implementation-plan.md).

- **S1** needs no credential: the stored-view read, and console P6 rebuilt on it.
- **S2** brings Bitbucket into the coordinator job through the service's read consumer.
- **S3** has the service mint Bitbucket run tokens, with an allow-list, an audit line per mint, the
  minimum scopes, SECURITY.md, and the gated drop of the job's own per-repository token.
- **Owner-run checks** are Checks 145–149.

**Migration.** Per-repository `bitbucket-token` secrets stay in place:

- Image builds keep using them (F8).
- Runs on an installation without the service keep using them.
- Runs on an installation with the service keep the mounted copy until the owner flips T3.6's gate.

To opt in, a repository is added to the bundle's allow-list and the two consumers are written to
the bundle with `fugaro creds set`. An installation that has not opted in sees no change.

**Threat model, as answered.**

| Asset | Who can obtain it | What it reaches | How it is revoked |
| --- | --- | --- | --- |
| Read consumer (key and secret) | The service, from the bundle. The owner writes it | Read access to every repository and pull request the installing person can see (U3: maybe beyond the workspace) | A workspace admin deletes the consumer or regenerates its secret, then writes it again with `fugaro creds set` |
| Write consumer (key and secret) | The service, from the bundle | **Write access to everything the installing person can write**, within the consumer's scopes, including approving and merging (F20) | The same |
| Read token (1–2 hours) | The coordinator's account through the service's new caller class, audited | The same as the read consumer | It expires. Whether a single token can be revoked is U2 |
| Run token (1–2 hours) | The job of an **allow-listed** repository with a **live run identity**, audited per mint. **Its agent can read it** (F7) | **The whole workspace, as the installing person**, for its life | It expires. U2. Rotating the consumer is the emergency stop |

**What a hostile agent can do, precisely.** Take an agent in an allow-listed repository's run.
For up to 1–2 hours, and as the installing person, it can:

- push to any branch the person may push to, in any repository of the workspace;
- open, comment on, approve, decline and merge pull requests there (F20);
- write comments that read as the person's own (F19).

The service's allow-list decides **which runs** receive such a token. It does not decide **what
the token does**. The audit line names the run that received each token, so misuse can be traced
to a run but not prevented. Neither the per-repository policy nor the audit narrows what a token
can reach.

## 7. Open tensions and items for the owner

- **T1, now accepted with mitigation (D360, D363, 2026-10-26).** The spec's per-run tokens are
  workspace-wide. The owner accepted the widening. The mitigation is: an allow-list at the
  service, an audit line per mint, minimum scopes (T3.5, Check 147), plain statements in SECURITY.md
  and the docs (T3.4), and the job token's gated drop (T3.6). **The mitigation decides who
  receives a token. It does not narrow what the token reaches.**
- **T2, open (D362 against F13).** Bitbucket support in the coordinator and in runs depends on the
  optional credentials service, which ships dormant. Its live Checks 94–99 have not been run, and
  91, 92, 94, 95, 96 and 99 are written and NOT RUN. S2 and S3 are useless to an installation that
  has not opted in, and the plan does not ask for those checks to run first. The owner may want to
  make Check 99 a precondition of S3.
- **T3, open (D364 against coordinator Q8(A)).** Q8(A) puts the console's data behind CLI reads,
  and D364 keeps that rule, but P6 now reads the stored view and not `coordinator status`. This
  folder's plan carries the amended P6 text (task T1.2). `docs/coordinator/` is not edited.
- **O1, confirmation request (F19, F20).** D363 and D367 together let a run's agent write as the
  installing person. That person's own comments can then never steer a follow-up, and the agent
  can approve or merge as them across the workspace. A fix that counts only marked comments as
  `Self` would open a prompt-injection path. **Options:**
  - (a) Keep `Self` as the token's user, and document that the installing person cannot steer a
    Bitbucket follow-up.
  - (b) Use a dedicated account for the run consumer only, which reverses D367 for runs.
  - (c) Keep D363 off until (a) or (b) is chosen.

  Task T3.3 is held on this.
- **O2, confirmation request (F21).** Two consumers, read and run, both owned by the installing
  person.
- **O3, owner gate (F22).** The job's own `bitbucket-token` stays mounted until the owner flips
  T3.6's gate after Check 147. Image builds keep it in any case (F8).
- **O4, a precondition for any mint.** Check 145 (U1, U2, U3, U5, U8) runs on a sandbox workspace
  before T3.1 merges. If it shows that a `client_credentials` token reaches beyond its workspace
  (U3), the owner rules again on D363 before S3 continues.
- **O5, recorded.** D362's answer was relayed to this document as "recommended", but it is
  option (b) and not this document's recommendation. It is recorded as given, marked as not the
  recommendation.

## 8. Numbers this work holds

**D360–D389** and **Checks 145–154**, taken above the highest on `main` (task governance's
D320–D359 and Checks 135–144) and above every open pull request's docs, none of which writes D334
or higher, or Check 135 or higher.

- **D360–D367** are written and answered on 2026-10-26. **D368–D389** are reserved and unused.
- **Checks 145–149** are proposed by the plan; all are owner-run and NOT RUN. **150–154** are
  reserved and unused.

## 9. UNVERIFIED

Every item below that matters for minting is settled by **Check 145** on a sandbox workspace before
the first slice that mints (S2's T2.3 and S3's T3.1).

- **U1.** Whether an OAuth token lasts one hour (swagger) or two hours (support page). Check 145.
- **U2.** Whether the `client_credentials` grant returns a refresh token, and whether a single
  minted token can be revoked. Check 145.
- **U3.** Whether a `client_credentials` token reaches only the consumer's own workspace or every
  workspace its owner can reach. Check 145, using a second sandbox workspace the person belongs to.
- **U4.** Whether OAuth consumers and repository access tokens need any paid plan. Check 145 runs
  on the sandbox's plan.
- **U5.** Whether an access token or a consumer's owner uses a paid seat. Check 145 records the
  workspace's seat count before and after.
- **U6.** Whether a project access token covers repositories added after it was created. Not on
  the plan's path.
- **U7.** Whether a Forge app can get a token narrowed to a repository. Not on the plan's path; if
  it can, that would be a narrower alternative to D363.
- **U8.** Whether IP allowlisting applies to OAuth tokens. Check 145 records it, if the sandbox
  plan offers allowlisting.
- **U9.** Whether a console child can make the D364 read. The CLI half uses the person's ADC
  (`internal/cli/cloud.go` `newBudgetClient`), so the open question is whether a console child has
  ADC. Check 149.
- **U10.** Whether `git` accepts an OAuth access token with the username `x-token-auth`. Bitbucket
  documents this form for access tokens, but not for OAuth tokens on this page. Check 147.
- **U11.** Whether the token endpoint's response lists the granted scopes, which T3.5 would assert.
  If it does not, T3.5 falls back to the consumer's configured scopes. Check 145.
