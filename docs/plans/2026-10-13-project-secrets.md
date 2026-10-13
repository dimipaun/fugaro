# Project-level secrets: implementation plan

**Goal:** let `anthropic-api-key`, `bitbucket-token`, `github-app-key`, `claude-oauth-token` (default), a model provider's key, or a named Claude account's token be stored **once per project** and mounted by every repository the owner allows, while a repository's own stored value still overrides it. No code in this plan has been written or run; this is a design-to-code plan, the same status as `docs/plans/2026-10-09-named-oauth-tokens.md` before its Task 1 started. **This PR (the one that adds this plan and its design doc) is docs-only: no file under this plan's "Files" lists is touched by it.** Tasks 1 onward are future work, gated on the owner approving the design.

**Spec:** [docs/design/project-secrets.md](../design/project-secrets.md). Its decisions (§3 opt-in model, §4 storage, §5 mounting/resolution, §6 migration) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable.

**Depends on:** release 0.7.x on `main` (the base-image hard cut already merged). No functional dependency on any other in-flight design.

**Checked before this plan was written:** every fact the design doc cites was read from the code at this branch's base — `internal/config/providers.go`, `internal/config/claude_accounts.go`, `internal/config/config.go`, `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/backend/gcp/names.go`, `internal/backend/gcp/secrets.go`, `internal/cli/secrets.go`, `internal/cli/initsecrets.go`, `deploy/terraform/gcp/modules/{installation,repo,workflow}/*.tf`, `internal/infra/tf/guard.go`, `docs/design/layered-config.md`, `docs/project-layer.md`, `docs/multi-model.md`, `SECURITY.md`, `docs/gcp-live-checklist.md`. If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the same rule `docs/plans/2026-10-08-upgrade.md` and `named-oauth-tokens.md`'s plan use).

## Decisions (veto any before execution starts)

- **P1. `project_secrets` is keyed by the exact logical secret name and may hold only a `config.ReservedSecrets` key.** No separate allow-list for a provider's or a named Claude account's secret — their own `allow_data_to` governs project-sharing too (design §3). `ValidateProjectSecrets` refuses a `project_secrets` key that is a provider's `.Secret` or any `config.ClaudeAccountSecret(name)`, pointing at that object's own `allow_data_to` instead. *Veto alternative:* one allow-list block covering every kind of secret uniformly (including providers/accounts), accepting the "two lists that must agree" risk the design rejected.
- **P2. `rs.Secrets` (the repo module's own containers) is never pruned when a name becomes project-shared.** The repo-owned container keeps existing, inert if unused, in every repository's Terraform state (design §5, §6). This is the one choice that keeps `prevent_destroy` from ever blocking an apply because of a scope change. *Veto alternative:* drop a project-shared name from `rs.Secrets` once no repo-level version exists, which would require `terraform state rm`-style handling before any apply could proceed — rejected as a strictly worse migration story for no operational gain.
- **P3. Precedence (repo overrides project) is resolved once, in `internal/infra.Readiness`, at `fugaro init --repo` apply time — never at run launch.** Cloud Run Jobs mount secrets at the job, not the execution (the same fact `named-oauth-tokens.md` §1 establishes); re-resolving at every run would need a live Secret Manager read on every launch, which nothing in this codebase does today. *Veto alternative:* resolve in the runner at boot (a live call each run); rejected as a new, unprecedented live dependency on the hot path, and because it would not change which secret a *job's IAM* can reach — only the job's own grant does that, and that is fixed at apply time regardless.
- **P4. `build.tf`'s build-account grant is rewired to go through the same Go-computed map `sa.tf` already uses**, instead of indexing `google_secret_manager_secret.this` directly (design §5). This is the one genuine Terraform-shape change this feature needs; everything else is new Go computation over an unmodified module shape. **Flagged for opus review (security- and Terraform-sensitive): Task 5.**
- **P5. No command deletes or migrates a now-redundant repo-owned secret.** Decommissioning is a manual, owner-paced `terraform state rm`/delete, outside this plan (design §6 step 5, §9 non-goals).
- **P6. No `*Since` version-gate constant is added.** This feature changes which secret ID a job's existing env var is mounted from, never a new field the runner's `task.Parse` must know about (design §7). A repository needs `fugaro init --repo` re-applied before project-sharing takes effect for it, exactly like any other `infra` change — no new constant, no new skew test family.

## Global Constraints

From the design:
- `project_secrets` (and every existing `providers`/`claude_accounts` key reused for this) is local-config, operator-only; `localcfg.Config.Shared()` strips it (design §7) — a test pins this the same way `TestMergeSharedNeverCarriesProviders`/`...ClaudeAccounts` already does.
- No secret value is ever read back, echoed, or logged, by any new code path (`fugaro secrets set --project-wide`, `ls --project-wide`, `doctor`'s two new checks all use `List`/metadata only, never `Access`).
- No `fugaro.yaml` key, JSON Schema entry, or project-layer scope-table row is touched: this feature is local-config and Terraform/Go only.
- `rs.Secrets`'s `for_each` never shrinks because of this feature (P2) — a golden test diffs it.
- No live GCP call in any automated test; Check 40's items (design §8) are the owner's own task, run once, sandbox/`fugaro` installations only, never `belong`.

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the foreground, `-count=1`, `-race` where the package's existing tests use it.
- Fakes only in tests: `gcpfake`, no real Secret Manager, no real Terraform apply.
- Docs must match behavior: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout (this plan adds no `fugaro.yaml` key, so no schema/docs-lint edit is expected — a task that finds otherwise stops and reports rather than loosening the lint).

## Review Focus (mutation-proof notes)

1. **A repository outside an allow-list gets a project secret's variable anyway**, or `secrets set --project-wide` accepts a name with an empty allow-list. Pinned by `TestProjectSecretAllowedRepos` (Task 1), `TestReadinessOmitsProjectSecretForUnlistedRepo` (Task 3), `TestSecretsSetProjectWideRefusesEmptyAllowList` (Task 4).
2. **A repo-level override stops winning even though it still has a version**, or a project copy is chosen while the repo copy is live. Pinned by `TestReadinessPrefersRepoOverrideWhenPresent` and `TestReadinessFallsBackOnceRepoVersionGone` (Task 3) — both assert the *resolved secret ID*, not merely that the job deploys.
3. **A scope change plans a delete of an existing repo-owned secret.** Pinned by `TestBuildRepoSpecNeverPrunesRepoSecrets` (Task 2, a golden diff of `rs.Secrets`'s key set before/after a name becomes project-shared) and `TestInstallationRootPlanHasNoRepoSecretDeletes` if a Terraform-plan-level test harness exists for the installation root (Task 6; otherwise a Go-level assertion suffices and the task says so).
4. **The build account's grant drifts from the job account's resolved secret** (P4): `TestBuildAndJobGrantSameResolvedSecret` (Task 5) asserts both `var.secrets`-consuming paths resolve the identical ID for the identical logical name, for every fixture.
5. **A value leaks through `ls --project-wide`, `doctor`, or a plan/state print.** Pinned by `TestSecretsLsProjectWideNeverPrintsValue`, `TestDoctorProjectSecretChecksReadNoValue` (Task 4/7), both asserting the fake Secret Manager's `Access` is never called.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/project_secrets.go` (new), `project_secrets_test.go` (new) | `ProjectSecret`, `ValidateProjectSecrets`, `AllowedProjectSecrets`-style helper reusing `ProviderBySecret`/`ClaudeAccountBySecret` for collision checks | 1 |
| `internal/localcfg/localcfg.go`, `localcfg_test.go`, `internal/localcfg/shared.go`, `shared_test.go` | `Config.ProjectSecrets` field, `Shared()` strips it, `Validate` wiring | 1 |
| `internal/backend/gcp/names.go`, `names_test.go` | `ProjectSecretID` | 2 |
| `internal/infra/spec.go`, `spec_test.go` | `rs.Secrets` unchanged (golden), no change needed to add project IDs yet (Task 3 does the resolution) | 2 |
| `internal/infra/readiness.go`, `readiness_test.go` | the live precedence check; rewrites a workflow's resolved `SecretIDs[name]` | 3 |
| `internal/cli/secrets.go`, `secrets_test.go` | `--project-wide` flag, validation branch, `ls` SCOPE column, `ls --project-wide` | 4 |
| `deploy/terraform/gcp/modules/installation/secrets.tf` (new), `variables.tf`, roots/installation | `google_secret_manager_secret.project`, operator grants, `var.project_secrets` input | 4 |
| `deploy/terraform/gcp/modules/repo/build.tf`, `workflows.tf`, `variables.tf` | **P4, opus review:** `local.secrets`, `build.tf`'s grant rewired through it | 5 |
| `internal/infra/installation_terraform_test.go` or equivalent, `deploy/terraform/gcp/roots/installation/tests/` | plan-level fixtures for the new resources, 0 destroys on an unrelated-change plan | 6 |
| `internal/cli/doctor.go`, `doctor_test.go` | two new checks (design §5) | 7 |
| `docs/design/v1.md`, `docs/gcp-live-checklist.md`, `internal/cli/docs_*_test.go` | cross-references, Check 40 | 8 |
| none | full suite | 9 |
| `docs/gcp-live-checklist.md` (results pasted in) | the owner's own run, Check 40a-e | 10 |

## PR group

One PR, branch `project-secrets`, Tasks 1 to 9 in order for the merge; Task 10 is the owner's own run, not blocking (the same relationship `named-oauth-tokens.md`'s Task 11 has to its own PR).

Parallelizable: **Tasks 1 and 2 are independent and may be implemented together.** Task 3 needs 1 and 2. Task 4 needs 1, 2 and the installation Terraform half of itself (its Go and Terraform halves can split further if useful). Task 5 needs 2 (and is reviewed separately from the rest, per P4). Task 6 needs 4 and 5. Task 7 needs 3. Task 8 needs 1 through 7 settled. Task 9 is last.

---

### Task 1: `config.ProjectSecret`, its validation, and the local config field

**Files:**
- Create: `internal/config/project_secrets.go`
- Test: `internal/config/project_secrets_test.go`
- Edit: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`, `internal/localcfg/shared.go`, `internal/localcfg/shared_test.go`

**Interfaces:**
```go
// package config
type ProjectSecret struct{ AllowDataTo []string }
func (p ProjectSecret) AllowsData(repo string) bool
func AllowedForProjectSecret(secrets map[string]ProjectSecret, name, repo string) bool
func ValidateProjectSecrets(secrets map[string]ProjectSecret, providers map[string]ModelProvider, accounts map[string]ClaudeAccount) []Problem
```
`ValidateProjectSecrets` refuses (P1): a key not in `config.ReservedSecrets`; a key equal to any `providers[*].Secret` or `ClaudeAccountSecret(name)` for a configured account (message names the object to edit instead); a malformed `allow_data_to` entry (reusing `repoSlugRE`/`hasDotSegment`, exactly as `ValidateClaudeAccounts` does, `claude_accounts.go:136-140`).

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
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"db-password": {AllowDataTo: []string{"acme/app"}}}, nil, nil)
	if len(ps) != 1 {
		t.Fatalf("ValidateProjectSecrets = %+v, want one problem", ps)
	}
}

func TestValidateProjectSecretsRefusesProviderCollision(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}}}
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"openrouter-api-key": {AllowDataTo: []string{"acme/app"}}}, providers, nil)
	if len(ps) != 1 {
		t.Fatalf("a provider-secret collision was not refused: %+v", ps)
	}
}

func TestValidateProjectSecretsRefusesClaudeAccountCollision(t *testing.T) {
	accounts := map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"claude-oauth-token-acme-personal": {AllowDataTo: []string{"acme/app"}}}, nil, accounts)
	if len(ps) != 1 {
		t.Fatalf("a claude-account collision was not refused: %+v", ps)
	}
}

func TestValidateProjectSecretsAcceptsReservedName(t *testing.T) {
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"acme/app"}}}, nil, nil); len(ps) != 0 {
		t.Fatalf("a valid block was refused: %+v", ps)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestProjectSecret|TestValidateProjectSecrets' ./internal/config/`
Expected: FAIL, build failed: `undefined: ProjectSecret`.

- [ ] **Step 3: Write the implementation**

`internal/config/project_secrets.go`, mirroring `claude_accounts.go`'s shape exactly (`AllowsData` case-folded via `strings.EqualFold`, `sortedKeys`, `pluginwire.Printable` on every echoed value). `ValidateProjectSecrets` iterates `sortedKeys(secrets)`; for each name checks `_, reserved := ReservedSecrets[name]` first, then `ProviderBySecret(providers, name)` and `ClaudeAccountBySecret(accounts, name)` for collisions.

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
Wire `config.ValidateProjectSecrets(c.ProjectSecrets, c.Providers, c.ClaudeAccounts)` next to the existing `ValidateModelProviders`/`ValidateClaudeAccounts` calls.

Edit `internal/localcfg/shared.go`: add `s.ProjectSecrets = nil` next to `s.Providers = nil`/`s.ClaudeAccounts = nil` (`shared.go:37-38`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/config/... ./internal/localcfg/... && go test -count=1 ./internal/config/... ./internal/localcfg/...`
Expected: `ok`.

Also add, in `internal/localcfg/shared_test.go`, `TestMergeSharedNeverCarriesProjectSecrets`, the same shape as the two existing tests it sits next to (`shared_test.go:217,245`).

- [ ] **Step 5: Commit**

```bash
git add internal/config/project_secrets.go internal/config/project_secrets_test.go internal/localcfg/localcfg.go internal/localcfg/localcfg_test.go internal/localcfg/shared.go internal/localcfg/shared_test.go
git commit -m "project secrets task 1: config.ProjectSecret, its validation and the local config field"
```

---

### Task 2: `gcp.ProjectSecretID`; `rs.Secrets` stays unconditional (golden)

**Files:**
- Edit: `internal/backend/gcp/names.go`, `names_test.go`
- Edit: `internal/infra/spec_test.go` (golden test only; no production-code change expected in `spec.go` for this task — see note)

**Interfaces:**
- Produces: `func ProjectSecretID(fugaroProject, logical string) string` — same `derive()` call as `SecretID` (`names.go:143-145`), same length/hash budget (`maxSecretID`, `secretHashHex`).

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
// slug, so the two ID spaces can never collide.
func ProjectSecretID(fugaroProject, logical string) string {
	return derive(sanitize("fugaro-project-"+fugaroProject+"-"+logical), fugaroProject, logical, maxSecretID, secretHashHex)
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

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./internal/backend/gcp/... ./internal/infra/... && go test -count=1 ./internal/backend/gcp/... ./internal/infra/...`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/backend/gcp/names.go internal/backend/gcp/names_test.go internal/infra/spec_test.go
git commit -m "project secrets task 2: gcp.ProjectSecretID; pin that rs.Secrets stays unconditional"
```

---

### Task 3: `Readiness` resolves precedence (the live check)

**Files:**
- Edit: `internal/infra/readiness.go`, `readiness_test.go`

**Interfaces:**
- `Readiness` gains a parameter (or `RepoSpec` gains a field `ProjectSecrets map[string]string`, logical name → `gcp.ProjectSecretID(...)`, computed by the caller from local config before calling `Readiness` — the implementer picks whichever keeps `Readiness`'s existing signature style; `RepoSpec` gaining the field, populated unconditionally wherever `AllowedForProjectSecret`/`ProviderBySecret`/`ClaudeAccountBySecret` says this repo may use a name, reads more consistently with how `rs.Secrets` is already populated in `infra.Repo()`).
- `Readiness`'s per-workflow loop (`readiness.go:89-137`): for each `logical` in `uniqueSecrets(ws)` that also has a `spec.ProjectSecrets[logical]` entry, check `hasVersion(logical)` against the **repo** ID (today's `spec.Secrets[logical]`) first; if false, check `hasEnabledVersion` against `spec.ProjectSecrets[logical]` instead, and if that is true, set `ws.SecretIDs[logical] = spec.ProjectSecrets[logical]` (note: `SecretEnv` — env var name → logical name — is unchanged; only the ID a logical name resolves to moves). If neither has a version, the existing `Missing{Kind: MissingSecret}` reason names `fugaro secrets set NAME --project-wide` instead of `--repo NAME` when `spec.ProjectSecrets[logical]` exists (design §5).

**Tests:**
```go
func TestReadinessPrefersRepoOverrideWhenPresent(t *testing.T) {
	// Both the repo and project secret have an enabled version (fake
	// Secret Manager). Assert the resolved ws.SecretIDs[logical] equals
	// the REPO's ID, unchanged from today, and that no Missing is reported.
}

func TestReadinessFallsBackToProjectWhenRepoHasNoVersion(t *testing.T) {
	// Repo secret has 0 versions, project secret has 1. Assert
	// ws.SecretIDs[logical] equals the PROJECT ID and no Missing is
	// reported for that logical name.
}

func TestReadinessOmitsProjectSecretForUnlistedRepo(t *testing.T) {
	// spec.ProjectSecrets has no entry for this logical name (the repo
	// isn't allow-listed): behavior is byte-for-byte today's (repo ID only,
	// Missing names --repo, never --project-wide).
}

func TestReadinessMissingNamesProjectWideFix(t *testing.T) {
	// Neither has a version, and the repo IS allow-listed: the Missing
	// reason names `fugaro secrets set NAME --project-wide`, not --repo.
}

func TestReadinessFallsBackOnceRepoVersionGone(t *testing.T) {
	// Simulates P3/design §5's "falls back on the next apply" rule: a
	// first Readiness call with the repo version present resolves to the
	// repo ID; a second call (simulating a later init --repo) with that
	// version now disabled resolves to the project ID. Two calls, not one,
	// since Readiness never re-runs within a single apply.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestReadinessPrefersRepoOverride|TestReadinessFallsBack|TestReadinessOmitsProjectSecret|TestReadinessMissingNamesProjectWideFix' ./internal/infra/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/infra/... && go test -count=1 ./internal/infra/...`
Expected: `ok` (the full package, to catch any regression in the existing `Readiness` tests from the edit).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/readiness.go internal/infra/readiness_test.go
git commit -m "project secrets task 3: Readiness resolves repo-vs-project precedence at apply time"
```

---

### Task 4: `fugaro secrets set/ls --project-wide`; the installation root's container

**Files:**
- Edit: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`
- Create: `deploy/terraform/gcp/modules/installation/secrets.tf`
- Edit: `deploy/terraform/gcp/modules/installation/variables.tf`, `deploy/terraform/gcp/roots/installation/{main,variables}.tf`

**Interfaces (Go):**
- `secretsOptions` gains `projectWide bool`; `--project-wide` on `secrets set` and `secrets ls`.
- `secretsSet`: when `--project-wide`, skip `resolveSecretRepo` entirely (there is no `--repo` to resolve); validate `name` is a `config.ReservedSecrets` key with a non-empty `project_secrets[name].AllowDataTo`, or a provider's/claude-account's secret with a non-empty `allow_data_to` of its own (`config.AllowedForProjectSecret`-style check, reusing `ProviderBySecret`/`ClaudeAccountBySecret`); the secret ID is `gcp.ProjectSecretID(env.lc.Name, name)`; labels are `{fugaro: managed, fugaro_project: env.lc.Name, fugaro_secret: name}` (`gcp.LabelProject`, already defined, `names.go:66`, reused here for the first time outside the runs bucket).
- `secretsLs`: `--project-wide` lists by `{LabelProject: env.lc.Name}` instead of `{LabelRepo: r.label}`, and needs no `resolveSecretRepo` either. The existing (non-`--project-wide`) `ls` gains a `SCOPE` column: for each repo-owned entry, look up whether `config.AllowedForProjectSecret`-equivalent logic would also let this repo use a project copy of the same name, and print `project, repo override stored` when both exist; print a synthetic row (no version info beyond what a second `List` call under `{LabelProject}` returns) for a name the repo is allowed to use project-wide but has no repo-owned entry for at all, scope `project`.

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
func TestSecretsSetProjectWideRefusesEmptyAllowList(t *testing.T) { /* project_secrets has no entry, or an entry with no repos: refused, no network call made for the value (assert the fake prompt/reader was never invoked) */ }
func TestSecretsSetProjectWideAcceptsProviderSecret(t *testing.T) { /* openrouter-api-key, provider has allow_data_to: stored under gcp.ProjectSecretID */ }
func TestSecretsLsAddsScopeColumn(t *testing.T) { /* fixture with both a repo-owned and project-shared entry for one name: two distinct SCOPE values appear */ }
func TestSecretsLsProjectWideNeverPrintsValue(t *testing.T) { /* fake Secret Manager's Access never called, same as every existing *NeverPrintsValue test */ }
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestSecretsSetProjectWide|TestSecretsLsAddsScope|TestSecretsLsProjectWide' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the Go implementation**
- [ ] **Step 4: Write the Terraform (installation module)**, then validate it offline:

Run: `terraform -chdir=deploy/terraform/gcp/modules/installation fmt -check && terraform -chdir=deploy/terraform/gcp/modules/installation validate` (against a local init with no backend/credentials — the existing pattern this repository's Terraform tests already use; the implementer checks `deploy/terraform/gcp/roots/installation/tests/` for the harness in place).

- [ ] **Step 5: Run the Go tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -count=1 -run 'TestSecretsSetProjectWide|TestSecretsLsAddsScope|TestSecretsLsProjectWide' ./internal/cli/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/secrets.go internal/cli/secrets_test.go deploy/terraform/gcp/modules/installation/secrets.tf deploy/terraform/gcp/modules/installation/variables.tf deploy/terraform/gcp/roots/installation/main.tf deploy/terraform/gcp/roots/installation/variables.tf
git commit -m "project secrets task 4: fugaro secrets set/ls --project-wide; the installation root's container"
```

---

### Task 5: `build.tf`'s grant rewired through the resolved map (P4 — flagged for opus review)

**This task is security- and Terraform-sensitive: a build service account's `secretAccessor` grant is being re-pointed from a resource this module creates to a value that may come from a different Terraform root entirely. Route this task's review through opus, not the default reviewer, before merge.**

**Files:**
- Edit: `deploy/terraform/gcp/modules/repo/build.tf`, `workflows.tf`, `variables.tf`

**Interfaces:**
- `workflows.tf` currently computes, inline, `secrets = { for k, s in google_secret_manager_secret.this : k => s.secret_id }` (`workflows.tf:14`) and passes it to each `module "workflow"` call. Hoist this into a `locals` block in the repo module (e.g. `locals { secrets = merge({ for k, s in google_secret_manager_secret.this : k => s.secret_id }, var.repo.project_secrets) }`), where `var.repo.project_secrets` is a new `repo` object field (`map(string)`, logical name → an **already-existing** project secret ID, Go-computed from `Readiness`'s resolution in Task 3 — never a name this module creates a resource for).
- `build.tf:17` changes from `google_secret_manager_secret.this[each.value].secret_id` to `local.secrets[each.value]`.
- `variables.tf`'s existing precondition (`variables.tf:150-155`, "every secret that build_secrets... name must be one of repo.secrets") is widened to accept a name that is a key of either `repo.secrets` or `repo.project_secrets` — not narrowed: every name P2 already guarantees stays a key of `repo.secrets` too (P2: `rs.Secrets` never prunes), so in practice this precondition's existing form already holds; the widening is defensive, for a future change that might relax P2.

**Tests:**
```go
// internal/infra/ or a Terraform-plan-level test, whichever harness this
// repository's existing *_terraform_test.go files use (the implementer
// greps for TestBuildRepoSpec*Terraform* / terraform plan fixtures first)
func TestBuildAndJobGrantSameResolvedSecret(t *testing.T) {
	// For every fixture where a logical name resolves to the project ID
	// (Task 3's precedence), assert both the job's secret_env mount
	// (sa.tf/job.tf path) and the build account's grant (build.tf path)
	// reference the identical secret ID string. A divergence here means
	// one of the two paths is still reading a stale, repo-only map.
}
```

- [ ] **Step 1: Write the failing test**
- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run TestBuildAndJobGrantSameResolvedSecret ./internal/infra/...`
Expected: FAIL (or a build failure if the fixture needs `repo.project_secrets` wired first — the implementer adds that plumbing as part of this task, not Task 3, since Task 3 only touches `internal/infra`'s pure Go resolution, not the tfvars shape).

- [ ] **Step 3: Write the Terraform and the tfvars-shape Go change** (the `RepoSpec`/`tfvars.go` field that carries `rs.ProjectSecrets` into the JSON `repo.project_secrets` key)
- [ ] **Step 4: Run the test to verify it passes, then the full Terraform validate**

Run: `go vet ./internal/infra/... && go test -count=1 ./internal/infra/... && terraform -chdir=deploy/terraform/gcp/modules/repo fmt -check && terraform -chdir=deploy/terraform/gcp/modules/repo validate`
Expected: `ok` / no diff / valid.

- [ ] **Step 5: Commit**

```bash
git add deploy/terraform/gcp/modules/repo/build.tf deploy/terraform/gcp/modules/repo/workflows.tf deploy/terraform/gcp/modules/repo/variables.tf internal/infra/
git commit -m "project secrets task 5: build.tf's grant follows the same resolved secret map as the job's (opus-reviewed)"
```

---

### Task 6: plan-level fixtures (0 destroys on an unrelated-change plan)

**Files:**
- Edit: whichever existing `*_terraform_test.go`/`deploy/terraform/gcp/roots/*/tests/` harness this repository already runs offline Terraform plans through (the implementer greps for the harness `named-oauth-tokens.md`-style plans would have used, or, if none exists at the Go level, a Go-level fixture asserting the tfvars JSON shape is the acceptable substitute — state which was used in the commit message).

**Tests:**
```go
func TestInstallationPlanProjectSecretsIsCreateOnly(t *testing.T) {
	// A fresh var.project_secrets entry plans exactly 1 create (the secret)
	// plus its operator grants, 0 updates to any unrelated installation
	// resource, 0 destroys.
}
func TestRepoPlanScopeChangeIsZeroDestroys(t *testing.T) {
	// The scenario in design §6 step 4: a name moves from repo-only to
	// project-shared (repo has no version); the repo root's plan has 0
	// deletes of google_secret_manager_secret.this[name], at most 1 update
	// to the workflow's job (secret_env's secret_key_ref value changes).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**
- [ ] **Step 3: Make them pass** (this task should need no new production code if Tasks 2-5 are correct; it exists to prove the golden claim end to end, across both roots, not just within `internal/infra`)
- [ ] **Step 4: Run**

Run: `go test -count=1 ./internal/infra/... ./deploy/... 2>/dev/null || true` (the implementer uses whichever exact package path the harness lives in)

- [ ] **Step 5: Commit**

```bash
git commit -m "project secrets task 6: plan-level fixtures proving zero destroys across both roots"
```

---

### Task 7: `fugaro doctor`'s two new checks

**Files:**
- Edit: `internal/cli/doctor.go`, `internal/cli/doctor_test.go`

**Interfaces:**
- `project-secret-stored`: for every name `config.ReservedSecrets`/a provider's/an account's own list allow-lists this repository for, if neither the repo's own secret nor the project's has an enabled version, red, naming both candidate fix commands.
- `project-secret-scope-stale`: the repository's **last deployed** job's `secret_env` (read the same way `doctor`'s existing image-config checks already read a deployed job's spec) resolves a different scope than `Readiness` would compute right now — a warning (fails only `--strict`), mirroring `layered-config.md`'s "last run launched on an older generation" shape (`layered-config.md:179`).

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

### Task 8: Docs, cross-references, Check 40

**Files:**
- Edit: `docs/design/v1.md` (one cross-reference sentence, §5.4 local config area, no rewrite)
- Edit: `docs/multi-model.md` (a one-paragraph note that a provider's key may now also be stored `--project-wide`, with a link — no change to §1-§6's own content)
- Edit: `docs/gcp-live-checklist.md`: add Check 40 (40a-40e, design §8) as a new row and a `## Check 40: ...` section, in the file's existing style; update the "Numbering ruling" paragraph (`gcp-live-checklist.md:183-189`) to name 40 as taken by this design
- New/extended test in whichever file already asserts the checklist's numbering/doc shape (the implementer greps for a test covering the "Numbering ruling" claims, if one exists; otherwise this task adds none beyond the existing docs-lint suite)

- [ ] **Step 1: Write the docs edits**
- [ ] **Step 2: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`.

- [ ] **Step 3: Commit**

```bash
git add docs/design/v1.md docs/multi-model.md docs/gcp-live-checklist.md
git commit -m "project secrets task 8: docs cross-references and Check 40"
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
- [ ] **40b.** Confirm a repository outside the allow-list is refused at `--project-wide` with a message naming the missing entry, and that its deployed job carries no such env var at all.
- [ ] **40c.** Confirm precedence: a repository with its own stored version keeps mounting it even though the project copy exists and lists it; disable the repo version's last enabled version, re-run `fugaro init --repo`, confirm the job now mounts the project copy.
- [ ] **40d.** Confirm the data-policy gate still refuses a provider model for a repository outside that provider's `allow_data_to`, with the provider's key stored `--project-wide`.
- [ ] **40e.** Confirm no value appears in any log, `fugaro diagnose`, `secrets ls`/`ls --project-wide` output, or a `terraform plan`/`state show` of either root.
