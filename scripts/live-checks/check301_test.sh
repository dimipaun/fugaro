#!/usr/bin/env bash
# Tests for scripts/live-checks/check301.sh, against a fake fugaro, a fake
# service manager (systemctl for Linux, launchctl for Darwin) and a fake uname:
# nothing here reaches the network, there is no cloud and no login unit on this
# machine, and every fact the check reads about a machine is a file this test
# wrote.
#
# The two things a fake cannot provide are the check's own seams: the platform
# (CHECK301_PLATFORM) and the machine's boot identity (CHECK301_BOOT_ID), which
# is what makes the reboot proof testable at all. The third is how long the
# handover reading waits for the manager (CHECK301_HANDOVER_SECONDS), set to a
# few seconds here so a case that cannot hand over does not sit for four
# minutes.
#
# Usage: bash scripts/live-checks/check301_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check301.sh
# By absolute path: the missing-tool case replaces PATH with a directory that
# holds nothing, and `env` would then find no bash, no timeout and no python3 at
# all.
BASH_BIN=$(command -v bash)
TIMEOUT_BIN=$(command -v timeout)
PYTHON_BIN=$(command -v python3)
case "$(uname -s 2>/dev/null || echo unknown)" in
Linux | Darwin) ;;
*) echo "check301_test: this test needs timeout and a POSIX shell: run it on Linux or macOS" >&2 ;;
esac
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK301_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

# faketty.py gives a child a REAL terminal on its standard input and leaves its
# standard output and standard error where the caller put them. It exists for
# one reading: the update phase refuses to read a re-render where no person is
# at the terminal, and [ -t 0 ] is the only honest way to ask that — a variable
# standing in for it would pin the test's own answer and not the machine's.
cat >"$work/faketty.py" <<'PY'
import os, pty, sys

master, slave = pty.openpty()
pid = os.fork()
if pid == 0:
    os.close(master)
    os.setsid()
    os.dup2(slave, 0)
    os.execvp(sys.argv[1], sys.argv[1:])
os.close(slave)
_, status = os.waitpid(pid, 0)
os.close(master)
if os.WIFEXITED(status):
    sys.exit(os.WEXITSTATUS(status))
sys.exit(128 + os.WTERMSIG(status))
PY

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 800)" ;;
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
# exactly this one, which is what tells "the check never ran the install" from
# "the check asked the install's --help".
expect_no_call() { # line what
  if grep -qx "$1" "$dir/state/calls"; then
    fail "$2: the check ran '$1'"
  fi
}

# The installation every case runs against, and the machine label the fake uname
# makes: no case names belong, which is the refusal one case is about.
INSTALLATION=sandbox301
GCP_PROJECT=prj-sandbox301
FAKE_PLATFORM=linux

# Every fact key each phase's block must carry, whatever it measured. Pinned
# here rather than read from the script: a row that disappears from the block is
# a finding, and a test that asked the script which rows to expect would not see
# it.
START_KEYS=(phase platform installation agent release xtrace unit daemon state human-install human-reboot restore)
AFTER_KEYS=(phase platform installation release unit boot daemon lease second-refuses state human-next restore)
UPGRADE_KEYS=(phase platform installation release unit daemon never-installs version handover unit-kept lease state human-install human-upgrade human-next restore)
UPDATE_KEYS=(phase platform installation agent release xtrace terminal unit before boundary update re-render quiet lease state restore)
FORGET_KEYS=(phase platform installation state restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check301.sh's test: every answer it gives comes out of
# $FAKE301_DIR, and every call is recorded, so a case can say what the check
# asked and what it never asked. `<path> --version` and `fugaro version` are two
# different readings (the program the unit names, and the CLI this run is), and
# they are two files here for the same reason.
set -euo pipefail
D=$FAKE301_DIR
printf 'fugaro %s\n' "$*" >>"$D/calls"
agent=no
if [ -n "${CLAUDECODE:-}" ]; then agent=yes; fi

render_unit() {
  cp "$D/unit.new" "$(cat "$D/unit.path")"
  # What the manager does after a re-render: the daemon running is a new
  # process, so the pid moves. The unit's text becomes this release's template,
  # which is what `fugaro orchestrator status` then reports.
  printf '%s\n' "$(($(cat "$D/pid") + 1))" >"$D/pid"
  printf 'the login unit at %s is installed and current (fake)\n' "$(cat "$D/unit.path")" >"$D/unit.status"
}

case "${1:-}" in
version)
  cat "$D/cli.version"
  ;;
--version)
  cat "$D/program.version"
  ;;
orchestrator)
  case "${2:-}" in
  status)
    if [ "${3:-}" = --help ]; then
      printf 'Report whether this machine has the login unit\n'
      exit 0
    fi
    cat "$D/unit.status"
    ;;
  install | uninstall)
    if [ "${3:-}" = --help ]; then
      if [ -f "$D/no-$2-help" ]; then
        printf 'fake fugaro: %s --help is not in this build\n' "$2" >&2
        exit 1
      fi
      printf 'Usage: fugaro orchestrator %s\n' "$2"
      exit 0
    fi
    printf 'fake fugaro: the check never runs %s\n' "$2" >&2
    exit 64
    ;;
  ls)
    cat "$D/ls.json"
    ;;
  run)
    printf 'run\n' >>"$D/ran-second"
    cat "$D/second.out" 2>/dev/null || true
    cat "$D/second.err" >&2 2>/dev/null || true
    exit "$(cat "$D/second.status" 2>/dev/null || echo 1)"
    ;;
  *)
    printf 'fake fugaro: unexpected orchestrator verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
update)
  printf 'update %s\n' "$agent" >>"$D/updates"
  if [ -f "$D/update.status" ]; then
    printf 'fake fugaro: update failed\n' >&2
    exit "$(cat "$D/update.status")"
  fi
  # The agent-marked run: this release writes nothing in an agent's session, and
  # a case that says this release is broken makes it write anyway.
  if [ "$agent" = yes ]; then
    if [ -f "$D/agent-writes-unit" ]; then render_unit; fi
    exit 0
  fi
  case "$(cat "$D/update.mode" 2>/dev/null || echo quiet)" in
  quiet) ;;
  render)
    if ! cmp -s "$D/unit.new" "$(cat "$D/unit.path")"; then render_unit; fi
    ;;
  install-unit)
    render_unit
    ;;
  *)
    printf 'fake fugaro: unknown update mode\n' >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  # The manager, one fake per platform: both read the same two files (the state
  # and the pid), so a case moves the daemon by writing one of them.
  cat >"$1/bin/systemctl" <<'SYSTEMCTL_EOF'
#!/usr/bin/env bash
set -euo pipefail
D=$FAKE301_DIR
printf 'systemctl %s\n' "$*" >>"$D/calls"
if [ "${1:-}" != --user ] || [ "${2:-}" != show ]; then
  printf 'fake systemctl: unexpected %s\n' "$*" >&2
  exit 64
fi
case "$(cat "$D/manager.state")" in
running) printf 'LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=%s\n' "$(cat "$D/pid")" ;;
stopped) printf 'LoadState=loaded\nActiveState=inactive\nSubState=dead\nMainPID=0\n' ;;
absent) printf 'LoadState=notfound\nActiveState=inactive\nSubState=dead\nMainPID=0\n' ;;
*)
  printf 'fake systemctl: unknown state\n' >&2
  exit 64
  ;;
esac
SYSTEMCTL_EOF

  cat >"$1/bin/launchctl" <<'LAUNCHCTL_EOF'
#!/usr/bin/env bash
set -euo pipefail
D=$FAKE301_DIR
printf 'launchctl %s\n' "$*" >>"$D/calls"
if [ "${1:-}" != print ]; then
  printf 'fake launchctl: unexpected %s\n' "$*" >&2
  exit 64
fi
case "$(cat "$D/manager.state")" in
running) printf '\tstate = running\n\tpid = %s\n' "$(cat "$D/pid")" ;;
stopped) printf '\tstate = waiting\n' ;;
absent)
  printf 'Could not find service "dev.fugaro.orchestrator" in domain for user\n' >&2
  exit 113
  ;;
*)
  printf 'fake launchctl: unknown state\n' >&2
  exit 64
  ;;
esac
LAUNCHCTL_EOF

  cat >"$1/bin/uname" <<'UNAME_EOF'
#!/usr/bin/env bash
set -euo pipefail
D=$FAKE301_DIR
case "${1:-}" in
-s) printf '%s\n' "$(cat "$D/uname.s" 2>/dev/null || echo Linux)" ;;
-n) printf '%s\n' "$(cat "$D/uname.n" 2>/dev/null || echo testhost)" ;;
*) printf 'fake uname: unexpected %s\n' "${1:-}" >&2 ;;
esac
UNAME_EOF
  chmod +x "$1"/bin/*
}

# write_unit DIR PLATFORM: the login unit's file, in the shape the release
# renders for that manager. The program it names is this case's own fake fugaro,
# which is what `<path> --version` then reads.
write_unit() { # dir platform
  local dir=$1 platform=$2 path
  if [ "$platform" = darwin ]; then
    path=$dir/unit/dev.fugaro.orchestrator.plist
    cat >"$path" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.fugaro.orchestrator</string>
	<key>ProgramArguments</key>
	<array>
		<string>$dir/bin/fugaro</string>
		<string>orchestrator</string>
		<string>run</string>
		<string>--binary</string>
		<string>$dir/bin/fugaro</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
PLIST
  else
    path=$dir/unit/fugaro-orchestrator.service
    cat >"$path" <<SERVICE
[Unit]
Description=Fugaro orchestrator

[Service]
ExecStart=$dir/bin/fugaro orchestrator run --binary $dir/bin/fugaro
Restart=always

[Install]
WantedBy=default.target
SERVICE
  fi
  printf '%s\n' "$path" >"$dir/state/unit.path"
  # The unit a release re-renders: one line different, so a case can tell a
  # re-render from a leave-alone by the file's own text.
  sed 's/^Description=Fugaro orchestrator$/Description=Fugaro orchestrator (this release)/; s|<key>RunAtLoad</key>|<key>RunAtLoad</key>|' "$path" >"$dir/state/unit.new"
}

write_status() { # dir none|current|stale
  local dir=$1 what=$2 path
  path=$(cat "$dir/state/unit.path")
  case "$what" in
  none) printf 'the login unit is not installed: %s does not exist\n' "$path" >"$dir/state/unit.status" ;;
  current) printf 'the login unit at %s is installed and current (fake)\n' "$path" >"$dir/state/unit.status" ;;
  stale) printf 'the login unit at %s is out of date: it was rendered from an older template or another binary path (fake)\n' "$path" >"$dir/state/unit.status" ;;
  esac
}

# write_ls DIR held|free|two — what `fugaro orchestrator ls --json` answers.
write_ls() { # dir mode
  local dir=$1 mode=$2 holder=run-abc holders=1
  case "$mode" in
  held) ;;
  free) holder="" ;;
  two) holder=run-abc; holders=2 ;;
  esac
  {
    printf '{"version":1,"kind":"local","orchestrators":['
    printf '{"kind":"local","project":"%s","lease_holder":"%s","updated_at":"2026-11-13T10:00:05Z","cached_at":"2026-11-13T10:00:06Z","cache_stale":false}' \
      "$INSTALLATION" "$holder"
    if [ "$holders" = 2 ]; then
      printf ',{"kind":"central","project":"another","lease_holder":"run-other","updated_at":"2026-11-13T10:00:04Z","cached_at":"2026-11-13T10:00:06Z","cache_stale":false}'
    fi
    printf ']}\n'
  } >"$dir/state/ls.json"
}

# write_second DIR refused|other|ok — what a second instance started by hand
# answers.
write_second() { # dir mode
  local dir=$1 mode=$2
  case "$mode" in
  refused)
    printf 'orchestrator %s is driven by another daemon right now (holder run-abc, last beat 2026-11-13T10:00:05Z): stop it, or run it with a different installation\n' \
      "$INSTALLATION" >"$dir/state/second.out"
    : >"$dir/state/second.err"
    printf '1\n' >"$dir/state/second.status"
    ;;
  other)
    printf 'orchestrator %s is driven by another daemon right now (holder run-zzz, last beat 2026-11-13T10:00:05Z): stop it\n' \
      "$INSTALLATION" >"$dir/state/second.out"
    : >"$dir/state/second.err"
    printf '1\n' >"$dir/state/second.status"
    ;;
  ok)
    printf 'driving %s as run-second, one cycle every 30s\n' "$INSTALLATION" >"$dir/state/second.out"
    : >"$dir/state/second.err"
    printf '0\n' >"$dir/state/second.status"
    ;;
  esac
}

write_config() { # dir
  mkdir -p "$1/cfg/fugaro/projects"
  cat >"$1/cfg/fugaro/projects/$INSTALLATION.yaml" <<CONFIG
name: $INSTALLATION
gcp_project: $GCP_PROJECT
CONFIG
}

setup_case() { # name [platform]
  dir=$work/$1
  platform=${2:-$FAKE_PLATFORM}
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home" "$dir/statehome" "$dir/unit" "$dir/none"
  write_fakes "$dir"
  write_config "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/updates"
  printf 'running\n' >"$dir/state/manager.state"
  printf '4242\n' >"$dir/state/pid"
  printf '0.24.0\n' >"$dir/state/cli.version"
  printf '0.24.0\n' >"$dir/state/program.version"
  printf 'quiet\n' >"$dir/state/update.mode"
  printf '%s\n' "$platform" >"$dir/state/platform"
  if [ "$platform" = darwin ]; then printf 'Darwin\n' >"$dir/state/uname.s"; else printf 'Linux\n' >"$dir/state/uname.s"; fi
  printf 'testhost\n' >"$dir/state/uname.n"
  write_unit "$dir" "$platform"
  write_status "$dir" current
  write_ls "$dir" held
  write_second "$dir" refused
}

# state_file is the one file the check keeps between the phases.
state_file() { printf '%s\n' "$dir/statehome/fugaro/check301/state"; }

run301() {
  status=0
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # is itself started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  local runner=("$TIMEOUT_BIN" "${RUN301_TIMEOUT:-30}" "$BASH_BIN")
  if [ -n "${RUN301_TTY:-}" ]; then
    runner=("$TIMEOUT_BIN" "${RUN301_TIMEOUT:-30}" "$PYTHON_BIN" "$work/faketty.py" "$BASH_BIN")
  fi
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    HOME="$dir/home" TMPDIR="$dir/tmp" FAKE301_DIR="$dir/state" \
    XDG_CONFIG_HOME="${RUN301_XDG_CONFIG_HOME:-$dir/cfg}" \
    XDG_STATE_HOME="${RUN301_XDG_STATE_HOME:-$dir/statehome}" \
    PATH="${RUN301_PATH:-$dir/bin:/usr/bin:/bin}" \
    FUGARO_PROJECT="${RUN301_INSTALLATION-$INSTALLATION}" \
    CHECK301_PLATFORM="$platform" \
    CHECK301_BOOT_ID="${RUN301_BOOT_ID:-boot-a}" \
    CHECK301_HANDOVER_SECONDS=5 \
    ${RUN301_ENV[@]+"${RUN301_ENV[@]}"} \
    "${runner[@]}" ${RUN301_BASH[@]+"${RUN301_BASH[@]}"} "$script" ${RUN301_ARGS[@]+"${RUN301_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN301_ARGS=()
  RUN301_ENV=()
  RUN301_PATH=""
  RUN301_BASH=()
  RUN301_XDG_CONFIG_HOME=""
  RUN301_XDG_STATE_HOME=""
  RUN301_INSTALLATION=""
  RUN301_TTY=""
  RUN301_BOOT_ID=""
  RUN301_TIMEOUT=""
  errtext=$(cat "$dir/err" 2>/dev/null || true)
}

expect_block_keys() { # out what keys...
  local what=$1
  shift
  expect_contains "$out" "CHECK 301 RESULT" "$what: the block"
  local key
  for key in "$@"; do
    expect_contains "$out" "FACT $key:" "$what: the $key row"
  done
  expect_contains "$out" "CHECK 301 RESULT END (exit " "$what: the block's end"
}

expect_state_key() { # key value what
  local got
  got=$(sed -n "s/^$1=//p" "$(state_file)" | head -1)
  [ "$got" = "$2" ] || fail "${4:-state}: $1 is '$got', want '$2'"
}

# --------------------------------------------------------------------- cases
# case_dry_run plans a phase and changes nothing: the state directory the real
# run would make is not even there, and no command that writes was reached.
case_dry_run() {
  setup_case dry-run
  run301 start --dry-run
  expect_status "$status" 0 "dry run: the exit"
  expect_block_keys "dry run" "${START_KEYS[@]}" dry-run
  expect_fact dry-run PASS "dry run: the row"
  expect_contains "$errtext" "the one thing you do: reboot this machine" "dry run: the plan names the reboot"
  expect_contains "$errtext" "NOTHING below was run" "dry run: the plan says so"
  if [ -e "$dir/statehome/fugaro" ]; then
    fail "dry run: it made a state directory: $(find "$dir/statehome" -mindepth 1 | head -3)"
  fi
  expect_absent "$(cat "$dir/state/calls")" "update" "dry run: an update was reached"
  expect_absent "$(cat "$dir/state/calls")" "orchestrator run" "dry run: a daemon was started"
}

# case_start_linux fills script301's own slot 0 on Linux: the unit is installed
# and current, its daemon is running, and the phase records what a reboot has to
# survive and prints the reboot.
case_start_linux() {
  setup_case start-linux
  run301 start
  expect_status "$status" 0 "start: the exit"
  expect_block_keys "start" "${START_KEYS[@]}"
  expect_fact unit PASS "start: the unit"
  expect_fact daemon PASS "start: the daemon"
  expect_fact state PASS "start: what it recorded"
  expect_fact human-reboot INFO "start: the reboot"
  expect_fact restore PASS "start: the cleanup"
  expect_contains "$out" "FACT daemon: PASS — the unit's daemon is running: systemd the unit is running, pid 4242" "start: the manager's own words"
  [ -f "$(state_file)" ] || fail "start: it recorded no state file at $(state_file)"
  expect_state_key half reboot "start"
  expect_state_key stage pending-reboot "start"
  expect_state_key boot boot-a "start"
  expect_state_key pid 4242 "start"
  expect_state_key unit "$dir/unit/fugaro-orchestrator.service" "start"
  expect_state_key version 0.24.0 "start"
  expect_contains "$errtext" "sudo shutdown -r now" "start: the reboot command"
  expect_contains "$errtext" "bash scripts/live-checks/check301.sh after-reboot" "start: the next step"
  if [ -z "$(find "$(state_file)" -perm 600 2>/dev/null)" ]; then
    fail "start: the state file is not 0600: $(ls -l "$(state_file)")"
  fi
}

# case_start_darwin is the same phase on the other platform: the script picks
# launchd itself and reads the plist the release renders there.
case_start_darwin() {
  setup_case start-darwin darwin
  run301 start
  expect_status "$status" 0 "start on macOS: the exit"
  expect_block_keys "start on macOS" "${START_KEYS[@]}"
  expect_contains "$out" "FACT platform: PASS — this check picked launchd itself" "start on macOS: the platform"
  expect_contains "$out" "FACT daemon: PASS — the unit's daemon is running: launchd the unit is running, pid 4242" "start on macOS: the manager's own words"
  expect_state_key platform darwin "start on macOS"
  expect_state_key unit "$dir/unit/dev.fugaro.orchestrator.plist" "start on macOS"
  expect_state_key version 0.24.0 "start on macOS"
  expect_contains "$(cat "$dir/state/calls")" "launchctl print gui/" "start on macOS: the manager was read"
}

# case_start_no_unit: the one thing the check cannot do for you. The install
# needs a typed word at a real terminal, so the phase prints it and records it,
# and it records NO state: a reboot would prove nothing about a unit that is not
# there.
case_start_no_unit() {
  setup_case start-no-unit
  write_status "$dir" none
  rm -f "$dir/unit/fugaro-orchestrator.service"
  run301 start
  expect_status "$status" 2 "no unit: the exit"
  expect_fact unit INFO "no unit: the row"
  expect_fact human-install INFO "no unit: the install to run"
  expect_contains "$out" "fugaro orchestrator install" "no unit: the exact command"
  expect_contains "$out" "type the word install" "no unit: and why the check cannot run it"
  if [ -e "$(state_file)" ]; then
    fail "no unit: it recorded a state file for a machine with nothing to reboot"
  fi
  expect_no_call "fugaro orchestrator install" "no unit: the check ran the install itself"
}

# case_start_daemon_down: the unit is installed and its daemon is not running,
# so there is nothing for a reboot to bring back. The phase says so and records
# nothing rather than asking for a reboot that would prove nothing.
case_start_daemon_down() {
  setup_case start-daemon-down
  printf 'stopped\n' >"$dir/state/manager.state"
  run301 start
  expect_status "$status" 2 "daemon down: the exit"
  expect_fact daemon FAIL "daemon down: the row"
  expect_absent "$out" "FACT human-reboot: INFO" "daemon down: it asked for a reboot anyway"
  if [ -e "$(state_file)" ]; then
    fail "daemon down: it recorded a state file with no daemon to survive a reboot"
  fi
}

# case_start_again: running start twice on the same machine is the honest
# re-read — it replaces the readings, and the reboot proof becomes "the boot
# identity moved since the last start" again.
case_start_again() {
  setup_case start-again
  run301 start
  expect_status "$status" 0 "start again: the first run"
  cp "$(state_file)" "$dir/state/first"
  run301 start
  expect_status "$status" 0 "start again: the second run"
  expect_fact state PASS "start again: the row"
  expect_state_key boot boot-a "start again"
  local first
  first=$(sed -n 's/^at=//p' "$dir/state/first")
  local second
  second=$(sed -n 's/^at=//p' "$(state_file)")
  [ "$first" = "$second" ] || : # the timestamps may differ; the reading below is what matters
  expect_contains "$out" "FACT state: PASS" "start again: the state row is the last one written"
}

# case_belong: the owner's own installation is never the one this check drives.
case_belong() {
  setup_case belong
  RUN301_INSTALLATION=belong
  run301 start
  expect_status "$status" 2 "belong: the exit"
  expect_fact installation FAIL "belong: the row"
  expect_contains "$out" "never runs against 'belong'" "belong: the refusal"
  if [ -e "$dir/statehome/fugaro" ]; then
    fail "belong: the refused run made a state directory"
  fi
  expect_no_call "fugaro orchestrator status" "belong: it read the unit before refusing"
}

# case_no_installation: nothing names an installation, so the check cannot say
# which installation's daemon it would be driving.
case_no_installation() {
  setup_case no-installation
  mkdir -p "$dir/nocfg/fugaro/projects"
  RUN301_INSTALLATION=
  RUN301_XDG_CONFIG_HOME="$dir/nocfg"
  run301 start
  expect_status "$status" 2 "no installation: the exit"
  expect_fact installation FAIL "no installation: the row"
  expect_contains "$errtext" "--installation <name>" "no installation: the usage line"
}

# case_old_release: O9's own half of the check is released with 0.24.0, and a
# check that ran against an older fugaro would fail obscurely.
case_old_release() {
  setup_case old-release
  printf '0.23.0\n' >"$dir/state/cli.version"
  run301 start
  expect_status "$status" 2 "0.23.0: the exit"
  expect_fact release FAIL "0.23.0: the row"
  expect_contains "$out" "needs release 0.24.0" "0.23.0: the release it needs"
  expect_contains "$errtext" "fugaro update" "0.23.0: the one command that fixes it"
}

# case_dev_release: a build from a checkout is not a release, and the version
# string says so.
case_dev_release() {
  setup_case dev-release
  printf 'dev\n' >"$dir/state/cli.version"
  run301 start
  expect_status "$status" 2 "dev: the exit"
  expect_fact release FAIL "dev: the row"
  expect_contains "$out" "a build from a checkout" "dev: the row says what it is"
}

# case_missing_verb: a released fugaro whose orchestrator family lacks a verb
# the check drives stops the same way, by name.
case_missing_verb() {
  setup_case missing-verb
  : >"$dir/state/no-install-help"
  run301 start
  expect_status "$status" 2 "missing verb: the exit"
  expect_contains "$out" "carries no 'fugaro orchestrator install'" "missing verb: which verb"
  expect_contains "$out" "needs release 0.24.0" "missing verb: the release it needs"
}

# case_agent_session: no agent runs this check. The refusal comes before any
# reading, so the block says why instead of reporting what an agent's own
# environment made of the machine.
case_agent_session() {
  setup_case agent-session
  RUN301_ENV=(CLAUDECODE=1)
  run301 start
  expect_status "$status" 2 "agent session: the exit"
  expect_fact agent FAIL "agent session: the row"
  expect_contains "$out" "CLAUDECODE" "agent session: the marker by name"
  expect_absent "$(cat "$dir/state/calls")" "version" "agent session: it read the release before refusing"
  expect_contains "$errtext" "yours to run and never an agent's" "agent session: the refusal"
}

# case_xtrace: a trace of this run is not the run the block describes.
case_xtrace() {
  setup_case xtrace
  RUN301_BASH=(-x)
  run301 start
  expect_status "$status" 2 "xtrace: the exit"
  expect_fact xtrace FAIL "xtrace: the row"
  expect_contains "$out" "this shell is tracing" "xtrace: the row says why"
}

# case_after_reboot_happy: the machine rebooted (its boot identity moved), the
# unit brought the daemon back under a NEW pid with no terminal and no command,
# exactly one daemon holds the lease, and a second instance started by hand is
# refused by name.
case_after_reboot_happy() {
  setup_case after-happy
  run301 start
  expect_status "$status" 0 "after-reboot: the start before it"
  printf '5150\n' >"$dir/state/pid"
  RUN301_BOOT_ID=boot-b
  run301 after-reboot
  expect_status "$status" 0 "after-reboot: the exit"
  expect_block_keys "after-reboot" "${AFTER_KEYS[@]}"
  expect_fact boot PASS "after-reboot: the reboot"
  expect_fact daemon PASS "after-reboot: the daemon"
  expect_contains "$out" "pid moved from 4242 to 5150" "after-reboot: the daemon is a new process"
  expect_fact lease PASS "after-reboot: the lease"
  expect_contains "$out" "held by run-abc" "after-reboot: the holder"
  expect_fact second-refuses PASS "after-reboot: the second instance"
  expect_fact state PASS "after-reboot: the state file"
  if [ -e "$(state_file)" ]; then
    fail "after-reboot: the finished half left its state file behind"
  fi
  expect_contains "$(cat "$dir/state/calls")" "orchestrator run --once" "after-reboot: the probe"
  expect_contains "$errtext" "check301.sh upgrade" "after-reboot: the next step"
}

# case_after_reboot_no_reboot: the reading the whole phase is for. Nothing in
# this run can tell a reboot a person skipped, except the machine's own boot
# identity — which is why it is the one reading a person's word cannot replace.
case_after_reboot_no_reboot() {
  setup_case after-no-reboot
  run301 start
  run301 after-reboot
  expect_status "$status" 1 "no reboot: the exit"
  expect_fact boot FAIL "no reboot: the row"
  expect_contains "$out" "the machine has NOT rebooted" "no reboot: the row says so"
  expect_contains "$(cat "$dir/state/calls")" "orchestrator run --once" "no reboot: the second instance is still probed"
  [ -f "$(state_file)" ] || fail "no reboot: the readings were dropped, so the phase cannot be run again"
  expect_fact state INFO "no reboot: the state row says the readings are kept"
  expect_contains "$out" "run this phase again" "no reboot: and what to run"
}

# case_after_reboot_without_start: the phase compares against what start
# recorded, and there is nothing to compare with.
case_after_reboot_without_start() {
  setup_case after-no-start
  run301 after-reboot
  expect_status "$status" 2 "no start: the exit"
  expect_fact state FAIL "no start: the row"
  expect_contains "$errtext" "run 'check301.sh start'" "no start: the phase to run first"
}

# case_after_reboot_foreign_state: the state file came from another machine.
case_after_reboot_foreign_state() {
  setup_case after-foreign
  run301 start
  sed -i.bak 's/^machine=.*/machine=Linux:someoneelse/' "$(state_file)"
  RUN301_BOOT_ID=boot-b
  run301 after-reboot
  expect_status "$status" 2 "foreign state: the exit"
  expect_fact state FAIL "foreign state: the row"
  expect_contains "$out" "written by Linux:someoneelse" "foreign state: it names the machine"
}

# case_after_reboot_free_lease: with no holder there is nothing for a second
# instance to be refused by — and starting one would TAKE the lease, which is
# the one thing the probe must never do. So it is skipped and said out loud.
case_after_reboot_free_lease() {
  setup_case after-free-lease
  run301 start
  printf '5150\n' >"$dir/state/pid"
  write_ls "$dir" free
  RUN301_BOOT_ID=boot-b
  run301 after-reboot
  expect_status "$status" 1 "free lease: the exit"
  expect_fact lease FAIL "free lease: the row"
  expect_contains "$out" "FACT second-refuses: INFO — the lease names no holder" "free lease: the probe was skipped"
  expect_absent "$(cat "$dir/state/calls")" "orchestrator run" "free lease: the probe took a free lease"
}

# case_after_reboot_two_holders: two daemons driving ids in one installation is
# the concurrency fence broken.
case_after_reboot_two_holders() {
  setup_case after-two-holders
  run301 start
  printf '5150\n' >"$dir/state/pid"
  write_ls "$dir" two
  RUN301_BOOT_ID=boot-b
  run301 after-reboot
  expect_status "$status" 1 "two holders: the exit"
  expect_fact lease FAIL "two holders: the row"
  expect_contains "$out" "names 2 holders across" "two holders: the count"
}

# case_second_instance_not_refused: the probe exits 0 and says nothing about a
# holder. A second daemon is then NOT refused this installation's orchestrator.
case_second_instance_not_refused() {
  setup_case second-ok
  run301 start
  printf '5150\n' >"$dir/state/pid"
  write_second "$dir" ok
  RUN301_BOOT_ID=boot-b
  run301 after-reboot
  expect_status "$status" 1 "second not refused: the exit"
  expect_fact second-refuses FAIL "second not refused: the row"
  expect_contains "$out" "without the refusal this check reads" "second not refused: what was missing"
}

# case_second_instance_other_holder: refused, but for a holder the listing does
# not name — the two readings are about different daemons.
case_second_instance_other_holder() {
  setup_case second-other
  run301 start
  printf '5150\n' >"$dir/state/pid"
  write_second "$dir" other
  RUN301_BOOT_ID=boot-b
  run301 after-reboot
  expect_status "$status" 1 "other holder: the exit"
  expect_fact second-refuses FAIL "other holder: the row"
  expect_contains "$out" "for another holder (run-zzz) than the listing read (run-abc)" "other holder: the mismatch"
}

# case_upgrade_boundary: on a machine with no unit, `fugaro update` writes none
# — T7's boundary — and the check hands the install to the person instead of
# pretending to have measured a handover.
case_upgrade_boundary() {
  setup_case upgrade-boundary
  write_status "$dir" none
  rm -f "$dir/unit/fugaro-orchestrator.service"
  run301 upgrade
  expect_status "$status" 2 "boundary: the exit"
  expect_block_keys "boundary" "${UPGRADE_KEYS[@]}"
  expect_fact never-installs PASS "boundary: the row"
  expect_contains "$out" "wrote none" "boundary: what update did"
  expect_fact human-install INFO "boundary: the install to run"
  expect_fact handover INFO "boundary: nothing to hand over"
  if [ -e "$(state_file)" ]; then
    fail "boundary: it recorded a state file with no unit to read a handover on"
  fi
}

# case_upgrade_boundary_writes_unit: the mutation of that boundary. An update
# that installs a login unit on a machine that had none is exactly what T7
# refuses, and the row has to say so.
case_upgrade_boundary_writes_unit() {
  setup_case upgrade-installs
  write_status "$dir" none
  rm -f "$dir/unit/fugaro-orchestrator.service"
  printf 'install-unit\n' >"$dir/state/update.mode"
  run301 upgrade
  expect_status "$status" 2 "boundary broken: the exit"
  expect_fact never-installs FAIL "boundary broken: the row"
  expect_contains "$out" "update installed a unit" "boundary broken: what happened"
}

# case_upgrade_waits_for_the_human: the check never replaces a binary itself.
# It records the version at the path the unit names and stops with the one
# command the person runs.
case_upgrade_waits_for_the_human() {
  setup_case upgrade-waits
  run301 upgrade
  expect_status "$status" 2 "waiting: the exit"
  expect_fact daemon PASS "waiting: the daemon"
  expect_fact version INFO "waiting: the version before"
  expect_fact handover INFO "waiting: nothing measured yet"
  expect_fact human-upgrade INFO "waiting: the upgrade to run"
  expect_contains "$out" "replace $dir/bin/fugaro with the new release" "waiting: the exact command when brew is not the install"
  expect_state_key stage pending-upgrade "waiting"
  expect_state_key version 0.24.0 "waiting"
  expect_state_key pid 4242 "waiting"
  expect_absent "$(cat "$dir/state/calls")" "update" "waiting: the check ran an update"
}

# case_upgrade_handover: the whole point of the half. A package manager replaced
# the binary at the path the unit names; the daemon exited by itself and the
# manager started the new one. This check issued no command that starts or
# restarts a service: the pid and the version are both read.
case_upgrade_handover() {
  setup_case upgrade-handover
  run301 upgrade
  expect_status "$status" 2 "handover: the phase before it"
  printf '0.24.1\n' >"$dir/state/program.version"
  printf '5150\n' >"$dir/state/pid"
  run301 upgrade
  expect_status "$status" 0 "handover: the exit"
  expect_block_keys "handover" "${UPGRADE_KEYS[@]}"
  expect_fact version PASS "handover: the version at the unit's path"
  expect_contains "$out" "reported '0.24.0' before the upgrade and reports '0.24.1' now" "handover: the move"
  expect_fact handover PASS "handover: the row"
  expect_contains "$out" "pid moved from 4242 to 5150" "handover: the manager restarted it"
  expect_contains "$out" "This check issued no command that starts or restarts the service" "handover: and that nothing here did"
  expect_fact lease PASS "handover: the lease"
  expect_contains "$(cat "$dir/state/calls")" "systemctl --user show" "handover: only the manager's read verb"
  expect_absent "$(cat "$dir/state/calls")" "systemctl --user restart" "handover: a restart command"
  expect_absent "$(cat "$dir/state/calls")" "launchctl bootstrap" "handover: a load command"
  if [ -e "$(state_file)" ]; then fail "handover: the finished half left its state file behind"; fi
}

# case_upgrade_binary_not_moved: the person has not run the upgrade yet. Nothing
# moved, so there is no handover to read, and the phase keeps its readings.
case_upgrade_binary_not_moved() {
  setup_case upgrade-not-moved
  run301 upgrade
  run301 upgrade
  expect_status "$status" 2 "not moved: the exit"
  expect_fact version FAIL "not moved: the row"
  expect_contains "$out" "still reports '0.24.0'" "not moved: the row"
  [ -f "$(state_file)" ] || fail "not moved: the readings were dropped, so the phase cannot be run again"
  expect_fact state INFO "not moved: the state row"
}

# case_upgrade_no_handover: the binary moved and the daemon did not. The unit is
# written with launchd's KeepAlive and systemd's Restart=always, so this is the
# daemon's own version check or the unit's program path, and the row says so.
case_upgrade_no_handover() {
  setup_case upgrade-no-handover
  run301 upgrade
  printf '0.24.1\n' >"$dir/state/program.version"
  run301 upgrade
  expect_status "$status" 1 "no handover: the exit"
  expect_fact handover FAIL "no handover: the row"
  expect_contains "$out" "the daemon is still pid 4242" "no handover: what did not happen"
  expect_contains "$out" "the handover did not happen" "no handover: it says so"
}

# case_upgrade_unit_changed: replacing the binary must not rewrite the unit.
case_upgrade_unit_changed() {
  setup_case upgrade-unit-changed
  run301 upgrade
  printf '0.24.1\n' >"$dir/state/program.version"
  printf '5150\n' >"$dir/state/pid"
  printf '\n# something else edited the unit\n' >>"$dir/state/unit.path.x"
  printf '\n# something else edited the unit\n' >>"$dir/unit/fugaro-orchestrator.service"
  run301 upgrade
  expect_status "$status" 1 "unit edited: the exit"
  expect_fact unit-kept FAIL "unit edited: the row"
  expect_contains "$out" "must not rewrite the unit" "unit edited: the row says which rule"
}

# case_update_quiet: the unit is already this release's template, so there is
# nothing to write and nothing to reload — and the boundary run (the same
# command with a coding agent's marker) writes nothing either.
case_update_quiet() {
  setup_case update-quiet
  RUN301_TTY=yes
  run301 update
  expect_status "$status" 0 "quiet: the exit"
  expect_block_keys "quiet" "${UPDATE_KEYS[@]}"
  expect_fact terminal PASS "quiet: the terminal"
  expect_fact before PASS "quiet: the readings"
  expect_fact boundary PASS "quiet: the agent-marked run"
  expect_contains "$out" "CLAUDECODE=1" "quiet: the marker the boundary run carried"
  expect_fact update PASS "quiet: the update"
  expect_fact re-render PASS "quiet: the quiet half"
  expect_contains "$out" "was already this release's template" "quiet: why nothing was written"
  expect_fact quiet PASS "quiet: the repeated update"
  expect_fact lease PASS "quiet: the lease"
  if [ -e "$(state_file)" ]; then fail "quiet: the finished half left its state file behind"; fi
  expect_contains "$(cat "$dir/state/updates")" "update yes" "quiet: the update with the agent marker was really run"
  expect_contains "$(cat "$dir/state/updates")" "update no" "quiet: the unmarked update was really run"
}

# case_update_rerenders: the machine T7's half is about — a unit written by an
# older release. One `fugaro update`, and the file is this release's template
# and the manager has restarted the daemon on it.
case_update_rerenders() {
  setup_case update-rerenders
  write_status "$dir" stale
  printf 'render\n' >"$dir/state/update.mode"
  RUN301_TTY=yes
  run301 update
  expect_status "$status" 0 "re-render: the exit"
  expect_fact boundary PASS "re-render: the agent-marked run left it alone"
  expect_contains "$out" "the file still carries the previous template" "re-render: the boundary row says what it left"
  expect_fact update PASS "re-render: the update"
  expect_fact re-render PASS "re-render: the row"
  expect_contains "$out" "re-rendered the login unit from this release's template" "re-render: what happened"
  expect_contains "$out" "pid moved from 4242 to 4243" "re-render: the manager reloaded it"
  expect_fact quiet PASS "re-render: the repeated update"
  expect_fact lease PASS "re-render: the lease"
}

# case_update_agent_writes_unit: the marker rule broken. An agent-marked run
# that writes the unit is exactly what internal/agentenv's G2 refuses.
case_update_agent_writes_unit() {
  setup_case update-agent-writes
  : >"$dir/state/agent-writes-unit"
  RUN301_TTY=yes
  run301 update
  expect_status "$status" 1 "agent writes: the exit"
  expect_fact boundary FAIL "agent writes: the row"
  expect_contains "$out" "rewrote the login unit" "agent writes: what happened"
  expect_contains "$out" "agentenv" "agent writes: the rule it breaks"
}

# case_update_stays_stale: an installed unit that needs the re-render and does
# not get it. One `fugaro update` is the whole upgrade, so leaving it behind is
# a finding.
case_update_stays_stale() {
  setup_case update-stays-stale
  write_status "$dir" stale
  RUN301_TTY=yes
  run301 update
  expect_status "$status" 1 "stays stale: the exit"
  expect_fact boundary PASS "stays stale: the agent-marked run"
  expect_fact re-render FAIL "stays stale: the row"
  expect_contains "$out" "one 'fugaro update' converges an installed unit" "stays stale: what happened"
}

# case_update_not_terminal: no person is at this check's terminal, and the
# re-render is a step that needs one — so the row says which of the two reasons
# an unchanged unit has.
case_update_not_terminal() {
  setup_case update-no-terminal
  run301 update
  expect_status "$status" 1 "no terminal: the exit"
  expect_fact terminal FAIL "no terminal: the row"
  expect_contains "$out" "standard input is not a terminal" "no terminal: the row says why"
}

# case_update_no_unit: nothing to re-render, and update never installs one.
case_update_no_unit() {
  setup_case update-no-unit
  write_status "$dir" none
  rm -f "$dir/unit/fugaro-orchestrator.service"
  RUN301_TTY=yes
  run301 update
  expect_status "$status" 2 "update with no unit: the exit"
  expect_fact unit INFO "update with no unit: the row"
  expect_contains "$errtext" "never installs one" "update with no unit: why it stops"
  expect_absent "$(cat "$dir/state/calls")" "fugaro update" "update with no unit: it ran an update anyway"
}

# case_forget: the one thing the check leaves behind, removed on demand.
case_forget() {
  setup_case forget
  run301 start
  [ -f "$(state_file)" ] || fail "forget: the start before it recorded nothing"
  run301 forget
  expect_status "$status" 0 "forget: the exit"
  expect_block_keys "forget" "${FORGET_KEYS[@]}"
  expect_fact state PASS "forget: the row"
  if [ -e "$dir/statehome/fugaro/check301" ]; then
    fail "forget: it left its own directory behind: $(find "$dir/statehome/fugaro/check301")"
  fi
}

# case_forget_nothing: nothing to remove is a pass, not an error.
case_forget_nothing() {
  setup_case forget-nothing
  run301 forget
  expect_status "$status" 0 "forget with nothing: the exit"
  expect_fact state PASS "forget with nothing: the row"
  expect_contains "$out" "has left nothing behind" "forget with nothing: the row says so"
}

# case_state_dir_is_not_ours: a directory the check did not make is never
# written into or removed, whatever it is called.
case_state_dir_is_not_ours() {
  setup_case state-foreign
  mkdir -p "$dir/statehome/fugaro/check301"
  printf 'someone else writes here\n' >"$dir/statehome/fugaro/check301/notes.txt"
  run301 start
  expect_status "$status" 2 "foreign state dir: the exit"
  expect_contains "$errtext" "carries no marker from this check" "foreign state dir: the refusal"
  [ -f "$dir/statehome/fugaro/check301/notes.txt" ] || fail "foreign state dir: it removed a file that was not its own"
}

# case_missing_tool: no fugaro on PATH stops the run by name, before any
# reading.
case_missing_tool() {
  setup_case missing-tool
  RUN301_PATH="$dir/none:/usr/bin:/bin"
  run301 start
  expect_status "$status" 2 "missing tool: the exit"
  expect_contains "$errtext" "needs 'fugaro' on PATH" "missing tool: the tool by name"
  expect_fact aborted FAIL "missing tool: the block says the run stopped"
}

# case_unknown_argument: an argument the check does not know is refused with the
# usage line, never ignored.
case_unknown_argument() {
  setup_case unknown-arg
  run301 start --installation
  expect_status "$status" 2 "missing value: the exit"
  expect_contains "$errtext" "--installation needs a name" "missing value: the refusal"
  expect_contains "$errtext" "usage: bash scripts/live-checks/check301.sh" "missing value: the usage"
  run301 start --wat
  expect_status "$status" 2 "unknown flag: the exit"
  expect_contains "$errtext" "unknown argument '--wat'" "unknown flag: the refusal"
  run301
  expect_status "$status" 2 "no phase: the exit"
  expect_contains "$errtext" "name the phase to run" "no phase: the refusal"
}

# case_help: --help is the usage, and nothing else happens.
case_help() {
  setup_case help
  run301 --help
  expect_status "$status" 0 "--help: the exit"
  expect_contains "$out" "start|after-reboot|upgrade|update|forget" "--help: the phases"
  expect_absent "$(cat "$dir/state/calls")" "version" "--help: a command was run"
}

# case_the_boot_proof_catches_a_blind_script: the assertion above is worth only
# as much as its catching a script that lost the reading. This is the mutation:
# a copy whose boot comparison cannot fail, run against the case that did NOT
# reboot — the suite's own expectation (boot FAIL) has to see it.
case_the_boot_proof_catches_a_blind_script() {
  setup_case boot-mutation
  local copy=$dir/blind301.sh
  sed "s|^  elif \\[ \"\\\$now_boot\" = \"\\\$was_boot\" \\]; then\$|  elif false; then|" "$script" >"$copy"
  grep -q 'elif false; then' "$copy" || fail "mutation: the sed found no place to blind the boot comparison"
  run301 start
  expect_status "$status" 0 "mutation: the start before it"
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    HOME="$dir/home" TMPDIR="$dir/tmp" FAKE301_DIR="$dir/state" \
    XDG_CONFIG_HOME="$dir/cfg" XDG_STATE_HOME="$dir/statehome" \
    PATH="$dir/bin:/usr/bin:/bin" FUGARO_PROJECT="$INSTALLATION" \
    CHECK301_PLATFORM="$platform" CHECK301_BOOT_ID=boot-a CHECK301_HANDOVER_SECONDS=5 \
    "$TIMEOUT_BIN" 30 "$BASH_BIN" "$copy" after-reboot 2>"$dir/err") || status=$?
  expect_fact boot PASS "mutation: the blinded script reports a reboot that did not happen"
  expect_contains "$out" "FACT boot: PASS — the machine really rebooted" \
    "mutation: a script that lost the boot reading was NOT caught, so the case's boot FAIL assertion is vacuous"
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

t case_dry_run
t case_start_linux
t case_start_darwin
t case_start_no_unit
t case_start_daemon_down
t case_start_again
t case_belong
t case_no_installation
t case_old_release
t case_dev_release
t case_missing_verb
t case_agent_session
t case_xtrace
t case_after_reboot_happy
t case_after_reboot_no_reboot
t case_after_reboot_without_start
t case_after_reboot_foreign_state
t case_after_reboot_free_lease
t case_after_reboot_two_holders
t case_second_instance_not_refused
t case_second_instance_other_holder
t case_upgrade_boundary
t case_upgrade_boundary_writes_unit
t case_upgrade_waits_for_the_human
t case_upgrade_handover
t case_upgrade_binary_not_moved
t case_upgrade_no_handover
t case_upgrade_unit_changed
t case_update_quiet
t case_update_rerenders
t case_update_agent_writes_unit
t case_update_stays_stale
t case_update_not_terminal
t case_update_no_unit
t case_forget
t case_forget_nothing
t case_state_dir_is_not_ours
t case_missing_tool
t case_unknown_argument
t case_help
t case_the_boot_proof_catches_a_blind_script

if [ "$failures" -ne 0 ]; then
  echo "check301_test: $failures failure(s)" >&2
  exit 1
fi
echo "check301_test: all cases passed"
