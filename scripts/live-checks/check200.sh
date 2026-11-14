#!/usr/bin/env bash
# Check 200 — the first tagged release: the governance stage timeout halts, the
# image's edition carrier says ee, and the digest runs from an untagged build.
#
# Plan task ee-reorg E3.4 writes this check; what it settles is the three facts
# the 0.13.0 tag is the release of, on the sandbox installation with the
# released CLI and the images refreshed to it:
#
#   1. A run whose `governance.stage_timeout.implement` is tiny HALTS at that
#      ceiling with `halt.reason` `stage_budget`, `halt.detail` `time`,
#      `failure_class` `halted_governance` and a checkpoint report — the T7.6
#      tripwire end to end, read off the run's own record rather than off a
#      config the check itself wrote. The ceiling is a governance key, and the
#      only layers that reach a run are the default branch's `fugaro.yaml` and
#      the one the run is cut from (`internal/runner/policy.go`): this check
#      writes it into the sandbox checkout's `fugaro.yaml` on a THROWAWAY
#      BRANCH it pushes for the one run and deletes again, so nothing of the
#      owner's own branch and nothing of the sandbox's configuration is left
#      changed. A value written to the local config or the local project layer
#      would reach no runner at all, and the CLI writes no member of that
#      object either (a repository's `governance.stage_timeout` is one row, and
#      `--add-entry` takes no entry of it), so the check writes the three lines
#      itself and reads the edit back with `git diff`.
#   2. The image the run itself ran carries edition `ee`: the build record at
#      `builds/<slug>/<workflow>/image.json` says so (R3's carrier). This is
#      deliberately NOT `fugaro version`, whose text does not change between
#      the editions — a check that read the version string would pass on a core
#      binary and say nothing about the image.
#   3. `fugaro runs digest --period day` runs from a SOURCE BUILD WITHOUT THE
#      TAG: the digest is core (G5, T11), and a build that cannot report is the
#      build the fact is about. The untagged binary is built here, from the
#      source checkout (`--source`, this script's own tree by default), and
#      proved untagged before it is used: `fugaro report` is a paid name, so an
#      untagged build answers it with the core stub — "report is part of Fugaro
#      Subscription Edition; this build does not include it" — and a build that
#      runs it has the tag and would prove nothing.
#
# Every fact above is a reading of something the release itself wrote, so the
# check is scriptable and this script is the whole of it. Nothing about Check
# 200 is a person's step: no browser, no second person, no device.
#
# The register's row and section are docs/gcp-live-checklist.md, "Check 200".
#
# Usage:      bash scripts/live-checks/check200.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check200.sh --sandbox <path> --source <a fugaro source checkout> --yes
#             bash scripts/live-checks/check200.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check200.sh --help
#             `--sandbox` is the checkout of a repository of the SANDBOX project:
#             the run is launched from it, its fugaro.yaml is where the tiny
#             stage timeout is written, and its project is read from
#             `fugaro doctor --json` rather than guessed. `--source` is a Fugaro
#             source checkout, and it defaults to the tree this script lives in.
#             `--dry-run` prints every command a real run would make and runs
#             nothing at all: no fugaro call, no gcloud call, no git call.
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), git (the throwaway branch), go (the untagged build), gcloud
#             (authenticated as YOU, and able to read the runs bucket) and
#             fugaro (the installed release: the whole point is the build you
#             have). Nothing is installed and nothing is fetched; if one of
#             those is missing the script stops with one line naming it. The go
#             build reads the source checkout's module cache: a machine whose
#             cache is cold fetches modules, which is the one network access
#             this check makes that is not a read of the sandbox installation.
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project, an account, a region, a checkout
#             path or a repository name that names belong is refused by name,
#             and so is a coding agent's session: every command this check runs
#             is one you could type yourself.
# Creates:    one throwaway branch on the sandbox repository's origin
#             (`chk200-<hex>`, absent before the run) carrying one commit that
#             sets `governance.stage_timeout.implement`; ONE sandbox run cut
#             from that branch, which is billable, and the pull request that run
#             opens. On disk: one scratch directory, removed at the end, holding
#             the untagged binary, the commands' output and the task file.
# Costs:      ONE sandbox run and what its task costs — the model spend of the
#             implement stage up to the ceiling, which is short by construction:
#             the run is launched to be halted. Nothing else here is billable:
#             the doctor, the runs list, the record read, the build record read
#             and the digest are reads or a local build. `--yes` is required,
#             and CHECK200_STAGE_TIMEOUT is the ceiling the run is launched
#             under.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check200_test.sh) drives it against fake tools only.
# Cleanup:    always, including on a Ctrl-C or on a failed assertion. The
#             throwaway branch is deleted locally and on origin, the checkout is
#             put back on the branch it was on, and the fugaro.yaml the commit
#             carried is restored with it — the commit lives only on the branch
#             that is deleted, so the branch you were on never moved. The
#             Restore reads all of that back and says what it found, and a
#             deletion it could not confirm is a FAILed row rather than a silent
#             pass. The scratch directory is removed with the run. The run's own
#             pull request and its `fugaro/<run-id>` branch are NOT closed by
#             this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 200 RESULT,
#             then CHECK 200 RESULT END (exit N). Exit 0: every criterion held
#             AND the Restore confirmed what it deleted. 1: a criterion did not
#             hold — among them a stage timeout the run did not halt on — or the
#             Restore could not confirm what it deleted, which the block's
#             `restore` row names and which you then have to finish by hand. 2:
#             the check could not measure — a tool is missing, the checkout is
#             not one, a `--json` document could not be read, the build record
#             is absent, the untagged build could not be made, or the run did
#             not end. A key the run never reached reads `FAIL — not reached`.
#
# No credential is handled anywhere in it as a value: every call is made as your
# own signed-in identity, the objects it reads are the installation's own
# records, and nothing it writes is a secret. So a `bash -x` of this script
# shows no secret-shaped value, and none is printed, pasted or traced.
#
# Knobs (all CHECK200_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, SOURCE, YES, REPO
# (the project configuration's key for the sandbox repository; the checkout
# directory's name by default), TASK, STAGE_TIMEOUT (1m: the ceiling the run is
# launched under, and the value written into the throwaway branch),
# RELEASE (0.13.0: the first tagged release), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180),
# BUILD_TIMEOUT_SECONDS (900), LAUNCH_TIMEOUT_SECONDS (600),
# DOCTOR_TIMEOUT_SECONDS (300). None of them can point the check at another
# project or another repository: the project comes from the checkout itself.
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

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
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
  command -v bash >/dev/null 2>&1 || die "Check 200 needs bash"
  local t
  for t in mktemp date python3 git go gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 200 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk200-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
SLUG=""
WORKFLOW=""
SETTLED=no
CORE_OK=no
# CONFIG_OK is set by the one branch of step_config that wrote the tiny ceiling
# onto the throwaway branch. step_run reads it and nothing else: a run launched
# without the ceiling would spend the sandbox's money on a run that has nothing
# to halt on, and the row above is already the finding.
CONFIG_OK=no
# CEILING_WHY is why write_ceiling would not write, in the words the config row
# reports: the refusal is the row's own sentence rather than a second copy of
# it at the call site.
CEILING_WHY=""
ORIG_REF=""
BRANCH=""
BRANCH_MADE=no
BRANCH_PUSHED=no
COMMITTED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk200.XXXXXXXX") || die "Check 200 cannot make a scratch directory"
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
    say "CHECK200_KEEP_SCRATCH is set: $SCRATCH is kept"
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
    print("project=%s" % flat((doc.get("project") or {}).get("gcp_project")))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("run=%s" % (doc.get("run") or ""))
    print("branch=%s" % (doc.get("branch") or ""))
    print("ref=%s" % (doc.get("ref") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
            row.get("run") or "",
        ))
elif what == "record":
    halt = doc.get("halt") or {}
    cp = doc.get("checkpoint") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("workflow=%s" % (doc.get("workflow") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("failure_class=%s" % (doc.get("failure_class") or ""))
    print("cost_usd=%s" % (doc.get("cost_usd") or 0))
    print("halt_reason=%s" % (halt.get("reason") or ""))
    print("halt_detail=%s" % (halt.get("detail") or ""))
    print("halt_scope=%s" % (halt.get("scope") or ""))
    print("halt_at=%s" % (halt.get("at") or ""))
    print("checkpoint_present=%s" % yes(doc.get("checkpoint") is not None))
    print("cp_trigger=%s" % (cp.get("trigger") or ""))
    print("cp_why_over=%s" % flat(cp.get("why_over")))
    print("cp_done=%s" % flat(cp.get("done")))
    print("cp_next=%s" % flat(cp.get("next")))
    print("cp_hard=%s" % flat(cp.get("hard_instructions")))
    print("cp_estimate_usd=%s" % (cp.get("estimate_usd") or 0))
elif what == "image":
    print("repo=%s" % (doc.get("repo") or ""))
    print("workflow=%s" % (doc.get("workflow") or ""))
    print("edition=%s" % (doc.get("edition") or ""))
    print("fugaro_version=%s" % flat(doc.get("fugaro_version")))
    print("build_id=%s" % flat(doc.get("build_id")))
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

row_field() { # row_field COLUMN — one column of the first `row\t...` line
  awk -F'\t' -v col="$1" '$1 == "row" { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null || true
}

run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check200.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused — the run it launches, and the
# branch it pushes. Refusing here rather than letting each command refuse one at
# a time is what makes the block say why: a session that ran this far would
# report the refusals as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 200 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_belong: the installation named `belong` is the owner's own, and this
# check launches a run, pushes a branch and writes a setting — so the name is
# refused in every name it holds before anything is created. It is a substring
# test on purpose: a name that merely contains it is refused too, and the
# message says which one matched. The sandbox checkout's own path is one of the
# names, because the run is launched from it.
guard_belong() {
  local name
  for name in "${ACCOUNT:-}" "${PROJECT:-}" "${REGION:-}" "$SANDBOX" "$REPO"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation: Check 200 launches a run, pushes a throwaway branch and writes a setting, and it is a SANDBOX check"
      stop "the name '$name' matches the installation 'belong', which this check must never touch"
      ;;
    esac
  done
  fact belong PASS "neither the account, the project, the region, the checkout path nor the repository name names the owner's own installation (nothing typed contains 'belong')"
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

# step_release: the gate the check carries because two of its three facts are
# the 0.13.0 tag's own. `fugaro version` is the release the owner reads, and the
# digest command is the feature probe: it is core (G5, T11) and the check's
# third fact is that it runs, so a build without it stops here rather than
# measuring a run and two reads that say nothing about the release. Both probes
# are offline.
step_release() {
  local v feature=no
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" runs digest --help >/dev/null 2>&1; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no 'runs digest': 'fugaro runs digest --help' does not answer, so the third fact of this check — the digest running from an untagged build — cannot be measured here, and neither can the run below be read as 0.13.0's. Check 200 needs release ${RELEASE}, the first tagged release. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no 'runs digest': this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and it holds 'runs digest': this build carries the work the three facts below are about"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the first tagged release: the digest exists in this build only if the feature probe above is right about a version string that says otherwise, and a run measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro runs digest --help' answers, so this build carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  fact bound PASS "ONE sandbox run under a ${STAGE_TIMEOUT} implement ceiling — a run launched to be halted, so the model spend is the implement stage up to that ceiling and no more — plus one throwaway branch pushed to the sandbox repository and deleted again. Every other call this check makes is a read (doctor, runs ls, the record, the build record, the digest) or a local build. --yes is required to launch it"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the run and its record are that project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the run this check launches is launched from one"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get("gcp_project") or "")' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$(printf '%s' "$PROJECT" | tr '[:upper:]' '[:lower:]')" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check launches from this checkout, writes its throwaway branch here and reads the record in this project's bucket"
}

# step_source: the untagged build the third fact is about. It is built from a
# source checkout with no build tag, and then PROVED untagged before it is used:
# `report` is a paid name, so an untagged build answers it with the core stub
# (D522) and a build that runs the command has the tag. The proof is what makes
# the digest row a reading of a core build rather than of whatever binary
# happened to be on PATH.
step_source() {
  if [ ! -f "$SOURCE/go.mod" ] || [ ! -d "$SOURCE/cmd/fugaro" ]; then
    unmeasured source "--source is not a Fugaro source checkout: $SOURCE has no go.mod and no cmd/fugaro, and this check builds the untagged binary it reads the digest from rather than taking one on trust. Pass --source <a fugaro source checkout>"
    return 0
  fi
  say "building the untagged binary from ${SOURCE}: go build -o $SCRATCH/fugaro-core ./cmd/fugaro"
  run_bounded "$BUILD_TIMEOUT" build "$SOURCE" go build -o "$SCRATCH/fugaro-core" ./cmd/fugaro
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured source "go build did not finish inside ${BUILD_TIMEOUT}s and was ended: the untagged binary is what the digest row runs, and there is none. What go printed is in $SCRATCH/build.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured source "go build exited ${BOUNDED_STATUS} in ${SOURCE}: the untagged binary is what the digest row runs, and a tree that does not build cannot give it. What go printed is in $SCRATCH/build.err"
    return 0
  fi
  if [ ! -x "$SCRATCH/fugaro-core" ]; then
    unmeasured source "go build exited 0 and left no binary at $SCRATCH/fugaro-core: what go printed is in $SCRATCH/build.out"
    return 0
  fi
  say "proving it is untagged: fugaro report answers the core stub"
  run_bounded "$CLI_TIMEOUT" core "$SANDBOX" "$SCRATCH/fugaro-core" report
  # The stub is an error, so this call is EXPECTED to exit non-zero: what says
  # the build is untagged is the sentence, never the status.
  local text
  text=$(cat "$SCRATCH/core.out" "$SCRATCH/core.err" 2>/dev/null || true)
  case "$text" in
  *"part of Fugaro Subscription Edition"*)
    CORE_OK=yes
    fact source PASS "the binary built from ${SOURCE} answers 'fugaro report' with the core stub (\"$(printf '%s' "$text" | head -n 1)\"): it was built without the ee tag, so the digest row below is a reading of a build that is not the Subscription Edition"
    ;;
  *)
    fact source FAIL "the binary built from ${SOURCE} did not answer 'fugaro report' with the core stub (it printed: $(printf '%s' "$text" | head -n 1)). Either it carries the ee tag — and then the digest row would prove nothing about an untagged build — or the stub has changed. What it printed is in $SCRATCH/core.out and $SCRATCH/core.err"
    ;;
  esac
}

# write_ceiling adds the ceiling to a checkout's fugaro.yaml. It is written
# here rather than with a `fugaro settings set` because the CLI answers no
# write of it: a repository's `governance.stage_timeout` is one object-valued
# row, so a scalar set on it is refused when the file is read back; the rows
# that name a member of it are the project layer's
# (`layer.governance.stage_timeout.*`), which reaches no run; and
# settingsEntryBlocks takes no entry of the object
# (internal/cli/settings_set.go), which is what `--add-entry` would need.
#
# A file that already carries a `governance:` block is left alone: its members
# are the owner's own, and a second `governance:` key — or a `stage_timeout:`
# beside one that is already there — is a file the strict decoder refuses at
# the run's bootstrap, which would read as a finding about the release.
write_ceiling() {
  local f=$1
  if grep -qE '^governance:' "$f"; then
    CEILING_WHY="${f} already carries a governance: block, and this check adds one of its own rather than edit yours: the ceiling has to sit at governance.stage_timeout.implement on the ref the run is cut from, and that block is your work. Run this check from a checkout of a sandbox repository whose fugaro.yaml writes no governance: block"
    return 1
  fi
  if ! printf '\ngovernance:\n  stage_timeout:\n    implement: %s\n' "$STAGE_TIMEOUT" >>"$f"; then
    CEILING_WHY="${f} could not be written, so the run below would be launched with no ceiling to halt it: the implement stage would spend the sandbox's budget up to its own end"
    return 1
  fi
}

# step_config: the tiny ceiling, written into the one layer that reaches a
# runner. The run is cut from the branch this check pushes, and the runner
# merges the default branch's file with the run's own ref, tightening only
# (internal/runner/policy.go) — so the value has to be in the checkout's
# fugaro.yaml on a ref origin has. A local-config or local-project-layer write
# would reach no runner at all, and a project-layer publish would reach every
# repository of the installation, which is not a sandbox check's to change.
step_config() {
  local dirty rc=0
  dirty=$(git -C "$SANDBOX" status --porcelain -- fugaro.yaml 2>"$SCRATCH/git.err") || rc=$?
  if [ "$rc" != 0 ]; then
    unmeasured config "git could not read the checkout at $SANDBOX (exit ${rc}): this check commits its own change to that repository's fugaro.yaml and then puts the file back, so it has to know the file was clean before it wrote. What git printed is in $SCRATCH/git.err"
    return 0
  fi
  if [ -n "$dirty" ]; then
    unmeasured config "${SANDBOX}/fugaro.yaml has uncommitted changes ('${dirty}'): this check writes that file, commits it on a throwaway branch and restores it from the branch it was on, so a file with your own work in it could not be put back. Commit or stash it and run this check again"
    return 0
  fi
  ORIG_REF=$(git -C "$SANDBOX" symbolic-ref --short -q HEAD 2>/dev/null || true)
  if [ -z "$ORIG_REF" ]; then
    unmeasured config "the checkout at $SANDBOX is on no branch (a detached HEAD): this check leaves it on the branch it found it on, and there is none to return to. Check out the branch you work on and run this check again"
    return 0
  fi
  BRANCH="chk200-${BRANCH_HEX}"
  say "writing the ceiling on a throwaway branch: git checkout -b ${BRANCH}, then governance.stage_timeout.implement = ${STAGE_TIMEOUT} in fugaro.yaml"
  run_bounded "$CLI_TIMEOUT" branch "$SANDBOX" git checkout -b "$BRANCH"
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured config "git checkout -b ${BRANCH} exited ${BOUNDED_STATUS} in ${SANDBOX}: the ceiling has to be on a ref origin has, and this is the branch that carries it. What git printed is in $SCRATCH/branch.err"
    return 0
  fi
  BRANCH_MADE=yes
  if ! write_ceiling "$SANDBOX/fugaro.yaml"; then
    unmeasured config "$CEILING_WHY"
    return 0
  fi
  local diff
  diff=$(git -C "$SANDBOX" diff -- fugaro.yaml 2>/dev/null || true)
  printf '%s\n' "$diff" >"$SCRATCH/config.diff"
  if ! printf '%s' "$diff" | grep -q 'stage_timeout'; then
    fact config FAIL "the edit left no stage_timeout in ${SANDBOX}/fugaro.yaml (git diff names none): the ceiling the run is launched under is that edit, and the halt below is a reading of it. What git diff printed is in $SCRATCH/config.diff"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" commit "$SANDBOX" git add fugaro.yaml
  run_bounded "$CLI_TIMEOUT" commit "$SANDBOX" git commit -m "Check 200: a ${STAGE_TIMEOUT} implement ceiling, for one run" -- fugaro.yaml
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact config FAIL "git commit exited ${BOUNDED_STATUS} in ${SANDBOX}: the ceiling has to be a commit on the branch the run is cut from. What git printed is in $SCRATCH/commit.err"
    return 0
  fi
  COMMITTED=yes
  run_bounded "$CLI_TIMEOUT" push "$SANDBOX" git push --quiet origin "refs/heads/${BRANCH}:refs/heads/${BRANCH}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact config FAIL "git push of ${BRANCH} exited ${BOUNDED_STATUS} in ${SANDBOX}: a run is cut from a ref origin has, and this is the ref that carries the ceiling. What git printed is in $SCRATCH/push.err"
    return 0
  fi
  BRANCH_PUSHED=yes
  CONFIG_OK=yes
  fact config PASS "governance.stage_timeout.implement = ${STAGE_TIMEOUT} is committed on ${BRANCH} and pushed to origin (written by this check itself and read back with git diff, since the CLI writes no member of that object: see write_ceiling): the run below is cut from that branch, so the runner's own merge of the default branch's file with this one is the ceiling it halts on (policy.Merge tightens only)"
}

# step_run: the run, launched from the branch that carries the ceiling. The task
# is deliberately longer than the ceiling: a task that could finish inside it
# would end succeeded and the halt row below would be a reading of nothing.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  if [ "$CONFIG_OK" != yes ]; then
    unmeasured run-launched "nothing was launched: the config row above did not put the ${STAGE_TIMEOUT} ceiling on a ref origin has, and a run launched without it would spend the sandbox's money proving nothing. Settle that row and run this check again"
    return 0
  fi
  say "launching one run from ${SANDBOX}, cut from ${BRANCH} (a real sandbox run, launched to be halted)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" --ref "$BRANCH" --task-file "$SCRATCH/task.md" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the check needs a run to read a record from, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch is the step this check follows, and its own --json view is how the run is named. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  RUN_REF=$(field "$SCRATCH/launch.txt" run)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  SLUG=${RUN_REF%%/*}
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout, cut from ${BRANCH} with its ${STAGE_TIMEOUT} implement ceiling (status $(field "$SCRATCH/launch.txt" status)): the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end. A check that read a record while a run was
# still going would read the previous run's, and the record of a run that is
# still working is not the reading this check is after.
step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 1)"
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
    # The clock advances by the poll at least: a poll of 0 — the knob's own
    # test — would otherwise leave `waited` where it was and spin here forever.
    local step=$POLL_SECONDS
    [ "$step" -gt 0 ] || step=1
    waited=$((waited + step))
  done
  if [ "$ended" != true ]; then
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state the record below is written in"
}

# step_record: the record, read from the runs bucket as the owner would read it.
# The record's own run_id is what ties it to the run this check launched.
step_record() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record: a record read while a run is still working is whatever was there before it. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured result-json "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=${RUN_REF:-none}), so there was no key to read: the record of run ${RUN_ID} lives under the repository's slug, and the slug is what that row's ref carries"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where its halt and its checkpoint are written. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact result-json FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact result-json FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this run's"
    return 0
  fi
  WORKFLOW=$(field "$SCRATCH/record.txt" workflow)
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), outcome=$(field "$SCRATCH/record.txt" outcome), workflow=${WORKFLOW:-none}, cost_usd=$(field "$SCRATCH/record.txt" cost_usd)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_halt: the first fact. The run was launched under a ${STAGE_TIMEOUT}
# implement ceiling, so the run's own halt is the tripwire having fired: the
# reason is `stage_budget` and the detail is `time`, which is what T7.6's
# stage-timeout half writes (ee/governance/runner.go, haltReasonText). A run
# that ended some other way — succeeded, or halted on money — is a FAIL with its
# own ending in the row.
step_halt() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured halt "there is no record to read the halt from: the row above says why"
    return 0
  fi
  local status reason detail scope cls
  status=$(field "$SCRATCH/record.txt" status)
  reason=$(field "$SCRATCH/record.txt" halt_reason)
  detail=$(field "$SCRATCH/record.txt" halt_detail)
  scope=$(field "$SCRATCH/record.txt" halt_scope)
  cls=$(field "$SCRATCH/record.txt" failure_class)
  if [ "$status" != halted ]; then
    fact halt FAIL "the run ended ${status} rather than halted, and it was launched with governance.stage_timeout.implement = ${STAGE_TIMEOUT}: a run under that ceiling is the reading this check is for, and one that ended another way says either that the ceiling did not reach the runner or that the task finished inside it. Its record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$reason" != stage_budget ] || [ "$detail" != time ]; then
    fact halt FAIL "the run halted with reason=${reason:-none} detail=${detail:-none}, and this check launched it under a ${STAGE_TIMEOUT} implement ceiling: the stage-timeout half of the tripwire writes reason=stage_budget detail=time (ee/governance/runner.go). Another reason is another bound having fired, and the ceiling this check set is then not what stopped the run. Its record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$cls" != halted_governance ]; then
    fact halt FAIL "the run halted on reason=${reason} detail=${detail} and its failure_class is ${cls:-none} rather than halted_governance: the class is what a reader of the record judges the ending by, and a governance halt that is not classified as one is a second finding. Its record is in $SCRATCH/record.out"
    return 0
  fi
  fact halt PASS "the run halted with reason=${reason} detail=${detail} (scope=${scope:-none}) and failure_class=${cls}: the ${STAGE_TIMEOUT} implement ceiling this check wrote into ${BRANCH} reached the runner and stopped the run at the stage's own time bound"
}

# step_checkpoint: the second half of the first fact. A governance halt leaves a
# checkpoint report (T6.1, D329, G7): the trigger is the halt's reason, why_over
# is the runner's own words for it, and the figure is the model spend the record
# had reached. A stage timeout gets no checkpoint CALL — its Done, Next and
# HardInstructions are empty by design (runstore.Checkpoint) — so those three
# are read and recorded, never graded.
step_checkpoint() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured checkpoint "there is no record to read the checkpoint from: the row above says why"
    return 0
  fi
  local present trigger why est done next hard
  present=$(field "$SCRATCH/record.txt" checkpoint_present)
  trigger=$(field "$SCRATCH/record.txt" cp_trigger)
  why=$(field "$SCRATCH/record.txt" cp_why_over)
  est=$(field "$SCRATCH/record.txt" cp_estimate_usd)
  done=$(field "$SCRATCH/record.txt" cp_done)
  next=$(field "$SCRATCH/record.txt" cp_next)
  hard=$(field "$SCRATCH/record.txt" cp_hard)
  if [ "$present" != true ]; then
    fact checkpoint FAIL "the run halted on a governance reason and its record carries no checkpoint block: the report is what a person deciding whether to continue the run reads, and a governance halt without one is the failure this row is for. Its record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$trigger" != stage_budget ]; then
    fact checkpoint FAIL "the checkpoint's trigger is ${trigger:-none} rather than the halt's own reason stage_budget: the trigger is the halt's reason written into the report, and a report that names another stop is a report of another run's ending. Its record is in $SCRATCH/record.out"
    return 0
  fi
  if [ "$why" != "a stage passed its own time bound" ]; then
    fact checkpoint FAIL "the checkpoint's why_over is '${why:-none}' and a stage timeout's is 'a stage passed its own time bound' (internal/runner/halt.go, haltReasonText): the sentence is the runner's own words for the bound that fired, and the time half is what this check launched the run for. Its record is in $SCRATCH/record.out"
    return 0
  fi
  fact checkpoint PASS "the checkpoint report reads trigger=${trigger}, why_over='${why}', estimate_usd=${est:-0} (done='${done}', next='${next}', hard_instructions='${hard}', empty as a stage timeout's are): the halt left the report a person continues from, with the money the run had reached"
}

# step_edition: the second fact, and the one R3 moved off `fugaro version`. The
# build record of the workflow THIS RUN RAN is the carrier: the record the
# image's own render step wrote, read from the runs bucket. The workflow comes
# from the run's record rather than from a guess, so the record read here is the
# one of the image that halted the run.
step_edition() {
  if [ -z "$SLUG" ] || [ -z "$WORKFLOW" ]; then
    unmeasured edition-carrier "there is no slug or workflow to name the build record with (slug=${SLUG:-none}, workflow=${WORKFLOW:-none}): the record lives at builds/<slug>/<workflow>/image.json, and the row above says why this check has neither"
    return 0
  fi
  local key="builds/${SLUG}/${WORKFLOW}/image.json"
  say "reading the image's edition carrier: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" image "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured edition-carrier "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the build record is where the edition of the image that ran this run is written (R3), and a check that read the version string instead would pass on a core binary. What gcloud printed is in $SCRATCH/image.err"
    return 0
  fi
  if ! read_json image "$SCRATCH/image.out" "$SCRATCH/image.txt"; then
    unmeasured edition-carrier "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/image.out"
    return 0
  fi
  local edition version
  edition=$(field "$SCRATCH/image.txt" edition)
  version=$(field "$SCRATCH/image.txt" fugaro_version)
  if [ "$edition" != ee ]; then
    fact edition-carrier FAIL "the build record for ${SLUG}'s ${WORKFLOW} workflow says edition='${edition:-none}' (fugaro_version=${version:-none}) and this check is the 0.13.0 tag's: from that release the build's render step writes the edition of the binary that rendered it (R3), and an image whose record says core is an image whose runner holds no governance to halt on. What the record holds is in $SCRATCH/image.out"
    return 0
  fi
  fact edition-carrier PASS "the build record for ${SLUG}'s ${WORKFLOW} workflow — the image the run above ran — says edition=ee (fugaro_version=${version:-none}): the carrier R3 writes says the image is the Subscription Edition's, which is what 'fugaro version' cannot say"
}

# step_digest: the third fact. The digest is core (G5, T11), so a build without
# the ee tag runs it — and the build this check uses is the one it proved
# untagged above. `--stdout` prints the report instead of writing it, so the
# check reads a report and writes nothing into the owner's digest folder.
step_digest() {
  if [ "$CORE_OK" != yes ]; then
    unmeasured digest-untagged "there is no untagged build to run the digest from: the source row above says why, and a digest run by the installed binary would be a reading of the tagged build, which is the opposite of this fact"
    return 0
  fi
  say "running the digest from the untagged build: fugaro-core runs digest --period day --stdout"
  run_bounded "$CLI_TIMEOUT" digest "$SANDBOX" "$SCRATCH/fugaro-core" runs digest --period day --stdout
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact digest-untagged FAIL "'fugaro runs digest --period day --stdout' exited ${BOUNDED_STATUS} in a build without the ee tag: the digest is core (G5, T11) and a core build that cannot run it is the fact this row is for. A refusal naming the Subscription Edition would say the command is paid after all. What it printed is in $SCRATCH/digest.out and $SCRATCH/digest.err"
    return 0
  fi
  local lines
  lines=$(wc -l <"$SCRATCH/digest.out" 2>/dev/null || echo 0)
  if [ "$lines" -lt 1 ] || [ ! -s "$SCRATCH/digest.out" ]; then
    fact digest-untagged FAIL "'fugaro runs digest --period day --stdout' exited 0 in the untagged build and printed nothing: the report is what the command is for, and an empty one says the command answered without running. What it printed is in $SCRATCH/digest.out"
    return 0
  fi
  fact digest-untagged PASS "the untagged build runs 'fugaro runs digest --period day --stdout' (exit 0, ${lines} line(s), first line: \"$(head -n 1 "$SCRATCH/digest.out")\"): the digest is core, and it reads this repository's runs from a build that cannot report"
}

# ------------------------------------------------------------------ the Restore
restore() {
  RESTORE_VERDICT=PASS
  if [ -z "$SCRATCH" ]; then
    RESTORE_NOTE="the check stopped before it made a scratch directory: it had written nothing anywhere, so there was nothing to restore"
    return 0
  fi
  local notes="" found=""
  if [ "$BRANCH_MADE" = yes ]; then
    run_bounded "$CLI_TIMEOUT" restore "$SANDBOX" git checkout --quiet "$ORIG_REF" || true
    if [ "$BOUNDED_STATUS" != 0 ]; then
      RESTORE_VERDICT=FAIL
      notes="${notes} the checkout could not be put back on ${ORIG_REF} (git checkout exited ${BOUNDED_STATUS});"
    fi
    run_bounded "$CLI_TIMEOUT" restore "$SANDBOX" git branch -D "$BRANCH" || true
    if [ "$BOUNDED_STATUS" != 0 ]; then
      RESTORE_VERDICT=FAIL
      notes="${notes} the local branch ${BRANCH} could not be deleted;"
    fi
    if [ "$BRANCH_PUSHED" = yes ]; then
      run_bounded "$CLI_TIMEOUT" restore "$SANDBOX" git push --quiet origin --delete "$BRANCH" || true
      if [ "$BOUNDED_STATUS" != 0 ]; then
        RESTORE_VERDICT=FAIL
        notes="${notes} origin's ${BRANCH} could not be deleted;"
      fi
    fi
    # Read back: the local branch must be gone, origin must have no head by that
    # name, and the file the commit carried must be back as it was. A deletion
    # that did not go is a FAILed row rather than a silent pass, because the
    # branch this check made is on the owner's own repository.
    if git -C "$SANDBOX" rev-parse --verify --quiet "refs/heads/${BRANCH}" >/dev/null 2>&1; then
      RESTORE_VERDICT=FAIL
      notes="${notes} the local branch ${BRANCH} is still there;"
    fi
    if [ -n "$(git -C "$SANDBOX" ls-remote --heads origin "$BRANCH" 2>/dev/null || true)" ]; then
      RESTORE_VERDICT=FAIL
      notes="${notes} origin still answers a head named ${BRANCH};"
    fi
    if [ -n "$(git -C "$SANDBOX" status --porcelain -- fugaro.yaml 2>/dev/null || true)" ]; then
      RESTORE_VERDICT=FAIL
      notes="${notes} ${SANDBOX}/fugaro.yaml is not back as it was;"
    fi
    found=" the throwaway branch ${BRANCH} was made, and it is deleted locally and on origin with the commit it carried"
  else
    found=" no branch was made: nothing was written to the repository"
  fi
  local run_note
  if [ -n "$RUN_ID" ]; then
    run_note="The run ${RUN_ID} is a real sandbox run and it opened a pull request on ${LAUNCH_REPO:-the sandbox repository}: close that pull request and delete its fugaro/<run-id> branch yourself once you have read the run."
  else
    run_note="No run was launched, so this check has no pull request of its own to close."
  fi
  RESTORE_NOTE="this check wrote no config of yours and no policy: the one edit it made to ${REPO:-the sandbox repository}'s fugaro.yaml lives on the throwaway branch, which is deleted.${found}. ${run_note}${notes:+ What did not go:${notes}}"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to read facts, put
  # the checkout back and remove a directory, and a second Ctrl-C during it
  # would kill this shell half done.
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
  say "CHECK 200 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro runs digest --help — the release ${RELEASE} gate"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [local, write] go build -o <scratch>/fugaro-core ./cmd/fugaro in ${SOURCE} — the untagged build, proved untagged with 'fugaro report' (the core stub)"
  say "  [write, the sandbox repository] git checkout -b chk200-<hex>; governance.stage_timeout.implement = ${STAGE_TIMEOUT} written into fugaro.yaml (the CLI has no write of it); git commit; git push origin chk200-<hex>"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --ref chk200-<hex> --task-file <scratch>/task.md --json — ONE sandbox run, launched to be halted at the ${STAGE_TIMEOUT} ceiling, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run>/result.json — the record: its halt and its checkpoint"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/builds/<slug>/<workflow>/image.json — the edition carrier R3 writes"
  say "  [read] <scratch>/fugaro-core runs digest --period day --stdout, in ${SANDBOX} — the digest from the untagged build"
  say "  [Restore] the throwaway branch is deleted locally and on origin, and the checkout is put back on the branch it was on; the run's pull request is yours to close"
  fact bound PASS "planned: ONE sandbox run under a ${STAGE_TIMEOUT} implement ceiling, plus one throwaway branch that is deleted again. --dry-run runs nothing, and a real run requires --yes"
  for key in fugaro-version release sandbox source config run-launched terminal \
    result-json halt checkpoint edition-carrier digest-untagged; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no fugaro call, no gcloud call, no git call and no run — the guards and these lines are the whole of it"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  source config run-launched terminal result-json halt checkpoint
  edition-carrier digest-untagged restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 200 RESULT\n' >&3
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
  printf 'CHECK 200 RESULT END (exit %s)\n' "$status" >&3
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
SOURCE_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the run is launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --source)
    [ $# -ge 2 ] || die "--source needs a path: a Fugaro source checkout, which is what the untagged binary is built from"
    SOURCE_CLI=$2
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

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SANDBOX=${CHECK200_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 200 needs --sandbox: the checkout of a repository of the sandbox project. It is what the run is launched from and what the ceiling is written into. Usage: bash scripts/live-checks/check200.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
# The source checkout defaults to the tree this script lives in — scripts/live-checks
# inside it — because the untagged binary has to be built from a Fugaro tree.
SOURCE=${CHECK200_SOURCE:-${SOURCE_CLI:-$(cd "$HERE/../.." && pwd)}}
case "$SOURCE" in
/*) ;;
*) SOURCE="$PWD/$SOURCE" ;;
esac
# The project configuration's key for this repository, and the name
# `fugaro runs ls --repo` takes: the checkout directory's own name by default,
# and the knob exists because the two need not agree.
REPO=${CHECK200_REPO:-${SANDBOX##*/}}
TASK_FILE_TEXT=${CHECK200_TASK:-}
STAGE_TIMEOUT=${CHECK200_STAGE_TIMEOUT:-1m}
RELEASE=${CHECK200_RELEASE:-0.13.0}
YES=${CHECK200_YES:-$YES_CLI}
DRY_RUN=${CHECK200_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK200_KEEP_SCRATCH:-}
REGION=${CHECK200_REGION:-us-east5}
RUN_TIMEOUT=${CHECK200_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK200_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK200_CLI_TIMEOUT_SECONDS:-180}
BUILD_TIMEOUT=${CHECK200_BUILD_TIMEOUT_SECONDS:-900}
LAUNCH_TIMEOUT=${CHECK200_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK200_DOCTOR_TIMEOUT_SECONDS:-300}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac
BRANCH_HEX=$(python3 -c 'import os; print(os.urandom(4).hex())' 2>/dev/null || printf '%08x' "$$")

# The task the run is launched with, written where the run reads it: it is
# deliberately longer than the ceiling, because a task that could finish inside
# ${STAGE_TIMEOUT} would end succeeded and leave the halt row a reading of
# nothing.
write_task() {
  if [ -n "$TASK_FILE_TEXT" ]; then
    printf '%s\n' "$TASK_FILE_TEXT" >"$SCRATCH/task.md"
    return 0
  fi
  cat >"$SCRATCH/task.md" <<'MD'
Write a file named INVENTORY.md at the repository root, and take your time over
it: walk every Go file under internal/ and add one line per file — its path, its
package and a one-sentence summary of what it holds. There are hundreds of them,
so work through them methodically and do not stop until every file is covered.
MD
}

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 200 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  write_task
  guard_xtrace
  guard_agent_session
  guard_belong
  if [ -n "$DRY_RUN" ]; then
    plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the guards and the plan itself. It reached its end -- the plan IS the end
    # of a dry run -- so the exit trap must not read it as a run that stopped on
    # an error.
    REACHED_END=yes
    BLOCK_KEYS=(xtrace agent-session belong dry-run)
    return 0
  fi
  if [ -z "$YES" ]; then
    die "Check 200 launches ONE sandbox run and spends what its implement stage costs up to the ${STAGE_TIMEOUT} ceiling, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check200.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_release
  step_bound
  step_sandbox
  step_source
  step_config
  step_run
  step_terminal
  step_record
  step_halt
  step_checkpoint
  step_edition
  step_digest
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
