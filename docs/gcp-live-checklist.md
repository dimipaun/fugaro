# GCP live checklist (M4, extended for M5, M6 and M9a)

These checks confirm the facts about Cloud Run, Cloud Logging, Cloud Storage
and Cloud Build that only a real project can show. The hermetic fakes
(`internal/gcpfake`) are written from the API documentation. These runs
confirm that the documentation was read correctly. The checks are the `live`
build-tag tests in `internal/backend/gcp/live_test.go` and
`internal/e2e/live_gcp_test.go`. Check 20 is the one exception that needs no GCP: `internal/e2e/live_gateway_test.go` (build tags `live` and `docker`) runs the gateway against the real Anthropic API from local Docker. None of them run in CI; CI only vets them (`go vet -tags live ./...` and `-tags 'live docker'`).

Each test logs its observations as `FACT:` lines. Paste those lines into the
PR description and into this checklist's results, at the end.

## Guardrails the tests enforce

- **Target.** `FUGARO_LIVE_GCP_PROJECT` names the live-test GCP project (its ID, not the Fugaro project's name) and
  `FUGARO_LIVE_REPO` the sandbox repository (`owner/name`), for example
  `my-fugaro-dev` and `acme/fugaro-sandbox`. Both are required. The local
  project config must then name all of the following, or every test calls
  `t.Fatal` before making any call. The tests pick the config the way the CLI
  does (design §5.4): `FUGARO_PROJECT=<name>`, else `FUGARO_CONFIG=<file>`,
  else the only file in `~/.config/fugaro/projects/`:
  - the same GCP project (`gcp_project`)
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
    build account's (which may push and remove tags, but not delete an
    image). That needs
    `artifactregistry.versions.delete` on the repository's registry: project
    owner or editor, or `roles/artifactregistry.repoAdmin` on that registry.
    Without it the cleanup fails the test and names the image to delete.
  - The Bitbucket token comes only from `FUGARO_BITBUCKET_TOKEN`. It is used
    to read the sandbox's `fugaro.yaml` for the spend check and to clean up,
    and in check 19 to read the sandbox repository, its PR and the PR's
    comments, and to decline that PR. Every request it makes goes to the
    sandbox repository (`FUGARO_LIVE_REPO`), and the GCP live tests
    (`internal/e2e`) refuse any other path. The CLI's environment never holds it, and it is scrubbed from
    anything the tests log.
  - **No second credential.** Check 19's review comments are posted by the
    person running it, by hand, in the Bitbucket UI; the tests never post a
    comment or push to the sandbox's base branch.
  - Nothing reads a credential from argv.
- **Cleanup comes first.** Each test registers its `t.Cleanup` before its
  first side effect. The cleanup does the following:
  - cancels an execution or build that is still going
  - declines the run's PR and deletes its `fugaro/<run-id>` branch (for a
    follow-up, the PR's branch, named after its first run, when that run is
    a live-test run too)
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
whole bring-up. If you have more than one project config, also export
`FUGARO_PROJECT=<name>` (the Fugaro project of the live-test installation), or
the tests refuse to choose:

```bash
export FUGARO_LIVE_GCP_PROJECT=<project> FUGARO_LIVE_REPO=<owner/name>
export FUGARO=<path to the fugaro binary built from the branch under test>   # jq is needed too
export SANDBOX=<a checkout of the sandbox repository>
```

Set the project up with `fugaro init` (`docs/gcp-setup.md`), using
`"$FUGARO"`:

1. The installation (steps 1 to 3 of the runbook, with `fugaro init --name
   <project> --gcp-project "$FUGARO_LIVE_GCP_PROJECT"`; its config is
   `~/.config/fugaro/projects/<project>.yaml`). The runs bucket must be
   named `fugaro-runs-*` (the tests refuse any other runs bucket, and a
   bucket can't be renamed later).
2. **⚠ CONFIRM** Commit the sandbox fixture, `deploy/sandbox/`, to the
   sandbox repository's base branch, `master`, as its `fugaro.yaml` says. Its
   `project:` must be the live-test project's name (the fixture says
   `sandbox`; change it to match before committing). This pushes to a real
   repository.
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
   gcloud services enable iamcredentials.googleapis.com --project "$FUGARO_LIVE_GCP_PROJECT"
   ```

2. **⚠ CONFIRM** Give yourself the Token Creator role on the sandbox job's
   service account. Its account ID comes from the Terraform variables
   `fugaro init --repo --print-vars` prints (no cloud calls; its warning goes
   to stderr):

   ```bash
   SA="$("$FUGARO" init --repo "$SANDBOX" --print-vars | jq -r '.repo.workflows.web.service_account.account_id')@$FUGARO_LIVE_GCP_PROJECT.iam.gserviceaccount.com"
   gcloud iam service-accounts add-iam-policy-binding "$SA" \
     --member "user:$(gcloud config get account)" --role roles/iam.serviceAccountTokenCreator --project "$FUGARO_LIVE_GCP_PROJECT"
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
project with `gcloud config get project` and `echo $FUGARO_LIVE_GCP_PROJECT`
before you start it.

```bash
FUGARO_BITBUCKET_TOKEN="$(cat <token file>)" FUGARO_LIVE_JOB_SA="$SA" \
  go test -tags live -p 1 -timeout 45m -run 'TestLive' -v ./internal/backend/gcp/ ./internal/e2e/ 2>&1 | tee live.log
grep 'FACT:' live.log
```

To run a single check, narrow `-run`. The commands are listed in the table
below. Check 19 (`TestLiveSandboxFollowUp`) is skipped by this run: it
pauses for you to post review comments, so it runs only on its own, with
`FUGARO_LIVE_FOLLOWUP=1` (see "Check 19").

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
`go test -tags live -p 1 -v -timeout 20m`. Set `FUGARO_LIVE_GCP_PROJECT` and
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
| 16 | The daily image check: a skipped check, a back-off after a forced failure, and one forced rebuild | With the sandbox's schedule unpaused (its `rebuild.check` is `daily` and its record exists): `fugaro image check --dry-run` from its checkout; then `gcloud scheduler jobs run <scheduler job> --location <scheduler region>` (from `fugaro init --repo --print-vars`, or `gcloud scheduler jobs list --location <scheduler region>`). To force a failure, temporarily disable the `bitbucket-token` secret's latest version and run the Scheduler job again, then re-enable the version. For the forced rebuild, run `fugaro image build`, which submits the request a fired trigger submits | (a) With nothing changed, the local check prints `skip`. The check job logs `decision: skip` for each workflow, `check.json` appears next to `image.json`, and no build starts. (b) The check job mounts the credential, so with its version disabled the execution fails at container start ("Failed to access secret ... Secret Version ... disabled"), before the check can log a decision or write `check.json`. Cloud Run records that only as an ERROR audit system event (`cloudaudit.googleapis.com%2Fsystem_event`, "Execution ... has failed to complete, 0/1 tasks were a success"), not in its system log, and the "Fugaro image check job failed" alert fires from that entry: the email arrives if one is configured, with log isolation on. A failure after start is covered by the check's own `decision: check-failed` line at ERROR (exit 2, the `fugaro ls` check warning and the "Fugaro image check failed" alert) and by the Cloud Run system log's ERROR lines. After a failed *rebuild* whose inputs haven't changed, the next check logs `rebuild-failed-last` and submits no build. (c) The forced rebuild runs candidate, smoke, gate, promote and record: `latest` moves to the record's `image_digest`, and `fugaro image status` shows the new build time. Moving the existing `latest` needs `artifactregistry.tags.delete`, which the build account holds through `fugaroTagMover` on its own registry; a `PERMISSION_DENIED` on `tags/latest` at `promote` means that grant is missing (gcp-setup.md, "Installations from before the tag mover role"). The untag then removes the `candidate-` tag: after the build only `latest` points at the new version, and the build log has no untag warning. Record each as a `FACT`, and re-enable the secret version. |
| 17 | The registry cleanup policy's dry run never selects a `latest` or `dev-` version | A day or more after the installation and the first builds exist, read the Artifact Registry audit log (Cloud Logging, `protoPayload.serviceName="artifactregistry.googleapis.com"`) for what the cleanup policies of `fugaro-base` and each repository's registry would delete in dry run, and list each registry's tags with `gcloud artifacts docker tags list` | No version the dry run would delete is tagged `latest` or `dev-`, and none of a registry's three newest versions is. If one is, stop: the keep rules are wrong. Otherwise turn deletion on (gcp-setup.md, "Turning registry cleanup on"). Record a `FACT` naming what the dry run would delete. |
| 18 | A push to the base branch during an image build does not fail the smoke (Bitbucket fetch-by-SHA of the pinned commit) | From the sandbox's checkout, start `fugaro image build`; while its render step has finished and the build step hasn't cloned yet, push a commit to the base branch (or force-push it back one commit, so the pinned commit is no longer the branch head) | The build clones, resets to the commit the render step read (fetching it by SHA when the clone lacks it), and the smoke, gate, promote and record succeed. The record's `source_commit`, `/etc/fugaro/image.json` and the image's revision label name that same commit, not the new head. If the provider refuses the fetch of an unadvertised commit, the build fails at the clone step: record that as a `FACT`. |
| 20 | The model gateway against the real Anthropic API, from local Docker: pins per role, cost, tokens, a halt (design §6.1, §4.5) | See "Check 20" below; **you** run it at your own terminal with your own API key: `FUGARO_LIVE_BASE_IMAGE=<tag> FUGARO_LIVE_ANTHROPIC_API_KEY=… FUGARO_LIVE_SPEND_OK=1 go test -tags 'live docker' -timeout 60m -run TestLiveGateway -v ./internal/e2e/` | See below. |
| 19 | A follow-up of the sandbox run's PR, acting on review comments you post by hand, and a follow-up refused because the PR was declined | See "Check 19" below: `FUGARO_LIVE_FOLLOWUP=1 FUGARO_BITBUCKET_TOKEN=… go test -tags live -p 1 -v -timeout 100m -run TestLiveSandboxFollowUp ./internal/e2e/` | See below. |
| 21 | The shared budget against the real Firebase project: tokens, rules, leases and releases, a repository daily cap, a kill, token expiry, the grace (design §5, §6.4) | See "Check 21" below; **you** run it at your own terminal with your own API key: `FUGARO_LIVE_BASE_IMAGE=<tag> FUGARO_LIVE_RTDB_URL=<url> FUGARO_LIVE_FIREBASE_API_KEY=<key> FUGARO_LIVE_TOKEN_SIGNER=<account> FUGARO_LIVE_ANTHROPIC_API_KEY=<key> FUGARO_LIVE_SPEND_OK=1 go test -tags 'live docker' -timeout 90m -run TestLiveBudget -v ./internal/e2e/` | About $0.50. Every answer is a `FACT:` line; the run, cap and kill halts are the assertions. |

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
- `result.json` has `pushed_head`, and the run saved its Claude Code
  session: `session/session.json` is readable, says `pushed`, names
  `pushed_head` as its `head_sha` and the file's size, and its
  `session/<id>.jsonl` is there (`FACT` lines give the session ID, size and
  workdir). Every run saves one, so that its PR's first follow-up can
  resume it.
- Cleanup declines the PR, deletes the branch and deletes the run's objects.

## Check 19: a follow-up run

`TestLiveSandboxFollowUp` runs three sandbox executions: a first run, a
follow-up of its PR, and a follow-up of the same PR after it was declined,
which ends at bootstrap. That is about twice the sandbox's cap plus a
minute of compute, and two runs' model usage. It needs things only a person
can do, so it is run one step at a time, and **you**, not the test or an
agent running it, post the review comments and edit the sandbox's trust
list, each with your own Bitbucket credential. The test holds only the
sandbox's repository access token.

**Before it (free: nothing the tests do writes; steps 3 and 4 are your own
writes, with your own credential):**

1. The sandbox repository is private. A follow-up refuses a public one, and
   the sandbox's `fugaro.yaml` doesn't set `followup.allow_public`. The test
   checks `is_private` before launching anything.
2. The sandbox's image has the follow-up runner: its `fugaro` is built from
   the branch under test (rebuild the base and the image as for any runner
   change; an older image fails a follow-up at bootstrap with "follow-up
   runs are not supported by this version of fugaro", touching nothing).
3. Find your Bitbucket `account_id`: post any comment on a sandbox PR (any
   PR in the sandbox will do, including one an earlier check 13 declined:
   Bitbucket allows comments on declined PRs), then
   run `TestLiveInspect` with that PR in `FUGARO_LIVE_INSPECT_PRS`
   ([git-providers.md](git-providers.md#finding-an-account-id-for-followuptrusted)).
   It prints each comment's `user.account_id`.
4. **You** add it to the sandbox's `fugaro.yaml` on `master`, in the
   sandbox repository only, with your own credential: an edit in the
   Bitbucket UI, or a PR you merge. `deploy/sandbox/fugaro.yaml` shows where
   it goes, commented out; the real ID is never committed to Fugaro.

   ```yaml
   followup:
     trusted: ["<your account_id>"]
   ```

   The test reads the file back before launching, and fails at once, at no
   cost, if `followup.trusted` is empty.

**⚠ CONFIRM** Then run it (it spends money, and opens and declines
a PR in the sandbox and deletes its branch):

```bash
FUGARO_LIVE_FOLLOWUP=1 FUGARO_BITBUCKET_TOKEN="$(cat <token file>)" \
  go test -tags live -p 1 -v -timeout 100m -run TestLiveSandboxFollowUp ./internal/e2e/ 2>&1 | tee -a live.log
```

What it does and checks:

- Before any launch: the spend caps of check 13, a non-empty
  `followup.trusted`, and a private sandbox. Every cleanup is registered
  before its launch.
- **The first run**, as check 13 (`--total-timeout 15m`, batch
  `live-<stamp>`), until its PR exists.
- **The pause.** It logs `ACTION: post one inline and one general review
  comment on <PR URL> in the Bitbucket UI now` and then only reads: every
  20 seconds, for up to `FUGARO_LIVE_COMMENT_WAIT` (default `20m`), it lists
  the PR's comments with the repository token until there is both an inline
  comment and a general one (not a reply) by someone other than the PR's
  author, who is the token's own identity. Post one comment on a line of the
  diff and one on the PR itself, from your own account. Each author's
  `account_id` is a `FACT`, and must be in `followup.trusted` (else: "add
  <id> to followup.trusted in the sandbox's fugaro.yaml on master, then
  rerun"). Past the wait it fails; the cleanup still runs.
- **The follow-up:** `fugaro run --pr <n> --run-id <id> --batch <same>
  --total-timeout 15m --json "Also add a line to the README saying the
  follow-up ran."` reports `launched` on the same branch, with `pr` and
  `previous_run` (the first run) set. Once it ends:
  - exactly one open PR comes from the branch, the same one;
  - the PR has one report per run, each carrying its run's marker, and the
    follow-up's report names your display name under "Comments used"; a
    `FACT` for each says whether Bitbucket shows the marker as text and
    gives the report's cost line (the follow-up's own cost);
  - the follow-up's `comments.json` holds your inline and general comments
    and neither report;
  - `result.json`'s `follow_up` names the PR and the first run, at least two
    comments, and `session: resumed` (a `FACT`; `fresh` fails the check and
    its `session_note` says why; "the saved session could not be resumed"
    means Claude Code's session directory naming or its `--resume`
    behaviour isn't what the runner assumes);
  - the follow-up saved its own session and has `pushed_head`;
  - `ls --pr <n> --json` lists exactly the two runs, with `totals.runs` 2;
  - `diagnose --json` of the follow-up has a `follow_up` block (a `FACT`);
  - the branch lock is gone.
- **The closed-PR refusal.** It declines the PR through the API, keeping its
  branch and both runs' objects, lists the PR's comments, and launches
  `fugaro run --pr <n> --batch <same> --json` with no instructions. The CLI
  launches (it can't see the PR's state); the run ends `infra_error` with
  "PR #<n> is closed" (a `FACT`), and the PR's comments are exactly those
  from before.
- **Timing.** A realistic run takes about 65 minutes: two runs of about 17
  minutes each, your comments, the refused run's minute, and the build. The
  waits are at most 30 minutes per run, 20 for the comments and 10 for the
  refused run, 90 in all, under the `-timeout 100m` (an abort skips the
  cleanup; run the sweep then).
- **Cleanup**, as for check 13: every open PR from the branch declined, the
  branch deleted, the three runs' objects deleted. The sweep covers the
  batch, follow-ups included: it deletes a follow-up's PR branch when that
  branch's first run is a live-test run too.

Afterwards, if you prefer the sandbox not to trust anyone between live
runs, remove the `followup:` block from its `fugaro.yaml` on `master`
again, the same way you added it. An older `fugaro validate` refuses the
block (strict decoding), so remove it before rolling a checkout's tooling
back.

## Check 20: the model gateway

`TestLiveGateway` runs `fugaro exec` **locally in a base image** you built from the branch (`docker run`, no GCP), against a `file://` bucket, the fake git provider and a small fixture repository with `auth: api-key`, the budget in `enforce` with a **$2.00 per-run cap**, and a different pinned model per role (coder `claude-sonnet-5`, reviewer `claude-sonnet-5-5`, background `claude-haiku-4-5`, `max_output_tokens` 4,000 each, one review round). The task tells the agent to write `greet.sh` with no shebang and no `chmod`, and the review rule calls both a finding, so the review asks for one fix and the fix stage resumes implement's session, invites a subagent, and gives the agent one small PNG to read. The fixture's committed `.claude/settings.json` sets `ANTHROPIC_BASE_URL` to `http://127.0.0.1:9`, with `FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1` set in the container so the bootstrap refusal steps aside and the managed settings' precedence is what is measured. A second run with a $0.002 cap follows.

**Cost:** about $0.30 for the first run (the cap is $2.00; one call's worst case is far below it), and the second run halts at its first call, for nothing. It spends **your** money on **your** key.

**Who runs it, and the key.** You run it, at your own terminal. The key is read from `FUGARO_LIVE_ANTHROPIC_API_KEY` in your shell, reaches the container through the `docker` process's environment (never an argument), is never logged, and the test fails if it finds it in the run's output, the bucket or the provider state. The test skips (it does not fail) unless all three variables are set, `FUGARO_LIVE_SPEND_OK=1` included. An agent or a controller must not hold the key or run this test; if you have no API key, the check stays open (see "If it can't be run").

**Before it (free):**

1. Docker is running, and you have built the base image from the branch under test: `sh images/build-base.sh web-node fugaro-web-node:m9a-live`. Set `FUGARO_LIVE_BASE_IMAGE` to that tag. (The test uses the image's own `fugaro` and Claude Code, so the base must be from this branch.)
2. Decide the key's spending limit on the Anthropic console, if you want a second brake.

**It asserts, and logs each answer as a `FACT:` line to paste into the results below:**

1. **A6, A-N5, A-N7.** The run reached the model through the gateway: the gateway logged calls, the run didn't fail on a refused connection, although the repository's own settings pointed Claude Code at a dead port. That shows Claude Code read the managed settings file, that its `env` outranks the repository's, and that a plain `http://127.0.0.1` base URL works.
2. **A-N6.** Every call the gateway saw used the model pinned for its role in its stage (implement and fix: `claude-sonnet-5`; review: `claude-sonnet-5-5`; Claude Code's background requests: `claude-haiku-4-5`), no call was refused, and a stage never failed on a pin. The distinct `x-claude-code-agent-id` values show whether the subagent made calls.
3. **A10, A11.** The record's `model_source` is `gateway`, `unreconciled` and `usage_unparsed` are zero, and the run's real spend is logged against the cap. The gateway's settled cost and Claude Code's `total_cost_usd` are logged side by side, per model, with each side's token components (input, output, cache read, cache write) and the gateway's tokens recomputed at Claude Code's own prices. A gap between them is a `FACT`, not a failure: Claude Code's figure is an estimate from its baked-in price table, and the arbiter is the Anthropic Console, so compare the gateway's cost with the Console's charge for the key.
4. **A9 (Anthropic side).** No call's output exceeded its role's `max_output_tokens`, and the largest output per model is recorded. The gateway's own refusal of a larger `max_tokens` is what bounds the request (the call log doesn't carry `max_tokens`), so a refused call would show as a violation in item 2.
5. **A-N2.** Per stage (implement, review, fix, the resumed one included), the result event's summed `usage`, its summed `modelUsage` and the gateway's own token count are logged side by side. The test fails if the result event's count is more than 5% below the gateway's for any stage: the `oauth` token cap, which rests on the result event, would under-count.
6. **A-N1, R8.** The second run, with a $0.002 cap, ends `halted` with reason `run_cap` (not `failed`, exit 0), and `claude` ended the stage on its own, within the 60-second grace, after the gateway's 403.
7. **The planted key.** The real key is in no log, transcript, bucket object or provider-state file of either run.
8. **A-N8, A-N9.** No call's charge exceeds its reservation (the call that read the PNG included), and the `usage.service_tier` and `usage.inference_geo` Anthropic returned are logged. The call log carries no request tools: the gateway refuses every server or typed tool as a violation, so a run that ended without one proves Claude Code sent only client tools.

**If it fails**, read the failing line first. Known ways it can, and what they mean:

- *No call logged, or a refused connection:* the managed settings did not outrank the repository's (A6 false). The settings refusal is then the only guard for committed settings: keep the budget off for `api-key` and say so in design §6.1.
- *A stage fails with `context_management is not allowed` or a similar refusal:* Claude Code sent a shape the gateway refuses. The refusal names it. Narrow the refusal to the shapes that are really unbounded (an allow-list with its own test), never remove it. `context_management` is already narrowed: only `clear_thinking_*` and `clear_tool_uses_*` edits pass. The gateway's call log carries `max_tokens` and `tool_types`, so the live run shows what Claude Code sent.
- *A content block type is refused:* the allow-list lacks a block Claude Code sends; add it with a test.
- *`usage_unparsed` or `unreconciled` above 0:* read the `model call` lines' `settled` and `priced_as` fields. *A gateway cost that differs from Claude Code's:* not a failure; read the per-model `FACT`s, then check the Console. If the Console disagrees with the gateway, the price is wrong in the table or needs `model_prices`.
- *The review asked for no fix:* the task didn't provoke one (`greet.sh` should lack a shebang and the executable bit, and the review rule flags both); the test fails on purpose, since the resumed stage was not exercised. Adjust `lgwTask` or `lgwReview` and run again.

**If it can't be run** (no API key, or you don't want to spend): check 20 stays open, and the budget must stay off for `api-key` repositories until someone runs it. Nothing in an `oauth` repository depends on it, since `oauth` never uses the gateway.

**The Vertex facts** are separate: `enforce` with `auth: vertex` stays refused until they are recorded. They need a Vertex-auth workflow in `observe` mode on a project with Vertex AI enabled, and a real run, and answer: that Claude Code with `CLAUDE_CODE_SKIP_VERTEX_AUTH=1` sends Vertex-shaped paths to `ANTHROPIC_VERTEX_BASE_URL` with the pinned model and an allowed location in the path (A-N3); that the usage fields come back as documented through `streamRawPredict` (A9, Vertex side); and what Vertex bills compared with the gateway's figure on the regional and the global endpoints, since the embedded prices are the global ones (A11). Record each as a `FACT`; the refusal in `init --repo`, `validate` and the runner is lifted only by a code change that cites them.

**Afterwards:** nothing to clean up in the cloud. `docker image rm` the base tag if you don't need it.

## Check 21: the shared budget

`TestLiveBudget` runs `fugaro exec` **locally in a base image** you built from the branch (`docker run`), against the project's **real** Realtime Database, Identity Toolkit and Secure Token and the real Anthropic API (`auth: api-key`, a different pinned model per role, as in check 20). The harness plays the launcher: it mints each run's custom token with **your** Application Default Credentials (`signJwt` as the token-signer account) and leaves it in a `file://` bucket. Every run uses a scratch repository name (`live21-<hex>/...`), so no real repository's counters or caps move. The test does change two project-wide nodes for its duration, `config/mode` (set to `enforce`) and `config/caps/global` (daily $5.00, per run $2.00), **and restores both**; it also removes its caps, kill switches and registry entries. Use a project nobody else is launching into (the scratch Firebase project of the bring-up is ideal).

**Cost:** about $0.50 on your key: a full run at about $0.30 (the lease and release run), a run that a **$0.25 repository daily cap** halts part-way, a run a **$0.002 per-run cap** halts at its first call (free), and a run killed a few seconds after it registers. The token-expiry and grace checks cost nothing.

**Who runs it, and the key.** As for check 20: you, at your own terminal; the Anthropic key is read from `FUGARO_LIVE_ANTHROPIC_API_KEY`, reaches the container through the `docker` process's environment (never an argument), is never logged, and the test fails if it, or a minted custom token, appears in a run's output, bucket or pushed files. The test skips (it does not fail) unless all six variables are set, `FUGARO_LIVE_SPEND_OK=1` included. `FUGARO_LIVE_REQUESTED_BY` is optional (default `live-budget-test`): it is the `rb` claim of the tokens the test mints. An agent or a controller must not hold the key, the signer rights or run this test.

**Before it (free):**

1. The Firebase project exists, billing is linked and `fugaro init --firebase <id> --budget-mode observe` has run (docs/gcp-setup.md, "Turning the shared budget on"). The rules (about 37 KB) were deployed by it: **if that deploy failed on size or syntax, record it** (A13).
2. `gcloud auth application-default login --scopes=https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/firebase.database,https://www.googleapis.com/auth/userinfo.email`, and your user holds `fugaroTokenMinter` on the signer (the Firebase root grants it to launchers).
3. Build the base image from this branch (`FUGARO_LIVE_BASE_IMAGE`); the web API key is the Firebase root's `firebase_api_key` output (also the jobs' `FUGARO_FIREBASE_API_KEY`); the database URL is `budget.rtdb_url` in the project config; the signer is `budget.token_signer`.

**What it asserts and records** (each a `FACT:` line to paste below):

- *Free identity probe* (A2, A4, A12, A13, A15, A-F4): the person's ADC is accepted by the REST API; `signJwt` as the signer with the `cloud-platform` scope mints a token; the exchange works with **no sign-in provider enabled** and the restricted web key; the ID token carries `fs`, `fr`, `fx`, `fp`, `rb` at the top level; the refresh token produces a new ID token; the deployed rules deny a write into another repository's run ledger, deny a run identity writing `config/mode`, and allow it to read `config/mode`.
- *Run 1, lease and release* (R3, A4, A14, A-F1): the run ends other than `halted`/`infra_error`; its lifetime ledger has nothing unsettled and its `spent` matches the gateway's cost to $0.001; the repository's day counter under `spend/<epoch day>/` has `counted == spent` (the plain decimal day, A14) and shows the models; the registry entry is gone at the end; the log's lease grants and **lease give-ups** (8 stale denials in a row) are counted (the plan's risk: contention).
- *Run 2*: the repository's $0.25 daily cap halts a run with `repo_daily_cap`, exit 0.
- *Run 3*: a $0.002 per-run cap halts it with `run_cap`.
- *Run 4, a kill mid-run* (R6, A-F2): after the registry entry appears, the repository's kill switch is written; the run ends `halted` with `kill_switch` within 60 seconds, and the time is recorded.
- *Run 5, token expiry* (R5): a token minted two hours earlier halts the run `budget_token_expired`.
- *Run 6, the grace* (D14): `FUGARO_RTDB_URL` points at a dead local port with `FUGARO_BUDGET_GRACE=5s`; the run halts `budget_unavailable`, outcome `none`, exit 0.
- No secret (the Anthropic key, a minted token) in any output, bucket object or pushed file.

**Only a live run on Cloud Run can confirm** (the local test cannot; record each as a `FACT`, from step 7 of the migration runbook or a sandbox run):

- **SSE over Cloud Run egress for an hour** (A-F2): the kill stream survives a long run on default egress and `auth_revoked` arrives on token refresh; otherwise the 15-second poll is the only kill path.
- **The history job's sweep actually runs:** a Scheduler execution of `fugarohist` ends `succeeded`, and **Cloud Run reports the overridden env in `execution.template.containers[0].env`** (the sweeper's execution lookup relies on it); the sweep finds a leaked entry and a deleted run's execution as missing.
- **The first CI run of the history image job** (CI builds, smoke-tests and scans the image but does not push it: the push is manual, `sh images/build-base.sh history <registry>/fugaro-base/history:latest` then `docker push`, as `init --firebase` prints, gcp-setup.md "The history image").
- **The third apply as a plain operator:** nobody holds `actAs` on the history account, so record whether creating the history job succeeds for an operator who is neither owner nor editor (it needs them to be able to act as the account by other means).
- **`createdAt` of a custom-token user** (the sweeper deletes users older than 2 days by it) and **Secure Token's error codes** on a revoked or expired refresh (`TOKEN_EXPIRED`, `USER_NOT_FOUND`, `INVALID_REFRESH_TOKEN`): the runner classifies them as expired, revoked or invalid.
- **`ETag` of a never-written node:** `null_etag`, as the cap writes assume for an unset cap.
- **The real `batchGet`/`batchDelete` shapes** of Identity Toolkit used by the sweeper.
- **The rules at about 37 KB deploy to the real project** and, with a minted real token, a legitimate lease is allowed and released (the emulator proves it, production must agree: A4, A13, A14).
- **Contention:** the lease give-up rate with several runs at once.

**If it can't be run** (no API key): the leases are verified for `oauth` (notional) only, and the `api-key` budget behaviour stays documented as unverified.

**Afterwards:** the test removes what it made. `fugaro budget show --all` should show no `live21-*` repository; `docker image rm` the base tag if you don't need it.

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
     --member "user:$(gcloud config get account)" --role roles/iam.serviceAccountTokenCreator --project "$FUGARO_LIVE_GCP_PROJECT"
   ```

2. **⚠ CONFIRM**, optional: disable the IAM Credentials API if nothing
   else uses it: `gcloud services disable iamcredentials.googleapis.com
   --project "$FUGARO_LIVE_GCP_PROJECT"`.

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
- **The forced failure alert (check 16b):** verified. Disabling the latest version of a repository's Git token made the check job fail at container start (Cloud Run couldn't read the secret). Cloud Run logged that only as an audit system event, `Execution ... has failed to complete, 0/1 tasks were a success`, not in its `varlog/system` log, so the alert's second condition first missed it; it now matches ERROR entries in either log. The first forced failure, minutes after the filter change, sent nothing: a policy edit takes some minutes to reach log matching, so wait half an hour before forcing a failure after changing an alert. The second one produced the "Fugaro image check job failed" email (a `Log alert fired` from Google Cloud Alerting, naming the job) within a minute. Email channels have no verification step (`sendVerificationCode` is for SMS).
- **The web repo's first build and switch:** its first build ran as its own build account in about 8.5 minutes (`skip_build_scripts` in place); the second apply changed exactly two things, the job's image and the Scheduler job's `paused`; `fugaro image check --dry-run` then printed `skip`, and a manual Scheduler run logged `decision: skip`, wrote `check.json` and started no build.
- **A real run on the migrated web repo** (a unit-test task) succeeded in 25 minutes: compute $0.12, $0.63 notional model usage, `image_age_s` about 54 minutes in its `ls --json` row, its logs readable through the view by `logs` and `diagnose`.
- **Live tests on the migrated project:** `TestLiveSandboxRun` passes (with `--total-timeout 15m` the execution's task timeout is 1020s, the 17 minutes expected), and `TestLiveCloudBuildSecretAndDigest` passes as the repository's build account: the metadata server is reachable from a Cloud Build step (which is why the smoke isn't a step), blocked from a `docker run --network none` and from a default-network `docker run` of the candidate, and `--network=host` is refused.
- **Cleanup (check 17):** all registries held one version each, younger than any delete rule, so nothing could match; the policies as applied were checked against the design and switched from dry run to on.
- **Retiring the old build account:** its five bindings (project log writer, the legacy registry writer, three secret accessors) were removed and the account disabled after both repositories built as their own accounts; deleting it a week later is a separate step.
- **The installation plan:** 2 imports (the runs bucket and the legacy registry, labels only), 28 creates, 2 in-place updates, 0 deletes. Removing project Viewers' read access to the runs bucket was confirmed and done.
- **The sandbox plan:** 5 imports (the job, its account, three secrets), 24 creates, 4 label-only updates, 0 deletes; 4 live IAM bindings adopted, none duplicated; the runs bucket still has one conditional binding for the sandbox account; the job kept `maxRetries: 0`, its account and its legacy image until the second apply, which changed only the image.
- **The sandbox's first build** ran as the sandbox's own build account into its own registry and succeeded: `latest` points at the built digest, the record exists (`fugaro image status` shows it with the base digest), and a `candidate-` tag was left behind. That was taken as expected then, but it was the first sign of a missing permission: the build account held only `writer`, which lacks `artifactregistry.tags.delete`, so the untag was refused, and every later build of the repository, the nightly check's included, failed at `promote` with `PERMISSION_DENIED` on `tags/latest` (safely: `latest` stayed where it was). The fix is the `fugaroTagMover` role (`tags.delete` only) on each repository's own registry; after it, a build removes its `candidate-` tag once promoted, and a leftover one means the untag failed.
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
- **The build (check 11, 11b):** `install -o 1000` on the credential volume works and the credential step can write there; `$BUILDER_OUTPUT` is writable by the docker and `cloud-sdk` builders; `docker run <image>@<digest>` uses the local image with no pull; `gcloud artifacts docker tags add` by digest works with `writer` and `fugaroTagMover`, including when `latest` already exists on another version (a rebuild); the untag removes the `candidate-` tag with no warning; the two `docker run` probes are `blocked`.
- **The GitHub mint (check 15):** the result, or "no GitHub sandbox".
- **A fresh repository's first `init --repo`:** the first `init --repo` on a fresh repo does not fail on the empty credential secret: it plans no check job, invoker grant or Scheduler job while the provider credential has no version, and the rerun after `fugaro secrets set` deploys them (paused until a build record exists).
- **The daily check (check 16):** the skip, the back-off, the forced rebuild, the metadata-server token accepted by the registry for the manifest `HEAD`, and blobless clones with both providers' credential helpers.
- **Cleanup (check 17):** what the dry run would delete.
- **The pinned commit (check 18):** whether a push to the base branch during an image build left the smoke passing, and whether the provider served the fetch of the pinned commit by SHA.
- **A real run:** its `ls --json` row's `image_age_s`, and that `--total-timeout 15m` gave the execution a 17-minute timeout.
- **The live suite on a migrated installation (not yet run live):** `TestLiveCloudBuildSecretAndDigest` is expected to build as the repository's build account, into its registry, and to run the two `docker run` probes of check 11b; `TestLiveSandboxRun` is expected to launch with `--total-timeout 15m` and see the 17-minute execution timeout. Record their `FACT`s from the first run.
- **Retirement:** `fugaro-build`'s bindings removed, disabled, deleted.

## Results of the fourth live run (M6)

Run on 2026-09-30 in the dev project, against the Bitbucket sandbox, with
the base image and the sandbox's image rebuilt from the M6 merge. The
Bitbucket adapter's live test with recording, check 13 and check 19 all
passed. What the run showed:

- **One finding that led to a fix, before any check ran.** Rebuilding the sandbox's image for the M6 runner failed at `promote` with `PERMISSION_DENIED` on `tags/latest`: `gcloud artifacts docker tags add` on an existing tag deletes it and creates it again, and the build account held only `writer`, which lacks `artifactregistry.tags.delete`. `latest` stayed where it was. Every rebuild, the nightly check's included, would have failed the same way; the first builds had passed only because `latest` didn't exist yet. The fix is the `fugaroTagMover` role (`tags.delete` only) on each repository's own registry (merged; see the third run's sandbox build item and gcp-setup.md, "Installations from before the tag mover role"). **Verified live:** once `fugaro init` and `fugaro init --repo` had applied it (one create each), the sandbox's rebuild promoted, moved `latest`, updated its record and removed its `candidate-` tag.
- **The sandbox's trust list.** For check 19, the sandbox's `fugaro.yaml` on `master` carries `followup: {trusted: ["<owner's account_id>"]}`. It can stay for the next live run; removing it is optional (check 19, "Afterwards").
- **The Bitbucket adapter** (`TestLiveBitbucket` with `FUGARO_LIVE_RECORD_DIR`). The recordings of the follow-up reads and of both updates by number are committed, pseudonymized, in `internal/gitprov/bitbucket/testdata/recorded` and replayed by `TestRecordedFollowUpReads`, `TestRecordedEnsureByNumber` and `TestRecordedEnsureByNumberDeclined`; the hand-written fixtures now follow the real shapes where they differed. No adapter change was needed.
  - git over HTTPS with the env-only credential helper works, and `.git/config` holds no token.
  - As in the 2026-09-27 run: `draft` honoured on create and on update in both directions, a `PUT {title}` keeping `draft` and the description, the mixed-case lookup finding the existing PR, the comment, the labels warning once across two PRs, and an unknown reviewer answered with HTTP 400 "Malformed reviewers list", the PR opened without reviewers.
  - `Repository` reads `is_private: true`. `PullRequest` reads `state` `OPEN`, `draft`, `author.account_id` (the token's app user; Bitbucket account IDs have the form `<digits>:<uuid>`), the source branch, `source.repository.full_name`, and a 12-hex `source.commit.hash` that `SameCommit` matches with the pushed head.
  - **`GET /user` with the repository access token returns HTTP 403** ("This API is not accessible by this authentication mechanism"). The identity is unknown: one warning, every comment `self_known=false`, the designed fallback. The PR's `author.account_id` equals the `author_id` of every comment the token posted, so the rule that always drops the PR author's comments is what keeps Fugaro's own out.
  - Comments: `POST …/comments/{id}/resolve` works. The resolved thread's first comment carries `"resolution": {}` (an empty object) and the others none, and the reply reads resolved through its thread. A reply carries `parent` (`id` and `links`) and an `inline` of its own. `inline` has `from`, `to`, `path`, `start_from` and `start_to`, and no `outdated` field while the comment is current. `deleted` and `pending` are always present. `user` has `uuid`, `account_id` and `display_name` (`type: app_user` and `kind: repository_access_token` for the token). A single page has no `next`.
  - The raw content keeps `<!-- … -->`; `content.html` escapes it as text (the known issue below).
  - The update by number changes only `draft`, keeping the title and description. On the declined PR (`DECLINED`, `closed_by` set) `PullRequest` reads `closed`, and `EnsurePR` by number returns `ErrPRNotOpen`, with no open PR for the branch.
  - A bad token gets HTTP 401, and the token never appears in the error.
- **Check 13, the first-run regression on the M6 runner:** succeeded and ended ready in about 44 seconds, with a 1020s task timeout under `--total-timeout 15m`. Compute $0.0008, model $0.30 notional on the subscription, `total_usd` 0. `logs --json` has a `stage started` line and an `"event":"tool"` entry; `result.json`'s execution matches `launch.json`'s; `pushed_head` equals `head_sha`; the run saved its session (about 200 KB, workdir `/work/repo`) and its cache archives.
- **Check 19, the follow-up.** The first attempt ran out of the default 20-minute comment wait (nobody was at the Bitbucket UI in time; its cleanup declined the PR). The rerun, with `FUGARO_LIVE_COMMENT_WAIT=45m`, passed:
  - Before launching: the sandbox is private, `followup.trusted` lists one account ID, `allow_public` is off.
  - The first run ended ready in about 44 seconds; its PR's `author.account_id` is the token's identity.
  - The trusted person posted one inline and one general comment by hand.
  - The follow-up launched on the same branch with `pr` and `previous_run` set, ran implement, review and finalize, and ended `succeeded`, outcome `ready`, on the same PR, in about 1.5 minutes.
  - Its `comments.json` holds 2 comments, both by the trusted person, with Fugaro's own report omitted (`omitted: {fugaro: 1}`) and no untrusted author; the report names the person under "Comments used".
  - `follow_up.session` is `resumed`: Claude Code's session directory naming and `--resume` work as the runner assumes. The follow-up saved its own session (about 250 KB, workdir `/work/repo`), and its `pushed_head` equals `head_sha`.
  - The PR has two reports, one per run, each with its marker in the raw text and its own cost line ($0.14 and $0.25 notional model, compute ≈ $0.00).
  - `ls --pr` lists exactly the two runs (`totals.runs` 2, $0.39 notional model in all), and `diagnose --json` of the follow-up has its `follow_up` block (the PR, the previous run, `session: resumed`, `start_sha`, 2 comments, the authors and `omitted`).
  - After the PR was declined, the third run ended `infra_error` at bootstrap after about 23 seconds, "bootstrap: PR #N is closed", touching nothing.
  - The cleanup deleted the branch and the three runs' objects.
- **Known issue (cosmetic): the report marker shows in Bitbucket.** Bitbucket's Markdown escapes the HTML comment (check 19's report `FACT`s: "the rendered HTML shows fugaro:report as text true"), so every comment Fugaro posts on Bitbucket ends with a visible `<!-- fugaro:report run=… -->` line. Nothing reads the rendered HTML, and the raw text keeps the marker, so recognizing Fugaro's own comments works. The fix is in the design's backlog (§14).

### The assumptions this run answered

These were written from documentation or observation elsewhere before the
run. What it showed for each:

- **Claude Code's session directory: verified.** Naming a project directory
  `~/.claude/projects/<working directory, symbolic links resolved, with
  every byte outside [A-Za-z0-9] replaced by ->`, so `/work/repo` is
  `-work-repo`, is what Claude Code expects: check 19's follow-up resumed
  the restored session (`session: resumed`).
- **`claude -p --resume <id>`: verified that it resumes.** Whether it keeps
  the session ID or forks a new one isn't recorded by the `FACT`s; the
  runner follows the ID the result reports either way, and the follow-up
  saved its own session. The "No conversation found with session ID" path
  was not exercised, and stays covered by the fake `claude` only.
- **Bitbucket's field shapes: answered** (the adapter item above).
  `is_private`, `author.account_id` and `source.commit.hash` are as
  documented. `resolution` is an empty object on the resolved thread's first
  comment and absent elsewhere; whether reopening clears it wasn't
  exercised. `inline.outdated` is absent on a current comment; an outdated
  comment wasn't exercised. `deleted` and `pending` are always present,
  `user.uuid` and `user.account_id` are set, and a reply carries its own
  `inline`. Whether `GET /user`'s `uuid` is spelled as the comments'
  `user.uuid` can't be seen with a repository access token, which may not
  read `GET /user`.
- **`GET /user` with a repository access token: answered, HTTP 403** ("This
  API is not accessible by this authentication mechanism"). The identity is
  unknown, as designed, and trust doesn't depend on it: the PR author's
  comments, Fugaro's own, are always dropped.
- **How Bitbucket renders `<!-- fugaro:report run=… -->`: answered, as a
  visible last line** (the known issue above).
- **Whether users with read access can edit a Bitbucket PR's reviewers:**
  not shown by this run; not load-bearing under the allowlist.
- **GitHub: still open, with no live check in M6.** That GraphQL's
  `Bot.databaseId` equals the REST `user.id` of `<slug>[bot]`, and that a
  bot's GraphQL login has no `[bot]` suffix; the GraphQL shapes
  (`reviewThreads`, `isResolved`, `isOutdated`, `path`, `line`,
  `authorAssociation`, `comments.pageInfo`); that organization members
  whose membership is private show as `CONTRIBUTOR` to an App without
  `members: read`; and that converting a PR to a draft keeps its requested
  reviewers. These stay open until a GitHub sandbox and App exist.

## Results of the fifth live run (M9a)

Migration and sandbox run on 2026-10-01 in the dev project. The installation is named `belong` (both repositories, EdgeWeb and the sandbox, are members of it). Check 20 is deferred to a later session; the other items are recorded below.

- **Migrated.** Base `dev-419456d` (built from main at 419456d), then:
  - `fugaro init --name belong`: plan 1 create, 2 updates, 0 destroys (the `fugaro_project=belong` bucket label, the marker object, and the base registry's cleanup policy set to dry run).
  - EdgeWeb `init --repo`: 4 updates, 0 destroys (job env and check job env gain `FUGARO_PROJECT=belong` and `FUGARO_GCP_PROJECT`; the daily check unpaused).
  - Sandbox `init --repo`: 2 updates, 0 destroys.
  - Before the migration the EdgeWeb image check was paused; the sandbox has no check job (`rebuild: check: off`).
- **Verified.** `fugaro/project.json` reads `{"gcp_project":"edge-devel-dimi","name":"belong","version":1}`; the runs bucket carries `fugaro_project=belong`; all three jobs (EdgeWeb, sandbox, EdgeWeb's check) carry both variables; the check job is `ENABLED`; `fugaro ls` prints `project: belong (GCP edge-devel-dimi)`; `fugaro validate` passes in both checkouts.
- **Project selection.** With two project configs (belong and a dummy) and no selector, outside a checkout, `fugaro ls` refuses and lists both; with exactly one it works.
- **Sandbox run (check 13) on the M9a runner:** `20261001-042234-4b0e` succeeded in about a minute, PR #23 (the sandbox). Model usage $0.33 notional on the subscription (`model_basis: subscription`), compute $0.001. No halt.
- **EdgeWeb:** its `project: belong` line went in as PR #1774 (merged on the user's instruction). No EdgeWeb run was started.

Check 20 ran on 2026-10-01 and passed (third attempt; the first two stopped on test bugs, fixed in #46 and #49, with no model spend beyond the figures below). Still to record: the Vertex facts. Record every `FACT` and each plan summary in the same way as the runs above.

- **The migration:** the plan summaries of `fugaro init --name` and of each `fugaro init --repo`; that `fugaro/project.json` shows the name and the GCP project; that the label `fugaro_project` is on the runs bucket; that each job carries `FUGARO_PROJECT` and `FUGARO_GCP_PROJECT`; that each repository's Scheduler job is `ENABLED` again.
- **Project selection:** outside a checkout with two project configs and no selector, a command refuses and lists them; with exactly one, it works.
- **A sandbox run (check 13) on the M9a runner:** it ends ready; its record has `model_source: claude-code` and no `halt`.
- **Check 20 (`TestLiveGateway`), passed 2026-10-01** (Claude Code 2.1.283 in the image, API key, local Docker; the last run took 69 s and cost $0.48 by the gateway's count, the earlier runs $0.42 and $0.41, the first confirmed against the Console invoice at $0.42):
  - **A6, A-N5, A-N7: true.** Claude Code read `/etc/claude-code/managed-settings.json`; its `env` beat the repository's `ANTHROPIC_BASE_URL`; it accepted a plain `http` loopback base URL (20 calls logged). No default request shape was refused (no `context_management` or block-type refusals).
  - **A-N6: true.** implement and fix calls all `claude-sonnet-5`, review calls all `claude-sonnet-5-5`, no violations; one `x-claude-code-agent-id` (no subagent calls).
  - **A-N2: true.** The gateway's tokens equal the result events' per stage (implement 523,632; review 163,684; fix 138,627). A resumed fix stage's `modelUsage` carries the earlier stages' totals (662,259 = 523,632 + 138,627); `usage` and the gateway agree at 138,627.
  - **A10, A11: the gateway's cost is right.** unreconciled $0.0000, `usage_unparsed` 0. On `claude-sonnet-5` the gateway and Claude Code agree; the gap is `claude-sonnet-5-5`, which Claude Code does not know and prices at its default 2.5x (see the note below).
  - **A9:** largest output of one call 1,942 tokens (`claude-sonnet-5`) and 201 (`claude-sonnet-5-5`) against role limits of 4,000.
  - **A-N8:** `usage.service_tier` `standard`, `usage.inference_geo` `global`, `tools[].type` `custom`.
  - **A-N9:** the largest charge was 30.0% of its reservation across 20 calls, including the one that read `logo.png`.
  - **R8, A-N1:** the run under a $0.002 cap ended `halted (run_cap)`, outcome draft, exit 0, one call reached the model; `claude` exited 449 ms after the gateway's 403 (grace 60 s).
  - **The planted key** is in no log, transcript, bucket object or provider state of either run.
  - **The fix stage** resumed implement's session (three calls); the run still ended `failed` ("review round 2 still has 2 findings"), which the test allows.
- **Vertex facts (A-N3, A9 Vertex side, A11 Vertex price):** recorded, or "open: Vertex `enforce` stays refused".
- **Assumptions this run answers:** A6 and A-N5 (the managed settings), A-N7 (the plain http base URL), A-N6 (pins per role), A-N1 (the 403 and the grace), A-N2 (the result event's count against the gateway's), A-N8 (default request shapes), A-N9 (the image ceiling) and A10 and A11 (cost). Each stays open until a `FACT` above answers it.

### Check 20: why Claude Code's cost differs from the gateway's (2026-10-01)

The first real run settled $0.4232 at the gateway while Claude Code's `total_cost_usd` summed to $0.5944; tokens matched exactly per model. The Anthropic Console charged $0.42, so the gateway's table is right and Claude Code's estimate is high. Reading Claude Code 2.1.283 (`/home/fugaro/.local/bin/claude`) explains the gap exactly:

- Its model catalog carries pricing tiers per million tokens (input / output / cache write 5m / cache write 1h / cache read): `tier_2_10` 2 / 10 / 2.5 / 4 / 0.2 (Sonnet 5), `tier_3_15`, `tier_5_25` 5 / 25 / 6.25 / 10 / 0.5, Haiku 4.5 1 / 5 / 1.25 / 2 / 0.1, Opus 5.5 4 / 20 / 5 / 8 / 0.2, Fable 5.1 10 / 50 / 12.5 / 20 / 0.25. These agree with `internal/pricing`'s rows.
- **`claude-sonnet-5-5` is not in its catalog.** A model it does not know is priced at its default, the `5 / 25 / 6.25 / 10 / 0.5` tier (`pricing: "default"`), which is 2.5 times Sonnet 5.5's real $2 / $10 row. The gateway's per-model cost for `claude-sonnet-5-5` was $0.114173; times 2.5 is $0.285433, and with `claude-sonnet-5`'s $0.308982 that is $0.5944, Claude Code's total.
- Claude Code also multiplies by 1.1 for `inference_geo: us`; this run reported `global`, so it did not apply.

So Claude Code's cost estimate is not the arbiter for any model newer than its catalog; the gateway's table, checked against the Console, is. `TestLiveGateway` now records the gap per model and recomputes the gateway's tokens at Claude Code's prices instead of failing on it.

## Results of the sixth live run (M9b, check 21)

Fill in at your own terminal. Date, base image, Firebase project, who ran it.

| Item | Result |
|---|---|
| A2 ADC token accepted by the RTDB REST API | |
| A12/A15 signJwt, custom token, no provider enabled, claims at top level, refresh | |
| A13 rules at ~37 KB deployed; denials (another run's ledger, `config/mode`) | |
| A4/A14 lease, release, counters under the plain decimal day | |
| A-F1 lease give-ups / stale denials (count, of leases) | |
| Run 1 cost (gateway) and the Console's charge | |
| Run 2 `repo_daily_cap` halt | |
| Run 3 `run_cap` halt | |
| Run 4 kill: seconds from write to halt; stream or poll | |
| Run 5 `budget_token_expired` | |
| Run 6 `budget_unavailable` (D14) | |
| Secrets scan | |
| A-F2 SSE over Cloud Run egress for 1 h; `auth_revoked` on refresh | |
| Sweeper execution, `execution.template.containers[0].env` override | |
| First CI run of the history image job | |
| `createdAt` of custom-token users; Secure Token error codes | |
| `null_etag` of a never-written node | |
| `batchGet` / `batchDelete` shapes | |
