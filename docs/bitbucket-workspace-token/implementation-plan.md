# Bitbucket workspace and project tokens: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). It
references decisions by `D#` and findings by `F#`.

## Status

- **Nothing is implemented. No task is approved.** Each task needs the owner's own explicit go,
  given after reading this PR. Answering a decision does not approve a task.
- **The owner's rulings that shape the plan, all of 2026-10-27:**
  - **D460:** Bitbucket repositories also run in shared project jobs. This was not the
    recommendation.
  - **D461:** a workspace or project access token, whichever is narrowest.
  - **D462:** the reach risk is accepted, with the allow-list and a plain statement.
  - **D463:** 0.12.1 is quick, and anything big goes to 0.13.0.
- **Pending decisions:** D464–D468. Each task lists the decisions it waits on.

| Release | Tasks |
| --- | --- |
| **0.12.1** | T1.1 (docs), T1.2 (CLI text), T1.3 (Check 180 written, only if D468 = "before"), T1.4 (Check 180, OWNER-RUN, only if D468 = "before") |
| **0.13.0** | T2.1–T2.6, T2.7 (Check 181, OWNER-RUN), T3.1 (only if D464 = "pattern"). T1.3 and T1.4 move here if D468 = "after" |

## Conventions every part follows

- `CLAUDE.md`, **Things that bite**:
  - Docs are tested. Run `go test ./internal/cli/ -run Docs` after any doc change. Run
    `npm run check` in `website/` when `website/content/` changes.
  - Comments explain why.
  - A fix round adds no functionality.
- `.claude/rules/terraform.md` loads for `internal/infra/**`. The plan changes no `.tf` file:
  `modules/project/job.tf` and `sa.tf` already iterate a job's `secret_env`.
- `.claude/rules/website.md` loads for `website/content/security.md`. That file is `SECURITY.md`
  rendered, and `internal/cli/docs_website_security_test.go` holds the two equal.
- The plugin's tests check every command a skill names: `go test ./plugin/ -run 'Setup'`.
- **No secret-shaped literal** in any test, doc or fixture. Bitbucket tokens in tests are plain
  words, as in `TestBitbucketUnchanged`.
- **No live cloud or Bitbucket action by an agent.** Checks 180 and 181 are OWNER-RUN, on the
  sandbox installation and a sandbox Bitbucket workspace only. No real person's or organisation's
  name appears in any doc.
- Historical records are amended by a dated note, never rewritten: `docs/releases/v0.9.0.md`,
  Check 52's FACT, and `docs/plans/2026-10-14-project-centric.md`.
- Opting in is the operator's own act, so no "For operators" release step is needed. T2.2 changes
  deploy behaviour only for a Bitbucket profile that mounts `bitbucket-token`, and no installation
  can have one today, because `projectJobSecrets` refuses it.

## Global Constraints and Review Focus

1. **No behaviour change in 0.12.1.** T1.2 changes help, refusal and comment text only. Its
   reviewer checks that the diff touches no condition. Pinned by T1.2's own tests, and by the
   existing `initsecrets` and `secrets` tests, which pass unchanged.
2. **The allow-list remains the only way a Bitbucket token becomes project-wide.** It is never a
   default, and `ProjectScopeByDefault("bitbucket-token")` stays false. Pinned by
   `internal/config/project_secrets_test.go` (unchanged) and by T2.2.
3. **A GitHub profile never mounts `bitbucket-token`.** Pinned by T2.2:
   `TestProjectJobsRefuseBitbucketTokenOnAGitHubProfile`.
4. **No Bitbucket shared job admits a repository the allow-list does not name**, whether at deploy
   or at publish (D465). Pinned by T2.1, T2.2 and T2.3.
5. **An installation with only Bitbucket profiles needs no GitHub App ID.** Pinned by T2.2:
   `TestProjectJobsBitbucketOnlyNeedsNoGitHubAppID`.
6. **The deployer and the launcher ask one predicate.** If they disagree, a launch targets a job
   that does not exist. Pinned by T2.2: `TestLaunchPutsABitbucketProfileOnItsProjectJob` and
   `TestLaunchKeepsABitbucketProfileWithoutTheTokenOnThePerRepositoryJob`.
7. **The reach statement is in `SECURITY.md` in plain words** (D462). Pinned by T1.1:
   `TestDocsStateTheBitbucketProjectTokenReach`.

## Slices

- **S1 (0.12.1): one token, stored once.** Docs and CLI text only. Useful on its own, because the
  mechanism already exists (F1–F4).
- **S2 (0.13.0): Bitbucket in shared project jobs** (D460). Inert until an operator writes a
  Bitbucket profile that mounts `bitbucket-token`.
- **S3 (0.13.0, conditional): a workspace pattern in the allow-list** (D464). Built only on a
  "pattern" answer.

## Tasks

### T1.1: Docs: one workspace or project token for many repositories (0.12.1)

- **Files:**
  - `docs/git-providers.md`: the variables-table row, the §Bitbucket Cloud paragraphs on storage,
    and "Name the token".
  - `SECURITY.md`: "Narrow git credentials".
  - `website/content/security.md`: the same text.
  - `docs/design/project-secrets.md`: a dated note under "Stated, not implied".
  - `plugin/skills/setup/SKILL.md` and `plugin/skills/setup/reference/decisions.md`: one sentence
    each (D469).
  - `internal/cli/docs_project_wide_git_test.go`.
- **Interfaces:** none.
- **What the docs say** (D461, D462, D467, D469–D472, F3, F4, F8, F9):
  - **The option.** A Premium workspace or project access token can serve many repositories.
    Prefer a project token when the repositories sit in one Bitbucket project.
  - **The steps:** the allow-list entry; `fugaro secrets set bitbucket-token --project-wide <
    token-file`; then each repository's `fugaro update --repo` asks nothing.
  - **The reach, in plain words.** Every allow-listed run's agent holds the token's value. A hostile
    agent can push, open and comment on pull requests, and trigger Pipelines in every repository the
    token reaches, including repositories not enrolled in Fugaro.
  - **Hardening:** minimum scopes and an optional expiry.
  - **Rotation:** store a new version, wait for running executions to end, then revoke the old
    token.
  - **Migration:** a repository's own copy wins until its versions are disabled.
  - **A second operator's machine** needs the same `project_secrets` entry.
  - **The PR author:** one name on every repository.
  - **What is unchanged:** phase 3's "shared jobs serve GitHub" statements, which T2.6 changes in
    0.13.0.
  - **The live check:** if D468 = "after", the path is labelled "not yet live-checked with a
    workspace or project token (Check 180)".
- **Schema/rules/config change:** none.
- **Tests:**
  - `TestDocsStateTheBitbucketProjectTokenReach` (new). It pins the reach sentence in
    `SECURITY.md` and in `docs/git-providers.md`, the rotation order, and the second-machine
    sentence.
  - `TestDocsWarnAboutAProjectWideGitCredential` (its pinned sentences keep their facts, reworded in
    both places if needed).
  - `TestDocsSayTheInitPromptHasNoRepoFlagAndBitbucketIsPerRepository`.
  - `TestSetupSkillStoresTheGitCredentialWithoutRepo` (unchanged: the skill's steps still never
    store `--project-wide`).
  - The `docs_website_security_test.go` equality.
- **Red step:** write `TestDocsStateTheBitbucketProjectTokenReach` first. It fails because neither
  file has the sentences.
- **Command:**
  - `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|TestSetupSkill' -count=1 -timeout 1800s`
  - `go test ./plugin/ -run Setup -count=1`
  - `cd website && npm run check`
- **Depends on:** none. D467 must be answered: its recommendation is what this task writes.
- **Commit:** `docs(bitbucket): one workspace or project token for many repositories, and what it reaches`
- **Reviewer tier:** opus + mandatory independent review (a `SECURITY.md` statement about a
  credential's reach).

### T1.2: CLI text names the workspace or project token (0.12.1)

- **Files:**
  - `internal/cli/secrets.go`: the `secrets set` and `secrets list` help paragraphs on
    `bitbucket-token`, and `projectWideRefusal`'s sentence for `bitbucket-token`, which gains "for
    a workspace or project access token".
  - `internal/config/project_secrets.go`: `PerRepositorySecret`'s and `ProjectScopeByDefault`'s doc
    comments.
  - `internal/cli/init.go`: the comment above `infra.SecretVersions`.
  - `internal/cli/secrets_bitbucket_text_test.go` (new).
- **Interfaces:** none. Behaviour is unchanged (Global Constraint 1).
- **Schema/rules/config change:** none.
- **Tests:**
  - `TestSecretsSetHelpNamesTheBitbucketProjectToken` (new): the help says a workspace or project
    access token may be stored project-wide through the allow-list.
  - `TestSecretsSetProjectWideBitbucketRefusalNamesTheAllowList` (new): the refusal names
    `project_secrets.bitbucket-token.allow_data_to` and the token kinds.
  - The existing `TestInitRepoProjectSecretUnlistedRepoIsMissing`, passing unchanged.
- **Red step:** the two new tests fail on today's text, which says "a repository access token,
  issued for one repository".
- **Command:**
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestSecretsSet.*Bitbucket|TestInitRepoProjectSecret' -count=1 && go test ./internal/config/ -run ProjectSecret -count=1 && go test ./plugin/ -count=1`
- **Depends on:** none.
- **Commit:** `cli: secrets set names the Bitbucket workspace or project token as the project-wide case`
- **Reviewer tier:** standard. The reviewer confirms the diff changes text only.

### T1.3: Check 180 written into the register (0.12.1 if D468 = "before", else 0.13.0)

- **Files:** `docs/gcp-live-checklist.md` (a new `## Check 180` section and its row).
- **What Check 180 measures:** on the sandbox installation and a Premium sandbox workspace (U6), the
  operator does the following:
  1. Creates a project access token with `repository:write` and `pullrequest:write`. U7 records the
     exact names.
  2. Allow-lists two sandbox repositories and stores the token with `--project-wide`.
  3. Runs `fugaro update --repo` on each, and records that there is no prompt.
  4. Launches one run per repository and records:
     - a clone and push with `x-token-auth` (U1);
     - a pull request opened and its author's name (U2);
     - `TestLiveBitbucket` with the token, which also records `GET /user`'s answer (U2);
     - whether approve and merge work with the scopes (U4);
     - whether a repository added to the Bitbucket project afterwards is reachable (U3).
  5. Rotates the token (F3) and records that the next run uses the new version.
- **Tests:** `go test ./internal/cli/ -run Docs` (the register's row and section pins).
- **Red step:** add the row to the register's index first. The register pin fails until the
  section exists.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 1800s`
- **Depends on:** D468.
- **Commit:** `docs(gcp-live-checklist): Check 180, a Bitbucket project or workspace token live (NOT RUN)`
- **Reviewer tier:** standard.

### T1.4: Check 180, OWNER-RUN

- **OWNER-RUN.** The owner follows T1.3's section on the sandbox only. No agent performs any step.
  The owner types the commands the section gives, which are the
  `fugaro secrets set bitbucket-token --project-wide < <token-file>`,
  `fugaro update --repo <path>` and `fugaro run` lines and the `TestLiveBitbucket` command from
  `docs/git-providers.md`. The owner then records the results in the section.
- **Depends on:** T1.3, and T1.1 and T1.2 released (or a dev build), plus U6.

### T2.1: The allow-list check for Bitbucket profiles (0.13.0)

- **Files:** `internal/projectpolicy/bitbucket.go` (new) and
  `internal/projectpolicy/bitbucket_test.go` (new).
- **Interfaces produced:** the predicate and the check below. T2.2 and T2.3 consume them.
  - `projectpolicy.SharedBitbucketProfile(p config.JobProfile) bool` is true when
    `p.Provider == "bitbucket"` and `p.Secrets` contains `bitbucket-token`. A Bitbucket profile
    without the token stays on per-repository jobs (resolution D473: existing installations are
    unchanged).
  - `projectpolicy.CheckBitbucketTokenAdmission(profiles map[string]Profile, allowRepos []string, secrets map[string]config.ProjectSecret) error`
    refuses when such a profile admits a repository that is not on
    `project_secrets.bitbucket-token.allow_data_to`. A profile's absent `allow_repos` means the
    policy's `allowRepos`, which is checked the same way. The comparison is case-folded.
- **Schema/rules/config change:** none.
- **Tests:**
  - `TestCheckBitbucketTokenAdmissionRefusesAnUnlistedRepository`
  - `TestCheckBitbucketTokenAdmissionReadsThePolicyListWhenTheProfileHasNone`
  - `TestCheckBitbucketTokenAdmissionIgnoresGitHubProfiles`
  - `TestSharedBitbucketProfileNeedsTheTokenMount`
- **Red step:** the tests are written against a stub that returns nil, and they fail.
- **Command:** `go test ./internal/projectpolicy/ -run 'Bitbucket' -count=1`
- **Depends on:** D465 answered. No task.
- **Commit:** `projectpolicy: a Bitbucket profile that mounts the token admits only allow-listed repositories`
- **Reviewer tier:** opus + mandatory independent review (it decides who receives a credential that
  reaches a workspace).

### T2.2: Deploy and launch Bitbucket shared jobs (0.13.0)

- **Files:** `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`,
  `internal/cli/project_job.go` and `internal/cli/project_job_test.go`.
- **Interfaces consumed:** T2.1.
- **Interfaces produced:** `infra.SharedJobProfile(pr config.JobProfile) bool`, which is GitHub, or
  `projectpolicy.SharedBitbucketProfile`. It replaces `SharedJobProvider` at its three call sites:
  `ProjectJobs`, its App-ID guard and `launchProjectJobFor`.
- **Behaviour:**
  - `ProjectJobs` calls `CheckBitbucketTokenAdmission`.
  - The App-ID guard becomes `pr.Provider == "github"` (Global Constraint 5).
  - `projectJobSecrets` refuses `bitbucket-token` on a profile that is not a Bitbucket one (Global
    Constraint 3).
  - The comments that cite the 2026-10-15 ruling cite D460–D462 instead.
- **Schema/rules/config change:** none. `modules/project` already grants `secretAccessor` per
  `secret_env` entry.
- **Tests:**
  - `TestProjectJobsDeployABitbucketProfileThatMountsTheToken` (replaces
    `TestProjectJobsSkipBitbucketProfiles`)
  - `TestProjectJobsSkipABitbucketProfileWithoutTheToken`
  - `TestProjectJobsRefuseBitbucketTokenOnAGitHubProfile`
  - `TestProjectJobsBitbucketOnlyNeedsNoGitHubAppID`
  - `TestProjectJobsRefuseABitbucketProfileAdmittingAnUnlistedRepository`
  - `TestSharedJobProviderIsTheProvidersList`, reshaped for `SharedJobProfile`
  - `TestLaunchPutsABitbucketProfileOnItsProjectJob` (replaces
    `TestLaunchKeepsABitbucketProfileOnThePerRepositoryJob`)
  - `TestLaunchKeepsABitbucketProfileWithoutTheTokenOnThePerRepositoryJob`
  - `TestLaunchKeepsAnotherProvidersRepositoryOnItsOwnJob` (unchanged)
- **Red step:** the new infra test fails on today's skip, and the launcher test fails on today's
  per-repository job.
- **Command:**
  `go test ./internal/infra/ -run 'ProjectJob|SharedJob' -count=1 && GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestLaunch' -count=1`
- **Depends on:** T2.1.
- **Commit:** `infra, cli: Bitbucket profiles that mount the workspace or project token get a shared project job`
- **Reviewer tier:** opus + mandatory independent review.

### T2.3: Policy publish runs the same check (0.13.0)

- **Files:** `internal/cli/project_cmd.go` (beside `DeriveAccounts` in the publish path) and
  `internal/cli/project_cmd_bitbucket_test.go` (new).
- **Interfaces consumed:** T2.1.
- **Tests:**
  - `TestPolicyPublishRefusesABitbucketProfileAdmittingAnUnlistedRepository`
  - `TestPolicyPublishAcceptsAnAllowListedBitbucketProfile`
- **Red step:** the first test fails, because publish writes the node today.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestPolicyPublish.*Bitbucket' -count=1`
- **Depends on:** T2.1.
- **Commit:** `cli: project policy publish refuses a Bitbucket profile that admits a repository the token's allow-list does not`
- **Reviewer tier:** opus + mandatory independent review.

### T2.4: `fugaro doctor` warns about an own copy that a shared job ignores (0.13.0)

- **Files:** `internal/cli/doctor.go` and `internal/cli/doctor_bitbucket_shared_test.go` (new).
- **Behaviour (D466):** for each repository admitted to a Bitbucket profile that mounts the token
  and that has an enabled version of its own `bitbucket-token` copy, the check reads metadata only,
  as `doctorProjectSecrets` does. It warns: "its shared job uses the project token; its own copy
  applies only to its per-repository jobs".
- **Tests:** `TestDoctorWarnsABitbucketOwnCopyOnASharedProfile` and
  `TestDoctorIsQuietWithoutAnOwnCopy`.
- **Red step:** the warning test fails, because there is no warning today.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDoctor.*Bitbucket' -count=1`
- **Depends on:** D466 answered. No task. It reads `projectpolicy.SharedBitbucketProfile`, so it
  depends on T2.1.
- **Commit:** `doctor: a Bitbucket repository's own token is not what its shared job uses`
- **Reviewer tier:** standard.

### T2.5: Runner tests for a Bitbucket project job (0.13.0)

- **Files:** `internal/runner/projectjob_bitbucket_test.go` (new).
- **What it pins (F13):** a run on a `bitbucket` project job is admitted for a Bitbucket repository
  the profile admits. It is refused for a `github.com` remote, before any credential. Its provider
  opens from `FUGARO_BITBUCKET_TOKEN` for the run's own `owner/name`.
- **Tests:**
  - `TestBitbucketProjectJobAdmitsItsProfilesRepository`
  - `TestBitbucketProjectJobRefusesAGitHubRemote`
  - `TestBitbucketProjectJobOpensTheRunsOwnRepository`
- **Red step (mutation).** The runner already supports this, so the tests may pass at once. Remove
  `bitbucket` from `jobProviderKinds` locally, watch the tests fail, then restore it.
- **Command:** `go test ./internal/runner/ -run 'TestBitbucketProjectJob' -count=1 -timeout 600s`
- **Depends on:** none.
- **Commit:** `runner: pin a Bitbucket project job's admission and provider`
- **Reviewer tier:** standard.

### T2.6: Docs: phase 3 serves Bitbucket too (0.13.0)

- **Files:**
  - `docs/git-providers.md`: "Bitbucket stays on the per-repository path" is rewritten.
  - `docs/design/project-centric.md`: dated amendments to §4.1 "GitHub only" and §4.5/D5.
  - `docs/project-layer.md`: "Bitbucket repositories never reach one".
  - `docs/gcp-live-checklist.md`: a dated note under Check 52 §2, the Check 181 section, and Check
    180 when T1.3 moved here.
  - `docs/plans/2026-10-14-project-centric.md`: a dated note.
  - `SECURITY.md` and `website/content/security.md`: a shared Bitbucket job's isolation (F12).
  - `internal/cli/docs_project_centric_test.go`: the Check 52 pins stay; a pin is added for the
    dated note.
  - `internal/cli/docs_project_wide_git_test.go`.
- **Tests:**
  - `TestDocsStateBitbucketSharedJobIsolation` (new)
  - the Check 52 pins
  - `TestDocsStateTheBitbucketProjectTokenReach`
- **Red step:** the new pin fails first.
- **Command:**
  `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 1800s && cd website && npm run check`
- **Depends on:** T2.2 and T2.3 (the docs describe shipped code), and T1.1 (the same files, an
  earlier release).
- **Commit:** `docs(bitbucket): shared project jobs serve Bitbucket profiles that mount the workspace or project token`
- **Reviewer tier:** opus + mandatory independent review (a `SECURITY.md` change).

### T2.7: Check 181, OWNER-RUN (0.13.0)

- **OWNER-RUN, sandbox only.** Check 181 runs as follows:
  1. A Bitbucket profile mounting `bitbucket-token` admits two allow-listed sandbox repositories.
  2. `fugaro update` deploys one `fugaro-p-…` job.
  3. A run of each repository executes on it and opens its pull request.
  4. A third sandbox repository, not allow-listed, makes `fugaro project policy publish` and
     `fugaro update` refuse.
- The owner types the commands T2.6's section gives. No agent performs any step.
- **Depends on:** T2.2, T2.3 and T2.6 released or a dev build, plus U6.

### T3.1: A workspace pattern in the allow-list (0.13.0, only if D464 = "pattern")

- **Files:**
  - `internal/config/project_secrets.go`: `AllowsData` and `ValidateProjectSecrets` accept
    `<workspace>/*` for `bitbucket-token` only.
  - `internal/config/project_secrets_test.go`.
  - the local config's JSON schema under `schemas/`.
  - `internal/projectpolicy/bitbucket.go` (T2.1's check reads the pattern).
- **Red step:** `TestProjectSecretWorkspacePatternAdmitsItsRepositories` fails, because `*` is
  refused today.
- **Command:** `go test ./internal/config/ ./internal/projectpolicy/ -run 'ProjectSecret|Bitbucket' -count=1`
- **Depends on:** T2.1 (the same file).
- **Commit:** `config: project_secrets.bitbucket-token may allow-list a whole workspace`
- **Reviewer tier:** opus + mandatory independent review.

## Parallel execution

**Edges.**

| Task | Depends on |
| --- | --- |
| T2.2 | T2.1 |
| T2.3 | T2.1 |
| T2.4 | T2.1 |
| T2.6 | T2.2, T2.3, T1.1 |
| T2.7 | T2.6 |
| T3.1 | T2.1 |
| T1.4 | T1.3 |

**Levels.**

| Release | Level | Tasks (concurrent) |
| --- | --- | --- |
| 0.12.1 | L0 | T1.1, T1.2, T1.3 |
| 0.12.1 | L1 | T1.4 (OWNER-RUN) |
| 0.13.0 | L0 | T2.1, T2.5 |
| 0.13.0 | L1 | T2.2, T2.3, T2.4 |
| 0.13.0 | L2 | T2.6, T3.1 |
| 0.13.0 | L3 | T2.7 (OWNER-RUN) |

**Critical path.**

- **0.12.1:** T1.1. With D468 = "before": T1.3 → T1.4.
- **0.13.0:** T2.1 → T2.2 → T2.6 → T2.7.
- What must be sequential, and why:
  - T2.2, T2.3 and T2.4 consume T2.1's interface.
  - T2.6 must describe shipped code.
  - T3.1 edits T2.1's file.

**Shared-files table.** No level holds two writers of one file.

| File | Writers (level) |
| --- | --- |
| `docs/git-providers.md`, `SECURITY.md`, `website/content/security.md`, `internal/cli/docs_project_wide_git_test.go` | T1.1 (0.12.1 L0), T2.6 (0.13.0 L2) |
| `docs/gcp-live-checklist.md` | T1.3 (0.12.1 L0), T2.6 (0.13.0 L2) |
| `docs/design/project-secrets.md`, `plugin/skills/setup/*` | T1.1 |
| `internal/cli/secrets.go`, `internal/config/project_secrets.go`, `internal/cli/init.go`, `internal/cli/secrets_bitbucket_text_test.go` | T1.2 (0.12.1 L0) |
| `internal/config/project_secrets.go`, `internal/config/project_secrets_test.go` | T1.2 (0.12.1 L0), T3.1 (0.13.0 L2) |
| `internal/projectpolicy/bitbucket.go`, `internal/projectpolicy/bitbucket_test.go` | T2.1 (L0), T3.1 (L2) |
| `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`, `internal/cli/project_job.go`, `internal/cli/project_job_test.go` | T2.2 (L1) |
| `internal/cli/project_cmd.go`, `internal/cli/project_cmd_bitbucket_test.go` | T2.3 (L1) |
| `internal/cli/doctor.go`, `internal/cli/doctor_bitbucket_shared_test.go` | T2.4 (L1) |
| `internal/runner/projectjob_bitbucket_test.go` | T2.5 (L0) |
| `docs/design/project-centric.md`, `docs/project-layer.md`, `docs/plans/2026-10-14-project-centric.md`, `internal/cli/docs_project_centric_test.go` | T2.6 (L2) |

## Checks this plan takes

| Check | Measures | Who | When |
| --- | --- | --- | --- |
| **180** | A project or workspace token: no prompt, clone and push, PR author, `GET /user`, scopes, rotation (U1–U4, U7) | the owner, sandbox only | Before 0.12.1 if D468 = "before", otherwise 0.13.0 |
| **181** | A Bitbucket shared project job end to end, and the allow-list refusal | the owner, sandbox only | 0.13.0 |

Checks 182–189 are reserved and unused.

## Considered and declined

- **Publishing `project_secrets` so that teammates' machines see the allow-list** (D467): this would
  reverse `sharedForbidden`'s reason.
- **An image-build task:** the build already clones with the resolved project copy (F2), and a
  shared job builds no per-repository image.
- **A credentials-service task:** the service holds no Bitbucket credential, and this work mints
  nothing (F14). docs/bitbucket-credentials' S3 stays parked.
