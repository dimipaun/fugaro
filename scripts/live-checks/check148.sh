#!/usr/bin/env bash
# Check 148, the read half — the Bitbucket read consumer's stop, rotation and
# ownership transfer, as ONE script YOU run.
#
# It is the runbook's §12, executed: the read consumer is taken out of the
# bundle and a read mint is confirmed refused; the consumer's secret is
# regenerated and the bundle written again, with the OLD secret confirmed
# refused and U13 measured (whether a token minted before the rotation still
# reads); and a successor consumer, created under a second sandbox account, is
# written and a read mint confirmed against it. The register's row and section
# are docs/gcp-live-checklist.md, "Check 148".
#
# Usage:      bash scripts/live-checks/check148.sh \
#               --installation <sandbox> --repo <owner/name> \
#               --consumer-file <path> --rotated-consumer-file <path> \
#               --successor-consumer-file <path> [--yes]
#
#             Two of the three files exist before the run: the consumer document
#             the bundle holds today, and a consumer created under the SECOND
#             sandbox account the transfer goes to. The third — the same
#             consumer with the secret Bitbucket regenerates — is written BY YOU
#             at the rotation, when the drill asks for it (below); naming the
#             path here is saying where you will write it. Each is the mapping
#             the bundle's entry holds — key, secret and workspace, one per line
#             — and none of them is printed, echoed or passed on a command line:
#             they go into `fugaro creds set` on stdin, and the shell never holds
#             a value.
# Needs:      bash, mktemp, python3, gcloud, fugaro and curl on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it.
# Where:      SANDBOX installations only, and never `belong`: the name is
#             refused, whether you typed it as the installation or the config in
#             force answers it. It WRITES: the bundle of the installation you
#             name, four times. It deletes nothing and touches no other project.
# Costs:      four Secret Manager versions, one disabled, and the read mints
#             around them. No model call, no run, no image, no Terraform.
# Runs:       by YOU, after the confirmation line below and one answer at the
#             rotation. No agent runs any step of it.
# Asks:       two things, and the second one is what makes the drill measurable:
#             the confirmation, and — at the rotation — the moment where you
#             regenerate the consumer's secret at Bitbucket and write the rotated
#             document. Bitbucket's regeneration is what stops the secret the
#             bundle holds from being exchangeable, so a run that began with the
#             rotated document already written would be minting with a dead
#             secret from its first row: the baseline would be refused and the
#             drill could measure nothing. So the drill asks, waits for your
#             answer, holds what you wrote to the consumer file's own key and
#             workspace, and only then writes it into the bundle. That answer is
#             needed whether or not you passed --yes: --yes stands for the typed
#             confirmation, and the regeneration is a step at Bitbucket that no
#             flag can do for you.
# Cleanup:    always, including on a Ctrl-C: the scratch directory — which holds
#             the whole bundle document while the stop is being made, and every
#             token this run mints — is removed, and the `restore` row says
#             whether it really went. The bundle is NOT put back the way it was:
#             it ends holding the successor consumer, which is what the transfer
#             is, and the `restore` row says so and names the one consumer to
#             delete at Bitbucket once the transfer has landed.
# Output:     progress on stderr, ONE block on stdout headed CHECK 148 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, a mint was refused where it should have
#             been served).
#
# The service serves the PARSED bundle for 30 seconds (credsvc.BundleCacheTTL),
# so every write below is followed by a bounded wait for the service to see it:
# a mint read too soon after a write is the old document's answer, and a check
# that treated it as the new one's would report a stop that had not happened.
# CHECK148_MINT_WAIT is that bound in seconds (default 90, and 0 means one
# attempt), and the register's section says what it is for.
#
# The wait cuts both ways, and the served direction is the one a bound alone
# cannot settle: the document a write REPLACED answers with a 200 too, so a mint
# read a moment after the write can be the old credential's mint and read as the
# new one's. What tells the two apart is the cache's own lifetime — the loader
# serves a parsed document only while that parse is younger than
# credsvc.BundleCacheTTL, so a 200 read at or after the TTL can only come from a
# parse that happened after the write, which is the write's own document.
# CHECK148_CACHE_WAIT is that lifetime in seconds (default 40, so the reading is
# taken clear of the 30-second TTL and of the loop's own step; 0 reads once,
# which is what the script's own test uses).
#
# No secret and no token is printed, and none is on a command line: the
# consumer files are read by the reader and handed to `fugaro creds set` on
# stdin, the tokens this run mints are written into 0600 files, and the curl
# requests that carry one carry it in a `--config` file rather than in argv. The
# block prints a fixed vocabulary — yes/no, a count, an HTTP status and a
# Bitbucket or credsvc error CODE — and never a value, so an endpoint that
# echoes back what it was given cannot reach it. And no trace: `bash -x` prints
# every command, so guard_xtrace REFUSES a run whose shell arrived tracing.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check handles the
# bundle document and the tokens it mints, and a trace prints values. The three
# sources are read separately (BASHOPTS carries names like extquote, and one
# pattern over the three would match that 'x' and refuse every run), the trace
# is turned off here, and BASH_XTRACEFD is unset so a trace redirected to a file
# this script knows nothing about cannot be reopened.
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
ABORT_REASON=""
# Set to yes by main once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
# Set by --help: it answers a question rather than running the drill, so the
# exit trap leaves its status and prints no block — a block of "not reached"
# rows for a run nobody asked for would be worse than no output at all.
HELP_SHOWN=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
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
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as a finished drill for a run that measured
# nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the read consumer" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 148 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 148 needs bash"
  need mktemp
  need python3 "the reader that parses the bundle document, the mint's answer and the consumer files is one python3 file"
  need gcloud "the stop drill writes a bundle version by hand, because no fugaro command removes an entry"
  need fugaro "the rotation and the transfer are written with fugaro creds set, the bundle's only writer"
  need curl "the read mint and the two Bitbucket calls are HTTP"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk148-mine"
SCRATCH_REMOVED=no
PRINTED=""
# The files this run writes a credential into and nothing else: the bundle
# document it reads, the document with the entry taken out, the identity tokens
# and read tokens it mints, the answers those tokens arrive IN (a mint's body
# carries the read token and the grant's carries the replaced consumer's own
# access token, which is a live credential of exactly the kind U13 is about),
# and the curl configs built from them. They are
# removed by cleanup and their removal is READ, not assumed — the `restore` row
# reports it, because a credential file that survived is the one thing an owner
# has to be told about rather than read a PASS over.
CREDENTIAL_FILES=()
CURL_LOG=""
# What the bundle holds when the run ends, as a phrase for the restore row.
LEFTOVER=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk148.XXXXXXXX") || die "Check 148 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  CURL_LOG="$SCRATCH/curl.log"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 148 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  local f
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
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before
  # print_block and lose the whole block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  if [ "$HELP_SHOWN" = yes ]; then exit "$status"; fi
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # a block that read as a finished drill would offer a row of PASSes for steps
  # that never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the read consumer"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so no credential file exists and the bundle was not written"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    [ "$CREDENTIALS_REMOVED" = yes ] || restore=FAIL
    # The bundle's own state, as the row can say it: a run that wrote nothing is
    # not one whose bundle "is not back the way it was", and a row that said so
    # while its own next clause said nothing was written would be contradicting
    # itself in the sentence an owner reads first.
    local bundle_state
    if [ -z "$LEFTOVER" ]; then
      bundle_state="The bundle was NOT written: it holds the consumer it held when the run started, and no version was added"
    else
      bundle_state="The bundle is NOT back the way it was: $LEFTOVER"
    fi
    # What is owed at Bitbucket, and it is ONE consumer: the rotation regenerates
    # the secret of the consumer the bundle already holds, and the transfer then
    # replaces that same consumer's entry with the successor's — so the consumer
    # to delete is the one the drill replaced, and the successor is the bundle's
    # entry now and stays. Only the transfer's own write makes the delete safe:
    # while the bundle still holds the consumer you started with, deleting it at
    # Bitbucket refuses every read through the service.
    local owed
    if [ "$TRANSFERRED" = yes ]; then
      owed="Delete ONE consumer at Bitbucket: the one this drill replaced — the consumer whose secret you regenerated, which is also the consumer the transfer replaced, since the two steps are one consumer throughout. The successor is the bundle's entry now and stays — deleting it would refuse every read through the service — while the consumer you replaced is still a credential that can be exchanged for tokens for this workspace until it is deleted at Bitbucket"
    else
      owed="This run did not confirm that the bundle holds the successor, so no consumer is owed a delete from it: read the entry with fugaro creds ls first. The consumer the bundle names is the one the read mint spends, and deleting it at Bitbucket refuses every read through the service; the one to delete is the one the bundle no longer names"
    fi
    facts="${facts}"$'\n'"restore|$restore|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself"), and the credential files in it $([ "$CREDENTIALS_REMOVED" = yes ] && echo "are gone" || echo "are STILL THERE — remove them yourself"). $bundle_state. $owed"
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
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the bundle document and every token this run handles"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check148.sh --installation <sandbox> --repo <owner/name> --consumer-file <path> --rotated-consumer-file <path> --successor-consumer-file <path> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the bundle document or a token"
}

# guard_sandbox: `belong` is the owner's own installation and this check WRITES
# its bundle, so the name is refused here — before doctor is asked — and doctor's
# own answer is refused the same way a moment later, because the name you type
# and the config in force are two things and the check has to see the same one
# twice.
guard_sandbox() {
  local lowered
  lowered=$(printf '%s' "$INSTALLATION" | tr '[:upper:]' '[:lower:]')
  case "$lowered" in
  belong | belong-*)
    fact installation FAIL "the installation named is $INSTALLATION, which is the owner's own: Check 148 writes the bundle it is pointed at, and it is a sandbox check"
    stop "Check 148 never runs against 'belong'. Name a sandbox installation: bash scripts/live-checks/check148.sh --installation <sandbox> ..."
    ;;
  esac
}

# guard_endpoint NAME VALUE: the two Bitbucket roots this check calls are
# overridable, for the shape test that drives it against a fake on a loopback
# port, and an override that is not a loopback http address is refused rather
# than called: these requests carry the consumer's secret and a minted read
# token, and over plain http to any other host they would travel in cleartext.
guard_endpoint() {
  local name=$1 value=$2
  case "$value" in
  https://*) return 0 ;;
  esac
  case "$value" in
  http://localhost | http://localhost/* | http://localhost:*) return 0 ;;
  http://127.0.0.1 | http://127.0.0.1/* | http://127.0.0.1:*) return 0 ;;
  http://\[::1\] | http://\[::1\]/* | http://\[::1\]:*) return 0 ;;
  esac
  die "Check 148 refused: $name is '$value', which is neither https nor a loopback http address. This check sends the consumer's secret and the tokens it mints to the addresses it is given, so an override is for a fake on this machine and nothing else"
}

# --------------------------------------------------------------- the requests
# request OUT METHOD URL [curl args...] — one request, its body in OUT, its
# status on stdout. Everything curl says goes to the scratch log: its messages
# can quote a request, and this script's requests carry a credential. A request
# that never got an answer is 000.
request() {
  local out=$1 method=$2 url=$3
  shift 3
  local code=000
  code=$(curl -sS -o "$out" -w '%{http_code}' --max-time "$CURL_SECONDS" \
    --request "$method" "$@" "$url" 2>>"$CURL_LOG") || code=000
  printf '%s' "$code"
}

# redact_secrets TEXT: TEXT with anything credential-shaped replaced. The
# excerpt below is the only text from outside this script that can reach the
# block, and it is a `curl` message rather than a response body: the values this
# check handles are never in the shell at all, so this filter is the belt beside
# that fact, not the guarantee.
redact_secrets() {
  printf '%s' "$1" | sed -E \
    -e 's/(([Pp]roxy-)?[Aa]uthorization[[:space:]]*:?[[:space:]]*)([Bb]asic|[Bb]earer|[Dd]igest)?[[:space:]]*[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Bb]earer[[:space:]]+)[A-Za-z0-9._~+\/=-]{6,}/\1<redacted>/g' \
    -e 's/([Aa]ccess[_-]?[Tt]oken|[Cc]lient[_-]?[Ss]ecret|[Rr]efresh[_-]?[Tt]oken|[Aa]pi[_-]?[Kk]ey|[Tt]oken)[[:space:]]*[:=][[:space:]]*[A-Za-z0-9._~+\/=-]{4,}/\1=<redacted>/g'
}

# curl_note: one bounded, redacted line for a request that got no answer at all
# (status 000). The credential is in the config file and never on the command
# line, so what curl says about it carries none.
curl_note() {
  local text
  text=$(tail -n 1 "$CURL_LOG" 2>/dev/null | tr -d '\r' | tr '\n' ' ' | head -c 200 || true)
  [ -n "$text" ] || text="curl printed nothing"
  printf ' (curl said, with anything credential-shaped removed: %s)' "$(redact_secrets "$text")"
}

# ------------------------------------------------------------- the reader
# The reader is where every credential is handled. The shell hands it file names
# and gets back a fixed vocabulary, so no value ever enters this shell.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

# run_capture OUTFILE CMD... — runs a read, keeps stdout in OUTFILE, and leaves
# its exit status in $?. stderr is shown to the owner and never parsed: it is
# where gcloud and fugaro put their progress, and a warning on it is not a
# failed read.
run_capture() {
  local out=$1
  shift
  "$@" >"$out" 2>"$SCRATCH/cmd.err" || {
    local status=$?
    sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true
    return "$status"
  }
}

# ------------------------------------------------------------------ the steps
# step_installation asks the local config which installation it is looking at and
# holds it to the name that was typed. doctor is the only thing that reads the
# config here, and its --json answer is the whole of what this step wants.
step_installation() {
  # doctor's exit code is not its answer. It prints the whole --json document
  # and then exits non-zero whenever any ordinary check fails
  # (internal/cli's emitDoctor), and a sandbox installation routinely carries
  # one — a stale plugin pin, a secret not stored yet — while its config still
  # answers with the project and the region this check needs. So the status is
  # carried, never a stop: what stops the run is an answer that is not the
  # document, or one that names no project.
  local doctor_status=0
  run_capture "$SCRATCH/doctor.json" fugaro doctor --json || doctor_status=$?
  local answer name project region
  answer=$(reader doctor <"$SCRATCH/doctor.json") || stop "fugaro doctor --json did not answer with the document this check reads, so this run cannot tell which installation it is looking at (fugaro exited ${doctor_status}; is this fugaro too old to print --json, or is no local config readable?). Run fugaro doctor yourself and fix what it reports"
  if [ "$doctor_status" != 0 ]; then
    say "fugaro doctor --json exited $doctor_status and printed its document anyway: this check read the document, and what doctor reported is between you and it"
  fi
  name=$(field_of name "$answer")
  project=$(field_of project "$answer")
  region=$(field_of region "$answer")
  [ -n "$region" ] || stop "fugaro doctor --json named no region, so this check cannot address the job: the local config has to carry one"
  PROJECT=$project
  REGION=$region
  NAME=$name
  # The name typed is held to the config: a config naming another installation is
  # the same refusal as naming `belong` by hand, because this check WRITES the
  # bundle of whatever it is pointed at and the two have to be the same thing.
  if [ "$PROJECT" != "$INSTALLATION" ]; then
    fact installation FAIL "you named $INSTALLATION and the config in force is the $PROJECT installation: set FUGARO_CONFIG to the sandbox's own config, or name $PROJECT — this check never decides for itself which installation it writes"
    stop "the installation named ($INSTALLATION) is not the one the local config in force describes ($PROJECT)"
  fi
  fact installation PASS "the config in force is the $NAME installation: project $PROJECT, region $REGION"
}

# step_service reads the read mint's URL off the coordinator job's own
# environment — the same variable Check 146 reads, and the same service: a
# person's mint is an HTTP call to it, which is what this check makes instead of
# running fugaro coordinator show, because the drill has to read the answer's
# code rather than a command's message.
step_service() {
  if ! run_capture "$SCRATCH/jobs.json" gcloud run jobs list --project "$PROJECT" --region "$REGION" --format=json; then
    stop "gcloud run jobs list did not answer for $PROJECT/$REGION: the check needs an account that may read Cloud Run there"
  fi
  local answer job rc=0
  answer=$(reader joblist <"$SCRATCH/jobs.json" 2>"$SCRATCH/reader.err") || rc=$?
  if [ "$rc" -ne 0 ]; then
    # The reader's own line is carried into the reason: a project that lists two
    # coordinator jobs and one that lists none are the same refusal to this
    # check and very different things to an operator.
    stop "no single coordinator job is deployed in $PROJECT/$REGION: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). The job carries the label fugaro_role=coordinator, and this check reads the credentials service's address off it — turn the coordinator on first: fugaro update --firebase-project <id> --coordinator, which is the flag that creates the job (and with it the read mint's variable)"
  fi
  job=$(field_of job "$answer")
  if ! run_capture "$SCRATCH/job.json" gcloud run jobs describe "$job" --project "$PROJECT" --region "$REGION" --format=json; then
    stop "gcloud run jobs describe did not answer for $job"
  fi
  answer=$(reader job <"$SCRATCH/job.json") || stop "the coordinator job's environment is not the document this check reads"
  SERVICE_URL=$(field_of url "$answer")
  [ -n "$SERVICE_URL" ] || stop "the coordinator job carries no FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL, so there is no read mint to exercise: the credentials service is off, or this job was deployed before BB5 — run fugaro update --firebase-project <id>"
  guard_endpoint "the read mint's URL" "$SERVICE_URL"
  fact service PASS "the read mint is at the address the coordinator job carries; the drill mints there as YOU, which is the person class, so your own address has to be one of the installation's operators"
}

# step_consumer reads the bundle's listing and the two files the drill starts
# with. The listing answers the container, its newest version and whether the
# entry is there — names and metadata, never a value — and the files are read by
# the reader, which holds the key and the secret and prints only their shape. The
# rotated document is not one of them: it is written at the rotation, once
# Bitbucket has regenerated the secret (step_rotate_ask asks for it), because a
# rotated document written before the run starts would mean the secret the bundle
# holds is already dead.
step_consumer() {
  local f
  for f in "$CONSUMER_FILE" "$SUCCESSOR_CONSUMER_FILE"; do
    [ -f "$f" ] || stop "the consumer file $f does not exist. The drill starts with two of them: the consumer the bundle holds today, and the successor created under the second sandbox account. The third — the same consumer with the secret Bitbucket regenerates — is written during the run, at the rotation"
  done

  if ! run_capture "$SCRATCH/creds.json" fugaro creds ls --json; then
    stop "fugaro creds ls --json did not answer: this check needs the bundle's container, its newest version and whether it holds the read consumer, and that listing is the only read of it. Run it yourself and fix what it reports"
  fi
  local answer
  answer=$(reader creds <"$SCRATCH/creds.json") || stop "the answer to fugaro creds ls --json is not the document this check reads (is this fugaro too old to print --json?)"
  SECRET=$(field_of secret "$answer")
  VERSION=$(field_of version "$answer")
  local present
  present=$(field_of present "$answer")
  [ -n "$SECRET" ] || stop "fugaro creds ls --json named no bundle container, so the stop drill has nothing to write a version into"
  if [ "$present" != yes ]; then
    fact read-consumer FAIL "the bundle holds no bitbucket-read-consumer entry, so there is nothing to stop, rotate or transfer. Write it first, with fugaro creds set bitbucket-read-consumer < <consumer-file> (the runbook's §12), and run this check again"
    stop "the bundle holds no read consumer"
  fi
  fact read-consumer PASS "the bundle $SECRET is at version ${VERSION:-?} and holds the bitbucket-read-consumer entry: this is the consumer the drill stops, rotates and replaces"

  # The two files that exist now, held to each other. The transfer is a consumer
  # created under another account, so the successor must carry a DIFFERENT key.
  # The comparison needs no value: the reader prints a fingerprint of the key and
  # never the key. The rotated document is held to the consumer file the same way
  # at the rotation, once you have written it.
  local cur suc
  cur=$(read_shape "$CONSUMER_FILE" "the consumer file")
  suc=$(read_shape "$SUCCESSOR_CONSUMER_FILE" "the successor consumer file")
  local cur_ws suc_ws cur_key suc_key
  cur_ws=$(field_of workspace "$cur")
  suc_ws=$(field_of workspace "$suc")
  cur_key=$(field_of keyfp "$cur")
  suc_key=$(field_of keyfp "$suc")
  REPO_WS=${REPO%%/*}
  [ "$cur_ws" = "$REPO_WS" ] || stop "the consumer file names the workspace '$cur_ws' and --repo names '$REPO'. The consumer is read as the workspace it was created in, and the read mint refuses a repository outside it by name (G11), so the two must agree"
  [ "$suc_ws" = "$cur_ws" ] || stop "the successor consumer file names the workspace '$suc_ws' and the consumer file names '$cur_ws': the transfer replaces the credential, not the workspace"
  [ "$suc_key" != "$cur_key" ] || stop "the successor consumer file carries the SAME consumer key as the consumer file, so it is the same consumer and the transfer measures nothing: create a consumer under the second sandbox account"
  # Kept for the rotation's own comparison, which cannot be made here: the
  # rotated document does not exist yet.
  CONSUMER_KEYFP=$cur_key
  fact consumer-files PASS "the bundle's consumer and the successor are one consumer and a consumer created under the second sandbox account (a different key, the same workspace); the key is compared as a fingerprint and never printed, and the rotated document is held the same way at the rotation"
}

# read_shape PATH WHAT — the reader's shape vocabulary for one consumer file.
read_shape() {
  local out rc=0
  out=$(reader shape "$1" 2>"$SCRATCH/reader.err") || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "$2 ($1) could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null) (the file holds the read consumer's key, secret and workspace, one field per line — the mapping the bundle's bitbucket-read-consumer entry holds). Nothing was written and no value from the file is printed"
  fi
  printf '%s' "$out"
}

# step_confirm is the ONE confirmation line: what the run is about to change,
# named once, and one typed answer. It is not a --force to be remembered: the
# four writes below land on the installation's live bundle, and the drill's
# whole subject is that a bundle write is what stops, rotates and transfers the
# credential.
step_confirm() {
  if [ "$ASSUME_YES" = yes ]; then
    fact confirm PASS "--yes was given on the command line, so the confirmation is that line's: the four writes the register's section names are the ones this run makes"
    return 0
  fi
  owner "Check 148 will WRITE the $INSTALLATION bundle FOUR times: it removes the bitbucket-read-consumer entry once (a version written by hand, with the version it replaced disabled) and sets it back three times with fugaro creds set — and it deletes nothing. Type $INSTALLATION to go ahead, or Ctrl-C"
  local answer=""
  read -r answer || true
  if [ "$answer" != "$INSTALLATION" ]; then
    stop "the confirmation was not given (you typed '${answer}'): nothing was written, no version was added and the bundle is as it was. Type the project ID ($INSTALLATION) to run the drill"
  fi
  fact confirm PASS "the confirmation was typed: the four writes the register's section names are the ones this run makes"
}

# mint_once N — one read mint, as this person. It mints the caller's own Google
# ID token, writes the request's config file, and records what the service
# answered in MINT_STATUS, MINT_CODE, MINT_REPO and MINT_EXPIRES. The token
# itself is written to $SCRATCH/token-$N and never printed.
mint_once() {
  local n=$1
  if ! run_capture "$SCRATCH/idtoken-$n" gcloud auth print-identity-token; then
    stop "gcloud auth print-identity-token did not answer, so this run cannot present the caller's own identity — which is what the person class verifies. Run gcloud auth login as one of the installation's operators"
  fi
  CREDENTIAL_FILES+=("$SCRATCH/idtoken-$n")
  chmod 600 "$SCRATCH/idtoken-$n" 2>/dev/null || true
  reader mintconf "$SCRATCH/idtoken-$n" "$SCRATCH/mint-$n.conf" "$REPO" >/dev/null 2>&1 \
    || stop "the read mint's request could not be built: the identity token is not one header value"
  CREDENTIAL_FILES+=("$SCRATCH/mint-$n.conf")
  MINT_STATUS=$(request "$SCRATCH/mint-$n.json" POST "$SERVICE_URL$READ_PATH" --config "$SCRATCH/mint-$n.conf")
  CREDENTIAL_FILES+=("$SCRATCH/mint-$n.json")
  local answer
  answer=$(reader mint "$SCRATCH/mint-$n.json" "$SCRATCH/token-$n" "$REPO" 2>"$SCRATCH/reader.err") \
    || stop "the read mint's answer could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). The answer is in the run's scratch directory and is not printed"
  MINT_CODE=$(field_of code "$answer")
  MINT_REPO=$(field_of repo "$answer")
  MINT_EXPIRES=$(field_of expires "$answer")
  MINT_TOKEN=$(field_of token "$answer")
  MINT_ANSWERED=$(field_of answered "$answer")
  MINT_NO_CONSUMER=$(field_of noconsumer "$answer")
  if [ "$MINT_TOKEN" = yes ]; then
    CREDENTIAL_FILES+=("$SCRATCH/token-$n")
  fi
  return 0
}

# mint_want N WANT WHAT — mints until the service answers WANT (ok or refused),
# waiting out the service's 30-second bundle cache. The bound is
# CHECK148_MINT_WAIT seconds, and the wait is the point: a mint read too soon
# after a write is the OLD document's answer, so a check that took the first one
# would report a stop that had not happened yet.
mint_want() {
  local n=$1 want=$2 what=$3
  local waited=0 step=5
  MINT_WAITED=0
  while :; do
    mint_once "$n"
    case "$want" in
    ok)
      # The served direction needs the cache waited out as much as the refused
      # one, and it is the direction a first-200-read would get wrong in silence:
      # the document this write replaced is served for its own cache lifetime and
      # answers a 200 too, so a mint read a moment after the write can be the OLD
      # credential's mint, with the row below claiming it as the new one's. What
      # makes a 200 readable is the clock rather than the body: the loader serves
      # a parsed document only while that parse is younger than the cache's
      # lifetime, so a 200 read that far after the write can only come from a
      # parse that happened after it — the document the write made.
      if [ "$MINT_STATUS" = 200 ] && [ "$MINT_ANSWERED" = yes ] && [ "$waited" -ge "$CACHE_WAIT" ]; then
        MINT_WAITED=$waited
        return 0
      fi
      ;;
    refused)
      # Only the service's own no-consumer refusal is a stop, and it takes all
      # three readings: 503, state_unavailable (internal/credsvc/wire.go's status
      # and type) and ErrReadNoConsumer's own message
      # (internal/credsvc/bitbucket.go, which the reader matches and reports as
      # one word). The status and the type alone are not enough: the service
      # answers the stop switches it cannot read, the kill switch, a bundle that
      # fails to load, a failed Bitbucket exchange and a failed audit with the
      # same 503 state_unavailable, and only the sentence tells them apart. And
      # anything else at all — a curl timeout (000), a Cloud Run 429 or 5xx, an
      # answer that is not a document — is this run NOT KNOWING. A stop reported
      # on any of those would be a stop that may never have happened, which is
      # the one thing this check exists to avoid; so they keep waiting and the
      # row fails at the bound saying which answer it got.
      [ "$MINT_STATUS" = 503 ] && [ "$MINT_CODE" = state_unavailable ] \
        && [ "$MINT_NO_CONSUMER" = yes ] && return 0
      ;;
    esac
    if [ "$waited" -ge "$MINT_WAIT" ]; then
      MINT_WAITED=$waited
      return 1
    fi
    local seen="${MINT_STATUS}${MINT_CODE:+ (refused: ${MINT_CODE})}"
    # A 503 state_unavailable that is not the no-consumer sentence does not age
    # into one: the service is not serving for another reason. The wait goes on
    # -- the cache is still the first thing to rule out -- but the line says
    # which refusal it is looking at.
    if [ "$MINT_STATUS" = 503 ] && [ "$MINT_CODE" = state_unavailable ] && [ "$MINT_NO_CONSUMER" != yes ]; then
      seen="$seen, and not with the no-consumer message this write should have made it answer with"
    fi
    local why="which is not what this write should have made it answer yet"
    if [ "$want" = ok ] && [ "$MINT_STATUS" = 200 ]; then
      why="which the service can still be answering from the document this write replaced, and only the cache's own lifetime tells the two apart"
    fi
    say "$what: the service answered ${seen}, ${why}; waiting for the bundle cache (${waited}s of ${MINT_WAIT}s)"
    sleep "$step"
    waited=$((waited + step))
  done
}

# mint_note: what the last mint's own answer adds to a row that has to say what
# it did not get. A timeout has curl's own line, and an answer that is not the
# mint is one of two things — a body that is not a document at all, or a document
# that carries no token — which an operator reads differently, so the reader
# tells them apart (cmd_mint's `answered`).
mint_note() {
  local note=""
  if [ "$MINT_STATUS" = 000 ]; then note=$(curl_note); fi
  case "$MINT_ANSWERED" in
  none) note="$note (the answer was not a document at all)" ;;
  no) note="$note (the answer carried no read token)" ;;
  esac
  printf '%s' "$note"
}

# mint_ok_got: what a row that expected the document a write just made says it
# got instead. The one answer that has to be spelled out is a 200 read before the
# cache could have expired: the service did answer a mint, and which document
# served it is exactly what this run cannot read.
mint_ok_got() {
  if [ "$MINT_STATUS" = 200 ] && [ "$MINT_ANSWERED" = yes ]; then
    printf 'the service answered 200 %ss after the write, which is inside the %ss this run waits for the bundle cache to expire, so this mint may be the credential the write replaced rather than the one it wrote — and only a reading taken once that cache has expired tells the two apart (raise CHECK148_MINT_WAIT above CHECK148_CACHE_WAIT and run it again)' "$MINT_WAITED" "$CACHE_WAIT"
    return 0
  fi
  printf 'the service answered %s%s' "${MINT_STATUS}${MINT_CODE:+ (refused: ${MINT_CODE})}" "$(mint_note)"
}

# step_mint_baseline: one mint before anything is written, and its token is kept
# for U13. A refused baseline is the end of the drill: nothing below it can be
# read, because every row after it is a difference from this one.
step_mint_baseline() {
  mint_once baseline
  if [ "$MINT_STATUS" != 200 ]; then
    local note
    note=$(mint_note)
    fact repository FAIL "the read mint refused before anything was written: the service answered $MINT_STATUS${MINT_CODE:+ (refused: ${MINT_CODE})}${note}. Check that your own address is one of the installation's operators (the person class), that the bundle holds the consumer, that the consumer's own key and secret are the ones Bitbucket accepts — the service exchanges them at Bitbucket's OAuth root on every mint, so a secret regenerated at Bitbucket and not yet written into the bundle is refused here, with the exchange's own message rather than the missing consumer's — and that the credentials service is on"
    stop "the baseline read mint was not served, so there is nothing to compare the drill's four writes against"
  fi
  if [ "$MINT_REPO" != same ]; then
    local why
    why=$(mint_note)
    fact repository FAIL "the read mint's answer names a repository other than the one this check asked for, which the client refuses as a token minted for another read (G11)${why}"
    stop "the read mint's answer does not name the repository it was asked for"
  fi
  fact repository PASS "the read mint served the person class for $REPO (HTTP 200, the answer names the repository it was asked for and carries an expiry: ${MINT_EXPIRES:-?}) — this token is kept, unprinted, for U13"
}

# step_stop takes the entry OUT of the bundle. No Fugaro command removes an
# entry, so this is a version written by hand — the document with the entry
# deleted, added with gcloud — and the version it replaced is disabled, because
# a superseded version that stays enabled keeps the consumer readable, which is
# the one thing removing it is for.
step_stop() {
  local before=$VERSION
  if ! run_capture "$SCRATCH/bundle.yaml" gcloud secrets versions access latest --secret "$SECRET" --project "$PROJECT"; then
    stop "gcloud secrets versions access did not answer for $SECRET, so this run cannot build the version that omits the entry. The stop drill needs to read the document and write it back without one block"
  fi
  chmod 600 "$SCRATCH/bundle.yaml" 2>/dev/null || true
  CREDENTIAL_FILES+=("$SCRATCH/bundle.yaml")
  local stripped rc=0
  stripped=$(reader strip "$SCRATCH/bundle.yaml" "$SCRATCH/bundle-stopped.yaml" 2>"$SCRATCH/reader.err") || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "the bundle document could not be read as the mapping this check strips an entry from: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). The document is in the run's scratch directory and is not printed"
  fi
  CREDENTIAL_FILES+=("$SCRATCH/bundle-stopped.yaml")
  local before_n after_n
  before_n=$(field_of entries_before "$stripped")
  after_n=$(field_of entries_after "$stripped")
  if [ "$after_n" != "$((before_n - 1))" ]; then
    stop "stripping the entry changed the document's entry count from $before_n to $after_n, which is not one fewer: the version written would not be the bundle with one credential taken out, so nothing was written"
  fi
  # The add can land the version AND exit non-zero — the same partial write
  # fugaro's own bundle writer names for creds set (internal/cli/creds.go, "a
  # version of this credential may exist") — so the Restore row is told what the
  # bundle MAY hold before the write rather than after it. Everything from here
  # to the disable is a state the operator has to read, not assume.
  LEFTOVER="a version of the bundle MAY have been written just now — the document with the bitbucket-read-consumer entry taken out — and the version it replaced ($before) is still enabled until this run says otherwise"
  if ! run_capture "$SCRATCH/added.txt" gcloud secrets versions add "$SECRET" --project "$PROJECT" --data-file "$SCRATCH/bundle-stopped.yaml"; then
    stop "gcloud secrets versions add did not answer for $SECRET. It can land the version and still exit non-zero, so the bundle MAY now hold the document with the entry taken out while the version it replaced ($before) is still enabled: read the bundle with fugaro creds ls before you go on"
  fi
  VERSION=$(newest_version)
  [ -n "$VERSION" ] && [ "$VERSION" != "$before" ] \
    || stop "the bundle's newest version is still $before after a version was added, so this run cannot tell which version it wrote and will not disable anything. Read the versions yourself (gcloud secrets versions list $SECRET): a version with the entry taken out may exist, and it may be the one the service reads"
  LEFTOVER="the bundle holds the document with the bitbucket-read-consumer entry taken out (version $VERSION), with version $before possibly still enabled"
  if ! run_capture "$SCRATCH/disabled.txt" gcloud secrets versions disable "$before" --secret "$SECRET" --project "$PROJECT"; then
    stop "gcloud secrets versions disable did not answer for version $before of $SECRET, so this run cannot tell whether the version it replaced is still enabled — and an enabled version still holds the consumer, which is the one thing the stop is for. Read the versions yourself (gcloud secrets versions list $SECRET) and disable $before by hand before going on"
  fi
  LEFTOVER="the bundle holds the document with the bitbucket-read-consumer entry taken out (version $VERSION, with version $before disabled)"
  if mint_want stop refused "the stop"; then
    fact stop PASS "with the entry out of the bundle (version $VERSION; version $before disabled) the read mint is refused with the service's own no-consumer answer: ${MINT_STATUS}${MINT_CODE:+ (refused: ${MINT_CODE})} carrying ErrReadNoConsumer's message, so the bundle the service reads really holds no read consumer rather than being served from another entry"
    return 0
  fi
  # Which answer the bound ran out on decides what the row can say. A 200 is the
  # mint still serving; anything else is the run failing to read the refusal it
  # needs, and the row says which answer it got rather than calling it a stop.
  if [ "$MINT_STATUS" = 200 ]; then
    fact stop FAIL "the read mint was still served (HTTP 200) ${MINT_WAIT}s after the entry was taken out of the bundle and the version it replaced was disabled: either the service is reading a document that still holds it — another version is enabled, or something else wrote one — or the mint is not reading the bundle at all. Read the bundle with fugaro creds ls and the service's own log before you trust a stop"
  else
    local note
    note=$(mint_note)
    local why="this row is a reading this run does not have rather than a stop it measured"
    if [ "$MINT_STATUS" = 503 ] && [ "$MINT_CODE" = state_unavailable ]; then
      why="the service answered that status and type with a message that is not ErrReadNoConsumer's, and it answers the stop switches it cannot read, the kill switch, a bundle that fails to load, a failed Bitbucket exchange, a token whose scopes this route will not hand on and a failed audit with the same pair (among other states) — so which of those this is cannot be read from here"
    fi
    fact stop FAIL "the read mint did not answer with the service's own refusal ${MINT_WAIT}s after the entry was taken out of the bundle: the service answered ${MINT_STATUS}${MINT_CODE:+ (${MINT_CODE})}${note}. The refusal a stop is read on is 503 state_unavailable carrying ErrReadNoConsumer's own message, and ${why} — read the service's own log and the bundle with fugaro creds ls before you trust a stop"
  fi
  return 1
}

# newest_version: the bundle's newest version, from the listing, so the version
# this run must disable is read rather than assumed to be the previous number
# plus one.
newest_version() {
  local answer
  run_capture "$SCRATCH/creds.json" fugaro creds ls --json || return 0
  answer=$(reader creds <"$SCRATCH/creds.json") || return 0
  field_of version "$answer"
}

# set_consumer PATH WHAT: one bundle write through the bundle's only writer,
# with the document on stdin so the shell never holds a value.
set_consumer() {
  # The same partial write the stop drill's own add has: a version MAY exist
  # even when the command exits non-zero, so the Restore row is told what the
  # bundle may hold before the write and the caller tightens it once the write
  # is known to have landed.
  LEFTOVER="a fugaro creds set of the $2 may have landed: the bundle may hold it, or it may hold what the run left before it"
  if ! fugaro creds set bitbucket-read-consumer <"$1" >"$SCRATCH/set.out" 2>"$SCRATCH/set.err"; then
    sed -n '1,5p' "$SCRATCH/set.err" >&4 || true
    stop "fugaro creds set bitbucket-read-consumer refused the $2: its message is above. A refused write is not always a write that did not land — a version of the credential may exist — so read the entry with fugaro creds ls before you trust what the bundle holds"
  fi
}

# step_restore_consumer writes the consumer back, which is the drill's own way
# out of the stop: the entry is one the bundle's writer sets like any other.
step_restore_consumer() {
  set_consumer "$CONSUMER_FILE" "consumer the bundle held before the stop"
  LEFTOVER="the bundle holds the consumer it held before the stop"
  if mint_want restore-consumer ok "the restore"; then
    fact restore-consumer PASS "fugaro creds set put the read consumer back and the mint is served again (HTTP 200 after a ${MINT_WAITED}s wait for the bundle cache, which is the wait a write's own document is read on — a mint read sooner can be the document the write replaced): the stop is a bundle write and its undo is one too"
    return 0
  fi
  fact restore-consumer FAIL "the read mint is not serving the consumer ${MINT_WAIT}s after fugaro creds set put it back ($(mint_ok_got)): the entry this run wrote is not the one the service reads, and the rotation below would measure a bundle that is not serving"
  return 1
}

# step_rotate_ask: the owner's own step, and the one moment the drill waits on a
# person. It asks for the regeneration, waits for the answer, and holds the file
# that was written to the consumer file's own key and workspace — which is what
# makes the document it is about to write the SAME consumer with a new secret
# rather than a second consumer. It writes nothing itself.
step_rotate_ask() {
  owner "regenerate the secret of the read consumer at Bitbucket NOW — the consumer the bundle holds (workspace $REPO_WS): the same consumer, the same key, and Bitbucket's own regenerate step. Then write its document into $ROTATED_CONSUMER_FILE — key, secret and workspace, one per line, the secret Bitbucket just gave you — and type ROTATED. Ctrl-C stops here: the bundle holds the consumer fugaro creds set put back, nothing at Bitbucket has changed, and this drill has written nothing since"
  local answer=""
  read -r answer || true
  if [ "$answer" != ROTATED ]; then
    stop "the rotation did not run: the drill asked you to regenerate the secret at Bitbucket and write $ROTATED_CONSUMER_FILE, and the answer was '${answer}'. Nothing was written by this step, and nothing at Bitbucket has changed — the bundle holds the consumer it held before the stop. Run the drill again and type ROTATED once the file is written: without the regeneration the drill can measure neither the rotated document nor the refusal of the secret it replaced"
  fi
  [ -f "$ROTATED_CONSUMER_FILE" ] || stop "no file at $ROTATED_CONSUMER_FILE after you typed ROTATED, so there is no rotated document to write: the drill does not guess one. Write the consumer's document there — key, secret and workspace, one per line, the secret Bitbucket just gave you — and run the drill again"
  local rot rot_ws rot_key
  rot=$(read_shape "$ROTATED_CONSUMER_FILE" "the rotated consumer file")
  rot_ws=$(field_of workspace "$rot")
  rot_key=$(field_of keyfp "$rot")
  [ "$rot_ws" = "$REPO_WS" ] || stop "the rotated consumer file names the workspace '$rot_ws' and the consumer file names '$REPO_WS': a rotation regenerates the SECRET of the consumer you already have, so the workspace and the key stay. Nothing was written by this step"
  [ "$rot_key" = "$CONSUMER_KEYFP" ] || stop "the rotated consumer file carries a DIFFERENT consumer key from the consumer file: a rotation regenerates the secret of the consumer you already have, so the key stays. If you created a second consumer instead, the drill's rotation step is not what you have — create the successor with --successor-consumer-file and rotate the first one at Bitbucket. Nothing was written by this step"
}

# step_rotate: the secret regenerated at Bitbucket, written into the bundle, and
# then the two readings that make the rotation a fact rather than a write — the
# OLD secret must be refused, and U13 is whether the token minted BEFORE the
# rotation still reads.
step_rotate() {
  step_rotate_ask
  set_consumer "$ROTATED_CONSUMER_FILE" "rotated consumer"
  LEFTOVER="the bundle holds the rotated consumer (the same key with the secret Bitbucket regenerated)"
  if ! mint_want rotate ok "the rotation"; then
    fact rotate FAIL "the read mint is not serving the rotated document ${MINT_WAIT}s after it was written ($(mint_ok_got)): either the new secret is not the one Bitbucket holds — regenerate it and write the file again — or the bundle did not take the write"
    return 1
  fi
  fact rotate PASS "the rotated document is live: the read mint is served again on it (HTTP 200 after a ${MINT_WAITED}s wait for the bundle cache, which is the wait a write's own document is read on — a mint read sooner can be the credential this run replaced), so the secret this run wrote is the one Bitbucket accepts"

  # The old secret, used the way a consumer is used directly: a
  # client_credentials grant at Bitbucket. It must be refused — a regenerated
  # secret is the whole point of the step — and its refusal is what says the
  # bundle's copy is no longer a credential.
  reader grantconf "$CONSUMER_FILE" "$SCRATCH/grant.conf" >/dev/null 2>&1 \
    || stop "the pre-rotation consumer's key and secret could not be written into a request config file, so the old secret cannot be tried"
  CREDENTIAL_FILES+=("$SCRATCH/grant.conf")
  local status
  status=$(request "$SCRATCH/grant.json" POST "$OAUTH_URL/site/oauth2/access_token" --config "$SCRATCH/grant.conf")
  CREDENTIAL_FILES+=("$SCRATCH/grant.json")
  local granted
  granted=$(reader grant "$SCRATCH/grant.json" 2>/dev/null || true)
  case "$granted" in
  yes)
    fact old-secret FAIL "the secret this run replaced still exchanges for a token (HTTP $status): Bitbucket did not revoke it, so the rotation left a live credential behind. Delete the old secret at Bitbucket or regenerate it again, and do not treat this rotation as done"
    ;;
  refused*)
    # `refused` is the endpoint's own answer about the secret: Bitbucket read it
    # and would not exchange it, which is the reading a rotation is done on. It
    # is the word the reader prints for a document it read, and nothing else
    # prints it — so a request that answered no document at all (`unanswered`,
    # or a reader that said nothing) cannot reach this row as a PASS.
    fact old-secret PASS "the replaced secret is refused (HTTP $status, $granted): the rotation took, and the copy this run replaced is no longer a credential"
    ;;
  *)
    local why="the OAuth endpoint answered"
    if [ "$status" = 000 ]; then
      why="$why no answer at all$(curl_note)"
    else
      why="$why HTTP $status with no token answer to read"
    fi
    fact old-secret FAIL "$why, so whether the replaced secret still exchanges for a token is a reading this run does not have — and a rotation whose old secret was never tried is not a rotation this run can call done. Run the grant by hand (the runbook's §12) before you trust it"
    ;;
  esac

  # U13: the token minted before the rotation, replayed against Bitbucket. It is
  # an OPEN QUESTION and never a verdict — the check records which of the two it
  # is, and the runbook says so where it tells an operator to rotate quietly.
  reader apiconf "$SCRATCH/token-baseline" "$SCRATCH/api.conf" >/dev/null 2>&1 \
    || stop "the token minted before the rotation could not be written into a request config file, so U13 cannot be measured"
  CREDENTIAL_FILES+=("$SCRATCH/api.conf")
  local api_status read
  api_status=$(request "$SCRATCH/api.json" GET "$API_URL/repositories/$REPO" --config "$SCRATCH/api.conf")
  read=$(reader api "$SCRATCH/api.json" 2>/dev/null || true)
  if [ "$read" = yes ]; then
    fact u13 INFO "U13 measured: the token minted BEFORE the rotation still reads $REPO (HTTP $api_status), so a rotation does not end the tokens already handed out — revoke the old secret at Bitbucket as well as regenerating it, and treat a rotated-away token as live until it expires. This is an open question answered, not a failure"
  elif [ "$read" = no ] && { [ "$api_status" = 401 ] || [ "$api_status" = 403 ]; }; then
    # The refused reading, and only where Bitbucket's own answer is an
    # authorization one: a document the token may not read is the reading this
    # question is about, while a 5xx is the API not answering it at all.
    fact u13 INFO "U13 measured: the token minted BEFORE the rotation is refused (HTTP $api_status), so this rotation ended it. Whether that is Bitbucket's rule or this token's age is what the runbook leaves open until Check 145's lifetime reading (U1) is in hand. This is an open question answered, not a failure"
  else
    local why="the request did not come back with a document to read"
    if [ "$read" = no ]; then
      why="Bitbucket answered HTTP $api_status without the repository in it, which is not an authorization answer about the token"
    fi
    fact u13 INFO "U13 NOT measured: whether the token minted BEFORE the rotation still reads $REPO is a reading this run does not have — $why. Run the read by hand after the rotation and record which of the two it gets: the reconciliation carries U13 as UNVERIFIED, and this check is what settles it. An open question left open is not a failure"
  fi
  return 0
}

# step_transfer: a consumer created under the SECOND sandbox account, written
# into the bundle. The mint is the confirmation, and it is the successor's own
# credential that serves it — which is what "ownership transferred" means here.
step_transfer() {
  set_consumer "$SUCCESSOR_CONSUMER_FILE" "successor consumer"
  LEFTOVER="the bundle holds the successor consumer, which is the transfer"
  # The write reported success, so the successor is the bundle's entry and the
  # consumer the rotation regenerated is no longer named by it: the Restore row's
  # delete is about that consumer, and it is only safe to name from here.
  TRANSFERRED=yes
  if ! mint_want transfer ok "the transfer"; then
    fact transfer FAIL "the read mint is not serving the successor ${MINT_WAIT}s after its document was written ($(mint_ok_got)): the successor's own key and secret have to be the ones Bitbucket accepts for $REPO_WS, and a consumer created under another account only reads what that account reads"
    return 1
  fi
  fact transfer PASS "the successor consumer is live: the read mint is served again (HTTP 200 after a ${MINT_WAITED}s wait for the bundle cache, so the credential that served it is the successor's rather than the one the rotation left) on the second account's own consumer, so the bundle's entry is the credential the mint spends and replacing it is the transfer"
  return 0
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(installation service read-consumer consumer-files repository confirm xtrace stop restore-consumer rotate old-secret u13 transfer restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 148 RESULT\n' >&3
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
  printf 'CHECK 148 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
USAGE="check148.sh --installation <sandbox> --repo <owner/name> --consumer-file <path> --rotated-consumer-file <path> --successor-consumer-file <path> [--yes]"

parse_args() {
  INSTALLATION=""
  REPO=""
  CONSUMER_FILE=""
  ROTATED_CONSUMER_FILE=""
  SUCCESSOR_CONSUMER_FILE=""
  ASSUME_YES=no
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a project ID. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs a repository in the form owner/name. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --consumer-file)
      [ $# -ge 2 ] || die "--consumer-file needs a path. usage: $USAGE"
      CONSUMER_FILE=$2
      shift 2
      ;;
    --rotated-consumer-file)
      [ $# -ge 2 ] || die "--rotated-consumer-file needs a path. usage: $USAGE"
      ROTATED_CONSUMER_FILE=$2
      shift 2
      ;;
    --successor-consumer-file)
      [ $# -ge 2 ] || die "--successor-consumer-file needs a path. usage: $USAGE"
      SUCCESSOR_CONSUMER_FILE=$2
      shift 2
      ;;
    --yes)
      ASSUME_YES=yes
      shift
      ;;
    -h | --help)
      HELP_SHOWN=yes
      printf 'usage: %s\n' "$USAGE"
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$INSTALLATION" ] || die "--installation is required. usage: $USAGE"
  [ -n "$REPO" ] || die "--repo is required. usage: $USAGE"
  [ -n "$CONSUMER_FILE" ] || die "--consumer-file is required. usage: $USAGE"
  [ -n "$ROTATED_CONSUMER_FILE" ] || die "--rotated-consumer-file is required. usage: $USAGE"
  [ -n "$SUCCESSOR_CONSUMER_FILE" ] || die "--successor-consumer-file is required. usage: $USAGE"
  case "$REPO" in
  */*/* | /* | */ | "") die "--repo '$REPO' is not a repository in the form owner/name" ;;
  esac
}

main() {
  need_tools
  parse_args "$@"
  guard_sandbox

  PROJECT=""
  REGION=""
  NAME=""
  SECRET=""
  VERSION=""
  REPO_WS=""
  SERVICE_URL=""
  # The read mint's path is a fact of the service, not a knob: credsvc's
  # BitbucketReadPath and the client's gitprov/bitbucket.ServiceReadPath are held
  # to each other by internal/cli/docs_bitbucket_security_test.go, and a check
  # that could be pointed at another path would measure a route nobody serves.
  READ_PATH=/v1/bitbucket/read-token
  OAUTH_URL=${CHECK148_OAUTH_URL:-https://bitbucket.org}
  API_URL=${CHECK148_API_URL:-https://api.bitbucket.org/2.0}
  CURL_SECONDS=${CHECK148_CURL_SECONDS:-30}
  MINT_WAIT=${CHECK148_MINT_WAIT:-90}
  # credsvc.BundleCacheTTL is 30 seconds; the reading a write's own document is
  # read on is taken clear of it and of the loop's own 5-second step.
  CACHE_WAIT=${CHECK148_CACHE_WAIT:-40}
  MINT_STATUS=""
  MINT_CODE=""
  MINT_REPO=""
  MINT_EXPIRES=""
  MINT_TOKEN=""
  MINT_ANSWERED=""
  MINT_NO_CONSUMER=""
  MINT_WAITED=0
  # The consumer file's own key, kept as a fingerprint (never printed) so the
  # rotation can hold the rotated document to the same consumer; and whether the
  # transfer's write landed, which decides whether any consumer is owed a delete.
  CONSUMER_KEYFP=""
  TRANSFERRED=no

  guard_endpoint "the OAuth root" "$OAUTH_URL"
  guard_endpoint "the API root" "$API_URL"

  scratch_up
  write_files
  guard_xtrace
  step_installation
  step_service
  step_consumer
  step_confirm
  step_mint_baseline
  step_stop || true
  step_restore_consumer || true
  step_rotate || true
  step_transfer || true

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

write_files() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 148's reader: the one place a credential is handled.

The shell never holds the consumer's key or secret, the bundle document's
values, a Google ID token or a Bitbucket token. This program reads those out of
files and writes them straight into the 0600 files it is told to write them
into, and the only text it prints is a fixed vocabulary -- present/absent,
yes/no, a count, a fingerprint, or an error CODE from a closed set. Nothing from
a response is printed as it arrived, so an endpoint that echoes back what it was
given cannot reach the check's result block through this.
"""
import hashlib, json, os, re, sys

# What a credential may look like to be written into a curl config file: one
# line of printable ASCII with no space. A value that is anything else is
# refused, and its own text is never printed -- the message names the FIELD.
VALUE = re.compile(r"[\x21-\x7e]+")
# The entry this drill is about, as the bundle spells it.
ENTRY = "bitbucket-read-consumer"
# The credsvc and Bitbucket error codes this check knows. A code outside the set
# is dropped rather than printed: an error body is written by the service, and a
# field of its choosing must not reach the block.
CODES = {
    # credsvc's own refusal types (internal/credsvc/wire.go): the service's
    # state, the run's identity, the caller's Google identity, and a request the
    # gateway's rules refuse. A refusal whose code is dropped here reaches the
    # block with no code at all, which is how a row about a refusal stops saying
    # which refusal it measured.
    "state_unavailable", "exhausted_until", "unverified_run_identity",
    "unverified_job_identity", "request_refused",
    "no_credential", "unauthorized", "forbidden",
    "invalid_request", "invalid_client", "invalid_grant", "invalid_scope",
    "invalid_token", "insufficient_scope", "not_found", "access_denied",
    "unsupported_grant_type", "unauthorized_client", "server_error",
    "bad_request", "method_not_allowed", "conflict", "too_many_requests",
}
# The sentence internal/credsvc's read mint answers with when the credentials
# bundle holds no read consumer (bitbucket.go's ErrReadNoConsumer, carried as
# the wire body's message). The service refuses a whole family of states with
# the same 503 state_unavailable -- the operator's stop switches it cannot read,
# the kill switch, a bundle that fails to load, a failed Bitbucket exchange, a
# failed audit -- so the status and the type cannot tell the missing consumer
# from any of them. Only this sentence can, and the stop row reads on it: a 503
# state_unavailable that says something else is a service not serving for
# another reason, not a stop this run measured.
NO_CONSUMER = "credsvc: the credentials bundle holds no Bitbucket read consumer"


def die(message):
    print(message, file=sys.stderr)
    sys.exit(1)


def load(path, what):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        die("%s is not readable JSON" % what)


def load_or_none(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return None


def write_private(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, text.encode("utf-8"))
    finally:
        os.close(fd)
    os.chmod(path, 0o600)


def read_private(path, what):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return fh.read().strip()
    except OSError:
        die("%s could not be read back" % what)


def escaped(value):
    return value.replace("\\", "\\\\").replace('"', '\\"')


def one_line(value, field):
    if not value or not VALUE.fullmatch(value):
        die("the %s is not one line of printable text without spaces" % field)
    return value


def consumer_mapping(path):
    """The key, the secret and the workspace of a consumer file, which is the
    mapping the bundle's entry holds: one field per line, YAML."""
    try:
        with open(path, "r", encoding="utf-8") as fh:
            raw = fh.read()
    except OSError:
        die("the consumer file could not be read")
    doc = {}
    for line in raw.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if line[:1].isspace():
            # A continuation line of a block scalar: the value this check reads
            # is one line, and a value that is not one is refused below.
            die("the consumer file's value is more than one line")
        if ":" not in line:
            die("the consumer file is not a mapping of key, secret and workspace")
        name, _, value = line.partition(":")
        name, value = name.strip(), value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        if name in doc:
            die("the consumer file carries %s twice" % name)
        doc[name] = value
    unknown = sorted(set(doc) - {"key", "secret", "workspace"})
    if unknown:
        die("the consumer file carries field(s) this check does not read: %s" % ", ".join(unknown))
    for field in ("key", "secret", "workspace"):
        if field not in doc:
            die("the consumer file carries no %s" % field)
    return doc


def cmd_doctor():
    doc = json.load(sys.stdin)
    project = doc.get("project") or {}
    print("name=%s" % (project.get("name") or ""))
    print("project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))


def cmd_joblist():
    found = []
    for job in json.load(sys.stdin):
        labels = (job.get("metadata") or {}).get("labels") or {}
        if labels.get("fugaro_role") == "coordinator":
            found.append(job)
    if len(found) != 1:
        die("the project lists %d coordinator jobs" % len(found))
    print("job=%s" % ((found[0].get("metadata") or {}).get("name") or ""))


def env_of(job):
    out = {}
    spec = (job.get("spec") or {}).get("template") or {}
    spec = (spec.get("template") or {}).get("containers") or []
    for container in spec:
        for entry in container.get("env") or []:
            value = entry.get("value")
            if value is None:
                value = ""  # a value_source entry: a mounted secret, not a URL
            out[entry.get("name") or ""] = value
    return out


def cmd_job():
    env = env_of(json.load(sys.stdin))
    print("url=%s" % (env.get("FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL") or ""))


def cmd_creds():
    doc = json.load(sys.stdin)
    rows = doc.get("credentials") or []
    present = any(r.get("name") == ENTRY and r.get("present") for r in rows)
    print("secret=%s" % (doc.get("secret") or ""))
    print("version=%s" % (doc.get("version") or ""))
    print("present=%s" % ("yes" if present else "no"))


def cmd_shape(path):
    doc = consumer_mapping(path)
    key = one_line(doc["key"], "consumer key")
    one_line(doc["secret"], "consumer secret")
    workspace = one_line(doc["workspace"], "consumer workspace")
    # The key is compared as a fingerprint so that two files can be held to each
    # other without either key reaching the block.
    print("keyfp=%s" % hashlib.sha256(key.encode("utf-8")).hexdigest()[:16])
    print("workspace=%s" % workspace)
    print("mode=%o" % (os.stat(path).st_mode & 0o777))


def cmd_strip(src, dst):
    """The bundle document with the read consumer's entry taken out.

    The document is a YAML mapping of name to value, so the entry is its own
    key line and everything indented under it: that block is dropped and every
    other line is copied byte for byte, which is what makes the version this
    writes the bundle with one credential missing rather than a re-encoding of
    it."""
    try:
        with open(src, "r", encoding="utf-8") as fh:
            lines = fh.read().splitlines(keepends=True)
    except OSError:
        die("the bundle document could not be read")
    key_line = re.compile(r"^%s\s*:" % re.escape(ENTRY))
    top_key = re.compile(r"^[^\s#][^:]*:")
    out, dropped = [], 0
    skipping = False
    for line in lines:
        if key_line.match(line):
            skipping = True
            dropped += 1
            continue
        if skipping:
            if not line.strip() or line[:1].isspace():
                continue
            skipping = False
        out.append(line)
    if dropped != 1:
        die("the bundle document carries %d %s entries, want exactly one" % (dropped, ENTRY))
    # The two counts are read off the two documents rather than one being the
    # other minus one: the shell holds the result to `after == before - 1`, and
    # an identity would hold whatever the strip had actually done.
    before = sum(1 for line in lines if top_key.match(line))
    after = sum(1 for line in out if top_key.match(line))
    if after < 1:
        die("taking the entry out would leave the bundle empty, which is not a document the loader reads")
    write_private(dst, "".join(out))
    print("entries_before=%d" % before)
    print("entries_after=%d" % after)


def cmd_mintconf(tokenfile, conf, repo):
    token = one_line(read_private(tokenfile, "the identity token"), "identity token")
    body = json.dumps({"repository": repo}, separators=(",", ":"))
    write_private(conf, 'header = "Authorization: Bearer %s"\nheader = "Content-Type: application/json"\n'
                        'header = "Accept: application/json"\ndata = "%s"\n'
                  % (escaped(token), escaped(body)))
    print("conf=written")


def cmd_mint(path, tokenout, repo):
    # An answer that is not a document at all is a status and no reading: a curl
    # timeout (000), a dropped connection, a proxy's own error page. It prints as
    # exactly that rather than ending the run, because the caller has to be able
    # to tell "the service refused" apart from "there was no answer" -- and a
    # check that treated the second as the first would report a refusal it never
    # read. A document that parses and carries a token that is not one line is
    # still a die: that is the service answering with something it cannot mean.
    doc = load_or_none(path)
    if doc is None:
        print("code=")
        print("token=no")
        print("repo=-")
        print("expires=absent")
        # `answered=none` is the answer there was not one: a document that is not
        # the mint's at all reads as `no` below, and a row that has to say which
        # of the two it got cannot read that from `answered` alone.
        print("answered=none")
        print("noconsumer=no")
        return
    code = doc.get("error") if isinstance(doc, dict) else None
    print("code=%s" % (code if isinstance(code, str) and code in CODES else ""))
    # Whether this refusal is the one a stop is read on, as one word: yes or no.
    # The message itself is never printed -- it is the service's text, and what
    # the shell needs from it is only whether it is ErrReadNoConsumer's.
    message = doc.get("message") if isinstance(doc, dict) else None
    print("noconsumer=%s" % ("yes" if message == NO_CONSUMER else "no"))
    if not isinstance(doc, dict) or not isinstance(doc.get("token"), str) or not doc["token"].strip():
        print("token=no")
        print("repo=-")
        print("expires=absent")
        # A document, and not the mint's: every path out of this function prints
        # `answered`, so a row can say which of the two a 200 was.
        print("answered=no")
        return
    token = doc["token"].strip()
    if not VALUE.fullmatch(token):
        die("the read mint's answer carries a token that is not one line of printable text")
    write_private(tokenout, token)
    print("token=yes")
    # The repository the answer names is held to the one this check asked for:
    # neither name is printed, so a mint for another repository reads as `other`
    # rather than as the repository it was for.
    print("repo=same" if doc.get("repository") == repo else "other")
    print("expires=%s" % ("present" if doc.get("expires_at") else "absent"))
    print("answered=yes")


def cmd_grantconf(consumer, conf):
    doc = consumer_mapping(consumer)
    key = one_line(doc["key"], "consumer key")
    secret = one_line(doc["secret"], "consumer secret")
    write_private(conf, 'user = "%s:%s"\nheader = "Accept: application/json"\ndata = "grant_type=client_credentials"\n'
                  % (escaped(key), escaped(secret)))
    print("conf=written")


def cmd_grant(path):
    """One word: `yes` when Bitbucket exchanged the consumer for a token,
    `refused` (with a code from the closed set) when it did not, and
    `unanswered` when there is no document to read at all -- a timeout, a dropped
    connection, a proxy's error page. The shell compares the word, so the word is
    all that is printed, and the third one is what keeps a request that was never
    answered from being read as the endpoint's own refusal of the secret."""
    doc = load_or_none(path)
    if doc is None:
        print("unanswered")
        return
    token = doc.get("access_token") if isinstance(doc, dict) else None
    if isinstance(token, str) and token.strip():
        print("yes")
        return
    code = doc.get("error") if isinstance(doc, dict) else None
    print("refused %s" % ("code=%s" % code if isinstance(code, str) and code in CODES else "no code"))


def cmd_apiconf(tokenfile, conf):
    token = one_line(read_private(tokenfile, "the read token"), "read token")
    write_private(conf, 'header = "Authorization: Bearer %s"\nheader = "Accept: application/json"\n' % escaped(token))
    print("conf=written")


def cmd_api(path):
    """One word: `yes` when the token read the repository, `no` when Bitbucket
    answered a document that does not carry it, and `unanswered` when there is no
    document at all. U13 is which of the three a token minted before the rotation
    gets, and the shell records that word rather than a verdict about it: a
    request that never came back is not a reading that the token was refused."""
    doc = load_or_none(path)
    if doc is None:
        print("unanswered")
        return
    if isinstance(doc, dict) and isinstance(doc.get("full_name"), str) and doc["full_name"]:
        print("yes")
        return
    print("no")


def main(argv):
    if not argv:
        die("no subcommand")
    cmd = argv[0]
    if cmd == "doctor":
        cmd_doctor()
    elif cmd == "joblist":
        cmd_joblist()
    elif cmd == "job":
        cmd_job()
    elif cmd == "creds":
        cmd_creds()
    elif cmd == "shape":
        cmd_shape(argv[1])
    elif cmd == "strip":
        cmd_strip(argv[1], argv[2])
    elif cmd == "mintconf":
        cmd_mintconf(argv[1], argv[2], argv[3])
    elif cmd == "mint":
        cmd_mint(argv[1], argv[2], argv[3])
    elif cmd == "grantconf":
        cmd_grantconf(argv[1], argv[2])
    elif cmd == "grant":
        cmd_grant(argv[1])
    elif cmd == "apiconf":
        cmd_apiconf(argv[1], argv[2])
    elif cmd == "api":
        cmd_api(argv[1])
    else:
        die("unknown subcommand %s" % cmd)


main(sys.argv[1:])
READER_EOF
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}
trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
