#!/usr/bin/env bash
# Check 781 — a non-Claude provider model, served through the credentials
# service end to end: the key read from the installation's one bundle, the call
# charged the provider's own reported cost.
#
# Plan task KS17 writes this check; its subject is the reservation
# docs/keys-through-service/implementation-plan.md's §8 gives number 781
# ("After release A: a non-Claude provider model served through the service end
# to end, charged the provider's reported cost, with the key read from the
# bundle"), and the reconciliation's R6, F13 and D3005 are what it answers.
#
# What it measures:
#
#   * the installation the sandbox checkout belongs to, from `fugaro doctor
#     --json` in that checkout — the project the run and every read belong to —
#     and the installation's ONE bundle container, found in `gcloud secrets
#     list` by the name internal/backend/gcp derives for it
#     (`fugaro-project-<project>-creds-bundle-<hash>`; the readable part is
#     truncated when the name is long, so the reader matches the logical name
#     and the hash and never a prefix a rename would move);
#   * one real sandbox run, launched from the checkout and followed with
#     `fugaro runs ls --repo <the repository the launch named> --json` until it
#     is terminal — a record read while a run is still working is whatever was
#     there before it;
#   * its `result.json`, read from the runs bucket at the key the listing's own
#     `run` ref gives, checked against the run id this check launched, and read
#     for the execution the run ran as (the job name is in that path, and it is
#     the job whose environment the next row reads) and for `cost.reported_usd`,
#     `cost.route_by` and `cost.model_usd`;
#   * **the job's own environment**: `gcloud run jobs describe` of the job the
#     run's execution names, read for the model credential containers it is fed
#     from. No provider key may be mounted at all — the credential families the
#     config reserves (`credential-<name>-key`, `anthropic-api-key`,
#     `claude-oauth-token`, a `FUGARO_PROVIDER_KEY_*` variable) are searched for
#     by their own shapes — while the credentials service's URL is in the
#     environment, which is the pair D3005 is about: the key is not in the job,
#     so the call was served;
#   * **the gateway's own `model call` lines for that execution** (`gcloud
#     logging read`, filtered by the execution's own labels), read for the
#     calls that went to a provider: `route` is emitted only for a call that
#     went to one (internal/gateway/forward.go, internal/gateway/log.go), so
#     the row is the calls that carry it, their `serving_model`, their
#     `priced_as` and their `reported_micros` — the provider's own figure for
#     the call, charged;
#   * **the service's own audit line for the run** (`credsvc: audit`,
#     internal/credsvc/audit.go): a line whose `run` is this run, whose
#     `purpose` is `forward` and whose `status` is `ok`, carrying the NAME of
#     the credential it served and its tier and never a value — the bundle is
#     what was read, and this line is the service saying so;
#   * nothing else. No credential value is read, and none can reach the block:
#     the rows carry names, refs, counts, model IDs and money.
#
# Usage:      bash scripts/live-checks/check781.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check781.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check781.sh --help
# Needs:      bash, mktemp, date, python3 (every `--json` document and every log
#             read is read with it), gcloud (authenticated as YOU, able to read
#             Cloud Logging, `run jobs` and the runs bucket) and fugaro (the
#             installed release).
# Where:      your own machine and the sandbox project and repository only, at
#             your own terminal: a coding agent's session is refused by name, as
#             is a project or region naming belong.
# Creates:    one sandbox run — a real run, with its branch and the pull request
#             it opens if its agent commits. Nothing else: this check writes no
#             config, publishes no policy and changes no secret. On disk: one
#             scratch directory, removed at the end.
# Costs:      one sandbox run and what its task costs, all of it model spend
#             (every call of it is a provider call, which is the subject of the
#             check), plus the wall clock of waiting for it.
# Runs:       by YOU. No agent runs any step of it, and none has: what it
#             measures is a live installation. Its own test (check781_test.sh)
#             drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's pull request and branch are NOT
#             closed by this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 781 RESULT,
#             then CHECK 781 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a read did not answer, or the run never ended. A key the run
#             never reached reads `FAIL — not reached`.
#
# Knobs (all CHECK781_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), TASK and RECIPE (what the run is
# launched with), LOG_VIEW (a Cloud Logging view to scope the reads to, when the
# installation keeps its logs in one), RUN_TIMEOUT_SECONDS (3600),
# POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS (600) and
# REGION (us-east5).
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
  command -v bash >/dev/null 2>&1 || die "Check 781 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 781 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk781-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REGION_USED=""
BUNDLE_ID=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
EXECUTION=""
RUN_JOB=""
SETTLED=no
PICKED_RUN=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk781.XXXXXXXX") || die "Check 781 cannot make a scratch directory"
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
    say "CHECK781_KEEP_SCRATCH is set: $SCRATCH is kept"
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
#
# The credential families the reader looks for are the config's own shapes
# (internal/config/secretnames.go): a KEY credential's container is
# `credential-<name>-key`, the harness OAuth tokens are `claude-oauth-token`
# and its namespace, the paid Anthropic key is `anthropic-api-key`, and a
# provider key handed to a job as its own variable is `FUGARO_PROVIDER_KEY_*`.
# A derived Secret Manager ID carries 16 hex of a hash as its tail
# (internal/backend/gcp/names.go: secretHashHex), which is stripped before the
# logical name is looked for — the readable part is truncated when the name is
# long, so a match on a prefix would answer "no mount" for a job that mounts
# one, and a match on the tail is what the derivation actually guarantees.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import re
import sys

HASH_RE = re.compile(r"-[0-9a-f]{16}$")
# The credential families a job may be fed a model key from, as the logical
# names the derived IDs end with. The three are one rule — no model key is
# mounted anywhere once the service serves them — and they are searched for as
# one.
KEY_FAMILIES = ("anthropic-api-key", "claude-oauth-token")
KEY_VAR_PREFIX = "FUGARO_PROVIDER_KEY_"
CREDENTIAL_KEY_RE = re.compile(r"(^|-)credential-[a-z0-9][a-z0-9-]*-key$")


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def epoch(text):
    if not text:
        return ""
    stamp = text.strip().replace("Z", "+00:00")
    try:
        return "%d" % datetime.datetime.fromisoformat(stamp).timestamp()
    except ValueError:
        return ""


def epoch_of(text):
    if not isinstance(text, str) or not text.strip():
        return None
    stamp = text.strip().replace("Z", "+00:00")
    try:
        when = datetime.datetime.fromisoformat(stamp)
    except ValueError:
        return None
    if when.tzinfo is None:
        when = when.replace(tzinfo=datetime.timezone.utc)
    return when


def spec_of(doc):
    """The containers of a job or of one execution of it: the two live at
    different depths, so the known paths are tried and the first containers list
    wins — a reading that pinned one of them would answer for the other."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def containers_of(doc):
    spec = spec_of(doc)
    return (spec.get("containers") or []) if isinstance(spec, dict) else []


def env_of(spec):
    out = {}
    for entry in (spec.get("env") or []) if isinstance(spec, dict) else []:
        if isinstance(entry, dict) and entry.get("name"):
            out[entry["name"]] = entry
    return out


def secret_of(entry):
    src = entry.get("valueSource") or {}
    ref = ((src.get("secretKeyRef") or {}).get("secret")
           or (src.get("secretKeyRef") or {}).get("name") or "")
    return ref.split(":")[-1].split("/")[-1]


def key_mount(base):
    """Whether a mounted secret ID is a model key of one of the reserved
    families. The ID a deployment writes is the DERIVED one, whose readable
    part is truncated and whose tail is the hash, so the hash is stripped and
    the logical name is looked for at the END of what is left."""
    stem = HASH_RE.sub("", base)
    if CREDENTIAL_KEY_RE.search(stem):
        return True
    return any(stem == name or stem.endswith("-" + name) or ("-" + name + "-") in stem
               for name in KEY_FAMILIES)


def key_mounts_of(containers):
    out = []
    for spec in containers:
        for name, entry in env_of(spec).items():
            base = secret_of(entry)
            if base and key_mount(base):
                if base not in out:
                    out.append(base)
            elif name.startswith(KEY_VAR_PREFIX) and (entry.get("value") or ""):
                if name not in out:
                    out.append(name)
    return out


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("gcp_project=%s" % (project.get("gcp_project") or ""))
    print("region=%s" % (project.get("region") or ""))
    print("name=%s" % (project.get("name") or ""))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
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
    print("model_basis=%s" % (cost.get("model_basis") or ""))
    print("model_source=%s" % (cost.get("model_source") or ""))
    print("reported_usd=%s" % (cost.get("reported_usd", 0)))
    print("unreported_calls=%s" % (cost.get("unreported_calls", 0)))
    print("route_by=%s" % ";".join(sorted((cost.get("route_by") or {}).keys())))
    print("reported_by=%s" % ";".join(sorted((cost.get("reported_by") or {}).keys())))
elif what == "secrets":
    rows = doc if isinstance(doc, list) else []
    print("count=%d" % len(rows))
    for row in rows:
        name = row.get("name") or ""
        short = name.split("/")[-1]
        print("secret\t%s" % short)
elif what == "job":
    containers = containers_of(doc)
    mounts = key_mounts_of(containers)
    print("name=%s" % (((doc.get("metadata") or {}).get("name")) or ""))
    print("keymounts=%s" % (",".join(mounts) if mounts else "none"))
    url = "absent"
    for spec in containers:
        if "FUGARO_CREDENTIALS_SERVICE_URL" in env_of(spec):
            url = "present"
    print("url=%s" % url)
elif what == "calls":
    rows = doc if isinstance(doc, list) else []
    routed = 0
    reported = 0
    served = []
    priced = []
    total_charged = 0
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        payload = entry.get("jsonPayload") or {}
        if not payload:
            continue
        route = payload.get("route") or ""
        if not route:
            continue
        routed += 1
        model = payload.get("serving_model") or payload.get("model") or ""
        if model and model not in served:
            served.append(model)
        if payload.get("priced_as") and payload["priced_as"] not in priced:
            priced.append(payload["priced_as"])
        if float(payload.get("reported_micros") or 0) > 0:
            reported += 1
        total_charged += int(payload.get("charged_micros") or 0)
    print("count=%d" % len(rows))
    print("routed=%d" % routed)
    print("reported=%d" % reported)
    print("served=%s" % ";".join(served))
    print("priced=%s" % ";".join(priced))
    print("charged_micros_route=%d" % total_charged)
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

# read_json QUERY FILE DEST: answer one query into a file, and fail the step
# when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST [ARGS...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check781.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a gcloud or fugaro call to the terminal a second time"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 781 launches a real sandbox run, at your own terminal, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the release whose provider path this check measures)"
}

# ---------------------------------------------------------------- the steps
# step_installation: the premise. The project comes from the checkout itself,
# so everything below is that installation's. The bundle is looked for by the
# name internal/backend/gcp derives, because the hash cannot be computed here.
step_installation() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and every command below is run from it"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured installation "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): there is no installation whose run this check could launch. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  REGION_USED=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$PROJECT" ]; then
    unmeasured installation "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact installation PASS "${SANDBOX} is a Fugaro repository's checkout of the installation ${PROJECT} (region ${REGION_USED:-unknown}): the run below and every read of it are this installation's"
}

# step_bundle: the ONE container the credentials service reads. The row is a
# premise and not a finding: it says the bundle is there, which is what the
# audit row below is about.
step_bundle() {
  if [ -z "$PROJECT" ]; then
    unmeasured bundle "there is no installation whose bundle could be found: the row above says why"
    return 0
  fi
  say "looking for the installation's bundle container: gcloud secrets list --project ${PROJECT}"
  run_bounded "$CLI_TIMEOUT" secrets "$SANDBOX" gcloud secrets list --project="${PROJECT}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured bundle "gcloud secrets list did not answer for ${PROJECT} (exit ${BOUNDED_STATUS}), so this check cannot say the installation has a bundle at all. What it printed is in $SCRATCH/secrets.err"
    return 0
  fi
  if ! read_json secrets "$SCRATCH/secrets.out" "$SCRATCH/secrets.txt"; then
    unmeasured bundle "gcloud secrets list printed a document this check could not read: what it printed is in $SCRATCH/secrets.out"
    return 0
  fi
  BUNDLE_ID=$(awk -F'\t' '$1 == "secret" && $2 ~ /creds-bundle-[0-9a-f]+$/ { print $2; exit }' "$SCRATCH/secrets.txt" 2>/dev/null)
  if [ -z "$BUNDLE_ID" ]; then
    fact bundle FAIL "the installation ${PROJECT} holds no secret whose derived name ends in creds-bundle-<hash> ($(field "$SCRATCH/secrets.txt" count) secrets read, none of them it): the credentials service reads every model credential from that one container, so a call it serves has no bundle to come from. Run \`fugaro update\` and read what it says about the bundle secret, then run this check again"
    return 0
  fi
  fact bundle PASS "the installation's bundle container is ${BUNDLE_ID} (found in gcloud secrets list by the name internal/backend/gcp derives, internal/backend/gcp.CredsBundleSecretID): it is the one Secret Manager container the credentials service reads, and the audit row below is what says a call was served from it"
}

# step_run: one real sandbox run, launched from the checkout. `--json` names
# the run this check then follows.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no installation to launch a run in: the row above says why"
    return 0
  fi
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the run this check exists to read never started, so nothing about the provider path was measured. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch names the run this check follows. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)): the run is the check's own, and the rows below follow this one and no other"
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
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the execution, the outcome and the cost are written. What gcloud printed is in $SCRATCH/record.err"
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
  RUN_JOB=$(printf '%s' "$EXECUTION" | cut -d/ -f6)
  PICKED_RUN=$RUN_ID
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), stage=$(field "$SCRATCH/record.txt" stage), outcome=$(field "$SCRATCH/record.txt" outcome), cost.model_usd=$(field "$SCRATCH/record.txt" model_usd), cost.reported_usd=$(field "$SCRATCH/record.txt" reported_usd), cost.route_by=$(field "$SCRATCH/record.txt" route_by)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_job_env: the job the run ran as, read for the model keys it is fed from.
# This is the row that says WHERE the key was not: with the service serving
# them, no job mounts a provider key at all, and the credentials service's URL
# is in its environment instead.
step_job_env() {
  if [ -z "$RUN_JOB" ]; then
    unmeasured job-env "the run's record names no execution this check could take a job name from (execution=${EXECUTION:-none}), so there is no job whose environment could be read"
    return 0
  fi
  say "reading the job the run ran as: gcloud run jobs describe ${RUN_JOB} --region ${REGION_USED:-us-east5}"
  run_bounded "$CLI_TIMEOUT" job "$SANDBOX" gcloud run jobs describe "$RUN_JOB" \
    --region="${REGION_USED:-us-east5}" --project="${PROJECT}" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured job-env "gcloud run jobs describe ${RUN_JOB} did not answer (exit ${BOUNDED_STATUS}): this check cannot read the job's environment, which is where a provider key would have to be mounted. What gcloud printed is in $SCRATCH/job.err"
    return 0
  fi
  if ! read_json job "$SCRATCH/job.out" "$SCRATCH/job.txt"; then
    unmeasured job-env "gcloud run jobs describe printed a document this check could not read: what it printed is in $SCRATCH/job.out"
    return 0
  fi
  local mounts url
  mounts=$(field "$SCRATCH/job.txt" keymounts)
  url=$(field "$SCRATCH/job.txt" url)
  if [ "$mounts" != none ]; then
    fact job-env FAIL "the job ${RUN_JOB} mounts a model key (${mounts}): every model credential is served by the credentials service, so a job that still holds one is a job whose calls did not need the service at all — and the rows below then say nothing about the bundle. Read the mounts back with 'gcloud run jobs describe ${RUN_JOB} --region ${REGION_USED:-us-east5} --project ${PROJECT}' and run \`fugaro update\` in the checkout"
    return 0
  fi
  if [ "$url" != present ]; then
    fact job-env FAIL "the job ${RUN_JOB} mounts no model key, and carries no credentials service URL either (FUGARO_CREDENTIALS_SERVICE_URL absent): a job with neither cannot make a model call at all, so the run above cannot have made one — the release this check is written for hands every job the service's URL"
    return 0
  fi
  fact job-env PASS "the job ${RUN_JOB} mounts no model key at all (no credential-<name>-key container, no anthropic-api-key, no claude-oauth-token, no FUGARO_PROVIDER_KEY_* variable) and carries the credentials service's URL: the key it called with is not in the job, which is the half of D3005 this row measures"
}

# step_calls: the gateway's own lines for the execution. `route` is emitted
# only for a call that went to a provider — "" is the Anthropic upstream
# (internal/gateway/log.go) — so the row is the calls that carry it, what
# served them and the provider's own figure for them.
step_calls() {
  if [ -z "$EXECUTION" ]; then
    unmeasured calls "there is no execution whose logs could be read: the record row above says why"
    return 0
  fi
  local region job name
  region=$(printf '%s' "$EXECUTION" | cut -d/ -f4)
  job=$(printf '%s' "$EXECUTION" | cut -d/ -f6)
  name=$(printf '%s' "$EXECUTION" | cut -d/ -f8)
  if [ -z "$region" ] || [ -z "$job" ] || [ -z "$name" ]; then
    unmeasured calls "the run's execution '$EXECUTION' is not projects/<project>/locations/<region>/jobs/<job>/executions/<name>, so there is no log to read: a run from before the launcher recorded an execution has none"
    return 0
  fi
  local scope=(--resource-names="projects/$PROJECT")
  [ -z "$LOG_VIEW" ] || scope=(--resource-names="$LOG_VIEW")
  local filter
  filter="resource.type=\"cloud_run_job\" AND resource.labels.location=\"$region\" AND resource.labels.job_name=\"$job\" AND labels.\"run.googleapis.com/execution_name\"=\"$name\" AND jsonPayload.message=\"model call\""
  say "reading the run's own model calls: gcloud logging read (execution ${name}, last 30 days)"
  run_bounded "$CLI_TIMEOUT" calls "$SANDBOX" gcloud logging read "${scope[@]}" --limit=5000 --freshness=30d --format=json "$filter"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured calls "gcloud logging read of execution ${name} did not answer (exit ${BOUNDED_STATUS}): the calls this check is about are in that log, and no access, a view that is not there and the wrong project are the three usual reasons. What gcloud printed is in $SCRATCH/calls.err"
    return 0
  fi
  if ! read_json calls "$SCRATCH/calls.out" "$SCRATCH/calls.txt"; then
    unmeasured calls "gcloud logging read answered something this check cannot read as a log: what gcloud printed is in $SCRATCH/calls.out"
    return 0
  fi
  local count routed reported served priced
  count=$(field "$SCRATCH/calls.txt" count)
  routed=$(field "$SCRATCH/calls.txt" routed)
  reported=$(field "$SCRATCH/calls.txt" reported)
  served=$(field "$SCRATCH/calls.txt" served)
  priced=$(field "$SCRATCH/calls.txt" priced)
  if [ "${count:-0}" = 0 ]; then
    unmeasured calls "gcloud logging read of execution ${name} answered no 'model call' line at all: the read covers the last 30 days (--freshness=30d) and the project's own default log bucket, so an execution older than that window, or one whose entries live in the installation's own view, answers an empty log for a run that really made calls. CHECK781_LOG_VIEW is the scope to read it again with"
    return 0
  fi
  if [ "${routed:-0}" = 0 ]; then
    fact calls FAIL "${count} model call line(s) were read for ${RUN_ID} and not one of them carries a \`route\` attribute: \`route\` is emitted only for a call that went to a provider — an empty one IS the Anthropic upstream (internal/gateway/forward.go, internal/gateway/log.go) — so this run's calls went to Anthropic and no provider was served at all. Check the repository's own pin and the project's routing switch (\`fugaro project routing show --json\`) before reading anything below as being about a provider"
    return 0
  fi
  fact calls PASS "${routed} of the run's ${count} model call line(s) went to a provider (serving_model=${served:-unknown}, priced_as=${priced:-unpriced}, charged_micros over the routed calls=$(field "$SCRATCH/calls.txt" charged_micros_route)): the provider's own model served these calls, read from the gateway's lines rather than from the pin the checkout carries"
  fact charged "$([ "${reported:-0}" -gt 0 ] && echo PASS || echo FAIL)" "$([ "${reported:-0}" -gt 0 ] && echo "${reported} of the ${routed} routed call(s) carry the provider's own reported_micros (>0), and the record's cost.reported_usd is $(field "$SCRATCH/record.txt" reported_usd) with cost.route_by=$(field "$SCRATCH/record.txt" route_by): the provider's reported cost is what was charged" || echo "${routed} routed call(s) carry no reported_micros at all and the record's cost.reported_usd is $(field "$SCRATCH/record.txt" reported_usd): a routed call whose cost nobody reported is charged from the price table, not from what the provider says it cost, which is the half of the acceptance this row exists for. cost.unreported_calls is $(field "$SCRATCH/record.txt" unreported_calls)")"
}

# step_key_from_bundle: the service's own audit line for this run. It carries
# the NAME of the credential it served, its tier and its status, and never a
# value — so this row is a name a reader can place and not a secret.
step_key_from_bundle() {
  if [ -z "$RUN_ID" ]; then
    unmeasured key-from-bundle "there is no run whose issuance could be read: the rows above say why"
    return 0
  fi
  local filter='jsonPayload.message="credsvc: audit" AND jsonPayload.run="'"$RUN_ID"'"'
  say "reading the service's own audit line for ${RUN_ID}: gcloud logging read (credsvc: audit)"
  run_bounded "$CLI_TIMEOUT" audit "$SANDBOX" gcloud logging read --resource-names="projects/$PROJECT" \
    --limit=2000 --freshness=30d --format=json "$filter"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured key-from-bundle "gcloud logging read of the service's audit lines did not answer (exit ${BOUNDED_STATUS}): this check cannot say which credential served the run. What gcloud printed is in $SCRATCH/audit.err"
    return 0
  fi
  if ! read_json audit "$SCRATCH/audit.out" "$SCRATCH/audit.txt" "$RUN_ID"; then
    unmeasured key-from-bundle "gcloud logging read answered something this check cannot read as a log: what gcloud printed is in $SCRATCH/audit.out"
    return 0
  fi
  local forward credential tier status
  forward=$(field "$SCRATCH/audit.txt" forward)
  credential=$(field "$SCRATCH/audit.txt" credential)
  tier=$(field "$SCRATCH/audit.txt" tier)
  status=$(field "$SCRATCH/audit.txt" status)
  if [ "${forward:-0}" = 0 ]; then
    unmeasured key-from-bundle "the service's log holds no forwarded model call for ${RUN_ID} (the filter is jsonPayload.message=\"credsvc: audit\" AND jsonPayload.run=\"${RUN_ID}\", over the last 30 days): Cloud Logging takes seconds to ingest, and an execution older than that window answers nothing — so this row could not be measured, and the job-env row above is what says the key was not in the job"
    return 0
  fi
  if [ "$status" != ok ]; then
    fact key-from-bundle FAIL "the service's audit line for ${RUN_ID} records credential=${credential:-none} tier=${tier:-none} with status=${status:-none}: a forwarded model call that did not end in \`ok\` was not served, so the calls above were answered by something else and the bundle is not what carried them"
    return 0
  fi
  fact key-from-bundle PASS "the service's own audit line for ${RUN_ID} records a forwarded model call served from credential=${credential:-none} (tier=${tier:-none}, status=ok): the credential is the bundle's, named and never valued — with the job-env row above (no model key mounted) this is the two halves of \"the key was read from the bundle\""
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="this check wrote no config, published no policy and changed no secret: its only write is the sandbox run ${RUN_ID:-none} and the branch and pull request that run left (outcome=$(field "$SCRATCH/record.txt" outcome 2>/dev/null || true), pr=$(field "$SCRATCH/record.txt" pr 2>/dev/null || true)) — close that pull request and delete its branch yourself once you have read the run"
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

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 781 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, its project and its region"
  say "  [read] gcloud secrets list — the installation's one bundle container, by the name internal/backend/gcp derives"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<project>/runs/<slug>/<run>/result.json — the record: the execution, the outcome and the cost figures"
  say "  [read] gcloud run jobs describe <the run's job> — its environment: no model key mounted, the credentials service's URL present"
  say "  [read] gcloud logging read (jsonPayload.message=\"model call\" for the execution) — the calls that carry route, serving_model, priced_as and reported_micros"
  say "  [read] gcloud logging read (credsvc: audit for the run) — the credential NAME and tier the service served the call from"
  for key in installation bundle run-launched terminal result-json job-env calls charged key-from-bundle; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no run, no job read and no log read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong installation bundle
  run-launched terminal result-json job-env calls charged key-from-bundle restore
  scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 781 RESULT\n' >&3
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
  printf 'CHECK 781 RESULT END (exit %s)\n' "$status" >&3
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

SANDBOX=${CHECK781_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 781 needs --sandbox: the checkout of a repository of the sandbox project. The run is launched from it. Usage: bash scripts/live-checks/check781.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
TASK=${CHECK781_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK781_RECIPE:-${RECIPE_CLI:-}}
DRY_RUN=${CHECK781_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK781_KEEP_SCRATCH:-}
LOG_VIEW=${CHECK781_LOG_VIEW:-}
REGION=${CHECK781_REGION:-us-east5}
RUN_TIMEOUT=${CHECK781_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK781_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK781_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK781_LAUNCH_TIMEOUT_SECONDS:-600}
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
  [ -n "$FUGARO_BIN" ] || die "Check 781 needs 'fugaro' on PATH: the check launches the run you have installed, and it is not there"
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
  step_bundle
  step_run
  step_terminal
  step_result_json
  step_job_env
  step_calls
  step_key_from_bundle
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
