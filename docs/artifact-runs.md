# Artifact runs: files in, files out

An artifact run needs fugaro 0.8.0 or later. Design: [design/artifact-runs.md](design/artifact-runs.md).

## 1. What an artifact run is

Every run so far ends in a pull request: the agent changes the checkout, the runner pushes a branch and opens a PR. An **artifact run** ends in a file instead. You attach what it needs (`--attach`), its render step writes the deliverable into the checkout, and the run uploads that file into the runs bucket under the run's own prefix, `runs/<repo-slug>/<run-id>/artifacts/`. You fetch it back with `fugaro artifacts get`.

`mode: artifact` is what makes a run one, and it turns off exactly what `mode: review` turns off, for the same two reasons — there is no pull request, and there is nothing for a git credential to push:

- **No pull request, ever.** No branch is pushed, no PR is opened, updated or marked ready, and readiness is not a concept. `result.json`'s `outcome` is `artifacts` for a run that delivered a set and `none` for one that failed before it could.
- **No git token in the agent's environment.** The runner still fetches the task's ref read-only, exactly as a review run's does; the agent itself gets no credential helper, no `FUGARO_GIT_TOKEN` and no `GH_TOKEN`.
- **`implement`, `first_line`, `bounce` and `roles` are refused** in the recipe — there is no loop and no PR to loop around — and `outputs:` is legal only here (on any other mode the parser refuses it with `outputs: is only for mode: artifact`).
- **The workflow's secrets stay.** A render is the same class of repository text as `commands.build` (it may have to reach a private source), so the render step runs with the run's own environment, secrets included. See [SECURITY.md](../SECURITY.md): that is deliberate, and it is why an artifact recipe must not be something an untrusted author can change.
- **An artifact run is repository-bound.** The recipe and both of the commands the runner executes come from the repository's own `fugaro.yaml` at the task's ref — there is no repository-less artifact run yet (section 5).

## 2. The recipe

A `mode: artifact` recipe names the agent stage, the render step and the deliverable's own block:

```yaml
version: 1
name: sizzle
description: Render a short video from attached screenshots with ffmpeg.
use_when: You have screenshots (or frames) and want a video file back, not a pull request.
mode: artifact
steps:
  - plan: {}       # the agent: writes the render plan; no push, no git token
  - render: {}     # deterministic: runs commands.render in the checkout
outputs:
  globs: ["out/*.mp4"]        # relative to the checkout; no absolute paths, no ..
  max_files: 8
  max_bytes: 268435456        # each artifact, 256 MiB
  max_total_bytes: 2147483648 # the whole set, 2 GiB
  probe: probe                # names commands.probe of the repository's fugaro.yaml
```

- **`steps` is `plan` then `render`, in that order and no other.** `plan` is an agent stage like the `implement` stage of an ordinary recipe — its own stage timeout, its own model budget — whose task says one thing: write a render plan into the checkout. `render` is deterministic: the runner runs `commands.render` from the workflow's `fugaro.yaml` and nothing else calls a model.
- **A `render`-only recipe is legal and costs nothing.** `steps: [render: {}]` is a complete artifact recipe with **zero model dollars**: the runner runs the render step and uploads what it produced, with no agent at all. It is what you want once your own script has settled. The `plan` step exists for the case where the value *is* the agent writing the invocation — the ffmpeg command for this particular set of screenshots and this task.
- **`outputs.globs` is required** and names the artifacts, relative to the checkout (`out/*.mp4`, `dist/*.zip`). An absolute path, a `..` element and a path that leaves the checkout are refused. The set is what matches when the render step ends, not a manifest the step writes.
- **The caps are per artifact and for the set, and each key has one meaning.** `max_files` counts paths (default 8, ceiling 32), `max_bytes` is **each** artifact (default 256 MiB, ceiling 2 GiB), `max_total_bytes` is **the whole set** (default 2 GiB, ceiling 16 GiB). A set over any of them refuses the whole upload with the number, the cap and the count named — nothing is uploaded and the staging directory is emptied.
- **`outputs.probe` is `probe` or `none`, and defaults to `none`.** With `probe`, the runner runs the workflow's `commands.probe` **once, over the whole staged set**, after the render and before the upload. `probe` is the only value that starts a process, and a probe that fails — non-zero, timed out, or unable to start — **refuses the upload entirely**, even though the files are staged and within every cap. That is the point of it: a probe is the only thing that can say a 0-byte render is not a video.
- **Everything the runner executes comes from the repository's `fugaro.yaml`** — `commands.render`, and `commands.probe` when the recipe asks for a probe — the same rule the `check` step already follows: a recipe, a task and the bucket name no command. A render step with no `commands.render`, or `outputs.probe: probe` with no `commands.probe`, is refused at bootstrap, before the first step.

The matching workflow side, which is the ffmpeg case end to end:

```yaml
workflows:
  sizzle:
    image:
      apt: [ffmpeg]
    commands:
      render: |
        set -eu
        mkdir -p out
        ffmpeg -y -framerate 1 -pattern_type glob -i 'inputs/*.png' \
          -c:v libx264 -pix_fmt yuv420p -vf scale=640:-2 out/sizzle.mp4
      probe: |
        set -eu
        ffprobe -v error -select_streams v:0 \
          -show_entries stream=width,height,duration -of csv=p=0 "$FUGARO_OUT_DIR/sizzle.mp4"
        test -s "$FUGARO_OUT_DIR/sizzle.mp4"
    resources: { cpu: 4, memory: 8Gi }
    timeouts:
      total: 40m
      render: 20m
      verify: 20m
```

**Write `commands.render` as the script itself — an inline `|` block, as above.** The runner keeps what it read in memory and never re-reads it, so the command that ran is the string a reviewer read at the task's ref; but the plan stage and the render step are the same container, the same user and the same environment, and a command that names a *file* — `sh .fugaro/render.sh` — runs **that file's working-tree bytes**, which the plan stage could have rewritten. The runner does not promise which copy runs, and in no sense is the step a boundary against the agent: an agent that wanted to run code has had every opportunity already. The inline script is the shape that gets the strong property for free; a command naming a file is permitted, and it is a command naming a file.

**The probe's interface is two environment variables and nothing else.** It runs in the checkout, as the workflow's own command, under `timeouts.verify`:

- `FUGARO_OUT_DIR` — the staging directory's absolute path. It is **flat**: it holds exactly the files that will be uploaded, one per name in the names file, whatever shape the globs had. It exists whenever a probe runs, an empty set included — so a probe may `cd` into it or list it, but must not assume anything is in it.
- `FUGARO_ARTIFACTS_FILE` — the path of a file holding those names, one per line, in upload order. Each name is the relative, sanitised name, and it is both the path under `FUGARO_OUT_DIR` and the object key's leaf under `artifacts/`.

A probe is one invocation for the whole set, never one per artifact: the runner never interpolates a name of its own into a command line.

**Timeouts and memory.** `timeouts.render` bounds the render step alone. `timeouts.verify` bounds `commands.probe` (a probe is a check, not a render), and the run's finalize reserve can cut either of them short — when it does, the run's reason says the reserve did it and not a timeout that still had time left. The staging copy and the render's own temp files are both on the memory-backed filesystem, so give the workflow enough: as a rule, `2 × outputs.max_total_bytes + inputs.max_total_bytes` must fit inside `resources.memory`, next to what the render itself needs.

## 3. Files in: `fugaro run --attach PATH`

`--attach PATH` is repeatable, and it is how a run is given a screenshot, a frame or any other input file:

```bash
fugaro run --workflow sizzle --recipe sizzle \
  --attach a.png --attach b.png --attach c.png "render a 6-second sizzle from these"
```

- **The files are uploaded to `runs/<repo-slug>/<run-id>/inputs/<name>`** before the run starts, each with its `{name, bytes, sha256}` recorded in the task spec, and the runner downloads them into the checkout's `inputs/` **before the first step**, verifying every one against its recorded sha256 — it does not trust the bucket's copy. It enforces the same caps and the same name refusals again, because the task spec is text a launcher wrote.
- **The caps are the workflow's `inputs:` block**: `max_files` (default 16, ceiling 64), `max_bytes` **per file** (default 256 MiB, ceiling 1 GiB) and `max_total_bytes` **for the set** (default 1 GiB, ceiling 4 GiB). The launcher refuses a set over any of the three **before the first byte is written**, naming the number, the cap and the count.
- **An existing run id is refused outright.** A plain launch may adopt a stored, byte-identical task; a launch carrying attachments may not, and it also refuses a run id that already has a `launch.json` — whether or not a task was stored — because an attachment is uploaded before a run starts and a run that already started is not one this launch can give files to. The refusal happens before the caps and before the first byte, so a mistyped `--run-id` costs nothing and changes nothing.
- **`--attach` is refused unless the launch resolves to an artifact recipe**, naming the recipe and its mode — an attachment is only meaningful to a run that will produce files rather than a pull request. A recipe of another mode is refused for that reason, and so is a follow-up: `fugaro run --pr N` continues a pull request, and an artifact run opens none. A task that already carries `mode: artifact` needs none of this; it is allowed on the stamp alone.
- **Passing no `--recipe` at all is allowed only when this checkout resolves an artifact recipe**, and there are two halves to that. First, the CLI has to resolve *something*: if the task carries no recipe and this directory is not a checkout whose `fugaro.yaml` the CLI can read for that repository, the recipe is the **runner's** to choose (`agent.recipe` at the ref, else the catalog default) and the launch is refused — the launcher cannot prove the mode the runner will pick, and being unable to prove it is not a reason to upload the files anyway (`runnerChooses`, `internal/cli/run.go`). Run from the repository's own checkout. Second, the recipe it resolves — exactly as the runner does (`recipeForLaunch`: `agent.recipe` in this checkout, else the default) — must be `mode: artifact`; a resolved recipe of any other mode is refused with its name and its mode. `TestAttachRefusesARecipeTheRunnerChooses` and `TestAttachAllowsAnArtifactRecipeWithNoModeStamped` (`internal/cli/run_attach_test.go`) are the two sides of it.
- **Names are checked as a set**, because the files land in one directory. An attachment's name is the base name of the path, and the only rule for it is `task.InputNameProblem` (`internal/task/task.go`), which the runner applies again when it writes the files back — the launcher's rule and the runner's are one function, not two lists. It refuses a name that is **empty**, **nothing but whitespace**, **longer than 200 bytes**, **holds a NUL, a CR or an LF**, is an **absolute path**, or has an **empty, `.` or `..` path element**. It does *not* refuse a leading dot (an input is written under `inputs/`, which is never a checkout root and is excluded from git, so `.hidden.png` is a name this takes — unlike an *artifact* name on the way back out, whose own rule refuses one) and it does not refuse control characters generally, only those three. On top of the name rule, the set must have no duplicate — the same name twice, or two names `runner.ArtifactCollisionKey` folds together (`A.png` against `a.png`) — because the runner writes them into one directory, where that is one name, and the second upload would replace the first. Each path is opened **once**, with `O_NOFOLLOW` — a symlink is refused rather than followed, and a directory, a device, a FIFO or a socket is refused too — and the open handle is what was judged and what is uploaded, so a name swapped for a FIFO, a device or a file grown past the cap in the meantime changes nothing.
- **`inputs/` is excluded from git** (the runner writes it into `.git/info/exclude`), because an artifact run must never commit a screenshot. Under `plan` the agent sees the directory at `inputs/`, which is also `FUGARO_INPUTS_DIR` in its environment.
- **One line is printed on the `warn` writer, once per launch, only when `--attach` carried a file,** naming where the file may go: every allowed provider that claims one of the run's models plus the run's own auth route when a model is unclaimed. It is not a gate — attaching a file is sending it to that provider under the same policy the model already runs under, and the run's record carries the inputs' names and hashes so "what did we send" has an answer.

> **Attachments are as readable as the run.** An attached file is uploaded to the runs bucket, where *every launcher and every operator of this installation can read it*, and where every run of the same repository can read it too (the job's account is scoped to the repository's prefix, not to one run: `SECURITY.md`). Project Owners and Editors bypass the bucket's policy entirely. A screenshot is not a diff: it can show a token in a terminal, an API key in a browser tab, a customer's name, someone's face. Do not attach anything you would not paste into a pull request on a repository every launcher can read. Attachments also expire with the run (90 days by default), and so do the artifacts.

## 4. What the run records, and how to read it back

A finished artifact run's `result.json` carries the deliverable's whole account of itself:

- **`render`** — `command` (redacted, as everything the runner prints is), `command_sha256` (the hash of the exact string it ran, before redaction, so a reader can check which command ran without the record carrying a secret), `tree: "working"` (the render ran in the checkout the plan stage had, not a pristine copy of the ref), `exit_code`, `duration_s` and `timed_out`.
- **`inputs`** — one `{name, bytes, sha256}` per attached file, once the files it names are actually in the checkout.
- **`artifacts`** — one entry per uploaded file, in name order: `name`, `bytes`, `sha256`, `content_type` and the probe's verdict. `bytes` and `sha256` are what the upload wrote, so they are the bytes actually in the bucket; `content_type` is a **recorded hint** — the name's extension first, then a sniff of the first bytes when the extension names nothing (`artifactContentType`), never a claim about the content, and no reader trusts it. It is also the string the upload hands the store as the object's own `Content-Type`.
- **`artifacts[].probe`** — `{ran, exit_code}`. A recipe that asks for no probe gets `ran: false, exit_code: -1` — "no probe ran", said out loud rather than inferred from an absent field, and `-1` rather than `0` so that a reader skimming an exit code does not read "skipped" as "passed". A probe that *fails* refuses the upload, so a failed run carries no `artifacts` at all: what you read about it is the run's reason and the report's log tail.

Two commands read it back, and both read `result.json` and nothing else — `ls` is the record, `get` is the object:

```bash
fugaro artifacts ls <run-id>                 # the record's own table
fugaro artifacts ls <run-id> --json           # the artifact list, verbatim
fugaro artifacts get <run-id>                 # every artifact, verified
fugaro artifacts get <run-id> sizzle.mp4 --out ~/Downloads
```

- **`fugaro artifacts ls RUN [--repo OWNER/NAME] [--json]`** prints name, size, short sha256, recorded content type and the probe's verdict. **It lists the record, not the bucket**: a name the record holds and the bucket no longer has is a row plus a `get` that says exactly that.
- **`fugaro artifacts get RUN [NAME] [--repo OWNER/NAME] [--out DIR] [--force]`** downloads one artifact, or all of them with no `NAME`, verifying each one's size and sha256 against the record **as the bytes arrive**. It creates a fresh directory — `./<run-id>-artifacts/`, with `--out` naming that directory's parent — never the directory you are standing in, so a crafted name in a tampered record cannot drop a `.git/hooks/` or `.claude/` file into a checkout you are working in. A mismatch is an error naming both hashes and leaves nothing behind, not even the file the download streamed into. An existing file needs `--force`; a name in the record that is absolute, walks out of the directory, begins with a dot or holds a control character is refused, never flattened. **Nothing downloaded is ever opened, executed or unpacked**: the content type, the size and the sha256 are printed, and that is all.
- `fugaro runs ls` gains an `ARTIFACTS` column once any listed run has artifacts (the column's own rule: it appears only when it would say something), and `fugaro diagnose` names one bucket path per artifact the record lists.
- **Retention is the run's own.** Everything under `runs/` is deleted at `runs_days`, **90 days by default**, so an artifact lives exactly as long as the record it belongs to — `result.json`, the report, the transcripts and the artifact disappear in the same sweep. Keep what you want before then: `fugaro artifacts get` writes it to your machine with its sha256 verified, and `gcloud storage cp gs://…` copies it anywhere if you prefer. An artifact registry, a cross-run index and a separate retention window are not built (section 5).
- **The hash proves the bytes came back intact; it does not prove who wrote them.** It and the artifact live in the same repository-scoped prefix, so anything that can rewrite one can rewrite the other — including another run of the same repository. The recorded sha256 is **integrity in transit, not tamper evidence** (design §8.3).

## 5. What is not built

Named here so nothing above is read as promising more than it does. Each is a decision with its cost, and the design's §13 and §14 carry them.

- **No GPUs.** A render runs on the run's CPU. A GPU backend is an open question, not a promise (design §5.4).
- **No compute inside the cap.** The budget's caps count **model dollars only**; a render is compute, and what bounds it is the run's own clock (`timeouts.total`, and the Cloud Run task timeout of `total + 2m`) and the workflow's `resources` — never `budget.per_run_usd`. `result.json`'s `cost.compute_usd` is an estimate, reported and never charged. Give an artifact workflow 8 vCPU and a two-hour total and a command that never finishes, and you pay for two hours of 8 vCPU; the caps will not stop it ([SECURITY.md](../SECURITY.md), design §6).
- **No signed URLs and no signed manifest**, and therefore no tamper evidence (design §8.3, §13): the recorded sha256 is the mechanism there is.
- **No artifact registry, no manifest of many runs, no cross-run index.** One run, one artifact set, one record.
- **No general `outputs:` on implement recipes**, and no "the run opens a PR for the script change it used" (design §14).
- **No local-only artifact mode.** `fugaro exec` with a `file://` bucket gets the mechanism for free; there is deliberately no second local path (design §14).
- **No repository-less artifact runs *yet*.** A run whose recipe, render script and assets live under a profile rather than in a checkout is the **recorded end state**, not a rejected idea — the design's [§13.1](design/artifact-runs.md#131-the-repository-less-end-state--recorded-not-built-here) keeps it as the destination, and the plan's own "The repository-less end state" section records why it is not in this one. It needs three things that do not exist today, in this order: **project-centric phase 3's shared project jobs** (a run with no repository has no per-repository job to bind to), **a per-run narrowing** — a per-run prefix condition or a per-run account, because on a shared project job one run's inputs, artifacts and record would be readable *and overwritable* by every run on the profile — and **a decision about where a repository-less run's render script comes from**, which is a new exception to the rule that a recipe names no command. Until then an artifact-producing run stays on the plain per-repository Cloud Run backend (design §8.5, D11).
