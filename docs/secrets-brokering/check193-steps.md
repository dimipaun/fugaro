# Check 193 — the owner's steps (an `api-key` and an `oauth` repository through the credentials service, and the flip)

**Status: written by task B2.5 of the secrets-brokering plan; run by the owner, task B2.6 (the switch
mode) and task B2.8 (the `--after-flip` mode, after the flip).** NOT RUN. No agent runs any step of it,
and no agent has: what it measures is a live installation, real runs, and the credentials service's own
answers about them. **Both modes are run on the release that carries the flip** (the owner's ruling of
2026-11-18 moved the switch mode behind it, waiving the PASS it was to give B2.7), so the two are one
sitting: a **FAIL in the switch mode** means S2 is not in place for one of the two repositories — the
service is not serving a run whose job no longer holds the credential **the flip took away** — and a
**FAIL in the `--after-flip` mode** means the flip did not reach the jobs or reached into a run that was
already going. Both come back to the owner as a decision rather than as something to work around.

The check is `scripts/live-checks/check193.sh` — one script, two modes: without an argument it runs
**Check 193** (the switch mode, the plan's B2.6), with `--after-flip` it runs the flip's half (B2.8).
The register's section is `docs/gcp-live-checklist.md`, "Check 193", and that is where the block you
paste back is recorded; the script's own header carries what each mode settles (the reconciliation's
**D2070 (a)**, **D2071**, **F29** and **U9**), what it creates, what it costs and what its restore
deletes. Read those before you start. This file is the commands.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are hard-coded
  in the script and never read from `gcloud config`; `fugaro doctor --project fugaro --all` must print
  its `project: … (GCP fugaro-dev,` line (the check reads that **line**, not doctor's exit status, which
  is `1` whenever any check of its own failed), and `fugaro version` must not be a development build:
  a build from a checkout resolves no released credentials-service image to deploy, and this whole
  reading is about what that image does. Never `belong`.
- **Two sandbox repositories of that installation, already on the credentials-service switch**, one
  whose own `fugaro.yaml` records `agent.auth: api-key` and one whose records `oauth` — and, through
  `fugaro update --repo`, one `creds/repoauth/<slug>` record each, which is what the check reads their
  auths out of. The `<slug>` is the repository's own task slug (`task.Slug`: the readable path, a `-`
  and 16 hex — `acme-sandbox-api-1a2b3c4d5e6f7081`, never the path spelled out of the repository's
  name), and the check reads it off the `fugaro_repo` label of that repository's jobs, which is the key
  `fugaro update --repo` writes the record under: a repository whose jobs carry no such label leaves the
  record unread (verdict `2`). Name them in that order or the other; the check reads the pair and says
  which is which. A repository that is not switched, a pair that is not one of each, and an auth the
  service serves no call by (`cascade`, or anything else) are each stopped before a run is launched.
- `bash`, `mktemp`, `python3`, `gcloud`, `curl` and `fugaro` on PATH, with `gcloud` authenticated as
  you: the two records are read with `gcloud auth print-access-token` and a GET against the
  installation's own Realtime Database (`budget.rtdb_url` in `~/.config/fugaro/projects/fugaro.yaml`, or
  the file `CHECK193_CONFIG` names), read-only. Nothing else is asked of your account: this check mints
  no credential and creates nothing.
- **Two throwaway runs, and wall clock.** Each writes one line to `CHK193.md`, commits it, pushes a
  branch and opens a pull request — so it takes as long as a run of that repository takes (about 30–40
  minutes). `--after-flip` launches two more. **Wall clock, not money**: nothing is created in the
  project, and the runs are ordinary runs of the sandbox repositories.
- **For `--after-flip`: a run of one of the two repositories that is still going** when you re-apply
  the release that drops the mount. Its id goes in `CHECK193_INFLIGHT`. That run is the whole of U9's
  reading — an execution created before the re-apply keeps the container spec it was created with, and
  the check reads **that spec**: the execution's own containers still carry the model credential while
  the job template the re-apply wrote carries none. The check requires it to have started **before**
  that repository's jobs were last updated (their own `updateTime`) and to have finished with a pull
  request; a run that started after the re-apply is refused rather than read, because it was launched by
  the template the re-apply wrote and its mount says nothing about U9. The service's own `credsvc: audit`
  lines for that run are **not** this row's reading: the flip takes the **mounts** (D2072) and not the
  address, which the launcher writes on each run's own **execution** (no per-repository job template
  carries it at all), so a switched run reaches the service before and after it and has lines of its
  own either way.

## The steps

1. **The dry run first.** It prints every command a real run would run and it **calls nothing at all** —
   no `gcloud`, no `fugaro`, no `curl` — so it is safe on a machine with no credentials, and it is the
   mode to try if you are not sure your shell is the right one.

   ```sh
   CHECK193_REPOS=<api-key repo>,<oauth repo> bash scripts/live-checks/check193.sh --dry-run
   ```

   What it would do, in order: check that this is the `fugaro-dev` installation and that `fugaro
   version` is not a development build; read `gcloud run jobs list` for the two repositories' jobs;
   read each repository's `creds/repoauth` record out of the installation's own database — the row that
   assigns the two auths, and the `switch` row below is read against them, which is why the records are
   read **first**; then read `gcloud run services get-iam-policy fugaro-credentials` for the invoker
   half of the switch (the other half being that no job of either still mounts a model credential) and
   `fugaro project policy show --json` for the budget mode; launch one throwaway run in each repository
   (each writes one line to `CHK193.md`, commits it and opens a pull request) and poll `fugaro runs ls`
   for its row; then read the service's own `credsvc: audit` lines for those two runs out of Cloud
   Logging.
   Its cleanup would run nothing: this check creates no cloud resource and writes no record, so the only
   thing it makes is the scratch directory its own exit trap removes.

2. **The run itself** — the same command without `--dry-run`:

   ```sh
   CHECK193_REPOS=<api-key repo>,<oauth repo> bash scripts/live-checks/check193.sh
   ```

   Progress goes to stderr; one `CHECK 193 RESULT` block goes to stdout, at the end. **Paste that whole
   block back to the coordinator** and nothing else: it carries the two repositories and which is which,
   each run's id and outcome, the credential and tier the service's own lines name for each, the account
   the `oauth` run declared, whether the proxied one's git traffic went through the service, and the
   names of the four runs — and no token, no access token and no credential value: the check redacts
   credential-shaped text and never prints one.

3. **After the flip, with a run in flight.** From each sandbox checkout: `fugaro update` (the release
   that carries B2.7), then `fugaro update --repo` **while a run of one of the two is going**, then:

   ```sh
   CHECK193_REPOS=<api-key repo>,<oauth repo> CHECK193_INFLIGHT=<that run id> \
     bash scripts/live-checks/check193.sh --after-flip --dry-run
   ```

4. **And the same without `--dry-run`**; paste that block back too. Check 193 has two runs in the
   register (B2.6 and B2.8), and the two modes print **different** facts: the switch mode has no mount
   to read and the flip mode has no account boundary to read.

## What each row means

- **`switch`** — every job of both repositories is on the credentials-service switch, read per repository
  off what the apply **changes**: the job mounts **no model credential** (D2072/G17 — the one mount a
  switched job keeps is a mixed stage account's own `CLAUDE_CODE_OAUTH_TOKEN`, and only an **`api-key`
  repository's** jobs have one — the mix is `api-key`'s alone, so the row judges that mount by the auth
  the repository's own record names, which is why the `record` row is read first, and never by the job's
  `FUGARO_STAGE_ACCOUNT_SERVED`, which says whether the service *selects* that account and leaves the
  mount in place either way; the same single token on an `oauth` repository's job is the run's own
  credential, the one the flip takes away, and is a FAIL), and the job's own account is a member of the
  `roles/run.invoker` binding on the `fugaro-credentials` service's own IAM policy
  (`gcloud run services get-iam-policy`,
  D135). And the published budget mode is not `off` (D2071: the mode is read at `fugaro update`, and a
  switched repository whose mode is off has nothing to be served from). **Not**
  `FUGARO_CREDENTIALS_SERVICE_URL`: no per-repository job template carries it — the launcher writes the
  address on each run's **execution** — so it is read only for a shared project job's template, where it
  is baked (Check 197). A FAIL here **stops the run before any run is launched**.
- **`record`** — each repository's `creds/repoauth/<slug>` node, read out of the installation's own
  database at the slug its jobs' `fugaro_repo` label carries: one `api-key` and one `oauth`. `cascade` is
  refused by name: such a repository is served the whole cascade, and this check is about a repository
  the service narrows to its own recorded auth.
- **`run-api-key`, `run-oauth`** — the run's own row: `succeeded`, with a pull request. On a repository
  on the switch every model call of that run is answered by the service, so a run that could not finish
  is the service not serving it.
- **`audited-api-key`, `audited-oauth`** — the number of the service's own `credsvc: audit` lines for
  that run and the credentials they name, with tier. No line at all means its model calls were answered
  by something else — the credential the job itself mounts, or an upstream that is not this service.
- **`served-api-key`** — a line naming `anthropic-api-key` at **tier B**. **`served-oauth`** — a line
  naming a Claude account's token at **tier A**, and a tier-A line names the **account** itself —
  `default` for the unsuffixed `claude-oauth-token`, the bare account name for a named one — never the
  `claude-oauth-token[-<account>]` Secret Manager container the token is stored in: no audit line
  carries a container's name.
- **`declared-account`** — the account the **launch** declared, which for this check is the repository's
  default account (`default`): the check runs `fugaro run` with no `--token`, and a launch that names no
  account declares the default one. It is deliberately **not** read off the run's own row — that row's
  `oauth_account` is the credential the service's answers carried, the account the run used **last**, so
  a row that read the declaration there would compare the answers with themselves and pass a service
  that served one account the run never declared. The observation is the run's tier-A lines: every one
  of them must name `default` and no other account the repository's record names. A record naming a
  single account leaves this row unmeasured (verdict `2`) rather than passing it, and so does an
  `oauth` run that **never launched** — the row is read off that run's own lines, and a launch that
  failed left it none (the launch's own `served-oauth` row says why). A sandbox
  repository whose own `fugaro.yaml` sets `agent.oauth_token` names an account there, which its run
  declares instead — this row then reads the account it was served as one the run did not declare.
- **`proxy`** — a line of the run's own under the purpose `git` or `bitbucket-api`, for the one of the
  two the published policy admits with `proxy: true` (F29). Unmeasured (verdict `2`) when neither is
  published with it.
- **`no-model-mounts`, `no-model-mounts-detail`** (`--after-flip`) — no job of either repository carries
  `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`, and none is fed from the `anthropic-api-key` or
  `claude-oauth-token*` Secret Manager IDs (the DERIVED id is what a job mounts). Provider keys left on
  a job are counted in the detail row and are **not** a finding (R6).
- **`inflight-run`, `inflight-mount`** (`--after-flip`) — the run named by `CHECK193_INFLIGHT` started
  before its repository's jobs were last updated and finished all the same, and the execution **its own
  row names** still mounts the model credential on the container spec it was **created** with, while the
  job template the re-apply wrote mounts none: the execution kept what it was launched with, which is
  U9. The mount is read off `gcloud run jobs executions describe` and never off the service's lines.
  Unmeasured (verdict `2`) when that row names no execution, when the `describe` does not answer, or when
  the repository's jobs still mount the credential themselves — then the flip has not reached them and
  the mount on the execution is not a retention (see `no-model-mounts`) — and a run whose execution
  mounts **nothing** is a FAIL: it was launched by the flipped template, so the re-apply was not in
  flight across it.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1`; a row the
check could not measure reads `FAIL` and the verdict is `2` ("could not measure"). The stops worth
knowing by name:

- **Not the `fugaro-dev` installation** — `fugaro doctor --project fugaro --all` printed no
  `project: … (GCP fugaro-dev,` line. Nothing was called beyond doctor and `fugaro version`.
- **A development build** — `fugaro version` printed `dev`: a checkout build resolves no released
  credentials-service image to deploy. Install the release under test and run it again.
- **This shell is tracing** (`bash -x`, or `xtrace` in `SHELLOPTS`) — refused before anything is read:
  a trace would print every command into the log you would paste, including the access token read. Run
  it without `-x`; the check prints its own `RESULT` block, so a trace is not needed to report a failure.
- **`CHECK193_REPOS` missing, malformed, one repository, more than two, or the same one twice** — the
  check reads exactly two, one `api-key` and one `oauth`, and it says which of the shapes it got.
- **A repository of the owner's own workspace** (`belong/…`) — refused on sight: this check runs
  against sandbox repositories of the `fugaro-dev` installation and nothing else.
- **`CHECK193_INFLIGHT` empty with `--after-flip`** — U9 is a run that was already going when you
  re-applied, and a run this check picked itself could be one that ended hours before the re-apply.
- **`gcloud auth print-access-token` failed** — the record cannot be read; sign in with `gcloud auth
  login` and run it again.
- **No Realtime Database URL** — `budget.rtdb_url` was not found in
  `~/.config/fugaro/projects/fugaro.yaml`; point `CHECK193_CONFIG` at that file if it lives elsewhere.
- **A record that is not there** (HTTP 404 on `creds/repoauth/<slug>.json`) — `fugaro update --repo` has
  not run in that repository since the release that writes one. Both records are read before any run is
  launched.
- **A tool that is not on PATH** — `python3` is needed to read the `gcloud` and `fugaro` documents, and
  the check says which one is missing before it calls anything.

## What is left behind

Nothing but the scratch directory, which the check removes itself (its own row says whether it did; set
`CHECK193_KEEP_SCRATCH` if you want to read what it wrote before it goes — the row then reads `PASS`
with "kept on request", and the verdict stays the reading's own). The **two**
runs of the switch mode, and the **two more** of the `--after-flip` mode, open a branch and a pull
request each on the sandbox repositories: they are ordinary runs and the check holds no credential to
close them, so they are yours to settle — the block names every run id and pull request number it saw.

**No agent runs any step of it.**
