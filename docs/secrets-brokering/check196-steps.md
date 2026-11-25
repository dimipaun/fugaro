# Check 196 — the owner's steps (a repository's image build and its daily image check through the credentials service, GitHub and Bitbucket, and the flip)

**Status: written by task B4.7 of the secrets-brokering plan; run by the owner, task B4.8 and, after
the flip, task B4.10. NOT RUN.** No agent runs any step of it, and no agent has: what it reads is a
live installation's own records. A **FAIL in the switch mode stops S4** and comes back to the owner as
a decision — a build served another repository's credential, or a service answering anyone, is a design
answer and not a bug to work around. The owner's gate is task **B4.8**: the dry run, then the run, then
the block pasted back.

The check is `scripts/live-checks/check196.sh`. The register's section is `docs/gcp-live-checklist.md`,
"Check 196", and that is where the block you paste back is recorded; the script's own header carries
what it settles (the reconciliation's **D2076**, **D2077**, **R5**, **R6**, **G18**, **G19**, **G20**,
**F28** and **F29**), what it reads, what it costs and what its Restore removes. Read those before you
start. This file is the three commands and what each row means.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are
  hard-coded in the script, and `fugaro doctor --project fugaro --all` must print its
  `project: … (GCP fugaro-dev,` line, or the check stops: it refuses to run against anything else.
  Never `belong`, and no knob can point it anywhere else. `fugaro version` must not print `dev`.
- `bash`, `mktemp`, `python3`, `gcloud` and `fugaro` on PATH, `gcloud` authenticated as you.
- An account that can list the installation **region's** Cloud Build records (`gcloud builds list
  --region=us-east5`: a listing with no `--region` reads the global builds, which hold no catalog build
  at all), list Cloud Run jobs, list one job's executions, read the service's own IAM policy and read
  the service's own lines out of Cloud Logging: a project Owner can.
- **Two sandbox repositories already switched and built**: one **GitHub** (`CHECK196_REPO`) and one
  **Bitbucket** (`CHECK196_BITBUCKET`), both required, each with its **catalog entry's** build submitted
  from the checkout of the entry's **source** repository — `fugaro image build <entry>` (or `fugaro
  apply`, which submits the same build); `fugaro image refresh` is refused under D1900 and builds
  nothing — which is what task B4.8 does, and what puts a build in the project's own records
  for the check to read. Each repository has to be named by the project's **effective** `image_map`
  (**D1906**: this machine's project config when it states a map, the published policy node's
  otherwise) for the entry that build carries, or — a repository the map does not name — to have a
  catalog build of its own clone URL. A repository with no such build in the installation region's last
  `CHECK196_BUILDS` (default 50) builds is a row the check **could not measure** (exit 2), not a pass.
- **Nothing else**: no token, no secret, no file to prepare. The check **submits no build of its own**
  — it reads the ones you submitted — and it holds no credential beyond the `gcloud` session it reads
  the project's records with.
- **Minutes at the terminal, not money.**

## The steps

1. **The dry run first.** It prints every read a real run would make and **calls nothing at all** — no
   `gcloud`, no `fugaro`:

   ```sh
   CHECK196_REPO=<owner>/<github-repository> \
   CHECK196_BITBUCKET=<owner>/<bitbucket-repository> \
   bash scripts/live-checks/check196.sh --dry-run
   ```

2. **The run itself** — the same command without `--dry-run`:

   ```sh
   CHECK196_REPO=<owner>/<github-repository> \
   CHECK196_BITBUCKET=<owner>/<bitbucket-repository> \
   bash scripts/live-checks/check196.sh
   ```

   Progress goes to stderr; one `CHECK 196 RESULT` block goes to stdout, at the end. **Paste that
   whole block back** and nothing else: it carries the builds' ids and statuses, the account each ran
   as, the service's own address, the names of the credentials the service served and the callers it
   verified — all names, and no credential value.

3. **After the flip** (task B4.10, once B4.9 has been applied and each repository's root re-applied
   with the switch on):

   ```sh
   CHECK196_REPO=<owner>/<github-repository> \
   CHECK196_BITBUCKET=<owner>/<bitbucket-repository> \
   bash scripts/live-checks/check196.sh --after-flip
   ```

   Paste that block back too. Its rows are the flip's own — the switch mode's rows are not printed as
   `not reached`, which would read as a reading this mode never takes.

## What each row means (the switch mode)

- **`fugaro-version`** — `fugaro version` must not be a development build: a checkout build resolves no
  released credentials-service image to deploy.
- **`project-region`** — `fugaro-dev`/`us-east5`, read off `fugaro doctor`'s own `project:` line, which
  is what says this is the installation the check is about.
- **`xtrace`** — the shell was not started with `-x`. A trace would print every command of the run,
  including the `gcloud` session's, into the log you paste.
- **`inputs`** — the two repositories, and that neither is `belong` and that they are not the same one.
- **`service`** — the credentials service **itself**, read with `gcloud run services describe` and
  measured from nothing else (no build is read for this row). It is deployed and Cloud Run serves it a
  `status.url`, which is the address every caller mints its ID token for. Not deployed, or no URL, is a
  **FAIL**; a read that failed is a row that could not be measured.
- **`build-github`**, **`build-bitbucket`** — each repository's newest **catalog entry's** build (the
  entry the project's **effective** `image_map` names for it, picked out by the catalog image in its
  `_IMAGE` substitution, out of the installation **region's** recent builds; a repository the map does
  not name is matched by its own `_REPO_URL` clone URL instead) ended
  `SUCCESS` and ran as the installation's **build identity**
  (`fugaro-build-identity@fugaro-dev.iam.gserviceaccount.com`, read off the build's own record, never
  taken on trust), which is the account `gcp.CatalogBuildSpec.check` admits. A repository-level derived
  image is disabled (**D1900**), so this build is what `fugaro image build <entry>` or `fugaro apply`
  submits; `fugaro image refresh` is refused and builds nothing.
- **`mounts-github`**, **`mounts-bitbucket`** — the catalog build's request carries **no secret at
  all**: **0** `availableSecrets` entries and **0** `secretEnv` entries, and it **does** name the
  credentials service (`_CREDENTIALS_SERVICE_URL`), because the mount went with the build identity's
  accessor on it and the credential step asks that service's **entry** route for the git credential of
  the entry it bakes, as the project's build identity — the caller that route admits (**IS8**, **D3330**;
  a submission refuses an empty address). That credential is the entry's **source repository's**, which is
  the checkout the build bakes and the served repository only where the entry is sourced from it. A build
  that still carries a mount is one from before **IS8** and is a **FAIL**, and so is one with no mount
  **and** no address: it holds no credential of any kind.
- **`served-github`**, **`served-bitbucket`** — the service's **own** `credsvc: audit` line of purpose
  `build` for each repository, read out of Cloud Logging: where one exists it must name the account the
  build's own record says it ran as (**G20**, the one field a call cannot claim for itself), the
  provider's own git credential (the App key for GitHub, a `bitbucket-token…` for Bitbucket) and the
  repository's own slug. A catalog build asks the **entry** route instead, whose lines name the project's
  build identity and **no** repository (one token covers the entry's repositories), so the line such a
  build's own call leaves cannot appear under a repository's slug and **no line is INFO**, with the reason
  in the row. A **refused** line under the repository's slug is a **FAIL**: nothing of this installation's
  names a repository on that route.
- **`check-github`**, **`check-bitbucket`** — the repository's own daily check job runs as its build
  account, still **mounts** the repository's git credential (the platform's own reserved variable for the
  provider's git secret, `FUGARO_GITHUB_APP_PRIVATE_KEY` or `FUGARO_BITBUCKET_TOKEN`) and carries **no**
  `FUGARO_CREDENTIALS_SERVICE_URL` — the shape before the flip, because the address is written only
  where the mount has been taken away — and its **newest execution** ended completed. A job that already
  carries the address reads **INFO** (the flip has landed; run `--after-flip`), a job that carries it
  *beside* a mount is a shape the installation never plans and a **FAIL**, and a job with neither a
  mount nor an address holds no credential at all and is a **FAIL**. A repository the installation
  deploys **no** check job for reads **INFO** and not a row that could not be measured: a
  repository-level image check is disabled with the repository-level images (**D1900**, IS2), so the
  installation emits none for a repository that has none.
- **`boundary`** — the service's own IAM policy admits repository build accounts and names **no**
  `allUsers` and **no** `allAuthenticatedUsers`. That, read beside the `served-*` rows — which settle
  that no line of another repository's, and no refusal, is served under this slug — is how the route's
  boundary is read from the owner's machine: a call that names another repository can only be made by a
  build **running as** that other account, which is a throwaway build rather than a reading of the
  installation, and a caller-identity reading is what Check 195 makes. **This is the one place this check
  is narrower than the plan's one sentence** ("another repository's build account is refused"): report it
  as read rather than as something the check constructed.
- **`restore`**, **`scratch-removed`** — nothing was created, submitted or deleted; the scratch
  directory is gone (or kept, if you set `CHECK196_KEEP_SCRATCH`).

## What each row means (`--after-flip`)

- **`noavailable-github`**, **`noavailable-bitbucket`** — the catalog build's request carries **0**
  `availableSecrets` entries and **0** `secretEnv` entries and **does** name the service (**IS8**,
  **D3330**): the shape of every catalog build, which the flip neither gives nor takes away. A build that
  still carries a mount, or one with neither a mount nor an address, is a **FAIL**.
- **`build-flip-github`**, **`build-flip-bitbucket`** — that build still ended `SUCCESS` and ran as the
  installation's build identity, taking its git credential from the service's entry route: nothing the
  flip took was a path this build used, and the image still bakes.
- **`served-github-flip`**, **`served-bitbucket-flip`** — the service's own line for that build, read
  exactly as the switch mode reads it.
- **`noaccessor-github`**, **`noaccessor-bitbucket`** — the repository's daily check job mounts **0**
  Secret Manager containers **and is handed `FUGARO_CREDENTIALS_SERVICE_URL` instead**: the flip drops
  the check job's `secret_key_ref` mounts and writes the address in the same breath, so the check's own
  clone and its rebuilds read their credentials from the service like the repository's builds. The two
  go together — a job that mounts nothing and names no service holds no credential at all, and reads
  **FAIL**. A repository the installation deploys no check job for reads **INFO** here too: the flip
  had no mount of its own to have taken away (**D1900**, IS2).

## If it stops before the end

A stop prints the block first, with an `aborted` fact naming the reason, and exits `1`. A row the check
could not measure — no build of a repository in the window, a log read that failed, a job or an
execution that could not be read — reads **FAIL** in the block and ends the run as exit `2`, which is
"could not measure" rather than "a criterion did not hold". The stops worth knowing by name:

- **`CHECK196_REPO` or `CHECK196_BITBUCKET` missing**, or naming `belong`, or naming the same
  repository twice — stopped before anything is read. Both are required: the reading is one repository
  of each provider.
- **This is not the `fugaro-dev` installation** — `fugaro doctor --project fugaro --all` printed no
  `project: … (GCP fugaro-dev,` line. Run doctor yourself and read its output; the check runs on the
  sandbox installation only.
- **This shell is tracing** (`-x`) — run it again without it.
- **The installation's region's builds cannot be read** — `gcloud builds list --region=us-east5` failed
  (a listing with no `--region` reads the global builds, which hold no catalog build at all). Your
  account needs `cloudbuild.builds.list` in that region, which a project Owner has; the stop prints the
  command to run by hand.
- **Cloud Run jobs cannot be read** — `gcloud run jobs list` failed (`run.jobs.list`).
- **The reader failed** — the check's own reader could not parse a document `gcloud` returned; the
  stop says so with `gcloud`'s own last lines, and this is a defect rather than a reading.

## What is left behind

**Nothing.** This check creates no cloud resource, submits no build, writes no record and deletes
nothing: the only thing its Restore removes is its own scratch directory, which the exit trap takes
too, so a Ctrl-C leaves nothing behind. The builds and the check executions it reads are the ones you
submitted (the sandbox repositories' catalog entries, with `fugaro image build <entry>` or `fugaro
apply`), and they are the project's own records rather than resources
this check made — nothing here deletes them, and `gcloud builds list --project=fugaro-dev --region=us-east5`
shows them.
`CHECK196_KEEP_SCRATCH` keeps the scratch directory (mode `0700`) when you want to read what the check
wrote; its row then says it was kept rather than that it was left behind.
