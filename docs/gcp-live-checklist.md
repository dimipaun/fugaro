# GCP live checklist (M4, extended for M5)

These checks confirm the facts about Cloud Run, Cloud Logging, Cloud Storage
and Cloud Build that only a real project can show. The hermetic fakes
(`internal/gcpfake`) are written from the API documentation. These runs
confirm that the documentation was read correctly. The checks are the `live`
build-tag tests in `internal/backend/gcp/live_test.go` and
`internal/e2e/live_gcp_test.go`. They never run in CI.

Each test logs its observations as `FACT:` lines. Paste those lines into the
PR description and into this checklist's results, at the end.

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
  - Check 11's cleanup deletes the image it pushed with your own ADC, not the
    build account's (which may push but not delete). That needs
    `artifactregistry.versions.delete` on the repository's registry: project
    owner or editor, or `roles/artifactregistry.repoAdmin` on that registry.
    Without it the cleanup fails the test and names the image to delete.
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
export FUGARO=<path to the fugaro binary built from the branch under test>
export SANDBOX=<a checkout of the sandbox repository>
```

Set the project up with `fugaro init` (`docs/gcp-setup.md`), using
`"$FUGARO"`:

1. The installation (steps 1 to 3 of the runbook). The runs bucket must be
   named `fugaro-runs-*` (the tests refuse any other runs bucket, and a
   bucket can't be renamed later).
2. **⚠ CONFIRM** Commit the sandbox fixture, `deploy/sandbox/`, to the
   sandbox repository's base branch, `master`, as its `fugaro.yaml` says.
   This pushes to a real repository.
3. The sandbox, from `$SANDBOX` (step 4 of the runbook): `fugaro init
   --repo`, then store every secret it lists, including `sandbox-probe`
   (any random value), then rerun it for the first image build and the
   switch to the new image.

Checks 11 and 11b build as the repository's own build account, into its own
registry; only a local config with no installation (no
`terraform.state_bucket`) that still names the deprecated
`build.service_account` builds as that account, into the legacy registry.

Then set up the following:

1. **⚠ CONFIRM** Enable the IAM Credentials API, which impersonation needs.
   `fugaro init` doesn't enable it. Enabling it is free.

   ```bash
   gcloud services enable iamcredentials.googleapis.com --project "$FUGARO_LIVE_PROJECT"
   ```

2. **⚠ CONFIRM** Give yourself the Token Creator role on the sandbox job's
   service account. Its account ID comes from the Terraform variables
   `fugaro init --repo --print-vars` prints (no cloud calls; its warning goes
   to stderr):

   ```bash
   SA="$("$FUGARO" init --repo "$SANDBOX" --print-vars | jq -r '.repo.workflows.web.service_account.account_id')@$FUGARO_LIVE_PROJECT.iam.gserviceaccount.com"
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
| 11 | Cloud Build honours a BuildKit file secret (`--secret id=…,src=…`) in `gcr.io/cloud-builders/docker`, under the derived template's pinned `# syntax=` frontend | `T -run TestLiveCloudBuildSecretAndDigest ./internal/backend/gcp/` | The build succeeds, running as the repository's build account (the one `fugaro image build` uses), and its log has `LIVE secret-mounted`. That line comes from `RUN --mount=type=secret,id=SANDBOX_PROBE,uid=1000,mode=0400,required=true`. Cloud Build's docker does not support `env=` secret mounts: the first live derived build failed with `requested experimental feature exec.secretenv is not supported by build server`, so neither `cloudbuild.yaml` nor the template uses `env=` (design §7.2). The build pushes its image as `candidate-<build ID>` to the workflow's image in the repository's registry (a `FACT` gives `<image>@sha256:…`), and the cleanup deletes it with your credentials (see Credentials above); it refuses, and fails, if a tag other than a `candidate-` one points at that version. A cancelled or aborted build whose push finishes after the cleanup leaves its candidate behind: delete it by hand. |
| 11b | Repository code can't reach the metadata server (design §7.2) | same test | `FACT` lines give each probe's outcome for the token endpoint, by name and by address. The control, a Cloud Build step on the `cloudbuild` network, is `REACHABLE`. A `RUN` on BuildKit's default network is `blocked`, and a `RUN --network=host` is `blocked` or `build-refused`. Any `REACHABLE` from a `RUN` fails the test as `BOUNDARY BROKEN`: stop, and don't onboard an untrusted repository until builds use a builder that denies `network.host` (design §7.2). One more probe takes the smoke test's path: the pushed candidate, by digest, under `docker run --network none`, from a step on the smoke's builder. An additional probe runs the same candidate under a plain `docker run`, on docker's default network, which the smoke never uses. Both must be `blocked`, and a `REACHABLE` from either fails the test as `BOUNDARY BROKEN`. From `--network none`, it means the smoke test, which runs repository code, could reach the build account's token; from the default network, it means any `docker run` of repository code in a build step could. Either way, don't onboard an untrusted repository until that path is isolated. |
| 12 | How `FROM repo@sha256` resolves | same test | `FACT` lines give the pinned `repo@sha256:<64 hex>`, BuildKit's `load metadata` and `FROM` or `resolve` lines, and whether the registry was contacted for metadata. |
| 13 | The sandbox end to end: run (with a `--total-timeout 15m` override, and the execution's 17m task timeout), `ls`, cost, `logs`, `diagnose`, objects | `FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 45m -run TestLiveSandboxRun ./internal/e2e/` | See below. |
| 14 | Sweep | `FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 15m -run TestLiveGCPCleanup ./internal/e2e/` | It cancels, declines, deletes and logs every live-batch run, every `fugaro-live-*` prefix and every `fugaro-live-*` secret, and fails nothing. |
| 15 | The GitHub credential mint (run only if a GitHub sandbox repository and App exist) | Store the App's private key with `fugaro secrets set github-app-key`, run `fugaro init --repo --github-app-id <id>` in the sandbox checkout, and let it submit the first build | The `credential` step's log shows `fugaro image git-credential` minting a token and no value; `source` clones the repository with it; the build succeeds. The minted token carries `contents: read` and `metadata: read` only (check the App's token request in the GitHub App's advanced log) and lives at least 30 minutes, which covers the clone. A `FACT` records the outcome. Skip, and say so in the PR, when there is no GitHub sandbox: M5 then ships the GitHub build path tested hermetically only. |
| 16 | The daily image check: a skipped check, a back-off after a forced failure, and one forced rebuild | With the sandbox's schedule unpaused (its `rebuild.check` is `daily` and its record exists): `fugaro image check --dry-run` from its checkout; then `gcloud scheduler jobs run <scheduler job> --location <scheduler region>` (from `fugaro init --repo --print-vars`, or `gcloud scheduler jobs list --location <scheduler region>`). To force a failure, temporarily disable the `bitbucket-token` secret's latest version and run the Scheduler job again, then re-enable the version. For the forced rebuild, run `fugaro image build`, which submits the request a fired trigger submits | (a) With nothing changed, the local check prints `skip`. The check job logs `decision: skip` for each workflow, `check.json` appears next to `image.json`, and no build starts. (b) The check job mounts the credential, so with its version disabled the execution fails at container start ("Failed to access secret ... Secret Version ... disabled"), before the check can log a decision or write `check.json`. Cloud Run records that only as an ERROR audit system event (`cloudaudit.googleapis.com%2Fsystem_event`, "Execution ... has failed to complete, 0/1 tasks were a success"), not in its system log, and the "Fugaro image check job failed" alert fires from that entry: the email arrives if one is configured, with log isolation on. A failure after start is covered by the check's own `decision: check-failed` line at ERROR (exit 2, the `fugaro ls` check warning and the "Fugaro image check failed" alert) and by the Cloud Run system log's ERROR lines. After a failed *rebuild* whose inputs haven't changed, the next check logs `rebuild-failed-last` and submits no build. (c) The forced rebuild runs candidate, smoke, gate, promote and record: `latest` moves to the record's `image_digest`, and `fugaro image status` shows the new build time. A leftover `candidate-` tag with an untag warning in the build log is expected. Record each as a `FACT`, and re-enable the secret version. |
| 17 | The registry cleanup policy's dry run never selects a `latest` or `dev-` version | A day or more after the installation and the first builds exist, read the Artifact Registry audit log (Cloud Logging, `protoPayload.serviceName="artifactregistry.googleapis.com"`) for what the cleanup policies of `fugaro-base` and each repository's registry would delete in dry run, and list each registry's tags with `gcloud artifacts docker tags list` | No version the dry run would delete is tagged `latest` or `dev-`, and none of a registry's three newest versions is. If one is, stop: the keep rules are wrong. Otherwise turn deletion on (gcp-setup.md, "Turning registry cleanup on"). Record a `FACT` naming what the dry run would delete. |
| 18 | A push to the base branch during an image build does not fail the smoke (Bitbucket fetch-by-SHA of the pinned commit) | From the sandbox's checkout, start `fugaro image build`; while its render step has finished and the build step hasn't cloned yet, push a commit to the base branch (or force-push it back one commit, so the pinned commit is no longer the branch head) | The build clones, resets to the commit the render step read (fetching it by SHA when the clone lacks it), and the smoke, gate, promote and record succeed. The record's `source_commit`, `/etc/fugaro/image.json` and the image's revision label name that same commit, not the new head. If the provider refuses the fetch of an unadvertised commit, the build fails at the clone step: record that as a `FACT`. |

For check 13, `TestLiveSandboxRun` checks the following:

- `run --total-timeout 15m --json` reports `launched`, run `<slug>/<id>` and
  branch `fugaro/<id>`. 15m is below the sandbox's own `timeouts.total`, so
  it is a real override.
- The execution's task timeout is the override plus the task-timeout slack:
  17m (a `FACT` line records it).
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
- **The full derived-image build.** The sandbox's first image build (`fugaro
  init --repo`, bring-up step 3) builds it for real, and its `image.setup` step fails without `SANDBOX_PROBE`. Check 11
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

Keep the rest for the next live run: the installation, the sandbox's
resources, and the sandbox fixture in its repository. Run the sweep (above)
if an aborted run left anything behind. There is no teardown command; see
`docs/gcp-setup.md`, "Offboarding a repository".

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

### Recorded so far (2026-09-29, the dev project)

The installation and the sandbox are migrated; the web repo is applied and its first build is under way. What the run showed:

- **Two bugs found and fixed by the live run.** Neither changed anything in the project before it failed.
  - `fugaro init` read the project number through Cloud Resource Manager, which was disabled, and stopped with a 403 `SERVICE_DISABLED` before doing anything. `init` now offers to enable the API (its own confirmation) and reads other disabled APIs' resources as missing. After the enable the read succeeded at once, so the propagation delay is unmeasured.
  - Cloud Monitoring refuses a policy that has a log-match condition next to another condition. The first apply created everything else and stopped at the alert; the image alert is now two policies, and a text test pins the rule. A rerun applied the rest, and the next plan showed no changes.
- **The default build identity** is the Compute Engine default account, and it holds `roles/editor`, so the escalation path of §6.1 is real in this project. The owner accepted it: per-repository builds run as their own build accounts and never use it. It stays a known exposure for anyone who can submit a build without naming an account.
- **The forced failure alert (check 16b):** disabling the latest version of a repository's Git token made the check job fail at container start (Cloud Run couldn't read the secret). Cloud Run logged that only as an audit system event, `Execution ... has failed to complete, 0/1 tasks were a success`, not in its `varlog/system` log, so the alert's second condition missed it. The alert now matches ERROR entries in either log. Rerun the forced failure after applying the change to confirm the email.
- **The installation plan:** 2 imports (the runs bucket and the legacy registry, labels only), 28 creates, 2 in-place updates, 0 deletes. Removing project Viewers' read access to the runs bucket was confirmed and done.
- **The sandbox plan:** 5 imports (the job, its account, three secrets), 24 creates, 4 label-only updates, 0 deletes; 4 live IAM bindings adopted, none duplicated; the runs bucket still has one conditional binding for the sandbox account; the job kept `maxRetries: 0`, its account and its legacy image until the second apply, which changed only the image.
- **The sandbox's first build** ran as the sandbox's own build account into its own registry and succeeded: `latest` points at the built digest, the record exists (`fugaro image status` shows it with the base digest), and a `candidate-` tag was left behind, which is expected.
- **Log isolation (check 6):** a new run's stdout and stderr appeared in the `fugaro` log bucket (readable through the view) and not in `_Default`, which held only the Cloud Run system line, as designed. The adopted job's template labels did reach the log entries, so the sink filter matched. A run from before isolation prints the hint that its lines are in `_Default`.
- **The job account's prefix conditions (check 7):** pass on the adopted binding. The sandbox job account can write and delete under its own `runs/`, `cache/` and `locks/` prefixes and gets 403 on a sibling prefix, another repository's prefix, a read of another run's `task.json`, and listing `runs/`.
- **The sandbox run** launched on the new image and got as far as the agent, which failed at once with "You've hit your org's monthly spend limit": a model-account limit, unrelated to the infrastructure. The run opened a draft PR, wrote its result and cache, and the test's cleanup declined the PR and deleted the branch. Rerun `TestLiveSandboxRun` after the limit is raised.
- **The web repo's plan:** 4 imports, 23 creates, 3 label-only updates, 0 deletes, 3 bindings adopted; the check job, its invoker grant and a Scheduler job in the scheduler region, which stayed `PAUSED`; the job stayed on its legacy image. `fugaro image check --dry-run` printed `rebuild (no-record)`, as it must for a repository without a record.

Still to record: the items below that this list doesn't cover.

- **The project's default build identity** (gcp-setup.md, precondition 9): the account `gcloud builds get-default-service-account` names, its roles, and whether the leaked-build-token path of design §6.1 is real in this project.
- **Cloud Resource Manager disabled:** on a project where it was disabled, `fugaro init --plan-only` asked to enable it, enabled it, and carried on once the enable propagated (how many retries it took).
- **The installation plan (adoption):** the imports, creates and in-place updates the plan showed, and that it had zero deletes; that `plan -detailed-exitcode` exits 2 for an import-only plan.
- **Log isolation:** a new Fugaro job line lands in the `fugaro` log bucket and not in `_Default`; the project exclusion applies to `_Default`; `fugaro logs` reads through the view; adopted M4 jobs already carry template labels, so the sink's filter matches them; and the check-job failure alert fires with log isolation on (check 16b).
- **Sandbox adoption:** the job, account and secrets imported; the live bucket condition still one binding, not two; the job's in-place diffs (container name, CPU form, annotations); the legacy image and display name kept until the first build.
- **The build (check 11, 11b):** `install -o 1000` on the credential volume works and the credential step can write there; `$BUILDER_OUTPUT` is writable by the docker and `cloud-sdk` builders; `docker run <image>@<digest>` uses the local image with no pull; `gcloud artifacts docker tags add` by digest works with `writer`; the untag is refused with a warning; the two `docker run` probes are `blocked`.
- **The GitHub mint (check 15):** the result, or "no GitHub sandbox".
- **A fresh repository's first `init --repo`:** the first `init --repo` on a fresh repo does not fail on the empty credential secret: it plans no check job, invoker grant or Scheduler job while the provider credential has no version, and the rerun after `fugaro secrets set` deploys them (paused until a build record exists).
- **The daily check (check 16):** the skip, the back-off, the forced rebuild, the metadata-server token accepted by the registry for the manifest `HEAD`, and blobless clones with both providers' credential helpers.
- **Cleanup (check 17):** what the dry run would delete.
- **The pinned commit (check 18):** whether a push to the base branch during an image build left the smoke passing, and whether the provider served the fetch of the pinned commit by SHA.
- **A real run:** its `ls --json` row's `image_age_s`, and that `--total-timeout 15m` gave the execution a 17-minute timeout.
- **The live suite on a migrated installation (not yet run live):** `TestLiveCloudBuildSecretAndDigest` is expected to build as the repository's build account, into its registry, and to run the two `docker run` probes of check 11b; `TestLiveSandboxRun` is expected to launch with `--total-timeout 15m` and see the 17-minute execution timeout. Record their `FACT`s from the first run.
- **Retirement:** `fugaro-build`'s bindings removed, disabled, deleted.
