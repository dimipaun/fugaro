# The budget mode's vocabulary (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). It carries out **D3009** (budget mode off is
removed) and **D3010** (`observe` stays, and the migration lands on it) of
[reconciliation.md](reconciliation.md); the spec is [spec.md](spec.md). The names below live in
[`internal/policy/mode.go`](../../internal/policy/mode.go), the runnable form of the same claim is
[`internal/policy/mode_test.go`](../../internal/policy/mode_test.go), and this document is what the tasks
around them read instead of each other's code. **It is frozen: a task that finds it wrong or incomplete
raises a change request ([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather
than working around it.** **R-mode-text** (implementation-plan.md §7) is the one already anticipated.

**AB1a, AB3, AB4, AB5, AB8a, AB8b and AB10 consume this contract** and build in parallel once it merges.
**AB2** builds the validation behind it (`ValidMode` and the decoders; the plan leaves `ValidMode`
untouched here). **AB1b** and **AB15** do not consume it: each stops reading the mode instead. **I1**
deletes the constants, and `Mode.Removed` is what is left of the old word.

## The names

| Name | Is | Answers |
| --- | --- | --- |
| `policy.Mode` | `type Mode string` | the mode string a reader already holds — out of a `fugaro.yaml`, a local config, a published policy node, a job's environment, a flag or a run's record |
| `Mode.Effective() string` | method | what the string means from now on: `observe` or `enforce`; unset is `observe` |
| `Mode.Removed() bool` | method | whether the string is the word that was a mode and is not one any more |
| `policy.ModeOffRefusal` | `const string` | the one text every refusal of that word says, with its fix |

```go
policy.Mode(m).Effective()   // "" -> "observe", "off" -> "observe", "enforce" -> "enforce"
policy.Mode(m).Removed()     // "off" -> true, and every other string -> false
policy.ModeOffRefusal        // "off is not a budget mode any more: there is always a budget, and a mode
                             //  left unset is observe; write observe or enforce"
```

**Why the two answers are methods and not package-level functions.** The plan names `Effective` and
`Removed`. A package-level `func Effective(...)` cannot exist in `internal/policy`: `policy.Effective` is
already the name of the merged policy value every reader of a run's policy holds (`internal/policy/policy.go`,
taken as a parameter by `ee/ab`, `ee/governance`, `internal/cli` and the runner), and Go gives one
package-scope name to one declaration. Renaming the merged value is not this task's to do — it is AB2's and
I1's file and neither renames it — so the plan's two names stay the two answers, written on the string they
answer about. What the plan asks for is the interface, and it is this: one place answers what a mode string
means, one predicate knows the removed word, one text is every refusal of it. No change request is raised
for the receiver.

## What each answer means

1. **Unset is observe, everywhere** (R3, spec §2). `Effective` is the one place that decides it, so the
   readers that read unset as off today and the ones that read it as observe cannot disagree again (F2).
2. **`Effective` is total, and an answer of the old word is not among its answers.** The two live modes are
   themselves; every other string — the empty one, the removed word, a word nobody wrote — is `observe`, the
   mode the migration lands on and the one that refuses nothing (spec §7). Handing the string back unchanged
   would leave a reader comparing it to `observe` and behaving as off, which is the reading this vocabulary
   exists to end: the mode a run with an unrecognised mode gets is observe, as an older image's does (R6).
3. **`Removed` is the only predicate over the old word, and its comparison is the literal.** I1 deletes
   `policy.ModeOff` and its aliases `config.BudgetOff` and `localcfg.BudgetOff`, and `mode.go` compares
   against `"off"` rather than through the constant, so it is the one line that outlives them and I1's check
   — `! git grep -n -w -E 'ModeOff|BudgetOff' -- '*.go'` — finds the word nowhere in Go. **No other line
   spells the word as a literal**, and a reader that meets it asks this predicate.
4. **Nothing here validates.** A string that names no mode is refused where it was written — a flag, a
   `fugaro.yaml`, a local config, a policy node — by AB2's validation, not by `Effective`. `Effective` is
   asked once a value has been accepted, so it never has to answer "that is not a mode". Until AB2 merges,
   `ValidMode` still accepts the old word, which is what lets the tasks between C1 and AB2 keep compiling and
   running.
5. **A refusal of the old word is `ModeOffRefusal`, wrapped by the door.** The door says where the word was
   written (the flag, the file, the node) and this text says the rest, so two doors cannot word the same
   refusal differently: **AB2** refuses
   a `fugaro.yaml` that says it, **AB3** the `--budget-mode` flag and `fugaro budget`, **AB8a** a publish of
   it, **AB8b** the checkout's own file where `fugaro update` is run. The word is never quietly read as
   observe at a door that accepts user-written text; the tasks that must keep reading it are the two the
   migration owns, and they read it through `Removed` **before** validation, so a config or a node that says
   it still loads for the update that rewrites it (F22).
6. **Old records still read** (R7, AB11). `schemas/result.schema.json` keeps the word in
   `policy.effective.mode` for the records old runs wrote, and a reader of one asks `Removed`; nothing writes
   it any more.

## The one refusal text

`ModeOffRefusal` is the whole of what every refusal of the old word says:

> off is not a budget mode any more: there is always a budget, and a mode left unset is observe; write
> observe or enforce

It names the word it refuses, says what replaced it (there is always a budget, and unset is observe) and
gives the fix, in words that fit every door the word is written at: a flag's fix is to write one of the two
live modes, and a file's is the same answer — a mode left out is observe — which is why one text serves both.
The fix a door must **not** invent is a second mode named off: the refusal says the word is gone, not that it
is lower than observe.

**What it cannot say, and the change request that is written for it.** A door whose fix is something else —
a repository file whose line `fugaro update` offers to remove as a diff, a policy node whose write must
follow a database read — says that part itself, around this text. If a door's fix cannot be said in these
words at all, the contract is wrong for it and **R-mode-text** (implementation-plan.md §7) splits the text:
one refusal text per door, which reaches AB2, AB3 and AB8a. Writing that split is a change request and not a
quiet edit of `ModeOffRefusal`.

## Every reader that must move to it

A reader "moves" when it answers through `Effective` (the mode it reads), through `Removed` (the word it
refuses) or through `ModeOffRefusal` (what it says), instead of comparing the string itself. The task that
owns each reader is the one whose file list names it (implementation-plan.md §3, §4).

**AB1a — the run.** `spendFromEnv` (`internal/runner/spend.go`) reads `FUGARO_BUDGET_MODE`, where unset means
off and any other unknown word is refused; `Spend.On` (`spend.go`) asks `"observe" || "enforce"`, so a mode
nobody wrote is not on; `resolvePolicy` (`internal/runner/policy.go`) turns an unset mode into
`policy.ModeOff`; `chainLayer` (`policy.go`) reads a chain mode as off when it is `""` or `ModeOff`;
`startBudgetBackend` and `backendOn` (`internal/runner/budget_session.go`) open no session while spend is off;
and the mode-off path of the policy read and the token exchange (`internal/runner/projectpolicy.go`,
`strayTokenNoBackend` in `budget_session.go`) is entered by the job's own mode being off.

**AB3 — the CLI's readers.** The `--budget-mode` flag's validation (`internal/cli/init.go`) accepts the three
words today; `ceilingLayer`, the vertex refusal and the price rules (`internal/cli/validate.go`) treat an off
mode as no mode at all; `routingInertWhy` (`validate.go`) spells `""` as `ModeOff`; `fugaro budget`
(`internal/cli/budget.go`) reads and prints the mode; `checkTestHooks` and `localFileMode`
(`internal/cli/exec.go`) read a file's mode where an unset one is off; `doctor` and `doctor_brokering`
(`internal/cli/doctor.go`, `doctor_brokering.go`) word the switch and the keyless sentences from the mode;
`settings_cloud.go` accepts a published mode through `policy.ValidMode`.

**AB4 — the launcher.** `budgetBackendOn` and `budgetMintOn` (`internal/cli/launch_budget.go`, with
`policyMode` and `policyBudgetMode`) mint nothing with no database or an off mode; `launcherLedgers`
(`internal/cli/chain_continues.go`) reads no ledger for an installation whose budget is off; `craft`'s
`priced` flag (`internal/cli/craft.go`) is false for an off mode; `orchestrator_serve.go` composes a config
whose mode is `BudgetEnforce` for the runs it starts.

**AB5 — the job's environment.** `budgetEnv` (`internal/infra/spec.go`) writes `FUGARO_BUDGET_MODE` and the
grace only when the mode is not off, so an older image reads a missing entry as off; `projectJobEnv`
(`internal/infra/projectjobs.go`) writes no mode at all, for a job that takes its mode from the published
policy; the readiness check (`internal/infra/readiness.go`) has a note for a mode with no backend and a
refusal for a job whose mode entry is empty.

**AB8a — the published policy.** `projectpolicy.Migrate` (`internal/projectpolicy/migrate.go`) keys on the
node's version, so a version-10 node is not touched; the node's decoder
(`internal/projectpolicy/policy.go`) refuses a mode `ValidMode` does not know; `policyMigrationFor`
(`internal/cli/init_policy_migrate.go`) runs the migration; the publish refuses a node whose mode is off
while the credentials service serves a switched repository (`refuseModeOffWhileTheServiceServes`,
`internal/cli/project_cmd.go`, D2071) and requires a published budget block to name a mode.

**AB8b — the local config, and the file in the checkout.** `Config.BudgetMode` and `(*Config).validateBudget`
(`internal/localcfg/localcfg.go`) answer off for an unset mode and validate the word; the helper that prints
what will stop, the rewrite of the local config and the removal offered from a repository's `fugaro.yaml`
(`internal/cli/budget_stops.go`, `update_budget_off.go`, `load_removed_mode.go`, `init_fugaroyaml.go`,
`init_stages.go`, `update_in_flight.go`) read the word before validation, because a config or a file that
says it must still load for the update that rewrites it (F22).

**AB10 — the enterprise code.** `policyMode` (`ee/ab/admit.go`) maps an empty mode to off for its provider
check; `declare` (`ee/coordinator/cli/declare.go`) skips declaring on an off mode or with no database.

**The sides the plan does not name as consumers, which read the same answers.**
`infra.CredentialsServiceServes`'s mode argument and its callers (`internal/cli/init_credentials.go`,
`doctor_brokering.go`, `run.go`, `budget_stops.go`) are AB15's: with the argument gone the switch asks the
local config's mode, which is this vocabulary's. `ValidMode`, `modeRank` (`internal/policy/merge.go`),
`validateBudget` in `internal/config/validate.go`, `internal/localcfg/localcfg.go` and
`internal/projectpolicy/policy.go`, `modeName` (`internal/config/stage_accounts.go`),
`CheckProviderGateway` (`internal/config/providers.go`) and `schemas/fugaro.schema.json` are AB2's: they
build the validation this contract says is not here. `policy.ModeOff` and its aliases are I1's, which deletes
them and leaves `Mode.Removed` as the one place the word is written.

## What this contract does not decide

- **Which doors refuse the word and which rewrite it.** R2, R4, R7, D3022 and D3024 decide that, and each
  door's own task owns its sentence around `ModeOffRefusal`.
- **The validation behind the vocabulary** — which strings `ValidMode`, the decoders and
  `schemas/fugaro.schema.json` accept, and when they stop accepting the old word. AB2's, and it follows AB8a
  and AB8b so that a config or a node that says the word can still be read and rewritten (F22).
- **The database's own mode.** `internal/budget` has only observe and enforce (`budget.PathMode`) and never
  had the word; the effective mode of a run stays the stricter of the database's and the repository's
  (F21, D3024), and `internal/budget/evaluate.go` is unchanged by this contract.
- **The kill switch.** Its `off` (`internal/budget/kill.go`) is another word for another thing (A2).
- **The wire formats that carry a mode.** The run record, `budget show --json` and the published node keep
  their shapes and their versions (R4, R7); a field never changes meaning
  ([CLAUDE.md](../../CLAUDE.md)).
- **Where `Mode` is used as a type and where a string is converted.** Both are the same string; a reader
  converts (`policy.Mode(m)`) where it holds a plain one, and no reader needs a second name for it.

## Review

Reviewer tier **standard** (implementation-plan.md): the change is two answers and one sentence, and what it
fences is every task of the plan that reads a budget mode.
