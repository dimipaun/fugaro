# Check 197 — the owner's steps (coding runs on the profile's **shared** project jobs, GitHub and Bitbucket, through the credentials service, and the flip)

**Status: written by task B5.5 of the secrets-brokering plan; run by the owner, task B5.6 (the switch
mode, the gate S5 stops on) and task B5.8 (the `--after-flip` mode, after B5.7's flip).** NOT RUN. No
agent runs any step of it, and no agent has: what it measures is a live installation, real runs, a
**shared** job that serves several repositories, and the credentials service's own answers about them. A
**FAIL in the switch mode** means S5 is not in place — the profile's shared job, the run's landing on it,
or the service serving it — and **B5.7's flip does not go on until it is**; a **FAIL in the
`--after-flip` mode** means the flip did not reach the shared jobs or reached into a run that was already
going, and both come back to the owner as a decision rather than as something to work around.

The check is `scripts/live-checks/check197.sh` — one script, two modes: without an argument it runs
**Check 197** (the switch mode, the plan's B5.6), with `--after-flip` it runs the flip's half (B5.8). The
register's section is `docs/gcp-live-checklist.md`, "Check 197", and that is where the block you paste
back is recorded; the script's own header carries what each mode settles (the reconciliation's **D2071**,
**D2072**, **D2078**, **D2079**, **D2080**, **G21**, **F40** and **U9**), what it creates, what it costs
and what its restore removes. Read those before you start. This file is the commands.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are hard-coded
  in the script and never read from `gcloud config`; `fugaro doctor --project fugaro --all` must print
  its `project: … (GCP fugaro-dev,` line (the check reads that **line**, not doctor's exit status, which
  is `1` whenever any check of its own failed), and `fugaro version` must not be a development build:
  a build from a checkout resolves no released credentials-service image to deploy, and this whole
  reading is about what that image does. Never `belong`.
- **A sandbox GitHub repository whose own `fugaro.yaml` selects a job profile** (`jobs.profile`), and a
  published policy whose **one** profile admits it (`fugaro project repos add`, then publish). That
  selection is what puts a run on the profile's **shared** job: a repository that selects no profile
  stays on its per-repository job, and the `job-one` row says so by name rather than reading a
  per-repository run as the shared shape S5 is. Name it with `CHECK197_REPO=<owner>/<repository>`.
- **A second sandbox repository whose own `fugaro.yaml` selects the SAME profile and which that
  profile's own `allow_repos` does not admit, while the project's does** — the launcher refuses it, and
  that refusal is a row of this check. Name it with `CHECK197_OUTSIDE=<owner>/<repository>`. The profile's
  own list can only narrow the project's enrollment and never widen it, so this pair is a policy you
  publish, not a repository you set up.
- **The profile's shared job deployed, and on the credentials-service switch**: `gcloud run jobs list`
  must hold a job named `fugaro-p-<profile>-github…` (`gcp.ProjectJobName`: one job per profile, provider
  and Claude account, plus a hash) and that job's own environment must carry
  `FUGARO_CREDENTIALS_SERVICE_URL`. A profile that deploys no job, or a job that names no service, is a
  row that ends the run **before any run is launched** (verdict `2` and `1`), because a run on a job off
  the switch would be a row about a job this check is not about.
- `bash`, `mktemp`, `python3`, `gcloud` and `fugaro` on PATH, with `gcloud` authenticated as you. **No
  `curl`, no access token, no Realtime Database read**: a shared job's credential is the service's own
  bundle, and the one record this check could read — `creds/repoauth`, Check 193's subject — belongs to a
  per-repository job and not to a shared one. Nothing is asked of your account beyond reading a job list,
  a job, an execution and Cloud Logging, and nothing is minted.
- **One throwaway run** in the GitHub sandbox repository (two with `--after-flip`), one refused launch in
  the repository outside the profile, and in the `--after-flip` mode a **run of that repository that is
  still going** when the flipping `fugaro update` runs, named in `CHECK197_INFLIGHT`. Each run writes one line to `CHK197.md`,
  commits it, pushes a branch and opens a pull request — so each takes as long as a run of that
  repository takes (about 30–40 minutes). **Wall clock, not money**: nothing is created in the project,
  and every model call of those runs is answered by the credentials service, which is the subject.
- **Optional, and not applicable before D2079's T2.2: a sandbox Bitbucket repository** the published
  `bitbucket_token` block proxies and whose own profile this installation deploys a shared Bitbucket job
  for. Name it with `CHECK197_BITBUCKET`. Without it — or with it, while no shared Bitbucket job exists —
  those four rows read **INFO** ("not applicable"), never FAIL: the plan itself makes this half
  "(after D2079)", and a run that measures the GitHub half perfectly ends `0` with this half reading that
  it is not deployed. As soon as the job is there, they are criteria like any other.
- **For `--after-flip`: the run that was going across the flipping `fugaro update`** (`CHECK197_INFLIGHT`).
  The check requires it to have started **before** the shared job was last updated (the job's own
  `updateTime`, which is the moment that one `fugaro update` rewrote it) and to have finished with a pull
  request; a run that started after the update is refused rather than read, because it was launched by the
  template the update wrote and its mount says nothing about **U9**. The service's own lines for that run are not
  this row's reading: the flip keeps `FUGARO_CREDENTIALS_SERVICE_URL` on the job (D2072), so a run of a
  shared job reaches the service before and after it.

## The steps

1. **The dry run first.** It prints every command a real run would run and it **calls nothing at all** —
   no `gcloud`, no `fugaro` — so it is safe on a machine with no credentials, and it is the mode to try
   if you are not sure your shell is the right one. (It still needs the knobs: they are inputs, not
   credentials.)

   ```sh
   CHECK197_REPO=<owner>/<repository> CHECK197_OUTSIDE=<owner>/<outside> \
     bash scripts/live-checks/check197.sh --dry-run
   ```

   What it would do, in order: check that this is the `fugaro-dev` installation and that `fugaro version`
   is not a development build; read `fugaro project policy show --json` for the one profile that admits
   the repository and for the budget mode; read `gcloud run jobs list` for the profile's shared jobs and
   whether each names the service; launch **one** throwaway run in the repository (it writes one line to
   `CHK197.md`, commits it and opens a pull request) and poll `fugaro runs ls` for its row; read the job
   the run landed on out of that row's own execution path, then the job's own service account; read the
   service's own `credsvc: audit` lines for the run out of Cloud Logging; launch a run of the repository
   outside the profile and expect the launcher's refusal; and (with `CHECK197_BITBUCKET` set) the same
   three reads for the Bitbucket repository. Its cleanup would run nothing: this check creates no cloud
   resource and writes no record, so the only thing it makes is the scratch directory its own exit trap
   removes.

2. **The run itself** — the same command without `--dry-run`:

   ```sh
   CHECK197_REPO=<owner>/<repository> CHECK197_OUTSIDE=<owner>/<outside> \
     bash scripts/live-checks/check197.sh
   ```

   Progress goes to stderr; one `CHECK 197 RESULT` block goes to stdout, at the end. **Paste that whole
   block back to the coordinator** and nothing else: it carries the profile the policy admits the
   repository to, the profile's shared jobs and whether each names the service, the job the run landed on,
   the run's id and outcome, the credential, tier and purpose the service's own lines name for it — at
   least one of them a proxied model call — the service account the service verified that call as, the
   refusal of the repository outside the profile, and no token, no key and no credential value: the check
   redacts credential-shaped text and never prints one.

3. **After the flip, with a run in flight.** The order matters and it is two commands, not three: the run
   is launched on the **pre-flip** installation — the one below, which is what step 2 ran against — and
   the **one** flipping `fugaro update` (the release that carries B5.7) happens **while that run is
   going**, so the shared job's last update lands after the run started and the execution the run is
   living in keeps the spec it was created with (**U9**). Do not upgrade first and re-apply again after:
   the flip's own update is the one that must land across the run, and a second `fugaro update` on the
   already-flipped installation leaves the job unchanged — its `updateTime` does not move — so a run
   launched after it started after the job's last update and the check refuses it by name instead of
   reading the retention. So, from the sandbox checkout on the pre-flip installation:

   ```sh
   fugaro run <the sandbox repository>        # note the run id it prints
   ```

   and then, while that run is going:

   ```sh
   fugaro update                              # the one command that flips: B5.7's release
   ```

   Then, once the update has finished (the run may still be going, which is the row's subject):

   ```sh
   CHECK197_REPO=<owner>/<repository> CHECK197_OUTSIDE=<owner>/<outside> \
     CHECK197_INFLIGHT=<that run id> \
     bash scripts/live-checks/check197.sh --after-flip --dry-run
   ```

4. **And the same without `--dry-run`**; paste that block back too. Check 197 has two runs in the
   register (B5.6 and B5.8), and the two modes print **different** facts: the switch mode has no mount and
   no in-flight run to read, and the flip mode has no repository outside the profile to refuse.

## What each row means

- **`policy`** — the published policy admits the repository to the project **and to exactly one profile**
  (the one the repository's own `fugaro.yaml` selects), that profile serves `github`, and the published
  budget mode is not `off`. A mode of `off` is no gateway, and the gateway is what reaches the service
  (**D2071**): a run of it would stop in bootstrap and every row below would be a row about nothing. A
  repository admitted to two profiles is **unmeasured** (verdict `2`) rather than guessed at, since which
  profile its file selects is not a fact of the policy. A FAIL here **stops the run before any run is
  launched**.
- **`switch`** — every shared job of the profile (one per provider and Claude account, matched by the
  name prefix `fugaro-p-<profile>-github`) carries `FUGARO_CREDENTIALS_SERVICE_URL`. That environment
  variable is the switch a launcher writes and nothing else can (**B2.3**, **D2072**), so a job without
  it holds whatever it mounts and reaches nothing. A FAIL or an unmeasured here stops the run before any
  run is launched.
- **`job-one`** — the job the run **landed on**, read out of the run's own row: the execution path
  `projects/<project>/locations/<region>/jobs/<job>/executions/<name>` is the only place that job is
  written down, and the row requires it to be one of the profile's shared jobs. A run that landed on a
  per-repository job means the repository's `fugaro.yaml` selects no profile — or this installation
  deploys no job for the one it selects — and the row says which.
- **`run-one`** — the run's own row: `succeeded`, with a pull request. On a shared job every credential
  of the run's path comes from the service, so a run that could not finish is the service, the shared job
  or the switch not serving it.
- **`audited-one`** — the number of the service's own `credsvc: audit` lines for that run, the
  `credential|tier|purpose` triples they name, and the job those lines **report** (a claim, recorded
  beside the verified identity and never instead of it). No line at all means the run's calls were
  answered by something else — an upstream that is not this service, or a credential the job should not
  hold after the flip.
- **`served-one`** — at least one of those lines is a proxied **model** call (purpose `forward`), and the
  **caller** on the lines — the identity the service **verified**, `AuditEvent.Caller`, the account Cloud
  Run minted the job's ID token for — is the shared job's own service account, read off the job's own
  spec. The caller is the one field of those lines a run cannot claim for itself (**G21**), which is why
  this row reads it and not the job the line reports. A line whose caller is another account is a
  credential served to something other than the shared job the run was launched in.
- **`outside-profile`** — a launch of `CHECK197_OUTSIDE` — a repository the project admits and the
  profile does not — is refused with the launcher's own sentence (`the job profile "…" does not admit …`,
  then `The launch stops before any execution exists`), and the repository's own listing holds no row for
  it. **What this row does not measure, and says so in the block**: the service's own half of the same
  gate. A shared caller's call needs both the job's Google ID token and a run identity, so it cannot be
  made from your machine at all (**F40**'s limitation); what is measured is the installation's refusal of
  the launch, decided by the same published policy. An installation that **launched** the repository
  anyway is a FAIL, and so is a refusal whose sentence is another gate's — the row then says it could not
  measure which gate answered.
- **`job-bitbucket`, `run-bitbucket`, `audited-bitbucket`, `served-bitbucket`** — the Bitbucket half
  (**D2079**): the profile's shared Bitbucket job carries the service's URL, a run of the proxied
  repository is served on it, and the same three readings as the GitHub half hold. `INFO` — not
  applicable — while no shared Bitbucket job is deployed (D2079's T2.2), or while no Bitbucket repository
  is named.
- **`no-model-mounts`, `no-model-mounts-detail`** (`--after-flip`) — **no** shared job of the profile
  carries `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`, and none is fed from the `anthropic-api-key`
  or `claude-oauth-token*` Secret Manager IDs (the DERIVED id is what a job mounts, so the logical name
  is the last readable segment of it and never the first). Provider keys left on a job are counted in the
  detail row and are **not** a finding (**R6**: the flip takes the model credentials and keeps every
  provider route a repository's stages go out on).
- **`no-token-mounts`** (`--after-flip`) — no shared job whose name reads as a Bitbucket one carries
  `FUGARO_BITBUCKET_TOKEN` (**D2079**: a shared Bitbucket job is proxied and mounts **no** token, which
  is the whole point of the job serving several repositories).
- **`inflight-run`, `inflight-mount`** (`--after-flip`) — the run named by `CHECK197_INFLIGHT` started
  before the shared job was last updated and finished all the same, and the execution **its own row
  names** still mounts the model credential on the container spec it was **created** with, while the job
  template the re-apply wrote mounts none: the execution kept what it was launched with, which is **U9**.
  The mount is read off `gcloud run jobs executions describe` and never off the service's lines.
  Unmeasured (verdict `2`) when that row names no execution, when the `describe` does not answer, or when
  the shared job still mounts the credential itself — then the flip has not reached it and the mount on
  the execution is not a retention (see `no-model-mounts`) — and a run whose execution mounts **nothing**
  is a FAIL: it was launched by the flipped template, so the re-apply was not in flight across it.
- **`run-flip-one`, `audited-flip-one`, `served-flip-one`, `job-flip-one`** (`--after-flip`) — one more
  run of the repository **on the flipped shared job**: with the mounts gone the service is the only path
  its model calls have, so a run that finishes with its calls served by the service is the flip having
  left the path intact. They are unmeasured when the shared job is off the switch (the `switch` row
  carries why).

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1`; a row the
check could not measure reads `FAIL` and the verdict is `2` ("could not measure"). The stops worth
knowing by name:

- **Not the `fugaro-dev` installation** — `fugaro doctor --project fugaro --all` printed no
  `project: … (GCP fugaro-dev,` line. Nothing was called beyond doctor and `fugaro version`.
- **A development build** — `fugaro version` printed `dev`: a checkout build resolves no released
  credentials-service image to deploy. Install the release under test and run it again.
- **This shell is tracing** (`bash -x`, or `xtrace` in `SHELLOPTS`) — refused before anything is read:
  a trace would print every command into the log you would paste. Run it without `-x`; the check prints
  its own `RESULT` block, so a trace is not needed to report a failure.
- **`CHECK197_REPO` or `CHECK197_OUTSIDE` missing, malformed (`<owner>/<repository>` is the shape), the
  same repository twice** — the check reads one repository on the shared job and one outside its profile,
  and it says which of the shapes it got. A repository of the owner's own workspace (`belong/…`) is
  refused on sight: this check runs against sandbox repositories of the `fugaro-dev` installation and
  nothing else.
- **`CHECK197_INFLIGHT` empty with `--after-flip`** — U9 is a run that was already going when you
  re-applied, and a run this check picked itself could be one that ended hours before the re-apply.
- **A tool that is not on PATH** — `python3` is needed to read the `gcloud` and `fugaro` documents, and
  the check says which one is missing before it calls anything.

## What is left behind

Nothing but the scratch directory, which the check removes itself (its own row says whether it did; set
`CHECK197_KEEP_SCRATCH` if you want to read what it wrote before it goes — the row then reads `PASS`
with "kept on request", and the verdict stays the reading's own). The runs open a branch and a pull
request each on the sandbox repositories — one in the switch mode, one more with `--after-flip`, and the
refused launch of the repository outside the profile leaves none — and they are ordinary runs: the check
holds no credential to close them, so they are yours to settle, and the block names every run id and
pull request number it saw. No record is written, no node is read and no cloud resource is created.

**No agent runs any step of it.**
