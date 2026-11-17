#!/usr/bin/env bash
# Check 591 — which of the installation's Bitbucket tokens serves WHICH
# repository, and the refusal of a call that is not a run — as ONE script YOU
# run.
#
# The release serves each proxied request with the narrowest token whose
# recorded coverage admits the repository (R2, T4, D2355): a repository token
# before a project token, and a project token only for the repositories its own
# project lookup confirms. What only live tokens settle is that the choice
# really happens per REQUEST and per repository: a job run on two repositories
# of the same installation, one of them covered by a repository token and the
# other only by a project token, must be served by one token in the first and
# by the other in the second — and the service's own audit line is where the
# name of the token that served each request is written down (R9, C1.13).
#
# The check launches TWO runs — one per repository, because the released CLI has
# no change-set launch (`fugaro run --change-set` is Subscription Edition and
# not available in any build yet, so a "two-repository job" here is two launches
# of one task) — waits for both to reach a final status, and then reads the
# credentials service's lines for each run: the credential NAMES on its git and
# Bitbucket-API lines, which is the only place the service records which token
# a request was served with. It reads nothing about a token out of the run's
# own reports.
#
# It also makes ONE refused call of its own, live: the credentials service's
# front door, asked for the very path a run's git traffic takes, with an
# identity token and NO run identity, must answer HTTP 403
# `unverified_run_identity` — the service refuses a caller that is not a run of
# the installation BEFORE any route and therefore before any token is chosen
# (internal/credsvc/server.go's shared parse, wire.go's StatusUnverified and
# ErrorUnverifiedRunIdentity). A run cannot earn that 403: an identity that is
# not admitted is refused `unverified_run_identity` too, a coverage refusal is
# HTTP 400 with the class on the audit line, and the run's pairing fixes it to
# its own repository. So this is the ONE 403 a check at your terminal can force
# from the service, and the row says exactly which reading it is.
#
# The PREMISES it reads first are what make the swap a real choice rather than a
# wording of this check's: with the two tokens to hand — each in its own 0600
# file, never printed, never on a command line — it asks Bitbucket directly and
# records that the repository token reads the first repository and NOT the
# second, while the project token reads both. And `fugaro creds ls --json` is
# read to confirm the two RECORDS are the ones this check's sentences assume:
# the repository token covering the first repository alone, the project token
# covering (at least) the second, both present in the installation's bundle.
#
# Usage:      bash scripts/live-checks/check591.sh \
#               --repo <workspace>/<first repo> \
#               --other-repo <workspace>/<second repo> \
#               --token-file <path> --project-token-file <path> \
#               (--project-uuid <uuid> | --project-key <key>) \
#               [--repo-token-name <name>] [--project-token-name <name>] \
#               [--project <installation>] [--gcp-project <project>] \
#               [--region <region>] [--workspace <slug>] [--dry-run]
#             --dry-run  prints the plan and touches nothing.
#             Each token file holds ONE token and nothing else — one line, no
#             quotes, no JSON — at mode 0600. They are the ONE place the values
#             live: this script never puts one on a command line, never prints
#             one, and hands each to the reader, which writes it into a 0600
#             curl config inside its scratch directory, removed on the way out
#             including on a Ctrl-C.
#             --repo is the repository the REPOSITORY token was created for, and
#             --other-repo the one only the project token reaches: both are
#             repositories of the SAME Bitbucket project (the one the project
#             token was scoped to), which is what makes two repositories, two
#             tokens and one installation a real reading of the selection. Run
#             Check 590 first if the two records do not exist yet: its three
#             `creds set` writes name them bitbucket-token--ck590-<type>, which
#             is this check's default.
#             --repo-token-name and --project-token-name name the two RECORDS in
#             the installation (the bundle key bitbucket-token--<name>): the
#             default is what Check 590 wrote, and a check pointing at a
#             different installation's records is refused by name.
#             --project-uuid is the Bitbucket project's UUID (braces included)
#             and --project-key its key; one of them is required, and never both.
# Needs:      bash, mktemp, python3 (the reader), curl, gcloud, fugaro. It does
#             NOT need a checkout and does NOT need git: nothing here runs git,
#             and the two runs it launches do their own cloning and pushing in
#             the cloud.
# Where:      the installation `fugaro` in the GCP project `fugaro-dev` and the
#             Bitbucket sandbox workspace `dimi_bmb` ONLY — all three are
#             hard-coded in this file and overridable by CHECK591_INSTALLATION,
#             CHECK591_GCP_PROJECT and CHECK591_WORKSPACE. It never reads
#             `gcloud config get-value project`, it refuses a doctor that names
#             another installation or another GCP project, and it refuses
#             `belong` by name — as the installation, as the workspace, as the
#             first repository's workspace and as the second's. Sandbox
#             repositories only.
# Costs:      TWO full runs of the task it writes — each clones, implements,
#             reviews and opens a pull request, so two runs' worth of model
#             spend, two branches and two pull requests on the sandbox
#             repositories — plus a handful of read-only API calls and one
#             request that is refused. No write is made to the installation: no
#             `creds set`, no policy publish, no `fugaro update`. Both runs go
#             through the credentials service, which requires both repositories
#             to be on the credentials-service switch (agent.auth: cascade) —
#             the audit rows are where that is read.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check591_test.sh) runs it against fakes and
#             never touches Bitbucket, gcloud or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: the scratch directory and every
#             file a credential was written into are removed, and the block's
#             `restore` row says whether they really went — a FAILed row there
#             names what to remove by hand. What a person has to undo is what
#             the runs made: two branches and two pull requests on the sandbox
#             repositories, which the register's Restore names.
# Output:     progress on stderr, ONE block on stdout headed CHECK 591 RESULT,
#             to copy back to the coordinator. Its last line is the verdict, and
#             the verdict is the exit status:
#
#               CHECK 591 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 591 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 591 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything (a
#                                                      guard stopped it, a tool is
#                                                      missing, a command was
#                                                      refused, an error ended it);
#               CHECK 591 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one criterion was
#                                                      NOT settled: a row reading
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS: a check that measured most of its
#             criteria and left one unmeasured is INCOMPLETE, not a pass, and the
#             register's section is read against that word.
#
# NO SECRET IN ANY OUTPUT is a property of the design, not a promise kept by
# care. A token is read from its file by the reader, which writes it into a 0600
# curl config (`Authorization: Bearer …`); the identity token this check mints
# for its one refused call is written into a 0600 file and from there into a
# second 0600 curl config, and never printed. What the reader prints is a fixed
# vocabulary: a count, a status word, a credential NAME, a repository slug, a
# coverage pattern, `present`/`absent`, `yes`/`no` and one wire error type out of
# the service's own set. Nothing from a response is printed as it arrived, so an
# endpoint that echoes back what it was given cannot reach the block. And no
# trace: `bash -x` prints every command as it runs, so guard_xtrace REFUSES a run
# whose shell arrived with tracing on.
#
# CHECK591_API_URL, CHECK591_SERVICE_URL, CHECK591_CURL_SECONDS,
# CHECK591_RUN_SECONDS, CHECK591_RUN_POLL_SECONDS, CHECK591_AUDIT_WAIT_SECONDS,
# CHECK591_AUDIT_POLL_SECONDS, CHECK591_DRY_RUN and the LOG_* knobs exist for
# this script's own test: the first two may only name a loopback address of
# exactly the form http://127.0.0.1:<port> (the test's fake Bitbucket and its
# fake service front door), so no environment can send a token anywhere but
# Bitbucket Cloud's own API and the installation's own service.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below pass
# file names that hold a credential.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the selection (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 591 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 591 needs bash"
  need mktemp
  need python3 "the reader that handles the tokens and parses the JSON is one python3 file"
  need curl
  need gcloud "the service's own lines are read with gcloud logging read, and the front door is asked with an identity token gcloud mints"
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk591-mine"
SCRATCH_REMOVED=no
CREDENTIALS_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
# Every file a credential was written into: the two curl configs that carry the
# tokens, the file the minted identity token lands in and the curl config
# written from it, and the logs a command's own output goes to (an error message
# can quote a URL, and a URL is not a credential — but the same files are where
# a command's own words land, so they go with the rest). Their removal is READ,
# not assumed: the `restore` row reports it.
CREDENTIAL_FILES=()
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk591.XXXXXXXX") || die "Check 591 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 591 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  local f
  stop_call
  for f in ${CREDENTIAL_FILES[@]+"${CREDENTIAL_FILES[@]}"}; do
    rm -f "$f" 2>/dev/null || true
  done
  CREDENTIALS_REMOVED=yes
  for f in ${CREDENTIAL_FILES[@]+"${CREDENTIAL_FILES[@]}"}; do
    [ ! -e "$f" ] || CREDENTIALS_REMOVED=no
  done
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard is what keeps the removal from ever reaching the
  # owner's home, and reading it unguarded would abort cleanup under `set -u` on
  # a machine where HOME is unset, leaving the scratch directory behind.
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

finish() {
  local status=$?
  trap - EXIT ERR
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the selection"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so no file holds a credential and there is nothing to remove"
  else
    [ "$CREDENTIALS_REMOVED" = yes ] && [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local cred_note="the two curl configs the tokens were written into, the identity token's own file and its curl config, and the logs a command's own words were written into are removed"
    [ "$CREDENTIALS_REMOVED" = yes ] || cred_note="A FILE HOLDING A CREDENTIAL IS STILL THERE — look in $SCRATCH yourself and remove it: it holds a token's value or this check's minted identity token in a curl config"
    facts="${facts}"$'\n'"restore|$restore|${cred_note}, and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
  fi
  # The status the block's last line carries, decided here because the restore
  # row above is a criterion like any other and exists only now. A finished run
  # whose restore row FAILed exits 1 (main decided 0 before that row existed);
  # a finished run with a criterion that is not a PASS row — a row reading
  # UNVERIFIED, a key nobody wrote — exits 3, because a block that measured most
  # of its criteria and left one unmeasured is not a pass; and a run that stopped
  # before it could measure everything keeps 2 and reads INCOMPLETE, because a
  # guard, a missing tool or an error is not a criterion that failed.
  #
  # PRINTED says the run has already decided its own ending and its own status:
  # --dry-run prints the plan and --help the usage, and neither is a run whose
  # criteria were left unmeasured — there are no criteria, and a plan that
  # measured nothing is the one thing that is not INCOMPLETE.
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then
      status=1
    elif [ -n "$(criteria_unmet "$facts")" ]; then
      status=3
    fi
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check591.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry a token or a command's environment"
}

# loopback_url: exactly http://127.0.0.1:<port> and nothing else. A value with
# userinfo is refused: curl reads everything before the '@' as a username and
# connects to the host AFTER it, so a request built from it would hand a token's
# header to a host of the environment's choosing.
loopback_url() {
  local rest port
  case "$1" in
  *@*) return 1 ;;
  http://127.0.0.1:*) rest=${1#http://127.0.0.1:} ;;
  *) return 1 ;;
  esac
  port=${rest%%/*}
  case "$port" in
  "" | *[!0-9]*) return 1 ;;
  esac
  case "$rest" in
  "$port" | "$port"/*) ;;
  *) return 1 ;;
  esac
  # A query or a fragment is refused too: the requests below are built by
  # appending a path to this value, and curl would send the tail of it as the
  # query rather than as the path the check means to read.
  case "${rest#"$port"}" in
  *'?'* | *'#'* | *' '* | *$'\t'*) return 1 ;;
  esac
  return 0
}

# service_url_ok: the two spellings a Cloud Run service's own URL may have here —
# the platform's https://<name>-<hash>-<region>.run.app, and the loopback address
# this check's own test serves its fake front door from. Anything else (userinfo,
# a query, a fragment, a space, another scheme, another host) is refused: this
# value is asked for a path that carries a run's own git traffic, so where it
# points is the whole of the property.
service_url_ok() {
  local url=$1 rest
  case "$url" in
  *@* | *'?'* | *'#'* | *' '* | *$'\t'*) return 1 ;;
  esac
  loopback_url "$url" && return 0
  case "$url" in
  https://*.run.app) ;;
  *) return 1 ;;
  esac
  rest=${url#https://}
  case "$rest" in
  */* | "") return 1 ;;
  esac
  return 0
}

guard_endpoints() {
  local value
  value=$(printenv CHECK591_API_URL 2>/dev/null || true)
  if [ -n "$value" ]; then
    loopback_url "$value" \
      || die "Check 591 refused: CHECK591_API_URL may only name a loopback address of the form http://127.0.0.1:<port> (with no userinfo, query, fragment or space), so a token can never leave this machine (it is set for this check's own test and for nothing else)"
  fi
  value=$(printenv CHECK591_SERVICE_URL 2>/dev/null || true)
  if [ -n "$value" ]; then
    loopback_url "$value" \
      || die "Check 591 refused: CHECK591_SERVICE_URL may only name a loopback address of the form http://127.0.0.1:<port> (with no userinfo, query, fragment or space): in a real run the service's URL is read from gcloud and held to service_url_ok, and this variable is set for this check's own test and for nothing else"
  fi
}

# guard_belong: `belong` is the installation and the workspace this check must
# never touch, and a script that can be pointed at it by a typo has no business
# launching runs that push with the owner's own tokens.
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

guard_belong() {
  local what value
  for what in installation gcp-project workspace repo other-repo repo-token-name project-token-name; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    gcp-project) value=$GCP_PROJECT ;;
    workspace) value=$WORKSPACE ;;
    repo) value=$REPO ;;
    other-repo) value=$OTHER_REPO ;;
    repo-token-name) value=$REPO_TOKEN_NAME ;;
    project-token-name) value=$PROJECT_TOKEN_NAME ;;
    esac
    [ -n "$value" ] || continue
    case "$(lower "$value")" in
    belong | belong/* | */belong | *--belong | belong--)
      die "Check 591 refused: '$value' (the $what) names 'belong', the installation and workspace this check must never touch. Nothing was sent anywhere"
      ;;
    esac
  done
}

# --------------------------------------------------------------- the requests
# request OUT METHOD URL [curl args...] — one request, its body in OUT, its
# status on stdout. Everything curl says goes to the scratch log: its messages
# can quote a request. A request that never got an answer is 000.
request() {
  local out=$1 method=$2 url=$3
  shift 3
  local code=000
  code=$(curl -sS -o "$out" -w '%{http_code}' --max-time "$CURL_SECONDS" \
    --request "$method" "$@" "$url" 2>>"$SCRATCH/curl.log") || code=000
  printf '%s' "$code"
}

# api_request OUT METHOD PATH [curl args...] — one request to Bitbucket Cloud's
# own API, and to nothing else: a redirect is REFUSED rather than followed,
# because the request carries a token.
api_request() {
  local out=$1 method=$2 path=$3
  shift 3
  request "$out" "$method" "$API_ROOT$path" "$@"
}

# service_request OUT METHOD PATH [curl args...] — one request to the
# installation's credentials service.
service_request() {
  local out=$1 method=$2 path=$3
  shift 3
  request "$out" "$method" "$SERVICE_URL$path" "$@"
}

redact_secrets() {
  printf '%s' "$1" | sed -E \
    -e 's/(([Pp]roxy-)?[Aa]uthorization[[:space:]]*:?[[:space:]]*)([Bb]asic|[Bb]earer|[Dd]igest)?[[:space:]]*[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Bb]earer[[:space:]]+)[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Aa]ccess[_-]?[Tt]oken|[Cc]lient[_-]?[Ss]ecret|[Rr]efresh[_-]?[Tt]oken|[Aa]pi[_-]?[Kk]ey|[Tt]oken)[[:space:]]*[:=][[:space:]]*[A-Za-z0-9._~+\/=-]{4,}/\1=<redacted>/g'
}

curl_note() {
  local text
  text=$(tail -n 1 "$SCRATCH/curl.log" 2>/dev/null | tr -d '\r' | tr '\n' ' ' | head -c 200 || true)
  [ -n "$text" ] || text="curl printed nothing"
  printf ' (curl said, with anything credential-shaped removed: %s)' "$(redact_secrets "$text")"
}

# last_line FILE: one bounded, redacted line of a command's own output, for a
# block that has to say why a step failed without pasting a log.
last_line() {
  local text
  text=$(grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | tr -d '\r' | tr '\n' ' ' | head -c 240 || true)
  [ -n "$text" ] || text="the command printed nothing"
  printf '%s' "$(redact_secrets "$text")"
}

# run_call SECONDS OUT LOG cmd... — one long command with a deadline of its own,
# in a process group of its own so the deadline reaches whatever the call
# started. `timeout(1)` is not used: it is not on macOS, where this check is also
# run.
CALL_PID=""
run_call() { # run_call SECONDS OUT LOG cmd...
  local secs=$1 out=$2 log=$3
  shift 3
  set -m
  "$@" </dev/null >"$out" 2>"$log" &
  CALL_PID=$!
  local waited=0
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      local pid=$CALL_PID
      stop_call
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  local status=0
  wait "$CALL_PID" || status=$?
  CALL_PID=""
  return "$status"
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

# ------------------------------------------------------------- the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 591's reader: the one place the tokens and the responses are handled.

The shell never holds a token. This program reads one out of the file the owner
named and writes it into the 0600 curl config the requests carry it in, and it
prints a fixed vocabulary -- counts, status words, credential NAMES, repository
slugs, coverage patterns, present/absent, yes/no, and one wire error type out of
the service's own set -- so a response that echoes back what it was given cannot
reach the check's result block through this.
"""
import json
import re
import sys

# Anything outside this set is dropped from a word before it is printed: a
# credential name, a purpose, a status and a slug are all text another writer
# chose, and this is the bound on what reaches a line of the block.
WORD = re.compile(r"[^A-Za-z0-9._:@/+-]")
# A repository's owner/name, as this check is willing to print it.
REPO = re.compile(r"[A-Za-z0-9._-]+/[A-Za-z0-9._-]+")
# One coverage entry in the operator's own spelling (R1): a repository
# (ws/repo), a whole workspace (ws/*), a project token's workspace slug (ws), or
# the same pattern as the published node spells it (bitbucket/ws/repo).
COVERAGE = re.compile(r"[A-Za-z0-9._-]+(?:/[A-Za-z0-9._*-]+){0,2}")
# A run id, as the listing spells it.
RUNID = re.compile(r"[A-Za-z0-9._-]+")
# The two spellings a service URL may have here (service_url_ok holds the same
# rule on the shell's side).
URL = re.compile(r"https://[A-Za-z0-9.-]+\.run\.app")
LOOPBACK = re.compile(r"http://127\.0\.0\.1:[0-9]{1,5}")
# What a value may look like to be written into a config file: one line of
# printable ASCII with no space. Anything else is refused, and its own text is
# never printed -- the message names the FILE.
VALUE = re.compile(r"[\x21-\x7e]+")


def die(message):
    print(message, file=sys.stderr)
    sys.exit(1)


def load(path, what):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        die("%s is not readable JSON" % what)


def word(v, cap=80):
    return WORD.sub("", str(v))[:cap] or "-"


def safe(value, limit=120):
    return value if isinstance(value, str) and VALUE.fullmatch(value) and len(value) <= limit else ""


def token_of(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            raw = fh.read()
    except OSError:
        die("the token file could not be read")
    token = raw.strip()
    if not token:
        die("the token file is empty")
    if not VALUE.fullmatch(token):
        die("the token file must hold one line of printable text without spaces (the token's value and nothing else)")
    return token


def write_private(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, text.encode("utf-8"))
    finally:
        os.close(fd)
    os.chmod(path, 0o600)


def cmd_tokenfile(path, conf):
    """One token file's own facts, and the curl config that carries the value:
    `Authorization: Bearer <token>`, which is how Bitbucket Cloud's API takes an
    access token of any of the three types (U5's REST half). The same config
    serves this check's premise reads and nothing else -- the runs it launches
    hold no token at all."""
    token = token_of(path)
    mode = os.stat(path).st_mode & 0o777
    write_private(conf, 'header = "Authorization: Bearer %s"\nheader = "Accept: application/json"\n'
                  % token.replace("\\", "\\\\").replace('"', '\\"'))
    print("mode=%o" % mode)
    print("bytes=%d" % len(token))


def cmd_idconf(path, conf):
    """The curl config for the identity token this check mints for its ONE forced
    refusal: a Bearer header, and nothing else -- no run identity header, which is
    exactly what the call is about."""
    token = token_of(path)
    write_private(conf, 'header = "Authorization: Bearer %s"\n' % token.replace("\\", "\\\\").replace('"', '\\"'))
    print("header=written")


def cmd_doctor(path):
    doc = load(path, "fugaro doctor --json")
    project = doc.get("project") if isinstance(doc, dict) else None
    if not isinstance(project, dict):
        print("project=")
        print("gcp_project=")
        print("region=")
        return
    print("project=%s" % safe(project.get("name")))
    print("gcp_project=%s" % safe(project.get("gcp_project")))
    print("region=%s" % safe(project.get("region")))


def cmd_service(path):
    """The credentials service's own URL, from `gcloud run services describe`.
    Only the two spellings this check is willing to send a run's path to are
    printed; anything else reads as no URL at all."""
    doc = load(path, "gcloud run services describe")
    url = ""
    if isinstance(doc, dict):
        status = doc.get("status")
        candidate = status.get("url") if isinstance(status, dict) else None
        if isinstance(candidate, str) and (URL.fullmatch(candidate) or LOOPBACK.fullmatch(candidate)):
            url = candidate
    print("url=%s" % url)
    print("exists=%s" % ("yes" if url else "no"))


def cmd_repo(path):
    doc = load(path, "the repository")
    if not isinstance(doc, dict):
        print("full_name=")
        print("project=")
        print("error=-")
        return
    full = doc.get("full_name")
    print("full_name=%s" % (full if isinstance(full, str) and REPO.fullmatch(full) else ""))
    project = doc.get("project")
    key = project.get("key") if isinstance(project, dict) else None
    print("project=%s" % safe(key))
    error = doc.get("error")
    code = error.get("code") if isinstance(error, dict) else error
    print("error=%s" % (word(code, 40) if code else "-"))


def cmd_creds(path, name_token, name_project):
    """`fugaro creds ls --json`'s two rows: whether the two records this check's
    sentences are about exist, what TYPE each states and what COVERAGE the
    operator recorded (R1's own spelling, which is what the published node
    derives the selection's patterns from). The listing's rows carry no value,
    so nothing of a token can pass through here."""
    doc = load(path, "fugaro creds ls --json")
    rows = doc.get("credentials") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        rows = []
    for key, name in (("token", name_token), ("project", name_project)):
        row = None
        for r in rows:
            if isinstance(r, dict) and r.get("name") == name:
                row = r
                break
        if row is None:
            print("present-%s=missing" % key)
            print("type-%s=-" % key)
            print("coverage-%s=-" % key)
            print("state-%s=-" % key)
            continue
        print("present-%s=%s" % (key, "yes" if row.get("present") is True else "no"))
        print("type-%s=%s" % (key, word(row.get("type"), 40)))
        cov = row.get("coverage")
        entries = []
        if isinstance(cov, list):
            for c in cov:
                if isinstance(c, str) and COVERAGE.fullmatch(c) and len(c) <= 120:
                    entries.append(c)
        print("coverage-%s=%s" % (key, " ".join(entries) or "-"))
        print("state-%s=%s" % (key, word(row.get("state"), 40)))


def cmd_runid(path):
    doc = load(path, "fugaro run --json")
    run_id = doc.get("run_id") if isinstance(doc, dict) else None
    print("run_id=%s" % (run_id if isinstance(run_id, str) and RUNID.fullmatch(run_id) else ""))


def cmd_row(path, wanted):
    doc = load(path, "fugaro runs ls --json")
    rows = doc.get("runs") if isinstance(doc, dict) else None
    row = None
    if isinstance(rows, list):
        for r in rows:
            if isinstance(r, dict) and r.get("run_id") == wanted:
                row = r
                break
    print("found=%s" % ("yes" if row is not None else "no"))
    if row is None:
        print("status=")
        print("terminal=no")
        print("settled=no")
        print("pushed=no")
        print("pr=0")
        print("branch=absent")
        print("outcome=")
        return
    print("status=%s" % safe(row.get("status")))
    # Terminal is the listing's word for a run that reached a final status:
    # `fugaro run` returns at LAUNCH, so the first listing after it holds an
    # in-flight row.
    print("terminal=%s" % ("yes" if row.get("terminal") is True else "no"))
    print("settled=%s" % ("yes" if row.get("settled") is True else "no"))
    print("pushed=%s" % ("yes" if row.get("pushed") is True else "no"))
    pr = row.get("pr")
    print("pr=%d" % pr if isinstance(pr, int) and not isinstance(pr, bool) and pr > 0 else "pr=0")
    print("branch=%s" % ("present" if row.get("branch") or row.get("pushed") is True else "absent"))
    print("outcome=%s" % safe(row.get("outcome")))


def cmd_audit(path, want):
    """The credentials service's OWN lines for one run, as `gcloud logging read`
    answers them: which credential NAMES served its git and Bitbucket-API calls,
    and the statuses, purposes, classes and repositories the lines carry.

    This is the reading the whole check exists for: the service writes the
    DOCUMENT KEY of the token a request was served with on that request's line
    (R9), so a run's git lines name the tokens the proxy chose for it. Model
    calls are lines of the same run (purpose `forward`), which is why the
    credential is read only off the two purposes a Bitbucket call has."""
    entries = load(path, "gcloud logging read")
    if not isinstance(entries, list):
        entries = []
    lines = 0
    git_creds = []
    purposes = {}
    statuses = {}
    classes = {}
    repositories = set()
    for entry in entries:
        payload = entry.get("jsonPayload") if isinstance(entry, dict) else None
        if not isinstance(payload, dict) or payload.get("run") != want:
            continue
        lines += 1
        message = payload.get("message")
        if message == "credsvc: audit":
            purpose = word(payload.get("purpose"), 40)
            purposes[purpose] = purposes.get(purpose, 0) + 1
            status = word(payload.get("status"), 40)
            statuses[status] = statuses.get(status, 0) + 1
            repo = payload.get("repository")
            if isinstance(repo, str) and REPO.fullmatch(repo):
                repositories.add(repo)
            credential = word(payload.get("credential"), 120)
            if purpose in ("git", "bitbucket-api") and credential != "-":
                git_creds.append(credential)
        elif message == "credsvc: a Bitbucket call was refused":
            cls = word(payload.get("class"), 40)
            classes[cls] = classes.get(cls, 0) + 1
    print("lines=%d" % lines)
    print("git-lines=%d" % len(git_creds))
    print("git-credentials=%s" % (",".join(sorted(set(git_creds))) or "-"))
    print("purposes=%s" % (",".join("%s:%d" % kv for kv in sorted(purposes.items())) or "-"))
    print("statuses=%s" % (",".join("%s:%d" % kv for kv in sorted(statuses.items())) or "-"))
    print("refusals=%d" % sum(classes.values()))
    print("classes=%s" % (",".join("%s:%d" % kv for kv in sorted(classes.items())) or "-"))
    print("repositories=%s" % (",".join(sorted(repositories)) or "-"))


# The wire error types this check is willing to print: the service's own closed
# vocabulary (internal/credsvc/wire.go) and nothing else, so a body some other
# front end wrote cannot put its own text into the block.
WIRE_TYPES = {
    "no_bitbucket_token", "not_covered", "other_repository", "request_refused",
    "state_unavailable", "throttled", "broken", "exhausted_until", "not_found",
    "unauthorized", "unverified_job_identity", "unverified_run_identity",
}


def cmd_frontdoor(path):
    """The answer to this check's one refused call: whether the body is the
    service's OWN wire document, and which of its types it carries. A body that
    is not (an IAM refusal from the front end, a proxy's page) prints no type:
    this check reads only the service's own words, and its own words are a closed
    set."""
    doc = None
    try:
        with open(path, "r", encoding="utf-8") as fh:
            doc = json.load(fh)
    except (OSError, ValueError):
        doc = None
    if not isinstance(doc, dict) or not isinstance(doc.get("error"), str):
        print("wire=no")
        print("type=-")
        return
    print("wire=yes")
    value = doc.get("error")
    print("type=%s" % (value if value in WIRE_TYPES else "other"))


def main():
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]
    table = {
        "tokenfile": (cmd_tokenfile, 2),
        "idconf": (cmd_idconf, 2),
        "doctor": (cmd_doctor, 1),
        "service": (cmd_service, 1),
        "repo": (cmd_repo, 1),
        "creds": (cmd_creds, 3),
        "runid": (cmd_runid, 1),
        "row": (cmd_row, 2),
        "audit": (cmd_audit, 2),
        "frontdoor": (cmd_frontdoor, 1),
    }
    if command not in table or len(args) != table[command][1]:
        die("the reader was called with an argument list it does not know")
    table[command][0](*args)


if __name__ == "__main__":
    main()
READER_EOF
  chmod 600 "$SCRATCH/reader.py"
}

# ------------------------------------------------------------------ the steps
# Every fact's detail says what was MEASURED and what it means for the plan, and
# a step that could not be measured says so rather than reading as a pass.

step_installation() {
  local status rc=0
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  run_call "$CURL_SECONDS" "$SCRATCH/doctor.json" "$SCRATCH/doctor.err" \
    fugaro doctor --project "$INSTALLATION" --json || rc=$?
  if [ "$rc" = 0 ] || [ "$rc" = 1 ]; then status=ok; else status=failed; fi
  local facts
  facts=$(reader doctor "$SCRATCH/doctor.json" 2>/dev/null || true)
  PROJECT_NAME=$(field_of project "$facts")
  GCP_PROJECT_SEEN=$(field_of gcp_project "$facts")
  REGION_SEEN=$(field_of region "$facts")
  if [ "$status" != ok ]; then
    stop "fugaro doctor --project ${INSTALLATION} failed (exit ${rc}): $(last_line "$SCRATCH/doctor.err"). This check reads the installation's own project before it touches anything, and it needs the installation named on this machine with usable credentials"
  fi
  if [ "$(lower "${PROJECT_NAME:-}")" != "$(lower "$INSTALLATION")" ]; then
    stop "the local config selected by --project ${INSTALLATION} is the installation '${PROJECT_NAME:-none}': this check launches two runs on the installation you named and no other, so it stops rather than pushing branches with tokens of a project you did not mean"
  fi
  # The GCP project is hard-coded (never `gcloud config get-value project`): a
  # check that took the project from the environment could be pointed at another
  # installation's project by a stray variable, and both runs below push with
  # this project's tokens.
  if [ "$(lower "${GCP_PROJECT_SEEN:-}")" != "$(lower "$GCP_PROJECT")" ]; then
    stop "the installation '${PROJECT_NAME}' lives in GCP project '${GCP_PROJECT_SEEN:-none}' and this check runs on the sandbox installation in '${GCP_PROJECT}' only: 'belong' and every other installation are refused by name. Set CHECK591_GCP_PROJECT (and CHECK591_INSTALLATION) if you really mean another one, or point --project at the sandbox installation"
  fi
  fact installation PASS "installation '${PROJECT_NAME}' in GCP project ${GCP_PROJECT_SEEN}, region ${REGION:-unset} (doctor answered ${REGION_SEEN:-unset}), read from fugaro doctor --project ${INSTALLATION} at $(date -u +%Y-%m-%dT%H:%M:%SZ). The workspace both repositories and both tokens must belong to is ${WORKSPACE}, and it refuses 'belong' as an installation, as the workspace and as either repository's workspace"
}

# One token's own files: its mode, its size, and the 0600 curl config the premise
# reads below carry it in. Nothing here reads the value into the shell.
read_token_file() { # read_token_file LABEL FILE
  local label=$1 file=$2 facts mode bytes
  facts=$(reader tokenfile "$file" "$SCRATCH/$label.conf" 2>"$SCRATCH/reader.err" || true)
  [ -n "$facts" ] || stop "the ${label} token file $file could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). It holds one access token, one line and nothing else, and no value from it is printed. Nothing was sent anywhere"
  mode=$(field_of mode "$facts")
  bytes=$(field_of bytes "$facts")
  case "$mode" in
  600) ;;
  *) stop "the ${label} token file $file is mode ${mode:-?} and this check wants 0600: a token any other user of this machine can read is not one to hold beside the installation's own. Type 'chmod 600 $file' and run again. Nothing was sent anywhere" ;;
  esac
  case "$label" in
  repository) BYTES_REPOSITORY=$bytes ;;
  project) BYTES_PROJECT=$bytes ;;
  esac
}

step_inputs() {
  read_token_file repository "$TOKEN_FILE"
  read_token_file project "$PROJECT_TOKEN_FILE"
  fact inputs PASS "the two token files are mode 0600 (${BYTES_REPOSITORY} bytes for the repository token, ${BYTES_PROJECT} for the project token) and were read into two 0600 curl configs inside the scratch directory, so no value is on a command line or in this shell. The records they are expected to be are ${REPO_TOKEN_NAME} (repository) and ${PROJECT_TOKEN_NAME} (project); the bundle row below reads whether the installation holds exactly those"
}

# The two RECORDS, read from the installation's own listing: this check's whole
# sentence -- a run on ${REPO} is served by the repository token and a run on
# ${OTHER_REPO} by the project token -- holds because of what the records say, so
# a check that did not read them would be measuring a selection it assumed.
step_bundle() {
  local rc=0 got present_t present_p type_t type_p cov_t cov_p state_note
  run_call "$CURL_SECONDS" "$SCRATCH/creds.json" "$SCRATCH/creds.err" \
    fugaro creds ls --project "$INSTALLATION" --json || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact bundle FAIL "fugaro creds ls --project ${INSTALLATION} --json exited ${rc}, so the two records this check's sentences are about could not be read: $(last_line "$SCRATCH/creds.err"). Nothing was launched"
    stop "fugaro creds ls could not be read (exit ${rc}): this check has to read the records before it launches anything, because which token SERVES a repository is decided from them"
  fi
  got=$(reader creds "$SCRATCH/creds.json" "$REPO_TOKEN_NAME" "$PROJECT_TOKEN_NAME" 2>"$SCRATCH/reader.err") \
    || stop "fugaro creds ls --json wrote something this check cannot read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"
  present_t=$(field_of present-token "$got")
  present_p=$(field_of present-project "$got")
  type_t=$(field_of type-token "$got")
  type_p=$(field_of type-project "$got")
  cov_t=$(field_of coverage-token "$got")
  cov_p=$(field_of coverage-project "$got")
  # The four readings this check's sentences need, each named the way the owner
  # would fix it: both records present in the bundle, the repository token typed
  # and covering ${REPO} alone, the project token typed and covering
  # ${OTHER_REPO}. A repository token whose record also names ${OTHER_REPO} would
  # be chosen there too (R2's narrowest-first order) and the project token would
  # never serve anything.
  local bad=""
  [ "$present_t" = yes ] || bad="${bad}the record ${REPO_TOKEN_NAME} is not in the installation's bundle (present=${present_t:-unread}); "
  [ "$present_p" = yes ] || bad="${bad}the record ${PROJECT_TOKEN_NAME} is not in the installation's bundle (present=${present_p:-unread}); "
  [ "$type_t" = repo ] || bad="${bad}${REPO_TOKEN_NAME} states type '${type_t:-none}' where this check needs a repository token; "
  [ "$type_p" = project ] || bad="${bad}${PROJECT_TOKEN_NAME} states type '${type_p:-none}' where this check needs a project token; "
  if ! coverage_covers "$cov_t" "$REPO"; then
    bad="${bad}${REPO_TOKEN_NAME}'s coverage (${cov_t}) does not admit ${REPO}; "
  fi
  if coverage_covers "$cov_t" "$OTHER_REPO"; then
    bad="${bad}${REPO_TOKEN_NAME}'s coverage (${cov_t}) ALSO admits ${OTHER_REPO}, so the narrowest token would serve BOTH repositories and the swap below would not happen; "
  fi
  if ! coverage_covers "$cov_p" "$OTHER_REPO"; then
    bad="${bad}${PROJECT_TOKEN_NAME}'s coverage (${cov_p}) does not admit ${OTHER_REPO}; "
  fi
  state_note="state $(field_of state-token "$got") / $(field_of state-project "$got")"
  if [ -n "$bad" ]; then
    fact bundle FAIL "${bad}this check needs two records whose coverage makes the selection a real choice: a repository token recorded for ${REPO} alone, and a project token recorded for the workspace (or the repositories) and scoped to the Bitbucket project that holds both. Set them with 'fugaro creds set ${REPO_TOKEN_NAME} --type repo --coverage ${REPO} --project ${INSTALLATION}' and 'fugaro creds set ${PROJECT_TOKEN_NAME} --type project --coverage ${WORKSPACE} $(project_flag_for_usage) --project ${INSTALLATION}', then run 'fugaro update' to publish the coverage — a record is this machine's alone until the node carries it. Nothing was launched"
    stop "the two records are not the ones this check's sentences are about (see the bundle row)"
  fi
  fact bundle PASS "the installation holds both records as this check's sentences need: ${REPO_TOKEN_NAME} is a repository token covering ${REPO} and NOT ${OTHER_REPO} (coverage: ${cov_t}), and ${PROJECT_TOKEN_NAME} is a project token covering ${OTHER_REPO} (coverage: ${cov_p}); both present in the installation's bundle, ${state_note} (fugaro creds ls --project ${INSTALLATION} --json). A record is read here and never a value"
}

# coverage_covers PATTERNS REPO — whether any entry of the operator's own
# coverage list (R1: ws/repo for a repository token, ws/* for a workspace token,
# the workspace slug for a project token) admits the repository, exactly as
# bbtokens' own covers does over the node's qualified patterns.
coverage_covers() {
  local patterns=$1 repo=$2 entry ws=${2%%/*}
  for entry in $patterns; do
    case "$entry" in bitbucket/*) entry=${entry#bitbucket/} ;; esac
    case "$entry" in
    "$repo" | "$ws/*" | "$ws") return 0 ;;
    esac
  done
  return 1
}

# The premise the swap rests on, read from Bitbucket with each token itself: the
# repository token reaches ${REPO} and NOT ${OTHER_REPO}, and the project token
# reaches both. It is the F7/F9 reading, and it is what tells "the installation
# happens to have two tokens" from "the repository token CANNOT serve the second
# repository, so a run there served by another token is a choice".
step_premises() {
  local sr_a sr_b pr_a pr_b
  sr_a=$(api_request "$SCRATCH/a-repo.json" GET "/repositories/${REPO}" --config "$SCRATCH/repository.conf")
  sr_b=$(api_request "$SCRATCH/b-repo.json" GET "/repositories/${OTHER_REPO}" --config "$SCRATCH/repository.conf")
  pr_a=$(api_request "$SCRATCH/a-project.json" GET "/repositories/${REPO}" --config "$SCRATCH/project.conf")
  pr_b=$(api_request "$SCRATCH/b-project.json" GET "/repositories/${OTHER_REPO}" --config "$SCRATCH/project.conf")
  local err_note
  err_note=$(field_of error "$(reader repo "$SCRATCH/b-repo.json" 2>/dev/null || true)")

  if [ "$sr_a" = 200 ]; then
    case "$sr_b" in
    403 | 404)
      fact premise-repo PASS "the repository token ${REPO_TOKEN_NAME} reads ${REPO} over REST (HTTP ${sr_a}) and is REFUSED ${sr_b} at ${OTHER_REPO}${err_note:+ (Bitbucket code ${err_note})}: Bitbucket itself says this token cannot serve ${OTHER_REPO}, so a run there served by another token is a real choice of the proxy's and not a wording of this check's (F7/F9, R2). The project token's own reading is the row below"
      ;;
    200)
      fact premise-repo PASS "the repository token ${REPO_TOKEN_NAME} reads both ${REPO} and ${OTHER_REPO} over REST (HTTP 200 both times), so the token itself reaches ${OTHER_REPO} as well: the selection below is the design's narrowest-first order over the RECORDS (its recorded coverage admits ${REPO} alone, read by the bundle row above) rather than a necessity at Bitbucket, and the audit lines still name which token served each run. Record this reading against the register's premise — the stronger premise needs a repository token created for ${REPO} alone"
      ;;
    *)
      fact premise-repo UNVERIFIED "the repository token answered HTTP ${sr_b} at ${OTHER_REPO}, which is neither a 200 nor the 403/404 that means 'does not cover it' (${sr_a} at ${REPO}), so whether it could serve ${OTHER_REPO} was NOT measured — a 000 is no answer at all, and a 401 or 500 is about the token or about Bitbucket rather than about its coverage$(curl_note)"
      ;;
    esac
  else
    fact premise-repo FAIL "the repository token ${REPO_TOKEN_NAME} answered HTTP ${sr_a} for ${REPO}, so this token does not read the repository it is recorded for${err_note:+ (Bitbucket code ${err_note})}: a token whose own coverage it cannot read is one no run below is served by, and the swap would be measured from a broken premise$(curl_note)"
  fi

  case "$pr_a:$pr_b" in
  200:200)
    fact premise-project PASS "the project token ${PROJECT_TOKEN_NAME} reads BOTH repositories over REST with its own Bearer config (HTTP 200 for ${REPO} and for ${OTHER_REPO}), so it is the token that can serve ${OTHER_REPO}; with the repository token refused there (the row above), a run on ${OTHER_REPO} has exactly one token to be served by"
    ;;
  200:*)
    fact premise-project FAIL "the project token reads ${REPO} (HTTP 200) but answered HTTP ${pr_b} for ${OTHER_REPO}: a project token's coverage is the project it is scoped to, so either ${OTHER_REPO} is not in that Bitbucket project (both repositories have to be in the one the token was scoped to), or the token lacks the scopes for it. Fix that before reading anything below$(curl_note)"
    ;;
  *)
    fact premise-project UNVERIFIED "the project token answered HTTP ${pr_a} for ${REPO} and HTTP ${pr_b} for ${OTHER_REPO}, which does not settle that it reaches both (only two 200s do)$(curl_note)"
    ;;
  esac
}

# One run of the task, on one repository, and its final row.
#
# `fugaro run` RETURNS AT LAUNCH, with the launch result (status launched, or
# already-launched for a run that was already going): the run itself goes on in
# the cloud, so its row in `fugaro runs ls` is an in-flight one for a while — no
# push, no pull request, terminal=false — and this waits for THAT run's row to
# reach a final status before it reads anything out of it.
step_run() { # step_run LABEL REPO
  local label=$1 repo=$2 rc=0
  local task="$SCRATCH/task-${label}.md" json="$SCRATCH/run-${label}.json" log="$SCRATCH/run-${label}.log"
  cat >"$task" <<TASK_EOF
Add a line to the repository's README.md that says the repository was used for Fugaro's Check 591
($(date -u +%Y-%m-%d)), and open the pull request. Change nothing else: this task exists so a run
clones, pushes and opens a pull request through the installation's credentials service, and Check 591
reads which of the installation's Bitbucket tokens served that traffic.
TASK_EOF
  CREDENTIAL_FILES+=("$log")
  say "launching one run in ${repo} (this is a long step: a run clones, implements, reviews and opens its pull request)"
  run_call "$LAUNCH_SECONDS" "$json" "$log" fugaro run --repo "$repo" --project "$INSTALLATION" \
    --title "Check 591: which Bitbucket token serves which repository (${repo})" \
    --task-file "$task" --json || rc=$?
  local run_id row
  # The run the check waits for is the one THIS launch named: the listing is
  # newest-first and holds the runs of the whole window, so the row is picked by
  # run_id and never by position.
  run_id=$(field_of run_id "$(reader runid "$json" 2>/dev/null || true)")
  case "$label" in
  run-a) RUN_A=$run_id ;;
  run-b) RUN_B=$run_id ;;
  esac
  say "the run in ${repo} is launched and now goes on in the cloud; waiting up to ${RUN_SECONDS}s for that run to finish"
  row=$(wait_for_run "$label" "$repo" "$run_id" "$log")
  set_run_result "$label" "$repo" "$run_id" "$row" "$rc" "$log"
}

# wait_for_run LABEL REPO RUNID LOG — the final row of one run, as the reader
# prints it, with a `poll=` word saying how the wait ended.
wait_for_run() { # wait_for_run LABEL REPO RUNID LOG
  local label=$1 repo=$2 run_id=$3 log=$4 waited=0 row=""
  [ -n "$run_id" ] || { printf 'poll=norunid\n'; return 0; }
  while :; do
    row=""
    # No --limit: the row being waited for is one run out of the window, so the
    # listing read is the window's own and the row is picked out of it by
    # run_id.
    if fugaro runs ls --repo "$repo" --project "$INSTALLATION" --json \
      >"$SCRATCH/rows-${label}.json" 2>>"$log"; then
      row=$(reader row "$SCRATCH/rows-${label}.json" "$run_id" 2>>"$log" || true)
    fi
    if [ "$(field_of found "$row")" = yes ] && [ "$(field_of terminal "$row")" = yes ]; then
      printf 'poll=terminal\n%s\n' "$row"
      return 0
    fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then
      printf 'poll=timeout\n%s\n' "$row"
      return 0
    fi
    sleep "$RUN_POLL_SECONDS"
    waited=$((waited + RUN_POLL_SECONDS))
  done
}

set_run_result() { # set_run_result LABEL REPO RUNID ROW RC LOG
  local label=$1 repo=$2 run_id=$3 row=$4 rc=$5 log=$6
  local status terminal pushed pr poll outcome
  status=$(field_of status "$row")
  terminal=$(field_of terminal "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  poll=$(field_of poll "$row")
  outcome=$(field_of outcome "$row")
  if [ "$poll" = terminal ] && [ "$pushed" = yes ] && [ -n "$pr" ] && [ "$pr" != 0 ]; then
    fact "$label" PASS "the run in ${repo}${run_id:+ (${run_id})} pushed its branch and opened pull request #${pr} (the row's pushed=yes, pr=${pr}, terminal=${terminal:-no}, status=${status:-unread}, outcome=${outcome:-none}): a run that cloned, pushed and opened a pull request is one whose git traffic the credentials service served, which is what the audit row below reads the token out of. This run's token is named by that row and never by the run's own report"
    return 0
  fi
  if [ "$poll" = norunid ]; then
    fact "$label" FAIL "fugaro run --json named NO run id for the run in ${repo}, so which row of the listing belongs to this run cannot be read — and the row is the reading, not the launch: fugaro run returns as soon as the run is launched${rc:+ (exit ${rc})}"
    return 0
  fi
  if [ "$poll" != terminal ]; then
    fact "$label" FAIL "no row in a FINAL status for the run in ${repo}${run_id:+ (${run_id})} appeared in its listing within ${RUN_SECONDS}s (poll=${poll:-nolisting}; the last row read: found=$(field_of found "$row" || true), status=${status:-unread}, pushed=${pushed:-unread}, pr=${pr:-unread}). The run's own log ends with the last listing: $(last_line "$log")"
    return 0
  fi
  if [ "$pushed" = yes ]; then
    fact "$label" FAIL "the run in ${repo} pushed its branch but its row names no pull request (pr=0, status=${status:-unread}, outcome=${outcome:-none}): the token could push, and a run that leaves no pull request settles less than this check's sentence needs — read the run's record before going on"
    return 0
  fi
  fact "$label" FAIL "the run in ${repo} did not push (its final row reads pushed=no, status=${status:-unread}, outcome=${outcome:-none}): $(last_line "$log"). A run that never reached the proxy settles nothing about which token would have served it"
}

# audit_read RID OUT NEED — the run's own service lines, retried while Cloud
# Logging catches up: ingestion lags the request by seconds, and a reading taken
# the moment the run's row turned terminal is often empty. NEED is how many
# git/bitbucket-api lines the reading is about (one per run here), and the loop
# runs until the lines hold that many or AUDIT_WAIT_SECONDS is up — a reading
# that stopped at the first would leave a row at whatever had been ingested at
# that instant, which is a FAIL a correct installation earns for being fast.
audit_read() {
  local rid=$1 out=$2 need=$3 waited=0 filter got=0
  filter='resource.type="cloud_run_revision" resource.labels.service_name="'"$SERVICE_NAME"'" jsonPayload.run="'"$rid"'"'
  while :; do
    gcloud logging read "$filter" --project="$GCP_PROJECT" --freshness="$AUDIT_AGE" \
      --limit="$AUDIT_LIMIT" --format=json >"$out" 2>"$SCRATCH/audit.err" || true
    got=$(reader audit "$out" "$rid" 2>/dev/null | sed -n 's/^git-lines=//p' | head -n 1 || true)
    case "$got" in '' | *[!0-9]*) got=0 ;; esac
    if [ "$got" -ge "$need" ]; then return 0; fi
    if [ "$waited" -ge "$AUDIT_WAIT_SECONDS" ]; then return 0; fi
    sleep "$AUDIT_POLL_SECONDS"
    waited=$((waited + AUDIT_POLL_SECONDS))
  done
}

# The row this check exists for: which credential NAME the service's own lines
# carry for that run's git and Bitbucket-API calls. It is read from the service's
# lines and from nothing the run reported, and a run whose lines name no token at
# all is UNVERIFIED rather than failed: the reading says the run's git traffic
# did not go through the service.
step_audit() { # step_audit LABEL RUNID EXPECTED
  local label=$1 rid=$2 expected=$3 facts repo=""
  case "$label" in
  audit-a) repo=$REPO ;;
  audit-b) repo=$OTHER_REPO ;;
  esac
  # No run id is no reading at all, not a reading of an empty run: the launch
  # above already wrote its own row saying so, and a listing read here would
  # answer about a run the check cannot name.
  if [ -z "$rid" ]; then
    case "$label" in
    audit-a) CREDS_A="" ;;
    audit-b) CREDS_B="" ;;
    esac
    fact "$label" UNVERIFIED "the run on ${repo} has no run id on this side, so the credentials service's own lines for it cannot be read and which token served ${repo} was NOT measured: the run's own row above says why. Nothing about the swap is concluded from this row"
    return 0
  fi
  audit_read "$rid" "$SCRATCH/audit-${label}.json" 1
  facts=$(reader audit "$SCRATCH/audit-${label}.json" "$rid" 2>"$SCRATCH/reader.err") \
    || stop "the service's lines for ${rid} could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"
  local lines git_lines creds purposes statuses refusals classes repositories
  lines=$(field_of lines "$facts")
  git_lines=$(field_of git-lines "$facts")
  creds=$(field_of git-credentials "$facts")
  purposes=$(field_of purposes "$facts")
  statuses=$(field_of statuses "$facts")
  refusals=$(field_of refusals "$facts")
  classes=$(field_of classes "$facts")
  repositories=$(field_of repositories "$facts")
  # The credential NAMES, as a list, so "is the expected one among them" and
  # "are they more than one" are both readable.
  local named=""
  case "$creds" in
  - | "") ;;
  *) named=$(printf '%s' "$creds" | tr ',' ' ') ;;
  esac
  case "$label" in
  audit-a) CREDS_A=$creds ;;
  audit-b) CREDS_B=$creds ;;
  esac
  # The repository the lines name is the pairing's own fixing of the run to the
  # repository it was launched on (B1.4): a line naming another repository would
  # make "which token served WHICH repository" a reading of a run that went
  # somewhere else, so it is said out loud rather than passed over.
  local scope_note=""
  case "$repositories" in
  - | "") ;;
  "$repo") scope_note=" (every one of the run's lines names the repository ${repositories}, which is the pairing's own fixing of the run to its repository, B1.4)" ;;
  *) scope_note=" (THE LINES NAME THE REPOSITORY ${repositories} AND THIS RUN WAS LAUNCHED ON ${repo}: the row's reading is about the repository the lines name, so treat it as a finding about the pairing rather than about ${repo})" ;;
  esac
  local audit_note="${lines} line(s) in total for this run, purposes ${purposes:-none}, statuses ${statuses:-none}, ${refusals} refusal line(s)${classes:+, classes ${classes}}, read with gcloud logging read$([ "$refusals" = 0 ] && echo ", and no refusal line, which is what a run whose traffic the service served writes")"
  if [ "${git_lines:-0}" -lt 1 ]; then
    fact "$label" UNVERIFIED "the service's own lines for ${rid} carry NO git or Bitbucket-API line, so which token served ${repo} was NOT measured (${audit_note}). A run whose git traffic the credentials service served writes one line per git request naming the token that served it; none written means the traffic did not go through the service — the repository is not on the credentials-service switch (agent.auth: cascade for ${repo} in its own fugaro.yaml, with the image republished by 'fugaro update --repo'), or this run pushed before its lines were ingested. That switch is Check 191's own reading (its proxied row), and this row is the one that would have shown the swap"
    return 0
  fi
  case " $named " in
  *" $expected "*) ;;
  *)
    fact "$label" FAIL "the service's own lines for ${rid} name the credential(s) '${creds}' on its ${git_lines} git/Bitbucket-API line(s), and ${expected} is NOT among them: the proxy served ${repo}'s traffic with a token this check's records say should not have been chosen for it (R2's narrowest-first order over the published node). Read the node ('fugaro policy show', or the installation's bitbucket_tokens entry), the record's coverage and the token's status: a token the store holds is skipped and the next narrowest serves (R12). ${audit_note}"
    return 0
    ;;
  esac
  local extra
  extra=$(printf '%s' "$named" | tr ' ' '\n' | grep -v "^$expected\$" | tr '\n' ' ' || true)
  if [ -n "${extra// /}" ]; then
    fact "$label" PASS "the service's own lines for ${rid} name ${expected} for ${repo} — and also ${extra% }, so the run was served by more than one token: the narrowest usable one is chosen per request and a token the store holds is skipped for a wider one (R12), which is the stated price of the swap and not a failure. The token that served each request is that request's own line. ${audit_note}${scope_note}"
    return 0
  fi
  fact "$label" PASS "the service's own lines for ${rid} name ${expected} — and nothing else — on all ${git_lines} of its git/Bitbucket-API line(s), so ${repo} was served by that one token for the whole run and the choice is the service's own record rather than the run's word (R9, C1.13). ${audit_note}${scope_note}"
}

# The swap itself: two runs of one installation, on two repositories of one
# project, served by two different tokens.
step_swap() {
  case "${CREDS_A:-}${CREDS_B:-}" in
  ""*)
    fact swap UNVERIFIED "one of the two runs' audit rows named no credential at all, so whether the swap happens per request could NOT be measured: per-request swapping is a claim about two readings, and a row that is missing leaves it unsettled. The two audit rows above say which of the two it was"
    return 0
    ;;
  esac
  if [ "$CREDS_A" = "$CREDS_B" ]; then
    case " $CREDS_A " in
    *" $PROJECT_TOKEN_NAME "*)
      # One token served both repositories: with a repository token recorded for
      # ${REPO} alone and usable, that is the finding this check exists to catch.
      fact swap FAIL "both runs were served by the SAME credential '${CREDS_A}': the run on ${REPO} and the run on ${OTHER_REPO} name one token. The selection is per request and narrowest first (R2, D2355), so with a repository token recorded for ${REPO} alone the run there has to be served by it. Read the two audit rows above against the bundle row: the records, the node they were published to ('fugaro update' publishes a record's coverage, and a record this machine holds alone is invisible to every run) and the token's status"
      ;;
    *)
      fact swap FAIL "both runs were served by the same credential '${CREDS_A}', and it is neither of the two this check expects (${REPO_TOKEN_NAME} for ${REPO}, ${PROJECT_TOKEN_NAME} for ${OTHER_REPO}): the audit rows above carry the reading"
      ;;
    esac
    return 0
  fi
  fact swap PASS "the two runs of one installation were served by DIFFERENT tokens — ${REPO} by '${CREDS_A}', ${OTHER_REPO} by '${CREDS_B}' — read from the service's own lines for each run and never from the runs' reports: the choice is made per REQUEST and per repository (R2, D2355), over the records the bundle row read, and the narrower token is the one ${REPO} got"
}

# The ONE 403 this check can force live: the service's front door, asked for the
# path a run's git traffic takes, with an identity token and NO run identity. A
# caller that is not a run of the installation is refused before any route and
# therefore before any token is chosen, which is D143's "a job never receives a
# token" and F9's trust boundary. A run cannot earn it: a coverage refusal is
# HTTP 400 with its class on the audit line, and the pairing fixes a run to its
# own repository.
step_refusal() {
  local rc=0
  if ! read_service_url; then return 0; fi
  # The identity token is written to a 0600 file and from there into a 0600 curl
  # config; it is never printed and never on a command line. It stays in the
  # scratch directory until the cleanup removes it, like the two tokens' configs.
  run_call "$CURL_SECONDS" "$SCRATCH/idtoken" "$SCRATCH/idtoken.err" \
    gcloud auth print-identity-token || rc=$?
  if [ "$rc" -ne 0 ] || [ ! -s "$SCRATCH/idtoken" ]; then
    fact refusal UNVERIFIED "gcloud auth print-identity-token exited ${rc} and answered nothing (${rc:+$(last_line "$SCRATCH/idtoken.err")}), so the one 403 this check can force from ${SERVICE_URL} was NOT measured. Run 'gcloud auth print-identity-token' here yourself; this check needs the identity of an account that may invoke the service"
    return 0
  fi
  CREDENTIAL_FILES+=("$SCRATCH/idtoken")
  chmod 600 "$SCRATCH/idtoken"
  reader idconf "$SCRATCH/idtoken" "$SCRATCH/id.conf" >/dev/null 2>"$SCRATCH/reader.err" \
    || stop "the identity token gcloud minted could not be written into a curl config: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). It is a credential-shaped value, so it is never put on a command line"
  local path="/v1/bitbucket/proxy/${REPO}.git/info/refs?service=git-upload-pack"
  local status
  status=$(service_request "$SCRATCH/frontdoor.json" GET "$path" --config "$SCRATCH/id.conf")
  local facts wire type
  facts=$(reader frontdoor "$SCRATCH/frontdoor.json" 2>"$SCRATCH/reader.err" || true)
  wire=$(field_of wire "$facts")
  type=$(field_of type "$facts")
  case "$status:$wire:$type" in
  403:yes:unverified_run_identity)
    fact refusal PASS "the credentials service answered HTTP 403 'unverified_run_identity' to a call on the route a run's git traffic takes (GET ${path}) that carried an identity token and no run identity: the refusal is the SERVICE's own typed one and it comes before any route and any token choice, so a caller that is not a run of this installation is refused at the front door (D143's 'a job never receives a token', F9's boundary). This is the forced 403, and the service's answer carries no token, no repository and no credential"
    ;;
  403:yes:*)
    fact refusal UNVERIFIED "the service refused the call with HTTP 403 and its own wire document, but the type it wrote is '${type}' rather than unverified_run_identity, so this check's sentence — a call with no run identity is refused as such, before any token is chosen — was NOT settled: read the answer yourself and record the type against the register's row"
    ;;
  403:no:*)
    fact refusal UNVERIFIED "the call was answered HTTP 403 by something that is not the service's own wire document (no {\"error\": …} body), which is what Cloud Run's own front end answers when the caller may not invoke the service: no fact about the service's behaviour was measured. Grant the account running this check roles/run.invoker on the service ('gcloud run services add-iam-policy-binding ${SERVICE_NAME} --project=${GCP_PROJECT} --region=${REGION} --member=user:<you> --role=roles/run.invoker') and run the check again$(curl_note)"
    ;;
  401:*:*)
    fact refusal UNVERIFIED "the service's front end answered HTTP 401, so the identity token gcloud minted was not accepted (a 401 here is the front end, not the service): nothing about the service's own refusal was measured. Run 'gcloud auth print-identity-token' here yourself and read the error$(curl_note)"
    ;;
  200:*:*)
    fact refusal FAIL "the credentials service ANSWERED a call that carried no run identity (HTTP 200 on the proxy route): the front door did not refuse a caller that is not a run of the installation, which is the boundary this row exists for (D143, F9). Check whether a route or a proxy in front of the service was changed, and treat the run-facing proxy route as open until it is fixed"
    ;;
  *)
    local type_note=""
    if [ "$type" != "-" ]; then type_note=" with the service's type '${type}'"; fi
    fact refusal UNVERIFIED "the call was answered HTTP ${status}${type_note}, which is not the 403 this check measures (a coverage refusal of the proxy route is HTTP 400 with its class on the audit line, and only a call with no run identity earns the 403 'unverified_run_identity')$(curl_note)"
    ;;
  esac
}

# read_service_url: the service's own URL, from CHECK591_SERVICE_URL when the
# test set one, else from `gcloud run services describe`. It answers 0 and sets
# SERVICE_URL when there is one, and 1 with the refusal row written when there is
# not.
read_service_url() {
  SERVICE_URL=""
  local override
  override=$(printenv CHECK591_SERVICE_URL 2>/dev/null || true)
  if [ -n "$override" ]; then
    SERVICE_URL=${override%/}
    service_url_ok "$SERVICE_URL" || stop "CHECK591_SERVICE_URL is not a URL this check will send a run's path to (https://<service>.run.app, or the loopback address this check's own test serves)"
    return 0
  fi
  local rc=0
  run_call "$CURL_SECONDS" "$SCRATCH/service.json" "$SCRATCH/service.err" \
    gcloud run services describe "$SERVICE_NAME" --project "$GCP_PROJECT" --region "$REGION" --format=json || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact refusal UNVERIFIED "the credentials service ${SERVICE_NAME} could not be described in ${GCP_PROJECT}/${REGION} (gcloud exited ${rc}: $(last_line "$SCRATCH/service.err")), so its URL could not be read and the forced 403 was NOT measured. Run 'gcloud run services describe ${SERVICE_NAME} --project=${GCP_PROJECT} --region=${REGION}' here yourself, or pass CHECK591_SERVICE_URL"
    return 1
  fi
  SERVICE_URL=$(field_of url "$(reader service "$SCRATCH/service.json" 2>/dev/null || true)")
  if [ -z "$SERVICE_URL" ]; then
    fact refusal UNVERIFIED "gcloud answered for ${SERVICE_NAME} in ${GCP_PROJECT}/${REGION} but with no usable status.url (the deployment may carry none, or the answer named a host this check will not send a request to), so the forced 403 was NOT measured"
    return 1
  fi
  SERVICE_URL=${SERVICE_URL%/}
  return 0
}

# ------------------------------------------------------------------ the block
# The keys, in the order they are measured: the installation and the two records
# first (both are premises this check's sentences rest on), then Bitbucket's own
# answer about each token, then the two runs, the two audit readings, the swap
# they make together, and the one call the service refuses.
BLOCK_KEYS=(installation inputs bundle xtrace premise-repo premise-project
  run-a run-b audit-a audit-b swap refusal restore)

# criteria_unmet FACTS — the block's criteria that are not a PASS row, one name
# per line. A criterion is one of BLOCK_KEYS, the rows the owner reads back: a
# row reading UNVERIFIED (the step could not verify its criterion — a run whose
# git lines name no token, a coverage premise Bitbucket answered another way, a
# forced 403 something other than the service answered) and a key no step wrote
# at all (the block prints those as "not reached", and such a run already ends on
# a FAIL or on the stop that made the step return early — the branch is here so
# that a run which finished without a row for a criterion can never be a pass).
# Both are criteria that were NOT settled, and they are what makes a run
# INCOMPLETE rather than a pass. A `restore` row that FAILed is a FAIL like any
# other, not an unmet criterion.
criteria_unmet() {
  local facts=$1 key line
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    case "$line" in
    "$key|PASS|"*) ;;
    *) printf '%s\n' "$key" ;;
    esac
  done
}

# verdict_of FACTS STATUS — the block's own last word, and the one the exit
# status carries. The status is decided first (finish above, from the facts) and
# the word follows it, so the block can never tell two stories: 0 is PASS and
# every criterion passed, 1 is FAIL and a criterion was measured and failed, and
# both 2 (the run stopped before it could measure everything) and 3 (it finished
# with criteria left unsettled) are INCOMPLETE.
verdict_of() { # verdict_of FACTS STATUS
  case "$2" in
  0) printf 'PASS' ;;
  1) printf 'FAIL' ;;
  *) printf 'INCOMPLETE' ;;
  esac
}

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 591 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      # A criterion no step wrote is not a pass, and it is loud rather than
      # quiet: a key nothing wrote comes with the step that would have written it
      # having stopped the run (exit 2) or with the FAIL that made it return
      # early (exit 1), so the block's last line agrees with the row.
      printf 'FACT %s: FAIL — not reached: nothing measured this criterion (the step that writes this row did not run, or it returned early)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 591 RESULT: %s (exit %s)\n' "$(verdict_of "$facts" "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  local project_flag="--project-key ${PROJECT_KEY}"
  if [ -n "$PROJECT_UUID" ]; then project_flag="--project-uuid ${PROJECT_UUID}"; fi
  local service_line="read with 'gcloud run services describe ${SERVICE_NAME} --project ${GCP_PROJECT} --region ${REGION}'"
  if [ -n "${CHECK591_SERVICE_URL:-}" ]; then service_line="${CHECK591_SERVICE_URL} (CHECK591_SERVICE_URL is set)"; fi
  cat <<PLAN_EOF
Check 591 — which of the installation's Bitbucket tokens serves WHICH repository.
This is the PLAN. --dry-run touches nothing: no installation is read, no run is
launched, no request is made and no credential is written.

  installation     ${INSTALLATION}   (GCP project ${GCP_PROJECT}, hard-coded, overridable)
  workspace        ${WORKSPACE}      (sandbox only; 'belong' is refused by name)
  first repo       ${REPO}      (the repository token's own; both tokens must reach it)
  second repo      ${OTHER_REPO}      (only the project token reaches it; same Bitbucket project)
  records          ${REPO_TOKEN_NAME} (repo, covering ${REPO} alone)
                   ${PROJECT_TOKEN_NAME} (project, covering ${OTHER_REPO})
  token files      repository: ${TOKEN_FILE}
                   project:    ${PROJECT_TOKEN_FILE}   (each mode 0600, one line, no other value)
  project record   ${project_flag:-none}   (a project token's record needs one of the two)
  API root         ${API_ROOT}
  service          ${SERVICE_NAME} — ${service_line}
  curl cap         ${CURL_SECONDS}s per request, ${LAUNCH_SECONDS}s for a launch

What the run will do, in this order:
  1. read the installation with 'fugaro doctor --project ${INSTALLATION} --json' and refuse it if it is
     not ${INSTALLATION} in GCP project ${GCP_PROJECT}, or if it names 'belong';
  2. read the two token files (each mode 0600) into two 0600 curl configs inside a scratch
     directory, and read the two RECORDS with 'fugaro creds ls --project ${INSTALLATION} --json';
  3. read Bitbucket itself with each token: the repository token reaches ${REPO} and not
     ${OTHER_REPO}, the project token reaches both (the premise the swap rests on);
  4. launch ONE RUN PER REPOSITORY ('fugaro run --repo <repo> --project ${INSTALLATION} --task-file …'),
     wait for each run's row in 'fugaro runs ls' to be terminal, and require each to push and open a
     pull request: the released CLI has no change-set launch, so "a two-repository job" is two launches;
  5. read the credentials service's OWN lines for each run with 'gcloud logging read' and record which
     credential NAMEs its git and Bitbucket-API lines carry — the token that served each repository;
  6. compare the two: two repositories, two different tokens, one installation (the swap);
  7. make ONE call of its own that must be refused: GET ${REPO}.git/info/refs on the service's
     proxy route with an identity token and NO run identity, expecting HTTP 403 'unverified_run_identity';
  8. nothing is written to the installation: no 'creds set', no policy publish, no 'fugaro update'.
It removes its scratch directory and every file a credential was written into on the way out,
however it ends, and says so in the block's restore row.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
USAGE="check591.sh --repo <workspace>/<first> --other-repo <workspace>/<second> --token-file <path> --project-token-file <path> (--project-uuid <uuid> | --project-key <key>) [--repo-token-name <name>] [--project-token-name <name>] [--project <installation>] [--gcp-project <project>] [--region <region>] [--workspace <slug>] [--dry-run]"

parse_args() {
  REPO=""
  OTHER_REPO=""
  TOKEN_FILE=""
  PROJECT_TOKEN_FILE=""
  REPO_TOKEN_NAME=${CHECK591_REPO_TOKEN_NAME:-bitbucket-token--ck590-repo}
  PROJECT_TOKEN_NAME=${CHECK591_PROJECT_TOKEN_NAME:-bitbucket-token--ck590-project}
  PROJECT_UUID=""
  PROJECT_KEY=""
  INSTALLATION=${CHECK591_INSTALLATION:-fugaro}
  GCP_PROJECT=${CHECK591_GCP_PROJECT:-fugaro-dev}
  WORKSPACE=${CHECK591_WORKSPACE:-dimi_bmb}
  REGION=${CHECK591_REGION:-us-east5}
  SERVICE_NAME=${CHECK591_SERVICE_NAME:-fugaro-credentials}
  DRY_RUN=no
  PROJECT_NAME=""
  GCP_PROJECT_SEEN=""
  REGION_SEEN=""
  SERVICE_URL=""
  RUN_A=""
  RUN_B=""
  CREDS_A=""
  CREDS_B=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --repo | --other-repo)
      [ $# -ge 2 ] || die "$1 needs workspace/name. usage: $USAGE"
      case "$1" in
      --repo) REPO=$2 ;;
      --other-repo) OTHER_REPO=$2 ;;
      esac
      case "$2" in
      */*) ;;
      *) die "$1's repository '$2' is not workspace/name. usage: $USAGE" ;;
      esac
      shift 2
      ;;
    --token-file | --project-token-file)
      [ $# -ge 2 ] || die "$1 needs a path. usage: $USAGE"
      case "$1" in
      --token-file) TOKEN_FILE=$2 ;;
      --project-token-file) PROJECT_TOKEN_FILE=$2 ;;
      esac
      shift 2
      ;;
    --repo-token-name | --project-token-name)
      [ $# -ge 2 ] || die "$1 needs the record's name. usage: $USAGE"
      case "$1" in
      --repo-token-name) REPO_TOKEN_NAME=$2 ;;
      --project-token-name) PROJECT_TOKEN_NAME=$2 ;;
      esac
      shift 2
      ;;
    --project-uuid)
      [ $# -ge 2 ] || die "--project-uuid needs the project's UUID, braces included. usage: $USAGE"
      PROJECT_UUID=$2
      shift 2
      ;;
    --project-key)
      [ $# -ge 2 ] || die "--project-key needs the project's key. usage: $USAGE"
      PROJECT_KEY=$2
      shift 2
      ;;
    --project)
      [ $# -ge 2 ] || die "--project needs an installation name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --gcp-project)
      [ $# -ge 2 ] || die "--gcp-project needs a project id. usage: $USAGE"
      GCP_PROJECT=$2
      shift 2
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a Cloud Run region. usage: $USAGE"
      REGION=$2
      shift 2
      ;;
    --workspace)
      [ $# -ge 2 ] || die "--workspace needs a slug. usage: $USAGE"
      WORKSPACE=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      # An intentional ending, not an abort: REACHED_END says so, and PRINTED
      # keeps the EXIT trap from printing a block of "not reached" rows for a run
      # that never started.
      printf 'usage: %s\n' "$USAGE"
      REACHED_END=yes
      PRINTED=1
      trap - ERR
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$REPO" ] || die "--repo is required (the repository the repository token was created for). usage: $USAGE"
  [ -n "$OTHER_REPO" ] || die "--other-repo is required (the repository only the project token reaches, in the same Bitbucket project). usage: $USAGE"
  [ -n "$TOKEN_FILE" ] || die "--token-file is required (the repository access token). usage: $USAGE"
  [ -n "$PROJECT_TOKEN_FILE" ] || die "--project-token-file is required. usage: $USAGE"
  case "$REPO" in
  "$WORKSPACE"/*) ;;
  *) die "--repo '$REPO' is not a repository of the workspace ${WORKSPACE}: the two tokens are sandbox tokens of ${WORKSPACE}, and a repository of another workspace would make every row below a reading of another workspace's token. Pass --workspace if you really mean another one. usage: $USAGE" ;;
  esac
  case "$OTHER_REPO" in
  "$WORKSPACE"/*) ;;
  *) die "--other-repo '$OTHER_REPO' is not a repository of the workspace ${WORKSPACE} (see --repo). usage: $USAGE" ;;
  esac
  if [ "$REPO" = "$OTHER_REPO" ]; then
    die "--repo and --other-repo are the same repository '$REPO': the whole check is which token serves WHICH repository, and one repository for both would make its central row a reading of one choice. usage: $USAGE"
  fi
  if [ "$TOKEN_FILE" = "$PROJECT_TOKEN_FILE" ]; then
    die "--token-file and --project-token-file are the same path: two types are two credentials, and one file for both would make the premise rows a reading of one token. usage: $USAGE"
  fi
  if [ "$REPO_TOKEN_NAME" = "$PROJECT_TOKEN_NAME" ]; then
    die "--repo-token-name and --project-token-name are the same name '$REPO_TOKEN_NAME': two records are two tokens. usage: $USAGE"
  fi
  local name
  for name in "$REPO_TOKEN_NAME" "$PROJECT_TOKEN_NAME"; do
    case "$name" in
    bitbucket-token--?*) ;;
    *) die "the record name '$name' is not a typed Bitbucket token's (the bundle key is bitbucket-token--<name>, R5/R13). usage: $USAGE" ;;
    esac
    case "$name" in
    *[!A-Za-z0-9._-]*) die "the record name '$name' holds a character a token's name may not have (letters, digits, dot, dash, underscore). usage: $USAGE" ;;
    esac
  done
  if [ -n "$PROJECT_UUID" ] && [ -n "$PROJECT_KEY" ]; then
    die "--project-uuid and --project-key were both given: a project token's record takes ONE of them (the UUID is what Bitbucket matches, the key is display). usage: $USAGE"
  fi
  [ -n "$PROJECT_UUID" ] || [ -n "$PROJECT_KEY" ] \
    || die "one of --project-uuid or --project-key is required: the check reads the project token's record and a project token's coverage is its workspace alone, so the project it belongs to is what the record carries (R1). usage: $USAGE"
}

# project_flag_for_usage: one of the two flags, for a sentence that tells the
# owner how to set the record the way this check needs it. Named for what it is:
# the sentence is not a command.
project_flag_for_usage() {
  if [ -n "$PROJECT_UUID" ]; then printf -- '--project-uuid %s' "$PROJECT_UUID"; else printf -- '--project-key %s' "$PROJECT_KEY"; fi
}

main() {
  parse_args "$@"
  guard_endpoints
  # The endpoint is Bitbucket Cloud's own API, and nothing else: a redirect is
  # REFUSED rather than followed, because the request carries a token.
  API_ROOT=${CHECK591_API_URL:-https://api.bitbucket.org/2.0}
  API_ROOT=${API_ROOT%/}
  CURL_SECONDS=${CHECK591_CURL_SECONDS:-60}
  RUN_SECONDS=${CHECK591_RUN_SECONDS:-5400}
  RUN_POLL_SECONDS=${CHECK591_RUN_POLL_SECONDS:-20}
  LAUNCH_SECONDS=${CHECK591_LAUNCH_SECONDS:-300}
  AUDIT_WAIT_SECONDS=${CHECK591_AUDIT_WAIT_SECONDS:-120}
  AUDIT_POLL_SECONDS=${CHECK591_AUDIT_POLL_SECONDS:-5}
  AUDIT_AGE=${CHECK591_AUDIT_AGE:-3h}
  AUDIT_LIMIT=${CHECK591_AUDIT_LIMIT:-300}
  if [ "${CHECK591_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
  guard_belong

  if [ "$DRY_RUN" = yes ]; then
    print_plan
    REACHED_END=yes
    PRINTED=1
    trap - ERR
    exit 0
  fi

  # After the two endings above rather than before the arguments are read: a run
  # that only prints the plan, or only the usage, calls none of these tools, and
  # refusing it for a missing `fugaro` would deny the owner the one output they
  # can have without an installation at all.
  need_tools
  scratch_up
  CREDENTIAL_FILES=("$SCRATCH/repository.conf" "$SCRATCH/project.conf" "$SCRATCH/id.conf"
    "$SCRATCH/curl.log" "$SCRATCH/reader.py"
    "$SCRATCH/doctor.err" "$SCRATCH/creds.err" "$SCRATCH/run-a.log" "$SCRATCH/run-b.log"
    "$SCRATCH/audit.err" "$SCRATCH/idtoken.err" "$SCRATCH/service.err")
  write_reader
  guard_xtrace

  step_installation
  step_inputs
  step_bundle
  step_premises
  step_run run-a "$REPO"
  step_run run-b "$OTHER_REPO"
  step_audit audit-a "${RUN_A:-}" "$REPO_TOKEN_NAME"
  step_audit audit-b "${RUN_B:-}" "$PROJECT_TOKEN_NAME"
  step_swap
  step_refusal

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# -- including through the cleanup, which is where its second answer matters. A
# Ctrl-C while the credential files are being removed would otherwise kill this
# shell with them still on disk.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
