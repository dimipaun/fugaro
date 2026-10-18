# Project-level named Claude OAuth tokens

**Goal:** a project (shared job) may serve **several named** Claude OAuth accounts, exactly as a
per-repository job already can: one job per `(profile, provider, account)`, the published policy
says which repositories may use which token, and each account's job gets its token by the static
`secret_env` mount — a **stopgap**, to be replaced by the security service
([../credentials-service/](../credentials-service/README.md), #512). Release 0.9.0.

**Spec:** [../design/project-named-oauth-tokens.md](../design/project-named-oauth-tokens.md)
(this plan's authority; every decision in its §8 is RULED, 2026-10-18),
[../design/named-oauth-tokens.md](../design/named-oauth-tokens.md) (the individual level this
mirrors), [../design/project-centric.md](../design/project-centric.md) §4.1 and §7,
[../design/project-secrets.md](../design/project-secrets.md).

**Depends on:** nothing outside this plan. In particular **not** the budget-token seal, PR #563,
project-centric phases 4, 5.1, 5.2, 6, or the Cloud Batch backend (design §5, D9).

**Checked before this plan was written** (on `origin/integration-0.9.0`, head `13a5a48`; no cloud
call, no secret read):

- The two refusals: `internal/projectpolicy/profiles.go:291-293` inside `validProfiles` (predicate
  `isClaudeOAuthSecret` at `:328-331`, `CheckProfiles` at `:412`), and
  `internal/cli/project_job.go:223-233` (`checkProjectJobOAuth`, called at `:105`).
- The account axis on all three sides, empty by construction: `internal/infra/projectjobs.go:96`
  (`const account = ""`), `internal/cli/project_job.go:98` and `internal/runner/projectjob.go:180`
  (both `config.ClaudeAccountOfSecrets(pr.Secrets)`), `internal/backend/gcp/names.go:152-191`.
- The carrier and its grant: a project job's `secret_env` is mounted by `secret_key_ref`
  (`deploy/terraform/gcp/modules/project/job.tf:61-74`) and its service account gets
  `secretAccessor` on exactly those secrets, one member per secret
  (`deploy/terraform/gcp/modules/project/sa.tf:48-63`).
- A named account's container is already created project-wide (`internal/infra/tfvars.go:271-276`);
  the default account's needs an explicit `project_secrets:` entry
  (`internal/config/project_secrets.go:63-71`).
- The agent's environment is an allow-list keyed on auth (`internal/agent/env.go:103-137`).
- No launcher can read a secret value (`internal/backend/gcp/run.go:361-373`, `SECURITY.md:56`),
  and nothing under `internal/` calls `AccessSecretVersion`.
- The policy's profile decoder refuses unknown fields and its rule is "a newer profile field bumps
  Version" (`internal/projectpolicy/policy.go:501-519`); `Version = 2`
  (`internal/projectpolicy/version.go:24`); the skew messages and their remedies are
  `version.go:123-172`.

## Decisions — RULED 2026-10-18

Restated from the design's §8; none is open.

- **D1 (carrier).** The static `secret_env` mount, labelled a stopgap, replaced by the security
  service as a clean swap (design §4.6). No seal, no sealed object.
- **D2 (who reads the token).** Only the account job's own service account, on only its own
  account's secret. No launcher read.
- **D3 (gate).** `accounts.<name>.allow_repos` derived at publish from `allow_data_to`,
  provider-qualified, never a wildcard; the subset rule; a profile listing accounts writes its own
  exact `allow_repos`.
- **D4 (profile field).** `profiles.<p>.claude_accounts: [...]`, one job per account; `secrets:`
  refusal unchanged; no Anthropic API-key mount beside accounts.
- **D5 (default account).** Allowed as `default`, on the profile's no-account job.
- **D6 (named container).** Unchanged: needs a non-empty `allow_data_to`.
- **D7 (re-seal, lifetime).** Moot: no sealed object.
- **D8 (SECURITY.md).** Rewritten for the static mount (design §6.1).
- **D9 (sequencing, job set).** No dependency on #563; a profile keeps its no-account job and
  gains one per listed account; nothing renamed or deleted.
- **D10 (inspection).** `fugaro project policy show` prints `accounts`; `fugaro accounts ls`
  unchanged.

## Global Constraints

1. **Every task has a red step** and every test it adds is mutation-proven: plant the bug, see the
   test fail, remove the bug.
2. **The launcher never touches the token.** No Secret Manager value read under `internal/`, no
   token in `LaunchSpec`, an override, a log line, an error or a message. A test pins the absence of
   `AccessSecretVersion` outside tests.
3. **No silent fallback.** A run that cannot use its token stops. `agent.BuildEnv`'s allow-list
   (`internal/agent/env.go:103-137`) is not widened.
4. **Every grant is a member resource on one secret**, never a binding, never project-level
   (`TestNoProjectLevelSecretAccessor`, `deploy/terraform/terraform_test.go:129`).
5. **Terraform changes run `terraform fmt`, `validate`, the root's `terraform test` and regenerate
   the golden plans** (`scripts/gen-golden-plans.sh`, `internal/infra/tf/testdata/golden/`). A plan
   that deletes or replaces a project job or a secret container is a bug (`RefuseProjectJobDeletes`).
6. **Nothing under `.github/workflows/` is created or edited.**
7. **No secret-shaped literal anywhere**; tests use `"placeholder-oauth-token"`.
8. **`GOFLAGS=-p=2 go vet ./...`, `gofmt -l`, and the tests of every package touched** before a
   task is called done.
9. **Each task merges alone and leaves the tree safe.** Tasks 1 and 2 are inert for runs: an OAuth
   launch onto a shared job stays refused until Task 3 lifts `checkProjectJobOAuth`.

## Review Focus (mutation-proof notes)

1. **The `secrets:` refusal is not lifted.** `isClaudeOAuthSecret` keeps refusing a Claude token in
   a profile's `secrets:`; the permission is the new `claude_accounts` field.
2. **One function computes a profile's accounts**, called by the deployer, the launcher and the
   runner.
3. **One function adds the token mount** (`projectAccountMount`), so the security-service swap is
   local; the job name does not depend on `secret_env`.
4. **The grant is per (account job, own secret).** A tftest proves account job A's service account
   has no member on account B's secret, and the no-account job of a profile not listing `default`
   has none on `claude-oauth-token`.
5. **The gate is at the point of use.** The runner refuses a repository the account does not cover,
   and a non-OAuth run or a mismatched selection on an account job, independently of the launcher.
6. **No API key beside the accounts**, in both spellings.
7. **No existing job is renamed.** The golden-plan diff for an installation whose profiles list no
   account is empty; a profile gaining an account plans creates only.
8. **The schema bump is real.** A version-2 reader refuses a version-3 node with the skew message,
   and a profile entry carrying `claude_accounts` is refused by a decoder that does not know it.

## PR group

| PR | Task | What it is |
| --- | --- | --- |
| 1 | 1 | **SECURITY-SENSITIVE.** The policy schema: `accounts` block, profile field, gate rules, Version 3 |
| 2 | 2 | **SECURITY-SENSITIVE (credentials carriage, IAM).** The account jobs and their static mount |
| 3 | 3 | **SECURITY-SENSITIVE.** The launch and runner gates, doctor, SECURITY.md — the feature turns on |
| 4 | 4 | Operator docs, website reference, design status |

## Known follow-ups, deliberately not in this plan

- **The security service** replaces this carrier (design §4.6); its plan is
  [../credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md).
- **Per-person ownership** (project-centric Task 5.1), **failover and usage-limit switching** for
  project accounts (Task 5.3), **Codex accounts** (Task C3), **the Cloud Batch backend**.
- **Retiring a project job.** Removing an account from a profile later plans that job's delete,
  which `RefuseProjectJobDeletes` stops.

---

### Task 1: The policy schema — SECURITY-SENSITIVE

**Files:** `internal/config/config.go` (`JobProfile` at :519), `internal/config/validate.go`,
`internal/localcfg/localcfg.go`, `internal/projectpolicy/policy.go`, `version.go`, `profiles.go`,
`accounts.go` (new), their tests, `internal/cli/project_cmd.go` (`project policy publish` and
`show`), `internal/cli/project_policy_test.go`.

**What it does:** adds `JobProfile.ClaudeAccounts []string` (`claude_accounts`), shape-checked
against `config.ClaudeAccountNameRE` / `MaxClaudeAccountNameLen` or `default`, no duplicates, and
resolved at local-config load against `lc.ClaudeAccounts` (or, for `default`, the `project_secrets`
entry). Adds `Policy.Accounts map[string]Account{AllowRepos []string}` with a strict decode (an
unknown key inside the block refused), validation (non-empty, exact, provider-qualified, no `*`),
and publish's derivation from `allow_data_to` (D3, D5), publishing only accounts some profile
names. In `validProfiles` (so publish, `init` and both readers hold one rule): every listed account
is in `accounts`; the **subset rule**; the **no-API-key rule** in both spellings (D4).
`isClaudeOAuthSecret` untouched. `projectpolicy.Version` 2 → 3 with a doc paragraph in `version.go`
beside version 2's, saying what a version-3 runner enforces that a version-2 one does not.
`policy show` prints `accounts` (D10).

**Compatibility:** per the decoder's own rule (`policy.go:501-519`) the new profile field bumps
`Version`. A version-2 runner image refuses a version-3 node with `RunnerSkew`; a version-3 CLI or
runner refuses a version-2 node with `CLISkewNewer` / `RunnerSkewNewer` (`version.go:166-172`). The
rollout — upgrade the CLI, `fugaro project policy publish --delete`, publish, refresh the runner
images — goes into the operator docs in Task 4 and into this PR's description. No migration code.

**Review (independent, opus):** this is the gate's definition. Can a profile admit a repository its
account does not cover (absent list, wildcard, case, provider mismatch)? Does any decode accept an
unknown key? Does publish emit an account no profile names?

**Tests:** `TestJobProfileClaudeAccountsShape`, `TestProfileAccountMustBeConfigured`,
`TestAccountsBlockStrictDecode`, `TestAccountAllowReposExactNonEmpty`,
`TestPublishDerivesAccounts`, `TestPublishDerivesDefaultFromProjectSecrets`,
`TestProfileAllowReposMustBeCoveredByAccount`, `TestProfileWithAccountsNeedsOwnExactAllowRepos`,
`TestProfileAccountWithAnthropicKeyRefused` (both spellings), `TestSecretsRefusalUnchanged`,
`TestPolicyVersion3Skew`, `TestPolicyShowPrintsAccounts`.

**Red step:** delete the subset check. **Expected: FAIL** —
`TestProfileAllowReposMustBeCoveredByAccount` publishes a profile admitting `github/acme/other`
beside an account listing only `github/acme/app`. Restore; it passes.

**Verification:** `GOFLAGS=-p=2 go test ./internal/config/... ./internal/localcfg/... ./internal/projectpolicy/... ./internal/cli/ -run 'Profile|Account|Policy|Version' -count=1`

**Commit:** `projectpolicy: a profile may list Claude accounts, gated by a derived accounts block`.

### Task 2: The account jobs and their static mount — SECURITY-SENSITIVE (credentials carriage, IAM)

**Files:** `internal/config/claude_accounts.go` (the shared accounts function),
`internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`,
`internal/runner/projectjob.go`, `internal/cli/project_job.go` and their tests,
`deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`,
`deploy/terraform/terraform_test.go`, `internal/infra/tf/testdata/golden/` (regenerated).
`deploy/terraform/gcp/modules/project/` is not expected to change (its `account` field and its
per-secret member exist).

**What it does:** replaces `const account = ""` with the profile's no-account job exactly as today
plus one job per listed non-default account, via the shared accounts function, which the runner's
`projectJobCandidates` and the launcher's derivation also call (replacing
`ClaudeAccountOfSecrets` there). `projectAccountMount` — the one function the security service will
replace — adds `CLAUDE_CODE_OAUTH_TOKEN` → `config.ClaudeAccountSecret(account)` to the account
job's `secret_env` (and to the no-account job's when the profile lists `default`), refusing at
`init` when the account's container is not created. `projectJobSecrets` and `projectSecretEnv` are
unchanged. The member `google_secret_manager_secret_iam_member.job` follows `secret_env`, so each
account job's service account reads only its own account's secret. `checkProjectJobOAuth` still
refuses every OAuth launch, so the jobs exist but no OAuth run reaches them.

**Review (independent, opus):** this carries a long-lived credential and adds IAM members. Is the
member set exactly (account job × its own secret)? Can any job's service account read another
account's secret? Is the mount added in one place? Is every no-account job's name byte-for-byte
unchanged, and does the golden diff for a no-account fixture stay empty?

**Tests:** `TestProjectJobAccountAxis`, `TestProjectJobNoAccountJobUnchanged`,
`TestAccountJobMountsOnlyItsOwnToken`, `TestDefaultAccountOnNoAccountJob`,
`TestAccountJobRefusedWithoutContainer`, `TestJobNameIndependentOfSecretEnv`,
`TestRunnerCandidatesIncludeAccountJobs`, `TestLauncherAccountDerivationSharedFunction`,
`TestNoSecretValueReadUnderInternal`; tftest `account_job_reads_only_own_secret`; golden plans
(no-account fixture: empty diff; account fixture: creates only).

**Red step:** make `projectAccountMount` mount every account the profile lists on every account
job. **Expected: FAIL** — `TestAccountJobMountsOnlyItsOwnToken` and the tftest go red. Restore;
both pass.

**Verification:** `GOFLAGS=-p=2 go test ./internal/config/... ./internal/infra/... ./internal/runner/ ./internal/cli/ ./deploy/terraform/ -run 'ProjectJob|Account|Candidate|Secret' -count=1`, then `terraform -chdir=deploy/terraform/gcp/roots/installation init -backend=false && terraform -chdir=deploy/terraform/gcp/roots/installation test`, then `scripts/gen-golden-plans.sh && git diff --stat internal/infra/tf/testdata/golden/`.

**Depends on:** Task 1.

**Commit:** `infra: one project job per Claude account, its token by static mount (stopgap)`.

### Task 3: The gates, doctor and SECURITY.md — SECURITY-SENSITIVE

**Files:** `internal/cli/project_job.go`, `internal/cli/project_job_test.go`,
`internal/cli/doctor.go`, `internal/cli/doctor_test.go`, `internal/runner/projectjob.go` and its
tests, `SECURITY.md`, `internal/cli/docs_project_named_oauth_tokens_test.go` (new).

**What it does:** retires `checkProjectJobOAuth` and replaces it with the gate: an OAuth launch onto
a shared job is allowed when the profile lists the run's selected account (`agent.oauth_token` /
`--token`, `default` when neither) and the repository is in that account's `allow_repos`; it then
targets that account's job. A non-OAuth launch never targets an account job.
`checkProjectJobAccount` compares against the selected account's job. The runner refuses, on its
own: a run on an account job whose auth is not `oauth` or whose selection is not the job's account,
and a repository the policy's `accounts` block does not cover for the job's account. `fugaro
doctor` gains a project-account check (each listed account's container has a stored version,
read through the launcher's existing list permission, never a value), with
`claude-accounts-stored`'s severity discipline (`internal/cli/doctor.go:1201-1279`). SECURITY.md
gains design §6.1 (D8) — no launcher read; every run of an account's job holds its token; a
hostile run can use it during its own run; the account is the blast radius; a stopgap pending the
security service — and keeps `SECURITY.md:56`'s "only the job and build accounts hold" sentence.
It ships here because the wording is the feature's cost.

**Review (independent, opus):** does every §4.4 row refuse; can any path run an OAuth run on the
no-account job of a profile that does not list `default`; is the runner's refusal independent of
the launcher's; does `--token` naming an unlisted account refuse rather than pick another?

**Tests:** `TestProjectJobOAuthAllowedWhenAccountCovered`, `TestProjectJobOAuthSelectsAccountJob`,
`TestProjectJobOAuthRefusedAccountNotListed`, `TestProjectJobOAuthRefusedRepoNotAllowed`,
`TestNonOAuthNeverTargetsAccountJob`, `TestRunnerRefusesNonOAuthOnAccountJob`,
`TestRunnerRefusesUncoveredAccount`, `TestDoctorProjectAccountStored`, and the docs test pinning
SECURITY.md's residual and stopgap sentences in the house style of
`internal/cli/docs_named_oauth_tokens_test.go`.

**Red step:** restore `checkProjectJobOAuth`'s unconditional refusal beside the new gate.
**Expected: FAIL** — `TestProjectJobOAuthAllowedWhenAccountCovered`. Remove it; it passes. Then
delete SECURITY.md's "hostile run" sentence: the docs test fails. Restore.

**Verification:** `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/runner/ -run 'ProjectJob|Account|Doctor|Docs' -count=1`

**Depends on:** Tasks 1, 2.

**Commit:** `cli: a shared project job may serve named Claude accounts`.

### Task 4: Operator docs and status

**Files:** `docs/gcp-setup.md` (the profile field, the project-wide storage step, the Version 3
rollout), `docs/design/named-oauth-tokens.md` (the "project level" pointer),
`docs/design/project-named-oauth-tokens.md` (Status: built in 0.9.0, stopgap carrier),
`website/` (CLI reference, if help text changed).

**What it does:** documents what Tasks 1-3 built, with the stopgap stated where the operator meets
it.

**Red step:** extend the Task 3 docs test to pin the gcp-setup rollout sentence; delete it.
**Expected: FAIL.** Restore.

**Verification:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1`

**Depends on:** Task 3.

**Commit:** `docs: project Claude accounts for operators`.

---

## What could not be verified without a live check

From the design's §7: **U3** whether one account's token serves several concurrent runs (a sizing
question); **U5** whether the account jobs' members plan as pure creates (Task 2's golden plans
answer it). **U2** is not needed. U1 and U4 of the earlier draft are moot (no override, no seal).
All are owner-run, sandbox only, and none blocks Task 1.
