# M9a.1 — Budget and Model Policy in `fugaro.yaml` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team can commit cost and model policy in `fugaro.yaml`: a per-run dollar cap, the budget mode, a run token cap and a list of allowed models. The run reads that policy from the repository's **default branch**, never from the branch it runs, and merges it with the owner's ceiling (the project config's budget, which reaches the job as env) so that the result is **never looser than the ceiling**. A branch's own file can tighten further and can never loosen. Nothing changes for a repository that sets no policy.

**Why now.** M9a put caps, mode and prices only in `~/.config/fugaro/projects/<name>.yaml`, so a team can't share them, and it left `agent.max_run_tokens` in `fugaro.yaml` read from the run's own branch, which the PR author or the agent can set to 0 (M9a's "Decisions recorded" says the docs warn about this). This plan closes that hole and gives the committed file a safe meaning.

**Spec:** [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §2.1, §5.2, §5.6–§5.8; M9a plan [2026-09-30-m9a-gateway-and-project-identity.md](2026-09-30-m9a-gateway-and-project-identity.md) (T6 pins, T7 halts, T8 `Spend`, R4 the project check, R9 pins, R11 Vertex).

## Rulings

**P1. The policy is read from the default branch (`gitops.DefaultBranch`), not from the run's branch.** This is the trust anchor `checkProject` already uses (`internal/runner/project.go`): a file on the default branch got there through whatever review the repository requires, and no file in the checkout can choose it. The alternatives fail:
- *The run's own branch* is editable by the PR author and by the agent mid-run. Clamping it against the ceiling still lets a branch spend up to the ceiling when the team committed a lower number, so the team's number would be advisory.
- *The base of a follow-up* (`readBaseConfig`) is not always the default branch (a release branch), and a first run's `ref` can be any branch. One anchor for every run is simpler to explain and test.
- *A stale branch* cut before the team lowered the cap would carry the old, looser number. Reading the default branch applies today's policy to yesterday's branch.

**P2. The merge is "the tightest wins", in three layers.** `effective = merge(merge(ceiling, defaultBranchPolicy), branchPolicy)`, where each later layer can only tighten (below). The ceiling is the job env that `fugaro init --repo` sets from the project config. A key a layer leaves out passes the earlier layer through. The branch layer exists so a PR can try a stricter setting before merging it; it can never loosen.

**P3. A branch that tries to loosen is ignored with a warning; it is never refused.** A refusal would strand every stale branch and follow-up whenever a team tightens its policy, and turn "I changed a number in my PR" into a failed run. A loosening branch value is dropped, the run goes on with the merged value, and the attempt is recorded (`result.json` `policy.ignored`, a log warning, a line in the report). The two cases that cannot be "ignored" are refused at bootstrap as `infra_error`, like a failed pin check today: a **policy that doesn't parse or is invalid on the default branch** (fail closed: the ceiling alone would silently drop the team's rules, so say so), and a **model outside the effective allow-list** (there is no looser value to fall back to).

## Keys and merge rules

Policy lives in a new top-level `budget:` block, spelled as the project config's, plus the existing `agent` keys.

| Key | Where it can be set | Merge (ceiling `C`, default-branch `D`, branch `B`) | Notes |
|---|---|---|---|
| `budget.per_run_usd` | project config (`FUGARO_MAX_RUN_USD`), `fugaro.yaml` | `min` of the positive values; 0 or unset is "no value", not "no cap" | with `enforce` and no value anywhere: `no_cap` halt, as in M9a |
| `budget.mode` | project config (`FUGARO_BUDGET_MODE`), `fugaro.yaml` | the strictest: `enforce` > `observe` > `off` | a repository may escalate (including from `off`); no layer lowers it. `vertex` + `enforce` stays refused (R11), so a committed `enforce` on a Vertex repository is an `infra_error` and `validate` says so |
| `agent.max_run_tokens` | project config (`budget.max_run_tokens`, `FUGARO_MAX_RUN_TOKENS`), `fugaro.yaml` | `min` of the positive values | applies to `oauth` too (it is the only cap `oauth` has); `oauth` stays budget-off for dollars, unchanged |
| `budget.allowed_models` | project config (`budget.allowed_models`, `FUGARO_ALLOWED_MODELS`, comma-joined), `fugaro.yaml` | intersection of the lists that are set; an empty list from a layer is a validation error (it would forbid every model) | explicit IDs only, no aliases |
| `agent.models.*`, `agent.model`, `max_output_tokens.*` | `fugaro.yaml` (the run's own branch, as today) | each chosen model must be in the effective allow-list when there is one; output limits `min` with `D` when `D` sets them | choosing models is the repository's job (M9a ruling "repositories choose models"); the list only bounds the choice |
| `model_prices` | project config only | not settable in `fugaro.yaml` | a repository that could set prices could set them to zero |

**Out of scope here (M9b):** per-day caps. They live in RTDB, are owner-set, and M9b takes `min(RTDB, committed policy)` the same way; the `budget:` block reserves the name `per_day_usd` and M9a.1 refuses it ("not supported yet") so a file written now can't silently do nothing later. **Not changed:** `agent.max_budget_usd` (per stage, notional, passed to Claude Code); the gateway; pricing; `halted` and its reasons (a clamped cap halts as `run_cap`, `token_cap` as before).

## Behaviour

1. **Bootstrap.** After the project check and before the lock, `policy.go` reads `fugaro.yaml` at `origin/<default>` (the fetch and `ShowFile` `checkProject` already does, now shared and cached on the run, and done for follow-ups too, which return early today), decodes only `budget:`, `agent.max_run_tokens` and `agent.max_output_tokens` leniently (unknown keys elsewhere are the default branch's business), and validates them. It then builds `r.spend` (replacing `r.d.Spend` in `gatewayOn`, `startGateway`, `checkBudget`) from the three layers. A default-branch file without those keys is the ceiling alone.
2. **The branch layer** is the `cfg` the run already parses (checkout for a first run, base for a follow-up). Its `budget:` and token keys go through the same merge; each looser value is recorded in `Policy.Ignored` as `{key, branch_value, effective, source: "default-branch"|"ceiling"}`.
3. **Models.** `CheckPins` runs against the effective prices as today; a new `CheckAllowed(agent, allowed)` problem names the role, the model and the layer that set the list. The default branch's own `agent.models` is not imposed; only the list is.
4. **Events.** Policy is logged once at bootstrap (`policy: per_run_usd=… (ceiling 5, repo 2) mode=enforce …`), each ignored value as a `Warn`, and `result.json` gets an optional `policy` object: `{effective: {per_run_usd, mode, max_run_tokens, allowed_models}, sources: {<key>: "ceiling"|"default-branch"|"branch"}, ignored: [...]}`. The report (`internal/runner/report.go`) adds one line under the cost when `ignored` is not empty. A run with no policy anywhere writes no `policy` object, so existing records are unchanged.
5. **`fugaro validate`** (`internal/cli/validate.go`, `budgetProblems`) validates the `budget:` block's shape always, and, with a selected project config, simulates the merge (ceiling = the project config, branch = the file in hand) and reports each key the file would loosen as a **warning** (`budget.per_run_usd: 20 is above the project's ceiling of 5; the runner will use 5`) and each model outside the allow-list as a **problem**, with the same `Problem` paths as the schema. It cannot know the default branch's file from a laptop checkout of a branch; it says so in one line when the current branch isn't the default.
6. **Project config** (`internal/localcfg`) gains `budget.max_run_tokens` and `budget.allowed_models`; `init --repo` passes them as `FUGARO_MAX_RUN_TOKENS` and `FUGARO_ALLOWED_MODELS` (same code path as `FUGARO_MAX_RUN_USD`, `internal/infra/spec.go`). Editing the project config and re-running `init --repo` raises or lowers the ceiling, as in M9a.

## Review Focus

1. **Policy read from the run's branch, or from a ref the agent can move.** `TestPolicyReadFromDefaultBranchNotRef` (the run's branch raises the cap to 1000, the default branch says 2: the run uses 2), `TestPolicyFollowUpReadsDefaultBranch` (base is a release branch with a looser file), `TestPolicyDefaultBranchFetched` (the fetch uses the same `origin/<default>` as the project check, one fetch per run), `TestPolicyAgentEditMidRunIgnored` (the agent edits `fugaro.yaml` during implement; review stage's limits are unchanged, since policy is computed once at bootstrap).
2. **A looser value that wins.** `TestMergeNeverLoosens` (table: ceiling x default x branch for every key, including unset layers and the 0 meaning "no value"), `TestMergePerRunMin`, `TestMergeModeOrder` (a branch `off` under an `observe` ceiling stays `observe`), `TestBranchRaisesCapIsClamped` (hermetic run with the ceiling $5 and a branch asking $500: the gateway's cap is $5, a call that would cross it halts with `run_cap`), `TestBranchZeroTokenCapIgnored` (closes the M9a hole: a branch's `max_run_tokens: 0` under a ceiling of 1M).
3. **A model outside the list.** `TestAllowedModelsIntersection`, `TestModelOutsideAllowListInfraError` (names role, model and the list's source; before the lock), `TestAllowedModelsAliasRefused`, `TestEmptyAllowListInvalid`.
4. **A fail-open default branch.** `TestDefaultBranchPolicyInvalidFailsClosed` (an unparsable `budget:` on the default branch is an `infra_error`, not "no policy"), `TestDefaultBranchWithoutFileKeepsProjectCheckError` (unchanged message), `TestDefaultBranchPolicyUnknownKeyElsewhereOK`.
5. **Silent change in what a repository without policy does.** `TestNoPolicyIsM9aBehaviour` (golden: the same run with no `budget:` key anywhere produces a byte-identical `result.json` and report to M9a), `TestOAuthStaysBudgetOff` (policy `enforce` on an `oauth` workflow starts no gateway; the token cap still applies).
6. **Vertex + a committed `enforce`.** `TestPolicyVertexEnforceRefused` (runner), `TestValidateRefusesVertexEnforceFromFile` (the existing refusal now also covers the committed value).
7. **Prices leaking into the file.** `TestModelPricesNotAllowedInFugaroYaml` (strict decoding rejects `budget.model_prices`; the schema has no such property).
8. **Surprising a tightening team.** `TestIgnoredValuesRecordedAndReported` (`policy.ignored`, the log warning and the report line, each once per key) and `TestStaleBranchLooserThanDefault` (a branch cut before the team tightened: runs, is clamped, is told).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/config.go`, `validate.go`, `example.yaml`, `schemas/fugaro.schema.json`, `testdata/config/**` | `Budget` block, `allowed_models`, `per_day_usd` refused, shape validation; the lenient `PolicyOf(data)` | T1 |
| `internal/policy/` (new: `policy.go`, `merge.go`) | `Layer`, `Merge`, `Effective`, `Ignored`; pure, no I/O | T2 |
| `internal/localcfg/localcfg.go`, `internal/infra/spec.go` | `budget.max_run_tokens`, `budget.allowed_models`; the two new env variables | T3 |
| `internal/runner/spend.go`, `policy.go` (new), `project.go`, `followup.go`, `gateway.go`, `runner.go`, `report.go`, `cost.go` | read the default branch once; `r.spend`; the merge at bootstrap; `CheckAllowed`; events and report line | T4, T5 |
| `internal/runstore/runstore.go`, `schemas/result.schema.json`, `schemas/schemas_test.go` | optional `policy` object | T5 |
| `internal/cli/validate.go`, `internal/cli/init.go` | `validate` simulation and warnings; `init --repo` env | T3, T6 |
| `docs/design/v1.md`, `docs/gcp-setup.md`, `README.md` config section, `plugin/skills/onboard/SKILL.md` | docs; the onboard skill suggests a `budget:` block | T7 |
| `internal/e2e/cloud_test.go` | hermetic cloud run with a clamped branch | T5 |

## Tasks

T1 and T2 are independent and may run in parallel. T3 needs T1's types, T4 needs T2 and T3, T5 needs T4, T6 needs T1 to T3, T7 is last. Sizes: S under half a day, M about a day.

### Task 1 (S): The `budget:` block in `fugaro.yaml`

**Files:** `internal/config/config.go`, `validate.go`, `example.yaml`, `config_test.go`, `internal/config/policy.go` (new, `PolicyOf`), `schemas/fugaro.schema.json`, `testdata/config/valid/budget-policy.yaml`, `testdata/config/invalid/budget-{mode,per-run-negative,per-day,allowed-empty,allowed-alias,prices}.yaml`.

- `Config.Budget *Budget` with `Mode`, `PerRunUSD`, `AllowedModels []string`; `PerDayUSD` accepted by the decoder only to produce the "not supported yet (M9b)" problem. `agent.max_run_tokens` and `max_output_tokens` are unchanged.
- Validation reuses the project config's rules for mode and cap range (enforce needs `per_run_usd` > 0 only after the merge, so not here: a repository may set `enforce` and leave the cap to the owner). Models: explicit IDs, the same character rules as `agent.models`.
- `PolicyOf(data []byte) (Policy, error)` decodes only the policy keys, leniently (like `ProjectOf`), and validates them.
- [ ] Failing tests first: `TestBudgetBlockValid`, `TestBudgetBlockInvalid` (corpus), `TestPerDayRefused`, `TestPolicyOfIgnoresOtherKeys`, `TestPolicyOfInvalidIsError`, `TestSchemaBudgetBlock`, `TestExampleIsValid`.
- [ ] `go test ./internal/config/ ./schemas/`; implement; `go test -race ./...`.
- [ ] Commit: `config: a budget block in fugaro.yaml (mode, per-run cap, allowed models)`

### Task 2 (M): The merge

**Files:** `internal/policy/policy.go`, `merge.go`, `merge_test.go` (new).

```go
type Layer struct {
	Mode          string   // "" = unset
	PerRunUSD     float64  // 0 = unset
	MaxRunTokens  int64    // 0 = unset
	AllowedModels []string // nil = unset
}
type Effective struct {
	Layer
	Sources map[string]string // key -> "ceiling" | "default-branch" | "branch"
	Ignored []Ignored         // looser values dropped
}
type Ignored struct{ Key, Value, Effective, Source string }
// Merge applies layers in order (ceiling first); a later layer may only tighten.
func Merge(ceiling Layer, tighten ...Layer) Effective
```

- Rules exactly as in the table; `Ignored` is filled only for values a later layer set that don't tighten (equal values are not "ignored"). The ceiling is never recorded as ignored.
- [ ] Failing tests first: `TestMergeNeverLoosens` (property over random layers: for every key the result is at least as tight as every layer's value that is set), the table tests, `TestMergeIgnoredRecords`, `TestAllowedIntersectionOrderStable`.
- [ ] Commit: `policy: merge budget layers so that only tightening wins`

### Task 3 (S): The ceiling carries the new keys

**Files:** `internal/localcfg/localcfg.go`, `localcfg_test.go`, `internal/infra/spec.go`, `spec_test.go`, `internal/cli/init.go`, `init_test.go`, `internal/runner/spend.go`, `spend_test.go`.

- `localcfg.Budget` gains `MaxRunTokens int64` and `AllowedModels []string` (validated like `fugaro.yaml`'s); `infra` writes `FUGARO_MAX_RUN_TOKENS` (only when > 0) and `FUGARO_ALLOWED_MODELS` (only when set) beside the M9a budget env, for every workflow including `oauth`'s (the token cap applies there; `FUGARO_BUDGET_MODE` handling is unchanged).
- `SpendFromEnv` returns them as `Spend.MaxRunTokens` and `Spend.AllowedModels`; a malformed value is an error, as for the cap.
- [ ] Failing tests first: `TestBudgetTokensAndModelsValidation`, `TestWorkflowEnvPolicyKeys`, `TestWorkflowEnvOAuthGetsTokenCap`, `TestCheckJobHasNoBudgetEnv`, `TestSpendFromEnvPolicyKeys`, `TestInitRepoPassesPolicyEnv`.
- [ ] Commit: `budget: a token cap and an allow-list of models in the project config's ceiling`

### Task 4 (M): Read the default branch and merge at bootstrap

**Files:** `internal/runner/policy.go` (new), `policy_test.go`, `project.go`, `followup.go`, `runner.go`, `gateway.go`.

- Extract the default-branch read from `checkProject` into `r.defaultBranchFile(ctx) ([]byte, string, error)`, cached on the run, used by both the project check (now also for follow-ups: it still only checks `project:` where it did) and `policy.go`. `r.spend` and `r.policy` are set after `cfg` is parsed and before `checkBudget`; `checkBudget`, `gatewayOn`, `startGateway` and `capReached` read them instead of `r.d.Spend` and `cfg.Agent.MaxRunTokens`.
- The effective `max_run_tokens` is stored on `r.policy` so a branch edit can't change it later. When `r.d.Project == ""` (local runs, no cloud) the default-branch read is skipped and the layers are ceiling and branch only, with a log line, the same posture as the project check.
- `CheckAllowed` in `internal/config/pins.go`; `checkBudget` calls it for every role and the background model when a list is in force.
- [ ] Failing tests first (runner, hermetic fake git provider): every Review Focus 1, 3 and 4 test above.
- [ ] Commit: `runner: read budget policy from the default branch and merge it with the ceiling`

### Task 5 (M): Events, record, report and the end-to-end clamp

**Files:** `internal/runner/policy.go`, `report.go`, `runner.go`, `internal/runstore/runstore.go`, `schemas/result.schema.json`, `schemas/schemas_test.go`, `internal/e2e/cloud_test.go`.

- `Record.Policy *PolicyRecord` (`effective`, `sources`, `ignored`), set only when any layer set something; the schema adds it as optional. The report line: `Policy: 2 values from fugaro.yaml on this branch were looser than the project's limits and were ignored (budget.per_run_usd 500 -> 5)`.
- [ ] Failing tests first: `TestIgnoredValuesRecordedAndReported`, `TestStaleBranchLooserThanDefault`, `TestNoPolicyIsM9aBehaviour`, `TestBranchRaisesCapIsClamped` and `TestBranchZeroTokenCapIgnored` (runner level with the fake upstream), `TestCloudBranchRaisesCapIsClamped` (e2e), `TestPolicyRecordSchema`, `TestOAuthStaysBudgetOff`, `TestPolicyVertexEnforceRefused`.
- [ ] Commit: `runner: record the effective policy and what was ignored; clamp a branch's looser caps`

### Task 6 (S): `fugaro validate` and `init --repo`

**Files:** `internal/cli/validate.go`, `validate_test.go`, `internal/cli/init.go`.

- `budgetProblems` simulates the merge as in "Behaviour" 5, with warnings through the existing warnings channel and `--json`'s `problems` keeping only real problems. `init --repo` prints the ceiling it is about to apply and, when the checkout's `fugaro.yaml` already has a `budget:` block, which keys would be clamped.
- [ ] Failing tests first: `TestValidateWarnsOnLooserThanCeiling`, `TestValidateModelOutsideAllowListIsProblem`, `TestValidateWithoutProjectConfigChecksShapeOnly`, `TestValidateNotOnDefaultBranchSaysSo`, `TestValidateRefusesVertexEnforceFromFile`, `TestInitRepoShowsCeilingAndClamps`.
- [ ] Commit: `validate: show what the project's ceiling would clamp in fugaro.yaml's budget block`

### Task 7 (S): Docs

**Files:** `docs/design/v1.md` (the trust boundary §6.1 and the config reference), `docs/design/m9-budget-and-dashboard.md` (§5.6: the committed policy and the `min` rule), `docs/gcp-setup.md`, `README.md`, `plugin/skills/onboard/SKILL.md`, `internal/config/example.yaml`.

- State plainly: the project config is the ceiling; `fugaro.yaml` on the default branch tightens it; a branch can only tighten further; changing a committed limit takes a merge to the default branch; raising the ceiling takes `init --repo`. Replace M9a's "a repository writer can lift `max_run_tokens`" with this. Prices are owner-only.
- [ ] `go test ./...` (docs examples are in the corpus). Commit: `docs: committed budget policy and who can loosen what`

## Migration

None. A repository without `budget:` keys and a project config without the two new keys behave as after M9a, byte for byte (`TestNoPolicyIsM9aBehaviour`). Old job env is valid: the new variables are optional. To use the ceiling's new keys, edit the project config and re-run `fugaro init --repo` (no backward-compat code, per the project's rule). `oauth` repositories stay budget-off for dollars; the token cap now also comes from the ceiling, and a branch can no longer lift it.

## Open questions for the user

1. **`budget.mode` from `off`.** The plan lets a committed `enforce` switch the gateway on under an `off` ceiling (a team's opt-in). The stricter alternative is that only the ceiling turns the gateway on. *Default:* allow it, since the file can only add safety.
2. **Fail closed on an invalid default-branch policy** blocks all runs until fixed on the default branch. *Default:* yes; the message names the file, the key and the branch.
