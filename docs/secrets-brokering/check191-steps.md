# Checks 191 and 192 — the owner's steps (the Bitbucket proxy end to end, and the flip)

**Status: written by task B1.11 of the secrets-brokering plan; run by the owner, task B1.12 (Check
191) and the flip's own task (Check 192).** Neither has been run: Check 191 runs on the **released**
0.15.0 and not on a development build — a build from a checkout resolves no released
credentials-service image to deploy (`internal/cli/init_credentials.go`, `resolveCredentialsImage`),
so `fugaro version` must not print `dev` — and Check 192 runs after the flip (D500, O7). **No agent runs any step of either**, and no agent has: what they measure is a live
installation, real runs and the credentials service's own answers.

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
a push to `main`, a tag, a delete, another pull request's update, a merge or `/workspaces` is refused.
That is why the check launches throwaway runs whose task tells the agent to make each attempt and
print one line per attempt.

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
  hard-coded in the script, and `fugaro doctor --project fugaro` must answer that this is the
  `fugaro-dev` installation: the script refuses to run against anything else. Never `belong`, and a
  repository whose workspace is `belong` is refused by name.
- **The credentials service on for this installation, and the sandbox repository published with
  `proxy: true`** (the plan's B1.7/B1.8, `fugaro project policy publish` from the project's
  `project_secrets.bitbucket-token.allow_data_to`). Check 191 reads the published policy from
  `fugaro project policy show --project fugaro --json` and fails the `proxied` row — before it
  launches anything — when the repository is not admitted, or is admitted without `proxy: true`.
- **Three sandbox Bitbucket repositories of that installation**: `CHECK191_REPO` (the proxied one the
  runs work in), `CHECK191_OTHER_REPO` (a **second** one, for the other-repository refusal) and, for
  `--after-flip`, `CHECK191_PLAIN_REPO` (one the published policy does **not** proxy). Plus
  `CHECK191_OTHER_PR`: the number of an **open** pull request in `CHECK191_OTHER_REPO` that is not
  this run's, for the other-pull-request and merge attempts.
- `bash`, `mktemp`, `python3`, `curl`, `gcloud` and `fugaro` on PATH. `gcloud` must be authenticated
  as an account that may read Cloud Logging for the installation, read the job list, read
  `bitbucket-token`'s IAM policy, and **write and delete one node in the installation's Realtime
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
   `CHECK191_RUN_ID` (default `chk191-<UTC date>-<time>`), and a base that does **not** start with
   today's UTC date is refused: the service reads a run's outcome under the day its **run id** names,
   so a run id of another day is one whose ended-run probe would be written where the service never
   looks.

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
repository's jobs **giving up the token**: the job no longer mounts `bitbucket-token` and its account
holds no accessor on it. Check 192 reads that, and reads the other half — a repository the policy does
**not** proxy keeps its mount and keeps working.

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

Check 191: `proxied` (the published policy admits the repository with `proxy: true`), `run-launch` and
`run-outcome` (the working run reached a final status, succeeded and pushed nothing — a probe that
pushed is one this check's own restore cannot undo), the seven refusal rows
(`refusal-other-repository`, `refusal-other-ref`, `refusal-tag-ref`, `refusal-ref-delete`,
`refusal-other-pull-request`, `refusal-merge`, `refusal-workspaces`), `review-run`,
`ended-identity`, and the `restore` and `scratch-removed` rows. Each refusal row reads **three**
things and fails on any one of them: the run's own line for the attempt, the answer it got
(`refused=yes` with the service's own words), and the service's **audit line** for that refusal, under
the class the call belongs to — `other_repository`, `other_ref`, `tag_ref`, `ref_delete`,
`other_pull_request`, `call_not_allowed` (a merge and `/workspaces` are the two `call_not_allowed`
attempts) and `no_run_branch` for the review-only run's push. A refusal the service did not audit is
not a pass: the audit line is the other half of the reading.

**Exit codes.** `0` is every criterion that was measured holding. `1` is a measured criterion that did
not hold, or a guard that stopped the check before it ran. `2` is the check not being able to
**measure** — a tool is missing, or the ended-run probe could not be set up (the node could not be
written, or did not read back). A key the run never reached reads `FAIL — not reached`.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1` (a guard
that stopped it before it could run) or `2` (it could not measure). The stops worth knowing by name:

- **`this is not the fugaro-dev installation`** — `fugaro doctor --project fugaro` printed no
  `project: ... (GCP fugaro-dev,` line. Either this is another installation, or `--project fugaro`
  disagrees with the checkout's `project:`, or doctor itself failed. Nothing was created.
- **`this shell is tracing`** — the check was started with `bash -x`. Run it without `-x`: it prints
  its own block, and a trace is not needed to report a failure.
- **A repository the policy does not proxy, when Check 191 needs one that is** (`proxied: FAIL`) — the
  published policy does not admit `CHECK191_REPO`, or admits it without `proxy: true`. Publish the
  sandbox repository, or run this check as `--after-flip` if the flip has already happened.
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
run and the ended-run probe are the two that write nothing: a review-only run's push must be refused,
and the probe is refused before it can be served at all.

**No agent runs any step of either check.**
