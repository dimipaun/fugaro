# Artifact runs: a run whose deliverable is a file

Status: **decisions answered (owner, 2026-10-15); implementation awaits the
owner's explicit go on the plan — nothing here is implemented.** D1–D11 are
answered (the table in §13 carries each answer verbatim); **answering a decision
is not approving the tasks**, and the plan's P12 gate stands unchanged: no task
starts until the owner says yes to the plan itself. This
document and its plan ([../plans/2026-10-15-artifact-runs.md](../plans/2026-10-15-artifact-runs.md))
are the whole of this change: no Go, no Terraform, no `fugaro.yaml`, no schema
file and no live check is edited by the pull request that adds them. Every
claim about the code is cited to the file and line read for it — **read at
`origin/main`'s `b9d5c5d`** (0.7.5 plus #387 and #389; the branch's own base
`dd049c3` predates them, so a line number here may run past the end of the file
in the tree under review, and every such citation says `origin/main`) — and
everything that could not be verified offline is written **UNVERIFIED** with
the check that would settle it.

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

- **`mode:`** is `implement | review` (`internal/recipe/recipe.go:38-42`,
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

**A recipe may not name a command, and a check step may only come first.** The
schema has exactly **four** `steps` variants (`schemas/recipe.schema.json:25-30`),
and every one of them is a fixed prefix list with `items: false`: a lone
`reviewNoBounce`; `firstLine` then `reviewBounce`; `check` then `reviewNoBounce`;
and `check`, `firstLine`, `reviewBounce`. So in the two variants that have a check
step at all it is `prefixItems[0]` (`$defs.check`), and neither of the other two
allows one anywhere — an `implement` recipe is the `firstLine`-then-`reviewBounce`
variant, and a `mode: review` recipe is overridden to the lone `reviewModeReview`
one (`:33-41`). A deterministic step *after* the agent has not been expressible at
all, which is precisely what a render needs.

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
with a memory↔CPU ratio rule (`internal/backend/gcp/limits.go:25,36-46`;
`limits_test.go:11` asserts `{8, "32Gi"}` passes and `limits_test.go:23` that
`{8, "64Gi"}` fails). So **VERIFIED, in this repository's own code: an artifact
render on today's substrate gets at most 8 vCPU and 32 GiB.**

**The tools situation.** The base image installs **mise**
(`images/base/Dockerfile:43-45` pins `MISE_VERSION`, `:171-181` installs it to
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
(`internal/backend/backend.go:292-296`), computed by the runner from the
workflow's **requested** vCPU/memory and the elapsed run time
(`internal/runner/cost.go:63`), recorded as `cost.compute_usd` +
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
| "the compute-instance backend … Tasks 1-3 implemented behind a flag" | **Partly wrong, and less wrong than when this design was first written.** The design is merged (#375) and Tasks 1–3 are merged (#380, #381 and **#389**, the VM price table — `internal/backend/batch/prices.go`'s `MachineType`/`MachineTypeOf`/`ForMachineType` and `backend.Execution.MachineType`/`BilledWindow`, read at `origin/main` b9d5c5d); the gate is not a flag but a **config refusal** — `backend: cloud-batch` is refused by `internal/localcfg/localcfg.go:711-719`, and what remains unmerged is the backend's *enable* work, not its price table. **§5.3 and D3 are written against this and V18 carries the corrected citation.** |
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
  max_bytes: 268435456        # each artifact, 256 MiB
  max_total_bytes: 2147483648 # the whole set, 2 GiB
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

**The separation, stated exactly: what the runner executes is repository text
read once — and it is not a sandbox.** `commands.render` and `commands.probe`
are repository text from `fugaro.yaml` at the task's ref, loaded into the
runner's **in-memory** workflow config at bootstrap **before any stage runs**
(`r.cfg, r.wf`, `runner.go:962`) and executed from there. They are deliberately
**not** carried into `verify.Settings` — that file is `fugaro verify`'s, and it
is writable by the run itself (below). Three things follow, and the design
claims only these three:

- **The recipe, the task spec and the bucket name no command.** This is the
  format's own rule (`internal/runner/gate.go:125-127`, generic-tool G10) and it
  is untouched: the only way a command enters a run is a `fugaro.yaml` a
  reviewer read.
- **The command *string* is fixed before the agent starts — because the render
  and the probe read it from the runner's own in-memory workflow config, never
  from a file.** `r.cfg`/`r.wf` are set once in `bootstrap`
  (`runner.go:962`) and nothing in the runner re-reads `fugaro.yaml` after it,
  so `wf.Commands.Render`/`wf.Commands.Probe` are the strings the reviewer read
  and the plan stage cannot reach them. **This is the whole of the property,
  and the tempting alternative does not have it:** `verify.Settings` *is*
  written into the state directory outside the checkout (`verify.WriteSettings`,
  `runner.go:1073-1081`) — but at mode **0644** (`internal/verify/verify.go:26,60`)
  and **re-read per invocation** by `verify.LoadSettings` (`:85`), because that
  file exists so the user-invocable `fugaro verify` can work in the agent's own
  terminal. A plan-stage agent that rewrites the `render` key of
  `<StateDir>/workflow.json` would therefore change the command a
  `LoadSettings`-reading render ran, and `render.command_sha256` would attest
  to a string no reviewer ever saw. So the runner's render and probe **must not
  be `LoadSettings` callers**, the plan says so in Task 2 and Task 5, and
  Review Focus 1 pins it with a mutation test (the plan stage rewrites
  `workflow.json`; the executed and recorded command is unchanged). What
  `fugaro verify` does with that file is unchanged and out of scope.
- **It is *not* a boundary against the agent.** The plan stage and the render
  step are the same container, the same user and the same environment. An agent
  that wanted to run code has already had every opportunity, and
  `SECURITY.md:42-52` says the agent is unsandboxed by design. A command that
  names a file in the checkout — `sh .fugaro/render.sh "$FUGARO_RENDER_PLAN"` —
  executes **that file's working-tree bytes**, which the plan stage could have
  rewritten, no more and no less than every other file in a checkout the agent
  controls.

**So the design states a rule instead of implying a property (D8): a
`commands.render` that *is* the script** — the inline `|` block in `fugaro.yaml`,
which is the same text the reviewer read and which the runner keeps in the
in-memory workflow config it read once at bootstrap (`r.wf`, §3's
`verify.Settings` boundary note) — **is what the docs and the skill recommend
and what the check's fixture recipe uses**; a command that names a file is
permitted, and documented for what it is. The runner cannot tell the two apart
without a static analysis it should not do, so it does not try: it records
`render.command` (redacted), `render.command_sha256` (the hash of the exact
string it ran — the `commands.render` text as read from `fugaro.yaml`, **before
the redactor**, which is what makes it recomputable by anyone holding the
repository at the recorded ref; the recorded `command` is that string *with*
the redactor applied, so the two are deliberately not a hash pair whenever
redaction fires) and `render.tree: "working"`, which says the command's own file
references were resolved in the checkout the plan stage also had.

*The alternatives, and what each costs.* **(a) A pristine tree at the task's
ref** (`git archive`, or a second worktree, so a named file's bytes are the
commit's): it answers the *file* question and not the boundary question — the
agent still shares the container, the user and the `PATH`, so the step is not
made untouchable — and it costs a second copy of the tree on a memory-backed
filesystem (§5.2) for a rule the docs can state for free. **(b) Operator config
only** (`commands.render` from the project layer, never from the repository):
genuinely narrower, and it takes the script away from the repository that owns
the tool and the ffmpeg flags — for the sizzle case the script *is* that
repository's artifact. **(c) Run it in the working tree and say so**: what this
design does. **The line the design draws, and it is three claims and no more:
the recipe, the bucket and the launcher cannot put a command into a run
(`commands.render` is repository text, and no path writes it from the bucket);
the command *string* is fixed before the agent's first turn — read once at
bootstrap from the repository's `fugaro.yaml`, and never re-read from the
writable `workflow.json` the plan stage can rewrite — so `command_sha256`
describes a string the reviewer could have read; and nothing about the step is
trusted against the agent.**

**The residual risk, named rather than implied.** "Fixed before the agent
starts" is a statement about *when* the string was read, and **not** a
guarantee about what executes: `SECURITY.md`'s own premise is that the agent is
the **same user in the same container** as the runner, so an agent that wanted
to could attach to the runner (`ptrace` — which depends on the container's
`/proc/sys/kernel/yama/ptrace_scope`, a setting **nothing in this design
checks or pins**), or simply make the command *mean* something else by writing
the files it names in the working tree, or its `PATH`, or a `mise.toml` hook.
`command_sha256` therefore proves **which string the runner ran** and, for a
reader holding the repository at the recorded ref, that it is the text the
reviewer read — it does **not** prove the executed result was what that text
says, and the docs say so in the same words. What confines the damage is the
run's shape, not this hash: the agent already holds the workflow's secrets
(§8.6), and an artifact run pushes neither a branch nor a pull request, so the
blast radius of a render that misbehaves is the run's own prefix — which is
exactly the boundary `SECURITY.md:62` already draws for a run of one
repository.

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
| `max_bytes` | A byte cap **per artifact** (proposed default 256 MiB, ceiling 2 GiB) | Same, and it bounds one file's upload time inside `finalize_reserve` |
| `max_total_bytes` | A byte cap **for the whole set** — the sum of the artifacts' sizes (proposed default 2 GiB) | The cap that actually bounds what a run costs and what the container's memory must hold: eight files each just under `max_bytes` would otherwise pass every check and be 2 GiB of staging. §5.2's refusal is computed from **this** number |
| `probe` | Names a `commands.*` key (`commands.probe`) the runner runs once, after the render, before the upload | The `ffprobe` half of the use case: duration, resolution, non-empty — the run's "does the deliverable actually exist and look right" without a test suite. Optional; without it the structural checks (non-empty, within caps, sha256 recorded) are all there is, and the report says so. |
| `content_type` | Not declared: **recorded, from the file itself**, by extension with a sniffed fallback | §8: it is recorded, never trusted |

**The probe is `commands.probe`, run once with the whole set in two
environment variables** — `FUGARO_OUT_DIR` (**the staging directory's absolute
path**, `<StateDir>/artifacts/`: the directory that holds exactly the files
that will be uploaded, one per line of `FUGARO_ARTIFACTS_FILE`, laid out flat
under their sanitised names) and `FUGARO_ARTIFACTS_FILE` (the absolute path of a
file the runner writes into the state directory, holding the sanitised relative
names one per line). **Two names, one root, and the design states which:** a
record's `<name>` is the match's path **relative to the deepest common root of
the recipe's globs** (§8.2, and the same root `dockerfile:`-style escapes are
measured against), so `$FUGARO_OUT_DIR/$name` is always the staged file — see
the example below, where `out/*.mp4` yields names like `sizzle.mp4` and the
staging directory holds them at its own top level. `FUGARO_OUT_DIR` is *not*
the checkout's `out/`; it is the flat staging root, so a script never has to
know the glob's shape.
**A file, not a value**: a newline-separated list inside one environment
variable cannot survive a name that itself holds a newline, and every shell
reads a file with `while IFS= read -r` without quoting rules in play — the
vocabulary the first draft of this design got wrong. The owner writes the check
they trust, for example:

```yaml
commands:
  probe: |
    set -e
    while IFS= read -r f; do
      d=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$FUGARO_OUT_DIR/$f")
      awk -v d="$d" 'BEGIN { exit !(d > 4.5) }'
      test -s "$FUGARO_OUT_DIR/$f"
    done < "$FUGARO_ARTIFACTS_FILE"
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
`render` object (`{command, command_sha256, tree, exit_code, timed_out,
duration_s, started_at}`) and an `artifacts` array (`[{name, bytes, sha256,
content_type, probe}]`), each
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
`<name>` the file's path **relative to the deepest common root of the
recipe's globs** (the "outputs root", §3), sanitised (§8). That is the one place the run's own account may write
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
streaming read (`GetFile(ctx, key, path, sum, limit, strict)`
— a `NewReader`, an `io.LimitReader` at the object's recorded size plus one, and
the **expected** sha256 as an argument, so a mismatch removes the temp file
instead of renaming it and the destination is never touched; `strict` is
**false** for the runner's input download and **true** for
`fugaro artifacts get`, for the reason two paragraphs down). Both are
testable against a `file://` bucket, and both preserve the properties the
existing methods have: a content type recorded on write and a cap that refuses
rather than truncates.

**The 403 classification is per caller, and this is the round-3 review's
correction — the two callers need *opposite* answers.** `blobx` already has both
shapes: `Read` folds an HTTP 403 into `ErrNotExist`, `ReadStrict` returns it as
an error, and the reason is written down in the file — *"job accounts hold
`storage.objectUser` only under a prefix condition, so they lack
`storage.objects.list` and GCS answers a GET of a missing object with 403.
Callers that treat 'absent' as normal depend on this"*
(`internal/blobx/blobx.go:143-176`, `isForbidden` at `:200`). So:

- **The runner's input download uses the non-strict classification.** The run's
  account is exactly that job account: it cannot list, so a GET of an input that
  is not there comes back **403**, not 404. Strict here would report a *missing
  input* as an IAM problem and send the owner to change grants that are correct
  — the one failure mode the review named. `downloadInputs` therefore gets
  `ErrNotExist` for the absence and fails the run with "the task names an input
  that is not in the bucket", and a 403 that is *not* an absence is not
  distinguishable by this caller — which is fine, because a run cannot fix
  either.
- **`fugaro artifacts get` uses the strict classification.** Its caller is a
  launcher or an operator holding unconditional `objectAdmin` (which includes
  `list`, `docs/design/bucket-iam.md:88,126-128`), so for them a 403 really is
  an access problem, and "no such artifact" would be a lie that costs an
  afternoon.

`GetFile` therefore takes the same `strict` flag the two read methods take (or
is two thin entry points), and the design fixes which caller passes which —
**non-strict in the runner, strict in the CLI** — rather than suggesting one
answer for both.

The write side gains the same symmetry, plus one rule the review asked for:
`PutFile` takes `Create`'s create-only condition, which gocloud's `file://`
driver honours as well as GCS (`fileblob.go:845-850`), and **on any error it
aborts the write before it closes the writer.** go-cloud requires `Close` to be
called even for an aborted write (*"To abort a write, cancel ctx; … The caller
must call Close on the returned Writer, even if the write is aborted"*,
`gocloud.dev/blob/blob.go:1138-1142`), and only cancelling the context stops the
object being committed — `Close` alone commits whatever arrived. So `PutFile`
owns a per-call `context.WithCancel`, and on a read error, a short read, or a
sha256 that does not match the expected one it **cancels first and then closes**,
returning the original error and not `Close`'s. Without that rule a failed
upload leaves a truncated object at a key the run recorded, and the owner's
`fugaro artifacts get` fails a hash check on a file that should never have been
written (`blob.go:436-456` is the same pattern in go-cloud's own MD5 path: cancel
the context, then close the driver writer).

## 4. Files in: `fugaro run --attach PATH...`

**`fugaro run` gains `--attach PATH` (repeatable)**, and the launch order
already supports it: the launcher mints the run id
(`task.NewRunID`, `internal/cli/run.go:887`), writes `task.json`
(`runstore.CreateTask`, `:1329`) and only then submits the execution
(`backend.Launch`, `:1574`). `--attach` fills the gap between the mint and the
write:

1. **Resolve the run id first**, and **establish that it is a new one before a
   single byte is uploaded.** A repeated `--run-id` is not a new run: the
   launcher adopts the stored task when it is byte-identical and refuses a
   different one (`createTask`, `internal/cli/run.go:1328`, over
   `runstore.CreateTask`'s `ErrExists`) — and that check happens *after* an
   upload would have. Uploading first would therefore **overwrite a finished
   run's `inputs/` objects before the launcher ever looked at its task**, which
   is the one ordering bug this section exists to avoid: with `--attach`, a run
   id whose `task.json` already exists is refused **before the first byte is
   written**, and the refusal names that run.

   **`--attach` therefore refuses an existing run id outright — it does not
   adopt one.** A plain launch may adopt a byte-identical stored task
   (`createTask`'s own rule); a launch carrying attachments may not, and the
   reason is that adoption and attachment cannot both be honoured. The stored
   task already names its `inputs`, and those objects are already in the
   bucket: re-uploading them is a create-only write against objects that exist,
   which is a failure rather than a no-op, and "the same bytes" cannot be
   established without reading the old objects back and re-hashing them — a
   read this slice does not do. So the refusal is the honest answer, and it
   happens **before the caps are checked and before the first byte is
   uploaded**, so a mistyped `--run-id` costs nothing and changes nothing.

   **The round-4 correction: a run id that has already gone is refused too,
   stored task or not.** The run-id rule above reads `task.json`; a run that
   started and wrote `task.json` under a checked-in placeholder — or whose task
   the launcher never got to write — leaves a `launch.json` and no task, and
   that run is exactly as gone as the adopted one. A launch that then found
   `prior != nil` (`existingLaunch`) skipped both the attach checks and the
   upload, and `createTask` wrote a task with no inputs: the user's `--attach`
   silently did nothing, and the run would run without the files it was
   promised. So `--attach` refuses whenever the run id already has a
   `launch.json`, naming the run, whether or not a task was stored: an
   attachment is uploaded before a run starts, and a run that already started
   is not one this launch can give files to.

   (The upload itself is create-only, and it is create-only on **every**
   backend: `blobx.PutFile` takes an `ifNotExist` argument carrying `Create`'s
   own condition into the streaming write (`internal/blobx/blobx.go:107-127`,
   plan Task 3), and gocloud's `file://` driver honours it too — `fileblob`'s
   writer stats the destination and returns `FailedPrecondition` when the
   object is already there (`gocloud.dev/blob/fileblob/fileblob.go:845-850`,
   which `blobx`'s own `write` turns into `ErrExists`). **That condition is
   still only race hardening, not the guarantee**: the launcher's own
   run-id check is what makes the refusal deterministic and gives it a message
   naming the run, and a condition that fires mid-upload leaves the earlier
   attachments in the bucket. `Create` itself cannot serve here — it takes a
   `[]byte`, and an attachment is the case the 1 MiB read cap and the streaming
   writer exist for.)

   **`--attach` is refused unless the launch resolves to an artifact recipe,
   and that refusal comes before the caps and before the first byte** (plan
   Task 6, with its own test). An attachment is only meaningful to a run that
   will produce files rather than a pull request: on an implement or review
   recipe the inputs would be downloaded into a run that commits a diff and
   opens a PR, and nothing in its steps reads `inputs/`. Every arm fails
   closed, and **no follow-up is ever an artifact run** (the runner refuses
   every follow-up whose *resolved* recipe is `mode: artifact`,
   `internal/runner/runner.go`): the two ways a launch can be an artifact run
   are allowed for a first run only, and a follow-up that reaches either is
   refused with the reason. So:

   - **A resolved recipe whose mode is not `artifact` is refused**, naming the
     recipe and its mode — "`--attach` needs an artifact recipe; <name> is
     mode: <mode>".
   - **A launch that leaves the recipe to the runner is refused too.** When
     `spec.Recipe` is nil the CLI cannot know what the runner will pick
     (`runnerChooses`, `internal/cli/run.go:1128`: no recipe in the task, not a
     follow-up, no checkout here — the runner then takes `agent.recipe` from
     `fugaro.yaml` at the ref, else the catalog default), so it cannot promise
     an artifact run. The refusal says so and names the fix: pass the artifact
     recipe explicitly. Being unable to *prove* the recipe is artifact mode is
     not a reason to upload the files anyway.
   - **A follow-up whose resolved recipe is `mode: artifact` is refused**, and
     this is not the arm below: a follow-up inherits the recipe it resolves
     rather than choosing one, so an artifact recipe can arrive without anyone
     asking for one — a repository may commit `.fugaro/recipes/default.yaml` as
     an artifact recipe, which is what a follow-up that inherited nothing runs.
     The runner refuses it unconditionally, at bootstrap; the launcher refuses
     it before the first byte, rather than uploading files for a run that
     cannot start. A follow-up's task *carrying* `mode: artifact` is refused
     the same way, since that mode is only reachable from a spec the launcher
     did not build (a stored task re-launched, or a forged one).
   - **A follow-up whose previous run left no recipe behind is refused.** A
     follow-up runs the recipe of the run it continues — the previous task's,
     else that run's record's — and when both are silent the runner runs its
     own default, because `resolveRecipe` reads `agent.recipe` only for a first
     run (`r.follow == nil && cfg.Agent.Recipe != ""`). The launcher resolves
     the same way (`recipeForLaunch`), so the refusal names the recipe the run
     will really use and says why: reading `agent.recipe` here would let an
     artifact recipe in `fugaro.yaml` — which the follow-up will not run — pass
     the check, and the bytes would leave the machine for a run that fails at
     bootstrap with "the task attached N file(s), but recipe default is mode:
     implement".

   The rule is deliberately the launcher's, because the launcher is where the
   bytes are: a refusal there costs nothing and leaves nothing in the bucket.
   The runner is the second line, not the first — see the empty-`Spec.Mode`
   rule in §8.2.
2. **Check the caps before uploading anything**: `inputs.max_files` (proposed
   16), `inputs.max_bytes` (proposed 256 MiB **per file**) and
   `inputs.max_total_bytes` (proposed 1 GiB **for the whole set**) — the launcher
   refuses a set over any of the three **before** the first byte is written, and
   the workflow declares all of them (`workflows.<name>.inputs`). **Each key has
   one meaning and only one, because a cap whose meaning depends on the reader is
   a cap nobody can check** (the round-3 review found `outputs.max_bytes` written
   both ways — per file in one place and for the set in another — and the fix is
   the same on both sides of the feature):

   - `max_files` **counts paths** — the number of things one launch may push;
   - `max_bytes` is **per file** — one oversized screenshot is refused by name;
   - `max_total_bytes` is **for the set** — the sum of the sizes about to be
     uploaded, which is the cap that actually bounds what a launch costs, and the
     one a set of ten files each comfortably under `max_bytes` still trips.

   The checks all happen before the first `PutFile`, and a refusal names the
   number, the cap and the count.
   **One printed line names where the file may go (D9's whole mechanism, and the
   round-3 and round-4 reviews' corrections).** With the caps checked, the
   launcher prints **one** line — the destination set of §8 item 8, which is
   every allowed provider that claims one of the run's models plus the run's
   **own auth route** whenever a model is unclaimed — on the `warn` writer beside
   the other launch notes. It is **not a gate**, it is printed once per launch
   and **only when `--attach` carried a file**, and it never tells a default
   Claude workflow that its screenshot "routes nowhere": `lc.Providers` holds
   only the non-Anthropic backends (`internal/localcfg/localcfg.go:89-91`), so a
   set read from it alone would be empty exactly for the owner's own workflows.
   The *local* wording is for the case where nothing receives the bytes at all —
   no claiming provider **and** no auth route.

   **The round-4 correction: the launcher opens each path exactly once.**
   Judging a path (`os.Stat`) and then uploading that path (`PutFile(path)`) are
   two opens of a name, and a name can be a different file at the second one —
   the file swapped for a FIFO, a device, or something grown past the cap in the
   instant between. So `--attach` opens the path once (`blobx.OpenSource`, the
   `O_NOFOLLOW` open the merged `blobx` grew for exactly this), `fstat`s the
   **held handle** for every judgement — regular file, per-file cap, set total —
   and hands **that same handle** to the upload, which bounds the copy by the
   size it was judged at (`PutFileOpened`, whose copy takes the judged size and
   refuses a source that no longer matches it). The handle survives a rename or
   removal of its path, so what was judged is what is uploaded. `--attach` is
   therefore also the place a **symlink** is refused: `Lstat`, not `Stat`, so a
   link to a file elsewhere is not silently read and stored under the link's own
   name — the launch would be uploading a file it never named. And two names
   that differ only in case are refused here too, on the same rule Task 4
   applies to a render's outputs (`strings.ToLower`): the runner writes the set
   into one directory, where `A.png` and `a.png` are one name, so one of the two
   would be refused — or overwrite the other — only after the bytes were up.

3. **Upload each file to `runs/<slug>/<run>/inputs/<name>`**, hashing it as it
   goes, recording `{name, bytes, sha256}` in the task spec — the plan's
   Interfaces block is authoritative for that record (Task 6), and the content
   type is a property of the **object**, taken from the file's extension with
   an `application/octet-stream` fallback when `blobx.PutFile` writes it: it is
   a recorded hint no reader trusts (§8.1), not a spec field, and
   `schemas/task.schema.json` closes the item (`TestTaskSchemaInputs` refuses a
   `content_type` key). This line listed a fourth field while the slice was
   built; the code is the authority.
   The launcher's own credentials suffice: a launcher holds `objectUser`
   under `runs/` (`docs/design/bucket-iam.md:126-128`) and an operator holds
   `objectAdmin` (`:88`) — **no new grant is needed to attach a file.**
4. **The runner downloads them before the first step**, into the checkout at
   `inputs/`, and **verifies every one against the sha256 the task spec
   recorded** — it does not trust the bucket's copy, and it enforces the same
   caps **and the same name refusals** again (§8.2), because the task spec is
   text a launcher wrote: a spec that claims 3 files cannot cause 300
   downloads, and a spec whose name is `../../.git/hooks/pre-commit` cannot
   write one byte outside `inputs/`. **The name refusals include the case-fold
   rule** (round 4): a spec naming both `A.png` and `a.png` is refused, because
   both land in one directory where that is one name.
   **And what the checkout commits under `inputs/` is refused, while the run's
   own leftovers are emptied and the directory reused** (round 4, corrected by
   the review of round 4). The two halves are the same question asked of git
   rather than of the directory: `git ls-files -- inputs` says what the
   *repository* put there, and only the repository's own paths are a hazard —
   an attached file landing on one of those names would replace committed
   content, silently, in the checkout the run goes on to commit from. A
   non-empty `inputs/` that git tracks nothing under is the run's own: the run
   excludes `inputs/` in the git directory (§ step 5), so bootstrap's `git
   clean -fd` keeps ignored files and a second artifact run in one checkout —
   the local `fugaro exec --workdir` shape — finds exactly what the first run
   downloaded. Refusing on "not empty" would fail that second run in bootstrap,
   blaming the repository for a directory the run itself created, and it would
   leave a stale file (one the bucket no longer names) sitting in `inputs/`
   looking exactly like an attachment the task did name. So the directory is
   emptied and reused when nothing under it is tracked, and refused by name when
   something is.
5. **`inputs/` is excluded from git** — the runner writes it into
   `.git/info/exclude` — because an artifact run must never commit a
   screenshot. The plan step's prompt names it, and the environment gains
   `FUGARO_INPUTS_DIR`.
6. **The report and `result.json` record each input** (name, bytes, sha256) so
   a reader can see exactly what the run was given. Task 6 writes the half it
   owns — `runstore.InputFile` and `Record.Inputs`, in `result.json`, once the
   files the list names are actually in the checkout — and nothing renders it
   yet: the report and the `runs ls`/`diagnose` display that read the record
   belong to the CLI surface of the task that owns them (the plan's Task 7).
   This line describes the finished feature, not what Task 6 ships alone.

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
| Cloud Run job resources | 1, 2, 4, 6 or 8 vCPU; ≤ 32Gi with ratio rules | `internal/backend/gcp/limits.go:25,36-46` |
| The container's own disk | **It is memory, and this repository records nothing about it.** On Cloud Run's second-generation execution environment the container's filesystem is an in-memory filesystem with no volume mounts (platform documentation; **nothing in this repository states it**, U3), so the staging directory, the render's own temp files and the upload's reader are all charged to the job's **memory** limit — 32 GiB at most (`limits.go`'s `CheckResources`) | Check 58's step 7, in three halves: an over-*cap* set must refuse cleanly, an over-*memory* render must be **observed** (killed container or clean failure), and the run's own record must say which happened |
| The artifact caps | `outputs.max_files` (path count), `outputs.max_bytes` (**per artifact**), `outputs.max_total_bytes` (**the whole set**) | this design |

**A render that runs away is bounded by the clock, and only by the clock** —
which is exactly what §6 is about. The *disk* is the other bound, and it is the
memory one: on the second-generation execution environment the container's
filesystem **is** memory (no volumes, U3, §8.4), so the staged artifacts sit
inside the job's memory limit beside the render's own temp files and the
upload's reader. **And staging is a *copy* (§8.4, P5), so the peak holds two
sets and not one**: the render's outputs where it wrote them in the checkout,
and the runner's copies in `<StateDir>/artifacts/` — both on the same
memory-backed filesystem, and both alive from the staging pass until the last
upload finishes. **And an attached input set is a third tenant of the same
filesystem**: `downloadInputs` writes each input into the checkout's `inputs/`
(§4 step 4), where it stays — the render reads it — while the render's outputs
and the staged copies are there too, so the input set is charged once and belongs
in the same count. **The check's arithmetic is therefore
`2 × outputs.max_total_bytes + inputs.max_total_bytes` against
`resources.memory`**, because the totals are the bounds that actually hold each
set: a set that cannot fit does not degrade into a clean refusal, it kills the
container, which writes no `result.json` at all. The defaults stay boring —
2 GiB of outputs is a 4 GiB doubled peak, 1 GiB of inputs is 1 GiB more, 5 GiB
against a 32 GiB ceiling — and an owner who raises the caps is told to raise
`resources.memory` past whatever the render itself needs, because the render's
own working set is the part this arithmetic cannot see and Check 58's step 7 is
where it is *observed* rather than assumed. **Two smaller terms are deliberately
outside it, each for the same reason — bounded by a cap that is already checked,
and observed rather than asserted:** the render's own temp files (no cap at all,
hence the observation) and the momentary one-file doubling `GetFile` causes while
downloading an input (its temp copy beside the destination before the rename,
bounded by `inputs.max_bytes`). **Leaving the *input set* out would have been
the wrong exclusion**, which is why it is in: unlike those two it is a whole
declared cap's worth of bytes, and a 1 GiB attachment set against a small
`resources.memory` fails in exactly the way this section says must not happen —
a killed container with no `result.json`.

**Where the refusal happens, named — and it is the recipe/workflow pairing site,
not the config-limits one.** `computeProblems` (`internal/cli/compute.go:14`)
cannot be it: it takes `*config.Config` alone, and the caps live on the
**recipe's** `outputs` block, which the config never sees. The functions that
pair a recipe with a workflow are `runner.RecipeGateProblems` — the shared list
`fugaro validate` reports through `gateCheckProblems`
(`internal/cli/validate_recipes.go:112-126`) and the runner re-checks at
bootstrap through `checkGateCommands` (`internal/runner/gate.go:93`) — so the
memory rule goes **there**, and `RecipeGateProblems` takes the whole
`config.Workflow` instead of its `Commands` alone, because the arithmetic needs
`Resources.Memory` as well as the caps. Three arms, one rule:

- **`fugaro validate`** reports it for every workflow the recipe could run on,
  through the same `gateCheckProblems` loop that already reports the
  render/probe refusals, with a `Path` of `workflows.<name>.resources.memory`;
- **the launch** refuses it in `finishLaunch`'s pre-write block, beside the
  recipe-image gate where the launcher already holds both the resolved recipe and
  the workflow from `checkoutConfig` (after the image gates and
  `checkMaxParallel` at `internal/cli/run.go:477`, around `budgetPrecheck` at
  `:488`, **before `createTask` writes anything** at `:503`);
- **the runner's bootstrap** refuses it through `checkGateCommands` when the
  launcher could not resolve the recipe at all (the `runnerChooses` launch), so
  that case fails closed before any stage runs rather than only in `validate`.

The message names the set size, the doubled peak and the workflow's
`resources.memory`. **Nothing here touches `internal/backend/**`** and no new
gate is invented: `RecipeGateProblems` is the list two existing call sites
already share (the plan's Review Focus 12, Task 1).

### 5.3 Which substrate: Cloud Run now, the VM backend when it hurts

**Recommendation: the Cloud Run job, unchanged, for the first slice (D3).**
The reasons are all facts: it is the shipped substrate (a run *is* a Cloud Run
job); the compute estimate already exists for it
(`internal/backend/backend.go:292-296`); 8 vCPU / 32 GiB / a 24h-class task
timeout is comfortably more than a sizzle video needs (three 320×240 PNGs into
a 5-second 1080p clip is seconds of ffmpeg, not minutes); and the workflow's
`resources` and `timeouts` are already per-workflow knobs, so an owner with one
heavy render gives *that workflow* 8 vCPU and a longer total without touching
anything else.

**The VM backend (compute-backend.md) is the answer to a specific failure, and
it is closer than this design first recorded.** It exists as a design (#375)
with the name seam (Task 1, #380), the fake + conformance suite (Task 2, #381)
and **the VM price table (Task 3, #389) merged** — `internal/backend/batch/`'s
`prices.go` (`MachineType`, `MachineTypeOf`, `ForMachineType`) and
`backend.Execution`'s `MachineType`/`BilledWindow`/`WindowNote`, which price a
VM from its boot window with Compute Engine's one-minute minimum
(`internal/backend/backend.go:103,147,174-205` — `MachineType`,
`VMMinimumBilled`, `BilledWindow`, `WindowNote`) — and it records the
same `compute_usd` shape Cloud Run's does. What is *not* merged is the enable
work: `backend: cloud-batch` is still refused by config validation
(`internal/localcfg/localcfg.go:711-719`). Its value for a render is real and
narrow: **a Docker-capable VM** means the render can run in *any* container
image rather than in the repository's own derived image, the 8-vCPU ceiling
becomes a machine-type choice, and a very heavy render stops competing with
the repository's other runs for job-level resources. Its costs are the
design's own: one VM per run, the reaper, the log
routing, and an installation-level Batch stage — Tasks 3–9 of that design.
**So the design's rule for the owner is: ship artifacts on Cloud Run; when a
render hits the 8-vCPU/32-GiB ceiling or needs a container the derived image
cannot carry, that is the trigger to finish the VM backend** — whose price
table is merged already, so `compute_usd` stays honest across the switch (D3) —
**and whose effect on who can read an artifact is D11.**

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
`compute_usd`'s price table has a GPU rate (its fields are the two per-second
rates `VCPUSecondUSD` and `GiBSecondUSD`, the two quantities `VCPU` and
`MemoryGiB` a machine-type table bills instead of the job's own limits, and a
`Source` string naming where the figures came from — `type Prices` at
`internal/backend/backend.go:264-277` on `origin/main`; a GPU would need a third
rate and a count field, and there is neither; a VM run bills *the machine type's*
quantities rather than the workflow's, `Prices.Quantities` at `:285-289`, and the
launcher's own table leaves those quantities zero,
`internal/localcfg/localcfg.go:868-874`).
**§13 carries this as a question for the owner, and no task in the plan may
assume a GPU.**

## 6. The budget: what counts against a cap

**The facts, again, because everything else follows from them.** The caps
(per-run, per-repository-day, global-day) count **model** dollars; the
gateway's ledger feeds them; compute is a **reported estimate, never capped**
(`docs/design/m9-spec-v3-reconciliation.md:10`,
`docs/design/m9-budget-and-dashboard.md:798`,
`internal/budget/evaluate.go:190-204`). The estimate exists and is charged
nowhere: `internal/runner/cost.go:63` prices the workflow's **requested**
vCPU/memory over the run's elapsed wall clock.

**So a runaway render's only cap is the clock (D4).** Say the number plainly:
8 vCPU and 32 GiB for two hours, at the installation's own compute prices
(`FUGARO_COMPUTE_PRICES`, `internal/infra/spec.go:44-46,682`; the launcher reads
them through `localcfg.ComputePrices`, `internal/localcfg/localcfg.go:73-75`),
is the worst case a *single* artifact run can bill — and because the caps do not look at it, the
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
(the VM one exists now — `internal/backend/batch/prices.go`, #389 — and which
quantities a row bills is `backend.Prices.Quantities`'s business, not the
caller's — that is where the display path already routes its rows,
`internal/runview/runview.go:403-404`, while the runner's own estimate hands
`ComputeUSD` the workflow's resources directly, `internal/runner/cost.go:63`),
and an interaction
with the reservation sweep. **It is a separate design, and this one does not
pretend to have it.**

**What `fugaro budget preview` should say for such a run.** Today it prints a
ceiling built from model caps only (`internal/cli/budget_preview.go:26-28`) and
a "typical" line from stage model spend (`:489-538`), so for an artifact run it
**under-reports by the whole render**. The recommendation, as a task in the
plan: a preview line that prices the *compute* ceiling the same shape
`compute_usd` uses — the workflow's **requested vCPU/memory × `timeouts.total`**
at the installation's compute prices, through `backend.Prices.ComputeUSD`
(`internal/backend/backend.go:292-296`) exactly as the runner's own estimate
calls it (`internal/runner/cost.go:63`) — clearly
labelled **"not capped"**, and with the "typical" line unchanged (it is
model-only history, and that is what it should stay). **`Prices.Quantities` is
deliberately not in that path**: it exists for a table that bills a *machine
type* (`internal/backend/backend.go:285-289`), and the launcher's table cannot:
`localcfg.PriceOverride` builds its `backend.Prices` from the two per-second
fields alone, leaving `VCPU`/`MemoryGiB` zero (`internal/localcfg/localcfg.go:868-874`),
so `Quantities` would return the caller's own figures unchanged. A launcher
prices the run's requested resources, which on Cloud Run is what the run is
billed for. **The basis is
`timeouts.total`, not `timeouts.render`**: the line is the worst case a run can
*bill*, and a render that overruns its own step still burns the run's clock
until the deadline. **And the *model* ceiling above it must not inflate**: an
artifact recipe can start **one** model stage (the `plan`; the render is a
runner command with its own timeout, the same rule that keeps a `check` step
out), but `recipe.StageBound` (`internal/recipe/stages.go:7-31`) has only a
`ModeReview` arm, so a `mode: artifact` recipe would fall through to the
implement arithmetic and be priced as `1 + 2 × review_rounds` stages — five at
this repository's defaults. Its artifact arm is part of the same task. (The
earlier draft of this section said
`timeouts.render + estimate of the model stages`; the plan's Task 8 is the
corrected basis, and this paragraph is the correction.) Its cost: preview
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
   upload name is the file's path **relative to the deepest common root of the
   recipe's globs** — the one root `outputs.globs` are matched against, so the
   rule "no name escapes it" is what the name is already measured from — after
   a refusal list, and it is the list the code enforces rather than a summary
   of it (**`runner.ArtifactName` / `firstControl`,
   `internal/runner/artifact.go`**, one function with two callers, below):
   no absolute path; no `..` element; **no control character — every C0 byte,
   DEL, every C1 byte and the bidi overrides** (`firstControl`'s table: a name
   is printed in the report and typed at a shell, and a control character is
   how a name reads as something other than what it is); **no backslash** (a
   path separator on Windows and an ordinary character elsewhere, so one name
   is two different paths depending on the machine that reads it — and
   `path.Clean`, which this rule cleans with, reads `frames\a.png` as one
   element while `filepath.Join`, which `get` builds its destination with,
   reads it as two on Windows); **no duplicate — decided by the standard's
   canonical caseless form, NFD of the full case fold of NFD** (Unicode D145;
   `ArtifactCollisionKey`, the same key both sides ask for: `ß` folds to `ss`,
   `ﬁ` to `fi`, `ς` to `σ`, so `straße.png` and `strasse.png` are one name, as
   `café.png`'s two normalisations already were — a *simple* fold, or
   `strings.ToLower`, gives those pairs two keys and lets a run stage a set no
   download of it can hand back, or hand back one file where the record names
   two). **Both normalization passes are there for a reason**: the marks a name
   carries are ordered by the one before the fold, so two spellings differing
   only in *that* order — no difference at all to the filesystem — fold from
   the same input (`ǰ̣.png` and `J̣̌.png` are one file on APFS), and
   folding a precomposed letter yields its pieces, which the one after the fold
   settles (`ᾼ͂.png` and `ᾶι.png`, the ypogegrammeni folding to a standalone
   iota, are one file too). The fold is also **settled to one target**: the 172
   Cherokee code points (U+13A0–U+13F5, U+13F8–U+13FD, U+AB70–U+ABBF) fold to
   *each other* in the Go library's tables where CaseFolding.txt names a single
   target, and a key that took that as it came would give the two spellings of
   one Cherokee name two keys; no name beginning
   with a dot (see below); and an explicit length limit. The leaf is always
   `runs/<slug>/<run>/artifacts/<name>` — a **fixed** leaf under a **fixed**
   run prefix, so no artifact name (however crafted) can be made to write
   outside the run's own prefix. On the way back, `fugaro artifacts get`
   sanitises the name from the record the same way, **and the destination is a
   fresh directory the command creates and then defaults to — never `.`**,
   because "writes only into the directory the user asked for" is not enough on
   its own: the record's names are untrusted text (§8.3's own point — a hostile
   sibling run of the same repository can rewrite `result.json` and the artifact
   to match), and a name that is a directory path inside a checkout
   (`.git/hooks/pre-commit`, `.claude/settings.json`, `.envrc`) is a file the
   user's *next* command runs or loads. **The fix is the destination, not the
   name's shape**, because nested names are the common case
   (`frames/a.png`): **`get` creates and defaults to a fresh directory**
   (`./<run>-artifacts/`, under `--out` when given), so every write lands under
   a root the user did not already have files in; it **refuses a name that is
   absolute, contains a `..` element, begins with a dot, or escapes that root**;
   it refuses to overwrite an existing file without `--force`; and the
   `--out` the user passes is used as the *parent* of that fresh directory, never
   as the destination itself. A record naming `.git/hooks/pre-commit` is an
   error the user sees, not a silently flattened `pre-commit`.
   **The same refusals apply to an *input* name, and on the runner's side as
   well as the launcher's**, because an input name becomes a filesystem path
   (`<checkout>/inputs/<name>`) — and the task spec is text a launcher wrote,
   which §4's own reasoning already treats as untrusted for the caps. A spec
   whose `inputs[].name` is `../../.git/hooks/pre-commit` must be refused by
   the runner, not sanitised into something plausible: the runner applies the
   same list (no empty or whitespace-only name, no absolute path, no `..`
   element, no NUL or newline, a length limit, no repeated name — the exact
   name, not a cleaned one: the list REFUSES a `.` or empty path element
   rather than normalising it away) and
   **fails the run** with the name quoted, rather than writing outside
   `inputs/`. It is one predicate in one place (`task.InputNameProblem`, with
   `task.InputNameBlank` for the emptiness half, which `Spec.Validate`'s
   required-name check shares), because the launcher and the runner have to
   agree: a name the launcher accepts and the decoder refuses is a file in the
   bucket under a run that never starts, which is what a whitespace-only name
   was — it is a legal file name and a legal JSON string, so the launcher took
   it and the runner's `ReadTask` refused the task at bootstrap.

   **"No repeated name" is the collision question, and it is asked with the
   artifact rule's own key** (`ArtifactCollisionKey`: NFD of the full case fold
   of NFD), in `downloadInputs` and in the launcher's `--attach` check
   alike — not with `strings.ToLower` and not with the exact spelling. The
   inputs of one run are downloaded into **one directory**, on the operator's
   own filesystem when the run is local, and a name that differs only in case
   or only in a normalization a filesystem applies is one file there: the
   second download would replace the first while the task still named both,
   and — the same drift the artifact names are keyed against (§9) — a spec
   keyed more weakly than the retriever's is a set the run accepts and then
   writes over itself. `straße.png` and `strasse.png` are two names and one
   file on APFS, so a task naming both is refused with both quoted. (Two
   *identical* spellings are a different sentence — "named twice; one file
   cannot be two" — and are refused at the launcher before anything is
   uploaded.)

   **And the runner does not trust `Spec.Mode` either, because it may be
   empty.** The launcher does not always resolve the recipe: when it leaves the
   choice to the runner (`runnerChooses`, `internal/cli/run.go:1128`) the spec
   carries **no** `mode`, and the runner takes it from the recipe it resolves
   (`agent.recipe` at the ref, else the catalog default). So the runner's rule
   is: **`Spec.Mode` when set, the resolved recipe's mode when not**, and the
   spec's mode may never *widen* what the recipe allows. Two consequences,
   both fail-closed:

   - **Inputs with a non-artifact resolved mode fail the run before the first
     step.** If the spec carries `inputs` and the recipe the runner resolved is
     `mode: implement` or `mode: review`, the run is refused with the recipe
     named — not started with the inputs quietly downloaded into a run that
     opens a pull request. (§4 refuses this at the launcher for every launch
     that resolves a recipe; this is the arm that catches the ones the launcher
     could not resolve, and a hand-written `task.json`.)
   - **An artifact recipe on an older runner fails closed, and that is the
     whole skew story.** A runner predating this feature parses the recipe with
     `mode` restricted to `implement` or `review` (`internal/recipe/recipe.go:360-362`,
     "must be implement or review"), so a task naming an artifact recipe is
     refused at the recipe gate rather than silently run as an implement run.
     The skew direction that would be dangerous — an old runner ignoring
     `mode: artifact` — is a parse error, not a default.
3. **The job's own account cannot write outside its repository's prefixes**,
   and this is IAM, not code: `resource.name.startsWith("…/objects/runs/<slug>/")`
   (and `cache/`, `locks/`), `internal/backend/gcp/names.go:374,383-389`. **The
   honest corollary, which the design states rather than hides: the condition
   is per *repository*, not per *run*.** Every run of one repository shares
   that account and that prefix, so a hostile run of repository A can overwrite
   repository A's other runs' artifacts — exactly as `SECURITY.md:62` already
   says for their records and caches. **This design does not narrow that;**
   nothing in it can, and claiming otherwise would be the lie a reader would
   catch. **What the recorded sha256 is, precisely — because the obvious
   overclaim is one keystroke away:** it proves **integrity in transit**, that
   the bytes `fugaro artifacts get` returns are the bytes the run uploaded and
   that nothing truncated or swapped them on the way. It is **not tamper
   evidence**, and no reader may treat it as such: the hash lives in
   `result.json`, in the same repository-scoped prefix, written by an account
   that can write both objects — so any principal who can overwrite an artifact
   can rewrite the record beside it and leave the pair consistent. A sibling run
   of the same repository, a launcher, an operator and (on a profile-wide
   backend, D11) a run of *another* repository can all do it, and none of them
   leaves a hash mismatch behind. Tamper *evidence* needs a key the rewriter does
   not hold — a launcher-signed manifest, or a per-run key outside the run's
   prefix — which is its own design and is **not** in this slice. `SECURITY.md`'s paragraph (§8.9) says this in the same words.
4. **Caps stop the disk-filling loop, and they are enforced twice** — by the
   launcher (before the upload) and by the runner (before the download and
   before the upload), because the task spec is text the launcher wrote. An
   over-cap set is refused with a named reason; a glob that matches nothing is
   **not** an error by itself (the run may legitimately produce nothing) but it
   is reported, and the probe is what turns "nothing" into a failure if the
   owner wants one. **The limit behind these is the container's memory, not a disk**: on the
   second-generation execution environment the filesystem is in-memory and there
   are no volumes (§5.2, U3), so a staged set is memory the render may also need
   for its own temp files. **A cap that is too generous therefore does not
   produce a clean refusal — it produces a killed container**, and a killed
   container writes no `result.json`, so the run's record cannot say what
   happened and the reader sees a run that stopped mid-render. **Staging is a
   copy, so the peak is two sets, not one** — the render's outputs in the
   checkout and the runner's staged copies, both on the memory-backed
   filesystem and both alive until the uploads finish — which is why the
   refusal is on **`2 × outputs.max_total_bytes + inputs.max_total_bytes`**
   against `resources.memory` (§5.2, the plan's P5 and Review Focus 12) and why
   the caps' defaults are conservative to the point of boring (8 files × 256 MiB
   each, 2 GiB of outputs, a 4 GiB doubled peak, 1 GiB of inputs at their
   default, 5 GiB in all against a 32 GiB ceiling), why the docs tell an owner
   who raises them to raise `resources.memory` past twice the output set plus the
   input set, and why Check 58's step 7 has a
   third half that deliberately drives the container over its memory rather
   than over a cap — the design says the OOM direction is *observed, then
   written down*, not assumed away. **The two ways to avoid the doubling were
   considered and are both worse:** a **hard link** into the staging root is
   the same inode, so the bytes stay as agent-writable as the checkout's copy —
   precisely what staging exists to stop (P5's own veto) — and a **rename** out
   of the checkout removes the render's outputs from the tree the record and
   the agent's plan describe. Two copies is the price of "the bytes uploaded
   are the bytes the runner staged", and it is paid in memory the workflow
   declares, not in correctness.
5. **Who can read an artifact, at each phase, and what that costs (D10).**
   Today: **every launcher and every operator of the installation** (a launcher
   holds `objectViewer` on the whole bucket, `docs/design/bucket-iam.md:126-128`;
   an operator `objectAdmin`, `:88`), and **every run of the same repository**
   (`runs/<slug>/` is the job account's condition, V4) — which for an artifact is
   not a diff but a rendered file, so "the repository can see it" means "every
   one of that repository's runs can replace it with a different video and a
   consistent hash". **Two futures widen it, and both are decided elsewhere:**
   project-centric **phase 3** and the **VM backend** give a run a grant that is
   not per-repository (`docs/design/project-centric.md:15,66-68`; the VM design
   binds a run to **the job account of (`profile`, `account`)** — one account
   per repository today, *never* the default Compute Engine service account, and
   **there is no per-run account** — `docs/design/compute-backend.md:1007-1030`,
   §1 `:525-528`), so "every run of this
   repository" becomes "**every run on this profile**" — artifacts, inputs and
   records alike. **D10 decides the first half of this; D11 decides what the phases do to
   it.** **Recommendation: state it now, and make the narrowing a
   precondition of the first of those that lands** — a per-run condition
   (`runs/<slug>/<run>/`, or a per-run account) is not expressible in the
   compiled-in `JobBucketPrefixes` grant today (`internal/backend/gcp/names.go:374,383-389`),
   and it is exactly what those designs must add before an artifact-producing
   run is enabled on them. This design adds the *warning* (the docs' and
   SECURITY.md's wording) rather than the mechanism.
6. **The render step's environment is the agent's, and that is deliberate.**
   `commands.render` runs as the agent's user, with the agent's environment
   (`r.env`), in the checkout — which means **the workflow's secrets are in its
   environment**, unlike a review run's agent, which gets none of them
   (`runner.go:1091-1099`, `!r.review`). That is the right answer for artifact
   mode and it is a decision, not an oversight: an artifact run checks out the
   repository's **own ref** (there is no pull request whose head is untrusted),
   so it stands where an implement run stands, and a render that needs a token to
   publish somewhere needs the same environment the agent had. **The design keeps
   `!r.review` exactly as it is and says so**, so no reader infers a secret
   boundary that is not there.
7. **Signed URLs are the launcher's, never the job's, and they are not in the
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
8. **Attachments are a prompt-injection surface, and they are also a
   data-policy question — the two are separate and both are answered here.**
   **Injection:** a screenshot with text in it, a CSV with instructions, a
   filename that reads like a request — all of it reaches the agent. This is not
   new (the task text already does, `SECURITY.md:67`), and it must be said out
   loud in the docs: *attaching a file is writing part of the task.*
   **Destination (D9's mechanism, and the round-3 review's correction):** the
   attached bytes reach the model, so the launch prints **one line naming every
   destination they may reach through it**, and the hard part is getting that set
   right rather than empty.

   **What the set is, exactly.** The model route is decided by
   `config.ProviderFor` over the local config's `providers` block
   (`internal/config/providers.go:268-275`) for each model the run will use
   (`config.NeedsAnthropicKey`'s three, in its own order — the coder's, the
   reviewer's and `config.EffectiveBackground`, `internal/config/providers.go:670`;
   **not** `Agent.PinnedModels`, whose third entry is the raw
   `Models.Background`, empty unless the config sets it: a run whose coder a
   provider claims never makes an unset-background call to Anthropic at all,
   so `PinnedModels` would name a direct route for a run that makes no such
   call); a model no such provider claims goes to the run's **own auth route**,
   which `agent.auth` decides: `vertex` — Fugaro's default, written into every
   config the launcher reads by `config.applyDefaults`
   (`internal/config/defaults.go:48-50`) — is Claude on **Google Vertex AI**,
   `api-key` is Anthropic's own API, and `oauth` is Anthropic on a subscription's
   token, never proxied (`docs/design/m9-budget-and-dashboard.md:8`). Naming one
   of the three for all three would tell the owner of a default configuration
   that a screenshot goes straight to Anthropic while the call, and the bytes in
   it, go to Google. The
   only providers a run can use at all are the ones that list the repository in
   `allow_data_to` (`config.ModelProvider.AllowsData`,
   `internal/config/providers.go:194`; the job's environment is built from
   exactly that filter, `runner.ProvidersEnv`, `internal/runner/providers.go:29-51`).
   So the line is the union of:

   - **each allowed provider that claims one of the run's models**, by name; and
   - **the run's own auth route**, whenever any of the run's models is *not*
     claimed by one of those providers: `Google Vertex AI` under `agent.auth`
     `vertex` (the default), `Anthropic (direct)` under `api-key`, and
     `Anthropic (oauth token)` under `oauth`.

   **The correction the review asked for:** `lc.Providers` is documented as
   **"the non-Anthropic backends models may be routed to"**
   (`internal/localcfg/localcfg.go:89-91`), so a set built from it *alone* is
   **empty for a default Claude workflow** — and a line that read "routes
   nowhere" there would be telling an owner their screenshot stays local while it
   is on its way to the model's route. The line therefore never claims the file
   stays on the machine unless **there is no auth route and no claiming
   provider**.
   **The round-4 correction:** the route an unclaimed model takes is the run's
   `agent.auth`, so naming "Anthropic (direct)" for it was wrong for the default
   configuration — `applyDefaults` writes `vertex`, so those bytes go to Google
   Vertex AI — and the line names the auth mode's own destination.

   **And the gate it is not:** `allow_data_to` answers "may this repository's
   content reach this provider?", and an attachment is *task text*, not
   repository content — so this design does not invent a second policy, does not
   refuse a launch, and says so (D9). The line is printed on the `warn` writer,
   once per launch, **only when `--attach` carried a file**, exactly where the
   other launch notes go.
9. **`SECURITY.md` carries this text, and this block is kept equal to it.**
   **The file is the source of the wording:** where the two disagree, the shipped
   `SECURITY.md` wins and this block is corrected to it, not the other way round
   (the four-paragraph shape below is what shipped; the review that produced it
   is also why the third paragraph names symbols rather than `runner.go` line
   numbers — those had drifted — and why the first says the render is repository
   text read once and is not a boundary against the agent). It goes in
   **"### The assumption"** (`SECURITY.md:21-27`), as the paragraph after the
   existing one:

> **A run may also be given files, and may also be asked for one.** An artifact run (`docs/design/artifact-runs.md`) has no git token and no pull request: it is handed files the launcher attached, and it leaves files in the runs bucket instead. Two consequences follow, and neither is a sandbox. **In:** an attached file is trusted exactly as much as the task text, since the agent reads it and can act on what is in it — a screenshot can contain a token, a customer's name or someone's face, and an operator who attaches a file they did not write has handed the agent that content as an instruction. **Out:** an artifact is output of the same unsandboxed agent, written to `runs/<slug>/<run>/artifacts/` under this repository's job account — the account a run of this repository runs as; there is no per-run account — so every launcher and every operator of the installation can read it (a launcher holds `objectViewer` on the whole bucket, an operator `objectAdmin`), every run of the same repository can read and overwrite it (the account is scoped to the repository's prefix, not to one run), and it is deleted with the rest of the run's record after 90 days by default. Nothing about an artifact is trusted: `fugaro artifacts get` verifies the sha256 recorded by the run, refuses a mismatch, never opens, executes or unpacks what it writes, and downloads into a **fresh directory it creates** (never `.`) — so a crafted name in a tampered record cannot land a `.git/hooks/` or `.claude/` file in a checkout you are working in. **And what produces it is repository text read once, which is not a boundary against the agent:** `commands.render` and `commands.probe` are the repository's own commands, kept in the runner's in-memory workflow configuration rather than re-read from a file the run itself can write, so the command string that ran is the string a reviewer read at the task's ref — but the plan stage and the render step are the same container, the same user and the same environment, so a render command that names a file in the checkout runs **that file's working-tree bytes**, which the plan stage could have rewritten, no more and no less than every other file in a checkout the agent controls.
>
> That hash is **integrity in transit, not tamper evidence**: it and the artifact live in the same repository-scoped prefix, so anything that can rewrite one can rewrite the other — including another run of the same repository. Tampering is not detectable today; what is guaranteed is that the bytes you receive are the bytes the run uploaded.
>
> **The render step runs with the run's own environment, secrets included.** `commands.render` executes as the agent's user with the agent's environment, so a workflow's secrets are visible to it — that is deliberate and is why an artifact recipe must not be handed to an untrusted repository. The runner builds the workflow's secret names into that environment only when `!r.review` (the `secretEnvs` block in `internal/runner/runner.go`), hands the result to `r.env` via `agent.BuildEnv`, and `runRender` in `internal/runner/artifact.go` starts the command with `Env: r.env`. It also means a render command can write a secret into a deliverable, and the deliverable is readable by everyone the paragraph above names.
>
> **Today that "everyone" stops at this repository, and that boundary is a precondition, not a property of artifacts.** An artifact run's account is the repository's job account, so only runs *of this repository* can read or overwrite its deliverables. Project-centric phase 3's shared project jobs and the VM backend's accounts are **not** per-repository: on either of them the same run's inputs, artifacts and record would be readable and overwritable by **every run on the profile**. Because that blast radius has not been reviewed, **an artifact-producing run stays on the plain per-repository Cloud Run backend until a per-run prefix condition or a per-run account exists** (`docs/design/artifact-runs.md` §8.5, D11). **No code in this slice enforces that, and this document does not claim it does**: it is a decision about when artifact runs may be *enabled* on those backends, and the plan that does so must bring the per-run boundary with it. Until then an artifact run is only ever launched on the per-repository Cloud Run backend this installation uses today.

   And in **"Known limits"**, one bullet:

> - **An artifact run's compute is not capped.** The budget's caps count model
>   dollars (`docs/design/m9-spec-v3-reconciliation.md` §7.4's reconciliation),
>   and a render is compute: it is bounded by the run's own clock
>   (`timeouts.total`, and a Cloud Run task timeout of `total + 2m`) and by the
>   workflow's `resources`, never by `budget.per_run_usd`. `result.json`'s
>   `cost.compute_usd` is an estimate, reported and never charged. If you give
>   an artifact workflow 8 vCPU, a two-hour total, and a command that never
>   finishes, you pay for two hours of 8 vCPU and the caps will not stop it.

10. **What this design adds to the launcher's powers: nothing.** Attaching a
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
  one artifact (or all of them with no `NAME`), verifies the sha256 and the
  byte count against the record, refuses to overwrite without `--force`, and
  never opens what it writes. A mismatch is an error with both hashes printed,
  and **nothing is written**. **The destination is a fresh directory it creates
  and defaults to (`./<run>-artifacts/`; `--out` names its parent), never `.`**,
  and a record name that is absolute, holds a control character or a backslash,
  has a `..` element, begins with a dot or escapes that root is refused — a
  checkout's `.git/hooks/`, `.claude/` or `.envrc` is what an untrusted record
  would otherwise be able to write (§8.2, §8.3). **Two record names that fold
  to one** — the same key as §8.2's duplicate rule (NFD of the full case fold
  of NFD), so `straße.png` with `strasse.png` — **are refused as the duplicate
  they are**, before a byte is read: two downloads into one directory, on a
  filesystem that folds, would leave one file and print two success lines.
  **And the refusal to replace is enforced twice, at two different moments**
  (§8.2's "it refuses to overwrite an existing file without `--force`"): once
  as a pre-check of every destination before the first byte is read, and once
  **by the placement itself, which is what makes the promise race-proof** — the
  pre-check cannot see a file that arrives while the object is being read, nor
  what a folding filesystem makes of two different names, so without `--force`
  the verified temporary is put in place in a way that **fails rather than
  replaces** (`blobx.GetFileExactly`'s `replace` argument: a hard link, or an
  `O_EXCL` create plus a copy where the filesystem has no hard links, and
  `ErrDestExists` either way). The operator's file survives, the download is
  refused, and the same `--force` wiring is what permits the replace when it is
  given.

  **The two sides are one rule, and the design states it once so the upload
  path cannot produce what the retrieval path refuses.** The upload sanitiser
  refuses a name beginning with a dot for the same reason `get` does (§8.2), so
  a run cannot record a `.hidden.mp4` that `get` will never hand back. That
  refusal is load-bearing rather than cosmetic, because **Go's `filepath.Glob`
  and `filepath.Match` match a leading dot** — unlike a POSIX shell — so
  `out/*.mp4` in a recipe *does* select `out/.hidden.mp4`, and without the rule
  the run would upload and record an artifact the retriever refuses. For a
  record that carries such a name anyway (written before this rule, or by a
  tamperer, whose names are untrusted text, §8.3), the behaviour is fixed and
  fail-closed:

  - `fugaro artifacts ls RUN` **lists it**, flagged as unretrievable, with the
    reason — the record is shown as it is, never silently filtered;
  - `fugaro artifacts get RUN` (no `NAME`) **refuses wholesale and names the
    offending entry**, writing nothing: an "all artifacts" download that skips
    one would tell the owner everything was retrieved when it was not;
  - `fugaro artifacts get RUN <that name>` refuses it too, and prints the reason.

  **No other name is affected**: a dot *inside* a name (`sizzle.v2.mp4`) and a
  name in a dotted *directory* below the outputs root (`clips/a.mp4`) are both
  fine — the refusal is only on a name whose first character is `.`, which is
  how the retrieval path's own rule is worded.
- **`fugaro runs ls`** gains a column (or a `--json` field) for the artifact
  count and total bytes, and **`fugaro diagnose`** names the artifacts beside
  `report.md` (the path shape it already prints,
  `internal/cli/diagnose.go:158`). **Both read `result.json` only**; neither
  enumerates the bucket.
- **The pull request report** is not applicable (there is no PR). The *run's*
  `report.md` gains an "Artifacts" section with name, size, sha256, content
  type and the probe's verdict, and it says the compute estimate and the
  render's wall clock. **The outcome switch has to be taught the new value**:
  `internal/runner/report.go`'s three cases end in `default: "**Outcome:** draft — {reason}"` (`:72-79`), so `OutcomeArtifacts` would otherwise render a
  successful artifact run as a draft pull request with an empty reason. The same
  value is read at `internal/cli/cancel.go:468` (`NoPR = Outcome == OutcomeNone`),
  which needs the arm as well: without it `fugaro cancel <run>` describes a
  **succeeded** artifact run as though it had opened a pull request. **The halt
  path does not**, and the first draft of this design said it did:
  `internal/runner/halt.go:177`'s continuation advice is `"start the run again"`
  exactly when `rec.Outcome == OutcomeNone`, and a halted artifact run *is*
  `OutcomeNone` — `OutcomeArtifacts` is the success value, so a halted run never
  carries it and the existing arm is already the right advice (a case the plan's
  Review Focus 11 records).
- **`fugaro watch` reads the same value, and it is where an owner actually sees
  a run end.** Two display switches read a *row's* outcome straight out of
  `result.json`, and **neither has a `default:` arm**: `internal/watch/plain.go:99`
  (`switch f.Outcome`, arms for `"ready"` and `"draft"` only, over
  `word := strings.ToUpper(f.Stage)`) and `internal/watch/render.go:653` (the
  same two arms, same fallback). The failure is quiet rather than loud — an
  artifact run is drawn as its bare stage word and its outcome never appears —
  which is worse for a reader than the report's wrong word, because nothing
  looks amiss. **Both gain an `"artifacts"` arm**, printing a word no stage can
  produce (`ARTIFACTS, NO PR` plain, `artifacts, no PR` in the dashboard, in the
  success colour where `"ready"` prints `READY FOR REVIEW`), so that
  `SUCCEEDED` is never the last word on a run that did not do what a
  `SUCCEEDED` implement run does. **`ReadyForReview` (`internal/watch/filter.go:67`,
  `r.Outcome == "ready"`) is deliberately unchanged** — an artifact run is not
  ready for review and must not be listed under that heading — and the enum's own
  documentation, `internal/watch/finished.go:17`'s `// ready, draft, none,
  reviewed`, is updated with it. That is **four** call sites in all (the two
  record-reading ones above — the report's and `cancel`'s — and these two
  row-reading ones; `halt.go` reads the value but never sees the new one), all in
  the plan's Task 2.
- **A signed URL is not offered** (§8.7): the operator's own credentials are
  what the retrieval path uses.

## 10. Task outline, and where it lands in the roadmap

The full plan is [../plans/2026-10-15-artifact-runs.md](../plans/2026-10-15-artifact-runs.md).
**The outline is the plan's own task list, with the plan's numbers**, so a
cross-reference here and one there name the same task:

1. **The format**: `mode: artifact`, `plan`/`render`, `outputs`, and the
   `commands.render`/`commands.probe` keys and `timeouts.render` — the recipe
   schema, the parser, the config types and defaults, the validation refusals —
   **and the runner's own fail-closed refusal of a `mode: artifact` recipe
   it does not implement**; **no `verify.Settings` field is added**, because the
   render and the probe read the runner's in-memory config and `Settings` is a
   0644 file the plan stage can rewrite (§3) (§10's release note: without that arm the recipe
   falls through to the implement path and opens a pull request). An older binary
   refuses the recipe, a newer one refuses `outputs:` on a `mode: implement`
   recipe, and a runner that knows the format but not the mode refuses to run
   it.
2. **`mode: artifact` in the runner, and the render step**: the bootstrap
   branch, the plan stage, deterministic execution of `commands.render`, its
   deadline, its failure path, and the `render` record **with its own
   `runstore` types (`RenderRecord`, `OutcomeArtifacts`, `Record.Render`) and
   the `outcome` enum's new value**, because a task that records something must
   own the type it records — **and every place that reads the value**, which is
   **four sites** and not one (§9): the report's `default:` arm, `cancel`'s
   `NoPR`, and the two `fugaro watch` display switches that would otherwise draw
   an artifact run as a bare stage word. **`halt.go` is not one of them**, and
   the first draft of this design wrongly said it was: a halted artifact run
   ends `OutcomeNone` (§9), for which the existing advice is already correct.
   Proven with an in-memory bucket and no cloud.
3. **Two streaming `blobx` methods**: `PutFile` and `GetFile` — the byte
   layer the rest needs, since every write today is a `[]byte` and every read
   is capped at 1 MiB (§3, V23). `PutFile` aborts (cancels) the write before
   closing it on any error, so a failed upload never commits a partial object;
   `GetFile`'s 403 classification is **per caller** — non-strict for the
   runner's input download, strict for the CLI (§3).
4. **The outputs**: selection, sanitisation, caps, hashing, upload to
   `runs/<slug>/<run>/artifacts/`, and the `result.json` + schema additions.
5. **The probe**: `commands.probe`, the two environment variables, the
   record.
6. **The attachments**: `--attach`, the task-spec list, the launcher's
   **refusal of any launch that does not resolve to an artifact recipe** (§4),
   the runner's download (with its own name refusals, hash check, and the
   empty-`Spec.Mode` arm of §8.2), the caps, the `.git/info/exclude` write.
   (D6: the same release, a separate commit.)
7. **The CLI**: `fugaro artifacts ls|get` with hash verification, and the
   `runs ls`/`diagnose` display.
8. **`budget preview`'s compute ceiling line** (§6), labelled "not capped", and
   **`recipe.StageBound`'s artifact arm** — without it the *model* ceiling
   above that line prices an artifact recipe as five stages where a run of it
   starts one (§6).
9. **The docs and the skills**: a new `docs/artifact-runs.md` carrying **D8's
   rule** (an inline `commands.render` is what to write, and why), the sections
   in `docs/recipes.md` and `docs/base-image.md` (ffmpeg via `image.apt`), the
   routing skill's "when to choose an artifact recipe", **the SECURITY.md text
   of §8.9 verbatim**, and **Check 58's row and section in the checklist** —
   written last, because the docs and skill lints resolve the flags they name.
10. **Check 58** (owner-run, sandbox only) — §11. Not part of the pull request.
11. **The full suite** and the `fugaro verify build`/`verify test` run.

**And three things that are deliberately not tasks in this plan:** the durable
**retention** prefix (D2's alternative), **signed URLs** (D7) and
**compute-in-the-cap** (D4's alternative). Each is its own design, named here
so this plan is not mistaken for the whole story.

**Nothing above starts until the owner approves it.** Tasks 1–9 are gated on an
explicit go-ahead (the plan's P12): the decisions in §13 and the plan's own P-list
are answered by a yes or by a veto that rewrites the affected tasks, and **this
design assumes none of them by itself**. In particular **Task 1 is not
"approved by default"**: it is D5's answer in code.

**Release placement.** Tasks 1–9 extend the recipe format, add a step kind, add
one optional key to two stored schemas and add a CLI subcommand — all additive,
and all shippable in **0.8.0**, one point release, with a new launch-time
`*Since` constant (`artifactsSince`, through the existing `checkImageSince`
seam, `internal/cli/recipes_skew.go:204-270`) so a recipe or an `--attach` using
the new keys is refused against a job image whose runner predates them.

**Which of them is safe *alone*, and the one that is not unless it is
finished.** A `mode: artifact` recipe against a runner that has the format and
not the mode would take the **implement** path — `runner.go`'s dispatch and its
checkout arm key off the task spec, not the recipe, and a recipe the runner does
not understand falls through to the default arm, where a PR is opened — so
**Task 1 must carry its own fail-closed refusal in the runner** (a
`mode: artifact` recipe that this runner does not implement is refused at
bootstrap, with a test), and then Task 1, Task 8 (`budget preview`, display
only) and Task 9 (the docs) are each safe as a patch on their own. **Task 2
needs Task 1** — it *consumes* the three config keys Task 1 adds
(`commands.render`, `commands.probe`, `timeouts.render`), read from the runner's
in-memory `r.wf`, and brings its own `runstore` types with it (`RenderRecord`,
`OutcomeArtifacts`, `Record.Render`, the `outcome` value) rather than waiting
for Task 4 — and Tasks 3–7 are the feature and belong in the point
release. Nothing here needs the project-centric phases (D1), so it does not wait
on them — and D10 and D11 say what happens the day those phases arrive.

## 11. Check 58, in outline

**Check 58 (OWNER-RUN, NOT RUN)** in `docs/gcp-live-checklist.md`, written with
**Check 43's guard block verbatim** (`docs/gcp-live-checklist.md:1437-1458`):
`P` hard-coded to `fugaro-dev` and never read from `gcloud`, `${P:?}` on every
argument, a `fugaro doctor` check, the checkout's git origin as a hard stop,
**never the `belong` project** (`:1431`), and **every step run by the owner,
none by an agent** — plus a Restore block. **Seven numbered steps**, each
preceded by a re-run of the guard block: (1) the sandbox's prerequisites —
ffmpeg in the image through `image.apt`, one `fugaro image build`, and the
fixture artifact recipe, render script and probe committed to the sandbox
(`image build` and not `image refresh`: refresh decides on the build record's
base alone — `refreshVerdict`, `internal/cli/image_refresh_rules.go` — so a
workflow already built from the pinned base would submit no Cloud Build for a
config-only change, which is the whole of this step);
(2) three tiny PNGs in, generated locally, and the launch with three
`--attach`; (3) a five-second video out — the `render` record, the counters,
the absence of any push or pull request; (4) `ffprobe` verification through
`commands.probe` **and** an independent local `ffprobe` of the downloaded
file; (5) the recorded content type against the object's own (U5); (6) the
artifact retrieved with a **sha256 match**, and the upload's wall clock
against `finalize_reserve` (U6, with its own step **6b** for the near-cap
size: a render-only recipe whose two artifacts are written at exactly
`outputs.max_bytes`, since step 6's own set is a few megabytes per file and
never reaches the size U6 asks about); (7) the failure direction, in three
halves — a probe that must fail the run and upload nothing; an over-*cap* set
that must refuse cleanly and leave the container alive; and an over-*memory*
render, which is the one that decides how a killed container looks from outside,
because a killed container writes no `result.json` at all (U3, §5.2). Then the
Restore.
That list is what the section must keep asserting, in whatever wording the file
has; **the checklist file stays the single source of the wording.** The plan's
Task 9 writes the row and the section (its own assertions list is a checklist
of what they must pin, not a second copy of the steps), and the plan's Task 10
runs them without quoting them back — the rule
`../plans/2026-10-14-openrouter-senior-reviewer.md:146` states, whose finding
was that a plan repeating a check's steps is how the two drift.

**The numbering is checked against the file's own register, not inferred from
the gaps.** `docs/gcp-live-checklist.md` keeps the reservations in prose
(`:250-257` on `origin/main`): **48–56 are reserved by the project-centric
plan** (`docs/plans/*project-centric*`, PR #366, merged as `c86eb92` — reserved,
with no rows written yet, and that plan's sub-checks are not named here), and
**57 by the compute-backend design** (`docs/design/compute-backend.md` §7, PR
#375, whose row is in the table at `:325`). Rows exist for 40–47 (`:317-324`).
**So 58 is the next free number — the first one past every reservation — and it
is this design's**, which is a different statement from the first draft's "48–56
are free", and the right one: a reserved number belongs to the plan that
reserved it even before its row is written.

**And one reservation was made after this design was written: PR #401 (the
desktop console design, open at the time of writing) also claims 58.** This
pull request keeps 58 — it is the design that was written against the register
as it stood, and #401 is not this design's to edit — and **whichever of the two
merges second renumbers itself to the next free number in the register at that
moment** (59, unless something else takes it first), rewriting its own row, its
own section heading and every in-document reference to it. The rule is the
register's own: a number belongs to the check that first wrote a row, and a
duplicate is fixed by the later writer, never by reopening the earlier one.
That is a sentence for the owner in each design, not an edit to #401.

## 12. Verified, and unverified

**Verified offline against the code or the docs** (each with the symbol or
line read for it — the plan's Review Focus repeats the important ones):

| # | Claim | Read from |
|---|---|---|
| V1 | The run prefix is `runs/<slug>/<run>/`, and the object names are `task.json`, `result.json`, `launch.json`, `cancel`, `report.md`, `transcripts/<stage>-<n>.jsonl`, `session/session.json`, `verify/<n>.json` | `internal/runstore/runstore.go:386-387,492,502,547`, `internal/runner/runner.go:1710,2132,2192`, `internal/runstore/session.go:39` |
| V2 | `blobx.Open` accepts `gs://` **and** `file://`, so an artifact path is testable with no cloud | `blobx.Open` call sites `internal/cli/exec.go:146,296`, its flag default `:59`, `internal/e2e/budget_test.go:129` |
| V3 | The bucket deletes `runs/` objects at `runs_days` (default 90); `builds/` has no rule | `deploy/terraform/gcp/modules/installation/bucket.tf:16-24,63`, `variables.tf:109-121` |
| V4 | A run's account is scoped to the **repository's** prefixes (`runs`, `cache`, `locks`), **not** to one run; a launcher reads the whole bucket and writes under `runs/`; an operator has `objectAdmin`; project Owners/Editors bypass it | `internal/backend/gcp/names.go:374,383-389`, `deploy/terraform/gcp/modules/workflow/sa.tf:24-33`, `docs/design/bucket-iam.md:88,126-128`, `SECURITY.md:36,62` |
| V5 | `mode:` is `implement|review`; `review` is the no-git-token, no-PR precedent, with the outcome `reviewed` | `internal/recipe/recipe.go:38-42,356-362,632-639`, `docs/design/generic-tool.md:195-203`, `internal/runner/runner.go:635` |
| V6 | The `check` step is deterministic, runs `verify.Run` in the checkout as the agent's user, may only come first, and may name only `commands.build|test|lint` | `internal/recipe/recipe.go:47-62`, `internal/runner/gate.go:61,125-132`, `internal/verify/verify.go:176,243`, `schemas/recipe.schema.json` |
| V7 | A recipe names no command; bucket text would run code | `docs/design/generic-tool.md:356` (G10's row), `internal/runner/gate.go:125-127` |
| V8 | Timeouts: `total 90m` / `stage min(40m,total)` by default; this repository's `fugaro.yaml` says `2h/90m/25m/5m`; `finalizeAt` is the real deadline; Cloud Run gets `total+2m`; a task may run 24h; the CLI caps `--total-timeout` at 24h | `internal/config/defaults.go:77-90`, `fugaro.yaml:54`, `internal/runner/budget.go:22-34`, `docs/design/v1.md:370`, `docs/SPEC.md:30`, `internal/cli/run.go:976-991` |
| V9 | Cloud Run jobs take 1, 2, 4, 6 or 8 vCPU and at most 32Gi | `internal/backend/gcp/limits.go:25,36-46`, `limits_test.go:11,23` |
| V10 | The caps count model dollars; compute is an estimate, reported and never capped; `compute_usd`/`compute_estimated` exist and are rolled into history | `docs/design/m9-spec-v3-reconciliation.md:10`, `docs/design/m9-budget-and-dashboard.md:798`, `internal/budget/evaluate.go:190-204`, `internal/runner/cost.go:62-64`, `internal/runstore/cost.go:12-18`, `internal/budget/rollup.go:296-303` |
| V11 | `fugaro budget preview` has no compute notion | `internal/cli/budget_preview.go:26-28,489-538,697-731` |
| V12 | mise is in the base image; ffmpeg is nowhere in the repository; `image.apt`, `image.setup`, `dockerfile:` and `image.tools`/`mise.toml` are the four ways to add a tool | `images/base/Dockerfile:43-45,171-181`, `images/derived/Dockerfile.tmpl:26-33`, `internal/image/render.go:92-117`, `docs/base-image.md:36-47,63` |
| V13 | No artifact/attachment/output concept in `generic-tool.md`, `recipes.md` or `v1.md` — except that v1's 15 hits are Artifact Registry (the GCP service) but one is `ARTIFACTORY_TOKEN` (`v1.md:542`, a JFrog token) — and none in the schemas or the CLI | grep over the tree, `schemas/recipe.schema.json`, `schemas/result.schema.json`, `internal/cli` |
| V14 | No signed URL and no `signBlob` grant anywhere; only `iam.serviceAccounts.signJwt` for the budget identity | grep over `internal/` and `cmd/`, `deploy/terraform/gcp/modules/firebase/iam.tf:23` |
| V15 | `result.json`'s schema is `additionalProperties: false` and is validated **only by tests**, never at read time; readers use the Go struct | `schemas/result.schema.json`, `schemas/schemas_test.go:23`, `internal/runstore/runstore.go:532` |
| V16 | `runs ls` and `diagnose` read named files, never enumerate the prefix | `internal/cli/runs.go:379-390`, `internal/cli/diagnose.go:158,177` |
| V17 | The launcher mints the run id, writes `task.json`, then submits the execution — so `--attach` has a place to stand | `internal/cli/run.go:887,1329,1574`, `internal/runstore/runstore.go:509` |
| V18 | The VM backend is a merged design (#375) with **Tasks 1–3 merged** (#380, #381, #389 — the price table included); `backend: cloud-batch` is refused by config validation, not by a flag; a VM run prices the machine type's quantities from its boot window | `internal/localcfg/localcfg.go:711-719`, `internal/backend/batch/prices.go` (`MachineType`, `MachineTypeOf`, `ForMachineType`), `internal/backend/backend.go:103,147,174-205,285-289,292-296` (`MachineType`, `VMMinimumBilled`, `BilledWindow`, `WindowNote`, `Prices.Quantities`, `Prices.ComputeUSD` — it is `backend.Prices.ComputeUSD`, there is no `pricing.ComputeUSD`), read at `origin/main` b9d5c5d |
| V19 | GPUs are out of scope in the VM design, by name | `docs/design/compute-backend.md:1485` (§6.3) |
| V20 | A repository-less run needs project-centric phase 3's project jobs, which are not shipped; `checkout: clone` is not shipped either | `docs/design/project-centric.md:15,66-68,109`, `internal/cli/recipes_skew.go:51-58`, `internal/cli/docs_checkout_test.go:123-132` |
| V21 | Check numbers: rows exist for 40–47 (`:317-324`) and 57 (`:325`); **48–56 are reserved by the project-centric plan** (PR #366) and 57 by the compute backend, so **58 is the next free number**; Check 43's guard block is at `:1437-1458` and "never `belong`" at `:1431` | `docs/gcp-live-checklist.md:250-257,317-325,1431,1437-1458` |
| V24 | A repeated `--run-id` adopts a byte-identical stored task and refuses a different one, **after** `CreateTask` — so an upload ordered before it would rewrite a finished run's inputs | `createTask` `internal/cli/run.go:1328`, `runstore.CreateTask`/`ErrExists`, `blobx.Create`'s `IfNotExist` `internal/blobx/blobx.go:107-127` |
| V25 | An artifact run would checkpoint like an implement run unless it is stopped: the gate is `checkpointsOn()`, not the checkout arm | `internal/runner/checkpoint.go:65` (`r.follow == nil && r.baseSHA != "" && r.startSHA != "" && cfg.Git.PR.CheckpointsOn()`), `runner.go:1684` (`startCheckpoints` inside the stage runner) |
| V26 | The agent's git credentials enter its environment at **two** sites inside `bootstrap` (the repo env and the stage env map), and the workflow's secrets at a third (`!r.review`) | `internal/runner/runner.go:635`, `:1135`, `:1091-1099` |
| V27 | `verify.Settings` is written at bootstrap, before any stage, from the workflow's commands — and it is a **0644 file re-read per invocation** by `verify.LoadSettings`, so it is not a place a fixed command can live | `verify.WriteSettings` call, `internal/runner/runner.go:1073-1081`; `internal/verify/verify.go:26,60,85` |
| V28 | The runner's workflow config is read **once** and kept in memory; nothing re-reads `fugaro.yaml` afterwards, and the sites that switch on `runstore`'s outcome value are four: the report's, `cancel`'s, and the two `watch` displays. `internal/runner/halt.go:177` **reads** it but never sees the new value — a halted artifact run ends `OutcomeNone` — so it is not a fifth | `r.cfg, r.wf` at `internal/runner/runner.go:962`; the outcome sites `internal/runner/report.go:72-79`, `internal/cli/cancel.go:468`, `internal/watch/plain.go:99`, `internal/watch/render.go:653`; `internal/runner/halt.go:177` reads `OutcomeNone` only |
| V22 | The three PNGs the check generates are real PNGs: the generator the plan commits to (a `python3` one-liner over `zlib`/`struct`, in Check 58's section) was run in this session and produced three valid 320×240 8-bit RGB PNGs, 865 bytes each | run in this session; the command is in the plan, Check 58's step 2 |
| V23 | `blobx` has no streaming write and no read above 1 MiB (`MaxReadBytes`), and no `Put`/`Create` variant that takes a path | `internal/blobx/blobx.go:51-54,109-138,148-198,314` |

**UNVERIFIED, with the check that would settle each:**

| # | Unverified | Settled by |
|---|---|---|
| U1 | Whether a GPU can be attached to a Cloud Run job (or to a Batch VM) in the owner's project, and at what price | an owner-run probe in the sandbox (a job submitted with a GPU request) — **not** promised anywhere in this design (§5.4) |
| U2 | Whether mise's registry can install `ffmpeg` (an asdf/community plugin, or a backend) | `mise ls-remote ffmpeg` on the base image; the design does not depend on it (`image.apt` is the recommendation) |
| U3 | The container's own filesystem: the platform's model is an in-memory filesystem with no volumes (**not stated anywhere in this repository** — this design takes the platform's documented behaviour, not a repo fact), so what a render's temp files do to the job's memory, and **what an OOM kill looks like from outside** (a run with no `result.json`, or a recorded failure?) is unverified | Check 58 step 7's third half: a render driven past the container's memory, observed and written down |
| U4 | Whether a Cloud Run job's `resources` may be raised on a *running* job's future executions without a new job (it cannot today: the resources are in the job spec) | an owner-run `gcloud run jobs update` on the sandbox; it affects only the docs' "raise the ceiling" advice |
| U5 | Whether content-type sniffing is needed at all in practice (what the object store reports for a `.mp4` written by ffmpeg on the base image) | Check 58 step 5's recorded type |
| U6 | The real wall-clock of the upload inside `finalize_reserve` for a file near `max_bytes` | Check 58 step **6b** — a render-only recipe whose two artifacts are written at exactly `outputs.max_bytes`, because step 6's own set is a few megabytes per file and never reaches that size — plus the cap's own default choice |

## 13. Open for the owner

Each item is a decision the plan's tasks depend on, with the recommendation this
document argues for and the cost of the alternative. **D1–D7 answer the brief's
own questions; D8–D11 are the ones this design found while writing it, and two of
them (D8, D10) would have been silent overclaims if they had been left implicit.**

**The owner answered all eleven on 2026-10-15, and the answers are recorded in the
last column.** Every one was the recommendation this document argues for, with one
piece of history worth keeping: **D1 the owner first answered "repository-less",
then resolved the conflict with D11 as "repo-bound first"** — the repository-less
end state is on record, not withdrawn (§13.1). **An answered decision is not an
approved task**: the answers settle *what the design is*, and the plan's tasks
still wait for the explicit yes the plan's P12 describes. A veto now rewrites the
tasks the answer touches rather than being worked around, exactly as before.

| # | Question | Recommendation | The alternative, and what it costs | Owner's answer 2026-10-15 |
|---|---|---|---|---|
| **D1** | **Repo-bound or repository-less?** An artifact run needs a checkout for its recipe, its scripts and its `mise.toml`. | **Repo-bound: an "assets" repository** (the owner's existing one) holds the recipe and the render script. It keeps the format's safety rule (*a recipe names no command*, and nothing from the bucket is executed, V7), it versions and reviews the script, and it makes "the run proposes a PR for a script change" a *later* slice rather than a redesign. | **Repository-less runs under a profile.** They do not exist: they need project-centric phase 3's project jobs (V20), and a `checkout: none`; and their render script would have to come from the **bucket** — project recipes are bucket text, which the format deliberately refuses to execute (V7). Cost: the whole of phase 3, plus a new exception to the safety rule, for a case ("I have no repository at all") the owner does not have. | **Repo-bound first — and repository-less is the recorded end state.** Slice 1 uses the owner's **assets repository** for the recipe and the render script, on the per-repository Cloud Run job. The owner first answered repository-less, then resolved the conflict with D11 as repo-bound first; the end state and what it needs are §13.1. |
| **D2** | **Retention.** | **Under the run prefix, expiring with the record at `runs_days` (90 by default)** (V3), with `fugaro artifacts get` and `gcloud storage cp` documented as the way to keep one. | **An `artifacts/<slug>/…` prefix with its own `artifacts_days`.** Right for a deliverable that must outlive its run; costs a new lifecycle rule, a new variable, **a change to every job's IAM condition** (`JobBucketPrefixes` is compiled into each grant, V4) — a plan and apply per repository — and an unanswered question about which account writes there. Recommended as its own design **once artifacts are in use**, never a separate bucket. | **Yes — with the run record, `runs_days`, 90 by default.** No new prefix, no new lifecycle rule. |
| **D3** | **Where heavy renders run, and GPUs.** | **The Cloud Run job, as it is** (V8, V9): 8 vCPU / 32 GiB / a 24h-class task timeout, the existing `compute_usd` estimate (`backend.Prices.ComputeUSD`, priced by `quantities` — the machine type's on a VM, the workflow's here), per-workflow `resources` and `timeouts` already there. **GPUs are promised by nothing** (§5.4, U1, V19). **The memory ceiling doubles as the staging ceiling** (§5.2): the container's filesystem is memory, staging is a copy, so **`2 × outputs.max_total_bytes + inputs.max_total_bytes`** must fit inside `resources.memory` with room left for the render. | **The VM backend** (Docker-capable VMs, no 8-vCPU ceiling). Its price table is merged (V18, #389), so `compute_usd` survives the switch; what it costs is the backend's *enable* work (the reaper, the log routing, the installation-level Batch stage, and the deployment's account binding — **one job account per repository today; the VM design has no per-run account**, `docs/design/compute-backend.md:1007-1030`) and, per D11, a wider read/overwrite blast radius. Worth it the day a render hits the ceiling or needs a container the derived image cannot carry — that is the trigger. A **GPU** answer needs U1 first, in either substrate. | **Yes — the Cloud Run job now; no GPUs promised.** The memory ceiling stays the staging ceiling. |
| **D4** | **The budget model: is the timeout the cap?** | **Yes, in the first slice**: compute is reported (`compute_usd`) and capped only by `timeouts.total`/`timeouts.render`/Cloud Run's task timeout (V10), and the docs, the report and `budget preview` all say **"not capped"** out loud. | **Compute inside the cap**: the spec sketches a 60-second heartbeat reserving vCPU/GiB-second cost through the lease flow (`docs/design/m9-spec-source-v3.md:493`). Costs a new halt reason, a decision about which caps count compute (all three?), and — the price tables themselves being merged now (V18, #389) — a decision about **which quantities a row bills**, which `backend.Prices.Quantities` currently answers from the table rather than from the cap, plus an interaction with the sweep. A separate design, recommended next. | **Yes — the timeout is the compute cap, said out loud** in the docs, the report and `budget preview`. |
| **D5** | **`mode: artifact`, or a general `outputs:` on any recipe?** | **`mode: artifact` with `outputs:` legal only there.** `mode: review` is the precedent for a run whose shape changes (V5), the mode is where "no PR, no git token, no implement" already lives, and the restriction keeps a new key off every existing recipe. | **`outputs:` on any recipe** (so an implement run could ship a PR *and* a file). It is the better end state and the natural second slice; it costs a decision this design does not need yet — what a run that is both a PR and an artifact means for readiness, the PR's draft state and the report — and it widens the surface of every recipe in the catalog. | **Yes — `mode: artifact`, with `outputs:` legal only there.** |
| **D6** | **Attachments in the first slice, or a second one?** | **The same release, as its own commit (the plan's Task 6).** The owner's use case *needs* files in: artifacts out with nothing in is a demo, not the sizzle video. | **Outputs first.** A smaller review surface, and the same two schema changes (task spec + result) are needed either way — so it buys one commit's worth of review and costs the owner a whole release before their own use case works. | **Yes — attachments in the same release, as their own commit** (the plan's Task 6). |
| **D7** | **Signed URLs now, or later?** | **Later, and launcher-minted only.** No grant in this installation can sign (V14), no job needs one (it writes with its own account), and the launcher and operator can already read the bytes (V4) — so `fugaro artifacts get` + `gcloud storage cp` is the path today. | **Now, minted by the launcher with a hard TTL.** It is genuinely nicer for a browser (a video you can click); it costs a new IAM grant (`signBlob`), a TTL and audit story, and a bearer credential for bytes that may be a customer's screenshot — its own design, not this one's. | **Yes — later, and launcher-minted only.** |
| **D8** | **Who authors the render command, and what the "no agent text" claim means.** | **Repository text from `fugaro.yaml`, kept in the runner's in-memory workflow config read once at bootstrap — never re-read from `<StateDir>/workflow.json`, which the plan stage can rewrite (0644, same user) — with the design claiming exactly three things and no more** (§3): the recipe, the bucket and the launcher name no command; the command *string* is fixed before the agent starts, so `render.command_sha256` means something; and **the step is not a boundary against the agent** — same container, same user, same environment. **The rule the docs and the skill carry: make `commands.render` the script itself** (an inline `\|` block), so nothing it runs comes from a file the plan stage could rewrite; a command naming a file is permitted, works, and is documented as executing the working tree's copy. | **A pristine tree at the ref for the render** (`git archive`/a second worktree), so a named file's bytes are the commit's. It answers the file question, not the boundary question (the agent still shares container, user and `PATH`), and it costs a second copy of the tree on a memory-backed filesystem (§5.2) — the right shape the day a render must be reproducible from the commit, not the right price for the property this design actually claims. **Operator-config-only** (`commands.render` from the project layer) is the other alternative, and it takes the script out of the repository that owns the tool. | **Yes — repository text read once at bootstrap, never re-read from `<StateDir>/workflow.json`, with the residual risk named.** The rule the docs and the skill carry is the one above: write `commands.render` as the script itself. |
| **D9** | **Attachments and the data policy: does a screenshot the owner hands to a run need `allow_data_to`?** | **State it, gate it with the same policy that already governs the repository, and do not invent a second one** (§8.8, §4): an attached file is *task text*, not repository data, so `allow_data_to` — which answers "may this repository's content reach this provider?" — does not literally cover it; but the *consequence* is the one the policy exists for, because the plan stage reads the file and the model sees what it holds. **Recommendation: `fugaro run --attach` prints one line naming the provider the workflow may route to, and the docs say plainly that attaching a file is sending it to that provider under the same policy the model already runs under** — no new key, no new gate, no new refusal; the run's record carries the inputs' names and hashes so "what did we send" has an answer. | **A hard refusal** — `--attach` refused unless every provider the workflow may route to is one the repository already allows: sound in principle and it needs a new cross-check between the launcher's attachment path and the routing config, plus a decision about the case where nothing receives the file at all (no claiming provider and no Anthropic path — the design's own empty-set branch, §4) and about a workflow whose route is chosen at run time. Bigger than this slice, and it is the natural follow-up to `provider-price-catalog`'s own gate. **The third option — say nothing** — is the one to reject outright: an owner leaking a customer's screenshot through a tool that never mentioned the provider is exactly the failure the docs exist to prevent. | **Yes — one printed warning line naming the provider the workflow routes to, the existing policy reused, no new gate, and the inputs' names and hashes in the record.** The round-3 review found the wording wrong for a default Claude workflow: the line names **Anthropic (direct)** as well as any provider in `lc.Providers` — the rule is §8 item 8. |
| **D10** | **Who can read what a run produced, and what is in the render's environment?** | **The facts, stated rather than implied** (§8.3, §8.5, §8.6): an artifact and an input are readable by **every launcher and every operator of the installation** (`objectViewer` on the bucket, `objectAdmin` for an operator) and by **every run of the same repository** (the job grant is the repository's prefix) — and the render step runs with **the agent's own environment, including the workflow's secrets**, because an artifact run checks out the repository's own ref rather than an untrusted pull request's head. **Recommendation: keep all of it as it is and write all of it down** — the artifact ISV docs paragraph, SECURITY.md's, and the record's own `inputs`/`artifacts` lists. Nothing in the first slice narrows it, and the design says so instead of leaving a reader to assume a per-run boundary that does not exist (V4). | **Narrow it now** — a per-run condition or a per-run account for a run's own prefix. It is the right end state, it is not expressible in the compiled-in per-repository grant (`JobBucketPrefixes`, V4), and it would add an IAM plan-and-apply per repository to a slice that currently touches no Terraform at all. It belongs to the design that makes runs multi-repository (D11). | **Yes — keep and document the facts; no Terraform in slice 1.** |
| **D11** | **What project-centric phase 3 and the VM backend do to this blast radius.** | **State the widening and make the narrowing a precondition of whichever lands first** (§8.5): phase 3's project jobs and the VM backend's accounts are not per-repository, so an artifact run on either makes its artifacts, inputs and records readable — and **overwritable** — by every run on the profile, not only by its own repository's. **Recommendation: an artifact-producing run stays on the plain Cloud Run backend until that narrowing exists** (per-run prefix condition, or a per-run account), and the trigger to revisit is the same one D3 names. | **Enable artifact runs on those backends as they are.** Cheapest today, and it turns "runs of one repository can see each other" (`SECURITY.md:62`) into "runs of every repository on the profile can see and replace each other's deliverables" — a blast radius nobody has reviewed, for a feature that does not need those backends yet. | **Yes — an artifact-producing run stays on the plain per-repository Cloud Run backend until a per-run prefix condition or a per-run account exists.** It waits for that narrowing before moving to shared project jobs or the VM backend — which is why D1 is repo-bound first. |

### 13.1 The repository-less end state — recorded, not built here

**D1's answer is "repo-bound first", and the other half of that answer is a
recorded end state, not a rejected idea.** The owner wants a run that needs no
repository at all — the recipe, the render script and the assets living under a
profile rather than in a checkout — and the design keeps it as the destination.
**It is deliberately not a task in this plan**, and it cannot be one yet: it
needs both of the following, and neither exists.

1. **Project-centric phase 3's shared project jobs** (`docs/design/project-centric.md` §4–§5,
   §11 phase 3; V20). An artifact run needs a job to run in, and a run with no
   repository has no per-repository job to bind to. This is the same work
   repository-less runs of *any* kind wait on, and it is not this design's.
2. **A per-run narrowing — the same one D11 makes a precondition.** Phase 3's
   project jobs are not per-repository, so the grant an artifact run writes
   under would be the profile's, not its own repository's: its artifacts,
   inputs and records would be readable **and overwritable** by every run on the
   profile (§8.5). That is the blast radius D11 refuses to enable artifact runs
   on, and it is a per-run prefix condition (`runs/<slug>/<run>/`) or a per-run
   account — neither of which the compiled-in `JobBucketPrefixes` grant can
   express today (`internal/backend/gcp/names.go:374,383-389`).

**And one thing beyond those two, which is a design question and not a
dependency:** where a repository-less run's **render script** comes from. In
slice 1 it is repository text in the assets repository's `fugaro.yaml` (D8), so
`git` is the review, the version and the rollback. With no repository there is
no such tree, and the candidates — the project layer, a bucket object, an
installation-level config — are each a *new exception* to the rule the format
has kept so far (a recipe names no command, and nothing from the bucket is
executed, V7). Choosing one is a decision this design does not take, and it is
the first thing its successor has to answer.

**The order that follows from this, stated once:** project-centric phase 3 →
the per-run narrowing → repository-less artifact runs. Until then an artifact
run is repo-bound, on the plain per-repository Cloud Run job, which is exactly
what D1 and D11 answer.

## 14. What this design deliberately does not do

- **No GPUs** (§5.4, U1).
- **No compute-in-the-cap** (D4's alternative).
- **No durable retention** (D2's alternative).
- **No signed URLs** (D7).
- **No repository-less runs in this slice** (D1's other half): they are the
  recorded **end state**, and §13.1 names what they still need — phase 3, the
  per-run narrowing, and a decision about where a repository-less run's render
  script comes from.
- **No general `outputs:` on implement recipes** (D5's alternative), and no "the run opens a PR for the script change it used" — the latter is a natural third slice, and it needs the artifact run to be worth having first.
- **No local-only artifact mode**: `fugaro exec` with a `file://` bucket gets the mechanism for free (V2), and the design does not add a second local path.
- **No artifact registry, no manifest of many runs, no cross-run index.** One run, one artifact set, one record.
- **No signed manifest, and therefore no tamper evidence** (§8.3): the recorded sha256 proves the bytes came back intact, not who wrote them, because the hash and the artifact live in the same repository-scoped prefix. A launcher-signed manifest (or a per-run key outside the run's prefix) is the mechanism that would change that, and it is its own design.
- **No per-run IAM narrowing** (D10, D11): the grant an artifact run writes under is its repository's, not its own run's, and this slice touches no Terraform. The narrowing is a precondition of the backend that makes runs multi-repository, and it is named there rather than here.
- **No pristine render tree** (D8's alternative): the render runs in the checkout the plan stage had, `render.tree` says so, and the docs carry the rule (an inline `commands.render`) that gets the strong property for free.
