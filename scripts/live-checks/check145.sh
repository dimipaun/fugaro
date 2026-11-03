#!/usr/bin/env bash
# Check 145 — the Bitbucket read consumer's facts, as ONE script YOU run.
#
# It answers the questions the reconciliation carries as UNVERIFIED about a
# Bitbucket Cloud OAuth consumer minted with the client_credentials grant: how
# long its token lives, whether a refresh token comes back and whether a token
# can be revoked, whether it reaches past the workspace it was minted in, what
# the sandbox's own plan admits, the workspace's seat count, whether IP
# allowlisting applies, whether the answer lists the granted scopes, whether two
# grants return distinct tokens, what rights creating a consumer needs and what
# the token reaches beyond its scopes, and whether the consumer has to be
# private. It also shows that a WRITE with the read token is refused, which is
# the read-only guard the read mint is written to.
#
# It is the precondition of the read mint (O4): the read mint does not merge
# before this check passes, and if U3 shows reach beyond the workspace, D360 and
# D361 go back to the owner first. The register's row and section are
# docs/gcp-live-checklist.md, "Check 145".
#
# Usage:      bash scripts/live-checks/check145.sh \
#               --consumer-file <path> --workspace <sandbox> \
#               --second-workspace <sandbox-2>
#             The consumer file holds the read consumer as JSON, the same
#             mapping the credentials service's bundle holds for the
#             `bitbucket-read-consumer` entry (`{"key": ..., "secret": ...}`,
#             with an optional `workspace` that must agree with --workspace if
#             it is there). It is the ONE place the key and the secret live:
#             this script never holds either value in a variable, on a command
#             line or in its output — the reader below writes them straight
#             into a 0600 file the requests are pointed at.
# Needs:      bash, mktemp, python3 (the reader), curl. Nothing is installed and
#             nothing is fetched; if one of those is missing the script stops
#             with one line naming it.
# Where:      SANDBOX workspaces only, and never `belong`: this check refuses
#             `belong` by name. It creates nothing, deletes nothing and writes
#             nothing outside its own scratch directory.
# Costs:      a handful of API calls on the sandbox workspace. No cloud project
#             is touched and no Fugaro installation is involved.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory and the files
#             holding the consumer's key and secret and the minted tokens are
#             removed, and the block's `restore` row says whether they really
#             went — a FAILed row there names what to remove by hand.
# Output:     progress on stderr, ONE block on stdout headed CHECK 145 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, the exchange was refused).
#
# NO SECRET IN ANY OUTPUT is a property of the design, not a promise kept by
# care: the shell never holds the key, the secret or a token. Every request's
# credential is written into a 0600 curl config file by the reader, which the
# request names with --config — so the value is not on a command line either,
# where `ps` would show it to every user of the machine. What the reader prints
# is a fixed vocabulary: a scope word, a workspace permission, an OAuth error
# code, a count, a duration, and one-word answers like present, absent, distinct
# or identical. Nothing from a response is printed as it arrived, so an endpoint
# that echoes back what it was given — which this check's own test uses as its
# ordinary fake — cannot reach the block. curl's own messages go to a log inside
# the scratch directory and only a redacted excerpt of one ever reaches the
# block. And no trace: `bash -x` prints every command as it runs, so guard_xtrace
# REFUSES a run whose shell arrived with tracing on.
#
# CHECK145_OAUTH_URL, CHECK145_API_URL and CHECK145_CURL_SECONDS exist for this
# script's own test: the first two may only name a loopback address of exactly
# the form http://127.0.0.1:<port> (the test's fake Bitbucket), so no
# environment can send a minted token anywhere but Bitbucket Cloud's own
# endpoints, and the third shortens the requests' cap.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. `bash -x check145.sh`
# is what someone reaches for when a run failed and they want a log to paste,
# and this script's commands carry a credential: the reader's argv, the curl
# calls and the copies of answers would be traced in full. So the trace is
# turned off here, BASH_XTRACEFD is unset so a trace redirected to a file this
# script knows nothing about cannot be reopened, and guard_xtrace refuses a
# shell that arrived tracing (the flag sits in SHELLOPTS/BASHOPTS then, and every
# child would start tracing on its own).
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
ABORT_REASON=""
# Set to yes by main once every step has run, so the exit trap can tell a
# finished run from one that ended on an error of its own.
REACHED_END=no
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
# all, and the block would then read as "the consumer is fine" for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the consumer (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 145 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 145 needs bash"
  need mktemp
  need python3 "the reader that handles the consumer's key and secret and mints nothing into this shell is one python3 file"
  need curl
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk145-mine"
SCRATCH_REMOVED=no
CREDENTIALS_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
# The files the run writes a credential into, and nothing else: the consumer
# config (the key and the secret, as curl's own user line), the two token files
# and the two configs built from them, and the revoke config. They are removed
# by cleanup and their removal is READ, not assumed -- the `restore` row reports
# it, because a credential file that survived is the one thing an owner has to
# be told about rather than read a PASS over.
CREDENTIAL_FILES=()
CURL_LOG=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk145.XXXXXXXX") || die "Check 145 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  CURL_LOG="$SCRATCH/curl.log"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 145 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
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
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the consumer when the steps
  # that would have filled them never ran. Any such ending is an abort, and 2
  # says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the consumer"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is no credential file and nothing to remove"
  else
    [ "$CREDENTIALS_REMOVED" = yes ] && [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local cred_note="the file holding the consumer's key and secret and the files holding the minted tokens are removed"
    [ "$CREDENTIALS_REMOVED" = yes ] || cred_note="A CREDENTIAL FILE IS STILL THERE — look in $SCRATCH yourself and remove it: it holds the consumer's key and secret or a minted token"
    facts="${facts}"$'\n'"restore|$restore|${cred_note}, and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
# guard_xtrace: refuse to run when this shell arrived with tracing on. The trace
# itself is off from the first line, but with -x the flag is also in
# SHELLOPTS/BASHOPTS in the environment, and every child shell inherits those:
# the reader's own commands, and the values it handles, would be traced.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check145.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry a credential"
}

# guard_endpoints: the two endpoints are Bitbucket Cloud's own unless this
# script's own test names a loopback address, and a value that is neither is
# refused. A check that could be pointed at another host would mint the token
# and then send it wherever the environment said.
loopback_url() {
  local rest port
  # A value with userinfo is refused outright: curl reads everything before the
  # '@' as a username and password and connects to the host AFTER it, so
  # `http://127.0.0.1:1@host/` is not a loopback address at all -- it is a
  # request that hands the consumer's Basic credential to `host`.
  case "$1" in
  *@*) return 1 ;;
  http://127.0.0.1:*) rest=${1#http://127.0.0.1:} ;;
  *) return 1 ;;
  esac
  # What is left must be the port and then a path or the end of the string, and
  # the port must be digits: a query, a fragment, a backslash or a space after
  # the port is refused rather than passed to curl, which is free to read such a
  # value as a different address than the one this check means.
  port=${rest%%/*}
  case "$port" in
  "" | *[!0-9]*) return 1 ;;
  esac
  case "$rest" in
  "$port" | "$port"/*) return 0 ;;
  esac
  return 1
}
guard_endpoints() {
  local name value
  for name in CHECK145_OAUTH_URL CHECK145_API_URL; do
    value=$(printenv "$name" 2>/dev/null || true)
    [ -n "$value" ] || continue
    loopback_url "$value" \
      || die "Check 145 refused: $name may only name a loopback address of the form http://127.0.0.1:<port> (with no userinfo, query, fragment or space), so a minted token can never leave this machine (it is set for this check's own test and for nothing else)"
  done
}

# guard_workspaces: `belong` is the workspace this check must never run against.
# The register says so for every Bitbucket check, and a script that can be
# pointed at it by a typo has no business handling the installation's own
# workspace.
guard_workspaces() {
  local ws
  for ws in "$WORKSPACE" "$SECOND_WORKSPACE"; do
    [ -n "$ws" ] || die "usage: $USAGE"
    # A lowercased copy, so every spelling of the dev installation is refused:
    # a list of the spellings someone thought of ('belong', 'Belong', 'BELONG')
    # is exactly the guard a typo gets past.
    case "${ws,,}" in
    belong)
      die "Check 145 refused: 'belong' is the installation's own workspace and this check runs on sandboxes only. Nothing was sent anywhere"
      ;;
    esac
  done
  [ "$WORKSPACE" != "$SECOND_WORKSPACE" ] \
    || die "Check 145 needs two DIFFERENT workspaces: U3 is measured by reading the second one you belong to, and one name for both measures nothing"
}

# --------------------------------------------------------------- the requests
# request OUT METHOD URL [curl args...] — one request, its body in OUT, its
# status on stdout. Everything curl says goes to the scratch log: its messages
# can quote a request, and this script's requests carry a credential. A request
# that never got an answer is 000, and the block quotes those messages only
# through redact_secrets and one bounded line.
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
# (status 000). Naming only the status would leave the owner without the one
# thing that explains it -- a name that does not resolve, a TLS failure, a
# proxy that is set -- and the messages that say so carry no credential, since
# the credential is in the config file and never on the command line.
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

read_consumer() {
  local rc=0
  reader read "$CONSUMER_FILE" "$CONSUMER_CONF" >"$SCRATCH/reader.out" 2>"$SCRATCH/reader.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "the consumer file $CONSUMER_FILE could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null) (the file holds the read consumer's key and secret as JSON, the same mapping the bundle's bitbucket-read-consumer entry holds). Nothing was sent anywhere, and no value from the file is printed"
  fi
  local facts mode ws note=""
  facts=$(cat "$SCRATCH/reader.out")
  mode=$(field_of mode "$facts")
  ws=$(field_of workspace "$facts")
  if [ -n "$ws" ] && [ "$ws" != "$WORKSPACE" ]; then
    stop "the consumer file names the workspace '${ws}' and --workspace names '${WORKSPACE}': the two must agree, because a consumer is read as the workspace it was created in, and this check will not choose between them. Correct one of the two and run again"
  fi
  case "$mode" in
  600 | 400) ;;
  *) note=" — this file is readable by other users of this machine: chmod 600 it" ;;
  esac
  fact consumer-file PASS "read from $CONSUMER_FILE (mode ${mode:-?})${note}; the key and the secret went straight into a 0600 file inside the run's scratch directory, so they are never in this shell's memory, on a command line or in this block"
}

# mint N: exchanges the consumer for one access token, and writes everything it
# got into files. Prints nothing: the caller reads the reader's vocabulary.
mint() {
  local n=$1
  local body="$SCRATCH/answer-$n.json" status rc=0
  status=$(request "$body" POST "$OAUTH_ROOT/site/oauth2/access_token" --config "$CONSUMER_CONF")
  if [ "$status" != 200 ]; then
    local code note
    code=$(reader error "$body" 2>/dev/null || true)
    note=$(curl_note)
    stop "the client_credentials exchange was refused: HTTP $status${code:+ (${code#error=})}${note}. Nothing about the consumer can be measured with no token. Check that the consumer file holds the key and secret of the READ consumer created in ${WORKSPACE}, then run again. No value from the answer is printed: an endpoint that echoes back what it was given cannot reach this block"
  fi
  reader answer "$body" "$SCRATCH/token-$n" >"$SCRATCH/reader.out" 2>"$SCRATCH/reader.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "the exchange answered HTTP $status but its answer could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). The answer is in the run's scratch directory and is not printed"
  fi
  reader tokconf "$SCRATCH/token-$n" "$SCRATCH/token-$n.conf" >/dev/null 2>&1 \
    || stop "the minted token could not be written into a config file for the requests that carry it"
  ANSWER_FACTS=$(cat "$SCRATCH/reader.out")
}

mint_and_report_first() {
  mint 1
  local expires scopes
  expires=$(field_of expires_in "$ANSWER_FACTS")
  scopes=$(field_of scopes "$ANSWER_FACTS")
  if [ -n "$expires" ] && [ "$expires" != "-" ]; then
    local minutes=$((expires / 60))
    fact U1 PASS "the grant gave the token a life of ${expires}s (${minutes} minutes). The swagger page says one hour and the support page two; this endpoint said ${expires}s"
  else
    fact U1 FAIL "the grant's answer carried no expires_in, so the token's lifetime was not read from it and U1 stays UNVERIFIED"
  fi
  case "$scopes" in
  none) fact U11 INFO "the token answer lists no scopes, so the service cannot read what a consumer is scoped to and the minimum-scope rule (G7) rests on Check 147 alone — the plan says so where that reading is written" ;;
  unrecognised) fact U11 PASS "the token answer carries a scopes field, so the endpoint does list the granted scopes — but its value is not a list of scope names this check knows, so its value is not printed here (U11 is answered by the field's presence)" ;;
  "") fact U11 INFO "the token answer could not be read for its scopes, so U11 stays UNVERIFIED" ;;
  *) fact U11 PASS "the token answer lists the granted scopes: ${scopes}" ;;
  esac
  REFRESH=$(field_of refresh_token "$ANSWER_FACTS")
}

# --------------------------------------------------------------- the steps
step_workspaces() {
  fact workspaces INFO "the check ran against the sandboxes '${WORKSPACE}' and '${SECOND_WORKSPACE}', both named on the command line; 'belong' is refused by this check, so neither is the installation's own workspace"
}

# U3: does the token read the second workspace's repositories? Both reads are
# made, so a refusal on both is told apart from a refusal on one.
step_reach() {
  local own second own_note
  own=$(request "$SCRATCH/repos-own.json" GET "$API_ROOT/repositories/${WORKSPACE}" --config "$SCRATCH/token-1.conf")
  second=$(request "$SCRATCH/repos-second.json" GET "$API_ROOT/repositories/${SECOND_WORKSPACE}" --config "$SCRATCH/token-1.conf")
  own_note=""
  case "$own" in
  000) own_note=$(curl_note) ;;
  esac
  if [ "$own" = 000 ] || [ "$second" = 000 ]; then
    fact U3 INFO "the reach was NOT measured: the read of '${WORKSPACE}' answered ${own} and the read of '${SECOND_WORKSPACE}' answered ${second}${own_note}; a request that never got an answer says nothing about what the token reaches. U3 stays UNVERIFIED"
    return 0
  fi
  if [ "$second" = 200 ]; then
    local size
    size=$(field_of size "$(reader size "$SCRATCH/repos-second.json" 2>/dev/null || true)")
    fact U3 PASS "the token reaches PAST its own workspace: '${WORKSPACE}' answered ${own} and '${SECOND_WORKSPACE}' answered ${second}, so a client_credentials token reads every workspace its owner can reach${size:+ (${size} repositories visible there)}. Global Constraint: D360 and D361 go back to the owner before the read mint merges (O4)"
  else
    fact U3 PASS "the token does not reach past its own workspace: '${WORKSPACE}' answered ${own} and the second workspace '${SECOND_WORKSPACE}' answered ${second}, so the read is bounded by the workspace the consumer was made in"
  fi
}

# U4: whether this sandbox's own plan admits a consumer. The grant that just
# succeeded is the measurement; the workspace object is read beside it, because
# it is the only place a plan could show up.
step_plan() {
  local status fields
  status=$(request "$SCRATCH/workspace.json" GET "$API_ROOT/workspaces/${WORKSPACE}" --config "$SCRATCH/token-1.conf")
  fields=$(reader workspace "$SCRATCH/workspace.json" 2>/dev/null || true)
  if [ "$status" = 200 ]; then
    fact U4 PASS "the client_credentials grant succeeded against '${WORKSPACE}' and its workspace object was read (HTTP 200), so this workspace's own plan admits an OAuth consumer${fields:+ (${fields})}; no plan or billing field is visible to this token, so what the plan is called is not measured here"
  else
    fact U4 PASS "the client_credentials grant succeeded against '${WORKSPACE}', so this workspace's own plan admits an OAuth consumer; the workspace object itself answered ${status}, which limits nothing here"
  fi
}

seats_now() { # seats_now FILE — the member count, or the status that stopped it
  local status
  status=$(request "$1" GET "$API_ROOT/workspaces/${WORKSPACE}/members" --config "$SCRATCH/token-1.conf")
  if [ "$status" != 200 ]; then
    printf 'status:%s' "$status"
    return 0
  fi
  local got
  got=$(field_of size "$(reader size "$1" 2>/dev/null || true)")
  printf '%s' "${got:-unknown}"
}

# U5: the seat count before and after a second grant. The consumer was created
# before this check ran, so whether creating one consumes a seat is not
# measurable from here, and the row says that rather than implying it.
step_seats_before() {
  SEATS_BEFORE=$(seats_now "$SCRATCH/members-before.json")
}

step_seats_after() {
  local after before=$SEATS_BEFORE
  after=$(seats_now "$SCRATCH/members-after.json")
  case "$before" in
  status:* | unknown)
    fact U5 INFO "the seat count was NOT read: the members listing answered ${before#status:} before the second grant, so U5 stays UNVERIFIED. The consumer itself was created before this check ran, so whether it or a token uses a seat is not measurable here either"
    return 0
    ;;
  esac
  if [ "$after" = "$before" ]; then
    fact U5 PASS "the workspace's member count reads ${before} before the second grant and ${after} after it, so the two grants did not change it; the consumer was created before this check ran, so whether CREATING one uses a paid seat is not measurable here"
  else
    fact U5 PASS "the workspace's member count read ${before} before the second grant and ${after} after it — that is a change this check did not make, and it is worth reading: the consumer was created before this check ran, so whether creating it uses a paid seat is not measurable here"
  fi
}

# U8: whether IP allowlisting applies to OAuth tokens. What this check can see
# is whether the workspace's plan exposes an allowlist at all; whether a request
# from an address that is NOT allowlisted is refused needs a request from such
# an address, which this machine cannot make.
step_allowlist() {
  local status
  status=$(request "$SCRATCH/allowlist.json" GET "$API_ROOT/workspaces/${WORKSPACE}/ip-allowlist" --config "$SCRATCH/token-1.conf")
  case "$status" in
  200)
    fact U8 INFO "the plan exposes an IP allowlist (the allowlist endpoint answered 200) and this run's requests from this machine were admitted; whether a request from an address that is NOT allowlisted is refused cannot be measured from here, so U8 stays UNVERIFIED"
    ;;
  403 | 404)
    fact U8 INFO "the sandbox's plan offers no IP allowlist this token can read (the allowlist endpoint answered ${status}), so U8 stays UNVERIFIED and SECURITY.md says so for the read token (T2.11)"
    ;;
  *)
    fact U8 INFO "the allowlist endpoint answered ${status}, which is neither an allowlist nor the absence of one, so U8 stays UNVERIFIED$(curl_note)"
    ;;
  esac
}

# U14: what the account this token acts as may do, and what the token itself
# reaches. The two halves are different questions and the row keeps them apart.
step_admin() {
  local status perm members allowlist
  status=$(request "$SCRATCH/permissions.json" GET "$API_ROOT/user/permissions/workspaces" --config "$SCRATCH/token-1.conf")
  perm=""
  if [ "$status" = 200 ]; then
    perm=$(field_of permission "$(reader permission "$SCRATCH/permissions.json" "$WORKSPACE" 2>/dev/null || true)")
  fi
  members=$(request /dev/null GET "$API_ROOT/workspaces/${WORKSPACE}/members" --config "$SCRATCH/token-1.conf")
  allowlist=$(request /dev/null GET "$API_ROOT/workspaces/${WORKSPACE}/ip-allowlist" --config "$SCRATCH/token-1.conf")
  local who="the account this token acts as"
  if [ -n "$perm" ] && [ "$perm" != "not-listed" ] && [ "$perm" != "unrecognised" ]; then
    who="the account this token acts as holds '${perm}' in '${WORKSPACE}' (from /user/permissions/workspaces)"
  elif [ "$status" = 200 ]; then
    who="the permission list of the account this token acts as names no permission for '${WORKSPACE}'"
  else
    who="the account this token acts as could not be read (permissions answered ${status})"
  fi
  if [ "$status" = 200 ] && [ -n "$perm" ] && [ "$perm" != "not-listed" ] && [ "$perm" != "unrecognised" ]; then
    fact U14 PASS "${who}; the admin-only members listing answered ${members} and the allowlist endpoint answered ${allowlist}, which is what this token reaches beyond its read scopes. Whether CREATING a consumer needs workspace-admin rights cannot be measured from a consumer that already exists, so this check does not claim it: the runbook's step (T2.6) records what the owner's account could do"
  else
    fact U14 INFO "${who}, so U14 stays UNVERIFIED for this run; the admin-only members listing answered ${members} and the allowlist endpoint answered ${allowlist}"
  fi
}

# U12, and then U2's second half: two grants, and whether a minted token can be
# revoked. The token revoked is the SECOND one, so the first one survives for
# the steps above.
step_second_grant() {
  mint 2
  local verdict
  verdict=$(reader distinct "$SCRATCH/token-1" "$SCRATCH/token-2" 2>/dev/null || true)
  case "$verdict" in
  distinct) fact U12 PASS "two grants returned DISTINCT tokens (neither value is printed), which is what the audit line every mint writes depends on" ;;
  identical) fact U12 INFO "two grants returned the SAME token, which bears on attribution (F29) and on the audit line: a mint that names a person and a repository cannot be told apart from the one before it. RECORD THIS FOR THE OWNER it is an answer the plan has to read, not a defect of this run" ;;
  *) fact U12 INFO "whether the two grants returned distinct tokens could not be read, so U12 stays UNVERIFIED" ;;
  esac
}

step_revoke() {
  local status after revoked_note
  status=$(request "$SCRATCH/revoke.json" POST "$OAUTH_ROOT/site/oauth2/revoke" --config "$REVOKE_CONF")
  local refresh_note
  case "$REFRESH" in
  present) refresh_note="the grant carried a refresh token (its value is not read, kept or printed, and it is never forwarded)" ;;
  absent) refresh_note="the grant carried no refresh token" ;;
  *) refresh_note="whether the grant carries a refresh token could not be read" ;;
  esac
  if [ "$status" = 200 ]; then
    after=$(request /dev/null GET "$API_ROOT/repositories/${WORKSPACE}" --config "$SCRATCH/token-2.conf")
    case "$after" in
    401 | 403) revoked_note="the minted token was revoked at the revoke endpoint (HTTP 200) and the API then REFUSED that same token (HTTP ${after}), so a single minted token can be revoked" ;;
    *) revoked_note="the revoke endpoint answered 200, but the API still answered ${after} with that token, so whether it was really revoked is not settled" ;;
    esac
    fact U2 PASS "${refresh_note}; ${revoked_note}"
  else
    fact U2 INFO "${refresh_note}; the revoke endpoint answered ${status}${status:+$(curl_note)}, so whether a single minted token can be revoked was NOT measured (U2 stays UNVERIFIED). The consumer itself is what an operator can revoke: deleting it or regenerating its secret in the workspace's settings"
  fi
}

# The read-only guard the read mint is written to: a write with the read token
# must be refused. The probe is a create with NO name and no body that could
# make anything: a workspace repository cannot be created without a slug, so a
# token that may write answers 400 and one that may not answers 403. Nothing is
# created either way, and this check never names a repository.
step_write_refused() {
  local status code
  status=$(request "$SCRATCH/write.json" POST "$API_ROOT/repositories/${WORKSPACE}" \
    --config "$SCRATCH/token-1.conf" -H 'Content-Type: application/json' --data '{}')
  code=$(field_of error "$(reader error "$SCRATCH/write.json" 2>/dev/null || true)")
  case "$status" in
  401 | 403)
    fact write-refused PASS "a write that would create a repository in '${WORKSPACE}' was REFUSED (HTTP ${status}${code:+, ${code#error=}}), so the read token cannot write and the read-only guard the read mint is written to holds for this consumer"
    ;;
  2*)
    fact write-refused FAIL "a write that would create a repository in '${WORKSPACE}' was ANSWERED ${status}: a WRITE WITH THE READ TOKEN WAS NOT REFUSED. Check what it created in '${WORKSPACE}' by hand — this check sent no name it could have used, so nothing should be there — and do not merge the read mint on this consumer"
    ;;
  *)
    # The probe did not settle: the answer is neither the refusal the read mint
    # depends on nor a completed write. A row that read as a pass here would
    # vouch for a guard nobody measured, so it FAILs and says UNVERIFIED, the
    # same convention as a row the run never reached.
    fact write-refused FAIL "the write probe was answered ${status}${code:+, ${code#error=}}${status:+$(curl_note)}, which is neither a refusal (401 or 403) nor a completed write: whether the read token can write stays UNVERIFIED, so the read-only guard the read mint is written to is NOT vouched for by this run. Check by hand whether this token can write in '${WORKSPACE}', or run the check again, before the read mint merges"
    ;;
  esac
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(consumer-file workspaces xtrace U1 U2 U3 U4 U5 U8 U11 U12 U14 U15
  write-refused restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 145 RESULT\n' >&3
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
  printf 'CHECK 145 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
USAGE="check145.sh --consumer-file <path> --workspace <sandbox> --second-workspace <sandbox-2>"

parse_args() {
  CONSUMER_FILE=""
  WORKSPACE=""
  SECOND_WORKSPACE=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --consumer-file)
      [ $# -ge 2 ] || die "--consumer-file needs a path. usage: $USAGE"
      CONSUMER_FILE=$2
      shift 2
      ;;
    --workspace)
      [ $# -ge 2 ] || die "--workspace needs a workspace. usage: $USAGE"
      WORKSPACE=$2
      shift 2
      ;;
    --second-workspace)
      [ $# -ge 2 ] || die "--second-workspace needs a workspace. usage: $USAGE"
      SECOND_WORKSPACE=$2
      shift 2
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$CONSUMER_FILE" ] || die "--consumer-file is required. usage: $USAGE"
  [ -n "$WORKSPACE" ] || die "--workspace is required. usage: $USAGE"
  [ -n "$SECOND_WORKSPACE" ] || die "--second-workspace is required (U3 needs a second sandbox you belong to). usage: $USAGE"
}

main() {
  # FIRST, before anything below can use one: the reader is a python3 program,
  # and `need python3` is the only place that says so.
  need_tools
  parse_args "$@"
  guard_endpoints
  guard_workspaces

  # Bitbucket Cloud's own endpoints, and nothing else: allowlisting is off, so
  # a redirect is followed by curl and could carry the basic credential to
  # another host -- so a redirect is REFUSED rather than followed.
  OAUTH_ROOT=${CHECK145_OAUTH_URL:-https://bitbucket.org}
  OAUTH_ROOT=${OAUTH_ROOT%/}
  API_ROOT=${CHECK145_API_URL:-https://api.bitbucket.org/2.0}
  API_ROOT=${API_ROOT%/}
  CURL_SECONDS=${CHECK145_CURL_SECONDS:-60}

  scratch_up
  CONSUMER_CONF="$SCRATCH/consumer.conf"
  REVOKE_CONF="$SCRATCH/revoke.conf"
  CREDENTIAL_FILES=("$CONSUMER_CONF" "$REVOKE_CONF" "$SCRATCH/token-1" "$SCRATCH/token-2" \
    "$SCRATCH/token-1.conf" "$SCRATCH/token-2.conf")
  write_files
  guard_xtrace
  read_consumer
  step_workspaces

  mint_and_report_first
  step_reach
  step_plan
  step_seats_before
  step_admin
  step_allowlist
  step_write_refused
  step_second_grant
  step_seats_after
  # The revoke config is built here, from the second token: it carries the token
  # in a body and the consumer's own credentials, so it is a file like the rest.
  reader revokeconf "$CONSUMER_FILE" "$SCRATCH/token-2" "$REVOKE_CONF" >/dev/null 2>&1 \
    || stop "the revoke request could not be prepared: the config file it carries the token in could not be written"
  step_revoke

  fact U15 INFO "the grant from the consumer you named succeeded, so THIS consumer works; whether the client_credentials grant needs the consumer marked 'private' is not readable from the API — a consumer's visibility lives in the workspace's settings. To settle U15, run this check once more with a consumer that is NOT private: the grant is the answer, and nothing else about this run changes"

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
"""Check 145's reader: the one place a credential is handled.

The shell never holds the consumer's key or secret, a minted token or a refresh
token. This program writes them straight into the 0600 files it is told to write
them into, and the only text it prints is a fixed vocabulary -- a scope word, a
workspace permission, an OAuth error code, a count, a duration, or a one-word
answer like present, absent, distinct or identical. Nothing from a response is
printed as it arrived, so an endpoint that echoes back what it was given cannot
reach the check's result block through this.
"""
import json, os, re, sys

# The scopes Bitbucket documents for a consumer, so a value that is not one of
# them is refused a place in the block rather than copied into it.
SCOPES = {
    "account", "account:read", "account:write",
    "email", "email:read", "email:write",
    "issue", "issue:read", "issue:write",
    "pipeline", "pipeline:read", "pipeline:write",
    "project", "project:read", "project:write",
    "pullrequest", "pullrequest:read", "pullrequest:write",
    "repository", "repository:read", "repository:write", "repository:admin", "repository:delete",
    "snippet", "snippet:read", "snippet:write",
    "team", "team:read", "team:write",
    "webhook", "wiki", "wiki:read", "wiki:write",
}
# The workspace permissions Bitbucket reports for a member.
PERMISSIONS = {"admin", "create-repo", "write", "read", "none"}
# The OAuth and API error codes this check knows. A code outside the set is
# dropped rather than printed: an error body is written by the endpoint, and an
# endpoint that echoes what it was given must not have a field of its choosing
# reach the block.
ERROR_CODES = {
    "access_denied", "invalid_client", "invalid_grant", "invalid_request",
    "invalid_scope", "invalid_token", "insufficient_scope", "not_found",
    "server_error", "unauthorized", "unauthorized_client", "unsupported_grant_type",
    "forbidden", "bad_request", "conflict", "method_not_allowed",
}
# What a credential may look like to be written into a curl config file: one
# line of printable ASCII with no space. A value that is anything else is
# refused, and its own text is never printed -- the message names the FIELD.
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


def write_private(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, text.encode("utf-8"))
    finally:
        os.close(fd)
    os.chmod(path, 0o600)


def escaped(value):
    return value.replace("\\", "\\\\").replace('"', '\\"')


def consumer_mapping(doc):
    """The key, the secret and the optional workspace, from either spelling of
    the file: the mapping itself, or the bundle document that holds it under
    bitbucket-read-consumer."""
    if not isinstance(doc, dict):
        die("the consumer file is not a JSON object")
    if "bitbucket-read-consumer" in doc:
        doc = doc["bitbucket-read-consumer"]
        if not isinstance(doc, dict):
            die("the consumer file's bitbucket-read-consumer entry is not a JSON object")
    unknown = sorted(set(doc) - {"key", "secret", "workspace"})
    if unknown:
        die("the consumer file carries field(s) this check does not read: %s" % ", ".join(unknown))
    for field in ("key", "secret", "workspace"):
        if field in doc and not isinstance(doc[field], str):
            die("the consumer file's %s is not a string" % field)
    return doc


def value_of(doc, field):
    raw = doc.get(field, "")
    if not raw:
        die("the consumer file carries no %s" % field)
    if not VALUE.fullmatch(raw):
        die("the consumer file's %s is not one line of printable text without spaces" % field)
    return raw


def cmd_read(path, conf):
    doc = consumer_mapping(load(path, "the consumer file"))
    key = value_of(doc, "key")
    secret = value_of(doc, "secret")
    write_private(conf, 'user = "%s:%s"\nheader = "Accept: application/json"\ndata = "grant_type=client_credentials"\n'
                  % (escaped(key), escaped(secret)))
    print("mode=%o" % (os.stat(path).st_mode & 0o777))
    workspace = doc.get("workspace", "")
    if workspace:
        if not VALUE.fullmatch(workspace):
            die("the consumer file's workspace is not one line of printable text without spaces")
        print("workspace=%s" % workspace)


def cmd_answer(path, tokenout):
    doc = load(path, "the token answer")
    if not isinstance(doc, dict):
        die("the token answer is not a JSON object")
    token = doc.get("access_token")
    if not isinstance(token, str) or not token.strip():
        die("the token answer carried no access token")
    token = token.strip()
    if not VALUE.fullmatch(token):
        die("the token answer's access token is not one line of printable text")
    write_private(tokenout, token)
    life = doc.get("expires_in")
    print("expires_in=%d" % life if isinstance(life, int) and not isinstance(life, bool) and 0 < life <= 604800
          else "expires_in=-")
    refresh = doc.get("refresh_token")
    print("refresh_token=present" if isinstance(refresh, str) and refresh.strip() else "refresh_token=absent")
    scopes = doc.get("scopes")
    if scopes is None:
        print("scopes=none")
    elif isinstance(scopes, str):
        words = [w for w in re.split(r"[,\s]+", scopes) if w]
        print("scopes=%s" % ",".join(sorted(set(words))) if words and all(w in SCOPES for w in words)
              else "scopes=unrecognised")
    else:
        print("scopes=unrecognised")


def cmd_distinct(first, second):
    try:
        with open(first, "r", encoding="utf-8") as fh:
            a = fh.read().strip()
        with open(second, "r", encoding="utf-8") as fh:
            b = fh.read().strip()
    except OSError:
        die("a minted token could not be read back for the comparison")
    if not a or not b:
        die("a minted token is empty, so the two cannot be compared")
    print("identical" if a == b else "distinct")


def cmd_size(path):
    doc = load(path, "the answer")
    size = doc.get("size") if isinstance(doc, dict) else None
    print("size=%d" % size if isinstance(size, int) and not isinstance(size, bool) and size >= 0 else "size=-")


def cmd_permission(path, workspace):
    doc = load(path, "the permissions answer")
    values = doc.get("values") if isinstance(doc, dict) else None
    if not isinstance(values, list):
        print("permission=unrecognised")
        return
    for entry in values:
        if not isinstance(entry, dict):
            continue
        space = entry.get("workspace")
        if not isinstance(space, dict) or space.get("slug") != workspace:
            continue
        permission = entry.get("permission")
        print("permission=%s" % permission if isinstance(permission, str) and permission in PERMISSIONS
              else "permission=unrecognised")
        return
    print("permission=not-listed")


def cmd_error(path):
    doc = load(path, "the answer")
    error = doc.get("error") if isinstance(doc, dict) else None
    code = error.get("code") if isinstance(error, dict) else error
    print("error=%s" % code if isinstance(code, str) and code in ERROR_CODES else "error=-")


def cmd_workspace(path):
    """One short line about the workspace object, from a fixed vocabulary: which
    of the fields this check knows were there, and nothing else."""
    doc = load(path, "the workspace answer")
    if not isinstance(doc, dict):
        print("")
        return
    notes = []
    if isinstance(doc.get("type"), str) and doc["type"] == "workspace":
        notes.append("type=workspace")
    if isinstance(doc.get("is_private"), bool):
        notes.append("is_private=%s" % ("yes" if doc["is_private"] else "no"))
    print(", ".join(notes))


def cmd_tokconf(tokenfile, conf):
    try:
        with open(tokenfile, "r", encoding="utf-8") as fh:
            token = fh.read().strip()
    except OSError:
        die("the minted token could not be read back")
    if not token or not VALUE.fullmatch(token):
        die("the minted token is not a value this check will write into a config file")
    write_private(conf, 'header = "Authorization: Bearer %s"\nheader = "Accept: application/json"\n'
                  % escaped(token))


def cmd_revokeconf(consumerfile, tokenfile, conf):
    doc = consumer_mapping(load(consumerfile, "the consumer file"))
    key = value_of(doc, "key")
    secret = value_of(doc, "secret")
    try:
        with open(tokenfile, "r", encoding="utf-8") as fh:
            token = fh.read().strip()
    except OSError:
        die("the minted token could not be read back")
    if not token or not VALUE.fullmatch(token):
        die("the minted token is not a value this check will write into a config file")
    write_private(conf, 'user = "%s:%s"\nheader = "Accept: application/json"\ndata = "token=%s"\n'
                  % (escaped(key), escaped(secret), escaped(token)))


def main():
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]
    table = {
        "read": (cmd_read, 2),
        "answer": (cmd_answer, 2),
        "distinct": (cmd_distinct, 2),
        "size": (cmd_size, 1),
        "permission": (cmd_permission, 2),
        "error": (cmd_error, 1),
        "workspace": (cmd_workspace, 1),
        "tokconf": (cmd_tokconf, 2),
        "revokeconf": (cmd_revokeconf, 3),
    }
    if command not in table or len(args) != table[command][1]:
        die("the reader was called with an argument list it does not know")
    table[command][0](*args)


if __name__ == "__main__":
    main()
READER_EOF
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
