#!/usr/bin/env bash
# Check 590 — the three Bitbucket token types, and what the verifier says for
# each — as ONE script YOU run.
#
# A repository access token, a project access token and a workspace access token
# are three different credentials that a run may be served by, and the release
# admits all three (internal/config's BitbucketTokenTypes). What only live
# tokens settle is what each one ANSWERS: whether Bitbucket's own
# `GET /2.0/user` answers for it at all and what its `kind` says there (U1),
# which scopes Bitbucket reports for it (U2), and whether a project token is
# accepted as `x-token-auth` for git over HTTPS and as `Bearer` for REST (U5).
# The design's own row for a repository token is a 403 at /user — "This API is
# not accessible by this authentication mechanism" — accepted as an access
# token with no identity at that endpoint (R14, O7, confirmed 2026-11-13); this
# check is what reads the same answer for the other two.
#
# It ALSO runs the release's OWN add path three times: `fugaro creds set
# bitbucket-token--<name> --type ... --coverage ...` (T11), whose check runs
# BEFORE anything is written (D2352) and which prints, under --json, the record
# it wrote: the type, the coverage, the identity the check read and whether the
# write scopes were proven (they are not, D2353). The block's three `verifier-*`
# rows are that reading; the U-rows are the raw calls, made by this script with
# each token, which is what settles the open questions rather than the
# verifier's own summary of them.
#
# U6 — whether a project token covers a repository moved into its project later
# — is NOT this check's: it is Check 180's (docs/bitbucket-workspace-token,
# its own register row), which moves a repository into the project as one of its
# steps. This check records no row for it and does not claim it.
#
# Usage:      bash scripts/live-checks/check590.sh \
#               --repo <workspace>/<sandbox repo> \
#               --token-file <path> \
#               --project-token-file <path> \
#               --workspace-token-file <path> \
#               (--project-uuid <uuid> | --project-key <key>) \
#               [--project <installation>] [--gcp-project <project>] \
#               [--workspace <slug>] [--dry-run]
#             --dry-run  prints the plan and touches nothing.
#             Each token file holds ONE token and nothing else — one line, no
#             quotes, no JSON — at mode 0600. They are the ONE place the values
#             live: this script never puts one on a command line, never prints
#             one, and hands it to `fugaro creds set` on stdin. The three 0600
#             curl configs and git configs this check writes inside its scratch
#             directory are the only files a value is ever written into, and they
#             are removed on the way out, including on a Ctrl-C.
#             --repo is the sandbox repository the REPOSITORY token was created
#             for (its coverage is that one repository); the project and
#             workspace tokens must reach it too, which is the premise the
#             scripts reads first.
#             --project-uuid is the Bitbucket project's UUID (braces included)
#             and --project-key its key; a project token's record needs one of
#             them, because a project token is matched by the UUID and the key is
#             display. Either may be given, never both.
# Needs:      bash, mktemp, python3 (the reader), curl, git, fugaro. It does NOT
#             need gcloud or a checkout: nothing here launches a run.
# Where:      the installation `fugaro` in the GCP project `fugaro-dev` and the
#             Bitbucket sandbox workspace `dimi_bmb` ONLY — all three are
#             hard-coded in this file and overridable by CHECK590_INSTALLATION,
#             CHECK590_GCP_PROJECT and CHECK590_WORKSPACE. It never reads
#             `gcloud config get-value project`, it refuses a doctor that names
#             another installation or another GCP project, and it refuses
#             `belong` by name — as the installation, as the workspace and as the
#             repository's workspace. Sandbox repositories only.
# Costs:      three `creds set` writes into the sandbox installation (three
#             bundle keys and three records), a handful of API calls and one
#             `fugaro update --repo`-scale republish is NOT made: the published
#             node is what a run reads, so nothing here is visible to a run until
#             you republish. No run is launched and no model call is made.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check590_test.sh) runs it against fakes and
#             never touches Bitbucket, gcloud or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: the scratch directory and every file
#             a credential was written into are removed, and the block's
#             `restore` row says whether they really went — a FAILed row there
#             names what to remove by hand. What a person has to undo is what
#             the check exists to make: the three bundle keys and the three
#             records the `store` rows name.
# Output:     progress on stderr, ONE block on stdout headed CHECK 590 RESULT,
#             to copy back to the coordinator. Its last line is the verdict, and
#             the verdict is the exit status:
#
#               CHECK 590 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 590 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 590 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything (a
#                                                      guard stopped it, a tool is
#                                                      missing, a command was
#                                                      refused, an error ended it);
#               CHECK 590 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one criterion was
#                                                      NOT settled: a row reading
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS: a check that measured most of its
#             criteria and left one unmeasured is INCOMPLETE, not a pass, and the
#             register's section is read against that word.
#
# NO SECRET IN ANY OUTPUT is a property of the design, not a promise kept by
# care. A value is read from its token file by the reader, which writes it into
# 0600 files: a curl config (`Authorization: Bearer …`) and a git config whose
# `http.extraHeader` carries `x-token-auth:<token>` base64-encoded, so the
# credential is never on a command line where `ps` would show it. What the
# reader prints is a fixed vocabulary: a count, a status word, a scope NAME out
# of the set the spec cares about, `present`/`absent`, `yes`/`no`, the access
# token `kind` when it is one of the four Bitbucket documents, and a repository's
# owner/name. Nothing from a response is printed as it arrived, so an endpoint
# that echoes back what it was given — which this check's own test uses as its
# ordinary fake — cannot reach the block. And no trace: `bash -x` prints every
# command as it runs, so guard_xtrace REFUSES a run whose shell arrived with
# tracing on.
#
# CHECK590_API_URL, CHECK590_CURL_SECONDS and CHECK590_DRY_RUN exist for this
# script's own test: the first may only name a loopback address of exactly the
# form http://127.0.0.1:<port> (the test's fake Bitbucket), so no environment can
# send a token anywhere but Bitbucket Cloud's own API; the other two shorten the
# cap and plan a run without touching anything.
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
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the tokens (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 590 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 590 needs bash"
  need mktemp
  need python3 "the reader that handles the tokens and parses the JSON is one python3 file"
  need curl
  need git
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk590-mine"
SCRATCH_REMOVED=no
CREDENTIALS_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
# Every file a credential was written into: the three curl configs, the three git
# configs and the logs a command's own output goes to (an error message can quote
# a URL, and a URL is not a credential — but the same files are where a command's
# own words land, so they go with the rest). Their removal is READ, not assumed:
# the `restore` row reports it.
CREDENTIAL_FILES=()
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk590.XXXXXXXX") || die "Check 590 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 590 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the tokens"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so no file holds a credential and there is nothing to remove"
  else
    [ "$CREDENTIALS_REMOVED" = yes ] && [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local cred_note="the three curl configs, the three git configs and the logs a token's value or a command's own words were written into are removed"
    [ "$CREDENTIALS_REMOVED" = yes ] || cred_note="A FILE HOLDING A CREDENTIAL IS STILL THERE — look in $SCRATCH yourself and remove it: it holds a token's value in a curl or git config"
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check590.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
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

guard_endpoints() {
  local value
  value=$(printenv CHECK590_API_URL 2>/dev/null || true)
  [ -n "$value" ] || return 0
  loopback_url "$value" \
    || die "Check 590 refused: CHECK590_API_URL may only name a loopback address of the form http://127.0.0.1:<port> (with no userinfo, query, fragment or space), so a token can never leave this machine (it is set for this check's own test and for nothing else)"
}

# guard_belong: `belong` is the installation and the workspace this check must
# never touch, and a script that can be pointed at it by a typo has no business
# handling the owner's own tokens.
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

guard_belong() {
  local what value
  for what in installation gcp-project workspace repo; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    gcp-project) value=$GCP_PROJECT ;;
    workspace) value=$WORKSPACE ;;
    repo) value=$REPO ;;
    esac
    [ -n "$value" ] || continue
    case "$(lower "$value")" in
    belong | belong/* | */belong)
      die "Check 590 refused: '$value' (the $what) names 'belong', the installation and workspace this check must never touch. Nothing was sent anywhere"
      ;;
    esac
  done
}

# --------------------------------------------------------------- the requests
# request OUT METHOD PATH [curl args...] — one API request, its body in OUT, its
# status on stdout. Everything curl says goes to the scratch log: its messages
# can quote a request. A request that never got an answer is 000.
request() {
  local out=$1 method=$2 path=$3
  shift 3
  local code=000
  code=$(curl -sS -o "$out" -w '%{http_code}' --max-time "$CURL_SECONDS" \
    --request "$method" "$@" "$API_ROOT$path" 2>>"$SCRATCH/curl.log") || code=000
  printf '%s' "$code"
}

# request_h is request with the response's HEADERS kept: the scope header (U2)
# is a header and never a body field, so the response headers are the only place
# it can be read, and the dump is a file this check removes with the rest.
request_h() { # request_h OUT HEADERS METHOD PATH [curl args...]
  local out=$1 headers=$2 method=$3 path=$4
  shift 4
  local code=000
  code=$(curl -sS -o "$out" -D "$headers" -w '%{http_code}' --max-time "$CURL_SECONDS" \
    --request "$method" "$@" "$API_ROOT$path" 2>>"$SCRATCH/curl.log") || code=000
  printf '%s' "$code"
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
"""Check 590's reader: the one place the tokens and the responses are handled.

The shell never holds a token. This program reads it out of the file the owner
named, writes it into the 0600 config files the requests and the git call carry
it in, and prints a fixed vocabulary -- counts, HTTP-adjacent words, the access
token `kind` out of the four Bitbucket documents, scope NAMES out of the set this
check cares about, yes/no, present/absent and one repository name -- so a
response that echoes back what it was given cannot reach the check's result block
through this.
"""
import base64, json, os, re, sys

# What a value may look like to be written into a config file: one line of
# printable ASCII with no space. Anything else is refused, and its own text is
# never printed -- the message names the FIELD.
VALUE = re.compile(r"[\x21-\x7e]+")
# A repository's owner/name, as this check is willing to print it.
REPO = re.compile(r"[A-Za-z0-9._-]+/[A-Za-z0-9._-]+")
# The four `kind` values Bitbucket documents for an access token, and nothing
# else: a body that names another one is a body this check does not read a kind
# out of, and its own text never reaches the block.
KINDS = {
    "repository_access_token", "project_access_token", "workspace_access_token",
    "access_token",
}
# The scopes this check reads out of the scope header. The two write scopes are
# the ones D2353 says are never proven; the read scopes are what a per-request
# selection needs. A scope outside this set is not printed.
SCOPES = (
    "repository", "repository:read", "repository:write", "repository:admin",
    "pullrequest", "pullrequest:read", "pullrequest:write", "project",
    "project:read", "project:write", "account", "account:read",
)
ERROR_CODES = {
    "access_denied", "bad_request", "conflict", "forbidden", "insufficient_scope",
    "invalid_request", "invalid_token", "method_not_allowed", "not_found",
    "server_error", "unauthorized",
}


def die(message):
    print(message, file=sys.stderr)
    sys.exit(1)


def load(path, what):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        die("%s is not readable JSON" % what)


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
    access token of any of the three types (U5's REST half)."""
    token = token_of(path)
    mode = os.stat(path).st_mode & 0o777
    write_private(conf, 'header = "Authorization: Bearer %s"\nheader = "Accept: application/json"\n'
                  % token.replace("\\", "\\\\").replace('"', '\\"'))
    print("mode=%o" % mode)
    print("bytes=%d" % len(token))


def cmd_gitconfig(path, conf):
    """The git config that authenticates as the username Bitbucket documents for
    an access token: `x-token-auth` with the token as the password (U5's git
    half), as an http.extraHeader so the value is in a FILE and never on a
    command line."""
    token = token_of(path)
    basic = base64.b64encode(("x-token-auth:%s" % token).encode("utf-8")).decode("ascii")
    write_private(conf, "[http]\n\textraHeader = Authorization: Basic %s\n" % basic)
    print("header=written")


def safe(value, limit=120):
    return value if isinstance(value, str) and VALUE.fullmatch(value) and len(value) <= limit else ""


def named(value):
    """Whether a body carries a NAME: the one question cmd_user asks of it. It is
    a presence test and not a value this program may print -- a display name has
    spaces in it, and it never reaches a line of output -- so it allows what
    `safe` refuses and still bounds what it accepts."""
    return isinstance(value, str) and 0 < len(value) <= 120 and all("\x20" <= c <= "\x7e" for c in value)


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


def cmd_user(path):
    """U1's own reading: what `GET /2.0/user` answers for one token. A 200 is
    read for its `kind` (only a value of Bitbucket's own four is printed) and for
    whether it carries an identity; a body that is an error is read for its code
    and nothing else."""
    doc = load(path, "GET /2.0/user")
    if not isinstance(doc, dict):
        print("kind=none")
        print("identity=no-account")
        print("error=-")
        return
    kind = doc.get("kind")
    print("kind=%s" % (kind if kind in KINDS else "none"))
    has_name = bool(named(doc.get("display_name")) or named(doc.get("username")) or named(doc.get("nickname")))
    print("identity=%s" % ("account" if has_name else "no-account"))
    error = doc.get("error")
    code = error.get("code") if isinstance(error, dict) else error
    print("error=%s" % (code if isinstance(code, str) and code in ERROR_CODES else "-"))


def cmd_scopes(path):
    """U2: the scope header, whichever of the two spellings Bitbucket used. The
    header's VALUE is not printed -- only the scope names out of this check's own
    set, a count, and whether the two write scopes are in it."""
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            raw = fh.read()
    except OSError:
        die("the response headers could not be read")
    header = ""
    for line in raw.splitlines():
        name, _, value = line.partition(":")
        if name.strip().lower() in ("x-oauth-scopes", "x-accepted-oauth-scopes"):
            header = value.strip()
            break
    found = [s for s in SCOPES if s in [p.strip() for p in header.split(",")]]
    print("scope-header=%s" % ("present" if header else "absent"))
    print("scopes=%s" % (",".join(found) if found else "-"))
    print("write-scopes=%s" % ("yes" if "repository:write" in found and "pullrequest:write" in found
                               else ("partial" if "repository:write" in found or "pullrequest:write" in found else "no")))
    print("read-scopes=%s" % ("yes" if "repository" in found or "repository:read" in found
                              or "project" in found or "project:read" in found else "no"))


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
    print("error=%s" % (code if isinstance(code, str) and code in ERROR_CODES else "-"))


def cmd_setjson(path):
    """`fugaro creds set --json`'s own document, read for the four facts the
    verifier's rows are: which record was written, the identity the check read
    (absent for a repository token, R14), and whether the write scopes were
    proven (never, D2353). The value is not in this document at all -- the shape
    has no field a credential's value could be written into (credsSetResult)."""
    doc = load(path, "fugaro creds set --json")
    bit = doc.get("bitbucket") if isinstance(doc, dict) else None
    if not isinstance(bit, dict):
        print("name=")
        print("type=")
        print("coverage=0")
        print("identity=absent")
        print("write-verified=no")
        return
    print("name=%s" % safe(bit.get("name")))
    print("type=%s" % safe(bit.get("type")))
    coverage = bit.get("coverage")
    print("coverage=%d" % len(coverage) if isinstance(coverage, list) else "coverage=0")
    print("identity=%s" % ("present" if safe(bit.get("identity")) else "absent"))
    print("write-verified=%s" % ("yes" if bit.get("write_verified") is True else "no"))


def main():
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]
    table = {
        "tokenfile": (cmd_tokenfile, 2),
        "gitconfig": (cmd_gitconfig, 2),
        "doctor": (cmd_doctor, 1),
        "user": (cmd_user, 1),
        "scopes": (cmd_scopes, 1),
        "repo": (cmd_repo, 1),
        "setjson": (cmd_setjson, 1),
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
  REGION=$(field_of region "$facts")
  if [ "$status" != ok ]; then
    stop "fugaro doctor --project ${INSTALLATION} failed (exit ${rc}): $(last_line "$SCRATCH/doctor.err"). This check reads the installation's own project before it touches anything, and it needs the installation named on this machine with usable credentials"
  fi
  if [ "$(lower "${PROJECT_NAME:-}")" != "$(lower "$INSTALLATION")" ]; then
    stop "the local config selected by --project ${INSTALLATION} is the installation '${PROJECT_NAME:-none}': this check runs on the installation you named and no other, so it stops rather than writing three tokens into a project you did not mean"
  fi
  # The GCP project is hard-coded (never `gcloud config get-value project`): a
  # check that took the project from the environment could be pointed at another
  # installation's project by a stray variable, and the three writes below go
  # into this project's secret containers.
  if [ "$(lower "${GCP_PROJECT_SEEN:-}")" != "$(lower "$GCP_PROJECT")" ]; then
    stop "the installation '${PROJECT_NAME}' lives in GCP project '${GCP_PROJECT_SEEN:-none}' and this check runs on the sandbox installation in '${GCP_PROJECT}' only: 'belong' and every other installation are refused by name. Set CHECK590_GCP_PROJECT (and CHECK590_INSTALLATION) if you really mean another one, or point --project at the sandbox installation"
  fi
  fact installation PASS "installation '${PROJECT_NAME}' in GCP project ${GCP_PROJECT_SEEN}, region ${REGION:-unset}, read from fugaro doctor --project ${INSTALLATION} at $(date -u +%Y-%m-%dT%H:%M:%SZ). The workspace this check's tokens must belong to is ${WORKSPACE}, and it refuses 'belong' as an installation, as the workspace and as the repository's workspace"
}

# One token's own files: its mode, its size, and the two 0600 configs the calls
# below carry it in. Nothing here reads the value into the shell.
read_token_files() { # read_token_files LABEL FILE
  local label=$1 file=$2 facts
  facts=$(reader tokenfile "$file" "$SCRATCH/$label.conf" 2>"$SCRATCH/reader.err" || true)
  [ -n "$facts" ] || stop "the ${label} token file $file could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). It holds one access token, one line and nothing else, and no value from it is printed. Nothing was sent anywhere"
  eval "MODE_${label}=\$(field_of mode \"\$facts\")"
  eval "BYTES_${label}=\$(field_of bytes \"\$facts\")"
  local mode
  eval "mode=\$MODE_${label}"
  case "$mode" in
  600) ;;
  *) stop "the ${label} token file $file is mode ${mode:-?} and this check wants 0600: a token any other user of this machine can read is not one to store in the installation's bundle. Type 'chmod 600 $file' and run again. Nothing was sent anywhere" ;;
  esac
  reader gitconfig "$file" "$SCRATCH/$label.gitconfig" >/dev/null 2>"$SCRATCH/reader.err" \
    || stop "the git config for the ${label} token could not be written from $file: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"
}

step_inputs() {
  read_token_files repository "$TOKEN_FILE"
  read_token_files project "$PROJECT_TOKEN_FILE"
  read_token_files workspace "$WORKSPACE_TOKEN_FILE"
  # The coverage premise: every one of the three tokens must reach the repository
  # this check was pointed at, whichever of the three types it is. It is read
  # with each token's own Bearer config, and a token that cannot read the
  # repository is not one whose /user answer means anything here.
  local status_r status_p status_w code_note=""
  status_r=$(request "$SCRATCH/repo-repository.json" GET "/repositories/${REPO}" --config "$SCRATCH/repository.conf")
  status_p=$(request "$SCRATCH/repo-project.json" GET "/repositories/${REPO}" --config "$SCRATCH/project.conf")
  status_w=$(request "$SCRATCH/repo-workspace.json" GET "/repositories/${REPO}" --config "$SCRATCH/workspace.conf")
  REPO_PROJECT=$(field_of project "$(reader repo "$SCRATCH/repo-repository.json" 2>/dev/null || true)")
  local bad=""
  [ "$status_r" = 200 ] || bad="${bad}the repository token answered ${status_r} for ${REPO}; "
  [ "$status_p" = 200 ] || bad="${bad}the project token answered ${status_p} for ${REPO}; "
  [ "$status_w" = 200 ] || bad="${bad}the workspace token answered ${status_w} for ${REPO}; "
  if [ -n "$bad" ]; then
    code_note=$(field_of error "$(reader repo "$SCRATCH/repo-project.json" 2>/dev/null || true)")
    stop "${bad}every one of the three tokens has to read ${REPO} before its own answer means anything (the project token's own error code was ${code_note:-none}). All three are sandbox tokens of the workspace ${WORKSPACE}, and the repository is a sandbox repository of the project they were created in; check the names, the scopes and that each token file holds the token you think it does, then run again. No value from an answer is printed$(curl_note)"
  fi
  fact inputs PASS "the three token files are mode 0600 (${BYTES_repository}, ${BYTES_project} and ${BYTES_workspace} bytes) and all three read ${REPO} over REST with their own Bearer config (HTTP ${status_r}, ${status_p} and ${status_w}), so each is a token of the workspace ${WORKSPACE} that reaches this repository. They were read into 0600 curl and git configs inside the scratch directory, so no value is on a command line or in this shell. The repository's Bitbucket project reads as '${REPO_PROJECT:-unread}'"
}

# U1 and U2 for ONE token: what Bitbucket answers about the token itself, and
# which scopes it reports. The two are separate rows per type because they are
# separate questions, and the type is in the row's own name.
step_type_probe() { # step_type_probe TYPE
  local type=$1 status
  status=$(request_h "$SCRATCH/user-$type.json" "$SCRATCH/user-$type.headers" GET "/user" --config "$SCRATCH/$type.conf")
  local facts kind identity error scopes scope_header write_scopes read_scopes scope_note
  facts=$(reader user "$SCRATCH/user-$type.json" 2>/dev/null || true)
  kind=$(field_of kind "$facts")
  identity=$(field_of identity "$facts")
  error=$(field_of error "$facts")
  case "$status:$kind:$identity" in
  # The reader prints `identity=account` or `identity=no-account` and nothing
  # else, so this arm has to come before the two that wildcard the identity: a
  # 200 whose body names no kind this check knows is UNVERIFIED whatever the
  # identity says, and reading it as the PASS above would answer U1 with a kind
  # the check does not know.
  200:none:*)
    fact "U1-$type" UNVERIFIED "the ${type} token answers GET /2.0/user with HTTP 200 but the body names no kind this check knows (Bitbucket's four access-token kinds are repository_access_token, project_access_token, workspace_access_token and access_token), so what a ${type} token reports there is NOT settled by this run (U1 for the ${type} token stays UNVERIFIED: the body's own text is never printed here)"
    ;;
  200:*:account)
    fact "U1-$type" PASS "the ${type} token answers GET /2.0/user with HTTP 200, an identity and kind '${kind}', so this type HAS an identity at that endpoint and Bitbucket names its own type there (U1 for the ${type} token). The name the body carried, and the identity the verifier's own row below records, are never printed here"
    ;;
  200:*:no-account)
    fact "U1-$type" FAIL "the ${type} token answers GET /2.0/user with HTTP 200 and kind '${kind}' but no name this check can read, so the identity it answers with could not be read: an identity the add-time check would record as none while Bitbucket returns one is a record this check cannot vouch for (U1 for the ${type} token)"
    ;;
  403:*:*)
    # The design's own answer for a REPOSITORY token (R14, O7): a 403 at /user is
    # the endpoint refusing an authentication mechanism it does not accept, which
    # is what an access token with no identity there gets. For the other two
    # types a 403 is the interesting answer rather than the expected one, and it
    # is recorded as the fact rather than as a failure: it settles U1 either way.
    fact "U1-$type" PASS "the ${type} token is REFUSED at GET /2.0/user with HTTP 403${error:+ (Bitbucket code ${error})}, so this type has NO identity at that endpoint and the add-time check records none for it. For a repository token this is the expected answer (R14, O7: 'not accessible by this authentication mechanism', accepted as an access token with no identity); for a project or workspace token it is a fact about Bitbucket rather than a failure, and the verifier's own row below says what it recorded"
    ;;
  401:*:* | 404:*:*)
    fact "U1-$type" FAIL "the ${type} token answered GET /2.0/user with HTTP ${status}${error:+ (Bitbucket code ${error})}: a token Bitbucket does not accept at all is not a token whose type can be read, and this is a question about the token's value or its scopes rather than about the endpoint$(curl_note)"
    ;;
  *)
    fact "U1-$type" UNVERIFIED "the ${type} token answered GET /2.0/user with HTTP ${status}${error:+ (Bitbucket code ${error})}, which settles nothing about whether this type has an identity there (U1 for the ${type} token stays UNVERIFIED)$(curl_note)"
    ;;
  esac
  scopes=$(reader scopes "$SCRATCH/user-$type.headers" 2>/dev/null || true)
  scope_header=$(field_of scope-header "$scopes")
  if [ "$scope_header" = absent ]; then
    # A refusal need not carry the header, and a repository token's /user answer
    # IS a refusal: the scopes are a property of the TOKEN rather than of the
    # endpoint, so the header is read off a second answer when the first carried
    # none. The read is the repository the token reaches, which this check has
    # already required of all three.
    request_h "$SCRATCH/repo-$type.json" "$SCRATCH/repo-$type.headers" GET "/repositories/${REPO}" --config "$SCRATCH/$type.conf" >/dev/null
    scopes=$(reader scopes "$SCRATCH/repo-$type.headers" 2>/dev/null || true)
    scope_header=$(field_of scope-header "$scopes")
    scope_note=" (the header was read off the repository read, because the /user answer carried none)"
  else
    scope_note=""
  fi
  write_scopes=$(field_of write-scopes "$scopes")
  read_scopes=$(field_of read-scopes "$scopes")
  case "$scope_header:$write_scopes:$read_scopes" in
  present:yes:*)
    fact "U2-$type" PASS "the ${type} token's answer carries the scope header, and it names BOTH write scopes (repository:write and pullrequest:write) beside a read scope — so a ${type} token's scopes are readable from a response, which is what a selection or a listing could be built on one day (U2 for the ${type} token). The header's value is never printed here; the scope NAMES are out of this check's own set${scope_note}"
    ;;
  present:*:*)
    # Narrow scopes are the FAIL this row exists for: a token without BOTH write
    # scopes can neither push nor open a pull request, which is what a run is
    # served by (the spec's own requirement for the tokens a run selects), and one
    # with no read scope this check knows cannot serve its clone. The header is
    # read rather than guessed, so the row says which scopes Bitbucket reports.
    fact "U2-$type" FAIL "the ${type} token's answer carries the scope header but does not name BOTH write scopes (write scopes: ${write_scopes}, a read scope this check knows: ${read_scopes}): a token without repository:write and pullrequest:write can neither push nor open a pull request, and one with no read scope cannot serve a run's clone. Create the token with repository:read, repository:write, pullrequest:read and pullrequest:write, then run this check again (U2 for the ${type} token)${scope_note}"
    ;;
  absent:*:*)
    fact "U2-$type" UNVERIFIED "the ${type} token's answer carries NO scope header at all, so this check cannot read what Bitbucket says the token's scopes are (U2 for the ${type} token stays UNVERIFIED). The scopes the token was CREATED with are what the workspace's own settings hold, and the first real use is what proves them${scope_note}"
    ;;
  esac
}

# U5: a project token as `x-token-auth` for git over HTTPS and as Bearer for
# REST. Both halves are reads, and both are made with the SAME token: the git
# config the reader wrote carries it as `x-token-auth:<value>` and the curl
# config as `Authorization: Bearer`.
step_git_reach() {
  local rc=0
  GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL="$SCRATCH/project.gitconfig" GIT_CONFIG_SYSTEM=/dev/null \
    git ls-remote --heads "https://bitbucket.org/${REPO}.git" >"$SCRATCH/ls-remote.out" 2>"$SCRATCH/ls-remote.err" || rc=$?
  local heads=0
  heads=$(grep -c 'refs/heads/' "$SCRATCH/ls-remote.out" 2>/dev/null || true)
  local rest
  rest=$(request "$SCRATCH/user-project-rest.json" GET "/repositories/${REPO}" --config "$SCRATCH/project.conf")
  if [ "$rc" -eq 0 ] && [ "$rest" = 200 ]; then
    fact U5-project PASS "the project token is accepted BOTH ways: git read ${REPO} over HTTPS as the username 'x-token-auth' with the token as the password (git ls-remote --heads: ${heads:-0} heads, exit 0) AND the same token read the repository over REST as Bearer (HTTP ${rest}). U5 is settled: a project access token is one credential for both halves of a run's traffic, whatever the record's own type says about which calls it is selected for"
  elif [ "$rc" -ne 0 ]; then
    fact U5-project FAIL "the project token is NOT accepted as 'x-token-auth' for git over HTTPS (git exited ${rc}): $(last_line "$SCRATCH/ls-remote.err"). A project token a run cannot clone or push with is not one this release can be served by, whatever REST says (U5). The REST half answered ${rest} in the same step, and nothing was changed by either read"
  else
    fact U5-project FAIL "the project token authenticated for git (git ls-remote --heads: ${heads:-0} heads) but the SAME token's Bearer REST read of ${REPO} answered HTTP ${rest}: the two halves of a run's traffic have to be served by one credential (U5), and a token good for one is not good for the other$(curl_note)"
  fi
}

# The release's own add path, once per type: `fugaro creds set
# bitbucket-token--<name>` with the value on stdin, whose check runs BEFORE
# anything is written (D2352) and which refuses with "nothing was written: …"
# when the check does not pass. --json needs every fact as a flag and asks
# nothing at a terminal, which is why the type, the coverage and the project's
# UUID or key are passed here rather than typed there. The write itself is the
# bundle entry plus the local config's record plus the state store's identity —
# three writes the CLI refuses to make half of.
step_verifier() { # step_verifier TYPE NAME FILE [extra args...]
  local type=$1 name=$2 file=$3
  shift 3
  local rc=0 out="$SCRATCH/set-$type.out" err="$SCRATCH/set-$type.err"
  fugaro creds set "$name" --type "$type" --project "$INSTALLATION" --json "$@" \
    <"$file" >"$out" 2>"$err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    stop "fugaro creds set ${name} was REFUSED (exit ${rc}) for the ${type} token: $(last_line "$err"). The add-time check is the whole of D2352 — nothing was written, and the refusal names the answer Bitbucket gave. A refusal here is a reading of the token rather than of this check, so record it in the block by hand and read docs/bitbucket-token-types/reconciliation.md's U rows against it. No value is printed"
  fi
  local facts got_type coverage identity write_verified
  facts=$(reader setjson "$out" 2>"$SCRATCH/reader.err") \
    || stop "fugaro creds set ${name} wrote something this check cannot read as its own --json document: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"
  got_type=$(field_of type "$facts")
  coverage=$(field_of coverage "$facts")
  identity=$(field_of identity "$facts")
  write_verified=$(field_of write-verified "$facts")
  if [ "$got_type" != "$type" ] || [ "${coverage:-0}" -lt 1 ]; then
    fact "verifier-$type" FAIL "fugaro creds set ${name} answered --json with type '${got_type:-none}' and ${coverage:-0} coverage entr(ies) where this check asked for a ${type} token with at least one: the record the write leaves is not the record that was asked for, and a run's selection reads exactly this document"
    return 0
  fi
  # D2353 first: write_verified=true is a FAIL whatever the rest of the document
  # says, and it is this criterion's row rather than a second row under the same
  # key (a block prints one row per criterion).
  if [ "$write_verified" != no ]; then
    fact "verifier-$type" FAIL "fugaro creds set ${name} answered --json with write_verified=true for the ${type} token: this release has no dry run for a push or a pull request, so a write the check did not make cannot have been verified (D2353)"
    return 0
  fi
  case "$type:$identity" in
  repo:absent)
    fact "verifier-$type" PASS "fugaro creds set ${name} ACCEPTED the ${type} token and wrote it (exit 0, ${coverage} coverage entr(y/ies), write scopes not proven — D2353, and no push is ever tried to prove them). The check read NO identity for it, which is R14's accepted answer for a repository token: its own GET /2.0/user is a 403, the set a run trusts is therefore partial, and the token still works. The row for the raw call above is the 403 that produced this"
    ;;
  repo:present)
    fact "verifier-$type" PASS "fugaro creds set ${name} ACCEPTED the ${type} token and wrote it, reading an identity for it (${coverage} coverage entr(y/ies); write scopes not proven, D2353). A repository token with an identity is the case R14 says does NOT happen — record this row against U1/U5 in the reconciliation: it is a fact about Bitbucket that the design reads differently, not a failure of the write"
    ;;
  *:present)
    fact "verifier-$type" PASS "fugaro creds set ${name} ACCEPTED the ${type} token and wrote it (exit 0, ${coverage} coverage entr(y/ies), an identity read and recorded in the project's credential state, write scopes not proven — D2353; the first real push is what tries them). The record is this machine's alone until you run fugaro update, which publishes the coverage to runs"
    ;;
  *:absent)
    fact "verifier-$type" UNVERIFIED "fugaro creds set ${name} ACCEPTED the ${type} token and wrote it (${coverage} coverage entr(y/ies), write scopes not proven), but the check read NO identity for a ${type} token: the write is real and the token works, while the identity a run's own set would trust is empty (R12). Whether a ${type} token has an identity at GET /2.0/user is exactly U1, and the row above this one carries the raw answer"
    ;;
  esac
}

# ------------------------------------------------------------------ the block
# The keys, in the order they are measured: the three types are each read for
# their own U1 and U2 row, then added through the release's own add path (the
# verifier row), and the project token's own git/REST row sits with it.
BLOCK_KEYS=(installation inputs xtrace U1-repository U2-repository verifier-repo
  U1-project U2-project U5-project verifier-project
  U1-workspace U2-workspace verifier-workspace restore)

# criteria_unmet FACTS — the block's criteria that are not a PASS row, one name
# per line. A criterion is one of BLOCK_KEYS, the rows the owner reads back: a
# row reading UNVERIFIED (the step could not verify its criterion — a /user
# answer outside the ones U1 is read from, a scope header Bitbucket did not send,
# an identity the check could not read for a project or workspace token) and a
# key no step wrote at all (the block prints those as "not reached", and such a
# run already ends on a FAIL or on the stop that made the step return early — the
# branch is here so that a run which finished without a row for a criterion can
# never be a pass). Both are criteria that were NOT settled, and they are what
# makes a run INCOMPLETE rather than a pass. A `restore` row that FAILed is a
# FAIL like any other, not an unmet criterion.
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
  printf '\nCHECK 590 RESULT\n' >&3
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
  printf 'CHECK 590 RESULT: %s (exit %s)\n' "$(verdict_of "$facts" "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  local project_flag="--project-key ${PROJECT_KEY}"
  if [ -n "$PROJECT_UUID" ]; then project_flag="--project-uuid ${PROJECT_UUID}"; fi
  cat <<PLAN_EOF
Check 590 — the three Bitbucket token types, and what the verifier says for each.
This is the PLAN. --dry-run touches nothing: no installation is read, no secret is
stored, no request is made and no credential is written.

  installation     ${INSTALLATION}   (GCP project ${GCP_PROJECT}, hard-coded, overridable)
  workspace        ${WORKSPACE}      (sandbox only; 'belong' is refused by name)
  repository       ${REPO}
  token files      repository: ${TOKEN_FILE}
                   project:    ${PROJECT_TOKEN_FILE}
                   workspace:  ${WORKSPACE_TOKEN_FILE}   (each mode 0600, one line, no other value)
  project record   ${project_flag:-none}   (a project token's record needs one of the two)
  API root         ${API_ROOT}
  curl cap         ${CURL_SECONDS}s per request

What the run will do, in this order:
  1. read the installation with 'fugaro doctor --project ${INSTALLATION} --json' and refuse it if it is
     not ${INSTALLATION} in GCP project ${GCP_PROJECT}, or if it names 'belong';
  2. read the three token files (each mode 0600), write six 0600 configs inside a scratch
     directory, and read ${REPO} with each token over REST — the premise every row below rests on;
  3. for each of the three types: GET /2.0/user with that token, its response headers kept
     for the scope header (U1 and U2), then 'fugaro creds set bitbucket-token--ck590-<type>'
     --type <type> --coverage ... ${project_flag} --project ${INSTALLATION} --json with the value on
     stdin, and read the document it answers (the verifier's own row);
  4. the project token again, as 'x-token-auth' for git over HTTPS and as Bearer for REST (U5);
  5. nothing is launched, nothing is published and no run is made: the three records are this
     machine's alone until you run 'fugaro update'.
It removes its scratch directory and every file a credential was written into on the way out,
however it ends, and says so in the block's restore row.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
USAGE="check590.sh --repo <workspace>/<name> --token-file <path> --project-token-file <path> --workspace-token-file <path> (--project-uuid <uuid> | --project-key <key>) [--project <installation>] [--gcp-project <project>] [--workspace <slug>] [--dry-run]"

parse_args() {
  REPO=""
  TOKEN_FILE=""
  PROJECT_TOKEN_FILE=""
  WORKSPACE_TOKEN_FILE=""
  PROJECT_UUID=""
  PROJECT_KEY=""
  INSTALLATION=${CHECK590_INSTALLATION:-fugaro}
  GCP_PROJECT=${CHECK590_GCP_PROJECT:-fugaro-dev}
  WORKSPACE=${CHECK590_WORKSPACE:-dimi_bmb}
  DRY_RUN=no
  PROJECT_NAME=""
  GCP_PROJECT_SEEN=""
  REGION=""
  REPO_PROJECT=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --repo)
      [ $# -ge 2 ] || die "--repo needs workspace/name. usage: $USAGE"
      REPO=$2
      case "$REPO" in
      */*) ;;
      *) die "--repo's repository '$REPO' is not workspace/name. usage: $USAGE" ;;
      esac
      shift 2
      ;;
    --token-file | --project-token-file | --workspace-token-file)
      [ $# -ge 2 ] || die "$1 needs a path. usage: $USAGE"
      case "$1" in
      --token-file) TOKEN_FILE=$2 ;;
      --project-token-file) PROJECT_TOKEN_FILE=$2 ;;
      --workspace-token-file) WORKSPACE_TOKEN_FILE=$2 ;;
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
  [ -n "$REPO" ] || die "--repo is required (the sandbox repository all three tokens reach). usage: $USAGE"
  [ -n "$TOKEN_FILE" ] || die "--token-file is required (the repository access token). usage: $USAGE"
  [ -n "$PROJECT_TOKEN_FILE" ] || die "--project-token-file is required. usage: $USAGE"
  [ -n "$WORKSPACE_TOKEN_FILE" ] || die "--workspace-token-file is required. usage: $USAGE"
  case "$REPO" in
  "$WORKSPACE"/*) ;;
  *) die "--repo '$REPO' is not a repository of the workspace ${WORKSPACE}: the three tokens are sandbox tokens of ${WORKSPACE}, and a repository of another workspace would make every row below a reading of another workspace's token. Pass --workspace if you really mean another one. usage: $USAGE" ;;
  esac
  if [ -n "$PROJECT_UUID" ] && [ -n "$PROJECT_KEY" ]; then
    die "--project-uuid and --project-key were both given: a project token's record takes ONE of them (the UUID is what Bitbucket matches, the key is display), and a record with two spellings of one project cannot be read as one fact. usage: $USAGE"
  fi
  [ -n "$PROJECT_UUID" ] || [ -n "$PROJECT_KEY" ] \
    || die "one of --project-uuid or --project-key is required: a project access token's coverage is its workspace slug alone, so the project it belongs to is what the record carries (R1, and bitbucketTokenProjectProblems refuses a project record without one). usage: $USAGE"
  if [ "$TOKEN_FILE" = "$PROJECT_TOKEN_FILE" ] || [ "$TOKEN_FILE" = "$WORKSPACE_TOKEN_FILE" ] || [ "$PROJECT_TOKEN_FILE" = "$WORKSPACE_TOKEN_FILE" ]; then
    die "two of the three token files are the same path: three types are three credentials, and one file for two of them would make two rows a reading of one token. usage: $USAGE"
  fi
}

main() {
  parse_args "$@"
  guard_endpoints
  # The endpoint is Bitbucket Cloud's own API, and nothing else: a redirect is
  # REFUSED rather than followed, because the request carries a token.
  API_ROOT=${CHECK590_API_URL:-https://api.bitbucket.org/2.0}
  API_ROOT=${API_ROOT%/}
  CURL_SECONDS=${CHECK590_CURL_SECONDS:-60}
  if [ "${CHECK590_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
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
  CREDENTIAL_FILES=("$SCRATCH/repository.conf" "$SCRATCH/project.conf" "$SCRATCH/workspace.conf"
    "$SCRATCH/repository.gitconfig" "$SCRATCH/project.gitconfig" "$SCRATCH/workspace.gitconfig"
    "$SCRATCH/curl.log" "$SCRATCH/reader.py"
    "$SCRATCH/set-repository.err" "$SCRATCH/set-project.err" "$SCRATCH/set-workspace.err"
    "$SCRATCH/doctor.err" "$SCRATCH/ls-remote.err")
  write_reader
  guard_xtrace

  step_installation
  step_inputs
  step_type_probe repository
  step_verifier repo bitbucket-token--ck590-repo "$TOKEN_FILE" --coverage "$REPO"
  step_type_probe project
  # The project's UUID or key, as an ARRAY rather than a `${VAR:+... "…"}`
  # expansion: a nested quoted word inside a conditional parameter expansion is a
  # shape this file's own parser reads differently from what it means.
  local project_args=()
  if [ -n "$PROJECT_UUID" ]; then project_args=(--project-uuid "$PROJECT_UUID"); else project_args=(--project-key "$PROJECT_KEY"); fi
  step_verifier project bitbucket-token--ck590-project "$PROJECT_TOKEN_FILE" --coverage "$WORKSPACE" "${project_args[@]}"
  step_git_reach
  step_type_probe workspace
  step_verifier workspace bitbucket-token--ck590-workspace "$WORKSPACE_TOKEN_FILE" --coverage "$WORKSPACE/*"

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
