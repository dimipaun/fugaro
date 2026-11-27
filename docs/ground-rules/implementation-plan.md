# Ground rules: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped the plan are D3420–D3449, all given on 2026-11-26.
- **Not the recommendation:** D3441 (the stamp lives in `task.json`), D3445 (one eager rebuild) and
  D3448 (accept the launch window).
- **Amended after review:** D3425 and D3430, both confirmed by the owner the same day; D3424 and D3448,
  which wait on the owner's confirmation (open items O1 and O2).
- **Resolutions**, stated without a question: D3424, D3426, D3428, D3429, D3434, D3435, D3439, D3446 and
  D3449.

## Conventions every part follows

- `CLAUDE.md` throughout, in particular:
  - the HARD RULE on `fugaro update`, safe with runs in flight (`TestUpdateLeavesARunInFlightRunning`
    must keep passing in every task that touches `update`);
  - Fugaro asks for nothing; no task adds a confirmation;
  - "a field never changes meaning": `task.Spec` gains a field and never retypes one, and VS4 changes a
    default, not what `paused` means;
  - hermetic tests (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`); no network (the
    self-upgrade tests use `httptest`); no secret-shaped literal;
  - search before writing a helper (DG0 extends `tf.DataBearingType`, DG3 extends `TestPreventDestroy`);
  - comments explain why;
  - targeted tests (`-run`), and `go test ./internal/cli/ -run Docs` for any document change.
- Area rules load by path: `.claude/rules/terraform.md` (DG3), `.claude/rules/credentials-service.md`
  (DOC1, DOC2: `SECURITY.md` and `website/content/security.md`), `.claude/rules/website.md` (DOC2:
  `npm run check` in `website/`).
- **Command help text.** A task that changes a flag or a command's help regenerates
  `website/content/cli-reference.md` with `go run ./ee/clidoc` and runs `go test ./ee/clidoc/`. The
  plugin tests (`go test ./plugin/...`) check every flag a skill names, so a flag leaves the CLI only after
  the skills stop naming it (UO4a waits on DOC1).
- Human output follows the owner's brew style (`internal/report`; see F20): one line per step, colour
  only on a TTY, `NO_COLOR` honoured, `--json` unchanged except where D3437 adds to it.
- Boundaries:
  - **Always:**
    - classify a delete through DG0's table, never by a list a task keeps for itself;
    - refuse an irreplaceable delete in at most two sentences;
    - keep secret containers when a root retires;
    - keep the keep list of D3425 (as amended) refused to agents.
  - **Ask first:** a new Go dependency; a change to a wire format beyond VS0's one field; any new prompt
    or flag.
  - **Never:**
    - delete a project, a bucket, the RTDB, Firestore, a KMS key, Terraform state, a current member's
      secret or a person's IAM grant;
    - print raw Terraform or build output on the terminal;
    - build a download URL from task data;
    - edit `.github/workflows/` (stage under `docs/pending-workflows/` instead).
- Live checks are the owner's (Checks 950–953).
- The project's rigor is `startup`, and decoupling is on (`planning.decoupling`, true).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| An unknown resource type is irreplaceable (fail closed), and a person's IAM grant is irreplaceable | DG0 |
| `update` never plans a destroy of a project, bucket, RTDB, Firestore, KMS key or a member's secret | DG3 |
| A retired root keeps its secret containers and still waits on in-flight executions (F38) | UO2, DG4 |
| An agent's `update` in a managed project runs every stage, refuses only irreplaceable deletes, and still refuses the keep-list flags | AG2, DG4 |
| An agent can never pass `--allow-delete`, at any point in the rollout | DG2 before AG2 |
| The runner upgrades only to exactly the stamped release, from a fixed repository, checked by sha256; never a dev build, never down, never twice | VS1, VS5 |
| A failed upgrade ends the run in one line naming the version | VS1, VS5 |
| A foreign or broken baked tree is never used for a run | CF1 |
| A file newer than the reader is refused in one line; an old supported version reads as before | CV1 |
| No human message over 140 characters outside the allowlist | CO5 |
| An installation that last ran `update` on the release before converges | UO2, VS3b, VS4 (each with a previous-version converge test) |

## Slices

1. **The first pieces** (DG0, VS0, UO1, UO3, CV1): inert. The class table, the stamp field the runner
   accepts, the earlier documents brought in line, and the versioned loader.
2. **Output** (CO1–CO5): useful alone.
3. **Deletes by class** (DG1a, DG1b, DG2, DG3, DG4).
4. **One update for the project** (UO2, then DOC1, DOC2, UO4a, UO4b).
5. **Agents run update** (AG2–AG5c).
6. **Checkout** (CF1, CF2a, CF2c, CF2b).
7. **Versions** (CV2, VS1–VS5, REL1, then CV3 after release A).

AG1 is the audit itself, §A of the reconciliation; the agent tasks start at AG2. UO5 of the first draft
was dropped with D3424's amendment.

## Tasks

### DG0: the delete classes, on the existing table
- Acceptance:
  - `tf.DataBearingType` and its tables (`cover.go`) follow D3430 as amended: registries and
    Fugaro-made service accounts are no longer data-bearing;
  - an IAM member that names a person on the operator or admin list is irreplaceable;
  - an unknown type stays refused (fail closed).
- Files: `internal/infra/tf/cover.go`, `internal/infra/tf/cover_classes_test.go` (new),
  `docs/ground-rules/delete-classes.md` (new).
- Interfaces: the same functions, with the new table. Consumed by DG1a, DG2, DG3, UO2, AG2 and DG4. A task
  that finds a type Fugaro makes missing from the table adds it in its own change.
- Tests: `TestDeleteClassesFollowD3430`, `TestPersonGrantIsIrreplaceable`, `TestUnknownTypeRefused`.
- Command: `go test ./internal/infra/tf/ -run 'TestDeleteClassesFollowD3430|TestPersonGrantIsIrreplaceable|TestUnknownTypeRefused' -count=1`
- Red step: `TestDeleteClassesFollowD3430` fails while `google_artifact_registry_repository` is data-bearing.
- Depends on: none.
- Commit: `ground-rules DG0: the delete classes follow D3430`
- Reviewer tier: opus (a write guard's input).

### VS0: the stamp field
- Acceptance: `task.Parse` accepts `cli_version` and round-trips it, and the schema names it. A runner with
  VS0 reads a stamped task (D3441, D3445).
- Files: `internal/task/task.go`, `internal/task/task_test.go`, `schemas/task.schema.json`.
- Interfaces: `task.Spec.CLIVersion string` (`json:"cli_version,omitempty"`), a strict semver or empty.
  Consumed by VS2 and VS5.
- Tests: `TestParseCLIVersion`, `TestCLIVersionMustBeSemver`, `TestSchemaHasCLIVersion`.
- Command: `go test ./internal/task/ -run 'CLIVersion' -count=1`
- Red step: `TestParseCLIVersion` fails on the unknown field while `Parse` is strict.
- Depends on: none.
- Commit: `ground-rules VS0: task.json carries cli_version (D3441)`
- Reviewer tier: opus (a wire format). It is reviewed independently (`fugaro review --pr N`) before VS2
  writes the field.

### UO1: the earlier plans brought in line (docs)
- Acceptance:
  - `docs/update-from-anywhere/` records U11 as cancelled and U12 as no longer waiting on U11, with
    D3353 amended by D3420;
  - the stale status lines F14 names are corrected.
- Files: `docs/update-from-anywhere/README.md`, `docs/update-from-anywhere/implementation-plan.md`,
  `docs/update-from-anywhere/reconciliation.md`, `docs/required-version-stamp/README.md`,
  `docs/managed-flag-ergonomics/README.md`.
- Tests: the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents only); the Docs tests must stay green.
- Depends on: none.
- Commit: `ground-rules UO1: update-from-anywhere drops U11 (D3420); stale statuses`
- Reviewer tier: standard.

### UO2: every repository on shared jobs; update retires the rest
- Acceptance:
  - every enrolled repository is admitted to a shared-job profile (`infra.SharedRepo`);
  - plain `update` retires each remaining per-repository root without `--retire`: jobs and schedulers are
    deleted, and secret containers leave state and are not destroyed;
  - retirement still waits on `infra.InFlightJobs` (F38);
  - an installation that last ran `update` on the release before converges.
- Files: `internal/infra/spec.go`, `internal/cli/init.go` (`repoEngine`, `enrolSharedRepo`,
  `retireSharedRoot`), `internal/cli/init_retire.go`, `internal/cli/init_repo_stage.go`,
  `internal/cli/update_retire_root_test.go` (new).
- Interfaces: consumes DG0. Produces a repository stage that covers every enrolled repository with no
  checkout.
- Tests: `TestUpdateRetiresEveryRepoRoot`, `TestRetiredRootKeepsSecretContainers`,
  `TestRetireWaitsOnInFlight`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateRetiresEveryRepoRoot|TestRetiredRootKeepsSecretContainers|TestRetireWaitsOnInFlight|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateRetiresEveryRepoRoot` fails while retirement still needs `--retire`.
- Depends on: DG0 (the class table).
- Commit: `ground-rules UO2: every repository runs on shared jobs; update retires the rest (D3421)`
- Reviewer tier: opus (Terraform, IAM, deletes).

### UO3: `workflows.*.resources` read and ignored
- Acceptance: a `fugaro.yaml` with `workflows.*.resources` loads, the key is dropped in memory, and one
  note names the profile that decides the sizing (D3421).
- Files: `internal/config/retired.go`, `internal/config/retired_test.go`.
- Tests: `TestRetiredWorkflowResources`.
- Command: `go test ./internal/config/ -run TestRetiredWorkflowResources -count=1`
- Red step: the test fails while the key still feeds the job.
- Depends on: none.
- Commit: `ground-rules UO3: workflow resources come from the profile (D3421)`
- Reviewer tier: standard.

### DOC1: the standing rules and the skills
- Acceptance:
  - CLAUDE.md's HARD RULE says `update` is the one project-wide operation, agents may run it, and it
    waits on a rebuild only when the base image changes;
  - CLAUDE.md points to `docs/ground-rules/` in one line;
  - the credentials-service rules, the `update`, `setup` and `images` skills, and the setup skill's
    references name no `update --repo` (D3447);
  - the setup skill's test pins the new text.
- Files: `CLAUDE.md`, `.claude/rules/credentials-service.md`, `plugin/skills/update/SKILL.md`,
  `plugin/skills/setup/SKILL.md`, `plugin/skills/images/SKILL.md`, `plugin/skills/setup/reference/*.md`,
  `plugin/setup_skill_test.go`.
- Tests: the plugin tests and the Docs tests.
- Command: `go test ./plugin/... -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents); `git grep -n 'update --repo' CLAUDE.md .claude plugin` must print nothing.
- Depends on: UO2, AG2 (behaviour: the text describes what has shipped).
- Commit: `ground-rules DOC1: CLAUDE.md, rules and skills follow the ground rules (D3447)`
- Reviewer tier: opus (CLAUDE.md, rules files, plugin skills). The file count is over five; the edits are
  one sentence each.

### DOC2: the how-tos, the security pages and their pins
- Acceptance:
  - nothing outside `internal/` names `update --repo`, including `docs/how-to/`, `README.md`, `SECURITY.md`
    and `website/content/security.md`;
  - `docs/managed-agent-converge/handoffs.md` records D3425 (F13);
  - the Docs tests that pin the old sentences change with them.
- Files: `docs/how-to/*.md`, `README.md`, `SECURITY.md`, `website/content/security.md`,
  `docs/managed-agent-converge/handoffs.md`, `internal/cli/docs_*_test.go`.
- Tests: the Docs tests and the website check.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: none (documents); `git grep -n 'update --repo' -- docs README.md SECURITY.md website` prints
  only this folder and the historical specs.
- Depends on: UO2, AG2 (behaviour).
- Commit: `ground-rules DOC2: how-tos and security pages for a project-wide update`
- Reviewer tier: opus (`SECURITY.md` falls under the credentials-service rule).

### UO4a: `update --repo` and its flags go
- Acceptance: `--repo`, `--no-build`, `--retire` and `--allow-job-delete` are gone from `setup` and
  `update`. `retireNextCommand` is deleted, and the CLI reference is regenerated.
- Files: `internal/cli/init.go`, `internal/cli/init_refusal.go`, `internal/cli/init_repo_target.go`,
  `internal/cli/init_retire.go`, `website/content/cli-reference.md`.
- Tests: `TestUpdateHasNoRepoFlag`, the plugin tests, `go test ./ee/clidoc/`.
- Command: `go test ./internal/cli/ -run 'TestUpdateHasNoRepoFlag|Docs' -count=1 && go test ./ee/clidoc/ ./plugin/... -count=1`
- Red step: `TestUpdateHasNoRepoFlag` fails while the flag is registered.
- Depends on: UO2 (behaviour), DOC1 and DOC2 (nothing may name the flag when it goes), CF2c (one file:
  `init.go`).
- Commit: `ground-rules UO4a: update --repo and its flags go (D3420, D3431)`
- Reviewer tier: standard.

### UO4b: no message names `update --repo`
- Acceptance: no non-test Go file names `update --repo` (64 today, F37). Each message says `fugaro update`
  or drops the instruction, within 140 characters.
- Files: `internal/**/*.go` (a sweep; alone in its level).
- Tests: `TestNoMessageNamesRepoFlag` (a `git grep`-style walk of the source), CO5's census.
- Command: `go test ./internal/cli/ -run 'TestNoMessageNamesRepoFlag|TestMessagesAreLaconic' -count=1`
- Red step: `TestNoMessageNamesRepoFlag` lists today's 64 files.
- Depends on: UO4a (the sweep follows the flag's removal).
- Commit: `ground-rules UO4b: no message names update --repo`
- Reviewer tier: standard.

### CV1: the versioned loader
- Acceptance:
  - `fugaro.yaml` decodes its `version` first and runs the registered upgraders up to the current version
    in memory (v1 is the identity today);
  - a version above the reader's is refused in one line naming `brew upgrade`;
  - `config.SupportedVersions` records each version's deprecation release (D3422, D3423).
- Files: `internal/config/versions.go` (new), `internal/config/versions_test.go` (new),
  `internal/config/config.go` (`decodeRepo`), `internal/config/validate.go`, `schemas/fugaro.schema.json`.
- Tests: `TestLoadNewerVersionRefused`, `TestUpgradersChain`, `TestV1ReadsUnchanged`.
- Command: `go test ./internal/config/ -run 'TestLoadNewerVersionRefused|TestUpgradersChain|TestV1ReadsUnchanged' -count=1`
- Red step: `TestLoadNewerVersionRefused` fails on today's "must be 1" message.
- Depends on: none.
- Commit: `ground-rules CV1: fugaro.yaml loads by version and upgrades in memory (D3422)`
- Reviewer tier: standard.

### CV2: the deprecation notice
- Acceptance: for a version in its deprecation window, `doctor` and `update` print one line naming the
  file and the version (D3423).
- Files: `internal/cli/doctor_config_version.go` (new), `internal/cli/doctor_config_version_test.go`
  (new), `internal/cli/update_summary.go`.
- Tests: `TestDoctorNamesDeprecatedVersion`.
- Command: `go test ./internal/cli/ -run TestDoctorNamesDeprecatedVersion -count=1`
- Red step: the test fails with no notice.
- Depends on: CV1 (it reads `SupportedVersions`).
- Commit: `ground-rules CV2: doctor and update name a deprecated fugaro.yaml version`
- Reviewer tier: standard.

### CV3: the image-age gates retire
- Acceptance: the per-key `*Since` gates in `recipes_skew.go` and `gcpProjectFieldSince` are gone, with
  their call sites in `run.go` and the `--anchor` help in `init.go` (D3446). A launch no longer reads a
  build record's release to admit a key.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/run.go`, `internal/cli/init.go`.
- Tests: `TestLaunchNeedsNoImageAgeGate`.
- Command: `go test ./internal/cli/ -run 'TestLaunchNeedsNoImageAgeGate|Skew' -count=1`
- Red step: the test fails while `rigorSince` and the others still refuse.
- Depends on: VS5 and VS2 (behaviour: runners upgrade themselves); CF2c (behaviour: no `clone` value is
  left to gate); UO4a and UO4b (one file: `init.go`, and the sweep). **External:** release A (with VS5)
  has shipped and its eager rebuild has run.
- Commit: `ground-rules CV3: the image-age gates retire (D3446)`
- Reviewer tier: standard.

### DG1a: the gate judges what is deleted
- Acceptance:
  - gate rule 1 admits a removal whose type DG0 calls recreatable, with no delete-list entry;
  - it refuses an irreplaceable one in at most two sentences, naming no flag (D3427).
- Files: `internal/infra/tf/managed_cover.go`, `internal/infra/tf/managed_cover_test.go`,
  `internal/cli/managed_gate.go`.
- Tests: `TestGateAdmitsRegistryRemoval`, `TestGateRefusesSecretRemoval`, `TestGateRefusesPersonGrantRemoval`.
- Command: `go test ./internal/infra/tf/ ./internal/cli/ -run 'TestGate(AdmitsRegistryRemoval|RefusesSecretRemoval|RefusesPersonGrantRemoval)' -count=1`
- Red step: `TestGateAdmitsRegistryRemoval` fails while a registry is data-bearing and unlisted.
- Depends on: DG0 (the class table).
- Commit: `ground-rules DG1a: the managed gate judges what is deleted (D3427)`
- Reviewer tier: opus (the write guard).

### DG1b: the delete list retires
- Acceptance: `fugaro allow-delete`, `fugaro/allow-delete.json` and `internal/infra/managed/deletelist.go`
  are gone, and so is every reader: `managed apply`, `restore` and the inventory (D3427).
- Files: `internal/cli/allow_delete.go` (deleted), `internal/infra/managed/deletelist.go` (deleted),
  `internal/cli/managed_apply.go`, `internal/cli/restore.go`, `internal/managed/inventory.go`,
  `internal/cli/root.go`.
- Tests: `TestNoDeleteListReader`, and `managed apply`'s and `restore`'s existing tests.
- Command: `go build ./... && go test ./internal/cli/ ./internal/managed/ -run 'TestNoDeleteListReader|ManagedApply|Restore' -count=1`
- Red step: `TestNoDeleteListReader` finds `allow-delete` registered.
- Depends on: DG1a (behaviour: nothing needs the list once the rule changed).
- Commit: `ground-rules DG1b: the owner's delete list retires (D3427)`
- Reviewer tier: opus (the write guard).

### DG2: the guard judges what is deleted; `--allow-delete` goes
- Acceptance: `tf.Guard` refuses only irreplaceable deletes or replaces, and `--allow-delete` is gone.
  The refusal says in at most two sentences which resource and that `update` never deletes it (D3431).
- Files: `internal/infra/tf/guard.go`, `internal/infra/tf/guard_test.go`, `internal/cli/init.go`,
  `internal/cli/init_refusal.go`, `website/content/cli-reference.md`.
- Tests: `TestGuardPassesRecreatable`, `TestGuardRefusesIrreplaceable`.
- Command: `go test ./internal/infra/tf/ -run TestGuard -count=1 && go test ./ee/clidoc/ -count=1`
- Red step: `TestGuardPassesRecreatable` fails on today's refusal.
- Depends on: DG0 (the class table), UO2 (one file: `init.go`).
- Commit: `ground-rules DG2: the guard judges what is deleted; --allow-delete goes (D3431)`
- Reviewer tier: opus (the write guard).

### DG3: what update never destroys
- Acceptance: `TestPreventDestroy` (`deploy/terraform/terraform_test.go`) covers every irreplaceable type
  in DG0's table, and a converge test shows `update` deletes no current member's secret (D3432, D3434).
- Files: `deploy/terraform/terraform_test.go`, `internal/cli/update_member_secrets_test.go` (new).
- Tests: `TestPreventDestroy`, `TestUpdateKeepsMemberSecrets`.
- Command: `go test ./deploy/terraform/ -run TestPreventDestroy -count=1 && go test ./internal/cli/ -run TestUpdateKeepsMemberSecrets -count=1`
- Red step: `TestPreventDestroy`, extended over DG0's table, fails on the first irreplaceable type it does
  not yet check.
- Depends on: DG0 (the class table).
- Commit: `ground-rules DG3: pin what update never destroys`
- Reviewer tier: opus.

### DG4: deletes by class, end to end
- Acceptance: on the fake cloud, an agent's `update` in a managed project does all of this in one run,
  with runs in flight:
  - it retires an orphaned repository root (jobs deleted, container kept);
  - it applies a plan that replaces a job;
  - it refuses a plan that destroys a secret, in at most two sentences;
  - it refuses `--allow-delete` and `--base-image`.
- Files: `internal/cli/update_ground_rules_test.go` (new).
- Tests: `TestGroundRulesUpdateEndToEnd`.
- Command: `go test ./internal/cli/ -run TestGroundRulesUpdateEndToEnd -count=1`
- Red step: the test fails before DG1b, DG2, UO2 and AG2 are in.
- Depends on: DG1b, DG2, UO2, AG2 (behaviour: the end-to-end test).
- Commit: `ground-rules DG4: update by class, end to end`
- Reviewer tier: opus.

### CO1: plain step names
- Acceptance: every converge stage prints a plain-words name ("Applying infrastructure changes"), and the
  stage ID stays in the log and in `--json`/`--events` (D3435).
- Files: `internal/initflow/stage.go`, `internal/initflow/loop.go`,
  `internal/cli/testdata/update_*.golden`, `internal/cli/testdata/setup_*.golden`.
- Tests: `TestStageDisplayNames`, the update and setup goldens.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestStageDisplayNames|Golden' -count=1`
- Red step: `TestStageDisplayNames` fails while `openStep` prints the ID.
- Depends on: none.
- Commit: `ground-rules CO1: steps print plain names (D3435)`
- Reviewer tier: standard.

### CO2: liveness
- Acceptance:
  - the TTY spinner shows the elapsed time;
  - without a TTY, a step still running after a minute prints one "still …" line a minute;
  - `--events` carries a `heartbeat` event (D3436).
- Files: `internal/report/spinner.go`, `internal/report/report.go`, `internal/cli/setup_events.go`,
  `schemas/setup-events.schema.json`, `internal/cli/testdata/setup_events.golden.ndjson`.
- Tests: `TestSpinnerShowsElapsed`, `TestHeartbeatWithoutTTY`, `TestEventsHeartbeat`.
- Command: `go test ./internal/report/ ./internal/cli/ -run 'Elapsed|Heartbeat' -count=1`
- Red step: `TestHeartbeatWithoutTTY` fails, because nothing prints for a minute (a fake clock).
- Depends on: none.
- Commit: `ground-rules CO2: long steps show they are alive (D3436)`
- Reviewer tier: standard.

### CO3: no raw output on screen
- Acceptance: no path prints the Terraform or docker stream on the terminal (D3438):
  - `--plan-only` prints the plain plan and the log path;
  - `--verbose` adds full step reasons;
  - `--forget`, `--config-only` and `--print-vars` go through the reporter;
  - `image build --local` and the mirror lines go to the log.
- Files: `internal/cli/init.go` (`progress`, `planBlock`), `internal/cli/image.go`,
  `internal/cli/init_images.go`, `internal/cli/raw_output_test.go` (new).
- Tests: `TestNoRawTerraformOnScreen`, `TestLocalBuildToLog`.
- Command: `go test ./internal/cli/ -run 'TestNoRawTerraformOnScreen|TestLocalBuildToLog' -count=1`
- Red step: `TestNoRawTerraformOnScreen` under `--verbose` sees a `module.` address on stderr.
- Depends on: none.
- Commit: `ground-rules CO3: raw Terraform and build output stay in the log (D3438)`
- Reviewer tier: standard.

### CO4: structured detail for agents
- Acceptance: in an agent session, a command that has `--json` writes its result document to stdout and
  the human lines to stderr, as if `--json` were passed. Outside one, nothing changes (D3437).
- Files: `internal/cli/report_flags.go`, `internal/cli/root.go`, `internal/cli/agent_json_test.go` (new).
- Tests: `TestAgentSessionGetsJSON`, `TestTerminalUnchanged`.
- Command: `go test ./internal/cli/ -run 'TestAgentSessionGetsJSON|TestTerminalUnchanged' -count=1`
- Red step: `TestAgentSessionGetsJSON` finds no document on stdout.
- Depends on: none.
- Commit: `ground-rules CO4: an agent session gets the --json document (D3437)`
- Reviewer tier: standard.

### CO5: the 140-character census
- Acceptance: one test walks the human messages of `internal/cli` (refusals, warnings, notices and step
  reasons) and fails on any over 140 characters that is not in the allowlist. The allowlist may only
  shrink (D3439).
- Files: `internal/cli/laconic_census_test.go` (new), `internal/cli/testdata/laconic_allowlist.txt` (new),
  `internal/cli/update_laconic_test.go` (folded in and deleted).
- Tests: `TestMessagesAreLaconic`, `TestLaconicAllowlistOnlyShrinks`.
- Command: `go test ./internal/cli/ -run 'TestMessagesAreLaconic|TestLaconicAllowlistOnlyShrinks' -count=1`
- Red step: with an empty allowlist the census fails, listing today's long messages, which become the
  allowlist.
- Depends on: none.
- Commit: `ground-rules CO5: every human message is held to 140 characters (D3439)`
- Reviewer tier: standard.

### AG2: an agent's update runs every stage
- Acceptance:
  - in a managed project, an agent's `update` runs every stage through the gate, including Compute and
    retirement;
  - the secrets stage skips with one line naming the secret and the command that sets it;
  - `--onboard-repo`, `--publish-config` and `--coordinator` are admitted;
  - `shapesTheInstallation` still refuses the keep-list flags (budget, billing, operator and admin lists,
    `--managed`, `--create-project`, `--state-bucket`, `--allow-delete` and the image-source flags) and
    admits the rest (D3425 as amended).
- Files: `internal/initflow/loop.go` (`agentStage`), `internal/cli/update_managed.go`,
  `internal/cli/init.go`, `internal/cli/init_stages.go`, `internal/cli/initsecrets.go`,
  `internal/cli/init_compute.go`.
- Interfaces: consumes DG0 through the gate's unchanged `admit`.
- Tests: `TestAgentUpdateRunsEveryStage`, `TestAgentSecretsStageSkipsWithLine`,
  `TestAgentRefusedKeepListFlags`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestAgentUpdateRunsEveryStage|TestAgentSecretsStageSkipsWithLine|TestAgentRefusedKeepListFlags|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestAgentUpdateRunsEveryStage` fails on Compute's `AgentRefusal`.
- Depends on: DG0 (the class table); DG1a (behaviour: the gate admits recreatable deletes first); DG2
  (one file: `init.go`; it also removes `--allow-delete` before agents run every stage).
- Commit: `ground-rules AG2: an agent runs the whole update (D3425)`
- Reviewer tier: opus (the agent's reach and the gate). The file count is six.

### AG3: settings, rigor and accounts picks lift
- Acceptance:
  - an agent may run `settings set`/`unset` for every key except those that raise a budget or widen
    `allow_data_to`, which are still refused;
  - an agent may lower rigor (`rigor set`, `rigor floor`, `rigor security rm`) and run
    `accounts switch`, `alias` and `pool`;
  - `--yes` is gone from `accounts alias` and `accounts switch` (D3425 as amended, D3429).
- Files: `internal/cli/settings_managed.go`, `internal/cli/rigor.go`, `internal/cli/rigor_floor.go`,
  `internal/cli/accounts_switch.go`, `internal/cli/accounts_alias.go`, `internal/cli/accounts_pool.go`,
  `website/content/cli-reference.md`.
- Tests: `TestAgentMaySet`, `TestAgentBudgetRaiseRefused`, `TestAccountsHaveNoYes`.
- Command: `go test ./internal/cli/ -run 'TestAgentMaySet|TestAgentBudgetRaiseRefused|TestAccountsHaveNoYes' -count=1 && go test ./ee/clidoc/ ./plugin/... -count=1`
- Red step: `TestAgentMaySet` fails on the rigor lowering's refusal.
- Depends on: none.
- Commit: `ground-rules AG3: settings, rigor and account picks are an agent's too (D3425)`
- Reviewer tier: opus (agent reach). The file count is seven; most are a one-line refusal each.

### AG4: project, image and publish commands lift
- Acceptance: an agent may run `project repos add`/`rm`/`sync`, `image add`, `image map`, `config publish`
  and `project policy publish`. `--delete` stays a person's (D3425).
- Files: `internal/cli/project_enroll.go`, `internal/cli/project_repos_rm.go`,
  `internal/cli/image_catalog.go`, `internal/cli/config_publish.go`,
  `internal/cli/project_policy_managed.go`.
- Tests: `TestAgentMayEnrolAndPublish`, `TestAgentPolicyDeleteRefused`.
- Command: `go test ./internal/cli/ -run 'TestAgentMayEnrolAndPublish|TestAgentPolicyDeleteRefused' -count=1`
- Red step: `TestAgentMayEnrolAndPublish` fails on `project repos add`.
- Depends on: none.
- Commit: `ground-rules AG4: enrolment and publishing are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

### AG5a: maintenance commands lift
- Acceptance: an agent may run `recipes publish`/`rm`, `runs reindex`, `issue sync`/`dismiss` and
  `stats runs` (D3425).
- Files: `internal/cli/recipes.go`, `internal/cli/runs_reindex.go`, `internal/cli/issue_sync.go`,
  `internal/cli/issue_dismiss.go`, `internal/cli/stats.go`.
- Tests: `TestAgentMayMaintain`.
- Command: `go test ./internal/cli/ -run TestAgentMayMaintain -count=1`
- Red step: the test fails on the `runs reindex` refusal.
- Depends on: none.
- Commit: `ground-rules AG5a: maintenance commands are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

### AG5b: reads and stops lift
- Acceptance: an agent may run `issue list`/`show`/`mine`, `runs digest`, `followups` and
  `budget kill --workspace`. `budget resume` stays refused (D3425).
- Files: `internal/cli/issue_list.go`, `internal/cli/issue_show.go`, `internal/cli/issue_mine.go`,
  `internal/cli/runs_digest.go`, `internal/cli/followups.go`, `internal/cli/budget_kill_workspace.go`.
- Tests: `TestAgentMayReadAndStop`.
- Command: `go test ./internal/cli/ -run TestAgentMayReadAndStop -count=1`
- Red step: the test fails on the `issue list` refusal.
- Depends on: none.
- Commit: `ground-rules AG5b: reads and stops are an agent's too (D3425)`
- Reviewer tier: standard.

### AG5c: applies and builds lift
- Acceptance: an agent may run `orchestrator patch`/`install`, `apply`, `managed apply` (through the
  reclassified gate), `image build` and the cloud half of `upgrade` (D3425).
- Files: `internal/cli/orchestrator_patch.go`, `internal/cli/orchestrator_install.go`,
  `internal/cli/apply.go`, `internal/cli/managed_apply.go`, `internal/cli/image_build_catalog.go`,
  `internal/cli/upgrade_cloud.go`.
- Tests: `TestAgentMayApplyAndBuild`.
- Command: `go test ./internal/cli/ -run TestAgentMayApplyAndBuild -count=1`
- Red step: the test fails on the `managed apply` refusal.
- Depends on: DG1a (behaviour: `managed apply` goes through the reclassified gate); DG1b (one file:
  `managed_apply.go`).
- Commit: `ground-rules AG5c: applies and builds are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

### CF1: the runner fetches a usable bake or clones
- Acceptance:
  - with a usable baked copy of the run's repository, the runner fetches and checks out;
  - with none, or with a foreign, unreadable or broken one (fetch or reset fails), it logs one line and
    clones into a fresh directory without touching the baked tree;
  - `FUGARO_CHECKOUT` is ignored;
  - the clone path (blobless plus `mise install`) is chosen by the absence of a usable bake;
  - a `shared` image (no source) clones (D3440, F35).
- Files: `internal/runner/runner.go` (`openCheckout`, `checkBakedCheckoutOrigin`),
  `internal/runner/projectjob.go`, `internal/runner/provision.go` (`cloneJob`),
  `internal/runner/checkout_fallback_test.go` (new), `docs/checkout.md`.
- Tests: `TestForeignBakeClonesFresh`, `TestBrokenBakeClonesFresh`, `TestUsableBakeFetches`,
  `TestNoBakeClones`.
- Command: `go test ./internal/runner/ -run 'TestForeignBakeClonesFresh|TestBrokenBakeClonesFresh|TestUsableBakeFetches|TestNoBakeClones' -count=1`
- Red step: `TestForeignBakeClonesFresh` fails on today's origin refusal.
- Depends on: none. It must ship no later than release A, because CF2a relies on every image carrying it
  after A's eager rebuild.
- Commit: `ground-rules CF1: a run fetches a usable bake or clones fresh (D3440)`
- Reviewer tier: opus (the agent's working tree).

### CF2a: deploy stops marking the checkout mode
- Acceptance: job specs no longer carry `FUGARO_CHECKOUT`, `jobCheckoutEnv` is gone, and D1191's
  `unbuiltOnBaked` refusal is gone.
- Files: `internal/infra/spec.go`, `internal/infra/checkjob.go`, `internal/infra/spec_test.go`.
- Tests: `TestJobSpecHasNoCheckoutEnv`.
- Command: `go test ./internal/infra/ -run TestJobSpecHasNoCheckoutEnv -count=1`
- Red step: the test finds `FUGARO_CHECKOUT` on a clone job.
- Depends on: CF1 (behaviour: the runner decides without the mark); VS3b and VS5 (behaviour: release A's
  eager rebuild puts CF1's runner in every image before a job loses its mark). **External:** release A has
  shipped, so CF2a lands in a later release.
- Commit: `ground-rules CF2a: jobs carry no checkout mode (D3440)`
- Reviewer tier: standard.

### CF2c: nothing reads the checkout mode
- Acceptance: no code outside `internal/config` reads `Workflow.Checkout` or `Profile.Checkout`. The
  catalog selection, image refresh and promote, launch, readiness and the settings writer behave as for
  `baked`, and `shared` image builds are refused as today (F35).
- Files: the readers F35 lists: `internal/catalog/select.go`, `internal/cli/image.go`,
  `image_promote.go`, `image_refresh.go`, `image_refresh_shared.go`, `image_refresh_steps.go`,
  `imagecheck.go`, `init.go`, `init_anchor.go`, `init_checkout_upgrade.go`, `init_review.go`,
  `project.go`, `project_job.go`, `run.go`, `settings_set.go`, `validate.go`,
  `internal/infra/readiness.go`, `internal/infra/repo.go` and `internal/localcfg/select.go`.
- Tests: `TestNothingReadsCheckout`, and each reader's existing tests.
- Command: `go build ./... && go test ./internal/catalog/ ./internal/infra/ ./internal/localcfg/ -count=1 && go test ./internal/cli/ -run 'TestNothingReadsCheckout|Image|Readiness' -count=1`
- Red step: `TestNothingReadsCheckout` lists today's readers.
- Depends on: CF2a (behaviour: deploy no longer marks the mode); AG2 (one file: `init.go`).
- Commit: `ground-rules CF2c: nothing reads the checkout mode (D3440)`
- Reviewer tier: standard. The file count is about twenty; each change removes a branch.

### CF2b: the `checkout` key is read and ignored
- Acceptance: `workflows.*.checkout` and `Profile.Checkout` load, are dropped in memory, and leave one
  note. The field, `validateCheckout` and the `clone`-only key refusals are gone (D3440).
- Files: `internal/config/config.go`, `internal/config/image.go`, `internal/config/defaults.go`,
  `internal/config/retired.go`, `internal/config/layer.go`, `internal/config/validate.go`.
- Tests: `TestRetiredCheckoutKey`.
- Command: `go build ./... && go test ./internal/config/ -run TestRetiredCheckoutKey -count=1`
- Red step: the test fails while `checkout: clone` refuses `image.apt`.
- Depends on: CF2c (build: the field leaves once nothing reads it).
- Commit: `ground-rules CF2b: the checkout key retires (D3440)`
- Reviewer tier: standard.

### VS1: the in-place upgrader
- Acceptance: given a wanted release above its own, the upgrader:
  - fetches the archive for exactly that tag from the fixed repository `dimipaun/fugaro` (a constant;
    nothing in the URL comes from the task beyond the validated semver), for linux and `runtime.GOARCH`;
  - fetches `checksums.txt` and checks the sha256;
  - unpacks into a fresh directory and makes the directory and the file read-only (F33);
  - retries briefly, then fails with one line naming the version;
  - returns nothing for a `dev` stamp or binary, or for a release not above its own (D3442, D3443,
    D3449).
- Files: `internal/selfupgrade/fetch.go` (new), `internal/selfupgrade/verify.go` (new),
  `internal/selfupgrade/install.go` (new), `internal/selfupgrade/selfupgrade_test.go` (new).
- Interfaces: the unexported `upgrade(ctx, want, have string) (exe string, err error)`. VS5 exports it.
- Tests: `TestUpgradeChecksSHA256`, `TestUpgradeRefusesMismatch`, `TestUpgradeFixedRepository`,
  `TestUpgradeNeverDowngrades`, `TestDevNeverUpgrades`, `TestUpgradeFailureOneLine`,
  `TestInstallReadOnly`, all against `httptest`.
- Command: `go test ./internal/selfupgrade/ -count=1`
- Red step: `TestUpgradeRefusesMismatch` fails with no verifier.
- Depends on: none.
- Commit: `ground-rules VS1: the runner can upgrade itself to a stamped release (D3442)`
- Reviewer tier: opus (code fetched and run inside the job).

### VS2: the launcher stamps the task
- Acceptance: `createTask` sets `CLIVersion` to the launching CLI's release on every launch path (run,
  follow-up, review, orchestrator, troubleshoot). A development build writes none (D3441, D3445, D3449).
- Files: `internal/cli/run.go` (`createTask`), `internal/cli/run_stamp_test.go` (new).
- Tests: `TestEveryLaunchStampsCLIVersion`, `TestDevBuildDoesNotStamp`.
- Command: `go test ./internal/cli/ -run 'TestEveryLaunchStampsCLIVersion|TestDevBuildDoesNotStamp' -count=1`
- Red step: the test finds an empty `cli_version`.
- Depends on: VS0 (the field).
- Commit: `ground-rules VS2: every launch stamps its CLI version (D3441)`
- Reviewer tier: standard.

### VS3a: the base-image epoch
- Acceptance:
  - `imagecheck.BaseEpoch` is a constant;
  - a test hashes `images/base/` minus the `FUGARO_VERSION` lines and fails when they change without the
    epoch moving;
  - each catalog build records the epoch it was built from (D3446).

  A catalog entry's own contents are already in its key, so `images/derived` and `images/catalog` need no
  epoch.
- Files: `internal/imagecheck/epoch.go` (new), `internal/imagecheck/epoch_test.go` (new),
  `internal/imagecheck/record.go`, `internal/cli/image_build_catalog.go`.
- Tests: `TestBaseEpochTracksBaseImage`, `TestRecordCarriesEpoch`.
- Command: `go test ./internal/imagecheck/ ./internal/cli/ -run 'Epoch' -count=1`
- Red step: `TestBaseEpochTracksBaseImage` fails with no recorded hash.
- Depends on: none.
- Commit: `ground-rules VS3a: the base-image epoch (D3446)`
- Reviewer tier: standard.

### VS3b: update rebuilds in the background, in parallel
- Acceptance:
  - an entry is stale by epoch or age, not by release;
  - `update` waits only when an epoch is stale, and a record with no epoch counts as stale, which is
    release A's one eager rebuild (D3445);
  - otherwise it submits the builds and waits for none;
  - builds run four at a time (D3444, D3446);
  - an installation that last ran `update` on the release before converges.
- Files: `internal/cli/init_images_catalog.go`, `internal/cli/init_stages.go`,
  `internal/cli/init_images_catalog_test.go`.
- Tests: `TestUpdateWaitsOnlyOnEpoch`, `TestReleaseAloneIsNotStale`, `TestCatalogBuildsFourAtOnce`,
  `TestPreviousReleaseConverges`.
- Command: `go test ./internal/cli/ -run 'TestUpdateWaitsOnlyOnEpoch|TestReleaseAloneIsNotStale|TestCatalogBuildsFourAtOnce|TestPreviousReleaseConverges' -count=1`
- Red step: `TestReleaseAloneIsNotStale` fails on `catalogBaseStale`.
- Depends on: VS3a (the recorded epoch; a contract would be the constant itself).
- Commit: `ground-rules VS3b: update no longer waits on a release's rebuild (D3446)`
- Reviewer tier: standard.

### VS4: the nightly rebuild runs, four at a time
- Acceptance:
  - when the layer is silent, `catalog_check` is unpaused, and the reports job keeps its own default;
  - an explicit `paused: true` stays paused;
  - each night every stale entry is rebuilt, four at a time;
  - an installation from the release before converges to the new default (D3444, F36).
- Files: `internal/config/layer.go` (`jobPaused`, for the catalog check only), `internal/infra/tfvars.go`,
  `internal/infra/catalogcheck.go`, `internal/cli/image_check_catalog.go`,
  `internal/cli/image_check_catalog_test.go`.
- Tests: `TestCatalogCheckUnpausedByDefault`, `TestReportsStillPaused`, `TestExplicitPauseHolds`,
  `TestNightlyRebuildsAllStale`.
- Command: `go test ./internal/config/ ./internal/infra/ ./internal/cli/ -run 'TestCatalogCheckUnpausedByDefault|TestReportsStillPaused|TestExplicitPauseHolds|TestNightlyRebuildsAllStale' -count=1`
- Red step: `TestCatalogCheckUnpausedByDefault` fails on `jobPaused`'s default.
- Depends on: VS3a (staleness by epoch).
- Commit: `ground-rules VS4: the nightly rebuild catches every image up (D3444)`
- Reviewer tier: standard.

### VS5: the runner upgrades before it runs
- Acceptance:
  - `fugaro exec` (the cloud path, not `--task-file` on a person's machine) parses the task, and when
    `cli_version` is above its own release it calls `selfupgrade.Ensure` and re-execs the result before
    anything else runs;
  - an environment marker stops a second upgrade;
  - an end-to-end test with a fake release server shows a stamped task re-executing the fetched binary,
    and an unfetchable stamp ending the run in one line.
- Files: `internal/cli/exec.go`, `internal/selfupgrade/selfupgrade.go` (new: the exported `Ensure` over
  VS1's `upgrade`), `internal/cli/exec_selfupgrade_test.go` (new).
- Tests: `TestExecUpgradesToStamp`, `TestExecUpgradesOnce`, `TestExecFailsOnUnfetchableStamp`,
  `TestLocalExecNeverUpgrades`.
- Command: `go test ./internal/cli/ ./internal/selfupgrade/ -run 'TestExec(UpgradesToStamp|UpgradesOnce|FailsOnUnfetchableStamp)|TestLocalExecNeverUpgrades' -count=1`
- Red step: `TestExecUpgradesToStamp` fails while `exec` ignores the stamp.
- Depends on: VS0 (it reads the field), VS1 (it calls the upgrader).
- Commit: `ground-rules VS5: the runner upgrades to its task's stamp (D3441)`
- Reviewer tier: opus (code fetched and run inside the job).

### REL1: the release's operator lines
- Acceptance: the next release's notes (`docs/releases/vX.Y.Z.md`) carry a `### For operators` section:
  - "Run `fugaro update`. It rebuilds every image once and waits." (VS5, VS3b);
  - "Repositories still on their own jobs move to the shared ones; their secrets are kept." (UO2);
  - "The nightly image check now runs; set `jobs.catalog_check.paused: true` to keep it off." (VS4);
  - "Teammates already on this release cannot launch until the update finishes." (D3448).
- Files: `docs/releases/v*.md` (the next release's file).
- Tests: the release notes' checks in `scripts/release.sh`'s preflight, and the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents).
- Depends on: UO2, VS3b, VS4, VS5 (the lines describe them).
- Commit: `ground-rules REL1: the operator lines for the ground-rules release`
- Reviewer tier: standard.

The lines sit in one task rather than in each of four PRs because all four tasks land in one level, and
one file cannot have four writers there. REL1 lands before the release is cut, which is what the
`For operators` rule needs.

## Parallel execution

### Edges

| Task | Needs | Reason |
| --- | --- | --- |
| UO2, DG1a, DG2, DG3, AG2 | DG0 | the class table |
| DOC1, DOC2 | UO2, AG2 | behaviour: the text describes shipped behaviour |
| UO4a | UO2 | behaviour: plain `update` covers every repository first |
| UO4a | DOC1, DOC2 | the plugin and Docs tests: nothing may name the flag when it goes |
| UO4a | CF2c | one file: `init.go` |
| UO4b | UO4a | the sweep follows the flag's removal |
| CV2 | CV1 | `SupportedVersions`; a contract would be the table itself |
| CV3 | VS5, VS2 | behaviour: runners upgrade themselves |
| CV3 | CF2c | behaviour: the `clone` value is gone |
| CV3 | UO4a, UO4b | one file: `init.go`, and the sweep |
| DG1b | DG1a | behaviour: nothing needs the list once the rule changed |
| DG2 | UO2 | one file: `init.go` |
| AG2 | DG1a | behaviour: the gate admits recreatable deletes first |
| AG2 | DG2 | one file: `init.go`; `--allow-delete` is gone before agents run every stage |
| AG5c | DG1a | behaviour: `managed apply` goes through the reclassified gate |
| AG5c | DG1b | one file: `managed_apply.go` |
| DG4 | DG1b, DG2, UO2, AG2 | behaviour: the end-to-end test |
| CF2a | CF1 | behaviour: the runner decides without the mark |
| CF2a | VS3b, VS5 | behaviour: release A's eager rebuild puts CF1's runner in every image |
| CF2c | CF2a | behaviour: deploy no longer marks the mode |
| CF2c | AG2 | one file: `init.go` |
| CF2b | CF2c | build: the field leaves once nothing reads it |
| VS2 | VS0 | the field |
| VS5 | VS0, VS1 | the field and the upgrader |
| VS3b, VS4 | VS3a | the recorded epoch; a contract would be the constant itself |
| REL1 | UO2, VS3b, VS4, VS5 | the operator lines describe them |

### The dependencies broken and kept

The contract-first pass ran and broke no edge.

- **Tried and undone: VS2 on VS1.** The launcher needs only the stamp field, so a contract could have let
  it run beside the upgrader. The graph read reported no gain, because the critical path is the `init.go`
  chain, so the break was undone. Instead, VS0 (the field) is its own small first task, and VS1 is
  self-contained.
- **DG0 is not a contract.** UO2, AG2, DG1a, DG2 and DG3 all wait on DG0, the class table. It is small
  and first, and those five then run as their other edges allow. No provider builds beside it, so it is an
  ordinary task.
- **Kept.**
  - CV1→CV2, VS3a→VS3b and VS3a→VS4: a contract would be the shared constant or table itself, which is
    nearly the whole of the first task.
  - CF1→CF2a→CF2c→CF2b: behaviour and the build.
  - Every "one file" edge exists because no level may hold two writers of one file. `init.go` has seven
    writers, which forces CO3, UO2, DG2, AG2, CF2c, UO4a and CV3 into that order.

### Levels

| Level | Tasks |
| --- | --- |
| 0 | DG0, VS0, UO1, UO3, CV1, CO1, CO2, CO3, CO4, CO5, AG3, AG4, AG5a, AG5b, CF1, VS1, VS3a |
| 1 | UO2, CV2, DG1a, DG3, VS2, VS3b, VS4, VS5 |
| 2 | DG1b, DG2, CF2a, REL1 |
| 3 | AG2, AG5c |
| 4 | DOC1, DOC2, DG4, CF2c |
| 5 | UO4a, CF2b |
| 6 | UO4b (alone: a sweep of `internal/`) |
| 7 | CV3 (also waits on release A) |

The critical path is DG0 → UO2 → DG2 → AG2 → DOC1 → UO4a → UO4b → CV3, eight tasks with DG0 the
bottleneck, as the graph read reports. Release A is cut after level 2. It carries VS0–VS5 and CF1, and its
`update` runs the eager rebuild.

### Shared files

| File | Writers, by level |
| --- | --- |
| `internal/cli/init.go` | CO3 (0), UO2 (1), DG2 (2), AG2 (3), CF2c (4), UO4a (5), CV3 (7) |
| `internal/cli/run.go` | VS2 (1), CF2c (4), CV3 (7) |
| `internal/cli/image.go` | CO3 (0), CF2c (4) |
| `internal/cli/image_build_catalog.go` | VS3a (0), AG5c (3) |
| `internal/cli/init_refusal.go` | DG2 (2), UO4a (5) |
| `internal/cli/init_retire.go` | UO2 (1), UO4a (5) |
| `internal/cli/init_stages.go` | VS3b (1), AG2 (3) |
| `internal/cli/managed_apply.go` | DG1b (2), AG5c (3) |
| `internal/cli/root.go` | CO4 (0), DG1b (2) |
| `internal/config/config.go` | CV1 (0), CF2b (5) |
| `internal/config/layer.go` | VS4 (1), CF2b (5) |
| `internal/config/retired.go` | UO3 (0), CF2b (5) |
| `internal/config/validate.go` | CV1 (0), CF2b (5) |
| `internal/infra/spec.go` | UO2 (1), CF2a (2) |
| `internal/initflow/loop.go` | CO1 (0), AG2 (3) |
| `website/content/cli-reference.md` | AG3 (0), DG2 (2), UO4a (5) |
| `internal/**/*.go` | UO4b (6), alone in its level |

### Contract changes anticipated

None: the plan has no contract.

## Estimate

The graph read prints one line for the plan:

> ready in 581–2359 min, no history yet for 30 tasks (39 tasks; tiers for 9, seeds for 30; from the cached history of 2026-11-22 07:25 UTC)

It is informational: it reserves no budget, and it moves as runs finish and as the graph changes.

## Checks (owner-run)

- **Check 950.** In a coding-agent session on project `fugaro`, the agent runs `fugaro update` to the
  end. Every stage applies, nothing irreplaceable is deleted, and the keep-list flags are refused. After
  AG2 and DG4.
- **Check 951.** After release A, launch a run with a CLI one release ahead of the image. The run's log
  shows the upgrade to exactly that release, and the run completes. This also settles U1 and U3. After
  VS5 and VS2 ship.
- **Check 952.** Leave the project overnight with a stale catalog. By morning every stale entry has
  rebuilt, four at a time. After VS4.
- **Check 953.** On a project with one repository still on its own root, `fugaro update` retires the
  root. The repository's secret containers remain, and the next run starts on the shared job. After UO2.

Checks 954–959 are reserved and unused.

## After-deploy steps

None beyond `fugaro update`. REL1 writes the release's `### For operators` lines.
