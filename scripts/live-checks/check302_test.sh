#!/usr/bin/env bash
# Tests for scripts/live-checks/check302.sh, against a fake fugaro and a fake
# terminal: nothing here reaches the network, there is no cloud and no daemon,
# and every reading the check makes about an installation is a file this test
# wrote.
#
# The two things a fake cannot provide are the check's own seams, and there are
# none here beyond the word typed at the terminal: the machine label comes from
# the real `uname`, the window's dates come from the real clock, and the
# confirmation comes from faketty.py, which gives a child a REAL terminal on its
# standard input and types the phase's word at the prompt the check prints — the
# same reading `[ -t 0 ]` makes, rather than a variable standing in for it.
#
# Usage: bash scripts/live-checks/check302_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check302.sh
# By absolute path: the missing-tool case replaces PATH with a directory that
# holds nothing, and `env` would then find no bash, no timeout and no python3 at
# all.
BASH_BIN=$(command -v bash)
TIMEOUT_BIN=$(command -v timeout)
PYTHON_BIN=$(command -v python3)
case "$(uname -s 2>/dev/null || echo unknown)" in
Linux | Darwin) ;;
*) echo "check302_test: this test needs timeout and a POSIX shell: run it on Linux or macOS" >&2 ;;
esac
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK302_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

# faketty.py gives a child a real terminal on its standard input, leaves its
# standard output and standard error in the two files the caller names, and
# types WORD once the child prints the confirmation prompt. It waits for the
# prompt rather than for a timer, so a case is not a race, and it gives up after
# fifteen seconds so a case that would hang instead fails.
#   faketty.py WORD OUT ERR LOG -- CMD ARGS...
cat >"$work/faketty.py" <<'PY'
import os, pty, select, sys, time

argv = sys.argv[1:]
word, outfile, errfile, logfile = argv[0], argv[1], argv[2], argv[3]
cmd = argv[5:]

master, slave = pty.openpty()
pid = os.fork()
if pid == 0:
    os.close(master)
    os.setsid()
    try:
        os.dup2(slave, 0)
        fo = os.open(outfile, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        os.dup2(fo, 1)
        fe = os.open(errfile, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        os.dup2(fe, 2)
    except OSError:
        os._exit(3)
    os.execvp(cmd[0], cmd)
    os._exit(127)
os.close(slave)

log = open(logfile, "wb")
sent = False
reaped = None
deadline = time.time() + 15
while time.time() < deadline:
    r, _, _ = select.select([master], [], [], 0.02)
    if master in r:
        try:
            data = os.read(master, 4096)
        except OSError:
            data = b""
        if not data:
            # The child closed the terminal, which it does on the way out: the
            # exit status still has to be collected below, so this is not a
            # reason to kill anything.
            break
        log.write(data)
    if not sent:
        try:
            with open(errfile, "rb") as fh:
                seen = fh.read()
        except OSError:
            seen = b""
        if b"to go ahead:" in seen:
            # The prompt is on the terminal's other side and the check is
            # blocked reading it: this is the typed word and nothing else.
            os.write(master, (word + "\n").encode())
            sent = True
    done, st = os.waitpid(pid, os.WNOHANG)
    if done == pid:
        reaped = st
        break
log.close()
if reaped is None:
    # A short grace so a child that has just closed the terminal is waited for
    # rather than killed, and the case's exit status is its own.
    grace = time.time() + 3
    while time.time() < grace:
        done, st = os.waitpid(pid, os.WNOHANG)
        if done == pid:
            reaped = st
            break
        time.sleep(0.02)
if reaped is None:
    os.kill(pid, 9)
    _, reaped = os.waitpid(pid, 0)
os.close(master)
if os.WIFEXITED(reaped):
    sys.exit(os.WEXITSTATUS(reaped))
sys.exit(128 + os.WTERMSIG(reaped))
PY

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s' "$out" | grep -E '^FACT ' | sed 's/^/ ... block: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2" "${3:-$1} (FACT $1)"
}
# expect_no_call EXACT-LINE what: the fake fugaro logged no call whose line is
# exactly this one, which is what tells "the check never ran the import" from
# "the check asked the import's --help".
expect_no_call() { # line what
  if grep -qx "$1" "$dir/fake/calls"; then
    fail "$2: the check ran '$1'"
  fi
}
# expect_no_call_match REGEX what: the same, for a call whose arguments this
# case cannot name. The release gate reads `orchestrator import --help` and
# `orchestrator uninstall --help` on every run, so a bare substring would always
# match those two pages and never the command a phase must not reach: every
# pattern here is anchored and shaped like the command itself.
expect_no_call_match() { # regex what
  local hit
  hit=$(grep -E "$1" "$dir/fake/calls" | head -1 || true)
  [ -z "$hit" ] || fail "$2: the check ran '$hit'"
}
# The two commands the cutover and the rollback really run, and the reads the
# phases after them make: what a phase that must act on nothing is checked for.
# `uninstall` is anchored alone because that is the whole command.
LIVE_ORCH_RE='^orchestrator (import -f|uninstall$|run|status|ls|state|events|patch|resume)'
IMPORT_RE='^orchestrator import -f'
UNINSTALL_RE='^orchestrator uninstall$'
expect_block_keys() { # what keys...
  local what=$1
  shift
  expect_contains "$out" "CHECK 302 RESULT" "$what: the block"
  local key
  for key in "$@"; do
    expect_contains "$out" "FACT $key:" "$what: the $key row"
  done
  expect_contains "$out" "CHECK 302 RESULT END (exit " "$what: the block's end"
}
expect_state_key() { # key value what
  local got
  got=$(sed -n "s/^$1=//p" "$(state_file)" | head -1)
  [ "$got" = "$2" ] || fail "${3:-state}: $1 is '$got', want '$2'"
}

# The installation every case runs against, and the machine label the real uname
# produces: the check compares the two, so the test computes it the same way.
INSTALLATION=sandbox302
GCP_PROJECT=prj-sandbox302
# Every fact key each phase's block must carry, whatever it measured. Pinned
# here rather than read from the script: a row that disappears from the block is
# a finding, and a test that asked the script which rows to expect would not see
# it.
BEFORE_KEYS=(phase installation agent release xtrace guardrails scripts terminal human-guardrails human-launching guardrails-kept window state human-next restore)
CUTOVER_KEYS=(phase installation release window import daemon finished launch notification scripts terminal state human-next restore)
ROLLBACK_KEYS=(phase installation release window uninstall daemon-stopped lease-after no-rollback-command scripts terminal rearm launch-scripts state human-next restore)
FORGET_KEYS=(phase installation state restore)

machine_label() { printf '%s:%s' "$(uname -s | tr '[:upper:]' '[:lower:]')" "$(uname -n | tr '[:upper:]' '[:lower:]')"; }

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check302.sh's test: every answer it gives comes out of
# $FAKE302_DIR, and every call is recorded, so a case can say what the check
# asked and what it never asked. Nothing here reaches the network.
set -uo pipefail
D=${FAKE302_DIR:?}
printf '%s\n' "$*" >>"$D/calls"

case "${1:-}" in
version)
  cat "$D/cli.version"
  ;;
budget)
  case "${2:-}" in
  show)
    # The second read is what the guardrails-kept row compares against: a case
    # that had a cap not put back answers with the second document from the
    # second call on.
    n=$(wc -l <"$D/budget.calls" 2>/dev/null || echo 0)
    printf 'show\n' >>"$D/budget.calls"
    if [ -e "$D/budget.notrestored" ] && [ "$n" -ge 1 ]; then
      cat "$D/budget2.json"
    else
      cat "$D/budget.json"
    fi
    ;;
  *)
    echo "fugaro: unexpected budget subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
runs)
  case "${2:-}" in
  ls) cat "$D/runs.json" ;;
  *)
    echo "fugaro: unexpected runs subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
orchestrator)
  case "${2:-}" in
  import)
    # A release that has no such command answers neither the command nor its
    # --help, which is what the release gate reads.
    if [ -e "$D/no-import-verb" ]; then
      echo "unknown command \"import\" for \"fugaro orchestrator\"" >&2
      exit 1
    fi
    if [ "${3:-}" = "--help" ]; then echo "Usage: fugaro orchestrator import [flags]"; exit 0; fi
    [ -r "${4:-}" ] || { echo "import: no such dump: ${4:-}" >&2; exit 1; }
    if [ -e "$D/import.fail" ]; then
      echo "import: the dump carries rev 7 and the node is at rev 9" >&2
      exit 1
    fi
    printf 'import %s\n' "${4:-}" >>"$D/imports"
    echo "imported 3 ops as a person: orchestrator $DNAME is at rev 8"
    echo "2 work items the dump declares arrived unapproved: w2, w3" >&2
    ;;
  uninstall)
    if [ "${3:-}" = "--help" ]; then echo "Usage: fugaro orchestrator uninstall [flags]"; exit 0; fi
    if [ -e "$D/uninstall.fail" ]; then
      echo "uninstall: the login unit could not be unloaded" >&2
      exit 1
    fi
    if [ -e "$D/unit.keep" ]; then
      # The unit is still installed afterwards, which is the shape of an
      # uninstall that did not stop the daemon at all.
      printf 'uninstall\n' >>"$D/uninstalls"
      echo "Removed $D/unit/fugaro-orchestrator.plist and unloaded it with launchd."
    elif [ -e "$D/unit.installed" ]; then
      rm -f "$D/unit.installed"
      # The real command takes no argument, so the line names the command and
      # not a value: a fake that echoed "$3" would break on the call the check
      # really makes.
      printf 'uninstall\n' >>"$D/uninstalls"
      echo "Removed $D/unit/fugaro-orchestrator.plist and unloaded it with launchd. The daemon is not started at login any more; 'fugaro orchestrator install' writes the unit back."
    else
      echo "no login unit is installed at $D/unit/fugaro-orchestrator.plist: nothing to remove"
    fi
    ;;
  status)
    if [ "${3:-}" = "--help" ]; then echo "Usage: fugaro orchestrator status [flags]"; exit 0; fi
    if [ -e "$D/unit.installed" ]; then
      cat "$D/unit.status"
    else
      echo "the login unit is not installed: $D/unit/fugaro-orchestrator.plist does not exist"
    fi
    ;;
  ls)
    # The listing answered once the uninstall has removed the unit is the one
    # a case writes as 'after-uninstall': a daemon that stopped frees the
    # lease, and a case that wants it still held leaves that file absent.
    if [ ! -e "$D/unit.installed" ] && [ -f "$D/ls.after-uninstall.json" ]; then
      cat "$D/ls.after-uninstall.json"
    else
      cat "$D/ls.json"
    fi
    ;;
  state)
    if [ "${3:-}" = "--help" ]; then echo "Usage: fugaro orchestrator state [flags]"; exit 0; fi
    cat "$D/state.json"
    ;;
  rollback)
    if [ -e "$D/rollback.exists" ]; then
      echo "Usage: fugaro orchestrator rollback [flags]"
      exit 0
    fi
    echo "unknown command \"rollback\" for \"fugaro orchestrator\"" >&2
    exit 1
    ;;
  *)
    echo "fugaro: unexpected orchestrator subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_config() { # dir name
  mkdir -p "$1/cfg/fugaro/projects"
  cat >"$1/cfg/fugaro/projects/$2.yaml" <<EOF
version: 1
name: $2
gcp_project: $GCP_PROJECT
region: europe-west1
EOF
}

write_budget() { # dir mode
  local mode=${2:-fresh}
  case "$mode" in
  fresh)
    cat >"$1/fake/budget.json" <<'JSON'
{"version":1,"kind":"budget-show","project":"sandbox302","gcp_project":"prj-sandbox302","mode":"enforce","day":"2026-11-13","max_reserve_usd":200,
 "global":{"daily_cap_usd":50,"per_run_cap_usd":5,"counted_usd":12.5,"spent_usd":10,"notional_usd":10,"leased_usd":2.5,"headroom_usd":37.5,"kill":null},
 "defaults":{"repo_daily_cap_usd":5,"repo_per_run_cap_usd":2},"repos":[],"running":[],"warnings":[]}
JSON
    ;;
  killed)
    cat >"$1/fake/budget.json" <<'JSON'
{"version":1,"kind":"budget-show","project":"sandbox302","gcp_project":"prj-sandbox302","mode":"enforce","day":"2026-11-13","max_reserve_usd":200,
 "global":{"daily_cap_usd":50,"per_run_cap_usd":5,"counted_usd":12.5,"spent_usd":10,"notional_usd":10,"leased_usd":2.5,"headroom_usd":37.5,
  "kill":{"by":"owner@example.com","at":"2026-11-13T09:00:00Z","reason":"check302"}},
 "defaults":{},"repos":[],"running":[],"warnings":[]}
JSON
    ;;
  unreadable)
    printf 'this is not a document\n' >"$1/fake/budget.json"
    ;;
  esac
  # The second read answers with this one: by default the same document, which
  # is what "the test's own writes were put back" means.
  cp "$1/fake/budget.json" "$1/fake/budget2.json"
  : >"$1/fake/budget.calls"
}

# write_brief writes `state --json`'s document around one brief, shaped the way
# internal/orchestrator.Summarize renders it: the goal, the counts, one block
# per item, then the decisions and the facts.
write_brief() { # dir mode
  local mode=$1 out=$1/fake/brief.text
  case "$2" in
  fresh)
    cat >"$out" <<'BRIEF'
goal: cut this installation over to the daemon
work items: 3, 2 still open
  w1 [done] the first item — acme/app, web, standard
    approved by owner@example.com at 2026-11-13T09:00:00Z, attempt 1 of 1
    run run-aaa
  w2 [running] the second item — acme/app, web, standard
    approved by owner@example.com at 2026-11-13T09:30:00Z, attempt 1 of 1
    run run-bbb
    after w1
  w3 [queued] the third item — acme/app, web, standard

decisions: 0, 0 waiting

facts: 0
BRIEF
    ;;
  no-finished)
    cat >"$out" <<'BRIEF'
goal: cut this installation over to the daemon
work items: 2, 2 still open
  w1 [queued] the first item — acme/app, web, standard
  w2 [queued] the second item — acme/app, web, standard
    after w1

decisions: 0, 0 waiting

facts: 0
BRIEF
    ;;
  no-launch)
    cat >"$out" <<'BRIEF'
goal: cut this installation over to the daemon
work items: 2, 1 still open
  w1 [done] the first item — acme/app, web, standard
    approved by owner@example.com at 2026-11-13T09:00:00Z, attempt 1 of 1
    run run-aaa
  w2 [queued] the second item — acme/app, web, standard

decisions: 0, 0 waiting

facts: 0
BRIEF
    ;;
  esac
  "$PYTHON_BIN" -c '
import json, sys
brief = open(sys.argv[1], encoding="utf-8").read()
json.dump({"version": 1, "kind": "state", "id": "sandbox302", "source": "node",
           "rev": 8, "schema_version": 1, "brief": brief}, open(sys.argv[2], "w", encoding="utf-8"))
' "$out" "$1/fake/state.json"
}

write_ls() { # dir mode
  case "$2" in
  held)
    cat >"$1/fake/ls.json" <<'JSON'
{"version":1,"kind":"orchestrator-ls","orchestrators":[
 {"kind":"local","project":"sandbox302","lease_holder":"the-owners-macbook","updated_at":"2026-11-13T09:59:00Z","cached_at":"2026-11-13T09:58:00Z"}]}
JSON
    ;;
  free)
    cat >"$1/fake/ls.json" <<'JSON'
{"version":1,"kind":"orchestrator-ls","orchestrators":[
 {"kind":"local","project":"sandbox302","cached_at":"2026-11-13T09:58:00Z"}]}
JSON
    ;;
  central)
    cat >"$1/fake/ls.json" <<'JSON'
{"version":1,"kind":"orchestrator-ls","orchestrators":[
 {"kind":"central","project":"sandbox302","lease_holder":"cloud-run-7"}]}
JSON
    ;;
  after-uninstall)
    cat >"$1/fake/ls.after-uninstall.json" <<'JSON'
{"version":1,"kind":"orchestrator-ls","orchestrators":[
 {"kind":"local","project":"sandbox302","cached_at":"2026-11-13T09:58:00Z"}]}
JSON
    ;;
  esac
}

write_runs() { # dir mode
  case "$2" in
  fresh)
    cat >"$1/fake/runs.json" <<'JSON'
{"version":1,"kind":"runs","runs":[
 {"run_id":"run-aaa","status":"done","repo":"acme/app","requested_by":"owner@example.com","launched_by":"sandbox302","terminal":true,"settled":true,"created":"2026-11-13T09:20:00Z"},
 {"run_id":"run-bbb","status":"running","repo":"acme/app","launched_by":"sandbox302","created":"2026-11-13T09:40:00Z"}],
 "totals":{},"warnings":[]}
JSON
    ;;
  without-the-finished-one)
    cat >"$1/fake/runs.json" <<'JSON'
{"version":1,"kind":"runs","runs":[
 {"run_id":"run-bbb","status":"running","repo":"acme/app","launched_by":"sandbox302","created":"2026-11-13T09:40:00Z"}],
 "totals":{},"warnings":[]}
JSON
    ;;
  after-rollback)
    cat >"$1/fake/runs.json" <<'JSON'
{"version":1,"kind":"runs","runs":[
 {"run_id":"run-ccc","status":"running","repo":"acme/app","requested_by":"owner@example.com","created":"2026-11-13T10:30:00Z"},
 {"run_id":"run-aaa","status":"done","repo":"acme/app","launched_by":"sandbox302","terminal":true,"settled":true,"created":"2026-11-13T09:20:00Z"}],
 "totals":{},"warnings":[]}
JSON
    ;;
  esac
}

write_log() { # dir mode
  case "$2" in
  fresh)
    cat >"$1/daemon.log" <<'LOG'
orchestrator sandbox302: driving it as the-owners-macbook, one cycle every 30s
orchestrator sandbox302: idle (0 folded, 0 carried)
orchestrator sandbox302: started (1 folded, 1 carried)
run-finished run-aaa: the pull request is ready — https://example.test/acme/app/pull/12
orchestrator sandbox302: idle (0 folded, 0 carried)
LOG
    ;;
  quiet)
    cat >"$1/daemon.log" <<'LOG'
orchestrator sandbox302: driving it as the-owners-macbook, one cycle every 30s
orchestrator sandbox302: idle (0 folded, 0 carried)
LOG
    ;;
  esac
}

setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/fake" "$dir/tmp" "$dir/home" "$dir/statehome" "$dir/none" "$dir/scripts"
  write_fakes "$dir"
  write_config "$dir" "$INSTALLATION"
  : >"$dir/fake/calls"
  : >"$dir/fake/imports"
  : >"$dir/fake/uninstalls"
  printf '0.24.0\n' >"$dir/fake/cli.version"
  write_budget "$dir"
  write_brief "$dir" fresh
  write_ls "$dir" held
  write_runs "$dir" fresh
  write_log "$dir" fresh
  printf 'the login unit at %s is installed and current (launchd)\n' "$dir/fake/unit/fugaro-orchestrator.plist" >"$dir/fake/unit.status"
  : >"$dir/fake/unit.installed"
  printf 'the session dump\n' >"$dir/dump.json"
  printf '#!/usr/bin/env bash\n# the sweep\n' >"$dir/scripts/sweep.sh"
  printf '#!/usr/bin/env bash\n# the Monitor watcher\n' >"$dir/scripts/watcher.sh"
  printf '#!/usr/bin/env bash\n# the queue\n' >"$dir/scripts/queue.sh"
  RUN302_ARGS=()
  RUN302_ENV=()
  RUN302_INSTALLATION=""
  RUN302_XDG_CONFIG_HOME=""
  RUN302_XDG_STATE_HOME=""
  RUN302_PATH=""
  RUN302_WORD=""
  RUN302_TTY=""
  RUN302_TIMEOUT=""
  RUN302_BASH=()
  RUN302_SCRIPT=""
}

# state_file is the one file the check keeps between the phases.
state_file() { printf '%s\n' "$dir/statehome/fugaro/check302/state"; }

# seed_state writes the state file `before` would have written, so that the
# phases after it can be tested without running it: one case runs the two in
# sequence to prove the handoff itself.
seed_state() { # seed_state [half] [stage]
  mkdir -p "$dir/statehome/fugaro/check302"
  : >"$dir/statehome/fugaro/check302/.check302-mine"
  local now end
  now=$(date -u '+%s')
  end=$((now + 14 * 86400))
  cat >"$(state_file)" <<EOF
half=${1:-cutover}
stage=${2:-pending-cutover}
machine=$(machine_label)
installation=$INSTALLATION
scripts_dir=$dir/scripts
guardrails=mode=enforce daily=50.0000 per-run=5.0000 kill=off
window_start=$(date -u -r "$now" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d "@$now" '+%Y-%m-%dT%H:%M:%SZ')
window_end=$(date -u -r "$end" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d "@$end" '+%Y-%m-%dT%H:%M:%SZ')
window_end_epoch=$end
at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
EOF
  chmod 600 "$(state_file)"
}

# seed_expired_state is the same file from a window that has closed: the two
# phases that read it must refuse rather than read a window that is over.
seed_expired_state() {
  seed_state cutover pending-cutover
  local old
  old=$(($(date -u '+%s') - 86400))
  sed -i.bak "s/^window_end_epoch=.*/window_end_epoch=$old/" "$(state_file)"
  rm -f "$(state_file).bak"
}

run302() {
  status=0
  : >"$dir/out"
  : >"$dir/err"
  : >"$dir/tty"
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # is itself started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  local common=(
    HOME="$dir/home" TMPDIR="$dir/tmp" FAKE302_DIR="$dir/fake" DNAME="$INSTALLATION"
    XDG_CONFIG_HOME="${RUN302_XDG_CONFIG_HOME:-$dir/cfg}"
    XDG_STATE_HOME="${RUN302_XDG_STATE_HOME:-$dir/statehome}"
    PATH="${RUN302_PATH:-$dir/bin:/usr/bin:/bin}"
    FUGARO_PROJECT="${RUN302_INSTALLATION-$INSTALLATION}"
  )
  if [ -n "${RUN302_TTY:-}" ]; then
    # A real terminal on the child's standard input, and the phase's word typed
    # at the prompt: the confirmation is what this drives.
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
      "${common[@]}" ${RUN302_ENV[@]+"${RUN302_ENV[@]}"} \
      "$TIMEOUT_BIN" "${RUN302_TIMEOUT:-30}" "$PYTHON_BIN" "$work/faketty.py" "$RUN302_WORD" \
      "$dir/out" "$dir/err" "$dir/tty" -- \
      "$BASH_BIN" ${RUN302_BASH[@]+"${RUN302_BASH[@]}"} "${RUN302_SCRIPT:-$script}" ${RUN302_ARGS[@]+"${RUN302_ARGS[@]}"} "$@" >/dev/null 2>&1 || status=$?
  else
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
      "${common[@]}" ${RUN302_ENV[@]+"${RUN302_ENV[@]}"} \
      "$TIMEOUT_BIN" "${RUN302_TIMEOUT:-30}" "$BASH_BIN" ${RUN302_BASH[@]+"${RUN302_BASH[@]}"} "${RUN302_SCRIPT:-$script}" ${RUN302_ARGS[@]+"${RUN302_ARGS[@]}"} "$@" \
      >"$dir/out" 2>"$dir/err" || status=$?
  fi
  out=$(cat "$dir/out" 2>/dev/null || true)
  errtext=$(cat "$dir/err" 2>/dev/null || true)
}

# phase WORD PHASE ARGS...: one real run of a phase, with the word typed at the
# terminal the check asks at.
phase() { # phase WORD ARGS...
  RUN302_TTY=yes
  RUN302_WORD=$1
  shift
  run302 "$@"
  RUN302_TTY=""
  RUN302_WORD=""
}

# dryrun ARGS...: a dry run, which asks for nothing and so needs no terminal.
dryrun() {
  run302 "$@" --dry-run
}

# --------------------------------------------------------------- book-keeping
# before_ok runs `before` and fails loudly if it did not get through: the cases
# after it read what it recorded.
before_ok() {
  phase before before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 0 "before: the exit"
}

# --------------------------------------------------------------------- cases
# case_help: the usage, and no block. A CHECK 302 RESULT for a run that ran
# nothing would be a verdict about a cutover this run never looked at.
case_help() {
  setup_case help
  run302 --help
  expect_status "$status" 0 "--help: the exit"
  expect_contains "$out" "usage: bash scripts/live-checks/check302.sh before|cutover|rollback|forget" "--help: the usage line"
  expect_contains "$out" "before (the preconditions and the window)" "--help: the phases"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_no_call_match "$LIVE_ORCH_RE" "--help: nothing was asked of fugaro"
}

# case_unknown_argument and case_no_phase: an argument this script cannot mean
# is refused rather than ignored, and a run with no phase has nothing to do.
case_unknown_argument() {
  setup_case unknown
  run302 watch
  expect_status "$status" 2 "unknown argument: the exit"
  expect_contains "$errtext" "unknown argument 'watch'" "unknown argument: what it said"
  expect_contains "$errtext" "usage: bash scripts/live-checks/check302.sh" "unknown argument: the usage"
}

case_no_phase() {
  setup_case no-phase
  run302
  expect_status "$status" 2 "no phase: the exit"
  expect_contains "$errtext" "name the phase to run" "no phase: what it said"
}

# case_dry_run_before: the plan, the guards, and nothing changed. The state
# directory the real run would write into is not even there.
case_dry_run_before() {
  setup_case dry-run-before
  dryrun before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 0 "dry run: the exit"
  expect_block_keys "dry run" "${BEFORE_KEYS[@]}" dry-run
  expect_fact dry-run PASS "dry run: the row"
  expect_fact guardrails PASS "dry run: the guardrails were read"
  expect_fact window PASS "dry run: the window it would start"
  expect_contains "$errtext" "test the caps and the kill switch FIRST" "dry run: the plan names the human step"
  expect_contains "$errtext" "fugaro budget kill --all --reason check302" "dry run: the plan names the exact command"
  expect_contains "$errtext" "the plan, for installation $INSTALLATION" "dry run: the plan is addressed to the installation"
  expect_absent "$(cat "$dir/out")" "FACT human-guardrails: PASS" "dry run: nothing was confirmed"
  if [ -e "$dir/statehome/fugaro" ]; then
    fail "dry run: it made a state directory: $(find "$dir/statehome" -mindepth 1 | head -3)"
  fi
  expect_no_call_match "$LIVE_ORCH_RE" "dry run: a daemon command was reached"
}

# case_before_happy: the whole phase on a machine that is ready. The guardrails
# are read, the scripts are found, the two human steps are recorded on the
# owner's own word, the caps are read again and the window is dated.
case_before_happy() {
  setup_case before-happy
  phase before before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 0 "before: the exit"
  expect_block_keys "before" "${BEFORE_KEYS[@]}"
  expect_fact agent PASS "before: the session guard"
  expect_fact release PASS "before: the release"
  expect_contains "$out" "FACT guardrails: PASS — the guardrails this phase read before anything started: the daily cap is \$50.0000, the per-run cap is \$5.0000" "before: the caps it read"
  expect_contains "$out" "the project's kill switch is off" "before: the switch it read"
  expect_fact scripts PASS "before: the scripts"
  expect_contains "$out" "FACT scripts: PASS — 3 file(s) are in $dir/scripts" "before: it counted them"
  expect_fact terminal PASS "before: the terminal"
  expect_fact human-guardrails PASS "before: the human step, recorded"
  expect_fact human-launching PASS "before: the other human step"
  expect_fact guardrails-kept PASS "before: the caps were put back"
  expect_fact window PASS "before: the window"
  expect_fact state INFO "before: the state file"
  expect_fact human-next INFO "before: the next step"
  expect_fact restore PASS "before: the scratch"
  [ -f "$(state_file)" ] || fail "before: it recorded no state file at $(state_file)"
  expect_state_key half cutover "before"
  expect_state_key stage pending-cutover "before"
  expect_state_key installation "$INSTALLATION" "before"
  expect_state_key scripts_dir "$dir/scripts" "before"
  expect_state_key machine "$(machine_label)" "before"
  expect_state_key guardrails "mode=enforce daily=50.0000 per-run=5.0000 kill=off" "before"
  if [ -z "$(find "$(state_file)" -perm 600 2>/dev/null)" ]; then
    fail "before: the state file is not 0600: $(ls -l "$(state_file)")"
  fi
  expect_contains "$errtext" "fugaro budget resume --all --reason check302" "before: the plan names the kill switch command"
  expect_contains "$errtext" "fugaro orchestrator run > <file> 2>&1" "before: the plan names starting the daemon"
  expect_contains "$errtext" "bash scripts/live-checks/check302.sh cutover --dump" "before: the next step's exact command"
  expect_no_call_match "$IMPORT_RE" "before: nothing was imported"
  if [ -n "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ]; then
    fail "before: the scratch directory was left behind: $(find "$dir/tmp" -mindepth 1 | head -3)"
  fi
}

# case_before_no_terminal: the one confirmation needs a terminal. A piped
# answer is exactly what a step that starts a daemon must not take.
case_before_no_terminal() {
  setup_case before-no-terminal
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "no terminal: the exit"
  expect_fact terminal FAIL "no terminal: the row"
  expect_contains "$out" "FACT terminal: FAIL — this check's standard input is not a terminal" "no terminal: why"
  expect_absent "$out" "FACT human-guardrails: PASS" "no terminal: nothing was confirmed"
  [ ! -f "$(state_file)" ] || fail "no terminal: it recorded a state file anyway"
}

# case_before_wrong_word: the phase goes ahead on its own word and nothing else.
case_before_wrong_word() {
  setup_case before-wrong-word
  phase yes before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "wrong word: the exit"
  expect_contains "$out" "FACT aborted: FAIL — you typed 'yes'" "wrong word: what it said"
  expect_absent "$out" "FACT human-guardrails: PASS" "wrong word: nothing was recorded as confirmed"
  [ ! -f "$(state_file)" ] || fail "wrong word: it recorded a state file anyway"
}

# case_before_belong: the register refuses the sandbox by name from an agent's
# session, and this check refuses it however it arrives.
case_before_belong() {
  setup_case belong
  RUN302_INSTALLATION=belong
  run302 before --scripts "$dir/scripts"
  expect_status "$status" 2 "belong: the exit"
  expect_fact installation FAIL "belong: the row"
  expect_contains "$out" "FACT installation: FAIL — the installation in force is belong, which the register refuses by name" "belong: why"
  expect_no_call_match "$LIVE_ORCH_RE" "belong: nothing was asked of the installation"
}

# case_before_no_installation: with nothing naming an installation the check
# cannot say which one it is cutting over, so it says so rather than guessing.
case_before_no_installation() {
  setup_case no-installation
  RUN302_INSTALLATION=""
  RUN302_XDG_CONFIG_HOME="$dir/none"
  run302 before --scripts "$dir/scripts"
  expect_status "$status" 2 "no installation: the exit"
  expect_contains "$out" "FACT aborted: FAIL — no installation is selected" "no installation: what it said"
}

# case_before_old_release: O11's import is released with 0.24.0. A check that
# ran against an older binary would fail obscurely on an unknown command, so it
# stops with one sentence naming the release it needs.
case_before_old_release() {
  setup_case old-release
  printf '0.23.4\n' >"$dir/fake/cli.version"
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "old release: the exit"
  expect_fact release FAIL "old release: the row"
  expect_contains "$out" "needs release 0.24.0" "old release: the release it needs"
  expect_contains "$errtext" "run 'fugaro update'" "old release: how to get there"
  expect_absent "$(cat "$dir/fake/calls")" "budget show" "old release: nothing else was read"
}

# case_before_dev_release: a build from a checkout is not a release.
case_before_dev_release() {
  setup_case dev-release
  printf 'dev\n' >"$dir/fake/cli.version"
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "dev release: the exit"
  expect_contains "$out" "FACT release: FAIL — this fugaro reports 'dev'" "dev release: why"
  expect_contains "$out" "needs release 0.24.0" "dev release: the release it needs"
}

# case_before_missing_import: the same gate on the command rather than the
# version — a released fugaro whose orchestrator family has no import.
case_before_missing_import() {
  setup_case missing-import
  : >"$dir/fake/no-import-verb"
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "missing import: the exit"
  expect_fact release FAIL "missing import: the row"
  expect_contains "$out" "carries no 'fugaro orchestrator import'" "missing import: the command it needs"
  expect_contains "$out" "needs release 0.24.0" "missing import: the release it needs"
}

# case_before_kill_switch_on: the cutover under a project whose kill switch is
# on would show none of the three facts it is for.
case_before_kill_switch_on() {
  setup_case kill-switch
  write_budget "$dir" killed
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "kill switch: the exit"
  expect_fact guardrails FAIL "kill switch: the row"
  expect_contains "$out" "the project's kill switch is ON (set by owner@example.com at 2026-11-13T09:00:00Z, \"check302\")" "kill switch: who set it"
  expect_contains "$errtext" "fugaro budget resume --all --reason check302" "kill switch: how to clear it"
  [ ! -f "$(state_file)" ] || fail "kill switch: it opened a window anyway"
}

# case_before_guardrails_not_restored: the step people forget. The cap the test
# of the guardrails set is still there on the second read, and the phase refuses
# to start a window the daemon would launch under.
case_before_guardrails_not_restored() {
  setup_case guardrails-not-restored
  : >"$dir/fake/budget.notrestored"
  cat >"$dir/fake/budget2.json" <<'JSON'
{"version":1,"kind":"budget-show","project":"sandbox302","mode":"enforce","day":"2026-11-13",
 "global":{"daily_cap_usd":0.01,"per_run_cap_usd":5,"counted_usd":12.5,"spent_usd":10,"headroom_usd":0,"kill":null},
 "defaults":{},"repos":[],"running":[],"warnings":[]}
JSON
  # The second budget read is after the confirmation, so this case types the
  # word like the owner does.
  phase before before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "not restored: the exit"
  expect_fact guardrails-kept FAIL "not restored: the row"
  expect_contains "$out" "the daily cap was \$50.0000 and is \$0.0100" "not restored: what moved"
  expect_contains "$out" "Put them back" "not restored: how"
  [ ! -f "$(state_file)" ] || fail "not restored: it opened a window with the caps moved"
}

# case_before_budget_unreadable: a budget document this check cannot read is a
# FAIL, not a pass on an empty reading.
case_before_budget_unreadable() {
  setup_case budget-unreadable
  write_budget "$dir" unreadable
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "unreadable budget: the exit"
  expect_fact guardrails FAIL "unreadable budget: the row"
  expect_contains "$out" "printed a document this check cannot read" "unreadable budget: what it said"
}

# case_before_no_scripts_dir and case_before_scripts_empty: the rollback's
# second step re-arms the sweep, the watcher and the queue scripts, so the
# window is not worth opening where they are not.
case_before_no_scripts_dir() {
  setup_case no-scripts-dir
  run302 before --installation "$INSTALLATION" --scripts "$dir/nowhere"
  expect_status "$status" 2 "no scripts directory: the exit"
  expect_fact scripts FAIL "no scripts directory: the row"
  expect_contains "$out" "there is no directory at $dir/nowhere" "no scripts directory: what it said"
  [ ! -f "$(state_file)" ] || fail "no scripts directory: it opened a window anyway"
}

case_before_scripts_empty() {
  setup_case scripts-empty
  rm -f "$dir/scripts/sweep.sh" "$dir/scripts/watcher.sh" "$dir/scripts/queue.sh"
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "empty scripts directory: the exit"
  expect_fact scripts FAIL "empty scripts directory: the row"
  expect_contains "$out" "holds no file" "empty scripts directory: what it said"
}

# case_before_twice: `before` starts the window, and running it twice would move
# the 14 days T1 allows.
case_before_twice() {
  setup_case before-twice
  before_ok
  expect_status "$status" 0 "before twice: the first run"
  phase before before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "before twice: the second run"
  expect_contains "$out" "FACT state: INFO — the readings 'before' records are already at" "before twice: what it said"
  expect_contains "$errtext" "running it twice would move the 14 days" "before twice: why it refused"
}

# case_before_agent_session: this check is owner-run, and a coding agent's
# session is refused by the same marker internal/agentenv reads.
case_before_agent_session() {
  setup_case agent-session
  RUN302_ENV=(CLAUDECODE=1)
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "agent session: the exit"
  expect_fact agent FAIL "agent session: the row"
  expect_contains "$out" "FACT agent: FAIL — this session carries the coding agent's marker CLAUDECODE" "agent session: why"
  expect_absent "$(cat "$dir/fake/calls")" "budget show" "agent session: nothing was read"
}

# case_before_xtrace: a trace of a run that reads the budget's document is not
# the block anyone can paste back.
case_before_xtrace() {
  setup_case xtrace
  RUN302_BASH=(-x)
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "xtrace: the exit"
  expect_fact xtrace FAIL "xtrace: the row"
  expect_contains "$out" "this shell arrived with tracing on" "xtrace: why"
}

# case_state_dir_is_not_ours: a directory this check did not write is never
# clobbered. The marker is what tells the two apart.
case_state_dir_is_not_ours() {
  setup_case foreign-state-dir
  mkdir -p "$dir/statehome/fugaro/check302"
  printf 'something of mine\n' >"$dir/statehome/fugaro/check302/notes.txt"
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "foreign state dir: the exit"
  expect_contains "$errtext" "carries no marker from this check" "foreign state dir: why it refused"
  [ -f "$dir/statehome/fugaro/check302/notes.txt" ] || fail "foreign state dir: it removed a file it did not write"
}

# case_missing_tool: a tool this check needs and cannot find stops the run with
# one line naming it.
case_missing_tool() {
  setup_case missing-tool
  # Everything this check shells out to is on PATH here, and fugaro is not: the
  # gate that names the tool it needs is what this case reads.
  mkdir -p "$dir/min"
  local tool
  for tool in bash sh mktemp chmod mkdir rm mv cp sed awk find python3 uname date \
    tr grep cat cut head tail sort wc sleep env dirname basename; do
    ln -s "$(command -v "$tool")" "$dir/min/$tool" 2>/dev/null || true
  done
  RUN302_PATH="$dir/min"
  run302 before --installation "$INSTALLATION" --scripts "$dir/scripts"
  expect_status "$status" 2 "missing tool: the exit"
  expect_contains "$errtext" "needs 'fugaro' on PATH and it is not there" "missing tool: what it said"
}

# case_forget: the one thing the check leaves behind is its own state file, and
# this is how it goes.
case_forget() {
  setup_case forget
  seed_state
  run302 forget
  expect_status "$status" 0 "forget: the exit"
  expect_block_keys "forget" "${FORGET_KEYS[@]}"
  expect_fact state PASS "forget: the row"
  [ ! -f "$(state_file)" ] || fail "forget: the state file is still there"
  [ -f "$dir/scripts/sweep.sh" ] || fail "forget: it removed something that is not its own"
  expect_no_call_match "$LIVE_ORCH_RE" "forget: it read no installation"
}

case_forget_nothing() {
  setup_case forget-nothing
  run302 forget
  expect_status "$status" 0 "forget with nothing: the exit"
  expect_contains "$out" "FACT state: PASS — there is no state file at" "forget with nothing: what it said"
}

# case_cutover_without_before: the phase reads the window from what `before`
# recorded and will not start a cutover this check never prepared.
case_cutover_without_before() {
  setup_case cutover-without-before
  run302 cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "cutover without before: the exit"
  expect_fact state FAIL "cutover without before: the row"
  expect_no_call_match "$IMPORT_RE" "cutover without before: nothing was imported"
}

# case_cutover_foreign_state: the window and the scripts' directory are that
# machine's.
case_cutover_foreign_state() {
  setup_case cutover-foreign-state
  seed_state
  sed -i.bak 's/^machine=.*/machine=sunos:someone-elses-box/' "$(state_file)"
  rm -f "$(state_file).bak"
  run302 cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "foreign state: the exit"
  expect_contains "$out" "FACT state: FAIL — the state file at $(state_file) was written by sunos:someone-elses-box" "foreign state: what it said"
}

# case_cutover_window_expired: the cutover this check is for is the one that
# runs while the scripts are still on disk inside the 14 days.
case_cutover_window_expired() {
  setup_case cutover-window-expired
  seed_expired_state
  run302 cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "expired window: the exit"
  expect_fact window FAIL "expired window: the row"
  expect_contains "$out" "the 14 days T1 allows ended" "expired window: what it said"
  expect_no_call_match "$IMPORT_RE" "expired window: nothing was imported"
}

# case_cutover_happy: the whole phase. The dump lands, one daemon holds the
# installation's lease, the node names a finished run and a launch whose
# dependencies are done, the daemon's own output carries a notification.
case_cutover_happy() {
  setup_case cutover-happy
  before_ok
  expect_status "$status" 0 "cutover: the before run"
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 0 "cutover: the exit"
  expect_block_keys "cutover" "${CUTOVER_KEYS[@]}"
  expect_fact window PASS "cutover: the window is still open"
  expect_fact import PASS "cutover: the import"
  expect_contains "$out" "imported 3 ops as a person" "cutover: the import's own words"
  expect_contains "$out" "arrives unapproved" "cutover: what the dump does not do"
  expect_fact daemon PASS "cutover: the daemon"
  expect_contains "$out" "the listing names it as holder the-owners-macbook" "cutover: the lease holder"
  expect_fact finished PASS "cutover: the finished run"
  expect_contains "$out" "FACT finished: PASS — one finished run: the daemon folded run run-aaa into item w1" "cutover: the run it was about"
  expect_contains "$out" "the installation's own listing names the run as 'done'" "cutover: the run's own outcome"
  expect_fact launch PASS "cutover: the dependency-driven launch"
  expect_contains "$out" "FACT launch: PASS — one dependency-driven launch: item w2 waits on w1" "cutover: the item that waited"
  expect_contains "$out" "run run-bbb" "cutover: the launch's run"
  expect_contains "$out" "under the approval the node records (owner@example.com)" "cutover: the approval"
  expect_contains "$out" "the cycle that carried it" "cutover: the daemon's own cycle"
  expect_fact notification PASS "cutover: the notification"
  expect_contains "$out" "run-finished run-aaa: the pull request is ready" "cutover: the notification's own line"
  expect_fact scripts PASS "cutover: the scripts are still on disk"
  expect_fact state INFO "cutover: the state is kept for the rollback"
  expect_state_key stage pending-facts "cutover"
  expect_state_key imported yes "cutover"
  grep -qx "import $dir/dump.json" "$dir/fake/imports" || fail "cutover: the dump was not imported"
  expect_no_call_match '^orchestrator run' "cutover: the check started no daemon"
  if [ -n "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ]; then
    fail "cutover: the scratch directory was left behind: $(find "$dir/tmp" -mindepth 1 | head -3)"
  fi
}

# case_cutover_no_dump and case_cutover_no_daemon_log: the two files this phase
# reads are named to it, and the run stops before it asks for anything.
case_cutover_no_dump() {
  setup_case cutover-no-dump
  seed_state
  run302 cutover --dump "$dir/nowhere.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "no dump: the exit"
  expect_fact import FAIL "no dump: the row"
  expect_no_call_match "$IMPORT_RE" "no dump: nothing was imported"
}

case_cutover_no_daemon_log() {
  setup_case cutover-no-daemon-log
  seed_state
  run302 cutover --dump "$dir/dump.json" --daemon-log "$dir/nowhere.log"
  expect_status "$status" 2 "no daemon log: the exit"
  expect_fact notification FAIL "no daemon log: the row"
  expect_contains "$out" "no readable daemon output at" "no daemon log: what it said"
  expect_no_call "import $dir/dump.json" "no daemon log: the dump was imported before the channel was checked"
}

# case_cutover_no_daemon: no daemon is beating the installation's lease, so the
# cutover has not happened. The state is kept so the same phase can be run
# again once it has.
case_cutover_no_daemon() {
  setup_case cutover-no-daemon
  seed_state
  write_ls "$dir" free
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "no daemon: the exit"
  expect_fact daemon FAIL "no daemon: the row"
  expect_contains "$out" "names no daemon holding this installation's lease" "no daemon: what it said"
  expect_contains "$out" "FACT state: INFO — the state file $(state_file) is KEPT" "no daemon: the state is kept to run it again"
  [ -f "$(state_file)" ] || fail "no daemon: it dropped the state a re-run needs"
}

# case_cutover_central_only: a central orchestrator with a holder is not this
# machine's daemon, so the listing does not answer the question.
case_cutover_central_only() {
  setup_case cutover-central
  seed_state
  write_ls "$dir" central
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "central only: the exit"
  expect_fact daemon FAIL "central only: the row"
}

# case_cutover_import_fails: the dump did not land, so nothing after it is
# measured and the phase says so.
case_cutover_import_fails() {
  setup_case cutover-import-fails
  seed_state
  : >"$dir/fake/import.fail"
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "import fails: the exit"
  expect_fact import FAIL "import fails: the row"
  expect_contains "$out" "carries rev 7 and the node is at rev 9" "import fails: the command's own words"
  expect_state_key imported "" "import fails"
}

# case_cutover_already_imported: a dump carries the revision it was built from,
# so a second import of the same document is a refusal — a re-run of the phase
# reads the node and does not import again.
case_cutover_already_imported() {
  setup_case cutover-already-imported
  seed_state
  sed -i.bak 's/^stage=.*/stage=pending-facts/' "$(state_file)"
  printf 'imported=yes\n' >>"$(state_file)"
  rm -f "$(state_file).bak"
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 0 "already imported: the exit"
  expect_contains "$out" "FACT import: INFO — the dump was imported by an earlier run of this phase" "already imported: what it said"
  expect_no_call "import $dir/dump.json" "already imported: it imported the same dump twice"
  expect_fact finished PASS "already imported: the readings still happen"
}

# case_cutover_no_finished: the register's first fact is one finished run, and
# it is the daemon folding one that makes the cutover real.
case_cutover_no_finished() {
  setup_case cutover-no-finished
  seed_state
  write_brief "$dir" no-finished
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 1 "no finished run: the exit"
  expect_fact finished FAIL "no finished run: the row"
  expect_contains "$out" "names no work item that is finished" "no finished run: what it said"
  expect_fact launch FAIL "no finished run: no launch either"
  expect_fact notification PASS "no finished run: the notification still reads"
}

# case_cutover_run_not_listed: the node names a run the installation's own
# listing does not carry, so the run's outcome cannot be read back.
case_cutover_run_not_listed() {
  setup_case cutover-run-not-listed
  seed_state
  write_runs "$dir" without-the-finished-one
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 1 "run not listed: the exit"
  expect_fact finished FAIL "run not listed: the row"
  expect_contains "$out" "does not name that run" "run not listed: what it said"
}

# case_cutover_no_launch: an item with a run and no `after` is work a person or
# a session started, not the dependency-driven launch the register names.
case_cutover_no_launch() {
  setup_case cutover-no-launch
  seed_state
  write_brief "$dir" no-launch
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 1 "no launch: the exit"
  expect_fact finished PASS "no launch: the finished run reads"
  expect_fact launch FAIL "no launch: the row"
  expect_contains "$out" "waited on another and went once it was done" "no launch: what it said"
}

# case_cutover_no_notification: a notification is a channel and not a record,
# and the daemon's own output is where this check finds it.
case_cutover_no_notification() {
  setup_case cutover-no-notification
  seed_state
  write_log "$dir" quiet
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 1 "no notification: the exit"
  expect_fact notification FAIL "no notification: the row"
  expect_contains "$out" "carries no notification line" "no notification: what it said"
}

# case_cutover_scripts_gone: the way back is the scripts, and something removed
# them.
case_cutover_scripts_gone() {
  setup_case cutover-scripts-gone
  seed_state
  rm -f "$dir/scripts/sweep.sh" "$dir/scripts/watcher.sh" "$dir/scripts/queue.sh"
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 1 "scripts gone: the exit"
  expect_fact scripts FAIL "scripts gone: the row"
  expect_contains "$out" "the way back T1 keeps for 14 days is gone" "scripts gone: what it said"
}

# case_cutover_belong: every phase refuses the sandbox by name, not only the
# first.
case_cutover_belong() {
  setup_case cutover-belong
  seed_state
  RUN302_INSTALLATION=belong
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 2 "cutover belong: the exit"
  expect_contains "$out" "FACT installation: FAIL — the installation in force is belong" "cutover belong: why"
  expect_no_call_match "$IMPORT_RE" "cutover belong: nothing was imported"
}

# case_rollback_without_before: the rollback's 14 days are dated from `before`.
case_rollback_without_before() {
  setup_case rollback-without-before
  run302 rollback
  expect_status "$status" 2 "rollback without before: the exit"
  expect_fact state FAIL "rollback without before: the row"
  expect_no_call_match "$UNINSTALL_RE" "rollback without before: the daemon was left alone"
}

# case_rollback_window_expired: what the register reads is the rollback inside
# the 14 days.
case_rollback_window_expired() {
  setup_case rollback-window-expired
  seed_expired_state
  run302 rollback
  expect_status "$status" 2 "rollback expired: the exit"
  expect_fact window FAIL "rollback expired: the row"
  expect_contains "$out" "the rollback Check 302 reads is the one inside the 14 days" "rollback expired: what it said"
  expect_no_call_match "$UNINSTALL_RE" "rollback expired: the daemon was left alone"
}

# case_rollback_happy: the whole way back. The daemon is uninstalled, the unit
# is gone, the lease is free, no rollback command exists, the scripts are on
# disk, the owner re-armed them and one launch went through them.
case_rollback_happy() {
  setup_case rollback-happy
  seed_state cutover pending-facts
  write_ls "$dir" after-uninstall
  write_runs "$dir" after-rollback
  phase rollback rollback --launched run-ccc
  expect_status "$status" 0 "rollback: the exit"
  expect_block_keys "rollback" "${ROLLBACK_KEYS[@]}"
  expect_fact window PASS "rollback: the window"
  expect_fact uninstall PASS "rollback: the daemon is stopped"
  expect_contains "$out" "Removed $dir/fake/unit/fugaro-orchestrator.plist and unloaded it with launchd" "rollback: the command's own words"
  expect_fact daemon-stopped PASS "rollback: the unit is gone"
  expect_contains "$out" "FACT daemon-stopped: PASS — the login unit is gone" "rollback: the status reading"
  expect_fact lease-after PASS "rollback: the lease is free"
  expect_fact no-rollback-command PASS "rollback: no rollback command exists"
  expect_contains "$out" "there is no 'fugaro orchestrator rollback'" "rollback: what it said"
  expect_fact rearm PASS "rollback: the human step, recorded"
  expect_fact launch-scripts PASS "rollback: the launch through the scripts"
  expect_contains "$out" "run run-ccc is in the installation's own listing (running, acme/app)" "rollback: the run it read back"
  expect_fact state PASS "rollback: the state file is removed"
  [ ! -f "$(state_file)" ] || fail "rollback: the state file is still there"
  grep -qx "uninstall" "$dir/fake/uninstalls" || fail "rollback: the daemon was never uninstalled"
  [ -f "$dir/scripts/sweep.sh" ] || fail "rollback: it removed a script"
}

# case_rollback_without_launched: the re-arm and the launch are the owner's, so
# the phase reads what it can, records that the last reading is not measured
# yet, and keeps the state so the same phase can be run again with the run id.
case_rollback_without_launched() {
  setup_case rollback-without-launched
  seed_state cutover pending-facts
  write_ls "$dir" after-uninstall
  phase rollback rollback
  expect_status "$status" 2 "rollback without a run id: the exit"
  expect_fact uninstall PASS "rollback without a run id: the daemon is stopped"
  expect_fact rearm PASS "rollback without a run id: the re-arm is recorded"
  expect_contains "$out" "FACT launch-scripts: INFO — not measured yet" "rollback without a run id: what it said"
  expect_contains "$errtext" "--launched <run-id>" "rollback without a run id: the step it asks for"
  expect_contains "$out" "FACT state: INFO — the state file $(state_file) is KEPT" "rollback without a run id: the state is kept"
  expect_state_key stage pending-launch "rollback without a run id"
}

# case_rollback_second_run: the uninstall already happened, and the state says
# so — the second run reads and does not uninstall twice.
case_rollback_second_run() {
  setup_case rollback-second-run
  seed_state cutover pending-launch
  write_runs "$dir" after-rollback
  rm -f "$dir/fake/unit.installed"
  write_ls "$dir" after-uninstall
  phase rollback rollback --launched run-ccc
  expect_status "$status" 0 "second run: the exit"
  expect_contains "$out" "FACT uninstall: INFO — the daemon was stopped by an earlier run of this phase" "second run: what it said"
  expect_no_call_match "$UNINSTALL_RE" "second run: it uninstalled again"
  expect_fact launch-scripts PASS "second run: the launch reads"
  [ ! -f "$(state_file)" ] || fail "second run: the state file is still there"
}

# case_rollback_unit_still_there: the uninstall ran and the unit is still
# installed, so the rollback did not stop the daemon at all.
case_rollback_unit_still_there() {
  setup_case rollback-unit-still-there
  seed_state cutover pending-facts
  printf 'the login unit at %s is installed and current (launchd)\n' "$dir/fake/unit/fugaro-orchestrator.plist" >"$dir/fake/unit.status"
  # The unit is installed before the uninstall and still installed after it:
  # the fake's 'unit.keep' is what leaves it there.
  : >"$dir/fake/unit.keep"
  phase rollback rollback
  expect_status "$status" 2 "unit still there: the exit"
  expect_fact daemon-stopped FAIL "unit still there: the row"
  expect_contains "$out" "the unit the daemon was started from is still there" "unit still there: what it said"
}

# case_rollback_lease_held: a lease still held after the uninstall means a
# daemon is still beating it.
case_rollback_lease_held() {
  setup_case rollback-lease-held
  seed_state cutover pending-facts
  phase rollback rollback
  expect_status "$status" 2 "lease held: the exit"
  expect_fact uninstall PASS "lease held: the uninstall ran"
  expect_fact daemon-stopped PASS "lease held: the unit is gone"
  expect_fact lease-after FAIL "lease held: the row"
  expect_contains "$out" "the installation's lease is still held (the-owners-macbook)" "lease held: who holds it"
  expect_contains "$out" "FACT state: INFO — the state file $(state_file) is KEPT" "lease held: the state is kept"
}

# case_rollback_command_exists: the register's reading is that NO rollback
# command is built, and a release that grew one is a finding for the register.
case_rollback_command_exists() {
  setup_case rollback-command-exists
  seed_state cutover pending-facts
  write_ls "$dir" after-uninstall
  write_runs "$dir" after-rollback
  : >"$dir/fake/rollback.exists"
  phase rollback rollback --launched run-ccc
  expect_status "$status" 1 "rollback command exists: the exit"
  expect_fact no-rollback-command FAIL "rollback command exists: the row"
  expect_contains "$out" "A rollback command that exists is a finding for the register" "rollback command exists: what it said"
}

# case_rollback_no_scripts: the third step is re-arming scripts that are not
# there.
case_rollback_no_scripts() {
  setup_case rollback-no-scripts
  seed_state cutover pending-facts
  rm -f "$dir/scripts/sweep.sh" "$dir/scripts/watcher.sh" "$dir/scripts/queue.sh"
  phase rollback rollback
  expect_status "$status" 2 "no scripts: the exit"
  expect_fact scripts FAIL "no scripts: the row"
  expect_contains "$out" "there is nothing to re-arm" "no scripts: what it said"
}

# case_rollback_launched_unknown: the id the owner names is not in the
# installation's own listing, so it is not the proof the reading is for.
case_rollback_launched_unknown() {
  setup_case rollback-launched-unknown
  seed_state cutover pending-facts
  phase rollback rollback --launched run-zzz
  expect_status "$status" 2 "unknown run id: the exit"
  expect_fact launch-scripts FAIL "unknown run id: the row"
  expect_contains "$out" "does not name run 'run-zzz'" "unknown run id: what it said"
  expect_contains "$out" "FACT state: INFO — the state file $(state_file) is KEPT" "unknown run id: the state is kept"
}

# case_dry_run_cutover and case_dry_run_rollback: the plan of a phase that
# changes something, with nothing changed.
case_dry_run_cutover() {
  setup_case dry-run-cutover
  seed_state
  dryrun cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  expect_status "$status" 0 "dry run cutover: the exit"
  expect_block_keys "dry run cutover" "${CUTOVER_KEYS[@]}" dry-run
  expect_fact dry-run PASS "dry run cutover: the row"
  expect_fact import INFO "dry run cutover: the import is not run"
  expect_contains "$errtext" "fugaro orchestrator import -f $dir/dump.json" "dry run cutover: the plan names the exact command"
  expect_no_call_match "$IMPORT_RE" "dry run cutover: the dump was imported"
  [ -f "$(state_file)" ] || fail "dry run cutover: it removed the state file"
}

case_dry_run_rollback() {
  setup_case dry-run-rollback
  seed_state cutover pending-facts
  dryrun rollback
  expect_status "$status" 0 "dry run rollback: the exit"
  expect_block_keys "dry run rollback" "${ROLLBACK_KEYS[@]}" dry-run
  expect_fact uninstall INFO "dry run rollback: the uninstall is not run"
  expect_contains "$errtext" "fugaro orchestrator uninstall" "dry run rollback: the plan names the exact command"
  expect_no_call_match "$UNINSTALL_RE" "dry run rollback: the daemon was uninstalled"
  [ -f "$(state_file)" ] || fail "dry run rollback: it removed the state file"
}

# case_the_notification_reading_catches_a_blind_script: the assertion above is
# worth only as much as its catching a script that lost the reading. This is the
# mutation: a copy whose notification grep matches any line at all, run against
# the case whose daemon output carries none — the suite's own expectation
# (notification FAIL) has to see it.
case_the_notification_reading_catches_a_blind_script() {
  setup_case notification-mutation
  seed_state
  write_log "$dir" quiet
  local copy=$dir/blind302.sh
  # The one line the notification row reads through, with the pattern it greps
  # replaced by one that matches any line: python3 rewrites it because the
  # pattern carries a '|' of its own.
  python3 - "$script" "$copy" <<'MUTATE'
import sys
src = open(sys.argv[1], encoding="utf-8").read()
old = "  grep -E '^(run-finished|decision-needed) ' \"$1\""
new = "  grep -E '^' \"$1\""
if src.count(old) != 1:
    raise SystemExit("the mutation found %d places to blind" % src.count(old))
open(sys.argv[2], "w", encoding="utf-8").write(src.replace(old, new))
MUTATE
  grep -q "^  grep -E '\^' \"\$1\"" "$copy" || fail "mutation: the rewrite found no place to blind the notification reading"
  # The mutated copy is run exactly as the real one is, with the word typed at
  # its terminal: the reading this case is about is measured and not assumed.
  RUN302_SCRIPT=$copy
  phase cutover cutover --dump "$dir/dump.json" --daemon-log "$dir/daemon.log"
  RUN302_SCRIPT=""
  expect_contains "$out" "FACT notification: PASS" \
    "mutation: a script that lost the notification reading was NOT caught, so the case's notification FAIL assertion is vacuous"
}

# Sharding, so the Go test in scripts/ can run this file's cases side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th case starting at i, and
# CHECK_TEST_ONLY=name runs that one case alone. Neither set: all run.
case_index=0
t() {
  local i=$case_index
  case_index=$((case_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t case_help
t case_unknown_argument
t case_no_phase
t case_dry_run_before
t case_before_happy
t case_before_no_terminal
t case_before_wrong_word
t case_before_belong
t case_before_no_installation
t case_before_old_release
t case_before_dev_release
t case_before_missing_import
t case_before_kill_switch_on
t case_before_guardrails_not_restored
t case_before_budget_unreadable
t case_before_no_scripts_dir
t case_before_scripts_empty
t case_before_twice
t case_before_agent_session
t case_before_xtrace
t case_state_dir_is_not_ours
t case_missing_tool
t case_forget
t case_forget_nothing
t case_cutover_without_before
t case_cutover_foreign_state
t case_cutover_window_expired
t case_cutover_happy
t case_cutover_no_dump
t case_cutover_no_daemon_log
t case_cutover_no_daemon
t case_cutover_central_only
t case_cutover_import_fails
t case_cutover_already_imported
t case_cutover_no_finished
t case_cutover_run_not_listed
t case_cutover_no_launch
t case_cutover_no_notification
t case_cutover_scripts_gone
t case_cutover_belong
t case_rollback_without_before
t case_rollback_window_expired
t case_rollback_happy
t case_rollback_without_launched
t case_rollback_second_run
t case_rollback_unit_still_there
t case_rollback_lease_held
t case_rollback_command_exists
t case_rollback_no_scripts
t case_rollback_launched_unknown
t case_dry_run_cutover
t case_dry_run_rollback
t case_the_notification_reading_catches_a_blind_script

if [ "$failures" -ne 0 ]; then
  echo "check302_test: $failures failure(s)" >&2
  exit 1
fi
echo "check302_test: all cases passed"
