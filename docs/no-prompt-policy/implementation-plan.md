# No-prompt policy — implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). **Status: plan only;
nothing is implemented; no task is approved.** Answering a decision is not approving a task: each task below
needs the owner's explicit go. D1540–D1555 are all answered (2026-11-07; D1552–D1555 as recommended, delegated to the PR).
Owner answers (2026-11-07) that shaped the plan: D1540 values stay, never hang; D1541 unmanaged refuses and
names the flag-by-argument command (the shape is D1553: setup always managed; `update` flags an unmanaged installation itself given its budget once; flagging refused in an agent session); D1542 cap raise is owner-session only;
D1543 builds lease an estimated cost, the count bound goes; **D1544 keep `--ask`** (not the recommendation),
remove `--yes`/`--non-interactive`; **D1545 audit-only gate** (not the recommendation); D1546 no meta-repo
prerequisite; D1547 harness plus lint; D1548 observe: builds proceed and record; D1549 the integrity refusals
stay; **D1550 the rate is queried from Cloud Billing at run time** (not the recommendation); D1551 delete/replace
of data-bearing types stays refused. Checks 380–389 and D1540–D1559 are reserved here.

## Order against open pull requests

#1870 (NP3: image build asks nothing) **merged 2026-11-07**. #1804 (a person at a real terminal on a managed project) and #1891 (CV1: the cover-table row for `catalog_check_records`) are **open**; on `main` `cover.go` lacks the row, so T9 starts after both merge. Merging them first costs nothing: they remove prompts and add one cover row.

## Conventions every part follows

- `.claude/rules/terraform.md` for `internal/infra/**` and `deploy/terraform/**`; `CLAUDE.md` "Things that bite" —
  HARD RULE (`fugaro update` is the whole upgrade; a format change ships its migration inside `update` with a
  test that the previous version converges), "Docs are tested", "A field never changes meaning", "Never edit
  `.github/workflows/`".
- Targeted tests with `GOFLAGS=-p=2` (CLAUDE.md's default); `-p=1` only on a machine the owner says is loaded.
  Finish each task with `go test ./internal/cli -run Docs -count=1`.
- Hermetic: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`. No secret-shaped literal. No agent performs
  a live check; each owner-run check is ONE idempotent script under `scripts/live-checks/` with `--dry-run`, a
  hermetic fake-gcloud test, and it refuses the `belong` project.
- A change that touches a flag a plugin skill or the console names changes the skill and the console in the
  same task.
- Security-sensitive tasks (IAM, cover tables, the gate, agent detection, the budget rules, `CLAUDE.md`, rules,
  plugin skills, the console's spawn list) are **opus + mandatory independent review**: in-run level 3 via
  `--review-levels`, never also a local reviewer.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No command waits on closed stdin (data reads fail on EOF, they do not wait) | T2 harness, T1 lint |
| Only the `--ask` helper and the value-entry helper read stdin for a decision (an AST rule; data reads exempt by name) | T1 |
| `--ask` never asks in an agent session or with `--json` | T1 |
| No raise form is open to an agent session: `budget set`, `budget resume`, the policy ceilings | T3 |
| Over-cap error: estimated cost, cap, spent, the one command, non-zero exit | T8 |
| A missing or unknown Cloud Billing price **refuses**; a missing machine type refuses; local config cannot supply a rate | T8 |
| Observe mode: the build proceeds and records; an absent mode is observe; only maxReserve and kill switches bite; `doctor` says so | T8, T9 |
| The integrity refusals stay (listed in T9) and are in `TestIntegrityRefusalsStillRefuse` | T9 |
| Out-of-table grants apply, are recorded and warned (session and `doctor`); the record is harder to erase | T9 |
| The marker and catalog-check spec format changes converge an installation at the previous version | T10 |
| No doc, skill or console file says `--yes`/`--non-interactive` after T11; `--ask` stays documented | T11 |

### Trust probes (written here so the reviewers attack them)

- The owner session is identified only by `internal/agentenv` markers: `env -u CLAUDECODE -u
  CLAUDE_CODE_ENTRYPOINT fugaro budget set …` or a direct `/config` write defeats it. Today `budget set --yes`
  has **no** agent check, so D1542 adds a fence — a courtesy one.
- Audit-only: a hostile agent can apply an out-of-table IAM grant (the `admins`/`budget_admins` tfvars feed
  `iam.tf`), delete objects outside D1551's list, then erase the record unless T9's hardening holds.
- The lease estimate is client-side; a direct `gcloud builds submit` bypasses it.
- A billing read adds a network failure mode and an IAM grant to the launch path (T5 of the reconciliation).

## Slice 0 — the rail (ships inert)

### T1 — sanctioned helpers and the static lint
- **Files:** `internal/cli/managed_ask.go` (the existing `--ask` logic: **reused and extended, not duplicated** by a new `askconfirm.go`), `internal/cli/valueentry.go` (new),
  `internal/cli/noprompt_lint_test.go` (new).
- **Interfaces:** `askConfirm(cmd, question)` (a thin entry over `managed_ask.go`) (reads only under `--ask`, owner terminal, not JSON, not an agent
  session) and `readValue(cmd, spec)` (fails at once naming the flag/env when stdin is not a terminal).
  Consumes `stdinIsTerminal`, `agentMarker`.
- **The lint is an AST rule**, not a grep: it fails on a call that reads a decision from `cmd.InOrStdin()` /
  `os.Stdin` (`bufio.NewReader`, `NewScanner`, `ReadString`, `term.ReadPassword`, `readConfirmLine`) outside the
  two helpers, and it does **not** flag `stdinIsTerminal(cmd.InOrStdin())` (a predicate, 58 sites) nor the named
  data reads (`run.go` `--task-file -`, `exec.go`, `creds.go` `readBundleValue`'s data form), which are an explicit
  exemption list in the test. **The rule follows an alias**: `in := cmd.InOrStdin(); bufio.NewReader(in)` is caught (the AST rule tracks the
  identifier the stdin expression was assigned to within the function).
- **Tests:** `TestAskConfirmNeverReadsOutsideAsk`, `TestAskConfirmRefusesInAnAgentSession`,
  `TestReadValueFailsAtOnceWithNoTerminal`, `TestAskInAnAgentSessionFailsFastAndDoesNotHang`, `TestLintCatchesAnAliasedStdinReader`, `TestNoDecisionReadOutsideTheTwoHelpers` (red first: it lists every
  current site; the list is the allow-list and **each of T3–T7 deletes its entries**, so it reaches empty only
  because those tasks, T15 and T4 own every file in the list: `update_workspace.go` and the `Typed` init stages are T15's, `image_build_managed.go` is T4's, `managed_ask.go` is T1's).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'AskConfirm|ReadValue|NoDecisionRead' -count=1`
- **Depends on:** none. **Commit:** `cli: the two sanctioned stdin readers and the lint that holds them (T1)`.
- **Reviewer:** opus + mandatory independent review (agent detection).

### T2 — the closed-stdin harness and the static gcloud scan
- **Files:** `internal/cli/noprompt_harness_test.go` (new), `internal/backend/gcp/gcloud_quiet_test.go` (new).
- The harness walks `NewRootCmd`, runs every leaf with stdin a closed pipe and a short deadline against fakes,
  with a per-leaf fixture or listed exemption; red today (it fails naming every prompting leaf). It **does not
  settle the gcloud question**: the calls run inside Cloud Build step scripts and the preload pipeline, so the
  second test is a **static scan** of the step-script strings in `build.go` and `catalog.go` that fails on a
  `gcloud` invocation with no `--quiet`.
- **Tests:** `TestEveryCommandRunsWithClosedStdin`, `TestEveryGcloudStepScriptIsQuiet`. **Command:**
  `GOFLAGS=-p=2 go test ./internal/cli ./internal/backend/gcp -run 'ClosedStdin|GcloudStep' -count=1`
- **Depends on:** none. **Commit:** `cli: no command waits on a closed stdin; no gcloud step prompts (T2)`.
  **Reviewer:** standard.

## Slice 1 — remove the confirmations (parallel, one family per task; every file is owned by exactly one)

### T3 — budget: every raise form, owner-session only (D1542, D1552)
- **Files:** `internal/cli/budget.go`, `internal/cli/budget_test.go`, `internal/cli/settings_write.go` and
  `internal/cli/update_managed.go` (the policy-ceiling loosening), their tests.
- Remove `confirmTyped`/`confirmTypedStrict`. **List and fence every raise form:** `budget set` raising a cap,
  `budget resume`, the `PerRunUSD`/`PerDayUSD` ceiling loosening (D1552: the same owner-session-only fence) — each refuses in an agent session and prints the exact owner command; the direct `/config` write by a
  budget admin stays an IAM matter and is listed in the PR body. `budget set` gains `--ask` (it registers none
  today).
- **Tests:** `TestBudgetRaiseRefusesInAnAgentSession`, `TestBudgetResumeRefusesInAnAgentSession`,
  `TestCeilingLooseningRefusesInAnAgentSession`, `TestBudgetRaiseNeedsNoPromptAtATerminal`. **Command:**
  `GOFLAGS=-p=2 go test ./internal/cli -run 'TestBudget(Raise|Resume|Kill|Set)|TestCeiling' -count=1`
- **Depends on:** T1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `budget: a raise asks nothing and an agent session cannot make one (T3)`.

### T4 — image build and refresh: no confirmation, the lease
- **Files:** `internal/cli/image.go`, `image_build_catalog.go`, `image_refresh.go`, `image_refresh_managed.go`,
  `image_refresh_steps.go`, `image_build_managed.go` (`ImageBuildsBound` use), their tests. **`image_refresh_managed.go` and `image_build_managed.go` lose the `ImageBuildsBound` call here** (moved from T10, so T9 and T10 do not both write `image_refresh_managed.go`).
- Remove `confirmBuild`/`buildNotConfirmable`; take T8's lease before each submit. **Tests:**
  `TestImageBuildAsksNothing`, `TestImageBuildOverCapFailsWithTheSpecError`.
- **Depends on:** T1, T8, (#1870 merged). **Reviewer:** opus + mandatory independent review.
- **Commit:** `image: a build asks nothing and leases its cost (T4)`.

### T5 — accounts, settings, config, secrets removal, models, workspace, upgrade, delete
- **Files:** `internal/cli/accounts.go`, `accounts_add.go`, `accounts_add_login.go`, `accounts_rm.go`,
  `accounts_managed.go`, `allow_delete.go`, `config_publish.go`, `config_init.go`, `settings_write.go` **(the
  y/N only; T3 owns the ceiling logic — T3 lands first, T5 after, see levels)**, `settings_set.go`,
  `settings_managed.go`, `secrets_rm.go`, `models_add.go`, `models_rm.go`, `models_use.go`, `workspace.go`,
  `upgrade.go`, tests.
- Remove the y/N and typed confirmations (`confirmSettingsWrite`, `settingsAsk`, `confirmRemoval*`); the
  owner-terminal login keeps its value entry through `readValue`. **Tests:** per command `…AsksNothing`.
- **Depends on:** T1, T3. **Reviewer:** opus + mandatory independent review (accounts add).
- **Commit:** `cli: settings, accounts, models and config writes ask nothing (T5)`.

### T6 — managed flag, unflag, project commands, and the unmanaged refusal (D1541, D1553)
- **Files:** `internal/cli/managed_flag.go`, `managed_unflag.go`, `project_cmd.go`, `project_enroll.go`,
  `restore.go`, `managed_require.go` (new), `root.go` (a command annotation), tests.
- **Self-flag in `update`/`setup`:** new `internal/cli/update_selfflag.go` (new) and the single hook in `init_stages.go` (T7 does not own `init_stages.go`; `init.go` stays T7's alone, so T6 and T7 share no file at level 1). In an agent session the self-flag refuses even when `--budget` is passed (consent by flag is only as strong as the agent markers, F20).
- The flag command takes the GCP project ID as an argument; an unmanaged project's mutating command refuses
  naming it; `setup`/`update` behaviour and flagging in an agent session are D1553.
- **Tests:** `TestUnmanagedProjectRefusesAndNamesTheFlagCommand`, `TestFlagTakesTheProjectIDAsAnArgument`, `TestUpdateSelfFlagRefusesInAnAgentSession`,
  `TestUpdateOnAnUnmanagedInstallationFlagsItAndNeedsNoSecondCommand` (HARD RULE).
- **Depends on:** T1; D1553. **Reviewer:** opus + mandatory independent review (the one-way door).
- **Commit:** `managed: flag by argument; unmanaged projects refuse with the command (T6)`.

### T7 — init, setup, policy publish, review, retire, billing, pairing, secrets
- **Files:** `internal/cli/init.go`, `init_firebase.go`, `init_fugaroyaml.go`, `init_inputs.go`,
  `init_plugin_stage.go`, `init_project.go`, `init_repo_gate.go`, `init_layer_stage.go`,
  `init_policy_migrate.go`, `init_policy_publish.go` (`confirmEmptyAllowList`), `init_review.go`
  (`askOrdinary`), `init_retire.go`, `billing_prompt.go`, `pair.go`, `secrets.go`, `creds.go`
  (`readBundleValue`), tests.
- Confirmations removed; the billing choice, names, pairing code and secrets go through `readValue`.
  **Tests:** `TestInitAsksNoConfirmation`, `TestBillingChoiceFailsAtOnceWithoutATerminal`.
- **Depends on:** T1. **Reviewer:** standard; `secrets.go` and `creds.go`: opus + review (keep the hidden
  entry, change only its door). **Commit:** `init: no confirmations; values fail fast (T7)`.

### T8 — the estimator, the Billing read, and the lease (D1543, D1548, D1550, F19)
- **Files:** `internal/budget/buildcost.go` (new), `internal/budget/buildcost_test.go` (new),
  `internal/budget/lease_build.go` (new), `internal/cli/image_cost.go` (new), `internal/cli/doctor_build_budget.go` (new; the observe-mode warning, D1548), the Firebase rules under
  `deploy/terraform/gcp/modules/firebase` and `internal/budget/rules` (the build lease path) with their emulator
  tests, `deploy/terraform/gcp/modules/installation/iam.tf` (only if the spike finds a grant is needed), golden plans, **and the rules path the catalog check job's lease uses (T10 consumes it, so it is written here)**.
- **First a spike** decides the Billing mechanism (list prices need the API enabled, not a project role; contract prices need a billing-account role a project module cannot grant; the CLI runs on the person's credentials): the grant in `iam.tf` is **UNVERIFIED** until then. **The price is queried from Cloud Billing at run time** (D1550); **a missing or unknown price refuses**,
  and so does a machine type absent from the build config; local config can supply neither. The lease: a node under its **own key** (`buildleases/<slug>/<id>`, never `runs/`, so no record reader mistakes it for a run), with **its own writer scoping** for the CLI and the catalog check service account (the rules today admit only a run token with `fs`/`fr` claims matching `$slug/$run`), counted against the per-run cap, settled
  on completion, **reclaimed if the CLI dies mid-build**; the rules change carries a **generation bump** and an
  `update` migration (HARD RULE) with a test that an installation at the previous generation converges. The
  over-cap error names the estimate, the cap, what is spent (jobs plus settled builds), the one command, and
  exits non-zero. Observe mode records and proceeds (D1548).
- **Tests:** `TestBuildCostIsAnUpperBound`, `TestUnknownBillingPriceRefuses`, `TestOverCapErrorNamesEstimateCapSpentAndTheCommand`,
  `TestObserveModeRecordsAndProceeds`, the rules' emulator test `TestBuildLeasePath`,
  `TestPreviousRulesGenerationConvergesOnUpdate`, `TestAnAbandonedBuildLeaseIsReclaimed`, `TestBuildLeaseWriterCannotTouchAnotherRunsReservedOrCaps` (emulator), `TestDoctorWarnsObserveWithUnattendedBuilds`.
- **Depends on:** T1; the owner's review of the grant's shape (T5 of the reconciliation). **Reviewer:** opus +
  mandatory independent review (IAM, rules, money). **Commit:** `budget: an image build leases its estimated cost (T8)`.

### T9 — the gate becomes audit-only (D1545, D1549, D1551)
- **Files:** `internal/cli/managed_gate.go`, `managed_apply.go`, `managed_cost.go`, `update_managed.go`,
  `config_publish_managed.go`, `project_policy_managed.go`, `image_refresh_managed.go` (the gate call only; T4
  owns the rest and lands first), `init_retire.go` (the gate call only; T7 lands first), `accounts_rm.go`
  (the gate call only; T5 lands first), `doctor_managed_audit.go`, `internal/managed/converge.go`, `plan.go`,
  `apply.go`, `audit.go`, `internal/infra/tf/cover.go`, `guard.go`, `internal/initflow/stage.go` (`Removes` only),
  the installation module's bucket versioning for the record, tests, golden plans.
- The **content** refusals — cover tables (`allowedTypes`, `grantRules`), delete/replace (outside D1551's hard
  list), blocked cost attribute, removal rule, budget evidence — become record-and-warn (session and `doctor`),
  and the record is made harder to erase (versioned bucket). **`TestIntegrityRefusalsStillRefuse` covers, by
  name:** saved plan changed after the gate; creation fact not managed; environment injection; the managed
  configuration rule in `managed_cover.go` (no provisioner, only Google providers, no data source, no upload
  from a local path, no provider endpoint/project/billing redirect); the pre-init refusal of
  `backend`/`cloud`/`provider_meta`/`ephemeral`/`action` blocks; an address named on the command line; the
  symlink and nesting bounds; the deployer impersonation checks; **the marker checks of `internal/managed/budget.go`** (unreadable marker, unknown version, another project's marker: fail closed). **D1551:** every entry of `allowedTypes` is classified data-bearing or not (the table in the reconciliation); data-bearing and **unclassified** types refuse delete/replace, including a type added later. `managedAsk` is deleted.
- **Tests:** `TestOutOfTableGrantAppliesAndIsRecorded`, `TestIntegrityRefusalsStillRefuse`,
  `TestEveryAllowedTypeIsClassifiedDataBearingOrNot`, `TestAnUnclassifiedTypeRefusesDelete`, `TestMarkerChecksFailClosed`, `TestDoctorFlagsAnOutOfTableGrant`, `TestAuditRecordBucketIsVersioned`.
- **Depends on:** T2, T4, T5, T7 (the shared files above), #1891 and #1804 merged; the owner's word on T2 of the
  tensions. **Reviewer:** opus + mandatory independent review at level 3; its PR body carries the tension.
- **Commit:** `managed: the gate records and warns on content; the integrity refusals stay (T9)`.

### T10 — remove the build-count bound (D1543, D1554)
- **Files:** `internal/managed/builds.go`, `internal/infra/managed/marker.go`, `read.go`,
  `internal/infra/catalogcheck.go`, `internal/cli/image_check_catalog.go`,
  `deploy/terraform/gcp/modules/installation/catalogcheck.tf` (if the job's account needs the lease path T8 wrote), the
  schemas the marker/spec shape appears in, tests. **Not** `image_refresh_managed.go` (T4 removed that call).
- Marker version bump; `fugaro update` converges an installation at the previous version; the catalog check job
  takes a lease through T8's estimator with its own per-run cap (D1554). **Stated plainly: on a default installation the mode is
  observe, a lease does not bite in observe, so the nightly job and every build are unbounded there; this is the owner's
  tension T6, not fixed here.** **Tests:** `TestPreviousMarkerVersionConvergesOnUpdate`, `TestCatalogCheckJobLeasesEachBuild`.
- **Depends on:** T4, T8, **T9** (golden plans and installation-module files are shared). **Release:** `### For operators`:
  "run `fugaro update`". **Reviewer:** opus + review. **Commit:** `managed: the image build count is gone; update migrates the marker (T10)`.

### T15 — the non-money `Typed` steps get the owner-session-only fence (D1555)
- **Files:** `internal/cli/update_managed.go` (`workspaceStage.gateStep`, the `p.widens` step), `update_workspace.go`
  (`confirmRemoval`, `confirmTypedStrict`), `init_credentials.go`, `init_compute.go`, their tests.
- Each of these steps refuses in an agent session with the owner's command printed and asks nothing at the owner's
  terminal. **Tests:** `TestWorkspaceMemberPublishRefusesInAnAgentSession`, `TestCredentialsOptInRefusesInAnAgentSession`,
  `TestComputeStageRefusesInAnAgentSession`, `TestWorkspaceRemovalAsksNothingAtATerminal`.
- **Depends on:** T1, T3 (`update_managed.go` is T3's at level 1, T9's at level 3). **Reviewer:** opus + mandatory
  independent review (it is the fence that replaces `Typed` for membership and credential minting).
- **Commit:** `cli: membership, credential minting and compute are owner-session-only (T15)`.

## Slice 2 — the flags, the class system, the words

### T11 — remove `--yes` and `--non-interactive`; skills, docs and the console
- **Files:** every command file that registers them (the list in F4), `plugin/skills/{setup,upgrade,working,
  parallelism}` and `reference/`, `plugin/*_test.go` (including `setup_skill_test.go`), `console/src/main/spawn.ts`,
  `console/src/main/ipc.ts`, `console/src/main/launch.ts`, `console/src/renderer/confirm.ts` and its tests,
  `docs/gcp-live-checklist.md` (31 mentions), `docs/design/managed-projects.md`, `managed-projects-spec.md`,
  `docs/design/image-refresh.md`, `docs/managed-agent-converge/*`, `docs/console-managed-screens/*`,
  `SECURITY.md` (flag text only), other docs the grep finds.
- **Depends on:** T3, T4, T5, T6, T7, T9, T10, T15. **Tests:** plugin tests, console `npm run typecheck` and
  `npm run test:unit -- --maxWorkers=2`, `go test ./internal/cli -run Docs`. **Reviewer:** opus + mandatory
  independent review (plugin skills, console spawn allow-list). **Commit:**
  `cli: --yes and --non-interactive are gone; --ask stays; skills, docs and console follow (T11)`.

### T12 — delete the confirm class system
- **Files:** `internal/initflow/stage.go`, `internal/initflow/*_test.go`, `internal/cli/initsecrets.go`
  (`agentMarker` stays), the last callers.
- Remove `Typed`/`YesOrTyped`, `CanConfirmManaged`, `GateConfirms`, `ConfirmsByGate`, `Managed.Covers`, D1026's
  pin; keep `NeedsTerminal`/`Conditions` as far as value entry and `--ask` need them.
- **Depends on:** T11 and **T15** (nothing deletes the `Typed` class while a step still relies on it). **Reviewer:** opus + mandatory independent review. **Commit:**
  `initflow: the confirmation classes are gone (T12)`.

### T13 — the policy in CLAUDE.md, the rules and SECURITY.md
- **Files:** `internal/cli/docs_no_prompt_policy_test.go` (added with this PR: every D answered, in range D1540–D1559, unique), `CLAUDE.md`, `.claude/rules/terraform.md`, `.claude/rules/credentials-service.md`, `SECURITY.md`
  (the gate text; T11 owns its flag text and lands first), `website/content/security.md` (`npm run check`), the
  status line of this folder.
- States the policy and points here as the authority; rewrites the cover-table rule as a tripwire **only after the
  owner confirms T2 of the tensions**. Agents must not add confirmation steps.
- **Depends on:** T9, T11, the owner's ruling. **Reviewer:** opus + mandatory independent review.
  **Commit:** `docs: no prompts, and what the cover tables now are (T13)`.

### T14 — owner-run checks
- **Files:** `scripts/live-checks/check-380-no-prompt-update.sh`, `check-381-over-cap-build.sh`,
  `check-382-agent-cannot-raise-a-cap.sh` (all new) plus a fake-gcloud test each; the `docs/gcp-live-checklist.md`
  rows **are added after T11 lands** (T11 owns the file's flag text).
- **OWNER-RUN:** the owner types `scripts/live-checks/check-380-no-prompt-update.sh` (then 381, 382); each prints
  PASS/FAIL, takes `--dry-run`, refuses `belong`. No agent runs them. **Depends on:** T3, T4, T9, T11.
- **Commit:** `scripts: Checks 380-382 as runnable owner scripts (T14)`.

## Parallel execution

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | T1, T2 |
| 1 | T3, T6, T7, T8 (needs the Billing spike) |
| 2 | T4 (after T8), T5 (after T3), T15 (after T3) |
| 3 | T9 (after T2, T4, T5, T7, #1891, #1804) |
| 4 | T10 (after T4, T8, T9) |
| 5 | T11 |
| 6 | T12 (after T11, T15), T13, T14 |

**Critical path:** T8 → T4 → T9 → T10 → T11 → T12. T9 is also gated by the owner's word on T2 of the tensions.

### Shared-files table (no level holds two writers of one file)

| File | Writers (level) |
| --- | --- |
| `budget.go`, `update_managed.go` | T3 (1), T9 (3: the gate call), T11 (5: flags) |
| `settings_write.go` | T3 (1), T5 (2) |
| `accounts_rm.go`, `accounts_managed.go`, `config_publish.go` | T5 (2), T9 (3), T11 (5) |
| `image*.go`, `image_refresh_managed.go`, `image_build_managed.go` | T4 (2), T9 (3: the gate call in `image_refresh_managed.go`), T11 (5) |
| `image_check_catalog.go`, `internal/infra/catalogcheck.go`, installation-module files, golden plans | T8 (1), T9 (3), T10 (4) |
| `update_workspace.go`, `init_credentials.go`, `init_compute.go`, `update_managed.go` | T3 (1: ceiling), T15 (2), T9 (3: gate call), T11 (5: flags) |
| `managed_ask.go`, `askconfirm`-surface | T1 (0) |
| `update_selfflag.go` (new), `init_stages.go` | T6 (1) |
| `init*.go`, `init_retire.go`, `pair.go`, `secrets.go`, `creds.go`, `billing_prompt.go` | T7 (1), T9 (3: `init_retire.go` gate call), T11 (5) |
| `managed_flag.go`, `managed_unflag.go`, `project_cmd.go`, `project_enroll.go`, `restore.go`, `root.go` | T6 (1), T11 (5) |
| `managed_gate.go`, `managed_apply.go`, `doctor_managed_audit.go`, `internal/managed/{converge,plan,apply,audit}.go`, `cover.go`, `guard.go` | T9 (3) |
| `internal/managed/builds.go`, `internal/infra/managed/*`, `internal/infra/catalogcheck.go` | T10 (3) |
| `internal/budget/*`, Firebase rules, `iam.tf` (billing grant) | T8 (1) |
| `internal/initflow/stage.go` | T9 (3: `Removes` only), T12 (6) |
| `docs/gcp-live-checklist.md`, `SECURITY.md` flag text, plugin skills, console `spawn.ts`/`confirm.ts` | T11 (5); T14 rows and T13 gate text land at level 5 |
| `CLAUDE.md`, `.claude/rules/*` | T13 (6) |

Where two tasks write one file they are at different levels, and the later writer's description says it changes
only its own part. Sequential only where a dependency forces it: the estimator before the build command, the
build command before the marker change, every caller before the flags, every flag before the class system.

## Checks

| Check | Measures | Run by |
| --- | --- | --- |
| 380 | `fugaro update` on the **sandbox project** (`edgeappinc/fugarosandbox`, never `belong`) with stdin closed finishes without a prompt | owner, T14 script |
| 381 | an image build over the cap fails with estimate, cap, spent and the command, non-zero | owner, T14 script |
| 382 | a coding-agent session's `budget set` raise refuses and prints the command | owner, T14 script |
| 383–389 | reserved | — |

## Considered and declined

- Literal `-auto-approve` (F6): the saved-plan apply is the equivalent and cannot apply a different plan.
- Keeping the typed GCP-ID prompt for flagging a project managed: replaced by consent by argument (D1541).
- Waiting for the meta repo (D1546).
- A repo-held rate table (D1550's recommendation): the owner chose a Billing query.
