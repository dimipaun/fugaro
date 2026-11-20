#!/usr/bin/env bash
# Check 790 — the upgrade that gives an installation its budget database, watched
# across a run that is already in flight, as ONE script YOU run.
#
# `always-a-budget` removes budget mode off (D3009), so every run of every
# repository opens a budget session and is metered by a gateway (R3). An
# installation that predates that release has no budget database at all: the
# release stops such an installation launching anything until a PROJECT OWNER
# runs `fugaro update`, which creates the database, migrates the config and the
# published policy, and re-applies the repositories it is run for (D3023).
# Nothing offline can settle what that upgrade does to a real installation, and
# three questions are open in docs/always-a-budget/reconciliation.md §7:
#
#   U1  whether an OPERATOR's roles suffice to apply the Firebase root. AB7's
#       probe reads this and refuses where they do not; what the operator's own
#       update prints and whether it really changes nothing is this check's.
#   U2  what a run IN FLIGHT does when the database's rules are deployed for the
#       first time underneath it. It holds no session, so nothing is expected —
#       this check runs one across the update and reads its record.
#   U4  that a runner from BEFORE the release, under an explicit `observe` on a
#       database created minutes earlier, behaves as any observe run.
#
# So the check does its own setup and teardown and reads, row by row:
#
#   premise     the installation is one this release has not reached: the
#               repository's job carries no database address, and the published
#               policy carries no database URL. A failure here is a STOP (exit
#               2), not a finding — this check is about an installation at the
#               PREVIOUS release, and on one already migrated every row below
#               would be a row about nothing.
#   in-flight   a run is launched BEFORE the update and its id recorded.
#   update      `fugaro update` is run for the installation as its project
#               owner; the command must exit 0.
#   database    the same job, read back: it now carries FUGARO_RTDB_URL, and the
#               update's own output named the database it created.
#   in-flight-2 the run launched before the update is read until it ends: the
#               rules deployed underneath it must not have killed it.
#   gateway     a run launched AFTER the update lands on an execution whose own
#               environment carries the database address and mounts no model
#               key: the agent's environment holds no API key for want of a
#               gateway (AB1b, D3009).
#   old-image   the job's image is read for its revision; when it still predates
#               the release, the post-update run's record must read mode
#               `observe` — an unset mode is observe (C1, U4).
#   operator    a SECOND installation, at the previous release too, is updated
#               with the operator's own credential: the command must change
#               nothing (its job still carries no database address) and must say
#               that a project owner has to run it (U1, D3023).
#
# It launches TWO runs of a sandbox repository on the installation you name, and
# a project owner's `fugaro update` on it. Both COST MONEY: the runs' own model
# spend and whatever the database costs the project. It is NOT read-only.
#
# Usage:      bash scripts/live-checks/check790.sh --installation <name> \
#               --sandbox <a sandbox repository checkout> --job <the repo's Cloud
#               Run job> --gcp-project <project> --region <region> \
#               [--operator-config <file>] [--dry-run]
#             --dry-run       prints the plan and calls NOTHING: no gcloud, no
#                             fugaro, no run, no update.
#             --installation  the installation whose upgrade is watched (also
#                             CHECK790_INSTALLATION).
#             --sandbox       the checkout of the sandbox repository the two
#                             runs are launched from (also CHECK790_SANDBOX).
#             --job           the Cloud Run job that repository's runs land on,
#                             as the installation names it.
#             --gcp-project   the installation's GCP project (also
#                             CHECK790_GCP_PROJECT).
#             --region        the Cloud Run region (also CHECK790_REGION).
#             --operator-config  the local config file of a SECOND installation
#                             at the previous release, read for the operator
#                             half; without it that row is UNVERIFIED rather
#                             than skipped silently.
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), gcloud (authenticated as the project owner for the update
#             half; the operator half uses your own default credential),
#             fugaro, git. Nothing is installed and nothing is fetched.
# Where:      the installation, project and region you name, and NOWHERE else —
#             every call carries them explicitly and the script never reads
#             `gcloud config get-value project`. Run it on an installation you
#             may upgrade and on a sandbox repository you may spend on.
# Costs:      the two runs' model spend (bounded by the repository's own caps)
#             and the database the update creates, at the design's own estimate
#             of about two dollars a month (F18). Nothing else.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check790_test.sh) drives it against fake
#             `gcloud`, `fugaro` and `git` on PATH and touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. The two runs it
#             launched are left to end on their own, as any run is; nothing
#             else was created.
# Output:     progress on stderr, ONE block on stdout headed CHECK 790 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 790 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 790 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 790 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 790 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# No secret is read, so none can be printed: this check reads a job's
# environment NAMES (never a value that could be one), a policy document, a
# run's own record and an update's output. The one value it writes is a task
# file of its own making, in its scratch directory.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
IN_FLIGHT_ID=""
NEW_ID=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real installation with it"
}

# jget FILE PATH prints the value at a dotted path of a JSON document, or the
# empty string. Every read this check makes is one document and one field, and a
# field that is absent is an absence rather than an error: a row that has to tell
# "absent" from "unreadable" reads the exit status of the command that wrote the
# file, not this.
jget() { # jget <file> <dotted.path>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
cur = doc
for part in sys.argv[2].split("."):
    if part == "":
        continue
    if isinstance(cur, list):
        try:
            cur = cur[int(part)]
        except Exception:
            print("")
            sys.exit(0)
    elif isinstance(cur, dict):
        if part not in cur:
            print("")
            sys.exit(0)
        cur = cur[part]
    else:
        print("")
        sys.exit(0)
if isinstance(cur, (dict, list)):
    print(json.dumps(cur))
elif isinstance(cur, bool):
    print("true" if cur else "false")
else:
    print(cur)
PY
}

# job_env FILE prints one line per environment entry of the FIRST container of a
# Cloud Run job or execution document: `<NAME>=<value>` for a plain entry and
# `secret:<NAME>` for one read from a secret. It is the NAMES that are read: the
# row that says a job carries no database address, and the row that says an
# execution mounts no model key, are both readings of this list, and a value is
# never printed.
job_env() { # job_env <file>
  python3 - "$1" <<'PY'
import json, sys

def containers(doc):
    for path in (
        ("spec", "template", "spec", "template", "spec", "containers"),
        ("spec", "template", "spec", "containers"),
        ("spec", "containers"),
        ("containers",),
    ):
        cur = doc
        for part in path:
            if isinstance(cur, dict) and part in cur:
                cur = cur[part]
            else:
                cur = None
                break
        if isinstance(cur, list) and cur:
            return cur
    return []

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for c in containers(doc):
    for e in c.get("env", []) or []:
        name = e.get("name", "")
        if not name:
            continue
        if "valueSource" in e:
            src = e["valueSource"].get("secretKeyRef", {}) or {}
            print("secret:%s" % src.get("name", name))
        else:
            print("%s=%s" % (name, e.get("value", "")))
PY
}

# job_image FILE prints the image reference of that first container.
job_image() { # job_image <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
def containers(doc):
    for path in (
        ("spec", "template", "spec", "template", "spec", "containers"),
        ("spec", "template", "spec", "containers"),
        ("spec", "containers"),
        ("containers",),
    ):
        cur = doc
        for part in path:
            if isinstance(cur, dict) and part in cur:
                cur = cur[part]
            else:
                cur = None
                break
        if isinstance(cur, list) and cur:
            return cur
    return []
cs = containers(doc)
if cs:
    print(cs[0].get("image", ""))
PY
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 790: it updates a real installation and launches two paid runs. Run it yourself: bash scripts/live-checks/check790.sh --installation <name> --sandbox <checkout> --job <job> --gcp-project <project> --region <region>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the environment of every command this check runs. Run it without -x: bash scripts/live-checks/check790.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 790 — the upgrade that gives an installation its budget database, watched
across a run that is already in flight. Run by YOU, against an installation at
the PREVIOUS release that has no budget database yet.

Usage:
  bash scripts/live-checks/check790.sh --installation <name> \
    --sandbox <a sandbox repository checkout> --job <that repo's Cloud Run job> \
    --gcp-project <project> --region <region> [--operator-config <file>] \
    [--dry-run]
  bash scripts/live-checks/check790.sh --help

It launches TWO paid runs and runs `fugaro update` for the installation as its
project owner. Every row it prints is one criterion of the register's Check 790;
the last line of the block is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

INSTALLATION=${CHECK790_INSTALLATION:-}
SANDBOX=${CHECK790_SANDBOX:-}
JOB=${CHECK790_JOB:-}
GCP_PROJECT=${CHECK790_GCP_PROJECT:-}
REGION=${CHECK790_REGION:-}
OPERATOR_CONFIG=${CHECK790_OPERATOR_CONFIG:-}
DRY_RUN=no
if [ "${CHECK790_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --installation)
    [ $# -ge 2 ] || stop "--installation needs the installation's name"
    INSTALLATION=$2
    shift 2
    ;;
  --sandbox)
    [ $# -ge 2 ] || stop "--sandbox needs the path of a sandbox repository checkout"
    SANDBOX=$2
    shift 2
    ;;
  --job)
    [ $# -ge 2 ] || stop "--job needs the name of the repository's Cloud Run job"
    JOB=$2
    shift 2
    ;;
  --gcp-project)
    [ $# -ge 2 ] || stop "--gcp-project needs the project id"
    GCP_PROJECT=$2
    shift 2
    ;;
  --region)
    [ $# -ge 2 ] || stop "--region needs the Cloud Run region"
    REGION=$2
    shift 2
    ;;
  --operator-config)
    [ $# -ge 2 ] || stop "--operator-config needs the path of a local config file"
    OPERATOR_CONFIG=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  *)
    stop "unknown argument $1 (usage: check790.sh --installation <name> --sandbox <checkout> --job <job> --gcp-project <project> --region <region>, --help, or --dry-run)"
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 790 plan (nothing is called: no gcloud, no fugaro, no run, no update)

  installation:   ${INSTALLATION:-<not given>}
  sandbox repo:   ${SANDBOX:-<not given>}
  job:            ${JOB:-<not given>}
  gcp project:    ${GCP_PROJECT:-<not given>}
  region:         ${REGION:-<not given>}
  operator config: ${OPERATOR_CONFIG:-<not given>}

What it would do, in order:

  premise      gcloud run jobs describe ${JOB:-<job>} --project ${GCP_PROJECT:-<project>} --region ${REGION:-<region>}
               read the first container's environment NAMES: FUGARO_RTDB_URL must be ABSENT, and
               'fugaro project policy show --json' must carry no budget database URL. A premise that
               does not hold STOPS the check (exit 2).
  in-flight    fugaro run --task-file <a task the check writes> --json   (in ${SANDBOX:-<sandbox>})
               -> the FIRST paid run, launched before the update.
  update       fugaro update --project ${INSTALLATION:-<installation>}
               -> the project owner's upgrade; it must exit 0.
  database     the same job, read back: FUGARO_RTDB_URL must now be PRESENT, and the update's output
               must name the database it created.
  in-flight-2  fugaro runs ls --json, polled until the FIRST run's row is terminal: the rules
               deployed underneath it must not have killed it (U2).
  gateway      fugaro run ... -> the SECOND paid run, launched after the update; then
               gcloud run jobs executions list --job ${JOB:-<job>} --limit 1
               -> its execution's environment must carry FUGARO_RTDB_URL and must mount no model key.
  old-image    the job's image revision is compared with ${SANDBOX:-<sandbox>}'s HEAD; when the image
               still predates the release, the second run's record must read budget mode 'observe' (U4).
  operator     fugaro update --config <operator config>
               -> a second installation at the previous release: it must change NOTHING and must say a
               project owner has to run it (U1).

Costs: the two runs' model spend, and the database the update creates (~2 USD/month).
Cleanup: the check's scratch directory. The two runs are left to end on their own.
PLAN
}

[ -n "$INSTALLATION" ] || { [ "$DRY_RUN" = yes ] || stop "no --installation was given (or --help for the usage)"; }
[ -n "$SANDBOX" ] || { [ "$DRY_RUN" = yes ] || stop "no --sandbox was given (or --help for the usage)"; }
[ -n "$JOB" ] || { [ "$DRY_RUN" = yes ] || stop "no --job was given (or --help for the usage)"; }
[ -n "$GCP_PROJECT" ] || { [ "$DRY_RUN" = yes ] || stop "no --gcp-project was given (or --help for the usage)"; }
[ -n "$REGION" ] || { [ "$DRY_RUN" = yes ] || stop "no --region was given (or --help for the usage)"; }

guard_xtrace
guard_agent

if [ "$DRY_RUN" = yes ]; then
  dry_run_plan
  exit 0
fi

need bash
need mktemp
need python3
need gcloud
need fugaro
need git

[ -d "$SANDBOX" ] || stop "$SANDBOX is not a directory: --sandbox is a checkout of a sandbox repository"
[ -d "$SANDBOX/.git" ] || stop "$SANDBOX is not a git checkout: --sandbox is the repository the two runs are launched from"
if [ -n "$OPERATOR_CONFIG" ]; then
  [ -f "$OPERATOR_CONFIG" ] || stop "$OPERATOR_CONFIG does not exist: --operator-config is a local config file of a second installation at the previous release"
fi

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk790.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk790-mine"
: >"$MARKER"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'INCOMPLETE'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  local facts=$1 status=$2
  printf '\nCHECK 790 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 790 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  # The facts are read BEFORE the scratch directory holding them is removed.
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the upgrade"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: a job's describe, the published policy, two runs' rows, the update's output and the task file this check wrote). The two runs it launched are left to end on their own"
  else
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself"
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

describe_job() { # describe_job <outfile>
  gcloud run jobs describe "$JOB" --project "$GCP_PROJECT" --region "$REGION" --format=json >"$1" 2>"$1.err"
}

launch_run() { # launch_run <outfile> — launches one run of the sandbox repository
  (cd "$SANDBOX" && fugaro run --task-file "$SCRATCH/task.md" --json) >"$1" 2>"$1.err"
}

# ---------------------------------------------------------------- the rows

step_premise() {
  say "reading the installation's state before the update (premise)"
  describe_job "$SCRATCH/job-before.json" || {
    fact premise FAIL "the job '${JOB}' could not be described in ${GCP_PROJECT}/${REGION}: $(head -c 400 "$SCRATCH/job-before.json.err" 2>/dev/null). Nothing can be read about an installation whose job is not there"
    stop "the job '${JOB}' could not be described — check --job, --gcp-project and --region"
  }
  local names
  names=$(job_env "$SCRATCH/job-before.json")
  if printf '%s\n' "$names" | grep -q '^FUGARO_RTDB_URL='; then
    fact premise FAIL "the job '${JOB}' already carries a database address (FUGARO_RTDB_URL), so this installation has already been reached by the release: Check 790 is about an installation at the PREVIOUS release with NO budget database, and every row below would be a row about nothing. Run the check on such an installation, or read the register's section for what to do with one already migrated"
    stop "the installation already has a budget database — this check is about the release before it"
  fi
  (cd "$SANDBOX" && fugaro project policy show --project "$INSTALLATION" --json) >"$SCRATCH/policy.json" 2>"$SCRATCH/policy.json.err" || true
  local url
  url=$(jget "$SCRATCH/policy.json" budget.rtdb_url)
  if [ -n "$url" ]; then
    fact premise FAIL "the published policy carries a budget database URL, so this installation has already been reached by the release: Check 790 is about an installation at the PREVIOUS release. Raise a clean one and run again"
    stop "the published policy already names a database — this check is about the release before it"
  fi
  fact premise PASS "the job '${JOB}' of ${GCP_PROJECT}/${REGION} carries no FUGARO_RTDB_URL and the published policy carries no database URL: this installation is at the previous release, which is the state Check 790 is about"
}

step_inflight() {
  say "launching the run that will be IN FLIGHT across the update (paid)"
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check790 — the run in flight across the budget-database upgrade

Change nothing that matters: add one line to README.md naming this run as Check
790's in-flight run, and open a pull request.
TASK
  launch_run "$SCRATCH/inflight.json" || {
    fact in-flight FAIL "the run that has to be in flight across the update could not be launched: $(head -c 400 "$SCRATCH/inflight.json.err" 2>/dev/null)"
    stop "the in-flight run could not be launched, and the update is not run without it"
  }
  IN_FLIGHT_ID=$(jget "$SCRATCH/inflight.json" run_id)
  [ -n "$IN_FLIGHT_ID" ] || IN_FLIGHT_ID=$(jget "$SCRATCH/inflight.json" id)
  if [ -z "$IN_FLIGHT_ID" ]; then
    fact in-flight UNVERIFIED "a run was launched but its own answer carries no run id this check can read, so the row that follows it across the update has nothing to follow: $(head -c 200 "$SCRATCH/inflight.json" 2>/dev/null)"
    return 0
  fi
  fact in-flight PASS "the run ${IN_FLIGHT_ID} was launched before the update and is in flight: the rows below read it across the upgrade"
}

step_update() {
  say "running the project owner's 'fugaro update' for ${INSTALLATION} (this is the upgrade)"
  if ! (cd "$SANDBOX" && fugaro update --project "$INSTALLATION") >"$SCRATCH/update.out" 2>&1; then
    fact update FAIL "the project owner's 'fugaro update --project ${INSTALLATION}' did not exit 0: $(head -c 500 "$SCRATCH/update.out" 2>/dev/null). The whole upgrade is that one command, and a run that did not finish it leaves the installation where it was"
    stop "the owner's update failed — nothing below can be read"
  fi
  fact update PASS "the project owner's 'fugaro update --project ${INSTALLATION}' exited 0"
}

step_database() {
  local names
  describe_job "$SCRATCH/job-after.json" || {
    fact database UNVERIFIED "the job '${JOB}' could not be described after the update, so whether it now carries the database address is NOT measured: $(head -c 300 "$SCRATCH/job-after.json.err" 2>/dev/null)"
    return 0
  }
  names=$(job_env "$SCRATCH/job-after.json")
  if ! printf '%s\n' "$names" | grep -q '^FUGARO_RTDB_URL='; then
    fact database FAIL "the update exited 0 and the job '${JOB}' still carries no database address: the re-applied job is what gives a run its identity (an addressless job's launch is refused), so the upgrade has not reached this repository. Re-apply it with 'fugaro update' run for that repository and run this check again"
    return 0
  fi
  if ! grep -qi 'database' "$SCRATCH/update.out"; then
    fact database UNVERIFIED "the job now carries the database address, but the update's own output does not name the database it created: the row that says WHICH database a run will spend against is not read here. Read the update's output yourself and record the URL in the register's section"
    return 0
  fi
  fact database PASS "the update created the budget database and re-applied the job '${JOB}', which now carries FUGARO_RTDB_URL; the update's own output names the database it created"
}

step_inflight_finished() {
  if [ -z "$IN_FLIGHT_ID" ]; then
    fact in-flight-2 UNVERIFIED "there is no in-flight run id to follow: the launch row above says why"
    return 0
  fi
  say "reading the run ${IN_FLIGHT_ID} across the update"
  local i=0 outcome="" state=""
  while [ "$i" -lt 30 ]; do
    (cd "$SANDBOX" && fugaro runs ls --json) >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.json.err" || true
    state=$(python3 - "$SCRATCH/runs.json" "$IN_FLIGHT_ID" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
rows = doc if isinstance(doc, list) else doc.get("runs", []) or []
for r in rows:
    if not isinstance(r, dict):
        continue
    if r.get("id") == sys.argv[2] or r.get("run_id") == sys.argv[2]:
        print("%s|%s" % (r.get("outcome", ""), r.get("status", "")))
        break
PY
)
    outcome=${state%%|*}
    if [ -n "$outcome" ]; then break; fi
    i=$((i + 1))
    sleep "${CHECK790_POLL_SECONDS:-20}"
  done
  if [ -z "$outcome" ]; then
    fact in-flight-2 UNVERIFIED "the run ${IN_FLIGHT_ID} launched before the update is still not terminal after the check's own wait, so whether the database's rules deployed underneath it killed it is NOT measured. Read it yourself ('fugaro runs ls') and record the outcome in the register's section"
    return 0
  fi
  case "$outcome" in
  failed | cancelled)
    fact in-flight-2 FAIL "the run ${IN_FLIGHT_ID}, which was in flight when the budget database was created underneath it, ended '${outcome}': a run that started with no session must still finish (U2), and an upgrade that kills the runs already going is the stop R1 accepts only for runs that have NOT started"
    ;;
  *)
    fact in-flight-2 PASS "the run ${IN_FLIGHT_ID}, in flight when the database was created underneath it, ended '${outcome}': the upgrade left a run already going alone (U2)"
    ;;
  esac
}

step_gateway() {
  say "launching the run that has to have a gateway (paid)"
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check790 — the run that must have a gateway

Add one line to README.md naming this run as Check 790's post-update run, and
open a pull request.
TASK
  launch_run "$SCRATCH/new.json" || {
    fact gateway FAIL "the run launched after the update could not be launched: $(head -c 400 "$SCRATCH/new.json.err" 2>/dev/null). If the launcher refused it, the refusal it printed is the finding — read it and record it in the register's section"
    return 0
  }
  NEW_ID=$(jget "$SCRATCH/new.json" run_id)
  [ -n "$NEW_ID" ] || NEW_ID=$(jget "$SCRATCH/new.json" id)
  gcloud run jobs executions list --job "$JOB" --project "$GCP_PROJECT" --region "$REGION" --limit 1 --format=json >"$SCRATCH/exec.json" 2>"$SCRATCH/exec.json.err" || {
    fact gateway UNVERIFIED "the newest execution of '${JOB}' could not be read, so whether the agent's environment holds an API key is NOT measured: $(head -c 300 "$SCRATCH/exec.json.err" 2>/dev/null)"
    return 0
  }
  local names image
  names=$(job_env "$SCRATCH/exec.json")
  image=$(job_image "$SCRATCH/exec.json")
  if ! printf '%s\n' "$names" | grep -q '^FUGARO_RTDB_URL='; then
    fact gateway FAIL "the newest execution of '${JOB}' carries no database address: a run with no database is refused at its start, and the gateway that reaches the credentials service is the database's own session — a run on this execution has no gateway"
    return 0
  fi
  if printf '%s\n' "$names" | grep -qE '^ANTHROPIC_API_KEY=|^secret:.*model|^secret:.*anthropic|^secret:.*provider-key'; then
    fact gateway FAIL "the newest execution of '${JOB}' still carries a model credential in the container's own environment, so a run on it can reach the model WITHOUT the gateway: the whole point of the release is that the gateway is what holds the credential and the agent's environment holds none (AB1b, D3009)"
    return 0
  fi
  fact gateway PASS "the execution of '${JOB}' that the post-update run ${NEW_ID:-<id not read>} landed on carries FUGARO_RTDB_URL and mounts no model credential: the run has a gateway, and the agent's environment holds no API key for want of one (image ${image:-<not read>})"
}

step_old_image() {
  local image head
  image=$(job_image "$SCRATCH/job-after.json")
  head=$(git -C "$SANDBOX" rev-parse HEAD 2>/dev/null || true)
  if [ -z "$image" ]; then
    fact old-image UNVERIFIED "the job's image could not be read, so whether it predates the release is NOT measured, and the row that reads a pre-release runner's mode has no subject"
    return 0
  fi
  if [ -n "$head" ]; then
    case "$image" in
    *"$head"*)
      fact old-image UNVERIFIED "the job's image carries this checkout's own HEAD (${head}), so it does NOT predate the release: nothing here is about a runner from before it. Point the check at a job whose image the previous release built, or record this row in the register as not measured"
      return 0
      ;;
    esac
  fi
  local mode
  (cd "$SANDBOX" && fugaro runs ls --json) >"$SCRATCH/runs2.json" 2>/dev/null || true
  mode=$(python3 - "$SCRATCH/runs2.json" "$NEW_ID" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
rows = doc if isinstance(doc, list) else doc.get("runs", []) or []
for r in rows:
    if not isinstance(r, dict):
        continue
    if sys.argv[2] and (r.get("id") == sys.argv[2] or r.get("run_id") == sys.argv[2]):
        b = r.get("budget") or {}
        print(b.get("mode", ""))
        break
PY
)
  if [ -z "$mode" ]; then
    fact old-image UNVERIFIED "the post-update run's own record carries no budget mode, so whether a pre-release runner lands on observe is NOT measured"
    return 0
  fi
  case "$mode" in
  observe)
    fact old-image PASS "the job's image (${image}) predates this checkout and the run launched on it recorded budget mode 'observe': an unset mode IS observe, and a runner from before the release behaves as any observe run (C1, U4)"
    ;;
  *)
    fact old-image FAIL "the run launched on a pre-release image recorded budget mode '${mode}', not 'observe': an unset mode is observe (C1), and any other reading is a runner resolving a mode of its own"
    ;;
  esac
}

step_operator() {
  if [ -z "$OPERATOR_CONFIG" ]; then
    fact operator UNVERIFIED "no --operator-config was given, so the second installation's operator update is NOT measured (U1): the row that reads whether an operator's roles suffice and whether their update says a project owner must run it needs a SECOND installation at the previous release"
    return 0
  fi
  say "running the operator's 'fugaro update' on the second installation"
  local operator operator_job
  operator=$(python3 - "$OPERATOR_CONFIG" <<'PY'
import re, sys
try:
    text = open(sys.argv[1]).read()
except Exception:
    sys.exit(0)
m = re.search(r"(?m)^\s*name:\s*(\S+)", text)
print(m.group(1) if m else "")
PY
)
  operator_job=$(python3 - "$OPERATOR_CONFIG" <<'PY'
import re, sys
try:
    text = open(sys.argv[1]).read()
except Exception:
    sys.exit(0)
m = re.search(r"(?m)^\s*job:\s*(\S+)", text)
print(m.group(1) if m else "")
PY
)
  if ! (cd "$SANDBOX" && fugaro update --config "$OPERATOR_CONFIG") >"$SCRATCH/operator.out" 2>&1; then
    fact operator FAIL "the operator's 'fugaro update --config ${OPERATOR_CONFIG}' did not exit 0: $(head -c 500 "$SCRATCH/operator.out" 2>/dev/null). An operator's update on an installation with no database must skip that stage and say who must run it, never fail"
    return 0
  fi
  if ! grep -qi 'project owner' "$SCRATCH/operator.out"; then
    fact operator FAIL "the operator's update exited 0 without naming a project owner: an installation with no budget database launches nothing until a project owner runs the update (D3023), and an update that says nothing about it leaves the operator with an installation that silently launches nothing"
    return 0
  fi
  if [ -n "$operator_job" ]; then
    gcloud run jobs describe "$operator_job" --project "$GCP_PROJECT" --region "$REGION" --format=json >"$SCRATCH/operator-job.json" 2>/dev/null || true
    if printf '%s\n' "$(job_env "$SCRATCH/operator-job.json")" | grep -q '^FUGARO_RTDB_URL='; then
      fact operator FAIL "the operator's update CHANGED the second installation: its job '${operator_job}' now carries a database address, which an operator's roles must not be able to create. An operator's update on an installation with no database skips that stage and changes nothing (U1)"
      return 0
    fi
  fi
  fact operator PASS "the operator's update on '${operator:-the second installation}' exited 0, changed nothing (its job still carries no database address) and says a project owner must run it: an operator's roles do not suffice, and the update says so rather than failing (U1, D3023)"
}

step_premise
step_inflight
step_update
step_database
step_inflight_finished
step_gateway
step_old_image
step_operator
REACHED_END=yes
