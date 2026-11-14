#!/usr/bin/env bash
# Check 313 — CG13's endpoint on a real machine, as ONE script YOU run.
#
# It settles the live half of the local endpoint (CG13 of
# docs/cli-output-console-graph/implementation-plan-graph.md, its CG13b and
# D1359) on a real machine, where the three things CG13b's own tests can only
# fake are real: a SIGKILL that leaves a file behind, an flock the kernel drops
# when the process dies, and a loopback listener that answers the handshake.
# The register's row and section are docs/gcp-live-checklist.md, "Check 313".
#
# What it reads, in the order D1359 fixes:
#
#   1. The registry file `<UserDir state>/fugaro/run/<id>.json` (CG13b): the
#      directory is mode 0700 and the file mode 0600, and the document carries
#      the id, the daemon's pid, the literal `127.0.0.1:<port>` the listener
#      bound, a 32-byte token and a 32-byte **nonce** as hex, and a start time.
#      The state directory is the one localcfg.UserDir resolves for DirState:
#      $XDG_STATE_HOME where it is set and ABSOLUTE, and ~/.local/state where it
#      is not, which is the answer on macOS too -- `${XDG_STATE_HOME:-...}` would
#      expand the unset variable to the empty string and the path would read
#      `/fugaro/run`.
#   2. **Liveness is a handshake plus a lock, never a pid.** The check performs
#      the handshake itself: GET http://<addr>/state with `Authorization: Bearer
#      <the file's token>`, and the answer must carry `X-Fugaro-Orchestrator`
#      equal to the id and `X-Fugaro-Nonce` equal to the file's nonce -- a port
#      another process took over, and a pid another process took over, both
#      answer without that nonce. The status is not part of the handshake
#      (endpoint.Probe compares the two headers and nothing else): the endpoint's
#      answer before its first tick is a 503 that carries both, deliberately, and
#      reading that as a dead daemon is the one mistake this rule exists to stop.
#      Then the `<id>.lock` beside the file must be
#      HELD (an exclusive flock taken here fails), which is what the daemon holds
#      for its life.
#   3. `fugaro graph --source local:<id> --json` answers while the daemon is
#      live: the local scope prefers the endpoint (CG13c, T5(a)), and this is the
#      read that goes through `endpoint.Probe`.
#   4. The crash: `kill -9` of the daemon's pid, the read again (it must answer
#      from the node or the cache and remove the dead file), the file gone, and
#      the lock free -- the kernel dropped it with the process, which is the
#      whole reason the lock and not the pid decides removability.
#   5. A live process that has taken the pid is never touched. A pid from a
#      previous boot is a number the kernel hands out again on its own schedule,
#      so this check does not wait for that lottery: it puts the dead daemon's
#      OWN file back, byte for byte, with the one field the reader must not
#      consult replaced by the pid of a live process this check starts, and reads
#      the scope once more. The reader must still judge it dead -- nothing
#      answers the handshake and the lock is free -- remove it, and leave that
#      live process running and unsignalled.
#
# **The daemon restarts itself if a supervisor owns it, and this check says so
# instead of pretending.** The installed daemon is a login unit (launchd's
# KeepAlive, systemd's `Restart=always` with `RestartSec=5`), so a SIGKILL of a
# supervised daemon is undone within about 5 to 10 seconds: a fresh process takes
# the lock and writes a fresh file at the same path. The crash half therefore
# wants a daemon YOU started (`fugaro orchestrator run` in a terminal), and the
# check guards the other case rather than misjudging it -- after the kill it
# compares the file it finds with the one it killed, and where a different file
# is there it says the daemon came back and reports the crash half as NOT
# MEASURED (exit 2) with the one line that fixes it: stop the unit, or start a
# daemon by hand.
#
# **The reboot is the one manual phase.** `--phase live` (the default) is
# everything above, taken before you reboot. Then you reboot, start a daemon
# again (`fugaro orchestrator run`), and run the same script with `--phase
# reboot`, which repeats every reading above and adds one: the registry file the
# new daemon wrote carries a **different nonce** than the one the first run's
# block printed (pass it as `--previous-nonce <value>`), which is the half of the
# nonce's meaning that no single start can show -- it is minted per start and
# never reused (D1359). A reboot also drops every lock and every listener of the
# previous boot, so the readings after it are of a fresh file that only the
# handshake and the lock decide.
#
# What it NEVER does. It signals exactly one process: the pid the registry file
# names, and only when its command line is the daemon's (`ps -o command=` carries
# `orchestrator` and `run`) -- a file naming a process that is not the daemon
# stops the run instead of being killed, which is the reading that matters more
# than any row below it. It writes exactly one file outside its own scratch
# directory: the copy of the dead daemon's own registry described in (5), at the
# same 0600 in the same 0700 directory, removed by the read that judges it dead
# and by this run's own teardown if that read did not -- and it writes it only
# where the crash half was measured, so a daemon that came back never has its
# file overwritten. It creates no cloud resource, spends nothing and touches no
# project: nothing here talks to GCP, and the only network call is the loopback
# handshake.
#
# Usage:      bash scripts/live-checks/check313.sh [--id <orchestrator id>] [--phase live|reboot]
#                                                 [--previous-nonce <hex>] [--fugaro-flags '<flags>'] [--dry-run]
#             --id is the orchestrator's id, which is the Fugaro project's name
#             (the daemon drives the project it is configured for). Leave it out
#             where the run directory holds exactly ONE registry file: that file
#             names the id. With more than one the check stops (exit 2) and lists
#             them rather than picking, because killing the wrong daemon is the
#             one thing this check must not do.
#             --phase live is everything before the reboot and is the default.
#             --phase reboot is what you run after rebooting and starting a
#             daemon again; it repeats every reading and compares the nonce with
#             --previous-nonce.
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--project myproject`, where the check runs outside a
#             checkout). It is split on whitespace, so a value with a space in it
#             cannot be passed.
#             --dry-run prints the plan and makes no call at all.
# Needs:      bash, mktemp, sed, grep, head, cut, python3, curl, ps and kill on
#             PATH (the lock is taken with python3's fcntl, because macOS ships
#             no `flock`), and a `fugaro` that has `graph` (a build without the
#             `ee` tag answers with a stub, which stops this check at its first
#             read). Nothing is installed and nothing is fetched; a missing tool
#             stops the run with one line naming it.
# Where:      your own machine (macOS or Linux; the daemon is those two, D1232),
#             against a daemon you can LOSE: the check SIGKILLs it, and it does
#             not come back by itself unless a login unit supervises it (in which
#             case the check says the crash half was not measured -- stop the
#             unit for that half, or start a daemon by hand). An id naming the
#             owner's own installation (`belong`) is refused before anything is
#             read.
# Costs:      nothing: one loopback handshake, one local read, two `fugaro`
#             commands and one `kill -9`. No model call, no run, no cloud call.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 313 RESULT.
#             Exit 0 when every fact held, 1 when one did not (a mode, a
#             handshake, a lock, a removal, a live process this check had to
#             leave alone -- each row says which), and 2 when the run could not
#             tell (a guard stopped it, no daemon is running, the pid is not the
#             daemon's, the daemon came back before the crash half's read, a tool
#             is missing, an internal error ended it).
#
# The endpoint's token is read from the registry file and is never printed: it
# travels into the handshake through a 0600 curl config file inside this run's
# 0700 scratch directory, because `ps` shows a process's argv to every user on
# the machine (the pattern Checks 91 and 361 use). The NONCE is printed, in the
# block's `registry-fields` row: it is the half of the pair that proves identity
# rather than admitting anything, and `--phase reboot` needs it on the command
# line to compare the next boot's with it.
#
# CHECK313_WAIT_SECONDS bounds the wait for the daemon's process to be gone
# after the SIGKILL (default 10, in steps of 0.2s), CHECK313_DECOY_SECONDS is how
# long the process that takes the dead file's pid lives (default 600) and
# CHECK313_CURL_SECONDS bounds the handshake (default 10). All three exist so
# this script's own test can run the same code in a second or two.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had. This check reads the
# endpoint's token out of the registry file and writes it into a 0600 file with
# printf, and a trace prints that line in full -- the token, into the log the
# owner would then paste back. So the trace is turned off here, BASH_XTRACEFD is
# unset so a trace redirected to a file this script knows nothing about cannot be
# reopened, and guard_xtrace refuses a shell that arrived tracing (the flag sits
# in SHELLOPTS/BASHOPTS then, and every child would start tracing on its own).
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptor rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set to yes by the_verdict once the reading has been judged, so the exit trap
# can tell a finished run from one that ended on an error of its own.
REACHED_END=no
# --help printed the usage itself: it is not a run, and it is read by the exit
# trap, which has to see both of these whatever path ended the run.
HELPED=no
DRY_RUN=""
USAGE="check313.sh [--id <orchestrator id>] [--phase live|reboot] [--previous-nonce <hex>] [--fugaro-flags '<flags>'] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something
# to paste back.
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

# rewrite_fact replaces the row a key already has, or writes it when there is
# none. The block prints the FIRST row a key carries, so a reading that turns out
# to be stale has to be replaced in place: appending a second one would leave the
# stale row in the block and hide the correction.
rewrite_fact() { # rewrite_fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  python3 - "$FACTS_FILE" "$1" "$2" "$3" <<'PY'
import sys

path, key, verdict, detail = sys.argv[1:5]
row = "%s|%s|%s" % (key, verdict, detail)
with open(path, encoding="utf-8") as fh:
    rows = fh.read().split("\n")
out, replaced = [], False
for line in rows:
    if not replaced and line.startswith(key + "|"):
        out.append(row)
        replaced = True
    else:
        out.append(line)
if not replaced:
    out.append(row)
with open(path, "w", encoding="utf-8") as fh:
    fh.write("\n".join(out))
PY
}

has_fact() { # has_fact KEY
  [ -n "$FACTS_FILE" ] && grep -q "^$1|" "$FACTS_FILE" 2>/dev/null
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as an answer about the endpoint for a run that
# measured nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the endpoint"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 313 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 313 needs bash"
  need mktemp
  need sed "the block's rows are read back out of this run's own fact file with it"
  need grep
  need head
  need cut
  need python3 "the registry document is JSON, and the lock is taken with its fcntl because macOS ships no flock"
  need curl "the handshake is one loopback request to the endpoint"
  need ps "the pid is refused unless its command line is the daemon's, and ps is what says so"
  need kill "the crash half is a SIGKILL of the daemon's pid"
  need sleep "the crash half waits for the process to be gone"
  need cp
  need "$FUGARO_BIN" "the scope is read with it, and a build without the ee tag answers with a stub"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk313-mine"
SCRATCH_REMOVED=no
TOKEN_FILE=""
TOKEN_FILE_REMOVED=yes
CURL_CONF=""
CURL_CONF_REMOVED=yes
DECOY_PID=""
PLANTED=no
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk313.XXXXXXXX") || die "Check 313 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 313 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_curl_conf
  remove_token_file
  remove_decoy
  remove_planted
  remove_scratch
}

# The credential and the file built from it go first, and their outcomes are
# recorded: a run that stopped between the read and the handshake would otherwise
# leave a live token on disk in a directory the trap is about to remove. Guarded,
# because this runs from the exit trap, where a failing `rm` under `set -e` would
# end the shell before the block is printed.
remove_token_file() {
  [ -n "$TOKEN_FILE" ] || return 0
  rm -f "$TOKEN_FILE" 2>/dev/null || true
  if [ -e "$TOKEN_FILE" ]; then TOKEN_FILE_REMOVED=no; else TOKEN_FILE_REMOVED=yes; fi
  return 0
}
remove_curl_conf() {
  [ -n "$CURL_CONF" ] || return 0
  rm -f "$CURL_CONF" 2>/dev/null || true
  if [ -e "$CURL_CONF" ]; then CURL_CONF_REMOVED=no; else CURL_CONF_REMOVED=yes; fi
  return 0
}

# The process this check started to hold the dead file's pid is this run's own,
# and it goes in the teardown: the row above it is that the READER left it alone,
# which the check reads before this.
remove_decoy() {
  [ -n "$DECOY_PID" ] || return 0
  if kill -0 "$DECOY_PID" 2>/dev/null; then
    kill "$DECOY_PID" 2>/dev/null || true
    wait "$DECOY_PID" 2>/dev/null || true
  fi
  return 0
}

# The planted copy is the one file this check writes outside its scratch
# directory. A read that judged it dead removes it; this is the run ending
# without one, and the teardown says so either way. Only this check's own plant is
# removed: the pid and the nonce it wrote are read back first, so a daemon a
# supervisor started again between the plant and the end of the run keeps the
# registry file it wrote.
remove_planted() {
  [ "$PLANTED" = yes ] || return 0
  [ -e "$REG_PATH" ] || return 0
  if [ "$(file_now)" = "$DECOY_PID $NONCE" ]; then
    rm -f "$REG_PATH" 2>/dev/null || true
  fi
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
# guard_xtrace: a shell that arrived tracing would print the token this check
# reads out of the registry file, so the run is refused rather than read: the
# trace is already off (the top of this file), and what is left to catch is the
# shell that started with it, whose flag sits in SHELLOPTS/BASHOPTS and would be
# inherited by every child.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the endpoint's token -- and the log you would then paste. Run it without -x"
    stop "the shell this check was started from is tracing (-x): the trace would print the endpoint's token. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the token read out of the registry file"
}

# guard_belong: the id is the Fugaro project's name, and the installation named
# `belong` is the owner's own. This check kills a daemon and writes into a run
# directory, so a mistyped id pointing at the owner's own installation would be
# work nobody asked for; the name is refused before anything is read, and the test
# is a substring one on purpose, so a name that merely contains it is refused too
# and the message says which one matched.
guard_belong() {
  case "$(printf '%s' "$ID" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact belong FAIL "'$ID' names the owner's own installation: Check 313 kills a daemon and writes into its run directory, and it is never pointed at 'belong'"
    stop "the id '$ID' matches the installation 'belong', which this check must never touch"
    ;;
  esac
  fact belong PASS "the id ($ID) does not name the owner's own installation (what was typed contains no 'belong')"
}

# guard_id: the id has to be one a `local:` scope names as well, because the two
# reads this check hangs its removal facts on are `fugaro graph --source
# local:<id>`. The pattern is the graph document's own (CG1's ParseScope, H1),
# and CG13c names the limit rather than widening it: a project whose name carries
# an underscore, or is longer than 40 characters, is one no local scope names, so
# this check cannot take its two reads and says that instead of failing.
guard_id() {
  if ! printf '%s' "$ID" | grep -Eq '^[a-z0-9][a-z0-9-]{0,39}$'; then
    fact id FAIL "$ID is not an id a local: scope names (the graph document's own pattern: one lower-case letter or digit, then up to 39 more of those or a hyphen). CG13c names this limit rather than widening it: an id the daemon drives but no local scope names is read from its node and its cache exactly as a machine with no daemon reads it, so there is no read here to hang the crash half on"
    stop "the id '$ID' is not one a local: scope names, so this check cannot make the two reads it measures"
  fi
  fact id PASS "$ID is the orchestrator id this run reads, and it is one a local: scope names"
}

# ------------------------------------------------------ where the file lives
# resolve_state_dir mirrors localcfg.UserDir for DirState, which is the rule the
# daemon places its registry by: $XDG_STATE_HOME where it is set to an ABSOLUTE
# path, and $HOME/.local/state where it is not (the XDG spec ignores a relative
# value, and UserDir ignores it in the same words). macOS is the second case --
# and the reason the plan's row spells the expansion out, because
# `${XDG_STATE_HOME:-$HOME/.local/state}` would expand an unset variable to the
# empty string and the path would read `/fugaro/run`.
STATE_BASE=""
RUN_DIR=""
resolve_state_dir() {
  local xdg=${XDG_STATE_HOME:-}
  case "$xdg" in
  "") ;;
  /*) STATE_BASE=$xdg ;;
  *) say "XDG_STATE_HOME is set to '$xdg', which is not absolute: the XDG rule ignores such a value and localcfg.UserDir falls back to \$HOME, so this check does the same. fugaro is given the same environment, so the two agree on the directory either way" ;;
  esac
  if [ -z "$STATE_BASE" ]; then
    local home=${HOME:-${USERPROFILE:-}}
    [ -n "$home" ] || die "Check 313 cannot place the state directory: neither XDG_STATE_HOME nor HOME is set. usage: $USAGE"
    case "$home" in
    /*) ;;
    *) die "Check 313 cannot place the state directory: HOME is '$home', which is not absolute. usage: $USAGE" ;;
    esac
    STATE_BASE="$home/.local/state"
  fi
  RUN_DIR="$STATE_BASE/fugaro/run"
}

# The id, when it was not typed: the run directory holds one file per daemon that
# serves an endpoint on this machine, so exactly one file names the id. More than
# one is a stop rather than a pick -- the check kills the daemon it names.
derive_id() {
  [ -n "$ID" ] && return 0
  local files=() f
  if [ -d "$RUN_DIR" ]; then
    for f in "$RUN_DIR"/*.json; do
      [ -e "$f" ] || continue
      files+=("${f##*/}")
    done
  fi
  case ${#files[@]} in
  0)
    fact id FAIL "no daemon serves a local endpoint on this machine: $RUN_DIR holds no registry file, so there is nothing for this check to read. Start one ('fugaro orchestrator run', which drives the project your config names) and run this check again"
    stop "no local endpoint is running: $RUN_DIR holds no registry file. Start a daemon and run this check again"
    ;;
  1)
    ID=${files[0]%.json}
    OWN_ID=yes
    say "the run directory holds one registry file, so the id is taken from it: $ID"
    ;;
  *)
    fact id FAIL "$RUN_DIR holds ${#files[@]} registry files ($(printf '%s ' "${files[@]%.json}")), so this check is not told which daemon to read and kill. Name one with --id"
    stop "more than one registry file: name the daemon with --id"
    ;;
  esac
}

# ------------------------------------------------------------- the registry file
REG_PATH=""
# The non-secret fields are read out as one `key=value` line each and the TOKEN is
# written into a 0600 file of its own, so it never becomes a line of this script's
# output by accident. The mode and the directory's mode come from lstat/stat here
# rather than from the shell, because a symlink and a file are two different
# readings and only a stat tells them apart.
write_registry_reader() {
  cat >"$SCRATCH/registry_reader.py" <<'PY'
import json, os, re, stat, sys

path, token_out = sys.argv[1], sys.argv[2]
HEX64 = re.compile(r"\A[0-9a-f]{64}\Z")


def answer(key, value):
    print("%s=%s" % (key, value))


try:
    st = os.lstat(path)
except OSError as e:
    answer("exists", 0)
    answer("error", str(e))
    sys.exit(0)
answer("exists", 1)
answer("regular", 1 if stat.S_ISREG(st.st_mode) else 0)
answer("link", 1 if stat.S_ISLNK(st.st_mode) else 0)
answer("mode", "%04o" % (st.st_mode & 0o777))
answer("uid", st.st_uid)
d = os.stat(os.path.dirname(path))
answer("dirmode", "%04o" % (d.st_mode & 0o777))
try:
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, encoding="utf-8") as fh:
        doc = json.load(fh)
except Exception as e:
    answer("doc", "unreadable")
    answer("error", str(e))
    sys.exit(0)
if not isinstance(doc, dict):
    answer("doc", "notanobject")
    sys.exit(0)
answer("doc", "read")
answer("id", doc.get("id"))
answer("pid", doc.get("pid"))
addr = doc.get("addr") or ""
answer("addr", addr)
host, _, port = str(addr).rpartition(":")
answer("host", host)
answer("port", port)
started = doc.get("started_at")
answer("started", started if isinstance(started, str) else "")
token = doc.get("token") or ""
nonce = doc.get("nonce") or ""
answer("token_hex", 1 if HEX64.match(token) else 0)
answer("nonce", nonce)
answer("nonce_hex", 1 if HEX64.match(nonce) else 0)
answer("same", 0 if (not nonce or nonce == token) else 1)
if token:
    out = os.open(token_out, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(out, "w", encoding="utf-8") as fh:
        fh.write(token)
PY
}

REG_OUT=""
read_registry() {
  REG_PATH="$RUN_DIR/$ID.json"
  TOKEN_FILE="$SCRATCH/token"
  write_registry_reader
  REG_OUT=$(python3 "$SCRATCH/registry_reader.py" "$REG_PATH" "$TOKEN_FILE" 2>"$SCRATCH/reader.err") ||
    stop "the registry file at $REG_PATH could not be read as the document this release writes ($(first_line "$SCRATCH/reader.err")). Nothing is concluded about the endpoint"
}

field() { printf '%s\n' "$REG_OUT" | sed -n "s/^$1=//p" | head -1; }

judge_registry() {
  if [ "$(field exists)" != 1 ]; then
    fact registry-file FAIL "$REG_PATH does not exist, so no daemon serves a local endpoint for $ID on this machine. Start one ('fugaro orchestrator run') and run this check again"
    stop "no registry file at $REG_PATH: start a daemon and run this check again"
  fi
  if [ "$(field link)" = 1 ] || [ "$(field regular)" != 1 ]; then
    fact registry-file FAIL "$REG_PATH is not a regular file (or it is a symlink), and the daemon writes a regular file the reader opens with O_NOFOLLOW: a link planted at that path is refused rather than followed (CG13b)"
  elif [ "$(field mode)" = 0600 ]; then
    fact registry-file PASS "$REG_PATH is a regular file, mode 0600: the mode is what keeps the token a secret of this user's, because the loopback port is reachable by every process on the machine"
  else
    fact registry-file FAIL "$REG_PATH is mode $(field mode) and the endpoint writes 0600: the token would be readable by every user on the machine (CG13b). Stop the daemon and start it again -- it rewrites the file and its mode at its next start"
  fi
  if [ "$(field dirmode)" = 0700 ]; then
    fact state-dir PASS "$RUN_DIR is mode 0700: it is the state directory localcfg.UserDir resolves for DirState ($STATE_BASE), and its mode is what keeps the file's name and its existence from every other user"
  else
    fact state-dir FAIL "$RUN_DIR is mode $(field dirmode) and the endpoint makes it 0700: a directory another user can list is one whose registry file another user can see and read (CG13b). The daemon chmods it at its next start"
  fi

  if [ "$(field doc)" != read ]; then
    fact registry-fields FAIL "$REG_PATH is there and is not the document this release writes (the reader answered '$(field doc)': $(field error)). CG13b refuses such a file rather than half-reading it"
    stop "the registry file is not a document this release reads, so no field of it can be judged"
  fi
  local problems=() bad_addr=no
  [ "$(field id)" = "$ID" ] || problems+=("it names id '$(field id)' and $ID was asked for")
  case "$(field pid)" in
  '' | *[!0-9]*) problems+=("its pid is '$(field pid)', which is not a positive number") ;;
  0) problems+=("its pid is 0") ;;
  esac
  if [ "$(field host)" != 127.0.0.1 ]; then
    problems+=("its address '$(field addr)' is not the loopback literal this endpoint binds")
    bad_addr=yes
  fi
  case "$(field port)" in
  '' | *[!0-9]*) problems+=("its address '$(field addr)' carries no port"); bad_addr=yes ;;
  *)
    if [ "$(field port)" -le 0 ] || [ "$(field port)" -gt 65535 ]; then
      problems+=("its address '$(field addr)' carries a port outside 1-65535")
      bad_addr=yes
    fi
    ;;
  esac
  [ "$(field token_hex)" = 1 ] || problems+=("its token is not 32 bytes of hex")
  [ "$(field nonce_hex)" = 1 ] || problems+=("its nonce is not 32 bytes of hex")
  [ "$(field same)" = 1 ] || problems+=("its nonce is the token (or it carries none), so it proves no identity of its own")
  [ -n "$(field started)" ] || problems+=("it carries no start time")
  if [ ${#problems[@]} -eq 0 ]; then
    NONCE=$(field nonce)
    fact registry-fields PASS "the document carries id $ID, pid $(field pid), the literal address $(field addr), a 32-byte token and a 32-byte nonce as hex, and a start time. The nonce is $(field nonce) -- printed on purpose: it proves identity rather than admitting anything, and --phase reboot compares the next boot's with it"
  else
    local list
    list=$(printf '%s; ' "${problems[@]}"); list=${list%; }
    fact registry-fields FAIL "the document is not the one CG13b writes: $list"
    # The address decides where the token would go, so it is judged before any
    # request: the reader this check mirrors refuses such a document in validate
    # and makes no call at all, and a check that handshook anyway would hand the
    # file's bearer token to whatever address the file names -- the file is a
    # document on disk, and nothing in it is this run's to trust.
    if [ "$bad_addr" = yes ]; then
      stop "the registry file names '$(field addr)', which is not the loopback address this endpoint binds: endpoint.Probe refuses such a document before making any request, and this check will not send the file's token to an address the file chose"
    fi
  fi
}

# --------------------------------------------------------------- the handshake
# The token goes into the curl config file, never onto the command line: `ps`
# shows a process's argv to every user on the machine. The file is 0600 inside
# this run's 0700 scratch directory and the exit trap removes it.
handshake() {
  if [ ! -s "$TOKEN_FILE" ]; then
    fact handshake FAIL "the registry file carries no token, so the endpoint cannot be asked to prove it is the process that wrote the file"
    return 0
  fi
  CURL_CONF="$SCRATCH/curl.conf"
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$(cat "$TOKEN_FILE")" >"$CURL_CONF") ||
    stop "the handshake's credential file could not be written"
  chmod 600 "$CURL_CONF" || stop "the handshake's credential file could not be made 0600"
  local url="http://$(field addr)/state" code=""
  if ! code=$(curl -sS --max-time "$CURL_SECONDS" -o "$SCRATCH/state.json" -D "$SCRATCH/state.headers" \
    -w '%{http_code}' --config "$CURL_CONF" "$url" 2>"$SCRATCH/curl.err"); then
    fact handshake FAIL "nothing answered the handshake at $url: no endpoint is listening at the address the file names, which is what a daemon that was killed leaves behind ($(first_line "$SCRATCH/curl.err"))"
    return 0
  fi
  local got_id got_nonce nonce
  got_id=$(sed -n 's/^[Xx]-[Ff]ugaro-[Oo]rchestrator: *\([^ ]*\).*/\1/p' "$SCRATCH/state.headers" | head -1)
  got_nonce=$(sed -n 's/^[Xx]-[Ff]ugaro-[Nn]once: *\([^ ]*\).*/\1/p' "$SCRATCH/state.headers" | head -1)
  nonce=$(field nonce)
  if [ "$got_id" = "$ID" ] && [ -n "$got_nonce" ] && [ "$got_nonce" = "$nonce" ]; then
    fact handshake PASS "the endpoint at $(field addr) read back the id ($got_id) and the nonce the file carries: the process behind that address is the one that wrote the file, which a port another process took over cannot fake. The two identity headers are the whole handshake and the status is not part of it (endpoint.Probe compares these two and nothing else), so the HTTP $code here is not a reading of the daemon's state -- a daemon answering 503 before its first tick is live and carries both headers (CG13b)"
  else
    fact handshake FAIL "the handshake at $(field addr) answered HTTP $code with X-Fugaro-Orchestrator '$got_id' and X-Fugaro-Nonce '${got_nonce:-<none>}', and the file it must match carries id $ID and nonce ${nonce:-<none>}. A daemon whose listener is gone, and a port another process has taken, both read this way; a refusal carries no identity header at all, so this is not a daemon that had simply not ticked yet (CG13b)"
  fi
}

# ------------------------------------------------------------------- the lock
# The lock is taken with python3's fcntl and never with `flock`: macOS ships no
# `flock` binary, and this check runs on the owner's Mac. An exclusive
# non-blocking lock that FAILS is the daemon holding it for its life; the kernel
# drops it when the process dies, however it dies, which is why it and not the
# pid decides whether a file may be removed.
LOCK_STATE=""
lock_state() {
  local path="$RUN_DIR/$ID.lock"
  LOCK_STATE=$(python3 - "$path" <<'PY'
import fcntl, os, sys

path = sys.argv[1]
try:
    fd = os.open(path, os.O_RDWR)
except FileNotFoundError:
    print("missing")
    sys.exit(0)
except OSError as e:
    print("error %s" % e)
    sys.exit(0)
try:
    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
except OSError:
    print("held")
    sys.exit(0)
print("free")
PY
  )
}

judge_lock_held() {
  lock_state
  case "$LOCK_STATE" in
  held)
    fact lock-held PASS "$RUN_DIR/$ID.lock is held exclusively, and the daemon holds it for its life: the kernel drops it when the process dies, however it dies, which is the half of the liveness rule that a pid can never answer (D1359)"
    ;;
  free)
    fact lock-held FAIL "$RUN_DIR/$ID.lock is free while the registry file is there, and nothing holds it: the file is a dead daemon's leftover rather than a live endpoint's. Read the scope once (fugaro graph --source local:$ID, which is the read that removes it) and run this check again against a daemon that is running"
    ;;
  missing)
    fact lock-held FAIL "$RUN_DIR/$ID.lock does not exist, and the daemon takes that lock before it writes its registry file (CG13b): a file whose lock was never taken is not this release's"
    ;;
  *)
    fact lock-held FAIL "the lock at $RUN_DIR/$ID.lock could not be judged: $LOCK_STATE"
    ;;
  esac
}

# ------------------------------------------------------------- the daemon's pid
# `ps -o command= -ww` is the whole command line on both macOS and Linux. The pid
# is used for exactly one thing in this check -- the SIGKILL below -- and it is
# only ever signalled when the command line says the process is the daemon. A pid
# a reader would have trusted is the very thing this check exists to refuse, and
# the check refuses it in the same breath: a file naming a process that is not
# the daemon stops the run rather than killing a stranger.
DAEMON_PID=""
daemon_pid_is_the_daemon() {
  local pid
  pid=$(field pid)
  case "$pid" in
  '' | *[!0-9]* | 0)
    fact daemon-pid FAIL "the registry file names no usable pid ('$(field pid)'), so this check cannot tell which process to kill, and it never kills one it cannot name"
    stop "the registry file names no usable pid"
    ;;
  esac
  if ! kill -0 "$pid" 2>/dev/null; then
    fact daemon-pid FAIL "pid $pid is not running, and an endpoint answered the handshake: the process that wrote the file is gone (or the pid in it is not the daemon's). Nothing is signalled, and nothing is concluded"
    stop "pid $pid is not running, so this check has nothing to kill and nothing to conclude"
  fi
  local cmd
  cmd=$(ps -o command= -ww -p "$pid" 2>/dev/null || true)
  if [ -z "$cmd" ]; then
    fact daemon-pid FAIL "the command line of pid $pid could not be read, so this check cannot tell whether it is the daemon, and it signals nothing it cannot name"
    stop "the command line of pid $pid could not be read"
  fi
  case "$cmd" in
  *orchestrator*run*)
    DAEMON_PID=$pid
    fact daemon-pid PASS "pid $pid is running and its command line is the daemon's ($(printf '%s' "$cmd" | cut -c1-160)): this is the one process this check signals, and it is chosen by the command line and not by the pid alone"
    ;;
  *)
    fact daemon-pid FAIL "pid $pid is running, and its command line is not the daemon's ($(printf '%s' "$cmd" | cut -c1-160)): a pid another process has taken is never signalled, and this run stops here rather than killing a stranger. Start the daemon again and run this check once more"
    stop "pid $pid is not the daemon: nothing is signalled"
    ;;
  esac
}

# ------------------------------------------------------------------ the read
# `fugaro graph --source local:<id>` is the read that goes through
# endpoint.Probe: the local scope prefers a live endpoint, then the node, then
# the daemon's cached brief (CG13c, T5(a)). Both of the facts that hang on it are
# about the reader -- that it answers while the endpoint is live, and that after
# the crash it still answers AND removes the dead file.
read_scope() { # read_scope LABEL -> SCOPE_STATUS, SCOPE_OUT, SCOPE_ERR
  local label=$1
  SCOPE_STATUS=0
  SCOPE_OUT=$("$FUGARO_BIN" graph --source "local:$ID" --json \
    ${FUGARO_FLAGS[@]+"${FUGARO_FLAGS[@]}"} 2>"$SCRATCH/$label.err") || SCOPE_STATUS=$?
  SCOPE_ERR=$(first_line "$SCRATCH/$label.err")
}

scope_state() {
  printf '%s' "$SCOPE_OUT" | python3 -c 'import json,sys
try:
    doc = json.load(sys.stdin)
except Exception:
    print("unreadable")
    sys.exit(0)
for s in doc.get("sources") or []:
    if s.get("source") == "local:" + sys.argv[1]:
        print("source %s over %d nodes" % (s.get("state") or "?", len(doc.get("nodes") or [])))
        sys.exit(0)
print("no local source in the document")
' "$ID" 2>/dev/null || echo "unreadable"
}

judge_graph_live() {
  read_scope live
  if [ "$SCOPE_STATUS" = 0 ]; then
    fact graph-live PASS "'fugaro graph --source local:$ID --json' answered while the endpoint was live ($(scope_state)), and that read is endpoint.Probe's: the local scope prefers the handshake-proven endpoint over the node and the cache (CG13c, T5(a))"
  else
    fact graph-live FAIL "'fugaro graph --source local:$ID --json' exited $SCOPE_STATUS while a live endpoint answered the handshake, so this read is not the one CG13c describes (the local scope prefers the live endpoint). Its own line: ${SCOPE_ERR:-<none>}"
  fi
}

# ------------------------------------------------------------------- the crash
# The wait after the SIGKILL is bounded and short: a process the kernel has killed
# is gone at once, and a pid that is still there a second later means the signal
# did not land on the daemon (a zombie held by a parent, a pid the file named
# wrongly), which is a reading and not something to wait out.
CRASH_HALF=measured
CRASH_HALF_REASON=""
crash() {
  local pid waited=0 cmd
  pid=$DAEMON_PID
  kill -9 "$pid" 2>/dev/null || true
  while [ "$waited" -lt "$WAIT_SECONDS" ]; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.2
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    cmd=$(ps -o command= -ww -p "$pid" 2>/dev/null || true)
    fact crash FAIL "pid $pid is still there ${WAIT_SECONDS}x0.2s after the SIGKILL ($(printf '%s' "$cmd" | cut -c1-120)): the endpoint's process is not gone, so the crash half measured nothing"
    stop "the daemon did not die from the SIGKILL, so the crash half measured nothing"
  fi
  fact crash PASS "pid $pid was SIGKILLed and is gone: the daemon got no chance to clean up, which is exactly the state the reader has to judge"
}

# What the file at that path is NOW, as `pid nonce` on one line, and empty when
# there is none. This is what tells the crash half's own reading apart from a
# daemon a supervisor started again: a restart writes a fresh file with a fresh
# pid and a fresh nonce, and this check must not judge -- or overwrite -- that one
# as if it were the dead daemon's.
file_now() {
  python3 - "$REG_PATH" <<'PY'
import json, sys

try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        doc = json.load(fh)
except Exception:
    print("")
    raise SystemExit(0)
print("%s %s" % (doc.get("pid"), doc.get("nonce")))
PY
}

# crash_half_restarted PID records that a daemon a supervisor started again holds
# the path. The window the crash half needs is 5 to 10 seconds wide (RestartSec=5,
# launchd's KeepAlive), so the restart can land anywhere in it -- including
# between two rows of this check, after the crash-half row was already written.
# The row is therefore replaced rather than appended to.
crash_half_restarted() { # crash_half_restarted PID
  local pid=${1:-?}
  CRASH_HALF=restarted
  CRASH_HALF_REASON="the registry file at $REG_PATH belongs to a NEW daemon (pid $pid) a supervisor started again after the SIGKILL of pid $DAEMON_PID. The installed daemon is a login unit (launchd's KeepAlive, systemd's Restart=always with RestartSec=5, so 5 to 10 seconds), and the only window in which the crash half can be read is that one: stop the unit (launchctl bootout gui/$UID/<the daemon's label>, or systemctl --user stop <the unit>) or start a daemon by hand with 'fugaro orchestrator run', and run this check again. This is a stop and not a failure: the removal it would read is this check's other half, and it was not measured"
  rewrite_fact crash-half INFO "$CRASH_HALF_REASON"
}

# recheck_the_crash_half re-reads the path before a step that reads or writes it,
# and it never fails a `set -e` shell: callers read CRASH_HALF. The graph read
# that follows the crash was made around the same time as this look, so its own
# PASS would claim a fallback to the node and the cache that this run cannot show
# either -- it is replaced too. The callers that find a restart later than that
# read leave it standing.
recheck_the_crash_half() {
  [ "$CRASH_HALF" = measured ] || return 0
  local now
  now=$(file_now)
  case "$now" in
  "" | "$DAEMON_PID "*) return 0 ;;
  esac
  crash_half_restarted "${now%% *}"
  if has_fact graph-after-crash; then
    rewrite_fact graph-after-crash INFO "not measured: a daemon a supervisor started again took the path while this read ran, so the file it read was not the dead daemon's own"
  fi
  return 0
}

# The daemon of a login unit comes back by itself, and the readings after a crash
# are about a file that no live process owns: where a supervisor has written a new
# one, the crash half is NOT MEASURED rather than failed, and the run says which
# one thing fixes it. The live half above stands either way.
after_crash() {
  local now
  now=$(file_now)
  if [ -n "$now" ] && [ "${now%% *}" != "$DAEMON_PID" ]; then
    crash_half_restarted "${now%% *}"
    return 0
  fi
  if [ -z "$now" ]; then
    fact crash-half INFO "measured: no registry file is at $REG_PATH after the SIGKILL, so the rows below read a path nothing owns"
  else
    fact crash-half INFO "measured: the file at $REG_PATH is still the dead daemon's own (pid $DAEMON_PID, nonce $NONCE), so the crash half below is its reading and nothing else wrote it in the meantime"
  fi
}

judge_graph_after_crash() {
  if [ "$CRASH_HALF" = restarted ]; then
    fact graph-after-crash INFO "not measured: the daemon came back before this read, so what it would have read is the live endpoint again rather than the fallback to the node and the cache"
    return 0
  fi
  read_scope crash
  if [ "$SCOPE_STATUS" = 0 ]; then
    fact graph-after-crash PASS "'fugaro graph --source local:$ID --json' answered after the SIGKILL ($(scope_state)): the read fell back from the endpoint that is now nothing to the node and the cache, which is what T5(a) says it does"
  else
    fact graph-after-crash FAIL "'fugaro graph --source local:$ID --json' exited $SCOPE_STATUS after the SIGKILL, so the reader did not fall back to the node and the cache. Its own line: ${SCOPE_ERR:-<none>}"
  fi
}

judge_dead_file() {
  recheck_the_crash_half
  if [ "$CRASH_HALF" = restarted ]; then
    fact dead-file INFO "not measured: the file at $REG_PATH is the new daemon's, and the dead one's was removed or replaced before this check could tell which"
    return 0
  fi
  if [ -e "$REG_PATH" ]; then
    fact dead-file FAIL "$REG_PATH is STILL THERE after the read that found it dead. CG13b removes a dead file only in the reader that read it and found it dead -- nothing sweeps the directory -- so a file that survives its reader is one every later read has to judge again"
  else
    fact dead-file PASS "$REG_PATH is gone, removed by the read that found it dead and by no other process: the daemon never removes another process's file, and nothing sweeps the directory"
  fi
}

judge_lock_free() {
  recheck_the_crash_half
  if [ "$CRASH_HALF" = restarted ]; then
    fact lock-free INFO "not measured: the lock is the new daemon's now, so whether the kernel dropped the dead one's is no longer readable here"
    return 0
  fi
  lock_state
  case "$LOCK_STATE" in
  free)
    fact lock-free PASS "$RUN_DIR/$ID.lock is free now that the process is gone: the kernel dropped the flock at the SIGKILL, which is what makes the lock a liveness answer a pid can never give"
    ;;
  held)
    fact lock-free FAIL "$RUN_DIR/$ID.lock is still held after the daemon was SIGKILLed, so something else holds it and the file that was judged dead was not the only writer"
    ;;
  *)
    fact lock-free FAIL "the lock at $RUN_DIR/$ID.lock could not be judged after the crash: $LOCK_STATE"
    ;;
  esac
}

# ---------------------------------------------------------------- the pid reuse
# A pid from a previous boot is a number the kernel hands out again on its own
# schedule, so waiting for that lottery would make this row a matter of luck: the
# check reproduces the state the wait is for. It puts the dead daemon's own file
# back, byte for byte, with the ONE field the reader must not consult replaced by
# the pid of a process this check starts -- which is exactly what a reboot leaves
# behind, a file naming a pid that now belongs to somebody else. The reader must
# still judge it dead (nothing answers the handshake and the lock is free),
# remove it, and leave that process running.
# Returns 0 when the plant is there, 1 when a daemon a supervisor started again
# holds the path, and 2 when the dead daemon's own file is still there because the
# read that judged it dead did not remove it. Nothing is ever written over a file
# that is on the path: the file is opened with O_EXCL, because a supervisor's
# restart can land at any point in the 5-to-10-second window and a cp that lands
# after one would overwrite a live daemon's registry with a dead daemon's. A link
# would say the same but needs both paths on one filesystem, and the scratch
# directory is TMPDIR's.
plant_dead_file_with_live_pid() {
  cat >"$SCRATCH/replant.py" <<'PY'
import json, os, sys

src, path, pid = sys.argv[1], sys.argv[2], int(sys.argv[3])
with open(src, encoding="utf-8") as fh:
    doc = json.load(fh)
doc["pid"] = pid
try:
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
except FileExistsError:
    raise SystemExit(3)
with os.fdopen(fd, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
  # The bytes that go back are the dead daemon's own, taken before the read that
  # removed the file: the re-planted document is the daemon's, not one this check
  # invented -- in the same 0700 directory and at the same 0600 the daemon wrote
  # it.
  local rc=0
  python3 "$SCRATCH/replant.py" "$SAVED_REG" "$REG_PATH" "$DECOY_PID" || rc=$?
  case "$rc" in
  0) PLANTED=yes ;;
  3)
    case "$(file_now)" in
    "$DAEMON_PID $NONCE") return 2 ;;
    *)
      crash_half_restarted "$(file_now | cut -d' ' -f1)"
      return 1
      ;;
    esac
    ;;
  *) stop "the dead daemon's registry file could not be put back for the pid-reuse row (python3 exited $rc)" ;;
  esac
  return 0
}

judge_pid_reuse() {
  recheck_the_crash_half
  if [ "$CRASH_HALF" = restarted ]; then
    fact pid-reuse INFO "not measured: a daemon is serving this id again, and writing this check's own file over a live daemon's registry is the one write it must not make"
    return 0
  fi
  sleep "$DECOY_SECONDS" &
  DECOY_PID=$!
  local cmd_before cmd_after alive
  cmd_before=$(ps -o command= -ww -p "$DECOY_PID" 2>/dev/null || true)
  if [ -z "$cmd_before" ]; then
    fact pid-reuse FAIL "the process this check starts to hold the dead file's pid (pid $DECOY_PID) did not come up, so the row measured nothing"
    return 0
  fi
  local planted=0
  plant_dead_file_with_live_pid || planted=$?
  case "$planted" in
  0) ;;
  1)
    fact pid-reuse INFO "not measured: a daemon a supervisor started again wrote its own registry file at $REG_PATH, so this check planted nothing over it and the removal this row reads was not made"
    return 0
    ;;
  2)
    fact pid-reuse FAIL "the dead daemon's own file is STILL at $REG_PATH, so this row could not be measured: it is the file the read that judged it dead had to remove, and nothing may be written over a file that is already there"
    return 0
    ;;
  esac
  read_scope reuse
  if kill -0 "$DECOY_PID" 2>/dev/null; then alive=yes; else alive=no; fi
  cmd_after=$(ps -o command= -ww -p "$DECOY_PID" 2>/dev/null || true)
  if [ "$alive" = no ]; then
    fact pid-reuse FAIL "the process holding the dead file's pid (pid $DECOY_PID, this check's own) is gone: something signalled a process whose only connection to the dead daemon is a number in a file, which is the very thing D1359 forbids"
  elif [ "$cmd_before" != "$cmd_after" ]; then
    fact pid-reuse FAIL "the process holding the dead file's pid (pid $DECOY_PID) is running another command than it was ($(printf '%s' "$cmd_after" | cut -c1-80)): it was replaced rather than left alone"
  elif [ -e "$REG_PATH" ] && [ "$(file_now)" = "$DECOY_PID $NONCE" ]; then
    fact pid-reuse FAIL "the registry file naming a LIVE pid ($DECOY_PID) survived the read: the file is judged by the handshake and the lock, and neither of them answered for it, so the reader that read it had to remove it"
  elif [ -e "$REG_PATH" ]; then
    # The path changed while this read ran: a supervisor's restart writes its own
    # file, so what the reader judged is not what this check planted and the
    # removal this row reads was not made. That daemon's file is left where it is.
    crash_half_restarted "$(file_now | cut -d' ' -f1)"
    fact pid-reuse INFO "not measured: a daemon a supervisor started again wrote its own registry file at $REG_PATH while this read ran, so the file the reader judged is not the one this check planted, and the daemon's own file is left where it is"
  elif [ "$SCOPE_STATUS" != 0 ]; then
    fact pid-reuse FAIL "the read of the file whose pid is a live process's exited $SCOPE_STATUS: the scope must read the node or the cache whatever the file's pid says. Its own line: ${SCOPE_ERR:-<none>}"
  else
    fact pid-reuse PASS "the file naming a LIVE pid ($DECOY_PID: $(printf '%s' "$cmd_after" | cut -c1-60)) was judged dead by the handshake and the lock, removed by the reader that read it, and that process was never signalled and is still running: the pid decides nothing here"
  fi
}

# The nonce across the reboot: the one half of the nonce's meaning a single start
# cannot show. It is not a criterion unless you passed --previous-nonce, because a
# run that has no previous boot to compare with has not measured it -- and the row
# says so rather than reading as a pass.
judge_reboot_nonce() {
  if [ "$PHASE" != reboot ]; then
    fact reboot-nonce INFO "this is the pre-reboot phase, so no previous boot's nonce is compared. Reboot, start a daemon again, and run this script with --phase reboot --previous-nonce $NONCE to settle that the nonce is minted per start"
    return 0
  fi
  if [ -z "$PREVIOUS_NONCE" ]; then
    fact reboot-nonce INFO "no --previous-nonce was given, so whether this boot's nonce differs from the last one's was not measured. The value is the one the earlier block's registry-fields row printed"
    return 0
  fi
  if [ "$NONCE" = "$PREVIOUS_NONCE" ]; then
    fact reboot-nonce FAIL "the registry file written after the reboot carries the SAME nonce as the one before it ($PREVIOUS_NONCE). The nonce is what makes the file's identity a value rather than a number, and one reused across two starts proves identity no more than a pid does (D1359)"
  else
    fact reboot-nonce PASS "the nonce this boot's daemon minted ($NONCE) differs from the previous boot's ($PREVIOUS_NONCE): it is minted per start and never reused, which is what makes the handshake's answer an identity (D1359)"
  fi
}

# ------------------------------------------------------------------- the teardown
# The check's own footprint, read back rather than assumed: the file it planted,
# the process it started, and the daemon it killed.
report_teardown() {
  if [ "$PLANTED" = yes ]; then
    if [ -e "$REG_PATH" ] && [ "$(file_now)" = "$DECOY_PID $NONCE" ]; then
      fact teardown FAIL "the registry file this check planted at $REG_PATH was still there at the end and this run's teardown removed it: the read should have. Nothing of this check's is left behind"
    elif [ -e "$REG_PATH" ]; then
      fact teardown PASS "the registry file this check planted is gone, and the one at $REG_PATH now is a live daemon's own"
    else
      fact teardown PASS "the registry file this check planted was removed by the read that judged it dead, and the run directory holds nothing this check wrote"
    fi
  else
    fact teardown PASS "this run planted no registry file"
  fi
  if [ -n "$DECOY_PID" ]; then
    if kill -0 "$DECOY_PID" 2>/dev/null; then
      fact teardown-decoy PASS "the process this check started to hold the dead pid (pid $DECOY_PID) was still running when the readings were taken, and the teardown stopped it: the reader above never signalled it"
    else
      fact teardown-decoy PASS "the process this check started to hold the dead pid (pid $DECOY_PID) is gone"
    fi
  fi
  if [ -n "$DAEMON_PID" ]; then
    if [ "$CRASH_HALF" = restarted ]; then
      fact teardown-daemon INFO "the daemon this check SIGKILLed (pid $DAEMON_PID) was replaced by one a supervisor started, which serves $ID now; there is nothing to start again"
    elif kill -0 "$DAEMON_PID" 2>/dev/null; then
      fact teardown-daemon INFO "pid $DAEMON_PID is running again, which means something started it back up"
    else
      fact teardown-daemon INFO "the daemon this check SIGKILLed (pid $DAEMON_PID) is not running any more and this check does not start it again: start one ('fugaro orchestrator run') for the reboot phase, or leave it to the login unit"
    fi
  fi
  fact teardown-cost INFO "nothing is created in the cloud and nothing is spent by this check; the only file it writes outside its scratch directory is the copy of the dead daemon's own registry, in the daemon's own directory"
}

# print_plan: what a real run would do, printed by --dry-run INSTEAD of doing any
# of it. This check's own test pins that a dry run makes no call at all -- no
# curl, no fugaro, no signal and no read of the run directory -- so the plan names
# the calls and never makes one.
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [reads the filesystem] resolve the state directory the way localcfg.UserDir does (\$XDG_STATE_HOME where it is set and absolute, else \$HOME/.local/state), and with no --id take the id from the one registry file under <state>/fugaro/run"
  say "  [reads] <state>/fugaro/run/<id>.json: its mode (must be 0600), its directory's mode (must be 0700), and its id, pid, address, token, nonce and start time. The token goes into a 0600 file inside this run's 0700 scratch directory and never onto a command line"
  say "  [network, loopback only] curl -sS -D <headers> --config <that file, as a Bearer header> http://<the file's addr>/state -- the handshake, which must read back the file's id and nonce"
  say "  [reads] fcntl.flock(LOCK_EX|LOCK_NB) on <state>/fugaro/run/<id>.lock -- the lock must FAIL to be taken, because the daemon holds it"
  say "  [reads] ps -o command= -ww -p <the file's pid> -- the pid is signalled only when its command line is the daemon's"
  say "  [reads] ${FUGARO_BIN} graph --source local:$ID --json -- the read that goes through endpoint.Probe"
  say "  [writes] kill -9 on that pid, the same read again, and the registry file (gone) and the lock (free) read back; where a supervisor has already started the daemon again, the crash half is reported as NOT MEASURED and nothing below it runs"
  say "  [writes] one process of this check's own, and the dead daemon's own file written again with that process's pid in it -- created only where the path is empty, never over a daemon a supervisor started again -- then the scope read once more: the file must be judged by the handshake and the lock, removed, and that process left running and unsignalled"
  say "  [writes] the teardown: that process stopped, the planted file removed if the read did not, the credential file and the scratch directory removed"
  say "  phase reboot: the same readings after a reboot, plus the nonce compared with the one --previous-nonce names"
  say "Nothing is created in the cloud, no model call is made and nothing is billable."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# -- not reached, and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(id belong xtrace state-dir registry-file registry-fields handshake
  lock-held daemon-pid graph-live crash crash-half graph-after-crash dead-file
  lock-free pid-reuse reboot-nonce endpoint teardown)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 313 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
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
  while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case "$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done <<<"$facts"
  printf 'CHECK 313 RESULT END (exit %s)\n' "$status" >&3
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
  # bad substitution), and a block that then read as a finished run would offer the
  # rows that read "not reached" as an answer about the endpoint when the reads
  # that would have filled them never ran. Any such ending is an abort, and 2 says
  # so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the endpoint"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  # The credential file and the scratch directory are this check's own, and both
  # are read back rather than assumed: a run that leaves the endpoint's token on
  # disk has to say so.
  if [ -n "$TOKEN_FILE" ]; then
    if [ "$TOKEN_FILE_REMOVED" = yes ]; then
      facts="${facts}"$'\n'"token-file|PASS|the file the token was read into is gone"
    else
      facts="${facts}"$'\n'"token-file|FAIL|the file the token was read into IS STILL THERE at $TOKEN_FILE (mode 600, holding the endpoint's token) -- remove it yourself"
    fi
  fi
  if [ -n "$CURL_CONF" ]; then
    if [ "$CURL_CONF_REMOVED" = yes ]; then
      facts="${facts}"$'\n'"curl-conf|PASS|the handshake's credential file is gone"
    else
      facts="${facts}"$'\n'"curl-conf|FAIL|the handshake's credential file IS STILL THERE at $CURL_CONF (mode 600, holding the token as a header) -- remove it yourself"
    fi
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH -- remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# ------------------------------------------------------------------- the verdict
row_verdict() {
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" | head -1
}

the_verdict() {
  local key drifted=() list
  for key in state-dir registry-file registry-fields handshake lock-held daemon-pid \
    graph-live crash graph-after-crash dead-file lock-free pid-reuse; do
    if [ "$(row_verdict "$key")" = FAIL ]; then drifted+=("$key"); fi
  done
  if [ "$PHASE" = reboot ] && [ -n "$PREVIOUS_NONCE" ] && [ "$(row_verdict reboot-nonce)" = FAIL ]; then
    drifted+=("reboot-nonce")
  fi
  if [ "$CRASH_HALF" = restarted ]; then
    if [ ${#drifted[@]} -eq 0 ]; then
      fact endpoint INFO "the live half of the endpoint holds on this machine: the registry file at $RUN_DIR/$ID.json is 0600 in a 0700 directory, carries a nonce minted per start, answers the handshake with the id and the nonce the file carries, and its lock is held. The CRASH half was NOT MEASURED this run -- $CRASH_HALF_REASON"
    else
      list=$(printf '%s, ' "${drifted[@]}"); list=${list%, }
      fact endpoint FAIL "the live half of the endpoint does not hold on this machine ($list; each row above says which reading it is), and the crash half was not measured -- $CRASH_HALF_REASON"
    fi
    REACHED_END=yes
    exit 2
  fi
  if [ ${#drifted[@]} -eq 0 ]; then
    fact endpoint PASS "the endpoint's registry file, its nonce and its liveness hold on this machine: mode 0600 in a 0700 directory, a nonce minted per start, a handshake that proves the process behind the address, a lock the kernel drops when the process dies, and a reader that judges a file by the handshake and the lock rather than by a pid -- and it removed the dead file while leaving a live process that had taken the pid alone (D1359, CG13b)"
    REACHED_END=yes
    exit 0
  fi
  list=$(printf '%s, ' "${drifted[@]}"); list=${list%, }
  fact endpoint FAIL "the endpoint does not hold on this machine: $list. Each row above says which reading it is and what to do about it"
  REACHED_END=yes
  exit 1
}

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# -------------------------------------------------------------------- main
parse_args() {
  ID=${CHECK313_ID:-}
  PHASE=live
  PREVIOUS_NONCE=${CHECK313_PREVIOUS_NONCE:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK313_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --id)
      [ $# -ge 2 ] || die "--id needs an orchestrator id. usage: $USAGE"
      ID=$2
      shift 2
      ;;
    --phase)
      [ $# -ge 2 ] || die "--phase needs live or reboot. usage: $USAGE"
      case "$2" in
      live | reboot) PHASE=$2 ;;
      *) die "--phase takes live or reboot, and '$2' is neither. usage: $USAGE" ;;
      esac
      shift 2
      ;;
    --previous-nonce)
      [ $# -ge 2 ] || die "--previous-nonce needs the value the earlier block printed. usage: $USAGE"
      PREVIOUS_NONCE=$2
      shift 2
      ;;
    --fugaro-flags)
      [ $# -ge 2 ] || die "--fugaro-flags needs a string of flags. usage: $USAGE"
      read -r -a FUGARO_FLAGS <<<"$2"
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
  if [ "$PHASE" = reboot ] && [ -z "$PREVIOUS_NONCE" ]; then
    say "no --previous-nonce was given: the nonce comparison will be recorded as not measured rather than as a pass"
  fi
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  if [ -n "$DRY_RUN" ]; then
    # A dry run reads nothing: the id and the belong guard are judged on what was
    # TYPED (a string test, no filesystem and no call), and the derivation and the
    # reads are the plan's business.
    if [ -n "$ID" ]; then
      guard_belong
      guard_id
    else
      fact id INFO "no --id was typed, and a dry run reads nothing: a real run takes the id from the one registry file under <state>/fugaro/run"
      fact belong INFO "no id was typed, so the guard had nothing to judge: a real run refuses an id that names the owner's own installation (belong) before it reads anything"
    fi
    fact dry-run PASS "guards and the plan only: nothing was read, no daemon was killed, no file was written, no scope was read and no cloud was touched"
    BLOCK_KEYS=(xtrace id belong dry-run)
    print_plan
    REACHED_END=yes
    return 0
  fi
  resolve_state_dir
  derive_id
  guard_belong
  guard_id
  read_registry
  judge_registry
  SAVED_REG="$SCRATCH/registry.saved.json"
  cp "$REG_PATH" "$SAVED_REG" 2>/dev/null ||
    stop "the registry file at $REG_PATH could not be copied into this run's scratch directory"
  handshake
  judge_lock_held
  daemon_pid_is_the_daemon
  judge_graph_live
  crash
  after_crash
  judge_graph_after_crash
  judge_dead_file
  judge_lock_free
  judge_pid_reuse
  judge_reboot_nonce
  report_teardown
  the_verdict
}

CURL_SECONDS=${CHECK313_CURL_SECONDS:-10}
WAIT_SECONDS=${CHECK313_WAIT_SECONDS:-10}
DECOY_SECONDS=${CHECK313_DECOY_SECONDS:-600}
FUGARO_BIN=${CHECK313_FUGARO:-fugaro}
NONCE=""
PHASE=live
PREVIOUS_NONCE=""
ID=""
OWN_ID=""
SAVED_REG=""
SCOPE_STATUS=0
SCOPE_OUT=""
SCOPE_ERR=""

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
