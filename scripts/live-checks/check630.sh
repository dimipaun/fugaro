#!/usr/bin/env bash
# Check 630 — the crossing, on a real installation: a pool naming an OpenRouter
# member and an Anthropic-key member refused by name at `fugaro accounts pool
# add` and at the read of the published node, with the one sentence, and an
# installation that already held one converged by `fugaro update` into two
# pools, each member on its own path and in its own order.
#
# Plan task MC26 writes this check; its subject is the sentence MC26 gives
# number 630 in docs/model-crafts/activation-plan.md.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to: its `project:` line out
#     of the checkout's own fugaro.yaml, beside `fugaro doctor --all`;
#   * **the node's own door** (`fugaro project policy show --json`, whose read
#     runs config.ValidatePublishedPools through projectpolicy.Decode): a node
#     that already carries a crossing pool is refused with the one sentence,
#     naming both members and the fix. An installation whose node carries none
#     has nothing for that door to refuse, and the row says so rather than
#     claiming either;
#   * **the write door** (`fugaro accounts pool add`): the check hands it a list
#     naming one OpenRouter member and one Anthropic member — the acceptance's
#     own pair, taken from the installation's declared credentials — and reads
#     the refusal, which has to be the same one sentence and has to name both
#     members. It then reads the pools back and fails if the pool was written:
#     a refusal that writes is no refusal;
#   * **`fugaro validate`**: the acceptance names this command as the crossing's
#     second door, and in THIS build it is not one. `fugaro validate` reads a
#     repository's fugaro.yaml; the crossing's second door is the read of a
#     PUBLISHED node (config.ValidatePublishedPools, which projectpolicy.Decode
#     runs and doctor, the runner's Read, the credentials service's re-read and
#     `update` all go through). The row runs `fugaro validate` to show what it
#     is, and states the deviation rather than passing a clause this command
#     does not hold — see the register's own note for 630;
#   * **the convergence** (`fugaro update`, the acceptance's verb): an
#     installation whose config still holds a crossing pool is split into one
#     pool per half, `<pool>-claude` and `<pool>-provider`, each keeping the
#     members of its own path in the order the pool listed them, and the node's
#     own crossings are split too as its version moves up. The check reads the
#     crossing BEFORE and AFTER, and names what it split; an installation that
#     holds none has no premise for that clause, and the rows say so.
#
# Nothing here reads a credential value: names, types, paths, versions and the
# words of a refusal only.
#
# Usage:      bash scripts/live-checks/check630.sh --sandbox <a sandbox repository checkout> [--project <name>]
#             bash scripts/live-checks/check630.sh --sandbox <path> --preflight
#             bash scripts/live-checks/check630.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check630.sh --help
#             --preflight makes the check's own READS and evaluates every
#             precondition before anything is written or launched (the
#             installation, the node, the crossing members and the pool the
#             write door is asked for), prints every blocker at once with the
#             command that clears it, writes nothing and launches nothing, and
#             exits non-zero when anything blocks.
# Needs:      bash, mktemp, date, python3, awk, sed, grep and fugaro (the
#             installed release, an admin of the project: `fugaro update` and
#             the pool write both reach the cloud as you).
# Where:      your own machine, the sandbox project and the sandbox repository
#             only, at your own terminal: a coding agent's session is refused by
#             name, as is a project or pool naming belong.
# Creates:    `fugaro update` over the installation (twice-run, it converges
#             what it finds and says so), and the pool the write door is asked
#             to declare — which it must refuse, so nothing is written when the
#             rule holds.
# Costs:      nothing billable: no run is launched and no model is called. The
#             wall clock is the update's.
# Runs:       by YOU. No agent runs any step of it. Its own test
#             (check630_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The update's own writes are NOT undone: the
#             Restore row says what they are.
# Output:     progress on stderr, ONE block on stdout headed CHECK 630 RESULT,
#             then CHECK 630 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure.
#
# Knobs (all CHECK630_*): DRY_RUN, KEEP_SCRATCH, SANDBOX, PROJECT, POOL_NAME
# (the pool the write door is asked to declare: chk630-crossing),
# OPENROUTER_MEMBER and ANTHROPIC_MEMBER (the pair it hands it, each defaulting
# to the first credential of that kind the installation declares), CLI_TIMEOUT_SECONDS
# (180), DOCTOR_TIMEOUT_SECONDS (300) and UPDATE_TIMEOUT_SECONDS (1800).
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
PREFLIGHT_TOOL=check630

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 630 needs bash"
  local t
  for t in mktemp date python3 awk sed grep fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 630 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk630-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
INSTALLATION=""
GCP_PROJECT=""
POOL_NAME=""
RUNNER_MEMBER=""
CREDSVCC_MEMBER=""
CROSSED_POOL=""
CROSSED_MEMBERS=""
NODE_VERSION_BEFORE=""
NODE_VERSION_AFTER=""
UPDATED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk630.XXXXXXXX") || die "Check 630 cannot make a scratch directory"
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
    say "CHECK630_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The reader: the credentials a config declares (names, types and the half that
# serves each), the pools and crafts a node carries, and which of a node's pools
# cross. The crossing is computed from the document's own types, in path order —
# the credentials service's member first — so the check reads the same pair the
# refusal's sentence names.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys

PATHS = {
    "claude-oauth": "credsvc",
    "anthropic": "credsvc",
    "openrouter": "runner",
    "vertex": "runner",
}


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "creds":
    creds = doc.get("credentials") or []
    print("count=%d" % len(creds))
    for c in creds:
        print("cred\t%s\t%s\t%s" % (
            c.get("name") or "", c.get("type") or "", c.get("serving_path") or ""))
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
elif what == "crossing":
    # The crossed pool is a member of `policy` in this document too: read from
    # the top level there is no credential and no pool, so every node would read
    # as carrying no crossing at all.
    policy = doc.get("policy")
    policy = policy if isinstance(policy, dict) else {}
    types = {}
    for c in policy.get("craft_credentials") or []:
        types[c.get("name") or ""] = c.get("type") or ""
    if "vertex" not in types:
        types["vertex"] = "vertex"
    crossed = []
    for p in policy.get("craft_pools") or []:
        seen = {}
        for m in p.get("members") or []:
            path = PATHS.get(types.get(m, ""), "")
            if path and path not in seen:
                seen[path] = m
        if len(seen) == 2:
            crossed.append((p.get("name") or "", seen.get("credsvc", ""), seen.get("runner", ""),
                            ",".join(p.get("members") or [])))
    print("crossing=%d" % len(crossed))
    for name, svc, run, members in crossed:
        print("crosspool\t%s\t%s\t%s\t%s" % (name, svc, run, members))
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

# A pool's row out of `fugaro accounts pool ls`, which prints a tabwriter table
# padded with spaces: NAME, STRATEGY, MEMBERS (comma and space) and the half
# that serves them. "" when there is no such pool.
pool_ls_row() { # pool_ls_row FILE POOL
  [ -f "$1" ] || return 0
  awk -F'  +' -v want="$2" '$1 == want { print; exit }' "$1" 2>/dev/null
  return 0
}

pool_ls_field() { # pool_ls_field FILE POOL COLUMN
  local row
  row=$(pool_ls_row "$1" "$2")
  [ -n "$row" ] || return 0
  printf '%s' "$row" | awk -F'  +' -v col="$3" '{ print $col }'
  return 0
}

# crossing_pools FILE: every pool of the listing whose members span the two
# halves, as "name<TAB>members", from the SERVED BY column the listing pads with
# " and " for exactly that pool (accountsPoolServingText).
crossing_pools() { # crossing_pools FILE
  [ -f "$1" ] || return 0
  awk -F'  +' '
    $1 == "NAME" { next }
    $1 != "" && $4 ~ / and / { printf "%s\t%s\n", $1, $3 }
  ' "$1" 2>/dev/null
  return 0
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

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check630.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy an update or a refusal to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 630 asks a live installation to refuse a pool write and then converges it with 'fugaro update' — which under this marker changes nothing outside this machine — so it is run at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT}${POOL_NAME}${GCP_PROJECT}" in
  *belong*) stop "the account, project or pool this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "neither the project nor the pool this check names is belong, and the account it runs as is yours"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release whose crossing rule and whose split this check measures)"
}

# ---------------------------------------------------------------- preflight
# step_preflight: --preflight. The reads the check makes, every precondition it
# has evaluated, nothing written and nothing launched. The installation the
# checkout names is the same thing step_installation refuses a bad --sandbox
# for, so that check is argument validation and stays a stop.
step_preflight() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the pool write and the update are both made from it"
  fi
  local named
  named=$(sed -n 's/^project:[[:space:]]*//p' "$SANDBOX/fugaro.yaml" | head -n 1)
  PROJECT=${PROJECT_ARG:-$named}
  INSTALLATION=$PROJECT
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")

  if [ -z "$INSTALLATION" ]; then
    preflight_block installation \
      "$SANDBOX/fugaro.yaml names no project, and both the pool write and the update are made from the installation it names" \
      "cd $SANDBOX && fugaro config show --json"
    return 0
  fi

  say "preflight: reading the installation: fugaro doctor --all, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" pf-doctor "$SANDBOX" "$FUGARO_BIN" doctor --all
  local line gcp=""
  line=$(grep -m1 '^project: ' "$SCRATCH/pf-doctor.out" 2>/dev/null || true)
  if [ -n "$line" ]; then
    gcp=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
  fi
  case "${gcp:-$INSTALLATION}" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${gcp:-$INSTALLATION}: this check is never run there" ;;
  esac
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ -z "$line" ]; then
    preflight_block installation \
      "fugaro doctor --all does not answer for ${INSTALLATION} in ${SANDBOX} (exit ${BOUNDED_STATUS}), so the installation this check would write to cannot be read" \
      "cd $SANDBOX && fugaro doctor --all"
  else
    preflight_note installation "the installation is ${INSTALLATION} (GCP ${gcp:-unknown}), read from ${SANDBOX}/fugaro.yaml and fugaro doctor --all"
  fi

  say "preflight: reading the published node: fugaro project policy show --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-node "$SANDBOX" "$FUGARO_BIN" project policy show --json
  local words
  words=$(head -c 1200 "$SCRATCH/pf-node.err" 2>/dev/null | tr '\n' ' ' || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    preflight_block node-crossing \
      "fugaro project policy show --json did not finish inside ${CLI_TIMEOUT}s, and the node's own read is one of the check's rows" \
      "cd $SANDBOX && fugaro project policy show --json"
  elif [ "$BOUNDED_STATUS" != 0 ]; then
    case "$words" in
    *"name two pools, one per half"*)
      preflight_note node-crossing "the published node already carries a crossing pool and this build's read refuses it, with the crossing's own sentence"
      ;;
    *)
      preflight_block node-crossing \
        "fugaro project policy show --json refuses ${INSTALLATION}'s node for a reason that is not the crossing's sentence: ${words:-no stderr}" \
        "cd $SANDBOX && fugaro project policy show --json"
      ;;
    esac
  elif ! read_json crossing "$SCRATCH/pf-node.out" "$SCRATCH/pf-node.cross.txt"; then
    preflight_block node-crossing \
      "fugaro project policy show --json prints a document this check cannot read" \
      "cd $SANDBOX && fugaro project policy show --json"
  else
    local crossed
    crossed=$(field "$SCRATCH/pf-node.cross.txt" crossing)
    local v
    v=$(read_json node "$SCRATCH/pf-node.out" "$SCRATCH/pf-node.txt" && field "$SCRATCH/pf-node.txt" version)
    if [ "${crossed:-0}" != 0 ]; then
      preflight_block node-crossing \
        "the published node (version ${v:-unknown}) carries a crossing pool and this build's read did NOT refuse it, so every reader of the node would serve it" \
        "fugaro update"
    else
      preflight_note node-crossing "the published node (version ${v:-unknown}) carries no crossing pool, so this door has nothing to refuse"
    fi
  fi

  say "preflight: reading the credentials: fugaro accounts ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local runner="" svc=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] ||
    ! read_json creds "$SCRATCH/pf-creds.out" "$SCRATCH/pf-creds.txt"; then
    preflight_block refused-at-add \
      "fugaro accounts ls --json does not answer in ${SANDBOX}, and the crossing list needs one member of each path from it" \
      "cd $SANDBOX && fugaro accounts ls --json"
  else
    local name type
    while IFS=$'\t' read -r kind name type _path; do
      [ "$kind" = cred ] || continue
      case "$type" in
      openrouter | vertex) [ -n "$runner" ] || runner=$name ;;
      anthropic | claude-oauth) [ -n "$svc" ] || svc=$name ;;
      esac
    done <"$SCRATCH/pf-creds.txt"
    [ -z "$WANT_RUNNER" ] || runner=$WANT_RUNNER
    [ -z "$WANT_CREDSVCC" ] || svc=$WANT_CREDSVCC
    if [ -z "$runner" ]; then
      preflight_block refused-at-add \
        "the config declares no OpenRouter or vertex member, so the crossing list has no member of the run's own path" \
        "fugaro accounts add openrouter-key --type openrouter"
    fi
    if [ -z "$svc" ]; then
      preflight_block refused-at-add \
        "the config declares no Anthropic-key or Claude OAuth member, so the crossing list has no member of the credentials service's path" \
        "fugaro accounts add anthropic-key --type anthropic"
    fi
    [ -z "$runner" ] || [ -z "$svc" ] || preflight_note refused-at-add \
      "the crossing list would name ${runner} (the run's path) and ${svc} (the credentials service's)"
  fi

  say "preflight: reading the pools: fugaro accounts pool ls, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-pools "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    preflight_block config-crossing \
      "fugaro accounts pool ls does not answer in ${SANDBOX}, so the pool the write door would be asked for cannot be read" \
      "cd $SANDBOX && fugaro accounts pool ls"
  else
    local existing
    existing=$(pool_ls_row "$SCRATCH/pf-pools.out" "$POOL_NAME")
    if [ -n "$existing" ]; then
      preflight_block refused-at-add \
        "the pool ${POOL_NAME} is already declared (${existing}), and the write door is asked to declare it" \
        "fugaro accounts pool rm $POOL_NAME"
    fi
    local crossed
    crossed=$(crossing_pools "$SCRATCH/pf-pools.out")
    if [ -n "$crossed" ]; then
      preflight_note config-crossing "the config already holds a crossing pool ($(printf '%s' "$crossed" | tr '\n' ' ')), which the update below splits"
    else
      preflight_note config-crossing "the config holds no crossing pool, so the update has no crossing of its own to split"
    fi
  fi
}

# ---------------------------------------------------------------- the steps
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the pool write and the update are both made from it"
  fi
  local named
  named=$(sed -n 's/^project:[[:space:]]*//p' "$SANDBOX/fugaro.yaml" | head -n 1)
  PROJECT=${PROJECT_ARG:-$named}
  if [ -z "$PROJECT" ]; then
    unmeasured installation "the checkout at $SANDBOX names no project: its fugaro.yaml carries no 'project:' line, and both the pool write and the update are made from the installation that line names"
    return 0
  fi
  INSTALLATION=$PROJECT
  say "reading the installation: fugaro doctor --all, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --all
  local line
  line=$(grep -m1 '^project: ' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -n "$line" ]; then
    GCP_PROJECT=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
  fi
  case "$GCP_PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${GCP_PROJECT}: this check is never run there" ;;
  esac
  local node_note=""
  if [ "$BOUNDED_STATUS" != 0 ]; then
    node_note=" — and its node read refused, which the node-crossing row below reads"
  fi
  fact installation PASS "${SANDBOX} is a checkout of the installation ${INSTALLATION}${GCP_PROJECT:+ (GCP ${GCP_PROJECT})}, read from the checkout's own fugaro.yaml$([ -n "$line" ] && printf ' and confirmed by fugaro doctor --all')${node_note}: the pool write and the update are both made from it"
}

# step_node_crossing: the door a PUBLISHED node is read through.
step_node_crossing() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured node-crossing "there is no installation whose node could be read: the row above says why"
    return 0
  fi
  say "reading the published node: fugaro project policy show --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" node-before "$SANDBOX" "$FUGARO_BIN" project policy show --json
  local words
  words=$(head -c 1200 "$SCRATCH/node-before.err" 2>/dev/null | tr '\n' ' ' || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured node-crossing "fugaro project policy show --json did not finish inside ${CLI_TIMEOUT}s and was ended: the node's own read is where a crossing pool is refused"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    case "$words" in
    *"name two pools, one per half"*)
      fact node-crossing PASS "the read of the published node refused it, and it is the crossing's one sentence: ${words}"
      ;;
    *)
      unmeasured node-crossing "fugaro project policy show --json exited ${BOUNDED_STATUS} in ${SANDBOX} and its words are not the crossing's own sentence (which is 'name two pools, one per half'); the node could not be read, so this door could not be measured: ${words:-no stderr}"
      ;;
    esac
    return 0
  fi
  if ! read_json node "$SCRATCH/node-before.out" "$SCRATCH/node-before.txt" ||
    ! read_json crossing "$SCRATCH/node-before.out" "$SCRATCH/node-before.cross.txt"; then
    unmeasured node-crossing "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/node-before.out"
    return 0
  fi
  NODE_VERSION_BEFORE=$(field "$SCRATCH/node-before.txt" version)
  local crossed
  crossed=$(field "$SCRATCH/node-before.cross.txt" crossing)
  if [ "${crossed:-0}" != 0 ]; then
    fact node-crossing FAIL "the published node of ${INSTALLATION} still carries a crossing pool ($(awk -F'\t' '$1 == "crosspool" { printf "%s(%s, %s) ", $2, $3, $4 }' "$SCRATCH/node-before.cross.txt")) and this build's read of it did NOT refuse it: the rule (config.ValidatePublishedPools, D1668) is not held at this door, so every reader of the node — doctor, the runner, the credentials service — would serve a pool whose order cannot mean anything"
    return 0
  fi
  fact node-crossing INFO "the published node of ${INSTALLATION} (version ${NODE_VERSION_BEFORE:-unknown}) carries no crossing pool, so this door had nothing to refuse: the acceptance's premise is an installation that already held one — a node 0.25.0 wrote, whose 'accounts pool add' wrote a crossing pool — and this installation is not in that state. The rule itself is held by config.ValidatePublishedPools, which projectpolicy.Decode runs: doctor, the runner's Read, the credentials service's per-call re-read and update all go through it"
}

# step_refused_at_add: the door a NEW member list is written through.
step_refused_at_add() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured refused-at-add "there is no installation whose config could be read: the row above says why"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "reading the credentials the installation declares: fugaro accounts ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured refused-at-add "fugaro accounts ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the crossing list needs one OpenRouter member and one Anthropic member this config declares. What it printed is in $SCRATCH/creds.err"
    return 0
  fi
  if ! read_json creds "$SCRATCH/creds.out" "$SCRATCH/creds.txt"; then
    unmeasured refused-at-add "fugaro accounts ls --json printed a document this check could not read: what it printed is in $SCRATCH/creds.out"
    return 0
  fi
  local name type
  while IFS=$'\t' read -r kind name type _path; do
    [ "$kind" = cred ] || continue
    case "$type" in
    openrouter | vertex) [ -n "$RUNNER_MEMBER" ] || RUNNER_MEMBER=$name ;;
    anthropic) [ -n "$CREDSVCC_MEMBER" ] || CREDSVCC_MEMBER=$name ;;
    claude-oauth) [ -n "$CREDSVCC_FALLBACK" ] || CREDSVCC_FALLBACK=$name ;;
    esac
  done <"$SCRATCH/creds.txt"
  [ -z "$CREDSVCC_MEMBER" ] || CREDSVCC_FALLBACK=$CREDSVCC_MEMBER
  [ -z "$WANT_RUNNER" ] || RUNNER_MEMBER=$WANT_RUNNER
  [ -z "$WANT_CREDSVCC" ] || CREDSVCC_MEMBER=$WANT_CREDSVCC
  if [ -z "$RUNNER_MEMBER" ] || [ -z "$CREDSVCC_MEMBER" ]; then
    unmeasured refused-at-add "${INSTALLATION}'s config declares no pair to cross (an OpenRouter or vertex member: ${RUNNER_MEMBER:-none}; an Anthropic-key member: ${CREDSVCC_MEMBER:-none}, ${CREDSVCC_FALLBACK:+a Claude OAuth credential '${CREDSVCC_FALLBACK}' beside it}): the acceptance's pool names one member of each half, and without both there is nothing for the door to refuse. Declare one of each — 'fugaro accounts add <name> --type openrouter' and 'fugaro accounts add <name> --type anthropic' at your own terminal — or name the pair with CHECK630_OPENROUTER_MEMBER and CHECK630_ANTHROPIC_MEMBER, and run this check again"
    return 0
  fi
  say "asking the write door to declare a crossing pool: fugaro accounts pool add ${POOL_NAME} ${RUNNER_MEMBER} ${CREDSVCC_MEMBER}"
  run_bounded "$CLI_TIMEOUT" add "$SANDBOX" "$FUGARO_BIN" accounts pool add "$POOL_NAME" "$RUNNER_MEMBER" "$CREDSVCC_MEMBER"
  ADD_STATUS=$BOUNDED_STATUS
  ADD_TIMED_OUT=$BOUNDED_TIMED_OUT
  local add_words
  add_words=$(cat "$SCRATCH/add.err" 2>/dev/null | tr '\n' ' ' | head -c 1200 || true)
  run_bounded "$CLI_TIMEOUT" add-read-back "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  local written
  written=$(pool_ls_row "$SCRATCH/add-read-back.out" "$POOL_NAME")
  if [ -n "$written" ]; then
    fact refused-at-add FAIL "the write door ACCEPTED the crossing list and wrote the pool: ${written}. The rule (config.ValidatePoolPaths, D1668) is not held where a new member list is written, which is the one door it has. Remove what it wrote with 'fugaro accounts pool rm ${POOL_NAME}' and report this"
    return 0
  fi
  if [ "$ADD_TIMED_OUT" = yes ]; then
    unmeasured refused-at-add "fugaro accounts pool add did not finish inside ${CLI_TIMEOUT}s and was ended: its refusal is the measurement here, and nothing was written ($POOL_NAME is not in the listing)"
    return 0
  fi
  if [ "$ADD_STATUS" = 0 ]; then
    fact refused-at-add FAIL "fugaro accounts pool add exited 0 for a pool naming ${RUNNER_MEMBER} (an OpenRouter member) and ${CREDSVCC_MEMBER} (an Anthropic member), and wrote nothing only because this check read the listing fast: the crossing has to be refused, not accepted and left out"
    return 0
  fi
  local missing=""
  case "$add_words" in
  *"name two pools, one per half"*) ;;
  *) missing="the one sentence ('name two pools, one per half')" ;;
  esac
  case "$add_words" in
  *"$RUNNER_MEMBER"*) ;;
  *) missing="${missing:+$missing, }the OpenRouter member ${RUNNER_MEMBER}" ;;
  esac
  case "$add_words" in
  *"$CREDSVCC_MEMBER"*) ;;
  *) missing="${missing:+$missing, }the Anthropic member ${CREDSVCC_MEMBER}" ;;
  esac
  if [ -n "$missing" ]; then
    fact refused-at-add FAIL "fugaro accounts pool add refused the crossing list (exit ${ADD_STATUS}) and its refusal does not name ${missing}: ${add_words:-no stderr}. The rule has to refuse a crossing pool where it is written and name the two members that cross and the fix, or the operator cannot tell a crossing from any other bad member list"
    return 0
  fi
  fact refused-at-add PASS "fugaro accounts pool add refused a pool naming ${RUNNER_MEMBER} (an OpenRouter member) and ${CREDSVCC_MEMBER} (an Anthropic member) with the crossing's one sentence, naming both members and the fix — and the pool is not in the listing afterwards ($POOL_NAME absent). Its words: ${add_words}"
}

# step_validate: the command the acceptance names as the crossing's second door,
# as this build has it.
step_validate() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured validate "there is no installation whose repository could be validated: the row above says why"
    return 0
  fi
  say "reading what fugaro validate is: fugaro validate --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" validate "$SANDBOX" "$FUGARO_BIN" validate --json
  local first
  first=$(head -c 400 "$SCRATCH/validate.out" 2>/dev/null | tr '\n' ' ' || true)
  local words
  words=$(head -c 400 "$SCRATCH/validate.err" 2>/dev/null | tr '\n' ' ' || true)
  local said="${first}${words:+ — stderr: ${words}}"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    said="it did not finish inside ${CLI_TIMEOUT}s"
  fi
  fact validate INFO "the acceptance names 'fugaro validate' as the crossing's second door and in this build it is not one: it reads a repository's fugaro.yaml (its own --project-layer is the repository's layer, and a config's pools are not in it), so there is no pool for it to refuse — it exited ${BOUNDED_STATUS} on ${SANDBOX}/fugaro.yaml and said: ${said:-nothing}. The crossing's second door in this build is the read of a PUBLISHED node — config.ValidatePublishedPools, which projectpolicy.Decode runs, so doctor, the runner's Read, the credentials service's per-call re-read and update all hold it — and the node-crossing row above is that door measured. The register's note for Check 630 says this"
}

# step_config_crossing: the config-side premise, before the update.
step_config_crossing() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured config-crossing "there is no installation whose config could be read: the row above says why"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "reading the config's own pools before the update: fugaro accounts pool ls, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pools-pre "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured config-crossing "fugaro accounts pool ls did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the pools the config declares are in that listing. What it printed is in $SCRATCH/pools-pre.err"
    return 0
  fi
  local crossed
  crossed=$(crossing_pools "$SCRATCH/pools-pre.out")
  if [ -z "$crossed" ]; then
    fact config-crossing INFO "the config of ${INSTALLATION} holds no pool whose members span the two halves (fugaro accounts pool ls, in $SCRATCH/pools-pre.out): the acceptance's convergence is about an installation that already held one — a config 0.25.0's 'accounts pool add' wrote, or one written by hand — and this installation is not in that state, so the update below has no crossing of its own to split. The split's own rule is one for both documents (internal/craft's SplitCrossPathPools); MC21's tests pin it hermetically"
    return 0
  fi
  CROSSED_POOL=$(printf '%s\n' "$crossed" | awk -F'\t' 'NR == 1 { print $1 }')
  CROSSED_MEMBERS=$(printf '%s\n' "$crossed" | awk -F'\t' 'NR == 1 { print $2 }' | tr -d ' ')
  fact config-crossing PASS "the config of ${INSTALLATION} still holds a crossing pool: $(printf '%s' "$crossed" | tr '\n' ' ') (name and members, from fugaro accounts pool ls, whose SERVED BY column says 'the credentials service and a run's own gateway' for exactly such a pool). This is the acceptance's premise, in the owner's config, and the update below is what converges it"
}

# step_update: the acceptance's verb.
step_update() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured update "there is no installation to update: the row above says why"
    return 0
  fi
  say "converging the installation: fugaro update, in ${SANDBOX} (the acceptance's own verb; it converges the config and the policy node and asks nothing)"
  local uflag=()
  [ -z "$PROJECT_ARG" ] || uflag=(--project "$PROJECT_ARG")
  run_bounded "$UPDATE_TIMEOUT" update "$SANDBOX" "$FUGARO_BIN" update ${uflag[@]+"${uflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured update "fugaro update did not finish inside ${UPDATE_TIMEOUT}s and was ended: the convergence below is measured after it, so there is nothing to read. What it printed is in $SCRATCH/update.out and $SCRATCH/update.err"
    return 0
  fi
  local tail_lines
  tail_lines=$(tail -n 3 "$SCRATCH/update.out" 2>/dev/null | tr '\n' ' ' || true)
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact update FAIL "fugaro update exited ${BOUNDED_STATUS} in ${SANDBOX}: the acceptance's convergence is this command's own, so nothing below could be measured. Its last words are in $SCRATCH/update.err${tail_lines:+ (stdout: ${tail_lines})}"
    return 0
  fi
  UPDATED=yes
  fact update PASS "fugaro update converged ${INSTALLATION} from ${SANDBOX} and exited 0${tail_lines:+ — its last lines: ${tail_lines}}. What it converges without being asked is the crossing (the config's split runs on every write of the file, internal/cli's derivePathPools) and the node's own (the policy chain's version step, internal/projectpolicy's migrateV8ToV9)"
}

# step_converged_pools: what the update left in the config.
step_converged_pools() {
  if [ "$UPDATED" != yes ]; then
    unmeasured converged-pools "the update did not finish (its own row above says why), so there is no converged config to read"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  run_bounded "$CLI_TIMEOUT" pools-after "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured converged-pools "fugaro accounts pool ls did not answer after the update (exit ${BOUNDED_STATUS}): the pools the config holds now are in that listing. What it printed is in $SCRATCH/pools-after.err"
    return 0
  fi
  local still
  still=$(crossing_pools "$SCRATCH/pools-after.out")
  if [ -n "$still" ]; then
    fact converged-pools FAIL "the config of ${INSTALLATION} still holds a pool whose members span the two halves after fugaro update: $(printf '%s' "$still" | tr '\n' ' '). The convergence is the update's own step (internal/cli's derivePathPools, which SplitCrossPathPools runs on every write of the file), so a crossing the update left is a crossing nothing converges"
    return 0
  fi
  if [ -z "$CROSSED_POOL" ]; then
    fact converged-pools INFO "every pool of ${INSTALLATION} is served on one path after the update ($(awk -F'  +' '$1 != "" && $1 != "NAME" { printf "%s(%s) ", $1, $3 }' "$SCRATCH/pools-after.out")), which is the rule's own invariant read in the config. There was no crossing pool before the update, so the acceptance's clause — that a crossing becomes two pools, each member on its own path and in its own order — had no subject on this installation and was not measured here"
    return 0
  fi
  # The premise held: the two halves have to be there, each member on its own
  # path and in the order the crossed pool listed them.
  local base=${CROSSED_POOL%-claude}
  base=${base%-provider}
  local svc_half="${base}-claude" run_half="${base}-provider"
  local svc_members="" run_members="" m
  local creds_rows="$SCRATCH/creds.txt"
  for m in ${CROSSED_MEMBERS//,/ }; do
    local typ
    typ=$(table_field "$creds_rows" cred "$m" 3)
    case "$typ" in
    anthropic | claude-oauth) svc_members="${svc_members:+$svc_members,}$m" ;;
    openrouter | vertex) run_members="${run_members:+$run_members,}$m" ;;
    esac
  done
  local got_svc got_run missing=""
  got_svc=$(pool_ls_field "$SCRATCH/pools-after.out" "$svc_half" 3 | tr -d ' ')
  got_run=$(pool_ls_field "$SCRATCH/pools-after.out" "$run_half" 3 | tr -d ' ')
  [ -n "$got_svc" ] || missing="${missing:+$missing, }${svc_half} (the credentials service's half)"
  [ -n "$got_run" ] || missing="${missing:+$missing, }${run_half} (the run's own gateway's half)"
  if [ -z "$missing" ] && [ "$got_svc" != "$svc_members" ]; then
    missing="${missing:+$missing, }${svc_half} lists ${got_svc} where the crossed pool's own members on that path are ${svc_members}"
  fi
  if [ -z "$missing" ] && [ "$got_run" != "$run_members" ]; then
    missing="${missing:+$missing, }${run_half} lists ${got_run} where the crossed pool's own members on that path are ${run_members}"
  fi
  if [ -n "$missing" ]; then
    fact converged-pools FAIL "the crossed pool ${CROSSED_POOL} (${CROSSED_MEMBERS}) was not split as the acceptance says it is: ${missing}. Each half keeps the members of its own path, in the order the pool listed them, and the halves are ${CROSSED_POOL}-claude and ${CROSSED_POOL}-provider (with -2 and on appended while a name is taken)"
    return 0
  fi
  fact converged-pools PASS "the crossed pool ${CROSSED_POOL} (${CROSSED_MEMBERS}) is two pools after the update: ${svc_half} carries ${got_svc} (the credentials service's members, in the order the pool listed them) and ${run_half} carries ${got_run} (the run's own gateway's), each member on its own path — the acceptance's clause, read out of the config the update wrote"
}

# step_converged_node: what the update left in the published node.
step_converged_node() {
  if [ "$UPDATED" != yes ]; then
    unmeasured converged-node "the update did not finish (its own row above says why), so there is no converged node to read"
    return 0
  fi
  say "reading the node back after the update: fugaro project policy show --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" node-after "$SANDBOX" "$FUGARO_BIN" project policy show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact converged-node FAIL "the published node of ${INSTALLATION} does not read back after fugaro update (exit ${BOUNDED_STATUS}): $(head -c 600 "$SCRATCH/node-after.err" 2>/dev/null | tr '\n' ' '). The node has to be one this build reads, or neither the runner nor the credentials service can use it. What it printed is in $SCRATCH/node-after.out and $SCRATCH/node-after.err"
    return 0
  fi
  if ! read_json node "$SCRATCH/node-after.out" "$SCRATCH/node-after.txt" ||
    ! read_json crossing "$SCRATCH/node-after.out" "$SCRATCH/node-after.cross.txt"; then
    fact converged-node FAIL "fugaro project policy show --json printed a document this check could not read after the update: what it printed is in $SCRATCH/node-after.out"
    return 0
  fi
  NODE_VERSION_AFTER=$(field "$SCRATCH/node-after.txt" version)
  local crossed
  crossed=$(field "$SCRATCH/node-after.cross.txt" crossing)
  if [ "${crossed:-0}" != 0 ]; then
    fact converged-node FAIL "the published node of ${INSTALLATION} still carries a crossing pool after the update ($(awk -F'\t' '$1 == "crosspool" { printf "%s(%s, %s) ", $2, $3, $4 }' "$SCRATCH/node-after.cross.txt")): the node's own step (internal/projectpolicy's migrateV8ToV9) is what splits it, and it did not"
    return 0
  fi
  local moved="the version is ${NODE_VERSION_AFTER:-unknown}, and the read before the update named ${NODE_VERSION_BEFORE:-no version}"
  if [ -n "$NODE_VERSION_BEFORE" ] && [ -n "$NODE_VERSION_AFTER" ] && [ "$NODE_VERSION_AFTER" -lt "$NODE_VERSION_BEFORE" ] 2>/dev/null; then
    fact converged-node FAIL "the published node's version went DOWN over the update (${NODE_VERSION_BEFORE} before, ${NODE_VERSION_AFTER} after): the node's version moves forward with each step of the chain, never back"
    return 0
  fi
  if [ -z "$CROSSED_POOL" ] && [ -z "$NODE_VERSION_BEFORE" ]; then
    fact converged-node INFO "the published node of ${INSTALLATION} reads back after the update with no crossing pool among its craft_pools (${moved}), but there was no crossing to move and no version read before the update, so the version's own move was not measured here"
    return 0
  fi
  fact converged-node PASS "the published node of ${INSTALLATION} reads back after the update with no crossing pool among its craft_pools and its crafts naming one path each (${moved}, $(field "$SCRATCH/node-after.txt" pools) pool(s), $(field "$SCRATCH/node-after.txt" crafts) craft(s)): the acceptance's 'on the published node' half, read out of the node the update left. The plan's own 'whose policy version the update moves to 8' is this build's step migrateV8ToV9 of a chain that is at version ${NODE_VERSION_AFTER:-unknown} today — the register's note for Check 630 says so"
}

restore() {
  RESTORE_VERDICT=PASS
  local update_note="no update was made, so the installation is exactly as this check found it"
  if [ "$UPDATED" = yes ]; then
    update_note="the update ran and converged ${INSTALLATION}, which is the acceptance's own verb"
  fi
  local split_note=""
  if [ -n "$CROSSED_POOL" ]; then
    split_note=", and that write split the crossing pool ${CROSSED_POOL} (${CROSSED_MEMBERS}) into ${CROSSED_POOL}-claude and ${CROSSED_POOL}-provider — the split the acceptance asks for, and one that running the check again does not undo"
  fi
  RESTORE_NOTE="this check writes no credential and no pool of its own: 'fugaro accounts pool add' is asked for one and must refuse it, and the listing is read back to be sure it is absent. What it does write is the update (${update_note})${split_note}"
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
  say "CHECK 630 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] ${SANDBOX}/fugaro.yaml and fugaro doctor --all — the installation this checkout belongs to"
  say "  [read] fugaro project policy show --json — the node's own door (config.ValidatePublishedPools)"
  say "  [read] fugaro accounts ls --json — the OpenRouter and Anthropic members the crossing list needs"
  say "  [write, must be REFUSED] fugaro accounts pool add ${POOL_NAME} <openrouter-member> <anthropic-member> — the write door, and the pool is read back to be sure it is absent"
  say "  [read] fugaro validate --json in ${SANDBOX} — the command the acceptance names, and what it really reads"
  say "  [read] fugaro accounts pool ls — the config-side crossing, before the update"
  say "  [write] fugaro update — the acceptance's own convergence (it splits a crossing pool in the config and in the node)"
  say "  [read] fugaro accounts pool ls and fugaro project policy show --json again — the two pools, their members' order, and the node's version"
  for key in installation node-crossing refused-at-add validate config-crossing update converged-pools converged-node; do
    fact "$key" INFO "planned: a real installation measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no write door was asked, and no update was made"
}

BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation node-crossing
  refused-at-add validate config-crossing update converged-pools converged-node
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 630 RESULT\n' >&3
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
  printf 'CHECK 630 RESULT END (exit %s)\n' "$status" >&3
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

SANDBOX=${CHECK630_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 630 needs --sandbox: the checkout of a repository of the sandbox project. Usage: bash scripts/live-checks/check630.sh --sandbox <a sandbox repository checkout>"
fi
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PROJECT_ARG=${CHECK630_PROJECT:-${PROJECT_CLI:-}}
PROJECT=$PROJECT_ARG
POOL_NAME=${CHECK630_POOL_NAME:-chk630-crossing}
WANT_RUNNER=${CHECK630_OPENROUTER_MEMBER:-}
WANT_CREDSVCC=${CHECK630_ANTHROPIC_MEMBER:-}
CREDSVCC_FALLBACK=""
DRY_RUN=${CHECK630_DRY_RUN:-$DRY_RUN_CLI}
PREFLIGHT=${CHECK630_PREFLIGHT:-$PREFLIGHT_CLI}
PREFLIGHT_MODE=no
[ -z "$PREFLIGHT" ] || PREFLIGHT_MODE=yes
KEEP_SCRATCH=${CHECK630_KEEP_SCRATCH:-}
CLI_TIMEOUT=${CHECK630_CLI_TIMEOUT_SECONDS:-180}
DOCTOR_TIMEOUT=${CHECK630_DOCTOR_TIMEOUT_SECONDS:-300}
UPDATE_TIMEOUT=${CHECK630_UPDATE_TIMEOUT_SECONDS:-1800}
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 630 needs 'fugaro' on PATH: the check reads the installation you have, and it is not there"
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
  step_node_crossing
  step_refused_at_add
  step_validate
  step_config_crossing
  step_update
  step_converged_pools
  step_converged_node
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
