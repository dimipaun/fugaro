#!/usr/bin/env bash
# Check 773 — `fugaro update` on a workspace and on a member at the previous
# release, with a run in flight on each: both converge with no other step, and
# the run finishes.
#
# Plan task WS20 writes it (docs/workspace-service/implementation-plan.md §8);
# no task of that plan runs it. What it settles is R11 of
# docs/workspace-service/reconciliation.md, and with it the HARD RULE it comes
# from (CLAUDE.md): `fugaro update` is the whole upgrade and it works perfectly
# — and it is safe while runs are in flight, always. Two things can still reach
# a run in flight, and the update names each in one short line rather than
# denying it: a membership or an entry that changed, which the service refuses
# from its next read of the node (R13), and a new revision of the service, which
# drains the old one.
#
# WHAT THIS SCRIPT IS, AND WHAT IT IS NOT. It runs no update: an update is a
# real installation's, and nothing here may write to a cloud. The owner stages
# both sides at the previous release, launches a run on each, runs
# `fugaro update` on each with its output kept, and passes the paths in. What
# the script measures is what those updates left:
#
#   * both sides CONVERGED: `fugaro doctor --all --json` on the workspace and on
#     the member reports no warning afterwards, which is what "with no other
#     step" means — every step the update owed the installation, it took;
#   * the update's own line about the runs in flight: the sentence the HARD RULE
#     asks for (internal/cli/update_in_flight.go: "<N> in flight: update waits
#     for none and changes nothing they hold"), present in both logs, together
#     with no failure line beside it;
#   * the two runs themselves, as `fugaro runs ls --json` states them: terminal,
#     with no halt that fell inside the update's own window. A run the update
#     stopped is exactly what R11 forbids.
#
# Usage:      bash scripts/live-checks/check773.sh
#             bash scripts/live-checks/check773.sh --dry-run
#             bash scripts/live-checks/check773.sh --help
# Needs:      bash, mktemp, python3, gcloud (for the account guard and because
#             the run listing is read from the cloud) and fugaro (doctor, the
#             run listing and the version).
# Where:      run from anywhere; the two updates' logs are files you name. The
#             installation the guards read is ${CHECK773_INSTALLATION}.
# Creates:    nothing anywhere. The update logs are files you wrote; the copies
#             of the doctor documents and the run listing this check reads sit in
#             a scratch directory (mode 0700) removed from the exit trap even on
#             a failure or Ctrl-C.
# Costs:      nothing billable by the check: it calls no model, launches no run
#             and runs no update. The two runs it reads are the owner's own.
# Runs:       by YOU, after the two updates — and no agent runs any step of it.
#             Its own test (check773_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else.
# Output:     progress on stderr, ONE block on stdout headed CHECK 773 RESULT,
#             then CHECK 773 RESULT END (exit N). Exit 0: every criterion that
#             was measured held. 1: a measured criterion did not hold. 2: the
#             check could not measure. In the block, a key the run never reached
#             reads `FAIL — not reached`.
#
# Knobs (all CHECK773_*): DRY_RUN, KEEP_SCRATCH, WORKSPACE_PROJECT (the
# workspace's Fugaro project, for its doctor read), MEMBER_PROJECT (the member's,
# `fugaro` by default), INSTALLATION (the installation the guard reads),
# WORKSPACE_UPDATE_LOG and MEMBER_UPDATE_LOG (the two updates' own output, saved
# by you — unset, the rows about them are not measured), WORKSPACE_RUN and
# MEMBER_RUN (the run in flight on each side, by run id or its last four
# characters — unset, the two run rows are not measured) and
# WORKSPACE_UPDATE_AT (when the workspace's update ran, epoch seconds or
# RFC 3339).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this run reads an
# installation's doctor document, and the shell it is started from may be a
# login shell whose trace ends up in a log.
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

# The one sentence the HARD RULE asks the update to say about the runs in flight
# (internal/cli/update_in_flight.go, announceRunsInFlight). It is grepped, not
# paraphrased: a check that read a differently worded line as the same promise
# would read a release that says something else as this one.
IN_FLIGHT_PHRASE='in flight: update waits for none and changes nothing they hold'
# A line of an update that says a step it owed did not land. An update that
# failed a step and printed it is the release that ships an operator step, which
# the HARD RULE forbids.
FAILURE_WORDS='refused|failed|could not|not applied|permission denied'

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 773 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 773 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk773-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
RUNS_OUT=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk773.XXXXXXXX") || die "Check 773 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  write_reader
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
    say "CHECK773_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was deleted by this check: no update ran, no installation was written and no run was touched. The two update logs are yours and stay where you put them, and this check reads them from there"
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

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }
field_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }
lines_of() { printf '%s\n' "$2" | sed -n "s/^$1=//p"; }
last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 773's reader: an installation's doctor document, an update's own
output, and the run listing. None of them carries a credential.

doctor   argv: `fugaro doctor --all --json`'s file. Prints `ok=` and one
         `warning=<id>: <problem>` line per check whose severity is a warning —
         a problem doctor leaves for the operator — plus `checks=` and
         `warnings=`. A warning is what "converged with no other step" is not.
update   argv: a file of the update's own output, the phrase that names the runs
         in flight, and the words that mean a step did not land. Prints
         `lines=`, `inflight=yes|no`, and one `failure=<line>` line per line
         matching those words, so the caller can read a step that did not land
         rather than only its count.
runs     argv: `fugaro runs ls --json`'s file and the run id asked about (or its
         last characters). Prints the row's status, terminal, outcome and its
         halt's reason, moment, detail and workspace marker; `found=no` when no
         row matches.
"""

import json
import re
import sys


def dig(doc, *path):
    cur = doc
    for step in path:
        if not isinstance(cur, dict) or step not in cur:
            return None
        cur = cur[step]
    return cur


def doctor(path):
    with open(path) as fh:
        doc = json.load(fh)
    checks = dig(doc, "checks")
    if not isinstance(checks, list):
        checks = []
    warnings = 0
    for one in checks:
        if not isinstance(one, dict):
            continue
        if str(one.get("severity") or "") != "warning":
            continue
        warnings += 1
        problem = re.sub(r"\s+", " ", str(one.get("problem") or "")).strip()
        print("warning=%s: %s" % (str(one.get("id") or "?"), problem[:300]))
    print("ok=" + ("yes" if doc.get("ok") else "no"))
    print("checks=%d" % len(checks))
    print("warnings=%d" % warnings)


def update(path, phrase, words):
    with open(path, "r", errors="replace") as fh:
        raw = fh.read()
    lines = raw.splitlines()
    print("lines=%d" % len(lines))
    print("inflight=" + ("yes" if any(phrase in one for one in lines) else "no"))
    pattern = re.compile(words, re.IGNORECASE)
    for one in lines:
        text = one.strip()
        if not text or not pattern.search(text):
            continue
        print("failure=" + re.sub(r"\s+", " ", text)[:300])


def runs(path, asked):
    with open(path) as fh:
        doc = json.load(fh)
    rows = dig(doc, "runs")
    if not isinstance(rows, list):
        rows = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        run_id = str(row.get("run_id") or "")
        if not asked or not (run_id == asked or run_id.endswith(asked)):
            continue
        halt = row.get("halt") if isinstance(row.get("halt"), dict) else {}
        print("found=yes")
        print("run_id=" + run_id)
        print("status=" + str(row.get("status") or ""))
        print("terminal=" + ("yes" if row.get("terminal") else "no"))
        print("outcome=" + str(row.get("outcome") or ""))
        print("reason=" + str(row.get("reason") or ""))
        print("halt_reason=" + str(halt.get("reason") or ""))
        print("halt_at=" + str(halt.get("at") or ""))
        print("halt_detail=" + str(halt.get("detail") or ""))
        print("halt_workspace_refusal=" + str(halt.get("workspace_refusal") or ""))
        return
    print("found=no")


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading", file=sys.stderr)
        return 2
    if argv[1] == "doctor":
        doctor(argv[2])
        return 0
    if argv[1] == "update":
        update(argv[2], argv[3], argv[4])
        return 0
    if argv[1] == "runs":
        runs(argv[2], argv[3] if len(argv) > 3 else "")
        return 0
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
# No agent runs any step of Check 773 (the plan says so): the two updates it
# reads are the owner's, and a coding agent's session is refused by the markers
# it carries, before anything is read.
guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 773. Run it yourself: bash scripts/live-checks/check773.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check773.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry the doctor documents it reads"
}

guard_project() {
  local doctor_out v
  doctor_out=$(fugaro doctor --project "$INSTALLATION" --all || true)
  if ! printf '%s\n' "$doctor_out" | grep -q '^project: .*(GCP fugaro-dev,'; then
    stop "this is not the fugaro-dev installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP fugaro-dev,' line. Run it here yourself and read its output before going on — this check's doctor reads are about ${MEMBER_PROJECT}${WORKSPACE_PROJECT:+, and about ${WORKSPACE_PROJECT},} and never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  [[ "$v" == *belong* ]] && stop "the version this check printed names belong: this check is never run there"
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} — this is the CLI the two updates' logs are read against: R11 is about 'fugaro update' on the release the CLI is, and a check run with an older CLI than the updates used reads the wrong release's promises"
}

# ------------------------------------------------------------------- the steps
# step_converged: doctor after the update, on each side. This is "with no other
# step": every step the update owed the installation, it took.
step_converged() { # step_converged KEY PROJECT WHERE
  local key=$1 project=$2 where=$3
  fugaro doctor --project "$project" --all --json >"$SCRATCH/doctor-$key.json" 2>"$SCRATCH/doctor-$key.err" || true
  if [ ! -s "$SCRATCH/doctor-$key.json" ]; then
    unmeasured "$key" "the doctor document for ${where} (${project}) could not be read ($(last_line "$(cat "$SCRATCH/doctor-$key.err" 2>/dev/null || true)")): 'fugaro doctor --project ${project} --all --json' failed. Run it here yourself and read its output"
    return 0
  fi
  local out warnings checks
  out=$(reader doctor "$SCRATCH/doctor-$key.json") || die "the doctor document could not be parsed ($(last_line "$out"))"
  warnings=$(field_of warnings "$out")
  checks=$(field_of checks "$out")
  if [ "${warnings:-0}" -eq 0 ]; then
    fact "$key" PASS "doctor on ${where} (${project}) reports ${checks:-?} checks and NO warning after the update: the installation is where the release the CLI is says it should be, with no step left for a person to take — which is what R11 asks the update to converge to, and what the HARD RULE calls 'the whole upgrade, and it works perfectly'. Run it yourself: fugaro doctor --project ${project} --all"
    return 0
  fi
  fact "$key" FAIL "doctor on ${where} (${project}) reports ${warnings} warning(s) after the update: $(printf '%s' "$(lines_of warning "$out" | head -n 3 | tr '\n' '; ')" | head -c 500). An update that leaves a warning is an update with another step behind it, which is exactly what the HARD RULE forbids — the fix belongs IN the update of the same release, not in a runbook"
}

# step_update_log: the update's own line about the runs in flight, and no step
# that did not land.
step_update_log() { # step_update_log KEY LOG ENVVAR WHERE
  local key=$1 log=$2 envvar=$3 where=$4
  if [ -z "$log" ]; then
    unmeasured "$key" "no path was given for ${where}'s update output (${envvar} is unset). Save it when you run the update — 'fugaro update 2>&1 | tee /tmp/${key}.log' on ${where} — and pass the path as ${envvar}"
    return 0
  fi
  if [ ! -r "$log" ]; then
    unmeasured "$key" "the file you named for ${where}'s update output (${log}) cannot be read: check the path, and that the update's output was saved rather than typed into a terminal that scrolled"
    return 0
  fi
  local out inflight failures
  out=$(reader update "$log" "$IN_FLIGHT_PHRASE" "$FAILURE_WORDS") || die "the update output could not be parsed ($(last_line "$out"))"
  inflight=$(field_of inflight "$out")
  failures=$(lines_of failure "$out")
  if [ -n "$failures" ]; then
    fact "$key" FAIL "the update on ${where} printed $(printf '%s\n' "$failures" | grep -c .) line(s) that name a step which did not land: $(printf '%s' "$(printf '%s\n' "$failures" | head -n 3 | tr '\n' '; ')" | head -c 500). R11 and the HARD RULE both say 'fugaro update' is the whole upgrade and it works perfectly: a step that failed and was printed is an operator step the release ships, and the next release has to take it inside the update"
    return 0
  fi
  if [ "$inflight" != yes ]; then
    fact "$key" FAIL "the update on ${where} printed no '${IN_FLIGHT_PHRASE}' line, and a run was in flight on it: the HARD RULE says the update says so in one line — the count, and that the update waits for none of them and changes nothing they hold. Either the run started after the update, or this release does not print the line. Read the log yourself: grep 'in flight' ${log}"
    return 0
  fi
  fact "$key" PASS "the update on ${where} printed the line the HARD RULE asks for ('${IN_FLIGHT_PHRASE}', $(field_of lines "$out") lines of output) and no line naming a step that did not land: it named the runs in flight rather than denying them, and it left nothing for a person to take"
}

# step_run: the run in flight, as its own record states it. It has to be over,
# and no halt of it may fall inside the update's window: a run the update stopped
# is what R11 forbids.
step_run() { # step_run KEY RUN ENVVAR AT WHERE
  local key=$1 run=$2 envvar=$3 at=$4 where=$5
  if [ -z "$run" ]; then
    unmeasured "$key" "no run id was given for ${where} (${envvar} is unset). Launch the run BEFORE the update, keep its run id, and pass it as ${envvar} (or its last four characters)"
    return 0
  fi
  if [ -z "$RUNS_OUT" ]; then
    unmeasured "$key" "not read: the run listing could not be read. Read it yourself: fugaro runs ls --json | grep -A8 ${run}"
    return 0
  fi
  local out
  out=$(reader runs "$SCRATCH/runs.json" "$run") || die "the run listing could not be parsed ($(last_line "$out"))"
  if [ "$(field_of found "$out")" != yes ]; then
    unmeasured "$key" "no run in the listing has an id ending in ${run}. List them: fugaro runs ls --json | grep run_id"
    return 0
  fi
  local rid status terminal halt_reason halt_at detail
  rid=$(field_of run_id "$out")
  status=$(field_of status "$out")
  terminal=$(field_of terminal "$out")
  halt_reason=$(field_of halt_reason "$out")
  halt_at=$(field_of halt_at "$out")
  detail=$(field_of halt_detail "$out")
  if [ "$terminal" != yes ]; then
    fact "$key" FAIL "the run ${rid} on ${where} is not terminal after the update (status ${status:-unknown}): R11 says a run in flight keeps the environment and the route it started with, so the update leaves it alone — a run still going hours after an update is a run the update is holding, or one that died without its record being written. Read it: fugaro runs show ${rid}"
    return 0
  fi
  if [ -z "$halt_reason" ]; then
    fact "$key" PASS "the run ${rid} in flight on ${where} finished across the update (status ${status}, outcome $(field_of outcome "$out"), no halt): the update reached neither its environment nor its route, which is what R11 promises and what the HARD RULE calls 'safe while runs are in flight, always'"
    return 0
  fi
  if [ -n "$at" ] && [ -n "$halt_at" ] && in_window "$at" "$halt_at" "$UPDATE_WINDOW_SECONDS"; then
    fact "$key" FAIL "the run ${rid} in flight on ${where} halted ${halt_reason} at ${halt_at}, $(gap_after "$at" "$halt_at" || true), with the update running at ${at} and marker '$(field_of halt_workspace_refusal "$out")'${detail:+, detail '$(printf '%s' "$detail" | head -c 200)'}: a run whose halt falls inside the update's own window is a run the update stopped, which R11 forbids. The step that reached it has to be named in the update's own line (the budget mode's raise to enforce, the policy a cascade project re-reads, a retirement) — and a run it stopped is not that line's promise being kept"
    return 0
  fi
  fact "$key" FAIL "the run ${rid} in flight on ${where} halted after the update: ${halt_reason} at ${halt_at}${detail:+, detail '$(printf '%s' "$detail" | head -c 200)'}. Whether the update caused it needs the moment the update ran (CHECK773_WORKSPACE_UPDATE_AT): with it, this row says whether the halt fell inside the update's window. A halt elsewhere is a finding of its own for that run — 'fugaro runs show ${rid}' and 'fugaro diagnose ${rid}' are the two that read it"
}

# in_window FROM TO SECONDS: whether TO falls in [FROM, FROM+SECONDS).
in_window() {
  python3 -c '
import sys, datetime
def at(one):
    if one.isdigit():
        return datetime.datetime.fromtimestamp(int(one), datetime.timezone.utc)
    return datetime.datetime.fromisoformat(one.replace("Z", "+00:00"))
try:
    start, end = at(sys.argv[1]), at(sys.argv[2])
except ValueError:
    sys.exit(1)
sys.exit(0 if 0 <= (end - start).total_seconds() < int(sys.argv[3]) else 1)
' "$1" "$2" "$3"
}

gap_after() {
  python3 -c '
import sys, datetime
def at(one):
    if one.isdigit():
        return datetime.datetime.fromtimestamp(int(one), datetime.timezone.utc)
    return datetime.datetime.fromisoformat(one.replace("Z", "+00:00"))
try:
    seconds = (at(sys.argv[2]) - at(sys.argv[1])).total_seconds()
except ValueError:
    sys.exit(1)
print("%dm%02ds after the update began" % (seconds // 60, seconds % 60))
' "$1" "$2"
}

# step_prompt: what only the owner can do, said out loud.
step_prompt() {
  local log_key
  fact update-prompt INFO "not run by this check, and no agent may run any of them: (1) stage the WORKSPACE's installation at the previous release and the MEMBER's too — a checkout at the previous tag, or the installed older CLI — and launch one run on each with 'fugaro run' so it is in flight; (2) run 'fugaro update' on each, keeping the output ('fugaro update 2>&1 | tee /tmp/ws-update.log'); (3) write down the moment the update began (date -u +%s); (4) run this check: CHECK773_WORKSPACE_UPDATE_LOG=/tmp/ws-update.log CHECK773_MEMBER_UPDATE_LOG=/tmp/member-update.log CHECK773_WORKSPACE_RUN=<id> CHECK773_MEMBER_RUN=<id> CHECK773_WORKSPACE_UPDATE_AT=<that> bash scripts/live-checks/check773.sh"
  for log_key in WORKSPACE MEMBER; do
    case "$log_key" in
    WORKSPACE) [ -n "$WORKSPACE_UPDATE_LOG" ] || fact "workspace-update-log-prompt" INFO "waiting on the workspace's update output: save it at the previous release and pass CHECK773_WORKSPACE_UPDATE_LOG" ;;
    MEMBER) [ -n "$MEMBER_UPDATE_LOG" ] || fact "member-update-log-prompt" INFO "waiting on the member's update output: save it at the previous release and pass CHECK773_MEMBER_UPDATE_LOG" ;;
    esac
  done
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 773 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guard: fugaro doctor --project ${INSTALLATION} --all and fugaro version"
  say "  [network, read] fugaro doctor --project ${WORKSPACE_PROJECT:-<workspace project>} --all --json — the workspace, converged?"
  say "  [network, read] fugaro doctor --project ${MEMBER_PROJECT} --all --json — the member, converged?"
  say "  [local, read] the two update logs you saved — the in-flight line and any step that did not land"
  say "  [network, read] fugaro runs ls --json — the run in flight on each side, terminal and unhalted"
  say "  [not run] the two updates and the two runs: the owner's, never a script's"
  say "  [nothing created] no update, no installation write, no run; the scratch directory is removed at the end"
  for key in fugaro-version xtrace workspace-converged member-converged workspace-update-line member-update-line workspace-run member-run update-prompt; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no installation was read and no update log was opened"
}

BLOCK_KEYS=(fugaro-version xtrace dry-run workspace-converged member-converged
  workspace-update-line member-update-line workspace-run member-run update-prompt
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 773 RESULT\n' >&3
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
  printf 'CHECK 773 RESULT END (exit %s)\n' "$status" >&3
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
for arg in "$@"; do
  case "$arg" in
  --dry-run) DRY_RUN_CLI=1 ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *) say "unknown argument: $arg"; exit 2 ;;
  esac
done

INSTALLATION=${CHECK773_INSTALLATION:-fugaro}
WORKSPACE_PROJECT=${CHECK773_WORKSPACE_PROJECT:-}
MEMBER_PROJECT=${CHECK773_MEMBER_PROJECT:-fugaro}
WORKSPACE_UPDATE_LOG=${CHECK773_WORKSPACE_UPDATE_LOG:-}
MEMBER_UPDATE_LOG=${CHECK773_MEMBER_UPDATE_LOG:-}
WORKSPACE_RUN=${CHECK773_WORKSPACE_RUN:-}
MEMBER_RUN=${CHECK773_MEMBER_RUN:-}
WORKSPACE_UPDATE_AT=${CHECK773_WORKSPACE_UPDATE_AT:-}
UPDATE_WINDOW_SECONDS=${CHECK773_UPDATE_WINDOW_SECONDS:-3600}
DRY_RUN=${CHECK773_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK773_KEEP_SCRATCH:-}
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$ACCOUNT" ] || die "gcloud prints no account ('gcloud config get-value account'): the check reads the run listing from the cloud with that account's own roles. Run 'gcloud auth login' first"
  [ "$UPDATE_WINDOW_SECONDS" -gt 0 ] || die "CHECK773_UPDATE_WINDOW_SECONDS is ${UPDATE_WINDOW_SECONDS}: the window is seconds an update may take, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  [ -n "$WORKSPACE_PROJECT" ] || unmeasured workspace-converged "CHECK773_WORKSPACE_PROJECT was not set, so this run does not know the workspace's Fugaro project to read doctor from. Name it and run the check again: CHECK773_WORKSPACE_PROJECT=<project> bash scripts/live-checks/check773.sh"
  guard_agent
  guard_xtrace
  guard_project
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  say "checking the two updates and the runs that were in flight across them"
  [ -n "$WORKSPACE_PROJECT" ] && step_converged workspace-converged "$WORKSPACE_PROJECT" "the workspace"
  step_converged member-converged "$MEMBER_PROJECT" "the member"
  step_update_log workspace-update-line "$WORKSPACE_UPDATE_LOG" CHECK773_WORKSPACE_UPDATE_LOG "the workspace"
  step_update_log member-update-line "$MEMBER_UPDATE_LOG" CHECK773_MEMBER_UPDATE_LOG "the member"
  fugaro runs ls --json >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err" || true
  if [ -s "$SCRATCH/runs.json" ]; then RUNS_OUT=yes; else say "the run listing could not be read ($(last_line "$(cat "$SCRATCH/runs.err" 2>/dev/null || true)"))"; fi
  step_run workspace-run "$WORKSPACE_RUN" CHECK773_WORKSPACE_RUN "$WORKSPACE_UPDATE_AT" "the workspace"
  step_run member-run "$MEMBER_RUN" CHECK773_MEMBER_RUN "$WORKSPACE_UPDATE_AT" "the member"
  step_prompt
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
