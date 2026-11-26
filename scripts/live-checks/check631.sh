#!/usr/bin/env bash
# Check 631 — the key credential, on a real installation: `fugaro accounts add
# <name> --type anthropic` (and `--type openrouter`) at YOUR terminal — the
# command's own hidden prompt and nothing echoed, no key shape in a file under
# ~/.config/fugaro/ and none in a shell history, the refusal in a coding agent's
# session, `fugaro accounts ls --json` naming each credential's serving path,
# `fugaro accounts rm` against the pool that listed it, and one sandbox run
# served by the credential the check declared.
#
# Plan task MC26 writes this check; its subject is the sentence MC26 gives
# number 631 in docs/model-crafts/activation-plan.md.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to (`fugaro doctor --project
#     <name> --all`, whose `project:` line the `--all` view alone prints);
#   * **the declaration it found** (`fugaro craft ls --json`, the published node
#     through `fugaro project policy show --json`, `fugaro accounts pool ls`,
#     `fugaro accounts ls --json`): the craft the sandbox run is served through,
#     its model and the chain the project's routing table resolves it to, the
#     access the published node declares for it — the one value this check takes
#     over and puts back — and the pools and credentials the config already
#     holds;
#   * **the refusal in a coding agent's session**: `fugaro accounts add <name>
#     --type anthropic` run with CLAUDECODE set is refused by name — a session
#     may write the declaration and never the key — and stores nothing. This
#     check reads the config back to say which of the two the session's refusal
#     left behind;
#   * **the command's own hidden prompt, and nothing echoed**: BOTH credentials
#     are declared with the value pasted at the COMMAND's own hidden prompt —
#     `fugaro accounts add <name> --type <type>` runs AT your terminal: the
#     terminal on its input and a terminal on its output, which is the whole of
#     the door a managed project's credential is created through
#     (accountsAddOwnTerminal, internal/cli/accounts_managed.go) — so this check
#     never holds the value at all: no flag, no argument, no environment and no
#     pipe carries it. The check reads each command's own prompt sentence out of
#     that command's output, and proves no stream of either carries a line
#     shaped like a provider key;
#   * **no key shape in a file, and none in a history**: every file under the
#     fugaro config directory (`~/.config/fugaro/`, the `.bak-<UTC>` backups
#     included) and every shell history file on this machine is scanned for a
#     line shaped like a provider key (`sk-ant-…`, `sk-or-v1-…`) — by a child
#     that prints file names only and is given no value to look for;
#   * **`fugaro accounts ls --json` naming the serving path**: each credential's
#     type and the half that serves it (the credentials service for an
#     Anthropic-key credential, a run's own gateway for an OpenRouter one);
#   * **one sandbox run served by it**: the check declares its own pool, points
#     the craft the acceptance serves at that pool, publishes the node (the
#     access half a run reads is the published one), launches one run, and reads
#     the credentials service's own audit line for that run — the credential's
#     NAME, never a value;
#   * **`fugaro accounts rm` and the pool that listed it**: the acceptance's
#     last clause, read as this build has it. This build refuses `accounts rm`
#     while a pool lists the credential, naming `fugaro accounts pool rm` — so
#     the check measures the refusal, then the pool's removal, then that
#     `accounts rm` succeeds and `fugaro accounts ls` no longer names it, and
#     says in its own row that the acceptance's "clearing a pool that listed it"
#     is this build's two steps rather than one.
#
# Nothing here reads a credential value out of the installation: names, types,
# paths, prompts, statuses and reason words only. This check holds no value of
# yours at any point — each one is read by the command under test at its own
# hidden prompt — so the two sweeps below look for the SHAPE of a provider key
# rather than for the value, which is the one reading a check that never sees the
# value can take.
#
# Usage:      bash scripts/live-checks/check631.sh --sandbox <a sandbox repository checkout> [--project <name>]
#             bash scripts/live-checks/check631.sh --sandbox <path> --preflight
#             bash scripts/live-checks/check631.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check631.sh --help
#             --preflight makes the check's own READS and evaluates every
#             precondition before anything is written or launched (the
#             installation, the declaration, the craft's chain and the access
#             the node declares for it, the pool and the two credential names
#             the check would declare), prints every blocker at once with the
#             command that clears it, writes nothing and launches nothing, and
#             exits non-zero when anything blocks.
# Needs:      bash, mktemp, date, python3, awk, gcloud (authenticated as YOU:
#             the runs bucket and Cloud Logging) and fugaro (the installed
#             release, an admin of the project), and YOUR OWN TERMINAL: the two
#             pastes are made at it.
# Where:      your own machine, the sandbox project and the sandbox repository
#             only, at your own terminal: a coding agent's session is refused by
#             name, as is a project, pool, craft or credential naming belong.
# Creates:    two declared credentials (their keys go to Secret Manager under
#             credential-<name>-key), one pool, one craft access taken over and
#             put back, one publish, and one sandbox run with its branch and the
#             pull request it opens. The two credentials and the pool are
#             removed again by the check itself.
# Costs:      one sandbox run and what its task costs, on the sandbox's own
#             budget, plus the wall clock of waiting for it.
# Runs:       by YOU. No agent runs any step of it. Its own test
#             (check631_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. What the check wrote is unwound: the craft's
#             access is put back and the node re-published, the pool is removed,
#             and both credentials are removed. What it does NOT put back: the
#             Secret Manager versions of the two keys (a value cannot be
#             unlearned), and the run's branch and pull request. The Restore row
#             says exactly that.
# Output:     progress on stderr, ONE block on stdout headed CHECK 631 RESULT,
#             then CHECK 631 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure.
#
# Knobs (all CHECK631_*): DRY_RUN, PREFLIGHT, KEEP_SCRATCH, SANDBOX, PROJECT, ANTHROPIC
# (chk631-anthropic) and OPENROUTER (chk631-openrouter) — the two names the
# check declares, so the credentials you already have are never touched — POOL
# (chk631-pool), CRAFT (coder: the craft whose access the sandbox run is served
# through), OWNER (the address the check's pool is served to; the default is the
# address gcloud is authenticated as, and a pool published to the node needs
# one), TASK and RECIPE (what the run is launched with), REGION (us-east5),
# LOG_VIEW, RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS
# (180), DOCTOR_TIMEOUT_SECONDS (300), LAUNCH_TIMEOUT_SECONDS (600),
# PUBLISH_TIMEOUT_SECONDS (600) and TTY (/dev/tty: the terminal both commands
# run at and read their value from, which only the check's own test points
# elsewhere).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads a live
# project, and a trace would copy its output a second time — and a trace of the
# step that runs a declaration at the terminal would print, a second time, every
# frame the command drew on the screen you are typing at.
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
die() { say "$*"; exit 2; }
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

UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

# The shared `--preflight` driver (scripts/live-checks/lib/preflight.sh).
live_check_lib_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$live_check_lib_dir}
# shellcheck source=lib/preflight.sh
. "$LIVE_CHECK_LIB/preflight.sh"
PREFLIGHT_TOOL=check631

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 631 needs bash"
  local t
  for t in mktemp date python3 awk gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 631 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk631-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
# PROJECT starts as the name the owner asked for, so the belong guard can read
# it before any command runs; INSTALLATION says the doctor named one.
PROJECT=""
INSTALLATION=""
GCP_PROJECT=""
# The value is the one thing this check never holds: each credential's is read
# by the command under test at its own hidden prompt, with this check's terminal
# as that command's stdin.
HAVE_TTY=no
ANTHROPIC=""
OPENROUTER=""
POOL=""
CRAFT=""
OWNER=""
PREV_ACCESS=""
PREV_MODEL=""
DECLARED_BEFORE=no
ANTHROPIC_DECLARED=no
OPENROUTER_DECLARED=no
POOL_WRITTEN=no
ACCESS_TAKEN=no
PUBLISHED=no
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
EXECUTION=""
SETTLED=no
REMOVED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk631.XXXXXXXX") || die "Check 631 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    say "CHECK631_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def text(value):
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    return json.dumps(value, sort_keys=True)


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "launch":
    print("project=%s" % (doc.get("project") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "", row.get("status") or "",
            "true" if row.get("terminal") else "false", row.get("stage") or "",
            row.get("run") or ""))
elif what == "credentials":
    creds = doc.get("credentials") or []
    print("count=%d" % len(creds))
    print("complete=%s" % ("true" if doc.get("complete", True) else "false"))
    print("note=%s" % (doc.get("note") or ""))
    for c in creds:
        print("cred\t%s\t%s\t%s" % (
            c.get("name") or "", c.get("type") or "", c.get("serving_path") or ""))
elif what == "crafts":
    # Version 2 (RT6, D3170): the chain a craft's model resolves to, in place of
    # the access, access_from and inert columns the version-1 document carried.
    # The access this check takes over is not in this document any more — it is
    # the published node's, read by the `node` branch below, which is the
    # document a run's access half is read from.
    rows = doc.get("crafts") or []
    print("version=%s" % doc.get("version", ""))
    print("project=%s" % (doc.get("project") or ""))
    print("count=%d" % len(rows))
    for r in rows:
        print("craft\t%s\t%s\t%s\t%s\t%s" % (
            r.get("craft") or "", r.get("model") or "", r.get("model_from") or "",
            ",".join(r.get("chain") or []),
            "true" if r.get("model_servable") else "false"))
elif what == "node":
    # `fugaro project policy show --json` prints {"project": ..., "policy": ...}:
    # every block of the node is a member of `policy`, and read at the top level
    # each one is null — a node row built from those nulls calls a correct
    # installation broken.
    policy = doc.get("policy")
    policy = policy if isinstance(policy, dict) else {}
    pools = policy.get("craft_pools") or []
    creds = policy.get("craft_credentials") or []
    crafts = policy.get("crafts") or {}
    print("version=%s" % policy.get("version", ""))
    print("pools=%d" % len(pools))
    print("credentials=%d" % len(creds))
    print("crafts=%d" % len(crafts))
    for p in pools:
        print("pool\t%s\t%s\t%s" % (
            p.get("name") or "", p.get("strategy") or "",
            ",".join(p.get("members") or [])))
    for c in creds:
        print("cred\t%s\t%s" % (c.get("name") or "", c.get("type") or ""))
    for name in sorted(crafts):
        row = crafts[name] or {}
        print("nodecraft\t%s\t%s\t%s" % (
            name, row.get("model") or "", row.get("access") or ""))
elif what == "record":
    cost = doc.get("cost") or {}
    pr = doc.get("pr") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("stage=%s" % (doc.get("stage") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("pr=%s" % (pr.get("url") or ""))
    print("execution=%s" % (doc.get("execution") or ""))
    print("model_usd=%s" % (cost.get("model_usd", 0)))
    print("error=%s" % (text(doc.get("error")) if doc.get("error") else ""))
    stages = doc.get("stages") or []
    print("stages=%d" % len(stages))
    for st in stages:
        print("stage\t%s\t%s\t%s\t%s" % (
            st.get("name") or "", st.get("model") or "", st.get("credential") or "",
            st.get("served_by") or ""))
    for name, account in sorted((doc.get("stage_accounts") or {}).items()):
        print("account\t%s\t%s" % (name, account))
elif what == "audit":
    want = sys.argv[3]
    rows = doc if isinstance(doc, list) else []
    printed = 0
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        message = entry.get("textPayload") or payload.get("msg") or payload.get("message") or ""
        if message != "credsvc: audit" or (payload.get("run") or "") != want:
            continue
        if (payload.get("purpose") or "") != "forward":
            continue
        printed += 1
        print("audit\t%s\t%s\t%s\t%s" % (
            payload.get("credential") or "", payload.get("tier") or "",
            payload.get("status") or "", payload.get("error_type") or ""))
    print("forward=%d" % printed)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# write_sweeper is the child that reads a config directory and a set of history
# files looking for a line shaped like a provider key. It is handed NO value —
# this check never holds one — so all it can do is name the files a shape appears
# in, and it prints names and counts only, never a line it read.
write_sweeper() {
  cat >"$SCRATCH/sweep.py" <<'PY'
import os
import re
import sys

KEY_SHAPE = re.compile(r"(sk-ant-|sk-or-v1-)[A-Za-z0-9_-]{12,}")


def walk(paths):
    for path in paths:
        if os.path.isdir(path):
            for root, dirs, names in os.walk(path):
                dirs.sort()
                for name in sorted(names):
                    yield os.path.join(root, name)
        elif os.path.isfile(path):
            yield path


what = sys.argv[1]
paths = sys.argv[2:]
scanned = 0
hits = 0
for path in walk(paths):
    try:
        with open(path, "rb") as fh:
            data = fh.read(8 * 1024 * 1024)
    except OSError:
        continue
    scanned += 1
    found = False
    if what == "shape":
        for line in data.split(b"\n"):
            if KEY_SHAPE.search(line.decode("utf-8", "replace")):
                found = True
                break
    else:
        sys.stderr.write("sweep.py: unknown query %s\n" % what)
        sys.exit(2)
    if found:
        hits += 1
        print("hit\t%s" % path)
print("scanned=%d" % scanned)
print("hits=%d" % hits)
PY
  chmod 700 "$SCRATCH/sweep.py"
}

read_json() { # read_json QUERY FILE DEST [ARGS...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

sweep() { # sweep QUERY DEST PATH...
  local query=$1 dest=$2
  shift 2
  [ $# -gt 0 ] || return 0
  python3 "$SCRATCH/sweep.py" "$query" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY
  [ -f "$1" ] || return 0
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
  return 0
}

table_field() { # table_field FILE KIND NAME COLUMN
  [ -f "$1" ] || return 0
  awk -F'\t' -v kind="$2" -v name="$3" -v col="$4" '$1 == kind && $2 == name { print $col; exit }' "$1" 2>/dev/null
  return 0
}

row_field() { # row_field COLUMN — one column of the run this check launched
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# The bound is counted in tenths of a second, and the poll is the same tenth:
# every command this check runs answers in milliseconds, and a poll of a whole
# second made each of them cost a second of wall clock -- forty-odd of them per
# run, which is what put this check's test past its runner's deadline on a
# loaded machine. A tick that overruns under load only makes the bound later,
# never earlier.
bound_loop() { # bound_loop SECONDS PID
  local secs=$1 pid=$2 ticks=$(( $1 * 10 )) waited=0
  BOUNDED_TIMED_OUT=no
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$ticks" ] || break
    sleep 0.1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}

run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# run_at_terminal runs a command at the owner's OWN terminal: the terminal
# itself on the command's input, and a terminal on each of its two output
# streams. That is the whole of the door a managed project's credential is
# created through — no coding agent's marker, no --yes, a terminal on the run's
# input and a terminal on its output (accountsAddOwnTerminal,
# internal/cli/accounts_managed.go) — and a command given anything less is
# refused before it reads a value. The value travels from the terminal to the
# command on the command's own fd 0, so this check never holds it; what the
# command prints is captured, its two streams apart, and relayed to this
# check's own stderr, which is the screen the person at the terminal is reading
# the prompt on.
run_at_terminal() { # run_at_terminal SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1
    exec python3 "$SCRATCH/at_terminal.py" "$secs" "$SCRATCH/$key.out" "$SCRATCH/$key.err" "$@" ) &
  bound_loop "$secs" "$!"
  # The runner ends a command that outstayed its deadline itself, with 124, the
  # child killed by its own process group rather than by this shell.
  [ "$BOUNDED_STATUS" != 124 ] || BOUNDED_TIMED_OUT=yes
}

# write_terminal_runner is the child that runs one declaration at the owner's
# terminal. fd 5 is that terminal (open_terminal); fd 4 is this check's stderr,
# the screen the command's own output is relayed to. Each of the command's
# output streams gets a terminal of its own, and everything written to it is
# appended to that stream's capture file as well, so the command sees the
# terminal the door asks for while this check can still read what it printed.
# NO_COLOR is set for the command: a terminal writer draws its steps with colour
# and an in-flight line, and the escape sequences are not what any row of this
# check reads.
write_terminal_runner() {
  cat >"$SCRATCH/at_terminal.py" <<'PY'
import os
import pty
import select
import signal
import subprocess
import sys
import termios
import time


def output_terminal():
    master, slave = pty.openpty()
    attrs = termios.tcgetattr(slave)
    attrs[1] = 0  # no output post-processing, so the capture keeps the bytes
    termios.tcsetattr(slave, termios.TCSANOW, attrs)
    return master, slave


secs = float(sys.argv[1])
out_path, err_path = sys.argv[2], sys.argv[3]
argv = sys.argv[4:]

out_master, out_slave = output_terminal()
err_master, err_slave = output_terminal()
env = dict(os.environ, NO_COLOR="1")
child = subprocess.Popen(argv, stdin=5, stdout=out_slave, stderr=err_slave,
                         env=env, start_new_session=True, close_fds=True)
os.close(out_slave)
os.close(err_slave)
screen = os.fdopen(os.dup(4), "wb", 0)
captures = {out_master: open(out_path, "wb"), err_master: open(err_path, "wb")}


def relay(master):
    try:
        chunk = os.read(master, 65536)
    except OSError:
        chunk = b""
    if not chunk:
        return False
    captures[master].write(chunk)
    captures[master].flush()
    screen.write(chunk)
    screen.flush()
    return True


live = set(captures)
deadline = time.time() + secs
timed_out = False
while True:
    if live:
        ready, _, _ = select.select(list(live), [], [], 0.2)
    else:
        ready = []
        time.sleep(0.2)
    for master in ready:
        if not relay(master):
            live.discard(master)
    if child.poll() is not None:
        break
    if time.time() > deadline:
        timed_out = True
        try:
            os.killpg(os.getpgid(child.pid), signal.SIGKILL)
        except OSError:
            pass
        child.wait()
        break
# The child is gone; drain what its terminals still hold, briefly.
edge = time.time() + 1.0
while live and time.time() < edge:
    ready, _, _ = select.select(list(live), [], [], 0.1)
    for master in ready:
        if not relay(master):
            live.discard(master)
for fh in captures.values():
    fh.close()
screen.close()
sys.exit(124 if timed_out else child.returncode)
PY
  chmod 700 "$SCRATCH/at_terminal.py"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check631.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of a declaration's own run would copy every frame it drew on your terminal a second time"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a credential's value to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 631 is run by YOU at your own terminal, because the two values it declares are pasted at it and a session may write the declaration and never the key. (The refusal a session meets is a step of this check, run against the CLI — it is not how the check itself runs.)"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT}${GCP_PROJECT}${REGION}${POOL}${CRAFT}${ANTHROPIC}${OPENROUTER}" in
  *belong*) stop "the account, project, pool, craft or credential this check would use names belong: this check is never run there, and no name it declares is belong" ;;
  esac
  fact belong PASS "neither the project this check names nor the pool, craft and two credentials it declares is belong, and the account it runs as is yours"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release whose accounts add and accounts rm this check measures)"
}

# ---------------------------------------------------------------- preflight
# step_preflight: --preflight. The reads the check makes before it writes or
# launches anything — the installation, the declaration (the craft, the pools
# and the credentials) — with every precondition evaluated and nothing written:
# no credential declared, no pool written, no node published, no run launched.
# The two names it would declare and the pool it would write are read for
# collision, and never written. The two values are not read: a preflight is safe
# with no terminal.
step_preflight() {
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")

  say "preflight: reading the installation: fugaro doctor --all, in ${SANDBOX}"
  local args=(doctor --all)
  [ -z "$PROJECT_ARG" ] || args=(doctor --project "$PROJECT_ARG" --all)
  run_bounded "$DOCTOR_TIMEOUT" pf-doctor "$SANDBOX" "$FUGARO_BIN" "${args[@]}"
  local line
  line=$(grep -m1 '^project: ' "$SCRATCH/pf-doctor.out" 2>/dev/null || true)
  local named_gcp=""
  if [ -n "$line" ]; then
    PROJECT=$(printf '%s' "$line" | sed -n 's/^project: \([^ ]*\) .*/\1/p')
    named_gcp=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
  fi
  case "${named_gcp:-$PROJECT}" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${named_gcp:-$PROJECT}: this check is never run there" ;;
  esac
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ -z "$line" ]; then
    preflight_block installation \
      "fugaro ${args[*]} does not answer for $SANDBOX (exit ${BOUNDED_STATUS}), so the installation this check would write to cannot be read" \
      "cd $SANDBOX && fugaro ${args[*]}"
  else
    preflight_note installation "the installation is ${PROJECT:-unknown} (GCP ${named_gcp:-unknown}), read from fugaro ${args[*]} in ${SANDBOX}"
  fi

  say "preflight: reading the declaration: fugaro craft ls --json, fugaro project policy show --json, fugaro accounts pool ls and fugaro accounts ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-crafts "$SANDBOX" "$FUGARO_BIN" craft ls --json ${pflag[@]+"${pflag[@]}"}
  local crafts_ok=no
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
    read_json crafts "$SCRATCH/pf-crafts.out" "$SCRATCH/pf-crafts.txt"; then
    crafts_ok=yes
  else
    preflight_block declaration \
      "fugaro craft ls --json does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and the craft the sandbox run is served through is in that document" \
      "cd $SANDBOX && fugaro craft ls --json${PROJECT_ARG:+ --project $PROJECT_ARG}"
  fi
  run_bounded "$CLI_TIMEOUT" pf-pools "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  local pools_ok=no
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ]; then
    pools_ok=yes
  else
    preflight_block wiring \
      "fugaro accounts pool ls does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and the pool this check would declare cannot be read" \
      "cd $SANDBOX && fugaro accounts pool ls${PROJECT_ARG:+ --project $PROJECT_ARG}"
  fi
  run_bounded "$CLI_TIMEOUT" pf-creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local creds_ok=no
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
    read_json credentials "$SCRATCH/pf-creds.out" "$SCRATCH/pf-creds.txt"; then
    creds_ok=yes
  else
    preflight_block add-anthropic \
      "fugaro accounts ls --json does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and whether the two names this check declares are taken is in that document" \
      "cd $SANDBOX && fugaro accounts ls --json${PROJECT_ARG:+ --project $PROJECT_ARG}"
  fi
  run_bounded "$CLI_TIMEOUT" pf-node "$SANDBOX" "$FUGARO_BIN" project policy show --json ${pflag[@]+"${pflag[@]}"}
  local node_ok=no
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
    read_json node "$SCRATCH/pf-node.out" "$SCRATCH/pf-node.txt"; then
    node_ok=yes
  else
    preflight_block declaration \
      "fugaro project policy show --json does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and the access this check takes over and puts back is the one the published node declares for the craft" \
      "cd $SANDBOX && fugaro project policy show --json${PROJECT_ARG:+ --project $PROJECT_ARG}"
  fi

  if [ "$crafts_ok" = yes ] && [ "$node_ok" = yes ]; then
    local have_craft chain access other target
    have_craft=$(table_field "$SCRATCH/pf-crafts.txt" craft "$CRAFT" 2)
    chain=$(table_field "$SCRATCH/pf-crafts.txt" craft "$CRAFT" 5)
    access=$(table_field "$SCRATCH/pf-node.txt" nodecraft "$CRAFT" 4)
    other=$(awk -F'\t' '$1 == "craft" { print $2; exit }' "$SCRATCH/pf-crafts.txt" 2>/dev/null || true)
    target=$(awk -F'\t' '$1 == "cred" { print $2; exit }' "$SCRATCH/pf-creds.txt" 2>/dev/null || true)
    [ -n "$target" ] || target=$(awk 'NR > 2 && $1 != "" { print $1; exit }' "$SCRATCH/pf-pools.out" 2>/dev/null || true)
    if [ -z "$have_craft" ]; then
      preflight_block declaration \
        "the installation names no craft ${CRAFT}, and the sandbox run is served through a craft's access" \
        "CHECK631_CRAFT=${other:-$CRAFT} bash scripts/live-checks/check631.sh --sandbox $SANDBOX"
    elif [ -n "$chain" ]; then
      preflight_block declaration \
        "the project's routing table serves ${SANDBOX}: the ${CRAFT} craft's calls go out on its chain (${chain}) and not on the access this check takes over, so the sandbox run would not be served by the credential this check declares" \
        "cd $SANDBOX && fugaro routing rm <the pattern that matches the ${CRAFT} craft's model>"
    elif [ -z "$access" ]; then
      preflight_block declaration \
        "the craft ${CRAFT} declares no access, and the sandbox run is served by the credential that access names" \
        "fugaro accounts craft set $CRAFT --access ${target:-$CRAFT}"
    else
      preflight_note declaration "the craft ${CRAFT} declares the access ${access} in the published node, which this check takes over for the sandbox run and puts back"
    fi
  fi

  if [ "$pools_ok" = yes ]; then
    local existing
    existing=$(awk -v p="$POOL" 'NR > 2 && $1 == p { print $0; exit }' "$SCRATCH/pf-pools.out" 2>/dev/null || true)
    if [ -n "$existing" ]; then
      preflight_block wiring \
        "the pool ${POOL} is already declared (${existing}), and this check declares it with its own member" \
        "fugaro accounts pool rm $POOL"
    fi
  fi

  if [ "$creds_ok" = yes ]; then
    local a_type o_type
    a_type=$(table_field "$SCRATCH/pf-creds.txt" cred "$ANTHROPIC" 3)
    o_type=$(table_field "$SCRATCH/pf-creds.txt" cred "$OPENROUTER" 3)
    if [ -n "$a_type" ]; then
      preflight_block add-anthropic \
        "the credential ${ANTHROPIC} is already declared (type ${a_type}), and this check declares it from a value you paste, replacing the one in use" \
        "fugaro accounts rm $ANTHROPIC"
    fi
    if [ -n "$o_type" ]; then
      preflight_block add-openrouter \
        "the credential ${OPENROUTER} is already declared (type ${o_type}), and this check declares it from a value pasted at the command's own prompt" \
        "fugaro accounts rm $OPENROUTER"
    fi
    [ -n "$a_type" ] || [ -n "$o_type" ] ||
      preflight_note add-anthropic "the two names this check declares (${ANTHROPIC} and ${OPENROUTER}) are both free in the config"
  fi
}

# ---------------------------------------------------------------- the steps
step_installation() {
  local args=(doctor --all)
  [ -z "$PROJECT_ARG" ] || args=(doctor --project "$PROJECT_ARG" --all)
  say "reading the installation: fugaro ${args[*]}, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" "${args[@]}"
  local line
  line=$(grep -m1 '^project: ' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$line" ]; then
    unmeasured installation "fugaro ${args[*]} printed no 'project: <name> (GCP <project>, <region>)' line in ${SANDBOX} (exit ${BOUNDED_STATUS}): the project line is what tells this check which installation it is reading. What it printed is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(printf '%s' "$line" | sed -n 's/^project: \([^ ]*\) .*/\1/p')
  GCP_PROJECT=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
  if [ -z "$PROJECT" ] || [ -z "$GCP_PROJECT" ]; then
    unmeasured installation "the project line '${line}' is not the shape this check reads (project: <name> (GCP <project>, <region>)): what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$GCP_PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${GCP_PROJECT}: this check is never run there" ;;
  esac
  INSTALLATION=$PROJECT
  fact installation PASS "${SANDBOX} is a checkout of the installation ${PROJECT} (GCP ${GCP_PROJECT}): the credentials this check declares, the pool it writes and the run it launches are all this installation's"
}

# step_declaration: what the installation already holds. The craft's own access
# is read here and not later, because it is what the check takes over for the
# sandbox run and the one value it has to put back.
step_declaration() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured declaration "there is no installation whose config could be read: the row above says why"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "reading the declaration: fugaro craft ls --json, fugaro project policy show --json, fugaro accounts pool ls and fugaro accounts ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" crafts "$SANDBOX" "$FUGARO_BIN" craft ls --json ${pflag[@]+"${pflag[@]}"}
  local crafts_problem=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    crafts_problem="fugaro craft ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS})"
  elif ! read_json crafts "$SCRATCH/crafts.out" "$SCRATCH/crafts.txt"; then
    crafts_problem="fugaro craft ls --json printed a document this check could not read"
  fi
  run_bounded "$CLI_TIMEOUT" node-before "$SANDBOX" "$FUGARO_BIN" project policy show --json ${pflag[@]+"${pflag[@]}"}
  local node_problem=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    node_problem="fugaro project policy show --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS})"
  elif ! read_json node "$SCRATCH/node-before.out" "$SCRATCH/node-before.txt"; then
    node_problem="fugaro project policy show --json printed a document this check could not read"
  fi
  run_bounded "$CLI_TIMEOUT" pools "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  local pools_problem=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    pools_problem="fugaro accounts pool ls did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS})"
  fi
  run_bounded "$CLI_TIMEOUT" creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local creds_problem=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    creds_problem="fugaro accounts ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS})"
  elif ! read_json credentials "$SCRATCH/creds.out" "$SCRATCH/creds.txt"; then
    creds_problem="fugaro accounts ls --json printed a document this check could not read"
  fi
  local problem="${crafts_problem:-}"
  if [ -n "$node_problem" ]; then problem="${problem:+$problem; }${node_problem}"; fi
  if [ -n "$pools_problem" ]; then problem="${problem:+$problem; }${pools_problem}"; fi
  if [ -n "$creds_problem" ]; then problem="${problem:+$problem; }${creds_problem}"; fi
  if [ -n "$problem" ]; then
    unmeasured declaration "${problem}: the craft, the node that declares its access, the pools and the credentials this check reads before it writes anything are in those answers. What it printed is in $SCRATCH/crafts.out, $SCRATCH/node-before.out, $SCRATCH/pools.out and $SCRATCH/creds.out"
    return 0
  fi
  PREV_ACCESS=$(table_field "$SCRATCH/node-before.txt" nodecraft "$CRAFT" 4)
  local have_craft craft_chain
  have_craft=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 2)
  craft_chain=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 5)
  local craft_model="" craft_model_from=""
  craft_model=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 3)
  craft_model_from=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 4)
  if [ -z "$have_craft" ]; then
    unmeasured declaration "fugaro craft ls --json in ${SANDBOX} names no craft '${CRAFT}' among the crafts of ${INSTALLATION} (its document is in $SCRATCH/crafts.out): the acceptance's sandbox run is served through a craft's access, so this check has nothing to serve it with. Name another with CHECK631_CRAFT, or declare this one, and run the check again"
    return 0
  fi
  if [ -n "$craft_chain" ]; then
    unmeasured declaration "the project's routing table serves ${SANDBOX}: the ${CRAFT} craft's calls go out on its chain (${craft_chain}) and not on the access this check takes over, so the sandbox run would not be served by the credential this check declares. Clear the entry that matches the craft's model with 'fugaro routing rm <the pattern>' and run the check again"
    return 0
  fi
  if [ -z "$PREV_ACCESS" ]; then
    unmeasured declaration "the craft ${CRAFT} of ${INSTALLATION} declares no access in the published node (its model is ${craft_model:-none}, from ${craft_model_from:-nowhere}): the sandbox run of this check is served by the credential that access names, and this check puts the access back when it is done, so it only takes one over that is already declared. Declare one with 'fugaro accounts craft set ${CRAFT} --access <a pool or credential of this installation>', then run this check again"
    return 0
  fi
  DECLARED_BEFORE=yes
  local pools_text
  pools_text=$(awk 'NR > 1 { printf "%s ", $0 }' "$SCRATCH/pools.out" 2>/dev/null || true)
  fact declaration PASS "the craft ${CRAFT} of ${INSTALLATION} declares the access ${PREV_ACCESS} in the published node, its model is ${craft_model:-none} from ${craft_model_from:-nowhere}, and the installation's pools read ${pools_text:-none}: the craft's access is the one value this check takes over and puts back, and that it is declared is what makes the putting back exact"
}

# step_agent_refusal: the acceptance's clause about a coding agent's session.
# The check runs the CLI with the marker set and reads what the session was told.
step_agent_refusal() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured agent-refusal "there is no installation to run the CLI against: the row above says why"
    return 0
  fi
  # `accounts add` declares no --project (it is a write: the project is the
  # checkout's own fugaro.yaml, which is the directory this runs in), so the act
  # is run without one — passing it would exit 1 with 'unknown flag: --project'
  # and the refusal this row measures would never be reached. A --project the
  # owner gave still names the installation, through FUGARO_PROJECT; the reads
  # below take the flag itself.
  local penv=()
  [ -z "$PROJECT_ARG" ] || penv=(FUGARO_PROJECT="$PROJECT_ARG")
  say "reading what a coding agent's session is told: CLAUDECODE=1 fugaro accounts add ${ANTHROPIC} --type anthropic"
  run_bounded "$CLI_TIMEOUT" add-refused "$SANDBOX" env CLAUDECODE=1 ${penv[@]+"${penv[@]}"} "$FUGARO_BIN" accounts add "$ANTHROPIC" --type anthropic
  local out
  out=$(cat "$SCRATCH/add-refused.out" "$SCRATCH/add-refused.err" 2>/dev/null || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured agent-refusal "the add run with CLAUDECODE set did not finish inside ${CLI_TIMEOUT}s and was ended: what it printed is in $SCRATCH/add-refused.out and $SCRATCH/add-refused.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" = 0 ]; then
    fact agent-refusal FAIL "fugaro accounts add ${ANTHROPIC} --type anthropic exited 0 with CLAUDECODE set: a coding agent's session was not refused a key, and the acceptance's clause is that it is ('no agent runs any step of it'). What it printed is in $SCRATCH/add-refused.out"
    return 0
  fi
  # The words a session is refused a credential entry in. The release answers
  # this act with the credential's own refusal — what --type would create, and
  # the narrower step the session can take instead — rather than with the token
  # step's "not in an agent session", which is the sentence for an account the
  # session may not read the value of (accountsAddRefusal, accountsAddTokenStep,
  # internal/cli/accounts_managed.go). Both are the refusal this row measures: a
  # session may write the declaration and never the key.
  case "$out" in
  *"not in an agent session"* | *"agent session"* | *"session is set"* | *"coding agent's session"* | *"managed project's owner is the one who creates credentials"* | *"Declare the account without --type"*) ;;
  *)
    fact agent-refusal FAIL "the add run with CLAUDECODE set exited ${BOUNDED_STATUS} and its own words are neither the credential's refusal nor the token step's (it said: $(printf '%s' "$out" | head -n 2 | tr '\n' ' ')): the acceptance's clause is a refusal a session can act on, and one that does not name the step it may not take is not it. What it printed is in $SCRATCH/add-refused.err"
    return 0
    ;;
  esac
  run_bounded "$CLI_TIMEOUT" creds-after-refusal "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local declared="not read"
  if read_json credentials "$SCRATCH/creds-after-refusal.out" "$SCRATCH/creds-after-refusal.txt" 2>/dev/null; then
    if [ -n "$(table_field "$SCRATCH/creds-after-refusal.txt" cred "$ANTHROPIC" 3)" ]; then
      declared=yes
    else
      declared=no
    fi
  fi
  fact agent-refusal PASS "fugaro accounts add ${ANTHROPIC} --type anthropic with CLAUDECODE set exited ${BOUNDED_STATUS} and told the session '$(printf '%s' "$out" | grep -m1 -v '^$' | tr -s ' ' || true)' — and fugaro accounts ls --json after it names ${ANTHROPIC} ${declared}. A session may write the declaration and never the key: no value was read and none was stored, which is what that sentence promises"
}

# ADD_STREAMS is the pair of files one declaration's output was captured in.
ADD_STREAMS=()
add_streams() { # add_streams NAME — sets ADD_STREAMS
  if [ "$1" = "$OPENROUTER" ]; then
    ADD_STREAMS=("$SCRATCH/add-openrouter.out" "$SCRATCH/add-openrouter.err")
  else
    ADD_STREAMS=("$SCRATCH/add-anthropic.out" "$SCRATCH/add-anthropic.err")
  fi
}

# step_add_anthropic: the acceptance's first verb, at the COMMAND's own hidden
# prompt. It runs at your terminal — the terminal on its input, a terminal on
# its output — so the value is read by the command and by nothing else here: the
# gate a managed project's credential is created through admits the owner's own
# terminal and refuses a pipe, a run and a --yes (accountsAddOwnTerminal,
# internal/cli/accounts_managed.go), which is why this check never holds the
# value and proves what it can prove — the shape.
step_add_anthropic() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured add-anthropic "there is no installation to declare the credential on (the installation row above says why), so no value was asked for: a value pasted for a check that cannot store it is a value in a transcript for nothing"
    return 0
  fi
  owner ""
  owner "Paste the value for the credential ${ANTHROPIC} (--type anthropic) now, at the COMMAND's own hidden prompt."
  owner "That prompt is the one the acceptance names: it is hidden by the command itself, what you type is read by the command and by nothing else here, and this check never holds it."
  owner ""
  say "declaring the Anthropic-key credential: fugaro accounts add ${ANTHROPIC} --type anthropic (at your terminal: its own hidden prompt, and what it prints comes back here)"
  run_at_terminal "$CLI_TIMEOUT" add-anthropic "$SANDBOX" "$FUGARO_BIN" accounts add "$ANTHROPIC" --type anthropic
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured add-anthropic "fugaro accounts add ${ANTHROPIC} --type anthropic did not finish inside ${CLI_TIMEOUT}s and was ended: what it printed is in $SCRATCH/add-anthropic.out and $SCRATCH/add-anthropic.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact add-anthropic FAIL "fugaro accounts add ${ANTHROPIC} --type anthropic exited ${BOUNDED_STATUS} in ${SANDBOX}: the credential the acceptance's sandbox run is served by was not declared. Its own words are in $SCRATCH/add-anthropic.err — it never quotes a value"
    return 0
  fi
  ANTHROPIC_DECLARED=yes
  local said
  said=$(grep -m1 'no job is deployed' "$SCRATCH/add-anthropic.out" 2>/dev/null || true)
  fact add-anthropic PASS "fugaro accounts add ${ANTHROPIC} --type anthropic declared the credential of ${INSTALLATION} from the value you pasted at the command's own hidden prompt, and the CLI stored it under its own derived secret (credential-${ANTHROPIC}-key, never an account's token container)${said:+ — it said: ${said}}"
}

# step_nothing_echoed: the acceptance's "nothing echoed". This check never holds
# the value, so what it reads is the SHAPE of a provider key: a command that
# echoed the value it read would print one, in either stream.
step_nothing_echoed() {
  local names=() name scanned=0 hits=0 named=""
  if [ "$ANTHROPIC_DECLARED" = yes ]; then names+=("$ANTHROPIC"); fi
  if [ "$OPENROUTER_DECLARED" = yes ]; then names+=("$OPENROUTER"); fi
  if [ "${#names[@]}" = 0 ]; then
    unmeasured nothing-echoed "neither credential was declared (their rows above say why), so no command printed an output this check could read"
    return 0
  fi
  for name in "${names[@]}"; do
    add_streams "$name"
    sweep shape "$SCRATCH/echoed-${name}.txt" "${ADD_STREAMS[@]}"
    local s h
    s=$(field "$SCRATCH/echoed-${name}.txt" scanned)
    h=$(field "$SCRATCH/echoed-${name}.txt" hits)
    scanned=$((scanned + ${s:-0}))
    hits=$((hits + ${h:-1}))
    if [ "${h:-1}" != 0 ]; then
      named="${named:+$named, }${name}($(awk -F'\t' '$1 == "hit" { printf "%s ", $2 }' "$SCRATCH/echoed-${name}.txt"))"
    fi
  done
  if [ "$hits" != 0 ]; then
    fact nothing-echoed FAIL "the output of fugaro accounts add --type for ${named} carries a line shaped like a provider key: a command that echoes the value it stores writes it into every transcript of this check, and the acceptance's clause is that nothing is echoed. The check did not print those lines; what each command printed is in $SCRATCH/add-anthropic.err and $SCRATCH/add-openrouter.err"
    return 0
  fi
  fact nothing-echoed PASS "neither the stdout nor the stderr of fugaro accounts add --type for ${names[*]} carries a line shaped like a provider key (${scanned} stream(s) scanned, 0 of them carrying one): the value was read with no echo at the command's own hidden prompt and printed nowhere"
}

# step_add_openrouter: the acceptance's second type, at the COMMAND's own hidden
# prompt — it runs at your terminal, so the prompt below is the command's.
step_add_openrouter() {
  if [ "$ANTHROPIC_DECLARED" != yes ]; then
    unmeasured add-openrouter "the Anthropic-key credential this check's own sandbox run is served by was not declared (its row above says why), so the second type was not declared either: fix that row and run the check again"
    return 0
  fi
  owner ""
  owner "Now paste the value for the credential ${OPENROUTER} (--type openrouter) at the COMMAND's own prompt."
  owner "That prompt is the one the acceptance names: it is hidden by the command itself, and what you type is read by the command and by nothing else here."
  owner ""
  say "declaring the OpenRouter credential: fugaro accounts add ${OPENROUTER} --type openrouter (at your terminal: its own hidden prompt, and what it prints comes back here)"
  run_at_terminal "$CLI_TIMEOUT" add-openrouter "$SANDBOX" "$FUGARO_BIN" accounts add "$OPENROUTER" --type openrouter
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured add-openrouter "fugaro accounts add ${OPENROUTER} --type openrouter did not finish inside ${CLI_TIMEOUT}s and was ended: what it printed is in $SCRATCH/add-openrouter.out and $SCRATCH/add-openrouter.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact add-openrouter FAIL "fugaro accounts add ${OPENROUTER} --type openrouter exited ${BOUNDED_STATUS} in ${SANDBOX}: the acceptance's second type was not declared. Its own words are in $SCRATCH/add-openrouter.err — it never quotes a value"
    return 0
  fi
  OPENROUTER_DECLARED=yes
  fact add-openrouter PASS "fugaro accounts add ${OPENROUTER} --type openrouter declared the second credential of ${INSTALLATION} at the command's own hidden prompt, and the CLI stored it under credential-${OPENROUTER}-key"
}

# step_prompt_hidden: each command's own prompt, out of that command's own
# output — the sentence the acceptance names, and the only door the value was
# read through.
step_prompt_hidden() {
  local names=() name want out missing="" prompts=""
  if [ "$ANTHROPIC_DECLARED" = yes ]; then names+=("$ANTHROPIC"); fi
  if [ "$OPENROUTER_DECLARED" = yes ]; then names+=("$OPENROUTER"); fi
  if [ "${#names[@]}" = 0 ]; then
    unmeasured prompt-hidden "neither credential was declared (their rows above say why), so no command printed the hidden prompt this row reads"
    return 0
  fi
  for name in "${names[@]}"; do
    add_streams "$name"
    out=$(cat "${ADD_STREAMS[@]}" 2>/dev/null || true)
    want="Paste the value for credential-${name}-key (input hidden), then press Enter:"
    case "$out" in
    *"$want"*) prompts="${prompts:+$prompts, }credential-${name}-key" ;;
    *) missing="${missing:+$missing, }${name}" ;;
    esac
  done
  if [ -n "$missing" ]; then
    fact prompt-hidden FAIL "the output of fugaro accounts add --type for ${missing} does not carry the command's own hidden prompt ('Paste the value for credential-<name>-key (input hidden), then press Enter:'): the acceptance's clause is that the value is read at THAT prompt with nothing echoed, and a command whose prompt never appeared read the value some other way — a flag, an argument or a pipe. What each command printed is in $SCRATCH/add-anthropic.err and $SCRATCH/add-openrouter.err"
    return 0
  fi
  fact prompt-hidden PASS "each declaration carries the command's own hidden prompt on the command's own output (${prompts}) and no other door was opened: the values you pasted were read at that prompt, by the commands, on your terminal — never handed over by this check on a pipe, a flag or an argument"
}

# step_no_file: the acceptance's "the value in no file under ~/.config/fugaro/",
# read as the shape of a provider key because this check never holds the value.
step_no_file() {
  if [ "$ANTHROPIC_DECLARED" != yes ] && [ "$OPENROUTER_DECLARED" != yes ]; then
    unmeasured no-file "neither credential was declared (their rows above say why), so no value was pasted and there is nothing to look for"
    return 0
  fi
  if [ -z "$FUGARO_CONFIG_DIR" ]; then
    unmeasured no-file "this machine has no HOME, so there is no ~/.config/fugaro to sweep"
    return 0
  fi
  if [ ! -d "$FUGARO_CONFIG_DIR" ]; then
    unmeasured no-file "$FUGARO_CONFIG_DIR does not exist, and the acceptance's clause is about the files under it: declare the credential on the installation whose config lives there, or set HOME, and run the check again"
    return 0
  fi
  sweep shape "$SCRATCH/config-sweep.txt" "$FUGARO_CONFIG_DIR"
  local scanned hits names
  scanned=$(field "$SCRATCH/config-sweep.txt" scanned)
  hits=$(field "$SCRATCH/config-sweep.txt" hits)
  names=$(awk -F'\t' '$1 == "hit" { printf "%s ", $2 }' "$SCRATCH/config-sweep.txt")
  if [ "${hits:-1}" != 0 ]; then
    fact no-file FAIL "a line shaped like a provider key is in ${hits} file(s) under ${FUGARO_CONFIG_DIR}: ${names} — the acceptance's clause is that the declaration holds the credential's type and its name and never its value, and this check holds no value to look for, so the shape is what it reads. The files are named and never printed, and no line of them is in this block"
    return 0
  fi
  fact no-file PASS "no line of the ${scanned} file(s) under ${FUGARO_CONFIG_DIR} (the declaration, the project layer and every .bak-<UTC> backup among them) is shaped like a provider key: the config carries the credential's type and its name, and the values went to Secret Manager"
}

# step_no_history: and in no shell history.
step_no_history() {
  if [ "$ANTHROPIC_DECLARED" != yes ] && [ "$OPENROUTER_DECLARED" != yes ]; then
    unmeasured no-history "neither credential was declared (their rows above say why), so no value was pasted and there is nothing to look for"
    return 0
  fi
  local files=() f
  for f in "${HISTFILE:-}" "${HOME:-}/.bash_history" "${HOME:-}/.zsh_history" "${HOME:-}/.sh_history" "${HOME:-}/.history" "${HOME:-}/.histfile" "${HOME:-}/.local/share/fish/fish_history"; do
    [ -n "$f" ] || continue
    [ -f "$f" ] || continue
    files+=("$f")
  done
  if [ "${#files[@]}" = 0 ]; then
    fact no-history INFO "this machine has no shell history file this check can read (it looked at HISTFILE, ~/.bash_history, ~/.zsh_history, ~/.sh_history, ~/.history, ~/.histfile and fish's): the acceptance's clause is read against the histories that exist, and a value read at a hidden prompt never reached the shell's command line"
    return 0
  fi
  sweep shape "$SCRATCH/history-sweep.txt" "${files[@]}"
  local scanned hits names
  scanned=$(field "$SCRATCH/history-sweep.txt" scanned)
  hits=$(field "$SCRATCH/history-sweep.txt" hits)
  names=$(awk -F'\t' '$1 == "hit" { printf "%s ", $2 }' "$SCRATCH/history-sweep.txt")
  if [ "${hits:-1}" != 0 ]; then
    fact no-history FAIL "a line shaped like a provider key is in ${hits} shell history file(s): ${names} — a value read at a hidden prompt never reaches the shell's command line, so a history holding the shape means it was typed at a prompt that echoes and records. The files are named and never printed"
    return 0
  fi
  fact no-history PASS "no line of the ${scanned} shell history file(s) this machine has (${files[*]}) is shaped like a provider key: the hidden prompt reads the terminal directly, so the shell that started this check never saw a keystroke of it"
}

# step_serving_path: the acceptance's "fugaro accounts ls naming the serving path".
step_serving_path() {
  if [ "$ANTHROPIC_DECLARED" != yes ] && [ "$OPENROUTER_DECLARED" != yes ]; then
    unmeasured serving-path "neither credential was declared (their rows above say why), so there is no serving path to read"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "reading the declaration back: fugaro accounts ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" creds-after "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured serving-path "fugaro accounts ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the serving path of each credential is in that document. What it printed is in $SCRATCH/creds-after.out"
    return 0
  fi
  if ! read_json credentials "$SCRATCH/creds-after.out" "$SCRATCH/creds-after.txt"; then
    unmeasured serving-path "fugaro accounts ls --json printed a document this check could not read: what it printed is in $SCRATCH/creds-after.out"
    return 0
  fi
  local a_type a_path o_type o_path
  a_type=$(table_field "$SCRATCH/creds-after.txt" cred "$ANTHROPIC" 3)
  a_path=$(table_field "$SCRATCH/creds-after.txt" cred "$ANTHROPIC" 4)
  o_type=$(table_field "$SCRATCH/creds-after.txt" cred "$OPENROUTER" 3)
  o_path=$(table_field "$SCRATCH/creds-after.txt" cred "$OPENROUTER" 4)
  local want_a=credsvc want_o=runner
  local problem=""
  if [ "$ANTHROPIC_DECLARED" = yes ]; then
    [ -n "$a_type" ] || problem="${problem:+$problem, }${ANTHROPIC}(not declared)"
    [ "$a_type" = anthropic ] || problem="${problem:+$problem, }${ANTHROPIC}(type ${a_type:-none})"
    [ "$a_path" = "$want_a" ] || problem="${problem:+$problem, }${ANTHROPIC}(serving_path ${a_path:-none}, want ${want_a})"
  fi
  if [ "$OPENROUTER_DECLARED" = yes ]; then
    [ "$o_type" = openrouter ] || problem="${problem:+$problem, }${OPENROUTER}(type ${o_type:-none})"
    [ "$o_path" = "$want_o" ] || problem="${problem:+$problem, }${OPENROUTER}(serving_path ${o_path:-none}, want ${want_o})"
  fi
  if [ -n "$problem" ]; then
    fact serving-path FAIL "fugaro accounts ls --json in ${SANDBOX} reads ${ANTHROPIC} as type ${a_type:-none}/serving_path ${a_path:-none} and ${OPENROUTER} as type ${o_type:-none}/serving_path ${o_path:-none}, and the acceptance's clause is that the listing names each credential's serving path: an Anthropic-key credential is served by the credentials service (${want_a}) and an OpenRouter one by a run's own gateway (${want_o}). What is wrong here: ${problem}"
    return 0
  fi
  fact serving-path PASS "fugaro accounts ls --json names both credentials of ${INSTALLATION} with the half that serves them — ${ANTHROPIC} is type ${a_type} served by ${a_path} (the credentials service, which reads the key out of the bundle), ${OPENROUTER} is type ${o_type} served by ${o_path} (a run's own gateway) — which is the acceptance's clause and the thing that decides where a run's calls are spent"
}

# step_wiring: the check's own pool, and the craft's access pointed at it. This
# is what makes the sandbox run "served by it"; the craft's own access was read
# in step_declaration and is put back in step_unwiring.
step_wiring() {
  if [ "$DECLARED_BEFORE" != yes ]; then
    unmeasured wiring "the craft's access was not read out of the published node of ${INSTALLATION} (the declaration row above says why), so this check will not take one over: settle that row and run the check again"
    return 0
  fi
  if [ "$ANTHROPIC_DECLARED" != yes ]; then
    unmeasured wiring "the credential the run should be served by was not declared (its row above says why), so there is no pool this check may put in one"
    return 0
  fi
  say "wiring the run's craft to the check's own pool: fugaro accounts pool add ${POOL} ${ANTHROPIC} --owner ${OWNER}"
  run_bounded "$CLI_TIMEOUT" pool-add "$SANDBOX" "$FUGARO_BIN" accounts pool add "$POOL" "$ANTHROPIC" --owner "$OWNER"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured wiring "fugaro accounts pool add ${POOL} ${ANTHROPIC} --owner ${OWNER} did not write the pool in ${SANDBOX} (exit ${BOUNDED_STATUS}): a pool published to the policy node needs an owner, and the sandbox installation's own address is what this check passed. Its own words are in $SCRATCH/pool-add.err — pass CHECK631_OWNER with the address this installation serves the pool to, and run the check again"
    return 0
  fi
  POOL_WRITTEN=yes
  say "pointing the craft at it: fugaro accounts craft set ${CRAFT} --access ${POOL}"
  run_bounded "$CLI_TIMEOUT" craft-set "$SANDBOX" "$FUGARO_BIN" accounts craft set "$CRAFT" --access "$POOL"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured wiring "fugaro accounts craft set ${CRAFT} --access ${POOL} did not write the access in ${SANDBOX} (exit ${BOUNDED_STATUS}): the run's stages resolve their credential through a craft's access, so with the access unwritten the run below would not be served by this credential. Its own words are in $SCRATCH/craft-set.err — a craft's access naming a pool is checked against the pool's owner, and CHECK631_OWNER is what this check passed"
    return 0
  fi
  ACCESS_TAKEN=yes
  fact wiring PASS "this check declared the pool ${POOL} with ${ANTHROPIC} as its only member (owner ${OWNER}) and pointed the craft ${CRAFT} at it, where its access was ${PREV_ACCESS} before: the run below is served by the pool and so by the credential this check declared, and both writes are put back at the end"
}

# step_publish: the access half a run reads is the published node's, so the
# check publishes the node its own pool is on.
step_publish() {
  if [ "$ACCESS_TAKEN" != yes ]; then
    unmeasured publish "the craft's access was not pointed at the check's pool (the row above says why), so this check does not publish a node from a config it half-wrote: nothing is written"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "publishing the policy node of ${INSTALLATION}: fugaro project policy publish"
  run_bounded "$PUBLISH_TIMEOUT" publish "$SANDBOX" "$FUGARO_BIN" project policy publish ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured publish "fugaro project policy publish did not finish inside ${PUBLISH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/publish.out and $SCRATCH/publish.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured publish "fugaro project policy publish exited ${BOUNDED_STATUS} in ${SANDBOX}: the node the run's access half is read from was not written. Its own words are in $SCRATCH/publish.err — the two usual refusals are an account that is not an admin of the project and a node carrying no rules marker (which 'fugaro update' deploys)"
    return 0
  fi
  PUBLISHED=yes
  run_bounded "$CLI_TIMEOUT" node "$SANDBOX" "$FUGARO_BIN" project policy show --json ${pflag[@]+"${pflag[@]}"}
  local node_note="the node was not read back"
  if read_json node "$SCRATCH/node.out" "$SCRATCH/node.txt" 2>/dev/null; then
    node_note="version $(field "$SCRATCH/node.txt" version), $(field "$SCRATCH/node.txt" crafts) craft(s), $(field "$SCRATCH/node.txt" pools) pool(s), $(field "$SCRATCH/node.txt" credentials) credential(s), and the craft ${CRAFT} names $(awk -F'\t' -v c="$CRAFT" '$1 == "nodecraft" && $2 == c { print $4; exit }' "$SCRATCH/node.txt")"
  fi
  fact publish PASS "fugaro project policy publish wrote the policy node of ${INSTALLATION} from the config in ${SANDBOX}, and it reads back as ${node_note}: the craft's access half a run resolves is that node's, so the run below is served by the credential this check declared"
}

step_run() {
  if [ "$PUBLISHED" != yes ]; then
    unmeasured run-launched "the node the run's access half is read from was not published (the row above says why), so a run launched here would not be served by the credential this check declared: nothing is launched"
    return 0
  fi
  local run_args=(run --repo "$SANDBOX")
  [ -z "$RECIPE" ] || run_args=(run --repo "$SANDBOX" --recipe "$RECIPE")
  run_args+=(--json "$TASK")
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" "${run_args[@]}"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the run this check exists to read never started. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt" 2>/dev/null; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch names the run this check follows. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout: the rows below follow this run and no other"
}

step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 5)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    # run_bounded appends to its key's output, so each poll starts from an empty
    # file: an answer read on top of the one before it is not one document, and a
    # reader that cannot parse the listing can never see a run end.
    : >"$SCRATCH/rows.out"
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 5
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      run_status=$(row_field 3)
      ended=$(row_field 4)
      stage=$(row_field 5)
      RUN_REF=$(row_field 6)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record and the service's audit lines. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state its record and the service's audit lines are complete in"
}

step_record() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured record "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured record "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured record "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=$(printf '%s' "${RUN_REF:-none}")), so there was no key to read"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${GCP_PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/${key}" --project="${GCP_PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact record FAIL "gs://fugaro-runs-${GCP_PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the run's own record is where the stages name the credential they were served by. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact record FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact record FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's"
    return 0
  fi
  EXECUTION=$(field "$SCRATCH/record.txt" execution)
  local stages said=""
  stages=$(awk -F'\t' '$1 == "stage" { printf "%s=%s/%s ", $2, $3, $4 }' "$SCRATCH/record.txt")
  if [ -n "$stages" ]; then said="; its stages read ${stages}"; fi
  fact record PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), stage=$(field "$SCRATCH/record.txt" stage), outcome=$(field "$SCRATCH/record.txt" outcome), cost.model_usd=$(field "$SCRATCH/record.txt" model_usd))${said}"
}

# step_served_by: the acceptance's "a sandbox run served by it" — read from the
# credentials service's own audit line for the run, and from the record's stages.
step_served_by() {
  if [ -z "$RUN_ID" ] || [ -z "$EXECUTION" ]; then
    unmeasured served-by "there is no terminal run's record to take an execution from: the rows above say why"
    return 0
  fi
  # Two readings of the record's stages: the credential every stage it names,
  # and the stages whose credential is the one this check declared. A record
  # that names another credential is a finding in its own right, so the service's
  # own audit lines are read first and the record settles what they leave open.
  local rec_all="" rec_mine=""
  if [ -f "$SCRATCH/record.txt" ]; then
    rec_all=$(awk -F'\t' '$1 == "stage" && $4 != "" { printf "%s(%s) ", $2, $4 }' "$SCRATCH/record.txt")
    rec_mine=$(awk -F'\t' -v c="$ANTHROPIC" '$1 == "stage" && $4 == c { printf "%s(%s) ", $2, $5 }' "$SCRATCH/record.txt")
  fi
  say "reading the service's own audit lines for ${RUN_ID}: gcloud logging read (credsvc: audit)"
  local filter="jsonPayload.message=\"credsvc: audit\" AND jsonPayload.run=\"$RUN_ID\""
  run_bounded "$CLI_TIMEOUT" audit "$SANDBOX" gcloud logging read --resource-names="projects/$GCP_PROJECT" \
    --limit=2000 --freshness=30d --format=json "$filter"
  local no_audit=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    no_audit="gcloud logging read of the service's audit lines for ${RUN_ID} did not answer (exit ${BOUNDED_STATUS}); what gcloud printed is in $SCRATCH/audit.err"
  elif ! read_json audit "$SCRATCH/audit.out" "$SCRATCH/audit.txt" "$RUN_ID"; then
    no_audit="gcloud logging read answered something this check cannot read as a log for ${RUN_ID}; what gcloud printed is in $SCRATCH/audit.out"
  fi
  if [ -n "$no_audit" ]; then
    if [ -n "$rec_mine" ]; then
      fact served-by PASS "${no_audit}, and the run's own record names ${ANTHROPIC} as the credential its stages were served by (${rec_mine}): the acceptance's clause is that this run was served by the credential this check declared, read from the record where the service's own line could not be read"
      return 0
    fi
    if [ -n "$rec_all" ]; then
      fact served-by FAIL "${no_audit}, and the run's own record names ${rec_all} for its stages — and not ${ANTHROPIC}, the credential this check declared and pointed the craft at: the acceptance's clause is a run served by this credential, and the one surface this build has for it names another"
      return 0
    fi
    unmeasured served-by "${no_audit}, and the record names no stage with a credential at all (the record row above says what it read): which credential answered is written in one of the two and neither answered. Run the check again, or read ${RUN_ID} with 'fugaro watch' and say in the row which credential served it"
    return 0
  fi
  local forward answered="" names=""
  forward=$(field "$SCRATCH/audit.txt" forward)
  local kind name _tier status _err
  while IFS=$'\t' read -r kind name _tier status _err; do
    [ "$kind" = audit ] || continue
    names="${names:+$names, }${name}(${status:-unknown})"
    [ "$status" = ok ] || continue
    answered="${answered:+$answered, }${name}"
  done <"$SCRATCH/audit.txt"
  case ",${answered}," in
  *",$ANTHROPIC,"*)
    fact served-by PASS "the credentials service served ${RUN_ID} as ${ANTHROPIC}, the credential this check declared --type anthropic (the service's own audit lines for this run: ${forward} forward line(s), credentials ${names:-none}): the acceptance's clause is that a sandbox run is served by the credential this check declared, and this is that run's own line"
    return 0
    ;;
  esac
  if [ -n "$answered" ]; then
    fact served-by FAIL "the credentials the service's own audit lines name as having served ${RUN_ID} are ${answered} (${forward} forward line(s), read: ${names:-none}), and not ${ANTHROPIC} — the credential this check declared and pointed the craft at${rec_all:+ (the record names ${rec_all} for its stages)}: the acceptance's clause is a sandbox run served by this credential, and the service's own line, which is the surface that decides what answered a call, says another did"
    return 0
  fi
  if [ -n "$rec_mine" ]; then
    fact served-by PASS "the run's own record names ${ANTHROPIC} as the credential its stages were served by (${rec_mine}) — read from the record because the service's own audit lines for this run name no credential (${forward} forward line(s), read: ${names:-none}): the acceptance's clause is that this run was served by the credential this check declared"
    return 0
  fi
  if [ -n "$rec_all" ]; then
    fact served-by FAIL "the credentials service's ${forward} forward line(s) for ${RUN_ID} name no credential (${names:-none}) — a refused call names none, by design — and the run's own record names ${rec_all} for its stages, and not ${ANTHROPIC}: the acceptance's clause is that a sandbox run is served by the credential this check declared, and the record, the one surface here that names a credential at all, names another"
    return 0
  fi
  if [ "${forward:-0}" = 0 ]; then
    unmeasured served-by "the credentials service wrote no forward line for ${RUN_ID} and the run's record names no stage with a credential: there is no line that says which credential served a call — most often a run that ended before its first model call. Run the check again, or read the run with 'fugaro watch ${RUN_ID}' and say in the row why it made no call"
    return 0
  fi
  unmeasured served-by "the credentials service's ${forward} forward line(s) for ${RUN_ID} carry no credential (${names:-none}) — a refused call names none, by design — and the run's record names no stage with a credential: with every call refused and no stage's credential, neither surface says what answered. What the service wrote is in $SCRATCH/audit.out"
}

# step_unwiring: the craft's access back, and the node published from the config
# the check found. It runs BEFORE the pool is removed, so the config never names
# a pool that does not exist and the node never serves the check's own pool.
step_unwiring() {
  if [ "$ACCESS_TAKEN" != yes ]; then
    fact unwiring INFO "this check took no access over (the wiring row above says why), so there is nothing to put back"
    return 0
  fi
  say "putting the craft's access back: fugaro accounts craft set ${CRAFT} --access ${PREV_ACCESS}"
  run_bounded "$CLI_TIMEOUT" craft-restore "$SANDBOX" "$FUGARO_BIN" accounts craft set "$CRAFT" --access "$PREV_ACCESS"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact unwiring FAIL "fugaro accounts craft set ${CRAFT} --access ${PREV_ACCESS} did not put the access back in ${SANDBOX} (exit ${BOUNDED_STATUS}): the craft still names the check's own pool ${POOL}, which is what this check must not leave behind. Put it back yourself with that command — its own words are in $SCRATCH/craft-restore.err — and read the restore row below"
    return 0
  fi
  ACCESS_TAKEN=no
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "publishing the node the config now declares: fugaro project policy publish"
  run_bounded "$PUBLISH_TIMEOUT" publish-restore "$SANDBOX" "$FUGARO_BIN" project policy publish ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact unwiring FAIL "the craft's access is back to ${PREV_ACCESS}, and fugaro project policy publish did not write the node from that config (exit ${BOUNDED_STATUS}): the published node still carries the check's own pool ${POOL} for the craft ${CRAFT}, which is what a run's access half is read from. Publish it yourself with that command — its own words are in $SCRATCH/publish-restore.err — and read the restore row below"
    return 0
  fi
  PUBLISHED=no
  fact unwiring PASS "the craft ${CRAFT}'s access is back to ${PREV_ACCESS} and the policy node of ${INSTALLATION} was published again from that config, so no run reads the check's own pool ${POOL} any more: the two writes the wiring row made are unwound"
}

# step_rm_refusal: the acceptance's "fugaro accounts rm clearing a pool that
# listed it", read as this build has it — the removal is refused while the pool
# lists the credential, and the sentence names the command that clears it.
step_rm_refusal() {
  if [ "$ANTHROPIC_DECLARED" != yes ]; then
    unmeasured rm-refusal "the credential was not declared (its row above says why), so there is nothing to remove"
    return 0
  fi
  if [ "$POOL_WRITTEN" != yes ]; then
    unmeasured rm-refusal "the check's own pool was not written (the wiring row above says why), so this build's refusal — a removal is refused while a pool lists the credential — has no pool to be refused over"
    return 0
  fi
  say "removing the credential while the pool lists it: fugaro accounts rm ${ANTHROPIC}"
  run_bounded "$CLI_TIMEOUT" rm "$SANDBOX" "$FUGARO_BIN" accounts rm "$ANTHROPIC"
  local out
  out=$(cat "$SCRATCH/rm.out" "$SCRATCH/rm.err" 2>/dev/null | tr -s ' ' || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured rm-refusal "fugaro accounts rm ${ANTHROPIC} did not finish inside ${CLI_TIMEOUT}s and was ended: what it printed is in $SCRATCH/rm.out and $SCRATCH/rm.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" = 0 ]; then
    fact rm-refusal FAIL "fugaro accounts rm ${ANTHROPIC} exited 0 while the pool ${POOL} listed it: the acceptance's clause is that the removal and the pool that listed the credential are reconciled, and this build's own rule is that the removal is refused, naming the pool and the command that clears it. Its own words are in $SCRATCH/rm.out"
    return 0
  fi
  case "$out" in
  *"is still a member of"*)
    case "$out" in
    *"accounts pool rm"*)
      fact rm-refusal PASS "fugaro accounts rm ${ANTHROPIC} exited ${BOUNDED_STATUS} while the pool ${POOL} listed it, and its sentence names the pool and the step that clears it: '${out}' — this build reconciles the two by refusing the removal and naming 'fugaro accounts pool rm', which the row below runs. The acceptance's own words ('clearing a pool that listed it') name a removal that does this in one step, and it is this build's two"
      ;;
    *)
      fact rm-refusal FAIL "fugaro accounts rm ${ANTHROPIC} was refused (exit ${BOUNDED_STATUS}) and its sentence names no step that clears it: '${out}'. The acceptance's clause is that the two are reconciled, and a refusal whose reader is given no next step leaves the credential and the pool in the state they were already in"
      ;;
    esac
    ;;
  *)
    fact rm-refusal FAIL "fugaro accounts rm ${ANTHROPIC} exited ${BOUNDED_STATUS} while the pool ${POOL} listed it, and its own words are not this build's refusal over a pool that lists the credential: '${out}'. Nothing was removed, and the check will not remove anything it cannot account for"
    ;;
  esac
}

# step_rm_clears: the pool's removal, and then the credential's — the two steps
# this build's reconciliation takes.
step_rm_clears() {
  if [ "$POOL_WRITTEN" != yes ] || [ "$ANTHROPIC_DECLARED" != yes ]; then
    unmeasured rm-clears "there is no pool and credential pair to reconcile (the rows above say why)"
    return 0
  fi
  say "clearing the pool: fugaro accounts pool rm ${POOL}"
  run_bounded "$CLI_TIMEOUT" pool-rm "$SANDBOX" "$FUGARO_BIN" accounts pool rm "$POOL"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured rm-clears "fugaro accounts pool rm ${POOL} did not remove the pool in ${SANDBOX} (exit ${BOUNDED_STATUS}): the pool that lists the credential is still the one the removal is refused over. Its own words are in $SCRATCH/pool-rm.err — remove it yourself with that command, then the two credentials with 'fugaro accounts rm'"
    return 0
  fi
  POOL_WRITTEN=no
  say "removing the credential: fugaro accounts rm ${ANTHROPIC}"
  run_bounded "$CLI_TIMEOUT" rm-after "$SANDBOX" "$FUGARO_BIN" accounts rm "$ANTHROPIC"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured rm-clears "fugaro accounts rm ${ANTHROPIC} did not remove the credential once the pool ${POOL} was gone (exit ${BOUNDED_STATUS}): its own words are in $SCRATCH/rm-after.err"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  local openrouter_note="the OpenRouter credential was not declared, so there is none to remove"
  if [ "$OPENROUTER_DECLARED" = yes ]; then
    say "removing the second credential: fugaro accounts rm ${OPENROUTER}"
    run_bounded "$CLI_TIMEOUT" rm-openrouter "$SANDBOX" "$FUGARO_BIN" accounts rm "$OPENROUTER"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      fact rm-clears FAIL "fugaro accounts pool rm ${POOL} and fugaro accounts rm ${ANTHROPIC} both succeeded, and fugaro accounts rm ${OPENROUTER} exited ${BOUNDED_STATUS}: the second credential this check declared is still in ${INSTALLATION}'s config. Its own words are in $SCRATCH/rm-openrouter.err — remove it yourself with that command"
      return 0
    fi
    openrouter_note="the OpenRouter credential is removed too"
  fi
  run_bounded "$CLI_TIMEOUT" creds-final "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local still=""
  if read_json credentials "$SCRATCH/creds-final.out" "$SCRATCH/creds-final.txt" 2>/dev/null; then
    [ -z "$(table_field "$SCRATCH/creds-final.txt" cred "$ANTHROPIC" 2)" ] || still="${still:+$still, }${ANTHROPIC}"
    [ -z "$(table_field "$SCRATCH/creds-final.txt" cred "$OPENROUTER" 2)" ] || still="${still:+$still, }${OPENROUTER}"
  else
    still="(fugaro accounts ls --json could not be read back, so this check cannot say what the listing holds)"
  fi
  if [ -n "$still" ]; then
    fact rm-clears FAIL "the pool ${POOL} was removed and fugaro accounts rm succeeded, and fugaro accounts ls --json still names ${still} in ${INSTALLATION}: the acceptance's clause is that the credential and the pool that listed it are reconciled and the credential is gone. Read the listing in $SCRATCH/creds-final.out"
    return 0
  fi
  REMOVED=yes
  fact rm-clears PASS "fugaro accounts pool rm ${POOL} cleared the pool that listed ${ANTHROPIC}, fugaro accounts rm then removed it, ${openrouter_note}, and fugaro accounts ls --json names neither in ${INSTALLATION}: the acceptance's clause — the removal and the pool that listed the credential — is settled by these two steps in this build, and the refusal the row above read is the first of them"
}

restore() {
  RESTORE_VERDICT=PASS
  local undone="the craft's access is back to what it was" notes=""
  if [ "$ACCESS_TAKEN" = yes ]; then
    undone="the craft ${CRAFT}'s access STILL names the check's own pool ${POOL}: put it back yourself with 'fugaro accounts craft set ${CRAFT} --access ${PREV_ACCESS}', and publish the node again with 'fugaro project policy publish'"
  fi
  if [ "$POOL_WRITTEN" = yes ]; then
    notes="${notes:+$notes; }the pool ${POOL} is still declared: remove it with 'fugaro accounts pool rm ${POOL}'"
  fi
  if [ "$REMOVED" != yes ] && [ "$ANTHROPIC_DECLARED" = yes ]; then
    notes="${notes:+$notes; }the credential ${ANTHROPIC} is still declared: remove it with 'fugaro accounts rm ${ANTHROPIC}' (and the OpenRouter one with 'fugaro accounts rm ${OPENROUTER}') once no pool lists it"
  fi
  RESTORE_NOTE="what this check unwound: ${undone}${notes:+; $notes}. What it does NOT put back: the Secret Manager versions of the two keys (credential-${ANTHROPIC}-key and credential-${OPENROUTER}-key — a value cannot be unlearned; rotate them if they were not yours to spend), and the sandbox run ${RUN_ID:-none} with the branch and pull request it left (outcome=$(field "$SCRATCH/record.txt" outcome), pr=$(field "$SCRATCH/record.txt" pr)) — close that pull request and delete its branch yourself once you have read the run${EXECUTION:+, and delete the execution ${EXECUTION} if you would rather it not wait for its own retention}). The node was published from your own config both times, which is what 'fugaro update' does too, so nothing it holds is anything your config does not declare"
}

cleanup() {
  trap - ERR
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  trap - ERR
  if [ "$PREFLIGHT_MODE" = yes ]; then
    # A preflight wrote nothing and launched nothing: it prints its own lines
    # and no RESULT block.
    remove_scratch
    exit "$status"
  fi
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
  fi
  if [ "$status" = 0 ]; then
    if [ "$failed" = yes ]; then
      status=1
    elif [ -n "$UNMEASURED_KEYS" ]; then
      status=2
    fi
  fi
  print_block "$facts" "$status"
  exit "$status"
}

plan() {
  say "CHECK 631 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --project <name> --all in ${SANDBOX} — the project line: which installation this is"
  say "  [read] fugaro craft ls --json, fugaro project policy show --json, fugaro accounts pool ls and fugaro accounts ls --json in ${SANDBOX} — the craft, its chain, the access the node declares and the pools and credentials as they are"
  say "  [read] CLAUDECODE=1 fugaro accounts add ${ANTHROPIC} --type anthropic — what a coding agent's session is told"
  say "  [YOU, write] fugaro accounts add ${ANTHROPIC} --type anthropic at your own terminal: the COMMAND reads the value at its own hidden prompt — the credential, stored under credential-${ANTHROPIC}-key"
  say "  [YOU, write] fugaro accounts add ${OPENROUTER} --type openrouter at your own terminal, the value again at the command's own hidden prompt"
  say "  [read] the SHAPE of a provider key in the command's own output, in ${FUGARO_CONFIG_DIR:-~/.config/fugaro} and in every shell history file — this check never holds a value, so names and shapes only"
  say "  [write] fugaro accounts pool add ${POOL} ${ANTHROPIC} --owner ${OWNER}, then fugaro accounts craft set ${CRAFT} --access ${POOL}"
  say "  [write] fugaro project policy publish — the node the run's access half is read from"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, followed to its end"
  say "  [read] gcloud storage cat gs://fugaro-runs-<gcp-project>/runs/<slug>/<run>/result.json and gcloud logging read (credsvc: audit for the run)"
  say "  [write] fugaro accounts craft set ${CRAFT} --access ${PREV_ACCESS:-<the access it had>}, fugaro project policy publish, fugaro accounts pool rm ${POOL}, fugaro accounts rm ${ANTHROPIC} and fugaro accounts rm ${OPENROUTER} — the unwinding"
  for key in installation declaration agent-refusal add-anthropic add-openrouter prompt-hidden nothing-echoed no-file no-history serving-path wiring publish run-launched terminal record served-by unwiring rm-refusal rm-clears; do
    fact "$key" INFO "planned: only a real run with your own two declarations at your terminal measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no credential, no pool, no publish, no run and no read"
}

BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation declaration
  agent-refusal add-anthropic add-openrouter prompt-hidden nothing-echoed
  no-file no-history serving-path wiring publish run-launched
  terminal record served-by unwiring rm-refusal rm-clears restore
  scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 631 RESULT\n' >&3
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
  printf 'CHECK 631 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
PREFLIGHT_CLI=""
SANDBOX_CLI=""
PROJECT_CLI=""
ANTHROPIC_CLI=""
OPENROUTER_CLI=""
POOL_CLI=""
CRAFT_CLI=""
TASK_CLI=""
RECIPE_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --project)
    [ $# -ge 2 ] || die "--project needs the installation's own name"
    PROJECT_CLI=$2
    shift 2
    ;;
  --credential)
    [ $# -ge 2 ] || die "--credential needs the name the Anthropic-key credential is declared under"
    ANTHROPIC_CLI=$2
    shift 2
    ;;
  --openrouter)
    [ $# -ge 2 ] || die "--openrouter needs the name the OpenRouter credential is declared under"
    OPENROUTER_CLI=$2
    shift 2
    ;;
  --pool)
    [ $# -ge 2 ] || die "--pool needs the name the check's own pool is declared under"
    POOL_CLI=$2
    shift 2
    ;;
  --craft)
    [ $# -ge 2 ] || die "--craft needs the craft whose access the sandbox run is served through"
    CRAFT_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
    ;;
  --preflight)
    PREFLIGHT_CLI=1
    shift
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${CHECK631_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 631 needs --sandbox: the checkout of a repository of the sandbox project. Usage: bash scripts/live-checks/check631.sh --sandbox <a sandbox repository checkout>"
fi
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PROJECT_ARG=${CHECK631_PROJECT:-${PROJECT_CLI:-}}
PROJECT=$PROJECT_ARG
ANTHROPIC=${CHECK631_ANTHROPIC:-${ANTHROPIC_CLI:-chk631-anthropic}}
OPENROUTER=${CHECK631_OPENROUTER:-${OPENROUTER_CLI:-chk631-openrouter}}
POOL=${CHECK631_POOL:-${POOL_CLI:-chk631-pool}}
CRAFT=${CHECK631_CRAFT:-${CRAFT_CLI:-coder}}
TASK=${CHECK631_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK631_RECIPE:-${RECIPE_CLI:-}}
DRY_RUN=${CHECK631_DRY_RUN:-$DRY_RUN_CLI}
PREFLIGHT=${CHECK631_PREFLIGHT:-$PREFLIGHT_CLI}
PREFLIGHT_MODE=no
[ -z "$PREFLIGHT" ] || PREFLIGHT_MODE=yes
KEEP_SCRATCH=${CHECK631_KEEP_SCRATCH:-}
REGION=${CHECK631_REGION:-us-east5}
RUN_TIMEOUT=${CHECK631_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK631_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK631_CLI_TIMEOUT_SECONDS:-180}
DOCTOR_TIMEOUT=${CHECK631_DOCTOR_TIMEOUT_SECONDS:-300}
LAUNCH_TIMEOUT=${CHECK631_LAUNCH_TIMEOUT_SECONDS:-600}
PUBLISH_TIMEOUT=${CHECK631_PUBLISH_TIMEOUT_SECONDS:-600}
TTY_DEV=${CHECK631_TTY:-/dev/tty}
FUGARO_BIN=$(command -v fugaro || true)
# The fugaro config directory the acceptance's "no file under ~/.config/fugaro/"
# is about, resolved the way internal/localcfg resolves it.
if [ -n "${HOME:-}" ]; then
  FUGARO_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro"
else
  FUGARO_CONFIG_DIR=""
fi
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac
OWNER=${CHECK631_OWNER:-$ACCOUNT}

open_terminal() {
  [ -n "$TTY_DEV" ] || return 0
  if [ ! -c "$TTY_DEV" ]; then return 0; fi
  if [ ! -r "$TTY_DEV" ] || [ ! -w "$TTY_DEV" ]; then return 0; fi
  # The brace group, and never `exec 5<... 2>/dev/null`: a bare `exec` with two
  # redirections applies BOTH of them to this shell, and the 2>/dev/null would
  # send every line this check prints afterwards into /dev/null.
  if { exec 5<"$TTY_DEV"; } 2>/dev/null; then
    if [ -t 5 ]; then
      HAVE_TTY=yes
    else
      exec 5<&-
    fi
  fi
  return 0
}

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 631 needs 'fugaro' on PATH: the check declares two credentials with the fugaro you have, and it is not there"
  # The checkout is a precondition of every step below and of the terminal gate
  # itself: it is read here rather than inside the steps, so a --sandbox that is
  # not one is refused before the check asks anyone to stand at a terminal.
  [ -d "$SANDBOX" ] || die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  [ -f "$SANDBOX/fugaro.yaml" ] || die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the credentials, the pool and the run are all read in it"
  scratch_up
  trap on_error ERR
  write_reader
  write_sweeper
  write_terminal_runner
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -n "$PREFLIGHT" ]; then
    step_preflight
    preflight_report
  fi
  open_terminal
  if [ "$HAVE_TTY" != yes ]; then
    stop "this check needs your own terminal: each credential's value is read at the hidden prompt of the command that declares it, and there is no terminal here (stdin is not one). Run it at your terminal — 'bash scripts/live-checks/check631.sh --sandbox <a sandbox repository checkout>' — and not through a pipe, a redirect or a coding agent's session"
  fi
  step_installation
  step_declaration
  step_agent_refusal
  step_add_anthropic
  step_add_openrouter
  step_prompt_hidden
  step_nothing_echoed
  step_no_file
  step_no_history
  step_serving_path
  step_wiring
  step_publish
  step_run
  step_terminal
  step_record
  step_served_by
  step_unwiring
  step_rm_refusal
  step_rm_clears
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
