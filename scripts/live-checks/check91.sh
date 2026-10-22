#!/usr/bin/env bash
# Check 91 — Cloud Run streaming, as ONE script YOU run.
#
# It settles three of U5's four numbers for the credentials service on the
# sandbox installation: Cloud Run's request timeout (the longest stream that
# completes), its request-body limit on HTTP/1.1, whether an SSE response
# reaches the caller unbuffered through the front end, and the cold start. The
# fourth — the limit on an end-to-end HTTP/2 request — is NOT settled here and
# this script does not claim to settle it (the container serves HTTP/1.1, so
# its HTTP/2 row measures the front end above the container). The register's
# section is docs/gcp-live-checklist.md, "Check 91"; the step-by-step text it
# used to carry is docs/credentials-service/check91-steps.md.
#
# Usage:      bash scripts/live-checks/check91.sh
#             bash scripts/live-checks/check91.sh --dry-run   (guards and files
#             only: nothing is created, built, deployed or deleted; it prints
#             the commands a real run would run and the cleanup commands)
#             CHECK91_BUILD_SA=<account>@fugaro-dev.iam.gserviceaccount.com  (optional:
#             names the build service account when the installation has several)
# Needs:      bash, mktemp, gcloud, curl, fugaro, and a curl built with HTTP/2.
#             Nothing is installed and nothing is fetched; if one of those is
#             missing the script stops with one line naming it.
# Where:      installation `fugaro` ONLY — project fugaro-dev, region us-east5.
#             The project is HARD-CODED and `fugaro doctor` must answer that
#             this is the fugaro-dev installation. Never belong, never another
#             project: you must not run it against any other installation.
# Build as:   NOT the project's default Compute service account (which on
#             fugaro-dev has had roles/editor removed and holds nothing on the
#             staging bucket or on Artifact Registry). The build runs as one of
#             the installation's own build service accounts (fugaro-b-*), read
#             from the project before anything is created; BEFORE any write the
#             script prints which account that is and the roles it needs, reads
#             the project's and the bucket's IAM policy, and stops with each
#             missing grant as a copy-paste command for YOU to run. The script
#             never grants IAM itself.
# Creates:    one throwaway Cloud Run service (fugaro-chk91), the Artifact
#             Registry repository its image tag needs, and the image. The
#             Restore deletes all three and asserts three counts read 0. A
#             repository or service of that name that was already there is a
#             hard stop: this check never adopts, writes into or deletes what
#             it did not create.
# Costs:      wall clock, not money: around two hours at the terminal. The cold
#             start waits 35 minutes (CHECK91_IDLE_SECONDS), the long stream
#             runs up to 59 minutes, and the body table sends five sizes on two
#             protocols. An idle Cloud Run instance and one build are billed by
#             the project while it runs.
# Runs:       by YOU. No agent runs any step of this, and no agent has: what it
#             measures is a live project.
# Cleanup:    always: the service, the image and the repository it created are
#             deleted by the Restore, which runs from the exit trap too — so a
#             Ctrl-C in the middle does not leave a service running. The three
#             counts are checked before the scratch directory goes, and the
#             directory is KEPT when a count is not 0, with a message saying
#             where it is and what to delete by hand.
# Output:     progress on stderr, ONE block on stdout headed CHECK 91 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one did not (or a hard stop ended the run).
#
# CHECK91_IDLE_SECONDS, CHECK91_BODY_SIZES, CHECK91_PROBE_SECONDS,
# CHECK91_LONG_SECONDS, CHECK91_CURL_SECONDS and CHECK91_DRY_RUN exist for this
# script's own test: they shorten waits and sizes, and none of them can point
# the check at another project, another region or another service. (The long
# stream's LENGTH is the relay's own SSE_MINUTES, which the check sets to 59
# with the service update it runs for that step; CHECK91_LONG_SECONDS is only
# the curl-side cap that keeps that one request from outliving the run —
# --timeout is 3600. CHECK91_CURL_SECONDS is the cap every OTHER request
# carries: a request with no cap of its own is waited on for as long as the
# front end keeps the socket open, and a Ctrl-C would then be the only thing
# that ends it.)
#
# The identity token is never on a command line: it is minted fresh for each
# request and written into an 0600 curl config file inside the scratch
# directory, which the request names with --config. `ps` shows every process's
# argv — to every user on the machine, for as long as the process lives — and
# the hour-long stream would hold a token there for an hour. The file is
# removed with the scratch directory.
#
# The build's own litter: `gcloud builds submit` uploads the source as a tar
# into a staging bucket and leaves it there. This run gives that upload a
# PREFIX OF ITS OWN inside the installation's staging bucket and the Restore
# deletes exactly that prefix, so what this check put in the owner's bucket is
# removed by the check rather than left to a bucket lifecycle rule. That
# bucket has to be there and readable BEFORE the build: `gcloud builds submit`
# CREATES gs://<project>_cloudbuild when it is missing, and a bucket is the
# installation's (Terraform-managed), not this check's — this run would have
# no record of having made it and no Restore that could remove it. So an
# unreadable staging bucket is a STOP before the build, never a fallback to
# gcloud's default staging directory (which would also leave the tarball
# unnameable, for a lifecycle rule to sweep away).
#
# The HTTP/2 capability of this machine's curl is checked BEFORE anything is
# created or spent: the body table's second row is meaningless without it, and
# finding that out after the cold-start wait and the hour-long stream would
# waste them.
set -euo pipefail

# No xtrace, whatever the shell it was started from had. `bash -x
# check91.sh` is a natural thing to reach for on a two-hour script — and this
# script's commands carry the identity token: `tok=$(gcloud auth
# print-identity-token)` and the printf that writes it into the curl config
# file would both be traced in full, printing the bearer token twice to stderr,
# into a log the owner would then paste back. So the trace is turned OFF here,
# BASH_XTRACEFD is unset so a trace redirected to a file this script knows
# nothing about cannot be reopened, and guard_xtrace REFUSES the run when this
# shell arrived with tracing on — the case where the flag sits in
# SHELLOPTS/BASHOPTS and every child would start tracing on its own.
# Check 90 has the same guard for the same reason; its header spells the three
# parts out.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
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
SVC=fugaro-chk91
IMG="${L}-docker.pkg.dev/${P}/${SVC}/${SVC}:chk91"

# Every request carries a cap of its own (--max-time), including the ones that
# are not stream probes: a curl with no cap is waited on for as long as the
# front end keeps the socket open, so an hour-long run can end with a request
# that outlives it and a Ctrl-C that has nothing to end. The stream probes have
# their own (CHECK91_PROBE_SECONDS, CHECK91_LONG_SECONDS); these are the two
# the rest use.
CURL_SECONDS=${CHECK91_CURL_SECONDS:-300}
# The cold-start probe IS the measurement, so it is given more room than an
# ordinary request: a cold start is seconds, but the figure is worthless if
# this check's own cap is what ended it.
COLD_PROBE_SECONDS=600

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

DRY_RUN=${CHECK91_DRY_RUN:-}
case "${1:-}" in
--dry-run) DRY_RUN=1 ;;
"") ;;
*) die "usage: $PROG [--dry-run]" ;;
esac

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 91 needs '$1' on PATH and it is not there"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 91 needs bash"
  need mktemp
  need gcloud
  need curl
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk91-mine"
REPO_EXISTED=""
REPO_MINE=""
SERVICE_MINE=""
URL=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=""
PRINTED=""
# The 0600 curl config file each request's credential is written into, the two
# background jobs an interrupt has to end, and the prefix this run gave the
# build's staged source (see step_build_deploy and the Restore).
AUTH_CONF=""
# Whether the credential file above was actually gone when the run ended. The
# Restore section of the appendix documents this row: `restore-token-file` is
# PASS only when the file is not on disk, because the scratch directory it lives
# in is deliberately KEPT when a count is not 0.
AUTH_CONF_REMOVED=yes
WAIT_PID=""
# The per-run prefix this run staged the build's source under and what the
# build's command line was told to stage the source into (the two go together:
# a build is only ever started when the prefix could be given), and the flag
# that says a build was attempted at all — the Restore removes the staged
# source only then.
STAGING_PREFIX=""
STAGING_ARG=""
BUILD_ATTEMPTED=no
# The service account the build runs as, resolved read-only by
# guard_build_identity before anything is created.
BUILD_SA=""
BUILD_CONFIG=
# The cleanup flag finish sets: a second Ctrl-C while the Restore is running
# must not kill the shell that is running it (see finish).
CLEANUP_RUNNING=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk91.XXXXXXXX") || die "Check 91 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  REPO_EXISTED="$SCRATCH/repo-existed"
  REPO_MINE="$SCRATCH/repo-mine"
  SERVICE_MINE="$SCRATCH/service-mine"
  AUTH_CONF="$SCRATCH/idt.conf"
  BUILD_CONFIG="$SCRATCH/cloudbuild.yaml"
  # Nameable by this run alone: the scratch directory's own name is unique and
  # is deleted with it, so two runs of this check never share a staging prefix.
  STAGING_PREFIX="gs://${P}_cloudbuild/chk91-source/${SCRATCH##*/}/"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 91 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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
  # Guarded: this runs from the exit trap, where a `rm` that fails (a read-only parent, a
  # file the owner's own user cannot unlink) would end the shell under `set -e` before the
  # RESULT block is printed. A directory that is still there is a FACT, and the block says
  # so — it is not a reason to lose the rest of the block.
  rm -rf "$SCRATCH" || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The Restore: step 7 as a function, because a run that stops in the middle has
# to run it too — a throwaway service left alive is billed to the project.
restore_run() {
  # Nothing of this check's was created -- a guard stopped the run, or it was a
  # dry run -- so there is nothing to delete. The repository counts here as
  # something created: a build that failed after the create still leaves the
  # repository behind, and the Restore is what removes it.
  if [ ! -f "$SERVICE_MINE" ] && [ ! -f "$REPO_MINE" ]; then
    fact "restore" PASS "nothing of this check's was created (no service, no repository), so there is nothing to delete"
    return 0
  fi
  local mine
  mine=$(cat "$REPO_EXISTED" 2>/dev/null || true)
  if [ "$mine" != no ]; then
    fact "restore" FAIL "$REPO_EXISTED does not say the repository ${SVC} was absent when this check started (it says '${mine:-nothing}'), so it is not this check's to delete. Nothing was deleted. Remove the service and the image by hand only if you know they are yours, and leave the repository alone. The scratch directory is kept: $SCRATCH"
    SCRATCH_KEPT=yes
    return 0
  fi
  say "Restore: deleting what this check created"
  # Only what the records say is this check's: a service or repository the run
  # did not create is never named here.
  if [ -f "$SERVICE_MINE" ]; then
    gcloud run services delete "${SVC:?}" --project="${P:?}" --region="${L:?}" --quiet >&2 || true
  fi
  # The image is not the service's, so it is NOT deleted under the service's
  # record: the build is what puts an image in the repository, and the one path
  # that has an image with no service — the deploy FAILED, and its own stop
  # takes the service record back again — was left with the image in the
  # repository, the repository delete failing on a repository that still holds
  # it, and a stop message promising the image went with it. What says an image
  # may exist here is BUILD_ATTEMPTED, which is set before `builds submit` runs.
  if [ "$BUILD_ATTEMPTED" = yes ]; then
    gcloud artifacts docker images delete "${IMG:?}" --project="${P:?}" --delete-tags --quiet >&2 || true
  fi
  if [ -f "$REPO_MINE" ]; then
    gcloud artifacts repositories delete "${SVC:?}" --project="${P:?}" --location="${L:?}" --quiet >&2 || true
  fi

  # The staged source (m11): the build uploaded the scratch directory as a tar
  # under this run's own prefix, and that tar is this check's litter in the
  # installation's bucket. Removed only when a build was attempted and only
  # under the prefix this run chose — never a bucket-wide delete. A build is
  # only ever attempted with that prefix (an unreadable bucket stops the run
  # before the build), so an attempted build always has one to remove.
  if [ "$BUILD_ATTEMPTED" = yes ] && [ -n "$STAGING_ARG" ]; then
    gcloud storage rm -r "$STAGING_PREFIX" >&2 || true
    # `gcloud storage ls` on a prefix with nothing under it exits non-zero and
    # says so on stderr ("matched no objects"), so the status alone does not
    # separate "gone" from "could not be read": the message does. Anything else
    # — a permission error, an expired session — is reported as unread, not as
    # a 0, exactly like the three counts below.
    local staged st_rc=0
    staged=$(gcloud storage ls "$STAGING_PREFIX" 2>"$SCRATCH/staged.err" | wc -l | tr -d ' ') || st_rc=$?
    if [ "$staged" != 0 ]; then
      fact "restore-staged-source" FAIL "the source tarball this run staged at ${STAGING_PREFIX} is still there (${staged} object(s)); the scratch directory does not help here, so remove it by hand: gcloud storage rm -r ${STAGING_PREFIX}"
    elif [ "$st_rc" = 0 ] || grep -q 'matched no objects' "$SCRATCH/staged.err"; then
      fact "restore-staged-source" PASS "the source tarball this run staged at ${STAGING_PREFIX} is gone (the prefix is empty and no bucket-wide delete was attempted)"
    else
      fact "restore-staged-source" FAIL "whether the staged source at ${STAGING_PREFIX} is still there cannot be read (the list exited ${st_rc} for a reason other than an empty prefix), so nothing is claimed about it: check it by hand with gcloud storage ls ${STAGING_PREFIX} and remove it with gcloud storage rm -r ${STAGING_PREFIX} if it is there"
    fi
  elif [ "$BUILD_ATTEMPTED" = yes ]; then
    # Not reachable through this script's own path — the build is given its
    # prefix or the run stops before it — and kept as the Restore's own guard:
    # a build attempted with no prefix of this run's is exactly the case whose
    # tarball could not be named, and it must be said rather than assumed away.
    fact "restore-staged-source" FAIL "a build was attempted without a staging prefix of this run's own, so the source tarball this run uploaded cannot be named and cannot be removed by this Restore: look for it under gcloud's default staging directory in gs://${P}_cloudbuild and remove it by hand"
  fi

  # Each count is guarded: a count query is how this run REPORTS, and one that exits
  # non-zero must not end the run. The image list is the one that does it in the ordinary
  # case — the repository was just deleted above, and the real CLI answers a list against a
  # repository that is not there with NOT_FOUND, a non-zero exit and nothing on stdout.
  # `set -o pipefail` would carry that exit out of the substitution, `set -e` would end the
  # script inside this cleanup, and the RESULT block would never be printed: the owner would
  # be left with a deleted service and no statement of what was left behind.
  #
  # Guarded is not the same as COUNTED, though. `gcloud … | wc -l` prints 0 for a query that
  # printed nothing, and a query that FAILED printed nothing too: an expired ADC session late
  # in a two-hour run, a transient API error, and the deletes above (guarded the same way)
  # can all fail together and still leave a block that reads "the three counts read 0" —
  # `restore: PASS`, exit 0, and the ownership records removed, which is the false PASS the
  # scratch-removed row beside it exists to prevent. So each query's own exit status is read
  # here, and a count that could not be READ is reported as that rather than as a 0.
  local svc_left img_left repo_left svc_rc=0 img_rc=0 repo_rc=0
  svc_left=$(gcloud run services list --project="${P:?}" --region="${L:?}" \
    --filter="metadata.name=${SVC}" --format='value(metadata.name)' | wc -l | tr -d ' ') || svc_rc=$?
  repo_left=$(gcloud artifacts repositories list --project="${P:?}" --location="${L:?}" \
    --filter="name~/repositories/${SVC}\$" --format='value(name)' 2>"$SCRATCH/repo-count.err" | wc -l | tr -d ' ') || repo_rc=$?
  cat "$SCRATCH/repo-count.err" >&2 2>/dev/null || true
  # The repository list swallows a server error (exit 0, nothing on stdout, `WARNING:
  # Failed to list repositories ...` on stderr), which would read as a count of 0 -- and
  # skip the image count below -- while the delete above failed in the same outage. That
  # warning makes the count a failed query, exactly as a non-zero exit does.
  if [ "$repo_rc" = 0 ] && grep -q 'Failed to list' "$SCRATCH/repo-count.err" 2>/dev/null; then
    say "the repository list printed 'Failed to list repositories' on stderr and exited 0: gcloud swallowed an API error, so its empty answer is read as a failed query (exit 1), not as 0"
    repo_rc=1
    repo_left=""
  fi
  # The images live inside the repository, so the question is asked only while the repository
  # list succeeded and said one is there: against a repository this Restore has just deleted
  # the CLI answers NOT_FOUND, and that answer IS the count of images left, not a failed
  # query. A repository list that FAILED is not read as "gone" (an empty value is not a
  # measurement), so the images are asked about then — and a failure there is a failure.
  if [ "$repo_rc" = 0 ] && [ "$repo_left" = 0 ]; then
    img_left=0
  else
    img_left=$(gcloud artifacts docker images list "${L}-docker.pkg.dev/${P}/${SVC}" \
      --project="${P:?}" --include-tags --format='value(image)' 2>/dev/null | wc -l | tr -d ' ') || img_rc=$?
  fi
  if [ "$svc_rc" != 0 ] || [ "$img_rc" != 0 ] || [ "$repo_rc" != 0 ]; then
    fact "restore-counts" FAIL "a list query failed, so how many are left cannot be read: services ${svc_left:-unread} (the query exited ${svc_rc}), images ${img_left:-unread} (exit ${img_rc}), repository ${repo_left:-unread} (exit ${repo_rc}). Each count must read 0 for this check's own things to be gone, and a query that failed is not a 0"
    fact "restore" FAIL "the counts could not all be read (the service list exited ${svc_rc}, the image list exited ${img_rc}, the repository list exited ${repo_rc}), so this check cannot say whether what it created is still in ${P}/${L}. Nothing was tried a second time, and nothing is claimed: run the three list commands by hand — they are the three in this check's steps appendix, docs/credentials-service/check91-steps.md, under the Restore — and read the answer yourself. The scratch directory is KEPT on purpose — it holds the records that say what is this check's — at $SCRATCH, and it is this check's own: remove it yourself (rm -rf) once you no longer need them. A fresh run of this check does NOT take it over: it makes a new scratch directory of its own and stops in the guards while the service or the repository is still in the project."
    SCRATCH_KEPT=yes
    return 0
  fi
  fact "restore-counts" PASS "services: ${svc_left}, images: ${img_left}, repository: ${repo_left} (each must read 0)"
  if [ "$svc_left" != 0 ] || [ "$img_left" != 0 ] || [ "$repo_left" != 0 ]; then
    fact "restore" FAIL "the counts are not all 0, so something this check created is still there (services=${svc_left} images=${img_left} repository=${repo_left}). Nothing was tried a second time: delete the survivors by hand (the service and its image, and the repository if the repo-existed record says no), then run the three list commands above until all three counts read 0. The scratch directory is KEPT on purpose — it holds the records that say what is this check's — at $SCRATCH, and it is this check's own: remove it yourself (rm -rf) once you no longer need them. A fresh run of this check does NOT take it over: it makes a new scratch directory of its own and stops in the guards while the service or the repository is still in the project."
    SCRATCH_KEPT=yes
    return 0
  fi
  fact "restore" PASS "the service, the image and the repository are deleted and the three counts read 0"
}

cleanup() {
  restore_run
  # The credential file goes with the run's own token file, before the scratch
  # directory: it holds a minted identity token, and it is this check's business
  # to leave nothing of the sort behind — not even inside a directory that is
  # KEPT because a count was not 0. That is exactly why the removal is CHECKED
  # rather than assumed: `rm -f ... || true` records nothing, and the one run
  # that keeps the scratch directory is the one where a failed `rm` would leave
  # a live bearer token inside a directory that survives the run. Check 90
  # reports its token file the same way (its TOKEN_REMOVED); this is the same
  # row for this script's credential file.
  optional_cleanup_note
  rm -f "$AUTH_CONF" || true
  AUTH_CONF_REMOVED=no
  [ ! -e "$AUTH_CONF" ] && AUTH_CONF_REMOVED=yes
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  if [ -n "$SCRATCH_KEPT" ]; then
    say "the scratch directory is kept: $SCRATCH"
  else
    remove_scratch
  fi
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here, and that is the point: cleanup is
  # about to run the Restore, which deletes a service and an image this check
  # created in the owner's project — and a second Ctrl-C during it would kill
  # this shell with the deletes half done, leaving a billed service, an image
  # or a repository behind, with the RESULT block never printed. So the
  # signals stay trapped for the whole of cleanup; `on_signal` says one line
  # and returns, and the Restore goes on. (The first Ctrl-C is already
  # accounted for: it came through `interrupted`, which ended the run at 130.)
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # The scratch directory's row is added BEFORE the verdict is taken, because the verdict
  # is taken from the facts: a directory this check could not remove is a FAIL, and a block
  # that printed `scratch-removed: FAIL` beside `END (exit 0)` would read as a pass. A run
  # that stopped in a guard created no directory at all, and says so rather than reporting a
  # cleanup failure with an empty path in it (the same shape Check 90's `restore` row had).
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  # The credential file's own row, added here for the same reason as the one
  # above: it holds a minted bearer token, so a run that could not remove it
  # must not come back reading as a pass. A run that never minted one (stopped
  # in a guard, before the scratch directory existed) says so rather than
  # claiming a removal of a file that was never written.
  if [ -z "$AUTH_CONF" ]; then
    facts="${facts}"$'\n'"restore-token-file|PASS|no credential file was ever written: the run stopped before it made one, so there is no minted token to remove"
  else
    facts="${facts}"$'\n'"restore-token-file|$([ "$AUTH_CONF_REMOVED" = yes ] && echo PASS || echo FAIL)|the credential file $([ "$AUTH_CONF_REMOVED" = yes ] && echo "is gone" || echo "IS STILL THERE at $AUTH_CONF (mode 600, holding a minted identity token) — remove it yourself")"
  fi
  if printf '%s\n' "$facts" | grep -q '|FAIL|' && [ "$status" = 0 ]; then
    # A guard stop is exit 1 already; a FAILed fact with exit 0 would read as a
    # pass, so the verdict is taken from the facts, not from the exit path.
    status=1
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
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
  # is not the fugaro-dev installation" (the wrong cause, and every other
  # doctor check is unrelated to it), and `grep -q` closes the pipe on its
  # match while doctor is still printing — its secrets: line comes right after
  # the project: line — so doctor would die on the broken pipe (141) and
  # pipefail would stop the run just the same. The stop names all three
  # causes, as this guard's own wording in the check register does
  # (docs/gcp-live-checklist.md, Check 38's guard block): a foreign checkout, a
  # --project that disagrees with it, or a doctor that could not run here at
  # all.
  local doctor_out
  doctor_out=$(fugaro doctor --project fugaro || true)
  # `grep` reads every line it is given here — no -q, no -m, so it never closes
  # the pipe on its match. With -q it would: printf, still writing doctor's
  # output, would be killed by the closed pipe (141), and pipefail would report
  # the pipeline as failed although the line matched — the same trap this
  # capture exists to remove, one process further along.
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP fugaro-dev,' >/dev/null; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project fugaro printed no 'project: ... (GCP fugaro-dev,' line. Either this is not the fugaro-dev installation, or --project fugaro disagrees with this checkout's project:, or doctor itself failed (doctor is not offline: it needs usable credentials and reads the installation's billing and IAM policy). Run 'fugaro doctor --project fugaro' here yourself and read its output before going on — this check runs on the sandbox installation only, never belong"
  fi
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  fact "project-region" PASS "${P}/${L} (installation fugaro, sandbox only; never belong)"
}

# list_state EXPECTED CMD... — run a `gcloud ... list` that is filtered to ONE
# resource and print WHICH of three things happened: `there` (a line equal to
# EXPECTED was printed), `not-there` (exit 0 and nothing printed) or
# `unreadable` (a non-zero exit, or output that is not EXPECTED).
#
# ABSENCE IS AN EMPTY LIST, never an error string. The first version read the
# message of a `describe` for the word NOT_FOUND, and the real gcloud says
# `ERROR: (gcloud.run.services.describe) Cannot find service [NAME]` — no such
# token — so the normal "absent" answer read as "unreadable" and the check could
# never start. A list of an absent resource exits 0 and prints nothing; a list
# that FAILS (an expired session, a transient API error, a permission this
# account does not hold) exits non-zero. Nothing here depends on any message.
# Reading an unreadable list as "not there" is how this check would adopt a
# service or a repository it did not create (the guard passes, the deploy
# writes into it, and the Restore DELETES it), and how a run that did create
# one would take its ownership record back on a transient failure and leave a
# billing service behind. "It is not there" is a claim about the project; "the
# call failed" is not a claim about anything.
#
# Always exits 0 (printf's status), so the caller can hold the word in a
# variable without the ERR trap or errexit seeing the list's own status.
list_state() {
  local want=$1 out rc=0
  shift
  out=$("$@" 2>"$SCRATCH/list.err") || rc=$?
  out=$(printf '%s' "$out" | tr -d '\r' | sed -e 's/[[:space:]]*$//' | grep -v '^$' || true)
  if [ "$rc" != 0 ]; then
    printf 'unreadable'
  elif grep -q 'Failed to list' "$SCRATCH/list.err" 2>/dev/null; then
    # `gcloud artifacts repositories list` SWALLOWS a server error: on an HTTP
    # 5xx (after its own retries) it logs `WARNING: Failed to list repositories
    # for project ...` on stderr, prints nothing and exits 0 -- the exact shape of
    # "not there". That warning is gcloud saying the answer is not a reading.
    printf 'unreadable'
  elif [ -z "$out" ]; then
    printf 'not-there'
  elif [ "$out" = "$want" ]; then
    printf 'there'
  else
    printf 'unreadable'
  fi
}

# The three probes, each one filtered LIST (see list_state).
svc_state() {
  list_state "${SVC:?}" gcloud run services list --project="${P:?}" --region="${L:?}" \
    --filter="metadata.name=${SVC}" --format='value(metadata.name)'
}
repo_probe() {
  list_state "projects/${P:?}/locations/${L:?}/repositories/${SVC:?}" gcloud artifacts repositories list \
    --project="${P:?}" --location="${L:?}" --filter="name~/repositories/${SVC}\$" --format='value(name)'
}

guard_service() {
  # Hard stop 4: the service is not already there — unless it is the one this
  # check deployed, recorded in service-mine right after the deploy.
  local st
  st=$(svc_state)
  if [ "$st" = unreadable ]; then
    stop "whether ${SVC} is already in ${P}/${L} cannot be read: the list failed (an expired session, a transient API error, or a permission this account does not hold), and a failed call is not the answer 'it is not there'. This guard is what stops this check adopting a service it did not create — one it would then update and its Restore would DELETE — so the run stops here rather than guessing. Run 'gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$st" = there ]; then
    [ -f "$SERVICE_MINE" ] \
      || stop "${SVC} already exists in ${P}/${L}, and it is not this check's. This check deletes what it creates and never adopts a service it did not create; delete it yourself if you know it is yours, or give this check a different name, then run again"
  fi
}

# The identity token every request carries, minted FRESH FOR EACH REQUEST: one
# minted here expires within the hour, and this check waits longer than that
# before it is done — and every 401 that would cause reads exactly like the
# size limit the body table is measuring. The value is never printed, and it is
# never on a command line either: it is written into the 0600 config file and
# the request names that file with --config. `ps` shows a process's argv to
# every user on the machine for as long as the process lives, and the
# hour-long stream would hold the token there for an hour.
# Non-zero when no token can be minted here.
mint_idt() {
  local tok
  tok=$(gcloud auth print-identity-token) || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
  return 0
}

baseline() {
  # The ownership record: a repository of this name that is already in the
  # project is not this check's, and this check never adopts, writes into or
  # deletes one it did not create.
  if [ ! -e "$REPO_EXISTED" ]; then
    local bst
    bst=$(repo_probe)
    if [ "$bst" = unreadable ]; then
      stop "whether a repository named ${SVC} is already in ${P}/${L} cannot be read: the list failed (an expired session, a transient API error, or a permission this account does not hold), and a failed call is not the answer 'it is not there'. Run 'gcloud artifacts repositories list --project=${P} --location=${L}' by hand and read its answer, then run this check again. Nothing was created"
    fi
    if [ "$bst" = there ]; then
      stop "a repository named ${SVC} is already in ${P}/${L}, and it is not this check's: this check never adopts, writes into or deletes a repository it did not create. If it is a leftover of an earlier attempt whose own Restore did not finish, look at it first (gcloud artifacts docker images list ${L}-docker.pkg.dev/${P}/${SVC} --include-tags --project=${P}) and delete it yourself if it holds nothing but that attempt's image; otherwise give this check a different name. Then run again"
    fi
    echo no >"$REPO_EXISTED"
  fi
  [ "$(cat "$REPO_EXISTED")" = no ] \
    || stop "$REPO_EXISTED does not say the repository ${SVC} was absent when this check started, and this check writes only into a repository it created itself"
  fact "baseline" PASS "no repository named ${SVC} was in ${P}/${L} when this check started (repo-existed: no)"
}

# --------------------------------------------------------------- step 2 files
write_relay() {
  cat >"$SCRATCH/relay.py" <<'RELAY_EOF'
#!/usr/bin/env python3
"""Check 91's relay. One container, two listeners: a fake SSE upstream on
loopback and the relay the caller reaches.

  GET  /         -> 200 "started=<utc>\n"       (liveness, and which instance
                     answered: the time THIS container's process started, which
                     is what makes a cold-start figure checkable)
  GET  /sse      -> SSE for SSE_MINUTES minutes, one event a second
  POST /echo     -> 200 "read N bytes\n"        (the body-limit probe)
"""
import http.client, http.server, os, threading, time

STARTED = time.time()                               # this instance's birth
PORT = int(os.environ.get("PORT", "8080"))          # Cloud Run sets PORT
FAKE = ("127.0.0.1", 8081)                          # the fake upstream, same box
MINUTES = float(os.environ.get("SSE_MINUTES", "20"))


class Fake(http.server.BaseHTTPRequestHandler):     # the fake upstream
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        deadline = time.monotonic() + MINUTES * 60
        n = 0
        while time.monotonic() < deadline:
            n += 1
            d = ('data: {"n": %d, "t": %.3f}\n\n' % (n, time.time())).encode()
            self.wfile.write(b"%x\r\n" % len(d) + d + b"\r\n")
            self.wfile.flush()
            time.sleep(1)
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


class Relay(http.server.BaseHTTPRequestHandler):    # what the caller reaches
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        if not self.path.startswith("/sse"):
            body = ("started=%s\n"
                    % time.strftime("%Y-%m-%dT%H:%M:%SZ",
                                    time.gmtime(STARTED))).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        conn = http.client.HTTPConnection(*FAKE, timeout=3700)
        conn.request("GET", "/sse")
        resp = conn.getresponse()
        self.send_response(resp.status)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("X-Relay", "chk91")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        try:
            while True:
                chunk = resp.read1(65536)   # as soon as the fake wrote: SSE
                if not chunk:
                    break
                self.wfile.write(b"%x\r\n" % len(chunk) + chunk + b"\r\n")
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass                               # the front end closed the request
        conn.close()

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        left = n
        while left > 0:
            chunk = self.rfile.read(min(left, 1 << 20))
            if not chunk:
                break
            left -= len(chunk)
        body = ("read %d bytes\n" % (n - left)).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def serve(handler, host, port):
    srv = http.server.ThreadingHTTPServer((host, port), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


serve(Fake, FAKE[0], FAKE[1])
print("chk91 relay on %d, fake upstream on %d, SSE_MINUTES=%g"
      % (PORT, FAKE[1], MINUTES), flush=True)
serve(Relay, "0.0.0.0", PORT)      # the public listener, and only this one
while True:
    time.sleep(3600)
RELAY_EOF
  cat >"$SCRATCH/Dockerfile" <<'DOCKER_EOF'
FROM python:3.13-slim
ENV PYTHONUNBUFFERED=1
COPY relay.py /relay.py
CMD ["python3", "/relay.py"]
DOCKER_EOF
  fact "relay" PASS "relay.py and Dockerfile written into the scratch directory: one container, two listeners (the fake SSE upstream on loopback, the relay the caller reaches); SSE_MINUTES from the environment, X-Relay on the stream"
  # The build's own config, so the build can run as a named service account
  # with logging that needs no bucket of its own (CLOUD_LOGGING_ONLY, as the
  # product's own builds set): `--tag` has no way to say either, and a build as
  # another account without a logs setting is refused unless gcloud creates a
  # logs bucket, which this check never does.
  cat >"$BUILD_CONFIG" <<BUILD_EOF
steps:
  - name: gcr.io/cloud-builders/docker
    args: ["build", "-t", "${IMG}", "."]
images: ["${IMG}"]
options:
  logging: CLOUD_LOGGING_ONLY
BUILD_EOF
  say "wrote $SCRATCH/relay.py, $SCRATCH/Dockerfile and $BUILD_CONFIG"
}

# redact_secrets TEXT: TEXT with anything credential-shaped replaced. The build
# error goes into the RESULT block, which is pasted into a PR: gcloud's and
# Cloud Build's text names objects, accounts and paths — that is what makes it
# worth reading — and it can also quote a signed URL, an authorization header
# or a token, which is not. The text is filtered rather than trusted.
# The scheme word is part of the shape, not a barrier to it: `Authorization:
# Basic <base64>` put a five-character word between the header name and the
# value, and the single rule this used to be required eight value characters
# immediately after the name, so Basic, Digest and a `client_secret=` or a
# `refresh_token:` went through untouched. A credential name is redacted when
# it is followed by a scheme word or by a value, whichever comes first.
redact_secrets() {
  printf '%s' "$1" | sed -E \
    -e 's/(([Pp]roxy-)?[Aa]uthorization[[:space:]]*:?[[:space:]]*)([Bb]asic|[Bb]earer|[Dd]igest)?[[:space:]]*[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Bb]earer[[:space:]]+)[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Aa]ccess[_-]?[Tt]oken|[Cc]lient[_-]?[Ss]ecret|[Rr]efresh[_-]?[Tt]oken|[Aa]pi[_-]?[Kk]ey|[Tt]oken)[[:space:]]*[:=][[:space:]]*[A-Za-z0-9._~+\/=-]{4,}/\1=<redacted>/g' \
    -e 's/([?&](X-Goog-Signature|X-Goog-Credential|Signature|sig|token|client_secret|refresh_token)=)[^ &]+/\1<redacted>/g' \
    -e 's/(ya29|eyJ|AIza|ghp_|gho_|github_pat_)[A-Za-z0-9._-]+/<redacted>/g'
}

# build_error_excerpt: what a FAILED build said, as the block can carry it. The
# last 30 lines and no more: the reason is at the end of gcloud's output and the
# beginning is progress noise. Reads $SCRATCH/build.err, which the build step
# writes; a build that failed with nothing on stderr says so rather than
# printing an empty row.
build_error_excerpt() {
  local file=$SCRATCH/build.err lines
  if [ ! -s "$file" ]; then
    printf 'it printed nothing on stderr'
    return 0
  fi
  lines=$(tail -n 30 "$file" | wc -l | tr -d ' ')
  printf 'the last %s line(s) of what it printed, with anything credential-shaped removed: %s' \
    "$lines" "$(redact_secrets "$(tail -n 30 "$file" | tr -d '\r' | tr '\n' ' ')")"
}

# ------------------------------------------------------ step 3 build + deploy
step_build_deploy() {
  [ "$(cat "$REPO_EXISTED" 2>/dev/null || true)" = no ] \
    || stop "$REPO_EXISTED does not say the repository ${SVC} was absent when this check started, and this check writes only into a repository it created itself. Read the baseline's answer and run again"
  local repo_state
  repo_state=$(repo_probe)
  if [ "$repo_state" = unreadable ]; then
    stop "whether a repository named ${SVC} is already in ${P}/${L} cannot be read: the list failed, and a failed call is not the answer 'it is not there' — a repository this check did not create would be written into and then DELETED by its Restore. Run 'gcloud artifacts repositories list --project=${P} --location=${L}' by hand and read its answer, then run this check again. Nothing was created"
  fi
  if [ "$repo_state" = there ]; then
    [ -f "$REPO_MINE" ] \
      || stop "a repository named ${SVC} is in ${P}/${L} and it is not this check's — this check creates its own and never adopts one, so it does not push the image tag into it or delete it. Nothing was written"
  else
    # The ownership record is written BEFORE the create, not after it: a run
    # that dies between the two — a Ctrl-C, a dropped connection, a kill — has
    # still made a repository, and the record is what tells the Restore it is
    # this check's to delete (the review's m6). The guards above have already
    # established that no repository of this name was there and that none is
    # adopted: a create that FAILS removes the record again, because then
    # nothing was created and a record claiming otherwise would be a claim to
    # delete something this run never made.
    : >"$REPO_MINE"
    if ! gcloud artifacts repositories create "${SVC:?}" --project="${P:?}" --location="${L:?}" \
      --repository-format=docker \
      --description="throwaway: Check 91's image repository (fugaro-chk91), deleted by its own Restore" --quiet >&2; then
      # A create that reported failure may still have made the repository — that
      # is exactly what the record before it is for — so the project is asked
      # which of the two happened. The record is taken back only when the
      # repository really is NOT THERE; a repository that IS there keeps its
      # record and the Restore deletes it, and so does one whose state cannot be
      # read: a list that failed is not
      # evidence that the repository is absent, and taking the record back on
      # one would leave a repository this run made with nothing naming it.
      local made
      made=$(repo_probe)
      if [ "$made" = there ]; then
        stop "the repository create reported failure, but ${SVC} IS there now, so it is this check's: the record says so and this run's Restore deletes it. Nothing else ran"
      fi
      if [ "$made" = unreadable ]; then
        stop "the repository create reported failure and whether ${SVC} is there now cannot be read (the list failed), so the record of it is KEPT: a create that reported failure may still have made the repository, and the record is what makes this run's Restore delete it. Check 'gcloud artifacts repositories list --project=${P} --location=${L}' by hand; if the repository is there, delete it yourself. Nothing else ran"
      fi
      rm -f "$REPO_MINE" || true
      stop "the repository create failed and ${SVC} is not there, so this run made no repository: the record of it is removed again"
    fi
  fi
  [ "$(repo_probe)" = there ] \
    || stop "no registry repository ${SVC} in ${P}/${L} for the image tag ${IMG} to build into"
  say "building ${IMG} (gcloud builds submit). If it offers to CREATE anything — the staging bucket, an API, a grant — say NO, stop, and report it: those are the installation's and are not this check's to accept"
  # The build's identity, its roles and the staging bucket were all read and
  # checked by guard_build_identity BEFORE the repository was created above;
  # STAGING_ARG gives the source upload a prefix of this run's own, so the
  # Restore can name and remove exactly what this check put in the bucket.
  [ -n "$BUILD_SA" ] && [ -n "$STAGING_ARG" ] \
    || stop "the build identity or the staging prefix was not resolved before the repository was created; this check never builds as an unnamed account"
  BUILD_ATTEMPTED=yes
  # The build's own output: stdout stays on the owner's stderr (that is the
  # progress line a slow build prints), and stderr is ALSO kept in a file, so a
  # build that fails can be read back into the block. gcloud's error is where
  # the reason is — on a project whose build identity cannot read the staged
  # source object it is the ONLY place it is, and the run the owner hit ended
  # with a bare stop and no way to tell that from a broken Dockerfile.
  local build_rc=0
  # Unquoted on purpose: STAGING_ARG is one word or nothing, and an empty
  # argument must not reach gcloud as an empty string.
  # shellcheck disable=SC2086
  gcloud builds submit --project="${P:?}" --config="$BUILD_CONFIG" \
    --service-account="projects/${P}/serviceAccounts/${BUILD_SA}" $STAGING_ARG "$SCRATCH" >&2 2>"$SCRATCH/build.err" || build_rc=$?
  if [ "$build_rc" != 0 ]; then
    fact "build-error" FAIL "gcloud builds submit (as ${BUILD_SA}) exited ${build_rc}: $(build_error_excerpt). Likely causes, most likely first: the identity the build ran as cannot read the source this run staged at ${STAGING_PREFIX} (storage.objects.get; roles/storage.objectViewer on gs://${P}_cloudbuild); it cannot WRITE the image to Artifact Registry (artifactregistry.repositories.uploadArtifacts; roles/artifactregistry.writer on the project, since ${SVC} is created by this run); YOUR account lacks roles/iam.serviceAccountUser on it; a grant that was read before the build has not taken effect; the Cloud Build API is not enabled in ${P}; or a step of the Dockerfile failed. The grants are the installation's IAM, not this check's to change. Nothing was deployed, and the repository, the image and the staged source are deleted by this run's Restore"
    stop "the build failed (exit ${build_rc}), so nothing was deployed: the block's build-error row carries what gcloud said and the likely causes"
  fi
  # Same rule as the repository's record above: a deploy that is interrupted is
  # the ordinary way to be left with a service that bills, so the record that
  # says the service is this check's is on disk BEFORE the deploy starts, and a
  # deploy that FAILED takes it back (m6).
  : >"$SERVICE_MINE"
  if ! gcloud run deploy "${SVC:?}" --project="${P:?}" --region="${L:?}" --image="${IMG:?}" \
    --no-allow-unauthenticated --timeout=3600 --min-instances=0 --max-instances=1 \
    --set-env-vars SSE_MINUTES=20 --quiet >&2; then
    local deployed
    deployed=$(svc_state)
    if [ "$deployed" = there ]; then
      stop "the deploy reported failure, but ${SVC} IS there now, so it is this check's: the record says so and this run's Restore deletes it, and the image and the repository go with it"
    fi
    if [ "$deployed" = unreadable ]; then
      stop "the deploy reported failure and whether ${SVC} is there now cannot be read (the list failed), so the record of it is KEPT: a deploy that reported failure may still be serving and being billed, and the record is what makes this run's Restore delete it. Check 'gcloud run services list --project=${P} --region=${L} --filter=metadata.name=${SVC}' by hand; if the service is there, delete it yourself. Nothing else ran"
    fi
    rm -f "$SERVICE_MINE" || true
    stop "the deploy failed and ${SVC} is not there, so nothing of this check's is running: the record is removed again, and the repository and the image it created are deleted by this run's Restore"
  fi
  # Both describes below are GUARDED, like every other describe here: this
  # script installs no ERR trap, so under `set -Eeuo pipefail` a transient
  # failure would end the run with gcloud's own exit status, no `aborted` fact,
  # and `service-config`/`unauthenticated-request` reading FAIL — not reached —
  # a block that says a fact failed without saying why. The service IS deployed
  # and IS this check's at this point (the record says so), so the stop says
  # that too: the Restore deletes it.
  local url_err=$SCRATCH/url.err
  URL=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" --format='value(status.url)' 2>"$url_err") \
    || stop "the deployed service's URL cannot be read: 'gcloud run services describe ${SVC} --format=value(status.url)' failed with $(tail -n 1 "$url_err" | tr -d '\r' | tr '\n' ' '). The service IS deployed and IS this check's — the record says so, and this run's Restore deletes it, with the image and the repository. Nothing else ran"
  [ -n "$URL" ] || stop "the service has no URL"
  mint_idt || stop "no identity token can be minted here, so no request can be authorised"
  local cfg cfg_err=$SCRATCH/cfg.err
  cfg=$(gcloud run services describe "${SVC:?}" --project="${P:?}" --region="${L:?}" \
    --format='value(spec.template.spec.containers[0].image,spec.template.spec.timeoutSeconds,spec.template.metadata.annotations)' 2>"$cfg_err" | tr '\n' ' ') \
    || stop "the deployed service's own configuration cannot be read: 'gcloud run services describe ${SVC} --format=value(spec.template...)' failed with $(tail -n 1 "$cfg_err" | tr -d '\r' | tr '\n' ' '). The service IS deployed and IS this check's — the record says so, and this run's Restore deletes it, with the image and the repository. Nothing else ran"
  fact "service-config" PASS "image ${cfg:-$IMG}; --timeout=3600 --min-instances=0 --max-instances=1 --no-allow-unauthenticated; URL ${URL}"

  # An unauthenticated call must be refused: that is what says the service is
  # not wide open, and it is the register's own assertion.
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time "$CURL_SECONDS" "${URL}/" 2>/dev/null || true)
  case "$code" in
  401 | 403) fact "unauthenticated-request" PASS "an unauthenticated GET / was refused with ${code}" ;;
  *) fact "unauthenticated-request" FAIL "an unauthenticated GET / answered ${code:-nothing}, so the service does not refuse unauthenticated calls — stop and fix the deployment before trusting anything else here" ;;
  esac
}

# ------------------------------------------------------------ step 4 cold start
step_cold_start() {
  local idle_seconds=${CHECK91_IDLE_SECONDS:-2100}
  local before cold warm
  mint_idt || stop "no identity token can be minted here, so the relay cannot be asked anything"
  # Guarded like the two probes below, and for the same reason: this script
  # installs no ERR trap, so under `set -euo pipefail` a connection-level curl
  # failure (DNS, refused, or this call's own --max-time) would end the run with
  # curl's own exit code and NO `cold-start` fact at all — a run that says
  # nothing about the step it died in, against the header's 0/1 contract. The
  # status is caught and reported as a FAILed fact instead, with the pre-wait
  # measurement named so it is not mistaken for the cold probe.
  local before_rc=0
  before=$(curl -sS --max-time "$CURL_SECONDS" --config "$AUTH_CONF" "${URL}/" | sed -n 's/^started=//p') || before_rc=$?
  if [ "$before_rc" != 0 ]; then
    fact "cold-start" FAIL "the pre-wait liveness request failed (curl exit ${before_rc}), so nothing was measured: the relay could not be reached before the idle wait at all (DNS, connection refused, or this call's own ${CURL_SECONDS}s cap), and the cold-start figures need it as the 'before' instance. Nothing was waited for and nothing was probed"
    return 0
  fi
  [ -n "$before" ] || stop "the relay's / body carries no started= line, so this is not this check's image"
  say "instance now: started=${before}; waiting ${idle_seconds}s for it to be released"
  # Backgrounded and waited on, not slept in the foreground: bash runs a
  # signal's trap only after the current foreground command finishes, so a
  # Ctrl-C during a 35-minute sleep would not be acted on until the sleep
  # ended. `wait` is interruptible, and the trap kills this job.
  sleep "$idle_seconds" &
  WAIT_PID=$!
  wait "$WAIT_PID" || true
  WAIT_PID=""
  local cold_rc=0 warm_rc=0
  : >"$SCRATCH/cold.out"
  mint_idt || stop "no identity token can be minted here, so the cold-start probes cannot be made"
  cold=$(curl -sS -o "$SCRATCH/cold.out" -w '%{time_starttransfer}|%{time_total}' --max-time "$COLD_PROBE_SECONDS" --config "$AUTH_CONF" "${URL}/" 2>/dev/null) || cold_rc=$?
  warm=$(curl -sS -o /dev/null -w '%{time_starttransfer}|%{time_total}' --max-time "$CURL_SECONDS" --config "$AUTH_CONF" "${URL}/" 2>/dev/null) || warm_rc=$?
  local after
  after=$(sed -n 's/^started=//p' "$SCRATCH/cold.out")
  local detail="waited ${idle_seconds}s; cold: first byte ${cold%%|*}s, total ${cold##*|}s; warm: first byte ${warm%%|*}s, total ${warm##*|}s; started= before=${before} after=${after}; --min-instances=0 --max-instances=1"
  if [ "$cold_rc" != 0 ] || [ "$warm_rc" != 0 ]; then
    fact "cold-start" FAIL "a request failed (cold curl exit ${cold_rc}, warm curl exit ${warm_rc}), so no figure can be read — ${detail}"
  elif [ -z "$after" ]; then
    # A measurement that did not happen, and it is NOT the cold start: curl
    # without --fail exits 0 on an HTTP error, so a 500 or 503 error page from
    # the front end leaves cold.out with no started= line in it and an empty
    # `after`. Read as a cold start it would be a failed measurement written
    # into U5 as a number — the one thing worse than no number. The body is
    # quoted back, so the error page is visible rather than inferred.
    fact "cold-start" FAIL "the cold request came back with no started= line in its body (cold curl exit ${cold_rc}), so nothing was measured: an error page from the front end (an HTTP error still exits 0) or an empty reply looks exactly like this. The body said: $(head -c 200 "$SCRATCH/cold.out" | tr '\n' ' ') — ${detail}"
  elif [ "$after" != "$before" ]; then
    fact "cold-start" PASS "a DIFFERENT instance answered the cold request — ${detail}"
  else
    fact "cold-start" FAIL "NOT a cold start: the instance that started at ${after} answered the request after the wait, so the figure is warm. Record it as warm and repeat the step after a longer idle — ${detail}"
  fi
}

# --------------------------------------------------------------- step 5 stream
step_stream() {
  local probe=${CHECK91_PROBE_SECONDS:-30}
  : >"$SCRATCH/stream.times"
  local headers
  # curl exits 28 when its own --max-time cuts a probe, and that is the EXPECTED
  # end of a probe against a stream that runs for minutes: the guard on each of
  # the two calls here is what keeps it from ending the run -- and with them,
  # the last three of U5's numbers, which would otherwise be lost after the
  # cold-start wait. The headers curl has already printed its headers by the
  # time its own --max-time cuts it, and those are what this line is after.
  mint_idt || stop "no identity token can be minted here, so the stream probes cannot be made"
  headers=$(curl -sS -N -D - -o /dev/null --max-time "$probe" --config "$AUTH_CONF" "${URL}/sse" 2>/dev/null |
    sed -n 's/^[Xx]-[Rr]elay: */x-relay: /p' | head -n 1) || true
  curl -sS -N --max-time "$probe" --config "$AUTH_CONF" "${URL}/sse" 2>/dev/null |
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      printf '%s %s %s\n' "$(date -u +%s)" "$(date -u +%H:%M:%S)" "$line" >>"$SCRATCH/stream.times"
    done || true
  local events span
  events=$(wc -l <"$SCRATCH/stream.times" | tr -d ' ')
  if [ "$events" -lt 2 ]; then
    fact "stream-unbuffered" FAIL "the ${probe}s probe carried ${events} event(s), so nothing about buffering can be read from it"
  else
    span=$(( $(tail -n 1 "$SCRATCH/stream.times" | cut -d' ' -f1) - $(head -n 1 "$SCRATCH/stream.times" | cut -d' ' -f1) ))
    if [ "$span" -ge $((events - 1)) ]; then
      fact "stream-unbuffered" PASS "${events} events arrived over ${span}s (one a second, so the stream is unbuffered through the front end); ${headers:-no X-Relay header recorded}; timestamps: $(cut -d' ' -f2,3 "$SCRATCH/stream.times" | head -n 3 | tr '\n' ' ')"
    else
      fact "stream-unbuffered" FAIL "${events} events arrived inside ${span}s: a burst, not a stream — the front end or the relay is buffering. First timestamps: $(cut -d' ' -f2,3 "$SCRATCH/stream.times" | head -n 3 | tr '\n' ' ')"
    fi
  fi
}

step_long_stream() {
  say "the long stream: SSE_MINUTES=59 against --timeout=3600. This is the hour-long part"
  gcloud run services update "${SVC:?}" --project="${P:?}" --region="${L:?}" --set-env-vars SSE_MINUTES=59 --quiet >&2 \
    || stop "the service update to SSE_MINUTES=59 failed"
  local out rc=0
  mint_idt || stop "no identity token can be minted here, so the long stream cannot be started"
  # The point of this one is where the stream ENDS, so its cap is not the SSE
  # length but that length plus slack for the front end to cut it: without a
  # --max-time of its own the request outlives this run, and a
  # Ctrl-C has nothing to end. curl still exits non-zero when the front end
  # cuts the connection mid-stream (18, 56, 52), and that is the measurement,
  # not a reason to stop the run.
  #
  # It runs as a BACKGROUND job with this shell waiting on it, for the reason
  # the cold-start wait does: bash runs a signal's trap only once the current
  # foreground command is done, and either of these two can take an hour.
  : >"$SCRATCH/long.out"
  curl -sS -N -o /dev/null -w '%{http_code} after %{time_total}s' \
    --max-time "$LONG_SECONDS" --config "$AUTH_CONF" "${URL}/sse" >"$SCRATCH/long.out" 2>/dev/null &
  WAIT_PID=$!
  wait "$WAIT_PID" || rc=$?
  WAIT_PID=""
  out=$(cat "$SCRATCH/long.out" 2>/dev/null || true)
  # The status is read, not assumed: curl exits 0 for ANY HTTP status (there is
  # no --fail here), so a front end answering with an error page or a 403 in a
  # fraction of a second left `out` reading "403 after 0.2s" with rc=0 — and
  # this row was an unconditional PASS, which is exactly the bug that was fixed
  # for cold-start. A row that never started streaming is a FAILed fact, not a
  # number for U5. A stream the front end CUT still carries its 200 here (the
  # headers were sent), so the cut -- the thing this row measures -- stays a
  # PASS with curl's 18/56 beside it.
  local code=${out%% *}
  case "$code" in
  2*)
    fact "stream-longest" PASS "SSE_MINUTES=59: ${out}; curl exit ${rc} (0 = the stream ended by itself; 18/56 = the front end cut it; 52 = empty reply; 28 = this check's own ${LONG_SECONDS}s cap) — --timeout=3600; mint a fresh identity token for every probe after this one"
    ;;
  *)
    fact "stream-longest" FAIL "the long stream never started: the front end answered ${code:-nothing} (${out}), curl exit ${rc}, so nothing was measured about where a 59-minute stream ends. curl exits 0 for any HTTP status, so an error page reaches this step looking like a completed stream — this is a failed measurement, not a figure for U5"
    ;;
  esac
}

# ----------------------------------------------------- step 6 the body table
step_body_limit() {
  local sizes=${CHECK91_BODY_SIZES:-"1048576 16777216 33554432 67108864 134217728"}
  local n h1 h2 h1_big="" h2_big=""
  local h1rc h2rc
  local refused=0
  local table=""
  for n in $sizes; do
    # A FRESH token for every row, and this is not tidiness: an identity token
    # lives about an hour, and this step runs after the long stream, whose own
    # mint is up to 59 minutes plus slack old by the time the first row is sent.
    # A 401 is not matched by the `case ... in 200*` below, so an expired token
    # would leave body-limit-http1 reading PASS with a too-small "largest
    # accepted" — U5's request-body number silently wrong, and reading exactly
    # like the limit this table is measuring. step_long_stream's own fact says
    # to mint a fresh token for every probe after it.
    mint_idt || stop "no identity token can be minted here, so the body table cannot be measured"
    head -c "$n" /dev/zero >"$SCRATCH/body.bin"
    # A row refused above the limit is a refusal curl may report with a
    # non-zero exit of its own (56, 55, 52): the row records it instead of
    # ending the run -- the first refusal is exactly what this step measures.
    : >"$SCRATCH/echo.out"
    h1rc=0
    h1=$(curl -sS --http1.1 -o "$SCRATCH/echo.out" -w '%{http_code} %{size_upload} over HTTP/%{http_version}' \
      --max-time "$CURL_SECONDS" --config "$AUTH_CONF" -X POST --data-binary @"$SCRATCH/body.bin" "${URL}/echo" 2>/dev/null) || h1rc=$?
    h1="${h1} (curl exit ${h1rc}), relay said: $(tr -d '\n' <"$SCRATCH/echo.out")"
    : >"$SCRATCH/echo.out"
    h2rc=0
    h2=$(curl -sS --http2 -o "$SCRATCH/echo.out" -w '%{http_code} %{size_upload} over HTTP/%{http_version}' \
      --max-time "$CURL_SECONDS" --config "$AUTH_CONF" -X POST --data-binary @"$SCRATCH/body.bin" "${URL}/echo" 2>/dev/null) || h2rc=$?
    h2="${h2} (curl exit ${h2rc}), relay said: $(tr -d '\n' <"$SCRATCH/echo.out")"
    table="${table}${n}: HTTP/1.1 ${h1} | HTTP/2-to-front-end ${h2}"$'\n'
    case "$h1" in 200*) h1_big="$n: $h1" ;; esac
    case "$h2" in 200*) h2_big="$n: $h2" ;; esac
    # A row the relay refused as unauthenticated is counted, and said out loud
    # in the fact: with a fresh token per row a 401 is not expiry, and either
    # way a 401 row is NOT the size limit this table measures. Without this the
    # only place a 401 appeared was a row inside a table the PASS line does not
    # print.
    case "$h1" in 401*) refused=$((refused + 1)) ;; esac
    case "$h2" in 401*) refused=$((refused + 1)) ;; esac
    rm -f "$SCRATCH/body.bin" "$SCRATCH/echo.out"
  done
  local refusal_note=""
  if [ "$refused" -gt 0 ]; then
    refusal_note=" ${refused} row(s) answered 401: an identity token was refused, which is not a size limit — the token is minted fresh for every row here, so check the account's permission on the service before reading anything above into U5."
  fi
  if [ -n "$h1_big" ]; then
    fact "body-limit-http1" PASS "largest accepted: ${h1_big}.${refusal_note} A refusal above what the check believed is a finding about U5, not a failure of the check"
  else
    fact "body-limit-http1" FAIL "no size was accepted on HTTP/1.1 end to end: $(printf '%s' "$table" | tr '\n' '; ')"
  fi
  # Guarded like its HTTP/1.1 sibling above, so the two halves of one table
  # cannot disagree about what a PASS means: a PASS here is "a size was
  # accepted", and a run where nothing was accepted — or where every row came
  # back 401 or 403 — is a FAIL with the whole table in it. It used to be an
  # unconditional PASS that printed "nothing accepted" in the same row, which
  # reads as a measurement of the front end's limit when no request succeeded.
  if [ -n "$h2_big" ]; then
    fact "body-limit-http2-front-end" PASS "largest accepted over TLS to the front end (NOT end to end: the container serves HTTP/1.1 only): ${h2_big}.${refusal_note} Rows: $(printf '%s' "$table" | tr '\n' ' ')"
  else
    fact "body-limit-http2-front-end" FAIL "no size was accepted over TLS to the front end, so the front end's own limit was not measured.${refusal_note} Rows: $(printf '%s' "$table" | tr '\n' '; ')"
  fi
  fact "http2-end-to-end" INFO "NOT SETTLED by this check: the relay is Python http.server (HTTP/1.1), so no request here is HTTP/2 to the container. U5's end-to-end HTTP/2 half stays UNVERIFIED; settling it needs --use-http2 and a container that serves h2c"
}

# ----------------------------------------------------------------------- main
# guard_http2: this machine's curl must speak HTTP/2, and that is asked FIRST —
# before the guards that spend a gcloud call, and long before the build, the
# deploy, the 35-minute wait and the hour-long stream. Without HTTP/2 the body
# table's second row silently repeats the first (`--http2` falls back to
# HTTP/1.1), which is a wrong measurement rather than a missing one, and
# discovering it after the long stream would throw that hour away.
#
# `curl -V` is CAPTURED, not piped into grep: `grep -q` closes the pipe on its
# match while curl is still printing the rest of its version line, and with
# `set -o pipefail` curl's death on the broken pipe (141) would be reported as
# this stop — the right message for the wrong reason.
guard_http2() {
  local v
  v=$(curl -V 2>/dev/null || true)
  case "$v" in
  *HTTP2*)
    fact "curl-http2" PASS "$(printf '%s\n' "$v" | head -n 1) — the body table's two rows really are two protocols"
    ;;
  *)
    fact "curl-http2" FAIL "this curl has no HTTP/2 support (curl -V printed ${v:+no HTTP2 capability}); --http2 would silently send HTTP/1.1 and the second row of the body table would duplicate the first. Nothing was created, built or deployed: this is decided before any of that"
    stop "this curl has no HTTP/2 support, so --http2 would silently send HTTP/1.1 and the second row would duplicate the first — use a curl built with HTTP/2"
    ;;
  esac
}

# guard_xtrace: refuse to run when this shell arrived with tracing on. The trace
# would print the identity token — `tok=$(gcloud auth print-identity-token)` and
# the printf that writes it into the 0600 config file are both traced in full —
# into stderr, which is exactly the log an owner pastes back. It runs before
# anything is created or spent, and it is the guard, not the `set +x` above,
# that covers the case where the flag is in SHELLOPTS/BASHOPTS and every child
# this script starts would trace on its own.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact "xtrace" FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs — the minting of the identity token and the write of the file it goes into among them — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check91.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact "xtrace" PASS "this shell is not tracing, so no trace of this run can carry the identity token"
}

# ------------------------------------------------------------ build identity
# The roles the build's service account must hold, one place so the guard, its
# printout and the docs agree. Accepted = the role named or one that contains it.
ROLES_AR='roles/artifactregistry.writer roles/artifactregistry.repoAdmin roles/artifactregistry.admin roles/owner roles/editor'
ROLES_BUCKET='roles/storage.objectViewer roles/storage.objectUser roles/storage.objectAdmin roles/storage.admin roles/owner roles/editor'
ROLES_LOG='roles/logging.logWriter roles/logging.admin roles/owner roles/editor'

# sa_roles KIND — the roles BUILD_SA holds on the project or on the staging
# bucket, one per line. Read-only. Non-zero when the policy cannot be read: a
# failed read is not the answer "it holds nothing".
sa_roles() {
  local kind=$1 err=$SCRATCH/policy.err raw
  # No --filter: `gcloud storage buckets get-iam-policy` is not a list command
  # and has none. Both reads flatten to "role,member" lines and the account is
  # picked out here, so the two calls take the same flags.
  case "$kind" in
  project)
    raw=$(gcloud projects get-iam-policy "${P:?}" --flatten='bindings[].members' \
      --format='csv[no-heading](bindings.role,bindings.members,bindings.condition.title)' 2>"$err") || return 1
    ;;
  bucket)
    raw=$(gcloud storage buckets get-iam-policy "gs://${P:?}_cloudbuild" --flatten='bindings[].members' \
      --format='csv[no-heading](bindings.role,bindings.members,bindings.condition.title)' 2>"$err") || return 1
    ;;
  esac
  printf '%s\n' "$raw" | awk -F, -v m="serviceAccount:${BUILD_SA}" 'tolower($2) == tolower(m) && $3 == "" { print $1 }'
}

# owner_member — the IAM member string of the active account: serviceAccount:
# for a service-account email, user: otherwise.
owner_member() {
  case "${1}" in
  *.gserviceaccount.com) printf 'serviceAccount:%s' "$1" ;;
  *) printf 'user:%s' "$1" ;;
  esac
}

# owner_can_act_as — whether the owner's active account can use BUILD_SA
# (roles/iam.serviceAccountUser, or roles/owner or roles/editor, which contain
# iam.serviceAccounts.actAs; serviceAccountAdmin and serviceAccountTokenCreator
# do NOT), read from the build account's own policy and the project's. Prints
# "yes", "no" or "unknown:<why>". Read-only. Grants held through a group, a
# folder or the organization are not visible here, and a conditional grant is
# not counted, so "no" is only a WARNING: the run goes on and the real error,
# if any, shows at `builds submit`.
owner_can_act_as() {
  local acct=${OWNER_ACCT:-} sa_pol proj_pol member
  if [ -z "$acct" ]; then
    echo "unknown:the active gcloud account cannot be read"
    return 0
  fi
  member=$(owner_member "$acct")
  sa_pol=$(gcloud iam service-accounts get-iam-policy "$BUILD_SA" --project="${P:?}" --flatten='bindings[].members' \
    --format='csv[no-heading](bindings.role,bindings.members,bindings.condition.title)' 2>"$SCRATCH/policy.err") \
    || { echo "unknown:the policy of ${BUILD_SA} cannot be read"; return 0; }
  if printf '%s\n' "$sa_pol" | awk -F, -v m="$member" 'tolower($2) == tolower(m) && $3 == "" && ($1 == "roles/iam.serviceAccountUser" || $1 == "roles/owner" || $1 == "roles/editor") { f = 1 } END { exit !f }'; then
    echo yes
    return 0
  fi
  proj_pol=$(gcloud projects get-iam-policy "${P:?}" --flatten='bindings[].members' \
    --format='csv[no-heading](bindings.role,bindings.members,bindings.condition.title)' 2>"$SCRATCH/policy.err") \
    || { echo "unknown:the project policy cannot be read"; return 0; }
  if printf '%s\n' "$proj_pol" | awk -F, -v m="$member" 'tolower($2) == tolower(m) && $3 == "" && ($1 == "roles/iam.serviceAccountUser" || $1 == "roles/owner" || $1 == "roles/editor") { f = 1 } END { exit !f }'; then
    echo yes
  else
    echo no
  fi
}

# The dedicated account this check recommends, and the commands to make it.
DEDICATED_SA_ID=fugaro-chk91-build
print_dedicated_setup() {
  local sa="${DEDICATED_SA_ID}@${P}.iam.gserviceaccount.com"
  say "  [per installation fugaro (${P}), run by YOU] gcloud iam service-accounts create ${DEDICATED_SA_ID} --project=${P} --display-name='Check 91 throwaway build account'"
  say "  [per installation fugaro (${P}), run by YOU] gcloud projects add-iam-policy-binding ${P} --member=serviceAccount:${sa} --role=roles/artifactregistry.writer --condition=None"
  say "  [per installation fugaro (${P}), run by YOU] gcloud storage buckets add-iam-policy-binding gs://${P}_cloudbuild --member=serviceAccount:${sa} --role=roles/storage.objectViewer"
  say "  [per installation fugaro (${P}), run by YOU, optional] gcloud projects add-iam-policy-binding ${P} --member=serviceAccount:${sa} --role=roles/logging.logWriter --condition=None"
  say "  [per installation fugaro (${P}), run by YOU, unless you are a project owner] gcloud iam service-accounts add-iam-policy-binding ${sa} --project=${P} --member=user:YOUR-EMAIL --role=roles/iam.serviceAccountUser"
  say "  then: CHECK91_BUILD_SA=${sa} bash scripts/live-checks/check91.sh"
}

# optional_cleanup_note: the account this check was told to build as is the
# owner's to remove, never this script's.
optional_cleanup_note() {
  [ "${BUILD_SA%%@*}" = "$DEDICATED_SA_ID" ] || return 0
  say "OPTIONAL owner step, per installation fugaro (${P}), run by YOU (this script never runs it): remove the throwaway build account's grants FIRST (after the account is deleted its members read deleted:serviceAccount:... and the removes fail), then the account: gcloud projects remove-iam-policy-binding ${P} --member=serviceAccount:${BUILD_SA} --role=roles/artifactregistry.writer --condition=None; gcloud projects remove-iam-policy-binding ${P} --member=serviceAccount:${BUILD_SA} --role=roles/logging.logWriter --condition=None (only if you granted it); gcloud storage buckets remove-iam-policy-binding gs://${P}_cloudbuild --member=serviceAccount:${BUILD_SA} --role=roles/storage.objectViewer; and last: gcloud iam service-accounts delete ${BUILD_SA} --project=${P}"
}

# holds ROLES... — reads the role lines on stdin; true when any of ROLES is there.
holds() {
  local have want r
  have=$(cat)
  for want in "$@"; do
    for r in $have; do
      [ "$r" = "$want" ] && return 0
    done
  done
  return 1
}

# guard_build_identity: resolve the account the build runs as, say up front what
# it is and what it needs, and read whether it holds that. All reads; the first
# cloud WRITE of this run comes after it. A missing grant is printed as a
# command for the owner and ends the run — the script never grants IAM itself.
guard_build_identity() {
  local cand=${CHECK91_BUILD_SA:-} lerr=$SCRATCH/sa.err list n st
  if [ -n "$cand" ]; then
    case "$cand" in
    *@"${P}".iam.gserviceaccount.com) ;;
    *) stop "CHECK91_BUILD_SA=${cand} is not a service account of ${P} (it must end in @${P}.iam.gserviceaccount.com): this check builds inside the sandbox project only. Nothing was created" ;;
    esac
    st=$(list_state "$cand" gcloud iam service-accounts list --project="${P:?}" --filter="email=${cand}" --format='value(email)')
    case "$st" in
    there) BUILD_SA=$cand ;;
    not-there) [ "${cand%%@*}" = "$DEDICATED_SA_ID" ] && print_dedicated_setup
      stop "CHECK91_BUILD_SA=${cand} does not exist in ${P}. Per installation fugaro (${P}), run by YOU: gcloud iam service-accounts create ${cand%%@*} --project=${P} --display-name='Check 91 throwaway build account' — then run this check again. Nothing was created" ;;
    *) stop "whether ${cand} exists cannot be read (the list failed: an expired session, a transient error or a missing permission). Nothing was created" ;;
    esac
  else
    list=$(gcloud iam service-accounts list --project="${P:?}" --filter='email~^fugaro-b-' --format='value(email)' 2>"$lerr") \
      || stop "the installation's build service accounts cannot be listed in ${P}: gcloud iam service-accounts list failed with $(tail -n 1 "$lerr" | tr -d '\r' | tr '\n' ' '). Nothing was created"
    n=$(printf '%s\n' "$list" | grep -c . || true)
    if [ "$n" = 0 ]; then
      say "The dedicated build account this check recommends (commands for YOU; this script never runs them):"
      print_dedicated_setup
      stop "installation fugaro (${P}) has no build service account (fugaro-b-*), and this check will NOT fall back to the project's default Compute service account (it holds nothing on the staging bucket or on Artifact Registry there, and its grants are not this check's to widen). Fix, per REPOSITORY, run by YOU: 'fugaro update --repo' in a checkout of a repository set up for this installation — it creates that repository's build account. Or, per INSTALLATION (${P}) and RECOMMENDED, a dedicated one (it never widens a product account): see the commands printed above, then run CHECK91_BUILD_SA=${DEDICATED_SA_ID}@${P}.iam.gserviceaccount.com bash scripts/live-checks/check91.sh. Nothing was created"
    elif [ "$n" != 1 ]; then
      stop "installation fugaro (${P}) has ${n} build service accounts (fugaro-b-*: $(printf '%s' "$list" | tr '\n' ' ')): pick one and run again with CHECK91_BUILD_SA=<that account>. Nothing was created"
    fi
    BUILD_SA=$list
  fi

  say "BUILD IDENTITY (read from ${P} before anything is created): the image build runs as ${BUILD_SA}, never as the default Compute service account."
  say "  It needs: roles/artifactregistry.writer on the repository this run creates (${SVC}), which does not exist yet, so the grant is project-level (artifactregistry.repositories.uploadArtifacts);"
  say "            roles/storage.objectViewer on gs://${P}_cloudbuild (it reads the staged source); roles/logging.logWriter (build logs; the build itself still runs without it)."
  say "  YOU need roles/iam.serviceAccountUser on it (project owners have it). This script reads these grants below and never writes IAM."
  case "${BUILD_SA%%@*}" in
  fugaro-b-*)
    say "WARNING: ${BUILD_SA} is a per-repository product build account. Granting it roles/artifactregistry.writer on the WHOLE project (the only grant that covers a repository this run creates) also lets it write fugaro-base and every other repository's images, which run holding the repositories' credentials. Prefer the dedicated account:"
    print_dedicated_setup
    ;;
  esac

  local missing="" pr br rc=0
  pr=$(sa_roles project) || rc=$?
  if [ "$rc" != 0 ]; then
    stop "the project's IAM policy cannot be read (gcloud projects get-iam-policy ${P} failed with $(tail -n 1 "$SCRATCH/policy.err" | tr -d '\r' | tr '\n' ' ')), so whether ${BUILD_SA} holds its roles is unknown. Nothing was created"
  fi
  # shellcheck disable=SC2086
  if ! printf '%s\n' "$pr" | holds $ROLES_AR; then
    missing="${missing}  [per installation fugaro (${P}), run by YOU] gcloud projects add-iam-policy-binding ${P} --member=serviceAccount:${BUILD_SA} --role=roles/artifactregistry.writer --condition=None
"
  fi
  rc=0
  br=$(sa_roles bucket) || rc=$?
  if [ "$rc" != 0 ]; then
    stop "the staging bucket's IAM policy cannot be read (gcloud storage buckets get-iam-policy gs://${P}_cloudbuild failed with $(tail -n 1 "$SCRATCH/policy.err" | tr -d '\r' | tr '\n' ' ')), so whether ${BUILD_SA} can read the staged source is unknown. Nothing was created"
  fi
  # shellcheck disable=SC2086
  if ! printf '%s\n' "$br" | holds $ROLES_BUCKET; then
    missing="${missing}  [per installation fugaro (${P}), run by YOU] gcloud storage buckets add-iam-policy-binding gs://${P}_cloudbuild --member=serviceAccount:${BUILD_SA} --role=roles/storage.objectViewer
"
  fi
  local log_missing=""
  # shellcheck disable=SC2086
  if ! printf '%s\n' "$pr" | holds $ROLES_LOG; then
    log_missing="  [per installation fugaro (${P}), run by YOU, optional] gcloud projects add-iam-policy-binding ${P} --member=serviceAccount:${BUILD_SA} --role=roles/logging.logWriter --condition=None
"
  fi
  # The owner's own right to use the account (the build is submitted by the
  # owner as that account). A "no" is only a WARNING with the grant command:
  # the right may come through a group, a folder or the organization, which the
  # policies read here do not show. The real error, if any, shows at
  # `builds submit`, where the build-error row names the permission.
  local actas owner_member_str
  OWNER_ACCT=$(gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | tr -d '\r' | head -n 1) || OWNER_ACCT=""
  actas=$(owner_can_act_as)
  owner_member_str=$(owner_member "${OWNER_ACCT:-YOUR-EMAIL}")
  case "$actas" in
  yes) ;;
  no)
    say "WARNING: no iam.serviceAccounts.actAs on ${BUILD_SA} was found for ${owner_member_str} in the build account's or the project's policy (a grant through a group, folder or organization is not visible here, and a conditional grant is not counted), so the build may fail on permission. If it does, grant: gcloud iam service-accounts add-iam-policy-binding ${BUILD_SA} --project=${P} --member=${owner_member_str} --role=roles/iam.serviceAccountUser"
    ;;
  *)
    say "NOTE: whether your account can act as ${BUILD_SA} could not be read (${actas#unknown:}); if the build fails on permission, grant: gcloud iam service-accounts add-iam-policy-binding ${BUILD_SA} --project=${P} --member=${owner_member_str} --role=roles/iam.serviceAccountUser"
    ;;
  esac
  if [ -n "$missing" ]; then
    say "MISSING GRANTS for ${BUILD_SA} (this script never grants IAM; run these yourself, wait a minute or two for them to take effect, then run this check again; the project-level one is wider than this check needs for a product account: prefer the dedicated account, and revoke it afterwards):"
    printf '%s%s' "$missing" "$log_missing" >&2
    fact "build-identity" FAIL "the build would run as ${BUILD_SA}, which lacks a required grant (see the stop): nothing was created, built or deployed"
    stop "${BUILD_SA} lacks a role the build needs; the commands are printed above. Nothing was created, built or deployed"
  fi
  if [ -n "$log_missing" ]; then
    say "NOTE: ${BUILD_SA} has no roles/logging.logWriter, so its build logs will not be written (the build still runs). To grant it:"
    printf '%s' "$log_missing" >&2
  fi
  fact "build-identity" PASS "the build runs as ${BUILD_SA}: roles/artifactregistry.writer (project), roles/storage.objectViewer on gs://${P}_cloudbuild$([ -n "$log_missing" ] && printf '; roles/logging.logWriter NOT held (logs only)' || printf ', roles/logging.logWriter') were read from the IAM policies before anything was created"

  # The staging bucket must be there and listable by YOU BEFORE the build:
  # `gcloud builds submit` CREATES gs://<project>_cloudbuild when it is
  # missing, and that bucket is the installation's (Terraform-managed), not
  # this check's — this run would have no record of having made it and no
  # Restore that could remove it. So an unreadable staging bucket is a STOP
  # before anything is created, never a fallback to gcloud's default staging.
  if ! gcloud storage ls "gs://${P}_cloudbuild/" >/dev/null 2>&1; then
    fact "build-staging" FAIL "gs://${P}_cloudbuild/ could not be listed here (it is not there, or this account cannot read it), so the build was NOT started: 'gcloud builds submit' would create that staging bucket for this run's source, and the bucket is the installation's — this check creates only what its own Restore deletes. Nothing was created, built or deployed"
    stop "gs://${P}_cloudbuild/ cannot be listed, and 'gcloud builds submit' would CREATE it to stage this run's source: a bucket is the installation's own (Terraform-managed, not this check's), so this check never creates one and has no record by which to remove it. Fix, per INSTALLATION fugaro (${P}), run by YOU: if the bucket exists, give YOUR account read on it (gcloud storage buckets add-iam-policy-binding gs://${P}_cloudbuild --member=user:YOUR-EMAIL --role=roles/storage.objectViewer); if it does not exist, create it once (gcloud storage buckets create gs://${P}_cloudbuild --project=${P} --location=${L} --uniform-bucket-level-access). Then run this check again. Nothing was created, built or deployed by this run"
  fi
  STAGING_ARG="--gcs-source-staging-dir=${STAGING_PREFIX}"
  fact "build-staging" PASS "this run stages the build's source under ${STAGING_PREFIX}, inside the installation's own staging bucket (which was listed before anything was created, so gcloud creates nothing), and its Restore deletes that prefix again"
}

# print_plan: what a real run would run, in order, and what its Restore would
# delete. Printed by --dry-run; nothing here is run.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, writes] gcloud artifacts repositories create ${SVC} --project=${P} --location=${L} --repository-format=docker"
  say "  [network, writes] gcloud builds submit --project=${P} --config=${BUILD_CONFIG} --service-account=projects/${P}/serviceAccounts/${BUILD_SA} ${STAGING_ARG} ${SCRATCH}"
  say "  [network, writes] gcloud run deploy ${SVC} --project=${P} --region=${L} --image=${IMG} --no-allow-unauthenticated --timeout=3600 --min-instances=0 --max-instances=1"
  say "  [network] the probes: identity-token requests with curl to the service's URL (cold start, stream, 59-minute stream, body sizes)"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, deletes] gcloud run services delete ${SVC} --project=${P} --region=${L}"
  say "  [network, deletes] gcloud artifacts docker images delete ${IMG} --project=${P} --delete-tags"
  say "  [network, deletes] gcloud artifacts repositories delete ${SVC} --project=${P} --location=${L}"
  say "  [network, deletes] gcloud storage rm -r ${STAGING_PREFIX}"
  if [ "${BUILD_SA%%@*}" = "$DEDICATED_SA_ID" ]; then
    say "  [optional, per installation fugaro (${P}), run by YOU, never run by this script] gcloud iam service-accounts delete ${BUILD_SA} --project=${P}"
  fi
  say "What needed the network and your credentials in THIS dry run: fugaro doctor, the lists that ask whether the service and the repository are there, the build-account list, the two IAM policy reads and the staging-bucket list (all reads). Local only: the scratch directory and the files in it (relay.py, Dockerfile, cloudbuild.yaml)."
}

main() {
  need_tools
  scratch_up
  guard_xtrace
  guard_http2
  guard_project
  guard_build_identity
  write_relay
  guard_service
  baseline

  if [ -n "$DRY_RUN" ]; then
    print_plan
    fact "dry-run" PASS "guards, the baseline and the relay files only; the commands a real run would run and its cleanup commands were printed, not run"
    return 0
  fi

  LONG_SECONDS=${CHECK91_LONG_SECONDS:-$((59 * 60 + 300))}
  step_build_deploy
  step_cold_start
  step_stream
  step_long_stream
  step_body_limit
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key follows, in the order the run
# recorded it (nothing here sorts them, and `aborted` is appended by the stop
# that ended the run, so it comes after the keys recorded up to that point).
# That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region build-identity build-staging baseline scratch service-config
  unauthenticated-request cold-start stream-unbuffered stream-longest
  body-limit-http1 body-limit-http2-front-end http2-end-to-end relay restore
  restore-counts scratch-removed restore-token-file)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 91 RESULT\n' >&3
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
  printf 'CHECK 91 RESULT END (exit %s)\n' "$status" >&3
}

# interrupted: end the two long jobs FIRST, then leave. Both are background
# jobs this shell is waiting on (the cold-start sleep and the hour-long
# stream), and the trap runs as soon as the signal arrives because `wait` is
# interruptible — killing them here is what keeps the Restore from being held
# behind a sleep that has 30 minutes left. The rest is the same path as before:
# exit 130, and the EXIT trap runs the Restore and prints the block.
interrupted() {
  local pid
  for pid in ${WAIT_PID:-}; do
    kill "$pid" 2>/dev/null || true
  done
  WAIT_PID=""
  owner "interrupted"
  exit 130
}

# on_signal: the ONE signal handler, armed for the whole run and never
# disarmed — including through the cleanup, which is where its second answer
# matters. A second Ctrl-C during the Restore (or a Ctrl-C during a signal
# handler, or a closing terminal's SIGHUP) would otherwise kill this shell
# mid-cleanup: the service can be deleted while the image and the repository
# are not, the three counts are never read, and the RESULT block that says
# what was left behind is never printed. So while cleanup runs the handler
# says one line and returns; before it, the signal is the interrupt it always
# was.
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
