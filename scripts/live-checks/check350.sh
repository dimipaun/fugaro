#!/usr/bin/env bash
# Check 350 — the failure classes an owner can see: the class a run carries, the
# grouping, the digest, and the record nothing writes back — as ONE script YOU
# run.
#
# FC1-FC8 ship the vocabulary, the one writer of a record's ending, the reader
# that classes a record written before the field existed, `runs ls --group-by
# failure_class` and the digest's Failures table. What none of their hermetic
# tests can hold is the claim the design exists to make true: that the OWNER can
# see why the runs failed — a row's class, the window's groups, the week's table
# — and that the reader writes nothing back into the records it classes. That
# claim is this check, and every step of it runs on the sandbox installation and
# the sandbox repository only, never the installation named `belong`.
#
# The plan's §6 is six steps, and this script automates the first four and step
# 5's read:
#
#   1. one run that fails for a cause the classifier can name on purpose — a
#      reference carrying a `fugaro.yaml` with an unknown key, which the runner
#      refuses at bootstrap as `config_invalid` and which calls no model — then
#      `fugaro runs ls --json` and `fugaro diagnose`: the row and the diagnosis
#      carry the same class, and it is the class the cause deserves;
#   2. `fugaro runs ls --group-by failure_class`: the groups' runs add up to the
#      window's failed rows, `share` follows the runs and not the spend, and the
#      totals are the plain listing's own;
#   3. `fugaro runs digest --period week`: the Failures table is there, the
#      summary block carries `failures`, and an old record of the same week — one
#      whose own bytes hold no `failure_class` — lands in the class its `reason`
#      maps to, so the week is not split into before and after;
#   4. the read-only step: that old record is read twice through `runs ls
#      --json` and once through `diagnose`, and its object in the bucket is
#      hashed before the first of those reads and again after the last: the
#      bytes, and the bucket's own line for the object, are unchanged. §5's claim
#      is that nothing is written back, and this is what falsifies it if a later
#      change does. It is the one step a code review cannot settle;
#   5. the refutation step (read only with `--step5-run`): a run whose agent call
#      the account's own limit refused carries `stage_error_unexplained` and no
#      class of its own — which is O6 unanswered, and this read is what tells the
#      owner what the report looks like meanwhile. Making that run is YOURS: it
#      needs a job image and a refused call, and this script never fakes it;
#   6. the HARD RULE, on a real installation: `brew upgrade` and then `fugaro
#      update` ALONE, at the previous version, with `fugaro doctor` clean
#      afterwards. This script prints that prompt and measures nothing of it: a
#      step needed there that the plan did not name is a defect in the release,
#      not in the check.
#
# Nothing in this check is a criterion of the other's: steps 5 and 6 are the
# owner's own runs, so their rows are the prompt and an INFO, and the exit status
# is the verdict of steps 1 to 4 alone.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 350".
#
# Usage:      bash scripts/live-checks/check350.sh --repo <a sandbox repository checkout> \
#               --project <the installation's project> --bucket <its runs bucket> \
#               [--gcp-project <id>] [--since <Nd>] [--step5-run <run id>] [--dry-run]
#             --repo is a checkout of the SANDBOX repository: every `fugaro`
#             command runs in it, and the throwaway reference this check's one
#             run is cut from is made in it (by plumbing, so your work tree is
#             never switched and your own branch never moves). --project is the
#             installation's Fugaro project, carried by every `fugaro` call, and
#             --bucket the runs bucket of that project, where the records of
#             steps 1, 3 and 4 are read. --gcp-project is the GCP project id the
#             bucket lives in (default: the same value as --project): `fugaro
#             setup` names the two the same, and the gcloud reads are addressed
#             with this one. --since is the listing window of steps 1 and 2
#             (default: the one that reaches back past the week the digest
#             reports, with room for a run that was minted before that week and
#             finished inside it). --step5-run is a run YOU made for step 5,
#             whose class is then read here; without it step 5 is the prompt and
#             an INFO row. `--dry-run` prints the guards and every command a real
#             run would make, and runs nothing at all: no gcloud call, no
#             `fugaro` call, no object read and no verdict.
# Needs:      bash, mktemp, sed, grep, awk, python3, git, gcloud and fugaro on
#             PATH. Nothing is installed and nothing is fetched; if one of those
#             is missing the script stops with one line naming it.
# Where:      a SANDBOX installation and a SANDBOX repository only, and NEVER the
#             installation named `belong`: that name is refused in the project,
#             in the GCP project, in the bucket and in the checkout's own
#             `fugaro.yaml` before anything is created. A coding agent's session
#             is refused too (a check whose writes belong to you is not one an
#             agent runs), and the checkout has to be a git work tree whose
#             `origin` answers.
# Creates:    ONE throwaway branch on the sandbox repository's origin
#             (`chk350-<hex>`, cut at your checkout's HEAD with ONE file
#             replaced: `fugaro.yaml` plus an unknown key), ONE run of the
#             sandbox repository cut from it (refused at bootstrap, so no model
#             is called and nothing is billed), and — for that run to have a
#             record to read — its own run objects under the repository's slug.
#             The branch is deleted from the origin and from the checkout by the
#             Restore, which runs from the exit trap too; the run's own record is
#             LEFT in the bucket, because it is the evidence step 1 read, it
#             costs nothing, and the bucket's lifecycle ages it out.
# Costs:      nothing billable and no model call: the run is refused at
#             bootstrap, before its first stage. It does start one Cloud Run job
#             execution, and nothing it launches outlives it — a run still going
#             after CHECK350_RUN_TIMEOUT_SECONDS is cancelled with `fugaro cancel
#             --now`, from the step that gives up on it and from the exit trap.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 350 RESULT.
#             Exit 0 when every criterion of steps 1 to 4 held — and step 5's
#             too, when you passed --step5-run and let the check read that run —
#             1 when one of them FAILED, or when they all held and the Restore
#             could not confirm that the branch is gone, which the block's
#             `restore` row names, and 2 when the run could not tell (a guard
#             stopped it, the
#             window holds no record old enough for steps 3 and 4, the run never
#             ended, a read did not answer, a tool is missing, an internal error
#             ended it). `2` is not an answer about the classes, and the block's
#             `aborted` row says so.
#
# No credential is handled anywhere in it as a value: every read is made with
# your own identity, this check writes no secret and mints no token, and the only
# text it prints is what the tools printed.
#
# CHECK350_RUN_TIMEOUT_SECONDS, CHECK350_POLL_SECONDS, CHECK350_CALL_SECONDS,
# CHECK350_LAUNCH_SECONDS and CHECK350_CANDIDATES exist for this script's own
# test: they shorten the wait for the one run to end, bound a single call, and
# set how many old records the read-only step looks at before it gives up on the
# window. None of them can point the check at another repository, project or
# bucket.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all,
# and the block then reports the abort without the line that caused it.
set -E

# No xtrace, whatever the shell it was started from had: a trace of this run
# prints every command it makes and the run's own output with it. The three
# sources are read on their own (BASHOPTS carries names like extquote, and a
# single pattern over the concatenation would match that 'x' and refuse every
# run), and guard_xtrace below refuses a shell that arrived tracing, whose flag
# every child would inherit.
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
# Set to yes by step_verdict once the criteria have been judged, so the exit trap
# can tell a finished run from one that ended on an error of its own.
REACHED_END=no
HELPED=no
DRY_RUN=""
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory, so a stop still gives you something to
# paste back.
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
# and the block would then read as an answer about the classes for a run that
# measured nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the failure classes"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 350 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 350 needs bash"
  need mktemp
  need sed "the block's rows are read back out of this run's own fact file with it"
  need grep "the week's report is searched for its Failures table with it"
  need awk "one run's own row is picked out of a listing by its run id with it"
  need python3 "every document this check reads — the launch, a listing, the grouping, the digest and a record — is parsed by one python3 file"
  need git "the throwaway reference the run is cut from is made, pushed and deleted with it"
  need gcloud "the records of the run and of step 4 are read, listed and hashed with it"
  need fugaro "the run, the listing, the grouping, the digest and the diagnoses are its own commands"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk350-mine"
SCRATCH_REMOVED=no
PRINTED=""
FACTS_CAPTURED=""
# This run's own names, and what the steps fill in.
BRANCH=""
BRANCH_PUSHED=no
BRANCH_COMMIT=""
SLUG=""
RUN_ID=""
RUN_REF=""
REPO_NAME=""
PROJECT_SEEN=""
SETTLED=no
CANCEL_DONE=no
CANCEL_OK=""
# The old record steps 3 and 4 are about, and the two readings of its object.
TARGET_KEY=""
TARGET_RUN=""
TARGET_CLASS=""
TARGET_BYTES_CLASS=""
TARGET_STAMP=""
T0_HASH=""
T0_LINE=""
T1_HASH=""
T1_LINE=""
# Whether the poll of step 1 ever read the run's own record at all: a run that is
# still going and a run whose record never appeared leave the same empty slot
# behind, and they are two different readings of the sandbox.
POLL_READ=no
# The window the listing was read with, and the week the digest reports: both are
# printed, because a reader who cannot see the window cannot tell an empty
# reading from a narrow one.
WEEK_FROM=""
WEEK_TO=""
WEEK_LAST=""
SINCE_USED=""
SINCE_STAMP=""
WEEK_TO_STAMP=""

# rand_hex: four hex digits from bash's own $RANDOM, so this run's own names are
# its own without another tool.
rand_hex() { printf '%04x' "$RANDOM"; }

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk350.XXXXXXXX") || die "Check 350 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 350 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  # A name of this run's own: the branch is the one thing a stopped run could
  # leave on the origin, and a unique name is never one an earlier run — or your
  # own work — already holds.
  BRANCH="chk350-$(rand_hex)$(rand_hex)"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
  say "this run's own reference: $BRANCH (cut at your checkout's HEAD with ONE file replaced, and deleted again by the Restore)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what it
# managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  cancel_run "the check is ending"
  restore_run
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
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before the
  # block is printed.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# --------------------------------------------------------------- the Restore
# The Restore deletes the one reference this check made and reads it back. It
# runs from the exit trap, so it has to tolerate a run that stopped anywhere, and
# every command in it is guarded: a delete that fails must not end the shell
# before the block is printed. The run's own record is deliberately left — the
# row says so, and so does this file's header: it is what step 1 read.
restore_run() {
  if [ -n "$DRY_RUN" ]; then
    fact restore PASS "dry run: nothing was created, nothing was deleted and no origin was read"
    return 0
  fi
  if [ -z "$BRANCH" ]; then
    fact restore PASS "nothing of this check's was created: it stopped before it made a scratch directory, so there is no reference to delete"
    return 0
  fi
  local bad="" unknown="" out=""
  if [ "$BRANCH_PUSHED" = yes ]; then
    say "Restore: deleting the reference $BRANCH from the origin"
    # --no-verify: a pre-push hook of the sandbox repository is not this check's
    # business, and a hook that refused the delete would leave the reference on
    # the origin with this run's own file in it.
    if ! out=$(git -C "$REPO" push --no-verify origin --delete "$BRANCH" 2>&1); then
      bad="$bad the branch $BRANCH on the origin (git push --delete did not answer: $(printf '%s' "$out" | head -1))"
    fi
  fi
  # The local ref goes whether or not the push did: it is this check's own either
  # way, and a ref left behind in your checkout is a stray branch you would find
  # later and not know about.
  git -C "$REPO" update-ref -d "refs/heads/$BRANCH" >/dev/null 2>&1 || true
  # Both deletions are READ BACK rather than assumed: the local ref by resolving
  # it, and the origin by listing its heads. A listing that does not answer is
  # neither "gone" nor "there", and saying it was deleted on a listing nobody
  # read is the claim this row exists to make false.
  if git -C "$REPO" rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1; then
    bad="$bad the local branch $BRANCH (it still resolves in your checkout: git branch -D $BRANCH)"
  fi
  if out=$(git -C "$REPO" ls-remote --heads origin "$BRANCH" 2>&1); then
    if [ -n "$out" ]; then
      bad="$bad the branch $BRANCH on the origin (the origin still lists it: git push origin --delete $BRANCH)"
    fi
  else
    unknown="$unknown the origin's heads (git ls-remote did not answer, so the branch $BRANCH could not be read back: $(printf '%s' "$out" | head -1))"
  fi
  if [ -n "$bad" ] || [ -n "$unknown" ]; then
    fact restore FAIL "this check's Restore could not confirm that it deleted the reference it made.${bad:+ Still there:$bad.}${unknown:+ Not readable, so not confirmed:$unknown.} Delete it by hand before running any other check on this repository: the branch is $BRANCH, and it holds ONE file — a fugaro.yaml with an unknown key — cut at your checkout's HEAD. The run this check launched (${RUN_ID:-none}) is left in the runs bucket on purpose"
    return 0
  fi
  local left
  if [ -n "$RUN_ID" ]; then
    left=", and the run this check launched ($RUN_ID) is left in the runs bucket: it is the record step 1 read, it holds no secret, and the bucket's lifecycle ages it out"
  else
    left=", and nothing of this check's was left anywhere: no run was launched"
  fi
  fact restore PASS "the reference this check made ($BRANCH, pushed=$BRANCH_PUSHED) is gone: the local branch no longer resolves and the origin no longer lists it (both read back)$left"
}

# ------------------------------------------------------------------ the guards
# guard_xtrace: a shell that arrived tracing would print every read of this run,
# so the run is refused rather than made: the trace is already off (the top of
# this file), and what is left to catch is the shell that started with it, whose
# flag sits in SHELLOPTS/BASHOPTS and would be inherited by every child.
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print every command this check makes and the run's own output with it. Run it without -x"
    stop "the shell this check was started from is tracing (-x). Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run carries its output"
}

# guard_agent_session: the check is yours, and it writes to a sandbox
# repository's origin (one throwaway branch) and launches one run. A coding
# agent's session is refused by its own markers: every live check in this
# repository is owner-run, and a session that is refused cloud writes would have
# this one fail halfway instead.
guard_agent_session() {
  local name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then
      fact agent-session FAIL "$name is set, so this is a coding agent's session: Check 350 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
      stop "$name is set, so this is a coding agent's session: Check 350 writes a throwaway branch to a sandbox repository's origin and launches a run, and no agent runs any step of it"
    fi
  done
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: the installation named `belong` is the owner's own. This check
# pushes a branch and launches a run, so the name is refused in the project, in
# the GCP project, in the bucket and in the checkout's own configuration, before
# anything is written. It is a substring test on purpose: a name that merely
# contains it is refused too, and the row says which one matched.
guard_belong() {
  local name
  for name in "$P" "$GCP" "$B"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation: Check 350 pushes a throwaway branch and launches a run, and it is a SANDBOX check"
      stop "the name '$name' matches the installation 'belong', which this check must never touch"
      ;;
    esac
  done
  # The checkout's own `fugaro.yaml` is read as a FILE, before any call: the
  # project a run launched here lands in is the one that file names, and a
  # sandbox checkout pointed at the owner's own installation by a copied config
  # is exactly the accident the refusal is for. A checkout that names no project
  # is not a refusal — the CLI resolves it from the environment or from the only
  # project config, the way your own shell does.
  local named=""
  if [ -f "$REPO/fugaro.yaml" ]; then
    # The three characters a YAML scalar may carry around it, dropped by tr's
    # own escapes (\047 is a single quote) rather than by quoting them here.
    named=$(sed -n 's/^project:[[:space:]]*//p' "$REPO/fugaro.yaml" | head -1 | tr -d '"\047\r')
  fi
  case "$(printf '%s' "$named" | tr '[:upper:]' '[:lower:]')" in
  *belong*)
    fact belong FAIL "the checkout $REPO names the project '$named' in its fugaro.yaml, which is the owner's own installation: Check 350 is a SANDBOX check"
    stop "the checkout $REPO belongs to '$named', which is the owner's own installation"
    ;;
  esac
  fact belong PASS "none of the project ($P), the GCP project ($GCP), the bucket ($B) or the checkout's own project (${named:-unnamed}) is the owner's own installation (nothing read contains 'belong')"
}

# guard_repo: the checkout has to be a git work tree with an origin, and the
# branch this check makes has to be one that does not exist yet. This is checked
# before anything is written, because every write below is made in this
# directory: a path that is not a checkout would have the first object written
# and then fail.
guard_repo() {
  local why=""
  if [ ! -d "$REPO" ]; then
    why="--repo '$REPO' is not a directory"
  elif [ ! -f "$REPO/fugaro.yaml" ]; then
    why="--repo '$REPO' carries no fugaro.yaml, so it is not a Fugaro repository's checkout — the run this check launches is launched from one, and its own fugaro.yaml is the file the throwaway reference replaces"
  elif ! git -C "$REPO" rev-parse --show-toplevel >/dev/null 2>&1; then
    why="--repo '$REPO' is not a git work tree, and the reference the run is cut from is a commit in it"
  elif ! git -C "$REPO" remote get-url origin >/dev/null 2>&1; then
    why="--repo '$REPO' has no 'origin' remote, so the branch this check cuts could not be pushed anywhere the run can be started from"
  elif git -C "$REPO" rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1; then
    why="the branch $BRANCH already exists in $REPO, and this check never reuses a name it did not make"
  fi
  if [ -n "$why" ]; then
    fact repo FAIL "$why. Pass the checkout of a SANDBOX repository (the one the plan calls the sandbox), with an origin the run can be started from, and run the check again"
    stop "$why"
  fi
  local url
  url=$(git -C "$REPO" remote get-url origin 2>/dev/null | head -1)
  fact repo PASS "$REPO is a git work tree of a Fugaro repository with an origin ($url), and the reference this check makes ($BRANCH) does not exist yet: nothing of yours is switched, and your branch is never moved"
}

# Every command this check runs outside the launch gets its own wall clock, so
# one hung call cannot hold the check open for ever.
RUN_BOUND_SECONDS=${CHECK350_CALL_SECONDS:-120}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a cancel that
# had nothing to do, a record that is not there), and a trap inherited here would
# turn each of them into "an internal error" and a status of 2 before the step
# could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  # The directory is checked rather than trusted: a call site that forgot it
  # would hand the first word of the command to `cd`, run nothing at all, and
  # have every read below report "no record" — a broken check reading as an
  # empty bucket. This is a bug in this script and the run says so.
  if [ ! -d "$dir" ]; then
    die "an internal error: run_bounded was told to run from '$dir', which is not a directory. Nothing below was measured"
  fi
  BOUNDED_TIMED_OUT=no
  (trap - ERR; cd "$dir" || exit 1; exec "$@") >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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

# launch_bounded is run_bounded with the launch's own clock: starting a job
# execution takes a cloud call and a claim, which is slower than any read here
# and is not what the read bound is for.
LAUNCH_BOUND_SECONDS=${CHECK350_LAUNCH_SECONDS:-300}

# cancel_run WHY: stop the run this check started, once, and say whether it
# worked. It is what makes "launch nothing it cannot cancel" true rather than
# intended: the step that gives up waiting calls it, and so does the exit trap,
# so a Ctrl-C in the middle cancels the run too.
cancel_run() { # cancel_run WHY
  [ -n "$RUN_ID" ] || return 0
  [ "$SETTLED" = yes ] && return 0
  [ "$CANCEL_DONE" = yes ] && return 0
  CANCEL_DONE=yes
  say "cancelling $RUN_ID ($1): fugaro cancel $RUN_ID --now"
  run_bounded "$RUN_BOUND_SECONDS" cancel "$REPO" fugaro cancel "$RUN_ID" --now --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    CANCEL_OK=no
    fact cancelled FAIL "the run $RUN_ID was cancelled with 'fugaro cancel $RUN_ID --now' and the command did not answer (exit $BOUNDED_STATUS): the run may still be going. Read it with 'fugaro runs show $RUN_ID' and cancel it yourself. What the command printed is in $SCRATCH/cancel.out and $SCRATCH/cancel.err"
    return 0
  fi
  CANCEL_OK=yes
  fact cancelled PASS "the run $RUN_ID did not end on its own, so this check cancelled it with 'fugaro cancel $RUN_ID --now': nothing this check launched outlives it"
}

# ------------------------------------------------------------- the documents
# The reader is one python3 file (there is no jq here, and every document this
# check reads is JSON): one query per document, printing `key=value` lines or
# `\t`-separated rows, and the shell judges them, so the parsing and the verdict
# stay in one place each. A document a query cannot read is a stop or a FAILed
# row, never a set of zeroes.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def flat(value):
    return " ".join(str(value or "").split())


def yes(value):
    return "true" if value else "false"


def num(value):
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def totals(doc):
    tot = doc.get("totals") or {}
    print("runs=%s" % (tot.get("runs") or 0))
    print("model_usd=%s" % num(tot.get("model_usd")))
    print("model_notional_usd=%s" % num(tot.get("model_notional_usd")))
    print("compute_usd=%s" % num(tot.get("compute_usd")))
    print("total_usd=%s" % num(tot.get("total_usd")))
    print("compute_not_estimated=%s" % (tot.get("compute_not_estimated") or 0))


def table(rows):
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            flat(row.get("status")),
            flat(row.get("failure_class")),
            flat(row.get("created")),
            yes(row.get("terminal")),
            flat(row.get("run")),
            num((row.get("cost") or {}).get("total_usd")),
        ))


def listing(path):
    # `gcloud storage ls -l [--recursive]` prints "<size> <time> <url>" per
    # object and a TOTAL line after them. The size and the object's own time are
    # what the read-only step compares beside the bytes, so the whole line is
    # kept: a line whose shape this reader did not expect is reported by its own
    # text rather than dropped.
    out = []
    for line in open(path, "r", encoding="utf-8", errors="replace"):
        line = line.strip()
        if not line or line.startswith("TOTAL:"):
            continue
        url = ""
        for word in line.split():
            if word.startswith("gs://"):
                url = word
        if url:
            out.append((url, flat(line)))
    return out


what = sys.argv[1]
path = sys.argv[2]

if what == "launch":
    doc = load(path)
    print("project=%s" % flat(doc.get("project")))
    print("run_id=%s" % flat(doc.get("run_id")))
    print("run=%s" % flat(doc.get("run")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
elif what == "rows":
    doc = load(path)
    table(doc.get("runs") or [])
    totals(doc)
elif what == "grouped":
    doc = load(path)
    table(doc.get("runs") or [])
    totals(doc)
    print("groups=%d" % len(doc.get("groups") or []))
    for group in doc.get("groups") or []:
        print("group\t%s\t%s\t%s\t%s" % (
            flat(group.get("class")),
            group.get("runs") or 0,
            group.get("share") if group.get("share") is not None else "",
            num(group.get("spend_usd")),
        ))
elif what == "digest":
    doc = load(path)
    summary = doc.get("summary") or {}
    failures = summary.get("failures") or {}
    print("repo=%s" % flat(doc.get("repo")))
    print("period=%s" % flat(doc.get("period")))
    print("period_start=%s" % flat(summary.get("start")))
    print("period_end=%s" % flat(summary.get("end")))
    print("period_runs=%s" % (summary.get("runs") or 0))
    print("failures_present=%s" % yes("failures" in summary))
    print("failures_total=%d" % sum(int(v or 0) for v in failures.values()))
    for name in sorted(failures):
        print("failure\t%s\t%s" % (name, failures[name]))
elif what == "diagnose":
    doc = load(path)
    row = doc.get("row") or {}
    print("failure_class=%s" % flat(doc.get("failure_class")))
    print("row_failure_class=%s" % flat(row.get("failure_class")))
    print("row_run=%s" % flat(row.get("run")))
    print("row_run_id=%s" % flat(row.get("run_id")))
    print("row_status=%s" % flat(row.get("status")))
    print("reason=%s" % flat(doc.get("reason") or row.get("reason")))
elif what == "record":
    doc = load(path)
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("outcome=%s" % flat(doc.get("outcome")))
    print("failure_class=%s" % flat(doc.get("failure_class")))
    print("field_present=%s" % yes("failure_class" in doc))
    print("started_at=%s" % flat(doc.get("started_at")))
    print("finished_at=%s" % flat(doc.get("finished_at")))
    print("reason=%s" % flat(doc.get("reason")))
    print("cost_usd=%s" % num(doc.get("cost_usd")))
    print("stage_count=%d" % len(doc.get("stages") or []))
elif what == "objects":
    want = re.compile(sys.argv[3]) if len(sys.argv) > 3 and sys.argv[3] else None
    objs = [(u, t) for (u, t) in listing(path) if want is None or want.match(u)]
    print("count=%d" % len(objs))
    for url, text in objs:
        print("object\t%s\t%s" % (url, text))
elif what == "candidates":
    # The old records steps 3 and 4 are about: one per repository slug, minted
    # inside the listing window (at or after SINCE, because that is what the
    # listing command itself shows) and minted BEFORE the week's last day (a run
    # minted later cannot have finished inside the week), newest first, so the
    # records read first are the ones nearest the week the digest reports.
    slug = sys.argv[3]
    since = sys.argv[4]
    before = sys.argv[5]
    want = re.compile(r"^gs://[^/]+/runs/%s/[^/]+/result\.json$" % re.escape(slug))
    found = []
    seen = 0
    for url, text in listing(path):
        if not want.match(url):
            continue
        seen += 1
        run_id = url.rsplit("/", 2)[-2]
        stamp = run_id[:15]
        if not re.match(r"^[0-9]{8}-[0-9]{6}$", stamp):
            continue
        if stamp < since or stamp >= before:
            continue
        found.append((stamp, url, text))
    found.sort(reverse=True)
    print("count=%d" % seen)
    print("candidates=%d" % len(found))
    for stamp, url, text in found:
        print("candidate\t%s\t%s\t%s" % (stamp, url, text))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file. The exit status is the
# reader's own, so a document it cannot parse is a FAILed row or a stop at the
# call site and never a set of empty values.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

# read_objects DEST LISTING_FILE PATTERN: the objects of a listing, in file order.
read_objects() { # read_objects DEST LISTING_FILE PATTERN
  python3 "$SCRATCH/read.py" objects "$2" "$3" >"$1" 2>"$1.err"
}

# field FILE KEY — the value of one `key=value` line, "" when it is absent.
field() { # field FILE KEY
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_field COLUMN RUN_ID — one column of one run's row of the listing read into
# rows.txt. The run's own id is what picks the row: a list read by position alone
# could be another run's. The columns are NAMED and not numbered, because the
# row's own leading label shifts every index by one and the empty failure_class a
# record written before the field shipped carries is what most of these reads are
# about: an index counted by hand here is a comparison of two different columns.
row_field() { # row_field run_id|status|failure_class|created|run RUN_ID
  local col
  case "$1" in
  run_id) col=2 ;;
  status) col=3 ;;
  failure_class) col=4 ;;
  created) col=5 ;;
  run) col=7 ;;
  *) return 1 ;;
  esac
  awk -F'\t' -v id="$2" -v c="$col" '$1 == "row" && $2 == id { print $c; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# count_rows — how many rows the listing read into rows.txt carries.
count_rows() { # count_rows
  awk '$1 == "row"' "$SCRATCH/rows.txt" 2>/dev/null | wc -l | tr -d ' '
}

# hash_of FILE — the SHA-256 of a file's bytes, in python3 (the one tool this
# check already needs) so the figure is the same on every machine the owner runs
# it on, and so a record whose bytes hold no newline at the end is hashed as it
# is.
hash_of() { # hash_of FILE
  python3 -c 'import hashlib, sys; print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())' "$1"
}

# first_line FILE: a tool's own words, one line, for a detail a reader has to be
# able to scan.
first_line() {
  if [ -f "$1" ]; then
    head -1 "$1" 2>/dev/null | cut -c1-300 || true
  fi
}

# --------------------------------------------------------------- the window
# compute_window: the week steps 3 and 4 are about, and the window the listing is
# read with. The digest reports the most recent CLOSED period — the week before
# the one today falls in, Monday to Sunday, UTC (a run counts in the period it
# finishes, in UTC) — so this computes exactly those bounds: a record chosen
# outside them is one the digest never reports, and step 3 would then read a week
# that is not the week's. The default window reaches back past the week's first
# day with a week of room, because a run minted before the week and finishing
# inside it is one steps 3 and 4 must still be able to read, and `runs ls
# --since` windows on the mint and not on the finish.
compute_window() {
  local bounds
  if ! bounds=$(python3 - <<'PY'
import datetime
now = datetime.datetime.now(datetime.timezone.utc)
today = now.date()
monday = today - datetime.timedelta(days=today.weekday())
to = monday
frm = monday - datetime.timedelta(days=7)
days = -(-(now - datetime.datetime.combine(frm, datetime.time.min, datetime.timezone.utc)).total_seconds() // 86400)
days = int(max(1, days)) + 7
# An hour of slack on the window's own edge: the CLI windows on its own clock,
# read a moment after this one, so a run minted right at the edge is still in the
# listing even though this stamp says it is just outside.
since = now - datetime.timedelta(days=days) + datetime.timedelta(hours=1)
print(frm.isoformat())
print(to.isoformat())
print(days)
print(since.strftime("%Y%m%d-%H%M%S"))
print(to.strftime("%Y%m%d-000000"))
print((to - datetime.timedelta(days=1)).isoformat())
PY
  ); then
    stop "this check could not work out the week the digest reports (python3 did not answer), so it cannot tell which records steps 3 and 4 are about"
  fi
  WEEK_FROM=$(printf '%s\n' "$bounds" | sed -n 1p)
  WEEK_TO=$(printf '%s\n' "$bounds" | sed -n 2p)
  local days
  days=$(printf '%s\n' "$bounds" | sed -n 3p)
  SINCE_STAMP=$(printf '%s\n' "$bounds" | sed -n 4p)
  WEEK_TO_STAMP=$(printf '%s\n' "$bounds" | sed -n 5p)
  WEEK_LAST=$(printf '%s\n' "$bounds" | sed -n 6p)
  SINCE_USED=${SINCE:-${days}d}
}

# in_window STAMP: whether an RFC3339 stamp falls inside the week steps 3 and 4
# are about. That week is the digest's own period, which runs from WEEK_FROM to
# WEEK_LAST inclusive (the Sunday); the listing's mint bound is the exclusive
# WEEK_TO beside it. A stamp the reader could not give is "no": a record whose
# own time cannot be read is not evidence about a week.
in_window() { # in_window STAMP
  [ -n "$1" ] || return 1
  local day=${1%%T*}
  [ "$day" \> "$WEEK_FROM" ] || [ "$day" = "$WEEK_FROM" ] || return 1
  [ "$day" \< "$WEEK_TO" ]
}

# object_line FILE — the bucket's own line for one object (its size and its own
# time), out of a listing this check has already read. It is the second reading
# of the read-only step beside the bytes: a record rewritten with the same bytes
# — a reserialisation, a copy — moves this line even when the hash does not.
object_line() { # object_line FILE
  awk -F'\t' '$1 == "object" { print $2; exit }' "$1" 2>/dev/null
}

# ------------------------------------------------------------------ the launch
# step_branch: the throwaway reference. It is cut with PLUMBING and not with a
# checkout: `git read-tree` into an index of this run's own, one blob replaced,
# `git commit-tree`, and the ref created by name — so your work tree is never
# switched, nothing you have uncommitted is touched, and the branch the Restore
# deletes is one only this run ever held. The replaced file is `fugaro.yaml` plus
# an unknown key, which the strict decoder refuses: that is the repeatable cause
# step 1 asks for, and it calls no model.
step_branch() {
  say "cutting the reference $BRANCH at $REPO's HEAD (fugaro.yaml plus an unknown key, without touching your work tree)"
  local head blob tree commit idx="$SCRATCH/index"
  if ! head=$(git -C "$REPO" rev-parse HEAD 2>"$SCRATCH/git.err"); then
    fact branch FAIL "the checkout's HEAD could not be read ($(first_line "$SCRATCH/git.err")): the reference the run is cut from is a commit of this checkout, so nothing could be launched"
    return 0
  fi
  if ! git -C "$REPO" show "HEAD:fugaro.yaml" >"$SCRATCH/fugaro.yaml" 2>"$SCRATCH/git.err"; then
    fact branch FAIL "fugaro.yaml could not be read from $REPO's HEAD ($(first_line "$SCRATCH/git.err")): the throwaway reference is HEAD with ONE file replaced, and a checkout with no tracked fugaro.yaml has nothing to replace"
    return 0
  fi
  printf '\n# Check 350: an unknown key, which the repository config decoder refuses.\nchk350_invalid_key: true\n' >>"$SCRATCH/fugaro.yaml"
  if ! blob=$(git -C "$REPO" hash-object -w "$SCRATCH/fugaro.yaml" 2>"$SCRATCH/git.err"); then
    fact branch FAIL "the replacement fugaro.yaml could not be written into $REPO's object store ($(first_line "$SCRATCH/git.err")): nothing was pushed and no run was launched"
    return 0
  fi
  rm -f "$idx"
  if ! (cd "$REPO" && env GIT_INDEX_FILE="$idx" git read-tree "$head") 2>"$SCRATCH/git.err"; then
    fact branch FAIL "the reference's tree could not be read from HEAD ($(first_line "$SCRATCH/git.err")): nothing was pushed and no run was launched"
    return 0
  fi
  if ! (cd "$REPO" && env GIT_INDEX_FILE="$idx" git update-index --add --cacheinfo "100644,$blob,fugaro.yaml") 2>"$SCRATCH/git.err"; then
    fact branch FAIL "the replacement fugaro.yaml could not be put into the reference's tree ($(first_line "$SCRATCH/git.err")): nothing was pushed and no run was launched"
    return 0
  fi
  if ! tree=$(cd "$REPO" && env GIT_INDEX_FILE="$idx" git write-tree 2>"$SCRATCH/git.err"); then
    fact branch FAIL "the reference's tree could not be written ($(first_line "$SCRATCH/git.err")): nothing was pushed and no run was launched"
    return 0
  fi
  # An explicit identity: the commit is this check's, and a machine with no
  # user.email configured would otherwise refuse to make it at all.
  if ! commit=$(cd "$REPO" && env GIT_INDEX_FILE="$idx" git \
    -c user.name="Check 350 (throwaway reference)" -c user.email="check350@example.invalid" \
    commit-tree "$tree" -p "$head" -m "Check 350: a fugaro.yaml with an unknown key (throwaway reference; safe to delete)" 2>"$SCRATCH/git.err"); then
    fact branch FAIL "the reference's commit could not be made ($(first_line "$SCRATCH/git.err")): nothing was pushed and no run was launched"
    return 0
  fi
  BRANCH_COMMIT=$commit
  if ! git -C "$REPO" update-ref "refs/heads/$BRANCH" "$commit" 2>"$SCRATCH/git.err"; then
    fact branch FAIL "the reference $BRANCH could not be created in $REPO ($(first_line "$SCRATCH/git.err")): the local branch is what the Restore deletes, so it is made before anything is pushed"
    return 0
  fi
  if ! git -C "$REPO" push --no-verify origin "refs/heads/$BRANCH:refs/heads/$BRANCH" >"$SCRATCH/push.out" 2>"$SCRATCH/push.err"; then
    fact branch FAIL "the reference $BRANCH could not be pushed to $REPO's origin ($(first_line "$SCRATCH/push.err")): a run is cut from a ref the runner can fetch, so without it there is nothing to launch. Nothing was launched"
    return 0
  fi
  BRANCH_PUSHED=yes
  say "pushed $BRANCH ($commit, one file replaced) to $REPO's origin"
  fact branch PASS "the reference this check cut is $BRANCH ($commit): cut at $REPO's HEAD with ONE file replaced and pushed to its origin, so the run below is cut from a ref the runner can fetch. It is this check's own — your work tree was never switched — and the Restore deletes it again"
}

# step_launch: ONE run, cut from that reference, refused at bootstrap. The
# launcher reads the configuration from YOUR checkout — where fugaro.yaml is the
# one you have — so it starts the run as usual; what the run's own checkout finds
# is the reference's file, and the refusal is the runner's, recorded in the run's
# record. That is the repeatable `config_invalid` step 1 asks for.
step_launch() {
  if [ "$BRANCH_PUSHED" != yes ]; then
    fact run FAIL "no run was launched: the reference above was not pushed, and a run is cut from a ref the runner can fetch"
    return 0
  fi
  say "launching one run of $REPO_NAME from the reference $BRANCH (refused at bootstrap, so nothing billable)"
  run_bounded "$LAUNCH_BOUND_SECONDS" launch "$REPO" \
    fugaro run --repo "$REPO" --ref "$BRANCH" --json "$TASK_TEXT"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    fact run FAIL "the launch did not finish inside ${LAUNCH_BOUND_SECONDS}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run FAIL "fugaro run exited $BOUNDED_STATUS from $REPO: this check measures a run the JOB refuses at bootstrap, and a launcher that refuses before starting one says something else is wrong with the sandbox. What it printed is in $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run FAIL "fugaro run --json printed a document this check could not read: the launch is how the run is named, and its own --json view is what names it. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  RUN_REF=$(field "$SCRATCH/launch.txt" run)
  PROJECT_SEEN=$(field "$SCRATCH/launch.txt" project)
  local repo
  repo=$(field "$SCRATCH/launch.txt" repo)
  if [ -n "$repo" ]; then REPO_NAME=$repo; fi
  case "$RUN_REF" in
  */*) SLUG=${RUN_REF%%/*} ;;
  *) SLUG="" ;;
  esac
  if [ -z "$RUN_ID" ] || [ -z "$SLUG" ]; then
    fact run FAIL "fugaro run --json named no run this check could take a bucket key from (run_id=${RUN_ID:-none}, run=${RUN_REF:-none}): the record of the run it launched is what steps 1, 3 and 4 read, and the slug in that reply's own 'run' is where it lives. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run PASS "fugaro run launched $RUN_REF from the reference $BRANCH of $REPO_NAME in project ${PROJECT_SEEN:-$P} (status $(field "$SCRATCH/launch.txt" status)): the run is this check's own, and the rows below follow this one and no other"
}

# step_settle: the run has to end — and it ends in seconds, because the refusal
# is at bootstrap and no stage starts. The record is read from the BUCKET and not
# through the CLI: steps 3 and 4 are about what a `runs ls` read does to another
# record, and the read-only step's hash has to be taken before the CLI reads
# anything of the window at all.
step_settle() {
  if [ -z "$RUN_ID" ]; then
    fact settled FAIL "there is no run to follow: the row above says why"
    return 0
  fi
  local key="runs/$SLUG/$RUN_ID/result.json"
  local ticks=0 maxticks=$(((RUN_TIMEOUT + POLL_SECONDS - 1) / POLL_SECONDS)) status=""
  POLL_READ=no
  [ "$maxticks" -lt 1 ] && maxticks=1
  say "waiting up to $((maxticks * POLL_SECONDS))s for $RUN_ID to end: gcloud storage cat gs://$B/$key every ${POLL_SECONDS}s"
  while [ "$ticks" -lt "$maxticks" ]; do
    ticks=$((ticks + 1))
    status=""
    run_bounded "$RUN_BOUND_SECONDS" poll "$REPO" gcloud storage cat "gs://$B/$key" --project="$GCP"
    if [ "$BOUNDED_STATUS" = 0 ] && read_json record "$SCRATCH/poll.out" "$SCRATCH/poll.txt"; then
      # The record WAS read. A run that is still going and a run whose record
      # never appeared leave the same empty `record.txt` behind, and they are two
      # different readings of the sandbox: one says the run is slow, the other
      # that the bucket never saw it at all.
      POLL_READ=yes
      status=$(field "$SCRATCH/poll.txt" status)
      case "$status" in
      "" | running) ;;
      *)
        cp "$SCRATCH/poll.out" "$SCRATCH/record.out"
        cp "$SCRATCH/poll.txt" "$SCRATCH/record.txt"
        break
        ;;
      esac
    fi
    [ "$ticks" -lt "$maxticks" ] && sleep "$POLL_SECONDS"
  done
  case "$status" in
  "" | running) ;;
  *)
    SETTLED=yes
    fact settled PASS "$RUN_ID ended within $((ticks * POLL_SECONDS))s (status=$status, failure_class=$(field "$SCRATCH/record.txt" failure_class)): the run reached its own ending, read from its record in the bucket, and nothing had to be cancelled"
    return 0
    ;;
  esac
  # A record that never appeared at all is a different reading from one that is
  # still running, and the row says which: the first says the run never
  # bootstrapped, the second that it is still going.
  if [ "$POLL_READ" != yes ]; then
    fact settled FAIL "$RUN_ID left no record under runs/$SLUG/$RUN_ID/ within $((maxticks * POLL_SECONDS))s, so it never bootstrapped: there is no ending to read, and the class rows below cannot be measured. What gcloud printed is in $SCRATCH/poll.err"
  else
    fact settled FAIL "$RUN_ID was still running after $((maxticks * POLL_SECONDS))s (status=$status): a bootstrap refusal ends in seconds, so a run still going is one this check could not measure"
  fi
  cancel_run "it had not ended within ${RUN_TIMEOUT}s"
  stop "$RUN_ID had not ended within ${RUN_TIMEOUT}s, so there is no ending to read and the class rows below cannot be measured. It was cancelled rather than left going; read it with 'fugaro runs show $RUN_ID' and run this check again"
}

# step_class: step 1's first half — the class is the one the cause deserves. The
# cause is a configuration the strict decoder refuses, which is `config_invalid`
# and never `unclassified`, so a run that failed some other way is a different
# ending and this check may not read it as this one.
step_class() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    fact class FAIL "there is no record to read the class from: the row above says why"
    return 0
  fi
  local class reason
  class=$(field "$SCRATCH/record.txt" failure_class)
  reason=$(field "$SCRATCH/record.txt" reason)
  if [ "$class" != config_invalid ]; then
    fact class FAIL "the run ended $(field "$SCRATCH/record.txt" status) with failure_class '${class:-none}' rather than config_invalid: a fugaro.yaml the strict decoder refuses is a configuration the runner refuses at bootstrap, and that ending is recorded config_invalid. This run ended for another reason — its reason reads: ${reason:-none}. The record is in $SCRATCH/record.out"
    return 0
  fi
  fact class PASS "the run ended $(field "$SCRATCH/record.txt" status) with failure_class config_invalid, calling no model ($(field "$SCRATCH/record.txt" stage_count) stages, cost_usd $(field "$SCRATCH/record.txt" cost_usd)): the class is the one the cause deserves, and not unclassified. Its reason reads: $(printf '%s' "$reason" | cut -c1-200)"
}

# step_target: the old record steps 3 and 4 are about — one written BEFORE the
# field shipped, whose own bytes carry no failure_class and which failed all the
# same, and which finished inside the week the digest reports. It is chosen from
# the BUCKET alone, and its bytes are hashed here, before any `fugaro` read of
# the window: a class written back by a reader would already be in the record at
# the first CLI read, and a hash taken after it would compare a rewritten record
# with itself.
step_target() {
  if [ -z "$SLUG" ]; then
    fact target FAIL "there is no repository slug to look the old record up under: the row above says why"
    return 0
  fi
  say "listing the records under runs/$SLUG/ to find one written before failure_class shipped (gs://$B, metadata only)"
  run_bounded "$RUN_BOUND_SECONDS" objects "$REPO" gcloud storage ls -l --recursive "gs://$B/runs/$SLUG/" --project="$GCP"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact target FAIL "gs://$B/runs/$SLUG/ could not be listed (exit $BOUNDED_STATUS): the old record this step is about is chosen from the bucket, before any fugaro read of the window, so a listing that does not answer leaves nothing to choose from. What gcloud printed is in $SCRATCH/objects.err"
    return 0
  fi
  if ! python3 "$SCRATCH/read.py" candidates "$SCRATCH/objects.out" "$SLUG" "$SINCE_STAMP" "$WEEK_TO_STAMP" >"$SCRATCH/candidates.txt" 2>"$SCRATCH/candidates.err"; then
    fact target FAIL "the listing of gs://$B/runs/$SLUG/ could not be read ($(first_line "$SCRATCH/candidates.err")): what gcloud printed is in $SCRATCH/objects.out"
    return 0
  fi
  local seen candidates
  seen=$(field "$SCRATCH/candidates.txt" count)
  candidates=$(field "$SCRATCH/candidates.txt" candidates)
  if [ -z "$candidates" ] || [ "$candidates" = 0 ]; then
    fact target FAIL "gs://$B/runs/$SLUG/ holds $seen run record(s) and none of them was minted inside the window this check reads (--since $SINCE_USED: minted at or after $SINCE_STAMP and before ${WEEK_TO}T00:00:00Z, the day after the week's own last day): steps 3 and 4 need a record written before failure_class shipped that finished inside the week $WEEK_FROM..$WEEK_LAST, and this window holds none. Run the check on a sandbox repository that has run for longer than one week, or widen the window with --since 60d"
    stop "the window --since $SINCE_USED of $SLUG holds no run record minted before the week $WEEK_FROM..$WEEK_LAST, so the week's own half of step 3 and the whole of step 4 have nothing to read. Nothing is concluded about the classes: this is a could-not-tell"
  fi
  local want=$CANDIDATES read_any=no skipped="" why="" stamp url line
  [ "$want" -gt "$candidates" ] && want=$candidates
  local i=0
  while IFS=$'\t' read -r kind stamp url line; do
    [ "$kind" = candidate ] || continue
    [ "$i" -lt "$want" ] || break
    i=$((i + 1))
    local key=${url#gs://$B/} out="$SCRATCH/cand$i"
    run_bounded "$RUN_BOUND_SECONDS" "cand$i" "$REPO" gcloud storage cat "$url" --project="$GCP"
    if [ "$BOUNDED_STATUS" != 0 ] || ! read_json record "$out.out" "$out.txt"; then
      skipped="$skipped $key"
      continue
    fi
    read_any=yes
    local status finished present
    status=$(field "$out.txt" status)
    finished=$(field "$out.txt" finished_at)
    present=$(field "$out.txt" field_present)
    if ! in_window "$finished"; then
      [ -n "$why" ] || why="$key finished outside the week (finished_at=${finished:-none})"
      continue
    fi
    if [ "$status" = succeeded ] || [ -z "$status" ]; then
      [ -n "$why" ] || why="$key finished inside the week and did not fail (status=${status:-none})"
      continue
    fi
    if [ "$present" = true ]; then
      [ -n "$why" ] || why="$key failed inside the week and its own record already carries failure_class '$(field "$out.txt" failure_class)'"
      continue
    fi
    # The record step 3 wants: it failed, it finished inside the week, and its
    # own bytes carry no failure_class at all — a record written before the field
    # shipped, and the one a write-back would have changed.
    TARGET_KEY=$key
    TARGET_RUN=$(field "$out.txt" run_id)
    TARGET_BYTES_CLASS=$(field "$out.txt" failure_class)
    TARGET_STAMP=$stamp
    T0_HASH=$(hash_of "$out.out")
    cp "$out.out" "$SCRATCH/target.out"
    cp "$out.txt" "$SCRATCH/target.txt"
    break
  done <"$SCRATCH/candidates.txt"
  if [ -z "$TARGET_KEY" ]; then
    if [ "$read_any" != yes ]; then
      why="none of them could be read at all ($skipped)"
    fi
    fact target FAIL "of the $candidates record(s) minted inside the window (--since $SINCE_USED), the $want nearest the week were read and none of them is the one steps 3 and 4 are about: ${why:-none of them was a failed run whose record carries no failure_class}. The listing is in $SCRATCH/objects.out and each record read is in $SCRATCH/cand*.out, so what each one holds can be looked at by hand"
    stop "no record of the window $SINCE_USED is a failed run written before failure_class shipped, so the week's own half of step 3 and the whole of step 4 have nothing to read. Nothing is concluded about the classes: this is a could-not-tell"
  fi
  # The bucket's own line for the object, from a direct listing of it: the second
  # reading the read-only step compares beside the bytes.
  run_bounded "$RUN_BOUND_SECONDS" target-ls "$REPO" gcloud storage ls -l "gs://$B/$TARGET_KEY" --project="$GCP"
  if [ "$BOUNDED_STATUS" = 0 ] && read_objects "$SCRATCH/target-ls.txt" "$SCRATCH/target-ls.out" "^gs://[^/]+/runs/$SLUG/[^/]+/result\\.json$"; then
    T0_LINE=$(object_line "$SCRATCH/target-ls.txt")
  fi
  if [ -z "$T0_LINE" ]; then
    fact target FAIL "the bucket's own line for gs://$B/$TARGET_KEY could not be read (exit $BOUNDED_STATUS), so the second half of the read-only step has nothing to compare: what gcloud printed is in $SCRATCH/target-ls.out and $SCRATCH/target-ls.err"
    return 0
  fi
  fact target PASS "the old record of this week is $TARGET_KEY (run ${TARGET_RUN:-unknown}, finished $(field "$SCRATCH/target.txt" finished_at), minted $TARGET_STAMP): its own bytes carry NO failure_class (the field's value there is '${TARGET_BYTES_CLASS:-none}'), and both readings of it — sha256 $T0_HASH and the bucket's own line '$T0_LINE' — were taken BEFORE any fugaro read of the window"
}

# ----------------------------------------------------------------- the reads
# step_rows: the listing of steps 1 and 2's window, read ONCE and used for both —
# the row of the run this check launched, and the rows the groups below are
# checked against. This is the first `fugaro` read of the window, and with it the
# old record has been read once.
step_rows() {
  say "reading the window: fugaro runs ls --repo $REPO_NAME --since $SINCE_USED --json"
  run_bounded "$RUN_BOUND_SECONDS" rows "$REPO" fugaro runs ls --repo "$REPO_NAME" --since "$SINCE_USED" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact window FAIL "fugaro runs ls exited $BOUNDED_STATUS in $REPO (window --since $SINCE_USED): the rows the grouping is checked against are this read's, so nothing below could be measured. What it printed is in $SCRATCH/rows.err"
    return 0
  fi
  if ! read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
    fact window FAIL "fugaro runs ls --json printed a document this check could not read: what it printed is in $SCRATCH/rows.out"
    return 0
  fi
  fact window PASS "the window --since $SINCE_USED of $REPO_NAME holds $(field "$SCRATCH/rows.txt" runs) run(s) this read, carrying $(field "$SCRATCH/rows.txt" count) row(s): the totals are the window's own, and the row the class below is read from is picked out of it by the run's id"
}

# step_agree: step 1's second half — the row and the diagnosis carry the SAME
# class, and it is the class the cause deserves. The diagnosis is read for the
# run this check launched (its own record), so this read does not touch the old
# record the hash above was taken for.
step_agree() {
  if [ -z "$RUN_ID" ]; then
    fact agree FAIL "there is no run whose class could be compared: the row above says why"
    return 0
  fi
  local row_class row_seen
  row_class=$(row_field failure_class "$RUN_ID")
  row_seen=$(row_field run_id "$RUN_ID")
  if [ -z "$row_seen" ]; then
    fact agree FAIL "fugaro runs ls --json printed no row for $RUN_ID in this window (--since $SINCE_USED): the run this check launched is not in the listing it just read, so the row's class cannot be compared with the diagnosis. The listing's rows are in $SCRATCH/rows.out"
    return 0
  fi
  say "reading the diagnosis: fugaro diagnose $RUN_REF --json"
  run_bounded "$RUN_BOUND_SECONDS" diagnose "$REPO" fugaro diagnose "$RUN_REF" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact agree FAIL "fugaro diagnose exited $BOUNDED_STATUS for $RUN_REF: the diagnosis is half of what this step compares, so the two readings cannot be held against each other. What it printed is in $SCRATCH/diagnose.err"
    return 0
  fi
  if ! read_json diagnose "$SCRATCH/diagnose.out" "$SCRATCH/diagnose.txt"; then
    fact agree FAIL "fugaro diagnose --json printed a document this check could not read: what it printed is in $SCRATCH/diagnose.out"
    return 0
  fi
  local diag_class diag_row_class
  diag_class=$(field "$SCRATCH/diagnose.txt" failure_class)
  diag_row_class=$(field "$SCRATCH/diagnose.txt" row_failure_class)
  if [ "$row_class" != "$diag_class" ]; then
    fact agree FAIL "the listing's row for $RUN_ID carries failure_class '${row_class:-none}' and the diagnosis carries '${diag_class:-none}': the two commands read one record, so a reader told one class by the listing and another by the diagnosis has no class at all. The documents are in $SCRATCH/rows.out and $SCRATCH/diagnose.out"
    return 0
  fi
  if [ "$diag_row_class" != "$diag_class" ]; then
    fact agree FAIL "fugaro diagnose for $RUN_ID carries failure_class '${diag_class:-none}' at the top and '${diag_row_class:-none}' in its own row: the document's two places for one class disagree, and a reader of either is told something the other denies. The document is in $SCRATCH/diagnose.out"
    return 0
  fi
  if [ "$diag_class" != config_invalid ]; then
    fact agree FAIL "the row and the diagnosis agree on '${diag_class:-none}' for $RUN_ID, and the class this run's cause deserves is config_invalid: two readings of one wrong class are still a wrong class. The documents are in $SCRATCH/rows.out and $SCRATCH/diagnose.out"
    return 0
  fi
  fact agree PASS "the row of $RUN_ID in fugaro runs ls --json and fugaro diagnose $RUN_REF both carry failure_class config_invalid (the diagnosis's own row carries it too): the two commands a person reads a run with agree on why it failed"
}

# step_groups: step 2 — the grouping, in three rows: the groups' runs add up to
# the window's FAILED rows and each group holds exactly that class's rows, `share`
# follows the runs and not the spend, and the totals are the plain listing's own.
# This read is the second `fugaro` read of the old record.
step_groups() {
  if [ ! -s "$SCRATCH/rows.txt" ]; then
    fact groups FAIL "there is no plain listing to compare the grouping with: the row above says why"
    fact share FAIL "there is no plain listing to compare the shares with: the row above says why"
    fact totals FAIL "there is no plain listing to compare the totals with: the row above says why"
    return 0
  fi
  say "reading the grouping: fugaro runs ls --repo $REPO_NAME --since $SINCE_USED --group-by failure_class --json"
  run_bounded "$RUN_BOUND_SECONDS" grouped "$REPO" fugaro runs ls --repo "$REPO_NAME" --since "$SINCE_USED" --group-by failure_class --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact groups FAIL "fugaro runs ls --group-by failure_class exited $BOUNDED_STATUS in $REPO: the block this step is about was not read. What it printed is in $SCRATCH/grouped.err"
    fact share FAIL "the grouped listing did not answer (exit $BOUNDED_STATUS), so no share was read"
    fact totals FAIL "the grouped listing did not answer (exit $BOUNDED_STATUS), so no totals were read"
    return 0
  fi
  if ! read_json grouped "$SCRATCH/grouped.out" "$SCRATCH/grouped.txt"; then
    fact groups FAIL "fugaro runs ls --group-by failure_class printed a document this check could not read: what it printed is in $SCRATCH/grouped.out"
    fact share FAIL "the grouped document could not be read, so no share was read"
    fact totals FAIL "the grouped document could not be read, so no totals were read"
    return 0
  fi
  # The window's failed rows are its rows carrying a class — only a run that
  # failed has one — which is exactly what the block groups. Every one of these
  # figures is read with awk and not with bash's `read`: a tab is IFS whitespace,
  # so `read` DROPS an empty field and would shift a row whose class is empty into
  # the columns after it — and an empty class is exactly what this step reads.
  local failed sum mismatch groups_n
  failed=$(awk -F'\t' '$1 == "row" && $4 != "" { n++ } END { print n + 0 }' "$SCRATCH/rows.txt")
  sum=$(awk -F'\t' '$1 == "group" { s += $3 } END { print s + 0 }' "$SCRATCH/grouped.txt")
  # Every row of that class in the window has to be in the group: a group whose
  # count is not the window's own rows of that class is a figure that does not
  # follow from the listing beside it.
  mismatch=$(awk -F'\t' '
    FNR == NR {
      if ($1 == "group") { want[$2] = $3; order[++n] = $2 }
      next
    }
    $1 == "row" && $4 != "" { rows[$4]++ }
    END {
      for (i = 1; i <= n; i++) {
        c = order[i]
        have = rows[c] + 0
        if (have != want[c] + 0) printf " %s=%s(rows=%s)", c, want[c], have
      }
    }' "$SCRATCH/grouped.txt" "$SCRATCH/rows.txt")
  groups_n=$(field "$SCRATCH/grouped.txt" groups)
  local plain_count window_runs
  plain_count=$(count_rows)
  window_runs=$(field "$SCRATCH/rows.txt" runs)
  if [ "$sum" = "$failed" ] && [ -z "$mismatch" ]; then
    fact groups PASS "the ${groups_n:-0} group(s) of --group-by failure_class account for every failed row of the window and no other: their runs add up to $failed, which is the window's rows carrying a failure class (of $window_runs run(s) in the window, $plain_count row(s) read). The classes are: $(awk -F'\t' '$1 == "group" { printf "%s ", $2 }' "$SCRATCH/grouped.txt")"
  else
    local note=""
    [ -n "$mismatch" ] && note=", and a group whose count is not the window's rows of that class reads:$mismatch"
    fact groups FAIL "the groups do not follow the window's own listing: the groups' runs add up to $sum and the window's failed rows are $failed$note. The two documents are in $SCRATCH/rows.out and $SCRATCH/grouped.out"
  fi

  # share is a share of the window's RUNS (the decision the block's own comment
  # names): the figure of the row beside it, and never a share of the spend. So
  # the check recomputes it from the two counts the block itself carries, at the
  # four decimal places the block rounds to.
  local bad_share=""
  bad_share=$(awk -F'\t' -v n="$plain_count" '
    $1 == "group" {
      want = ($3 + 0) / n
      want = int(want * 10000 + 0.5) / 10000
      if (($4 + 0) != want) printf " %s=%s(want %s)", $2, $4, want
    }' "$SCRATCH/grouped.txt")
  if [ -n "$groups_n" ] && [ "$groups_n" != 0 ] && [ -z "$bad_share" ]; then
    fact share PASS "every group's share is its runs over the window's runs ($plain_count), to four decimal places: share follows the runs and not the spend, and the spend it does not follow is the column beside it"
  elif [ -z "$bad_share" ]; then
    fact share PASS "the window holds no failed run this read ($plain_count row(s), none carrying a class), so --group-by printed no block and there is no share to read: a window with nothing to group is not a share of zero, which would read as a class that happened and cost nothing"
  else
    fact share FAIL "these groups' shares are not their runs over the window's runs ($plain_count):$bad_share. share is a share of the runs, with the spend beside it as its own column, and a share of the spend under a runs column is the reading the block's own decision forbids. The document is in $SCRATCH/grouped.out"
  fi

  # The totals do not move: --group-by adds a block beside the rows and their
  # totals, and a listing whose totals changed with the grouping would make the
  # two listings two readings of one window.
  local keys=(runs model_usd model_notional_usd compute_usd total_usd compute_not_estimated) moved="" k a b
  for k in "${keys[@]}"; do
    a=$(field "$SCRATCH/rows.txt" "$k")
    b=$(field "$SCRATCH/grouped.txt" "$k")
    [ "$a" = "$b" ] || moved="$moved $k(plain=$a, grouped=$b)"
  done
  if [ -z "$moved" ]; then
    fact totals PASS "the grouped listing's totals are the plain listing's own — $(field "$SCRATCH/rows.txt" runs) run(s), total_usd $(field "$SCRATCH/rows.txt" total_usd) (model $(field "$SCRATCH/rows.txt" model_usd) + compute $(field "$SCRATCH/rows.txt" compute_usd)), of which $(field "$SCRATCH/rows.txt" model_notional_usd) is model spend that is not billed: --group-by adds the block and moves neither the rows nor the figures beside them"
  else
    fact totals FAIL "the totals moved between the two listings of one window:$moved. The plain listing and the grouped one are two readings of one window, and a window whose totals change with the grouping is a window nobody can read twice. The documents are in $SCRATCH/rows.out and $SCRATCH/grouped.out"
  fi
}

# step_target_class: step 4's third read — the diagnosis of the OLD record, whose
# class has to be the one its listing row carries. This is the last `fugaro` read
# of that record before the hash is taken the second time.
step_target_class() {
  if [ -z "$TARGET_KEY" ]; then
    fact old-record FAIL "there is no old record to read: the row above says why"
    return 0
  fi
  if [ -z "$TARGET_RUN" ]; then
    fact old-record FAIL "the old record $TARGET_KEY carries no run_id this check could diagnose: the class it lands in is what this row is about"
    return 0
  fi
  say "reading the diagnosis of the old record: fugaro diagnose $SLUG/$TARGET_RUN --json"
  run_bounded "$RUN_BOUND_SECONDS" old-diagnose "$REPO" fugaro diagnose "$SLUG/$TARGET_RUN" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact old-record FAIL "fugaro diagnose exited $BOUNDED_STATUS for the old record ${SLUG}/${TARGET_RUN}: the class its reason maps to is one of the two readings this row holds against each other. What it printed is in $SCRATCH/old-diagnose.err"
    return 0
  fi
  if ! read_json diagnose "$SCRATCH/old-diagnose.out" "$SCRATCH/old-diagnose.txt"; then
    fact old-record FAIL "fugaro diagnose --json printed a document this check could not read for the old record: what it printed is in $SCRATCH/old-diagnose.out"
    return 0
  fi
  local row_class diag_class
  row_class=$(row_field failure_class "$TARGET_RUN")
  diag_class=$(field "$SCRATCH/old-diagnose.txt" failure_class)
  TARGET_CLASS=$row_class
  if [ -z "$row_class" ]; then
    fact old-record FAIL "the old record $TARGET_KEY carries no failure_class in its own bytes AND none in its row of fugaro runs ls --json (window --since $SINCE_USED): a record written before the field shipped is classed from its reason at read time, so an empty row here says the reader did not class it — the week IS split into before and after, which is what the redesign exists to remove. Its reason reads: $(field "$SCRATCH/target.txt" reason)"
    return 0
  fi
  if [ "$row_class" != "$diag_class" ]; then
    fact old-record FAIL "the old record's row carries '${row_class}' and its diagnosis carries '${diag_class:-none}': the two commands read one record, and a record written before the field shipped is exactly where a reader that classes it one way and reports it another would show. The documents are in $SCRATCH/rows.out and $SCRATCH/old-diagnose.out"
    return 0
  fi
  fact old-record PASS "the old record $TARGET_KEY — written before failure_class shipped, its own bytes carrying none — is read as failure_class '${row_class}' by fugaro runs ls --json and by fugaro diagnose alike: the reader classes it from its reason rather than leaving the week split into before and after"
}

# step_digest: step 3 — the week's report. The table has to be there (the half a
# person reads) and the summary block has to carry `failures` (the half a later
# report reads), and the old record's own class has to be counted among them.
step_digest() {
  say "reading the week's report: fugaro runs digest --period week --repo $REPO_NAME --stdout"
  run_bounded "$RUN_BOUND_SECONDS" digest "$REPO" fugaro runs digest --period week --repo "$REPO_NAME" --stdout
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact digest FAIL "fugaro runs digest --period week exited $BOUNDED_STATUS in $REPO: the week's report was not read. What it printed is in $SCRATCH/digest.err"
    return 0
  fi
  if ! grep -q '^## Failures' "$SCRATCH/digest.out"; then
    fact digest FAIL "the week's report carries no '## Failures' table: the table is drawn only when the period's summary holds failures, so either the week had none (and the block read below says which classes it does hold) or the report is not the one this check reads. The report is in $SCRATCH/digest.out"
    return 0
  fi
  say "reading the week's summary block: fugaro runs digest --period week --repo $REPO_NAME --json"
  run_bounded "$RUN_BOUND_SECONDS" digest-json "$REPO" fugaro runs digest --period week --repo "$REPO_NAME" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact digest FAIL "fugaro runs digest --json exited $BOUNDED_STATUS in $REPO: the summary block is the half of this step a later report reads, and it was not read. What it printed is in $SCRATCH/digest-json.err"
    return 0
  fi
  if ! read_json digest "$SCRATCH/digest-json.out" "$SCRATCH/digest-json.txt"; then
    fact digest FAIL "fugaro runs digest --json printed a document this check could not read: what it printed is in $SCRATCH/digest-json.out"
    return 0
  fi
  local total present pf pt see
  total=$(field "$SCRATCH/digest-json.txt" failures_total)
  present=$(field "$SCRATCH/digest-json.txt" failures_present)
  pf=$(field "$SCRATCH/digest-json.txt" period_start)
  pt=$(field "$SCRATCH/digest-json.txt" period_end)
  see="the week $(field "$SCRATCH/digest-json.txt" period) (${pf:-?}..${pt:-?})"
  if [ "$present" != true ] || [ -z "$total" ] || [ "$total" = 0 ]; then
    fact digest FAIL "the week's summary block carries no failures (failures_present=${present:-false}, failures_total=${total:-0}): the Failures table beside it is drawn from that block, and a week whose block holds none is a week whose classes this check cannot read the old record out of. The block is in $SCRATCH/digest-json.out"
    return 0
  fi
  # The period the document itself names is held against this check's own week:
  # the candidates were drawn from this check's bounds, and a document reporting
  # another period would mean the two are not the same week.
  if [ "$pf" != "$WEEK_FROM" ] || [ "$pt" != "$WEEK_LAST" ]; then
    fact digest FAIL "$see is not the week this check read the bucket for (${WEEK_FROM}..${WEEK_LAST}): the old record was chosen from this check's own week, so a report of another period cannot be held against it — read the report's own period and run the check again. The block is in $SCRATCH/digest-json.out"
    return 0
  fi
  local counted
  counted=$(awk -F'\t' -v c="$TARGET_CLASS" '$1 == "failure" && $2 == c { print $3; exit }' "$SCRATCH/digest-json.txt")
  if [ -z "$counted" ]; then
    fact digest FAIL "the week's summary block does not count the class the old record is read as ('${TARGET_CLASS:-none}'): its classes are $(awk -F'\t' '$1 == "failure" { printf "%s ", $2 }' "$SCRATCH/digest-json.txt"). A record written before the field shipped whose class the report does not count is exactly the split into before and after this step exists to falsify. The block is in $SCRATCH/digest-json.out"
    return 0
  fi
  fact digest PASS "$see carries the Failures table, and its summary block counts $total failure(s) by class — the old record's own class '$TARGET_CLASS' among them, $counted time(s): a record written before the field shipped is counted in the week beside the ones written after it"
}

# step_readonly: step 4's comparison — the bytes of the old record, and the
# bucket's own line for it, taken now that the CLI has read it (twice through
# `runs ls --json`, once through `diagnose`) and held against the same two taken
# before the first of those reads. This is the one step a code review cannot
# settle: the design's claim is that a reader writes nothing back.
step_readonly() {
  if [ -z "$TARGET_KEY" ]; then
    fact readonly FAIL "there is no old record whose bytes could be compared: the row above says why"
    return 0
  fi
  say "re-reading the old record to compare it with the readings taken before the first read: gcloud storage cat gs://$B/$TARGET_KEY"
  local out="$SCRATCH/target-after.out"
  run_bounded "$RUN_BOUND_SECONDS" target-after "$REPO" gcloud storage cat "gs://$B/$TARGET_KEY" --project="$GCP"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact readonly FAIL "gs://$B/$TARGET_KEY could not be read again after the CLI's three reads (exit $BOUNDED_STATUS): the comparison this step is about was not made, so nothing is concluded about what a read writes. What gcloud printed is in $SCRATCH/target-after.err"
    return 0
  fi
  T1_HASH=$(hash_of "$out")
  run_bounded "$RUN_BOUND_SECONDS" target-after-ls "$REPO" gcloud storage ls -l "gs://$B/$TARGET_KEY" --project="$GCP"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact readonly FAIL "the bucket's own line for gs://$B/$TARGET_KEY could not be read again (exit $BOUNDED_STATUS): the bytes were hashed and the object's own line was not, so half of this comparison was not made. What gcloud printed is in $SCRATCH/target-after-ls.err"
    return 0
  fi
  if ! read_objects "$SCRATCH/target-after-ls.txt" "$SCRATCH/target-after-ls.out" "^gs://[^/]+/runs/$SLUG/[^/]+/result\\.json$"; then
    fact readonly FAIL "the bucket's line for the old record could not be read ($(first_line "$SCRATCH/target-after-ls.txt.err")): gcloud printed $(head -c 300 "$SCRATCH/target-after-ls.out" 2>/dev/null)"
    return 0
  fi
  T1_LINE=$(object_line "$SCRATCH/target-after-ls.txt")
  if [ -z "$T1_LINE" ]; then
    fact readonly FAIL "the bucket printed no line for gs://$B/$TARGET_KEY the second time: the object could not be read back, so nothing is concluded about what the reads wrote. What gcloud printed is in $SCRATCH/target-after-ls.out"
    return 0
  fi
  if [ "$T0_HASH" != "$T1_HASH" ]; then
    fact readonly FAIL "the bytes of $TARGET_KEY CHANGED between the reading taken before the first fugaro read of it and the one taken after three of them (before: $T0_HASH, after: $T1_HASH): a read of the window writes the record back, which is the one thing the design promises it does not do — and the field that came back is in $out. This is the finding, not a flake: keep both records and read the diff"
    return 0
  fi
  if [ "$T0_LINE" != "$T1_LINE" ]; then
    fact readonly FAIL "the bytes of $TARGET_KEY are unchanged ($T0_HASH), and the bucket's own line for the object is not: before '$T0_LINE', after '$T1_LINE'. An object rewritten with the same bytes is still an object a read wrote, and this line is what catches it"
    return 0
  fi
  fact readonly PASS "the record $TARGET_KEY is byte-identical after the three reads this check made of it — sha256 $T0_HASH, and the bucket's own line for it unchanged ('$T0_LINE') — and both readings were taken before the first of those reads: nothing is written back into a record by reading it. The reads were fugaro runs ls --json, fugaro runs ls --group-by failure_class --json and fugaro diagnose $SLUG/$TARGET_RUN"
}

# ------------------------------------------------------------------- the steps
# step_step5: the refutation step, read and never made. `--step5-run` is a run YOU
# made — a job image whose agent call the account's own limit refused — and what
# this check does with it is read its class: `stage_error_unexplained` is the
# reading the plan expects (O6 unanswered: no class of its own), and any other
# class is a finding about the classifier rather than a failure of yours.
step_step5() {
  if [ -z "$STEP5_RUN" ]; then
    fact step5 INFO "not measured by this run: step 5 is a run YOU make, and this check never fakes it — a job image whose agent call the account's own limit refuses, run by hand on the sandbox. Read it with 'fugaro diagnose <run> --json' afterwards, and run this check again with --step5-run <run> to have its class read here. The class the plan expects is stage_error_unexplained (O6 unanswered), and no class of its own"
    return 0
  fi
  say "reading the refutation run: fugaro diagnose $STEP5_RUN --json"
  run_bounded "$RUN_BOUND_SECONDS" step5 "$REPO" fugaro diagnose "$STEP5_RUN" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact step5 FAIL "fugaro diagnose exited $BOUNDED_STATUS for $STEP5_RUN: the run you named could not be read, so its class was not measured. What it printed is in $SCRATCH/step5.err"
    return 0
  fi
  if ! read_json diagnose "$SCRATCH/step5.out" "$SCRATCH/step5.txt"; then
    fact step5 FAIL "fugaro diagnose --json printed a document this check could not read for $STEP5_RUN: what it printed is in $SCRATCH/step5.out"
    return 0
  fi
  local class status reason
  class=$(field "$SCRATCH/step5.txt" failure_class)
  status=$(field "$SCRATCH/step5.txt" row_status)
  reason=$(field "$SCRATCH/step5.txt" reason)
  if [ "$class" = stage_error_unexplained ]; then
    fact step5 PASS "$STEP5_RUN (status ${status:-unknown}) carries failure_class stage_error_unexplained: the account's own limit refusing an agent call lands in the unexplained class and in no class of its own, which is O6 unanswered — and this read is what tells you what the report looks like meanwhile. Its reason reads: $(printf '%s' "$reason" | cut -c1-200)"
    return 0
  fi
  fact step5 FAIL "$STEP5_RUN (status ${status:-unknown}) carries failure_class '${class:-none}' rather than stage_error_unexplained: the plan's refutation step expects an agent call the account's own limit refused to land in the unexplained class (O6 unanswered, no class of its own), and another class here is a finding about the classifier — report it rather than reading it as the refutation holding. Its reason reads: $(printf '%s' "$reason" | cut -c1-200)"
}

# step_step6: the HARD RULE, which is yours and is not measured here.
step_step6() {
  fact step6 INFO "not measured by this run, and not measurable by it: step 6 is the HARD RULE on a real installation at the PREVIOUS version — 'brew upgrade', then 'fugaro update' ALONE, with 'fugaro doctor' clean afterwards. This check measures the failure classes; that step is the release's own and owes you no second command. A step a release needs there that the plan did not name is a defect in the release, not in this check"
}

# ------------------------------------------------------------- the judgement
# row_verdict KEY — the verdict this run recorded for KEY, empty when it recorded
# none.
row_verdict() { # row_verdict KEY
  sed -n "s/^$1|\([A-Z]*\)|.*/\1/p" "$FACTS_FILE" | head -1
}

# step_verdict: the rows of steps 1 to 4 decide, and nothing else does — except
# step 5's read when you passed one: that run is yours to make and this check
# never fakes it, but once you hand the check its ref the class it carries IS a
# criterion, and a class other than the one the plan expects is a finding like
# any other. Without --step5-run the row is the prompt it is, and INFO decides
# nothing. Step 6 is entirely yours.
step_verdict() {
  local key failed=() list=""
  for key in launch settled class agree window target groups share totals digest old-record readonly; do
    [ "$(row_verdict "$key")" = FAIL ] && failed+=("$key")
  done
  if [ -n "$STEP5_RUN" ] && [ "$(row_verdict step5)" = FAIL ]; then
    failed+=(step5)
  fi
  REACHED_END=yes
  if [ ${#failed[@]} -gt 0 ]; then
    list=$(printf '%s, ' "${failed[@]}")
    list=${list%, }
    owner "the classes do NOT hold: the block's FAILed rows are $list, and each one says what it read instead"
    exit 1
  fi
  if [ "$CANCEL_OK" = no ]; then
    owner "the classes were read, and a run this check launched may still be going: read the block's 'cancelled' row"
    exit 1
  fi
  owner "every criterion of steps 1 to 4 held: the run's class is the one its cause deserves and the listing and the diagnosis agree on it, the window's groups add up with the shares the runs give, the week's report carries its Failures table and counts the old record's own class, and the old record's bytes are unchanged by three reads of it. Steps 5 and 6 are yours: the block's rows say what is still to run"
  exit 0
}

# print_plan: the guards and every command a real run would make, in order, and
# the Restore's. Printed by --dry-run INSTEAD of running anything: this check's
# own test pins that a dry run makes no gcloud call and no fugaro call at all, so
# the plan names the shape of each command and never makes one.
print_plan() {
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [local, writes] git -C $REPO hash-object -w, read-tree, update-index, write-tree and commit-tree into an index of this run's own (GIT_INDEX_FILE), with ONE file replaced — fugaro.yaml plus the unknown key chk350_invalid_key — then git update-ref refs/heads/$BRANCH and git push --no-verify origin refs/heads/$BRANCH: your work tree is never switched and your branch never moves"
  say "  [network, writes] fugaro run --repo $REPO --ref $BRANCH --json '<the task below>' — ONE run of ${REPO_NAME:-this repository}, refused at bootstrap: no model is called and nothing is billed, and it does start one job execution"
  say "  [network, reads] gcloud storage cat gs://$B/runs/<the slug fugaro run answers>/<the run>/result.json every ${POLL_SECONDS}s until it is terminal (up to ${RUN_TIMEOUT}s; a run still going is cancelled with 'fugaro cancel <run> --now')"
  say "  [network, reads] gcloud storage ls -l --recursive gs://$B/runs/<the slug>/ — metadata only, and the OLD records are chosen from it: minted inside --since $SINCE_USED, before the week's last day ${WEEK_TO:-?}, nearest the week first"
  say "  [network, reads] gcloud storage cat of up to $CANDIDATES of those records, and gcloud storage ls -l of the one that is the old record — the step that takes the hash and the bucket's own line BEFORE the first fugaro read of the window, so a class written back by a reader could not be in them"
  say "  [network, reads] fugaro runs ls --repo ${REPO_NAME:-<the repository>} --since $SINCE_USED --json — the window's rows, and the row of the run above"
  say "  [network, reads] fugaro diagnose <the run> --json — the same class through the command a person reads a run with (step 1's agreement)"
  say "  [network, reads] fugaro runs ls --repo ${REPO_NAME:-<the repository>} --since $SINCE_USED --group-by failure_class --json — the groups, the shares and the totals of step 2"
  say "  [network, reads] fugaro diagnose <the old record's run> --json — the third read of the old record, which is what step 4 counts"
  say "  [network, reads] fugaro runs digest --period week --repo ${REPO_NAME:-<the repository>} --stdout, and again with --json — the week's Failures table and the summary block beside it (step 3)"
  say "  [network, reads] gcloud storage cat of the old record again, and gcloud storage ls -l of it: the same bytes and the same line as before the reads above (step 4)"
  say "  [network, reads] fugaro diagnose <--step5-run> --json, ONLY when you passed one: the refutation run is yours to make and this check never fakes it"
  say "and its Restore (also from the exit trap) would run:"
  say "  [network, deletes] git -C $REPO push --no-verify origin --delete $BRANCH, then git update-ref -d refs/heads/$BRANCH, then git ls-remote --heads origin $BRANCH and git rev-parse of the local ref, read back as gone"
  say "  [network, reads, on giving up or on Ctrl-C] fugaro cancel <the run> --now --json"
  say "The run's own record is LEFT in the runs bucket (it is what step 1 read), and nothing else of this check's survives it."
  say "Nothing was pushed, launched, cancelled or deleted: this dry run made no gcloud call and no fugaro call at all."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached — and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project gcp-project bucket repo belong agent-session xtrace branch
  run settled class agree window target groups share totals digest old-record
  readonly step5 step6 restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 350 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
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
    case "$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 350 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and made nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable, a
  # bad substitution), and a block that then read as a finished run would offer a
  # FAILed class row as an answer about the classes when the reads that would have
  # filled it never ran. Any such ending is an abort, and 2 says so.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the failure classes"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  # The Restore's own row, whenever it recorded none: an exit 0 beside a branch
  # still on the origin would read as a pass.
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is no reference to delete"
  elif ! printf '%s' "$facts" | grep -q '^restore|'; then
    facts="${facts}"$'\n'"restore|FAIL|the Restore recorded nothing, so the branch it made may still be on $REPO's origin: delete it by hand with 'git -C $REPO push origin --delete $BRANCH' and 'git -C $REPO branch -D $BRANCH'"
  fi
  # The verdict is taken from the facts, not from the exit path: an exit 0 beside
  # a FAILed restore row, or beside a Restore that did not finish, would read as a
  # pass.
  if [ "$status" -eq 0 ] && printf '%s\n' "$facts" | grep -qE '^restore\|FAIL\|'; then
    status=1
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — remove it yourself"
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: cancelling what is going, printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check350.sh --repo <a sandbox repository checkout> --project <the installation's project> --bucket <its runs bucket> [--gcp-project <id>] [--since <Nd>] [--step5-run <run id>] [--dry-run]"

# TASK_TEXT is the task the one run is launched with. It is a line about what the
# run is FOR, so an owner reading the run list later knows why it is there, and
# it is deliberately not a plan task: this run is expected to fail.
TASK_TEXT="Check 350: fail at bootstrap so the failure classes can be read (its ref carries a fugaro.yaml with an unknown key). Nothing is expected of this run but its own record."

RUN_TIMEOUT=${CHECK350_RUN_TIMEOUT_SECONDS:-600}
POLL_SECONDS=${CHECK350_POLL_SECONDS:-10}
CANDIDATES=${CHECK350_CANDIDATES:-8}

parse_args() {
  P=""
  B=""
  GCP=""
  REPO=""
  SINCE=""
  STEP5_RUN=""
  DRY_RUN=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --project)
      [ $# -ge 2 ] || die "--project needs a project name. usage: $USAGE"
      P=$2
      shift 2
      ;;
    --bucket)
      [ $# -ge 2 ] || die "--bucket needs a bucket name (with or without gs://). usage: $USAGE"
      B=$2
      shift 2
      ;;
    --gcp-project)
      [ $# -ge 2 ] || die "--gcp-project needs a GCP project id. usage: $USAGE"
      GCP=$2
      shift 2
      ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs a path. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --since)
      [ $# -ge 2 ] || die "--since needs a window. usage: $USAGE"
      SINCE=$2
      shift 2
      ;;
    --step5-run)
      [ $# -ge 2 ] || die "--step5-run needs a run id (or <slug>/<run id>). usage: $USAGE"
      STEP5_RUN=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  case "$B" in gs://*) B=${B#gs://} ;; esac
  case "$B" in */) B=${B%/} ;; esac
  # --repo is made absolute here: this check's own `git -C` calls and the CLI's
  # commands run with it, and a relative path would mean two different
  # directories once a step changed the working directory.
  if [ -n "$REPO" ]; then
    case "$REPO" in
    /*) ;;
    *) REPO=$(cd "$REPO" 2>/dev/null && pwd -P) || die "--repo '$REPO' is not a directory that can be entered" ;;
    esac
  fi
  [ -n "$REPO" ] || die "--repo is required, and it has to be a SANDBOX repository's checkout: the run this check launches is launched from it, and the throwaway reference it is cut from is made in it. usage: $USAGE"
  [ -n "$P" ] || die "--project is required, and it has to be the SANDBOX installation's project: this check launches a run in it and never reads the project from a config that may name another. usage: $USAGE"
  [ -n "$B" ] || die "--bucket is required, and it has to be that installation's runs bucket: the record the read-only step compares is read from it. usage: $USAGE"
  [ -n "$GCP" ] || GCP=$P
  case "$SINCE" in
  "") ;;
  [0-9]*d | [0-9]*h | [0-9]*m) ;;
  *) die "--since '$SINCE' is not a window this check can also read the bucket with: use a positive number of days (7d) or a Go duration (36h). usage: $USAGE" ;;
  esac
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  guard_xtrace
  guard_agent_session
  guard_belong
  guard_repo
  compute_window
  fact project PASS "$P (typed by the owner, and the Fugaro project every command this check makes carries)"
  fact gcp-project PASS "$GCP (the GCP project the runs bucket belongs to)"
  fact bucket PASS "$B (typed by the owner: the runs bucket the records of steps 1, 3 and 4 are read from, and READ ONLY — this check writes no object)"
  fact week INFO "the window steps 1 and 2 are listed with is --since $SINCE_USED, and the week steps 3 and 4 are about is ${WEEK_FROM}..${WEEK_LAST} — the week the digest reports: the one before the week today falls in, Monday to Sunday, UTC, with a run counted in the period it finishes"
  if [ -n "$DRY_RUN" ]; then
    fact dry-run PASS "guards and the plan only: nothing was pushed, launched, cancelled or deleted, and no gcloud call and no fugaro call was made at all"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the arguments, the guards, the window and the dry-run row itself. It
    # reached its end — the plan IS the end of a dry run — so the exit trap must
    # not read it as a run that stopped on an error.
    REACHED_END=yes
    BLOCK_KEYS=(project gcp-project bucket repo belong agent-session xtrace week dry-run restore)
    return 0
  fi
  write_reader
  step_branch
  step_launch
  step_settle
  step_class
  step_target
  step_rows
  step_agree
  step_groups
  step_target_class
  step_digest
  step_readonly
  step_step5
  step_step6
  step_verdict
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
