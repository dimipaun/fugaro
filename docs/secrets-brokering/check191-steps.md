# Checks 191 and 192 — the owner's steps (the Bitbucket proxy end to end, and the flip)

**Status: written by task B1.11 of the secrets-brokering plan; run by the owner, task B1.12 (Check
191) and the flip's own task (Check 192).** Neither has been run: Check 191 runs on the **released**
build that first carries the proxy — **0.21.0** (the plan's Levels table gives the proxy chain
0.15.0, and that release has no proxy to measure: `internal/credsvc/bitbucketproxy.go` landed
2026-11-07, and 0.21.0 is the first tag that holds it) — and not on a development build: a build from
a checkout resolves no released credentials-service image to deploy
(`internal/cli/init_credentials.go`, `resolveCredentialsImage`), so `fugaro version` must not print
`dev`. Check 192 runs after the flip (D500, O7). **No agent runs any step of either**, and no agent
has: what they measure is a live installation, real runs and the credentials service's own answers.

The check is `scripts/live-checks/check191.sh` — one script, two modes: without an argument it runs
**Check 191**, with `--after-flip` it runs **Check 192**. The register's sections are
`docs/gcp-live-checklist.md`, "Check 191" and "Check 192", and that is where the block you paste back
is recorded; the script's own header carries what each settles (the plan's B1.11, the reconciliation's
D493 and D500, and the constraints **G1**, **G2**, **G4**, **G9**), what it creates, what it costs and
what its restore deletes. Read those before you start. This file is the commands.

## Why the attempts have to come from inside a run

Every row Check 191 exists for is a **refusal**, and a run's identity is sealed to its own execution:
its calls carry a launch token and its own job identity, bound to its run id and its repository. A
call made from your machine — with your own credentials, or with the service's — is answered about
**that** identity, not about a run's, so it can say nothing about whether a run in another repository,
a push to `main`, a tag, a delete, a write to another pull request of the run's **own** repository, a
merge or `/workspaces` is refused. That is why the check launches throwaway runs whose task tells the
agent to make each attempt and print one line per attempt.

**Two of the seven attempts are only reachable from the right target.** `other_pull_request` is what a
pull-request write earns when the pull request is not the run's — and the rules refuse a call that names
another **repository** for the repository *before* they look at the pull request, so a pull request of
another repository would earn `other_repository` and never `other_pull_request`: the attempt has to be a
write to a pull request of the run's own repository that is not the run's own. `call_not_allowed` is the
classification's own refusal — a merge and `/workspaces` are not paths this proxy names at all, so they
earn it wherever they point.

**And the probes are real runs.** The working run — and both of Check 192's runs — first commits one
line, pushes its branch and opens its pull request, because a run whose agent commits nothing is
**failed** by the runner ("the agent made no commits", which records an empty commit and pushes it as
a draft anyway): a probe that changed nothing would measure a failed run rather than the proxy. That
push and pull request are also the **positive** half of the reading — the service SERVES the clone,
the push and the pull request that the same identity is later refused for. Those branches and pull
requests are left on the sandbox repositories: the check holds no Bitbucket credential of its own and
neither deletes nor closes them, and the block names each one.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are
  hard-coded in the script, and `fugaro doctor --project fugaro --all` must answer that this is the
  `fugaro-dev` installation: the script refuses to run against anything else. Never `belong`, and a
  repository whose workspace is `belong` is refused by name.
- **The credentials service on for this installation, the sandbox repository on the
  credentials-service switch (`agent.auth: cascade` for it, with a model credential in the service's
  bundle, its root re-applied), and the sandbox repository published with `proxy: true`** (the plan's
  B1.7/B1.8, `fugaro project policy publish` from the project's
  `project_secrets.bitbucket-token.allow_data_to`). Check 191 reads the published policy from
  `fugaro project policy show --project fugaro --json` and the switch from the credentials service's
  OWN IAM policy (`gcloud run services get-iam-policy fugaro-credentials --project fugaro-dev --region
  us-east5 --format=json`) crossed with the repository's REAL jobs (the jobs whose own `FUGARO_REMOTE`
  names it, from the job list the check already reads), and fails the `proxied` row — before it
  launches anything — when the repository is not admitted, is admitted without `proxy: true`, or is
  off the switch: the published half alone is **inert**, a repository off the switch holds its own
  token and is served by nothing, so the check stops rather than launch three runs at a path the proxy
  is not on. Its remedy is to set `agent.auth: cascade` for that repository, run `fugaro update
  --repo` for it and run the check again. It stops the same way — unmeasured, nothing launched — when
  `fugaro doctor --project fugaro --json` says the service is not there at all or has nothing to serve
  a run's model credential out of: every model call of a run against such a service is refused, so the
  three runs would fail at their first call rather than measure the proxy.
- **Three sandbox Bitbucket repositories of that installation**: `CHECK191_REPO` (the proxied one the
  runs work in), `CHECK191_OTHER_REPO` (a **second** one, for the other-repository refusal) and, for
  `--after-flip`, `CHECK191_PLAIN_REPO` (one the published policy does **not** proxy). Plus
  `CHECK191_OTHER_PR`: the number of an **open** pull request **in `CHECK191_REPO`** — the run's own
  repository, and not the run's own pull request — for the other-pull-request and merge attempts. It
  has to be this repository's: a call naming another repository is refused for the repository before
  the pull-request rules are reached, so a pull request of `CHECK191_OTHER_REPO` would earn the
  other-repository refusal and never `other_pull_request`. `CHECK191_OTHER_REPO` itself is only ever
  the target of the other-repository attempt.
- `bash`, `mktemp`, `python3`, `curl`, `gcloud` and `fugaro` on PATH, and `git` for `--after-flip`
  (the checkout's origin remote, so the check knows the directory it is about to read doctor in is
  this repository's). `gcloud` must be authenticated as an account that may read Cloud Logging for
  the installation, read the job list, read the credentials service's own IAM policy, list the
  installation's secrets and read the IAM policy of the
  one holding `bitbucket-token`, and **write and delete one node in the installation's Realtime
  Database** (Check 95 step 3 in the register is the same write): the ended-run row is set up by
  hand, as you, with `gcloud auth print-access-token` and `curl`, because no call of a run's own can
  put its identity in that state. The URL is read out of the installation's own
  `~/.config/fugaro/projects/fugaro.yaml` (`budget.rtdb_url`); name another file with
  `CHECK191_CONFIG`.
- **Around 40 minutes per run** at the terminal (`CHECK191_WAIT_SECONDS`, 2400 by default), three
  runs for Check 191 and two for Check 192, plus the audit read: a few cents of Cloud Run time per
  run and **no model call** for the seven attempts, which are refused before a call is served.

## Check 191 — the steps

1. **The dry run first.** It reads nothing from the network and launches nothing: it runs the guards,
   then prints every command a real run would run and every row the block would carry.

   ```sh
   CHECK191_REPO=<workspace>/<proxied sandbox repo> \
   CHECK191_OTHER_REPO=<workspace>/<another sandbox repo> \
   CHECK191_OTHER_PR=<n> \
     bash scripts/live-checks/check191.sh --dry-run
   ```

2. **The run itself** — the same command without `--dry-run`. The run's ids are built from
   `CHECK191_RUN_ID`, which must be today's UTC date and a time (`YYYYMMDD-HHMMSS`; the default is
   now). Each run's id adds four hex characters of its own, because `fugaro run` admits one shape and
   only one (`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`), and the base's day is the day the service reads a
   run's outcome under: a run id of another day is one whose ended-run probe would be written where
   the service never looks. A base outside that shape is refused before anything is launched.

   ```sh
   CHECK191_REPO=<workspace>/<proxied sandbox repo> \
   CHECK191_OTHER_REPO=<workspace>/<another sandbox repo> \
   CHECK191_OTHER_PR=<n> \
     bash scripts/live-checks/check191.sh
   ```

   Progress goes to stderr; one `CHECK 191 RESULT` block goes to stdout, at the end. **Paste that
   whole block back to the coordinator** and nothing else: it is verdicts and the check's own short
   words, and it carries no token, no header and no log body.

## Check 192 — the steps, after the flip

The flip (the plan's B1.13 part two, D500) turns the proxy from a per-repository publish into the
repository's jobs **giving up the token**: the job no longer mounts the Bitbucket token and its
account holds no accessor on it. Check 192 reads that, and reads the other half — a repository the
policy does **not** proxy keeps its mount and keeps working.

**Run it from the proxied repository's own checkout** (the directory `fugaro update --repo` was run
in), or name that directory with `CHECK191_CHECKOUT`. One of its rows is `fugaro doctor`'s
`bitbucket-direct` line, and doctor has no `--repo`: it resolves the repository it answers about from
the **working directory**, and prints no `bitbucket-direct` line at all anywhere else — which would
read as a finding about the flip when it is a finding about where you were standing. The check
resolves the checkout's own origin remote and **stops before it launches anything** if it is not this
repository's, so that mistake costs a second rather than two runs.

1. **The dry run:**

   ```sh
   CHECK191_REPO=<workspace>/<the proxied sandbox repo> \
   CHECK191_PLAIN_REPO=<workspace>/<a NOT-proxied sandbox repo> \
     bash scripts/live-checks/check191.sh --after-flip --dry-run
   ```

2. **The run**, the same command without `--dry-run`. Its block is headed `CHECK 192 RESULT`.

   ```sh
   CHECK191_REPO=<workspace>/<the proxied sandbox repo> \
   CHECK191_PLAIN_REPO=<workspace>/<a NOT-proxied sandbox repo> \
     bash scripts/live-checks/check191.sh --after-flip
   ```

## What the block reads

Check 192's `mount` reads whether any job of the repository carries `FUGARO_BITBUCKET_TOKEN` as a
Secret Manager reference — the DERIVED container ID, never the logical name — and `accessor` reads
the IAM policy of every secret the installation labels `fugaro_secret=bitbucket-token` (there is one
project-wide container, and a repository that stored the token itself has its own). No job account of
the repository may appear in any of them. If no secret carries that label the row was not measured:
it reads `FAIL` and the check ends `2`, because there was no policy to read.

Check 191: `proxied` (the published policy admits the repository with `proxy: true` **and** the
credentials-service switch is on for it — the switch is read from the credentials service's OWN IAM
policy (`gcloud run services get-iam-policy fugaro-credentials --project fugaro-dev --region us-east5
--format=json`) and the repository's REAL jobs (the jobs whose own `FUGARO_REMOTE` names it, from the
job list the check already reads): it is ON when every one of those jobs runs as an account that
policy grants `roles/run.invoker` to and at least one job exists, OFF when an existing job's account
is not granted, and UNREAD — a row the check could not measure — otherwise. `fugaro doctor`'s
`credentials-service-deployed` sentence is NOT the decision: the released doctor lists job accounts of
workflow variants the repository does not deploy, so the doctor row is read only for the states where
the service cannot serve a run at all — the installation has no credentials service, or it has one that
admits every job account and holds nothing to serve a model credential out of — and its text is carried
in the row for context.
`proxy: true` is inert for a repository off the switch, and a row that passed on the policy alone
would call a run at the direct path a broken proxy), `run-launch` and
`run-outcome` (the working run reached a final status, succeeded, and the service served its push and
its pull request — the branch and the number the check leaves for you), the seven refusal rows
(`refusal-other-repository`, `refusal-other-ref`, `refusal-tag-ref`, `refusal-ref-delete`,
`refusal-other-pull-request`, `refusal-merge`, `refusal-workspaces`), `review-run` (a **review-only**
run launched with `--pr`, the working run's own pull request: a review-mode recipe reviews a pull
request and needs exactly one, and such a run has no branch of its own, which is what the row is
about), `ended-identity`, and the `restore` and `scratch-removed` rows. Each refusal row reads **three**
things and fails on any one of them: the run's own line for the attempt, the answer it got
(`refused=yes` with the service's own words), and the service's **audit line** for that refusal, under
the class the call belongs to — `other_repository`, `other_ref`, `tag_ref`, `ref_delete`,
`other_pull_request`, `call_not_allowed` (a merge and `/workspaces` are the two `call_not_allowed`
attempts) and `no_run_branch` for the review-only run's push. A refusal the service did not audit is
not a pass: the audit line is the other half of the reading.

**Exit codes.** `0` is every criterion that was measured holding. `1` is a measured criterion that did
not hold, or a guard that stopped the check before it ran. `2` is the check not being able to
**measure** — a tool is missing, the ended-run probe could not be set up (the node could not be
written, or did not read back), or no secret carries the label the accessor row is read from. A key
the run never reached reads `FAIL — not reached`.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1` (a guard
that stopped it before it could run) or `2` (it could not measure). A `proxied` row that FAILs ends
Check 191 the same way — the block prints, every row below it reads `FAIL — not reached`, and nothing
was launched — with the reason in the row itself rather than in an `aborted` fact. The stops worth
knowing by name:

- **`this is not the fugaro-dev installation`** — `fugaro doctor --project fugaro --all` printed no
  `project: ... (GCP fugaro-dev,` line (that line is part of doctor's `--all` tail: the default view
  stops at its pointer lines). Either this is another installation, or `--project fugaro`
  disagrees with the checkout's `project:`, or doctor itself failed. Nothing was created.
- **`this shell is tracing`** — the check was started with `bash -x`. Run it without `-x`: it prints
  its own block, and a trace is not needed to report a failure.
- **`is a checkout of <another repository>`**, or **`is not a checkout with an origin remote`**
  (Check 192, before anything is created) — `CHECK191_CHECKOUT` (the working directory, unless you set
  it) is not the proxied repository's own checkout, and its `doctor` row would be read about another
  repository or about none. `cd` into `${REPO}`'s checkout, or set `CHECK191_CHECKOUT` to it.
- **`CHECK191_RUN_ID must be today's UTC date and a time`** — the run id base is not
  `YYYYMMDD-HHMMSS` (or is another day's): the runs' ids are built from it as `<base>-<4 hex>`, which
  is the only shape `fugaro run` admits. Fix the knob, or unset it and take the default.
- **The accessor row could not be read** (`accessor: FAIL`, exit `2`, Check 192) — no secret of the
  installation carries the label `fugaro_secret=bitbucket-token`, so there was no IAM policy to read.
  Read `gcloud secrets list --project=fugaro-dev` yourself: a repository that stored the token in a
  container of its own has one, and that container's policy is what this row is about.
- **A repository the policy does not proxy, when Check 191 needs one that is** (`proxied: FAIL`) — the
  published policy does not admit `CHECK191_REPO`, or admits it without `proxy: true`. Publish the
  sandbox repository, or run this check as `--after-flip` if the flip has already happened. The check
  stops at this row: nothing below it was measured and no run was launched.
- **The repository is off the credentials-service switch** (`proxied: FAIL`) — the published policy
  admits it with `proxy: true`, and the credentials service's OWN IAM policy does not grant
  `roles/run.invoker` to every account the repository's jobs run as, so its own `agent.auth` is not
  `cascade` (or its root was never re-applied with it): the job mounts the token and the runs push
  straight to Bitbucket. Set `agent.auth: cascade` for that repository (with a model credential in the
  service's bundle, `fugaro creds set`), run `fugaro update --repo` for it and run the check again.
  The check stops here, and no run was launched — the rows below would have measured the direct path,
  not the proxy. An installation with no credentials service at all fails this row with that reason
  instead: `proxy: true` is inert without one, and neither the policy nor a grant can even be read.
- **The service has nothing to serve a run** (`proxied: FAIL`, exit `2`) — `fugaro doctor --project
  fugaro --json` says the credentials service admits every job account and that the bundle it serves
  from holds no version, or that the newest version of it is not `ENABLED`: every model call of a run
  against it is refused, so whether the repository is on the switch was **not measured** and the three
  runs would fail at their first call. Give the service a model credential (`fugaro creds set` is the
  bundle's only writer; a version Secret Manager will not serve has to be enabled first) and run the
  check again.
- **The switch could not be read** (`proxied: FAIL`, exit `2`) — the service's own IAM policy or the
  job list could not be read, the policy binds a job account in a way this check cannot interpret (a
  custom role, or a grant that holds only while a condition does), no job of the repository is in the
  list, a job runs as no account the check can name, or `fugaro doctor`'s
  `credentials-service-deployed` row is one this check does not place (a bundle it could not read, or a
  document carrying no such row) — so whether the repository is served was **not measured**. Read the
  two facts yourself — `gcloud run services get-iam-policy fugaro-credentials --project fugaro-dev
  --region us-east5 --format=json`, and the jobs of the repository in `gcloud run jobs list` — and
  settle it before you launch anything: a run whose switch nobody settled is a run whose rows are
  about nothing.
- **`the working run reported NO line for the … attempt`** — the probe's task asked for one
  `CHK191 attempt=<name> refused=… answer=…` line per attempt and the log holds none: the attempt was
  never made or never printed. Read `fugaro logs <run id>` and see what the agent did.
- **`was SERVED`** — the attempt was **answered** instead of refused. This is the finding the check
  exists for: a call the proxy must refuse got through. Read that run's log and the service's audit
  lines before anything else, and do not re-run the check over it.
- **`but the service's audit log holds N line(s) classified … where M … need it`** — the run was
  refused, but the service did not audit the refusal under the class the call belongs to. Read the
  audit lines yourself (`gcloud logging read`, `jsonpayload.run`) and the run's log.
- **The ended-run row could not be set up** (`ended-identity: FAIL`, exit `2`) — the outcome node
  could not be written (the Realtime Database refused the write: the URL is not this installation's,
  or the account may not write it), or was written and did not read back. **No probe was launched**
  and nothing about the ended-run refusal was measured; the row's detail says which of the two, and
  the node, if one was written, is left for the restore to delete.
- **`ended-identity: FAIL` on a launched probe** — the probe was launched with its outcome already
  written and was **not** refused (`without the service's ended-run refusal in its own log or its
  row`), or it pushed, or opened a pull request. That is the reading that the write-once outcome no
  longer ends an identity: run `fugaro diagnose <slug>/<run id>` before anything else.

## What is left behind

The runs themselves, whose records stay (that is what `fugaro runs ls` shows), and nothing else: the
scratch directory, and **one Realtime Database node** Check 191 wrote by hand for the ended-run probe,
`outcomes/<UTC epoch day>/<slug>/<run id>.json`. The restore deletes that node and **reads it back**
rather than assuming the delete worked — the `restore` row is that reading, and a `restore: FAIL` row
names the URL so you can delete it with `curl -X DELETE` and your own access token. It runs from the
exit trap too, so a Ctrl-C in the middle still deletes the node.

The runs the check launches each commit one line, push their branch and open their pull request, and
**those branches and pull requests are left behind** — the block names each one, and they are yours to
settle on the sandbox repositories (the check holds no Bitbucket credential of its own, so it cannot
delete them; Check 190 instead has a token in a file for its own throwaway branch). The review-only
run and the ended-run probe are the two that write nothing: the review-only run reviews the working
run's own pull request (a review-mode recipe is launched with the `--pr` it reviews and has no branch
of its own), so its push must be refused, and the probe is refused before it can be served at all.

**No agent runs any step of either check.**
