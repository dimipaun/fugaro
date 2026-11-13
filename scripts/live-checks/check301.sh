#!/usr/bin/env bash
# Check 301 — the orchestrator daemon under launchd and systemd, as ONE script
# YOU run.
#
# It settles the live half of O9 of the stateless orchestrator plan
# (docs/stateless-orchestrator/implementation-plan.md; T7 ruled 2026-11-05 and
# T8 ruled 2026-11-10, in docs/stateless-orchestrator/reconciliation.md). The
# register's section "Check 301" in docs/gcp-live-checklist.md is the source of
# truth: this script implements exactly its four readings — the install and the
# lease, the self-upgrade with no command typed, T7's re-render, and the
# boundary — and nothing else.
#
# Usage:      bash scripts/live-checks/check301.sh start        [--installation <name>]
#             bash scripts/live-checks/check301.sh after-reboot [--installation <name>]
#             bash scripts/live-checks/check301.sh upgrade      [--machine <label>]
#             bash scripts/live-checks/check301.sh update       [--machine <label>]
#             bash scripts/live-checks/check301.sh forget
#             Every phase takes --dry-run, which prints its plan and changes
#             nothing.
#
# Why it is phased. The check spans a reboot and two platforms, so one run
# cannot measure it: `start` records what is true before the reboot and prints
# the one thing you do (reboot the machine); `after-reboot` proves the reboot
# happened by the machine's own boot identity, reads the daemon it brought
# back, and prints the next step; `upgrade` and `update` are the T7 half on the
# machine where the daemon runs under its unit — the upgrade half is meant for a
# THROWAWAY machine, macOS or Linux, so the two halves need not be the same one.
# The same script runs on both platforms and picks the manager itself
# (launchd on Darwin, systemd --user on Linux).
#
#   start        on your Mac (launchd) or a Linux machine (systemd): reads the
#                login unit and the daemon, records what a reboot has to
#                survive, and prints the reboot.
#   after-reboot the machine rebooted: the boot identity changed, the daemon is
#                running again, exactly one instance holds the lease, and a
#                second instance started by hand is refused.
#   upgrade      the self-upgrade with no command typed: you replace the binary
#                the unit names (brew-style), the daemon notices and exits so
#                launchd or systemd starts the new one, and nothing restarts it.
#                On a machine whose unit is not installed yet it first measures
#                the boundary: `fugaro update` never installs one.
#   update       T7's half: `fugaro update` re-renders the unit when this
#                release's template differs, a second run writes nothing and
#                reloads nothing, and an agent-marked run writes no unit.
#   forget       removes the one thing this check leaves behind: its state file.
#
# What is a FACT and what is yours. Every reading the script makes is a
# `FACT <name>: PASS|FAIL|INFO — <what>` line in the block, and the run ends
# with `CHECK 301 RESULT ... END (exit <code>)`. The steps only you can take are
# printed once, with their exact command, and recorded in the block as their own
# row: the reboot (`human-reboot`), the install of the unit (`human-install`,
# which the command itself confirms by asking you to type the word `install` —
# no flag stands in for it, so this script never runs it for you), and the
# brew-style upgrade (`human-upgrade`). Each is verified by a reading rather
# than by your word: the reboot by the boot identity, the install by the unit's
# file and the manager, the upgrade by the version at the path the unit names.
#
# Needs:      bash, mktemp, awk, sed and python3 on PATH. No credential is read,
#             printed or needed: every reading of the cloud goes through your own
#             `fugaro` (its `orchestrator ls`, `status` and `update`), which
#             carries your own credential itself. A missing tool stops the run
#             with one line naming it.
# Where:      on the machine that runs the daemon. `belong` is refused as the
#             installation, and the installation is refused when nothing names
#             it. The upgrade half belongs on a throwaway machine — it replaces
#             the fugaro at the path your unit names — so run it there, not on
#             the Mac you care about.
# Costs:      `upgrade` runs `fugaro update --yes` once (and only on a machine
#             with no unit, to measure the boundary), `update` runs it three
#             times: an agent-marked run, the run itself, and a second one to
#             prove it is quiet. `fugaro orchestrator run --once` is run twice as
#             the second-instance probe and is refused both times. No model call
#             and no run of your own is launched by this check.
# Runs:       by YOU. No agent runs any step of it, and a shell that arrived
#             with a coding agent's marker is refused rather than measured.
# Cleanup:    each phase removes the run's own scratch directory; the state file
#             it keeps between phases is removed by the phase that finishes the
#             half, and `forget` removes it at any time. Nothing else is
#             restored: the unit and the daemon are meant to stay installed and
#             running, which is what the `restore` row says in the block.
# Output:     progress on stderr, ONE block on stdout headed CHECK 301 RESULT, to
#             copy back. Exit 0 when every fact passed, 1 when one FAILed, 2 when
#             the run could not tell (a guard stopped it, a tool is missing, the
#             unit is not installed yet, the step you were asked for has not
#             happened).
#
# The state file. The phases are resumable through one small file under this
# check's own state directory — ${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/
# check301/state, mode 0600, carrying the machine's own readings (the boot
# identity, the unit's file and its hash, the daemon's pid, the version at the
# path the unit names) and nothing else. It is the reason `after-reboot` can say
# the reboot really happened and `upgrade` can say the binary moved: both compare
# against what the earlier phase recorded. It holds no credential and no
# document a project owns. CHECK301_PLATFORM and CHECK301_BOOT_ID are the
# hermetic test's seams for the things a fake cannot provide — the platform, the
# machine's boot identity, and how long the handover reading waits for the
# manager to report the new daemon (CHECK301_HANDOVER_SECONDS, four minutes by
# default); a real run sets none of them.
set -Eeuo pipefail

# No xtrace: this check reads no credential, but a trace of a run that carries
# none is still a trace nobody can read as this check's block, and the sibling
# checks refuse one for the same reason.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block goes to
# the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
USAGE="bash scripts/live-checks/check301.sh start|after-reboot|upgrade|update|forget [--installation <name>] [--machine <label>] [--dry-run]"
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
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

# oneline is how a command's own output reaches a FACT: one line, so a detail
# cannot break the block's shape, and bounded, so a whole log cannot arrive in
# one row.
oneline() { tr '\n' ' ' | tr -s ' ' | cut -c1-400; }

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as "the unit is fine" for a run that measured
# nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the daemon" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 301 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 301 needs bash"
  need mktemp
  need sed
  need awk
  need python3 "the reader that parses fugaro's own --json documents is one python3 file"
  need fugaro "every reading of the installation and every command this check is about is fugaro's"
  case "$PLATFORM" in
  darwin) need launchctl "the login unit is launchd's on this machine" ;;
  linux) need systemctl "the login unit is systemd's on this machine" ;;
  *) die "Check 301 knows launchd and systemd, and this machine is '$PLATFORM' (uname -s answered it)" ;;
  esac
}

# ------------------------------------------------------------- scratch + state
SCRATCH=""
SCRATCH_MARKER=".chk301-mine"
SCRATCH_REMOVED=no
STATE_DIR=""
STATE_FILE=""
STATE_MARKER=".check301-mine"
PRINTED=""
NOT_REACHED_TEXT="FAIL — not reached (the run stopped before this step)"
PLATFORM=""
MANAGER=""
UNIT_LABEL=dev.fugaro.orchestrator
SYSTEMD_UNIT=fugaro-orchestrator.service
PHASE=""
RELEASE_NEEDED=0.24.0
# How long the handover reading waits for the manager to report the new daemon:
# the daemon checks the version at the path its unit names every cycle (30s by
# default) and the manager starts the new one right after, so four minutes is
# generous on a machine that is doing something else too. The hermetic test sets
# it to a few seconds, which is the only reason it is a variable.
HANDOVER_SECONDS=${CHECK301_HANDOVER_SECONDS:-240}
case "$HANDOVER_SECONDS" in
"" | *[!0-9]*) HANDOVER_SECONDS=240 ;;
esac
INSTALLATION=""
MACHINE=""
DRY_RUN=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk301.XXXXXXXX") || die "Check 301 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 301 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

state_paths() {
  STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/check301"
  STATE_FILE="$STATE_DIR/state"
}

# state_dir_up creates this check's own state directory, or refuses to touch a
# directory it did not write: a phase that needs to record something must not
# clobber a path a person happens to have made, and the marker is what tells the
# two apart.
state_dir_up() {
  state_paths
  if [ ! -e "$STATE_DIR" ]; then
    (umask 077 && mkdir -p "$STATE_DIR") || die "Check 301 cannot make its state directory $STATE_DIR"
  fi
  if [ ! -f "$STATE_DIR/$STATE_MARKER" ] && [ -n "$(ls -A "$STATE_DIR" 2>/dev/null || true)" ]; then
    die "Check 301 stopped: $STATE_DIR holds files and carries no marker from this check — it is not this check's directory, so nothing here is written or removed. Move it aside and run again"
  fi
  : >"$STATE_DIR/$STATE_MARKER"
  chmod 700 "$STATE_DIR" 2>/dev/null || true
}

# state_write KEY=VALUE ...: the whole state file is rewritten each time, so a
# half-written file cannot be read as a state. It is written under umask 077 and
# moved into place, and it holds readings and no credential.
state_write() {
  local tmp="$STATE_FILE.tmp.$$"
  (umask 077 && printf '%s\n' "$@" >"$tmp") || die "Check 301 cannot write its state file $STATE_FILE"
  mv "$tmp" "$STATE_FILE" || die "Check 301 cannot move its state file into place at $STATE_FILE"
  chmod 600 "$STATE_FILE" 2>/dev/null || true
}

state_get() { # state_get KEY
  [ -f "$STATE_FILE" ] || return 0
  sed -n "s/^$1=//p" "$STATE_FILE" | head -1
}

state_present() { [ -f "$STATE_FILE" ]; }

state_remove() {
  [ -n "$STATE_FILE" ] || return 0
  rm -f "$STATE_FILE" 2>/dev/null || true
  if [ -e "$STATE_FILE" ]; then
    fact state FAIL "the state file $STATE_FILE could not be removed — remove it yourself (or run this check's 'forget')"
    return 0
  fi
  fact state PASS "the state file this check kept between the phases ($STATE_FILE) is removed; nothing else of it is left"
}

# state_keep is state_remove's other half: a phase that has to be run again says
# where its readings are instead of dropping them.
state_keep() { # why
  fact state INFO "the state file $STATE_FILE is KEPT ($1): run this phase again, with no new '$PROG start' — '$PROG forget' drops it"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
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
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it: a
  # block that read as a finished run would offer a row of FAILs as a verdict
  # about the daemon when the steps that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local note="nothing is restored: the login unit and the daemon are meant to stay installed and running, which is the point of this check"
  if [ "$SCRATCH_REMOVED" = yes ]; then
    note="$note; the run's scratch directory is gone"
  elif [ -n "$SCRATCH" ]; then
    note="$note; the scratch directory is STILL THERE, at $SCRATCH — remove it yourself"
  fi
  if [ "$SCRATCH_REMOVED" != yes ] && [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|FAIL|$note"
    if [ "$status" -eq 0 ]; then status=1; fi
  else
    facts="${facts}"$'\n'"restore|PASS|$note"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# run_bounded SECONDS KEY CMD...: run one command with its output in
# $SCRATCH/KEY.out and .err, and end it after SECONDS. `timeout` is not used: it
# is GNU coreutils, and this check runs on the owner's machine, where the tool is
# absent on macOS. Sets BOUNDED_STATUS and BOUNDED_TIMED_OUT.
#
# The poll is a tenth of a second rather than a whole one: nearly every command
# this wraps answers in milliseconds, so a one-second poll would add a second to
# each of them — a minute of pure waiting over one run, which is why the hermetic
# test would be minutes long for no reading it makes. Both platforms' sleep take
# a fraction.
run_bounded() {
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  "$@" >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0 limit=$((secs * 20))
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$limit" ] || break
    sleep 0.05
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

# bounded_ok SECONDS KEY CMD...: run_bounded, and REPORT whether it answered with
# exit 0. run_bounded itself always returns 0 — nearly every caller reads its
# answer out of BOUNDED_STATUS, and a helper that failed under `set -e` would end
# the run instead of letting the caller record what happened.
bounded_ok() {
  run_bounded "$@"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" -ne 0 ]; then return 1; fi
  return 0
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS): a trace of this run is not the run this block describes, and it prints every path and reading of it"
    stop "this shell is tracing (bash -x). Run it without -x: $USAGE — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, and this check reads no credential at all: every reading of the installation goes through your own fugaro, which carries your own credential itself"
}

# guard_agent: the check drives a login unit and a daemon, and one of its rows is
# what an agent-marked run of `fugaro update` does — a marker in the very shell
# that runs the check would make that row measure the check's own environment
# instead of the command's rule, so the run is refused rather than measured. The
# rule is internal/agentenv's Session: an agent-set marker whatever the terminal,
# the IDE's own marker (CLAUDE_CODE_SSE_PORT, which a person's IDE window sets
# too) only where no terminal is attached.
guard_agent() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then marker=$name; break; fi
  done
  if [ -z "$marker" ] && [ -n "$(printenv CLAUDE_CODE_SSE_PORT 2>/dev/null || true)" ] && [ ! -t 0 ]; then
    marker=CLAUDE_CODE_SSE_PORT
  fi
  if [ -n "$marker" ]; then
    fact agent FAIL "this shell carries a coding agent's marker ($marker is set), and Check 301 drives a login unit, a daemon and fugaro update on the machine it runs on: no agent runs any step of it"
    stop "Check 301 is yours to run and never an agent's (internal/agentenv's markers decide, and $marker is set). Run it in your own terminal window: $USAGE"
  fi
  fact agent PASS "this is not a coding agent's session (no agentenv marker is set), so the rows below measure the machine and not a session's own environment"
}

# guard_owner: `belong` is the owner's own installation and this check installs a
# daemon, starts a second instance and runs `fugaro update` against whatever the
# local config in force selects, so the name is refused here and the reading that
# follows names the installation it acted on.
guard_owner() {
  if [ "$PHASE" = forget ]; then
    # `forget` removes this check's own state file and nothing else, so the
    # installation in force is not part of what it does and is not asked for.
    return 0
  fi
  if [ -z "$INSTALLATION" ]; then
    stop "no installation is selected: the local config in force names none, and this check must know which installation it is driving the daemon for. Name it: $USAGE (--installation <name>, the name fugaro doctor --json reports as project.name)"
  fi
  case "$INSTALLATION" in
  belong | belong-*)
    fact installation FAIL "the installation in force is $INSTALLATION, which is the owner's own: Check 301 installs a unit, starts a daemon and runs fugaro update, and it is never run against belong"
    stop "Check 301 never runs against 'belong'. Point FUGARO_PROJECT at another installation, or name one: $USAGE"
    ;;
  esac
}

# guard_release: O9's unit and T7's re-render are on main and the daemon they
# describe is released with 0.24.0. A check that ran against an older binary
# would fail obscurely — a missing command, an update that names no unit — so the
# version and the commands are read first and the run stops with one sentence
# naming the release it needs.
guard_release() {
  if ! bounded_ok 30 release fugaro version; then
    fact release FAIL "fugaro version did not answer (exit $BOUNDED_STATUS): this check needs release $RELEASE_NEEDED"
    stop "fugaro version did not answer, so this check cannot tell which release it is looking at: needs release $RELEASE_NEEDED"
  fi
  local v
  v=$(tr -d '\r\n' <"$SCRATCH/release.out")
  case "$v" in
  "" | dev)
    fact release FAIL "this fugaro reports '$v', which is a build from a checkout and not a release: needs release $RELEASE_NEEDED"
    stop "this fugaro is '$v' (a checkout build). Check 301 needs the released $RELEASE_NEEDED: $USAGE"
    ;;
  esac
  if ! version_at_least "$v" "$RELEASE_NEEDED"; then
    fact release FAIL "this fugaro is $v: needs release $RELEASE_NEEDED"
    stop "this fugaro is $v and Check 301 needs release $RELEASE_NEEDED: run 'fugaro update' (the whole upgrade) or 'brew upgrade fugaro' first"
  fi
  local verb missing=""
  for verb in status install uninstall; do
    if ! bounded_ok 30 "verb-$verb" fugaro orchestrator "$verb" --help; then missing="$missing $verb"; fi
  done
  if [ -n "$missing" ]; then
    fact release FAIL "this fugaro ($v) carries no 'fugaro orchestrator$missing': needs release $RELEASE_NEEDED"
    stop "this fugaro ($v) is missing 'fugaro orchestrator$missing', the login-unit commands O9 ships: needs release $RELEASE_NEEDED"
  fi
  fact release PASS "this fugaro is $v (release $RELEASE_NEEDED or newer) and it carries the login unit's commands: 'fugaro orchestrator status|install|uninstall' answer"
}

version_at_least() { # version_at_least HAVE WANT
  awk -v a="${1#v}" -v b="${2#v}" '
  function num(s, f) { f = s; gsub(/[^0-9.].*$/, "", f); return f }
  function part(s, i) { return (i in s && s[i] != "") ? s[i] + 0 : 0 }
  BEGIN {
    n = split(num(a), x, ".")
    m = split(num(b), y, ".")
    for (i = 1; i <= (n > m ? n : m); i++) {
      if (part(x, i) > part(y, i)) exit 0
      if (part(x, i) < part(y, i)) exit 1
    }
    exit 0
  }'
}

guard_platform() {
  PLATFORM=${CHECK301_PLATFORM:-}
  if [ -z "$PLATFORM" ]; then
    PLATFORM=$(uname -s 2>/dev/null || true)
    PLATFORM=$(printf '%s' "$PLATFORM" | tr '[:upper:]' '[:lower:]')
  fi
  case "$PLATFORM" in
  darwin) MANAGER=launchd ;;
  linux) MANAGER=systemd ;;
  *) die "Check 301 knows launchd (Darwin) and systemd (Linux), and this machine answers '$PLATFORM'" ;;
  esac
}

parse_args() {
  PHASE=""
  INSTALLATION=${FUGARO_PROJECT:-}
  MACHINE=""
  DRY_RUN=no
  while [ $# -gt 0 ]; do
    case "$1" in
    start | after-reboot | upgrade | update | forget)
      [ -z "$PHASE" ] || die "one phase per run, and '$PHASE' was already given. usage: $USAGE"
      PHASE=$1
      shift
      ;;
    --installation)
      [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --machine)
      [ $# -ge 2 ] || die "--machine needs a label. usage: $USAGE"
      MACHINE=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      # The usage is the whole answer, so there is no block: a CHECK 301 RESULT
      # for a run that ran nothing would be a verdict about a machine this run
      # never looked at.
      REACHED_END=yes
      PRINTED=1
      printf 'usage: %s\n' "$USAGE"
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$PHASE" ] || die "name the phase to run. usage: $USAGE"
  if [ -z "$INSTALLATION" ] && [ "$PHASE" != forget ]; then
    INSTALLATION=$(only_installation)
  fi
  if [ -z "$MACHINE" ]; then
    MACHINE="$PLATFORM:$(uname -n 2>/dev/null || echo unknown)"
  fi
}

# only_installation is the installation a run with no --installation drove: the
# configuration directory's only project config, and nothing when there is none
# or more than one. It is read rather than asked of fugaro because `fugaro
# doctor --json` reaches the cloud and this check reads nothing that way until it
# has to.
only_installation() {
  local dir="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects" f found="" n=0
  for f in "$dir"/*.yaml; do
    [ -f "$f" ] || continue
    n=$((n + 1))
    found=${f##*/}
    found=${found%.yaml}
  done
  if [ "$n" -eq 1 ]; then printf '%s' "$found"; fi
}

gcp_project_of() { # gcp_project_of NAME
  local cfg="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$1.yaml"
  [ -r "$cfg" ] || return 0
  sed -n 's/^ *gcp_project: *//p' "$cfg" | tr -d "\"'" | head -1
}

# ------------------------------------------------------------ the machine reads
# boot_identity is what proves a reboot: the kernel's own boot id on Linux, the
# boot time on Darwin. It is recorded before the reboot and compared after it,
# which is the only reading here that a person's word cannot stand in for.
boot_identity() {
  if [ -n "${CHECK301_BOOT_ID:-}" ]; then printf '%s' "$CHECK301_BOOT_ID"; return 0; fi
  if [ -r /proc/sys/kernel/random/boot_id ]; then
    tr -d '\r\n' </proc/sys/kernel/random/boot_id
    return 0
  fi
  sysctl -n kern.boottime 2>/dev/null | sed -n 's/.*sec = \([0-9][0-9]*\).*/\1/p'
}

# read_unit asks `fugaro orchestrator status`, which reads the unit's file and
# nothing else, and sets UNIT_STATE (none|current|stale|unknown), UNIT_PATH and
# UNIT_NOTE. The path comes from its own answer rather than from a second copy of
# the renderer's rule, so the file this check reads is the file that command is
# talking about.
UNIT_STATE=""
UNIT_PATH=""
UNIT_NOTE=""
read_unit() { # read_unit KEY
  run_bounded 30 "$1" fugaro orchestrator status
  local out="$SCRATCH/$1.out"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    UNIT_STATE=unknown
    stop "fugaro orchestrator status did not answer within 30s: this check cannot read the login unit. Run it yourself and fix what it reports"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    UNIT_STATE=unknown
    sed -n '1,5p' "$SCRATCH/$1.err" >&4 || true
    stop "fugaro orchestrator status exited $BOUNDED_STATUS: this check cannot read the login unit. Run it yourself and fix what it reports"
  fi
  UNIT_NOTE=$(head -1 "$out")
  UNIT_PATH=$(sed -n 's/^the login unit at \(.*\) is .*$/\1/p' "$out" | head -1)
  if [ -z "$UNIT_PATH" ]; then
    UNIT_PATH=$(sed -n 's/^the login unit is not installed: \(.*\) does not exist$/\1/p' "$out" | head -1)
  fi
  case "$UNIT_NOTE" in
  *"is not installed"*) UNIT_STATE=none ;;
  *"is installed and current"*) UNIT_STATE=current ;;
  *"is out of date"*) UNIT_STATE=stale ;;
  *) UNIT_STATE=unknown ;;
  esac
}

# unit_program is the path the unit names as its program: the first string of a
# launchd plist's ProgramArguments, or systemd's ExecStart's first word. It is
# read out of the file rather than derived from this process, because the whole
# point of the upgrade half is that the file names a path a package manager
# replaces.
unit_program() { # unit_program FILE
  case "$MANAGER" in
  launchd)
    awk '/<key>ProgramArguments<\/key>/{f=1} f && /<string>/{sub(/.*<string>/,"");sub(/<\/string>.*/,"");print;exit}' "$1"
    ;;
  systemd)
    sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' "$1" | head -1
    ;;
  esac
}

unit_hash() { # unit_hash FILE
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    cksum "$1" | awk '{print $1 "-" $2}'
  fi
}

binary_version_at() { # binary_version_at PATH
  "$1" --version 2>/dev/null | head -1 | tr -d '\r'
}

# read_manager asks the service manager what it knows about the unit. Only its
# own read verbs are used (`launchctl print`, `systemctl show`): this check never
# issues a command that starts, stops or restarts the service, and that is what
# makes the upgrade half's handover a reading of the machine rather than of
# something this check did.
MANAGER_STATE=""
MANAGER_PID=""
read_manager() { # read_manager KEY
  case "$MANAGER" in
  launchd) run_bounded 20 "$1" launchctl print "gui/$(id -u)/$UNIT_LABEL" ;;
  systemd) run_bounded 20 "$1" systemctl --user show -p LoadState -p ActiveState -p SubState -p MainPID "$SYSTEMD_UNIT" ;;
  esac
  local out="$SCRATCH/$1.out"
  MANAGER_STATE=unknown
  MANAGER_PID=""
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then MANAGER_STATE=unknown; return 0; fi
  case "$MANAGER" in
  launchd)
    if [ "$BOUNDED_STATUS" -ne 0 ]; then MANAGER_STATE=absent; return 0; fi
    case "$(sed -n 's/^[[:space:]]*state = //p' "$out" | head -1)" in
    running) MANAGER_STATE=running ;;
    *) MANAGER_STATE=notrunning ;;
    esac
    MANAGER_PID=$(sed -n 's/^[[:space:]]*pid = //p' "$out" | head -1)
    ;;
  systemd)
    if [ "$BOUNDED_STATUS" -ne 0 ]; then MANAGER_STATE=unknown; return 0; fi
    if [ "$(sed -n 's/^LoadState=//p' "$out" | head -1)" != loaded ]; then MANAGER_STATE=absent; return 0; fi
    case "$(sed -n 's/^ActiveState=//p' "$out" | head -1)" in
    active) MANAGER_STATE=running ;;
    *) MANAGER_STATE=notrunning ;;
    esac
    MANAGER_PID=$(sed -n 's/^MainPID=//p' "$out" | head -1)
    if [ "$MANAGER_PID" = 0 ]; then MANAGER_PID=""; fi
    ;;
  esac
  return 0
}

# manager_words is what the manager said, as the block prints it, so a row about
# the daemon carries the manager's own words and not this check's paraphrase.
manager_words() {
  printf '%s' "$MANAGER the unit is ${MANAGER_STATE}${MANAGER_PID:+, pid $MANAGER_PID}"
}

# read_lease asks `fugaro orchestrator ls --json` who holds the id, and never the
# database directly: the listing is the daemon's own record read through your own
# credential, and it is what tells local from central.
LEASE_LOCAL=""
LEASE_HOLDER=""
LEASE_BEAT=""
LEASE_HOLDERS="0"
read_lease() { # read_lease KEY
  run_bounded 60 "$1" fugaro orchestrator ls --json
  LEASE_LOCAL=""
  LEASE_HOLDER=""
  LEASE_BEAT=""
  LEASE_HOLDERS=0
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    fact lease FAIL "fugaro orchestrator ls --json did not answer within 60s, so whether any daemon holds this installation's lease cannot be told"
    return 1
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    fact lease FAIL "fugaro orchestrator ls --json exited $BOUNDED_STATUS: the lease of installation $INSTALLATION could not be read ($(sed -n '1p' "$SCRATCH/$1.err" 2>/dev/null || true))"
    return 1
  fi
  local answer
  answer=$(python3 "$SCRATCH/ls_reader.py" "$SCRATCH/$1.out") || {
    fact lease FAIL "the answer to fugaro orchestrator ls --json is not the document this check reads (is this fugaro too old to print --json?)"
    return 1
  }
  LEASE_LOCAL=$(printf '%s\n' "$answer" | sed -n 's/^local=//p')
  LEASE_HOLDER=$(printf '%s\n' "$answer" | sed -n 's/^local_holder=//p')
  LEASE_BEAT=$(printf '%s\n' "$answer" | sed -n 's/^local_beat=//p')
  LEASE_HOLDERS=$(printf '%s\n' "$answer" | sed -n 's/^holders=//p')
  return 0
}

# second_instance_probe starts a second daemon by hand and reads the refusal. It
# refuses to take the lease or launch anything: a fresh holder is what the call
# is made under, and the probe is skipped when the lease names none — a second
# instance started against a free lease would take it and drive one cycle, which
# is the one thing this probe must never do.
SECOND_REFUSAL=""
probe_second_instance() {
  SECOND_REFUSAL=""
  run_bounded 60 second fugaro orchestrator run --once
  SECOND_REFUSAL=$(grep -h 'driven by another daemon right now' "$SCRATCH/second.out" "$SCRATCH/second.err" 2>/dev/null | head -1 || true)
}

probe_holder() {
  printf '%s' "$SECOND_REFUSAL" | sed -n 's/.*(holder \([^,)]*\),.*/\1/p' | head -1
}

# ------------------------------------------------------------------ the plans
plan_start() {
  cat <<PLAN
check301 start — the plan, and nothing of it is run in a dry run:
  1. read this machine's platform and the manager that owns the login unit
  2. read which installation is in force (and refuse belong)
  3. read the release: fugaro version, and the login unit's own commands
  4. read the login unit's file with 'fugaro orchestrator status'
  5. read the manager: is the unit loaded and is the daemon running, and under which pid
  6. record the pre-reboot readings in $STATE_DIR/state (mode 0600): the boot
     identity, the unit's path and hash, the daemon's pid, the version at the
     path the unit names
  7. print the one thing you do: reboot this machine
  You do, before step 4 can pass: nothing — the unit is installed by
  'fugaro orchestrator install' (which asks you to type the word install) if it
  is not there yet.
PLAN
}

plan_after_reboot() {
  cat <<PLAN
check301 after-reboot — the plan, and nothing of it is run in a dry run:
  1. read the state file $STATE_DIR/state that 'start' recorded on this machine
  2. compare this machine's boot identity with the recorded one (the reboot)
  3. read the manager: the unit is loaded and the daemon is running again, under a
     pid that differs from the recorded one
  4. read 'fugaro orchestrator ls --json': exactly one holder for this
     installation's orchestrator, and the beat it last made
  5. run 'fugaro orchestrator run --once' as the second instance you would have
     started by hand: it must be refused, naming the holder read in step 4
  6. remove the state file and print the next step (the upgrade half, on a
     throwaway machine)
PLAN
}

plan_upgrade() {
  cat <<PLAN
check301 upgrade — the plan, and nothing of it is run in a dry run:
  1. read the platform, the release and the login unit; record the version at the
     path the unit names, the unit's hash and the daemon's pid
  2. on a machine whose unit is not installed yet: run 'fugaro update --yes' and
     read that it wrote no unit — the boundary T7 keeps — then hand the install
     to you
  3. print the one thing you do: replace the binary the unit names, brew-style
     ('brew upgrade fugaro', or the release over that path)
  4. read the handover: the version at the unit's path moved, the manager reports
     a new pid in state running, and the unit's file is byte-for-byte what it was
     — this check issues no command that starts or restarts the service
  5. read the lease again and refuse a second instance by hand
  6. remove the state file
PLAN
}

plan_update() {
  cat <<PLAN
check301 update — the plan, and nothing of it is run in a dry run:
  1. read the login unit ('fugaro orchestrator status') and record its hash, the
     daemon's pid and whether this release's template differs from the file
  2. run 'fugaro update --yes' with a coding agent's marker set (CLAUDECODE=1):
     it must write no unit and reload nothing (the G2 marker rule)
  3. run 'fugaro update --yes' — the one command T7's half is about — and read
     whether it re-rendered the unit, reloaded the service and named the unit's
     file in its plan
  4. run 'fugaro update --yes' a second time: it writes nothing and reloads
     nothing
  5. read the lease and refuse a second instance by hand
  6. remove the state file
PLAN
}

plan_forget() {
  cat <<PLAN
check301 forget — the plan, and nothing of it is run in a dry run:
  remove $STATE_DIR (its state file, its marker and the directory itself), which
  is the only thing this check leaves behind between the phases. The login unit,
  the daemon and the state node are not touched.
PLAN
}

print_plan() {
  local text
  case "$PHASE" in
  start) text=$(plan_start) ;;
  after-reboot) text=$(plan_after_reboot) ;;
  upgrade) text=$(plan_upgrade) ;;
  update) text=$(plan_update) ;;
  forget) text=$(plan_forget) ;;
  esac
  say "dry run: NOTHING below was run."
  printf '%s\n' "$text" | sed "s|^|$PROG: |" >&2
}

# ---------------------------------------------------------------- the phases
phase_start() {
  read_unit unit-status
  case "$UNIT_STATE" in
  none)
    fact unit INFO "the login unit is not installed at $UNIT_PATH, so there is nothing for a reboot to bring back yet. This is the one thing you do here: in this terminal run 'fugaro orchestrator install' (it asks you to type the word install — no flag stands in for it, so this check cannot run it for you), then run this phase again"
    fact human-install INFO "run in your own terminal, once: fugaro orchestrator install"
    stop "the login unit is not installed on this machine yet: run 'fugaro orchestrator install' (typing the word install) and then '$USAGE'"
    ;;
  unknown)
    fact unit FAIL "fugaro orchestrator status did not answer a state this check reads: its first line is '$UNIT_NOTE'"
    stop "fugaro orchestrator status did not answer the unit's state: $UNIT_NOTE"
    ;;
  current)
    fact unit PASS "the login unit at $UNIT_PATH is installed and current: the file is this release's own template"
    ;;
  stale)
    fact unit PASS "the login unit at $UNIT_PATH is installed: its file is not this release's template ('$UNIT_NOTE'), which is what the update phase is about — the daemon runs either way"
    ;;
  esac

  if [ -z "$UNIT_PATH" ]; then
    fact unit FAIL "fugaro orchestrator status named no unit path, so this check cannot read the file or the manager's own report of it"
    stop "fugaro orchestrator status named no path for the login unit: run it yourself and read its answer"
  fi
  if [ ! -r "$UNIT_PATH" ]; then
    fact unit FAIL "the login unit's file $UNIT_PATH cannot be read, so this check cannot read the path it names as its program"
    stop "the login unit at $UNIT_PATH is not readable by this user: run 'fugaro orchestrator status' yourself and read what it says"
  fi
  local program hash version
  program=$(unit_program "$UNIT_PATH")
  if [ -z "$program" ]; then
    fact unit FAIL "the login unit at $UNIT_PATH names no program this check can read (a launchd plist's ProgramArguments or systemd's ExecStart), so the upgrade half could not read the version at that path"
    stop "the login unit at $UNIT_PATH names no program: read it yourself ('$MANAGER' owns it) and run 'fugaro orchestrator install' if it is malformed"
  fi
  hash=$(unit_hash "$UNIT_PATH")
  version=$(binary_version_at "$program")

  read_manager manager-before
  case "$MANAGER_STATE" in
  running)
    fact daemon PASS "the unit's daemon is running: $(manager_words), and the program the unit names is $program"
    ;;
  notrunning | absent)
    fact daemon FAIL "the unit is installed and $(manager_words): the install did not bring the daemon up, so nothing here has a daemon to survive a reboot. Read the manager's own report ($([ "$MANAGER" = launchd ] && echo "launchctl print gui/$(id -u)/$UNIT_LABEL" || echo "systemctl --user status $SYSTEMD_UNIT")) and fix what it says"
    stop "the login unit is installed and its daemon is not running ($(manager_words)): there is nothing for a reboot to bring back yet, so this phase records nothing and asks you for no reboot. Fix what the manager reports and run it again"
    ;;
  *)
    fact daemon FAIL "the manager did not answer a state this check reads ($(manager_words)), so whether the daemon is running cannot be told"
    stop "the manager did not answer a state this check reads ($(manager_words)): run '$([ "$MANAGER" = launchd ] && echo "launchctl print gui/$(id -u)/$UNIT_LABEL" || echo "systemctl --user status $SYSTEMD_UNIT")' yourself and read what it says"
    ;;
  esac

  state_dir_up
  if state_present && [ "$(state_get half)" != reboot ]; then
    fact state FAIL "a state file from an earlier half is already at $STATE_FILE ($(state_get half)/$(state_get stage), written by $(state_get machine)): this phase would overwrite readings another half still needs"
    stop "$STATE_FILE already holds a $(state_get half)/$(state_get stage) run. Finish it or drop it ('$PROG forget'), then run this phase again"
  fi
  if state_present && [ "$(state_get machine)" != "$MACHINE" ]; then
    fact state FAIL "the state file at $STATE_FILE was written by $(state_get machine) and this machine is $MACHINE"
    stop "the recorded run is $(state_get machine)'s and this is $MACHINE: run this phase on that machine, or drop the state ('$PROG forget')"
  fi
  if state_present; then
    # Re-running 'start' on the same machine is allowed: it replaces the
    # readings, and the reboot proof is "the boot identity moved since the last
    # start", and a fresh start after a reboot correctly fails that until the
    # machine reboots again.
    fact state INFO "the readings from an earlier 'start' on this machine are replaced by this run's (the earlier one was $(state_get stage), recorded at $(state_get at))"
  fi
  state_write \
    "half=reboot" \
    "stage=pending-reboot" \
    "machine=$MACHINE" \
    "platform=$PLATFORM" \
    "manager=$MANAGER" \
    "installation=$INSTALLATION" \
    "unit=$UNIT_PATH" \
    "unit_sha=$hash" \
    "unit_state=$UNIT_STATE" \
    "program=$program" \
    "version=$version" \
    "boot=$(boot_identity)" \
    "pid=$MANAGER_PID" \
    "holder=" \
    "at=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo unknown)"
  fact state PASS "the pre-reboot readings are recorded at $STATE_FILE (mode 0600): the boot identity, the unit's path and hash, the daemon's pid and the version at $program — 'after-reboot' reads them and removes them"

  fact human-reboot INFO "this is the one thing you do: reboot this machine now (Apple menu → Restart, or 'sudo shutdown -r now'), then run: bash scripts/live-checks/check301.sh after-reboot. Nothing has to be typed into this check to confirm it: the next phase proves the reboot by the machine's own boot identity"
  say "now reboot this machine: sudo shutdown -r now"
  say "then run: bash scripts/live-checks/check301.sh after-reboot"
}

phase_after_reboot() {
  state_dir_up
  if ! state_present; then
    fact state FAIL "there is no state file at $STATE_FILE: 'after-reboot' compares against what 'start' recorded on this machine, and it cannot tell a reboot from a machine that never ran it"
    stop "no state from 'start' is at $STATE_FILE on this machine: run '$PROG start' first, on this same machine and in this same home directory"
  fi
  local was_machine was_boot was_pid was_unit was_half was_stage
  was_machine=$(state_get machine)
  was_boot=$(state_get boot)
  was_pid=$(state_get pid)
  was_unit=$(state_get unit)
  was_half=$(state_get half)
  was_stage=$(state_get stage)
  if [ "$was_half" != reboot ] || [ "$was_stage" != pending-reboot ]; then
    fact state FAIL "the state file at $STATE_FILE is a $was_half/$was_stage run, not the 'start' this phase reads"
    stop "$STATE_FILE holds a $was_half/$was_stage run and not a pending 'start': drop it ('$PROG forget') and run '$PROG start' again"
  fi
  if [ "$was_machine" != "$MACHINE" ]; then
    fact state FAIL "the state file at $STATE_FILE was written by $was_machine and this machine is $MACHINE: the reboot this phase proves has to be the same machine's"
    stop "the recorded run is $was_machine's and this is $MACHINE: run this phase on that machine, or drop the state ('$PROG forget')"
  fi

  # A reading that a re-run fixes — the reboot itself, or a daemon the reboot
  # did not bring back — keeps the state: the phase says "run this again", and a
  # re-run with no state would only be told to start over.
  local again=no
  local now_boot
  now_boot=$(boot_identity)
  if [ -z "$now_boot" ] || [ -z "$was_boot" ]; then
    fact boot INFO "this machine's boot identity could not be read (it was '$was_boot' before), so whether the machine really rebooted is not measured here — the daemon row below still reads what the manager says"
  elif [ "$now_boot" = "$was_boot" ]; then
    fact boot FAIL "the machine has NOT rebooted since 'start' recorded it: the boot identity is still '$was_boot'. The install and the reboot are the two things this half is: reboot the machine and run this phase again"
    again=yes
  else
    fact boot PASS "the machine really rebooted: its boot identity moved from '$was_boot' to '$now_boot'"
  fi

  read_unit unit-status-2
  if [ "$UNIT_PATH" != "$was_unit" ]; then
    fact unit FAIL "the login unit this release renders is $UNIT_PATH and the one 'start' recorded is $was_unit: the unit moved between the two phases, so the readings below are about another file"
  elif [ -r "$UNIT_PATH" ]; then
    fact unit PASS "the login unit at $UNIT_PATH is installed and current after the reboot ($UNIT_NOTE)"
  else
    fact unit FAIL "the login unit at $UNIT_PATH is gone after the reboot: something removed it, and no daemon will be started at the next login"
  fi

  read_manager manager-after
  if [ "$MANAGER_STATE" = running ] && [ -n "$MANAGER_PID" ] && [ "$MANAGER_PID" != "$was_pid" ]; then
    fact daemon PASS "the login unit brought the daemon back after the reboot with no terminal and no command: $(manager_words), and its pid moved from $was_pid to $MANAGER_PID"
  elif [ "$MANAGER_STATE" = running ]; then
    fact daemon FAIL "the manager reports the daemon running under pid $MANAGER_PID, which is the pid 'start' recorded: that process was started before the reboot, so the reboot was not survived by a new one — read the manager's own report and fix what it says"
    again=yes
  else
    fact daemon FAIL "the unit is installed and $(manager_words) after the reboot: the daemon is not running, so nothing came back at login. Read the manager's own report and the daemon's log and fix what it says"
    again=yes
  fi

  if read_lease lease-ls; then
    if [ -z "$LEASE_LOCAL" ]; then
      fact lease FAIL "fugaro orchestrator ls names no orchestrator this machine runs (kind=local), so the id the daemon drives cannot be told ($(printf '%s' "$LEASE_HOLDERS") holder(s) in the listing)"
    elif [ -z "$LEASE_HOLDER" ]; then
      fact lease FAIL "the listing names this machine's orchestrator $LEASE_LOCAL and no holder for its lease: no daemon is beating it, so nothing holds this installation's orchestrator"
    elif [ "$LEASE_HOLDERS" != 1 ]; then
      fact lease FAIL "the listing names $LEASE_HOLDERS holders across the installation's orchestrators, and exactly one is what this check reads: $(printf '%s' "$LEASE_HOLDERS") daemons are driving ids in this installation"
    else
      fact lease PASS "exactly one holder for installation $INSTALLATION: orchestrator $LEASE_LOCAL is held by $LEASE_HOLDER, whose last beat the database stamped $LEASE_BEAT (the server's own clock)"
    fi
  fi

  if [ -z "$LEASE_HOLDER" ]; then
    fact second-refuses INFO "the lease names no holder, so the second instance is NOT started by hand here: a second daemon would take a free lease and drive one cycle, which is the one thing this probe must never do"
  else
    probe_second_instance
    local named
    named=$(probe_holder)
    if [ -n "$SECOND_REFUSAL" ] && [ "$named" = "$LEASE_HOLDER" ]; then
      fact second-refuses PASS "a second instance started by hand ('fugaro orchestrator run --once') was refused by the daemon that holds the lease, naming the same holder the listing read ($named): $(printf '%s' "$SECOND_REFUSAL")"
    elif [ -n "$SECOND_REFUSAL" ]; then
      fact second-refuses FAIL "a second instance was refused, and for another holder ($named) than the listing read ($LEASE_HOLDER): the lease moved while this phase ran, so the two readings are not about the same daemon"
    elif [ "$BOUNDED_TIMED_OUT" = yes ]; then
      fact second-refuses FAIL "the second instance was still running after 60s: it did not refuse, so it may have taken the lease — check 'fugaro orchestrator ls' and stop it ($(printf '%s' "$SCRATCH/second.out" | tail -c 300))"
    else
      fact second-refuses FAIL "the second instance exited $BOUNDED_STATUS without the refusal this check reads (want 'is driven by another daemon right now'): a second daemon is NOT refused this installation's orchestrator, which is the concurrency fence the lease is. Its last lines: $(tail -n 3 "$SCRATCH/second.out" "$SCRATCH/second.err" 2>/dev/null | tr '\n' ' ')"
    fi
  fi

  if [ "$again" = yes ]; then
    state_keep "the reboot or the daemon reading above did not pass, and both are read again on a run of this phase"
  else
    state_remove
  fi
  fact human-next INFO "the next step is the upgrade half, on a THROWAWAY machine (macOS or Linux, never this one if you care about it): there run 'fugaro orchestrator install' (typing the word install), then: bash scripts/live-checks/check301.sh upgrade --machine <label>"
  say "next, on a throwaway machine: fugaro orchestrator install, then bash scripts/live-checks/check301.sh upgrade"
}

phase_upgrade() {
  state_dir_up
  local half stage
  half=$(state_get half)
  stage=$(state_get stage)
  if state_present && [ "$half" != upgrade ]; then
    fact state FAIL "the state file at $STATE_FILE is a $half/$stage run: the upgrade half needs no reboot, and this phase must not overwrite a 'start' that has not been followed by 'after-reboot'"
    stop "$STATE_FILE holds a $half/$stage run: finish it ('$PROG after-reboot') or drop it ('$PROG forget'), then run this phase again"
  fi
  local was_machine was_version was_pid was_sha was_unit
  was_machine=$(state_get machine)
  was_version=$(state_get version)
  was_pid=$(state_get pid)
  was_sha=$(state_get unit_sha)
  was_unit=$(state_get unit)
  if state_present && [ "$was_machine" != "$MACHINE" ]; then
    fact state FAIL "the state file at $STATE_FILE was written by $was_machine and this machine is $MACHINE"
    stop "the recorded run is $was_machine's and this is $MACHINE: run this phase on that machine, or drop the state ('$PROG forget')"
  fi

  read_unit unit-status
  local measuring=no
  if [ "$stage" = pending-upgrade ] && [ -n "$was_version" ]; then measuring=yes; fi
  case "$UNIT_STATE" in
  none)
    if [ "$measuring" = yes ]; then
      fact never-installs FAIL "the unit is gone from $UNIT_PATH, and the upgrade half was waiting to read a handover on it: nothing can be measured on a machine with no unit"
      stop "the login unit at $UNIT_PATH was removed while the upgrade half was waiting: run 'fugaro orchestrator install' and start this half again"
    fi
    fact unit INFO "there is no login unit on this machine (${UNIT_PATH:+$UNIT_PATH does not exist}), which is what this half reads the boundary on"
    if ! bounded_ok 600 never-installs fugaro update --yes; then
      fact never-installs FAIL "the unit is not installed here and 'fugaro update --yes' exited $BOUNDED_STATUS: the boundary reading needs the command to converge (or to report a settled installation) and this run did neither. Its last lines: $(tail -n 5 "$SCRATCH/never-installs.out" "$SCRATCH/never-installs.err" 2>/dev/null | tr '\n' ' ')"
    else
      read_unit unit-status-2
      if [ "$UNIT_STATE" = none ]; then
        fact never-installs PASS "this machine has no login unit and 'fugaro update --yes' (exit 0) wrote none: update never installs a unit, which is the boundary T7 keeps — 'fugaro orchestrator install' (and fugaro setup) is the door that writes the first one"
      else
        fact never-installs FAIL "'fugaro update --yes' wrote a login unit at $UNIT_PATH on a machine that had none: update installed a unit, which T7's ruling refuses"
      fi
    fi
    fact handover INFO "nothing to hand over: this machine has no login unit, so the binary at the path a unit would name was never replaced and no daemon was restarted here"
    fact human-install INFO "now install the unit here, in your own terminal, once: fugaro orchestrator install (it asks you to type the word install)"
    fact state INFO "no state file is written: this phase stopped before it had a unit to read a handover on"
    stop "no login unit is installed on this machine, and 'fugaro update' writes none: run 'fugaro orchestrator install' (typing the word install) and then run this phase again"
    ;;
  unknown)
    fact unit FAIL "fugaro orchestrator status did not answer a state this check reads: its first line is '$UNIT_NOTE'"
    stop "fugaro orchestrator status did not answer the unit's state: $UNIT_NOTE"
    ;;
  esac
  local program hash version
  program=$(unit_program "$UNIT_PATH")
  hash=$(unit_hash "$UNIT_PATH")
  version=$(binary_version_at "$program")
  case "$UNIT_STATE" in
  current) fact unit PASS "the login unit at $UNIT_PATH is installed and current; its program is $program" ;;
  stale) fact unit PASS "the login unit at $UNIT_PATH is installed and its file is not this release's template ('$UNIT_NOTE'); the update phase is where that is re-rendered, and the program it names is $program" ;;
  esac
  if [ -z "$program" ]; then
    fact unit FAIL "the login unit at $UNIT_PATH names no program this check can read, so the version at that path cannot be read before the upgrade"
    stop "the login unit at $UNIT_PATH names no program: read it yourself ('$MANAGER' owns it)"
  fi

  fact release PASS "the release gate was read at the start of this run (fugaro $RELEASE_NEEDED or newer, with the login unit's commands)"

  if [ "$measuring" != yes ]; then
    read_manager manager-before
    if [ "$MANAGER_STATE" != running ]; then
      fact daemon FAIL "the upgrade half needs the daemon running under the unit — that is the whole reading — and $(manager_words). Start it ('fugaro orchestrator install' loads the unit; the machine it runs on is a throwaway one) and run this phase again"
      stop "the daemon is not running under the login unit ($(manager_words)), so there is nothing to hand over to the new binary"
    fi
    fact daemon PASS "the unit's daemon is running before the upgrade: $(manager_words)"
    fact version INFO "the binary the unit names ($program) reports '$version' now; the handover is read against this"
    state_write \
      "half=upgrade" \
      "stage=pending-upgrade" \
      "machine=$MACHINE" \
      "platform=$PLATFORM" \
      "manager=$MANAGER" \
      "installation=$INSTALLATION" \
      "unit=$UNIT_PATH" \
      "unit_sha=$hash" \
      "unit_state=$UNIT_STATE" \
      "program=$program" \
      "version=$version" \
      "boot=$(boot_identity)" \
      "pid=$MANAGER_PID" \
      "holder=" \
      "at=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo unknown)"
    fact state INFO "the before-readings are recorded at $STATE_FILE: the version at $program ($version), the unit's hash and the daemon's pid ($MANAGER_PID). This phase now stops for the one thing you do"
    if command -v brew >/dev/null 2>&1; then
      fact handover INFO "not measured yet — this is the one thing you do: run 'brew upgrade fugaro' in another window, and run this phase again. Nothing here restarts the service: the daemon reads the version at the path the unit named every cycle and exits cleanly, and launchd's KeepAlive or systemd's Restart=always starts the new one"
      fact human-upgrade INFO "run in your own terminal, once: brew upgrade fugaro"
      stop "the upgrade has not happened yet: run 'brew upgrade fugaro' and then run this phase again — the check reads the version at $program, the pid the manager reports, and the unit's own file"
    fi
    fact handover INFO "not measured yet — this is the one thing you do: replace the binary the unit names ($program) with the new release, the way this machine installs fugaro (no brew was found on PATH: the release's own archive over that path is the same thing), and run this phase again. Nothing here restarts the service: the daemon reads the version at that path every cycle and exits cleanly, and the unit starts the new one"
    fact human-upgrade INFO "run in your own terminal, once: replace $program with the new release (brew upgrade fugaro, where brew is the install)"
    stop "the upgrade has not happened yet: replace $program with the new release and run this phase again"
  fi

  # The measurement: this run is the one after the human step.
  if [ "$version" = "$was_version" ]; then
    fact version FAIL "the binary the unit names ($program) still reports '$was_version', the version this phase recorded before the upgrade: the upgrade has not happened yet. The version is read with '$program --version' — the same read the daemon makes of the path its unit names — so replace that binary and run this phase again"
    fact state INFO "the before-readings are kept at $STATE_FILE (version $was_version at $program, unit hash $was_sha, pid $was_pid): run the command named in the fact above and then this phase again"
    stop "the binary at $program is still $was_version: nothing moved, so there is no handover to read"
  fi
  fact version PASS "the binary the unit names ($program) reported '$was_version' before the upgrade and reports '$version' now"
  read_manager manager-after
  local waited=0
  while [ -n "$MANAGER_PID" ] && [ "$MANAGER_PID" = "$was_pid" ] && [ "$waited" -lt "$HANDOVER_SECONDS" ]; do
    sleep 5
    waited=$((waited + 5))
    read_manager "manager-after-$waited"
  done
  if [ "$MANAGER_STATE" != running ]; then
    fact handover FAIL "the binary at $program moved from '$was_version' to '$version' and $(manager_words): the unit's new daemon is not running. The unit is written with launchd's KeepAlive and systemd's Restart=always, so a clean exit is followed by a start of the program at that path — read the manager's own report and the daemon's log"
  elif [ -n "$MANAGER_PID" ] && [ "$MANAGER_PID" = "$was_pid" ]; then
    fact handover FAIL "the binary at $program moved from '$was_version' to '$version' and the daemon is still pid $was_pid after ${waited}s: the handover did not happen. The daemon reads the version at the path its unit named every cycle and exits cleanly on a difference, so check that the unit's program and its --binary are the same path ($program) and that $MANAGER is really running that unit"
  elif [ -z "$MANAGER_PID" ]; then
    fact handover FAIL "the binary at $program moved to '$version' and the manager reports the daemon running without a pid ($(manager_words)): which process runs the new binary cannot be told"
  else
    fact handover PASS "the handover happened with no command typed by anyone but your upgrade: the binary at $program is '$version' now (was '$was_version'), and $MANAGER restarted the daemon by itself — $(manager_words), pid moved from $was_pid to $MANAGER_PID after ${waited}s. This check issued no command that starts or restarts the service: it only reads the manager (launchctl print / systemctl show)"
  fi
  local now_sha
  now_sha=$(unit_hash "$UNIT_PATH")
  if [ "$now_sha" = "$was_sha" ]; then
    fact unit-kept PASS "the login unit's own file is byte-for-byte what it was before the upgrade: replacing the binary under it does not touch the unit, which is why the unit survives every brew upgrade"
  else
    fact unit-kept FAIL "the login unit's file changed while the binary was replaced ($was_sha -> $now_sha): an upgrade of the binary must not rewrite the unit — something else wrote it, and the next 'fugaro update' will compare against whatever is there now"
  fi
  if read_lease lease-ls; then
    if [ -n "$LEASE_HOLDER" ] && [ "$LEASE_HOLDERS" = 1 ]; then
      probe_second_instance
      local named
      named=$(probe_holder)
      if [ -n "$SECOND_REFUSAL" ] && [ "$named" = "$LEASE_HOLDER" ]; then
        fact lease PASS "the new daemon holds the installation's lease exactly once: $LEASE_LOCAL is held by $LEASE_HOLDER (last beat $LEASE_BEAT), and a second instance started by hand was refused naming the same holder"
      else
        fact lease FAIL "the new daemon's lease reads $LEASE_HOLDER and a second instance was not refused naming it ('$(printf '%s' "$SECOND_REFUSAL")'): read 'fugaro orchestrator ls' and the second instance's own output"
      fi
    else
      fact lease FAIL "the listing names $LEASE_HOLDERS holder(s) and this machine's orchestrator '${LEASE_LOCAL:-none}': the daemon that took over from the new binary holds the lease exactly once, and this reading says otherwise"
    fi
  fi
  state_remove
  fact human-next INFO "the last step of this check is T7's half, on this machine or the one the daemon drives: bash scripts/live-checks/check301.sh update"
  say "next: bash scripts/live-checks/check301.sh update"
}

phase_update() {
  state_dir_up
  local half stage
  half=$(state_get half)
  stage=$(state_get stage)
  if state_present && [ "$half" != upgrade ]; then
    fact state FAIL "the state file at $STATE_FILE is a $half/$stage run: this phase must not overwrite a 'start' that has not been followed by 'after-reboot'"
    stop "$STATE_FILE holds a $half/$stage run: finish it ('$PROG after-reboot') or drop it ('$PROG forget'), then run this phase again"
  fi
  if state_present && [ -n "$(state_get machine)" ] && [ "$(state_get machine)" != "$MACHINE" ]; then
    fact state FAIL "the state file at $STATE_FILE was written by $(state_get machine) and this machine is $MACHINE"
    stop "the recorded run is $(state_get machine)'s and this is $MACHINE: drop the state ('$PROG forget') and run this phase here"
  fi

  if [ -t 0 ]; then
    fact terminal PASS "this check's standard input is a real terminal, so the 'fugaro update' it runs reaches the re-render with a person at it — which is the condition that step needs (T7: --yes never covers it, and an agent-marked or terminal-less run writes no unit)"
  else
    fact terminal FAIL "this check's standard input is not a terminal (it is redirected), and 'fugaro update' re-renders an out-of-date unit only where a person is at the terminal: run this phase from your own terminal window, or the boundary row below reads what a terminal-less run writes rather than what a person's run does"
  fi

  read_unit unit-status
  local program hash before_pid state_before
  case "$UNIT_STATE" in
  none)
    fact unit INFO "the login unit is not installed at $UNIT_PATH, so there is nothing for 'fugaro update' to re-render here. Install it ('fugaro orchestrator install', typing the word install) and run this phase again"
    stop "no login unit is installed at $UNIT_PATH: 'fugaro update' never installs one, so this phase has nothing to measure — install it first"
    ;;
  unknown)
    fact unit FAIL "fugaro orchestrator status did not answer a state this check reads: its first line is '$UNIT_NOTE'"
    stop "fugaro orchestrator status did not answer the unit's state: $UNIT_NOTE"
    ;;
  current)
    fact unit PASS "the login unit at $UNIT_PATH is installed and current: its file is this release's own template, so this run reads the quiet half T7 keeps (below) rather than a re-render"
    ;;
  stale)
    fact unit PASS "the login unit at $UNIT_PATH is installed and its file is NOT this release's template ('$UNIT_NOTE'): this is the machine T7's half is about — 'fugaro update' must re-render it and reload the service"
    ;;
  esac
  program=$(unit_program "$UNIT_PATH")
  if [ -z "$program" ]; then
    fact unit FAIL "the login unit at $UNIT_PATH names no program this check can read, so the daemon it starts cannot be told apart from the one running now"
    stop "the login unit at $UNIT_PATH names no program (a launchd plist's ProgramArguments, systemd's ExecStart): read it yourself and run 'fugaro orchestrator install' if it is malformed"
  fi
  hash=$(unit_hash "$UNIT_PATH")
  state_before=$UNIT_STATE
  read_manager manager-before
  before_pid=$MANAGER_PID
  fact before PASS "the readings this phase compares against: the unit's file hashes $hash, it is '$state_before' against this release's template, the daemon is $(manager_words), and the program the unit names is $program ($(binary_version_at "$program"))"

  # The agent-marked run comes first: it is the reading that only exists while
  # something is still there to re-render, and it must not be the run that
  # re-renders it.
  if ! bounded_ok 900 boundary env CLAUDECODE=1 fugaro update --yes; then
    fact boundary FAIL "fugaro update --yes exited $BOUNDED_STATUS with CLAUDECODE=1 set: the agent-marked run did not finish, so what it wrote cannot be read. Its last lines: $(tail -n 5 "$SCRATCH/boundary.out" "$SCRATCH/boundary.err" 2>/dev/null | tr '\n' ' ')"
  else
    local after_sha after_pid
    after_sha=$(unit_hash "$UNIT_PATH")
    read_manager manager-after-boundary
    after_pid=$MANAGER_PID
    if [ "$after_sha" != "$hash" ]; then
      fact boundary FAIL "an agent-marked run of 'fugaro update --yes' rewrote the login unit at $UNIT_PATH ($hash -> $after_sha): a coding agent's session must write no unit, and this is the marker rule internal/agentenv's G2 is"
    elif [ "$after_pid" != "$before_pid" ]; then
      fact boundary FAIL "an agent-marked run of 'fugaro update --yes' reloaded the service (pid $before_pid -> $after_pid) without writing the unit: the step must touch nothing at all in an agent's session"
    elif [ "$UNIT_STATE" = stale ]; then
      fact boundary PASS "an agent-marked run of 'fugaro update --yes' (CLAUDECODE=1, exit 0) wrote no unit and reloaded nothing: the file still carries the previous template, so the re-render is left for the person — which is T7's open item and what doctor reports as a drift until someone runs 'fugaro update' where they can answer"
    else
      fact boundary PASS "an agent-marked run of 'fugaro update --yes' (CLAUDECODE=1, exit 0) wrote no unit and reloaded nothing; the unit was already this release's template here, so this reading cannot tell the marker rule from a unit with nothing to write — the machine for that is one whose template differs"
    fi
  fi

  if ! bounded_ok 3600 update fugaro update --yes; then
    fact update FAIL "fugaro update --yes exited $BOUNDED_STATUS: the one command T7's half is about did not finish. Its last lines: $(tail -n 8 "$SCRATCH/update.out" "$SCRATCH/update.err" 2>/dev/null | tr '\n' ' ')"
    fact re-render FAIL "not measured: the update above did not exit 0"
  else
    local planned=no
    if grep -qF "$UNIT_PATH" "$SCRATCH/update.out" 2>/dev/null; then planned=yes; fi
    fact update PASS "fugaro update --yes exited 0, and its plan $([ "$planned" = yes ] && echo "named the login unit's own path ($UNIT_PATH)" || echo "named no line for the login unit, which is what a run with nothing to re-render prints")"
    local now_sha
    now_sha=$(unit_hash "$UNIT_PATH")
    read_unit unit-status-3
    if [ "$UNIT_STATE" != current ]; then
      fact re-render FAIL "after 'fugaro update --yes' the login unit at $UNIT_PATH is still not this release's template ('$UNIT_NOTE'): one 'fugaro update' converges an installed unit — the HARD RULE's carve-out — and this one did not. Its file still hashes $now_sha"
    elif [ "$hash" = "$now_sha" ] && [ "$state_before" = current ]; then
      read_manager manager-after-current
      if [ "$MANAGER_PID" != "$before_pid" ]; then
        fact re-render FAIL "the login unit's file was already this release's template (hash $hash, unchanged) and 'fugaro update' reloaded the service anyway (pid $before_pid -> $MANAGER_PID): an update with nothing to write reloads nothing — otherwise every update restarts the daemon for no reason"
      else
        fact re-render PASS "fugaro update wrote nothing to the login unit and reloaded nothing, because the file was already this release's template (hash $hash, unchanged; $(manager_words)): this is the quiet half T7 keeps, and it is what a repeated update does every time. It does NOT exercise a re-render — for that reading the template has to have changed since the unit was installed, and this machine's unit was already current"
      fi
    elif [ "$hash" = "$now_sha" ]; then
      fact re-render FAIL "the login unit at $UNIT_PATH was not this release's template before 'fugaro update' ('$state_before') and its file is unchanged ($now_sha): the re-render the unit needed did not happen"
    else
      read_manager manager-after-update
      if [ "$MANAGER_PID" != "$before_pid" ]; then
        fact re-render PASS "fugaro update re-rendered the login unit from this release's template ($hash -> $now_sha) and reloaded the service by itself: $(manager_words), the pid moved from $before_pid to $MANAGER_PID — one command, and nothing else typed"
      else
        fact re-render FAIL "fugaro update re-rendered the login unit ($hash -> $now_sha) and the daemon is still pid $before_pid: the file is written and the manager was not reloaded, so the daemon running is still the one the old file started"
      fi
    fi
  fi

  local quiet_before_sha quiet_before_pid quiet_sha
  quiet_before_sha=$(unit_hash "$UNIT_PATH")
  read_manager manager-before-quiet
  quiet_before_pid=$MANAGER_PID
  if ! bounded_ok 3600 quiet fugaro update --yes; then
    fact quiet FAIL "the second 'fugaro update --yes' exited $BOUNDED_STATUS: a repeated update must finish and must write nothing. Its last lines: $(tail -n 5 "$SCRATCH/quiet.out" "$SCRATCH/quiet.err" 2>/dev/null | tr '\n' ' ')"
  else
    quiet_sha=$(unit_hash "$UNIT_PATH")
    read_manager manager-after-quiet
    if [ "$quiet_sha" = "$quiet_before_sha" ] && [ "$MANAGER_PID" = "$quiet_before_pid" ]; then
      fact quiet PASS "the second 'fugaro update --yes' (exit 0) wrote nothing to the login unit and reloaded nothing: its file still hashes $quiet_sha and the daemon is still pid $quiet_before_pid — a repeated update is quiet"
    else
      fact quiet FAIL "the second 'fugaro update --yes' did not leave the unit alone (hash $quiet_before_sha -> $quiet_sha, pid $quiet_before_pid -> $MANAGER_PID): a repeated update must write nothing and reload nothing"
    fi
  fi

  if read_lease lease-ls; then
    if [ -n "$LEASE_HOLDER" ] && [ "$LEASE_HOLDERS" = 1 ]; then
      probe_second_instance
      local named
      named=$(probe_holder)
      if [ -n "$SECOND_REFUSAL" ] && [ "$named" = "$LEASE_HOLDER" ]; then
        fact lease PASS "one daemon still holds the installation's lease: $LEASE_LOCAL is held by $LEASE_HOLDER (last beat $LEASE_BEAT), and a second instance started by hand was refused naming it"
      else
        fact lease FAIL "after the updates the lease reads $LEASE_HOLDER and a second instance was not refused naming it ('$(printf '%s' "$SECOND_REFUSAL")'): read 'fugaro orchestrator ls' and the second instance's own output"
      fi
    else
      fact lease FAIL "the listing names $LEASE_HOLDERS holder(s) and this machine's orchestrator '${LEASE_LOCAL:-none}': one daemon holding the id is what T7's reloads must leave behind"
    fi
  fi

  state_remove
  say "the T7 half is done: read the block below and paste it back"
}

phase_forget() {
  state_dir_up
  if ! state_present; then
    fact state PASS "there is no state file at $STATE_FILE: this check has left nothing behind on this machine"
  else
    fact state PASS "the state file at $STATE_FILE ($(state_get half)/$(state_get stage), written by $(state_get machine)) is removed, and with it this check's own directory — the login unit, the daemon and the state node are not touched"
  fi
  rm -f "$STATE_FILE" "$STATE_DIR/$STATE_MARKER" 2>/dev/null || true
  rmdir "$STATE_DIR" 2>/dev/null || true
  if [ -e "$STATE_DIR" ]; then
    fact state FAIL "$STATE_DIR is still there: this check refuses to remove a directory that is not its own, so read what is in it and remove it yourself"
  fi
}

# ------------------------------------------------------------ the fixed files
write_files() {
  # ls_reader.py: `fugaro orchestrator ls --json` as the four answers this check
  # asks, and never the document itself.
  cat >"$SCRATCH/ls_reader.py" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(1)
if not isinstance(doc, dict) or "orchestrators" not in doc:
    sys.exit(1)
entries = doc.get("orchestrators") or []
local_id = local_holder = local_beat = ""
holders = 0
for e in entries:
    if not isinstance(e, dict):
        sys.exit(1)
    holder = e.get("lease_holder") or ""
    if holder:
        holders += 1
    if e.get("kind") == "local":
        local_id = e.get("project") or ""
        local_holder = holder
        local_beat = e.get("updated_at") or ""
print("local=%s" % local_id)
print("local_holder=%s" % local_holder)
print("local_beat=%s" % local_beat)
print("holders=%d" % holders)
PY
}

# ------------------------------------------------------------------- the block
print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 301 RESULT\n' >&3
  for key in $(block_keys); do
    local line
    # The LAST fact under a key, not the first: a phase that reads a thing twice
    # records the later reading, and a row that passed and then failed must read
    # as the failure.
    line=$(printf '%s\n' "$facts" | grep "^$key|" | tail -1 || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: %s\n' "$key" "$NOT_REACHED_TEXT" >&3
    fi
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
    printed="$printed"$'\n'"$key"
  done
  printf 'CHECK 301 RESULT END (exit %s)\n' "$status" >&3
}

# block_keys is each phase's rows in the order they are read. A row that a phase
# cannot reach reads 'not reached' rather than being dropped, so a block is the
# whole of what that phase could have said.
block_keys() {
  local list=""
  case "$PHASE" in
  start) list="phase platform installation agent release xtrace unit daemon state human-install human-reboot restore" ;;
  after-reboot) list="phase platform installation release unit boot daemon lease second-refuses state human-next restore" ;;
  upgrade) list="phase platform installation release unit daemon never-installs version handover unit-kept lease state human-install human-upgrade human-next restore" ;;
  update) list="phase platform installation agent release xtrace terminal unit before boundary update re-render quiet lease state restore" ;;
  forget) list="phase platform installation state restore" ;;
  esac
  if [ "$DRY_RUN" = yes ]; then list="$list dry-run"; fi
  local key
  for key in $list; do printf '%s\n' "$key"; done
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

main() {
  guard_platform
  need_tools
  state_paths
  parse_args "$@"

  scratch_up
  write_files
  # After scratch_up, not before: a guard that stops the run records the fact
  # that says why, and a fact written before the facts file exists is dropped.
  guard_xtrace
  guard_agent
  guard_release
  guard_owner
  fact phase PASS "$PHASE on $MACHINE: this machine is $PLATFORM, and the login unit's manager here is $MANAGER"
  fact platform PASS "this check picked $MANAGER itself from the machine it is running on (uname -s answered $PLATFORM), and it reads and writes only the person's own login level ($([ "$MANAGER" = launchd ] && echo "launchd's gui/$(id -u)" || echo 'systemd --user'))"
  if [ "$PHASE" = forget ]; then
    fact installation INFO "forget removes only this check's own state file under $STATE_DIR, so which installation is in force is not part of it"
  else
    local project=""
    project=$(gcp_project_of "$INSTALLATION")
    fact installation PASS "the installation in force is $INSTALLATION${project:+ on GCP project $project}, and it is not belong"
  fi

  if [ "$DRY_RUN" = yes ]; then
    # The dry run stops here: the guards and the reads above are the part of a
    # run that cannot change anything, and everything below installs, loads,
    # starts, upgrades or writes a state file. A phase's own state directory is
    # not even created here, so a dry run of a phase that would write one leaves
    # the machine exactly as it found it.
    NOT_REACHED_TEXT="INFO — not measured (--dry-run: nothing live was run, so nothing below it is a verdict)"
    print_plan
    fact dry-run PASS "guards and the machine's own reads only: $PHASE was planned, not run — nothing was installed, loaded, started, upgraded, rewritten or recorded, and no state file or state directory was created. Exit 0 here means the guards and the reads passed, not that any claim of this phase is verified"
    REACHED_END=yes
    trap - ERR
    return 0
  fi

  case "$PHASE" in
  start) phase_start ;;
  after-reboot) phase_after_reboot ;;
  upgrade) phase_upgrade ;;
  update) phase_update ;;
  forget) phase_forget ;;
  esac

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
