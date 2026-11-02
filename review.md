# Review of internal/cli/models_use.go

## Scope

Focused on correctness bugs, logic errors, nil pointer dereferences, error handling gaps, and Go idiom misuse in `models_use.go`. Also read `models_write.go` for context on shared helpers. No modifications made.

---

## Finding 1: `context.Background()` instead of `cmd.Context()` -- line 61

**File:** `/work/repo/internal/cli/models_use.go`  
**Line:** 61

```go
ctx := context.Background()
```

The cobra command's `RunE` receives a context via `cmd.Context()`. Using `context.Background()` instead discards the cobra context, which carries cancellation and deadline signals attached to the process lifecycle. The `ctx` is passed to `modelsUseFile` (line 62), which passes it to `gitRead` and `settingsRepoFile` -- both accept `context.Context` and could honor cancellation.

In practice, a short-lived CLI command is unlikely to hit a timeout, but this is a Go idiom violation: command handlers should derive from `cmd.Context()`.

**Severity:** Low (unlikely to manifest in normal usage).

---

## Finding 2: `modelsUseRefuseID` does not check `isClaudeModel` -- lines 116-124

**File:** `/work/repo/internal/cli/models_use.go`  
**Lines:** 116-124

`modelsUseRefuseID` checks:
1. `strings.Contains(id, ":")` -- refuses variants.
2. `config.CheckModelID(id)` -- refuses empty, whitespace, too-long, and aliases.

It does **not** check `isClaudeModel(id)`, unlike `modelsAddRefuseID` (in `models_add.go:133`) and `modelsRmRefuseID` (in `models_rm.go`, which checks it).

**Analysis:** This is **intentional**. The comment at lines 149-153 in `modelsUseCheck` explains that Claude models reach Anthropic directly with no route and are valid for pinning. `models add` and `models rm` operate on provider route claims, where Claude models are not involved, so they refuse them. `models use` pins a model in a repository's fugaro.yaml, and Claude models are legitimate there.

**Severity:** Not a bug -- behavior is correct per design.

---

## Finding 3: Route existence check skipped for IDs without `/` -- Line 155

**File:** `/work/repo/internal/cli/models_use.go`  
**Line:** 155

```go
if !claimed && !claude && strings.Contains(id, "/") {
```

The condition that triggers the "no route serves this ID" refusal includes `strings.Contains(id, "/")`. An unclaimed, non-Claude ID that does not contain `/` (e.g., a plain name like `gpt-5`) skips this check entirely and falls through to the pricing check on line 161.

**Analysis:** Likely intentional. IDs without `/` are either plain aliases the built-in catalog may price directly, or model names that don't need routing. The pricing check (line 161) and budget check (line 175) would then catch any issues downstream.

**Severity:** Likely not a bug, but the comment doesn't explain the `/` guard. Worth confirming it's deliberate.

---

## Finding 4: Agent copy mutation is safe -- Lines 178-192

**File:** `/work/repo/internal/cli/models_use.go`  
**Lines:** 178-192

```go
agent := cfg.Agent
// switch modifies agent.Models.<role> = id ...
if ps := config.CheckProviderAuth(agent, lc.Providers); len(ps) > 0 { ... }
if ps := config.CheckProviderGateway(agent, config.EffectiveBackground(agent, lc.Providers), lc.Providers, mode); len(ps) > 0 { ... }
```

The `agent` copy is used only for validation. The `cfg.Agent` struct (and its `ModelRoles` field) contain only value types (strings, ints), so the copy is fully independent. The validation correctly verifies the proposed state without mutating the original config used by other code paths.

**Severity:** Correct -- no bug.

---

## Finding 5: `modelsUseCheck` validates the full agent state, not just the changed role -- Lines 187-192

**File:** `/work/repo/internal/cli/models_use.go`  
**Lines:** 187-192

`CheckProviderAuth` validates all model pins (coder via `ModelFor(RoleCoder)`, reviewer via `ModelFor(RoleReviewer)`), not just the role being changed. If a *different* role's existing pin is already invalid (e.g., the reviewer is pinned to a broken model, and the user is changing the coder), the command refuses the coder change with an error about the reviewer.

**Failure scenario:**
```
fugaro models use owner/repo coder valid-model
# Error: fugaro.yaml would not launch with "valid-model" pinned for coder:
#   agent.models.reviewer: broken-model: no provider serves it
```

The message names the *passed* model and role (`valid-model` for `coder`), not the broken one (`broken-model` for `reviewer`). This could confuse the user into thinking the model they chose is the problem.

**Analysis:** `CheckProviderAuth` returns `[]Problem`, each with a path like `"agent.models.reviewer"` and a message. The first problem's `String()` is used in the error. The path component clearly identifies which model is the problem, so a careful reader will understand the issue.

**Severity:** Low. The error is technically correct (the config would not launch), and the problem's descriptive text names the actual failing model/role.

---

## Finding 6: No validation that the model name exists in the project's model list before writing

**File:** `/work/repo/internal/cli/models_use.go`  
**Lines:** 79-81 (and entire function)

`modelsUseCheck` verifies:
- Route claims the model (or is Claude).
- `allow_data_to` opens the route to the repo.
- Model is priced in the table.
- Budget's `allowed_models` admits it.
- Provider auth/gateway supports it.

It does **not** verify that the model is actually registered in the project (e.g., via `models list`). A model ID that is in the built-in catalog is priced and passes all checks, but may not be a model the operator intended to offer.

**Analysis:** The `priceTable` built at line 75 includes both built-in catalog models and project config models. If the ID is in the catalog, it's priced and passes. The budget and route checks are sufficient guards. The `models_write.go` file has a `modelExists` function (not used here) that checks against `ListModels`, but that level of verification is unnecessary for `models use` because the checks here are stricter.

**Severity:** Not a bug -- the existing checks are sufficient.

---

## Finding 7: `models_use_runner` type from the system-reminder snippet does not exist in the actual file

**System-reminder code vs. actual file:** The code shown in the system reminder (with `rosemary.Role`, `rosemary.ModelName`, `rosemary.SetModelUse`) does not match the actual file on disk. The actual file imports from `github.com/dimipaun/fugaro/internal/config`, `localcfg`, `pricing`, `runner`, `yamledit`. This indicates the system-reminder snippet was from an older version or a different branch. The actual file operates on fugaro.yaml directly as a YAML edit, not through a Rosemary RPC.

The `models_write.go` file (also in the system reminder) does not exist at that import path either. The actual file on disk is the 183-line `models_write.go` that I read, containing `modelsLocalFile`, `modelsEditLocal`, `modelsSpliceRefusal`, `modelsRefuseVariant`, and the pin/warn helpers.

**Severity:** Informational -- the system-reminder code is stale. The actual code on disk was fully reviewed above.

---

## Summary

| Finding | Severity | Bug? |
|---|---|---|
| 1. `context.Background()` instead of `cmd.Context()` | Low | Minor idiom violation |
| 2. Missing `isClaudeModel` in `modelsUseRefuseID` | N/A | Intentional |
| 3. Route check skipped for IDs without `/` | Info | Likely intentional |
| 4. Agent copy mutation is safe | N/A | Correct |
| 5. Full agent validation error message context | Low | Ambiguous for user |
| 6. No `ListModels` existence check | N/A | Not needed |
| 7. System-reminder snippet is stale | Info | Stale code |

**No blocking correctness bugs found.** The one actionable finding is #1 (use `cmd.Context()` instead of `context.Background()`), which is a minor Go idiom improvement rather than a correctness issue.