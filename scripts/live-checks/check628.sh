#!/usr/bin/env bash
# Check 628 — the model half, on a real installation: the project layer's craft
# model is the one the coder's craft runs, the coder's craft names the same
# credential, and nothing about it is a "not pinned for the stage" refusal.
#
# Plan task MC26 writes this check; its subject is the sentence MC26 gives
# number 628 in docs/model-crafts/activation-plan.md: "A project layer whose
# `defaults.agent.crafts.coder.model` names a model the project's allow-list
# admits, a sandbox run whose record's StageTiming.Model for the coder stage and
# `fugaro craft ls` name that model and the same credential, the gateway raising
# no 'not pinned for the stage' refusal, and `agent.models.coder` left alone."
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to, read from `fugaro doctor
#     --project <name> --all` in that checkout: the `project:` line is printed
#     by the `--all` view alone, and it is where this check's project, GCP
#     project and region come from;
#   * **the project layer**: `fugaro config show --json` resolves the
#     checkout's file over the project layer, so the coder craft's model is read
#     at its own path (`agent.crafts.coder.model`) with the SOURCE that wrote it
#     — the layer's key must be the one in force, and
#     `agent.models.coder` (the role slot below the craft in the ladder) must
#     NOT be the layer's: the acceptance says the craft key named the model and
#     the slot was left alone;
#   * **the craft's own answer**: `fugaro craft ls --json` — the resolution a run
#     makes, from the same layers — carries the coder's model, where it came
#     from (`model_from`), the access that serves it and whether the model is
#     servable in this project at all;
#   * **one sandbox run**, launched from the checkout with `fugaro run --json`
#     and followed with `fugaro runs ls --repo <repo> --json` until it is
#     terminal — a record read while a run is still working is whatever was
#     there before it;
#   * **its record** (`result.json`, read from the runs bucket at the key the
#     listing's own `run` ref gives, checked against the run id this check
#     launched): every stage the coder craft serves (`plan`, `implement`, `fix`
#     — internal/runner's stageCraft) records that model, and the run's own
#     stages and accounts name the credential the craft's access named;
#   * **the gateway's own lines for that execution** (`gcloud logging read`,
#     filtered by the execution's own labels): NO `budget: request refused` line
#     whose `violation` says a model "is not pinned for" a stage. That refusal
#     is the whole of the acceptance's last-but-one clause, and the gateway is
#     what raises it (internal/gateway/forward.go, checkRequest);
#   * **the credentials service's own audit line for the run** (`credsvc:
#     audit`): the NAME of the credential it served — never a value — which is
#     what says which credential answered, when the craft's access is one the
#     service serves;
#   * nothing else. No credential value is read, and none can reach the block:
#     the rows carry names, paths, sources, model IDs, statuses and money.
#
# **This build's record carries no `craft_credentials`.** MC8's record half has
# not landed, so the row that would read the stage's credential off that
# additive field reads the surfaces this build does have — the record's
# `stage_accounts` and per-stage `credential`/`served_by`, and the service's own
# audit line — and says so; Check 420 of the same plan reports the missing field
# the same way.
#
# Usage:      bash scripts/live-checks/check628.sh --sandbox <a sandbox repository checkout> [--project <name>]
#             bash scripts/live-checks/check628.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check628.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, able to read the runs bucket
#             and Cloud Logging) and fugaro (the installed release: the whole
#             point is the build you have).
# Where:      your own machine, the sandbox project and the sandbox repository
#             only, at your own terminal: a coding agent's session is refused by
#             name, as is a project or region naming belong.
# Creates:    one sandbox run — a real run, with its branch and the pull request
#             it opens if its agent commits. Nothing else: this check writes no
#             config, publishes no policy and changes no secret. On disk: one
#             scratch directory, removed at the end.
# Costs:      one sandbox run and what its task costs, on the sandbox's own
#             budget, plus the wall clock of waiting for it. Every other call is
#             a read.
# Runs:       by YOU. No agent runs any step of it, and none has: what it
#             measures is a live installation. Its own test (check628_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's pull request and branch are NOT
#             closed by this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 628 RESULT,
#             then CHECK 628 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a read did not answer, or the run never ended. A key the run
#             never reached reads `FAIL — not reached`.
#
# Knobs (all CHECK628_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), PROJECT (the installation's own name,
# when the checkout's is not the one to read), CRAFT (coder), TASK and RECIPE
# (what the run is launched with), LOG_VIEW (a Cloud Logging view to scope the
# reads to, when the installation keeps its logs in one), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180),
# DOCTOR_TIMEOUT_SECONDS (300) and LAUNCH_TIMEOUT_SECONDS (600).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check reads a
# live project, and a trace of a gcloud or fugaro call is a second copy of
# output that may name a resource the block then prints twice.
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
  command -v bash >/dev/null 2>&1 || die "Check 628 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 628 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk628-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
# PROJECT starts as the name the owner asked for, so that the belong guard can
# read it before any command runs, and becomes the installation the doctor
# named once that read has answered: INSTALLATION is what says it did.
PROJECT=""
INSTALLATION=""
GCP_PROJECT=""
REGION_USED=""
LAYER_MODEL=""
CRAFT_ACCESS=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
EXECUTION=""
SETTLED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk628.XXXXXXXX") || die "Check 628 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
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
    say "CHECK628_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The documents this check reads, in one program: a live check on the owner's
# machine has python3 and no jq, and a row that read a document with grep would
# report a model ID the day the document is re-wrapped. Every query prints
# `key=value` lines or `\t`-separated rows, so a shell can hold them without a
# second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def text(value):
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    return json.dumps(value, sort_keys=True)


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "config":
    # Every resolved key with the layer that wrote it (internal/cli/config_show.go):
    # "default", "repo", "project" or "profile <name>".
    print("count=%d" % len(doc.get("values") or []))
    for row in doc.get("values") or []:
        print("value\t%s\t%s\t%s" % (
            row.get("path") or "", text(row.get("value")), row.get("source") or ""))
elif what == "launch":
    print("project=%s" % (doc.get("project") or ""))
    print("run=%s" % (doc.get("run") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("branch=%s" % (doc.get("branch") or ""))
    print("execution=%s" % (doc.get("execution") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "crafts":
    print("count=%d" % len(doc.get("crafts") or []))
    for row in doc.get("crafts") or []:
        print("craft\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("craft") or "", row.get("model") or "", row.get("model_from") or "",
            row.get("access") or "", row.get("access_from") or "", yes(row.get("inert")),
            yes(row.get("model_servable")), row.get("model_problem") or ""))
elif what == "node":
    # `fugaro project policy show --json` prints {"project": ..., "policy": ...}:
    # every block of the node is a member of `policy`, and read at the top level
    # each one is null — an allow-list read that way is always empty, so the row
    # below measures nothing.
    policy = doc.get("policy")
    policy = policy if isinstance(policy, dict) else {}
    budget = policy.get("budget")
    budget = budget if isinstance(budget, dict) else {}
    pools = policy.get("craft_pools") or []
    creds = policy.get("craft_credentials") or []
    crafts = policy.get("crafts") or {}
    print("version=%s" % policy.get("version", ""))
    print("allowed_models=%s" % ";".join(budget.get("allowed_models") or []))
    print("pools=%d" % len(pools))
    print("credentials=%d" % len(creds))
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
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "", row.get("status") or "",
            yes(row.get("terminal")), row.get("stage") or "", row.get("run") or ""))
elif what == "record":
    cost = doc.get("cost") or {}
    pr = doc.get("pr") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("stage=%s" % (doc.get("stage") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("pr=%s" % (pr.get("url") or ""))
    print("execution=%s" % (doc.get("execution") or ""))
    print("model_usd=%s" % (cost.get("model_usd", 0)))
    print("total_usd=%s" % (cost.get("total_usd", 0)))
    print("total_usd_value=%s" % (cost.get("total_usd", 0)))
    stages = doc.get("stages") or []
    print("stages=%d" % len(stages))
    for st in stages:
        print("stage\t%s\t%s\t%s\t%s\t%s" % (
            st.get("name") or "", st.get("model") or "", st.get("credential") or "",
            st.get("served_by") or "", st.get("model_usd", 0)))
    for name, account in sorted((doc.get("stage_accounts") or {}).items()):
        print("account\t%s\t%s" % (name, account))
    print("oauth_account=%s" % (doc.get("oauth_account") or ""))
    switches = doc.get("switches") or []
    print("switches=%d" % len(switches))
    for sw in switches:
        print("switch\t%s\t%s\t%s" % (
            sw.get("from") or "", sw.get("to") or "", sw.get("reason") or ""))
elif what == "calls":
    rows = doc if isinstance(doc, list) else []
    calls = 0
    refused = 0
    violations = []
    errors = []
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        if not payload:
            continue
        if payload.get("message") == "budget: request refused":
            refused += 1
            v = payload.get("violation") or ""
            if v and v not in violations:
                violations.append(v)
            continue
        if payload.get("message") != "model call":
            continue
        calls += 1
        if payload.get("error_type") and payload["error_type"] not in errors:
            errors.append(payload["error_type"])
    print("refusals=%d" % refused)
    print("calls=%d" % calls)
    print("violations=%s" % " | ".join(violations))
    print("errors=%s" % ";".join(errors))
elif what == "audit":
    want = sys.argv[3]
    rows = doc if isinstance(doc, list) else []
    forwarded = 0
    credential = ""
    tier = ""
    status = ""
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        message = entry.get("textPayload") or payload.get("msg") or payload.get("message") or ""
        if message != "credsvc: audit" or (payload.get("run") or "") != want:
            continue
        if (payload.get("purpose") or "") != "forward":
            continue
        forwarded += 1
        credential = credential or (payload.get("credential") or "")
        tier = tier or (payload.get("tier") or "")
        status = status or (payload.get("status") or "")
    print("forward=%d" % forwarded)
    print("credential=%s" % credential)
    print("tier=%s" % tier)
    print("status=%s" % status)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST [ARGS...]: answer one query into a file, and fail the
# step when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST [ARGS...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  [ -f "$1" ] || return 0
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
  return 0
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# table_field TABLE KIND NAME COLUMN: one column of one row of a read document,
# keyed by its first field — the crafts, the config's resolved keys and the
# record's stages are all read this way. A document that was never read answers
# "" rather than failing the ERR trap: a step whose document is missing has its
# own row to say so.
table_field() { # table_field FILE KIND NAME COLUMN
  [ -f "$1" ] || return 0
  awk -F'\t' -v kind="$2" -v name="$3" -v col="$4" '$1 == kind && $2 == name { print $col; exit }' "$1" 2>/dev/null
  return 0
}

# record_field KEY: one key of the run's own record, "" when the record was
# never read.
record_field() { # record_field KEY
  [ -f "$SCRATCH/record.txt" ] || return 0
  field "$SCRATCH/record.txt" "$1"
  return 0
}

# bound_loop SECONDS PID: watch one background job to its bound and end it when
# the bound is reached. A hung cloud call would otherwise outlive the check that
# gave up on it, which is the one thing the bound exists to prevent — so the
# child of the helper below execs, and the pid watched here IS the command.
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
# check runs commands whose failing exit status is one of its own readings, and
# a trap inherited here would turn each of them into "an internal error".
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  bound_loop "$secs" "$!"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check628.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a gcloud or fugaro call to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 628 launches a real sandbox run, at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT}${GCP_PROJECT}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release whose craft ladder this check measures)"
}

# ---------------------------------------------------------------- the steps
# step_installation: the premise. The project comes from the checkout itself
# through `fugaro doctor --project <name> --all`, whose `project:` line the
# `--all` view alone prints (internal/cli/doctor.go): everything below is that
# installation's.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and every command below is run from it"
  fi
  local args=(doctor --all)
  [ -z "$PROJECT_ARG" ] || args=(doctor --project "$PROJECT_ARG" --all)
  say "reading the installation: fugaro ${args[*]}, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" "${args[@]}"
  local line
  line=$(grep -m1 '^project: ' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$line" ]; then
    unmeasured installation "fugaro ${args[*]} printed no 'project: <name> (GCP <project>, <region>)' line in ${SANDBOX} (exit ${BOUNDED_STATUS}): the project line is what tells this check which installation it is reading, and the --all view is the one that prints it. What it printed is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(printf '%s' "$line" | sed -n 's/^project: \([^ ]*\) .*/\1/p')
  INSTALLATION=$PROJECT
  GCP_PROJECT=$(printf '%s' "$line" | sed -n 's/^.*(GCP \([^,]*\),.*/\1/p')
  REGION_USED=$(printf '%s' "$line" | sed -n 's/^.*, \([^)]*\)).*/\1/p')
  if [ -z "$PROJECT" ] || [ -z "$GCP_PROJECT" ]; then
    unmeasured installation "the project line '${line}' is not the shape this check reads (project: <name> (GCP <project>, <region>)): what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$GCP_PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${GCP_PROJECT}: this check is never run there" ;;
  esac
  fact installation PASS "${SANDBOX} is a checkout of the installation ${PROJECT} (GCP ${GCP_PROJECT}, ${REGION_USED:-unknown}): the run below and every read of it are this installation's"
}

# step_layer: the project layer's craft model, read where it is set and with the
# layer that set it. The role slot below the craft is read in the same document:
# the acceptance says the craft key named the model and the slot was left alone,
# and only the source tells the two apart.
step_layer() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured layer "there is no installation whose layer could be read: the row above says why"
    unmeasured role-slot "there is no installation whose config could be read: the row above says why"
    return 0
  fi
  say "reading the resolved config: fugaro config show --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" config "$SANDBOX" "$FUGARO_BIN" config show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured layer "fugaro config show --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project layer's craft model. What it printed is in $SCRATCH/config.err"
    unmeasured role-slot "fugaro config show --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}), so the role slot could not be read either"
    return 0
  fi
  if ! read_json config "$SCRATCH/config.out" "$SCRATCH/config.txt"; then
    unmeasured layer "fugaro config show --json printed a document this check could not read: what it printed is in $SCRATCH/config.out"
    unmeasured role-slot "the config document could not be read, so the role slot could not be either"
    return 0
  fi
  local model model_from slot slot_from
  model=$(table_field "$SCRATCH/config.txt" value "agent.crafts.${CRAFT}.model" 3)
  model_from=$(table_field "$SCRATCH/config.txt" value "agent.crafts.${CRAFT}.model" 4)
  LAYER_MODEL=$model
  if [ -z "$model" ]; then
    fact layer FAIL "the resolved config names no model for agent.crafts.${CRAFT}.model in ${SANDBOX} (the document carries $(field "$SCRATCH/config.txt" count) resolved keys, and the craft's model key is not among them): the project layer's key is what this check's first clause is about. Write defaults.agent.crafts.${CRAFT}.model into the project layer (fugaro project layer, or the layer the installation reads) and run this check again"
  elif [ "$model_from" != project ]; then
    fact layer FAIL "agent.crafts.${CRAFT}.model resolves to ${model} with source '${model_from}': the acceptance's first clause is a PROJECT LAYER whose defaults.agent.crafts.${CRAFT}.model names the model, and a value this repository's own file wrote is a different reading. Read the sources back with 'fugaro config show --json' in ${SANDBOX}"
  else
    fact layer PASS "the project layer sets agent.crafts.${CRAFT}.model = ${model} (source project, read from fugaro config show --json in ${SANDBOX}): this run's coder stages resolve their model from the layer and not from the repository's file"
  fi
  slot=$(table_field "$SCRATCH/config.txt" value "agent.models.${ROLE}" 3)
  slot_from=$(table_field "$SCRATCH/config.txt" value "agent.models.${ROLE}" 4)
  if [ "$slot_from" = project ] && [ -n "$slot" ]; then
    fact role-slot FAIL "agent.models.${ROLE} resolves to ${slot} with source 'project': the project layer wrote the ROLE SLOT as well, so a run would pin this model whether or not the craft key is read — and the acceptance's last clause is that ${ROLE} was left alone. A layer that names the craft's model and the role it sits above cannot tell the two apart"
  elif [ -n "$slot" ]; then
    fact role-slot PASS "agent.models.${ROLE} resolves to ${slot} with source '${slot_from:-default}' and not the project layer: the craft's own key is what named this run's model, and the slot below it in the ladder was left alone"
  else
    fact role-slot PASS "agent.models.${ROLE} resolves to nothing (source '${slot_from:-default}') in ${SANDBOX}: the slot is unset, so the only key that could have named the coder's model is the craft's own — left alone, which is the acceptance's last clause"
  fi
}

# step_craft_ls: the craft's own answer — the resolution a run makes from the
# same layers, with where the model came from and the access that serves it.
step_craft_ls() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured craft-ls "there is no installation whose crafts could be listed: the row above says why"
    return 0
  fi
  say "reading the crafts: fugaro craft ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" crafts "$SANDBOX" "$FUGARO_BIN" craft ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured craft-ls "fugaro craft ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the crafts a run resolves are in that document. If this build has no craft command, this check is not for it. What it printed is in $SCRATCH/crafts.err"
    return 0
  fi
  if ! read_json crafts "$SCRATCH/crafts.out" "$SCRATCH/crafts.txt"; then
    unmeasured craft-ls "fugaro craft ls --json printed a document this check could not read: what it printed is in $SCRATCH/crafts.out"
    return 0
  fi
  local model model_from access access_from problem
  model=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 3)
  model_from=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 4)
  access=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 5)
  access_from=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 6)
  problem=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 9)
  CRAFT_ACCESS=$access
  # The notes below are built here rather than with ${VAR:+...}: an apostrophe
  # or a bracket inside the word of a parameter expansion is read as quoting by
  # the shell that parses it, and a check on the owner's machine may be any
  # bash.
  local access_note="" problem_note=""
  [ -z "$access_from" ] || access_note=", from ${access_from}"
  [ -z "$problem" ] || problem_note=", and reports '${problem}'"
  if [ -z "$model" ]; then
    fact craft-ls FAIL "fugaro craft ls --json names no model for the ${CRAFT} craft in ${SANDBOX} (the document carries $(field "$SCRATCH/crafts.txt" count) craft row(s)): a run of this repository would resolve the coder's model from the role slot and not from the craft, which is the half of the acceptance this row measures"
  elif [ "$model_from" != project ]; then
    fact craft-ls FAIL "fugaro craft ls --json resolves the ${CRAFT} craft's model to ${model} from '${model_from}': the layer's key is what the acceptance names, and this craft's model came from somewhere else. Read it back with 'fugaro craft ls --json' in ${SANDBOX}"
  elif [ -n "$LAYER_MODEL" ] && [ "$model" != "$LAYER_MODEL" ]; then
    fact craft-ls FAIL "fugaro craft ls --json resolves the ${CRAFT} craft's model to ${model} while the resolved config's agent.crafts.${CRAFT}.model is ${LAYER_MODEL}: the two surfaces a run and this check read disagree about the model, so nothing below can be read as one model's"
  else
    fact craft-ls PASS "fugaro craft ls --json resolves the ${CRAFT} craft to model ${model} from '${model_from}' (access ${access:-none}${access_note}): the craft command a run's own resolution is built on names the layer's model${problem_note}"
  fi
}

# step_allow_list: the model is one the project's allow-list admits. The node's
# own allowed_models is the published answer where it names one; where it names
# none the ceiling is the job's own environment (internal/projectpolicy refuses
# a non-nil empty list for exactly that reason), and the reading then is the
# craft's own `model_servable`, which the same resolution publishes.
step_allow_list() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured allow-list "there is no installation whose policy could be read: the row above says why"
    return 0
  fi
  say "reading the published node: fugaro project policy show --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" node "$SANDBOX" "$FUGARO_BIN" project policy show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured allow-list "fugaro project policy show --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the project's published allow-list is in that node. What it printed is in $SCRATCH/node.err"
    return 0
  fi
  if ! read_json node "$SCRATCH/node.out" "$SCRATCH/node.txt"; then
    unmeasured allow-list "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/node.out"
    return 0
  fi
  local allowed servable problem problem_note=""
  allowed=$(field "$SCRATCH/node.txt" allowed_models)
  servable=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 8)
  problem=$(table_field "$SCRATCH/crafts.txt" craft "$CRAFT" 9)
  [ -z "$problem" ] || problem_note=" (model_problem: ${problem})"
  if [ -z "$LAYER_MODEL" ]; then
    unmeasured allow-list "there is no model to look for in the allow-list: the craft row above says why"
    return 0
  fi
  if [ -z "$allowed" ]; then
    if [ "$servable" = true ]; then
      fact allow-list PASS "the published node names no allowed_models, so the ceiling is the job's own environment (internal/projectpolicy refuses a non-nil empty list for exactly that reason) — and fugaro craft ls --json reports the ${CRAFT} craft's model ${LAYER_MODEL} as model_servable: the project's own resolution serves the model the layer names, which is the acceptance's first clause"
    else
      fact allow-list FAIL "the published node names no allowed_models and fugaro craft ls --json reports the ${CRAFT} craft's model ${LAYER_MODEL} as NOT servable${problem_note}: a run of this repository would be refused the model the project layer names, and the acceptance's first clause is a model the project admits"
    fi
    return 0
  fi
  case ";$allowed;" in
  *";$LAYER_MODEL;"*)
    fact allow-list PASS "${LAYER_MODEL} is in the published node's allowed_models (${allowed}), read from fugaro project policy show --json in ${SANDBOX}: the model the layer names is one this project admits, which is the acceptance's first clause"
    ;;
  *)
    fact allow-list FAIL "the published node's allowed_models is ${allowed} and the model the project layer names is ${LAYER_MODEL}: the layer names a model this project's own allow-list does not admit, so a run of it is refused before its first call. Publish an allow-list carrying that model (or name one it admits) and run this check again"
    ;;
  esac
}

# step_run: one real sandbox run, launched from the checkout. `--json` names the
# run this check then follows.
step_run() {
  if [ -z "$INSTALLATION" ]; then
    unmeasured run-launched "there is no installation to launch a run in: the row above says why"
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
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the run this check exists to read never started, so no part of the acceptance about a run was measured. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt" 2>/dev/null; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch names the run this check follows. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout: the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end. A check that read a record while a run was
# still going would read the previous run's.
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
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state the record below is written in"
}

# step_result_json: the record, read from the runs bucket at the run's own key,
# checked against the run this check launched, and read for the execution (the
# job name is in that path) and the cost figures.
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
    unmeasured result-json "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=$(printf '%s' "${RUN_REF:-none}")), so there was no key to read: the record of run ${RUN_ID} lives under the repository's slug"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${GCP_PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/${key}" --project="${GCP_PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${GCP_PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where its stages and their models are written. What gcloud printed is in $SCRATCH/record.err"
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
  EXECUTION=$(field "$SCRATCH/record.txt" execution)
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), stage=$(field "$SCRATCH/record.txt" stage), outcome=$(field "$SCRATCH/record.txt" outcome), cost.model_usd=$(field "$SCRATCH/record.txt" model_usd), executions=$(field "$SCRATCH/record.txt" stages) stage row(s)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_stage_model: the acceptance's second clause — the record's own model for
# every stage the coder craft serves.
step_stage_model() {
  if [ "$SETTLED" != yes ] || [ -z "$RUN_ID" ]; then
    unmeasured stage-model "there is no terminal run's record to read a stage model from: the rows above say why"
    return 0
  fi
  if [ -z "$LAYER_MODEL" ]; then
    unmeasured stage-model "there is no model to compare the record's stages against: the layer row above says why"
    return 0
  fi
  if [ ! -f "$SCRATCH/record.txt" ]; then
    unmeasured stage-model "the record of ${RUN_ID} was not read (its own row above says why), so there is no stage row to compare ${LAYER_MODEL} against"
    return 0
  fi
  # A stage row is `stage<TAB>name<TAB>model<TAB>credential<TAB>served_by<TAB>model_usd`.
  local seen="" bad=0 st_name st_model other=""
  while IFS=$'\t' read -r kind st_name st_model _rest; do
    [ "$kind" = stage ] || continue
    case "$st_name" in
    plan | implement | fix) ;;
    *) continue ;;
    esac
    seen="${seen:+$seen, }${st_name}"
    if [ "$st_model" != "$LAYER_MODEL" ]; then
      bad=$((bad + 1))
      other="${other:+$other, }${st_name}=${st_model:-none}"
    fi
  done <"$SCRATCH/record.txt"
  if [ -z "$seen" ]; then
    fact stage-model FAIL "the record of ${RUN_ID} carries no row for plan, implement or fix — the stages internal/runner's stageCraft hands the coder craft (it carries $(field "$SCRATCH/record.txt" stages) stage row(s)) — so the run never reached a coder stage and the acceptance's second clause was not measured by it. A run whose task needs no work may stop before implement; give CHECK628_TASK a task the coder really answers and run this check again"
    return 0
  fi
  if [ "$bad" -gt 0 ]; then
    fact stage-model FAIL "the record of ${RUN_ID} records ${other} for the coder craft's stage(s) while the project layer names ${LAYER_MODEL} (stages seen: ${seen}): the model a run actually ran is what the acceptance's second clause is about, and this run did not run the layer's model. Compare it with 'fugaro craft ls --json' in ${SANDBOX}"
    return 0
  fi
  fact stage-model PASS "every coder stage of ${RUN_ID} records model ${LAYER_MODEL} in its own record (${seen}): the StageTiming.Model the acceptance names is the project layer's model, read from the run rather than from the pin"
}

# step_credential: the credential that served the run, against the access the
# craft names. This build's record carries no craft_credentials (MC8's record
# half), so the reading is the surfaces it does carry — the record's
# stage_accounts, its per-stage credential/served_by, and the credentials
# service's own audit line — and the row says so.
step_credential() {
  local account="" served="" audit_cred="" forward=""
  if [ -z "$RUN_ID" ]; then
    unmeasured credential "there is no run whose credential could be read: the rows above say why"
    return 0
  fi
  if [ ! -f "$SCRATCH/record.txt" ]; then
    unmeasured credential "the record of ${RUN_ID} was not read (its own row above says why), so there is no stage row and no execution to read this run's credential from: the record is where the accounts that served it are written, and a run that had not ended has none"
    return 0
  fi
  # An account row is `account<TAB>stage<TAB>account-name`; a stage row is
  # `stage<TAB>name<TAB>model<TAB>credential<TAB>served_by<TAB>model_usd`.
  if [ "$SETTLED" = yes ] && [ -f "$SCRATCH/record.txt" ]; then
    account=$(awk -F'\t' '$1 == "account" { printf "%s=%s ", $2, $3 }' "$SCRATCH/record.txt" 2>/dev/null || true)
    served=$(awk -F'\t' '$1 == "stage" && ($4 != "" || $5 != "") { printf "%s=%s/%s ", $2, ($4 ? $4 : "-"), ($5 ? $5 : "-") }' "$SCRATCH/record.txt" 2>/dev/null || true)
  fi
  if [ -n "$EXECUTION" ]; then
    local filter="jsonPayload.message=\"credsvc: audit\" AND jsonPayload.run=\"$RUN_ID\""
    say "reading the service's own audit line for ${RUN_ID}: gcloud logging read (credsvc: audit)"
    run_bounded "$CLI_TIMEOUT" audit "$SANDBOX" gcloud logging read --resource-names="projects/$GCP_PROJECT" \
      --limit=2000 --freshness=30d --format=json "$filter"
    if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] &&
      read_json audit "$SCRATCH/audit.out" "$SCRATCH/audit.txt" "$RUN_ID"; then
      forward=$(field "$SCRATCH/audit.txt" forward)
      audit_cred=$(field "$SCRATCH/audit.txt" credential)
    fi
  fi
  local named="$audit_cred"
  if [ -z "$named" ] && [ -f "$SCRATCH/record.txt" ]; then
    named=$(awk -F'\t' '$1 == "account" { print $3; exit }' "$SCRATCH/record.txt" 2>/dev/null || true)
  fi
  if [ -z "$CRAFT_ACCESS" ]; then
    unmeasured credential "the ${CRAFT} craft's access could not be read, so there is nothing to compare the run's credential against: the craft row above says why"
    return 0
  fi
  if [ -z "$named" ]; then
    fact credential FAIL "neither the record of ${RUN_ID} (stage_accounts: ${account:-none}) nor the credentials service's audit lines name a credential for this run, while the ${CRAFT} craft's access is ${CRAFT_ACCESS}: nothing here says what served the run. Note that this build's record carries no craft_credentials (MC8's record half has not landed), so the reading is the record's stage rows and the service's audit line — read them yourself with 'fugaro watch ${RUN_ID}' and this check's own commands, which are in $SCRATCH"
    return 0
  fi
  local members=""
  if [ -f "$SCRATCH/node.txt" ]; then
    members=$(table_field "$SCRATCH/node.txt" pool "$CRAFT_ACCESS" 4)
  fi
  # Built rather than written inline: an apostrophe or a bracket inside the word
  # of a `${VAR:+...}` is read as quoting by the shell parsing it, so the notes
  # are plain strings the block can print whatever bash the owner runs.
  local audit_note="" served_note="" forward_note=""
  [ -z "$audit_cred" ] || audit_note="the credentials service's own audit line, and from "
  [ -z "$served" ] || served_note="; per-stage credential/served_by: ${served}"
  [ -z "$forward" ] || forward_note=" (${forward} forwarded call(s))"
  [ "$forward" = 0 ] && forward_note=""
  if [ "$named" = "$CRAFT_ACCESS" ]; then
    fact credential PASS "the run ${RUN_ID} was served by ${named} — the access the ${CRAFT} craft names — read from ${audit_note}the record's own stage rows (stage_accounts: ${account:-none}${served_note})"
  elif [ -n "$members" ]; then
    case ",${members}," in
    *",$named,"*)
      fact credential PASS "the run ${RUN_ID} was served by ${named}, which is a member of the pool ${CRAFT_ACCESS} (members ${members}) the ${CRAFT} craft's access names — read from the credentials service's own audit line for this run${forward_note} and the record's own stage rows (stage_accounts: ${account:-none})"
      ;;
    *)
      fact credential FAIL "the run ${RUN_ID} was served by ${named}, which is NOT a member of ${CRAFT_ACCESS} (members ${members}) — the pool the ${CRAFT} craft's access names: the acceptance's 'the same credential' is about the craft's access and the credential that answered, and these two disagree"
      ;;
    esac
  else
    fact credential FAIL "the run ${RUN_ID} was served by ${named} while the ${CRAFT} craft's access is ${CRAFT_ACCESS}: the acceptance's 'the same credential' is about the craft's access and the credential that answered, and these two disagree. The published node names no pool called ${CRAFT_ACCESS} to read a member list from (craft_credentials: $(field "$SCRATCH/node.txt" credentials), craft_pools: $(field "$SCRATCH/node.txt" pools)). Note that this build's record carries no craft_credentials (MC8's record half has not landed), so the row reads the record's stage rows and the service's audit line instead"
  fi
}

# step_not_pinned: the gateway's own lines. `budget: request refused` is the
# line a request the gateway's pin rules turned away writes
# (internal/gateway/forward.go, checkRequest), and a model "not pinned for" a
# stage is the acceptance's refusal — so this row is the count of them.
step_not_pinned() {
  if [ -z "$EXECUTION" ]; then
    unmeasured not-pinned "the run's record names no execution this check could take a log read from (execution=${EXECUTION:-none}), so the gateway's own refusals for it cannot be read"
    return 0
  fi
  local region job name
  region=$(printf '%s' "$EXECUTION" | cut -d/ -f4)
  job=$(printf '%s' "$EXECUTION" | cut -d/ -f6)
  name=$(printf '%s' "$EXECUTION" | cut -d/ -f8)
  if [ -z "$region" ] || [ -z "$job" ] || [ -z "$name" ]; then
    unmeasured not-pinned "the run's execution '$EXECUTION' is not projects/<project>/locations/<region>/jobs/<job>/executions/<name>, so there is no log to read"
    return 0
  fi
  local scope=(--resource-names="projects/$GCP_PROJECT")
  [ -z "$LOG_VIEW" ] || scope=(--resource-names="$LOG_VIEW")
  local filter
  filter="resource.type=\"cloud_run_job\" AND resource.labels.location=\"$region\" AND resource.labels.job_name=\"$job\" AND labels.\"run.googleapis.com/execution_name\"=\"$name\" AND (jsonPayload.message=\"budget: request refused\" OR jsonPayload.message=\"model call\")"
  say "reading the run's own gateway lines: gcloud logging read (execution ${name}, last 30 days)"
  run_bounded "$CLI_TIMEOUT" calls "$SANDBOX" gcloud logging read "${scope[@]}" --limit=5000 --freshness=30d --format=json "$filter"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured not-pinned "gcloud logging read of execution ${name} did not answer (exit ${BOUNDED_STATUS}): the refusals this row is about are in that log, and no access, a view that is not there and the wrong project are the three usual reasons. What gcloud printed is in $SCRATCH/calls.err"
    return 0
  fi
  if ! read_json calls "$SCRATCH/calls.out" "$SCRATCH/calls.txt"; then
    unmeasured not-pinned "gcloud logging read answered something this check cannot read as a log: what gcloud printed is in $SCRATCH/calls.out"
    return 0
  fi
  local refusals violations calls
  refusals=$(field "$SCRATCH/calls.txt" refusals)
  violations=$(field "$SCRATCH/calls.txt" violations)
  calls=$(field "$SCRATCH/calls.txt" calls)
  if [ "${calls:-0}" = 0 ] && [ "${refusals:-0}" = 0 ]; then
    unmeasured not-pinned "gcloud logging read of execution ${name} answered no gateway line at all: the read covers the last 30 days (--freshness=30d) and the project's own default log bucket, so an execution older than that window, or one whose entries live in the installation's own view, answers an empty log for a run that really made calls. CHECK628_LOG_VIEW is the scope to read it again with"
    return 0
  fi
  case "$violations" in
  *"is not pinned for"*)
    fact not-pinned FAIL "the gateway refused ${refusals} request(s) of ${RUN_ID} and its own violation line reads '${violations}': the acceptance's clause is that the gateway raises NO 'not pinned for the stage' refusal — a model the gateway's pin does not carry is a model whose calls never reached a provider at all, so whatever the record says above was not served. Read the pin back with 'fugaro craft ls --json' and 'fugaro config show --json' in ${SANDBOX}"
    ;;
  *)
    if [ "${refusals:-0}" != 0 ]; then
      fact not-pinned PASS "the gateway answered ${calls} model call line(s) for ${RUN_ID} and refused ${refusals} request(s), none of them for a model 'not pinned for' a stage (the violations read: ${violations:-none}): the refusal the acceptance names is not among them, and this row reports the ones that are"
    else
      fact not-pinned PASS "the gateway's lines for ${RUN_ID} carry ${calls} model call line(s) and no 'budget: request refused' line at all, so no model of this run was refused as not pinned for a stage: the coder's model reached the upstream, which is the acceptance's clause about the gateway"
    fi
    ;;
  esac
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="this check wrote no config, published no policy and changed no secret: its only write is the sandbox run ${RUN_ID:-none} and the branch and pull request that run left (outcome=$(record_field outcome), pr=$(record_field pr)) — close that pull request and delete its branch yourself once you have read the run. The project layer's craft model is read, never written: this check leaves it exactly as it found it"
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
  # A row the check could not measure is said every time, even when a finding
  # ended the run as well: the two are different pieces of news, and the one
  # that says "this was not measured" is what tells the owner the run has not
  # finished answering yet. A finding outranks a gap in the exit status, so a
  # run that both found something and left a row unmeasured exits 1.
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

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 628 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --project <name> --all in ${SANDBOX} — the project line: which installation this is"
  say "  [read] fugaro config show --json in ${SANDBOX} — agent.crafts.${CRAFT}.model, its source, and agent.models.${ROLE} left alone"
  say "  [read] fugaro craft ls --json in ${SANDBOX} — the craft's model, where it came from, and the access that serves it"
  say "  [read] fugaro project policy show --json in ${SANDBOX} — the published allow-list"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<gcp-project>/runs/<slug>/<run>/result.json — the stages, their models and the accounts that served them"
  say "  [read] gcloud logging read (budget: request refused and model call for the execution) — no 'not pinned for' refusal"
  say "  [read] gcloud logging read (credsvc: audit for the run) — the credential NAME the service served, never a value"
  for key in installation layer role-slot craft-ls allow-list run-launched terminal result-json stage-model credential not-pinned; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no run, no record read and no log read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation layer
  role-slot craft-ls allow-list run-launched terminal result-json stage-model
  credential not-pinned restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 628 RESULT\n' >&3
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
  printf 'CHECK 628 RESULT END (exit %s)\n' "$status" >&3
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
PROJECT_CLI=""
TASK_CLI=""
RECIPE_CLI=""
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
  --project)
    [ $# -ge 2 ] || die "--project needs the installation's own name: the one 'fugaro doctor --project <name> --all' reads"
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

SANDBOX=${CHECK628_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 628 needs --sandbox: the checkout of a repository of the sandbox project. The run is launched from it. Usage: bash scripts/live-checks/check628.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
PROJECT_ARG=${CHECK628_PROJECT:-${PROJECT_CLI:-}}
# The name the owner asked for is what the belong guard reads before any command
# runs; step_installation replaces it with the installation the doctor names.
PROJECT=$PROJECT_ARG
CRAFT=${CHECK628_CRAFT:-coder}
ROLE=${CHECK628_ROLE:-coder}
TASK=${CHECK628_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK628_RECIPE:-${RECIPE_CLI:-}}
DRY_RUN=${CHECK628_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK628_KEEP_SCRATCH:-}
LOG_VIEW=${CHECK628_LOG_VIEW:-}
REGION=${CHECK628_REGION:-us-east5}
RUN_TIMEOUT=${CHECK628_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK628_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK628_CLI_TIMEOUT_SECONDS:-180}
DOCTOR_TIMEOUT=${CHECK628_DOCTOR_TIMEOUT_SECONDS:-300}
LAUNCH_TIMEOUT=${CHECK628_LAUNCH_TIMEOUT_SECONDS:-600}
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
  [ -n "$FUGARO_BIN" ] || die "Check 628 needs 'fugaro' on PATH: the check reads the installation you have, and it is not there"
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
  step_layer
  step_craft_ls
  step_allow_list
  step_run
  step_terminal
  step_result_json
  step_stage_model
  step_credential
  step_not_pinned
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
