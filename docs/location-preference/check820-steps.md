# Check 820 — the owner's steps (a first installation in the EU)

**Status: written by task LP13 of the location-preference plan; run by you, after the release that carries LP4.
NOT RUN.** No agent runs any step of it: it creates an installation in a project of yours, and what it measures is
what the real APIs did with the places it asked for. A **FAIL** means the location preference did not hold on a
first installation — a resource a `--location eu` setup placed somewhere else, or a run that could not launch from
it — and it comes back to you as a decision, never as something to work around.

The check is `scripts/live-checks/check-820.sh`, one command. Its register section is
`docs/gcp-live-checklist.md`, "Check 820", and that is where the block you paste back is recorded; read it for what
each row settles (the reconciliation's **U1**, **U2**, **U6** and **U8**). This file is the command and what you
need.

## What you need

- **A scratch GCP project of your own** — one that exists and holds nothing of Fugaro's: no `fugaro-runs-*` and
  no `fugaro-tfstate-*` bucket. The check refuses anything else before it calls `fugaro`, because a project that
  already has an installation would converge *that* one instead of placing a first. Billing is the setup's own
  business: it links it at your terminal.
- **The installed release** (not a checkout build): `fugaro` on PATH, authenticated with `gcloud` as you. The
  script asks for read-only `gcloud` reads (`projects describe`, `storage buckets`, `firestore databases`,
  `artifacts repositories`, `secrets`, `logging buckets`), one `gcloud auth print-access-token` and `curl`, and it
  runs the real `fugaro setup` and one real `fugaro run`.
- **A real terminal**, because `fugaro setup` is the one command the check hands over to you rather than
  capturing: a first installation creates the project's resources, links billing and places a **permanent**
  Firestore database, and those confirmations are a person's. This is the only row of the check that is not
  unattended.
- **A checkout the scratch installation is onboarded to** (`--repo`, default the working directory): the run is
  launched from it.
- **No local config for the name** the installation is created as (`--name`, default `chk820`): if
  `${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/<name>.yaml` exists, the check stops — it would converge that
  installation rather than make a first one. Name another with `--name`.
- `bash`, `mktemp`, `python3`, `gcloud`, `curl` and `fugaro` on PATH.
- **Wall clock and the installation's own budget**: the setup is minutes, the run is an ordinary run (its model
  calls are real and the installation's budget governs them), and the scratch project's resources cost what they
  cost. The project is yours to delete afterwards.

## The step

1. **The dry run first.** It prints every command a real run would run and it **calls nothing at all** — no
   `gcloud`, no `fugaro`, no `curl` — so it is safe anywhere, and it is the mode to try if you are not sure the
   shell is the right one.

   ```sh
   bash scripts/live-checks/check-820.sh <project> --dry-run
   ```

2. **The check itself** — the one command, from anywhere:

   ```sh
   bash scripts/live-checks/check-820.sh <your scratch project> [--name chk820] [--repo <checkout>]
   ```

   Progress goes to stderr; one `CHECK 820 RESULT` block goes to stdout, at the end. **Paste that whole block
   back to the coordinator** and nothing else: it carries locations, names, counts and one run id — and no secret,
   because the check reads none (the run's task file says nothing secret either).

## What each row means

- **`premise`** — the project exists, is named what you passed, holds no Fugaro bucket, and this machine has no
  local config for the name. A premise that does not hold **stops** the check (exit 2) before `fugaro` is called
  at all, with the reason on one line.
- **`setup`** — `fugaro setup --location eu --gcp-project <project> --name <name> --rigor poc --bake-repos` exited
  `0` at your terminal. Everything below reads back what *that* run placed, so a setup that did not converge is
  the whole check unanswered.
- **`record`** — the locations row of `fugaro doctor --json` (the record's only reader, **R5**, written by
  **LP12/D3087**): its Firestore database in `europe-west1`, its Realtime Database in `europe-west1`, its secret
  containers replicated in `europe-west1`, its run log bucket in `europe-west1`. The row names what is missing
  from doctor's own sentence.
- **`firestore`** — the `(default)` database's own `locationId`: `europe-west1`. This is **U1** — whether a project
  whose default resource location is set places a `(default)` database in the installation's own region — answered
  by the cloud rather than predicted.
- **`rtdb`** — the default Realtime Database instance's location, read across every location, is `europe-west1`,
  and the instance's database URL carries the EU host. This is **U2**.
- **`buckets`** — every bucket of the project reads in the EU (`europe-west1` or Google's `EU` multi-region). A
  bucket that could not be read leaves the row FAIL with the names, because an unread bucket is not one in the EU.
- **`registry`** — every Artifact Registry repository (`base`, `history`) is in `europe-west1`; a repository's own
  name carries its location, so this row is read from the name.
- **`secrets`** — every container carrying `fugaro=managed` is **user-managed** with its replica in
  `europe-west1`, and **none** is `automatic`: automatic replication names no region and today means the United
  States (**D3092**).
- **`logbucket`** — the installation's own log bucket (`fugaro`) is in `europe-west1`. An installation set up with
  `--no-log-isolation` has none of its own, and the row reads UNVERIFIED rather than failing.
- **`hosts`** — **U8**: what each of the three Realtime Database hosts (the legacy one, `europe-west1`'s and
  `asia-southeast1`'s) answers for a database that lives in the EU. The answers are **recorded**, not predicted:
  only an answer the key service could read as neither an installation nor an absence of one is a FAIL, because a
  database in Europe that host does not find is an installation the vendor's service cannot see (**LP15**).
- **`run`** — `fugaro run` launched, `fugaro runs ls` names the run in the installation's runs bucket (the record
  reached it), and `fugaro budget show` reports money leased for the project (the budget backend in
  `europe-west1` answered). The run is what says a job in the region read the region's own secret container with
  no other change — that is **U6**.
- **`restore`** — the check's scratch directory is gone, and the one command that deletes the scratch project is
  named. The check deletes nothing itself.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `2`; a row the check could
not measure reads `UNVERIFIED` and the verdict is `INCOMPLETE` (exit `3`). The stops worth knowing by name:

- **The project cannot be read**, is not named what you passed, or already holds Fugaro buckets.
- **A local config exists for the name** (`--name` another one).
- **This shell is tracing** (`bash -x`, or `xtrace` in `SHELLOPTS`) — refused before anything is called.
- **A coding agent's session** (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_REMOTE`, `CURSOR_AGENT` or
  `AI_AGENT` in the environment) — refused on sight, with the command to run yourself.
- **A tool that is not on PATH** — the check says which one before it calls anything.
- **No project id** was given (or the usage, with `--help`).

## What is left behind

The scratch project and everything the setup created in it — the check deletes nothing, and the `restore` row
names `gcloud projects delete <project> --quiet` for you. The one run it launched is an ordinary run of that
installation — it is asked to change nothing, so it pushes no branch and opens no pull request — and its spend is
on the installation's budget.

**No agent runs any step of it.**
