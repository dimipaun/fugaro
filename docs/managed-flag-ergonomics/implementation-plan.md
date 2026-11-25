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
- **D2218** (not the recommendation): a managed read that fails warns and proceeds.
- **D2219:** the in-run commands are exempt.
- **D3160** (not the recommendation; amended after review): `fugaro user offboard` is exempt. Confirmed by the owner 2026-11-21.
- **TN1:** `update` never marks a project; it stops.
- **R1–R10:** the reconciliation's resolutions.

## Conventions every task follows

- **`CLAUDE.md` throughout.** No confirmation is added. The one new question, the wizard's budget, is a value with a default. It is asked only at a person's terminal, through the same gate the rigor question uses. With no terminal the default applies, and nothing waits. Comments explain why. Tests are hermetic (`internal/gcpfake`, `t.TempDir()`), with no secret-shaped literal, and runs are targeted.
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
5. **A stop writes nothing, and is safe with runs in flight.** Pinned by T3 extending `TestUpdateLeavesARunInFlightRunning` (an unmanaged installation with a run in flight is refused, and the run's records are untouched) and by T7a's `TestFencedCommandWritesNothingOnUnmanaged`.
6. **The fence's classification is complete.** Pinned by T7a's `TestEveryWritingCommandIsClassified`.
7. **The budget alert survives a plain update on any machine** (R5, R7). Pinned by T0's `TestUpdateCarriesTheBudget` and `TestUpdateCarriesTheBudgetWithAnEmptyWorkdir`.
8. **A rewrite never changes the currency unasked** (R6). Pinned by T3's `TestUpdateRewriteKeepsRecordedCurrency` and T4's `TestManagedFlagRewriteKeepsRecordedCurrency`.
9. **A failed managed read warns and proceeds on the path an unmanaged project took before this plan, never through the managed gate** (D2218). Only a read that answers "unmanaged" stops. Pinned by T3's `TestUpdateManagedReadFailsWarns` and `TestUpdateReadFailureNeverAdmitsTheGate`, and by T7a's `TestFenceReadFailsWarns`.
10. **An exempt command writes nothing to an unmanaged project, unless its exemption says it does** (`fugaro user offboard`, D3160; the in-run commands, D2219). Pinned by T7a's `TestExemptCommandsWriteNothing`.
11. **`RewriteBudget` has exactly two callers, both fenced** (R9). Pinned by T2c's `TestRewriteBudgetCallSites`.

## Slices and tasks

### Slice A: foundations (inert alone)

### T0: carry the budget block between runs
- Acceptance:
  - A run given no budget flags carries the budget block, so a plain `update` after a run that created the alert plans no change to `google_billing_budget`.
  - Per R7, the amount and currency come from the marker when it records them, and the billing account comes from the remote Terraform state's budget resource. Neither comes from the local work directory, so the carry works on a fresh machine.
  - Explicit flags replace what is carried, and an explicit `--budget` with no `--billing-account` takes the carried account.
  - `flagHints`' sentence for the budget no longer says "nothing records".
  - The read: `infra.ReadStateObject` (`statebucket.go`) fetches the raw state, and a small parser in `tfvars.go` takes the budget resource's `billing_account` out of it. Both run before `prepare` writes the variables, since `tf.ShowState` needs an `Init` that `prepare` precedes.
  - `prepare`'s other callers: `--print-vars` reads no cloud by design, so it carries nothing and says so; the rollback (`--forget`) carries nothing.
  - The Billing Budgets API's "pass them again" sentence in `flagHints` goes with the budget's.
  - An installation whose alert differs from its marker plans the change once (R10); the notes say so.
- Files: `internal/infra/tfvars.go`, `internal/infra/budgetcarry_test.go` (new), `internal/infra/installation.go`, `internal/cli/init.go` (the call sites beside `CarriedManagedDeployerScoped`), `internal/cli/init_budget_test.go`.
- Interfaces: produces `infra.CarriedBudget`, consumed inside this task.
- Tests: `TestCarriedBudgetKeepsPrevious`, `TestCarriedBudgetExplicitReplaces`, `TestCarriedBudgetAmountFromMarker`, `TestUpdateCarriesTheBudget`, `TestUpdateCarriesTheBudgetWithAnEmptyWorkdir`.
- Command: `go test ./internal/infra/ -run TestCarriedBudget -count=1 && go test ./internal/cli/ -run 'TestUpdateCarriesTheBudget' -count=1` (the pattern also matches `TestUpdateCarriesTheBudgetWithAnEmptyWorkdir`).
- Red step: `TestUpdateCarriesTheBudget` fails: the plan deletes `module.installation.google_billing_budget.this[0]`.
- Depends on: none.
- Commit: `infra: carry the budget block from the previous run (R5)`.
- Reviewer tier: standard.

### T1: the budget defaults, and rewriting a recorded amount
- Acceptance:
  - `internal/infra/managed/defaults.go` exports `DefaultBudgetAmount = 100` and `DefaultBudgetCurrency = "USD"`, and no other budget default spells either figure. `"USD"` elsewhere, as in `internal/budget`'s price figures, is not a budget default and stays.
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
  - Every creating run, a resumed creation included (R8), records its budget in the creation fact, whether or not `--managed` was passed.
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
  - On an installation the managed read answers is unmanaged, `fugaro update`, and `fugaro setup` on an existing installation (R8), refuses through `managedRequired` and names `fugaro managed flag`.
    - The stop sits at the head of the engine, before the first cloud write and before the local config is rewritten (F16). The test asserts that `gcpfake` recorded zero writes and that the local config is byte-identical.
    - `--plan-only` and `--check` still read.
  - A managed read that fails warns and proceeds (D2218).
  - `selfFlagUnmanaged`'s marking is removed, and so is its post-converge hook in `init_stages.go` (TN1; D1553 amended).
  - The managed read reports a failure separately from an "unmanaged" answer (`readUpdateGate`, `readManagedGate`). A failure takes the path an unmanaged project took before this plan, and never the managed gate (D2218, amended).
  - On a managed installation, an explicit `--budget` rewrites the recorded amount through `RewriteBudget` before the apply (R7), with the currency defaulting to the recorded one (R6), and prints the old and new amounts. No flag means no write (D2215).
  - A refused rewrite (an agent session, a run's process) ends the run. It never goes on to apply the explicit amount anyway.
  - The rewrite is refused in a coding agent's session and inside a Fugaro run, before it writes.
  - With runs in flight, the stop asks none to finish and touches nothing they hold.
  - An installation at the previous release that is unmanaged is refused with the command. After the fake's `managed flag`, it converges.
  - `budgetFlagHelp`'s update-door sentence and `docs/design/update-in-flight.md` say this.
  - The stale "refused by every managed apply" comment (F14) and the file's `--managed` mention go.
- Files: `internal/cli/update_selfflag.go`, `internal/cli/update_selfflag_test.go`, `internal/cli/update_in_flight_test.go`, `internal/cli/update_converge_test.go`, `internal/cli/init_stages.go` (the stop at the head; the old hook goes), `internal/cli/init.go` (`budgetFlagHelp`), `internal/cli/update_managed.go` and `internal/cli/managed_gate.go` (the read's error kept apart), `docs/design/update-in-flight.md`, `website/content/cli-reference.md` (generated), plus the update test fixture (U3). Over five files: the stop, its hook and its in-flight and converge proofs are one change.
- Interfaces: consumes `RewriteBudget` (T1) and `managedRequired` (as it is today).
- Tests: `TestUpdateRefusesUnmanaged`, `TestSetupExistingUnmanagedRefuses`, `TestUpdateManagedReadFailsWarns`, `TestUpdateReadFailureNeverAdmitsTheGate`, `TestUpdateRewriteKeepsRecordedCurrency`, `TestUpdateRewriteRefusalEndsTheRun`, `TestUpdateExplicitBudgetRewrites`, `TestUpdateKeepsARecordedAmountWithoutBudget`, `TestUpdateRewriteRefusedInAgentSession`, `TestUpdateRewriteRefusedInsideAFugaroRun`, `TestUpdateConvergesAfterFlag`, `TestUpdateLeavesARunInFlightRunning` (extended).
- Command: `go test ./internal/cli/ -run 'TestUpdate(RefusesUnmanaged|ManagedReadFails|ReadFailure|ExplicitBudget|KeepsARecorded|Rewrite|ConvergesAfterFlag|LeavesARunInFlight)|TestSetupExistingUnmanagedRefuses|CliRef|Docs' -count=1`.
- Red step: `TestUpdateRefusesUnmanaged` fails: the update converges an unmanaged installation.
- Depends on: T1 (behaviour: the rewrite); T2a (the same file `init.go`).
- Commit: `update: stop on an unmanaged installation; an explicit --budget rewrites the recorded amount (TN1, D2215)`.
- Reviewer tier: opus (the managed marker and fence).

### T4: managed flag takes the project from the installation and defaults the budget
- Acceptance:
  - `fugaro managed flag [PROJECT_ID]`: omitted, the selected installation's project, named in `managedFlagStatement` before the write; given, it must match (D2213).
  - `--budget` and `--budget-currency` default to T1's constants.
  - On a project already recording an amount, an explicit `--budget` rewrites through `RewriteBudget`, with the currency defaulting to the recorded one (R6); the default does not rewrite (D2215). The alert follows on the next `update` through T0's carry (R7), and the output says so.
  - The agent and run-process refusals hold with and without the argument.
  - The help loses "takes no flag at all", the D64 sentence and the stale claim (F8, F14), and its `--managed` mentions.
  - The gate's warning in `internal/managed/budget.go` names plain `fugaro managed flag`.
- Files: `internal/cli/managed_flag.go`, `internal/cli/managed_flag_test.go`, `internal/managed/budget.go`, `internal/managed/budget_test.go`, `website/content/cli-reference.md` (generated).
- Interfaces: consumes T1.
- Tests: `TestManagedFlagNoArgUsesInstallation`, `TestManagedFlagArgMustMatch`, `TestManagedFlagRewriteKeepsRecordedCurrency`, `TestManagedFlagDefaultsBudget`, `TestManagedFlagExplicitRewrites`, `TestManagedFlagDefaultDoesNotRewrite`, `TestManagedFlagNoArgRefusedInAgentSession`, `TestManagedFlagNoArgRefusedInsideAFugaroRun`.
- Command: `go test ./internal/cli/ -run 'TestManagedFlag|CliRef' -count=1 && go test ./internal/managed/ -count=1`.
- Red step: `TestManagedFlagNoArgUsesInstallation` fails on `cobra.ExactArgs(1)`.
- Depends on: T1 (the constants and the rewrite).
- Commit: `managed flag: the project defaults to the installation's, the budget to 100 USD (D2213, D2212, D2211)`.
- Reviewer tier: opus (the marker).

### T7a: the fence's mechanism and classification, and the commands that open through openCloud
- Acceptance:
  - The fence moves from a one-row table to a classification of the whole tree. Every runnable command is either *fenced* or *exempt* with a one-line reason.
  - The mechanism: the root's pre-run puts the running `*cobra.Command` in `cmd.Context()`. `openCloud` and `openCloudFor` (`cloud.go`) read it and, for a fenced command, run the managed read and refuse through `managedRequired` before returning clients. `requireManaged(ctx, cmd, project)` in `managed_require.go` is the same check, for the commands that open clients another way (T7b, T7c).
  - The exemptions:
    - setup's creating run, a resumed creation included
    - `managed flag`
    - `managed unflag` (R8)
    - `fugaro user offboard` (D3160)
    - the in-run commands `tell`, `mailbox`, `followups` and `escalation` (D2219)
    - read-only commands (A5)
  - `update` and `setup` on an existing installation are fenced in their own engine (T3). The commands that open through `newInitClients` or `infra.NewClients` outside the init engine are classified fenced here and wired by T7b and T7c.
  - A managed read that fails warns and proceeds on the unmanaged path, never through the managed gate (D2218).
  - `TestEveryWritingCommandIsClassified` walks every runnable command (`root.Commands()` recursively) and fails on any command in neither list.
  - `managedFlagCommand` spells plain `fugaro managed flag`. The rest of `managedRequired`'s sentence drops "the argument is the declaration itself" and keeps "run by its owner at their own terminal".
- Files: `internal/cli/root.go`, `internal/cli/managed_require.go`, `internal/cli/cloud.go`, `internal/cli/managed_require_test.go`, plus the shared command fixture (U3).
- Interfaces: produces `requireManaged` (T7b, T7c); consumes T4's behaviour (the bare command it names).
- Tests: `TestEveryWritingCommandIsClassified`, `TestFencedCommandRefusesUnmanaged` (table-driven over the commands that open through `openCloud`, `fugaro apply` included), `TestFencedCommandWritesNothingOnUnmanaged`, `TestExemptCommandsWriteNothing` (each exempt path against a write-recording `gcpfake`; zero writes apart from what the exemptions of `fugaro user offboard` and the in-run commands name), `TestFenceReadFailsWarns`.
- Command: `go test ./internal/cli/ -run 'TestEveryWritingCommandIsClassified|TestFencedCommand|TestExemptCommands|TestFenceReadFails|TestRestore' -count=1`.
- Red step: `TestFencedCommandRefusesUnmanaged` fails for a command that opens through `openCloud` on an unmanaged fake.
- Depends on: T4 (behaviour: the bare command the refusal names).
- Commit: `cli: every command that writes to a project refuses an unmanaged one (D2217)`.
- Reviewer tier: opus (the managed fence).

### T7b: the fence on the accounts, delete-list, apply, policy and repository commands
- Acceptance: each command in these files calls `requireManaged` once its project is known and before its first write: `accounts_managed.go`, `accounts_rm.go`, `allow_delete.go`, `managed_apply.go`, `project_policy_managed.go` and `project_repos_rm.go` (they open clients through `newInitClients` or `infra.NewClients`, F17). A command whose path already refuses an unmanaged project keeps that refusal, gains no second one, and its test pins the sentence `managedRequired` writes.
- Files: those six files and their tests, with the refusal cases added in each command's own test file. Over the five-file guideline: each file gains one call and one test case.
- Interfaces: consumes `requireManaged` (T7a).
- Tests: `TestAccountsRmRefusesUnmanaged`, `TestAllowDeleteRefusesUnmanaged`, `TestManagedApplyRefusesUnmanaged`, `TestProjectPolicyRefusesUnmanaged`, `TestProjectReposRmRefusesUnmanaged`, `TestAccountsManagedRefusesUnmanaged`.
- Command: `go test ./internal/cli/ -run 'RefusesUnmanaged' -count=1`.
- Red step: `TestAllowDeleteRefusesUnmanaged` fails: the delete list is written on an unmanaged fake.
- Depends on: T7a (`requireManaged`; an interface, and T7a is small, so no contract).
- Commit: `cli: the accounts, delete-list, apply, policy and repository commands refuse an unmanaged project (D2217)`.
- Reviewer tier: opus (the managed fence).

### T7c: the fence on the image, workspace and publish commands
- Acceptance: the same as T7b, for `image_build_managed.go`, `image_refresh_managed.go`, `image_refresh_steps.go`, `workspace_memberops.go` and `write_publish.go`.
- Files: those five files and their tests. Over the five-file guideline: each file gains one call and one test case.
- Interfaces: consumes `requireManaged` (T7a).
- Tests: `TestImageBuildRefusesUnmanaged`, `TestImageRefreshRefusesUnmanaged`, `TestWorkspaceMemberRefusesUnmanaged`, `TestWritePublishRefusesUnmanaged`.
- Command: `go test ./internal/cli/ -run 'RefusesUnmanaged' -count=1`.
- Red step: `TestImageRefreshRefusesUnmanaged` fails: a build is submitted on an unmanaged fake.
- Depends on: T7a.
- Commit: `cli: the image, workspace and publish commands refuse an unmanaged project (D2217)`.
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
  - The old spelling `managed flag <id> --budget N --budget-currency CCC` in `init.go` and `restore.go` messages becomes plain `fugaro managed flag`, and the `--managed` comment in `recipes_skew.go` goes.
- Files: `internal/cli/init.go`, `internal/cli/init_project.go`, `internal/cli/restore.go`, `internal/cli/setup.go`, `internal/cli/update.go`, `internal/cli/managed_unflag.go`, `internal/cli/recipes_skew.go`, `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/managed.md`, `website/content/cli-reference.md` (generated), and the remaining test files from `git grep -l -e '--managed\([^-a-z_]\|$\)' -- 'internal/**/*_test.go'` (argv only). **Over the five-file guideline on purpose:** a flag and its callers cannot be split across pull requests without a red tree in between.
- Interfaces: consumes T2a's creation-fact behaviour.
- Tests (all new): `TestSetupManagedFlagIsGone`, `TestUpdateManagedFlagIsGone`, `TestRestoreCreateArgs`, and `TestRewriteBudgetCallSites` (R9: a `go/ast` scan of `internal/` finding exactly the two fenced callers). Plus the plugin tests and the Docs filter. The doc pins in `docs_security_test.go` and `docs_managed_deployer_test.go` are T5's, not T2c's.
- Command: `go test ./internal/cli/ -run 'ManagedFlagIsGone|TestRestore|TestRewriteBudgetCallSites|Docs|CliRef' -count=1 && go test ./internal/e2e/ ./internal/managed/ ./internal/infra/managed/ -count=1 && go test ./plugin/ -count=1`.
- Red step: `TestSetupManagedFlagIsGone` fails: the flag is accepted.
- Depends on: T2a (behaviour: dropping the flag first would stop creations recording an amount; and `init.go`); T3 (`init.go`); T2b (`setup.go` and `setup_wizard_test.go`); T4, T7b and T7c (test files their stops and defaults touch, among them `managed_apply_test.go`).
- Commit: `setup: --managed is gone; every creating run records its budget (D2214)`.
- Reviewer tier: opus (plugin skills).

### T5: the skills, SECURITY.md and the website say what ships
- Acceptance:
  - `plugin/skills/setup/reference/managed.md` describes the defaults, the optional ID, the rewrite, and the stop on an unmanaged project, with `fugaro managed flag` as the one way out.
  - `plugin/skills/update/SKILL.md` and `reference/states.md` gain the refusal state. The agent cannot run `managed flag` (it is refused in an agent session), so it hands the owner that one command and stops.
  - `SECURITY.md` and `website/content/security.md` say:
    - a command that writes refuses an unmanaged project, with its exemptions;
    - every refusal now names `fugaro managed flag` to whoever reads it, an agent included. That is the same capability F2 describes, now printed, and its only fence is the agent-session marker, which is a mitigation, not a barrier.

    They drop "a project that is not managed keeps the confirmations", "an installation that never asks for managed …" and `--create-project --managed` as the creating command. The pins move with them.
- Files: `plugin/skills/setup/reference/managed.md`, `plugin/skills/update/SKILL.md`, `plugin/skills/update/reference/states.md`, `SECURITY.md`, `website/content/security.md`, `internal/cli/docs_security_test.go`, `internal/cli/docs_managed_deployer_test.go`. Seven files, all words about one behaviour.
- Interfaces: describes T2c, T3, T4 and T7a–c.
- Tests: the Docs filter, the plugin tests, the website's checks.
- Command: `go test ./internal/cli/ -run Docs -count=1 && go test ./plugin/ -count=1 && (cd website && npm run check)`.
- Red step: the changed pins in `docs_security_test.go` fail against the old text.
- Depends on: T2c (the same skill file); T3, T4 and T7a–c (behaviour described).
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
| 2 | T3, T7a | `init.go` after T2a, T1's rewrite; T4's bare command |
| 3 | T7b, T7c | T7a's `requireManaged` |
| 4 | T2c | `init.go` after T3; the files of T2b, T4, T7b and T7c |
| 5 | T5 | the skill file after T2c; the behaviour of T3, T4 and T7a–c |
| after a CLI release | T6 | T2b's members in a tagged CLI |

**Critical path:** six tasks, T1 → T4 → T7a → T7b → T2c → T5, as `fugaro plan graph` reports it (six levels, bottleneck T1, ready in 252–1156 minutes). T0 → T2a → T3 runs beside it, one task shorter. The graph places T6 at level 2 by its edges, but it launches only after the CLI release that ships T2b.

**Edges kept, each with its reason:**

- T2a, T2b and T4 need T1's constants. This is an interface only, but T1 is level 0 and holds them already, so no contract is needed.
- T2a needs T0: the same file `init.go`, and the carry the default alert needs.
- T3 and T4 need T1: the rewrite's behaviour.
- T3 needs T2a: `init.go`.
- T7a needs T4: the behaviour the refusal names.
- T7b and T7c need T7a: `requireManaged`. This is an interface, but T7a is itself small, and a contract for one function signature would add a task to save less than one.
- T2c needs:
  - T2a (behaviour and `init.go`)
  - T3 (`init.go`)
  - T2b (`setup.go` and `setup_wizard_test.go`)
  - T4, T7b and T7c (shared test files, among them `managed_apply_test.go`)
- T5 needs T2c (the skill file), and T3, T4 and T7a–c (behaviour).
- T6 needs T2b and its release.

**Edges broken: none.** Considered and declined:

- A contract for the constants (they moved into T1, already at level 0, which costs nothing).
- A contract for `requireManaged` (above).
- Breaking T3→T2a or T2c→T3. Both carry `init.go`, a file, not an interface.

**Shared files.** No level holds two hand-writers of one file.

| File | Writers (level) |
| --- | --- |
| `internal/cli/init.go` | T0 (0), T2a (1), T3 (2), T2c (4). The fence never touches `newInitClients` itself; T7b and T7c call `requireManaged` in each command file |
| `internal/cli/init_budget_test.go` | T0 (0), T2a (1), T2c (4) |
| `internal/infra/managed/marker.go` | T1 (0) |
| `internal/cli/setup.go`, `setup_wizard_test.go` | T2b (1), T2c (4) |
| `internal/cli/managed_flag.go`, `managed_flag_test.go` | T4 (1); T2c touches neither (T4 drops its own `--managed`) |
| `internal/managed/budget.go`, `budget_test.go` | T4 (1) |
| `internal/cli/update_selfflag.go`, `update_selfflag_test.go`, `init_stages.go`, `update_managed.go`, `managed_gate.go` | T3 (2) |
| `internal/cli/root.go`, `managed_require.go`, `cloud.go` | T7a (2) |
| the six command files of T7b and their tests | T7b (3); T2c (4) for `managed_apply_test.go`'s argv |
| the five command files of T7c and their tests | T7c (3) |
| `internal/cli/rewrite_callsites_test.go` (new) | T2c (4) |
| shared test fixtures (U3) | T3 (2, the update fixture) and T7a (2, the command fixture): separate fixtures. A run that finds them to be one file reports it, and T7a waits for T3 |
| `plugin/skills/setup/reference/managed.md` | T2c (4), T5 (5) |
| `website/content/cli-reference.md` | **generated**: T2a, T2b, T4 (1), T3 (2), T2c (4). Each regenerates it; a conflict is resolved by `go run ./ee/clidoc` on the merged tree |

**Contract changes anticipated:** none.

- If T3 or T4 finds `RewriteBudget`'s signature wrong, a small follow-up to T1 changes it, and that reaches T3 and T4 only.
- If T7b or T7c finds `requireManaged`'s signature wrong, the same applies to T7a.

## Checks

- **Check 861** (owner-run, before upgrading): `fugaro managed status` on each installation; flag any unmanaged one first.
- **Check 860** (owner-run, after the release that ships T3, T4 and T7a–c): the stop, the bare `managed flag`, the converge, the carried alert, and a drifted installation (R10). See the reconciliation, § Checks.

## After-deploy steps

T3's pull request adds to its release notes' `### For operators`:

> Run `fugaro update`. An installation that is not managed is now refused, and the refusal names `fugaro managed flag`. Run that once (it takes the installation's project and 100 USD unless you pass `--budget`/`--budget-currency`), then `fugaro update` again. On an installation whose budget alert differs from its recorded amount, the first update corrects the alert. Run it yourself: a coding agent's update is refused at that step.

That is the one extra command the owner's principle allows (F11, R10). T7a's notes add that every command that writes to a project refuses an unmanaged one. T2c's notes add that `fugaro setup --managed` is gone.

## Proposed runs (none launched)

Twelve tasks, one run each, so at most twelve times the per-run cap. T6 waits for a CLI release. Launch order follows the levels:

1. T0 and T1
2. T2a, T2b and T4
3. T3 and T7a
4. T7b and T7c
5. T2c
6. T5

No run starts without the owner's go on each task.
