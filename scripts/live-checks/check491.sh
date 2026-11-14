#!/usr/bin/env bash
# Check 491 — the deferrals are filed, one debt task per group, and listed.
#
# Plan task T-TR13 writes this check; what it settles is
# docs/post-review-triage/implementation-plan.md's "Checks" block and the task
# it builds on: T-TR10 (`fugaro followups RUN` after the parent's pull request
# is merged launches one debt task per group, each at the parent's applied mode,
# and `fugaro runs ls --debt` lists them). Check 490 leaves the run this one
# reads: its pull request carries the deferral, and its record carries the
# groups.
#
# What it measures:
#
#   * the release the check needs: `fugaro version` and the presence of the
#     feature itself (`fugaro followups --help` answers, and `fugaro runs ls
#     --help` names `--debt`). The work is unreleased until 0.24.0, so a build
#     without it stops here with the release named rather than measuring a
#     command that is not there;
#   * the parent run's plan (`fugaro followups RUN --json`, without --launch):
#     the command finds the run's pull request on the branch its launcher wrote
#     and refuses an open or a closed one, so a plan that answers is the reading
#     that the pull request is MERGED — the state this check depends on;
#   * the parent's record, read from the runs bucket: the deferrals it left and
#     the group the triage tied each one to, and the mode the run resolved. The
#     record is the source of the groups the plan is checked against;
#   * the plan against the record: every group of the record is named by exactly
#     one task of the plan — a group already covered by a run of an earlier call
#     is named as filed, and the two together cover the record's findings with
#     none left over and none counted twice;
#   * the launch (`fugaro followups RUN --launch --json`): one debt task per
#     group, each with a run ID of its own. A plan with nothing to file — every
#     group already has a run — launches nothing, which is what makes this check
#     safe to run twice;
#   * each filed run's own task.json, read from the runs bucket: the debt mark,
#     the parent run and the finding keys it settles, and the mode the task
#     carries — the parent's applied mode, never empty;
#   * `fugaro runs ls --debt --json`: each filed run is listed, carrying its
#     parent and its keys, and every row the filter prints is a task filed from
#     a deferral rather than an ordinary run;
#   * the same plan read again: nothing left to file. A second call files
#     nothing, which is what makes an interrupted first one safe to repeat.
#
# It creates nothing but the tasks it files: no job of this check's own, no
# build, no binding and no account. Each task it files IS a run — filed in the
# sandbox project and cancelled from the exit trap as soon as this check has
# read its task, so a Ctrl-C cancels them too. Nothing this check files is left
# going: your sandbox's runs list stays what it was, plus the cancelled rows.
#
# Usage:      bash scripts/live-checks/check491.sh --sandbox <a sandbox checkout> --run <the parent run> --yes
#             bash scripts/live-checks/check491.sh --sandbox <path> --run <run> --yes --max 3
#             bash scripts/live-checks/check491.sh --sandbox <path> --run <run> --dry-run
#             bash scripts/live-checks/check491.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents and the tasks
#             are read with it), gcloud (authenticated as YOU, and able to read
#             the runs bucket) and fugaro (the installed release: the whole point
#             is the build you have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    one sandbox run per group the parent deferred — each cancelled as
#             soon as this check has read its task, from the exit trap as well as
#             the ordinary way out (CHECK491_KEEP_RUNS leaves them going). The
#             runs and their branches are left behind as cancelled rows.
#             Nothing it files outlives it. On disk: one scratch directory,
#             removed at the end.
# Costs:      one sandbox run started per group — each does the work of a start
#             and is cancelled, so the figure is a fraction of a run and the
#             pull request a whole one would open is never opened. Every other
#             call is a read (doctor, the plan, the record, the tasks, the runs
#             list), and `--yes` is required before anything is filed.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check491_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and every run this check filed, cancelled
#             from that same trap. The parent run and its pull request are left
#             exactly as they were: this check writes nothing about either.
# Output:     progress on stderr, ONE block on stdout headed CHECK 491 RESULT,
#             then CHECK 491 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a build older than the release the work ships in. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or a filed run could
#             not be cancelled. A key the run never reached reads
#             `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry run
# IDs, finding keys, one-line summaries the reviewer wrote, mode names and the
# command's own refusals.
#
# Knobs (all CHECK491_*): DRY_RUN, YES, KEEP_SCRATCH, KEEP_RUNS, SANDBOX (the
# checkout, when you would rather set it in the environment), RUN (the parent
# run), MAX (how many groups one call files, 10 — the command's own default),
# RELEASE (0.24.0: the release this work ships in), REGION (us-east5),
# POLL_SECONDS (15), LIST_TIMEOUT_SECONDS (300), CLI_TIMEOUT_SECONDS (180),
# LAUNCH_TIMEOUT_SECONDS (600), CANCEL_TIMEOUT_SECONDS (300),
# DOCTOR_TIMEOUT_SECONDS (300).
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
  command -v bash >/dev/null 2>&1 || die "Check 491 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 491 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk491-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REPO=""
SLUG=""
MODE=""
PARENT_PR=""
# PARENT is the run whose deferrals are filed, as the command resolves it: the
# check never rewrites the ID it was given, and the record's own run_id is read
# against it.
PARENT=""
# PLAN_OK is set by the one branch of step_parent that read the plan, and
# step_parent_record reads it and nothing else: the plan carries the repository
# and the slug the parent's record is stored under, and without it the check
# would be reading a record it has guessed the key of.
PLAN_OK=no
# RECORD_OK is set by the one branch of step_parent_record that read THIS
# parent's record, and every row below it reads it and nothing else.
RECORD_OK=no
# GROUPS_OK is set by the one branch of step_groups that found every group of
# the record named by exactly one task of the plan. The launch below files what
# the plan names, so a plan that does not cover the record is not a plan this
# check may go on to file.
GROUPS_OK=no
# LAUNCHED_NOW is the runs THIS invocation filed, one per line. They are the
# runs the exit trap cancels, and nothing else is ever cancelled: a run an
# earlier invocation filed is a row this check reads, not a run it owns.
LAUNCHED_NOW=""
# LAUNCH_UNNAMED is set when a launch that went wrong may have filed runs this
# check was never told the IDs of — a refusal after the first task, a bound that
# fired, or a task the command reports without naming its run. Those runs are
# going and this check cannot cancel what it cannot name, so the rows say so
# rather than a block that reads as if nothing were left behind.
LAUNCH_UNNAMED=no
# FILED_FILE holds `run<TAB>keys` for every task the parent now has a run for —
# the ones this invocation filed, or the ones an earlier call already filed when
# the plan had nothing left to do. The rows below read it.
FILED_FILE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk491.XXXXXXXX") || die "Check 491 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  FILED_FILE="$SCRATCH/filed.tsv"
  : >"$FILED_FILE"
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
    say "CHECK491_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a field the release did not write the day the document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, so a
# shell can hold them without a second parser.
#
# The grouping is here rather than in the shell for the same reason: "one task
# per group" is a comparison of two documents — the plan's keys and the
# record's groups — and a shell that joined them line by line would be a second
# implementation of the command it is checking.
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
    print("project=%s" % (project.get("gcp_project") or ""))
    print("ok=%s" % yes(doc.get("ok")))
elif what == "plan":
    print("version=%s" % (doc.get("version") or 0))
    print("repo=%s" % (doc.get("repo") or ""))
    print("slug=%s" % (doc.get("slug") or ""))
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("branch=%s" % flat(doc.get("branch")))
    print("pr=%s" % (doc.get("pr") or 0))
    print("pr_url=%s" % (doc.get("pr_url") or ""))
    print("deferred=%s" % (doc.get("deferred") or 0))
    print("deferred_total=%s" % (doc.get("deferred_total") or 0))
    print("max=%s" % (doc.get("max") or 0))
    print("launched=%s" % yes(doc.get("launched")))
    print("over_cap=%s" % (doc.get("over_cap") or 0))
    tasks = doc.get("tasks") or []
    skipped = doc.get("skipped") or []
    print("task_count=%d" % len(tasks))
    print("skip_count=%d" % len(skipped))
    for i, t in enumerate(tasks, 1):
        print("planned\t%d\t%s\t%s\t%s\t%s" % (
            i,
            ",".join(t.get("keys") or []),
            flat(t.get("rigor")),
            t.get("run_id") or "",
            flat(t.get("status")),
        ))
    for s in skipped:
        print("filed\t%s\t%s" % (s.get("run") or "", ",".join(s.get("keys") or [])))
elif what == "record":
    rigor = doc.get("rigor") or {}
    deferred = doc.get("deferred") or {}
    findings = deferred.get("findings") or []
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("mode=%s" % (rigor.get("mode") or ""))
    print("mode_from=%s" % (rigor.get("from") or ""))
    print("pr=%s" % (doc.get("pr") or 0))
    print("deferred_count=%d" % len(findings))
    print("deferred_total=%s" % (deferred.get("total") or 0))
    for f in findings:
        key = f.get("key") or ""
        print("deferred\t%s\t%s" % (key, f.get("group") or key))
elif what == "task":
    fo = doc.get("follow_up_of") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("ref=%s" % (doc.get("ref") or ""))
    print("debt=%s" % yes(doc.get("debt")))
    print("rigor=%s" % flat(doc.get("rigor")))
    print("follow_up_run=%s" % (fo.get("run") or ""))
    print("follow_up_keys=%s" % ",".join(fo.get("keys") or []))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        fo = row.get("follow_up_of") or {}
        rigor = row.get("rigor") or {}
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
            row.get("run") or "",
            row.get("pr_url") or "",
            row.get("outcome") or "",
            fo.get("run") or "",
            ",".join(fo.get("keys") or []),
            rigor.get("mode") or "",
        ))
elif what == "groups":
    plan = load(sys.argv[3])
    order, keys = [], {}
    for f in ((doc.get("deferred") or {}).get("findings") or []):
        key = f.get("key") or ""
        group = f.get("group") or key
        if group not in keys:
            keys[group] = []
            order.append(group)
        keys[group].append(key)
    named = []
    for t in plan.get("tasks") or []:
        named.append(("planned", sorted(t.get("keys") or []), t.get("run_id") or ""))
    for s in plan.get("skipped") or []:
        named.append(("filed", sorted(s.get("keys") or []), s.get("run") or ""))
    problems, covered = [], {}
    for kind, named_keys, run in named:
        group = None
        for g in order:
            if sorted(keys[g]) == named_keys:
                group = g
                break
        if group is None:
            problems.append("a %s task names the keys %s, which are no single group of the record" % (kind, ",".join(named_keys) or "none"))
            continue
        if group in covered:
            problems.append("the group %s is named twice, as a %s and as a %s" % (group, covered[group], kind))
            continue
        covered[group] = kind
    left = [g for g in order if g not in covered]
    over_cap = plan.get("over_cap") or 0
    if over_cap and len(left) != over_cap:
        problems.append("the plan says the cap left %d group(s) behind and %d of them are unnamed" % (over_cap, len(left)))
    elif over_cap:
        problems.append("the cap left %d group(s) unnamed: this check files every group the parent deferred, so raise --max to the number of groups (%d)" % (over_cap, len(order)))
    elif left:
        problems.append("%d group(s) of the record are named by neither a task nor a filed run: %s" % (len(left), ",".join(left)))
    carried = len(((doc.get("deferred") or {}).get("findings") or []))
    declared = (doc.get("deferred") or {}).get("total") or 0
    if declared > carried:
        problems.append("the record carries %d of the %d findings it deferred (the writer's own cap), so %d of them are named by no group here and no call can file them" % (carried, declared, declared - carried))
    print("groups=%d" % len(order))
    print("planned=%d" % len(plan.get("tasks") or []))
    print("filed=%d" % len(plan.get("skipped") or []))
    print("left=%d" % len(left))
    print("over_cap=%d" % over_cap)
    print("ok=%s" % yes(not problems))
    print("problem=%s" % "; ".join(problems))
    for i, g in enumerate(order, 1):
        print("group\t%d\t%s\t%s\t%s" % (
            i,
            ",".join(keys[g]),
            covered.get(g, "left"),
            next((run for kind, ks, run in named if sorted(keys[g]) == ks and kind == covered.get(g)), ""),
        ))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE [FILE...] DEST: answer one query into a file, and fail
# the step when the document is not the document it should be.
read_json() { # read_json QUERY FILE [FILE...] DEST
  local query=$1 dest=${!#}
  shift
  set -- "${@:1:$#-1}"
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  # `|| true`: a reader that found nothing is an empty answer rather than a
  # failure, and under `set -o pipefail` the `head` that closes the pipe early is
  # enough to make this pipeline non-zero.
  { sed -n "s/^$2=//p" "$1" 2>/dev/null || true; } | head -n 1 || true
}

# tab_field FILE KIND COLUMN [MATCH]: one column of one `tab`-separated row, ""
# when the kind of row or the match is not there. The readers print values that
# hold a `|` or an `=` unmangled, so a row is cut by tab and never by a
# separator a value could carry.
tab_field() { # tab_field FILE KIND COLUMN [MATCH]
  local file=$1 kind=$2 col=$3 want=${4:-}
  awk -F'\t' -v kind="$kind" -v col="$col" -v want="$want" '
    $1 == kind && (want == "" || $2 == want) { print $col; exit }
  ' "$file" 2>/dev/null || true
}

# run_row FILE RUN COLUMN: one column of one run's row in a listing, "" when the
# listing does not hold it.
run_row() { # run_row FILE RUN COLUMN
  local file=$1 id=$2 col=$3
  awk -F'\t' -v id="$id" -v col="$col" '$1 == "row" && $2 == id { print $col; exit }' "$file" 2>/dev/null || true
}

# filed_named: how many of the filed tasks carry a run ID. A task the command
# reports without one names nothing a reader could go to, and every row below is
# a reading of a run — so a step that found none says so rather than passing
# over an empty list.
filed_named() {
  awk -F'\t' '$1 != "" { n++ } END { print n + 0 }' "$FILED_FILE" 2>/dev/null || echo 0
}

# keyed CSV: the same keys in one spelling whatever order they were written in,
# so two readings of one group are compared as the set they are.
sortcsv() { # sortcsv "a,b"
  { printf '%s' "${1:-}" | tr ',' '\n' | sort | paste -sd, - ; } 2>/dev/null || true
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a plan refused
# because the pull request is not merged, a task object that is not there), and a
# trap inherited here would turn each of them into "an internal error" and a
# status of 2 before the step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  # Truncated, not appended: the tags a poll loop reuses (`rows` in Check 490,
  # `debt` here) are read again after every call, and a second document appended
  # to the first is one the reader cannot read — the row would report a listing
  # it never failed to get.
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

# cancel_filed WHY: stop every run this invocation filed, once, and say whether
# it worked. It is what makes "nothing this check files outlives it" true rather
# than intended: the exit trap calls it, so a Ctrl-C cancels the runs too. A run
# an earlier invocation filed is NOT cancelled — this check does not own it.
CANCEL_DONE=no
CANCEL_OK=""
cancel_filed() { # cancel_filed WHY
  [ "$CANCEL_DONE" = yes ] && return 0
  CANCEL_DONE=yes
  if [ -z "$LAUNCHED_NOW" ]; then
    if [ "$LAUNCH_UNNAMED" = yes ]; then
      CANCEL_OK=no
      fact cancelled FAIL "the launch did not name the runs it filed, so this check has nothing to cancel: any run it filed is going and spending. Read the runs list with 'fugaro runs ls --repo ${REPO} --debt' and stop what is there — the launched row above says what happened"
      return 0
    fi
    fact cancelled INFO "not needed: this call filed no run of its own, so it cancelled nothing — either the rows above stopped before anything was filed, or every group was already covered by a run of an earlier call, and the runs it did not file are not its own to cancel"
    return 0
  fi
  if [ -n "$KEEP_RUNS" ]; then
    fact cancelled PASS "CHECK491_KEEP_RUNS is set, so the $(printf '%s\n' "$LAUNCHED_NOW" | grep -c .) run(s) this check filed are LEFT GOING ($1): they are real sandbox runs and they will spend until they end. Read them with 'fugaro runs ls --repo ${REPO}' and stop them with 'fugaro cancel <run> --now'"
    return 0
  fi
  local failed=0 done_runs="" id
  for id in $(printf '%s\n' "$LAUNCHED_NOW"); do
    say "cancelling ${id} ($1): fugaro cancel ${id} --now"
    run_bounded "$CANCEL_TIMEOUT" "cancel-${id}" "$SANDBOX" "$FUGARO_BIN" cancel "$id" --now --json
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      failed=$((failed + 1))
      say "cancel ${id} did not answer (exit ${BOUNDED_STATUS}); see $SCRATCH/cancel-${id}.err"
      continue
    fi
    done_runs="${done_runs}${done_runs:+, }${id}"
  done
  if [ "$failed" != 0 ]; then
    CANCEL_OK=no
    fact cancelled FAIL "${failed} of the run(s) this check filed did not accept 'fugaro cancel <run> --now': they may still be going, and they are real sandbox runs that spend until they end. Stop them yourself with 'fugaro cancel <run> --now' and read each with 'fugaro runs show <run>'. Cancelled: ${done_runs:-none}"
    return 0
  fi
  CANCEL_OK=yes
  fact cancelled PASS "'fugaro cancel <run> --now' answered for every run this check filed (${done_runs}): the tasks it filed were cancelled as soon as their own rows had been read, and nothing this check started outlives it"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check491.sh --sandbox <a sandbox checkout> --run <the parent run> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused. Refusing here rather than letting
# each command refuse one at a time is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 491 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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
# ships in 0.24.0, and a build that predates it has no `followups` command and
# no `--debt` filter, so a call made here would be a reading of a build nobody
# shipped this for. Both probes are offline: `fugaro version`, the usage line
# `fugaro followups --help` prints, and the `--debt` line of `fugaro runs ls
# --help`.
step_release() {
  local v feature=yes
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if ! "$FUGARO_BIN" followups --help >/dev/null 2>&1; then feature=no; fi
  if ! "$FUGARO_BIN" runs ls --help 2>/dev/null | grep -q -- '--debt'; then feature=no; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no follow-ups: either 'fugaro followups --help' does not answer or 'fugaro runs ls --help' does not name --debt, so neither the command this check runs nor the filter it reads the filed runs through is in this build. Check 491 needs release ${RELEASE} (the post-review triage work) or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro does not carry the triage work: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'followups' and 'runs ls --debt' are there: this build carries the work the calls below measure"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the post-review triage work ships in: a call made here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and this build answers 'followups --help' and names 'runs ls --debt', so it carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  fact bound PASS "ONE sandbox run per group the parent ${PARENT} deferred, at most CHECK491_MAX=${MAX} of them: 'fugaro followups --launch' files a debt task for each group, and each task IS a run — it starts, and this check cancels it as soon as it has read the task, so the figure is the start of a run and not a whole one, on the sandbox's own budget. Every other call is a read (doctor, the plan, the record, the tasks, the runs list), and --yes is required before anything is filed"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the runs and their tasks are that project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of the repository the parent run belongs to)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and both the plan and the launches are made from one"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project's runs bucket. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get("gcp_project") or "")' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the plan is read from this checkout and the tasks it files are read in this project's bucket"
}

# step_parent: the plan, and with it the state the whole check depends on. The
# command finds the parent's pull request on the branch the run's launcher wrote
# and refuses an open or a closed one, so a plan that answers is the reading
# that the pull request is merged. Nothing is written by this call.
step_parent() {
  if [ -z "$PROJECT" ]; then
    unmeasured parent-plan "there is no project to read the plan in: the row above says why"
    return 0
  fi
  say "reading the plan: fugaro followups ${PARENT} --json, in ${SANDBOX} (no --launch: this call writes nothing)"
  run_bounded "$CLI_TIMEOUT" plan1 "$SANDBOX" "$FUGARO_BIN" followups "$PARENT" --json --max "$MAX"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured parent-plan "the plan did not answer inside ${CLI_TIMEOUT}s and was ended: what it printed is in $SCRATCH/plan1.out and $SCRATCH/plan1.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    local refused
    refused=$(head -c 400 "$SCRATCH/plan1.err" 2>/dev/null | tr '\n' ' ' || true)
    fact parent-plan FAIL "fugaro followups ${PARENT} was refused (exit ${BOUNDED_STATUS}): ${refused:-nothing was printed}. The command files a deferral only from a MERGED pull request — an open one is refused because the work is still going and a task filed now would race the run holding it, and a closed one because nothing was left behind. Merge the parent's pull request (Check 490 prints its URL), then run this check again. What it printed is in $SCRATCH/plan1.out and $SCRATCH/plan1.err"
    return 0
  fi
  if ! read_json plan "$SCRATCH/plan1.out" "$SCRATCH/plan1.txt"; then
    fact parent-plan FAIL "fugaro followups --json printed a document this check could not read: the plan is the whole of what this check goes on to file, and a plan it cannot read is not one it may act on. What it printed is in $SCRATCH/plan1.out"
    return 0
  fi
  local planned_run
  planned_run=$(field "$SCRATCH/plan1.txt" run_id)
  REPO=$(field "$SCRATCH/plan1.txt" repo)
  SLUG=$(field "$SCRATCH/plan1.txt" slug)
  PARENT_PR=$(field "$SCRATCH/plan1.txt" pr)
  if [ -z "$REPO" ] || [ -z "$SLUG" ] || [ -z "$planned_run" ]; then
    fact parent-plan FAIL "the plan names no repository (repo=${REPO:-none}), no slug (${SLUG:-none}) or no run (${planned_run:-none}): the slug is the key the parent's own record is stored under and the repository is what the runs list is read for, so a plan without them is not one this check may file from. What it printed is in $SCRATCH/plan1.out"
    return 0
  fi
  PLAN_OK=yes
  local tasks files skipped cap deferred
  tasks=$(field "$SCRATCH/plan1.txt" task_count)
  files=$(field "$SCRATCH/plan1.txt" skip_count)
  deferred=$(field "$SCRATCH/plan1.txt" deferred)
  cap=$(field "$SCRATCH/plan1.txt" max)
  fact parent-plan PASS "fugaro followups ${PARENT} planned from a MERGED pull request: #${PARENT_PR} of ${REPO} (branch $(field "$SCRATCH/plan1.txt" branch)), found on the branch the run's launcher wrote — the command refuses an open or a closed one, so a plan at all is the reading that the deferral's pull request is merged. The record deferred ${deferred} finding(s), and the plan holds ${tasks} task(s) to file and ${files} group(s) already filed (cap ${cap:-unset})"
}

# step_parent_record: the record, read from the runs bucket as the owner would
# read it. It is the source of the groups the plan is checked against, and of
# the mode each filed task has to carry — the plan's own words are read against
# the record, never the other way round.
step_parent_record() {
  if [ "$PLAN_OK" != yes ]; then
    unmeasured parent-record "there is no parent to read a record of: the plan row above did not name the repository and the slug the record is stored under, and a key this check guessed is a record that may be another run's"
    return 0
  fi
  local key="runs/${SLUG}/${PARENT}/result.json"
  say "reading the parent's record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact parent-record FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the record is where the deferrals and the groups the plan is checked against live, and no reading of the plan stands without it. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact parent-record FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$PARENT" ]; then
    fact parent-record FAIL "${key} records run_id=${recorded:-none} and this check was given ${PARENT}: the record at that key is another run's, and the groups read from it would be groups this parent never deferred"
    return 0
  fi
  # A hotfix parent is the one mode a child never carries: the command leaves its
  # task's rigor empty so the child resolves its own, so the mode read here is
  # the one the child's own task has to hold — and empty is a finding, not a
  # reading, because it is exactly what the parent's own mode shape gives.
  MODE=$(field "$SCRATCH/record.txt" mode)
  if [ "$MODE" = hotfix ]; then
    MODE=""
  fi
  RECORD_OK=yes
  local count total
  count=$(field "$SCRATCH/record.txt" deferred_count)
  total=$(field "$SCRATCH/record.txt" deferred_total)
  fact parent-record PASS "${key} records ${PARENT} (mode=$(field "$SCRATCH/record.txt" mode), from $(field "$SCRATCH/record.txt" mode_from)), and it deferred ${count} of ${total} finding(s): the groups below are read from this record and from nothing else"
}

# step_groups: one debt task per group, read as a comparison of the plan and the
# record. Every group the record's deferrals carry has to be named by exactly
# one task — planned or already filed — and no task may name keys that are not a
# group of the record.
step_groups() {
  if [ "$RECORD_OK" != yes ] || [ "$PLAN_OK" != yes ]; then
    unmeasured groups "this reading is not graded: the plan row or the record row above did not establish the two documents it compares, and a comparison of one document with nothing is not a reading of the grouping"
    return 0
  fi
  if ! read_json groups "$SCRATCH/record.out" "$SCRATCH/plan1.out" "$SCRATCH/groups.txt"; then
    fact groups FAIL "the plan and the record could not be read together: what the reader printed is in $SCRATCH/groups.txt.err"
    return 0
  fi
  local ok problem n planned filed left over
  ok=$(field "$SCRATCH/groups.txt" ok)
  problem=$(field "$SCRATCH/groups.txt" problem)
  n=$(field "$SCRATCH/groups.txt" groups)
  planned=$(field "$SCRATCH/groups.txt" planned)
  filed=$(field "$SCRATCH/groups.txt" filed)
  left=$(field "$SCRATCH/groups.txt" left)
  over=$(field "$SCRATCH/groups.txt" over_cap)
  if [ "${n:-0}" = 0 ]; then
    fact groups FAIL "the parent's record deferred no finding at all (its deferred block is absent or empty): this check files what a run left behind, and there is nothing here to file. Run Check 490 first — it is the check that produces a run with a deferral — and pass this one that run's ID"
    return 0
  fi
  if [ "$ok" != true ]; then
    fact groups FAIL "the plan is not one task per group of the record: ${problem:-the reader gave no reason}. The record is in $SCRATCH/record.out, the plan in $SCRATCH/plan1.out, and the comparison in $SCRATCH/groups.txt. A group has to be named by exactly one task, and a task may only name the keys of one group: a caller that files from a plan like this one would file a task for work another task already carries, or none for a group nobody covers"
    return 0
  fi
  GROUPS_OK=yes
  local detail=""
  while IFS=$'\t' read -r kind idx keys state run; do
    [ "$kind" = group ] || continue
    detail="${detail}${detail:+; }group ${idx} (${keys}) ${state}${run:+ as ${run}}"
  done <"$SCRATCH/groups.txt"
  fact groups PASS "the record's ${n} group(s) are named by exactly one task each: ${planned} to file and ${filed} already filed${over:+ (the cap left ${over} behind — the row above is a FAIL when it does)}. ${detail}. The plan and the record are in $SCRATCH/plan1.out and $SCRATCH/record.out"
}

# step_launch: the filing itself. A plan with tasks is filed with --launch, and
# a plan with none — every group already has a run from an earlier call — files
# nothing, which is what makes this check safe to run twice. The runs this
# invocation files are the ones its exit trap cancels.
step_launch() {
  if [ "$GROUPS_OK" != yes ]; then
    unmeasured launched "nothing was filed: the plan row above did not establish a plan that covers the record's groups, and a launch from it would file tasks for work a task of the plan does not carry. Settle that row and run this check again"
    return 0
  fi
  local planned filed
  planned=$(field "$SCRATCH/groups.txt" planned)
  filed=$(field "$SCRATCH/groups.txt" filed)
  if [ "${planned:-0}" = 0 ]; then
    awk -F'\t' '$1 == "filed" && $2 != "" { print $2 "\t" $3 }' "$SCRATCH/plan1.txt" >"$FILED_FILE"
    local unnamed
    unnamed=$(awk -F'\t' '$1 == "filed" && $2 == "" { n++ } END { print n + 0 }' "$SCRATCH/plan1.txt")
    if [ "$unnamed" != 0 ]; then
      fact launched FAIL "the plan names ${unnamed} group(s) as already covered by a run and names no such run: a group the command reports as filed is a run going in your project, and a plan this check cannot name it from is one whose runs it can neither read nor leave alone with a clear conscience. What it printed is in $SCRATCH/plan1.out"
      return 0
    fi
    local rows listed
    rows=$(grep -c . "$FILED_FILE" 2>/dev/null || true)
    listed=$(awk -F'\t' '{ printf "%s%s", (n++ ? ", " : ""), $1 }' "$FILED_FILE" 2>/dev/null || true)
    fact launched PASS "the plan has nothing to file: all ${filed} group(s) of the record are already covered by a run of an earlier call (${listed:-none}), and 'fugaro followups' files only what is left. This check filed nothing here and reads those runs instead, which is what makes it safe to run again after an interrupted call. It cancelled nothing: the runs it did not file are not its own. The plan is in $SCRATCH/plan1.out"
    return 0
  fi
  say "filing ${planned} task(s): fugaro followups ${PARENT} --launch --max ${MAX} --json, in ${SANDBOX}"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" followups "$PARENT" --launch --max "$MAX" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    LAUNCH_UNNAMED=yes
    unmeasured launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err. A launch that got as far as filing a run has filed it, and this check was never told its ID — read the runs list with 'fugaro runs ls --repo ${REPO} --debt' and stop what is there before running this check again; the groups that went are skipped by the next call"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    LAUNCH_UNNAMED=yes
    local refused
    refused=$(head -c 400 "$SCRATCH/launch.err" 2>/dev/null | tr '\n' ' ' || true)
    fact launched FAIL "fugaro followups ${PARENT} --launch was refused (exit ${BOUNDED_STATUS}): ${refused:-nothing was printed}. A refusal after the first task leaves the ones before it filed and running, and the command says how many: read the runs list with 'fugaro runs ls --repo ${REPO} --debt' and, once the ones that went have been dealt with, run this check again — the groups they cover are skipped. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json plan "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    LAUNCH_UNNAMED=yes
    fact launched FAIL "fugaro followups --launch --json printed a document this check could not read: the launch is what the rows below read the filed runs from, and a launch whose own --json view cannot be read is one whose runs this check would leave going. Read the runs list with 'fugaro runs ls --repo ${REPO} --debt' and cancel what is there. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  local launched count statuses=""
  launched=$(field "$SCRATCH/launch.txt" launched)
  count=$(field "$SCRATCH/launch.txt" task_count)
  # Only the tasks that name their run go into the file: an empty run ID would
  # otherwise make a line whose FIRST field is the keys — a run this check would
  # try to cancel under a name that is not a run. The unnamed ones are counted
  # here and are a FAIL below.
  awk -F'\t' '$1 == "planned" && $5 != "" { print $5 "\t" $3 }' "$SCRATCH/launch.txt" >"$FILED_FILE"
  local missing
  missing=$(awk -F'\t' '$1 == "planned" && $5 == "" { n++ } END { print n + 0 }' "$SCRATCH/launch.txt")
  while IFS=$'\t' read -r run keys; do
    [ -n "$run" ] || continue
    LAUNCHED_NOW="${LAUNCHED_NOW}${LAUNCHED_NOW:+
}${run}"
    statuses="${statuses}${statuses:+, }${run} (${keys})"
  done <"$FILED_FILE"
  if [ "$launched" != true ] || [ "${count:-0}" = 0 ] || [ "$missing" != 0 ]; then
    LAUNCH_UNNAMED=yes
    fact launched FAIL "the launch reports launched=${launched} over ${count:-0} task(s), ${missing} of them without a run ID: a filed task IS a run, and a call that reports one without naming it has started something this check can neither read nor cancel. Read the runs list with 'fugaro runs ls --repo ${REPO} --debt' and cancel what is there. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact launched PASS "fugaro followups ${PARENT} --launch filed ONE debt task per group — ${count} of them, from a MERGED pull request: ${statuses}. Each is a real run in ${PROJECT}, filed by this check and cancelled from its exit trap, so nothing it filed outlives it"
}

# step_debt_list: the listing the plan names beside the launch. `--debt` filters
# on the task's own mark, which is what makes it the filter this reading is
# about, and every row it prints has to be a task filed from a deferral.
step_debt_list() {
  if [ "$(filed_named)" = 0 ]; then
    unmeasured debt-list "there is no filed run whose ID this check was given: the launch row above did not produce one, so there is no row this listing is about"
    return 0
  fi
  local n
  n=$(grep -c . "$FILED_FILE" 2>/dev/null || true)
  say "reading the listing: fugaro runs ls --repo ${REPO} --debt --json (${n} run(s) filed by the parent ${PARENT})"
  # A listing read the moment a task was filed is polled for rather than
  # demanded: the row is a read of the runs bucket and it is there at once, but
  # a listing that has not caught up yet would report a run that a second read
  # finds, and the row below is about the filter and not about that race.
  local waited=0 listed=0 run keys
  while [ "$waited" -lt "$LIST_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" debt "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$REPO" --debt --json --limit 50
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      fact debt-list FAIL "fugaro runs ls --repo ${REPO} --debt --json did not answer (exit ${BOUNDED_STATUS}): the listing is how an owner finds the tasks a run left behind, and a filter that does not answer is not one they can. What it printed is in $SCRATCH/debt.err"
      return 0
    fi
    if ! read_json rows "$SCRATCH/debt.out" "$SCRATCH/debt.txt"; then
      fact debt-list FAIL "fugaro runs ls --debt --json printed a document this check could not read: what it printed is in $SCRATCH/debt.out"
      return 0
    fi
    listed=0
    while IFS=$'\t' read -r run keys; do
      [ -n "$run" ] || continue
      if [ "$(run_row "$SCRATCH/debt.txt" "$run" 2)" = "$run" ]; then listed=$((listed + 1)); fi
    done <"$FILED_FILE"
    [ "$listed" -ge "$n" ] && break
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  local not_debt
  not_debt=$(awk -F'\t' '$1 == "row" && $9 == "" { print $2; exit }' "$SCRATCH/debt.txt" 2>/dev/null || true)
  if [ -n "$not_debt" ]; then
    fact debt-list FAIL "the debt listing carries run ${not_debt}, whose own task names no parent deferral (follow_up_of is absent): --debt is the filter an owner reads the debt through, and a row it prints that no deferral filed is a row of work somebody asked for. The listing is in $SCRATCH/debt.txt"
    return 0
  fi
  local missing="" bad="" found=0 run keys
  while IFS=$'\t' read -r run keys; do
    [ -n "$run" ] || continue
    local listed parent_row keys_row
    listed=$(run_row "$SCRATCH/debt.txt" "$run" 2)
    if [ "$listed" = "$run" ]; then
      found=$((found + 1))
      parent_row=$(run_row "$SCRATCH/debt.txt" "$run" 9)
      keys_row=$(run_row "$SCRATCH/debt.txt" "$run" 10)
      if [ "$parent_row" != "$PARENT" ]; then
        bad="${bad}${bad:+, }${run} names parent ${parent_row:-none}"
      elif [ "$(sortcsv "$keys_row")" != "$(sortcsv "$keys")" ]; then
        bad="${bad}${bad:+, }${run} carries keys ${keys_row:-none}"
      fi
    else
      missing="${missing}${missing:+, }${run}"
    fi
  done <"$FILED_FILE"
  if [ -n "$missing" ] || [ -n "$bad" ]; then
    fact debt-list FAIL "fugaro runs ls --debt does not list what the parent filed: ${missing:+not listed: ${missing}. }${bad:+listed with the wrong parent or the wrong keys: ${bad}. }The listing is a read of the runs bucket, so a run filed a moment ago is in it; a filtered listing that leaves one out, or lists it against another parent, sends an owner to the wrong work. The listing is in $SCRATCH/debt.txt"
    return 0
  fi
  fact debt-list PASS "fugaro runs ls --repo ${REPO} --debt --json lists all ${found} run(s) the parent ${PARENT} filed, each carrying the parent as its follow_up_of.run and the finding keys of its own group, and every row the filter printed is a task filed from a deferral: the debt is what an owner reads the left-behind work through"
}

# step_debt_task: the tasks themselves, read from the runs bucket. The listing
# is a view; each filed run's own task.json is what the launcher wrote, and it
# is where the debt mark, the parent and the keys are facts rather than a
# rendering of them.
step_debt_task() {
  if [ "$(filed_named)" = 0 ]; then
    unmeasured debt-task "there is no filed run named to this check, so there is no task of its own to read: the launch row above says why"
    return 0
  fi
  local bad="" pin="" run keys n=0
  while IFS=$'\t' read -r run keys; do
    [ -n "$run" ] || continue
    n=$((n + 1))
    local key="runs/${SLUG}/${run}/task.json"
    run_bounded "$CLI_TIMEOUT" "task-${run}" "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      bad="${bad}${bad:+, }${run} (its task.json could not be read: ${key})"
      continue
    fi
    if ! read_json task "$SCRATCH/task-${run}.out" "$SCRATCH/task-${run}.txt"; then
      bad="${bad}${bad:+, }${run} (its task.json is not a document this check could read)"
      continue
    fi
    local rec debt parent_r fo_keys
    rec=$(field "$SCRATCH/task-${run}.txt" run_id)
    debt=$(field "$SCRATCH/task-${run}.txt" debt)
    parent_r=$(field "$SCRATCH/task-${run}.txt" follow_up_run)
    fo_keys=$(field "$SCRATCH/task-${run}.txt" follow_up_keys)
    if [ "$rec" != "$run" ]; then
      bad="${bad}${bad:+, }${run} (the task under its key records ${rec:-none})"
    elif [ "$debt" != true ]; then
      bad="${bad}${bad:+, }${run} (debt=${debt:-none}: 'runs ls --debt' finds it, so the mark and the task disagree)"
    elif [ "$parent_r" != "$PARENT" ]; then
      bad="${bad}${bad:+, }${run} (follow_up_of.run=${parent_r:-none})"
    elif [ "$(sortcsv "$fo_keys")" != "$(sortcsv "$keys")" ]; then
      bad="${bad}${bad:+, }${run} (follow_up_of.keys=${fo_keys:-none})"
    else
      pin="${pin}${pin:+, }${run}→${keys}"
    fi
  done <"$FILED_FILE"
  if [ -n "$bad" ]; then
    fact debt-task FAIL "the tasks the parent filed are not the ones this check reads: ${bad}. Each task has to carry the debt mark, the parent run and the finding keys of its own group — a task whose keys are not its group's settles work another task carries, or none. The tasks read are in $SCRATCH/task-*.txt"
    return 0
  fi
  fact debt-task PASS "each of the ${n} filed run(s) has a task.json of its own in the runs bucket, marked debt, naming the parent ${PARENT} and the keys of its group: ${pin}. The task, not the listing, is where the launcher's own words are — read under runs/<slug>/<run>/task.json in ${PROJECT}"
}

# step_mode: the mode each filed task carries. The plan's claim is "the parent's
# own applied mode", and the record above is the only place it is written: the
# task's rigor field is what the launch filed, and a task filed without it
# leaves the child to resolve a mode of its own, which is the reading the plan
# does not make.
step_mode() {
  if [ "$(filed_named)" = 0 ]; then
    unmeasured mode "there is no filed run named to this check, so there is no task whose mode could be read: the launch row above says why"
    return 0
  fi
  if [ -z "$MODE" ]; then
    fact mode FAIL "the parent's record resolved no mode this task could carry (its rigor block is absent, or the mode is hotfix, which a filed task never carries): the plan's reading is that each task goes at the parent's applied mode, and a parent with none leaves every child to resolve its own. Run Check 490 against a checkout that resolves to startup — its mode row is the same reading — and pass this check that run's ID"
    return 0
  fi
  local bad="" seen="" run keys n=0
  while IFS=$'\t' read -r run keys; do
    [ -n "$run" ] || continue
    n=$((n + 1))
    local got
    got=$(field "$SCRATCH/task-${run}.txt" rigor)
    if [ "$got" != "$MODE" ]; then
      bad="${bad}${bad:+, }${run} carries '${got:-none}'"
    else
      seen="${seen}${seen:+, }${run}"
    fi
  done <"$FILED_FILE"
  if [ -n "$bad" ]; then
    fact mode FAIL "the parent resolved mode ${MODE} and the tasks it filed do not all carry it: ${bad}. Every task a deferral files goes at the parent's own applied mode, and a task filed at another mode (or none) is a run that re-resolves its rigor from the repository rather than continuing the one the finding came from. The tasks read are in $SCRATCH/task-*.txt"
    return 0
  fi
  fact mode PASS "all ${n} filed task(s) carry mode ${MODE} (rigor in their own task.json: ${seen}), which is the mode the parent's record resolved (from $(field "$SCRATCH/record.txt" mode_from)): each group is filed at the parent's applied mode and not at one of the child's own"
}

# step_idempotent: the same plan read once more. The command files only what is
# left, so a call after this one has nothing to do — which is the whole of what
# makes an interrupted call safe to run again.
step_idempotent() {
  if [ "$GROUPS_OK" != yes ]; then
    unmeasured idempotent "this reading is not graded: the plan row above did not establish a plan that covers the record's groups"
    return 0
  fi
  say "reading the plan again: fugaro followups ${PARENT} --json (nothing is written)"
  run_bounded "$CLI_TIMEOUT" plan3 "$SANDBOX" "$FUGARO_BIN" followups "$PARENT" --json --max "$MAX"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured idempotent "the second plan did not answer (exit ${BOUNDED_STATUS}): what it printed is in $SCRATCH/plan3.out and $SCRATCH/plan3.err"
    return 0
  fi
  if ! read_json plan "$SCRATCH/plan3.out" "$SCRATCH/plan3.txt"; then
    fact idempotent FAIL "the second fugaro followups --json printed a document this check could not read: what it printed is in $SCRATCH/plan3.out"
    return 0
  fi
  if ! read_json groups "$SCRATCH/record.out" "$SCRATCH/plan3.out" "$SCRATCH/groups3.txt"; then
    fact idempotent FAIL "the second plan and the record could not be read together: what the reader printed is in $SCRATCH/groups3.txt.err"
    return 0
  fi
  local planned filed left ok problem
  planned=$(field "$SCRATCH/groups3.txt" planned)
  filed=$(field "$SCRATCH/groups3.txt" filed)
  left=$(field "$SCRATCH/groups3.txt" left)
  ok=$(field "$SCRATCH/groups3.txt" ok)
  problem=$(field "$SCRATCH/groups3.txt" problem)
  if [ "$ok" != true ] || [ "${left:-0}" != 0 ]; then
    fact idempotent FAIL "after this call, the plan still leaves ${left:-?} group(s) unnamed: ${problem:-the reader gave no reason}. The command files the groups a run already covers as skipped, so a second call files only what is left — and an interrupted one can simply be run again. The plan is in $SCRATCH/plan3.out"
    return 0
  fi
  local second
  second=$(field "$SCRATCH/plan3.txt" task_count)
  if [ "${second:-0}" != 0 ]; then
    fact idempotent FAIL "a second call would file ${second} task(s) again, while every group already has a run: the groups this check filed a moment ago are not being seen as filed, so a call run twice files the same work twice and an interrupted one cannot be repeated. The plan is in $SCRATCH/plan3.out"
    return 0
  fi
  fact idempotent PASS "reading the plan again after this call, every one of the record's groups is covered by a filed run (${filed}) and nothing is left to file (${planned} task(s)): a second 'fugaro followups ${PARENT}' writes nothing, so this check can be run again — and an interrupted call repeated — without filing the same work twice"
}

# restore: what this check changed, said rather than assumed. The runs it filed
# are the one thing it started, and they are cancelled here — from the exit trap,
# so a Ctrl-C or a failed assertion cancels them as surely as the ordinary way
# out. The parent run and its pull request are left exactly as they were.
restore() {
  cancel_filed "the check is ending"
  local created="nothing was created but the tasks the parent's groups were filed as: no job of this check's own, no build, no binding and no account. The parent run ${PARENT} and its pull request were read and never written."
  if [ "$CANCEL_OK" = no ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="a run this check filed may still be going: either 'fugaro cancel <run> --now' did not answer for it or the launch never named it. Read the runs with 'fugaro runs ls --repo ${REPO} --debt' and stop them yourself — they are real sandbox runs and they spend until they end. ${created}"
    return 0
  fi
  RESTORE_VERDICT=PASS
  if [ -z "$LAUNCHED_NOW" ]; then
    RESTORE_NOTE="${created} This call filed no run of its own — every group was already covered by a run of an earlier call, and the runs it did not file are not its own to cancel"
    return 0
  fi
  if [ -n "$KEEP_RUNS" ]; then
    RESTORE_NOTE="${created} The run(s) this check filed — $(printf '%s' "$LAUNCHED_NOW" | tr '\n' ' ') — were LEFT GOING because CHECK491_KEEP_RUNS is set: they are real sandbox runs and they will spend until they end. Stop them with 'fugaro cancel <run> --now'"
    return 0
  fi
  RESTORE_NOTE="${created} The run(s) this check filed — $(printf '%s' "$LAUNCHED_NOW" | tr '\n' ' ') — were cancelled as soon as their own tasks had been read"
}

cleanup() {
  # restore runs BEFORE the facts are captured: it is what cancels the runs this
  # check filed, and the row that says so has to be in the block.
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to cancel runs and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done — leaving the runs it was about to cancel.
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
  say "CHECK 491 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version, 'fugaro followups --help' and the --debt line of 'fugaro runs ls --help' — the release ${RELEASE} gate: the build must carry the post-review triage work"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [network, read] fugaro followups ${PARENT} --json in ${SANDBOX} — the plan, and with it the reading that the parent's pull request is MERGED (an open or a closed one is refused)"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<slug>/${PARENT}/result.json — the deferrals, their groups and the mode the parent resolved"
  say "  [local] the plan against the record: every group named by exactly one task"
  say "  [network, BILLABLE] fugaro followups ${PARENT} --launch --max ${MAX} --json — ONE sandbox run per group, each cancelled from the exit trap as soon as its task has been read"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<slug>/<run>/task.json — the debt mark, the parent and the keys of each filed task, and its mode"
  say "  [network, read] fugaro runs ls --repo <owner/name> --debt --json — the listing the plan names beside the launch"
  say "  [read] fugaro followups ${PARENT} --json again — nothing left to file"
  say "  [nothing created] no job of this check's own, no build, no binding, no account; the scratch directory is removed at the end, and the parent run and its pull request are never written"
  for key in release sandbox parent-plan parent-record groups launched debt-list \
    debt-task mode idempotent cancelled; do
    fact "$key" INFO "planned: the calls above measure this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no followups call and no cancel"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  parent-plan parent-record groups launched debt-list debt-task mode idempotent
  cancelled restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 491 RESULT\n' >&3
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
  printf 'CHECK 491 RESULT END (exit %s)\n' "$status" >&3
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
RUN_CLI=""
MAX_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of the repository the parent run belongs to"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --run)
    [ $# -ge 2 ] || die "--run needs the parent run whose deferrals are filed: the run Check 490 leaves, whose pull request the deferral is on"
    RUN_CLI=$2
    shift 2
    ;;
  --max)
    [ $# -ge 2 ] || die "--max needs a number: how many groups one call files"
    MAX_CLI=$2
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

SANDBOX=${CHECK491_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 491 needs --sandbox: the checkout of the repository the parent run belongs to. It is where the plan and the launches are made from. Usage: bash scripts/live-checks/check491.sh --sandbox <a sandbox checkout> --run <the parent run> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PARENT=${CHECK491_RUN:-${RUN_CLI:-}}
if [ -z "$PARENT" ]; then
  die "Check 491 needs --run: the parent run whose deferrals are filed — the run Check 490 launched and left with a deferred finding, which is the run whose ID its own block's run-launched row names"
fi
MAX=${CHECK491_MAX:-${MAX_CLI:-10}}
RELEASE=${CHECK491_RELEASE:-0.24.0}
YES=${CHECK491_YES:-$YES_CLI}
DRY_RUN=${CHECK491_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK491_KEEP_SCRATCH:-}
KEEP_RUNS=${CHECK491_KEEP_RUNS:-}
REGION=${CHECK491_REGION:-us-east5}
POLL_SECONDS=${CHECK491_POLL_SECONDS:-15}
LIST_TIMEOUT=${CHECK491_LIST_TIMEOUT_SECONDS:-300}
CLI_TIMEOUT=${CHECK491_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK491_LAUNCH_TIMEOUT_SECONDS:-600}
CANCEL_TIMEOUT=${CHECK491_CANCEL_TIMEOUT_SECONDS:-300}
DOCTOR_TIMEOUT=${CHECK491_DOCTOR_TIMEOUT_SECONDS:-300}
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
  [ -n "$FUGARO_BIN" ] || die "Check 491 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
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
    die "Check 491 files one sandbox run per group the parent deferred, so it asks for --yes. Nothing has been filed. Type: bash scripts/live-checks/check491.sh --sandbox ${SANDBOX} --run ${PARENT} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_parent
  step_parent_record
  step_groups
  step_launch
  step_debt_list
  step_debt_task
  step_mode
  step_idempotent
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
