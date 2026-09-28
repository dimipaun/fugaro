# GCP live checklist (M4)

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

Complete the bootstrap runbook (`docs/gcp-bootstrap.md`) for the sandbox
repository, in the runbook's order:

1. `config` first: every other step refuses without the local config it
   writes.
2. The shared steps: `apis`, `bucket`, `registry`, `build-sa`.
3. Commit the sandbox fixture, `deploy/bootstrap/sandbox/`, to the sandbox
   repository's base branch (see `deploy/bootstrap/README.md`).
4. For the sandbox's `web` workflow, with `REPO`, `WORKFLOW` and `CHECKOUT`
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
   SA=$(cd <sandbox checkout> && "$FUGARO" gcp job-spec --repo "$FUGARO_LIVE_REPO" --workflow web --field sa)
   gcloud iam service-accounts add-iam-policy-binding "$SA" \
     --member "user:$(gcloud config get account)" --role roles/iam.serviceAccountTokenCreator --project "$FUGARO_LIVE_PROJECT"
   ```

   Remove it again afterwards with `remove-iam-policy-binding` and the same
   arguments, as the runbook's "Live tests" section says.

## Running

Pass `-p 1` so the two packages run one after the other.
`TestLiveGCPCleanup` must never run while another live test is still going,
because it would delete that test's objects.

```bash
export FUGARO_LIVE_PROJECT=<project> FUGARO_LIVE_REPO=<owner/name>
FUGARO_BITBUCKET_TOKEN="$(cat <token file>)" FUGARO_LIVE_JOB_SA="$SA" \
  go test -tags live -p 1 -timeout 45m -run 'TestLive' -v ./internal/backend/gcp/ ./internal/e2e/ 2>&1 | tee live.log
grep 'FACT:' live.log
```

To run a single check, narrow `-run`. The commands are listed in the table
below. Run the sweep after any abort:

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
| 1 | Execution names from `executions.list` (project ID or number) | `T -run TestLiveListAndLogs ./internal/backend/gcp/` | A `FACT` gives the raw form (project ID or project NUMBER). `ParseExecution` accepts every raw name. Every name `List` returns starts with `projects/<project>/locations/<region>/jobs/`. |
| 1b | The `jobs/-` listing is sorted newest first across every job, not only per job | same test | A `FACT` says `newest first across N jobs (global)`. `List` stops at the first execution older than `Since` on that assumption; `ls` falls back to one read per run if it is wrong, and `max_parallel` lists exhaustively either way. A listing that is not sorted across jobs fails the test. Telling needs executions of at least two jobs (any jobs in the region) on the first page of 20; with fewer, the `FACT` says it can't tell, and the question stays open in the PR until a project with two jobs shows it. |
| 2 | The log filter `labels."run.googleapis.com/execution_name"` works | same test | The newest Fugaro execution has at least 1 log entry, and at most 10 are read. It fails if there are none. |
| 3 | The operation metadata of `jobs.run` is an Execution | `T -run TestLiveExecutionProbe ./internal/backend/gcp/` | `metadata @type="type.googleapis.com/google.cloud.run.v2.Execution"`, and `name` is a parseable execution. The probe's `task.json` has version 999, so the runner exits at once without cloning or starting an agent. |
| 4 | The forms Cloud Run returns for CPU and memory limits | same test | `FACT` lines give the `jobs.get` and `executions.get` limits, for example `cpu="1"` or `"1000m"` and `memory="2Gi"`. The backend parses them to CPU > 0 and MemoryGiB > 0. It fails if the cost would be unknown. |
| 5 | What cancelling a finished execution returns | same test | The HTTP code, status and message are recorded, and the state stays terminal. Expect a `FAILED_PRECONDITION` (400). `cancel` must tolerate this, since a run can finish between its read and the cancel. |
| 6 | The log label key, read raw | same test | A raw entry of the probe execution has the `run.googleapis.com/execution_name` label, and the backend's `Logs`, whose filter also pins `resource.labels.location` to the region, finds at least 1 entry. |
| 7 | The IAM prefix condition denies the job SA outside its prefixes | `T -run TestLiveJobSADeniedOutsideItsPrefixes ./internal/backend/gcp/` | Acting as the job SA: a write and a delete under its own `runs/`, `cache/` and `locks/` `<slug>/` prefixes are allowed. Writes to `runs/<slug>-x/…` and `runs/other/…` get 403. A read of `runs/other/…/task.json` gets 403. Listing `runs/` gets 403. The test skips unless `FUGARO_LIVE_JOB_SA` is set, and fails if it names any other SA. |
| 8 | Secret Manager set, list and delete | `T -run TestLiveSecretsRoundTrip ./internal/backend/gcp/` | `Set` returns version `"1"`. `List` by labels shows 1 version with the matching latest. `Delete` succeeds. The value is random and never logged. |
| 9 | The lock's generation preconditions on real GCS | `T -run TestLiveLockOnGCS ./internal/backend/gcp/` | A second holder gets `BusyError`, an expired takeover succeeds, and the old holder's `Release` leaves the new lock in place. The final `Release` deletes it. |
| 10 | Cache save and restore, and `customTime` | `T -run TestLiveCacheOnGCS ./internal/backend/gcp/` | The 1 MiB archive is saved with a non-zero `customTime` and restores byte for byte. |
| 11 | Cloud Build honours BuildKit `--secret id=…,env=…` in `gcr.io/cloud-builders/docker` | `T -run TestLiveCloudBuildSecretAndDigest ./internal/backend/gcp/` | The build succeeds, running as `build.service_account`, and its log has `LIVE secret-mounted`. That line comes from `RUN --mount=type=secret,id=SANDBOX_PROBE,required=true`. |
| 11b | Repository code can't reach the metadata server (design §7.2) | same test | `FACT` lines give each probe's outcome for the token endpoint, by name and by address. The control, a Cloud Build step on the `cloudbuild` network, is `REACHABLE`. A `RUN` on BuildKit's default network is `blocked`, and a `RUN --network=host` is `blocked` or `build-refused`. Any `REACHABLE` from a `RUN` fails the test as `BOUNDARY BROKEN`: stop, and don't onboard a second, untrusted repository until builds get per-repository service accounts (M5) or a builder that denies `network.host` (design §7.2). |
| 12 | How `FROM repo@sha256` resolves | same test | `FACT` lines give the pinned `repo@sha256:<64 hex>`, BuildKit's `load metadata` and `FROM` or `resolve` lines, and whether the registry was contacted for metadata. |
| 13 | The sandbox end to end: run, `ls`, cost, `logs`, `diagnose`, objects | `FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 45m -run TestLiveSandboxRun ./internal/e2e/` | See below. |
| 14 | Sweep | `FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 15m -run TestLiveGCPCleanup ./internal/e2e/` | It cancels, declines, deletes and logs every live-batch run, every `fugaro-live-*` prefix and every `fugaro-live-*` secret, and fails nothing. |

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
- **The full derived-image build.** The runbook's `image` step builds it for
  real, and its `image.setup` step fails without `SANDBOX_PROBE`. Check 11
  isolates the same mechanism.
