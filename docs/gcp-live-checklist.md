# GCP live checklist (M4, extended for M5)

These checks confirm the facts about Cloud Run, Cloud Logging, Cloud Storage
and Cloud Build that only a real project can show. The hermetic fakes
(`internal/gcpfake`) are written from the API documentation. These runs
confirm that the documentation was read correctly. The checks are the `live`
build-tag tests in `internal/backend/gcp/live_test.go` and
`internal/e2e/live_gcp_test.go`. They never run in CI.

Each test logs its observations as `FACT:` lines. Paste those lines into the
M4 PR description and into the runbook (`docs/gcp-bootstrap.md`).

## Guardrails the tests enforce

- **Target.** `FUGARO_LIVE_PROJECT` names the live-test GCP project and
  `FUGARO_LIVE_REPO` the sandbox repository (`owner/name`), for example
  `my-fugaro-dev` and `acme/fugaro-sandbox`. Both are required. The local
  config (`$FUGARO_CONFIG`, else `~/.config/fugaro/config.yaml`) must then
  name all of the following, or every test calls `t.Fatal` before making
  any call:
  - the same project
  - a region (the tests run in the local config's region)
  - a `fugaro-runs-*` runs bucket
  - no `bucket_url` or endpoint override
  - the same repository, under `repos`

  `GOOGLE_CLOUD_PROJECT` and `CLOUDSDK_CORE_PROJECT` may only be unset or
  the same project.
- **Credentials.**
  - Google calls use Application Default Credentials only.
  - The prefix-denial check impersonates the job's service account, starting
    from ADC.
  - The Bitbucket token comes only from `FUGARO_BITBUCKET_TOKEN`. It is used
    to read the sandbox's `fugaro.yaml` for the spend check and to clean up.
    The CLI's environment never holds it, and it is scrubbed from anything
    the tests log.
  - Nothing reads a credential from argv.
- **Cleanup comes first.** Each test registers its `t.Cleanup` before its
  first side effect. The cleanup does the following:
  - cancels an execution or build that is still going
  - declines the run's PR and deletes its `fugaro/<run-id>` branch
  - deletes the run's `runs/<slug>/<run-id>/` objects
  - deletes every `fugaro-live-*` object and secret the test made

  A `-timeout` abort skips `t.Cleanup`. `TestLiveGCPCleanup` sweeps what is
  left.
- **Spend caps.**
  - Before launching anything, the tests check that the sandbox job has at
    most 1 CPU, 2Gi of memory, a 30m task timeout and `maxRetries` 0.
  - They check that the sandbox's `fugaro.yaml` has an agent budget of at
    most $2, a total timeout of at most 20m, and no reviewers.
  - The probe execution runs with a `jobs.run` task-timeout override of 120s.
  - The probe build has a 900s timeout and pushes nothing.

  `jobs.run` can't override CPU or memory, so the sandbox's own
  `resources: {cpu: 1, memory: 2Gi}` sets the limit. The web-node base needs
  more than 512Mi to run `npm test`.

## Preconditions

Set these in the shell you run everything from, and keep that shell for the
whole bring-up:

```bash
export FUGARO_LIVE_PROJECT=<project> FUGARO_LIVE_REPO=<owner/name>
# FUGARO must be exported: the bootstrap script reads it, and would otherwise
# use whatever fugaro is on PATH.
export FUGARO=<path to the fugaro binary built from the branch under test>
export SANDBOX=<a checkout of the sandbox repository>
# The bootstrap's shared settings, the same values config wrote:
export PROJECT="$FUGARO_LIVE_PROJECT" REGION=<region> BUCKET=fugaro-runs-<suffix>
```

From M5 on, set the project up with `fugaro init` (`docs/gcp-setup.md`)
instead: the installation, then the sandbox with `fugaro init --repo`. The
checks below still apply. Checks 11 and 11b still build as the deprecated
`build.service_account` (the legacy `fugaro-build`) until the live test is
moved to the repository's own build account and registry. The bootstrap steps that
follow are the M4 path, kept for the rollback (`docs/gcp-bootstrap.md`).

Complete the bootstrap runbook (`docs/gcp-bootstrap.md`) for the sandbox
repository, in the runbook's order:

1. `config` first: every other step refuses without the local config it
   writes. `BUCKET` must start with `fugaro-runs-` (the tests refuse any
   other runs bucket, and `config` and `bucket` refuse one too; a bucket
   can't be renamed later). The sandbox's `REPOS` entry must use the base
   branch `master`, as `deploy/bootstrap/sandbox/fugaro.yaml` does, for
   example `REPOS="$FUGARO_LIVE_REPO:master:web"`.
2. The shared steps: `apis`, `bucket`, `registry`, `build-sa`.
3. **⚠ CONFIRM** Commit the sandbox fixture, `deploy/bootstrap/sandbox/`,
   to the sandbox repository's base branch (see
   `deploy/bootstrap/README.md`). This pushes to a real repository.
4. For the sandbox's `web` workflow, with
   `export REPO="$FUGARO_LIVE_REPO" WORKFLOW=web CHECKOUT="$SANDBOX"`
   set: `job-sa`, `secrets` (store every secret it prints, including
   `sandbox-probe`), `secrets-access`, `base` (then set `base_image` as the
   runbook's "Setting the base image" says), `image` and `job`.

Then set up the following:

1. **⚠ CONFIRM** Enable the IAM Credentials API, which impersonation needs.
   The bootstrap's seven APIs don't include it. Enabling it is free.

   ```bash
   gcloud services enable iamcredentials.googleapis.com --project "$FUGARO_LIVE_PROJECT"
   ```

2. **⚠ CONFIRM** Give yourself the Token Creator role on the sandbox job's
   service account (the runbook's "Live tests" section):

   ```bash
   SA=$(cd "$SANDBOX" && "$FUGARO" gcp job-spec --repo "$FUGARO_LIVE_REPO" --workflow web --field sa)
   gcloud iam service-accounts add-iam-policy-binding "$SA" \
     --member "user:$(gcloud config get account)" --role roles/iam.serviceAccountTokenCreator --project "$FUGARO_LIVE_PROJECT"
   ```

   Keep `SA` set in this shell: the run below passes it as
   `FUGARO_LIVE_JOB_SA`. Remove the grant again afterwards (see "Undo").

## Running

Pass `-p 1` so the two packages run one after the other.
`TestLiveGCPCleanup` must never run while another live test is still going,
because it would delete that test's objects.

**⚠ CONFIRM** The run spends money and changes things in the project and
the sandbox repository: it launches billable Cloud Run executions (the probe
and the sandbox run) and a Cloud Build, creates and deletes secrets and
bucket objects, and opens and declines a PR in the sandbox. Check the
project with `gcloud config get project` and `echo $FUGARO_LIVE_PROJECT`
before you start it.

```bash
FUGARO_BITBUCKET_TOKEN="$(cat <token file>)" FUGARO_LIVE_JOB_SA="$SA" \
  go test -tags live -p 1 -timeout 45m -run 'TestLive' -v ./internal/backend/gcp/ ./internal/e2e/ 2>&1 | tee live.log
grep 'FACT:' live.log
```

To run a single check, narrow `-run`. The commands are listed in the table
below.

On a fresh project `TestLiveListAndLogs` (checks 1, 1b and 2) runs before
anything has made an execution, and skips with "rerun … after the sandbox
end-to-end". Once the run above has finished, rerun it so its `FACT`s are
recorded:

```bash
go test -tags live -p 1 -v -timeout 10m -run 'TestLiveListAndLogs' ./internal/backend/gcp/ 2>&1 | tee -a live.log
```

**⚠ CONFIRM** Run the sweep after any abort. It cancels and deletes: every
live-batch run's execution, PR, branch and objects, and every
`fugaro-live-*` object and secret.

```bash
FUGARO_BITBUCKET_TOKEN="$(cat <token file>)" \
  go test -tags live -p 1 -timeout 15m -run 'TestLiveGCPCleanup' -v ./internal/e2e/
```

## Checks

In the commands below, `T` is short for
`go test -tags live -p 1 -v -timeout 20m`. Set `FUGARO_LIVE_PROJECT` and
`FUGARO_LIVE_REPO` for every check, and `FUGARO_BITBUCKET_TOKEN` and
`FUGARO_LIVE_JOB_SA` as above wherever a check needs them.

| # | Check | Command | Expected |
|---|---|---|---|
| 1 | Execution names from `executions.list` (project ID or number) | `T -run TestLiveListAndLogs ./internal/backend/gcp/`, rerun after check 13 on a fresh project | It skips while the region has no execution. A `FACT` gives the raw form (project ID or project NUMBER). `ParseExecution` accepts every raw name. Every name `List` returns starts with `projects/<project>/locations/<region>/jobs/`. |
| 1b | The `jobs/-` listing is sorted newest first across every job, not only per job | same test | A `FACT` says `newest first across N jobs (global)`. `List` stops at the first execution older than `Since` on that assumption, and so does the `max_parallel` check, at a horizon of the longest task timeout among the region's `fugaro-` jobs (or the 24-hour override cap, whichever is longer) plus the task slack and an hour; `ls` falls back to one read per run if the assumption is wrong. A listing that is not sorted across jobs fails the test. Telling needs executions of at least two jobs (any jobs in the region) on the first page of 20; with fewer, the `FACT` says it can't tell, and the question stays open in the PR until a project with two jobs shows it. |
| 2 | The log filter `labels."run.googleapis.com/execution_name"` works | same test | The newest Fugaro execution has at least 1 log entry, and at most 10 are read. It fails if that execution has none, and skips (rerun it after check 13) if there is no Fugaro execution yet. |
| 3 | The operation metadata of `jobs.run` is an Execution | `T -run TestLiveExecutionProbe ./internal/backend/gcp/` | `metadata @type="type.googleapis.com/google.cloud.run.v2.Execution"`, and `name` is a parseable execution. The probe's `task.json` has version 999, so the runner exits at once without cloning or starting an agent. |
| 4 | The forms Cloud Run returns for CPU and memory limits | same test | `FACT` lines give the `jobs.get` and `executions.get` limits, for example `cpu="1"` or `"1000m"` and `memory="2Gi"`. The backend parses them to CPU > 0 and MemoryGiB > 0. It fails if the cost would be unknown. |
| 5 | What cancelling a finished execution returns | same test | The HTTP code, status and message are recorded, and the state stays terminal. Expect a `FAILED_PRECONDITION` (400). `cancel` must tolerate this, since a run can finish between its read and the cancel. |
| 6 | The log label key, read raw | same test | A raw entry of the probe execution has the `run.googleapis.com/execution_name` label, and the backend's `Logs`, whose filter also pins `resource.labels.location` to the region, finds at least 1 entry. |
| 7 | The IAM prefix condition denies the job SA outside its prefixes | `T -run TestLiveJobSADeniedOutsideItsPrefixes ./internal/backend/gcp/` | Acting as the job SA: a write and a delete under its own `runs/`, `cache/` and `locks/` `<slug>/` prefixes are allowed. Writes to `runs/<slug>-x/…` and `runs/other/…` get 403. A read of `runs/other/…/task.json` gets 403. Listing `runs/` gets 403. The test skips unless `FUGARO_LIVE_JOB_SA` is set, and fails if it names any other SA. |
| 8 | Secret Manager set, list and delete | `T -run TestLiveSecretsRoundTrip ./internal/backend/gcp/` | `Set` returns version `"1"`. `List` by labels shows 1 version with the matching latest. `Delete` succeeds. The value is random and never logged. |
| 9 | The lock's generation preconditions on real GCS | `T -run TestLiveLockOnGCS ./internal/backend/gcp/` | A second holder gets `BusyError`, an expired takeover succeeds, and the old holder's `Release` leaves the new lock in place. The final `Release` deletes it. |
| 10 | Cache save and restore, and `customTime` | `T -run TestLiveCacheOnGCS ./internal/backend/gcp/` | The 1 MiB archive is saved with a non-zero `customTime` and restores byte for byte. |
| 11 | Cloud Build honours a BuildKit file secret (`--secret id=…,src=…`) in `gcr.io/cloud-builders/docker`, under the derived template's pinned `# syntax=` frontend | `T -run TestLiveCloudBuildSecretAndDigest ./internal/backend/gcp/` | The build succeeds, running as `build.service_account`, and its log has `LIVE secret-mounted`. That line comes from `RUN --mount=type=secret,id=SANDBOX_PROBE,uid=1000,mode=0400,required=true`. Cloud Build's docker does not support `env=` secret mounts: the first live derived build failed with `requested experimental feature exec.secretenv is not supported by build server`, so neither `cloudbuild.yaml` nor the template uses `env=` (design §7.2). |
| 11b | Repository code can't reach the metadata server (design §7.2) | same test | `FACT` lines give each probe's outcome for the token endpoint, by name and by address. The control, a Cloud Build step on the `cloudbuild` network, is `REACHABLE`. A `RUN` on BuildKit's default network is `blocked`, and a `RUN --network=host` is `blocked` or `build-refused`. Any `REACHABLE` from a `RUN` fails the test as `BOUNDARY BROKEN`: stop, and don't onboard an untrusted repository until builds use a builder that denies `network.host` (design §7.2). **M5 adds two probes,** the path the build's smoke test takes: the same token endpoint, by name and by address, from `docker run --network none <image>` and from a plain `docker run <image>` (default network), inside a Cloud Build step. Both must be `blocked`; a `REACHABLE` from either means the smoke test, which runs repository code, could reach the build account's token, and the build must not be onboarded until the smoke's isolation is fixed. Until the test itself runs these probes, run them by hand in a scratch build and record the `FACT`s. |
| 12 | How `FROM repo@sha256` resolves | same test | `FACT` lines give the pinned `repo@sha256:<64 hex>`, BuildKit's `load metadata` and `FROM` or `resolve` lines, and whether the registry was contacted for metadata. |
| 13 | The sandbox end to end: run, `ls`, cost, `logs`, `diagnose`, objects | `FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 45m -run TestLiveSandboxRun ./internal/e2e/` | See below. |
| 14 | Sweep | `FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 15m -run TestLiveGCPCleanup ./internal/e2e/` | It cancels, declines, deletes and logs every live-batch run, every `fugaro-live-*` prefix and every `fugaro-live-*` secret, and fails nothing. |
| 15 | The GitHub credential mint (run only if a GitHub sandbox repository and App exist) | Store the App's private key with `fugaro secrets set github-app-key`, run `fugaro init --repo --github-app-id <id>` in the sandbox checkout, and let it submit the first build | The `credential` step's log shows `fugaro image git-credential` minting a token and no value; `source` clones the repository with it; the build succeeds. The minted token carries `contents: read` and `metadata: read` only (check the App's token request in the GitHub App's advanced log) and lives at least 30 minutes, which covers the clone. A `FACT` records the outcome. Skip, and say so in the PR, when there is no GitHub sandbox: M5 then ships the GitHub build path tested hermetically only. |
| 16 | The daily image check: a skipped check, a back-off after a forced failure, and one forced rebuild | With the sandbox's schedule unpaused (its `rebuild.check` is `daily` and its record exists): `fugaro image check --dry-run` from its checkout; then `gcloud scheduler jobs run <scheduler job> --location <scheduler region>` (from `fugaro init --repo --print-vars`, or `gcloud scheduler jobs list --location <scheduler region>`). To force a failure, temporarily disable the `bitbucket-token` secret's latest version and run the Scheduler job again, then re-enable the version. For the forced rebuild, run `fugaro image build`, which submits the request a fired trigger submits | (a) With nothing changed, the local check prints `skip`. The check job logs `decision: skip` for each workflow, `check.json` appears next to `image.json`, and no build starts. (b) With the credential disabled, the check job logs `decision: check-failed` at ERROR and exits 2, `fugaro ls` prints the check warning, and the alert email arrives if one is configured (which also proves the alert sees lines that log isolation routed away from `_Default`). After a failed *rebuild* whose inputs haven't changed, the next check logs `rebuild-failed-last` and submits no build. (c) The forced rebuild runs candidate, smoke, gate, promote and record: `latest` moves to the record's `image_digest`, and `fugaro image status` shows the new build time. A leftover `candidate-` tag with an untag warning in the build log is expected. Record each as a `FACT`, and re-enable the secret version. |
| 17 | The registry cleanup policy's dry run never selects a `latest` or `dev-` version | A day or more after the installation and the first builds exist, read the Artifact Registry audit log (Cloud Logging, `protoPayload.serviceName="artifactregistry.googleapis.com"`) for what the cleanup policies of `fugaro-base` and each repository's registry would delete in dry run, and list each registry's tags with `gcloud artifacts docker tags list` | No version the dry run would delete is tagged `latest` or `dev-`, and none of a registry's three newest versions is. If one is, stop: the keep rules are wrong. Otherwise turn deletion on (gcp-setup.md, "Turning registry cleanup on"). Record a `FACT` naming what the dry run would delete. |
| 18 | A push to the base branch during an image build does not fail the smoke (Bitbucket fetch-by-SHA of the pinned commit) | From the sandbox's checkout, start `fugaro image build`; while its render step has finished and the build step hasn't cloned yet, push a commit to the base branch (or force-push it back one commit, so the pinned commit is no longer the branch head) | The build clones, resets to the commit the render step read (fetching it by SHA when the clone lacks it), and the smoke, gate, promote and record succeed. The record's `source_commit`, `/etc/fugaro/image.json` and the image's revision label name that same commit, not the new head. If the provider refuses the fetch of an unadvertised commit, the build fails at the clone step: record that as a `FACT`. |

For check 13, `TestLiveSandboxRun` checks the following:

- `run --json` reports `launched`, run `<slug>/<id>` and branch `fugaro/<id>`.
- `ls --batch live-<stamp>` reaches a terminal `succeeded` or `failed` with a
  PR URL within 30m. It is polled every 20s.
- `cost.model_basis` is `subscription`, `compute_usd` is above 0, and
  `total_usd` is `compute_usd` rounded to the cent. `totals.total_usd` is
  within 0.01 of the sum of the rows.
- `logs --json` holds a `stage started` line and an `"event":"tool"` entry.
- `diagnose --json` gives the same PR URL.
- `result.json` has `stage: writeback`, and its execution matches
  `launch.json`'s.
- The branch lock is gone.
- A `cache/<slug>/web/*.tar.zst` archive exists.
- Cleanup declines the PR, deletes the branch and deletes the run's objects.

## Not covered by these tests (manual)

- **Live cancel of a running run.** The hermetic `TestCloudCancel` covers the
  logic. A live check would hold a 15-minute sleep on the subscription, so
  it is a manual spot check.
- **Cloud Build's docker has no `env=` secret mounts.** A `RUN --mount=type=secret,…,env=X` fails there with `requested experimental feature exec.secretenv is not supported by build server`. The derived template mounts workflow secrets as files and exports them inside the RUN instead; keep it that way.
- **The full derived-image build.** The runbook's `image` step builds it for
  real, and its `image.setup` step fails without `SANDBOX_PROBE`. Check 11
  isolates the same mechanism.

## Undo

The bring-up leaves two temporary changes. Undo them once the checks are
recorded:

1. **⚠ CONFIRM** Remove your Token Creator grant on the sandbox job's
   service account:

   ```bash
   gcloud iam service-accounts remove-iam-policy-binding "$SA" \
     --member "user:$(gcloud config get account)" --role roles/iam.serviceAccountTokenCreator --project "$FUGARO_LIVE_PROJECT"
   ```

2. **⚠ CONFIRM**, optional: disable the IAM Credentials API if nothing
   else uses it: `gcloud services disable iamcredentials.googleapis.com
   --project "$FUGARO_LIVE_PROJECT"`.

Keep the rest for M5, which takes the bootstrap's resources over under the
same names: the bucket, the registry, `fugaro-build`, the sandbox job, its
service account and its secrets, and the sandbox fixture in its repository.
Run the sweep (above) if an aborted run left anything behind. To remove
everything instead, use the runbook's "Teardown".

## Results of the first live run (2026-09-28)

These are the first live run's findings. Record every later run in the same way, and update this section if anything changes.

- **Execution names:** `jobs.run`'s operation metadata is a `google.cloud.run.v2.Execution`. `executions.list` returns names that use the project ID, not the project number.
- **Log labels:** log entries carry `run.googleapis.com/execution_name`, plus the job's `fugaro`, `fugaro_repo` and `fugaro_workflow` labels.
- **Limits:** `jobs.get` and `executions.get` report limits as `cpu="1"` and `memory="2Gi"`.
- **Cancelling a finished execution:** it returns HTTP 400 `FAILED_PRECONDITION`, "… cannot be cancelled because it is not running", and the execution's state is unchanged.
- **Listing order:** `jobs/-` ordering across jobs wasn't shown, because the region held only one job. The check is still open.
- **Storage IAM:** the job's service account can write and delete under its own `runs/`, `cache/` and `locks/` prefixes. It gets 403 on another slug's prefix (including `<slug>-x`), on reading another repository's objects, and on listing the bucket.
- **Secret Manager:** a round-trip works, and the label filter finds the secret.
- **Lock:** a second holder is refused while the lock is live. A takeover of an expired lock and the release both work with generation-matched writes, and a late release by the old holder leaves the new lock in place.
- **Cache:** the archive is saved with `application/zstd` and a `customTime`, and restores byte-identical.
- **Cloud Build:**
  - The docker daemon is 20.10.24, and it doesn't support `env=` secret mounts (`exec.secretenv`), so secrets are mounted as files.
  - A `src=` secret is mounted as a file.
  - `FROM repo@sha256` resolves through the registry.
  - The metadata server is reachable from a build step, which is the control, but blocked from a `RUN` on the default network. A `RUN --network=host` build is refused.
- **Sandbox end to end:** one run succeeded and ended ready in about 44 seconds. Compute cost $0.0008, and the notional subscription model cost was $0.30. `total_usd` was 0, as designed for `oauth`. The PR was declined and the branch deleted.
- **Test fixes found:**
  - The first `jobs.run` of this project rejected an unused Cloud Build substitution.
  - The live cache test used `Attributes.As` with the wrong pointer type.
  - The impersonation check failed until the new Token Creator grant had propagated (a few minutes); rerun it if it fails right after the grant.

## Results of the second live run (2026-09-29)

- **Listing order (check 1b):** with two jobs in the region, `executions.list` on `jobs/-` returns executions newest first across jobs. The early stop in the listing code is sound.
- **First run on the web repo:** a task to add two unit-test files ended in a ready PR into `master`, with the default reviewer, no labels and only the two new files changed.
  - It took 22 minutes. The container started about two minutes after launch, the first pull of the 5.5 GB image included.
  - Compute cost was $0.11, with $0.53 of notional model usage on the subscription.
  - Both verify passes succeeded: `build` (lint and typecheck) and `test` (7,886 tests, 0 failures).
  - The lock was released. The bucket holds the run's records and redacted transcripts, plus a 434 MB dependency cache archive.
- **Web repo image build:** with `image.skip_build_scripts`, `yarn install` finished in 1 minute 4 seconds. A plain `yarn install --immutable` in the same builder hung for 57 minutes and ended `INTERNAL_ERROR`. The whole Cloud Build took about 12 minutes.
- **Claude Code in the base image:** `claude install` fetches the current release, not the binary it is run from, so the base image now installs the sha256-verified binary directly and fails unless `claude --version` is the pinned version.

## Results of the third live run (M5)

To be filled in by the live bring-up and migration (gcp-setup.md, "Adopting an M4 installation"). Record every `FACT` and each plan summary in the same way as the runs above. Each item names the check that produces it.

- **The project's default build identity** (gcp-setup.md, precondition 9): the account `gcloud builds get-default-service-account` names, its roles, and whether the leaked-build-token path of design §6.1 is real in this project.
- **The installation plan (adoption):** the imports, creates and in-place updates the plan showed, and that it had zero deletes; that `plan -detailed-exitcode` exits 2 for an import-only plan.
- **Log isolation:** a new Fugaro job line lands in the `fugaro` log bucket and not in `_Default`; the project exclusion applies to `_Default`; `fugaro logs` reads through the view; adopted M4 jobs already carry template labels, so the sink's filter matches them; and the failure alert fires for a line the exclusion routed away (check 16b).
- **Sandbox adoption:** the job, account and secrets imported; the live bucket condition still one binding, not two; the job's in-place diffs (container name, CPU form, annotations); the legacy image and display name kept until the first build.
- **The build (check 11, 11b):** `install -o 1000` on the credential volume works and the credential step can write there; `$BUILDER_OUTPUT` is writable by the docker and `cloud-sdk` builders; `docker run <image>@<digest>` uses the local image with no pull; `gcloud artifacts docker tags add` by digest works with `writer`; the untag is refused with a warning; the two `docker run` probes are `blocked`.
- **The GitHub mint (check 15):** the result, or "no GitHub sandbox".
- **A fresh repository's first `init --repo`:** the first `init --repo` on a fresh repo does not fail on the empty credential secret: it plans no check job, invoker grant or Scheduler job while the provider credential has no version, and the rerun after `fugaro secrets set` deploys them (paused until a build record exists).
- **The daily check (check 16):** the skip, the back-off, the forced rebuild, the metadata-server token accepted by the registry for the manifest `HEAD`, and blobless clones with both providers' credential helpers.
- **Cleanup (check 17):** what the dry run would delete.
- **The pinned commit (check 18):** whether a push to the base branch during an image build left the smoke passing, and whether the provider served the fetch of the pinned commit by SHA.
- **A real run:** its `ls --json` row's `image_age_s`, and that `--total-timeout 15m` gave the execution a 17-minute timeout.
- **Retirement:** `fugaro-build`'s bindings removed, disabled, deleted.
