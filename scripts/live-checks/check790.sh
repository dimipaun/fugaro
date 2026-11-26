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
#   premise     the installation is one this release has not reached, read at
#               once through its own budget state: 'fugaro budget show --json',
#               which only a budget database can answer, must refuse as an
#               installation with no backend; and the repository's job must carry
#               no database address. Both are read BEFORE anything is launched,
#               the budget state first — an installation that already has its
#               database stops the check there, before the job is even described,
#               with the three ways forward below. A failure here is a STOP
#               (exit 2), not a finding — this check is about an installation at
#               the PREVIOUS release, and on one already migrated every row below
#               would be a row about nothing.
#   in-flight   a run is launched BEFORE the update and its id recorded.
#   update      `fugaro update` is run as the project owner, TWICE as the
#               after-deploy steps name it: for the installation (which creates
#               the database and migrates the config and the published policy)
#               and for the repository (`--repo` in its checkout, which is what
#               re-applies its job). Both commands must exit 0.
#   database    the same job, read back: it now carries FUGARO_RTDB_URL, and the
#               update's own output named the database it created.
#   in-flight-2 the run launched before the update is read until it ends: the
#               rules deployed underneath it must not have killed it.
#   gateway     a run launched AFTER the update lands on an execution whose own
#               environment carries the database address and mounts no model
#               key: the agent's environment holds no API key for want of a
#               gateway (AB1b, D3009).
#   old-image   the job's image is read for its revision; when it still predates
#               the release, the mode the re-applied JOB names must be `observe`
#               and the post-update run's OWN RECORD, read from the runs bucket,
#               must read mode `observe` — an unset mode is observe (C1, U4).
#   operator    a SECOND installation, at the previous release too, is updated
#               with the operator's own credential: the command must change
#               nothing (no job of ITS OWN project, read from its local config,
#               may carry a database address) and must say that a project owner
#               has to run it (U1, D3023).
#
# An installation that already has its budget database is not one this check can
# measure — the upgrade it watches has already happened there — so that reading
# stops it at once, before any run is launched, and names the THREE WAYS FORWARD
# in one line: point it at a second installation that predates the release
# (`--installation`, `--gcp-project`, and that installation's own `--job`,
# `--sandbox` and `--region`), record U1, U2 and U4 as waived by the owner in
# docs/always-a-budget, or run it on a fresh scratch project `fugaro setup`
# created at the release behind.
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
#             reader), gcloud (authenticated as the project owner: the update
#             half, and the `gcloud storage cat` that reads the post-update run's
#             record out of the runs bucket; the operator half uses your own
#             default credential), fugaro, git. Nothing is installed and nothing
#             is fetched.
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
# environment NAMES (never a value that could be one), the installation's own
# refusal, a run's own record, a listing's rows and an update's output. The one
# value it writes is a task file of its own making, in its scratch directory.
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
# `secret:<NAME>:<secret id>` for one read from a secret. It is the NAMES that
# are read: the row that says a job carries no database address, and the row that
# says an execution mounts no model key, are both readings of this list, and a
# value is never printed.
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

# A `jobs describe` document is one job; a `jobs list` document carries them
# under `items`, and the same reader is what lets a row read every job of an
# installation the check may not name one of.
if isinstance(doc, dict) and isinstance(doc.get("items"), list):
    jobs = [d for d in doc["items"] if isinstance(d, dict)]
elif isinstance(doc, list):
    jobs = [d for d in doc if isinstance(d, dict)]
else:
    jobs = [doc]

for job in jobs:
    for c in containers(job):
        for e in c.get("env", []) or []:
            name = e.get("name", "")
            if not name:
                continue
            # A mount is answered in two spellings and BOTH are read: a run/v2
            # document — what the repository's own terraform writes — names the
            # secret itself, under `valueSource.secretKeyRef.secret`, while the
            # older one names it under `secretKeyRef.name`, under `valueFrom` or
            # `valueSource`. The id is printed with the env name, because a
            # reader of a mount needs the two together.
            if "valueSource" in e or "valueFrom" in e:
                src = (e.get("valueSource") or e.get("valueFrom") or {}).get("secretKeyRef", {}) or {}
                print("secret:%s:%s" % (name, src.get("secret") or src.get("name") or name))
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

# config_key FILE KEY prints a top-level key of a local config file, with the
# quotes a YAML writer may have put around it stripped. It is how the operator's
# own installation is named: a local config carries its project and region, and
# no job name.
config_key() { # config_key <file> <key>
  python3 - "$1" "$2" <<'PY'
import re, sys
try:
    text = open(sys.argv[1]).read()
except Exception:
    sys.exit(0)
m = re.search(r"(?m)^%s:\s*(.*)$" % re.escape(sys.argv[2]), text)
if not m:
    sys.exit(0)
v = m.group(1).strip()
if v[:1] in ("'", '"') and v[-1:] == v[:1]:
    v = v[1:-1]
print(v)
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

It reads the installation's budget state first and STOPS AT ONCE, before any run
is launched, on one that already has its budget database — naming the three ways
forward: a second installation that predates the release, the owner's waiver of
U1, U2 and U4 in docs/always-a-budget, or a fresh scratch project `fugaro setup`
created at the release behind.

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

  premise      fugaro budget show --json --project ${INSTALLATION:-<installation>}   (in ${SANDBOX:-<sandbox>})
               -> the installation's own budget state, read FIRST: it must REFUSE for want of a budget
               backend, and an installation that already has its database STOPS the check AT ONCE
               (exit 2) with the three ways forward: a second installation that predates the release
               (--installation, --gcp-project), the owner's waiver of U1, U2 and U4 in
               docs/always-a-budget, or a fresh scratch project 'fugaro setup' created at the release behind.
               Then  gcloud run jobs describe ${JOB:-<job>} --project ${GCP_PROJECT:-<project>} --region ${REGION:-<region>}
               read the first container's environment NAMES: FUGARO_RTDB_URL must be ABSENT.
               A premise that does not hold STOPS the check (exit 2), before any run is launched.
  in-flight    fugaro run --task-file <a task the check writes> --json   (in ${SANDBOX:-<sandbox>})
               -> the FIRST paid run, launched before the update.
  update       fugaro update --project ${INSTALLATION:-<installation>}
               fugaro update --repo --project ${INSTALLATION:-<installation>}   (in ${SANDBOX:-<sandbox>})
               -> the project owner's upgrade and the repository's own re-apply; both must exit 0.
  database     the same job, read back: FUGARO_RTDB_URL must now be PRESENT, and the update's output
               must name the database it created.
  in-flight-2  fugaro runs ls --json, polled until the FIRST run's row is terminal: the rules
               deployed underneath it must not have killed it (U2).
  gateway      fugaro run ... -> the SECOND paid run, launched after the update; then
               gcloud run jobs executions list --job ${JOB:-<job>} --limit 1
               -> its execution's environment must carry FUGARO_RTDB_URL and must mount no model key.
  old-image    the job's image revision is compared with ${SANDBOX:-<sandbox>}'s HEAD; when the image
               still predates the release: the re-applied job must name FUGARO_BUDGET_MODE 'observe',
               and the SECOND run's own record, read from the runs bucket
               (gcloud storage cat gs://fugaro-runs-${GCP_PROJECT:-<project>}/runs/<the row's run>/result.json),
               must read budget mode 'observe' (U4).
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
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: a job's describe before and after, the installation's own refusal, two runs' rows, the two launches' answers, the runs listing the post-update run's record key is read from, that run's record, the newest execution, the two updates' own outputs, the second installation's job list and the task file this check wrote). The two runs it launched are left to end on their own"
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

# The three ways forward a person has when the installation they named already
# has its budget database. It is one sentence, and both the row that reads FAIL
# and the stop line beside it carry it, so the two cannot drift apart.
BUDGET_THERE_FIX="point the check at a second installation that predates the release (--installation, --gcp-project, and that installation's own --job, --sandbox and --region), run it on a fresh scratch project that 'fugaro setup' created at the release behind, or record U1, U2 and U4 as waived by the owner in docs/always-a-budget"

step_premise() {
  # The FIRST reading, and the reason this check stops at once rather than a
  # paragraph later: 'fugaro budget show --json' is the one command only a budget
  # database can answer (an installation with no backend has no database to read
  # the mode, the caps and the counters from, so it refuses and names it), and an
  # installation that already has its database has no upgrade for Check 790 to
  # watch — the owner's own fugaro-dev, mode enforce and caps set, is exactly
  # that. Nothing is described and nothing is launched before it holds.
  say "reading the installation's budget state before anything else (premise, first)"
  local status=0
  (cd "$SANDBOX" && fugaro budget show --json --project "$INSTALLATION") >"$SCRATCH/budget.json" 2>"$SCRATCH/budget.err" || status=$?
  if [ "$status" -eq 0 ]; then
    fact premise FAIL "the installation ${INSTALLATION} already has its budget database: 'fugaro budget show --json' answered (mode '$(jget "$SCRATCH/budget.json" mode)') instead of refusing as an installation with no budget backend, so the upgrade Check 790 watches has already happened here and every row below would be a row about nothing. Three ways forward: ${BUDGET_THERE_FIX}"
    stop "the installation ${INSTALLATION} already has a budget database, and Check 790 watches the upgrade that gives one — there is no upgrade to watch here: ${BUDGET_THERE_FIX}"
  fi
  if ! grep -qiE 'no firebase budget backend|budget\.rtdb_url' "$SCRATCH/budget.err" "$SCRATCH/budget.json" 2>/dev/null; then
    fact premise UNVERIFIED "'fugaro budget show --json --project ${INSTALLATION}' exited ${status} without naming a missing budget backend, so whether this installation is at the previous release is NOT measured: $(head -c 300 "$SCRATCH/budget.err" 2>/dev/null). Read that command yourself ('fugaro budget show --json --project ${INSTALLATION}'), and run the check again on an installation whose refusal says it has no backend"
    stop "the installation's own budget state could not be read — nothing is launched on an installation nobody settled"
  fi
  say "reading the repository's job before the update (premise, second)"
  describe_job "$SCRATCH/job-before.json" || {
    fact premise FAIL "the job '${JOB}' could not be described in ${GCP_PROJECT}/${REGION}: $(head -c 400 "$SCRATCH/job-before.json.err" 2>/dev/null). Nothing can be read about an installation whose job is not there"
    stop "the job '${JOB}' could not be described — check --job, --gcp-project and --region"
  }
  local names
  names=$(job_env "$SCRATCH/job-before.json")
  if printf '%s\n' "$names" | grep -q '^FUGARO_RTDB_URL='; then
    fact premise FAIL "the job '${JOB}' already carries a database address (FUGARO_RTDB_URL), so this installation has already been reached by the release: Check 790 is about an installation at the PREVIOUS release with NO budget database, and every row below would be a row about nothing. Three ways forward: ${BUDGET_THERE_FIX}"
    stop "the installation's job already carries a database address, and Check 790 watches the upgrade that gives one: ${BUDGET_THERE_FIX}"
  fi
  fact premise PASS "the installation's own budget state refuses for want of a backend ('fugaro budget show --json' exited ${status} naming budget.rtdb_url) and the job '${JOB}' of ${GCP_PROJECT}/${REGION} carries no FUGARO_RTDB_URL: this installation is at the previous release, which is the state Check 790 is about"
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
  # The installation's converge creates the database and migrates the config and
  # the published policy; the REPOSITORY's own root is re-applied by the update
  # run for that repository (`--repo`, in its checkout), which is what puts the
  # mode and the database in the job the rows below read (F20, and the plan's
  # after-deploy steps: once for the installation and once for each repository).
  say "re-applying the repository's own root ('fugaro update --repo')"
  if ! (cd "$SANDBOX" && fugaro update --repo --project "$INSTALLATION") >"$SCRATCH/update-repo.out" 2>&1; then
    fact update FAIL "the project owner's 'fugaro update --repo --project ${INSTALLATION}', run in ${SANDBOX}, did not exit 0: $(head -c 500 "$SCRATCH/update-repo.out" 2>/dev/null). A repository's job is re-applied by the update run for that repository, and a run that did not finish it leaves the job without its mode and its database (F20)"
    stop "the owner's repository update failed — the job's own rows cannot be read"
  fi
  fact update PASS "the project owner's 'fugaro update --project ${INSTALLATION}' and the repository's own 'fugaro update --repo' both exited 0"
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
  fact database PASS "the project owner's update created the budget database and the repository's own 'fugaro update --repo' re-applied the job '${JOB}', which now carries FUGARO_RTDB_URL; the update's own output names the database it created"
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

# model_key_mounts FILE prints the model credentials the container's own
# environment holds — the mount the release takes away (AB1b, D3009). job_env
# prints a mount as `secret:<env name>:<secret id>`, and the two are read
# together the way check193.sh's mounts_of reads them: the variable
# (ANTHROPIC_API_KEY, or a provider key's FUGARO_PROVIDER_KEY_ namespace) and
# the secret's own id (`anthropic-api-key`, `provider-key`, with the hash
# SecretID puts on a derived id stripped). Reading both is the whole point: a
# `gcloud` document names the reference in two spellings, so a reader that knew
# one of them would call a job that still mounts the paid key an unmounted one
# — and this row would then report the gateway's absence on an agent whose
# environment holds the key. The Claude OAuth token is deliberately not among
# these names: AB1b keeps the oauth arm and OAuthStageEnv unchanged.
model_key_mounts() { # model_key_mounts <file>
  job_env "$1" | python3 -c '
import re, sys

MODEL_ENVS = ("ANTHROPIC_API_KEY",)
PROVIDER_KEY_ENV_PREFIX = "FUGARO_PROVIDER_KEY_"
MODEL_SECRETS = ("anthropic-api-key", "provider-key")
SECRET_HASH_RE = re.compile(r"-[0-9a-f]{16}$")


def named(base):
    stem = SECRET_HASH_RE.sub("", base)
    return any(stem.endswith("-" + s) or ("-" + s + "-") in stem for s in MODEL_SECRETS)


out = []
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    base = ""
    if line.startswith("secret:"):
        _, name, ref = line.split(":", 2)
        base = ref.split(":")[-1].split("/")[-1]
    else:
        name, _, value = line.partition("=")
        if not value:
            continue
    if name in MODEL_ENVS or name.startswith(PROVIDER_KEY_ENV_PREFIX) or named(base):
        if name not in out:
            out.append(name)
print(", ".join(out))
'
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
  local names image mounts
  names=$(job_env "$SCRATCH/exec.json")
  image=$(job_image "$SCRATCH/exec.json")
  if ! printf '%s\n' "$names" | grep -q '^FUGARO_RTDB_URL='; then
    fact gateway FAIL "the newest execution of '${JOB}' carries no database address: a run with no database is refused at its start, and the gateway that reaches the credentials service is the database's own session — a run on this execution has no gateway"
    return 0
  fi
  mounts=$(model_key_mounts "$SCRATCH/exec.json")
  if [ -n "$mounts" ]; then
    fact gateway FAIL "the newest execution of '${JOB}' still carries a model credential in the container's own environment (${mounts}), so a run on it can reach the model WITHOUT the gateway: the whole point of the release is that the gateway is what holds the credential and the agent's environment holds none (AB1b, D3009)"
    return 0
  fi
  fact gateway PASS "the execution of '${JOB}' that the post-update run ${NEW_ID:-<id not read>} landed on carries FUGARO_RTDB_URL and mounts no model credential: the run has a gateway, and the agent's environment holds no API key for want of one (image ${image:-<not read>})"
}

# record_ref prints the ref (slug/run-id) `fugaro runs ls --json` gives the run
# this check launched after the update, or nothing. It is the key of the run's
# record under the runs bucket, and it is read from the listing rather than
# built: the slug is a name this check does not know and must not guess.
record_ref() {
  (cd "$SANDBOX" && fugaro runs ls --json) >"$SCRATCH/runs-after.json" 2>"$SCRATCH/runs-after.json.err" || true
  python3 - "$SCRATCH/runs-after.json" "$NEW_ID" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
rows = doc if isinstance(doc, list) else doc.get("runs", []) or []
for r in rows:
    if not isinstance(r, dict) or not sys.argv[2]:
        continue
    ref = r.get("run", "")
    if r.get("run_id") == sys.argv[2] or ref.endswith("/" + sys.argv[2]):
        print(ref)
        break
PY
}

# record_read fetches the record of the ref into $SCRATCH/record.json. It is the
# read a person makes themselves ('gcloud storage cat gs://fugaro-runs-<project>/
# runs/<slug>/<run-id>/result.json'), with the installation's own project, and
# the same shape check-500.sh reads a record with.
record_read() { # record_read <ref>
  gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/runs/$1/result.json" --project="$GCP_PROJECT" \
    >"$SCRATCH/record.json" 2>"$SCRATCH/record.json.err"
}

# record_mode prints the budget mode the post-update run's own record carries, or
# nothing. The record's budget block is the session the run opened (its mode is
# what the run resolved), and its policy block's effective mode is the same
# reading one layer up: a record that carries the two reads its budget block,
# and one that carries only the policy block still names a mode.
record_mode() {
  python3 - "$SCRATCH/record.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
mode = (doc.get("budget") or {}).get("mode", "") or ""
if not mode:
    mode = ((doc.get("policy") or {}).get("effective") or {}).get("mode", "") or ""
print(mode)
PY
}

# record_mode_field names the field record_mode read the mode from, so the row
# says where its reading lives: a record that carries the mode exactly once, and
# a reader who wants to see it.
record_mode_field() {
  python3 - "$SCRATCH/record.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("record")
    sys.exit(0)
if (doc.get("budget") or {}).get("mode", ""):
    print("budget.mode")
elif ((doc.get("policy") or {}).get("effective") or {}).get("mode", ""):
    print("policy.effective.mode")
else:
    print("record")
PY
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
  # Two readings, and the second is U4's own. First the mode the JOB names: a
  # runner from before the release is handed its mode in FUGARO_BUDGET_MODE (the
  # repository's own apply writes it onto every job, v1.md §5.4 and §12), and the
  # upgrade re-applies the repository's job "so its job names its mode and its
  # database" (plan §9). A job the release re-applied and that still names no
  # mode is a finding, not a subject: the runner on it reads a mode from nowhere
  # and runs with the off the release removed (v1.md §10.1, D3009).
  local jobmode
  jobmode=$(job_env "$SCRATCH/job-after.json" | sed -n 's/^FUGARO_BUDGET_MODE=//p')
  # Then the run's OWN record, read from the runs bucket as you would read it:
  # the record is where a run writes the mode it resolved and the session it
  # opened (runstore.Record.Budget, and the policy block's effective mode), and
  # no JSON view of the CLI carries either — `runs ls --json` prints a row, and a
  # row has no budget block. The key is the one `runs ls` names: the row's own
  # `run` (slug/run-id) under the installation's runs bucket.
  local ref rmode field
  ref=$(record_ref)
  if [ -z "$ref" ]; then
    fact old-image UNVERIFIED "the job's image (${image}) predates this checkout, but the listing of runs names no ref (slug/run-id) for the post-update run ${NEW_ID:-<id not read>}, so its record was NOT read: the row that reads a pre-release runner's mode reads the run's own record, and the key of that record is the ref the listing carries"
    return 0
  fi
  if ! record_read "$ref"; then
    fact old-image UNVERIFIED "the job's image (${image}) predates this checkout, but gs://fugaro-runs-${GCP_PROJECT}/runs/${ref}/result.json could not be read, so the mode the post-update run resolved is NOT measured: $(head -c 300 "$SCRATCH/record.json.err" 2>/dev/null). A project whose config sets runs_bucket to another name is the one case where that is not this run's record; read the record yourself and record the row in the register's section"
    return 0
  fi
  rmode=$(record_mode)
  field=$(record_mode_field)
  case "$jobmode|$rmode" in
  \|*)
    fact old-image FAIL "the job '${JOB}', which the update re-applied (it now carries the database address), names NO FUGARO_BUDGET_MODE: a runner from before the release reads its mode from that variable, and a job that names none leaves it resolving none — the off the release removed (D3009) — on a job of an installation that now has a database. The upgrade re-applies a repository's job so its job names its mode and its database (plan §9, F20); the run's own record at runs/${ref}/result.json reads mode '${rmode:-<no mode>}'"
    ;;
  observe\|observe)
    fact old-image PASS "the job's image (${image}) predates this checkout, the re-applied job names FUGARO_BUDGET_MODE 'observe', and the run launched on it recorded budget mode 'observe' (its record's own ${field}, at runs/${ref}/result.json): a runner from before the release under an explicit observe behaves as any observe run (C1, U4)"
    ;;
  *\|)
    fact old-image UNVERIFIED "the image (${image}) predates this checkout and the re-applied job names FUGARO_BUDGET_MODE '${jobmode}', but the post-update run's own record carries no mode at all — neither its budget block nor its policy block names one — so whether a pre-release runner runs as observe is NOT measured. Read runs/${ref}/result.json yourself and record the row in the register's section"
    ;;
  observe\|*)
    fact old-image FAIL "the post-update run recorded budget mode '${rmode}' (its record's own ${field}, at runs/${ref}/result.json), not the 'observe' U4 is about, under a job whose FUGARO_BUDGET_MODE is 'observe': a runner resolving a mode of its own, which C1 does not allow. An installation whose mode is deliberately '${rmode}' is not the installation this check is about — the mode it lands on is the one the upgrade wrote ('off' becomes 'observe', the plan's after-deploy steps)"
    ;;
  *)
    fact old-image UNVERIFIED "the image (${image}) predates this checkout and the run launched on it recorded budget mode '${rmode}' (its record's own ${field}, at runs/${ref}/result.json), but the job names FUGARO_BUDGET_MODE '${jobmode}': U4 is the reading that a runner from before the release, under an explicit 'observe', behaves as any observe run, and this installation is not in it — run the check on one whose mode is observe, which is what the upgrade writes for an installation that had none"
    ;;
  esac
}

step_operator() {
  if [ -z "$OPERATOR_CONFIG" ]; then
    fact operator UNVERIFIED "no --operator-config was given, so the second installation's operator update is NOT measured (U1): the row that reads whether an operator's roles suffice and whether their update says a project owner must run it needs a SECOND installation at the previous release"
    return 0
  fi
  say "running the operator's 'fugaro update' on the second installation"
  local operator operator_project operator_region
  operator=$(config_key "$OPERATOR_CONFIG" name)
  operator_project=$(config_key "$OPERATOR_CONFIG" gcp_project)
  operator_region=$(config_key "$OPERATOR_CONFIG" region)
  if ! (cd "$SANDBOX" && fugaro update --config "$OPERATOR_CONFIG") >"$SCRATCH/operator.out" 2>&1; then
    fact operator FAIL "the operator's 'fugaro update --config ${OPERATOR_CONFIG}' did not exit 0: $(head -c 500 "$SCRATCH/operator.out" 2>/dev/null). An operator's update on an installation with no database must skip that stage and say who must run it, never fail"
    return 0
  fi
  if ! grep -qi 'project owner' "$SCRATCH/operator.out"; then
    fact operator FAIL "the operator's update exited 0 without naming a project owner: an installation with no budget database launches nothing until a project owner runs the update (D3023), and an update that says nothing about it leaves the operator with an installation that silently launches nothing"
    return 0
  fi
  # "Changed nothing" is read against the SECOND installation's own project, from
  # the config the operator's update was run with (its gcp_project and region; a
  # local config carries no job name, and one of the first installation's would
  # be a reading of the wrong project). An operator cannot create the database,
  # so no job of that project may carry a database address.
  if [ -z "$operator_project" ]; then
    fact operator UNVERIFIED "the operator's update exited 0 and says a project owner must run it, but ${OPERATOR_CONFIG} names no gcp_project, so whether the second installation was CHANGED is NOT measured: read its own jobs ('gcloud run jobs list --project <its project>') for a FUGARO_RTDB_URL and record it in the register's section"
    return 0
  fi
  gcloud run jobs list --project "$operator_project" ${operator_region:+--region "$operator_region"} --format=json >"$SCRATCH/operator-jobs.json" 2>"$SCRATCH/operator-jobs.json.err" || {
    fact operator UNVERIFIED "the operator's update exited 0 and says a project owner must run it, but the second installation's jobs could not be listed in ${operator_project}${operator_region:+/${operator_region}}, so whether it was CHANGED is NOT measured: $(head -c 300 "$SCRATCH/operator-jobs.json.err" 2>/dev/null)"
    return 0
  }
  if printf '%s\n' "$(job_env "$SCRATCH/operator-jobs.json")" | grep -q '^FUGARO_RTDB_URL='; then
    fact operator FAIL "the operator's update CHANGED the second installation: a job of ${operator_project} now carries a database address, which an operator's roles must not be able to create. An operator's update on an installation with no database skips that stage and changes nothing (U1)"
    return 0
  fi
  fact operator PASS "the operator's update on '${operator:-the second installation}' exited 0, says a project owner must run it, and left every job of ${operator_project} without a database address: an operator's roles do not suffice, and the update says so rather than failing (U1, D3023)"
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
