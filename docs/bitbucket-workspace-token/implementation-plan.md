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
- **Pending decisions:** D464–D468 and D474. Each task lists the decisions it waits on.
- **Review round 1 (2026-10-27)** amended D465's recommendation before any answer, added F17,
  F18, D474, O4 and O5, and reshaped T1.2, T2.1, T2.5 and T1.3 (see the PR).

| Release | Tasks |
| --- | --- |
| **0.12.1** | T1.1 (docs), T1.2 (CLI text), T1.3 (Check 180 written, only if D468 = "before"), T1.4 (Check 180, OWNER-RUN, only if D468 = "before") |
| **0.13.0** | T2.1–T2.6, T2.8, T2.7 (Check 181, OWNER-RUN), T3.1 (only if D464 = "pattern"). T1.3 and T1.4 move here, unchanged, if D468 = "after" |

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
- Opting in is the operator's own act, so S1 needs no "For operators" release step. **S2 does**
  (corrected in review round 1). A local config may already hold a Bitbucket profile that lists
  `bitbucket-token`, because `ProjectJobs` skips such a profile before `projectJobSecrets` would
  refuse it. After T2.2, `fugaro update` deploys a job for it or refuses under D465. T2.2's PR
  adds the `### For operators` entry (`docs/releases/README.md`).
- Generated pages are never edited by hand. `website/content/security.md` and
  `website/content/cli-reference.md` are regenerated with `go run ./internal/clidoc`, and
  `go test ./internal/clidoc/` holds them current.

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
   `TestLaunchKeepsABitbucketProfileWithoutTheTokenOnThePerRepositoryJob`. **Known drift, not
   closed:** the deployer reads the local profile and the launcher reads the published one, so two
   machines' configs that disagree on the token mount bring back the 404 that
   `launchProjectJobFor`'s comment describes. This holds for GitHub profiles today, and T2.6
   documents it.
8. **A launcher is not bounded by admission** (F7). Pinned only by T2.8 (the API-root pin) for the
   redirect half. The rest is a statement (T1.1).
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
  - `docs/git-providers.md`: the variables-table row, "What the agent gets", the §Bitbucket Cloud
    paragraphs on storage, and "Name the token".
  - `SECURITY.md`: "Narrow git credentials" and "Give Fugaro its own … repository access".
  - `website/content/security.md`: regenerated with `go run ./internal/clidoc`.
  - `README.md`: its Bitbucket line.
  - `docs/multi-model.md`: the per-repository sentence (its pin moves with it).
  - `docs/design/project-secrets.md` and `docs/design/project-centric.md` §6.3: dated notes.
  - `plugin/skills/setup/SKILL.md` and `plugin/skills/setup/reference/decisions.md`: one sentence
    each (D469).
  - `internal/cli/docs_project_wide_git_test.go`.
- **Interfaces:** none.
- **What the docs say** (D461, D462, D467, D469–D472, F3, F4, F8, F9):
  - **The option.** A Premium workspace or project access token can serve many repositories.
    Prefer a project token when the repositories sit in one Bitbucket project.
  - **The steps:**
    1. Write the allow-list entry.
    2. Run `fugaro update`, so the project container exists.
    3. Run `fugaro secrets set bitbucket-token --project-wide < token-file`.
    4. Run each repository's `fugaro update --repo`, which then asks nothing.

    Each repository needs a base image at 0.7.5 or later (F3).
  - **The reach, in plain words.** Every allow-listed run's agent holds the token's value. **So does
    every launcher of an allow-listed repository** (F7, O4). A hostile agent or launcher can push,
    open and comment on pull requests, and trigger Pipelines in every repository the token reaches,
    including repositories not enrolled in Fugaro.
  - **Cross-repository paths** (F17). A pull request one repository's agent opens in another is
    trusted by that repository's review-only runs. Never put the token's own account ID in
    `followup.trusted`.
  - **Hardening:** minimum scopes and an optional expiry.
  - **Rotation:** store a new version, wait for running executions to end, then revoke the old
    token.
  - **Migration:** a repository's own copy wins until its versions are disabled **and** its
    `fugaro update --repo` has run (F4).
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
  - `TestSecurityMdStatesTheProjectScopeDefault` and `TestWebsiteSecurityPage*`.
  - `internal/clidoc`'s generated-page tests.
- **Red step:** write `TestDocsStateTheBitbucketProjectTokenReach` first. It fails because neither
  file has the sentences.
- **Command:**
  - `go run ./internal/clidoc`
  - `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|TestSetupSkill|TestWebsiteSecurityPage|TestSecurityMd' -count=1 -timeout 1800s`
  - `go test ./internal/clidoc/ ./plugin/ -count=1`
  - `cd website && npm run check`
- **Depends on:** no task. It waits for these answers:
  - D467: its recommendation is what this task writes.
  - D468: whether the docs carry the "not yet live-checked" label.
  - O4: launchers named as accepted.
- **Commit:** `docs(bitbucket): one workspace or project token for many repositories, and what it reaches`
- **Reviewer tier:** opus + mandatory independent review (a `SECURITY.md` statement about a
  credential's reach).

### T1.2: CLI text names the workspace or project token (0.12.1)

- **Files:**
  - `internal/cli/secrets.go`: the `secrets set` and `secrets ls` help paragraphs on
    `bitbucket-token`. The refusal is untouched: `projectWideRefusal` has no Bitbucket branch, and
    adding one would be a new condition (corrected in review round 1).
  - `internal/config/project_secrets.go`: `PerRepositorySecret`'s and `ProjectScopeByDefault`'s doc
    comments.
  - Doc comments in `internal/config/config.go` (`BitbucketSecret`), `internal/config/providers.go`
    (`ServiceHeldSecretNames`) and `internal/infra/spec.go` (`GitSecret`).
  - `internal/gitprov/providers/providers.go`: the missing-token message ("a Bitbucket access
    token"), wording only.
  - `internal/cli/init.go`: the comment above `infra.SecretVersions`.
  - `website/content/cli-reference.md`: regenerated with `go run ./internal/clidoc`.
  - `internal/cli/secrets_bitbucket_text_test.go` (new).
- **Interfaces:** none. Behaviour is unchanged (Global Constraint 1).
- **Schema/rules/config change:** none.
- **Tests:**
  - `TestSecretsSetHelpNamesTheBitbucketProjectToken` (new): the help says a workspace or project
    access token may be stored project-wide through the allow-list.
  - `TestSecretsLsHelpNamesTheBitbucketProjectToken` (new).
  - The existing `TestRepoEngineBuildsOnAProjectWideGitCredential` and
    `TestInitRepoProjectSecretUnlistedRepoIsMissing`, passing unchanged.
- **Red step:** the new tests fail on today's help text, which says "a repository access token,
  issued for one repository".
- **Command:**
  `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/cli/ -run 'Test.*Help.*Bitbucket|TestInitRepoProjectSecret|TestRepoEngineBuildsOnAProjectWideGitCredential' -count=1 && go test ./internal/config/ ./internal/gitprov/providers/ ./internal/clidoc/ ./plugin/ -count=1`
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
- **Depends on:** D468, which decides its release only. T1.3 is Check 180's only writer either way:
  0.12.1 L0, or 0.13.0 L0.
- **Commit:** `docs(gcp-live-checklist): Check 180, a Bitbucket project or workspace token live (NOT RUN)`
- **Reviewer tier:** standard.

### T1.4: Check 180, OWNER-RUN

- **OWNER-RUN.** The owner follows T1.3's section on the sandbox only. No agent performs any step.
  The owner types the commands the section gives, which are the
  `fugaro secrets set bitbucket-token --project-wide < <token-file>`,
  `fugaro update --repo <path>` and `fugaro run` lines and the `TestLiveBitbucket` command from
  `docs/git-providers.md`. The owner then records the results in the section.
- **Depends on:** T1.3, and T1.1 and T1.2 released (or a dev build), plus U6.

### T2.1: The node rule and the allow-list check for Bitbucket profiles (0.13.0)

- **Files:**
  - `internal/projectpolicy/profiles.go` (`validProfiles`, and `providerRequired`'s message).
  - `internal/projectpolicy/bitbucket.go` (new).
  - `internal/projectpolicy/bitbucket_test.go` (new).
- **Interfaces produced:** T2.2, T2.3 and T2.4 consume these.
  - `projectpolicy.SharedBitbucketProfile(p config.JobProfile) bool` is true when
    `p.Provider == "bitbucket"` and `p.Secrets` contains `bitbucket-token`. A Bitbucket profile
    without the token stays on per-repository jobs (D473).
  - **The node's own rule** (D465 as amended, in `validProfiles`, which the runner and the launcher
    decode with):
    - such a profile writes its own `allow_repos`, non-empty, with exact `bitbucket/owner/name`
      entries and no wildcard;
    - `bitbucket-token` on a GitHub profile is refused, so policy and deploy judge it by one rule.
  - `projectpolicy.CheckBitbucketTokenAdmission(profiles map[string]Profile, secrets map[string]config.ProjectSecret) error`
    refuses when such a profile's own list, with the `bitbucket/` prefix stripped and compared
    case-folded, names a repository that is not on `project_secrets.bitbucket-token.allow_data_to`.
- **Schema/rules/config change:** the policy node's validation (above). The node's JSON Schema, if
  `schemas/` carries the profile shape, needs no change, because the rule is semantic.
- **Tests:**
  - `TestValidProfilesRequireAnOwnExactListForTheBitbucketToken`
  - `TestValidProfilesRefuseBitbucketTokenOnAGitHubProfile`
  - `TestCheckBitbucketTokenAdmissionRefusesAnUnlistedRepository`
  - `TestCheckBitbucketTokenAdmissionStripsTheProviderPrefix`
  - `TestSharedBitbucketProfileNeedsTheTokenMount`
  - `TestReposAddCannotWidenABitbucketTokenProfile` (admission is the intersection)
- **Red step:** the tests are written against stubs, and they fail.
- **Command:** `go test ./internal/projectpolicy/ -count=1`
- **Depends on:** D465 answered. No task.
- **Commit:** `projectpolicy: a Bitbucket profile that mounts the token names its own exact repositories, all allow-listed`
- **Reviewer tier:** opus + mandatory independent review (it decides who receives a credential that
  reaches a workspace).

### T2.2: Deploy and launch Bitbucket shared jobs (0.13.0)

- **Files:** `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`,
  `internal/cli/project_job.go`, `internal/cli/project_job_test.go`, and the next CLI release
  notes' `### For operators` entry (see Conventions).
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
- **Red step (mutation).** The runner already supports this, so the tests may pass at once. Make
  `admissionCheck`'s project-job provider gate refuse `bitbucket` locally, watch the tests fail,
  then restore it. (`jobProviderKinds` would not do: only the per-repository path reads it, as
  review round 1 found.)
- **Command:** `go test ./internal/runner/ -run 'TestBitbucketProjectJob' -count=1 -timeout 600s`
- **Depends on:** none.
- **Commit:** `runner: pin a Bitbucket project job's admission and provider`
- **Reviewer tier:** standard.

### T2.6: Docs: phase 3 serves Bitbucket too (0.13.0)

- **Files:**
  - `docs/git-providers.md`: "Bitbucket stays on the per-repository path" is rewritten.
  - `docs/design/project-centric.md`: dated amendments to §4.1 "GitHub only" and §4.5/D5.
  - `docs/project-layer.md`: "Bitbucket repositories never reach one".
  - `docs/gcp-live-checklist.md`: a dated note under Check 52 §2, and the Check 181 section.
  - Global Constraint 6's known drift, written into `docs/project-layer.md`.
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
- **Depends on:** T2.2, T2.3, T2.4 and T2.8 (the docs describe shipped code), and T1.1 (the same
  files, an earlier release).
- **Commit:** `docs(bitbucket): shared project jobs serve Bitbucket profiles that mount the workspace or project token`
- **Reviewer tier:** opus + mandatory independent review (a `SECURITY.md` change).

### T2.8: A cloud run pins the Bitbucket API root (0.13.0, only if D474 = "yes")

- **Files:** `internal/runner/runner.go` (or the file holding `openProvider`) and
  `internal/runner/bitbucket_api_root_test.go` (new).
- **Behaviour:** outside a local invocation, a run refuses a `FUGARO_BITBUCKET_API_URL` other than
  `https://api.bitbucket.org/2.0`, before the provider is opened. This keeps the token from being
  sent to a launcher's host (F7).
- **Tests:** `TestCloudRunRefusesAnotherBitbucketAPIRoot` and
  `TestLocalRunKeepsTheBitbucketAPIRootOverride`.
- **Red step:** the refusal test fails, because the override is honoured today.
- **Command:** `go test ./internal/runner/ -run 'BitbucketAPIRoot' -count=1 -timeout 600s`
- **Depends on:** D474.
- **Commit:** `runner: a cloud run sends its Bitbucket token only to Bitbucket's own API`
- **Reviewer tier:** opus + mandatory independent review.

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
  - No schema file: the local config has no JSON Schema under `schemas/` (corrected in review round
    1).
  - `internal/projectpolicy/bitbucket.go` (T2.1's check reads the pattern; the node rule's own
    exact list is unchanged).
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
| T2.6 | T2.2, T2.3, T2.4, T2.8, T1.1 |
| T2.7 | T2.6 |
| T3.1 | T2.1 |
| T1.4 | T1.3 |

**Levels.**

| Release | Level | Tasks (concurrent) |
| --- | --- | --- |
| 0.12.1 | L0 | T1.1, T1.2, T1.3 |
| 0.12.1 | L1 | T1.4 (OWNER-RUN) |
| 0.13.0 | L0 | T2.1, T2.5, T2.8 (and T1.3 if D468 = "after") |
| 0.13.0 | L1 | T2.2, T2.3, T2.4 |
| 0.13.0 | L2 | T2.6, T3.1 |
| 0.13.0 | L3 | T2.7 (OWNER-RUN) |

**Critical path.**

- **0.12.1:** T1.1. With D468 = "before": T1.3 → T1.4.
- **0.13.0:** T2.1 → T2.2 → T2.6 → T2.7.
- Review round 1: T2.6 now also waits on T2.4 and T2.8, which are both off the critical path.
- What must be sequential, and why:
  - T2.2, T2.3 and T2.4 consume T2.1's interface.
  - T2.6 must describe shipped code.
  - T3.1 edits T2.1's file.

**Shared-files table.** No level holds two writers of one file.

| File | Writers (level) |
| --- | --- |
| `docs/git-providers.md`, `SECURITY.md`, `website/content/security.md`, `internal/cli/docs_project_wide_git_test.go` | T1.1 (0.12.1 L0), T2.6 (0.13.0 L2) |
| `docs/gcp-live-checklist.md` | T1.3 (0.12.1 L0 or 0.13.0 L0), T2.6 (0.13.0 L2) |
| `docs/design/project-secrets.md`, `plugin/skills/setup/*` | T1.1 |
| `internal/cli/secrets.go`, `internal/cli/init.go`, `internal/config/config.go`, `internal/config/providers.go`, `internal/infra/spec.go`, `internal/gitprov/providers/providers.go`, `website/content/cli-reference.md`, `internal/cli/secrets_bitbucket_text_test.go` | T1.2 (0.12.1 L0) |
| `website/content/security.md`, `README.md`, `docs/multi-model.md` | T1.1 (0.12.1 L0); `security.md` again T2.6 (0.13.0 L2) |
| `internal/config/project_secrets.go` | T1.2 (0.12.1 L0), T3.1 (0.13.0 L2) |
| `internal/config/project_secrets_test.go` | T3.1 (0.13.0 L2) |
| `internal/projectpolicy/profiles.go`, `internal/projectpolicy/bitbucket.go`, `internal/projectpolicy/bitbucket_test.go` | T2.1 (L0), T3.1 (L2) |
| `internal/runner/runner.go`, `internal/runner/bitbucket_api_root_test.go` | T2.8 (L0) |
| `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`, `internal/cli/project_job.go`, `internal/cli/project_job_test.go` | T2.2 (L1) |
| `internal/cli/project_cmd.go`, `internal/cli/project_cmd_bitbucket_test.go` | T2.3 (L1) |
| `internal/cli/doctor.go`, `internal/cli/doctor_bitbucket_shared_test.go` | T2.4 (L1) |
| `internal/runner/projectjob_bitbucket_test.go` | T2.5 (L0) |
| `docs/design/project-centric.md` | T1.1 (0.12.1 L0, §6.3 note), T2.6 (0.13.0 L2) |
| `docs/project-layer.md`, `docs/plans/2026-10-14-project-centric.md`, `internal/cli/docs_project_centric_test.go` | T2.6 (L2) |

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
