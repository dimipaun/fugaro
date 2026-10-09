# Runs-Bucket IAM Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After this plan, only operators write `fugaro/`, `builds/`, `cache/` and `locks/` in the runs bucket. Launchers who are not operators keep:
- reading the whole bucket;
- writing `runs/`.

The project layer's executable keys (layered-config L7, option A) are then bounded by IAM, not only by `config publish --executable-changes`. Each of the following refuses what it should and says why: the install guard, `fugaro init`'s migration, the publish commands and `fugaro doctor`. This ships in release **0.7.0**, with the base image consolidation.

**Architecture:**
- **`internal/backend/gcp/names.go`:** `LauncherBucketCondition` and its title, the one definition.
- **`deploy/terraform/gcp/modules/installation/iam.tf`:** the grants.
  - `runs` (`objectAdmin`) goes to operators only.
  - `runs_reader` (`objectViewer`) and `runs_launcher` (`objectUser` under the condition) go to launchers who are not operators.
  - The new variable `launcher_bucket_condition` carries the condition, threaded through the root and `infra.InstallationSpec`.
- **`internal/infra/tf/cover.go`:** checks every bucket grant's condition.
- **`internal/infra/hardening.go`:**
  - `HardeningAllowDelete`, the migration's exact allow-list, used by the installation stage in `internal/cli/init.go`;
  - `RunsBucketFindings` and `ProjectStorageWriters`, doctor's pure policy readers.
- **`internal/blobx`:** classifies a 403 on a write or delete as `ErrForbidden`, and gains an unconditional `Put`.
- **`internal/cli/operator_write.go`:** the one refusal text.
- **`internal/cli/doctor_bucket.go`:** the `runs-bucket-iam` check.
- **`internal/gcpfake/gcs.go`:** can deny writes under a prefix, refuse a bucket's `getIamPolicy`, and answer `testIamPermissions`.

**Tech Stack:** Go 1.27, Terraform 1.16 (`terraform test` with `mock_provider`), tflint. Tests use:
- the GCS fake (`gcpfake.NewGCS`);
- the init rig (`newInitRig`, `setPlan`, `change`, the fake terraform);
- the doctor rig (`newDoctorRig`);
- the golden tfvars (`checkGolden`, `-update`).

**Spec:** [docs/design/bucket-iam.md](../design/bucket-iam.md)

**Depends on:**
- **Layered config Phase 1 (0.6.0):** Task 13 of [2026-10-08-layered-config.md](2026-10-08-layered-config.md) must be merged, because Task 6 wires `publishLayer` and `writeLayerCopy` from `internal/cli/config_publish.go` and `layer_copy.go`. Task 0 checks it.
- **The base image plan:** this plan does not depend on it, but it must merge before that plan's Task 22 (`/new-release 0.7.0`). The release notes of Task 11 join that release.

The code below is written against `main` at 65a6a24. It has **not** been compiled; each task's run command is its check.

## Decisions (veto any before execution starts)

These are the design's H1 to H14 (§12), restated as the plan executes them. **Ruling (2026-10-08): all of H1 to H14 are taken as recommended; no veto alternative below is implemented.**

- **H1. IAM Conditions on the existing bucket.** Veto alternative: managed folders, or a separate config bucket (a different plan).
- **H2. Launchers write all of `runs/`.** Veto alternative: leaf names only. Task 1's expression and Task 3's exact-match check change; the rest stands.
- **H3. Launchers read the whole bucket, unconditioned.**
- **H4. Operators keep `objectAdmin`.** Launcher grants go to `setsubtract(launchers, operators)`, so a one-person installation plans no change.
- **H5. The condition is defined in Go and passed as the tfvar `launcher_bucket_condition`,** title `fugaro-launchers-runs`. The installation module checks it with a resource precondition. Terraform 1.7, the minimum, has no cross-variable validation.
- **H6. The guard checks the condition of every `google_storage_bucket_iam_member`:**
  - a person's `objectUser` must carry exactly the launcher condition;
  - an account's `objectUser` must carry clauses of the `gcp.BucketCondition` shape;
  - `objectAdmin` and `objectViewer` must carry none.
- **H7. `infra.HardeningAllowDelete`** allows exactly the deletes of `runs["<launcher who is not an operator>"]` whose role was `objectAdmin`, when the same plan creates both new grants. The step shows a banner, and a delete is never covered, so the step asks its own typed name (or takes `--yes`).
- **H8. `blobx.ErrForbidden` plus one message from `operatorWriteErr`,** exit 1, and no pre-flight check.
- **H9. The doctor check `runs-bucket-iam`:**
  - the policy when it is readable, else the caller's `testIamPermissions`;
  - warnings and info, never an error;
  - skipped under a test configuration.
- **H10. No toggle.** Rollback is the documented `gcloud storage buckets add-iam-policy-binding`.
- **H11. In 0.7.0.** One operator's `fugaro init` is the last step of the operator notes.
- **H12. Project-level writers** are documented and reported, not changed.
- **H13. `DATA_WRITE` audit logs** are a documented optional step.
- **H14. The live check is user-run Check 32,** on installation `belong` with the sandbox repository only. The `belong` apply needs the owner's go-ahead.

## Global Constraints

Every task's requirements include these.

From the design:
- Nothing applies, plans against or creates anything in a cloud. Every test uses fakes (`gcpfake`, the fake terraform, `mock_provider`). Claude never runs `fugaro init` against a real project. The user applies the migration at their own terminal.
- `gcp.LauncherBucketCondition` is the only place the launcher expression is spelled. Terraform, the guard and doctor all compare against it, byte for byte.
- No launcher-run command gains a write outside `runs/`. Task 10's grep pins this.
- Every member string doctor or init prints from a policy or a plan goes through `pluginwire.Printable`.
- **Not doing:**
  - per-repository launcher grants;
  - the H2 leaf-names condition;
  - managed folders;
  - an audit config in Terraform;
  - any change to the job, build, history or scheduler accounts' grants;
  - a toggle.

Project rules:
- **Sized for dogfood runs.** No Docker and no cloud inside a run.
  - `terraform test` and `tflint` run only if `terraform`/`tflint` are on `PATH` and can fetch the pinned provider. Otherwise CI's `terraform` job is the check, and the PR says so.
  - The golden plans (`scripts/gen-golden-plans.sh`) need a provider cache. When the run has none, Task 2 leaves them, and the PR asks the reviewer to regenerate them. `TestGoldenPlansAreCovered` passes either way: the old plans hold no launcher grant.
- Subagent-driven development in one git worktree (`.worktrees/bucket-iam`, from `origin/main`), with a fresh implementer per task. **Tasks 2, 3 and 4 get their own review** (IAM, the guard, and a delete allow-list); the rest are reviewed once, on the branch, at the end.
- Every CI check (`test`, `terraform`, `rules`) is read before merge: each job's log, not only the summary.
- Docs must match behaviour. Task 8 adds `TestDocsLaunchersDoNotHoldObjectAdmin`.
- Releases go through `/new-release`, with `docs/releases/vX.Y.Z.md` merged first.
- **The `internal/cli` package is slow.** Each task runs only its focused tests (`-run`), with `-race`, **in the foreground**. Task 10 runs the touched packages in full, in the foreground, with `timeout: 600000` per call.

## Review Focus

The five failure modes most likely to hit a user, each pinned by a test:

1. **Launchers lose something they need**, such as a launch, a claim takeover, a cancel or the token cleanup, or a listing. Pinned by:
   - the tftest `people` run, which asserts the two grants and their exact condition (Task 2);
   - `TestLauncherGrantsCoverEveryLauncherWrite`, which walks the launcher write paths against `gcpfake` with `fugaro/`, `builds/`, `cache/` and `locks/` denied (Task 5);
   - live V1 and V2 (Task 12).
2. **The guard covers a widened grant:** an unconditioned `objectUser` to a person, a launcher condition naming another bucket or prefix, or an account grant with no condition. Pinned by `TestCoverBucketConditions` and the flipped cells of `TestGrantRulesPinnedCells` (Task 3).
3. **The migration deletes more than the launchers' old grants:** an operator's grant, a grant of another role, a replace, or a delete with no replacement created. Pinned by `TestHardeningAllowDelete` and `TestInitHardeningAllowsOnlyTheLauncherGrantDeletes` (Task 4).
4. **A launcher's refused publish looks like something else:** a missing object, a remote error with exit 2, or a half-written fan-out. Pinned by `TestWriteForbiddenIsClassified` (Task 5) and `TestPublishAsLauncherIsRefusedWithTheOperatorText` (Task 6).
5. **Doctor fails or touches a real bucket where it must not,** or calls an operator un-hardened. Pinned by `TestDoctorRunsBucketIAM*` (Task 7) and by the unchanged doctor suites (no storage endpoint means no check).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/backend/gcp/names.go`, `names_test.go` | `LauncherBucketCondition`, `LauncherBucketConditionTitle` | 1 |
| `deploy/terraform/gcp/modules/installation/iam.tf`, `variables.tf`, `bucket.tf` (comment), `roots/installation/main.tf`, `variables.tf`, `tests/installation.tftest.hcl`, `internal/infra/tfvars.go`, `internal/infra/project.go` (comment), `internal/infra/testdata/installation-budget*.tfvars.json`, `internal/infra/tf/testdata/golden/installation*.plan.json` (regenerated when possible) | the grants | 2 |
| `internal/infra/tf/cover.go`, `cover_grants_test.go`, `cover_bucket_test.go` (new) | the guard's condition check | 3 |
| `internal/infra/hardening.go` (new), `hardening_test.go` (new), `internal/cli/init.go`, `init_hardening_test.go` (new) | the migration's allow-list and banner | 4 |
| `internal/blobx/blobx.go`, `blobx_test.go`, `internal/gcpfake/gcs.go`, `internal/cli/launcher_writes_test.go` (new) | `ErrForbidden`, `Put`, the fake's denials | 5 |
| `internal/cli/operator_write.go` (new), `operator_write_test.go` (new), `recipes.go`, `sharedcfg.go`, `init.go`, `imagecheck.go`, `config_publish.go`, `layer_copy.go` | the refusal | 6 |
| `internal/infra/hardening.go`, `hardening_test.go`, `internal/gcpfake/gcs.go`, `internal/cli/doctor_bucket.go` (new), `doctor.go`, `doctor_bucket_test.go` (new) | the doctor check | 7 |
| `docs/gcp-setup.md`, `docs/recipes.md`, `docs/release.md`, `docs/design/v1.md`, `docs/design/shared-config.md`, `docs/design/watch-queued.md`, `docs/design/layered-config.md`, `internal/cli/docs_bucket_iam_test.go` (new) | docs | 8 |
| `docs/gcp-live-checklist.md` | Check 32 | 9 |
| none | full focused suites, PR | 10 |
| `docs/releases/v0.7.0.md` | release notes | 11 |
| none (user-run) | the live check | 12 |

## PR group

One PR, branch `bucket-iam`, Tasks 1 to 10 in order:
- Task 2 needs 1.
- Task 3 needs 1.
- Task 4 needs 2.
- Task 6 needs 5.
- Task 7 needs 1 and 5.
- Tasks 8 and 9 need everything before them.

Task 11 runs after the merge, in the 0.7.0 release PR. Task 12 is the user's, after `/new-release 0.7.0`.

### Task 0: Start

- [ ] **Step 1: Check the dependency and make the worktree**

```bash
cd /Users/dimi/git.lattica/Fugaro && git fetch -q
git worktree add -b bucket-iam .worktrees/bucket-iam origin/main
cd .worktrees/bucket-iam
grep -n 'func publishLayer' internal/cli/config_publish.go && grep -n 'func writeLayerCopy' internal/cli/layer_copy.go
grep -n 'resource "google_storage_bucket_iam_member" "runs"' deploy/terraform/gcp/modules/installation/iam.tf
```

Expected: the worktree is created, and each grep prints one line. If `config_publish.go` is missing, layered config Task 13 has not merged: **stop** and report. If `iam.tf` no longer has the `runs` resource, someone changed the grants since this plan was written: **stop** and report.

---

### Task 1: The launcher condition (`internal/backend/gcp/names.go`)

**Files:**
- Modify: `internal/backend/gcp/names.go`
- Test: `internal/backend/gcp/names_test.go`

**Interfaces:**
- Produces:
  ```go
  const LauncherBucketConditionTitle = "fugaro-launchers-runs"
  func LauncherBucketCondition(bucket string) string
  ```

- [ ] **Step 1: Write the failing test**

In `TestDisplayNamesAndConditions`, add two rows to the table:

```go
		{LauncherBucketConditionTitle, "fugaro-launchers-runs"},
		{LauncherBucketCondition("fugaro-runs-proj-1234"),
			`resource.name.startsWith("projects/_/buckets/fugaro-runs-proj-1234/objects/runs/")`},
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/backend/gcp/ -run TestDisplayNamesAndConditions`
Expected: a compile error, `undefined: LauncherBucketConditionTitle`.

- [ ] **Step 3: Implement**

Append to `names.go`, after `BucketConditionTitle`:

```go
// LauncherBucketConditionTitle is the title of the launchers' objectUser
// grant on the runs bucket (docs/design/bucket-iam.md H5).
const LauncherBucketConditionTitle = "fugaro-launchers-runs"

// LauncherBucketCondition limits the launchers' objectUser grant on bucket
// to runs/, every repository's (H2). Every launcher carries this exact
// string and LauncherBucketConditionTitle, with no description, so IAM keeps
// them in one conditional binding. The installation module, the install
// guard and doctor all compare against it: this is its one definition.
func LauncherBucketCondition(bucket string) string {
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/runs/")`, bucket)
}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test ./internal/backend/gcp/ -run TestDisplayNamesAndConditions`
Expected: `ok`.

**Mutation-proof:** both of these fail the row:
- dropping the trailing `/` after `runs` (which would let `runs-x/` match);
- writing `objects/runs` without the slash.

- [ ] **Step 5: Commit**

```bash
git add internal/backend/gcp/names.go internal/backend/gcp/names_test.go
git commit -m "bucket iam task 1: the launchers' runs/ condition, one definition"
```

---

### Task 2: The grants (Terraform, the spec, the golden tfvars) — **own review**

**Files:**
- Modify:
  - `deploy/terraform/gcp/modules/installation/iam.tf`, `variables.tf`, `bucket.tf` (the marker comment);
  - `deploy/terraform/gcp/roots/installation/main.tf`, `variables.tf`;
  - `internal/infra/tfvars.go`, `internal/infra/project.go` (the marker comment).
- Test:
  - `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`;
  - `internal/infra/testdata/installation-budget.tfvars.json`, `installation-budget-job.tfvars.json` (regenerated);
  - `internal/infra/installation_test.go`.

**Interfaces:**
- Consumes: `gcp.LauncherBucketCondition`, `gcp.LauncherBucketConditionTitle` (Task 1).
- Produces:
  - `infra.InstallationSpec.LauncherBucketCondition Condition` (JSON `launcher_bucket_condition`);
  - the module variable `launcher_bucket_condition`;
  - the resources `google_storage_bucket_iam_member.runs` (operators), `runs_reader` and `runs_launcher` (launchers who are not operators).

- [ ] **Step 1: Write the failing Go test**

Add to `internal/infra/installation_test.go`:

```go
// The installation spec carries the launchers' condition exactly as
// gcp.LauncherBucketCondition spells it, for the spec's own runs bucket.
func TestInstallationSpecCarriesTheLauncherCondition(t *testing.T) {
	spec := installationSpec(t)
	want := Condition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(spec.RunsBucket)}
	if spec.LauncherBucketCondition != want {
		t.Fatalf("launcher condition = %+v, want %+v", spec.LauncherBucketCondition, want)
	}
	data, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		C Condition `json:"launcher_bucket_condition"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.C != want {
		t.Fatalf("tfvars launcher_bucket_condition = %+v (%v), want %+v", doc.C, err, want)
	}
}
```

Add `"github.com/dimipaun/fugaro/internal/backend/gcp"` to the imports if the file lacks it.

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/infra/ -run 'TestInstallationSpecCarriesTheLauncherCondition|TestInstallationVarsMatchModule|TestInstallationRootPassesEveryVariable'`
Expected: a compile error, `spec.LauncherBucketCondition undefined`.

- [ ] **Step 3: Write the failing Terraform test**

In `installation.tftest.hcl`, add to the file-level `variables` block:

```hcl
  launcher_bucket_condition = {
    title      = "fugaro-launchers-runs"
    expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"
  }
```

In the `people` run, replace the two `google_storage_bucket_iam_member.runs` asserts with these:

```hcl
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs) == ["user:operator@example.com"]
    error_message = "only operators get objectAdmin on the runs bucket"
  }
  assert {
    condition     = alltrue([for m in google_storage_bucket_iam_member.runs : m.role == "roles/storage.objectAdmin" && m.bucket == "fugaro-runs-proj-1234" && length(m.condition) == 0])
    error_message = "operators' runs bucket grants must be unconditioned objectAdmin"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs_reader) == ["user:launcher@example.com"]
    error_message = "launchers who are not operators read the runs bucket"
  }
  assert {
    condition     = alltrue([for m in google_storage_bucket_iam_member.runs_reader : m.role == "roles/storage.objectViewer" && m.bucket == "fugaro-runs-proj-1234" && length(m.condition) == 0])
    error_message = "the launchers' read grant is unconditioned objectViewer: listing needs it"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs_launcher) == ["user:launcher@example.com"]
    error_message = "launchers who are not operators write runs/"
  }
  assert {
    condition = alltrue([for m in google_storage_bucket_iam_member.runs_launcher :
      m.role == "roles/storage.objectUser" && m.bucket == "fugaro-runs-proj-1234" &&
      one(m.condition).title == "fugaro-launchers-runs" &&
    one(m.condition).expression == "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"])
    error_message = "the launchers' write grant is objectUser on runs/ only"
  }
```

Add two runs after `people`:

```hcl
# A member on both lists gets the operator grant only (H4).
run "launcher_who_is_an_operator" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launchers = ["user:both@example.com", "user:launcher@example.com"]
    operators = ["user:both@example.com"]
  }

  assert {
    condition     = keys(google_storage_bucket_iam_member.runs) == ["user:both@example.com"]
    error_message = "the operator keeps objectAdmin"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs_reader) == ["user:launcher@example.com"] && keys(google_storage_bucket_iam_member.runs_launcher) == ["user:launcher@example.com"]
    error_message = "an operator who is also a launcher gets no launcher grant"
  }
}

# A condition that is not runs/ of this bucket fails the plan.
run "launcher_condition_must_be_this_buckets_runs" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "fugaro-launchers-runs"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/\")"
    }
  }

  expect_failures = [google_storage_bucket_iam_member.runs_launcher]
}
```

If `terraform` is on `PATH`, run `terraform -chdir=deploy/terraform/gcp/roots/installation init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/installation test`. Expected: failures naming `launcher_bucket_condition` (an undeclared variable) and the missing resources. If terraform is absent or cannot fetch the provider, note it for the PR and go on.

- [ ] **Step 4: Implement the module**

In `modules/installation/iam.tf`, replace the `runs` resource and its comment with:

```hcl
# The runs bucket (design bucket-iam.md §3). Operators read and write all of
# it: fugaro/ (the project layer, the shared config, recipes, the marker),
# builds/, cache/, locks/ and runs/.
resource "google_storage_bucket_iam_member" "runs" {
  for_each = toset(var.operators)

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectAdmin"
  member = each.value
}

locals {
  # A launcher who is also an operator needs nothing more (H4).
  launchers_only = setsubtract(toset(var.launchers), toset(var.operators))
}

# Launchers read the whole bucket. Listing runs/ needs storage.objects.list,
# which a condition on an object's name can't grant, so this one has none.
resource "google_storage_bucket_iam_member" "runs_reader" {
  for_each = local.launchers_only

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = each.value
}

# Launchers write runs/ only. objectUser, not objectCreator: the launch
# claim's takeover overwrites and its release deletes. The condition is the
# input byte for byte, the same for every launcher, so IAM keeps them in one
# binding; no description, like the job accounts' conditions.
resource "google_storage_bucket_iam_member" "runs_launcher" {
  for_each = local.launchers_only

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = each.value

  condition {
    title      = var.launcher_bucket_condition.title
    expression = var.launcher_bucket_condition.expression
  }

  lifecycle {
    precondition {
      condition     = var.launcher_bucket_condition.title == "fugaro-launchers-runs" && var.launcher_bucket_condition.expression == "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/runs/\")"
      error_message = "launcher_bucket_condition must be runs/ of the runs bucket, titled fugaro-launchers-runs (gcp.LauncherBucketCondition)."
    }
  }
}
```

Append to `modules/installation/variables.tf`:

```hcl
variable "launcher_bucket_condition" {
  description = "The condition of the launchers' objectUser grant on the runs bucket: runs/ of that bucket only (gcp.LauncherBucketCondition). Passed byte for byte, so every launcher shares one binding."
  type = object({
    title      = string
    expression = string
  })

  validation {
    condition     = startswith(var.launcher_bucket_condition.expression, "resource.name.startsWith(\"projects/_/buckets/") && length(var.launcher_bucket_condition.title) > 0
    error_message = "launcher_bucket_condition needs a title and a resource.name.startsWith expression on a bucket."
  }
}
```

In `roots/installation/variables.tf`, add the same variable with the same type and no default. In `roots/installation/main.tf`'s `module "installation"` block, add `launcher_bucket_condition = var.launcher_bucket_condition`; `terraform fmt` aligns the `=`.

In `modules/installation/bucket.tf`, replace the marker comment's last two sentences ("No job account can write it … It is a safety label, not a boundary.") with:

```hcl
# No job account can write it (the jobs' grants cover runs/, cache/ and
# locks/; builds cover builds/), and since 0.7.0 no launcher either
# (bucket-iam.md): only operators, and project-level storage writers.
```

- [ ] **Step 5: Implement the spec**

In `internal/infra/tfvars.go`, add to `InstallationSpec`, after `Operators`:

```go
	// LauncherBucketCondition limits the launchers' write grant on the runs
	// bucket to runs/ (gcp.LauncherBucketCondition).
	LauncherBucketCondition Condition `json:"launcher_bucket_condition"`
```

In `Installation`, add to the literal:

```go
		LauncherBucketCondition: Condition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(bucket)},
```

Import `github.com/dimipaun/fugaro/internal/backend/gcp` if `tfvars.go` lacks it.

In `internal/infra/project.go`, update `ProjectMarkerObject`'s comment. Replace "It is a safety label, not a boundary: no job account can write it, but a launcher could." with "No job account can write it, and since 0.7.0 no launcher (docs/design/bucket-iam.md); an operator or a project-level storage writer can." Keep `markerMaxBytes`' comment, which says "anyone holding objectAdmin", as it is: that is still true.

- [ ] **Step 6: Regenerate the golden tfvars and run the Go tests**

Run: `go test ./internal/infra/ -run TestFirebaseTfvarsGolden -update`, then `git diff internal/infra/testdata/`.
Expected: each `installation-budget*.tfvars.json` gains exactly one `launcher_bucket_condition` object, with the title and the expression for `fugaro-runs-proj-1234`, and nothing else changes.

Run: `go test ./internal/infra/ -run 'TestInstallation|TestFirebaseTfvarsGolden|TestHistory' && go test ./deploy/terraform/...`
Expected: `ok`. `TestInstallationVarsMatchModule` and `TestInstallationRootPassesEveryVariable` pass because the root declares and passes the new variable.

If terraform is available, rerun Step 3's `terraform test` and `terraform fmt -check -recursive deploy/terraform`. Expected: every run passes. If `tflint` is available: `tflint --chdir=deploy/terraform/gcp --init && tflint --chdir=deploy/terraform/gcp --recursive`, clean.

**Mutation-proof:** each of these fails a test:
- `for_each = toset(concat(var.launchers, var.operators))` left on `runs` fails the `people` key assert;
- `objectCreator` in `runs_launcher` fails the role assert;
- a `condition` block on `runs_reader` fails `length(m.condition) == 0`;
- `var.launchers` instead of `local.launchers_only` fails `launcher_who_is_an_operator`;
- removing the precondition fails `launcher_condition_must_be_this_buckets_runs`, which then has no failure to expect.

- [ ] **Step 7: Regenerate the golden plans when possible**

If `~/.cache/fugaro/terraform-plugins` holds the pinned google provider: run `scripts/gen-golden-plans.sh`, then `go test ./internal/infra/tf/ -run Golden`. Expected: `ok`. The installation plans then hold `runs_reader`/`runs_launcher` only if the golden tfvars list a launcher who is not an operator. They list none, so expect no new grant resources, only the variable. Otherwise skip, and put in the PR body: "golden plans not regenerated in the run: no provider cache".

- [ ] **Step 8: Commit**

```bash
git add deploy/terraform internal/infra
git commit -m "bucket iam task 2: launchers read the runs bucket and write only runs/; operators keep objectAdmin"
```

---

### Task 3: The guard checks bucket-grant conditions (`internal/infra/tf/cover.go`) — **own review**

**Files:**
- Modify: `internal/infra/tf/cover.go`
- Test: `internal/infra/tf/cover_grants_test.go`, `internal/infra/tf/cover_bucket_test.go` (new)

**Interfaces:**
- Consumes: `gcp.LauncherBucketCondition`, `gcp.LauncherBucketConditionTitle`. The `tf` package imports `internal/backend/gcp`, which imports nothing of `internal/infra`, so there is no cycle. Check it with `go list -deps ./internal/backend/gcp | grep internal/infra`, which must print nothing.
- Produces: `func (c Cover) bucketCondition(ch Change) string` (unexported), and `grantRules["google_storage_bucket_iam_member"]` with `objectUser` and `objectViewer` granted to `toPeople | toAccounts`.

- [ ] **Step 1: Write the failing tests**

Create `internal/infra/tf/cover_bucket_test.go`:

```go
package tf

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	cbBucket = "fugaro-runs-proj-1234"
	cbPerson = "user:launcher@example.com"
	cbJobSA  = "serviceAccount:fugaro-acme-web-1a2b3c4d@proj-1234.iam.gserviceaccount.com"
)

func bucketGrant(member, role string, cond map[string]any) *Plan {
	conds := []any{}
	if cond != nil {
		conds = []any{cond}
	}
	return &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"bucket": cbBucket, "member": member, "role": role, "condition": conds,
		}, AfterUnknown: map[string]any{"condition": []any{map[string]any{}}}},
	}}}
}

func cond(title, expr string) map[string]any {
	return map[string]any{"title": title, "expression": expr, "description": nil}
}

func TestCoverBucketConditions(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	launcher := cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket))
	job := cond("fugaro-x", gcp.BucketCondition(cbBucket, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef"))
	for _, tc := range []struct {
		name          string
		member, role  string
		cond          map[string]any
		covered       bool
		wantInProblem string
	}{
		{"launcher writes runs/", cbPerson, "roles/storage.objectUser", launcher, true, ""},
		{"launcher reads, unconditioned", cbPerson, "roles/storage.objectViewer", nil, true, ""},
		{"operator objectAdmin, unconditioned", cbPerson, "roles/storage.objectAdmin", nil, true, ""},
		{"job account under its prefixes", cbJobSA, "roles/storage.objectUser", job, true, ""},
		{"person objectUser without a condition", cbPerson, "roles/storage.objectUser", nil, false, "condition"},
		{"person objectUser on fugaro/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/")`), false, "condition"},
		{"person objectUser on another bucket's runs/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition("fugaro-runs-other")), false, "condition"},
		{"person objectUser, right expression, other title", cbPerson, "roles/storage.objectUser",
			cond("mine", gcp.LauncherBucketCondition(cbBucket)), false, "condition"},
		{"person objectUser with a description", cbPerson, "roles/storage.objectUser",
			map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition(cbBucket), "description": "x"}, false, "description"},
		{"objectAdmin under a condition", cbPerson, "roles/storage.objectAdmin", launcher, false, "condition"},
		{"objectViewer under a condition", cbPerson, "roles/storage.objectViewer", launcher, false, "condition"},
		{"account objectUser without a condition", cbJobSA, "roles/storage.objectUser", nil, false, "condition"},
		{"account objectUser with the launchers' condition", cbJobSA, "roles/storage.objectUser", launcher, false, "condition"},
		{"account objectUser on fugaro/<x>/", cbJobSA, "roles/storage.objectUser",
			cond("t", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/acme/")`), false, "condition"},
		{"account objectUser, one clause on another bucket", cbJobSA, "roles/storage.objectUser",
			cond("t", gcp.BucketCondition(cbBucket, []string{"runs"}, "s")+" || "+gcp.BucketCondition("fugaro-runs-other", []string{"cache"}, "s")), false, "condition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.NotCovered(bucketGrant(tc.member, tc.role, tc.cond), nil)
			if tc.covered && len(got) != 0 {
				t.Fatalf("not covered: %v", got)
			}
			if !tc.covered && (len(got) != 1 || !strings.Contains(got[0], tc.wantInProblem)) {
				t.Fatalf("got %v, want one problem naming %q", got, tc.wantInProblem)
			}
		})
	}
}

// A condition known only after apply is never covered.
func TestCoverBucketConditionUnknown(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	p := bucketGrant(cbPerson, "roles/storage.objectUser", cond(gcp.LauncherBucketConditionTitle, ""))
	p.ResourceChanges[0].Change.AfterUnknown = map[string]any{"condition": []any{map[string]any{"expression": true}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "not known") {
		t.Fatalf("got %v", got)
	}
}
```

In `cover_grants_test.go`'s `TestGrantRulesPinnedCells`:
- flip `{bkt, "roles/storage.objectUser", person, false}` to `true`;
- add `{bkt, "roles/storage.objectViewer", person, true}`.

These cells test `checkGrant` alone (the table), not the condition, which `TestCoverBucketConditions` covers. If the cell harness goes through `NotCovered`, give the person's `objectUser` cell the launcher condition so the cell stays a test of the table.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/infra/tf/ -run 'TestCoverBucketCondition|TestGrantRulesPinnedCells|TestGrantRulesAreTheModules'`
Expected:
- the person `objectUser` rows fail with "to a kind of principal the installation's modules do not grant it to";
- the conditioned `objectAdmin`/`objectViewer` rows and the account rows without a Fugaro prefix condition are wrongly covered;
- the flipped `TestGrantRulesPinnedCells` cells fail;
- `TestGrantRulesAreTheModules` already passes (it checks role names per resource type, not principals).

- [ ] **Step 3: Implement**

In `cover.go`, change the bucket row of `grantRules`:

```go
	"google_storage_bucket_iam_member": {
		"roles/storage.objectAdmin":  toPeople,
		"roles/storage.objectUser":   toPeople | toAccounts, // people only under the launchers' condition (bucketCondition)
		"roles/storage.objectViewer": toPeople | toAccounts,
	},
```

In `NotCovered`, replace the `_iam_member` case with:

```go
		case strings.HasSuffix(t, "_iam_member"):
			why := c.checkGrant(rc.Type, rc.Change, creates[modulePrefix(rc)+"|google_service_account"], creates[modulePrefix(rc)+"|google_project_iam_custom_role"])
			if why == "" && t == "google_storage_bucket_iam_member" {
				why = c.bucketCondition(rc.Change)
			}
			if why != "" {
				add(rc, why)
			}
```

Add after `checkGrant`:

```go
// accountClause is one clause of a job or build account's bucket condition,
// as gcp.BucketCondition writes it.
var accountClause = regexp.MustCompile(`^resource\.name\.startsWith\("projects/_/buckets/([a-z0-9][a-z0-9._-]*)/objects/(runs|cache|locks|builds)/([a-z0-9][a-z0-9_-]*)/"\)$`)

// bucketCondition says why a bucket grant's condition is not the modules'
// (docs/design/bucket-iam.md H6), "" when it is: objectAdmin and objectViewer
// carry none; a person's objectUser carries exactly the launchers' condition
// on one of this run's buckets; an account's objectUser carries clauses of
// gcp.BucketCondition's shape, each on one of this run's buckets. The modules
// never set a description, and a condition known only after apply is not
// covered.
func (c Cover) bucketCondition(ch Change) string {
	if unknown(ch, "condition") || unknownAt(ch.AfterUnknown, "condition", 0, "title") || unknownAt(ch.AfterUnknown, "condition", 0, "expression") {
		return "a bucket grant whose condition is not known"
	}
	conds := objects(ch.After["condition"])
	if len(conds) > 1 {
		return "a bucket grant with more than one condition"
	}
	var title, expr string
	if len(conds) == 1 {
		if str(conds[0]["description"]) != "" {
			return "a bucket grant whose condition has a description, which the modules never set"
		}
		title, expr = str(conds[0]["title"]), str(conds[0]["expression"])
	}
	role := str(ch.After["role"])
	person := !unknown(ch, "member") && slices.Contains(c.Listed, str(ch.After["member"]))
	switch {
	case role == "roles/storage.objectAdmin" || role == "roles/storage.objectViewer":
		if len(conds) != 0 {
			return "grants " + role + " on the bucket under a condition, which the modules never do"
		}
	case role == "roles/storage.objectUser" && person:
		bucket := str(ch.After["bucket"])
		for _, b := range c.Buckets {
			if (bucket == "" || bucket == b) && title == gcp.LauncherBucketConditionTitle && expr == gcp.LauncherBucketCondition(b) {
				return ""
			}
		}
		return "grants objectUser on the bucket to a person under a condition that is not the launchers' runs/ condition"
	case role == "roles/storage.objectUser":
		if len(conds) == 0 {
			return "grants objectUser on the bucket to an account with no condition"
		}
		for _, clause := range strings.Split(expr, " || ") {
			m := accountClause.FindStringSubmatch(clause)
			if m == nil || !slices.Contains(c.Buckets, m[1]) {
				return "grants objectUser on the bucket to an account under a condition that is not a Fugaro prefix condition of this run's bucket"
			}
		}
	}
	return ""
}
```

Import `github.com/dimipaun/fugaro/internal/backend/gcp`.

- [ ] **Step 4: Run them and see them pass, and the golden plans with them**

Run: `go test ./internal/infra/tf/...`
Expected: `ok`. That includes `TestGoldenPlansAreCovered`: the repo plan's build and job grants match `accountClause`, and the history reader is unconditioned.

**Mutation-proof:** each of these fails `TestCoverBucketConditions`:
- removing the `bucketCondition` call: every "not covered" row;
- accepting any expression that starts with `resource.name.startsWith("projects/_/buckets/<b>/objects/`: the `fugaro/` row;
- dropping `bucket == b`: the "another bucket" row;
- dropping the title comparison: the "other title" row;
- `accountClause` without `$`: the `fugaro/acme/` row.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/tf
git commit -m "bucket iam task 3: the install guard checks every bucket grant's condition"
```

---

### Task 4: The migration's deletes (`internal/infra/hardening.go`, `internal/cli/init.go`) — **own review**

**Files:**
- Create: `internal/infra/hardening.go`, `internal/infra/hardening_test.go`, `internal/cli/init_hardening_test.go`
- Modify: `internal/cli/init.go`

**Interfaces:**
- Consumes: `tf.Plan`, `infra.InstallationSpec.Launchers/Operators`, `guard` (`init.go`).
- Produces:
  ```go
  // package infra
  func HardeningAllowDelete(p *tf.Plan, launchers, operators []string) []string
  func HardeningBanner(allowed []string) string
  ```

- [ ] **Step 1: Write the failing unit test**

Create `internal/infra/hardening_test.go`:

```go
package infra

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

func grantAddr(name, member string) string {
	return `module.installation.google_storage_bucket_iam_member.` + name + `["` + member + `"]`
}

func rc(addr, role string, actions ...string) tf.ResourceChange {
	ch := tf.Change{Actions: actions}
	if slices.Contains(actions, "delete") {
		ch.Before = map[string]any{"role": role}
	}
	if slices.Contains(actions, "create") {
		ch.After = map[string]any{"role": role}
	}
	return tf.ResourceChange{Address: addr, Type: "google_storage_bucket_iam_member", Change: ch}
}

func TestHardeningAllowDelete(t *testing.T) {
	const a, b, o = "user:a@example.com", "user:b@example.com", "user:o@example.com"
	launchers, operators := []string{a, b, o}, []string{o}
	full := func(m string) []tf.ResourceChange {
		return []tf.ResourceChange{
			rc(grantAddr("runs", m), "roles/storage.objectAdmin", "delete"),
			rc(grantAddr("runs_reader", m), "roles/storage.objectViewer", "create"),
			rc(grantAddr("runs_launcher", m), "roles/storage.objectUser", "create"),
		}
	}
	for _, tc := range []struct {
		name    string
		changes []tf.ResourceChange
		want    []string
	}{
		{"two launchers migrate", append(full(a), full(b)...), []string{grantAddr("runs", a), grantAddr("runs", b)}},
		{"an operator's grant is never allowed", full(o), nil},
		{"a member on no list", full("user:gone@example.com"), nil},
		{"no reader created", []tf.ResourceChange{full(a)[0], full(a)[2]}, nil},
		{"no launcher grant created", []tf.ResourceChange{full(a)[0], full(a)[1]}, nil},
		{"a replace, not a delete", []tf.ResourceChange{rc(grantAddr("runs", a), "roles/storage.objectAdmin", "delete", "create"), full(a)[1], full(a)[2]}, nil},
		{"another role", []tf.ResourceChange{rc(grantAddr("runs", a), "roles/storage.objectViewer", "delete"), full(a)[1], full(a)[2]}, nil},
		{"another resource", []tf.ResourceChange{rc(grantAddr("state", a), "roles/storage.objectAdmin", "delete"), full(a)[1], full(a)[2]}, nil},
		{"another module", []tf.ResourceChange{rc(strings.Replace(grantAddr("runs", a), "module.installation", "module.other", 1), "roles/storage.objectAdmin", "delete"), full(a)[1], full(a)[2]}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HardeningAllowDelete(&tf.Plan{ResourceChanges: tc.changes}, launchers, operators)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHardeningBanner(t *testing.T) {
	if HardeningBanner(nil) != "" {
		t.Fatal("a banner with nothing allowed")
	}
	got := HardeningBanner([]string{grantAddr("runs", "user:a@example.com"), grantAddr("runs", "user:\x1b[31mx@example.com")})
	for _, want := range []string{"bucket access (0.7.0)", "  user:a@example.com\n", `\u001b`} {
		if !strings.Contains(got, want) {
			t.Errorf("banner lacks %q:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/infra/ -run 'TestHardening'`
Expected: a compile error, `undefined: HardeningAllowDelete`.

- [ ] **Step 3: Implement**

Create `internal/infra/hardening.go`:

```go
package infra

import (
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

const bucketGrantPrefix = "module.installation.google_storage_bucket_iam_member."

// grantMember is the member of address name["<member>"], ok false for any
// other address.
func grantMember(address, name string) (string, bool) {
	rest, ok := strings.CutPrefix(address, bucketGrantPrefix+name+"[")
	if !ok || !strings.HasSuffix(rest, "]") {
		return "", false
	}
	m, err := strconv.Unquote(strings.TrimSuffix(rest, "]"))
	return m, err == nil
}

// HardeningAllowDelete is the 0.7.0 bucket hardening's allow-list for p
// (docs/design/bucket-iam.md H7): each delete of a launcher's old objectAdmin
// grant on the runs bucket, runs["<m>"], where m is a launcher and not an
// operator and p also creates m's runs_reader and runs_launcher grants.
// A replace, another role, another resource, an operator's grant or a
// member on neither list is never allowed: the guard refuses it as before.
func HardeningAllowDelete(p *tf.Plan, launchers, operators []string) []string {
	if p == nil {
		return nil
	}
	created := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if slices.Equal(rc.Change.Actions, []string{"create"}) {
			created[rc.Address] = true
		}
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		m, ok := grantMember(rc.Address, "runs")
		if !ok || !slices.Equal(rc.Change.Actions, []string{"delete"}) {
			continue
		}
		if role, _ := rc.Change.Before["role"].(string); role != "roles/storage.objectAdmin" {
			continue
		}
		if !slices.Contains(launchers, m) || slices.Contains(operators, m) {
			continue
		}
		q := strconv.Quote(m)
		if !created[bucketGrantPrefix+"runs_reader["+q+"]"] || !created[bucketGrantPrefix+"runs_launcher["+q+"]"] {
			continue
		}
		out = append(out, rc.Address)
	}
	slices.Sort(out)
	return out
}

// HardeningBanner names the launchers whose old grant the plan deletes, ""
// when there are none.
func HardeningBanner(allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("bucket access (0.7.0): these launchers keep reading the runs bucket and writing runs/, and lose write access to fugaro/, builds/, cache/ and locks/:\n")
	for _, a := range allowed {
		m, _ := grantMember(a, "runs")
		b.WriteString("  " + pluginwire.Printable(m) + "\n")
	}
	return b.String()
}
```

Check that `internal/infra` may import `internal/pluginwire`: run `go list -deps ./internal/pluginwire | grep internal/infra`, which must print nothing. If it prints something, use the package's own `oneLine`/printable helper instead (`grep -n 'func.*[Pp]rintable\|func oneLine' internal/infra/*.go`).

In `internal/cli/init.go`, in the installation stage, replace

```go
		if err := guard(plan, r.o.allowDelete); err != nil {
			return outs, false, err
		}
```

(the call right after `fmt.Fprint(r.w, tf.Summary(plan))` in the installation apply, not the Firebase or repository ones) with:

```go
		// The 0.7.0 bucket hardening deletes launchers' old objectAdmin
		// grants; exactly those are allowed, and named (bucket-iam.md H7).
		// A delete is never covered, so the step still asks its own name.
		hardening := infra.HardeningAllowDelete(plan, spec.Launchers, spec.Operators)
		if err := guard(plan, append(slices.Clone(r.o.allowDelete), hardening...)); err != nil {
			return outs, false, err
		}
		fmt.Fprint(r.w, infra.HardeningBanner(hardening))
```

`spec` is the `infra.InstallationSpec` the stage planned. If the variable has another name there, use it, and never re-derive the lists.

- [ ] **Step 4: Write the failing init test**

Create `internal/cli/init_hardening_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func grantChange(name, member, role string, actions ...string) planChange {
	addr := `module.installation.google_storage_bucket_iam_member.` + name + `["` + member + `"]`
	ch := map[string]any{"actions": actions, "before": map[string]any{}, "after": nil}
	if actions[0] == "delete" {
		ch["before"] = map[string]any{"role": role, "member": member, "bucket": "fugaro-runs-proj-1234"}
	} else {
		ch["after"] = map[string]any{"role": role, "member": member, "bucket": "fugaro-runs-proj-1234", "condition": []any{}}
	}
	return planChange{"address": addr, "type": "google_storage_bucket_iam_member", "change": ch}
}

// The launchers' old objectAdmin grants are deleted without --allow-delete
// when their replacements are created, and named in a banner; an operator's
// grant, or a member on neither list, is still refused.
func TestInitHardeningAllowsOnlyTheLauncherGrantDeletes(t *testing.T) {
	const a, o = "user:a@example.com", "user:o@example.com"
	args := []string{"init", "--yes", "--launcher", a, "--launcher", o, "--operator", o}

	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t,
		grantChange("runs", a, "roles/storage.objectAdmin", "delete"),
		grantChange("runs_reader", a, "roles/storage.objectViewer", "create"),
		grantChange("runs_launcher", a, "roles/storage.objectUser", "create"))
	out, _, err := executeStdin(t, "", args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "bucket access (0.7.0)") || !strings.Contains(out, "  "+a+"\n") {
		t.Errorf("no banner naming %s:\n%s", a, out)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Fatal("the hardening plan was not applied")
	}

	// The operator's grant: refused, with the launcher/operator hint.
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, grantChange("runs", o, "roles/storage.objectAdmin", "delete"))
	_, _, err = executeStdin(t, "", args...)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `runs["`+o+`"]`) || !strings.Contains(err.Error(), "--launcher and --operator") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("an operator's grant delete was applied")
	}

	// A launcher's delete with no replacement: refused.
	r = newInitRig(t)
	r.stateBucket()
	r.setPlan(t, grantChange("runs", a, "roles/storage.objectAdmin", "delete"))
	if _, _, err := executeStdin(t, "", args...); ExitCode(err) != ExitUserError {
		t.Fatalf("a delete without its replacements: exit %d, err %v", ExitCode(err), err)
	}
}
```

If `--launcher`/`--operator` with `--yes` need a typed confirmation in this rig (a changed member list is not covered), pass the project name on stdin (`names(n)`, as the review tests do) instead of `""`, and keep `--yes`.

- [ ] **Step 5: Run the tests and see them pass**

Run: `go test ./internal/infra/ -run TestHardening && go test -race ./internal/cli/ -run 'TestInitHardening|TestInitRefusesDeletePlan|TestGuardListsRemovedAdminGrant'`
Expected: `ok`.

**Mutation-proof:** each of these fails a row or the init test:
- dropping the `created` checks: "no reader created";
- dropping the operator exclusion: "an operator's grant";
- accepting `delete, create`: "a replace";
- passing `r.o.allowDelete` alone in `init.go`: the first init case, refused;
- printing the banner before `guard`: the operator case's output gains a banner. The test does not check that; the review must.

- [ ] **Step 6: Commit**

```bash
git add internal/infra/hardening.go internal/infra/hardening_test.go internal/cli/init.go internal/cli/init_hardening_test.go
git commit -m "bucket iam task 4: init allows exactly the launchers' old grant deletes, behind a banner"
```

---

### Task 5: A refused write is `ErrForbidden` (`internal/blobx`, `internal/gcpfake`)

**Files:**
- Modify: `internal/blobx/blobx.go`, `internal/gcpfake/gcs.go`
- Test: `internal/blobx/blobx_test.go`, `internal/cli/launcher_writes_test.go` (new)

**Interfaces:**
- Produces:
  ```go
  // package blobx
  var ErrForbidden error
  func (b *Bucket) Put(ctx context.Context, key string, data []byte, contentType string) error
  // package gcpfake
  func (g *GCS) DenyWrites(bucket, prefix string)   // 403 on create, overwrite, delete, patch under prefix
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/blobx/blobx_test.go`:

```go
// gocloud reports a GCS 403 as NotFound; every write and delete returns
// ErrForbidden for it instead, so a refused write never reads as "absent".
func TestWriteForbiddenIsClassified(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.Put("runs", "fugaro/x.yaml", []byte("a: 1\n"))
	fake.DenyWrites("runs", "fugaro/")
	b := fake.Bucket(t, "runs")
	_, gen, err := b.Read(ctx, "fugaro/x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"create":  func() error { _, err := b.Create(ctx, "fugaro/y.yaml", []byte("b"), "text/plain"); return err }(),
		"replace": func() error { _, err := b.ReplaceIf(ctx, "fugaro/x.yaml", []byte("{}"), gen, nil); return err }(),
		"put":     b.Put(ctx, "fugaro/x.yaml", []byte("c"), "text/plain"),
		"delete":  b.DeleteIf(ctx, "fugaro/x.yaml", gen, nil),
		"delete existing": b.DeleteExisting(ctx, "fugaro/x.yaml", gen, nil),
	} {
		if !errors.Is(err, blobx.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", name, err)
		}
		if errors.Is(err, blobx.ErrNotExist) || errors.Is(err, blobx.ErrConflict) {
			t.Errorf("%s: a 403 read as %v", name, err)
		}
	}
	// Outside the denied prefix, writes work.
	if err := b.Put(ctx, "runs/s/r/task.json", []byte("{}"), "application/json"); err != nil {
		t.Fatal(err)
	}
}
```

Create `internal/cli/launcher_writes_test.go`:

```go
package cli

import (
	"context"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// Every write a launcher makes is under runs/, so the 0.7.0 grants (writes
// denied everywhere else) leave launching, the claim takeover and release,
// cancelling and the token object's cleanup working (bucket-iam.md §2.2).
func TestLauncherGrantsCoverEveryLauncherWrite(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	for _, p := range []string{"fugaro/", "builds/", "cache/", "locks/"} {
		fake.DenyWrites("fugaro-runs-proj-1234", p)
	}
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	const slug, run = "acme-web-0123456789abcdef", "20261008-120000-abcd"
	s := runstore.Open(b.Bucket, slug, run)
	if err := s.CreateTask(ctx, &task.Spec{RunID: run, Repo: "acme/web"}); err != nil {
		t.Fatalf("task.json: %v", err)
	}
	if ok, _, err := s.Claim(ctx, "me", time.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	data, gen, err := b.Read(ctx, s.ClaimKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReplaceIf(ctx, s.ClaimKey(), data, gen, data); err != nil {
		t.Fatalf("claim takeover: %v", err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{}); err != nil {
		t.Fatalf("launch.json: %v", err)
	}
	if err := s.RequestCancel(ctx); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := token.DeleteObject(ctx, b, slug, run); err != nil {
		t.Fatalf("token cleanup: %v", err)
	}
	_, gen, _ = b.Read(ctx, s.ClaimKey())
	if err := b.DeleteIf(ctx, s.ClaimKey(), gen, nil); err != nil {
		t.Fatalf("claim release: %v", err)
	}
}
```

Adjust the `task.Spec` and `runstore.Launch` literals to the fields their constructors require (see `run.go`'s `CreateTask` and `WriteLaunch` calls). The test is about where each write lands, not about the content.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/blobx/ -run TestWriteForbiddenIsClassified && go test -race ./internal/cli/ -run TestLauncherGrantsCoverEveryLauncherWrite`
Expected: compile errors, `fake.DenyWrites undefined` and `b.Put undefined`.

- [ ] **Step 3: Implement the fake**

In `internal/gcpfake/gcs.go`:
- add a field `denied map[string][]string` (bucket to prefixes) to `GCS`, and initialise it in `NewGCS`;
- add the method below;
- call `g.deniedWrite(w, bucket, name)` first in `store` (which multipart and resumable uploads reach), `delete` and `patch`, returning when it answers.

Update the doc comment's list of differences: "DenyWrites answers 403 for a write under a prefix, as a conditional grant would; reads and listings are never denied".

```go
// DenyWrites makes every create, overwrite, delete and metadata patch of an
// object under prefix in bucket answer 403, as GCS does for a principal
// whose write grant is conditioned elsewhere (bucket-iam.md). Reads and
// listings are not affected.
func (g *GCS) DenyWrites(bucket, prefix string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied[bucket] = append(g.denied[bucket], prefix)
}

// deniedWrite answers 403 and reports true when name is under a denied prefix.
func (g *GCS) deniedWrite(w http.ResponseWriter, bucket, name string) bool {
	for _, p := range g.denied[bucket] {
		if strings.HasPrefix(name, p) {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "caller does not have storage.objects.create access to the Google Cloud Storage object.")
			return true
		}
	}
	return false
}
```

`store` has no `ResponseWriter` path for resumable sessions before finalization. Deny in `store` only; a resumable upload is then refused at finalization, which is when GCS checks it too.

- [ ] **Step 4: Implement `blobx`**

In `internal/blobx/blobx.go`:

```go
	// ErrForbidden means GCS refused a write or delete for lack of
	// permission (HTTP 403). gocloud reports a 403 as NotFound, so without
	// it a refused write would read as a missing object
	// (docs/design/bucket-iam.md §2.3).
	ErrForbidden = errors.New("permission denied")
```

In `write`, after the `FailedPrecondition` branch:

```go
		if isForbidden(err) {
			return 0, fmt.Errorf("%w: %w", ErrForbidden, err)
		}
```

In `DeleteIf`'s and `DeleteExisting`'s GCS branches, before the final `return err`:

```go
		if errors.As(err, &ae) && ae.Code == http.StatusForbidden {
			return fmt.Errorf("%w: %w", ErrForbidden, err)
		}
```

Add:

```go
// Put writes key whatever is there (last writer wins). A 403 is
// ErrForbidden, as for the conditional writes.
func (b *Bucket) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := b.write(ctx, key, data, contentType, false, nil)
	return err
}
```

On file:// and mem://, `ReplaceIf` and `DeleteIf` read first. A denied read is not in scope: the fake denies writes only.

- [ ] **Step 5: Run them and see them pass**

Run: `go test ./internal/blobx/... ./internal/gcpfake/... && go test -race ./internal/cli/ -run TestLauncherGrantsCoverEveryLauncherWrite`
Expected: `ok`.

**Mutation-proof:**
- removing the `write` branch fails "create", "replace" and "put";
- removing either delete branch fails its row;
- a `DenyWrites` that also denied `get` would fail the launcher test's `Read` of the claim. That is why reads stay open.

- [ ] **Step 6: Commit**

```bash
git add internal/blobx internal/gcpfake/gcs.go internal/cli/launcher_writes_test.go
git commit -m "bucket iam task 5: a refused write is blobx.ErrForbidden; launchers' writes all land under runs/"
```

---

### Task 6: One refusal for an operator-only write (`internal/cli/operator_write.go`)

**Files:**
- Create: `internal/cli/operator_write.go`, `internal/cli/operator_write_test.go`
- Modify: `internal/cli/recipes.go`, `sharedcfg.go`, `init.go`, `imagecheck.go`, `config_publish.go`, `layer_copy.go`

**Interfaces:**
- Consumes: `blobx.ErrForbidden`, `blobx.(*Bucket).Put` (Task 5), `isAccessDenied`, `userErr`, `remote`.
- Produces:
  ```go
  func operatorWriteErr(url, key string, err error) error // nil for nil; a user error for a 403; err unchanged otherwise
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/operator_write_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func TestOperatorWriteErr(t *testing.T) {
	if operatorWriteErr("gs://b", "k", nil) != nil {
		t.Fatal("nil is not nil")
	}
	other := errors.New("boom")
	if got := operatorWriteErr("gs://b", "k", other); got != other {
		t.Fatalf("a non-403 changed: %v", got)
	}
	err := operatorWriteErr("gs://fugaro-runs-acme", "fugaro/recipes/review.yaml", fmt.Errorf("%w: x", blobx.ErrForbidden))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d", ExitCode(err))
	}
	for _, want := range []string{"fugaro/recipes/review.yaml", "gs://fugaro-runs-acme", "operator role", "since 0.7.0", "fugaro init --operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
}

// A launcher's recipes publish shows the diff, then is refused with the
// operator text and exit 1; nothing is written.
func TestPublishAsLauncherIsRefusedWithTheOperatorText(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "fugaro/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	var w bytes.Buffer
	_, err := publishRecipe(ctx, &w, b, "fugaro/recipes/review.yaml", "review", []byte("version: 1\n"))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if _, _, rerr := b.Read(ctx, "fugaro/recipes/review.yaml"); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("something was written: %v", rerr)
	}
}
```

Add the same shape of test for `publishLayer` (`config_publish_test.go`, with a valid layer from that file's fixtures) and for `--publish-config` (`executeStdin(t, "", "init", "--publish-config")` on a rig whose GCS denies `fugaro/`: exit 1 and the operator text). For the fan-out, extend layered-config Task 13's fan-out test: with `builds/` denied, the first copy's failure stops the fan-out with the operator text, and no later copy is attempted.

- [ ] **Step 2: Run them and see them fail**

Run: `go test -race ./internal/cli/ -run 'TestOperatorWriteErr|TestPublishAsLauncher|TestPublishConfig|TestConfigPublish'`
Expected: a compile error, `undefined: operatorWriteErr`.

- [ ] **Step 3: Implement**

Create `internal/cli/operator_write.go`:

```go
package cli

import (
	"errors"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// operatorWriteErr is the refusal of a write to the runs bucket that only
// operators may make (docs/design/bucket-iam.md H8): since 0.7.0 launchers
// read the whole bucket but write only runs/. A 403 is a user error (exit 1)
// naming the object, the bucket and what to do; any other error is returned
// unchanged, for the caller's own wrapping.
func operatorWriteErr(url, key string, err error) error {
	if err == nil || !(errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)) {
		return err
	}
	return userErr("nothing was published: writing %s in %s needs the operator role (since 0.7.0 launchers read the runs bucket but write only runs/); ask an operator to publish it, or to add you with fugaro init --operator", key, url)
}
```

Wire it in. Each change wraps only the write's error, before the caller's existing `remote(...)`:

- **`recipes.go` `publishRecipe`.** In the final `switch`, add before `case err != nil:`:

  ```go
  	case errors.Is(err, blobx.ErrForbidden):
  		return 0, operatorWriteErr("gs://"+b.GCSName, key, err)
  ```
- **`sharedcfg.go` `publishSharedWarn`.** Replace `b.Bucket.WriteAll(ctx, infra.SharedConfigObject, data, &blob.WriterOptions{ContentType: "application/yaml"})` with `b.Put(ctx, infra.SharedConfigObject, data, "application/yaml")`, and return `operatorWriteErr(lc.BucketURL(), infra.SharedConfigObject, err)` on failure. `init --publish-config` already returns `remote(err)` for it: change that to `if ExitCode(err) == ExitUserError { return err }; return remote(err)`. `publishSharedConfig`'s warning keeps warning, now with the operator text.
- **`config_publish.go` `publishLayer`** and **`layer_copy.go` `writeLayerCopy`.** The same as `publishRecipe`. In `fanOutLayer`, a copy whose error `errors.Is(err, blobx.ErrForbidden)` stops the loop and returns that refusal: every later copy would be refused the same way.
- **`imagecheck.go` `writeState`.** Wrap with `operatorWriteErr("gs://"+bucket.GCSName, key, err)`. The check job runs as the build account, which may write `builds/<slug>/`, so only a person sees it.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/cli/ -run 'TestOperatorWriteErr|TestPublishAsLauncher|TestRecipes|TestPublish|TestConfigPublish|TestImageCheck|TestShared'`
Expected: `ok`.

**Mutation-proof:**
- dropping the `ErrForbidden` case in `publishRecipe` turns the refusal into `remote` (exit 2), which fails the exit-code check;
- keeping `WriteAll` in `publishSharedWarn` loses the classification (V8). The `--publish-config` test pins it only if the gocloud error unwraps to `*googleapi.Error`, so the test asserts the operator text, which only `operatorWriteErr` writes.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "bucket iam task 6: one operator-role refusal for publishes a launcher can no longer make"
```

---

### Task 7: `fugaro doctor`'s `runs-bucket-iam` check

**Files:**
- Modify: `internal/infra/hardening.go`, `internal/infra/hardening_test.go`, `internal/gcpfake/gcs.go`, `internal/cli/doctor.go`
- Create: `internal/cli/doctor_bucket.go`, `internal/cli/doctor_bucket_test.go`

**Interfaces:**
- Consumes: `gcp.LauncherBucketCondition`, `localcfg.Config.Terraform.{Launchers,Operators}`, `lc.User`, `doctorAPIOpts`, `isAccessDenied`, `pluginwire.Printable`, `shellword.Quote`.
- Produces:
  ```go
  // package infra
  type BucketFinding struct{ Severity, Member, Role, Problem, Fix string }
  func RunsBucketFindings(p *storage.Policy, bucket string, launchers, operators []string) []BucketFinding
  func ProjectStorageWriters(p *crm.Policy, launchers, operators []string) []BucketFinding
  // package gcpfake
  func (g *GCS) RefuseBucketPolicy(bucket string)
  func (g *GCS) SetHeldPermissions(bucket string, perms ...string)
  // package cli
  func doctorRunsBucketIAM(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck
  ```

- [ ] **Step 1: Write the failing pure tests**

Append to `internal/infra/hardening_test.go`:

```go
func TestRunsBucketFindings(t *testing.T) {
	const b = "fugaro-runs-proj-1234"
	const a, o = "user:a@example.com", "user:o@example.com"
	launcherCond := &storage.Expr{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(b)}
	hardened := []*storage.PolicyBindings{
		{Role: "roles/storage.objectAdmin", Members: []string{o}},
		{Role: "roles/storage.objectViewer", Members: []string{a, "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"}},
		{Role: "roles/storage.objectUser", Members: []string{a}, Condition: launcherCond},
		{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:fugaro-acme-web-1a2b3c4d@proj-1234.iam.gserviceaccount.com"},
			Condition: &storage.Expr{Title: "fugaro-x", Expression: gcp.BucketCondition(b, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef")}},
		{Role: "roles/storage.legacyBucketOwner", Members: []string{"projectOwner:proj-1234", "projectEditor:proj-1234"}},
	}
	sev := func(fs []BucketFinding, s string) (n int) {
		for _, f := range fs {
			if f.Severity == s {
				n++
			}
		}
		return n
	}
	t.Run("hardened", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, []string{a, o}, []string{o})
		if sev(fs, "warning") != 0 || sev(fs, "info") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("not hardened: a launcher with objectAdmin and no launcher grants", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{{Role: "roles/storage.objectAdmin", Members: []string{a, o}}}}
		fs := RunsBucketFindings(p, b, []string{a, o}, []string{o})
		if sev(fs, "warning") != 2 { // the objectAdmin and the missing grants
			t.Fatalf("findings %+v", fs)
		}
		for _, f := range fs {
			if f.Severity == "warning" && !strings.Contains(f.Fix, "fugaro init") {
				t.Errorf("a launcher's fix is not fugaro init: %+v", f)
			}
		}
	})
	t.Run("a stranger with a conditional write on fugaro/", func(t *testing.T) {
		p := &storage.Policy{Bindings: append(slices.Clone(hardened), &storage.PolicyBindings{Role: "roles/storage.objectUser", Members: []string{"user:x@example.com"},
			Condition: &storage.Expr{Title: "t", Expression: `resource.name.startsWith("projects/_/buckets/` + b + `/objects/fugaro/")`}})}
		fs := RunsBucketFindings(p, b, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
		for _, f := range fs {
			if f.Severity == "warning" && !strings.Contains(f.Fix, "remove-iam-policy-binding") {
				t.Errorf("no remove command: %+v", f)
			}
		}
	})
	t.Run("a custom role is flagged as cannot tell", func(t *testing.T) {
		p := &storage.Policy{Bindings: append(slices.Clone(hardened), &storage.PolicyBindings{Role: "projects/proj-1234/roles/mine", Members: []string{"user:y@example.com"}})}
		fs := RunsBucketFindings(p, b, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("lists unknown: unconditioned writers are info", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, nil, nil)
		if sev(fs, "warning") != 0 {
			t.Fatalf("findings %+v", fs)
		}
	})
}

func TestProjectStorageWriters(t *testing.T) {
	p := &crm.Policy{Bindings: []*crm.Binding{
		{Role: "roles/editor", Members: []string{"user:a@example.com", "user:o@example.com"}},
		{Role: "roles/storage.objectViewer", Members: []string{"user:b@example.com"}},
	}}
	fs := ProjectStorageWriters(p, []string{"user:a@example.com", "user:b@example.com", "user:o@example.com"}, []string{"user:o@example.com"})
	if len(fs) != 1 || fs[0].Member != "user:a@example.com" || fs[0].Role != "roles/editor" || fs[0].Severity != "warning" {
		t.Fatalf("findings %+v", fs)
	}
}
```

Imports: `storage "google.golang.org/api/storage/v1"`, `crm "google.golang.org/api/cloudresourcemanager/v1"`, and `internal/backend/gcp`.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/infra/ -run 'TestRunsBucketFindings|TestProjectStorageWriters'`
Expected: a compile error, `undefined: RunsBucketFindings`.

- [ ] **Step 3: Implement the pure readers**

Append to `internal/infra/hardening.go`:

```go
// BucketFinding is one thing doctor reports about who can write the runs
// bucket (docs/design/bucket-iam.md H9).
type BucketFinding struct{ Severity, Member, Role, Problem, Fix string }

// bucketWriteRoles are the predefined roles that write objects.
var bucketWriteRoles = []string{"roles/storage.objectAdmin", "roles/storage.objectUser", "roles/storage.objectCreator",
	"roles/storage.legacyBucketWriter", "roles/storage.legacyBucketOwner", "roles/storage.admin"}

// projectWriteRoles are the project-level roles that write every bucket's objects.
var projectWriteRoles = []string{"roles/owner", "roles/editor", "roles/storage.admin", "roles/storage.objectAdmin",
	"roles/storage.objectUser", "roles/storage.objectCreator"}

var fugaroAccountMember = regexp.MustCompile(`^serviceAccount:fugaro-[a-z0-9-]+@[a-z0-9-]+\.iam\.gserviceaccount\.com$`)

func customRole(r string) bool { return strings.HasPrefix(r, "projects/") || strings.HasPrefix(r, "organizations/") }

// RunsBucketFindings reads the runs bucket's policy against the 0.7.0 end
// state. With launchers and operators unknown (both empty), an unconditioned
// writer is info, never a warning.
func RunsBucketFindings(p *storage.Policy, bucket string, launchers, operators []string) []BucketFinding {
	known := len(launchers)+len(operators) > 0
	launcherOnly := func(m string) bool { return slices.Contains(launchers, m) && !slices.Contains(operators, m) }
	removeCmd := func(m, r string) string {
		return "gcloud storage buckets remove-iam-policy-binding gs://" + bucket + " --member=" + shellword.Quote(m) + " --role=" + r
	}
	var out []BucketFinding
	reader, writer := map[string]bool{}, map[string]bool{}
	conventionNoted := false
	for _, bd := range p.Bindings {
		write := slices.Contains(bucketWriteRoles, bd.Role) || customRole(bd.Role)
		for _, m := range bd.Members {
			pm := pluginwire.Printable(m)
			switch {
			case bd.Role == "roles/storage.objectViewer" && bd.Condition == nil:
				reader[m] = true
			case !write:
			case strings.HasPrefix(m, "projectOwner:") || strings.HasPrefix(m, "projectEditor:"):
				if !conventionNoted {
					conventionNoted = true
					out = append(out, BucketFinding{Severity: "info", Member: pm, Role: bd.Role,
						Problem: "project Owners and Editors can write fugaro/ (GCS's convenience binding, and their project roles): treat them as operators"})
				}
			case bd.Condition != nil && bd.Role == "roles/storage.objectUser" && bd.Condition.Title == gcp.LauncherBucketConditionTitle &&
				bd.Condition.Expression == gcp.LauncherBucketCondition(bucket) && bd.Condition.Description == "":
				writer[m] = true
			case bd.Condition != nil && fugaroAccountMember.MatchString(m) && bd.Role == "roles/storage.objectUser":
				// A job or build account under its own prefixes (the guard checks the shape).
			case bd.Condition != nil:
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds " + bd.Role + " on gs://" + bucket + " under a condition that is not the launchers' runs/ condition",
					Fix:     removeCmd(m, bd.Role) + " --all"})
			case slices.Contains(operators, m) && bd.Role == "roles/storage.objectAdmin":
			case customRole(bd.Role):
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds the custom role " + bd.Role + " on gs://" + bucket + ": doctor cannot tell whether it writes fugaro/",
					Fix:     "review the role, or " + removeCmd(m, bd.Role)})
			case !known || strings.HasPrefix(m, "serviceAccount:"):
				out = append(out, BucketFinding{Severity: "info", Member: pm, Role: bd.Role,
					Problem: pm + " can write anywhere in gs://" + bucket + " (" + bd.Role + "): expected only for an operator"})
			case launcherOnly(m):
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: "launcher " + pm + " can write anywhere in gs://" + bucket + " (" + bd.Role + "): the installation predates the 0.7.0 bucket hardening",
					Fix:     "an operator runs fugaro init (0.7.0 or later), which replaces the grant"})
			default:
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " is neither a launcher nor an operator and can write anywhere in gs://" + bucket + " (" + bd.Role + ")",
					Fix:     removeCmd(m, bd.Role)})
			}
		}
	}
	for _, m := range launchers {
		if launcherOnly(m) && (!reader[m] || !writer[m]) {
			out = append(out, BucketFinding{Severity: "warning", Member: pluginwire.Printable(m),
				Problem: "launcher " + pluginwire.Printable(m) + " lacks the 0.7.0 runs bucket grants (read everything, write runs/)",
				Fix:     "an operator runs fugaro init (0.7.0 or later)"})
		}
	}
	return out
}

// ProjectStorageWriters lists the launchers (not operators) whose project
// roles write every bucket, which the bucket's policy cannot bind (H12).
// Groups are not expanded.
func ProjectStorageWriters(p *crm.Policy, launchers, operators []string) []BucketFinding {
	var out []BucketFinding
	for _, bd := range p.Bindings {
		if !slices.Contains(projectWriteRoles, bd.Role) {
			continue
		}
		for _, m := range bd.Members {
			if slices.Contains(launchers, m) && !slices.Contains(operators, m) {
				pm := pluginwire.Printable(m)
				out = append(out, BucketFinding{Severity: "warning", Member: m, Role: bd.Role,
					Problem: "launcher " + pm + " holds " + bd.Role + " on the project, which writes fugaro/ whatever the bucket's policy says",
					Fix:     "make " + pm + " an operator (fugaro init --operator), or remove the project role"})
			}
		}
	}
	return out
}
```

`Member` holds the raw member for `ProjectStorageWriters` (the test compares it) and the printable form in `RunsBucketFindings`. The doctor text uses `Problem`/`Fix`, which are printable either way. Imports: `regexp`, `storage "google.golang.org/api/storage/v1"`, `crm "google.golang.org/api/cloudresourcemanager/v1"`, `internal/backend/gcp`, `internal/shellword` (check that `internal/infra` may import it, as for `pluginwire` in Task 4).

Run: `go test ./internal/infra/ -run 'TestRunsBucketFindings|TestProjectStorageWriters'`
Expected: `ok`.

- [ ] **Step 4: Write the failing doctor tests**

In `internal/gcpfake/gcs.go`, add `refusedPolicy map[string]bool` and `held map[string][]string` to `GCS`, initialised in `NewGCS`, and the two methods below. In `handle`'s object-prefix case, before the `rest == "iam"` GET, route `rest == "iam/testPermissions" && r.Method == http.MethodGet` to `g.testPermissions(w, bucket, q["permissions"])`. In `bucketGet` with `iam`, answer 403 when `g.refusedPolicy[bucket]`.

```go
// RefuseBucketPolicy makes bucket's getIamPolicy answer 403, as it does for
// a caller without storage.buckets.getIamPolicy (a launcher, or an operator).
func (g *GCS) RefuseBucketPolicy(bucket string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refusedPolicy[bucket] = true
}

// SetHeldPermissions sets what testIamPermissions on bucket reports the
// caller holds (by default nothing).
func (g *GCS) SetHeldPermissions(bucket string, perms ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held[bucket] = perms
}

func (g *GCS) testPermissions(w http.ResponseWriter, bucket string, asked []string) {
	var got []string
	for _, p := range asked {
		if slices.Contains(g.held[bucket], p) {
			got = append(got, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "storage#testIamPermissionsResponse", "permissions": got})
}
```

Create `internal/cli/doctor_bucket_test.go`:

```go
package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const dbBucket = "fugaro-runs-proj-1234"

// withRunsBucket gives the doctor rig a storage endpoint holding the runs
// bucket with policy, and the local config's lists and user.
func (r *doctorRig) withRunsBucket(t *testing.T, g *gcpfake.GCS, extra string, policy ...gcpfake.Binding) {
	t.Helper()
	g.AddBucket(dbBucket, 123456789012, map[string]string{"fugaro": "managed"})
	g.SetBucketPolicy(dbBucket, policy)
	cfg := strings.Replace(r.cfg, "endpoints: { ", "endpoints: { storage: "+g.URL+"/storage/v1/, ", 1) + extra
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hardenedPolicy() []gcpfake.Binding {
	return []gcpfake.Binding{
		{Role: "roles/storage.objectAdmin", Members: []string{"user:o@example.com"}},
		{Role: "roles/storage.objectViewer", Members: []string{"user:a@example.com"}},
		{Role: "roles/storage.objectUser", Members: []string{"user:a@example.com"},
			Condition: &gcpfake.IAMCondition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(dbBucket)}},
	}
}

const dbLists = "terraform: { launchers: [\"user:a@example.com\", \"user:o@example.com\"], operators: [\"user:o@example.com\"] }\n"

func TestDoctorRunsBucketIAMHardenedIsOK(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	o := r.runDoctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || !c.OK {
		t.Fatalf("check = %+v (%v)", c, ok)
	}
}

func TestDoctorRunsBucketIAMOldGrantWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, gcpfake.Binding{Role: "roles/storage.objectAdmin", Members: []string{"user:a@example.com", "user:o@example.com"}})
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" && strings.Contains(c.Fix, "fugaro init") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning: %+v", o.Checks)
	}
	if !o.OK {
		t.Fatal("a warning failed doctor without --strict")
	}
}

func TestDoctorRunsBucketIAMSelfTest(t *testing.T) {
	for _, tc := range []struct {
		name, user string
		held       []string
		sev        string
		ok         bool
	}{
		{"launcher, hardened", "a@example.com", nil, "", true},
		{"operator holds writes", "o@example.com", []string{"storage.objects.create", "storage.objects.delete"}, "", true},
		{"launcher holds writes", "a@example.com", []string{"storage.objects.create"}, "warning", false},
		{"unknown caller holds writes", "", []string{"storage.objects.create"}, "info", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, g := newDoctorRig(t), gcpfake.NewGCS(t)
			cfg := strings.Replace(r.cfg, "user: someone@example.com\n", "", 1)
			if tc.user != "" {
				cfg += "user: " + tc.user + "\n"
			}
			r.cfg = cfg
			r.withRunsBucket(t, g, dbLists)
			g.RefuseBucketPolicy(dbBucket)
			g.SetHeldPermissions(dbBucket, tc.held...)
			o := r.runDoctorJSON(t)
			c, found := doctorCheckByID(o.Checks, "runs-bucket-iam")
			if !found || c.OK != tc.ok || c.Severity != tc.sev {
				t.Fatalf("check = %+v (found %v)", c, found)
			}
		})
	}
}

// A test configuration (no_auth with no storage endpoint) reads no bucket.
func TestDoctorRunsBucketIAMSkippedWithoutAStorageEndpoint(t *testing.T) {
	r := newDoctorRig(t)
	o := r.runDoctorJSON(t)
	if _, found := doctorCheckByID(o.Checks, "runs-bucket-iam"); found {
		t.Fatal("checked a bucket under a test configuration")
	}
}
```

`runDoctorJSON` is the rig's existing JSON runner. If the rig names it differently, use that, and look at `signerRig.doctor` for the shape. `GCS.SetBucketPolicy` exists (`gcs.go`), and `gcpfake.IAMCondition` is the binding condition type of `iam.go`.

- [ ] **Step 5: Run them and see them fail**

Run: `go test -race ./internal/cli/ -run 'TestDoctorRunsBucketIAM'`
Expected: no `runs-bucket-iam` check is found (the skip test passes already).

- [ ] **Step 6: Implement the check**

Create `internal/cli/doctor_bucket.go`:

```go
package cli

import (
	"context"
	"slices"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctorRunsBucketIAM is the runs-bucket-iam check (docs/design/bucket-iam.md
// H9): who can write the runs bucket. Read-only; every failure to read is a
// warning, never an error. A test configuration (no_auth with no storage
// endpoint) is skipped, so no real bucket is ever read from one.
func doctorRunsBucketIAM(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Storage == "" {
		return nil
	}
	bucket := lc.RunsBucketName()
	if bucket == "" {
		return nil
	}
	unchecked := func(err error) []doctorCheck {
		return []doctorCheck{{ID: "runs-bucket-iam", Severity: "warning",
			Problem: "could not check who can write gs://" + bucket + ": " + oneLineCLI(err.Error()),
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"}}
	}
	svc, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return unchecked(err)
	}
	launchers, operators := lc.Terraform.Launchers, lc.Terraform.Operators
	p, err := svc.Buckets.GetIamPolicy(bucket).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	switch {
	case err == nil:
		fs := infra.RunsBucketFindings(p, bucket, launchers, operators)
		if len(launchers) > 0 && c != nil {
			req := &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}
			if pp, perr := c.Projects.GetIamPolicy(lc.GCPProject, req).Context(ctx).Do(); perr == nil {
				fs = append(fs, infra.ProjectStorageWriters(pp, launchers, operators)...)
			}
		}
		return findingChecks(fs)
	case isAccessDenied(err):
		r, terr := svc.Buckets.TestIamPermissions(bucket, []string{"storage.objects.create", "storage.objects.delete"}).Context(ctx).Do()
		if terr != nil {
			return unchecked(terr)
		}
		return []doctorCheck{selfCheck(bucket, len(r.Permissions) > 0, lc.User, launchers, operators)}
	default:
		return unchecked(err)
	}
}

// selfCheck places the caller (only by the local config's self-asserted
// user) and says what its unconditioned write access means.
func selfCheck(bucket string, held bool, user string, launchers, operators []string) doctorCheck {
	if !held {
		return doctorCheck{ID: "runs-bucket-iam", OK: true}
	}
	m := "user:" + user
	const text = "your credentials can write anywhere in gs://%s: this installation predates the 0.7.0 bucket hardening"
	switch {
	case user != "" && slices.Contains(operators, m):
		return doctorCheck{ID: "runs-bucket-iam", OK: true}
	case user != "" && slices.Contains(launchers, m):
		return doctorCheck{ID: "runs-bucket-iam", Severity: "warning", Problem: fmt.Sprintf(text, bucket),
			Fix: "ask an operator to run fugaro init (0.7.0 or later)"}
	default:
		return doctorCheck{ID: "runs-bucket-iam", Severity: "info", Problem: "expected for an operator; otherwise " + fmt.Sprintf(text, bucket),
			Fix: "if you only launch runs, ask an operator to run fugaro init (0.7.0 or later)"}
	}
}

// findingChecks are the findings as doctor checks: one per finding, IDs
// runs-bucket-iam-1.., plus the runs-bucket-iam summary, OK when no finding
// is a warning.
func findingChecks(fs []infra.BucketFinding) []doctorCheck {
	sum := doctorCheck{ID: "runs-bucket-iam", OK: true}
	var out []doctorCheck
	for i, f := range fs {
		if f.Severity == "warning" {
			sum.OK, sum.Severity, sum.Problem = false, "warning", "someone other than the operators can write fugaro/ (see the runs-bucket-iam-N lines)"
		}
		out = append(out, doctorCheck{ID: fmt.Sprintf("runs-bucket-iam-%d", i+1), Severity: f.Severity,
			Problem: pluginwire.Printable(f.Problem), Fix: f.Fix})
	}
	return append([]doctorCheck{sum}, out...)
}
```

Add `fmt` to the imports. In `doctor.go`, right after the `preflight.IAMPolicy` loop, add:

```go
	o.Checks = append(o.Checks, doctorRunsBucketIAM(ctx, lc, crmSvc)...)
```

- [ ] **Step 7: Run them and see them pass, with the old doctor suites**

Run: `go test -race ./internal/cli/ -run 'TestDoctor'`
Expected: `ok`. The existing doctor tests have no storage endpoint, so they are unchanged (`TestDoctorRunsBucketIAMSkippedWithoutAStorageEndpoint` is the pin).

**Mutation-proof:**
- inverting `held` fails "launcher, hardened";
- dropping the operator case of `selfCheck` fails "operator holds writes";
- dropping the `NoAuth && Storage == ""` skip makes every existing doctor test try a real endpoint, and the skip test fails;
- a `findingChecks` whose summary ignores warnings fails `TestDoctorRunsBucketIAMOldGrantWarns`.

- [ ] **Step 8: Commit**

```bash
git add internal/infra/hardening.go internal/infra/hardening_test.go internal/gcpfake/gcs.go internal/cli/doctor.go internal/cli/doctor_bucket.go internal/cli/doctor_bucket_test.go
git commit -m "bucket iam task 7: fugaro doctor reports who can write the runs bucket"
```

---

### Task 8: Docs

**Files:**
- Modify:
  - `docs/gcp-setup.md`, `docs/recipes.md`, `docs/release.md`;
  - `docs/design/v1.md`, `docs/design/shared-config.md`, `docs/design/watch-queued.md`, `docs/design/layered-config.md`.
- Create: `internal/cli/docs_bucket_iam_test.go`

- [ ] **Step 1: Write the failing docs test**

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Since 0.7.0 launchers hold objectViewer and a runs/-conditioned objectUser
// on the runs bucket, never objectAdmin (docs/design/bucket-iam.md). The
// sentences that said otherwise are gone from every doc but the hardening's
// own design, the plans and the release notes (not scanned).
func TestDocsLaunchersDoNotHoldObjectAdmin(t *testing.T) {
	stale := []string{
		"Launchers and operators already hold `objectAdmin`",
		"launchers and operators already hold `roles/storage.objectAdmin`",
		"A launcher also gets `roles/storage.objectAdmin`",
		"`fugaroJobRunner` on each workflow job (§3.4), `roles/storage.objectAdmin`",
		"`roles/storage.objectAdmin` on it only to launchers",
		"Anyone holding `roles/storage.objectAdmin` on the runs bucket: launchers and operators",
		"The bucket stays writable by launchers until the hardening lands",
	}
	var files []string
	for _, g := range []string{"../../docs/*.md", "../../docs/design/*.md", "../../README.md", "../../plugin/skills/*/SKILL.md", "../../plugin/skills/*/reference/*.md"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "bucket-iam.md") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stale {
			if strings.Contains(string(data), s) {
				t.Errorf("%s still says %q", f, s)
			}
		}
	}
	gs, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"write only `runs/`", "runs-bucket-iam", "add-iam-policy-binding", "DATA_WRITE"} {
		if !strings.Contains(string(gs), want) {
			t.Errorf("docs/gcp-setup.md lacks %q", want)
		}
	}
}
```

Run: `go test ./internal/cli/ -run TestDocsLaunchersDoNotHoldObjectAdmin`
Expected: failures for `v1.md` (two sentences), `watch-queued.md`, `shared-config.md`, `layered-config.md` (two) and `gcp-setup.md` (one stale sentence, four missing strings). Each stale string was copied from the docs at 65a6a24: if a doc reworded one since, update the list to the current wording first, so the test fails for the right reason.

- [ ] **Step 2: Edit the docs**

- **`docs/gcp-setup.md`.** In "Launchers and operators" (the `--launcher`/`--operator` paragraph), add a "What each role reads and writes in the runs bucket" table: design §3's table, with these rows:
  - operators: everything;
  - launchers: read everything, write only `runs/`;
  - job, build and history accounts.

  Follow it with four short paragraphs:
  - the project-level writers (H12: Owners, Editors and project storage roles are operators in effect; list them as operators);
  - doctor's `runs-bucket-iam` lines and what each fix means;
  - rollback (H10), with the one `gcloud storage buckets add-iam-policy-binding gs://fugaro-runs-<p> --member=user:<m> --role=roles/storage.objectAdmin` line;
  - the optional `DATA_WRITE` audit logs (H13): `gcloud projects get-iam-policy <p> --format=json > policy.json`, add an `auditConfigs` entry for `storage.googleapis.com` with `DATA_WRITE`, then `gcloud projects set-iam-policy <p> policy.json`, with the note that it logs every object write (volume and cost).

  In "The shared file", replace "Launchers and operators already hold `objectAdmin` on that bucket, so reading it needs no new grant." with "Launchers and operators can read it (launchers read the whole runs bucket, and write only `runs/`); only operators can write it." In the shared-config error table, add a row: `needs the operator role` → "a launcher ran a publish; an operator runs it".
- **`docs/recipes.md`:** `fugaro recipes publish` is an operator command, and a launcher gets the operator-role refusal.
- **`docs/release.md`:** in the rollout section, the 0.7.0 steps 5 and 6 of the design's §8.
- **`docs/design/v1.md`.** On line 162 and in the "Who can do what" launchers bullet, replace `roles/storage.objectAdmin` on the runs bucket with "`roles/storage.objectViewer` on the runs bucket and `roles/storage.objectUser` on its `runs/` only (since 0.7.0, [bucket-iam.md](bucket-iam.md))". For operators, say that they get `roles/storage.objectAdmin` on the runs bucket, where today it is inherited from "everything launchers get".
- **`docs/design/shared-config.md`.** In §3's trust-level sentence and in §13's "A writer can relax policy" item, say "operators (and, before 0.7.0, launchers)".
- **`docs/design/watch-queued.md`.** Line 59 becomes "grants read access to launchers and operators, and write access to operators (launchers write only `runs/` since 0.7.0)".
- **`docs/design/layered-config.md` §11.** "Who can write the project layer" becomes: operators, and project-level storage writers; launchers could before 0.7.0 ([bucket-iam.md](bucket-iam.md)). Option A's cost cell becomes "Closed in 0.7.0 by the bucket hardening".

- [ ] **Step 3: Run the docs tests**

Run: `go test ./internal/cli/ -run 'TestDocs|TestDesignDoc' && go test ./internal/config/ -run TestScopeTableMatchesDocs && go test ./plugin/...`
Expected: `ok`.

**Mutation-proof:** restoring v1.md's old launcher bullet fails `TestDocsLaunchersDoNotHoldObjectAdmin`, naming the file and the sentence.

- [ ] **Step 4: Commit**

```bash
git add docs internal/cli/docs_bucket_iam_test.go
git commit -m "bucket iam task 8: docs say launchers read the runs bucket and write only runs/"
```

---

### Task 9: Check 32 in the live checklist (the user runs it)

**Files:**
- Modify: `docs/gcp-live-checklist.md`

- [ ] **Step 1: Add the check**

Before "## Not covered by these tests (manual)", add the following. If another plan merged a Check 32 first, use the next free number, and update the design's §11 and this plan's Task 12 to match.

```markdown
## Check 32: the runs bucket's launcher grants (installation belong, sandbox repository only, run by you; USER-RUN, NOT RUN)

**No step below has been run; nothing here is a claim that it works.** The 0.7.0 bucket hardening ([design/bucket-iam.md](design/bucket-iam.md)) gives launchers who are not operators `roles/storage.objectViewer` on the runs bucket and `roles/storage.objectUser` under `resource.name.startsWith("projects/_/buckets/<bucket>/objects/runs/")`. Items V1 to V5 and V8 of the design are unverified against real GCS.

**The owner's go-ahead first:** the apply in step 2 changes `belong`'s bucket policy for everyone (EdgeWeb's launchers included). Touch only the sandbox repository's `runs/` objects and `fugaro-live-*` objects. Never EdgeWeb's.

1. Record `fugaro version`, the date, and `gcloud storage buckets get-iam-policy gs://fugaro-runs-<p> --format=json` (the count of bindings with a `condition`: V3).
2. **⚠ CONFIRM** in your own terminal, add a second identity you control (a test Google account, or a service account you can impersonate) as a launcher only: `fugaro init --launcher <you> --launcher <test identity> --operator <you>`. Read the plan and the `bucket access (0.7.0)` banner, then type the name. Record the apply's output, and step 1's command again (V3: one new conditional binding, whatever the number of launchers).
3. As the test identity (`gcloud auth application-default login` with it, or `--impersonate-service-account`):
   - `gcloud storage cp` a file to `gs://fugaro-runs-<p>/runs/<sandbox slug>/fugaro-live-iam/x`, overwrite it, then remove it. All succeed (V1).
   - The same to `fugaro/fugaro-live-iam`, `builds/<sandbox slug>/fugaro-live-iam`, `cache/<sandbox slug>/fugaro-live-iam` and `locks/<sandbox slug>/fugaro-live-iam`. Each gets 403 (V1).
   - `gcloud storage ls gs://fugaro-runs-<p>/runs/` and `gcloud storage cat gs://fugaro-runs-<p>/fugaro/project.json` succeed (V1, list and get).
   - `fugaro run` a trivial task on the sandbox, then `fugaro cancel` it. Both work (V2: the claim's create-if-absent, its generation-matched release, the cancel marker, the token object).
   - `fugaro recipes publish` of a throwaway recipe named `fugaro-live-iam` is refused with "needs the operator role" and exit 1 (V8). Nothing is written.
   - `fugaro doctor`: `runs-bucket-iam` is ok ("writes only runs/") (V4).
4. As yourself (operator): `fugaro doctor` shows `runs-bucket-iam` ok. `gcloud storage buckets get-iam-policy` shows the launcher binding exactly as `gcp.LauncherBucketCondition` spells it.
5. **Propagation (V5).** Before step 2, as the test identity with the old grant, start a loop writing `fugaro/fugaro-live-iam` every 10 s. Record how long writes keep succeeding after the apply.

**FACT lines to record:**
- the version;
- the conditional-binding count before and after;
- each 403's exact text;
- the propagation time;
- the refusal text of step 3;
- doctor's lines.

**Restore.** Remove the test identity: `fugaro init --launcher <you> --operator <you>`, and read the plan (it deletes that member's two grants; pass `--allow-delete` for each, as the refusal prints). Then `gcloud storage rm` every `fugaro-live-iam` object.
```

- [ ] **Step 2: Commit**

```bash
git add docs/gcp-live-checklist.md
git commit -m "bucket iam task 9: Check 32, the launcher grants on real GCS (user-run)"
```

---

### Task 10: The full focused suites, then the PR

**Files:** none.

- [ ] **Step 1: Format, vet and build**

Run: `gofmt -l internal/ deploy/ && go vet ./... && go build ./...`
Expected: no output from `gofmt -l`, and `go vet` and `go build` are silent. If terraform is available: `terraform fmt -check -recursive deploy/terraform`.

- [ ] **Step 2: Run the touched packages in full, in the foreground**

Run, as separate foreground calls with `timeout: 600000` each:
- `go test -race -count=1 ./internal/backend/gcp/... ./internal/blobx/... ./internal/gcpfake/... ./internal/infra/... ./deploy/terraform/...`
- `go test -race -count=1 ./internal/cli/...`

Expected: `ok` for each package. If `./internal/cli/...` does not finish within one 10-minute call, split it into two `-run` halves (`-run '^Test[A-L]'` and `-run '^Test[M-Z]'`), both in the foreground. Never background it.

- [ ] **Step 3: Self-check against the design**

Run: `grep -rn 'roles/storage.objectAdmin' deploy/terraform/gcp/modules/installation/iam.tf`
Expected: two lines, the operators' `runs` grant and the state bucket's.

Run: `grep -rn 'resource.name.startsWith(\\"projects/_/buckets/%s/objects/runs/' internal/`
Expected: only `internal/backend/gcp/names.go`.

Run: `grep -rln 'WriteAll(' internal/cli/ | grep -v _test`
Expected: no file writes the runs bucket through gocloud's `WriteAll` directly. Every write goes through `blobx`, so a 403 is classified.

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin bucket-iam
gh pr create --base main --title "Runs bucket IAM hardening: only operators write fugaro/" --body-file <(cat <<'EOF'
Launchers who are not operators now read the runs bucket and write only runs/; operators keep objectAdmin (design: docs/design/bucket-iam.md, plan: docs/plans/2026-10-08-bucket-iam.md). Ships in 0.7.0.

- Terraform: `runs` (objectAdmin) for operators only; `runs_reader` (objectViewer) and `runs_launcher` (objectUser under `gcp.LauncherBucketCondition`) for launchers who are not operators. One-person installations plan no change.
- The install guard checks every bucket grant's condition (people: the launchers' exact condition; accounts: Fugaro prefix clauses).
- `fugaro init` allows exactly the deletes of launchers' old objectAdmin grants, behind a banner and the step's own confirmation.
- `blobx.ErrForbidden`; publishes a launcher can no longer make are refused with one operator-role message (exit 1).
- `fugaro doctor` gains `runs-bucket-iam`.

Not verified live: V1 to V5 and V8 (Check 32, user-run, sandbox repository on belong, after the owner's go-ahead).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_016CZnWCMtmmRStX7fK7AkCk
EOF
)
```

If Task 2's golden plans or `terraform test` could not run in the session, add a line saying so to the body.

- [ ] **Step 5: Read every CI check**

Run: `gh pr checks --watch` (foreground), then `gh run view <id> --log` for each of `test`, `terraform` and `rules`.
Expected: all pass. Read each job's log, not only the summary line: the `terraform` job runs the tftest of Task 2.

---

### Task 11 (after the merge): The 0.7.0 release notes

**Files:**
- Modify: `docs/releases/v0.7.0.md` (created by the base image plan's Task 22, or created here if this lands first)

- [ ] **Step 1: Write the section**

Add to the notes' list:

```markdown
- **Only operators write the project's configuration.** Launchers now read the runs bucket and write only `runs/`. The project layer, the shared config, recipes, build records, layer copies and caches are writable only by operators (and by people with project-level storage roles, such as Owners and Editors). `fugaro recipes publish`, `fugaro config publish` and `fugaro init --publish-config` run by a launcher are refused with "needs the operator role". `fugaro doctor` reports who can write the bucket (`runs-bucket-iam`). Launchers on an older CLI keep launching.
```

In `### For operators`, add these two steps after the base-image steps:

```markdown
5. One operator runs `fugaro init` in their own terminal. The plan replaces each launcher's `objectAdmin` grant on the runs bucket with read access plus write access to `runs/`. Read the `bucket access (0.7.0)` banner, which names those launchers, then type the project name. If every launcher is also an operator, the plan has no bucket change. Apply it when nobody is launching: a launch during the apply may need a retry.
6. Everyone runs `fugaro doctor`: `runs-bucket-iam` should be ok. To give one launcher the old access back: `gcloud storage buckets add-iam-policy-binding gs://fugaro-runs-<gcp-project> --member=user:<email> --role=roles/storage.objectAdmin` (doctor will flag it).
```

- [ ] **Step 2: Merge with the release notes PR**

The notes go into the same PR as the base image plan's release notes, before `/new-release 0.7.0` runs. That release's Highlights name this bullet. The user still owes Check 32.

---

### Task 12 (user-run, after `/new-release 0.7.0`): Check 32

**Files:** none (results go into `docs/gcp-live-checklist.md` in a follow-up PR).

- [ ] **Step 1: Ask the owner**

Ask the owner for:
- the go-ahead to change `belong`'s bucket policy, which affects EdgeWeb's launchers;
- a test identity.

Claude runs nothing in the cloud here. The user runs Check 32 at their own terminal.

- [ ] **Step 2: Record the results**

Paste the FACT lines under "Results" in `docs/gcp-live-checklist.md`, and mark Check 32 run. If V1 or V2 failed, **stop**: open an issue, and the design's H1 is reopened. If V3 shows the conditional-binding limit is near, add a note to `docs/gcp-setup.md`'s roles section with the real figure.
