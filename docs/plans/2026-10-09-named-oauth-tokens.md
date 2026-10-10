# Named OAuth tokens: implementation plan

**Goal:** Store several Claude OAuth tokens (different accounts/subscriptions), each under an owner-chosen name, and pick one per repository (default), per workflow (`agent.oauth_token: NAME` in `fugaro.yaml`) or per run (`fugaro run --token NAME`). No code in this plan has been written or run; this is a design-to-code plan, same status as `docs/plans/2026-10-08-upgrade.md` before its Task 1 started. **This PR (the one that adds this plan and its design doc) is docs-only: no file under this plan's "Files" lists is touched by it.** Tasks 1 onward are future work, gated on the owner approving the design.

**Spec:** [docs/design/named-oauth-tokens.md](../design/named-oauth-tokens.md). Its decisions T1 to T3 are settled there (where the named choice may live, and why not the project layer); this plan's own decisions continue the same letter, T4 onward, each independently vetoable.

**Depends on:** release 0.7.0 merged to `main` first (bucket-IAM hardening and the base-image consolidation both touch `internal/infra` heavily; this is a scheduling dependency, not a functional one — see design §8). No task here starts before 0.7.0 ships.

**Checked before this plan was written:** every fact the design doc cites was read from the code at commit `4df3021` (this branch's base) — `internal/config/config.go`, `internal/config/providers.go`, `internal/config/validate.go`, `internal/infra/spec.go`, `internal/infra/repo.go`, `internal/agent/env.go`, `internal/backend/gcp/run.go`, `internal/backend/gcp/names.go`, `internal/cli/secrets.go`, `internal/cli/initsecrets.go`, `internal/cli/run.go`, `internal/task/task.go`, `internal/runstore/runstore.go`, `internal/localcfg/localcfg.go`, `deploy/terraform/gcp/modules/{repo,workflow}/*.tf`, `docs/design/v1.md` §5 and §6, `docs/design/layered-config.md`, `SECURITY.md`. If the merged code differs when a task starts (especially `internal/infra/spec.go`, mid-churn from 0.7.0's base-image work), that task's implementer adapts its edits to the then-current file, not the other way round — the same rule `docs/plans/2026-10-08-upgrade.md` used for its own Task 3.

## Decisions (veto any before execution starts)

Continuing the design doc's T1 to T3:

- **T4. The secret ID is derived, never independently settable.** `claude_accounts.<name>` carries only `allow_data_to`; the secret is always `claude-oauth-token-<name>` (`config.ClaudeAccountSecret`), never a free-form `secret:` field (unlike `providers.<name>.secret`, which is free-form because a provider's key is also used as the HTTP credential's logical name across multiple possible routes). This removes a whole class of owner typo — two names pointing at the same secret, or a secret name that collides with a workflow's own declared secret — at the cost of one degree of freedom nobody asked for. *Veto alternative:* a free `secret:` field like providers, validated for uniqueness the same way `ValidateModelProviders` already does (`providers.go:109,131-135`); more flexible, more ways to misconfigure.
- **T5. One job/service-account variant per (workflow, allowed name) — ship it as Go-only, zero Terraform diff.** Verified in the design (§4, §1): `modules/repo/workflows.tf`'s `for_each = var.repo.workflows` and `modules/workflow/job.tf`'s per-secret IAM loop are already generic over whatever map `internal/infra` hands them. The entire feature's infrastructure surface is new entries in that Go-built map, keyed by `gcp.WorkflowVariant(workflow, token)`. *Veto alternative:* a new `modules/workflow-token` submodule, explicit about variants in HCL; rejected, since it would duplicate `modules/workflow` for no behavioral difference and the existing module already covers this shape.
- **T6. `task.Spec.OAuthToken` is a plain, one-shot field, not an `Overrides` entry.** It is resolved once at launch (`fugaro run`'s CLI code) and is not itself re-overridable by a follow-up: a follow-up's agent inherits the first run's resolved value, read from that run's own `task.json`/`result.json`, exactly as it inherits the branch and the session. *Veto alternative:* let a follow-up's launch also take `--token`, re-resolved each time; rejected because nothing in `followup.trusted` is meant to steer *which credential* a run uses, only what the agent does with the one it has (design §6b).
- **T7. The CLI resolves and refuses before any network call**, mirroring `secretsSet`'s existing provider check (`internal/cli/secrets.go:172-178`) exactly: `fugaro run --token NAME` (or `agent.oauth_token: NAME`) is checked against `config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)` in the launching CLI, before `Launch` is called. A disallowed name never reaches `Launch`'s own 404 path; it gets a direct, specific error instead. *Veto alternative:* let it fall through to `Launch`'s 404 ("job does not exist"); rejected as a worse message for an extremely likely typo.
- **T8. `fugaro doctor`'s new checks are additive and offline, in the existing `doctorCheck` family** (`internal/cli/doctor.go`), not a new subcommand. *Veto alternative:* a dedicated `fugaro accounts doctor`; rejected, `doctor` is already the one place that aggregates read-only health checks and a second command would split that.
- **T9. No per-account accounting rollup in this plan.** `runstore.Record.OAuthAccount` is written and displayed; `internal/budget`'s schema, the Realtime Database rules and the Firestore rollover are untouched (design §6e, §9). *Veto alternative:* add the dimension now; rejected for this plan because it is a security-sensitive change (the RTDB rules are "leaf-only for writes," proved by a property test against an emulator) that deserves its own review cycle, not a rider here.

## Global Constraints

Every task's requirements include these.

From the design:
- No `fugaro.yaml` key or project-layer object may ever *define* a named account or widen its allow-list; only the local config can (T1, design §3).
- `config.ReservedSecrets["claude-oauth-token"]` and its behavior with no `claude_accounts` configured are unchanged, byte for byte (design §5's golden-compatibility test, Task 4).
- No secret value is ever read back, echoed, or logged, in any new code path (the existing rule every `secrets` command already follows).
- No change to `internal/budget` (T9).
- `task.Spec`'s strict decoding stays strict; the new field is `omitempty` and gated by a version check before it is ever set to a non-default value (design §5).
- No live GCP or live Anthropic call in any automated test; the live-check items (U1 to U4) are the owner's own task (Task 11), run once, in the sandbox project only, never in CI.

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the **foreground**, with `-count=1`, and `-race` where the package's existing tests use it.
- Fakes only in tests: `gcpfake`, `file://`/`mem://` local configs, no real `claude`, no real Secret Manager.
- Docs must match behavior: a docs test (Task 9) keeps the setup skill's commands real, the same pattern `plugin/skills_lint_test.go` already enforces for every other skill.

## Review Focus (mutation-proof notes)

The failure modes most likely to hit an owner, each pinned by a test that would fail if the fix were subtly wrong (not merely present):

1. **A disallowed repository gets a job variant anyway**, or a disallowed name is accepted by `secrets set`/`run --token`. Pinned by: `TestAllowedClaudeAccountsExcludesUnlistedRepo` (Task 1 — asserts the *set* a repository may choose from, not just that one call refuses); `TestRepoSpecHasNoVariantForDisallowedName` (Task 4 — asserts the Terraform-bound map has no such key at all, not just that launching it fails); `TestRunTokenRefusesDisallowedName` (Task 6).
2. **A configuration with no `claude_accounts` entry produces a different job, SA, or secret mount than today.** Pinned by `TestWorkflowVariantIsIdentityForDefault` (Task 2) and the golden `TestBuildRepoSpecUnchangedWithNoClaudeAccounts` (Task 4), which diffs the full tfvars JSON of the existing fixture corpus before and after.
3. **A follow-up changes which account a run uses, via a PR comment or a re-launch flag.** Pinned by `TestFollowUpInheritsOAuthToken` (Task 6), which asserts the follow-up's task.json token equals the original's even when a different `--token` is passed to the follow-up command (it must be refused or ignored — see Task 6's exact rule) and even when a trusted comment's text contains a token name.
4. **An old runner silently ignores the new field instead of refusing.** Pinned by `TestOldRunnerRefusesOAuthTokenField` (Task 3): strict-decoding a `task.json` with `oauth_token` set, through a decoder built the way a pre-feature runner's was, must error, not silently drop the field.
5. **`fugaro secrets ls`/`doctor` ever print or imply a secret value.** Pinned by the existing redaction test pattern, extended: `TestSecretsLsNamedAccountNeverPrintsValue` (Task 5), `TestDoctorNamedAccountCheckReadsNoValue` (Task 8) — both assert the fake Secret Manager's `Access` (value-reading) call is never made, only `List`/metadata calls.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/claude_accounts.go` (new), `claude_accounts_test.go` (new) | `ClaudeAccount`, `ValidateClaudeAccounts`, `ClaudeAccountSecret`, `ClaudeAccountBySecret`, `AllowedClaudeAccounts`, `ClaudeAccountNameRE` | 1 |
| `internal/localcfg/localcfg.go`, `localcfg_test.go` | `Config.ClaudeAccounts map[string]config.ClaudeAccount` field, parse/validate wiring | 1 |
| `internal/backend/gcp/names.go`, `names_test.go` | `WorkflowVariant` | 2 |
| `internal/task/task.go`, `task_test.go` | `Spec.OAuthToken`, `namedTokensSince` gate | 3 |
| `internal/infra/spec.go`, `spec_test.go`, `internal/infra/repo.go` | `SecretMounts` takes a token; the repo-spec loop builds one `WorkflowSpec` per allowed name; `c.workflow` takes `(name, token string)` | 4 |
| `internal/cli/secrets.go`, `secrets_test.go`, `internal/cli/initsecrets.go`, `initsecrets_test.go` | `claude-oauth-token-<name>` validated like a provider secret; `SecretCommands`/`Left()` list every allowed name's `claude setup-token` + `secrets set` pair | 5 |
| `internal/cli/run.go`, `run_test.go` | `--token` flag, resolution and refusal, follow-up inheritance | 6 |
| `internal/runstore/runstore.go`, `runstore_test.go`, `internal/cli/ls.go` (or wherever `fugaro ls` renders a row), `diagnose.go`, `logs.go` | `Record.OAuthAccount`, displayed | 7 |
| `internal/cli/doctor.go`, `doctor_test.go` | the two offline checks (§T8) | 8 |
| `docs/design/v1.md`, `docs/design/layered-config.md`, `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`, `docs/gcp-live-checklist.md`, `internal/cli/docs_*_test.go` (whichever docs test fits, new case) | cross-references, the setup skill, U1-U4 | 9 |
| none | full suite, release-notes task group | 10 |
| `docs/gcp-live-checklist.md` (results pasted in) | the owner's own run | 11 |

## PR group

One PR, branch `named-oauth-tokens`, Tasks 1 to 10 in order for the merge; Task 11 is the owner's own run, ideally started early (its U1 finding affects whether Task 4/6 is written against option B or discovers option D is available — see design §4) but not blocking: this plan is written assuming option B, and ships correctly whatever U1 finds, since U1 only ever *narrows* work still to do, never invalidates work already merged.

Parallelizable: **Tasks 1, 2, 3 and 7 are independent of each other** and may be implemented together. Task 4 needs 1, 2 and 3. Task 5 needs 1. Task 6 needs 1 through 4. Task 8 needs 1. Task 9 needs 4 through 8 settled (it documents the shipped shape). Task 10 is last.

---

### Task 0: Start

- [ ] **Step 1: Confirm the dependency**

```bash
git fetch -q
git log --oneline -1 main  # expect the 0.7.0 release tag's merge commit, or a commit after it
grep -rn "ParseProjectLayer\|resolveFugaroYAML" internal/config/*.go | head -5  # 0.6.0 landed
```

Expected: 0.7.0's bucket-IAM and base-image commits are on `main`. If not: **stop** and report; nothing in this plan starts before that.

---

### Task 1: `config.ClaudeAccount`, its validation and the local config field

**Files:**
- Create: `internal/config/claude_accounts.go`
- Test: `internal/config/claude_accounts_test.go`
- Edit: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`

**Interfaces:**
- Consumes: `config.Problem`, `config.repoSlugRE` (package-private, same package), `config.sortedKeys` (package-private helper already used by `providers.go`).
- Produces:
  ```go
  // package config
  const DefaultClaudeAccount = "default"
  var ClaudeAccountNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
  type ClaudeAccount struct{ AllowDataTo []string }
  func (a ClaudeAccount) AllowsData(repo string) bool
  func ClaudeAccountSecret(name string) string
  func ClaudeAccountBySecret(accounts map[string]ClaudeAccount, secret string) (name string, ok bool)
  func AllowedClaudeAccounts(accounts map[string]ClaudeAccount, repo string) []string
  func ValidateClaudeAccounts(accounts map[string]ClaudeAccount) []Problem
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/config/claude_accounts_test.go`:

```go
package config

import "testing"

func TestClaudeAccountSecret(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"", "claude-oauth-token"},
		{"default", "claude-oauth-token"},
		{"acme-personal", "claude-oauth-token-acme-personal"},
	} {
		if got := ClaudeAccountSecret(tc.name); got != tc.want {
			t.Errorf("ClaudeAccountSecret(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClaudeAccountBySecret(t *testing.T) {
	accounts := map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}
	if name, ok := ClaudeAccountBySecret(accounts, "claude-oauth-token"); !ok || name != DefaultClaudeAccount {
		t.Fatalf("default: %q %v", name, ok)
	}
	if name, ok := ClaudeAccountBySecret(accounts, "claude-oauth-token-acme-personal"); !ok || name != "acme-personal" {
		t.Fatalf("named: %q %v", name, ok)
	}
	if _, ok := ClaudeAccountBySecret(accounts, "claude-oauth-token-unknown"); ok {
		t.Fatal("an unconfigured name matched")
	}
}

func TestAllowedClaudeAccountsExcludesUnlistedRepo(t *testing.T) {
	accounts := map[string]ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/app", "Acme/Sandbox"}}, // case folded
		"other-team":    {AllowDataTo: []string{"other/repo"}},
	}
	got := AllowedClaudeAccounts(accounts, "acme/app")
	want := []string{"acme-personal", "default"} // sorted, default always present
	if !equalSorted(got, want) {
		t.Fatalf("AllowedClaudeAccounts(acme/app) = %v, want %v", got, want)
	}
	if got := AllowedClaudeAccounts(accounts, "acme/sandbox"); !equalSorted(got, []string{"acme-personal", "default"}) {
		t.Fatalf("case-insensitive repo match failed: %v", got)
	}
	if got := AllowedClaudeAccounts(accounts, "acme/unrelated"); !equalSorted(got, []string{"default"}) {
		t.Fatalf("an unlisted repository got a named account: %v", got)
	}
	if got := AllowedClaudeAccounts(nil, "acme/app"); !equalSorted(got, []string{"default"}) {
		t.Fatalf("no claude_accounts configured: %v", got)
	}
}

func TestValidateClaudeAccounts(t *testing.T) {
	bad := map[string]ClaudeAccount{
		"default":  {},                               // reserved
		"Acme":     {},                                // uppercase
		"ok-name":  {AllowDataTo: []string{"not-a-repo-slug"}},
	}
	ps := ValidateClaudeAccounts(bad)
	wantPaths := map[string]bool{"claude_accounts.default": true, "claude_accounts.Acme": true, "claude_accounts.ok-name.allow_data_to": true}
	if len(ps) != len(wantPaths) {
		t.Fatalf("ValidateClaudeAccounts = %+v, want one problem per %v", ps, wantPaths)
	}
	for _, p := range ps {
		if !wantPaths[p.Path] {
			t.Errorf("unexpected problem path %q", p.Path)
		}
	}
	if ps := ValidateClaudeAccounts(map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("a valid block was refused: %+v", ps)
	}
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestClaudeAccount|TestAllowedClaudeAccounts|TestValidateClaudeAccounts' ./internal/config/`
Expected: FAIL, build failed: `undefined: ClaudeAccount`, `undefined: ClaudeAccountSecret`, etc.

- [ ] **Step 3: Write the implementation**

Create `internal/config/claude_accounts.go`:

```go
package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// DefaultClaudeAccount is the always-available, implicit name: the
// unsuffixed claude-oauth-token secret every repository already uses
// (design docs/design/named-oauth-tokens.md §1). It is reserved and may
// not appear as a key of claude_accounts.
const DefaultClaudeAccount = "default"

// ClaudeAccountNameRE is a named Claude OAuth account's name: never
// "default" (checked separately, so the message names the reservation).
var ClaudeAccountNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// ClaudeAccount is one named Claude OAuth credential the owner has
// configured in the local config (never fugaro.yaml or the project layer:
// design §3 T1), parallel to ModelProvider. Its secret is derived
// (ClaudeAccountSecret), never independently settable (design §3 T4).
type ClaudeAccount struct {
	// AllowDataTo are the repositories (owner/name) whose runs may choose
	// this account; empty means none.
	AllowDataTo []string `yaml:"allow_data_to,omitempty" json:"allow_data_to,omitempty"`
}

// AllowsData reports whether repo (owner/name) may choose a. Case-folded,
// like ModelProvider.AllowsData.
func (a ClaudeAccount) AllowsData(repo string) bool {
	return slices.ContainsFunc(a.AllowDataTo, func(r string) bool { return strings.EqualFold(r, repo) })
}

// ClaudeAccountSecret is the logical secret name holding name's token:
// "claude-oauth-token" for "" or "default", else
// "claude-oauth-token-"+name. It is a pure function of name: no local
// config lookup, so it never needs claude_accounts to exist for the
// default account, which is how backward compatibility holds (design §5).
func ClaudeAccountSecret(name string) string {
	if name == "" || name == DefaultClaudeAccount {
		return "claude-oauth-token"
	}
	return "claude-oauth-token-" + name
}

// ClaudeAccountBySecret is the account name whose secret is secret, the
// mirror of ProviderBySecret: used where a secret ID must be traced back
// to the name a human chose (fugaro secrets set's validation, Task 5).
func ClaudeAccountBySecret(accounts map[string]ClaudeAccount, secret string) (name string, ok bool) {
	if secret == ClaudeAccountSecret(DefaultClaudeAccount) {
		return DefaultClaudeAccount, true
	}
	for _, n := range sortedKeys(accounts) {
		if ClaudeAccountSecret(n) == secret {
			return n, true
		}
	}
	return "", false
}

// AllowedClaudeAccounts are the names repo may choose among: "default"
// always, plus every configured name whose allow_data_to lists repo,
// sorted. This is the one place that decides the question in design §6a;
// infra's job-variant loop (Task 4) and the CLI's --token resolution
// (Task 6) both call it, never duplicate the logic.
func AllowedClaudeAccounts(accounts map[string]ClaudeAccount, repo string) []string {
	out := []string{DefaultClaudeAccount}
	for _, n := range sortedKeys(accounts) {
		if accounts[n].AllowsData(repo) {
			out = append(out, n)
		}
	}
	return out
}

// ValidateClaudeAccounts reports every rule the local config's
// claude_accounts block breaks, the same shape as ValidateModelProviders.
func ValidateClaudeAccounts(accounts map[string]ClaudeAccount) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(accounts) {
		at := "claude_accounts." + name
		switch {
		case name == DefaultClaudeAccount:
			ps = append(ps, Problem{Path: at, Message: `"default" is reserved for the unnamed claude-oauth-token secret and may not be configured here`})
		case !ClaudeAccountNameRE.MatchString(name):
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("account name must match %s", ClaudeAccountNameRE)})
		}
		for _, r := range accounts[name].AllowDataTo {
			if !repoSlugRE.MatchString(r) {
				ps = append(ps, Problem{Path: at + ".allow_data_to", Message: fmt.Sprintf("%q must be a repository as owner/name", r)})
			}
		}
	}
	return ps
}
```

`sortedKeys` and `repoSlugRE` already exist in `internal/config/providers.go` (package-private); no new helper is added for them.

Edit `internal/localcfg/localcfg.go`: add, next to `Providers`:

```go
	// ClaudeAccounts are named Claude OAuth credentials a repository may
	// choose among by name (design docs/design/named-oauth-tokens.md).
	// Only the owner sets them, here, never in fugaro.yaml.
	ClaudeAccounts map[string]config.ClaudeAccount `yaml:"claude_accounts,omitempty"`
```

Wire `config.ValidateClaudeAccounts(c.ClaudeAccounts)` into whichever function already calls `config.ValidateModelProviders(c.Providers)` for the local config (the implementer greps `ValidateModelProviders(` to find it — expected to be one call, in the local config's own `Validate`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/config/... ./internal/localcfg/... && go test -count=1 ./internal/config/... ./internal/localcfg/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/claude_accounts.go internal/config/claude_accounts_test.go internal/localcfg/localcfg.go internal/localcfg/localcfg_test.go
git commit -m "named oauth tokens task 1: config.ClaudeAccount, its validation and the local config field"
```

(Every commit message in this plan ends with the session's two attribution lines.)

---

### Task 2: `gcp.WorkflowVariant`

**Files:**
- Edit: `internal/backend/gcp/names.go`
- Test: `internal/backend/gcp/names_test.go`

**Interfaces:**
- Produces: `func WorkflowVariant(workflow, token string) string`

- [ ] **Step 1: Write the failing test**

Add to `internal/backend/gcp/names_test.go`:

```go
func TestWorkflowVariantIsIdentityForDefault(t *testing.T) {
	for _, tok := range []string{"", "default"} {
		if got := WorkflowVariant("build", tok); got != "build" {
			t.Errorf("WorkflowVariant(build, %q) = %q, want build", tok, got)
		}
	}
	if got := WorkflowVariant("build", "acme-personal"); got != "build--acme-personal" {
		t.Errorf("WorkflowVariant(build, acme-personal) = %q", got)
	}
	// Every downstream name-deriving function must still produce a distinct,
	// valid name for a variant: this is what JobName, ServiceAccountID and
	// SecretID are exercised against.
	if JobName("s", WorkflowVariant("build", "acme-personal")) == JobName("s", "build") {
		t.Fatal("a variant collided with the plain workflow's job name")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run TestWorkflowVariant ./internal/backend/gcp/`
Expected: FAIL, `undefined: WorkflowVariant`.

- [ ] **Step 3: Write the implementation**

Add to `internal/backend/gcp/names.go`, next to `JobName`:

```go
// WorkflowVariant is the key JobName, ServiceAccountID and SecretID derive
// a token-scoped workflow's resources from: workflow unchanged for the
// repository's default account ("" or "default", config.DefaultClaudeAccount),
// so an installation with no named accounts configured gets byte-identical
// names (design docs/design/named-oauth-tokens.md §5); workflow+"--"+token
// otherwise. It is a plain string key, not a secret: never log-redacted.
func WorkflowVariant(workflow, token string) string {
	if token == "" || token == "default" {
		return workflow
	}
	return workflow + "--" + token
}
```

(This package does not import `internal/config`, so the literal `"default"` is repeated rather than referencing `config.DefaultClaudeAccount`; a comment says so. The implementer confirms there is no import cycle risk either way before choosing.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/backend/gcp/... && go test -count=1 ./internal/backend/gcp/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/backend/gcp/names.go internal/backend/gcp/names_test.go
git commit -m "named oauth tokens task 2: gcp.WorkflowVariant, the identity-for-default naming key"
```

---

### Task 3: `task.Spec.OAuthToken` and the version-skew gate

**Files:**
- Edit: `internal/task/task.go`, `internal/task/task_test.go`
- Edit: wherever `layeredSince`-style version gates already live (the implementer greps `layeredSince` to find the one place that compares a build record's base against a feature's minimum version; expected in `internal/cli`, near the check that refuses launching `ProjectLayer` against an old image)

**Interfaces:**
- Produces: `Spec.OAuthToken string` (json `oauth_token,omitempty`); `const namedTokensSince = "0.8.0"` (or the actual target version at ship time — the implementer confirms against `docs/release.md`'s in-flight version when this task starts, since 0.7.0 may have shipped as something other than exactly "0.7.0" by then).

- [ ] **Step 1: Write the failing test**

Add to `internal/task/task_test.go`:

```go
func TestOldRunnerRefusesOAuthTokenField(t *testing.T) {
	// Simulates a pre-feature runner's strict decode: it does not know
	// oauth_token, so it must refuse, not silently drop the field.
	spec := Spec{Version: 1, RunID: "20261009-101010-abcd", Repo: "acme/app", Ref: "main", OAuthToken: "acme-personal"}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	// Decode with an older schema: a struct copy of Spec minus OAuthToken,
	// strictly. The implementer builds this type from Spec as it stood
	// before this task (a git-blame copy, not a re-derivation), so the test
	// actually proves what an old binary did, not what this task wishes it did.
	type oldSpec struct {
		Version int    `json:"version"`
		RunID   string `json:"run_id"`
		Repo    string `json:"repo"`
		Ref     string `json:"ref"`
		// ... every other current field, copied verbatim ...
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old oldSpec
	if err := dec.Decode(&old); err == nil {
		t.Fatal("an old-shaped decoder accepted oauth_token silently")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run TestOldRunnerRefusesOAuthTokenField ./internal/task/`
Expected: FAIL, `undefined: Spec.OAuthToken` (build failure, since `OAuthToken` does not exist yet).

- [ ] **Step 3: Write the implementation**

In `internal/task/task.go`, add to `Spec` (next to `ProjectLayer`):

```go
	// OAuthToken is the named Claude account (config.ClaudeAccountNameRE,
	// or "" for config.DefaultClaudeAccount) this run's agent.auth: oauth
	// workflow used (design docs/design/named-oauth-tokens.md). Runners
	// before namedTokensSince refuse the field; the launching CLI checks
	// the job's build record first (Task 6).
	OAuthToken string `json:"oauth_token,omitempty"`
```

Add, near wherever `layeredSince` is defined:

```go
// namedTokensSince is the first release whose runner decodes task.json's
// oauth_token field; a launch that would set it against an older build
// record is refused (design §5).
const namedTokensSince = "0.8.0" // confirm against docs/release.md when this task starts
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/task/... && go test -count=1 ./internal/task/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/task/task.go internal/task/task_test.go
git commit -m "named oauth tokens task 3: task.Spec.OAuthToken and the version-skew gate"
```

---

### Task 4: `infra` builds one job variant per allowed account

**Files:**
- Edit: `internal/infra/spec.go`, `internal/infra/spec_test.go`
- Edit: `internal/infra/repo.go` (if `SecretMounts`'s call sites outside `spec.go` need the new parameter — the implementer greps `SecretMounts(` first)

**Interfaces:**
- Changes: `SecretMounts(gitSecret string, cfg *config.Config, name string, w config.Workflow, lc *localcfg.Config, repo string) ([]SecretMount, error)` gains a `token string` parameter (becomes the 4th or wherever reads best): for `cfg.Agent.Auth == "oauth"`, mounts `{config.ClaudeAccountSecret(token), "CLAUDE_CODE_OAUTH_TOKEN"}` instead of the literal `{"claude-oauth-token", ...}`.
- Changes: `func (c *repoCtx) workflow(name string) (WorkflowSpec, error)` becomes `func (c *repoCtx) workflow(name, token string) (WorkflowSpec, error)`: every place inside it that derives `Job`, `ServiceAccount.AccountID`/`DisplayName` from `(slug, name)` instead derives from `(slug, gcp.WorkflowVariant(name, token))`; every place that reads `c.in.Cfg.Workflows[name]` for commands/base/resources/timeouts is unchanged (it still looks up the *logical* workflow, by `name`, never by the variant key).
- Changes: `BuildRepoSpec`'s loop (`spec.go:726-734` today) iterates, for each workflow `name`:
  ```go
  tokens := []string{config.DefaultClaudeAccount}
  if in.Cfg.Agent.Auth == "oauth" {
      tokens = config.AllowedClaudeAccounts(lc.ClaudeAccounts, in.Repo)
  }
  for _, tok := range tokens {
      ws, err := c.workflow(name, tok)
      if err != nil {
          return RepoSpec{}, err
      }
      key := gcp.WorkflowVariant(name, tok)
      rs.Workflows[key] = ws
      maps.Copy(rs.Secrets, ws.SecretIDs)
      rs.BuildSecrets = append(rs.BuildSecrets, ws.BuildSecrets...)
      // ... the existing per-name rebuild-check-list append, keyed as today (once per logical
      // workflow, not once per variant: the daily check covers the workflow's image, which is
      // the same image across every token variant — only the running job differs) ...
  }
  ```
  The exact placement of the rebuild-check append and any other per-`name` bookkeeping in the existing loop body must be read from the then-current file before this is applied verbatim (design plan's standing rule, see header).

**Tests:**

```go
// internal/infra/spec_test.go

func TestBuildRepoSpecUnchangedWithNoClaudeAccounts(t *testing.T) {
	// Golden: every existing fixture (the corpus BuildRepoSpec's other tests
	// already use) produces byte-identical tfvars JSON before and after this
	// task, when lc.ClaudeAccounts is nil. Marshal RepoSpec to JSON with the
	// same indenting the CLI uses, compare against a checked-in golden file
	// per fixture (or, simpler, against a second BuildRepoSpec call on a
	// manually reverted copy — the implementer picks the lighter mechanism
	// the existing spec_test.go fixtures already support).
}

func TestRepoSpecHasNoVariantForDisallowedName(t *testing.T) {
	lc := &localcfg.Config{ /* ... */ ClaudeAccounts: map[string]config.ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/other"}}, // not this repo
	}}
	cfg := &config.Config{Agent: config.Agent{Auth: "oauth"}, Workflows: map[string]config.Workflow{"build": {/* ... */}}}
	rs, err := BuildRepoSpec(Inputs{LC: lc, Repo: "acme/app", Cfg: cfg /* ... */})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.Workflows["build--acme-personal"]; ok {
		t.Fatal("a job variant exists for a name this repository is not allowed")
	}
	if _, ok := rs.Workflows["build"]; !ok {
		t.Fatal("the default variant is missing")
	}
}

func TestRepoSpecSecretMountsOnlyTheVariantsOwnToken(t *testing.T) {
	// lc.ClaudeAccounts allows both "acme-personal" and "acme-overflow" for
	// this repository. Assert rs.Workflows["build--acme-personal"].SecretEnv
	// names exactly claude-oauth-token-acme-personal (not -overflow, not the
	// bare claude-oauth-token), and rs.Workflows["build--acme-overflow"]'s
	// names exactly the other — i.e., each variant's SecretIDs/SecretEnv map
	// has one Claude credential entry, never two, never the wrong one. This
	// is the test that would catch option A's regression (design §4) if
	// SecretMounts were wired wrong.
}
```

- [ ] **Step 1: Write the failing tests** (above, adapted to this repository's existing `spec_test.go` fixture helpers — the implementer reads that file first, since it likely already has a `repoCtx`/`Inputs` builder helper these tests should reuse rather than duplicate)
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestBuildRepoSpecUnchangedWithNoClaudeAccounts|TestRepoSpecHasNoVariantForDisallowedName|TestRepoSpecSecretMountsOnlyTheVariantsOwnToken' ./internal/infra/`
Expected: FAIL (new tests reference symbols/behavior that doesn't exist yet; the golden test should pass trivially until the loop changes, then it is the regression guard from here on).

- [ ] **Step 3: Write the implementation** (the diff described above, against the then-current `spec.go`)
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/infra/... && go test -count=1 ./internal/infra/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/spec.go internal/infra/spec_test.go internal/infra/repo.go
git commit -m "named oauth tokens task 4: infra builds one job variant per allowed account"
```

---

### Task 5: `fugaro secrets set/ls` and `init`'s prompts know the new shape

**Files:**
- Edit: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`
- Edit: `internal/cli/initsecrets.go`, `internal/cli/initsecrets_test.go`

**Interfaces:**
- Changes: `secretsSet` (`secrets.go:156-193`) gains a third branch, parallel to the provider one (lines 172-178):
  ```go
  if n, ok := config.ClaudeAccountBySecret(env.lc.ClaudeAccounts, name); ok && n != config.DefaultClaudeAccount {
      if !env.lc.ClaudeAccounts[n].AllowsData(r.repo) {
          return userErr("named Claude account %s does not list %s in allow_data_to, so its token is not stored for it", n, r.repo)
      }
  } else if !reserved && !declaresOrProvider /* existing condition */ {
      // existing workflow-secret branch, unchanged
  }
  ```
  The exact boolean wiring must respect the existing `reserved`/provider branch order (so `claude-oauth-token` itself, still reserved, is unaffected) — read `secrets.go:156-193` at task start and insert the new branch as a sibling of the provider one, not nested inside it.
- Changes: `SecretCommands` (`internal/cli/initsecrets.go:190-211`) and the interactive stage's `Left()`/`take()` (`initsecrets.go:414-439`, `506-528`) list one `claude setup-token` + `secrets set claude-oauth-token-<name>` pair **per name in `config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)` that is not yet stored**, not just the bare default — so `fugaro init` on a repository the owner has already allowed two named accounts for prompts for both.

**Tests:**

```go
func TestSecretsSetAcceptsAllowedNamedAccount(t *testing.T) { /* happy path, mirrors TestSecretsSet* for providers */ }

func TestSecretsSetRefusesDisallowedNamedAccount(t *testing.T) {
	// claude-oauth-token-acme-personal, repo not in allow_data_to: refused,
	// the message does not quote the stdin value (it never read it: the
	// refusal happens before readSecret is called — assert the fake prompt/
	// reader was never invoked).
}

func TestSecretsLsNamedAccountNeverPrintsValue(t *testing.T) {
	// A stored named-account secret appears in `secrets ls` by its
	// fugaro_secret label and version count only; the fake Secret Manager's
	// Access (value-read) method is asserted never called (mutation-proof
	// note #5).
}

func TestInitSecretsListsEveryAllowedName(t *testing.T) {
	// lc.ClaudeAccounts allows two names for this repo, neither stored yet:
	// Left().Commands has two "claude setup-token"+"secrets set
	// claude-oauth-token-<name>" pairs, not one.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestSecretsSet|TestSecretsLs|TestInitSecrets' ./internal/cli/`
Expected: FAIL (the new cases; the existing cases in the same `-run` pattern must still pass before this task's edits, confirming no regression was introduced by the test file edit alone).

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -count=1 -run 'TestSecretsSet|TestSecretsLs|TestInitSecrets' ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/secrets.go internal/cli/secrets_test.go internal/cli/initsecrets.go internal/cli/initsecrets_test.go
git commit -m "named oauth tokens task 5: secrets set/ls and init's prompts know named accounts"
```

---

### Task 6: `fugaro run --token NAME` and follow-up inheritance

**Files:**
- Edit: `internal/cli/run.go`, `internal/cli/run_test.go`

**Interfaces:**
- Produces: a new flag, `--token` (default `""`), added next to `--workflow` (`run.go:122`); `runOptions` gains `token string`.
- Changes: the launch path (around `run.go:742`, the `env.be.Launch` call) resolves, before building `backend.LaunchSpec`:
  ```go
  tokenName := o.token
  if tokenName == "" {
      tokenName = checkout.Agent.OAuthToken // the new fugaro.yaml key, resolved the same way agent.model already is
  }
  if tokenName == "" {
      tokenName = config.DefaultClaudeAccount
  }
  if checkout.Agent.Auth == "oauth" {
      allowed := config.AllowedClaudeAccounts(env.lc.ClaudeAccounts, repo)
      if !slices.Contains(allowed, tokenName) {
          return userErr("named Claude account %s is not one %s may use (allowed: %s); ask the owner to add it to claude_accounts in the project config", tokenName, repo, strings.Join(allowed, ", "))
      }
  }
  spec.OAuthToken = tokenName // on task.Spec, only when != config.DefaultClaudeAccount, per the omitempty field
  ```
  and the `backend.LaunchSpec{Workflow: ...}` passed to `Launch` uses `gcp.WorkflowVariant(workflow, tokenName)`, not the bare `workflow`.
- Adds `config.Agent.OAuthToken string `yaml:"oauth_token,omitempty"`` to `internal/config/config.go`'s `Agent` struct (next to `Recipe`), plus its row in `config.Scopes` (`docs/design/layered-config.md` §5's table — Project: no, Profile: **no**, Repo: yes, Override: yes, "it names a credential identity that only the repository's own, PR-reviewed fugaro.yaml may choose among names the local config (never the project layer) allow-lists" as the Why, per design §3 T2 — Profile is no for the same reason Project is: a profile is a key of the same launcher-writable `fugaro/project-layer.yaml` object as `defaults:`, so it gets no more trust) **only if Task 4's merge already landed the layered-config `Scopes`/`Resolve` machinery** — if `config.Scopes` does not exist yet at this task's start (layered config's 0.6.0 work may or may not include it depending on timing), this task adds the field without a `Scopes` row and flags the gap in its commit message for a follow-up once `Resolve` exists. A test asserts `Resolve` refuses `agent.oauth_token` set inside a `profiles.<name>` block with a message naming the layer, the same way `profile:` at top level is refused outside a workflow-less file (`layered-config.md` §4 "Errors name the layer") — this is the one new negative case `config.Scopes`'s existing enforcement (`ParseProjectLayer` refusing an out-of-scope key, `layered-config.md:107`) must cover without any code change, since the mechanism is already generic over the table; the test exists to prove the row was added correctly, not to add new refusal logic.
- Changes: a follow-up's launch path (wherever it builds the new run's `task.Spec` from the previous run's `result.json`/`task.json`) copies `OAuthToken` from the previous run unconditionally, ignoring any `--token` the follow-up command line passes (T6: log a warning if one was passed and differs, but never honor it).

**Tests:**

```go
func TestRunTokenDefaultsToConfiguredThenDefaultAccount(t *testing.T) { /* three sub-cases: --token wins, else agent.oauth_token, else "default" */ }

func TestRunTokenRefusesDisallowedName(t *testing.T) {
	// --token names an account this repo isn't allowed: refused before
	// Launch is called (assert the fake backend's Launch was never invoked).
}

func TestRunLaunchesTheVariantJob(t *testing.T) {
	// --token acme-personal: assert the fake backend recorded
	// LaunchSpec.Workflow == "build--acme-personal", not "build".
}

func TestFollowUpInheritsOAuthToken(t *testing.T) {
	// Previous run's task.json has OAuthToken "acme-personal". The follow-up
	// command is given --token "acme-overflow" (or the equivalent flag this
	// codebase's follow-up command actually exposes — the implementer checks
	// internal/cli's follow-up launch path for its real flag set first).
	// Assert the new run's task.Spec.OAuthToken is still "acme-personal",
	// and (if a flag was passed and differed) a warning was printed, never
	// an error that blocks the follow-up outright.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRunToken|TestRunLaunchesTheVariantJob|TestFollowUpInheritsOAuthToken' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -race -count=1 -run 'TestRunToken|TestRunLaunchesTheVariantJob|TestFollowUpInheritsOAuthToken' ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/run.go internal/cli/run_test.go internal/config/config.go
git commit -m "named oauth tokens task 6: fugaro run --token, agent.oauth_token, follow-up inheritance"
```

---

### Task 7: `fugaro ls`/`diagnose`/`logs` show which account a run used

**Files:**
- Edit: `internal/runstore/runstore.go`, `internal/runstore/runstore_test.go`
- Edit: whichever file(s) render `fugaro ls`'s row and `fugaro diagnose`'s summary (the implementer greps `r.Workflow` in `internal/cli/*.go` to find every rendering call site, since `Record.Workflow` is the existing sibling field to extend next to)

**Interfaces:**
- Adds `Record.OAuthAccount string `json:"oauth_account,omitempty"`` (`runstore.go:106-129`, next to `Workflow`), written by the runner at finalize from its own `task.Spec.OAuthToken` (falling back to `config.DefaultClaudeAccount` for display when the field is empty — the record itself stores the raw value, empty or not, matching `Workflow`'s own `omitempty` convention).
- `fugaro ls`'s row and `fugaro diagnose`'s summary print `account: NAME` only when it is not the default (keeps today's output unchanged for every repository with no named accounts — a snapshot/golden test on existing fixtures pins this).

**Tests:**

```go
func TestRecordRoundTripsOAuthAccount(t *testing.T) { /* marshal/unmarshal, omitempty when "" */ }

func TestLsRowShowsNonDefaultAccountOnly(t *testing.T) {
	// A record with OAuthAccount "" or "default": row unchanged from today
	// (golden). A record with "acme-personal": row gains exactly one new
	// field, in a fixed position, asserted by substring, not full-row
	// equality (so unrelated row-format changes don't spuriously fail this test).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRecordRoundTripsOAuthAccount|TestLsRowShowsNonDefaultAccountOnly' ./internal/runstore/... ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/runstore/... ./internal/cli/... && go test -count=1 -run 'TestRecordRoundTripsOAuthAccount|TestLsRowShowsNonDefaultAccountOnly' ./internal/runstore/... ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/runstore/runstore.go internal/runstore/runstore_test.go internal/cli/
git commit -m "named oauth tokens task 7: fugaro ls/diagnose/logs show which account a run used"
```

---

### Task 8: `fugaro doctor`'s two offline checks

**Files:**
- Edit: `internal/cli/doctor.go`, `internal/cli/doctor_test.go`

**Interfaces:**
- Adds two `doctorCheck`s, in the shape of `fugaroYAMLCheck` (`doctor.go:353-373`):
  1. **`claude-accounts-stored`**: every name in `config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)` other than `default` has a stored secret (metadata-only `secrets ls`-style read, by label, never `Access`); a missing one names the exact `claude setup-token` + `secrets set` pair (reusing Task 5's `SecretCommands`).
  2. **`oauth-token-allowed`**: the checkout's `agent.oauth_token` (if any) is in `config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)`; this is a pure, offline check (no GCP call at all — it only needs the already-parsed `fugaro.yaml` and the already-loaded local config), so it runs even without cloud access, like `fugaroYAMLCheck` itself.

**Tests:**

```go
func TestDoctorNamedAccountCheckReadsNoValue(t *testing.T) {
	// Asserts the fake Secret Manager's Access method is never called by
	// doctor, only List (mutation-proof note #5).
}

func TestDoctorFlagsUnstoredNamedAccount(t *testing.T) { /* ... */ }
func TestDoctorFlagsOAuthTokenNotAllowed(t *testing.T) {
	// fugaro.yaml names agent.oauth_token: acme-overflow, but the local
	// config's allow_data_to for acme-overflow does not list this repo:
	// offline check fails with a message naming both names, no GCP call made.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestDoctorNamedAccount|TestDoctorFlagsUnstoredNamedAccount|TestDoctorFlagsOAuthTokenNotAllowed' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -count=1 -run 'TestDoctor' ./internal/cli/`
Expected: `ok` (the full `TestDoctor*` set, to catch any regression in the existing checks from the edit).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/doctor.go internal/cli/doctor_test.go
git commit -m "named oauth tokens task 8: fugaro doctor's two offline checks for named accounts"
```

---

### Task 9: Docs

**Files:**
- Edit: `docs/design/v1.md` (§5.4 local config, §6.1 trust boundary: one cross-reference sentence each, no rewrite)
- Edit: `docs/design/layered-config.md` (§5's scope table gains the `agent.oauth_token` row, if Task 6's `config.Scopes` branch landed it)
- Edit: `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md` (the exact `claude setup-token` multi-account sequence, filled in **only after Task 11's U2 answers it** — until then this task adds the shape with a placeholder the live check replaces, and the plugin lint's existing rule that skill commands must be real still applies: no invented command appears in the skill text)
- Edit: `docs/gcp-live-checklist.md`: four new numbered checks, U1 to U4 (§7 of the design), in the file's existing style (`FUGARO_LIVE_*` env vars, `FACT:` lines, cleanup-first)
- New test, in whichever file already asserts a skill's commands are real (`plugin/skills_lint_test.go` or the `internal/cli/docs_*_test.go` family) covering the new setup-skill text

- [ ] **Step 1: Write the docs edits**
- [ ] **Step 2: Write/extend the docs test**
- [ ] **Step 3: Run the docs tests**

Run: `go test ./plugin/ ./internal/cli -run 'Docs|Design|Readme|Skill' -count=1 -timeout 9m`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add docs/design/v1.md docs/design/layered-config.md plugin/skills/setup/SKILL.md plugin/skills/setup/reference/decisions.md docs/gcp-live-checklist.md
git commit -m "named oauth tokens task 9: docs, the setup skill and the live-check additions"
```

---

### Task 10: Full suite and release-notes placement

- [ ] **Step 1: Full focused suite**

Run (foreground, split by package if any call would exceed 9 minutes):

```bash
go vet ./...
gofmt -l . # expect no output
go test -race -count=1 ./internal/config/... ./internal/localcfg/... ./internal/backend/gcp/... ./internal/task/... ./internal/infra/... ./internal/cli/... ./internal/runstore/... ./plugin/...
```

Expected: `ok` everywhere, no `gofmt` output.

- [ ] **Step 2: Release-notes task group**

Per `docs/release.md`'s existing rollout pattern (`layeredSince`-style precedent, §10 of `layered-config.md`): add this feature's section to whichever `docs/releases/vX.Y.Z.md` is open for the release named in design §8 (create it if Task 9's docs edits are the first to land after the previous release's notes were cut), naming `namedTokensSince` and the rollout order (upgrade every CLI and every workflow's job image before an owner configures `claude_accounts`).

- [ ] **Step 3: Commit**

```bash
git commit -m "named oauth tokens task 10: full suite, release-notes placement"
```

---

### Task 11: Owner-run live check (not part of the PR; run in the sandbox project only)

This task is never run by an implementer or by CI. It is the owner's own task, matching this repository's existing `docs/gcp-live-checklist.md` pattern (guardrails: `FUGARO_LIVE_GCP_PROJECT`/`FUGARO_LIVE_REPO` only, ADC only, cleanup registered before any side effect). Paste the `FACT:` lines into the PR that eventually implements Task 4/6 and into `docs/gcp-live-checklist.md` itself.

- [ ] **U1. Cloud Run Jobs execution overrides and secrets.** In the sandbox project, deploy one job with a `secret_key_ref` env var (as `modules/workflow/job.tf` already does), then attempt a `jobs.run` call whose `Overrides` names a different secret for that same env var (or a second `secret_key_ref`-shaped override field, if the live API exposes one `google.golang.org/api/run/v2`'s generated types in this repository do not reference). Record: does the API accept it? If it does, does the execution's container actually see the overridden secret, or the job template's original? Does the job's service account need `secretAccessor` on the overriding secret too (confirming or refuting the "static IAM" claim in design §4 option D)?
- [ ] **U2. `claude setup-token` with two accounts.** On a machine already authenticated to one Claude account via the `claude` CLI, determine the exact sequence to mint a second `claude setup-token` value for a different account (log out/in? a profile flag? a separate machine/browser profile?). Record the exact commands, verbatim, for Task 9's skill text.
- [ ] **U3. Cloud Run jobs-per-project/region comfort.** Confirm, from GCP's own console or `gcloud run jobs list --project <sandbox> --region <region> | wc -l` against a project deliberately given several dozen job variants (synthetic, in the sandbox only), that nothing silently throttles or refuses job creation or listing at the scale the owner actually intends (ballpark: workflows × named accounts, summed across onboarded repositories).
- [ ] **U4. The real Claude Code error text for an expired/revoked oauth token.** Store a syntactically valid but revoked token as a sandbox repository's `claude-oauth-token`, launch a run, and record the exact failure text and `fugaro diagnose`'s rendering of it, for Task 9's docs.
