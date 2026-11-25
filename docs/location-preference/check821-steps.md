# Check 821 — the owner's steps (`fugaro update` on the dogfood installation, with a run in flight)

**Status: written by task LP13 of the location-preference plan; run by you, after the release that carries LP4 and
LP12. NOT RUN.** No agent runs any step of it: it converges a real installation and launches a real run.

The check is `scripts/live-checks/check-821.sh`, one command, run from the checkout of that installation. Its
register section is `docs/gcp-live-checklist.md`, "Check 821", and that is where the block you paste back is
recorded. This file is the command and what you need.

It measures four things:

- **the locations record** (**LP12**, **D3087**) on the dogfood installation, read through its only reader,
  `fugaro doctor --json`'s locations row: after the update it must read what that installation really has —
  Firestore `us-east5`, the Realtime Database `us-central1`, **automatic** replication for the secret containers,
  and a **`global`** run log bucket.
- **U9** — whether the pinned provider's `ignore_changes` holds: `fugaro update --plan-only --json` straight after
  the converge must plan **no change** to any root. A plan that creates, updates, deletes or replaces anything is
  U9 answering no, and the row names the counts.
- **A run in flight** (`docs/design/update-in-flight.md`, the owner's requirement of 2026-11-09): a run launched
  **before** the update is followed across it and must finish with its record whole. That is the one reading a test
  cannot take.
- **Nothing is asked** (**D3090**, `docs/no-prompt-policy` **T7**): the update runs with its stdin on `/dev/null`
  and every line it prints is read for the shapes a question leaves behind. One is a finding.

## What you need

- **The checkout of the dogfood installation** (`--repo`, default the working directory), with a valid
  `fugaro.yaml`: the check runs `fugaro doctor`, the update and the run from there. The installation's region must
  read **`us-east5`** in doctor — the four strings above are that installation's own, and a checkout of another
  installation **stops** the check by name (a new EU installation is Check 820's business, not this one's).
- **The installed release**: `fugaro` on PATH. No `gcloud` and no `curl` — every reading here is the CLI's own —
  and `bash`, `mktemp` and `python3` (every `--json` document is read with it).
- **The will to converge the installation**: this is a real `fugaro update`, so its Terraform applies run. It
  asks you nothing and waits on no input; its output is captured and printed in the block's rows.
- **Wall clock and the run's budget**: the update under `--deadline` (default 1800s), the run's model calls
  (**billable**, the installation's budget governs them), and the run followed for up to `--wait` (default 3600s)
  after it was seen `running` (up to `--start-wait`, default 900s).

## The step

1. **The dry run first.** It prints the plan and **calls nothing** — no `fugaro` at all.

   ```sh
   bash scripts/live-checks/check-821.sh --dry-run
   ```

2. **The check itself** — the one command, from the checkout:

   ```sh
   bash scripts/live-checks/check-821.sh
   ```

   Progress goes to stderr; one `CHECK 821 RESULT` block goes to stdout, at the end. **Paste that whole block
   back to the coordinator** and nothing else: it carries the installation's name, the run id, the counts each
   root plans, the four locations strings and the update's own last line — and no credential, because the check
   holds none and reads none.

   Knobs, each with the default that makes an unset shell run it as written: `--project <name>` (default: the one
   the checkout's own doctor answers), `--repo <checkout>`, `--deadline <seconds>` (1800), `--start-wait <seconds>`
   (900), `--wait <seconds>` (3600), `CHECK821_POLL_SECONDS` (20), `--no-run` (measure everything but the run in
   flight, whose row then reads UNVERIFIED), `CHECK821_RUN_TITLE`, `CHECK821_KEEP_SCRATCH`.

## What each row means

- **`premise`** — the checkout belongs to an installation, the installation is the dogfood one (region
  `us-east5`), its `fugaro.yaml` is valid, and its run history is readable. A premise that does not hold **stops**
  the check (exit 2) before it launches anything.
- **`run`** — the throwaway run was launched with `fugaro run` and its record was seen `running`: that is what
  makes the update below an update **with a run in flight**. Its task asks for a report of the runner's stages and
  says to change nothing, commit nothing and open no pull request — it exists to be in flight, not to land a
  change.
- **`update`** — the real `fugaro update --project <name>`, stdin on `/dev/null`, streams captured, under
  `--deadline` (TERM, then KILL). Exit `0` passes. Exit `1` is a refusal **or** a step left for you: the row tells
  them apart by the CLI's own sentence `N step(s) left for you` — a hand-off is UNVERIFIED and names the steps,
  because only you can run them — and a refusal without that sentence is FAIL. Exit `2` (a cloud failure) and a
  timeout are FAIL and UNVERIFIED respectively.
- **`asked`** — every line the update printed, read for the shapes a question leaves behind: `[y/N]` in any
  casing, `(y/n)`, `(yes/no)`, a `to confirm:` left open, a `press enter` / `hit return` shape, and a line left
  ending on a question mark. One is a FAIL (**D3090**, **T7**).
- **`record`** — `fugaro doctor --json`'s locations row after the converge: its Firestore database in `us-east5`,
  its Realtime Database in `us-central1`, its secret containers replicated with Google's **automatic** replication
  (which today means the United States, and which is what that installation already has — nothing inside a place
  moves, **R5**), and its run log bucket in `global`. The row names each string that is missing.
- **`plan`** — `fugaro update --plan-only --json` immediately after the converge: **every count zero**
  (creates, updates, deletes, replaces, imports). A plan that would change something is FAIL (**U9**); a plan that
  only imports is UNVERIFIED — nothing in the cloud changes, but the plan is not empty.
- **`inflight`** — the run launched in the `run` row, followed with `fugaro runs ls --json` until its record is
  terminal: it must **still be there** (a record gone after an update is the finding this row exists for), it must
  have been seen `running` when the update started (otherwise UNVERIFIED — nothing was in flight), and
  `succeeded` is the reading the row wants, with its branch, push, pull request and outcome quoted. Any other
  terminal status leaves the row UNVERIFIED: its record is whole, but a run that did not finish is not the reading
  wanted. A run still running at `--wait` is UNVERIFIED too — raise `--wait` and run the check again.
- **`restore`** — the check's scratch directory is gone (`CHECK821_KEEP_SCRATCH` keeps it on purpose). The
  update's own writes to the installation **stay**, because that is what an update is; the run's branch and pull
  request, if it made one, stay on the repository, because this check holds no credential to remove them.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `2`; a row the check could
not measure reads `UNVERIFIED` and the verdict is `INCOMPLETE` (exit `3`). The stops worth knowing by name:

- **The checkout's doctor named no installation**, you named another one with `--project`, or the installation is
  not in `us-east5`.
- **The checkout holds no `fugaro.yaml`**, or `--repo` is not a directory.
- **A number flag is not a number** (`--deadline`, `--start-wait`, `--wait`) — stopped before the update runs.
- **This shell is tracing** (`bash -x`, or `xtrace` in `SHELLOPTS`) — refused before anything is called.
- **A coding agent's session** (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_REMOTE`, `CURSOR_AGENT` or
  `AI_AGENT` in the environment) — refused on sight, with the command to run yourself.
- **A tool that is not on PATH** — the check says which one before it calls anything.

## What is left behind

The converge itself — the installation's own config and cloud state, which is what you asked for. The launched run
is asked to change nothing, so it pushes no branch and opens no pull request; if it did open one, it is yours to
settle. Nothing else: the scratch directory is removed, and the check writes no cloud resource, no record and no
node of its own.

**No agent runs any step of it.**
