# Check 920 (and Check 921, `--catalog`) — the owner's steps (the workflow-secret class off a live installation)

**Status: written by task IS12 of the image-secrets plan; run by the owner, after the release that carries IS1 to
IS8. NOT RUN.** No agent runs any step of it, and no agent has: what it measures is a live installation, real runs
and the credentials service's own answers about them. A **FAIL** means the workflow-secret class is not in place on
that installation — the record the service serves a run from, the flip that took the mount off the run jobs, a
repository the allow-list refuses, or the file a `file: true` secret arrives as — and it comes back to the owner as
a decision, never as something to work around.

The check is `scripts/live-checks/check920.sh` — one script, two modes: without an argument it runs **Check 920**,
with `--catalog` it runs **Check 921** (the catalog half of IS8). The register's sections are
`docs/gcp-live-checklist.md`, "Check 920" and "Check 921", and that is where the blocks you paste back are
recorded; read them for what each row settles. This file is the commands and what you need.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are hard-coded in the
  script and never read from `gcloud config`; `fugaro doctor --project fugaro --all` must print its
  `project: … (GCP fugaro-dev,` line (the check reads that **line**, not doctor's exit status, which is `1`
  whenever any check of its own failed), and `fugaro version` must not be a development build — a checkout build
  resolves no released credentials-service image to deploy, and this whole reading is about what that image does.
  A development build is recorded as a FAILed `fugaro-version` row rather than a stop. Never `belong`.
- **The release under test applied to the repositories you name** (`fugaro update --repo` for the flip's IS4/IS5
  half): the run jobs must have stopped mounting a workflow secret, and the project record
  `creds/projectsecrets` (IS3) must name the secrets below.
- **Two sandbox repositories of that installation** — `CHECK920_REPO` (the one under test) and
  `CHECK920_UNLISTED_REPO` (the one the secret's allow-list does **not** admit), both `owner/name`, sandbox
  repositories only, and they must differ. The first must be a **vertex** repository (`agent.auth: vertex`): U1's
  reading is a run that holds a run identity and still fetches.
- **A checkout of the first one**, `CHECK920_CHECKOUT`. The review half is `fugaro review --pr N`, which resolves
  the repository from its own checkout's origin, so the check runs it there — and the origin must name the
  repository under test **exactly** (`owner/name`): a checkout of another one stops the check before it calls
  anything, because a review launched there would be a real run of *that* repository's pull request #N.
- **Two workflow secrets, declared by the first repository.** In its own `fugaro.yaml`:

  ```yaml
  agent:
    auth: vertex
  workflows:
    go:
      commands:
        build: |
          set -e
          <this repository's build>
        test: |
          set -e
          test -n "$DEPLOY_TOKEN" || { echo "DEPLOY_TOKEN is empty"; exit 1; }
          test -f "$DEPLOY_CERT" || { echo "DEPLOY_CERT is absent"; exit 1; }
          test "$(stat -c '%a' "$DEPLOY_CERT")" = 400 || { echo "DEPLOY_CERT is not 0400"; exit 1; }
          test -s "$DEPLOY_CERT" || { echo "DEPLOY_CERT is empty"; exit 1; }
      secrets:
        - { name: <CHECK920_SECRET>,      env: DEPLOY_TOKEN }
        - { name: <CHECK920_FILE_SECRET>, env: DEPLOY_CERT, file: true }
  ```

  The `test:` half is the verify stage's own command, and **failing on any of those four conditions is the whole
  point of it**: the `run-verify` row reads the run's own verify record and nothing else, so a repository whose
  verify passes without the file would not tell a `0400` file that arrived from one that did not. `file: true` is
  what makes the second secret a file at the path the variable names (**D3327**, **G3**) — the environment secret
  is the same class by the other route, and the two rows read apart.
- **The secrets stored and allowed.** `fugaro secrets set <name>` (it asks for the value at your own terminal and
  never prints it) and `fugaro secrets allow <name> <owner>/<name>` for the repository under test — the second
  repository is deliberately **not** listed. A name with no container, or one whose allow-list lists nothing, is
  still served to nobody: the `record` row reads the project record and says so by name.
- **The second repository declares the same secret, and is not allowed it.** It may declare it and fail: that is
  the row. Its own `fugaro.yaml` names `<CHECK920_FILE_SECRET>` or `<CHECK920_SECRET>` in some workflow's
  `secrets:`, and its allow-list entry does not list it.
- `bash`, `mktemp`, `python3`, `gcloud`, `curl` and `fugaro` on PATH, with `gcloud` authenticated as you. It is
  asked for read-only reads — `run jobs list`, one job and one execution, Cloud Logging (the service's own lines),
  each container's IAM policy, the build list and one build, and the runs bucket (`gcloud storage cat` of one run's
  `result.json`) — and, with `gcloud auth print-access-token`, one GET of the project record out of the
  installation's own Realtime Database (`budget.rtdb_url`, or the file `CHECK920_CONFIG` names). The record is
  **read and never written**.
- **Wall clock and the installation's budget**: two throwaway runs of the sandbox repositories you name (each
  writes one line to `CHK920.md`, commits it, pushes a branch and opens a pull request — about forty minutes
  apiece) and one **review** of the first run's pull request, which is a run of its own. Every model call of those
  three is answered by the credentials service, which is the subject. `CHECK920_RUN_SECONDS` (default `2400`) is
  how long each run is waited for.
- **For `--catalog`: one catalog build that ran as the build identity**, since IS8. Nothing else to prepare —
  `CHECK920_CATALOG_BUILD` names one when you want a specific build rather than the newest.

## The steps

1. **The dry run first.** It prints every command a real run would run and it **calls nothing at all** — no
   `gcloud`, no `fugaro`, no `curl` — so it is safe on a machine with no credentials, and it is the mode to try if
   you are not sure your shell is the right one.

   ```sh
   CHECK920_REPO=<the repository under test> CHECK920_CHECKOUT=<its checkout> \
   CHECK920_UNLISTED_REPO=<the repository the allow-list does not admit> \
   CHECK920_SECRET=<the environment secret> CHECK920_FILE_SECRET=<the file secret> \
     bash scripts/live-checks/check920.sh --dry-run
   ```

   What it would do, in order: check that this is the `fugaro-dev` installation and that `fugaro version` is not a
   development build; read the local config; mint one access token (into a `0600` curl config file in the run's own
   `0700` scratch directory); read `creds/projectsecrets.json` out of the installation's own database; read
   `gcloud run jobs list` for the installation; launch one run in the repository under test and poll `fugaro runs
   ls` for its row; read the job and the execution that run ran on; read the run's own `result.json` out of the
   runs bucket; read the service's `credsvc: audit` lines for the run; launch one run in the repository the
   allow-list does not admit; read that run's refusal; and run `fugaro review --pr <the pull request>` from the
   checkout, then read its job, its execution and the service's lines for it.

2. **The run itself** — the same command without `--dry-run`:

   ```sh
   CHECK920_REPO=<the repository under test> CHECK920_CHECKOUT=<its checkout> \
   CHECK920_UNLISTED_REPO=<the repository the allow-list does not admit> \
   CHECK920_SECRET=<the environment secret> CHECK920_FILE_SECRET=<the file secret> \
     bash scripts/live-checks/check920.sh
   ```

   Progress goes to stderr; one `CHECK 920 RESULT` block goes to stdout, at the end. **Paste that whole block back
   to the coordinator** and nothing else: it carries the record's names, the run ids, the pull requests, the
   execution names, the containers the audit lines name and the refusal's sentence — and **no workflow secret's
   value**: the value is never read by this check at all (the record holds names, and the service's lines are
   reduced to a short plain-word shape before they are printed), and credential-shaped text in any message is
   redacted besides.

3. **And the catalog half** — Check 921, which launches nothing, reviews nothing and reads no checkout, so it runs
   from any directory (none of the knobs above is needed):

   ```sh
   bash scripts/live-checks/check920.sh --catalog --dry-run   # calls nothing
   bash scripts/live-checks/check920.sh --catalog
   ```

   Both modes print the same kind of block; the two carry **different** rows, so one block is never read as the
   other (`MODE:` says which). The **two runs** the check launches are named from today's UTC date and time plus
   `-0920` (the repository under test) and `-0922` (the repository not allowed the secret); set `CHECK920_RUN_ID`
   to reuse a base. The **review's** id is not one of them: `fugaro review` has no flag to set a run id, it mints
   its own, and the check reads it out of the launch document and names it in the block.

## What each row means

- **`project-region`, `fugaro-version`, `xtrace`, `scratch`, `config`, `token`** — the guards and the two
  boilerplate rows. `fugaro-version` FAILs on a development build (see above). `config` says which local config
  was read and what it named; `token` says only that an access token was written to a `0600` file inside the
  `0700` scratch directory, never printed and never on an argv.
- **`record`** — `creds/projectsecrets.json`, read out of the installation's own database: the node IS3 writes and
  the only thing that says which names are **workflow secrets** at all. The `fugaro_p_` prefix is **also** the
  dynamic-delivery grammar for an admin-shared project credential (reconciliation **F10**), so a mount is this
  check's business only when the record names it. The row requires the record to be there, to name at least one
  workflow secret, and to name **both** secrets under test; a record that is absent, unreadable or short of them
  leaves this row **UNVERIFIED** with the reason, and every row below still runs.
- **`jobs`** — `gcloud run jobs list`: **no** deployed run job of the installation mounts a container the record
  names (the flip, **G6**) — in **any** spelling a deployment mounts one under: the delivery grammar's
  `fugaro_p_<name>` **and** the project copy `fugaro-project-fugaro-<name>-<16 hex>` (`gcp.ProjectSecretID`) a job
  mounted **before** the flip, which is what **G4** keeps alive after the release and what **U4** asks about, so a
  row keyed on the prefix alone would pass an installation that still mounts one. A job mounting a `fugaro_p_`
  container the record does **not** name is a shared credential and the row says so rather than failing; a mount
  whose class could not be settled (the record was not read) leaves the row UNVERIFIED, because a name's prefix
  alone is not a reading.
- **`run-one`** — the run of the **vertex** repository succeeded, with a pull request. A run that could not finish
  is the fetch failing, and a run with no pull request leaves the review row unmeasured.
- **`run-mounts`** — the job the run ran on **and** the execution it ran on: neither feeds any variable from a
  container of `<CHECK920_SECRET>` or `<CHECK920_FILE_SECRET>` in any spelling (the delivery grammar's or the
  project copy's), and neither runs as the installation's build identity. The execution's own account is read as well as the job template's, because Cloud Run overrides it. The
  two together with `run-one` are the whole of "the value reached the run from the service and from nowhere else".
- **`run-verify`** — the run's own last verify record out of its `result.json`: `passed`, on a **clean** tree. The
  steps' `test:` command fails unless the file secret arrived as a **`0400` non-empty file** at the path its
  variable names, so that record passing **is** the file's arrival (**D3327**, **G3**). It is read only on a run
  that succeeded: a failed run's verify says nothing about the file. The record is addressed at
  `runs/<slug>/<run id>/result.json`, where the slug is the `fugaro_repo` label of the repository's own job (the
  repository's name with every run of characters outside `[a-z0-9]` turned into one `-`, then 16 hex): a repository
  whose job carries no such label leaves the row UNVERIFIED, because the path cannot be spelled from the name.
- **`audited`** — the credentials service's own lines for that run, under the purpose `workflow-secret`: **exactly
  one line per fetch**, each naming one of the two containers. One fetch, one line (**G18**: a line is a name,
  never a value); a second line for the same container, a missing one, or one naming a container the check cannot
  account for is a FAIL. A `gcloud logging read` that exited non-zero is **not** the service's silence: the row
  reads UNVERIFIED with the reason, and the check retries the read for `CHECK920_LOG_WAIT_SECONDS` first, because
  ingestion lags the request.
- **`review`** — a real `fugaro review --pr N` run of its own, launched from the checkout: it finished
  `succeeded`, its job and execution carry **neither** container, and the service logged **no** `workflow-secret`
  act for it. A review reads an untrusted checkout and prompt, so it is served no secret and holds none — the file
  is **absent in it** (**G3**, **R5**). The review's own git and model calls are served by the service too and
  have lines of their own; a row that counted them would read a served review where none was.
- **`unlisted`** — the run in the repository the allow-list does not admit did **not** succeed, and the service's
  refusal names the fix (`fugaro secrets allow NAME owner/repo`). A refusal without the fix is a FAIL (**G1**,
  **D3328**); a failure with no refusal line at all is UNVERIFIED — its own lines say why it failed, and nothing
  there says the service refused it.
- **`restore`, `scratch-removed`** — the scratch directory, with the access token's curl config file in it, is
  gone. Nothing else is created: this check makes no cloud resource and writes no record.
- **`build-identity`** (**921**) — `fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com` exists: the ONE
  account a catalog build runs as, so what it may read is what a catalog build may read.
- **`identity-accessors`** (**921**) — the build identity holds no `secretmanager.secretAccessor` (or `admin`) on
  **any** container of the project and no such role at the project level (**D3330**). A policy that could not be
  read is UNVERIFIED, and so is a project holding more containers than `CHECK920_SECRET_LIMIT` (default `200`) —
  an unread container is not one the identity cannot read.
- **`catalog-build`** (**921**) — the newest build that ran as that identity (`CHECK920_CATALOG_BUILD` names one
  instead) reached **SUCCESS** with no `availableSecrets`, no secret in a step's environment and no secret volume.
  IS8's point is that a catalog build reads its git credential from the credentials service, so it succeeds with
  no secret available to it at all.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `2`; a row the check could
not measure reads `UNVERIFIED` and the verdict is `INCOMPLETE` (exit `3`). The stops worth knowing by name:

- **Not the `fugaro-dev` installation** — `fugaro doctor --project fugaro --all` printed no
  `project: … (GCP fugaro-dev,` line. Nothing was called beyond doctor and `fugaro version`.
- **This shell is tracing** (`bash -x`, or `xtrace` in `SHELLOPTS`) — refused before anything is read: a trace
  would print every command into the log you would paste, including the access token read.
- **A coding agent's session** (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_REMOTE`, `CURSOR_AGENT` or
  `AI_AGENT` in the environment) — refused on sight, with the two commands to run yourself.
- **An input missing or malformed** — `CHECK920_REPO`, `CHECK920_CHECKOUT` (a directory, with a readable origin
  that names `CHECK920_REPO` exactly: a checkout of another repository is stopped here, before anything is called,
  rather than launching a review of that repository's pull request), `CHECK920_UNLISTED_REPO`, `CHECK920_SECRET` or
  `CHECK920_FILE_SECRET`. The two
  repositories must differ, the two secret names must differ, and a name is `[a-z0-9][a-z0-9-]{0,62}`.
- **No local config, or the wrong one** — `${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/fugaro.yaml`
  (`CHECK920_CONFIG` names another file), naming no Realtime Database URL, no runs bucket, or another GCP project:
  this check runs on `fugaro-dev` only.
- **`gcloud auth print-access-token` failed** — the record cannot be read; sign in with `gcloud auth login`.
- **A tool that is not on PATH** — the check says which one before it calls anything.
- **A read that did not answer** — `gcloud run jobs list`, a job's or an execution's `describe`, a container's IAM
  policy, the project's policy, the build list, or the run's own `result.json` (`gcloud storage cat`). Each leaves
  its own row UNVERIFIED rather than reporting a state nobody read.

## What is left behind

Nothing but the scratch directory, which the check removes itself (its own row says whether it did; set
`CHECK920_KEEP_SCRATCH=1` if you want to read what it wrote before it goes — the row then reads `INFO` with "kept
on purpose", and the verdict stays the reading's own). **The two runs and the review open a branch and a pull
request each on the sandbox repositories**: they are ordinary runs and the check holds no credential to close
them, so they are yours to settle — the block names every run id and pull request number it saw. It writes nothing
into the installation, writes no record and deletes nothing; its runs spend the installation's own budget, as any
run does.

**No agent runs any step of it.**
