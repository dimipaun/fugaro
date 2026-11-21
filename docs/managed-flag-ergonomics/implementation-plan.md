# Managed flag ergonomics: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's explicit go.**

It builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), whose answers shaped it:

- D2210 adds a budget question to the wizard, suggesting 100.
- D2211 defaults the currency to USD.
- D2212 (not the recommendation) defaults the amount to 100 everywhere.
- D2213 makes `managed flag`'s project ID optional.
- D2214 drops `setup --managed`.
- D2215 lets an explicit `--budget` rewrite a recorded amount.
- D2216 defaults the billing account to the one the run links.
- T1 (not the recommendation): every `fugaro update` marks an unmanaged installation.
- R1–R4 are the reconciliation's resolutions.

## Conventions every task follows

- `CLAUDE.md` throughout, in particular: no prompt and no confirmation is added (the budget question is a value with a suggestion, refused at once with no terminal only where it has no default, and under D2212 it always has one); comments explain why; hermetic tests (`internal/gcpfake`, `t.TempDir()`); no secret-shaped literal; targeted tests only.
- **The generated CLI reference.** Every task that changes a flag or its help regenerates `website/content/cli-reference.md` with `go run ./ee/clidoc` and runs `go test ./internal/cli/ -run 'Docs|CliRef' -count=1`. The file is generated, never hand-edited. A merge conflict in it is resolved by regenerating it on the merged tree, never by hand.
- **Docs are tested.** A task that changes a sentence pinned by a `docs_*_test.go` changes the pin in the same commit.
- **Plugin.** A task that removes a flag a skill names edits the skill in the same pull request (`plugin/` tests check every flag a skill names against the CLI).
- **Boundaries.** *Always*: tests before the commit, `gofmt -l .` prints nothing, `go vet ./...`. *Ask first*: any change to the marker's shape (none is planned, A3), and any new dependency. *Never*: an agent runs `fugaro setup`, `update` or `managed flag` against a real project. Check 860 is the owner's.
- **Rigor.** The repository's default mode is `startup` and it sets no area rules (`fugaro settings rigor.default rigor.areas --repo dimipaun/fugaro`). Tasks that write the managed marker or edit `plugin/skills/**` or `SECURITY.md` get the opus reviewer tier, per the repository's reviewer routing.
- **Decoupling.** `planning.decoupling` is on for this repository. The pass ran and broke no edge (see below).

## Global constraints and review focus

1. **A default never overwrites a recorded amount** (D2215). Pinned by T3's `TestUpdateKeepsARecordedAmountWithoutBudget` and T4's `TestManagedFlagDefaultDoesNotRewrite`.
2. **The marker's shape does not change** (A3). Pinned by T1's `TestRewriteBudgetKeepsVersion4Bytes`.
3. **No command waits for input.** The budget question has a default, so a run with no terminal takes it (D2212) and never hangs. Pinned by T2b's `TestSetupBudgetQuestionNoTerminalTakesDefault`.
4. **The fences stay.** `managed flag` and `update`'s marking step are refused in a coding agent's session and in a Fugaro run's process, whether or not an amount was passed. Pinned by T3's `TestUpdateSelfFlagRefusedInAgentSessionWithoutBudget` and T4's existing refusal tests, run unchanged.
5. **`update` stays safe with runs in flight** (F11). Pinned by T3 extending `TestUpdateLeavesARunInFlightRunning`.

## Slices and tasks

### Slice A: the marker can be rewritten (inert alone)

### T1: rewrite a recorded budget amount
- Acceptance: `managed.RewriteBudget(ctx, s, bucket, project, number, budget) (old Budget, err error)` replaces the amount and currency of this project's version-4 marker with a write conditioned on the generation it read. It returns the old figures and writes nothing when they are equal. It refuses, with what is there, a version-1 creation fact (that object is `WriteFlag`'s), an unknown version, another project's object, a bucket that fails `proveRunsBucket`, and a budget that fails `Budget.Check`.
- Files: `internal/infra/managed/marker.go`, `internal/infra/managed/marker_test.go`.
- Interfaces: produces `RewriteBudget`, consumed by T3 and T4.
- Tests: `TestRewriteBudgetReplacesAmount`, `TestRewriteBudgetEqualIsNoWrite`, `TestRewriteBudgetRefusesVersion1`, `TestRewriteBudgetRefusesAnotherProject`, `TestRewriteBudgetLosesTheRace`, `TestRewriteBudgetKeepsVersion4Bytes`.
- Command: `go test ./internal/infra/managed/ -run TestRewriteBudget -count=1`.
- Red step: `TestRewriteBudgetReplacesAmount` fails to compile before the function exists.
- Depends on: none.
- Commit: `managed: rewrite a recorded budget amount under a generation precondition (D2215)`.
- Reviewer tier: opus (the managed marker is the managed gate's evidence).

### Slice B: setup asks for, defaults and records the budget

### T2a: setup's budget defaults and the creation fact
- Acceptance:
  - `internal/infra/managed` exports `DefaultBudgetAmount = 100` and `DefaultBudgetCurrency = "USD"`, and nothing else in the tree spells either figure.
  - On a creating run, an unset `--budget` takes 100 (D2212) and an unset `--budget-currency` takes USD (D2211).
  - An unset `--billing-account` takes the account the run links (D2216).
  - Every creating run records its budget in the creation fact, whether or not `--managed` was passed.
  - With no account linked, the run creates no Terraform budget and prints the one command that adds it.
  - The trio check reads only explicit flags (R2).
  - `--answers-file`'s help lists the document's members through `setupAnswerKeys()` and names no count of questions, so a later row changes no `init.go` line.
- Files: `internal/infra/managed/defaults.go` (new), `internal/cli/init.go`, `internal/cli/init_budget_test.go`, `website/content/cli-reference.md` (generated).
- Interfaces: produces the two constants (consumed by T2b, T3, T4) and the creation-fact behaviour (consumed by T2c).
- Tests: `TestSetupCreatingRunRecordsDefaultBudget`, `TestSetupBillingAccountDefaultsToLinked`, `TestSetupNoLinkedAccountRecordsAmountAndPrintsCommand`, `TestSetupExplicitBudgetWins`, the existing `init_budget_test.go` cases updated.
- Command: `go test ./internal/cli/ -run 'TestSetup(CreatingRun|BillingAccount|NoLinkedAccount|ExplicitBudget)|Budget|CliRef' -count=1`.
- Red step: `TestSetupCreatingRunRecordsDefaultBudget` fails because a creation without `--managed` writes the version-1 fact with no amount.
- Depends on: none.
- Commit: `setup: a creating run records its budget, 100 USD unless told otherwise (D2211, D2212, D2216)`.
- Reviewer tier: opus (it writes the creation fact).

### T2b: the wizard's budget question and the answers-file members
- Acceptance:
  - `setupQuestions` gains a budget row, flag `--budget`, asked on a creating run after the rigor row. The label says the amount is the budget alert's threshold, not a cap.
  - Enter takes `DefaultBudgetAmount` (D2210).
  - With no terminal, the question is not asked and the default applies (D2212).
  - The answers file accepts optional `budget` (1 to 10,000,000) and `budget_currency` (three uppercase letters), with the version unchanged (R4).
  - `schemas/setup-answers.schema.json` documents both members.
- Files: `internal/cli/setup.go`, `internal/cli/setup_answers.go`, `schemas/setup-answers.schema.json`, `internal/cli/setup_wizard_test.go`, `internal/cli/setup_answers_test.go`, `website/content/cli-reference.md` (generated).
- Interfaces: consumes T2a's constants; produces the members that T6 sends.
- Tests: `TestSetupBudgetQuestionEnterTakesDefault`, `TestSetupBudgetQuestionNoTerminalTakesDefault`, `TestSetupAnswersBudgetMembers`, `TestEveryWizardQuestionHasAFlag`, `TestSetupAnswersSchema`.
- Command: `go test ./internal/cli/ -run 'TestSetupBudgetQuestion|TestSetupAnswers|TestEveryWizardQuestionHasAFlag|CliRef' -count=1`.
- Red step: `TestSetupAnswersBudgetMembers` fails: the reader refuses `budget` as an unknown member.
- Depends on: T2a (the default constant).
- Commit: `setup: the wizard asks for the monthly budget, 100 by default (D2210)`.
- Reviewer tier: standard.

### Slice C: update and managed flag

### T3: update marks every unmanaged installation, and an explicit budget rewrites
- Acceptance:
  - `selfFlagUnmanaged` runs on every `fugaro update` that is not `--plan-only`. With no `--budget` it records the defaults (T1's ruling).
  - On an installation already recording an amount, an explicit `--budget` rewrites it through `RewriteBudget` and prints the old and new amounts. No flag means no write (D2215).
  - With no billing account, it prints the one command that adds the alert (R2).
  - The in-flight notice names the marking step in one line when runs are running.
  - An installation at the previous release that is unmanaged converges to managed.
  - `budgetFlagHelp`'s update-door sentence says all of this.
- Files: `internal/cli/update_selfflag.go`, `internal/cli/update_selfflag_test.go`, `internal/cli/update_in_flight.go`, `internal/cli/update_converge_test.go`, `internal/cli/init.go` (`budgetFlagHelp` only), `website/content/cli-reference.md` (generated).
- Interfaces: consumes `RewriteBudget` (T1) and the constants (T2a).
- Tests: `TestUpdateMarksUnmanagedWithDefaults`, `TestUpdateExplicitBudgetRewrites`, `TestUpdateKeepsARecordedAmountWithoutBudget`, `TestUpdateSelfFlagRefusedInAgentSessionWithoutBudget`, `TestUpdateConvergesAnUnmanagedInstallation`, `TestUpdateLeavesARunInFlightRunning` (extended).
- Command: `go test ./internal/cli/ -run 'TestUpdate(MarksUnmanaged|ExplicitBudget|KeepsARecorded|SelfFlag|ConvergesAnUnmanaged|LeavesARunInFlight)|CliRef' -count=1`.
- Red step: `TestUpdateMarksUnmanagedWithDefaults` fails: with no `--budget` the step returns at once.
- Depends on: T1 (behaviour: the rewrite it calls); T2a (the same file `init.go`, and the explicit-only trio check that keeps a default from tripping it).
- Commit: `update: mark an unmanaged installation at the default budget; an explicit --budget rewrites it (T1, D2215)`.
- Reviewer tier: opus (it writes the managed marker and label).

### T4: managed flag takes the project from the installation and defaults the budget
- Acceptance:
  - `fugaro managed flag [PROJECT_ID]`. Omitted, the selected installation's project is used and named in `managedFlagStatement` before the write. Given, it must match, as today (D2213).
  - `--budget` defaults to 100 and `--budget-currency` to USD (D2212, D2211).
  - On a project already recording an amount, an explicit `--budget` rewrites it through `RewriteBudget`, and the default does not (D2215).
  - The long help loses "takes no flag at all" and the D64 sentence (F8).
  - The agent and run-process refusals are unchanged.
- Files: `internal/cli/managed_flag.go`, `internal/cli/managed_flag_test.go`, `website/content/cli-reference.md` (generated).
- Interfaces: consumes `RewriteBudget` (T1) and the constants (T2a).
- Tests: `TestManagedFlagNoArgUsesInstallation`, `TestManagedFlagArgMustMatch`, `TestManagedFlagDefaultsBudget`, `TestManagedFlagExplicitRewrites`, `TestManagedFlagDefaultDoesNotRewrite`, the existing refusal tests.
- Command: `go test ./internal/cli/ -run 'TestManagedFlag|CliRef' -count=1`.
- Red step: `TestManagedFlagNoArgUsesInstallation` fails on `cobra.ExactArgs(1)`.
- Depends on: T1 (behaviour: the rewrite); T2a (the default constants).
- Commit: `managed flag: the project defaults to the installation's, the budget to 100 USD (D2213, D2212, D2211)`.
- Reviewer tier: opus (it writes the managed marker).

### Slice D: --managed goes, and the words follow

### T2c: drop `fugaro setup --managed`
- Acceptance:
  - Both doors refuse `--managed` before anything is read, in one sentence: the flag is gone and every creating run records its budget (R3, D2214).
  - The `--managed` cases in `checkCreateProject` go, and with them the comment that forbids a constant and the "types its ID" refusal (F8).
  - `restore`'s `createArgs` stops passing `--managed` (R1).
  - Every test passing `--managed` drops it.
  - `plugin/skills/setup/SKILL.md` and `plugin/skills/setup/reference/managed.md` stop naming it, with a minimal edit; T5 rewrites the prose.
- Files: `internal/cli/init.go`, `internal/cli/init_project.go`, `internal/cli/restore.go`, `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/managed.md`, `website/content/cli-reference.md` (generated). Also the 20 test files under `internal/` that pass `--managed` (`git grep -l -e '--managed\b' -- 'internal/**/*_test.go'`); in those, only argv lines change. **Over the five-file guideline, on purpose:** a flag cannot be removed in one pull request and its callers in another without a red tree in between.
- Interfaces: consumes T2a's creation-fact behaviour.
- Tests: `TestSetupManagedFlagIsGone`, `TestUpdateManagedFlagIsGone`, `TestRestoreCreateArgs`, the plugin tests, the Docs filter.
- Command: `go test ./internal/cli/ -run 'ManagedFlagIsGone|TestRestore|Docs|CliRef' -count=1 && go test ./internal/e2e/ ./internal/managed/ ./internal/infra/managed/ -count=1 && go test ./plugin/ -count=1`.
- Red step: `TestSetupManagedFlagIsGone` fails: `--managed` is accepted.
- Depends on: T2a (behaviour: without it, dropping the flag would stop every creation recording an amount; and the same file `init.go`); T3 (the same file `init.go`).
- Commit: `setup: --managed is gone; every creating run records its budget (D2214)`.
- Reviewer tier: opus (plugin skills).

### T5: the skill, SECURITY.md and the website say what ships
- Acceptance:
  - `plugin/skills/setup/reference/managed.md` describes the defaults (D2211, D2212), the optional project ID (D2213), the rewrite (D2215) and `update`'s marking (T1).
  - `SECURITY.md` and `website/content/security.md` no longer present `--create-project --managed` as the creating command; the pinned sentences move with them.
- Files: `plugin/skills/setup/reference/managed.md`, `SECURITY.md`, `website/content/security.md`, and the `internal/cli/docs_*_test.go` pins they touch (`docs_security_test.go`, `docs_managed_deployer_test.go`).
- Interfaces: describes the behaviour of T2c, T3 and T4.
- Tests: the Docs filter, the plugin tests, and the website's security test.
- Command: `go test ./internal/cli/ -run Docs -count=1 && go test ./plugin/ -count=1 && (cd website && npm run check)`.
- Red step: the changed pins in `docs_security_test.go` fail against the old `SECURITY.md` text.
- Depends on: T2c (the same skill file); T3 and T4 (behaviour: the text describes them).
- Commit: `docs: the managed budget defaults, the optional project ID and update's marking`.
- Reviewer tier: opus (plugin skills, `SECURITY.md`).

### Slice E: the console asks the same question

### T6: the console's first-run wizard asks for the budget
- Acceptance:
  - The onboarding form gains a budget field, prefilled with 100, saying it is the alert threshold.
  - `wizardrun.ts` sends `budget` in the answers document only to a CLI whose version accepts the member (`console/src/shared/version.ts`); an older CLI refuses an unknown member.
  - An empty field sends nothing, and the CLI's default applies.
- Files: `console/src/main/wizardrun.ts`, `console/src/renderer/entry.ts`, `console/src/shared/version.ts`, `console/test/unit/onboarding.test.ts`.
- Interfaces: consumes T2b's members.
- Tests: the onboarding unit tests for the field, and the version gate.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 onboarding`.
- Red step: the new test expecting `budget` in the document fails.
- Depends on: T2b (behaviour: the CLI version that reads the member is the one the gate names).
- Commit: `console(onboarding): the budget question the CLI wizard asks (D2210)`.
- Reviewer tier: standard.

## Parallel execution

| Level | Tasks | Why it waits |
| --- | --- | --- |
| 0 | T1, T2a | nothing |
| 1 | T2b, T3, T4 | T2a's constants and its `init.go` lines; T1's rewrite |
| 2 | T2c, T6 | `init.go` after T3, and T2a's creation fact; T2b's members |
| 3 | T5 | the skill file after T2c; the behaviour of T3 and T4 |

**Critical path:** four tasks. `fugaro plan graph` reports it as T1 → T3 → T2c → T5, with T1 as the bottleneck; T2a ties with T1, since T3 needs both. The read puts the plan ready in 167–777 minutes. T2b, T4 and T6 sit off the path.

**Edges kept, each with its reason:**

- T3 needs T1 and T4 needs T1: the rewrite's behaviour, which their tests run against `gcpfake`.
- T3 needs T2a: the same file, `init.go`, and the explicit-only trio check.
- T2b needs T2a and T4 needs T2a: the default constants. This is an interface only, but breaking it gains nothing (below).
- T2c needs T2a: behaviour. Removing `--managed` first would stop every creation from recording an amount.
- T2c needs T3: the same file, `init.go`.
- T6 needs T2b: the CLI version that reads the member.
- T5 needs T2c (the same skill file) and needs T3 and T4 (the behaviour it describes).

**Edges broken: none.** Considered and declined: a contract task C1 holding the two constants, to break T2b→T2a and T4→T2a. T3 needs T2a whatever happens to those two edges (a shared file and a behaviour), so T2a stays on a four-task path. A contract ahead of T2a would add a level to that path and take none away, so the break was undone.

**Shared files.** No level holds two hand-writers of one file.

| File | Writers (level) |
| --- | --- |
| `internal/cli/init.go` | T2a (0), T3 (1), T2c (2) |
| `internal/infra/managed/marker.go` | T1 (0) |
| `internal/cli/init_budget_test.go` | T2a (0); T2c (2), argv lines only |
| `internal/cli/managed_flag_test.go`, `update_selfflag_test.go`, `setup_wizard_test.go` | T4, T3, T2b (1); T2c (2), argv lines only |
| `plugin/skills/setup/reference/managed.md` | T2c (2), T5 (3) |
| `website/content/cli-reference.md` | **generated**: T2a (0), T2b, T3, T4 (1), T2c (2). Each regenerates it; a conflict is resolved by `go run ./ee/clidoc` on the merged tree (Conventions). |

**Contract changes anticipated:** none, since no contract exists. If T3 or T4 finds `RewriteBudget`'s signature wrong, the change goes through a small follow-up to T1, and it reaches T3 and T4 only.

## Checks

- **Check 860** (owner-run, after the CLI release that ships T3; reconciliation § Checks): `fugaro update` on an unmanaged sandbox installation with no budget flag records 100 USD and the label, and names the step. Run once with an EUR billing account, it also settles U1.

## After-deploy steps

T3's pull request adds a `### For operators` entry to its release notes:

- `fugaro update` marks an installation that is not managed as managed, at 100 USD unless `--budget` and `--budget-currency` say otherwise.
- A later explicit `--budget` changes the recorded amount.

No other command and no manual step. T2c's notes add one line: `fugaro setup --managed` is gone, and every creating run records its budget.

## Proposed runs (none launched)

There are seven runs, one per task. At the repository's cap they cost at most seven times the per-run cap. Launch order follows the levels: T1 and T2a together, then T2b, T3 and T4 together, then T2c and T6, then T5. No run starts without the owner's go on each task.
