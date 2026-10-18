# Project-level named Claude OAuth tokens

**Goal:** a project (shared job) may serve **several named** Claude OAuth accounts, exactly as a
per-repository job already can: one job per `(profile, provider, account)`, the published policy
says which repositories may use which token, and the token reaches the run through the budget-token
seal rather than through the job's environment. Release 0.9.0.

**Spec:** [../design/project-named-oauth-tokens.md](../design/project-named-oauth-tokens.md)
(this plan's authority; its §8 decisions are the ones this plan's Decisions section restates),
[../design/named-oauth-tokens.md](../design/named-oauth-tokens.md) (the individual level this
mirrors), [../design/project-centric.md](../design/project-centric.md) §4.1 and §7,
[../design/project-secrets.md](../design/project-secrets.md),
[../design/budget-token-channel.md](../design/budget-token-channel.md) (the channel the carrier
borrows).

**Depends on:** the seal — `origin/next-0.9` Slice A and B, being merged into `integration-0.9.0`
by PR #563, which also gives the shared job account its read-only grant on `tokens/` (commit
`cda64c5a`). Task 1 below is the gate that checks it landed. Nothing else — in particular **not**
project-centric phases 4, 5.1, 5.2, 6 or the Cloud Batch backend, all of which are out of 0.9.0 or
shown unnecessary in the design's §5.

**Checked before this plan was written** (on `origin/integration-0.9.0`, head `13a5a48`, this
branch's base; no cloud call, no secret read):

- The two refusals exist and are where the design says: `internal/projectpolicy/profiles.go:292`
  inside `validProfiles` (predicate `isClaudeOAuthSecret` at `:328-331`, `CheckProfiles` at `:412`),
  and `internal/cli/project_job.go:223-233` (`checkProjectJobOAuth`, called at `:105`).
- The account axis exists on all three sides and is empty by construction:
  `internal/infra/projectjobs.go:96` (`const account = ""`), `internal/cli/project_job.go:98` and
  `internal/runner/projectjob.go:180` (both `config.ClaudeAccountOfSecrets(pr.Secrets)`, which can
  only answer `""` while `secrets:` refuses every Claude token), `internal/runner/projectjob.go:74-100`
  (`jobIdentity` returns the account), `internal/backend/gcp/names.go:152-191`.
- A named account's token is already storable project-wide and already gets a project-scope Secret
  ID: `internal/cli/secrets.go:641-643`, `internal/infra/tfvars.go:271-276`,
  `internal/backend/gcp/names.go:207-209` (`ProjectSecretID`).
- The bare `claude-oauth-token` needs an explicit `project_secrets:` entry with a non-empty
  `allow_data_to`: `internal/config/project_secrets.go:63-71`.
- The agent's environment is an allow-list keyed on auth: `internal/agent/env.go:103-137`. An
  OAuth run's agent never receives `ANTHROPIC_API_KEY`.
- A launcher is a human member (`deploy/terraform/gcp/modules/installation/iam.tf:66`,
  `variables.tf:139-141`), and nothing under `internal/` reads a Secret Manager **value** today.
- The seal is **not** on this branch: `internal/budget/token/object.go:15` is still
  `runs/<slug>/<run>/budget-token` with no key, and `internal/cli/run.go:1626` writes the marker as
  `lspec.Env = []string{runner.BudgetTokenEnv + "=1"}`. On `origin/next-0.9` it is `tokens/…` with
  `PutObject(…, key)` / `TakeObject(…, key)` / `ErrSealedNoKey` / `ErrNotSealed`
  (`internal/budget/token/object.go:14-175`, `seal.go`); `TakeObject` never deletes; and the
  lifecycle rule reaps only `matches_suffix = ["/budget-token"]`
  (`deploy/terraform/gcp/modules/installation/bucket.tf:46-62`).
- `Shared()` strips `claude_accounts` from the runs-bucket shared config
  (`internal/localcfg/shared.go:44`) and its reader `ParseShared` refuses it by key and by value
  (`internal/cli/sharedcfg.go:180, 214-218, 247-248`); the reasoning at `shared.go:22-27` is about
  the **runs bucket**, not the policy node, whose rules say `".write": false`
  (`internal/budget/rules/rules.json.tmpl:15-17`) — which is why the design puts the project
  accounts in the policy.
- `config.ReservedSecrets["claude-oauth-token"]` is `CLAUDE_CODE_OAUTH_TOKEN`
  (`internal/config/config.go:481-486`); the whole `claude-oauth-token-` prefix is reserved against
  workflow secrets (`internal/config/validate.go:215-226`).
- The runner's redactor is `internal/runner/runner.go:603-616` (`secretList`, `addSecret`).

## Decisions (veto any before execution starts)

Restated from the design's §8, with the same recommendations. **D2 and D4 are the two that change
the shape of the work**, and D8 is the one a user who never reads a design doc will meet.

- **D1 (carrier, per the ruling).** A second sealed object `tokens/<slug>/<run>/claude-oauth-token`,
  written by the launcher under the same per-launch key, read once by the runner, unsealed into
  `CLAUDE_CODE_OAUTH_TOKEN`; deleted by the launcher on a failed launch; reaped by the lifecycle
  rule. Not a static `secret_env`, not a second field in the budget token's envelope.
- **D2 (who may read the token).** Recommended (b): a per-account launcher list in the local
  config (`claude_accounts.<name>.launchers`), and a `secretAccessor` member for each listed
  launcher on that account's project-scope secret. (a) is every member of `launchers` on every
  served account; (c) is the job reading the secret itself (a departure from the ruling's wording).
  *(a) changes only Task 5's `for_each`; (c) makes Task 5 a job-account grant and Task 6 a runner
  read with no launcher read.*
- **D3 (the gate).** `accounts.<name>.allow_repos` in the policy, derived at publish from
  `claude_accounts.<name>.allow_data_to`, provider-qualified, never a wildcard; publish refuses a
  profile whose admitted repositories its accounts do not cover, and a profile that lists accounts
  must write its own exact `allow_repos`. *Vetoing the subset rule moves that check from Task 3 to
  Task 7 as an intersection.*
- **D4 (the profile field).** `profiles.<p>.claude_accounts: [<name>, ...]`, one job per listed
  account; the `secrets:` refusal stands unchanged; a profile that lists accounts may not also
  mount an Anthropic API key. *Vetoing the list for a single `claude_account` simplifies Tasks 3, 4
  and 7 (one job per profile, no selection among accounts).*
- **D5 (the default account).** Allowed, listed as `default`, riding the profile's no-account job;
  its gate list derives from `project_secrets.claude-oauth-token.allow_data_to`.
- **D6 (the named account's container).** Keep requiring a non-empty `allow_data_to`; no change to
  `ProjectScopeByDefault`.
- **D7 (re-seal and lifetime).** Mirror the budget token's re-seal, failure mode included; add
  `/claude-oauth-token` to the lifecycle rule's `matches_suffix`.
- **D8 (SECURITY.md).** The design's §6.1, four sentences, plus the launcher paragraph's line for
  D2's grant.
- **D9 (sequencing and job set).** PR #563 first (Task 1). A profile keeps its no-account job and
  gains one per listed account, so no job is renamed or deleted and `RefuseProjectJobDeletes` never
  fires. *Vetoing this (dropping the no-account job) makes Task 4's rollout a documented operator
  step that `RefuseProjectJobDeletes` stops.*
- **D10 (inspection).** `fugaro project policy show` prints the `accounts` block; `fugaro accounts
  ls` stays local-only and unchanged.
- **Numbering, not a decision.** D1-D10 here are the design's §8 decisions. The "launcher stays
  trusted" decision is **project-centric.md's D10** (cited by budget-token-channel.md §3
  constraint 5), a different decision; where Review Focus or a task cites it, it says so.

## Global Constraints

1. **Every task that changes code has a red step**, and every test this plan adds is
   mutation-proven: plant the bug, see the test fail, remove the bug. The red step is written as
   `Expected: FAIL` with the mutation it catches. Task 1 changes no code and has a gate instead.
2. **Never a secret value in an override, a log line, an error or a message.** The per-launch
   **key** travels in `LaunchSpec.SecretEnv` — the field `next-0.9`'s Slice B adds for exactly this
   (`origin/next-0.9:internal/backend/backend.go:80-90`,
   `origin/next-0.9:internal/cli/run.go:1623`), whose entries `internal/backend/launchenv.go`'s
   `LaunchEnv` marks `Secret` so that a backend which could only carry one where a wider audience
   reads it refuses the launch. **Nothing of this feature's goes in `LaunchSpec.Env`**, which is the
   plain list whose entries reach the `jobs.run` request body and the audit logs — the carrier
   Slice B deliberately abandoned, and the one Task 6 must not reintroduce. The launcher's secret
   read never puts the value in an error (the `sealRedact` pattern, `origin/next-0.9:internal/cli/launch_budget.go`),
   and the key and the token are both registered with the runner's redactor
   (`internal/runner/runner.go:609-616`) before anything can print them.
3. **No silent fallback.** A run that cannot obtain the token stops. The agent's environment is
   `agent.BuildEnv`'s allow-list (`internal/agent/env.go:103-137`), which is the mechanism that keeps
   an API key away from an OAuth run's agent; no task may widen it.
4. **Every grant is a member resource**, never a binding or a policy, and its `precondition`
   re-checks the condition in the same change (the rule
   [budget-token-channel.md](../design/budget-token-channel.md) §3 constraint 2 quotes from
   `deploy/terraform/gcp/modules/installation/iam.tf`).
5. **Terraform changes run `terraform fmt`, `validate`, the root's `terraform test` and regenerate
   the golden plans** (`scripts/gen-golden-plans.sh`, `internal/infra/tf/testdata/golden/`). A plan
   that would delete or replace a project job or a secret container fails closed (`tf.Guard`,
   `infra.RefuseProjectJobDeletes`); under D9 no task of this plan plans such a delete, so one
   appearing is a bug.
6. **Nothing under `.github/workflows/` is created or edited.** No workflow change is needed by
   this plan; if one turns out to be, the file goes to `docs/pending-workflows/` for the owner.
7. **No secret-shaped literal anywhere**, in code, tests, docs or fixtures. Tests use obvious
   placeholders (`"placeholder-oauth-token"`), never a realistic-looking value.
8. **`GOFLAGS=-p=2 go vet ./...`, `gofmt -l`, and the tests of every package touched** before a
   task is called done.
9. The task order is a merge order, not just a reading order: **Tasks 2-6 are inert** — the policy
   can declare accounts, `init` can deploy the account jobs, the objects can be written and read,
   and `fugaro run` still refuses an OAuth launch onto a shared job, because Task 7 is what lifts
   `checkProjectJobOAuth`. Every non-OAuth launch keeps landing on the profile's unchanged
   no-account job. Any prefix of this plan is safe to stop at.

## Review Focus (mutation-proof notes)

Each is a claim a careless edit could silently reverse; the implementing PR proves each red.

1. **The `secrets:` refusal is not what gets lifted.** `isClaudeOAuthSecret`
   (`internal/projectpolicy/profiles.go:328-331`) keeps refusing a Claude token in a profile's
   `secrets:`, and the new permission is the `claude_accounts` field beside it. A change that lifts
   the predicate instead of adding a field is the wrong change and must fail review.
2. **The accounts are computed one way.** The launcher, the runner and the deployer must derive a
   profile's job set from one function. Two implementations that agree today and drift tomorrow is
   the failure mode `ClaudeAccountOfSecrets`'s own doc (`internal/config/claude_accounts.go:87-111`)
   exists to prevent; Task 4 moves all three sides at once.
3. **A sealed object is never read as plaintext.** `TakeObject` with no key refuses both a sealed
   object (`ErrSealedNoKey`) and a plaintext one (`ErrNotSealed`). The OAuth object inherits both
   refusals and adds none of its own.
4. **The token is redacted.** The runner registers the plaintext before the agent starts; a test
   that prints the token through the run's own redactor must come back masked, and a test that the
   token never reaches git's environment (the pattern of `next-0.9`'s
   `23b2fca gitops: the budget marker never reaches git's environment`) must exist.
5. **The gate is at the point of use, not only at launch.** The runner refuses a run whose
   repository the policy's `accounts` block does not cover for the job's account — the launcher is
   trusted (project-centric.md's D10, not this plan's), so a launch-time check alone is a policy
   expression rather than a boundary, and the design says so rather than pretending otherwise.
6. **No API key beside the accounts.** The publish-time and `init`-time refusal keeps an account
   job from holding a key no run on it can use; a test must show both spellings of the Anthropic
   secret (`anthropic-api-key` as a profile mount, and any provider whose key variable is
   `ANTHROPIC_API_KEY`) refused beside `claude_accounts`.
7. **The launchers' grant is bounded.** The member resources are created for the accounts a
   **published profile** names, and (under D2(b)) only for the launchers listed on that account — a
   tftest proves a local-config account no profile uses, and a launcher not on an account's list,
   get no member.
7a. **The key is a `SecretEnv` entry, not an `Env` one.** `LaunchSpec.Env` is the plain list whose
   entries reach the `jobs.run` request body and the audit logs; Slice B added
   `LaunchSpec.SecretEnv` so that a backend which could only carry an entry where a wider audience
   reads it refuses the launch. An implementation that writes the marker into `LaunchSpec.Env`
   because it is the field that already exists is the regression this item exists to catch.
8. **`ProjectSecretID` is unchanged.** No new secret-id helper, no new prefix, no rename: the
   container is `gcp.ProjectSecretID(project, config.ClaudeAccountSecret(name))` and a test pins it.
9. **The default account still needs its explicit entry.** `ProjectScopeByDefault` keeps excluding
   the whole `claude-oauth-token` namespace (design D5/D6); a change that flips it is a separate
   decision, not a convenience.
10. **No existing job is renamed.** A golden-plan diff for an installation whose profiles list no
   account is empty, and a profile gaining an account plans only creates.
11. **The object does not outlive the day.** The lifecycle rule's `matches_suffix` carries
   `/claude-oauth-token`; removing it must turn the installation tftest red.

## File Structure

| Path | Responsibility | Task |
| --- | --- | --- |
| (PR #563) `internal/budget/token/`, `modules/project/sa.tf`'s `job_tokens` | the seal and the shared job's `tokens/` read | 1 (gate only) |
| `internal/projectpolicy/policy.go` | `Policy.Accounts`, the `accounts` block's decode | 2 |
| `internal/projectpolicy/accounts.go` (new) | validating the block | 2 |
| `internal/cli/project_cmd.go` | publish derives `accounts` from the local config; `policy show` prints it (D10) | 2 |
| `internal/config/config.go` (`JobProfile` at :519) | `JobProfile.ClaudeAccounts` | 3 |
| `internal/projectpolicy/profiles.go` | the field's rules, the subset rule, the no-API-key rule; `isClaudeOAuthSecret` unchanged | 3 |
| `internal/localcfg/localcfg.go`, `internal/config/validate.go` | the field's validation against `claude_accounts` | 3 |
| `internal/config/claude_accounts.go` | the one function a profile's account set comes from (replacing `ClaudeAccountOfSecrets`'s use) | 4 |
| `internal/infra/projectjobs.go` | one job per (profile, provider, account) beside the no-account job | 4 |
| `internal/runner/projectjob.go`, `internal/cli/project_job.go` | candidate list and launcher derivation through the same function | 4 |
| `internal/backend/gcp/names.go` | unchanged — `ProjectJobName`/`ProjectServiceAccountID` already take the account | 4 |
| `internal/config/claude_accounts.go`, `internal/infra/tfvars.go` | (D2(b)) `claude_accounts.<name>.launchers`; the served accounts and their readers published to tfvars | 5 |
| `deploy/terraform/gcp/modules/installation/iam.tf`, `bucket.tf`, `variables.tf`; `roots/installation/` | the `secretAccessor` members (D2); the lifecycle suffix (D7) | 5 |
| `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, `deploy/terraform/terraform_test.go`, `internal/infra/tf/testdata/golden/` | the members' and the rule's tftests and the regenerated plans | 5 |
| `internal/backend/gcp/` (new secret-value read) | the launcher's read of one secret version | 6 |
| `internal/budget/token/account.go` (new) | the OAuth object's key, its seal, its read, its delete | 6 |
| `internal/cli/launch_budget.go`, `internal/cli/run.go` | the launcher writes the OAuth object beside the budget token | 6 |
| `internal/runner/budget_session.go`, `internal/runner/runner.go` | the runner reads it, redacts it, hands it to `agent.BuildEnv` | 6 |
| `internal/cli/project_job.go` | the launch gate: `checkProjectJobOAuth` retired, the account job selected | 7 |
| `internal/cli/doctor.go` | the project-account checks | 7 |
| `SECURITY.md`, `docs/`, `website/` | D8's sentences, the user-facing docs, the CLI reference | 8 |
| `internal/cli/docs_project_named_oauth_tokens_test.go` (new) | pins this design's and this plan's load-bearing claims | 8 |

## PR group

| PR | Tasks | What it is |
| --- | --- | --- |
| — | 1 | not a PR of this plan: PR #563 merged; Task 1 only checks it |
| 1 | 2, 3, 4 | **SECURITY-SENSITIVE.** The policy's accounts block, the profile field and its gate rules, the account jobs. Inert: no OAuth launch reaches them yet |
| 2 | 5, 6 | **SECURITY-SENSITIVE.** The launchers' grant, the lifecycle suffix and the sealed object. Inert: nothing launches an OAuth run onto a shared job yet |
| 3 | 7 | **SECURITY-SENSITIVE.** The gate flips; the feature works |
| 4 | 8 | the documentation and the pinned claims |

Task 8 can be split so its SECURITY.md half lands with PR 3, which is the honest place for it: the
wording is the feature's cost, and it should ship with the thing that costs.

## Known follow-ups, deliberately not in this plan

- **Per-person ownership** (project-centric Task 5.1) — a different feature; the design's §5 argues
  why. `fugaro accounts add`/`share` and opaque `PersonSecretID`s are not built here.
- **Failover and usage-limit switching for project accounts** (Task 5.3's subject).
- **Codex accounts** (the parallel Task C3): `codex-oauth-token[-<name>]`. Nothing here blocks it
  and nothing here depends on it.
- **The Cloud Batch backend**, excluded from 0.9.0 by the owner's ruling of 2026-10-18. If it ever
  returns, the seal's Batch refusal note in
  [budget-token-channel.md](../design/budget-token-channel.md) is where its carrier is priced.
- **Retiring a project job.** `RefuseProjectJobDeletes` still has no way back; under D9 this plan
  never needs one, but removing an account from a profile later will.
- **`fugaro run`'s failover into a project account** — the design's §4.4 refuses rather than fails
  over; a failover would need D8's wording to change with it.

---

### Task 1: The seal is on the branch (gate, no code)

**What it is:** PR #563 merges `origin/next-0.9` (Slice A: the runner's half, the versioned
envelope, `Seal`/`Unseal`, the key's one spelling; Slice B: the launcher seals, the retry re-seal,
the `tokens/` move with its grants, SECURITY.md's two sentences) into `integration-0.9.0`, and adds
the shared job account's read-only `tokens/` grant (`cda64c5a`). That PR is another run's; this
task only confirms it landed before Task 2 starts, because every later task's carrier is that API.

**Verification (the gate):**

```
git merge-base --is-ancestor origin/next-0.9 origin/integration-0.9.0 && echo seal-merged
git grep -n 'const ObjectPrefix = "tokens"' origin/integration-0.9.0 -- internal/budget/token/object.go
git grep -n 'SecretEnv \[\]string' origin/integration-0.9.0 -- internal/backend/backend.go
git grep -n '"job_tokens"' origin/integration-0.9.0 -- deploy/terraform/gcp/modules/project/sa.tf
GOFLAGS=-p=2 go test ./internal/budget/token/... -count=1
```

Expected: every command prints a match (or `ok`). Any one missing stops the plan.

**Depends on:** PR #563.

### Task 2: The policy's `accounts` block — SECURITY-SENSITIVE

**Files:** `internal/projectpolicy/policy.go`, `internal/projectpolicy/accounts.go` (new),
`internal/projectpolicy/accounts_test.go` (new), `internal/cli/project_cmd.go` (`project policy
publish` and `project policy show`), `internal/cli/project_policy_test.go`.

**What it does:** adds `Policy.Accounts map[string]Account` with `Account{AllowRepos []string}`,
decoded strictly (an unknown key inside the block is refused, as everywhere else in the policy),
validated (account names held to `config.ClaudeAccountNameRE` plus `default`; `allow_repos`
non-empty; every entry an exact, provider-qualified repository — `ValidPattern`'s shape with no
`*`), and *derived* by `fugaro project policy publish` from `localcfg.ClaudeAccounts[name].AllowDataTo`
(and, for `default`, from `project_secrets.claude-oauth-token.allow_data_to`, D5), qualified with
the provider of the profile(s) naming the account. Only accounts some profile names are published.
`fugaro project policy show` prints the block (D10). No profile can name an account yet — that is
Task 3 — so publish derives an empty block on every installation.

**SECURITY-SENSITIVE — independent opus review.** This is the gate's definition; a decode that
accepts an unknown key, a validation that lets an empty `allow_repos` or a wildcard through, or a
derivation that publishes an account no profile names turns the gate into a decoration.

**Tests:** `TestAccountsBlockDecodes`, `TestAccountsUnknownKeyRefused`,
`TestAccountAllowReposMustBeNonEmpty`, `TestAccountAllowReposExactProviderQualified`,
`TestPublishDerivesAccountsFromLocalConfig`, `TestPublishDerivesDefaultFromProjectSecrets`,
`TestPolicyShowPrintsAccounts`.

**Red step:** add `TestAccountAllowReposMustBeNonEmpty` and delete the non-empty check from
`accounts.go`'s validation. **Expected: FAIL** — the empty list decodes and validates, so the gate
admits every repository. Restore the check; the test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... ./internal/cli/ -run 'Account|Policy' -count=1`

**Depends on:** Task 1 (merge order only; inert without Tasks 3, 4 and 7).

**Commit:** `projectpolicy: the accounts block, derived at publish from the local config`.

### Task 3: The profile field and its rules — SECURITY-SENSITIVE

**Files:** `internal/config/config.go` (`JobProfile`, at :519), `internal/config/validate.go`,
`internal/localcfg/localcfg.go`, `internal/projectpolicy/profiles.go`, their tests,
`internal/cli/project_cmd.go` (publish's derivation now has profiles to read).

**What it does:** adds `JobProfile.ClaudeAccounts []string` (yaml/json `claude_accounts`), each
entry shape-checked against `config.ClaudeAccountNameRE` and `MaxClaudeAccountNameLen` or equal to
`default`, no duplicates, and — at local-config load and at publish — resolved against
`lc.ClaudeAccounts` (or, for `default`, the `project_secrets` entry) so a profile may only name an
account the operator's own config defines. In `validProfiles` (so publish, `init` and both readers
of the node hold one rule): every account a profile lists must be in the policy's `accounts` block;
**the subset rule** — a profile that lists accounts must write its own `allow_repos`, exact entries
only, each covered by every listed account's `allow_repos`, refused naming both lists and the
repository in one and not the other (D3); and **the no-API-key rule** — a profile that lists
accounts may not also mount an Anthropic API key, in either spelling (`anthropic-api-key` directly,
or a provider whose `Secret` maps to `ANTHROPIC_API_KEY`), refused with a message that says which
of the two to drop (D4). `isClaudeOAuthSecret` is untouched.

**SECURITY-SENSITIVE — independent opus review.** The subset rule is the publish-time half of the
gate. The reviewer should be asked: can a profile admit a repository its account does not cover
(absent `allow_repos`, a wildcard, a case difference, a provider mismatch); is the rule in
`validProfiles` so the deployer cannot disagree with publish.

**Tests:** `TestJobProfileClaudeAccountsShape`, `TestProfileAccountMustBeConfigured`,
`TestProfileAccountMustBeInPolicyAccounts`, `TestProfileAllowReposMustBeCoveredByAccount`,
`TestProfileWithAccountsNeedsOwnExactAllowRepos`, `TestProfileAccountWithAnthropicKeyRefused`,
`TestProfileAccountWithProviderKeyRefused`, `TestSecretsRefusalUnchanged`.

**Red step:** delete the subset check and run `TestProfileAllowReposMustBeCoveredByAccount`.
**Expected: FAIL** — a profile admitting `github/acme/other` publishes beside an account that lists
only `github/acme/app`. Restore the check; it passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/config/... ./internal/localcfg/... ./internal/projectpolicy/... ./internal/cli/ -run 'Profile|ClaudeAccount|Account' -count=1`

**Depends on:** Task 2 (the subset rule reads the `accounts` block).

**Commit:** `config: a profile may list Claude accounts its allow-list is covered by`.

### Task 4: The account jobs, on all three sides — SECURITY-SENSITIVE

**Files:** `internal/config/claude_accounts.go` (the shared function), `internal/infra/projectjobs.go`,
`internal/infra/projectjobs_test.go`, `internal/infra/tf/testdata/golden/` (regenerated),
`deploy/terraform/gcp/modules/project/` only if the module's object type needs a change (it already
has `account`), `internal/runner/projectjob.go` and its tests, `internal/cli/project_job.go` and
its tests.

**What it does:** replaces `const account = ""` (`projectjobs.go:96`) with a loop: the profile's
no-account job exactly as today, plus one job per non-default account the profile lists, named and
service-accounted through the existing `gcp.ProjectJobName` / `gcp.ProjectServiceAccountID`. One
function — `config.ProfileAccounts(profile)` or similar — gives the job set, and the deployer, the
runner's `projectJobCandidates` (`runner/projectjob.go:175-180`) and the launcher's derivation
(`project_job.go:98`) all call it in this task, so no side computes the axis from `secrets:` after
it (Review Focus 2). The launcher still sends every non-OAuth run to the no-account job and still
refuses every OAuth run (`checkProjectJobOAuth` is Task 7's). `projectJobSecrets` and
`projectSecretEnv` are **unchanged**: the account is not a `secret_env`, which is the whole point of
Task 6. `ProjectJobs`' doc comment loses its "empty until phase 5" paragraph and gains the new rule.

**SECURITY-SENSITIVE — independent opus review.** The job name is what the runner binds a run's
identity to. The reviewer should be asked: do the three sides compute the same job set from one
function; can a run be placed in an account job its profile does not list; is the no-account job's
name byte-for-byte unchanged.

**Tests:** `TestProjectJobAccountAxis`, `TestProjectJobNoAccountJobUnchanged`,
`TestRunnerCandidatesIncludeAccountJobs`, `TestLauncherAccountDerivationSharedFunction`, and the
golden plans: for a fixture whose profiles list no account the diff is empty; for one that lists
an account it is creates only.

**Red step:** add `TestProjectJobAccountAxis`, then restore `const account = ""`. **Expected:
FAIL** — the account job is never deployed, and the runner's candidate for it has no deployed
counterpart. Remove the constant again; the test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/config/... ./internal/infra/... ./internal/runner/ ./internal/cli/ -run 'ProjectJob|Account|Candidate' -count=1`, then `scripts/gen-golden-plans.sh` and `git diff --stat internal/infra/tf/testdata/golden/`.

**Depends on:** Task 3.

**Commit:** `infra: one project job per (profile, provider, account)`.

### Task 5: The launchers' read and the object's lifetime — SECURITY-SENSITIVE

**Files:** `internal/config/claude_accounts.go` (D2(b): `ClaudeAccount.Launchers`, validated as IAM
members and as a subset of the installation's `launchers`), `internal/infra/tfvars.go`,
`deploy/terraform/gcp/modules/installation/iam.tf`, `bucket.tf`, `variables.tf`,
`deploy/terraform/gcp/roots/installation/` (`variables.tf` / `main.tf` for the new input),
`deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`,
`deploy/terraform/terraform_test.go`, `internal/infra/tf/testdata/golden/` (regenerated).

**What it does (D2, D7):** one `google_secret_manager_secret_iam_member` per (reader, account) for
each account a **published profile names** — readers being that account's `launchers` under D2(b)
(every member of `var.launchers` under D2(a)) — role `roles/secretmanager.secretAccessor`, target =
the project-scope secret container the installation already creates for that account
(`tfvars.go:271-276`). A member resource, never a binding, with the same `precondition` discipline
as the rest of the file; `TestNoProjectLevelSecretAccessor` keeps passing because the grant is on
the secret, not the project. And the token lifecycle rule's `matches_suffix` gains
`/claude-oauth-token` (`bucket.tf:46-62`), with the tftest's "exactly that suffix" assertion
updated to the pair.

**SECURITY-SENSITIVE — independent opus review.** This is the one grant the feature adds, and it
lets people read a long-lived credential. The reviewer should be asked specifically: is the member
set exactly (listed readers × published accounts); is the role the narrowest that works; does the
precondition re-check the same condition in the same change; does the tftest fail if a member is
granted on an account no profile names or to a launcher not on the account's list; and does the
lifecycle rule still exist only when the budget is enabled, as the seal does.

**Tests:** tftests for the members' existence, role, target and precondition, and for the
lifecycle suffix pair; a Go test that `tfvars` publishes exactly the profiles' accounts and their
readers; a tftest that an unused local-config account and an unlisted launcher get no member.

**Red step:** widen the member to every account in `lc.ClaudeAccounts` and run the "unused account
gets no member" test. **Expected: FAIL.** Narrow it back; the test passes. Then drop
`/claude-oauth-token` from `matches_suffix`. **Expected: FAIL** (Review Focus 11). Restore it.

**Command:** `terraform -chdir=deploy/terraform/gcp/roots/installation init -backend=false && terraform -chdir=deploy/terraform/gcp/roots/installation test`, then `GOFLAGS=-p=2 go test ./deploy/terraform/ ./internal/infra/... ./internal/config/... -count=1`.

**Depends on:** Task 4 (the set of published accounts is what Task 4 makes real).

**Commit:** `infra: the named launchers may read the accounts their profiles serve`.

### Task 6: The sealed object — SECURITY-SENSITIVE

**Files:** `internal/backend/gcp/` (a new, narrow secret-version read; its test),
`internal/budget/token/account.go` (new) + `account_test.go` (new),
`internal/cli/launch_budget.go`, `internal/cli/run.go`, `internal/cli/run_test.go`,
`internal/runner/budget_session.go`, `internal/runner/runner.go`, `internal/runner/*_test.go`.

**What it does (D1, D7):** `token.AccountObjectKey(slug, run)` =
`tokens/<slug>/<run>/claude-oauth-token`; the launcher reads the account's secret version (the
CLI's first Secret Manager value read; the value never enters an error), seals it into that object
under the same per-launch key, create-if-absent, with the retry re-seal mirrored, and deletes it
where the launch fails exactly where `dropBudgetToken` deletes the budget token. A launch that
minted no key writes nothing and is refused. The runner — when its job identity is an account job
whose profile lists the run's account and the run's auth is `oauth` — reads it once, unseals it,
registers the plaintext with `addSecret` (`internal/runner/runner.go:609-616`) **before anything
else can see it**, and places it where `agent.BuildEnv`'s oauth branch reads
`CLAUDE_CODE_OAUTH_TOKEN`; it refuses the run when the object is absent, sealed-with-no-key, sealed
under another key, or sealed under another name. The launcher's marker entry goes to
`LaunchSpec.SecretEnv` and never to `LaunchSpec.Env` (Global Constraint 2). It is written **only**
for an OAuth run on an account job: the per-repository path keeps its static mount
(`internal/infra/spec.go:585-586`) untouched.

**SECURITY-SENSITIVE — independent opus review.** The reviewer should be asked: does the read
refuse every one of the four shapes; is the plaintext registered with the redactor before the agent
starts and before any log line can carry it; does the token reach git's environment, the launch
request body or an error by any route; is the object deleted on every failed-launch path the budget
token's is; and does the create-if-absent/retry pair have the same failure mode as the budget
token's.

**Tests:** `TestAccountObjectRoundTrip`, `TestAccountObjectSealedNoKeyRefused`,
`TestAccountObjectWrongKeyRefused`, `TestAccountObjectWrongNameRefused`,
`TestAccountObjectPlaintextRefused`, `TestAccountObjectRetryReseals`,
`TestAccountObjectDroppedOnFailedLaunch`, `TestAccountObjectRefusedWithoutMintedKey`,
`TestSecretReadErrorCarriesNoValue`, `TestOAuthTokenRedactedFromRunOutput`,
`TestOAuthTokenNeverInPlainLaunchEnv` (the pattern of `next-0.9`'s `23b2fca` — the key and the token
must be absent from `LaunchSpec.Env`, from git's environment and from anything the run publishes,
and the key present only in `LaunchSpec.SecretEnv`), `TestPerRepositoryOAuthPathUnchanged`.

**Red step:** make the read fall back to reading the object's bytes as the token when unsealing
fails. **Expected: FAIL** — `TestAccountObjectWrongKeyRefused` and
`TestAccountObjectPlaintextRefused` both go red, which is exactly the "plaintext fallback is the
exposure the seal exists to close" refusal. Remove the fallback; both pass.

**Command:** `GOFLAGS=-p=2 go test ./internal/backend/gcp/ ./internal/budget/token/... ./internal/cli/ ./internal/runner/ -run 'Account|OAuth|Seal|Secret' -count=1`

**Depends on:** Tasks 1, 4, 5.

**Commit:** `budget/token: the run's Claude account token travels sealed`.

### Task 7: The gate — SECURITY-SENSITIVE

**Files:** `internal/cli/project_job.go`, `internal/cli/project_job_test.go`,
`internal/cli/doctor.go`, `internal/cli/doctor_test.go`, `internal/runner/projectjob.go` and its
tests.

**What it does:** retires `checkProjectJobOAuth` (`project_job.go:223-233`) and replaces it with
the real gate: an OAuth launch onto a shared job is allowed when the profile lists the run's
selected account (`agent.oauth_token` / `--token`, the default when neither), the repository is in
that account's `allow_repos` (and the profile's), and the account's token has a project-wide
container; the launch then targets that account's job. Each failure names the field to fix (design
§4.4's table). `checkProjectJobAccount` (`:154-169`) compares against the selected account job
rather than the one account `secrets:` used to give. Adds the runner-side half: the runner refuses
a run whose repository the policy's `accounts` block does not cover for its job's account (Review
Focus 5). Adds `fugaro doctor` checks for the three publish-time facts a launch would otherwise
discover late, with the same severity discipline `claude-accounts-stored` and `oauth-token-allowed`
already follow (an ordinary finding for a real problem, a warning — never silence — when the check
itself could not be answered, `internal/cli/doctor.go:1201-1325`).

**SECURITY-SENSITIVE — independent opus review.** This is the task that turns the feature on, and
its reviewer should be asked: does every one of design §4.4's rows refuse; does any path fall
through to the per-repository branch or the no-account job and thereby run with no account; is the
runner's refusal independent of the launcher's; and does `--token` naming an account the profile
does not list refuse rather than silently select another.

**Tests:** `TestProjectJobOAuthAllowedWhenAccountCovered`, `TestProjectJobOAuthSelectsAccountJob`,
`TestProjectJobOAuthRefusedAccountNotListed`, `TestProjectJobOAuthRefusedRepoNotAllowed`,
`TestProjectJobOAuthRefusedNoContainer`, `TestRunTokenMustBeAListedAccount`,
`TestRunnerRefusesUncoveredAccount`, `TestDoctorProjectAccountChecks`.

**Red step:** restore `checkProjectJobOAuth`'s unconditional refusal beside the new gate.
**Expected: FAIL** — `TestProjectJobOAuthAllowedWhenAccountCovered` goes red, which proves the new
gate is what admits the launch rather than the old refusal having been bypassed. Remove it; the
test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/runner/ -run 'ProjectJob|Account|Doctor' -count=1`

**Depends on:** Tasks 2, 3, 4, 6.

**Commit:** `cli: a shared project job may serve named Claude accounts`.

### Task 8: The documentation, and the claims pinned

**Files:** `SECURITY.md`, `docs/design/project-named-oauth-tokens.md` (its Status line),
`docs/design/named-oauth-tokens.md` (the "project level" pointer), `docs/gcp-setup.md` (where
`claude_accounts` is documented for operators: the profile field, the accounts' `launchers`, and
the project-wide storage step), `website/` (the CLI reference, if any help text changed),
`internal/cli/docs_project_named_oauth_tokens_test.go` (new).

**What it does (D8):** SECURITY.md gains the design's §6.1 in the owner's terms — a hostile run can
use the token during its own run; the account is the blast radius; nothing prices an OAuth run the
way the gateway prices a provider-key run; and the named launchers can read a served account's
token (one line in the launcher paragraph, replacing "which only the job and build accounts hold"
for those secrets). It also gains the profile's new meaning where it already explains the shared
job's boundary. The design's Status line moves from "design" to "built in 0.9.0", with what is
built and what is not.

**Tests:** `docs_project_named_oauth_tokens_test.go` pins the load-bearing sentences — the
`claude_accounts` profile field's name, the refusals' wording, the no-fallback rule, the "hostile
run can use the token during its own run" residual, the launcher-read line, and that
`isClaudeOAuthSecret` still refuses the `secrets:` spelling — in the house style
(`internal/cli/docs_named_oauth_tokens_test.go` is the model: read the doc, assert the phrases,
and tie the behavioural ones to the real function rather than to text alone).

**Red step:** delete the SECURITY.md residual sentence and run the new docs test. **Expected:
FAIL.** Restore it; the test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1`

**Depends on:** Tasks 2-7 (it documents what they built).

**Commit:** `docs: SECURITY.md's OAuth residual, and the project accounts`.

---

## What could not be verified without a live check

Restated from the design's §7 so it is not lost in the plan: **U1** whether an execution's override
can be read back through the Cloud Run API (the channel doc's U2, Check 52 step 3, unrun — it
decides how wide the seal's audience is); **U3** whether one account's token serves several
concurrent runs (it decides whether the feature is useful, not whether it is safe); **U4** whether
the per-launch key survives a Cloud Run retry exactly as the budget token assumes; **U5** whether
the launchers' `secretAccessor` members leave the secret container undisturbed (the golden plans of
Task 5 answer it). **U2** (which credential Claude Code prefers when both are set) is not needed:
`agent.BuildEnv` never sets both for an agent. All are owner-run, sandbox only, and none of them
blocks Task 1.
