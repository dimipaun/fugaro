#!/usr/bin/env bash
# Check 190 — git fetch and push through a throwaway, IAM-only Cloud Run proxy.
#
# It settles the four UNVERIFIED questions the Bitbucket proxy is built on
# (secrets-brokering/reconciliation.md F13, F14, U2, U7 and U8): whether a pack
# above 32 MiB can be pushed through Cloud Run on HTTP/1.1 (chunked), the
# request count and bytes of a full clone and of a blobless one, the cold start
# of the first git request after idle, and what the front end does with a
# request held open past its 3600 s timeout. The register's section is
# docs/gcp-live-checklist.md, "Check 190"; the owner's steps are
# docs/secrets-brokering/check190-steps.md.
#
# Usage:      CHECK190_REPO=<workspace>/<repo> CHECK190_TOKEN_FILE=<file> \
#               bash scripts/live-checks/check190.sh
#             bash scripts/live-checks/check190.sh --dry-run   (guards and files
#             only: nothing is created, built, deployed or deleted; it prints
#             the commands a real run would run and the cleanup commands)
# Needs:      bash, mktemp, gcloud, git, curl, awk and fugaro. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it. YOUR account creates a
#             bucket and a registry and sets IAM on both (storage.buckets.create,
#             artifactregistry.repositories.create and setIamPolicy on each),
#             which a project Owner has; it does not need to read Cloud Logging.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             The project is HARD-CODED and `fugaro doctor` must answer that
#             this is the fugaro-dev installation. Never belong, never another
#             project: you must not run it against any other installation.
# Creates:    one throwaway Cloud Run service (fugaro-chk190) with IAM required
#             and no public invoker, the Artifact Registry repository and image
#             it is built into, the Secret Manager container that holds the
#             sandbox repository's token for the proxy, its own staging bucket
#             (fugaro-dev-chk190-staging, uniform access, us-east5) for the
#             build's source, two grants to the installation's build account
#             (artifactregistry.writer on that registry and
#             storage.objectViewer on that bucket, nothing project-wide), and
#             one branch on the sandbox repository. The Restore deletes all of
#             them and asserts four counts read 0. A service, repository,
#             secret or bucket of those names that was already there is a hard
#             stop: this check never adopts, writes into or deletes what it did
#             not create, and it writes nothing into the installation's own
#             gs://fugaro-dev_cloudbuild.
# Costs:      wall clock, not money: around two hours at the terminal. The
#             cold-start wait is 35 minutes (CHECK190_IDLE_SECONDS) and the
#             slow push is held open for an hour (CHECK190_SLOW_SECONDS). One
#             build and one idle Cloud Run instance are billed to the project
#             while it runs.
# Runs:       by YOU. No agent runs any step of this, and no agent has: what it
#             measures is a live project and a real token.
# Cleanup:    always: the service, the image, the repository (and its grant),
#             the secret, the staging bucket with the source in it (and its
#             grant) and the branch are deleted by the Restore, which runs from
#             the exit trap too — so a Ctrl-C in the middle does not leave a
#             service running, and a build still running is cancelled first.
#             The four counts are checked before the scratch directory goes,
#             and the directory is KEPT when a count is not 0, with a message
#             saying where it is and what to delete by hand.
# Output:     progress on stderr, ONE block on stdout headed CHECK 190 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one did not (or a hard stop ended the run).
#
# The token is never printed, never on a command line and never in a log: it is
# read out of CHECK190_TOKEN_FILE by gcloud's --data-file (so it is in no argv),
# put into the Secret Manager container this check creates, and mounted into the
# service as a file — not as an environment variable, which a `gcloud run
# services describe` would print back. The identity token each request carries
# is minted fresh and written into 0600 files in the scratch directory, which
# the requests name by path (curl's --config and git's GIT_CONFIG_GLOBAL):
# `ps` shows every process's argv to every user on the machine, and the slow
# push would hold a token there for an hour.
#
# CHECK190_DRY_RUN, CHECK190_REPO, CHECK190_TOKEN_FILE, CHECK190_CURL_SECONDS,
# CHECK190_IDLE_SECONDS, CHECK190_SLOW_SECONDS, CHECK190_BIG_MIB,
# CHECK190_GRANT_SECONDS, CHECK190_BUILD_WAIT_SECONDS, CHECK190_LOG_WAIT_SECONDS and CHECK190_BUILD_SA exist for this script's own test: they shorten waits and
# sizes, and none of them can point the check at another project, another
# region or another service. CHECK190_REPO and CHECK190_TOKEN_FILE are the two
# inputs the check cannot invent (the sandbox repository and the token file the
# owner writes with umask 077); CHECK190_BUILD_SA names the build account when
# the installation has several.
set -euo pipefail

# No xtrace, whatever the shell it was started from had. This script's commands
# carry credentials: the identity token is written into the 0600 files above and
# the token file is read by gcloud, and `bash -x` would print both. The trace is
# turned OFF here, BASH_XTRACEFD is unset so a trace redirected to a file this
# script knows nothing about cannot be reopened, and guard_xtrace REFUSES the
# run when this shell arrived with tracing on — the case where the flag sits in
# SHELLOPTS/BASHOPTS and every child would start tracing on its own. Check 90 and
# Check 91 have the same guard for the same reason.
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
die() { say "$*"; exit 1; }

# The two the whole check hangs on. HARD-CODED, never `gcloud config get-value
# project`: a check that reads the project from the environment can be pointed
# at another installation by a stray config, and this one must never be.
P=fugaro-dev
L=us-east5
SVC=fugaro-chk190
SECRET=fugaro-chk190-token
IMG="${L}-docker.pkg.dev/${P}/${SVC}/${SVC}:chk190"
# This check's own staging bucket. A bucket name is global, so it carries the
# project's name; it is fixed rather than per run so the baseline guard can say
# that one of this name was already there, which is a stop like the others.
BUCKET_NAME="${P}-chk190-staging"
BUCKET="gs://${BUCKET_NAME}"
CURL_SECONDS=${CHECK190_CURL_SECONDS:-300}
IDLE_SECONDS=${CHECK190_IDLE_SECONDS:-2100}
SLOW_SECONDS=${CHECK190_SLOW_SECONDS:-3660}
BIG_MIB=${CHECK190_BIG_MIB:-33}
# The slow route, in ONE place: the prefix the proxy is deployed with (it strips
# it off every request that carries it and holds that request open, see the
# proxy in check190/), and the upstream the proxy forwards to when nothing sets
# CHK190_UPSTREAM. The check builds the URL it pushes the slow branch through out
# of the prefix, so the route it asks through and the route the proxy answers on
# cannot drift apart — a prefix that reaches Bitbucket instead of being stripped
# is a repository that is not there, and a prefix stripped together with the
# separator after it is not a URL at all.
STALL_PREFIX=/chk190-slow/
UPSTREAM=https://bitbucket.org
GRANT_SECONDS=${CHECK190_GRANT_SECONDS:-60}
BUILD_WAIT_SECONDS=${CHECK190_BUILD_WAIT_SECONDS:-1800}
# How long a failed git row waits for the proxy's own log lines to reach Cloud
# Logging before it reads them: ingestion lags the request by seconds.
LOG_WAIT_SECONDS=${CHECK190_LOG_WAIT_SECONDS:-20}
BUILD_SA=${CHECK190_BUILD_SA:-}
REPO=${CHECK190_REPO:-}
TOKEN_FILE=${CHECK190_TOKEN_FILE:-}
# The proxy source is this script's own directory's check190/, so the check
# builds the main.go that ships beside it rather than anything from $PWD.
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROXY_SRC="$HERE/check190"
# The one deploy line, in one place, so guard_deploy_is_iam_only can read
# exactly what a real run would run. The token is mounted as a FILE and never as
# an environment variable.
DEPLOY_FLAGS=(
  --project="$P" --region="$L" --image="$IMG"
  --no-allow-unauthenticated
  --timeout=3600 --min-instances=0 --max-instances=1
  --set-secrets="/chk190/token=${SECRET}:latest"
  --set-env-vars="CHK190_TOKEN_FILE=/chk190/token,CHK190_STALL_SECONDS=${SLOW_SECONDS},CHK190_STALL_PREFIX=${STALL_PREFIX}"
)

# stop REASON: records a FAILed fact, ends the run. The EXIT trap prints the
# block, runs the Restore for whatever this run created, and cleans up.
stop() {
  fact "aborted" FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

DRY_RUN=${CHECK190_DRY_RUN:-}
case "${1:-}" in
--dry-run) DRY_RUN=1 ;;
"") ;;
*) die "usage: $PROG [--dry-run]" ;;
esac

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 190 needs '$1' on PATH and it is not there"; }
need_tools() {
  need bash
  need mktemp
  need gcloud
  need git
  need curl
  need awk
  # fugaro is checked here rather than left to guard_project's own call: a
  # machine without it would otherwise fail as "this is not the fugaro-dev
  # installation", which reads like the wrong installation rather than a
  # missing tool.
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk190-mine"
SERVICE_MINE=""
REPO_MINE=""
SECRET_MINE=""
BUCKET_MINE=""
PUSHED=""
SCRATCH_REMOVED=no
COUNTS_OK=no
PRINTED=""
FACTS_CAPTURED=""
# The two 0600 files the identity token is written into, the name this run gave
# its branch, the service's host, the build account resolved read-only, the
# prefix in this run's own bucket the build's source is staged under, the flag
# that says a build was attempted at all (the Restore removes the image only
# then) and the build's ID (the Restore cancels it if it is still running).
AUTH_CONF=""
GIT_CONF=""
NAME=""
HOST=""
BUILD_ATTEMPTED=no
BUILD_ID=""
STAGING_PREFIX=""
# The cleanup flag finish sets: a second Ctrl-C while the Restore is running
# must not kill the shell that is running it (see finish).
CLEANUP_RUNNING=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk190.XXXXXXXX") || die "Check 190 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  SERVICE_MINE="$SCRATCH/service-mine"
  REPO_MINE="$SCRATCH/repo-mine"
  SECRET_MINE="$SCRATCH/secret-mine"
  BUCKET_MINE="$SCRATCH/bucket-mine"
  PUSHED="$SCRATCH/pushed"
  AUTH_CONF="$SCRATCH/idt.conf"
  GIT_CONF="$SCRATCH/gitconfig"
  # The branch this run pushes: named after the scratch directory's own random
  # part, so two runs never share one and the Restore can name exactly what it
  # created.
  NAME=${SCRATCH##*.}
  STAGING_PREFIX="${BUCKET}/source/"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 190 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  fact "scratch" PASS "$SCRATCH (mode 0700, made by this check, removed by its Restore)"
}

remove_scratch() {
  case "$SCRATCH" in
  "")
    # A guard stop leaves this empty and there is nothing to refuse: saying
    # "refusing to remove a scratch directory of ''" for a run that never made
    # one reads like a cleanup that went wrong. The guard stays for the two
    # paths that do mean trouble (a bare "/" or the owner's home directory).
    return 0
    ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  # Guarded: this runs from the exit trap, where a `rm` that fails would end the
  # shell under `set -e` before the RESULT block is printed. A directory that is
  # still there is a FACT, and the block says so — it is not a reason to lose
  # the rest of the block.
  rm -rf "$SCRATCH" || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# redact: what a git or gcloud message may carry back into the block. The block
# is pasted into a pull request, so a credential-shaped run of characters goes,
# whatever it is: the value of any Authorization, Proxy-Authorization, Cookie or
# Set-Cookie header, a scheme with a credential-long value after it, or any
# name=value whose name says token or secret. A challenge (`Basic realm="..."`,
# `Bearer error="invalid_token"`) is shorter than any credential and stays: it
# is what tells a refusal by Bitbucket from one by the front end.
redact() {
  sed -E \
    -e 's/([Aa]uthorization|[Cc]ookie)[[:space:]]*:[^|]*/\1: <redacted> /g' \
    -e 's/(Bearer|Basic|x-token-auth)[[:space:]]+[A-Za-z0-9._~+/=-]{12,}/<redacted>/g' \
    -e 's/[A-Za-z0-9_]*(token|Token|TOKEN|secret|Secret|SECRET)[A-Za-z0-9_]*=[^[:space:]]+/<redacted>/g'
}

# git_error_excerpt NAME: the last two lines git printed for the step whose
# stderr went to $SCRATCH/NAME.err, redacted and shortened. Never a body and
# never a header: git's own messages are all this ever reads.
git_error_excerpt() {
  local f="$SCRATCH/$1.err"
  [ -f "$f" ] || {
    printf 'no output'
    return 0
  }
  tail -n 2 "$f" | tr -d '\r' | tr '\n' ' ' | redact | cut -c1-400
}

# run_git_traced NAME DIR ARGS...: run_git, with git's own HTTP trace written to
# $SCRATCH/NAME.curl — request lines and headers only (GIT_TRACE_CURL_NO_DATA),
# with git's own redaction of the credential headers on (GIT_TRACE_REDACT). A
# git failure says "could not read Username" for every 401 whoever sent it, so
# the trace is the only place the status and the challenge are. The file is in
# the 0700 scratch directory, is made 0600 before git writes to it, and is read
# by http_excerpt and http_reading only, which redact again.
run_git_traced() {
  local name=$1
  shift
  (umask 077 && : >"$SCRATCH/$name.curl") || return 1
  GIT_TRACE_CURL="$SCRATCH/$name.curl" GIT_TRACE_CURL_NO_DATA=1 GIT_TRACE_REDACT=1 run_git "$@"
}

# http_excerpt NAME: the last exchanges git traced for the step NAME — each
# request line, the status it was answered with and the three response headers
# that say who answered (WWW-Authenticate, the proxy's own X-Chk190-Upstream and
# Server) — redacted, newest last. No body and no request header but the
# request line is ever read.
http_excerpt() {
  local f="$SCRATCH/$1.curl"
  [ -s "$f" ] || {
    printf 'no HTTP exchange was traced'
    return 0
  }
  sed -n -E \
    -e 's/^.*=> Send header: ((GET|POST) .*)$/\1/p' \
    -e 's/^.*<= Recv header: (HTTP\/.*|[Ww][Ww][Ww]-[Aa]uthenticate:.*|[Xx]-[Cc]hk190-[Uu]pstream:.*|[Ss]erver:.*)$/\1/p' "$f" \
    | tr -d '\r' | tail -n 12 | tr '\n' '|' | sed -e 's/|$//' -e 's/|/ | /g' | redact | cut -c1-700 || true
}

# http_reading NAME: who refused, read off the last final answer git traced.
# The proxy marks every answer it forwards with X-Chk190-Upstream, so a 401 or
# 403 that carries the mark is Bitbucket's and one without it is the front
# end's own, given before the proxy saw the request.
http_reading() {
  local f="$SCRATCH/$1.curl"
  [ -s "$f" ] || {
    printf 'git saw no HTTP answer at all (a connection or TLS failure, or git never reached the network)'
    return 0
  }
  tr -d '\r' <"$f" | awk '
    /<= Recv header: HTTP\// {
      c = $0; sub(/.*<= Recv header: HTTP\/[^ ]* /, "", c); sub(/ .*/, "", c)
      if (c !~ /^1/) { code = c; marked = 0 }
      next
    }
    tolower($0) ~ /<= recv header: x-chk190-upstream:/ { if (code != "") marked = 1 }
    END {
      if (code == "") { print "git saw no HTTP answer at all (a connection or TLS failure)"; exit }
      if ((code == "401" || code == "403") && marked) {
        printf "Bitbucket refused the proxy%ss credential (the %s carries X-Chk190-Upstream, so the proxy forwarded it): the token is not an access token of this repository, has expired or been revoked, or lacks the repository scope\n", "\047", code; exit
      }
      if (code == "401" || code == "403") {
        printf "the Cloud Run front end refused the request before the proxy saw it (the %s carries no X-Chk190-Upstream): the identity token was missing, expired or not accepted, or your account lacks roles/run.invoker on the service\n", code; exit
      }
      if (marked) { printf "the last answer, %s, came from Bitbucket through the proxy\n", code; exit }
      printf "the last answer, %s, came from the front end or the proxy itself, not from Bitbucket (no X-Chk190-Upstream)\n", code
    }' || true
}

# proxy_log_excerpt: the proxy's own last lines (one per request: method,
# status, sizes; and one per refusal by Bitbucket: path and challenge scheme),
# newest first, redacted. Best effort: an account that cannot read Cloud
# Logging gets "none read", and the row says so rather than failing on it.
proxy_log_excerpt() {
  if [ "$LOG_WAIT_SECONDS" -gt 0 ]; then sleep_bg "$LOG_WAIT_SECONDS"; fi
  gcloud logging read \
    "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SVC}\" AND textPayload:\"chk190\"" \
    --project="${P:?}" --freshness=15m --limit=8 --format='value(textPayload)' 2>/dev/null \
    | grep -v '^$' | tr -d '\r' | tr '\n' '|' | sed -e 's/|$//' -e 's/|/ | /g' | redact | cut -c1-700 || true
}

# git_failure_detail NAME: everything a failed git row carries — git's own last
# words, the HTTP exchange it saw, who refused, and the proxy's own log.
git_failure_detail() {
  local logs
  logs=$(proxy_log_excerpt)
  printf '%s. HTTP as git saw it (newest last): %s. Reading: %s. Proxy log (newest first): %s' \
    "$(git_error_excerpt "$1")" "$(http_excerpt "$1")" "$(http_reading "$1")" \
    "${logs:-none read (your account may not read Cloud Logging, or the lines had not arrived yet)}"
}

# The Restore: everything this run created, deleted in the one order that works
# (the branch needs the service, the service needs the secret, and the image
# lives in the repository). It runs from the exit trap too, so a Ctrl-C in the
# middle does not leave a billed service behind.
restore_run() {
  if [ ! -f "$SERVICE_MINE" ] && [ ! -f "$REPO_MINE" ] && [ ! -f "$SECRET_MINE" ] && [ ! -f "$BUCKET_MINE" ]; then
    fact "restore" PASS "nothing of this check's was created (no service, no repository, no secret, no bucket), so there is nothing to delete"
    COUNTS_OK=yes
    return 0
  fi
  say "Restore: deleting what this check created"
  if [ -f "$PUSHED" ]; then
    # The branch first: deleting it needs the service, which the next lines
    # delete. Both listings are read with a freshly minted token, and a listing
    # that cannot be read is NOT the answer "the branch is not there" — see
    # branch_refs for why that is the ordinary case here rather than a corner.
    #
    # The delete runs whatever the first listing said, and it runs IN THE CLONE:
    # `git push` needs a repository to run in and the scratch directory is
    # deliberately not one, so a delete run from it dies with "fatal: not a git
    # repository". That is silent here (a failed delete must not end the
    # Restore) and it left the branch this run pushed on the sandbox repository
    # while the block read as cleaned up. Both names are this run's own, and
    # deleting one that is not there is harmless.
    local refs rc=0 listed
    refs=$(branch_refs) || rc=$?
    listed=$rc
    run_git "$SCRATCH/full" push --quiet "$(git_url)" --delete "chk190/${NAME}" "chk190/${NAME}-slow" >/dev/null 2>&1 || true
    rc=0
    refs=$(branch_refs) || rc=$?
    if [ "$listed" != 0 ]; then
      fact "branch-deleted" FAIL "whether this run's branches (chk190/${NAME}, chk190/${NAME}-slow) are on $(repo_name) cannot be read: 'git ls-remote' failed even with a freshly minted identity token ($(git_error_excerpt ls-remote)). A failed listing is not the answer 'there is no branch', so this run does not claim either was deleted — read it yourself (git ls-remote <remote> 'refs/heads/chk190/*') and delete what is there (git push <remote> --delete <branch>). The delete of both names was attempted from the clone all the same"
    elif [ "$rc" != 0 ]; then
      fact "branch-deleted" FAIL "the delete of this run's branches (chk190/${NAME}, chk190/${NAME}-slow) was attempted on $(repo_name), and whether they are gone cannot be read: the listing that followed failed ($(git_error_excerpt ls-remote)). Nothing is claimed about them — read it yourself (git ls-remote <remote> 'refs/heads/chk190/*') and delete what is still there"
    elif [ -n "$refs" ]; then
      fact "branch-deleted" FAIL "a branch this run pushed is still on $(repo_name): $(printf '%s' "$refs" | awk '{print $2}' | tr '\n' ' ')— delete it by hand (git push <remote> --delete <branch>)"
    else
      fact "branch-deleted" PASS "no branch named chk190/${NAME} or chk190/${NAME}-slow is on $(repo_name) any more"
    fi
  else
    fact "branch-deleted" PASS "no branch was pushed: the run stopped before its first push, so there is none to delete"
  fi
  if [ -n "$BUILD_ID" ]; then
    # A build still running would push into the registry the next lines delete;
    # cancelling one that already ended is refused, and that is fine.
    gcloud builds cancel "$BUILD_ID" --project="${P:?}" --quiet >/dev/null 2>&1 || true
  fi
  if [ -f "$SERVICE_MINE" ]; then
    gcloud run services delete "${SVC:?}" --project="${P:?}" --region="${L:?}" --quiet >&2 || true
  fi
  if [ "$BUILD_ATTEMPTED" = yes ]; then
    gcloud artifacts docker images delete "${IMG:?}" --project="${P:?}" --delete-tags --quiet >&2 || true
  fi
  if [ -f "$REPO_MINE" ]; then
    gcloud artifacts repositories delete "${SVC:?}" --project="${P:?}" --location="${L:?}" --quiet >&2 || true
  fi
  if [ -f "$SECRET_MINE" ]; then
    # The container that held the sandbox token: deleting it takes its version
    # and the accessor binding with it, so no copy of the token outlives this
    # check inside the project.
    gcloud secrets delete "${SECRET:?}" --project="${P:?}" --quiet >&2 || true
  fi
  if [ -f "$BUCKET_MINE" ]; then
    # The bucket with everything in it (the staged source): it is this run's
    # own, so the whole of it goes, and its IAM binding with it.
    gcloud storage rm -r "${BUCKET:?}" --quiet >&2 || true
  fi
  local st rp sc bk
  st=$(svc_state)
  rp=$(repo_state)
  sc=$(secret_state)
  bk=$(bucket_state)
  if [ ! -f "$BUCKET_MINE" ]; then
    fact "bucket-deleted" PASS "this run created no staging bucket, so there is none to delete"
  elif [ "$bk" = not-there ]; then
    fact "bucket-deleted" PASS "${BUCKET}, with the source staged in it and the build account's objectViewer binding, is gone (read back by a listing)"
  elif [ "$bk" = there ]; then
    fact "bucket-deleted" FAIL "${BUCKET} is still in ${P}: delete it by hand with gcloud storage rm -r ${BUCKET}"
  else
    fact "bucket-deleted" FAIL "whether ${BUCKET} is gone cannot be read (the list failed), so nothing is claimed about it: check it with gcloud storage buckets list --project=${P} --filter=name=${BUCKET_NAME} and delete it with gcloud storage rm -r ${BUCKET} if it is there"
  fi
  if [ "$st" = not-there ] && [ "$rp" = not-there ] && [ "$sc" = not-there ] && [ "$bk" = not-there ]; then
    COUNTS_OK=yes
    fact "restore" PASS "the service, the image's repository (with the build account's writer binding), the secret container and the staging bucket are gone: four counts read 0"
  else
    fact "restore" FAIL "counts after the Restore: service ${st}, repository ${rp}, secret ${sc}, bucket ${bk} — each must read not-there. The scratch directory is kept: $SCRATCH. Delete by hand what is still there only if you know it is this check's"
  fi
}

cleanup() {
  restore_run
  # The two 0600 files hold a minted identity token and go before the scratch
  # directory, so nothing of the sort survives inside a directory that is KEPT
  # because a count was not 0.
  rm -f "$AUTH_CONF" "$GIT_CONF" || true
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  if [ "$COUNTS_OK" = yes ]; then
    remove_scratch
  else
    say "the scratch directory is kept: $SCRATCH"
  fi
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to run the Restore,
  # which deletes a service this check created in the owner's project, and a
  # second Ctrl-C during it would kill this shell with the deletes half done.
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
  if printf '%s\n' "$facts" | grep -q '|FAIL|' && [ "$status" = 0 ]; then
    status=1
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check190.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this script prints the token file's contents and the identity token it mints"
  fi
}

guard_project() {
  # Hard stop 1: the project and the region are the two the script just set.
  if [ "$P" != fugaro-dev ] || [ -z "$L" ]; then
    stop "the project and the region must be fugaro-dev and a region; P=${P} L=${L}"
  fi
  # Hard stop 2: this is the fugaro-dev installation, not another one.
  #
  # The question is doctor's project: LINE, and its exit status is not the
  # answer to it: doctor exits 1 whenever any check of its own failed
  # (emitDoctor returns ExitUserError, internal/cli/doctor.go), and billing,
  # IAM and the update check have nothing to do with which installation this
  # is — so the status is not read (`|| true`). Its output is captured rather
  # than piped into grep for two reasons: `set -o pipefail` would put doctor's
  # own status into the pipeline and turn a perfectly matched line into "this
  # is not the fugaro-dev installation", and `grep -q` closes the pipe on its
  # match while doctor is still printing — so doctor would die on the broken
  # pipe (141) and pipefail would stop the run just the same. The stop names
  # all three causes: a foreign checkout, a --project that disagrees with it,
  # or a doctor that could not run here at all.
  local doctor_out
  doctor_out=$(fugaro doctor --project fugaro || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro printed no 'project: ... (GCP fugaro-dev,' line. Either this is not the fugaro-dev installation, or --project fugaro disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials and reads the installation's billing and IAM policy). Run 'fugaro doctor --project fugaro' here yourself and read its output before going on — this check runs on the sandbox installation only, never belong"
  fi
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

# guard_inputs: the two inputs the check cannot invent, and the mode the token
# file must have. The mode is checked BEFORE anything is created and before the
# file is read: a token file another user can read is a token the owner has
# already leaked, and this check must not be the thing that copies it into a
# project.
guard_inputs() {
  case "$REPO" in
  */*/* | "" | /* | */)
    stop "CHECK190_REPO must be '<workspace>/<repository>' of the sandbox Bitbucket repository, and it is '${REPO:-nothing}'. Nothing was created"
    ;;
  */*) ;;
  *)
    stop "CHECK190_REPO must be '<workspace>/<repository>' of the sandbox Bitbucket repository, and it is '${REPO:-nothing}'. Nothing was created"
    ;;
  esac
  fact "repo" PASS "$(repo_name) (the sandbox repository named by CHECK190_REPO; the check pushes one branch to it and deletes it again)"
  if [ -z "$TOKEN_FILE" ]; then
    stop "CHECK190_TOKEN_FILE must name the file holding the sandbox repository's access token (the owner writes it with 'umask 077 && pbpaste > ~/check190.token'), and it is empty. Nothing was created"
  fi
  if [ ! -f "$TOKEN_FILE" ]; then
    stop "CHECK190_TOKEN_FILE names '${TOKEN_FILE}', which is not a regular file here. Nothing was created"
  fi
  local mode
  # stat's mode flag is -c on GNU and -f on BSD/macOS; the owner runs this on
  # their own machine, which may be either.
  mode=$(stat -c %a "$TOKEN_FILE" 2>/dev/null || stat -f %Lp "$TOKEN_FILE" 2>/dev/null || true)
  if [ "$mode" != 600 ]; then
    stop "the token file named by CHECK190_TOKEN_FILE is mode ${mode:-unknown}, not 0600: a token file another user can read is a token that has already leaked, and this check will not copy it into a project. Write it with 'umask 077 && pbpaste > <file>' (or 'chmod 600 <file>') and run again. Nothing was created"
  fi
  if [ ! -s "$TOKEN_FILE" ]; then
    stop "the token file named by CHECK190_TOKEN_FILE is empty, so there is no token to hand the proxy. Nothing was created"
  fi
  # The proxy authenticates to Bitbucket as x-token-auth, which is the username
  # of a repository, project or workspace ACCESS token and of nothing else. An
  # Atlassian API token or an app password in its place is answered 401 on the
  # clone, after the build and the deploy, and git reports that 401 as "could
  # not read Username". Their prefixes say which they are; only the prefix is
  # read, and it is not printed.
  local kind=""
  case "$(head -c 5 "$TOKEN_FILE")" in
  ATATT) kind="an Atlassian API token" ;;
  ATBB*) kind="a Bitbucket app password" ;;
  esac
  if [ -n "$kind" ]; then
    stop "the token file named by CHECK190_TOKEN_FILE holds ${kind}, and the proxy authenticates to Bitbucket as x-token-auth, which takes a repository, project or workspace access token only: git would be answered 401 on the first clone. Create an access token on the sandbox repository (Repository settings > Access tokens, scopes repository:read and repository:write), write it to the file the same way and run again. Nothing was created"
  fi
  fact "token-file" PASS "mode 0600, non-empty, not an API token or an app password by its prefix, read from CHECK190_TOKEN_FILE (the path is not printed, and the value is in no log line, no argument and no block)"
}

# guard_deploy_is_iam_only: the deploy line is the one place a throwaway service
# could be left answering the whole internet, and the token it attaches is a real
# one — so the flag is read out of the argv a real run would pass rather than
# trusted to be in it. Without this the only thing that would notice a dropped
# flag is the request probe after the deploy, and that probe is a request to a
# service that is already public by then.
guard_deploy_is_iam_only() {
  local f
  for f in "${DEPLOY_FLAGS[@]}"; do
    if [ "$f" = --no-allow-unauthenticated ]; then
      fact "deploy-iam-only" PASS "the deploy line carries --no-allow-unauthenticated: the service requires IAM and the token it attaches is behind no public URL"
      return 0
    fi
  done
  stop "the deploy line does not carry --no-allow-unauthenticated (DEPLOY_FLAGS is: $(printf '%s ' "${DEPLOY_FLAGS[@]}")), so the throwaway service would answer anyone who found its URL, with the sandbox repository's own token behind it. This check refuses before anything is created: add the flag back to DEPLOY_FLAGS and run again. Nothing was created"
}

# The slow route is one prefix in two places — the environment variable the
# proxy is deployed with and the URL the slow push is sent to — and the proxy
# strips it off the path it forwards. A prefix that does not start and end with
# "/" is stripped together with the separator beside it, and what is left is not
# a path: the live run of 2026-10-30 had the slow endpoint answered with a 502
# after 0 s and no hold measured at all.
guard_slow_route() {
  case "$STALL_PREFIX" in
  /*/) ;;
  *) stop "the slow route's prefix (${STALL_PREFIX}) does not start and end with '/'. The proxy strips it off every request that carries it before it forwards, so a prefix without both separators takes one of them with it and Bitbucket is asked for a path that is not there — the slow push would then measure nothing. It is a constant in this script (STALL_PREFIX); fix it there and run again. Nothing was created" ;;
  esac
}

# guard_proxy_source: the proxy this check deploys is the main.go committed
# beside it, and nothing else. A missing file is a stop before anything exists.
guard_proxy_source() {
  if [ ! -f "$PROXY_SRC/main.go" ]; then
    stop "the proxy source ${PROXY_SRC}/main.go is not there, so there is nothing to build and deploy. Run this check from a checkout that carries scripts/live-checks/check190/main.go. Nothing was created"
  fi
  fact "proxy-source" PASS "${PROXY_SRC}/main.go (the throwaway reverse proxy, built into ${IMG})"
}

# The three existence probes, each one filtered LIST: absence is an empty list,
# never an error string. The real gcloud says `Cannot find service [NAME]` for a
# describe of an absent service, with no NOT_FOUND in it, so nothing here reads
# a message. A failed list is NOT the answer "it is not there": reading one as
# absent is how this check would adopt a service it did not create — which its
# Restore would then DELETE — so a failed list is `unreadable` and stops the run.
list_state() {
  local want=$1 out rc=0
  shift
  out=$("$@" 2>"$SCRATCH/list.err") || rc=$?
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

svc_state() {
  list_state "${SVC:?}" gcloud run services list --project="${P:?}" --region="${L:?}" \
    --filter="metadata.name=${SVC}" --format='value(metadata.name)'
}
repo_state() {
  list_state "projects/${P:?}/locations/${L:?}/repositories/${SVC:?}" gcloud artifacts repositories list \
    --project="${P:?}" --location="${L:?}" --filter="name~/repositories/${SVC}\$" --format='value(name)'
}
secret_state() {
  list_state "${SECRET:?}" gcloud secrets list --project="${P:?}" \
    --filter="name:${SECRET}" --format='value(name)'
}

bucket_state() {
  list_state "${BUCKET_NAME:?}" gcloud storage buckets list --project="${P:?}" \
    --filter="name=${BUCKET_NAME}" --format='value(name)'
}

guard_resources() {
  # Hard stop: neither the service nor the repository nor the secret is already
  # there — unless it is the one this check created, recorded right after its
  # create.
  local st
  st=$(svc_state)
  if [ "$st" = unreadable ]; then
    stop "whether ${SVC} is already in ${P}/${L} cannot be read: the list failed (an expired session, a transient API error, or a permission this account does not hold), and a failed call is not the answer 'it is not there'. This guard is what stops this check adopting a service it did not create — one its Restore would then DELETE — so the run stops here rather than guessing. Run 'gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$st" = there ]; then
    [ -f "$SERVICE_MINE" ] \
      || stop "${SVC} already exists in ${P}/${L}, and it is not this check's. This check deletes what it creates and never adopts a service it did not create; delete it yourself if you know it is yours, or give this check a different name, then run again"
  fi
  local rp
  rp=$(repo_state)
  if [ "$rp" = unreadable ]; then
    stop "whether a repository named ${SVC} is already in ${P}/${L} cannot be read: the list failed, and a failed call is not the answer 'it is not there'. Run 'gcloud artifacts repositories list --project=${P} --location=${L}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$rp" = there ]; then
    [ -f "$REPO_MINE" ] \
      || stop "a repository named ${SVC} is already in ${P}/${L}, and it is not this check's: this check never adopts, writes into or deletes a repository it did not create. Look at it first (gcloud artifacts docker images list ${L}-docker.pkg.dev/${P}/${SVC} --include-tags --project=${P}) and delete it yourself if it holds nothing but an earlier attempt's image; otherwise give this check a different name. Then run again"
  fi
  local sc
  sc=$(secret_state)
  if [ "$sc" = unreadable ]; then
    stop "whether a secret named ${SECRET} is already in ${P} cannot be read: the list failed, and a failed call is not the answer 'it is not there'. Run 'gcloud secrets list --project=${P} --filter=name:${SECRET}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$sc" = there ]; then
    [ -f "$SECRET_MINE" ] \
      || stop "a secret named ${SECRET} is already in ${P}, and it is not this check's: this check never writes into or deletes a secret it did not create, and it will not overwrite one that may hold a real token. Delete it yourself if you know it is a leftover of an earlier attempt, then run again"
  fi
  local bk
  bk=$(bucket_state)
  if [ "$bk" = unreadable ]; then
    stop "whether a bucket named ${BUCKET_NAME} is already in ${P} cannot be read: the list failed, and a failed call is not the answer 'it is not there'. Run 'gcloud storage buckets list --project=${P} --filter=name=${BUCKET_NAME}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$bk" = there ]; then
    [ -f "$BUCKET_MINE" ] \
      || stop "a bucket named ${BUCKET_NAME} is already in ${P}, and it is not this check's: this check never adopts, writes into or deletes a bucket it did not create. Look at it first (gcloud storage ls ${BUCKET}) and delete it yourself (gcloud storage rm -r ${BUCKET}) if it holds nothing but an earlier attempt's staged source; then run again"
  fi
  fact "baseline" PASS "no service, repository, secret or bucket of this check's names was in ${P} when it started"
}

# ------------------------------------------------------------- credentials
# The identity token every request carries, minted FRESH FOR EACH STEP: one
# minted here expires within the hour, and the idle wait plus the slow push take
# longer than that — and every 401 that would cause reads exactly like a
# permission the front end is enforcing. The value is never printed and never on
# a command line: it goes into two 0600 files, the curl config the probes name
# with --config and the git config every git command names with
# GIT_CONFIG_GLOBAL.
mint_idt() {
  local tok
  tok=$(gcloud auth print-identity-token) || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  # user.name and user.email are in here because GIT_CONFIG_GLOBAL replaces the
  # owner's ~/.gitconfig: without them the commit this check makes in the clone
  # fails for want of an identity, and asking git for one would mean reading a
  # config this check deliberately does not read.
  (umask 077 && printf '[http]\n\textraHeader = Authorization: Bearer %s\n[user]\n\tname = check190\n\temail = check190@example.invalid\n' "$tok" >"$GIT_CONF") || return 1
  chmod 600 "$GIT_CONF" || return 1
  unset tok
  return 0
}

# run_git DIR ARGS...: every git command goes through here, so every one of them
# carries GIT_CONFIG_GLOBAL (the 0600 file with the identity token and no
# credential helper of the owner's) and GIT_TERMINAL_PROMPT=0 (a prompt would
# hang a headless run for ever rather than fail it).
run_git() {
  local dir=$1
  shift
  GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL="$GIT_CONF" GIT_CONFIG_NOSYSTEM=1 \
    git -C "$dir" -c credential.helper= "$@"
}

repo_name() { printf '%s' "$REPO"; }
branch_name() { printf 'chk190/%s' "$NAME"; }
git_url() { printf 'https://%s/%s.git' "$HOST" "$REPO"; }
# slow_push_url: the slow push's own route — the same repository, on the same
# host, through the proxy's stall prefix. slow_upstream_url: the URL the proxy
# forwards that request with once it has stripped the prefix, which is the same
# repository on the upstream the proxy's own default names.
slow_push_url() { printf 'https://%s%s%s.git' "$HOST" "$STALL_PREFIX" "$REPO"; }
slow_upstream_url() { printf '%s/%s.git' "$UPSTREAM" "$REPO"; }

# branch_refs: this run's branches on the sandbox repository, one line per ref,
# read with a FRESHLY minted identity token. Both halves matter in the Restore:
# the token GIT_CONF holds was minted before the slow push, and that push is
# held open for an hour, so by the time the Restore runs it has usually expired
# and every call answers 403 — which an `ls-remote` reads exactly like an absent
# branch. A listing that cannot be read therefore returns 1 and is never the
# answer "the branch is gone": read as one, it would leave a branch carrying a
# ${BIG_MIB} MiB blob on the sandbox repository while the block claimed it had
# been deleted.
branch_refs() {
  mint_idt || return 1
  local out rc=0
  out=$(run_git "$SCRATCH" ls-remote --heads "$(git_url)" "refs/heads/chk190/${NAME}*" 2>"$SCRATCH/ls-remote.err") || rc=$?
  [ "$rc" = 0 ] || return 1
  printf '%s\n' "$out" | tr -d '\r' | grep -v '^$' || true
  return 0
}

# fetch_stats: the proxy's own counters — requests, bytes in, bytes out — which
# is what the clone and push rows are measured in. The proxy logs one line per
# request and serves the aggregate at /chk190/stats; both carry counts and sizes
# only, never a header and never a body.
#
# Only a body of the proxy's own shape counts: an error page from the front end
# is not empty, and read as counters it would make every delta 0.
fetch_stats() {
  local body
  body=$(curl -sS --max-time "$CURL_SECONDS" --config "$AUTH_CONF" "https://${HOST}/chk190/stats" 2>/dev/null || true)
  case "$body" in requests=*) printf '%s' "$body" ;; esac
}

# stats_status: the HTTP status GET /chk190/stats is answered with, for the stop
# that says why the counters could not be read.
stats_status() {
  curl -sS -o /dev/null -w '%{http_code}' --max-time "$CURL_SECONDS" --config "$AUTH_CONF" "https://${HOST}/chk190/stats" 2>/dev/null || true
}

# fetch_health: the instance that answered and when its process started. This is
# the check's own service answering about itself — no upstream traffic, no token
# and no body of the owner's repository is involved.
fetch_health() {
  local body
  body=$(curl -sS --max-time "$CURL_SECONDS" --config "$AUTH_CONF" "https://${HOST}/chk190/health" 2>/dev/null || true)
  case "$body" in start=*) printf '%s' "$body" ;; esac
}

field_of() { # field_of TEXT NAME: the value of NAME= in TEXT, or nothing
  printf '%s' "$1" | tr ' ' '\n' | sed -n "s/^$2=//p" | head -n 1
}

# stats_delta BEFORE AFTER: "N requests, X bytes in, Y bytes out, between the two
# readings" — the numbers the clone and push rows are made of.
stats_delta() {
  local b_req b_in b_out a_req a_in a_out
  b_req=$(field_of "$1" requests)
  b_in=$(field_of "$1" in)
  b_out=$(field_of "$1" out)
  a_req=$(field_of "$2" requests)
  a_in=$(field_of "$2" in)
  a_out=$(field_of "$2" out)
  awk -v r1="${b_req:-0}" -v i1="${b_in:-0}" -v o1="${b_out:-0}" \
    -v r2="${a_req:-0}" -v i2="${a_in:-0}" -v o2="${a_out:-0}" \
    'BEGIN { printf "%d request(s), %d bytes in, %d bytes out", r2-r1, i2-i1, o2-o1 }'
}

# ------------------------------------------------------ the build and the deploy
write_dockerfile() {
  # A throwaway image for a throwaway service: the Go tag matches go.mod's own
  # `go` directive, the module is made in the image (the proxy is standard
  # library only and imports nothing outside it), and the binary is the
  # entrypoint. Nothing here is part of the repository's own images. The binary
  # is NOT /chk190: the deploy mounts the token's volume at the directory
  # /chk190 (--set-secrets=/chk190/token), and a volume over the entrypoint's
  # own path is a container that never starts — the revision is never ready and
  # the deploy reports failure after creating the service.
  cat >"$SCRATCH/build/Dockerfile" <<'DOCKER_EOF'
FROM golang:1.27-alpine
WORKDIR /src
COPY main.go ./
RUN go mod init chk190 && CGO_ENABLED=0 go build -o /usr/local/bin/chk190 .
ENV PORT=8080
ENTRYPOINT ["/usr/local/bin/chk190"]
DOCKER_EOF
}

# write_build_config: the build as a config file rather than --tag, because a
# build that names its service account must say where its logs go (the API
# refuses it otherwise), and --tag has no flag for that. CLOUD_LOGGING_ONLY is
# what Fugaro's own image builds use with the same account
# (internal/backend/gcp/build.go, images/derived/cloudbuild.yaml), which holds
# roles/logging.logWriter for it; a logs bucket would be one more thing the
# installation owns and this check would write into. The file is outside the
# build directory, so it is not part of the staged source.
write_build_config() {
  cat >"$SCRATCH/cloudbuild.yaml" <<CONFIG_EOF
steps:
  - name: gcr.io/cloud-builders/docker
    args: ["build", "-t", "${IMG:?}", "."]
images: ["${IMG:?}"]
options:
  logging: CLOUD_LOGGING_ONLY
CONFIG_EOF
}

# conditions_of KIND NAME: a Cloud Run service's or revision's status
# conditions (type, status, reason, message), on one line, redacted. A deploy
# that fails after the service exists says why only here: gcloud's own error is
# often just "revision is not ready".
conditions_of() {
  gcloud run "$1" describe "$2" --project="${P:?}" --region="${L:?}" --format='yaml(status.conditions)' 2>/dev/null \
    | grep -v -e 'lastTransitionTime:' -e '^status:' -e '^ *conditions:' \
    | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-600 || true
}

# deploy_conditions: the service's conditions and its latest revision's, for the
# deploy row. Reads only; a read that fails says so rather than ending the run.
deploy_conditions() {
  local svc rev revc
  svc=$(conditions_of services "${SVC:?}")
  rev=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" \
    --format='value(status.latestCreatedRevisionName)' 2>/dev/null | tr -d '\r' | head -n 1 || true)
  if [ -n "$rev" ]; then
    revc=$(conditions_of revisions "$rev")
  fi
  # The revision's own startup lines (the container's stderr), newest first: the
  # condition says the container did not start, its log says why. The proxy
  # logs counts, sizes and refusals only, and the lines are redacted anyway.
  local logs=""
  if [ -n "$rev" ]; then
    logs=$(gcloud logging read \
      "resource.type=\"cloud_run_revision\" AND resource.labels.revision_name=\"${rev}\"" \
      --project="${P:?}" --freshness=30m --limit=6 --format='value(textPayload)' 2>/dev/null \
      | grep -v '^$' | tr -d '\r' | tr '\n' ' ' | tr -s ' ' | redact | cut -c1-500 || true)
  fi
  printf 'service conditions: %s; revision %s conditions: %s; revision log: %s' \
    "${svc:-unreadable}" "${rev:-unreadable}" "${revc:-unreadable}" "${logs:-none read}"
}

step_build_deploy() {
  # The build account: this check never builds as the project's default Compute
  # account, which on fugaro-dev has had roles/editor removed. It reads the
  # installation's own build accounts and takes the first, or the one
  # CHECK190_BUILD_SA names. Its installation grants are on its own repository's
  # registry and nothing this check makes, so the two it needs here are granted
  # below, on this check's own registry and bucket only.
  if [ -z "$BUILD_SA" ]; then
    BUILD_SA=$(gcloud iam service-accounts list --project="${P:?}" \
      --filter='email~^fugaro-b-' --format='value(email)' 2>/dev/null | head -n 1 || true)
  fi
  if [ -z "$BUILD_SA" ]; then
    stop "no build service account was found: this check builds the proxy as one of the installation's own accounts (fugaro-b-*), never as the project's default Compute account, and 'gcloud iam service-accounts list --project=${P} --filter=email~^fugaro-b-' printed none. Name one with CHECK190_BUILD_SA=<account>@${P}.iam.gserviceaccount.com and run again. Nothing was created"
  fi
  say "building as ${BUILD_SA}: this run grants it roles/artifactregistry.writer on its own registry ${SVC} and roles/storage.objectViewer on its own bucket ${BUCKET}, and nothing else"

  # The token's own container, created from the file with --data-file: the value
  # is in no argv, no log line and no environment variable of the service. The
  # record is written BEFORE the create, so a run killed in between still leaves
  # the Restore something that says the secret is this check's.
  : >"$SECRET_MINE"
  if ! gcloud secrets create "${SECRET:?}" --project="${P:?}" --replication-policy=automatic \
    --data-file="${TOKEN_FILE:?}" --quiet >&2; then
    rm -f "$SECRET_MINE" || true
    stop "the secret container ${SECRET} could not be created in ${P}, so there is nothing for the proxy to attach. Nothing was deployed and nothing else was created"
  fi
  # Cloud Run reads the mounted secret as the service's runtime account, which
  # is the project's default Compute account unless the deploy names another;
  # the binding is on this check's own secret, so deleting the secret in the
  # Restore takes the binding with it.
  local number runtime_sa
  number=$(gcloud projects describe "${P:?}" --format='value(projectNumber)' 2>/dev/null || true)
  if [ -z "$number" ]; then
    stop "the project number of ${P} cannot be read, so the service's runtime account cannot be named and the mounted secret could not be read by the service. Nothing else was created; the secret container ${SECRET} is deleted by this run's Restore"
  fi
  runtime_sa="${number}-compute@developer.gserviceaccount.com"
  if ! gcloud secrets add-iam-policy-binding "${SECRET:?}" --project="${P:?}" \
    --member="serviceAccount:${runtime_sa}" --role=roles/secretmanager.secretAccessor --quiet >&2; then
    stop "the service's runtime account ${runtime_sa} could not be granted roles/secretmanager.secretAccessor on ${SECRET}, so the deployed service could not read the token it attaches. Nothing was deployed; the secret container is deleted by this run's Restore"
  fi

  # The registry repository the image tag needs. Same record-before-create rule
  # as the secret, and a create that FAILED takes its record back only when the
  # repository really is not there.
  local rp
  rp=$(repo_state)
  if [ "$rp" = there ]; then
    [ -f "$REPO_MINE" ] \
      || stop "a repository named ${SVC} is in ${P}/${L} and it is not this check's — this check creates its own and never adopts one. Nothing was written"
  else
    : >"$REPO_MINE"
    if ! gcloud artifacts repositories create "${SVC:?}" --project="${P:?}" --location="${L:?}" \
      --repository-format=docker \
      --description="throwaway: Check 190's image repository (fugaro-chk190), deleted by its own Restore" --quiet >&2; then
      local made
      made=$(repo_state)
      if [ "$made" = there ]; then
        stop "the repository create reported failure, but ${SVC} IS there now, so it is this check's: the record says so and this run's Restore deletes it. Nothing else ran"
      fi
      if [ "$made" = unreadable ]; then
        stop "the repository create reported failure and whether ${SVC} is there now cannot be read (the list failed), so the record of it is KEPT: a create that reported failure may still have made the repository, and the record is what makes this run's Restore delete it. Check 'gcloud artifacts repositories list --project=${P} --location=${L}' by hand; if the repository is there, delete it yourself. Nothing else ran"
      fi
      rm -f "$REPO_MINE" || true
      stop "the repository create failed and ${SVC} is not there, so this run made no repository: the record of it is removed again, and the secret container is deleted by this run's Restore"
    fi
  fi

  # The bucket the build's source is staged in: this check's own, made here and
  # deleted with its contents by the Restore, so nothing of this check's is
  # written into a bucket of the installation's (gs://<project>_cloudbuild is
  # never touched, and `gcloud builds submit` is told where to stage, so it
  # creates nothing either). Same record-before-create rule as the repository.
  local bk
  bk=$(bucket_state)
  if [ "$bk" = there ]; then
    [ -f "$BUCKET_MINE" ] \
      || stop "a bucket named ${BUCKET_NAME} is in ${P} and it is not this check's — this check creates its own staging bucket and never adopts one. Nothing was written into it; the secret container and the repository are deleted by this run's Restore"
  else
    : >"$BUCKET_MINE"
    if ! gcloud storage buckets create "${BUCKET:?}" --project="${P:?}" --location="${L:?}" \
      --uniform-bucket-level-access --quiet >&2; then
      local made_b
      made_b=$(bucket_state)
      if [ "$made_b" = there ]; then
        fact "staging-bucket" FAIL "the bucket create reported failure, but ${BUCKET} IS in ${P} now, so it is this check's and this run's Restore deletes it"
        stop "the bucket create reported failure, but ${BUCKET} IS there now, so it is this check's: the record says so and this run's Restore deletes it. Nothing was built"
      fi
      if [ "$made_b" = unreadable ]; then
        fact "staging-bucket" FAIL "the bucket create reported failure and whether ${BUCKET} is there now cannot be read, so the record of it is kept and this run's Restore tries to delete it"
        stop "the bucket create reported failure and whether ${BUCKET} is there now cannot be read (the list failed), so the record of it is KEPT and this run's Restore tries to delete it. Check 'gcloud storage buckets list --project=${P} --filter=name=${BUCKET_NAME}' by hand. Nothing was built"
      fi
      rm -f "$BUCKET_MINE" || true
      fact "staging-bucket" FAIL "${BUCKET} could not be created in ${P}/${L} (the name is global: another project may hold it, or YOUR account lacks storage.buckets.create, which an Owner has)"
      stop "the staging bucket ${BUCKET} could not be created and is not in ${P}, so nothing was built: the record of it is removed again, and the secret container and the repository are deleted by this run's Restore. A bucket name is global, so another project may hold it; otherwise YOUR account needs storage.buckets.create on ${P}"
    fi
  fi
  fact "staging-bucket" PASS "${BUCKET} (${L}, uniform bucket-level access) was created by this run to stage the build's source, and its Restore deletes it with everything in it; the installation's own gs://${P}_cloudbuild is not touched"

  # The build account's two grants, each on a resource this run created, so the
  # Restore's deletes of the registry and the bucket take the grants with them
  # and nothing of the installation's IAM changes.
  if ! gcloud artifacts repositories add-iam-policy-binding "${SVC:?}" --project="${P:?}" --location="${L:?}" \
    --member="serviceAccount:${BUILD_SA}" --role=roles/artifactregistry.writer --quiet >&2; then
    fact "grants" FAIL "${BUILD_SA} could not be granted roles/artifactregistry.writer on this run's registry ${SVC}"
    stop "${BUILD_SA} could not be granted roles/artifactregistry.writer on this run's own registry ${SVC}, so the build could not push its image. YOUR account needs artifactregistry.repositories.setIamPolicy on it (an Owner has it). Nothing was built; the registry, the bucket and the secret container are deleted by this run's Restore"
  fi
  if ! gcloud storage buckets add-iam-policy-binding "${BUCKET:?}" \
    --member="serviceAccount:${BUILD_SA}" --role=roles/storage.objectViewer --quiet >&2; then
    fact "grants" FAIL "${BUILD_SA} could not be granted roles/storage.objectViewer on this run's bucket ${BUCKET}"
    stop "${BUILD_SA} could not be granted roles/storage.objectViewer on this run's own bucket ${BUCKET}, so the build could not read its source. YOUR account needs storage.buckets.setIamPolicy on it (an Owner has it). Nothing was built; the registry, the bucket and the secret container are deleted by this run's Restore"
  fi
  fact "grants" PASS "${BUILD_SA}: roles/artifactregistry.writer on this run's registry ${SVC} and roles/storage.objectViewer on this run's bucket ${BUCKET}, nothing project-wide; both go with the registry and the bucket in the Restore"
  # IAM is eventually consistent: a build that starts the second a grant lands
  # can still be refused by it, and that refusal reads like a missing grant.
  owner "waiting ${GRANT_SECONDS} s for the two grants to take effect"
  sleep_bg "$GRANT_SECONDS"

  mkdir -p "$SCRATCH/build"
  cp "$PROXY_SRC/main.go" "$SCRATCH/build/main.go" || stop "the proxy source could not be copied into the scratch directory"
  write_dockerfile
  write_build_config
  say "building ${IMG} (gcloud builds submit --async, then read back until it ends). If it offers to CREATE anything — a bucket, an API, a grant — say NO, stop, and report it: those are the installation's and are not this check's to accept"
  BUILD_ATTEMPTED=yes
  # --async, then the build's own status read back: a synchronous submit streams
  # the log, and with CLOUD_LOGGING_ONLY that needs YOUR account to read Cloud
  # Logging, which says nothing about whether the build passed. The status read
  # needs only cloudbuild.builds.get.
  local build_rc=0
  BUILD_ID=$(gcloud builds submit --project="${P:?}" --config="$SCRATCH/cloudbuild.yaml" \
    --service-account="projects/${P}/serviceAccounts/${BUILD_SA}" \
    --gcs-source-staging-dir="${STAGING_PREFIX:?}" --async --format='value(id)' "$SCRATCH/build" \
    2>"$SCRATCH/build.err") || build_rc=$?
  BUILD_ID=$(printf '%s' "$BUILD_ID" | tr -d '\r' | grep -v '^$' | tail -n 1 || true)
  if [ "$build_rc" != 0 ] || [ -z "$BUILD_ID" ]; then
    fact "build" FAIL "gcloud builds submit (as ${BUILD_SA}) exited ${build_rc} and started no build: $(git_error_excerpt build). Likely causes, most likely first: YOUR account lacks roles/iam.serviceAccountUser on ${BUILD_SA}; the Cloud Build API is not enabled in ${P}; or the config at ${SCRATCH}/cloudbuild.yaml lost its 'options: logging: CLOUD_LOGGING_ONLY' (a build that names its service account is refused without one). Nothing was deployed, and the registry, the bucket with the staged source and the secret container are deleted by this run's Restore"
    stop "the build was not started (exit ${build_rc}), so nothing was deployed: the block's build row carries what gcloud said"
  fi
  local status="" detail="" waited=0 line
  while :; do
    line=$(gcloud builds describe "$BUILD_ID" --project="${P:?}" --format='value(status,failureInfo.detail)' 2>/dev/null || true)
    status=$(printf '%s' "$line" | tr -d '\r' | head -n 1 | awk -F'\t' '{print $1}')
    detail=$(printf '%s' "$line" | tr -d '\r' | head -n 1 | awk -F'\t' '{print $2}' | redact | cut -c1-300)
    case "$status" in
    QUEUED | PENDING | WORKING | "") ;;
    *) break ;;
    esac
    if [ "$waited" -ge "$BUILD_WAIT_SECONDS" ]; then
      status="still ${status:-unreadable}"
      break
    fi
    sleep_bg 10
    waited=$((waited + 10))
  done
  if [ "$status" != SUCCESS ]; then
    fact "build" FAIL "build ${BUILD_ID} (as ${BUILD_SA}) ended ${status} after about ${waited} s: ${detail:-no failure detail}. Its log is in Cloud Logging (gcloud builds log ${BUILD_ID} --project=${P}, which needs YOUR account to read logs). Likely causes, most likely first: a grant above had not taken effect yet (run again); the Go tag in the Dockerfile is not a tag that registry has; or the build account cannot reach the registry or the bucket. Nothing was deployed, and the registry, the bucket and the secret container are deleted by this run's Restore"
    stop "the build did not succeed (${status}), so nothing was deployed: the block's build row carries what Cloud Build said"
  fi
  fact "build" PASS "build ${BUILD_ID} as ${BUILD_SA} succeeded in about ${waited} s (read back by its status, not its log): ${IMG}"

  # Same rule as the secret's record above: a deploy that is interrupted is the
  # ordinary way to be left with a service that bills, so the record that says
  # the service is this check's is on disk BEFORE the deploy starts, and a
  # deploy that FAILED takes it back.
  : >"$SERVICE_MINE"
  # The deploy's stderr is kept so the row can carry what gcloud said, and is
  # replayed to the terminal afterwards so nothing the owner used to see is lost.
  local deploy_rc=0
  gcloud run deploy "${SVC:?}" "${DEPLOY_FLAGS[@]}" --quiet 2>"$SCRATCH/deploy.err" >&2 || deploy_rc=$?
  cat "$SCRATCH/deploy.err" >&2 || true
  if [ "$deploy_rc" != 0 ]; then
    local deployed
    deployed=$(svc_state)
    if [ "$deployed" = there ]; then
      fact "deploy" FAIL "gcloud run deploy exited ${deploy_rc} after creating ${SVC}: $(git_error_excerpt deploy). $(deploy_conditions). Likely causes, most likely first: the container did not start (a volume mounted over the entrypoint's path, or the token file not readable at CHK190_TOKEN_FILE), so the revision never became ready; the runtime account cannot read the secret; or the container did not listen on \$PORT"
      stop "the deploy reported failure, but ${SVC} IS there now, so it is this check's: the record says so and this run's Restore deletes it, with the image, the repository and the secret. The block's deploy row carries what gcloud and the service's conditions said"
    fi
    if [ "$deployed" = unreadable ]; then
      stop "the deploy reported failure and whether ${SVC} is there now cannot be read (the list failed), so the record of it is KEPT: a deploy that reported failure may still be serving and being billed, and the record is what makes this run's Restore delete it. Check 'gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC}' by hand; if the service is there, delete it yourself. Nothing else ran"
    fi
    rm -f "$SERVICE_MINE" || true
    fact "deploy" FAIL "gcloud run deploy exited ${deploy_rc} and ${SVC} is not there: $(git_error_excerpt deploy)"
    stop "the deploy failed and ${SVC} is not there, so nothing of this check's is running: the record is removed again, and the image, the repository, the staging bucket and the secret container are deleted by this run's Restore"
  fi
  local url_err=$SCRATCH/url.err
  URL=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" --format='value(status.url)' 2>"$url_err") \
    || stop "the deployed service's URL cannot be read: 'gcloud run services describe ${SVC} --format=value(status.url)' failed with $(tail -n 1 "$url_err" | tr -d '\r' | tr '\n' ' '). The service IS deployed and IS this check's — the record says so, and this run's Restore deletes it, with the image, the repository and the secret. Nothing else ran"
  [ -n "$URL" ] || stop "the service has no URL"
  HOST=${URL#https://}
  HOST=${HOST%%/*}
  local cfg
  cfg=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" \
    --format='value(spec.template.spec.containers[0].image,spec.template.spec.timeoutSeconds)' 2>/dev/null | tr '\n' ' ' || true)
  fact "deploy" PASS "${SVC} deployed in ${L} from ${IMG}"
  fact "service-config" PASS "image ${cfg:-$IMG}; --timeout=3600 --min-instances=0 --max-instances=1; the token is mounted as the file /chk190/token and is in no environment variable; URL https://${HOST}"

  # The IAM assertion the plan requires BEFORE any request is sent: the
  # service's own policy must name no public member. A throwaway service that
  # answered unauthenticated calls would put the sandbox repository's token
  # behind a public URL, so this is a stop, not a warning.
  local pol pol_err=$SCRATCH/iam.err
  pol=$(gcloud run services get-iam-policy "${SVC:?}" --project="${P:?}" --region="${L:?}" --format=json 2>"$pol_err") \
    || stop "the deployed service's IAM policy cannot be read: 'gcloud run services get-iam-policy ${SVC} --format=json' failed with $(tail -n 1 "$pol_err" | tr -d '\r' | tr '\n' ' '). No request has been sent, and the service IS this check's — this run's Restore deletes it, with the image, the repository and the secret"
  case "$pol" in
  *allUsers* | *allAuthenticatedUsers*)
    stop "the deployed service's IAM policy names a public member (allUsers or allAuthenticatedUsers), so the proxy is reachable by anyone and the token it attaches is behind a public URL. No request was sent. The service, the image, the repository and the secret are deleted by this run's Restore; report this, and check 'gcloud run services get-iam-policy ${SVC} --project=${P} --region=${L}' by hand"
    ;;
  esac
  fact "iam-policy" PASS "get-iam-policy on ${SVC} names no allUsers and no allAuthenticatedUsers member, read BEFORE any request was sent: the service requires IAM"

  # An unauthenticated call must be refused: that is the second half of the same
  # assertion, and it is the register's own wording. A project-level allUsers or
  # run.invoker binding does NOT appear in the service's own get-iam-policy, so
  # this probe is the only thing that sees it — and the run stops here rather
  # than cloning, pushing a ${BIG_MIB} MiB pack and holding a push open for an
  # hour behind a public URL with the sandbox repository's token attached.
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time "$CURL_SECONDS" "${URL}/chk190/health" 2>/dev/null || true)
  case "$code" in
  401 | 403) fact "unauthenticated-request" PASS "an unauthenticated GET /chk190/health was refused with ${code}" ;;
  *)
    fact "unauthenticated-request" FAIL "an unauthenticated GET /chk190/health answered ${code:-nothing}, so the service does not refuse unauthenticated calls"
    stop "an unauthenticated GET /chk190/health answered ${code:-nothing}: the service answers unauthenticated calls, so the proxy and the sandbox repository's token it attaches are behind a public URL. The service's own get-iam-policy names no public member, so this is a project-level binding (allUsers or allAuthenticatedUsers with roles/run.invoker) — the one thing only this probe sees. Nothing was cloned and nothing was pushed, and the run stops here rather than spending the next two hours behind it: the service, the image, the repository and the secret container are deleted by this run's Restore, and the project's IAM is yours to fix"
    ;;
  esac
}

# ------------------------------------------------------------------- the clones
step_clone() {
  mint_idt || stop "no identity token can be minted here, so no request can be authorised"
  local before after t0 t1
  before=$(fetch_stats)
  if [ -z "$before" ]; then
    local code
    code=$(stats_status)
    stop "the proxy's own counters cannot be read: GET /chk190/stats answered HTTP ${code:-nothing} with a freshly minted identity token, so a clone's request count and bytes could not be measured and no git request was sent. A 401 is the front end not accepting the identity token ('gcloud auth print-identity-token' for the account gcloud is using); a 403 is that account lacking roles/run.invoker on ${SVC}; 000 is no answer at all. The service IS this check's; this run's Restore deletes it"
  fi
  t0=$(date +%s)
  if ! run_git_traced clone-full "$SCRATCH" clone --quiet "$(git_url)" "$SCRATCH/full" 2>"$SCRATCH/clone-full.err"; then
    fact "clone-full" FAIL "a full clone through the proxy failed: $(git_failure_detail clone-full)"
    stop "the full clone failed, so the proxy does not serve a fetch end to end: the block's clone-full row carries what git said, the HTTP status and headers it saw, who refused, and the proxy's own log. Nothing else ran"
  fi
  t1=$(date +%s)
  after=$(fetch_stats)
  fact "clone-full" PASS "a full clone through the proxy took $((t1 - t0)) s and cost $(stats_delta "$before" "$after")"

  before=$(fetch_stats)
  t0=$(date +%s)
  if ! run_git_traced clone-blobless "$SCRATCH" clone --quiet --filter=blob:none "$(git_url)" "$SCRATCH/blobless" 2>"$SCRATCH/clone-blobless.err"; then
    fact "clone-blobless" FAIL "a blobless clone through the proxy failed: $(git_failure_detail clone-blobless)"
    stop "the blobless clone failed. The full clone's row is in the block"
  fi
  t1=$(date +%s)
  after=$(fetch_stats)
  # The point of this row (F14): a blobless clone is cheaper in bytes and dearer
  # in requests, and it is the requests — each one a Cloud Run request with an
  # identity check and an audit line — that the design pays for.
  fact "clone-blobless" PASS "a blobless clone (--filter=blob:none) took $((t1 - t0)) s and cost $(stats_delta "$before" "$after")"
}

# -------------------------------------------------------------------- the pushes
step_push_small() {
  local dir=$SCRATCH/full
  mint_idt || stop "no identity token can be minted here, so the push cannot be authorised"
  run_git "$dir" checkout --quiet -b "$(branch_name)" || stop "the branch chk190/${NAME} cannot be made in the clone"
  printf 'check 190: a small push through the proxy\n' >"$dir/chk190-small.txt"
  run_git "$dir" add chk190-small.txt || stop "the small file could not be staged"
  run_git "$dir" commit --quiet -m "check 190: a small push through the proxy" || stop "the small commit could not be made"
  # The record that a branch of this check's is on the sandbox repository: it is
  # written before the push, so a push that fails half way is still cleaned up.
  : >"$PUSHED"
  if run_git_traced push-small "$dir" push --quiet origin "$(branch_name)" 2>"$SCRATCH/push-small.err"; then
    fact "push-small" PASS "a small push of branch chk190/${NAME} through the proxy succeeded"
  else
    fact "push-small" FAIL "a small push through the proxy failed: $(git_failure_detail push-small)"
    stop "the small push failed, so the proxy does not serve a push at all: nothing about the large push could be measured. The branch is deleted by this run's Restore"
  fi
}

step_push_big() {
  local dir=$SCRATCH/full bytes t0 t1 rc=0 before after
  mint_idt || stop "no identity token can be minted here, so the push cannot be authorised"
  bytes=$((BIG_MIB * 1048576))
  # Incompressible on purpose: a pack of zeros would be a few hundred bytes on
  # the wire whatever its size on disk, and the limit this row measures is a
  # limit on the bytes sent.
  head -c "$bytes" /dev/urandom >"$dir/chk190-big.bin" \
    || stop "the ${BIG_MIB} MiB file for the large push could not be written"
  run_git "$dir" add chk190-big.bin || stop "the large file could not be staged"
  run_git "$dir" commit --quiet -m "check 190: a push above 32 MiB" || stop "the large commit could not be made"
  before=$(fetch_stats)
  say "pushing a ${BIG_MIB} MiB pack on HTTP/1.1 (git uses a chunked body above http.postBuffer, which is 1 MiB by default): this is the row the whole check exists for"
  t0=$(date +%s)
  # HTTP/1.1 on purpose: the question is whether Cloud Run's front end accepts a
  # chunked body above 32 MiB on it, and HTTP/2 would answer a different one.
  run_git_traced push-big "$dir" -c http.version=HTTP/1.1 push --quiet origin "$(branch_name)" 2>"$SCRATCH/push-big.err" || rc=$?
  t1=$(date +%s)
  after=$(fetch_stats)
  local answer
  answer=$(git_error_excerpt push-big)
  if [ "$rc" = 0 ]; then
    # The gate B0.5 names: with this row PASS, S1 is built on HTTP/1.1.
    fact "push-big-http1" PASS "a ${BIG_MIB} MiB pack pushed on HTTP/1.1 in $((t1 - t0)) s; the proxy received $(stats_delta "$before" "$after"). The front end accepted a chunked body above 32 MiB, so the proxy path stays HTTP/1.1"
  else
    answer=$(git_failure_detail push-big)
    fact "push-big-http1" FAIL "a ${BIG_MIB} MiB pack on HTTP/1.1 was refused after $((t1 - t0)) s; git exited ${rc} and said: ${answer}. The proxy received $(stats_delta "$before" "$after") — if the bytes it received are far below ${BIG_MIB} MiB, the front end refused the body before the container saw it. This is the gate: S1 must serve h2c end to end (B1.0, B1.5) before the proxy is built on HTTP/1.1"
  fi
}

# ---------------------------------------------------------------- the cold start
sleep_bg() { # sleep_bg SECONDS: interruptible, so a Ctrl-C during a 35-minute
  # wait ends the run at once rather than after the sleep.
  sleep "$1" &
  WAIT_PID=$!
  wait "$WAIT_PID" || true
  WAIT_PID=""
}

step_cold_start() {
  mint_idt || stop "no identity token can be minted here, so the cold-start probe cannot be authorised"
  local before_h after_h t0 t1
  before_h=$(fetch_health)
  [ -n "$before_h" ] || stop "the proxy's health cannot be read (GET /chk190/health answered nothing), so a cold start could not be told from a warm answer. The service IS this check's; this run's Restore deletes it"
  owner "idle wait: ${IDLE_SECONDS} s — the instance has to be reclaimed before the next request can be a cold start"
  sleep_bg "$IDLE_SECONDS"
  # Minted again after the wait: the one minted before it lasts an hour, the
  # wait can be set longer than that, and a refused request is not a cold start.
  # Minting talks to Google's token endpoint, not to the service, so it does
  # not wake the instance.
  mint_idt || stop "no identity token can be minted here after the idle wait, so the cold-start probe cannot be authorised"
  # The first request after idle is a GIT request, because that is the cold start
  # the design pays for (U8): the service is the first hop of every fetch and push.
  local ls_rc=0
  t0=$(date +%s)
  run_git_traced lsremote "$SCRATCH" ls-remote --heads "$(git_url)" >"$SCRATCH/lsremote.out" 2>"$SCRATCH/lsremote.err" || ls_rc=$?
  t1=$(date +%s)
  if [ "$ls_rc" != 0 ]; then
    fact "cold-start" FAIL "the first git request after ${IDLE_SECONDS} s idle failed after $((t1 - t0)) s, so its time is no cold-start figure: $(git_failure_detail lsremote)"
    return 0
  fi
  after_h=$(fetch_health)
  [ -n "$after_h" ] || stop "the proxy's health cannot be read after the idle wait"
  local s1 s2
  s1=$(field_of "$before_h" start)
  s2=$(field_of "$after_h" start)
  if [ -n "$s1" ] && [ -n "$s2" ] && [ "$s1" != "$s2" ]; then
    fact "cold-start" PASS "the first git request after ${IDLE_SECONDS} s idle took $((t1 - t0)) s and was answered by a NEW instance (start ${s1} → ${s2}), so the figure is a cold start"
  else
    fact "cold-start" FAIL "the first git request after ${IDLE_SECONDS} s idle took $((t1 - t0)) s but the SAME instance answered it (start ${s1:-nothing} → ${s2:-nothing}), so this is a warm figure and not a cold start. The service scales to zero (D501), so either the idle wait is shorter than the platform's own, or an instance was kept alive; re-run with a longer CHECK190_IDLE_SECONDS"
  fi
}

# ----------------------------------------------------------------- the slow push
step_slow_push() {
  local dir=$SCRATCH/full url t0 t1 rc=0
  mint_idt || stop "no identity token can be minted here, so the slow push cannot be authorised"
  # Through the proxy's own stall prefix: the proxy holds the receive-pack POST
  # open for CHECK190_SLOW_SECONDS before it reads it, which is what makes this
  # a slow push rather than a large one. The GET of info/refs is not stalled, so
  # the request that hangs is the push itself. The URL is built out of the one
  # STALL_PREFIX the deploy line gives the proxy, so the route this asks through
  # and the route the proxy answers on are the same string.
  url=$(slow_push_url)
  printf 'check 190: a push the front end has to time out\n' >"$dir/chk190-slow.txt"
  run_git "$dir" add chk190-slow.txt || stop "the slow file could not be staged"
  run_git "$dir" commit --quiet -m "check 190: a slow push" || stop "the slow commit could not be made"
  say "slow push: ${url}, where the proxy holds this one receive-pack open for ${SLOW_SECONDS} s (the front end's own limit is 3600 s) and strips ${STALL_PREFIX} before it forwards, so Bitbucket sees $(slow_upstream_url)"
  t0=$(date +%s)
  # http.lowSpeedTime is this side's own cap, set beyond the stall so the front
  # end's answer is what ends the request when the front end cuts it: without it
  # a run that the front end never cuts would hang for ever.
  run_git_traced push-slow "$dir" -c http.version=HTTP/1.1 -c http.lowSpeedLimit=1 -c "http.lowSpeedTime=$((SLOW_SECONDS + 120))" \
    push --quiet "$url" "HEAD:refs/heads/$(branch_name)-slow" 2>"$SCRATCH/push-slow.err" || rc=$?
  t1=$(date +%s)
  local took=$((t1 - t0)) answer reading
  # The status and who sent it are the row's point: a 504 from the front end
  # (no X-Chk190-Upstream) is the limit, a 401 is a refusal that only looks
  # like one.
  answer="$(git_error_excerpt push-slow); HTTP: $(http_excerpt push-slow)"
  reading=$(http_reading push-slow)
  if [ "$rc" = 0 ]; then
    fact "timeout-3600" FAIL "a receive-pack held open for ${SLOW_SECONDS} s COMPLETED in ${took} s, so the front end did not end the request: the 3600 s timeout the proxy's design assumes is not enforced on this path"
  elif [ "$took" -ge 3400 ] && [ "$took" -le 3720 ]; then
    fact "timeout-3600" PASS "the hold was measured: a receive-pack held open by the proxy was ended by the front end after ${took} s (the service's own --timeout is 3600); git exited ${rc} and said: ${answer}"
  elif [ "$took" -lt 3400 ]; then
    # The row exists to measure the front end's 3600 s limit. A request that
    # came back this early was not held at all, so this run has NO figure for
    # it: 0 s is not a measurement of a hold, and a row that read as one would
    # let a broken slow route come back looking like an answer. The live run of
    # 2026-10-30 did exactly that — the proxy answered 502 after 0 s, which is
    # what a path that loses its separator with the prefix looks like.
    fact "timeout-3600" INFO "not measured: no hold happened, so this run says nothing about the 3600 s front-end limit — the request was answered after ${took} s. What answered it: ${reading}. git exited ${rc} and said: ${answer}. Read the route before the figure: this check sends the slow push to ${url} and the proxy strips ${STALL_PREFIX} before it forwards, so an answer that names the proxy itself, or a status with no X-Chk190-Upstream and no repository path in git's own trace, means the slow request never reached Bitbucket"
  else
    fact "timeout-3600" INFO "the hold was measured at ${took} s, past the front end's 3600 s limit (3400–3720 s counts as the limit): this side's own cap is $((SLOW_SECONDS + 120)) s, so a figure at that cap means the front end did not cut the request. git exited ${rc} and said: ${answer}"
  fi
}

# print_plan: what a real run would run, in order, and what its Restore would
# delete. Printed by --dry-run; nothing here is run.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, writes] gcloud secrets create ${SECRET} --project=${P} --data-file=<CHECK190_TOKEN_FILE>"
  say "  [network, writes] gcloud secrets add-iam-policy-binding ${SECRET} --project=${P} --member=serviceAccount:<project-number>-compute@developer.gserviceaccount.com --role=roles/secretmanager.secretAccessor"
  say "  [network, writes] gcloud artifacts repositories create ${SVC} --project=${P} --location=${L} --repository-format=docker"
  say "  [network, writes] gcloud storage buckets create ${BUCKET} --project=${P} --location=${L} --uniform-bucket-level-access"
  say "  [network, writes] gcloud artifacts repositories add-iam-policy-binding ${SVC} --project=${P} --location=${L} --member=serviceAccount:<build account> --role=roles/artifactregistry.writer"
  say "  [network, writes] gcloud storage buckets add-iam-policy-binding ${BUCKET} --member=serviceAccount:<build account> --role=roles/storage.objectViewer"
  say "  [local] a ${GRANT_SECONDS} s wait for the two grants to take effect"
  say "  [network, writes] gcloud builds submit --project=${P} --config=${SCRATCH}/cloudbuild.yaml --service-account=<build account> --gcs-source-staging-dir=${STAGING_PREFIX} --async ${SCRATCH}/build (the config: one docker build of ${IMG}, images: [${IMG}], options.logging CLOUD_LOGGING_ONLY)"
  say "  [network] gcloud builds describe <build id> --project=${P}, every 10 s until the build ends (its status, not its log: no Cloud Logging read is needed)"
  say "  [network, writes] gcloud run deploy ${SVC} ${DEPLOY_FLAGS[*]}"
  say "  [network, a failed deploy only] gcloud run services describe ${SVC}, gcloud run revisions describe <its latest revision> and gcloud logging read of that revision's lines (reads, for the deploy row)"
  say "  [network, a failed git step only] gcloud logging read of ${SVC}'s own chk190 lines, after a ${LOG_WAIT_SECONDS} s wait for them to arrive (a read, for that step's row; best effort)"
  say "  [network] the probes: identity-token requests with curl to the service's own /chk190/stats and /chk190/health, and git clone (full and blobless), git push (small and above ${BIG_MIB} MiB on HTTP/1.1) and the slow push, all through the proxy to bitbucket.org"
  say "  [network] the slow push itself: POST https://<service host>${STALL_PREFIX}$(repo_name).git/git-receive-pack, held open by the proxy for ${SLOW_SECONDS} s and then forwarded to $(slow_upstream_url)/git-receive-pack (the GET of info/refs on the same route is not held, so the request the front end times out is the push)"
  say "  [network, writes] one branch on $(repo_name): chk190/${NAME}, and its -slow sibling if that push ever completes"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, deletes] git push --delete the branches chk190/${NAME} and chk190/${NAME}-slow"
  say "  [network, writes] gcloud builds cancel <build id> --project=${P} (refused, harmlessly, when the build already ended)"
  say "  [network, deletes] gcloud run services delete ${SVC} --project=${P} --region=${L}"
  say "  [network, deletes] gcloud artifacts docker images delete ${IMG} --project=${P} --delete-tags"
  say "  [network, deletes] gcloud artifacts repositories delete ${SVC} --project=${P} --location=${L}"
  say "  [network, deletes] gcloud secrets delete ${SECRET} --project=${P}"
  say "  [network, deletes] gcloud storage rm -r ${BUCKET} (the bucket and the source staged in it)"
  say "  [network] the four lists that read back the service, the repository, the secret and the bucket as gone"
  say "What needed the network and your credentials in THIS dry run: fugaro doctor, the lists that ask whether the service, the repository, the secret and the bucket are there, and the build-account list (all reads). Local only: the scratch directory and the Dockerfile in it."
}

main() {
  need_tools
  scratch_up
  guard_xtrace
  guard_project
  guard_inputs
  guard_deploy_is_iam_only
  guard_slow_route
  guard_proxy_source
  guard_resources

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact "dry-run" PASS "guards and the baseline only; the commands a real run would run and its cleanup commands were printed, not run"
    return 0
  fi

  step_build_deploy
  step_clone
  step_push_small
  step_push_big
  step_cold_start
  step_slow_push
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region repo token-file deploy-iam-only proxy-source baseline scratch
  staging-bucket grants build deploy service-config iam-policy unauthenticated-request clone-full clone-blobless push-small
  push-big-http1 cold-start timeout-3600 branch-deleted bucket-deleted restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 190 RESULT\n' >&3
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
  printf 'CHECK 190 RESULT END (exit %s)\n' "$status" >&3
}

# interrupted: end the idle wait FIRST, then leave. It is a background job this
# shell is waiting on, and the trap runs as soon as the signal arrives because
# `wait` is interruptible — killing it here is what keeps the Restore from being
# held behind a sleep that has 30 minutes left. The rest is the same path as
# before: exit 130, and the EXIT trap runs the Restore and prints the block.
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
# second Ctrl-C during the Restore (or a closing terminal's SIGHUP) would
# otherwise kill this shell mid-cleanup: the service can be deleted while the
# image, the repository and the secret are not, the counts are never read, and
# the RESULT block that says what was left behind is never printed.
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
