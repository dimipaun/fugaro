# Managed flag ergonomics: reconciliation

**Checked against `origin/main` at d49063235, 2026-11-21. Where this document disagrees with [spec.md](spec.md), this document wins.**

The spec was written on 2026-11-13, before the no-prompt policy's last tasks and the update self-flag had settled into the text it cites. This file checks every claim in it against the code, records the owner's answers to its open decisions (D2211–D2214) and to the two this reconciliation adds (D2215, D2216), and lists the one tension the answers opened (T1), which the owner ruled on the same day.

## Assumptions

- **A1.** "The setup wizard" means the first run of `fugaro setup` at a person's terminal: the question table `setupQuestions` (`internal/cli/setup.go`) and the billing question (`askBilling`, `internal/cli/billing_prompt.go`). The console's first-run wizard reaches the same questions through `--answers-file` (`internal/cli/setup_answers.go`, `schemas/setup-answers.schema.json`).
- **A2.** "The amount" is the one figure the marker records (`managed.Budget` in `internal/infra/managed/marker.go`) and the one the installation's Terraform budget alert is created with (`infra.Budget`, set in `internal/cli/init.go`). The plan keeps them the same figure.
- **A3.** Nothing here changes the marker's shape (`fugaro/managed.json`, version 4 with `budget.amount` and `budget.currency`), so no format version moves (the spec's own closing paragraph).
- **A4.** "Non-interactive" means everything `canType()` answers no for: no terminal, `--json`, `--non-interactive`, a pipe, a coding agent's session, and the console's answers file.

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| The wizard asks for the budget (D2210) | No budget question. The table asks five questions (`--gcp-project`, `--location`, `--name`, `--rigor`, `--bake-repos`) and `askBilling` asks a sixth; the budget is only `--budget`, `--budget-currency` and `--billing-account` (`internal/cli/init.go`, flag registration). |
| `managed flag` reads the project from the installation (D2213) | `fugaro update` already does it: given `--budget` and `--budget-currency` on an installation that is not managed, it marks that installation itself, with no project ID argument and no prompt, and prints `managedFlagStatement` before it writes (`internal/cli/update_selfflag.go`, `selfFlagUnmanaged`; D1553). |
| A refusal that suggests an amount | `checkCreateProject` (`internal/cli/init_project.go`) suggests `--budget 50 --budget-currency USD` in the refusal for `--managed` without the trio; the code comment beside it says there is "deliberately no constant here and none anywhere else" (D64). |
| A first-run default that is a suggestion | `--location` suggests US; `--rigor` has no suggestion and a scripted run without it is refused (D900). The spec's "each question carries a suggestion Enter takes" holds for one and not the other (F4). |

## Findings

**F1 — Rule: D2213's recommendation is a prompt the no-prompt policy forbids.** The spec recommends "a typed confirmation of the project ID at the owner's terminal". `CLAUDE.md` ("Fugaro asks for nothing … no typing a name back") and D1541 (consent by argument *instead of* a prompt, `docs/no-prompt-policy/reconciliation.md`) rule it out. The recommendation was withdrawn and D2213 was asked again with options that contain no prompt.

**F2 — Exists: `fugaro update` already marks the selected installation without the ID.** `selfFlagUnmanaged` (`internal/cli/update_selfflag.go`) writes the marker and the `fugaro_managed` label for the selected installation when it is given the budget, with the same two fences as `managed flag` (a coding agent's session, a Fugaro run's own process). So requiring the exact project ID on `managed flag` protects nothing that `update --budget …` does not already allow without it. This is the premise of D2213's answer.

**F3 — Wrong: the wizard has no budget question.** D2210 ("the wizard prompts, default 100 USD") therefore *adds* a row to `setupQuestions`. A row needs its flag (`TestEveryWizardQuestionHasAFlag`) and a member in the answers file (`setupAnswerQuestions`, `TestSetupAnswersSchema`, `schemas/setup-answers.schema.json`), or the console's wizard cannot answer it.

**F4 — Wrong (minor): not every wizard question carries a suggestion.** Rigor has none on purpose (D900; `askRigor`). D2210's 100 USD suggestion follows the place question's pattern, not rigor's. No design change; recorded so the help text does not repeat the claim.

**F5 — Gap: D2211's default reaches four commands, and the spec names two.** `--budget-currency` is registered on `fugaro setup` and `fugaro update` (the same `newInitCmdWith`), on `fugaro managed flag` (`internal/cli/managed_flag.go`) and on `fugaro restore` (`internal/cli/restore.go`). Restore's currency already defaults to what the old project's creation fact records, and travels with `--budget` as one pair. Resolved as **R1** below.

**F6 — Gap: the budget needs a billing account the wizard never asks for.** The trio rule (`init.go`: `--budget`, `--budget-currency` and `--billing-account` together) exists because the Terraform budget alert sits on a billing account. The wizard's billing question picks the account to link (`askBilling` writes it into `--link-billing`'s field) and nothing ties the two. Asked as **D2216**.

**F7 — Gap: write-once amounts become wrong once a default records them.** `WriteFlag` (`internal/infra/managed/marker.go`) refuses a project that already records an amount ("the amount is the owner's decision at the time it was taken"), and `managed flag` says the same. Once D2212 records 100 USD that nobody chose, write-once leaves the owner no way to correct it. Asked as **D2215**.

**F8 — Wrong: two help texts contradict the code.** `managed flag`'s long help says "this command takes no flag at all" while registering `--budget` and `--budget-currency`. `checkCreateProject`'s `--managed` refusal ends "before a person, who types its ID", which contradicts D1541. Both are rewritten by the tasks that touch those lines (T4, T2c).

**F9 — Gap: dropping `--managed` (D2214) touches more than the spec counted (the spec's U1, now checked).** The flag is spelled in `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/managed.md`, the generated `website/content/cli-reference.md` (`go run ./ee/clidoc`, pinned by `docs_website_cliref_test.go`), `internal/cli/restore.go` (`createArgs` passes it to the child setup) and 20 test files under `internal/` (`init_budget_test.go` alone has 31 uses). `docs/gcp-live-checklist.md`, `docs/release.md` and `docs/releases/v0.*.md` mention it historically and stay as written. The plugin tests check every flag a skill names against the CLI, so the skill edit lands in the same pull request as the removal.

**F10 — Gap: the console's wizard sends no budget.** `console/src/main/wizardrun.ts` builds the answers document and sends no budget. Under D2212 a run that gets none takes the default, so nothing breaks. Under D2210 the person using the console should be asked the same question the terminal asks (D2904's rule that the console's wizard answers the wizard's own questions), so the plan holds a console task.

**F11 — Rule: T1's answer makes `fugaro update` a migration.** Every installation that is not managed becomes managed on its next `fugaro update`. The HARD RULE (`CLAUDE.md`) asks for a test that an installation at the previous version converges, and for the in-flight notice to name the step (`internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md`, `TestUpdateLeavesARunInFlightRunning`). A run already in flight keeps its records. It meets the marker at its next managed apply, which is a step the notice names rather than denies. `update`'s self-flag is refused in a coding agent's session; that fence stays, so an agent's `fugaro update` on an unmanaged installation is refused at that step, as `update`'s cloud writes already are.

**F12 — Exists: the console and the mobile app never read the marker's currency (the spec's U2, now checked).** No file under `console/src`, `mobile/` or `shared/` reads `managed.json` or a budget currency from it. A default changes who writes the field, never its shape (A3).

## Decisions

The spec reserved D2210–D2219. D2210 was answered in the spec; D2211–D2214 are its open questions; D2215 and D2216 are added here; D2217–D2219 stay reserved and unused.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| **D2210** | The wizard's budget suggestion | A prompt suggesting 100 USD, which Enter takes | **2026-11-13** (in the spec): as stated. Amended by F3: this is a new question row, not a changed one. |
| **D2211** | Does `--budget-currency` default to USD? | Yes, on `setup`, `update` and `managed flag`; restore keeps its own rule (R1) | **2026-11-21: as recommended.** It is a unit, not a spend decision. A non-USD billing account passes `--budget-currency`, or GCP refuses the budget alert at apply (U1). |
| **D2212** | With nobody to ask, does the amount default to 100? | No: the amount stays required there; only Enter at a prompt takes the suggestion | **2026-11-21: not the recommendation — default 100 everywhere.** Followed through: D64's "the amount is required, with no default" is retired. A scripted first run, `fugaro managed flag` with no `--budget`, and `fugaro update` (T1) all record 100 USD. The constant lives in one place (contract C1); the code comment in `checkCreateProject` that forbids one goes with the `--managed` case it sits in (T2c). |
| **D2213** | Where does `managed flag` get the project? (amended by F1) | The project ID becomes optional. Omitted, it is the selected installation's project, named in the statement printed before the write, as `update` does. Given, it must match, as today | **2026-11-21: as recommended.** This amends D1541's consent by argument for this one command, on F2's premise. The command keeps its two refusals and registers no `--yes`. |
| **D2214** | `fugaro setup --managed` | Drop it. Every creating run records its budget. A passed `--managed` is refused with a sentence saying it is gone (the `retiredCredentialsFlag` pattern) | **2026-11-21: as recommended.** |
| **D2215** | Can a later explicit `--budget` change a recorded amount? (F7) | Yes. An explicit `--budget` on `update` or `managed flag` rewrites the marker's amount, conditioned on the generation it read, and the run prints the old and new amounts. A default never overwrites a recorded amount | **2026-11-21: as recommended.** |
| **D2216** | Where does `--billing-account` come from on a creating run? (F6) | It defaults to the account the run links (`--link-billing`, or the wizard's billing pick). With none linked, the amount is still recorded in the creation fact, and the run prints the one command that adds the alert later | **2026-11-21: as recommended.** |

### Resolutions (one defensible answer)

- **R1 (F5).** `fugaro restore` keeps its pair rule. Its default is the old project's recorded currency and amount, and a USD default would replace a recorded choice. `createArgs` keeps passing both explicitly; it stops passing `--managed` (T2c).
- **R2 (D2216 on update).** `fugaro update` given no billing account records the amount in the marker as T1 requires. It creates no alert, and prints the same one command D2216's no-account branch prints. The trio check in `init.go` reads explicit flags only, so a default never trips it.
- **R3 (D2214).** The update door already declares `--managed` hidden and refuses it by name. After the removal, both doors refuse it with one "gone" sentence naming what replaced it, before anything is read.
- **R4 (F3, F10).** The answers file gains an optional `budget` member (an integer from 1 to 10,000,000) and an optional `budget_currency` member. They are new members, so the version stays 1 ("a field never changes meaning": adding one is fine). A document without them gets the defaults (D2211, D2212).

## Open tensions

| # | Between | Ruling |
| --- | --- | --- |
| **T1** | D2212 (default everywhere) against D1553 (`update` marks an installation when *given* its budget once) | **2026-11-21, not the recommendation: every `fugaro update` marks an unmanaged installation at 100 USD, with no `--budget` needed.** The recommendation was to keep `--budget` as `update`'s trigger. Followed through in F11 and task T3: D1553's "given its budget once" is amended to "always". `managed flag` stays the owner's command for a project `update` does not reach. |

No tension is open.

## Open items

None that block the plan. Check 860 (below) is the owner's live check after the release.

## Checks

The spec reserved no check numbers. This reconciliation takes Checks 860–869 and uses one.

- **Check 860 (owner-run, after the release that ships T3).** On a sandbox installation that is not managed, `fugaro update` with no budget flag. Expected: the in-flight line and the statement name the marking and 100 USD, `gs://fugaro-runs-<project>/fugaro/managed.json` records `{"amount":100,"currency":"USD"}`, the project carries `fugaro_managed=true`, and with no billing account the run prints the one command that adds the alert.

## UNVERIFIED

- **U1.** Whether Cloud Billing refuses a budget whose currency differs from the billing account's currency, or converts it. The plan assumes it refuses, which is why D2211's help says to pass `--budget-currency` on a non-USD account. Check 860, run once with an EUR account, settles it; the owner runs it.
- **U2.** Whether any installation the owner runs is still unmanaged. If none is, T1's migration reaches nobody today, and its converge test still pins the behaviour. `fugaro managed status` per installation settles it.
