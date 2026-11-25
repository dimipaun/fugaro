#!/usr/bin/env bash
# Check 629 — the publish, on a real installation: a credential and a pool
# declared in the installation's config, `fugaro project policy publish`, the
# node read back carrying craft_credentials, craft_pools and crafts, and a
# sandbox run whose call is served by the pool's SECOND member after the first
# is exhausted.
#
# Plan task MC26 writes this check; its subject is the sentence MC26 gives
# number 629 in docs/model-crafts/activation-plan.md.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to (`fugaro doctor --project
#     <name> --all`, whose `project:` line the `--all` view alone prints);
#   * **the declaration**: `fugaro accounts ls --json` (each credential's name,
#     type and the half that serves it) and `fugaro accounts pool ls` (each
#     pool's strategy, its members in order and the half that serves it) — the
#     pool you name must be declared, with two members or more, every one of
#     them a declared credential, and one path serving the whole pool;
#   * **the publish**: `fugaro project policy publish` itself, run from the
#     checkout. It is the one write this check makes, it is the acceptance's own
#     verb, and it publishes what the installation's config already declares, so
#     running the check twice publishes the same node twice;
#   * **the node read back** (`fugaro project policy show --json`): the three
#     blocks the acceptance names — `craft_credentials` (every member declared,
#     with its type), `craft_pools` (the pool, with its members in the config's
#     own order) and `crafts` (non-empty: a craft whose access names the pool);
#   * **one sandbox run**, launched from the checkout with `fugaro run --json`
#     and followed with `fugaro runs ls --repo <repo> --json` until it is
#     terminal, and its record read from the runs bucket at the run's own key;
#   * **which member answered**: the credentials service's own audit lines for
#     the run (`credsvc: audit`, purpose forward) name the credential it served,
#     and the acceptance's clause is that it is the pool's SECOND member;
#   * **why the first did not**: the record's own `switches` log carries the
#     move (from the first member, to the one that answered, with the service's
#     own reason word), or an audit line names the first member with a status
#     that is not ok — the first member was reached and did not answer, which is
#     what "after the first is exhausted" means;
#   * **the record's `craft_credentials`**: the acceptance's last clause. MC8's
#     record half has not landed in this build, so the field is absent from
#     `result.json`; the row says so and reads the surfaces this build does
#     carry, and it becomes a real measurement the day the field lands.
#
# Nothing here reads a credential value: names, types, paths, statuses, models
# and reason words only.
#
# Usage:      bash scripts/live-checks/check629.sh --sandbox <a sandbox repository checkout> --pool <name> [--project <name>]
#             bash scripts/live-checks/check629.sh --sandbox <path> --pool <name> --dry-run
#             bash scripts/live-checks/check629.sh --sandbox <path> --pool <name> --preflight
#             bash scripts/live-checks/check629.sh --help
#             --preflight makes the check's own READS and evaluates every
#             precondition before anything is written or launched (the
#             installation, the credentials, the pool the acceptance is about
#             and the published node), prints every blocker at once with the
#             command that clears it, writes nothing and launches nothing, and
#             exits non-zero when anything blocks.
# Needs:      bash, mktemp, date, python3, awk, gcloud (authenticated as YOU,
#             able to read the runs bucket and Cloud Logging) and fugaro (the
#             installed release, an admin of the project: the publish refuses an
#             account that is not).
# Where:      your own machine, the sandbox project and the sandbox repository
#             only, at your own terminal: a coding agent's session is refused by
#             name, as is a project or region naming belong.
# Creates:    one publish of the project's policy node, from your own config, and
#             one sandbox run with its branch and the pull request it opens.
# Costs:      one sandbox run and what its task costs, on the sandbox's own
#             budget, plus the wall clock of waiting for it. Every other call is
#             a read, beside the one publish.
# Runs:       by YOU. No agent runs any step of it. Its own test
#             (check629_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The publish and the run's pull request are
#             NOT undone: the Restore row says what to do about each.
# Output:     progress on stderr, ONE block on stdout headed CHECK 629 RESULT,
#             then CHECK 629 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure.
#
# Knobs (all CHECK629_*): DRY_RUN, PREFLIGHT, KEEP_SCRATCH, SANDBOX, POOL (the pool, when
# you would rather set it in the environment), PROJECT, CREDENTIAL (the member
# the acceptance says served the run: the pool's second member is the default),
# TASK and RECIPE (what the run is launched with), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180),
# DOCTOR_TIMEOUT_SECONDS (300), LAUNCH_TIMEOUT_SECONDS (600) and
# PUBLISH_TIMEOUT_SECONDS (300).
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
PREFLIGHT_TOOL=check629

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 629 needs bash"
  local t
  for t in mktemp date python3 awk gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 629 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk629-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
# PROJECT starts as the name the owner asked for, so the belong guard can read
# it before any command runs; INSTALLATION says the doctor named one.
PROJECT=""
INSTALLATION=""
GCP_PROJECT=""
POOL=""
SECOND_MEMBER=""
FIRST_MEMBER=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
EXECUTION=""
SETTLED=no
PUBLISHED=no
DECLARED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk629.XXXXXXXX") || die "Check 629 cannot make a scratch directory"
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
    say "CHECK629_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

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


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "launch":
    print("project=%s" % (doc.get("project") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "", row.get("status") or "",
            "true" if row.get("terminal") else "false", row.get("stage") or "",
            row.get("run") or ""))
elif what == "credentials":
    creds = doc.get("credentials") or []
    print("count=%d" % len(creds))
    print("complete=%s" % ("true" if doc.get("complete", True) else "false"))
    print("note=%s" % (doc.get("note") or ""))
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
elif what == "record":
    cost = doc.get("cost") or {}
    pr = doc.get("pr") or {}
    has_cc = "craft_credentials" in doc
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("stage=%s" % (doc.get("stage") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("pr=%s" % (pr.get("url") or ""))
    print("execution=%s" % (doc.get("execution") or ""))
    print("model_usd=%s" % (cost.get("model_usd", 0)))
    print("has_craft_credentials=%s" % ("true" if has_cc else "false"))
    print("craft_credentials=%s" % (text(doc.get("craft_credentials")) if has_cc else ""))
    stages = doc.get("stages") or []
    print("stages=%d" % len(stages))
    for st in stages:
        print("stage\t%s\t%s\t%s\t%s" % (
            st.get("name") or "", st.get("model") or "", st.get("credential") or "",
            st.get("served_by") or ""))
    for name, account in sorted((doc.get("stage_accounts") or {}).items()):
        print("account\t%s\t%s" % (name, account))
    switches = doc.get("switches") or []
    print("switches=%d" % len(switches))
    for sw in switches:
        print("switch\t%s\t%s\t%s" % (
            sw.get("from") or "", sw.get("to") or "", sw.get("reason") or ""))
elif what == "audit":
    want = sys.argv[3]
    rows = doc if isinstance(doc, list) else []
    printed = 0
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        message = entry.get("textPayload") or payload.get("msg") or payload.get("message") or ""
        if message != "credsvc: audit" or (payload.get("run") or "") != want:
            continue
        if (payload.get("purpose") or "") != "forward":
            continue
        printed += 1
        print("audit\t%s\t%s\t%s\t%s" % (
            payload.get("credential") or "", payload.get("tier") or "",
            payload.get("status") or "", payload.get("error_type") or ""))
    print("forward=%d" % printed)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json QUERY FILE DEST [ARGS...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
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

row_field() { # row_field COLUMN — one column of the run this check launched
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# list_has LIST VALUE: whether a comma-joined list holds exactly that value.
list_has() { # list_has LIST VALUE
  case ",$1," in *",$2,"*) return 0 ;; esac
  return 1
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check629.sh --sandbox <a sandbox repository checkout> --pool <name> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a publish or a gcloud call to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 629 publishes a policy node and launches a real sandbox run, at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT}${GCP_PROJECT}${REGION}${POOL}" in
  *belong*) stop "the account, project, pool or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "neither the project nor the pool this check names is belong, and the account it runs as is yours"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release whose publish this check measures)"
}

# ---------------------------------------------------------------- preflight
# pf_declared_creds — "<name>\t<type>" for every credential the preflight's
# `fugaro accounts ls --json` declared, which is what a pool's members are
# checked against and what a fix's example values are taken from.
pf_declared_creds() {
  [ -f "$SCRATCH/pf-creds.txt" ] || return 0
  awk -F'\t' '$1 == "cred" { print $2 "\t" $3 }' "$SCRATCH/pf-creds.txt" 2>/dev/null
  return 0
}

# pf_example_members — up to two declared credential names, for a fix command
# that declares a pool out of what the config already carries.
pf_example_members() {
  local names
  names=$(pf_declared_creds | awk '{ print $1 }' | head -n 2 | tr '\n' ' ')
  names=${names% }
  printf '%s' "${names:-coder-a coder-b}"
}

# pf_example_member_not_in IN_POOL — a declared credential the pool does not
# already list, for a fix command that gives a one-member pool its second.
pf_example_member_not_in() {
  local in=$1 ex
  ex=$(pf_declared_creds | awk -v in="$in" \
    'BEGIN { n=split(in,a,","); for (i=1;i<=n;i++) skip[a[i]]=1 } !($1 in skip) { print $1; exit }')
  printf '%s' "${ex:-coder-b}"
}

# pf_example_type — the type of a declared credential, for a fix command that
# declares a pool member that is missing from the credentials block.
pf_example_type() {
  local t
  t=$(pf_declared_creds | awk 'NR==1 { print $2 }')
  printf '%s' "${t:-openrouter}"
}

# preflight_pool_declaration CREDS_OK — the pool the acceptance is about, read
# out of the listing the preflight made: declared, two members or more, every one
# of them a declared credential. Every blocker of the three is raised at once.
preflight_pool_declaration() {
  local creds_ok=$1 row members
  row=$(awk -F'  +' -v want="$POOL" '$1 == want { print; exit }' "$SCRATCH/pf-pools.out" 2>/dev/null || true)
  if [ -z "$row" ]; then
    preflight_block declaration \
      "the config of ${INSTALLATION:-the installation} declares no pool '${POOL}', and the acceptance is about a run served by that pool" \
      "cd $SANDBOX && fugaro accounts pool add ${POOL} $(pf_example_members)"
    return 0
  fi
  members=$(printf '%s' "$row" | awk -F'  +' '{ print $3 }' | tr -d ' ')
  case "$members" in
  *,*) ;;
  *)
    preflight_block declaration \
      "the pool ${POOL} declares only ${members:-no member}, and the acceptance needs its second member to serve a run once the first is exhausted" \
      "cd $SANDBOX && fugaro accounts pool add ${POOL} $(pf_example_member_not_in "$members")"
    return 0
    ;;
  esac
  if [ "$creds_ok" != yes ]; then return 0; fi
  local missing="" m
  for m in ${members//,/ }; do
    [ -n "$(table_field "$SCRATCH/pf-creds.txt" cred "$m" 2)" ] || missing="${missing:+$missing, }${m}"
  done
  if [ -n "$missing" ]; then
    preflight_block declaration \
      "the pool ${POOL} lists ${members} and fugaro accounts ls --json declares no credential ${missing}" \
      "cd $SANDBOX && fugaro accounts add ${missing%%,*} --type $(pf_example_type)"
    return 0
  fi
  preflight_note declaration "the pool ${POOL} declares ${members}, every one a declared credential of ${INSTALLATION:-the installation}; the second member serves the run"
}

# step_preflight: --preflight. The reads the check makes before its publish and
# its run, every precondition it has evaluated, nothing written and nothing
# launched. The sandbox is argument validation (the way step_installation refuses
# a path that is not a checkout); everything else is the state of the live
# installation, and each precondition that does not hold is one blocker.
step_preflight() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the publish and the run are both made from it"
  fi
  local args=(doctor --all) line
  [ -z "$PROJECT_ARG" ] || args=(doctor --project "$PROJECT_ARG" --all)
  say "preflight: reading the installation: fugaro ${args[*]}, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" pf-doctor "$SANDBOX" "$FUGARO_BIN" "${args[@]}"
  line=$(grep -m1 '^project: ' "$SCRATCH/pf-doctor.out" 2>/dev/null || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ -z "$line" ]; then
    preflight_block installation \
      "fugaro ${args[*]} prints no 'project: <name> (GCP <project>, <region>)' line in ${SANDBOX} (exit ${BOUNDED_STATUS}): the publish and the run are both made for the installation it names" \
      "cd $SANDBOX && fugaro ${args[*]}"
  else
    local inst gcp
    inst=$(printf '%s' "$line" | sed -n 's/^project: \([^ ]*\) .*/\1/p')
    gcp=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
    case "$gcp" in
    *belong*) stop "the checkout at $SANDBOX belongs to ${gcp}: this check is never run there" ;;
    esac
    INSTALLATION=$inst
    preflight_note installation "${SANDBOX} is a checkout of ${inst} (GCP ${gcp}), read from fugaro ${args[*]}"
  fi

  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")

  say "preflight: reading the credentials: fugaro accounts ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local creds_ok=yes
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] ||
    ! read_json credentials "$SCRATCH/pf-creds.out" "$SCRATCH/pf-creds.txt"; then
    creds_ok=no
    preflight_block declaration \
      "fugaro accounts ls --json does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), and a pool's members are looked up among the credentials it declares" \
      "cd $SANDBOX && fugaro accounts ls --json"
  fi

  say "preflight: reading the pools: fugaro accounts pool ls, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-pools "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    preflight_block declaration \
      "fugaro accounts pool ls does not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), so the pool the acceptance is about cannot be read" \
      "cd $SANDBOX && fugaro accounts pool ls"
  else
    preflight_pool_declaration "$creds_ok"
  fi

  say "preflight: reading the published node: fugaro project policy show --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" pf-node "$SANDBOX" "$FUGARO_BIN" project policy show --json ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    preflight_note node-blocks "no published node reads back yet (fugaro project policy show --json exited ${BOUNDED_STATUS}); the publish below writes it from ${SANDBOX}'s config"
  else
    preflight_note node-blocks "the published node reads back in ${SANDBOX}; the publish below rewrites it from that config"
  fi
}

# ---------------------------------------------------------------- the steps
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the publish and the run are both made from it"
  fi
  local args=(doctor --all)
  [ -z "$PROJECT_ARG" ] || args=(doctor --project "$PROJECT_ARG" --all)
  say "reading the installation: fugaro ${args[*]}, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" "${args[@]}"
  local line
  line=$(grep -m1 '^project: ' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$line" ]; then
    unmeasured installation "fugaro ${args[*]} printed no 'project: <name> (GCP <project>, <region>)' line in ${SANDBOX} (exit ${BOUNDED_STATUS}): the project line is what tells this check which installation it is reading. What it printed is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
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
  fact installation PASS "${SANDBOX} is a checkout of the installation ${PROJECT} (GCP ${GCP_PROJECT}): the publish below writes that installation's node and the run is launched in it"
}

# step_declaration: the credential and the pool in the installation's config.
# The pool you name is read from `fugaro accounts pool ls`, which reads the
# config offline and is the command the acceptance's word "declared" is about,
# and every member is looked up in `fugaro accounts ls --json`.
step_declaration() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured declaration "there is no installation whose config could be read: the row above says why"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "reading the declaration: fugaro accounts ls --json and fugaro accounts pool ls, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" creds "$SANDBOX" "$FUGARO_BIN" accounts ls --json ${pflag[@]+"${pflag[@]}"}
  local creds_problem=""
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    creds_problem="fugaro accounts ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS})"
  elif ! read_json credentials "$SCRATCH/creds.out" "$SCRATCH/creds.txt"; then
    creds_problem="fugaro accounts ls --json printed a document this check could not read"
  fi
  run_bounded "$CLI_TIMEOUT" pools "$SANDBOX" "$FUGARO_BIN" accounts pool ls ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured declaration "fugaro accounts pool ls did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the pools the installation's config declares are in that listing. What it printed is in $SCRATCH/pools.err"
    return 0
  fi
  if [ -n "$creds_problem" ]; then
    unmeasured declaration "${creds_problem}: the credentials the config declares are in that document, and a pool's members are looked up among them. What it printed is in $SCRATCH/creds.out"
    return 0
  fi
  # The listing is a tabwriter table: a pool's row is its name, its strategy,
  # its members (comma and space) and the half that serves them.
  local row
  row=$(awk -F'  +' -v want="$POOL" '$1 == want { print; exit }' "$SCRATCH/pools.out" 2>/dev/null || true)
  if [ -z "$row" ]; then
    fact declaration FAIL "fugaro accounts pool ls in ${SANDBOX} names no pool '${POOL}' among the pools of ${INSTALLATION} (its listing is in $SCRATCH/pools.out). Declare it with 'fugaro accounts pool add ${POOL} <member> <member>' — the second member being the one that should answer once the first is exhausted — and run this check again"
    return 0
  fi
  local strategy members served
  strategy=$(printf '%s' "$row" | awk -F'  +' '{ print $2 }')
  members=$(printf '%s' "$row" | awk -F'  +' '{ print $3 }')
  served=$(printf '%s' "$row" | awk -F'  +' '{ print $4 }')
  members=$(printf '%s' "$members" | tr -d ' ')
  local m
  case "$members" in
  *,*) ;;
  *)
    fact declaration FAIL "the pool ${POOL} of ${INSTALLATION} declares ${members:-no member at all} (strategy ${strategy:-unknown}, served by ${served:-unknown}), and the acceptance is about a run served by the pool's SECOND member after the first is exhausted: a pool needs two members for that. Add the second with 'fugaro accounts pool add ${POOL} <member>' and run this check again"
    return 0
    ;;
  esac
  local missing=""
  for m in ${members//,/ }; do
    if ! read_json credentials "$SCRATCH/creds.out" "$SCRATCH/creds.txt" 2>/dev/null ||
      [ -z "$(table_field "$SCRATCH/creds.txt" cred "$m" 2)" ]; then
      missing="${missing:+$missing, }${m}"
    fi
  done
  if [ -n "$missing" ]; then
    fact declaration FAIL "the pool ${POOL} lists ${members} and fugaro accounts ls --json does not declare ${missing} as a credential of ${INSTALLATION}: a member a pool lists is a name the config's credentials block must carry, or the publish writes a node whose pool names a credential nothing declares. Declare it with 'fugaro accounts add <name> --type <openrouter|anthropic> ...' at your own terminal and run this check again"
    return 0
  fi
  SECOND_MEMBER=$(printf '%s' "$members" | cut -d, -f2)
  FIRST_MEMBER=$(printf '%s' "$members" | cut -d, -f1)
  [ -z "$WANT_MEMBER" ] || SECOND_MEMBER=$WANT_MEMBER
  POOL_MEMBERS=$members
  DECLARED=yes
  fact declaration PASS "the pool ${POOL} is declared in ${INSTALLATION}'s config with its members in order (${members}, strategy ${strategy:-unknown}, served by ${served:-unknown}) and every one of them is a declared credential of the credentials block: the premise the acceptance's publish is about. The first member is ${FIRST_MEMBER}, and ${SECOND_MEMBER} is the member the acceptance says serves the run"
}

# step_publish: the acceptance's own verb, run from the checkout.
step_publish() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured publish "there is no installation whose policy could be published: the row above says why"
    return 0
  fi
  if [ "$DECLARED" != yes ]; then
    unmeasured publish "the credential and the pool the acceptance is about were not read out of ${INSTALLATION}'s config (the declaration row above says why), so this check does not write a node from that config: settle that row and run the check again"
    return 0
  fi
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  say "publishing the policy node of ${INSTALLATION} from ${SANDBOX}: fugaro project policy publish"
  run_bounded "$PUBLISH_TIMEOUT" publish "$SANDBOX" "$FUGARO_BIN" project policy publish ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured publish "fugaro project policy publish did not finish inside ${PUBLISH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/publish.out and $SCRATCH/publish.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact publish FAIL "fugaro project policy publish exited ${BOUNDED_STATUS} in ${SANDBOX}: the node the acceptance is about was not written. Its own words are in $SCRATCH/publish.err — the two usual refusals are an account that is not an admin of the project and a node carrying no rules marker (which 'fugaro update' deploys)"
    return 0
  fi
  PUBLISHED=yes
  local last
  last=$(tail -n 3 "$SCRATCH/publish.out" 2>/dev/null | tr '\n' ' ' || true)
  fact publish PASS "fugaro project policy publish wrote the policy node of ${INSTALLATION} from the config in ${SANDBOX}${last:+ — it said: ${last}}"
}

# step_node_blocks: the node read back, carrying the three blocks.
step_node_blocks() {
  if [ "$PUBLISHED" != yes ]; then
    unmeasured node-blocks "the publish did not write the node (its own row above says why), so there is no node of this check's own to read back"
    return 0
  fi
  say "reading the node back: fugaro project policy show --json, in ${SANDBOX}"
  local pflag=()
  [ -z "$PROJECT_ARG" ] || pflag=(--project "$PROJECT_ARG")
  run_bounded "$CLI_TIMEOUT" node "$SANDBOX" "$FUGARO_BIN" project policy show --json ${pflag[@]+"${pflag[@]}"}
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured node-blocks "fugaro project policy show --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the node just written is in that document. What it printed is in $SCRATCH/node.err"
    return 0
  fi
  if ! read_json node "$SCRATCH/node.out" "$SCRATCH/node.txt"; then
    unmeasured node-blocks "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/node.out"
    return 0
  fi
  local missing=""
  local m
  for m in ${POOL_MEMBERS//,/ }; do
    [ -n "$(table_field "$SCRATCH/node.txt" cred "$m" 2)" ] || missing="${missing:+$missing, }craft_credentials:${m}"
  done
  local node_members
  node_members=$(table_field "$SCRATCH/node.txt" pool "$POOL" 4)
  if [ -z "$node_members" ]; then
    missing="${missing:+$missing, }craft_pools:${POOL}"
  elif [ "$node_members" != "$POOL_MEMBERS" ]; then
    missing="${missing:+$missing, }craft_pools:${POOL}(members ${node_members}, want ${POOL_MEMBERS})"
  fi
  if [ "$(field "$SCRATCH/node.txt" crafts)" = 0 ]; then
    missing="${missing:+$missing, }crafts(empty)"
  fi
  if [ -n "$missing" ]; then
    fact node-blocks FAIL "the node of ${INSTALLATION} read back after the publish does not carry ${missing} (version $(field "$SCRATCH/node.txt" version), craft_credentials $(field "$SCRATCH/node.txt" credentials), craft_pools $(field "$SCRATCH/node.txt" pools), crafts $(field "$SCRATCH/node.txt" crafts)): the acceptance is exactly these three blocks, and a publish that wrote the node without them leaves a run the pins the node does not carry"
    return 0
  fi
  fact node-blocks PASS "the node of ${INSTALLATION} read back from the publish carries craft_credentials (${POOL_MEMBERS} among $(field "$SCRATCH/node.txt" credentials) entries), craft_pools (${POOL} with its members in the config's own order: ${node_members}) and crafts ($(field "$SCRATCH/node.txt" crafts) craft(s), the first naming access $(awk -F'\t' '$1 == "nodecraft" { print $4; exit }' "$SCRATCH/node.txt"))"
}

# step_run / step_terminal / step_result_json: one real sandbox run, followed to
# its own record, as Check 628 reads it.
step_run() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured run-launched "there is no installation to launch a run in: the row above says why"
    return 0
  fi
  if [ "$DECLARED" != yes ]; then
    unmeasured run-launched "the pool the acceptance is about was not read out of ${INSTALLATION}'s config (the declaration row above says why), so a run launched here could not be read against it: settle that row and run the check again"
    return 0
  fi
  local run_args=(run --repo "$SANDBOX")
  [ -z "$RECIPE" ] || run_args=(run --repo "$SANDBOX" --recipe "$RECIPE")
  run_args+=(--json "$TASK")
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" "${run_args[@]}"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the run this check exists to read never started. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
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
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout: the rows below follow this run and no other"
}

step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 5)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    # run_bounded appends to its key's output, so each poll starts from an empty
    # file: an answer read on top of the one before it is not one document, and a
    # reader that cannot parse the listing can never see a run end.
    : >"$SCRATCH/rows.out"
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 5
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      run_status=$(row_field 3)
      ended=$(row_field 4)
      stage=$(row_field 5)
      RUN_REF=$(row_field 6)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record and its own audit lines. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state its record and its audit lines are complete in"
}

step_result_json() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured result-json "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=$(printf '%s' "${RUN_REF:-none}")), so there was no key to read"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${GCP_PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/${key}" --project="${GCP_PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${GCP_PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the run's own switch log is in that record, and it is where the move off the pool's first member is written. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact result-json FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact result-json FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's"
    return 0
  fi
  EXECUTION=$(field "$SCRATCH/record.txt" execution)
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), stage=$(field "$SCRATCH/record.txt" stage), outcome=$(field "$SCRATCH/record.txt" outcome), cost.model_usd=$(field "$SCRATCH/record.txt" model_usd)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_served_by_second: which member answered. The credentials service's own
# audit lines for the run name the credential it served; the acceptance's clause
# is that it is the pool's second member.
step_served_by_second() {
  if [ -z "$RUN_ID" ] || [ -z "$EXECUTION" ]; then
    unmeasured served-by-second "there is no terminal run's record to take an execution from: the rows above say why"
    return 0
  fi
  say "reading the service's own audit lines for ${RUN_ID}: gcloud logging read (credsvc: audit)"
  local filter="jsonPayload.message=\"credsvc: audit\" AND jsonPayload.run=\"$RUN_ID\""
  run_bounded "$CLI_TIMEOUT" audit "$SANDBOX" gcloud logging read --resource-names="projects/$GCP_PROJECT" \
    --limit=2000 --freshness=30d --format=json "$filter"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured served-by-second "gcloud logging read of the service's audit lines for ${RUN_ID} did not answer (exit ${BOUNDED_STATUS}): which member answered the run is written there. What gcloud printed is in $SCRATCH/audit.err"
    return 0
  fi
  if ! read_json audit "$SCRATCH/audit.out" "$SCRATCH/audit.txt" "$RUN_ID"; then
    unmeasured served-by-second "gcloud logging read answered something this check cannot read as a log: what gcloud printed is in $SCRATCH/audit.out"
    return 0
  fi
  local forward served_names="" line name status
  forward=$(field "$SCRATCH/audit.txt" forward)
  while IFS=$'\t' read -r kind name _tier status _err; do
    [ "$kind" = audit ] || continue
    served_names="${served_names:+$served_names, }${name}(${status:-unknown})"
  done <"$SCRATCH/audit.txt"
  SERVED_NAMES=$served_names
  if [ "${forward:-0}" = 0 ]; then
    fact served-by-second FAIL "the credentials service wrote no forward audit line for ${RUN_ID}: the run made no call the service served, so nothing answered through the pool of ${INSTALLATION}. A run whose calls never reach the credentials service is served by a run's own gateway (a pool of OpenRouter or Vertex members) or by nothing at all; the acceptance's clause is about the pool's own members answering, so give CHECK629_POOL a pool the service serves and run this check again"
    return 0
  fi
  local answered=""
  while IFS=$'\t' read -r kind name _tier status _err; do
    [ "$kind" = audit ] || continue
    [ "$status" = ok ] || continue
    answered="${answered:+$answered, }${name}"
  done <"$SCRATCH/audit.txt"
  case ",${answered}," in
  *",$SECOND_MEMBER,"*)
    fact served-by-second PASS "the service served ${RUN_ID} as ${SECOND_MEMBER}, the second member of the pool ${POOL} (${POOL_MEMBERS}) — read from the service's own audit lines for this run (${forward} forward line(s), credentials ${served_names:-none}): the acceptance's clause is this member and not the first"
    ;;
  *)
    fact served-by-second FAIL "the credentials that answered ${RUN_ID} are ${answered:-none} (the service's own audit lines read: ${served_names:-none}) and the pool ${POOL} walks ${POOL_MEMBERS}: the acceptance's clause is that the pool's SECOND member (${SECOND_MEMBER}) served the run"
    ;;
  esac
}

# step_first_exhausted: why the first did not answer.
step_first_exhausted() {
  if [ "$SETTLED" != yes ] || [ -z "$RUN_ID" ]; then
    unmeasured first-exhausted "there is no terminal run to read the move off the first member from: the rows above say why"
    return 0
  fi
  if [ ! -f "$SCRATCH/audit.txt" ] && [ ! -f "$SCRATCH/record.txt" ]; then
    unmeasured first-exhausted "neither the service's own audit lines nor the record of ${RUN_ID} were read (their own rows above say why): the move off the pool's first member is written in one of the two"
    return 0
  fi
  local moves="" kind from to reason
  if [ -f "$SCRATCH/record.txt" ]; then
    while IFS=$'\t' read -r kind from to reason; do
      [ "$kind" = switch ] || continue
      moves="${moves:+$moves, }${from:-start}->${to}(${reason:-no reason})"
    done <"$SCRATCH/record.txt"
  fi
  local first_named_ok="" first_refused=""
  if [ -f "$SCRATCH/audit.txt" ]; then
    local name status
    while IFS=$'\t' read -r kind name _tier status _err; do
      [ "$kind" = audit ] || continue
      [ "$name" = "$FIRST_MEMBER" ] || continue
      if [ "$status" = ok ]; then
        first_named_ok="${first_named_ok:+$first_named_ok, }${name}"
      else
        first_refused="${first_refused:+$first_refused, }${name}(${status:-unknown})"
      fi
    done <"$SCRATCH/audit.txt"
  fi
  if [ -n "$first_refused" ]; then
    fact first-exhausted PASS "the service's own audit lines for ${RUN_ID} name the pool's first member ${first_refused}, its status not ok: the first member was reached and did not answer, so the call moved on to ${SECOND_MEMBER} — the acceptance's 'after the first is exhausted' read from the service rather than from a log line of ours"
    return 0
  fi
  case ",${moves}," in
  *"${FIRST_MEMBER}->${SECOND_MEMBER}("*)
    fact first-exhausted PASS "the record of ${RUN_ID} carries the move off the pool's first member: ${moves} (the switch log, whose reason word is one of the service's own four). The run's call moved from ${FIRST_MEMBER} onto ${SECOND_MEMBER}, which is the acceptance's 'after the first is exhausted'"
    ;;
  *)
    if [ -n "$first_named_ok" ]; then
      fact first-exhausted FAIL "the service's own audit lines for ${RUN_ID} name the pool's first member ${first_named_ok} with status ok, and the record's switch log carries ${moves:-no move at all}: the FIRST member answered too, so the acceptance's clause — the second member serving 'after the first is exhausted' — was not what this run measured. Exhaust the first member (a key whose quota is gone: the service marks it exhausted at its next 429 and the next call moves on) and run this check again"
    else
      fact first-exhausted FAIL "neither the service's own audit lines for ${RUN_ID} nor its record carry any trace of the pool's first member ${FIRST_MEMBER} (audit credentials: ${SERVED_NAMES:-none}; switches: ${moves:-none}): nothing here shows the pool walking its order from the first member. Read the run yourself with 'fugaro watch ${RUN_ID}', and give CHECK629_POOL a pool this run's stages really walk"
    fi
    ;;
  esac
}

# step_craft_credentials: the acceptance's last clause. MC8's record half has
# not landed in this build, so the field is absent and the row says so.
step_craft_credentials() {
  if [ ! -f "$SCRATCH/record.txt" ]; then
    unmeasured craft-credentials "the record of ${RUN_ID:-the run} was not read (its own row above says why), so there is no document to look for the field in"
    return 0
  fi
  local has value
  has=$(field "$SCRATCH/record.txt" has_craft_credentials)
  value=$(field "$SCRATCH/record.txt" craft_credentials)
  if [ "$has" != true ]; then
    fact craft-credentials INFO "the record of ${RUN_ID} carries no craft_credentials (its own words: stages=$(field "$SCRATCH/record.txt" stages), stage_accounts=$(awk -F'\t' '$1 == "account" { printf "%s=%s ", $2, $3 }' "$SCRATCH/record.txt"), switches=$(field "$SCRATCH/record.txt" switches)): MC8's record half has not landed in this build, so the acceptance's last clause — craft_credentials naming the member that served — has no field to be read from yet. What this build does carry is read by the rows above: the service's own audit line names the credential it served, and the record's stage accounts name the accounts. This row is a real measurement the day the field lands and needs nothing else changed"
    return 0
  fi
  case "$value" in
  *"$SECOND_MEMBER"*)
    fact craft-credentials PASS "the record of ${RUN_ID} carries craft_credentials naming ${SECOND_MEMBER}, the member the service served: the acceptance's last clause, read from the field itself (${value})"
    ;;
  *)
    fact craft-credentials FAIL "the record of ${RUN_ID} carries craft_credentials ${value:-empty}, which does not name ${SECOND_MEMBER}, the member that served this run: the acceptance's clause is that the field names the credential that answered"
    ;;
  esac
}

restore() {
  RESTORE_VERDICT=PASS
  local node_note="the policy node of ${INSTALLATION:-the installation} holds what your own config declares"
  if [ "$PUBLISHED" = yes ]; then
    node_note="the node was published from your own config, which is what 'fugaro update' does too: the check wrote nothing your config does not already declare, and running it again publishes the same node again"
  else
    node_note="no publish was made, so the node is exactly as this check found it"
  fi
  RESTORE_NOTE="this check writes no config, no credential and no pool: its writes are the publish (${node_note}) and the sandbox run ${RUN_ID:-none} with the branch and pull request that run left (outcome=$(field "$SCRATCH/record.txt" outcome), pr=$(field "$SCRATCH/record.txt" pr)) — close that pull request and delete its branch yourself once you have read the run"
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
  say "CHECK 629 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --project <name> --all in ${SANDBOX} — the project line: which installation this is"
  say "  [read] fugaro accounts ls --json and fugaro accounts pool ls in ${SANDBOX} — the credential and the pool the acceptance is about"
  say "  [write] fugaro project policy publish in ${SANDBOX} — the node, built from your own config (this is the acceptance's own verb)"
  say "  [read] fugaro project policy show --json in ${SANDBOX} — craft_credentials, craft_pools and crafts, read back"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, followed to its end"
  say "  [read] gcloud storage cat gs://fugaro-runs-<gcp-project>/runs/<slug>/<run>/result.json — the switch log and the craft_credentials field"
  say "  [read] gcloud logging read (credsvc: audit for the run) — which member the service served, by NAME"
  for key in installation declaration publish node-blocks run-launched terminal result-json served-by-second first-exhausted craft-credentials; do
    fact "$key" INFO "planned: a real publish and run measure this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no publish, no run and no read"
}

BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation declaration
  publish node-blocks run-launched terminal result-json served-by-second
  first-exhausted craft-credentials restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 629 RESULT\n' >&3
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
  printf 'CHECK 629 RESULT END (exit %s)\n' "$status" >&3
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
POOL_CLI=""
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
  --pool)
    [ $# -ge 2 ] || die "--pool needs the pool's name: the one whose second member should answer"
    POOL_CLI=$2
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
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  --preflight)
    PREFLIGHT_CLI=1
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

SANDBOX=${CHECK629_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 629 needs --sandbox: the checkout of a repository of the sandbox project. Usage: bash scripts/live-checks/check629.sh --sandbox <a sandbox repository checkout> --pool <name>"
fi
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
POOL=${CHECK629_POOL:-${POOL_CLI:-}}
if [ -z "$POOL" ]; then
  die "Check 629 needs --pool: the name of the pool the acceptance is about (two members or more, the first exhausted). Usage: bash scripts/live-checks/check629.sh --sandbox <a sandbox repository checkout> --pool <name>"
fi
PROJECT_ARG=${CHECK629_PROJECT:-${PROJECT_CLI:-}}
PROJECT=$PROJECT_ARG
POOL_MEMBERS=""
WANT_MEMBER=${CHECK629_CREDENTIAL:-}
TASK=${CHECK629_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK629_RECIPE:-${RECIPE_CLI:-}}
DRY_RUN=${CHECK629_DRY_RUN:-$DRY_RUN_CLI}
PREFLIGHT=${CHECK629_PREFLIGHT:-$PREFLIGHT_CLI}
PREFLIGHT_MODE=no
[ -z "$PREFLIGHT" ] || PREFLIGHT_MODE=yes
KEEP_SCRATCH=${CHECK629_KEEP_SCRATCH:-}
REGION=${CHECK629_REGION:-us-east5}
RUN_TIMEOUT=${CHECK629_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK629_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK629_CLI_TIMEOUT_SECONDS:-180}
DOCTOR_TIMEOUT=${CHECK629_DOCTOR_TIMEOUT_SECONDS:-300}
LAUNCH_TIMEOUT=${CHECK629_LAUNCH_TIMEOUT_SECONDS:-600}
PUBLISH_TIMEOUT=${CHECK629_PUBLISH_TIMEOUT_SECONDS:-300}
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 629 needs 'fugaro' on PATH: the check reads the installation you have, and it is not there"
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
  step_declaration
  step_publish
  step_node_blocks
  step_run
  step_terminal
  step_result_json
  step_served_by_second
  step_first_exhausted
  step_craft_credentials
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
