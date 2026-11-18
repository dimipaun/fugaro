#!/usr/bin/env bash
# Check 540 — a stalled, Routine sandbox pull request walks R0 to R3, merges, and
# leaves its follow-ups pending.
#
# Plan task E15 (docs/outer-loop-escalation/implementation-plan.md) writes this
# check; the ladder itself is E10a/E10b and the merge mint it lands through is
# E16. What it measures is the one thing no test of the daemon can: that on YOUR
# installation, with your own credential, a pull request that stopped converging
# is really walked — rebased at rung 0, given one stronger pass at rung 2, and
# landed at rung 3 under the deterministic gate — and that what the landing left
# open waits for you rather than running itself.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to (`fugaro doctor --json`)
#     and the pull request the walk is about, read from the host with `gh pr
#     view`: it has to be an OPEN **draft**, because readying a stalled draft is
#     the one thing `escalation.ready_drafts` switches on and the row below turns
#     it on for the length of this check only;
#   * the two ladder settings this check needs and nothing else: it reads
#     `escalation.merge_judge` (a merge at rung 3 needs a judge named — an unset
#     grade is a question for you rather than a landing) and it writes
#     `escalation.ready_drafts=true` and `escalation.r2_after_attempts=1` into the
#     **local** layer so that the walk completes while you watch. Both writes are
#     the owner's own hand (D846, D1026): no `--yes` is passed and nothing is
#     asked, an agent's session is refused one of them at the fence, and the
#     restore row puts each key back where it was;
#   * the walk itself, driven through the daemon's own cycle
#     (`fugaro orchestrator run --once`, one cycle per invocation, the lease
#     released between them) until the pull request is merged or the bound is
#     reached. R0 is a rebase run, R2 the one stronger pass, R3 the merge rung;
#   * the ledger the rung transitions land in (`fugaro escalation show ITEM
#     --json`), the audit lines the moves leave (`fugaro orchestrator events
#     --json`), the merge at the host (`gh pr view` again: MERGED, and when);
#   * **the time to merge**, from the first rung move to the merge, both read
#     rather than assumed: the ledger's own stamps and the host's `mergedAt`;
#   * the follow-ups the landing filed (`fugaro backlog list --json`): the items
#     named `<item>-fu-…`, which have to be queued and **unapproved** — a landing
#     files the work it did not block, and D1226/D1245 leave every one of them
#     for you to approve.
#
# It does not launch a run of its own and it does not merge anything by hand:
# every step of the walk is the daemon's, driven one cycle at a time from your
# terminal.
#
# Usage:      bash scripts/live-checks/check540.sh --sandbox <checkout> --item <work item> --pr <number>
#             bash scripts/live-checks/check540.sh --sandbox <checkout> --item <item> --pr <n> --dry-run
#             bash scripts/live-checks/check540.sh --help
# Needs:      bash, mktemp, date, python3 (every --json document is read with
#             it), gcloud (authenticated as YOU), gh (as YOU, with read access to
#             the sandbox repository) and fugaro (the installed release).
# Where:      your own terminal and the **sandbox project and repository** only.
#             A coding agent's session is refused by name, as are a project or an
#             account naming belong.
# Creates:    two local settings for the length of the run, put back by the
#             restore; whatever the daemon's own runs cost (the rebase, the
#             stronger pass, the review at the rebased head, the judge — all
#             covered by the item's approval, D2042); one scratch directory,
#             removed from the exit trap.
# Costs:      the ladder's own runs on your installation. This check starts none
#             itself: it drives the daemon's cycles, and the daemon launches the
#             runs the ladder calls for.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check540_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C; the two settings, put back by the restore.
#             The pull request is **merged** by the walk and is not undone — that
#             is the thing being measured — and its branch and the follow-ups are
#             yours to deal with afterwards.
# Output:     progress on stderr, ONE block on stdout headed CHECK 540 RESULT,
#             then CHECK 540 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a read did not answer, or the walk never finished inside its
#             bound. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential, no token, no diff and no model
# output reaches the block, the scratch directory or a command line. The rows
# carry rungs, reason codes, ids, refs, counts, timestamps and exit statuses; the
# judge's own words are in the node's question for you, which this check reads by
# id and never quotes.
#
# Knobs (all CHECK540_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, ITEM, PR, SKIP_SETTINGS
# (1: leave the ladder's settings alone — what you want when you have already
# turned them on for a walk you are watching), EXPECT_FOLLOWUPS (1: the landing
# is expected to have left something open; 0 records that a clean landing is what
# you expected), REGION (us-east5), CYCLES (90), POLL_SECONDS (60),
# WALK_TIMEOUT_SECONDS (5400), CYCLE_TIMEOUT_SECONDS (600) and
# CLI_TIMEOUT_SECONDS (180).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives real
# runs on a real installation, and a trace of that is a second copy of a
# transcript that may carry a model's own words.
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
# finding about the code. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2).
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
  command -v bash >/dev/null 2>&1 || die "Check 540 needs bash"
  local t
  for t in mktemp date python3 gcloud gh fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 540 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk540-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REGION_USED=""
SLUG=""
ITEM=""
PR=""
PR_STATE=""
PR_DRAFT=""
HEAD_BEFORE=""
HEAD_AFTER=""
MERGED_AT=""
MERGED_EPOCH=""
FIRST_MOVE_AT=""
FIRST_MOVE_EPOCH=""
RUNGS_SEEN=""
WAIT_LINE=""
CYCLES_DRIVEN=0
WALK_STOPPED=""
# SETTINGS_LOG is what this check wrote for its own run, one `key<TAB>value<TAB>
# before` line per write, where `before` is what the local layer held before it
# or the word `unset` when the key was set nowhere below it (which the restore
# answers with `settings unset`). A file rather than arrays: the owner's machine
# may be a Mac, whose bash is 3.2, where an empty array under `set -u` is an
# unbound variable.
SETTINGS_LOG=""
SET_WROTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk540.XXXXXXXX") || die "Check 540 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  SETTINGS_LOG="$SCRATCH/settings.set"
  : >"$SETTINGS_LOG"
  mkdir -p "$SCRATCH/out"
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
    say "CHECK540_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with grep
# would report a rung the ledger does not carry the day the document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def epoch(text):
    if not text:
        return ""
    stamp = str(text).strip().replace("Z", "+00:00")
    try:
        return "%d" % datetime.datetime.fromisoformat(stamp).timestamp()
    except ValueError:
        return ""


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("gcp_project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))
elif what == "name":
    print("nameWithOwner=%s" % (doc.get("nameWithOwner") or ""))
elif what == "pr":
    print("number=%s" % (doc.get("number") or ""))
    print("state=%s" % (doc.get("state") or ""))
    print("is_draft=%s" % yes(doc.get("isDraft")))
    print("head_sha=%s" % (doc.get("headRefOid") or ""))
    print("merged_at=%s" % (doc.get("mergedAt") or ""))
    print("merged_at_epoch=%s" % epoch(doc.get("mergedAt")))
    print("url=%s" % (doc.get("url") or ""))
elif what == "show":
    item = doc.get("item") or {}
    print("item=%s" % (item.get("id") or ""))
    print("rung=%s" % (item.get("rung") or ""))
    print("reason=%s" % (item.get("reason") or ""))
    print("entered_at=%s" % (item.get("entered_at") or ""))
    print("outer_attempts=%s" % (item.get("outer_attempts") if item.get("outer_attempts") is not None else ""))
    print("age_seconds=%s" % (item.get("age_seconds") if item.get("age_seconds") is not None else ""))
    rungs = []
    for tr in item.get("transitions") or []:
        to = tr.get("to") or ""
        if to and to not in rungs:
            rungs.append(to)
        print("tr\t%s\t%s\t%s\t%s\t%s\t%s" % (
            tr.get("from") or "",
            to,
            tr.get("reason") or "",
            tr.get("at") or "",
            epoch(tr.get("at")),
            tr.get("head_sha") or "",
        ))
    print("rungs=%s" % ",".join(rungs))
    print("transitions=%d" % len(item.get("transitions") or []))
elif what == "ls":
    items = doc.get("items") or []
    print("count=%d" % len(items))
    for it in items:
        print("item\t%s\t%s\t%s" % (it.get("id") or "", it.get("repo") or "", it.get("rung") or ""))
elif what == "backlog":
    print("waiting=%s" % (doc.get("waiting") if doc.get("waiting") is not None else ""))
    for it in doc.get("items") or []:
        print("item\t%s\t%s\t%s\t%s" % (
            it.get("id") or "",
            it.get("phase") or "",
            it.get("repo") or "",
            yes(it.get("approved")),
        ))
elif what == "events":
    events = doc.get("events") or []
    print("count=%d" % len(events))
    for e in events:
        print("event\t%s\t%s\t%s\t%s" % (
            e.get("at") or "",
            e.get("subject") or "",
            e.get("transition") or "",
            (e.get("text") or "").replace("\t", " "),
        ))
elif what == "setting":
    rows = doc.get("settings") or []
    if not rows:
        print("present=false")
    else:
        row = rows[0]
        print("present=true")
        print("key=%s" % (row.get("key") or ""))
        print("value=%s" % (row.get("value") if row.get("value") is not None else ""))
        for lev in row.get("levels") or []:
            print("level\t%s\t%s" % (lev.get("layer") or "", lev.get("value") or ""))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file, and fail the step when
# the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_verdict KEY: the verdict this run has recorded for one row, "" when it
# recorded none. It is how a step asks whether the step before it left a reading
# worth walking from, without a second copy of the question.
row_verdict() { # row_verdict KEY
  awk -F'|' -v k="$1" '$1 == k { print $2; exit }' "$FACTS_FILE" 2>/dev/null || true
}

# bound_loop SECONDS PID: watch one background job to its bound and end it when
# the bound is reached. A hung cloud call would otherwise outlive the check that
# gave up on it, which is the one thing the bound exists to prevent — so the
# child of both helpers below execs, and the pid watched here IS the command.
bound_loop() { # bound_loop SECONDS PID
  local secs=$1 pid=$2 waited=0
  BOUNDED_TIMED_OUT=no
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
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

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. The ERR trap is dropped in the child: this
# check runs commands whose failing exit status is one of its own readings (a
# daemon cycle that refuses, a pull request that is not there), and a trap
# inherited here would turn each of them into "an internal error" and a status
# of 2 before the step could report it. Each key's files are STARTED FRESH: the
# ledger and the pull request are read once per cycle under the same key, and two
# JSON documents appended into one file is a document this check cannot read.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# human_seconds SECONDS: a duration a person reads, for a row that has to be
# about a figure rather than a number of seconds.
human_seconds() { # human_seconds SECONDS
  local s=$1
  if [ "$s" -lt 60 ]; then
    printf '%ss' "$s"
  elif [ "$s" -lt 3600 ]; then
    printf '%dm%02ds' "$((s / 60))" "$((s % 60))"
  else
    printf '%dh%02dm' "$((s / 3600))" "$(((s % 3600) / 60))"
  fi
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check540.sh --sandbox <checkout> --item <item> --pr <n> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the daemon's transcript to the terminal a second time"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the daemon's own transcript"
}

# guard_agent_session: the two writes below are the owner's own hand (D846,
# D1026) and the daemon this check drives acts on the owner's own installation.
# Refusing here rather than letting a write be refused one step further down is
# what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 540 turns the ladder's own switches on, drives the daemon's cycles and merges a pull request, at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for — the settings it writes are the owner's own hand and the daemon it drives spends your money"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}" in
  *belong*) stop "the account or project this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v=""
  run_bounded "$CLI_TIMEOUT" version "$SANDBOX" "$FUGARO_BIN" version
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ]; then
    v=$(head -n 1 "$SCRATCH/version.out" 2>/dev/null || true)
  fi
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the build this check drives: the ladder, the gate and the merge mint it reads are the ones you have installed)"
}

# ---------------------------------------------------------------- the steps
# step_installation: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the installation you actually work
# in rather than one it guessed, and the repository name comes from the host
# (`gh repo view`) rather than from a path.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the daemon this check drives is that repository's project's"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured installation "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation this checkout belongs to, and every row below is about it. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  REGION_USED=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$PROJECT" ]; then
    unmeasured installation "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to, and the daemon, the ladder and the merge are all that installation's. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  say "reading the repository: gh repo view --json nameWithOwner, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" slug "$SANDBOX" gh repo view --json nameWithOwner
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured installation "gh repo view --json nameWithOwner did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the pull request this check reads and the item it drives are the repository's, and the repository's name could not be read. What gh printed is in $SCRATCH/slug.err"
    return 0
  fi
  if ! read_json name "$SCRATCH/slug.out" "$SCRATCH/slug.txt"; then
    unmeasured installation "gh repo view printed a document this check could not read: what it printed is in $SCRATCH/slug.out"
    return 0
  fi
  SLUG=$(field "$SCRATCH/slug.txt" nameWithOwner)
  if [ -z "$SLUG" ]; then
    unmeasured installation "gh repo view answered without a repository name: this check cannot say which repository's pull request it is about. What it printed is in $SCRATCH/slug.out"
    return 0
  fi
  fact installation PASS "${SANDBOX} is a Fugaro repository's checkout of the installation ${PROJECT} (region ${REGION_USED:-unknown}), and its repository is ${SLUG}: the daemon this check drives is that installation's and the pull request is that repository's"
}

# step_pull_request: the pull request the walk is about, read from the host. It
# has to be OPEN and it has to be a DRAFT: readying a stalled draft is the one
# thing `escalation.ready_drafts` switches on (D2041), so a pull request that is
# already ready measures nothing about it, and a merged or closed one has no walk
# left to take.
step_pull_request() {
  if [ -z "$SLUG" ]; then
    unmeasured pull-request "there is no repository to read a pull request from: the row above says why"
    return 0
  fi
  say "reading the pull request: gh pr view ${PR} --repo ${SLUG} --json state,isDraft,headRefOid,mergedAt,url"
  run_bounded "$CLI_TIMEOUT" pr "$SANDBOX" gh pr view "$PR" --repo "$SLUG" \
    --json state,isDraft,headRefOid,mergedAt,url,number
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pull-request "gh pr view ${PR} did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the pull request the walk is about. Its output is in $SCRATCH/pr.err"
    return 0
  fi
  if ! read_json pr "$SCRATCH/pr.out" "$SCRATCH/pr.txt"; then
    unmeasured pull-request "gh pr view ${PR} printed a document this check could not read: what it printed is in $SCRATCH/pr.out"
    return 0
  fi
  PR_STATE=$(field "$SCRATCH/pr.txt" state)
  PR_DRAFT=$(field "$SCRATCH/pr.txt" is_draft)
  HEAD_BEFORE=$(field "$SCRATCH/pr.txt" head_sha)
  case "$PR_STATE" in
  OPEN) ;;
  MERGED)
    fact pull-request FAIL "the pull request ${SLUG}#${PR} is MERGED at $(field "$SCRATCH/pr.txt" merged_at): the walk this check measures has already happened, and a merged pull request cannot be walked again. Point --pr at a stalled draft of this repository, or run the item again and point this check at the draft it leaves"
    return 0
    ;;
  *)
    fact pull-request FAIL "the pull request ${SLUG}#${PR} is ${PR_STATE:-unreadable}, so there is nothing for the ladder to walk: it has to be OPEN and a draft. Point --pr at the stalled draft the item's own runs left"
    return 0
    ;;
  esac
  if [ "$PR_DRAFT" != true ]; then
    fact pull-request FAIL "the pull request ${SLUG}#${PR} is open and NOT a draft: readying a stalled draft is the one thing escalation.ready_drafts switches on (D2041), so a pull request that is already ready measures nothing about it. Convert it back to a draft (\`gh api graphql -f query='mutation{convertPullRequestToDraft(input:{pullRequestId:\"<id>\"}){clientMutationId}}'\`) and run this check again, or point --pr at a stalled draft of this repository"
    return 0
  fi
  if [ -z "$HEAD_BEFORE" ]; then
    unmeasured pull-request "gh pr view answered without a head: this check cannot say which head the walk starts from, and every reading below is at a head. What it printed is in $SCRATCH/pr.out"
    return 0
  fi
  fact pull-request PASS "the pull request ${SLUG}#${PR} is OPEN and a DRAFT at head ${HEAD_BEFORE:0:8} (read with gh pr view): a stalled draft is what the ladder's rung 3 readies under escalation.ready_drafts, and it is the pull request every row below is about"
}

# step_settings: the two switches this check needs, and the judge's grade it has
# to find already named. Each write is the owner's own hand (D846, D1026): no
# --yes is passed — a typed write is refused in a coding agent's session whatever
# --yes says, and at your terminal it asks nothing — and both are put back by the
# restore from the values this step reads first.
step_settings() {
  if [ -z "$PROJECT" ]; then
    unmeasured settings "there is no installation whose settings could be read: the row above says why"
    return 0
  fi
  if ! read_setting escalation.merge_judge "$SCRATCH/judge.before"; then
    unmeasured settings "fugaro settings get escalation.merge_judge --json did not answer (exit ${BOUNDED_STATUS}): this check cannot say whether the ladder has a judge named, and a merge at rung 3 with none is a question for you rather than a landing. What it printed is in $SCRATCH/get-escalation_merge_judge.err"
    return 0
  fi
  local judge
  judge=$(field "$SCRATCH/judge.before" value)
  if [ -z "$judge" ]; then
    fact settings FAIL "escalation.merge_judge is unset on this installation, so the ladder's rung 3 asks you instead of judging and nothing merges by itself (escalation.Grades is the closed list: strong or strongest). Name one once, at your terminal — \`fugaro settings set escalation.merge_judge strong\` — and run this check again. Nothing below can merge without it"
    return 0
  fi
  if [ -n "$SKIP_SETTINGS" ]; then
    fact settings INFO "not written (CHECK540_SKIP_SETTINGS): escalation.merge_judge is '${judge}' and the two switches below were left as they are, so the walk runs on the settings this installation already has"
    return 0
  fi
  local wrote=""
  write_setting escalation.ready_drafts true "it lets the ladder ready the stalled draft above" || return 0
  wrote="escalation.ready_drafts=true"
  write_setting escalation.r2_after_attempts 1 "so the item reaches rung 2 and then rung 3 while you watch rather than on the shipped clock of hours" || return 0
  wrote="${wrote}, escalation.r2_after_attempts=1"
  local before_list
  before_list=$(awk -F'\t' '{ printf "%s=%s ", $1, $3 }' "$SETTINGS_LOG" 2>/dev/null | sed 's/ $//')
  fact settings PASS "escalation.merge_judge is '${judge}', and for the length of this check the local layer holds ${wrote} (it held ${before_list:-nothing}, read with fugaro settings get --json and put back by the restore): the writes are the owner's own hand, so no --yes was passed and none of them asked you anything"
}

# read_setting KEY DEST: the value a run gets for KEY and the value the local
# layer holds for it, into one file. Answers 0 when the read answered.
read_setting() { # read_setting KEY DEST
  run_bounded "$CLI_TIMEOUT" "get-${1//[^A-Za-z0-9]/_}" "$SANDBOX" "$FUGARO_BIN" settings get "$1" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    return 1
  fi
  read_json setting "$SCRATCH/get-${1//[^A-Za-z0-9]/_}.out" "$2"
}

# write_setting KEY VALUE WHY: one typed write into the local layer, with the
# value the key held before it recorded for the restore. A write the CLI refuses
# is a FAIL row and the step stops: the block says what the CLI said.
write_setting() { # write_setting KEY VALUE WHY
  local key=$1 value=$2 why=$3 keyfile=${1//[^A-Za-z0-9]/_}
  local before
  # What the local layer holds for the key is read BEFORE it is written: the
  # restore puts back this value, and a write whose before-value could not be
  # read is a write this check could not undo, so it is not made at all.
  if ! read_setting "$key" "$SCRATCH/get-${keyfile}.before"; then
    unmeasured settings "fugaro settings get ${key} --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and this check is about to write that key: a switch whose previous value could not be read is one the restore could not put back, so the write was not made and no cycle was driven behind it. What it printed is in $SCRATCH/get-${keyfile}.err"
    return 1
  fi
  before=$(awk -F'\t' '$1 == "level" && $2 == "local" { print $3; exit }' "$SCRATCH/get-${keyfile}.before" 2>/dev/null || true)
  run_bounded "$CLI_TIMEOUT" "set-${keyfile}" "$SANDBOX" "$FUGARO_BIN" settings set "$key" "$value" --layer local
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    local said
    said=$(head -c 600 "$SCRATCH/set-${keyfile}.err" 2>/dev/null | tr '\n' ' ')
    fact settings FAIL "fugaro settings set ${key} ${value} --layer local exited ${BOUNDED_STATUS} in ${SANDBOX}, so ${why} cannot be measured. What it said: ${said:-nothing}. If the refusal is the typed fence, it is the one write a coding agent's session may not make: run this at your own terminal"
    return 1
  fi
  printf '%s\t%s\t%s\n' "$key" "$value" "${before:-unset}" >>"$SETTINGS_LOG"
  say "wrote ${key}=${value} into the local layer of ${SANDBOX} (it was ${before:-unset}; the restore puts it back)"
  return 0
}

# step_walk: drive the daemon's own cycle and read what the ladder did. One
# `orchestrator run --once` is one cycle — it takes the lease, runs a cycle and
# releases it — so this check drives the ladder the way the installed service
# does and never merges anything itself. The loop stops at the first reading that
# says the walk is over: the pull request merged, or a rung-3 refusal (which is
# the ladder asking you a question, not a walk still going), or the bound.
step_walk() {
  if [ -z "$PROJECT" ] || [ -z "$SLUG" ]; then
    unmeasured walk "there is no installation or repository to drive: the rows above say why"
    return 0
  fi
  local started now cycle=0 stop=no
  started=$(date +%s)
  while :; do
    cycle=$((cycle + 1))
    CYCLES_DRIVEN=$cycle
    say "cycle ${cycle}/${CYCLES}: fugaro orchestrator run --once, in ${SANDBOX}"
    run_bounded "$CYCLE_TIMEOUT" "cycle-${cycle}" "$SANDBOX" "$FUGARO_BIN" orchestrator run --once
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      say "cycle ${cycle} did not finish within ${CYCLE_TIMEOUT}s and was ended"
    fi
    if ! read_ledger; then
      unmeasured walk "fugaro escalation show ${ITEM} --json did not answer after cycle ${cycle} (exit ${BOUNDED_STATUS}): the ledger this check reads the rungs from could not be read. What it printed is in $SCRATCH/show.err"
      return 0
    fi
    RUNGS_SEEN=$(field "$SCRATCH/show.txt" rungs)
    if [ -z "$FIRST_MOVE_AT" ]; then
      FIRST_MOVE_AT=$(awk -F'\t' '$1 == "tr" { print $5; exit }' "$SCRATCH/show.txt")
      FIRST_MOVE_EPOCH=$(awk -F'\t' '$1 == "tr" { print $6; exit }' "$SCRATCH/show.txt")
    fi
    if ! read_events; then
      unmeasured walk "fugaro orchestrator events --json did not answer after cycle ${cycle} (exit ${BOUNDED_STATUS}): the audit line the ladder records a rung-3 wait in could not be read, and that line is what tells this check the walk is over. What it printed is in $SCRATCH/events.err"
      return 0
    fi
    WAIT_LINE=$(ladder_wait)
    if pull_request_merged; then
      WALK_STOPPED="the pull request is MERGED after ${cycle} cycle(s)"
      break
    fi
    if [ -n "$WAIT_LINE" ]; then
      WALK_STOPPED="the ladder recorded a rung-3 wait after ${cycle} cycle(s) — '${WAIT_LINE:0:200}' — so it is asking you something rather than walking on"
      break
    fi
    if [ "$cycle" -ge "$CYCLES" ]; then
      WALK_STOPPED="the bound of ${CYCLES} cycles was reached"
      break
    fi
    now=$(date +%s)
    if [ $((now - started)) -ge "$WALK_TIMEOUT" ]; then
      WALK_STOPPED="the bound of ${WALK_TIMEOUT}s of wall clock was reached after ${cycle} cycle(s)"
      break
    fi
    sleep "$POLL_SECONDS"
  done
  fact cycles PASS "${CYCLES_DRIVEN} cycle(s) of \`fugaro orchestrator run --once\` were driven in ${SANDBOX} ($(human_seconds "$(($(date +%s) - started))") of wall clock): ${WALK_STOPPED}"
}

# read_ledger: the item's ledger into the scratch. An item the ladder has never
# acted on is a READING and not a failed read: `escalation show` says so and exits
# non-zero (the item has no rung and no transitions), and that state is the one
# finding this check is most likely to come back with.
read_ledger() { # read_ledger
  run_bounded "$CLI_TIMEOUT" show "$SANDBOX" "$FUGARO_BIN" escalation show "$ITEM" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    if grep -q 'never entered the escalation ladder\|no work item' "$SCRATCH/show.err" 2>/dev/null; then
      : >"$SCRATCH/show.txt"
      return 0
    fi
    return 1
  fi
  read_json show "$SCRATCH/show.out" "$SCRATCH/show.txt"
}

# read_events: the audit lines of the plan's own node. The rung-3 refusal is one of
# them (the ladder writes `escalation: rung R3 (…), run … wait: <clauses>`) and it is
# the reading that says the ladder has STOPPED rather than that a cycle is still
# going: rung 3 launches the review it merges on and the landing comes in a later
# cycle, so a walk stopped at the first sight of rung 3 in the ledger would report a
# working ladder as a pull request that never merged.
read_events() { # read_events
  run_bounded "$CLI_TIMEOUT" events "$SANDBOX" "$FUGARO_BIN" orchestrator events --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    return 1
  fi
  read_json events "$SCRATCH/events.out" "$SCRATCH/events.txt"
}

# ladder_wait: the last audit line in which the ladder's own rung-3 merge attempt was
# recorded as a wait — `escalation: rung R3 (…), run …, wait: <clauses>` — and "" when
# no cycle has written one. A retry's own line is not one of these (it is written under
# the word `retry`, and the ladder is still going), so the match is anchored on the rung.
ladder_wait() { # ladder_wait
  awk -F'\t' '$1 == "event" && $5 ~ /^escalation: rung R[34] .*wait: / { line = $5 } END { if (line != "") print line }' \
    "$SCRATCH/events.txt" 2>/dev/null || true
}

# pull_request_merged: the host's own answer, and the one that counts: the ledger
# saying a landing happened is the daemon's record of its own intent, and the
# merged state at GitHub is whether the merge is really there.
pull_request_merged() { # pull_request_merged
  run_bounded "$CLI_TIMEOUT" pr-now "$SANDBOX" gh pr view "$PR" --repo "$SLUG" \
    --json state,isDraft,headRefOid,mergedAt,url,number
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    return 1
  fi
  read_json pr "$SCRATCH/pr-now.out" "$SCRATCH/pr-now.txt" || return 1
  PR_STATE=$(field "$SCRATCH/pr-now.txt" state)
  PR_DRAFT=$(field "$SCRATCH/pr-now.txt" is_draft)
  HEAD_AFTER=$(field "$SCRATCH/pr-now.txt" head_sha)
  MERGED_AT=$(field "$SCRATCH/pr-now.txt" merged_at)
  MERGED_EPOCH=$(field "$SCRATCH/pr-now.txt" merged_at_epoch)
  [ "$PR_STATE" = MERGED ]
}

# step_walk_row: what the ledger holds for the item, in the ladder's own words:
# the rungs reached, the reason each was entered, and whether the walk got as far
# as the merge rung at all.
step_walk_row() {
  if [ -z "$ITEM" ] || [ -z "$RUNGS_SEEN" ]; then
    if grep -q 'no work item\|never entered' "$SCRATCH/show.err" 2>/dev/null; then
      fact walk FAIL "the ledger of ${PROJECT} holds nothing for ${ITEM} after ${CYCLES_DRIVEN} cycle(s) (\`fugaro escalation show ${ITEM}\` answers 'no work item' or 'never entered the escalation ladder', so no rung was ever walked for it). Two readings, and they are different findings. (1) The daemon you are running was built without the ladder's seam — the cycle internal/cli/orchestrator_run.go builds carries no Ladder, so escalation.Decide is never called, no rung is walked and \`fugaro escalation ls\` answers an empty list. That is the known limit 0.26.0 ships (docs/releases/v0.26.0.md: 'Fugaro merges nothing today'); check \`fugaro version\` and run this again on the release that wires it. (2) Your build carries the seam and the ladder really did not act: read \`fugaro orchestrator events\` for the cycles above, and check that the item is APPROVED and that its runs are terminal — an item the ladder may not launch runs for is an item it cannot walk"
      return 0
    fi
    unmeasured walk "the ledger could not be read for ${ITEM}: what the read answered is in $SCRATCH/show.out and $SCRATCH/show.err"
    return 0
  fi
  local reasons
  reasons=$(awk -F'\t' '$1 == "tr" { printf "%s->%s(%s) ", ($2 == "" ? "-" : $2), $3, $4 }' "$SCRATCH/show.txt")
  if case ",${RUNGS_SEEN}," in *,R3,*) true ;; *) false ;; esac; then
    fact walk PASS "the ledger records ${ITEM} at rung $(field "$SCRATCH/show.txt" rung) (reason $(field "$SCRATCH/show.txt" reason)) over $(field "$SCRATCH/show.txt" transitions) transition(s): ${reasons}— rungs ${RUNGS_SEEN} were reached, so the ladder walked the item up to the merge rung"
    return 0
  fi
  fact walk FAIL "the ledger records ${ITEM} over $(field "$SCRATCH/show.txt" transitions) transition(s) — ${reasons}— and rung 3 (the merge rung) is not among the rungs reached (${RUNGS_SEEN:-none}). The walk is what this check measures: read \`fugaro orchestrator events\` for the cycles above to see which clause stopped it, and \`fugaro escalation show ${ITEM}\` for the rung it is standing at"
}

# step_merge: the merge, read at the host rather than from the ledger — a rung 3
# that recorded a landing and a pull request GitHub still holds open is the one
# disagreement this row exists to catch.
step_merge() {
  if [ -z "$PR_STATE" ]; then
    unmeasured merge "the pull request was never read: the row above says why"
    return 0
  fi
  if [ "$PR_STATE" = MERGED ]; then
    local changed=""
    if [ -n "$HEAD_BEFORE" ] && [ "$HEAD_AFTER" != "$HEAD_BEFORE" ]; then
      changed=", and its head moved during the walk (${HEAD_BEFORE:0:8} -> ${HEAD_AFTER:0:8}: the rebase at rung 0 and the review at the rebased head are what move it)"
    fi
    fact merge PASS "the pull request ${SLUG}#${PR} is MERGED at ${MERGED_AT}${changed}, read with gh pr view: the ladder landed it, and the merge is at the host rather than only in the ledger"
    return 0
  fi
  fact merge FAIL "the pull request ${SLUG}#${PR} is ${PR_STATE:-unreadable} after ${CYCLES_DRIVEN} cycle(s) and the walk stopped because ${WALK_STOPPED}. Rung 3 is the merge rung: a walk that reached it and left the pull request open is the ladder asking YOU something — \`fugaro orchestrator events\` holds the refusal's own clause (a non-Routine path set, a veto, a draft still held, CI read red), and the question is waiting for you in \`fugaro orchestrator state --brief\`"
}

# step_time_to_merge: the figure the check is named for. It is measured and not
# assumed: the walk's start is the ledger's own stamp on the first rung move and
# the end is the host's mergedAt, so the two clocks are the ones that really
# decided, and both are named in the row.
step_time_to_merge() {
  if [ "$PR_STATE" != MERGED ]; then
    unmeasured "time-to-merge" "there is no merge to measure the time to: the row above says why"
    return 0
  fi
  if [ -z "$MERGED_EPOCH" ] || [ -z "$FIRST_MOVE_EPOCH" ]; then
    fact "time-to-merge" INFO "not dated: the merge is at ${MERGED_AT:-an unreadable stamp} and the first rung move at ${FIRST_MOVE_AT:-an unreadable stamp}, and one of the two is not a timestamp this check could read (the ledger's stamps are the database server's, D1230). The walk and the merge above are the readings; only the duration is missing"
    return 0
  fi
  local secs age
  secs=$((MERGED_EPOCH - FIRST_MOVE_EPOCH))
  age=$(field "$SCRATCH/show.txt" age_seconds)
  if [ "$secs" -lt 0 ]; then
    fact "time-to-merge" INFO "not dated: the merge is stamped before the first rung move (${MERGED_AT} against ${FIRST_MOVE_AT}), so this check cannot read a duration out of the two. The stamps are in the row above"
    return 0
  fi
  fact "time-to-merge" PASS "the walk took $(human_seconds "$secs") (${secs}s) from the first rung move (${FIRST_MOVE_AT}) to the merge (${MERGED_AT}), and the item had been going ${age:-an unknown number of} seconds when the ledger was last read: the ladder's own clock, read from the server's stamps rather than from this machine's"
}

# step_follow_ups: what the landing left open. A rung-3 landing files what it did
# not block as items of its own (D2040), named `<item>-fu-…`, and D1226/D1245
# leave every one of them queued and unapproved: a landing that approved its own
# follow-up is the finding this row is for, because it would spend the person's
# approval on work nobody read.
step_follow_ups() {
  if [ -z "$PROJECT" ]; then
    unmeasured follow-ups "there is no project whose backlog could be read: the row above says why"
    return 0
  fi
  say "reading the backlog: fugaro backlog list --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" backlog "$SANDBOX" "$FUGARO_BIN" backlog list --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured follow-ups "fugaro backlog list --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the follow-ups the landing filed can only be read from the backlog. What it printed is in $SCRATCH/backlog.err"
    return 0
  fi
  if ! read_json backlog "$SCRATCH/backlog.out" "$SCRATCH/backlog.txt"; then
    unmeasured follow-ups "fugaro backlog list --json printed a document this check could not read: what it printed is in $SCRATCH/backlog.out"
    return 0
  fi
  local found approved listed
  found=$(awk -F'\t' -v p="${ITEM}-fu-" '$1 == "item" && index($2, p) == 1 { print $2 }' "$SCRATCH/backlog.txt")
  listed=$(awk -F'\t' -v p="${ITEM}-fu-" '$1 == "item" && index($2, p) == 1 { printf "%s(%s%s) ", $2, $3, ($5 == "true" ? ",approved" : "") }' "$SCRATCH/backlog.txt")
  if [ -z "$found" ]; then
    if [ "${EXPECT_FOLLOWUPS:-1}" = "0" ]; then
      fact follow-ups PASS "the landing filed no follow-up for ${ITEM} and CHECK540_EXPECT_FOLLOWUPS=0 records that a clean landing is what you expected: there is no pending follow-up to read, and the walk above is the reading this run gives"
      return 0
    fi
    unmeasured follow-ups "the landing filed no follow-up for ${ITEM} (no backlog item named ${ITEM}-fu-…), so this row cannot say whether a follow-up waits for you: the walk landed the pull request and left nothing open — which is what a landing whose findings are all fixed looks like, and not a pending follow-up. Give the item a task whose review leaves a finding and run the walk again, or set CHECK540_EXPECT_FOLLOWUPS=0 to record that a clean landing is what you expected"
    return 0
  fi
  approved=$(awk -F'\t' -v p="${ITEM}-fu-" '$1 == "item" && index($2, p) == 1 && $5 == "true" { print $2 }' "$SCRATCH/backlog.txt")
  if [ -n "$approved" ]; then
    fact follow-ups FAIL "the landing filed follow-up(s) ${listed}and $(printf '%s' "$approved" | tr '\n' ' ')is APPROVED: nothing the ladder files may be approved by anything but you (D1226, D1245, G15), and a landing that approves its own next attempt spends your word on work you did not read. Unapprove it and read what it says"
    return 0
  fi
  fact follow-ups PASS "the landing filed ${found//$'\n'/, } — queued and unapproved in the backlog (${listed}), with the project's own waiting count at $(field "$SCRATCH/backlog.txt" waiting): what a landing leaves open waits for you to approve it, which is what D1226 and D1245 require. \`fugaro backlog list\` prints them, and approving one is your own step"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  if [ -n "$DRY_RUN" ]; then
    RESTORE_NOTE="nothing was written and nothing was run: this was a dry run, so no setting was touched and no cycle was driven"
    return 0
  fi
  local key want before note="" failed=""
  if [ -s "$SETTINGS_LOG" ]; then
    while IFS=$'\t' read -r key want before; do
      [ -n "$key" ] || continue
      local keyfile=${key//[^A-Za-z0-9]/_}
      if [ "$before" = unset ]; then
        run_bounded "$CLI_TIMEOUT" "unset-${keyfile}" "$SANDBOX" "$FUGARO_BIN" settings unset "$key" --layer local
      else
        run_bounded "$CLI_TIMEOUT" "restore-${keyfile}" "$SANDBOX" "$FUGARO_BIN" settings set "$key" "$before" --layer local
      fi
      if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
        failed="${failed}${failed:+, }${key}"
        note="${note} ${key} could NOT be put back to '${before}' (the CLI exited ${BOUNDED_STATUS});"
      else
        note="${note} ${key} is back at ${before};"
      fi
    done <"$SETTINGS_LOG"
  fi
  if [ -n "$failed" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the ladder's settings could not all be put back:${note} put ${failed} back by hand at your terminal (\`fugaro settings get ${failed%% *} --json\` reads what they hold now)."
  else
    RESTORE_NOTE="the ladder's settings are back where they were:${note:- nothing needed writing}. This check wrote no other file and no policy. It did not merge anything itself: ${SLUG:-the sandbox repository}#${PR:-?} was merged by the ladder's own rung 3, if it merged at all, and it is not un-merged here. Its branch, and every follow-up the landing filed, are yours to deal with: the follow-ups wait for your approval, which is where D1226 leaves them"
  fi
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
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
  say "CHECK 540 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation this checkout belongs to, and gh repo view --json nameWithOwner for the repository"
  say "  [read, host] gh pr view ${PR:-<n>} --repo ${SLUG:-<owner/name>} --json state,isDraft,headRefOid,mergedAt — the pull request has to be OPEN and a DRAFT: readying a stalled draft is the one thing escalation.ready_drafts switches on"
  say "  [read] fugaro settings get escalation.merge_judge --json — a merge at rung 3 needs a judge named; unset, the ladder asks you instead of landing"
  for key in escalation.ready_drafts escalation.r2_after_attempts; do
    say "  [write, YOUR OWN HAND] fugaro settings set ${key} ... --layer local — put back by the restore (no --yes: the typed write asks nothing at your terminal and is refused in a coding agent's session)"
  done
  say "  [network, BILLABLE, drives the daemon] fugaro orchestrator run --once in ${SANDBOX}, up to ${CYCLES} cycle(s) every ${POLL_SECONDS}s under a ${WALK_TIMEOUT}s bound — the ladder's own runs (the rung 0 rebase, the rung 2 pass, the review at the rebased head, the judge, the merge) are launched by the daemon and covered by the item's approval"
  say "  [read] fugaro escalation show ${ITEM:-<item>} --json — the rungs, the reason each was entered, and the head each was decided at"
  say "  [read, host] gh pr view — MERGED and when, or the refusal the walk stopped on"
  say "  [read] fugaro backlog list --json — the follow-ups the landing filed (${ITEM:-<item>}-fu-…), queued and unapproved"
  for key in installation pull-request settings cycles walk merge time-to-merge follow-ups; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no setting was written, no cycle was driven, no run was launched and no pull request was read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation pull-request
  settings cycles walk merge time-to-merge follow-ups restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 540 RESULT\n' >&3
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
  printf 'CHECK 540 RESULT END (exit %s)\n' "$status" >&3
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
ITEM_CLI=""
PR_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the daemon this check drives is the daemon of"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --item)
    [ $# -ge 2 ] || die "--item needs the id of the work item whose pull request the ladder walks (fugaro backlog list prints the ids)"
    ITEM_CLI=$2
    shift 2
    ;;
  --pr)
    [ $# -ge 2 ] || die "--pr needs the number of the stalled draft pull request the walk is about"
    PR_CLI=$2
    shift 2
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

SANDBOX=${CHECK540_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 540 needs --sandbox: the checkout of a repository of the sandbox project. Usage: bash scripts/live-checks/check540.sh --sandbox <checkout> --item <work item> --pr <number>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
ITEM=${CHECK540_ITEM:-${ITEM_CLI:-}}
PR=${CHECK540_PR:-${PR_CLI:-}}
if [ -z "$ITEM" ]; then
  die "Check 540 needs --item: the id of the work item whose stalled draft the ladder walks, which is the id its own runs and its follow-ups are named from (fugaro backlog list prints the ids). Usage: bash scripts/live-checks/check540.sh --sandbox <checkout> --item <work item> --pr <number>"
fi
if [ -z "$PR" ]; then
  die "Check 540 needs --pr: the number of the stalled draft pull request the pull request the item's runs opened, which every reading below is at. Usage: bash scripts/live-checks/check540.sh --sandbox <checkout> --item <work item> --pr <number>"
fi
case "$PR" in
*[!0-9]*) die "--pr takes a pull request number, and '$PR' is not one" ;;
esac
DRY_RUN=${CHECK540_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK540_KEEP_SCRATCH:-}
SKIP_SETTINGS=${CHECK540_SKIP_SETTINGS:-}
EXPECT_FOLLOWUPS=${CHECK540_EXPECT_FOLLOWUPS:-1}
REGION=${CHECK540_REGION:-us-east5}
CYCLES=${CHECK540_CYCLES:-90}
POLL_SECONDS=${CHECK540_POLL_SECONDS:-60}
WALK_TIMEOUT=${CHECK540_WALK_TIMEOUT_SECONDS:-5400}
CYCLE_TIMEOUT=${CHECK540_CYCLE_TIMEOUT_SECONDS:-600}
CLI_TIMEOUT=${CHECK540_CLI_TIMEOUT_SECONDS:-180}
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
  [ -n "$FUGARO_BIN" ] || die "Check 540 needs 'fugaro' on PATH: the check drives the ladder you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_installation
  step_pull_request
  # The switches are written only once the premise holds: a check that turned
  # escalation.ready_drafts on and then refused to walk — a pull request that is
  # not the stalled draft, a repository whose name could not be read — would have
  # changed the installation for a run it never measured.
  if [ "$(row_verdict installation)" = FAIL ] || [ "$(row_verdict pull-request)" = FAIL ]; then
    fact settings INFO "not written: the premise above did not hold (the installation and the stalled draft the walk would be about), so this check touched none of the installation's settings"
  else
    step_settings
  fi
  # The walk needs the premise above and the switches the settings row writes: a
  # pull request that is not an open draft, a repository whose name could not be
  # read, or a ladder with no judge named is a block that says so rather than a
  # walk driven against a reading that cannot happen.
  if [ -z "$PROJECT" ] || [ -z "$SLUG" ] || [ "$(row_verdict installation)" = FAIL ] ||
    [ "$(row_verdict pull-request)" = FAIL ] || [ "$(row_verdict settings)" = FAIL ]; then
    say "stopping before the walk: the premise above did not hold"
    return 0
  fi
  step_walk
  step_walk_row
  step_merge
  step_time_to_merge
  step_follow_ups
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
