#!/usr/bin/env bash
# Check 195 — a Cloud Build step calls an IAM-only Cloud Run service.
#
# It settles the one thing S4's build exception rests on (plan B4.1, its "What
# it measures"; the reconciliation's U4 and U10): that a repository's BUILD
# account, running inside Cloud Build, can mint a Google ID token out of the
# build's OWN metadata server, aimed at a Cloud Run service, and be admitted by
# that service's IAM. Offline nothing answers it: the token is minted by the
# metadata server of a real build, and only Cloud Run can say whether the grant
# admits the call. So this check makes the whole path by hand, with throwaways —
# a Cloud Run service deployed WITH IAM REQUIRED, a build account granted
# roles/run.invoker ON THAT SERVICE ALONE, and a Cloud Build whose step runs as
# that account, fetches the token and calls the service — and reads the answer
# back from TWO places that are not the log: the build's OWN RECORD, where the
# step's exit code ends (one code per outcome, see write_build_config), and an
# OBJECT the step writes its one line into, in a throwaway bucket this check
# creates. The line is read out of the log too when the log arrives, for the same
# status and email claim as text; nothing rests on it — on 2026-11-24 the owner's
# run came back with a FAILURE build and an empty `gcloud builds log`, and a check
# that could only read the log had no reading at all. B4.4 writes the Terraform
# that gives a
# repository's build account exactly that one grant (on the credentials service)
# and B4.3 serves it, and this is the reading both are built on: a FAIL stops S4
# and returns to the owner as a decision.
#
# Usage:      bash scripts/live-checks/check195.sh
#             bash scripts/live-checks/check195.sh --dry-run   (prints the plan
#             and calls NOTHING: no gcloud, no fugaro, no curl)
# Needs:      bash, mktemp, date, gcloud (authenticated as you, as an account that
#             can create a service account, a Cloud Run service, a bucket and a
#             build, add and remove one project-level IAM binding and one
#             bucket-level one, read the bucket's own policy and an object in it,
#             and read the build's own record and its lines — a project Owner
#             can), curl (one unauthenticated request, to find a project-level
#             public invoker the service's own IAM policy cannot show) and fugaro
#             (doctor, to assert that this project is the installation's).
#             Nothing is installed and nothing is fetched.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             Both are HARD-CODED in this file and never read from `gcloud
#             config get-value project`, and `fugaro doctor` must answer that
#             this is the fugaro-dev installation: a check that read the project
#             from the environment could be pointed at another installation by a
#             stray config, and this one must never be. Never belong.
# Creates:    one throwaway Cloud Run service (fugaro-chk195), deployed from a
#             stock public container image and left IAM-only; one throwaway
#             service account (fugaro-chk195-caller); the roles/run.invoker
#             binding on that service which admits it; one throwaway Cloud
#             Storage bucket (fugaro-dev-chk195-<the moment this run started>),
#             which the build's step writes its one line into and this check reads
#             it back out of, with the account granted roles/storage.objectCreator
#             ON THAT BUCKET ALONE; and one Cloud Build. A service or an account
#             of those two names that was already there is a hard stop: this check
#             never adopts, writes into or deletes what it did not create. Bucket
#             names are global, so this one is neither a constant nor adopted: it
#             carries the moment this run started and a label only this run
#             writes, and a bucket without that label is never deleted.
# Costs:      one build and a moment of Cloud Run — minutes at the terminal,
#             not money.
# Runs:       by YOU. No agent runs any step of it, and no agent has: what it
#             measures is a live project. Its own test (check195_test.sh) drives
#             it against fake tools only.
# Cleanup:    always, including on a Ctrl-C: a build still running is cancelled
#             first, then the service (which takes the invoker binding with it),
#             the bucket (which takes the object the step wrote and the
#             bucket-level grant with it), the project-level binding, and the
#             throwaway account — and that
#             last one is RETRIED, because the binding removal and the account
#             deletion are ordered IAM writes that IAM's own consistency can put
#             out of order: the owner's run of 2026-11-24 ended with the binding
#             removed and read back clean, the service gone, and the account
#             still there — and what the refusal said was not captured, so the
#             re-read covers both ways that happens: a delete refused as still
#             referenced by a policy binding, and a delete that landed whose
#             account the list had not stopped naming yet. The bucket goes ahead
#             of both, so its own binding never outlives its member. Each of the four
#             is READ BACK rather than assumed — the `service-deleted`,
#             `account-deleted`, `bucket-deleted` and `binding-removed` rows are
#             that reading. The
#             build's own RECORD stays in the project's Cloud Build history: it
#             is the project's log of what ran, not a resource this check made.
# Output:     progress on stderr, ONE block on stdout headed CHECK 195 RESULT,
#             then ... RESULT END (exit N) — to copy back to the coordinator, and
#             nothing else. Exit 0: every criterion that was measured held. 1: a
#             measured criterion did not hold, or a guard stopped the check
#             before it could run. In the block, a key the run never reached
#             reads `FAIL — not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no authorization header and no
# credential of any kind. This check mints nothing itself: the only token in the
# whole path is minted inside the build, by the metadata server, for the build's
# own step, and it is used where it is minted. What reaches this script's output
# is the token's `email` CLAIM alone — the build account's own address, which is
# a name and not a credential — and every line it reads out of the build's log or
# out of the object is cut to a short plain-word shape before it is printed. The
# step never prints the access token it writes the object with either: its upload
# is `-o /dev/null`, and the token lives in a shell variable for one command.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK195_DRY_RUN (anything non-empty is the dry run), CHECK195_IMAGE (the
# container the throwaway service is deployed from; the default is Google's own
# Cloud Run sample container, so this check builds and pushes no image of its
# own), CHECK195_GRANT_SECONDS (how long the invoker grant is given to take
# effect — IAM is eventually consistent, and a call that lands in the window
# reads exactly like a missing grant), CHECK195_BUILD_WAIT_SECONDS (how long the
# build is waited for), CHECK195_LOG_WAIT_SECONDS (how long the build's own
# lines are waited for in Cloud Logging, whose ingestion lags the request),
# CHECK195_LINE_WAIT_SECONDS (how long the object the step wrote is re-read for),
# CHECK195_LINE_TRIES (how many times the step itself retries that write, and
# CHECK195_LINE_RETRY_SECONDS the pause between attempts: IAM is eventually
# consistent, and a build that starts inside the window is refused the write),
# CHECK195_GRANTS_WAIT_SECONDS (the bound on re-reading the project's own policy
# and the bucket's until the roles/logging.logWriter and
# roles/storage.objectCreator bindings this check added are in them) and
# CHECK195_GRANTS_SETTLE_SECONDS (the one short fixed settle given to those
# bindings before the build, so the propagation has happened whatever the
# read-back said). None of them can point this check at another project, another
# region or another service: the two names it creates are constants in this file.
set -euo pipefail

# No xtrace, whatever the shell it was started from had: the block this check
# prints is pasted into a pull request, and `bash -x` would print every command
# of the run into the log it is read from. BASH_XTRACEFD is unset so a trace
# redirected to a file this script knows nothing about cannot be reopened, and
# guard_xtrace REFUSES the run when the shell arrived with tracing on.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and runs the Restore for whatever this run created.
stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# The two the whole check hangs on, and the two names it creates. HARD-CODED,
# never `gcloud config get-value project`: see the header.
P=fugaro-dev
L=us-east5
SVC=fugaro-chk195
CALLER=fugaro-chk195-caller
CALLER_EMAIL="${CALLER}@${P}.iam.gserviceaccount.com"
# The bucket the step writes its one line into, and the object in it. This is the
# one name here that is NOT a constant, and it cannot be: bucket names are global
# to all of Google Cloud, so a fixed one either already belongs to somebody or is
# one this check could not create. It carries the moment this run started and this
# process's id, and it carries a LABEL of the same text — which is what says a
# bucket is this run's, since the name alone cannot.
BUCKET_NONCE="$(date -u +%Y%m%d-%H%M%S)-$$"
BUCKET="${P}-chk195-${BUCKET_NONCE}"
LINE_OBJECT="chk195-line.txt"
LINE_URI="gs://${BUCKET}/${LINE_OBJECT}"
# The container the throwaway service runs. A stock public image, on purpose:
# this check's subject is the IAM grant and the metadata token, never the
# service's own code, and building an image of its own would add a registry, a
# staging bucket and a second build to everything below for nothing. The image
# holds nothing, receives one ID token whose audience is this very service's URL
# (so nothing else accepts it), and the service is deleted at the end.
IMAGE=${CHECK195_IMAGE:-us-docker.pkg.dev/cloudrun/container/hello}
DRY_RUN=${CHECK195_DRY_RUN:-}
GRANT_SECONDS=${CHECK195_GRANT_SECONDS:-30}
BUILD_WAIT_SECONDS=${CHECK195_BUILD_WAIT_SECONDS:-1800}
LOG_WAIT_SECONDS=${CHECK195_LOG_WAIT_SECONDS:-60}
# The object the step wrote, re-read for this long: Cloud Storage hands an object
# back the moment it is written, so this is only slack for a transient read, and
# the step's own write is retried inside the build (LINE_TRIES) for the IAM window.
LINE_WAIT_SECONDS=${CHECK195_LINE_WAIT_SECONDS:-30}
LINE_TRIES=${CHECK195_LINE_TRIES:-6}
LINE_RETRY_SECONDS=${CHECK195_LINE_RETRY_SECONDS:-5}
GRANTS_WAIT_SECONDS=${CHECK195_GRANTS_WAIT_SECONDS:-120}
GRANTS_SETTLE_SECONDS=${CHECK195_GRANTS_SETTLE_SECONDS:-30}
# The unauthenticated probe's cap, so a service that never answers cannot hang
# the run.
CURL_SECONDS=30
# How long a reading that lags the request is re-read for, and how many of the
# build's own lines are read.
READ_POLL_SECONDS=10
LOG_AGE=30m
LOG_LIMIT=100
# How often the two policies are re-read while waiting for the bindings the
# build's own writes need to land, and how many times the throwaway account's
# deletion is attempted before the Restore gives up and says so. Every delete
# attempt is preceded by a policy read and followed by a read of the account's
# own state, so a delete that landed but reported failure is not repeated: the
# state is the answer.
GRANTS_POLL_SECONDS=5
ACCOUNT_DELETE_TRIES=6

# The one deploy line, in one place, so guard_deploy_is_iam_only can read exactly
# what a real run would run.
DEPLOY_FLAGS=(
  --project="$P" --region="$L" --image="$IMAGE"
  --no-allow-unauthenticated
)

case "${1:-}" in
--dry-run) DRY_RUN=1 ;;
"") ;;
*) die "usage: $PROG [--dry-run]" ;;
esac

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 195 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need gcloud
  need curl "the unauthenticated probe is the one request this check makes itself"
  # fugaro is checked here rather than left to guard_project's own call: a
  # machine without it would otherwise fail as "this is not the fugaro-dev
  # installation", which reads like the wrong installation rather than a missing
  # tool.
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk195-mine"
SERVICE_MINE=""
ACCOUNT_MINE=""
BUCKET_MINE=""
BUILD_ID=""
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
WAIT_PID=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk195.XXXXXXXX") || die "Check 195 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  SERVICE_MINE="$SCRATCH/service-mine"
  ACCOUNT_MINE="$SCRATCH/account-mine"
  BUCKET_MINE="$SCRATCH/bucket-mine"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  fact scratch PASS "$SCRATCH (mode 0700, made by this check, removed by its Restore)"
}

remove_scratch() {
  case "$SCRATCH" in
  "")
    # A guard stop leaves this empty and there is nothing to refuse: saying
    # "refusing to remove a scratch directory of ''" for a run that never made
    # one reads like a cleanup that went wrong. The guard stays for the two
    # paths that do mean trouble: a bare "/" and the owner's home directory.
    return 0
    ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# redact: what a gcloud message may carry back into the block, which is pasted
# into a pull request. A credential-shaped run of characters goes, whatever it
# is: the value of any Authorization or Proxy-Authorization header, a scheme
# with a credential-long value after it, or any name=value whose name says token
# or secret. No token of this check's ever reaches a message it prints, and this
# is the second line of that defence rather than the first.
redact() {
  sed -E \
    -e 's/([Aa]uthorization)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g'
}

# last_lines FILE [N]: the last N lines of a file, on one redacted line, cut
# short. The one shape every failure row reads a command's own stderr with.
last_lines() {
  local f=$1 n=${2:-2}
  [ -f "$f" ] || {
    printf 'no output'
    return 0
  }
  tail -n "$n" "$f" | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-400
}

# delete_account_retrying: deletes the throwaway account, and does not give up
# while the account is still there.
#
# The two IAM writes the Restore makes are ordered — the project-level binding
# goes before the account, because a binding that outlives its member reads back
# as deleted:serviceAccount:… and can never be removed again — and that order is
# also what IAM's own consistency can put out of order: the owner's run of
# 2026-11-24 ended with the service deleted, the binding removed AND read back
# clean, and account-deleted FAILed with the account still there. What the
# refusal said was not captured, and either of two causes fits it: a delete
# issued in the moment after the binding is removed is refused as still
# referenced by a policy binding even though the policy already reads clean, and
# a delete that landed can leave the account still named by a listing that has
# not caught up either. So the policy is re-read before every attempt and the
# account's own state after every one, and an attempt that did not take is
# retried; the last refusal is kept for the row. A build that FAILED is no
# reason to skip any of it: the account is this check's and goes either way.
delete_account_retrying() {
  local try=0
  : >"$SCRATCH/account-delete.err"
  while [ "$try" -lt "$ACCOUNT_DELETE_TRIES" ]; do
    try=$((try + 1))
    if [ "$(project_naming_account)" != clean ]; then
      # The binding's removal has not reached the policy the deletion reads:
      # an account delete now is exactly the refusal this loop is here for.
      sleep_bg "${CHECK195_ACCOUNT_DELETE_RETRY_SECONDS:-5}"
    fi
    gcloud iam service-accounts delete "${CALLER_EMAIL:?}" --project="${P:?}" --quiet >&2 2>>"$SCRATCH/account-delete.err" || true
    # A delete that reported failure may still have landed, and a delete that
    # reported success may not have: the account's own state is the answer.
    if [ "$(account_state)" = not-there ]; then return 0; fi
    if [ "$try" -lt "$ACCOUNT_DELETE_TRIES" ]; then
      sleep_bg "${CHECK195_ACCOUNT_DELETE_RETRY_SECONDS:-5}"
    fi
  done
  return 1
}

# The Restore: everything this run created, deleted in the one order that works
# — the invoker binding goes with the service and the bucket-level
# roles/storage.objectCreator binding goes with the bucket (with the object the
# step wrote in it), and the project-level binding is
# removed BEFORE the account, because a binding that outlives its member reads
# back as `deleted:serviceAccount:...` and could never be removed again. It runs
# from the exit trap too, so a Ctrl-C in the middle still leaves nothing behind.
# The bucket is deleted only when this run's own record of creating it is there
# AND the bucket's own label says it is this run's: the record is written before
# the create returns, so an interrupt in that window leaves a record beside a
# bucket of that NAME that can be somebody else's. A bucket whose label cannot be
# read is left alone and reported, never read as somebody else's.
restore_run() {
  if [ ! -f "$SERVICE_MINE" ] && [ ! -f "$ACCOUNT_MINE" ] && [ ! -f "$BUCKET_MINE" ]; then
    fact restore PASS "nothing of this check's was created, so there is nothing to delete"
    fact service-deleted PASS "this run deployed no service, so there is none to delete"
    fact account-deleted PASS "this run created no service account, so there is none to delete"
    fact binding-removed PASS "this run added no project-level binding, so there is none to remove"
    fact bucket-deleted PASS "this run created no bucket, so there is none to delete"
    return 0
  fi
  say "Restore: deleting what this check created"
  if [ -n "$BUILD_ID" ]; then
    # A build still running would call a service the next line deletes;
    # cancelling one that already ended is refused, and that is fine.
    gcloud builds cancel "$BUILD_ID" --project="${P:?}" --quiet >/dev/null 2>&1 || true
  fi
  if [ -f "$SERVICE_MINE" ]; then
    gcloud run services delete "${SVC:?}" --project="${P:?}" --region="${L:?}" --quiet >&2 || true
  fi
  local bucket_own="" bucket_deleted=no
  if [ -f "$BUCKET_MINE" ]; then
    # The label is read HERE as well as after a failed create: the record is
    # written before the create call returns, so an interrupt in that window
    # leaves a record with no bucket of this run's behind it — and a bucket of
    # that NAME can be somebody else's. Only a bucket carrying this run's own
    # label is ever deleted, whatever the record says. Deleted BEFORE the account
    # below, so the bucket's own roles/storage.objectCreator binding goes with it:
    # a binding that outlives its member reads back as deleted:serviceAccount:...
    # and could never be removed. The object the step wrote — the whole reason the
    # bucket existed — goes with the bucket: `rm --recursive` is the only one of
    # the two that can take a bucket holding an object, since the API refuses a
    # plain buckets delete on a non-empty one.
    bucket_own=$(bucket_owner_state)
    case "$bucket_own" in
    mine)
      gcloud storage rm --recursive "gs://${BUCKET:?}" --quiet >&2 || true
      bucket_deleted=yes
      ;;
    other)
      owner "not deleting gs://${BUCKET}: it does not carry this run's own label chk195=${BUCKET_NONCE}, so it is not this run's bucket"
      ;;
    *)
      owner "not deleting gs://${BUCKET}: whether it carries this run's own label chk195=${BUCKET_NONCE} could not be read"
      ;;
    esac
  fi
  # Attempted whenever this run created anything at all, and NOT only when the
  # grant call came back: a Ctrl-C during that call reaches gcloud too, so a
  # binding that landed can outlive a flag set only after it returned — and once
  # the account below is deleted, a binding naming it reads back as
  # deleted:serviceAccount:... and can never be removed again. Removing one that
  # was never added is refused, harmlessly, and the read-back below is what says
  # whether the policy still names the account.
  gcloud projects remove-iam-policy-binding "${P:?}" \
    --member="serviceAccount:${CALLER_EMAIL:?}" --role=roles/logging.logWriter --condition=None --quiet >&2 || true
  if [ -f "$ACCOUNT_MINE" ]; then
    delete_account_retrying || true
  fi
  local st ac bind bst left=""
  st=$(service_state)
  ac=$(account_state)
  bind=$(project_naming_account)
  bst=$(bucket_state)
  if [ ! -f "$SERVICE_MINE" ]; then
    fact service-deleted PASS "this run deployed no service, so there is none to delete"
  elif [ "$st" = not-there ]; then
    fact service-deleted PASS "${SVC}, and with it the roles/run.invoker binding that admitted ${CALLER_EMAIL}, is gone (read back by a listing)"
  elif [ "$st" = there ]; then
    left="$left the service ${SVC};"
    fact service-deleted FAIL "${SVC} is still in ${P}/${L}: delete it by hand with gcloud run services delete ${SVC} --project=${P} --region=${L}"
  else
    left="$left whether the service ${SVC} is gone;"
    fact service-deleted FAIL "whether ${SVC} is gone cannot be read (the list failed), so nothing is claimed about it: check it with gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC} and delete it if it is there"
  fi
  if [ ! -f "$BUCKET_MINE" ]; then
    fact bucket-deleted PASS "this run created no bucket, so there is none to delete"
  elif [ "$bucket_deleted" != yes ]; then
    if [ "$bucket_own" = other ]; then
      fact bucket-deleted PASS "gs://${BUCKET} was NOT deleted, and deliberately: it does not carry this run's own label chk195=${BUCKET_NONCE}, so it is not this run's bucket — a create that was refused because somebody else holds that name leaves this run's record behind with no bucket of its own, and this check deletes only its own. Nothing of this check's is in it"
    else
      left="$left whether the bucket gs://${BUCKET} is this run's;"
      fact bucket-deleted FAIL "whether gs://${BUCKET} carries this run's own label chk195=${BUCKET_NONCE} could not be read — the describe failed with $(last_lines "$SCRATCH/bucket-owner.err") — so it was NOT deleted and nothing is claimed about it: read the label with gcloud storage buckets describe gs://${BUCKET} --format=value(labels.chk195) and delete the bucket yourself only if it carries that label"
    fi
  elif [ "$bst" = not-there ]; then
    fact bucket-deleted PASS "gs://${BUCKET}, the line the step wrote into it and the roles/storage.objectCreator binding beside it are gone (read back by a listing)"
  elif [ "$bst" = there ]; then
    left="$left the bucket gs://${BUCKET};"
    fact bucket-deleted FAIL "gs://${BUCKET} is still in ${P}: delete it by hand with gcloud storage rm --recursive gs://${BUCKET} --project=${P} --quiet"
  else
    left="$left whether the bucket gs://${BUCKET} is gone;"
    fact bucket-deleted FAIL "whether gs://${BUCKET} is gone cannot be read (the list failed), so nothing is claimed about it: check it with gcloud storage buckets list --project=${P} --format=value(name) and delete it if it is there"
  fi
  if [ ! -f "$ACCOUNT_MINE" ]; then
    fact account-deleted PASS "this run created no service account, so there is none to delete"
  elif [ "$ac" = not-there ]; then
    fact account-deleted PASS "${CALLER_EMAIL} is gone (read back by a listing)"
  elif [ "$ac" = there ]; then
    left="$left the account ${CALLER_EMAIL};"
    fact account-deleted FAIL "${CALLER_EMAIL} is still in ${P} after ${ACCOUNT_DELETE_TRIES} deletion attempts, each preceded by a read of the project policy: $(last_lines "$SCRATCH/account-delete.err"). Delete it by hand with gcloud iam service-accounts delete ${CALLER_EMAIL} --project=${P}"
  else
    left="$left whether the account ${CALLER_EMAIL} is gone;"
    fact account-deleted FAIL "whether ${CALLER_EMAIL} is gone cannot be read (the list failed), so nothing is claimed about it: check it with gcloud iam service-accounts list --project=${P} --filter=email:${CALLER}@"
  fi
  case "$bind" in
  clean) fact binding-removed PASS "${P}'s own IAM policy names ${CALLER_EMAIL} in no binding any more (read back from the policy itself)" ;;
  named)
    left="$left the project binding naming ${CALLER_EMAIL};"
    fact binding-removed FAIL "${P}'s own IAM policy STILL names ${CALLER_EMAIL}: remove what is left by hand with gcloud projects remove-iam-policy-binding ${P} --member=serviceAccount:${CALLER_EMAIL} --role=<the role the policy shows> --condition=None — a binding that outlives the account reads back as deleted:serviceAccount:... and can no longer be removed"
    ;;
  *)
    left="$left whether the project binding naming ${CALLER_EMAIL} is gone;"
    fact binding-removed FAIL "whether ${P}'s own IAM policy still names ${CALLER_EMAIL} cannot be read (the policy read failed), so nothing is claimed about it: read it with gcloud projects get-iam-policy ${P} --format=json and remove the binding by hand if it is there"
    ;;
  esac
  if [ -z "$left" ]; then
    local gone="the service (and the invoker binding with it), the project-level roles/logging.logWriter binding, and the account"
    local kept=""
    if [ "$bucket_deleted" = yes ]; then
      gone="the service (and the invoker binding with it), the bucket (and with it the object the step wrote its one line into and the bucket-level roles/storage.objectCreator binding), the project-level roles/logging.logWriter binding, and the account"
    elif [ -f "$BUCKET_MINE" ]; then
      kept=" gs://${BUCKET} was NOT deleted and is not this check's: it does not carry this run's own label, and this check deletes only its own bucket"
    fi
    fact restore PASS "everything this check created was deleted and READ BACK: ${gone}.${kept} The only thing this run leaves in ${P} is the build's own RECORD, in Cloud Build's history: it is the project's log of what ran, not a resource this check made"
  else
    fact restore FAIL "the Restore did not finish:$left delete it by hand from one of the rows above. (The build's own record in Cloud Build's history is left on purpose: it is the project's log of what ran)"
  fi
}

cleanup() {
  restore_run
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to run the Restore,
  # which deletes a service and an account this check created in the owner's
  # project, and a second Ctrl-C during it would kill this shell with the
  # deletes half done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # The scratch directory's row is added before the verdict is taken, because
  # the verdict is taken from the facts: a directory this check could not remove
  # is a FAIL, and a block that printed `scratch-removed: FAIL` beside `END
  # (exit 0)` would read as a pass.
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  case "$facts" in
  *'|FAIL|'*)
    if [ "$status" = 0 ]; then status=1; fi
    ;;
  esac
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    say "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check195.sh -- the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
}

# guard_constants: the two names this check creates and the two it may never
# leave. They are constants rather than inputs, so the only way one of them is
# wrong is an edit that broke this file — which is worth a stop before anything
# in the project is touched.
guard_constants() {
  [ "$P" = fugaro-dev ] || stop "the project this check runs in must be fugaro-dev and it is '${P}': it is a constant in this file, so edit it back"
  case "$L" in "" | /* | */) stop "the region this check runs in must be a region name and it is '${L}'" ;; esac
  case "$SVC" in "" | /* | */) stop "the throwaway service's name must be a name and it is '${SVC}'" ;; esac
  case "$CALLER" in "" | /* | */) stop "the throwaway account's name must be a name and it is '${CALLER}'" ;; esac
  # The bucket is the one name here that is built rather than written, so it is
  # checked for the two ways a shell can leave it empty or shaped like a path: an
  # empty BUCKET would make the create, the grant and the delete all read as a
  # command with a missing argument.
  case "$BUCKET" in
  "") stop "the throwaway bucket's name came out empty: it is built from the project, the moment this run started and this shell's own process id, so this is a shell that could not run date" ;;
  /* | */) stop "the throwaway bucket's name must be a bucket name and it is '${BUCKET}'" ;;
  esac
  case "$BUCKET_NONCE" in "" | */*) stop "the throwaway bucket's label came out '${BUCKET_NONCE}': it is what says a bucket is this run's, so an empty one would make every bucket read as this run's" ;; esac
  case "$IMAGE" in
  "") stop "CHECK195_IMAGE is empty: the throwaway service has to be deployed from some container, and the check's own default is Google's Cloud Run sample" ;;
  esac
  fact image PASS "${IMAGE} (the throwaway service's container; the check builds and pushes nothing itself)"
}

# guard_deploy_is_iam_only: the deploy line is the whole of this service's
# access control, and everything below rests on the service being IAM-only — so
# the flag is read out of the argv a real run would pass rather than trusted to
# be in it. Without this, the only thing that would notice a dropped flag is the
# unauthenticated probe, which is a request to a service that is public by then.
guard_deploy_is_iam_only() {
  local f
  for f in "${DEPLOY_FLAGS[@]}"; do
    if [ "$f" = --no-allow-unauthenticated ]; then
      fact deploy-iam-only PASS "the deploy line carries --no-allow-unauthenticated: the throwaway service requires IAM and answers nobody without one"
      return 0
    fi
  done
  stop "the deploy line does not carry --no-allow-unauthenticated (DEPLOY_FLAGS is: $(printf '%s ' "${DEPLOY_FLAGS[@]}")), so the throwaway service would answer anyone who found its URL. This check refuses before anything is created: add the flag back to DEPLOY_FLAGS and run again. Nothing was created"
}

guard_project() {
  # This check is about ONE installation's project, and the project it writes
  # into is the one hard-coded above. The question is doctor's project: LINE,
  # and its exit status is not the answer to it: doctor exits 1 whenever any
  # check of its own failed (emitDoctor returns ExitUserError, internal/cli/
  # doctor.go), and billing, IAM and the update check have nothing to do with
  # which installation this is — so the status is not read (`|| true`). Its
  # output is captured rather than piped into grep for two reasons: `set -o
  # pipefail` would put doctor's own status into the pipeline and turn a
  # perfectly matched line into "this is not the fugaro-dev installation", and
  # `grep -q` closes the pipe on its match while doctor is still printing — so
  # doctor would die on the broken pipe (141) and pipefail would stop the run
  # just the same.
  local doctor_out
  doctor_out=$(fugaro doctor --project fugaro --all || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro printed no 'project: ... (GCP fugaro-dev,' line. Either this is not the fugaro-dev installation, or --project fugaro disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials). Run 'fugaro doctor --project fugaro' here yourself and read its output before going on — this check runs on the sandbox installation only, never belong"
  fi
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact fugaro-version PASS "${v:-fugaro version printed nothing}"
  fact project-region PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

# The three existence probes, each one filtered LIST: absence is an empty list,
# never an error string. A failed list is NOT the answer "it is not there":
# reading one as absent is how this check would adopt a service or an account it
# did not create — which its Restore would then DELETE — so a failed list is
# `unreadable` and stops the run.
list_state() {
  local want=$1 out rc=0
  shift
  out=$("$@" 2>/dev/null) || rc=$?
  out=$(printf '%s' "$out" | tr -d '\r' | sed -e 's/[[:space:]]*$//' | grep -v '^$' || true)
  if [ "$rc" != 0 ]; then
    printf 'unreadable'
  elif [ -z "$out" ]; then
    printf 'not-there'
  elif [ "$out" = "$want" ]; then
    printf 'there'
  else
    printf 'unreadable'
  fi
}

service_state() {
  list_state "${SVC:?}" gcloud run services list --project="${P:?}" --region="${L:?}" \
    --filter="metadata.name=${SVC}" --format='value(metadata.name)'
}

account_state() {
  list_state "${CALLER_EMAIL:?}" gcloud iam service-accounts list --project="${P:?}" \
    --filter="email:${CALLER}@" --format='value(email)'
}

# project_naming_account: whether the project's OWN IAM policy names the
# throwaway account at all — `clean`, `named`, or `unread` when the policy could
# not be read. The policy is read as a document and searched for the account's
# address: it is a whole policy, with conditions and roles this reading has no
# business interpreting, and the one question here is whether the address is in
# it. A read that failed is not the answer "it is not there".
project_naming_account() {
  local doc="$SCRATCH/project-policy.json"
  if ! gcloud projects get-iam-policy "${P:?}" --format=json >"$doc" 2>"$SCRATCH/project-policy.err"; then
    printf 'unread'
    return 0
  fi
  if grep -q -- "${CALLER_EMAIL:?}" "$doc"; then printf 'named'; else printf 'clean'; fi
}

# bucket_naming_account: the same three answers for the BUCKET's own IAM policy,
# which is where the roles/storage.objectCreator binding the step's write needs
# lives. Read as a document and searched for the address, like the project's: this
# reading has no business interpreting roles or conditions either.
bucket_naming_account() {
  local doc="$SCRATCH/bucket-policy.json"
  if ! gcloud storage buckets get-iam-policy "gs://${BUCKET:?}" --format=json >"$doc" 2>"$SCRATCH/bucket-policy.err"; then
    printf 'unread'
    return 0
  fi
  if grep -q -- "${CALLER_EMAIL:?}" "$doc"; then printf 'named'; else printf 'clean'; fi
}

# bucket_state: whether the bucket is there, read from a LIST for the reason the
# two probes above use one — absence is an empty list, and a list that FAILED is
# `unreadable` rather than the answer "it is not there". `describe` cannot make
# that reading: it fails the same way for a bucket that is gone and for a session
# that expired. Bucket resource names are printed with the scheme by one gcloud
# version and without it by another, so both forms are looked for by name.
bucket_state() {
  local out rc=0
  out=$(gcloud storage buckets list --project="${P:?}" --format='value(name)' 2>/dev/null) || rc=$?
  out=$(printf '%s' "$out" | tr -d '\r' | sed -e 's/[[:space:]]*$//' | grep -v '^$' || true)
  if [ "$rc" != 0 ]; then
    printf 'unreadable'
    return 0
  fi
  if printf '%s\n' "$out" | grep -qx -e "${BUCKET:?}" -e "gs://${BUCKET}"; then
    printf 'there'
  else
    printf 'not-there'
  fi
}

# bucket_owner_state: who the bucket this run created says it belongs to — `mine`
# for the label this run wrote at creation, `other` for a bucket of the same name
# that does not carry it, and `unreadable` for a describe that FAILED. A bucket
# name is global, so a create that reported failure can have been refused because
# SOMEBODY ELSE holds that name: the label is what keeps the Restore from deleting
# a stranger's bucket, and it is read there as well as after a failed create. The
# third state is the one the two probes above already have, and for the same
# reason: a failed call is not the answer "it is not mine". Read as `other`, it
# would leave this run's own bucket behind and report it as somebody else's — the
# mirror image of deleting a stranger's.
bucket_owner_state() {
  local out rc=0
  out=$(gcloud storage buckets describe "gs://${BUCKET:?}" --format='value(labels.chk195)' 2>"$SCRATCH/bucket-owner.err") || rc=$?
  if [ "$rc" != 0 ]; then
    printf 'unreadable'
    return 0
  fi
  out=$(printf '%s' "$out" | tr -d '\r' | grep -v '^$' | head -n 1 || true)
  if [ "$out" = "$BUCKET_NONCE" ]; then printf 'mine'; else printf 'other'; fi
}

# step_line_bucket: the throwaway bucket the step writes its one line into, created
# before anything else this check makes. The object in it is the reading that needs
# no Cloud Logging at all, and the bucket is this run's by its LABEL rather than by
# its name: the name carries this run's own moment and process id, and a create
# that reported failure may have been refused because that name is already taken —
# which is not this run's bucket to delete.
step_line_bucket() {
  : >"$BUCKET_MINE"
  local create_err=$SCRATCH/bucket-create.err rc=0
  say "creating the throwaway bucket gs://${BUCKET} for the step's own line"
  gcloud storage buckets create "gs://${BUCKET:?}" --project="${P:?}" --location="${L:?}" \
    --uniform-bucket-level-access --labels="chk195=${BUCKET_NONCE}" --quiet >&2 2>"$create_err" || rc=$?
  if [ "$rc" != 0 ]; then
    case "$(bucket_owner_state)" in
    mine)
      stop "the bucket create reported failure, but gs://${BUCKET} IS there with this run's own label ${BUCKET_NONCE}, so it is this check's: the record says so and this run's Restore deletes it. Nothing else ran. What gcloud said: $(last_lines "$create_err")"
      ;;
    unreadable)
      stop "the bucket create reported failure and whether gs://${BUCKET} is this run's cannot be read (the label describe failed) — and a create that reported failure may still have created it, so the record of it is KEPT: this run's Restore reads the label again and deletes only a bucket that carries chk195=${BUCKET_NONCE}. Check it by hand with 'gcloud storage buckets describe gs://${BUCKET} --format=value(labels.chk195)' and delete it yourself only if it carries that label. What gcloud said: $(last_lines "$create_err")"
      ;;
    *)
      # Either no bucket of that name at all — the ordinary refusal — or one that
      # does not carry this run's label, which is somebody else's and is never
      # touched. The record goes either way: this check deletes only its own.
      rm -f "$BUCKET_MINE" || true
      stop "the bucket gs://${BUCKET} could not be created (${rc}): $(last_lines "$create_err"). YOUR account needs storage.buckets.create and, on this project, storage.buckets.list (a project Owner has them). Nothing of this check's is there, so its record is removed again, and the service is deleted by this run's Restore"
      ;;
    esac
  fi
  fact line-bucket PASS "gs://${BUCKET} is this run's — created here, labelled chk195=${BUCKET_NONCE}, private (--uniform-bucket-level-access) — and it exists for one reason: the build's step writes its one line into ${LINE_URI}, so the reading does not go through Cloud Logging at all. It is deleted, with the object in it, by this run's Restore"
}

guard_resources() {
  local st ac
  st=$(service_state)
  if [ "$st" = unreadable ]; then
    stop "whether ${SVC} is already in ${P}/${L} cannot be read: the list failed (an expired session, a transient API error, or a permission this account does not hold), and a failed call is not the answer 'it is not there'. This guard is what stops this check adopting a service it did not create — one its Restore would then DELETE — so the run stops here rather than guessing. Run 'gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$st" = there ]; then
    [ -f "$SERVICE_MINE" ] \
      || stop "${SVC} already exists in ${P}/${L}, and it is not this check's. This check deletes what it creates and never adopts a service it did not create; look at it first (gcloud run services describe ${SVC} --project=${P} --region=${L}), delete it yourself if you know it is a leftover of an earlier attempt, then run again. Nothing was created"
  fi
  ac=$(account_state)
  if [ "$ac" = unreadable ]; then
    stop "whether a service account named ${CALLER_EMAIL} is already in ${P} cannot be read: the list failed, and a failed call is not the answer 'it is not there'. Run 'gcloud iam service-accounts list --project=${P} --filter=email:${CALLER}@' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$ac" = there ]; then
    [ -f "$ACCOUNT_MINE" ] \
      || stop "a service account named ${CALLER_EMAIL} is already in ${P}, and it is not this check's: this check never adopts, grants on or deletes an account it did not create. Look at it first (gcloud iam service-accounts describe ${CALLER_EMAIL} --project=${P}) and delete it yourself if you know it is a leftover of an earlier attempt, then run again. Nothing was created"
  fi
  # The third leftover this check must not walk past, and the only one the two
  # lists cannot see: a binding on the PROJECT that names the account's address
  # while no account of that name exists — a leftover of an earlier attempt
  # whose removal failed. This run adds a binding at that same address and then
  # reads the project policy back to prove it removed it again; a leftover makes
  # that read-back say the binding is still there, and once this run's own
  # account is deleted the leftover can never be removed at all.
  local pol
  pol=$(project_naming_account)
  case "$pol" in
  named)
    stop "${P}'s own IAM policy already names ${CALLER_EMAIL}, though no account of that name is in ${P}: a binding left at this address by an earlier attempt would make this run's read-back of the project policy report its own removal as incomplete, and once this run deletes its account that leftover can never be removed again. Remove it by hand — gcloud projects remove-iam-policy-binding ${P} --member=serviceAccount:${CALLER_EMAIL} --role=<the role the policy shows> --condition=None — then run this check again. Nothing was created"
    ;;
  unread)
    stop "whether ${P}'s own IAM policy already names ${CALLER_EMAIL} cannot be read: the policy read failed, and a failed call is not the answer 'it is not there'. Read it with gcloud projects get-iam-policy ${P} --format=json and look for the address, then run this check again. Nothing was created"
    ;;
  esac
  fact baseline PASS "neither the service ${SVC} nor the account ${CALLER_EMAIL} was in ${P} when this check started, and its own project policy was read for the account's name"
}

# ------------------------------------------------------------------- the steps
# field_of TEXT NAME: the value of NAME= in TEXT, or nothing. The text is one
# line of what gcloud's value printer wrote, and that printer separates the
# columns it was asked for with a TAB — and writes one even for a column the
# entry has nothing in. A step's own `echo` is a textPayload and never a
# jsonPayload.message, so the build's line reaches this with a trailing tab and
# the value ends at the first blank of either kind: a separator left inside it
# made the email claim compare unequal to the account's own address, which reads
# as a token minted for another identity — a FALSE FAIL on the reading S4 stops
# on.
field_of() {
  printf '%s' "$1" | tr ' \t' '\n\n' | sed -n "s/^$2=//p" | head -n 1 | sed -e 's/[[:space:]]*$//'
}

# names_the_caller RESOURCE: whether a build's own serviceAccount field names the
# throwaway account. Cloud Build reports that field as a RESOURCE NAME —
# "projects/<project>/serviceAccounts/<account>" — rather than the bare address
# the build was submitted with, and <account> is the address where the API kept
# it and the account's numeric uniqueId where it resolved it. Comparing the raw
# field with the address would report a build that ran as this check's own
# account as another identity: a false FAIL on the reading S4's build exception
# rests on. So the field is reduced to its last segment, and a numeric one is
# read against the account's own uniqueId — which the API will give only for an
# account that exists, so a build that ran as somebody else still fails.
names_the_caller() {
  local v uid
  v=$(printf '%s' "$1" | tr -d '\r' | sed -e 's/[[:space:]]*$//')
  v=${v##*/}
  if [ "$v" = "${CALLER_EMAIL:?}" ]; then return 0; fi
  case "$v" in
  "" | *[!0-9]*) return 1 ;;
  esac
  uid=$(gcloud iam service-accounts describe "${CALLER_EMAIL:?}" --project="${P:?}" \
    --format='value(uniqueId)' 2>/dev/null | tr -d '\r' | grep -v '^$' | head -n 1 || true)
  if [ -n "$uid" ] && [ "$v" = "$uid" ]; then return 0; fi
  return 1
}

# sleep_bg: an interruptible wait, so a Ctrl-C during it ends the run at once
# rather than after the sleep.
sleep_bg() {
  sleep "$1" &
  WAIT_PID=$!
  wait "$WAIT_PID" || true
  WAIT_PID=""
}

# conditions_of KIND NAME: a Cloud Run service's status conditions (type,
# status, reason, message), on one line, redacted. A deploy that fails after the
# service exists says why only here: gcloud's own error is often just "revision
# is not ready".
conditions_of() {
  gcloud run "$1" describe "$2" --project="${P:?}" --region="${L:?}" --format='yaml(status.conditions)' 2>/dev/null \
    | grep -v -e 'lastTransitionTime:' -e '^status:' -e '^ *conditions:' \
    | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-600 || true
}

step_service() {
  # The record says the service is this check's BEFORE the deploy starts: a
  # deploy that is interrupted is the ordinary way to be left with a service
  # that runs, and the record is what makes the Restore delete it. A deploy that
  # FAILED takes the record back only when the service really is not there.
  : >"$SERVICE_MINE"
  local deploy_rc=0
  say "deploying ${SVC} from ${IMAGE} with IAM required (--no-allow-unauthenticated)"
  gcloud run deploy "${SVC:?}" "${DEPLOY_FLAGS[@]}" --quiet >&2 2>"$SCRATCH/deploy.err" || deploy_rc=$?
  cat "$SCRATCH/deploy.err" >&2 || true
  if [ "$deploy_rc" != 0 ]; then
    local deployed
    deployed=$(service_state)
    if [ "$deployed" = there ]; then
      fact service FAIL "gcloud run deploy exited ${deploy_rc} after creating ${SVC}: $(last_lines "$SCRATCH/deploy.err"). Service conditions: $(conditions_of services "${SVC}") . Likely causes, most likely first: the container did not start, or the image could not be pulled"
      stop "the deploy reported failure, but ${SVC} IS there now, so it is this check's: the record says so and this run's Restore deletes it. The block's service row carries what gcloud and the service's conditions said"
    fi
    if [ "$deployed" = unreadable ]; then
      stop "the deploy reported failure and whether ${SVC} is there now cannot be read (the list failed), so the record of it is KEPT: a deploy that reported failure may still have created a service, and the record is what makes this run's Restore delete it. Check 'gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC}' by hand; if the service is there, delete it yourself. Nothing else ran"
    fi
    rm -f "$SERVICE_MINE" || true
    fact service FAIL "gcloud run deploy exited ${deploy_rc} and ${SVC} is not there: $(last_lines "$SCRATCH/deploy.err"). YOUR account needs run.services.create and iam.serviceAccounts.actAs on the runtime account, which a project Owner has"
    stop "the deploy failed and ${SVC} is not there, so nothing of this check's is running: the record is removed again. The block's service row carries what gcloud said"
  fi
  local url_err=$SCRATCH/url.err
  URL=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" --format='value(status.url)' 2>"$url_err") \
    || stop "the deployed service's URL cannot be read: 'gcloud run services describe ${SVC} --format=value(status.url)' failed with $(last_lines "$url_err"). The service IS deployed and IS this check's — the record says so, and this run's Restore deletes it. Nothing else ran"
  [ -n "$URL" ] || stop "the service has no URL, so there is nothing for the build's step to call"
  HOST=${URL#https://}
  HOST=${HOST%%/*}
  fact service PASS "${SVC} deployed in ${L} from ${IMAGE}, with --no-allow-unauthenticated: URL https://${HOST} (it is this check's and its Restore deletes it)"

  # The IAM assertion the plan names, read BEFORE the grant and before anything
  # is called: the service's own policy must name no public member. A service
  # that answered unauthenticated calls would be called by the build's step with
  # a token that proves nothing about the grant, so this is a stop.
  local pol_err=$SCRATCH/iam.err
  local pol
  pol=$(gcloud run services get-iam-policy "${SVC:?}" --project="${P:?}" --region="${L:?}" --format=json 2>"$pol_err") \
    || stop "the deployed service's IAM policy cannot be read: 'gcloud run services get-iam-policy ${SVC} --format=json' failed with $(last_lines "$pol_err"). Nothing has been called, and the service IS this check's — this run's Restore deletes it"
  case "$pol" in
  *allUsers* | *allAuthenticatedUsers*)
    stop "the deployed service's IAM policy names a public member (allUsers or allAuthenticatedUsers), so the service answers anyone and a 200 from it would prove nothing about the grant this check is about. Nothing was called. The service is deleted by this run's Restore; report this, and check it by hand with gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L}"
    ;;
  esac
  fact iam-policy PASS "get-iam-policy on ${SVC} names no allUsers and no allAuthenticatedUsers member, read BEFORE the grant and before anything was called: the service requires IAM"

  # The second half of the same assertion, and the register's own wording: an
  # unauthenticated call must be refused. A project-level allUsers or
  # allAuthenticatedUsers binding does NOT appear in the service's own
  # get-iam-policy, so this one request is the only thing that sees it — and it
  # is made BEFORE the throwaway account's token is ever sent, so nothing this
  # check exists for rests on a service that was public all along.
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time "$CURL_SECONDS" "${URL}" 2>/dev/null || true)
  case "$code" in
  401 | 403) fact unauthenticated-request PASS "an unauthenticated GET on the service's own URL was refused with ${code}" ;;
  *)
    fact unauthenticated-request FAIL "an unauthenticated GET on the service's own URL answered ${code:-nothing}, so the service does not refuse unauthenticated calls"
    stop "an unauthenticated GET on ${URL} answered ${code:-nothing}: the service answers unauthenticated calls, so a 200 in the build's step would prove nothing about the grant. The service's own get-iam-policy names no public member, so this is a project-level binding (allUsers or allAuthenticatedUsers with roles/run.invoker) — the one thing only this request sees. The run stops here, before the account is created and before any token is sent: the service is deleted by this run's Restore, and the project's IAM is yours to fix"
    ;;
  esac
}

step_caller() {
  # The throwaway build account, and the ONE grant this check is about. It is
  # created here rather than borrowed from the installation: the reading is that
  # a build account's admission comes from a roles/run.invoker binding on the
  # service alone, and an account the installation already has would come with
  # grants this check does not control. Record-before-create, like the service.
  : >"$ACCOUNT_MINE"
  if ! gcloud iam service-accounts create "${CALLER:?}" --project="${P:?}" \
    --display-name='Check 195 throwaway build account' --quiet >&2 2>"$SCRATCH/sa.err"; then
    local made
    made=$(account_state)
    if [ "$made" = there ]; then
      stop "the account create reported failure, but ${CALLER_EMAIL} IS there now, so it is this check's: the record says so and this run's Restore deletes it. Nothing else ran"
    fi
    if [ "$made" = unreadable ]; then
      stop "the account create reported failure and whether ${CALLER_EMAIL} is there now cannot be read (the list failed), so the record of it is KEPT. Check 'gcloud iam service-accounts list --project=${P} --filter=email:${CALLER}@' by hand; if the account is there, delete it yourself. Nothing else ran"
    fi
    rm -f "$ACCOUNT_MINE" || true
    stop "the service account ${CALLER} could not be created in ${P} ($(last_lines "$SCRATCH/sa.err")): YOUR account needs iam.serviceAccounts.create, which a project Owner has. The account is not there, so the record of it is removed again, and the service is deleted by this run's Restore"
  fi
  say "granting ${CALLER_EMAIL} roles/run.invoker on ${SVC} alone"

  # The grant this check exists for: on the SERVICE, never on the project, and
  # read back from the service's own policy just below.
  # A service account that was just created is not yet visible to every IAM
  # front end: the grant answers "does not exist" for a few seconds. That answer
  # is waited out and retried; any other refusal (a denied setIamPolicy) is not.
  local grant_try=0 grant_ok=no
  while [ "$grant_try" -lt 8 ]; do
    grant_try=$((grant_try + 1))
    if gcloud run services add-iam-policy-binding "${SVC:?}" --project="${P:?}" --region="${L:?}" \
      --member="serviceAccount:${CALLER_EMAIL}" --role=roles/run.invoker --quiet >&2 2>"$SCRATCH/invoker.err"; then
      grant_ok=yes
      break
    fi
    grep -q 'does not exist' "$SCRATCH/invoker.err" || break
    sleep_bg "${CHECK195_GRANT_RETRY_SECONDS:-5}"
  done
  if [ "$grant_ok" != yes ]; then
    fact invoker-grant FAIL "${CALLER_EMAIL} could not be granted roles/run.invoker on ${SVC}: $(last_lines "$SCRATCH/invoker.err")"
    stop "${CALLER_EMAIL} could not be granted roles/run.invoker on this check's own service ${SVC}, so the build's step would be refused at the front end and nothing about the grant could be measured. YOUR account needs run.services.setIamPolicy on it (an Owner has it). Nothing was built; the account and the service are deleted by this run's Restore"
  fi
  # The project-level binding Cloud Build itself requires of a build's own
  # service account: with a service account named, a build must say where its
  # logs go, and CLOUD_LOGGING_ONLY writes them with THAT account's credentials.
  # It is what lets this check read the build's own line — the token's email
  # claim — out of Cloud Logging, and the Restore removes it again before it
  # deletes the account.
  if ! gcloud projects add-iam-policy-binding "${P:?}" \
    --member="serviceAccount:${CALLER_EMAIL}" --role=roles/logging.logWriter --condition=None --quiet >&2 2>"$SCRATCH/logwriter.err"; then
    fact invoker-grant FAIL "${CALLER_EMAIL} could not be granted roles/logging.logWriter on ${P}: $(last_lines "$SCRATCH/logwriter.err")"
    stop "${CALLER_EMAIL} could not be granted roles/logging.logWriter on ${P}, which a build that names its own service account needs to write its log: without it the build's own line — the token's email claim — is never written anywhere this check can read. YOUR account needs resourcemanager.projects.setIamPolicy (an Owner has it). Nothing was built; the account and the service are deleted by this run's Restore"
  fi

  # The bucket-level binding the step's own WRITE needs, and the one the owner's
  # brief asks for by name: roles/storage.objectCreator ON THIS BUCKET ALONE, so
  # the throwaway account can put one object in it and cannot read it, list the
  # bucket or write anywhere else. The step's upload is the raw JSON API
  # (uploadType=media), which asks for storage.objects.create and nothing more:
  # `gcloud storage cp` would also need storage.buckets.get, which this role does
  # not carry, so the step does not use it.
  if ! gcloud storage buckets add-iam-policy-binding "gs://${BUCKET:?}" \
    --member="serviceAccount:${CALLER_EMAIL}" --role=roles/storage.objectCreator --quiet >&2 2>"$SCRATCH/objectcreator.err"; then
    fact invoker-grant FAIL "${CALLER_EMAIL} could not be granted roles/storage.objectCreator on gs://${BUCKET}: $(last_lines "$SCRATCH/objectcreator.err")"
    stop "${CALLER_EMAIL} could not be granted roles/storage.objectCreator on gs://${BUCKET}, so the step could not write its one line there and the reading would fall back on the step's exit code alone. YOUR account needs storage.buckets.setIamPolicy on that bucket (an Owner has it). Nothing was built; the bucket, the account and the service are deleted by this run's Restore"
  fi

  # The grant, read back from the service's own IAM policy: exactly one member,
  # the throwaway account, with the invoker role — which is what "on it alone"
  # means. This is read BEFORE the build, so a binding that did not land stops
  # the run rather than turning into a 403 the block would report as a refusal.
  local pol_err=$SCRATCH/invoker-policy.err pol
  pol=$(gcloud run services get-iam-policy "${SVC:?}" --project="${P:?}" --region="${L:?}" --format=json 2>"$pol_err") \
    || stop "the service's own IAM policy cannot be read back after the grant: $(last_lines "$pol_err"). Nothing was built; the account and the service are deleted by this run's Restore"
  case "$pol" in
  *allUsers* | *allAuthenticatedUsers*)
    stop "the service's own IAM policy names a public member after the grant, which it did not before: something else wrote to ${SVC}'s policy while this check ran. Nothing was built. Report this"
    ;;
  esac
  local members
  members=$(printf '%s' "$pol" | grep -o '"serviceAccount:[^"]*"' | tr -d '"' | sort -u | tr '\n' ' ' || true)
  case "$members" in
  *"serviceAccount:${CALLER_EMAIL}"*)
    fact invoker-grant PASS "the service's own IAM policy names ${CALLER_EMAIL} with roles/run.invoker, and the only member it names is ${members% } — one grant, on this service alone: ${CALLER_EMAIL} may invoke ${SVC} and nothing else. It also holds roles/logging.logWriter on ${P} (which Cloud Build requires of a build's own service account to write its log) and roles/storage.objectCreator on gs://${BUCKET} alone (which is what lets the step write its one line, and nothing else, there); the Restore removes each of those and deletes the account"
    ;;
  *)
    fact invoker-grant FAIL "the service's own IAM policy does not name ${CALLER_EMAIL} after the binding was added (it names: ${members:-nobody}), so the build's step would be refused whatever the grant call said"
    stop "the invoker grant is not in ${SVC}'s own IAM policy after it was added, so nothing the build's step did could be read as a statement about it. Nothing was built; the account and the service are deleted by this run's Restore"
    ;;
  esac

  # IAM is eventually consistent: a call that lands in the window between the
  # binding and its propagation is refused exactly like a missing grant.
  if [ "$GRANT_SECONDS" -gt 0 ]; then
    owner "waiting ${GRANT_SECONDS} s for the grant to take effect"
    sleep_bg "$GRANT_SECONDS"
  fi
  wait_for_write_grants
}

# wait_for_write_grants: the two bindings the build's own WRITES need, and neither
# of them is what this check measures. A build that names its own service account
# writes its log with that account's credentials, which is the project-level
# roles/logging.logWriter binding just added; the step writes its one line into the
# bucket's object with the same account's token, which is the bucket-level
# roles/storage.objectCreator binding just added. IAM is eventually consistent and
# a build that starts inside the window writes neither: that is how the owner's run
# of 2026-11-24 came back with a build record that said FAILURE and a `gcloud
# builds log` that printed an empty body, on a binding granted seconds earlier. So
# each binding is read back out of the policy that holds it — the project's own, and
# the bucket's own — re-read while it is not there, and then given ONE short fixed
# settle before the build starts. The wait is BOUNDED (CHECK195_GRANTS_WAIT_SECONDS,
# and CHECK195_GRANTS_SETTLE_SECONDS for the settle) and the row says how long each
# was waited for and what each read back. Nothing rests on it: a build whose writes
# did not land is read from its own step's exit code, so a wait that runs out costs
# this run the email claim's text, never the reading.
wait_for_write_grants() {
  local waited=0 pol="" bpol="" pol_at="" bpol_at=""
  while :; do
    pol=$(project_naming_account)
    bpol=$(bucket_naming_account)
    # When each policy was READ, whether it named the account or not: the row says
    # how long the read-back took for the naming case and for the case where the
    # binding never landed, and neither may be quoted as a time that was never
    # waited.
    if [ "$pol" != unread ] && [ -z "$pol_at" ]; then pol_at=$waited; fi
    if [ "$bpol" != unread ] && [ -z "$bpol_at" ]; then bpol_at=$waited; fi
    if [ "$pol" = named ] && [ "$bpol" = named ]; then break; fi
    if [ "$waited" -ge "$GRANTS_WAIT_SECONDS" ]; then break; fi
    sleep_bg "$GRANTS_POLL_SECONDS"
    waited=$((waited + GRANTS_POLL_SECONDS))
  done
  # What the row says about the settle, per reading: only the two policies READ and
  # both naming the account can be settled. A policy that could not be read says
  # nothing either way, and the row for it may not claim the binding is absent.
  local settle="no settle, because whether both policies name the account was not read"
  if [ "$pol" = named ] && [ "$bpol" = named ]; then
    settle="${GRANTS_SETTLE_SECONDS} s of settle"
    if [ "$GRANTS_SETTLE_SECONDS" -gt 0 ]; then
      owner "waiting ${GRANTS_SETTLE_SECONDS} s for the roles/logging.logWriter and roles/storage.objectCreator grants to propagate"
      sleep_bg "$GRANTS_SETTLE_SECONDS"
    fi
  elif [ "$pol" = clean ] || [ "$bpol" = clean ]; then
    settle="no settle, because a binding that never landed is not in its policy to settle"
  fi
  # Each side read on its own, so a row names only what it read: a policy that
  # names the account in no binding is a binding that did not land, and a policy
  # that could not be read says nothing either way.
  local log_said bucket_said bad=""
  case "$pol" in
  named) log_said="${P}'s own IAM policy named ${CALLER_EMAIL} ${pol_at} s after the roles/logging.logWriter binding was added (read back from the policy, not assumed)" ;;
  clean)
    log_said="${P}'s own IAM policy names ${CALLER_EMAIL} in no binding ${pol_at} s after the roles/logging.logWriter binding was reported added"
    bad="the build may write no log"
    ;;
  *)
    log_said="whether ${P}'s own IAM policy names ${CALLER_EMAIL} could not be read"
    bad="nothing is claimed about the build's log"
    ;;
  esac
  case "$bpol" in
  named) bucket_said="the bucket's own policy named ${CALLER_EMAIL} ${bpol_at} s after the roles/storage.objectCreator binding was added (read back from that policy too)" ;;
  clean)
    bucket_said="the bucket's own policy names ${CALLER_EMAIL} in no binding ${bpol_at} s after the roles/storage.objectCreator binding was reported added"
    bad="${bad:+${bad}; }the step may write no line into ${LINE_URI}"
    ;;
  *)
    bucket_said="could not be read"
    bad="${bad:+${bad}; }nothing is claimed about the step's write into ${LINE_URI}"
    ;;
  esac
  local readback="${log_said}, and ${bucket_said}"
  local bounded="the wait is bounded at ${GRANTS_WAIT_SECONDS} s by CHECK195_GRANTS_WAIT_SECONDS, because IAM's propagation is not something this check can wait out indefinitely"
  if [ -z "$bad" ]; then
    fact write-grants PASS "${readback}. The build was then held back a fixed ${settle}, and ${bounded}. Nothing rests on either write: the object the step writes carries the reading as text, and the build's own record carries it as its step's exit code"
  else
    fact write-grants FAIL "${readback}: ${bad}. ${settle}, and ${bounded}. The build runs either way and its step's exit code still carries the reading; read the two policies by hand with gcloud projects get-iam-policy ${P} --format=json and gcloud storage buckets get-iam-policy gs://${BUCKET} --format=json"
  fi
}

# write_build_config: the one step — mint an ID token for this service's URL out
# of the build's own metadata server, call the service with it, print the
# token's email claim and the answer's status, write that same ONE line into the
# object this check made for it, and end with ONE exit code per outcome. The
# line and the exit code are the two readings that do not need Cloud Logging:
# the object is read back with `gcloud storage cat` (put_line below), and the
# exit code is written by Cloud Build into the build the step ran in, where
# `gcloud builds describe` hands it back. The codes, and the row each one
# becomes:
#
#   0   the service answered 200 AND the token's email claim is the throwaway
#       account — the reading S4's build exception rests on. This is the one code
#       Cloud Build does NOT write into the record: the field is a proto3 int64
#       with no presence, so a zero is omitted, and the check reads this code from
#       the build's own SUCCESS instead (see step_build)
#   11  the service answered 401 (the front end refusing the token itself)
#   12  the service answered 403 (Cloud Run refusing this caller)
#   13  any other status, including no status at all (curl could not reach it)
#   14  the metadata server minted no token, so the service was never called
#   15  the service answered 200 but the token's email claim is another identity
#
# The step's script runs without `set -e` on purpose: every way out of it has to
# be one of those codes, and a command that failed under `set -e` would end the
# step with that command's own status, which means nothing here. The service's
# URL is baked in as the token's AUDIENCE and as the URL that is called: Cloud
# Run checks the audience against the service's own URL, so the two have to be
# the same string, and they are the one the check read off the deployed service.
#
# `$$` is Cloud Build's escape for a literal `$`: the step's script is a shell
# script inside a YAML block scalar, and without the escape Cloud Build would
# substitute its own variables into it. The URL, the account, the bucket and the
# object go in through placeholders rather than through the shell, so no `$` of
# the check's own ever reaches this file. options.logging is CLOUD_LOGGING_ONLY
# — the mode Fugaro's own image builds use (internal/backend/gcp/build.go,
# images/derived/
# cloudbuild.yaml) — because the build names its own service account and the API
# refuses a build that does not say where its logs go.
write_build_config() {
  cat >"$SCRATCH/cloudbuild.tmpl" <<'CONFIG_EOF'
steps:
  - name: gcr.io/google.com/cloudsdktool/cloud-sdk:slim
    entrypoint: bash
    args:
      - -c
      - |
        set -uo pipefail
        url="__AUDIENCE__"
        meta="http://metadata.google.internal/computeMetadata/v1/instance/service-accounts"
        upload="https://storage.googleapis.com/upload/storage/v1/b/__BUCKET__/o?uploadType=media&name=__OBJECT__"
        atok=""
        # put_line: the ONE line this step measured, into the object the check
        # created for it — the reading that does not go through Cloud Logging at
        # all. The upload is the JSON API's own media upload with the metadata
        # server's access token, because the grant this check makes on the bucket
        # is roles/storage.objectCreator alone: `gcloud storage cp` would also
        # need storage.buckets.get, which that role does not carry. The token
        # lives in this one variable for this one command and is never echoed;
        # the upload prints nothing (-o /dev/null). IAM is eventually consistent
        # and this step may start inside the window of the grant that lets it
        # write, so the write is retried; a line that could not be written leaves
        # the step's own exit code as the reading, which is why every attempt
        # failing does not change the code this step ends with.
        put_line() {
          if [ -z "$${atok}" ]; then
            atok="$$(curl -f -sS -H 'Metadata-Flavor: Google' "$${meta}/default/token" 2>/dev/null \
              | sed -n 's/.*"access_token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
          fi
          [ -n "$${atok}" ] || return 1
          try=0
          while [ "$${try}" -lt "__LINE_TRIES__" ]; do
            try=$$((try + 1))
            if curl -f -sS -o /dev/null -X POST -H "Authorization: Bearer $${atok}" \
              -H 'Content-Type: text/plain' --data-binary "$$1" "$${upload}" 2>/dev/null; then
              return 0
            fi
            sleep "__LINE_RETRY_SECONDS__"
          done
          return 1
        }
        tok="$$(curl -f -sS -H 'Metadata-Flavor: Google' "$${meta}/default/identity?audience=__AUDIENCE__&format=full" 2>/dev/null || true)"
        if [ -z "$${tok}" ]; then
          tok="$$(curl -f -sS -H 'Metadata-Flavor: Google' "$${meta}/__ACCOUNT__/identity?audience=__AUDIENCE__&format=full" 2>/dev/null || true)"
        fi
        if [ -z "$${tok}" ]; then
          line="CHK195 status=notoken email=unread"
          echo "$${line}"
          put_line "$${line}" || echo "CHK195-OBJECT-NOT-WRITTEN __BUCKET__/__OBJECT__"
          exit 14
        fi
        code="$$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $${tok}" "$${url}" 2>/dev/null || true)"
        email="$$(printf '%s' "$${tok}" | python3 -c 'import base64,json,sys; p=sys.stdin.read().strip().split(".")[1]; p+="="*(-len(p)%4); print(json.loads(base64.urlsafe_b64decode(p)).get("email",""))' 2>/dev/null || true)"
        line="CHK195 status=$${code:-none} email=$${email:-unread}"
        echo "$${line}"
        put_line "$${line}" || echo "CHK195-OBJECT-NOT-WRITTEN __BUCKET__/__OBJECT__"
        case "$${code}" in
        401) exit 11 ;;
        403) exit 12 ;;
        200) ;;
        *) exit 13 ;;
        esac
        [ "$${email}" = "__ACCOUNT__" ] || exit 15
        exit 0
options:
  logging: CLOUD_LOGGING_ONLY
CONFIG_EOF
  sed -e "s|__AUDIENCE__|${URL}|" -e "s|__ACCOUNT__|${CALLER_EMAIL}|" \
    -e "s|__BUCKET__|${BUCKET}|" -e "s|__OBJECT__|${LINE_OBJECT}|" \
    -e "s|__LINE_TRIES__|${LINE_TRIES}|" -e "s|__LINE_RETRY_SECONDS__|${LINE_RETRY_SECONDS}|" \
    "$SCRATCH/cloudbuild.tmpl" >"$SCRATCH/cloudbuild.yaml" \
    || stop "the build config could not be written into the scratch directory"
}

# step_line_in FILE: the step's own line, out of a file that may hold other lines
# around it — the build's own log holds the step's whole output, and one of those
# lines says the object could not be written. A line without `status=` is not the
# measurement: the step prints exactly one line that has it, and it is the one
# this reading is.
step_line_in() {
  sed -n 's/.*CHK195 //p' "$1" 2>/dev/null | grep -m1 'status=' || true
}

# build_log: the build's own lines, retried while Cloud Logging catches up —
# ingestion lags the request by seconds, and a reading taken immediately is an
# empty answer about a build that did report. Reads BOTH payload shapes a Cloud
# Build log entry can have (a step's line is a textPayload, and a build that
# logged JSON puts it in jsonPayload.message), and returns 0 as soon as one line
# carries the step's own marker.
build_log() { # build_log ID
  local id=$1 waited=0
  local filter='resource.type="build" resource.labels.build_id="'"$id"'"'
  while :; do
    gcloud logging read "$filter" --project="${P:?}" --freshness="$LOG_AGE" \
      --limit="$LOG_LIMIT" --format='value(textPayload,jsonPayload.message)' >"$SCRATCH/build-log.txt" 2>"$SCRATCH/build-log.err" || true
    if grep -q 'CHK195 status=' "$SCRATCH/build-log.txt" 2>/dev/null; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT_SECONDS" ]; then return 1; fi
    sleep "$READ_POLL_SECONDS"
    waited=$((waited + READ_POLL_SECONDS))
  done
}

# line_object: the step's own one line, read back OUT of the object it wrote it
# into — `gcloud storage cat gs://<bucket>/chk195-line.txt`, the one command this
# reading is, and the one a person runs by hand when it comes back empty. This is
# the reading the owner's brief asks for: Cloud Storage hands an object back the
# moment it is written (reads are strongly consistent, unlike Cloud Logging's
# ingestion), so nothing here waits on a log pipeline, and the object is in a
# bucket this check created and deletes. Retried only for a transient read: the
# write itself is retried inside the build (LINE_TRIES).
line_object() {
  local waited=0
  while :; do
    if gcloud storage cat "${LINE_URI:?}" >"$SCRATCH/line-object.txt" 2>"$SCRATCH/line-object.err" \
      && grep -q 'CHK195 status=' "$SCRATCH/line-object.txt" 2>/dev/null; then
      return 0
    fi
    if [ "$waited" -ge "$LINE_WAIT_SECONDS" ]; then return 1; fi
    sleep "$READ_POLL_SECONDS"
    waited=$((waited + READ_POLL_SECONDS))
  done
}

# step_build: the throwaway Cloud Build, running as the throwaway account. It
# has no source at all (--no-source): the step is a config file's own script, so
# nothing of the installation's is staged and no bucket is created — the
# installation's gs://<project>_cloudbuild is not touched.
step_build() {
  write_build_config || return 0
  say "submitting the throwaway build as ${CALLER_EMAIL} (--no-source: nothing is staged anywhere)"
  local build_rc=0
  BUILD_ID=$(gcloud builds submit --project="${P:?}" --no-source --config="$SCRATCH/cloudbuild.yaml" \
    --service-account="projects/${P}/serviceAccounts/${CALLER_EMAIL}" --async --format='value(id)' \
    2>"$SCRATCH/build.err") || build_rc=$?
  BUILD_ID=$(printf '%s' "$BUILD_ID" | tr -d '\r' | grep -v '^$' | tail -n 1 || true)
  if [ "$build_rc" != 0 ] || [ -z "$BUILD_ID" ]; then
    fact build FAIL "gcloud builds submit (as ${CALLER_EMAIL}) exited ${build_rc} and started no build: $(last_lines "$SCRATCH/build.err"). Likely causes, most likely first: YOUR account lacks roles/iam.serviceAccountUser on ${CALLER_EMAIL} (iam.serviceAccounts.actAs); the Cloud Build API is not enabled in ${P}; or this gcloud has no --no-source flag, which is how the build is submitted without a source directory. Nothing was built, and the account and the service are deleted by this run's Restore"
    stop "the build was not started (exit ${build_rc}), so the call this check exists for was never made: the block's build row carries what gcloud said"
  fi

  local status="" detail="" account="" waited=0 unread=0 line
  while :; do
    line=$(gcloud builds describe "$BUILD_ID" --project="${P:?}" --format='value(status,failureInfo.detail)' 2>/dev/null || true)
    status=$(printf '%s' "$line" | tr -d '\r' | head -n 1 | awk -F'\t' '{print $1}')
    detail=$(printf '%s' "$line" | tr -d '\r' | head -n 1 | awk -F'\t' '{print $2}' | redact | cut -c1-300)
    # A status that could not be read at all is not the status QUEUED: three in
    # a row means the reading is broken (the API refused it, or the session went
    # away) rather than the build being slow, and waiting half an hour for it
    # would report a timeout where the truth is "cannot read".
    if [ -z "$status" ]; then
      unread=$((unread + 1))
      if [ "$unread" -ge 3 ]; then
        status="unreadable"
        break
      fi
    else
      unread=0
      case "$status" in
      QUEUED | PENDING | WORKING) ;;
      *) break ;;
      esac
    fi
    if [ "$waited" -ge "$BUILD_WAIT_SECONDS" ]; then
      status="still ${status:-unreadable}"
      break
    fi
    sleep 10
    waited=$((waited + 10))
  done
  # The account the build RAN as, read back from the build's own record rather
  # than taken from the line above: a build that ran as anything else would make
  # the 200 below a fact about another identity. The field is a resource name, so
  # it is READ as one (names_the_caller) and kept raw for the rows: the raw text
  # is what a reader sees in `gcloud builds describe`.
  account=$(gcloud builds describe "$BUILD_ID" --project="${P:?}" --format='value(serviceAccount)' 2>/dev/null | tr -d '\r' | head -n 1 || true)
  local ran_as_caller=no
  if names_the_caller "$account"; then ran_as_caller=yes; fi

  # The step's own EXIT CODE, out of the same record and never out of Cloud
  # Logging: Cloud Build writes it into the build the step ran in, so this
  # reading is there the moment the build ends, whatever the logging front end is
  # doing. It is what the `call` row falls back on when the step's line has not
  # arrived — see call_from_step_exit_code.
  local step_exit
  step_exit=$(gcloud builds describe "$BUILD_ID" --project="${P:?}" --format='value(steps[0].exitCode)' 2>/dev/null \
    | tr -d '\r' | grep -v '^$' | head -n 1 || true)

  # Cloud Build OMITS a zero exit code — the field is a proto3 int64 with no
  # presence — so a step that exited 0 and a step that never ran print the SAME
  # empty column. Read as "no code", the empty column would report the log-less
  # PASS this reading exists for as unmeasured, which is exactly the reading the
  # owner's run of 2026-11-24 needed. The two are told apart by the build's own
  # status, which this run already read: the step is this build's ONLY step, so a
  # build that ended SUCCESS is a step that ran and exited 0, and a build that
  # ended anything else with an empty column is a step that never exited at all.
  local step_exit_omitted_zero=no
  if [ -z "$step_exit" ] && [ "$status" = SUCCESS ]; then
    step_exit=0
    step_exit_omitted_zero=yes
  fi

  # The build's own line, read whether or not the build passed: the step prints
  # its answer BEFORE it exits non-zero, so a refused call carries both the
  # status and the email claim it was refused with. It is the reading that
  # carries the email claim as TEXT; the exit code above carries the same
  # verdict without it.
  #
  # It is read OUT OF THE OBJECT first — the bucket this check made, which the
  # step wrote its one line into — and out of Cloud Logging only if the object
  # did not carry it. That order is the whole point of the object: Cloud Storage
  # hands an object back the moment it is written, while Cloud Logging's
  # ingestion is a pipeline that can simply not carry a line at all — the
  # owner's run of 2026-11-24 came back with a FAILURE build and an empty
  # `gcloud builds log`. So the object is the reading, the log is the fallback,
  # and the exit code read above is under both.
  local got=""
  if line_object; then
    got=$(step_line_in "$SCRATCH/line-object.txt")
    fact line-object PASS "the step's own line was read OUT OF the object it wrote: gcloud storage cat ${LINE_URI} printed '$(printf '%s' "$got" | redact | cut -c1-200)' (the step's own words, redacted and cut short like every other line this block prints). No Cloud Logging was involved in this reading"
  else
    local obj_why
    obj_why=$(last_lines "$SCRATCH/line-object.err")
    if build_log "$BUILD_ID"; then
      got=$(step_line_in "$SCRATCH/build-log.txt")
      owner "the object the step writes its one line into carried nothing this reading could take: gcloud storage cat ${LINE_URI} answered '${obj_why}' within ${LINE_WAIT_SECONDS} s. The step's WRITE to the bucket is what to look at rather than the reading: the account holds roles/storage.objectCreator on that bucket alone (the write-grants row says whether that binding was read back from the bucket's own policy) and the step retries the write ${LINE_TRIES} times, ${LINE_RETRY_SECONDS} s apart, and the step says in the build's own log when its write did not land"
      fact line-object INFO "line not read from the object: gcloud storage cat ${LINE_URI}; read out of the build's own log instead: gcloud builds log ${BUILD_ID} --project=${P}"
    else
      owner "the step's own line was read from NEITHER place: gcloud storage cat ${LINE_URI} answered '${obj_why}' within ${LINE_WAIT_SECONDS} s, and the build's own log carried no line with status= within ${LOG_WAIT_SECONDS} s (it carried '$(last_lines "$SCRATCH/build-log.txt")', and the command answered $(last_lines "$SCRATCH/build-log.err")) — the step says in that log when its write did not land"
      fact line-object INFO "line not read: gcloud storage cat ${LINE_URI}; gcloud builds log ${BUILD_ID} --project=${P}. The reading below is the step's own exit code, out of the build's own record"
    fi
  fi
  local code email
  code=$(field_of "$got" status)
  email=$(field_of "$got" email)

  if [ "$status" = SUCCESS ] && [ "$ran_as_caller" = yes ]; then
    fact build PASS "build ${BUILD_ID} ran as ${CALLER_EMAIL} — its own record's serviceAccount field reads '${account}' — and ended SUCCESS in about ${waited} s (read back by its status and that field, not by streaming its log)"
  elif [ "$status" != SUCCESS ]; then
    local said
    said=$(printf '%s\n' "$(cat "$SCRATCH/build-log.txt" 2>/dev/null || true)" | tr -d '\r' \
      | grep -v '^[[:space:]]*$' | tail -n 4 | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-500 || true)
    fact build FAIL "build ${BUILD_ID} (as ${CALLER_EMAIL}) ended ${status} after about ${waited} s: ${detail:-no failure detail}. Its step's exit code, read out of the build's own record, is ${step_exit:-not given}, and the row below reads it. Its own last lines, for context: ${said:-none read}. Read the whole log with 'gcloud builds log ${BUILD_ID} --project=${P}'. A step that was refused by the front end (403) is the reading this check exists for: the row below carries the status the step got. The account and the service are deleted by this run's Restore, whatever the build did"
  else
    fact build FAIL "build ${BUILD_ID} ended SUCCESS but its own record says it ran as ${account:-nobody this reading can name}, not as ${CALLER_EMAIL}: a 200 earned by another identity is not a statement about the grant this check made. Read 'gcloud builds describe ${BUILD_ID} --project=${P}' — its serviceAccount field is a resource name, and this reading is of the account it ends in"
  fi

  case "$code" in
  "")
    # The step's own line did not arrive — the log was not written, or Cloud
    # Logging has not ingested it. The build's OWN RECORD answers instead: the
    # step ends with one exit code per outcome, and Cloud Build wrote that code
    # into the build. This is why the verdict is independent of the log.
    call_from_step_exit_code "$step_exit" "$detail" "$step_exit_omitted_zero"
    ;;
  notoken)
    fact call FAIL "the build's step minted no ID token from the build's own metadata server, so it never called ${SVC}: the metadata server answered it nothing, which is a fact about the build and not about the grant this check made. Read the step's own lines with 'gcloud builds log ${BUILD_ID} --project=${P}'"
    ;;
  none)
    fact call FAIL "the build's step called ${SVC} with an ID token minted for ${URL} and read no HTTP status back at all (curl answered nothing), so the token's email claim is ${email:-unread} and whether the grant admits the call is NOT measured. Read the step's own lines with 'gcloud builds log ${BUILD_ID} --project=${P}'"
    ;;
  200)
    if [ "$email" = "$CALLER_EMAIL" ]; then
      fact call PASS "the build's step minted an ID token from the build's own metadata server for ${URL}, was answered 200 by the IAM-only service ${SVC}, and the token's email claim is ${email} — the throwaway account this check granted roles/run.invoker on that service alone. The token itself is in no line of this block: only its email claim and the status were printed"
    else
      fact call FAIL "the service answered 200 but the token's email claim is ${email}, not ${CALLER_EMAIL}: the token was minted for another identity, so the 200 says nothing about the account this check granted. The build's serviceAccount field, the grant, and the metadata server's default account are the three things to read"
    fi
    ;;
  *)
    fact call FAIL "the build's step called ${SVC} with an ID token minted for ${URL} and was answered ${code} (the token's email claim: ${email:-unread}): the grant this check made does not admit the account's own token, which is the reading S4's build exception would rest on. A 403 is Cloud Run refusing the caller; a 401 is the front end refusing the token itself. Read the step's own lines with 'gcloud builds log ${BUILD_ID} --project=${P}'"
    ;;
  esac
}

# call_from_step_exit_code EXIT_CODE DETAIL [ZERO_OMITTED]: the `call` row when
# the step's own line was read from neither the object it writes it into nor the
# build's log. The step ends with ONE exit code per outcome
# (the list is on write_build_config), Cloud Build writes it into the build's own
# record, and `gcloud builds describe` hands it back — so the one thing this
# reading never depends on is Cloud Logging, whose ingestion is what the owner's
# run of 2026-11-24 came back empty from, or on the object the step wrote with
# the account's own token. What the row loses by not having the
# line is the email claim as TEXT; the step's exit code 0 is only written when
# that claim IS the throwaway account, so the verdict is the same one. ZERO_OMITTED
# is `yes` when the record carried no code at all on a build that ended SUCCESS:
# Cloud Build omits the zero, so the 0 is read from the build's own status and
# the row says which of the two readings it is. An exit code that is not on the
# list is not read as any of them: the step's script has one `exit` per outcome
# and nothing else in it exits, so a code this reading cannot name — 1, or a
# shell's own status — means the step died before it decided anything.
call_from_step_exit_code() {
  local code=$1 detail=$2 zero_omitted=${3:-no}
  # Every row below is a code that was read WITHOUT the line, so every one of
  # them says the one clause. The two commands that would read it by hand are in
  # the line-object row above, which is the row that says the line did not come;
  # what the object cat answered and what the log carried is progress for the
  # terminal, on stderr, and not a fact in the block.
  owner "the step's own line was read from NEITHER place: gcloud storage cat ${LINE_URI} answered '$(last_lines "$SCRATCH/line-object.err")' within ${LINE_WAIT_SECONDS} s, and the build's own log carried no line with status= in it within ${LOG_WAIT_SECONDS} s (it carried '$(last_lines "$SCRATCH/build-log.txt")', and the command answered $(last_lines "$SCRATCH/build-log.err"))"
  local nowhere="the step's own line was read from neither place"
  case "$code" in
  0)
    local read_as="the build's own record says its step exited 0"
    if [ "$zero_omitted" = yes ]; then
      read_as="the build ended SUCCESS with no steps[0].exitCode in its record, and that empty column IS the code 0: Cloud Build omits a zero exit code, and this build has no step other than this one"
    fi
    fact call PASS "${read_as}, the one code the step exits for: the service answered 200 and the token's email claim is ${CALLER_EMAIL}, the account this check granted roles/run.invoker on ${SVC} alone. ${nowhere}, so the claim is asserted by the exit code and not printed, and no token is in any line of this block"
    ;;
  11)
    fact call FAIL "the build's own record says its step exited 11, the one code it exits for a 401 — the front end refusing the token itself. ${nowhere}, so the email claim was not printed. A 401 refuses the token rather than the grant, so this is not yet the reading S4 rests on"
    ;;
  12)
    fact call FAIL "the build's own record says its step exited 12, the one code it exits for a 403 — Cloud Run refusing this caller. ${nowhere}, so the email claim was not printed. This is the reading S4's build exception would rest on: the grant does not admit the account's own token"
    ;;
  13)
    fact call FAIL "the build's own record says its step exited 13, the one code it exits for any other status or none at all. ${nowhere}, so neither the claim nor the status was printed. The build's own failureInfo.detail: ${detail:-none given}"
    ;;
  14)
    fact call FAIL "the build's own record says its step exited 14, the one code it exits for the metadata server minting it no ID token: ${SVC} was never called and the grant was not measured. ${nowhere}. The build's own default service account and the metadata server are the two things to read"
    ;;
  15)
    fact call FAIL "the build's own record says its step exited 15, the one code it exits for a 200 whose token is NOT ${CALLER_EMAIL}: it was minted for another identity, so the 200 says nothing about the account this check granted. ${nowhere}, so the claim itself was not printed. The build's serviceAccount field, the grant and the metadata server are the three things to read"
    ;;
  "")
    fact call FAIL "${nowhere}, and the build's own record carries no steps[0].exitCode either — and the build did NOT end SUCCESS, so this empty column is not the zero Cloud Build omits — so neither the claim nor the status the service answered was measured. A record with no step exit code on a build that did not succeed is a step that never ran: a container that could not start, or an API that refused the step, and its failureInfo.detail says so: ${detail:-none given}. Read it with 'gcloud builds describe ${BUILD_ID} --project=${P}' and its lines with 'gcloud builds log ${BUILD_ID} --project=${P}'"
    ;;
  *)
    fact call FAIL "${nowhere}, and the build's own record says its step exited ${code}, which is not a code this step writes — 0, 11, 12, 13, 14 and 15 are the only ones it can end with — so the cause is NOT named and the grant was not measured. It is a step that died before it decided anything: a shell that could not run the script, a pull the container could not make. The build's own failureInfo.detail says: ${detail:-none given}. Read it with 'gcloud builds describe ${BUILD_ID} --project=${P}' and its lines with 'gcloud builds log ${BUILD_ID} --project=${P}'"
    ;;
  esac
}

# print_plan: what a real run would run, in order, and what its Restore would
# delete. Printed by --dry-run, which runs NOTHING: no gcloud, no fugaro and no
# curl is called, so this mode is safe on a machine with no credentials at all.
print_plan() {
  say "dry run: NOTHING below was run, and no tool was called at all — not gcloud, not fugaro, not curl. A real run would first check that this is the fugaro-dev installation (fugaro doctor --project fugaro), then read whether the two names below already exist — a filtered list for the service, a filtered list for the account, and the project's own IAM policy, which must not already name the account — and stop there on a name that is already there or on a list that could not be read; then run, in this order:"
  say "  [network, writes] gcloud storage buckets create gs://${BUCKET} --project=${P} --location=${L} --uniform-bucket-level-access --labels=chk195=${BUCKET_NONCE}  (the throwaway bucket the step writes its one line into; the name carries the moment this run started and this shell's process id, and the label is what says a bucket of that name is this run's — a bucket without it is never deleted)"
  say "  [network, writes] gcloud run deploy ${SVC} ${DEPLOY_FLAGS[*]}"
  say "  [network] gcloud run services describe ${SVC} --project=${P} --region=${L} --format=value(status.url)  (the URL the token's audience is)"
  say "  [network] gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L} --format=json  (no public member, or the run stops)"
  say "  [network] curl -sS -o /dev/null -w '%{http_code}' --max-time ${CURL_SECONDS} https://<the URL of the service just deployed>  (an unauthenticated GET: a 401 or 403 is the pass, anything else stops the run before a token is sent)"
  say "  [network, writes] gcloud iam service-accounts create ${CALLER} --project=${P}"
  say "  [network, writes] gcloud run services add-iam-policy-binding ${SVC} --project=${P} --region=${L} --member=serviceAccount:${CALLER_EMAIL} --role=roles/run.invoker  (on THIS service alone)"
  say "  [network, writes] gcloud projects add-iam-policy-binding ${P} --member=serviceAccount:${CALLER_EMAIL} --role=roles/logging.logWriter --condition=None  (which Cloud Build requires of a build's own service account to write its log — the log this check reads the token's email claim out of when it arrives)"
  say "  [network, writes] gcloud storage buckets add-iam-policy-binding gs://${BUCKET} --member=serviceAccount:${CALLER_EMAIL} --role=roles/storage.objectCreator  (on THIS bucket alone: it is what lets the step write its one line into ${LINE_URI}, which is the reading that needs no Cloud Logging at all)"
  say "  [network] gcloud run services get-iam-policy ${SVC} --format=json  (read back: the account, and nobody else, may invoke it)"
  say "  [local] a ${GRANT_SECONDS} s wait for the grant to take effect"
  say "  [network, local] gcloud projects get-iam-policy ${P} --format=json and gcloud storage buckets get-iam-policy gs://${BUCKET} --format=json, each re-read until it names ${CALLER_EMAIL}, bounded at ${GRANTS_WAIT_SECONDS} s (CHECK195_GRANTS_WAIT_SECONDS), then one fixed ${GRANTS_SETTLE_SECONDS} s settle (CHECK195_GRANTS_SETTLE_SECONDS): the roles/logging.logWriter and roles/storage.objectCreator grants are what let the build write its own line and IAM is eventually consistent, so a build started in the window writes neither — the write-grants row records what was waited for and what each policy read back"
  say "  [network, writes] gcloud builds submit --project=${P} --no-source --config=${SCRATCH}/cloudbuild.yaml --service-account=projects/${P}/serviceAccounts/${CALLER_EMAIL} --async (nothing is staged anywhere: the build has no source, and the installation's gs://${P}_cloudbuild is not touched)"
  say "  [network] gcloud builds describe <build id> --project=${P}, every 10 s until the build ends, then once more for its serviceAccount field and once more for steps[0].exitCode — the account field is a RESOURCE NAME, and this reading reduces it to the account it ends in (the bare address, or the account's numeric uniqueId, which then costs one gcloud iam service-accounts describe) so that a build that ran as somebody else still fails and a build that ran as this check's own account is not reported as another identity"
  say "  [network] gcloud storage cat gs://${BUCKET}/${LINE_OBJECT}  (the step's own line, OUT of the object it wrote it into: re-read for up to ${LINE_WAIT_SECONDS} s (CHECK195_LINE_WAIT_SECONDS) — this is the reading that needs no Cloud Logging)"
  say "  [network] gcloud logging read 'resource.type=\"build\" resource.labels.build_id=\"<build id>\"' --project=${P}  (the same line out of the build's log, read only when the object did not carry it: retried while Cloud Logging catches up. Nothing rests on it)"
  say "  [network, local] when NEITHER the object nor the log carried the line, the row says so in one line, with the exact command to read it by hand (gcloud storage cat gs://${BUCKET}/${LINE_OBJECT}) and the step's exit code above as the reading that is left"
  say "  [the build itself, which is the point of the check] one step in ${IMAGE}: mint an ID token for the service's URL from the build's own metadata server (http://metadata.google.internal/…/default/identity?audience=<the service URL>&format=full), call the service with it, print CHK195 status=<code> email=<the token's email claim> — never the token — and end with ONE exit code per outcome, which Cloud Build writes into the build's own record: 0 = 200 and the claim is ${CALLER_EMAIL} (the one code the record does not carry: Cloud Build omits a zero exit code, and this check reads it from the build's own SUCCESS), 11 = 401, 12 = 403, 13 = any other status or none at all, 14 = the metadata server minted no token, 15 = the claim is another identity"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, writes] gcloud builds cancel <build id> --project=${P} (refused, harmlessly, when the build already ended)"
  say "  [network, deletes] gcloud run services delete ${SVC} --project=${P} --region=${L}  (which takes the roles/run.invoker binding with it)"
  say "  [network, deletes] gcloud storage rm --recursive gs://${BUCKET} --quiet  (--recursive because the step's object is in it, and a bucket holding an object is not deleted by buckets delete; it takes the object and the bucket-level roles/storage.objectCreator binding with it, and runs only when the bucket carries this run's own label chk195=${BUCKET_NONCE} — a bucket that does not is never touched)"
  say "  [network, deletes] gcloud projects remove-iam-policy-binding ${P} --member=serviceAccount:${CALLER_EMAIL} --role=roles/logging.logWriter --condition=None"
  say "  [network, deletes] gcloud iam service-accounts delete ${CALLER_EMAIL} --project=${P}, re-reading the project policy first and retrying up to ${ACCOUNT_DELETE_TRIES} times: the binding removal and the account deletion are ordered IAM writes that IAM's own consistency can put out of order — the owner's run of 2026-11-24 came back with the account still there and the refusal was not captured, so the re-read covers both causes: a delete refused as still referenced by a policy binding, and a delete that landed whose account the list was still naming"
  say "  [network] four reads that say the service, the bucket, the account and the project binding are gone"
  say "What is left behind either way: the build's own RECORD, in the project's Cloud Build history. It is the project's log of what ran rather than a resource this check made, and nothing here deletes it."
}

main() {
  need_tools
  scratch_up
  guard_xtrace
  guard_constants
  guard_deploy_is_iam_only

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact dry-run PASS "the plan above was printed and NOTHING was called: no gcloud, no fugaro, no curl. The guards that read the cloud (the installation's project, the two names' existence) run only in a real run"
    return 0
  fi

  guard_project
  guard_resources
  step_line_bucket
  step_service
  step_caller
  step_build
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region image deploy-iam-only baseline scratch line-bucket service
  iam-policy unauthenticated-request invoker-grant write-grants build line-object call
  service-deleted bucket-deleted account-deleted binding-removed restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 195 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 195 RESULT END (exit %s)\n' "$status" >&3
}

# interrupted: end the wait FIRST, then leave. It is a background job this shell
# is waiting on, and the trap runs as soon as the signal arrives because `wait`
# is interruptible — killing it here is what keeps the Restore from being held
# behind a sleep that has minutes left.
interrupted() {
  local pid
  for pid in ${WAIT_PID:-}; do
    kill "$pid" 2>/dev/null || true
  done
  WAIT_PID=""
  owner "interrupted"
  exit 130
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# — including through the cleanup, which is where its second answer matters. A
# second Ctrl-C during the Restore would otherwise kill this shell mid-cleanup:
# the service can be deleted while the account is not, and the RESULT block that
# says what was left behind is never printed.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  interrupted
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
