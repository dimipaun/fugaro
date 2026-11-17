#!/usr/bin/env bash
# Check 127 — the rigor modes on a real repository, as ONE script YOU run.
#
# It is a READER: it launches no run, opens no pull request, writes no object
# and makes no model call. What it reads is the eleven live steps of
# docs/rigor-modes/implementation-plan.md §9, every one of which needs
# something a container cannot have: a live model call, two real runs compared,
# a repository configured with a floor, the gateway's own price, a provider's
# CI, a published project layer beside two repositories, a live multi-round
# agent, a deployed rules tree, and pull requests a person reads as a diff.
#
# The runs are YOURS to launch — the check prints the exact command for each
# step whose run it was not given — and then it reads what they left: the run's
# own record (`fugaro runs ls --json`, whose `rigor` block is the mode the run
# resolved and the layer that decided it), `fugaro diagnose` (the rounds, the
# levels and the findings the mode tolerated), the config in force in each
# checkout, the review floor and the security list, the backlog's index, the
# claim the provider's CI made about a merge, and `fugaro watch`'s own frame.
#
# Usage:      bash scripts/live-checks/check127.sh --sandbox <checkout>
#             --run <slug>/<run-id> [--critical <slug>/<run-id>]
#             [--hotfix <slug>/<run-id> --path <repo-relative path>]
#             [--pr <N>] [--checkout2 <checkout> --run2 <slug>/<run-id>]
#             [--ref-run <slug>/<run-id> --ref <branch>]
#             [--areas-run <slug>/<run-id>] [--item-pr <N> --item-pr2 <N>]
#             [--recipe <name>] [--runs <N>] [--dry-run]
#             bash scripts/live-checks/check127.sh --help
#
#             --sandbox is a SANDBOX repository's checkout: the project it
#             belongs to, the repository's own name and the runs read are all
#             read there, through the installed `fugaro`, so the check never
#             takes a project from an argument.
#             --run is step 1's POC run and it is required: the mode that is
#             not the default is the one a repository has to ask for, and a
#             record that says `poc` is what proves it arrived.
#             Each other flag is one step's evidence, and a step whose flag is
#             absent is printed as one that stays yours rather than guessed at.
#             `--dry-run` prints the guards and every command a real run would
#             make, runs nothing at all — no fugaro call, no gh call — and
#             reads no verdict.
# Needs:      bash, mktemp, sed, awk, python3, git and fugaro on PATH, and `gh`
#             for the steps that read a pull request and the CI behind it
#             (steps 5, 10 and 11). Nothing is installed and nothing is
#             fetched; a missing tool stops the run with one line naming it.
#             python3 reads every JSON document: a row read with grep would
#             report a shape the release did not write the day a document is
#             re-wrapped.
# Where:      a SANDBOX repository only, and NEVER the installation or the
#             repository named `belong`: the project the checkout belongs to is
#             refused when it names it, and so is a repository, a path or a run
#             reference. It is also never run in a coding agent's session
#             (CLAUDECODE and the like are refused): this is the owner's own
#             check, and no agent runs a step of it.
# Creates:    nothing. Every call is a read, and the one command that would
#             write — `fugaro backlog index` — is run against a COPY of the
#             backlog folder in the scratch directory, so the checkout is left
#             exactly as it was found.
# Costs:      nothing billable and no model call: reads alone.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed,
#             and the `restore` row says what was created (nothing) and whether
#             the directory really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 127 RESULT,
#             one FACT row per reading, to copy back to the coordinator.
#             Exit 0 when every measured row passed and nothing was left
#             unmeasured, 1 when a measured row FAILed, and 2 when the run
#             could not tell — a guard stopped it, a row that was asked for
#             could not be read, a tool is missing, or an internal error ended
#             it. A row that could not be read is never a pass.
#
# Two rows are the work's own risk reaching the live half, and they are written
# to FAIL rather than to be quietly skipped: step 3's floor (a path the list in
# force treats as security-sensitive, which is T1.5's built-in set and it is NOT
# in this tree — a repository that declares its own `security:` row is what the
# step can read today), and step 9's watch row (a live run whose frame carries
# `mode: "-"` is the drop-and-warn path F40 records rather than a mode).
#
# No credential is handled anywhere in it as a value: every read is a name (a
# mode, a layer, an area, a recipe, a path, a status) and no command here prints
# an environment's contents, a secret or a token. A `bash -x` of it would show
# the commands and nothing shaped like a credential.
#
# CHECK127_TIMEOUT bounds one command, and CHECK127_KEEP_SCRATCH keeps the
# scratch directory. Neither can point the check at another repository, another
# project or another run.
set -Eeuo pipefail

# A trace would copy every read this check makes to the terminal, and what it
# reads carries a person's own words (a pull request's branch and title, an
# area's name). Refused before anything else runs.
XTRACE_AT_START=no
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
# Set to yes once every step has run, so the exit trap can tell a finished run
# from one that ended on an error of its own.
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
FACTS_EARLY=""
# A row recorded before the scratch directory exists is kept here and written
# into the facts file the moment there is one: the guards over what you typed run
# before anything is created, and a guard that passed is a row of the block like
# any other.
fact() { # fact KEY PASS|FAIL|INFO "detail"
  local row
  row=$(printf '%s|%s|%s' "$1" "$2" "$3")
  if [ -n "$FACTS_FILE" ]; then
    printf '%s\n' "$row" >>"$FACTS_FILE"
  else
    FACTS_EARLY="${FACTS_EARLY}${row}"$'\n'
  fi
}

# unmeasured KEY "detail": a row that WAS asked for and could not be read — a
# run the listing does not hold, a document that is not there, a frame with no
# live run in it. It reads FAIL in the block (it is not a pass) and the run ends
# as one that could not tell, exit 2: a check that read nothing is not a check
# that found nothing. A step whose flag was simply not given is not this; it is
# an INFO row saying which step stayed yours.
#
# The keys are collected in a FILE and not in a variable: a step is a function,
# and a variable set inside one would be a row in the block that never reached
# the verdict.
UNMEASURED_FILE=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  [ -n "$UNMEASURED_FILE" ] && printf '%s\n' "$1" >>"$UNMEASURED_FILE"
  return 0
}

# yours KEY "what to run": the step is the owner's and this script has no
# evidence for it. It is recorded as INFO with its own command, so the block
# says which steps were not measured rather than reading as a run that measured
# them and found nothing.
yours() { # yours KEY "detail"
  fact "$1" INFO "not measured: $2"
  owner "step not measured — $2"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the modes"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 127 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 127 needs bash"
  local t
  for t in mktemp sed awk python3 git fugaro; do
    need "$t"
  done
}

# How long one call is given before its row reads as a call that did not answer.
# Every read here answers in seconds on a healthy installation, save `gh run
# list`, which a busy repository can make think for a while.
TIMEOUT=${CHECK127_TIMEOUT:-120}
BOUND=()
if command -v timeout >/dev/null 2>&1; then BOUND=(timeout "$TIMEOUT"); fi

SCRATCH=""
SCRATCH_MARKER=".chk127-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK127_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""
DRY_RUN=no
HELPED=no
# Yes once the steps are about to run: before that, a step's row is not one that
# was "not reached", it is one that was never going to be asked for.
STEPS_STARTED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk127.XXXXXXXX") || die "Check 127 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  [ -z "$FACTS_EARLY" ] || printf '%s' "$FACTS_EARLY" >>"$FACTS_FILE"
  UNMEASURED_FILE="$SCRATCH/unmeasured"
  : >"$UNMEASURED_FILE"
  # A scratch directory inside a work tree could be committed by accident, and
  # this one holds a run's record and a copy of the backlog folder.
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 127 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  FACTS_CAPTURED="${FACTS_EARLY}$(cat "$FACTS_FILE" 2>/dev/null || true)"
  remove_scratch
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
    say "CHECK127_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  # --help printed the usage on its own and read nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it:
  # bash exits by itself for an error the ERR trap does not see (an unbound
  # variable, a bad substitution), and a block that then read as a finished run
  # would offer a row of FAILs as a verdict about the modes when the steps that
  # would have filled them never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the modes"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$DRY_RUN" = yes ]; then
    facts="${facts}"$'\n'"dry-run|INFO|nothing was run: this is the plan, step by step, and no fugaro, git or gh call was made"$'\n'"restore|PASS|nothing was created: a dry run makes no scratch directory and reaches nothing"
  fi
  local restore=PASS
  if [ "$DRY_RUN" != yes ]; then
    if [ -z "$SCRATCH" ]; then
      facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove"
    else
      [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
      facts="${facts}"$'\n'"restore|$restore|this check launched nothing, opened nothing and wrote nothing outside its scratch directory — the backlog index it reads in step 11 is regenerated in a COPY under the scratch — and the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
    fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the requests
# bounded CMD... — the command under `timeout` where the machine has one, so a
# read that hangs is a row that says so rather than a run nobody can end. The
# branch is written out rather than splatted through an empty array: the owner's
# own shell may be bash 3.2, where expanding one under `set -u` is an error.
bounded() {
  if [ "${#BOUND[@]}" -gt 0 ]; then
    "${BOUND[@]}" "$@"
  else
    "$@"
  fi
}

# run_capture OUTFILE CMD... — runs a bounded read, keeps stdout in OUTFILE and
# leaves the exit status in $?. stderr is shown to the owner and never parsed:
# it is where fugaro and gh put their progress, and a warning on it is not a
# failed read.
run_capture() {
  local out=$1
  shift
  bounded "$@" >"$out" 2>"$SCRATCH/cmd.err" || {
    local status=$?
    sed -n '1,5p' "$SCRATCH/cmd.err" >&4 || true
    return "$status"
  }
}

# run_in DIR OUT CMD... — the same, in a checkout: the config in force, the
# backlog and the base branch are read where the repository is, and the check
# never changes directory itself.
run_in() {
  local dir=$1 out=$2
  shift 2
  (cd "$dir" && run_capture "$out" "$@")
}

# reader SUBCOMMAND [ARGS] — the python3 reader that parses what the tools
# print. It prints a fixed vocabulary and never a document: `mode=`, `from=`,
# `revision=`, `areas=2`, `tolerated=0`, `levels=l1,l2`, `sensitive=yes`,
# `merged=yes`, `same`, and the like.
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }

# repo_from_url URL — `owner/name` out of a remote URL, so the repository the
# run references belong to is the checkout's own and not an argument.
repo_from_url() {
  local u=$1
  u=${u%.git}
  case "$u" in
  *://*) u=${u#*://}; u=${u#*/} ;;
  *@*:*) u=${u#*:} ;;
  esac
  printf '%s' "$u"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the documents this check reads — pull request branches and titles other people wrote — into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check127.sh --sandbox <checkout> --run <slug>/<run-id> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries a document it read"
}

guard_agent_session() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  if [ -n "$found" ]; then
    fact agent-session FAIL "this shell carries $found, so it is a coding agent's session: no agent runs any step of Check 127"
    stop "this is a coding agent's session ($found), and no agent runs any step of Check 127. Run it yourself, at your own terminal"
  fi
  fact agent-session PASS "this shell carries no coding-agent marker, so the check was started by a person"
}

# guard_belong NAME... — `belong` is the owner's own installation and
# repository, and this check reads a sandbox repository's runs, config and pull
# requests. Every value the owner typed and every one a tool answered is put
# through it, because a checkout named for a sandbox can still belong to the
# owner's own project.
BELONG_SEEN=""
guard_belong() {
  local name
  for name in "$@"; do
    case "$name" in
    "") continue ;;
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation or repository: Check 127 reads a repository's runs, its config and its pull requests, and it is never run against the owner's own work"
      stop "the value '$name' matches 'belong', which this check must never read"
      ;;
    esac
    BELONG_SEEN="${BELONG_SEEN}${BELONG_SEEN:+, }$name"
  done
}

# The values the owner typed, refused before any tool is called: a dry run and a
# real run make the same refusal, so a plan that reads as a plan cannot be one
# that would have been stopped a moment later.
guard_typed() {
  guard_belong "$SANDBOX" "$RUN_REF" "$CRITICAL_REF" "$HOTFIX_REF" "$FLOOR_PATH" \
    "$CHECKOUT2" "$RUN2_REF" "$REF_RUN" "$REF" "$AREAS_REF" "$ITEM_PR" "$ITEM_PR2" "$RECIPE"
  guard_belong "$CHECKOUT2"
  fact belong PASS "nothing you gave this check names 'belong', the owner's own installation or repository"
}

# ------------------------------------------------------------------ the steps
step_installation() {
  local doctor_status=0
  run_in "$SANDBOX" "$SCRATCH/doctor.json" fugaro doctor --json || doctor_status=$?
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$doctor_status" != 0 ] && [ "$doctor_status" != 1 ]; then
    stop "fugaro doctor --json did not answer in $SANDBOX, so this run cannot tell which installation the sandbox belongs to. Run it yourself there and fix what it reports: the check needs your own credentials and a local config for that installation"
  fi
  local answer
  answer=$(reader doctor <"$SCRATCH/doctor.json") || stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  NAME=$(field_of name "$answer")
  PROJECT=$(field_of project "$answer")
  REGION=$(field_of region "$answer")
  [ -n "$PROJECT" ] || stop "fugaro doctor --json named no project, so this check cannot say which installation the sandbox belongs to"
  guard_belong "$PROJECT" "$NAME"
  local url
  url=$(git -C "$SANDBOX" remote get-url origin 2>/dev/null) || stop "$SANDBOX is not a git checkout with an origin remote, so the repository the runs belong to cannot be read from it"
  REPO=$(repo_from_url "$url")
  case "$url" in
  *github.com*) ;;
  *) stop "the sandbox's origin is $url, which is not a GitHub repository: steps 5, 10 and 11 read a merge, a CI run and a pull request through gh, which is GitHub's" ;;
  esac
  guard_belong "$REPO"
  # The config in force is read here and not in the steps that use it: the base
  # branch (steps 5 and 11) and a repository's own rigor.default (step 6) are
  # keys of this file, and reading it once is what keeps two steps from
  # disagreeing about it. A failure is not a stop — the steps that need a key
  # say so themselves — so a checkout whose config cannot be read still gets the
  # rows that do not need one.
  if ! run_in "$SANDBOX" "$SCRATCH/config-sandbox.json" fugaro config show --json; then
    owner "fugaro config show --json did not answer in $SANDBOX: the steps that read the config in force will say so"
  fi
  fact installation PASS "the sandbox checkout belongs to the $NAME installation ($PROJECT, $REGION), and its runs are read there"
  fact sandbox PASS "the repository is $REPO, at $SANDBOX, and neither it nor any other value this check was given or read names 'belong'"
}

# config_key KEY FILE — a value and the layer it came from, out of a `fugaro
# config show --json` document; both empty when the file is not there.
config_key() { # config_key KEY [FILE]
  local file=${2:-$SCRATCH/config-sandbox.json} answer
  [ -f "$file" ] || return 0
  answer=$(reader config "$1" <"$file" 2>/dev/null || true)
  field_of value "$answer"
}

# base_branch — the branch a merge lands on, from the config in force. Empty
# when the file names none.
base_branch() { config_key git.base_branch; }

# backlog_dir — the folder the item files live in, answered by the command that
# generates the index from them rather than by a second copy of the default.
backlog_dir() {
  local answer
  if answer=$(run_in "$SANDBOX" "$SCRATCH/backlog-list.json" fugaro backlog list --json &&
    reader backlog <"$SCRATCH/backlog-list.json" 2>/dev/null); then
    local dir
    dir=$(field_of dir "$answer")
    if [ -n "$dir" ]; then
      printf '%s' "$dir"
      return 0
    fi
  fi
  printf 'docs/backlog'
}

# runs_ls NAME REPO — every row this repository has, into the scratch. --since 0
# is every run the bucket holds rather than the default window, because the
# runs step 1 and 2 name are the owner's own and may be a week old.
runs_ls() { # runs_ls NAME REPO
  run_in "$SANDBOX" "$SCRATCH/ls-$1.json" fugaro runs ls --json --since 0 --repo "$2"
}

# run_row NAME REF [REPO] — the reader's fixed vocabulary for one row of a
# repository's listing, or a failure when the listing does not hold it.
run_row() { # run_row NAME REF [REPO]
  local name=$1 ref=$2 repo=${3:-$REPO} answer
  if ! runs_ls "$name" "$repo"; then
    stop "fugaro runs ls --json did not answer for $repo, so no run's record can be read. Run it yourself in $SANDBOX: the check needs your own credentials for the installation"
  fi
  answer=$(reader run "$ref" <"$SCRATCH/ls-$name.json") \
    || stop "the answer to fugaro runs ls --json is not the document this check reads (is this fugaro too old to print --json?)"
  if [ "$(field_of found "$answer")" != yes ]; then
    printf '%s\n' "$answer" >&2
    return 1
  fi
  RUN_ANSWER=$answer
  return 0
}

# run_diagnose REF FILE — `fugaro diagnose` of one run, parsed.
run_diagnose() { # run_diagnose REF FILE
  run_in "$SANDBOX" "$2" fugaro diagnose "$1" --json || return 1
  reader diagnose <"$2"
}

step_poc() {
  run_row poc "$RUN_REF" || {
    unmeasured poc-run "$REPO's listing does not hold the run $RUN_REF (the row's own name is <slug>/<run-id>, which fugaro run --json printed as run). A run older than the bucket's retention, or a reference typed from the run id alone, reads here"
    return 0
  }
  POC_MODE=$(field_of mode "$RUN_ANSWER")
  POC_FROM=$(field_of from "$RUN_ANSWER")
  POC_REVISION=$(field_of revision "$RUN_ANSWER")
  POC_RECIPE=$(field_of recipe "$RUN_ANSWER")
  POC_COST=$(field_of cost_model "$RUN_ANSWER")
  if [ "$POC_MODE" != poc ]; then
    fact poc-run FAIL "the run $RUN_REF resolved mode '$POC_MODE' (from '$POC_FROM'), not poc: step 1 is a run that ASKED for POC — the default is Critical (D19), so a repository shown to be running at POC has to be one that asked, and fugaro run --mode poc is the command that asks"
    return 1
  fi
  fact poc-run PASS "the run $RUN_REF resolved mode poc, decided by the '$POC_FROM' layer, from $POC_REVISION, and it cost \$$POC_COST of model spend"
  local answer
  if ! answer=$(run_diagnose "$RUN_REF" "$SCRATCH/diag-poc.json"); then
    unmeasured diagnose "fugaro diagnose $RUN_REF --json did not answer, so the run's rounds and the findings its mode tolerated cannot be read"
    return 0
  fi
  POC_ROUNDS=$(field_of rounds "$answer")
  POC_TOLERATED=$(field_of tolerated "$answer")
  POC_LEVELS=$(field_of levels "$answer")
  DIAG_RUN=$(field_of run "$answer")
  if [ "$DIAG_RUN" != "$RUN_REF" ]; then
    fact diagnose FAIL "fugaro diagnose answered about '$DIAG_RUN' and not about $RUN_REF: the document is not this run's"
    return 1
  fi
  fact diagnose PASS "fugaro diagnose reads $RUN_REF: $POC_ROUNDS review rounds, levels ${POC_LEVELS:-none}, and $POC_TOLERATED finding(s) its mode tolerated"
}

step_critical() {
  if [ -z "$CRITICAL_REF" ]; then
    yours critical-run "step 2, the same task at Critical: launch it with 'fugaro run --repo $REPO --task-file <the same task> --mode critical --json' and run this check again with --critical <slug>/<run-id>"
    yours mode-diff "step 2's diff: it is read by this check once --critical is given"
    return 0
  fi
  run_row critical "$CRITICAL_REF" || {
    unmeasured critical-run "$REPO's listing does not hold the run $CRITICAL_REF, so the Critical half of step 2 cannot be read"
    return 1
  }
  local mode
  mode=$(field_of mode "$RUN_ANSWER")
  if [ "$mode" != critical ]; then
    fact critical-run FAIL "the run $CRITICAL_REF resolved mode '$mode', not critical: step 2 compares the same task at the two ends of the scale, and a run whose record does not say critical is not the second end"
    return 1
  fi
  fact critical-run PASS "the run $CRITICAL_REF resolved mode critical, decided by the '$(field_of from "$RUN_ANSWER")' layer, and it cost \$$(field_of cost_model "$RUN_ANSWER") of model spend"
  local answer
  if ! answer=$(run_diagnose "$CRITICAL_REF" "$SCRATCH/diag-critical.json"); then
    unmeasured mode-diff "fugaro diagnose $CRITICAL_REF --json did not answer, so the two runs cannot be compared"
    return 0
  fi
  local rounds tolerated levels
  rounds=$(field_of rounds "$answer")
  tolerated=$(field_of tolerated "$answer")
  levels=$(field_of levels "$answer")
  # The diff is one-directional and that is the claim: a mode higher on the scale
  # tolerates no more than a lower one over the same task (D1's table), so the
  # strict run's tolerated count may not exceed the lenient one's. The rounds and
  # the levels the two runs reached are printed as they are rather than compared:
  # a run spends the rounds it needs, and Critical's five senior rounds are a
  # default and not a floor.
  if [ "${tolerated:-0}" -gt "${POC_TOLERATED:-0}" ]; then
    fact mode-diff FAIL "the Critical run tolerated $tolerated finding(s) where the POC run of the same task tolerated $POC_TOLERATED: a mode higher on the scale must tolerate no more than a lower one (D1's table), so the strict run is the more lenient of the two here"
    return 1
  fi
  fact mode-diff PASS "over the same task: POC tolerated $POC_TOLERATED finding(s) in $POC_ROUNDS rounds at levels ${POC_LEVELS:-none}, Critical tolerated $tolerated in $rounds rounds at levels ${levels:-none} — the strict run tolerated no more"
}

step_hotfix() {
  if [ -z "$HOTFIX_REF" ]; then
    yours hotfix-run "step 3, the Hotfix run on a floored path: launch it with 'fugaro run --repo $REPO --task-file <the task that touches the path> --mode hotfix --path <path> --json' and run this check again with --hotfix <slug>/<run-id> --path <path>"
    yours floor "step 3's floor: it is read by this check once --hotfix and --path are given"
    return 0
  fi
  [ -n "$FLOOR_PATH" ] || stop "--hotfix was given and --path was not: step 3 is a Hotfix run touching a path, and the path is what the list in force is asked about"
  run_row hotfix "$HOTFIX_REF" || {
    unmeasured hotfix-run "$REPO's listing does not hold the run $HOTFIX_REF, so the Hotfix half of step 3 cannot be read"
    return 1
  }
  local mode
  mode=$(field_of mode "$RUN_ANSWER")
  if [ "$mode" != hotfix ]; then
    fact hotfix-run FAIL "the run $HOTFIX_REF resolved mode '$mode', not hotfix: Hotfix is taken whole and needs no opt-in (T5.2), so a record that does not say hotfix is a run the flag never reached"
    return 1
  fi
  fact hotfix-run PASS "the run $HOTFIX_REF resolved mode hotfix, decided by the '$(field_of from "$RUN_ANSWER")' layer, over the path $FLOOR_PATH"
  local answer
  if ! run_in "$SANDBOX" "$SCRATCH/paths.json" fugaro security paths --check "$FLOOR_PATH" --json; then
    unmeasured floor "fugaro security paths --check $FLOOR_PATH --json did not answer in $SANDBOX, so whether the list in force treats that path as security-sensitive cannot be read"
    return 0
  fi
  answer=$(reader paths <"$SCRATCH/paths.json") || stop "the answer to fugaro security paths --json is not the document this check reads"
  local sensitive matched
  sensitive=$(field_of sensitive "$answer")
  matched=$(field_of matched "$answer")
  if [ "$sensitive" != yes ]; then
    fact floor FAIL "the list in force does NOT treat $FLOOR_PATH as security-sensitive (no row of it matched), so this run's finding on it was a finding the mode's table may weigh rather than the mode's own floor: T1.5's built-in set — .github/workflows/ and the credential paths — is what makes such a path a floor in every mode, and it is not in this tree yet, so today the step needs a repository that declares the path itself (a security: row in fugaro.yaml)"
    return 1
  fi
  if ! answer=$(run_diagnose "$HOTFIX_REF" "$SCRATCH/diag-hotfix.json"); then
    unmeasured floor "fugaro diagnose $HOTFIX_REF --json did not answer, so the findings this run's mode tolerated cannot be read"
    return 0
  fi
  local tol
  tol=$(field_of tolerated "$answer")
  if [ "${tol:-0}" -gt 0 ]; then
    fact floor FAIL "$FLOOR_PATH is on the list in force (row '$matched'), and the Hotfix run tolerated $tol finding(s) anyway: a path the list calls security-sensitive is where the mode may not let a finding through on its own, so a tolerated finding here is the mode being lenient where it must not be"
    return 1
  fi
  fact floor PASS "$FLOOR_PATH is on the list in force (row '$matched'), and the Hotfix run over it tolerated no finding: what kept the finding open is the mode's own reading and not a guard refusing the run"
}

step_preview() {
  local recipe=${RECIPE:-$POC_RECIPE}
  if [ -z "$recipe" ] || [ "$recipe" = "-" ]; then
    yours preview "step 4, the preview against a finished run's own figure: the run's record names no recipe, so pass --recipe <name> and run this check again (fugaro budget preview --recipe <name> --runs ${RUNS_N:-10} --repo $REPO --mode critical --json)"
    return 0
  fi
  local runs=${RUNS_N:-10}
  if ! run_in "$SANDBOX" "$SCRATCH/preview-critical.json" fugaro budget preview --recipe "$recipe" --runs "$runs" --repo "$REPO" --mode critical --json; then
    unmeasured preview "fugaro budget preview --mode critical --json did not answer in $SANDBOX for the recipe $recipe, so the ceiling the mode resolves cannot be read"
    return 0
  fi
  local critical answer
  critical=$(reader preview <"$SCRATCH/preview-critical.json") \
    || stop "the answer to fugaro budget preview --json is not the document this check reads (is this fugaro too old to print --json?)"
  local ceiling knobs verdict
  ceiling=$(field_of ceiling "$critical")
  knobs=$(field_of knobs_review "$critical")
  verdict=$(field_of verdict "$critical")
  # The repository's own resolution beside the mode's: the request may only raise
  # a mode, so the preview of the mode asked for may not price fewer review
  # rounds than the preview without the flag (T6.2's tighten-only reading).
  local own_knobs="" own
  if run_in "$SANDBOX" "$SCRATCH/preview-own.json" fugaro budget preview --recipe "$recipe" --runs "$runs" --repo "$REPO" --json; then
    own=$(reader preview <"$SCRATCH/preview-own.json" 2>/dev/null || true)
    own_knobs=$(field_of knobs_review "$own")
  fi
  if [ -z "$ceiling" ] || [ "$ceiling" = "-" ]; then
    unmeasured preview "the preview answered no ceiling for --mode critical (verdict '$verdict'), so there is no figure to hold against the finished run's"
    return 0
  fi
  if ! awk -v c="$ceiling" -v r="${POC_COST:-0}" 'BEGIN { exit !(c + 0 >= r + 0) }'; then
    fact preview FAIL "the preview prices a Critical run of $recipe at \$$ceiling, and the finished run of this repository spent \$$POC_COST: the ceiling the mode resolves is below what a real run of it cost, so the preview is not the figure an owner can plan against"
    return 1
  fi
  if [ -n "$own_knobs" ] && [ "$own_knobs" != "-" ] && [ "$knobs" != "-" ] && [ "$knobs" -lt "$own_knobs" ]; then
    fact preview FAIL "the preview at --mode critical prices $knobs review rounds and the same preview without the flag prices $own_knobs: asking for a mode may only raise what a run spends, so the mode's own preview is the more lenient of the two"
    return 1
  fi
  fact preview PASS "fugaro budget preview prices $recipe at \$$ceiling with $knobs review rounds at --mode critical${own_knobs:+ (the resolution in force: $own_knobs)}, and the finished run's gateway figure was \$$POC_COST — the ceiling is not below it. Verdict: $verdict"
}

step_postmerge() {
  if [ -z "$PR" ]; then
    yours post-merge "step 5, a merged Hotfix PR's post-merge CI: name the pull request the Hotfix run opened with --pr <N> and run this check again (the CI behind it is read with 'gh run list --branch <base>')"
    return 0
  fi
  command -v gh >/dev/null 2>&1 ||
    stop "step 5 reads a pull request and the CI behind it with gh, and gh is not on PATH"
  local head="" n=$PR base=""
  base=$(base_branch)
  head=$base
  if [ -z "$head" ] || [ "$head" = "-" ]; then
    unmeasured post-merge "the base branch the merge lands on could not be named: $SANDBOX's config answered no git.base_branch, so which branch's CI would carry the merge cannot be read"
    return 0
  fi
  if ! run_capture "$SCRATCH/pr.json" gh pr view "$n" --json state,mergedAt,mergeCommit,baseRefName,url; then
    unmeasured post-merge "gh pr view $n did not answer for $REPO, so the pull request's own state cannot be read"
    return 0
  fi
  local answer state sha
  answer=$(reader pr <"$SCRATCH/pr.json") || stop "the answer to gh pr view --json is not the document this check reads"
  state=$(field_of merged "$answer")
  sha=$(field_of merge_sha "$answer")
  if [ "$state" != yes ]; then
    fact post-merge FAIL "the pull request $n is not merged ('$(field_of state "$answer")'), so there is no post-merge CI to read: step 5 is about what the provider does AFTER the merge"
    return 1
  fi
  if ! run_capture "$SCRATCH/ci.json" gh run list --branch "$head" --limit 20 --json databaseId,headSha,event,status,conclusion; then
    unmeasured post-merge "gh run list did not answer for $REPO, so whether the provider ran a workflow on the merge cannot be read"
    return 0
  fi
  local ci
  ci=$(reader ciruns "$sha" <"$SCRATCH/ci.json") || stop "the answer to gh run list --json is not the document this check reads"
  if [ "$(field_of found "$ci")" != yes ]; then
    fact post-merge FAIL "the pull request $n is merged into $head (merge commit ${sha:-unknown}), and gh run list names no workflow run whose head is that commit: the post-merge CI either did not fire or fired on another commit, which is the thing step 5 exists to read. Read it yourself in the provider's own UI before concluding anything about the merge"
    return 1
  fi
  fact post-merge PASS "the pull request $n is merged into $head as ${sha:-the merge commit}, and the provider ran $(field_of runs "$ci") workflow run(s) on $head, one of them on that very commit (${sha:-the merge})"
}

step_two_repos() {
  if [ -z "$CHECKOUT2" ]; then
    yours second-repo "step 6's second repository: clone the project's other sandbox repository and run this check again with --checkout2 <checkout>"
    yours project-default "step 6's reading: it is read by this check once --checkout2 is given"
    return 0
  fi
  local answer2
  if ! run_in "$CHECKOUT2" "$SCRATCH/doctor2.json" fugaro doctor --json; then
    unmeasured second-repo "fugaro doctor --json did not answer in $CHECKOUT2, so the two checkouts cannot be held to the same project"
    return 0
  fi
  answer2=$(reader doctor <"$SCRATCH/doctor2.json") || stop "the answer to fugaro doctor --json in $CHECKOUT2 is not the document this check reads"
  local project2
  project2=$(field_of project "$answer2")
  if [ "$project2" != "$PROJECT" ]; then
    fact second-repo FAIL "$CHECKOUT2 belongs to the $project2 installation and $SANDBOX to $PROJECT: step 6 is ONE project whose two repositories differ, and two installations are two projects"
    return 1
  fi
  fact second-repo PASS "both checkouts belong to the $PROJECT installation, so the two repositories share one project layer"
  # The second repository's own name comes from its own checkout, not from an
  # argument: the listing a run is read out of has to be that repository's.
  REPO2=$(repo_from_url "$(git -C "$CHECKOUT2" remote get-url origin 2>/dev/null || echo '')")
  if [ -z "$REPO2" ]; then
    unmeasured second-repo "$CHECKOUT2 is not a git checkout with an origin remote, so the second repository's own name cannot be read"
    return 0
  fi
  guard_belong "$REPO2"
  if ! run_in "$CHECKOUT2" "$SCRATCH/config-second.json" fugaro config show --json; then
    unmeasured project-default "fugaro config show --json did not answer in $CHECKOUT2"
    return 0
  fi
  local a b va vb sa sb
  if [ ! -f "$SCRATCH/config-sandbox.json" ]; then
    unmeasured project-default "fugaro config show --json did not answer in $SANDBOX, so the sandbox repository's own rigor.default cannot be read"
    return 0
  fi
  a=$(reader config rigor.default <"$SCRATCH/config-sandbox.json") || stop "the answer to fugaro config show --json in $SANDBOX is not the document this check reads"
  b=$(reader config rigor.default <"$SCRATCH/config-second.json") || stop "the answer to fugaro config show --json in $CHECKOUT2 is not the document this check reads"
  va=$(field_of value "$a")
  vb=$(field_of value "$b")
  sa=$(field_of source "$a")
  sb=$(field_of source "$b")
  if [ "$va" = "-" ] || [ "$vb" = "-" ] || [ -z "$va" ] || [ -z "$vb" ]; then
    fact project-default FAIL "one of the two repositories sets no rigor.default (${REPO}: '${va:-none}' from '${sa:-none}'; the second: '${vb:-none}' from '${sb:-none}'): step 6 is a project whose two repositories DIFFER in that key, and a repository that does not set it is one whose mode the project layer decides"
    return 1
  fi
  if [ "$va" = "$vb" ]; then
    fact project-default FAIL "both repositories set rigor.default to '$va', so they do not differ in their own key: step 6 is the case where the project layer is shared and the two repositories' own resolutions are not"
    return 1
  fi
  if [ "$sa" != repo ] || [ "$sb" != repo ]; then
    fact project-default FAIL "the key resolves from the '$sa' layer in $REPO and the '$sb' layer in $CHECKOUT2, and step 6 is about each REPOSITORY's own key: a value that comes from the project layer, the default or a request is not the repository's own"
    return 1
  fi
  # And each repository's run obeyed its own key: the record is where "the
  # repository's own rigor.default decides its runs" is read rather than assumed.
  local modes=""
  if run_row two-a "$RUN_REF"; then
    modes="the run of $REPO resolved '$(field_of mode "$RUN_ANSWER")'"
    if [ "$(field_of mode "$RUN_ANSWER")" != "$va" ]; then
      fact project-default FAIL "the run $RUN_REF resolved '$(field_of mode "$RUN_ANSWER")' where $REPO's own rigor.default says '$va': the repository's key is not what its runs resolve"
      return 1
    fi
  fi
  if [ -n "$RUN2_REF" ]; then
    if ! run_row two-b "$RUN2_REF" "$REPO2"; then
      unmeasured project-default "$REPO2's listing does not hold the run $RUN2_REF, so the second repository's resolution cannot be read"
      return 0
    fi
    modes="$modes; the run of $REPO2 resolved '$(field_of mode "$RUN_ANSWER")'"
    if [ "$(field_of mode "$RUN_ANSWER")" != "$vb" ]; then
      fact project-default FAIL "the run $RUN2_REF resolved '$(field_of mode "$RUN_ANSWER")' where $REPO2's own rigor.default says '$vb': the repository's key is not what its runs resolve"
      return 1
    fi
  else
    yours project-default-runs "step 6's second half: launch a run in the second repository and pass --run2 <slug>/<run-id>, so each repository's own key is read against its own run's record"
  fi
  fact project-default PASS "$REPO sets rigor.default '$va' and $REPO2 '$vb', each from its own file; $modes"
}

step_ref() {
  if [ -z "$REF_RUN" ]; then
    yours ref-revision "step 7, --ref at a branch whose rigor: disagrees with the base branch's: launch it with 'fugaro run --repo $REPO --ref <branch> --task-file <task> --json' and run this check again with --ref-run <slug>/<run-id> --ref <branch>"
    yours ref-branch "step 7's second half: it is read by this check once --ref-run and --ref are given"
    return 0
  fi
  run_row ref "$REF_RUN" || {
    unmeasured ref-revision "$REPO's listing does not hold the run $REF_RUN, so the revision its mode was read from cannot be read"
    return 1
  }
  local rev branch base
  rev=$(field_of revision "$RUN_ANSWER")
  branch=$(field_of branch "$RUN_ANSWER")
  base=$(field_of base "$RUN_ANSWER")
  if [ -z "$rev" ] || [ "$rev" = "-" ]; then
    fact ref-revision FAIL "the run $REF_RUN carries no rigor.revision, so which revision its mode was read from is not in the record at all: D25's answer is exactly this field, and a record without one is a record nobody can hold to it"
    return 1
  fi
  [ -n "$base" ] && [ "$base" != "-" ] || base=$(base_branch)
  if [ -n "$base" ] && [ "$rev" != "origin/$base" ]; then
    fact ref-revision FAIL "the run $REF_RUN read its mode from '$rev' where its base branch is '$base': D25 says the mode is read from the BASE branch for every run kind, and the branch under review never supplies it"
    return 1
  fi
  if [ -n "$branch" ] && [ "$branch" != "-" ] && [ "$rev" = "origin/$branch" ]; then
    fact ref-revision FAIL "the run $REF_RUN read its mode from '$rev', which is the run's OWN branch $branch: a branch that writes its own rigor: would then be setting the mode its own review runs under, which is the thing D25 refuses"
    return 1
  fi
  local from
  from=$(field_of from "$RUN_ANSWER")
  if [ "$from" = request ]; then
    fact ref-revision FAIL "the run $REF_RUN resolved its mode from the run's own request (from 'request'): step 7 is a run whose mode came from the configuration at a revision, and --mode is the flag that would put it beyond the question. Record: mode '$(field_of mode "$RUN_ANSWER")', revision '$rev'"
    return 1
  fi
  fact ref-revision PASS "the run $REF_RUN resolved its mode from the '$from' layer at revision '$rev'${branch:+, while the run itself is on $branch}${base:+, and its base branch is $base} — the configuration the mode came from is named, and it is not the branch under review"
  if [ -n "$REF" ]; then
    local text
    if text=$(git -C "$SANDBOX" show "$REF:fugaro.yaml" 2>/dev/null); then
      local branch_default
      branch_default=$(printf '%s\n' "$text" | reader yaml-rigor-default || true)
      fact ref-branch INFO "the branch $REF's own fugaro.yaml says '${branch_default:-nothing}' for rigor.default, and the record carries '$(field_of mode "$RUN_ANSWER")' from '$rev': the two are the comparison step 7 is about, and a person reads whether the branch's own block was ignored as D25 says"
    else
      fact ref-branch INFO "git show $REF:fugaro.yaml found no config at $REF in $SANDBOX, so the branch's own block could not be read; the record's revision above is still the answer D25 asks for"
    fi
  else
    yours ref-branch "step 7's comparison: pass --ref <branch> and this check reads the branch's own rigor: block out of the checkout's objects (git show <branch>:fugaro.yaml) beside the mode the record carries"
  fi
}

step_areas() {
  if [ -z "$AREAS_REF" ]; then
    yours areas "step 8, a run whose diff matches two areas: launch it with 'fugaro run --repo $REPO --path <path-a> --path <path-b> --task-file <task> --json' and run this check again with --areas-run <slug>/<run-id>"
    yours areas-round "step 8's second half: it is read by this check once --areas-run is given"
    return 0
  fi
  run_row areas "$AREAS_REF" || {
    unmeasured areas "$REPO's listing does not hold the run $AREAS_REF, so the rules it matched cannot be read"
    return 1
  }
  local n
  n=$(field_of areas "$RUN_ANSWER")
  if [ "${n:-0}" -lt 2 ]; then
    fact areas FAIL "the run $AREAS_REF matched $n area rule(s), and step 8 is a run whose diff matches TWO: read the repository's rigor.areas against the paths the run declared (--path), and check that both rules' globs cover them"
    return 1
  fi
  local listed
  listed=$(printf '%s\n' "$RUN_ANSWER" | sed -n 's/^area=//p' | tr '\n' ';')
  fact areas PASS "the run $AREAS_REF matched $n area rules — ${listed%;} — each with the mode it sets and the moment it was first applied at"
  local late
  late=$(printf '%s\n' "$RUN_ANSWER" | sed -n 's/^area=[^ ]* [^ ]* //p' | grep -v '^bootstrap$' | head -n 1 || true)
  if [ -n "$late" ]; then
    fact areas-round INFO "an area was first applied at the '$late' moment and not at bootstrap: that is step 8's second half — a round that reached a new rule — read off the record's own moments"
  else
    yours areas-round "step 8's second half: a run whose SECOND round reaches a new rule needs a live multi-round agent. Read the review transcript of $AREAS_REF for a round whose diff touched a path no earlier round had, and check the record's area moments again"
  fi
}

step_watch() {
  if ! run_in "$SANDBOX" "$SCRATCH/watch.json" fugaro watch --once --json --repo "$REPO"; then
    unmeasured watch "fugaro watch --once --json did not answer in $SANDBOX, so the frame a live run's mode is read in cannot be read. It needs the deployed rules: regenerate and deploy them first (fugaro update --firebase-project <id>), then run this again"
    return 0
  fi
  local answer live nomode
  answer=$(reader watch <"$SCRATCH/watch.json") || stop "the answer to fugaro watch --json is not the document this check reads"
  live=$(field_of runs "$answer")
  nomode=$(field_of nomode "$answer")
  if [ "${live:-0}" -eq 0 ]; then
    unmeasured watch "fugaro watch read no live run for $REPO, so there was no frame carrying a mode to read: start a run (fugaro run --repo $REPO ...) with the regenerated rules deployed and run this step while it is in flight"
    return 0
  fi
  if [ "${nomode:-0}" -gt 0 ]; then
    fact watch FAIL "$nomode of $live live run(s) carry mode '-' in the watch frame: that is the drop-and-warn path F40 records — an installation whose rules are older than the registry's children reads no mode rather than failing, so what a reader sees is a run with no rigor at all. Regenerate the rules and deploy them (fugaro update --firebase-project <id>), then read the frame again"
    return 1
  fi
  local modes
  modes=$(printf '%s\n' "$answer" | sed -n 's/^run=//p' | tr '\n' ' ')
  fact watch PASS "$live live run(s) carry a mode in the watch frame ($modes) — the mode and the layer it came from are what a person reads while the run is in flight"
}

# pull_request N NAME — gh's own document of one pull request, in the scratch.
pull_request() { # pull_request N OUT
  run_capture "$2" gh pr view "$1" --json state,mergedAt,mergeCommit,headRefName,baseRefName,files,commits,url
}

# pr_items PR_ANSWER — one `path=<p>` line per item file the pull request
# touches, in the backlog folder. The folder comes from `fugaro backlog list`,
# which is the same answer the index is generated from.
pr_items() { # pr_items PR_ANSWER
  local answer=$1 dir
  dir=$(backlog_dir)
  printf '%s\n' "$answer" | reader pritems "$dir"
}

step_item_pr() {
  if [ -z "$ITEM_PR" ]; then
    yours item-pr "step 10, the pull request that fixes a filed item: name it with --item-pr <N> once it is open (the flip and the regenerated index in one commit are read out of gh's own document of it)"
    return 0
  fi
  need gh "step 10 reads a pull request and its commits through gh"
  local answer
  if ! pull_request "$ITEM_PR" "$SCRATCH/itempr.json"; then
    unmeasured item-pr "gh pr view $ITEM_PR did not answer for $REPO, so the pull request's files and commits cannot be read"
    return 0
  fi
  answer=$(reader pr <"$SCRATCH/itempr.json") || stop "the answer to gh pr view --json is not the document this check reads"
  local merged head size dir items
  merged=$(field_of merged "$answer")
  head=$(field_of head "$answer")
  # A pull request the check is pointed at before the merge is read anyway: the
  # flip and the one-commit claim are readable off it, and the merge is what the
  # step's own text says it waits on. It is said on stderr rather than in a row:
  # one row per key reaches the block, and a second `item-pr` row would be the
  # one that got there first rather than the measurement.
  [ "$merged" = yes ] || owner "the pull request $ITEM_PR is not merged yet ('$(field_of state "$answer")'): the flip and the index are read off it as they stand"
  dir=$(backlog_dir)
  items=$(pr_items "$answer") || {
    unmeasured item-pr "the item files of $ITEM_PR could not be listed"
    return 0
  }
  size=$(printf '%s\n' "$items" | grep -c '^path=' || true)
  if [ "${size:-0}" -eq 0 ]; then
    fact item-pr FAIL "the pull request $ITEM_PR touches no file under $dir/, so it fixes no item: step 10 is the pull request that closes an item, and the item file is the thing it has to carry"
    return 1
  fi
  # The flip itself: the item's own front matter at the pull request's head.
  local path status flipped=""
  while IFS= read -r line; do
    path=${line#path=}
    [ -n "$path" ] || continue
    if ! run_capture "$SCRATCH/contents.json" gh api "repos/$REPO/contents/$path?ref=$head"; then
      unmeasured item-pr "gh api could not read $path at $head"
      return 0
    fi
    status=$(reader itemfile <"$SCRATCH/contents.json" | sed -n 's/^status=//p' | head -n 1)
    printf 'item %s status=%s\n' "$path" "$status" >&4
    case "$status" in
    closed | dropped) flipped=$path ;;
    esac
  done <<<"$items"
  if [ -z "$flipped" ]; then
    fact item-pr FAIL "none of the $size item file(s) the pull request $ITEM_PR touches reads closed (or dropped) at its head: the flip from open to closed is what step 10 is about, and a pull request that carries the file without the flip is a fix that left the item open"
    return 1
  fi
  if ! printf '%s\n' "$answer" | grep -q "^path=$dir/README.md$"; then
    fact item-pr FAIL "the pull request $ITEM_PR flips $flipped and does not touch $dir/README.md, the regenerated index: D31 puts the flip and the index in one commit, because an index that is not regenerated beside the item is one the next reader finds stale"
    return 1
  fi
  # One commit: gh's own list of the files each commit touched.
  : >"$SCRATCH/commit-files"
  local oid
  while IFS= read -r oid; do
    [ -n "$oid" ] || continue
    if ! run_capture "$SCRATCH/commit.json" gh api "repos/$REPO/commits/$oid"; then
      unmeasured item-pr "gh api could not read the commit $oid of $ITEM_PR"
      return 0
    fi
    local f
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      printf '%s|%s\n' "$oid" "${f#path=}" >>"$SCRATCH/commit-files"
    done <<<"$(reader commitfiles <"$SCRATCH/commit.json")"
  done <<<"$(printf '%s\n' "$answer" | sed -n 's/^commit=//p')"
  local together
  together=$(reader onecommit "$dir" <"$SCRATCH/commit-files")
  if [ "$(field_of one_commit "$together")" != yes ]; then
    fact item-pr FAIL "in $ITEM_PR no single commit carries both an item file and $dir/README.md: the flip and the regenerated index are split across commits, which is the state D31 exists to prevent — a reviewer reading one commit sees an item closed under an index that still lists it open"
    return 1
  fi
  fact item-pr PASS "the pull request $ITEM_PR flips $flipped (status closed at its head), carries $dir/README.md, and one commit carries both: the flip and the regenerated index are in the same commit (D31)"
}

step_item_branches() {
  if [ -z "$ITEM_PR2" ]; then
    yours item-branches "step 11, two branches filing an item on the same day: run this check again with --item-pr <N> --item-pr2 <M>, with the sandbox checkout at the base branch that carries both merges"
    yours item-branches-index "step 11's index: it is read by this check once --item-pr2 is given"
    return 0
  fi
  need gh "step 11 reads two pull requests through gh"
  local a b head_a head_b items_a items_b names_a names_b
  if ! pull_request "$ITEM_PR" "$SCRATCH/itempr-a.json" || ! pull_request "$ITEM_PR2" "$SCRATCH/itempr-b.json"; then
    unmeasured item-branches "gh pr view did not answer for one of the two pull requests, so the two branches cannot be compared"
    return 0
  fi
  a=$(reader pr <"$SCRATCH/itempr-a.json") || stop "the answer to gh pr view --json is not the document this check reads"
  b=$(reader pr <"$SCRATCH/itempr-b.json") || stop "the answer to gh pr view --json is not the document this check reads"
  if [ "$(field_of merged "$a")" != yes ] || [ "$(field_of merged "$b")" != yes ]; then
    stop "one of the two pull requests is not merged ($ITEM_PR: $(field_of state "$a"), $ITEM_PR2: $(field_of state "$b")): step 11 is about the order two merged branches land in, so both merges have to be in before the index can be read"
  fi
  head_a=$(field_of head "$a")
  head_b=$(field_of head "$b")
  items_a=$(pr_items "$a") || items_a=""
  items_b=$(pr_items "$b") || items_b=""
  names_a=$(printf '%s\n' "$items_a" | sed -n 's|^path=.*/||p' | sort | tr '\n' ' ')
  names_b=$(printf '%s\n' "$items_b" | sed -n 's|^path=.*/||p' | sort | tr '\n' ' ')
  if [ -z "${names_a// /}" ] || [ -z "${names_b// /}" ]; then
    fact item-branches FAIL "one of the two pull requests touches no item file ($ITEM_PR: '${names_a:-none}'; $ITEM_PR2: '${names_b:-none}'): two branches filing an item the same day are exactly what D26 renames, and a branch that filed none has nothing to rename"
    return 1
  fi
  if [ "$names_a" = "$names_b" ]; then
    fact item-branches FAIL "both pull requests carry the item file(s) '$names_a': two same-day items that share a file name are two items one of which overwrote the other, and the rename D26 asks for is what keeps them apart"
    return 1
  fi
  fact item-branches PASS "the two pull requests filed differently named items ($ITEM_PR: $names_a; $ITEM_PR2: $names_b), so the second's file is the rename D26 asks for"
  # The index, read without writing anything in the checkout: the folder is
  # copied into the scratch and regenerated there, and the bytes are compared
  # with the index the checkout holds.
  local dir
  dir=$(backlog_dir)
  local here
  here=$(git -C "$SANDBOX" rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  local base
  base=$(base_branch)
  if [ -n "$base" ] && [ "$base" != "-" ] && [ "$here" != "$base" ]; then
    stop "$SANDBOX is on '$here' and not on the base branch '$base': step 11's index is the one main carries once both merges are in, so check the sandbox out at $base and run this again"
  fi
  [ -d "$SANDBOX/$dir" ] || stop "$SANDBOX has no $dir folder, so there is no backlog to reconcile"
  cp -R "$SANDBOX/$dir" "$SCRATCH/backlog-copy" || die "Check 127 cannot copy $dir into its scratch directory"
  if ! run_in "$SANDBOX" "$SCRATCH/index.json" fugaro backlog index --dir "$SCRATCH/backlog-copy"; then
    unmeasured item-branches-index "fugaro backlog index did not answer on a copy of $dir, so whether the index in $SANDBOX is current cannot be read"
    return 0
  fi
  if [ "$(reader same "$SCRATCH/backlog-copy/README.md" "$SANDBOX/$dir/README.md" 2>/dev/null || echo different)" != same ]; then
    fact item-branches-index FAIL "regenerating the index on a copy of $dir changes it: the index at $SANDBOX/$dir/README.md is NOT what the item files beside it say, so whoever merged second did not reconcile it — 'fugaro backlog index' is the one command that does (D28), and the diff is in the copy the check left in its scratch"
    return 1
  fi
  local count
  count=$(field_of items "$(reader backlog <"$SCRATCH/backlog-list.json" 2>/dev/null || true)")
  fact item-branches-index PASS "the index at $SANDBOX/$dir/README.md is current: regenerating it on a copy rewrites the same bytes (the folder holds ${count:-?} item(s)), so the second branch's merge reconciled it"
}

# ------------------------------------------------------------------- reporting
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL — not reached. Any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(installation sandbox belong agent-session xtrace poc-run diagnose
  critical-run mode-diff hotfix-run floor preview post-merge second-repo
  project-default ref-revision ref-branch areas areas-round watch item-pr
  item-branches item-branches-index restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 127 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    elif [ "$STEPS_STARTED" = yes ]; then
      # The steps began and this one did not finish: the row belongs in the
      # block as one that was NOT measured.
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 127 RESULT END (exit %s)\n' "$status" >&3
}

# ----------------------------------------------------------------------- main
USAGE="check127.sh --sandbox <checkout> --run <slug>/<run-id> [--critical <slug>/<run-id>] [--hotfix <slug>/<run-id> --path <path>] [--pr <N>] [--checkout2 <checkout> --run2 <slug>/<run-id>] [--ref-run <slug>/<run-id> --ref <branch>] [--areas-run <slug>/<run-id>] [--item-pr <N> --item-pr2 <N>] [--recipe <name>] [--runs <N>] [--dry-run]"

usage() {
  cat <<'USAGE'
Check 127 — the rigor modes on a real repository, read as ONE script you run.

Usage:
  bash scripts/live-checks/check127.sh --sandbox <checkout> --run <slug>/<run-id> [options]

  --sandbox <checkout>     a SANDBOX repository's checkout (required): the
                           project, the repository's name and the runs are read
                           there. `belong` is refused, here and everywhere else.
  --run <slug>/<run-id>    step 1's POC run (required)
  --critical <slug>/<run-id>   step 2: the same task at Critical
  --hotfix <slug>/<run-id>     step 3: the Hotfix run
  --path <path>                step 3: the repository-relative path it touches
  --pr <N>                     step 5: the merged Hotfix pull request
  --checkout2 <checkout>       step 6: the project's second repository
  --run2 <slug>/<run-id>       step 6: a run in that repository
  --ref-run <slug>/<run-id>    step 7: the run started with --ref
  --ref <branch>               step 7: the branch whose rigor: disagrees
  --areas-run <slug>/<run-id>  step 8: the run whose diff matches two areas
  --item-pr <N>                steps 10 and 11: the pull request of a filed item
  --item-pr2 <N>               step 11: the second branch's pull request
  --recipe <name>              step 4: the recipe to price (default: the run's)
  --runs <N>                   step 4: the run count to price (default 10)
  --dry-run                    print the plan and run nothing at all
  --help                       this text

Each step whose flag is absent is printed as one that stays yours, with the
command that produces it, and its row is INFO rather than a FAIL: a run that was
not asked for cannot be a finding about the modes.

The block this prints has one row per reading. Exit 0 when every measured row
passed, 1 when a measured row FAILed, 2 when the run could not tell.
USAGE
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --sandbox)
      [ $# -ge 2 ] || die "--sandbox needs a checkout. usage: $USAGE"
      SANDBOX=$2
      shift 2
      ;;
    --run)
      [ $# -ge 2 ] || die "--run needs <slug>/<run-id>. usage: $USAGE"
      RUN_REF=$2
      shift 2
      ;;
    --critical)
      [ $# -ge 2 ] || die "--critical needs <slug>/<run-id>. usage: $USAGE"
      CRITICAL_REF=$2
      shift 2
      ;;
    --hotfix)
      [ $# -ge 2 ] || die "--hotfix needs <slug>/<run-id>. usage: $USAGE"
      HOTFIX_REF=$2
      shift 2
      ;;
    --path)
      [ $# -ge 2 ] || die "--path needs a repository-relative path. usage: $USAGE"
      FLOOR_PATH=$2
      shift 2
      ;;
    --pr)
      [ $# -ge 2 ] || die "--pr needs a pull request number. usage: $USAGE"
      PR=$2
      shift 2
      ;;
    --checkout2)
      [ $# -ge 2 ] || die "--checkout2 needs a checkout. usage: $USAGE"
      CHECKOUT2=$2
      shift 2
      ;;
    --run2)
      [ $# -ge 2 ] || die "--run2 needs <slug>/<run-id>. usage: $USAGE"
      RUN2_REF=$2
      shift 2
      ;;
    --ref-run)
      [ $# -ge 2 ] || die "--ref-run needs <slug>/<run-id>. usage: $USAGE"
      REF_RUN=$2
      shift 2
      ;;
    --ref)
      [ $# -ge 2 ] || die "--ref needs a branch. usage: $USAGE"
      REF=$2
      shift 2
      ;;
    --areas-run)
      [ $# -ge 2 ] || die "--areas-run needs <slug>/<run-id>. usage: $USAGE"
      AREAS_REF=$2
      shift 2
      ;;
    --item-pr)
      [ $# -ge 2 ] || die "--item-pr needs a pull request number. usage: $USAGE"
      ITEM_PR=$2
      shift 2
      ;;
    --item-pr2)
      [ $# -ge 2 ] || die "--item-pr2 needs a pull request number. usage: $USAGE"
      ITEM_PR2=$2
      shift 2
      ;;
    --recipe)
      [ $# -ge 2 ] || die "--recipe needs a name. usage: $USAGE"
      RECIPE=$2
      shift 2
      ;;
    --runs)
      [ $# -ge 2 ] || die "--runs needs a count. usage: $USAGE"
      RUNS_N=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      usage
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$SANDBOX" ] || die "--sandbox is required: this check reads a sandbox repository's own project, runs and config. usage: $USAGE"
  [ -n "$RUN_REF" ] || die "--run is required: step 1's POC run is what every other step is read beside. usage: $USAGE"
  [ -d "$SANDBOX" ] || die "--sandbox '$SANDBOX' is not a directory"
}

print_plan() {
  owner "the plan, step by step (nothing below is run: this is --dry-run)"
  owner "  1  $SANDBOX: fugaro doctor --json; fugaro runs ls --json --since 0 --repo $REPO_PLAN; fugaro runs ls to find $RUN_REF; fugaro diagnose $RUN_REF --json"
  owner "  2  ${CRITICAL_REF:+fugaro diagnose $CRITICAL_REF --json beside the POC run}${CRITICAL_REF:-this step was not given a --critical run}"
  owner "  3  ${HOTFIX_REF:+fugaro diagnose $HOTFIX_REF --json; fugaro security paths --check ${FLOOR_PATH:-<path>} --json}${HOTFIX_REF:-this step was not given a --hotfix run}"
  owner "  4  fugaro budget preview --recipe ${RECIPE:-<the recipe the run recorded>} --runs ${RUNS_N:-10} --repo <repo> [--mode critical] --json"
  owner "  5  ${PR:+gh pr view $PR --json state,mergedAt,mergeCommit,baseRefName,url; gh run list --branch <base> --limit 20 --json headSha,status,conclusion}${PR:-this step was not given a --pr}"
  owner "  6  ${CHECKOUT2:+fugaro doctor --json and fugaro config show --json in $CHECKOUT2, beside the sandbox}${CHECKOUT2:-this step was not given a --checkout2}"
  owner "  7  ${REF_RUN:+fugaro runs ls --json, reading the rigor.revision of $REF_RUN}${REF_RUN:-this step was not given a --ref-run}${REF:+; git show $REF:fugaro.yaml}"
  owner "  8  ${AREAS_REF:+fugaro runs ls --json, reading the areas of $AREAS_REF}${AREAS_REF:-this step was not given an --areas-run}"
  owner "  9  fugaro watch --once --json --repo <repo>"
  owner " 10  ${ITEM_PR:+gh pr view $ITEM_PR --json files,commits; gh api repos/<owner>/<name>/commits/<oid>; gh api .../contents/<item>?ref=<head>; fugaro backlog list --json}${ITEM_PR:-this step was not given an --item-pr}"
  owner " 11  ${ITEM_PR2:+the same for $ITEM_PR2, then cp -R <checkout>/docs/backlog <scratch>/backlog-copy; fugaro backlog index --dir <scratch>/backlog-copy; cmp}${ITEM_PR2:-this step was not given an --item-pr2}"
  owner "and, in every step: nothing else. No run is launched, no pull request is opened, no object is written and no model is called."
}

main() {
  need_tools
  SANDBOX=""
  RUN_REF=""
  CRITICAL_REF=""
  HOTFIX_REF=""
  FLOOR_PATH=""
  PR=""
  CHECKOUT2=""
  RUN2_REF=""
  REF_RUN=""
  REF=""
  AREAS_REF=""
  ITEM_PR=""
  ITEM_PR2=""
  RECIPE=""
  RUNS_N=""
  parse_args "$@"
  # The guards over what you typed come first and come for a dry run too: a plan
  # that reads as a plan must be the plan of a run that would not have been
  # stopped a moment later.
  guard_agent_session
  guard_typed
  REPO_PLAN=$(repo_from_url "$(git -C "$SANDBOX" remote get-url origin 2>/dev/null || echo "<the checkout's origin>")")
  if [ "$DRY_RUN" = yes ]; then
    print_plan
    REACHED_END=yes
    trap - ERR
    return 0
  fi
  PROJECT=""
  REGION=""
  NAME=""
  REPO=""
  REPO2=""
  RUN_ANSWER=""
  POC_MODE=""
  POC_FROM=""
  POC_REVISION=""
  POC_RECIPE=""
  POC_COST=""
  POC_ROUNDS=""
  POC_TOLERATED=""
  POC_LEVELS=""
  DIAG_RUN=""
  scratch_up
  write_reader
  guard_xtrace
  guard_belong "$BELONG_SEEN"
  STEPS_STARTED=yes
  step_installation
  step_poc || true
  step_critical || true
  step_hotfix || true
  step_preview || true
  step_postmerge || true
  step_two_repos || true
  step_ref || true
  step_areas || true
  step_watch || true
  step_item_pr || true
  step_item_branches || true
  REACHED_END=yes
  trap - ERR
  # A row that FAILED is a verdict about the modes and it is what the exit
  # status carries; a row that could not be read is not, and 2 says the run
  # could not tell. An unmeasured row is written FAIL in the block too, so it is
  # the count of those that is taken off before the verdict is read. The block
  # prints both either way.
  local failed unread
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  unread=$(grep -c . "$UNMEASURED_FILE" 2>/dev/null || true)
  if [ "${failed:-0}" -gt "${unread:-0}" ]; then
    return 1
  fi
  if [ "${unread:-0}" -gt 0 ]; then
    owner "rows that could not be read: $(tr '\n' ' ' <"$UNMEASURED_FILE")"
    return 2
  fi
  return 0
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 127's reader: every document this check reads is parsed here.

It prints a fixed vocabulary and never a document: a mode, a layer, an area's
name, a figure, a status word, and nothing a person wrote. `number` renders a
figure with six decimals so two runs of the check can be diffed, and a document
that is not the shape this check reads is an error rather than an empty answer.

The one thing it parses that is not JSON is a fugaro.yaml read with `git show`:
there it scans for the `default:` of a `rigor:` block, which is one key read for
one comparison and not a YAML parse.
"""
import base64, json, re, sys


def fail(message):
    print("error=%s" % message, file=sys.stderr)
    sys.exit(1)


def load(path=None):
    raw = open(path, encoding="utf-8").read() if path else sys.stdin.read()
    try:
        return json.loads(raw)
    except ValueError as e:
        fail("not JSON: %s" % e)


def number(v):
    if v is None:
        return "-"
    try:
        return "%.6f" % float(v)
    except (TypeError, ValueError):
        return "-"


def line(key, value):
    print("%s=%s" % (key, value))


def cmd_doctor():
    doc = load()
    project = doc.get("project") or {}
    line("name", project.get("name") or "")
    line("project", project.get("gcp_project") or "")
    line("region", project.get("region") or "")


def cmd_run(ref):
    doc = load()
    if not isinstance(doc, dict) or not isinstance(doc.get("runs"), list):
        fail("not a runs listing")
    rows = [r for r in doc["runs"] if isinstance(r, dict) and r.get("run") == ref]
    if not rows:
        line("found", "no")
        line("rows", "%d" % len(doc["runs"]))
        return
    r = rows[0]
    g = r.get("rigor") or {}
    cost = r.get("cost") or {}
    cf = r.get("changed_files") or {}
    line("found", "yes")
    line("repo", r.get("repo") or "")
    line("mode", g.get("mode") or "-")
    line("from", g.get("from") or "-")
    line("at", g.get("at") or "-")
    line("revision", g.get("revision") or "-")
    line("areas", "%d" % len(g.get("areas") or []))
    for a in g.get("areas") or []:
        line("area", "%s %s %s" % (a.get("name") or "-", a.get("mode") or "-", a.get("at") or "-"))
    line("recipe", r.get("recipe") or "-")
    line("status", r.get("status") or "-")
    line("outcome", r.get("outcome") or "-")
    line("terminal", "yes" if r.get("terminal") else "no")
    line("settled", "yes" if r.get("settled") else "no")
    line("pr", "%d" % (r.get("pr") or 0))
    line("pr_state", r.get("pr_state") or "-")
    line("branch", r.get("branch") or "-")
    line("base", r.get("base_branch") or "-")
    line("cost_model", number(cost.get("model_usd")))
    line("cost_total", number(cost.get("total_usd")))
    line("changed", "%d" % (cf.get("total") or 0))


def cmd_diagnose():
    doc = load()
    row = doc.get("row") or {}
    g = row.get("rigor") or {}
    line("run", row.get("run") or "")
    line("mode", g.get("mode") or "-")
    line("from", g.get("from") or "-")
    tol = doc.get("tolerated") or {}
    line("tolerated", "%d" % (tol.get("total") or 0))
    line("tolerated_block", "yes" if doc.get("tolerated") else "no")
    rounds = 0
    for rv in doc.get("reviews") or []:
        try:
            rounds = max(rounds, int(rv.get("round") or 0))
        except (TypeError, ValueError):
            continue
    line("rounds", "%d" % rounds)
    line("reviews", "%d" % len(doc.get("reviews") or []))
    levels = sorted({(t.get("level") or "") for t in (doc.get("triage") or []) if t.get("level")})
    line("levels", ",".join(levels))
    line("draft_note", "yes" if doc.get("draft_note") else "no")
    line("failure_class", doc.get("failure_class") or "-")


def cmd_preview():
    doc = load()
    knobs = doc.get("knobs") or {}
    line("ceiling", number(doc.get("ceiling_usd")))
    line("per_run", number(doc.get("per_run_usd")))
    line("knobs_first_line", knobs.get("first_line_rounds", "-"))
    line("knobs_review", knobs.get("review_rounds", "-"))
    line("verdict", doc.get("verdict") or "-")
    line("bounded", "yes" if doc.get("bounded") else "no")


def cmd_config(key):
    doc = load()
    for v in doc.get("values") or []:
        if v.get("path") == key:
            value = v.get("value")
            line("found", "yes")
            line("value", "-" if value is None else value)
            line("source", v.get("source") or "-")
            return
    line("found", "no")
    line("value", "-")
    line("source", "-")


def cmd_paths():
    doc = load()
    checked = doc.get("checked") or []
    if not checked:
        fail("the document checked no path")
    a = checked[0]
    m = a.get("matched") or {}
    line("path", a.get("path") or "")
    line("sensitive", "yes" if a.get("sensitive") else "no")
    line("matched", m.get("name") or "-")
    line("category", m.get("category") or "-")
    line("source", m.get("source") or "-")


def cmd_watch():
    """The frame `fugaro watch --once --json` prints, read for the modes on it.

    A frame also carries the budget and the project mode; what this check reads
    is one line per live run — its reference, its mode and the layer behind it —
    and how many of them carry none, which is the drop-and-warn path F40 names
    (an installation whose rules are older than the registry's children).
    """
    doc = load()
    runs = doc.get("runs") or doc.get("live") or []
    nomode = 0
    for r in runs:
        mode = r.get("mode") or "-"
        if mode in ("", "-"):
            nomode += 1
        line("run", (r.get("run") or r.get("slug") or "-"))
        line("run_mode", "%s %s" % (mode, r.get("from") or "-"))
    line("runs", "%d" % len(runs))
    line("nomode", "%d" % nomode)
    line("notice", "yes" if doc.get("notice") else "no")
    line("degraded", "yes" if doc.get("degraded") else "no")


def cmd_backlog():
    doc = load()
    line("dir", doc.get("dir") or "")
    items = doc.get("items") or []
    line("items", "%d" % len(items))
    for it in items:
        line("item", "%s %s %s %s" % (it.get("id") or "-", it.get("date") or "-",
                                      it.get("status") or "-", it.get("path") or "-"))


def cmd_pr():
    doc = load()
    mc = doc.get("mergeCommit") or {}
    files = [f.get("path") for f in (doc.get("files") or []) if f.get("path")]
    commits = [c.get("oid") for c in (doc.get("commits") or []) if c.get("oid")]
    line("state", doc.get("state") or "-")
    line("merged", "yes" if (doc.get("mergedAt") or doc.get("state") == "MERGED") else "no")
    line("head", doc.get("headRefName") or "-")
    line("base", doc.get("baseRefName") or "-")
    line("merge_sha", mc.get("oid") or "-")
    line("files", "%d" % len(files))
    for f in files:
        line("path", f)
    line("commits", "%d" % len(commits))
    for c in commits:
        line("commit", c)


def cmd_pritems(dirpath):
    """The item files out of the `pr` lines this reads on stdin, not a document:
    the caller hands it the answer `cmd_pr` printed, so that the two readings of
    one pull request cannot disagree about what it touches."""
    prefix = dirpath.rstrip("/") + "/"
    for raw in sys.stdin.read().splitlines():
        if not raw.startswith("path="):
            continue
        p = raw[len("path="):]
        if p.startswith(prefix) and p.endswith(".md") and not p.endswith("/README.md"):
            line("path", p)


def cmd_itemfile():
    doc = load()
    raw = doc.get("content")
    if raw is None:
        fail("the contents document carries no content")
    try:
        text = base64.b64decode(raw).decode("utf-8", "replace")
    except Exception as e:
        fail("the content is not base64: %s" % e)
    m = re.search(r"^status:[ \t]*(\S+)", text, re.M)
    line("status", m.group(1) if m else "-")
    line("lines", "%d" % len(text.splitlines()))


def cmd_commitfiles():
    doc = load()
    for f in (doc.get("files") or []):
        if f.get("filename"):
            line("path", f["filename"])


def cmd_onecommit(dirpath):
    """Whether ONE commit carries both an item file and the index beside it.

    The input is the `<oid>|<path>` lines the check collected from gh's own
    commit documents. D31's claim is about one commit, so a flip in one commit
    and a regenerated index in the next reads NO here even though the pull
    request carries both files.
    """
    index = dirpath.rstrip("/") + "/README.md"
    prefix = dirpath.rstrip("/") + "/"
    per = {}
    for raw in sys.stdin.read().splitlines():
        if "|" not in raw:
            continue
        oid, path = raw.split("|", 1)
        per.setdefault(oid, set()).add(path)
    together = [oid for oid, paths in per.items()
                if index in paths and any(p.startswith(prefix) and p.endswith(".md") and p != index for p in paths)]
    line("commits", "%d" % len(per))
    line("one_commit", "yes" if together else "no")
    line("commit", together[0] if together else "-")


def cmd_same(a, b):
    try:
        same = open(a, "rb").read() == open(b, "rb").read()
    except OSError as e:
        fail("cannot compare: %s" % e)
    print("same" if same else "different")


def cmd_ciruns(sha):
    runs = load()
    if not isinstance(runs, list):
        fail("not a workflow run listing")
    hit = [r for r in runs if r.get("headSha") == sha]
    line("found", "yes" if hit else "no")
    line("runs", "%d" % len(runs))
    line("on_merge", "%d" % len(hit))
    if hit:
        line("conclusion", hit[0].get("conclusion") or "-")


def cmd_yaml_rigor_default():
    """The `default:` of a `rigor:` block, scanned out of a fugaro.yaml.

    One key, read for step 7's comparison. It is a scan and not a parse: a
    document that indents its block differently reads as no value here rather
    than as a wrong one, which is why the step prints it as a fact beside the
    record's revision rather than as a verdict on its own.
    """
    text = sys.stdin.read()
    in_rigor = False
    for raw in text.splitlines():
        line_ = raw.rstrip()
        stripped = line_.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if re.match(r"^rigor:[ \t]*$", stripped):
            in_rigor = True
            continue
        if in_rigor:
            m = re.match(r"^default:[ \t]*(\S+)", stripped)
            if m:
                print(m.group(1))
                return
            if not raw.startswith((" ", "\t")):
                in_rigor = False
    print("-")


def main(argv):
    if not argv:
        fail("no subcommand")
    cmd = argv[0]
    if cmd == "doctor":
        cmd_doctor()
    elif cmd == "run":
        cmd_run(argv[1])
    elif cmd == "diagnose":
        cmd_diagnose()
    elif cmd == "preview":
        cmd_preview()
    elif cmd == "config":
        cmd_config(argv[1])
    elif cmd == "paths":
        cmd_paths()
    elif cmd == "watch":
        cmd_watch()
    elif cmd == "backlog":
        cmd_backlog()
    elif cmd == "pr":
        cmd_pr()
    elif cmd == "pritems":
        cmd_pritems(argv[1])
    elif cmd == "itemfile":
        cmd_itemfile()
    elif cmd == "commitfiles":
        cmd_commitfiles()
    elif cmd == "onecommit":
        cmd_onecommit(argv[1])
    elif cmd == "same":
        cmd_same(argv[1], argv[2])
    elif cmd == "ciruns":
        cmd_ciruns(argv[1])
    elif cmd == "yaml-rigor-default":
        cmd_yaml_rigor_default()
    else:
        fail("unknown subcommand %s" % cmd)


main(sys.argv[1:])
READER_EOF
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}
trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
