# Artifact runs: implementation plan

**Goal:** a Fugaro run whose deliverable is a file in the runs bucket instead of a pull request — `fugaro run --recipe sizzle --attach shot1.png --attach shot2.png` renders a video in the cloud, checks it with an `ffprobe`-shaped command the repository owns, uploads it to `runs/<slug>/<run>/artifacts/`, records its size, sha256 and content type in `result.json`, and leaves `fugaro artifacts get` as the way to take it back. **This PR (the one that adds this plan and its design doc) is docs-only: no Go, Terraform, schema or `fugaro.yaml` file under any "Files" list below is touched by it.**

**Spec:** [docs/design/artifact-runs.md](../design/artifact-runs.md). Its findings against the code (§1), its verified/unverified table (§12) and its **eleven** decisions for the owner (D1–D11, §13) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable.

**Approval gate: nothing below is executed until the owner says yes.** The plan is a *proposal*: **no task starts on the strength of this document alone** (P12). **The owner answered the design's eleven decisions on 2026-10-15** (design §13, verbatim in its last column; the summary table below maps each P onto the answer it stands on). **Answering a decision is not approving a task**: every decision is still answered by a yes or by a veto that rewrites the tasks it touches, and **Task 1 is D5's answer in code, so it is not unblocked by default either**. Two answers moved the plan's shape and are propagated through the tasks, not left in the table: **D1 is repo-bound first** — slice 1 uses the owner's assets repository for the recipe and the render script, on the **per-repository** Cloud Run job, and the repository-less end state is recorded in the design's §13.1 as a *future* section, not a task here — and **D11 keeps an artifact-producing run on the plain per-repository Cloud Run backend** until a per-run prefix condition or a per-run account exists — **a decision about when artifact runs may be enabled on a shared backend, and a mechanism in no task here**: D11 is "visible in this plan only as text" (below), so §8.9's SECURITY.md text and Task 9 must say it as a decision and never as a refusal this slice performs. **Nothing in this plan assumes a repository-less launch, a shared project job or the VM backend.** Tasks 3–9 assume the recommendations the design argues for (D1 repo-bound, D2 under the run prefix, D3 Cloud Run, D5 `mode: artifact`, D6 attachments in this slice, D8 the command is repository text with an inline script recommended) and are rewritten, not patched, if the owner vetoes one. **D4 (the timeout is the cap), D7 (no signed URLs yet), D9 (attachments and the data policy), D10 (who can read an artifact) and D11 (the phases that widen it) are visible in this plan only as text** — the preview line (Task 8), SECURITY.md's paragraphs and the docs (Task 9), and the attach warning (Task 6) — and none of them adds a mechanism.

**Depends on:** release 0.7.5 on `main` (the recipe gate `recipesV07Since`, `internal/cli/recipes_skew.go:123`, and `fugaro image build`/`image refresh` as they stand). The substrate stays Cloud Run (D3), so nothing here needs [compute-backend.md](../design/compute-backend.md)'s enable work — **its Task 3 (the VM price table) merged as #389, so the "unmerged" note this plan first carried is stale** — and nothing depends on project-centric phase 3 (D1, D11): **the slice is repo-bound (D1's answer), so it needs the recipe and the render script in the repository it runs for, which is the owner's assets repository in the sizzle case**.

**Ordering, stated once and enforced by the tasks.** Task 1 is first and depends on nothing. Task 2 **needs Task 1**: it consumes the three config keys Task 1 adds (`commands.render`, `commands.probe`, `timeouts.render`), read from the runner's in-memory `r.wf`, and it brings its own `runstore` types with it (`RenderRecord`, `OutcomeArtifacts`, `Record.Render`, the `outcome` enum value) rather than waiting for Task 4. Task 4 (the outputs) needs Task 3's `PutFile`. Task 5 (the probe) needs Task 2's render step and Task 4's staging directory. Task 6 (files in) needs Task 3's `GetFile`, Task 1's `workflows.<name>.inputs`, and Task 2's `spec.Mode`; it owns `task.Spec.Inputs`, `FUGARO_INPUTS_DIR` and the download. Task 7 needs Task 4's record and Task 3's `GetFile`. Tasks 8 and 9 depend on the surfaces they document — **Task 8 needs Task 1's `recipe.ModeArtifact` for `StageBound`'s artifact arm; the two tasks that touch `internal/recipe` are Task 1 (`recipe.go` and the schema: `ModeArtifact`, `StepPlan`/`StepRender`, the `outputs` block) and Task 8 (`stages.go`: the ceiling's artifact arm), and they touch different files in it**. **A task that needs a symbol from a later task is a bug in this list, not a licence to reach forward.**

**Checked before this plan was written:** every fact below was read from the code **at `origin/main` (`b9d5c5d`), which is 0.7.5 plus the openrouter senior reviewer (#387) and the compute backend's Task 3 (#389)**, since the branch's base predates them; and the design's §12 table carries the full list with its citations (V1–V28). The load-bearing ones for the edits here: the run prefix and the record names (`internal/runstore/runstore.go:386-387,492,502,547`); `blobx`'s write-all-bytes and `MaxReadBytes = 1<<20` (`internal/blobx/blobx.go:51-54,109-138,152-155,173-197,314`, V23); the recipe's mode and step kinds (`internal/recipe/recipe.go:38-62`), `checkGateSteps`/`checkGateCommands`/`gateCommand`/`verify.Run` and the "a check step only ever names a `commands.*` key" rule (`internal/runner/gate.go:47-62,100-127,132-175`, `internal/verify/verify.go:39-64,176-260`); the runner's mode dispatch and its mode cross-check (`internal/runner/runner.go:399-403`, the recipe/spec cross-check at `:930-935`), the two places the agent's git credentials enter its environment (`:635`, `:1135`) and the secrets site (`:1091`); **the checkpoint gate is `checkpointsOn()` (`internal/runner/checkpoint.go:65`), not the checkout arm**, and it is `runner.go:1684` that starts it inside a stage; the review mode's `skipPR` and `OutcomeReviewed` (`internal/runner/reviewmode.go:469-470,698`); `verify.Settings` and its writer — and that it is a 0644 file `verify.LoadSettings` re-reads, so it is not where a fixed command can live (`internal/verify/verify.go:26,60,85`, `internal/runner/runner.go:1073-1081`, V27) — and the runner's in-memory config `r.cfg, r.wf` set once (`runner.go:962`, V28); `task.Spec` and its strict decoder (`internal/task/task.go:46-89`); the launch order — mint, `CreateTask`, `Launch` (`internal/cli/run.go:819,1146-1172,1392`), where a repeated `--run-id` **adopts a byte-identical stored task and refuses a different one**, after the task is written; `checkImageSince` and the `*Since` constants (`internal/cli/recipes_skew.go:25-150,204-270`); the Cloud Run limits by symbol (`internal/backend/gcp/limits.go`'s `cpuChoices`, `memNeedCPU`, `CheckResources` — 1/2/4/6/8 vCPU, ≤ 32Gi, the ratio rules); the caps and the uncapped compute estimate (`internal/budget/model.go:150-165`, `internal/budget/evaluate.go:190-209`, `internal/runner/cost.go:63`, `internal/cli/budget_preview.go:26-28,489-538`, `docs/design/m9-spec-v3-reconciliation.md:10`); the runner test harness's in-memory bucket (`internal/runner/runner_test.go:53-120`); and the checklist's **number register** — `docs/gcp-live-checklist.md:250-257`, where **48–56 are reserved by the project-centric plan (PR #366) and 57 by the compute backend (PR #375)**, which is why Check 58 is the next free number rather than a gap — plus rows 40–47 (`:317-324`) and Check 43's guard block (`:1437-1458`, "never `belong`" at `:1431`). If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the rule `docs/plans/2026-10-08-upgrade.md` uses).

## Decisions (veto any before execution starts — and none is assumed)

**P12 below is the gate: an explicit yes from the owner, and no task before it.**

**The owner's answers, mapped onto this list (2026-10-15).** The design's D1–D11 were put to the owner and answered; the P-list is this plan's own mechanism, and where a P carries an answer it is named here. "Not put to the owner" means exactly that — it is a mechanism inside an answered decision, and it is still vetoable under P12.

| # | The decision, in one line | Owner's answer 2026-10-15 |
|---|---|---|
| P1 | Two new `blobx` methods (`PutFile`, `GetFile`) carry the bytes | Not put to the owner; the mechanism of D5/D8. **Amended this round**: `PutFile` aborts the write before closing it, `GetFile`'s 403 classification is per caller (non-strict in the runner, strict in the CLI) |
| P2 | The command is repository text read once at bootstrap; the step is not a sandbox | **D8** — yes: read once at bootstrap, never re-read from `workflow.json`, residual risk named |
| P3 | `render` is a new step kind, `plan` an explicit one | Not put to the owner; the shape D5 answers ("`mode: artifact` with `outputs:` legal only there") |
| P4 | The render and the probe run like `verify.Run`, with the repository's environment | **D10** — yes: keep the facts, document them; the render keeps the agent's environment, secrets included |
| P5 | Staged locally, hashed, then uploaded from the staging directory | Not put to the owner; the design's §8.4. **The figures changed this round**: the refusal's arithmetic is `2 × outputs.max_total_bytes + inputs.max_total_bytes` against `resources.memory`, not `2 × max_files × max_bytes` |
| P6 | Caps are defaults with hard ceilings, checked twice | **D9** for the input side (names and hashes recorded, no new gate) and the design's §4 step 2 for the output side. **`max_bytes` is per file everywhere; `max_total_bytes` is the set** |
| P7 | `--attach` uploads between the run-id mint and `CreateTask` | Not put to the owner; design §4 step 1 |
| P8 | `budget preview` gains the compute-ceiling line, labelled "not capped" | **D4** — yes: the timeout is the compute cap, said out loud |
| P9 | One `*Since` constant gates the new keys and the new spec field | Not put to the owner; the existing skew seam |
| P10 | Release placement: one PR, Tasks 1–9, 0.8.0 | Not put to the owner; **D6** fixes the attachment release ("in the same release as their own commit") |
| P11 | The recorded sha256 proves integrity, not authorship | **D10** — yes: keep and document the facts, no Terraform in slice 1 |
| P12 | Nothing here is executed before the owner approves it | **Stands unchanged.** Answering D1–D11 is not approving P1–P13, and no task starts on the strength of the answers alone |
| P13 | A task owns the types and settings it records with | Not put to the owner; this plan's own ordering rule |

**In full:**

- **P1. Two new `blobx` methods carry the bytes, and nothing else in the object layer changes.** `PutFile` (streaming write, sha256 in the same pass) and `GetFile` (streaming read to a file, hash-verified, capped) are added to `internal/blobx`; `Create`/`ReplaceIf`/`Read`/`ReadMax` keep their signatures and their 1 MiB cap for locks, claims, `result.json` and `verify/*.json`. *Veto alternative:* put `[]byte` artifacts through the existing `Put` and raise a limit — rejected because `ReadMax`'s only consumer shape is "the whole object in memory" (`internal/blobx/blobx.go:152-155`) and an artifact is a video; the CLI would then need the artifact's size to fit RAM, and the runner would hold a second copy of a 256 MiB file to hash it. *Second alternative:* do the streaming in `internal/runner` with go-cloud's `blob.NewWriter` directly — rejected because the runner, the CLI and (later) any copy command would each grow their own write path with its own content-type and error-classification rules, which is exactly what `blobx` exists to prevent (`isForbidden`, `blobx.go:200`).
- **P2. The agent writes a *plan*; the command is repository text read once — and the step is not a sandbox (design §3, D8).** `commands.render` and `commands.probe` are repository text from `fugaro.yaml` at the task's ref, loaded into the runner's **in-memory** workflow config at bootstrap, **before any stage runs** (`r.cfg, r.wf` at `internal/runner/runner.go:962`, read once and never re-read), exactly as `commands.build|test|lint` are; the agent's plan file is *data* that command may read. **The claim this plan makes is exactly three things and no more:** the recipe, the task spec and the bucket name no command; the command *string* is fixed before the agent starts, so `render.command_sha256` is meaningful; and **the render is not a boundary against the agent** — same container, same user, same environment. A `commands.render` that names a file (`sh .fugaro/render.sh …`) executes **the working tree's bytes**, which the plan stage could have rewritten, and the docs say so. **The rule the docs and the skill carry: write `commands.render` as the script itself**, an inline `|` block, so nothing it runs comes from a file the agent could touch. *Veto alternative:* a pristine `git archive` tree at the ref for the render — it makes a named file's bytes the commit's, and it does not make the step untouchable (shared container, user and `PATH`), for a second copy of the tree on a memory-backed filesystem. **P2 is not negotiable in this plan; the *claim* it used to make is, and it is now the design's D8.**
- **P3. `render` is a new step kind and `plan` an explicit one; a check step is not reused.** `StepRender` runs last and its failure is the run's failure; `StepPlan` marks the optional agent stage a `mode: artifact` recipe may begin with (`steps: [plan, render]` is the sizzle case, `steps: [render]` alone is the zero-model case). *Veto alternative:* add `render` to `check.command`'s enum and let a check step be last in artifact mode — one concept fewer, and it makes a step kind whose meaning depends on its mode, which is the conditional the recipe format has avoided so far, and it would have to re-cut the ordering rule (`schemas/recipe.schema.json`'s `prefixItems`) and the failure path (`gateResult`, which feeds the implement prompt).
- **P4. The render and the probe run as one command each, in the checkout, as the agent's user, through `procgroup`, with the repository's own environment** (`r.env`), exactly like `verify.Run` (`internal/verify/verify.go:242-246`) — no shell wrapper of the runner's own, no new executor. **"Exactly like `verify.Run`" means the process shape and nothing else:** unlike `fugaro verify`, the render and the probe do **not** read their commands through `verify.LoadSettings` (`:85`) — they take the strings from the runner's in-memory `r.wf`, because that file is readable *and writable* by the plan stage (0644, same user, same container) and a command loaded from it could be swapped after the reviewer read it (Review Focus 1). The probe is one invocation for the whole set, with `FUGARO_OUT_DIR` and **`FUGARO_ARTIFACTS_FILE`** (the path of the file the runner writes the names into, not a newline-joined value) in the environment. *Veto alternative:* one probe process per artifact with the path interpolated into a command line — rejected in the design (§3): it puts a runner-derived name inside a shell command.
- **P5. Artifacts are staged locally, then hashed, then uploaded from the staging directory, and the object key is always `runs/<slug>/<run>/artifacts/<name>`.** The staging directory is `<StateDir>/artifacts/`, beside `verify/` (which is uploaded the same way, `internal/runner/runner.go:2192`), so the sanitiser has exactly one output path, the probe sees the same bytes that will be uploaded, and a name that escapes cannot reach a key. **On Cloud Run that directory is memory** (§5.2, U3: the container's filesystem is an in-memory filesystem with no volumes), so the caps are a *memory* budget as much as a disk one — **and because staging is a copy the peak is two sets, the render's outputs where it wrote them and the staged copies beside them, so the figure the refusal is built on is `2 × outputs.max_total_bytes + inputs.max_total_bytes` and not one times it** (Global Constraints, Review Focus 12, design §5.2/§8.4) — which is why the defaults are the boring 8 artifacts × 256 MiB each (2 GiB of outputs, doubled to 4 GiB) plus 1 GiB of inputs: 5 GiB under a 32 GiB ceiling, and why the docs tell an owner who raises them to raise `resources.memory` past that. *Veto alternative:* upload the files in place from the checkout and record the paths — rejected: the checkout is agent-writable, so between hashing and uploading the bytes could change, and the name would then be a path into the repository rather than into a staging root.
- **P6. The caps are defaults with hard ceilings, and they are checked twice.** `outputs.max_files` defaults to 8 and may not exceed 32; `outputs.max_bytes` (per artifact) defaults to 256 MiB and may not exceed 2 GiB; `outputs.max_total_bytes` (the whole set) defaults to 2 GiB; `inputs.max_files` defaults to 16/ceiling 64, `inputs.max_bytes` (per file) to 256 MiB/ceiling 1 GiB and `inputs.max_total_bytes` (the set) to 1 GiB. **`max_bytes` means one artifact and `max_total_bytes` means the set, in every block and in every sentence — a cap with two meanings is a cap nobody can check**, and the memory refusal is built on the total (P5). The launcher checks the attachments before the first byte goes up; the runner checks the task spec's claim *and* the files it actually found before uploading. A cap is a refusal with a named reason, never a truncation. *Veto alternative:* no caps on the first slice — rejected: bucket-iam §10's L6 ("fill `runs/` with junk") is already a known limit, and a glob that matches a directory of frames is the cheapest way to hit it.
- **P7. `--attach` uploads between the run-id mint and `CreateTask` — and only after the id is known to be new.** The launcher mints the id (`internal/cli/run.go:819`), **refuses a `--run-id` whose task already exists — adoption included — before writing a single byte** (a repeated `--run-id` is not a new run: `createTask`, `internal/cli/run.go:1146-1172`, adopts a byte-identical stored task and refuses a different one, and it runs *after* an upload would have, which is exactly how an attach could overwrite a finished run's `inputs/`; with `--attach` **both** outcomes are refused, because an existing task already has its `inputs` objects — design §4 step 1), then uploads and hashes the files to `runs/<slug>/<run>/inputs/<name>` with `blobx.PutFile`'s create-only condition (`ifNotExist`, `internal/blobx/blobx.go:107-127`, honoured by gocloud's `file://` driver too — `fileblob.go:845-850` — but still only race hardening, since the launcher-side refusal is what is deterministic), writes the list into the task spec (`task.Spec.Inputs`), and only then writes `task.json` and launches. *Veto alternative:* upload after the launch — rejected: the run would have to poll for names it does not know, and a half-uploaded set is a race with no good resolution; the launcher's own `objectUser` under `runs/` is enough for the upload (`docs/design/bucket-iam.md:126-128`), so nothing is gained by waiting.
- **P8. `budget preview` gains one line and one line only: the compute ceiling, labelled "not capped".** No cap is applied to compute (design §6, D4), and the line prices the workflow's `resources` × **`timeouts.total`** with **`backend.Prices.ComputeUSD`** (`internal/backend/backend.go:292-296`) — exactly what the runner's own estimate calls (`internal/runner/cost.go:63`), and **not** `Prices.Quantities`: that method exists for a table that bills a machine type (`:285-289`), and the launcher's table cannot, because `localcfg.PriceOverride` sets only the two per-second fields and leaves `VCPU`/`MemoryGiB` zero (`internal/localcfg/localcfg.go:868-874`) — at the prices the launcher can already read (`localcfg.ComputePrices`, `internal/localcfg/localcfg.go:73-75`, over `FUGARO_COMPUTE_PRICES`, `internal/infra/spec.go:44-46`). **The basis is `timeouts.total` and not `timeouts.render`** — the line is the worst case a run can *bill*, and a render that overruns its own step still burns the run's clock until the deadline (Task 8 is the implementing step, and design §6 carries the same correction). **The model ceiling above it must not move, and Task 8 is where that is enforced**: `recipe.StageBound` would count a `mode: artifact` recipe as an implement recipe's `1 + 2*rounds` model stages, where a run of it starts one — so Task 8 gives `StageBound` an artifact arm. *Veto alternative:* leave the preview alone until compute is inside the cap — rejected: the preview is where an owner decides whether to launch, and for an artifact run it currently under-reports by the whole render.
- **P9. The new recipe keys are gated at launch by one `*Since` constant (`artifactsSince`), and the task spec's new field by the same one.** A `mode: artifact` recipe needs a runner that knows it, and `task.Spec` refuses unknown fields (`internal/task/task.go:46-89`), so `fugaro run --attach` must refuse against a job image whose runner predates the feature, through the existing `checkImageSince` seam (`internal/cli/recipes_skew.go:204-270`) with a message naming `fugaro image refresh`. *Veto alternative:* let the runner refuse after the launch — rejected: that is the skew the seam exists to catch, and it costs a launched run that fails on a field the launcher could have checked.
- **P10. Release placement: one PR, Tasks 1–9, released as 0.8.0** (design §10). **Only Task 1 may be called patch-safe, and only because it carries the runner's own refusal**: a `mode: artifact` recipe against a runner that has the format and not the mode would otherwise take the **implement** path (`runner.go:399-403` dispatches on the spec, and the mode cross-check at `:930-935` has no artifact arm), so Task 1 ships the format *and* a fail-closed refusal that ends such a run at bootstrap. Tasks 8 (display only) and 9 (docs) are safe alone after it; Task 2 **needs Task 1** and is not safe alone; Tasks 3–7 are the feature and belong in the point release. Task 10 is the owner-run live check and is **not** part of the PR; Task 11 is the full suite. No Terraform, no IAM, no bucket lifecycle and no `.github/workflows/` file is touched by any task in this plan.
- **P11. The recorded sha256 proves integrity, not authorship (design §8.3, D10).** Every doc, the report and the command's own help say **"the bytes you got are the bytes the run uploaded"** and never "tamper evidence": the hash, `result.json` and the artifact live in the same repository-scoped prefix, so anything that can rewrite one can rewrite the others consistently — a sibling run of the same repository, a launcher, an operator. A signed manifest is its own design and is **not** in this slice (design §14).
- **P12. Nothing here is executed before the owner approves it.** This plan is a proposal, not a work order: Tasks 1–9 wait for an explicit yes, and a veto of any D or P rewrites the tasks it touches rather than being worked around. The plan's own "Depends on", "Ordering" and "File Structure" sections are the contract for *what* the tasks are; this line is the contract for *when*.
- **P13. A task owns the types and settings it records with.** The three config keys (`commands.render`, `commands.probe`, `timeouts.render` — and **not** a `verify.Settings` field, which would put the command in a 0644 file the agent can rewrite) go to Task 1, the `render` record's `runstore` types and the `outcome` value to Task 2 (which writes them), `ArtifactFile` and `Record.Artifacts` to Task 4, `InputFile` and `Record.Inputs` to Task 6. A task never calls a symbol a later task introduces.

## Global Constraints

- **Nothing the runner executes is new text, and the claim stops exactly there.** Every command a run runs comes from the `fugaro.yaml` the run read at its ref: for `build|test|lint`, through `verify.Settings` written at bootstrap (`internal/runner/runner.go:1073-1081`); for `render` and `probe`, from the runner's **in-memory** `r.wf` (`:962`) and never through `verify.LoadSettings`, because that file is 0644 and writable by the plan stage (design §3). A recipe, a task spec and a bucket object still name no command. **The configuration is read once, before any stage runs, so the command *string* is fixed before the agent starts — and that is the whole of the property.** The render is not a boundary against the agent (same container, same user, same environment, P2/D8), and no doc, comment, report or test in this plan may say otherwise.
- **An artifact run never pushes and never opens a pull request.** `r.artifact` joins `r.review` at the sites that decide it: the two places the agent's git credentials enter its environment (`runner.go:635`, `:1135`), **`checkpointsOn()` (`internal/runner/checkpoint.go:65`) — the real gate; `runner.go:966`'s base fetch is a read and stays** — and finalize's PR path (`r.skipPR`, `internal/runner/gate.go:305-313`). **It is the checkout arm that stays the implement one**: an artifact run checks the task's ref out on a local branch exactly as an implement run does (`runner.go:899-921`), because there is no pull request whose configuration could be untrusted, and `r.rec.Branch` is the branch lock's key rather than a ref anything pushes (review mode's own wording, `runner.go:878-887`).
- **An artifact run keeps the workflow's secrets — deliberately.** `!r.review` at `runner.go:1091-1099` is left exactly as it is: the agent stage and the render step get the workflow's secret environment, because an artifact run stands where an implement run stands (its own ref, not an untrusted pull request's head). **No task narrows it, and Task 9's docs and SECURITY.md say it out loud** (design §8.6, D10).
- **The staging set must fit the container's memory, and that is a *config* rule, not a hope.** On Cloud Run the filesystem is memory (§5.2, U3), so `2 × outputs.max_total_bytes + inputs.max_total_bytes` — **the staging pair, because the runner copies the render's outputs into `<StateDir>/artifacts/` and both sets live on the same memory-backed filesystem until the uploads finish, plus the input set the render has downloaded there** — is refused **through the recipe/workflow pairing list, not the config-limits one**: `runner.RecipeGateProblems` (`internal/runner/gate.go:121`), which Task 1 extends to take the whole `config.Workflow` — because the arithmetic needs `Resources.Memory`, and `computeProblems` (`internal/cli/compute.go:14`) cannot see the recipe's caps at all. The same list reaches three places: `fugaro validate` through `gateCheckProblems` (`internal/cli/validate_recipes.go:112-126`, the loop that already reports the render/probe refusals), the launch through `finishLaunch`'s pre-write block (after the image gates and `checkMaxParallel` at `internal/cli/run.go:430`, around `budgetPrecheck` at `:434-438`, **before `createTask` writes anything** at `:440`), and the runner's bootstrap through `checkGateCommands` (`internal/runner/gate.go:93`) for the launches whose recipe the launcher could not resolve. A refusal with the three numbers in it (the set, the doubled peak and the limit), added in Task 1 beside the other `outputs` refusals, and the reason an owner who raises one must raise the other. **The render's own temp files are still outside this arithmetic** and are what Check 58's step 7 observes.
- **No live cloud or network call in any automated test.** The runner tests use the harness's in-memory bucket (`internal/runner/runner_test.go:53-120`); `blobx` tests use `file://` buckets, which `blobx.Open` supports (`internal/cli/exec.go:59`); the CLI tests use the existing fakes. The one task that would touch a real cloud is Task 10, and it is the owner's, run by hand.
- **A stored record stays readable by every version.** `result.json`'s new keys (`render`, `artifacts`, `inputs`) and `task.json`'s (`inputs`) are **optional**, none is added to `required`, and `schemas/result.schema.json`'s `additionalProperties: false` is satisfied by adding the properties. A reader that has not heard of them ignores them; a writer that predates them simply omits them. `schemas/schemas_test.go` is the only validator, and it grows with `runstore.Record` (`TestResultSchemaAcceptsRunnerRecords`) — a run record written before this change still validates, and that is a test, not a hope.
- **Docs must match code.** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout, and a proposed command or flag is never written into a lint-scanned doc as if it existed: `TestBaseImageDocCommandFlagsAreReal` (`docs_base_test.go:173`) resolves every `--flag` near a `trackedDocCommands` mention against the real cobra flag set, and the skill lint (`plugin/skills_lint_test.go`) resolves every `fugaro …` mention in `plugin/skills/` against the tree. Task 9's docs either land after the commands they name, or describe them in a form the lint does not resolve.
- **`gofmt -l .` clean**, every test run in the foreground with `-count=1`, one commit per task, and the repository's `Co-Authored-By` line.
- **No new dependency.** The render, the probe, the hashing and the upload use the standard library, `procgroup` and `blobx`; ffmpeg is the *repository's* tool, installed through `image.apt` (Task 9's docs and Task 10's check), never a Go dependency of this repository.

## Review Focus (mutation-proof notes)

1. **The command the runner runs is not one the agent wrote — and nothing more than that is claimed.** Pinned by asserting (a) the argv the runner ran is the `commands.render` string the runner holds in memory (never one read back from `<StateDir>/workflow.json`), which the *recipe* cannot influence: a fixture recipe naming a `render:` step over a workflow with `commands.render: ""` is refused at launch, and the run fails closed rather than running an empty `sh -c` (the shape `verify.Run` already has for an empty `commands.lint`, `internal/verify/verify.go:221-229`); (b) `render.command_sha256` is the hash of **the string the runner ran** — the unredacted `commands.render` text — so a reader holding the repository at the recorded ref can recompute it and confirm the command (design §3, D8); the recorded `command` is the same string *redacted*, so the two disagree exactly when redaction fires, and a test pins that they do; (c) **`<StateDir>/workflow.json` is rewritten by the plan stage and the executed and recorded command does not change** — the runner reads the string from its in-memory config, never through `verify.LoadSettings` (`internal/verify/verify.go:85`, which re-reads a 0644 file this same user can write), because otherwise the hash would attest to a string no reviewer read (design §3); and (d) **the agent's own file is explicitly not asserted against**: the test writes a `render.sh` into the checkout and says in a comment that the runner does not promise which copy runs, because it does not (P2/D8). Task 2.
2. **An artifact run pushes something.** Pinned three ways: the harness's fake provider records zero pushes, `rec.Outcome` is `artifacts` and never `ready`/`draft`, and the agent's environment contains no git credential — asserted by the same shape `TestReviewModeAgentHasNoGitToken` uses (`internal/runner/reviewmode_test.go:969`, and its `envValue` helper at `internal/runner/runner_test.go:156`). Task 2.
3. **A file escapes the run's prefix.** Pinned by a table test over the sanitiser: `../../etc/passwd`, `/etc/passwd`, `a/../../b`, `out/./x` normalising to a duplicate, a name with a NUL or a newline, an over-long name, and a symlink pointing outside the outputs root — each refused with a named reason, and the accepted ones all landing under `runs/acme-app/<run>/artifacts/` in the harness's bucket (assert the bucket's key set, not the returned slice). Task 4.
4. **A cap is silently exceeded.** Pinned by **eight** tests, and **all three caps on both sides of the feature** — because the caps are not the same kind of number: `max_files` counts entries, `max_bytes` is **one artifact**, and `max_total_bytes` is **the set**, so a set whose every member is comfortably under `max_bytes` can still be over the total, and a test that only ever sets one oversized file proves nothing about the cap an owner actually hits. On the artifacts side: a glob matching more files than `max_files`; one file larger than `max_bytes` (the per-artifact arm, `TestOutputsRefusesAnOversizedArtifact`); and a set whose **total** is over `max_total_bytes` while every file is under `max_bytes` (`TestOutputsRefusesASetOverTheTotal`). On the inputs side: more attachments than `inputs.max_files` (`TestAttachRefusesMoreFilesThanTheCap`); one attachment over `inputs.max_bytes` (`TestAttachRefusesAnOversizedFile`); and a launcher set over `inputs.max_total_bytes` whose members are all under `inputs.max_bytes` (`TestAttachRefusesASetOverTheTotal`), with the runner's own arm of the same three (`TestInputsOverTheCapFailTheRun`, `TestInputsOverTheFileCapFailTheRun`, `TestInputsOverTheTotalCapFailTheRun`) since the spec's claim is launcher-written text. Each must refuse with the cap, the total and the count named, and upload/download **nothing** — assert the bucket has no `artifacts/` key and `inputs/` is empty, so a partial set fails the test. Tasks 4 and 6.
5. **The hash is of something else.** Pinned by asserting the recorded `sha256` equals `sha256sum` of the bytes actually in the bucket (read back through the harness) — and by a test where the staged file is replaced between the hash and the upload (the seam exists for the test), which must fail the run rather than record a hash for bytes that are not there. Task 4.
6. **The probe's verdict is ignored, or its failure is a pass.** Pinned by two runs: one where `commands.probe` exits 1 and the run must end `failed` with `outcome: none` **and no artifacts uploaded** (fail closed, even though the files exist), and one where `commands.probe` is absent and the run succeeds with a `render` record that says no probe ran — so "there is no probe" is visible in the record rather than inferred from a missing field. Task 5.
7. **A render that outlives its deadline.** Pinned by a test with a small `timeouts.render` and a `sleep`-shaped render command: the step ends as a timeout, the run is `failed`/`timed_out`, the record quotes it, and the run's own `finalizeAt` still holds (no stage may eat the reserve). Task 2.
8. **The record breaks an old reader, or an old record breaks the new one.** Pinned by `TestResultSchemaAcceptsRunnerRecords` over a record with the new fields **and** by a test that unmarshals a fixture `result.json` written before this change (no `artifacts`, no `render`, no `inputs`) into `runstore.Record` and asserts every new field is nil/empty and nothing else changed — the compatibility claim in Global Constraints, as a test. Task 4.
9. **`fugaro artifacts get` writes something it should not.** Pinned by tests: a mismatching sha256 writes **nothing** (assert the output directory is empty), an over-cap `Content-Length` refuses before the body is read, a name from the record that tries to escape is refused, `--force` is required to overwrite, and the command never opens what it wrote (no exec, no unpack — assert by making the artifact a shell script whose execution would create a second file, and asserting that file does not exist). Task 7.
10. **`mode: artifact` silently runs as an implement run.** Pinned three ways. **Before Task 2 exists**: a `mode: artifact` recipe launched against this runner ends `failed` at bootstrap with the refusal naming the mode — asserted by a test that would otherwise see a push (the harness's fake provider records pushes) and a `ready` outcome. **And after**: the recipe/spec cross-check at `runner.go:930-935` gets its artifact pair, asserted in both directions — a spec whose mode disagrees with the resolved recipe is refused, and an artifact recipe launched by a spec that does not claim it is refused too. **And the empty `Spec.Mode`**: a launch where the CLI left the recipe to the runner carries no mode at all, so the resolved recipe's mode must be the one that decides — asserted by running a `mode: artifact` recipe against a spec with no `Mode` and seeing an artifact run, never an implement one — **while a spec that carries `Inputs` under a non-artifact resolved mode is refused before the first step** (design §8.2), so an attachment can never ride into a run that opens a pull request. Task 1 (the refusal) and Task 2 (the cross-check and its input arm).
11. **An attach overwrites a finished run's inputs — or adopts a run whose inputs it did not upload.** Pinned twice. **First**, a test that seeds a run prefix with a `task.json` and an `inputs/` object, launches with `--attach --run-id <that run>`, and asserts **the object's bytes are unchanged**, no new object was written and the launch is refused — run for **both** a differing stored task and a byte-identical one, because with `--attach` adoption is refused too (design §4 step 1: the objects already exist, so the create-only upload could only fail, and the same-bytes case would need a read-back this slice does not do). **Second**, that a plain launch with the same `--run-id` still adopts the byte-identical task, so the rule being changed is the `--attach` one and not `createTask`'s. Asserted from the bucket, not from the call order. Task 6.
12. **The staging set is bigger than the container's memory.** Pinned in **`internal/cli`**, not `internal/config`, because the rule pairs a recipe with a workflow and `internal/config` can reach neither `gateCheckProblems` nor `finishLaunch`: an `outputs` block whose **`2 × max_total_bytes + inputs.max_total_bytes`** exceeds the workflow's `resources.memory` is refused by `fugaro validate` (asserted through `gateCheckProblems`) **and** by the launch (asserted through `finishLaunch`, the same `runner.RecipeGateProblems` list called from beside the recipe-image gate). The sites are named in design §5.2 and the numbers are in the message — the invariant that keeps the common case out of an OOM kill, which the design states is a killed container with no record rather than a clean failure (§5.2, U3). Task 1. **And the direction this test cannot pin** — a render's own temp files against the memory limit — is Check 58's step 7, observed rather than asserted.
13. **The upload's memory.** Pinned by a test that streams an artifact larger than `blobx.MaxReadBytes` (say 8 MiB) through `PutFile` from a `file://` bucket and asserts the object's bytes and hash, plus a `runtime.ReadMemStats`-shaped guard is **not** the assertion (allocation is not a contract) — the assertion is that `PutFile` never calls `ReadAll`: `grep`-proof by construction because `PutFile` takes a path and `io.Copy`s it. Task 3.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `schemas/recipe.schema.json`, `internal/recipe/recipe.go`, `internal/recipe/recipe_test.go` | `ModeArtifact`, `StepPlan`/`StepRender`, the `outputs` block, the ordering and refusal rules, `UsesV08` (the new keys, for the launch gate) | 1 |
| `schemas/fugaro.schema.json`, `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/config_test.go`, `internal/cli/validate_recipes.go`, `internal/runner/runner.go` (the fail-closed refusal only — `internal/verify/verify.go` is **not** edited) | `commands.render`, `commands.probe`, `timeouts.render`, `workflows.<name>.inputs`, their defaults and their `validate` refusals, **the `outputs`-fits-in-memory refusal**, and the refusal that makes Task 1 safe alone (P10) — the three keys, **never a `verify.Settings` field** | 1 |
| `internal/runner/artifact.go` (new), `internal/runner/artifact_test.go` (new), `internal/runner/runner.go` (the dispatch, `r.artifact`, the two git-env sites, the mode cross-check), `internal/runner/checkpoint.go` (`checkpointsOn`), `internal/runner/report.go` + its test (the `OutcomeArtifacts` arm and the Artifacts section), `internal/cli/cancel.go` (`finalized`'s `NoPR`), **`internal/watch/plain.go`, `internal/watch/render.go`, `internal/watch/finished.go` (the enum comment) + `internal/watch/watch_test.go`** | `r.artifact`, the plan stage, the render step's deadline and failure path, and **every site that switches on the new outcome value** (`report.go:72-79`, `cancel.go:468`, and the two display switches `watch/plain.go:99` and `watch/render.go:653` — **four, not five**: `halt.go:177` reads `OutcomeNone`, which is what a halted artifact run already ends as) | 2 |
| `internal/runstore/artifact.go` (new: `RenderRecord`, `OutcomeArtifacts`), `internal/runstore/runstore.go` (`Record.Render`), `schemas/result.schema.json` (the `render` property, `outcome`'s new value), `schemas/schemas_test.go`, `internal/task/task.go` + `schemas/task.schema.json` (`Spec.Mode`) | the types Task 2 writes with, owned by Task 2 (P13) | 2 |
| `internal/blobx/blobx.go`, `internal/blobx/blobx_test.go` | `PutFile`, `GetFile` — streaming, hashing, capped, 403-classified | 3 |
| `internal/runstore/artifact.go` (`ArtifactFile`), `internal/runstore/runstore.go` (`Record.Artifacts`), `schemas/result.schema.json` (the `artifacts` property), `schemas/schemas_test.go` | `ArtifactFile`, the record's `artifacts` field, the schema property, the round-trip tests (`RenderRecord` and `OutcomeArtifacts` are Task 2's) | 4 |
| `internal/runner/artifact.go` (the selection, sanitiser, caps, upload), `internal/runner/artifact_test.go` | `artifactSet`, `sanitiseName`, `uploadArtifacts` | 4 |
| `internal/runner/artifact.go` (the probe), `internal/runner/artifact_test.go` | the probe's one invocation, `FUGARO_OUT_DIR`/`FUGARO_ARTIFACTS_FILE`, its record (the config keys are Task 1's; the probe reads `r.wf`, not `verify.LoadSettings`) | 5 |
| `internal/task/task.go` (its `Inputs`; `Mode` is Task 2's), `schemas/task.schema.json`, `internal/task/task_test.go`, `internal/cli/run.go` (the flags, the run-id freshness check, the upload), `internal/cli/run_attach.go` (new), `internal/cli/run_attach_test.go` (new) | `task.Spec.Inputs`, `--attach`, the upload **after** the freshness check, the caps, the image gate | 6 |
| `internal/runstore/artifact.go` (`InputFile`), `internal/runstore/runstore.go` (`Record.Inputs`), `schemas/result.schema.json` (the `inputs` property) | the input record Task 6 writes with (P13) | 6 |
| `internal/runner/artifact.go` (the download), `internal/runner/artifact_test.go` | the inputs' download, the hash check, the `inputs/` write and the git exclude | 6 |
| `internal/cli/artifacts.go` (new), `internal/cli/artifacts_test.go` (new), `internal/cli/runs.go`, `internal/cli/diagnose.go`, **`internal/runview/runview.go`** (the view's own package — there is no `internal/cli/runview.go`, and the design cites the real path at §5.2 and §9), the corresponding tests | `fugaro artifacts ls\|get`, the display in `runs ls` and `diagnose` | 7 |
| `internal/cli/budget_preview.go`, `internal/cli/budget_preview_test.go` | the compute-ceiling line, labelled "not capped" | 8 |
| `docs/artifact-runs.md` (new), `docs/recipes.md`, `docs/base-image.md`, `docs/gcp-live-checklist.md` (Check 58's row and section), `SECURITY.md`, `plugin/skills/routing/SKILL.md` (its "Choosing a recipe" section, `:47`), `docs/release.md` | the user docs, the ffmpeg recipe, the SECURITY.md paragraph of design §8.9 verbatim, Check 58 as written | 9 |

No task edits `internal/backend/**`, `deploy/terraform/**`, `internal/image/render.go` or `.github/workflows/**`: the substrate is unchanged (D3 — Cloud Run, no GPU, no Batch), ffmpeg reaches the image through `image.apt`, which already exists (`images/derived/Dockerfile.tmpl:26-33`), and no permission, bucket, lifecycle rule or prefix is added anywhere (D2, D7).

## PR groups

**One PR, Tasks 1–9, released together as 0.8.0** (P10). Task 10 is the owner-run live check and is **not** in it; Task 11 is the repository's full suite plus the `fugaro verify build`/`verify test` run that closes every task.

The patch-safe subset the design names (§10) is **Tasks 1, 8 and 9 — each harmless alone; Task 2 rides with Task 1 and is not safe alone (P10)** — but the plan keeps them all in the one release: the feature is only usable with 3–7 beside them.

---

### Task 1: The format — `mode: artifact`, `plan`/`render`, `outputs`, and the two commands

**Files:**
- Edit: `schemas/recipe.schema.json`, `internal/recipe/recipe.go`
- Test: `internal/recipe/recipe_test.go`
- Edit: `schemas/fugaro.schema.json`, `internal/config/config.go`, `internal/config/defaults.go`
- Test: `internal/config/config_test.go`, `internal/cli/validate_test.go`
- Edit: `internal/cli/validate_recipes.go:112-126` (`gateCheckProblems`; the `render`/`probe` pairing refusals go beside `RecipeGateProblems`)
- Edit: `internal/runner/gate.go` — **`RecipeGateProblems` (`:121`) takes the whole `config.Workflow` instead of `cmds config.Commands`**, so the memory rule (`2 × outputs.max_total_bytes + inputs.max_total_bytes` against `wf.Resources.Memory`) can be reported from the one list its two callers share. **Not `computeProblems`** (`internal/cli/compute.go:14`): it takes `*config.Config` and cannot see the recipe's `outputs` block at all
- Edit: `internal/cli/validate_recipes.go:112-126` (`gateCheckProblems`, the recipe↔workflow loop that already reports the render/probe refusals, now passing the whole workflow) **and `internal/cli/run.go` (the same list called in `finishLaunch` beside the recipe-image gate, `:400-425`, before `createTask` at `:440`)** — so an over-memory artifact workflow is refused by `fugaro validate` *and* by the launch, and the runner's bootstrap `checkGateCommands` is the third arm for a launch whose recipe the launcher could not resolve
- Test: `internal/cli/validate_recipes_test.go`, `internal/cli/run_test.go` (the launch twin) — **not `internal/config/config_test.go`**: `internal/config` can reach neither `gateCheckProblems` nor `finishLaunch`
- Edit: `internal/runner/runner.go` (the one refusal arm at `:930-935` and its test; **no `verify.Settings` change** — `internal/verify/verify.go` is not touched by this task or any other)

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

// MaxOutputFiles, MaxOutputBytes and MaxOutputTotalBytes are the ceilings
// Outputs.MaxFiles, Outputs.MaxBytes (per artifact) and Outputs.MaxTotalBytes
// (the whole set) may not exceed (P6).
const (
    MaxOutputFiles      = 32
    MaxOutputBytes      = int64(2) << 30
    MaxOutputTotalBytes = int64(16) << 30
)

// Outputs is a mode: artifact recipe's declaration of what it delivers,
// from the recipe's top-level outputs: block.
type Outputs struct {
    Globs    []string // relative to the checkout root; no absolute path, no ".."
    MaxFiles int      // 0 means DefaultOutputFiles (8)
    MaxBytes int64    // 0 means DefaultOutputBytes (256 MiB); PER ARTIFACT
    MaxTotalBytes int64 // 0 means DefaultOutputTotalBytes (2 GiB); THE WHOLE SET
    Probe    string   // a commands.* key: "probe" or "none"
    // HasKeys records that the block was present at all, so UsesV08 can
    // decide from presence rather than from a parsed value, the rule
    // newKeys already follows (recipe.go:100-115).
    HasKeys bool
}

// Recipe gains Outputs *Outputs, non-nil exactly when Mode is ModeArtifact.
```

`Parse` refuses, with the recipe's own message shape: `outputs:` on a `mode: implement` or `mode: review` recipe; a `mode: artifact` recipe without `outputs:`; a `mode: artifact` recipe with `steps` that is not exactly one `render` or `plan` followed by one `render`; `plan` or `render` in the other two modes; `check`/`first_line`/`review` in artifact mode (`check` included: an artifact recipe has no implement to gate); an absolute glob, a glob with a `..` element, an empty glob list, a `max_files` above the ceiling or below 1, a `max_bytes` or `max_total_bytes` above its ceiling or below 1, and a `max_total_bytes` below `max_bytes` together with a `max_files` of 1 (a set cap that one legal artifact already breaks is a config nobody can satisfy); `probe` naming anything but a `commands.*` key this version knows; and `roles`/`review.bounce`.

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
    MaxFiles      int   `yaml:"max_files"`       // 0 means 16; ceiling 64
    MaxBytes      int64 `yaml:"max_bytes"`       // 0 means 256 MiB; PER FILE; ceiling 1 GiB
    MaxTotalBytes int64 `yaml:"max_total_bytes"` // 0 means 1 GiB; THE WHOLE SET
}
```

Defaults in `internal/config/defaults.go`: `timeouts.render = min(verify, total − finalize_reserve)`, computed where the other three timeouts are (`defaults.go:77-90`, which already does exactly this min-shape for `stage` and `verify`); `inputs.max_files = 16`, `inputs.max_bytes = 256 << 20` (per file), `inputs.max_total_bytes = 1 << 30` (the set).

`gateCheckProblems` (`internal/cli/validate_recipes.go:112-126`) — the function `fugaro validate` uses to report a recipe's gate refusals against every workflow — gains two more messages beside `RecipeGateProblems`'s (`internal/runner/gate.go:118-122`), whose shared list is `gateCommandProblems` (`internal/runner/gate.go:100-116`): an artifact recipe over a workflow with a blank `commands.render` is refused at launch **and** reported by validate, and `outputs.probe: probe` over a blank `commands.probe` likewise.

**No `verify.Settings` field is added, and that is the point.** `internal/verify/verify.go` and the `verify.WriteSettings` call at `internal/runner/runner.go:1075-1081` are **not edited**: `Settings` is what the user-invocable `fugaro verify` reads, through `verify.LoadSettings` (`verify.go:85`) from a **0644** file (`:60`) in the state directory, and a runner that took its render command from there could be made to run a string the reviewer never read (design §3, Review Focus 1). The render and the probe therefore read the runner's own **in-memory** workflow config — `r.wf.Commands.Render`, `r.wf.Commands.Probe`, `r.wf.Timeouts.Render` — which `bootstrap` sets once (`runner.go:962`) and nothing re-reads. **Two of those three are typed by the first block above and only by it** — `Commands.Render`/`Commands.Probe` are `string`s with `yaml:` tags beside `build|test|lint|fix` (`internal/config/config.go:359-370`), and `Timeouts.Render` is the repository's own **`config.Duration`**, not a `time.Duration` (`internal/config/config.go:424-437`, whose `UnmarshalYAML` is what parses `render: 30m`). There is deliberately no second declaration of these two structs in this plan: an implementer who copied a `json:`-tagged `time.Duration` version of them would ship a key that never parses and a tag the YAML loader ignores. Task 1's job here is only to make those keys exist.

**And the refusal that makes Task 1 safe alone (P10).** The runner's recipe/spec mode cross-check (`internal/runner/runner.go:930-935`) gains one arm **here** and Task 2 replaces it with the real mode:

```go
case rcp.Mode == recipe.ModeArtifact:
    return fmt.Errorf("recipe %s is mode: artifact, which this runner does not implement", rrec.Name)
```

Without it a `mode: artifact` recipe falls through to the implement path (`runner.go:399-403` dispatches on a flag the recipe never sets, and the checkout arm at `:899-921` is the implement one), so a patch release carrying the format alone would open a pull request for a recipe that asked for a file. **The refusal is three lines and a test; it is what makes "Task 1 is patch-safe" true rather than hopeful.**

**And one config refusal the memory bound needs** (Global Constraints): **`2 × outputs.max_total_bytes + inputs.max_total_bytes`** must not exceed the workflow's `resources.memory`, refused by `fugaro validate` and by the launch gate with the set, the peak and the limit in the message — the staging directory is memory on Cloud Run (§5.2, U3), **the staged copies live beside the render's originals**, and the downloaded inputs sit there too, so a set that cannot fit *twice over* is a killed container rather than a clean failure.

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
func TestOutputTotalCapHasACeiling(t *testing.T)          // max_total_bytes above its ceiling, and a max_total_bytes one legal artifact already trips
func TestInputTotalCapHasACeiling(t *testing.T)           // the same two on workflows.<name>.inputs
```
```go
// internal/cli/validate_recipes_test.go and internal/cli/run_test.go — the
// memory rule pairs a recipe with a workflow, which internal/config cannot do
func TestOutputCapsMustFitTheWorkflowsMemory(t *testing.T) // Review Focus 12: the doubled staging peak, through gateCheckProblems (validate)
func TestAnArtifactLaunchRefusesACapSetThatCannotFitMemory(t *testing.T) // the same list in finishLaunch's pre-write block — zero createTask calls, nothing written
```
```go
// internal/runner/modeartifact_test.go (Task 2 folds this file into artifact_test.go)
func TestAModeArtifactRecipeIsRefusedByARunnerThatDoesNotImplementIt(t *testing.T) // Review Focus 10: failed at bootstrap, zero pushes, outcome none
```
Run: `go test ./internal/recipe ./internal/config -run 'TestArtifact|TestOutputs|TestUsesV08|TestRenderTimeout|TestInputCaps|TestOutputCaps' -count=1` and `go test ./internal/runner -run 'TestAModeArtifactRecipe' -count=1`
Expected: **FAIL** — `undefined: recipe.ModeArtifact` and the schema tests' first run against an unknown key.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`. The recipe schema's `steps` variants gain artifact's two shapes as their own `oneOf` entries (a new `$defs.artifactSteps` beside the existing `reviewModeReview`), and `schemas/recipe.schema.json`'s conditional gains "`mode: artifact` → exactly one render, optionally led by one plan".
- [ ] **Step 3: The schema is compiled by a test, so a malformed one fails here**: `go test ./schemas -count=1`. Expected: `ok`.
- [ ] **Step 4: `gofmt -l .`** Expected: no output.
- [ ] **Step 5: Commit.**

---

### Task 2: `mode: artifact` in the runner, and the render step

**Files:**
- Create: `internal/runner/artifact.go`, `internal/runner/artifact_test.go`
- Edit: `internal/runner/runner.go` (the dispatch at `:399-403`, `r.artifact` beside `r.review` at `:831-833`, the two git-env sites at `:635` and `:1135`, the mode cross-check at `:930-935` — replacing Task 1's refusal arm), `internal/runner/checkpoint.go` (`checkpointsOn`, `:65`)
- Edit: `internal/task/task.go` + `schemas/task.schema.json` + `internal/task/task_test.go` (`Spec.Mode`), `internal/cli/run.go` (the launcher writes it), `internal/cli/run_test.go`
- Create: `internal/runstore/artifact.go` (this task's `RenderRecord` and `OutcomeArtifacts` only), `internal/runstore/artifact_test.go`
- Edit: `internal/runstore/runstore.go` (`Record.Render`), `schemas/result.schema.json` (the `render` property and `outcome`'s new value), `schemas/schemas_test.go`
- Edit: `internal/runner/report.go`, `internal/runner/report_test.go` (**this task adds the `OutcomeArtifacts` arm and the Artifacts section — see below**) and `internal/cli/cancel.go` + its test (`finalized`'s `NoPR`, `:468`)
- Delete: `internal/runner/modeartifact_test.go` (Task 1's file; its one case is folded in here)

**Interfaces:**
```go
// package task
type Spec struct {
    // … existing fields …
    // Mode is the recipe's mode, written by the launcher from the recipe it
    // resolved ("artifact" today). The runner cannot read the recipe before
    // it reads the configuration, so the spec is what selects the shape at
    // bootstrap — exactly as ReviewPRs does for mode: review — and the
    // resolved recipe is cross-checked against it afterwards.
    Mode string `json:"mode,omitempty"`
}
```
```go
// package runstore
// OutcomeArtifacts is a run whose deliverable is in the bucket.
const OutcomeArtifacts = "artifacts"

// RenderRecord is what the render step did, recorded into result.json.
// CommandSHA256 is the hash of the string the runner RAN — the commands.render
// text as the runner read it from fugaro.yaml — which is what makes it
// checkable against the repository at the recorded ref (design §3, D8).
// Command is that same string with the redactor applied, so the record never
// holds a secret; **whenever the redactor fires the two are deliberately not a
// hash pair**, and CommandSHA256 is the unredacted string's hash either way.
// Recording the hash of the redacted text instead would attest only to the
// record's own consistency. Tree is always "working" today and exists because
// the design's D8 is what decides it (plan P2).
type RenderRecord struct {
    Command       string  `json:"command"`
    CommandSHA256 string  `json:"command_sha256"`
    Tree          string  `json:"tree"`
    ExitCode      int     `json:"exit_code"`
    TimedOut      bool    `json:"timed_out,omitempty"`
    DurationS     float64 `json:"duration_s"`
    StartedAt     string  `json:"started_at"`
}
```
```go
// package runner
// artifact is whether this run is a mode: artifact run (design
// artifact-runs.md §3). Set at bootstrap from the TASK SPEC's Mode, beside
// r.review, because the recipe is not resolved until after the configuration
// is read (runner.go:926): the spec selects the shape, and the resolved
// recipe is cross-checked against it in exactly two directions — never a
// third:
//   - a spec whose Mode disagrees with the resolved recipe's mode is refused;
//   - a resolved artifact recipe whose spec does not claim it is refused too.
//
// An EMPTY Mode is a real case and not a default to the implement path: the
// launcher leaves the recipe to the runner whenever it cannot resolve one
// (runnerChooses, internal/cli/run.go:950 — no recipe in the task, not a
// follow-up, no checkout here), so the spec carries no mode at all and the
// runner is the one that reads agent.recipe at the ref. The rule is therefore
// "spec.Mode when set, the resolved recipe's mode when not" — never "empty
// means implement". The cross-check has one more arm because of it: a spec
// that carries Inputs under a non-artifact resolved mode fails the run before
// the first step (design §8.2), so a hand-written task.json, or a launch that
// predates Task 6's launcher-side refusal, cannot download attachments into a
// run that opens a pull request. An artifact spec with NO inputs is legal and
// must stay legal: an out-only run (steps: [render], or a plan stage that
// needs nothing attached) carries an empty Inputs and is launched every day,
// so "a spec with inputs is an artifact run" is one direction of that
// cross-check and its converse is not a rule.
type run struct {
    // … existing fields …
    artifact  bool
    out       *recipe.Outputs
    renderRec *runstore.RenderRecord
}

// runArtifactMode runs an artifact run: the optional plan stage, then the
// one render step. It sets skipPR and ends the run with OutcomeArtifacts
// on success, failed/OutcomeNone otherwise. It never pushes.
func (r *run) runArtifactMode(ctx context.Context)

// runRender runs commands.render once, in the checkout, as the agent's
// user, under timeouts.render and the run's own deadline, capturing a
// redacted log tail. Its exit code decides the run.
//
// The command string and the timeout come from the runner's IN-MEMORY
// workflow config (r.wf.Commands.Render, r.wf.Timeouts.Render), which
// bootstrap set once at runner.go:962 and nothing re-reads: this function
// is NOT a verify.LoadSettings caller, and neither is runProbe. That is
// deliberate, not an omission — <StateDir>/workflow.json is 0644 inside a
// directory the plan-stage agent can write (it is the same user in the same
// container), and verify.LoadSettings re-reads it per invocation
// (internal/verify/verify.go:26,60,85). A render that loaded its command
// from there could be made to run a string the reviewer never read, while
// render.command_sha256 attested to it (design §3, Review Focus 1).
func (r *run) runRender(ctx context.Context) (runstore.RenderRecord, error)
```

**The seams, each with the site as it is on `origin/main` and why it is that site.** This table is the correction of this plan's own first draft, which put the mode on the recipe and the gate on the checkout arm:

| Site | Change | Why there, and not where the first draft had it |
|---|---|---|
| `internal/runner/runner.go:831-833` | `if spec.Mode == recipe.ModeArtifact { r.artifact = true }`, beside `len(spec.ReviewPRs) > 0 → r.review` | **the ordering seam.** `resolveRecipe` runs at `:926`, *after* the configuration is read, and the checkout switch (`:868-921`) is decided before that — so nothing at the checkout can depend on the recipe's mode. Review mode solves it the same way: the spec carries the shape |
| `internal/runner/runner.go:399-403` | the dispatch gains `case r.artifact: r.runArtifactMode(runCtx)` | one branch beside `case r.review:`; without it the artifact recipe takes `runAgentLoop` and opens a pull request |
| `internal/runner/runner.go:635` **and** `:1135` | `!r.review` → `!r.review && !r.artifact` at **both** | the first draft named only `:635` (the repository's own env). The credentials that actually reach the agent's stage environment are built at `:1135`, in `bootstrap`'s env map, so a task that changes one and not the other leaves the git token in the agent's environment and fails its own test |
| `internal/runner/runner.go:899-921` | **no change** — an artifact run takes the implement checkout arm (`CheckoutNewBranch`), and `r.rec.Branch` is the branch lock's key, never pushed (review mode's own wording, `:878-887`) | there is no pull request whose `fugaro.yaml` could be untrusted, so `readBaseConfig` is not needed; the draft's "bootstrap's mode branch at `:878`" was not expressible at all |
| `internal/runner/checkpoint.go:65` | `checkpointsOn()` gains `&& !r.artifact` | **this is the real "never pushes" gate.** Checkpoints are started per stage at `runner.go:1684`, not at the checkout; the `!r.review` at `:966` guards `FetchBase` (a read) and the checkpoint *base SHA*, so changing it alone would still let a stage push a tip |
| `internal/runner/runner.go:930-935` | the mode cross-check gains its artifact pair — a spec/recipe mode disagreement refused, an artifact recipe the spec does not claim refused, and **`Spec.Inputs` under a non-artifact resolved mode refused** — and **Task 1's `case rcp.Mode == recipe.ModeArtifact` refusal is deleted here** | the recipe is known now, and the spec must agree with it both ways. **An empty `Spec.Mode` takes the resolved recipe's mode** rather than defaulting to implement: the launcher leaves the recipe to the runner when it cannot resolve one (`runnerChooses`, `internal/cli/run.go:950`), so the spec genuinely carries no mode on those launches (design §8.2) |
| `internal/runner/runner.go:1091-1099` | **no change** — `!r.review` stays | deliberate: an artifact run keeps the workflow's secrets (Global Constraints, design §8.6/D10), and the test asserts they are present rather than absent so a later refactor cannot change it silently |
| `internal/runner/gate.go:305-313` (`endWithoutPR`) | **no change** — the artifact arm sets `r.skipPR` itself | finalize's PR path is already keyed on it |
| `internal/runner/runner.go:1073-1081` | **no change** — and deliberately so, on both sides: `verify.Settings` gains **no** `Render`/`Probe` field, because `Settings` is read back through `verify.LoadSettings` (`internal/verify/verify.go:85`) from a 0644 file the plan stage can rewrite | the render's command comes from `r.wf`, the in-memory config `bootstrap` read once at `:962`; putting it in `Settings` would move it into a file the agent can edit (Review Focus 1) |
| `internal/runner/runner.go:962` | **no change** — `r.cfg, r.wf = cfg, wf` is the one place the workflow config is kept | this is the source the render and the probe read; nothing re-reads `fugaro.yaml` after it |

The plan stage: one agent stage named `plan`, its own stage timeout, its own `agent.max_budget_usd` (the existing stage machinery, unchanged), with the task text plus the recipe's `use_when` plus the render plan's expected path, `.fugaro/render-plan.json`. Its prompt is the *only* place the run describes the deliverable to the agent, so it is written to say: write a plan, do not render, the render is the runner's. **It does not name `FUGARO_INPUTS_DIR`**: that variable arrives with Task 6, which adds it beside `FUGARO_STATE_DIR` in the env map this task builds — and until then a plan stage cannot see an input, because nothing can attach one.

**The report, and the two arms that fall through today.** `internal/runner/report.go`'s outcome switch is three cases — `OutcomeReady` → "ready for review", `OutcomeNone` → "stopped — {reason}", and `default:` → **"draft — {reason}"** (`:72-79`) — so a new outcome value silently reads as a *draft pull request* with an empty reason. **This task adds the arm** (`case runstore.OutcomeArtifacts:` → "`**Outcome:** artifacts — the deliverable is in this run's bucket, no pull request`") **and the render half of the "Artifacts" section** design §9 requires: the render's command, its tree, exit code, wall clock, and the `cost.compute_usd` estimate with `compute_estimated`. **The per-file table — name, size, short sha256, recorded content type and the probe's verdict — is deliberately *not* here**: it renders `runstore.ArtifactFile`/`ProbeResult`, which are `Record.Artifacts`' types and are Task 4's by P13, so the section is added **table header and render lines now, table rows in Task 4** (`case OutcomeArtifacts` says "the artifacts are listed below"; with no `rec.Artifacts` the table is absent and the reader is told the record carries none), and Task 5 adds the probe column. That is the honest split: a task never renders a type it does not own, and Task 2's report test asserts only what Task 2 can write. `internal/cli/cancel.go:468` has the same shape — `r.NoPR = rec.Outcome == runstore.OutcomeNone` — so a **succeeded** artifact run is reported by `fugaro cancel <run>` as though it had opened a pull request; the arm becomes `rec.Outcome == runstore.OutcomeNone || rec.Outcome == runstore.OutcomeArtifacts`. **`halt.go:177` needs no change, and the first draft of this plan wrongly said it did**: its continuation advice is `"start the run again"` when `rec.PR == nil && rec.Outcome == OutcomeNone` (`internal/runner/halt.go:174-178`), and a halted artifact run *is* `OutcomeNone` — `OutcomeArtifacts` is the success value the run writes when it finishes, so it can never be the outcome of a halted run, and an added `|| rec.Outcome == runstore.OutcomeArtifacts` arm would be dead code that no test could reach. Those are the two non-test sites, and `internal/cli/runs.go:671` needs nothing: it decays to a PR column from `r.PRURL`/`r.PR`, both empty for an artifact run.

**The sites that grep does not find, because they switch on a *row's* outcome rather than the record's — and the display an owner actually watches.** The `default:` in `report.go` reads the record, but `fugaro watch` reads the same value out of `result.json` through `internal/runview` and switches on it in two places with **no default arm at all**, so an artifact run's success is silent rather than wrong: `internal/watch/plain.go:99` (`switch f.Outcome`, only `"ready"` and `"draft"` arms, over `word := strings.ToUpper(f.Stage)`) and `internal/watch/render.go:653` (`switch run.Outcome`, the same two arms over `strings.ToUpper(run.Stage)`) — an artifact run is drawn as its bare stage word and the outcome never appears. **So the enumeration is by symbol and not by a pattern:** `git grep -n -e 'Outcome ==' -e 'switch .*\.Outcome' -e 'switch .*Outcome' origin/main -- 'internal/**/*.go'` — the shape `case rec.Outcome` / `switch f.Outcome` / `switch run.Outcome` all match, which the narrower `"Outcome ==\|switch rec.Outcome"` this plan first carried could not. The four call sites are those two record-reading ones (`report.go`, `cancel.go`) plus these two row-reading ones.

- **Both switches gain an `"artifacts"` arm**, and the word is deliberately not a stage word: plain prints `ARTIFACTS, NO PR` and the dashboard `artifacts, no PR` in the success colour, exactly where `"ready"` prints `READY FOR REVIEW`. A bare `SUCCEEDED` would be the one thing an owner must not see here — the run did not do the thing a `SUCCEEDED` implement run does. `TestWatchShowsAnArtifactRunsOutcome` asserts both renderings from a fixture whose `Outcome` is `"artifacts"`, and that the string `SUCCEEDED` is not the last word on that row.
- **`filter.go:67`'s `ReadyForReview` is left alone, deliberately**: it selects `r.Outcome == "ready"`, and an artifact run is *not* ready for review — it has no pull request to review, and listing it under "Ready for your review" would be the same lie in the opposite direction. `TestArtifactRunIsNotReadyForReview` pins that it is absent.
- **`internal/watch/finished.go:17`'s comment is the enum's documentation and is updated with it** — `// ready, draft, none, reviewed` becomes `// ready, draft, none, reviewed, artifacts` — since that line is where a reader learns what the field can hold. No `finished.go` code changes: `RunRow.Finished`/`Failed` are set from the status and the outcome is passed through untouched (`:53`).

The `render` record keeps the shape the design fixes, and the two fields the design's D8 added are why it is legible: `command` is the **redacted** exact string (the redactor is already there), `command_sha256` is the hash of the string **the runner ran** — the unredacted `commands.render` text it read from `fugaro.yaml`, which is the text a reviewer can hash too (design §3, D8) — and `tree` is `"working"` — the checkout the plan stage also had. **The two are one hash pair only when the redactor did not fire**; a test with a redaction-triggering command pins that they diverge on purpose, so a later "fix" cannot quietly switch the hash onto the redacted text and leave D8's claim false. `OutcomeArtifacts` is the new `runstore` value (`"artifacts"`), added to `schemas/result.schema.json`'s `outcome` enum **here**, because this task is what writes it (P13).

**The launcher's side of `Spec.Mode`, which is what makes the field's presence a fact and not a hope. Three things, and the second is the one that is easy to get wrong.**

1. **The launcher writes `Mode` from the recipe it resolved**, at the one place the launch already holds the recipe — beside where it writes `ReviewPRs` for a review recipe, in the `Spec` literal `createTask` receives (`internal/cli/run.go`). A recipe whose `mode` is `implement` or `review` writes **nothing** (`omitempty`), so every existing spec is byte-identical to what the previous release wrote, and only an artifact recipe changes the shape of the JSON an old runner sees.

2. **A launch that cannot read its recipe must not write an empty `Mode` and hope.** `resolveRecipe` has paths where what it returns is not the recipe the run will use: a project layer the launcher may not read, and a **stale cached project recipe** standing in for an unreachable bucket (the paths `printRecipeNote` reports, and the ones `budget preview` notes too). On any of those, `Mode` would stay empty for a recipe that is `mode: artifact`, the spec would be indistinguishable from an implement spec, the image gate below would not fire, and an old runner would take the implement path — a silent pull request where the owner asked for a file. **So the launcher refuses**: when the resolution note says the recipe did not come from the repository or a readable project layer, an artifact launch — one whose recipe name is the artifact recipe the launch intended, from `--recipe` or `agent.recipe` — is refused with that note as its reason, before anything is written. The conservative direction is the right one; the owner reruns when the layer is reachable. `TestAModeArtifactLaunchRefusesWhenTheRecipeCannotBeRead`.

3. **The image gate, which `Spec.Mode` now needs for the same reason `--attach` does.** A spec carrying `mode` is refused outright by an older runner — `task.Parse` decodes with `DisallowUnknownFields` (`internal/task/task.go:199-202`) — so an artifact launch against a job image that predates `artifactsSince` must be refused by the launcher through the seam that exists for exactly this skew: `checkImageSince(ctx, env, slug, spec, kind, artifactsSince, "artifact recipes", "artifact runs", "", warn)` in the read path (`internal/cli/run.go:419`, beside the project-layer and named-token gates) and its clone-workflow twin `checkCloneImageSince(env.lc, spec, artifactsSince, …)` (`:402`, the project-layer gate's twin) — the two-branch shape every neighbouring gate uses — with the message naming `fugaro image refresh`. **This is the gate for a plain artifact launch; Task 6's `--attach` call is the same seam with `--attach` as its subject, and both are needed**, because an artifact recipe can be launched with nothing attached. `TestAnArtifactLaunchGatesOnTheImageThatKnowsMode`.

**And the two launch shapes an artifact run cannot have, refused where the launcher already refuses them.** The `--attempts` block (`internal/cli/run.go:181-191`) is the pattern: it rejects the flags a best-of-N batch cannot carry, by name, before anything else happens. An artifact run opens no pull request and pushes no branch, so:

- **`--pr`, and therefore every follow-up run, is refused.** The follow-up path exists to resolve a pull request's chain — its branch, its previous run, its account, and the ref and workflow it inherits (`internal/cli/followup.go:27-40`) — and an artifact run has no pull request to resolve, so a follow-up would have no anchor and would have to take `ref`/`workflow`/account from a run that never opened one. The refusal reuses `--attempts`' own wording shape: "a `mode: artifact` recipe opens no pull request; it takes no `--pr`".
- **`--attempts` is refused.** A best-of-N batch is defined by the pull requests its N runs open — its whole point is picking a winner among them — so the flag already refuses `--pr`, `--run-id`, `--retry` and a review-mode recipe; an artifact recipe joins that list rather than inventing a new rule.
- **`--retry` and `--run-id` are *allowed*, deliberately, and the difference is worth writing down**: `--retry` re-launches the **stored task**, which already carries its `mode` and so cannot drift from the shape the run had, and `--run-id` is how a refused launch is repeated. Only the flags that need a pull request are refused.

`TestArtifactLaunchRefusesPRAndAttempts`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/runner/artifact_test.go
func TestArtifactRunNeverPushesAndHasNoGitToken(t *testing.T) // Review Focus 2: zero pushes, outcome artifacts, and no credential in the stage env
func TestArtifactRunKeepsTheWorkflowsSecrets(t *testing.T)    // the deliberate non-change, asserted so it cannot drift
func TestArtifactRunDoesNotCheckpoint(t *testing.T)          // checkpointsOn() false with CheckpointsOn() on and both SHAs set
func TestArtifactRunOutcomeIsArtifacts(t *testing.T)
func TestRenderFailureEndsTheRunFailedWithNoArtifacts(t *testing.T)
func TestRenderTimeoutIsRecordedAndFailsTheRun(t *testing.T)  // Review Focus 7
func TestRenderCommandComesFromSettings(t *testing.T)         // Review Focus 1, with the assertion the plan can actually make
func TestRenderCommandSurvivesAPlanStageThatRewritesSettings(t *testing.T) // Review Focus 1: the plan stage overwrites <StateDir>/workflow.json's render key; the command that runs and is recorded is unchanged
func TestRenderCommandSHA256IsOfTheStringThatRan(t *testing.T) // the hash equals sha256 of the fixture's commands.render text — what a reviewer can recompute
func TestRenderCommandAndItsHashDivergeOnlyWhenRedactionFires(t *testing.T) // a command carrying a redaction-triggering value: the recorded command is redacted, the hash is still the unredacted string's
func TestBlankRenderCommandRefusesTheRun(t *testing.T)        // Review Focus 1, fail closed
func TestPlanStageIsOptional(t *testing.T)                    // steps: [render] runs with no agent call at all
func TestArtifactRunRecordsNoBranchAndNoPR(t *testing.T)
func TestReportSaysArtifactsNotDraft(t *testing.T)            // report.go's new arm: the words "artifacts", never "draft"
func TestReportShowsTheRenderAndTheComputeEstimate(t *testing.T) // Task 2's half of the section: command, tree, exit code, wall clock, compute_usd; the per-file table is Task 4's and this test asserts its absence is graceful
func TestCancelDoesNotClaimAPullRequestForAnArtifactRun(t *testing.T) // cancel.go:468
func TestHaltAdviceForAHaltedArtifactRunSaysStartAgain(t *testing.T) // the deliberate NON-change: a halted artifact run is OutcomeNone, so halt.go:177 already prints "start the run again" and needs no new arm — asserted so a later "fix" cannot add an unreachable one
```
```go
// internal/watch/watch_test.go
func TestWatchShowsAnArtifactRunsOutcome(t *testing.T)  // plain: "ARTIFACTS, NO PR"; dashboard: "artifacts, no PR" — never a bare SUCCEEDED (plain.go:99, render.go:653)
func TestArtifactRunIsNotReadyForReview(t *testing.T)   // the deliberate non-change: filter.go:67 still selects only "ready"
func TestAModeArtifactSpecThatDisagreesWithTheRecipeIsRefused(t *testing.T) // Review Focus 10
func TestAModeArtifactRecipeWithNoSpecModeIsRefused(t *testing.T)          // Review Focus 10, the other direction
func TestAnEmptySpecModeTakesTheRecipesMode(t *testing.T)                  // the runner-chooses case: no Mode in the spec, an artifact recipe resolved, and the run is an artifact run — never an implement default
func TestInputsUnderAnImplementRecipeFailTheRun(t *testing.T)              // design §8.2: a spec carrying Inputs whose resolved recipe is mode: implement — refused before the first step, nothing downloaded
```
The harness (`internal/runner/runner_test.go:53-120`) already provides the in-memory bucket, the fake provider and the scripted agent; the artifact tests use `newHarnessFiles` with a fixture recipe committed to the remote (`.fugaro/recipes/sizzle.yaml`) and a `fugaro.yaml` carrying `commands.render`, so no new fake is needed. **If the scripted agent cannot yet script "the agent writes a file", that is a two-line addition to the harness (a step that writes into `req.Dir`), and it belongs to this task.**

Run: `go test ./internal/runner -run 'TestArtifact|TestRender|TestPlanStage|TestAModeArtifact|TestReport|TestHalt' -count=1`, `go test ./internal/cli -run 'TestCancel|TestArtifactLaunch|TestAnArtifactLaunch|TestAModeArtifactLaunch' -count=1`, `go test ./internal/watch -count=1` and `go test ./internal/task ./internal/runstore ./schemas -count=1`
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
//
// ifNotExist is Create's own condition (blobx.go:107-127), carried into the
// streaming write through the same WriterOptions the existing `write` uses
// and classified the same way — gcerrors.FailedPrecondition becomes
// ErrExists. It is a *parameter* and not a separate method because the
// caller that needs it (the launcher's --attach upload, Task 6) is the same
// caller that needs the streaming path: a 256 MiB attachment cannot go
// through Create, which takes a []byte. The condition holds on every
// backend gocloud ships — fileblob stats the destination and returns
// FailedPrecondition too (gocloud.dev/blob/fileblob/fileblob.go:845-850) —
// but it is still only race hardening: the launcher's own run-id refusal is
// what is deterministic, and a condition that fires mid-upload leaves the
// earlier attachments written.
//
// ON ANY ERROR IT ABORTS THE WRITE BEFORE IT CLOSES IT. go-cloud requires
// Close to be called even for an aborted write — "To abort a write, cancel
// ctx; ... The caller must call Close on the returned Writer, even if the
// write is aborted" (gocloud.dev/blob/blob.go:1138-1142) — and only
// cancelling the context stops the object being committed: Close alone
// commits whatever arrived. So PutFile owns a per-call
// context.WithCancel, and on a source read error, a short read, or a
// sha256 that does not match what was read it CANCELS FIRST AND THEN
// CLOSES, returning the original error and not Close's. Without this rule a
// failed upload leaves a truncated object at a key the run recorded, and
// the owner's `fugaro artifacts get` fails a hash check on a file that
// should never have been written; blob.go:436-456 is the same pattern in
// go-cloud's own MD5 path (cancel the context, then close the driver
// writer).
func (b *Bucket) PutFile(ctx context.Context, key, path, contentType string, ifNotExist bool) (size int64, sum string, err error)

// GetFile streams key into path, up to limit bytes, hashing as it goes, and
// returns the size and the lower-case hex sha256 of what it wrote. sum is the
// *expected* hash (the record's own, or the task spec's for an input): when it
// is non-empty and does not match, the temporary file is removed and nothing
// is ever placed at path — ErrMismatch, with both hashes in the message. An
// object larger than limit is ErrTooLarge, and likewise leaves path alone:
// the cap refuses, it never truncates.
//
// strict decides how a 403 is classified, and the two callers need OPPOSITE
// answers, which is why it is a parameter (the round-3 review's correction;
// design artifact-runs.md §3):
//
//   - strict (fugaro artifacts get): a 403 is ErrForbidden and a 404 is
//     ErrNotExist — ReadStrict's classification (blobx.go:164-171). Its
//     caller is a launcher or an operator holding unconditional
//     objectAdmin, which includes storage.objects.list
//     (docs/design/bucket-iam.md:88,126-128), so for them a 403 really is
//     an access problem and "no such artifact" would be a lie.
//   - non-strict (the runner's input download): a 403 folds into
//     ErrNotExist, exactly as Read does. The run's own account holds
//     storage.objectUser under a prefix condition and CANNOT list, so GCS
//     answers a GET of a missing object with 403, not 404 — blobx's own
//     comment says so (blobx.go:143-176, isForbidden at :200) — and a
//     strict caller here would report a MISSING INPUT as an IAM problem and
//     send the owner to change grants that are already correct.
func (b *Bucket) GetFile(ctx context.Context, key, path, sum string, limit int64, strict bool) (size int64, sha string, err error)
```

Both use `NewWriter`/`NewReader` on the embedded `*blob.Bucket` (the type already embeds it and already reaches GCS-specific handles through `BeforeWrite`/`As`, `blobx.go:109-138`), `io.Copy` with a `sha256.New()` through `io.MultiWriter`, and `os.CreateTemp` in the destination's directory so **`path` is only ever reached by a `Rename` of verified bytes**: a mismatch or an over-cap object removes the temp file and leaves no destination at all. Content type is set on the writer exactly as `write` sets it (`blobx.go:110-111`), and `GetFile` checks the writer's own `Content-Length` first when the backend reports one (the `file://` bucket does; GCS does) — the check is an optimisation, not the guarantee, which is the limit. **The expected hash is an argument because the two callers cannot check it afterwards and still keep the "nothing was written" property:** the CLI would otherwise find the mismatching file already sitting at its final path (Review Focus 9), and the runner's input download would have to delete it. `PutFile` has no such argument: the hash *is* what it produces, and its caller records it.

- [ ] **Step 1: Write the failing tests**

```go
// internal/blobx/blobx_test.go
func TestPutFileStreamsAndHashes(t *testing.T)        // 8 MiB through a file:// bucket, read back with ReadMax and compare
func TestPutFileSetsContentType(t *testing.T)
func TestPutFileWithIfNotExistRefusesAnExistingObject(t *testing.T) // over gcpfake.NewGCS, the fixture TestConditionalOpsOnGCS uses: ErrExists, and the object's bytes are unchanged
func TestPutFileWithoutIfNotExistOverwrites(t *testing.T)           // the artifact upload's own case: it is writing a new key
func TestPutFileAbortsTheWriteBeforeClosingOnAFailedRead(t *testing.T) // the abort rule: a source that errors mid-copy leaves NO object at key (assert the bucket), and Close is still called
func TestPutFileAbortsOnAShortRead(t *testing.T)                      // the source shrank between stat and copy: no object, the original error returned
func TestGetFileRefusesOverTheLimitAndLeavesNothing(t *testing.T) // no destination, no temp file left behind
func TestGetFileRefusesAMismatchAndLeavesNothing(t *testing.T)    // the expected hash is an argument: nothing at path, temp removed
func TestGetFileWithoutAnExpectedHashStillReturnsOne(t *testing.T) // "" means no check, and the hash comes back for the caller to record
func TestGetFileClassifiesForbidden(t *testing.T)     // forbiddenBucket(t) helper already exists; strict mode returns ErrForbidden
func TestGetFileClassifiesMissingAsNotExist(t *testing.T) // a genuine 404 is ErrNotExist, not ErrForbidden, in both modes
func TestGetFileNonStrictFoldsForbiddenIntoNotExist(t *testing.T) // the runner's mode: a 403 and a 404 are the same ErrNotExist, because the job account has no storage.objects.list (blobx.go:143-176) — a missing input must never read as an IAM problem
```
Run: `go test ./internal/blobx -run 'TestPutFile|TestGetFile' -count=1`
Expected: **FAIL** — `b.PutFile undefined`.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 4: The outputs — select, sanitise, cap, hash, upload, and record

**Files:**
- Edit: `internal/runstore/artifact.go`, `internal/runstore/artifact_test.go` (both created by Task 2)
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
// RenderRecord, OutcomeArtifacts and InputFile are NOT here: the first two
// are Task 2's and InputFile and Record.Inputs are Task 6's, in this same
// file, because they are what write them (P13). This task adds to Record,
// optional and absent from required:
//   Artifacts []ArtifactFile `json:"artifacts,omitempty"`
// (Record.Render and the outcome value are already there, added by Task 2.)
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

The sanitiser is one function with one refusal list (design §8.2): the name is the match's path **relative to the deepest common root of the recipe's globs**, cleaned with `path.Clean`, and it must be non-empty, must not be absolute, must not contain a `..` element or a NUL/newline, **must not begin with a dot**, must be at most 200 bytes, and must not collide with a name already taken (after cleaning). **The dot rule is a matched pair with `fugaro artifacts get`'s** (design §9): `get` refuses a dot-named entry, and Go's `filepath.Glob`/`Match` — unlike a shell — match a leading dot, so without the rule here `out/*.mp4` would upload and record a `.hidden.mp4` the retriever can never hand back. Nothing else about a dot is affected: `sizzle.v2.mp4` and `clips/a.mp4` are both names this accepts. A symlink in the checkout whose target is outside the outputs root is skipped with a warning rather than followed — a refusal, not a copy, because the bytes would then be a file the run never rendered.

The caps, and the order they are checked in: match count against `max_files`, then each match's size against `max_bytes` (**per artifact**), then the set's total against `max_total_bytes` (**the set cap, its own key and not `max_bytes × max_files`** — a cap derived from two other caps is a cap the owner cannot set). Nothing is staged until every check passes — **so a refused set leaves the staging directory empty and the bucket untouched** (Review Focus 4).

`schemas/result.schema.json` gains the `artifacts` property (the `render` property and the `artifacts` outcome are already there from Task 2, and `inputs` arrives with Task 6, which owns `Record.Inputs`), not required. `schemas/schemas_test.go`'s `TestResultSchemaAcceptsRunnerRecords` grows with the struct (it marshals a fully-populated `runstore.Record`, so it fails until the schema has the new properties — that is the test that keeps the two in step), and a new `TestResultSchemaAcceptsARecordWithoutArtifacts` unmarshals a pre-change fixture and asserts the new fields are empty (Review Focus 8).

- [ ] **Step 1: Write the failing tests**

```go
// internal/runner/artifact_test.go
func TestStageArtifactsRefusesEscapes(t *testing.T)           // Review Focus 3, the table
func TestStageArtifactsRefusesTheCaps(t *testing.T)           // Review Focus 4, every shape: count, one oversized artifact, and a set over the total whose members are all under max_bytes
func TestUploadArtifactsWritesUnderTheRunPrefix(t *testing.T) // assert the bucket's key set
func TestArtifactHashMatchesTheUploadedBytes(t *testing.T)    // Review Focus 5
func TestArtifactsAreUploadedInNameOrder(t *testing.T)
func TestResultJSONCarriesRenderAndArtifacts(t *testing.T) // inputs are Task 6's; that test grows then
func TestReportListsTheArtifacts(t *testing.T)             // the table Task 2 deliberately does not write: name, size, short sha256, content type, and (from Task 5) the probe verdict
func TestArtifactNameCollisionAfterCleaningIsRefused(t *testing.T)
func TestStageArtifactsRefusesADotNamedMatch(t *testing.T) // the paired rule: out/.hidden.mp4 is refused, not uploaded-and-unretrievable
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
- Edit: `internal/runner/artifact.go`, `internal/runner/artifact_test.go`
- **No edit to `internal/verify/verify.go` or the `verify.WriteSettings` call** — there is no `Settings.Render`/`Settings.Probe` at all (Task 1, P13): the probe's command comes from `r.wf.Commands.Probe`, the in-memory config, **not** from `verify.LoadSettings` (`internal/verify/verify.go:85`)

**Interfaces:**
```go
// package runner
// runProbe runs commands.probe once, after the render and before the
// upload, with the whole staged set named in the environment:
//   FUGARO_OUT_DIR   the staging directory's absolute path — flat: it
//   holds exactly the files that will be uploaded, one per name below, so
//   "$FUGARO_OUT_DIR/$name" is always the staged file. It is the staging
//   root and not the checkout's outputs root (the deepest common root of
//   the recipe's globs, Task 4): a script never has to know the glob shape
//   (design §3, §9).
//   FUGARO_ARTIFACTS_FILE the path of the file holding the staged names,
//   one per line, in upload order — each the relative, sanitised name,
//   which is also the object key's leaf under artifacts/ and the path
//   under FUGARO_OUT_DIR
// A non-zero exit or a timeout fails the run and refuses the upload
// (Review Focus 6): the probe is the only thing that can say a 0-byte
// render is not a video, so it must be able to stop one.
func (r *run) runProbe(ctx context.Context, set artifactSet) (runstore.ProbeResult, string, error)
```

The probe runs through `procgroup.Run` with `Dir: s.RepoDir`, `Env: append(r.env, "FUGARO_OUT_DIR=…", "FUGARO_ARTIFACTS_FILE=…")` and `Grace: commandGrace` — the same call shape `verify.Run` uses (`internal/verify/verify.go:242-246`) — under `timeouts.verify`, since it is a check and not a render. Its output is captured as a redacted log tail (`logtail`, already imported by the gate) and quoted in the report when it fails.

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
- Edit: `internal/task/task.go` (the `Inputs` field and `InputFile`), `schemas/task.schema.json`
- Test: `internal/task/task_test.go`
- Edit: `internal/runstore/artifact.go` (`InputFile`), `internal/runstore/runstore.go` (`Record.Inputs`), `schemas/result.schema.json` (the `inputs` property), `schemas/schemas_test.go` — Task 6 owns these (P13)
- Edit: `internal/cli/run.go` (the flag, the gate, and the ordering above), Create: `internal/cli/run_attach.go`, `internal/cli/run_attach_test.go`
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

// attachProviders lists, sorted, every destination the attached bytes may
// reach through the model, which is design artifact-runs.md §8 item 8 and is
// NOT lc.Providers on its own: lc.Providers holds only the non-Anthropic
// backends (internal/localcfg/localcfg.go:89-91), so a set read from it alone
// is EMPTY for a default Claude workflow — exactly the workflow the owner
// runs — and a line built from it would tell an owner their screenshot stays
// local while it is on its way to Anthropic. The set is the union of:
//
//   - each provider that claims one of the run's models AND may receive this
//     repository's data — config.ProviderFor over Agent.PinnedModels
//     (internal/config/providers.go:268-275, internal/config/config.go:236-244),
//     filtered by config.ModelProvider.AllowsData(repo)
//     (internal/config/providers.go:194), which is the same filter
//     runner.ProvidersEnv applies when it builds the job's own
//     FUGARO_MODEL_PROVIDERS (internal/runner/providers.go:29-51) — the
//     launcher computes what the job will have, and nothing more; and
//   - "Anthropic (direct)" whenever any of those models is not claimed by one
//     of those providers, which is exactly config.NeedsAnthropicKey
//     (internal/config/providers.go:613-631) and is the case a default Claude
//     workflow always takes.
//
// The empty set therefore means "no Anthropic path and no claiming provider",
// which the current Agent.PinnedModels cannot actually produce (three models,
// and an unclaimed one is an Anthropic path) — the branch is defensive, and
// the warning says so by printing the local wording rather than claiming the
// file stays on the machine.
func attachProviders(lc *localcfg.Config, ag config.Agent, repo string) []string

// attachProviderWarning is D9's line, and it is the whole of D9's mechanism:
// one printed sentence naming every destination the attached file may reach,
// so an owner handing over a customer's screenshot is told where it may go
// before it goes. It is printed once per launch, on stderr beside the other
// launch notes (the same writer checkImageSince takes as `warn`), only when
// --attach carried at least one file, and it is never a gate: the policy that
// already governs the model's own data governs this. It names "Anthropic
// (direct)" as well as any provider in lc.Providers, and it uses the local
// wording only for the empty set above.
func attachProviderWarning(warn io.Writer, providers []string)
```

**The order, which is the fix Review Focus 11 asked for: every refusal that can be made from what the launcher already has happens before the first byte leaves the machine.** `--attach --run-id <existing>` today would upload into that run's `inputs/` and only then reach `createTask`'s adoption-or-refusal logic (`internal/cli/run.go:1146-1172`), which sits *after* the task is written and after a launch could have happened — so the object's bytes would already be overwritten even though the run is refused. This task therefore:

1. resolves the repo and the run id;
2. **reads `runs/<slug>/<run>/task.json` first** (the same read `createTask` does, `internal/cli/run.go:1146-1162`). **If it exists at all, the launch is refused here — `--attach` does not adopt.** Not "refused unless byte-identical": an adoption is refused too, because the stored task already names its `inputs` and those objects already exist, so the upload this launch would do is a create-only write against objects that are there (a failure, not a no-op), and "the same bytes" would need a read-back-and-rehash this slice does not do. The refusal message reuses the wording a plain launch already uses for a run id it will not reuse and names the run; it happens **before the caps are checked and before the first byte leaves the machine**. A plain launch — no `--attach` — keeps `createTask`'s adoption rule exactly as it is, byte-identical or refused;
3. **refuses the launch unless it resolves to an artifact recipe** — design §4's rule, and an attachment is only meaningful to a run that produces files rather than a pull request. Two arms, both fail-closed and both before the first byte: a **resolved recipe whose mode is not `artifact`** is refused, naming the recipe and its mode; and a launch where the CLI left the recipe to the runner (`runnerChooses`, `internal/cli/run.go:950` — no recipe in the task, not a follow-up, no checkout here) is refused too, because the CLI cannot prove the mode it will run under, with the message naming the fix (pass the artifact recipe explicitly). Then it checks the caps, the provider-warning line (D9, below) and the image gate (P9);
4. uploads with `PutFile(…, ifNotExist: true)`. **This is race hardening and not the guarantee**, even though the condition does hold on every backend: gocloud's `file://` driver enforces it as well as GCS (`fileblob`'s writer stats the destination and returns `FailedPrecondition`, `gocloud.dev/blob/fileblob/fileblob.go:845-850`, which `blobx`'s `write` classifies as `ErrExists`) — the *deterministic*, explained refusal is step 2's, and a condition that fires mid-upload still leaves the earlier attachments in the bucket. `Create` cannot be used here because it takes a `[]byte` and an attachment is exactly the object too large for one (design §4, Task 3).

`TestAttachRefusesAnExistingRunIdBeforeUploading` asserts this from the recorded call order (zero `PutFile` calls, and the object's bytes unchanged) **in both shapes — a different stored task and a byte-identical one** — and Review Focus 11 carries it. A fresh run id has an empty prefix and uploads normally.

`TestAttachRefusesANonArtifactRecipe` and `TestAttachRefusesARecipeTheRunnerChooses` pin step 3, both from the same place the other refusals are pinned — the recorded call order and the bucket: **zero `PutFile` calls and no `inputs/` key**, so an attachment never reaches the bucket on a launch that cannot use it. The first launches `--attach --recipe <mode: implement>`; the second launches with no resolvable recipe so the runner would choose, which is the arm a launcher-side refusal can only take by declining to guess. The runner's own arm of the same rule — `Spec.Inputs` under a non-artifact resolved mode — is `TestInputsUnderAnImplementRecipeFailTheRun` (Task 2's cross-check, design §8.2), so a hand-written `task.json` cannot slip past the launcher's refusal.

The `--attach` flag is repeatable (`StringArray`) and resolves its repository exactly as a plain launch does — `--repo`, else the checkout's origin (`internal/cli/run.go:155,519-523`), so the goal's `fugaro run --recipe sizzle --attach shot1.png …` needs no `--repo` at all. It refuses a set over **any of the three** caps **before** uploading anything — and the three are independent, which is the part that is easy to get wrong: `inputs.max_files` counts *paths*, **`inputs.max_bytes` bounds one file**, and **`inputs.max_total_bytes` bounds the set** (design §4 step 2, P6). A set of ten 40 MiB screenshots is under `max_files = 16` and under the 256 MiB **per-file** cap and is still over `max_total_bytes = 1 GiB`; a single 300 MiB screenshot is under both the count and the total and is over `max_bytes`. The launcher therefore checks the count, **each path's own size**, and the **sum of the sizes it is about to upload** — stat'ing each path before the first `PutFile` — and refuses with the number that tripped, the cap it tripped and the count in the message, writing nothing. The workflow's `inputs.max_files` is not the only thing it refuses. The name for each attachment is `filepath.Base(path)` after the same sanitiser shape Task 4 uses; two attachments with the same base name are refused rather than overwritten. The image gate (P9) runs with the rest of the launch's gates: `checkImageSince(..., artifactsSince, "--attach", …)` — the same message shape, naming `fugaro image refresh --repo %s --workflow %s`.

The runner's side: before the first step, `downloadInputs` reads `spec.Inputs` and **re-applies both refusals the launcher applied** — the caps and **the name refusals**, with the same list Task 4's `sanitiseName` uses. The caps here are the same three and they are checked the same way, because the spec is text a launcher wrote and the launcher's check is not a fact about it: the entry **count** against `inputs.max_files`, **each entry's own `bytes`** against `inputs.max_bytes`, and the **sum of `inputs[].bytes`** against `inputs.max_total_bytes` — the set cap, never one entry's size and never `bytes × max_files` — all refused **before the first `GetFile`**, so an over-cap spec downloads nothing and leaves `inputs/` empty. (The `bytes` in the spec are the launcher's own claim, and the runner also enforces each file's recorded size against what it actually reads; the two checks are the spec's claim and the bytes on the wire.) And **the name refusals**, with the same list Task 4's `sanitiseName` uses, because an input name becomes a filesystem path: a spec whose `inputs[].name` is `../../.git/hooks/pre-commit`, absolute, NUL-bearing or over-long is refused and **fails the run**, never cleaned into something plausible and never written outside `inputs/`. Each accepted name is then downloaded from `runs/<slug>/<run>/inputs/<name>` into `<RepoDir>/inputs/<name>` with `blobx.GetFile`, **passing the spec's `sha256` as the expected hash and `strict: false`** — the account it downloads with cannot list, so a 403 is what a missing input looks like and must read as "the task names an input that is not in the bucket" (design §3, §8.2), never as an IAM problem — **so a mismatch leaves nothing at the destination**. A file the bucket has but the spec does not is not downloaded at all (it cannot be named), which is the whole reason the list lives in the spec. `inputs/` is written into `.git/info/exclude` (appended once, idempotently) so an artifact run can never commit a screenshot, and `FUGARO_INPUTS_DIR` joins the agent's environment for the plan stage. The record's `inputs` are the spec's list, echoed, so a reader sees exactly what the run was given.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/run_attach_test.go
func TestAttachUploadsBeforeTheTaskSpecIsWritten(t *testing.T) // the order, asserted from recorded calls
func TestAttachRefusesAnExistingRunIdBeforeUploading(t *testing.T) // Review Focus 11: zero PutFile, object bytes unchanged — both for a different stored task and for a byte-identical one
func TestAPlainLaunchStillAdoptsAByteIdenticalTask(t *testing.T)   // the rule --attach does not change: no attachments, adoption as today
func TestAttachRefusesMoreFilesThanTheCap(t *testing.T)
func TestAttachRefusesAnOversizedFile(t *testing.T) // one file over inputs.max_bytes: the per-file cap, named with its own number
func TestAttachRefusesASetOverTheTotal(t *testing.T) // ten files each well under max_bytes whose sum is over max_total_bytes: the set cap, not one file
func TestAttachRefusesDuplicateBaseNames(t *testing.T)
func TestAttachNeedsAResolvableRepo(t *testing.T) // the origin is enough (P7); a checkout with neither is the only refusal
func TestAttachGatesOnTheImageThatKnowsInputs(t *testing.T)   // P9
func TestAttachNamesTheProvidersThatMayReceiveTheFile(t *testing.T) // D9: the sentence, with both providers listed
func TestAttachProviderWarningIsSilentWithoutAttachments(t *testing.T) // D9: a plain launch prints nothing
func TestAttachProviderWarningNamesAnthropicDirect(t *testing.T)      // D9's round-3 correction: a default Claude workflow — lc.Providers empty — prints "Anthropic (direct)", never "routes nowhere"
func TestAttachProviderWarningSaysLocalOnlyWhenNothingRoutes(t *testing.T) // D9: the empty set (no claiming provider AND no Anthropic path) prints the local wording, not a blank name
func TestAttachProviderWarningIsNotAGate(t *testing.T) // D9: the launch proceeds — the line is printed, never returned as an error
```
```go
// internal/runner/artifact_test.go
func TestInputsAreDownloadedHashedAndExcludedFromGit(t *testing.T)
func TestAnInputWhoseHashDoesNotMatchFailsTheRun(t *testing.T) // Review Focus 5, the other direction
func TestASpecClaimingFewerInputsDownloadsOnlyThose(t *testing.T) // Review Focus 4
func TestInputsOverTheCapFailTheRun(t *testing.T)      // max_files: more entries in the spec than the workflow allows
func TestInputsOverTheFileCapFailTheRun(t *testing.T) // max_bytes: one spec entry's own bytes are over it — nothing written under inputs/
func TestInputsOverTheTotalCapFailTheRun(t *testing.T) // max_total_bytes: the spec's entries sum over it although every one is under max_bytes — nothing written under inputs/
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
- Edit: `internal/cli/runs.go` (the listing's artifact column/field), `internal/cli/diagnose.go` (the paths it prints), **`internal/runview/runview.go`** (the view's artifact fields — `runview` is its own package, not a CLI file), and their tests
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
// get downloads one artifact (or all of them with no NAME), verifying the
// byte count and the sha256 against the
// record. blobx.GetFile takes the record's hash as its expected sum and
// strict: true — a launcher or an operator holds unconditional
// objectAdmin, which includes storage.objects.list, so a 403 here is an
// access problem and must not be reported as "no such artifact" (design
// §3) — and renames into place only once it matches, so a mismatch is an
// error naming both hashes with nothing ever written at the destination
// path. An existing file needs --force. Nothing downloaded is ever
// opened, executed or unpacked: the command prints the recorded type, the
// size and the hash and stops there.
//
// The destination is a fresh directory this command creates and then
// defaults to (./<run>-artifacts/, with --out naming its PARENT), never
// ".": the record was written by an untrusted, model-driven run, so a name
// that is absolute, has a ".." element, begins with a dot or escapes that
// root is refused — otherwise `get` run inside a checkout could drop
// .git/hooks/pre-commit or .claude/settings.json for the user's next
// command to run (design §8.2, §9). The refusal is a refusal: a record
// naming .git/hooks/pre-commit is an error, never a flattened
// "pre-commit".
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
func TestArtifactsGetRefusesADotFilePathFromTheRecord(t *testing.T) // .git/hooks/pre-commit, .claude/settings.json, .envrc: refused
func TestArtifactsGetWithoutANameRefusesWholesaleOnOneBadEntry(t *testing.T) // one dot-named entry: the whole download is refused, named, nothing written — never a silent skip
func TestArtifactsLsFlagsANameItCannotRetrieve(t *testing.T)       // the record is shown as it is, with the reason, not filtered
func TestArtifactsGetCreatesAFreshDirectoryRatherThanWritingDots(t *testing.T) // the default is ./<run>-artifacts/, and the checkout is untouched
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
- Edit: `internal/cli/budget_preview.go`, `internal/cli/budget_preview_test.go`, **`internal/recipe/stages.go`, `internal/recipe/stages_test.go`**

**Interfaces:** no new exported API. The preview gains one line per workflow, printed after the existing ceiling line (`internal/cli/budget_preview.go:26-28`) and before the "typical" line (`:489-538`):

```
compute ceiling: 8 vCPU x 32 GiB x 2h0m0s = $X.XX at this installation's compute prices — NOT CAPPED
                 (bounded by timeouts.total, not by budget.per_run_usd; see SECURITY.md)
```

The arithmetic is `p.ComputeUSD(cpu, memGiB, d)` on a `backend.Prices` value (`internal/backend/backend.go:292-296`; there is no `pricing.ComputeUSD`, and the design's §5.3/V18 carry the same correction), the method the runner's own cost estimate calls with the workflow's own resources (`internal/runner/cost.go:63`) — **not** `p.Quantities(...)`: that method is for a table that bills a machine type (`:285-289`), and the launcher's table never carries one (`localcfg.PriceOverride` sets only the two per-second fields, `internal/localcfg/localcfg.go:868-874`) — with the workflow's `resources` and its `timeouts.total`, **not** `timeouts.render`: the line is the worst case a run can bill, and a render that overruns its own step still burns the run's clock until the deadline. Prices come from the same source the runner's do (the installation's `FUGARO_COMPUTE_PRICES`, `internal/infra/spec.go:46,682`); when they are unset the line is printed with the price source named and no dollars, rather than a wrong number.

- [ ] **Step 1: Write the failing tests**

```go
// internal/recipe/stages_test.go
func TestStageBoundArtifactIsOneModelStage(t *testing.T) // ModeArtifact → 1, whatever the knobs
// internal/cli/budget_preview_test.go
func TestBudgetPreviewPrintsTheComputeCeiling(t *testing.T)
func TestBudgetPreviewSaysTheCeilingIsNotCapped(t *testing.T) // the words "NOT CAPPED" and the SECURITY.md pointer
func TestBudgetPreviewComputeCeilingFollowsTheWorkflowsResources(t *testing.T) // 4vCPU/8Gi vs 8vCPU/32Gi double
func TestBudgetPreviewWithoutPricesNamesTheSourceInsteadOfGuessing(t *testing.T)
func TestBudgetPreviewArtifactRecipeDoesNotInflateTheModelCeiling(t *testing.T) // D4: N × 1 × max_budget_usd for mode: artifact
```
Run: `go test ./internal/recipe -run 'TestStageBound' -count=1 && go test ./internal/cli -run 'TestBudgetPreview' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.

**The model ceiling must not inflate for an artifact recipe, and today it would.** `recipe.StageBound` (`internal/recipe/stages.go:7-31`) answers `1` for `ModeReview` and otherwise counts `1 + 2*first + rounds*per`; a `mode: artifact` recipe has no `first_line` and no `review` step, so it would fall into the implement branch and report `1 + 2*rounds` model stages — five at this repository's defaults — where a run of it can start **at most one** (`plan`). The render is a runner command with its own timeout, exactly like a `check` step, and the comment at `:4-6` already states the rule that excludes it. **So `StageBound` gains `ModeArtifact` in its `ModeReview` arm** — one model stage — and that is a change to `internal/recipe`, made here and not in Task 1, because it is only observable through the ceiling this task prints. Task 1's `TestStageBound…`-adjacent tests are unaffected: `ModeReview` and `ModeImplement` keep their figures.

- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 9: Docs, the skills, SECURITY.md, and Check 58 in the checklist

**Files:**
- Create: `docs/artifact-runs.md` (the user-facing guide)
- Edit: `docs/recipes.md` (the format table gains `mode: artifact`, `plan`, `render`, `outputs`), `docs/base-image.md` (ffmpeg through `image.apt`, as a worked example)
- Edit: `SECURITY.md` — **design §8.9's text, verbatim, and it is now four paragraphs plus a bullet**: in "### The assumption", the artifact/input paragraph (which says the render executes repository text read once and is **not** a boundary against the agent — the corrected wording, never "the runner never executes text the agent wrote"), the paragraph that the hash is integrity in transit and not tamper evidence, **the paragraph that the render runs with the run's own environment and so *with the workflow's secrets*** (§8.6/D10, and the reason a repository must not put an artifact recipe where an untrusted author can change it), and **the D11 paragraph — that today the readable-and-overwritable set stops at this repository, and that project-centric phase 3 and the VM backend would widen it, which is why an artifact-producing run stays on the per-repository Cloud Run backend until a per-run boundary exists**; plus the "an artifact run's compute is not capped" bullet in "Known limits"
- Edit: `docs/gcp-live-checklist.md` — Check 58's row in the table and its section, below
- Edit: `plugin/skills/routing/SKILL.md` — its "Choosing a recipe" section (`:47`) gains one paragraph on when an artifact recipe is the right answer, and the words "attach a file only if you would paste it into a pull request on a repository every launcher can read"
- Edit: `docs/release.md` — one plan-shape bullet: the release note names the new recipe mode, the new CLI subcommand and the fact that the caps count model dollars only

**Ordering constraint, as in every plan here:** the docs that name `--attach`, `fugaro artifacts ls|get` and the new keys land **after** Tasks 1–8, because the docs and skill lints resolve what they read (`docs_base_test.go:173`, `plugin/skills_lint_test.go`). `docs/artifact-runs.md` is not in `trackedDocCommands` until someone adds it; when it is added, every flag it names must exist.

**The user guide is short and answers five things**, in this order: what an artifact run is (and that it has no pull request); the recipe (`mode: artifact`, `plan`, `render`, `outputs`) with the ffmpeg example, **carrying D8's rule in the guide's own words: write `commands.render` as the script itself (an inline `|` block), because a command that names a file runs the *working tree's* bytes, which the plan stage could have rewritten — the runner does not promise which copy runs, and the step is not a sandbox**; the attachments and **the warning of design §4 verbatim**; what the run records and how to read it back (`fugaro artifacts ls|get`, the sha256, the retention window); and what is not built (GPUs, signed URLs, compute inside the cap, repository-less runs — the last one as the **recorded end state**, pointing at the design's §13.1 and at this plan's own future section, never phrased as "not wanted").

**Check 58's row goes in the table beside the others**, and its section is written in full: **seven numbered steps**, each preceded by a re-run of **Check 43's guard block** (`docs/gcp-live-checklist.md:1437-1458`) — that is the file's own shape, not a copy of it here.

**The file is the single source of the wording.** This task authors the row and the section; Task 10 runs them and does not quote them back, because a plan that repeats a check's steps is how the two drift (`docs/plans/2026-10-14-openrouter-senior-reviewer.md:146` — that drift was a review finding, and dropping the quote was the fix). What the section must keep asserting, in whatever wording it has:

- **The guard block, verbatim and re-run at the start of every step**: `P=fugaro-dev` **hard-coded and never read from `gcloud`**, `L`/`R`/`S` from the installation, `${VAR:?}` on every argument, the three hard stops (all variables non-empty; `fugaro doctor --project fugaro`'s `project:` line names `fugaro-dev`; the checkout's `origin` is `edgeappinc/fugarosandbox`), `--project fugaro` on every Fugaro command that takes it, **never `belong`** (`:1431`), **every step the owner's and none run by an agent**, and a Restore as the last step.
- **Step 1, the sandbox's prerequisites** (the only step that changes the sandbox's configuration): `image.apt: [ffmpeg]` on the workflow, one `fugaro image refresh`, and the fixture artifact recipe, render script and probe committed to the sandbox. The FACT is the image's digest and the job naming it — **this is the step that makes the ffmpeg claim true on a real image (U2), and it is the one Cloud Build the check pays for.**
- **Step 2, files in**: the three PNGs, and the launch. **The generator is written into the section as a real command, not as a description** — a `python3` one-liner over `zlib`/`struct` writing three 320×240 8-bit RGB PNGs, which is exactly what was run while the design was written (V22: three valid PNGs, 865 bytes each) — followed by `fugaro run --workflow <the one with ffmpeg> --recipe sizzle-check --attach a.png --attach b.png --attach c.png` **with no `--repo`**, since a plain launch resolves the repository from the checkout (plan P7's note). The FACT is the run id and `task.json`'s `inputs` against `sha256sum` of the three files.
- **Step 3, the render record**: `outcome: artifacts`; `render.command` is the repository's command; `render.exit_code` 0; `render.duration_s`; `cost.model_usd` (the plan stage alone) beside `cost.compute_usd` with `compute_estimated: true`; **no branch pushed and no pull request opened**; and the job's own CPU/memory matching the workflow's `resources`.
- **Step 4, the probe**, two halves: the record's `artifacts[].probe` (`ran: true`, exit 0) **and** an independent local `ffprobe` of the file `fugaro artifacts get` wrote — duration and resolution.
- **Step 5, the recorded content type** against the object's own (U5).
- **Step 6, retrieval**: the file comes back, `sha256sum` equals the record's `sha256`, and the **upload's wall clock** is measured against `timeouts.finalize_reserve` (U6). The row also records that the run's objects are inside `runs/` and expire with it.
- **Step 7, the failure direction, three halves (U3)**: a probe that demands an impossible duration must end the run `failed`/`none` with **nothing under `artifacts/`**; a set over `max_total_bytes` (with every member under `max_bytes`) must refuse with the cap named, upload nothing, and leave the **container alive**; and **a render whose own peak exceeds the job's memory** (the staging copy and the render's temp files are both on the memory-backed filesystem, §5.2/D3/U3), run under a deliberately small `resources.memory` — this half **states no expected outcome**, because the design does not know one: the FACT is what was observed (a container killed with **no `result.json` at all**, or a clean recorded `failed`), written down in the section so U3 stops being unverified. It is deliberately the one step whose assertion is "record what you saw".
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
3. **The caps' numbers** (8 artifacts / 256 MiB each / 2 GiB as a set out, 16 files / 256 MiB each / 1 GiB as a set in). They are defaults with ceilings (P6); the task that could invalidate them is Task 10 step 6, which measures the upload's real wall clock against `finalize_reserve`.
4. **The probe's environment variables are the interface.** `FUGARO_OUT_DIR` + `FUGARO_ARTIFACTS_FILE` (a path to a one-name-per-line file, not a joined value) is the whole contract with a repository's script, and it is deliberately dumb (no JSON, no arguments). If a reviewer wants per-file arguments instead, that is P4 and Task 5 alone.
5. **The staging directory's location** (`<StateDir>/artifacts/`) is chosen for parity with `verify/`; nothing else depends on it, and it is not uploaded itself — only the individual files are.
6. **What this plan does not do, deliberately:** no GPU (D3, U1), no compute inside the cap (D4), no retention prefix (D2's alternative), no signed URLs (D7), no repository-less runs (D1's **recorded end state** — the future section below says what they still need), no general `outputs:` on implement recipes (D5's alternative), and no artifact registry or cross-run index. Each is named in the design's §13/§14 with its cost, so a reviewer can rule on it there rather than in a task.
7. **The one claim in this plan that could not be verified offline** is whether `mise` can install `ffmpeg` (U2). The plan does not use it: the docs (Task 9) and the check (Task 10) use `image.apt`, which is a shipped mechanism (`images/derived/Dockerfile.tmpl:26-33`). Task 10's FACT line records the digest, which is what makes the ffmpeg claim true on a real image rather than plausible on a template.
8. **The numbers in Check 58's name and its neighbours are checked, not assumed** (`docs/gcp-live-checklist.md:250-257` is the register: **48–56 are reserved by the project-centric plan (PR #366, merged `c86eb92`) and 57 by the compute backend (PR #375)**; rows 40–47 are at `:317-324` and 57 at `:325`). **58 is therefore the next free number, and the section says so rather than leaving a reader to guess whether 55 was skipped by mistake.** **PR #401 (the desktop console design, open) also claims 58**: this PR keeps it, #401 is not this plan's to edit, and **whichever of the two merges second renumbers itself to the next free number at that moment** — row, section heading and every in-document reference together. Should a third plan take 58 first, this plan's check renumbers the same way and nothing else changes.
9. **The one seam this fix round moved, and why it is the right one.** The first draft put the mode on the recipe and the branch on the checkout arm. Neither is expressible: `resolveRecipe` runs at `runner.go:926`, after the configuration is read, while the checkout switch is decided at `:868-921`; and "never pushes" is really `checkpointsOn()` (`checkpoint.go:65`), started per stage at `:1684`, not the `!r.review` at `:966` (which guards `FetchBase` and the base SHA). The mode therefore travels in the **task spec** (`Spec.Mode`), beside `ReviewPRs`, and the resolved recipe is cross-checked against it in both directions. **If a reviewer prefers the recipe to be the only source, the change is Task 2's two directions and nothing else** — but then the bootstrap cannot know the shape at all.
10. **The command does *not* live in `verify.Settings`, and that is a deliberate narrowing.** The first draft put `Render`/`Probe` there "beside `build|test|lint`", which reads as symmetry and is a hole: `Settings` is written 0644 into the state directory and re-read per invocation by `verify.LoadSettings`, so the plan stage — same user, same container — could rewrite the `render` key and the runner would then run, and hash, a string no reviewer read. The render and the probe read the runner's in-memory `r.wf` instead (`runner.go:962`), Review Focus 1 pins the mutation, and Task 1 therefore does not touch `internal/verify/verify.go` at all. *Veto alternative:* keep the fields and have `fugaro verify` print the render command too; the cost is that the file becomes load-bearing for a command the runner executes.
11. **A new `runstore` outcome value has four call sites, not one — and the two the first draft missed were the ones an owner looks at.** `report.go`'s switch (`:72-79`) would render a successful artifact run as a *draft pull request* with an empty reason through its `default:` arm; `cancel.go:468` reads the value too; and the two `fugaro watch` display switches (`internal/watch/plain.go:99`, `internal/watch/render.go:653`) have **no default arm at all**, so the outcome simply never prints and an artifact run shows as its bare stage word. Task 2 carries all four and their tests, because this is the class of bug a new enum value creates and the plan would otherwise have shipped it. **`halt.go:177` is not one of them**: a halted artifact run ends `OutcomeNone`, whose existing continuation advice is already correct, so the arm an earlier draft of this plan added there would have been unreachable. `internal/cli/runs.go:671` needs nothing (its PR column decays from empty `PRURL`/`PR`), `ReadyForReview` (`internal/watch/filter.go:67`) is deliberately unchanged, and the enumeration is a `git grep` by symbol shape in the task — the narrower pattern this plan first carried matched neither watch site — not a guess.
12. **The render's environment is not sanitised, deliberately.** An artifact run keeps the workflow's secrets (design §8.6/D10, the `!r.review` at `:1091-1099` unchanged), because the render is the same class of repository text as `commands.build`, and removing secrets would remove the ability to render from a private source. **The residual risk is stated, not hidden**: a plan-stage agent that can write the working tree shares the container with a render that has the secrets. The veto alternative is a render with the secrets stripped, and it is one line in Task 2's env construction. **The same shape covers D11**: an artifact is readable by every launcher of the installation, and the VM backend would widen overwrite from one repository to every repository on the profile.

---

## The repository-less end state (a future section, not a task here)

**D1's answer is "repo-bound first", and the other half of the answer — repository-less artifact runs — is on record as the end state, not withdrawn.** It is written here so the next plan does not have to rediscover why it is not in this one, and **it is deliberately not a task**: no task in this plan may be read as preparing for it, and nothing below is scheduled.

A repository-less artifact run is one whose recipe and render script do not come from a repository the run checks out — the sizzle case the owner first answered with, where the render script is the owner's and the input is a screenshot, not a tree. It needs **three things that do not exist today**, in this order:

1. **Project-centric phase 3's shared project jobs** (`docs/design/project-centric.md:15,66-68`) — a job a launch can use without naming a repository, which is what "repository-less" means at the substrate level. Until it exists there is nowhere for such a run to execute.
2. **A per-run narrowing** — a **per-run prefix condition** or a **per-run account**. This is the blocker the design's §8.5 and D11 name: the compiled-in grant is per *repository* (`gcp.JobBucketPrefixes`, `internal/backend/gcp/names.go:374,383-389`, baked into each job's condition by `BucketCondition`), so on a shared project job every run on the profile could read **and overwrite** every other run's artifacts. **An artifact-producing run therefore stays on the plain per-repository Cloud Run backend until that narrowing exists** (D11), which is also why D1 is repo-bound first: the two answers are one answer.
3. **A decision about where a repository-less run's render script comes from** — and the honest statement that it is a **new exception to V7** ("a recipe never names a command"). Today every command a run executes is repository text read at the task's ref; a repository-less run has no ref, so the script has to come from somewhere else (a task-spec field, a bucket object, a catalog recipe with an inline command) and each of those is a different trust story. **That decision is not made here**, and the design's §13.1 says so.

**The order is therefore: project-centric phase 3 → the per-run narrowing → repository-less artifact runs**, and this plan is none of the three.
