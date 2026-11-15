#!/usr/bin/env bash
# Check 550 — a repoint with a run in flight, as ONE script YOU run.
#
# Plan task AL3 ships the command this check is about; what it settles is
# docs/account-aliases/implementation-plan.md's "Check 550" and the ruling the
# whole work rests on: an alias is repointed by one command, and the repoint
# moves NEW launches only — a run already in flight keeps the account its own
# launch resolved, whatever the alias points at by the time its review stage
# runs (docs/account-aliases/spec.md §1, §5; D2094, D2095, D2096, D2097).
#
# That is the one claim of the work no hermetic test in this tree can take. The
# tests pin the pieces — the resolver, the in-flight guard in
# internal/runner/agentenv.go, the job variant a repoint adds and never deletes
# — but only a real installation can say that a run launched before a repoint
# and read after it was served by the account the launch resolved.
#
# What it measures:
#
#   * the release the check needs: `fugaro version` and the presence of the
#     command itself (`fugaro accounts alias --help` names `set`). The work is
#     unreleased until 0.25.0, so a build without it stops here with the release
#     named rather than measuring a repoint it cannot make;
#   * the premise, from `fugaro accounts ls --json` read in the sandbox
#     checkout: the block declares an alias, the alias's target is one of the
#     repository's admitted accounts, and a SECOND admitted account exists to
#     repoint onto. `fugaro settings get stages.review.claude_account` and
#     `fugaro settings get default_claude_account` are read beside it, because a
#     repoint only moves a stage's account where a choice key names the alias;
#   * run 1, launched with `fugaro run --json` from the sandbox checkout;
#   * the repoint itself, made WHILE run 1 is in a stage and before it ends:
#     `fugaro accounts alias set <alias> <target> --yes`, the one command
#     D2096 makes the whole of it;
#   * run 1's record: its review stage's account in `stage_accounts` is the
#     account the launch resolved — the pre-repoint target — and not the one the
#     alias points at now. This is the claim;
#   * run 2, launched after the repoint, followed to its own record: its review
#     stage's account IS the new target, so the repoint moved new launches;
#   * the alias moved back, so the sandbox ends the run as it began it.
#
# It creates nothing of its own choosing: two sandbox runs, which are billable,
# and the pull requests they open. Every other call is a read, plus the two
# converges the repoint and the restore run (each publishes the node and applies
# the job set — that is what D2096 means by one command).
#
# Usage:      bash scripts/live-checks/check550.sh --yes
#             bash scripts/live-checks/check550.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check550.sh --dry-run
#             bash scripts/live-checks/check550.sh --help
#             Run it from the sandbox repository's checkout and --sandbox is not
#             needed: the directory you are standing in is the one it reads and
#             launches from. It is a throwaway installation's checkout — never
#             belong — and the alias it moves is that installation's.
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU) and fugaro (the installed
#             release: the whole point is the build you have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    two sandbox runs and the pull requests they open, and two
#             converges (the repoint and the restore). On disk: one scratch
#             directory, removed at the end, holding the commands' output.
# Costs:      TWO sandbox runs and what their tasks cost, on the sandbox's own
#             budget, plus whatever the two converges apply. `--yes` is
#             required. Give the runs a task that takes minutes rather than
#             seconds: the repoint has to land while run 1 is still in a stage,
#             and CHECK550_TASK is where you say so.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check550_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the alias, moved back by the restore
#             step. The two runs' pull requests and their branches are NOT
#             closed by this script: the restore row names them. A run that
#             stopped before the restore leaves the alias on the new target, and
#             the restore row names the one command that moves it back.
# Output:     progress on stderr, ONE block on stdout headed CHECK 550 RESULT,
#             then CHECK 550 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a build older than the release the work ships in. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or a run did not end.
#             A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry account
# NAMES, stage names, statuses and dollars — never a token and never a secret.
#
# Knobs (all CHECK550_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, YES, ALIAS (the alias
# to move; the block's first by default), TARGET (the account to repoint onto;
# the first admitted account that is not the alias's current target by default),
# REPO (the sandbox repository as owner/name, when the checkout's own origin is
# not it), TASK and RECIPE (what both runs are launched
# with), RELEASE (0.25.0: the release this work ships in), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), IMPLEMENT_WAIT_SECONDS (900), POLL_SECONDS (15),
# CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS (600),
# REPOINT_TIMEOUT_SECONDS (1800), DOCTOR_TIMEOUT_SECONDS (300).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a
# real `fugaro` at a real terminal, and a trace of that is a second copy of what
# its own output holds.
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

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
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

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 550 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 550 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk550-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
ALIAS=""
CURRENT=""
TARGET=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
SETTLED=no
# REPOINT_LANDED is set by the one branch of step_repoint that reads the move
# back off the installation. step_relaunch reads it and nothing else: a launch
# made after a repoint that did not land would resolve the same target as the
# first run, so the second half of the claim would be measured against nothing
# — and it would spend the sandbox's money saying so.
REPOINT_LANDED=no
# ALIAS_MOVED is set the moment the repoint's command exits 0, before its result
# is read back. step_restore reads it and nothing else: the move back is owed
# for a repoint that was MADE, and a run that never made one — a premise that
# did not hold, a launch that refused, a command that hung — has nothing to put
# back. REPOINT_LANDED is the stricter fact (the installation really reads the
# new target) and the one the second launch is gated on.
ALIAS_MOVED=no
LAUNCH_ARGS=()
RUN2_ID=""
RUN2_REF=""
RUN2_SETTLED=no
# PREMISE_OK is set by the one branch of step_premise that establishes the state
# the two runs are a reading of. step_run reads it and nothing else: a run
# launched after a failed premise would spend the sandbox's money measuring a
# state this check could not name.
PREMISE_OK=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk550.XXXXXXXX") || die "Check 550 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching the owner's home,
  # and reading it unguarded would abort cleanup under `set -u` on a machine
  # where HOME is unset, leaving the scratch directory behind.
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
    say "CHECK550_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report an account name the release did not write the day the
# document is re-wrapped. Every reader prints `key=value` lines or
# `\t`-separated rows, so a shell can hold them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("gcp_project=%s" % flat(project.get("gcp_project")))
    print("name=%s" % flat(project.get("name")))
elif what == "accounts":
    print("repo=%s" % flat(doc.get("repo")))
    print("complete=%s" % yes(doc.get("complete")))
    names = [flat(n) for n in (doc.get("accounts") or []) if flat(n)]
    print("names=%s" % ",".join(names))
    for name, target in sorted((doc.get("aliases") or {}).items()):
        print("alias\t%s\t%s" % (flat(name), flat(target)))
    print("alias_count=%d" % len(doc.get("aliases") or {}))
elif what == "launch":
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            flat(row.get("run_id")),
            flat(row.get("status")),
            yes(row.get("terminal")),
            flat(row.get("stage")),
            flat(row.get("run")),
        ))
elif what == "record":
    cost = doc.get("cost") or {}
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("outcome=%s" % flat(doc.get("outcome")))
    print("failure_class=%s" % flat(doc.get("failure_class")))
    print("reason=%s" % flat(doc.get("reason")))
    print("oauth_account=%s" % flat(doc.get("oauth_account")))
    print("cost_usd=%s" % (cost.get("model_usd") or 0))
    stages = doc.get("stages") or []
    print("stage_count=%d" % len(stages))
    for stage in stages:
        print("stage\t%s\t%s" % (flat(stage.get("name")), flat(stage.get("model"))))
    accounts = doc.get("stage_accounts") or {}
    for stage, account in sorted(accounts.items()):
        print("account\t%s\t%s" % (flat(stage), flat(account)))
    split = " ".join("%s=%s" % (flat(s), flat(a)) for s, a in sorted(accounts.items()))
    print("accounts_split=%s" % split)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST [ARG]: answer one query into a file, and fail the
# step when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST [ARG]
  if [ $# -ge 4 ]; then
    python3 "$SCRATCH/read.py" "$1" "$2" "$4" >"$3" 2>"$3.err"
  else
    python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
  fi
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# alias_target FILE NAME — the account an alias stands for, out of the block
# `accounts ls --json` prints.
alias_target() { # alias_target FILE NAME
  awk -F'\t' -v name="$2" '$1 == "alias" && $2 == name { print $3; exit }' "$1" 2>/dev/null || true
}

# alias_names FILE — every alias the block declares, comma-separated.
alias_names() { # alias_names FILE
  awk -F'\t' '$1 == "alias" { print $2 }' "$1" 2>/dev/null | paste -sd, - 2>/dev/null || true
}

# account_names FILE — every account the repository may choose among, one per
# line, `default` included.
account_names() { # account_names FILE
  field "$1" names | tr ',' '\n' | sed '/^$/d'
}

has_account() { # has_account FILE NAME
  # Read to the end rather than let grep -q close the pipe: under pipefail an
  # early close fails the pipeline of a producer that is still writing.
  account_names "$1" | grep -x -- "$2" >/dev/null
}

# pick_target FILE — the account to repoint onto: the first admitted account
# that is neither the one the alias already stands for nor an alias's own name,
# preferring a named account over `default`. A named account is what makes the
# block readable — `stage_accounts` names it, and `default` is the repository's
# own unnamed one, which a repoint onto says less with — and `default` is still
# taken when it is the only one left, because a repoint onto it is a move the
# command really makes.
pick_target() { # pick_target FILE
  local names named
  names=$(account_names "$1" 2>/dev/null | grep -vx -- "$CURRENT" | grep -vx -- "$ALIAS" || true)
  named=$(printf '%s\n' "$names" | grep -vx -- default | head -n 1 || true)
  if [ -n "$named" ]; then
    printf '%s\n' "$named"
    return 0
  fi
  printf '%s\n' "$names" | head -n 1
}

# stage_account FILE STAGE — the account the record says served one stage, out
# of the record's own `stage_accounts` map. An absent key and a stage served by
# no named account are different readings, and both are "".
stage_account() { # stage_account FILE STAGE
  awk -F'\t' -v name="$2" '$1 == "account" && $2 == name { print $3; exit }' "$1" 2>/dev/null || true
}

# stage_names FILE — the stages the record lists, space-separated, for a row
# that has to say which stages a run had when the one it looked for is absent.
stage_names() { # stage_names FILE
  awk -F'\t' '$1 == "stage" { printf "%s ", $2 }' "$1" 2>/dev/null || true
}

# launch_args — the extra arguments both launches take, kept in an array rather
# than in a `${RECIPE:+--recipe "$RECIPE"}` expansion: the quotes inside such an
# expansion are literal text to the shell, so a recipe with a space in its name
# would reach `fugaro run` as three arguments.
launch_args() {
  LAUNCH_ARGS=()
  if [ -n "$RECIPE" ]; then LAUNCH_ARGS=(--recipe "$RECIPE"); fi
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

row_field2() { # row_field2 COLUMN — the same, for the second run
  awk -F'\t' -v id="$RUN2_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows2.txt" 2>/dev/null
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. Every command here names the project through
# the checkout it is run in, and the record read is the one exception.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a run that
# failed, a record that is not there), and a trap inherited here would turn each
# of them into "an internal error" and a status of 2 before the step could
# report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  # `>` and not `>>`: this check polls two runs with the same key, and a file
  # that accumulated one JSON document per poll would stop being a document a
  # reader could parse from the second poll on — the row would read as "the
  # command printed nothing" for the rest of the wait.
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  # The poll is a tenth of a second and the bound is counted in tenths: a read
  # that answers at once — every one of them, when the installation is healthy —
  # would otherwise cost this check a whole second each, and the owner watches
  # the check rather than the clock.
  local pid=$! ticks=0 max=$((secs * 10))
  while kill -0 "$pid" 2>/dev/null; do
    [ "$ticks" -lt "$max" ] || break
    sleep 0.1
    ticks=$((ticks + 1))
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

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check550.sh --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is the owner's, and it drives a command whose
# write a coding agent's session is refused — `accounts alias set` is refused
# there by name (D2097). Refusing here rather than letting that command refuse
# is what makes the block say why: a session that ran this far would report the
# refusal as a finding about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 550 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

# version_at_least HAVE NEED: 0 when HAVE is a release number at or above NEED,
# 1 when it is a release number below it, 2 when HAVE is not a release number at
# all (a dev build, a commit) — the third answer is why this is not a comparison
# alone: a build that names no version is decided by the feature probe below and
# never by a guess at what "dev" sorts as.
version_at_least() { # version_at_least HAVE NEED
  awk -v have="$1" -v need="$2" 'BEGIN {
    n = split(have, h, ".")
    m = split(need, k, ".")
    for (i = 1; i <= n; i++) {
      if (h[i] !~ /^[0-9]+$/) exit 2
    }
    for (i = 1; i <= (n > m ? n : m); i++) {
      a = (i <= n ? h[i] + 0 : 0)
      b = (i <= m ? k[i] + 0 : 0)
      if (a > b) exit 0
      if (a < b) exit 1
    }
    exit 0
  }'
}

# step_release: the gate every check of an unreleased feature carries. The work
# ships in 0.25.0, and a build that predates it has neither the command nor the
# resolver behind it, so a repoint here would measure nothing. Both probes are
# offline: `fugaro version` and the usage line `accounts alias` prints.
step_release() {
  local v feature=no
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  local usage
  usage=$("$FUGARO_BIN" accounts alias --help 2>/dev/null || true)
  case "$usage" in *set*) feature=yes ;; esac
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no 'accounts alias' command: 'fugaro accounts alias --help' does not name 'set', so neither the command nor the resolver the two runs below are a reading of is in this build. Check 550 needs release ${RELEASE} (the account-aliases work) or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no 'accounts alias': this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'accounts alias' names 'set': this build carries the work the two runs below measure"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the account-aliases work ships in: the command exists in this build only if the probe above is right about a version string that says otherwise, and a repoint measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the probe decides, and 'fugaro accounts alias --help' names 'set', so this build carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  fact bound PASS "TWO sandbox runs and what their tasks cost, on the sandbox's own budget, plus the two converges the repoint and the restore apply (the policy node and the job set, each). Every other call this check makes is a read (doctor, accounts ls, settings get, the runs list, the record). --yes is required to launch anything"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the runs and their records are that project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (run this from the sandbox repository's checkout, or pass its path)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the runs this check launches are launched from one"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured sandbox "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check launches from this checkout and reads the records in this project's bucket"
}

# step_premise: the state the two runs are a reading of. `accounts ls --json` is
# the read that names the alias and its target — the block is local and this
# machine's, so nothing else can say what the alias stands for — and the two
# `settings get` reads are what say a choice key names it: a repoint only moves
# a stage's account where the key that chooses that stage's account holds the
# alias (spec §4), and a run whose keys name the account directly would be a
# reading of nothing.
step_premise() {
  if [ -z "$PROJECT" ]; then
    unmeasured premise "there is no project to read the alias in: the row above says why"
    return 0
  fi
  say "reading the accounts: fugaro accounts ls --json"
  run_bounded "$CLI_TIMEOUT" accounts "$SANDBOX" "$FUGARO_BIN" accounts ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured premise "fugaro accounts ls --json did not answer (exit ${BOUNDED_STATUS}): the alias and its target are what the repoint below moves, and neither could be read. Its output is in $SCRATCH/accounts.out and $SCRATCH/accounts.err"
    return 0
  fi
  if ! read_json accounts "$SCRATCH/accounts.out" "$SCRATCH/accounts.txt"; then
    unmeasured premise "fugaro accounts ls --json printed a document this check could not read: what it printed is in $SCRATCH/accounts.out"
    return 0
  fi
  if [ -n "$REPO" ] && [ "$(field "$SCRATCH/accounts.txt" repo)" != "$REPO" ]; then
    fact premise FAIL "fugaro accounts ls --json read the accounts of $(field "$SCRATCH/accounts.txt" repo) and CHECK550_REPO names ${REPO}: the alias, its target and the accounts this check may repoint onto would all be another repository's, so the two runs below would be a reading of a state this check did not name. Drop the knob — the check reads the repository from the checkout's own origin — or set it to the key that command printed"
    return 0
  fi
  if [ "$(field "$SCRATCH/accounts.txt" complete)" != true ]; then
    unmeasured premise "fugaro accounts ls --json answered with complete=false (note: $(field "$SCRATCH/accounts.txt" repo)): the list may be short the repository's real accounts, so this check cannot say which account the alias may be repointed onto. Its output is in $SCRATCH/accounts.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/accounts.txt" alias_count)" = 0 ]; then
    fact premise FAIL "the block at this installation declares no alias at all: Check 550 moves one, and there is nothing here to move. Declare one at your own terminal — fugaro accounts alias set <name> <account>, which needs an account of claude_accounts and a choice key naming the alias — and run this check again"
    return 0
  fi
  if [ -z "$ALIAS" ]; then
    ALIAS=$(alias_names "$SCRATCH/accounts.txt" | cut -d, -f1)
  fi
  CURRENT=$(alias_target "$SCRATCH/accounts.txt" "$ALIAS")
  if [ -z "$CURRENT" ]; then
    fact premise FAIL "the block declares no alias named '${ALIAS}' (it declares: $(alias_names "$SCRATCH/accounts.txt")): the alias to move is the block's first by default, and CHECK550_ALIAS names another. Read the names with 'fugaro accounts ls'"
    return 0
  fi
  if ! has_account "$SCRATCH/accounts.txt" "$CURRENT"; then
    fact premise FAIL "the alias ${ALIAS} stands for '${CURRENT}', which is not one of the accounts this repository may choose among (it may choose: $(field "$SCRATCH/accounts.txt" names)): an alias whose target is not admitted is a configuration this check will not repoint, because the run it launches would be refused before any stage. Give the account an allow_data_to for this repository (fugaro accounts add), or point the alias at one that has it"
    return 0
  fi
  if [ -z "$TARGET" ]; then
    TARGET=$(pick_target "$SCRATCH/accounts.txt")
  fi
  if [ -z "$TARGET" ]; then
    fact premise FAIL "this repository may choose no account but '${CURRENT}', which is the one the alias ${ALIAS} already stands for: a repoint needs a second admitted account to move onto. Add one (fugaro accounts add) and give it an allow_data_to for this repository, or name one with CHECK550_TARGET=<account>"
    return 0
  fi
  if [ "$TARGET" = "$CURRENT" ]; then
    fact premise FAIL "the account to repoint onto is the one the alias ${ALIAS} already stands for (${CURRENT}): a repoint onto the same account moves nothing and both runs would read the same target, so nothing here would be measured. Name another with CHECK550_TARGET=<account>"
    return 0
  fi
  if ! has_account "$SCRATCH/accounts.txt" "$TARGET"; then
    fact premise FAIL "the account to repoint onto ('${TARGET}') is not one this repository may choose among (it may choose: $(field "$SCRATCH/accounts.txt" names)): the second run would be refused before any stage, so the repoint's own half of this check could not be read. Give that account an allow_data_to for this repository (fugaro accounts add), or name another with CHECK550_TARGET=<account>"
    return 0
  fi
  # The choice keys: a repoint only moves a stage's account where the key that
  # chooses it holds the alias.
  local stage_value default_value named=""
  run_bounded "$CLI_TIMEOUT" stages "$SANDBOX" "$FUGARO_BIN" settings get stages.review.claude_account
  stage_value=$(cat "$SCRATCH/stages.out" 2>/dev/null || true)
  run_bounded "$CLI_TIMEOUT" default "$SANDBOX" "$FUGARO_BIN" settings get default_claude_account
  default_value=$(cat "$SCRATCH/default.out" 2>/dev/null || true)
  case "$stage_value" in *"$ALIAS"*) named="stages.review.claude_account" ;; esac
  if [ -z "$named" ]; then
    case "$default_value" in *"$ALIAS"*) named="default_claude_account" ;; esac
  fi
  if [ -z "$named" ]; then
    fact premise FAIL "no choice key names the alias ${ALIAS}: stages.review.claude_account and default_claude_account are read here and neither holds it, so a repoint of this alias would move no stage's account and both runs would be served by whatever the keys name directly. Point one of them at the alias at your own terminal (fugaro settings set stages.review.claude_account ${ALIAS} --layer local) and run this check again"
    return 0
  fi
  PREMISE_OK=yes
  fact premise PASS "${ALIAS} stands for ${CURRENT} and ${named} names it, and this repository may also choose ${TARGET}: the check repoints ${ALIAS} onto ${TARGET} while run 1 is in a stage, and run 1's review stage must still be served by ${CURRENT}"
}

# step_run: run 1, launched before the repoint. `--json` is what names the run
# this check then follows; a launch that prints no run ID is a finding about the
# launcher, not a row this check could not measure.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  # A run is launched only once the row above has named the state it is a
  # reading of. A run launched after a failed premise would spend the sandbox's
  # money on a reading nobody could grade, and the FAIL row above is already the
  # finding.
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured run-launched "nothing was launched: the premise row above did not establish the state this run is a reading of, and a run launched without it would spend the sandbox's money on a reading nobody could grade. Settle the row above and run this check again"
    return 0
  fi
  say "launching run 1 from ${SANDBOX} (a real sandbox run, with what its task costs)"
  launch_args
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${LAUNCH_ARGS[@]+"${LAUNCH_ARGS[@]}"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the check needs a run to repoint under, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch is the step this check follows, and its own --json view is how the run is named. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)): this is the run the repoint below is made under, and the one whose review stage must keep the account this launch resolved"
}

# step_repoint: the repoint, made while run 1 is in a stage and before it ends.
# The row says which stage the run was in when the command landed, because
# "in flight" is the whole premise: a repoint made after the run ended measures
# nothing, and one made before it started is not a repoint under a run at all.
step_repoint() {
  if [ -z "$RUN_ID" ]; then
    unmeasured repointed "there is no run to repoint under: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to reach a stage (up to ${IMPLEMENT_WAIT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 1)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$IMPLEMENT_WAIT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 1
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      run_status=$(row_field 3)
      ended=$(row_field 4)
      stage=$(row_field 5)
      RUN_REF=$(row_field 6)
      if [ "$ended" = true ]; then break; fi
      if [ -n "$stage" ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" = true ]; then
    fact repointed FAIL "${RUN_ID} had already ended when the alias was to be moved (status=${run_status:-unknown}): a repoint under a run that has ended is not a repoint under a run in flight, and the claim this check exists for would not be measured. The task is too short for this check — give it one that takes minutes rather than seconds with CHECK550_TASK=<text> and run this check again"
    return 0
  fi
  if [ -z "$stage" ]; then
    unmeasured repointed "${RUN_ID} was still in no stage after ${IMPLEMENT_WAIT}s (status=${run_status:-unknown}): the check could not tell whether the run was in flight, and a repoint made on a guess would be a reading nobody could grade. Watch it with 'fugaro watch ${RUN_ID}' and run this check again, or raise CHECK550_IMPLEMENT_WAIT_SECONDS"
    return 0
  fi
  say "run 1 is in '${stage}': repointing ${ALIAS} -> ${TARGET} now (fugaro accounts alias set)"
  run_bounded "$REPOINT_TIMEOUT" repoint "$SANDBOX" "$FUGARO_BIN" accounts alias set "$ALIAS" "$TARGET" --yes
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured repointed "fugaro accounts alias set did not finish inside ${REPOINT_TIMEOUT}s and was ended: the alias may or may not have moved, and the rows below are read against a state this check cannot name. What it printed is in $SCRATCH/repoint.out and $SCRATCH/repoint.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact repointed FAIL "fugaro accounts alias set ${ALIAS} ${TARGET} exited ${BOUNDED_STATUS}: the repoint is the command this check is about, and a refusal says either that the alias could not be moved or that the installation could not converge. What it printed is in $SCRATCH/repoint.out and $SCRATCH/repoint.err"
    return 0
  fi
  ALIAS_MOVED=yes
  # The move is read back off the installation rather than taken from the
  # command's own exit status: a repoint that printed a line and wrote nothing
  # would leave every row below a reading of the wrong target.
  run_bounded "$CLI_TIMEOUT" after "$SANDBOX" "$FUGARO_BIN" accounts ls --json
  if [ "$BOUNDED_STATUS" != 0 ] || ! read_json accounts "$SCRATCH/after.out" "$SCRATCH/after.txt"; then
    unmeasured repointed "the alias could not be read back after the repoint (exit ${BOUNDED_STATUS}): the rows below are read against the target the installation holds, and this check cannot name it. What fugaro printed is in $SCRATCH/after.out and $SCRATCH/after.err"
    return 0
  fi
  local moved
  moved=$(alias_target "$SCRATCH/after.txt" "$ALIAS")
  if [ "$moved" != "$TARGET" ]; then
    fact repointed FAIL "the block still reads ${ALIAS} -> ${moved:-nothing} after 'accounts alias set ${ALIAS} ${TARGET}' exited 0: the command reported a move the installation does not hold, and nothing below would be a reading of it. What fugaro printed is in $SCRATCH/repoint.out"
    return 0
  fi
  REPOINT_LANDED=yes
  fact repointed PASS "fugaro accounts alias set moved ${ALIAS} from ${CURRENT} to ${TARGET} while ${RUN_ID} was in '${stage}' (read back from fugaro accounts ls --json, not taken from the command's exit status): this is the repoint the claim below is about"
}

# step_terminal: run 1 has to end. A check that read a record while a run was
# still going would read the previous run's, and the record of a run that is
# still working is not the reading this check is after.
step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 1
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
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}): the run that was in flight over the repoint reached its end, which is the state its record is written in"
}

# read_run_record ID REF KEY PREFIX: the record, read from the runs bucket as
# the owner would read it. The record's own run_id is what ties it to the run
# this check launched.
read_run_record() { # read_run_record RUN_ID REF KEY
  local id=$1 ref=$2 key=$3
  case "$ref" in
  */"$id") ;;
  *)
    unmeasured "$key" "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=${ref:-none}), so there was no key to read: the record of run ${id} lives under the repository's slug, and the slug is what that row's ref carries"
    return 1
    ;;
  esac
  local object="runs/${ref}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${object}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${object}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact "$key" FAIL "gs://fugaro-runs-${PROJECT}/${object} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the account each stage was served by is written. What gcloud printed is in $SCRATCH/record.err"
    return 1
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact "$key" FAIL "${object} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 1
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$id" ]; then
    fact "$key" FAIL "${object} records run_id=${recorded} and this check launched ${id}: the record at that key is another run's, and nothing below can be read as this run's"
    return 1
  fi
  return 0
}

# step_record: run 1's record. `stage_accounts` is the field this whole check is
# about (D876): the stage name to the account that served it, written where the
# stage's account is chosen.
step_record() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record: a record read while a run is still working is whatever was there before it. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  read_run_record "$RUN_ID" "$RUN_REF" result-json || return 0
  fact result-json PASS "${RUN_ID}'s record was read from the bucket (status=$(field "$SCRATCH/record.txt" status), outcome=$(field "$SCRATCH/record.txt" outcome), $(field "$SCRATCH/record.txt" stage_count) stage(s), oauth_account=$(field "$SCRATCH/record.txt" oauth_account)): its stage_accounts is what the row below is read against"
}

# step_kept: THE CLAIM. Run 1 was launched before the repoint, so its review
# stage must have been served by the account its launch resolved — the alias's
# target at launch time — and not by the one the alias points at now.
step_kept() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured kept-account "there is no record to read the review stage's account from: the row above says why"
    return 0
  fi
  local served split
  split=$(field "$SCRATCH/record.txt" accounts_split)
  served=$(stage_account "$SCRATCH/record.txt" review)
  if [ -z "$served" ]; then
    fact kept-account FAIL "run ${RUN_ID}'s record carries no stage_accounts entry for 'review' (it carries: ${split:-nothing}): the run was launched on a repository whose stages.review.claude_account or default_claude_account names the alias ${ALIAS}, so a mixed run's review stage is served by that account and the record says which — a record without the entry cannot be read as the launch's account surviving the repoint. Its stages are $(stage_names "$SCRATCH/record.txt"), and the record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$served" != "$CURRENT" ]; then
    fact kept-account FAIL "run ${RUN_ID}'s review stage was served by '${served}' and its launch resolved '${CURRENT}': the alias was repointed to ${TARGET} while the run was in flight, and this run did NOT keep the account it was launched on — which is the one thing this check exists to measure. The record is in $SCRATCH/record.out, and its own oauth_account reads $(field "$SCRATCH/record.txt" oauth_account)"
    return 0
  fi
  fact kept-account PASS "run ${RUN_ID}'s review stage was served by ${CURRENT} — the account its launch resolved, before the alias was moved to ${TARGET} — read from the record's own stage_accounts (${split}). The repoint did not reach a run in flight"
}

# step_relaunch: run 2, launched after the repoint. It is what says the repoint
# moved NEW launches, which is the other half of the ruling.
step_relaunch() {
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured relaunched "nothing was launched: the premise row above did not establish the state this run is a reading of"
    return 0
  fi
  if [ -z "$CURRENT" ] || [ -z "$TARGET" ]; then
    unmeasured relaunched "this check does not know the two targets the second run is read against: the rows above say why"
    return 0
  fi
  if [ "$REPOINT_LANDED" != yes ]; then
    unmeasured relaunched "nothing was launched: the alias was not moved onto ${TARGET} (the row above says why), so a launch now would resolve the same account as the first run and the repoint's own half of this check would be measured against nothing. Settle the row above and run this check again"
    return 0
  fi
  say "launching run 2 from ${SANDBOX}, after the repoint (a real sandbox run, with what its task costs)"
  launch_args
  run_bounded "$LAUNCH_TIMEOUT" launch2 "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${LAUNCH_ARGS[@]+"${LAUNCH_ARGS[@]}"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured relaunched "the second launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch2.out and $SCRATCH/launch2.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact relaunched FAIL "the second fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX} after the alias moved to ${TARGET}: a launch that refuses after a repoint says the repoint left the installation in a state no run starts on — which is the other failure this check is for. What it printed is in $SCRATCH/launch2.out and $SCRATCH/launch2.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch2.out" "$SCRATCH/launch2.txt"; then
    fact relaunched FAIL "the second fugaro run --json printed a document this check could not read: what it printed is in $SCRATCH/launch2.out"
    return 0
  fi
  RUN2_ID=$(field "$SCRATCH/launch2.txt" run_id)
  if [ -z "$RUN2_ID" ]; then
    fact relaunched FAIL "the second fugaro run --json named no run: this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch2.out"
    return 0
  fi
  if [ "$RUN2_ID" = "$RUN_ID" ]; then
    fact relaunched FAIL "the second launch named the same run as the first (${RUN2_ID}): the two runs are different readings — one launched before the repoint and one after — and one run cannot be both. What it printed is in $SCRATCH/launch2.out"
    return 0
  fi
  fact relaunched PASS "fugaro run launched ${RUN2_ID} on ${LAUNCH_REPO} after the alias moved to ${TARGET}: it is the launch the repoint was supposed to reach, and the row below reads its review stage"
}

# step_relaunch_record: run 2 has to end, and its record has to be read, before
# its review stage's account can be graded.
step_relaunch_record() {
  if [ -z "$RUN2_ID" ]; then
    unmeasured relaunch-record "there is no second run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN2_ID} to end (up to ${RUN_TIMEOUT}s)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows2 "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 1
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows2.out" "$SCRATCH/rows2.txt"; then
      run_status=$(row_field2 3)
      ended=$(row_field2 4)
      stage=$(row_field2 5)
      RUN2_REF=$(row_field2 6)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured relaunch-record "${RUN2_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record. Watch it with 'fugaro watch ${RUN2_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  RUN2_SETTLED=yes
  read_run_record "$RUN2_ID" "$RUN2_REF" relaunch-record || return 0
  fact relaunch-record PASS "${RUN2_ID}'s record was read from the bucket (status=$(field "$SCRATCH/record.txt" status), $(field "$SCRATCH/record.txt" stage_count) stage(s)) after ${waited}s: its stage_accounts is what the row below is read against"
}

# step_new_target: the other half of the ruling. A launch made after the repoint
# is served by the new target, so the repoint moved new launches and the
# installation converged behind it (D2095, D2096).
step_new_target() {
  if [ "$RUN2_SETTLED" != yes ] || [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured new-target "there is no second record to read the review stage's account from: the row above says why"
    return 0
  fi
  local served split
  split=$(field "$SCRATCH/record.txt" accounts_split)
  served=$(stage_account "$SCRATCH/record.txt" review)
  if [ -z "$served" ]; then
    fact new-target FAIL "run ${RUN2_ID}'s record carries no stage_accounts entry for 'review' (it carries: ${split:-nothing}): the launch was made after the alias moved to ${TARGET}, so its review stage should have been served by ${TARGET} and the record should say so. Its stages are $(stage_names "$SCRATCH/record.txt"), and the record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$served" != "$TARGET" ]; then
    fact new-target FAIL "run ${RUN2_ID}'s review stage was served by '${served}' and the alias stood for ${TARGET} when it was launched: a repoint moves NEW launches (D2094), and a launch made after it that is served by another account is the repoint not having reached the job set — the converge behind the command (D2096) or the resolver, and either way the half of the ruling this run is for. The record is in $SCRATCH/record.out"
    return 0
  fi
  fact new-target PASS "run ${RUN2_ID}'s review stage was served by ${TARGET} — the account the alias stood for when it was launched, after the repoint from ${CURRENT} — read from the record's own stage_accounts (${split}). The repoint moved new launches"
}

# step_restore: the alias moved back, so the sandbox ends the run as it began
# it. It is a step and not a note: a check that left the owner's installation on
# a target the check chose is a check that changed something of theirs.
step_restore() {
  if [ "$ALIAS_MOVED" != yes ] || [ -z "$CURRENT" ]; then
    RESTORE_VERDICT=PASS
    RESTORE_NOTE="nothing was moved: this run never repointed ${ALIAS:-the alias} (the rows above say why), so there is nothing to put back and the installation is where it was. The scratch directory held the commands' output"
    return 0
  fi
  say "putting the alias back: ${ALIAS} -> ${CURRENT}"
  run_bounded "$REPOINT_TIMEOUT" restore "$SANDBOX" "$FUGARO_BIN" accounts alias set "$ALIAS" "$CURRENT" --yes
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the alias ${ALIAS} is left standing for ${TARGET}: 'fugaro accounts alias set ${ALIAS} ${CURRENT} --yes' exited ${BOUNDED_STATUS} ($([ "$BOUNDED_TIMED_OUT" = yes ] && echo "ended at ${REPOINT_TIMEOUT}s" || echo "it refused")). Run that one command yourself at your own terminal — it is the whole of it — and read $SCRATCH/restore.out and $SCRATCH/restore.err for what it printed"
    fact restore FAIL "$RESTORE_NOTE"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" restored "$SANDBOX" "$FUGARO_BIN" accounts ls --json
  local back=""
  if [ "$BOUNDED_STATUS" = 0 ] && read_json accounts "$SCRATCH/restored.out" "$SCRATCH/restored.txt"; then
    back=$(alias_target "$SCRATCH/restored.txt" "$ALIAS")
  fi
  if [ "$back" != "$CURRENT" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the alias ${ALIAS} reads ${back:-nothing} after the move back was asked for and exited 0: run 'fugaro accounts alias set ${ALIAS} ${CURRENT} --yes' yourself at your own terminal. The two runs ${RUN_ID:-none} and ${RUN2_ID:-none} are real sandbox runs and each opened a pull request: close those and delete their branches yourself once you have read them"
    fact restore FAIL "$RESTORE_NOTE"
    return 0
  fi
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="the alias ${ALIAS} stands for ${CURRENT} again, read back from fugaro accounts ls --json, so this installation ends the run as it began it. The two runs ${RUN_ID:-none} and ${RUN2_ID:-none} are real sandbox runs and each opened a pull request: close those and delete their branches yourself once you have read them. The scratch directory held the commands' output"
}

cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to read facts and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done.
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  # The fallback row is only read when step_restore never ran: print_block takes
  # the FIRST line for a key, and a run that reached the step has already
  # recorded one.
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore step did not run, so the alias ${ALIAS:-?} may be left standing for ${TARGET:-?}: put it back with 'fugaro accounts alias set ${ALIAS:-?} <the account it stood for>' at your own terminal}"
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
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 550 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro accounts alias --help — the release ${RELEASE} gate: the build must carry the alias command"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [read] fugaro accounts ls --json — the alias, the account it stands for, and the accounts this repository may choose among"
  say "  [read] fugaro settings get stages.review.claude_account and fugaro settings get default_claude_account — whether a choice key names the alias"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --json \"${TASK}\" — run 1, launched BEFORE the repoint"
  say "  [read] fugaro runs ls --repo <the repository> --json --limit 1 until run 1 is in a stage"
  say "  [network, BILLABLE, WRITES] fugaro accounts alias set ${ALIAS:-<the first alias the block declares>} ${TARGET:-<another admitted account>} --yes — the repoint, under run 1, and the converge behind it (the policy node and the job set)"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run 1>/result.json — its stage_accounts must name the launch's account for review"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --json \"${TASK}\" — run 2, launched AFTER the repoint, followed to its own record, whose review stage must name the new target"
  say "  [network, WRITES] fugaro accounts alias set ${ALIAS:-<the alias>} <the account it stood for> --yes — the move back, so the sandbox ends the run as it began it"
  say "  [nothing else] no config key is written by this check, and the scratch directory is removed at the end"
  fact bound PASS "planned: TWO sandbox runs and what their tasks cost, plus the two converges. --dry-run runs nothing, and a real run requires --yes"
  for key in release sandbox premise run-launched repointed terminal result-json \
    kept-account relaunched relaunch-record new-target; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no accounts read, no run, no repoint and no move back"
  # A dry run moved nothing, so the fallback row finish appends reads PASS rather
  # than the FAIL a run that stopped before its restore deserves.
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was moved: --dry-run read the release and the bound and stopped there, so the alias is wherever it was and no run was launched"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  premise run-launched repointed terminal result-json kept-account relaunched
  relaunch-record new-target restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 550 RESULT\n' >&3
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
  printf 'CHECK 550 RESULT END (exit %s)\n' "$status" >&3
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
SANDBOX_CLI=""
TASK_CLI=""
RECIPE_CLI=""
ALIAS_CLI=""
TARGET_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what both runs are launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text both runs are launched with; give it something that takes minutes rather than seconds, because the repoint has to land while run 1 is in a stage"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
    ;;
  --alias)
    [ $# -ge 2 ] || die "--alias needs the name of an alias of this installation's claude_account_aliases block"
    ALIAS_CLI=$2
    shift 2
    ;;
  --target)
    [ $# -ge 2 ] || die "--target needs the name of an account this repository may choose among, and one the alias does not already stand for"
    TARGET_CLI=$2
    shift 2
    ;;
  --yes)
    YES_CLI=1
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

# The checkout you are standing in, unless --sandbox names another: the plan's
# own reading of this check is that you type its path and nothing else, and the
# directory you run it from IS the sandbox when you are standing in it.
SANDBOX=${CHECK550_SANDBOX:-${SANDBOX_CLI:-$PWD}}
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
# The repository as owner/name, when the owner wants to pin it: the check reads
# it from the checkout's own origin (`accounts ls --json` prints it), and the
# knob exists so a run in a checkout whose origin is not the repository under
# test says so rather than reading another repository's accounts.
REPO=${CHECK550_REPO:-}
TASK=${CHECK550_TASK:-${TASK_CLI:-"Read docs/account-aliases/spec.md and write one short paragraph to docs/account-aliases/notes.md saying what an alias is, then commit it. Do not open a pull request."}}
RECIPE=${CHECK550_RECIPE:-${RECIPE_CLI:-}}
ALIAS=${CHECK550_ALIAS:-${ALIAS_CLI:-}}
TARGET=${CHECK550_TARGET:-${TARGET_CLI:-}}
RELEASE=${CHECK550_RELEASE:-0.25.0}
YES=${CHECK550_YES:-$YES_CLI}
DRY_RUN=${CHECK550_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK550_KEEP_SCRATCH:-}
REGION=${CHECK550_REGION:-us-east5}
RUN_TIMEOUT=${CHECK550_RUN_TIMEOUT_SECONDS:-3600}
IMPLEMENT_WAIT=${CHECK550_IMPLEMENT_WAIT_SECONDS:-900}
POLL_SECONDS=${CHECK550_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK550_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK550_LAUNCH_TIMEOUT_SECONDS:-600}
REPOINT_TIMEOUT=${CHECK550_REPOINT_TIMEOUT_SECONDS:-1800}
DOCTOR_TIMEOUT=${CHECK550_DOCTOR_TIMEOUT_SECONDS:-300}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 550 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  step_release
  step_bound
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$YES" ]; then
    die "Check 550 launches TWO sandbox runs and spends what their tasks cost, and it repoints an alias of this installation, so it asks for --yes. Nothing has been launched and nothing has been moved. Type: bash scripts/live-checks/check550.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_premise
  step_run
  step_repoint
  step_terminal
  step_record
  step_kept
  step_relaunch
  step_relaunch_record
  step_new_target
  step_restore
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
