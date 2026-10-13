# Project-level secrets: implementation plan

**Goal:** let `anthropic-api-key`, `bitbucket-token`, `github-app-key`, `claude-oauth-token` (default), a model provider's key, or a named Claude account's token be stored **once per project** and mounted by every repository the owner allows, while a repository's own stored value still overrides it. No code in this plan has been written or run; this is a design-to-code plan, the same status as `docs/plans/2026-10-09-named-oauth-tokens.md` before its Task 1 started. **This PR (the one that adds this plan and its design doc) is docs-only: no file under this plan's "Files" lists is touched by it.** Tasks 1 onward are future work, gated on the owner approving the design.

**Spec:** [docs/design/project-secrets.md](../design/project-secrets.md). Its decisions (§3 opt-in model, §4 storage, §5 mounting/resolution, §6 migration) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable. **§3a carries an open question the owner still needs to answer** (whether `CheckProviderPolicy`'s data-policy gate also opens on an absent `allow_data_to`); this plan implements the design's default, Option A (gate stays closed), per P7.

**Depends on:** release 0.7.x on `main` (the base-image hard cut already merged). No functional dependency on any other in-flight design.

**Checked before this plan was written:** every fact the design doc cites was read from the code at this branch's base — `internal/config/providers.go`, `internal/config/claude_accounts.go`, `internal/config/config.go`, `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/backend/gcp/names.go`, `internal/backend/gcp/secrets.go`, `internal/cli/secrets.go`, `internal/cli/initsecrets.go`, `deploy/terraform/gcp/modules/{installation,repo,workflow}/*.tf`, `internal/infra/tf/guard.go`, `docs/design/layered-config.md`, `docs/project-layer.md`, `docs/multi-model.md`, `SECURITY.md`, `docs/gcp-live-checklist.md`. If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the same rule `docs/plans/2026-10-08-upgrade.md` and `named-oauth-tokens.md`'s plan use).

## Decisions (veto any before execution starts)

- **P1. `project_secrets` is keyed by the exact logical secret name and may hold only a `config.ReservedSecrets` key.** No separate allow-list for a provider's or a named Claude account's secret — their own `allow_data_to` governs project-sharing too (design §3). `ValidateProjectSecrets` refuses a `project_secrets` key that is a provider's `.Secret` or any `config.ClaudeAccountSecret(name)`, pointing at that object's own `allow_data_to` instead. *Veto alternative:* one allow-list block covering every kind of secret uniformly (including providers/accounts), accepting the "two lists that must agree" risk the design rejected.
- **P2. `rs.Secrets` (the repo module's own containers) is never pruned when a name becomes project-shared.** The repo-owned container keeps existing, inert if unused, in every repository's Terraform state (design §5, §6). This is the one choice that keeps `prevent_destroy` from ever blocking an apply because of a scope change. *Veto alternative:* drop a project-shared name from `rs.Secrets` once no repo-level version exists, which would require `terraform state rm`-style handling before any apply could proceed — rejected as a strictly worse migration story for no operational gain.
- **P3. Precedence (repo overrides project) is resolved once, in `internal/infra.Readiness`, at `fugaro init --repo` apply time — never at run launch.** Cloud Run Jobs mount secrets at the job, not the execution (the same fact `named-oauth-tokens.md` §1 establishes); re-resolving at every run would need a live Secret Manager read on every launch, which nothing in this codebase does today. *Veto alternative:* resolve in the runner at boot (a live call each run); rejected as a new, unprecedented live dependency on the hot path, and because it would not change which secret a *job's IAM* can reach — only the job's own grant does that, and that is fixed at apply time regardless.
- **P4. `build.tf`'s build-account grant is rewired to go through the same Go-computed map `sa.tf` already uses**, instead of indexing `google_secret_manager_secret.this` directly (design §5). This is the one genuine Terraform-shape change this feature needs; everything else is new Go computation over an unmodified module shape. **Flagged for opus review (security- and Terraform-sensitive): Task 5.**
- **P5. No command deletes or migrates a now-redundant repo-owned secret.** Decommissioning is a manual, owner-paced `terraform state rm`/delete, outside this plan (design §6 step 5, §9 non-goals).
- **P6. No `*Since` version-gate constant is added.** This feature changes which secret ID a job's existing env var is mounted from, never a new field the runner's `task.Parse` must know about (design §7). A repository needs `fugaro init --repo` re-applied before project-sharing takes effect for it, exactly like any other `infra` change — no new constant, no new skew test family.
- **P7. A provider's empty/absent `allow_data_to` means "available to any repository" for *mounting and storage* only (owner ruling, 2026-10-13; design §3a), via a new `ModelProvider.ProviderKeyAvailable(repo)` method that is never called from `CheckProviderPolicy` or `runner.ProvidersEnv`.** **Three** existing call sites change from `AllowsData` to `ProviderKeyAvailable` (corrected from "two" — B5(a) found a third): `SecretMounts`'s per-provider mount condition (Task 2), `Readiness`'s provider-secret precedence check (Task 3), and `secretsSet`'s repo-scope validation (`internal/cli/secrets.go:222`, Task 4) — without the third, an operator could never store a repo-level override for a no-list provider at all, since the repo-scope path would refuse every repository. `CheckProviderPolicy` (`providers.go:334`) and `runner.ProvidersEnv` (`internal/runner/providers.go:32`) are untouched by this plan — Option A of the design's open question, the default until the owner answers otherwise. `project_secrets`/`claude_accounts` resolution keeps using `AllowsData` unchanged; this ruling is scoped to `providers:` only. *Veto alternative:* also flip both `CheckProviderPolicy` and `ProvidersEnv` to `ProviderKeyAvailable` (Option B) — not implemented in this plan; if the owner picks it, it is a two-file change (not one — design §3a corrects an earlier claim that it was), each needing a test, and `ProvidersEnv`'s own doc comment ("only the providers that list the job's repository... are in it") needs updating too since it would no longer be literally true.
- **P7a. An existing, already-deployed job must never gain a secret_env entry with no version anywhere (B5(a)).** `Readiness`'s existing-job branch (`readiness.go:121-122`, `case exists: ws.DeployJob = true`, unconditional) predates this feature and was never designed to protect a live job from an update that adds a brand-new, unresolvable secret reference — which configuring a provider with no list is now enough to trigger, for every existing `agent.auth: api-key` job, with no local `fugaro.yaml`/config edit for that specific repository. Fixed by having `Readiness` omit such an entry from the workflow's spec for that one apply (reporting `Missing`, not refusing the whole update) rather than ever setting `DeployJob` to `false` for an existing job (job.tf's `count = var.workflow.deploy_job ? 1 : 0` would destroy it outright — strictly worse). *Veto alternative:* refuse the whole `fugaro init --repo` apply outright whenever any workflow would gain an unresolvable mount; rejected as a worse ergonomic regression than simply not adding the one mount yet, and inconsistent with how a missing image is already handled for an existing job (`readiness.go:122-130`, it keeps the current image rather than refusing).

## Global Constraints

From the design:
- `project_secrets` (and every existing `providers`/`claude_accounts` key reused for this) is local-config, operator-only; `localcfg.Config.Shared()` strips it (design §7) — a test pins this the same way `TestMergeSharedNeverCarriesProviders`/`...ClaudeAccounts` already does.
- No secret value is ever read back, echoed, or logged, by any new code path (`fugaro secrets set --project-wide`, `ls --project-wide`, `doctor`'s two new checks all use `List`/metadata only, never `Access`).
- No `fugaro.yaml` key, JSON Schema entry, or project-layer scope-table row is touched: this feature is local-config and Terraform/Go only.
- `rs.Secrets`'s `for_each` never shrinks because of this feature (P2) — a golden test diffs it.
- No live GCP call in any automated test; Check 40's items (design §8) are the owner's own task, run once, sandbox/`fugaro` installations only, never `belong`.
- `CheckProviderPolicy` (`providers.go:334`) and `runner.ProvidersEnv` (`internal/runner/providers.go:32`) are never edited by this plan (P7, design §3a, Option A) — every task touching `ProviderKeyAvailable` keeps both lines, and `AllowsData` itself, byte for byte.
- An existing, already-deployed job never gains a secret_env entry with no version anywhere (P7a) — pinned by a dedicated Task 3 test, not merely implied by the `MissingSecret` gate (which only ever protected a brand-new job before this feature).

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the foreground, `-count=1`, `-race` where the package's existing tests use it.
- Fakes only in tests: `gcpfake`, no real Secret Manager, no real Terraform apply.
- Docs must match behavior: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout (this plan adds no `fugaro.yaml` key, so no schema/docs-lint edit is expected — a task that finds otherwise stops and reports rather than loosening the lint).

## Review Focus (mutation-proof notes)

1. **A repository outside an allow-list gets a project secret's variable anyway**, or `secrets set --project-wide` accepts a name with an empty allow-list. Pinned by `TestProjectSecretAllowedRepos` (Task 1), `TestReadinessOmitsProjectSecretForUnlistedRepo` (Task 3), `TestSecretsSetProjectWideRefusesEmptyAllowList` (Task 4).
2. **A repo-level override stops winning even though it still has a version**, or a project copy is chosen while the repo copy is live — the precise bug an earlier draft of Task 3 had (B2 in review: "populated unconditionally wherever eligible" plus a Terraform-side `merge()` that lets the second map win would always resolve to the project copy, inverting "repo > project" for every eligible name, not just the unversioned ones). Pinned by `TestReadinessRepoVersionMeansNoProjectSecretsEntry` and `TestReadinessFallsBackOnceRepoVersionGone` (Task 3) — both assert `out.ProjectSecrets` has no entry (or gains one only once the repo version is gone), not merely that the job deploys or that some other field looks right.
3. **A scope change plans a delete of an existing repo-owned secret.** Pinned by `TestBuildRepoSpecNeverPrunesRepoSecrets` (Task 2, a golden diff of `rs.Secrets`'s key set before/after a name becomes project-shared) and `TestInstallationRootPlanHasNoRepoSecretDeletes` if a Terraform-plan-level test harness exists for the installation root (Task 6; otherwise a Go-level assertion suffices and the task says so).
4. **The build account's grant (or the check job's secret reference, B4) drifts from the job account's resolved secret** (P4): the `repo.tftest.hcl` `run "build_and_check_use_the_resolved_secret"` block (Task 5) asserts all **three** `local.secrets`-consuming paths (job, build, check) resolve the identical ID for the identical logical name, for every fixture — through the real Terraform test framework, not a Go unit test, since only it can observe what a Terraform `local` actually resolves to.
5. **A value leaks through `ls --project-wide`, `doctor`, or a plan/state print.** Pinned by `TestSecretsLsProjectWideNeverPrintsValue`, `TestDoctorProjectSecretChecksReadNoValue` (Task 4/7), both asserting the fake Secret Manager's `Access` is never called.
6. **The P7 widening (a no-list provider's key becomes mountable everywhere) leaks into `CheckProviderPolicy`, or `ProviderKeyAvailable`'s empty-list branch silently vanishes.** Pinned by `TestProviderKeyAvailableNeverChangesAllowsData` (Task 1, asserts `AllowsData` on an empty list stays `false` — the thing `CheckProviderPolicy` calls — even though `ProviderKeyAvailable` now returns `true`) and the paired `TestSecretMountsNoListProviderAvailableToAnyRepo`/`TestSecretMountsListedProviderStillRefusesUnlistedRepo` (Task 2) and `TestReadinessProviderNoListEligibleForEveryRepo`/`TestReadinessProviderListedExcludesUnlistedRepo` (Task 3), each pair covering both the empty-list and non-empty-list branches so a mutation that collapses `ProviderKeyAvailable` to either `AllowsData` or `true` always fails at least one test in its pair.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/project_secrets.go` (new), `project_secrets_test.go` (new) | `ProjectSecret`, `ValidateProjectSecrets` (reserved-name membership only, no collision check — S1), `AllowedForProjectSecret` | 1 |
| `internal/localcfg/localcfg.go`, `localcfg_test.go`, `internal/localcfg/shared.go`, `shared_test.go` | `Config.ProjectSecrets` field, `Shared()` strips it, `Validate` wiring | 1 |
| `internal/config/providers.go`, `providers_test.go` | `ProviderKeyAvailable` (P7, owner ruling, design §3a) — never called from `CheckProviderPolicy` | 1 |
| `internal/backend/gcp/names.go`, `names_test.go` | `ProjectSecretID` | 2 |
| `internal/infra/spec.go`, `spec_test.go` | `rs.Secrets` unchanged (golden); `SecretMounts` refuses a workflow secret whose name collides with a `project_secrets` entry (design §3); `SecretMounts`'s per-provider mount condition switches to `ProviderKeyAvailable` (P7) | 2 |
| `internal/infra/readiness.go`, `readiness_test.go` | the live precedence check; rewrites a workflow's resolved `SecretIDs[name]`; a provider's own name resolves eligibility via `ProviderKeyAvailable`, not `AllowsData` (P7) | 3 |
| `internal/cli/secrets.go`, `secrets_test.go` | `--project-wide` flag, validation branch (3 call sites, S1 order fix), `ls` SCOPE column, `ls --project-wide` | 4 |
| `deploy/terraform/gcp/modules/installation/secrets.tf` (new), `variables.tf`, roots/installation | `google_secret_manager_secret.project`, operator grants, `var.project_secrets` input | 4 |
| `internal/infra/tfvars.go`, `imports.go`, `discover.go` | `InstallationSpec.ProjectSecrets`, `importProjectSecret`, `DiscoverInstallation`'s adoption step (S2) | 4 |
| `deploy/terraform/gcp/modules/repo/build.tf`, `workflows.tf`, `check.tf`, `variables.tf`, `internal/infra/tfvars.go`/`spec.go`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl` | **P4, opus review:** `local.secrets`; `build.tf` AND `check.tf` (B4) rewired through it; the Go-to-tfvars wire for `RepoSpec.ProjectSecrets` | 5 |
| `internal/infra/installation_terraform_test.go`, `deploy/terraform/gcp/roots/installation/tests/`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `internal/infra/spec_test.go` golden fixtures | real `terraform test` plan-level fixtures: no secret-resource deletes, but the expected IAM-member replacements, `tf.Guard` refusing/accepting them (B3) | 6 |
| `internal/cli/doctor.go`, `doctor_test.go` | two new checks (design §5) | 7 |
| `docs/design/v1.md`, `docs/multi-model.md`, `docs/gcp-live-checklist.md`, `docs/release.md`, `internal/cli/docs_*_test.go` | cross-references, Check 40, the release checklist bullet | 8 |
| none | full suite | 9 |
| `docs/gcp-live-checklist.md` (results pasted in) | the owner's own run, Check 40a-g | 10 |

## PR group

One PR, branch `project-secrets`, Tasks 1 to 9 in order for the merge; Task 10 is the owner's own run, not blocking (the same relationship `named-oauth-tokens.md`'s Task 11 has to its own PR).

Parallelizable: **Task 2 needs Task 1** (its `SecretMounts` collision refusal reads `config.ProjectSecret`/`lc.ProjectSecrets`, and its P7 mount-condition change reads `ProviderKeyAvailable`, both added in Task 1), so they are no longer independent — Task 1 goes first, Task 2 follows directly after. Task 3 needs 1 and 2 (its P7 resolution also reads `ProviderKeyAvailable`). Task 4 needs 1, 2 and the installation Terraform half of itself (its Go and Terraform halves can split further if useful). Task 5 needs 2 (and is reviewed separately from the rest, per P4). Task 6 needs 4 and 5. Task 7 needs 3. Task 8 needs 1 through 7 settled. Task 9 is last.

---

### Task 1: `config.ProjectSecret`, its validation, the local config field, and `ProviderKeyAvailable` (P7)

**Files:**
- Create: `internal/config/project_secrets.go`
- Test: `internal/config/project_secrets_test.go`
- Edit: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`, `internal/localcfg/shared.go`, `internal/localcfg/shared_test.go`
- Edit: `internal/config/providers.go`, `internal/config/providers_test.go` (P7, the owner's 2026-10-13 follow-up ruling, design §3a — independent of `ProjectSecret` above; both are local-config-layer additions this task delivers together)

**Interfaces (signature corrected per S1: `providers`/`accounts` parameters removed — a provider's `.Secret` and a named account's derived secret can never collide with a `config.ReservedSecrets` key in the first place, by construction, so a collision check needs no access to either object; see below):**
```go
// package config
type ProjectSecret struct{ AllowDataTo []string }
func (p ProjectSecret) AllowsData(repo string) bool
func AllowedForProjectSecret(secrets map[string]ProjectSecret, name, repo string) bool
func ValidateProjectSecrets(secrets map[string]ProjectSecret) []Problem

// providers.go, next to AllowsData — never called by CheckProviderPolicy (P7)
func (p ModelProvider) ProviderKeyAvailable(repo string) bool
```
`ValidateProjectSecrets` refuses (P1): a key not in `config.ReservedSecrets`; a malformed `allow_data_to` entry (reusing `repoSlugRE`/`hasDotSegment`, exactly as `ValidateClaudeAccounts` does, `claude_accounts.go:136-140`). **No collision check against `providers`/`claude_accounts` is needed or added (S1):** `ValidateModelProviders` already refuses a provider's `.Secret` from ever being a `config.ReservedSecrets` key (`providers.go:127-129`, unconditional), and no named claude account's derived secret (`claude-oauth-token-<name>`) is shaped like any of the 4 reserved keys either — so once `project_secrets`'s own keyspace is `ReservedSecrets`-only, neither kind of collision can structurally occur. An earlier draft of this interface built such a check with `ClaudeAccountBySecret`, which would have incorrectly refused `project_secrets.claude-oauth-token` itself (the bare, intended default-account case, design §3's own example) — `ClaudeAccountBySecret(accounts, "claude-oauth-token")` always returns `(DefaultClaudeAccount, true)`, configured or not (`claude_accounts.go:91-93`) — caught in review and removed rather than special-cased, since the check itself was unnecessary.

`ProviderKeyAvailable(repo)` is `len(p.AllowDataTo) == 0 || p.AllowsData(repo)` (design §3a): true for any repo when the list is empty or nil (the two are not distinguished — design §3a's own reasoning), otherwise identical to `AllowsData`. It is a pure addition: `AllowsData` itself, and every existing caller of it (`CheckProviderPolicy` at `providers.go:334` chief among them), is untouched, byte for byte.

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/project_secrets_test.go
package config

import "testing"

func TestProjectSecretAllowedRepos(t *testing.T) {
	secrets := map[string]ProjectSecret{
		"anthropic-api-key": {AllowDataTo: []string{"acme/app", "Acme/Sandbox"}},
	}
	if !AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/app") {
		t.Fatal("an allowed repo was refused")
	}
	if !AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/sandbox") {
		t.Fatal("case-insensitive match failed")
	}
	if AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/other") {
		t.Fatal("an unlisted repo was allowed")
	}
	if AllowedForProjectSecret(secrets, "bitbucket-token", "acme/app") {
		t.Fatal("a name with no entry was allowed")
	}
}

func TestValidateProjectSecretsRefusesNonReservedName(t *testing.T) {
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"db-password": {AllowDataTo: []string{"acme/app"}}})
	if len(ps) != 1 {
		t.Fatalf("ValidateProjectSecrets = %+v, want one problem", ps)
	}
}

func TestValidateProjectSecretsAcceptsReservedName(t *testing.T) {
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("a valid block was refused: %+v", ps)
	}
}

func TestValidateProjectSecretsAcceptsBareClaudeOAuthToken(t *testing.T) {
	// S1, the regression guard: this is the exact case a naive
	// ClaudeAccountBySecret-based collision check would have wrongly
	// refused (ClaudeAccountBySecret(accounts, "claude-oauth-token")
	// always matches the implicit default account, configured or not).
	// project_secrets.claude-oauth-token is design §3's own documented,
	// intended configuration and must never be refused.
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"claude-oauth-token": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("the bare default claude-oauth-token name was refused: %+v", ps)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestProjectSecret|TestValidateProjectSecrets' ./internal/config/`
Expected: FAIL, build failed: `undefined: ProjectSecret`.

- [ ] **Step 3: Write the implementation**

`internal/config/project_secrets.go`, mirroring `claude_accounts.go`'s shape exactly (`AllowsData` case-folded via `strings.EqualFold`, `sortedKeys`, `pluginwire.Printable` on every echoed value). `ValidateProjectSecrets` iterates `sortedKeys(secrets)`; for each name checks `_, reserved := ReservedSecrets[name]` — nothing else (S1: no `ProviderBySecret`/`ClaudeAccountBySecret` call, since neither parameter exists on this function any more).

Edit `internal/localcfg/localcfg.go`: add, next to `ClaudeAccounts`:
```go
	// ProjectSecrets are the reserved secret names (config.ReservedSecrets)
	// the owner has opted into project-wide storage for, by allow-listed
	// repository (design docs/design/project-secrets.md §3). Only the
	// owner sets them, here, never in fugaro.yaml. A provider's or a named
	// Claude account's own allow_data_to governs its own secret instead
	// (ValidateProjectSecrets refuses a name that collides with one).
	ProjectSecrets map[string]config.ProjectSecret `yaml:"project_secrets,omitempty"`
```
Wire `config.ValidateProjectSecrets(c.ProjectSecrets)` next to the existing `ValidateModelProviders`/`ValidateClaudeAccounts` calls.

Edit `internal/localcfg/shared.go`: add `s.ProjectSecrets = nil` next to `s.Providers = nil`/`s.ClaudeAccounts = nil` (`shared.go:37-38`).

- [ ] **Step 4: Write the failing tests for `ProviderKeyAvailable` (P7)**

```go
// internal/config/providers_test.go
func TestProviderKeyAvailableNoListMeansAnyRepo(t *testing.T) {
	for _, p := range []ModelProvider{{}, {AllowDataTo: nil}, {AllowDataTo: []string{}}} {
		if !p.ProviderKeyAvailable("acme/anything") {
			t.Fatalf("ProviderKeyAvailable(%+v) = false, want true (no list set)", p)
		}
	}
}

func TestProviderKeyAvailableNonEmptyListMatchesAllowsData(t *testing.T) {
	p := ModelProvider{AllowDataTo: []string{"acme/app", "Acme/Sandbox"}}
	for _, repo := range []string{"acme/app", "acme/sandbox"} {
		if !p.ProviderKeyAvailable(repo) {
			t.Errorf("ProviderKeyAvailable(%q) = false, want true (listed)", repo)
		}
		if !p.AllowsData(repo) {
			t.Errorf("sanity: AllowsData(%q) = false", repo)
		}
	}
	if p.ProviderKeyAvailable("acme/other") {
		t.Fatal("an unlisted repo was available against a non-empty list")
	}
	if p.AllowsData("acme/other") {
		t.Fatal("sanity: AllowsData agreed, as it must for a non-empty list")
	}
}

func TestProviderKeyAvailableNeverChangesAllowsData(t *testing.T) {
	// The mutation-proof case: ProviderKeyAvailable must be purely additive.
	// An empty-list provider's AllowsData (what CheckProviderPolicy calls,
	// providers.go:334) must stay false for every repo, even though
	// ProviderKeyAvailable now returns true for the same provider and repo.
	p := ModelProvider{}
	if p.AllowsData("acme/app") {
		t.Fatal("AllowsData changed behavior for an empty list: CheckProviderPolicy would now accept an unlisted repo (design §3a Option B), which this plan does not implement")
	}
	if !p.ProviderKeyAvailable("acme/app") {
		t.Fatal("ProviderKeyAvailable did not pick up the empty-list case")
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test -count=1 -run TestProviderKeyAvailable ./internal/config/`
Expected: FAIL, `undefined: ProviderKeyAvailable` (the first two), and the third fails to build for the same reason.

- [ ] **Step 6: Write the `ProviderKeyAvailable` implementation**

```go
// providers.go, next to AllowsData
// ProviderKeyAvailable reports whether p's secret may be mounted or stored
// project-wide for repo: true for any repo when AllowDataTo is empty (owner
// ruling, 2026-10-13, docs/design/project-secrets.md §3a), else identical
// to AllowsData. This answers "is the key available", never "may a model
// be routed/pinned here" — CheckProviderPolicy keeps calling AllowsData
// directly (providers.go:334), unchanged.
func (p ModelProvider) ProviderKeyAvailable(repo string) bool {
	return len(p.AllowDataTo) == 0 || p.AllowsData(repo)
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go vet ./internal/config/... ./internal/localcfg/... && go test -count=1 ./internal/config/... ./internal/localcfg/...`
Expected: `ok`.

Also add, in `internal/localcfg/shared_test.go`, `TestMergeSharedNeverCarriesProjectSecrets`, the same shape as the two existing tests it sits next to (`shared_test.go:217,245`).

- [ ] **Step 8: Commit**

```bash
git add internal/config/project_secrets.go internal/config/project_secrets_test.go internal/config/providers.go internal/config/providers_test.go internal/localcfg/localcfg.go internal/localcfg/localcfg_test.go internal/localcfg/shared.go internal/localcfg/shared_test.go
git commit -m "project secrets task 1: config.ProjectSecret, its validation, the local config field, and ProviderKeyAvailable (owner ruling, design §3a)"
```

---

### Task 2: `gcp.ProjectSecretID` (domain-separated, nit); `rs.Secrets` stays unconditional (golden); `SecretMounts` mounts a no-list provider's key everywhere (P7)

**Files:**
- Edit: `internal/backend/gcp/names.go`, `names_test.go`
- Edit: `internal/infra/spec.go`, `spec_test.go`

**Interfaces:**
- Produces: `func ProjectSecretID(fugaroProject, logical string) string` — same `derive()` call as `SecretID` (`names.go:143-145`), same length/hash budget (`maxSecretID`, `secretHashHex`), domain-separated from it (a nit from review: hash `"project\x00"+fugaroProject+"\x00"+logical`, not `fugaroProject+"\x00"+logical`, so a Fugaro project name that happened to equal some repository's own slug could never make `ProjectSecretID`/`SecretID` collide on the same byte string).
- **No `SecretMounts` collision check for `project_secrets` is added (S1 — an earlier draft of this task proposed one; it was dead code, already covered unconditionally by `config.Validate`, see design §3).**
- **Changes (P7, owner ruling, design §3a): `SecretMounts`'s existing per-provider mount condition (`spec.go:534`, `if p := lc.Providers[pn]; p.AllowsData(repo) {`) becomes `if p := lc.Providers[pn]; p.ProviderKeyAvailable(repo) {`.** This is pre-existing, already-shipped code (predates this entire feature) being modified, not new code — flag it as such in the commit message. No other line of `SecretMounts` changes for this: the loop still appends exactly one `SecretMount{p.Secret, p.SecretEnv()}` per eligible provider, only *which* providers are eligible widens for a no-list provider.

- [ ] **Step 1: Write the failing test**

```go
// internal/backend/gcp/names_test.go
func TestProjectSecretIDDistinctFromRepoSecretID(t *testing.T) {
	a := ProjectSecretID("acme", "anthropic-api-key")
	b := SecretID("some-repo-slug-0123456789abcdef", "anthropic-api-key")
	if a == b {
		t.Fatal("a project secret ID collided with a repo secret ID")
	}
	if ProjectSecretID("acme", "anthropic-api-key") != ProjectSecretID("acme", "anthropic-api-key") {
		t.Fatal("ProjectSecretID is not pure")
	}
	if ProjectSecretID("acme", "x") == ProjectSecretID("acme2", "x") {
		t.Fatal("two different projects collided")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run TestProjectSecretID ./internal/backend/gcp/`
Expected: FAIL, `undefined: ProjectSecretID`.

- [ ] **Step 3: Write the implementation**

```go
// names.go, next to SecretID
// ProjectSecretID is the Secret Manager secret holding a project-wide
// logical secret (design docs/design/project-secrets.md §4), built like
// SecretID but keyed by the Fugaro project name instead of a repository
// slug. The hash input is domain-separated with a literal "project\x00"
// prefix (a nit from review) so a Fugaro project name that happened to
// equal some repository's own slug could never make this collide with
// SecretID's hash of the identical (name, logical) pair.
func ProjectSecretID(fugaroProject, logical string) string {
	return derive(sanitize("fugaro-project-"+fugaroProject+"-"+logical), "project\x00"+fugaroProject, logical, maxSecretID, secretHashHex)
}
```

- [ ] **Step 4: The golden test (P2)**

```go
// internal/infra/spec_test.go
func TestBuildRepoSpecNeverPrunesRepoSecrets(t *testing.T) {
	// Build a RepoSpec twice from the identical fugaro.yaml/local-config
	// fixture: once with lc.ProjectSecrets empty, once with an entry that
	// allow-lists this repository for one of the workflow's mounted names.
	// Assert rs.Secrets's key SET is identical both times (same keys, same
	// IDs) — this task adds no code that would change it yet (Task 3 adds
	// the resolution), so this test is the regression guard for every
	// later task: if a later change ever starts pruning rs.Secrets, this
	// is the test that catches it, written and passing before that change
	// exists.
}
```

- [ ] **Step 5: Write the `SecretMounts` implementation (the P7 condition change only — no collision check, S1)**

- [ ] **Step 6: Write the failing tests for the P7 mount-condition change**

```go
// internal/infra/secretmounts_test.go (the existing file SecretMounts's
// own tests already live in — S6, not spec_test.go)
func TestSecretMountsNoListProviderAvailableToAnyRepo(t *testing.T) {
	// cfg.Agent.Auth = "api-key"; lc.Providers has one provider with no
	// AllowDataTo at all. Call SecretMounts for a repo this provider has
	// never named. Assert the returned mounts include a SecretMount for
	// that provider's Secret — the opposite of today's behavior, and the
	// direct effect of design §3a's ruling.
}

func TestSecretMountsListedProviderStillRefusesUnlistedRepo(t *testing.T) {
	// Same as above, but the provider HAS an explicit, non-empty
	// AllowDataTo that does not name this repo. Assert SecretMounts's
	// result does NOT include a mount for this provider — confirms the
	// non-empty-list case is byte-for-byte unchanged by P7 (mutation-proof:
	// a mutation that deletes the "len == 0" branch of ProviderKeyAvailable
	// entirely, collapsing it to AllowsData, must fail
	// TestSecretMountsNoListProviderAvailableToAnyRepo above, not this one;
	// a mutation that makes ProviderKeyAvailable always true must fail
	// this test instead — the two together pin both halves of the
	// function, not just one).
}
```

Run: `go test -count=1 -run 'TestSecretMountsNoListProviderAvailableToAnyRepo|TestSecretMountsListedProviderStillRefusesUnlistedRepo' ./internal/infra/`
Expected: FAIL (the first: no such mount present today; the second should already pass before this task's P7 edit, confirming it is a true regression guard — run it once before Step 6's P7 change lands to check that, then again after).

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go vet ./internal/backend/gcp/... ./internal/infra/... && go test -count=1 ./internal/backend/gcp/... ./internal/infra/...`
Expected: `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal/backend/gcp/names.go internal/backend/gcp/names_test.go internal/infra/spec.go internal/infra/spec_test.go internal/infra/secretmounts_test.go
git commit -m "project secrets task 2: gcp.ProjectSecretID (domain-separated); pin that rs.Secrets stays unconditional; SecretMounts mounts a no-list provider's key to every repo (owner ruling, design §3a)"
```

---

### Task 3: `Readiness` resolves precedence (the live check), using `ProviderKeyAvailable` for a provider's own logical name (P7)

**Files:**
- Edit: `internal/infra/readiness.go`, `readiness_test.go`

**Interfaces (corrected after review B1/B2: the original draft's `ws.SecretIDs` rewrite never reached Terraform at all — `WorkflowSpec.SecretIDs` is `json:"-"`, internal/infra/spec.go:148 — and its "populated unconditionally wherever eligible" wording would, once wired to Terraform, have made the project copy win even when a repo override exists, inverting "repo > project." Both are fixed together below with two separate fields — one input, one output — not one field doing both jobs):**
- **Input, computed in `infra.Repo()`/`workflow()` (where the local config is in scope), not by `Readiness`:** `RepoSpec.ProjectSecretCandidates map[string]string` (`json:"-"`, never reaches tfvars) — one entry per logical name this repository is *eligible* to use project-wide, value `gcp.ProjectSecretID(fugaroProject, name)`. Eligibility is **not uniform** across name sources (P7): `project_secrets`/`claude_accounts`-derived names use `AllowedForProjectSecret`/`ClaudeAccountBySecret`+`AllowsData` (unchanged); **a provider's own `.Secret` name uses `ProviderKeyAvailable`, not `AllowsData`** — the same switch Task 2 makes in `SecretMounts`, kept consistent here. Being a candidate says nothing about resolution yet.
- **Output, computed by `Readiness` (`Readiness` has no `*localcfg.Config` parameter — `func Readiness(ctx context.Context, c *Clients, spec RepoSpec, ex Existing) (RepoSpec, []Missing, error)` — so it can only consult the candidates map above, never recompute eligibility itself):** `RepoSpec.ProjectSecrets map[string]string` (`json:"project_secrets"` — **this is the field that reaches tfvars as `repo.project_secrets`**, a new Terraform variable, Task 5). `Readiness`'s per-workflow loop (`readiness.go:89-137`) *and* its check-job branch (`readiness.go:139-151`, B4 — the check job mounts a subset of the same logical names and needs the identical treatment, since it was missed in the design's first draft): for each `logical` in `uniqueSecrets(ws)` (or `slices.Compact(slices.Sorted(maps.Values(out.Check.SecretEnv)))` for the check branch) that also has a `spec.ProjectSecretCandidates[logical]` entry, check `hasVersion(logical)` against the **repo** ID (today's `spec.Secrets[logical]`) first.
  - **Repo has a version → add nothing to `out.ProjectSecrets` for this `logical`.** This is the entire "repo overrides project" rule: the absence of an entry, not a flag or a comparison Terraform has to make. A dedicated test (`TestReadinessRepoVersionMeansNoProjectSecretsEntry`, below) asserts `out.ProjectSecrets` has **no key** for `logical` in this case — not merely that `ws.SecretIDs` or any other field looks like the repo won, since the earlier draft's bug was exactly this kind of indirect assertion passing while the real (then-nonexistent) wiring would have inverted precedence.
  - **Repo has no version → `out.ProjectSecrets[logical] = spec.ProjectSecretCandidates[logical]`.** If the project copy *also* has no version, `Missing{Kind: MissingSecret}` names `fugaro secrets set NAME --project-wide` instead of today's `--repo NAME`, and `logical` still gets no `ProjectSecrets` entry.
- **Only names present in `spec.ProjectSecretCandidates` for this repository are ever considered** — an unlisted repository's `ProjectSecretCandidates` simply has no entry for that provider's/name's logical name, so `ProjectSecrets` can never gain one either, regardless of what Terraform later does with the map.
- **P7a: an existing job never gains an unresolvable secret_env entry.** When `logical` has no version anywhere (repo nor project) *and* `exists` is true for the workflow it belongs to (`readiness.go:118-120`'s existing `current, exists := ex.Jobs[name]`), `Readiness` removes `logical`'s entry from `ws.SecretEnv` (and, if it was the only env var naming it, `ws.SecretIDs`) for this apply — it does **not** set `ws.DeployJob = false` (that field's only job is "does this job resource exist at all," `job.tf`'s `count = var.workflow.deploy_job ? 1 : 0`; flipping it for an existing job would delete the job outright). The `Missing` diagnostic is still reported, unconditionally, so `fugaro init --repo`'s own output tells the owner exactly what is missing and why nothing was mounted. A brand-new job (`exists` false) is unaffected: it already refuses to deploy at all until every mount resolves (`ws.DeployJob = len(reasons) == 0`, unchanged).

**Tests (every assertion is now on `out.ProjectSecrets`, the tfvars-reaching output map — never on `ws.SecretIDs`, which the corrected design no longer touches at all):**
```go
func TestReadinessRepoVersionMeansNoProjectSecretsEntry(t *testing.T) {
	// B2, the precedence-inversion finding: both the repo and the project
	// secret have an enabled version (fake Secret Manager), and the name is
	// an eligible ProjectSecretCandidates entry. Assert out.ProjectSecrets
	// has NO key for this logical name at all (not that some other field
	// "looks like" the repo won) and that no Missing is reported. This is
	// the test the earlier, inverted draft of this task would have failed:
	// a Terraform-side merge(repoMap, projectMap) always prefers the second
	// map's value when a key is present in both, so if ProjectSecrets were
	// (wrongly) populated for every eligible name regardless of the repo's
	// own version, this test would catch the project copy winning anyway.
}

func TestReadinessFallsBackToProjectWhenRepoHasNoVersion(t *testing.T) {
	// Repo secret has 0 versions, project secret has 1. Assert
	// out.ProjectSecrets[logical] equals spec.ProjectSecretCandidates[logical]
	// (the project ID) and no Missing is reported.
}

func TestReadinessOmitsProjectSecretForUnlistedRepo(t *testing.T) {
	// spec.ProjectSecretCandidates has no entry for this logical name (the
	// repo isn't allow-listed): out.ProjectSecrets gets no entry either,
	// and Missing (if any) still names --repo, never --project-wide.
}

func TestReadinessMissingNamesProjectWideFix(t *testing.T) {
	// Neither has a version, and the repo IS allow-listed (a candidate
	// exists): the Missing reason names `fugaro secrets set NAME
	// --project-wide`, not --repo, and out.ProjectSecrets still gets no
	// entry (there is nothing to resolve to).
}

func TestReadinessFallsBackOnceRepoVersionGone(t *testing.T) {
	// Simulates P3/design §5's "falls back on the next apply" rule: a
	// first Readiness call with the repo version present produces no
	// out.ProjectSecrets entry; a second call (simulating a later init
	// --repo) with that version now disabled produces one, pointing at the
	// project ID. Two calls, not one, since Readiness never re-runs within
	// a single apply.
}

func TestReadinessProviderNoListEligibleForEveryRepo(t *testing.T) {
	// P7: a provider with no AllowDataTo, a project-wide secret stored with
	// an enabled version, and the repo's OWN copy has no version. Assert
	// out.ProjectSecrets[logical] equals the PROJECT ID for a repository
	// that provider's config names nowhere at all — the no-list-means-any-
	// repo case, distinct from TestReadinessFallsBackToProjectWhenRepoHasNoVersion
	// above (which uses a project_secrets/claude_accounts-style name whose
	// eligibility is unaffected by P7). This requires the repository to
	// have been given a ProjectSecretCandidates entry via ProviderKeyAvailable
	// in infra.Repo() first (Task 2's fixture plumbing).
}

func TestReadinessProviderListedExcludesUnlistedRepo(t *testing.T) {
	// P7, the non-empty-list case: a provider WITH an explicit AllowDataTo
	// that does not name this repo. Assert spec.ProjectSecretCandidates has
	// no entry for that provider's logical name for this repo at all (not
	// merely that resolution skips it) — mirrors
	// TestReadinessOmitsProjectSecretForUnlistedRepo but specifically
	// exercises the ProviderKeyAvailable path, not the AllowedForProjectSecret
	// one, so a mutation that swaps one eligibility function for the other
	// anywhere in infra.Repo()'s provider branch is caught by one of these
	// two provider-specific tests.
}

func TestReadinessCheckJobResolvesProjectSecretsToo(t *testing.T) {
	// B4: the daily check job's secret_env (bitbucket-token/github-app-key,
	// typically) must go through the identical repo-wins-else-project
	// resolution as a workflow's, writing into the SAME out.ProjectSecrets
	// map (check.tf and sa.tf/job.tf all read the one merged Terraform
	// local, Task 5). Repo has no version, project does: assert
	// out.ProjectSecrets gains the entry from the check branch
	// (readiness.go's spec.Check != nil block), not only from a workflow's.
}

func TestReadinessOmitsUnresolvableMountFromExistingJob(t *testing.T) {
	// P7a/B5(a): ex.Jobs[name] reports the workflow's job as already
	// existing; a newly-eligible logical name (a no-list provider's
	// secret) has no version anywhere, repo or project. Assert: (1)
	// ws.SecretEnv (and SecretIDs) for that workflow has NO entry for this
	// logical name — the mount is omitted, not left dangling; (2)
	// ws.DeployJob is still true (the job's other properties keep
	// updating); (3) a MissingSecret Missing is still reported, Deployed:
	// true. This is the test that would catch a regression back to "the
	// job updates with a secret_key_ref pointing at a version-less
	// secret," the exact failure mode B5(a) describes.
}

func TestReadinessNewJobStillRefusesOnUnresolvableMount(t *testing.T) {
	// The companion case: ex.Jobs[name] reports no existing job at all.
	// Assert the existing, unchanged behavior holds: ws.DeployJob is
	// false while any mount (this one included) is unresolved — P7a does
	// not weaken the brand-new-job gate, only the existing-job one.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestReadinessPrefersRepoOverride|TestReadinessFallsBack|TestReadinessOmitsProjectSecret|TestReadinessMissingNamesProjectWideFix|TestReadinessProvider' ./internal/infra/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/infra/... && go test -count=1 ./internal/infra/...`
Expected: `ok` (the full package, to catch any regression in the existing `Readiness` tests from the edit).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/readiness.go internal/infra/readiness_test.go
git commit -m "project secrets task 3: Readiness resolves repo-vs-project precedence at apply time, ProviderKeyAvailable for a provider's own name (owner ruling, design §3a)"
```

---

### Task 4: `fugaro secrets set/ls --project-wide`; the installation root's container (provider validation per P7)

**Files:**
- Edit: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`
- Create: `deploy/terraform/gcp/modules/installation/secrets.tf`
- Edit: `deploy/terraform/gcp/modules/installation/variables.tf`, `deploy/terraform/gcp/roots/installation/{main,variables}.tf`
- Edit: `internal/infra/tfvars.go` (`InstallationSpec.ProjectSecrets`, populated by `Installation(lc, o)`), `internal/infra/imports.go` (a new `importProjectSecret` kind), `internal/infra/discover.go` (`DiscoverInstallation` gains the symmetric import step `DiscoverRepo` already has, S2) — **this is new scope the design's first draft missed (S2): without it, `fugaro secrets set NAME --project-wide` run before the installation root's first apply makes that apply fail with a 409, not a graceful adoption.**

**Interfaces (Go):**
- `secretsOptions` gains `projectWide bool`; `--project-wide` on `secrets set` and `secrets ls`.
- `secretsSet`: when `--project-wide`, skip `resolveSecretRepo` entirely (there is no `--repo` to resolve); validate `name` is one of: a `config.ReservedSecrets` key with a non-empty `project_secrets[name].AllowDataTo`; a claude-account's derived name whose own `allow_data_to` is non-empty; or **a provider's `.Secret` — accepted unconditionally, with no check on `allow_data_to` at all (P7, design §3a: an empty list is a valid, intentional "available to everyone" state for a provider, not a reason to refuse storage, unlike the other two kinds)**. The secret ID is `gcp.ProjectSecretID(env.lc.Name, name)`; labels are `{fugaro: managed, fugaro_project: env.lc.Name, fugaro_secret: name}` (`gcp.LabelProject`, already defined, `names.go:66`, reused here for the first time outside the runs bucket).
- **Changes (P7, third call site, B5(a)): `secretsSet`'s existing REPO-scope branch (`internal/cli/secrets.go:222`: `if !env.lc.Providers[p].AllowsData(r.repo) {`) becomes `if !env.lc.Providers[p].ProviderKeyAvailable(r.repo) {`.** Without this, `fugaro secrets set NAME --repo X` (no `--project-wide`) stays refused for *every* repository whenever the provider's list is empty — including the repository an operator is specifically trying to give its own override to (§5, "repo overrides project") — since an empty list makes `AllowsData` false for everyone. This is pre-existing, already-shipped code being modified, like Task 2's `SecretMounts` switch — flag it as such in the commit message.
- `secretsLs`: `--project-wide` lists by `{LabelProject: env.lc.Name}` instead of `{LabelRepo: r.label}`, and needs no `resolveSecretRepo` either. The existing (non-`--project-wide`) `ls` gains a `SCOPE` column: for each repo-owned entry, look up whether `config.AllowedForProjectSecret`-equivalent logic would also let this repo use a project copy of the same name, and print `project, repo override stored` when both exist; print a synthetic row (no version info beyond what a second `List` call under `{LabelProject}` returns) for a name the repo is allowed to use project-wide but has no repo-owned entry for at all, scope `project`.
- **`InstallationSpec.ProjectSecrets map[string]string` (`json:"project_secrets"`), populated by `Installation(lc, o)` (`internal/infra/tfvars.go:150`), for exactly these names (S2's second ask — stating precisely which, not leaving it implicit):**
  - every key of `lc.ProjectSecrets` whose own `AllowDataTo` is non-empty, value `gcp.ProjectSecretID(lc.Name, key)`;
  - every `lc.Providers[name].Secret`, **unconditionally** — regardless of that provider's own `AllowDataTo` (§3a: a provider's secret is eligible for project storage whether or not a list exists; the container must exist in Terraform either way for `--project-wide` storage to have somewhere to adopt into);
  - every `lc.ClaudeAccounts[name]` whose own `AllowDataTo` is non-empty, value `gcp.ProjectSecretID(lc.Name, config.ClaudeAccountSecret(name))`.
- **A new import kind, `importProjectSecret` (`internal/infra/imports.go`, next to `importSecret` at `imports.go:87`), template `module.installation.google_secret_manager_secret.project[{key}]`.** `DiscoverInstallation` (`internal/infra/discover.go:401`) gains a step mirroring `DiscoverRepo`'s existing one (`discover.go:540-570`, `for _, logical := range slices.Sorted(maps.Keys(spec.Secrets))`): for each `logical` in `spec.ProjectSecrets`, read it from GCP (`c.Secrets.Projects.Secrets.Get`), skip if absent, check it carries `{LabelManaged, LabelProject: spec.FugaroProject, LabelSecret: logical}` (`d.foreign(...)` otherwise, the existing ownership-mismatch path), and `d.add(importProjectSecret, spec.Region, logical, id)` if it matches — so a value stored with `--project-wide` before the installation root's first apply is adopted, not conflicted with.

**Interfaces (Terraform, installation module):**
```hcl
variable "project_secrets" {
  description = "Project-wide secret containers: logical name -> secret ID (gcp.ProjectSecretID), fugaro init creates/adopts each; values are never set by Terraform."
  type        = map(string)
  default     = {}
  nullable    = false
}
```
`secrets.tf` as drafted in the design §4 (`google_secret_manager_secret.project`, `for_each = var.project_secrets`, `prevent_destroy = true`, operator `secretVersionAdder`+`viewer` grants mirroring `modules/repo/secrets.tf`'s `operator_secrets` local).

**Tests:**
```go
func TestSecretsSetProjectWideRefusesEmptyAllowList(t *testing.T) { /* a RESERVED name (project_secrets has no entry, or an entry with no repos) or a claude-account name with an empty allow_data_to: refused, no network call made for the value (assert the fake prompt/reader was never invoked). Must NOT cover a provider's secret — that is the opposite case, next. */ }
func TestSecretsSetProjectWideAcceptsProviderSecret(t *testing.T) { /* openrouter-api-key, provider has a non-empty allow_data_to: stored under gcp.ProjectSecretID */ }
func TestSecretsSetProjectWideAcceptsProviderSecretWithNoList(t *testing.T) { /* P7, design §3a: openrouter-api-key, provider has NO allow_data_to at all: accepted (not refused) — the one case that behaves oppositely to TestSecretsSetProjectWideRefusesEmptyAllowList above for a non-provider name, so both tests exist specifically to pin that the two kinds of name diverge here */ }
func TestSecretsSetRepoScopeAcceptsProviderSecretWithNoList(t *testing.T) { /* B5(a), the third call site: fugaro secrets set openrouter-api-key --repo acme/app (NOT --project-wide), provider has no allow_data_to at all: accepted, not refused -- today's code (AllowsData) would refuse every repo here, which this test would have caught */ }
func TestSecretsLsAddsScopeColumn(t *testing.T) { /* fixture with both a repo-owned and project-shared entry for one name: two distinct SCOPE values appear */ }
func TestSecretsLsProjectWideNeverPrintsValue(t *testing.T) { /* fake Secret Manager's Access never called, same as every existing *NeverPrintsValue test */ }
func TestDiscoverInstallationImportsPreExistingProjectSecret(t *testing.T) { /* S2: a project secret already exists in the fake GCP (created by a --project-wide set run before the installation root's first apply), correctly labelled; assert DiscoverInstallation's Imports includes an importProjectSecret entry for it, so the subsequent plan adopts rather than conflicts with it */ }
func TestDiscoverInstallationRefusesForeignProjectSecret(t *testing.T) { /* S2, the mirror of DiscoverRepo's existing foreign-secret path: a secret exists at the computed ID but its labels don't match -- refused as foreign, never silently adopted or overwritten */ }
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestSecretsSetProjectWide|TestSecretsLsAddsScope|TestSecretsLsProjectWide' ./internal/cli/ && go test -count=1 -run 'TestDiscoverInstallationImportsPreExistingProjectSecret|TestDiscoverInstallationRefusesForeignProjectSecret' ./internal/infra/`
Expected: FAIL.

- [ ] **Step 3: Write the Go implementation** (`internal/cli/secrets.go`, `internal/infra/tfvars.go`'s `InstallationSpec.ProjectSecrets` population, `internal/infra/imports.go`'s new kind, `internal/infra/discover.go`'s new import step)
- [ ] **Step 4: Write the Terraform (installation module)**, then validate it offline:

Run: `terraform -chdir=deploy/terraform/gcp/modules/installation fmt -check && terraform -chdir=deploy/terraform/gcp/modules/installation validate` (against a local init with no backend/credentials — the existing pattern this repository's Terraform tests already use; the implementer checks `deploy/terraform/gcp/roots/installation/tests/` for the harness in place).

- [ ] **Step 5: Run the Go tests to verify they pass**

Run: `go vet ./internal/cli/... ./internal/infra/... && go test -count=1 -run 'TestSecretsSetProjectWide|TestSecretsLsAddsScope|TestSecretsLsProjectWide' ./internal/cli/ && go test -count=1 -run 'TestDiscoverInstallation' ./internal/infra/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/secrets.go internal/cli/secrets_test.go internal/infra/tfvars.go internal/infra/imports.go internal/infra/discover.go internal/infra/*_test.go deploy/terraform/gcp/modules/installation/secrets.tf deploy/terraform/gcp/modules/installation/variables.tf deploy/terraform/gcp/roots/installation/main.tf deploy/terraform/gcp/roots/installation/variables.tf
git commit -m "project secrets task 4: fugaro secrets set/ls --project-wide; the installation root's container and its import/adoption step (S2); a provider's empty allow_data_to is accepted, not refused (owner ruling, design §3a)"
```

---

### Task 5: `local.secrets`, the tfvars wire, and `build.tf`/`check.tf`'s grants rewired through it (P4 — flagged for opus review; B1/B4)

**This task is security- and Terraform-sensitive: a build service account's `secretAccessor` grant (and the daily check job's own secret reference) is being re-pointed from a resource this module creates to a value that may come from a different Terraform root entirely. Route this task's review through opus, not the default reviewer, before merge.**

**Files:**
- Edit: `deploy/terraform/gcp/modules/repo/build.tf`, `workflows.tf`, `check.tf`, `variables.tf`
- Edit: `internal/infra/tfvars.go` (or wherever `RepoSpec`'s JSON tags live, `internal/infra/spec.go`) — the Go side of the tfvars wire
- Edit: `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `deploy/terraform/gcp/roots/repo/tests/testdata/*.tfvars.json` (a new golden fixture with a resolved `project_secrets` entry — regenerate with `internal/infra/spec_test.go`'s `-update` flag)

**Interfaces:**
- `RepoSpec.ProjectSecrets map[string]string` (`json:"project_secrets"`, Task 3's output field) is the **only** new tfvars key this task wires through — it is Go-computed entirely by `Readiness`, never by this module.
- `workflows.tf` currently computes, inline, `secrets = { for k, s in google_secret_manager_secret.this : k => s.secret_id }` (`workflows.tf:14`) and passes it to each `module "workflow"` call. Hoist this into a `locals` block in the repo module: `locals { secrets = merge({ for k, s in google_secret_manager_secret.this : k => s.secret_id }, var.repo.project_secrets) }`, where `var.repo.project_secrets map(string)` is a new `repo` object field (logical name → an **already-existing** project secret ID, fed straight from `RepoSpec.ProjectSecrets` — never a name this module creates a resource for).
- `build.tf:17` changes from `google_secret_manager_secret.this[each.value].secret_id` to `local.secrets[each.value]`.
- **`check.tf:71` changes identically**, from `google_secret_manager_secret.this[env.value].secret_id` to `local.secrets[env.value]` (B4 — missed in the design's first draft; the check job is a third site, not covered by "build.tf is the one place" as that draft claimed).
- `variables.tf`'s existing precondition (`variables.tf:150-155`, "every secret that build_secrets... name must be one of repo.secrets") is widened to accept a name that is a key of either `repo.secrets` or `repo.project_secrets` — not narrowed: every name P2 already guarantees stays a key of `repo.secrets` too (P2: `rs.Secrets` never prunes), so in practice this precondition's existing form already holds; the widening is defensive, for a future change that might relax P2.

**Tests (corrected per S6/B1: pure Go cannot observe a Terraform `local`'s resolved value, so this cannot be an `internal/infra` unit test — it must run through the real Terraform-test framework, the same one Task 6 drives):**
```hcl
# deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl, a new run block added
# to the existing file (reusing its mock_provider and testdata pattern)
run "build_and_check_use_the_resolved_secret" {
  # Using a golden tfvars fixture whose repo.project_secrets has one entry
  # (a logical name both a workflow's secret_env, build_secrets and the
  # check job's secret_env all name — the git credential is the natural
  # choice, since it is in all three sets already). Assert, from the plan's
  # own resource attributes (not Go): the workflow module's job mount, the
  # build account's grant, and the check job's secret_key_ref all resolve
  # to the IDENTICAL secret ID string (the project one, matching
  # repo.project_secrets, not google_secret_manager_secret.this's repo ID).
  # A divergence here means one of the three paths is still reading a
  # stale, repo-only value.
}
```

- [ ] **Step 1: Write the failing test** (add the `run` block above to `repo.tftest.hcl`; it needs the new golden fixture with a resolved `project_secrets` entry first — write that as part of this step, via `spec_test.go`'s `-update` flag, reviewing the diff)
- [ ] **Step 2: Run the test to verify it fails**

Run: `terraform -chdir=deploy/terraform/gcp/roots/repo init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/repo test -no-color`
Expected: FAIL — today, `build.tf`/`check.tf` read `google_secret_manager_secret.this` directly, so they'd resolve the repo ID, not the project one the new fixture's `secret_env` expects; `workflows.tf` has no `var.repo.project_secrets` input to even accept yet, so the init step itself may fail first, confirming the field doesn't exist.

- [ ] **Step 3: Write the Terraform and the Go tfvars-shape change** (the `RepoSpec.ProjectSecrets` JSON tag, Task 3's output field, threaded into the tfvars this test's golden fixture encodes)
- [ ] **Step 4: Run the test to verify it passes, then the full Terraform validate**

Run: `terraform -chdir=deploy/terraform/gcp/roots/repo test -no-color && go vet ./internal/infra/... && go test -count=1 ./internal/infra/... && terraform -chdir=deploy/terraform/gcp/modules/repo fmt -check && terraform -chdir=deploy/terraform/gcp/modules/repo validate`
Expected: `ok` / no diff / valid.

- [ ] **Step 5: Commit**

```bash
git add deploy/terraform/gcp/modules/repo/build.tf deploy/terraform/gcp/modules/repo/workflows.tf deploy/terraform/gcp/modules/repo/check.tf deploy/terraform/gcp/modules/repo/variables.tf internal/infra/tfvars.go internal/infra/spec.go deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl deploy/terraform/gcp/roots/repo/tests/testdata/
git commit -m "project secrets task 5: local.secrets; build.tf AND check.tf (B4) both rewired through it (opus-reviewed)"
```

---

### Task 6: plan-level fixtures (no secret deletes, but real IAM-member replacements, on a scope-move plan — B3)

**Files:**
- Edit: `deploy/terraform/gcp/roots/installation/tests/` (the harness's own `*.tftest.hcl` file, following the existing pattern there) and `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl` (this repository's real Terraform-test framework for the repo root — not a Go-level fixture: Go cannot observe a Terraform plan's resource actions, only `terraform plan -json` through this framework can, which is exactly why B3's scope-move claim needed a live plan to catch, not a unit test).

**Tests (named for what is actually true, not "zero destroys" — corrected per B3):**
```hcl
# deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl
run "project_secrets_is_create_only" {
  # A fresh var.project_secrets entry plans exactly 1 create (the secret)
  # plus its operator grants, 0 updates to any unrelated installation
  # resource, 0 destroys, 0 replacements — the installation root's own
  # first apply touches nothing that already exists.
}
```
```hcl
# deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl
run "scope_move_replaces_iam_members_never_deletes_secrets" {
  # The scenario in design §6 step 4: a name moves from repo-only to
  # project-shared (repo has no version, project does). Assert:
  #   - 0 "delete" actions on any google_secret_manager_secret.this[*]
  #     address (the secret container itself is never destroyed);
  #   - EXACTLY the expected google_secret_manager_secret_iam_member.job[*]
  #     (one per workflow/account variant that mounts the name) and, if the
  #     name is a BuildSecret, google_secret_manager_secret_iam_member.build[*]
  #     addresses show a "replace" action (["delete","create"]) — asserting
  #     this is present, not merely permitted, is the point: a plan that
  #     silently keeps the OLD secret_id (a bug) would show no replacement
  #     at all and must also fail this test;
  #   - the plan, run through tf.Guard with no --allow-delete, is refused
  #     (asserts the guard really does fire on these addresses, not just
  #     that the design document says it should);
  #   - the plan, run through tf.Guard WITH every expected address listed
  #     in --allow-delete, succeeds;
  #   - the workflow's (and, if applicable, the check job's) Cloud Run job
  #     resource itself shows an "update" action, never "replace" or
  #     "delete" (new revision, same resource).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run (the installation root already has a Go-side driver that writes a fresh `tests/vars.tftest.hcl` and shells out to the real `terraform` binary, `internal/infra/installation_terraform_test.go:59-69`'s pattern — reuse it, do not invent a second one): `go test -count=1 -run TestInstallationVarsPlan ./internal/infra/`. The repo root's `repo.tftest.hcl` is a checked-in file (not Go-generated) reading golden tfvars from `deploy/terraform/gcp/roots/repo/tests/testdata/` (the same fixtures `internal/infra/spec_test.go`'s `-update` flag maintains, `spec_test.go:24,28`); run it directly against the real `terraform` binary with no credentials: `terraform -chdir=deploy/terraform/gcp/roots/repo init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/repo test -no-color` (the exact flags `installation_terraform_test.go` already uses for its own `terraform init`/`terraform test` calls).
Expected: both FAIL (no `replace`/`delete` assertions exist yet on the fixtures, which today have no project-shared name at all).

- [ ] **Step 3: Make them pass** (this task should need no new production code if Tasks 2-5 are correct; it exists to prove the golden claim end to end, across both roots, not just within `internal/infra`). If `repo.tftest.hcl`'s fixtures need a new golden tfvars case with a `project_secrets` entry resolved, regenerate it with `internal/infra/spec_test.go`'s own `-update` flag rather than hand-editing the JSON: `go test -run TestBuildRepoSpec -update ./internal/infra/` (the exact invocation the file's own flag comment documents), then review the diff before committing it.
- [ ] **Step 4: Run both again to verify they pass**

Run the same two commands as Step 2.
Expected: `ok` (installation) / Terraform's own `Success!` summary (repo root), with the replace actions and the `tf.Guard` refusal/success asserted, not merely "0 destroys."

- [ ] **Step 5: Commit**

```bash
git commit -m "project secrets task 6: plan-level fixtures proving no secret deletes, but real IAM-member replacements that tf.Guard catches and --allow-delete clears (B3)"
```

---

### Task 7: `fugaro doctor`'s two new checks

**Files:**
- Edit: `internal/cli/doctor.go`, `internal/cli/doctor_test.go`

**Interfaces:**
- `project-secret-stored`: for every name `config.ReservedSecrets`/a provider's/an account's own list allow-lists this repository for, if neither the repo's own secret nor the project's has an enabled version, red, naming both candidate fix commands.
- `project-secret-scope-stale`: the repository's **last deployed** job's `secret_env` (read the same way `doctor`'s existing image-config checks already read a deployed job's spec) resolves a different scope than `Readiness` would compute right now — a warning (fails only `--strict`), mirroring `doctor`'s existing project-layer shape, "flags a run that launched without it or on an older generation" (`internal/cli/doctor.go:148-149`).

**Tests:**
```go
func TestDoctorProjectSecretChecksReadNoValue(t *testing.T) { /* fake Secret Manager's Access never called */ }
func TestDoctorFlagsUnstoredProjectSecret(t *testing.T) { /* ... */ }
func TestDoctorFlagsStaleScope(t *testing.T) { /* deployed job still mounts the repo ID; Readiness would now resolve the project ID (repo version was deleted since the last apply): warning, not red */ }
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestDoctorProjectSecret|TestDoctorFlagsUnstoredProjectSecret|TestDoctorFlagsStaleScope' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -count=1 -run 'TestDoctor' ./internal/cli/`
Expected: `ok` (the full `TestDoctor*` set, to catch any regression from the edit).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/doctor.go internal/cli/doctor_test.go
git commit -m "project secrets task 7: fugaro doctor's two checks for project-wide secrets"
```

---

### Task 8: Docs, cross-references, Check 40, the release checklist bullet

**Files:**
- Edit: `docs/design/v1.md` (one cross-reference sentence, §5.4 local config area, no rewrite)
- Edit: `docs/multi-model.md` (a one-paragraph note that a provider's key may now also be stored `--project-wide`, with a link — no change to §1-§6's own content)
- Edit: `docs/gcp-live-checklist.md`: add Check 40 (40a-40g, design §8 — 40f and 40g are the owner's follow-up ruling on provider keys, design §3a) as a new row and a `## Check 40: ...` section, in the file's existing style; update the "Numbering ruling" paragraph (`gcp-live-checklist.md:183-199`) to name 40 as taken by this design
- Edit: `docs/release.md` — a new bullet in the release checklist, in the same family and immediately alongside the existing `gcpProjectFieldSince`/`recipesSince`/`layeredSince`/`namedTokensSince` bullets (`docs/release.md:55-58`, design §7): confirm the release note for the shipping version names the owner's migration sequence (design §6), and that an installation applying the `installation` root for the first time after this feature ships sees the new `secrets.tf` resources as **creates only** — never an update to an unrelated resource (a regression elsewhere in the module would show as an unexpected diff there). Unlike the other bullets in that list, this one names no `*Since` Go constant (design §7, P6: this feature adds none) — it is a plan-shape check, not a constant-equality one, and its wording should say so rather than mimic the others' "verify `X` equals Y" phrasing where it would be inaccurate.
- New/extended test in whichever file already asserts the checklist's numbering/doc shape (the implementer greps for a test covering the "Numbering ruling" claims, if one exists; otherwise this task adds none beyond the existing docs-lint suite)

- [ ] **Step 1: Write the docs edits**
- [ ] **Step 2: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`.

- [ ] **Step 3: Commit**

```bash
git add docs/design/v1.md docs/multi-model.md docs/gcp-live-checklist.md docs/release.md
git commit -m "project secrets task 8: docs cross-references, Check 40, the release checklist bullet"
```

---

### Task 9: Full suite

- [ ] **Step 1: Full focused suite**

Run (foreground, split by package if any call would exceed 9 minutes):
```bash
go vet ./...
gofmt -l . # expect no output
go test -race -count=1 ./internal/config/... ./internal/localcfg/... ./internal/backend/gcp/... ./internal/infra/... ./internal/cli/...
go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m
terraform -chdir=deploy/terraform/gcp/modules/installation fmt -check
terraform -chdir=deploy/terraform/gcp/modules/repo fmt -check
```
Expected: `ok` everywhere, no `gofmt`/`terraform fmt` output.

- [ ] **Step 2: Commit**

```bash
git commit -m "project secrets task 9: full suite"
```

---

### Task 10: Owner-run live check (not part of the PR; `fugaro`/sandbox installations only, never `belong`)

This task is never run by an implementer or by CI. Paste the `FACT:` lines into the PR that eventually implements Tasks 1-9 and into `docs/gcp-live-checklist.md` itself, under Check 40.

- [ ] **40a.** Store a project secret with `--project-wide`, confirm (via `gcloud run jobs describe`/`jobs.get`, never by reading the value) that an opted-in repository's job's `secret_key_ref` names the project's secret ID.
- [ ] **40b.** For a `project_secrets`/`claude_accounts`-backed name (reserved secrets, named accounts): confirm a repository outside the allow-list is refused at `--project-wide` with a message naming the missing entry, and that its deployed job carries no such env var at all.
- [ ] **40c.** Confirm precedence: a repository with its own stored version keeps mounting it even though the project copy exists and lists it; disable the repo version's last enabled version, re-run `fugaro init --repo`, confirm the job now mounts the project copy.
- [ ] **40d.** Confirm the data-policy gate still refuses a provider model for a repository whose `allow_data_to` lists other repositories but not this one, with the provider's key stored `--project-wide` (the non-empty-list case; the empty/absent-list case is 40f's own check, Option A, design §3a — the gate does not open on an absent list in this plan).
- [ ] **40e.** Confirm no value appears in any log, `fugaro diagnose`, `secrets ls`/`ls --project-wide` output, or a `terraform plan`/`state show` of either root.
- [ ] **40f (owner ruling, design §3a).** Configure a provider with **no** `allow_data_to` at all; confirm `fugaro secrets set <provider-secret> --project-wide` succeeds (not refused); confirm, via `jobs.describe`, that an `agent.auth: api-key` repository never named by this provider anywhere still mounts the key; confirm that `fugaro validate` still refuses every repository's pin to it (the data-policy gate stays closed, Option A).
- [ ] **40g.** Configure a provider **with** an explicit, non-empty `allow_data_to`; confirm an unlisted repository's job does **not** mount the key at all (the non-empty-list case is unaffected by 40f).
