# Managed flag ergonomics: reconciliation

**Checked against `origin/main` at d49063235, 2026-11-21. Where this document disagrees with [spec.md](spec.md), this document wins.**

The spec was written on 2026-11-13, before the no-prompt policy's last tasks and the update self-flag had settled into the text it cites. This file:

- checks every claim in the spec against the code;
- records the owner's answers to its open decisions (D2211–D2214) and to the three this reconciliation adds (D2215–D2217);
- records the principle the owner restated on 2026-11-21, which ruled tension TN1 and widened the scope: **Fugaro deals only with managed projects; any project it touches must be managed, and if one is not, the command stops.**

## Assumptions

- **A1.** "The setup wizard" means the first run of `fugaro setup` at a person's terminal: the question table `setupQuestions` (`internal/cli/setup.go`), asked by `internal/cli/wizard_stage.go`, plus the billing question (`askBilling`, `internal/cli/billing_prompt.go`). The console's first-run wizard reaches the same questions through `--answers-file` (`internal/cli/setup_answers.go`, `schemas/setup-answers.schema.json`).
- **A2.** "The amount" is the one figure the marker records (`managed.Budget`, `internal/infra/managed/marker.go`) and the one the installation's Terraform budget alert is created with (`infra.Budget`, `internal/infra/tfvars.go`). The plan keeps them the same figure.
- **A3.** Nothing here changes the marker's shape. The current object is version 4 (`budget.amount`, `budget.currency`), and no format version moves.
- **A4.** "Non-interactive" means everything `canType()` answers no for: no terminal, `--json`, `--non-interactive`, a pipe, a coding agent's session, and the console's answers file.
- **A5.** "Touch" means write. A command that only reads a project (`doctor`, `status`, `runs ls`, `managed status`, `--check`, `--plan-only`) keeps working on an unmanaged project, because it is what tells the owner the project is unmanaged. D2217 records this as part of its answer.

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| The wizard asks for the budget (D2210) | No budget question. The table asks five questions (`--gcp-project`, `--location`, `--name`, `--rigor`, `--bake-repos`) and `askBilling` asks a sixth. The budget exists only as `--budget`, `--budget-currency` and `--billing-account`, registered in `newInitCmdWith` (`internal/cli/init.go`). |
| `managed flag` reads the project from the installation (D2213) | `fugaro update` already does it: given `--budget` and `--budget-currency` on an unmanaged installation, it marks that installation itself, with no ID argument and no prompt (`selfFlagUnmanaged`, `internal/cli/update_selfflag.go`; D1553). Under TN1's ruling this step goes. |
| "Mutating commands refuse an unmanaged project" (D1541) | One writer of the refusal sentence (`managedRequired`, `internal/cli/managed_require.go`). It is applied through the table `managedMutatingCommands` (`internal/cli/root.go`), **which holds only `fugaro restore`**. Every other command writes to an unmanaged project and keeps its confirmations there (`update_managed.go`: "both keep every confirmation they have"). |
| A refusal that suggests an amount | `checkCreateProject` (`internal/cli/init_project.go`) suggests `--budget 50 --budget-currency USD`. The code comment beside it forbids a constant (D64). |
| A first-run question with a suggestion | `--location` suggests US. `--rigor` has none, and a scripted run without it is refused (D900). |

## Findings

**F1 — Rule: D2213's recommendation is a prompt the no-prompt policy forbids.** The spec recommends "a typed confirmation of the project ID at the owner's terminal". `CLAUDE.md` ("no typing a name back") and D1541 (consent by argument *instead of* a prompt) rule it out. The recommendation was withdrawn and D2213 was asked again with options that hold no prompt.

**F2 — Exists: `fugaro update` already marks without the ID.** `selfFlagUnmanaged` writes the marker and the `fugaro_managed` label for the selected installation, behind the same two fences as `managed flag`. So the exact ID on `managed flag` protected nothing that `update --budget …` did not already allow. This is the premise of D2213's answer.

**F3 — Wrong: the wizard has no budget question.** D2210 *adds* a row to `setupQuestions`. A row needs a flag (`TestEveryWizardQuestionHasAFlag`) and an answers-file member (`setupAnswerQuestions`, `TestSetupAnswersSchema`, the schema). Otherwise the console's wizard cannot answer it.

**F4 — Wrong (minor): not every wizard question carries a suggestion.** Rigor has none on purpose (D900). D2210 follows the place question's pattern.

**F5 — Gap: D2211's default reaches four commands; the spec names two.** `--budget-currency` is registered on `setup`/`update` (the shared `newInitCmdWith`), on `managed flag` and on `restore`. Restore's currency already defaults to the old project's recorded currency. Resolved as R1.

**F6 — Gap: the budget needs a billing account the wizard never asks for.** The trio rule (`init.go`) exists because the alert sits on a billing account. The wizard's billing pick goes only to `--link-billing`. Asked as D2216.

**F7 — Gap: write-once amounts become wrong once a default records them.** `WriteFlag` refuses a project that already records an amount. Asked as D2215.

**F8 — Wrong: help texts contradict the code.** `managed flag`'s long help says "this command takes no flag at all" while it registers `--budget` and `--budget-currency`. `checkCreateProject`'s `--managed` refusal ends "before a person, who types its ID", contradicting D1541. The gate's warning (`internal/managed/budget.go`) and `managedFlagCommand` (`managed_require.go`) spell `managed flag <id> --budget N --budget-currency CCC`, which D2212 and D2213 make unnecessary.

**F9 — Gap: what `--managed` reaches (the spec's U1, checked; count corrected after review).**

- **Help and messages:** `init.go`, `init_project.go`, `setup.go` (setup's help), `update.go` (the update door's refusal rows), `managed_unflag.go`, `managed_flag.go`, `update_selfflag.go`, and `internal/managed/budget.go` (pinned in `budget_test.go`).
- **Restore:** `restore.go` (`createArgs`).
- **Skills:** `plugin/skills/setup/SKILL.md` and `reference/managed.md`.
- **Generated reference:** `website/content/cli-reference.md`.
- **Prose:** `SECURITY.md` and `website/content/security.md`.
- **Tests:** 17 test files pass it in argv (`git grep -l -e '--managed\([^-a-z_]\|$\)' -- 'internal/**/*_test.go'`). The looser `\b` grep also matches `--managed-settings`.

Historical release notes stay as written. The plugin tests check every flag a skill names, so the skill edit lands with the removal.

**F10 — Gap: the console's wizard sends no budget** (`console/src/main/wizardrun.ts`). Under D2212 nothing breaks. Under D2210 the console should ask the same question (D2904).

**F11 — Rule: TN1's ruling and D2217 make `fugaro update` stop on an unmanaged installation, which the HARD RULE does not allow on its own.** The HARD RULE (`CLAUDE.md`) says `fugaro update` is the whole upgrade, with no other command. An installation that is unmanaged today will, after this release, be refused by `update`, which names `fugaro managed flag` (no arguments needed after D2213 and D2212). That is a second command, once. **The owner's principle of 2026-11-21 is the ruling that allows it.** The release's `### For operators` says it in one line, and Check 861 has the owner confirm beforehand that none of their installations is unmanaged. The refusal writes nothing, so it is safe with runs in flight: it asks no run to finish and changes nothing a run holds (`docs/design/update-in-flight.md` names it as a step that stops the update, not a run).

**F12 — Exists: the console and the mobile app never read the marker's currency (the spec's U2, checked).** No file under `console/src`, `mobile/` or `shared/` reads `managed.json`.

**F13 — Gap (found in review): the Terraform budget alert is not carried between runs.**

- `deploy/terraform/gcp/modules/installation/budget.tf` declares the budget only when `var.budget` is set.
- `init.go` sets `opts.Budget` only when `--budget` is passed.
- `infra`'s `flagHints` says it outright: the budget is declared "only by --budget, --budget-currency and --billing-account, which nothing records".

So once T2a creates an alert by default, every plain `update` plans its deletion, and the delete guard refuses that (`TestInitRefusesDeletePlan`). It is a latent bug today and becomes a certain one under D2212. Resolved as R5.

**F14 — Wrong (found in review): the spec's "a marked project that records no amount is refused by every managed apply" is stale.** D1545 opens the gate with a warning for a version-1 fact (`internal/managed/budget.go`, the default case of the budget read). The amount is evidence a budget was chosen, not a cap and not a lock. `managed flag`'s help and a comment in `update_selfflag.go` repeat the stale claim; T4 and T3 correct them.

**F15 — Gap (found in review): marker versions 2 and 3 exist in installations.** `ConvergeMarker` leaves a version-2 fact as it is (`marker.go`). D2215's rewrite therefore meets versions 2, 3 and 4, and the rewrite writes version 4 under the generation it read.

## Decisions

The spec reserved D2210–D2219. D2210 was answered in the spec; D2211–D2214 are its open questions; D2215–D2217 are added here; D2218–D2219 stay reserved and unused.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| **D2210** | The wizard's budget suggestion | A prompt suggesting 100 USD, which Enter takes | **2026-11-13** (in the spec): as stated. Amended by F3: a new question row. |
| **D2211** | Does `--budget-currency` default to USD? | Yes, on `setup`, `update` and `managed flag`; restore keeps its own rule (R1) | **2026-11-21: as recommended.** A non-USD billing account passes `--budget-currency` (U1). |
| **D2212** | With nobody to ask, does the amount default to 100? | No: required there; only Enter at a prompt takes it | **2026-11-21: not the recommendation — default 100 everywhere.** D64's "required, with no default" is retired. A scripted first run and `fugaro managed flag` with no `--budget` record 100 USD. The constants live in `internal/infra/managed/defaults.go` (task T1). The comment in `checkCreateProject` that forbids a constant goes with the `--managed` case it sits in (T2c). |
| **D2213** | Where does `managed flag` get the project? (amended by F1) | The ID becomes optional. Omitted, it is the selected installation's, named in the statement printed before the write. Given, it must match | **2026-11-21: as recommended.** This amends D1541's consent by argument for this command, on F2's premise. The two refusals stay; there is no `--yes`. Under TN1 this is the one way an existing project becomes managed. |
| **D2214** | `fugaro setup --managed` | Drop it; every creating run records its budget; a passed `--managed` is refused as gone (`retiredCredentialsFlag` pattern) | **2026-11-21: as recommended.** |
| **D2215** | Can a later explicit `--budget` change a recorded amount? (F7) | Yes, on `update` (managed installations) and `managed flag`, conditioned on the read generation, printing old and new amounts. A default never overwrites | **2026-11-21: as recommended.** Amended after review, which leaves the intent unchanged: the rewrite stands behind the same agent-session and run-process refusals as the marking (the fences in `update_selfflag.go` sit after the early return the rewrite would use), and it accepts marker versions 2–4 (F15). |
| **D2216** | Where does `--billing-account` come from on a creating run? (F6) | It defaults to the linked account (`--link-billing` or the wizard's pick). With none, record the amount and print the one command that adds the alert | **2026-11-21: as recommended.** It relies on R5: without the carry, the next update would plan the alert's deletion. |
| **D2217** | How far does "any project we touch must be managed, else stop" reach? | Every command that writes to a project refuses an unmanaged one, through `managedRequired`, naming `fugaro managed flag`. The exemptions are setup's creating run, `managed flag`, and read-only commands and modes (A5). A test that walks the command tree keeps the classification complete | **2026-11-21: as recommended.** |

### Resolutions (one defensible answer)

- **R1 (F5).** `fugaro restore` keeps its pair rule, defaulting to the old project's recorded figures. `createArgs` stops passing `--managed` (T2c).
- **R2 (D2216).** The trio check in `init.go` reads explicit flags only, so a default never trips it.
- **R3 (D2214).** Both doors refuse `--managed` with one "gone" sentence, before anything is read.
- **R4 (F3, F10).** The answers file gains optional `budget` (1 to 10,000,000) and `budget_currency` members. Adding members leaves the version at 1. A document without them gets the defaults.
- **R5 (F13).** The budget block is carried from the previous run's Terraform variables when no budget flag is passed, the way `infra.CarriedManagedDeployerScoped` carries its key. An explicit trio replaces it. `flagHints`' "nothing records" sentence goes.

## Tension

| # | Between | Ruling |
| --- | --- | --- |
| **TN1** | D2212 (default everywhere) against D1553 (`update` marks an installation when given its budget) | The first answer, given 2026-11-21, was "every update marks it". It was **superseded the same day** by the owner's principle: **`update` never marks a project. On an unmanaged installation it stops and names `fugaro managed flag`. D1553's self-marking step is removed.** Not the recommendation either way (the recommendation was to keep `--budget` as the trigger). Followed through in F11, D2217 and tasks T3 and T7. |

No tension is open.

## Open items

None that block the plan. Checks 860 and 861 are the owner's.

## Checks

The spec reserved none. This reconciliation takes Checks 860–869 and uses two.

- **Check 860 (owner-run, after the release that ships T3, T4 and T7).** On an unmanaged sandbox installation:
  1. `fugaro update` stops, writes nothing, and names `fugaro managed flag`.
  2. `fugaro managed flag` with no arguments records `{"amount":100,"currency":"USD"}` and the label.
  3. `fugaro update` then converges.
  4. A second plain `fugaro update` plans no change to the budget alert (R5).

  Run once with an EUR billing account, it also settles U1.
- **Check 861 (owner-run, before upgrading to that release).** `fugaro managed status` on each of the owner's installations. Any that is unmanaged is flagged with `fugaro managed flag` first, so no upgrade meets the stop (F11).

## UNVERIFIED

- **U1.** Whether Cloud Billing refuses a budget whose currency differs from the billing account's. Check 860 settles it.
- **U2.** Whether any of the owner's installations is still unmanaged. Check 861 settles it.
- **U3.** How many existing tests run a writing command against an unmanaged fake installation. Every one of them meets the new stop (T3 for `update`, T7 for the rest), and the reviewer counted many among the update tests. The run of each task counts them first, and makes managed the default of the shared fixture rather than editing each test. If the count is over five files, the run reports it, and the task is split into a fixture task and the refusal.
