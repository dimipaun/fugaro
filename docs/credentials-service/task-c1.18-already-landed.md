# Task C1.18 is already on the base: nothing was implemented

**A run was asked to implement Task C1.18 of [implementation-plan.md](implementation-plan.md)
("Terraform — the service, its SA, the grants moved"). The task's deliverable is
already on the base branch `integration-0.9.6`, so this pull request changes no
Terraform, no Go, no rules, no schema and no configuration: it adds this note,
and nothing else.**

## Why this note exists

The instructions for the run were explicit about the first step: *check whether
the task's deliverable is already on the base; if it is, change nothing, open a
docs-only PR note saying so with the evidence, and stop.* The check came back
positive, so that is what this is. Writing a second `credentials.tf`, a second
`credentials_service` variable or a second set of grants would be the failure
mode this first step exists to prevent — two definitions of one service, one
bundle container and one accessor grant, and no way for a reviewer to tell which
one an apply would use.

## The evidence

Everything below was read out of the base branch, not out of a working tree:
`git log`, `git show` and `git grep` against `origin/integration-0.9.6`.

**The commit is the task's own commit line, verbatim.**

```
$ git log --oneline origin/integration-0.9.6 -- deploy/terraform/gcp/modules/installation/credentials.tf
af24947e 703's leftovers: the gid's stale copy goes to Task 10, the reaper pin is
         named as it is, and the plan-only test stops proving nothing (#702 already
         landed) (#788)
aec4ec72 terraform: an opt-in credentials service in the installation root; its
         credentials are one bundle secret with one accessor grant (D146, D147, Q6) (#732)
```

`aec4ec72` is the C1.18 commit: its subject is the task's **Commit.** line
character for character, and it is an ancestor of `origin/integration-0.9.6`
(`git merge-base --is-ancestor aec4ec72 origin/integration-0.9.6` exits 0). It
landed as PR #732 on 2026-10-20. `af24947e` (#788) is later maintenance text
inside the file, not a second implementation.

**Every path the task's Files line names exists on the base, and each carries
what the task says it carries.**

| What C1.18 asks for | On the base |
|---|---|
| `deploy/terraform/gcp/modules/installation/credentials.tf` (**new**) | Present; gated on `local.credentials_service_on`, creates the service account, the one bundle container, the one accessor grant and the Cloud Run service |
| `modules/installation/variables.tf` — `credentials_service` opt-in, default off | Present; `default = null`, with the validation that refuses a tag-form image |
| `modules/workflow/sa.tf` — model accessors dropped only when the opt-in is on, plus the job SA's `run.invoker` | Present: `sa.tf` keeps `roles/secretmanager.secretAccessor` behind `var.credentials_service == null \|\| !…` and adds `google_cloud_run_v2_service_iam_member "credentials"` with `role = "roles/run.invoker"`, `count = var.credentials_service == null ? 0 : 1` |
| `modules/workflow/variables.tf`, `modules/repo/workflows.tf` — name and region passed down | Present; `modules/repo/workflows.tf` reads `credentials_service = var.installation.credentials_service` |
| `roots/installation/tests/installation.tftest.hcl` and the repo root's tests | Present at `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` and `…/roots/repo/tests/repo.tftest.hcl` (note the path: `roots/` sits under `deploy/terraform/gcp/`, and the root's own tests live in a `tests/` subdirectory) |
| The cover's cases in `internal/infra/tf/cover.go` | Present: `credentialsImageOK` is used five times in `cover.go` and asserted twice in `cover_test.go` |

**Every test the task's Tests line names is declared in the tree**, which is how
the plan's own guard (`internal/cli/docs_credentials_plan_tests_test.go`) would
read it:

```
$ git grep -l TestCoverRefusesACredentialsServiceImageOutsideThePin origin/integration-0.9.6
internal/infra/tf/cover_test.go
$ git grep -l TestCoverRefusesACredentialsServiceThatIsNotTheModules origin/integration-0.9.6
internal/infra/tf/cover_test.go
$ git grep -l TestAllowedTypesCoverTheModules origin/integration-0.9.6
internal/infra/tf/cover.go  internal/infra/tf/cover_test.go
```

The Terraform assertions the task names are in the two `*.tftest.hcl` files, by
their `run` blocks: `credentials_service_off_by_default`, `credentials_service_on`
(the installation root — the one bundle container, the one accessor grant on it,
no member over the project's other containers, no `condition` on the grant) and
`credentials_service_on_job`, `credentials_service_on_provider_keys`,
`credentials_service_off_job` (the repository root — the job account loses the
model-secret accessors, the mount *itself* is untouched, and it gains the
invoker grant).

**The security rules the task states are the ones the base enforces.**

- **The image is a digest and a tag is refused.** `variables.tf`'s validation
  matches `@sha256:` followed by 64 lower-case hex digits, and its
  `error_message` says why ("never a tag: an operator can push a tag").
- **One container, one accessor grant (Q6, Global Constraint 12).** Both live in
  `credentials.tf`; the file's own comment names
  `TestCredentialsServiceGrantsAreTheOneBundle` as what keeps that true, and
  that test is declared in `deploy/terraform/terraform_test.go`.
  `TestCredentialsServiceResourcesAreCounted` and
  `TestNoSecretVersionsInTerraform` are declared there too: the resource count
  is pinned in both directions, and Terraform manages no secret *version* —
  the document stays the owner-run C1.25 commands'.
- **Turning the opt-in off fails closed.** The bundle container carries
  `prevent_destroy`, so dropping the opt-in in one plan fails the plan instead
  of destroying every credential at once; the two owner steps that get around
  that are written into the plan text (M2) and are C1.22's runbook to carry.
- **The managed deployer never deploys the service (D146).** `managed_cover.go`
  drops both new types from a managed plan.

**The plan text on the base already records all of this.** The current
`### Task C1.18` section is the *stricter* version — the digest rule (M1), the
invoker grant stated per workflow variant (n4), the `prevent_destroy` opt-out
(M2), the cover's `credentialsImageOK` case and the two cover tests — and
`aec4ec72` is the commit that put it there. There is no amended or older
version of the task left to implement: the plan and the Terraform agree.

## What was not done, and why that is the whole point

- **No Terraform was written, planned or applied.** Nothing under
  `deploy/terraform/` is touched by this pull request. No live cloud action was
  taken or is proposed: `belong` (edge-devel-dimi, fugaro-belong,
  edgeappinc/\*) was not read, named in a command or touched in any way, and no
  credential value was read, printed, logged or handled.
- **No verification of the Terraform was run by this run**, either. The task's
  own Verification line names `terraform fmt -check -recursive`,
  `go test -tags terraform ./internal/infra/...` and `terraform test` in both
  roots; running them here would be verifying code this run did not write, on a
  base whose own CI (`ci.yml`'s terraform job) already owns that. The
  repository-wide verification this run is required to record *was* run, and its
  result is stated in the pull request body.

## One observation for the owner, offered and not acted on

`internal/cli/docs_credentials_plan_tests_test.go` holds a table of tasks whose
**Tests.** lists are checked against the tree, and it still lists only C0.6,
C1.2, C1.3 and C1.9 — C1.18's landing commit did not add C1.18's entry, so
C1.18's three cover tests are not held by that guard the way the other landed
tasks' lists are. The names do all resolve in the tree today (the `git grep`
above), so nothing is broken; the guard is simply narrower than its own comment
describes. **This note does not change it** — that edit belongs to whichever
task wants to extend the guard, and making it here would be a change this
run was told not to make.
