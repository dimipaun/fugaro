#!/usr/bin/env bash
# Check 560 — the installation renews its key by itself: one real fire of the
# deployed Scheduler job renews a short-lived auto key, and `licence status`
# reads the successor's term.
#
# Plan task K7.4 of docs/licence-key-v2/implementation-plan.md writes this check
# (the owner's instruction of 2026-11-21), and the number is that plan's own
# ("Checks this plan takes (560–569)"). What it settles is **K4.2 with K2.3 and
# K2.4**, read end to end on a real installation and from the outside only: the
# tick Terraform applies is the one that renews a key nobody is watching (K4.2),
# the key it renews is an auto one whose successor the licence server issues
# (K2.3), and the installation ends up holding that successor beside the key it
# replaces (K2.4's two keys in the one project object).
#
# It is the opposite half of `fugaro licence refresh`: that command renews the
# key a person is watching, and this check measures the path where nobody is.
# The one thing it does to start the work is `gcloud scheduler jobs run` — the
# Scheduler job's own fire, the same call the daily tick makes — and everything
# it reads after that is the job's own output and `licence status`.
#
# What it measures:
#
#   * the installation under test, from `fugaro doctor --json` run in
#     `--sandbox`: the Fugaro project's name, the GCP project and the region.
#     Every gcloud read below is aimed with those three;
#   * the Scheduler job `fugaro-licence-renew` in the installation's scheduler
#     region: that it is there, that it is ENABLED (K4.2's module comment says it
#     ships RUNNING, because a paused schedule is a licence that quietly stops
#     renewing), and that its target is Scheduler's own POST to the Cloud Run
#     job's `:run` endpoint, authorised with an OAuth token of the installation's
#     scheduler account. The daily tick's own cron and time zone are read and
#     printed, and they are not a FAIL: K4.2's module pins `23 4 * * *` UTC, and
#     a difference is drift the row shows rather than a licence this check calls
#     broken;
#   * the Cloud Run job `fugarolicence` in the installation's region: that it is
#     THIS installation's licence job — its two `fugaro` labels, its argv
#     (`licence renew --job`), its 600s timeout, no retries, the licence account
#     as its service account, and the two names the module's own preconditions
#     pin (`FUGARO_PROJECT` is the Fugaro project doctor read and
#     `FUGARO_GCP_PROJECT` the GCP project);
#   * the premise of a renewal, from `fugaro licence status --json` in the
#     sandbox: the PROJECT's own key (source `project` and not a machine's — the
#     job renews the project's and only the project's, D2137), `renewal_mode:
#     auto` (a manual key is checked and never renewed), a state of `active` or
#     `grace`, and the term it is paid through. A SHORT-LIVED auto key is what
#     makes the renewal window open now. The check cannot read that window out of
#     `status` — the key's own `renew_before_days` is not in that document — so a
#     window that is not open is read where it is said: the job's own line;
#   * **the fire and the execution it starts**: one `gcloud scheduler jobs run`
#     of that Scheduler job, and then the newest execution of `fugarolicence`
#     created at or after it, with its own terminal condition. Nothing else in
#     this check starts a Cloud Run execution, so an execution created after this
#     call is that call's;
#   * **the renewal**: the job's own lines for that execution, read back with
#     `gcloud logging read` filtered by the execution's own
#     `run.googleapis.com/execution_name` — a line an earlier execution wrote is
#     not this fire's — `checked: the licence server answered that key … is
#     good` where the check was due, and `renewed: key <successor> is installed
#     in project <name> beside the key it replaces (fugaro/licence.json)`. The
#     successor's key ID is the support handle the specification says is not a
#     secret, and it is the one thing about the key this block carries. A window
#     that is not open is that same read: the job's own `nothing to do: key … is
#     paid through …, its renewal window opens …` is the row that could not be
#     measured, and it names the short-lived key the check needs;
#   * **the state after**: `fugaro licence status --json` again — the term is
#     STRICTLY later than the premise's, the source is still the project, the
#     renewal mode still `auto`, the state still `active` or `grace`. The check
#     record (`last_check`) is reported and is not required to have moved: a
#     window that opens between two checks renews without asking one (K4.1's
#     order is the check first and the renewal after, and a renewal alone writes
#     no check).
#
# What it creates: ONE Cloud Run job execution of the installation's own licence
# job (`fugarolicence`) — the installation's own renewal work, one container run
# of the job's 600s ceiling at 1 vCPU and 512 MiB — which writes what the licence
# server issues: the successor key and its secret version, in the installation's
# own project. That write is the installation's licence and is kept on purpose.
# Nothing else: the check writes no object of the runs bucket itself, reads the
# licence object's bytes nowhere, and deletes nothing at all.
#
# Usage:      bash scripts/live-checks/check560.sh --sandbox <a checkout>
#             bash scripts/live-checks/check560.sh --sandbox <a checkout> --dry-run
#             bash scripts/live-checks/check560.sh --help
# Needs:      bash, mktemp and python3, `gcloud` on PATH (the two describes, the
#             Scheduler fire, the execution read and the log read), and `fugaro`
#             on PATH from the sandbox checkout (doctor, and licence status).
# Where:      a SANDBOX installation first, and the one that holds a short-lived
#             `renewal_mode: auto` key. `fugaro-prod` is refused unless `--production`
#             is given, because production is only ever measured on purpose; a
#             value naming belong is refused by name; and so is a coding agent's
#             session — a renewal writes the installation's key, and `fugaro
#             licence renew --job` refuses an agent's session too.
# Costs:      one Cloud Run job execution (the job's own ceiling is 600s at 1
#             vCPU and 512 MiB) and a handful of read-only gcloud calls. No model
#             call, and no run of Fugaro's own compute.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check560_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. What stays in the project is the renewal the
#             job made — the installation's own licence — and one execution
#             record of its own job.
# Output:     progress on stderr and ONE block on stdout headed CHECK 560 RESULT,
#             then CHECK 560 RESULT END (exit N) — to copy back into the
#             register's Check 560 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold (a paused schedule, a target that is not Scheduler's own call
#             to this job, a job that is not this installation's licence job, a
#             renewal the job reported whose term did not move). 2: a row could
#             not be measured (no `gcloud` or `fugaro` answer, an installation
#             whose coordinator is off so it has no renewal job, a key that is
#             manual, a machine's key, no key at all, a renewal window that is
#             not open, or an execution that did not finish within `--wait`).
#
# A NOTE ON WHAT IS NEVER PRINTED: no licence key and no renewal secret, in whole
# or in part. The job's own lines carry the successor's key ID — the support
# handle the specification says is not a secret — and that ID is what this block
# prints. The check never reads the licence object of the runs bucket: `gcloud
# storage cat` on `fugaro/licence.json` would print every key it holds, and
# `licence status --json` carries none. It passes no key anywhere, and a line of
# the job's log this check did not recognise is never printed — the row names the
# `gcloud logging read` command to read them with instead.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK560_SANDBOX, CHECK560_PROJECT (the GCP project; doctor's own
# `project.gcp_project` when unset), CHECK560_REGION (the Cloud Run job's region;
# doctor's own when unset), CHECK560_SCHEDULER_REGION (when unset the check looks
# for the job among the project's Scheduler locations, the installation's own
# region first), CHECK560_PRODUCTION, CHECK560_DRY_RUN, CHECK560_WAIT (600: the
# bound on waiting for the execution, which is the job's own ceiling),
# CHECK560_POLL (15: seconds between reads while waiting), CHECK560_LOG_WAIT (90:
# the bound on waiting for the job's own lines to reach the log),
# CHECK560_LOG_FRESHNESS (30m: how far back the log read looks) and
# CHECK560_KEEP_SCRATCH.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a trace of this run
# prints the project's ids and the whole log dump the readers work from, and the
# RESULT block below is what the register takes.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to
# the saved descriptor.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory. A stop right after an `unmeasured` row
# ends the run as one that could not measure (exit 2): the difference matters to
# whoever reads the block, because 1 is a finding about the release and 2 is a
# run that never got to measure it.
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}

FACTS_FILE=""
LAST_ROW=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in
  FAIL) LAST_ROW=fail ;;
  *) LAST_ROW="" ;;
  esac
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2), whether it stopped
# there or ran to the end.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  LAST_ROW=unmeasured
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  local t
  for t in bash mktemp python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 560 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""
MANUAL=""

SANDBOX=""
PROJECT=""
REGION=""
SCHEDULER_REGION=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""
WAIT=""
POLL=""
LOG_WAIT=""
LOG_FRESHNESS=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check560.XXXXXX") || die "Check 560 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK560_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
}

scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then
    SCRATCH_KEPT=yes
    return 0
  fi
  rm -rf "$SCRATCH"
  SCRATCH_REMOVED=yes
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK560_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    if [ "$status" -eq 0 ]; then status=2; fi
  fi
  # A run that reached the end with a FAILed row is a run with a finding: exit 1
  # says so without anyone having to read the block for it. An unmeasured row is
  # the exception — it is a FAIL in the block and a 2 above.
  if [ "$status" -eq 0 ]; then
    local failed=no key
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
    done <<<"$facts"
    if [ "$failed" = yes ]; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(xtrace agent-session belong sandbox project scheduler job premise execution renewed status restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 560 RESULT\n' >&3
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
  if [ -n "$MANUAL" ]; then
    printf 'MANUAL: %s\n' "$MANUAL" >&3
  fi
  printf 'CHECK 560 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------- the readers
# Every document below is read with python3 rather than by pattern, so a field
# this check does not know about cannot be mistaken for one it does. Each reader
# prints `key=value` lines and exits 0 even on a document it cannot read: the
# caller reads the empty key as "the document did not answer", which is a row
# about the installation rather than this check's crash.

doctor_fields() { # doctor_fields FILE — \`fugaro doctor --json\`
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
p = doc.get("project") or {}
print("name=%s" % (p.get("name") or ""))
print("gcp_project=%s" % (p.get("gcp_project") or ""))
print("region=%s" % (p.get("region") or ""))
PY
}

scheduler_fields() { # scheduler_fields FILE — \`gcloud scheduler jobs describe\`
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
t = doc.get("httpTarget") or {}
tok = t.get("oauthToken") or {}
r = doc.get("retryConfig") or {}
print("name=%s" % (doc.get("name") or ""))
print("state=%s" % (doc.get("state") or ""))
print("schedule=%s" % (doc.get("schedule") or ""))
print("timezone=%s" % (doc.get("timeZone") or ""))
print("uri=%s" % (t.get("uri") or ""))
print("method=%s" % (t.get("httpMethod") or ""))
print("sa=%s" % (tok.get("serviceAccountEmail") or ""))
print("scope=%s" % (tok.get("scope") or ""))
retries = r.get("retryCount")
print("retries=%s" % ("" if retries is None else retries))
PY
}

# The Cloud Run job, in the shape `gcloud run jobs describe --format=json` prints
# (the same shape check-740-run-messages.sh reads a job's service account out
# of): metadata at the top, the container spec under spec.template.spec.
job_fields() { # job_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
spec = ((doc.get("spec") or {}).get("template") or {}).get("spec") or {}
c = (spec.get("template") or {}).get("spec") or {}
labels = (doc.get("metadata") or {}).get("labels") or {}
containers = c.get("containers") or []
first = containers[0] if containers and isinstance(containers[0], dict) else {}
env = {}
for e in first.get("env") or []:
    if isinstance(e, dict) and e.get("name"):
        env[e["name"]] = e.get("value") or ""
print("name=%s" % ((doc.get("metadata") or {}).get("name") or ""))
print("label_fugaro=%s" % (labels.get("fugaro") or ""))
print("label_role=%s" % (labels.get("fugaro_role") or ""))
print("sa=%s" % (c.get("serviceAccountName") or ""))
print("retries=%s" % (c.get("maxRetries") if c.get("maxRetries") is not None else ""))
print("timeout=%s" % (c.get("timeoutSeconds") if c.get("timeoutSeconds") is not None else ""))
print("command=%s" % " ".join(first.get("command") or []))
print("args=%s" % " ".join(first.get("args") or []))
print("fugaro_project=%s" % env.get("FUGARO_PROJECT", ""))
print("gcp_project=%s" % env.get("FUGARO_GCP_PROJECT", ""))
PY
}

# The newest execution of the job created at or after the fire. `gcloud run jobs
# executions list --format=json` prints the executions in whichever order the
# API hands them back, so the newest is picked here by its own creation time
# against the instant this check fired the Scheduler job: the only thing this
# check does to start work is that fire, so an execution created after it is that
# fire's.
executions_fields() { # executions_fields FILE STARTED_EPOCH
  python3 - "$1" "$2" <<'PY'
import datetime, json, sys

def epoch(ts):
    if not isinstance(ts, str) or not ts:
        return None
    try:
        return datetime.datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp()
    except Exception:
        return None

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
if not isinstance(doc, list):
    doc = []
started = int(sys.argv[2])
best = None
for e in doc:
    if not isinstance(e, dict):
        continue
    md = e.get("metadata") or {}
    st = e.get("status") or {}
    created = md.get("creationTimestamp") or st.get("startTime") or ""
    at = epoch(created)
    if at is None or at < started:
        continue
    if best is None or at > best[0]:
        best = (at, e, created)
if best is None:
    print("found=no")
    raise SystemExit(0)
_, e, created = best
md = e.get("metadata") or {}
st = e.get("status") or {}
completed = ""
for c in st.get("conditions") or []:
    if isinstance(c, dict) and c.get("type") == "Completed":
        completed = c.get("status") or ""
print("found=yes")
print("name=%s" % ((md.get("name") or "").rsplit("/", 1)[-1]))
print("created=%s" % created)
print("completed=%s" % completed)
print("completion_time=%s" % (st.get("completionTime") or ""))
PY
}

# The job's own lines for the execution, read back out of Cloud Logging. Every
# marker below is a sentence ee/register/licence_renew.go really writes, and the
# terminal ones are the ones the job ends on: the renewal it made, and each of
# the states it can stop in. The newest terminal line is what the row is read
# from; a line this check does not recognise is counted and not printed, because
# a line the check cannot name is a line it cannot vouch for.
logs_fields() { # logs_fields FILE
  python3 - "$1" <<'PY'
import json, re, sys

MARKERS = [
    ("renewed", r"renewed: key (\S+) is installed in project (\S+) beside the key it replaces \((.*)\)$"),
    ("window", r"nothing to do: key (\S+) is paid through (\S+), its renewal window opens (\S+), and its last check was (.*)$"),
    ("none_yet", r"nothing to renew yet: the key is paid through (\S+), and its own renewal window opens (\d+) days before that$"),
    ("already", r"the licence server answered key (\S+), which is the key this installation already holds \(paid through (\S+)\): nothing was written$"),
    ("revoked", r"(?:the licence server answered that key (\S+) is revoked:|key (\S+) is revoked, so project )"),
    ("manual", r"key (\S+) renews manually \(renewal_mode: (\S+)\)"),
    ("offline", r"this key is offline \(renewal_mode: none\)"),
    ("unreachable", r"note: the check of key (\S+) did not complete"),
    ("renew_failed", r"note: the licence server did not answer the renewal of key (\S+)"),
    ("unread_status", r"note: the licence server answered the renewal with a status this Fugaro does not read"),
    ("checked", r"checked: the licence server answered that key (\S+) is good$"),
]
TERMINAL = {"renewed", "window", "none_yet", "already", "revoked", "manual", "offline", "unreachable", "renew_failed", "unread_status"}

try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
if not isinstance(doc, list):
    doc = []
lines = []
for e in doc:
    if not isinstance(e, dict):
        continue
    text = e.get("textPayload")
    if text is None:
        payload = e.get("jsonPayload") or {}
        text = payload.get("message") if isinstance(payload, dict) else None
    if not isinstance(text, str) or not text.strip():
        continue
    lines.append((e.get("timestamp") or "", text.strip(), e.get("severity") or ""))
lines.sort()
print("lines=%d" % len(lines))
terminals = []
checked = ""
for ts, text, _ in lines:
    for kind, pattern in MARKERS:
        m = re.match(pattern, text)
        if not m:
            continue
        if kind == "checked":
            checked = "%s|%s" % (m.group(1), ts)
            continue
        groups = [g if g is not None else "" for g in m.groups()]
        if kind in TERMINAL:
            terminals.append((ts, kind, groups))
        break
if terminals:
    ts, kind, groups = terminals[-1]
    print("marker=%s|%s" % (kind, "|".join(groups)))
    print("marker_at=%s" % ts)
print("checked=%s" % checked)
PY
}

# licence_fields FILE — `fugaro licence status --json`, the shape K3.2 prints.
# Not one field here is a key: the document carries no key at all.
licence_fields() { # licence_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit(0)
if not isinstance(doc, dict):
    raise SystemExit(0)
check = doc.get("last_check") or {}
print("project=%s" % (doc.get("project") or ""))
print("state=%s" % (doc.get("state") or ""))
print("tier=%s" % (doc.get("tier") or ""))
print("seats=%s" % (doc.get("seats") if doc.get("seats") is not None else ""))
print("paid_through=%s" % (doc.get("paid_through") or ""))
print("kind=%s" % (doc.get("kind") or ""))
print("renewal_mode=%s" % (doc.get("renewal_mode") or ""))
print("source=%s" % (doc.get("source") or ""))
print("revoked=%s" % ("true" if doc.get("revoked") else "false"))
print("check_at=%s" % (check.get("at") or ""))
print("check_status=%s" % (check.get("status") or ""))
print("check_key=%s" % (check.get("key_id") or ""))
print("check_failures=%s" % (check.get("failures") if check.get("failures") is not None else ""))
PY
}

field() { # field FILE KEY — the value of one \`key=value\` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ts_after A B — 0 when A is strictly later than B, 1 when it is not, 2 when one
# of the two is not a timestamp this check can read. The comparison is made in
# python3 rather than between strings, so a difference of format cannot be read
# as a difference of time.
ts_after() { # ts_after A B
  python3 - "$1" "$2" <<'PY'
import datetime, sys

def e(s):
    s = (s or "").strip()
    if not s:
        return None
    try:
        return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except Exception:
        return None

a, b = e(sys.argv[1]), e(sys.argv[2])
if a is None or b is None:
    raise SystemExit(2)
raise SystemExit(0 if a > b else 1)
PY
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check560.sh --sandbox <a checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run prints the whole log dump the readers work from"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the gcloud answers and the job's lines to the terminal"
}

# guard_agent_session: this check fires the installation's own renewal job, which
# writes the installation's key. `fugaro licence renew --job` refuses a coding
# agent's session for exactly that reason, and the check refuses one before it
# spends anything.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 560 fires the installation's own renewal job, which writes the installation's key — \`fugaro licence renew --job\` refuses an agent's session for that reason, and so does this check. It is yours to run at your own terminal, on a sandbox installation holding a short-lived auto key"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# guard_inputs: the sandbox and the two ids, before anything runs. `fugaro-prod`
# is the one project a check never measures by accident.
guard_inputs() {
  local all="${SANDBOX:-}${PROJECT:-}${REGION:-}${SCHEDULER_REGION:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check was given names belong"
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --project fugaro-prod, which is refused unless --production is given: this check FIRES the installation's renewal job, and production is only ever measured on purpose. Nothing was read and nothing was fired"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact sandbox PASS "$SANDBOX (a real run reads it with \`fugaro doctor --json\` from there)"
    fact project PASS "${PROJECT:-the project doctor reads} (a real run holds the GCP project and the region the job lives in, and refuses fugaro-prod without --production)"
    return 0
  fi
}

# ------------------------------------------------------------------ the steps
# step_sandbox: the installation under test, and the two ids every gcloud read
# below is aimed with. `fugaro doctor --json` is the CLI's own answer about the
# installation it is run in, so it is run in the sandbox checkout and nowhere
# else.
step_sandbox() {
  local rc=0
  ( cd "$SANDBOX" && fugaro doctor --json ) >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured sandbox "\`fugaro doctor --json\` in $SANDBOX exited $rc and printed: $(last_lines "$SCRATCH/doctor.err"). This check reads the installation it is measuring through doctor, so a doctor that does not answer is a row it cannot measure. Run it yourself in that directory ('fugaro doctor') and settle what it reports"
    stop "fugaro doctor did not answer in the sandbox, so the check cannot say which installation it is about"
  fi
  doctor_fields "$SCRATCH/doctor.json" >"$SCRATCH/doctor.txt"
  local name gcp region
  name=$(field "$SCRATCH/doctor.txt" name)
  gcp=$(field "$SCRATCH/doctor.txt" gcp_project)
  region=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$name" ] || [ -z "$gcp" ] || [ -z "$region" ]; then
    unmeasured sandbox "doctor answered in $SANDBOX with name '${name:-none}', GCP project '${gcp:-none}' and region '${region:-none}': this check needs all three, because the job, the schedule and the licence all live in the project doctor names. Run 'fugaro doctor' in that directory and settle what it reports"
    stop "doctor did not name the installation's project and region, so the check cannot aim a single gcloud read"
  fi
  FUGARO_NAME=$name
  [ -n "$PROJECT" ] || PROJECT=$gcp
  [ -n "$REGION" ] || REGION=$region
  if [ "$PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this installation is the fugaro-prod project, which is refused unless --production is given: this check FIRES the installation's renewal job. Nothing was read and nothing was fired"
  fi
  fact sandbox PASS "$SANDBOX is the installation doctor answered for: the Fugaro project '$FUGARO_NAME', in GCP project $PROJECT, region $REGION"
}

# step_project: the ids in play, said out loud in their own row, and the
# production fence answered where they are known rather than where they were
# typed.
step_project() {
  if [ "$PROJECT" = fugaro-prod ]; then
    fact project PASS "$PROJECT — production, named with --production: the registration's own rule is that Checks 560 to 569 run on a sandbox installation first, so read this block as a production run by choice"
  else
    fact project PASS "$PROJECT is the GCP project this installation's jobs and schedule live in (doctor's own \`project.gcp_project\`, or --project), region $REGION, and it is not fugaro-prod, which is refused without --production"
  fi
}

# step_scheduler: the Scheduler job, and the region it lives in. Scheduler is not
# offered in every Cloud Run region (K4.2's module comment), so the job's region
# is the installation's scheduler region and not necessarily the installation's
# own: with no --scheduler-region this check asks the project which Scheduler
# locations it has and describes the job in each, the installation's own region
# first, and takes the one that answers.
scheduler_location() {
  # SCHEDULER_REGION is set HERE and not returned through a command
  # substitution: a stop below has to end this run, and a stop inside $( ) would
  # end a subshell instead, leaving the check to read an empty answer as one it
  # could not measure. Every path below leaves the job's own document in
  # $SCRATCH/scheduler.json, which is what step_scheduler reads.
  local rc=0
  if [ -n "$SCHEDULER_REGION" ]; then
    gcloud scheduler jobs describe "$SCHEDULER_JOB" --location "$SCHEDULER_REGION" --project "$PROJECT" --format=json >"$SCRATCH/scheduler.json" 2>"$SCRATCH/scheduler.err" || rc=$?
    if [ "$rc" -ne 0 ]; then
      unmeasured scheduler "the Scheduler job $SCHEDULER_JOB is not in the location --scheduler-region named (\`gcloud scheduler jobs describe\` exited $rc: $(last_lines "$SCRATCH/scheduler.err")). K4.2 applies that job UNDER THE COORDINATOR'S OWN CONDITION: an installation that has not opted into the coordinator has no tagged image to run and no renewal job at all, and \`fugaro licence status\` says renewal is by hand there (TN5). Nothing about the renewal was measured. Name the region the installation's Scheduler jobs live in (\`gcloud scheduler jobs list --location <location>\`) or turn the coordinator on with \`fugaro update\`"
      stop "the Scheduler job $SCHEDULER_JOB was not found in the location this check was given, so the tick this check exists to fire is not there"
    fi
    return 0
  fi
  gcloud scheduler locations list --project "$PROJECT" --format=json >"$SCRATCH/locations.json" 2>"$SCRATCH/locations.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured scheduler "the project's Scheduler locations could not be listed (\`gcloud scheduler locations list\` exited $rc: $(last_lines "$SCRATCH/locations.err")), so this check cannot find the region the Scheduler job runs in. Scheduler is not offered in every Cloud Run region (K4.2), so its region is a fact about this installation rather than a name this check can derive: pass --scheduler-region with the region the installation's Scheduler jobs live in, or run the command yourself"
    stop "the Scheduler locations could not be listed, so the check cannot aim its read at the Scheduler job"
  fi
  local candidate found=""
  for candidate in "$REGION" $(python3 - "$SCRATCH/locations.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = []
if not isinstance(doc, list):
    doc = []
names = []
for e in doc:
    if isinstance(e, dict):
        n = e.get("locationId") or (e.get("name") or "").rsplit("/", 1)[-1]
        if n:
            names.append(n)
print(" ".join(sorted(set(names))))
PY
  ); do
    [ -n "$candidate" ] || continue
    rc=0
    gcloud scheduler jobs describe "$SCHEDULER_JOB" --location "$candidate" --project "$PROJECT" --format=json >"$SCRATCH/scheduler.json" 2>"$SCRATCH/scheduler.err" || rc=$?
    if [ "$rc" -eq 0 ]; then
      found=$candidate
      break
    fi
  done
  if [ -z "$found" ]; then
    unmeasured scheduler "the Scheduler job $SCHEDULER_JOB is in none of this project's Scheduler locations (the installation's own region $REGION first). K4.2 applies that job UNDER THE COORDINATOR'S OWN CONDITION: an installation that has not opted into the coordinator has no tagged image to run and no renewal job at all, and \`fugaro licence status\` says renewal is by hand there (TN5). Nothing about the renewal was measured. Read that job's region yourself (\`gcloud scheduler jobs list --location <location>\`) and pass --scheduler-region, or turn the coordinator on with \`fugaro update\`"
    stop "the Scheduler job $SCHEDULER_JOB was not found, so the tick this check exists to fire is not there"
  fi
  SCHEDULER_REGION=$found
}

step_scheduler() {
  local where
  scheduler_location
  where=$SCHEDULER_REGION
  [ -n "$where" ] || return 0
  scheduler_fields "$SCRATCH/scheduler.json" >"$SCRATCH/scheduler.txt"
  local state schedule timezone uri method sa scope retries
  state=$(field "$SCRATCH/scheduler.txt" state)
  schedule=$(field "$SCRATCH/scheduler.txt" schedule)
  timezone=$(field "$SCRATCH/scheduler.txt" timezone)
  uri=$(field "$SCRATCH/scheduler.txt" uri)
  method=$(field "$SCRATCH/scheduler.txt" method)
  sa=$(field "$SCRATCH/scheduler.txt" sa)
  scope=$(field "$SCRATCH/scheduler.txt" scope)
  retries=$(field "$SCRATCH/scheduler.txt" retries)
  local want_uri="https://run.googleapis.com/v2/projects/$PROJECT/locations/$REGION/jobs/$LICENCE_JOB:run"
  # The tick's own words, printed in every row below so a reader sees what the
  # schedule is: K4.2's module pins a daily tick at 23 4 * * * UTC and a
  # difference is drift the row shows rather than a licence this check calls
  # broken.
  local tick="state '$state', schedule '$schedule' in '${timezone:-no time zone}', ${retries:-no} retries"
  if [ "$state" != ENABLED ]; then
    fact scheduler FAIL "$SCHEDULER_JOB in Scheduler location $where is $tick: K4.2's module ships it RUNNING, because a paused schedule is a licence that quietly stops renewing — the installation's key would lapse at its own expires_at with nothing on the job's log. Enable it (\`gcloud scheduler jobs resume $SCHEDULER_JOB --location $where --project $PROJECT\`), or say why this installation's renewal is by hand"
    stop "the licence renewal schedule is not ENABLED, so no tick renews this installation's key"
  fi
  if [ "$method" != POST ] || [ "$uri" != "$want_uri" ]; then
    fact scheduler FAIL "$SCHEDULER_JOB in $where is $tick, but its target is ${method:-nothing} ${uri:-nothing} rather than POST $want_uri: K4.2 applies this job as Scheduler's own POST to the Cloud Run job's :run endpoint, and a target that is anything else is a tick that does not run the licence job this row was read about"
    stop "the licence renewal schedule does not call the Cloud Run licence job"
  fi
  case "$sa" in
  *"@$PROJECT.iam.gserviceaccount.com") ;;
  *)
    fact scheduler FAIL "$SCHEDULER_JOB in $where is $tick and calls $want_uri, but its OAuth token is minted for '${sa:-nobody}': K4.2 grants roles/run.invoker on the licence job to THIS installation's scheduler account, whose address is an account of $PROJECT, so a call authorised as anything else is a tick that the job would refuse"
    stop "the licence renewal schedule calls the job as an account that is not this installation's"
    ;;
  esac
  if [ "$scope" != "https://www.googleapis.com/auth/cloud-platform" ]; then
    fact scheduler FAIL "$SCHEDULER_JOB in $where is $tick and calls $want_uri as $sa, but with the OAuth scope '${scope:-none}' rather than 'https://www.googleapis.com/auth/cloud-platform': K4.2 writes that scope, and the Cloud Run Admin API's :run call is not in a narrower one"
    stop "the licence renewal schedule's call carries a scope the Cloud Run Admin API does not accept"
  fi
  fact scheduler PASS "$SCHEDULER_JOB in Scheduler location $where is $tick and calls POST $want_uri with an OAuth token of $sa (scope cloud-platform): this is K4.2's tick, and the fire below is the same call the daily schedule makes"
}

# step_job: the Cloud Run job itself, and that it is THIS installation's licence
# job. The two names in its environment are the ones the module's own
# preconditions pin, and they are what makes a job in a shared project this
# installation's own.
step_job() {
  local rc=0
  gcloud run jobs describe "$LICENCE_JOB" --project "$PROJECT" --region "$REGION" --format=json >"$SCRATCH/job.json" 2>"$SCRATCH/job.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured job "the Cloud Run job $LICENCE_JOB is not in $PROJECT/$REGION (\`gcloud run jobs describe\` exited $rc: $(last_lines "$SCRATCH/job.err")). K4.2 applies that job UNDER THE COORDINATOR'S OWN CONDITION, so an installation whose coordinator is off has no renewal job and renews by hand (TN5): \`fugaro doctor\` reads the coordinator, and \`fugaro update\` applies the job where it holds. Nothing about the renewal was measured. This check needs an account that may read Cloud Run jobs there (roles/run.viewer or better)"
    stop "the Cloud Run licence job $LICENCE_JOB was not found, so the Scheduler fire below would have nothing to run"
  fi
  job_fields "$SCRATCH/job.json" >"$SCRATCH/job.txt"
  local name lf lr sa retries timeout command args fp gp
  name=$(field "$SCRATCH/job.txt" name)
  lf=$(field "$SCRATCH/job.txt" label_fugaro)
  lr=$(field "$SCRATCH/job.txt" label_role)
  sa=$(field "$SCRATCH/job.txt" sa)
  retries=$(field "$SCRATCH/job.txt" retries)
  timeout=$(field "$SCRATCH/job.txt" timeout)
  command=$(field "$SCRATCH/job.txt" command)
  args=$(field "$SCRATCH/job.txt" args)
  fp=$(field "$SCRATCH/job.txt" fugaro_project)
  gp=$(field "$SCRATCH/job.txt" gcp_project)
  if [ "$lf" != managed ] || [ "$lr" != licence ]; then
    fact job FAIL "$LICENCE_JOB exists in $PROJECT/$REGION and its labels are fugaro='${lf:-none}' and fugaro_role='${lr:-none}' rather than 'managed' and 'licence': a job by that name that this installation did not apply as the licence job is not the one this check may fire, and nothing was fired"
    stop "the job named $LICENCE_JOB is not this installation's licence job"
  fi
  if [ "$args" != "licence renew --job" ]; then
    fact job FAIL "$LICENCE_JOB in $PROJECT/$REGION is labelled as the licence job but its argv is '${command:-nothing} ${args:-nothing}' rather than 'licence renew --job': K4.2 writes the job's own verb, which is what makes it the installation's check-and-renew loop, and a job whose argv differs runs something else. Nothing was fired"
    stop "the licence job's argv is not the renewal job's own command"
  fi
  case "$sa" in
  *"@$PROJECT.iam.gserviceaccount.com") ;;
  *)
    fact job FAIL "$LICENCE_JOB in $PROJECT/$REGION runs as '${sa:-nobody}' rather than as an account of $PROJECT: K4.2 gives the job its own account ('fugaro-licence') and that account is the whole of its reach — the three-object read, the one-object write and four calls on the renewal secret's container"
    stop "the licence job does not run as this installation's own licence account"
    ;;
  esac
  if [ "$fp" != "$FUGARO_NAME" ] || [ "$gp" != "$PROJECT" ]; then
    fact job FAIL "$LICENCE_JOB in $PROJECT/$REGION carries FUGARO_PROJECT='${fp:-none}' and FUGARO_GCP_PROJECT='${gp:-none}' rather than '$FUGARO_NAME' and '$PROJECT': K4.2's module pins those two to this root's own project, and the job has no checkout and no config file, so those two names are the whole of how it finds the installation whose key it renews"
    stop "the licence job's environment names another installation"
  fi
  if [ "$timeout" != 600 ] || [ "$retries" != 0 ]; then
    fact job FAIL "$LICENCE_JOB in $PROJECT/$REGION carries a ${timeout:-none}-second timeout with ${retries:-none} retries rather than the 600 seconds and no retries K4.2's module pins (deploy/terraform/gcp/modules/installation/licence.tf: timeout = \"600s\" and max_retries = 0). The renewal is one container run at that ceiling and a retry after a timeout would renew twice, so a job that differs here is not the job this check may fire. Nothing was fired"
    stop "the licence job's timeout and retries are not the ones K4.2's module pins"
  fi
  fact job PASS "$LICENCE_JOB in $PROJECT/$REGION is this installation's licence job: labels fugaro=managed and fugaro_role=licence, argv '$args', ${timeout:-no} timeout in seconds, ${retries:-no} retries, running as $sa, with FUGARO_PROJECT=$fp and FUGARO_GCP_PROJECT=$gp — the two names the module's own preconditions pin"
}

# licence_status KEY — `fugaro licence status --json` in the sandbox, and the
# fields this check reads out of it. The document carries no key at all (K3.2),
# which is why this is the only read of the installation's licence the check
# makes: the licence object itself holds the keys and is never read here.
licence_status() { # licence_status KEY
  local key=$1 rc=0
  ( cd "$SANDBOX" && fugaro licence status --json ) >"$SCRATCH/$key.json" 2>"$SCRATCH/$key.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured "$key" "\`fugaro licence status --json\` in $SANDBOX exited $rc and printed: $(last_lines "$SCRATCH/$key.err"). Nothing about the installation's licence was read"
    stop "licence status did not answer in the sandbox, so the check cannot say what the installation is running on"
  fi
  licence_fields "$SCRATCH/$key.json" >"$SCRATCH/$key.txt"
  if [ -z "$(field "$SCRATCH/$key.txt" project)" ]; then
    unmeasured "$key" "\`fugaro licence status --json\` in $SANDBOX did not answer with the document this check reads (K3.2's shape carries project, state, tier, seats, paid_through, kind, renewal_mode, source, revoked, last_check and warning). Run it yourself and settle what it prints"
    stop "licence status did not answer with the JSON shape this check reads"
  fi
}

# step_premise: what the installation is running on before anything is fired. The
# job renews the PROJECT's key and only the project's (D2137), an auto key and
# only an auto one (a manual key is checked and never renewed), and a state that
# still has a term to renew.
step_premise() {
  licence_status premise
  local state source mode paid check_key check_status
  state=$(field "$SCRATCH/premise.txt" state)
  source=$(field "$SCRATCH/premise.txt" source)
  mode=$(field "$SCRATCH/premise.txt" renewal_mode)
  paid=$(field "$SCRATCH/premise.txt" paid_through)
  check_key=$(field "$SCRATCH/premise.txt" check_key)
  check_status=$(field "$SCRATCH/premise.txt" check_status)
  PRE_PAID=$paid
  PRE_KEY=$check_key
  PRE_CHECK_AT=$(field "$SCRATCH/premise.txt" check_at)
  local check_note="no check has run yet"
  if [ -n "$check_status" ]; then
    check_note="the last check was $check_status at $(field "$SCRATCH/premise.txt" check_at) about key ${check_key:-none}"
  fi
  if [ "$source" != project ]; then
    unmeasured premise "this installation's licence answers from source '${source:-none}' with state '${state:-none}': the renewal job renews the PROJECT'S key and only the project's (D2137), so a machine's own key is not the licence the tick renews. Install the project's key with \`fugaro licence set\` in this installation, then run the check again"
    stop "no project key is installed, so the job this check fires would have nothing to renew (${check_note})"
  fi
  case "$mode" in
  auto) ;;
  manual)
    unmeasured premise "the project's key has renewal_mode 'manual' and state '$state', paid through ${paid:-nowhere}: K2.3's job CHECKS a manual key and never renews it — the next term is a person's \`fugaro licence refresh\` — so this check has nothing to measure here. Check 560 needs a SHORT-LIVED key whose renewal_mode is auto (the licence site's own test key), installed with \`fugaro licence set\`; a manual one is Check 561's premise"
    stop "the key is a manual one, which no tick renews"
    ;;
  *)
    unmeasured premise "the project's key answers with renewal_mode '${mode:-none}' and state '$state': this check needs an auto key (the job renews an auto key and only an auto one), and a key whose mode is none contacts nothing at all. Install an auto key with \`fugaro licence set\` and run the check again"
    stop "the key's renewal mode is '${mode:-none}', so the tick would not renew it"
    ;;
  esac
  case "$state" in
  active | grace) ;;
  *)
    unmeasured premise "the project's key is '$state', paid through ${paid:-nowhere}: a key past its term has no window to renew in, and the job's renewal would be a license server answer about a lapsed licence. Install a SHORT-LIVED auto key with \`fugaro licence set\` (the licence site's own test key) and run the check again"
    stop "the key is not in a state a renewal is about"
    ;;
  esac
  if [ -z "$paid" ]; then
    unmeasured premise "the project's key answers with renewal_mode auto and state '$state' and no paid_through: this check compares the term before and after its one fire, so a key with no term in the document is a row it cannot measure"
    stop "the key's term was not read, so the before-and-after this check is made of has no before"
  fi
  fact premise PASS "this installation runs on the project's own auto key ${check_key:-<id not in this document>} (source $source, state $state, paid through $paid, tier $(field "$SCRATCH/premise.txt" tier)): $check_note. A SHORT-LIVED key is what makes the renewal window open now; the window itself is not in this document, so a window that is not open is read from the job's own line below"
}

# step_execution: the fire, and the execution it starts. The only thing this
# check does to start work is `gcloud scheduler jobs run` — the same call the
# daily tick makes — so the newest execution created at or after it is that
# call's, and its terminal condition is what says the job finished.
step_execution() {
  local started rc=0
  started=$(date -u +%s)
  say "firing the Scheduler job $SCHEDULER_JOB in $SCHEDULER_REGION (this is the daily tick's own call)..."
  gcloud scheduler jobs run "$SCHEDULER_JOB" --location "$SCHEDULER_REGION" --project "$PROJECT" >"$SCRATCH/fire.out" 2>"$SCRATCH/fire.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    unmeasured execution "\`gcloud scheduler jobs run $SCHEDULER_JOB --location $SCHEDULER_REGION --project $PROJECT\` exited $rc: $(last_lines "$SCRATCH/fire.err"). The renewal this check is about is started by that call and by nothing else, so no execution was read and nothing was renewed by this run"
    stop "the Scheduler job could not be fired, so no renewal ran"
  fi
  local deadline=$((started + WAIT)) found completed name created completion rc2
  while :; do
    rc2=0
    gcloud run jobs executions list --job "$LICENCE_JOB" --project "$PROJECT" --region "$REGION" --limit 5 --format=json >"$SCRATCH/execs.json" 2>"$SCRATCH/execs.err" || rc2=$?
    if [ "$rc2" -ne 0 ]; then
      unmeasured execution "the executions of $LICENCE_JOB in $PROJECT/$REGION could not be read (\`gcloud run jobs executions list\` exited $rc2: $(last_lines "$SCRATCH/execs.err")), so this check cannot say whether the fire it made started one. Read them yourself (\`gcloud run jobs executions list --job $LICENCE_JOB --region $REGION --project $PROJECT\`) and run the check again. This check needs an account that may read Cloud Run jobs there (roles/run.viewer or better)"
      stop "the executions of the licence job could not be read after the fire"
    fi
    executions_fields "$SCRATCH/execs.json" "$started" >"$SCRATCH/execs.txt"
    found=$(field "$SCRATCH/execs.txt" found)
    completed=$(field "$SCRATCH/execs.txt" completed)
    if [ "$found" = yes ] && [ -n "$completed" ]; then break; fi
    if [ "$(date -u +%s)" -ge "$deadline" ]; then
      if [ "$found" != yes ]; then
        unmeasured execution "no execution of $LICENCE_JOB in $PROJECT/$REGION was created in the ${WAIT}s after the fire (\`gcloud scheduler jobs run\` answered and the executions list holds none newer than it): the Scheduler job's call to the Cloud Run job did not start one. Check the Scheduler job's target and the roles/run.invoker grant K4.2 gives the installation's scheduler account on that job (\`gcloud scheduler jobs describe $SCHEDULER_JOB --location $SCHEDULER_REGION --project $PROJECT\`)"
      else
        unmeasured execution "the execution of $LICENCE_JOB that the fire started had not finished ${WAIT}s later (its Completed condition is still '${completed:-absent}'): the job's own ceiling is 600s, so a run that outlives it is a job that will not report a renewal tonight. Read it yourself: \`gcloud run jobs executions describe $(field "$SCRATCH/execs.txt" name) --project $PROJECT --region $REGION\`"
      fi
      stop "the execution the fire started did not finish within ${WAIT}s, so no renewal was read"
    fi
    sleep "$POLL"
  done
  name=$(field "$SCRATCH/execs.txt" name)
  EXECUTION_NAME=$name
  created=$(field "$SCRATCH/execs.txt" created)
  completion=$(field "$SCRATCH/execs.txt" completion_time)
  if [ "$completed" != True ]; then
    unmeasured execution "the execution $name of $LICENCE_JOB (created $created, completed ${completion:-no completion time}) ended with its Completed condition '${completed:-absent}' rather than True: the job itself failed, and a check of the installation's licence was still made and recorded, so read the job's own error first: gcloud logging read 'resource.type=\"cloud_run_job\" AND resource.labels.job_name=\"$LICENCE_JOB\"' --project $PROJECT --freshness 1h --limit 20"
    stop "the licence job's execution failed"
  fi
  fact execution PASS "one fire of the Scheduler job $SCHEDULER_JOB in $SCHEDULER_REGION started exactly one execution of the Cloud Run job $LICENCE_JOB, and it finished: $name (created $created, completed ${completion:-with no completion time}), Completed condition True"
}

# step_renewed: what the job said. Every terminal sentence below is one
# ee/register/licence_renew.go really writes, and each of them is a different
# answer about this installation's key: the renewal it made, and the states it
# can stop in. A line this check does not recognise is counted and not printed.
step_renewed() {
  local deadline=$(($(date -u +%s) + LOG_WAIT)) marker kind m1 m2 m3 m4 checked checked_key
  # The filter names the execution THIS run's fire started (step_execution's own
  # `name`): the job's lines for this fire, and not the terminal line of whatever
  # execution ran before it.
  local filter="resource.type=\"cloud_run_job\" AND resource.labels.job_name=\"$LICENCE_JOB\" AND labels.\"run.googleapis.com/execution_name\"=\"$EXECUTION_NAME\""
  while :; do
    local rc=0
    gcloud logging read "$filter" \
      --project "$PROJECT" --freshness "$LOG_FRESHNESS" --limit 50 --format=json \
      >"$SCRATCH/logs.json" 2>"$SCRATCH/logs.err" || rc=$?
    if [ "$rc" -ne 0 ]; then
      unmeasured renewed "the job's own lines could not be read (\`gcloud logging read\` exited $rc: $(last_lines "$SCRATCH/logs.err")), so this check cannot say what the job answered about the key. Read them yourself: gcloud logging read '$filter' --project $PROJECT --freshness 1h --limit 20. This check needs an account that may read logs there (roles/logging.viewer or better)"
      stop "the job's own lines could not be read, so the renewal was not measured"
    fi
    logs_fields "$SCRATCH/logs.json" >"$SCRATCH/logs.txt"
    marker=$(field "$SCRATCH/logs.txt" marker)
    if [ -n "$marker" ]; then break; fi
    if [ "$(date -u +%s)" -ge "$deadline" ]; then
      unmeasured renewed "the job's own lines hold no sentence this check recognises about the key (it read $(field "$SCRATCH/logs.txt" lines) line(s) within --freshness $LOG_FRESHNESS), so what the job answered about this installation's licence was not measured. Read the job's lines yourself: gcloud logging read '$filter' --project $PROJECT --freshness 1h --limit 20"
      stop "the job's own lines did not carry an answer this check knows, so the renewal was not measured"
    fi
    sleep 6
  done
  IFS='|' read -r kind m1 m2 m3 m4 <<<"$marker"
  checked=$(field "$SCRATCH/logs.txt" checked)
  checked_key=${checked%%|*}
  local at
  at=$(field "$SCRATCH/logs.txt" marker_at)
  case "$kind" in
  renewed)
    if [ -n "$PRE_KEY" ] && [ "$m1" = "$PRE_KEY" ]; then
      fact renewed FAIL "the job's own line says 'renewed: key $m1 …' and the installation was already running key $PRE_KEY (the key its last check was about): a renewal installs the SUCCESSOR beside the key it replaces (K2.4), and a line naming the key that was already there would be a renewal that replaced the key with itself. The line: renewed: key $m1 is installed in project $m2 beside the key it replaces ($m3)"
      stop "the job reported a renewal whose successor is the key the installation already held"
    fi
    fact renewed PASS "the job renewed the installation's key at $at: 'renewed: key $m1 is installed in project $m2 beside the key it replaces ($m3)' — the successor key ID is the support handle the specification says is not a secret, and it is the only thing about the key this block carries${checked_key:+; the check before the renewal answered 'checked: the licence server answered that key $checked_key is good'}"
    ;;
  window)
    unmeasured renewed "the job's own line says the renewal window is NOT open: 'nothing to do: key $m1 is paid through $m2, its renewal window opens $m3, and its last check was $m4'. Check 560 measures a renewal that happens now, so its premise is a SHORT-LIVED auto key: install one (the licence site's own short test key, whose renew_before_days reaches back past now) with \`fugaro licence set\`, and run the check again"
    stop "the key's renewal window is not open, so this tick had nothing to renew"
    ;;
  none_yet)
    unmeasured renewed "the job's own line says the licence server answered with no successor: 'nothing to renew yet: the key is paid through $m1, and its own renewal window opens $m2 days before that'. The window is open and the server still issues nothing, which is a state of the entitlement rather than of this installation: settle it at the licence site, then run the check again"
    stop "the licence server answered the renewal with no successor key"
    ;;
  already)
    unmeasured renewed "the job's own line says the server answered with the key the installation already holds: 'the licence server answered key $m1, which is the key this installation already holds (paid through $m2): nothing was written' — D2200's second half, a reissue of the term that is running. Nothing was renewed, so this row could not be measured: the entitlement needs a successor the installation does not hold"
    stop "the server's answer was the key the installation already holds"
    ;;
  revoked)
    unmeasured renewed "the job's own line says the server answered that the key is REVOKED, so the installation runs on Free and nothing was renewed: ${m1:-$m2}. A revoked key is a state \`fugaro licence set\` repairs with a new key (nothing has been deleted, and its tier and seats stay readable), and this check has nothing to renew while it holds. Check 561 is the one about revocation"
    stop "the installation's key is revoked, so this tick had nothing to renew"
    ;;
  manual)
    unmeasured renewed "the job's own line says the key renews MANUALLY (key $m1, renewal_mode $m2): K2.3's job checks a manual key and never renews it. Check 560 needs a SHORT-LIVED auto key, installed with \`fugaro licence set\`"
    stop "the key the job read is a manual one"
    ;;
  offline)
    unmeasured renewed "the job's own line says the key is OFFLINE (renewal_mode: none): nothing contacts the licence server for it. Check 560 measures the tick's own renewal, so its premise is an auto key"
    stop "the key is offline and contacts nothing"
    ;;
  unreachable)
    unmeasured renewed "the job's own line says the check of key $m1 did not complete, so the key keeps its state and nothing was renewed (only an explicit answer revokes, D2140): the route from the job to the licence server did not answer. A blocked route is what makes a revocation unable to reach this installation, so it is worth settling before reading this check as anything about the renewal: look at the installation's route to the licence server, then run the check again"
    stop "the job could not reach the licence server, so nothing was renewed"
    ;;
  renew_failed)
    unmeasured renewed "the job's own line says the licence server did not answer the RENEWAL of key $m1, so nothing was renewed and the key is unchanged: an error is not an answer (D2140), and the next run asks again. Settle the route to the licence server and run the check again"
    stop "the licence server did not answer the renewal"
    ;;
  unread_status)
    unmeasured renewed "the job's own line says the licence server answered the renewal with a status this Fugaro does not read, so nothing was renewed: a status is a field of a body, and a value outside the ones this build knows is a licence server newer than the installation's CLI. Run \`fugaro update\` to move this installation to the release that reads it, then run the check again"
    stop "the licence server answered a renewal status this Fugaro does not read"
    ;;
  *)
    unmeasured renewed "the job's own line matched '${kind:-nothing}', which this check has no row for, so what the job answered about the key was not measured. Read the job's lines yourself: gcloud logging read '$filter' --project $PROJECT --freshness 1h --limit 20"
    stop "the job's answer did not match a sentence this check knows"
    ;;
  esac
}

# step_status: the installation's own answer after the fire. The term is what the
# successor's key claims, and it is the half of "the installation shows the new
# key" that `licence status` can state: K3.2's document carries no key ID outside
# the check record, and the check the job runs comes BEFORE the renewal it makes
# (K4.1's order), so the last check names the key that was replaced.
step_status() {
  licence_status post
  local state source mode paid check_status check_key check_at
  state=$(field "$SCRATCH/post.txt" state)
  source=$(field "$SCRATCH/post.txt" source)
  mode=$(field "$SCRATCH/post.txt" renewal_mode)
  paid=$(field "$SCRATCH/post.txt" paid_through)
  check_status=$(field "$SCRATCH/post.txt" check_status)
  check_key=$(field "$SCRATCH/post.txt" check_key)
  check_at=$(field "$SCRATCH/post.txt" check_at)
  local check_note="the check record is unchanged from the read before the fire (${check_at:-none}, key ${check_key:-none}) — a window that opens between two checks renews without asking one"
  if [ -n "$check_at" ] && { [ -z "$PRE_CHECK_AT" ] || [ "$check_at" != "$PRE_CHECK_AT" ]; }; then
    check_note="and the job recorded its check at $check_at ($check_status) about key ${check_key:-none}, which is the key it replaced: K4.1's own order is the check first and the renewal after"
  fi
  if [ "$source" != project ]; then
    fact status FAIL "after the job renewed the key, \`licence status\` answers from source '${source:-none}' with state '${state:-none}': the successor the job installed is a PROJECT key, so an answer from anywhere else is an installation whose licence moved away while the job was writing it"
    stop "the installation's licence answers from a source that is not the project's after the renewal"
  fi
  if [ "$mode" != auto ]; then
    fact status FAIL "after the renewal the key's renewal_mode reads '${mode:-none}' rather than 'auto': the job does not renew a manual key, so the successor of an auto one is an auto one, and a mode that changed means the key the installation now runs on is not the successor of the key it replaced"
    stop "the renewal mode changed across the renewal"
  fi
  if [ "$state" != active ] && [ "$state" != grace ]; then
    fact status FAIL "after the job renewed the key, the installation reads '$state' (paid through ${paid:-nowhere}) rather than active or grace: the successor's term is what the installation runs on now, so a key whose state is not good is not the renewal the job reported"
    stop "the installation's state is not a good one after the renewal"
  fi
  local order=0
  ts_after "$paid" "$PRE_PAID" || order=$?
  case $order in
  0) ;;
  1)
    fact status FAIL "the job reported a renewal and the installation's term did not move: it was paid through $PRE_PAID before the fire and reads $paid after it. The successor's term is what the store keeps (T26), so a renewal whose term is not later either did not write the key or wrote one the installation already held"
    stop "the installation's paid-through did not move across the renewal the job reported"
    ;;
  *)
    fact status FAIL "the installation's term could not be compared: it read '$PRE_PAID' before the fire and '$paid' after it, and one of those is not a timestamp this check can read. \`licence status --json\` carries \`paid_through\` as RFC 3339 UTC; a value of any other shape is a document this check cannot read rather than a renewal that did not move"
    stop "the installation's paid-through before or after the fire was not a timestamp"
    ;;
  esac
  fact status PASS "the installation now reads: state $state, source $source, renewal_mode $mode, paid through $paid — strictly later than $PRE_PAID before the fire, which is the successor key's own term — tier $(field "$SCRATCH/post.txt" tier) and kind $(field "$SCRATCH/post.txt" kind); $check_note"
}

step_restore() {
  fact restore PASS "nothing of this check's own is left behind: its one write of any kind was the fire, which made the installation's own renewal (the successor key and its secret version in $PROJECT, kept on purpose as this installation's licence), and its scratch directory goes with the exit trap. It deleted nothing, wrote no object of the runs bucket itself, and read the licence object's bytes nowhere"
}

plan() {
  say "CHECK 560 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [local, no network] fugaro doctor --json in $SANDBOX — the Fugaro project, the GCP project and the region every read below is aimed with"
  say "  [network, read] gcloud scheduler jobs describe $SCHEDULER_JOB — the tick, its state, its target and the account its call is authorised as"
  say "  [network, read] gcloud run jobs describe $LICENCE_JOB in ${REGION:-<region>} — that the job is this installation's licence job (labels, argv 'licence renew --job', account, and the two names its preconditions pin)"
  say "  [network, read] fugaro licence status --json in $SANDBOX — the project's own auto key and the term it is paid through (no key is in that document)"
  say "  [network, WRITE] gcloud scheduler jobs run $SCHEDULER_JOB — the daily tick's own call, which starts ONE execution of the Cloud Run job"
  say "  [network, read] gcloud run jobs executions list — the execution that fire started, and its terminal condition"
  say "  [network, read] gcloud logging read over cloud_run_job/$LICENCE_JOB, for the execution this fire starts — the job's own line: the renewal it made, or the state it stopped in"
  say "  [network, read] fugaro licence status --json again — the term, strictly later than the read above"
  fact dry-run PASS "the plan was printed and nothing was run: no gcloud call was made, no execution was started and no licence was read"
}

main() {
  need_tools
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_inputs
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_sandbox
  step_project
  step_scheduler
  step_job
  step_premise
  step_execution
  step_renewed
  step_status
  step_restore
}

# ---------------------------------------------------------------------- inputs
# The two names K4.2 gives these resources, spelled as internal/infra/licence.go
# spells them: the job must not start with fugaro- (that is what `fugaro runs ls`
# counts as a workflow job), and the schedule job carries the prefix.
LICENCE_JOB=fugarolicence
SCHEDULER_JOB=fugaro-licence-renew

FUGARO_NAME=""
# The execution this run's own fire started. Every read of the job's lines below
# is filtered by its `run.googleapis.com/execution_name`, so the terminal line of
# an execution that ran earlier — last night's renewal, say — cannot be read as
# this fire's answer.
EXECUTION_NAME=""
PRE_PAID=""
PRE_KEY=""
PRE_CHECK_AT=""

DRY_RUN_CLI=""
SANDBOX_CLI=""
PROJECT_CLI=""
REGION_CLI=""
SCHEDULER_REGION_CLI=""
USAGE="check560.sh --sandbox <a checkout> [--project <gcp project>] [--region <region>] [--scheduler-region <region>] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path. usage: $USAGE"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --project)
    [ $# -ge 2 ] || die "--project needs a GCP project id. usage: $USAGE"
    PROJECT_CLI=$2
    shift 2
    ;;
  --region)
    [ $# -ge 2 ] || die "--region needs a region. usage: $USAGE"
    REGION_CLI=$2
    shift 2
    ;;
  --scheduler-region)
    [ $# -ge 2 ] || die "--scheduler-region needs a region. usage: $USAGE"
    SCHEDULER_REGION_CLI=$2
    shift 2
    ;;
  --production)
    PRODUCTION=1
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
    die "unknown argument '$1'. usage: $USAGE"
    ;;
  esac
done

SANDBOX=${CHECK560_SANDBOX:-${SANDBOX_CLI:-}}
PROJECT=${CHECK560_PROJECT:-${PROJECT_CLI:-}}
REGION=${CHECK560_REGION:-${REGION_CLI:-}}
SCHEDULER_REGION=${CHECK560_SCHEDULER_REGION:-${SCHEDULER_REGION_CLI:-}}
PRODUCTION=${CHECK560_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK560_DRY_RUN:-${DRY_RUN_CLI:-}}
WAIT=${CHECK560_WAIT:-600}
POLL=${CHECK560_POLL:-15}
LOG_WAIT=${CHECK560_LOG_WAIT:-90}
LOG_FRESHNESS=${CHECK560_LOG_FRESHNESS:-30m}
KEEP_SCRATCH=${CHECK560_KEEP_SCRATCH:-}

if [ -z "$SANDBOX" ]; then
  die "Check 560 needs --sandbox: the checkout of the installation being measured (the one whose short-lived auto key this check renews). Usage: $USAGE"
fi
# Absolute, before anything changes directory: the CLI below is run with this as
# its working directory, and a relative path would name another one after a cd.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
if [ ! -d "$SANDBOX" ]; then
  die "the sandbox directory $SANDBOX is not there. --sandbox takes the checkout of the installation being measured. Usage: $USAGE"
fi

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
