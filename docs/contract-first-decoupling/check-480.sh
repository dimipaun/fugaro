#!/usr/bin/env bash
# Check 480 — a launched dependent conforms to an amended contract (U4).
#
# Plan task CD5 of docs/contract-first-decoupling/implementation-plan.md writes
# this file, and YOU run it; the plan's own "Checks (480-489 reserved; 480 and 481
# used)" section is where this number is registered. What it measures is the half
# of the pass no hermetic test can take:
# a dependent that is already launched cannot be reached by the plan, so an
# amendment reaches it as a follow-up run whose instructions name the contract
# file as it is on the base branch and ask it to merge the base and conform
# (D1812, U4). This script sends exactly that follow-up and reads the branch back
# afterwards.
#
# THE AMENDMENT IS YOURS. This script commits nothing, pushes nothing and edits
# no file of yours: it reads the base branch, sends the one run the pass would
# propose, and reads the branch back. A plan with nothing amended on the base is
# not measurable, so it stops there rather than spend a run measuring nothing.
#
# The subject is a plan at the repository root: the graph is read through
# `fugaro plan graph` (D1821 — the CLI validates it, and this script never parses
# the document), and the contract node, its own file, the dependent's own paths
# and the dependent's run all come from there. --plan names the plan when
# docs/plans/ holds more than one graph.
#
# What it asserts, in the order it measures:
#
#   plan          the graph reads and broke exactly one edge: the contract node,
#                 the file of its own, the dependent's paths and its run are
#                 taken from the CLI's answer and not from a reading of the file;
#   dependent     that run has opened a pull request, and the branch head before
#                 the follow-up is known;
#   amendment     the base branch's copy of the contract file differs from the
#                 branch's — both branches fetched from origin, and the branch's
#                 head checked against the one the run row names — so there IS an
#                 amendment to meet;
#   instructions  the text this check sends names the contract file and carries
#                 no line of the contract's body — instructions are trusted text,
#                 so a contract pasted into them is plan text the runner acts on
#                 as its own (D1812);
#   launch        `fugaro run --pr N` accepted the follow-up (this is the run the
#                 check spends);
#   run           the follow-up ended succeeded and pushed a new head;
#   base-merged   the amended base commit is an ancestor of the branch head;
#   conformed     the branch's copy of the contract file is byte-identical to the
#                 base's: the run took the amendment instead of editing the
#                 contract to suit itself;
#   own-code      the run's own commits touch the dependent's own files and none
#                 of the contract's — the F10 boundary, measured on a live run,
#                 and read from the commits the run made rather than from the
#                 whole range, which carries the base's own change to the
#                 contract the moment the run merges it;
#   restore       nothing but the scratch directory is this check's to undo.
#
# Usage:      bash docs/contract-first-decoupling/check-480.sh [--dry-run]
#             bash docs/contract-first-decoupling/check-480.sh [--plan <plan>]
#                 [--pr N] [--base <branch>] [--poll SECONDS] [--run-seconds N]
#             Run it from the checkout of the SANDBOX repository the plan
#             describes, after the contract is amended on its base branch and the
#             dependent's run has opened its pull request. --dry-run prints every
#             command a real run would run and sends nothing; -h/--help prints
#             this block.
# Needs:      bash, mktemp, git, jq and fugaro on PATH.
# Where:      that checkout and its remote, and nothing else: no other project,
#             no other repository, and never `belong`, EdgeWeb or EdgeServer —
#             an origin naming one of those is refused by name. The one thing it
#             writes in your checkout is the fetched ref it reads the branch back
#             from; everything else lives in a scratch directory it removes.
# Costs:      ONE cloud run — the follow-up on the pull request — with its model
#             and compute spend, under the same caps as any other run. Nothing
#             else is created, and the check runs no other command of yours.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 480 RESULT,
#             whose last line is the verdict and the exit status:
#
#               CHECK 480 RESULT: PASS (exit 0)        every criterion held;
#               CHECK 480 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 480 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything
#                                                      (a guard, a refusal, an
#                                                      error, a timeout).
#
#             A row reading `FAIL — not reached` was NOT measured, and `aborted`
#             says what stopped the run.
# Cleanup:    always, including on a Ctrl-C: the scratch directory goes and the
#             `restore` row says whether it really did. Nothing else is this
#             check's to undo — the amendment on the base branch is yours, and the
#             follow-up's commits on the pull-request branch are the run's.
#
# The knobs (CHECK480_*, and the flags that override them) change only how long
# the check waits and how often it looks: nothing here can point it at another
# repository, and no argument can make it read the graph itself.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace prints the
# commands and the rows this check reads, into the log you would then paste.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block goes to
# the saved descriptor rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; ABORT_REASON=$*; exit 2; }

ABORT_REASON=""
REACHED_END=no
HELP=""
DRY_RUN=""
CLEANUP_RUNNING=no
FACTS_FILE=""

fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# stop REASON: records why the run ended and exits 2. The EXIT trap prints the
# block and cleans up, so a stopped run still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

# An internal error is an abort and not a finding under `set -e`: without this a
# failed command would end the shell at the line it failed on, and a block that
# printed afterwards would offer the rows of a stopped run as a verdict about the
# pass.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the follow-up" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 480 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 480 needs bash"
  need mktemp
  need git "the branch is read back from the repository's own remote rather than from the run's report"
  need jq "the graph's answer and the run's row are read by jq, so no figure in the block is read by eye"
  need fugaro "the graph is read, the runs are listed and the follow-up is launched through the CLI"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk480-mine"
SCRATCH_REMOVED=no
PRINTED=""
MANUAL_LINE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk480.XXXXXXXX") || die "Check 480 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 480 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  CLEANUP_RUNNING=yes
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
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
  # Guarded: this runs from the exit trap, where a failing `rm` under `set -e`
  # would end the shell before the block is printed and lose what it says.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  if [ -n "$HELP" ]; then exit 0; fi
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the follow-up"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  local restore=PASS
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, and no run was launched"
  else
    [ "$SCRATCH_REMOVED" = yes ] || restore=FAIL
    local note
    note="the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself")"
    if [ -n "$FOLLOWUP" ]; then
      note="$note. The follow-up $FOLLOWUP and its commits on $BRANCH are the run's and stay: this check undoes neither, and the amendment on $BASE is yours"
    else
      note="$note. No run was launched and nothing on any branch or pull request was touched"
    fi
    facts="${facts}"$'\n'"restore|$restore|${note}"
  fi
  # The restore row is a criterion like any other and it exists only now, so the
  # status main decided before it is re-decided here: a finished run with a FAIL
  # row exits 1, and a run that stopped early keeps 2 and reads INCOMPLETE.
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The word the block ends on follows the exit status, not the rows: a run that
# stopped early has rows reading FAIL — not reached, and printing FAIL there
# would offer a row nobody measured as a verdict about the follow-up.
verdict_word() {
  case "$1" in
  0) printf 'PASS\n' ;;
  1) printf 'FAIL\n' ;;
  *) printf 'INCOMPLETE\n' ;;
  esac
}

# The block's shape does not depend on how far the run got: these keys print in
# this order whatever happened, a key the run never reached reads FAIL — not
# reached, and any other recorded key follows in the order it was recorded.
BLOCK_KEYS=(setup plan dependent amendment instructions launch run base-merged conformed own-code restore)

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 480 RESULT\n' >&3
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
  if [ -n "$MANUAL_LINE" ]; then
    printf 'MANUAL: %s\n' "$MANUAL_LINE" >&3
  else
    printf 'MANUAL: nothing beyond the block: this check measures its criteria itself, and the amendment it read is yours to keep or revert\n' >&3
  fi
  printf 'CHECK 480 RESULT: %s (exit %s)\n' "$(verdict_word "$status")" "$status" >&3
}

interrupted() {
  owner "interrupted"
  exit 130
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  interrupted
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on, which prints the rows and the branch this check reads into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash docs/contract-first-decoupling/check-480.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run is printed a second time into a log"
}

# The repository comes from the checkout's origin rather than from an argument:
# a check that couples two repositories can be pointed at a second one by
# mistake, and this one sends a run.
guard_checkout() {
  ROOT=$(git rev-parse --show-toplevel 2>/dev/null) \
    || stop "Check 480 stopped: this is not a git checkout, and the repository it reads is that checkout's own origin. Run it from the sandbox repository the plan describes"
  ORIGIN=$(git -C "$ROOT" remote get-url origin 2>/dev/null) \
    || stop "Check 480 stopped: this checkout has no origin remote, so which repository the plan describes is unknown"
  # A token-based clone carries its credential in the origin's userinfo, and this
  # origin is printed in the setup row: it goes before anything is matched or
  # recorded, so what is printed is the repository and nothing that opens it.
  ORIGIN=$(printf '%s' "$ORIGIN" | sed -E 's#//[^@/]*@#//#')
  case "$ORIGIN" in
  *github.com[:/]*/* | *bitbucket.org[:/]*/*) : ;;
  *) stop "Check 480 stopped: this checkout's origin ('${ORIGIN}') names no github.com or bitbucket.org repository, so which repository to read is unknown" ;;
  esac
  REPO=$(printf '%s' "$ORIGIN" | sed -E 's#^.*(github\.com|bitbucket\.org)[:/]##; s#\.git$##')
  case "$REPO" in
  */*/* | "") stop "Check 480 stopped: this checkout's origin names no owner/name repository" ;;
  esac
  say "repository: $REPO (origin ${ORIGIN})"
}

# `belong` is the owner's own installation, EdgeWeb and EdgeServer its
# repositories. This check sends a real run at whatever repository it is pointed
# at, so one whose origin names any of the three is refused by name rather than
# measured — and past that refusal the setup row is recorded, because the
# repository, the tools and the trace state are what this check's setup is.
guard_repository() {
  local low
  low=$(printf '%s' "$REPO" | tr 'A-Z' 'a-z')
  case "/${low}/" in
  */belong/* | */edgeweb/* | */edgeserver/*)
    stop "Check 480 stopped: '${REPO}' names the owner's own installation, and this check sends a run. Run it from a checkout of a sandbox repository"
    ;;
  esac
  fact setup PASS "bash, git, jq and fugaro are on PATH; this is the checkout of ${REPO} (${ORIGIN}); this shell is not tracing"
}

# ------------------------------------------------------------- the steps
# fetch_ref BRANCH: fetch one branch and print the commit it points at now.
# FETCH_HEAD is read at once and by name rather than through a remote-tracking
# ref, so nothing this check reads depends on how the checkout is configured.
fetch_ref() {
  git -C "$ROOT" fetch --quiet origin "refs/heads/$1" 2>"$SCRATCH/fetch.err" || return 1
  git -C "$ROOT" rev-parse FETCH_HEAD
}

# graph_field JQ-ARGS...: one value out of the read's answer. The graph is the
# CLI's to read (D1821): every path and id below comes from this answer, and the
# script never looks at the document itself.
graph_field() {
  jq -r "$@" "$GRAPH"
}

# read_plan: the graph, through the CLI, and the four things the check needs out
# of it — the contract node, its own file, the dependent's paths and its run.
read_plan() {
  local args=(--json)
  [ -n "$PLAN" ] && args=(--plan "$PLAN" "${args[@]}")
  GRAPH="$SCRATCH/graph.json"
  if ! fugaro plan graph "${args[@]}" >"$GRAPH" 2>"$SCRATCH/graph.err"; then
    stop "Check 480 stopped: 'fugaro plan graph' refused the plan — $(tr '\n' ' ' <"$SCRATCH/graph.err") The check reads the contract node, its stubs and the dependent's own files out of the graph, and the graph is the CLI's to read"
  fi
  local broken
  broken=$(graph_field '(.decoupling // []) | length')
  if [ "$broken" != 1 ]; then
    stop "Check 480 stopped: the plan records $broken broken edge(s) and this check measures one — the follow-up it sends is about one contract. Pass --plan for a graph that broke exactly one edge"
  fi
  CONTRACT_NODE=$(graph_field '.decoupling[0].contract')
  DEP_NODE=$(graph_field '.decoupling[0].was.task')
  WAITED_ON=$(graph_field '.decoupling[0].was.needs')
  INTEGRATION_NODE=$(graph_field '.decoupling[0].integration')
  DEP_RUN=$(graph_field --arg id "$DEP_NODE" '.nodes[] | select(.id==$id) | .run // empty' 2>/dev/null || true)
  if [ -z "$CONTRACT_NODE" ] || [ -z "$DEP_NODE" ]; then
    stop "Check 480 stopped: the plan's decoupling record names no contract and no dependent, which the read should have refused"
  fi
  graph_field --arg id "$DEP_NODE" '.nodes[] | select(.id==$id) | .footprint.paths[]' >"$SCRATCH/dep-paths" 2>/dev/null || true
  if [ ! -s "$SCRATCH/dep-paths" ]; then
    stop "Check 480 stopped: the dependent node $DEP_NODE predicts no path, so the check cannot tell its own files from the contract's"
  fi
  graph_field --arg id "$CONTRACT_NODE" '.nodes[] | select(.id==$id) | .footprint.paths[]' >"$SCRATCH/contract-paths" 2>/dev/null || true
  if [ -z "$CONTRACT_FILE" ]; then
    # The contract's own file is the one its footprint names that is not one of
    # its stubs: `stubs` lists the files the integration task deletes, and those
    # are scaffolding rather than the file the dependent builds against.
    CONTRACT_FILE=$(graph_field --arg id "$CONTRACT_NODE" '
      [.nodes[] | select(.id==$id) | . as $c | ($c.stubs // []) as $s
       | $c.footprint.paths[] | select(. as $p | ($s | index($p)) == null)] | .[0] // empty')
  fi
  if [ -z "$CONTRACT_FILE" ]; then
    stop "Check 480 stopped: the contract node $CONTRACT_NODE names no file of its own, so there is no contract file for the follow-up to be told about. The read refuses such a graph, so this is a plan to fix"
  fi
  fact plan PASS "read by the CLI: the pass broke one edge — $DEP_NODE waited on $WAITED_ON — with contract node $CONTRACT_NODE, integration node $INTEGRATION_NODE, contract file $CONTRACT_FILE and the dependent's own paths $(tr '\n' ' ' <"$SCRATCH/dep-paths")"
}

# read_dependent: the dependent's pull request, its branch and the head it is on
# before the follow-up. The row is the CLI's reading of the runs bucket, and the
# head it names is what the branch's own diff is taken from afterwards.
read_dependent() {
  if [ -z "$PR" ]; then
    [ -n "$DEP_RUN" ] || stop "Check 480 stopped: the plan's dependent node $DEP_NODE names no run and no --pr was given. The pass proposes its runs and never launches one, so launch the dependent first — the plan records the run on the node once it is launched — or pass --pr N"
    if ! fugaro runs ls --repo "$REPO" --since 36h --json >"$SCRATCH/ls.json" 2>"$SCRATCH/ls.err"; then
      stop "Check 480 stopped: 'fugaro runs ls' could not read the runs — $(tr '\n' ' ' <"$SCRATCH/ls.err")"
    fi
    PR=$(jq -r --arg run "$DEP_RUN" '[.runs[] | select(.run_id==$run)][0].pr // empty' "$SCRATCH/ls.json")
    [ -n "$PR" ] || stop "Check 480 stopped: no run of $REPO started in the last 36 hours has the id the plan's node names ($DEP_RUN), so its pull request is unknown. Launch the dependent again, or pass --pr N"
  fi
  # --since 0 with --pr reads the pull request's own runs whatever their age: the
  # window a plain listing applies is about the bucket, and the dependent's first
  # run may be older than it.
  if ! fugaro runs ls --repo "$REPO" --pr "$PR" --since 0 --json >"$SCRATCH/pr.json" 2>"$SCRATCH/pr.err"; then
    stop "Check 480 stopped: 'fugaro runs ls --pr $PR' could not read the pull request's runs — $(tr '\n' ' ' <"$SCRATCH/pr.err")"
  fi
  # The empty initial value is what makes the fallback below reachable: a plan
  # whose dependent node carries no `run` leaves this unset, and `set -u` would
  # end the check on that line — which is exactly the plan-plus-`--pr` reading
  # the stop message below tells the owner to take.
  local row=""
  if [ -n "$DEP_RUN" ]; then
    row=$(jq -c --arg run "$DEP_RUN" '[.runs[] | select(.run_id==$run)][0] // empty' "$SCRATCH/pr.json")
  fi
  [ -n "$row" ] || row=$(jq -c '.runs[0] // empty' "$SCRATCH/pr.json")
  [ -n "$row" ] || stop "Check 480 stopped: PR $PR has no run of $REPO in the runs bucket (or its runs are older than the listing reads), so the branch the follow-up continues is unknown"
  BRANCH=$(printf '%s' "$row" | jq -r '.branch // empty')
  BEFORE_HEAD=$(printf '%s' "$row" | jq -r '.pushed_head // empty')
  [ -n "$BRANCH" ] || stop "Check 480 stopped: PR $PR's run records no branch, so there is nothing to read back afterwards"
  [ -n "$BEFORE_HEAD" ] || stop "Check 480 stopped: no run on PR $PR has pushed (its row carries no pushed_head), so the follow-up has no branch state to continue"
  if live=$(printf '%s' "$row" | jq -r '.status'); then
    case "$live" in
    launching | pending | running) stop "Check 480 stopped: a run on PR $PR is still $live, and a follow-up is refused while one is in flight. Wait for it (fugaro runs ls --pr $PR --repo $REPO), then run this check again" ;;
    esac
  fi
  if [ -z "$BASE" ]; then
    BASE=$(printf '%s' "$row" | jq -r '.base_branch // empty')
    BASE=${BASE:-main}
  fi
  say "pull request: $PR on branch $BRANCH, head before the follow-up $BEFORE_HEAD, base $BASE"
  fact dependent PASS "PR $PR of $REPO is on branch $BRANCH, at $BEFORE_HEAD before the follow-up; the run it continues is $(printf '%s' "$row" | jq -r '.run_id') and its base branch is $BASE"
}

# read_amendment: the amendment is the check's premise and it is yours. What is
# measured here is that it exists: the base branch's copy of the contract file
# differs from the branch's, so the follow-up has something to take. With nothing
# amended the check stops before it spends a run.
#
# Both heads are fetched, and the branch's copy is read at the fetched head rather
# than at the commit the run row names: a commit this checkout never fetched makes
# `git show` fail, which reads as "the branch has no copy of it" — so the guard
# would pass a base the contract was never amended on and spend a run measuring
# nothing. The remote's head is compared with the row's for the same reason: they
# are the same commit or the branch has moved since the run, and a diff taken
# across the two would answer about a branch state that was never the premise.
read_amendment() {
  local branch_head
  AMENDED_HEAD=$(fetch_ref "$BASE") \
    || stop "Check 480 stopped: branch '$BASE' could not be fetched from origin ($(tr '\n' ' ' <"$SCRATCH/fetch.err"))"
  if ! git -C "$ROOT" show "$AMENDED_HEAD:$CONTRACT_FILE" >"$SCRATCH/contract.base" 2>/dev/null; then
    stop "Check 480 stopped: the contract file $CONTRACT_FILE is not on $BASE ($AMENDED_HEAD), so there is no amended contract for the follow-up to conform to. Amend it on $BASE and push, then run this check again"
  fi
  branch_head=$(fetch_ref "$BRANCH") \
    || stop "Check 480 stopped: branch '$BRANCH' could not be fetched from origin ($(tr '\n' ' ' <"$SCRATCH/fetch.err"))"
  if [ "$branch_head" != "$BEFORE_HEAD" ]; then
    stop "Check 480 stopped: $BRANCH is at $branch_head on origin and the run the plan names pushed $BEFORE_HEAD — the branch has moved since that run, so what the follow-up would continue is not the branch this check measured. Re-run the check"
  fi
  local before_note="the branch had no copy of it"
  if git -C "$ROOT" show "$branch_head:$CONTRACT_FILE" >"$SCRATCH/contract.before" 2>/dev/null; then
    if cmp -s "$SCRATCH/contract.base" "$SCRATCH/contract.before"; then
      fact amendment FAIL "the contract file $CONTRACT_FILE is the same on $BASE ($AMENDED_HEAD) and on the branch ($BRANCH at $branch_head, both fetched): there is nothing amended for the dependent to meet, so this check has nothing to measure and sends no run. Amend the contract on $BASE and push it first"
      stop "the contract is not amended on $BASE. This check measures a launched dependent meeting an amendment, and with nothing amended there is nothing to meet"
    fi
    before_note="the branch's copy at $branch_head differs"
  fi
  fact amendment PASS "$CONTRACT_FILE differs between $BASE ($AMENDED_HEAD) and the branch ($before_note), so there is an amendment to take; this check committed and pushed none of it"
}

# write_instructions: the run's instructions, built from the contract file's path
# alone. The contract's own words are never pasted into them — instructions are
# trusted text, so plan text in them is text the runner acts on as its own — and
# the row below measures that on the text this check is about to send.
write_instructions() {
  INSTRUCTIONS="$SCRATCH/instructions.txt"
  # Built from the contract file's path and the base branch alone, one printf per
  # line, so nothing of the contract's own text can reach the instructions by
  # construction — a heredoc carrying the file's words is exactly what D1812
  # forbids, and a name substituted through a sed script is a name a repository
  # could have made into an expression.
  {
    printf 'The contract this branch was written against changed on %s.\n\n' "$BASE"
    printf 'Do this, in this order:\n'
    printf "1. Merge origin/%s into this branch and resolve the conflicts in this branch's own files.\n" "$BASE"
    printf "2. Conform to the contract in %s as it is on origin/%s: read that file there and change this branch's code to match it. Do not edit the contract file; if it looks wrong, say so in your report instead of changing it.\n" "$CONTRACT_FILE" "$BASE"
    printf '3. Run the checks that cover the changed code, and report what you ran and what it printed.\n\n'
    printf 'Acceptance: this branch has merged origin/%s, the code that uses the contract matches the contract as it is there, and the checks covering it pass.\n' "$BASE"
  } >"$INSTRUCTIONS"
  local pasted=""
  while IFS= read -r line; do
    [ "${#line}" -ge 8 ] || continue
    if grep -qF -- "$line" "$INSTRUCTIONS"; then
      pasted=$line
      break
    fi
  done <"$SCRATCH/contract.base"
  if [ -n "$pasted" ]; then
    fact instructions FAIL "a line of the contract's own body is in the instructions this check would send ('$pasted'): instructions are trusted, so a contract's words in them are plan text the runner acts on as its own (D1812)"
    stop "the instructions this check built carry a line of the contract's text, which is the one thing they must not. The text is in ${INSTRUCTIONS}, still on disk"
  fi
  say "the instructions, which name $CONTRACT_FILE and paste none of it:"
  while IFS= read -r line; do say "  $line"; done <"$INSTRUCTIONS"
  fact instructions PASS "the instructions name $CONTRACT_FILE as it is on origin/$BASE and ask for the merge and the conformance, and carry no line of the contract's body: the file is named, its text is not pasted"
}

# launch_followup: the run the pass would propose, sent because you typed the
# command. The pass itself proposes and never launches (F11, D1820); this check
# is your own tool, and the run it sends is the one the pass would have asked you
# to send.
launch_followup() {
  RUN_ID="chk480-$(date -u +%Y%m%d%H%M%S)"
  owner "about to launch ONE follow-up run on PR $PR of $REPO (run id $RUN_ID): the pass proposes its runs and never launches one — this goes because you typed the command"
  if ! fugaro run --pr "$PR" --repo "$REPO" --run-id "$RUN_ID" --json --task-file - <"$INSTRUCTIONS" >"$SCRATCH/launch.json" 2>"$SCRATCH/launch.err"; then
    fact launch FAIL "fugaro run --pr $PR was refused: $(tr '\n' ' ' <"$SCRATCH/launch.err")"
    stop "the launch was refused, and this check does not work around a refusal. The text it would have sent is in ${INSTRUCTIONS}"
  fi
  FOLLOWUP=$(jq -r '.run // empty' "$SCRATCH/launch.json")
  [ -n "$FOLLOWUP" ] || stop "'fugaro run --pr $PR --json' printed no run id, so the follow-up cannot be followed: $(tr '\n' ' ' <"$SCRATCH/launch.json")"
  fact launch PASS "launched $FOLLOWUP on PR $PR of $REPO (status $(jq -r '.status // "?"' "$SCRATCH/launch.json")), continuing $(jq -r '.previous_run // "?"' "$SCRATCH/launch.json"): one run, spent by this check"
}

# wait_followup: the row the follow-up settles on. A timeout is an INCOMPLETE and
# not a finding: the run may still be going, and the check cannot tell what a
# branch looks like before the run pushed it.
wait_followup() {
  local deadline=$(( $(date +%s) + RUN_SECONDS ))
  local row status terminal
  while :; do
    if ! fugaro runs ls --repo "$REPO" --pr "$PR" --since 0 --json >"$SCRATCH/pr.json" 2>"$SCRATCH/pr.err"; then
      stop "Check 480 stopped: 'fugaro runs ls --pr $PR' could not read the pull request's runs — $(tr '\n' ' ' <"$SCRATCH/pr.err")"
    fi
    row=$(jq -c --arg run "$FOLLOWUP" '[.runs[] | select(.run_id==$run)][0] // empty' "$SCRATCH/pr.json")
    if [ -n "$row" ]; then
      terminal=$(printf '%s' "$row" | jq -r '.terminal')
      if [ "$terminal" = true ]; then
        status=$(printf '%s' "$row" | jq -r '.status')
        AFTER_HEAD=$(printf '%s' "$row" | jq -r '.pushed_head // empty')
        if [ "$status" = succeeded ] && [ -n "$AFTER_HEAD" ] && [ "$AFTER_HEAD" != "$BEFORE_HEAD" ]; then
          fact run PASS "the follow-up $FOLLOWUP ended succeeded and pushed $AFTER_HEAD, moving the branch from $BEFORE_HEAD"
        else
          fact run FAIL "the follow-up $FOLLOWUP ended '$status' at $AFTER_HEAD (the branch was at $BEFORE_HEAD): U4 needs a follow-up that merges the base and conforms, and this one did not. Read 'fugaro diagnose $FOLLOWUP' and its report on the PR before re-running"
          stop "the follow-up ended '$status' instead of pushing a conformed branch"
        fi
        return 0
      fi
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fact run FAIL "the follow-up $FOLLOWUP was still going after ${RUN_SECONDS}s and is not measured: watch it with 'fugaro runs ls --pr $PR --repo $REPO', and read the branch back by hand when it ends"
      stop "the follow-up did not end within ${RUN_SECONDS}s, so nothing about the branch is measured"
    fi
    owner "waiting for $FOLLOWUP (status ${status:-launching}); re-reading in ${POLL_SECONDS}s"
    sleep "$POLL_SECONDS"
  done
}

# path_matches PATTERN PATH: whether a changed path is one a footprint predicts.
# A footprint is a doublestar pattern and this is its literal prefix rather than
# the whole semantics: what the row measures is that the run changed the files the
# task predicted, and a path this cannot place is reported for you to read.
path_matches() {
  local prefix=${1%%[*?[]*}
  [ -z "$prefix" ] && return 0
  case "$2" in
  "$prefix"*) return 0 ;;
  esac
  return 1
}

# measure_branch: the branch, read back from the repository's remote — the one
# reading no report can stand in for, because what U4 asks is what the branch
# holds and not what the run says it did.
measure_branch() {
  local head
  head=$(fetch_ref "$BRANCH") \
    || stop "Check 480 stopped: branch '$BRANCH' could not be fetched from origin after the follow-up ($(tr '\n' ' ' <"$SCRATCH/fetch.err"))"
  if [ "$head" != "$AFTER_HEAD" ]; then
    stop "Check 480 stopped: the remote's $BRANCH is at $head and the follow-up's row recorded $AFTER_HEAD — someone pushed after the run, so what the branch holds is not what the run left"
  fi
  if git -C "$ROOT" merge-base --is-ancestor "$AMENDED_HEAD" "$head"; then
    fact base-merged PASS "the amended base $AMENDED_HEAD is an ancestor of $BRANCH at $head: the run merged the base it was told to take"
  else
    fact base-merged FAIL "$AMENDED_HEAD (the amended $BASE) is not an ancestor of $BRANCH at $head: the follow-up did not merge the base, so the dependent still builds against the contract it was written for"
  fi
  if git -C "$ROOT" show "$head:$CONTRACT_FILE" >"$SCRATCH/contract.after" 2>/dev/null; then
    if cmp -s "$SCRATCH/contract.base" "$SCRATCH/contract.after"; then
      fact conformed PASS "$CONTRACT_FILE on $BRANCH at $head is byte-identical to its copy on $AMENDED_HEAD: the run took the amendment instead of editing the contract to suit itself"
    else
      fact conformed FAIL "$CONTRACT_FILE on $BRANCH at $head differs from its amended copy on $BASE ($AMENDED_HEAD): either the run edited the contract, which the pass's own rule says it raises a change request for instead, or it did not take the amendment. Diff it by hand: git diff $AMENDED_HEAD..$head -- $CONTRACT_FILE"
    fi
  else
    fact conformed FAIL "$CONTRACT_FILE is not on $BRANCH at $head, though it is on $BASE at $AMENDED_HEAD: the run did not take the amended contract"
  fi

  # The run's own commits, never the whole `$BEFORE_HEAD..$head` range: the run
  # merges the base, and the amendment's own change to the contract file arrives
  # with that merge. Counting the range would read the base's change as the run
  # editing the contract, which is the finding this row exists to make honestly.
  local changed own="" outside="" contract_hit=""
  if git -C "$ROOT" cat-file -e "$BEFORE_HEAD^{commit}" 2>/dev/null; then
    changed=$(git -C "$ROOT" rev-list --no-merges "$head" --not "$BEFORE_HEAD" "$AMENDED_HEAD" 2>/dev/null |
      while IFS= read -r commit; do
        [ -n "$commit" ] || continue
        git -C "$ROOT" diff-tree --no-commit-id --name-only -r "$commit"
      done | sort -u)
  else
    # A run that rewound the branch leaves the head this check read before it
    # unreachable, and a diff against a commit that is gone is not an empty
    # diff: saying "it changed nothing" there would be a finding the record does
    # not support.
    fact own-code FAIL "the branch no longer holds $BEFORE_HEAD, the head it was on before the follow-up (the run rewound or force-pushed it), so the run's own diff cannot be read: compare the branch with the plan by hand"
    MANUAL_LINE="read the branch $BRANCH back by hand: the follow-up rewound it, so the diff this check takes could not be taken"
    return 0
  fi
  if [ -z "$changed" ]; then
    fact own-code FAIL "the follow-up pushed $head and left no commit of its own above $BEFORE_HEAD and the base it merged: the run changed nothing, so it did not conform to the amendment by changing its code"
  else
    while IFS= read -r path; do
      [ -n "$path" ] || continue
      if grep -qxF -- "$path" "$SCRATCH/contract-paths"; then
        contract_hit="$path"
        continue
      fi
      local hit=""
      while IFS= read -r pattern; do
        [ -n "$pattern" ] || continue
        if path_matches "$pattern" "$path"; then
          hit=$pattern
          break
        fi
      done <"$SCRATCH/dep-paths"
      if [ -n "$hit" ]; then
        own="$own $path"
      else
        outside="$outside $path"
      fi
    done <<<"$changed"
    if [ -n "$contract_hit" ]; then
      fact own-code FAIL "the run's own commits touch $contract_hit, a file of the contract's own: a task that has to change a contract file raises the change request instead of editing it (F10), so the follow-up widened its scope past the task it continued"
    elif [ -n "$own" ]; then
      fact own-code PASS "the run's own commits touch the dependent's own files:$own, and no file of the contract's"
    else
      fact own-code FAIL "the run's own commits touch none of the dependent's own paths and none of the contract's: it changed files the task did not predict, so nothing here says it conformed"
    fi
    if [ -n "$outside" ]; then
      fact outside INFO "the diff also touches paths no footprint of this task predicts, for you to read:$outside"
    fi
  fi
  MANUAL_LINE="nothing of this check is left to undo: the amendment on $BASE is yours to keep or revert, and the follow-up's commits on $BRANCH are the run's. If you keep them, $INTEGRATION_NODE is the node that deletes the contract's stubs"
}

# ----------------------------------------------------------------------- main
USAGE="check-480.sh [--dry-run] [--plan <plan>] [--pr N] [--base <branch>] [--contract <path>] [--poll SECONDS] [--run-seconds N]"

usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}

PLAN=""
PR=""
BASE=""
CONTRACT_FILE=""
POLL_SECONDS=${CHECK480_POLL_SECONDS:-60}
RUN_SECONDS=${CHECK480_RUN_SECONDS:-3600}
FOLLOWUP=""
BRANCH=""

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --plan)
      [ $# -ge 2 ] || die "Check 480: --plan needs a plan"
      PLAN=$2
      shift 2
      ;;
    --pr)
      [ $# -ge 2 ] || die "Check 480: --pr needs a pull request number"
      case "$2" in '' | *[!0-9]*) die "Check 480: --pr needs a number, not '$2'" ;; esac
      PR=$2
      shift 2
      ;;
    --base)
      [ $# -ge 2 ] || die "Check 480: --base needs a branch"
      BASE=$2
      shift 2
      ;;
    --contract)
      [ $# -ge 2 ] || die "Check 480: --contract needs a path"
      CONTRACT_FILE=$2
      shift 2
      ;;
    --poll)
      [ $# -ge 2 ] || die "Check 480: --poll needs seconds"
      POLL_SECONDS=$2
      shift 2
      ;;
    --run-seconds)
      [ $# -ge 2 ] || die "Check 480: --run-seconds needs seconds"
      RUN_SECONDS=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      HELP=1
      shift
      ;;
    *) die "Check 480: unknown argument '$1' (usage: $USAGE)" ;;
    esac
  done
  case "$POLL_SECONDS$RUN_SECONDS" in
  *[!0-9]*) die "Check 480: --poll and --run-seconds take whole seconds" ;;
  esac
  [ "$POLL_SECONDS" -ge 1 ] || die "Check 480: --poll has to be at least a second"
}

# plan_only: what --dry-run prints — the commands a real run would run, and the
# commands it would not — with nothing called and nothing spent.
plan_only() {
  local args=(--json)
  [ -n "$PLAN" ] && args=(--plan "$PLAN" "${args[@]}")
  printf 'CHECK 480 PLAN (--dry-run: nothing below is run, and no run is spent)\n' >&3
  printf '  from %s, origin %s\n' "$ROOT" "$ORIGIN" >&3
  printf '  read the graph:      fugaro plan graph %s\n' "${args[*]}" >&3
  printf '  read the runs:       fugaro runs ls --repo %s --pr <N> --since 0 --json\n' "$REPO" >&3
  printf '  fetch the branches:  git -C %s fetch origin refs/heads/<base> refs/heads/<branch>\n' "$ROOT" >&3
  printf '  send the follow-up:  fugaro run --pr <N> --repo %s --run-id chk480-<stamp> --json --task-file - (instructions naming the contract file, pasting none of it)\n' "$REPO" >&3
  printf '  wait:                up to %ss, re-reading every %ss\n' "$RUN_SECONDS" "$POLL_SECONDS" >&3
  printf '  then it asserts:     the amended base is an ancestor of the branch; the branch'"'"'s contract file is the base'"'"'s byte for byte; the commits the run itself made touch the dependent'"'"'s own files and no file of the contract'"'"'s\n' >&3
  # Every key the real run records is named here as unmeasured, so the block a
  # dry run prints reads as a plan and not as a run that stopped at its first
  # step: `FAIL — not reached` is what an interrupted run's block says.
  local key
  for key in plan dependent amendment instructions launch run base-merged conformed own-code; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
}

main() {
  parse_args "$@"
  need_tools
  scratch_up
  trap on_error ERR
  if [ -n "$HELP" ]; then
    usage
    REACHED_END=yes
    return 0
  fi
  guard_xtrace
  guard_checkout
  guard_repository
  if [ -n "$DRY_RUN" ]; then
    plan_only
    REACHED_END=yes
    return 0
  fi
  read_plan
  read_dependent
  read_amendment
  write_instructions
  launch_followup
  wait_followup
  measure_branch
  REACHED_END=yes
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
