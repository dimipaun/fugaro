# Composite management commands: implementation plan

Builds `spec.md` as amended by `reconciliation.md`. **Plan only: nothing is implemented and no task is
approved; each task needs the owner's explicit go.** Answers that shaped it (2026-11-07): D1560 bare
`fugaro apply` (no rename needed, F1), D1561 one step registry over the initflow stage names (amended), D1562 local pending file, D1563 keep the
change and mark pending on a failed step.

## Conventions every task follows

- `CLAUDE.md` ("Things that bite": `fugaro update` is the whole upgrade; docs are tested; a field never
  changes meaning; no `.github/workflows` edits; comments say why), `.claude/rules/console.md` for the
  console task, `.claude/rules/terraform.md` and `.claude/rules/credentials-service.md` for AP2.
- Hermetic tests only (`internal/testutil`, `internal/gcpfake`, `t.TempDir()`); no live cloud action by an
  agent; no secret-shaped literal. Targeted tests, `GOFLAGS=-p=1`, never a whole package.
- A new wire shape ships with its schema in `schemas/` and a version (F9). The plugin skill lint
  (`maxSkillBytes`, named commands and flags) is run for any skill edit.
- Anything an operator must do after the release goes under `### For operators` in
  `docs/releases/vX.Y.Z.md` in the same PR; the answer here is "run `fugaro update`" and nothing else.

## External dependencies (the no-prompt plan, `docs/no-prompt-policy`, D1540-D1559)

| Edge | Needed by | Why |
| --- | --- | --- |
| NP-confirm: confirmations removed everywhere | AP3-AP6 | A chain never asks (spec rule 5) |
| NP-lease: image builds lease from the job budget (D1543) | AP2, AP4 | The image-build step costs money |
| NP-cap: cap raising is owner-only (D1542) | AP3 | The over-cap error names that command |
| NP-gate: managed gate is audit-only (D1545) | AP2 | `publish-policy` and `deploy` must not refuse an admitted plan with a typed prompt |

Until NP slices land the tasks may be built, but their end-to-end check (390-392) cannot pass. **The no-prompt plan's audit-only gate (D1545) removes a refusal this plan's D1573 and D1576 lean on; those two rules are stated here and tested here, not inherited.**

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| Stages dedupe and keep their order; publish and deploy run once for N changes | AP2 subset test, AP3 test; check 391 |
| A step that fails leaves the change saved and the step pending, exit non-zero (D1563) | AP3 test; check 392 |
| Pending file holds step names, never a value, and is versioned (D1570) | AP1 test and schema |
| Repo-layer writes never publish, commit or open a PR (D1564) | AP5 test |
| No message tells the user to run publish/update; `fugaro apply` is the only named command (F10) | AP9 lint |
| Agents/unmanaged projects follow the no-prompt rules (D1571) | AP3 test with the agent marker |
| Console reads only the one new status verb through the fence (D1569); the `settings.edit` row passes `--no-apply` and its `--yes` covers only the local write (D1572) | AP8 equality test and spawn test |
| Each extracted stage keeps its door's agent refusal, managed gate and loosening judgement (F5) | AP2 test per stage under the agent marker, plus a mutation check (drop the refusal, the test must fail) |
| `image add` in an agent session refuses executable content (`--inline`, `--dockerfile`, `--default`) unless managed and judged (D1573) | AP4 test and mutation check |
| `settings set budget.*` + publish never raises a cap in an agent session (D1576) | AP5 test |
| An entry is bound to project name and GCP project ID; `apply` prints diffs, re-judges loosening, escapes the change text; a deleted file is detected (D1575) | AP3 tests |
| `--no-apply` then `apply` reaches no billable build unasked on an unmanaged project before the no-prompt slices land (F6) | AP3 test (the build step refuses without a lease) |
| `deploy` is per repository; unreachable repositories stay pending (D1574) | AP3 test |

## Slices and tasks

Slice A (inert foundation): AP1, AP2, AP3. Slice B (commands): AP4-AP6. Slice C (messages, console,
enforcement, notes): AP7-AP10.

### AP1: the pending store and the change-to-stage table (over initflow)
- **Files:** `internal/apply/pending.go`, `internal/apply/steps.go`, `internal/apply/pending_test.go`, `internal/apply/steps_test.go`, `schemas/pending-steps.schema.json` (all new).
- **Interfaces:** `steps.go` is **a table from a change (setting key, resource type) to initflow stage names plus the one non-stage step, `catalog-build`**; ordering and dedupe come from `internal/initflow` (`Names()`, `Needs()`), not from a second registry. `pending.go` is the local file beside the project config: entries bound to the project name and GCP project ID, a `last_apply` stamp, no value ever stored (D1570, D1575).
- **Tests:** `TestStepsTableNamesOnlyKnownStages`, `TestPendingNeverHoldsAValue`, `TestPendingRefusesAProjectMismatch`, `TestPendingFileVersioned`. **Command:** `go test ./internal/apply -count=1`.
- **Depends on:** none. **Commit:** `apply: the pending store and the change-to-stage table (AP1)`. **Red step:** `TestStepsTableNamesOnlyKnownStages` fails against an unknown stage name.
- **Reviewer:** standard.

### AP2: run a named subset of the stages (generalise `accountsAddStages`)
- **Files:** `internal/cli/init_stages.go`, `internal/cli/accounts_add_converge.go`, `internal/cli/init_policy_migrate.go`, `internal/cli/init_policy_publish.go`, `internal/cli/update.go`, `internal/cli/stages_subset_test.go` (new).
- **Interfaces:** consumes AP1's table; produces `stagesNamed(e, names...)` (what `accountsAddStages` does by hand), used by `accounts add` and by `apply`. `update` keeps running the whole list; no behaviour change.
- **Tests:** `TestStagesSubsetKeepsOrderAndNeeds`, `TestEachStageKeepsItsAgentRefusalGateAndLooseningJudgement` (per stage, under the agent marker), `TestAccountsAddStillRunsItsThree`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'TestStagesSubset|TestEachStage|TestAccountsAdd|TestUpdate' -count=1`.
- **Depends on:** AP1; external NP-gate. **Commit:** `cli: run a named subset of the update stages (AP2)`. **Red step:** a subset test missing a `needs` edge fails. **Mutation check:** remove the policy stage's agent refusal in the subset path; the per-stage test must fail.
- **Reviewer:** opus + mandatory independent review (in-run level 3: policy publish, managed gate).

### AP3: `fugaro apply`, the `--no-apply` helper, `apply --status`
- **Files:** `internal/cli/apply.go`, `internal/cli/apply_test.go` (new), `internal/cli/root.go`, `schemas/apply-status.schema.json` (new).
- **Interfaces:** consumes AP1/AP2; produces `fugaro apply [--json]`, `fugaro apply --status [--json]`, and `addNoApplyFlag(cmd)` used by every management command (D1565).
- **Tests:** `TestApplyRunsEachPendingStepOnce`, `TestApplyNothingPending`, `TestApplyFailureKeepsPending`, `TestApplyUnderAgentMarker`, `TestApplyPrintsEachStepsDiffAndRejudgesLoosening`, `TestApplyDetectsADeletedPendingFile`, `TestApplyDeploysOnlyTheCheckoutsRepository`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'TestApply' -count=1`.
- **Depends on:** AP1, AP2; external NP-confirm, NP-cap, NP-lease. **Commit:** `apply: the one command that runs what is pending (AP3)`. **Red step:** `TestApplyRunsEachPendingStepOnce` fails with two queued changes naming the same step.
- **Reviewer:** opus + mandatory independent review (it runs the policy publish and the deploy).

### AP4: `image add` / `image rm` run their chain
- **Files:** `internal/cli/image_catalog.go`, `internal/cli/image_build_catalog.go`, `internal/cli/image_catalog_apply_test.go` (new).
- **Interfaces:** declares the policy stage, `catalog-build` (leased, D1543) and the redeploy for an entry; `--no-apply` leaves them pending. **New agent rule (D1573): in a coding agent's session `--inline`, `--dockerfile` and `--default` refuse unless the project is managed and judged.**
- **Tests:** `TestImageAddAloneMakesTheImageUsable` (asserts the deployed job's image equals the built entry's digest, through `catalogDockerAsk`'s inputs and the fake backend), `TestImageAddNoApplyLeavesPending`, `TestImageAddRefusesExecutableContentForAnAgent`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'TestImageAdd|TestImageCatalog' -count=1`.
- **Depends on:** AP3; external NP-lease. **Commit:** `image: add and rm apply their own follow-up (AP4)`. **Red step:** add with fakes ends with a pending step before the change. **Mutation check:** delete the agent refusal; the executable-content test must fail.
- **Reviewer:** opus + mandatory independent review (in-run level 3): an agent-reachable publish, Cloud Build of agent-written content and redeploy.

### AP5: `settings set` / `unset` run their chain
- **Files:** `internal/cli/settings_set.go`, `internal/cli/settings_apply_test.go` (new), `internal/settings/steps.go` (new: key to steps table).
- **Interfaces:** each key in the settings registry declares its steps (or none); a repo-layer write declares none and prints that it is live once merged (D1564).
- **Tests:** `TestSettingsSetPublishesOnlyWhatTheKeyNeeds`, `TestRepoLayerWriteNeverPublishes`, `TestEverySettingKeyDeclaresItsSteps`, `TestBudgetKeyPublishRefusedInAnAgentSession` (D1576). **Command:** `GOFLAGS=-p=1 go test ./internal/cli ./internal/settings -run 'TestSettings' -count=1`.
- **Depends on:** AP3. **Commit:** `settings: set and unset apply their own follow-up (AP5)`. **Red step:** the every-key test fails on a key with no declaration. **Reviewer:** standard.

### AP6: `accounts rm`, `project repos`, `models`, `workspace`, `project policy publish`
- **Files:** `internal/cli/accounts_rm.go`, `internal/cli/project_enroll.go`, `internal/cli/project_cmd.go`, `internal/cli/models_add.go`, `internal/cli/models_rm.go`, `internal/cli/workspace.go`, `internal/cli/chain_apply_test.go` (new).
- **Interfaces:** declares steps per command. **`accounts add` is already composite (`accountsAddStages`) and is not rewired: its failure rule stays "error, re-run repairs" (F10); only its text is audited in AP7.** `project policy publish` stays the policy stage's own door.
- **Tests:** `TestAccountsRmChain`, `TestReposAddChain`, `TestModelsAddChain`, `TestWorkspaceWritesChain`, `TestPolicyPublishStillTheStagesDoor`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'TestAccountsRm|TestProjectRepos|TestModels|TestWorkspace|TestPolicyPublish' -count=1`.
- **Depends on:** AP3. **Commit:** `cli: accounts rm, repos, models, workspace apply their own follow-up (AP6)`. **Red step:** a chain test that ends with a pending step. **Reviewer:** opus + mandatory independent review (policy publish, accounts grants).

### AP7: the audit of "now run X" texts, doctor and the plugin skills
- **Files:** the F11 audit set not owned by AP4-AP6 (the output of the F11 grep minus the files AP2-AP6 own: `doctor*.go`, `config_show.go`, `exec.go`, `history_rollover.go`, `image_check_catalog.go`, `image_layer.go`, `image_promote.go`, `image_refresh*.go`, `imagecheck.go`, `init_credentials.go`, `init_fugaroyaml.go`, `init_images.go`, `init_new_machine.go`, `init_project.go`, `init_retire.go`, `launch_budget.go`, `operator_write.go`, `pair.go`, `project_job.go`, `recipes_skew.go`, `restore.go`, `review.go`, `reviewverify.go`, `run.go`, `sharedcfg.go`, `skillwarn.go`, `upgrade_cloud.go`, `user.go`, `validate.go`, and the `accounts_add_converge.go` text), `plugin/skills/*/SKILL.md`, `CLAUDE.md`, `docs/` pages that tell the user to publish/update.
- **Interfaces:** classifies each text (a)/(b)/(c) (F11); (a) moves into its command; (b) names `fugaro update` or `fugaro apply`.
- **Tests:** `go test ./plugin -run 'TestPlugin|Skill' -count=1`; `go test ./internal/cli -run Docs -count=1`; the touched files' targeted tests.
- **Depends on:** AP4, AP5, AP6. **Commit:** `cli: no message sends the user to publish or update by hand (AP7)`. **Red step:** AP9's lint run first (expected red). **Reviewer:** standard (the skills edit makes the path-routing rule send it to level 3 in-run: CLAUDE.md/plugin skills are in the security routing list).

### AP8: console read of the pending steps
- **Files:** `internal/cli/project.go` (`consoleReadPaths` row), `internal/cli/docs_desktop_console_test.go` (`consoleLaunchPaths`), `console/src/main/spawn.ts` and its test (the new read row, **and the `settings.edit` row gains `--no-apply`; one new exact-argv row for `fugaro apply` (a write verb, D1572)**), `console/docs/json-contract.md`, `docs/design/desktop-console.md` (row).
- **Tests:** the Go equality test between the two lists; a spawn test that `settings.edit` carries `--no-apply` in both phases; `console` typecheck and the spawn unit test.
- **Depends on:** AP3. **Commit:** `console: the pending-steps read joins the fence (AP8)`. **Red step:** the equality test fails with only one side updated. **Reviewer:** opus + mandatory independent review (the console spawn allow-list is security-sensitive).

### AP9: enforcement
- **Files:** `internal/cli/apply_enforce_test.go` (new), `internal/cli/testdata/management_commands.golden` (new).
- **Tests:** every command in the golden list declares its steps and, run without `--no-apply` against fakes, ends with no pending step (spec's test); a text lint over user-facing strings fails on "run fugaro (config publish|project policy publish|update|image build|image refresh)" outside the (b) allow-list.
- **Depends on:** AP4-AP7. **Commit:** `cli: enforce composite commands (AP9)`. **Red step:** the lint is red until AP7 lands. **Reviewer:** standard.

### AP10: release notes and live checks
- **Files:** `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`"), `website/data/release-summaries.yaml`, `docs/gcp-live-checklist.md`, `scripts/live-checks/check-390.sh`, `check-391.sh`, `check-392.sh` and their fake-gcloud tests (new). **OWNER-RUN** checks, one idempotent script each (setup/assertions/teardown, PASS/FAIL, `--dry-run`, refusing `belong`).
- **Depends on:** AP9. **Commit:** `docs: composite commands release actions and checks 390-392 (AP10)`. **Reviewer:** standard.

## Checks (reserved 390-399)

| Check | Measures | Runner |
| --- | --- | --- |
| 390 | `fugaro image add` alone: the deployed job's image digest equals the built entry's digest, and `apply --status --json` lists nothing | owner, after AP4 |
| 391 | Three `--no-apply` changes then one `apply`: publish and deploy run once each | owner |
| 392 | A forced publish failure leaves the change saved, the step pending, exit non-zero; `fugaro apply` finishes it | owner |
| 393-399 | Reserved | |

## Parallel execution

Edges: AP1 -> AP2 -> AP3 -> {AP4, AP5, AP6, AP8} -> AP7 -> AP9 -> AP10. AP1 and (the read half of) AP2
need nothing else.

| Level | Tasks |
| --- | --- |
| 0 | AP1 |
| 1 | AP2 |
| 2 | AP3 |
| 3 | AP4, AP5, AP6, AP8 |
| 4 | AP7 |
| 5 | AP9 |
| 6 | AP10 |

Critical path: AP1, AP2, AP3, AP4 (or AP6), AP7, AP9, AP10. AP2 and AP3 are sequential because AP3's
`apply` calls the step functions AP2 extracts; AP7 follows the command tasks because it touches files
they write (`image_catalog.go` and the other families) and the lint it satisfies is theirs.

### Shared files (no level holds two writers)

| File | Writers (level) |
| --- | --- |
| `internal/cli/init_stages.go`, `init_policy_migrate.go`, `init_policy_publish.go`, `update.go`, `accounts_add_converge.go` | AP2 (1) only |
| `internal/cli/root.go` | AP3 (2) only |
| `internal/cli/image_catalog.go`, `image_build_catalog.go` | AP4 (3) only |
| `internal/cli/settings_set.go`, `internal/settings/steps.go` | AP5 (3) only |
| `internal/cli/accounts_rm.go`, `project_enroll.go`, `project_cmd.go`, `models_add.go`, `models_rm.go`, `workspace.go` | AP6 (3) only |
| `internal/cli/project.go`, `docs_desktop_console_test.go`, `console/src/main/spawn.ts` | AP8 (3) only |
| the rest of the F11 set (`doctor*.go`, `run.go`, ...), `plugin/skills/*`, `CLAUDE.md` | AP7 (4) only |
| `docs/releases/*`, `docs/gcp-live-checklist.md`, `scripts/live-checks/*` | AP10 (6) only |

## Post-deploy actions

None beyond `fugaro update`. A missing pending file means nothing is recorded as owed; whether that is true is checked by the `last_apply` stamp `doctor` compares (D1575), not assumed. Drift other than the project layer's is not recomputed (F12, UNVERIFIED).
