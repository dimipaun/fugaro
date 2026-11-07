#!/usr/bin/env bash
# Check 180 — one Bitbucket project or workspace access token serving many
# repositories, as ONE script YOU run.
#
# It answers what only a live Bitbucket project access token settles, and what
# no offline test can: whether the token a run is meant to hold authenticates at
# all, what it authenticates AS (the `x-token-auth` username git needs, and the
# identity `GET /2.0/user` answers with), what the project it was created in
# admits (the repositories of that project, present ones and one added
# afterwards), whether approve and merge are within its scopes, whether a
# rotation is picked up by the next run, and what a second operator's machine
# without the allow-list entry plans. The register's row and section are
# docs/gcp-live-checklist.md, "Check 180"; the plan is
# docs/bitbucket-workspace-token/implementation-plan.md, its task T1.3 (which
# wrote this script) and T1.4 (which runs it).
#
# Usage:      bash scripts/live-checks/check180.sh \
#               --installation <sandbox> \
#               --token-file <path> \
#               --repo <owner/name>=<checkout path> \
#               --repo <owner/name>=<checkout path> \
#               --probe-repo <owner/name> \
#               [--new-token-file <path>] [--source <fugaro checkout>] [--merge]
#             --dry-run  prints the plan and touches nothing.
#             The token file holds the project access token and nothing else —
#             one line, no quotes, no JSON — at mode 0600. It is the ONE place
#             the value lives: this script never holds it in a variable, never
#             puts it on a command line and never prints it. It is handed to
#             `fugaro secrets set` on stdin, and to the two requests and the git
#             call below through 0600 config FILES a python3 reader writes.
#             The two --repo pairs are the repositories the allow-list entry
#             covers (owner/name) and their checkouts, one pair each. The
#             --probe-repo is the sandbox repository the check adds to the same
#             Bitbucket PROJECT afterwards, which is what makes U3 a reading of
#             the token rather than of a list of names: create it before the
#             run, and do NOT add it to `project_secrets`.
#             --new-token-file is the token created in step 5's rotation
#             (F3). Without it the rotation's own facts are INFO and name what
#             to pass; `--merge` is what lets the check measure U4's merge, and
#             without it the approve is measured and the merge is not.
# Needs:      bash, mktemp, python3 (the reader), curl, git, gcloud, fugaro,
#             and `go` with a Fugaro checkout for the live test. Nothing is
#             installed and nothing is fetched.
# Where:      a SANDBOX installation and a sandbox Bitbucket workspace only, and
#             never `belong`: this check refuses `belong` by name, as the
#             installation, as a repository's workspace and as `--project`.
# Costs:      one run per repository (two, plus one after the rotation), an
#             approve and, with --merge, a merge on sandbox repositories; a few
#             API calls and two `gcloud secrets` reads. Every one of them is
#             billed to the sandbox installation, which is what it is for.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check180_test.sh) runs it against fakes and
#             never touches Bitbucket, gcloud or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: the scratch directory and every
#             file a credential was written into are removed, and the block's
#             `restore` row says whether they really went — a FAILed row there
#             names what to remove by hand. What a person has to undo is what
#             the check exists to make: the runs' branches and pull requests,
#             the repository added to the project afterwards, the merge, and
#             the rotated token pair.
# Output:     progress on stderr, ONE block on stdout headed CHECK 180 RESULT,
#             to copy back to the coordinator. Exit 0 when every fact passed,
#             1 when one FAILed, 2 when the run could not tell (a guard stopped
#             it, a tool is missing, a command was refused).
#
# NO SECRET IN ANY OUTPUT is a property of the design, not a promise kept by
# care. The value is read from the token file by the reader, which writes it
# into 0600 files: a curl config (`Authorization: Bearer …`) and a git config
# whose `http.extraHeader` carries `x-token-auth:<token>` base64-encoded, so the
# credential is never on a command line where `ps` would show it. The one
# exception is the live test, whose own contract is the environment variable
# `FUGARO_BITBUCKET_TOKEN`: for that one command the value is read into a shell
# variable, exported, and unset again, so it is in that child's environment and
# nowhere else — it is never typed, printed or pasted. What the reader prints is
# a fixed vocabulary: a count, a status word, `present`/`absent`, `yes`/`no` and
# a name only where a name is not a secret (a repository's owner/name). Nothing
# from a response is printed as it arrived, so an endpoint that echoes back what
# it was given — which this check's own test uses as its ordinary fake — cannot
# reach the block. And no trace: `bash -x` prints every command as it runs, so
# guard_xtrace REFUSES a run whose shell arrived with tracing on.
#
# CHECK180_API_URL, CHECK180_CURL_SECONDS, CHECK180_RUN_SECONDS and
# CHECK180_DRY_RUN exist for this script's own test: the first may only name a
# loopback address of exactly the form http://127.0.0.1:<port> (the test's fake
# Bitbucket), so no environment can send the token anywhere but Bitbucket
# Cloud's own API; the others shorten the two caps and plan a run without
# touching anything.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below pass
# file names that hold a credential, and the live test's environment carries one.
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
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the token (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 180 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 180 needs bash"
  need mktemp
  need python3 "the reader that handles the token and parses the JSON is one python3 file"
  need curl
  need git
  need gcloud
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk180-mine"
SCRATCH_REMOVED=no
CREDENTIALS_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
# Every file a credential was written into: the curl config, the git config, the
# live test's environment file and the logs a command's own output goes to (an
# error message can quote a URL, and a URL is not a credential — but the same
# files are where a command's own words land, so they go with the rest). Their
# removal is READ, not assumed: the `restore` row reports it.
CREDENTIAL_FILES=()
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk180.XXXXXXXX") || die "Check 180 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 180 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
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
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the token"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so no file holds a credential and there is nothing to remove"
  else
    [ "$CREDENTIALS_REMOVED" = yes ] && [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local cred_note="the curl config, the git config and the logs the token's value or a command's own words were written into are removed"
    [ "$CREDENTIALS_REMOVED" = yes ] || cred_note="A FILE HOLDING A CREDENTIAL IS STILL THERE — look in $SCRATCH yourself and remove it: it holds the token's value in a curl or git config, or an environment file the live test read"
    facts="${facts}"$'\n'"restore|$restore|${cred_note}, and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check180.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the token or a command's environment"
}

# loopback_url: exactly http://127.0.0.1:<port> and nothing else. A value with
# userinfo is refused: curl reads everything before the '@' as a username and
# connects to the host AFTER it, so a request built from it would hand the
# token's header to a host of the environment's choosing.
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
  value=$(printenv CHECK180_API_URL 2>/dev/null || true)
  [ -n "$value" ] || return 0
  loopback_url "$value" \
    || die "Check 180 refused: CHECK180_API_URL may only name a loopback address of the form http://127.0.0.1:<port> (with no userinfo, query, fragment or space), so the token can never leave this machine (it is set for this check's own test and for nothing else)"
}

# guard_belong: `belong` is the installation and the workspace this check must
# never touch, and a script that can be pointed at it by a typo has no business
# handling the owner's own token.
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

guard_belong() {
  local what value
  for what in installation project repo-a repo-b probe-repo; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    project) value=$PROJECT_NAME ;;
    repo-a) value=$REPO_A ;;
    repo-b) value=$REPO_B ;;
    probe-repo) value=$PROBE_REPO ;;
    esac
    [ -n "$value" ] || continue
    case "$(lower "$value")" in
    belong | belong/* | */belong)
      die "Check 180 refused: '$value' (the $what) names 'belong', the installation and workspace this check must never touch. Nothing was sent anywhere"
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

# run_call SECONDS OUT LOG cmd... — one long command (a run) with a deadline of
# its own, in a process group of its own so the deadline reaches whatever the
# call started. `timeout(1)` is not used: it is not on macOS, where this check is
# also run, and a run left behind after a failure is a job someone else pays for.
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
"""Check 180's reader: the one place the token and the responses are handled.

The shell never holds the token. This program reads it out of the file the owner
named, writes it into the 0600 config files the requests and the git call carry
it in, and prints a fixed vocabulary -- counts, HTTP-adjacent words, yes/no,
present/absent and one repository name -- so a response that echoes back what it
was given cannot reach the check's result block through this.
"""
import base64, json, os, re, sys

# What a value may look like to be written into a config file: one line of
# printable ASCII with no space. Anything else is refused, and its own text is
# never printed -- the message names the FIELD.
VALUE = re.compile(r"[\x21-\x7e]+")
# A repository's owner/name, as this check is willing to print it: the register
# prints the same shape, and a value that is not one is dropped rather than
# copied into the block.
REPO = re.compile(r"[A-Za-z0-9._-]+/[A-Za-z0-9._-]+")
RUNID = re.compile(r"[A-Za-z0-9._-]{1,80}")
# The API error codes this check knows. A code outside the set is dropped rather
# than printed.
ERROR_CODES = {
    "access_denied", "bad_request", "conflict", "forbidden", "insufficient_scope",
    "invalid_request", "invalid_token", "method_not_allowed", "not_found",
    "server_error", "unauthorized",
}
PR_STATES = {"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}


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
    """The token file's own facts, and the curl config that carries the value:
    `Authorization: Bearer <token>`, which is how Bitbucket Cloud's API takes a
    project or workspace access token."""
    token = token_of(path)
    mode = os.stat(path).st_mode & 0o777
    write_private(conf, 'header = "Authorization: Bearer %s"\nheader = "Accept: application/json"\n'
                  % token.replace("\\", "\\\\").replace('"', '\\"'))
    print("mode=%o" % mode)
    print("bytes=%d" % len(token))


def cmd_gitconfig(path, conf):
    """The git config that authenticates as the username Bitbucket documents for
    an access token: `x-token-auth` with the token as the password, as an
    http.extraHeader so the value is in a FILE and never on a command line."""
    token = token_of(path)
    basic = base64.b64encode(("x-token-auth:%s" % token).encode("utf-8")).decode("ascii")
    write_private(conf, "[http]\n\textraHeader = Authorization: Basic %s\n" % basic)
    print("header=written")


def safe(value):
    return value if isinstance(value, str) and VALUE.fullmatch(value) and len(value) <= 120 else ""


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


def rows_of(path):
    doc = load(path, "the listing")
    if isinstance(doc, list):
        return doc
    if isinstance(doc, dict):
        rows = doc.get("runs")
        if isinstance(rows, list):
            return rows
        warnings = doc.get("warnings")
        if warnings:
            die("the listing carried no runs and said: %s" % safe(str(warnings[0]))[:80])
    die("the listing carries no rows")


def newest_row(path):
    rows = [r for r in rows_of(path) if isinstance(r, dict)]
    if not rows:
        die("the listing carries no run rows")
    # Newest first is the listing's own contract (register check 1b); the row
    # this check reads is the one it just launched.
    return rows[0]


def cmd_row(path):
    row = newest_row(path)
    run_id = row.get("run_id")
    print("run_id=%s" % (run_id if isinstance(run_id, str) and RUNID.fullmatch(run_id) else ""))
    print("status=%s" % safe(row.get("status")))
    print("pushed=%s" % ("yes" if row.get("pushed") is True else "no"))
    pr = row.get("pr")
    print("pr=%d" % pr if isinstance(pr, int) and not isinstance(pr, bool) and pr > 0 else "pr=0")
    print("branch=%s" % ("present" if row.get("base_branch") or row.get("pushed") is True else "absent"))


def cmd_runid(path):
    doc = load(path, "fugaro run --json")
    run_id = doc.get("run_id") if isinstance(doc, dict) else None
    print("run_id=%s" % (run_id if isinstance(run_id, str) and RUNID.fullmatch(run_id) else ""))


def cmd_user(path):
    doc = load(path, "GET /2.0/user")
    if not isinstance(doc, dict):
        print("identity=no-account")
        print("uuid=absent")
        return
    uuid = doc.get("uuid")
    named = bool(safe(doc.get("display_name")) or safe(doc.get("username")) or safe(doc.get("nickname")))
    print("identity=%s" % ("account" if named else "no-account"))
    print("uuid=%s" % ("present" if isinstance(uuid, str) and VALUE.fullmatch(uuid) else "absent"))


def cmd_repo(path):
    doc = load(path, "the repository")
    if not isinstance(doc, dict):
        print("full_name=")
        print("project=")
        return
    full = doc.get("full_name")
    print("full_name=%s" % (full if isinstance(full, str) and REPO.fullmatch(full) else ""))
    project = doc.get("project")
    key = project.get("key") if isinstance(project, dict) else None
    print("project=%s" % safe(key))


def cmd_pr(path, userfile):
    """The pull request's own facts, and whether its author is the identity the
    token answers with: the two uuids are compared here and only yes/no leaves
    this program. The author's NAME is not printed -- Bitbucket shows it in the
    browser, and this check's block is pasted into an issue."""
    doc = load(path, "the pull request")
    author = doc.get("author") if isinstance(doc, dict) else None
    state = safe(doc.get("state")) if isinstance(doc, dict) else ""
    print("state=%s" % (state if state in PR_STATES else ("absent" if not state else "other")))
    if not isinstance(author, dict):
        print("author=absent")
        print("matches-identity=unknown")
        return
    print("author=%s" % ("user" if safe(author.get("display_name")) or safe(author.get("nickname")) else "no-name"))
    mine = load(userfile, "GET /2.0/user")
    ours = mine.get("uuid") if isinstance(mine, dict) else None
    theirs = author.get("uuid")
    if isinstance(ours, str) and ours and isinstance(theirs, str) and theirs:
        print("matches-identity=%s" % ("yes" if ours == theirs else "no"))
    else:
        print("matches-identity=unknown")


def cmd_error(path):
    doc = load(path, "the answer")
    error = doc.get("error") if isinstance(doc, dict) else None
    code = error.get("code") if isinstance(error, dict) else error
    print("error=%s" % code if isinstance(code, str) and code in ERROR_CODES else "error=-")


def cmd_versions(path):
    """gcloud's `--format=value(name,state)` output: one `<number> <STATE>` line
    per version. Counts only -- a version's own name is not a secret, but a
    count is what the rotation's reading is."""
    total = enabled = latest = 0
    try:
        with open(path, "r", encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    except OSError:
        die("the version listing could not be read")
    for line in lines:
        parts = line.split()
        if not parts or not parts[0].isdigit():
            continue
        total += 1
        number = int(parts[0])
        if number > latest:
            latest = number
        if len(parts) > 1 and parts[1] == "ENABLED":
            enabled += 1
    print("versions=%d" % total)
    print("enabled=%d" % enabled)
    print("latest=%d" % latest)


def main():
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]
    table = {
        "tokenfile": (cmd_tokenfile, 2),
        "gitconfig": (cmd_gitconfig, 2),
        "doctor": (cmd_doctor, 1),
        "row": (cmd_row, 1),
        "runid": (cmd_runid, 1),
        "user": (cmd_user, 1),
        "repo": (cmd_repo, 1),
        "pr": (cmd_pr, 2),
        "error": (cmd_error, 1),
        "versions": (cmd_versions, 1),
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
  status=$(fugaro doctor --project "$INSTALLATION" --json >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" && echo ok || echo failed)
  local facts
  facts=$(reader doctor "$SCRATCH/doctor.json" 2>/dev/null || true)
  PROJECT_NAME=$(field_of project "$facts")
  GCP_PROJECT=$(field_of gcp_project "$facts")
  REGION=$(field_of region "$facts")
  if [ "$status" != ok ]; then
    stop "fugaro doctor --project ${INSTALLATION} failed: $(last_line "$SCRATCH/doctor.err"). This check reads the installation's own project before it touches anything, and it needs an installation named on this machine with usable credentials"
  fi
  [ -n "$GCP_PROJECT" ] || stop "fugaro doctor --project ${INSTALLATION} named no gcp_project, so this check cannot read the installation's secret containers and stops before it touches anything"
  guard_belong
  if [ "$(lower "$PROJECT_NAME")" != "$(lower "$INSTALLATION")" ]; then
    stop "the local config selected by --project ${INSTALLATION} is the installation '${PROJECT_NAME}': this check runs on the installation you named and no other, so it stops rather than measuring a project you did not mean"
  fi
  fact installation PASS "installation '${PROJECT_NAME}' in GCP project ${GCP_PROJECT}, region ${REGION:-unset}, read from fugaro doctor --project ${INSTALLATION} at $(date -u +%Y-%m-%dT%H:%M:%SZ). It is a sandbox installation: this check refuses 'belong' as an installation, as a repository's workspace and as the project doctor selects"
}

step_inputs() {
  local facts
  facts=$(reader tokenfile "$TOKEN_FILE" "$CURL_CONF" 2>"$SCRATCH/reader.err" || true)
  [ -n "$facts" ] || stop "the token file $TOKEN_FILE could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null). It holds the project access token, one line and nothing else, and no value from it is printed. Nothing was sent anywhere"
  TOKEN_MODE=$(field_of mode "$facts")
  TOKEN_BYTES=$(field_of bytes "$facts")
  case "$TOKEN_MODE" in
  600) ;;
  *) stop "the token file $TOKEN_FILE is mode ${TOKEN_MODE:-?} and this check wants 0600: a token any other user of this machine can read is not one to store project-wide. Type 'chmod 600 $TOKEN_FILE' and run again. Nothing was sent anywhere" ;;
  esac
  reader gitconfig "$TOKEN_FILE" "$GIT_CONF" >/dev/null 2>"$SCRATCH/reader.err" \
    || stop "the git config the clone reads could not be written from the token file: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"

  # The project premise (D461): a project access token serves the repositories
  # of ONE Bitbucket project, so the two repositories and the probe have to be
  # in the same one -- and the check reads that from Bitbucket rather than
  # trusting the allow-list entry.
  local status_a status_b status_p
  status_a=$(request "$SCRATCH/repo-a.json" GET "/repositories/${REPO_A}" --config "$CURL_CONF")
  status_b=$(request "$SCRATCH/repo-b.json" GET "/repositories/${REPO_B}" --config "$CURL_CONF")
  status_p=$(request "$SCRATCH/probe.json" GET "/repositories/${PROBE_REPO}" --config "$CURL_CONF")
  local proj_a proj_b proj_p code
  proj_a=$(field_of project "$(reader repo "$SCRATCH/repo-a.json" 2>/dev/null || true)")
  proj_b=$(field_of project "$(reader repo "$SCRATCH/repo-b.json" 2>/dev/null || true)")
  proj_p=$(field_of project "$(reader repo "$SCRATCH/probe.json" 2>/dev/null || true)")
  code=$(field_of error "$(reader error "$SCRATCH/repo-a.json" 2>/dev/null || true)")
  if [ "$status_a" != 200 ] || [ "$status_b" != 200 ]; then
    stop "the token did not read the two repositories it was stored for: ${REPO_A} answered ${status_a} and ${REPO_B} answered ${status_b}${code:+ (${code#error=})}$(curl_note). Both are sandbox repositories of one Bitbucket project, and the token has to reach them before anything else here is a reading. Check the token, its scopes and that the two names are right, then run again. No value from an answer is printed"
  fi
  if [ -n "$proj_a" ] && [ -n "$proj_b" ] && [ "$proj_a" != "$proj_b" ]; then
    fact inputs FAIL "the two repositories are in DIFFERENT Bitbucket projects (${REPO_A} in '${proj_a}', ${REPO_B} in '${proj_b}'), and a project access token serves the repositories of one project: this run measures a wider token than the check is about. Use two repositories of one project, or a workspace access token and the register's account of it"
  else
    fact inputs PASS "the token read ${REPO_A} and ${REPO_B} (HTTP ${status_a} and ${status_b}), both in the Bitbucket project '${proj_a:-unread}'; the token file is mode ${TOKEN_MODE} with ${TOKEN_BYTES} bytes, read into the 0600 config files this check's requests and its git call carry it in, so the value is never on a command line, in this shell, or in this block"
  fi
  PROBE_STATUS=$status_p
  PROBE_PROJECT=$proj_p
}

# U1: the clone and the push. `git ls-remote` with the git config the reader
# wrote is a READ: it lists the repository's heads and changes nothing, and it
# authenticates exactly the way a run's clone and push do -- as `x-token-auth`
# with the token as the password.
step_git_reach() {
  local rc=0
  GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL="$GIT_CONF" GIT_CONFIG_SYSTEM=/dev/null \
    git ls-remote --heads "$REPO_A_URL" >"$SCRATCH/ls-remote.out" 2>"$SCRATCH/ls-remote.err" || rc=$?
  if [ "$rc" -eq 0 ]; then
    local heads
    heads=$(grep -c 'refs/heads/' "$SCRATCH/ls-remote.out" 2>/dev/null || true)
    fact U1 PASS "git read ${REPO_A} with the token as the username 'x-token-auth' and its value as the password (git ls-remote --heads: ${heads:-0} heads), so the clone and the push of a run authenticate the way the register says. The credential travelled in a 0600 git config, so it was not on the command line"
  else
    fact U1 FAIL "git could NOT read ${REPO_A} with the token as 'x-token-auth' (git exited ${rc}): $(last_line "$SCRATCH/ls-remote.err"). A token that does not authenticate this way is not one a run can clone or push with, whatever the API says. Nothing was changed by this step"
  fi
}

# U2's first half, and U7: what the token answers about itself.
step_identity() {
  local status
  status=$(request "$SCRATCH/user.json" GET "/user" --config "$CURL_CONF")
  local facts identity
  facts=$(reader user "$SCRATCH/user.json" 2>/dev/null || true)
  identity=$(field_of identity "$facts")
  case "$status:$identity" in
  200:account)
    fact U2 PASS "the token answers GET /2.0/user with an account of its own (HTTP 200, a display name and a uuid are present; neither value is printed here). Bitbucket shows that name as the author of every pull request the token opens, so one token means one author on every repository it is allow-listed for (F8, D472)"
    ;;
  200:*)
    fact U2 INFO "GET /2.0/user answered 200 but the answer carries no name this check can read, so what the token's pull requests are authored BY is not settled here (U2 stays UNVERIFIED); the pull request's own author field is read below"
    ;;
  *)
    fact U2 INFO "GET /2.0/user answered ${status}$(curl_note), so the identity behind the token was NOT read (U2 stays UNVERIFIED): Bitbucket documents this endpoint for an access token and whether it answers for one is exactly what U7 is open on"
    ;;
  esac
}

# The store, and the allow-list behind it: accepted here is what says the local
# config's project_secrets.bitbucket-token entry names the repositories the
# token is meant for.
step_store() {
  local rc=0
  fugaro secrets set bitbucket-token --project-wide --yes --project "$INSTALLATION" \
    <"$TOKEN_FILE" >"$SCRATCH/store.out" 2>"$SCRATCH/store.err" || rc=$?
  if [ "$rc" -eq 0 ]; then
    fact store PASS "fugaro secrets set bitbucket-token --project-wide accepted the token for installation ${INSTALLATION} (exit 0), reading the value from ${TOKEN_FILE} on stdin — so the local config's project_secrets.bitbucket-token.allow_data_to names these repositories, which is the entry the allow-list is built from. Nothing about the value is printed, and --yes is passed so no prompt can read the token file as its answer"
  else
    stop "fugaro secrets set bitbucket-token --project-wide was REFUSED (exit ${rc}): $(last_line "$SCRATCH/store.err"). The store is the first half of the path this check measures, and the likely reasons are that the local config has no project_secrets.bitbucket-token.allow_data_to entry (step 1 of docs/git-providers.md's 'One workspace or project token for many repositories') or that --project ${INSTALLATION} is not the installation you meant. Nothing was written and no value is printed"
  fi
}

# D462's second half: with the name covered, `fugaro update --repo` asks
# NOTHING. The reading is the absence of the prompt AND the absence of the
# "still missing" lines the update prints for a repository it has no token for,
# with stdin closed so a prompt could not be answered from the token file.
update_one() { # update_one NAME PATH
  local name=$1 path=$2 rc=0 out="$SCRATCH/update.log"
  : >"$out"
  [ -d "$path/.git" ] || [ -f "$path/.git" ] || stop "the checkout ${path} for ${name} is not a git work tree: pass --repo ${name}=<path to the checkout> and run again"
  ( cd "$path" && fugaro update --repo --project "$INSTALLATION" --yes </dev/null >"$out" 2>&1 ) || rc=$?
  if [ "$rc" -ne 0 ]; then
    printf 'FAILED (exit %s): %s\n' "$rc" "$(last_line "$out")"
    return 0
  fi
  if grep -q 'secrets set bitbucket-token' "$out"; then
    printf 'ASKED-FOR-A-TOKEN: %s\n' "$(grep 'secrets set bitbucket-token' "$out" | head -n 1 | tr -d '\r' | head -c 200)"
    return 0
  fi
  if grep -qE 'Still missing|not covered' "$out"; then
    printf 'MISSING: %s\n' "$(last_line "$out")"
    return 0
  fi
  printf 'OK\n'
}

step_update() {
  local a b
  a=$(update_one "$REPO_A" "$PATH_A")
  b=$(update_one "$REPO_B" "$PATH_B")
  case "$a$b" in
  *FAILED* | *ASKED-FOR-A-TOKEN* | *MISSING*)
    fact update FAIL "fugaro update --repo did not come out clean on both repositories: ${REPO_A} → $(printf '%s' "$a" | cut -c1-140), ${REPO_B} → $(printf '%s' "$b" | cut -c1-140). It has to ASK NOTHING for a repository the allow-list covers, and a run that names a token to store means the name is not covered (D462). This is the reading the whole project-wide store exists for"
    ;;
  *)
    fact update PASS "fugaro update --repo ran to completion on both repositories with the project-wide token in place and named no missing secret and no 'fugaro secrets set bitbucket-token' line for either: with the name covered the update asks NOTHING, which is the second half of D462. Both ran with stdin at /dev/null, so a prompt could not have been answered by the token file"
    ;;
  esac
}

# U2's second half, and U4's approve: one run per repository, then the pull
# request it opened, read with the token.
step_run() { # step_run LABEL REPO PATH
  local label=$1 repo=$2 path=$3 rc=0
  local task="$SCRATCH/task-${label}.md" json="$SCRATCH/run-${label}.json" log="$SCRATCH/run-${label}.log"
  cat >"$task" <<TASK_EOF
Add a line to the repository's README.md that says the repository was used for Fugaro's Check 180
($(date -u +%Y-%m-%d)), and open the pull request. Change nothing else: this task exists so a run
clones, pushes and opens a pull request with the installation's Bitbucket token, and Check 180 reads
what that run recorded.
TASK_EOF
  CREDENTIAL_FILES+=("$log")
  say "launching one run in ${repo} (this is the long step: a run clones, implements, reviews and opens its pull request)"
  run_call "$RUN_SECONDS" "$json" "$log" fugaro run --repo "$repo" --project "$INSTALLATION" \
    --title "Check 180: one token for many repositories (${repo})" \
    --task-file "$task" --json || rc=$?
  local run_id status pushed pr
  run_id=$(field_of run_id "$(reader runid "$json" 2>/dev/null || true)")
  local row
  row=$(fugaro runs ls --repo "$repo" --project "$INSTALLATION" --limit 1 --json >"$SCRATCH/rows-${label}.json" 2>>"$log" \
    && reader row "$SCRATCH/rows-${label}.json" 2>/dev/null) || true
  status=$(field_of status "$row")
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  case "$label" in
  run-a) PR_A=${pr#pr=} ;;
  run-b) PR_B=${pr#pr=} ;;
  esac
  set_run_result "$label" "$run_id" "${pr#pr=}" "$pushed" "$status" "$rc" "$repo" "$log"
}

# run_pr LABEL: the pull request the run of that label opened, or 0. A number
# and nothing else, so a step below can name it without reading the block.
run_pr() {
  case "$1" in
  run-a) printf '%s' "${PR_A:-0}" ;;
  run-b) printf '%s' "${PR_B:-0}" ;;
  esac
}

# set_run_result: the fact for one run, from the row's own reading. Kept apart
# so the rotation's run is read exactly the way the two are.
set_run_result() { # set_run_result LABEL RUNID PR PUSHED STATUS RC REPO LOG
  local label=$1 run_id=$2 pr=$3 pushed=$4 status=$5 rc=$6 repo=$7 log=$8
  if [ "$pushed" = yes ] && [ -n "$pr" ] && [ "$pr" != 0 ]; then
    fact "$label" PASS "the run in ${repo}${run_id:+ (${run_id})} pushed its branch and opened pull request #${pr} (the row's pushed=yes and pr=${pr}), status=${status:-unread}. One run per repository is what makes the push, the pull request and their author a reading of THIS token rather than of the credentials a single repository holds"
    return 0
  fi
  if [ "$rc" -ne 0 ] && [ "$pushed" != yes ]; then
    fact "$label" FAIL "the run in ${repo} did not finish (fugaro run exited ${rc}${run_id:+, run ${run_id}}; the row reads status=${status:-unread}): $(last_line "$log"). Nothing about the clone, the push or the pull request is settled by it"
    return 0
  fi
  if [ "$pushed" = yes ]; then
    fact "$label" FAIL "the run in ${repo} pushed its branch but the row names no pull request (pr=0, status=${status:-unread}): the token could push and could not open a pull request, which is the pair the scopes are chosen for"
    return 0
  fi
  fact "$label" FAIL "the run in ${repo} did not push (the row reads pushed=no, status=${status:-unread}): $(last_line "$log"). A run that does not push settles nothing about the token"
}

# The pull request's author, and (with --merge) U4's second half: only the
# scopes settle whether the token may approve and merge, and a merge cannot be
# undone — on a sandbox repository that is the point, and this check never does
# it on belong.
# step_pr LABEL REPO PR USERFILE — LABEL is the short a/b the block's rows are
# named after (author-a, U4-approve-a, U4-merge-a), one per --repo pair.
step_pr() { # step_pr LABEL REPO PR USERFILE
  local label=$1 repo=$2 pr=$3 userfile=$4
  [ -n "$pr" ] && [ "$pr" != 0 ] || return 0
  local status facts author match state
  status=$(request "$SCRATCH/pr-${label}.json" GET "/repositories/${repo}/pullrequests/${pr}" --config "$CURL_CONF")
  if [ "$status" != 200 ]; then
    fact author-$label INFO "pull request #${pr} of ${repo} could not be read with the token (HTTP ${status}$(curl_note)), so its author was NOT read; the register's section is where you record the name Bitbucket shows"
    return 0
  fi
  facts=$(reader pr "$SCRATCH/pr-${label}.json" "$userfile" 2>/dev/null || true)
  author=$(field_of author "$facts")
  match=$(field_of matches-identity "$facts")
  case "$author:$match" in
  absent:*)
    fact author-$label INFO "pull request #${pr} of ${repo} carries no author this check can read, so U2's 'the token authors the pull request' stays UNVERIFIED for it; read the name in Bitbucket and record it in the register's section"
    ;;
  *:yes)
    fact author-$label PASS "pull request #${pr} of ${repo} was authored by the SAME account the token answers GET /2.0/user with (the two uuids match; neither is printed), so the token's own name is the author a reader sees (U2, F8, D472). With one token every enrolled repository's pull requests carry that one author"
    ;;
  *)
    fact author-$label INFO "pull request #${pr} of ${repo} has an author this check cannot compare with the token's identity (${match}), so U2's attribution stays UNVERIFIED for it: record the author's name from Bitbucket in the register's section"
    ;;
  esac

  local approve
  approve=$(request "$SCRATCH/approve-${label}.json" POST "/repositories/${repo}/pullrequests/${pr}/approve" --config "$CURL_CONF")
  case "$approve" in
  200 | 201)
    fact U4-approve-$label PASS "the token APPROVED pull request #${pr} of ${repo} (HTTP ${approve}), so approving is within its scopes; the approval is undone by DELETE on the same path, and nothing else changed"
    ;;
  *)
    local code
    code=$(field_of error "$(reader error "$SCRATCH/approve-${label}.json" 2>/dev/null || true)")
    fact U4-approve-$label FAIL "the token could NOT approve pull request #${pr} of ${repo} (HTTP ${approve}${code:+, ${code#error=}}): approving is not within a token's reach, and the register's U4 reading has to say so${approve:+$(curl_note)}"
    ;;
  esac

  if [ "$MERGE" != yes ]; then
    fact U4-merge-$label INFO "the merge was NOT attempted: pass --merge to measure it (only the scopes settle whether a token may merge). A merge cannot be undone, which is why it is a flag rather than the default; on a sandbox repository it is what the check is for"
    return 0
  fi
  local merge
  merge=$(request "$SCRATCH/merge-${label}.json" POST "/repositories/${repo}/pullrequests/${pr}/merge" --config "$CURL_CONF" \
    -H 'Content-Type: application/json' --data '{"close_source_branch": true, "merge_strategy": "merge_commit"}')
  case "$merge" in
  200 | 201)
    fact U4-merge-$label PASS "the token MERGED pull request #${pr} of ${repo} (HTTP ${merge}): approve and merge are both within its scopes, which is what U4 is open on. The merge cannot be undone — the repository is a sandbox one, and the register's Restore records it"
    ;;
  *)
    local code
    code=$(field_of error "$(reader error "$SCRATCH/merge-${label}.json" 2>/dev/null || true)")
    fact U4-merge-$label FAIL "the token could NOT merge pull request #${pr} of ${repo} (HTTP ${merge}${code:+, ${code#error=}}): approve and merge are NOT both within a project token's scopes, which is a reading the plan has to have before it promises a merge anywhere${merge:+$(curl_note)}"
    ;;
  esac
}

# U3: a repository added to the same Bitbucket PROJECT afterwards, with no new
# allow-list entry. The read uses the same token the store accepted, and the
# premise (that the entry covers the project rather than naming two repositories)
# is what the register's section has you record.
step_probe() {
  local status=${PROBE_STATUS:-} code
  if [ -z "$status" ]; then
    status=$(request "$SCRATCH/probe.json" GET "/repositories/${PROBE_REPO}" --config "$CURL_CONF")
    PROBE_PROJECT=$(field_of project "$(reader repo "$SCRATCH/probe.json" 2>/dev/null || true)")
  fi
  code=$(field_of error "$(reader error "$SCRATCH/probe.json" 2>/dev/null || true)")
  case "$status" in
  200)
    fact U3 PASS "the token READ ${PROBE_REPO} (HTTP 200), a repository of the Bitbucket project '${PROBE_PROJECT:-unread}' that this run stored for only two other repositories: a repository added to the project afterwards is reachable with the SAME token and no new entry (D464's 'present and future'). Whether it is admitted by the allow-list entry is the reading you confirm in the register's section — if the entry names repositories one by one, this read still shows the TOKEN's reach and not what a run would be launched for"
    ;;
  401 | 403 | 404)
    fact U3 FAIL "the token could NOT read ${PROBE_REPO} (HTTP ${status}${code:+, ${code#error=}}): a repository added to the Bitbucket project afterwards is NOT reachable with this token, which contradicts the project scope the plan's U3 rests on. Record it before any task builds on it"
    ;;
  *)
    fact U3 INFO "the probe repository ${PROBE_REPO} answered ${status}, which settles neither reach nor its absence, so U3 stays UNVERIFIED$(curl_note)"
    ;;
  esac
}

# The live test: the API itself, through the project's own harness. It is the
# one command whose contract is the token in an environment variable, so the
# value is read into a shell variable here and unset again straight after.
step_live_test() {
  if [ ! -d "$SOURCE/internal/gitprov/bitbucket" ]; then
    fact live-test INFO "the live test was NOT run: no Fugaro checkout with internal/gitprov/bitbucket was found at ${SOURCE} (pass --source <path>). Run it by hand, with the token file you named: FUGARO_LIVE_REPO=${REPO_A} FUGARO_BITBUCKET_TOKEN=\$(cat ${TOKEN_FILE}) go test -tags live -run TestLiveBitbucket -count=1 ./internal/gitprov/bitbucket/ — and record what it says in the register's section"
    return 0
  fi
  if ! command -v go >/dev/null 2>&1; then
    fact live-test INFO "the live test was NOT run: 'go' is not on PATH. Run the command in the register's section by hand and record its answer"
    return 0
  fi
  local rc=0 token=""
  IFS= read -r token <"$TOKEN_FILE" || true
  ( cd "$SOURCE" && FUGARO_LIVE_REPO="$REPO_A" FUGARO_BITBUCKET_TOKEN="$token" \
    go test -tags live -run TestLiveBitbucket -count=1 ./internal/gitprov/bitbucket/ ) \
    >"$SCRATCH/live-test.log" 2>&1 || rc=$?
  unset token
  if [ "$rc" -eq 0 ]; then
    fact live-test PASS "TestLiveBitbucket passed with the token (go test -tags live -run TestLiveBitbucket ./internal/gitprov/bitbucket/, exit 0): the API the run's own code speaks answers this token, which is the harness's half of U2 and U7. The token was read out of ${TOKEN_FILE} into that one command's environment and unset again; it was never on a command line"
  else
    fact live-test FAIL "TestLiveBitbucket failed with the token (exit ${rc}): $(last_line "$SCRATCH/live-test.log"). The live test is the API harness the run's code is written against, so a failure here is a reading about the token rather than about the test — and the output is in the run's scratch directory, not here"
  fi
}

# F3: the rotation. A new version of the same secret, and a run after it, which
# is the reading the register's step 8 records. When --new-token-file is absent
# the step says what to pass rather than guessing.
step_rotation() {
  if [ -z "$NEW_TOKEN_FILE" ]; then
    fact rotation INFO "the rotation was NOT measured: pass --new-token-file <path> with the token created in step 8 of the register's section (stored as a new version of the same secret). Without it, what a rotation does to the next run is an assumption this check does not make"
    return 0
  fi
  local container count
  count=$(gcloud secrets list --project "$GCP_PROJECT" \
    --filter="name~^fugaro-project-$(lower "$INSTALLATION")-bitbucket-token-" --format='value(name)' 2>"$SCRATCH/gcloud.err" \
    | grep -c . || true)
  if [ "${count:-0}" != 1 ]; then
    stop "the project-wide container for bitbucket-token was not found once (the listing named ${count:-0}): $(last_line "$SCRATCH/gcloud.err"). The container's name carries a hash suffix, so this check finds it by filter rather than composing it, and it will not pick one of several. Read 'gcloud secrets list --project ${GCP_PROJECT} --filter=name~bitbucket-token' yourself and store the new version with the fugaro secrets set command below"
  fi
  container=$(gcloud secrets list --project "$GCP_PROJECT" \
    --filter="name~^fugaro-project-$(lower "$INSTALLATION")-bitbucket-token-" --format='value(name)' 2>>"$SCRATCH/gcloud.err" | head -n 1)

  local before rc=0
  before=$(gcloud secrets versions list "$container" --project "$GCP_PROJECT" --format='value(name,state)' 2>>"$SCRATCH/gcloud.err" >"$SCRATCH/versions-before.txt" && reader versions "$SCRATCH/versions-before.txt")
  local before_n
  before_n=$(field_of versions "$before")
  local rc2=0
  fugaro secrets set bitbucket-token --project-wide --yes --project "$INSTALLATION" \
    <"$NEW_TOKEN_FILE" >"$SCRATCH/store-new.out" 2>"$SCRATCH/store-new.err" || rc2=$?
  if [ "$rc2" -ne 0 ]; then
    fact rotation FAIL "the new token was NOT stored (fugaro secrets set exited ${rc2}): $(last_line "$SCRATCH/store-new.err"). The rotation's first half is storing the new value as a new version of the same secret; nothing about the next run can be read until it is"
    return 0
  fi
  local after after_n enabled latest
  after=$(gcloud secrets versions list "$container" --project "$GCP_PROJECT" --format='value(name,state)' 2>>"$SCRATCH/gcloud.err" >"$SCRATCH/versions-after.txt" && reader versions "$SCRATCH/versions-after.txt")
  after_n=$(field_of versions "$after")
  enabled=$(field_of enabled "$after")
  latest=$(field_of latest "$after")

  say "the rotation's new version is stored; launching one more run in ${REPO_A} so the next run's own reading is the last word"
  local task="$SCRATCH/task-rotation.md"
  cat >"$task" <<TASK_EOF
Add a second line to the repository's README.md, saying that Fugaro's Check 180 ran once more after the
token was rotated ($(date -u +%Y-%m-%d)). Change nothing else: this run exists so the check can read that a
run launched AFTER the rotation still clones, pushes and opens its pull request.
TASK_EOF
  CREDENTIAL_FILES+=("$SCRATCH/rotation.log")
  local run_rc=0
  run_call "$RUN_SECONDS" "$SCRATCH/rotation.json" "$SCRATCH/rotation.log" fugaro run --repo "$REPO_A" --project "$INSTALLATION" \
    --title "Check 180: the run after the rotation (${REPO_A})" \
    --task-file "$task" --json || run_rc=$?
  local row pushed pr status
  row=$(fugaro runs ls --repo "$REPO_A" --project "$INSTALLATION" --limit 1 --json >"$SCRATCH/rows-rotation.json" 2>>"$SCRATCH/rotation.log" \
    && reader row "$SCRATCH/rows-rotation.json" 2>/dev/null) || true
  pushed=$(field_of pushed "$row")
  pr=$(field_of pr "$row")
  status=$(field_of status "$row")
  if [ "$pushed" = yes ] && [ "${pr#pr=}" != 0 ] && [ "${pr#pr=}" != "" ]; then
    fact rotation PASS "the new token is a new ENABLED version of the project-wide container (${before_n:-?} version(s) before, ${after_n:-?} after; ${enabled:-?} enabled, highest number ${latest:-?}), and a run launched AFTER the store pushed its branch and opened pull request #${pr#pr=} (status=${status:-unread}): the next run uses the new version, which is what F3's order is written for — the old token is revoked only after that"
  else
    fact rotation FAIL "the new version was stored (${before_n:-?} version(s) before, ${after_n:-?} after) but the run after it did not push and open a pull request (pushed=${pushed:-unread}, pr=${pr:-unread}, status=${status:-unread}, fugaro run exited ${run_rc}): $(last_line "$SCRATCH/rotation.log"). The rotation is not settled by the secret's versions alone — the reading is that the NEXT RUN uses the new value"
  fi
}

# U5 and D467: a second machine, seeded from the shared config, has no
# project_secrets entry, and what it plans is a per-repository token. The whole
# machine is a scratch home, so nothing on this one is touched.
step_second_machine() {
  local home="$SCRATCH/home"
  mkdir -p "$home/.config/fugaro"
  chmod 700 "$home"
  local env=(HOME="$home" XDG_CONFIG_HOME="$home/.config" CLOUDSDK_CONFIG="$home/.gcloud" GIT_CONFIG_GLOBAL="$home/.gitconfig")
  local rc=0
  env "${env[@]}" fugaro update --gcp-project "$GCP_PROJECT" --region "$REGION" --name "$INSTALLATION" --config-only \
    </dev/null >"$SCRATCH/second-seed.log" 2>&1 || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact second-machine INFO "the second machine was NOT seeded (fugaro update --gcp-project ... --config-only exited ${rc}): $(last_line "$SCRATCH/second-seed.log"). U5 stays UNVERIFIED for this run; the reading is that a machine seeded from the shared config has NO project_secrets.bitbucket-token entry, which is operator-local by design"
    return 0
  fi
  # The paths the seeded machine is asked about are the two checkouts this run
  # uses: `update --repo` is a BOOL, and the checkout it acts on is its
  # positional PATH — whose default is the current directory, which here is
  # wherever the owner launched the check. Without the path the plan would be
  # taken against that directory rather than against an allow-listed checkout,
  # and the reading below would be about the wrong repository.
  local rc2=0
  ( cd "$PATH_A" && exec env "${env[@]}" fugaro update --repo "$PATH_A" --plan-only </dev/null ) \
    >"$SCRATCH/second-plan.log" 2>&1 || rc2=$?
  local plan="$SCRATCH/second-plan.log"
  if grep -qE "secrets set bitbucket-token --repo|per-repository token|bitbucket-token.*--repo" "$plan"; then
    fact second-machine PASS "a machine seeded from the shared config with fugaro update --gcp-project ... --config-only, holding no project_secrets.bitbucket-token entry, plans a PER-REPOSITORY token: 'fugaro update --repo <path> --plan-only' printed a fugaro secrets set bitbucket-token --repo line (D467's sentence is exactly this — copy the project_secrets entry to every operator machine, or that machine types a per-repository token). The whole machine was a scratch home inside this run's scratch directory"
  elif [ "$rc2" -ne 0 ]; then
    fact second-machine INFO "the plan did not run (exit ${rc2}): $(last_line "$plan"), so U5 stays UNVERIFIED"
  else
    fact second-machine FAIL "the second machine's 'fugaro update --repo <path> --plan-only' planned NO per-repository token, and a machine seeded from the shared config must have no project_secrets.bitbucket-token entry: either the entry reached the shared config (which D467 says it must not) or the plan hides the prompt. $(last_line "$plan")"
  fi
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(installation inputs xtrace U1 U2 store update run-a run-b author-a author-b
  U4-approve-a U4-merge-a U4-approve-b U4-merge-b U3 live-test rotation second-machine
  restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 180 RESULT\n' >&3
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
  printf 'CHECK 180 RESULT END (exit %s)\n' "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  cat <<PLAN_EOF
Check 180 — one Bitbucket project or workspace access token serving many repositories.
This is the PLAN. --dry-run touches nothing: no installation is read, no secret is
stored, no run is launched, no request is made and no credential is written.

  installation   ${INSTALLATION}
  repositories   ${REPO_A} (checkout ${PATH_A})
                 ${REPO_B} (checkout ${PATH_B})
  probe repo     ${PROBE_REPO}   (added to the same Bitbucket project afterwards)
  token file     ${TOKEN_FILE}
  rotation       ${NEW_TOKEN_FILE:-not given}   (without it the rotation's facts read INFO)
  merge          ${MERGE}   (--merge lets the check measure U4's merge; a merge cannot be undone)
  source tree    ${SOURCE}
  API root       ${API_ROOT}
  run cap        ${RUN_SECONDS}s per run

What the run will do, in this order:
  1. read the installation with 'fugaro doctor --project ${INSTALLATION} --json', and refuse 'belong';
  2. read the token file (mode 0600, one line), write the 0600 curl and git configs inside a
     scratch directory, and read the two repositories and the probe with the token;
  3. git ls-remote ${REPO_A} as 'x-token-auth' with the token (U1, a read that changes nothing);
  4. GET /2.0/user with the token (U2, U7);
  5. 'fugaro secrets set bitbucket-token --project-wide --yes' with the token on stdin (the store,
     and the allow-list entry behind it);
  6. 'fugaro update --repo' in ${PATH_A} and ${PATH_B} with stdin at /dev/null, expecting NO prompt;
  7. one run in ${REPO_A} and one in ${REPO_B}, then each pull request's author and an approve
     (and, with --merge, a merge) on the sandbox repositories;
  8. the probe repository, for U3;
  9. TestLiveBitbucket in ${SOURCE} with the token in that one command's environment;
 10. with --new-token-file: the new version of the same secret, its version list, and one more run;
 11. a scratch home seeded with 'fugaro update --gcp-project ... --config-only', then
     'fugaro update --repo --plan-only' there, expecting a per-repository token (U5, D467).
It removes its scratch directory and every file a credential was written into on the way out,
however it ends, and says so in the block's restore row.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
USAGE="check180.sh --installation <sandbox> --token-file <path> --repo <owner/name>=<checkout> --repo <owner/name>=<checkout> --probe-repo <owner/name> [--new-token-file <path>] [--source <path>] [--merge] [--dry-run]"

parse_args() {
  INSTALLATION=""
  TOKEN_FILE=""
  NEW_TOKEN_FILE=""
  PROBE_REPO=""
  SOURCE=""
  MERGE=no
  DRY_RUN=no
  REPO_A=""
  REPO_B=""
  PATH_A=""
  PATH_B=""
  PR_A=""
  PR_B=""
  PROJECT_NAME=""
  GCP_PROJECT=""
  REGION=""
  PROBE_STATUS=""
  PROBE_PROJECT=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --token-file)
      [ $# -ge 2 ] || die "--token-file needs a path. usage: $USAGE"
      TOKEN_FILE=$2
      shift 2
      ;;
    --new-token-file)
      [ $# -ge 2 ] || die "--new-token-file needs a path. usage: $USAGE"
      NEW_TOKEN_FILE=$2
      shift 2
      ;;
    --probe-repo)
      [ $# -ge 2 ] || die "--probe-repo needs owner/name. usage: $USAGE"
      PROBE_REPO=$2
      shift 2
      ;;
    --source)
      [ $# -ge 2 ] || die "--source needs a path. usage: $USAGE"
      SOURCE=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs owner/name=<checkout path>. usage: $USAGE"
      local pair=$2 name=${2%%=*} path=${2#*=}
      if [ "$name" = "$pair" ] || [ -z "$name" ] || [ -z "$path" ]; then
        die "--repo takes owner/name=<checkout path>, and '$pair' is not that. usage: $USAGE"
      fi
      case "$name" in
      */*) ;;
      *) die "--repo's repository '$name' is not owner/name. usage: $USAGE" ;;
      esac
      if [ -z "$REPO_A" ]; then
        REPO_A=$name
        PATH_A=$path
      elif [ -z "$REPO_B" ]; then
        REPO_B=$name
        PATH_B=$path
      else
        die "--repo was given three times: this check stores for TWO repositories (the pair the allow-list entry covers). usage: $USAGE"
      fi
      shift 2
      ;;
    --merge)
      MERGE=yes
      shift
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
  [ -n "$INSTALLATION" ] || die "--installation is required. usage: $USAGE"
  [ -n "$TOKEN_FILE" ] || die "--token-file is required. usage: $USAGE"
  [ -n "$PROBE_REPO" ] || die "--probe-repo is required (U3 is the repository the same Bitbucket project gains afterwards). usage: $USAGE"
  [ -n "$REPO_A" ] && [ -n "$REPO_B" ] || die "two --repo pairs are required. usage: $USAGE"
  [ "$REPO_A" != "$REPO_B" ] \
    || die "the two --repo pairs name the same repository: one run per repository is what makes the push, the pull request and their author a reading of this token, and one name for both measures it once. usage: $USAGE"
  case "$SOURCE" in
  "") SOURCE=$(cd -- "${0%/*}/../.." 2>/dev/null && pwd || true) ;;
  esac
  [ -n "$SOURCE" ] || SOURCE=/nonexistent
}

main() {
  need_tools
  parse_args "$@"
  guard_endpoints
  # The endpoint is Bitbucket Cloud's own API, and nothing else: a redirect is
  # REFUSED rather than followed, because the request carries the token.
  API_ROOT=${CHECK180_API_URL:-https://api.bitbucket.org/2.0}
  API_ROOT=${API_ROOT%/}
  CURL_SECONDS=${CHECK180_CURL_SECONDS:-60}
  RUN_SECONDS=${CHECK180_RUN_SECONDS:-5400}
  REPO_A_URL="https://bitbucket.org/${REPO_A}.git"
  if [ "${CHECK180_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
  guard_belong

  if [ "$DRY_RUN" = yes ]; then
    print_plan
    REACHED_END=yes
    PRINTED=1
    trap - ERR
    exit 0
  fi

  scratch_up
  CURL_CONF="$SCRATCH/api.conf"
  GIT_CONF="$SCRATCH/gitconfig"
  CREDENTIAL_FILES=("$CURL_CONF" "$GIT_CONF" "$SCRATCH/curl.log" "$SCRATCH/update.log")
  write_reader
  guard_xtrace

  step_installation
  step_inputs
  step_git_reach
  step_identity
  step_store
  step_update
  step_run run-a "$REPO_A" "$PATH_A"
  step_pr a "$REPO_A" "$(run_pr run-a)" "$SCRATCH/user.json"
  step_run run-b "$REPO_B" "$PATH_B"
  step_pr b "$REPO_B" "$(run_pr run-b)" "$SCRATCH/user.json"
  step_probe
  step_live_test
  step_rotation
  step_second_machine

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
