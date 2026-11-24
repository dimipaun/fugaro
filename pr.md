live-checks: Check 195 reads the build's own step exit code, so its verdict no longer needs the log

## What the owner's run showed

On `fugaro-dev` (0.28.2, 2026-11-24) the check's build ended FAILURE and could not say
why: `gcloud builds log` printed an empty body and
`gcloud logging read 'resource.type="build" resource.labels.build_id=…'` returned no
lines at all, so the `call` row — the token's `email` claim and the status the service
answered — was left **unmeasured**. The project-level `roles/logging.logWriter` binding
had been granted seconds before the build: IAM propagation had not landed, and a build
that names its own service account writes its log **with that account's credentials**,
so it wrote nothing anywhere. A check whose only reading was the log had no reading at
all.

## What it does now

1. **The step ends with one exit code per outcome**, and Cloud Build writes that code
   into the build's own record — which is not Cloud Logging: `0` = the service answered
   200 **and** the token's `email` claim is the throwaway account, `11` = 401, `12` =
   403, `13` = any other status or none at all, `14` = the metadata server minted no
   token, `15` = the claim is another identity. The check reads it with
   `gcloud builds describe <build id> --format='value(steps[0].exitCode)'`. The step's
   script now runs `set -uo pipefail` rather than `set -euo`: every way out of it has to
   be one of those codes, and a command's own failure status means nothing here.
2. **The log line is still read** when it arrives (`CHK195 status=… email=…`), for the
   `email` claim as **text** and the status text; with the log present every row reads
   exactly as before. When it does not arrive, `call_from_step_exit_code` names the
   cause from the exit code. An exit code this step never writes — a `1`, a shell's own
   status — is a **failure whose cause is not named**, never a guess at one: the step
   has one `exit` per outcome and nothing else in it exits.
3. **The `logWriter` grant is waited for, bounded**, so the log gets written in the
   first place: the project's own policy is re-read until it names the throwaway account
   (`CHECK195_LOGWRITER_WAIT_SECONDS`, 120 s), then one fixed settle
   (`CHECK195_LOGWRITER_SETTLE_SECONDS`, 30 s), and the new `logwriter-wait` row says
   what was waited and what the policy read back. Nothing rests on it: a wait that runs
   out costs this run the claim's text, never the reading.
4. **The Restore deletes the account even when the build FAILED**, and retries it. The
   owner's run came back with the service deleted, the binding removed **and read back
   clean**, and the account still there: the binding removal and the account deletion
   are ordered IAM writes and IAM's own consistency can put them out of order, so the
   delete was refused as *still referenced by a policy binding*. Every attempt now
   re-reads the policy first, and up to six attempts are made; the last refusal is kept
   in the row, with the exact `gcloud iam service-accounts delete …` to run by hand.

### What the Restore does, per failure

- **The build FAILED, was refused, or never started** — nothing changes. The deploy is
  deleted (taking its `roles/run.invoker` binding with it), the project-level
  `roles/logging.logWriter` binding is removed, the throwaway account is deleted. The
  build is cancelled only if it is still running.
- **The service delete fails** — read back and reported (`service-deleted` FAIL) with
  the name to delete by hand; the other two writes go on.
- **The project binding removal fails** — read back and reported (`binding-removed`).
  Its not having landed is exactly what makes the account delete refuse, which is why
  the account delete re-reads the policy and retries.
- **The account delete fails** — up to six attempts, each preceded by a read of the
  project policy and followed by a read of the account's **own state**, so a delete that
  reported failure but landed is never reported as left behind. If the account is still
  there, `account-deleted` FAILs naming it and the exact delete command.
- **A Ctrl-C at any point** — the same Restore runs from the exit trap.

## Tests

`scripts/live-checks/check195_test.sh` (fake `gcloud`) gained, on top of the existing
cases: a build that fails with each of `11`, `12`, `13`, `14`, `15` and an unknown
`1`, **with no log lines at all** — the check names the cause from the exit code and
still restores everything; a build whose record carries no `steps[0].exitCode` and no
log (a failure naming both); exit code `0` with no log (a PASS); the 403 case **with**
the log present (every row unchanged, and the row does not mention the exit code); a
delete that fails once and then succeeds (exactly two delete calls); and a `logWriter`
binding that never lands (`logwriter-wait` FAIL, with the `call` row still read from
exit code `0`).

`bash scripts/live-checks/check195_test.sh` → ok, and each shard
(`CHECK_TEST_SHARD=0/3`, `1/3`, `2/3`) → ok. `go test ./internal/cli/ -run Docs
-count=1` → ok. `fugaro verify build` → passed.

## `fugaro verify test`: red on `main`, not on this change

`fugaro verify test` passed the `scripts` package — where this change lives — and failed
**10 tests in `internal/cli`**: `TestBaseBranchOnAFollowUp`,
`TestCompressionImageGateRefusesOldBuild` (+2 subtests), `TestRunRecipeReviewModeWritesReviewPRs`,
`TestRunRecipeReviewModeIgnoresAnyExistingChain`, `TestRunPickWritesSeveralReviewPRs`,
`TestRunReviewModeKeepsRefWorkflowFlags`, `TestStoredFollowUpKeepsAHotfixRequest`,
`TestStoredFollowUpKeepsARaise`.

They are **not** this branch's: they fail the same way on their own
(`go test ./internal/cli/ -run '…' -count=1`), and `git diff origin/main --stat` is this
branch's four files, all of them under `docs/` and `scripts/live-checks/` — no Go file
is touched, so `internal/cli` is byte-identical to `main` and these failures are
`main`'s. They share one shape: a job-image skew gate (`internal/cli/recipes_skew.go`,
the *needs a job image whose runner knows …* family, added by the job-title work in
`5d5112a`) now refuses before the refusal the test expects, and a stored follow-up is
refused for having no title before its own field is read. Fixing them is a different
task's work, so this run left them alone — see Deferred. **`main`'s CI wants them fixed
before the next cloud run is launched.**

## Not in this run

No agent ran `scripts/live-checks/check195.sh`, here or anywhere: the hermetic test is
what pins the new behaviour, and the owner's run (the secrets-brokering plan's B4.2) is
what observes the exit-code path live. The check prints no screenshots, so nothing in
this run needed an image read.

## Deferred

- **The 10 red `internal/cli` tests on `main`** (`5d5112a`'s job-image gate firing ahead
  of the refusals those tests expect). Skipped: they are another task's area, and
  changing them is behaviour beyond this brief. They must be green before `main` can
  carry a cloud run.
- **The log-present path does not cross-check the step's exit code.** A line that says
  `status=200` and a step that exited `11` are not compared. Skipped on purpose: both
  readings are the same fact from the same step, so a disagreement could only mean the
  check was handed a different build, and a row that argues with itself is worse than
  one that reads.
- **A log-less PASS asserts the `email` claim by construction, not by text.** With exit
  code `0` the row says the claim *is* the throwaway account because the step writes `0`
  for that outcome alone; the owner does not see the address itself, only the
  `gcloud builds log` command that would print it. That is the whole point of the
  change, and re-minting a token to print the claim is not something this check should
  do.
- **The `logWriter` wait is a bounded re-read plus a fixed settle, not proof of
  propagation.** GCP offers no read that proves a binding has propagated. The row says
  what it waited and what it read rather than claiming the log will arrive.
- **`CHECK195_ACCOUNT_DELETE_RETRY_SECONDS` (and the existing
  `CHECK195_GRANT_RETRY_SECONDS`) stay out of the header's knob list.** They only
  shorten a wait the hermetic test would otherwise sit through, which is how the
  existing internals are handled; the header documents the knobs an owner would set.
- **The `account-delete` retry count (6) and its 5 s spacing are constants, not knobs.**
  Six attempts cover every ordering the two IAM writes can take in practice; an owner
  who needs more has the exact command printed in the FAIL row.
