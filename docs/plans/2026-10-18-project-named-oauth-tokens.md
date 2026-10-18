# Project-level named Claude OAuth tokens

**Goal:** a project (shared job) may serve a **named** Claude OAuth account, exactly as a
per-repository job already can: the job is keyed by `(profile, provider, account)`, the published
policy says which repositories may use which token, and the token reaches the run through the
budget-token seal channel rather than through the job's environment. Release 0.9.0.

**Spec:** [../design/project-named-oauth-tokens.md](../design/project-named-oauth-tokens.md)
(this plan's authority; its §8 decisions are the ones this plan's Decisions section restates),
[../design/named-oauth-tokens.md](../design/named-oauth-tokens.md) (the individual level this
mirrors), [../design/project-centric.md](../design/project-centric.md) §4.1 and §7,
[../design/project-secrets.md](../design/project-secrets.md),
[../design/budget-token-channel.md](../design/budget-token-channel.md) (the channel the carrier
borrows).

**Depends on:** the seal, `origin/next-0.9` Slice A and B (Task 1 below merges it). Nothing else —
in particular **not** project-centric phases 4, 5.1, 5.2, 6 or the Cloud Batch backend, all of
which are out of 0.9.0 or argued unnecessary in the design's §5.

**Checked before this plan was written** (all on `origin/next-0.8.5`, head `13a5a48`, which is what
this branch is cut from; no cloud call, no secret read):

- The two refusals exist and are where the design says: `internal/projectpolicy/profiles.go:292`
  inside `validProfiles` (predicate `isClaudeOAuthSecret` at `:328-331`, `CheckProfiles` at `:412`),
  and `internal/cli/project_job.go:223-233` (`checkProjectJobOAuth`, called at `:105`).
- The account axis exists on all three sides and is empty by construction:
  `internal/infra/projectjobs.go:96` (`const account = ""`), `internal/cli/project_job.go:98`
  (`config.ClaudeAccountOfSecrets(pr.Secrets)`), `internal/runner/projectjob.go:76-101`
  (`jobIdentity` returns the account), `internal/backend/gcp/names.go:179-191`.
- A named account's token is already storable project-wide and already gets a project-scope Secret
  ID: `internal/cli/secrets.go:641-643`, `internal/infra/tfvars.go:271-276`,
  `internal/backend/gcp/names.go:207-209` (`ProjectSecretID`).
- The bare `claude-oauth-token` needs an explicit `project_secrets:` entry with a non-empty
  `allow_data_to`: `internal/config/project_secrets.go:63-71`.
- The seal is **not** on this branch: `internal/budget/token/object.go:15` is still
  `runs/<slug>/<run>/budget-token` with no key. On `origin/next-0.9` it is `tokens/…` with
  `PutObject(…, key)` / `TakeObject(…, key)` / `ErrSealedNoKey` / `ErrNotSealed`
  (`internal/budget/token/object.go:14-175`, `seal.go`). The 24 commits' base is an ancestor of this
  branch (`git rev-list --left-right --count HEAD...origin/next-0.9` → `231  24`).
- `Shared()` strips `claude_accounts` (`internal/localcfg/shared.go:44`) and
  `sharedForbidden` refuses it by key and by value (`internal/cli/sharedcfg.go:180, 214-218,
  247-248`); the reasoning at `shared.go:22-27` is about the **runs bucket**, not the policy node —
  which is why the design puts the project account in the policy.
- `config.ReservedSecrets["claude-oauth-token"]` is `CLAUDE_CODE_OAUTH_TOKEN`
  (`internal/config/config.go:478-486`); the whole `claude-oauth-token-` prefix is reserved against
  workflow secrets (`internal/config/validate.go:215-226`).
- The runner's redactor is `internal/runner/runner.go:603-616` (`secretList`, `addSecret`).

## Decisions (veto any before execution starts)

Restated from the design's §8, with the same recommendations. **D2 and D9 are the two that change
the shape of the work**, and D8 is the one a user who never reads a design doc will meet.

- **D1 (carrier).** A second sealed object `tokens/<slug>/<run>/claude-oauth-token`, written by the
  launcher under the same per-launch key, taken once by the runner, unsealed into
  `CLAUDE_CODE_OAUTH_TOKEN`. Not a static `secret_env` (design §3), not a second field in the
  budget token's envelope.
- **D2 (the launcher's read).** Grant the launcher `roles/secretmanager.secretAccessor` on the
  project-scope secret of each account a **published profile names**, as a member resource.
  *Vetoing this means Task 5 becomes "the job's service account reads it and the seal carries the
  account name" — a different Task 5 and 6, both smaller, both priced in the design's §4.3.*
- **D3 (the gate).** `accounts.<name>.allow_repos` in the policy, derived at publish from
  `claude_accounts.<name>.allow_data_to`; publish refuses a profile whose `allow_repos` its
  account's list does not cover.
- **D4 (the profile field).** `profiles.<p>.claude_account: <name>`, a new field; the `secrets:`
  refusal stands unchanged; a profile that names an account may not also mount an Anthropic API
  key.
- **D5 (the default account).** Allowed on a shared job, with the bare `claude-oauth-token` stored
  through the explicit `project_secrets:` entry it already requires.
- **D6 (the named account's container).** Keep requiring a non-empty `allow_data_to`; no change to
  `ProjectScopeByDefault`.
- **D7 (the retry re-seal).** Mirror the budget token's, failure mode included.
- **D8 (SECURITY.md).** The design's §6.1, four sentences, plus one line in the launcher paragraph
  for D2.
- **D9 (sequencing).** The seal merges first, as its own change (Task 1). Populating the account
  axis renames the jobs of installations that already have profiles; the rollout is a documented,
  one-time step and `RefuseProjectJobDeletes` will stop the first apply, by design.
  *Vetoing D9's rename means the account rides only in the seal's payload and the policy, and Task 4
  changes shape.*
- **D10 (inspection).** `fugaro project policy show` prints the `accounts` block; `fugaro accounts
  ls` stays local-only and unchanged.

## Global Constraints

1. **Every task has a red step**, and every test this plan adds is mutation-proven: plant the bug,
   see the test fail, remove the bug. The red step is written as `Expected: FAIL` with the mutation
   it catches.
2. **Never a secret value in an override, a log line, an error or a message.** The only thing that
   travels in `LaunchSpec.Env` is the per-launch **key**, exactly as the budget token's does today,
   and the key and the token are both registered with the runner's redactor
   (`internal/runner/runner.go:609-616`) before anything can print them.
3. **No silent fallback.** A run that cannot obtain the token stops. A profile that names an account
   may not mount an Anthropic API key (design §4.4), and the runner refuses a run whose marker
   promises a token it did not get.
4. **Every grant is a member resource**, never a binding or a policy, and its `precondition`
   re-checks the condition in the same change (the rule
   [budget-token-channel.md](../design/budget-token-channel.md) §3 constraint 2 quotes from
   `deploy/terraform/gcp/modules/installation/iam.tf`).
5. **Terraform changes run `terraform fmt`, `validate` and regenerate the golden plans**
   (`scripts/gen-golden-plans.sh`, `internal/infra/tf/testdata/golden/`). A plan that would delete
   or replace a project job or a secret container fails closed (`tf.Guard`,
   `infra.RefuseProjectJobDeletes`) and that is the expected outcome of Task 4's rename, not a bug
   to work around.
6. **Nothing under `.github/workflows/` is created or edited.** No workflow change is needed by
   this plan; if one turns out to be, the file goes to `docs/pending-workflows/` for the owner.
7. **No secret-shaped literal anywhere**, in code, tests, docs or fixtures. Tests use obvious
   placeholders (`"placeholder-oauth-token"`), never a realistic-looking value.
8. **`GOFLAGS=-p=2 go vet ./...`, `gofmt -l`, and the tests of every package touched** before a
   task is called done.
9. The task order is a merge order, not just a reading order: **Tasks 2-6 are inert** — the policy
   can declare an account, `init` can deploy the job, the objects can be written and taken, and
   `fugaro run` still refuses an OAuth launch onto a shared job, because Task 7 is what lifts
   `checkProjectJobOAuth`. Any prefix of this plan is safe to stop at.

## Review Focus (mutation-proof notes)

Each is a claim a careless edit could silently reverse; the implementing PR proves each red.

1. **The `secrets:` refusal is not what gets lifted.** `isClaudeOAuthSecret`
   (`internal/projectpolicy/profiles.go:328-331`) keeps refusing a Claude token in a profile's
   `secrets:`, and the new permission is the `claude_account` field beside it. A change that lifts
   the predicate instead of adding a field is the wrong change and must fail review.
2. **The account is computed one way.** The launcher, the runner and the deployer must derive the
   job's account from one function. Two implementations that agree today and drift tomorrow is the
   failure mode `ClaudeAccountOfSecrets`'s own doc (`internal/config/claude_accounts.go:87-111`)
   exists to prevent.
3. **A sealed object is never read as plaintext.** `TakeObject` with no key refuses both a sealed
   object (`ErrSealedNoKey`) and a plaintext one (`ErrNotSealed`). The OAuth object inherits both
   refusals and adds none of its own.
4. **The token is redacted.** The runner registers the plaintext before the agent starts; a test
   that prints the token through the run's own redactor must come back masked, and a test that the
   token never reaches git's environment (the pattern of `next-0.9`'s
   `23b2fca gitops: the budget marker never reaches git's environment`) must exist.
5. **The gate is at the point of use, not only at launch.** The runner refuses a run whose job's
   account is not the one the policy's `accounts` block covers for that repository — the launcher is
   trusted (D10), so a launch-time check alone is a policy expression rather than a boundary, and
   the design says so rather than pretending otherwise.
6. **No API key beside the account.** The publish-time and `init`-time refusal is the mechanism that
   keeps the no-fallback property true; a test must show both spellings of the Anthropic secret
   (`anthropic-api-key` as a profile mount, and any provider whose key variable is
   `ANTHROPIC_API_KEY`) refused beside `claude_account`.
7. **The launcher's grant is bounded.** The member resource is created per account a **published
   profile** names — not per account in the local config — and a tftest proves a local-config
   account no profile uses gets no member.
8. **`ProjectSecretID` is unchanged.** No new secret-id helper, no new prefix, no rename: the
   container is `gcp.ProjectSecretID(project, config.ClaudeAccountSecret(name))` and a test pins it.
9. **The default account still needs its explicit entry.** `ProjectScopeByDefault` keeps excluding
   the whole `claude-oauth-token` namespace (design D5/D6); a change that flips it is a separate
   decision, not a convenience.

## File Structure

| Path | Responsibility | Task |
| --- | --- | --- |
| `internal/budget/token/seal.go`, `object.go`, `seal*_test.go` | the seal, arriving from `next-0.9` | 1 |
| `internal/projectpolicy/policy.go` | `Policy.Accounts`, the `accounts` block's decode | 2 |
| `internal/projectpolicy/accounts.go` (new) | validating the block, the subset rule, the publish-side derivation | 2 |
| `internal/projectpolicy/profiles.go` | the `claude_account` field's rules; `isClaudeOAuthSecret` unchanged | 2, 4 |
| `internal/cli/project_cmd.go`, `internal/cli/sharedcfg.go` | publish: derive `accounts` from the local config, hold the subset rule | 2 |
| `internal/config/config.go` (`JobProfile` at :519) | `JobProfile.ClaudeAccount` | 3 |
| `internal/localcfg/localcfg.go`, `internal/config/validate.go` | the field's validation against `claude_accounts`, the no-API-key rule | 3 |
| `internal/infra/projectjobs.go` | populate the account axis; the job's name and service account | 4 |
| `internal/backend/gcp/names.go` | unchanged — `ProjectJobName`/`ProjectServiceAccountID` already take the account | 4 |
| `deploy/terraform/gcp/modules/installation/iam.tf` | the launcher's `secretAccessor` member (D2) | 5 |
| `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, `deploy/terraform/terraform_test.go`, `internal/infra/tf/testdata/golden/` | the member's tftest and the regenerated plans | 5 |
| `internal/budget/token/account.go` (new) | the OAuth object's key, its seal, its take | 6 |
| `internal/cli/launch_budget.go`, `internal/cli/run.go` | the launcher writes the OAuth object beside the budget token | 6 |
| `internal/runner/budget_session.go`, `internal/runner/runner.go` | the runner takes it, redacts it, sets `CLAUDE_CODE_OAUTH_TOKEN` | 6 |
| `internal/cli/project_job.go` | the launch gate: `checkProjectJobOAuth` retired, `checkProjectJobAccount` extended | 7 |
| `internal/cli/doctor.go` | the project-account checks | 7 |
| `SECURITY.md`, `docs/`, `website/` | D8's sentences, the user-facing docs, the CLI reference | 8 |
| `internal/cli/docs_project_named_oauth_tokens_test.go` (new) | pins this design's and this plan's load-bearing claims | 8 |

## PR group

| PR | Tasks | What it is |
| --- | --- | --- |
| 1 | 1 | the seal, merged from `next-0.9` — no new work of this feature's own |
| 2 | 2, 3, 4 | the policy's accounts block, the profile field, the populated axis. Inert: nothing reads them yet |
| 3 | 5, 6 | **SECURITY-SENSITIVE.** The launcher's grant and the sealed object. Inert: nothing launches an OAuth run onto a shared job yet |
| 4 | 7 | **SECURITY-SENSITIVE.** The gate flips; the feature works |
| 5 | 8 | the documentation and the pinned claims |

Task 8 can be split so its SECURITY.md half lands with PR 4, which is the honest place for it: the
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
- **Retiring a project job.** `RefuseProjectJobDeletes` still has no way back, and Task 4's rename
  makes that a live concern for any installation with profiles. Not fixed here.
- **`fugaro run`'s failover into a project account** — the design's §4.4 refuses rather than fails
  over; a failover would need D8's wording to change with it.

---

### Task 1: Merge the seal

**Files:** `internal/budget/token/` (seal.go, object.go, seal_test.go, seal_aad_test.go,
newkey_test.go, export_test.go), `internal/runner/`, `internal/cli/`, `SECURITY.md`,
`docs/design/budget-token-channel.md` — all as they are on `origin/next-0.9`.

**What it is:** the 24 commits of `origin/next-0.9` whose base is an ancestor of this branch:
Slice A (the runner's half, the versioned envelope, `Seal`/`Unseal`, the key's one spelling) and
Slice B (the launcher seals, the retry re-seal, the `tokens/` move with its grants, SECURITY.md's
two sentences). No new work of this feature's own — it is a merge, and the only thing this plan
asks of it is that it is *done before* anything else, because every later task's carrier is this
API.

**Verification:** `fugaro verify test`, plus the merge's own gates (the golden plans and the tftest
for the `tokens/` grants move with it). Confirm the shape the later tasks depend on:
`token.ObjectPrefix == "tokens"`, `token.ObjectKey(slug, run) == "tokens/<slug>/<run>/budget-token"`,
`PutObject` and `TakeObject` taking a key, and `ErrSealedNoKey` / `ErrNotSealed` exported.

**Depends on:** nothing.

**Commit:** a merge commit naming `origin/next-0.9`.

### Task 2: The policy's `accounts` block

**Files:** `internal/projectpolicy/policy.go`, `internal/projectpolicy/accounts.go` (new),
`internal/projectpolicy/accounts_test.go` (new), `internal/projectpolicy/profiles.go`,
`internal/cli/project_cmd.go` (the `project policy publish` command),
`internal/cli/sharedcfg.go` (the published document's own validation),
`internal/cli/project_policy_test.go`.

**What it does:** adds `Policy.Accounts map[string]Account` with `Account{AllowRepos []string}`,
decoded strictly (an unknown key inside the block is refused, as everywhere else in the policy),
validated (`allow_repos` non-empty, every entry a repo slug, no dot segments — the rules
`ValidateClaudeAccounts` already applies to `allow_data_to`,
`internal/config/claude_accounts.go:161-189`), and *derived* by `fugaro project policy publish`
from `localcfg.ClaudeAccounts[name].AllowDataTo`. Publish gains the subset rule: a profile naming an
account whose `allow_repos` does not cover the profile's own `allow_repos` is refused, naming both
lists and the repository in one and not the other.

**SECURITY-SENSITIVE — independent opus review.** This is the gate's definition; a decode that
accepts an unknown key or a validation that lets an empty `allow_repos` through turns the gate into
a decoration.

**Tests:** `TestAccountsBlockDecodes`, `TestAccountsUnknownKeyRefused`,
`TestAccountAllowReposMustBeNonEmpty`, `TestAccountAllowReposSlugShape`,
`TestPublishDerivesAccountsFromLocalConfig`,
`TestPublishRefusesProfileAllowReposNotCoveredByAccount`.

**Red step:** add `TestAccountAllowReposMustBeNonEmpty` and delete the non-empty check from
`accounts.go`'s validation. **Expected: FAIL** — the empty list decodes and validates, so the gate
admits every repository. Restore the check; the test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... ./internal/cli/ -run 'Accounts|Policy' -count=1`

**Depends on:** nothing (inert without Tasks 3, 4 and 7).

**Commit:** `projectpolicy: the accounts block, and the allow-list publish derives from it`.

### Task 3: The profile field

**Files:** `internal/config/config.go` (`JobProfile`, at :519), `internal/config/validate.go`,
`internal/localcfg/localcfg.go`, their tests, `internal/cli/sharedcfg.go` if the field must be
refused in the shared config as profiles already are.

**What it does:** adds `JobProfile.ClaudeAccount string` (yaml `claude_account`), shape-checked
against `config.ClaudeAccountNameRE` and `MaxClaudeAccountNameLen`, and — at local-config load and
at publish — resolved against `lc.ClaudeAccounts` so a profile may only name an account the
operator's own config defines. Adds the no-fallback rule: a profile that names an account may not
also mount an Anthropic API key, in either spelling (`anthropic-api-key` directly, or a provider
whose `Secret` is it), refused with a message that says which of the two to drop.

**Tests:** `TestJobProfileClaudeAccountShape`, `TestProfileAccountMustBeConfigured`,
`TestProfileAccountWithAnthropicKeyRefused`, `TestProfileAccountWithProviderKeyRefused`.

**Red step:** delete the mutual-exclusion check and run
`TestProfileAccountWithAnthropicKeyRefused`. **Expected: FAIL** — the profile decodes with both
credentials, which is the state the no-fallback property forbids. Restore the check; it passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/config/... ./internal/localcfg/... ./internal/cli/ -run 'Profile|ClaudeAccount' -count=1`

**Depends on:** nothing (inert; no profile can be deployed with it until Task 4).

**Commit:** `config: a profile may name a Claude account, and never beside an API key`.

### Task 4: The account axis, populated

**Files:** `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`,
`internal/infra/tf/testdata/golden/` (regenerated), `deploy/terraform/gcp/modules/project/` if the
module's object type needs the field (it already has `account`), `internal/runner/projectjob.go`
and its tests.

**What it does:** replaces `const account = ""` (`projectjobs.go:96`) with the account the
profile's new field names, computed through one function shared with the launcher and the runner
(Review Focus 2). The job's name and service account gain the account segment through the existing
`gcp.ProjectJobName` / `gcp.ProjectServiceAccountID`. `projectJobSecrets` and `projectSecretEnv`
are **unchanged**: the account is not a `secret_env`, which is the whole point of Task 6.
`ProjectJobs`' doc comment loses its "empty until phase 5" paragraph and gains the new rule.

**Verification:** `terraform fmt -check`, `terraform validate` on the installation root, the golden
plans regenerated and reviewed (the diff is the account segment appearing on the jobs of profiles
that name one, and nothing else), `fugaro verify test`. **`RefuseProjectJobDeletes` will fire for
an installation whose profiles gain an account** — that is the guard working; the rollout step is
documented in Task 8, not worked around here.

**Red step:** add `TestProjectJobAccountAxis`, then restore `const account = ""`. **Expected:
FAIL** — the deployed job's name and service account lose the account segment, and a run's
`jobIdentity` binds to the wrong job. Remove the constant again; the test passes.

**Depends on:** Task 3.

**Commit:** `infra: a project job is keyed by its profile's Claude account`.

### Task 5: The launcher's read — SECURITY-SENSITIVE

**Files:** `deploy/terraform/gcp/modules/installation/iam.tf`,
`deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` (and the root's
`variables.tf` / `main.tf` if the member needs a new input), `deploy/terraform/terraform_test.go`
(the tftest driver), `internal/infra/tfvars.go`, `internal/infra/tf/testdata/golden/`
(regenerated).

**What it does (D2):** one `google_secret_manager_secret_iam_member` per account a **published
profile names**, member = the launcher account, role `roles/secretmanager.secretAccessor`, target =
the project-scope secret container the installation already creates for that account
(`tfvars.go:271-276`). A member resource, never a binding, with the same `precondition` discipline
as the rest of the file. Bounded by "an account a published profile names": an account in the
operator's local config that no profile serves gets no member.

**SECURITY-SENSITIVE — independent opus review.** This is the one grant the feature adds, and it
lets the launcher read a long-lived credential. The reviewer should be asked specifically: is the
member bounded to exactly the published accounts; is the role the narrowest that works; does the
precondition re-check the same condition in the same change; and does the tftest fail if the member
is granted on an account no profile names.

**Tests:** a tftest for the member's existence, its role, its target and its precondition; a Go test
that `tfvars` publishes exactly the profiles' accounts; a tftest or Go test that an unused
local-config account gets no member.

**Red step:** widen the member to every account in `lc.ClaudeAccounts` and run the "unused account
gets no member" test. **Expected: FAIL.** Narrow it back; the test passes.

**Depends on:** Task 4 (the set of published accounts is what Task 4 makes real).

**Commit:** `infra: the launcher may read the accounts its profiles serve`.

### Task 6: The sealed object — SECURITY-SENSITIVE

**Files:** `internal/budget/token/account.go` (new) + `account_test.go` (new),
`internal/cli/launch_budget.go`, `internal/cli/run.go`, `internal/cli/run_test.go`,
`internal/runner/budget_session.go`, `internal/runner/runner.go`,
`internal/runner/*_test.go`.

**What it does (D1, D7):** `token.AccountObjectKey(slug, run)` =
`tokens/<slug>/<run>/claude-oauth-token`; the launcher seals the account's token into it under the
same per-launch key, create-if-absent, with the retry re-seal mirrored; the runner takes it once,
unseals it, registers the plaintext with `addSecret` (`internal/runner/runner.go:609-616`) **before
anything else can see it**, sets `CLAUDE_CODE_OAUTH_TOKEN` in the agent's environment, and refuses
the run when the marker promises a token that is absent, sealed-with-no-key, sealed under another
key, or sealed under another name. It is written **only** when the run's job is a shared project
job whose profile names an account: the per-repository path keeps its static mount
(`internal/infra/spec.go:585-586`) untouched.

**SECURITY-SENSITIVE — independent opus review.** The reviewer should be asked: does the take
refuse every one of the four shapes; is the plaintext registered with the redactor before the agent
starts and before any log line can carry it; does the token reach git's environment or the launch
request body by any route; is the object deleted or left, and is the answer the same as the budget
token's; and does the create-if-absent/retry pair have the same failure mode as the budget token's.

**Tests:** `TestAccountObjectRoundTrip`, `TestAccountObjectSealedNoKeyRefused`,
`TestAccountObjectWrongKeyRefused`, `TestAccountObjectWrongNameRefused`,
`TestAccountObjectPlaintextRefused`, `TestAccountObjectRetryReseals`,
`TestOAuthTokenRedactedFromRunOutput`, `TestOAuthTokenNeverInLaunchEnv` (the pattern of
`next-0.9`'s `23b2fca`), `TestPerRepositoryOAuthPathUnchanged`.

**Red step:** make the take fall back to reading the object's bytes as the token when unsealing
fails. **Expected: FAIL** — `TestAccountObjectWrongKeyRefused` and
`TestAccountObjectPlaintextRefused` both go red, which is exactly the "plaintext fallback is the
exposure the seal exists to close" refusal. Remove the fallback; both pass.

**Command:** `GOFLAGS=-p=2 go test ./internal/budget/token/... ./internal/cli/ ./internal/runner/ -run 'Account|OAuth|Seal' -count=1`

**Depends on:** Tasks 1, 4, 5.

**Commit:** `budget/token: the run's Claude account token travels sealed`.

### Task 7: The gate — SECURITY-SENSITIVE

**Files:** `internal/cli/project_job.go`, `internal/cli/project_job_test.go`,
`internal/cli/doctor.go`, `internal/cli/doctor_test.go`, `internal/runner/projectjob.go` and its
tests.

**What it does:** retires `checkProjectJobOAuth` (`project_job.go:223-233`) and replaces it with
the real gate: an OAuth launch onto a shared job is allowed when the profile names an account, the
repository is in that account's `allow_repos` (and the profile's), and the account's token has a
project-wide container; each failure names the field to fix (design §4.4's table). Extends
`checkProjectJobAccount` (`:154-176`) so a run's own selection must be the job's account. Adds the
runner-side half: the runner refuses a run whose job's account the policy's `accounts` block does
not cover for the repository it is running (Review Focus 5). Adds `fugaro doctor` checks for the
three publish-time facts a launch would otherwise discover late, with the same severity discipline
`claude-accounts-stored` and `oauth-token-allowed` already follow (an ordinary finding for a real
problem, a warning — never silence — when the check itself could not be answered,
`internal/cli/doctor.go:1230-1325`).

**SECURITY-SENSITIVE — independent opus review.** This is the task that turns the feature on, and
its reviewer should be asked: does every one of design §4.4's rows refuse; does any path fall
through to the per-repository branch and thereby run with no account; is the runner's refusal
independent of the launcher's; and does `--token` on a shared job refuse rather than silently
select the job's own account.

**Tests:** `TestProjectJobOAuthAllowedWhenAccountCovered`, `TestProjectJobOAuthRefusedNoAccount`,
`TestProjectJobOAuthRefusedRepoNotAllowed`, `TestProjectJobOAuthRefusedNoContainer`,
`TestRunTokenMustBeTheJobsAccount`, `TestRunnerRefusesUncoveredAccount`,
`TestDoctorProjectAccountChecks`.

**Red step:** restore `checkProjectJobOAuth`'s unconditional refusal beside the new gate.
**Expected: FAIL** — `TestProjectJobOAuthAllowedWhenAccountCovered` goes red, which proves the new
gate is what admits the launch rather than the old refusal having been bypassed. Remove it; the
test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/runner/ -run 'ProjectJob|Account|Doctor' -count=1`

**Depends on:** Tasks 2, 3, 4, 6.

**Commit:** `cli: a shared project job may serve a named Claude account`.

### Task 8: The documentation, and the claims pinned

**Files:** `SECURITY.md`, `docs/design/project-named-oauth-tokens.md` (its Status line),
`docs/named-oauth-tokens.md` (the "project level" pointer), `docs/project-layer.md` or
`docs/gcp-setup.md` (the rollout step), `website/` (the CLI reference, if any help text changed),
`internal/cli/docs_project_named_oauth_tokens_test.go` (new).

**What it does (D8, D9, D10):** SECURITY.md gains the design's §6.1 in the owner's terms — a
hostile run can use the token during its own run; the account is the blast radius; nothing prices
an OAuth run the way the gateway prices a provider-key run; and the launcher reads the account's
secret (one line in the launcher paragraph). It also gains the profile's new meaning where it
already explains the shared job's boundary. The design's Status line moves from "design" to "built
in 0.9.0", with what is built and what is not. The rollout step for Task 4's rename is written down
as the one-time operator action it is, with `RefuseProjectJobDeletes` named as the guard the
operator will meet. `fugaro project policy show` prints the `accounts` block (D10).

**Tests:** `docs_project_named_oauth_tokens_test.go` pins the load-bearing sentences — the
`claude_account` field's name, the two refusals' wording, the no-fallback rule, the "hostile run
can use the token during its own run" residual, and that `isClaudeOAuthSecret` still refuses the
`secrets:` spelling — in the house style
(`internal/cli/docs_named_oauth_tokens_test.go` is the model: read the doc, assert the phrases,
and tie the behavioural ones to the real function rather than to text alone).

**Red step:** delete the SECURITY.md residual sentence and run the new docs test. **Expected:
FAIL.** Restore it; the test passes.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1`

**Depends on:** Tasks 2-7 (it documents what they built).

**Commit:** `docs: SECURITY.md's OAuth residual, and the 0.9.0 rollout step`.

---

## What could not be verified without a live check

Restated from the design's §7 so it is not lost in the plan: **U1** whether an execution's override
can be read back through the Cloud Run API (the channel doc's U2, Check 52 step 3, unrun — it
decides how wide the seal's audience is); **U2** which credential Claude Code prefers when both
`CLAUDE_CODE_OAUTH_TOKEN` and `ANTHROPIC_API_KEY` are set (Task 3's refusal removes the question
rather than answering it); **U3** whether one account's token serves several concurrent runs (it
decides whether the feature is useful, not whether it is safe); **U4** whether the per-launch key
survives a Cloud Run retry exactly as the budget token assumes. All four are owner-run, sandbox
only, and none of them blocks Task 1.
