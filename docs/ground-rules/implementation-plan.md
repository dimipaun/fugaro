# Ground rules: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped the plan are D3420–D3459, all given or stated on 2026-11-26.
- **Not the recommendation:**
  - D3441 (the stamp lives in `task.json`);
  - D3445 (one eager rebuild);
  - D3448 (accept the launch window);
  - D3454 (the membership commands revoke grants themselves);
  - D3455 (only `--executable-changes` stays a person's);
  - D3456 (recreate, then re-grant and rebuild);
  - D3457 (accept the skipped-release gap).
- **Amended after review and confirmed by the owner:** D3424, D3425 (twice), D3430 and D3448. The open
  items O1–O10 are all answered; O4–O10 became D3453–D3459.
- **Resolutions**, stated without a question: D3424, D3426, D3428, D3429, D3434, D3435, D3439, D3446,
  D3449, D3450, D3451 and D3452.

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
| An agent's `update` in a managed project runs every stage, refuses only irreplaceable deletes, and still refuses the keep-list flags | AG2a, AG2b, DG4 |
| A recreatable delete never takes a run in flight with it (D3450) | DG1a, DG4 |
| A task carrying fields an old runner does not know still upgrades that runner | VS5 |
| An agent can never pass `--allow-delete`, at any point in the rollout | DG2 before AG2a |
| The runner upgrades only to exactly the stamped release, from a fixed repository, checked by sha256; never a dev build, never down, never twice | VS1, VS5 |
| A failed upgrade ends the run in one line naming the version | VS1, VS5 |
| A foreign or broken baked tree is never used or readable, and the push binding always holds | CF1 |
| A file newer than the reader is refused in one line; an old supported version reads as before | CV1 |
| No human message over 140 characters outside the allowlist | CO5 |
| An installation that last ran `update` on the release before converges | UO2, VS3b, VS4 (each with a previous-version converge test) |

## Slices

1. **The first pieces** (DG0, VS0, UO1, UO3, CV1): inert. The class table, the stamp field the runner
   accepts, the earlier documents brought in line, and the versioned loader.
2. **Output** (CO1–CO5, CO2b, then CO2c after the console release).
3. **Deletes by class** (DG5 first, then DG1a, DG1b, DG1c, DG2, DG3, DG6, DG4).
4. **One update for the project** (UO2, UO7, then DOC1, DOC2, DOC3, UO4a, UO4b; UO6 and UO7b a release
   later).
5. **Agents run update** (AG2a–AG5d).
6. **Checkout** (CF1, CF2a, CF2c, CF2b).
7. **Versions** (CV2, VS1–VS5, REL1, then CV3 after release A).

AG1 is the audit itself, §A of the reconciliation; the agent tasks start at AG2a. Each task's **Depends on**
line names the edges that carry behaviour, a build or an interface; the edges that only order two writers
of one file are in the Edges table and the graph, which are complete. UO5 of the first draft
was dropped with D3424's amendment.

## Tasks

### DG0: the delete classes, per change
- Acceptance:
  - `tf.RemovalClass(rc ResourceChange, people []string) Class` classifies one planned removal by its type
    and, for an IAM member, by its `member`: a grant to a person on the operator or admin list is
    irreplaceable, and a grant to a Fugaro service account is recreatable;
  - registries and Fugaro-made service accounts are recreatable (D3430 as amended);
  - an unknown type is irreplaceable (fail closed);
  - `tf.DataBearingType` and `RefusesRemovalType` are rewritten over the same table rather than kept beside
    it.
- Files: `internal/infra/tf/cover.go`, `internal/infra/tf/cover_classes_test.go` (new),
  `docs/ground-rules/delete-classes.md` (new).
- Interfaces: `tf.RemovalClass`, consumed by DG1a, DG2, DG3, UO2, AG2a and DG4. A task that finds a type
  Fugaro makes missing from the table adds it in its own change.
- Tests: `TestRemovalClassFollowsD3430`, `TestPersonGrantIsIrreplaceable`, `TestServiceAccountGrantIsRecreatable`,
  `TestUnknownTypeRefused`.
- Command: `go test ./internal/infra/tf/ -run 'TestRemovalClassFollowsD3430|TestPersonGrantIsIrreplaceable|TestServiceAccountGrantIsRecreatable|TestUnknownTypeRefused' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestPersonGrantIsIrreplaceable` fails while `google_project_iam_member` is classed by type alone.
- Depends on: none.
- Commit: `ground-rules DG0: the delete classes, per change (D3430)`
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
  - a repository whose workflows no profile serves runs on the default profile, and `update` prints one line
    naming the repository and the profile setting to add (D3458);
  - an installation that last ran `update` on the release before converges.
- Files: `internal/projectpolicy/profiles.go` (admission: the project's default profile admits every
  enrolled repository), `internal/infra/spec.go`, `internal/cli/init.go` (`repoEngine`, `enrolSharedRepo`,
  `retireSharedRoot`), `internal/cli/init_retire.go`, `internal/cli/init_repo_stage.go`,
  `internal/cli/update_retire_root_test.go` (new). The file count is six. A repository whose workflows
  no profile can serve is open item O9.
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
- Files: `internal/config/retired.go`, `internal/config/retired_test.go`, and the two other readers of
  `Workflow.Resources`: `internal/cli/compute.go` (the Batch checks) and `internal/cli/budget_preview.go`.
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
  `plugin/skills/working/reference/status.md`, `plugin/setup_skill_test.go`, `plugin/skills_lint_test.go`.
  It starts from `main` after #4179 (U7a), which already rewrote the HARD RULE's pin clause.
- Tests: the plugin tests and the Docs tests.
- Command: `go test ./plugin/... -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents); `git grep -n 'update --repo' CLAUDE.md .claude plugin` must print nothing.
- Depends on: UO2, AG2a, AG2b (behaviour: the text describes what has shipped).
- Commit: `ground-rules DOC1: CLAUDE.md, rules and skills follow the ground rules (D3447)`
- Reviewer tier: opus (CLAUDE.md, rules files, plugin skills). The file count is over five; the edits are
  one sentence each.

### DOC2: the how-tos, the security pages and their pins
- Acceptance:
  - nothing outside `internal/` names `update --repo`, including `docs/how-to/`, `README.md`, `SECURITY.md`
    and `website/content/security.md`;
  - `docs/managed-agent-converge/handoffs.md` records D3425 (F13);
  - the Docs tests that pin the old sentences change with them.
- Files: `docs/*.md` (the nine top-level documents that name it), `docs/how-to/*.md`, `README.md`,
  `SECURITY.md`, `website/content/security.md`,
  `docs/managed-agent-converge/handoffs.md`, `internal/cli/docs_*_test.go`.
- Tests: the Docs tests and the website check.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: none (documents); `git grep -n 'update --repo' -- docs README.md SECURITY.md website ':!website/content/cli-reference.md'`
  prints only this folder and the historical specs (UO4a regenerates the reference).
- Depends on: UO2, AG2a (behaviour).
- Commit: `ground-rules DOC2: how-tos and security pages for a project-wide update`
- Reviewer tier: opus (`SECURITY.md` falls under the credentials-service rule).


### DOC3: the live-check scripts
- Acceptance: no script under `scripts/live-checks/` runs `fugaro update --repo` (34 today); each runs plain
  `fugaro update`.
- Files: `scripts/live-checks/**`.
- Tests: `bash -n` over every changed script, and `git grep -n 'update --repo' scripts` printing nothing.
- Command: `for f in $(git grep -l 'fugaro update' scripts/live-checks); do bash -n "$f" || exit 1; done && ! git grep -n 'update --repo' scripts`
- Red step: the `git grep` lists today's 34 files.
- Depends on: UO2 (behaviour: plain `update` covers every repository).
- Commit: `ground-rules DOC3: the live-check scripts run plain update`
- Reviewer tier: standard. The owner runs these scripts; none is run by an agent.
### UO4a: `update --repo` and its flags go
- Acceptance: `--repo`, `--no-build`, `--retire` and `--allow-job-delete` are gone from `setup` and
  `update`. `retireNextCommand` is deleted, and the CLI reference is regenerated.
- Files: `internal/cli/init.go`, `internal/cli/init_refusal.go`, `internal/cli/init_repo_target.go`,
  `internal/cli/init_retire.go`, `website/content/cli-reference.md`.
- Tests: `TestUpdateHasNoRepoFlag`, the plugin tests, `go test ./ee/clidoc/`.
- Command: `go test ./internal/cli/ -run 'TestUpdateHasNoRepoFlag|Docs' -count=1 && go test ./ee/clidoc/ ./plugin/... -count=1`
- Red step: `TestUpdateHasNoRepoFlag` fails while the flag is registered.
- Depends on: UO2 (behaviour), DOC1, DOC2 and DOC3 (nothing may name or run the flag when it goes).
- Commit: `ground-rules UO4a: update --repo and its flags go (D3420, D3431)`
- Reviewer tier: standard.

### UO4b: no message names `update --repo`; help says what agents may do
- Acceptance:
  - no non-test Go file names `update --repo` (64 today, F37); each message says `fugaro update` or drops
    the instruction, within 140 characters;
  - no command's help still says it is refused in a coding agent's session unless it is on D3425's keep
    list;
  - the CLI reference is regenerated.

  The lift tasks (AG2b–AG5d) change no help text, so that this one sweep owns it.
- Files: `internal/**/*.go` (a sweep), `website/content/cli-reference.md`, and the console's pins of
  doctor's `fix` value: `console/docs/json-contract.md`, `console/test/unit/manage-code.test.ts`.
- Tests: `TestNoMessageNamesRepoFlag`, `TestHelpMatchesAgentKeepList`, CO5's census, `go test ./ee/clidoc/`.
- Command: `go test ./internal/cli/ -run 'TestNoMessageNamesRepoFlag|TestHelpMatchesAgentKeepList|TestMessagesAreLaconic' -count=1 && go test ./ee/clidoc/ -count=1 && (cd console && npm run test:unit -- --maxWorkers=2 manage-code)`
- Red step: `TestNoMessageNamesRepoFlag` lists today's 64 files.
- Depends on: UO4a, and every earlier writer under `internal/` (the sweep runs alone).
- Commit: `ground-rules UO4b: no message names update --repo; help follows the keep list`
- Reviewer tier: standard.

### UO6: the per-repository Terraform goes
- Acceptance: `deploy/terraform/gcp/roots/repo` and `modules/repo` are deleted, with every Go reference to
  them, once no installation can still hold a per-repository root (D3451).
- Files: `deploy/terraform/gcp/roots/repo/**`, `deploy/terraform/gcp/modules/repo/**`,
  `internal/infra/workdir.go` (the `roots` map), `internal/infra/retire.go` (it reads
  `modules/repo/registry.tf`), `internal/infra/*_test.go`, `deploy/terraform/terraform_test.go`,
  `deploy/terraform/secret_readers_test.go`.
- Tests: `go build ./...`, `TestNoRepoRoot`, `terraform fmt -check -recursive deploy/terraform`.
- Command: `go build ./... && go test ./deploy/terraform/ ./internal/infra/ -run 'TestNoRepoRoot|TestPreventDestroy' -count=1`
- Red step: `TestNoRepoRoot` finds the root.
- Depends on: UO2 (behaviour). **External:** one release after the release carrying UO2, so an
  installation one release behind still retires its roots. An installation that skips that release
  removes its leftover roots by hand: UO6's release notes (`docs/releases/vX.Y.Z.md`) name the release
  to install first (D3457).
- Commit: `ground-rules UO6: the per-repository Terraform goes (D3451)`
- Reviewer tier: opus (Terraform).
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
  every call site and the `--anchor` help in `init.go` (D3446). A launch no longer reads a
  build record's release to admit a key.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/init.go`, and the callers: `run.go`,
  `launch_budget.go`, `config_publish.go`, `image_layer.go`, `imagecheck.go`, `validate.go`,
  `validate_recipes.go`, `doctor_layer.go`; `website/content/cli-reference.md` (the `--anchor` help). The
  file count is fourteen; outside `recipes_skew.go` each change deletes a call.
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
  - the cover (`managed_cover.go`) and `GuardManaged` (`guard.go`) route a removal the same way: one DG0
    calls recreatable passes with no delete-list entry, and an irreplaceable one is refused in at most two
    sentences, naming no flag (D3427);
  - a recreatable removal of a job, or of a service account a job runs as, while an execution of that job
    is in flight, is left for a later update with one line, as whole-root retirement already is (F38,
    D3450).
- Files: `internal/infra/tf/managed_cover.go`, `internal/infra/tf/managed_cover_test.go`,
  `internal/infra/tf/guard.go`, `internal/cli/managed_gate.go`.
- Tests: `TestGateAdmitsRegistryRemoval`, `TestGateRefusesSecretRemoval`, `TestGateRefusesPersonGrantRemoval`,
  `TestGateDefersJobDeleteWithRunInFlight`, `TestCoverAndGuardAgree`.
- Command: `go test ./internal/infra/tf/ ./internal/cli/ -run 'TestGate|TestCoverAndGuardAgree' -count=1`
- Red step: `TestGateAdmitsRegistryRemoval` fails while a registry is data-bearing and unlisted.
- Depends on: DG0 (the class table).
- Commit: `ground-rules DG1a: the managed gate judges what is deleted (D3427)`
- Reviewer tier: opus (the write guard).
### DG1b: the delete-list command retires
- Acceptance: `fugaro allow-delete` is gone, `managed apply` and `restore` read no delete list, and the CLI
  reference is regenerated (D3427).
- Files: `internal/cli/allow_delete.go` (deleted), `internal/cli/managed_apply.go`,
  `internal/cli/restore.go`, `internal/cli/root.go`, `internal/cli/report_exempt.go`,
  `website/content/cli-reference.md`.
- Tests: `TestNoAllowDeleteCommand`, and `managed apply`'s and `restore`'s existing tests.
- Command: `go build ./... && go test ./internal/cli/ -run 'TestNoAllowDeleteCommand|ManagedApply|Restore' -count=1 && go test ./ee/clidoc/ -count=1`
- Red step: `TestNoAllowDeleteCommand` finds `allow-delete` registered.
- Depends on: DG1a (behaviour: nothing needs the list once the rule changed).
- Commit: `ground-rules DG1b: the allow-delete command retires (D3427)`
- Reviewer tier: opus (the write guard).

### DG1c: the delete list leaves the libraries
- Acceptance:
  - `internal/infra/managed/deletelist.go` is gone, and no package reads `fugaro/allow-delete.json`;
  - the audit record keeps its `delete_list_generation` field, always written as 0 ("no list consulted"),
    so the field's meaning holds and its format needs no version bump (CLAUDE.md, "a field never changes
    meaning").
- Files: `internal/infra/managed/deletelist.go` (deleted), `internal/managed/inventory.go`,
  `internal/managed/plan.go`, `internal/managed/apply.go`, `internal/managed/audit.go`,
  `internal/infra/identity_allow_delete.go`, `internal/infra/build_identity.go`,
  `internal/infra/tf/identity.go`.
- Tests: `TestAuditDeleteListGenerationZero`, and the packages' existing tests.
- Command: `go build ./... && go test ./internal/managed/ ./internal/infra/... -count=1`
- Red step: `TestAuditDeleteListGenerationZero` fails while the audit copies a list generation.
- Depends on: DG1b (build: the library leaves once no command reads it).
- Commit: `ground-rules DG1c: the delete list leaves the libraries (D3427)`
- Reviewer tier: opus (the write guard). The file count is eight; each change deletes a read.

### DG5: membership commands revoke their own grants
- Acceptance:
  - `user rm`, `user offboard`, `workspace user rm` and a role change (`user rm` from one list plus
    `user add` to the other) remove the person's IAM grants themselves, through the IAM API, before they
    return, and edit the lists as today;
  - the next `update` plans no change for that grant, because the configuration and the project agree;
  - these commands stay refused to agents (D3425) (D3454).
- Files: `internal/cli/user.go`, `internal/cli/offboard.go`, `internal/cli/workspace_offboard.go`,
  `internal/cli/user_revoke.go` (new), `internal/cli/user_revoke_test.go` (new).
- Tests: `TestUserRmRevokesAtOnce`, `TestOffboardRevokesAtOnce`, `TestUpdatePlansNoGrantChangeAfterRm`.
- Command: `go test ./internal/cli/ -run 'TestUserRmRevokesAtOnce|TestOffboardRevokesAtOnce|TestUpdatePlansNoGrantChangeAfterRm' -count=1`
- Red step: `TestUserRmRevokesAtOnce` finds the grant still bound after `user rm`.
- Depends on: none. DG1a and DG2 wait on it, so `update` never refuses a revocation nobody else makes.
- Commit: `ground-rules DG5: membership commands revoke grants themselves (D3454)`
- Reviewer tier: opus (IAM).

### DG6: a recreation re-grants and rebuilds
- Acceptance:
  - when an `update` recreates the workspace service account, it re-runs the converging grants
    (`SAAuthority.AddAdmin` for each workspace admin, `Link` for each member project);
  - when it recreates a registry, it rebuilds that registry's catalog entries eagerly, in the same update
    (D3456).
- Files: `internal/cli/update_workspace.go`, `internal/cli/init_images_catalog.go`,
  `internal/cli/update_recreate_test.go` (new).
- Tests: `TestRecreatedWorkspaceAccountRegranted`, `TestRecreatedRegistryRebuiltEagerly`.
- Command: `go test ./internal/cli/ -run 'TestRecreatedWorkspaceAccountRegranted|TestRecreatedRegistryRebuiltEagerly' -count=1`
- Red step: `TestRecreatedWorkspaceAccountRegranted` finds the admin's token-creator grant missing.
- Depends on: DG1a (behaviour: the gate admits the recreation); VS3b (behaviour: the eager-rebuild path).
- Commit: `ground-rules DG6: a recreated account is re-granted and a recreated registry rebuilt (D3456)`
- Reviewer tier: opus (IAM).
### DG2: the guard judges what is deleted; `--allow-delete` goes
- Acceptance:
  - `tf.Guard` refuses only irreplaceable deletes or replaces, and `--allow-delete` is gone, with every
    use of `o.allowDelete` (`gateDeleteLeft` no longer offers the flag);
  - the refusal says in at most two sentences which resource and that `update` never deletes it (D3431);
  - on every path, the owner's included, a plan that deletes a job, or a service account a job runs as,
    while that job has an execution in flight, is not applied: that root's apply is skipped with one line
    and left for a later update, and the other roots apply (D3450).
- Files: `internal/infra/tf/guard.go`, `internal/infra/tf/guard_test.go`, `internal/cli/init.go`,
  `internal/cli/init_refusal.go`, `internal/cli/init_compute.go`, `internal/cli/init_stages.go`,
  `internal/cli/init_firebase.go`, `internal/cli/init_retire.go`, `internal/cli/update_managed.go`,
  `website/content/cli-reference.md`. The file count is ten; outside `guard.go` each change drops one
  field read.
- Tests: `TestGuardPassesRecreatable`, `TestGuardRefusesIrreplaceable`, `TestGuardSkipsRootWithJobInFlight`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/infra/tf/ -run TestGuard -count=1 && go test ./internal/cli/ -run 'TestGuardSkipsRootWithJobInFlight|TestUpdateLeavesARunInFlightRunning' -count=1 && go test ./ee/clidoc/ -count=1`
- Red step: `TestGuardPassesRecreatable` fails on today's refusal.
- Depends on: DG0 (the class table); UO2 and CO3 (one file: `init.go`); DG1a (one file: `guard.go`).
- Commit: `ground-rules DG2: the guard judges what is deleted; --allow-delete goes (D3431)`
- Reviewer tier: opus (the write guard).

### DG3: `prevent_destroy` follows the classes
- Acceptance:
  - the registry repositories lose `prevent_destroy` (`modules/repo/registry.tf`, and the cache registries
    of `modules/installation/registry.tf`), so a recreatable delete the gate admits can apply (D3430);
  - `TestPreventDestroy` (`deploy/terraform/terraform_test.go`) is rewritten over DG0's table: every
    irreplaceable type keeps `prevent_destroy`, and no recreatable type needs it;
  - a converge test shows `update` deletes no current member's secret (D3432, D3434).
- Files: `deploy/terraform/terraform_test.go`, `deploy/terraform/gcp/modules/repo/registry.tf`,
  `deploy/terraform/gcp/modules/installation/registry.tf`, `internal/cli/update_member_secrets_test.go` (new).
- Tests: `TestPreventDestroy`, `TestUpdateKeepsMemberSecrets`, plus `terraform fmt -check -recursive deploy/terraform`.
- Command: `go test ./deploy/terraform/ -run TestPreventDestroy -count=1 && go test ./internal/cli/ -run TestUpdateKeepsMemberSecrets -count=1 && terraform fmt -check -recursive deploy/terraform`
- Red step: `TestPreventDestroy`, rewritten over DG0's table, fails while the registry still carries
  `prevent_destroy`.
- Depends on: DG0 (the class table).
- Commit: `ground-rules DG3: prevent_destroy follows the delete classes`
- Reviewer tier: opus (Terraform).
### DG4: deletes by class, end to end
- Acceptance: on the fake cloud, an agent's `update` in a managed project does all of this in one run,
  with a run in flight:
  - it retires an orphaned repository root (jobs deleted, container kept);
  - it applies a plan that replaces an idle job and destroys a registry;
  - it defers the delete of the job the run in flight executes on, and that run finishes;
  - it refuses a plan that destroys a secret or a person's grant, in at most two sentences;
  - it refuses `--allow-delete` and `--base-image`.
- Files: `internal/cli/update_ground_rules_test.go` (new).
- Tests: `TestGroundRulesUpdateEndToEnd`.
- Command: `go test ./internal/cli/ -run TestGroundRulesUpdateEndToEnd -count=1`
- Red step: the test fails before DG1c, DG2, DG3, UO2, AG2a and AG2b are in.
- Depends on: DG1c, DG2, DG3, UO2, AG2a, AG2b (behaviour: the end-to-end test).
- Commit: `ground-rules DG4: update by class, end to end`
- Reviewer tier: opus.
### CO1: plain step names
- Acceptance: every converge stage prints a plain-words name ("Applying infrastructure changes"), and the
  stage ID stays in the log and in `--json`/`--events` (D3435).
- Files: `internal/initflow/stage.go`, `internal/initflow/loop.go`, `internal/cli/testdata/init_commands.golden`
  (and any golden its test run regenerates).
- Tests: `TestStageDisplayNames`, the update and setup goldens.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestStageDisplayNames|Golden' -count=1`
- Red step: `TestStageDisplayNames` fails while `openStep` prints the ID.
- Depends on: none.
- Commit: `ground-rules CO1: steps print plain names (D3435)`
- Reviewer tier: standard.

### CO2: liveness
- Acceptance:
  - the TTY spinner shows the elapsed time;
  - without a TTY, a step still running after a minute prints one "still …" line a minute (D3436).

  The `--events` heartbeat is CO2c's, after the console accepts it (D3453).
- Files: `internal/report/spinner.go`, `internal/report/report.go`, `internal/report/spinner_test.go`.
- Tests: `TestSpinnerShowsElapsed`, `TestHeartbeatWithoutTTY`.
- Command: `go test ./internal/report/ -run 'Elapsed|Heartbeat' -count=1`
- Red step: `TestHeartbeatWithoutTTY` fails, because nothing prints for a minute (a fake clock).
- Depends on: none.
- Commit: `ground-rules CO2: long steps show they are alive (D3436)`
- Reviewer tier: standard.

### CO2b: the console accepts a heartbeat
- Acceptance: the console's setup-feed reader (`setupFeedAt`) accepts an event of type `heartbeat` and
  ignores it, where today it refuses an unknown type (D3453).
- Files: `console/src/shared/new-ops.ts`, `console/test/unit/new-ops.test.ts`.
- Tests: `heartbeat events are accepted and ignored` in `new-ops.test.ts`.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 new-ops`
- Red step: the test fails on `refuse('unknown-value', …)`.
- Depends on: none. It ships in a console release before CO2c's CLI release.
- Commit: `ground-rules CO2b: the console accepts a heartbeat event (D3453)`
- Reviewer tier: standard.

### CO2c: the events feed carries a heartbeat
- Acceptance: under `--json --events`, a step still running after a minute emits one `heartbeat` event a
  minute, and the schema and golden name it (D3436, D3453).
- Files: `internal/cli/setup_events.go`, `schemas/setup-events.schema.json`,
  `internal/cli/testdata/setup_events.golden.ndjson`.
- Tests: `TestEventsHeartbeat`.
- Command: `go test ./internal/cli/ -run TestEventsHeartbeat -count=1`
- Red step: the test finds no `heartbeat` event in a minute (a fake clock).
- Depends on: CO2 (the ticker), CO2b (behaviour: consoles accept the event first). **External:** the
  console release that carries CO2b has shipped.
- Commit: `ground-rules CO2c: the events feed carries a heartbeat (D3453)`
- Reviewer tier: standard.
### CO3: no raw output on screen
- Acceptance: no path prints the Terraform or docker stream on the terminal (D3438):
  - `--plan-only` prints the plain plan and the log path;
  - `--verbose` adds full step reasons;
  - `--forget`, `--config-only` and `--print-vars` go through the reporter;
  - `image build --local` and the mirror lines go to the log.
- Files: `internal/cli/init.go` (`progress`, `planBlock`), `internal/cli/image.go`,
  `internal/cli/init_images.go`, `internal/cli/raw_output_test.go` (new).
- Tests: `TestNoRawTerraformOnScreen`, `TestLocalBuildToLog`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestNoRawTerraformOnScreen|TestLocalBuildToLog|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestNoRawTerraformOnScreen` under `--verbose` sees a `module.` address on stderr.
- Depends on: none.
- Commit: `ground-rules CO3: raw Terraform and build output stay in the log (D3438)`
- Reviewer tier: standard.

### CO4: structured detail for agents
- Acceptance: in an agent session, a command that has `--json` writes its result document to stdout and
  the human lines to stderr, as if `--json` were passed. Outside one, nothing changes (D3437).
- Files: `internal/cli/report_flags.go`, `internal/cli/root.go`, `internal/cli/agent_json_test.go` (new).
- Tests: `TestAgentSessionGetsJSON`, `TestTerminalUnchanged`, `TestIDETerminalUnchanged` (an IDE marker with a
  terminal on stdin is a person, not an agent).
- Command: `go test ./internal/cli/ -run 'TestAgentSessionGetsJSON|TestTerminalUnchanged|TestIDETerminalUnchanged' -count=1`
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

### AG2a: an agent's update reaches every stage
- Acceptance:
  - in a managed project, the loop (`agentStage`) routes every stage of an agent's `update` through the
    gate, Compute and retirement included;
  - `--onboard-repo`, `--publish-config` and `--coordinator` are admitted;
  - `shapesTheInstallation` keeps refusing every flag it covers except `--registry-cleanup` and
    `--scheduler-region`. The refused set is the exact table in `delete-classes.md`'s neighbour,
    `docs/ground-rules/agent-flags.md`, written by this task: the budget, billing, operator, admin,
    project, bucket, `--base`, image-source, `--github-app-id` and `--allow-delete` flags (D3425 as
    amended; O3).
- Files: `internal/initflow/loop.go` (`agentStage`), `internal/cli/update_managed.go`,
  `internal/cli/init.go`, `internal/cli/init_stages.go`, `docs/ground-rules/agent-flags.md` (new).
- Interfaces: consumes DG0 through the gate's unchanged `admit`.
- Tests: `TestAgentUpdateReachesEveryStage`, `TestAgentRefusedKeepListFlags`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestAgentUpdateReachesEveryStage|TestAgentRefusedKeepListFlags|TestUpdateLeavesARunInFlightRunning|Docs' -count=1`
- Red step: `TestAgentUpdateReachesEveryStage` fails on the loop's `AgentRefusal` for Compute.
- Depends on: DG0 (the class table); DG1a (behaviour: the gate admits recreatable deletes first); DG2
  (behaviour: `--allow-delete` is gone before agents run every stage).
- Commit: `ground-rules AG2a: an agent's update reaches every stage (D3425)`
- Reviewer tier: opus (the agent's reach and the gate).

### AG2b: each stage's own refusal lifts
- Acceptance:
  - the secrets stage skips with one line naming the secret and the command that sets it;
  - the Compute, orchestrator and checkout-upgrade stages drop their own agent refusals (D3425).
- Files: `internal/cli/initsecrets.go`, `internal/cli/init_compute.go`,
  `internal/cli/init_orchestrator_stage.go`, `internal/cli/init_checkout_upgrade.go`.
- Tests: `TestAgentSecretsStageSkipsWithLine`, `TestAgentStagesHaveNoOwnRefusal`.
- Command: `go test ./internal/cli/ -run 'TestAgentSecretsStageSkipsWithLine|TestAgentStagesHaveNoOwnRefusal' -count=1`
- Red step: `TestAgentStagesHaveNoOwnRefusal` fails on the Compute stage's own agent refusal (`init_compute.go`).
- Depends on: none.
- Commit: `ground-rules AG2b: update's stages run in an agent's session (D3425)`
- Reviewer tier: opus (agent reach).
### AG3: settings, rigor and accounts picks lift
- Acceptance:
  - an agent may run `settings set`/`unset` for every key except those that raise a budget or widen
    `allow_data_to`, and without `--executable-changes` (D3455), which are still refused;
  - an agent may lower rigor (`rigor set`, `rigor floor`, `rigor security rm`) and run
    `accounts switch`, `alias` and `pool`;
  - `--yes` is gone from `accounts alias` and `accounts switch` (D3425 as amended, D3429).
- Files: `internal/cli/settings_managed.go`, `internal/cli/settings_write.go`, `internal/cli/rigor.go`,
  `internal/cli/rigor_floor.go`,
  `internal/cli/accounts_switch.go`, `internal/cli/accounts_alias.go`, `internal/cli/accounts_pool.go`,
  `website/content/cli-reference.md`.
- Tests: `TestAgentMaySet`, `TestAgentBudgetRaiseRefused`, `TestAccountsHaveNoYes`.
- Command: `go test ./internal/cli/ -run 'TestAgentMaySet|TestAgentBudgetRaiseRefused|TestAccountsHaveNoYes' -count=1 && go test ./ee/clidoc/ ./plugin/... -count=1`
- Red step: `TestAgentMaySet` fails on the rigor lowering's refusal.
- Depends on: none.
- Commit: `ground-rules AG3: settings, rigor and account picks are an agent's too (D3425)`
- Reviewer tier: opus (agent reach). The file count is eight; most are a one-line refusal each.

### AG4a: project commands lift
- Acceptance: an agent may run `project repos add`/`rm`/`sync` and `project policy publish`. `--delete`
  stays a person's (D3425).
- Files: `internal/cli/project_enroll.go`, `internal/cli/project_enroll_converge.go`,
  `internal/cli/project_repos_rm.go`, `internal/cli/project_cmd.go`, `internal/cli/project_policy_managed.go`.
- Tests: `TestAgentMayEnrol`, `TestAgentPolicyDeleteRefused`.
- Command: `go test ./internal/cli/ -run 'TestAgentMayEnrol|TestAgentPolicyDeleteRefused' -count=1`
- Red step: `TestAgentMayEnrol` fails on `project repos add`.
- Depends on: none.
- Commit: `ground-rules AG4a: enrolment and policy publishing are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

### AG4b: image and publish commands lift
- Acceptance: an agent may run `image add`, `image map`, `image refresh` (image-source flags included) and
  `config publish`. `--executable-changes` stays a person's on `config publish` and `workflow`, because it
  decides what code every repository runs (D3425 as amended, D3455).
- Files: `internal/cli/image_catalog.go`, `internal/cli/image_map_managed.go`, `internal/cli/image_refresh.go`,
  `internal/cli/config_publish.go`, `internal/cli/workflow.go`.
- Tests: `TestAgentMayEditImages`, `TestAgentExecutableChangesRefused`.
- Command: `go test ./internal/cli/ -run 'TestAgentMayEditImages|TestAgentExecutableChangesRefused' -count=1`
- Red step: `TestAgentMayEditImages` fails on `image refresh`.
- Depends on: none.
- Commit: `ground-rules AG4b: image edits and config publishing are an agent's too (D3425)`
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

### AG5c: applies lift
- Acceptance: an agent may run `orchestrator patch`/`install`, `apply`, `managed apply` (through the
  reclassified gate) and the cloud half of `upgrade` (D3425). `orchestrator install` writes a login unit
  on the person's own machine, which the session already runs code on; the reconciliation's §A records it.
- Files: `internal/cli/orchestrator_patch.go`, `internal/cli/orchestrator_install.go`,
  `internal/cli/apply.go`, `internal/cli/managed_apply.go`, `internal/cli/upgrade_cloud.go`.
- Tests: `TestAgentMayApply`.
- Command: `go test ./internal/cli/ -run TestAgentMayApply -count=1`
- Red step: the test fails on the `managed apply` refusal.
- Depends on: DG1a (behaviour: `managed apply` goes through the reclassified gate); DG1b (one file:
  `managed_apply.go`).
- Commit: `ground-rules AG5c: applies are an agent's too (D3425)`
- Reviewer tier: opus (agent reach).

### AG5d: builds lift
- Acceptance: an agent may run `image build` and `image check`'s job submission (D3425).
- Files: `internal/cli/image_build_catalog.go`, `internal/cli/image_build_managed.go`, `internal/cli/imagecheck.go`.
- Tests: `TestAgentMayBuild`.
- Command: `go test ./internal/cli/ -run TestAgentMayBuild -count=1`
- Red step: the test fails on the `image build` refusal.
- Depends on: VS3a (one file: `image_build_catalog.go`).
- Commit: `ground-rules AG5d: builds are an agent's too (D3425)`
- Reviewer tier: standard.
### CF1: the runner fetches a usable bake or clones
- Acceptance:
  - with a usable baked copy of the run's repository, the runner fetches and checks out;
  - with none, or with a foreign, unreadable or broken one (fetch or reset fails), it logs one line,
    removes the unusable tree before the agent starts, and clones into a fresh directory;
  - the push binding (`checkBakedCheckoutOrigin`'s checks on `pushurl`, `insteadOf` and URL-named remotes)
    runs on whichever checkout the run uses, and a foreign tree is never readable by the agent (F39);
  - `FUGARO_CHECKOUT` is ignored;
  - the clone path (blobless plus `mise install`) is chosen by the absence of a usable bake;
  - a `shared` image (no source) clones (D3440, F35).
- Files: `internal/runner/runner.go` (`openCheckout`, `checkBakedCheckoutOrigin`),
  `internal/runner/projectjob.go`, `internal/runner/provision.go` (`cloneJob`),
  `internal/runner/checkout_fallback_test.go` (new), `docs/checkout.md`.
- Tests: `TestForeignBakeRemovedAndCloned`, `TestBrokenBakeClonesFresh`, `TestUsableBakeFetches`,
  `TestNoBakeClones`, `TestPushBindingOnFreshClone`.
- Command: `go test ./internal/runner/ -run 'TestForeignBakeRemovedAndCloned|TestBrokenBakeClonesFresh|TestUsableBakeFetches|TestNoBakeClones|TestPushBindingOnFreshClone' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestForeignBakeRemovedAndCloned` fails on today's origin refusal.
- Depends on: none. It must ship no later than release A, because CF2a relies on every image carrying it
  after A's eager rebuild.
- Commit: `ground-rules CF1: a run fetches a usable bake or clones fresh (D3440)`
- Reviewer tier: opus (the agent's working tree and the push binding).
### CF2a: deploy stops marking the checkout mode
- Acceptance: job specs no longer carry `FUGARO_CHECKOUT`, `jobCheckoutEnv` is gone, and D1191's
  `unbuiltOnBaked` refusal is gone.
- Files: `internal/infra/spec.go`, `internal/infra/checkjob.go`, `internal/infra/spec_test.go`.
- Tests: `TestJobSpecHasNoCheckoutEnv`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/infra/ -run TestJobSpecHasNoCheckoutEnv -count=1 && go test ./internal/cli/ -run TestUpdateLeavesARunInFlightRunning -count=1`
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
- Depends on: CF2a (behaviour: deploy no longer marks the mode).
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
  - fetches the `fugaro_` (ee) archive, never `fugaro-core_`, for exactly that tag, linux and
    `runtime.GOARCH`, from the repository `updatecheck` already names (reused, not a second constant;
    nothing in the URL comes from the task beyond the validated semver);
  - fetches `checksums.txt` and checks the sha256, with the download size capped;
  - extracts only the regular file `fugaro`, refusing links and any path that leaves the directory;
  - installs into a fresh directory and makes the directory and the file read-only (F33);
  - retries briefly, then fails with one line naming the version;
  - returns nothing for a `dev` stamp or binary, or for a release not above its own (D3442, D3443,
    D3449).
- Files: `internal/selfupgrade/fetch.go` (new), `internal/selfupgrade/verify.go` (new),
  `internal/selfupgrade/install.go` (new), `internal/selfupgrade/selfupgrade_test.go` (new).
- Interfaces: the unexported `upgrade(ctx, want, have string) (exe string, err error)`. VS5 exports it.
- Tests: `TestUpgradeChecksSHA256`, `TestUpgradeRefusesMismatch`, `TestUpgradeFixedRepository`,
  `TestUpgradeEEArchive`, `TestExtractRefusesLinksAndTraversal`, `TestDownloadCapped`,
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
  The repeat check (`repeatTaskBytes`) ignores `cli_version`, so a retried run ID launched from another
  release is not refused as a different task.
- Files: `internal/cli/run.go` (`createTask`), `internal/cli/run_stamp_test.go` (new).
- Tests: `TestEveryLaunchStampsCLIVersion`, `TestDevBuildDoesNotStamp`, `TestRepeatIgnoresStamp`.
- Command: `go test ./internal/cli/ -run 'TestEveryLaunchStampsCLIVersion|TestDevBuildDoesNotStamp|TestRepeatIgnoresStamp' -count=1`
- Red step: the test finds an empty `cli_version`.
- Depends on: VS0 (the field).
- Commit: `ground-rules VS2: every launch stamps its CLI version (D3441)`
- Reviewer tier: standard.

### VS3a: the base-image epoch
- Acceptance:
  - `imagecheck.BaseEpoch` is a constant;
  - a test hashes `images/base/`, `images/derived/` and `images/common/` (the derived template and
    `finalize-checkout.sh` are not in a catalog entry's key, F43), minus the `FUGARO_VERSION` lines, and
    fails when they change without the epoch moving;
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
  - builds run four at a time (D3444, D3446);
  - a build that fails during an eager rebuild fails the update's images step in one line naming the
    entry, so the person or agent reruns `update`; the entry is not left silently on an old runner;
  - an installation that last ran `update` on the release before converges.
- Files: `internal/cli/init_images_catalog.go`, `internal/cli/init_stages.go`,
  `internal/cli/update_catalog_background_test.go` (its `TestUpdateRunsItsOtherStepsWhileACatalogImageBuilds`
  changes with the wait).
- Tests: `TestUpdateWaitsOnlyOnEpoch`, `TestReleaseAloneIsNotStale`, `TestCatalogBuildsFourAtOnce`,
  `TestEagerBuildFailureFailsStep`, `TestPreviousReleaseConverges`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateWaitsOnlyOnEpoch|TestReleaseAloneIsNotStale|TestCatalogBuildsFourAtOnce|TestEagerBuildFailureFailsStep|TestPreviousReleaseConverges|TestUpdateRunsItsOtherStepsWhileACatalogImageBuilds|TestUpdateLeavesARunInFlightRunning' -count=1`
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
  - `fugaro exec` (the cloud path, not `--task-file` on a person's machine) first reads `cli_version` with
    a lenient decode of `task.json` that ignores every other field, so a task carrying fields this runner
    does not know still upgrades it (F41);
  - when `cli_version` is above its own release, it calls `selfupgrade.Ensure` and re-execs the result
    before anything else runs, and the upgraded runner then parses the task strictly;
  - an environment marker stops a second upgrade;
  - before each later stage, the runner checks that the install directory holds exactly the binary with
    its checksum, and ends the run in one line otherwise (F33);
  - an end-to-end test with a fake release server shows a stamped task re-executing the fetched binary,
    and an unfetchable stamp ending the run in one line.
- Files: `internal/cli/exec.go`, `internal/selfupgrade/selfupgrade.go` (new: the exported `Ensure` over
  VS1's `upgrade`, and the install-directory check), `internal/runner/runner.go` (the per-stage hook
  for the check), `internal/cli/exec_selfupgrade_test.go` (new).
- Tests: `TestExecUpgradesToStamp`, `TestExecUpgradesDespiteUnknownField`, `TestExecUpgradesOnce`,
  `TestExecFailsOnUnfetchableStamp`, `TestInstallDirTampered`, `TestLocalExecNeverUpgrades`.
- Command: `go test ./internal/cli/ ./internal/selfupgrade/ -run 'TestExec|TestInstallDirTampered|TestLocalExecNeverUpgrades' -count=1`
- Red step: `TestExecUpgradesDespiteUnknownField` fails while `exec` parses strictly first.
- Depends on: VS0 (it reads the field), VS1 (it calls the upgrader).
- Commit: `ground-rules VS5: the runner upgrades to its task's stamp (D3441)`
- Reviewer tier: opus (code fetched and run inside the job).
### REL1: the release's operator lines
- Acceptance: the next release's notes (`docs/releases/vX.Y.Z.md`) carry a `### For operators` section:
  - "Run `fugaro update`. It rebuilds every image once and waits." (VS5, VS3b);
  - "Repositories still on their own jobs move to the shared ones; their secrets are kept." (UO2);
  - "The nightly image check now runs; set `jobs.catalog_check.paused: true` to keep it off." (VS4);
  - "Teammates already on this release cannot launch until the update finishes, and teammates on an
    older CLI cannot read new runs until they `brew upgrade`." (D3448, F34);
  - "`fugaro allow-delete` and `--allow-delete` are gone: `update` deletes what it can recreate and never
    deletes the rest." (DG1b, DG2).
- Files: `docs/releases/v*.md` (the next release's file).
- Tests: the release notes' checks in `scripts/release.sh`'s preflight, and the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents).
- Depends on: UO2, VS3b, VS4, VS5, DG1b (the lines describe them).
- Commit: `ground-rules REL1: the operator lines for the ground-rules release`
- Reviewer tier: standard.

The lines sit in one task rather than in each of four PRs because all four tasks land in one level, and
one file cannot have four writers there. REL1 lands before the release is cut, which is what the
`For operators` rule needs.


### UO7: the credentials service reads workflow secrets from `fugaro.yaml`
- Acceptance:
  - when a run fetches its workflow secrets, the credentials service resolves the workflow's secret names
    from the repository's default-branch `fugaro.yaml`, read with its own read mint (U4), so a secret added
    there needs no `update` (D3459);
  - while the per-repository record `creds/workflowsecrets/<slug>` still exists, it is read as a fallback,
    so a run of the release behind and a repository whose read fails keep working.
- Files: `internal/credsvc/workflowsecrets.go`, `internal/credsvc/workflowsecrets_test.go`,
  `internal/creds/workflowsecrets.go`.
- Tests: `TestWorkflowSecretsFromDefaultBranch`, `TestWorkflowSecretsRecordFallback`,
  `TestWorkflowSecretsReadMint`.
- Command: `go test ./internal/credsvc/ ./internal/creds/ -run 'WorkflowSecrets' -count=1`
- Red step: `TestWorkflowSecretsFromDefaultBranch` fails while only the record is read.
- Depends on: none.
- Commit: `ground-rules UO7: workflow secrets come from fugaro.yaml at fetch (D3459)`
- Reviewer tier: opus (the credentials service; `.claude/rules/credentials-service.md`).

### UO7b: update stops writing the workflow-secrets record
- Acceptance: `update` no longer writes `creds/workflowsecrets/<slug>`. That lets the next release delete
  the record and UO7's fallback, which is a follow-up, not in this plan (D3459).
- Files: `internal/cli/update_workflowsecrets.go`, `internal/cli/update_workflowsecrets_test.go`.
- Tests: `TestUpdateWritesNoWorkflowSecretsRecord`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateWritesNoWorkflowSecretsRecord|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateWritesNoWorkflowSecretsRecord` finds the record written.
- Depends on: UO7 (behaviour: nothing needs a fresh record). **External:** one release after UO7's.
- Commit: `ground-rules UO7b: update stops writing the workflow-secrets record (D3459)`
- Reviewer tier: opus (the credentials service).
## Parallel execution

### Edges

| Task | Needs | Reason |
| --- | --- | --- |
| VS5 | CF1 | one file: internal/runner/runner.go |
| CO2c | CO2 | behaviour: the heartbeat ticker |
| CO2c | CO2b | behaviour: consoles accept the event first; external: that console release shipped |
| UO2 | CO3 | one file: init.go |
| CV2 | CV1 | SupportedVersions; a contract would be the table itself |
| UO2, DG1a, DG2, DG3, AG2a | DG0 | the class table |
| DG1a | DG5 | behaviour: as above |
| UO7b | UO7 | behaviour: nothing reads the record; external: one release later |
| VS2 | VS0 | the field |
| VS5 | VS0 | it reads the field |
| VS5 | VS1 | it calls the upgrader |
| VS3b | VS3a | the recorded epoch; a contract would be the constant itself |
| VS4 | VS3a | staleness by epoch |
| AG5d | VS3a | one file: image_build_catalog.go |
| DG2 | CO3, UO2 | one file: init.go |
| DG6 | DG1a | behaviour: the gate admits the recreation first |
| DG2 | DG1a | one file: internal/infra/tf/guard.go |
| DG2 | DG5 | behaviour: membership commands revoke before update refuses a person's grant removal |
| DOC3 | UO2 | behaviour: plain update covers every repository |
| DG6 | VS3b | behaviour: the eager-rebuild path |
| DG2 | VS3b | one file: init_stages.go |
| AG2a | CO1 | one file: internal/initflow/loop.go |
| DG1b | CO4 | one file: root.go |
| DG1b | DG1a | behaviour: nothing needs the list once the rule changed |
| AG2a | DG1a | behaviour: the gate admits recreatable deletes first |
| AG2a | DG2 | behaviour: --allow-delete is gone before agents run every stage |
| DG1b | DG2 | one file: website/content/cli-reference.md |
| AG2b | DG2 | one file: init_compute.go |
| DOC1 | AG2b | behaviour |
| DOC2 | CF1 | one file: docs/checkout.md |
| AG5c | DG1a | behaviour: managed apply goes through the reclassified gate |
| DG1c | DG1b | build: the library leaves once no command reads it |
| AG5c | DG1b | one file: managed_apply.go |
| AG3 | DG2, DG1b | one file: website/content/cli-reference.md |
| DOC1 | UO2, AG2a | behaviour: the text describes shipped behaviour |
| DOC2 | UO2, AG2a | behaviour |
| REL1 | UO2, VS3b, VS4, VS5, DG1b | the operator line describes it |
| UO4a, CV3 | DG1b, AG3 | one file: website/content/cli-reference.md |
| DG4 | DG1c, DG2, UO2, AG2a, AG2b | behaviour: the end-to-end test |
| DG4 | DG3 | behaviour: the registry can be destroyed |
| UO4a | DOC1 | the plugin tests: no skill may name the flag when it goes |
| UO4a | DOC2 | the Docs tests |
| UO4a | DOC3 | the live-check scripts must not run a removed flag |
| UO4a | UO2 | behaviour: plain update covers every repository first |
| UO4b | AG3, AG4a, AG4b, AG5a, AG5b, AG5c, AG5d, AG2b | the help sweep follows every lift |
| UO4b, CF2b | UO3 | one file: internal/config/retired.go |
| UO4b | UO4a | the sweep follows the flag's removal |
| UO4b, CF2c | VS2 | one file: run.go |
| UO4b, CF2b | VS4 | one file: internal/config/layer.go |
| CF2a | CF1 | behaviour: the runner decides without the mark |
| CF2a | UO2 | one file: internal/infra/spec.go |
| CF2a, UO6 | UO4b | one file: internal/**/*.go |
| CF2a | VS3b | behaviour: release A's eager rebuild puts CF1's runner in every image; external: release A shipped |
| CF2a | VS5 | behaviour: as above |
| CF2c | AG2b | one file: init_checkout_upgrade.go |
| CF2c | AG4b | one file: image_refresh.go |
| CF2c | AG5d | one file: imagecheck.go |
| CF2c | CF2a | behaviour: deploy no longer marks the mode |
| UO6 | CF2a | one file: internal/infra/spec_test.go |
| CF2c | CO3, DG2, AG2a, UO4a | one file: init.go |
| UO6 | DG3 | one file: deploy/terraform/terraform_test.go |
| UO6 | REL1 | one file: docs/releases/v*.md |
| UO6 | UO2 | behaviour: no installation applies a per-repository root after UO2; external: one release later |
| CV3 | CF2c | behaviour: the clone value is gone |
| CF2b | CF2c | build: the field leaves once nothing reads it |
| CF2b | CV1 | one file: internal/config/config.go |
| CV3 | VS2 | behaviour: every launch is stamped |
| CV3 | VS5 | behaviour: runners upgrade themselves |
| UO4b | every earlier writer under `internal/` | one file each: the sweep runs alone |

### The dependencies broken and kept

The contract-first pass ran and broke no edge.

- **Tried and undone: VS2 on VS1.** The launcher needs only the stamp field, so a contract could have let
  it run beside the upgrader. The graph read reported no gain, because the critical path is the `init.go`
  chain, so the break was undone. Instead, VS0 (the field) is its own small first task, and VS1 is
  self-contained.
- **DG0 is not a contract.** UO2, AG2a, DG1a, DG2 and DG3 all wait on DG0, the class table. It is small
  and first, and those five then run as their other edges allow. No provider builds beside it, so it is an
  ordinary task.
- **Kept.**
  - CV1→CV2, VS3a→VS3b and VS3a→VS4: a contract would be the shared constant or table itself, which is
    nearly the whole of the first task.
  - CF1→CF2a→CF2c→CF2b: behaviour and the build.
  - Every "one file" edge orders two writers of one file, so that launching by edges never runs them at
    once. `init.go` has seven writers: CO3, UO2, DG2, AG2a, UO4a, CF2c and CV3, in that order. UO4a comes
    before CF2c so that removing `--repo` does not wait on release A.

### Levels

| Level | Tasks |
| --- | --- |
| 0 | DG0, VS0, UO1, CO3, UO3, DG5, VS3a, CO1, CF1, CO4, AG4a, AG4b, AG5a, AG5b, UO7, CV1, CO2, CO5, VS1, CO2b |
| 1 | UO2, DG1a, VS3b, AG5d, DG3, VS2, VS5, VS4, CV2, CO2c, UO7b |
| 2 | DG2, DOC3, DG6 |
| 3 | AG2a, AG2b, DG1b |
| 4 | DOC1, DOC2, AG3, AG5c, DG1c, REL1 |
| 5 | UO4a, DG4 |
| 6 | UO4b |
| 7 | CF2a |
| 8 | UO6, CF2c |
| 9 | CV3, CF2b |

The critical path is CO3 → UO2 → DG2 → AG2a → DOC1 → UO4a → UO4b → CF2a → CF2c → CF2b, ten tasks with
CO3 the bottleneck, as the graph read reports. **Release A** is the first release that carries VS0–VS5, VS3b and
CF1; its `update` runs the eager rebuild. CF2a and CV3 launch only after release A ships; UO6 waits a
release after UO2's, UO7b a release after UO7's, and CO2c a console release carrying CO2b. The graph cannot hold an external wait, so each task and this paragraph state it.

### Shared files

Every pair of writers of one file is ordered by an edge, not only placed in different levels.

| File | Writers, in order |
| --- | --- |
| `deploy/terraform/terraform_test.go` | DG3 (1), UO6 (8) |
| `docs/releases/v*.md` | REL1 (4), UO6 (8) |
| `internal/cli/config_publish.go` | AG4b (0), CV3 (9) |
| `internal/cli/image.go` | CO3 (0), CF2c (8) |
| `internal/cli/image_build_catalog.go` | VS3a (0), AG5d (1) |
| `internal/cli/image_refresh.go` | AG4b (0), CF2c (8) |
| `internal/cli/imagecheck.go` | AG5d (1), CF2c (8), CV3 (9) |
| `internal/cli/init.go` | CO3 (0), UO2 (1), DG2 (2), AG2a (3), UO4a (5), CF2c (8), CV3 (9) |
| `internal/cli/init_checkout_upgrade.go` | AG2b (3), CF2c (8) |
| `internal/cli/init_compute.go` | DG2 (2), AG2b (3) |
| `internal/cli/init_images_catalog.go` | VS3b (1), DG6 (2) |
| `internal/cli/init_refusal.go` | DG2 (2), UO4a (5) |
| `internal/cli/init_retire.go` | UO2 (1), DG2 (2), UO4a (5) |
| `internal/cli/init_stages.go` | VS3b (1), DG2 (2), AG2a (3) |
| `internal/cli/managed_apply.go` | DG1b (3), AG5c (4) |
| `internal/cli/root.go` | CO4 (0), DG1b (3) |
| `internal/cli/run.go` | VS2 (1), CF2c (8), CV3 (9) |
| `internal/cli/update_managed.go` | DG2 (2), AG2a (3) |
| `internal/cli/validate.go` | CF2c (8), CV3 (9) |
| `internal/config/config.go` | CV1 (0), CF2b (9) |
| `internal/config/layer.go` | VS4 (1), CF2b (9) |
| `internal/config/retired.go` | UO3 (0), CF2b (9) |
| `internal/config/validate.go` | CV1 (0), CF2b (9) |
| `internal/infra/spec.go` | UO2 (1), CF2a (7) |
| `internal/infra/tf/guard.go` | DG1a (1), DG2 (2) |
| `internal/initflow/loop.go` | CO1 (0), AG2a (3) |
| `internal/runner/runner.go` | CF1 (0), VS5 (1) |
| `website/content/cli-reference.md` | DG2 (2), DG1b (3), AG3 (4), UO4a (5), UO4b (6), CV3 (9) |
| `internal/**/*.go` | UO4b (6), ordered after every earlier writer under `internal/` and before CF2a, CF2c, CF2b and CV3 |

### Contract changes anticipated

None: the plan has no contract.

## Estimate

The graph read prints one line for the plan:

> ready in 746–3038 min, no history yet for 38 tasks (51 tasks; tiers for 13, seeds for 38; from the cached history of 2026-11-22 07:25 UTC)

It is informational: it reserves no budget, and it moves as runs finish and as the graph changes.

## Checks (owner-run)

- **Check 950.** In a coding-agent session on project `fugaro`, the agent runs `fugaro update` to the
  end. Every stage applies, nothing irreplaceable is deleted, and the keep-list flags are refused. After
  AG2a, AG2b and DG4.
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
