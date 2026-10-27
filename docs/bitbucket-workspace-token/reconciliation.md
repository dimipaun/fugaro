# Bitbucket workspace and project tokens: reconciliation

Checked against `main` at `a5f521f1c` and against Atlassian's own pages as read on 2026-10-27.
Where this document disagrees with [spec.md](spec.md), this document wins.

## 1. Summary

The spec asks that an operator with 50 Bitbucket repositories should not have to create, and type,
50 repository access tokens. The owner then ruled (§5) that Bitbucket repositories should also run
in **shared project jobs**, as GitHub repositories have since phase 3. On 2026-10-27 the owner also
ruled that **0.12.1 must be quick**: anything large moves to 0.13.0.

The headline finding is that **most of the first half already exists** (F1–F4):

- `fugaro secrets set bitbucket-token --project-wide` already stores one value for the whole
  installation. It needs a `project_secrets.bitbucket-token` allow-list in the operator's local
  config.
- `fugaro update --repo` already skips the token prompt for an allow-listed repository whose
  project copy has a version.
- Every per-repository job and image build of such a repository already mounts and clones with the
  project copy.

That path is tested in `internal/infra` and `internal/cli`. What is missing for that half is
**words, not code**. The docs, `SECURITY.md`, the CLI's help and the setup skill all describe the
path as an exception "for an operator who has such a workspace token". They do not present it as
the supported way to run many repositories on one Bitbucket workspace or project access token.
They also describe the reach risk only in general terms.

So the 0.12.1 slice is **genuinely small** (§6): one docs task and one CLI-text task, with no
change of behaviour, and one or two runs. Bitbucket in shared project jobs is a real code change.
It touches a security boundary, the deploy-time check, the launcher and phase 3's test pins, so it
goes to **0.13.0**, with its live checks.

## 2. What the spec names, and what the repository already has

| Spec concept / owner ruling | What exists | Where |
| --- | --- | --- |
| Store one Bitbucket token for many repositories | **Yes.** `fugaro secrets set bitbucket-token --project-wide` stores the project's copy, but only when `project_secrets.bitbucket-token.allow_data_to` lists the repositories. Without an entry the name keeps repository scope | `internal/cli/secrets.go` `projectScopeName` and the `secrets set` help; `internal/config/project_secrets.go` `PerRepositorySecret`, `ProjectScopeByDefault`, `AllowedForProjectSecret` |
| Enrol N repositories with no per-repository prompt | **Yes, per repository.** `secretsStage.find` treats a name as covered when `infra.ProjectSecretCandidate` allows it for the repository and the project copy has an enabled version. The same function decides where the stage stores a value (`projectScope`). Each repository is still enrolled by its own `fugaro update --repo` from its checkout | `internal/cli/initsecrets.go` `find`, `projectScope`; `internal/cli/update.go` |
| A per-repository job mounts the project copy | **Yes.** The repository module's `local.secrets` merges `repo.project_secrets` over the repository's own containers. Readiness resolves a repository copy with a version first, then the project copy (`infra.ResolveSecretID`). Mounts read `version = "latest"` | `deploy/terraform/gcp/modules/repo/workflows.tf`; `modules/workflow/job.tf`; `internal/infra/spec.go` `ProjectSecretCandidate`, `projectSecretCandidates` |
| The image build clones with the project token | **Yes.** `modules/repo/build.tf` grants the build account the container that Readiness resolved, so a project copy is granted when it is the resolved one. `gitSecretID` and `resolveGitSecretID` resolve the same ID from live state and refuse drift | `deploy/terraform/gcp/modules/repo/build.tf`; `internal/cli/gitsecret.go`; `internal/backend/gcp/build.go` `GitSecretID` |
| A per-repository copy overrides the project copy | **Yes**, on per-repository jobs and builds. Disabling the copy's last enabled version is the only way back | `internal/infra` `ResolveSecretID`; `secrets set` help |
| Tests for the project-copy path | **Yes.** `TestRepoEngineBuildsOnAProjectWideGitCredential` (`internal/cli/init_repo_stage_test.go`: an allow-listed repository with a project copy is not prompted and builds; the slice-1 regression pin), `TestInitRepoProjectSecretUnlistedRepoIsMissing` (the negative case), `TestInitRepoRepoCopyStillWinsAndIsReady`, and `internal/infra/projectsecret_coverage_test.go` (project copy covers `bitbucket-token`; disabled versions do not) | as named |
| Bitbucket in shared project jobs | **No, by ruling.** `SharedJobProvider` is GitHub only (`sharedJobProviders = []string{"github"}`). `ProjectJobs` skips a Bitbucket profile **before** `projectJobSecrets` runs, so a Bitbucket profile that lists `bitbucket-token` is accepted and silently gets no job. `projectJobSecrets` refuses `bitbucket-token` only on a GitHub profile. `projectpolicy.validProfiles` accepts both (corrected in review round 1) | `internal/infra/projectjobs.go` |
| How the token authenticates | Git uses the username `x-token-auth` (`bitbucket.GitUsername`). The REST API uses `Authorization: Bearer <token>`. The adapter is built with `Workspace: owner, Slug: name` from the run's repository and does not care which kind of access token it holds | `internal/gitprov/bitbucket/bitbucket.go`; `internal/gitprov/providers/providers.go` `FromEnv` |
| The agent's `FUGARO_GIT_TOKEN` on Bitbucket | It is the mounted token's own value, by design. So with a project copy, the agent holds the workspace or project token | `docs/git-providers.md` "What the agent gets" |

## 3. Bitbucket Cloud's workspace and project access tokens (read 2026-10-27)

| Fact | Status | Source |
| --- | --- | --- |
| A workspace access token reaches *"a single workspace and any projects/repositories under that workspace"* | Verified | support.atlassian.com/bitbucket-cloud/docs/workspace-access-tokens/ |
| A project access token reaches *"a single project and any repositories in that project"*, and *"can't be used to manage or interact with workspaces or other projects"* | Verified | …/project-access-tokens/ |
| Both are **Premium** features | Verified | both pages |
| Both "can have an expiry date". Expiry is optional unless an admin requires it | Verified (the optional part comes from the access-tokens page cited in docs/bitbucket-credentials §3) | both pages |
| Scopes are fixed when the token is created | Verified | both pages |
| Git over HTTPS uses `x-token-auth`, and the API uses a Bearer header | Verified **for repository access tokens** (the page's examples use one). That project and workspace tokens use the same mechanism is **UNVERIFIED** (U1) | …/using-access-tokens/ |
| Commits are matched to a `{botid}@bots.bitbucket.org` identity, and the token is "connected to a repository, not a user" | Verified for repository tokens. The identity of a project or workspace token is **UNVERIFIED** (U2) | …/using-access-tokens/ |
| Whether a project token covers a repository added to the project later | **UNVERIFIED** (U3). The page's wording suggests it does | …/project-access-tokens/ |
| At most 25 access tokens per repository, project and workspace | Verified 2026-10-26 | docs/bitbucket-credentials/reconciliation.md §3 |
| Whether a token can be narrowed to a set of repositories | Not offered: the pages describe the whole workspace or the whole project only | both pages |

## 4. Findings

**F1, Exists.** The spec's request is already supported. Allow-list the repositories in
`project_secrets.bitbucket-token.allow_data_to`, store one workspace or project token with
`fugaro secrets set bitbucket-token --project-wide`, and every listed repository's
`fugaro update --repo` finds the name covered and asks for nothing (`initsecrets.go` `find`).
*Consequence:* the 0.12.1 slice is docs and CLI text. It needs no new mechanism.

**F2, Exists.** The image build path is already correct for a project copy. `modules/repo/build.tf`
grants the build account the resolved container, and `resolveGitSecretID` refuses a build whose
grant and resolution disagree. *Consequence:* no image-build task is needed in either release. A
shared project job runs the base image (`ProjectJobs`, `Image: lc.BaseImage(config.BaseKind)`) and
clones at run time, so it involves no Cloud Build clone either.

**F3, Exists.** Rotation is one write for N repositories. Every mount reads `version = "latest"`
(`modules/workflow/job.tf`, `modules/project/job.tf`), so the next execution of every repository
reads the new version. Executions already running keep the old value until they end.
The daily image-check job's base must be at or after `gitSecretIDSince` (0.7.5) for the project copy: a release ref below it is refused, and a non-release ref gets a warning (`init.go` `checkCheckImageKnowsGitSecretID`; narrowed in review round 2). *Consequence:* the documented order is: create the new token, store it as a new version, wait for
running executions to end, then revoke the old token in Bitbucket.

**F4, Exists.** Migration from per-repository tokens needs nothing new. A repository's own copy with
an enabled version still wins over the project copy (`ResolveSecretID`). The deployed job's mount is
fixed at apply time by Readiness (`MountSecretID`). So to move a repository onto the project token,
disable its own copy's versions **and then run `fugaro update --repo --allow-delete` for it** (the
re-apply replaces the job's IAM member, which is a delete; `docs/multi-model.md`; flag added in
review round 3). Until that update,
its job still mounts its own container, whose `latest` is disabled, and its runs fail. This is what
`docs/multi-model.md` already says for any secret (corrected in review round 1). The owner asked to
keep the override.
*Consequence:* the docs state both directions, plus one caveat: on a **shared** job the override
does not apply (F11).

**F5, Rule.** The docs say the opposite of what the owner now wants, and tests pin those words:

- `docs/git-providers.md` §Bitbucket Cloud ("never stored project-wide" in the variables table;
  "So Fugaro stores it per repository"; "**Bitbucket stays on the per-repository path**"; "Phase
  3's shared project jobs serve GitHub repositories").
- `SECURITY.md` "Narrow git credentials", which `TestDocsWarnAboutAProjectWideGitCredential` pins.
- The same text in `website/content/security.md`.
- `docs/design/project-centric.md` §4.1 ("GitHub only") and §4.5 (D5).
- `docs/design/project-secrets.md` (the "Stated, not implied" paragraph).
- `docs/project-layer.md` ("Bitbucket repositories never reach one").
- `docs/gcp-live-checklist.md` Check 52 §2 (the U1 ruling), pinned by
  `internal/cli/docs_project_centric_test.go` ("Bitbucket stays on the per-repository path",
  "**Phase 3's shared project jobs serve GitHub repositories.**").
- `docs/plans/2026-10-14-project-centric.md` (D5 amendment).
- `docs/releases/v0.9.0.md` (released notes: historical, never edited).
- Statements that a Bitbucket token is a repository token (review round 1): `docs/git-providers.md`
  "What the agent gets" ("this repository's own long-lived token") and "For Bitbucket this is the
  repository access token itself"; `SECURITY.md` and `website/content/security.md` "Give Fugaro its
  own … a repository access token on Bitbucket"; `README.md`'s Bitbucket line; `docs/multi-model.md`
  ("a repository access token serves one repository", pinned by
  `TestDocsSayTheInitPromptHasNoRepoFlagAndBitbucketIsPerRepository`); `docs/design/project-centric.md`
  §6.3; `docs/coordinator/reconciliation.md` (historical, left as is). In code comments and messages:
  `config.BitbucketSecret`, `infra.GitSecret` ("never stored project-wide", already false),
  `config.ServiceHeldSecretNames`, the `providers.FromEnv` error ("a Bitbucket repository access
  token"), `projectpolicy`'s `providerRequired` refusal ("a Bitbucket profile never shares a
  project-wide credential"), and `coordinator.ErrBitbucket` (out of scope: the coordinator read is
  docs/bitbucket-credentials').
- Found in review round 2: `internal/cli/docs_credentials_github_tokens_test.go` pins "**On
  Bitbucket it IS this repository's access token**" and "never the App's private key and never
  another repository's token"; `website/src/pages/getting-started.astro` ("a repository access
  token on Bitbucket"); `SECURITY.md`'s "project scope is the default" paragraph and its review-mode
  premise ("pushing one already needs write access", which F17 says a workspace token breaks); the
  `bitbucket.New` error "a repository access token is required"; `docs/bitbucket-credentials/`
  (D363's record, pinned by `docs_bitbucket_credentials_test.go`, gains a dated note that D462
  reverses it for opted-in operators); `docs/coordinator/README.md`, whose "highest check section
  written" pin moves when Check 180 is written; and, inside the phase-3 paragraph of
  `docs/git-providers.md`, the sentence "the project-wide token an earlier design assumed therefore
  does not exist … nothing to configure for one", which is a slice-1 statement and is rewritten by
  T1.1. The Check 52 pins in `docs_project_centric_test.go` also **ban** "project access token" and
  "bitbucket.org" inside that section, which constrains T2.6's dated note.
- The generated pages `website/content/security.md` and `website/content/cli-reference.md` (from
  `secrets set` and `secrets ls` help) are regenerated by `go run ./internal/clidoc`, and
  `internal/clidoc`'s tests hold them current.
- `plugin/skills/setup/SKILL.md` and `reference/decisions.md`, pinned by
  `TestSetupSkillStoresTheGitCredentialWithoutRepo` (which **fails** if the setup skill writes
  `secrets set bitbucket-token --project-wide`) and
  `TestDocsSayTheInitPromptHasNoRepoFlagAndBitbucketIsPerRepository`.
- In code, the comments and refusal text of `config.PerRepositorySecret`, `secrets set`'s help, and
  `infra.ProjectJobs`, `SharedJobProvider` and `projectJobSecrets`. Their tests are
  `internal/infra/projectjobs_test.go` (`TestSharedJobProviderIsTheProvidersList` and the Bitbucket
  skip and refusal cases), the launcher test in `internal/cli/project_job_test.go` that asserts a
  Bitbucket profile keeps the per-repository job, and `internal/runner`
  `TestBitbucketUnchanged`, which stays true.

*Consequence:* the 0.12.1 tasks rewrite the slice-1 statements (T1.1 the docs, T1.2 the code text). The phase-3 statements are
rewritten **only by the 0.13.0 task that changes the code**, so that no doc says shared Bitbucket
jobs exist before they do. Historical records (Check 52's FACT, the 0.9.0 notes, the
2026-10-14 plan) are amended by a dated note, not rewritten.

**F6, Rule.** The 2026-10-15 ruling's premise was that "a Bitbucket credential cannot be scoped to
one project's repositories". A **Bitbucket project** access token is scoped to the repositories of
one *Bitbucket* project. That matches a Fugaro project whenever the operator groups the Fugaro
project's repositories in one Bitbucket project. The premise holds only for a Fugaro project that
spans Bitbucket projects. *Consequence:* the docs recommend a project token where it fits, and a
workspace token only otherwise (D461).

**F7, Gap (security).** The allow-list limits **which jobs mount** the token. It does not limit
**what the token reaches**. Every allow-listed repository's run hands its agent the token's own value
(`FUGARO_GIT_TOKEN`). So a hostile or prompt-injected agent in one repository can, for the token's
life, do whatever the token's scopes allow in **every repository the token reaches**. That includes
repositories that are not enrolled in Fugaro at all. It can:

- push to their branches, including `bitbucket-pipelines.yml`, which triggers their Pipelines with
  their secured variables (docs/bitbucket-credentials F26);
- open and comment on pull requests there;
- possibly approve and merge them (U4).

**Every allow-listed repository's other credentials too** (review round 2). With
`repository:write`, A's agent can push to B's base branch or to B's Fugaro pull-request branches.
That covers `fugaro.yaml`, build and test scripts, and the Dockerfile. B's next run or image build
then executes that code with every credential B's jobs hold: B's own provider keys, B's Claude
account token, workflow `secrets:` and build secrets. This holds already in slice 1.

**Launchers too, not only agents** (review round 1, carrying docs/bitbucket-credentials F27
forward). Anyone who may launch a run of an allow-listed repository can override the job's
environment on `jobs.run` (`SECURITY.md`, "A launcher can override a job's environment"). That
includes `FUGARO_BITBUCKET_API_URL`, which `providers.FromEnv` passes unchecked to `bitbucket.New` as
the API root. So a launcher can have the runner send the workspace token to a host of their own, or
simply instruct the agent to print it. This holds already in slice 1, on per-repository jobs. Today
the same launcher gets one repository's token. With the opt-in, the launcher gets the whole token's
reach. → D474.

The owner accepted this risk for agents (D462); D475 (answered yes on 2026-10-27) extends the
acceptance to launchers. *Consequence:* `SECURITY.md` and `docs/git-providers.md` say
it in those words. The docs recommend the narrowest token (project over workspace) and the minimum
scopes (D470). A token does not expire on its own unless the operator sets an expiry.

**F8, Gap.** The PR author is the token's identity on every repository. Bitbucket shows the token's
name as the author of every pull request and comment (`docs/git-providers.md` "Name the token").
With one token, every enrolled repository's pull requests carry one name. `GET /user` with a
repository access token was refused live (HTTP 403, 2026-09-30), so Fugaro's own identity is
unknown and its own comments are dropped as the PR author's. For a workspace or project token this
is **UNVERIFIED** (U2). *Consequence:* the trust rule is unchanged. A hostile agent in repository A
can post comments on repository B's Fugaro pull request as the same bot. A follow-up of B drops them
as the PR author's own, so they do not become B's instructions. When the identity is unknown,
markers are honoured from every author, as they already are today.

**F9, Gap.** `project_secrets` is operator-local and is never published. `localcfg.Config.Shared`
drops it, and `sharedcfg.go` `sharedForbidden` refuses it, because publishing it would let a
runs-bucket writer widen a credential's mount. So a second operator's machine that got its local
config from `fugaro update --gcp-project` has no `bitbucket-token` entry. Its `fugaro update --repo`
then reads the name as repository-scoped and **prompts for a per-repository token**. If a value is
typed, that repository's own copy then wins over the project copy forever. What its plan does to the
existing mount before Readiness stops it is **UNVERIFIED** (U5). This applies today to every
`project_secrets` entry. *Consequence:* 0.12.1 documents "copy the entry to every operator's local
config". Anything more is D467.

**F10, Gap.** The allow-list is an explicit list of `owner/name` slugs. `repoSlugRE` refuses `*`
deliberately, and "absent or empty means nobody" holds. So 50 repositories are 50 lines, typed once
in the local config. Typing a line is not a token round trip, so it meets the spec's need, but it is
not zero work. *Consequence:* D464 (a workspace pattern) is a 0.13.0 candidate, not 0.12.1.

**F11, Gap (security), 0.13.0.** A shared project job does **not** honour `allow_data_to` for what it
mounts. The shape of admission makes a plain "the profile's list ⊆ the allow-list" check unsound
(review round 1):

- `allow_repos` entries take wildcards (`projectpolicy.MatchRepo`) and are written as
  `bitbucket/owner/name`, while `allow_data_to` holds exact `owner/name` slugs.
- A profile with no `allow_repos` of its own falls through to the project's list. That list lives
  only in the published node, so `infra.ProjectJobs` never sees it.
- `fugaro project repos add` (`runProjectReposEdit`) writes the node's `allow_repos` without going
  through publish or deploy.
- A check that runs only in the CLI is skipped by an older binary.

**A precedent exists** (review round 2): the policy node already carries an allow-list derived from
`project_secrets`. `projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets)`
publishes `accounts.<n>.allow_repos`. The runner enforces it (`checkProjectJobAccount`), and
`validProfileAccounts` already requires an own exact `allow_repos` for account profiles. So a
derived Bitbucket block in the node is the existing pattern, not a new one, and it is unlike the
shared config `sharedForbidden` guards, because the node is IAM-only.

Beyond that, the `projectJobSecrets` comment says the entry decides that the container exists, and the
profile decides who mounts it (D2). The policy's profile admits repositories, and the runner
enforces that admission (`admissionCheck`, before any git credential). It never sees the
allow-list, which is local only. A Bitbucket shared job must therefore be prevented, **at deploy and
at policy publish**, from admitting a repository the allow-list does not name. Otherwise the policy
alone (IAM-only writes) decides who gets the workspace token. Also, a repository's **own**
`bitbucket-token` copy is never mounted on a shared job, which mounts project containers only. So
the "per-repository copy overrides" rule (F4) does not hold there. *Consequence:* D465 (recommendation amended in review round 1, before any answer), D466, and
tasks T2.1, T2.2, T2.3 and T2.4.

**F12, Gap, 0.13.0.** The isolation of a shared job, stated plainly. On GitHub a shared job's runs
share the job's service account and bucket condition, but each run's **git token** is minted for its
one repository. On Bitbucket all runs of the job hold **the same** workspace or project token. So
the shared job adds no git reach beyond F7: per-repository jobs that mount the project copy already
hand every agent that token. What it adds is phase 3's existing trade, the shared service account
and the shared bucket condition across the profile's repositories, now for Bitbucket repositories
too. *Consequence:* the profile is the boundary. `SECURITY.md`'s existing shared-job text applies
unchanged, and D462's statement covers the token.

**F13, Exists.** The runner needs no Bitbucket change for a shared job. `FromEnv` builds the adapter
for the run's own repository from `FUGARO_BITBUCKET_TOKEN`. `jobProviderKinds` already lists
`bitbucket`. The remote-host binding already handles "a bitbucket job" (`remoteHost`).
*Consequence:* the 0.13.0 work is in `internal/infra`, `internal/cli` and the docs. The runner gains
tests, not code (T2.3).

**F14, Exists.** The credentials service is untouched. It holds no Bitbucket credential (D145), and
`Authenticator.Verify` refuses shared project jobs. The coordinator's Bitbucket read is
docs/bitbucket-credentials' OAuth read consumer (D361, D362). The coordinator does not use this
token, and this work does not make it.

**F15, Gap.** The 0.12.1 docs would describe a path that has never been run live with a workspace or
project token. Fugaro's Bitbucket live checks used a repository access token
(`docs/git-providers.md`: 2026-09-27 and 2026-09-30). Whether a sandbox Bitbucket workspace on the
**Premium** plan exists to run Check 180 is **UNVERIFIED** (U6). *Consequence:* tension T1.

**F16, Gap.** Enrolment is still one `fugaro update --repo` per repository, run from its checkout,
with its own `fugaro.yaml`. That is how every repository is onboarded, whatever its provider, and it
is not a token question. *Consequence:* out of scope. It is listed so that "no per-repository
prompt" is not read as "no per-repository step".

**F17, Gap (security).** Cross-repository trust paths (review round 1, carrying
docs/bitbucket-credentials F28 forward). With a workspace or project token, an agent in repository A
can push a branch to repository B and open a pull request there. A review-only run on Bitbucket
refuses every fork and reviews a same-repository pull request with no author check, *because pushing
a branch there already needs write access* (`internal/runner/reviewmode.go`; corrected in review
round 2). A workspace token breaks that premise. So a review-only run of B trusts that pull request,
and that run holds the same token. A comment the token posts on B also carries the token's identity. That identity steers
B's follow-up wherever an operator has put it in B's `followup.trusted`. *Consequence:* the
`SECURITY.md` statement (T1.1) names both, and the docs tell operators not to put the token's own
account ID in `followup.trusted`. Any code mitigation is a new behaviour → O5.

**F18, UNVERIFIED (security).** An image build hands the git credential to BuildKit as a secret
(`images/derived/cloudbuild.yaml`, `--secret id=git-credentials`). Whether a Dockerfile step under
the repository's control can read it is unexamined (U8). If one can, a repository's own Dockerfile
reaches the workspace token at build time as well as at run time.

## 5. Decisions

**Block.** D460–D489 and Checks 180–189 (§8).

**The owner's rulings of 2026-10-27**, given through `AskUserQuestion` and relayed. A later round
the same day answered D474, D475 and D477 in their own rows below, and approved tasks T1.1 and T1.2
for 0.12.1. Approving those tasks is the owner's separate act, recorded in the plan.

| # | Question | Recommendation | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D460** | Scope: a project-wide token only, or Bitbucket in shared project jobs too? | **A project-wide token, set once, with the existing per-repository jobs** (F1). It is mostly docs, and it is what the spec asks | **Also run Bitbucket repositories in shared project jobs, as GitHub has since phase 3.** That is real code: the deploy check, the launcher, phase 3's pins and a live check (F11–F13, slice S2) | **2026-10-27: shared project jobs as well. Not the recommendation.** (Sizing, not the owner's words: S1 is the first useful slice, S2 follows; §6) |
| **D461** | Which credential? | A Bitbucket **workspace access token or project access token**. The operator picks the narrowest that covers the Fugaro project's repositories: a project token when they sit in one Bitbucket project (F6). Both are Premium and long-lived (§3) | Repository tokens only. That is today's default, and it is the spec's problem | **2026-10-27: either a workspace or a project access token, the operator picks the narrowest that fits.** The existing `project_secrets.bitbucket-token` allow-list names which Fugaro repositories mount it |
| **D462** | Accept that the token's reach is the agent's reach (F7)? | Accept, with mitigations: the allow-list, the narrowest token type, minimum scopes (D470), an optional expiry, and a plain statement in `SECURITY.md` and `docs/git-providers.md` | Refuse, and keep per-repository tokens. That leaves the spec unmet | **2026-10-27: accepted, with the allow-list, minimum scopes, and a plain statement in `SECURITY.md` and `docs/git-providers.md`.** (The narrowest token type and an optional expiry are this document's additions.) This **reverses** the owner's position of 2026-10-26 (docs/bitbucket-credentials D363 as amended: runs keep per-repository tokens), **for operators who opt in** through the allow-list. It **amends** the ruling of 2026-10-15 ("Bitbucket stays per repository": `docs/git-providers.md`, `docs/design/project-centric.md` §4.5 and D5, `docs/design/project-secrets.md`). Every pin is listed in F5 |
| **D463** | Which release carries what? | 0.12.1: S1 only, if it is genuinely small. 0.13.0: everything else | Put S2 in 0.12.1. It is not small (§6) | **2026-10-27: 0.12.1 must be quick; anything big goes to 0.13.0, the "control the spend" release, and live checks go there too.** (Applied, not the owner's words: S1 is 0.12.1, everything else 0.13.0, §6; D468 asks to reopen the live-check part for Check 180 alone) |

**Decisions this reconciliation raises.** D474, D475 and D477 were answered on 2026-10-27, and
their rows carry the answers. The rest are **pending with the owner**: their answer cells are
empty, and they are listed under §7.

| # | Question | Recommendation | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D464** | Should the allow-list take a pattern such as `<workspace>/*`? (F10) | **No, keep the explicit list.** Each line is a deliberate grant of a workspace-reach credential, it is typed once, and "empty means nobody" stays simple | A `<workspace>/*` entry. Ten lines of config code plus the schema and `ProjectSecretCandidate`; security-sensitive (a new repository in the workspace would be admitted silently). 0.13.0 at the earliest (S3) | |
| **D465** | How is the allow-list enforced for a Bitbucket shared job? (F11) | **Amended in review rounds 1 and 2, before any answer. Follow the accounts precedent.** (1) `fugaro project policy publish` derives a `bitbucket_token.allow_repos` block from `project_secrets.bitbucket-token.allow_data_to`, as `DeriveAccounts` derives `accounts`. (2) The runner, on a Bitbucket shared job, refuses a repository not on that block before the provider opens, as `checkProjectJobAccount` does. (3) `validProfiles` requires a profile that mounts `bitbucket-token` to carry its own exact list, a subset of the block, and refuses the token on a GitHub profile. (4) Whether `projectpolicy.Version` moves is D477. (5) `fugaro update` runs the same subset check before it deploys. **Trust stated plainly:** whoever may write the node, and so whoever **publishes** (the block is derived from the publisher's own entry), is trusted to decide who mounts the token. The runner check binds every other path, but **not a launcher** (F7, D474) | (b) A CLI-only subset check at deploy and publish: no runner enforcement, bypassed by an older binary, a direct node writer, or a second operator with a wider entry. (c) Carry the list in the job's environment: a launcher may override it | |
| **D466** | A repository with its own `bitbucket-token` copy, admitted to a Bitbucket shared job (F11) | **Document it, and have `fugaro doctor` warn:** on a shared job the project token is used and the repository's own copy is not. The copy still wins on that repository's per-repository jobs (F4, kept) | The launcher keeps such a repository on its per-repository job. That needs a Secret Manager metadata read at every launch, plus the launcher's grant on it | |
| **D467** | A second operator's machine has no `project_secrets` (F9) | **0.12.1: document it** ("copy the entry to every operator's local config, or `fugaro update --repo` asks for a per-repository token"). No code. Under S2 (corrected in review round 2): `Shared()` drops `Profiles` too, so a machine seeded from the shared config has no profiles, and `RefuseProjectJobDeletes` stops its plan. A machine with profiles copied by hand but no entry stops at D465's subset check. A machine with a **wider** entry is not stopped: if it publishes, the block it derives widens what the runner admits (corrected in review round 3). The publisher decides, as D465 states | Publish the allow-list in the shared config. That reverses `sharedForbidden`'s security reason (a runs-bucket writer could widen a mount): no | |
| **D474** | Should a cloud run refuse a non-default `FUGARO_BITBUCKET_API_URL`? (F7) | **Yes, in 0.13.0:** outside a local invocation the runner refuses any API root other than `https://api.bitbucket.org/2.0`. That closes the token-redirect half of the launcher path (the agent half stays). `FUGARO_GITHUB_API_URL` is the same redirect for App-signed JWTs today; it is named here and out of scope | Leave it and state it. Every launcher of an allow-listed repository can then exfiltrate the workspace token without an agent's help | **2026-10-27: yes. The recommendation, extended by the owner:** an API root the owner allow-lists in the policy is also accepted, for self-managed Bitbucket. Why: the token must not be sent to a launcher's host. Consequence: the policy node gains an owner-set list of API roots (T2.1), and T2.8 is on. **Tension T2** (§7): D366 put Bitbucket Data Center out of scope, and the adapter speaks only the Cloud 2.0 API |
| **D475** | Does D462's acceptance cover launchers? (F7; was O4) | **Yes, and say so in `SECURITY.md`.** A launcher of an allow-listed repository can already obtain the run's git credential today; the opt-in widens it to the token's reach | No: then D474 (the API-root pin) and a way to keep the token from the agent are prerequisites, and the latter is the push and pull-request proxy docs/bitbucket-credentials parked | **2026-10-27: yes, launchers are trusted as before. The recommendation.** Why: a launcher can already obtain the run's git credential today. **Recorded consequence:** a malicious launcher can use the agent to write across everything the token reaches, the whole workspace or project. The API root is locked by D474, so a launcher cannot have the runner send the token to its own host. `SECURITY.md` says this (T1.1) |
| **D476** | A code mitigation for F17's cross-repository pull requests? (was O5) | **0.13.0: a review-only run on Bitbucket refuses a pull request whose author is the token's own identity**, when that identity is known (U2). It is a new behaviour, so it is the owner's | Document only (T1.1). A hostile agent's pull request in another repository is then reviewed by a run that holds the token | |
| **D477** | Does the node's new `bitbucket_token` block bump `projectpolicy.Version`? (D465, review round 3) | **No bump.** `Decode` records unknown top-level keys (`UnknownKeys`) instead of refusing them, so older readers tolerate the block. Older runners never run a Bitbucket shared job, because T2.2 refuses to deploy one on a base below the release. That floor is a per-feature `…Since`, which goes against P6's "no `*Since` constant per feature" (`internal/cli/recipes_skew.go`), so the owner rules on it here | **Bump 3 → 4.** `Decode` refuses any other version in both directions. Every runner, per-repository and GitHub-shared alike, every older CLI's launch, and the credentials service (`credsvc` decodes the node) would refuse the other version's node. That needs a lockstep upgrade of every installation (`publish --delete`, then publish; the 0.9.0 notes show the order and the loss of manual `repos add` entries), plus `VersionSince[4]` and the policy fixtures that hard-code version 3 (counted in plan T2.1) | **2026-10-27: bump the version (3 → 4). Not the recommendation**, which was no bump. The decision was relayed as "the design's recommendation". It is recorded as the owner's answer against this document's recommendation. Why: a clean refusal is better than a per-feature base floor. **Recorded consequence:** when 0.13.0 ships, every installation with a published policy upgrades in lockstep. Older runners, older CLIs' launches and the credentials service refuse a v4 node, and new ones refuse a v3 node. The operator republishes (`publish --delete`, then publish, re-adding manual `repos add` entries, as in 0.9.0). A release-notes task writes the `### For operators` steps (T2.11). T2.2's base floor is dropped |
| **D468** | **Reopen D463's live-check part for Check 180 alone?** That is, run Check 180 (a workspace or project token, live, sandbox) before 0.12.1's docs ship (F15, T1)? | **Run it before 0.12.1 ships if a Premium sandbox exists.** It is owner-run and needs no code, so it does not slow the release's code | Ship 0.12.1's docs marked "not yet live-checked", and run Check 180 in 0.13.0 as the 2026-10-27 ruling puts live checks | |

**Resolutions** (one defensible answer; not questions; D473 is a proposal pending the owner, see §7):

- **D469.** The setup skill keeps the per-repository default, because its users usually have one
  repository. It gains one sentence pointing to the project-wide option in
  `docs/git-providers.md`. `TestSetupSkillStoresTheGitCredentialWithoutRepo` keeps refusing a
  `--project-wide` store in the skill's own steps.
- **D470.** Minimum scopes are those a run needs today: `repository:write` and `pullrequest:write`
  (each implies its read). No admin, webhook, pipeline or project-admin scope. That the Bitbucket UI
  names them exactly so for project and workspace tokens is **UNVERIFIED** (U7). Check 180 records
  them.
- **D471.** Rotation and revocation follow F3, documented in T1.1.
- **D472.** The PR author is the token's name (F8). The docs say to name a shared token as they
  already say for a repository token, once rather than per repository.
- **D473 (proposed, pending the owner, review round 2).** A Bitbucket profile gets a shared job only when it mounts `bitbucket-token`. A
  Bitbucket profile without that mount keeps its repositories on per-repository jobs, as every
  Bitbucket profile does today, and the deployer and the launcher ask one profile predicate (plan
  T2.1, T2.2). **Corrected in review round 1:** a local config may already hold a Bitbucket profile
  that lists `bitbucket-token`, because `ProjectJobs` skips it before `projectJobSecrets` would
  refuse anything. After 0.13.0, `fugaro update` would deploy a job for it, or would refuse under
  D465. T2.2's PR therefore adds a `### For operators` entry to the release notes.

## 6. The 0.12.1 versus 0.13.0 split

| Release | Slice | What | Size |
| --- | --- | --- | --- |
| **0.12.1** | **S1** | The docs (`docs/git-providers.md`, `SECURITY.md`, `website/content/security.md`, the setup skill) present one workspace or project token, allow-listed and stored once, as the supported path for many repositories. They state the reach (D462), the rotation, the migration, the second machine (D467) and the PR author. The CLI's help text and message wording, and `PerRepositorySecret`'s doc comment, say the same. **No behaviour change**; per-repository jobs are unchanged | 2 tasks, about 20 files (docs, generated pages, comments and message text), 1–2 runs |
| **0.13.0** | **S2** | Bitbucket in shared project jobs: a Bitbucket profile that mounts `bitbucket-token` gets a job (a GitHub profile still may not mount it), the node rule and the deploy and publish check (D465), the doctor warning (D466), the API-root pin (D474), the launcher and runner tests, the phase-3 doc pins (F5), and Check 181 | 10 tasks (T2.1–T2.4, T2.6–T2.11); the policy version 4 makes 0.13.0 a lockstep upgrade (D477) |
| **0.13.0** | **S3** | Only if D464 is answered "pattern" | 1 task |
| **0.13.0** (or before 0.12.1 under D468) | — | Check 180 (owner-run) | owner |

## 7. Open tensions and open items for the owner

- **T1.** D463 puts live checks in 0.13.0. 0.12.1's docs then present a path that has never been
  run with a workspace or project token (F15, U1, U2). Choices: run Check 180 first (owner time, and
  it needs a Premium sandbox, U6), or ship the docs saying "not yet live-checked". D468 asks.
- **O1.** D464–D468, D473 and D476 are pending. Each needs the owner's answer before the task that
  depends on it (shown in the plan) can be approved. D474, D475 and D477 were answered on
  2026-10-27.
- **T2 (new, 2026-10-27).** D474's answer accepts an owner-allow-listed API root "for self-managed
  Bitbucket". D366 (docs/bitbucket-credentials) put Bitbucket Data Center out of scope, and the
  adapter speaks only the Cloud 2.0 API (`bitbucket.DefaultBaseURL`). The owner has two choices:
  - **The list is for Cloud-compatible roots only**, such as a proxy in front of Bitbucket Cloud.
    D366 stands.
  - **Data Center comes into scope.** That needs a second adapter, and it is not planned here.

  T2.8 builds the list either way. It does not resolve this tension. Also stated: an owner-listed
  root reaches the runner only through a published policy. An installation that publishes none has
  no way to list a root, and its runs are refused unless they use Bitbucket Cloud's own API.
- **O2.** Does a Premium sandbox Bitbucket workspace exist for Checks 180–181? **UNVERIFIED** (U6).
  Without one, both checks wait.
- **O3.** D462 reverses the 2026-10-26 position for opted-in operators. docs/bitbucket-credentials'
  parked S3 (service-minted run tokens) stays parked: this work mounts a stored token and mints
  nothing.
- **O4.** Was "does D462 cover launchers"; now D475 (review round 2).
- **O5.** Was "a code mitigation for F17"; now D476 (review round 2).
- **O6.** D473 is a proposal and needs the owner's answer.
- **O7.** U5 and U8 land in Check 180's steps (U5 on a second machine) and in T2.2's review (U8);
  neither is settled by any test.

## 8. Numbers this work holds

D460–D489: D460–D477 used, D478–D489 reserved and unused. Checks 180–189: 180–181 proposed, 182–189
reserved. Both blocks sit above the subscriptions work's D430–D459 and Checks 170–179. They were
checked against `main` and the docs of every open PR on 2026-10-27 (none writes D460 or above, or
Check 180 or above).

## 9. UNVERIFIED

- **U1.** Project and workspace access tokens authenticate git as `x-token-auth` and the API as
  Bearer, as repository tokens do. Settled by Check 180.
- **U2.** The commit and PR identity of a project or workspace token, and whether `GET /user`
  refuses it as it refuses a repository token (Check 180).
- **U3.** A project token covers a repository added to the project after the token was created.
- **U4.** Whether `pullrequest:write` lets the token approve and merge.
- **U5.** What a second machine's `fugaro update --repo`, without the allow-list entry, plans for an
  existing project-copy mount before Readiness stops it (F9).
- **U6.** Whether a Premium sandbox Bitbucket workspace exists. On 2026-10-27 the owner knows of none.
- **U7.** The exact scope names the Bitbucket UI shows for project and workspace tokens.
- **U8.** Whether a repository's Dockerfile step can read the BuildKit `git-credentials` secret
  during an image build (F18).
