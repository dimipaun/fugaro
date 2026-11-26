#!/usr/bin/env bash
# Check 840 — what a Cloud Run execution can prove, as ONE script YOU run.
#
# It settles the three UNVERIFIED items the launcher-does-not-mint reconciliation
# leaves open (docs/launcher-does-not-mint/reconciliation.md, "UNVERIFIED"):
#
#   U1. What a Cloud Run job execution's metadata identity token carries, and
#       whether anything in it is the execution's. F11 reads the platform as
#       unable to tell two executions of one job apart, and R15 says that if the
#       token DOES carry something of the execution, a contract change
#       (R-C1-execution) binds the run's claim to it and the residual in R5
#       closes for Cloud Run.
#   U2. Whether `run.executions.get` returns an execution's environment
#       overrides to a launcher. F9 rests on it: the launcher role
#       (`google_project_iam_custom_role.launcher`,
#       deploy/terraform/gcp/modules/installation/iam.tf) carries
#       `run.executions.get`, and a second launcher reads a peer's sealed token
#       out of the execution's own override.
#   U3. Whether one custom token exchanges twice inside its lifetime. F9 assumes
#       it does; if it does not, the copy F9 describes is a race today as well.
#
# HOW IT READS THEM. It creates ONE throwaway Cloud Run job of its own
# (`fugaro-chk840-probe`, an image with a shell in it, no Fugaro code and no
# credential of any kind), launches ONE execution of it WITH AN OVERRIDE through
# the Cloud Run v2 API, and deletes the job again. The probe prints, from inside
# the execution: the Google ID token the execution's own metadata server mints
# for a fixed audience (`format=full`, the endpoint internal/runner/gateway.go's
# metadataIDToken reads), its CLAIMS only and never the token, and the value of
# the marker variable the override set.
#
#   U1 is read off those claims: the execution's own name is known from the
#   `jobs.run` operation (its `metadata.name`, the field
#   internal/backend/gcp/run.go's launch reads), and the row says whether any
#   claim value carries it.
#   U2 is read off `GET /v2/<the execution's name>` -- the API call the launcher
#   role's `run.executions.get` admits -- made with YOUR credential, plus a read
#   of the role itself for that one permission. It does NOT claim a launcher was
#   tried: your account is not one, and the permission row is what says a
#   launcher could make the same call.
#   U3 is read with a Firebase custom token signed as the installation's signer
#   (`fugaro-token-signer`, internal/infra/firebase.go's SignerAccountID) through
#   IAM Credentials signJwt, the way the launcher mints one
#   (internal/budget/token/mint.go), exchanged TWICE at
#   `accounts:signInWithCustomToken` (internal/budget/token/exchange.go). Its
#   uid is `r~chk840~<random>`: run-shaped, and naming no run of yours.
#
# The override goes through the API's `jobs.run` because that is the only place
# it exists: `gcloud run jobs execute` has no `--overrides` flag (checked against
# the GA and beta references, and pinned in docs/gcp-live-checklist.md's Check 52
# step 3). Nothing here starts a Fugaro run, calls a model or touches a run.
#
# It changes nothing it does not remove: the ONE Cloud Run job it creates, and
# the executions on it, are deleted at the end and the deletion is READ BACK, and
# so is the Firebase Auth user the accepted exchange of the custom token creates
# in the signer's own project (a sign-in makes the account, so the run-shaped uid
# this check mints becomes a user of the project until it is deleted). If a job
# of that name is already there -- someone else's, or a previous run's -- it is
# refused before anything is created, because this check never adopts what it did
# not make.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 840".
#
# Usage:      bash scripts/live-checks/check-840.sh --project <project> \
#               --region <region> --job <the installation's job> [--signer <email>] [--dry-run]
#             --project and --region are the installation's own GCP project and
#             the Cloud Run region its jobs run in. --job is a job the
#             installation ALREADY runs (a repository job, a shared job or the
#             review job): the check reads two things off it and nothing else --
#             the service account it runs as, which the probe job is created
#             with, and the Firebase web API key in its environment
#             (`FUGARO_FIREBASE_API_KEY`, which internal/infra writes as a plain
#             value), which the U3 exchange is addressed with. It never launches
#             that job and never changes it. --signer overrides the signing
#             account U3 mints with; it defaults to
#             `fugaro-token-signer@<project>.iam.gserviceaccount.com`.
#             `--dry-run` prints the calls a real run would make and runs
#             nothing at all.
# Needs:      bash, mktemp, sed, python3, gcloud and curl on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it.
# Where:      YOUR OWN development installation, and never `belong`: any typed
#             value containing that name is refused before any call. It creates
#             a Cloud Run job in the project and deletes it again.
# Costs:      two Cloud Run job executions of a few seconds each on a small
#             image, one Cloud Run job created and deleted, and a handful of API
#             calls. No model call, no Fugaro run, no Cloud Build.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the job this check created is
#             deleted and read back as gone, the Firebase Auth user the accepted
#             exchange creates is deleted from the signer's own project and read
#             back gone, and the scratch directory is removed. The `restore` and
#             `auth-user` rows say so.
# Output:     progress on stderr, ONE block on stdout headed CHECK 840 RESULT,
#             to copy back. Exit 0 when all three readings came back the way the
#             design assumes, 1 when one of them came back the other way (each
#             such row names the finding and what it moves), and 2 when the run
#             could not tell (a guard stopped it, a tool is missing, a read did
#             not answer, an internal error ended it).
#
# No credential is printed, logged or put on a command line. The access token
# this check carries is minted into a 0600 config file inside a 0700 scratch
# directory and curl reads it with `--config`; the Firebase web API key travels
# the same way, inside that file's URL rather than on a command line; and the
# custom token U3 mints, and the ID token it answers with, are written to 0600
# files in the scratch directory and never printed at all. `ps` shows a
# process's argv to every user of the machine, which is why none of the three is
# ever an argument of anything.
#
# CHECK840_CURL_SECONDS, CHECK840_POLL_TRIES/SECONDS and
# CHECK840_LOG_TRIES/SECONDS exist for this script's own test: they bound the
# calls and the polls, and none of them can point the check at another project.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had. This check mints an
# access token, a custom token and an ID token, and a trace prints those lines
# in full — the credentials, into the log the owner would then paste back. So
# the trace is turned off here, BASH_XTRACEFD is unset so a trace redirected to a
# file this script knows nothing about cannot be reopened, and guard_xtrace
# refuses a shell that arrived tracing (the flag sits in SHELLOPTS/BASHOPTS then,
# and every child would start tracing on its own). Check 145 spells the three
# parts out; this is the same guard.
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
USAGE="check-840.sh --project <project> --region <region> --job <the installation's job> [--signer <email>] [--dry-run]"
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by step_verdict once the reading has been judged, so the exit trap
# can tell a finished run from one that ended on an error of its own.
REACHED_END=no
# --help printed the usage itself: it is not a run, and the exit trap has to see
# that whatever path ended it.
HELPED=no
DRY_RUN=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block, runs the Restore and removes the scratch directory, so a stop still
# gives you something to paste back and leaves nothing behind.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the execution for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the execution"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
# The names this check creates, and the values it reads. They are constants in
# this file so that the two the whole check hangs on -- the job it creates and
# the variable the override sets -- are read in exactly one place.
PROBE_JOB="fugaro-chk840-probe"
MARKER_ENV="FUGARO_CHK840_MARKER"
SIGNER_DEFAULT="fugaro-token-signer"
PROBE_IMAGE="gcr.io/google.com/cloudsdktool/cloud-sdk:slim"
# The audience the probe mints its ID token for. Any string is a valid audience
# and Cloud Run checks nothing about it: what matters is that it is the same for
# every run of this check, so two of them can be compared.
PROBE_AUDIENCE="https://chk840-check.invalid/"
RUN_URL_BASE="https://run.googleapis.com/v2"
IAMCRED_URL="https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts"
TOOLKIT_BASE="https://identitytoolkit.googleapis.com/v1"
IDENTITY_URL="$TOOLKIT_BASE/accounts:signInWithCustomToken"
# The Firebase project the exchanges happen at: the signer's own, derived in
# guard_probe_names. It is also where the Auth user an exchange creates is
# deleted from again (restore_auth_user).
FP=""
LAUNCHER_ROLE="fugaroLauncher"
FIREBASE_KEY_ENV="FUGARO_FIREBASE_API_KEY"

need() { command -v "$1" >/dev/null 2>&1 || die "Check 840 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 840 needs bash"
  need mktemp
  need sed "the block's rows are read back out of this run's own fact file with it"
  need python3 "every JSON body this check reads and writes is read and written by python3 files"
  need gcloud "the access token, the job reads and the log read are made with it"
  need curl "the API calls are made with it"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk840-mine"
SCRATCH_REMOVED=no
AUTH_CONF=""
PRINTED=""
FACTS_CAPTURED=""
# What this run created, and whether it is gone. RESTORED is what the restore
# facts are read from, and it is set even when there was nothing to delete.
PROBE_CREATED=no
RESTORED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk840.XXXXXXXX") || die "Check 840 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  AUTH_CONF="$SCRATCH/auth.conf"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 840 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: the Restore records what it
# managed to do in RESTORED, and a `$(cleanup)` would throw that answer away with
# the subshell.
cleanup() {
  restore
  restore_auth_user
  remove_credentials
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

# The credential files go first, and each outcome is recorded: a run that stopped
# between the mint and the call would otherwise leave a live access token on disk
# in a directory the trap is about to remove. Guarded, because this runs from the
# exit trap, where a failing `rm` under `set -e` would end the shell before the
# block is printed.
#
# The whole list is skipped when SCRATCH is empty, which it is until scratch_up
# runs: `die` from parse_args or need_tools exits before that, and "$SCRATCH/x"
# with SCRATCH empty is "/x" — a usage error would otherwise aim the rm at the
# root of the filesystem. The curl-written files are in the list too: signjwt.json
# carries the minted custom token and signin.N.json the ID tokens Firebase
# answers with, and they are the check's own files like the rest.
CREDENTIALS_REMOVED=yes
remove_credentials() {
  local f
  [ -n "$SCRATCH" ] || return 0
  for f in "$AUTH_CONF" "$SCRATCH/u3.conf" "$SCRATCH/custom.jwt" \
    "$SCRATCH/signin-body.json" "$SCRATCH/signjwt.json" "$SCRATCH"/signin.*.json; do
    [ -n "$f" ] || continue
    rm -f "$f" 2>/dev/null || true
    [ -e "$f" ] && CREDENTIALS_REMOVED=no
  done
  return 0
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
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

# ------------------------------------------------------------------- the guards
# guard_xtrace: a shell that arrived tracing would print the credentials this
# check mints, so the run is refused rather than read: the trace is already off
# (the top of this file), and what is left to catch is the shell that started
# with it, whose flag sits in SHELLOPTS/BASHOPTS and would be inherited by every
# child.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the access token and the custom token this check mints — and the log you would then paste. Run it without -x"
    stop "the shell this check was started from is tracing (-x): the trace would print the credentials this check mints. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the access token, the web API key or the custom token"
}

# guard_belong: the installation named `belong` is the owner's own. This check
# creates a Cloud Run job in the project it names, and the name is refused before
# any call: a check pointed at the owner's own installation by a mistyped value
# would be work nobody asked for. It is a substring test on purpose, over every
# value the owner typed, and the message says which one matched.
guard_belong() {
  local label value
  for label in "project:$P" "region:$L" "job:$JOB"; do
    value=${label#*:}
    case "$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$value' ($label) names the owner's own installation: Check 840 creates a job in the project it is pointed at, and it is never pointed at 'belong'"
      stop "the ${label%%:*} '$value' matches the installation 'belong', which this check must never touch"
      ;;
    esac
  done
  fact belong PASS "nothing typed names the owner's own installation (project $P, region $L, job $JOB)"
}

# guard_project: the id has to BE a project id. It is also what keeps the value
# out of the curl config file's own syntax — a quote or a newline in it would be
# read as another directive rather than as part of a URL — and a mistyped id is a
# stop rather than a call about some other project. The shape is GCP's own rule
# (6-30 characters, lowercase letters, digits and hyphens, starting with a letter
# and not ending in a hyphen).
guard_project() {
  case "$P" in
  [a-z]*) ;;
  *) die "--project '$P' is not a GCP project id: it starts with a lowercase letter. usage: $USAGE" ;;
  esac
  if ! printf '%s' "$P" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$'; then
    die "--project '$P' is not a GCP project id: 6 to 30 characters of lowercase letters, digits and hyphens, not ending in a hyphen. usage: $USAGE"
  fi
}

# guard_region: Cloud Run's own rule (a lowercase letter, then lowercase letters,
# digits and hyphens). The region is a path segment of every call below, so a
# value with a slash or a space in it would address something else.
guard_region() {
  if ! printf '%s' "$L" | grep -Eq '^[a-z][a-z0-9-]{1,40}$'; then
    die "--region '$L' is not a Cloud Run region: lowercase letters, digits and hyphens. usage: $USAGE"
  fi
}

# guard_probe_names: the name this check creates is its own, and the two ways it
# could touch something else are both refused here. The job the owner named must
# not BE the throwaway name (this check would then delete the installation's job
# as its own), and the throwaway name must be readable as a job name at all.
guard_probe_names() {
  if [ "$JOB" = "$PROBE_JOB" ]; then
    die "--job '$JOB' is the name of the ONE job this check creates and deletes: pointed at it, this check would delete the job it was told to read. Name another job of the installation. usage: $USAGE"
  fi
  if ! printf '%s' "$JOB" | grep -Eq '^[a-z][a-z0-9-]{0,62}$'; then
    die "--job '$JOB' is not a Cloud Run job name: lowercase letters, digits and hyphens. usage: $USAGE"
  fi
  if ! printf '%s' "$SIGNER" | grep -Eq '^[a-z0-9][a-z0-9.-]*@[a-z0-9-]+\.iam\.gserviceaccount\.com$'; then
    die "--signer '$SIGNER' is not a service account email: U3 signs the custom token as it. usage: $USAGE"
  fi
  # The signer is an account OF the Firebase project the custom token is
  # exchanged at: its own domain is that project, which is what Check 96 relies
  # on too. It is the one thing here that names the project, and the exchange's
  # answer is addressed with the project's own web API key -- so an identity
  # signed by an account of another project cannot be exchanged at all, and the
  # Auth user the exchange creates is created HERE and has to be deleted here.
  FP=${SIGNER#*@}
  FP=${FP%.iam.gserviceaccount.com}
}

# --------------------------------------------------------------- the one read
# mint_token writes the read's Authorization header into AUTH_CONF. The token is
# never printed, never exported and never on a command line: curl is given the
# file with --config, and the file is 0600 inside a 0700 scratch directory that
# the exit trap removes. Non-zero when no token can be minted here.
mint_token() {
  local tok
  tok=$(gcloud auth print-access-token --quiet) || return 1
  [ -n "$tok" ] || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
  return 0
}

mint_or_stop() {
  if ! mint_token; then
    fact token FAIL "no access token could be minted with gcloud (an expired session, or no credential at all), so nothing was read or launched: run 'gcloud auth login' and this check again"
    stop "gcloud auth print-access-token did not answer, so this run measured nothing (its own line is above). Run 'gcloud auth login' and this check again"
  fi
  fact token PASS "an access token was minted for this run's calls, written to a 0600 file inside this run's 0700 scratch directory and never printed or put on a command line"
}

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# says_absent FILE: whether a failed describe said the resource is NOT THERE,
# read from gcloud's own words rather than from one spelling of them. The real
# gcloud answers a describe of a missing Cloud Run job with
# `ERROR: (gcloud.run.jobs.describe) Cannot find job [NAME].` — no NOT_FOUND in
# it — so a grep for NOT_FOUND alone reads the normal "absent" answer as
# "unreadable" and the check stops at its first guard against a clean project.
# Both spellings are accepted; any other failure stays a could-not-be-read,
# which is the whole point of the guard.
says_absent() {
  grep -Eqi 'NOT_FOUND|not found|Cannot find' "$1" 2>/dev/null
}

# ------------------------------------------------------------------- the job
# guard_probe_is_free: the name this check creates must be free BEFORE anything
# is created, so that the Restore below can never delete a job this run did not
# make. A describe that does not answer is a stop rather than a guess in either
# direction: "not there" and "could not be read" are different answers, and the
# one this check acts on is the first.
guard_probe_is_free() {
  local code=0
  gcloud run jobs describe "$PROBE_JOB" --project "$P" --region "$L" --format=json \
    >"$SCRATCH/probe-before.json" 2>"$SCRATCH/probe-before.err" || code=$?
  if [ "$code" -eq 0 ]; then
    fact probe FAIL "$PROBE_JOB is already in $P/$L, and this check creates exactly that name: it never adopts a job it did not create, so nothing was created and nothing was deleted. Read it with 'gcloud run jobs describe $PROBE_JOB --project $P --region $L' and delete it yourself if it is a leftover of an earlier run"
    stop "$PROBE_JOB already exists in $P/$L: this check will not adopt it. Delete it yourself and run again"
  fi
  # A missing job is not one message: gcloud exits 1 with `Cannot find job
  # [NAME]` (and NOT_FOUND on other paths), and any other failure is not the
  # answer this guard needs.
  if says_absent "$SCRATCH/probe-before.err"; then
    fact probe PASS "$PROBE_JOB is not in $P/$L (read back by a describe before anything was created), and this run creates it and deletes it again"
    return 0
  fi
  fact probe FAIL "whether $PROBE_JOB is already in $P/$L could not be read (the describe failed without saying the job is absent), so nothing was created: this guard is what stops the Restore deleting a job this run did not make ($(first_line "$SCRATCH/probe-before.err"))"
  stop "the describe of $PROBE_JOB did not say the job is absent (the line above), so this run created nothing. Read it by hand and run again"
}

# read_job: the installation's own job, and the two plain values this check needs
# off it — the account it runs as, and the Firebase web API key in its env. The
# whole document is written into the 0700 scratch directory (it holds the job's
# environment values in plain text, which is why nothing here prints them), and
# the key is written straight into the 0600 U3 config file by the same python3
# process, so it never reaches a shell variable, a command line or the block.
write_job_reader() {
  cat >"$SCRATCH/job_reader.py" <<'JOB_EOF'
#!/usr/bin/env python3
"""Reads an installation job and prints the two facts this check needs off it.

Prints `sa=<email>` (empty when the job names no account), `key=<yes|no>` and
`rtdb=<yes|no>`; and, when the key is there, writes the 0600 curl config file
the U3 exchange is addressed with. The key's VALUE is printed nowhere: it goes
from the job's document into that file inside this process.
"""
import json
import os
import sys

doc = json.load(open(sys.argv[1]))
conf = sys.argv[2]
key_env = sys.argv[3]
url = sys.argv[4]

template = (doc.get("template") or {}).get("template") or {}
sa = template.get("serviceAccount") or ""
env = {}
for c in template.get("containers") or []:
    for e in c.get("env") or []:
        env[e.get("name") or ""] = e.get("value") or ""
key = env.get(key_env) or ""
print("sa=%s" % sa)
print("key=%s" % ("yes" if key else "no"))
print("rtdb=%s" % ("yes" if env.get("FUGARO_RTDB_URL") else "no"))
if key:
    fd = os.open(conf, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as fh:
        fh.write('url = "%s?key=%s"\n' % (url, key))
    os.chmod(conf, 0o600)
JOB_EOF
}

read_job() {
  local code=0
  gcloud run jobs describe "$JOB" --project "$P" --region "$L" --format=json \
    >"$SCRATCH/job.json" 2>"$SCRATCH/job.err" || code=$?
  if [ "$code" -ne 0 ]; then
    fact job-account FAIL "the job '$JOB' could not be read in $P/$L, so neither the account it runs as nor its Firebase web key is known: U2's probe job cannot be created as that account and U3 cannot be addressed. Check the name and the region ($(first_line "$SCRATCH/job.err"))"
    stop "the read of the job '$JOB' in $P/$L did not answer (the line above). Check the name and the region and run again"
  fi
  write_job_reader
  local out
  if ! out=$(python3 "$SCRATCH/job_reader.py" "$SCRATCH/job.json" "$SCRATCH/u3.conf" "$FIREBASE_KEY_ENV" "$IDENTITY_URL" 2>"$SCRATCH/job_reader.err"); then
    fact job-account FAIL "the job '$JOB' was read and could not be parsed, so the account and the web key were not read off it ($(first_line "$SCRATCH/job_reader.err"))"
    stop "the document of the job '$JOB' could not be parsed (the line above). Nothing was created"
  fi
  JOB_SA=$(printf '%s\n' "$out" | sed -n 's/^sa=//p' | head -1)
  local haskey hasrtdb
  haskey=$(printf '%s\n' "$out" | sed -n 's/^key=//p' | head -1)
  hasrtdb=$(printf '%s\n' "$out" | sed -n 's/^rtdb=//p' | head -1)
  if [ -z "$JOB_SA" ]; then
    fact job-account FAIL "the job '$JOB' names no service account, so this check has no account to create its probe job with, and the U1 reading off the execution's identity token would be about some other account than the installation's jobs run as"
    stop "the job '$JOB' names no service account (its template carries none). Name another job of the installation and run again"
  fi
  fact job-account PASS "the installation's job '$JOB' runs as $JOB_SA (read off its own template), and the probe job this check creates is created as the same account so that the identity token it mints names it"
  if [ "$hasrtdb" = yes ]; then
    fact job-rtdb PASS "the job '$JOB' carries $JOB_SA's runtime database address in its environment (read as a name; no value is printed)"
  fi
  if [ "$haskey" != yes ]; then
    fact job-key FAIL "the job '$JOB' carries no $FIREBASE_KEY_ENV in its environment, so U3's exchange cannot be addressed and nothing was created: name a job of the installation that carries it (internal/infra writes it as a plain value on every job), and run this check again"
    stop "the job '$JOB' carries no $FIREBASE_KEY_ENV (the row above), so U3 cannot be measured and this run created nothing. Name a job that carries it and run again"
  fi
  fact job-key PASS "the job '$JOB' carries $FIREBASE_KEY_ENV, and the key travelled from its document into the 0600 curl config file inside the python3 process that read it — never printed, never an argument. The key is what names the project the exchange happens at, and it is the signer's own ($FP): the exchange is addressed there, so the Auth user it creates is created in $FP and this run deletes it there again"
}

# create_probe: the ONE Cloud Run job this check creates. Its account comes from
# the installation's own job (above); its command is a shell that decodes the
# probe below and runs it; and it carries no volume, no secret and no Fugaro
# code, so an execution of it can do nothing but print.
create_probe() {
  write_probe
  local b64
  b64=$(python3 - "$SCRATCH/probe.sh" <<'PY'
import base64
import sys

sys.stdout.write(base64.b64encode(open(sys.argv[1], "rb").read()).decode("ascii"))
PY
  ) || stop "the probe could not be encoded, so nothing was created"
  local code=0
  gcloud run jobs create "$PROBE_JOB" \
    --project "$P" --region "$L" --image "$PROBE_IMAGE" \
    --command /bin/bash \
    --args="-c,echo $b64 | base64 -d | bash" \
    --service-account "$JOB_SA" \
    --tasks 1 --max-retries 0 --quiet \
    >"$SCRATCH/create.out" 2>"$SCRATCH/create.err" || code=$?
  unset b64
  if [ "$code" -ne 0 ]; then
    fact probe-run FAIL "the probe job could not be created in $P/$L, so nothing was launched and nothing is concluded about the execution ($(first_line "$SCRATCH/create.err"))"
    stop "the creation of $PROBE_JOB in $P/$L did not answer (the line above). Nothing was created"
  fi
  PROBE_CREATED=yes
  fact probe PASS "$PROBE_JOB was created in $P/$L as $JOB_SA (one job, one image with a shell in it, no Fugaro code and no secret), and this run deletes it again"
}

# write_probe: the script the execution runs. It prints the claims of the ID
# token the execution's own metadata server mints for a fixed audience — never
# the token — plus the account and the marker the override set. The audience goes
# in through a placeholder rather than through the shell, so no `$` of this
# check's own reaches the file and the file is the one that runs inside the
# container. The last line it prints announces HOW MANY claims it printed
# (`CHK840 end=<n>`): Cloud Logging ingests a container's lines one at a time and
# lags the execution, so a block that opens with `exec=` can be in the log
# without its claims — read as a whole block that is a claim set with claims
# missing, which judges U1 from a token that is not there.
write_probe() {
  cat >"$SCRATCH/probe.tmpl" <<'PROBE_EOF'
set -euo pipefail
aud="__AUDIENCE__"
meta="http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity"
tok="$(curl -sS -H 'Metadata-Flavor: Google' "${meta}?audience=${aud}&format=full")"
echo "CHK840 exec=${CLOUD_RUN_EXECUTION:-unknown}"
echo "CHK840 marker=${__MARKER__:-none}"
echo "CHK840 token_len=${#tok}"
python3 - "$tok" <<'PY'
import base64
import json
import sys

tok = sys.argv[1].strip()
parts = tok.split(".")
if len(parts) != 3:
    print("CHK840 end=0")
    raise SystemExit("CHK840 error=not-a-jwt")
pad = parts[1] + "=" * (-len(parts[1]) % 4)
claims = json.loads(base64.urlsafe_b64decode(pad))
n = 0
for name in sorted(claims):
    value = claims[name]
    if isinstance(value, (dict, list)):
        value = json.dumps(value, sort_keys=True)
    print("CHK840 claim=%s=%s" % (name, value))
    n += 1
print("CHK840 end=%d" % n)
PY
PROBE_EOF
  sed -e "s|__AUDIENCE__|${PROBE_AUDIENCE}|" -e "s|__MARKER__|${MARKER_ENV}|" \
    "$SCRATCH/probe.tmpl" >"$SCRATCH/probe.sh" \
    || stop "the probe could not be written into the scratch directory"
}

# launch_probe: the ONE execution this check starts, with the marker as an
# EXECUTION-LEVEL override. That is the shape F9 rests on and the launcher
# writes (internal/backend/gcp/run.go), and the API's `jobs.run` is the only
# place it exists — `gcloud run jobs execute` has no `--overrides` flag. The
# execution's own name comes from the operation's metadata, never from a
# `--limit=1` listing a concurrent run could race.
launch_probe() {
  # The marker's value is this run's own and names nothing: it is what says
  # whether the value the launch put on the override came back in the
  # execution's document. CHECK840_MARKER_VALUE pins it for this script's own
  # test, which has to write the same value into what the fake API answers.
  MARKER_VALUE=${CHECK840_MARKER_VALUE:-chk840-$(python3 -c 'import secrets; print(secrets.token_hex(8))')}
  local body="$SCRATCH/override.json"
  (umask 077 && printf '{"overrides":{"containerOverrides":[{"env":[{"name":"%s","value":"%s"}]}]}}\n' \
    "$MARKER_ENV" "$MARKER_VALUE" >"$body") || stop "the override could not be written, so nothing was launched"
  local code=""
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -X POST \
    -H 'Content-Type: application/json' --config "$AUTH_CONF" \
    -d "@$body" -o "$SCRATCH/run.json" -w '%{http_code}' \
    "$RUN_URL_BASE/projects/$P/locations/$L/jobs/$PROBE_JOB:run" 2>"$SCRATCH/run.err"); then
    fact probe-run FAIL "the launch of an execution did not answer: curl failed before any HTTP response (no network, a proxy, DNS). Nothing was launched, so nothing is concluded about the override ($(first_line "$SCRATCH/run.err"))"
    stop "the launch of an execution of $PROBE_JOB did not answer (curl's own line is above). Nothing is concluded: check the network and run again"
  fi
  if [ "$code" != 200 ]; then
    local why
    why=$(python3 - "$SCRATCH/run.json" <<'PY'
import json
import sys

try:
    e = (json.load(open(sys.argv[1])).get("error") or {})
except Exception:
    e = {}
print("%s %s" % (e.get("status") or "", e.get("code") or ""))
PY
    )
    fact probe-run FAIL "the API refused the launch (HTTP $code, $why): the caller may not run this job with overrides, so no execution was started and U2 was not measured ($(first_line "$SCRATCH/run.json"))"
    stop "the API refused the launch of $PROBE_JOB with HTTP $code (the line above). No execution was started, so nothing is concluded about the override: the job this run created is deleted again"
  fi
  EXEC_FULL=$(python3 - "$SCRATCH/run.json" <<'PY'
import json
import sys

try:
    print((json.load(open(sys.argv[1])).get("metadata") or {}).get("name") or "")
except Exception:
    print("")
PY
  )
  if [ -z "$EXEC_FULL" ]; then
    # The operation was accepted but its metadata did not name the execution
    # yet: poll the operation, which is what the launcher's own launch does with
    # the same field.
    local opname i=0
    opname=$(python3 - "$SCRATCH/run.json" <<'PY'
import json
import sys

try:
    print(json.load(open(sys.argv[1])).get("name") or "")
except Exception:
    print("")
PY
    )
    if [ -n "$opname" ]; then
      while [ "$i" -lt "$POLL_TRIES" ]; do
        curl -sS --max-time "$CURL_SECONDS" --config "$AUTH_CONF" \
          -o "$SCRATCH/operation.json" "$RUN_URL_BASE/$opname" 2>/dev/null || true
        EXEC_FULL=$(python3 - "$SCRATCH/operation.json" <<'PY'
import json
import sys

try:
    print((json.load(open(sys.argv[1])).get("metadata") or {}).get("name") or "")
except Exception:
    print("")
PY
        )
        [ -n "$EXEC_FULL" ] && break
        i=$((i + 1))
        [ "$i" -lt "$POLL_TRIES" ] && sleep "$POLL_SECONDS"
      done
    fi
  fi
  if [ -z "$EXEC_FULL" ]; then
    fact probe-run FAIL "the launch answered without naming the execution (neither the operation's metadata nor a poll of the operation carried a name), so nothing was read back and U1 and U2 were not measured ($(first_line "$SCRATCH/run.json"))"
    stop "the launch of $PROBE_JOB answered without naming the execution (the line above). Nothing is concluded: the job this run created is deleted again"
  fi
  EXEC_SHORT=${EXEC_FULL##*/}
  fact probe-run PASS "one execution was started with the marker as an execution-level override through the API's jobs.run, and the operation named it: $EXEC_FULL"
}

# read_execution: `run.executions.get` — the call the launcher role's own
# permission admits — made with YOUR credential. It is polled until the
# execution reports a completion time, because its own log lines (U1) are
# written by then; the LAST answer is the one U2 is judged from.
read_execution() {
  local i=0 code=""
  while [ "$i" -lt "$POLL_TRIES" ]; do
    if ! code=$(curl -sS --max-time "$CURL_SECONDS" --config "$AUTH_CONF" \
      -o "$SCRATCH/execution.json" -w '%{http_code}' \
      "$RUN_URL_BASE/$EXEC_FULL" 2>"$SCRATCH/execution.err"); then
      fact probe-exec FAIL "the read of the execution did not answer: curl failed before any HTTP response ($(first_line "$SCRATCH/execution.err"))"
      stop "the read of $EXEC_FULL did not answer (curl's own line is above). Nothing is concluded about the override"
    fi
    if [ "$code" != 200 ]; then
      fact probe-exec FAIL "run.executions.get answered HTTP $code for $EXEC_FULL, so the execution's own document was not read: a 403 here is the permission a launcher would need and does not have ($(first_line "$SCRATCH/execution.json"))"
      stop "run.executions.get for $EXEC_FULL answered HTTP $code (the line above). Nothing is concluded about the override"
    fi
    if grep -q '"completionTime"' "$SCRATCH/execution.json" 2>/dev/null; then
      # The execution's own uid, off the document the API answered with, is the
      # third identifier U1 looks for in the token (with the name and the full
      # resource name). It comes from the platform's answer here and never from
      # the container.
      EXEC_UID=$(python3 - "$SCRATCH/execution.json" <<'PY'
import json
import sys

try:
    print(json.load(open(sys.argv[1])).get("uid") or "")
except Exception:
    print("")
PY
      )
      fact probe-exec PASS "the execution was read back through the API's run.executions.get ($RUN_URL_BASE/$EXEC_FULL answered HTTP 200), with the owner's own credential, until it reported a completion time; its own document's uid is ${EXEC_UID:-not in the document}, which is the third identifier U1 looks for in the token"
      return 0
    fi
    i=$((i + 1))
    [ "$i" -lt "$POLL_TRIES" ] && sleep "$POLL_SECONDS"
  done
  fact probe-exec FAIL "the execution $EXEC_SHORT did not report a completion time within $((POLL_TRIES * POLL_SECONDS)) s, so its own log lines may not be there yet and U1 was not read"
  stop "the execution $EXEC_SHORT did not finish within the poll's bound (the row above). Nothing is concluded about the identity token"
}

# ------------------------------------------------------- U2: the override back
# The execution's document is a run.v2 Execution: the override the launcher wrote
# is merged into the execution's own spec, so it shows up as a container's env.
# The reader prints the env NAMES only, and whether the marker is there with the
# value this run set — the marker's value is this check's own and names nothing,
# but the rule this file keeps is that no value is printed at all.
write_execution_reader() {
  cat >"$SCRATCH/execution_reader.py" <<'EXEC_EOF'
#!/usr/bin/env python3
"""Reads an execution and prints what U2 asks, with no value in it.

Prints `names=<comma-separated env names>`, `marker=<yes|no>` (the override's
variable, with the value this run set) and `sa=<the account the execution ran
as>`. Names only: no environment value is printed, by this file or by the shell.
"""
import json
import sys

doc = json.load(open(sys.argv[1]))
name = sys.argv[2]
want = sys.argv[3]

template = doc.get("template") or {}
names = []
marker = "no"
for c in template.get("containers") or []:
    for e in c.get("env") or []:
        n = e.get("name") or ""
        names.append(n)
        if n == name and (e.get("value") or "") == want:
            marker = "yes"
print("names=%s" % ", ".join(names))
print("marker=%s" % marker)
print("sa=%s" % (template.get("serviceAccount") or ""))
EXEC_EOF
}

judge_u2() {
  write_execution_reader
  local out
  if ! out=$(python3 "$SCRATCH/execution_reader.py" "$SCRATCH/execution.json" "$MARKER_ENV" "$MARKER_VALUE" 2>"$SCRATCH/execution_reader.err"); then
    fact u2 FAIL "the execution's document could not be parsed, so what run.executions.get returned is not read ($(first_line "$SCRATCH/execution_reader.err"))"
    stop "the execution's document could not be parsed (the line above). Nothing is concluded about the override"
  fi
  local names marker
  names=$(printf '%s\n' "$out" | sed -n 's/^names=//p' | head -1)
  marker=$(printf '%s\n' "$out" | sed -n 's/^marker=//p' | head -1)
  if [ "$marker" = yes ]; then
    fact u2 PASS "run.executions.get does return the execution's override: the execution's own document carries $MARKER_ENV with the value this run set on the override, so a launcher that can make this call (the permission is read beside it) reads what the launch put there (the execution's other env names: ${names:-none})"
  else
    fact u2 FAIL "run.executions.get did NOT return the execution's override: the execution's own document carries $MARKER_ENV with no value, and its env names are ${names:-none}. F9 rests on a launcher reading a peer's override back through this call, so this is a finding: the reconciliation's U2 and F9 must be updated to what the document says, and the launcher's read of an execution is not the way it is described"
  fi
}

# --------------------------------------------------- U1: what the ID token has
# The probe's own lines, read out of Cloud Logging. Ingestion lags the execution
# by seconds, so the read is retried while it answers with nothing of this
# execution; a read that never carries it is a stop, not a set of zeroes. Both
# payload shapes a step's line can have are read (a textPayload, and a build or
# a job that logged JSON puts it in jsonPayload.message).
#
# `--order=asc`, and never gcloud's default: every line the probe prints is its
# own log entry, so descending order reverses the lines WITHIN an execution's
# block as well as the blocks themselves, while the reader below takes a block as
# the lines after its `exec=` line. Descending, this execution's claims arrive
# before the `exec=` line that opens the block and are read as none. The
# register's own checks pass it for the same reason (check-580.sh, check610.sh).
PROBE_READ=""
PROBE_NOTE=""
read_probe_log() {
  local filter i=0 end count
  filter="resource.type=\"cloud_run_job\" resource.labels.job_name=\"$PROBE_JOB\""
  write_log_reader
  while [ "$i" -lt "$LOG_TRIES" ]; do
    gcloud logging read "$filter" --project "$P" --freshness=1h --limit=200 \
      --order=asc --format='value(textPayload,jsonPayload.message)' \
      >"$SCRATCH/log.txt" 2>"$SCRATCH/log.err" || true
    # This execution's block, and only a WHOLE one: its opening `exec=` line
    # alone is not enough, because ingestion is per line and in no order — the
    # claims can still be on their way when the line that closes the block has
    # arrived. A block read while it is half there is a claim set with claims
    # missing, which judges U1 either way on a token whose claims were never
    # seen, so the read waits for the probe's own closing line AND for the claims
    # it announces to be there. What is waited for is the log's own content, and
    # never a count of lines it happens to hold.
    if python3 "$SCRATCH/log_reader.py" "$SCRATCH/log.txt" "$EXEC_SHORT" \
      >"$SCRATCH/reader.out" 2>"$SCRATCH/log_reader.err"; then
      end=$(sed -n 's/^end=//p' "$SCRATCH/reader.out" | head -1)
      count=$(grep -c '^claim|' "$SCRATCH/reader.out" 2>/dev/null) || count=0
      count=${count:-0}
      if [ -n "$end" ] && [ "$end" = "$count" ]; then
        PROBE_READ="$SCRATCH/reader.out"
        PROBE_NOTE=""
        return 0
      fi
      if [ -n "$end" ]; then
        PROBE_NOTE="a read held $count of the $end claim(s) the probe closed its block with"
      else
        PROBE_NOTE="a read held this execution's opening line without the line that closes its block"
      fi
    else
      PROBE_NOTE="a read could not be parsed"
    fi
    i=$((i + 1))
    [ "$i" -lt "$LOG_TRIES" ] && sleep "$LOG_SECONDS"
  done
  return 1
}

# write_log_reader: the claims of the token the execution minted, one line per
# claim, out of the probe's own lines. The token itself is never in the log —
# the probe prints its claims — and this reader cannot print it either: what it
# reads is a claim set.
write_log_reader() {
  cat >"$SCRATCH/log_reader.py" <<'LOG_EOF'
#!/usr/bin/env python3
"""Reads the probe's own lines and prints the claim set it recorded.

One `name|value` line per claim of the identity token, and one `key=value` line
for the marker, the length and the count the probe's closing line announced. The
token is not in this file's input: the probe prints claims and never the token.
"""
import sys

want = sys.argv[2]
claims = []
marker = ""
length = ""
ended = ""
# ONE execution's lines, and only its own. A run of this check within the
# freshness window leaves its own lines behind, and the read is by job name, so
# the output can carry two executions' blocks: the probe prints `exec=` first,
# the read is asked for in ascending order, and the block is taken as the lines
# between this execution's `exec=` line and the next one -- and only when that
# name is the execution this run launched. The block ENDS at the probe's own
# `end=` line: the lines after it belong to no block of this probe's, and an
# `end=` the block does not have is what says the log holds only part of it.
mine = False
for line in open(sys.argv[1], encoding="utf-8", errors="replace"):
    line = line.strip()
    if not line.startswith("CHK840 "):
        continue
    body = line[len("CHK840 "):]
    if body.startswith("exec="):
        if mine:
            break
        mine = body[len("exec="):] == want
        continue
    if not mine:
        continue
    if body.startswith("claim="):
        name, _, value = body[len("claim="):].partition("=")
        claims.append((name, value))
    elif body.startswith("end="):
        ended = body[len("end="):]
        break
    elif body.startswith("marker="):
        marker = body[len("marker="):]
    elif body.startswith("token_len="):
        length = body[len("token_len="):]
sys.stdout.write("marker=%s\n" % marker)
sys.stdout.write("token_len=%s\n" % length)
sys.stdout.write("end=%s\n" % ended)
for name, value in claims:
    sys.stdout.write("claim|%s|%s\n" % (name, value))
LOG_EOF
}

judge_u1() {
  local out
  # The reader's answer for this execution's block, taken by read_probe_log over
  # the log read: it holds the block only when the probe's own closing line is in
  # it, so what is judged here is a whole claim set.
  out=$(cat "$PROBE_READ" 2>/dev/null || true)
  printf '%s\n' "$out" | grep '^claim|' >"$SCRATCH/claims" || true
  local marker len count
  marker=$(printf '%s\n' "$out" | sed -n 's/^marker=//p' | head -1)
  len=$(printf '%s\n' "$out" | sed -n 's/^token_len=//p' | head -1)
  # `grep -c` prints its 0 AND fails when nothing matched, so `|| printf '0'`
  # would append a second 0 and the count would read `0\n0`: the test below would
  # then fail on a non-integer and a token with no claims at all would pass as
  # "nothing in it is the execution's".
  count=$(grep -c '^' "$SCRATCH/claims" 2>/dev/null) || count=0
  count=${count:-0}
  if [ "$count" -lt 3 ]; then
    fact claims FAIL "the execution's identity token answered with $count claim(s) — too few to be a Google ID token — so nothing is judged about what it carries (the probe's own lines are in this run's log read)"
    stop "the identity token the execution minted carried $count claims (the row above), which cannot be the token this check is about. Nothing is concluded"
  fi
  # Every claim NAME is in the block — a name is what says what the token
  # carries, and a partial list would hide exactly the claim U1 is about. The
  # account and the audience are what the token is FOR; sub is the account's own
  # number. Those four are the only VALUES named: a claim set is not a
  # credential, but a reader of the block does not need the rest of it.
  local aud email iss sub names
  aud=$(claim_value aud)
  email=$(claim_value email)
  iss=$(claim_value iss)
  sub=$(claim_value sub)
  names=$(sed -n 's/^[^|]*|\([^|]*\)|.*/\1/p' "$SCRATCH/claims" | paste -sd, -)
  fact claims PASS "the identity token carries $count claim(s) for the audience $PROBE_AUDIENCE ($len bytes of token): aud=$aud, email=$email, iss=$iss, sub=$sub, and, with their values left out, $names. The token itself is never printed, and neither is any other claim's value"
  # The override's echo inside the container has a key and a verdict row of its
  # own. It is a reading in its own right -- the launch put the marker on the
  # execution and the container either saw it or did not -- and the block prints
  # one line per key: a second `claims` row would be swallowed by the PASS above
  # and the verdict below judges the keys named in it, so a FAIL recorded here
  # would leave the block saying the assumption held.
  if [ "$marker" != "$MARKER_VALUE" ]; then
    fact override-echo FAIL "the execution's own environment did not carry the marker this run overrode ($MARKER_ENV is '$marker' in the log, not the value that was set): the override the launch put on the execution did not reach the container, so the u2 row is not a read of an override that took effect. F9's launcher path, and the reconciliation's U2, have to be read against that"
  else
    fact override-echo PASS "the execution's own environment carried the marker this run overrode ($MARKER_ENV) with the value the launch set, so the override really reached the container and the u2 row is a read of an override that took effect"
  fi

  # U1's question: does anything in this token belong to THIS execution? The
  # execution's own name and uid were read off the platform's own answer (the
  # operation's metadata and the execution's document), not off the container, so
  # a claim carrying either is the platform's, not a self-report. All three
  # identifiers the platform gives an execution are looked for — the name, the
  # full resource name, and the uid of the execution's own document — because a
  # token that carries any of them tells one execution of a job from another,
  # which is the whole of F11's reading. The row names WHICH claim carries it and
  # what it carried, and never the value.
  local name value culprit="" carried=""
  while IFS='|' read -r _ name value; do
    [ -n "$name" ] || continue
    case "$value" in
    *"$EXEC_FULL"*) culprit="$name"; carried="the execution's full resource name" ;;
    *"$EXEC_SHORT"*) culprit="$name"; carried="the execution's name" ;;
    *)
      if [ -n "$EXEC_UID" ]; then
        case "$value" in
        *"$EXEC_UID"*) culprit="$name"; carried="the execution's uid" ;;
        esac
      fi
      ;;
    esac
    if [ -n "$culprit" ]; then break; fi
  done <"$SCRATCH/claims"
  if [ -n "$culprit" ]; then
    fact u1 FAIL "the identity token DOES carry something of the execution: the claim $culprit carries $carried. F11 reads the platform as unable to tell two executions of one job apart, and this says otherwise — the finding the reconciliation's R15 and its R-C1-execution exist for: a contract change binds the run's claim to this claim, and the residual in R5 closes for Cloud Run"
  else
    fact u1 PASS "nothing in the identity token is the execution's: the claims name the account ($email) and the audience ($aud), and no claim value carries $EXEC_SHORT, its full resource name${EXEC_UID:+ or its uid}. Two executions of one job minting for one audience get this same claim set, which is F11's reading of the platform and the reason a claim by a run proves the job rather than the execution"
  fi
}

# claim_value NAME — the value the probe's own lines gave a claim, empty when the
# claim is not there.
claim_value() {
  sed -n "s/^[^|]*|$1|//p" "$SCRATCH/claims" | head -1
}

# ------------------------------------------------- U2's other half: the role
# F9's premise is that the launcher role carries run.executions.get. The read is
# of the role itself; a role that is not there is a FAIL (the premise does not
# hold on this installation), and a read that does not answer is a stop, because
# "the role carries nothing" and "the role was not read" are different answers.
read_launcher_role() {
  local code=0
  gcloud iam roles describe "$LAUNCHER_ROLE" --project "$P" --format=json \
    >"$SCRATCH/role.json" 2>"$SCRATCH/role.err" || code=$?
  if [ "$code" -ne 0 ]; then
    fact launcher-perm FAIL "the launcher role '$LAUNCHER_ROLE' could not be read in $P, so whether a launcher may make the read U2 measures is not known: this role is the one the installation's launchers are given (internal/infra/tfvars.go's RoleLauncher) and its permissions are the premise F9 rests on ($(first_line "$SCRATCH/role.err"))"
    stop "the launcher role '$LAUNCHER_ROLE' could not be read in $P (the line above). The permission U2's 'to a launcher' half rests on is not known, so the read below would be about the owner's own credential alone and nothing is concluded"
  fi
  if python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if "run.executions.get" in (d.get("includedPermissions") or []) else 1)' "$SCRATCH/role.json" 2>/dev/null; then
    fact launcher-perm PASS "the launcher role '$LAUNCHER_ROLE' in $P carries run.executions.get (read off the role itself), so the read of an execution's document below is the read a launcher of the same job can make: F9's premise holds on this installation"
  else
    fact launcher-perm FAIL "the launcher role '$LAUNCHER_ROLE' in $P does NOT carry run.executions.get (read off the role itself). F9 rests on a second launcher reading a peer's sealed token out of the execution's own override through this permission, so this is a finding: the launcher role's list and F9 must be read against each other"
  fi
}

# ------------------------------------------------- U3: one token, twice
# The mint is the launcher's own path: the claims are built as
# internal/budget/token/mint.go builds them (iss and sub are the signer, aud is
# the identity toolkit audience, iat and exp an hour apart), signed as the
# signer through IAM Credentials signJwt, and never printed. The uid is
# run-shaped and names no run: nothing this check signs can be a run's identity.
write_mint_writer() {
  cat >"$SCRATCH/mint.py" <<'MINT_EOF'
#!/usr/bin/env python3
"""Writes the signJwt request body and, later, the exchange's body.

Two modes: `claims <signer> <uid> <out>` writes the claim set signJwt is asked
to sign; `body <jwt-file> <out>` writes the body the exchange carries, reading
the signed token out of a file so that it is never an argument of anything.
"""
import json
import os
import sys
import time

mode = sys.argv[1]
if mode == "claims":
    signer, uid, out = sys.argv[2:5]
    now = int(time.time())
    payload = {
        "iss": signer,
        "sub": signer,
        "aud": "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit",
        "iat": now,
        "exp": now + 3600,
        "uid": uid,
    }
    body = {"payload": json.dumps(payload)}
else:
    jwt = open(sys.argv[2]).read().strip()
    body = {"token": jwt, "returnSecureToken": True}
    out = sys.argv[3]
fd = os.open(out, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    json.dump(body, fh)
os.chmod(out, 0o600)
MINT_EOF
}

U3_UID=""
judge_u3() {
  write_mint_writer
  U3_UID="r~chk840~$(python3 -c 'import secrets; print(secrets.token_hex(6))')"
  python3 "$SCRATCH/mint.py" claims "$SIGNER" "$U3_UID" "$SCRATCH/signjwt-body.json" \
    || stop "the signJwt body could not be written, so nothing was signed"
  local code=""
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -X POST \
    -H 'Content-Type: application/json' --config "$AUTH_CONF" \
    -d "@$SCRATCH/signjwt-body.json" -o "$SCRATCH/signjwt.json" -w '%{http_code}' \
    "$IAMCRED_URL/$SIGNER:signJwt" 2>"$SCRATCH/signjwt.err"); then
    fact mint FAIL "the signJwt call did not answer: curl failed before any HTTP response ($(first_line "$SCRATCH/signjwt.err"))"
    stop "the signJwt call for $SIGNER did not answer (curl's own line is above). U3 was not measured"
  fi
  if [ "$code" != 200 ]; then
    fact mint FAIL "signJwt answered HTTP $code for $SIGNER, so no custom token was minted and U3 was not measured: signing as the runs' signer needs the fugaroTokenMinter role on it, and a 403 here is that grant missing for the account you are signed in as ($(first_line "$SCRATCH/signjwt.json"))"
    stop "signJwt answered HTTP $code for $SIGNER (the line above). U3 was not measured: fix the grant and run again"
  fi
  python3 - "$SCRATCH/signjwt.json" "$SCRATCH/custom.jwt" <<'PY' || stop "the signJwt answer carried no token, so U3 was not measured"
import json
import os
import sys

tok = (json.load(open(sys.argv[1])).get("signedJwt") or "").strip()
if not tok:
    raise SystemExit(1)
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    fh.write(tok)
os.chmod(sys.argv[2], 0o600)
PY
  python3 "$SCRATCH/mint.py" body "$SCRATCH/custom.jwt" "$SCRATCH/signin-body.json" \
    || stop "the exchange's body could not be written, so nothing was exchanged"
  fact mint PASS "one Firebase custom token was signed as $SIGNER through IAM Credentials signJwt, with the launcher's own claim set (iss and sub the signer, the identity toolkit audience, an hour of life) and the run-shaped uid $U3_UID. It was written to a 0600 file inside this run's 0700 scratch directory and is never printed"

  exchange 1
  if [ "$EXCHANGE_OK" != yes ]; then
    fact u3-first FAIL "Firebase refused the FIRST exchange of the token this check minted (HTTP $EXCHANGE_CODE): the token is not the shape Firebase accepts, so the second exchange is not measured ($EXCHANGE_DETAIL)"
    stop "Firebase refused the first exchange of the minted custom token (the row above). Nothing is concluded about a second one"
  fi
  # An accepted exchange has made a Firebase Auth user of this run's uid, and
  # the exit trap deletes it (restore_auth_user). Set here, on the one answer
  # that creates it, so a stop anywhere below still removes it.
  AUTH_USER_CREATED=yes
  fact u3-first PASS "Firebase accepted the first exchange of the minted custom token (HTTP 200, an ID token back), under uid $EXCHANGE_UID — which is a Firebase Auth user of $FP's now, and this run deletes it again on the way out (the auth-user row)"
  local first_uid=$EXCHANGE_UID
  exchange 2
  if [ "$EXCHANGE_OK" = yes ]; then
    fact u3-second PASS "Firebase accepted a SECOND exchange of the SAME custom token inside its hour (HTTP 200, an ID token back), under the same uid $EXCHANGE_UID"
    fact u3 PASS "one custom token exchanges more than once inside its lifetime, which is what F9 assumes: a launcher that copies a run's token (U2's read) can trade it for a session of its own. This is the reading U3 asked for, and F9 stands as written"
  else
    fact u3-second FAIL "Firebase REFUSED the second exchange of the same custom token (HTTP $EXCHANGE_CODE): $EXCHANGE_DETAIL"
    fact u3 FAIL "one custom token does NOT exchange twice inside its hour. F9 assumes it does — the copy a second launcher makes is a race because the first exchange does not use the token up — so this is a finding: the reconciliation's U3 and F9 must be updated, and the copy F9 describes was never the race it is written as"
  fi
  if [ -n "$first_uid" ] && [ "$first_uid" = "$EXCHANGE_UID" ]; then
    fact u3-uid PASS "$EXCHANGE_UID (the two exchanges answered under one identity: the uid is the token's own, and neither exchange changed it)"
  fi
}

# exchange N — one POST of the same custom token at
# accounts:signInWithCustomToken, with the body in a 0600 file so the token
# itself is never an argument. Sets EXCHANGE_OK, EXCHANGE_CODE, EXCHANGE_UID and
# EXCHANGE_DETAIL; no token is ever printed, neither the custom one nor the ID
# token that comes back.
EXCHANGE_OK=no
EXCHANGE_CODE=""
EXCHANGE_UID=""
EXCHANGE_DETAIL=""
exchange() { # exchange N
  local n=$1 code=""
  EXCHANGE_OK=no
  EXCHANGE_CODE=""
  EXCHANGE_UID=""
  EXCHANGE_DETAIL=""
  # The URL is in u3.conf and NOWHERE else. Passing it as an argument as well
  # would make curl fetch TWICE — the config file's URL and the argument's — and
  # `-w` prints a status for each transfer, so the code this reads would be
  # `200{…the keyless transfer's error…}403` rather than one answer. The keyless
  # second transfer is also the one thing this check must not make: the exchange
  # is addressed with the web API key or it is not measured.
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -X POST \
    -H 'Content-Type: application/json' --config "$SCRATCH/u3.conf" \
    -d "@$SCRATCH/signin-body.json" -o "$SCRATCH/signin.$n.json" -w '%{http_code}' \
    2>"$SCRATCH/signin.$n.err"); then
    EXCHANGE_CODE="no answer"
    EXCHANGE_DETAIL="curl failed before any HTTP response ($(first_line "$SCRATCH/signin.$n.err"))"
    return 0
  fi
  EXCHANGE_CODE=$code
  local out
  out=$(python3 - "$SCRATCH/signin.$n.json" <<'PY'
import json
import sys

try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = {}
err = d.get("error") or {}
print("uid=%s" % (d.get("localId") or ""))
print("id=%s" % ("yes" if d.get("idToken") else "no"))
print("why=%s" % (err.get("message") or d.get("error_description") or ""))
PY
  )
  EXCHANGE_UID=$(printf '%s\n' "$out" | sed -n 's/^uid=//p' | head -1)
  local hasid why
  hasid=$(printf '%s\n' "$out" | sed -n 's/^id=//p' | head -1)
  why=$(printf '%s\n' "$out" | sed -n 's/^why=//p' | head -1)
  EXCHANGE_DETAIL="$why"
  if [ "$code" = 200 ] && [ "$hasid" = yes ]; then
    EXCHANGE_OK=yes
  fi
  return 0
}

# ------------------------------------------------------------------- the verdict
# One verdict out of the rows above, so the block and the exit status cannot
# disagree: every FAIL row is named here, and the exit status is the verdict's.
# The list is read back out of the run's own fact file rather than remembered.
step_verdict() {
  local failed=() key list
  for key in u1 u2 launcher-perm u3 override-echo; do
    if [ "$(row_verdict "$key")" = FAIL ]; then failed+=("$key"); fi
  done
  if [ ${#failed[@]} -eq 0 ]; then
    fact verdict PASS "all three readings came back the way the design assumes: the execution's identity token carries nothing of the execution (U1), run.executions.get returns the execution's override to a caller whose role has the permission (U2), and one custom token exchanges twice (U3)"
    REACHED_END=yes
    exit 0
  fi
  list=$(printf '%s, ' "${failed[@]}"); list=${list%, }
  fact verdict FAIL "$list came back the other way. Each row above names the finding and what it moves: an answer against the design's assumption is not a fault of this installation, and the row is what the reconciliation's UNVERIFIED item and the design it feeds must be read against"
  REACHED_END=yes
  exit 1
}

# row_verdict KEY — the verdict this run recorded for KEY, empty when it recorded
# none.
row_verdict() {
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" | head -1
}

# ------------------------------------------------------------------- the Restore
# Everything this run created, deleted in the one order that works: the job (and
# with it its executions), then read back by a describe that must say it is gone.
# It runs from the exit trap, so it runs whether the run passed, failed or was
# interrupted, and what it managed to do is READ BACK rather than assumed.
restore() {
  [ "$RESTORED" = yes ] && return 0
  RESTORED=yes
  if [ "$PROBE_CREATED" != yes ]; then
    fact restore PASS "nothing of this check's was created, so there is nothing to delete: no job of the name $PROBE_JOB was made by this run"
    return 0
  fi
  say "Restore: deleting the one Cloud Run job this check created"
  gcloud run jobs delete "$PROBE_JOB" --project "$P" --region "$L" --quiet \
    >"$SCRATCH/delete.out" 2>"$SCRATCH/delete.err" || true
  local code=0
  gcloud run jobs describe "$PROBE_JOB" --project "$P" --region "$L" --format=json \
    >"$SCRATCH/probe-after.json" 2>"$SCRATCH/probe-after.err" || code=$?
  if [ "$code" -ne 0 ] && says_absent "$SCRATCH/probe-after.err"; then
    fact restore PASS "$PROBE_JOB and the executions on it are gone from $P/$L (deleted, and read back by a describe that says the job is absent). The only thing this run leaves in $P is the executions' own log lines, which are the project's record of what ran, not a resource this check made"
    return 0
  fi
  fact restore FAIL "$PROBE_JOB may still be in $P/$L: the describe after the delete did not say the job is absent. Delete it yourself with 'gcloud run jobs delete $PROBE_JOB --project $P --region $L' ($(first_line "$SCRATCH/probe-after.err"))"
  return 0
}

# restore_auth_user: U3's exchange CREATES a Firebase Auth user. Identity Toolkit
# makes the account on the first sign-in of a custom token, so the uid this check
# minted (r~chk840~<random>) is a user in $FP's table from the moment the first
# exchange is accepted — and the run deletes it the way the installation's own
# sweepers do: accounts:delete with the caller's own access token, then
# accounts:lookup read back. That read-back IS the assertion, so a delete that
# answered 200 and a lookup that could not be read are different answers and the
# second one is not a pass.
#
# It runs from the exit trap beside the Restore, before the credentials go, so a
# FAIL, a stop or a Ctrl-C after the first exchange still removes it.
AUTH_USER_CREATED=no
AUTH_RESTORED=no
restore_auth_user() {
  [ "$AUTH_RESTORED" = yes ] && return 0
  AUTH_RESTORED=yes
  if [ "$AUTH_USER_CREATED" != yes ]; then
    fact auth-user PASS "no Firebase Auth user of this check's was made: a user is created by the first accepted exchange of the custom token, and this run never got one, so there is nothing to delete in $FP"
    return 0
  fi
  local code=""
  (umask 077 && printf '{"localId":"%s"}\n' "$U3_UID" >"$SCRATCH/auth-delete-body.json") || true
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -X POST \
    -H 'Content-Type: application/json' --config "$AUTH_CONF" \
    -d "@$SCRATCH/auth-delete-body.json" -o "$SCRATCH/auth-delete.json" -w '%{http_code}' \
    "$TOOLKIT_BASE/projects/$FP/accounts:delete" 2>"$SCRATCH/auth-delete.err"); then
    fact auth-user FAIL "the Firebase Auth user $U3_UID that this run's accepted exchange created could not be deleted: curl failed before any HTTP response ($(first_line "$SCRATCH/auth-delete.err")). Delete it yourself at $FP's Identity Toolkit ('$TOOLKIT_BASE/projects/$FP/accounts:delete' with {\"localId\":\"$U3_UID\"} and your own access token)"
    return 0
  fi
  if [ "$code" != 200 ]; then
    fact auth-user FAIL "Identity Toolkit refused the delete of the Auth user $U3_UID (HTTP $code, $FP): the account this run's exchange created is STILL in the project's user table. The access token is your own, so a 403 here is your own right on $FP's Identity Toolkit ($(first_line "$SCRATCH/auth-delete.json"))"
    return 0
  fi
  # The read-back: an accounts:lookup for that one uid, whose body takes a LIST
  # of localIds where the delete's takes one string. The lookup's own status is
  # read first, because a document this check could not read would otherwise be
  # counted as the empty user list — a row that reports "gone" off a call that
  # never answered.
  (umask 077 && printf '{"localId":["%s"]}\n' "$U3_UID" >"$SCRATCH/auth-lookup-body.json") || true
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -X POST \
    -H 'Content-Type: application/json' --config "$AUTH_CONF" \
    -d "@$SCRATCH/auth-lookup-body.json" -o "$SCRATCH/auth-lookup.json" -w '%{http_code}' \
    "$TOOLKIT_BASE/projects/$FP/accounts:lookup" 2>"$SCRATCH/auth-lookup.err"); then
    fact auth-user FAIL "the Auth user $U3_UID was deleted (HTTP 200) but the read-back did not answer: curl failed before any HTTP response ($(first_line "$SCRATCH/auth-lookup.err")), so whether it is gone is not known"
    return 0
  fi
  if [ "$code" != 200 ]; then
    fact auth-user FAIL "the Auth user $U3_UID was deleted (HTTP 200) but the read-back answered HTTP $code rather than 200: an unauthenticated or refused document is not a user list, and this row is the count ($(first_line "$SCRATCH/auth-lookup.json"))"
    return 0
  fi
  local left
  left=$(python3 - "$SCRATCH/auth-lookup.json" <<'PY'
import json
import sys

try:
    d = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(1)
print("%d" % len(d.get("users") or []))
PY
  ) || left=""
  if [ -z "$left" ]; then
    fact auth-user FAIL "the read-back of the Auth user $U3_UID is not the document this check reads, so whether it is gone is not known: the account was deleted with a 200, and the lookup's own answer could not be parsed"
    return 0
  fi
  if [ "$left" = 0 ]; then
    fact auth-user PASS "the Firebase Auth user this run's exchange created ($U3_UID, created in $FP by the first accepted exchange) is gone: deleted through the project's own Identity Toolkit (accounts:delete answered 200) and read back with an accounts:lookup for that uid whose own answer holds 0 user(s). Nothing this check created survives it"
  else
    fact auth-user FAIL "the project's Identity Toolkit still answers with $left user(s) for the uid this run's exchange created ($U3_UID): the delete did not do what the run asked, and the read-back IS the assertion"
  fi
  return 0
}

# print_plan: the calls a real run would make, printed by --dry-run INSTEAD of
# running anything. This check's own test pins that a dry run makes no gcloud and
# no curl call at all, so the plan names the calls' shapes and never makes one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would, in this order:"
  say "  [local, reads] gcloud auth print-access-token --quiet — written into a 0600 file this run's 0700 scratch directory holds; curl reads it with --config, and it is never printed and never on a command line"
  say "  [reads] gcloud run jobs describe $PROBE_JOB --project $P --region $L — the guard that the name this check creates is free, BEFORE anything is created"
  say "  [reads] gcloud run jobs describe $JOB --project $P --region $L --format=json — the account this installation's jobs run as, and the Firebase web key, which goes straight into the 0600 config file the exchange is addressed with"
  say "  [creates ONE job] gcloud run jobs create $PROBE_JOB --project $P --region $L --image $PROBE_IMAGE --command /bin/bash --args=<the probe, base64> --service-account <that account>"
  say "  [creates ONE execution] curl -X POST $RUN_URL_BASE/projects/$P/locations/$L/jobs/$PROBE_JOB:run -d '{\"overrides\":{\"containerOverrides\":[{\"env\":[{\"name\":\"$MARKER_ENV\",\"value\":\"<a marker this run makes>\"}]}]}}' — the override the launcher writes, and the one call gcloud has no flag for"
  say "  [reads] curl $RUN_URL_BASE/<the execution's name> — the API's run.executions.get, which U2 is read off"
  say "  [reads] gcloud logging read resource.type=cloud_run_job job_name=$PROBE_JOB — the probe's own lines: the claims of the ID token the execution mints, which U1 is read off"
  say "  [reads] gcloud iam roles describe $LAUNCHER_ROLE --project $P — whether the launcher role carries run.executions.get"
  say "  [reads, signs U3] curl -X POST $IAMCRED_URL/<signer>:signJwt — one Firebase custom token signed as the runs' signer, never printed"
  say "  [reads, U3] curl -X POST $IDENTITY_URL — TWICE, the same token, to see whether Firebase exchanges it more than once. The token and the ID tokens it answers with are in 0600 files and are never printed"
  say "  [deletes] gcloud run jobs delete $PROBE_JOB --project $P --region $L --quiet, then a describe that must say the job is gone: the ONE job this check creates is deleted again and the deletion is read back"
  say "  [deletes] curl -X POST $TOOLKIT_BASE/projects/$FP/accounts:delete, then an accounts:lookup for the same uid: the Firebase Auth user the accepted exchange CREATES (a sign-in makes the account) is deleted and read back gone"
  say "Nothing else is created, changed or deleted, no Fugaro job is launched and no run starts: this dry run made no gcloud and no curl call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project region job probe belong xtrace token job-account probe-run
  probe-exec claims override-echo u1 launcher-perm u2 mint u3-first u3-second u3
  verdict restore auth-user scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 840 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 840 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer a
  # FAILed u-row as an answer when the read that would have filled it never ran.
  # Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the execution"
    fi
    # A stop that deleted the job is not an abort of the Restore: 2 is the
    # could-not-tell the run already is.
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  # The credentials and the scratch directory are this check's own, and both are
  # read back rather than assumed: a run that leaves a live access token, a web
  # key or a custom token on disk has to say so.
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"scratch-removed|$([ "$CREDENTIALS_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory and every 0600 file in it $([ "$CREDENTIALS_REMOVED" = yes ] && echo "are gone (the access token, the web key's config file, the custom token and the exchange's body)" || echo "IS STILL THERE at $SCRATCH, with a credential in it — remove it yourself")"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  else
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured, deleting what this check created and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
parse_args() {
  P=""
  L=""
  JOB=""
  SIGNER=""
  DRY_RUN=${CHECK840_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project ID. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a Cloud Run region. usage: $USAGE"
      L=$2
      shift 2
      ;;
    --job)
      [ $# -ge 2 ] || die "--job needs the name of a job the installation already runs. usage: $USAGE"
      JOB=$2
      shift 2
      ;;
    --signer)
      [ $# -ge 2 ] || die "--signer needs a service account email. usage: $USAGE"
      SIGNER=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$P" ] || die "--project is required: it is the installation's own GCP project, and every call below is addressed to it. usage: $USAGE"
  [ -n "$L" ] || die "--region is required: it is the Cloud Run region the installation's jobs run in. usage: $USAGE"
  [ -n "$JOB" ] || die "--job is required: it is a job the installation already runs, and this check reads the account it runs as and its Firebase web key off it. usage: $USAGE"
  [ -n "$SIGNER" ] || SIGNER="${SIGNER_DEFAULT}@${P}.iam.gserviceaccount.com"
}

main() {
  # Every file this check writes is the owner's alone. curl's `-o` writes the
  # body it answered with at this PROCESS's umask, not at any mode a config file
  # could set, so without this line the file holding the minted custom token
  # (signjwt.json) and the files holding the ID tokens Firebase answers with
  # (signin.N.json) land 0644: the scratch directory's 0700 is what keeps another
  # user out of them, and this is what makes the file itself owner-only, which is
  # what the header above promises. Set before anything is written, so it holds
  # for every file of the run.
  umask 077
  # The arguments first, so that `--help` and a missing flag answer on a machine
  # that has none of the tools: a usage line is not a run.
  parse_args "$@"
  need_tools
  scratch_up
  guard_xtrace
  fact project PASS "$P (typed by the owner; the project this check creates its one job in)"
  fact region PASS "$L (typed by the owner; the Cloud Run region that job is created in)"
  fact job PASS "$JOB (typed by the owner; the installation's own job this check reads the account and the web key off, and never launches)"
  guard_belong
  guard_project
  guard_region
  guard_probe_names
  if [ -n "$DRY_RUN" ]; then
    fact probe PASS "not created: this is a dry run, and a dry run makes no call at all"
    fact dry-run PASS "guards and the plan only: nothing was read from the project, nothing was created, and no gcloud and no curl call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle.
    # It reached its end -- the plan IS the end of a dry run -- so the exit trap
    # must not read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(project region job probe belong xtrace dry-run scratch-removed)
    return 0
  fi
  mint_or_stop
  guard_probe_is_free
  read_job
  create_probe
  launch_probe
  read_execution
  judge_u2
  if ! read_probe_log; then
    fact claims FAIL "the execution's own log lines were never found WHOLE in ${LOG_TRIES} read(s) of $P's Cloud Logging for $PROBE_JOB — its block is read only when the probe's own closing line (CHK840 end=<n>) is there WITH the claims it announces, and ${PROBE_NOTE:-no read answered} — so the identity token's claim set was never read. Its lines are written under resource.type=cloud_run_job, and ingestion lags the execution line by line; raise CHECK840_LOG_TRIES and run again, or read them by hand ($(first_line "$SCRATCH/log.err"))"
    stop "the execution's own log lines did not arrive whole (the row above), so the identity token's claims were never read. Nothing is concluded about U1"
  fi
  judge_u1
  read_launcher_role
  judge_u3
  step_verdict
}

CURL_SECONDS=${CHECK840_CURL_SECONDS:-60}
POLL_TRIES=${CHECK840_POLL_TRIES:-30}
POLL_SECONDS=${CHECK840_POLL_SECONDS:-2}
LOG_TRIES=${CHECK840_LOG_TRIES:-12}
LOG_SECONDS=${CHECK840_LOG_SECONDS:-5}
EXEC_FULL=""
EXEC_SHORT=""
EXEC_UID=""
MARKER_VALUE=""
JOB_SA=""

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
