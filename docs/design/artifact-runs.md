# Artifact runs: a run whose deliverable is a file

Status: **design for review (2026-10-15) — nothing here is implemented.** This
document and its plan ([../plans/2026-10-15-artifact-runs.md](../plans/2026-10-15-artifact-runs.md))
are the whole of this change: no Go, no Terraform, no `fugaro.yaml`, no schema
file and no live check is edited by the pull request that adds them. Every
claim about the code is cited to the file and line read for it (2026-10-15,
at `main`'s `dd049c3`); everything that could not be verified offline is
written **UNVERIFIED** with the check that would settle it.

**The origin, in the owner's words (2026-10-15).** *"We want to create sizzle
videos. We take various screenshots, send Claude in the cloud with those
screenshots to run FFmpeg and produce a video, which is a long, computationally
expensive process, so it is perfect to ship to the cloud. At the end, instead
of a PR, I should be able to get some artifact back through GCS, through the
object store."* Generalised, and that generalisation is what this design
designs: **any run whose result is a file** — a rendered video, a generated
report, a dataset, a build artifact — with **files in** as well as files out.

Related: [generic-tool.md](generic-tool.md) §3.1 (the recipe format, `mode:
review` (G12) as the precedent for a run whose outcome is not a Fugaro pull
request, and the `check` step's "a recipe names no command" rule), [recipes.md](recipes.md)
(where a recipe may live), [bucket-iam.md](bucket-iam.md) (who may read and
write the runs bucket, §9's L6 and §10's L3), [base-image.md](base-image.md)
(`image.apt`, `mise.toml`, the derived image), [compute-backend.md](compute-backend.md)
(the Docker-capable VM substrate, and §6.3's explicit exclusion of GPUs),
[project-centric.md](project-centric.md) §4–§5 and §11 phase 3 (profiles and
shared project jobs — what a *repository-less* run would need),
[m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) §4–§5 and
[m9-spec-v3-reconciliation.md](m9-spec-v3-reconciliation.md) (the caps count
model dollars only), [v1.md](v1.md) §3.4 and §4.6 (the backend seam and the
run record), and [gcp-live-checklist.md](../gcp-live-checklist.md) (Check 43's
guard block, which Check 58 imitates).

## 1. What exists today, read from the code

**A run is a Cloud Run job, one per workflow variant, and its records are
objects under one prefix.** `runstore.Open` (`internal/runstore/runstore.go:386-387`)
is the whole of the layout: `prefix = path.Join("runs", repoSlug, runID) + "/"`,
and every file is `prefix + name`. What lives there today: `task.json`
(`:502`, `:511`), `result.json` (`:492`, `:529`), `launch.json`
(`internal/runstore/launch.go:29`), the `launching` claim marker
(`launch.go:61`), the `cancel` marker (`runstore.go:547`), `report.md`
(`internal/runner/runner.go:2132`, `text/markdown`), one object per stage's
transcript — `transcripts/<name>-<n>.jsonl`, `application/x-ndjson`
(`runner.go:1710`) — the resumed session (`internal/runstore/session.go:39`,
`session/session.json`) and the verify records the run uploads,
`verify/<n>.json` (`runner.go:2192`). The budget token is one object beside
them (`internal/budget/token/object.go:15`). **So "a file produced by a run"
already has an obvious home — `runs/<slug>/<run>/` — and the only thing
missing is that nothing ever *declares* which files are the deliverable.**

**The object store is already an abstraction.** `blobx.Open` takes a `gs://`
**or** `file://` URL (`blobx.Open`, called at `internal/cli/exec.go:146,296`;
its default is the `--bucket` flag / `FUGARO_BUCKET`, `:59`), which is how
the e2e suite runs a whole run against a directory
(`internal/e2e/budget_test.go:129`). An artifact path built on `blobx` is
therefore testable end to end with no cloud, and the local `fugaro exec` mode
gains it for free.

**A recipe already knows two things this design needs.**

- **`mode:`** is `implement | review` (`internal/recipe/recipe.go:37-40`,
  parsed at `:356-362`, enum at `schemas/recipe.schema.json`), and `review` is
  **exactly the precedent**: a run whose outcome is not a Fugaro pull request
  and that **needs no git token** — *"It never pushes and never edits the PR.
  The agent's environment has no git token, exactly as a follow-up's has
  none"* (`docs/design/generic-tool.md:198`, G12 at `:358`). The code agrees:
  the runner skips `gitops.WithVars` when the run is a review run
  (`internal/runner/runner.go:635`), and the outcome is a new value,
  `reviewed`.
- **the `check` step** is the precedent for **deterministic work inside a run**:
  `StepCheck` (`internal/recipe/recipe.go:47-53`), run by `(*run).runGate`
  (`internal/runner/gate.go:132`) *before* implement, through `verify.Run`
  (`internal/verify/verify.go:176`), as the agent's user, **in the run's
  checkout** (`Dir: s.RepoDir`, `verify.go:243`). The command is
  `commands.build|test|lint` of the repository's own `fugaro.yaml`
  (`gate.go:61`), and the reason is a safety rule this design must not break:
  *"Every command it runs comes from Settings … never from the task or the
  recipe (a check step only ever names a `commands.*` key)"* (`gate.go:125-127`;
  `docs/design/generic-tool.md:356` (G10) rejects "commands in the recipe" in as many
  words: *bucket text would run code*).

**A recipe may not name a command, and a check step may only come first.** In the
two of `schemas/recipe.schema.json`'s **four** `steps` variants that have a check
step at all, it is `prefixItems[0]` (`$defs.check`), and the other two variants
begin with `first_line` — so a deterministic step *after* the agent has not been
expressible at all, which is precisely what a render needs.

**Timeouts are four numbers, and the run's clock is what really bounds it.**
`timeouts: {total, stage, verify, finalize_reserve}` (`internal/config/config.go:423-428`);
defaults `total = 90m`, `finalize_reserve = min(5m, total/5)`, `stage = min(40m, total)`,
`verify = min(30m, total)` (`internal/config/defaults.go:77-90`). This
repository's own `fugaro.yaml` raises them to `total: 2h, stage: 90m, verify:
25m, finalize_reserve: 5m` (`fugaro.yaml:54`). The runner turns `total` and
`finalize_reserve` into one wall-clock deadline — `finalizeAt = Start + (Total - Reserve)`
and `StageContext` capped by it (`internal/runner/budget.go:22-34`) — and the
launcher tells Cloud Run a task timeout of `total + 2m` so the two agree
(`docs/design/v1.md:370`; the CLI caps a request at 24 hours, `OverrideCap`,
`internal/cli/run.go:976-991`). A Cloud Run job task can run up to 24 hours
(`docs/SPEC.md:30`).

**Cloud Run job resources are validated, and 8 vCPU / 32 GiB is the ceiling.**
The workflow's `resources: {cpu, memory}` defaults to `4` / `8Gi`
(`internal/config/defaults.go:67-71`), reaches Terraform as the job's limits
(`deploy/terraform/gcp/modules/workflow/job.tf:38-42`) and is checked by
`CheckResources`: CPU must be **one of 1, 2, 4, 6, 8** and memory at most 32Gi,
with a memory↔CPU ratio rule (`internal/backend/gcp/limits.go:22,36-38,55`;
`limits_test.go:11` asserts `{8, "32Gi"}` passes and `limits_test.go:23` that
`{8, "64Gi"}` fails). So **VERIFIED, in this repository's own code: an artifact
render on today's substrate gets at most 8 vCPU and 32 GiB.**

**The tools situation.** The base image installs **mise**
(`images/base/Dockerfile:39-41` pins `MISE_VERSION`, `:154-168` installs it to
`/usr/local/bin/mise`), and a repository declares runtimes either in its own
`mise.toml` or in `image.tools` (`docs/base-image.md:36-47`). **ffmpeg is
nowhere in this repository** — a case-insensitive grep for `ffmpeg` over the
tree returns zero hits — and the mechanism to add it already exists three
ways: `image.apt` renders an apt layer into the derived image
(`images/derived/Dockerfile.tmpl:26-33`, `docs/base-image.md:63`), `image.setup`
runs arbitrary setup commands, and `dockerfile:` lets a repository own its
Dockerfile (`internal/image/render.go:92-117`). `fugaro image build` exists
both cloud-side and `--local` (`internal/cli/image.go:74-78`).

**The budget's caps count model dollars only, and compute is a reported
estimate.** The caps are per-run, per-repository-day and global-day, in
micro-dollars (`internal/budget/model.go:150-165`), enforced per lease
(`internal/budget/evaluate.go:190-204,209`) and, in `enforce`, by halting the
run (`internal/runner/budget_session.go:230`; reasons `run_cap`,
`repo_daily_cap`, `global_daily_cap` at `internal/runstore/runstore.go:45-48`).
**Compute is deliberately outside them**: *"Caps count model dollars only …
Compute is reported separately as `computeUsd` … never capped"*
(`docs/design/m9-spec-v3-reconciliation.md:10`), *"Compute — an estimate …
Never capped"* (`docs/design/m9-budget-and-dashboard.md:798`). The estimate
itself is real and shipped: `Prices.ComputeUSD(cpu, memGiB, d)`
(`internal/backend/backend.go:172-177`), computed by the runner from the
workflow's **requested** vCPU/memory and the elapsed run time
(`internal/runner/cost.go:62-64`), recorded as `cost.compute_usd` +
`cost.compute_estimated` (`internal/runstore/cost.go:12-18`,
`schemas/result.schema.json`), recomputed by the CLI from the backend's own
limits (`internal/runview/runview.go:363-368`) and rolled into history
(`internal/budget/rollup.go:296-303`, which folds `ComputeUSD` into
`ComputeMicros` only when `ComputeEstimated`). **`fugaro budget preview` has no compute
notion at all**: it prints a ceiling of `N × min(per-run cap, stages ×
agent.max_budget_usd)` (`internal/cli/budget_preview.go:26-28`) and a "typical"
line read from `result.json` stages' **model** spend only (`:489-538`).

**The runs bucket's readers and writers, exactly.** Its lifecycle rule deletes
objects under `runs/` at `bucket_lifecycle.runs_days`, **default 90**
(`deploy/terraform/gcp/modules/installation/bucket.tf:16-24`,
`variables.tf:109-121`) — and that is the *whole* run record: `result.json`,
`report.md` and the transcripts expire with the artifacts. IAM
(`docs/design/bucket-iam.md` §H4, `:126-128`): an operator holds
`objectAdmin` unconditioned; a launcher who is not an operator holds
`objectViewer` on the whole bucket plus `objectUser` on `runs/`; the history
account holds `objectViewer` unconditioned. **The job's own grant is
per-repository, not per-run**: `google_storage_bucket_iam_member.job`
(`deploy/terraform/gcp/modules/workflow/sa.tf:24-33`) carries
`gcp.BucketCondition(bucket, gcp.JobBucketPrefixes, slug)`
(`internal/backend/gcp/names.go:383-389`), where `JobBucketPrefixes` is
`{"runs", "cache", "locks"}` (`:374`) — so the condition is
`resource.name.startsWith("projects/_/buckets/<bucket>/objects/runs/<slug>/")`
**and the run id is not part of it**. One repository's runs share one account
and one prefix, which SECURITY.md already says in as many words:
*"Runs of one repository can see each other"* (`SECURITY.md:62`).

**What does not exist, checked rather than assumed** (this is the list the
brief asked to be verified, and two of its entries were wrong):

| Claim | Verdict |
|---|---|
| An artifact concept in the recipe format | **Does not exist.** No `outputs`, `artifacts` or glob key in `schemas/recipe.schema.json` (`additionalProperties: false`; the allowed keys are `version, name, description, use_when, mode, roles, steps`) or in `internal/recipe/recipe.go`. |
| An artifact concept in `generic-tool.md`, `recipes.md` or `v1.md` | **Does not exist.** Zero hits for artifact/attachment/output-glob in `generic-tool.md` and `recipes.md`; `v1.md`'s 15 hits are **Artifact Registry**, the GCP service — with one exception that is not: `ARTIFACTORY_TOKEN` at `v1.md:542` is a JFrog Artifactory token, so "all" would be wrong. |
| An artifact field in the run record | **Does not exist.** `schemas/result.schema.json` has none, and no package writes one. |
| A `fugaro artifacts` command | **Does not exist.** No such subcommand anywhere in `internal/cli`. |
| A signed URL anywhere | **Does not exist.** No `SignedURL`/`URLSigner`/`GenerateSigned` call in `internal/` or `cmd/`; the only signing is `iam.serviceAccounts.signJwt` for the budget identity's Firebase token (`deploy/terraform/gcp/modules/firebase/iam.tf:23`) — **`signBlob`, which a V4 signed URL needs, is granted nowhere.** |
| `fugaro runs ls` / `fugaro diagnose` list the run's objects | **They do not.** `runs ls` reads specific files (`internal/cli/runs.go:379-390`) and never enumerates; `diagnose` prints *paths* (`ReportPath`, `CommentsPath`, `internal/cli/diagnose.go:158,177`) and lists nothing. |
| A sha256 of an output blob | **Nowhere.** Every existing sha256 is a naming hash, a cache key or a config/recipe digest; no blob is hashed. Content type **is** recorded, implicitly, as the object's own content type on write (`internal/blobx/blobx.go:111`), never in `result.json`. |
| "the job's service account can write only its own run's prefix" | **Wrong as stated:** the condition is the **repository's** prefix (`runs/<slug>/`, plus `cache/`, `locks/`), shared by all of that repository's runs (`names.go:374,383-389`). |
| "Cloud Run job CPU/memory limits (up to 8 vCPU and 32 GiB)" | **VERIFIED in this repository's code** (`internal/backend/gcp/limits.go:36-38,55` and its test). |
| "the compute-instance backend … Tasks 1-3 implemented behind a flag" | **Partly wrong:** the design is merged (#375) and Tasks 1 and 2 are merged (#380, #381); **Task 3 (the VM price table) is not** (`internal/backend/batch/batch.go:19-21` says so at the site), and the gate is not a flag but a **config refusal** — `backend: cloud-batch` is refused by `internal/localcfg/localcfg.go:711-719`. |
| "GPUs" | **UNVERIFIED, and promised by nothing.** `compute-backend.md` §6.3 lists *"Windows VMs, GPUs, TPUs"* as out of scope (`:1485`). See §5.4 and §13. |
| "`mode: review` is the PRECEDENT of a run that needs no git token" | **VERIFIED** (`docs/design/generic-tool.md:198`, `internal/runner/runner.go:635`). |

## 2. What is missing, in one list

1. **A declaration of outputs.** Nothing says "the deliverable is `out/*.mp4`".
2. **A deterministic step that may run *after* the agent**, long, and outside
   every model budget.
3. **An upload and a record of it**: bytes, size, sha256, content type, in
   `result.json`, under the run's own prefix.
4. **A retrieval path**: `fugaro artifacts ls|get`, with the record's own
   hashes checked on the way back.
5. **Files in**: `fugaro run --attach`, the object layout for inputs, the
   download into the run's workspace, and caps.
6. **Who may read it, and the warning that says so** — SECURITY.md and the
   user docs, because a screenshot is not a diff.
7. **A probe** that judges the output without a test suite: `ffprobe`-shaped,
   repository-owned, and the reason the run can say "succeeded" with no PR and
   no test.
8. **A byte-streaming write and read in `blobx`.** Every write today is a
   `[]byte` through `WriteAll` and every read is capped at one mebibyte
   (`internal/blobx/blobx.go:51-54,109-138,173-198,314`) — fine for locks,
   claims and `result.json`, and no way to move a video. Two new methods on
   the same type, no new dependency (§3).

## 3. The shape: `mode: artifact`, `outputs:`, and a `render` step

**The recipe (D5).** A new mode, mirroring `mode: review` exactly in what it
turns off, plus a new step kind and one new top-level block:

```yaml
version: 1
name: sizzle
description: Render a short video from attached screenshots with ffmpeg.
use_when: You have screenshots (or frames) and want a video file back, not a pull request.
mode: artifact
steps:
  - plan: {}       # the agent: writes the render plan, no push, no git token
  - render: {}     # deterministic: runs commands.render in the checkout
outputs:
  globs: ["out/*.mp4"]        # relative to the checkout; no absolute paths, no ..
  max_files: 8
  max_bytes: 268435456        # per artifact, 256 MiB
  probe: probe                # names commands.probe of the repository's fugaro.yaml
```

**What `mode: artifact` means, all of it** (each line the analogue of a line
`mode: review` already has, `docs/design/generic-tool.md:195-203`):

- **No pull request, ever.** No branch is pushed, no PR is opened or edited,
  and readiness is not a concept. `result.json`'s `outcome` gains
  **`artifacts`** — the same move `reviewed` made.
- **No git token in the agent's environment**, through the same branch
  (`internal/runner/runner.go:635`: `r.follow == nil && !r.review`) widened to
  `&& !r.artifact`. A git credential still exists for the *runner's* read-only
  fetch of the task's ref, exactly as a review run's does.
- **`implement`, `first_line`, `bounce` and `roles` are refused** — there is no
  loop and no PR to loop around — and `artifacts` on a non-artifact recipe is
  refused outright (the key is legal only here: D5).
- **The run's step list is `plan` then `render`**, in that order and no other.
  `plan` is an agent stage (the coder role, its own stage timeout, its own
  `agent.max_budget_usd`) whose *task* is written to say one thing: write a
  render plan into the checkout. `render` is the new deterministic step.
- **The files it produces are the deliverable**, and the run's report says so:
  what was rendered, its size, its sha256, and the probe's verdict.

**Why `plan` + `render` rather than a `render`-only recipe.** A `render`-only
recipe is the cheapest possible artifact run — **zero model dollars**, because
nothing in it calls a model — and it is exactly what the owner wants once their
`sizzle.sh` is settled. But the origin use case is *"send Claude in the cloud
with those screenshots"*: the value is that the agent writes the ffmpeg
invocation for *this* set of screenshots and this *task*. Both shapes are the
same recipe with one step removed, so the first slice ships both and the design
says which is which: `steps: [render: {}]` is legal and needs no agent, and
`steps: [plan: {}, render: {}]` is the sizzle case.

**The separation that keeps the safety rule intact: the agent writes *data*,
the repository owns the *code*.** `commands.render` is repository text from
`fugaro.yaml` at the task's ref — reviewed like code, never from the bucket,
never from the recipe (the rule at `internal/runner/gate.go:125-127`) — and
the agent's output is a **plan file** it consumes, e.g.
`commands.render: "sh .fugaro/render.sh \"$FUGARO_RENDER_PLAN\""`. *The
alternative, and what it costs:* let `commands.render` be whatever the agent
wrote (`sh render.sh` from the working tree). It works, it is shorter, and it
costs the property that makes the check gate safe — the text the runner
executes would be agent-authored rather than repository-authored. It is not a
privilege escalation (the agent already runs unconstrained commands in the same
container, with the same environment, as the same user — `SECURITY.md:42-52`),
but it is a new *mechanism* whose blast radius is "as trusted as the agent",
and the runner executing agent text is a thing this design would rather not
introduce for a plan file's worth of convenience. **Decision: the plan is data
and the script is repository text; a repository may set `commands.render` to
anything it likes, including `sh render.sh` of a file it owns.**

**The `render` step, precisely.**

- **It is a new step kind** (`StepRender`, `internal/recipe/recipe.go:47-53`),
  not a new `check.command` enum value: a `check` step is a *gate that must
  pass before implement* and may only be first (`schemas/recipe.schema.json`'s
  `prefixItems`), while a render is *the work*, comes last, and its failure
  is the run's failure. Reusing `check` would mean rebuilding its ordering
  rule, its failure path (which feeds the implement prompt,
  `docs/design/generic-tool.md:194`) and its "gate" semantics for the
  opposite purpose. *Alternative, and its cost:* add `render` to
  `check.command`'s enum and let a check step be last in artifact mode — one
  fewer concept, at the price of a step kind whose meaning depends on the
  mode it is in, which is exactly the kind of conditional the format has
  avoided so far.
- **It runs `commands.render`** (a new key in `schemas/fugaro.schema.json`'s
  `commands`, beside `build|test|lint|fix`), through the same executor the
  check gate uses — `sh -c`, the agent's user, the agent's environment, in the
  run's checkout — with its output captured to a log tail the report can quote
  (redacted, the existing `logtail`).
- **It has its own timeout**, a new `timeouts.render` (default
  `min(verify, total - finalize_reserve)`, and always capped by the run's own
  `finalizeAt`), because it is neither a stage (no model turn) nor a verify
  step (no test parser). **This is the load-bearing line of the whole design**:
  a two-hour ffmpeg render is not inside an agent turn, does not consume a
  stage timeout (90m here, `fugaro.yaml:54`), and does not touch
  `agent.max_budget_usd` or any model cap — it is one deterministic command
  under one deadline, inside the run's total.
- **Its exit code is the run's gate.** A failed render ends the run `failed`
  with outcome `none` — no artifacts, and the report says which command, how
  long, and the last lines of its output. A timed-out render is recorded as
  `timed_out` and is a failure.
- **It may run more than once? No.** One render step, once. A second attempt
  is a second run, and `--attach` plus a recipe that is already right makes
  that cheap.

**The `outputs` block**, and what each field buys:

| Field | Meaning | Why it is not optional |
|---|---|---|
| `globs` | The files to upload, relative to the checkout (`out/*.mp4`, `reports/*.pdf`) | Without it there is no deliverable, only a working tree. Refuses an absolute path, a `..` element and any pattern that escapes the checkout — the same refusal shape `dockerfile:` already uses (`schemas/fugaro.schema.json`'s `pattern: "^[^/]"` + a `(^|/)\.\.(/|$)`) |
| `max_files` | A count cap (proposed default 8, ceiling 32) | A glob that matches 100 000 frames is a disk-filling loop (bucket-iam §10's L6 already names "fill `runs/` with junk" as a known limit) |
| `max_bytes` | A per-artifact byte cap (proposed default 256 MiB, ceiling 2 GiB) | Same, and it bounds the upload's own time inside `finalize_reserve` |
| `probe` | Names a `commands.*` key (`commands.probe`) the runner runs once, after the render, before the upload | The `ffprobe` half of the use case: duration, resolution, non-empty — the run's "does the deliverable actually exist and look right" without a test suite. Optional; without it the structural checks (non-empty, within caps, sha256 recorded) are all there is, and the report says so. |
| `content_type` | Not declared: **recorded, from the file itself**, by extension with a sniffed fallback | §8: it is recorded, never trusted |

**The probe is `commands.probe`, run once with the whole set in two
environment variables** — `FUGARO_OUT_DIR` (the outputs directory's absolute
path) and `FUGARO_ARTIFACTS` (the newline-separated relative names, already
sanitised). The owner writes the check they trust, for example:

```yaml
commands:
  probe: |
    set -e
    while read -r f; do
      d=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$FUGARO_OUT_DIR/$f")
      awk -v d="$d" 'BEGIN { exit !(d > 4.5) }'
      test -s "$FUGARO_OUT_DIR/$f"
    done < "$FUGARO_ARTIFACTS"
```

*Alternatives, and their costs:* (a) a per-file probe invocation
(`sh -c '<command> "$1"' _ <path>`) — one process per artifact, and the
runner interpolating a name into a command line, which is the one thing to
avoid; (b) a built-in duration/resolution checker — a media parser in Go,
which is a dependency this repository does not have and a format list it would
have to maintain; (c) no probe at all — the run then cannot tell a 0-byte mp4
from a real one, and its "succeeded" is a claim about exit codes alone.
**Decision: one `commands.probe`, whole-set, via the environment.**

**The probe's result is recorded, not interpreted.** `result.json` gains a
`render` object (`{command, exit_code, timed_out, duration_s, started_at}`) and
an `artifacts` array (`[{name, bytes, sha256, content_type, probe}]`), each
**optional** so that every run written before this change still validates and
every reader that has not heard of them ignores them. **The compatibility
argument, in full**, because it is the one place this design touches a stored
format:

- `schemas/result.schema.json` is `additionalProperties: false` at the top
  level, so the new keys **must** be added to the schema — as optional
  properties, none of them in `required` (which stays `version, run_id, status,
  stage, outcome, cost_usd, started_at`).
- **No product code validates a stored record against that schema.** The
  schema is compiled only by `schemas/schemas_test.go` (`compile`,
  `schemas_test.go:23`), which marshals a fully-populated `runstore.Record` and
  validates it — so `TestResultSchemaAcceptsRunnerRecords` grows with the
  struct and is the test that keeps the two in step. Readers use the Go struct
  (`runstore.ReadRecord`, `internal/runstore/runstore.go:532`) and ignore
  unknown or absent fields, which is why **an older binary reads a newer
  record and a newer binary reads an older one, both without a change.**
- The only other reader-shaped surface is display: `runview` (`internal/runview/runview.go`),
  `runs ls`, `diagnose`, the report and the history rollup. Each shows the new
  facts when they are present and says nothing when they are not, which is the
  same rule `recipe`, `policy` and `budget` already follow.
- `task.json` gains one optional key in the same way: `inputs`, the list the
  launcher uploaded (§4). `schemas/task.schema.json` gets its properties.

**Where the artifacts land: `runs/<slug>/<run>/artifacts/<name>`,** with
`<name>` the file's path **relative to the outputs directory's own root**,
sanitised (§8). That is the one place the run's own account may write
(`runs/<slug>/`, §1's verified condition), it expires with the run record
(§7), and it needs no new IAM, no new lifecycle rule and no new prefix.

**And the bytes do not fit `blobx` as it stands — this is the one place the
existing object layer is too small, and it is a checked fact.** Every write in
`internal/blobx` takes a `[]byte` and goes through `WriteAll`
(`internal/blobx/blobx.go:109-138`, `:314`), and every read is capped at
`MaxReadBytes = 1 << 20` — **one mebibyte**, sized for locks, claims and
`result.json` (`blobx.go:51-54`, `:173-198`; `ReadMax` exists but is still a
buffer). So the change needs **two new methods on the same type, and no new
dependency**: a streaming write (`PutFile(ctx, key, path, contentType)` — a
`NewWriter` plus `io.Copy` from the file, hashing with a `MultiWriter` in the
same pass, so a 256 MiB render is never a 256 MiB slice in memory) and a
streaming read for `fugaro artifacts get` (`GetFile(ctx, key, path, sum, limit)`
— a `NewReader`, an `io.LimitReader` at the object's recorded size plus one, and
the **expected** sha256 as an argument, so a mismatch removes the temp file
instead of renaming it and the destination is never touched). Both are
testable against a `file://` bucket, and both preserve the properties the
existing methods have: a content type recorded on write, a cap that refuses
rather than truncates, and a classification of 403 as forbidden
(`isForbidden`, `blobx.go:200`).

## 4. Files in: `fugaro run --attach PATH...`

**`fugaro run` gains `--attach PATH` (repeatable)**, and the launch order
already supports it: the launcher mints the run id
(`task.NewRunID`, `internal/cli/run.go:819`), writes `task.json`
(`runstore.CreateTask`, `:1147`) and only then submits the execution
(`backend.Launch`, `:1392`). `--attach` fills the gap between the mint and the
write:

1. **Resolve the run id first** (the existing `--run-id` flag makes this
   explicit and repeatable-free).
2. **Check the caps before uploading anything**: `inputs.max_files` (proposed
   16) and a total byte cap (`inputs.max_bytes`, proposed 256 MiB) — the
   launcher refuses a set over the cap **before** the first byte is written,
   and the workflow declares both (`workflows.<name>.inputs`).
3. **Upload each file to `runs/<slug>/<run>/inputs/<name>`**, hashing it as it
   goes, recording `{name, bytes, sha256, content_type}` in the task spec.
   The launcher's own credentials suffice: a launcher holds `objectUser`
   under `runs/` (`docs/design/bucket-iam.md:126-128`) and an operator holds
   `objectAdmin` (`:88`) — **no new grant is needed to attach a file.**
4. **The runner downloads them before the first step**, into the checkout at
   `inputs/`, and **verifies every one against the sha256 the task spec
   recorded** — it does not trust the bucket's copy, and it enforces the same
   caps **and the same name refusals** again (§8.2), because the task spec is
   text a launcher wrote: a spec that claims 3 files cannot cause 300
   downloads, and a spec whose name is `../../.git/hooks/pre-commit` cannot
   write one byte outside `inputs/`.
5. **`inputs/` is excluded from git** — the runner writes it into
   `.git/info/exclude` — because an artifact run must never commit a
   screenshot. The plan step's prompt names it, and the environment gains
   `FUGARO_INPUTS_DIR`.
6. **The report and `result.json` record each input** (name, bytes, sha256) so
   a reader can see exactly what the run was given.

*Alternatives, and their costs:* **`--attach-stdin`/a directory flag** —
convenience only; **attaching after the launch** (an upload to a run already
running) — no: the run would have to poll for files it does not know the names
of, and a half-uploaded set is a race; **a bucket-arcade "drop a folder in
`inputs/` by hand"** — cheapest to build and worst to use, because a
hand-uploaded file has no task-spec record and no hash, so the run cannot tell
an intended input from a leftover, and the caps live nowhere.

**The screenshot warning, in the user docs, verbatim (the wording the design
commits to), because this is the one place an owner can leak something
casually:**

> **Attachments are as readable as the run.** An attached file is uploaded to
> the runs bucket, where *every launcher and every operator of this
> installation can read it*, and where every run of the same repository can
> read it too (the job's account is scoped to the repository's prefix, not to
> one run: `SECURITY.md`). Project Owners and Editors bypass the bucket's
> policy entirely. A screenshot is not a diff: it can show a token in a
> terminal, an API key in a browser tab, a customer's name, someone's face. Do
> not attach anything you would not paste into a pull request on a
> repository every launcher can read. Attachments also expire with the run
> (90 days by default), and so do the artifacts.

## 5. The key idea: the model does not babysit the render

### 5.1 Why this is the design and not a detail

An agent turn is the wrong place for a two-hour render, for four independent
reasons, each of which is a number in this repository:

1. **A stage has a timeout** (`timeouts.stage`, 90m in this repository's own
   `fugaro.yaml:54`, 40m by default, `internal/config/defaults.go:77-90`). A
   render inside a turn is killed by a limit that has nothing to do with it —
   and a stage that *does* finish eats the budget of the loop it is in.
2. **Every model turn costs model dollars**, and the caps are model dollars.
   Watching a render is the most expensive possible way to wait.
3. **A turn's output is a transcript**, so the render's progress and its log
   would be charged as output tokens to nobody's benefit.
4. **A turn is where the agent can stop early.** "The render takes 40 minutes,
   so let me approximate it" is a failure mode; a deterministic step has no
   such option.

So: **the agent plans, the runner renders.** The agent's output is a small
plan file; the compute is one command under one deadline; the probe is one
command after it; the upload is the runner's. The run's *model* spend for the
sizzle case is the plan stage alone.

### 5.2 What bounds the compute, precisely

| Bound | Value today | Where |
|---|---|---|
| The run's own wall clock | `finalizeAt = StartedAt + (total − finalize_reserve)` | `internal/runner/budget.go:22-34` |
| The render's own deadline | new `timeouts.render`, also capped by `finalizeAt` | this design |
| Cloud Run's task timeout | `total + 2m` as the launcher sends it; a task may run up to 24h; the CLI's `--total-timeout` cap is 24h | `docs/design/v1.md:370`, `docs/SPEC.md:30`, `internal/cli/run.go:976-991` |
| Cloud Run job resources | 1, 2, 4, 6 or 8 vCPU; ≤ 32Gi with ratio rules | `internal/backend/gcp/limits.go:22,36-38,55` |
| The container's own disk | **UNVERIFIED** — the ephemeral disk a Cloud Run job gets (and how Cloud Run counts the render's temp files against it) is not stated anywhere in this repository | the check that settles it is Check 58's step 7 (a deliberate over-cap render must fail cleanly, not fill the container) |
| The artifact caps | `outputs.max_files`, `outputs.max_bytes` | this design |

**A render that runs away is bounded by the clock, and only by the clock** —
which is exactly what §6 is about.

### 5.3 Which substrate: Cloud Run now, the VM backend when it hurts

**Recommendation: the Cloud Run job, unchanged, for the first slice (D3).**
The reasons are all facts: it is the shipped substrate (a run *is* a Cloud Run
job); the compute estimate already exists for it
(`internal/backend/backend.go:172-177`); 8 vCPU / 32 GiB / a 24h-class task
timeout is comfortably more than a sizzle video needs (three 320×240 PNGs into
a 5-second 1080p clip is seconds of ffmpeg, not minutes); and the workflow's
`resources` and `timeouts` are already per-workflow knobs, so an owner with one
heavy render gives *that workflow* 8 vCPU and a longer total without touching
anything else.

**The VM backend (compute-backend.md) is the answer to a specific failure, and
it is not ready.** It exists as a design (#375) with the name seam (Task 1,
#380) and the fake + conformance suite (Task 2, #381) merged; the VM price
table (Task 3) is **not** (`internal/backend/batch/batch.go:19-21`), and
`backend: cloud-batch` is refused by config validation until the enable task
(`internal/localcfg/localcfg.go:711-719`). Its value for a render is real and
narrow: **a Docker-capable VM** means the render can run in *any* container
image rather than in the repository's own derived image, the 8-vCPU ceiling
becomes a machine-type choice, and a very heavy render stops competing with
the repository's other runs for job-level resources. Its costs are the
design's own: one VM per run, the per-run service account, the reaper, the log
routing, and an installation-level Batch stage — Tasks 3–9 of that design.
**So the design's rule for the owner is: ship artifacts on Cloud Run; when a
render hits the 8-vCPU/32-GiB ceiling or needs a container the derived image
cannot carry, that is the trigger to finish the VM backend, and by then
`compute_usd` needs the VM's price table to stay honest (D3).**

### 5.4 GPUs: an open question, and nothing is promised

**UNVERIFIED, and deliberately not designed here.** `compute-backend.md` §6.3
puts *"Windows VMs, GPUs, TPUs"* explicitly out of scope (`:1485`), and
nothing in this repository mentions a GPU-backed Cloud Run job, an L4
attached to a Batch job, or a GPU quota. The owner asked whether GPUs are the
answer for a heavy render, and the honest answer is **"not verifiably, not
today, and not by this design"**: what is verifiable is that today's job
substrate has no GPU (its resource model is vCPU and memory,
`internal/backend/gcp/limits.go`), and that the VM design excludes them by
name. The checks that would settle it are (a) whether a Cloud Run job can
request a GPU in the owner's project/region, (b) whether a Compute Engine GPU
machine type is available in the sandbox's region and quota, and (c) whether
`compute_usd`'s price table has a GPU rate (it has `VCPUSecondUSD` and
`GiBSecondUSD`, `internal/backend/backend.go:167-168`, plus a `Source` string at
`:169` naming where the figures came from, and nothing else).
**§13 carries this as a question for the owner, and no task in the plan may
assume a GPU.**

## 6. The budget: what counts against a cap

**The facts, again, because everything else follows from them.** The caps
(per-run, per-repository-day, global-day) count **model** dollars; the
gateway's ledger feeds them; compute is a **reported estimate, never capped**
(`docs/design/m9-spec-v3-reconciliation.md:10`,
`docs/design/m9-budget-and-dashboard.md:798`,
`internal/budget/evaluate.go:190-204`). The estimate exists and is charged
nowhere: `internal/runner/cost.go:62-64` prices the workflow's **requested**
vCPU/memory over the run's elapsed wall clock.

**So a runaway render's only cap is the clock (D4).** Say the number plainly:
8 vCPU and 32 GiB for two hours, at the installation's own compute prices
(`FUGARO_COMPUTE_PRICES`, `internal/infra/spec.go:46`), is the worst case a
*single* artifact run can bill — and because the caps do not look at it, the
run can be at 2% of its model cap and 100% of its clock while it does it.
That is not a hole this design can close cheaply, and it is not a hole this
design creates: it is what "compute is not capped" means, and it is exactly
what the origin's *"long, computationally expensive process"* trades on.

**Recommendation: the timeout is the cap, in the first slice, and the design
says so at every surface** — in the docs, in the run's own report (a line
giving the workflow's requested vCPU/memory and the render's elapsed time and
the estimate), and in `budget preview`. The *alternative* — compute inside the
cap — is sketched in the spec (`docs/design/m9-spec-source-v3.md:493`: a
60-second heartbeat that reserves `vcpu × vCPU-second price + GiB × GiB-second
price` through the same lease flow) and its cost is real: a new halt reason, a
decision about which caps count compute (all three?), a per-backend price table
(the VM one does not exist yet — compute-backend Task 3), and an interaction
with the reservation sweep. **It is a separate design, and this one does not
pretend to have it.**

**What `fugaro budget preview` should say for such a run.** Today it prints a
ceiling built from model caps only (`internal/cli/budget_preview.go:26-28`) and
a "typical" line from stage model spend (`:489-538`), so for an artifact run it
**under-reports by the whole render**. The recommendation, as a task in the
plan: a preview line that prices the *compute* ceiling the same shape
`compute_usd` uses — `requested vCPU/memory × (timeouts.render +
estimate of the model stages)` at the installation's compute prices — clearly
labelled **"not capped"**, and with the "typical" line unchanged (it is
model-only history, and that is what it should stay). Its cost: preview
becomes backend-aware and needs the workflow's resources in a place it does
not currently read them.

## 7. Retention

**The fact: everything under `runs/` is deleted at `runs_days`, default 90**
(`deploy/terraform/gcp/modules/installation/bucket.tf:16-24`,
`variables.tf:109-121`). An artifact at `runs/<slug>/<run>/artifacts/...`
therefore lives **exactly as long as the run record it belongs to** —
`result.json`, `report.md`, the transcripts, the session and the artifact all
disappear in the same sweep. bucket-iam §10's L6 already treats "fill `runs/`
with junk" as bounded by this rule.

**Recommendation (D2): keep the artifacts under the run prefix and accept the
90-day window, and make the "keep it" path explicit in the docs.** The
reasoning is that the artifact and its record are one thing: an artifact that
outlived its `result.json` would be a file with no run, no sha256, no probe
verdict and no way to say who made it or what it was made from — and the
owner's deliverable is a *video*, not a record. The keep path is one command
the docs will name: `fugaro artifacts get <run> <name>` writes it to the
laptop with its sha256 verified, and `gcloud storage cp gs://…` copies it
anywhere if the operator prefers. *The alternative, and its cost:* a separate
top-level prefix (`artifacts/<slug>/…`) with its own lifecycle variable
(`artifacts_days`). It is the right answer for a deliverable that must outlive
its run, and it is not free: a new lifecycle rule in
`modules/installation/bucket.tf`, a new variable in `variables.tf`, and — the
expensive half — **every workflow job's IAM condition has to name the new
prefix**, because `gcp.JobBucketPrefixes` is a Go constant
(`internal/backend/gcp/names.go:374`) that `BucketCondition` bakes into each
job's grant (`:383-389`), so adding `"artifacts"` is a plan-and-apply per
repository, and it raises the question this design does not answer: which
account writes there, the run's (then the artifact is outside the run's own
prefix and the run id is not in the condition) or the launcher's (then a run
cannot publish its own output). *The third option — a separate bucket — is
strictly heavier than the second and buys nothing the second does not, except
a different retention clock; it is not recommended.* **If the owner wants a
deliverable that outlives its run, the recommended order is: D2's alternative
(same bucket, new prefix, own lifecycle) as its own design once artifacts are
in use — not a third bucket.**

## 8. Security

**The subject is untrusted bytes in both directions**: files a model-driven job
produced (out), and files an operator handed to a model-driven job (in).

1. **The artifact bytes are the output of a job that ran a model with
   `--dangerously-skip-permissions`.** Nothing about them is trusted. The
   recorded content type is **recorded, never trusted**: the runner derives it
   from the file's extension with a sniffed fallback, writes it to
   `result.json` and to the object's own content type
   (`blobx` already sets it, `internal/blobx/blobx.go:111`), and every reader
   is required to treat it as a hint. `fugaro artifacts get` **never opens,
   executes or unpacks** what it writes; it writes to a path the user names and
   prints the recorded type, the size and the sha256.
2. **Names are sanitised, and the runner computes every one of them.** The
   upload name is the file's path **relative to the outputs directory**,
   after a refusal list: no absolute path, no `..` element, no name that
   escapes `outputs.globs`' common root, no duplicate after normalisation, and
   an explicit length limit. The leaf is always
   `runs/<slug>/<run>/artifacts/<name>` — a **fixed** leaf under a **fixed**
   run prefix, so no artifact name (however crafted) can be made to write
   outside the run's own prefix. On the way back, `fugaro artifacts get`
   sanitises the name from the record the same way, and writes only into the
   directory the user asked for (never a path the record supplies).
   **The same refusals apply to an *input* name, and on the runner's side as
   well as the launcher's**, because an input name becomes a filesystem path
   (`<checkout>/inputs/<name>`) — and the task spec is text a launcher wrote,
   which §4's own reasoning already treats as untrusted for the caps. A spec
   whose `inputs[].name` is `../../.git/hooks/pre-commit` must be refused by
   the runner, not sanitised into something plausible: the runner applies the
   same list (no absolute path, no `..` element, no NUL or newline, a length
   limit, no duplicate after cleaning) and **fails the run** with the name
   quoted, rather than writing outside `inputs/`.
3. **The job's own account cannot write outside its repository's prefixes**,
   and this is IAM, not code: `resource.name.startsWith("…/objects/runs/<slug>/")`
   (and `cache/`, `locks/`), `internal/backend/gcp/names.go:374,383-389`. **The
   honest corollary, which the design states rather than hides: the condition
   is per *repository*, not per *run*.** Every run of one repository shares
   that account and that prefix, so a hostile run of repository A can overwrite
   repository A's other runs' artifacts — exactly as `SECURITY.md:62` already
   says for their records and caches. **This design does not narrow that;**
   nothing in it can, and claiming otherwise would be the lie a reader would
   catch. What it does add is that an artifact now carries a **sha256 recorded
   by its own run**, so tampering is *detectable* after the fact even where it
   is not preventable: `fugaro artifacts get` verifies the hash and refuses a
   mismatch (exit non-zero, nothing written).
4. **Caps stop the disk-filling loop, and they are enforced twice** — by the
   launcher (before the upload) and by the runner (before the download and
   before the upload), because the task spec is text the launcher wrote. An
   over-cap set is refused with a named reason; a glob that matches nothing is
   **not** an error by itself (the run may legitimately produce nothing) but it
   is reported, and the probe is what turns "nothing" into a failure if the
   owner wants one. The container's own ephemeral disk is the limit behind
   these; its exact size is **UNVERIFIED** (§5.2) and Check 58's step 7 is the
   live witness.
5. **Signed URLs are the launcher's, never the job's, and they are not in the
   first slice (D7).** *The fact that decides it:* a V4 signed URL needs
   `iam.serviceAccounts.signBlob` (or a service-account key), and **no grant in
   this installation provides it** — the only signing permission in the tree is
   `iam.serviceAccounts.signJwt`, on the budget identity's own custom role
   (`deploy/terraform/gcp/modules/firebase/iam.tf:23`), for a purpose that has
   nothing to do with objects. So a signed-URL feature is: a new IAM grant, a
   TTL policy, an audit story and a new bearer-credential surface, for bytes
   that may contain a customer's screenshot. Meanwhile the launcher and the
   operator **already have read access** (`objectViewer`/`objectAdmin`,
   `docs/design/bucket-iam.md:88,126-128`), so `fugaro artifacts get` plus
   `gcloud storage cp` covers retrieval today with no new permission. And by
   construction the *job* needs no URL at all: it writes to its own prefix with
   its own account. **Decision: later, launcher-only, short-TTL, and as its
   own design — never as a way for a run to hand out access.**
6. **Attachments are a prompt-injection surface in the ordinary sense.** A
   screenshot with text in it, a CSV with instructions, a filename that reads
   like a request: all of it reaches the agent. This is not new (the task text
   already does, `SECURITY.md:67`), and it must be said out loud in the docs:
   *attaching a file is writing part of the task.*
7. **`SECURITY.md` must be extended, and this is the exact text this design
   commits to.** In **"### The assumption"** (`SECURITY.md:17-19`), the
   paragraph after the existing one:

> **A run may also be given files, and may also be asked for one.** An
> artifact run (`docs/design/artifact-runs.md`) has no git token and no pull
> request: it is handed files the launcher attached, and it leaves files in
> the runs bucket instead. Two consequences follow, and neither is a sandbox.
> **In:** an attached file is trusted exactly as much as the task text, since
> the agent reads it and can act on what is in it — a screenshot can contain a
> token, a customer's name or someone's face, and an operator who attaches a
> file they did not write has handed the agent that content as an instruction.
> **Out:** an artifact is output of the same unsandboxed agent, written to
> `runs/<slug>/<run>/artifacts/` under the run's own repository-scoped service
> account; every launcher and every operator of the installation can read it
> (a launcher holds `objectViewer` on the whole bucket, an operator
> `objectAdmin`), every run of the same repository can read and overwrite it
> (the account is scoped to the repository's prefix, not to one run), and it
> is deleted with the rest of the run's record after 90 days by default.
> Nothing about an artifact is trusted: `fugaro artifacts get` verifies the
> sha256 recorded by the run, refuses a mismatch, and never opens, executes or
> unpacks what it writes.

   And in **"Known limits"**, one bullet:

> - **An artifact run's compute is not capped.** The budget's caps count model
>   dollars (`docs/design/m9-spec-v3-reconciliation.md` §7.4's reconciliation),
>   and a render is compute: it is bounded by the run's own clock
>   (`timeouts.total`, and a Cloud Run task timeout of `total + 2m`) and by the
>   workflow's `resources`, never by `budget.per_run_usd`. `result.json`'s
>   `cost.compute_usd` is an estimate, reported and never charged. If you give
>   an artifact workflow 8 vCPU, a two-hour total, and a command that never
>   finishes, you pay for two hours of 8 vCPU and the caps will not stop it.

8. **What this design adds to the launcher's powers: nothing.** Attaching a
   file uses a grant a launcher already holds; artifact reads use grants that
   already exist; no new role, no new permission and no new bucket. That is a
   deliberate property: the security-relevant new code is *refusals* (names,
   caps, hashes), not capabilities.

## 9. Reading the artifacts

- **`fugaro artifacts ls RUN [--json]`** — reads `result.json` and prints the
  record's own table: name, bytes, sha256 (short), recorded content type, and
  the probe's exit code. It **lists the record, not the bucket** — the same
  choice `runs ls` already makes (`internal/cli/runs.go:379-390`) — so a
  missing object is visible as "recorded but not present" only if it asks for
  a listing; the design's answer is: `ls` is the record, `get` is the object,
  and a `get` that finds no object says exactly that.
- **`fugaro artifacts get RUN [NAME] [--out DIR] [--force]`** — downloads
  one artifact (or all of them with no `NAME`, into `./` or `--out`), verifies
  the sha256 and the byte count against the record, refuses to overwrite
  without `--force`, and never opens what it writes. A mismatch is an error
  with both hashes printed, and **nothing is written**.
- **`fugaro runs ls`** gains a column (or a `--json` field) for the artifact
  count and total bytes, and **`fugaro diagnose`** names the artifacts beside
  `report.md` (the path shape it already prints,
  `internal/cli/diagnose.go:158`). **Both read `result.json` only**; neither
  enumerates the bucket.
- **The pull request report** is not applicable (there is no PR). The *run's*
  `report.md` gains an "Artifacts" section with name, size, sha256, content
  type and the probe's verdict, and it says the compute estimate and the
  render's wall clock.
- **A signed URL is not offered** (§8.5): the operator's own credentials are
  what the retrieval path uses.

## 10. Task outline, and where it lands in the roadmap

The full plan is [../plans/2026-10-15-artifact-runs.md](../plans/2026-10-15-artifact-runs.md).
**The outline is the plan's own task list, with the plan's numbers**, so a
cross-reference here and one there name the same task:

1. **The format**: `mode: artifact`, `plan`/`render`, `outputs`, and the
   `commands.render`/`commands.probe` keys and `timeouts.render` — the recipe
   schema, the parser, the validation refusals. Lands with nothing executing
   it: an older binary refuses the recipe, and a newer binary refuses
   `outputs:` on a `mode: implement` recipe.
2. **`mode: artifact` in the runner, and the render step**: the bootstrap
   branch, the plan stage, deterministic execution of `commands.render`, its
   deadline, its failure path and the `render` record. Proven with an
   in-memory bucket and no cloud.
3. **Two streaming `blobx` methods**: `PutFile` and `GetFile` — the byte
   layer the rest needs, since every write today is a `[]byte` and every read
   is capped at 1 MiB (§3, V23).
4. **The outputs**: selection, sanitisation, caps, hashing, upload to
   `runs/<slug>/<run>/artifacts/`, and the `result.json` + schema additions.
5. **The probe**: `commands.probe`, the two environment variables, the
   record.
6. **The attachments**: `--attach`, the task-spec list, the runner's
   download (with its own name refusals and hash check), the caps, the
   `.git/info/exclude` write. (D6: the same release, a separate commit.)
7. **The CLI**: `fugaro artifacts ls|get` with hash verification, and the
   `runs ls`/`diagnose` display.
8. **`budget preview`'s compute ceiling line** (§6), labelled "not capped".
9. **The docs and the skills**: a new `docs/artifact-runs.md`, the sections
   in `docs/recipes.md` and `docs/base-image.md` (ffmpeg via `image.apt`), the
   routing skill's "when to choose an artifact recipe", **the SECURITY.md text
   of §8.7 verbatim**, and **Check 58's row and section in the checklist** —
   written last, because the docs and skill lints resolve the flags they name.
10. **Check 58** (owner-run, sandbox only) — §11. Not part of the pull request.
11. **The full suite** and the `fugaro verify build`/`verify test` run.

**And three things that are deliberately not tasks in this plan:** the durable
**retention** prefix (D2's alternative), **signed URLs** (D7) and
**compute-in-the-cap** (D4's alternative). Each is its own design, named here
so this plan is not mistaken for the whole story.

**Release placement.** Tasks 1–9 extend the recipe format, add a step kind, add
one optional key to two stored schemas and add a CLI subcommand — all additive,
and all shippable in **0.8.0**, one point release, with a new launch-time
`*Since` constant (`artifactsSince`, through the existing `checkImageSince`
seam, `internal/cli/recipes_skew.go:204-270`) so a recipe or an `--attach`
using the new keys is refused against a job image whose runner predates them.
Which of them could land in a
*patch*: Task 1 (format only, nothing executes it), Task 2 (the step, unused by
every existing recipe), Task 7 (`budget preview`, display only) and Task 8 (the
docs) are each safe alone; Tasks 3–6 are the feature and belong in the point
release. Nothing here needs the project-centric phases (D1), so it does not
wait on them.

## 11. Check 58, in outline

**Check 58 (OWNER-RUN, NOT RUN)** in `docs/gcp-live-checklist.md`, written with
**Check 43's guard block verbatim** (`docs/gcp-live-checklist.md:1437-1458`):
`P` hard-coded to `fugaro-dev` and never read from `gcloud`, `${P:?}` on every
argument, a `fugaro doctor` check, the checkout's git origin as a hard stop,
**never the `belong` project** (`:1431`), and **every step run by the owner,
none by an agent** — plus a Restore block. **Seven numbered steps**, each
preceded by a re-run of the guard block: (1) the sandbox's prerequisites —
ffmpeg in the image through `image.apt`, one `fugaro image refresh`, and the
fixture artifact recipe, render script and probe committed to the sandbox;
(2) three tiny PNGs in, generated locally, and the launch with three
`--attach`; (3) a five-second video out — the `render` record, the counters,
the absence of any push or pull request; (4) `ffprobe` verification through
`commands.probe` **and** an independent local `ffprobe` of the downloaded
file; (5) the recorded content type against the object's own (U5); (6) the
artifact retrieved with a **sha256 match**, and the upload's wall clock
against `finalize_reserve` (U6); (7) the failure direction, both halves — a
probe that must fail the run and upload nothing, and an over-cap set that
must refuse cleanly and leave the container alive (U3). Then the Restore.
That list is what the section must keep asserting, in whatever wording the file
has; **the checklist file stays the single source of the wording.** The plan's
Task 9 writes the row and the section (its own assertions list is a checklist
of what they must pin, not a second copy of the steps), and the plan's Task 10
runs them without quoting them back — the rule
`../plans/2026-10-14-openrouter-senior-reviewer.md:146` states, whose finding
was that a plan repeating a check's steps is how the two drift.

**The numbering is checked, not assumed.** On `main`, `docs/gcp-live-checklist.md`
has rows for 40–47 (`:317-324`, 42–47 at `:319-324`) and **57** (the compute backend's, `:325`); **48–56 and 58
are free** (no row, no heading), and 24, 25 and 39 are reserved with no row.
So **58 is this design's**, and it is the next free number for a check with a
section of its own.

## 12. Verified, and unverified

**Verified offline against the code or the docs** (each with the symbol or
line read for it — the plan's Review Focus repeats the important ones):

| # | Claim | Read from |
|---|---|---|
| V1 | The run prefix is `runs/<slug>/<run>/`, and the object names are `task.json`, `result.json`, `launch.json`, `cancel`, `report.md`, `transcripts/<stage>-<n>.jsonl`, `session/session.json`, `verify/<n>.json` | `internal/runstore/runstore.go:386-387,492,502,547`, `internal/runner/runner.go:1710,2132,2192`, `internal/runstore/session.go:39` |
| V2 | `blobx.Open` accepts `gs://` **and** `file://`, so an artifact path is testable with no cloud | `blobx.Open` call sites `internal/cli/exec.go:146,296`, its flag default `:59`, `internal/e2e/budget_test.go:129` |
| V3 | The bucket deletes `runs/` objects at `runs_days` (default 90); `builds/` has no rule | `deploy/terraform/gcp/modules/installation/bucket.tf:16-24,63`, `variables.tf:109-121` |
| V4 | A run's account is scoped to the **repository's** prefixes (`runs`, `cache`, `locks`), **not** to one run; a launcher reads the whole bucket and writes under `runs/`; an operator has `objectAdmin`; project Owners/Editors bypass it | `internal/backend/gcp/names.go:374,383-389`, `deploy/terraform/gcp/modules/workflow/sa.tf:24-33`, `docs/design/bucket-iam.md:88,126-128`, `SECURITY.md:36,62` |
| V5 | `mode:` is `implement|review`; `review` is the no-git-token, no-PR precedent, with the outcome `reviewed` | `internal/recipe/recipe.go:37-40,356-362,634-638`, `docs/design/generic-tool.md:195-203`, `internal/runner/runner.go:635` |
| V6 | The `check` step is deterministic, runs `verify.Run` in the checkout as the agent's user, may only come first, and may name only `commands.build|test|lint` | `internal/recipe/recipe.go:47-62`, `internal/runner/gate.go:61,125-132`, `internal/verify/verify.go:176,243`, `schemas/recipe.schema.json` |
| V7 | A recipe names no command; bucket text would run code | `docs/design/generic-tool.md:356` (G10's row), `internal/runner/gate.go:125-127` |
| V8 | Timeouts: `total 90m` / `stage min(40m,total)` by default; this repository's `fugaro.yaml` says `2h/90m/25m/5m`; `finalizeAt` is the real deadline; Cloud Run gets `total+2m`; a task may run 24h; the CLI caps `--total-timeout` at 24h | `internal/config/defaults.go:77-90`, `fugaro.yaml:54`, `internal/runner/budget.go:22-34`, `docs/design/v1.md:370`, `docs/SPEC.md:30`, `internal/cli/run.go:976-991` |
| V9 | Cloud Run jobs take 1, 2, 4, 6 or 8 vCPU and at most 32Gi | `internal/backend/gcp/limits.go:22,36-38,55`, `limits_test.go:11,23` |
| V10 | The caps count model dollars; compute is an estimate, reported and never capped; `compute_usd`/`compute_estimated` exist and are rolled into history | `docs/design/m9-spec-v3-reconciliation.md:10`, `docs/design/m9-budget-and-dashboard.md:798`, `internal/budget/evaluate.go:190-204`, `internal/runner/cost.go:62-64`, `internal/runstore/cost.go:12-18`, `internal/budget/rollup.go:296-303` |
| V11 | `fugaro budget preview` has no compute notion | `internal/cli/budget_preview.go:26-28,489-538,697-731` |
| V12 | mise is in the base image; ffmpeg is nowhere in the repository; `image.apt`, `image.setup`, `dockerfile:` and `image.tools`/`mise.toml` are the four ways to add a tool | `images/base/Dockerfile:39-41,154-168`, `images/derived/Dockerfile.tmpl:26-33`, `internal/image/render.go:92-117`, `docs/base-image.md:36-47,63` |
| V13 | No artifact/attachment/output concept in `generic-tool.md`, `recipes.md` or `v1.md` — except that v1's 15 hits are Artifact Registry (the GCP service) but one is `ARTIFACTORY_TOKEN` (`v1.md:542`, a JFrog token) — and none in the schemas or the CLI | grep over the tree, `schemas/recipe.schema.json`, `schemas/result.schema.json`, `internal/cli` |
| V14 | No signed URL and no `signBlob` grant anywhere; only `iam.serviceAccounts.signJwt` for the budget identity | grep over `internal/` and `cmd/`, `deploy/terraform/gcp/modules/firebase/iam.tf:23` |
| V15 | `result.json`'s schema is `additionalProperties: false` and is validated **only by tests**, never at read time; readers use the Go struct | `schemas/result.schema.json`, `schemas/schemas_test.go:23`, `internal/runstore/runstore.go:532` |
| V16 | `runs ls` and `diagnose` read named files, never enumerate the prefix | `internal/cli/runs.go:379-390`, `internal/cli/diagnose.go:158,177` |
| V17 | The launcher mints the run id, writes `task.json`, then submits the execution — so `--attach` has a place to stand | `internal/cli/run.go:819,1147,1392`, `internal/runstore/runstore.go:502` |
| V18 | The VM backend is a merged design (#375) with Tasks 1–2 merged (#380, #381); Task 3 is not; `backend: cloud-batch` is refused by config validation, not by a flag | `internal/localcfg/localcfg.go:711-719`, `internal/backend/batch/batch.go:19-21`, `docs/design/compute-backend.md` §8 |
| V19 | GPUs are out of scope in the VM design, by name | `docs/design/compute-backend.md:1485` (§6.3) |
| V20 | A repository-less run needs project-centric phase 3's project jobs, which are not shipped; `checkout: clone` is not shipped either | `docs/design/project-centric.md:15,66-68,109`, `internal/cli/recipes_skew.go:36`, `internal/cli/docs_checkout_test.go:123-132` |
| V21 | Check numbers: rows exist for 42–47 and 57; 48–56 and 58 are free; Check 43's guard block is at `:1437-1458` and "never `belong`" at `:1431` | `docs/gcp-live-checklist.md` |
| V22 | The three PNGs the check generates are real PNGs: the generator the plan commits to (a `python3` one-liner over `zlib`/`struct`, in Check 58's section) was run in this session and produced three valid 320×240 8-bit RGB PNGs, 865 bytes each | run in this session; the command is in the plan, Check 58's step 2 |
| V23 | `blobx` has no streaming write and no read above 1 MiB (`MaxReadBytes`), and no `Put`/`Create` variant that takes a path | `internal/blobx/blobx.go:51-54,109-138,148-198,314` |

**UNVERIFIED, with the check that would settle each:**

| # | Unverified | Settled by |
|---|---|---|
| U1 | Whether a GPU can be attached to a Cloud Run job (or to a Batch VM) in the owner's project, and at what price | an owner-run probe in the sandbox (a job submitted with a GPU request) — **not** promised anywhere in this design (§5.4) |
| U2 | Whether mise's registry can install `ffmpeg` (an asdf/community plugin, or a backend) | `mise ls-remote ffmpeg` on the base image; the design does not depend on it (`image.apt` is the recommendation) |
| U3 | The container's own ephemeral disk: how Cloud Run sizes it, and how it counts a render's temp files | Check 58 step 7 (an over-cap render must fail cleanly) |
| U4 | Whether a Cloud Run job's `resources` may be raised on a *running* job's future executions without a new job (it cannot today: the resources are in the job spec) | an owner-run `gcloud run jobs update` on the sandbox; it affects only the docs' "raise the ceiling" advice |
| U5 | Whether content-type sniffing is needed at all in practice (what the object store reports for a `.mp4` written by ffmpeg on the base image) | Check 58 step 5's recorded type |
| U6 | The real wall-clock of the upload inside `finalize_reserve` for a file near `max_bytes` | Check 58 step 6, plus the cap's own default choice |

## 13. Open for the owner

Each item is a decision the plan's tasks depend on, with the recommendation this
document argues for and the cost of the alternative. **All of them can be
vetoed one by one before any task starts, and none of them is settled by this
pull request.**

| # | Question | Recommendation | The alternative, and what it costs |
|---|---|---|---|
| **D1** | **Repo-bound or repository-less?** An artifact run needs a checkout for its recipe, its scripts and its `mise.toml`. | **Repo-bound: an "assets" repository** (the owner's existing one) holds the recipe and the render script. It keeps the format's safety rule (*a recipe names no command*, and nothing from the bucket is executed, V7), it versions and reviews the script, and it makes "the run proposes a PR for a script change" a *later* slice rather than a redesign. | **Repository-less runs under a profile.** They do not exist: they need project-centric phase 3's project jobs (V20), and a `checkout: none`; and their render script would have to come from the **bucket** — project recipes are bucket text, which the format deliberately refuses to execute (V7). Cost: the whole of phase 3, plus a new exception to the safety rule, for a case ("I have no repository at all") the owner does not have. |
| **D2** | **Retention.** | **Under the run prefix, expiring with the record at `runs_days` (90 by default)** (V3), with `fugaro artifacts get` and `gcloud storage cp` documented as the way to keep one. | **An `artifacts/<slug>/…` prefix with its own `artifacts_days`.** Right for a deliverable that must outlive its run; costs a new lifecycle rule, a new variable, **a change to every job's IAM condition** (`JobBucketPrefixes` is compiled into each grant, V4) — a plan and apply per repository — and an unanswered question about which account writes there. Recommended as its own design **once artifacts are in use**, never a separate bucket. |
| **D3** | **Where heavy renders run, and GPUs.** | **The Cloud Run job, as it is** (V8, V9): 8 vCPU / 32 GiB / a 24h-class task timeout, the existing `compute_usd` estimate, per-workflow `resources` and `timeouts` already there. **GPUs are promised by nothing** (§5.4, U1, V19). | **The VM backend** (Docker-capable VMs, no 8-vCPU ceiling). Costs the rest of compute-backend's tasks (Task 3's price table is not even merged, V18) and its per-run VM weight — worth it the day a render hits the ceiling or needs a container the derived image cannot carry, and that is the trigger to finish it. A **GPU** answer needs U1 first, in either substrate. |
| **D4** | **The budget model: is the timeout the cap?** | **Yes, in the first slice**: compute is reported (`compute_usd`) and capped only by `timeouts.total`/`timeouts.render`/Cloud Run's task timeout (V10), and the docs, the report and `budget preview` all say **"not capped"** out loud. | **Compute inside the cap**: the spec sketches a 60-second heartbeat reserving vCPU/GiB-second cost through the lease flow (`docs/design/m9-spec-source-v3.md:493`). Costs a new halt reason, a decision about which caps count compute, a per-backend price table (the VM one does not exist, V18) and an interaction with the sweep. A separate design, recommended next. |
| **D5** | **`mode: artifact`, or a general `outputs:` on any recipe?** | **`mode: artifact` with `outputs:` legal only there.** `mode: review` is the precedent for a run whose shape changes (V5), the mode is where "no PR, no git token, no implement" already lives, and the restriction keeps a new key off every existing recipe. | **`outputs:` on any recipe** (so an implement run could ship a PR *and* a file). It is the better end state and the natural second slice; it costs a decision this design does not need yet — what a run that is both a PR and an artifact means for readiness, the PR's draft state and the report — and it widens the surface of every recipe in the catalog. |
| **D6** | **Attachments in the first slice, or a second one?** | **The same release, as its own commit (Task 5).** The owner's use case *needs* files in: artifacts out with nothing in is a demo, not the sizzle video. | **Outputs first.** A smaller review surface, and the same two schema changes (task spec + result) are needed either way — so it buys one commit's worth of review and costs the owner a whole release before their own use case works. |
| **D7** | **Signed URLs now, or later?** | **Later, and launcher-minted only.** No grant in this installation can sign (V14), no job needs one (it writes with its own account), and the launcher and operator can already read the bytes (V4) — so `fugaro artifacts get` + `gcloud storage cp` is the path today. | **Now, minted by the launcher with a hard TTL.** It is genuinely nicer for a browser (a video you can click); it costs a new IAM grant (`signBlob`), a TTL and audit story, and a bearer credential for bytes that may be a customer's screenshot — its own design, not this one's. |

## 14. What this design deliberately does not do

- **No GPUs** (§5.4, U1).
- **No compute-in-the-cap** (D4's alternative).
- **No durable retention** (D2's alternative).
- **No signed URLs** (D7).
- **No repository-less runs** (D1's alternative).
- **No general `outputs:` on implement recipes** (D5's alternative), and no "the run opens a PR for the script change it used" — the latter is a natural third slice, and it needs the artifact run to be worth having first.
- **No local-only artifact mode**: `fugaro exec` with a `file://` bucket gets the mechanism for free (V2), and the design does not add a second local path.
- **No artifact registry, no manifest of many runs, no cross-run index.** One run, one artifact set, one record.
