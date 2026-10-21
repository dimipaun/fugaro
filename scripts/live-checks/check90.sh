#!/usr/bin/env bash
# Check 90 — the OAuth header swap, as ONE script you run.
#
# It answers one question: does Claude Code's subscription traffic survive a
# proxy that strips the incoming credential and attaches your real one? If it
# does not, the credentials service's TIER A tasks are dropped (the plan's
# Global Constraint 4). The register's section is
# docs/gcp-live-checklist.md, "Check 90"; the step-by-step text it used to
# carry is docs/credentials-service/check90-steps.md.
#
# Usage:      bash scripts/live-checks/check90.sh
# Needs:      bash, claude, python3. Nothing is installed, nothing is fetched,
#             no cloud project is touched, and no Fugaro installation is
#             involved. If one of those is missing the script stops with one
#             line naming it.
# Costs:      a handful of tiny prompts — a control, a swap, one long-ish
#             prompt, one prompt per optional header (at most 8) and one on a
#             delayed proxy. All of them on YOUR subscription; there is no
#             API-key spend, and step 9's wait is the only slow part (default
#             90s, CHECK90_DELAY).
# Duration:   about two minutes, more if the client waits longer than the
#             delayed proxy does.
# You type:   the value `claude setup-token` printed, ONCE, at a hidden prompt —
#             the token and nothing else. This check never asks you to paste any
#             other line of that command's output: that text sits next to the
#             token, so a prompt for it would invite a paste that puts the token
#             in the RESULT block and in your shell history (the independent
#             review's B1). NO AGENT EVER SEES THE TOKEN: the script is run by
#             you, the prompt does not echo, the value is never on a command
#             line, and it reaches nothing but the 0600 file the proxy reads and
#             the one `claude` child that needs it. That child is started in the
#             scratch directory with hooks disabled and MCP servers off, so no
#             hook, plugin hook or MCP server is started to inherit it — see the
#             note on CRED_CHILD for what still can. Nothing the script prints —
#             the RESULT block above all — can carry a token or a header value:
#             it prints header NAMES and statuses only, and a test enforces that.
# Cleanup:    always, including on failure and on Ctrl-C: the proxy is killed,
#             the token file is removed, the scratch directory goes. The trap
#             re-checks its own marker file before rm -rf, as the section's
#             step 9 did.
# Output:     progress on stderr, ONE block on stdout headed CHECK 90 RESULT,
#             to copy back to the coordinator. Exit 0 when tier A is viable,
#             1 when it is not, 2 when the run could not tell (a control that
#             failed says nothing about the swap). 1 is a VERDICT and only the
#             swap's own FAIL produces it; every guard and hard stop — a missing
#             tool, a routing variable, a settings file, a proxy that never
#             listened, a token that was not typed — exits 2, because a run that
#             stopped before its call learned nothing about the swap.
#
# CHECK90_UPSTREAM, CHECK90_DELAY, CHECK90_CALL_TIMEOUT, CHECK90_TOTAL_SECONDS,
# CHECK90_MAX_DROPS, CHECK90_MANAGED_SETTINGS_DIR and
# CHECK90_MANAGED_SETTINGS_DIRS exist for this script's own test:
# CHECK90_UPSTREAM may only name a loopback address (the test's fake upstream),
# so no environment can send the token anywhere but Anthropic's endpoint, and
# the two managed-settings names directories the guard still READS, never ones
# it skips. Setting either REPLACES the two directories the guard looks at on a
# real machine, so a test never reads this machine's own managed settings (a
# runner here has one, and it would decide the run instead of the test).
# -E (errtrace) is what makes the ERR trap below fire for a failure INSIDE a
# function or a command substitution too, which is where an internal error is
# most likely to happen; without it the trap only sees top-level failures.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had. `bash -x check90.sh`
# (or SHELLOPTS=xtrace exported) prints every command to stderr as it runs, and
# this script's commands carry the token: `[ -n "$token" ]` and the printf that
# writes the token file would both be traced in full, and `bash -x` is exactly
# what someone reaches for when a run failed and they want a log to paste.
#
# Three things, because one is not enough. The trace is turned OFF here, and
# again around the token itself in ask_token, so a trace switched on later
# cannot catch the read either. BASH_XTRACEFD is unset, so a trace redirected to
# a file this script knows nothing about cannot be reopened. And guard_xtrace
# REFUSES the run when this shell arrived with tracing on -- that is the case
# where the flag sits in SHELLOPTS/BASHOPTS in the environment, and every child
# shell, the credential child included, would start tracing on its own.
XTRACE_AT_START=no
# Each source on its own: BASHOPTS carries names like extquote, and a single
# pattern over the three concatenated would match that 'x' and refuse every run.
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4. A trap runs with whatever
# redirection is in effect at the moment the signal arrives -- and this script
# redirects a call's output for the whole of that call -- so the RESULT block
# and the messages around it are written to the SAVED descriptors, never to
# whatever fd 1 happens to be pointing at when Ctrl-C lands.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
#
# It exits 2, not 1: 1 is the block's VERDICT — tier A is not viable — and only
# the swap call's own FAIL reaches `tier_a` with it. Every stop in this script is
# the other thing: the run did not get to its measurement (a guard refused it, a
# tool is missing, the proxy never listened, no token was typed, the control
# failed), so the answer is "could not tell", which is the block's INCONCLUSIVE
# route and the code the header and the register both document. A stop that
# exited 1 would read as a swap that failed, which is a claim this script has no
# business making on evidence it does not have.
stop() {
  fact "aborted" FAIL "$1"
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() {                       # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# The run's own total budget. It is read before EVERY step and before every
# header-drop call, not once before the last three (the review's m3): one slow
# step (a drop on a client that retries, the delayed proxy) would otherwise run
# past the limit the script advertises and keep the owner waiting, or spend
# prompts after the budget is gone.
DEADLINE=0
past_deadline() { [ "$DEADLINE" -gt 0 ] && [ "$(date +%s)" -ge "$DEADLINE" ]; }
budget_exceeded() { # budget_exceeded WHAT — one fact, however often it is asked
  case "$(grep -c '^budget|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact "budget" FAIL "the run used its whole ${TOTAL_SECONDS}s budget before $1, so $1 did not run (raise CHECK90_TOTAL_SECONDS for a slower client)" ;;
  esac
  say "$1 skipped: the run used its whole ${TOTAL_SECONDS}s budget"
}

# on_error: an INTERNAL error is a fact, never a verdict (the review's M3).
# Under `set -e` the first command that fails outside a guard ended the run with
# no fact at all, and the block then read as a swap that was not viable --
# exit 1 and "NOT VIABLE" for what is really "this run could not tell". The ERR
# trap records which line it happened on (a line number carries no value) and
# exits 2, which is the block's INCONCLUSIVE route.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  # Once: with -E the trap also runs in a subshell, and the failure that ends
  # the run then reaches this function twice (once there, once at the top).
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact "aborted" FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the swap (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line); this says nothing about the swap"
  exit 2
}

DRY_RUN=${CHECK90_DRY_RUN:-}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 90 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 90 needs bash"
  need mktemp
  need claude "Claude Code, the client this check measures"
  need python3 "the throwaway proxy is one python3 file, written into the scratch directory"
  # Only so the message is one line when it is missing, not two.
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk90-mine"
PROXY_PID=""
TOKEN_FILE=""
# The 0600 file the run's placeholder lives in, handed to the proxy by NAME so
# the value is never in that process's argv (see start_proxy and the proxy's
# own docstring).
PLACEHOLDER_FILE=""
SCRATCH_REMOVED=no
TOKEN_REMOVED=no
PRINTED=""
# Set by finish before cleanup runs: a second Ctrl-C while the token file is
# being wiped and the scratch directory removed must not kill this shell (see
# on_signal).
CLEANUP_RUNNING=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk90.XXXXXXXX") || die "Check 90 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  TOKEN_FILE="$SCRATCH/token"
  PLACEHOLDER_FILE="$SCRATCH/placeholder"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 90 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  stop_call
  stop_proxy
  # Guarded, and its outcome RECORDED rather than assumed: this runs from the
  # exit trap, where a `rm` that fails (a read-only parent, a file this user
  # cannot unlink) would end the shell under `set -e` before `print_block` --
  # the run would emit NO `CHECK 90 RESULT` block at all and exit 1, which reads
  # as "tier A is not viable" for a run that may have measured nothing. A token
  # file that is still there is a FACT, and the `restore` row names it: it holds
  # the setup-token value, so "the token file is removed" must never be printed
  # over one that survived.
  rm -f "$TOKEN_FILE" 2>/dev/null || true
  if [ ! -e "$TOKEN_FILE" ]; then TOKEN_REMOVED=yes; fi
  # The placeholder file goes with it, and for the same reason this line is not
  # left to the scratch directory's removal: the directory is KEPT when a count
  # is not 0, and a per-run secret has no business surviving a run in it.
  rm -f "$PLACEHOLDER_FILE" 2>/dev/null || true
  keep_logs
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

# keep_logs copies the proxy logs — and nothing else — where the owner asked
# for them, so a failed run can be read after the scratch directory is gone.
# The token file is never among them: this is a copy of the .log files only.
keep_logs() {
  [ -n "${CHECK90_KEEP_LOGS:-}" ] || return 0
  [ -d "$SCRATCH" ] || return 0
  if (cd "$CHECK90_KEEP_LOGS" 2>/dev/null && git rev-parse --show-toplevel >/dev/null 2>&1); then
    say "not copying logs into $CHECK90_KEEP_LOGS: it is inside a git work tree"
    return 0
  fi
  mkdir -p "$CHECK90_KEEP_LOGS" 2>/dev/null || return 0
  cp "$SCRATCH"/proxy-*.log "$CHECK90_KEEP_LOGS"/ 2>/dev/null || true
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    # with_timeout puts every call in its own process group, so the signal
    # reaches the whole call -- the streaming one is a pipeline, and its reader
    # would otherwise outlive the client. The single-PID fallback is for a shell
    # where job control could not be turned on: there the call is one process.
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

remove_scratch() {
  case "$SCRATCH" in
  "" | / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  # Guarded for the reason cleanup's token removal is: this runs from the exit
  # trap, where a failing `rm` under `set -e` would end the shell before
  # `print_block`, losing the whole block. A directory that is still there is a
  # FACT, and the `restore` row says so — it is not a reason to lose the block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

stop_proxy() {
  if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  PROXY_PID=""
}

finish() {
  local status=$?
  # INT, TERM and HUP are NOT disarmed here, and that is the point: cleanup is
  # about to wipe the token file and remove the scratch directory, and a second
  # Ctrl-C during it would kill this shell with the token file still on disk —
  # in a directory the run's own record then says was removed. So the signals
  # stay trapped for the whole of cleanup and `on_signal` answers with one line
  # instead of ending the run (the first Ctrl-C already came through it and
  # exited 130). The ERR trap goes: from here on, a failing command is cleanup
  # bookkeeping, which is guarded at each step, not an internal error to record.
  trap - EXIT ERR
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if ! printf '%s' "$facts" | grep -q '^tier-a|' && [ "$status" = 2 ]; then
    # A run that reached the calls and could not judge the swap says so here,
    # rather than in a "not reached" line that reads like a step nobody ran.
    facts="${facts}"$'\n'"tier-a|INFO|INCONCLUSIVE — the run reached the calls and could not judge the swap; read the FAILed fact above"
  fi
  # The two removals are reported as they happened, never as they were meant to:
  # a token file or a directory that is still there is a FAILed row, with what
  # to remove and where. The exit status is NOT changed by this row: this
  # check's contract is 0 = tier A viable, 1 = it is not, 2 = the run could not
  # tell, and a cleanup failure is none of those (a FAILed `claude-version` row
  # can sit beside exit 0 for the same reason). What the guard above buys is
  # that the block is always printed at all.
  local restore=PASS
  [ "$SCRATCH_REMOVED" = yes ] && [ "$TOKEN_REMOVED" = yes ] || restore=FAIL
  local token_note="the token file is removed"
  [ "$TOKEN_REMOVED" = yes ] || token_note="the TOKEN FILE IS STILL THERE at $TOKEN_FILE (mode 600, holding the setup-token value) — remove it yourself"
  facts="${facts}"$'\n'"restore|$restore|the proxy is stopped, ${token_note}, and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
# Every routing variable the settings guard reads for, plus every
# CLAUDE_CODE_USE_* switch: a variable that would route the call or its
# credential elsewhere stops the run from the ENVIRONMENT exactly as it stops it
# from a settings file. This list and settingskeys.py's WANT are the same set --
# one of them covering less than the other was the review's m2.
GUARD_VARS=(ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL
  ANTHROPIC_VERTEX_BASE_URL ANTHROPIC_BEDROCK_BASE_URL CLAUDE_CODE_SKIP_VERTEX_AUTH
  CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_BEDROCK CLAUDE_CODE_USE_FOUNDRY
  HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY)

guard_env() {
  local set_vars=() v
  for v in "${GUARD_VARS[@]}"; do
    if printenv "$v" >/dev/null 2>&1; then set_vars+=("$v"); fi
  done
  # Any other switch of the same family, so a name this list does not know (a
  # newer CLAUDE_CODE_USE_...) is still refused rather than silently kept.
  while IFS= read -r v; do
    case "$v" in "") continue ;; esac
    case " ${set_vars[*]-} " in *" $v "*) continue ;; esac
    set_vars+=("$v")
  done < <(printenv | sed -n 's/^\(CLAUDE_CODE_USE_[A-Za-z0-9_]*\)=.*/\1/p' || true)
  if [ ${#set_vars[@]} -gt 0 ]; then
    fact "env-cleared" FAIL "set in your shell: ${set_vars[*]} — unset them and run again"
    stop "these are set in your shell: ${set_vars[*]} (their values are not printed, and none is read). Each one makes 'claude' answer with something other than the setup-token, so the control would measure the wrong credential. Run: unset ${set_vars[*]}"
  fi
  fact "env-cleared" PASS "none of ${GUARD_VARS[*]} is set in this shell"
}

# guard_xtrace: refuse to run when this shell arrived with tracing on. The trace
# itself is off from the first line (see the top of this file), but with -x the
# flag is also in SHELLOPTS/BASHOPTS in the environment, and a child shell
# inherits those: the credential child would start tracing its own commands, and
# its output is captured into a file this check reads. Refusing is the only
# answer that holds for the children too.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact "xtrace" FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs — and the token's own commands among them — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check90.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact "xtrace" PASS "this shell is not tracing, so no trace of this run can carry the token"
}

# The same names, removed from the child's environment: a variable the guard
# refused in this shell must not be able to reach the call either. ANTHROPIC_LOG
# and DEBUG are removed for a different reason and are NOT refused in this
# shell: neither routes the call nor its credential, so their presence is the
# owner's business — but both change what the client prints (ANTHROPIC_LOG even
# writes request bodies to a file of its choosing), and this child's output is
# captured into a file this check reads and pastes back. A debug log of a call
# carrying CLAUDE_CODE_OAUTH_TOKEN is exactly the surface this check exists to
# keep the token off.
ENV_UNSET=()
for v in "${GUARD_VARS[@]}" ANTHROPIC_LOG DEBUG; do ENV_UNSET+=(-u "$v"); done

# settings_keys FILE: the routing key NAMES a settings file sets, nothing else.
# Non-zero when the file is not settings JSON this check can read.
settings_keys() {
  python3 "$SCRATCH/settingskeys.py" "$1"
}

guard_settings() {
  local files=() f setkeys
  for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
    [ -f "$f" ] && files+=("$f")
  done
  if [ -n "${CLAUDE_CONFIG_DIR:-}" ] && [ -f "$CLAUDE_CONFIG_DIR/settings.json" ]; then
    files+=("$CLAUDE_CONFIG_DIR/settings.json")
  fi
  # The directories Claude Code keeps its managed settings in, one level deep,
  # BOTH of them: the Linux one the runner itself writes
  # (`internal/agent/settings.go`'s ManagedSettingsPath) and the one the client
  # reads on macOS. Reading only the Linux path left the guard blind on a Mac —
  # the one machine this check is most likely to be run from — so a managed
  # settings file there could route the call with the FACT saying no managed
  # settings were read at all. CHECK90_MANAGED_SETTINGS_DIR (one directory) and
  # CHECK90_MANAGED_SETTINGS_DIRS (a colon-separated list) exist so this script's
  # own test is hermetic on a machine that has a real one; nothing else should
  # set them. Either REPLACES the two defaults below — a value names directories
  # that are then READ, never ones that are skipped, and when neither is set this
  # reads both of the real locations.
  local dirs=() managed
  if [ -n "${CHECK90_MANAGED_SETTINGS_DIRS:-}" ]; then
    IFS=: read -r -a dirs <<<"$CHECK90_MANAGED_SETTINGS_DIRS"
  elif [ -n "${CHECK90_MANAGED_SETTINGS_DIR:-}" ]; then
    dirs=("$CHECK90_MANAGED_SETTINGS_DIR")
  else
    dirs=(/etc/claude-code "/Library/Application Support/ClaudeCode")
  fi
  local read_dirs=() absent_dirs=() f
  for managed in ${dirs[@]+"${dirs[@]}"}; do
    if [ -d "$managed" ]; then
      while IFS= read -r f; do files+=("$f"); done < <(find "$managed" -maxdepth 1 -type f 2>/dev/null)
      read_dirs+=("$managed")
    else
      absent_dirs+=("$managed")
    fi
  done
  if [ ${#read_dirs[@]} -gt 0 ]; then
    local detail="read: ${read_dirs[*]}"
    if [ ${#absent_dirs[@]} -gt 0 ]; then detail="${detail} — not on this machine: ${absent_dirs[*]}"; fi
    fact "managed-settings-dir" PASS "$detail"
  else
    fact "managed-settings-dir" PASS \
      "none of ${dirs[*]} is on this machine, so no managed settings file was read — if Claude Code keeps one somewhere else here, this check did NOT look at it"
  fi
  local found=()
  for f in ${files[@]+"${files[@]}"}; do
    if ! setkeys=$(settings_keys "$f"); then
      found+=("$f (not settings JSON this check can read)")
      continue
    fi
    if [ -n "$setkeys" ]; then found+=("$f sets $setkeys"); fi
  done
  if [ ${#found[@]} -gt 0 ]; then
    fact "settings-routing" FAIL "${found[*]}"
    stop "a settings file would route 'claude' around this check: ${found[*]} — only key names are printed, never a value. Remove those keys (or run the check where they are not set) and run again"
  fi
  fact "settings-routing" PASS \
    "no settings file this check reads sets apiKeyHelper or an env key that would route the call or its credential elsewhere (${#files[@]} file(s) read)"
}

# ------------------------------------------------------------------ the token
# drain_input: throw away anything still queued on the terminal. A paste can
# carry more than the one line this check asks for (setup-token prints a banner
# line and the token, and a terminal in bracketed-paste mode hands over both),
# and whatever is left in the tty's input queue is read as a COMMAND by the
# owner's own shell the moment this script exits — and saved to its history.
# Only a terminal is drained: with stdin a pipe or a file there is no owner
# sitting at it, and a read would block on the end of the input instead.
drain_input() {
  [ -t 0 ] || return 0
  local line n=0
  while [ "$n" -lt 100 ] && IFS= read -r -t 1 line 2>/dev/null; do n=$((n + 1)); done
  return 0
}

ask_token() {
  say ""
  say "Paste the value from 'claude setup-token' below — the TOKEN and nothing else."
  say "It is not echoed, not put on a command line and not exported to anything but"
  say "the one 'claude' child that needs it. This check asks for no other line of"
  say "that command's output: the line beside the token is the token, and a prompt"
  say "for it would put the token in the RESULT block and in your history."
  printf '%s: token (hidden, then Enter): ' "$PROG" >&2
  local token=""
  # Around the token itself, the trace is off whatever happened earlier: the
  # lines below are the ones a trace would print in full (the review's M2).
  { set +x; } 2>/dev/null
  IFS= read -rs token || true
  printf '\n' >&2
  drain_input
  [ -n "$token" ] || stop "no token was typed, so there is nothing to swap in"
  (umask 077 && printf '%s' "$token" >"$TOKEN_FILE")
  chmod 600 "$TOKEN_FILE"
  unset token
  local mode
  mode=$(python3 -c 'import os,sys; print(oct(os.stat(sys.argv[1]).st_mode & 0o777))' "$TOKEN_FILE")
  # The length of the token is a hint about the value, so it is not printed: the
  # mode and the path are what this fact is for (the review's m5).
  fact "token-file" PASS "written to the scratch directory as the only file that holds it; mode ${mode}; the value and its length are not printed anywhere"
}

# ------------------------------------------------------------------ the proxy
write_files() {
  cat >"$SCRATCH/proxy.py" <<'PROXY_EOF'
#!/usr/bin/env python3
"""Check 90's throwaway loopback proxy.

It does one thing to a request: it deletes the client's Authorization and
x-api-key and attaches the token read from --token-file -- unless --no-swap is
given, in which case it changes no header and attaches nothing, so the
credential the client sent is the one the API sees (that is the observed
control). Everything else is forwarded as it arrived, the response is streamed
back as it arrives, and the only things printed are header NAMES, statuses and
timings.

Two requests are refused with 403 and never forwarded, because loopback is
reachable from any page a browser loads. One whose Host is not this listener.
And, when --require-credential-file names one (every swapped phase): one whose
Authorization is not the per-run placeholder the check generated, so a request
this run did not make can never have the real token attached to it -- a
cross-origin "simple" POST carries no such header. --no-swap is exempt: there
the client's own credential, the real one, is exactly what has to pass through.

The placeholder comes from a FILE, never from this proxy's own command line:
`ps` shows a process's argv to every user on the machine, and this proxy lives
for the whole of a phase -- minutes, on the delayed one. It is a per-run secret
that any process on this machine could replay at the listener, so it is read
from the 0600 file the check wrote it into, and the file goes with the scratch
directory.
"""
import argparse, http.client, http.server, json, time

ARGS = None
TOKEN = None
HOP = {"authorization", "x-api-key", "host", "content-length", "connection",
       "transfer-encoding", "accept-encoding", "expect", "proxy-connection",
       "keep-alive", "te", "upgrade"}
DROP = set()


def fact(*parts):
    print("FACT", *parts, flush=True)


def read_capped(resp, limit=65536):
    out = b""
    while len(out) < limit:
        chunk = resp.read1(min(65536, limit - len(out)))
        if not chunk:
            break
        out += chunk
    return out


def loopback_host(host, port):
    return (host or "").strip().lower() in {
        "127.0.0.1:%d" % port, "localhost:%d" % port, "[::1]:%d" % port}


def credential_headers(headers):
    return [n for n in ("authorization", "x-api-key") if headers.get(n)]


def forward_names(headers, no_swap):
    names = [n for n in sorted({k.lower() for k in headers.keys()})
             if n not in HOP and n not in DROP]
    if no_swap:
        names += [n for n in ("authorization", "x-api-key") if headers.get(n)]
    return names


def error_fields(body):
    """The only two keys of a body that are ever read: the error object's own
    `type` and `message`. Nothing else is indexed, so nothing else can leak."""
    try:
        parsed = json.loads(body.decode("utf-8", "replace"))
    except Exception:
        return ("(unreadable body: not JSON)",)
    err = parsed.get("error") if isinstance(parsed, dict) else None
    if not isinstance(err, dict):
        return ("(no error object in the body)",)
    t, m = err.get("type"), err.get("message")
    return ("type: " + t if isinstance(t, str) else "type: (none)",
            "message: " + m.replace("\n", " ") if isinstance(m, str)
            else "message: (none)")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "chk90proxy"

    def log_message(self, *a):
        pass

    def read_body(self):
        if "chunked" in (self.headers.get("Transfer-Encoding") or "").lower():
            out = []
            while True:
                n = int(self.rfile.readline().split(b";")[0], 16)
                if n == 0:
                    self.rfile.readline()
                    return b"".join(out)
                out.append(self.rfile.read(n))
                self.rfile.readline()
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def forward(self):
        started = time.time()
        if not loopback_host(self.headers.get("Host"), ARGS.port):
            fact("refused", "host-not-loopback", "the request was not forwarded")
            self.send_error(403, "chk90proxy: Host must be this loopback address and port")
            return
        if not self.path.startswith("/"):
            fact("refused", "path-not-absolute", "the request was not forwarded")
            self.send_error(403, "chk90proxy: the request target must be a path")
            return
        if ARGS.require_credential and not ARGS.no_swap:
            sent = self.headers.get("Authorization") or ""
            if sent != "Bearer " + ARGS.require_credential:
                fact("refused", "not-this-run", "the request was not forwarded")
                self.send_error(403, "chk90proxy: this listener takes requests from its own run only")
                return
        body = self.read_body()
        fact("credential-header",
             ",".join(credential_headers(self.headers)) or "(neither)")
        sent = forward_names(self.headers, ARGS.no_swap)
        fact("request", self.command, self.path, "headers:",
             ",".join(sent) or "(none)")
        if ARGS.delay:
            fact("delaying", ARGS.delay, "seconds before answering")
            time.sleep(ARGS.delay)
        cls = http.client.HTTPSConnection if ARGS.upstream_scheme == "https" else http.client.HTTPConnection
        conn = cls(ARGS.upstream, timeout=ARGS.timeout)
        try:
            conn.putrequest(self.command, self.path, skip_host=True,
                            skip_accept_encoding=True)
            conn.putheader("Host", ARGS.upstream)
            if TOKEN is not None:
                conn.putheader("Authorization", "Bearer " + TOKEN)
            for n in sent:
                conn.putheader(n, self.headers[n])
            conn.putheader("Content-Length", str(len(body)))
            conn.endheaders(body)
            resp = conn.getresponse()
        except Exception as e:
            fact("upstream-error", type(e).__name__, "after", "%.1fs" % (time.time() - started))
            self.send_error(502, "chk90proxy: upstream error")
            return
        fact("response", resp.status, resp.reason, "headers:",
             ",".join(n.lower() for n, _ in resp.getheaders()))
        head = b""
        if ARGS.error_fields and resp.status >= 400:
            head = read_capped(resp)
            fact("error-fields", *error_fields(head))
        self.send_response(resp.status)
        for n, v in resp.getheaders():
            if n.lower() in ("content-length", "transfer-encoding",
                             "connection", "keep-alive"):
                continue
            self.send_header(n, v)
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        try:
            if head:
                self.wfile.write(b"%x\r\n" % len(head) + head + b"\r\n")
                self.wfile.flush()
            while True:
                chunk = resp.read1(65536)
                if not chunk:
                    break
                self.wfile.write(b"%x\r\n" % len(chunk) + chunk + b"\r\n")
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            fact("client-gone", "the client closed before the response ended")
        conn.close()

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = forward


def main():
    global ARGS, TOKEN, DROP
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8787)
    ap.add_argument("--token-file", help="the token attached in place of the client's own")
    ap.add_argument("--no-swap", action="store_true")
    ap.add_argument("--require-credential-file", default="",
                    help="a file holding the placeholder: refuse a request whose"
                         " Authorization is not it (argv is world-readable in ps)")
    ap.add_argument("--upstream", default="api.anthropic.com")
    ap.add_argument("--upstream-scheme", default="https", choices=("https", "http"))
    ap.add_argument("--delay", type=float, default=0.0)
    ap.add_argument("--drop", action="append", default=[])
    ap.add_argument("--timeout", type=float, default=3600.0)
    ap.add_argument("--error-fields", action="store_true")
    ARGS = ap.parse_args()
    if not ARGS.no_swap and not ARGS.token_file:
        ap.error("--token-file is required unless --no-swap is given")
    # The placeholder is read here, once, from the 0600 file: it never appears
    # on this proxy's command line, where every user of the machine could read
    # it out of `ps` for as long as the proxy lives.
    ARGS.require_credential = ""
    if ARGS.require_credential_file:
        try:
            with open(ARGS.require_credential_file) as fh:
                ARGS.require_credential = fh.read().strip()
        except OSError as exc:
            ap.error("--require-credential-file %s: %s" % (ARGS.require_credential_file, exc))
    if not ARGS.no_swap:
        TOKEN = open(ARGS.token_file, "rb").read().strip().decode()
    DROP = {d.lower() for d in ARGS.drop}
    server = http.server.ThreadingHTTPServer(("127.0.0.1", ARGS.port), Handler)
    print("chk90proxy listening on 127.0.0.1:%d, upstream %s" % (ARGS.port, ARGS.upstream),
          flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
PROXY_EOF
  cat >"$SCRATCH/settingskeys.py" <<'KEYS_EOF'
#!/usr/bin/env python3
"""The routing key NAMES a Claude Code settings file sets. Never a value."""
import json, sys

WANT = {"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL",
        "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_SKIP_VERTEX_AUTH",
        "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

with open(sys.argv[1]) as fh:
    doc = json.load(fh)
if not isinstance(doc, dict):
    sys.exit(1)
keys = []
for k in doc:
    if k.lower() == "apikeyhelper":
        keys.append("apiKeyHelper")
env = doc.get("env") or {}
if isinstance(env, dict):
    for k in env:
        upper = k.upper()
        if upper in WANT or upper.startswith("CLAUDE_CODE_USE_"):
            keys.append(upper)
print(" ".join(sorted(set(keys))))
KEYS_EOF
}

start_proxy() { # start_proxy LOGFILE [extra args...]
  local log=$1
  shift
  stop_proxy
  # --require-credential-file is passed on EVERY phase; the proxy ignores it
  # under --no-swap, where the client's own (real) credential is what must
  # pass. The FILE is named, never the placeholder: this proxy's argv is
  # readable by every user of the machine for as long as the phase lasts.
  python3 "$SCRATCH/proxy.py" --port "$PORT" --upstream "$UPSTREAM" \
    --upstream-scheme "$UPSTREAM_SCHEME" --require-credential-file "$PLACEHOLDER_FILE" "$@" >"$log" 2>&1 &
  PROXY_PID=$!
  local i=0
  while [ "$i" -lt 50 ]; do
    if grep -q 'listening on' "$log" 2>/dev/null; then return 0; fi
    if ! kill -0 "$PROXY_PID" 2>/dev/null; then
      stop "the proxy did not start: $(tail -n 3 "$log" | tr '\n' ' ')"
    fi
    sleep 0.1
    i=$((i + 1))
  done
  stop "the proxy did not report that it is listening within 5s"
}

# --------------------------------------------------------------- calling claude
with_timeout() { # with_timeout SECONDS cmd...
  local secs=$1
  shift
  # Job control, so the call gets a process group of its own: without it the
  # deadline below could only kill the group's leader -- and a streaming call is
  # a pipeline, whose reader and client are separate processes. stdin stays
  # /dev/null, as it was before job control was on: `claude -p` takes its prompt
  # as an argument, and a client that decided to read the terminal instead would
  # hang.
  set -m
  "$@" </dev/null &
  CALL_PID=$!
  local waited=0
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      # The pid is kept here and cleared by stop_call: the wait below has to
      # name the call to reap it, and cleanup must not signal it twice.
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

# The credential the client is given on the swapped calls. The control and the
# observed control run on the real token -- they are what says the token works
# and that the client sends it -- while the swap, the streaming prompt, the
# dropped headers and the delayed proxy all run on a PLACEHOLDER that is not a
# credential, because there the proxy is what attaches the real one, and a call
# that carried it anyway would prove nothing.
#
# The placeholder is RANDOM, one per run, and the proxy requires it on the
# incoming request before it attaches the real token. Two things follow. The
# swap's PASS now rests on the proxy having SEEN the request and performed the
# swap (the review's M3): a client that answered without going through the proxy
# leaves the proxy's log empty, and that is not a swap. And the real token
# cannot be attached to a request this check did not make: loopback is reachable
# from any page a browser loads, and a cross-origin "simple" POST from such a
# page carries no Authorization header this run's secret could match (the
# review's m1). It is generated here rather than written down, so no fixed string
# can be replayed at the listener.
random_placeholder() {
  python3 -c 'import secrets; print("placeholder-" + secrets.token_hex(16))'
}

# Where a call is told its credential is — a SOURCE, never the value, and the
# source is always a PATH: the 0600 token file for the real calls, the 0600
# placeholder file for the ones the proxy swaps. There is deliberately no
# `literal:` form. It was how the placeholder used to be passed, and that put
# the placeholder's value on `env`'s own argv (`CHK90_CRED_SOURCE=literal:<the
# value>`), which /proc/<pid>/cmdline shows to every process on the machine for
# as long as the process lives — the thing this design exists to prevent, done
# to the placeholder instead of the token. With a path the argv carries a
# filename. The child reads the source into CLAUDE_CODE_OAUTH_TOKEN itself, and
# that is what keeps the token off every command line too: `env
# CLAUDE_CODE_OAUTH_TOKEN=value` would put the value in `env`'s argv.
#
# Three things about the child, the first two of them the review's M1: it runs
# in the SCRATCH DIRECTORY, not in whatever directory the owner launched the
# check from; Claude Code is told to start no hook and no MCP server; and it is
# given NO TOOLS and NO SESSION FILE of its own. Every settings source Claude
# Code loads is discovered from its working directory (the project's .claude/
# above all), and every hook, plugin hook, MCP server or TOOL CALL it starts is
# a CHILD of `claude` that inherits its environment — the
# CLAUDE_CODE_OAUTH_TOKEN in this one included. The token is in the environment
# of this child by necessity (that is how the client is given its credential at
# all), so the check has to make sure nothing else is started to inherit it:
# --settings '{"disableAllHooks":true}' turns the hooks off,
# --strict-mcp-config refuses every MCP server the machine's configs name, and
# --tools "" removes the built-in set entirely — a prompt in this check asks
# for a sentence or for numbers, so no tool is needed for it, and a tool call
# is a process the client starts on its own with the token in its environment.
# --no-session-persistence then keeps the client from writing a session file
# beside the scratch directory's records, which would outlive the run in the
# owner's own transcript store. All three were checked against the installed
# client's `claude --help` (`--tools` accepts "" to disable every tool;
# --no-session-persistence needs --print, which every call here passes) and
# the script's own test fails if it passes a flag that help does not list.
# --bare is not the answer: it refuses OAuth, which is the credential under
# test. What still can inherit it is a process `claude` itself decides to start
# (an agent, a background fetch), and that is named in the register rather than
# hidden.
# shellcheck disable=SC2016 # the child's own text: nothing here expands in this shell
CRED_CHILD='
case "$CHK90_CRED_SOURCE" in
file:*) CLAUDE_CODE_OAUTH_TOKEN=$(cat "${CHK90_CRED_SOURCE#file:}") || exit 1 ;;
*) echo "check90: the call was given no credential file" >&2; exit 1 ;;
esac
export CLAUDE_CODE_OAUTH_TOKEN
unset CHK90_CRED_SOURCE
cd "$CHK90_CWD" || exit 1
unset CHK90_CWD
exec claude --settings "{\"disableAllHooks\":true}" --strict-mcp-config --tools "" --no-session-persistence "$@"'

# claude_call BASE_URL CRED_SOURCE OUT CALL_TIMEOUT "$@" — runs claude with
# nothing in its environment but the credential and the base URL this check
# chose. The five variables the guards refused cannot reach it from here
# either, and no argument of the call carries a credential: its argv holds only
# the source, which is a path, and the child is what reads it.
claude_call() {
  local base=$1 src=$2 out=$3 secs=$4
  shift 4
  # `|| status=$?` and not `set +e`: the ERR trap below is armed for internal
  # errors, and a function returning non-zero triggers it even where errexit is
  # off (checked, not assumed). A call that fails is the MEASUREMENT, so its
  # status is taken in a `||` list, where the trap is not run.
  local status=0
  with_timeout "$secs" env \
    ${ENV_UNSET[@]+"${ENV_UNSET[@]}"} \
    ANTHROPIC_BASE_URL="$base" \
    CHK90_CRED_SOURCE="$src" CHK90_CWD="$SCRATCH" \
    bash -c "$CRED_CHILD" check90-claude "$@" >"$out" 2>&1 || status=$?
  return "$status"
}

phase_log() { printf '%s/proxy-%s.log' "$SCRATCH" "$1"; }

# The observed control and the swap both go through the proxy; the direct
# control does not, and therefore produces no credential-header line.
header_of() { # header_of LOGFILE
  sed -n 's/^FACT credential-header //p' "$1" 2>/dev/null | tail -n 1
}
attempts_in() { # attempts_in LOGFILE
  local n
  n=$(grep -c '^FACT request ' "$1" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}
header_names_of() { # header_names_of LOGFILE — the names the swap call forwarded
  # `|| true` at the end of the pipeline: an empty log (the proxy saw no
  # request) leaves grep with nothing to match, grep exits 1, and `set -e` under
  # `pipefail` would end the run with no fact recorded -- an abort that reads as
  # "tier A is not viable" in the block (the review's M3).
  sed -n 's/^FACT request .* headers: //p' "$1" 2>/dev/null | tail -n 1 |
    tr ',' '\n' | sed '/^$/d' |
    grep -v -x -e host -e content-length -e authorization -e x-api-key \
      -e connection -e expect -e accept-encoding -e transfer-encoding | tr '\n' ' ' || true
}

# ------------------------------------------------------------------- the steps
step_control() {
  say "step 1/7: the control (no proxy, your token in the environment)"
  if claude_call "https://api.anthropic.com" "file:$TOKEN_FILE" "$SCRATCH/control.out" "$CALL_TIMEOUT" -p "say ok"; then
    fact "control" PASS "the call succeeded with the setup-token and no proxy in its path"
    # token-validity is DERIVED from this call, never from a paste: the only
    # thing this check can honestly say about the token's validity is that it
    # was accepted, now. The prompt that asked for the line setup-token printed
    # ("valid for 1 year") was removed — that line sits next to the token, so
    # asking for it invited a paste that carried the token into this block, into
    # the owner's history and into whatever the paste was sent to. U2's expiry
    # stays Anthropic's own answer, in `claude setup-token`.
    fact "token-validity" PASS "not read from the setup-token output and never asked for (see the token-file fact): the token was ACCEPTED by a live call at $(date -u +%Y-%m-%dT%H:%M:%SZ), which is what this run measured. When it expires is Anthropic's answer, printed by 'claude setup-token' itself"
  else
    fact "token-validity" FAIL "the control call was refused at $(date -u +%Y-%m-%dT%H:%M:%SZ), so this run measured nothing about the token's validity — and this check does not ask you to paste setup-token's line about it"
    fact "control" FAIL "the call failed ($(head -c 300 "$SCRATCH/control.out" | tr '\n' ' '))"
    stop "the control call failed, so nothing about the swap can be learned: the token itself is what is in question. A token that fails here says nothing about the header swap"
  fi
}

step_observed_control() {
  say "step 2/7: the observed control (the same token, through the proxy, swapping nothing)"
  local log
  log=$(phase_log observed)
  start_proxy "$log" --no-swap
  if claude_call "http://127.0.0.1:$PORT" "file:$TOKEN_FILE" "$SCRATCH/observed.out" "$CALL_TIMEOUT" -p "say ok"; then
    local h
    h=$(header_of "$log")
    if [ "$h" = "authorization" ]; then
      fact "observed-control" PASS "the client sent the credential this check swaps (credential-header: $h)"
    else
      fact "observed-control" FAIL "the client sent '${h:-nothing}', not authorization, so this environment is not on the setup-token path — the control measured something else"
      stop "the client sent '${h:-nothing}' rather than an Authorization header, so this environment routes 'claude' somewhere other than the subscription credential (a Vertex, Bedrock or API-key path). Nothing was learned about the swap"
    fi
  else
    fact "observed-control" FAIL "the call failed through the proxy with nothing swapped ($(head -c 300 "$SCRATCH/observed.out" | tr '\n' ' '))"
    stop "the observed control failed with the client's own credential untouched, so the proxy in the path is what failed — not the swap"
  fi
}

step_swap() {
  say "step 3/7: the swap (the proxy strips the client's credential and attaches yours)"
  local log
  log=$(phase_log swap)
  start_proxy "$log" --token-file "$TOKEN_FILE" --error-fields
  local started ended
  started=$(date +%s)
  if claude_call "http://127.0.0.1:$PORT" "file:$PLACEHOLDER_FILE" "$SCRATCH/swap.out" "$CALL_TIMEOUT" -p "say ok"; then
    ended=$(date +%s)
    local h attempts ok
    h=$(header_of "$log")
    attempts=$(attempts_in "$log")
    ok=$(sed -n 's/^FACT response \([0-9]*\) .*/\1/p' "$log" 2>/dev/null | grep -E '^2' | head -n 1 || true)
    if [ "$attempts" -lt 1 ]; then
      # The client exited 0 without the proxy having seen a request: nothing was
      # swapped, and nothing about the swap was measured. That is "could not
      # tell", never "not viable" — and never a PASS (the review's M3).
      fact "swap" INFO "the call exited 0, but the proxy saw no request: there was no swap to judge"
      fact "swap-credential-header" INFO "credential-header: ${h:-missing} (the proxy saw ${attempts} request(s))"
      stop "the swap call exited 0 without the proxy having seen a single request (${attempts}), so no credential was swapped and this run measured nothing. Nothing is concluded about tier A"
    fi
    if [ -z "$ok" ] || [ "$h" != authorization ]; then
      fact "swap" FAIL "the proxy performed the swap and the call still failed: upstream answer '${ok:-none}', credential-header '${h:-missing}', after $((ended - started))s. That is a measurement, not a missing one"
      fact "swap-credential-header" FAIL "credential-header: ${h:-missing}"
      fact "headers-required" FAIL "not recorded: the swapped call did not complete successfully"
      fact "tier-a" FAIL "NOT VIABLE — subscription traffic did not survive the header swap (Global Constraint 4: the TIER A tasks are dropped and the service ships the B-only cascade)"
      save_usage_limit "$log"
      return 0
    fi
    fact "swap" PASS "the call succeeded with the proxy's own credential attached ($((ended - started))s; the proxy saw ${attempts} request(s) and answered ${ok})"
    fact "swap-credential-header" PASS "credential-header: ${h}"
  else
    fact "swap" FAIL "the call failed through the proxy while the control succeeded ($(head -c 300 "$SCRATCH/swap.out" | tr '\n' ' '))"
    fact "swap-credential-header" INFO "credential-header: $(header_of "$log")"
    fact "headers-required" FAIL "not recorded: the swap call did not complete"
    fact "tier-a" FAIL "NOT VIABLE — subscription traffic did not survive the header swap (Global Constraint 4: the TIER A tasks are dropped and the service ships the B-only cascade)"
    save_usage_limit "$log"
    return 0
  fi
  save_usage_limit "$log"
  save_optional_headers "$log"
}

# The usage limit is looked for, never provoked: whatever an error response
# happened to carry is recorded, and if none occurred the check passes and U3
# stays open.
save_usage_limit() { # save_usage_limit LOGFILE
  local log=$1 line
  line=$(grep -m1 '^FACT error-fields ' "$log" 2>/dev/null || true)
  if [ -z "$line" ]; then
    local status
    status=$(sed -n 's/^FACT response \([0-9]*\) .*/\1/p' "$log" 2>/dev/null | sort -u | grep -E '^(4|5)' | head -n 1 || true)
    if [ -n "$status" ]; then
      local reset
      reset=$(sed -n 's/^FACT response [0-9]* .*headers: //p' "$log" | tr ',' '\n' |
        grep -E 'retry-after|ratelimit|reset' | tr '\n' ' ' || true)
      fact "usage-limit" PASS "status $status, reset headers: ${reset:-none seen} (no error object came back; U3 stays open until a reset-carrying answer is seen)"
    else
      fact "usage-limit" PASS "none observed — none was provoked; U3 stays open and the plan's 15-minute default back-off applies"
    fi
    return 0
  fi
  fact "usage-limit" PASS "$line (the error object's own type and message, the only body fields this check ever reads)"
  local reset
  reset=$(sed -n 's/^FACT response [0-9]* .*headers: //p' "$log" | tr ',' '\n' |
    grep -E 'retry-after|ratelimit|reset' | tr '\n' ' ' || true)
  fact "usage-limit-reset-headers" PASS "${reset:-none seen}"
}

OPTIONAL_HEADERS=""
save_optional_headers() {
  OPTIONAL_HEADERS=$(header_names_of "$(phase_log swap)")
  fact "headers-observed" PASS "${OPTIONAL_HEADERS:-none}"
}

step_streaming() {
  say "step 4/7: a long-ish prompt, streamed through the proxy"
  local log times
  log=$(phase_log stream)
  times="$SCRATCH/stream.times"
  : >"$times"
  start_proxy "$log" --token-file "$TOKEN_FILE"
  # The status is taken in a `||` list for the reason claude_call's comment
  # gives: a cap that fires is the answer, not an internal error.
  local status=0
  # Under the same cap as every other call. It is a pipeline, so the cap has to
  # reach the group, not one process: with_timeout and stop_call do that.
  with_timeout "$CALL_TIMEOUT" stream_call "$times" || status=$?
  local lines first last spread
  lines=$(wc -l <"$times" | tr -d ' ')
  first=$(head -n 1 "$times" 2>/dev/null || true)
  last=$(tail -n 1 "$times" 2>/dev/null || true)
  # A call the cap cut off before its first line leaves the file empty, and an
  # empty variable is 0 in an arithmetic context: this reads as a spread of 0,
  # which is only ever reported when the call produced lines.
  spread=$((last - first))
  if [ "$status" -eq 0 ] && [ "$lines" -gt 1 ]; then
    if [ "$spread" -ge 2 ]; then
      fact "stream" PASS "completed, $lines output lines over ${spread}s of arrival — the text arrived progressively, not in one burst"
    else
      fact "stream" FAIL "completed, but all $lines lines arrived within ${spread}s: this client buffers, or the answer was too short to tell"
    fi
  elif [ "$status" -eq 0 ]; then
    fact "stream" FAIL "completed but produced $lines line(s), too few to say whether it streamed"
  elif [ "$status" -eq 124 ]; then
    fact "stream" FAIL "did not complete: this check's own ${CALL_TIMEOUT}s cap cut the call off, after $lines line(s) of output"
  else
    fact "stream" FAIL "the call failed (status $status) after $lines line(s) of output"
  fi
}

stream_call() { # stream_call TIMESFILE — one prompt, its arrival times recorded
  local times=$1
  env ${ENV_UNSET[@]+"${ENV_UNSET[@]}"} \
    ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT" \
    CHK90_CRED_SOURCE="file:$PLACEHOLDER_FILE" CHK90_CWD="$SCRATCH" \
    bash -c "$CRED_CHILD" check90-claude \
    -p "Write the numbers from 1 to 20, one per line." |
    while IFS= read -r _line; do date +%s >>"$times"; done
}

step_drops() {
  say "step 5/7: dropping each optional header in turn (this is the list Task C1.7 is written against)"
  local required=() optional=() name log status
  local n=0 dropped=0 stopped=""
  for name in $OPTIONAL_HEADERS; do
    n=$((n + 1))
    if [ "$n" -gt "$MAX_DROPS" ]; then
      fact "header-drop-skipped" PASS "more than $MAX_DROPS optional headers were seen; the rest were not dropped (CHECK90_MAX_DROPS)"
      stopped="CHECK90_MAX_DROPS=$MAX_DROPS"
      break
    fi
    if past_deadline; then
      budget_exceeded "the rest of the header drops"
      stopped="the run's ${TOTAL_SECONDS}s budget"
      break
    fi
    log=$(phase_log "drop-$name")
    start_proxy "$log" --token-file "$TOKEN_FILE" --drop "$name"
    if claude_call "http://127.0.0.1:$PORT" "file:$PLACEHOLDER_FILE" "$SCRATCH/drop.out" "$CALL_TIMEOUT" -p "say ok"; then
      optional+=("$name")
      fact "header-drop-$name" PASS "the call survived dropping it (optional)"
    else
      required+=("$name")
      fact "header-drop-$name" FAIL "the call failed when it was dropped, so it MUST pass through"
    fi
    dropped=$((dropped + 1))
  done
  if [ "$dropped" -eq 0 ]; then
    # Nothing was dropped, so NEITHER list was measured — and an empty list is
    # not the same answer as a list that came back empty. It is reachable two
    # ways that both leave the loop before its first drop: the documented knob
    # CHECK90_MAX_DROPS=0, and the budget (past_deadline) on the first
    # iteration. The fallback text used to read "none: every header observed is
    # optional" for a run that tested not one header, which is a claim about the
    # wire made on no evidence — and it is the list Task C1.7 is written
    # against. INFO, not PASS and not FAIL: the run learned nothing here, and
    # the detail says which of the two stopped it.
    local why="the run saw no optional header to drop at all"
    if [ -n "$stopped" ]; then
      why="the loop was stopped before its first drop by $stopped"
    fi
    fact "headers-required" INFO "no header was dropped, so this list was NOT measured: $why. It is not 'none' — read headers-observed for what the client did send, and re-run without that stop to get the list Task C1.7 is written against"
    fact "headers-optional" INFO "no header was dropped, so nothing was tested as optional: $why"
    return 0
  fi
  fact "headers-required" PASS "${required[*]:-none: every header observed is optional}"
  fact "headers-optional" PASS "${optional[*]:-none}"
}

step_timeout() {
  say "step 6/7: the client's timeout and retries (a proxy that answers $DELAY s late)"
  local log started ended attempts
  local status=0
  log=$(phase_log delay)
  start_proxy "$log" --token-file "$TOKEN_FILE" --delay "$DELAY"
  started=$(date +%s)
  claude_call "http://127.0.0.1:$PORT" "file:$PLACEHOLDER_FILE" "$SCRATCH/delay.out" "$CALL_TIMEOUT" -p "say ok" || status=$?
  ended=$(date +%s)
  attempts=$(attempts_in "$log")
  if [ "$status" -eq 124 ]; then
    fact "timeout-retry" PASS "the client was still waiting when this check's own ${CALL_TIMEOUT}s cap cut it off, after ${attempts} attempt(s) — so its timeout is longer than ${CALL_TIMEOUT}s or it has none (U4: the wait belongs in the runner)"
  elif [ "$status" -eq 0 ]; then
    fact "timeout-retry" PASS "the client waited $((ended - started))s for an answer delayed by ${DELAY}s, in ${attempts} attempt(s), and succeeded"
  else
    fact "timeout-retry" PASS "the client gave up after $((ended - started))s on an answer delayed by ${DELAY}s, in ${attempts} attempt(s), status $status ($(head -c 200 "$SCRATCH/delay.out" | tr '\n' ' '))"
  fi
}

# ------------------------------------------------------------------- reporting
tier_a() {
  local t
  t=$(grep -m1 '^swap|' "$FACTS_FILE" 2>/dev/null | cut -d'|' -f2 || true)
  case "$t" in
  PASS)
    fact "tier-a" PASS "VIABLE — the swap call succeeded where the control did"
    return 0
    ;;
  FAIL)
    # The FAIL fact was already recorded with its reason; nothing to add.
    return 1
    ;;
  *)
    fact "tier-a" INFO "INCONCLUSIVE — the run did not reach the swap call; read the FAILed fact above for which step stopped it"
    return 2
    ;;
  esac
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# FAIL — not reached, and any other recorded key (the per-header drops) follows,
# sorted. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(claude-version env-cleared managed-settings-dir settings-routing
  token-file token-validity control observed-control swap swap-credential-header
  stream headers-observed headers-required headers-optional usage-limit
  timeout-retry restore tier-a)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 90 RESULT\n' >&3
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
  printf 'CHECK 90 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
free_port() {
  python3 - <<'PORT_EOF'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PORT_EOF
}

main() {
  # FIRST, before anything below can use one: free_port and random_placeholder
  # are python3 programs, and `need python3` is the only place that says so —
  # checked after them it would be checked after the run had already failed on a
  # missing interpreter, with python3's own "command not found" instead of the
  # one line this script promises. It is also the guard that needs nothing: no
  # scratch directory, no fact file, no call.
  need_tools

  PORT=${CHECK90_PORT:-}
  [ -n "$PORT" ] || PORT=$(free_port)
  DELAY=${CHECK90_DELAY:-90}
  CALL_TIMEOUT=${CHECK90_CALL_TIMEOUT:-240}
  TOTAL_SECONDS=${CHECK90_TOTAL_SECONDS:-1800}
  MAX_DROPS=${CHECK90_MAX_DROPS:-8}
  VERSION_TIMEOUT=${CHECK90_VERSION_TIMEOUT:-30}
  UPSTREAM=${CHECK90_UPSTREAM:-api.anthropic.com}
  UPSTREAM_SCHEME=https
  case "$UPSTREAM" in
  127.0.0.1:* | localhost:* | "[::1]:"*) UPSTREAM_SCHEME=http ;;
  *)
    if [ -n "${CHECK90_UPSTREAM:-}" ]; then
      die "Check 90 refused: CHECK90_UPSTREAM may only name a loopback address, so the token can never leave this machine"
    fi
    ;;
  esac

  # The per-run placeholder the proxy requires on every request it swaps. It is
  # generated here, before the first proxy starts: a fixed string could be
  # replayed at the listener by anything else on this machine, and a per-run
  # secret cannot (the review's m1). It is written into a 0600 file inside the
  # scratch directory and handed to the proxy by NAME — `ps` shows a process's
  # argv to every user on the machine for as long as it lives, and the delayed
  # phase's proxy lives for minutes. The file goes with the scratch directory.
  PLACEHOLDER=$(random_placeholder) || stop "cannot generate this run's placeholder"
  [ -n "$PLACEHOLDER" ] || stop "cannot generate this run's placeholder"

  scratch_up
  # Into the 0600 file the proxy is pointed at, and nowhere else: the value
  # reaches the proxy by NAME and never through its argv.
  (umask 077 && printf '%s' "$PLACEHOLDER" >"$PLACEHOLDER_FILE") \
    || stop "cannot write this run's placeholder into the scratch directory"
  chmod 600 "$PLACEHOLDER_FILE" \
    || stop "cannot make the placeholder file readable by this user only"
  write_files
  # ONE `claude --version`, and under a cap of its own (the review's m4): a
  # client that hangs on startup must not hold the whole run, and the version is
  # printed once rather than probed twice.
  local vout="$SCRATCH/claude-version.out" vstatus=0
  with_timeout "$VERSION_TIMEOUT" claude --version >"$vout" 2>/dev/null || vstatus=$?
  case "$vstatus" in
  0)
    local vtext
    vtext=$(head -n 1 "$vout" 2>/dev/null || true)
    if [ -n "$vtext" ]; then
      fact "claude-version" PASS "$vtext"
    else
      fact "claude-version" FAIL "claude --version printed nothing"
    fi
    ;;
  124) fact "claude-version" FAIL "claude --version did not answer within ${VERSION_TIMEOUT}s, so the client is not usable here (the probe is capped: CHECK90_VERSION_TIMEOUT)" ;;
  *) fact "claude-version" FAIL "claude --version failed (status $vstatus)" ;;
  esac
  guard_xtrace
  guard_env
  guard_settings
  ask_token

  if [ -n "$DRY_RUN" ]; then
    say "dry run: guards and prompts done, no calls made"
    fact "dry-run" PASS "guards and token prompt only"
    fact "token-validity" INFO "a dry run made no call, so nothing about the token was measured — and this check never asks you to paste setup-token's line about its validity"
    return 0
  fi

  DEADLINE=$(($(date +%s) + TOTAL_SECONDS))
  step_control
  step_observed_control
  step_swap
  if ! grep -q '^swap|PASS' "$FACTS_FILE"; then
    # The swap call failed: tier A is decided, and the remaining prompts would
    # cost the owner more subscription traffic to learn nothing new.
    say "the swap call failed, so the remaining steps are skipped (they would spend prompts for nothing)"
  else
    # Each step is checked against the budget as it is reached, so the run
    # stops where the budget ran out rather than a step later.
    local step
    for step in step_streaming step_drops step_timeout; do
      if past_deadline; then
        budget_exceeded "$step"
        continue
      fi
      "$step"
    done
  fi

  # The verdict is a RETURN VALUE, not an error: from here on the ERR trap is
  # off, so `tier_a` answering 1 (tier A not viable) is not rewritten as an
  # internal error. (Guarding the whole call instead -- `main "$@" || rc=$?` --
  # would be worse than useless: bash turns errexit OFF inside a function called
  # from an && or || list, so that form silently disables `set -e`, and this
  # trap with it, for the entire run.)
  trap - ERR
  local rc=0
  tier_a || rc=$?
  return "$rc"
}

# on_signal: the ONE signal handler, armed for the whole run and never
# disarmed — including through the cleanup, which is where its second answer
# matters. A Ctrl-C before cleanup ends the run at once; a Ctrl-C DURING
# cleanup (or a closing terminal's SIGHUP) would otherwise kill this shell with
# the token file still in the scratch directory, and with no RESULT block
# printed to say so. So while cleanup runs it says one line and returns.
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
# No `|| ...` around this call, for the reason spelled out where main turns the
# trap off before its verdict: an || list would switch errexit off inside main.
main "$@"
