#!/usr/bin/env bash
# Check 632 — convergence and the run in flight, on a real installation: an
# installation at the previous version, a run launched and still going when
# `fugaro update` starts, the four halves converged by that one command with no
# operator step beside it, and the run finishing undisturbed with its ledger,
# its counters and its record intact.
#
# Plan task MC26 writes this check; its subject is the sentence MC26 gives
# number 632 in docs/model-crafts/activation-plan.md.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to: its `project:` line out
#     of `fugaro doctor --project <name> --all` — the line the `--all` view
#     alone prints, and the same read without `--all` carries none;
#   * **whether the installation is behind this CLI** (`fugaro doctor --json`):
#     a check whose `fix` names `fugaro update` is the acceptance's premise, the
#     installation below this release; a doctor with no such check says the
#     installation is already current, and the update then converges nothing;
#   * **what the installation held before the update**, read only: the policy
#     node's version and its three blocks (`fugaro project policy show --json`),
#     the coder craft's model (`fugaro craft ls --json`), the pools and their
#     members (`fugaro accounts pool ls`) and the credentials and their serving
#     paths (`fugaro accounts ls --json`);
#   * **one sandbox run**, launched from the checkout with `fugaro run --json`,
#     still going when the update starts (`fugaro runs ls --json`), and followed
#     to its own terminal state after it;
#   * **the update itself** (`fugaro update`, the acceptance's verb): it exits 0,
#     its own output carries the one in-flight line (`N runs in flight: update
#     waits for none and changes nothing they hold`) whose count matches the runs
#     this check knows are in flight, and no line of it asks the owner to run
#     another `fugaro` command to finish the upgrade;
#   * **the four halves converged**, each read back with a read alone and nothing
#     written: the coder craft's model (`fugaro craft ls --json`), the policy
#     node carrying its crafts blocks with a version that did not go down
#     (`fugaro project policy show --json`), no pool naming members of both
#     halves (`fugaro accounts pool ls`, and the config's own pools), and every
#     credential with its type and serving path (`fugaro accounts ls --json`);
#   * **the run undisturbed**: it reached a terminal state after the update, its
#     ledger still accounts for the same run (`fugaro runs show --json`, whose
#     chain `used` is the ledger's held figure), its counters are monotone
#     (money, tokens and time never reset), and its record (`result.json` in the
#     runs bucket) carries the same run id, task and repository with an outcome.
#
# Nothing here reads a credential value: names, types, paths, versions and the
# words of the update's own note only.
#
# Usage:      bash scripts/live-checks/check632.sh --sandbox <a sandbox repository checkout> [--project <name>]
#             bash scripts/live-checks/check632.sh --sandbox <path> --preflight
#             bash scripts/live-checks/check632.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check632.sh --help
#             --preflight makes the check's own READS and evaluates every
#             precondition before anything is written or launched (the
#             installation, whether it is behind this CLI and what it held
#             before the update — the node, the coder craft, the pools and the
#             credentials), prints every blocker at once with the command that
#             clears it, writes nothing and launches nothing, and exits non-zero
#             when anything blocks.
# Needs:      bash, mktemp, date, python3, awk, sed, grep, gcloud (authenticated
#             as YOU, able to read the runs bucket) and fugaro (the installed
#             release, an admin of the project: `fugaro update` reaches the
#             cloud as you).
# Where:      your own machine, the sandbox project and the sandbox repository
#             only, at your own terminal: a coding agent's session is refused by
#             name, as is a project or region naming belong.
# Creates:    one sandbox run with its branch and the pull request it opens, and
#             `fugaro update` over the installation (which converges it and is
#             not undone).
# Costs:      one sandbox run and what its task costs, on the sandbox's own
#             budget, plus the wall clock of waiting for the run and of the
#             update. Every other call is a read.
# Runs:       by YOU. No agent runs any step of it. Its own test
#             (check632_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's branch and its pull request, and
#             the update's own writes, are NOT undone: the Restore row says what
#             they are and who declines them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 632 RESULT,
#             then CHECK 632 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure.
#
# Knobs (all CHECK632_*): DRY_RUN, PREFLIGHT, KEEP_SCRATCH, SANDBOX, PROJECT, TASK and
# RECIPE (what the run is launched with), REGION (us-east5), LOG_VIEW (the view
# a log read would name; no measurement here reads a log), RUN_TIMEOUT_SECONDS
# (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), DOCTOR_TIMEOUT_SECONDS
# (300), LAUNCH_TIMEOUT_SECONDS (600) and UPDATE_TIMEOUT_SECONDS (1800).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads a live
# project, and a trace would copy its output a second time.
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

# The shared `--preflight` driver (scripts/live-checks/lib/preflight.sh).
live_check_lib_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$live_check_lib_dir}
# shellcheck source=lib/preflight.sh
. "$LIVE_CHECK_LIB/preflight.sh"
PREFLIGHT_TOOL=check632

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 632 needs bash"
  local t
  for t in mktemp date python3 awk sed grep fugaro gcloud; do
    command -v "$t" >/dev/null 2>&1 || die "Check 632 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk632-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
INSTALLATION=""
GCP_PROJECT=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
UPDATED=no
IN_FLIGHT=no
TERMINAL=no
NODE_VERSION_BEFORE=""
CROSSED_BEFORE=""
COUNTERS_BEFORE=""
SHOW_BEFORE_OK=no
KNOWN_IN_FLIGHT=0

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk632.XXXXXXXX") || die "Check 632 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
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
    say "CHECK632_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The reader: the installation's own reads (its config's credentials and pools,
# a published node's crafts blocks, the crafts view, the update doctor's checks,
# a run's listing rows, its chain's limits, and its own record). Names, types,
# paths, versions, counts and figures only — never a credential value.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def text(value):
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    return json.dumps(value, sort_keys=True)


def flag(value):
    return "true" if value else "false"


PATHS = {
    "claude-oauth": "credsvc",
    "anthropic": "credsvc",
    "openrouter": "runner",
    "vertex": "runner",
}


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "launch":
    print("project=%s" % (doc.get("project") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "doctorjson":
    rel = doc.get("release") or {}
    print("release_tag=%s" % (rel.get("tag") or ""))
    print("release_commit=%s" % (rel.get("commit") or ""))
    print("ok=%s" % flag(doc.get("ok")))
    print("error=%s" % (doc.get("error") or ""))
    checks = doc.get("checks") or []
    print("checks=%d" % len(checks))
    for c in checks:
        print("check\t%s\t%s\t%s\t%s\t%s" % (
            c.get("id") or "", c.get("severity") or "", flag(c.get("ok")),
            c.get("problem") or "", c.get("fix") or ""))
elif what == "node":
    # `fugaro project policy show --json` prints {"project": ..., "policy": ...}:
    # every block of the node is a member of `policy`, and read at the top level
    # each one is null — a node row built from those nulls calls a correct
    # installation broken.
    policy = doc.get("policy")
    policy = policy if isinstance(policy, dict) else {}
    pools = policy.get("craft_pools") or []
    creds = policy.get("craft_credentials") or []
    crafts = policy.get("crafts") or {}
    print("version=%s" % policy.get("version", ""))
    print("pools=%d" % len(pools))
    print("credentials=%d" % len(creds))
    print("crafts=%d" % len(crafts))
    for p in pools:
        print("pool\t%s\t%s\t%s" % (
            p.get("name") or "", p.get("strategy") or "",
            ",".join(p.get("members") or [])))
    for c in creds:
        print("cred\t%s\t%s" % (c.get("name") or "", c.get("type") or ""))
    for name in sorted(crafts):
        row = crafts[name] or {}
        print("nodecraft\t%s\t%s\t%s" % (
            name, row.get("model") or "", row.get("access") or ""))
elif what == "crafts":
    print("project=%s" % (doc.get("project") or ""))
    rows = doc.get("crafts") or []
    print("count=%d" % len(rows))
    for r in rows:
        print("craft\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            r.get("craft") or "", r.get("model") or "", r.get("model_from") or "",
            r.get("access") or "", r.get("access_from") or "",
            flag(r.get("inert")), flag(r.get("model_servable")),
            r.get("model_problem") or ""))
elif what == "creds":
    creds = doc.get("credentials") or []
    print("count=%d" % len(creds))
    print("complete=%s" % flag(doc.get("complete", True)))
    print("note=%s" % (doc.get("note") or ""))
    for c in creds:
        print("cred\t%s\t%s\t%s" % (
            c.get("name") or "", c.get("type") or "", c.get("serving_path") or ""))
elif what == "rows":
    print("project=%s" % (doc.get("project") or ""))
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for r in rows:
        cost = r.get("cost") or {}
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            r.get("run_id") or "", r.get("run") or "", r.get("status") or "",
            flag(r.get("terminal")), r.get("stage") or "", r.get("title") or "",
            r.get("outcome") or "", r.get("repo") or "",
            cost.get("model_usd", 0), cost.get("total_usd", 0),
            cost.get("compute_usd", 0), cost.get("cache_read_tokens", 0),
            cost.get("cache_write_tokens", 0)))
elif what == "show":
    print("chain=%s" % (doc.get("chain") or ""))
    print("run_count=%s" % doc.get("run_count", 0))
    print("enforced=%s" % flag(doc.get("enforced")))
    print("limits_read=%s" % flag(doc.get("limits_read")))
    limits = doc.get("limits") or []
    print("limits=%d" % len(limits))
    for l in limits:
        used = l.get("used")
        remaining = l.get("remaining")
        print("limit\t%s\t%s\t%s\t%s\t%s" % (
            l.get("dimension") or "", l.get("limit", ""),
            "" if used is None else used, "" if remaining is None else remaining,
            l.get("source") or ""))
elif what == "configshow":
    values = doc.get("values") or []
    print("count=%d" % len(values))
    pools = 0
    for v in values:
        path = v.get("path") or ""
        if path == "pools" or path.startswith("pools."):
            pools += 1
        print("value\t%s\t%s\t%s" % (path, text(v.get("value")), v.get("source") or ""))
    print("pools=%d" % pools)
elif what == "record":
    cost = doc.get("cost") or {}
    pr = doc.get("pr") or {}
    stages = doc.get("stages") or []
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("workflow=%s" % (doc.get("workflow") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("stage=%s" % (doc.get("stage") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("reason=%s" % (doc.get("reason") or ""))
    print("pr=%s" % (pr.get("url") or ""))
    print("execution=%s" % (doc.get("execution") or ""))
    print("branch=%s" % (doc.get("branch") or ""))
    print("finished_at=%s" % (doc.get("finished_at") or ""))
    print("model_usd=%s" % cost.get("model_usd", 0))
    print("total_usd=%s" % cost.get("total_usd", 0))
    print("compute_usd=%s" % cost.get("compute_usd", 0))
    print("cache_read_tokens=%s" % cost.get("cache_read_tokens", 0))
    print("cache_write_tokens=%s" % cost.get("cache_write_tokens", 0))
    print("stages=%d" % len(stages))
    total = 0.0
    for st in stages:
        total += float(st.get("duration_s") or 0)
        print("stage\t%s\t%s" % (st.get("name") or "", st.get("duration_s", 0)))
    print("duration_s=%s" % total)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY
  [ -f "$1" ] || return 0
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
  return 0
}

table_field() { # table_field FILE KIND NAME COLUMN
  [ -f "$1" ] || return 0
  awk -F'\t' -v kind="$2" -v name="$3" -v col="$4" '$1 == kind && $2 == name { print $col; exit }' "$1" 2>/dev/null
  return 0
}

# row_field FILE COLUMN — one column of the run this check launched, out of a
# `fugaro runs ls --json` read this check made (the rows' run_id is column 2).
row_field_of() { # row_field_of FILE COLUMN
  [ -f "$1" ] || return 0
  awk -F'\t' -v id="$RUN_ID" -v col="$2" '$1 == "row" && $2 == id { print $col; exit }' "$1" 2>/dev/null
  return 0
}

# A pool's row out of `fugaro accounts pool ls`, which prints a tabwriter table
# padded with spaces: NAME, STRATEGY, MEMBERS (comma and space) and the half
# that serves them. "" when there is no such pool.
pool_ls_row() { # pool_ls_row FILE POOL
  [ -f "$1" ] || return 0
  awk -F'  +' -v want="$2" '$1 == want { print; exit }' "$1" 2>/dev/null
  return 0
}

# crossing_pools FILE: every pool of the listing whose members span the two
# halves, as "name<TAB>members", from the SERVED BY column the listing pads with
# " and " for exactly such a pool (accountsPoolServingText).
crossing_pools() { # crossing_pools FILE
  [ -f "$1" ] || return 0
  awk -F'  +' '
    $1 == "NAME" { next }
    $1 != "" && $4 ~ / and / { printf "%s\t%s\n", $1, $3 }
  ' "$1" 2>/dev/null
  return 0
}

# mono BEFORE AFTER: 0 when AFTER is not below BEFORE (a counter that did not go
# down), 1 when it did. A missing figure is never a drop.
mono() { # mono BEFORE AFTER
  awk -v b="$1" -v a="$2" 'BEGIN {
    if (b == "" || a == "") exit 0
    exit (a + 0 >= b + 0) ? 0 : 1
  }'
}

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

run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# run_fresh: run_bounded with the two output files truncated first, for a step
# whose call is repeated in a poll — a plain run_bounded appends, and two
# concatenated documents are no document.
run_fresh() { # run_fresh SECONDS KEY DIR CMD...
  local secs=$1 key=$2
  : >"$SCRATCH/$key.out" 2>/dev/null || true
  : >"$SCRATCH/$key.err" 2>/dev/null || true
  run_bounded "$secs" "$key" "${@:3}"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check632.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy an update or a run's record to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 632 launches a real sandbox run and runs 'fugaro update' over a live installation — a write that under this marker changes nothing outside this machine — so it is run at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${PROJECT}${GCP_PROJECT}${REGION}" in
  *belong*) stop "the project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "neither the project this check names nor the region it runs in is belong (the account it runs as is gcloud's own and is never read here)"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release whose update this check measures)"
}

# ---------------------------------------------------------------- preflight
# step_preflight: --preflight. The reads the check makes before it launches a run
# or runs the update — the installation, whether it is behind this CLI, and what
# it held before the update (the node, the coder craft, the pools and the
# credentials) — with every precondition evaluated and nothing written: no run
# launched, no update run, no read of a credential value.
step_preflight() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and both the run and the update are made from it"
  fi
  local named
  named=$(sed -n 's/^project:[[:space:]]*//p' "$SANDBOX/fugaro.yaml" | head -n 1)
  PROJECT_ARG=${PROJECT_ARG:-$named}
  if [ -z "$PROJECT_ARG" ]; then
    preflight_block installation \
      "$SANDBOX/fugaro.yaml names no project and no --project was given, and both the run and the update are made from the installation it names" \
      "cd $SANDBOX && fugaro config show --json"
  fi

  say "preflight: reading the installation: fugaro doctor --project ${PROJECT_ARG} --all, in ${SANDBOX}"
  if [ -z "$PROJECT_ARG" ]; then
    preflight_block installation \
      "fugaro doctor --project needs the installation's own name, which neither the checkout nor --project names" \
      "cd $SANDBOX && fugaro config show --json"
  else
    run_fresh "$DOCTOR_TIMEOUT" pf-doctor "$SANDBOX" "$FUGARO_BIN" doctor --project "$PROJECT_ARG" --all
    local line
    line=$(grep -m1 '^project: ' "$SCRATCH/pf-doctor.out" 2>/dev/null || true)
    local gcp=""
    if [ -n "$line" ]; then
      gcp=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
    fi
    case "${gcp:-$PROJECT_ARG}" in
    *belong*) stop "the checkout at $SANDBOX belongs to ${gcp:-$PROJECT_ARG}: this check is never run there" ;;
    esac
    INSTALLATION=${PROJECT_ARG}
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ -z "$line" ]; then
      preflight_block installation \
        "fugaro doctor --project ${PROJECT_ARG} --all does not answer for $SANDBOX (exit ${BOUNDED_STATUS}), so the installation this check launches a run in and updates cannot be read" \
        "cd $SANDBOX && fugaro doctor --project $PROJECT_ARG --all"
    else
      PROJECT=$(printf '%s' "$line" | sed -n 's/^project: \([^ ]*\) .*/\1/p')
      preflight_note installation "the installation is ${PROJECT:-$PROJECT_ARG} (GCP ${gcp:-unknown}), read from fugaro doctor --project ${PROJECT_ARG} --all in ${SANDBOX}"
    fi
  fi

  if [ -n "$PROJECT_ARG" ]; then
    say "preflight: reading whether the installation is behind this CLI: fugaro doctor --project ${PROJECT_ARG} --json, in ${SANDBOX}"
    run_fresh "$DOCTOR_TIMEOUT" pf-doctorjson "$SANDBOX" "$FUGARO_BIN" doctor --project "$PROJECT_ARG" --json
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] ||
      ! read_json doctorjson "$SCRATCH/pf-doctorjson.out" "$SCRATCH/pf-doctorjson.txt"; then
      preflight_block version-before \
        "fugaro doctor --json does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and the check whose fix names 'fugaro update' is the acceptance's premise" \
        "cd $SANDBOX && fugaro doctor --project $PROJECT_ARG --json"
    else
      local release ids
      release=$(field "$SCRATCH/pf-doctorjson.txt" release_tag)
      ids=$(awk -F'\t' '$1 == "check" && $6 ~ /fugaro update/ { printf "%s(%s) ", $2, $3 }' "$SCRATCH/pf-doctorjson.txt")
      if [ -z "$ids" ]; then
        preflight_note version-before "fugaro doctor --json names no check whose fix is 'fugaro update' (release ${release:-unknown}), so this installation is not reported behind this CLI and the update has nothing to converge"
      else
        preflight_note version-before "the installation is behind this CLI (release ${release:-unknown}): the check(s) whose fix is 'fugaro update' are ${ids}"
      fi
    fi
  fi

  say "preflight: reading what the installation held before the update (policy node, coder craft, pools, credentials), in ${SANDBOX}"
  local node_ok=no crafts_ok=no pools_ok=no creds_ok=no
  run_fresh "$CLI_TIMEOUT" pf-node "$SANDBOX" "$FUGARO_BIN" project policy show --json
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
    read_json node "$SCRATCH/pf-node.out" "$SCRATCH/pf-node.txt"; then node_ok=yes; fi
  run_fresh "$CLI_TIMEOUT" pf-crafts "$SANDBOX" "$FUGARO_BIN" craft ls --json
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
    read_json crafts "$SCRATCH/pf-crafts.out" "$SCRATCH/pf-crafts.txt"; then crafts_ok=yes; fi
  run_fresh "$CLI_TIMEOUT" pf-pools "$SANDBOX" "$FUGARO_BIN" accounts pool ls
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ]; then pools_ok=yes; fi
  run_fresh "$CLI_TIMEOUT" pf-creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
    read_json creds "$SCRATCH/pf-creds.out" "$SCRATCH/pf-creds.txt"; then creds_ok=yes; fi
  [ "$node_ok" = yes ] ||
    preflight_block before \
      "fugaro project policy show --json does not answer in ${SANDBOX}, and the node's version before the update is half of the publish convergence" \
      "cd $SANDBOX && fugaro project policy show --json"
  [ "$crafts_ok" = yes ] ||
    preflight_block before \
      "fugaro craft ls --json does not answer in ${SANDBOX}, and the coder craft's model before the update is half of the model convergence" \
      "cd $SANDBOX && fugaro craft ls --json"
  [ "$pools_ok" = yes ] ||
    preflight_block before \
      "fugaro accounts pool ls does not answer in ${SANDBOX}, and the pools before the update are half of the crossing convergence" \
      "cd $SANDBOX && fugaro accounts pool ls"
  [ "$creds_ok" = yes ] ||
    preflight_block before \
      "fugaro accounts ls --json does not answer in ${SANDBOX}, and the credentials before the update are half of the key convergence" \
      "cd $SANDBOX && fugaro accounts ls --json"
  if [ "$node_ok" = yes ] && [ "$crafts_ok" = yes ] && [ "$pools_ok" = yes ] && [ "$creds_ok" = yes ]; then
    local ver cm
    ver=$(field "$SCRATCH/pf-node.txt" version)
    cm=$(table_field "$SCRATCH/pf-crafts.txt" craft coder 3)
    preflight_note before "before the update, ${INSTALLATION:-the installation} held: the policy node at version ${ver:-unknown}, the coder craft runs ${cm:-no declared model}, and $(field "$SCRATCH/pf-creds.txt" count) credential(s) — every figure a read, and no credential value read"
  fi
}

# ---------------------------------------------------------------- the steps
# step_installation: the installation the checkout belongs to, out of the one
# read that prints its project line — `fugaro doctor --project <name> --all`.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and both the run and the update are made from it"
  fi
  local named
  named=$(sed -n 's/^project:[[:space:]]*//p' "$SANDBOX/fugaro.yaml" | head -n 1)
  PROJECT_ARG=${PROJECT_ARG:-$named}
  if [ -z "$PROJECT_ARG" ]; then
    unmeasured installation "the checkout at $SANDBOX names no project (its fugaro.yaml carries no 'project:' line) and no --project was given: the project line is what tells this check which installation it is reading, and both the run and the update are made from it"
    return 0
  fi
  say "reading the installation: fugaro doctor --project ${PROJECT_ARG} (then --all), in ${SANDBOX}"
  run_fresh "$DOCTOR_TIMEOUT" doctor-plain "$SANDBOX" "$FUGARO_BIN" doctor --project "$PROJECT_ARG"
  local plain_line
  plain_line=$(grep -m1 '^project: ' "$SCRATCH/doctor-plain.out" 2>/dev/null || true)
  run_fresh "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --project "$PROJECT_ARG" --all
  local line
  line=$(grep -m1 '^project: ' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$line" ]; then
    unmeasured installation "fugaro doctor --project ${PROJECT_ARG} --all printed no 'project: <name> (GCP <project>, <region>)' line in ${SANDBOX} (exit ${BOUNDED_STATUS}): the project line is what tells this check which installation it is reading. What it printed is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(printf '%s' "$line" | sed -n 's/^project: \([^ ]*\) .*/\1/p')
  GCP_PROJECT=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
  if [ -z "$PROJECT" ] || [ -z "$GCP_PROJECT" ]; then
    unmeasured installation "the project line '${line}' is not the shape this check reads (project: <name> (GCP <project>, <region>)): what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$GCP_PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${GCP_PROJECT}: this check is never run there" ;;
  esac
  INSTALLATION=$PROJECT
  local all_note="the same read without --all carries no project: line, so --all is what prints it"
  [ -z "$plain_line" ] || all_note="the read without --all carries a project: line too, which the acceptance says it does not"
  fact installation PASS "${SANDBOX} is a checkout of the installation ${PROJECT} (GCP ${GCP_PROJECT}), read from fugaro doctor --project ${PROJECT_ARG} --all; ${all_note}: the run is launched in it and the update is run from it"
}

# step_version_before: whether the installation is behind this CLI — the
# acceptance's premise, read out of `fugaro doctor --json`.
step_version_before() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured version-before "there is no installation whose version could be read: the row above says why"
    return 0
  fi
  say "reading whether the installation is behind this CLI: fugaro doctor --json, in ${SANDBOX}"
  run_fresh "$DOCTOR_TIMEOUT" doctorjson "$SANDBOX" "$FUGARO_BIN" doctor --project "$PROJECT_ARG" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured version-before "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the check whose fix names 'fugaro update' is the acceptance's premise. What it printed is in $SCRATCH/doctorjson.err"
    return 0
  fi
  if ! read_json doctorjson "$SCRATCH/doctorjson.out" "$SCRATCH/doctorjson.txt"; then
    unmeasured version-before "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctorjson.out"
    return 0
  fi
  local release
  release=$(field "$SCRATCH/doctorjson.txt" release_tag)
  local ids=""
  ids=$(awk -F'\t' '$1 == "check" && $6 ~ /fugaro update/ { printf "%s(%s) ", $2, $3 }' "$SCRATCH/doctorjson.txt")
  if [ -z "$ids" ]; then
    fact version-before INFO "fugaro doctor --json names no check whose fix is 'fugaro update' (release ${release:-unknown}), so the installation is not reported behind this CLI: the acceptance's convergence is about an installation at the previous version, and this one has nothing for the update to converge — the update below then converges nothing, which is itself the answer"
    return 0
  fi
  fact version-before PASS "fugaro doctor --json names the installation behind this CLI (release ${release:-unknown}): the check(s) whose fix is 'fugaro update' are ${ids}- each severity and version word above — which is the acceptance's premise, an installation at the previous version that the update below converges"
}

# step_before: what the installation held before the update, read only.
step_before() {
  if [ -z "$INSTALLATION" ]; then
    fact before INFO "there is no installation whose state could be read before the update: the installation row above says why"
    return 0
  fi
  say "reading what the installation held before the update (policy node, coder craft, pools, credentials), in ${SANDBOX}"
  run_fresh "$CLI_TIMEOUT" node-before "$SANDBOX" "$FUGARO_BIN" project policy show --json
  run_fresh "$CLI_TIMEOUT" crafts-before "$SANDBOX" "$FUGARO_BIN" craft ls --json
  run_fresh "$CLI_TIMEOUT" pools-before "$SANDBOX" "$FUGARO_BIN" accounts pool ls
  run_fresh "$CLI_TIMEOUT" creds-before "$SANDBOX" "$FUGARO_BIN" accounts ls --json
  local node_words="the node could not be read"
  if [ -f "$SCRATCH/node-before.out" ] && read_json node "$SCRATCH/node-before.out" "$SCRATCH/node-before.txt" 2>/dev/null; then
    NODE_VERSION_BEFORE=$(field "$SCRATCH/node-before.txt" version)
    node_words="the policy node was at version ${NODE_VERSION_BEFORE:-unknown} with $(field "$SCRATCH/node-before.txt" credentials) craft_credential(s), $(field "$SCRATCH/node-before.txt" pools) craft_pool(s) and $(field "$SCRATCH/node-before.txt" crafts) craft(s)"
  fi
  local craft_words="no coder craft model was declared"
  if [ -f "$SCRATCH/crafts-before.out" ] && read_json crafts "$SCRATCH/crafts-before.out" "$SCRATCH/crafts-before.txt" 2>/dev/null; then
    local cm
    cm=$(table_field "$SCRATCH/crafts-before.txt" craft coder 3)
    [ -z "$cm" ] || craft_words="the coder craft ran ${cm} (from $(table_field "$SCRATCH/crafts-before.txt" craft coder 4))"
  fi
  local pool_words="no pool was declared"
  CROSSED_BEFORE=$(crossing_pools "$SCRATCH/pools-before.out")
  local pool_rows
  pool_rows=$(awk -F'  +' '$1 != "" && $1 != "NAME" { printf "%s(%s) ", $1, $3 }' "$SCRATCH/pools-before.out" 2>/dev/null || true)
  [ -z "$pool_rows" ] || pool_words="the pools were ${pool_rows}"
  local cred_words="no credential was declared"
  if [ -f "$SCRATCH/creds-before.out" ] && read_json creds "$SCRATCH/creds-before.out" "$SCRATCH/creds-before.txt" 2>/dev/null; then
    local n
    n=$(field "$SCRATCH/creds-before.txt" count)
    if [ "${n:-0}" != 0 ]; then
      cred_words="the credentials were $(awk -F'\t' '$1 == "cred" { printf "%s(%s, %s) ", $2, $3, $4 }' "$SCRATCH/creds-before.txt")"
    fi
  fi
  local cross_note=""
  [ -z "$CROSSED_BEFORE" ] || cross_note=" — and one named members of both halves: $(printf '%s' "$CROSSED_BEFORE" | tr '\n' ' ')"
  fact before INFO "before the update, ${INSTALLATION} held: ${node_words}; ${craft_words}; ${pool_words}; ${cred_words}${cross_note}. Every figure here is a read (fugaro project policy show --json, fugaro craft ls --json, fugaro accounts pool ls and fugaro accounts ls --json) and no credential value was read"
}

# step_run: one sandbox run, from the checkout.
step_run() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured run-launched "there is no installation to launch a run in: the row above says why"
    return 0
  fi
  local run_args=(run --repo "$SANDBOX")
  [ -z "$RECIPE" ] || run_args=(run --repo "$SANDBOX" --recipe "$RECIPE")
  run_args+=(--json "$TASK")
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_fresh "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" "${run_args[@]}"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the run this check follows to the update never started. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt" 2>/dev/null; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch names the run this check follows. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout: the rows below follow this run and no other, and the update below meets it in flight"
}

# step_run_in_flight: the run is not terminal when the update starts.
step_run_in_flight() {
  if [ -z "$RUN_ID" ]; then
    unmeasured run-in-flight "there is no run to follow into the update: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to appear and reading its state (fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 5)"
  local waited=0 appeared=no ended="" run_status=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_fresh "$CLI_TIMEOUT" rows-inflight "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 5
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows-inflight.out" "$SCRATCH/rows-inflight.txt" 2>/dev/null; then
      if [ -n "$(row_field_of "$SCRATCH/rows-inflight.txt" 2)" ]; then
        appeared=yes
        ended=$(row_field_of "$SCRATCH/rows-inflight.txt" 5)
        run_status=$(row_field_of "$SCRATCH/rows-inflight.txt" 4)
        break
      fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$appeared" != yes ]; then
    unmeasured run-in-flight "${RUN_ID} had not appeared in fugaro runs ls --repo ${LAUNCH_REPO} --json after ${RUN_TIMEOUT}s: the check waits for the run it launched before it updates, and it never named one. Watch it with 'fugaro watch ${RUN_ID}' and run the check again"
    return 0
  fi
  if [ "$ended" = true ]; then
    unmeasured run-in-flight "${RUN_ID} had already ended (status=${run_status:-unknown}) by the time it appeared in the listing, so it was not in flight when the update started: the acceptance's clause is about a run going while the update runs, and this one ended too early. Raise CHECK632_TASK so the run works long enough, and run the check again"
    return 0
  fi
  IN_FLIGHT=yes
  KNOWN_IN_FLIGHT=$(awk -F'\t' '$1 == "row" && $5 != "true" { n++ } END { print n + 0 }' "$SCRATCH/rows-inflight.txt")
  COUNTERS_BEFORE=$(printf '%s,%s,%s,%s,%s' \
    "$(row_field_of "$SCRATCH/rows-inflight.txt" 10)" "$(row_field_of "$SCRATCH/rows-inflight.txt" 11)" \
    "$(row_field_of "$SCRATCH/rows-inflight.txt" 12)" "$(row_field_of "$SCRATCH/rows-inflight.txt" 13)" \
    "$(row_field_of "$SCRATCH/rows-inflight.txt" 14)")
  say "reading the run's ledger before the update: fugaro runs show --json ${RUN_ID}"
  run_fresh "$CLI_TIMEOUT" show-before "$SANDBOX" "$FUGARO_BIN" runs show --json "$RUN_ID"
  if [ "$BOUNDED_STATUS" = 0 ] && read_json show "$SCRATCH/show-before.out" "$SCRATCH/show-before.txt" 2>/dev/null; then
    SHOW_BEFORE_OK=yes
  fi
  fact run-in-flight PASS "${RUN_ID} is in flight (status=${run_status:-unknown}) when the update starts, read from fugaro runs ls --repo ${LAUNCH_REPO} --json: ${KNOWN_IN_FLIGHT} run(s) of this repository are still going, and the update is asked to leave them alone. Its ledger was read before the update ($([ "$SHOW_BEFORE_OK" = yes ] && echo 'fugaro runs show --json answered' || echo 'the ledger read did not answer, which the ledger row below names'))"
}

# step_update: the acceptance's verb, run from the checkout.
step_update() {
  if [ "$IN_FLIGHT" != yes ]; then
    unmeasured update "no run was in flight when the update would start (the row above says why), so an update here would not measure the acceptance's own clause — that a run going when it starts is undisturbed. Settle that row and run the check again"
    return 0
  fi
  say "converging the installation: fugaro update, in ${SANDBOX} (the acceptance's own verb; it converges all four halves and waits for no run)"
  run_fresh "$UPDATE_TIMEOUT" update "$SANDBOX" "$FUGARO_BIN" update --project "$PROJECT_ARG"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured update "fugaro update did not finish inside ${UPDATE_TIMEOUT}s and was ended: the convergence below is measured after it, so there is nothing to read. What it printed is in $SCRATCH/update.out and $SCRATCH/update.err"
    return 0
  fi
  local tail_lines
  tail_lines=$(tail -n 3 "$SCRATCH/update.out" 2>/dev/null | tr '\n' ' ' || true)
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact update FAIL "fugaro update exited ${BOUNDED_STATUS} in ${SANDBOX} with a run in flight: the acceptance's convergence is this command's own, so nothing below could be measured. Its last words are in $SCRATCH/update.err${tail_lines:+ (stdout: ${tail_lines})}"
    return 0
  fi
  UPDATED=yes
  fact update PASS "fugaro update converged ${INSTALLATION} from ${SANDBOX} and exited 0 while ${RUN_ID} was in flight${tail_lines:+ — its last lines: ${tail_lines}}"
}

# step_update_notes_runs: the update's own note about the runs in flight.
step_update_notes_runs() {
  if [ "$UPDATED" != yes ]; then
    unmeasured update-notes-runs "the update did not finish (its own row above says why), so there is no output of its own to read"
    return 0
  fi
  local phrase="in flight: update waits for none and changes nothing they hold"
  local line
  line=$(grep -m1 "$phrase" "$SCRATCH/update.out" 2>/dev/null || true)
  if [ -z "$line" ]; then
    if [ "$KNOWN_IN_FLIGHT" -ge 1 ] 2>/dev/null; then
      fact update-notes-runs FAIL "fugaro update printed no line carrying the in-flight note ('${phrase}') although it was given ${KNOWN_IN_FLIGHT} run(s) in flight: the acceptance's HARD RULE is that the update says so in one line — the count, and that it waits for none of them and changes nothing they hold. Its output is in $SCRATCH/update.out"
      return 0
    fi
    fact update-notes-runs INFO "fugaro update printed no in-flight line, and this check knows of no run in flight for it to name (the row above found none): the note appears only when a run is going"
    return 0
  fi
  local named
  named=$(printf '%s' "$line" | sed -n 's/.*[^0-9]\([0-9][0-9]*\) runs\? in flight: .*/\1/p')
  if [ -z "$named" ]; then
    fact update-notes-runs FAIL "the update's in-flight line '${line}' names no count this check could read: the acceptance's line is '<N> run(s) in flight: update waits for none and changes nothing they hold'"
    return 0
  fi
  if [ "$KNOWN_IN_FLIGHT" -ge 1 ] 2>/dev/null && [ "$named" -lt "$KNOWN_IN_FLIGHT" ] 2>/dev/null; then
    fact update-notes-runs FAIL "the update's in-flight line names ${named} run(s) where this check knows ${KNOWN_IN_FLIGHT} run(s) of this repository to be going: the count has to account for at least the runs in flight. Its line: ${line}"
    return 0
  fi
  local extra=""
  [ "$named" -eq "$KNOWN_IN_FLIGHT" ] 2>/dev/null || extra=" (it names more than this check's own listing saw, which is the installation's other runs)"
  fact update-notes-runs PASS "fugaro update printed its one in-flight line, '${line}': it names ${named} run(s) in flight${extra} and says it waits for none of them and changes nothing they hold — the acceptance's HARD RULE, in the update's own words"
}

# step_no_operator_step: the update exits 0 and every half below is read back
# with a read alone — nothing written, no other command asked for.
step_no_operator_step() {
  if [ "$UPDATED" != yes ]; then
    unmeasured no-operator-step "the update did not finish (its own row above says why), so there is no upgrade to read back"
    return 0
  fi
  local both="$SCRATCH/update-both.txt"
  cat "$SCRATCH/update.out" "$SCRATCH/update.err" >"$both" 2>/dev/null || true
  local bad
  bad=$(awk '
    / in flight: update waits for none and changes nothing they hold/ { next }
    /fugaro/ && /(to finish|to complete|then run|now run|next step|operator step)/ {
      if ($0 ~ /fugaro update/) next
      print; exit
    }
  ' "$both" 2>/dev/null || true)
  if [ -n "$bad" ]; then
    fact no-operator-step FAIL "the update's own output tells the owner to run another fugaro command to finish the upgrade: '${bad}'. The acceptance is that the update is the whole upgrade with no operator step beside it; a release must not ship one"
    return 0
  fi
  fact no-operator-step PASS "fugaro update exited 0 and printed no line asking for another command to finish the upgrade, and every half below is read back with a read alone — this check writing nothing and asking no other command to act: fugaro craft ls --json, fugaro project policy show --json, fugaro accounts pool ls, fugaro config show --json, fugaro accounts ls --json, fugaro runs ls --json, fugaro runs show --json and gcloud storage cat (the run's own record)"
}

# step_converge_model: the coder craft's model after the update.
step_converge_model() {
  if [ "$UPDATED" != yes ]; then
    unmeasured converge-model "the update did not finish (its own row above says why), so there is no converged craft to read"
    return 0
  fi
  say "reading the coder craft after the update: fugaro craft ls --json, in ${SANDBOX}"
  run_fresh "$CLI_TIMEOUT" crafts-after "$SANDBOX" "$FUGARO_BIN" craft ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured converge-model "fugaro craft ls --json did not answer after the update (exit ${BOUNDED_STATUS}): the model the coder craft runs is in that document. What it printed is in $SCRATCH/crafts-after.err"
    return 0
  fi
  if ! read_json crafts "$SCRATCH/crafts-after.out" "$SCRATCH/crafts-after.txt"; then
    unmeasured converge-model "fugaro craft ls --json printed a document this check could not read after the update: what it printed is in $SCRATCH/crafts-after.out"
    return 0
  fi
  local model from
  model=$(table_field "$SCRATCH/crafts-after.txt" craft coder 3)
  from=$(table_field "$SCRATCH/crafts-after.txt" craft coder 4)
  if [ -z "$model" ]; then
    fact converge-model INFO "fugaro craft ls --json names no model for the coder craft after the update, so the installation declares no craft model and the update had none to converge for this half; the crafts it does name are $(awk -F'\t' '$1 == "craft" { printf "%s ", $2 }' "$SCRATCH/crafts-after.txt")"
    return 0
  fi
  fact converge-model PASS "fugaro craft ls --json names the coder craft's model ${model} after the update (from ${from:-unknown}), its access $(table_field "$SCRATCH/crafts-after.txt" craft coder 5) (from $(table_field "$SCRATCH/crafts-after.txt" craft coder 6)): the model half of the convergence, read with a read alone and nothing written"
}

# step_converge_publish: the policy node read back carrying its crafts blocks.
step_converge_publish() {
  if [ "$UPDATED" != yes ]; then
    unmeasured converge-publish "the update did not finish (its own row above says why), so there is no converged node to read"
    return 0
  fi
  say "reading the policy node after the update: fugaro project policy show --json, in ${SANDBOX}"
  run_fresh "$CLI_TIMEOUT" node-after "$SANDBOX" "$FUGARO_BIN" project policy show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact converge-publish FAIL "the policy node of ${INSTALLATION} does not read back after fugaro update (exit ${BOUNDED_STATUS}): $(head -c 500 "$SCRATCH/node-after.err" 2>/dev/null | tr '\n' ' '). The node has to be one this build reads, or the publish half of the convergence did not happen. What it printed is in $SCRATCH/node-after.out and $SCRATCH/node-after.err"
    return 0
  fi
  if ! read_json node "$SCRATCH/node-after.out" "$SCRATCH/node-after.txt"; then
    fact converge-publish FAIL "fugaro project policy show --json printed a document this check could not read after the update: what it printed is in $SCRATCH/node-after.out"
    return 0
  fi
  local version pools creds crafts
  version=$(field "$SCRATCH/node-after.txt" version)
  pools=$(field "$SCRATCH/node-after.txt" pools)
  creds=$(field "$SCRATCH/node-after.txt" credentials)
  crafts=$(field "$SCRATCH/node-after.txt" crafts)
  if [ "${pools:-0}" = 0 ] && [ "${creds:-0}" = 0 ] && [ "${crafts:-0}" = 0 ]; then
    fact converge-publish INFO "the policy node of ${INSTALLATION} reads back at version ${version:-unknown} after the update and carries no crafts blocks (craft_pools, craft_credentials and crafts all empty), so there was no published half for the update to converge; every read of the node still answers, which is itself the publish half of the convergence"
    return 0
  fi
  if [ -n "$NODE_VERSION_BEFORE" ] && [ -n "$version" ] && [ "$version" -lt "$NODE_VERSION_BEFORE" ] 2>/dev/null; then
    fact converge-publish FAIL "the published node's version went DOWN over the update (${NODE_VERSION_BEFORE} before, ${version} after): the node's version moves forward with each step of the chain, never back"
    return 0
  fi
  fact converge-publish PASS "the policy node of ${INSTALLATION} reads back after the update with craft_credentials (${creds} entries), craft_pools (${pools}) and crafts (${crafts}) at version ${version:-unknown}, which did not go down from the ${NODE_VERSION_BEFORE:-unknown} read before it: the publish half of the convergence, read with a read alone"
}

# step_converge_crossing: no pool names members of both halves, read back.
step_converge_crossing() {
  if [ "$UPDATED" != yes ]; then
    unmeasured converge-crossing "the update did not finish (its own row above says why), so there are no converged pools to read"
    return 0
  fi
  say "reading the pools after the update: fugaro accounts pool ls, in ${SANDBOX}"
  run_fresh "$CLI_TIMEOUT" pools-after "$SANDBOX" "$FUGARO_BIN" accounts pool ls
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured converge-crossing "fugaro accounts pool ls did not answer after the update (exit ${BOUNDED_STATUS}): the pools the config holds now are in that listing. What it printed is in $SCRATCH/pools-after.err"
    return 0
  fi
  say "reading the config's own pools after the update: fugaro config show --json, in ${SANDBOX}"
  run_fresh "$CLI_TIMEOUT" config-after "$SANDBOX" "$FUGARO_BIN" config show --json
  local config_note=""
  if [ "$BOUNDED_STATUS" = 0 ] && read_json configshow "$SCRATCH/config-after.out" "$SCRATCH/config-after.txt" 2>/dev/null; then
    config_note=" and fugaro config show --json carries $(field "$SCRATCH/config-after.txt" pools) pool(s) of its own"
  fi
  local still
  still=$(crossing_pools "$SCRATCH/pools-after.out")
  if [ -n "$still" ]; then
    fact converge-crossing FAIL "a pool of ${INSTALLATION} still names members of both halves after fugaro update: $(printf '%s' "$still" | tr '\n' ' '). One path per pool is the rule, and an update that left a crossing pool left the convergence unfinished"
    return 0
  fi
  if [ -n "$CROSSED_BEFORE" ]; then
    local before_pool before_members svc_half run_half got_svc got_run
    before_pool=$(printf '%s\n' "$CROSSED_BEFORE" | awk -F'\t' 'NR == 1 { print $1 }')
    before_members=$(printf '%s\n' "$CROSSED_BEFORE" | awk -F'\t' 'NR == 1 { print $2 }' | tr -d ' ')
    local base=${before_pool%-claude}
    base=${base%-provider}
    svc_half="${base}-claude"
    run_half="${base}-provider"
    got_svc=$(pool_ls_row "$SCRATCH/pools-after.out" "$svc_half")
    got_run=$(pool_ls_row "$SCRATCH/pools-after.out" "$run_half")
    if [ -n "$got_svc" ] || [ -n "$got_run" ]; then
      fact converge-crossing PASS "the crossing pool ${before_pool} (${before_members}) held before the update is two pools now: ${got_svc:-${svc_half} absent} / ${got_run:-${run_half} absent}, each member on its own path — the acceptance's clause, read out of the config the update left (no pool names members of both halves)${config_note}"
      return 0
    fi
    fact converge-crossing PASS "the crossing pool ${before_pool} (${before_members}) held before the update is gone and no pool of ${INSTALLATION} names members of both halves now (the split's halves are not named ${svc_half}/${run_half}, so this check read the rule rather than the names)${config_note:-: the pools were read out of fugaro accounts pool ls, which is where this build keeps the config pools}"
    return 0
  fi
  fact converge-crossing PASS "no pool of ${INSTALLATION} names members of both halves after the update (one path per pool, read out of fugaro accounts pool ls)${config_note}: the convergence's crossing half, with no crossing to split on this installation"
}

# step_converge_key: every credential with its type and serving path.
step_converge_key() {
  if [ "$UPDATED" != yes ]; then
    unmeasured converge-key "the update did not finish (its own row above says why), so there are no converged credentials to read"
    return 0
  fi
  say "reading the credentials after the update: fugaro accounts ls --json, in ${SANDBOX}"
  run_fresh "$CLI_TIMEOUT" creds-after "$SANDBOX" "$FUGARO_BIN" accounts ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured converge-key "fugaro accounts ls --json did not answer after the update (exit ${BOUNDED_STATUS}): the credentials the config declares now, with their serving paths, are in that document. What it printed is in $SCRATCH/creds-after.err"
    return 0
  fi
  if ! read_json creds "$SCRATCH/creds-after.out" "$SCRATCH/creds-after.txt"; then
    unmeasured converge-key "fugaro accounts ls --json printed a document this check could not read after the update: what it printed is in $SCRATCH/creds-after.out"
    return 0
  fi
  local n
  n=$(field "$SCRATCH/creds-after.txt" count)
  if [ "${n:-0}" = 0 ]; then
    fact converge-key INFO "the installation ${INSTALLATION} declares no credential after the update, so the key half of the convergence had none to carry; fugaro accounts ls --json answered and named no entry"
    return 0
  fi
  fact converge-key PASS "fugaro accounts ls --json names every credential of ${INSTALLATION} after the update with its type and its serving path ($(awk -F'\t' '$1 == "cred" { printf "%s(%s, %s) ", $2, $3, $4 }' "$SCRATCH/creds-after.txt")): the key half of the convergence, names, types and paths only and no value"
}

# step_run_terminal: the run reached a terminal state after the update.
step_run_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured run-terminal "there is no run to follow: the run row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end after the update (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 5)"
  local waited=0 ended="" run_status="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_fresh "$CLI_TIMEOUT" rows-terminal "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 5
    ended="" run_status="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows-terminal.out" "$SCRATCH/rows-terminal.txt" 2>/dev/null; then
      run_status=$(row_field_of "$SCRATCH/rows-terminal.txt" 4)
      ended=$(row_field_of "$SCRATCH/rows-terminal.txt" 5)
      stage=$(row_field_of "$SCRATCH/rows-terminal.txt" 6)
      RUN_REF=$(row_field_of "$SCRATCH/rows-terminal.txt" 3)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured run-terminal "${RUN_ID} had not reached a terminal state after ${RUN_TIMEOUT}s from the update (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): the acceptance is a run that finishes undisturbed, and this one was still going when this check's poll budget of ${RUN_TIMEOUT}s ran out. Watch it with 'fugaro watch ${RUN_ID}' and run the check again when it has ended — the run is a real one and is not cancelled by stopping here"
    return 0
  fi
  TERMINAL=yes
  fact run-terminal PASS "${RUN_ID} reached a terminal state after the update (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json after ${waited}s): the run the update met in flight finished on its own, which is the acceptance's clause"
}

# step_ledger_intact: the ledger still accounts for the same run.
step_ledger_intact() {
  if [ "$SHOW_BEFORE_OK" != yes ]; then
    unmeasured ledger-intact "the run's ledger was not read before the update (the in-flight row above names why), so there is nothing to compare the ledger read after it against"
    return 0
  fi
  if [ -z "$RUN_ID" ]; then
    unmeasured ledger-intact "there is no run whose ledger could be read back: the run row above says why"
    return 0
  fi
  say "reading the run's ledger after it ended: fugaro runs show --json ${RUN_ID}"
  run_fresh "$CLI_TIMEOUT" show-after "$SANDBOX" "$FUGARO_BIN" runs show --json "$RUN_ID"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact ledger-intact FAIL "fugaro runs show --json ${RUN_ID} did not answer after the update (exit ${BOUNDED_STATUS}): the chain and the used figure it carries are the ledger's nearest read. What it printed is in $SCRATCH/show-after.err"
    return 0
  fi
  if ! read_json show "$SCRATCH/show-after.out" "$SCRATCH/show-after.txt"; then
    fact ledger-intact FAIL "fugaro runs show --json ${RUN_ID} printed a document this check could not read after the update: what it printed is in $SCRATCH/show-after.out"
    return 0
  fi
  local chain_before chain_after count_before count_after
  chain_before=$(field "$SCRATCH/show-before.txt" chain)
  chain_after=$(field "$SCRATCH/show-after.txt" chain)
  count_before=$(field "$SCRATCH/show-before.txt" run_count)
  count_after=$(field "$SCRATCH/show-after.txt" run_count)
  local d_before d_after used_before used_after figures="" dropped=""
  while IFS=$'\t' read -r kind d_before _limit used_before _rem _src; do
    [ "$kind" = limit ] || continue
    used_after=$(table_field "$SCRATCH/show-after.txt" limit "$d_before" 4)
    figures="${figures:+$figures, }${d_before}: ${used_before:-unmeasured}->${used_after:-unmeasured}"
    if ! mono "$used_before" "$used_after"; then
      dropped="${dropped:+$dropped, }${d_before}(${used_before}->${used_after})"
    fi
  done <"$SCRATCH/show-before.txt"
  if [ "$chain_before" != "$chain_after" ]; then
    fact ledger-intact FAIL "the run ${RUN_ID} belonged to chain ${chain_before:-unknown} before the update and to ${chain_after:-unknown} after it, with the run count moving from ${count_before:-unknown} to ${count_after:-unknown}: the update must not move a run between chains or lose it. (The budget run node's own Reserved/Released leaves have no CLI surface; this reads the chain block of fugaro runs show --json, whose used figure is the ledger's held figure — Reserved minus Released.)"
    return 0
  fi
  if [ -n "$dropped" ]; then
    fact ledger-intact FAIL "a chain dimension's used figure went DOWN over the update while ${RUN_ID} ran: ${dropped}. The ledger's held figure only grows while a run is going, so a drop is a counter reset or a lost run. (The chain block of fugaro runs show --json is the ledger's nearest read; its used is Reserved minus Released.)"
    return 0
  fi
  fact ledger-intact PASS "the ledger still accounts for ${RUN_ID} after the update: its chain is ${chain_after:-unknown} (${count_after:-unknown} run(s)) as before, and each dimension's used figure did not go down (${figures:-no dimension bounded}). Read from fugaro runs show --json before and after; its chain used is the budget run node's held figure (Reserved minus Released), which has no CLI leaf of its own"
}

# step_counters_intact: the run's money, tokens and time are monotone.
step_counters_intact() {
  if [ -z "$RUN_ID" ] || [ -z "$COUNTERS_BEFORE" ]; then
    unmeasured counters-intact "the run's counters were not read before the update (the in-flight row above names why), so there is nothing to compare against"
    return 0
  fi
  if [ "$TERMINAL" != yes ]; then
    unmeasured counters-intact "the run had not reached a terminal state (its own row above says why), so its record's counters are incomplete and this check did not read them"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured counters-intact "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=$(printf '%s' "${RUN_REF:-none}")): the record's time is read from that key"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the run's record for its counters: gcloud storage cat gs://fugaro-runs-${GCP_PROJECT}/${key}"
  run_fresh "$CLI_TIMEOUT" record-count "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/${key}" --project="${GCP_PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured counters-intact "gs://fugaro-runs-${GCP_PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the run's own time is in that record. What gcloud printed is in $SCRATCH/record-count.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record-count.out" "$SCRATCH/record.txt"; then
    unmeasured counters-intact "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record-count.out"
    return 0
  fi
  local mb mt mc mr mw ab at ac ar aw dropped=""
  IFS=',' read -r mb mt mc mr mw <<<"$COUNTERS_BEFORE"
  ab=$(row_field_of "$SCRATCH/rows-terminal.txt" 10)
  at=$(row_field_of "$SCRATCH/rows-terminal.txt" 11)
  ac=$(row_field_of "$SCRATCH/rows-terminal.txt" 12)
  ar=$(row_field_of "$SCRATCH/rows-terminal.txt" 13)
  aw=$(row_field_of "$SCRATCH/rows-terminal.txt" 14)
  local pair
  for pair in "model_usd:${mb}:${ab}" "total_usd:${mt}:${at}" "compute_usd:${mc}:${ac}" \
    "cache_read_tokens:${mr}:${ar}" "cache_write_tokens:${mw}:${aw}"; do
    local name=${pair%%:*}
    local rest=${pair#*:}
    local before=${rest%%:*}
    local after=${rest#*:}
    mono "$before" "$after" || dropped="${dropped:+$dropped, }${name}(${before:-0}->${after:-0})"
  done
  local duration
  duration=$(field "$SCRATCH/record.txt" duration_s)
  if [ -n "$dropped" ]; then
    fact counters-intact FAIL "the run ${RUN_ID}'s counters went DOWN over the update: ${dropped} (each a before->after, money in dollars and tokens in counts, read from fugaro runs ls --json before and after). A counter that only grows while a run is going must not reset or lose its figure"
    return 0
  fi
  if [ -z "$duration" ] || ! awk -v d="$duration" 'BEGIN { exit (d + 0 > 0) ? 0 : 1 }'; then
    fact counters-intact FAIL "the record of ${RUN_ID} carries no run time (the sum of its stages' duration_s is ${duration:-empty}): the acceptance is a run that finished with its counters intact, and a run with no recorded time lost its time counter"
    return 0
  fi
  fact counters-intact PASS "the run ${RUN_ID}'s counters are monotone across the update (each a before->after, from fugaro runs ls --json): money model_usd ${mb:-0}->${ab:-0}, total_usd ${mt:-0}->${at:-0}, compute_usd ${mc:-0}->${ac:-0}; tokens cache_read ${mr:-0}->${ar:-0}, cache_write ${mw:-0}->${aw:-0}; and its record carries a run time of ${duration}s (the sum of its stages' duration_s, from ${key}). No money, token or time counter reset or was lost"
}

# step_record_intact: the run's own record, the same run, task and repository.
step_record_intact() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured record-intact "there is no run whose record could be read: the run row above says why"
    return 0
  fi
  if [ "$TERMINAL" != yes ]; then
    unmeasured record-intact "the run ${RUN_ID} had not reached a terminal state (its own row above says why), so this check did not read its record"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured record-intact "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=$(printf '%s' "${RUN_REF:-none}")), so there was no key to read"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the run's record: gcloud storage cat gs://fugaro-runs-${GCP_PROJECT}/${key}"
  run_fresh "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/${key}" --project="${GCP_PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact record-intact FAIL "gs://fugaro-runs-${GCP_PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the run's own record is what says it survived the update. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact record-intact FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact record-intact FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, so this run's own record was lost or overwritten"
    return 0
  fi
  local title_before title_after repo_record status outcome
  title_before=$(row_field_of "$SCRATCH/rows-inflight.txt" 7)
  title_after=$(row_field_of "$SCRATCH/rows-terminal.txt" 7)
  repo_record=$(field "$SCRATCH/record.txt" repo)
  status=$(field "$SCRATCH/record.txt" status)
  outcome=$(field "$SCRATCH/record.txt" outcome)
  if [ -n "$title_before" ] && [ "$title_before" != "$title_after" ]; then
    fact record-intact FAIL "the run's task changed over the update: its listing row read '${title_before}' before and '${title_after}' after, and the record's own run is ${RUN_ID}. The update must not change what a run in flight holds"
    return 0
  fi
  if [ -z "$outcome" ]; then
    fact record-intact FAIL "the record of ${RUN_ID} carries no outcome (status=${status:-unknown}, reason=$(field "$SCRATCH/record.txt" reason)): a run the update left undisturbed reaches its own outcome, and an empty one is a run the update broke or that never settled"
    return 0
  fi
  if [ "$status" = failed ]; then
    fact record-intact FAIL "the run ${RUN_ID} the update met in flight ended failed (outcome=${outcome}, reason=$(field "$SCRATCH/record.txt" reason), failure_class=$(field "$SCRATCH/record.txt" failure_class)): a run going when the update starts must finish undisturbed, so a failure here is the update having broken it — read the record at ${key} and the update's own output"
    return 0
  fi
  fact record-intact PASS "the record at ${key} is the same run the check launched (run_id=${recorded}, repo=${repo_record:-unknown}), with the same task as before the update (${title_after:-the listing carried no title}) and its own outcome (${outcome}, status=${status}, execution=$(field "$SCRATCH/record.txt" execution), pr=$(field "$SCRATCH/record.txt" pr)): the run the update met in flight reached its own record intact"
}

restore() {
  RESTORE_VERDICT=PASS
  local update_note="no update was made, so the installation is exactly as this check found it"
  if [ "$UPDATED" = yes ]; then
    update_note="the update ran and converged ${INSTALLATION}, which is the acceptance's own verb and is not undone by this check"
  fi
  local run_note="no run was launched"
  if [ -n "$RUN_ID" ]; then
    run_note="the sandbox run ${RUN_ID} with the branch and the pull request it left (outcome=$(field "$SCRATCH/record.txt" outcome), pr=$(field "$SCRATCH/record.txt" pr)) — close that pull request and delete its branch yourself once you have read the run, or leave them if you want them"
  fi
  RESTORE_NOTE="this check writes no credential, no pool and no policy node of its own: every read below the update is a read. What it does leave is ${run_note}, and the update (${update_note}). Neither is put back: the run's branch and draft pull request are yours to decline, and the update's convergence is meant to stay"
}

cleanup() {
  trap - ERR
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  trap - ERR
  if [ "$PREFLIGHT_MODE" = yes ]; then
    # A preflight wrote nothing and launched nothing: it prints its own lines
    # and no RESULT block.
    remove_scratch
    exit "$status"
  fi
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
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
  fi
  if [ "$status" = 0 ]; then
    if [ "$failed" = yes ]; then
      status=1
    elif [ -n "$UNMEASURED_KEYS" ]; then
      status=2
    fi
  fi
  print_block "$facts" "$status"
  exit "$status"
}

plan() {
  say "CHECK 632 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] ${SANDBOX}/fugaro.yaml and fugaro doctor --project <name> --all — the installation this checkout belongs to"
  say "  [read] fugaro doctor --json — whether the installation is behind this CLI (a check whose fix names 'fugaro update')"
  say "  [read] fugaro project policy show --json, fugaro craft ls --json, fugaro accounts pool ls and fugaro accounts ls --json — what the installation held before the update"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, still going when the update starts"
  say "  [write] fugaro update — the acceptance's own verb (it converges all four halves, waits for no run and is not undone)"
  say "  [read] fugaro craft ls --json, fugaro project policy show --json, fugaro accounts pool ls, fugaro config show --json and fugaro accounts ls --json — the four converged halves, read back"
  say "  [read] fugaro runs ls --json, fugaro runs show --json and gcloud storage cat (the run's record) — the run followed to its end, its ledger and its counters"
  for key in installation version-before before run-launched run-in-flight update update-notes-runs no-operator-step converge-model converge-publish converge-crossing converge-key run-terminal ledger-intact counters-intact record-intact; do
    fact "$key" INFO "planned: a real installation and run measure this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no run, no update and no read"
}

BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation version-before
  before run-launched run-in-flight update update-notes-runs no-operator-step
  converge-model converge-publish converge-crossing converge-key run-terminal
  ledger-intact counters-intact record-intact restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 632 RESULT\n' >&3
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
  printf 'CHECK 632 RESULT END (exit %s)\n' "$status" >&3
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
PREFLIGHT_CLI=""
SANDBOX_CLI=""
PROJECT_CLI=""
TASK_CLI=""
RECIPE_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --project)
    [ $# -ge 2 ] || die "--project needs the installation's own name"
    PROJECT_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
    ;;
  --preflight)
    PREFLIGHT_CLI=1
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

SANDBOX=${CHECK632_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 632 needs --sandbox: the checkout of a repository of the sandbox project. Usage: bash scripts/live-checks/check632.sh --sandbox <a sandbox repository checkout>"
fi
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PROJECT_ARG=${CHECK632_PROJECT:-${PROJECT_CLI:-}}
PROJECT=$PROJECT_ARG
TASK=${CHECK632_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK632_RECIPE:-${RECIPE_CLI:-}}
DRY_RUN=${CHECK632_DRY_RUN:-$DRY_RUN_CLI}
PREFLIGHT=${CHECK632_PREFLIGHT:-$PREFLIGHT_CLI}
PREFLIGHT_MODE=no
[ -z "$PREFLIGHT" ] || PREFLIGHT_MODE=yes
KEEP_SCRATCH=${CHECK632_KEEP_SCRATCH:-}
REGION=${CHECK632_REGION:-us-east5}
LOG_VIEW=${CHECK632_LOG_VIEW:-}
RUN_TIMEOUT=${CHECK632_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK632_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK632_CLI_TIMEOUT_SECONDS:-180}
DOCTOR_TIMEOUT=${CHECK632_DOCTOR_TIMEOUT_SECONDS:-300}
LAUNCH_TIMEOUT=${CHECK632_LAUNCH_TIMEOUT_SECONDS:-600}
UPDATE_TIMEOUT=${CHECK632_UPDATE_TIMEOUT_SECONDS:-1800}
FUGARO_BIN=$(command -v fugaro || true)

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 632 needs 'fugaro' on PATH: the check reads the installation you have, and it is not there"
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
  if [ -n "$PREFLIGHT" ]; then
    step_preflight
    preflight_report
  fi
  step_installation
  step_version_before
  step_before
  step_run
  step_run_in_flight
  step_update
  step_update_notes_runs
  step_no_operator_step
  step_converge_model
  step_converge_publish
  step_converge_crossing
  step_converge_key
  step_run_terminal
  step_ledger_intact
  step_counters_intact
  step_record_intact
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
