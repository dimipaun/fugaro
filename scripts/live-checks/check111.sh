#!/usr/bin/env bash
# Check 111 — the billing picker's failure modes, as ONE script YOU run.
#
# It settles the failure half of the wizard's billing question (D3 and D6, plan
# tasks W4.1–W4.5): with neither source able to answer — the Cloud Billing API
# off on the credentials' quota project, or a caller without
# billing.accounts.list, or no terminal at all — what the picker prints, and what
# it must never do. Check 27 step 3 is the picker's success path against a real
# account; this is the other half: the seven runs of docs/gcp-live-checklist.md's
# "Check 111", one per failure mode, each on a throwaway project of your own.
#
# Usage:      bash scripts/live-checks/check111.sh \
#               --project <throwaway> --account-names <file> \
#               [--account-ids <file>] [--api-off-project <Q>] \
#               [--api-on-project <S>] [--second-account <email>] \
#               [--second-adc <file>] [--role-removed] [--region us-east5] \
#               [--modes 1a,1b,2a,2b,3,4,5]
#             bash scripts/live-checks/check111.sh --dry-run […same arguments…]
#             (the guards and the files only: no gcloud call and no fugaro call
#             is made; it prints the commands a real run would run and the
#             commands its Restore would run, and its block carries no verdict —
#             a dry run measures no criterion, so it has none to report)
# Needs:      bash, mktemp, python3, gcloud, fugaro and jq on PATH. Nothing is
#             installed and nothing is fetched; if one of those is missing the
#             script stops with one line naming it. python3 is this check's own
#             runner: it gives the four typed modes a real terminal (a pty), the
#             three untyped ones no terminal at all, and every run a transcript
#             and a timeout.
# Where:      YOUR throwaway project ONLY, and never `belong`, `fugaro-dev`,
#             `edge-devel-dimi` or `fugaro-belong`: those names are refused as
#             `--project`, as `--api-off-project` and as `--api-on-project`
#             before anything runs. `--project` must be the project `fugaro
#             setup` created (its label `fugaro_created=true` is asserted), it
#             must exist, be ACTIVE, carry Firebase and have NO billing account
#             linked — the state the project stage prints its guided line in,
#             which is the line every mode asserts. Never run this against a
#             project you need: three modes repoint YOUR Application Default
#             Credentials' quota project at these throwsaways, and each mode
#             first disables `cloudbilling.googleapis.com` on the throwaway it
#             is about (mode 1(a) on `--project`, mode 1(b) on
#             `--api-off-project`).
# Creates:    nothing. Every step is a refusal: no project is created, no
#             billing account is linked, no API is enabled by a run, no config is
#             written outside this check's own scratch directory, and the one
#             thing any mode reaches outside your shell is your ADC — its quota
#             project, and (with `--second-adc`) the login mode 2's runs use,
#             both of which the Restore puts back.
# Costs:      nothing but wall clock (a few minutes: seven wizard runs, each
#             stopping at the billing stage). No model call, no run, no money.
# Runs:       by YOU. No agent runs any step of this, and the script refuses to
#             start when this shell carries an agent's own marker (CLAUDECODE,
#             CLAUDE_CODE_ENTRYPOINT, CLAUDE_CODE_REMOTE, CURSOR_AGENT,
#             AI_AGENT): modes 1 to 4 assert what a real terminal does, and a
#             session that is itself an agent's cannot tell them apart from mode
#             5. Mode 5's marker is set for its one child alone, so it is gone
#             whatever ends the run, and modes 3 and 4 take CLAUDE_CODE_SSE_PORT
#             out of THEIR children alone — that marker with no terminal is what
#             internal/agentenv.Session reads as an agent, and those two runs are
#             a person's shell whose input is not a terminal.
# Cleanup:    always, and before the block is printed: if mode 2 swapped the
#             second login in, your ADC file is your own again, byte for byte,
#             which is what mode 2 found; the API this check disabled on
#             `--project` and on `--api-off-project` is enabled
#             again, your ADC's quota project goes back to the one it had, and
#             the scratch directory with the seven transcripts is removed. The
#             `restore` row says what really happened; a step that could not be
#             undone reads FAIL and names the command to run by hand. No role is
#             removed by any step of this script — the mode 2 route that removes
#             one is yours, and its undo is named in the `manual` row.
# Output:     progress on stderr (each run's own transcript, live), ONE block on
#             stdout headed CHECK 111 RESULT, one FACT per criterion, to copy
#             back to the register, ending `CHECK 111 RESULT: PASS` or
#             `CHECK 111 RESULT: FAIL`. Exit 0 when every fact passed, 1 when one
#             FAILed, 2 when the run could not tell (a guard stopped it, a tool
#             is missing, a step this check depended on failed).
#
# EVERY RUN IS A REFUSAL BY DESIGN, AND MODE 2(a) IS THE ONE EXCEPTION: mode 2(a)
# runs with the fallback able to answer, so a list of accounts IS the designed
# behaviour there, ids and all — its own criteria record which source answered
# and how many accounts it listed, and the block deliberately does not repeat the
# names and ids. The words "billing accounts" are not a leak in any mode: the
# guided line's own hint names `gcloud billing accounts list` on purpose. What
# must never appear anywhere else is a display name of yours, an account id or a
# numbered account list.
#
# `--modes` narrows a run to some of the seven, for settling one mode without
# re-running the rest. Its block is honest about the rest: the modes it did not
# select read FAIL — not run, because an unrun mode is not a pass — so the whole
# check reads FAIL until all seven have run and passed. `--project` and
# `--account-names` are required; `--api-off-project` is required by mode 1(b),
# `--api-on-project` by mode 2, and `--second-account`/`--role-removed` are how
# mode 2's premise is declared: a caller that lacks billing.accounts.list takes
# a browser or your own IAM, which no script may do for you. The `manual` row is
# that one step, with the exact commands and what to paste back.
#
# MODE 2 IS THE ONLY PART OF THIS RUN WHOSE CALLER IS THE SECOND ACCOUNT, and
# that is what `--second-adc` is for. One gcloud ADC file is one login, and no
# script can log an account in halfway through — so the second login is prepared
# ONCE, by you, as a file, and this check copies it over gcloud's own ADC file
# (the one `adcFilePath` names: $GOOGLE_APPLICATION_CREDENTIALS, else
# $CLOUDSDK_CONFIG, else ~/.config/gcloud) for mode 2's runs alone. Everything
# else — modes 1(a), 1(b), 3, 4 and 5 — runs under your own login, which is the
# premise the checklist writes: their runs read the project they are about
# through those credentials, and a caller that lacks billing.accounts.list is
# exactly what mode 2 exists to see, so 1(a) would refuse instead of offering if
# the second account were the whole run's caller (that combination is refused
# here rather than reported as a FAIL the checklist does not describe). Two
# byte-for-byte copies are kept in the scratch directory for the length of the
# run — your own file as mode 2 found it, and the second login as you made it —
# and both files are put back exactly, by mode 2's end and again by the Restore
# whatever ends the run; the copies never outlive the run, not even under
# `CHECK111_KEEP_SCRATCH`. Nothing here reads either file: `cp` and `cmp` move
# and compare bytes, and no refresh token ever reaches a shell variable, a
# transcript or a fact.
#
# CHECK111_DRY_RUN, CHECK111_RUN_SECONDS and CHECK111_KEEP_SCRATCH exist for this
# script's own test and for you: the first turns --dry-run on from the
# environment, the second shortens the cap on one run (default 900 s, after which
# the run is killed and the mode FAILs — a mode that waits for input it cannot
# get is a failure of that mode), and the third keeps the scratch directory with
# its transcripts and says where it is. None of them can point the check at
# another project or another installation.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace prints every
# command with its arguments, and one of mode 2(a)'s criteria reads the accounts
# the fallback listed — in a trace they would land in whatever log the owner
# pastes. BASH_XTRACEFD is unset so a trace redirected to a file this script
# knows nothing about cannot be reopened, and guard_xtrace REFUSES a shell that
# arrived with tracing on (the flag sits in SHELLOPTS/BASHOPTS then, and every
# child would start tracing on its own). The same guard as Checks 90, 91, 145,
# 146 and 190.
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
# Set to yes by main once every selected mode has run, so the exit trap can tell
# a finished run from one that ended on an error of its own.
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap runs the
# Restore (the APIs this check disabled go back on, the ADC's quota project goes
# back) and prints the block, so a stop still gives you something to paste back.
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
# first command that fails outside a guard would end the run with no fact at all,
# and the block would then read as a pass for a run that measured nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the picker" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ------------------------------------------------------------------- arguments
N=fugaro
REGION_DEFAULT=us-east5
USAGE="check111.sh --project <throwaway> --account-names <file> [--account-ids <file>] [--api-off-project <Q>] [--api-on-project <S>] [--second-account <email> [--second-adc <file>] | --role-removed] [--region <region>] [--modes 1a,1b,2a,2b,3,4,5] [--dry-run]"
MODES_ALL=(1a 1b 2a 2b 3 4 5)
MODES_ALL_STR="1a 1b 2a 2b 3 4 5"

P=""
NAMES_FILE=""
IDS_FILE=""
Q=""
S=""
SECOND_ACCOUNT=""
SECOND_ADC=""
ROLE_REMOVED=no
REGION=$REGION_DEFAULT
MODES=""

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project id. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --account-names)
      [ $# -ge 2 ] || die "--account-names needs a file. usage: $USAGE"
      NAMES_FILE=$2
      shift 2
      ;;
    --account-ids)
      [ $# -ge 2 ] || die "--account-ids needs a file. usage: $USAGE"
      IDS_FILE=$2
      shift 2
      ;;
    --api-off-project)
      [ $# -ge 2 ] || die "--api-off-project needs a project id. usage: $USAGE"
      Q=$2
      shift 2
      ;;
    --api-on-project)
      [ $# -ge 2 ] || die "--api-on-project needs a project id. usage: $USAGE"
      S=$2
      shift 2
      ;;
    --second-account)
      [ $# -ge 2 ] || die "--second-account needs the second account's email. usage: $USAGE"
      SECOND_ACCOUNT=$2
      shift 2
      ;;
    --second-adc)
      [ $# -ge 2 ] || die "--second-adc needs a file. usage: $USAGE"
      SECOND_ADC=$2
      shift 2
      ;;
    --role-removed)
      ROLE_REMOVED=yes
      shift
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a region. usage: $USAGE"
      REGION=$2
      shift 2
      ;;
    --modes)
      [ $# -ge 2 ] || die "--modes needs a comma-separated list of ${MODES_ALL[*]}. usage: $USAGE"
      MODES=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
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
  [ -n "$P" ] || die "--project is required: the throwaway project \`fugaro setup\` created, which exists, is ACTIVE, carries Firebase and has no billing link. usage: $USAGE"
  [ -n "$NAMES_FILE" ] || die "--account-names is required: a file holding YOUR billing accounts' display names, one per line — they are the strings a leak would print. usage: $USAGE"
}

DRY_RUN=${CHECK111_DRY_RUN:-}
RUN_SECONDS=${CHECK111_RUN_SECONDS:-900}
case "${RUN_SECONDS}" in '' | *[!0-9]*) RUN_SECONDS=900 ;; esac

# ------------------------------------------------------------------ requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 111 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  need bash
  need mktemp
  need python3 "the runner that gives the four typed modes a real terminal and every run a transcript and a timeout is one python3 file"
  need gcloud
  need fugaro "every mode runs the release build that carries the picker"
  need jq "mode 4's document is checked for an account resource id with the jq line the check's own section carries"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk111-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK111_KEEP_SCRATCH:-}
C=""
RUNNER=""
REAL_GCLOUD=""
# Everything the Restore has to undo. Set where the thing is done, never
# assumed: an API this check never disabled is never enabled by the Restore.
DISABLED_P=no
DISABLED_Q=no
QUOTA_POINTED=""
ADC_QUOTA_ORIGINAL=""
SECOND_ADC_IS=no
ADC_OWN_BACKUP=""
SECOND_ADC_BACKUP=""
API_P_BEFORE=""
API_Q_BEFORE=""
API_S_BEFORE=""
LAST_STATUS=0
PRINTED=""
DRY_RAN=""
FACTS_CAPTURED=""
CLEANUP_RUNNING=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk111.XXXXXXXX") || die "Check 111 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  C="$SCRATCH/config"
  RUNNER="$SCRATCH/run.py"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 111 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
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

# -------------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print mode 2(a)'s account list into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check111.sh --project <throwaway> --account-names <file> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries the accounts mode 2(a)'s fallback lists"
}

# guard_agent: modes 1 to 4 assert what a run does at a real terminal or with no
# terminal at all, and a shell that is itself a coding agent's session cannot
# tell either from mode 5's own premise. The markers are the ones
# internal/agentenv reads. CLAUDE_CODE_SSE_PORT alone is NOT one of them: the
# IDE extension sets it in a person's own terminal too, so alone it is a
# person's session (internal/cli/initsecrets.go) — it is recorded and not
# refused, and mode 5 sets its own marker for its one child. That is a fact
# about a shell WITH a terminal, and Session is not: it reads the marker with no
# terminal as an agent's, which is every run modes 3 and 4 make. So those two
# steps take the marker out of their own children (step_3, step_4): a mode 3 run
# that answered with the agent's own refusal would be mode 5's case, passing the
# rows mode 5 passes.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  if [ -n "$found" ]; then
    fact agent-session FAIL "this shell carries $found, so it is a coding agent's session: modes 1 to 4 would not be a person's terminal, and mode 5's premise would be this session rather than one you marked"
    stop "Check 111 refuses to run in a coding agent's session ($found is set). Run it yourself at your own terminal: bash scripts/live-checks/check111.sh --project <throwaway> --account-names <file>"
  fi
  if [ -n "${CLAUDE_CODE_SSE_PORT:-}" ]; then
    fact agent-session PASS "no agent marker is set; CLAUDE_CODE_SSE_PORT is (the IDE extension sets it in a person's own terminal too), recorded and not refused — modes 3 and 4 unset it for their own children alone, where no terminal is what makes it read as an agent's, and mode 5 sets AI_AGENT for its one child"
  else
    fact agent-session PASS "no agent marker is set, so this is a person's shell and mode 5 can mark its own child"
  fi
}

# guard_name NAME VAR: the projects this check never touches. `belong` is the
# owner's own installation (edge-devel-dimi), and `fugaro-dev` and
# `edge-devel-dimi` are the installations the register's other rows run on; the
# modes here repoint the owner's ADC and disable the Cloud Billing API on a
# project, so every one of them is refused by name — before anything runs.
guard_name() { # guard_name PROJECT VAR
  case "$1" in
  "") stop "$2 is empty: ${2#--} must name your own throwaway project. Nothing was run" ;;
  belong | belong-* | fugaro-dev | edge-devel-dimi | fugaro-belong)
    stop "$2=$1 names a project this check never touches (belong on edge-devel-dimi, fugaro-dev). Name your own throwaway project. Nothing was run"
    ;;
  esac
}

guard_projects() {
  guard_name "$P" "--project"
  fact project PASS "$P (your throwaway project; never belong, fugaro-dev or edge-devel-dimi)"
  if [ -n "$Q" ]; then
    guard_name "$Q" "--api-off-project"
    [ "$Q" != "$P" ] || stop "--api-off-project must not be --project: mode 1(b) is about the quota project being ANOTHER project. Nothing was run"
  fi
  if [ -n "$S" ]; then
    guard_name "$S" "--api-on-project"
    [ "$S" != "$P" ] || stop "--api-on-project must not be --project: mode 2 is about an API that IS enabled, which P is not. Nothing was run"
    [ -z "$Q" ] || [ "$S" != "$Q" ] || stop "--api-on-project must be its own project: mode 2's S and mode 1(b)'s Q are two throwaways with opposite premises. Nothing was run"
  fi
  fact api-off-project "$([ -n "$Q" ] && echo PASS || echo INFO)" "${Q:-not given} ($([ -n "$Q" ] && echo "mode 1(b) disables cloudbilling.googleapis.com here and the Restore enables it again" || echo "mode 1(b) will NOT RUN without it"))"
  fact api-on-project "$([ -n "$S" ] && echo PASS || echo INFO)" "${S:-not given} ($([ -n "$S" ] && echo "mode 2 needs cloudbilling.googleapis.com ENABLED here, asserted before its first run" || echo "mode 2 will NOT RUN without it"))"
}

# guard_names: the display names are the strings a leak would print, so the file
# has to be there and to hold something; a file of blank lines proves nothing.
guard_names() {
  [ -f "$NAMES_FILE" ] || stop "$NAMES_FILE does not exist, and --account-names is what a leak is read against. Write YOUR billing accounts' display names into it, one per line, and run again. Nothing was run"
  [ -s "$NAMES_FILE" ] || stop "$NAMES_FILE is empty, so a leak could not be told from a pass. Write YOUR billing accounts' display names into it, one per line. Nothing was run"
  grep -q '[^[:space:]]' "$NAMES_FILE" || stop "$NAMES_FILE holds no display name, only blank lines. Nothing was run"
  fact account-names PASS "$NAMES_FILE ($(grep -c '[^[:space:]]' "$NAMES_FILE") display name(s); the strings every capture except mode 2(a)'s is grepped for)"
  if [ -n "$IDS_FILE" ]; then
    [ -s "$IDS_FILE" ] || stop "--account-ids names $IDS_FILE, which does not exist or is empty. Drop the flag or write the ids into it"
    fact account-ids PASS "$IDS_FILE ($(grep -c '[^[:space:]]' "$IDS_FILE") id(s); also grepped for, except in mode 2(a), which lists the ids by design)"
  else
    fact account-ids INFO "not given (--account-ids is optional: the ids are also caught by their own resource form, billingAccounts/<id>)"
  fi
}

guard_modes() {
  local m seen=" "
  MODES_TO_RUN=()
  if [ -z "$MODES" ]; then
    MODES_TO_RUN=("${MODES_ALL[@]}")
  else
    # tr, not `local IFS=,`: the loop's own expansion of MODES_TO_RUN and
    # MODES_ALL below must keep the shell's real IFS, or a later ${arr[*]}
    # anywhere in this function would join on a comma.
    for m in $(printf '%s' "$MODES" | tr ',' ' '); do
      case " $MODES_ALL_STR " in
      *" $m "*) ;;
      *) stop "--modes names '$m', which is not one of $MODES_ALL_STR. usage: $USAGE" ;;
      esac
      case "$seen" in *" $m "*) stop "--modes names '$m' twice. usage: $USAGE" ;; esac
      seen="$seen$m "
      MODES_TO_RUN+=("$m")
    done
  fi
  # The config root is this check's own, and the machine's installations are
  # never read or written: XDG_CONFIG_HOME is pointed at the scratch directory
  # for every run below. A shell that already points somewhere else is refused
  # BEFORE the export — after it the same stop could never fire, which is what a
  # half-run leaves behind (Check 111's guard block states the same rule).
  if [ -n "${XDG_CONFIG_HOME:-}" ] && [ "$XDG_CONFIG_HOME" != "$C" ]; then
    stop "this shell's XDG_CONFIG_HOME ($XDG_CONFIG_HOME) is not this check's own root ($C). Unset it, or start the check in a fresh shell: the machine's own installations must never be read by a mode here. Nothing was run"
  fi
  export XDG_CONFIG_HOME="$C"
  mkdir -p "$XDG_CONFIG_HOME"
  printf 'C=%s\n' "$C" >"$SCRATCH/vars.env"
  [ "$(sed -n 's/^C=//p' "$SCRATCH/vars.env")" = "$C" ] || stop "this check cannot record its config root in $SCRATCH/vars.env"
  [ "$XDG_CONFIG_HOME" = "$C" ] || stop "the run did not leave XDG_CONFIG_HOME at this check's own root (it is $XDG_CONFIG_HOME)"
  fact modes PASS "this run selects: ${MODES_TO_RUN[*]} (of $MODES_ALL_STR; a mode this run does not select reads FAIL — not run, because an unrun mode is not a pass)"
  fact config-root PASS "$XDG_CONFIG_HOME (this check's own, mode 0700, removed with the scratch directory: the machine's installations are never read or written)"
}

# guard_adc: three modes repoint your Application Default Credentials' quota
# project, so the Restore can only be complete if there is one to put back. The
# quota project is READ, and only it: the credentials file also holds a refresh
# token, which no shell of this check ever holds, prints or traces — python3
# reads the one key and nothing else reaches a variable.
adc_file() {
  printf '%s/application_default_credentials.json' "${CLOUDSDK_CONFIG:-$HOME/.config/gcloud}"
}

read_adc_quota() { # read_adc_quota FILE — the quota project, or "none"/"unreadable"
  python3 -c '
import json, sys
try:
    with open(sys.argv[1]) as f:
        doc = json.load(f)
except FileNotFoundError:
    print("unreadable"); sys.exit(0)
except Exception:
    print("unreadable"); sys.exit(0)
q = doc.get("quota_project_id") or ""
print(q if q else "none")
' "$1" 2>/dev/null || printf 'unreadable'
}

guard_adc() {
  local file quota
  file=$(adc_file)
  if [ ! -f "$file" ]; then
    stop "no Application Default Credentials at $file: three of this check's modes repoint their quota project, and the Restore can only put back one that exists. Run 'gcloud auth application-default login' first. Nothing was run"
  fi
  quota=$(read_adc_quota "$file")
  case "$quota" in
  unreadable)
    stop "$file cannot be read as JSON (or python3 refused it), so this check cannot know what quota project to put back. Nothing was run"
    ;;
  none)
    stop "your ADC at $file carries no quota project, and this check cannot unset one it set: run 'gcloud auth application-default set-quota-project <your usual project>' once, then run this check. Nothing was run"
    ;;
  esac
  ADC_QUOTA_ORIGINAL=$quota
  fact adc-quota PASS "$quota (the quota project of your Application Default Credentials, read from $file; modes 1(a), 1(b) and 2 repoint it and the Restore puts it back)"
}

# second_adc_ok FILE — "ok" when FILE parses as a JSON object carrying a "type",
# which is the shape gcloud writes for a login (authorized_user) and the shape
# the Google libraries accept. The file is read for its SHAPE and nothing else:
# no value in it reaches a variable, a transcript or a fact.
second_adc_ok() {
  python3 -c '
import json, sys
try:
    with open(sys.argv[1]) as f:
        doc = json.load(f)
except Exception:
    print("notjson"); sys.exit(0)
t = doc.get("type") if isinstance(doc, dict) else None
print("ok" if isinstance(t, str) and t else "notjson")
' "$1" 2>/dev/null || printf 'notjson'
}

# guard_second_adc: mode 2's login, as a file (see the header). What is checked
# here is only that the file could be the second account's own credentials and
# that the machine is NOT already logged in as it — the account's identity is not
# readable from the file (a refresh token names no email), and the check does not
# pretend to read one.
guard_second_adc() {
  [ -z "${GOOGLE_APPLICATION_CREDENTIALS:-}" ] || stop "GOOGLE_APPLICATION_CREDENTIALS is set ($GOOGLE_APPLICATION_CREDENTIALS), and the run's credentials come from THAT file — not from gcloud's own ADC at $(adc_file), which is the one this check repoints and swaps. Unset it (or unset it for this one command) and run again. Nothing was run"
  if [ -z "$SECOND_ADC" ]; then
    fact second-adc INFO "not given (--second-adc is what keeps mode 2's login out of the other five modes; without it, --second-account declares the login of the whole run — which only a run of modes 2a,2b alone may be)"
    return 0
  fi
  local file
  file=$(adc_file)
  [ -f "$SECOND_ADC" ] || stop "--second-adc names $SECOND_ADC, which does not exist. It is the SECOND account's own Application Default Credentials, saved to a file before this run (the manual row carries the three commands). Nothing was run"
  [ -s "$SECOND_ADC" ] || stop "--second-adc names $SECOND_ADC, which is empty. Nothing was run"
  [ "$SECOND_ADC" != "$file" ] || stop "--second-adc names $file, which IS gcloud's own ADC file: mode 2 would run under your own login and prove nothing. Save the second account's login to another path (the manual row). Nothing was run"
  [ "$(second_adc_ok "$SECOND_ADC")" = ok ] || stop "$SECOND_ADC does not read as a credentials file: gcloud writes a JSON object with a 'type' for a login, and this one does not. Check the path — the manual row has the commands that make it. Nothing was run"
  if mode_2_selected && cmp -s "$SECOND_ADC" "$file"; then
    stop "the ADC at $file is byte for byte the login --second-adc names, so this machine is still logged in as the second account — and modes 1(a), 1(b), 3, 4 and 5 run under YOUR OWN login, which is the premise the checklist writes for them. Run 'gcloud auth application-default login' and choose your own account, then run this check again. Nothing was run"
  fi
  fact second-adc PASS "$SECOND_ADC (mode 2's runs alone are run with this login: this check copies it over $file for them and puts your own file back byte for byte as soon as each of them is done, so the modes that are not mode 2 run as you whatever --modes selects, and again from its exit trap whatever ends the run)"
}

mode_2_selected() {
  local m
  for m in "${MODES_TO_RUN[@]}"; do
    case "$m" in 2a | 2b) return 0 ;; esac
  done
  return 1
}

# guard_second_account: --second-account without --second-adc says "the ADC of
# this whole run is that account". That is mode 2's premise and only mode 2's:
# every other mode's run reads the project it is about through those credentials
# — mode 1(a)'s project stage reads P's number, its Firebase and its billing link
# through them — and a caller that lacks billing.accounts.list is exactly what
# mode 2 exists to see, so mode 1(a) under the second account refuses instead of
# offering. A FAIL the checklist does not describe would be a worse answer than a
# refusal, so the combination is refused here, with both ways forward.
guard_second_account() {
  [ -n "$SECOND_ACCOUNT" ] || return 0
  [ -z "$SECOND_ADC" ] || return 0
  local m other=""
  for m in "${MODES_TO_RUN[@]}"; do
    case "$m" in 2a | 2b) ;; *) other="$other $m" ;; esac
  done
  [ -n "$other" ] || return 0
  stop "--second-account $SECOND_ACCOUNT without --second-adc makes that account the ADC of EVERY run here, and modes$other are not written for it: their runs read their project through those credentials, and a caller that lacks billing.accounts.list is what mode 2 exists to see. Save the second account's login to a file and pass --second-adc (the manual row has the commands), so only mode 2's runs use it; or run modes 2a,2b alone, with --modes 2a,2b, as a run whose whole caller is that account. Nothing was run"
}

# adc_swap_in / adc_swap_out: mode 2's login, in place and out again. Two
# byte-for-byte copies live in the scratch directory for the length of the run
# (mode 0700, and removed before the directory is — never kept, not even under
# CHECK111_KEEP_SCRATCH): your own file as mode 2 found it, and the second login
# as you made it, which is a file of yours that this check gives back unchanged
# too. Nothing here reads either file.
adc_swap_in() {
  [ -n "$SECOND_ADC" ] || return 0
  [ "$SECOND_ADC_IS" = no ] || return 0
  local live
  live=$(adc_file)
  ADC_OWN_BACKUP="$SCRATCH/adc-own.bak"
  SECOND_ADC_BACKUP="$SCRATCH/adc-second.bak"
  cp -p "$live" "$ADC_OWN_BACKUP" || stop "this check could not keep a copy of your ADC file ($live) before mode 2's swap, and without it the swap could not be undone. Nothing was run"
  cp -p "$SECOND_ADC" "$SECOND_ADC_BACKUP" || stop "this check could not keep a copy of $SECOND_ADC before mode 2's swap. Nothing was run"
  cp -p "$SECOND_ADC" "$live" || stop "this check could not put the second account's login in place for mode 2 ($SECOND_ADC -> $live). Nothing was run"
  cmp -s "$live" "$SECOND_ADC" || stop "the second account's login did not land in $live, so mode 2 would run as the wrong caller. Nothing was run"
  SECOND_ADC_IS=yes
  say "mode 2's runs use the login in $SECOND_ADC; your own ADC file is copied back byte for byte as soon as each of them is done, and by the Restore whatever ends this run"
}

adc_swap_out() {
  [ "$SECOND_ADC_IS" = yes ] || return 0
  local live ok=yes
  live=$(adc_file)
  { cp -p "$ADC_OWN_BACKUP" "$live" && cmp -s "$live" "$ADC_OWN_BACKUP"; } 2>/dev/null || ok=no
  cp -p "$SECOND_ADC_BACKUP" "$SECOND_ADC" 2>/dev/null || ok=no
  [ "$ok" = yes ] || return 1
  SECOND_ADC_IS=no
  say "your own ADC file is back in place, byte for byte — mode 2 was the only part of this run that used $SECOND_ADC"
  return 0
}

# adc_swap_out_after MODE: mode 2's login goes back out as soon as THAT mode's
# run is asserted, and not at the end of mode 2(b) — --modes may name 2(a)
# without 2(b), and the claim the swap exists for is about the OTHER modes: 1(a),
# 1(b), 3, 4 and 5 run under the login each of them is written for, whatever the
# list selects. A swap that cannot be put back is a stop, never a shrug: it is
# the caller of the rest of the run that is wrong, and the Restore asks again.
adc_swap_out_after() {
  local m=$1
  adc_swap_out && return 0
  stop "this check could not put your own ADC file back after mode $m ($(adc_file) is still the login in $SECOND_ADC), so the modes after it would run as the wrong caller. The Restore tries again; if it cannot, run 'gcloud auth application-default login' and choose your own account"
}

# -------------------------------------------------------------------- the runner
# run.py is this check's own runner. It runs ONE child and gives it what the
# mode is about: a real terminal (a pty) for the four typed modes, no terminal at
# all for the three untyped ones, the answers a mode's prompt needs — written only
# once the prompt itself has appeared, so an answer can never be taken by another
# question — a transcript, a cap on the run, and a live echo to the owner's
# stderr. A run that hits the cap is killed and reads 124, which is how "the run
# blocked waiting for input" becomes that mode's FAIL rather than a hang nobody
# can report.
write_runner() {
  cat >"$RUNNER" <<'RUNNER_EOF'
#!/usr/bin/env python3
"""Check 111's runner: one child, a pty or no terminal, its own transcript.

  run.py --mode 1a --kind pty --out FILE [--err FILE] [--echo FD]
         [--timeout SECONDS] [--expect TEXT --answer TEXT]... -- CMD [ARG...]

--kind pty runs CMD on a pty, so the child sees a real terminal on stdin, stdout
and stderr; --kind plain runs it with stdin at /dev/null, no pty at all. Every
answer is written only after its own --expect text has appeared in the child's
output, in order, so the run is answered where the mode expects to be asked and
never earlier — each answer as a line of its own, the text plus its Enter. On the timeout the child's process group is killed, a
CHECK111-RUNNER line is appended to the transcript and the exit status is 124.
The transcript is echoed to --echo (the shell's fd 4, the owner's stderr) as it
arrives, so the run is watched live while the RESULT block stays clean.
"""
import os
import select
import signal
import subprocess
import sys
import time

KEEP = 65536


def parse(argv):
    o = {"mode": None, "kind": None, "out": None, "err": None, "echo": None,
         "timeout": 600.0, "expect": [], "answer": [], "cmd": []}
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--":
            o["cmd"] = argv[i + 1:]
            break
        if a in ("--mode", "--kind", "--out", "--err", "--echo",
                 "--timeout", "--expect", "--answer"):
            if i + 1 >= len(argv):
                sys.stderr.write("run.py: %s needs a value\n" % a)
                sys.exit(2)
            v = argv[i + 1]
            i += 2
            if a == "--expect":
                o["expect"].append(v)
            elif a == "--answer":
                o["answer"].append(v)
            elif a == "--timeout":
                o["timeout"] = float(v)
            elif a == "--echo":
                o["echo"] = int(v)
            else:
                o[a[2:]] = v
        else:
            sys.stderr.write("run.py: unknown argument %r\n" % a)
            sys.exit(2)
    return o


def write_out(log, echo, data):
    log.write(data)
    if echo is not None:
        try:
            os.write(echo, data)
        except OSError:
            pass


def wait_status(pid):
    _, st = os.waitpid(pid, 0)
    if os.WIFEXITED(st):
        return os.WEXITSTATUS(st)
    if os.WIFSIGNALED(st):
        return 128 + os.WTERMSIG(st)
    return 2


def drain(fd, log, echo, until):
    while True:
        left = until - time.monotonic()
        if left <= 0:
            return
        try:
            r, _, _ = select.select([fd], [], [], min(left, 0.2))
        except InterruptedError:
            continue
        if not r:
            continue
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            return
        if not chunk:
            return
        write_out(log, echo, chunk)


def run_pty(o, echo):
    log = open(o["out"], "wb", buffering=0)
    pid, fd = os.forkpty()
    if pid == 0:
        try:
            os.execvp(o["cmd"][0], o["cmd"])
        except OSError as e:
            sys.stderr.write("run.py: cannot run %s: %s\n" % (o["cmd"][0], e))
            os._exit(127)
    deadline = time.monotonic() + o["timeout"]
    pending = list(zip(o["expect"], o["answer"]))
    buf = b""
    timed_out = False
    while True:
        left = deadline - time.monotonic()
        if left <= 0:
            timed_out = True
            break
        try:
            r, _, _ = select.select([fd], [], [], min(left, 0.2))
        except InterruptedError:
            continue
        if not r:
            continue
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        buf += chunk
        write_out(log, echo, chunk)
        if len(buf) > KEEP:
            buf = buf[-KEEP:]
        while pending and pending[0][0].encode() in buf:
            _, text = pending.pop(0)
            # The answer is a LINE, so it is written with its Enter: a prompt
            # reads that way, and a terminal in canonical mode hands the child
            # nothing until the newline arrives — an answer missing it would look
            # exactly like a prompt nobody answered.
            try:
                os.write(fd, (text + "\n").encode())
            except OSError:
                pass
    if timed_out:
        try:
            os.killpg(pid, signal.SIGKILL)
        except OSError:
            pass
        drain(fd, log, echo, time.monotonic() + 0.5)
    status = wait_status(pid)
    if timed_out:
        note = ("\nCHECK111-RUNNER: TIMEOUT: the run was killed after %d s\n"
                % int(o["timeout"]))
        write_out(log, echo, note.encode())
        log.close()
        return 124
    if pending:
        note = ("\nCHECK111-RUNNER: the prompt this run was to answer never "
                "appeared: %s\n" % "; ".join(p for p, _ in pending))
        write_out(log, echo, note.encode())
    log.close()
    return status


def run_plain(o, echo):
    out = open(o["out"], "wb", buffering=0)
    if o["err"] and o["err"] != o["out"]:
        errf = open(o["err"], "wb", buffering=0)
    else:
        errf = out
    with open(os.devnull, "rb") as devnull:
        proc = subprocess.Popen(o["cmd"], stdin=devnull, stdout=out,
                                stderr=errf, start_new_session=True)
    try:
        status = proc.wait(timeout=o["timeout"])
    except subprocess.TimeoutExpired:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except OSError:
            pass
        proc.wait()
        note = ("\nCHECK111-RUNNER: TIMEOUT: the run was killed after %d s\n"
                % int(o["timeout"])).encode()
        write_out(errf if errf is not out else out, echo, note)
        status = 124
    out.close()
    if errf is not out:
        errf.close()
    return status


def main():
    o = parse(sys.argv[1:])
    if not o["mode"] or not o["kind"] or not o["out"] or not o["cmd"]:
        sys.stderr.write("run.py: --mode, --kind, --out and a command are required\n")
        return 2
    if len(o["expect"]) != len(o["answer"]):
        sys.stderr.write("run.py: every --expect needs an --answer\n")
        return 2
    echo = None
    if o["echo"] is not None:
        try:
            os.fstat(o["echo"])
            echo = o["echo"]
        except OSError:
            echo = None
    if o["kind"] == "pty":
        return run_pty(o, echo)
    if o["kind"] == "plain":
        return run_plain(o, echo)
    sys.stderr.write("run.py: --kind is pty or plain, not %r\n" % o["kind"])
    return 2


sys.exit(main())
RUNNER_EOF
  chmod 700 "$RUNNER"
}

# run_pty MODE [PROMPT ANSWER]... -- CMD...: a TYPED run, so the wizard sees a
# real terminal (canType) and its prompt is answered where the mode expects it.
# The transcript carries the prompts, the answers and the echo, and is echoed to
# you live.
run_pty() {
  local m=$1
  shift
  local answers=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    answers+=(--expect "$1" --answer "$2")
    shift 2
  done
  if [ $# -gt 0 ]; then shift; fi
  say "mode $m: at a real terminal (a pty). running: $*"
  LAST_STATUS=0
  python3 "$RUNNER" --mode "$m" --kind pty --echo 4 --timeout "$RUN_SECONDS" \
    --out "$SCRATCH/mode-$m.log" ${answers[@]+"${answers[@]}"} -- "$@" || LAST_STATUS=$?
}

# run_plain MODE OUTFILE [ERRFILE] -- CMD...: an UNTYPED run — no pty at all,
# standard input at /dev/null — which is the whole of what modes 3, 4 and 5 are
# about. No ERRFILE merges both streams into OUTFILE, which is what mode 3's own
# line and mode 5's do; mode 4 keeps them apart because --json's document is
# stdout.
run_plain() {
  local m=$1 out=$2 err=${3:-}
  shift 3
  [ "${1:-}" = "--" ] && shift
  local extra=()
  [ -z "$err" ] || extra=(--err "$err")
  say "mode $m: with NO terminal (standard input at /dev/null). running: $*"
  LAST_STATUS=0
  python3 "$RUNNER" --mode "$m" --kind plain --timeout "$RUN_SECONDS" --out "$out" \
    ${extra[@]+"${extra[@]}"} -- "$@" || LAST_STATUS=$?
}

# mode_exit MODE WHAT FILE...: the run's own row. A capture that is not there
# means the mode did not run, and a mode that hit the cap means it blocked waiting
# for input it cannot get — neither is a pass, and both are FAILs rather than a
# verdict about the picker.
mode_exit() {
  local m=$1 what=$2
  shift 2
  local f empty=no
  for f in "$@"; do [ -s "$f" ] || empty=yes; done
  if [ "$empty" = yes ]; then
    fact "run-$m" FAIL "no capture: $* is missing or empty, so mode $m was not run — a mode with no capture is not a pass"
    return 0
  fi
  if [ "$LAST_STATUS" -eq 124 ] || grep -q 'CHECK111-RUNNER: TIMEOUT' "$1"; then
    fact "run-$m" FAIL "the run did not finish in ${RUN_SECONDS}s and was killed; $what. A run that blocks waiting for input it cannot get is a failure of this mode — the transcript is $1"
    return 0
  fi
  local note=""
  if grep -q 'CHECK111-RUNNER: the prompt this run was to answer never appeared' "$1"; then
    note="; the prompt this mode was to answer never appeared, so whatever was read instead was NOT the answer this mode is about"
  fi
  fact "run-$m" INFO "exit $LAST_STATUS — 0, 1 and 2 are all ordinary here, since these runs end left-for-you; $what; transcript $1$note"
}

# ---------------------------------------------------------------- the readings
first_line() { # first_line FILE NEEDLE — the first line carrying NEEDLE, or nothing
  grep -m1 -F -- "$2" "$1" 2>/dev/null | tr -d '\r' | cut -c1-400 || true
}

count_lines() { # count_lines FILE ERE
  local n
  n=$(grep -c -E -- "$2" "$1" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}

# api_enabled_count PROJECT: the Cloud Billing API's own state in one project,
# the read the check's assertion block makes — or "unreadable", which is never a
# pass: a read that failed says nothing about the API's state.
api_enabled_count() { # api_enabled_count PROJECT
  local out rc
  out=$(gcloud services list --enabled --project="$1" \
    --filter='config.name:cloudbilling.googleapis.com' \
    --format='value(config.name)' 2>"$SCRATCH/api.err") || rc=$?
  if [ "${rc:-0}" -ne 0 ]; then
    printf 'unreadable'
    return 0
  fi
  printf '%s' "$(printf '%s\n' "$out" | grep -c . || true)"
}

expected_count_p() {
  [ "$DISABLED_P" = yes ] && {
    printf '0'
    return 0
  }
  printf '%s' "${API_P_BEFORE:-unknown}"
}

expected_count_q() {
  [ "$DISABLED_Q" = yes ] && {
    printf '0'
    return 0
  }
  printf '%s' "${API_Q_BEFORE:-unknown}"
}

# ---------------------------------------------------------------- the assertions
# assert_line MODE FILE...: criterion 1 — the guided `gcloud billing projects
# link` line reached you. Every failure mode ends in it, and a mode whose capture
# carries none of it fails, whatever else it printed.
assert_line() {
  local m=$1
  shift
  local f found=""
  for f in "$@"; do
    [ -s "$f" ] || continue
    found=$(first_line "$f" 'gcloud billing projects link')
    [ -z "$found" ] || break
  done
  if [ -n "$found" ]; then
    fact "line-$m" PASS "the guided line reached you: $found"
  else
    fact "line-$m" FAIL "no 'gcloud billing projects link' line in the capture — criterion 1 fails: the guided line, with how to find the account's id, is what every failure mode must end in (the words 'billing accounts' that may be there are not the leak: the hint names the gcloud command on purpose)"
  fi
}

# line_info MODE FILE...: the same read for a rerun the check's Passes-when does
# not name (mode 1(a) under --yes, mode 5 under --yes): recorded, not a criterion.
line_info() {
  local m=$1
  shift
  local f found=""
  for f in "$@"; do
    [ -s "$f" ] || continue
    found=$(first_line "$f" 'gcloud billing projects link')
    [ -z "$found" ] || break
  done
  if [ -n "$found" ]; then
    fact "line-$m" INFO "the guided line also came here: $found"
  else
    fact "line-$m" INFO "no guided line here (this rerun is not one of the seven runs the check's Passes-when names: what it records is the offer's absence, or the needs-you line)"
  fi
}

# assert_api MODE: criterion 2 — no API's state changed. P is read in every mode:
# no mode here enables an API in it, and where mode 1(a) disabled it itself (its
# own premise) the Restore enables it again, so the expected count is the one this
# run's own baseline and its own disable give. The mode's OWN extra project is
# read with it, keyed on the mode — the way the register's own assertion block
# keys it — rather than on an argument a call site could leave out: Q in mode
# 1(b), whose premise is Q's API being off and which the run may not enable, and
# S in mode 2, which must still read 1 because an S whose API is off would take
# mode 1's path and prove nothing.
assert_api() {
  local m=$1
  local n want ok=yes detail="" other=""
  case "$m" in
  1b) other=Q ;;
  2a | 2b) other=S ;;
  esac
  n=$(api_enabled_count "$P")
  if [ "$n" = unreadable ]; then
    ok=no
    detail="P's cloudbilling state could not be read (gcloud services list failed: $(first_line "$SCRATCH/api.err" 'ERROR'))"
  else
    want=$(expected_count_p)
    if [ "$n" = "$want" ]; then
      detail="P cloudbilling enabled-count $n (must read $want)"
    else
      ok=no
      detail="P cloudbilling enabled-count $n, must read $want — an API's state changed in P"
    fi
  fi
  case "$other" in
  Q)
    n=$(api_enabled_count "$Q")
    if [ "$n" = unreadable ]; then
      ok=no
      detail="$detail; Q's cloudbilling state could not be read"
    else
      want=$(expected_count_q)
      if [ "$n" = "$want" ]; then
        detail="$detail; Q cloudbilling enabled-count $n (must read $want: mode 1(b)'s premise, and the run may not enable it)"
      else
        ok=no
        detail="$detail; Q cloudbilling enabled-count $n, must read $want — mode 1(b)'s premise, and the run may not change it"
      fi
    fi
    ;;
  S)
    n=$(api_enabled_count "$S")
    if [ "$n" = unreadable ]; then
      ok=no
      detail="$detail; S's cloudbilling state could not be read"
    elif [ "$n" = 1 ]; then
      detail="$detail; S cloudbilling enabled-count $n (must read 1: mode 2's premise is an API that IS enabled, and nothing may disable it either)"
    else
      ok=no
      detail="$detail; S cloudbilling enabled-count $n, must read 1 — mode 2's premise failed, so record the mode as not run rather than as a pass"
    fi
    ;;
  esac
  if [ "$ok" = yes ]; then
    fact "api-$m" PASS "$detail"
  else
    fact "api-$m" FAIL "$detail"
  fi
}

# assert_leak MODE [may-list]: criterion 3 — no account list anywhere. The API's
# own resource form, the ids you wrote down if you wrote any, your accounts'
# display names, and any numbered list. Mode 2(a) is exempt and has to be: D6's
# picker shows each account's display name and id, so the fallback's own answer
# carries the ids, and a FAIL there would be a FAIL on the mode working as
# designed.
assert_leak() {
  local m=$1 may=${2:-}
  if [ "$may" = may-list ]; then
    fact "leak-$m" INFO "exempt, by design: this is the ONE run in the check where the gcloud fallback answers with a list — ids and display names included — and mode 2(a)'s own rows record which source answered and how many rows it listed"
    return 0
  fi
  local files=() f found="" n numbered=0
  case "$m" in
  4) files=("$SCRATCH/mode-4.json" "$SCRATCH/mode-4.err") ;;
  *) files=("$SCRATCH/mode-$m.log") ;;
  esac
  n=""
  for f in "${files[@]}"; do
    [ -s "$f" ] || continue
    n="$n$(grep -ohE 'billingAccounts/[A-Za-z0-9_-]+' "$f" 2>/dev/null || true)"$'\n'
  done
  n=$(printf '%s\n' "$n" | sort -u | grep . || true)
  [ -z "$n" ] || found="an account resource id: $(printf '%s' "$n" | tr '\n' ' ')"
  if [ -n "$IDS_FILE" ] && [ -s "$IDS_FILE" ]; then
    for f in "${files[@]}"; do
      [ -s "$f" ] || continue
      n=$(grep -ohFf "$IDS_FILE" "$f" 2>/dev/null | sort -u | tr '\n' ' ' || true)
      [ -z "$n" ] || found="${found:+$found; }one of YOUR account ids: $n"
    done
  fi
  for f in "${files[@]}"; do
    [ -s "$f" ] || continue
    n=$(grep -ohFf "$NAMES_FILE" "$f" 2>/dev/null | sort -u | tr '\n' ' ' || true)
    [ -z "$n" ] || found="${found:+$found; }a display name of yours: $n"
  done
  for f in "${files[@]}"; do
    [ -s "$f" ] || continue
    numbered=$((numbered + $(count_lines "$f" '^[[:space:]]*(\[[0-9]+\]|[0-9]+[\).:])')))
  done
  if [ -n "$found" ]; then
    fact "leak-$m" FAIL "a leak: $found — no display name of yours, no account id and no account list may appear in any mode but 2(a)"
  elif [ "${numbered:-0}" -gt 0 ]; then
    fact "leak-$m" FAIL "the capture carries $numbered numbered line(s) matching a list. A count above 0 is a numbered line and not necessarily an account list — the picker's own list reads '<n>. <display name>' or '<n>) …' — so look at the matching lines in ${files[*]} and say so in this FACT with the lines pasted"
  else
    fact "leak-$m" PASS "no account resource id, no id of yours, no display name of yours and no numbered list in ${files[*]} (the words 'billing accounts' are not a leak: the guided line's hint names 'gcloud billing accounts list' on purpose)"
  fi
}

# assert_no_picker MODE: the picker's own prompt never appeared. canType() is the
# four conditions of Global Constraint 8, and where it is false nothing may be
# listed and nothing may be asked. Mode 4 is the one mode whose capture is TWO
# files — its document on stdout and everything else on stderr — so this reads
# the same per-mode list assert_leak does, which is the list the register's own
# block reads: a prompt in either file is a prompt. A file that is not there is
# not a reading: mode_exit has already refused such a mode, and this row says the
# same thing rather than a PASS about a criterion nothing was read for.
assert_no_picker() {
  local m=$1 files=() f line found="" files_read=0
  case "$m" in
  4) files=("$SCRATCH/mode-4.json" "$SCRATCH/mode-4.err") ;;
  *) files=("$SCRATCH/mode-$m.log") ;;
  esac
  for f in "${files[@]}"; do
    [ -s "$f" ] || continue
    files_read=$((files_read + 1))
    line=$(first_line "$f" 'Billing account (a number, or')
    [ -z "$line" ] || { found="$f: $line"; break; }
  done
  if [ -n "$found" ]; then
    fact "picker-$m" FAIL "the picker ran where it must not: $found"
  elif [ "$files_read" -eq 0 ]; then
    fact "picker-$m" FAIL "nothing was read: ${files[*]} is missing or empty, and a criterion nothing was read for is not a pass"
  else
    fact "picker-$m" PASS "the picker never ran: neither its prompt nor its list appeared in ${files[*]}, so no run that cannot answer was asked to"
  fi
}

# assert_mode MODE [may-list]: the check's own assertion block, against THAT
# mode's capture. A mode whose capture is missing is never read as a pass.
assert_mode() {
  local m=$1 may=${2:-}
  assert_line "$m" "$SCRATCH/mode-$m.log"
  assert_api "$m"
  assert_leak "$m" "$may"
}

# ------------------------------------------------------------------- the steps
# setup_args is built once in main: every mode runs the wizard with --create-project
# (P has a project config, and without the flag the run registers no project stage
# at all, so the guided line every mode asserts would never print) and with
# --gcp-project P (the id the wizard offers, fugaro-fugaro, is another project).
disable_api() { # disable_api PROJECT WHY
  local out rc
  out=$(gcloud services disable cloudbilling.googleapis.com --project="$1" --force 2>&1) || rc=$?
  if [ "${rc:-0}" -ne 0 ]; then
    say "$out"
    stop "this check could not disable cloudbilling.googleapis.com on $1 ($2), so the mode was not run — its premise is that API being OFF there. The Restore still runs, and it enables nothing it did not disable"
  fi
  say "disabled cloudbilling.googleapis.com on $1 ($2); the Restore enables it again"
}

enable_api() { # enable_api PROJECT — the Restore's own command, status only
  gcloud services enable cloudbilling.googleapis.com --project="$1" >"$SCRATCH/enable.out" 2>&1
}

quota_set() { # quota_set PROJECT — points your ADC's quota project; status only
  gcloud auth application-default set-quota-project "$1" >"$SCRATCH/quota.out" 2>&1
}

# write_maimed() creates the maimed gcloud the two "neither source can answer"
# modes need: it fails at `billing accounts` — exactly as gcloud does for a
# caller without billing.accounts.list — and passes every other call through to
# the real gcloud, unchanged. The real gcloud's path is baked in here, BEFORE any
# maim, because the maimed wrapper sits first on the child's PATH and a lookup
# from inside it could find itself.
write_maimed() {
  REAL_GCLOUD=$(command -v gcloud) || die "Check 111 cannot find the real gcloud to pass the maimed wrapper's other calls to"
  mkdir -p "$SCRATCH/bin"
  cat >"$SCRATCH/bin/gcloud" <<WRAPPER_EOF
#!/usr/bin/env bash
# Check 111's maimed gcloud, made by check111.sh in its own scratch directory.
# It answers 'billing accounts list' the way gcloud does when the caller has no
# billing.accounts.list permission, and is the real gcloud for everything else.
if [ "\${1:-}" = billing ] && [ "\${2:-}" = accounts ]; then
  echo "ERROR: (gcloud.billing.accounts.list) The caller does not have permission" >&2
  exit 1
fi
exec "$REAL_GCLOUD" "\$@"
WRAPPER_EOF
  chmod 700 "$SCRATCH/bin/gcloud"
}

maim_fallback() {
  PATH="$SCRATCH/bin:$PATH"
  export PATH
  say "the gcloud fallback is maimed for this mode: $SCRATCH/bin/gcloud fails at 'billing accounts' and passes everything else to $REAL_GCLOUD"
}

unmaim() {
  case ":$PATH:" in
  *":$SCRATCH/bin:"*)
    PATH=${PATH//"$SCRATCH/bin:"/}
    export PATH
    say "the gcloud fallback is un-maimed"
    ;;
  esac
}

step_identity() {
  local v os when
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  os=$(uname -srm 2>/dev/null || true)
  when=$(date -u '+%Y-%m-%d %H:%M UTC' 2>/dev/null || true)
  fact fugaro-version PASS "${v:-fugaro version printed nothing} — $os, $when (this check needs a release build that carries the picker: 0.10.0 or later)"
}

# step_precondition: the state of P every mode's criterion 1 depends on — the
# project stage prints its guided line only for a project that exists, is ACTIVE,
# carries Firebase and has no billing link. Firebase itself is not read here: no
# gcloud call reads Firebase Management's projects.get, and the evidence for it is
# the run's own guided line, which the stage prints only when all four hold.
step_precondition() {
  local desc pid state label bill
  desc=$(gcloud projects describe "$P" --format='value(projectId,lifecycleState,labels.fugaro_created)' 2>"$SCRATCH/api.err") || {
    say "$(first_line "$SCRATCH/api.err" 'ERROR')"
    stop "P=$P could not be described by gcloud, so this check cannot tell whether it is the project \`fugaro setup\` created. Nothing was run"
  }
  pid=""
  state=""
  label=""
  IFS=$'\t' read -r pid state label <<<"$desc" || true
  [ "$pid" = "$P" ] || stop "gcloud projects describe $P answered about '$pid', so this check cannot tell what it just read. Nothing was run"
  [ "$state" = ACTIVE ] || stop "P=$P is $state, not ACTIVE: the project stage prints its guided line only for a project that is there and ACTIVE, which every mode's criterion 1 reads. Nothing was run"
  [ "$label" = true ] || stop "P=$P does not carry the label fugaro_created=true (it reads '${label:-none}'), so it was not created by \`fugaro setup\` and a run refuses it ('Fugaro only starts fresh'). Nothing was run"
  bill=$(gcloud billing projects describe "$P" --format='value(billingEnabled)' 2>"$SCRATCH/api.err") || {
    say "$(first_line "$SCRATCH/api.err" 'ERROR')"
    stop "P=$P's billing link could not be read with 'gcloud billing projects describe', and every mode needs P to have NO link. Nothing was run"
  }
  if [ "$bill" = true ]; then
    stop "P=$P has a billing account linked (billingEnabled=true): the project stage reports it done and prints the guided line nowhere, so criterion 1 could not be read. Unlink it (gcloud billing projects unlink $P) or use another throwaway. Nothing was run"
  fi
  API_P_BEFORE=$(api_enabled_count "$P")
  [ "$API_P_BEFORE" != unreadable ] || stop "P=$P's cloudbilling state could not be read, and every mode's criterion 2 is about that state. Nothing was run"
  if [ -n "$Q" ]; then
    API_Q_BEFORE=$(api_enabled_count "$Q")
    [ "$API_Q_BEFORE" != unreadable ] || stop "Q=$Q's cloudbilling state could not be read. Nothing was run"
  fi
  if [ -n "$S" ]; then
    API_S_BEFORE=$(api_enabled_count "$S")
    [ "$API_S_BEFORE" != unreadable ] || stop "S=$S's cloudbilling state could not be read. Nothing was run"
  fi
  fact precondition PASS "P=$P exists, is ACTIVE and carries fugaro_created=true; its billing link is not enabled; the API baselines: P cloudbilling enabled-count $API_P_BEFORE${Q:+, Q $API_Q_BEFORE}${S:+, S $API_S_BEFORE}. Firebase is not read with a gcloud call — no such call reads Firebase Management — and the run's own guided line is its evidence, since the stage prints it only for a project that exists, is ACTIVE, has Firebase and has no billing link"
}

step_1a() {
  say "mode 1(a): the Cloud Billing API is off on P, and P is the quota project — the run may offer to enable it, and the offer is declined"
  disable_api "$P" "mode 1(a)'s premise"
  DISABLED_P=yes
  quota_set "$P" || stop "this check could not point your ADC's quota project at $P, which is mode 1(a)'s premise. Nothing was run"
  QUOTA_POINTED=$P
  maim_fallback
  run_pty 1a 'to enable it, or press Enter' '' -- fugaro "${SETUP_ARGS[@]}"
  mode_exit 1a "the wizard at a real terminal, answered with one Enter (the offer declined)" "$SCRATCH/mode-1a.log"
  assert_offer 1a
  assert_mode 1a
  say "mode 1(a) again under --yes: the picker never runs there, so no offer may come"
  run_pty 1a-yes -- fugaro "${SETUP_ARGS[@]}" --yes
  mode_exit 1a-yes "the same line with --yes at a terminal" "$SCRATCH/mode-1a-yes.log"
  assert_offer_absent 1a-yes
  assert_no_picker 1a-yes
  line_info 1a-yes "$SCRATCH/mode-1a-yes.log"
  unmaim
}

assert_offer() { # assert_offer MODE — D3(a)'s typed offer, and the decline
  local m=$1 offer left
  offer=$(first_line "$SCRATCH/mode-$m.log" 'CONFIRM: ENABLE the Cloud Billing API')
  left=$(first_line "$SCRATCH/mode-$m.log" 'Nothing was enabled. Run:')
  if [ -n "$offer" ] && [ -n "$left" ]; then
    fact "offer-$m" PASS "the typed offer came and was declined: $offer | $left"
  elif [ -n "$offer" ]; then
    fact "offer-$m" FAIL "the offer came but the capture says nothing about where it left you (no 'Nothing was enabled. Run:' line): the run has to hand you the enable line when the offer is declined: $offer"
  else
    fact "offer-$m" FAIL "no 'CONFIRM: ENABLE the Cloud Billing API' offer in the capture. Mode 1(a)'s premise is the API off on the quota project that IS the project being set up, and the typed offer is what D3(a) is — without it this mode proves nothing"
  fi
}

assert_offer_absent() { # assert_offer_absent MODE
  local m=$1 offer
  offer=$(first_line "$SCRATCH/mode-$m.log" 'CONFIRM: ENABLE the Cloud Billing API')
  if [ -z "$offer" ]; then
    fact "offer-$m" PASS "no offer at all: --yes is a run that asked for no question, so the picker never runs and nothing is offered — and --yes must never stand in for the typed answer anyway"
  else
    fact "offer-$m" FAIL "an offer appeared under --yes: the picker must not run at all there, and --yes never covers the typed enable: $offer"
  fi
}

assert_no_offer() { # assert_no_offer MODE — a refusal must not offer
  local m=$1 offer
  offer=$(first_line "$SCRATCH/mode-$m.log" 'CONFIRM: ENABLE the Cloud Billing API')
  if [ -z "$offer" ]; then
    fact "offer-absent-$m" PASS "no offer: this mode refuses instead, which is what it is for — only mode 1(a) may offer to enable an API"
  else
    fact "offer-absent-$m" FAIL "this mode offered to enable an API where it must refuse and name the line and who to ask: $offer"
  fi
}

step_1b() {
  if [ -z "$Q" ]; then
    not_run 1b "no --api-off-project was given: mode 1(b)'s premise is a SECOND throwaway of yours whose Cloud Billing API is off, and an unset one must stop the mode, never silently run mode 1(a)'s premise"
    return 0
  fi
  say "mode 1(b): the API is off on Q and Q is the quota project — the run must REFUSE, naming the line and who to ask"
  disable_api "$Q" "mode 1(b)'s premise"
  DISABLED_Q=yes
  quota_set "$Q" || stop "this check could not point your ADC's quota project at $Q, which is mode 1(b)'s premise. Nothing was run"
  QUOTA_POINTED=$Q
  maim_fallback
  run_pty 1b -- fugaro "${SETUP_ARGS[@]}"
  mode_exit 1b "the wizard at a real terminal with neither source able to answer" "$SCRATCH/mode-1b.log"
  assert_refusal 1b
  assert_no_offer 1b
  assert_mode 1b
  unmaim
}

assert_refusal() { # assert_refusal MODE
  local m=$1 refusal line quota
  refusal=$(first_line "$SCRATCH/mode-$m.log" 'is disabled on')
  line=$(first_line "$SCRATCH/mode-$m.log" 'gcloud services enable cloudbilling.googleapis.com')
  quota=$(first_line "$SCRATCH/mode-$m.log" 'quota project of your credentials')
  if [ -n "$refusal" ] && [ -n "$line" ] && [ -n "$quota" ]; then
    fact "refusal-$m" PASS "the refusal names the quota project, the exact enable line and who to ask: $refusal | $line"
  else
    fact "refusal-$m" FAIL "mode 1(b) must REFUSE — never offer — and name the quota project, the exact 'gcloud services enable cloudbilling.googleapis.com --project $Q' line and who to ask. The capture carries: $( [ -n "$refusal" ] && echo "a refusal ($refusal)" || echo 'no refusal line at all')"
  fi
}

not_run() { # not_run MODE REASON — a mode the run did not select or could not run:
  # its own row and every one of its criteria's rows read FAIL — not run, so that
  # a block from a partial run can never read as a pass about the picker. The
  # keys come from mode_keys, never from the caller: a mode's rows are its own.
  local m=$1 k
  fact "run-$m" FAIL "not run: $2"
  # shellcheck disable=SC2046 # word splitting is the point: mode_keys prints the key list
  for k in $(mode_keys "$m"); do
    fact "$k" FAIL "not run: this mode's criteria were never read"
  done
}

step_2a() {
  if [ -z "$S" ]; then
    not_run 2a "no --api-on-project was given: mode 2's premise is a THIRD throwaway of yours whose Cloud Billing API is ON"
    return 0
  fi
  if [ -z "$SECOND_ACCOUNT" ]; then
    not_run 2a "no --second-account was given: mode 2(a) is the one run where the gcloud fallback must be able to answer, and it answers with YOUR OWN gcloud login — so ADC has to be logged in as a SECOND account that lacks billing.accounts.list. See the manual row"
    return 0
  fi
  if ! s_enabled 2a; then return 0; fi
  adc_swap_in
  say "mode 2(a): the API is ON on S, ADC is $SECOND_ACCOUNT and lacks the permission — the fallback answers, and a list here is the point"
  unmaim
  quota_set "$S" || stop "this check could not point your ADC's quota project at $S, which is mode 2's premise. Nothing was run"
  QUOTA_POINTED=$S
  run_pty 2a 'Billing account (a number, or' 'none' -- fugaro "${SETUP_ARGS[@]}"
  mode_exit 2a "the wizard at a real terminal, answered 'none' at the picker's prompt (the picker's own way out: it records no account and leaves --link-billing empty)" "$SCRATCH/mode-2a.log"
  assert_source_2a
  assert_mode 2a may-list
  adc_swap_out_after 2a
}

s_enabled() { # s_enabled MODE — S's API must be ON before the run, or the mode is
  # mode 1's path and proves nothing. Recorded as not run, which is what the
  # check's Fails-when says a premise that failed is — never a pass.
  local m=$1 n
  n=$(api_enabled_count "$S")
  if [ "$n" = 1 ]; then
    fact "premise-$m" PASS "S=$S has cloudbilling.googleapis.com ENABLED before the run, which is what mode 2 needs"
    return 0
  fi
  not_run "$m" "S=$S reads cloudbilling enabled-count ${n:-nothing} and mode 2 needs 1: with the API off on S the run takes mode 1's disabled-API path and proves nothing. Enable it on S and run again"
  return 1
}

assert_source_2a() {
  local line rows
  line=$(first_line "$SCRATCH/mode-2a.log" 'Billing accounts you can link')
  rows=$(count_lines "$SCRATCH/mode-2a.log" '^[[:space:]]*[0-9]+\) ')
  if [ -z "$line" ]; then
    fact source-2a FAIL "no list appeared: mode 2(a)'s premise is a caller without billing.accounts.list whose gcloud login still has it, so the picker must fall back to 'gcloud billing accounts list' and list what it returns. A run where neither source answered is mode 2(b)'s premise, not this one"
    fact list-2a FAIL "not read: there was no list to record"
    return 0
  fi
  case "$line" in
  *'from gcloud billing accounts list'*)
    fact source-2a PASS "the FALLBACK answered and the picker says so: $line"
    ;;
  *'from the Cloud Billing API'*)
    fact source-2a FAIL "the API answered, not the fallback: mode 2(a)'s premise is a caller that LACKS billing.accounts.list (ADC logged in as $SECOND_ACCOUNT), and the API answering means the permission is still there — record the mode as not run, not as a pass: $line"
    ;;
  *)
    fact source-2a FAIL "the list came but nothing says which source answered it: $line"
    ;;
  esac
  if [ "${rows:-0}" -gt 0 ]; then
    fact list-2a PASS "$rows account row(s) listed through the fallback — recorded, not failed: D6's picker shows each account's display name and id, and this block deliberately does not repeat them (they were on your screen as the run came past; the transcript is not kept). Say in the register what it listed"
  else
    fact list-2a INFO "the fallback answered with no account row at all (an empty list is a legitimate answer, and the picker then prints the guided line)"
  fi
}

step_2b() {
  if [ -z "$S" ]; then
    not_run 2b "no --api-on-project was given: mode 2's premise is a THIRD throwaway of yours whose Cloud Billing API is ON"
    return 0
  fi
  if [ -z "$SECOND_ACCOUNT" ] && [ "$ROLE_REMOVED" != yes ]; then
    not_run 2b "neither --second-account nor --role-removed was given: mode 2 needs a caller without billing.accounts.list, which takes a browser or your own IAM — no script may do it for you. See the manual row"
    return 0
  fi
  if ! s_enabled 2b; then return 0; fi
  adc_swap_in
  say "mode 2(b): the API refuses (403) and the fallback is maimed — the run must print the guided line"
  maim_fallback
  quota_set "$S" || stop "this check could not point your ADC's quota project at $S, which is mode 2's premise. Nothing was run"
  QUOTA_POINTED=$S
  run_pty 2b -- fugaro "${SETUP_ARGS[@]}"
  mode_exit 2b "the wizard at a real terminal with the API refusing and the fallback maimed ($([ "$ROLE_REMOVED" = yes ] && echo "the billing role was removed from your own account, so both sources refuse" || echo "ADC is $SECOND_ACCOUNT, which lacks the permission")); route: $([ "$ROLE_REMOVED" = yes ] && echo role-removed || echo second-account)" "$SCRATCH/mode-2b.log"
  assert_unlisted 2b
  assert_mode 2b
  unmaim
  adc_swap_out_after 2b
}

# assert_unlisted MODE: what the run must say when neither source answered — one
# value-free line, the guided line, and never a raw API error as its only output.
assert_unlisted() {
  local m=$1 line guided
  line=$(first_line "$SCRATCH/mode-$m.log" 'Billing accounts could not be listed')
  guided=$(first_line "$SCRATCH/mode-$m.log" 'gcloud billing projects link')
  if [ -n "$line" ]; then
    fact "unlisted-$m" PASS "which source answered: NEITHER — the run's own line: $line"
  elif [ -n "$guided" ]; then
    fact "unlisted-$m" INFO "the capture carries no 'Billing accounts could not be listed' line, and does carry the guided line, so the run did not crash and did not claim an account exists — read them together"
  else
    fact "unlisted-$m" FAIL "neither the value-free 'Billing accounts could not be listed' line nor the guided line is in the capture: mode 2(b)'s premise is that NO source answers, and a run that prints a raw API error, crashes or claims an account exists instead of the guided line is what this mode exists to catch"
  fi
}

step_3() {
  say "mode 3: NO TTY at all — the picker must never run, and the run must not wait for input"
  run_plain 3 "$SCRATCH/mode-3.log" "" -- env -u CLAUDE_CODE_SSE_PORT fugaro "${SETUP_ARGS[@]}"
  mode_exit 3 "the same line with standard input at /dev/null and no pty — the same case as a pipe, a redirect or --non-interactive" "$SCRATCH/mode-3.log"
  assert_no_picker 3
  assert_line 3 "$SCRATCH/mode-3.log"
  assert_api 3
  assert_leak 3
}

step_4() {
  say "mode 4: --json — the picker never runs, and no account list may be in the document"
  run_plain 4 "$SCRATCH/mode-4.json" "$SCRATCH/mode-4.err" -- env -u CLAUDE_CODE_SSE_PORT fugaro "${SETUP_ARGS[@]}" --json
  mode_exit 4 "the same line with --json, its stdout kept in mode-4.json and its stderr in mode-4.err (the two are kept apart because --json's document is stdout)" "$SCRATCH/mode-4.json" "$SCRATCH/mode-4.err"
  assert_no_picker 4
  assert_document 4
  assert_line 4 "$SCRATCH/mode-4.err" "$SCRATCH/mode-4.json"
  assert_api 4
  assert_leak 4
}

assert_document() { # assert_document MODE — the check's own jq line
  local m=$1 got ids
  got=$(jq -e '[.. | strings | select(test("billingAccounts/"))] | length == 0' "$SCRATCH/mode-$m.json" 2>"$SCRATCH/jq.err" || true)
  ids=$(grep -ohE 'billingAccounts/[A-Za-z0-9_-]+' "$SCRATCH/mode-$m.err" 2>/dev/null | sort -u | tr '\n' ' ' || true)
  if [ "$got" = true ] && [ -z "$ids" ]; then
    fact "document-$m" PASS "the check's own jq line printed true: no billingAccounts/<id> in the document, and none on stderr either"
  else
    fact "document-$m" FAIL "the jq line printed '${got:-nothing}'$( [ -s "$SCRATCH/jq.err" ] && echo " (jq said: $(first_line "$SCRATCH/jq.err" ''))")${ids:+; stderr carries an account resource id: $ids} — the picker must never list anything under --json"
  fi
}

step_5() {
  say "mode 5: an agent's session — AI_AGENT=1 in this one child's environment alone, never in this shell"
  run_plain 5 "$SCRATCH/mode-5.log" "" -- env AI_AGENT=1 fugaro "${SETUP_ARGS[@]}"
  mode_exit 5 "the same line with AI_AGENT=1 set by hand for the child alone, no terminal" "$SCRATCH/mode-5.log"
  assert_no_picker 5
  assert_needsyou 5
  assert_line 5 "$SCRATCH/mode-5.log"
  assert_api 5
  assert_leak 5
  say "mode 5 again under --yes: an agent's session applies nothing, --yes or not"
  run_plain 5-yes "$SCRATCH/mode-5-yes.log" "" -- env AI_AGENT=1 fugaro "${SETUP_ARGS[@]}" --yes
  mode_exit 5-yes "the same line with --yes: it must still apply nothing and still leave the step for you" "$SCRATCH/mode-5-yes.log"
  assert_needsyou 5-yes
  line_info 5-yes "$SCRATCH/mode-5-yes.log"
}

assert_needsyou() { # assert_needsyou MODE
  local m=$1 line
  line=$(first_line "$SCRATCH/mode-$m.log" 'run this in your own terminal window')
  if [ -n "$line" ]; then
    fact "needsyou-$m" PASS "the needs-you line: $line"
  else
    fact "needsyou-$m" FAIL "no 'run this in your own terminal window' line in the capture: an applying stage in a coding agent's session must be left for you, --yes included, with the needs-you wording internal/initflow prints (AgentAdvice)"
  fi
}

# ------------------------------------------------------------------- the Restore
# restore_run: everything this check did outside its own scratch directory is
# undone here, and it runs from the exit trap whatever ended the run — a FAILed
# assertion, a Ctrl-C, a stop, an internal error. It touches nothing it did not
# change: an API is enabled again only where a mode disabled it, and your ADC's
# quota project is put back only where a mode repointed it.
restore_run() {
  local notes="" ok=yes now
  if [ "$DISABLED_P" = yes ]; then
    if enable_api "$P"; then
      notes="cloudbilling.googleapis.com enabled again on $P"
    else
      ok=no
      notes="cloudbilling.googleapis.com could NOT be enabled again on $P — run: gcloud services enable cloudbilling.googleapis.com --project $P"
    fi
  fi
  if [ "$DISABLED_Q" = yes ]; then
    if enable_api "$Q"; then
      notes="${notes:+$notes; }cloudbilling.googleapis.com enabled again on $Q"
    else
      ok=no
      notes="${notes:+$notes; }cloudbilling.googleapis.com could NOT be enabled again on $Q — run: gcloud services enable cloudbilling.googleapis.com --project $Q"
    fi
  fi
  if [ "$DISABLED_P" = no ] && [ "$DISABLED_Q" = no ]; then
    notes="no API was disabled by this run, so none was enabled again"
  fi
  # The swap first, and before the quota project: putting your own file back
  # byte for byte is what makes the quota project's own restore the last word on
  # the file, and a swap left in place is the one thing here that would leave a
  # caller of yours wrong rather than a project's state.
  if [ "$SECOND_ADC_IS" = yes ]; then
    if adc_swap_out; then
      notes="$notes; $(adc_file) is your own login again, byte for byte, which is what mode 2 found there"
    else
      ok=no
      notes="$notes; your ADC is STILL the login in $SECOND_ADC and your own file could not be put back — run: gcloud auth application-default login, choosing YOUR OWN account, and keep $SECOND_ADC for the next run"
    fi
  elif [ -n "$ADC_OWN_BACKUP" ]; then
    notes="$notes; $(adc_file) is your own login, byte for byte (mode 2's second login, $SECOND_ADC, was in place for mode 2's runs alone and put back out when they were done)"
  elif [ -n "$SECOND_ADC" ]; then
    notes="$notes; mode 2's second login was never swapped in, so your ADC file is untouched"
  fi
  if [ -n "$QUOTA_POINTED" ]; then
    if quota_set "$ADC_QUOTA_ORIGINAL"; then
      notes="$notes; your ADC's quota project is back to $ADC_QUOTA_ORIGINAL"
    else
      ok=no
      notes="$notes; your ADC's quota project could NOT be put back — run: gcloud auth application-default set-quota-project $ADC_QUOTA_ORIGINAL"
    fi
  else
    notes="$notes; your ADC's quota project was never changed"
  fi
  if [ -n "$S" ] && [ "$API_S_BEFORE" = 1 ]; then
    now=$(api_enabled_count "$S")
    case "$now" in
    1) notes="$notes; S's cloudbilling API is still enabled (mode 2's premise, and nothing here may change it)" ;;
    unreadable) notes="$notes; S's cloudbilling state could not be read back" ;;
    *)
      ok=no
      notes="$notes; S's cloudbilling API reads $now and must read 1 — it was disabled, which no step of this check may do"
      ;;
    esac
  fi
  local verdict=PASS
  [ "$ok" = yes ] || verdict=FAIL
  fact restore "$verdict" "$notes; no role was removed by any step of this script (mode 2's role route is yours: its undo is in the manual row); no agent marker was ever set in this shell — mode 5's AI_AGENT went into one child's environment alone"
}

cleanup() {
  restore_run
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  # The two ADC copies mode 2's swap makes are the only credential files this
  # check ever writes, and they live exactly as long as the run: the Restore has
  # already put both files back, so the copies go even when the scratch
  # directory itself is kept, and no copy of a login outlives the run.
  rm -f "${ADC_OWN_BACKUP:-}" "${SECOND_ADC_BACKUP:-}" 2>/dev/null || true
  if [ -n "$KEEP_SCRATCH" ]; then
    say "the scratch directory is KEPT on purpose: $SCRATCH (CHECK111_KEEP_SCRATCH — the seven transcripts are in it)"
  else
    remove_scratch
  fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  # INT, TERM and HUP are NOT reset: cleanup is about to run the Restore, which
  # changes cloud state in the owner's projects, and a second Ctrl-C during it
  # would kill this shell with the enablers half done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it: a
  # block that read as a finished run would offer a row of FAILs as a verdict
  # about the picker when the modes that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the picker"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory, so there is none to remove"
  elif [ -n "$KEEP_SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|INFO|kept on purpose at $SCRATCH (CHECK111_KEEP_SCRATCH): the transcripts, the runner, the maimed gcloud wrapper and this check's config root are in it"
  elif [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|the scratch directory is gone, with the transcripts, the maimed gcloud wrapper and this check's config root in it — nothing of yours was ever installed there"
  else
    facts="${facts}"$'\n'"scratch-removed|FAIL|the scratch directory is STILL THERE, at $SCRATCH — remove it yourself: rm -rf $SCRATCH"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    if [ -n "$DRY_RAN" ]; then
      print_block "$facts" "$status" recorded
    else
      print_block "$facts" "$status"
    fi
  fi
  exit "$status"
}

# ------------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, a selected mode's keys reading FAIL —
# not reached when the run stopped before them, a mode this run did not select
# reading FAIL — not run, and any other recorded key (aborted) following. That is
# what makes the block safe to diff between two runs, and what keeps "an unrun
# mode is not a pass" visible in the block itself.
BLOCK_KEYS=(fugaro-version project api-off-project api-on-project account-names account-ids
  agent-session xtrace modes config-root adc-quota second-adc precondition
  run-1a offer-1a line-1a api-1a leak-1a
  run-1a-yes offer-1a-yes picker-1a-yes line-1a-yes
  run-1b refusal-1b offer-absent-1b line-1b api-1b leak-1b
  run-2a premise-2a source-2a list-2a line-2a api-2a leak-2a
  run-2b premise-2b unlisted-2b line-2b api-2b leak-2b
  run-3 picker-3 line-3 api-3 leak-3
  run-4 picker-4 document-4 line-4 api-4 leak-4
  run-5 picker-5 needsyou-5 line-5 api-5 leak-5
  run-5-yes needsyou-5-yes line-5-yes
  restore scratch-removed manual)

print_block() { # print_block FACTS STATUS [recorded] — always on the owner's own
  # stdout. The third argument is for a dry run: only what the run actually
  # recorded is printed, because a dry run measured no criterion and a wall of
  # "not reached" rows would read as a run that failed.
  local facts=$1 status=$2 mode=${3:-}
  local key printed="" verdict=PASS
  printf '\nCHECK 111 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    elif [ -z "$mode" ]; then
      printf 'FACT %s: FAIL — not reached (the run stopped before this step, and an unmeasured criterion is not a pass)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  if printf '%s\n' "$facts" | grep -q '|FAIL|' || [ "$status" != 0 ]; then
    verdict=FAIL
  fi
  printf 'CHECK 111 RESULT END (exit %s)\n' "$status" >&3
  if [ -n "$mode" ]; then
    printf 'CHECK 111 DRY RUN: nothing was run, so no criterion was measured and there is no PASS or FAIL to report. The plan above is what a real run would run, and the rows here are what the guards read locally.\n' >&3
  else
    printf 'CHECK 111 RESULT: %s\n' "$verdict" >&3
  fi
}

# record_manual: the ONE step no script may run for the owner, recorded first and
# carried by every block. Mode 2's premise is a caller that lacks
# billing.accounts.list, which takes a browser (a second account's login) or the
# owner's own IAM (removing a billing role from their account) — neither is
# something this check may do, and the route that removes a role is theirs to undo.
record_manual() {
  fact manual INFO "the ONE step no script runs for you, and what to paste back: mode 2 needs a caller that LACKS billing.accounts.list, which takes a browser or your own IAM — do it by hand, BEFORE this run, and pass what it made: (a) for mode 2(a), make your SECOND account's login a file and leave your machine on your own account — gcloud auth application-default login (choose the SECOND account); cp $(adc_file) /tmp/chk111-second.json; gcloud auth application-default login (choose YOUR OWN account again, so nothing of this is left behind); then grant that second account only the rank that lets the call be MADE on ${S:-<S>} (gcloud projects add-iam-policy-binding ${S:-<S>} --member=user:<SECOND> --role=roles/serviceusage.serviceUsageConsumer), and pass --second-adc /tmp/chk111-second.json --second-account <SECOND>: this check copies that login over $(adc_file) for mode 2's runs alone and puts your own file back byte for byte afterwards, and again from its exit trap whatever ends the run; (b) for mode 2(b) either the same --second-adc and --second-account, or remove the billing role from your own account (gcloud projects remove-iam-policy-binding ${S:-<S>} --member=user:<YOU> --role=<YOUR-BILLING-ROLE>) and pass --role-removed, then put the role back when you are done; if a run ever ends with your ADC still logged in as the second account (a swap that could not be put back — the restore row says so), run gcloud auth application-default login and choose your own account; then paste back, for each of the seven runs, its result, the exact guided line, the exact refusal or offer text, the exit code, which source answered in mode 2 AND which route made the caller lack the permission, and the assertion block's counts (P's and Q's must read 0, and S's must read 1 in mode 2), plus the agent marker your shell carried and whether --yes ever took mode 1(a)'s offer"
}

# -------------------------------------------------------------------- the plan
# print_plan: what a real run would run, in order, and what its Restore would undo.
# Printed by --dry-run; nothing here is run, and no gcloud call and no fugaro call
# is made by a dry run at all.
print_plan() {
  say "dry run: NOTHING below was run — no gcloud call and no fugaro call was made."
  say "  [local] the guards: the projects by name (never belong, fugaro-dev or edge-devel-dimi), the --account-names file, the --modes list, XDG_CONFIG_HOME at this check's own root, and your ADC's quota project read from $(adc_file)"
  say "  [network, reads] fugaro version; uname; gcloud projects describe $P --format=value(projectId,lifecycleState,labels.fugaro_created); gcloud billing projects describe $P --format=value(billingEnabled) — it must read false, or every mode's guided line prints nowhere"
  say "  [network, reads] the baselines: gcloud services list --enabled --project=<P|Q|S> --filter=config.name:cloudbilling.googleapis.com --format=value(config.name)"
  say "  [network, writes] mode 1(a): gcloud services disable cloudbilling.googleapis.com --project=$P --force; gcloud auth application-default set-quota-project $P; then at a REAL TERMINAL, answered with one Enter (the offer declined): fugaro ${SETUP_ARGS[*]}"
  say "  [network, writes] mode 1(a) again with --yes: fugaro ${SETUP_ARGS[*]} --yes (the picker never runs there, so no offer may come)"
  say "  [network, writes] mode 1(b): the same two gcloud calls on ${Q:-<--api-off-project, required>}, then at a real terminal: fugaro ${SETUP_ARGS[*]} with the gcloud fallback maimed — the run must REFUSE and name the enable line"
  if [ -n "$SECOND_ADC" ]; then
    say "  [local, writes] before mode 2: cp -p $(adc_file) to the scratch directory, cp -p $SECOND_ADC over $(adc_file) — mode 2's runs alone use the second account's login, and your own file is copied back byte for byte as soon as each of them is done, so the modes after mode 2 run as you, and again from the exit trap whatever ends the run. Both copies are removed with the scratch directory, never kept"
  else
    say "  [local] no --second-adc was given: mode 2's runs use $(adc_file) as it is, so that login has to be the second account's for the whole run — which only a run of modes 2a,2b alone may be"
  fi
  say "  [network, writes] mode 2(a): gcloud auth application-default set-quota-project ${S:-<--api-on-project, required>}, the fallback NOT maimed, then at a real terminal answered 'none' at the picker's prompt: fugaro ${SETUP_ARGS[*]} (the one run where a list is the point)"
  say "  [network] mode 2(b): the fallback maimed, then fugaro ${SETUP_ARGS[*]} — the API refuses and nothing can answer it"
  say "  [network] mode 3: env -u CLAUDE_CODE_SSE_PORT fugaro ${SETUP_ARGS[*]} with standard input at /dev/null and no pty (the marker is taken out of this one child: with no terminal it is what internal/agentenv.Session reads as an agent's session, which is mode 5's case and not this one)"
  say "  [network] mode 4: env -u CLAUDE_CODE_SSE_PORT fugaro ${SETUP_ARGS[*]} --json, its stdout and stderr kept apart, and the check's own jq line on the document — unset for the same reason as mode 3"
  say "  [network] mode 5: env AI_AGENT=1 fugaro ${SETUP_ARGS[*]} (the marker in that one child's environment, never in this shell), then the same line with --yes"
  say "and its Restore (also from the exit trap) would run:"
  if [ -n "$SECOND_ADC" ]; then
    say "  [local, writes] cp -p your own ADC file back over $(adc_file) — only if mode 2 swapped the second login in and could not put it back at the end of mode 2"
  fi
  say "  [network, writes] gcloud services enable cloudbilling.googleapis.com --project=$P — only if mode 1(a) disabled it"
  say "  [network, writes] gcloud services enable cloudbilling.googleapis.com --project=${Q:-<Q>} — only if mode 1(b) disabled it"
  say "  [network, writes] gcloud auth application-default set-quota-project $ADC_QUOTA_ORIGINAL — your own quota project, put back"
  say "  [network] gcloud services list --enabled --project=${S:-<S>} — S's API read back as still enabled"
  say "  [local] rm -rf $SCRATCH, with the seven transcripts, the runner and this check's config root in it (unless CHECK111_KEEP_SCRATCH=1)"
  say "and the ONE step no script may run for you is in the FACT manual row of the block below."
}

# ----------------------------------------------------------------------- main
SETUP_ARGS=()

# mode_keys MODE: the keys a mode's criteria are recorded under, so a mode that
# was not run (or not selected) still fills its own row of the block with FAIL —
# not run, rather than a verdict about a picker nothing asked.
mode_keys() {
  case "$1" in
  1a) printf 'offer-1a line-1a api-1a leak-1a offer-1a-yes picker-1a-yes line-1a-yes' ;;
  1b) printf 'refusal-1b offer-absent-1b line-1b api-1b leak-1b' ;;
  2a) printf 'premise-2a source-2a list-2a line-2a api-2a leak-2a' ;;
  2b) printf 'premise-2b unlisted-2b line-2b api-2b leak-2b' ;;
  3) printf 'picker-3 line-3 api-3 leak-3' ;;
  4) printf 'picker-4 document-4 line-4 api-4 leak-4' ;;
  5) printf 'picker-5 needsyou-5 line-5 api-5 leak-5 needsyou-5-yes line-5-yes' ;;
  esac
}

select_mode() { # select_mode MODE FN
  local m=$1 fn=$2 i
  for i in "${MODES_TO_RUN[@]}"; do
    if [ "$i" = "$m" ]; then
      "$fn"
      return 0
    fi
  done
  not_run "$m" "this run's --modes did not select it (it ran: ${MODES_TO_RUN[*]}). An unrun mode is not a pass: run the whole check"
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  write_runner
  write_maimed
  SETUP_ARGS=(setup --create-project --gcp-project "$P" --name "$N" --region "$REGION" --budget-mode observe)
  record_manual
  guard_xtrace
  guard_agent
  guard_projects
  guard_names
  guard_modes
  guard_adc
  guard_second_adc
  guard_second_account

  if [ -n "$DRY_RUN" ]; then
    # A dry run reaches its own end: it is the whole of what was asked of it, so
    # it exits 0 and its block carries no verdict — never REACHED_END=no, which
    # would report this as a run that died before its last step.
    DRY_RAN=1
    REACHED_END=yes
    print_plan
    fact dry-run PASS "the guards and the files only: no gcloud call and no fugaro call was made, and the commands a real run would run and the ones its Restore would run were printed, not run"
    return 0
  fi

  step_identity
  step_precondition
  select_mode 1a step_1a
  select_mode 1b step_1b
  select_mode 2a step_2a
  select_mode 2b step_2b
  select_mode 3 step_3
  select_mode 4 step_4
  select_mode 5 step_5

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# — including through the Restore, which is where its second answer matters. A
# second Ctrl-C during the Restore would otherwise kill this shell with the two
# API enablers and the ADC's quota project only half done.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "the Restore is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

trap on_error ERR
trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
