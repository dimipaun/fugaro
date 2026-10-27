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
  - **D462:** the reach risk is accepted, with the allow-list, minimum scopes and a plain statement.
  - **D463:** 0.12.1 is quick, and anything big goes to 0.13.0.
- **Pending decisions:** D464–D468, D473 (proposed) and D474–D477. Each task lists the decisions it waits on.
- **Review round 1 (2026-10-27)** amended D465's recommendation before any answer, added F17,
  F18, D474, O4 and O5, and reshaped T1.2, T2.1, T2.5 and T1.3 (see the PR).
- **Review round 2 (2026-10-27)** amended D465's recommendation again before any answer. The node now
  carries a derived Bitbucket block that the runner enforces (T2.9, the accounts precedent).
  O4 and O5 became D475 and D476, and D473 became a proposal. T2.5 was folded into T2.9, and
  T2.10 was added.
- **Review round 3 (2026-10-27, the last)** made the version bump a decision (D477), put the D465
  call sites and compile order right, and added the remaining pins. What it raised and this PR did
  not settle is listed in the PR's open items.

| Release | Tasks |
| --- | --- |
| **0.12.1** | T1.1 (docs), T1.2 (CLI text), T1.3 (Check 180 written, only if D468 = "before"), T1.4 (Check 180, OWNER-RUN, only if D468 = "before") |
| **0.13.0** | T2.1–T2.4, T2.6, T2.8 (if D474), T2.9, T2.10 (if D476), T2.7 (Check 181, OWNER-RUN), T3.1 (only if D464 = "pattern"). T1.3 and T1.4 move here, unchanged, if D468 = "after" |

## Conventions every part follows

- `CLAUDE.md`, **Things that bite**:
  - Docs are tested. Run `go test ./internal/cli/ -run Docs` after any doc change. Run
    `npm run check` in `website/` when `website/content/` changes.
  - Comments explain why.
  - A fix round adds no functionality.
- `.claude/rules/terraform.md` loads for `internal/infra/**`. The plan changes no `.tf` file:
  `modules/project/job.tf` and `sa.tf` already iterate a job's `secret_env`.
- `.claude/rules/credentials-service.md` loads for `SECURITY.md` and `website/content/security.md`
  (T1.1, T2.6). Both tasks take the opus tier with mandatory independent review.
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

1. **No behaviour change in 0.12.1.** T1.2 changes only help text, comments and message wording,
   and its reviewer checks that the diff touches no condition. The existing `initsecrets` and
   `secrets` tests pin this by passing unchanged.
2. **The allow-list remains the only way a Bitbucket token becomes project-wide.** It is never a
   default, and `ProjectScopeByDefault("bitbucket-token")` stays false. Pinned by
   `internal/config/project_secrets_test.go` (unchanged) and by T2.2.
3. **A GitHub profile never mounts `bitbucket-token`.** The policy node's own rule refuses it, so
   policy and deploy judge it by one rule. Pinned by T2.1:
   `TestValidProfilesRefuseBitbucketTokenOnAGitHubProfile`.
4. **On a Bitbucket shared job, the runner refuses a repository the published
   `bitbucket_token.allow_repos` block does not name**, before the provider opens (D465 as amended).
   Deploy and publish run the same subset check. Whoever publishes is trusted. A launcher is not
   bounded by this check either way; whether that is accepted is D475. Pinned by T2.1, T2.2, T2.3 and T2.9.
5. **An installation with only Bitbucket profiles needs no GitHub App ID.** Pinned by T2.2:
   `TestProjectJobsBitbucketOnlyNeedsNoGitHubAppID`.
6. **The deployer and the launcher ask one predicate.** If they disagree, a launch targets a job
   that does not exist. Pinned by T2.2: `TestLaunchPutsABitbucketProfileOnItsProjectJob` and
   `TestLaunchKeepsABitbucketProfileWithoutTheTokenOnThePerRepositoryJob`. **Known drift, not
   closed:** the deployer reads the local profile and the launcher the published one. That holds
   for GitHub profiles today, and T2.6 documents it.
7. **The reach statement is in `SECURITY.md` in plain words** (D462). It covers agents, the
   cross-repository paths, and every other credential of an allow-listed repository. It covers
   launchers as accepted only if D475 = "yes"; otherwise it states them as an open risk.
   Pinned by T1.1: `TestDocsStateTheBitbucketProjectTokenReach`.
8. **A launcher is not bounded by admission** (F7). T2.8 (the API-root pin) pins the redirect half.
   The rest is a statement (T1.1).
9. **An older runner never runs a Bitbucket shared job.** A shared job's base image must be at or
   above the first release that carries T2.9 and T2.8 (the mechanism is D477's). Pinned by T2.2:
   `TestUpdateRefusesABitbucketTokenProfileOnAnOldBase`.

## Slices

- **S1 (0.12.1): one token, stored once.** Docs and CLI text only. Useful on its own, because the
  mechanism already exists (F1–F4).
- **S2 (0.13.0): Bitbucket in shared project jobs** (D460). Inert until an operator writes a
  Bitbucket profile that mounts `bitbucket-token`, provided D477 = "no bump". A bump touches every
  installation with a published policy.
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
  - `internal/cli/docs_credentials_github_tokens_test.go`: it pins "**On Bitbucket it IS this
    repository's access token**". The pin keeps its fact ("the mounted token's own value"),
    reworded in both places.
  - `website/src/pages/getting-started.astro`: "a repository access token on Bitbucket".
  - `SECURITY.md`'s "project scope is the default" paragraph and its review-mode premise ("pushing
    one already needs write access"), both reworded.
  - `docs/bitbucket-credentials/README.md` and `reconciliation.md`: a dated note that D462 reverses
    D363 for operators who opt in. The `docs_bitbucket_credentials_test.go` pins stay.
  - In `docs/git-providers.md`'s phase-3 paragraph, only the sentence "the project-wide token an
    earlier design assumed therefore does not exist … nothing to configure for one". The
    shared-job claims stay for T2.6.
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

    For the daily image check, the base must be at 0.7.5 or later (F3).
  - **The reach, in plain words.** Every allow-listed run's agent holds the token's value. **So does
    every launcher of an allow-listed repository** (F7; written as accepted only if D475 = "yes").
    Through pushes to another repository's base or Fugaro branches, an agent also reaches **every
    other credential** that repository's runs and builds hold. A hostile agent or launcher can push,
    open and comment on pull requests, and trigger Pipelines in every repository the token reaches,
    including repositories not enrolled in Fugaro.
  - **Cross-repository paths** (F17). A pull request one repository's agent opens in another is
    trusted by that repository's review-only runs. Never put the token's own account ID in
    `followup.trusted`.
  - **Hardening:** minimum scopes and an optional expiry.
  - **Rotation:** store a new version, wait for running executions to end, then revoke the old
    token.
  - **Migration:** a repository's own copy wins until its versions are disabled **and** its
    `fugaro update --repo --allow-delete` has run (F4).
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
  - D475: launchers named as accepted.
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
  - `internal/gitprov/providers/providers.go` and `internal/gitprov/bitbucket/bitbucket.go`: the
    missing-token messages ("a Bitbucket access token"), wording only.
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

- **Files:** `docs/gcp-live-checklist.md` (a new `## Check 180` section and its row),
  `docs/coordinator/README.md` (its "highest check section written" pin moves to 180, which
  `docs_coordinator_test.go` compares with the register), and
  `internal/cli/docs_checks_180_181_test.go` (new: `TestDocsChecks180181AreRegistered`, on the
  pattern of `TestDocsChecks145149AreRegistered`).
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
  6. On a second machine seeded with `fugaro update --gcp-project`, without the entry, runs
     `fugaro update --repo --plan-only` and records what it plans (U5).
- **Tests:** `go test ./internal/cli/ -run Docs` (the register's row and section pins).
- **Red step:** write `TestDocsChecks180181AreRegistered` naming Check 180 first. It fails until the
  section and its row exist.
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
- **Depends on:** T1.1, T1.2 and T1.3, released or a dev build, plus U6.

### T2.1: The node's Bitbucket block and profile rules (0.13.0)

- **Files:**
  - `internal/projectpolicy/policy.go`: a new `BitbucketToken` field on `Policy`, holding
    `bitbucket_token.allow_repos`.
  - `internal/projectpolicy/profiles.go`: `validProfiles`, and `providerRequired`'s message.
  - `internal/projectpolicy/version.go`: only if D477 = "bump" (then also `VersionSince[4]`, and
    every test that hard-codes version 3 across `internal/cli`, `internal/runner` and
    `internal/credsvc`, which turns this task into a lockstep release).
  - `internal/projectpolicy/bitbucket.go` (new).
  - `internal/projectpolicy/bitbucket_test.go` (new).
  - the next CLI release notes' `### For operators` entry.
- **Interfaces produced:** T2.2, T2.3, T2.4 and T2.9 consume these.
  - `projectpolicy.SharedBitbucketProfile(p config.JobProfile) bool` is true when
    `p.Provider == "bitbucket"` and `p.Secrets` contains `bitbucket-token` (D473, pending).
  - `projectpolicy.DeriveBitbucketToken(profiles map[string]Profile, secrets map[string]config.ProjectSecret) (*BitbucketToken, error)`
    turns `project_secrets.bitbucket-token.allow_data_to` into exact `bitbucket/owner/name`
    entries. Like `DeriveAccounts`, it returns nil when no profile mounts the token, because every
    run reads the node, and it returns `Canonical`'s errors.
  - **The node's own rules**, applied in `Decode`'s validation of the whole policy (the block is
    a node-level field), which the runner and the launcher decode with. `CheckProfiles`'s
    signature is unchanged, so `internal/infra/projectjobs.go` still compiles at this level:
    - A profile that mounts `bitbucket-token` writes its own `allow_repos`. The list is non-empty
      and exact, with no wildcard.
    - That list is a subset of `bitbucket_token.allow_repos`.
    - `bitbucket-token` on a GitHub profile is refused.
    - Admission stays the intersection of the project's list and the profile's own
      (`Policy.Admits`), so `fugaro project repos add` cannot widen it.
- **Schema/rules/config change:** the policy node's shape and validation. The node has no JSON
  Schema under `schemas/`. Whether the version moves is D477.
- **Rollout:** this task's PR carries a `### For operators` entry. A node that today holds a GitHub
  profile mounting `bitbucket-token`, or a Bitbucket profile mounting it without its own list, is
  refused by the new runner as a whole, which would halt every run of the project. Those profiles
  are fixed and republished before the runners are upgraded.
- **Tests:**
  - `TestValidProfilesRequireAnOwnExactListForTheBitbucketToken`
  - `TestValidProfilesRefuseBitbucketTokenOnAGitHubProfile`
  - `TestValidProfilesRefuseAProfileListBeyondTheBitbucketBlock`
  - `TestDeriveBitbucketTokenQualifiesTheSlugs`
  - `TestSharedBitbucketProfileNeedsTheTokenMount`
  - `TestAdmitsIntersectsABitbucketTokenProfile`. Its fixture is a profile with its own list and a
    wider project list, as `repos add` would leave it, and it pins the intersection.
    (`runProjectReposEdit` itself is exercised by T2.3's tests.)
- **Red step:** the validation tests fail against today's `validProfiles`, which accepts both
  shapes.
- **Command:** `go test ./internal/projectpolicy/ -count=1`. With D477 = "bump", add
  `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/runner/ ./internal/credsvc/ -count=1 -timeout 3600s`.
- **Depends on:** D465, D473 and D477 answered. No task depends on it from S1.
- **Commit:** `projectpolicy: the node carries the Bitbucket token's allow-list, and a profile that mounts it names its own repositories within it`
- **Reviewer tier:** opus + mandatory independent review.

### T2.2: Deploy and launch Bitbucket shared jobs (0.13.0)

- **Files:**
  - `internal/infra/projectjobs.go`
  - `internal/infra/projectjobs_test.go`
  - `internal/cli/project_job.go`
  - `internal/cli/project_job_test.go`
  - `internal/cli/init_fugaroyaml.go` (the floor, if D477 = "no bump")
  - `docs/release.md` (the Verify bullet)
  - the next CLI release notes' `### For operators` entry
- **Interfaces consumed:** T2.1.
- **Interfaces produced:** `infra.SharedJobProfile(pr config.JobProfile) bool`, which is GitHub, or
  `projectpolicy.SharedBitbucketProfile`. It replaces `SharedJobProvider` at its three call sites:
  `ProjectJobs`, its App-ID guard and `launchProjectJobFor`.
- **Behaviour:**
  - `ProjectJobs` deploys a job for a profile that mounts the token.
  - Before deploying, it runs the subset check against the local `DeriveBitbucketToken`.
  - The App-ID guard becomes `pr.Provider == "github"`.
  - A base-image floor (Global Constraint 9), only if D477 = "no bump". `imagePredates` lives in
    `internal/cli` (`init_fugaroyaml.go`), so the floor is checked in the CLI's update path rather
    than in `ProjectJobs`. Its pin is the CLI test `TestUpdateRefusesABitbucketTokenProfileOnAnOldBase`,
    and `docs/release.md` gains a "Verify" bullet.
  - `projectJobSecrets`'s own `bitbucket-token` refusal is removed, because `CheckProfiles` now
    refuses first and the branch would be dead. That is the dead-refusal pattern its comment warns
    about.
  - The comments cite D460–D462.
  - The `### For operators` entry covers a local config whose Bitbucket profile already lists
    `bitbucket-token`: `ProjectJobs` skips such a profile today, so `fugaro update` would now deploy
    it or refuse it.
- **Schema/rules/config change:** none. `modules/project` already grants `secretAccessor` per
  `secret_env` entry.
- **Tests:**
  - `TestProjectJobsDeployABitbucketProfileThatMountsTheToken`, replacing
    `TestProjectJobsSkipBitbucketProfiles`
  - `TestProjectJobsSkipABitbucketProfileWithoutTheToken`
  - `TestProjectJobsBitbucketOnlyNeedsNoGitHubAppID`
  - `TestProjectJobsRefuseABitbucketProfileAdmittingAnUnlistedRepository`
  - `TestUpdateRefusesABitbucketTokenProfileOnAnOldBase` (in `internal/cli/project_job_test.go`)
  - `TestSharedJobProviderIsTheProvidersList`, reshaped for `SharedJobProfile`
  - `TestLaunchPutsABitbucketProfileOnItsProjectJob`, replacing
    `TestLaunchKeepsABitbucketProfileOnThePerRepositoryJob`
  - `TestLaunchKeepsABitbucketProfileWithoutTheTokenOnThePerRepositoryJob`
  - `TestLaunchKeepsAnotherProvidersRepositoryOnItsOwnJob`, unchanged
- **Red step:** the deploy test fails on today's skip, and the launcher test fails on today's
  per-repository job.
- **Command:**
  `go test ./internal/infra/ -run 'ProjectJob|SharedJob' -count=1 && GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestLaunch' -count=1`
- **Depends on:** T2.1.
- **Commit:** `infra, cli: Bitbucket profiles that mount the workspace or project token get a shared project job`
- **Reviewer tier:** opus + mandatory independent review.

### T2.3: Policy publish derives the block and runs the check (0.13.0)

- **Files:** `internal/cli/project_cmd.go` (beside `DeriveAccounts` in the publish path) and
  `internal/cli/project_cmd_bitbucket_test.go` (new).
- **Interfaces consumed:** T2.1.
- **Tests:**
  - `TestPolicyPublishWritesTheBitbucketTokenBlock`
  - `TestPolicyPublishRefusesABitbucketProfileAdmittingAnUnlistedRepository`
  - `TestPolicyPublishAcceptsAnAllowListedBitbucketProfile`
- **Red step:** `...WritesTheBitbucketTokenBlock` and `...AcceptsAnAllowListedBitbucketProfile` fail
  first: after T2.1 the decoder refuses a token profile with no block, so publish fails until it
  derives one. The refusal test may already pass through T2.1 and stays as a pin.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestPolicyPublish.*Bitbucket' -count=1`
- **Depends on:** T2.1.
- **Commit:** `cli: project policy publish carries the Bitbucket token's allow-list and refuses a profile beyond it`
- **Reviewer tier:** opus + mandatory independent review.

### T2.4: `fugaro doctor` warns about an own copy a shared job ignores (0.13.0)

- **Files:** `internal/cli/doctor.go` and `internal/cli/doctor_bitbucket_shared_test.go` (new).
- **Behaviour (D466):** "admitted" means admitted by the **published** node, read the way the
  launcher reads it. For each repository admitted to a profile that mounts the token, doctor checks
  whether the repository has an enabled version of its own `bitbucket-token` copy, reading metadata
  only, as `doctorProjectSecrets` does. If it does, doctor warns: "its shared job uses the project
  token; its own copy applies only to its per-repository jobs".
- **Tests:** `TestDoctorWarnsABitbucketOwnCopyOnASharedProfile` and
  `TestDoctorIsQuietWithoutABitbucketOwnCopy`.
- **Red step:** the warning test fails, because there is no warning today.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDoctor.*Bitbucket' -count=1`
- **Depends on:** D466 answered, and T2.1, because it reads `SharedBitbucketProfile`.
- **Commit:** `doctor: a Bitbucket repository's own token is not what its shared job uses`
- **Reviewer tier:** standard.

### T2.5: folded into T2.9 (review round 2)

The runner's characterization tests now sit beside the runner check that makes them fail first. The
number is not reused.

### T2.6: Docs: phase 3 serves Bitbucket too (0.13.0)

- **Files:**
  - `docs/git-providers.md`:
    - "Bitbucket stays on the per-repository path" (its shared-job claims only, since T1.1 already
      rewrote the slice-1 sentence);
    - the `FUGARO_BITBUCKET_API_URL` variables-table row (T2.8).
  - `docs/design/project-centric.md`: dated amendments to §4.1 "GitHub only" and §4.5/D5.
  - `docs/project-layer.md`: "Bitbucket repositories never reach one", plus Global Constraint 6's
    known drift.
  - `docs/gcp-live-checklist.md`: a dated note under Check 52 §2, and the Check 181 section.
    **Trap:** the Check 52 pins in `docs_project_centric_test.go` ban "project access token" and
    "bitbucket.org" anywhere in that section. The note says "the 2026-10-27 rulings D460–D462" and
    links to this folder instead.
  - `docs/plans/2026-10-14-project-centric.md`: a dated note.
  - `SECURITY.md` and `website/content/security.md` (regenerated): a shared Bitbucket job's
    isolation (F12).
  - `internal/cli/docs_project_centric_test.go`: a pin for the dated note.
  - `internal/cli/docs_project_wide_git_test.go`.
  - `internal/cli/docs_checks_180_181_test.go`: Check 181's registration.
  - `docs/coordinator/README.md`: the "highest check section written" pin moves to 181.
- **Tests:**
  - `TestDocsStateBitbucketSharedJobIsolation` (new)
  - the Check 52 pins
  - `TestDocsChecks180181AreRegistered`
  - `TestDocsStateTheBitbucketProjectTokenReach`
- **Red step:** the new pins fail first.
- **Command:**
  `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|TestWebsiteSecurityPage|TestSecurityMd' -count=1 -timeout 1800s && go test ./internal/clidoc/ -count=1 && cd website && npm run check`
- **Depends on:**
  - T2.2, T2.3, T2.4, T2.8, T2.9 and T2.10, because the docs describe shipped code;
  - T1.1 and T1.3, which write the same files in an earlier release or level.
- **Commit:** `docs(bitbucket): shared project jobs serve Bitbucket profiles that mount the workspace or project token`
- **Reviewer tier:** opus + mandatory independent review (a `SECURITY.md` change;
  `.claude/rules/credentials-service.md` loads).

### T2.7: Check 181, OWNER-RUN (0.13.0)

- **OWNER-RUN, sandbox only.** Check 181 runs as follows:
  1. A Bitbucket profile that mounts `bitbucket-token` admits two allow-listed sandbox
     repositories.
  2. `fugaro update` deploys one `fugaro-p-…` job.
  3. A run of each repository executes on that job and opens its pull request.
  4. A third sandbox repository, not allow-listed, is tested two ways:
     - added to the profile, it makes `fugaro project policy publish` and `fugaro update` refuse;
     - launched by hand against the job, it is refused by the runner (T2.9).
- The owner types the commands T2.6's section gives. No agent performs any step.
- **Depends on:** T2.6, released or a dev build, plus U6.

### T2.8: A cloud run pins the Bitbucket API root (0.13.0, only if D474 = "yes")

- **Files:** `internal/runner/runner.go` (`openProvider`) and
  `internal/runner/bitbucket_api_root_test.go` (new).
- **Behaviour:** outside a local invocation, a run refuses any `FUGARO_BITBUCKET_API_URL` other than
  `https://api.bitbucket.org/2.0`, before the provider opens. `FUGARO_GITHUB_API_URL` is out of
  scope (D474).
- **Tests:** `TestCloudRunRefusesAnotherBitbucketAPIRoot` and
  `TestLocalRunKeepsTheBitbucketAPIRootOverride`.
- **Red step:** the refusal test fails, because today the override is honoured.
- **Command:** `go test ./internal/runner/ -run 'BitbucketAPIRoot' -count=1 -timeout 600s`
- **Depends on:** D474.
- **Commit:** `runner: a cloud run sends its Bitbucket token only to Bitbucket's own API`
- **Reviewer tier:** opus + mandatory independent review.

### T2.9: The runner enforces the Bitbucket block on a shared job (0.13.0)

- **Files:** `internal/runner/projectjob.go` (the check, beside `checkProjectJobAccount`),
  `internal/runner/project.go` (the call, at the end of `admissionCheck`, which runs before
  `openProvider`, so T2.8's `runner.go` is untouched), and `internal/runner/projectjob_bitbucket_test.go` (new).
- **Behaviour (D465 as amended):** on a project job whose provider is `bitbucket`, after
  `admissionCheck` and before `openProvider`, the run checks its repository against the published
  `bitbucket_token.allow_repos`. A node with no block refuses the run.
- **Tests:**
  - `TestBitbucketProjectJobRefusesARepositoryOffTheTokenBlock`
  - `TestBitbucketProjectJobRefusesWithoutABlock`
  - `TestBitbucketProjectJobAdmitsAListedRepository`
  - `TestBitbucketProjectJobRefusesAGitHubRemote`
  - `TestBitbucketProjectJobOpensTheRunsOwnRepository`

  The last three are the characterization tests that came from T2.5.
- **Red step:** `TestBitbucketProjectJobRefusesARepositoryOffTheTokenBlock` fails, because no such
  check exists today. `...RefusesWithoutABlock` uses the drift fixture: a job named for a token
  profile while the published node's profile no longer mounts it, so the node decodes and only
  the new check refuses.
- **Command:** `go test ./internal/runner/ -run 'TestBitbucketProjectJob' -count=1 -timeout 600s`
- **Depends on:** T2.1.
- **Commit:** `runner: a Bitbucket shared job runs only repositories the token's published allow-list names`
- **Reviewer tier:** opus + mandatory independent review.

### T2.10: Review-only runs refuse the token's own pull requests (0.13.0, only if D476 = "yes")

- **Files:** `internal/runner/reviewmode.go` and `internal/runner/reviewmode_bitbucket_test.go`
  (new).
- **Behaviour:** on Bitbucket, a review-only run refuses a pull request whose author's `account_id`
  equals the token's own identity, when that identity is known (U2).
- **Tests:** `TestReviewOnlyRefusesThePullRequestOfTheTokensOwnIdentity` and
  `TestReviewOnlyKeepsAPersonsSameRepositoryPullRequest`.
- **Red step:** the refusal test fails, because today such a pull request is reviewed.
- **Command:** `go test ./internal/runner/ -run 'TestReviewOnly.*(Token|Person)' -count=1 -timeout 600s`
- **Depends on:** D476 and T1.4 (Check 180 settles U2). If Check 180 finds the token's identity
  unknown (`GET /user` refused, as for repository tokens), the check can never fire live, and T2.10
  is dropped rather than built.
- **Commit:** `runner: a Bitbucket review-only run never trusts a pull request its own token opened`
- **Reviewer tier:** opus + mandatory independent review.

### T3.1: A workspace pattern in the allow-list (0.13.0, only if D464 = "pattern")

- **Files:**
  - `internal/config/project_secrets.go`: `AllowsData` and `ValidateProjectSecrets` accept
    `<workspace>/*` for `bitbucket-token` only.
  - `internal/config/project_secrets_test.go`.
  - `internal/projectpolicy/bitbucket.go`: `DeriveBitbucketToken` carries the pattern. A
    profile's own list stays exact.
  - `internal/runner/projectjob.go`: T2.9's check matches the pattern with `MatchRepo`.
  - `docs/git-providers.md`: the pattern's sentence.
  - There is no schema file: the local config has no JSON Schema under `schemas/` (corrected in
    review round 1).
- **Red step:** `TestProjectSecretWorkspacePatternAdmitsItsRepositories` fails, because `*` is
  refused today.
- **Command:** `go test ./internal/config/ ./internal/projectpolicy/ -run 'ProjectSecret|Bitbucket' -count=1`
- **Depends on:** T2.1 and T2.9 (the same files and the same check).
- **Commit:** `config: project_secrets.bitbucket-token may allow-list a whole workspace`
- **Reviewer tier:** opus + mandatory independent review.

## Parallel execution

**Edges.**

| Task | Depends on |
| --- | --- |
| T1.4 | T1.1, T1.2, T1.3 |
| T2.2 | T2.1 |
| T2.3 | T2.1 |
| T2.4 | T2.1 |
| T2.9 | T2.1 |
| T2.10 | T1.4 |
| T2.6 | T1.1, T1.3, T2.2, T2.3, T2.4, T2.8, T2.9, T2.10 |
| T2.7 | T2.6 |
| T3.1 | T2.1, T2.9, T2.6 |

**Levels.**

| Release | Level | Tasks (concurrent) |
| --- | --- | --- |
| 0.12.1 | L0 | T1.1, T1.2, T1.3 |
| 0.12.1 | L1 | T1.4 (OWNER-RUN) |
| 0.13.0 | L0 | T2.1, T2.8 (and T1.3 if D468 = "after") |
| 0.13.0 | L1 | T2.2, T2.3, T2.4, T2.9, T2.10 (T2.10 after T1.4; and T1.4 itself if D468 = "after", which then pushes T2.10 to L2) |
| 0.13.0 | L2 | T2.6 |
| 0.13.0 | L3 | T2.7 (OWNER-RUN), T3.1 |

**Critical path.**

- **0.12.1:** T1.1. With D468 = "before": T1.3 → T1.4.
- **0.13.0:** T2.1 → T2.2 (or T2.9) → T2.6 → T2.7.
- What must be sequential, and why:
  - T2.2, T2.3, T2.4 and T2.9 consume T2.1's interface.
  - T2.6 must describe shipped code.
  - T3.1 edits T2.1's, T2.9's and T2.6's files.

**Shared-files table.** No level holds two writers of one file.

| File | Writers (release, level) |
| --- | --- |
| `docs/git-providers.md` | T1.1 (0.12.1 L0), T2.6 (0.13.0 L2), T3.1 (0.13.0 L3) |
| `SECURITY.md`, `website/content/security.md`, `internal/cli/docs_project_wide_git_test.go` | T1.1 (0.12.1 L0), T2.6 (0.13.0 L2) |
| `docs/gcp-live-checklist.md`, `internal/cli/docs_checks_180_181_test.go` | T1.3 (0.12.1 L0, or 0.13.0 L0), T2.6 (0.13.0 L2) |
| `docs/design/project-centric.md` | T1.1 (0.12.1 L0, §6.3 note), T2.6 (0.13.0 L2) |
| `docs/design/project-secrets.md`, `plugin/skills/setup/*`, `README.md`, `docs/multi-model.md`, `internal/cli/docs_credentials_github_tokens_test.go`, `website/src/pages/getting-started.astro`, `docs/bitbucket-credentials/README.md`, `docs/bitbucket-credentials/reconciliation.md` | T1.1 (0.12.1 L0) |
| `docs/coordinator/README.md` | T1.3 (0.12.1 L0, or 0.13.0 L0), T2.6 (0.13.0 L2) |
| `internal/gitprov/bitbucket/bitbucket.go` | T1.2 (0.12.1 L0) |
| `internal/cli/init_fugaroyaml.go`, `docs/release.md` | T2.2 (L1) |
| `internal/runner/project.go` | T2.9 (L1) |
| `internal/cli/secrets.go`, `internal/cli/init.go`, `internal/config/config.go`, `internal/config/providers.go`, `internal/infra/spec.go`, `internal/gitprov/providers/providers.go`, `website/content/cli-reference.md`, `internal/cli/secrets_bitbucket_text_test.go` | T1.2 (0.12.1 L0) |
| `internal/config/project_secrets.go` | T1.2 (0.12.1 L0), T3.1 (0.13.0 L3) |
| `internal/config/project_secrets_test.go` | T3.1 (0.13.0 L3) |
| `internal/projectpolicy/policy.go`, `profiles.go`, `version.go`, `bitbucket_test.go` | T2.1 (L0) |
| `internal/projectpolicy/bitbucket.go` | T2.1 (L0), T3.1 (L3) |
| the next CLI release notes file | T2.1 (L0), T2.2 (L1) |
| `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`, `internal/cli/project_job.go`, `internal/cli/project_job_test.go` | T2.2 (L1) |
| `internal/cli/project_cmd.go`, `internal/cli/project_cmd_bitbucket_test.go` | T2.3 (L1) |
| `internal/cli/doctor.go`, `internal/cli/doctor_bitbucket_shared_test.go` | T2.4 (L1) |
| `internal/runner/runner.go`, `internal/runner/bitbucket_api_root_test.go` | T2.8 (L0) |
| `internal/runner/reviewmode.go`, `internal/runner/reviewmode_bitbucket_test.go` | T2.10 (L0) |
| `internal/runner/projectjob.go` | T2.9 (L1), T3.1 (L3) |
| `internal/runner/projectjob_bitbucket_test.go` | T2.9 (L1) |
| `docs/project-layer.md`, `docs/plans/2026-10-14-project-centric.md`, `internal/cli/docs_project_centric_test.go` | T2.6 (L2) |

## Checks this plan takes

| Check | Measures | Who | When |
| --- | --- | --- | --- |
| **180** | A project or workspace token: no prompt, clone and push, PR author, `GET /user`, scopes, rotation (U1–U4, U7) | the owner, sandbox only | Before 0.12.1 if D468 = "before", otherwise 0.13.0 |
| **181** | A Bitbucket shared project job end to end, and the allow-list refusal at publish, at deploy and in the runner | the owner, sandbox only | 0.13.0 |

Checks 182–189 are reserved and unused.

## Considered and declined

- **Publishing `project_secrets` so that teammates' machines see the allow-list** (D467): this would
  reverse `sharedForbidden`'s reason.
- **An image-build task:** the build already clones with the resolved project copy (F2), and a
  shared job builds no per-repository image.
- **A credentials-service task:** the service holds no Bitbucket credential, and this work mints
  nothing (F14). docs/bitbucket-credentials' S3 stays parked.
