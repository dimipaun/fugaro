# Repo-coupling audit: implementation plan

**Status: plan only. The static report is written; nothing in this plan is implemented, and no
task is approved.** Each task needs the owner's explicit go.

This plan finishes the audit that [spec.md](spec.md), as amended by
[reconciliation.md](reconciliation.md), describes. The static inventory is already in
[report.md](report.md) (D3230). What is left is the part a static reading cannot do: the live
Method steps, which the owner runs as read-only scripts (D3231), and two small loose ends. It
builds none of the lift list; each lift item becomes its own spec when the owner picks it (D3236).
The version stamp is lift item L1, not a task here (D3233).

Answers that shaped the plan, all 2026-11-22:

- D3230: report in this PR.
- D3231: owner-run scripts.
- D3232: project build identity, a lift item.
- D3233: the version stamp is a lift item.
- D3234: keep D1900, not the recommendation.
- D3235: the benign candidates are ruled after merge.
- D3236: an ordered list, one spec each.
- T1: L9.

## Conventions

- `CLAUDE.md` and `scripts/live-checks/README.md` apply. Each check script is
  `scripts/live-checks/check-<NNN>-<name>.sh` (the form `check-881-chain-falls-through.sh` and its
  neighbours use; the README's `check<NNN>.sh` is the older form), with a hermetic test beside it that drives it
  against fake `gcloud`, `fugaro`, `git` and `curl` on `PATH`: no network, no home directory, no
  cloud.
- **Always.** A check script reads. Its whole call set is:
  - `gcloud … list|describe|get-iam-policy`, `gcloud storage ls`, `gcloud storage cat`
  - `gcloud auth print-access-token`
  - `curl` GET on the RTDB REST API (with `shallow=true`, except the grants node, below)
  - `curl` POST to Firestore's `documents:listCollectionIds`, which reads (it is a POST only by the
    API's shape), and a Firestore `documents` list GET with a `mask` that reads no field
  - `fugaro update --plan-only --json --events --project <p>` (`--events` is a bool) and
    `fugaro doctor --json`

  Two side effects of `update --plan-only` are accepted and documented in Check 891's section:
  Terraform's plan takes the state lock (it writes, then deletes, a `.tflock` object in the state
  bucket, once for each root the run plans: the installation, Firebase, compute where Batch is
  provisioned, and a checkout's repo root;
  `internal/infra` passes no `-lock=false`), and the root pre-run records remembered checkouts in
  the local config.
- **Tokens and values.** The access token never reaches an argv. It goes into a 0600 curl config
  file in a 0700 scratch directory that the exit trap removes (`check-840.sh` `mint_token` is the
  pattern). An RTDB read prints keys only, never values: `shallow=true` returns primitive leaves
  verbatim. The one exception is the grants node `config/admin/access` in Check 892, whose values
  are the principals and roles the check exists to compare; they hold no secret.
- **No input.** Every call runs with `</dev/null`, and every `gcloud` call also with `--quiet`
  (`fugaro` has no such flag; `--json` confirms nothing). It prints
  PASS or FAIL with what differed from `report.md`. It asks nothing (the no-prompt policy,
  `docs/no-prompt-policy/`).
- **Ask first.** Any write, any launch that costs money, any new dependency.
- **Never.** A secret-shaped literal in a test or fixture. A live call by an agent. A script that
  prints a secret value or a token. The owner alone runs the checks.
- Documents change with the tests that pin them: `go test ./internal/cli/ -run Docs -count=1`.

## Global constraints

| Constraint | Pinned by |
| --- | --- |
| A check never writes: every cloud verb it calls is in the call set. The one documented exception is the state lock that `fugaro update --plan-only` takes inside itself (Conventions), which a fake `fugaro` cannot observe | each script's test lists the fake binaries' received arguments and fails on any verb outside the set (K1–K3) |
| A check prints no secret value, and no token reaches an argv | K1–K3's tests feed a fake secret and a fake token, then grep the output and the fakes' recorded argv for both |
| A check waits for nothing | each test runs the script with stdin closed and a timeout |
| A path, a stage or a role the report does not name is a FAIL, not a silent pass | K1–K3, a case each |

## Tasks

### K1: Check 890, as a script — the data layout of a real project

- Acceptance: `check-890-data-layout.sh <fugaro-project>` reads, keys only:
  - in RTDB, the top-level keys, then for each storage-path row of `report.md` the parent of its
    slug segment (`config/caps/repos`, `config/kill/repos`, `config/policy/repos`,
    `config/policy/voice/repos`, `config/policy/route`, `creds/repoauth`, `creds/repoaccess`,
    `runs`, `spend/<today>/repos`, `agents`, `outcomes/<today>`, `buildleases`,
    `coordinator/repos`, `spend/<today>/runs`), plus one level under every other top-level key
    and `config/`'s children;
  - in GCS, the prefixes two levels under `runs/`, `builds/`, `cache/`, `locks/`, `tokens/` and
    `reports/`;
  - in Firestore, the collection ids and the first page of document ids in `spendDaily` (RC50's
    `<day>_<slug>`), through `listCollectionIds` and a `documents` list GET with `mask` so no field
    is read.

  A listing at a per-repo or per-run level samples at most `--sample N` keys (default 3), so `runs/`
  does not enumerate every run. It marks every path with a slug segment and prints FAIL naming any
  repo-keyed layout outside the expected patterns. The patterns are a data block at the top of the
  script, one per storage-path row (RC40–RC50 and RC52–RC56, each with its RC id). Rows that are not
  storage paths (RC51, RC57–RC59, RC95) are left out.
- Files: `scripts/live-checks/check-890-data-layout.sh` (new), `scripts/live-checks/check-890-data-layout_test.sh` (new).
- Interfaces: Consumes nothing. Produces the output R1 records.
- Tests: the script's own test, with the cases `test_a_known_layout_passes`,
  `test_an_unnamed_repo_keyed_path_fails`, `test_only_reads_are_called`, `test_values_are_never_printed`,
  `test_the_token_is_never_on_argv`, `test_runs_are_sampled`, `test_the_slug_level_is_reached`
  (a `config/caps/repos/<slug>` fixture) and `test_stdin_is_closed`.
- Command: `bash scripts/live-checks/check-890-data-layout_test.sh`
- Red step: `test_a_known_layout_passes` fails before the script exists.
- Depends on: none.
- Commit: `repo-coupling-audit K1: Check 890, as a script`
- Reviewer tier: standard.

### K2: Check 891, as a script — where `update` and a launch read repo context

- Acceptance: `check-891-update-trace.sh <fugaro-project> [checkout…]` does three things:
  1. It runs `fugaro update --plan-only --json --events --project "$1"` once from an empty
     temporary directory and once from each checkout named. `--project` is always passed, so a
     checkout's own `project:` cannot switch the comparison to another project; a checkout whose
     `project:` differs is reported and skipped.
  2. It prints, per stage, its state and detail in each place, and marks every stage whose state
     or detail differs between the empty directory and a checkout.
  3. It reads the newest `launch.json` of each enrolled repository (`gcloud storage cat`) and
     prints its `job`. `runstore.Launch` holds no repository field; the job name says whether the
     launch went to a shared project job or to the repository's own `JobPath(slug, workflow)`,
     which is the launch's repo coupling (RC76). A live launch is not part of the check (see
     "Considered and declined").

  It prints FAIL when a stage that differs is not among the blockers in `report.md` §4.
- Files: `scripts/live-checks/check-891-update-trace.sh` (new), `scripts/live-checks/check-891-update-trace_test.sh` (new).
- Interfaces: Consumes `update`'s `--events` feed (its `stage` lines) as it is. Produces the output R1 records,
  which settles U4.
- Tests: `test_matching_stages_pass`, `test_an_unlisted_differing_stage_fails`,
  `test_plan_only_is_always_passed`, `test_project_is_always_passed`,
  `test_a_checkout_of_another_project_is_skipped`, `test_only_reads_are_called`,
  `test_stdin_is_closed`, `test_values_and_tokens_are_never_printed` (a fake secret in a stage
  detail and in `launch.json`), `test_the_token_is_never_on_argv`.
- Command: `bash scripts/live-checks/check-891-update-trace_test.sh`
- Red step: `test_plan_only_is_always_passed` fails before the script exists.
- Depends on: none.
- Commit: `repo-coupling-audit K2: Check 891, as a script`
- Reviewer tier: standard.

### K3: Check 892, as a script — the IAM a member and an admin hold

- Acceptance: `check-892-iam.sh <fugaro-project> [member…]` reads, read-only:
  - the GCP project's IAM policy;
  - the Firebase project's IAM policy;
  - the IAM of each per-repo build and job service account;
  - the local config's `terraform.admins`, `operators` and `budget_admins`.

  It prints, for each member named, which of `TF-admin`, `DB-admin` and `operator` it holds, and
  which report rows (by the Perms column) it can therefore exercise. Each principal's role comes
  from the grants node `config/admin/access` (`{grants: [{user, role, scope}]}`, read with its
  values; the local `terraform.admins` is printed beside it, and a difference is reported). It
  prints the whole matrix (principal, role in the node, project and resource roles held) for R1.
  Operators and viewers legitimately hold many grants (custom roles, actAs on several accounts,
  secret version adders), so the check does not try to list them. It FAILs on one unambiguous
  rule: a principal the node places as `operator` or `viewer` (or scopes to repositories) holds
  `roles/owner`, `roles/editor` or `roles/resourcemanager.projectIamAdmin` on the GCP project,
  which makes it an admin in all but name. A project owner or editor the node does not place is
  reported as an implicit admin (D1592), never failed. This settles U3.
- Files: `scripts/live-checks/check-892-iam.sh` (new), `scripts/live-checks/check-892-iam_test.sh` (new).
- Interfaces: Consumes nothing. Produces the output R1 records.
- Tests: `test_an_admin_maps_to_tf_admin`, `test_an_operator_with_many_grants_passes`,
  `test_an_operator_with_owner_fails`, `test_a_viewer_with_editor_fails`,
  `test_an_unplaced_owner_is_reported_as_implicit_admin`, `test_a_budget_admin_is_not_flagged`,
  `test_a_stale_local_admin_list_is_reported`,
  `test_only_reads_are_called`, `test_the_token_is_never_on_argv`, `test_stdin_is_closed`.
- Command: `bash scripts/live-checks/check-892-iam_test.sh`
- Red step: `test_an_admin_maps_to_tf_admin` fails before the script exists.
- Depends on: none.
- Commit: `repo-coupling-audit K3: Check 892, as a script`
- Reviewer tier: opus, because it reads IAM. The in-job review is the review (the owner's rule of
  2026-11-20).

### K4: Checks 890 to 892 registered in the suite and the checklist

- Acceptance: `scripts/live_checks_test.go`'s list names the three scripts and their tests.
  `scripts/live-checks/README.md` says both naming forms are in use (`check<NNN>.sh` and
  `check-<NNN>-<name>.sh`).
  `docs/gcp-live-checklist.md` has a section per check: what it measures, the one command the
  owner types, and what PASS means. It also has a register row for each.
  `go test ./scripts -run TestLiveCheckScripts` passes.
- Files: `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `scripts/live-checks/README.md`.
- Interfaces: Consumes K1–K3's files. Produces the registered checks.
- Tests: `TestLiveCheckScripts`, `TestLiveCheckCIShardCoversTheSuiteOnce`, and the Docs tests that
  pin the checklist.
- Command: `for i in 0 1 2 3; do LIVE_CHECK_SHARD=$i/4 go test ./scripts -run TestLiveCheckScripts -count=1 -timeout 30m || break; done && go test ./scripts -run TestLiveCheckCIShardCoversTheSuiteOnce -count=1 && go test ./internal/cli/ -run Docs -count=1` (the README's sharded pass)
- Red step: `TestLiveCheckScripts` lists no check-890 entry; a docs test that counts checklist
  sections fails until the sections exist.
- Depends on: K1, K2, K3 (kept, data: the list test runs every file it names).
- Commit: `repo-coupling-audit K4: Checks 890 to 892 registered`
- Reviewer tier: standard.
- Owner-run, after merge, on `fugaro`:
  - `bash scripts/live-checks/check-890-data-layout.sh fugaro`
  - `bash scripts/live-checks/check-891-update-trace.sh fugaro <each checkout>`
  - `bash scripts/live-checks/check-892-iam.sh fugaro <a member's address>`

  Paste each output into R1's brief.

### V1: U1 and U2 settled from the code

- Acceptance: the reconciliation's U1 and U2 are either findings with a cited symbol or stay
  UNVERIFIED with a reason, and `report.md` rows RC34 and RC71 say the answer.
  - U1: whether `workflows.*.resources` and `timeouts.*` reach a shared job. One lead:
    `internal/backend/gcp/run.go` already sets `req.Overrides.Timeout` per execution from
    `spec.Timeout`; resources are not yet traced.
  - U2: whether `localcfg repos.<r>.github_app_id` is per repository or the project's one App.
    Leads: `internal/cli/catalog_provider.go` `repoGitHubAppID` and
    `internal/backend/gcp/build.go`.
- Files: `docs/repo-coupling-audit/reconciliation.md`, `docs/repo-coupling-audit/report.md`,
  `docs/repo-coupling-audit/README.md` (if a count or a U-number it states changes).
- Interfaces: Consumes nothing. Produces the answers R1 builds on.
- Tests: none (documents). `fugaro plan graph --plan docs/repo-coupling-audit/implementation-plan.md` still reads.
- Command: `fugaro plan graph --plan docs/repo-coupling-audit/implementation-plan.md`
- Red step: none (a documents task).
- Depends on: none.
- Commit: `repo-coupling-audit V1: U1 and U2 settled from the code`
- Reviewer tier: standard.

### D1: the docs name `fugaro update`, not the missing `fugaro upgrade`

- Acceptance: `README.md` (Getting started, Upgrading), the whole of `docs/release.md` (the
  maintainers' migrator bullet, the plugin paragraph and the users' step 3) and
  `website/data/install.yaml` say `brew upgrade`, then one
  `fugaro update`. None names `fugaro upgrade` (no such command) or `fugaro update-skills`,
  committing the retired plugin pin, or the `/fugaro:upgrade` skill (the skill is `/fugaro:update`). `update-skills` is a registered command
  (`internal/cli/updateskills.go`, `root.go`): the docs drop it because the one-command upgrade
  rule allows no second command, not because it is missing. Any test that pinned the old
  wording pins the new one. These are report rows RC93, RC96 and RC97, and the first half of lift
  L10. It changes no behaviour. README's per-repository `fugaro update --repo` in Providers step 6
  stays until lift L2.
- Files: `README.md`, `docs/release.md`, `website/data/install.yaml`,
  `internal/cli/docs_upgrade_test.go`.
- Interfaces: none.
- Tests: `go test ./internal/cli/ -run Docs -count=1`, and the website's tests for the install
  page.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: a new assertion in `docs_upgrade_test.go` that none of the three files contains
  `fugaro upgrade` fails on main.
- Depends on: none.
- Commit: `repo-coupling-audit D1: the docs name fugaro update`
- Reviewer tier: standard.

### R1: the report's live columns from Checks 890 to 892

- Acceptance: every row of `report.md` whose Perms or Update cell reads from the code alone is
  confirmed or corrected from the checks' output. The summary counts are recomputed. U3 and U4
  are closed or restated. The reconciliation's O2 is closed. Any repo-keyed path, differing stage
  or role the checks found that the report lacked becomes a new row, numbered after RC105. The
  README's count follows.
- Files: `docs/repo-coupling-audit/report.md`, `docs/repo-coupling-audit/reconciliation.md`,
  `docs/repo-coupling-audit/README.md`.
- Interfaces: Consumes the owner's pasted outputs of Checks 890–892 and V1's answers.
- Gate: launched only with the three outputs in its brief. A run that finds none stops at once and
  writes nothing: the graph shows R1 ready when K4 and V1 merge, but its real input is the owner's
  runs.
- Tests: none (documents). The graph read still passes.
- Command: `fugaro plan graph --plan docs/repo-coupling-audit/implementation-plan.md`
- Red step: none (a documents task).
- Depends on: K4 (kept, behaviour: it records the checks' output, which exists only after the
  owner runs them), V1 (kept: three shared files, and R1 fills the columns V1's answers change).
- Commit: `repo-coupling-audit R1: the report's live columns`
- Reviewer tier: standard.

## Parallel execution

| Level | Tasks | Why it waits |
| --- | --- | --- |
| 0 | K1, K2, K3, V1, D1 | nothing |
| 1 | K4 | the scripts must exist for the suite's list |
| 2 | R1 | the owner's check output, and V1's file |

The critical path is K1 → K4 → owner runs → R1. Three agent tasks and one owner step.

**Shared files.** No level has two writers of one file.

| File | Writers | Levels |
| --- | --- | --- |
| `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `scripts/live-checks/README.md` | K4 | 1 |
| `docs/repo-coupling-audit/report.md`, `docs/repo-coupling-audit/reconciliation.md`, `docs/repo-coupling-audit/README.md` | V1, R1 | 0, 2 |
| `README.md`, `docs/release.md`, `website/data/install.yaml`, `internal/cli/docs_upgrade_test.go` | D1 | 0 |
| each check script | its own task | 0 |

**Edges broken and kept.** The contract-first pass is on (`planning.decoupling: true`; the
repository's rigor is `startup`). No edge carries only an interface:

- K4 ← K1, K2, K3 is data: the list test runs the files.
- R1 ← K4 is behaviour: the owner's output.
- R1 ← V1 is three shared files.

No contract was written, so there is no gain to report: the critical path stays three agent tasks
long.

**Contract changes anticipated.** None.

## The estimate

`fugaro plan graph` printed:

`ready in 86–418 min, no history yet for 1 task (7 tasks; tiers for 6, seeds for 1; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, not a figure the plan holds to.

## Checks this plan takes

Reserved: **890–899**. Used:

- **890**: the data layout of a real project, against the report.
- **891**: where `update` and a launch read repo context, from an empty directory and from each
  checkout.
- **892**: the IAM each member holds, against the Perms column.

All three are owner-run on `fugaro` after K4 merges. They are read-only and need no confirmation.

## After deploy

Nothing ships: the checks are scripts and the rest is documents. No `### For operators` entry.

## Considered and declined

- **Per-surface report runs**, declined by D3230: the static inventory was verified in this PR.
- **Building the version stamp**, declined by D3233: it is lift L1.
- **Removing the per-repo image code**, declined by D3234 (D1900 stands).
- **A live launch in Check 891**, declined: it costs a run, and the stored `launch.json` already
  names the job a launch went to. The owner can launch one by hand and rerun the check.
- **One task for all three scripts**, declined: eight files, past the five-file limit, and three
  independent reviews in one.
