# Managed flag ergonomics: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's explicit go.**

It builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). The answers that shaped it:

- **The owner's principle of 2026-11-21:** Fugaro deals only with managed projects, and a command that would write to an unmanaged one stops. This rules TN1 and D2217.
- **D2210:** the wizard gets a budget question, suggesting 100.
- **D2211:** the currency defaults to USD.
- **D2212** (not the recommendation): the amount defaults to 100 everywhere.
- **D2213:** `managed flag`'s project ID becomes optional.
- **D2214:** `setup --managed` is dropped.
- **D2215:** an explicit `--budget` rewrites a recorded amount, behind the fences.
- **D2216:** the billing account defaults to the linked one.
- **D2217:** every writing command refuses an unmanaged project.
- **TN1:** `update` never marks a project; it stops.
- **R1–R5:** the reconciliation's resolutions.

## Conventions every task follows

- **`CLAUDE.md` throughout.** No prompt and no confirmation is added. The budget question is a value with a default, so with no terminal the default applies and nothing waits. Comments explain why. Tests are hermetic (`internal/gcpfake`, `t.TempDir()`), with no secret-shaped literal, and runs are targeted.
- **The generated CLI reference.** Every task that changes a flag or its help regenerates `website/content/cli-reference.md` with `go run ./ee/clidoc` and runs `go test ./internal/cli/ -run 'Docs|CliRef' -count=1`. The file is never hand-edited. A merge conflict in it is resolved by regenerating it on the merged tree.
- **Docs are tested.** A task that changes a pinned sentence changes the pin in the same commit.
- **Each task drops `--managed` from the files it owns.** T2c sweeps only what no other task owns.
- **Plugin.** Removing a flag a skill names edits the skill in the same pull request.
- **Boundaries.**
  - *Always:* tests before the commit, `gofmt -l .` prints nothing, `go vet ./...`.
  - *Ask first:* any change to the marker's shape (none is planned, A3), and any new dependency.
  - *Never:* an agent runs `setup`, `update` or `managed flag` against a real project. Checks 860 and 861 are the owner's.
- **Fixtures (U3).** A task whose new stop breaks existing tests that run against an unmanaged fake makes managed the default of the shared fixture, and it keeps one explicit unmanaged case per refusal. If that reaches more than five files, the run reports it, and the task is split before it goes on.
- **Rigor.** The repository's default is `startup`, with no area rules. Tasks that write the managed marker, the managed fence, `plugin/skills/**` or `SECURITY.md` get the opus reviewer tier.
- **Decoupling.** `planning.decoupling` is on. The pass ran; no edge was worth breaking (below).

## Global constraints and review focus

1. **A default never overwrites a recorded amount** (D2215). Pinned by T3's `TestUpdateKeepsARecordedAmountWithoutBudget` and T4's `TestManagedFlagDefaultDoesNotRewrite`.
2. **The marker's shape does not change** (A3). Pinned by T1's `TestRewriteBudgetWritesVersion4`.
3. **No command waits for input.** Pinned by T2b's `TestSetupBudgetQuestionNoTerminalTakesDefault`.
4. **Every marker write stands behind the agent-session and run-process refusals**: the rewrite on `update`, and `managed flag` with or without an ID. Pinned by T3's `TestUpdateRewriteRefusedInAgentSession` and `TestUpdateRewriteRefusedInsideAFugaroRun`, and by T4's `TestManagedFlagNoArgRefusedInAgentSession` and `TestManagedFlagNoArgRefusedInsideAFugaroRun`.
5. **A stop writes nothing, and is safe with runs in flight.** Pinned by T3 extending `TestUpdateLeavesARunInFlightRunning` (an unmanaged installation with a run in flight is refused, and the run's records are untouched) and by T7's `TestFencedCommandWritesNothingOnUnmanaged`.
6. **The fence's classification is complete.** Pinned by T7's `TestEveryWritingCommandIsClassified`.
7. **The budget alert survives a plain update** (R5). Pinned by T0's `TestUpdateCarriesTheBudget`.

## Slices and tasks

### Slice A: foundations (inert alone)

### T0: carry the budget block between runs
- Acceptance:
  - A run given no budget flags carries the previous run's `budget` Terraform variable unchanged, so a plain `update` after a run that created the alert plans no change to `google_billing_budget`.
  - An explicit `--budget`, `--budget-currency` and `--billing-account` replace it.
  - `flagHints`' sentence for the budget no longer says "nothing records".
- Files: `internal/infra/tfvars.go`, `internal/infra/tfvars_test.go`, `internal/infra/installation.go`, `internal/cli/init.go` (the two call sites beside `CarriedManagedDeployerScoped`), `internal/cli/init_budget_test.go`.
- Interfaces: produces `infra.CarriedBudget(vars, prev)`, consumed inside this task.
- Tests: `TestCarriedBudgetKeepsPrevious`, `TestCarriedBudgetExplicitReplaces`, `TestUpdateCarriesTheBudget`.
- Command: `go test ./internal/infra/ -run TestCarriedBudget -count=1 && go test ./internal/cli/ -run 'TestUpdateCarriesTheBudget' -count=1`.
- Red step: `TestUpdateCarriesTheBudget` fails: the plan deletes `module.installation.google_billing_budget.this[0]`.
- Depends on: none.
- Commit: `infra: carry the budget block from the previous run (R5)`.
- Reviewer tier: standard.

### T1: the budget defaults, and rewriting a recorded amount
- Acceptance:
  - `internal/infra/managed/defaults.go` exports `DefaultBudgetAmount = 100` and `DefaultBudgetCurrency = "USD"`, and nothing else in the tree spells either figure.
  - `managed.RewriteBudget(ctx, s, bucket, project, number, budget) (old Budget, err error)` writes this project's marker at version 4 with the new figures, conditioned on the generation it read. It accepts versions 2, 3 and 4 (F15), returns the old figures, and writes nothing when they are equal.
  - It refuses, with what is there: a version-1 creation fact (that object is `WriteFlag`'s), an unknown version, another project's object, a bucket that fails `proveRunsBucket`, and a budget that fails `Budget.Check`.
- Files: `internal/infra/managed/defaults.go` (new), `internal/infra/managed/marker.go`, `internal/infra/managed/marker_test.go`.
- Interfaces: produces the constants (T2a, T2b, T4) and `RewriteBudget` (T3, T4).
- Tests: `TestRewriteBudgetReplacesAmount`, `TestRewriteBudgetFromVersion2And3`, `TestRewriteBudgetEqualIsNoWrite`, `TestRewriteBudgetRefusesVersion1`, `TestRewriteBudgetRefusesAnotherProject`, `TestRewriteBudgetLosesTheRace`, `TestRewriteBudgetWritesVersion4`, `TestDefaultBudgetPassesCheck`.
- Command: `go test ./internal/infra/managed/ -run 'TestRewriteBudget|TestDefaultBudget' -count=1`.
- Red step: `TestRewriteBudgetReplacesAmount` fails to compile.
- Depends on: none.
- Commit: `managed: the budget defaults, and rewriting a recorded amount under a generation precondition (D2212, D2215)`.
- Reviewer tier: opus (the marker).

### Slice B: setup asks for, defaults and records the budget

### T2a: setup's budget defaults and the creation fact
- Acceptance:
  - On a creating run, unset `--budget` takes `DefaultBudgetAmount` and unset `--budget-currency` takes `DefaultBudgetCurrency`.
  - Unset `--billing-account` takes the account the run links (D2216).
  - Every creating run records its budget in the creation fact, whether or not `--managed` was passed.
  - With no account linked, the run creates no alert and prints the one command that adds it.
  - The trio check reads explicit flags only (R2).
  - `--answers-file`'s help lists members through `setupAnswerKeys()` and names no count, so T2b changes no `init.go` line.
- Files: `internal/cli/init.go`, `internal/cli/init_budget_test.go`, `internal/cli/init_managed_test.go` (its version-1 expectation for a creation without `--managed`), `website/content/cli-reference.md` (generated).
- Interfaces: consumes T1's constants and T0's carry; produces the creation-fact behaviour (T2c).
- Tests: `TestSetupCreatingRunRecordsDefaultBudget`, `TestSetupBillingAccountDefaultsToLinked`, `TestSetupNoLinkedAccountRecordsAmountAndPrintsCommand`, `TestSetupExplicitBudgetWins`.
- Command: `go test ./internal/cli/ -run 'TestSetup(CreatingRun|BillingAccount|NoLinkedAccount|ExplicitBudget)|Budget|CliRef' -count=1`.
- Red step: `TestSetupCreatingRunRecordsDefaultBudget` fails: a creation without `--managed` writes the version-1 fact.
- Depends on: T0 (the same file `init.go`, and the carry the default alert needs); T1 (the constants).
- Commit: `setup: a creating run records its budget, 100 USD unless told otherwise (D2211, D2212, D2216)`.
- Reviewer tier: opus (the creation fact).

### T2b: the wizard's budget question and the answers-file members
- Acceptance:
  - `setupQuestions` gains a budget row (flag `--budget`), asked by the wizard stage on a creating run after rigor. Its label says it is the alert threshold, not a cap.
  - Enter takes `DefaultBudgetAmount`.
  - With no terminal, the default applies.
  - The answers file accepts optional `budget` and `budget_currency` members, with the version unchanged (R4), and the schema documents them.
- Files: `internal/cli/setup.go`, `internal/cli/wizard_stage.go`, `internal/cli/setup_answers.go`, `schemas/setup-answers.schema.json`, `internal/cli/setup_wizard_test.go`, `internal/cli/setup_answers_test.go`, `website/content/cli-reference.md` (generated). Six hand-edited files: the row, its asker, its reader and its schema are one change.
- Interfaces: consumes T1's constant; produces the members (T6).
- Tests: `TestSetupBudgetQuestionEnterTakesDefault`, `TestSetupBudgetQuestionNoTerminalTakesDefault`, `TestSetupAnswersBudgetMembers`, `TestEveryWizardQuestionHasAFlag`, `TestSetupAnswersSchema`.
- Command: `go test ./internal/cli/ -run 'TestSetupBudgetQuestion|TestSetupAnswers|TestEveryWizardQuestionHasAFlag|CliRef' -count=1`.
- Red step: `TestSetupAnswersBudgetMembers` fails: `budget` is an unknown member.
- Depends on: T1 (the constant).
- Commit: `setup: the wizard asks for the monthly budget, 100 by default (D2210)`.
- Reviewer tier: standard.

### Slice C: only managed projects

### T3: update stops on an unmanaged installation; an explicit budget rewrites
- Acceptance:
  - `fugaro update` on an installation whose marks do not answer managed refuses through `managedRequired`, before any write. The refusal names `fugaro managed flag`. `--plan-only` and `--check` still read.
  - `selfFlagUnmanaged`'s marking is removed (TN1; D1553 amended).
  - On a managed installation, an explicit `--budget` rewrites the recorded amount through `RewriteBudget` and prints the old and new amounts. No flag means no write (D2215).
  - The rewrite is refused in a coding agent's session and inside a Fugaro run, before it writes.
  - With runs in flight, the stop asks none to finish and touches nothing they hold.
  - An installation at the previous release that is unmanaged is refused with the command. After the fake's `managed flag`, it converges.
  - `budgetFlagHelp`'s update-door sentence and `docs/design/update-in-flight.md` say this.
  - The stale "refused by every managed apply" comment (F14) and the file's `--managed` mention go.
- Files: `internal/cli/update_selfflag.go`, `internal/cli/update_selfflag_test.go`, `internal/cli/update_in_flight_test.go`, `internal/cli/update_converge_test.go`, `internal/cli/init.go` (`budgetFlagHelp` and the stop's call), `docs/design/update-in-flight.md`, `website/content/cli-reference.md` (generated), plus the update test fixture (U3).
- Interfaces: consumes `RewriteBudget` (T1) and `managedRequired` (as it is today).
- Tests: `TestUpdateRefusesUnmanaged`, `TestUpdateExplicitBudgetRewrites`, `TestUpdateKeepsARecordedAmountWithoutBudget`, `TestUpdateRewriteRefusedInAgentSession`, `TestUpdateRewriteRefusedInsideAFugaroRun`, `TestUpdateConvergesAfterFlag`, `TestUpdateLeavesARunInFlightRunning` (extended).
- Command: `go test ./internal/cli/ -run 'TestUpdate(RefusesUnmanaged|ExplicitBudget|KeepsARecorded|Rewrite|ConvergesAfterFlag|LeavesARunInFlight)|CliRef|Docs' -count=1`.
- Red step: `TestUpdateRefusesUnmanaged` fails: the update converges an unmanaged installation.
- Depends on: T1 (behaviour: the rewrite); T2a (the same file `init.go`).
- Commit: `update: stop on an unmanaged installation; an explicit --budget rewrites the recorded amount (TN1, D2215)`.
- Reviewer tier: opus (the managed marker and fence).

### T4: managed flag takes the project from the installation and defaults the budget
- Acceptance:
  - `fugaro managed flag [PROJECT_ID]`: omitted, the selected installation's project, named in `managedFlagStatement` before the write; given, it must match (D2213).
  - `--budget` and `--budget-currency` default to T1's constants.
  - On a project already recording an amount, an explicit `--budget` rewrites through `RewriteBudget`; the default does not (D2215).
  - The agent and run-process refusals hold with and without the argument.
  - The help loses "takes no flag at all", the D64 sentence and the stale claim (F8, F14), and its `--managed` mentions.
  - The gate's warning in `internal/managed/budget.go` names plain `fugaro managed flag`.
- Files: `internal/cli/managed_flag.go`, `internal/cli/managed_flag_test.go`, `internal/managed/budget.go`, `internal/managed/budget_test.go`, `website/content/cli-reference.md` (generated).
- Interfaces: consumes T1.
- Tests: `TestManagedFlagNoArgUsesInstallation`, `TestManagedFlagArgMustMatch`, `TestManagedFlagDefaultsBudget`, `TestManagedFlagExplicitRewrites`, `TestManagedFlagDefaultDoesNotRewrite`, `TestManagedFlagNoArgRefusedInAgentSession`, `TestManagedFlagNoArgRefusedInsideAFugaroRun`.
- Command: `go test ./internal/cli/ -run 'TestManagedFlag|CliRef' -count=1 && go test ./internal/managed/ -count=1`.
- Red step: `TestManagedFlagNoArgUsesInstallation` fails on `cobra.ExactArgs(1)`.
- Depends on: T1 (the constants and the rewrite).
- Commit: `managed flag: the project defaults to the installation's, the budget to 100 USD (D2213, D2212, D2211)`.
- Reviewer tier: opus (the marker).

### T7: every writing command refuses an unmanaged project
- Acceptance:
  - The fence moves from a one-row table to a classification of the whole tree. Every command that writes to a project is either *fenced*, refused through `managedRequired` before its first write once its installation is resolved, or *exempt* with a one-line reason.
  - The exemptions are setup's creating run, `managed flag`, and read-only commands and modes (A5).
  - `update` is classified as fenced in its own engine (T3).
  - `TestEveryWritingCommandIsClassified` walks the cobra tree and fails on any command that registers cloud flags (`addCloudFlags`) and is in neither list.
  - `managedFlagCommand` spells plain `fugaro managed flag`.
- Files: `internal/cli/root.go`, `internal/cli/managed_require.go`, `internal/cli/cloud.go`, `internal/cli/managed_require_test.go`, plus the shared command fixture (U3).
- Interfaces: consumes T4's behaviour (the sentence names a command that must work bare).
- Tests: `TestEveryWritingCommandIsClassified`, `TestFencedCommandRefusesUnmanaged` (table-driven over the fenced list), `TestFencedCommandWritesNothingOnUnmanaged`, `TestExemptCommandsRunOnUnmanaged`.
- Command: `go test ./internal/cli/ -run 'TestEveryWritingCommandIsClassified|TestFencedCommand|TestExemptCommands|TestRestore' -count=1`.
- Red step: `TestFencedCommandRefusesUnmanaged` fails for `fugaro image refresh` on an unmanaged fake.
- Depends on: T4 (behaviour: the bare command the refusal names).
- Commit: `cli: every command that writes to a project refuses an unmanaged one (D2217)`.
- Reviewer tier: opus (the managed fence).

### Slice D: --managed goes, and the words follow

### T2c: drop `fugaro setup --managed`
- Acceptance:
  - Both doors refuse `--managed` before anything is read, in one "gone" sentence (R3, D2214).
  - The `--managed` cases in `checkCreateProject` go, with their comment and the "types its ID" refusal.
  - `restore`'s `createArgs` stops passing it (R1).
  - The help of `setup` and `managed unflag` and the update door's refusal rows stop naming it.
  - Every test that passes it in argv drops it, apart from those owned by T2a, T2b, T3 and T4.
  - The setup skill stops naming it (a minimal edit; T5 rewrites the prose).
- Files: `internal/cli/init.go`, `internal/cli/init_project.go`, `internal/cli/restore.go`, `internal/cli/setup.go`, `internal/cli/update.go`, `internal/cli/managed_unflag.go`, `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/managed.md`, `website/content/cli-reference.md` (generated), and the remaining test files from `git grep -l -e '--managed\([^-a-z_]\|$\)' -- 'internal/**/*_test.go'` (argv only). **Over the five-file guideline on purpose:** a flag and its callers cannot be split across pull requests without a red tree in between.
- Interfaces: consumes T2a's creation-fact behaviour.
- Tests: `TestSetupManagedFlagIsGone`, `TestUpdateManagedFlagIsGone`, `TestRestoreCreateArgs`, the plugin tests, the Docs filter.
- Command: `go test ./internal/cli/ -run 'ManagedFlagIsGone|TestRestore|Docs|CliRef' -count=1 && go test ./internal/e2e/ ./internal/managed/ ./internal/infra/managed/ -count=1 && go test ./plugin/ -count=1`.
- Red step: `TestSetupManagedFlagIsGone` fails: the flag is accepted.
- Depends on: T2a (behaviour: dropping the flag first would stop creations recording an amount; and `init.go`); T3 (`init.go`); T2b (`setup.go` and `setup_wizard_test.go`); T4 and T7 (test files their stops and defaults touch).
- Commit: `setup: --managed is gone; every creating run records its budget (D2214)`.
- Reviewer tier: opus (plugin skills).

### T5: the skills, SECURITY.md and the website say what ships
- Acceptance:
  - `plugin/skills/setup/reference/managed.md` describes the defaults, the optional ID, the rewrite, and the stop on an unmanaged project, with `fugaro managed flag` as the one way out.
  - `SECURITY.md` and `website/content/security.md` say that a command which writes refuses an unmanaged project. They drop "a project that is not managed keeps the confirmations", "an installation that never asks for managed …" and `--create-project --managed` as the creating command. The pins move with them.
- Files: `plugin/skills/setup/reference/managed.md`, `SECURITY.md`, `website/content/security.md`, `internal/cli/docs_security_test.go`, `internal/cli/docs_managed_deployer_test.go`.
- Interfaces: describes T2c, T3, T4 and T7.
- Tests: the Docs filter, the plugin tests, the website's checks.
- Command: `go test ./internal/cli/ -run Docs -count=1 && go test ./plugin/ -count=1 && (cd website && npm run check)`.
- Red step: the changed pins in `docs_security_test.go` fail against the old text.
- Depends on: T2c (the same skill file); T3, T4 and T7 (behaviour described).
- Commit: `docs: only managed projects; the budget defaults and the optional project ID`.
- Reviewer tier: opus (plugin skills, `SECURITY.md`).

### Slice E: the console asks the same question

### T6: the console's first-run wizard asks for the budget
- Acceptance:
  - The onboarding form gains a budget field, prefilled with 100, saying it is the alert threshold.
  - `wizardrun.ts` sends `budget` only to a CLI at or after the first release that ships T2b (`console/src/shared/version.ts`), because an older CLI refuses the member.
  - An empty field sends nothing.
- Files: `console/src/main/wizardrun.ts`, `console/src/renderer/entry.ts`, `console/src/shared/version.ts`, `console/test/unit/onboarding.test.ts`.
- Interfaces: consumes T2b's members.
- Tests: the onboarding unit tests for the field and the version gate.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 onboarding`.
- Red step: the test expecting `budget` in the document fails.
- Depends on: T2b, **and the CLI release that ships it**: the gate's version is that release's number, so T6 launches only after the tag.
- Commit: `console(onboarding): the budget question the CLI wizard asks (D2210)`.
- Reviewer tier: standard.

## Parallel execution

| Level | Tasks | Why it waits |
| --- | --- | --- |
| 0 | T0, T1 | nothing |
| 1 | T2a, T2b, T4 | T1's constants and rewrite; T0's `init.go` and carry |
| 2 | T3, T7 | `init.go` after T2a, T1's rewrite; T4's bare command |
| 3 | T2c | `init.go` after T3; the files of T2b, T4 and T7 |
| 4 | T5 | the skill file after T2c; the behaviour of T3, T4 and T7 |
| after a CLI release | T6 | T2b's members in a tagged CLI |

**Critical path:** five tasks, T0 → T2a → T3 → T2c → T5. `fugaro plan graph` reports five levels with T0 as the bottleneck, and puts the plan ready in 212–957 minutes. T1 runs beside T0. T2b, T4 and T7 sit off the path. The graph places T6 at level 2 by its edges, but it launches only after the CLI release that ships T2b.

**Edges kept, each with its reason:**

- T2a, T2b and T4 need T1's constants. This is an interface only, but T1 is level 0 and holds them already, so no contract is needed.
- T2a needs T0: the same file `init.go`, and the carry the default alert needs.
- T3 and T4 need T1: the rewrite's behaviour.
- T3 needs T2a: `init.go`.
- T7 needs T4: the behaviour the refusal names.
- T2c needs T2a (behaviour and `init.go`), T3 (`init.go`), T2b (`setup.go` and `setup_wizard_test.go`), and T4 and T7 (shared test files).
- T5 needs T2c (the skill file) and T3, T4 and T7 (behaviour).
- T6 needs T2b and its release.

**Edges broken: none.** Considered and declined:

- A contract for the constants (they moved into T1, already at level 0, which costs nothing).
- Breaking T3→T2a or T2c→T3. Both carry `init.go`, a file, not an interface.

**Shared files.** No level holds two hand-writers of one file.

| File | Writers (level) |
| --- | --- |
| `internal/cli/init.go` | T0 (0), T2a (1), T3 (2), T2c (3) |
| `internal/cli/init_budget_test.go` | T0 (0), T2a (1), T2c (3) |
| `internal/infra/managed/marker.go` | T1 (0) |
| `internal/cli/setup.go`, `setup_wizard_test.go` | T2b (1), T2c (3) |
| `internal/cli/managed_flag.go`, `managed_flag_test.go` | T4 (1); T2c touches neither (T4 drops its own `--managed`) |
| `internal/managed/budget.go`, `budget_test.go` | T4 (1) |
| `internal/cli/update_selfflag.go`, `update_selfflag_test.go` | T3 (2) |
| `internal/cli/root.go`, `managed_require.go`, `cloud.go` | T7 (2) |
| shared test fixtures (U3) | T3 (2, the update fixture) and T7 (2, the command fixture): separate fixtures. A run that finds them to be one file reports it, and T7 waits for T3 |
| `plugin/skills/setup/reference/managed.md` | T2c (3), T5 (4) |
| `website/content/cli-reference.md` | **generated**: T2a, T2b, T4 (1), T3 (2), T2c (3). Each regenerates it; a conflict is resolved by `go run ./ee/clidoc` on the merged tree |

**Contract changes anticipated:** none. If T3 or T4 finds `RewriteBudget`'s signature wrong, a small follow-up to T1 changes it, and that reaches T3 and T4 only.

## Checks

- **Check 861** (owner-run, before upgrading): `fugaro managed status` on each installation; flag any unmanaged one first.
- **Check 860** (owner-run, after the release that ships T3, T4 and T7): the stop, the bare `managed flag`, the converge, and the carried alert. See the reconciliation, § Checks.

## After-deploy steps

T3's pull request adds to its release notes' `### For operators`:

> Run `fugaro update`. An installation that is not managed is now refused, and the refusal names `fugaro managed flag`. Run that once (it takes the installation's project and 100 USD unless you pass `--budget`/`--budget-currency`), then `fugaro update` again.

That is the one extra command the owner's principle allows (F11). T7's notes add that every command that writes to a project refuses an unmanaged one. T2c's notes add that `fugaro setup --managed` is gone.

## Proposed runs (none launched)

Ten tasks, one run each, so at most ten times the per-run cap. T6 waits for a CLI release. Launch order follows the levels: T0 and T1, then T2a, T2b and T4, then T3 and T7, then T2c, then T5. No run starts without the owner's go on each task.
