# Ground rules: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped the plan are D3420–D3449, all given on 2026-11-26. Three of them are not the
recommendation: D3441 (the stamp lives in `task.json`), D3445 (one eager rebuild) and D3448 (accept the
launch window). Nine are resolutions: D3424, D3426, D3428, D3429, D3434, D3435, D3439, D3446 and D3449.

## Conventions every part follows

- `CLAUDE.md` throughout, in particular:
  - the HARD RULE on `fugaro update`, safe with runs in flight (`TestUpdateLeavesARunInFlightRunning`
    must keep passing in every task that touches `update`);
  - Fugaro asks for nothing; no task adds a confirmation;
  - "a field never changes meaning": `task.Spec` gains a field, it never retypes one;
  - hermetic tests (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`); no network (the
    self-upgrade tests use `httptest`); no secret-shaped literal;
  - comments explain why;
  - targeted tests (`-run`), and `go test ./internal/cli/ -run Docs` for any document change.
- Area rules load by path: `.claude/rules/terraform.md` (VS4), `.claude/rules/credentials-service.md`
  (DOC1).
- **Command help text.** A task that changes a flag or a command's help regenerates
  `website/content/cli-reference.md` with `go run ./ee/clidoc` and runs `go test ./ee/clidoc/`. The
  plugin tests (`go test ./plugin/...`) check every flag a skill names, so a flag leaves the CLI only after
  the skills stop naming it (UO4 waits on DOC1).
- Human output follows the owner's brew style (`internal/report`; see F20): one line per step, colour
  only on a TTY, `NO_COLOR` honoured, `--json` unchanged except where D3437 adds to it.
- Boundaries:
  - **Always:** classify a delete through DG0' `tf.DeleteClass`, never by a resource name list of a
    task's own; refuse an irreplaceable delete in at most two sentences; keep secret containers when a root
    retires.
  - **Ask first:** a new Go dependency; a change to a wire format beyond the one field VS0 adds; any
    new prompt or flag.
  - **Never:** delete a project, a bucket, the RTDB, Firestore, a KMS key or Terraform state; delete a
    current member's secret; print raw Terraform or build output on the terminal; edit
    `.github/workflows/` (stage under `docs/pending-workflows/` instead).
- Live checks are the owner's (Checks 950–953).
- The project's rigor is `startup` and decoupling is on (`planning.decoupling`, true), so the plan
  decouples where an interface is all a dependent needs and the read shows a gain.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| An unknown resource type is irreplaceable (fail closed) | DG0' table test |
| `update` never plans a destroy of a project, bucket, RTDB, Firestore, KMS key or a member's secret | DG3 |
| A retired root keeps its secret containers (removed from state, not destroyed) | UO2, DG4 |
| An agent's `update` in a managed project runs every stage and refuses only irreplaceable deletes | AG2, DG4 |
| A runs-in-flight update stays safe after the gate change | AG2, UO2 (`TestUpdateLeavesARunInFlightRunning`) |
| The runner upgrades only to exactly the stamped release, checked by sha256; never a dev build, never down | VS1, VS5 |
| A failed upgrade ends the run in one line naming the version | VS1 |
| A foreign or broken baked tree is never used for a run | CF1 |
| A file newer than the reader is refused in one line; an old supported version reads the same as before | CV1 |
| No human message over 140 characters outside the allowlist | CO5 |

## Slices

1. **The first pieces** (DG0, VS0, UO1): inert. The class table, the stamp field the runner accepts, and
   the older documents brought in line.
2. **Output** (CO1–CO5): useful alone; no dependency on any other slice.
3. **Deletes by class** (DG1–DG3): the gate and the guard judge what is deleted.
4. **One update for the project** (UO2, UO3, UO5, then DOC1, DOC2, UO4): the per-repository root retires,
   the compute root joins `update`, and `--repo` goes last.
5. **Agents run update** (AG2–AG5, DG4).
6. **Checkout** (CF1, CF2a, CF2b).
7. **Versions** (CV1, CV2, VS1–VS4, VS5, then CV3 one release later).

## Tasks

AG1 is the audit itself, §A of the reconciliation; the agent tasks start at AG2.

### DG0: the delete classes
- Acceptance: `tf.DeleteClass(resourceType string) tf.Class` returns `Recreatable` for every type D3430
  lists as recreatable and `Irreplaceable` for every type it lists as irreplaceable or does not know.
- Files: `internal/infra/tf/classes.go` (new), `internal/infra/tf/classes_test.go` (new),
  `docs/ground-rules/delete-classes.md` (new).
- Interfaces: produces `tf.Class`, `tf.DeleteClass`, and the table as data. Consumed by DG1, DG2, DG3, UO2,
  AG2 and DG4. A task that finds a type Fugaro makes missing from the table adds it in its own change.
  This is an ordinary task, not a contract: the table is the whole of the work, so there is no separate
  provider to run beside it.
- Tests: `TestDeleteClassTable`, `TestDeleteClassUnknownIsIrreplaceable`.
- Command: `go test ./internal/infra/tf/ -run 'TestDeleteClass' -count=1`
- Red step: `TestDeleteClassUnknownIsIrreplaceable` fails while the function returns the zero value.
- Depends on: none.
- Commit: `ground-rules DG0: the delete classes, recreatable and irreplaceable`
- Reviewer tier: opus (a write guard's input).

### VS0: the stamp field
- Acceptance: `task.Parse` accepts `cli_version` and round-trips it, and the schema names it. A runner with
  VS0 reads a stamped task; one without it refuses it (D3445, D3448).
- Files: `internal/task/task.go`, `internal/task/task_test.go`, `schemas/task.schema.json`.
- Interfaces: `task.Spec.CLIVersion string` (`json:"cli_version,omitempty"`). Consumed by VS2 and VS5.
- Tests: `TestParseCLIVersion`, `TestSchemaHasCLIVersion`.
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
  - the stale "nothing is implemented" status lines F14 names are corrected.
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
  - a converge test shows one retired root and one kept container.
- Files: `internal/infra/spec.go`, `internal/cli/init.go` (`repoEngine`, `enrolSharedRepo`,
  `retireSharedRoot`), `internal/cli/init_retire.go`, `internal/cli/init_repo_stage.go`,
  `internal/cli/update_retire_root_test.go` (new).
- Interfaces: consumes DG0. Produces a repository stage that covers every enrolled repository with
  no checkout.
- Tests: `TestUpdateRetiresEveryRepoRoot`, `TestRetiredRootKeepsSecretContainers`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateRetiresEveryRepoRoot|TestRetiredRootKeepsSecretContainers|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateRetiresEveryRepoRoot` fails while retirement still needs `--retire`.
- Depends on: DG0 (interface), CO3 (one file: `init.go`).
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

### UO5: the compute root is an update stage
- Acceptance: when the project layer enables the batch backend, `update` applies the compute root as a
  stage. `--provision-batch` goes, and the `backends.go` refusal names `fugaro update` (D3424).
- Files: `internal/cli/init_compute.go`, `internal/cli/init.go`, `internal/cli/init_stages.go`,
  `internal/cli/backends.go`, `internal/cli/doctor_batch.go`, `website/content/cli-reference.md`.
- Tests: `TestUpdateAppliesComputeWhenBatchEnabled`, `TestUpdateSkipsComputeWithoutBatch`.
- Command: `go test ./internal/cli/ -run 'TestUpdate(Applies|Skips)Compute' -count=1`
- Red step: `TestUpdateAppliesComputeWhenBatchEnabled` fails while only `setup --provision-batch`
  applies it.
- Depends on: DG2 (one file: `init.go`).
- Commit: `ground-rules UO5: update applies the compute root when batch is on (D3424)`
- Reviewer tier: opus (IAM, billable resources).

### DOC1: the standing rules rewritten
- Acceptance:
  - CLAUDE.md's HARD RULE says `update` is the one project-wide operation, agents may run it, and it
    rebuilds images only on a base change;
  - CLAUDE.md points to `docs/ground-rules/` in one line;
  - the credentials-service rules and the `update`, `setup` and `images` skills name no `update --repo`
    (D3447).
- Files: `CLAUDE.md`, `.claude/rules/credentials-service.md`, `plugin/skills/update/SKILL.md`,
  `plugin/skills/setup/SKILL.md`, `plugin/skills/images/SKILL.md`.
- Tests: the plugin tests and the Docs tests.
- Command: `go test ./plugin/... -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents); `git grep -n 'update --repo' CLAUDE.md .claude plugin/skills` must print
  nothing.
- Depends on: UO2, AG2 (behaviour: the text describes what has shipped).
- Commit: `ground-rules DOC1: CLAUDE.md, rules and skills follow the ground rules (D3447)`
- Reviewer tier: opus (CLAUDE.md, rules files, plugin skills).

### DOC2: the how-tos and the managed handoffs
- Acceptance: `docs/how-to/budgets.md`, `registry-cleanup.md` and `offboarding.md` name no
  `update --repo`, and `docs/managed-agent-converge/handoffs.md` records D3425 (F13).
- Files: the four above, plus `docs/checkout.md` if a Docs test pins its `--repo` text.
- Tests: the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents).
- Depends on: UO2 (behaviour).
- Commit: `ground-rules DOC2: how-tos for a project-wide update`
- Reviewer tier: standard.

### UO4: `update --repo` goes
- Acceptance: `--repo`, `--no-build`, `--retire` and `--allow-job-delete` are gone from `setup` and
  `update`. No message names them. `retireNextCommand` is deleted. The CLI reference is regenerated.
- Files: `internal/cli/init.go`, `internal/cli/init_refusal.go`, `internal/cli/init_repo_target.go`,
  `internal/cli/init_retire.go`, `website/content/cli-reference.md`.
- Tests: `TestUpdateHasNoRepoFlag`, the plugin tests, `go test ./ee/clidoc/`.
- Command: `go test ./internal/cli/ -run 'TestUpdateHasNoRepoFlag|Docs' -count=1 && go test ./ee/clidoc/ ./plugin/... -count=1`
- Red step: `TestUpdateHasNoRepoFlag` fails while the flag is registered.
- Depends on: UO2 (behaviour), DOC1 and DOC2 (the skills and docs must stop naming it), UO5 (one file:
  `init.go`).
- Commit: `ground-rules UO4: update --repo and its flags go (D3420, D3431)`
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
- Depends on: CV1 (kept: it reads CV1's `SupportedVersions`, and a contract would be that table).
- Commit: `ground-rules CV2: doctor and update name a deprecated fugaro.yaml version`
- Reviewer tier: standard.

### CV3: the image-age gates retire
- Acceptance: the per-key `*Since` gates in `recipes_skew.go` and `gcpProjectFieldSince` are gone (D3446).
  A launch no longer reads a build record's release to admit a key.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/init_fugaroyaml.go`, their tests.
- Tests: `TestLaunchNeedsNoImageAgeGate`.
- Command: `go test ./internal/cli/ -run 'TestLaunchNeedsNoImageAgeGate|Skew' -count=1`
- Red step: the test fails while `cloneSince` and the others still refuse.
- Depends on: VS5 and VS2 (behaviour: runners upgrade themselves); CF2b (behaviour: no `clone` value
  left to gate). **External:** release A (the one with VS5) has shipped and its eager rebuild has run.
- Commit: `ground-rules CV3: the image-age gates retire (D3446)`
- Reviewer tier: standard.

### DG1: the gate judges what is deleted
- Acceptance:
  - gate rule 1 admits a recreatable delete and refuses an irreplaceable one, in at most two sentences;
  - `fugaro allow-delete`, `fugaro/allow-delete.json` and `internal/infra/managed/deletelist.go` are gone
    (D3427).
- Files: `internal/infra/tf/managed_cover.go`, `internal/cli/managed_gate.go`,
  `internal/infra/managed/deletelist.go` (deleted), `internal/cli/allow_delete.go` (deleted),
  `internal/cli/root.go`.
- Interfaces: consumes DG0.
- Tests: `TestGateAdmitsRecreatableDelete`, `TestGateRefusesIrreplaceableDelete`.
- Command: `go test ./internal/infra/tf/ ./internal/cli/ -run 'TestGate(Admits|Refuses)' -count=1`
- Red step: `TestGateAdmitsRecreatableDelete` fails while the delete list is required.
- Depends on: DG0 (interface).
- Commit: `ground-rules DG1: the managed gate judges what is deleted (D3427)`
- Reviewer tier: opus (the write guard).

### DG2: the guard judges what is deleted; `--allow-delete` goes
- Acceptance: `tf.Guard` refuses only irreplaceable deletes or replaces, and `--allow-delete` is gone.
  The refusal says in at most two sentences which resource and that `update` never deletes it (D3431).
- Files: `internal/infra/tf/guard.go`, `internal/infra/tf/guard_test.go`, `internal/cli/init.go`,
  `internal/cli/init_refusal.go`, `website/content/cli-reference.md`.
- Tests: `TestGuardPassesRecreatable`, `TestGuardRefusesIrreplaceable`.
- Command: `go test ./internal/infra/tf/ -run TestGuard -count=1 && go test ./ee/clidoc/ -count=1`
- Red step: `TestGuardPassesRecreatable` fails on today's refusal.
- Depends on: DG0 (interface), AG2 (one file: `init.go`).
- Commit: `ground-rules DG2: the guard judges what is deleted; --allow-delete goes (D3431)`
- Reviewer tier: opus (the write guard).

### DG3: what update never destroys
- Acceptance: a test over every root's plan fixtures and every `prevent_destroy` block shows that no
  normal command plans a destroy of a project, a bucket, the RTDB, Firestore, a KMS key or a member's
  secret (D3432, D3434).
- Files: `internal/infra/tf/never_destroy_test.go` (new), `internal/cli/update_member_secrets_test.go`
  (new).
- Tests: `TestIrreplaceableCarryPreventDestroy`, `TestUpdateKeepsMemberSecrets`.
- Command: `go test ./internal/infra/tf/ ./internal/cli/ -run 'TestIrreplaceableCarryPreventDestroy|TestUpdateKeepsMemberSecrets' -count=1`
- Red step: the first test, written against a fixture with `prevent_destroy` removed, fails.
- Depends on: DG0 (interface).
- Commit: `ground-rules DG3: pin what update never destroys`
- Reviewer tier: opus.

### CO1: plain step names
- Acceptance: every converge stage prints a plain-words name ("Applying infrastructure changes"), and the
  stage ID stays in the log and in `--json`/`--events` (D3435).
- Files: `internal/initflow/stage.go`, `internal/initflow/loop.go`, `internal/cli/testdata/` goldens that
  show step lines.
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
- Depends on: DG1 (one file: `root.go`).
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
  - `shapesTheInstallation` no longer refuses (D3425).
- Files: `internal/initflow/loop.go` (`agentStage`), `internal/cli/update_managed.go`,
  `internal/cli/init.go`, `internal/cli/init_stages.go`, `internal/cli/initsecrets.go`, `internal/cli/init_compute.go`.
- Interfaces: consumes DG0 (through the gate's unchanged `admit`).
- Tests: `TestAgentUpdateRunsEveryStage`, `TestAgentSecretsStageSkipsWithLine`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestAgentUpdateRunsEveryStage|TestAgentSecretsStageSkipsWithLine|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestAgentUpdateRunsEveryStage` fails on Compute's `AgentRefusal`.
- Depends on: DG0 (interface); UO2 (one file: `init.go`); CO1 (one file: `loop.go`); VS3b (one
  file: `init_stages.go`).
- Commit: `ground-rules AG2: an agent runs the whole update (D3425)`
- Reviewer tier: opus (the agent's reach and the gate).

### AG3: settings, rigor and accounts picks lift
- Acceptance: an agent may run `settings set`/`unset` (every key), `rigor set`, `rigor floor`, and
  `accounts switch`, `alias` and `pool`. `--yes` is gone from `accounts alias` and `accounts switch`
  (D3425, D3429).
- Files: `internal/cli/settings_managed.go`, `internal/cli/rigor_floor.go`,
  `internal/cli/accounts_switch.go`, `internal/cli/accounts_alias.go`, `internal/cli/accounts_pool.go`.
- Tests: `TestAgentMaySet`, `TestAccountsHaveNoYes`.
- Command: `go test ./internal/cli/ -run 'TestAgentMaySet|TestAccountsHaveNoYes' -count=1 && go test ./ee/clidoc/ ./plugin/... -count=1`
- Red step: `TestAgentMaySet` fails on the `rigor set` refusal.
- Depends on: none.
- Commit: `ground-rules AG3: settings, rigor and account picks are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

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

### AG5: the remaining person-only commands lift
- Acceptance: an agent may run `recipes publish`/`rm`, `runs reindex`, `issue sync`/`dismiss`,
  `orchestrator patch`/`install`, `stats runs` and `apply` (D3425).
- Files: `internal/cli/recipes.go`, `internal/cli/runs_reindex.go`, `internal/cli/issue_sync.go`,
  `internal/cli/issue_dismiss.go`, `internal/cli/orchestrator_patch.go`; `orchestrator_install.go`,
  `stats.go` and `apply.go` if their refusal is not a shared helper.
- Tests: `TestAgentMayMaintain`.
- Command: `go test ./internal/cli/ -run TestAgentMayMaintain -count=1`
- Red step: the test fails on the `runs reindex` refusal.
- Depends on: none.
- Commit: `ground-rules AG5: maintenance commands are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

### DG4: deletes by class, end to end
- Acceptance: on the fake cloud, an agent's `update` in a managed project:
  - retires an orphaned repository root (jobs deleted, container kept);
  - applies a plan that replaces a job;
  - refuses a plan that destroys a secret, in at most two sentences;

  all in one run, with runs in flight.
- Files: `internal/cli/update_ground_rules_test.go` (new).
- Tests: `TestGroundRulesUpdateEndToEnd`.
- Command: `go test ./internal/cli/ -run TestGroundRulesUpdateEndToEnd -count=1`
- Red step: the test fails before DG1, DG2, UO2 and AG2 are in.
- Depends on: DG1, DG2, UO2, AG2 (and through them DG0).
- Commit: `ground-rules DG4: update by class, end to end`
- Reviewer tier: opus.

### CF1: the runner fetches a usable bake or clones
- Acceptance:
  - with a usable baked copy of the run's repository, the runner fetches and checks out;
  - with none, or with a foreign, unreadable or broken one (fetch or reset fails), it logs one line and
    clones into a fresh directory without touching the baked tree;
  - `FUGARO_CHECKOUT` is ignored;
  - the clone path (blobless plus `mise install`) is chosen by the absence of a usable bake (D3440).
- Files: `internal/runner/runner.go` (`openCheckout`, `checkBakedCheckoutOrigin`),
  `internal/runner/projectjob.go`, `internal/runner/provision.go` (`cloneJob`),
  `internal/runner/checkout_fallback_test.go` (new), `docs/checkout.md`.
- Tests: `TestForeignBakeClonesFresh`, `TestBrokenBakeClonesFresh`, `TestUsableBakeFetches`.
- Command: `go test ./internal/runner/ -run 'TestForeignBakeClonesFresh|TestBrokenBakeClonesFresh|TestUsableBakeFetches' -count=1`
- Red step: `TestForeignBakeClonesFresh` fails on today's origin refusal.
- Depends on: none.
- Commit: `ground-rules CF1: a run fetches a usable bake or clones fresh (D3440)`
- Reviewer tier: opus (the agent's working tree).

### CF2a: deploy stops marking the checkout mode
- Acceptance: job specs no longer carry `FUGARO_CHECKOUT`, `jobCheckoutEnv` is gone, and D1191's
  `unbuiltOnBaked` refusal is gone.
- Files: `internal/infra/spec.go`, `internal/infra/checkjob.go`, `internal/infra/spec_test.go`.
- Tests: `TestJobSpecHasNoCheckoutEnv`.
- Command: `go test ./internal/infra/ -run TestJobSpecHasNoCheckoutEnv -count=1`
- Red step: the test finds `FUGARO_CHECKOUT` on a clone job.
- Depends on: CF1 (behaviour: the runner must decide without the mark first); UO2 (one file:
  `infra/spec.go`).
- Commit: `ground-rules CF2a: jobs carry no checkout mode (D3440)`
- Reviewer tier: standard.

### CF2b: the `checkout` key is read and ignored
- Acceptance: `workflows.*.checkout` and `Profile.Checkout` load, are dropped in memory, and leave one
  note. The `clone`-only key refusals and `validateCheckout` are gone (D3440).
- Files: `internal/config/config.go`, `internal/config/image.go`, `internal/config/defaults.go`,
  `internal/config/retired.go`, `internal/config/layer.go`.
- Tests: `TestRetiredCheckoutKey`.
- Command: `go test ./internal/config/ -run TestRetiredCheckoutKey -count=1`
- Red step: the test fails while `checkout: clone` refuses `image.apt`.
- Depends on: CF2a (it compiles against `w.Checkout`'s removal); UO3 (one file: `retired.go`); CV1 (one
  file: `config.go`).
- Commit: `ground-rules CF2b: the checkout key retires (D3440)`
- Reviewer tier: standard.

### VS1: the in-place upgrader
- Acceptance: given a wanted release above its own, the upgrader:
  - fetches the linux archive and `checksums.txt` for exactly that tag;
  - checks the sha256;
  - unpacks to a writable path and returns it;
  - retries briefly, then fails with one line naming the version;
  - returns nothing for a `dev` stamp or binary, or for a release not above its own (D3442, D3443,
    D3449).
- Files: `internal/selfupgrade/fetch.go` (new), `internal/selfupgrade/verify.go` (new),
  `internal/selfupgrade/install.go` (new), `internal/selfupgrade/selfupgrade_test.go` (new).
- Interfaces: self-contained: the unexported `upgrade(ctx, want, have string) (exe string, err error)` and
  its own types. VS5 exports it as `Ensure`.
- Tests: `TestUpgradeChecksSHA256`, `TestUpgradeRefusesMismatch`, `TestUpgradeNeverDowngrades`,
  `TestDevNeverUpgrades`, `TestUpgradeFailureOneLine`, all against `httptest`.
- Command: `go test ./internal/selfupgrade/ -count=1`
- Red step: `TestUpgradeRefusesMismatch` fails with no verifier.
- Depends on: none.
- Commit: `ground-rules VS1: the runner can upgrade itself to a stamped release (D3442)`
- Reviewer tier: opus (code fetched and run inside the job).

### VS2: the launcher stamps the task
- Acceptance: `createTask` sets `CLIVersion` to the launching CLI's release on every launch path (run,
  follow-up, review, orchestrator, troubleshoot) (D3441, D3445).
- Files: `internal/cli/run.go` (`createTask`), `internal/cli/run_stamp_test.go` (new).
- Tests: `TestEveryLaunchStampsCLIVersion`.
- Command: `go test ./internal/cli/ -run TestEveryLaunchStampsCLIVersion -count=1`
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
  - builds run four at a time (D3444, D3446).
- Files: `internal/cli/init_images_catalog.go`, `internal/cli/init_stages.go`,
  `internal/cli/init_images_catalog_test.go`.
- Tests: `TestUpdateWaitsOnlyOnEpoch`, `TestReleaseAloneIsNotStale`, `TestCatalogBuildsFourAtOnce`.
- Command: `go test ./internal/cli/ -run 'TestUpdateWaitsOnlyOnEpoch|TestReleaseAloneIsNotStale|TestCatalogBuildsFourAtOnce' -count=1`
- Red step: `TestReleaseAloneIsNotStale` fails on `catalogBaseStale`.
- Depends on: VS3a (kept: it reads the recorded epoch, and the contract would be the constant itself).
- Commit: `ground-rules VS3b: update no longer waits on a release's rebuild (D3446)`
- Reviewer tier: standard.

### VS4: the nightly rebuild runs, four at a time
- Acceptance: `catalog_check` is unpaused by default and rebuilds every stale entry, four at a time
  (D3444).
- Files: `deploy/terraform/gcp/roots/installation/variables.tf`, `internal/infra/catalogcheck.go`,
  `internal/cli/image_check_catalog.go`, `internal/cli/image_check_catalog_test.go`.
- Tests: `TestNightlyRebuildsAllStale`, `TestCatalogCheckUnpausedByDefault`, plus
  `terraform fmt -check -recursive deploy/terraform`.
- Command: `go test ./internal/cli/ ./internal/infra/ -run 'TestNightlyRebuildsAllStale|TestCatalogCheckUnpausedByDefault' -count=1`
- Red step: `TestNightlyRebuildsAllStale` fails at the cap of one.
- Depends on: VS3a (kept: staleness by epoch).
- Commit: `ground-rules VS4: the nightly rebuild catches every image up (D3444)`
- Reviewer tier: standard.

### VS5: the runner upgrades before it runs
- Acceptance:
  - `fugaro exec` parses the task, and when `cli_version` is above its own release it calls
    `selfupgrade.Ensure` and re-execs the result before anything else runs;
  - an end-to-end test with a fake release server shows a stamped task re-executing the fetched binary,
    and an unfetchable stamp ending the run in one line.
- Files: `internal/cli/exec.go`, `internal/selfupgrade/selfupgrade.go` (new: the exported `Ensure` over
  VS1's `upgrade`), `internal/cli/exec_selfupgrade_test.go` (new).
- Tests: `TestExecUpgradesToStamp`, `TestExecFailsOnUnfetchableStamp`.
- Command: `go test ./internal/cli/ ./internal/selfupgrade/ -run 'TestExec(UpgradesToStamp|FailsOnUnfetchableStamp)' -count=1`
- Red step: `TestExecUpgradesToStamp` fails while `exec` ignores the stamp.
- Depends on: VS0 (it reads the field), VS1 (it calls the upgrader).
- Commit: `ground-rules VS5: the runner upgrades to its task's stamp (D3441)`
- Reviewer tier: opus (code fetched and run inside the job).
## Parallel execution

### Edges

| Task | Needs | Reason |
| --- | --- | --- |
| UO2 | DG0 | interface: the class table |
| UO2 | CO3 | one file: `init.go` |
| UO5 | DG2 | one file: `init.go` |
| DOC1 | UO2, AG2 | behaviour: the text describes shipped behaviour |
| DOC2 | UO2 | behaviour |
| UO4 | UO2 | behaviour: plain `update` covers every repository first |
| UO4 | DOC1, DOC2 | the plugin and Docs tests: nothing may name the flag when it goes |
| UO4 | UO5 | one file: `init.go` |
| CV2 | CV1 | `SupportedVersions`; a contract would be the table itself |
| CV3 | VS5, VS2 | behaviour: runners upgrade themselves |
| CV3 | CF2b | behaviour: the `clone` value is gone |
| DG1, DG2, DG3 | DG0 | the class table |
| DG2 | AG2 | one file: `init.go` |
| CO4 | DG1 | one file: `root.go` |
| AG2 | DG0 | interface |
| AG2 | UO2 | one file: `init.go` |
| AG2 | CO1 | one file: `loop.go` |
| AG2 | VS3b | one file: `init_stages.go` |
| DG4 | DG1, DG2, UO2, AG2 | behaviour: the end-to-end test needs all four |
| CF2a | CF1 | behaviour: the runner decides without the mark |
| CF2a | UO2 | one file: `infra/spec.go` |
| CF2b | CF2a | build: `w.Checkout` is removed under it |
| CF2b | UO3 | one file: `retired.go` |
| CF2b | CV1 | one file: `config.go` |
| VS2 | VS0 | the field |
| VS5 | VS0, VS1 | the field and the upgrader |
| VS3b, VS4 | VS3a | the recorded epoch; a contract would be the constant itself |

### The dependencies broken and kept

The contract-first pass ran and broke no edge.

- **Tried and undone: VS2 on VS1.** The launcher needs only the stamp field, so a contract could have let
  it run beside the upgrader. The graph read reported no gain, because the critical path is the `init.go`
  chain, so the break was undone. Instead, VS0 (the field) is its own small first task, and VS1 is
  self-contained.
- **DG0 is not a contract.** UO2, AG2, DG1, DG2 and DG3 all wait on DG0, the class table. It is small and
  first. Carving it out of DG1 lets those five run side by side, and DG4 tests them together. No provider
  builds beside it, so it is an ordinary task.
- **Kept.**
  - CV1→CV2, VS3a→VS3b and VS3a→VS4: a contract would be the shared constant or table itself, which is
    nearly the whole of the first task.
  - CF1→CF2a→CF2b: behaviour and the build.
  - Every "one file" edge exists because no level may hold two writers of one file. `init.go` has six
    writers, which forces CO3, UO2, AG2, DG2, UO5 and UO4 into that order.

### Levels

| Level | Tasks |
| --- | --- |
| 0 | DG0, VS0, UO1, UO3, CV1, CO1, CO2, CO3, CO5, AG3, AG4, AG5, CF1, VS1, VS3a |
| 1 | UO2, DG1, DG3, CV2, VS2, VS5, VS3b, VS4 |
| 2 | AG2, CF2a, CO4, DOC2 |
| 3 | DG2, CF2b, DOC1 |
| 4 | UO5, DG4, CV3 (CV3 also waits on release A) |
| 5 | UO4 |

The critical path is the `init.go` chain plus its documents: CO3 → UO2 → AG2 → DG2 → UO5 → UO4, six
levels. The graph read confirms it: critical path (6 tasks) CO3 -> UO2 -> AG2 -> DG2 -> UO5 -> UO4, with CO3 the bottleneck.

### Shared files

| File | Writers, by level |
| --- | --- |
| `internal/cli/init.go` | CO3 (0), UO2 (1), AG2 (2), DG2 (3), UO5 (4), UO4 (5) |
| `internal/cli/init_stages.go` | VS3b (1), AG2 (2), UO5 (4) |
| `internal/cli/init_compute.go` | AG2 (2), UO5 (4) |
| `internal/cli/init_retire.go` | UO2 (1), UO4 (5) |
| `internal/cli/init_refusal.go` | DG2 (3), UO4 (5) |
| `internal/initflow/loop.go` | CO1 (0), AG2 (2) |
| `internal/cli/root.go` | DG1 (1), CO4 (2) |
| `internal/infra/spec.go` | UO2 (1), CF2a (2) |
| `internal/config/config.go` | CV1 (0), CF2b (3) |
| `internal/config/retired.go` | UO3 (0), CF2b (3) |
| `website/content/cli-reference.md` | DG2 (3), UO5 (4), UO4 (5); AG3 regenerates it only if `--yes`'s removal changes help, at level 0 |


### Contract changes anticipated

None: the plan has no contract.

## Estimate

The graph read prints one line for the plan:

> ready in 420–1699 min, no history yet for 24 tasks (34 tasks; tiers for 10, seeds for 24; from the cached history of 2026-11-22 07:25 UTC)

It is informational: it reserves no budget, and it moves as runs finish and as the graph changes.

## Checks (owner-run)

- **Check 950.** In a coding-agent session on project `fugaro`, the agent runs `fugaro update` to the
  end. Every stage applies, and nothing irreplaceable is deleted. After AG2 and DG4.
- **Check 951.** After release A, launch a run with a CLI one release ahead of the image. The run's log
  shows the upgrade to exactly that release, and the run completes. This also settles U1 and U3. After
  VS5 and VS2 ship.
- **Check 952.** Leave the project overnight with a stale catalog. By morning every stale entry has
  rebuilt, four at a time. After VS4.
- **Check 953.** On a project with one repository still on its own root, `fugaro update` retires the
  root. The repository's secret containers remain, and the next run starts on the shared job. After UO2.

Checks 954–959 are reserved and unused.

## After-deploy steps

None beyond `fugaro update`. Release A's update rebuilds every image once and waits (D3445). That is the
one step, and it is the `### For operators` line the release that carries VS5 must state: "run
`fugaro update`; it rebuilds every image once. Launches from a teammate already on this release fail
with a one-line pointer until the update finishes."
