# Artifact runs: implementation plan

**Goal:** a Fugaro run whose deliverable is a file in the runs bucket instead of a pull request — `fugaro run --recipe sizzle --attach shot1.png --attach shot2.png` renders a video in the cloud, checks it with an `ffprobe`-shaped command the repository owns, uploads it to `runs/<slug>/<run>/artifacts/`, records its size, sha256 and content type in `result.json`, and leaves `fugaro artifacts get` as the way to take it back. **This PR (the one that adds this plan and its design doc) is docs-only: no Go, Terraform, schema or `fugaro.yaml` file under any "Files" list below is touched by it.**

**Spec:** [docs/design/artifact-runs.md](../design/artifact-runs.md). Its findings against the code (§1), its verified/unverified table (§12) and its seven decisions for the owner (D1–D7, §13) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable, and **Tasks 1–2 are unblocked by every D**; Tasks 3–8 assume the recommendations the design argues for (D1 repo-bound, D2 under the run prefix, D3 Cloud Run, D5 `mode: artifact`, D6 attachments in this slice) and are rewritten, not patched, if the owner vetoes one. **D4 (the timeout is the cap) and D7 (no signed URLs yet) are visible in this plan only as text** — Tasks 8's preview line and Task 9's SECURITY.md paragraph — and neither adds a mechanism.

**Depends on:** release 0.7.5 on `main` (the recipe gate `recipesV07Since`, `internal/cli/recipes_skew.go:123`, and `fugaro image build`/`image refresh` as they stand). Nothing here depends on [compute-backend.md](../design/compute-backend.md)'s unmerged tasks (its Task 3's price table is **not** on `main`; the substrate stays Cloud Run, D3) and nothing depends on project-centric phase 3 (D1). **Task 1 depends on no other task. Tasks 4 and 6 depend on Task 3's two methods** (`PutFile` for the upload, `GetFile` for the download and for `fugaro artifacts get`); **Task 7 depends on Task 4's record** (it lists and verifies what the record says) and on `GetFile`; Task 5 needs Task 4's staging directory.

**Checked before this plan was written:** every fact below was read from the code at this branch's base, and the design's §12 table carries the full list with its citations (V1–V23). The load-bearing ones for the edits here: the run prefix and the record names (`internal/runstore/runstore.go:386-387,492,502,547`); `blobx`'s write-all-bytes and `MaxReadBytes = 1<<20` (`internal/blobx/blobx.go:51-54,109-138,173-198,314`, V23); the recipe's mode and step kinds (`internal/recipe/recipe.go:37-62`), `checkGateSteps`/`checkGateCommands`/`gateCommand`/`verify.Run` and the "a check step only ever names a `commands.*` key" rule (`internal/runner/gate.go:47-62,100-127,132-175`, `internal/verify/verify.go:39-64,176-260`); the runner's mode branches, the git-token skip and the checkpoint gate (`internal/runner/runner.go:399-403,635,878-885,931-935,966-1000,1076-1080`, `internal/runner/gate.go:305-313`); the review mode's `skipPR` and `OutcomeReviewed` (`internal/runner/reviewmode.go:469-470,698`); `verify.Settings` and its writer (`internal/verify/verify.go:39-64`, `internal/runner/runner.go:1076-1080`); `task.Spec` and its strict decoder (`internal/task/task.go:46-89`); the launch order — mint, `CreateTask`, `Launch` (`internal/cli/run.go:819,1147,1392`); `checkImageSince` and the `*Since` constants (`internal/cli/recipes_skew.go:25-150,204-270`); the Cloud Run limits (`internal/backend/gcp/limits.go:22,36-38,55`); the caps and the uncapped compute estimate (`internal/budget/model.go:150-165`, `internal/budget/evaluate.go:190-209`, `internal/runner/cost.go:62-64`, `internal/cli/budget_preview.go:26-28,489-538`, `docs/design/m9-spec-v3-reconciliation.md:10`); the runner test harness's in-memory bucket (`internal/runner/runner_test.go:53-120`); and Check 43's guard block plus the numbers this plan's Check 58 had to avoid colliding with (`docs/gcp-live-checklist.md:319-324` for rows 42–47, `:325` for check 57, `:1427-1458` for Check 43's guard block). If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the rule `docs/plans/2026-10-08-upgrade.md` uses).

## Decisions (veto any before execution starts)

- **P1. Two new `blobx` methods carry the bytes, and nothing else in the object layer changes.** `PutFile` (streaming write, sha256 in the same pass) and `GetFile` (streaming read to a file, hash-verified, capped) are added to `internal/blobx`; `Create`/`ReplaceIf`/`Read`/`ReadMax` keep their signatures and their 1 MiB cap for locks, claims, `result.json` and `verify/*.json`. *Veto alternative:* put `[]byte` artifacts through the existing `Put` and raise a limit — rejected because `ReadMax`'s only consumer shape is "the whole object in memory" (`internal/blobx/blobx.go:173-198`) and an artifact is a video; the CLI would then need the artifact's size to fit RAM, and the runner would hold a second copy of a 256 MiB file to hash it. *Second alternative:* do the streaming in `internal/runner` with go-cloud's `blob.NewWriter` directly — rejected because the runner, the CLI and (later) any copy command would each grow their own write path with its own content-type and error-classification rules, which is exactly what `blobx` exists to prevent (`isForbidden`, `blobx.go:200`).
- **P2. The agent writes a *plan*, the repository owns the *code*.** `commands.render` and `commands.probe` are repository text from `fugaro.yaml` at the task's ref, read by the runner out of `verify.Settings` exactly as `commands.build|test|lint` are (`internal/verify/verify.go:39-49`, written at `internal/runner/runner.go:1076-1080`), and the agent's plan file is *data* the command may read. **The runner never executes text the agent wrote.** *Veto alternative:* let `commands.render` be whatever the agent left in the working tree (`sh render.sh`). Shorter, and it costs the property that makes the gate safe — "a recipe never names a command", and the thing the runner runs is repository text reviewed like code (`docs/design/generic-tool.md:356`, `internal/runner/gate.go:125-127`). **This is P2 in the design's sense and it is not negotiable in this plan.**
- **P3. `render` is a new step kind and `plan` an explicit one; a check step is not reused.** `StepRender` runs last and its failure is the run's failure; `StepPlan` marks the optional agent stage a `mode: artifact` recipe may begin with (`steps: [plan, render]` is the sizzle case, `steps: [render]` alone is the zero-model case). *Veto alternative:* add `render` to `check.command`'s enum and let a check step be last in artifact mode — one concept fewer, and it makes a step kind whose meaning depends on its mode, which is the conditional the recipe format has avoided so far, and it would have to re-cut the ordering rule (`schemas/recipe.schema.json`'s `prefixItems`) and the failure path (`gateResult`, which feeds the implement prompt).
- **P4. The render and the probe run as one command each, in the checkout, as the agent's user, through `procgroup`, with the repository's own environment** (`r.env`), exactly like `verify.Run` (`internal/verify/verify.go:238-242`) — no shell wrapper of the runner's own, no new executor. The probe is one invocation for the whole set, with `FUGARO_OUT_DIR` and `FUGARO_ARTIFACTS` in the environment. *Veto alternative:* one probe process per artifact with the path interpolated into a command line — rejected in the design (§3): it puts a runner-derived name inside a shell command.
- **P5. Artifacts are staged locally, then hashed, then uploaded from the staging directory, and the object key is always `runs/<slug>/<run>/artifacts/<name>`.** The staging directory is `<StateDir>/artifacts/`, beside `verify/` (which is uploaded the same way, `internal/runner/runner.go:2192`), so the sanitiser has exactly one output path, the probe sees the same bytes that will be uploaded, and a name that escapes cannot reach a key. *Veto alternative:* upload the files in place from the checkout and record the paths — rejected: the checkout is agent-writable, so between hashing and uploading the bytes could change, and the name would then be a path into the repository rather than into a staging root.
- **P6. The caps are defaults with hard ceilings, and they are checked twice.** `outputs.max_files` defaults to 8 and may not exceed 32; `outputs.max_bytes` defaults to 256 MiB and may not exceed 2 GiB; `inputs.max_files` defaults to 16/ceiling 64 and `inputs.max_bytes` to 256 MiB/ceiling 1 GiB. The launcher checks the attachments before the first byte goes up; the runner checks the task spec's claim *and* the files it actually found before uploading. A cap is a refusal with a named reason, never a truncation. *Veto alternative:* no caps on the first slice — rejected: bucket-iam §10's L6 ("fill `runs/` with junk") is already a known limit, and a glob that matches a directory of frames is the cheapest way to hit it.
- **P7. `--attach` uploads between the run-id mint and `CreateTask`.** The launcher mints the id (`internal/cli/run.go:819`), uploads and hashes the files to `runs/<slug>/<run>/inputs/<name>`, writes the list into the task spec (`task.Spec.Inputs`), and only then writes `task.json` and launches. *Veto alternative:* upload after the launch — rejected: the run would have to poll for names it does not know, and a half-uploaded set is a race with no good resolution; the launcher's own `objectUser` under `runs/` is enough for the upload (`docs/design/bucket-iam.md:126-128`), so nothing is gained by waiting.
- **P8. `budget preview` gains one line and one line only: the compute ceiling, labelled "not capped".** No cap is applied to compute (design §6, D4), and the line prices `vCPU`/`memory` × (`timeouts.render` + the model stages' expected time) at the installation's compute prices. *Veto alternative:* leave the preview alone until compute is inside the cap — rejected: the preview is where an owner decides whether to launch, and for an artifact run it currently under-reports by the whole render.
- **P9. The new recipe keys are gated at launch by one `*Since` constant (`artifactsSince`), and the task spec's new field by the same one.** A `mode: artifact` recipe needs a runner that knows it, and `task.Spec` refuses unknown fields (`internal/task/task.go:46-89`), so `fugaro run --attach` must refuse against a job image whose runner predates the feature, through the existing `checkImageSince` seam (`internal/cli/recipes_skew.go:204-270`) with a message naming `fugaro image refresh`. *Veto alternative:* let the runner refuse after the launch — rejected: that is the skew the seam exists to catch, and it costs a launched run that fails on a field the launcher could have checked.
- **P10. Release placement: one PR, Tasks 1–9, released as 0.8.0** (design §10). Tasks 1, 2, 8 and 9 are individually safe to ship in a patch; Tasks 3–7 are the feature and belong in the point release. Task 10 is the owner-run live check and is **not** part of the PR; Task 11 is the full suite. No Terraform, no IAM, no bucket lifecycle and no `.github/workflows/` file is touched by any task in this plan.

## Global Constraints

- **Nothing the runner executes is new text.** Every command a run runs still comes from `verify.Settings`, written at bootstrap from the `fugaro.yaml` the run read at its ref (`internal/runner/runner.go:1076-1080`); `render` and `probe` join `build|test|lint` there and nowhere else. A recipe, a task spec and a bucket object still name no command.
- **An artifact run never pushes and never opens a pull request.** `r.artifact` joins `r.review` at every site that decides "the runner fetches, the agent does not": the agent's git env (`runner.go:635`), the checkpoint gate and the base fetch (`:966-1000`), and finalize's PR path (`r.skipPR`, `internal/runner/gate.go:305-313`). It also never checkpoints, so a run that renders for an hour cannot push a tip.
- **No live cloud or network call in any automated test.** The runner tests use the harness's in-memory bucket (`internal/runner/runner_test.go:53-120`); `blobx` tests use `file://` buckets, which `blobx.Open` supports (`internal/cli/exec.go:59`); the CLI tests use the existing fakes. The one task that would touch a real cloud is Task 10, and it is the owner's, run by hand.
- **A stored record stays readable by every version.** `result.json`'s new keys (`render`, `artifacts`, `inputs`) and `task.json`'s (`inputs`) are **optional**, none is added to `required`, and `schemas/result.schema.json`'s `additionalProperties: false` is satisfied by adding the properties. A reader that has not heard of them ignores them; a writer that predates them simply omits them. `schemas/schemas_test.go` is the only validator, and it grows with `runstore.Record` (`TestResultSchemaAcceptsRunnerRecords`) — a run record written before this change still validates, and that is a test, not a hope.
- **Docs must match code.** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout, and a proposed command or flag is never written into a lint-scanned doc as if it existed: `TestBaseImageDocCommandFlagsAreReal` (`docs_base_test.go:173`) resolves every `--flag` near a `trackedDocCommands` mention against the real cobra flag set, and the skill lint (`plugin/skills_lint_test.go`) resolves every `fugaro …` mention in `plugin/skills/` against the tree. Task 9's docs either land after the commands they name, or describe them in a form the lint does not resolve.
- **`gofmt -l .` clean**, every test run in the foreground with `-count=1`, one commit per task, and the repository's `Co-Authored-By` line.
- **No new dependency.** The render, the probe, the hashing and the upload use the standard library, `procgroup` and `blobx`; ffmpeg is the *repository's* tool, installed through `image.apt` (Task 9's docs and Task 10's check), never a Go dependency of this repository.

## Review Focus (mutation-proof notes)

1. **The runner executes text the agent wrote.** Pinned by a test in which the agent stage creates `render.sh` in the checkout and the recipe's `commands.render` is `sh .fugaro/render.sh "$FUGARO_RENDER_PLAN"`: assert the command the runner ran came from settings (the fake records the argv), that a *different* `render.sh` planted in the working tree is not what ran, and that `commands.render: ""` refuses the run rather than running an empty `sh -c` and recording a bogus success — the same fail-closed shape `verify.Run` already has for an empty `commands.lint` (`internal/verify/verify.go:212-222`). Task 2.
2. **An artifact run pushes something.** Pinned three ways: the harness's fake provider records zero pushes, `rec.Outcome` is `artifacts` and never `ready`/`draft`, and the agent's environment contains no git credential — asserted by the same shape `TestReviewModeAgentHasNoGitToken` uses (`internal/runner/reviewmode_test.go:969`, and its `envValue` helper at `internal/runner/runner_test.go:156`). Task 2.
3. **A file escapes the run's prefix.** Pinned by a table test over the sanitiser: `../../etc/passwd`, `/etc/passwd`, `a/../../b`, `out/./x` normalising to a duplicate, a name with a NUL or a newline, an over-long name, and a symlink pointing outside the outputs root — each refused with a named reason, and the accepted ones all landing under `runs/acme-app/<run>/artifacts/` in the harness's bucket (assert the bucket's key set, not the returned slice). Task 4.
4. **A cap is silently exceeded.** Pinned by four tests: a glob matching more files than `max_files`; one file larger than `max_bytes`; a task spec claiming fewer inputs than the bucket holds; and a set whose total is over the cap while every file is under it. Each must refuse with the cap named and upload **nothing** — assert the bucket has no `artifacts/` key at all, so a partial upload fails the test. Tasks 4 and 6.
5. **The hash is of something else.** Pinned by asserting the recorded `sha256` equals `sha256sum` of the bytes actually in the bucket (read back through the harness) — and by a test where the staged file is replaced between the hash and the upload (the seam exists for the test), which must fail the run rather than record a hash for bytes that are not there. Task 4.
6. **The probe's verdict is ignored, or its failure is a pass.** Pinned by two runs: one where `commands.probe` exits 1 and the run must end `failed` with `outcome: none` **and no artifacts uploaded** (fail closed, even though the files exist), and one where `commands.probe` is absent and the run succeeds with a `render` record that says no probe ran — so "there is no probe" is visible in the record rather than inferred from a missing field. Task 5.
7. **A render that outlives its deadline.** Pinned by a test with a small `timeouts.render` and a `sleep`-shaped render command: the step ends as a timeout, the run is `failed`/`timed_out`, the record quotes it, and the run's own `finalizeAt` still holds (no stage may eat the reserve). Task 2.
8. **The record breaks an old reader, or an old record breaks the new one.** Pinned by `TestResultSchemaAcceptsRunnerRecords` over a record with the new fields **and** by a test that unmarshals a fixture `result.json` written before this change (no `artifacts`, no `render`, no `inputs`) into `runstore.Record` and asserts every new field is nil/empty and nothing else changed — the compatibility claim in Global Constraints, as a test. Task 4.
9. **`fugaro artifacts get` writes something it should not.** Pinned by tests: a mismatching sha256 writes **nothing** (assert the output directory is empty), an over-cap `Content-Length` refuses before the body is read, a name from the record that tries to escape is refused, `--force` is required to overwrite, and the command never opens what it wrote (no exec, no unpack — assert by making the artifact a shell script whose execution would create a second file, and asserting that file does not exist). Task 7.
10. **The upload's memory.** Pinned by a test that streams an artifact larger than `blobx.MaxReadBytes` (say 8 MiB) through `PutFile` from a `file://` bucket and asserts the object's bytes and hash, plus a `runtime.ReadMemStats`-shaped guard is **not** the assertion (allocation is not a contract) — the assertion is that `PutFile` never calls `ReadAll`: `grep`-proof by construction because `PutFile` takes a path and `io.Copy`s it. Task 3.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `schemas/recipe.schema.json`, `internal/recipe/recipe.go`, `internal/recipe/recipe_test.go` | `ModeArtifact`, `StepPlan`/`StepRender`, the `outputs` block, the ordering and refusal rules, `UsesV08` (the new keys, for the launch gate) | 1 |
| `schemas/fugaro.schema.json`, `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/config_test.go`, `internal/cli/validate_recipes.go` | `commands.render`, `commands.probe`, `timeouts.render`, `workflows.<name>.inputs`, their defaults and their `validate` refusals | 1 |
| `internal/runner/artifact.go` (new), `internal/runner/artifact_test.go` (new), `internal/runner/runner.go` (bootstrap's mode branch, the render step, the report section) | `r.artifact`, the plan stage, the render step's deadline and failure path, the `render` record, the report's Artifacts section | 2 |
| `internal/blobx/blobx.go`, `internal/blobx/blobx_test.go` | `PutFile`, `GetFile` — streaming, hashing, capped, 403-classified | 3 |
| `internal/runstore/artifact.go` (new), `internal/runstore/artifact_test.go` (new), `internal/runstore/runstore.go` (the `Record` fields), `schemas/result.schema.json`, `schemas/schemas_test.go` | `ArtifactFile`, `InputFile`, `RenderRecord`, the record's three optional fields, the schema properties, the round-trip tests | 4 |
| `internal/runner/artifact.go` (the selection, sanitiser, caps, upload), `internal/runner/artifact_test.go` | `artifactSet`, `sanitiseName`, `uploadArtifacts` | 4 |
| `internal/verify/verify.go` (the two `Settings` fields), `internal/runner/artifact.go` (the probe), `internal/runner/artifact_test.go` | the probe's one invocation, its two environment variables, its record | 5 |
| `internal/task/task.go`, `schemas/task.schema.json`, `internal/task/task_test.go`, `internal/cli/run.go` (the flags and the upload), `internal/cli/run_attach.go` (new), `internal/cli/run_attach_test.go` (new) | `task.Spec.Inputs`, `--attach`, the pre-mint upload, the caps, the image gate | 6 |
| `internal/runner/artifact.go` (the download), `internal/runner/artifact_test.go` | the inputs' download, the hash check, the `inputs/` write and the git exclude | 6 |
| `internal/cli/artifacts.go` (new), `internal/cli/artifacts_test.go` (new), `internal/cli/runs.go`, `internal/cli/diagnose.go`, `internal/cli/runview.go`, the corresponding tests | `fugaro artifacts ls\|get`, the display in `runs ls` and `diagnose` | 7 |
| `internal/cli/budget_preview.go`, `internal/cli/budget_preview_test.go` | the compute-ceiling line, labelled "not capped" | 8 |
| `docs/artifact-runs.md` (new), `docs/recipes.md`, `docs/base-image.md`, `docs/gcp-live-checklist.md` (Check 58's row and section), `SECURITY.md`, `plugin/skills/routing/SKILL.md` (its "Choosing a recipe" section, `:47`), `docs/release.md` | the user docs, the ffmpeg recipe, the SECURITY.md paragraph of design §8.7 verbatim, Check 58 as written | 9 |

No task edits `internal/backend/**`, `deploy/terraform/**`, `internal/image/render.go` or `.github/workflows/**`: the substrate is unchanged (D3 — Cloud Run, no GPU, no Batch), ffmpeg reaches the image through `image.apt`, which already exists (`images/derived/Dockerfile.tmpl:26-33`), and no permission, bucket, lifecycle rule or prefix is added anywhere (D2, D7).

## PR groups

**One PR, Tasks 1–9, released together as 0.8.0** (P10). Task 10 is the owner-run live check and is **not** in it; Task 11 is the repository's full suite plus the `fugaro verify build`/`verify test` run that closes every task.

The patch-safe subset the design names (§10) is Tasks 1, 2, 8 and 9 — each harmless alone — but the plan keeps them in the one release: the feature is only usable with 3–7 beside them.

---

### Task 1: The format — `mode: artifact`, `plan`/`render`, `outputs`, and the two commands

**Files:**
- Edit: `schemas/recipe.schema.json`, `internal/recipe/recipe.go`
- Test: `internal/recipe/recipe_test.go`
- Edit: `schemas/fugaro.schema.json`, `internal/config/config.go`, `internal/config/defaults.go`
- Test: `internal/config/config_test.go`, `internal/cli/validate_test.go`
- Edit: `internal/cli/validate_recipes.go:112-126` (`gateCheckProblems`; the `render`/`probe` pairing refusals go beside `RecipeGateProblems`)

**Interfaces:**
```go
// package recipe
// ModeArtifact is the third Recipe.Mode: no pull request, no git token in the
// agent's environment, one optional plan stage, one render step, and an
// artifacts deliverable.
const ModeArtifact = "artifact"

const (
    // StepPlan is the optional agent stage an artifact recipe may begin
    // with: it writes a render plan, and nothing else.
    StepPlan StepKind = "plan"
    // StepRender is the deterministic render, always last, never in
    // implement or review mode.
    StepRender StepKind = "render"
)

// MaxOutputFiles and MaxOutputBytes are the ceilings Outputs.MaxFiles and
// Outputs.MaxBytes may not exceed (P6).
const (
    MaxOutputFiles = 32
    MaxOutputBytes = int64(2) << 30
)

// Outputs is a mode: artifact recipe's declaration of what it delivers,
// from the recipe's top-level outputs: block.
type Outputs struct {
    Globs    []string // relative to the checkout root; no absolute path, no ".."
    MaxFiles int      // 0 means DefaultOutputFiles (8)
    MaxBytes int64    // 0 means DefaultOutputBytes (256 MiB); per artifact
    Probe    string   // a commands.* key: "probe" or "none"
    // HasKeys records that the block was present at all, so UsesV08 can
    // decide from presence rather than from a parsed value, the rule
    // newKeys already follows (recipe.go:100-115).
    HasKeys bool
}

// Recipe gains Outputs *Outputs, non-nil exactly when Mode is ModeArtifact.
```

`Parse` refuses, with the recipe's own message shape: `outputs:` on a `mode: implement` or `mode: review` recipe; a `mode: artifact` recipe without `outputs:`; a `mode: artifact` recipe with `steps` that is not exactly one `render` or `plan` followed by one `render`; `plan` or `render` in the other two modes; `check`/`first_line`/`review` in artifact mode (`check` included: an artifact recipe has no implement to gate); an absolute glob, a glob with a `..` element, an empty glob list, a `max_files`/`max_bytes` above the ceiling or below 1; `probe` naming anything but a `commands.*` key this version knows; and `roles`/`review.bounce`.

**There is deliberately no refusal of a catalog artifact recipe, and the reason a first draft of this task gave for one was wrong.** `recipe.Parse` takes only bytes and `Recipe` has no source at all (`internal/recipe/recipe.go:195`; the source is decided by the resolver, `internal/runner/recipe.go:29-58`), so that refusal could not have lived in `Parse` — and it should not live anywhere: the catalog already ships `mode: review` recipes (`internal/recipe/catalog/review-only.yaml`, `pick.yaml`) and recipes whose check steps name `commands.*` keys (`lint-fix.yaml`, `test-and-fix.yaml`), which is the very thing the discarded justification claimed a catalog recipe may not do. A catalog artifact recipe is legitimate for the same reason those are: its **commands still come from the repository's `fugaro.yaml`**, so a catalog recipe can choose the shape of a run without the catalog ever naming a command (P2). No task in this plan adds a source-based refusal.

```go
// package config
type Commands struct {
    // … existing fields …
    // Render is mode: artifact's render command (design artifact-runs.md
    // §3): repository text, run once, in the checkout, as the agent's user.
    // "" means no artifact recipe may run against this workflow.
    Render string `yaml:"render"`
    // Probe is the artifact recipe's optional check on what it rendered:
    // one command, given the outputs directory and the names in its
    // environment. "" means the recipe's outputs.probe must be "none".
    Probe string `yaml:"probe"`
}

type Timeouts struct {
    // … existing fields …
    // Render bounds the render step alone (default min(verify, total -
    // finalize_reserve)); the run's own deadline caps it further.
    Render Duration `yaml:"render"`
}

type Workflow struct {
    // … existing fields …
    Inputs Inputs `yaml:"inputs"`
}

// Inputs caps what a launcher may attach to a run of this workflow.
type Inputs struct {
    MaxFiles int   `yaml:"max_files"` // 0 means 16; ceiling 64
    MaxBytes int64 `yaml:"max_bytes"` // 0 means 256 MiB; ceiling 1 GiB
}
```

Defaults in `internal/config/defaults.go`: `timeouts.render = min(verify, total − finalize_reserve)`, computed where the other three timeouts are (`defaults.go:77-90`, which already does exactly this min-shape for `stage` and `verify`); `inputs.max_files = 16`, `inputs.max_bytes = 256 << 20`.

`gateCheckProblems` (`internal/cli/validate_recipes.go:112-126`) — the function `fugaro validate` uses to report a recipe's gate refusals against every workflow — gains two more messages beside `RecipeGateProblems`'s (`internal/runner/gate.go:118-122`), whose shared list is `gateCommandProblems` (`internal/runner/gate.go:100-116`): an artifact recipe over a workflow with a blank `commands.render` is refused at launch **and** reported by validate, and `outputs.probe: probe` over a blank `commands.probe` likewise.

- [ ] **Step 1: Write the failing tests**

```go
// internal/recipe/recipe_test.go
func TestArtifactModeRequiresOutputs(t *testing.T)          // and refuses it in the other two modes
func TestArtifactStepsArePlanThenRenderOrRenderAlone(t *testing.T)
func TestArtifactModeRefusesTheAgentSteps(t *testing.T)      // check, first_line, review, roles, bounce
func TestOutputsGlobsRefuseEscapes(t *testing.T)             // absolute, "..", empty
func TestOutputsCapsHaveCeilings(t *testing.T)               // 33 files, 2GiB+1, 0
func TestUsesV08IsPresenceNotValue(t *testing.T)             // mode: implement plus an outputs: {} block still counts
```
```go
// internal/config/config_test.go
func TestRenderTimeoutDefaultsUnderTotal(t *testing.T)  // min(verify, total-finalize_reserve), and never above total
func TestInputCapsHaveCeilings(t *testing.T)
```
Run: `go test ./internal/recipe ./internal/config -run 'TestArtifact|TestOutputs|TestUsesV08|TestRenderTimeout|TestInputCaps' -count=1`
Expected: **FAIL** — `undefined: recipe.ModeArtifact` and the schema tests' first run against an unknown key.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`. The recipe schema's `steps` variants gain artifact's two shapes as their own `oneOf` entries (a new `$defs.artifactSteps` beside the existing `reviewModeReview`), and `schemas/recipe.schema.json`'s conditional gains "`mode: artifact` → exactly one render, optionally led by one plan".
- [ ] **Step 3: The schema is compiled by a test, so a malformed one fails here**: `go test ./schemas -count=1`. Expected: `ok`.
- [ ] **Step 4: `gofmt -l .`** Expected: no output.
- [ ] **Step 5: Commit.**

---

### Task 2: `mode: artifact` in the runner, and the render step

**Files:**
- Create: `internal/runner/artifact.go`, `internal/runner/artifact_test.go`
- Edit: `internal/runner/runner.go` (bootstrap's mode branch at `:878-935`, the mode switch at `:399-403`, the git-env skip at `:635`, the checkpoint gate at `:966`, `Run`'s finalize arm), `internal/runner/runner_test.go` (only if a harness helper is needed)

**Interfaces:**
```go
// package runner
// artifact is whether this run is a mode: artifact run (design
// artifact-runs.md §3). Set at bootstrap from the resolved recipe's Mode,
// beside r.review, and cross-checked against the task spec in exactly two
// directions — never a third:
//   - a spec that carries inputs but whose recipe is not mode: artifact is
//     refused (nothing but an artifact run reads inputs/);
//   - an artifact spec that names a branch, a PR or a previous run is
//     refused (an artifact run has no branch of its own and continues
//     nothing — the same refusal shape review mode's own cross-check has).
// An artifact spec with NO inputs is legal and must stay legal: an
// out-only run (steps: [render], or a plan stage that needs nothing
// attached) carries an empty Inputs and is launched every day, so "the
// reverse" of the first rule is not a rule.
type run struct {
    // … existing fields …
    artifact bool
    out      *recipe.Outputs
    // renderRec is what runRender produced, recorded into the run's
    // result.json by finalize and printed by the report.
    renderRec *runstore.RenderRecord
    artifacts []runstore.ArtifactFile
    inputs    []runstore.InputFile
}

// runArtifactMode runs an artifact run: the optional plan stage, then the
// one render step. It sets skipPR and ends the run with OutcomeArtifacts
// on success, failed/OutcomeNone otherwise. It never pushes.
func (r *run) runArtifactMode(ctx context.Context)

// runRender runs commands.render once, in the checkout, as the agent's
// user, under timeouts.render and the run's own deadline, capturing a
// redacted log tail. Its exit code decides the run.
func (r *run) runRender(ctx context.Context) (runstore.RenderRecord, error)
```

Bootstrap changes, each one line and each a mirror of the review-mode line beside it:

| Site | Change |
|---|---|
| `runner.go:399-403` | `case r.review: … case r.artifact: r.runArtifactMode(runCtx)` |
| `runner.go:635` | `r.follow == nil && !r.review` → `&& !r.artifact` (no git credential in the agent's env) |
| `runner.go:966` | the same condition → no `FetchBase`, no checkpoint push for an artifact run |
| `runner.go:878-885` | a new arm: an artifact run checks the task's ref out (a local branch, exactly as the implement path does — `CheckoutNewBranch`, never pushed) and reads its configuration from the working tree at that ref, because there is no pull request whose `fugaro.yaml` could be untrusted (the reason review mode reads the base instead) |
| `runner.go:931-935` | the mode cross-checks gain their artifact pair |
| `finalize` | `r.skipPR` is already set by the artifact arm, so finalize writes the report and `result.json` and nothing else — the same mechanism review mode uses (`reviewmode.go:470,698`) |

The plan stage: one agent stage named `plan`, its own stage timeout, its own `agent.max_budget_usd` (the existing stage machinery, unchanged), with the task text plus the recipe's `use_when` plus a line naming `FUGARO_INPUTS_DIR` (Task 6) and the render plan's expected path, `.fugaro/render-plan.json`. Its prompt is the *only* place the run describes the deliverable to the agent, so it is written to say: write a plan, do not render, the render is the runner's. Nothing about it is new code beyond the prompt and the stage's name.

The `render` record keeps the shape the design fixes: `{command, exit_code, timed_out, duration_s, started_at}`, where `command` is the **redacted** exact string (the redactor is already there) and `exit_code` a number. `OutcomeArtifacts` is a new value in `runstore` (`"artifacts"`), added to the schema's `outcome` enum in Task 4.

- [ ] **Step 1: Write the failing tests**

```go
// internal/runner/artifact_test.go
func TestArtifactRunNeverPushesAndHasNoGitToken(t *testing.T) // Review Focus 2
func TestArtifactRunOutcomeIsArtifacts(t *testing.T)
func TestRenderFailureEndsTheRunFailedWithNoArtifacts(t *testing.T)
func TestRenderTimeoutIsRecordedAndFailsTheRun(t *testing.T)  // Review Focus 7
func TestRenderCommandComesFromSettingsNotTheWorkingTree(t *testing.T) // Review Focus 1
func TestBlankRenderCommandRefusesTheRun(t *testing.T)        // Review Focus 1, fail closed
func TestPlanStageIsOptional(t *testing.T)                    // steps: [render] runs with no agent call at all
func TestArtifactRunRecordsNoBranchAndNoPR(t *testing.T)
```
The harness (`internal/runner/runner_test.go:53-120`) already provides the in-memory bucket, the fake provider and the scripted agent; the artifact tests use `newHarnessFiles` with a fixture recipe committed to the remote (`.fugaro/recipes/sizzle.yaml`) and a `fugaro.yaml` carrying `commands.render`, so no new fake is needed. **If the scripted agent cannot yet script "the agent writes a file", that is a two-line addition to the harness (a step that writes into `req.Dir`), and it belongs to this task.**

Run: `go test ./internal/runner -run 'TestArtifact|TestRender|TestPlanStage' -count=1`
Expected: **FAIL** — `undefined: runstore.OutcomeArtifacts`, and the run currently takes the implement path.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 3: `blobx.PutFile` and `blobx.GetFile` — the bytes, streamed

**Files:**
- Edit: `internal/blobx/blobx.go`
- Test: `internal/blobx/blobx_test.go`

**Interfaces:**
```go
// package blobx
// PutFile streams path to key with contentType, hashing the bytes as they
// go, and returns the object's size and the lower-case hex sha256 of what
// it wrote. It never holds the file in memory: unlike Put, which takes a
// []byte, this is the one write path for an object that can be large
// (design artifact-runs.md §3, V23).
func (b *Bucket) PutFile(ctx context.Context, key, path, contentType string) (size int64, sum string, err error)

// GetFile streams key into path, up to limit bytes, hashing as it goes, and
// returns the size and the lower-case hex sha256 of what it wrote. sum is the
// *expected* hash (the record's own, or the task spec's for an input): when it
// is non-empty and does not match, the temporary file is removed and nothing
// is ever placed at path — ErrMismatch, with both hashes in the message. An
// object larger than limit is ErrTooLarge, and likewise leaves path alone:
// the cap refuses, it never truncates. A 403 is ErrForbidden, exactly as Read
// classifies it.
func (b *Bucket) GetFile(ctx context.Context, key, path, sum string, limit int64) (size int64, sha string, err error)
```

Both use `NewWriter`/`NewReader` on the embedded `*blob.Bucket` (the type already embeds it and already reaches GCS-specific handles through `BeforeWrite`/`As`, `blobx.go:109-138`), `io.Copy` with a `sha256.New()` through `io.MultiWriter`, and `os.CreateTemp` in the destination's directory so **`path` is only ever reached by a `Rename` of verified bytes**: a mismatch or an over-cap object removes the temp file and leaves no destination at all. Content type is set on the writer exactly as `write` sets it (`blobx.go:110-111`), and `GetFile` checks the writer's own `Content-Length` first when the backend reports one (the `file://` bucket does; GCS does) — the check is an optimisation, not the guarantee, which is the limit. **The expected hash is an argument because the two callers cannot check it afterwards and still keep the "nothing was written" property:** the CLI would otherwise find the mismatching file already sitting at its final path (Review Focus 9), and the runner's input download would have to delete it. `PutFile` has no such argument: the hash *is* what it produces, and its caller records it.

- [ ] **Step 1: Write the failing tests**

```go
// internal/blobx/blobx_test.go
func TestPutFileStreamsAndHashes(t *testing.T)        // 8 MiB through a file:// bucket, read back with ReadMax and compare
func TestPutFileSetsContentType(t *testing.T)
func TestGetFileRefusesOverTheLimitAndLeavesNothing(t *testing.T) // no destination, no temp file left behind
func TestGetFileRefusesAMismatchAndLeavesNothing(t *testing.T)    // the expected hash is an argument: nothing at path, temp removed
func TestGetFileWithoutAnExpectedHashStillReturnsOne(t *testing.T) // "" means no check, and the hash comes back for the caller to record
func TestGetFileClassifiesForbidden(t *testing.T)     // forbiddenBucket(t) helper already exists
```
Run: `go test ./internal/blobx -run 'TestPutFile|TestGetFile' -count=1`
Expected: **FAIL** — `b.PutFile undefined`.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 4: The outputs — select, sanitise, cap, hash, upload, and record

**Files:**
- Create: `internal/runstore/artifact.go`, `internal/runstore/artifact_test.go`
- Edit: `internal/runstore/runstore.go` (the `Record` fields), `schemas/result.schema.json`, `schemas/schemas_test.go`
- Edit: `internal/runner/artifact.go`, `internal/runner/artifact_test.go`

**Interfaces:**
```go
// package runstore
// ArtifactFile is one file an artifact run delivered: the record's own
// account of it, written before the bytes are uploaded so a reader can
// verify what it downloads (design artifact-runs.md §3, §8).
type ArtifactFile struct {
    Name        string        `json:"name"`         // relative to artifacts/, never a path out of it
    Bytes       int64         `json:"bytes"`
    SHA256      string        `json:"sha256"`
    ContentType string        `json:"content_type"` // recorded, never trusted (§8.1)
    Probe       *ProbeResult  `json:"probe,omitempty"`
}
type ProbeResult struct {
    Ran      bool    `json:"ran"`
    ExitCode int     `json:"exit_code"`
    Warning  string  `json:"warning,omitempty"` // "" when the probe passed
}
type InputFile struct {
    Name   string `json:"name"`
    Bytes  int64  `json:"bytes"`
    SHA256 string `json:"sha256"`
}
type RenderRecord struct {
    Command   string    `json:"command"`
    ExitCode  int       `json:"exit_code"`
    TimedOut  bool      `json:"timed_out"`
    DurationS float64   `json:"duration_s"`
    StartedAt time.Time `json:"started_at"`
}
// Record gains, all optional and all absent from required:
//   Render    *RenderRecord  `json:"render,omitempty"`
//   Artifacts []ArtifactFile `json:"artifacts,omitempty"`
//   Inputs    []InputFile    `json:"inputs,omitempty"`
// And Outcome gains OutcomeArtifacts = "artifacts".
```

```go
// package runner
// artifactSet is what the runner stages, in the order it will upload.
type artifactSet struct {
    dir   string   // <StateDir>/artifacts/, the staging root (P5)
    files []staged // name (relative, sanitised), local path, bytes, sha256, content type
}
// stageArtifacts matches the recipe's globs against the checkout, refuses
// the escapes and the caps, copies each match into the staging directory
// and hashes it there.
func (r *run) stageArtifacts(out *recipe.Outputs) (artifactSet, error)
// uploadArtifacts writes the set to runs/<slug>/<run>/artifacts/, in name
// order, and returns the record's []ArtifactFile.
func (r *run) uploadArtifacts(ctx context.Context, set artifactSet) ([]runstore.ArtifactFile, error)
```

The sanitiser is one function with one refusal list (design §8.2): the name is the match's path **relative to the deepest common root of the recipe's globs**, cleaned with `path.Clean`, and it must be non-empty, must not be absolute, must not contain a `..` element or a NUL/newline, must be at most 200 bytes, and must not collide with a name already taken (after cleaning). A symlink in the checkout whose target is outside the outputs root is skipped with a warning rather than followed — a refusal, not a copy, because the bytes would then be a file the run never rendered.

The caps, and the order they are checked in: match count against `max_files`, then each match's size against `max_bytes`, then the set's total against `max_bytes × max_files` (the aggregate ceiling). Nothing is staged until every check passes — **so a refused set leaves the staging directory empty and the bucket untouched** (Review Focus 4).

`schemas/result.schema.json` gains the three properties and the `artifacts` outcome, none of them required. `schemas/schemas_test.go`'s `TestResultSchemaAcceptsRunnerRecords` grows with the struct (it marshals a fully-populated `runstore.Record`, so it fails until the schema has the new properties — that is the test that keeps the two in step), and a new `TestResultSchemaAcceptsARecordWithoutArtifacts` unmarshals a pre-change fixture and asserts the new fields are empty (Review Focus 8).

- [ ] **Step 1: Write the failing tests**

```go
// internal/runner/artifact_test.go
func TestStageArtifactsRefusesEscapes(t *testing.T)           // Review Focus 3, the table
func TestStageArtifactsRefusesTheCaps(t *testing.T)           // Review Focus 4, all four shapes
func TestUploadArtifactsWritesUnderTheRunPrefix(t *testing.T) // assert the bucket's key set
func TestArtifactHashMatchesTheUploadedBytes(t *testing.T)    // Review Focus 5
func TestArtifactsAreUploadedInNameOrder(t *testing.T)
func TestResultJSONCarriesRenderArtifactsAndInputs(t *testing.T)
func TestArtifactNameCollisionAfterCleaningIsRefused(t *testing.T)
func TestASymlinkOutOfTheOutputsRootIsRefusedNotFollowed(t *testing.T)
```
```go
// internal/runstore/artifact_test.go
func TestRecordReadsAPreChangeResultJSON(t *testing.T) // Review Focus 8
```
Run: `go test ./internal/runner ./internal/runstore -run 'TestStage|TestUpload|TestArtifact|TestResultJSON|TestRecordReads' -count=1` and `go test ./schemas -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 5: The probe

**Files:**
- Edit: `internal/verify/verify.go` (`Settings.Render`, `Settings.Probe`), `internal/runner/runner.go:1076-1080` (write both from `wf.Commands`)
- Edit: `internal/runner/artifact.go`, `internal/runner/artifact_test.go`

**Interfaces:**
```go
// package runner
// runProbe runs commands.probe once, after the render and before the
// upload, with the whole staged set named in the environment:
//   FUGARO_OUT_DIR   the staging directory's absolute path
//   FUGARO_ARTIFACTS the staged names, one per line, in upload order
// A non-zero exit or a timeout fails the run and refuses the upload
// (Review Focus 6): the probe is the only thing that can say a 0-byte
// render is not a video, so it must be able to stop one.
func (r *run) runProbe(ctx context.Context, set artifactSet) (runstore.ProbeResult, string, error)
```

The probe runs through `procgroup.Run` with `Dir: s.RepoDir`, `Env: append(r.env, "FUGARO_OUT_DIR=…", "FUGARO_ARTIFACTS=…")` and `Grace: commandGrace` — the same call shape `verify.Run` uses (`internal/verify/verify.go:238-242`) — under `timeouts.verify`, since it is a check and not a render. Its output is captured as a redacted log tail (`logtail`, already imported by the gate) and quoted in the report when it fails.

`outputs.probe: none` (or a blank `commands.probe`) means the probe is skipped and the record says `{ran: false}` (Review Focus 6's second half) — the design's §3 choice: the structural checks are still there, and the report says no probe ran.

- [ ] **Step 1: Write the failing tests**

```go
// internal/runner/artifact_test.go
func TestProbeFailureFailsTheRunAndUploadsNothing(t *testing.T) // Review Focus 6
func TestProbeEnvironmentNamesTheStagedSet(t *testing.T)        // the probe writes its env to a file; assert both variables
func TestNoProbeIsRecordedAsRanFalse(t *testing.T)
func TestProbeTimeoutFailsTheRun(t *testing.T)
func TestProbeRunsOnlyOnceForTheWholeSet(t *testing.T)          // one process for N artifacts, asserted by a call counter
```
Run: `go test ./internal/runner -run 'TestProbe|TestNoProbe' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 6: Files in — `--attach`, the task spec, the download

**Files:**
- Edit: `internal/task/task.go` (the `Inputs` field), `schemas/task.schema.json`
- Test: `internal/task/task_test.go`
- Edit: `internal/cli/run.go` (the flag and the gate), Create: `internal/cli/run_attach.go`, `internal/cli/run_attach_test.go`
- Edit: `internal/runner/artifact.go` (the download), `internal/runner/artifact_test.go`

**Interfaces:**
```go
// package task
// Spec gains:
//   Inputs []InputFile `json:"inputs,omitempty"`
// InputFile is one attached file: what the launcher uploaded, under
// runs/<slug>/<run>/inputs/<name>, with the hash it read on the way up.
// A runner that predates this field refuses the spec (the decoder
// disallows unknown fields), which is why the launcher gates on the image
// (P9, checkImageSince).
type InputFile struct {
    Name   string `json:"name"`
    Bytes  int64  `json:"bytes"`
    SHA256 string `json:"sha256"`
}
```

```go
// package cli
// attachInputs uploads each path to runs/<slug>/<run>/inputs/<name> and
// returns the spec's Inputs. It runs between the run id's mint and
// CreateTask (P7), checks the workflow's caps before the first byte, and
// hashes as it uploads (blobx.PutFile).
func attachInputs(ctx context.Context, b *blobx.Bucket, prefix string, paths []string, caps config.Inputs) ([]task.InputFile, error)
```

The `--attach` flag is repeatable (`StringArray`) and resolves its repository exactly as a plain launch does — `--repo`, else the checkout's origin (`internal/cli/run.go:155`, `:520-523`), so the goal's `fugaro run --recipe sizzle --attach shot1.png …` needs no `--repo` at all. It refuses more paths than the workflow's `inputs.max_files` **before** uploading anything. The name for each attachment is `filepath.Base(path)` after the same sanitiser shape Task 4 uses; two attachments with the same base name are refused rather than overwritten. The image gate (P9) runs with the rest of the launch's gates: `checkImageSince(..., artifactsSince, "--attach", …)` — the same message shape, naming `fugaro image refresh --repo %s --workflow %s`.

The runner's side: before the first step, `downloadInputs` reads `spec.Inputs` and **re-applies both refusals the launcher applied** — the caps (a list longer or larger than the workflow's, re-checked from the spec) and **the name refusals**, with the same list Task 4's `sanitiseName` uses, because an input name becomes a filesystem path: a spec whose `inputs[].name` is `../../.git/hooks/pre-commit`, absolute, NUL-bearing or over-long is refused and **fails the run**, never cleaned into something plausible and never written outside `inputs/`. Each accepted name is then downloaded from `runs/<slug>/<run>/inputs/<name>` into `<RepoDir>/inputs/<name>` with `blobx.GetFile`, **passing the spec's `sha256` as the expected hash** so a mismatch leaves nothing at the destination. A file the bucket has but the spec does not is not downloaded at all (it cannot be named), which is the whole reason the list lives in the spec. `inputs/` is written into `.git/info/exclude` (appended once, idempotently) so an artifact run can never commit a screenshot, and `FUGARO_INPUTS_DIR` joins the agent's environment for the plan stage. The record's `inputs` are the spec's list, echoed, so a reader sees exactly what the run was given.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/run_attach_test.go
func TestAttachUploadsBeforeTheTaskSpecIsWritten(t *testing.T) // Review Focus: the order, asserted from recorded calls
func TestAttachRefusesMoreFilesThanTheCap(t *testing.T)
func TestAttachRefusesDuplicateBaseNames(t *testing.T)
func TestAttachNeedsAResolvableRepo(t *testing.T) // the origin is enough (P7); a checkout with neither is the only refusal
func TestAttachGatesOnTheImageThatKnowsInputs(t *testing.T)   // P9
```
```go
// internal/runner/artifact_test.go
func TestInputsAreDownloadedHashedAndExcludedFromGit(t *testing.T)
func TestAnInputWhoseHashDoesNotMatchFailsTheRun(t *testing.T) // Review Focus 5, the other direction
func TestASpecClaimingFewerInputsDownloadsOnlyThose(t *testing.T) // Review Focus 4
func TestInputsOverTheCapFailTheRun(t *testing.T)
func TestAnInputNameThatEscapesInputsIsRefused(t *testing.T) // Review Focus 3's list, on the spec's side: ../../.git/hooks/pre-commit, absolute, NUL, over-long
func TestAnInputNameIsNeverCleanedIntoSomethingPlausible(t *testing.T) // refused and the run failed, not silently rewritten
```
Run: `go test ./internal/cli -run 'TestAttach' -count=1` and `go test ./internal/runner -run 'TestInputs|TestAnInput' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 7: `fugaro artifacts ls|get`, and the display in `runs ls` and `diagnose`

**Files:**
- Create: `internal/cli/artifacts.go`, `internal/cli/artifacts_test.go`
- Edit: `internal/cli/runs.go` (the listing's artifact column/field), `internal/cli/diagnose.go` (the paths it prints), `internal/cli/runview.go` (the view's artifact fields), and their tests
- Edit: `internal/cli/root.go` (the subcommand registration)

**Interfaces:**
```go
// fugaro artifacts ls RUN [--repo R] [--json]
// fugaro artifacts get RUN [NAME] [--repo R] [--out DIR] [--force]
//
// ls reads result.json and prints its own record — never a bucket
// listing: name, bytes, short sha256, recorded content type, the probe's
// exit code, and a line saying how many the record has. --json prints
// runstore.ArtifactFile verbatim.
//
// get downloads one artifact (or all of them with no NAME) into --out
// (default "."), verifying the byte count and the sha256 against the
// record. blobx.GetFile takes the record's hash as its expected sum and
// renames into place only once it matches, so a mismatch is an error
// naming both hashes with nothing ever written at the destination path. An existing file needs --force. Nothing downloaded is ever
// opened, executed or unpacked: the command prints the recorded type, the
// size and the hash and stops there.
func newArtifactsCmd() *cobra.Command
```

`runs ls` gains the artifact count and total bytes in its `--json` view and, in the table, nothing unless the run has artifacts (so the common case's columns do not change) — the same rule `runview` already follows for optional fields. `diagnose` prints each artifact's path beside `report.md` (`internal/cli/diagnose.go:158`), read from the record.

Its `--repo` resolution and its `--json` shape follow `fugaro runs ls` exactly; both commands read through `runview`/`runstore` over the bucket the installation's config names, so a `file://` bucket works in tests.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/artifacts_test.go
func TestArtifactsLsPrintsTheRecordNotTheBucket(t *testing.T) // a record with no object still lists; a get then says it is missing
func TestArtifactsGetVerifiesTheHash(t *testing.T)            // Review Focus 9
func TestArtifactsGetWritesNothingOnAMismatch(t *testing.T)
func TestArtifactsGetRefusesAnEscapingNameFromTheRecord(t *testing.T)
func TestArtifactsGetNeedsForceToOverwrite(t *testing.T)
func TestArtifactsGetNeverExecutesWhatItWrites(t *testing.T)
func TestArtifactsGetRefusesOverTheCapBeforeReadingTheBody(t *testing.T)
func TestRunsLsShowsArtifactCountOnlyWhenThereAreArtifacts(t *testing.T)
```
Run: `go test ./internal/cli -run 'TestArtifacts|TestRunsLsShowsArtifact' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 8: `budget preview`'s compute ceiling

**Files:**
- Edit: `internal/cli/budget_preview.go`, `internal/cli/budget_preview_test.go`

**Interfaces:** no new exported API. The preview gains one line per workflow, printed after the existing ceiling line (`internal/cli/budget_preview.go:26-28`) and before the "typical" line (`:489-538`):

```
compute ceiling: 8 vCPU x 32 GiB x 2h0m0s = $X.XX at this installation's compute prices — NOT CAPPED
                 (bounded by timeouts.total, not by budget.per_run_usd; see SECURITY.md)
```

The arithmetic is `pricing.ComputeUSD(cpu, memGiB, total)` — the same function the runner's cost estimate uses (`internal/runner/cost.go:62-64`, `internal/backend/backend.go:172-177`) — with the workflow's `resources` and its `timeouts.total`, **not** `timeouts.render`: the line is the worst case a run can bill, and a render that overruns its own step still burns the run's clock until the deadline. Prices come from the same source the runner's do (the installation's `FUGARO_COMPUTE_PRICES`, `internal/infra/spec.go:46,682`); when they are unset the line is printed with the price source named and no dollars, rather than a wrong number.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/budget_preview_test.go
func TestBudgetPreviewPrintsTheComputeCeiling(t *testing.T)
func TestBudgetPreviewSaysTheCeilingIsNotCapped(t *testing.T) // the words "NOT CAPPED" and the SECURITY.md pointer
func TestBudgetPreviewComputeCeilingFollowsTheWorkflowsResources(t *testing.T) // 4vCPU/8Gi vs 8vCPU/32Gi double
func TestBudgetPreviewWithoutPricesNamesTheSourceInsteadOfGuessing(t *testing.T)
func TestBudgetPreviewArtifactRecipeDoesNotChangeTheModelCeiling(t *testing.T) // D4: model caps unchanged
```
Run: `go test ./internal/cli -run 'TestBudgetPreview' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 9: Docs, the skills, SECURITY.md, and Check 58 in the checklist

**Files:**
- Create: `docs/artifact-runs.md` (the user-facing guide)
- Edit: `docs/recipes.md` (the format table gains `mode: artifact`, `plan`, `render`, `outputs`), `docs/base-image.md` (ffmpeg through `image.apt`, as a worked example)
- Edit: `SECURITY.md` — **the two paragraphs of design §8.7, verbatim**: the artifact paragraph in "### The assumption" and the "an artifact run's compute is not capped" bullet in "Known limits"
- Edit: `docs/gcp-live-checklist.md` — Check 58's row in the table and its section, below
- Edit: `plugin/skills/routing/SKILL.md` — its "Choosing a recipe" section (`:47`) gains one paragraph on when an artifact recipe is the right answer, and the words "attach a file only if you would paste it into a pull request on a repository every launcher can read"
- Edit: `docs/release.md` — one plan-shape bullet: the release note names the new recipe mode, the new CLI subcommand and the fact that the caps count model dollars only

**Ordering constraint, as in every plan here:** the docs that name `--attach`, `fugaro artifacts ls|get` and the new keys land **after** Tasks 1–8, because the docs and skill lints resolve what they read (`docs_base_test.go:173`, `plugin/skills_lint_test.go`). `docs/artifact-runs.md` is not in `trackedDocCommands` until someone adds it; when it is added, every flag it names must exist.

**The user guide is short and answers five things**, in this order: what an artifact run is (and that it has no pull request); the recipe (`mode: artifact`, `plan`, `render`, `outputs`) with the ffmpeg example; the attachments and **the warning of design §4 verbatim**; what the run records and how to read it back (`fugaro artifacts ls|get`, the sha256, the retention window); and what is not built (GPUs, signed URLs, compute inside the cap, repository-less runs) with a pointer to the design's §13.

**Check 58's row goes in the table beside the others**, and its section is written in full: **seven numbered steps**, each preceded by a re-run of **Check 43's guard block** (`docs/gcp-live-checklist.md:1437-1458`) — that is the file's own shape, not a copy of it here.

**The file is the single source of the wording.** This task authors the row and the section; Task 10 runs them and does not quote them back, because a plan that repeats a check's steps is how the two drift (`docs/plans/2026-10-14-openrouter-senior-reviewer.md:146` — that drift was a review finding, and dropping the quote was the fix). What the section must keep asserting, in whatever wording it has:

- **The guard block, verbatim and re-run at the start of every step**: `P=fugaro-dev` **hard-coded and never read from `gcloud`**, `L`/`R`/`S` from the installation, `${VAR:?}` on every argument, the three hard stops (all variables non-empty; `fugaro doctor --project fugaro`'s `project:` line names `fugaro-dev`; the checkout's `origin` is `edgeappinc/fugarosandbox`), `--project fugaro` on every Fugaro command that takes it, **never `belong`** (`:1431`), **every step the owner's and none run by an agent**, and a Restore as the last step.
- **Step 1, the sandbox's prerequisites** (the only step that changes the sandbox's configuration): `image.apt: [ffmpeg]` on the workflow, one `fugaro image refresh`, and the fixture artifact recipe, render script and probe committed to the sandbox. The FACT is the image's digest and the job naming it — **this is the step that makes the ffmpeg claim true on a real image (U2), and it is the one Cloud Build the check pays for.**
- **Step 2, files in**: the three PNGs, and the launch. **The generator is written into the section as a real command, not as a description** — a `python3` one-liner over `zlib`/`struct` writing three 320×240 8-bit RGB PNGs, which is exactly what was run while the design was written (V22: three valid PNGs, 865 bytes each) — followed by `fugaro run --workflow <the one with ffmpeg> --recipe sizzle-check --attach a.png --attach b.png --attach c.png` **with no `--repo`**, since a plain launch resolves the repository from the checkout (plan P7's note). The FACT is the run id and `task.json`'s `inputs` against `sha256sum` of the three files.
- **Step 3, the render record**: `outcome: artifacts`; `render.command` is the repository's command; `render.exit_code` 0; `render.duration_s`; `cost.model_usd` (the plan stage alone) beside `cost.compute_usd` with `compute_estimated: true`; **no branch pushed and no pull request opened**; and the job's own CPU/memory matching the workflow's `resources`.
- **Step 4, the probe**, two halves: the record's `artifacts[].probe` (`ran: true`, exit 0) **and** an independent local `ffprobe` of the file `fugaro artifacts get` wrote — duration and resolution.
- **Step 5, the recorded content type** against the object's own (U5).
- **Step 6, retrieval**: the file comes back, `sha256sum` equals the record's `sha256`, and the **upload's wall clock** is measured against `timeouts.finalize_reserve` (U6). The row also records that the run's objects are inside `runs/` and expire with it.
- **Step 7, the failure direction, both halves (U3)**: a probe that demands an impossible duration must end the run `failed`/`none` with **nothing under `artifacts/`**, and a set over `max_bytes` must refuse with the cap named, upload nothing, and leave the container alive.
- **The Restore**, last: the run prefixes, the local PNGs and video, the `image.apt` revert (noting the derived image stays until the next refresh), and the fixture-recipe commit — and a sentence saying whether the check created anything in the installation's IAM, bucket, lifecycle or jobs (**it should not**).

- [ ] **Step 1: Write the docs, SECURITY.md and the skill edits.**
- [ ] **Step 2: Write Check 58's row and section** — the guard block, the seven steps and the assertions list above, in the checklist's own wording and its own conventions (`| 58 | … | See "Check 58" below; **you** run it … **USER-RUN, NOT RUN.** |`).
- [ ] **Step 3: Run the docs tests.** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`, and `go test ./plugin -run 'Lint|Skill' -count=1`. Expected: `ok`.
- [ ] **Step 4: Commit.**

---

### Task 10: Owner-run live check 58 (not part of this PR)

**`docs/gcp-live-checklist.md` Check 58 is the check this task runs, and the file is the only copy of its steps** (`docs/plans/2026-10-14-openrouter-senior-reviewer.md:146`'s rule). Confirms [docs/design/artifact-runs.md](../design/artifact-runs.md) §3–§9 against the real substrate, not only the fakes, and settles the design's UNVERIFIED U2, U3, U5 and U6. **Never `belong`**; sandbox repository only; installation `fugaro`; every step yours, none an agent's.

Four obligations, and nothing else:

1. **Run Check 58 as its section is written**, from the sandbox checkout, in a script file rather than an interactive shell (so the guard block's comments are honoured and `exit 1` does not close a window — the reason Check 43 states at `:1470`).
2. **Paste every `FACT` line the section asks for** into that section and into the table row, in place of the placeholder: the image digest, the `task.json` inputs against `sha256sum`, the `render` record and the counters, both halves of the probe's verdict, the recorded and the object's own content type, the retrieved file's hash and the upload's wall clock, and the two failure runs' reason lines. Never a secret value, never a screenshot's contents.
3. **Keep the section's facts and the design in step:** a wrong expectation is a Task 1–9 bug, and the fix is the code or the design, not the check's wording.
4. **Restore, as the section's last step says.** If it turns out that something was created that its Restore does not undo, that is a finding for the design (the check claims it creates nothing in the installation's IAM, bucket, lifecycle or jobs) — record it in the row rather than silently cleaning up.

- [ ] **Step 1: Run it at your own terminal, on the sandbox only.**
- [ ] **Step 2: Paste the `FACT` lines into the checklist's Check 58 section and its table row.**
- [ ] **Step 3: Commit.**

---

### Task 11: Full suite

- [ ] **Step 1:** `go test ./... -count=1 -timeout 20m`
- [ ] **Step 2:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] **Step 3:** `gofmt -l .`
- [ ] **Step 4:** `fugaro verify build`, then `fugaro verify test` (the repository's configured commands), and commit.

---

## Self-review

What this plan knows it is unsure of, so the reviewer does not have to find it:

1. **The `plan` step's name and its stage.** `plan` is a new step kind whose only content is "run one agent stage here". If the reviewer prefers the implement-analogue (the agent stage is implicit and `steps: [render]` always means plan-then-render with a separate key to opt out), the change is local to Task 1 and Task 2 and does not touch Tasks 3–8. **Decided as written (P3) because a zero-model artifact run is worth having and "no agent" must be spellable.**
2. **`timeouts.render`'s default.** `min(verify, total − finalize_reserve)` is the shape the other three use, and it is the conservative side: on a repository that does not set it, a render gets the verify budget, not the whole run. An owner with a two-hour render sets `timeouts.total` and `timeouts.render` together, and the plan's docs say so. **If the owner would rather the default be `total − finalize_reserve`, that is one line in Task 1 and no other task changes.**
3. **The caps' numbers** (8 files / 256 MiB out, 16 files / 256 MiB in). They are defaults with ceilings (P6); the task that could invalidate them is Task 10 step 6, which measures the upload's real wall clock against `finalize_reserve`.
4. **The probe's environment variables are the interface.** `FUGARO_OUT_DIR` + `FUGARO_ARTIFACTS` (newline-separated) is the whole contract with a repository's script, and it is deliberately dumb (no JSON, no arguments). If a reviewer wants per-file arguments instead, that is P4 and Task 5 alone.
5. **The staging directory's location** (`<StateDir>/artifacts/`) is chosen for parity with `verify/`; nothing else depends on it, and it is not uploaded itself — only the individual files are.
6. **What this plan does not do, deliberately:** no GPU (D3, U1), no compute inside the cap (D4), no retention prefix (D2's alternative), no signed URLs (D7), no repository-less runs (D1's alternative), no general `outputs:` on implement recipes (D5's alternative), and no artifact registry or cross-run index. Each is named in the design's §13/§14 with its cost, so a reviewer can rule on it there rather than in a task.
7. **The one claim in this plan that could not be verified offline** is whether `mise` can install `ffmpeg` (U2). The plan does not use it: the docs (Task 9) and the check (Task 10) use `image.apt`, which is a shipped mechanism (`images/derived/Dockerfile.tmpl:26-33`). Task 10's FACT line records the digest, which is what makes the ffmpeg claim true on a real image rather than plausible on a template.
8. **The numbers in Check 58's name and its neighbours are checked, not assumed** (`docs/gcp-live-checklist.md`: rows 42–47 and 57 exist, 57 is the compute backend's, 48–56 and 58 are free). If a later plan takes 58 before this one lands, this plan's check renumbers and nothing else changes.
