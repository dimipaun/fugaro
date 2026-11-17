# Check 194 — the owner's steps (a repository's workflow secrets read by the credentials service, and the flip)

**Status: written by task B3.6 of the secrets-brokering plan; run by the owner, task B3.7 (the switch
mode) and task B3.9 (the `--after-flip` mode, after the flip).** NOT RUN. No agent runs any step of it,
and no agent has: what it measures is a live installation, real runs, and the credentials service's own
answers about them. A **FAIL in the switch mode** means S3 is not in place for the sandbox repository —
the record the service serves a run from, the containers that record names, or the boundary around the
second repository — and the flip (**B3.8**) does not go on until it is. A **FAIL in the `--after-flip`
mode** means the flip did not reach the repository's jobs, its account still holds an accessor on a
workflow secret's container, or the re-apply reached into a run that was already going (U9). All three
come back to the owner as a decision, never as something to work around.

The check is `scripts/live-checks/check194.sh` — one script, two modes: without an argument it runs
**Check 194** (the switch mode, the plan's B3.7), with `--after-flip` it runs the flip's half (B3.9). The
register's section is `docs/gcp-live-checklist.md`, "Check 194", and that is where the block you paste
back is recorded; the script's own header carries what it settles (the reconciliation's **D2073**,
**D2074**, **D2075**, **D1262**, **D1264**, **D1267**, **D2313**, **G18**, **G19**, **F33**, **F4** and
**R6**, and **U9** in the flip's half), what it creates, what it costs, and what its restore deletes. Read
those before you start. This file is the commands.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are hard-coded
  in the script and never read from `gcloud config`; `fugaro doctor --project fugaro --all` must print
  its `project: … (GCP fugaro-dev,` line (the check reads that **line**, not doctor's exit status, which
  is `1` whenever any check of its own failed), and `fugaro version` must not be a development build: a
  build from a checkout resolves no released credentials-service image to deploy, and this whole reading
  is about what that image does. A development build is recorded as a FAILed `fugaro-version` row rather
  than a stop, so read that row before you spend forty minutes on a run: install the release under test
  and run the check again. Never `belong`.
- **One sandbox repository of that installation under test, and one more beside it** — `CHECK194_REPO`
  and `CHECK194_OTHER_REPO`, both `owner/name`, sandbox repositories only, and they must differ. The
  repository under test is the one whose workflow declares the two secrets (below) and is the one the
  check launches runs in; the **second** repository is read and never launched in, and it is the one the
  per-repository allow-list entry must **not** list — that is what makes the boundary readable at all.
  Both must have been through `fugaro update --repo` since the release that carries B3.1, because the
  check reads each repository's `creds/workflowsecrets/<slug>` record by the `fugaro_repo` label of its
  own jobs; a repository whose jobs carry no such label leaves that row unmeasured (verdict `2`).
- **The repository under test must declare two workflow secrets, one of them project-wide by its
  allow-list entry.** In its own `fugaro.yaml`:

  ```yaml
  workflows:
    go:
      commands:
        build: |
          set -e
          <this repository's build>
        test: |
          set -e
          test -n "$DEPLOY_TOKEN"  || { echo "DEPLOY_TOKEN is empty"; exit 1; }
          test -n "$PROJECT_TOKEN" || { echo "PROJECT_TOKEN is empty"; exit 1; }
      secrets:
        - { name: deploy-token, env: DEPLOY_TOKEN }
        - { name: pool-token,   env: PROJECT_TOKEN }
  ```

  The `test:` half is the verify stage's own command and **failing on an empty variable is the whole
  point of it**: the `verify` row reads the run's own outcome and nothing else, and a repository whose
  verify passes with both variables empty could not tell a fetch that happened from one that did not.
  The two `env` names are what the allow-list entries are read against; `CHECK194_ENV_ONE` and
  `CHECK194_ENV_TWO` (defaults `DEPLOY_TOKEN` and `PROJECT_TOKEN`) must be **those** names, upper-case.
- **Two entries in the operator-local `workflow_secrets` block** of this machine's config
  (`~/.config/fugaro/projects/fugaro.yaml`) — the block only the owner writes, never a repository's own
  file. One lists the repository under test and one admits the whole workspace, and **both are stored
  project-wide**:

  ```yaml
  workflow_secrets:
    deploy-token:
      allow_data_to: [<workspace>/<the repository under test>]
    pool-token:
      allow_data_to: ["<workspace>/*"]
  ```

  Each entry is what gives its name a project-wide meaning: with the entry, `fugaro secrets set
  deploy-token` (and `pool-token`) stores the **project's own** container — it asks for the value at
  your own terminal and never prints it — and `fugaro secrets allow deploy-token <workspace>/<repo>`
  writes the list if you would rather not edit the file by hand. A name with no entry, or an entry that
  lists nothing, stays a repository's own copy and is not what this check reads.
- **What the record will name, and why it is not the repository's own container (D2313, decided
  2026-11-13, after this check was planned).** A repository-level secret is accepted and ignored, so the
  container a workflow secret's value comes from is the installation's **project-wide** copy and never
  the repository's own: the record names `fugaro-project-<installation>-<name>-<16 hex>`
  (`gcp.ProjectSecretID`) and never `fugaro-<readable slug>-<name>-<16 hex>` (`gcp.SecretID`) — for this
  installation, `fugaro-project-fugaro-deploy-token-…`. The plan's "one per-repository and one project
  workflow secret" is therefore read as the allow-list read the two ways it can be written: the entry
  that lists **one** repository, and the entry that admits the workspace. **Both** entries still produce
  a project container, and that is not a FAIL: a row that demanded a repository container here would
  refuse every installation the merged code can produce.
- `bash`, `mktemp`, `python3`, `gcloud`, `curl` and `fugaro` on PATH, with `gcloud` authenticated as
  you. It is asked for read-only reads: `run jobs list` (and one execution's description), a secret's
  own IAM policy, Cloud Logging, and — with `gcloud auth print-access-token` — a GET of each record out
  of the installation's own Realtime Database (`budget.rtdb_url` in `~/.config/fugaro/projects/fugaro.yaml`,
  or the file `CHECK194_CONFIG` names). The records are **read and never written**: the record is what
  the service serves a run from, and a check that rewrote one and failed to put it back would point
  every fetch of that repository at another container.
- **One throwaway run, and wall clock.** It writes one line to `CHK194.md`, commits it, pushes a branch
  and opens a pull request — so it takes as long as a run of that repository takes (about 40 minutes).
  `--after-flip` launches one more. **Wall clock, not money**: nothing is created in the project, and
  both are ordinary runs of the sandbox repositories.
- **For `--after-flip`: a run of the repository under test that is still going** when you re-apply the
  release that drops the mount. Its id goes in `CHECK194_INFLIGHT`. That run is the whole of U9's
  reading — an execution created before the re-apply keeps the container spec it was created with, and
  the check reads **that spec**: the execution's own containers still read both variables out of the
  containers the record names while the job template the re-apply wrote hands a run neither. The check
  requires the run to have started **before** that repository's jobs were last updated (their own
  `updateTime`) and to have finished with a pull request; a run that started after the re-apply is
  refused rather than read, because it was launched by the template the re-apply wrote and its spec says
  nothing about U9. The service's own `credsvc: audit` lines for that run are **not** this row's reading:
  the flip keeps `FUGARO_CREDENTIALS_SERVICE_URL` on the repository (B3.8), so a switched run fetches
  from the service before and after it and has lines of its own either way.

## The steps

1. **The dry run first.** It prints every command a real run would run and it **calls nothing at all** —
   no `gcloud`, no `fugaro`, no `curl` — so it is safe on a machine with no credentials, and it is the
   mode to try if you are not sure your shell is the right one.

   ```sh
   CHECK194_REPO=<the repository under test> CHECK194_OTHER_REPO=<the second repository> \
     bash scripts/live-checks/check194.sh --dry-run
   ```

   What it would do, in order: check that this is the `fugaro-dev` installation and that `fugaro version`
   is not a development build; read `gcloud run jobs list` for the installation (one read that serves the
   slugs, the mounts, the job environments and the re-apply's clock); read each repository's
   `creds/workflowsecrets/<slug>` record out of the installation's own database; read the second
   repository's record and its jobs; check that every job of the repository under test that carries a
   variable of interest is fed it from the container the record names; launch one throwaway run (it
   writes one line to `CHK194.md`, commits it and opens a pull request) and poll `fugaro runs ls` for its
   row, then read the execution that row names. Its cleanup would run nothing: this check creates no
   cloud resource and writes no record, so the only thing it makes is the scratch directory its own exit
   trap removes.

2. **The run itself** — the same command without `--dry-run`:

   ```sh
   CHECK194_REPO=<the repository under test> CHECK194_OTHER_REPO=<the second repository> \
     bash scripts/live-checks/check194.sh
   ```

   Progress goes to stderr; one `CHECK 194 RESULT` block goes to stdout, at the end. **Paste that whole
   block back to the coordinator** and nothing else: it carries both repositories, the record's key and
   the container it names for each variable, the container each job is fed from, the run's id, outcome
   and pull request, and what the second repository's own record and jobs hold — and **no workflow
   secret's value**: the value is never read by this check at all (the record holds container IDs, an
   audit line holds the container's name, and a job's spec holds the secret reference its variable is
   fed from), and credential-shaped text in any message is redacted besides.

3. **After the flip, with a run in flight.** From the sandbox checkout: `fugaro update` (the release
   that carries B3.8), then `fugaro update --repo` **while a run of the repository under test is going**,
   then:

   ```sh
   CHECK194_REPO=<the repository under test> CHECK194_OTHER_REPO=<the second repository> \
     CHECK194_INFLIGHT=<that run id> bash scripts/live-checks/check194.sh --after-flip --dry-run
   ```

4. **And the same without `--dry-run`**; paste that block back too. Check 194 has two runs in the
   register (B3.7 and B3.9), and the two modes print **different** facts: the switch mode has no mount
   to see gone and the flip mode has no mount to compare with the record.

Its runs are named from today's UTC date and time plus four hex characters each (`-0197` in the switch
mode, `-0198` with `--after-flip`); check 193's own four are `-0193` to `-0196`. Set `CHECK194_RUN_ID` to
reuse a base.

## What each row means

- **`record`** — the repository's `creds/workflowsecrets/<slug>` node, read out of the installation's own
  database at the slug its jobs' `fugaro_repo` label carries. Four things are one fact: the node is
  there; its shape is **closed** (`version` and `workflows`; per entry `env` and `id` — every member of
  it is a NAME, and a member this release does not model is a FAIL rather than a shrug, because a member
  nobody reads is one that could hold a value, **G18**); it names a container for **each** of the two
  variables; and each of those is a **project** container of this installation (**D2313**, above). A
  record that fails here leaves every other row about a mount, a fetch or an accessor unmeasured
  (verdict `2`) and **stops the check before any run is launched**.
- **`mounts`** — **F33** and **D1267**: the fetch reads the container the mount reads, so the two have
  to be the same one. Every variable of interest a job of the repository carries is fed from the
  container the record names for it, no job carries one as a plain value (a value written into the job
  is one a fetch could never be seen to replace), and the **execution the run itself ran on** reads both
  from those containers — that last part is what makes the run's own verify a reading about these
  containers rather than about a job spec somebody edited since.
- **`verify`** — the plan's "a run's verify sees both". The run's own row: `succeeded`, with a pull
  request. The sandbox's `test:` command fails on an empty `DEPLOY_TOKEN` or `PROJECT_TOKEN`, so a run
  that finished read both values — out of the containers the row above reads off the execution it ran
  on. Which container each value came from is not this row's business and is not guessed from it.
- **`other`** — the plan's "another repository's job is refused them", read where it can be. The service
  decides every call from the record of the repository the **verified** job account belongs to and is
  handed nothing else (the call names one environment variable and no repository, **G19**), so the
  boundary is exactly "what is in the caller's own record": the second repository's record must name no
  container for the per-repository variable, none of its jobs may carry that variable at all, and
  neither its record nor its jobs may name the first repository's container. The project-wide entry
  means it **is** served the other variable, and the row says so — the two entries read apart, per
  repository, is the whole point.
- **`no-mounts`** (`--after-flip`) — no job of the repository hands a run either variable **in any
  form** (not "fed from another container": not there at all), and every one of its jobs still carries
  `FUGARO_CREDENTIALS_SERVICE_URL`. The flip takes the mounts and **keeps the URL** (B3.8): a job with
  no mount and no URL is a run that would fetch nothing and start with the variable empty, which is the
  state this row exists to catch.
- **`served`** (`--after-flip`) — the run launched after the flip finished on a job that mounts
  neither variable, and the service's own `credsvc: audit` lines for that run name **both** containers
  the record names under the purpose `workflow-secret`. Those two together are the fetch: the value
  reached the run (the `verify` reasoning) and it can only have come from the service, because the job
  holds none. A line of another purpose is not read here — a switched run's git and model calls are
  served too, and a row that counted them would call the record's containers served when they were not.
  The check also reads the execution the run ran on, and an execution that still carries either
  variable means the values came out of the job and not the service: that is a FAIL with its own
  sentence, because this row would otherwise be reading a mount as a fetch.
- **`no-accessors`** (`--after-flip`) — the account the run's job runs as holds no `secretAccessor`
  binding on either container the record names, read off each container's own IAM policy
  (`gcloud secrets get-iam-policy`). **D2074**'s grants are the credentials service's, one per container
  the job mounts (B3.3), so after the flip the value reaches the run through the service and the job's
  own account reads nothing itself. The account is read off the job template the run's execution names,
  not off the execution (a finding about a real installation and not about a document that does not
  carry it), and the members are compared with their `serviceAccount:` prefix dropped.
- **`inflight-run`, `inflight-mount`** (`--after-flip`) — the run named by `CHECK194_INFLIGHT` started
  **before** its repository's jobs were last updated and finished all the same, and the execution **its
  own row names** still reads both variables out of the containers the record names while the job
  template the re-apply wrote hands a run neither: the execution kept the spec it was created with,
  which is **U9**. The mount is read off `gcloud run jobs executions describe` and never off the
  service's lines. `inflight-mount` is unmeasured (verdict `2`) when that row names no execution, when
  the `describe` does not answer, or when the repository's jobs still hand a workflow secret out — then
  the flip has not reached them and a mount on the execution is not a retention (see `no-mounts`) — and
  an execution that reads **neither** variable is a FAIL: it was launched by the flipped template, so
  the re-apply was not in flight across it.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1`; a row the
check could not measure reads `FAIL` and the verdict is `2` ("could not measure"). The stops worth
knowing by name:

- **Not the `fugaro-dev` installation** — `fugaro doctor --project fugaro --all` printed no
  `project: … (GCP fugaro-dev,` line. Nothing was called beyond doctor and `fugaro version`.
- **This shell is tracing** (`bash -x`, or `xtrace` in `SHELLOPTS`) — refused before anything is read: a
  trace would print every command into the log you would paste, including the access token read. Run it
  without `-x`; the check prints its own `RESULT` block, so a trace is not needed to report a failure.
- **`CHECK194_REPO` or `CHECK194_OTHER_REPO` missing, malformed, the same repository, or one of them in
  the owner's own workspace** (`belong/…`) — refused on sight: this check runs against two sandbox
  repositories of the `fugaro-dev` installation and nothing else.
- **`CHECK194_ENV_ONE` / `CHECK194_ENV_TWO`** — not upper-case names, or the same variable twice: one
  variable cannot become two workflow secrets.
- **`CHECK194_INFLIGHT` empty with `--after-flip`** — U9 is a run that was already going when you
  re-applied, and a run this check picked itself could be one that ended hours before the re-apply.
- **`gcloud auth print-access-token` failed** — the records cannot be read; sign in with `gcloud auth
  login` and run it again.
- **No Realtime Database URL, or no installation name** — `budget.rtdb_url` (or `name`) was not found in
  `~/.config/fugaro/projects/fugaro.yaml`; point `CHECK194_CONFIG` at that file if it lives elsewhere.
  The **name** is the installation's own Fugaro project (`fugaro`), not the GCP project (`fugaro-dev`),
  and it is what the project containers the record names are derived from — a check that read the GCP
  project here would refuse every container a real installation writes.
- **A record that is not there** (HTTP 404 on `creds/workflowsecrets/<slug>.json`) — `fugaro update
  --repo` has not run in that repository since the release that writes one, or the name's allow-list
  entry does not list it. Both records are read before any run is launched.
- **A record naming no container for a variable** — the operator's `workflow_secrets.<name>.allow_data_to`
  does not list the repository (the record carries a row for every declared secret whose container its
  plan resolved), so the allow-list entry and the workflow's `secrets:` names are the two things to read
  back before running `fugaro update --repo` again.
- **A tool that is not on PATH** — `python3` reads every `gcloud` and `fugaro` document, and the check
  says which one is missing before it calls anything.
- **A read that did not answer** — `gcloud run jobs list`, `gcloud run jobs executions describe`,
  `gcloud secrets get-iam-policy` or `gcloud logging read` exiting non-zero. Each leaves its own row
  unmeasured (verdict `2`) rather than reporting a state nobody read: a read that did not answer is
  **not** the service having served nothing, and not a container nobody can read.

Two of those are worth singling out because they look like answers and are not: a `logging read` that
failed is not the service's silence (a run with **no** line of its own is the reading this check wants
after the flip, and only a read that answered may be read that way), and an execution that mounts
nothing is a FAIL rather than a pass for the run in flight.

## What is left behind

Nothing but the scratch directory, which the check removes itself (its own row says whether it did; set
`CHECK194_KEEP_SCRATCH` if you want to read what it wrote before it goes — the row then reads `PASS`
with "kept on request", and the verdict stays the reading's own). The run of the switch mode, and the
one more of the `--after-flip` mode, open a branch and a pull request each on the sandbox repository:
they are ordinary runs and the check holds no credential to close them, so they are yours to settle —
the block names every run id and pull request number it saw. It writes nothing into the installation,
writes no record and deletes nothing.

**No agent runs any step of it.**
