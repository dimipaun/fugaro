#!/usr/bin/env bash
# Check 341 — the reports job's first firing, and what it stored: with
# `jobs.reports.paused: false` in the project layer and `fugaro update` applied,
# the project's own reports job fires once on its schedule, and the reader takes
# what it stored — one report per repository the job's own assignment names,
# under `reports/<slug>/<YYYY>/<key>.md`, the project aggregate at
# `reports/<YYYY>/<key>.md` beside them, and `reports/` reaped later than the
# run records' own 90 days, which is the installation's press longer `reports/`
# lifecycle rule doing what it is for. The stored report is then read back with
# `fugaro runs digest --stored` — the reading **Open item 5** of the
# periodic-reviews plan turns on, since **E4** makes this job the only writer of
# `reports/` and that is what R10's write half waits for.
#
# The periodic-reviews plan's §P8, its Check 341, and the plan's rulings E1, E3,
# E4 and R13; the step list is the section of docs/gcp-live-checklist.md below.
# Nothing here is a claim that the job fires: the check turns it on, waits, and
# reads what landed.
#
# Usage:      bash scripts/live-checks/check341.sh [--repo <owner/name>] [--key <period>] [--wait-minutes N] [--scheduler-region <region>] [--preflight] [--keep-scratch]
#             bash scripts/live-checks/check341.sh --help
#             --repo   the repository whose stored report is read back; default is this checkout's origin.
#             --key    the period read back (2026-11-20, 2026-W47, 2026-11 or 2026); default is yesterday's day.
#             --wait-minutes  how long to wait for the schedule to fire (default 20). The schedule fires at
#                      05:37 UTC, so run this shortly before it or raise this.
#             --scheduler-region  where the Cloud Scheduler job lives; default is the installation's own
#                      region, which is what Fugaro writes for a region Scheduler serves.
#             --preflight  reads every precondition and prints the blockers; writes and launches nothing.
# Needs:      bash, mktemp, python3 (every `--json` document is read with it), git (the
#             checkout's origin, when --repo is not given), fugaro, gcloud (authorised for the
#             installation's own project) and terraform's own run behind `fugaro update`.
# Where:      your own machine, against the installation named `fugaro` in the GCP project
#             `fugaro-dev` — both hard-coded here — and never `belong`, EdgeWeb or EdgeServer.
#             The check refuses a doctor that names another installation or project, and it
#             reads no bucket but that installation's own runs bucket.
# Costs:      one scheduled firing of the project's own reports job (no model call, no run and
#             no pull request), and TWO `fugaro update` runs: the one that starts the schedule
#             and the one that puts it back. What the firing writes is ordinary stored reports
#             in the project's own `reports/` prefix, which the bucket's own rule reaps.
# Runs:       by YOU. No agent runs any step of it, and it is the check that turns a job on:
#             its own test (check341_test.sh) drives it against fakes on PATH and reaches no
#             cloud, no bucket and no Fugaro installation.
# Cleanup:    `jobs.reports.paused` goes back to the value it had and `fugaro update` is run
#             again, read back from the schedule itself — on a stop as well as on a pass. The
#             stored reports it wrote are meant to stay: the bucket's rule reaps them, and
#             deleting them by hand would take this check's own reading. Nothing else is
#             created: no account, no binding, no credential.
# Output:     progress on stderr, ONE block on stdout headed CHECK 341 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        every criterion holds;
#               FAIL (exit 1)        a criterion was refuted, and the row names it;
#               INCOMPLETE (exit 2)  the run stopped before it could read everything;
#               INCOMPLETE (exit 3)  a read did not answer (UNVERIFIED).
#
# CHECK341_FUGARO, CHECK341_GCLOUD, CHECK341_REPO, CHECK341_KEY, CHECK341_WAIT_MINUTES,
# CHECK341_POLL_SECONDS, CHECK341_SCHEDULER_REGION and CHECK341_KEEP_SCRATCH exist for this
# script's own test and an owner's patience.
set -Eeuo pipefail

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

FUGARO=${CHECK341_FUGARO:-fugaro}
GCLOUD=${CHECK341_GCLOUD:-gcloud}
INSTALLATION=fugaro
GCP_PROJECT=fugaro-dev
BUCKET=fugaro-runs-fugaro-dev
JOB=fugarorpt-reports
SCHED=fugaro-reports
REPORTS_ACCOUNT=fugaro-reports
SCHEDULER_ACCOUNT=fugaro-scheduler
JOB_DOC_ENV=FUGARO_REPORTS_JOB_SPEC
REPO=${CHECK341_REPO:-}
KEY=${CHECK341_KEY:-}
WAIT_MINUTES=${CHECK341_WAIT_MINUTES:-20}
POLL_SECONDS=${CHECK341_POLL_SECONDS:-30}
SCHEDULER_REGION=${CHECK341_SCHEDULER_REGION:-}
KEEP_SCRATCH=${CHECK341_KEEP_SCRATCH:-}

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
PREFLIGHT=no
REACHED_END=no
ABORT=""
REGION=""
UNPAUSED=no
UPDATE_FAILED=no
PREVIOUS_PAUSED=""
PREVIOUS_WINS=""
POWERED_AT=""
DOC_PROJECT=""
DOC_BUCKET=""
DOC_PERIODS=""
SCHED_LOC=""
RESTORE_WORDS=()

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY STATE "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
  -h | --help)
    usage
    exit 0
    ;;
  --preflight)
    PREFLIGHT=yes
    ;;
  --keep-scratch)
    KEEP_SCRATCH=1
    ;;
  --repo)
    [ $# -ge 2 ] || stop "--repo needs owner/name"
    REPO=$2
    shift
    ;;
  --key)
    [ $# -ge 2 ] || stop "--key needs a period key"
    KEY=$2
    shift
    ;;
  --wait-minutes)
    [ $# -ge 2 ] || stop "--wait-minutes needs a number"
    WAIT_MINUTES=$2
    shift
    ;;
  --scheduler-region)
    [ $# -ge 2 ] || stop "--scheduler-region needs a region"
    SCHEDULER_REGION=$2
    shift
    ;;
  *)
    stop "unknown argument: $1 (see --help)"
    ;;
  esac
  shift
done

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ -n "$KEEP_SCRATCH" ]; then
    say "kept the scratch directory at $SCRATCH (--keep-scratch is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() {
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

print_block() {
  printf '\nCHECK 341 RESULT\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf 'FACT %s: %s — %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 341 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  if [ "$PREFLIGHT" = yes ]; then
    exit "$status"
  fi
  # The schedule goes back before anything is printed: a check that left the
  # project's reports job firing would be worse than no check at all.
  restore_now
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT judged"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"scratch|PASS|the scratch directory this check read into is gone"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch|INFO|the scratch directory is kept at ${SCRATCH}"
  fi
  local v
  v=$(verdict "$facts")
  if [ -n "$ABORT" ] || [ "$REACHED_END" = no ]; then
    status=2
  elif [ "$v" = FAIL ]; then
    status=1
  elif [ "$v" = INCOMPLETE ]; then
    status=3
  else
    status=0
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# --------------------------------------------------------------------- guards
guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell arrived with tracing on (bash -x): a trace prints every document this check reads, so run it without -x"
  fact xtrace PASS "no xtrace in this shell"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!marker:-}" ]; then found="${found}${found:+, }$marker"; fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session: Check 341 is yours to run at your own terminal, and it turns a job on"
  fact agent-session PASS "no coding agent's marker in this shell"
}

need_tools() {
  local tool missing=""
  for tool in mktemp python3 "$FUGARO" "$GCLOUD"; do
    command -v "$tool" >/dev/null 2>&1 || missing="${missing}${missing:+, }$tool"
  done
  [ -n "$REPO" ] || command -v git >/dev/null 2>&1 || missing="${missing}${missing:+, }git"
  [ -z "$missing" ] || stop "these tools are not on PATH: $missing"
  fact tools PASS "mktemp, python3, $FUGARO and $GCLOUD are on PATH"
}

guard_installation() {
  local doc name project
  doc=$("$FUGARO" doctor --json 2>/dev/null) || stop "fugaro doctor --json did not answer, so which installation this is cannot be read: fix it with $FUGARO doctor"
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null) ||
    stop "fugaro doctor --json did not carry a project block: fix it with $FUGARO doctor"
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  REGION=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("region",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    stop "this installation is ${name:-?} in ${project:-?}, and Check 341 never runs against belong, EdgeWeb or EdgeServer: run it on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] ||
    stop "fugaro doctor names the installation ${name:-nothing}, and Check 341 runs on $INSTALLATION only: run it on $INSTALLATION"
  [ "$project" = "$GCP_PROJECT" ] ||
    stop "fugaro doctor names the GCP project ${project:-nothing}, and Check 341 runs in $GCP_PROJECT only: run it on the installation $INSTALLATION in $GCP_PROJECT"
  [ -n "$REGION" ] ||
    stop "fugaro doctor names no region for this installation, so the reports job's own region cannot be read: fix it with $FUGARO doctor"
  fact installation PASS "installation $INSTALLATION in $GCP_PROJECT, region $REGION"
}

resolve_repo() {
  if [ -z "$REPO" ]; then
    local url
    url=$(git remote get-url origin 2>/dev/null) || stop "this directory is not a checkout of a repository and no --repo was given: pass --repo <owner/name> or run the check in the repository's checkout"
    REPO=$(printf '%s' "$url" | python3 -c 'import sys,re
u=re.sub(r"\.git$","",sys.stdin.read().strip()).rstrip("/")
parts=re.split(r"[/:]",u)
print(parts[-2]+"/"+parts[-1] if len(parts)>=2 and parts[-2] and parts[-1] else "")')
    [ -n "$REPO" ] || stop "this checkout's origin ($url) names no repository as owner/name: pass --repo <owner/name>"
  fi
  case "$REPO" in
  *belong* | *edgeweb* | *edgeserver*)
    stop "the repository is $REPO, and Check 341 never runs against belong, EdgeWeb or EdgeServer"
    ;;
  esac
  fact repository PASS "repository $REPO"
}

resolve_key() {
  [ -n "$KEY" ] || KEY=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc).date()-datetime.timedelta(days=1)).isoformat())')
  python3 - "$KEY" <<'PY' >/dev/null 2>&1 || stop "--key $KEY is not a period key this CLI writes (2026-11-20, 2026-W47, 2026-11 or 2026)"
import datetime,sys
key=sys.argv[1].strip()
if len(key)==10 and key[4]=="-": datetime.date.fromisoformat(key)
elif "W" in key:
    y,w=key.split("-W"); datetime.date.fromisocalendar(int(y),int(w),1)
elif len(key)==7: datetime.date(int(key.split("-")[0]),int(key.split("-")[1]),1)
elif len(key)==4: datetime.date(int(key),1,1)
else: raise SystemExit(1)
PY
  fact period PASS "the stored report read back is $KEY's"
}

# ------------------------------------------------------------------- the reads
# The reports job's own assignment, read off the deployed job: the repositories
# it reports (one report each), the periods it closes, and the bucket it writes.
# It is what the store is compared with, so the expected set is the plan's own
# and not a list this check guessed.
job_read() { # -> writes $SCRATCH/job.json; 1 when gcloud does not answer
  "$GCLOUD" run jobs describe "$JOB" --region "$REGION" --project "$GCP_PROJECT" --format=json >"$SCRATCH/job.json" 2>"$SCRATCH/job.err" || return 1
  python3 - "$SCRATCH/job.json" "$JOB_DOC_ENV" "$SCRATCH/job-doc.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); want=sys.argv[2]
t=(d.get("template") or {}).get("template") or {}
c=(t.get("containers") or [{}])[0]
args=" ".join(c.get("args") or [])
doc=""
for e in c.get("env") or []:
    if e.get("name")==want: doc=e.get("value") or ""
open(sys.argv[3],"w").write(doc)
print("\t".join([args, t.get("serviceAccount",""), t.get("timeout","")]))
PY
}

doc_read() { # -> "$version $project $bucket $nrepos $nperiods"; writes $SCRATCH/repos.txt; 1 when unreadable
  python3 - "$SCRATCH/job-doc.json" "$SCRATCH/repos.txt" <<'PY'
import json,sys
raw=open(sys.argv[1]).read()
if not raw.strip(): sys.exit(1)
try: d=json.loads(raw)
except ValueError: sys.exit(1)
repos=[r for r in (d.get("repositories") or []) if r]
periods=[p for p in (d.get("periods") or []) if p]
open(sys.argv[2],"w").write("\n".join(sorted(repos))+"\n" if repos else "")
print("\t".join([str(d.get("version","")), d.get("project",""), d.get("bucket",""), str(len(repos)), " ".join(periods)]))
PY
}

# Where the Scheduler job may live: the location given, else the installation's
# own region, else the fixed nearby location Cloud Scheduler serves for a Cloud
# Run region it is not offered in (internal/backend/gcp's nearestSchedulerRegion,
# which is what Fugaro itself resolves and what `fugaro update` writes). The
# fallback is tried only when the first read fails, so a region that serves
# Scheduler is never second-guessed.
scheduler_candidates() {
  printf '%s\n' "${SCHEDULER_REGION:-$REGION}"
  [ -n "$SCHEDULER_REGION" ] && return 0
  case "$REGION" in
  us-east5) printf 'us-east4\n' ;;
  us-west8) printf 'us-west4\n' ;;
  northamerica-south1) printf 'us-south1\n' ;;
  europe-north1 | europe-north2) printf 'europe-central2\n' ;;
  europe-southwest1) printf 'europe-west9\n' ;;
  europe-west10) printf 'europe-west3\n' ;;
  australia-southeast2) printf 'australia-southeast1\n' ;;
  southamerica-west1) printf 'southamerica-east1\n' ;;
  esac
}

scheduler_read() { # -> "$state $schedule $tz $uriJob"; 1 when gcloud does not answer anywhere tried
  local loc
  for loc in $(scheduler_candidates); do
    if "$GCLOUD" scheduler jobs describe "$SCHED" --location "$loc" --project "$GCP_PROJECT" --format=json >"$SCRATCH/scheduler.json" 2>"$SCRATCH/scheduler.err"; then
      SCHED_LOC=$loc
      break
    fi
  done
  [ -n "$SCHED_LOC" ] || return 1
  python3 - "$SCRATCH/scheduler.json" "$SCHED_LOC" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
uri=(d.get("httpTarget") or {}).get("uri","")
job=uri.rsplit("/",1)[-1].split(":")[0] if uri else ""
print("\t".join([sys.argv[2], d.get("state",""), d.get("schedule",""), d.get("timeZone",""), job]))
PY
}

executions_read() { # -> 0 and $SCRATCH/firing.txt when one started after $POWERED_AT
  "$GCLOUD" run jobs executions list --project "$GCP_PROJECT" --region "$REGION" --job "$JOB" --limit 20 --format=json >"$SCRATCH/executions.json" 2>"$SCRATCH/executions.err" || return 2
  python3 - "$SCRATCH/executions.json" "$POWERED_AT" "$SCRATCH/firing.txt" <<'PY'
import json,sys,datetime
def ts(s):
    if not s: return None
    s=s.replace("Z","+00:00")
    for cand in (s, s.split(".")[0]+"+00:00"):
        try: return datetime.datetime.fromisoformat(cand)
        except ValueError: continue
    return None
try: docs=json.load(open(sys.argv[1]))
except ValueError: sys.exit(2)
if isinstance(docs,dict): docs=docs.get("executions") or []
power=ts(sys.argv[2])
best=None
for e in docs:
    st=ts(e.get("startTime"))
    if st is None or power is None or st<=power: continue
    if best is None or st>ts(best.get("startTime")): best=e
if best is None: sys.exit(1)
state=""
for c in ((best.get("status") or {}).get("conditions") or []):
    if c.get("type")=="Completed": state=c.get("state","")
open(sys.argv[3],"w").write("\t".join([
    (best.get("name") or "").rsplit("/",1)[-1], best.get("startTime",""), state,
    str(best.get("completionTime") or "")]))
PY
}

# ---------------------------------------------------------------------- steps
step_setting() {
  local doc fields value
  doc=$("$FUGARO" settings get layer.jobs.reports.paused --json 2>"$SCRATCH/setting.err") ||
    stop "fugaro settings get layer.jobs.reports.paused --json did not answer: $(head -c 200 "$SCRATCH/setting.err")"
  fields=$(printf '%s' "$doc" | python3 -c 'import json,sys
d=json.load(sys.stdin)
rows=[r for r in (d.get("settings") or []) if r.get("key")=="layer.jobs.reports.paused"]
r=rows[0] if rows else {}
v=r.get("value")
if v is None: v=((r.get("default") or {}).get("value"))
print("%s\t%s" % (v or "", r.get("wins","")))' 2>/dev/null) ||
    stop "fugaro settings get printed a document this check cannot read: run $FUGARO settings get layer.jobs.reports.paused"
  value=${fields%%$'\t'*}
  PREVIOUS_PAUSED=$value
  PREVIOUS_WINS=${fields##*$'\t'}
  case "$value" in
  true | false)
    fact setting PASS "layer.jobs.reports.paused reads $value (wins: ${PREVIOUS_WINS:-unknown}) before the check writes anything"
    ;;
  *)
    stop "layer.jobs.reports.paused read as '${value:-nothing}', which is not a boolean: set it yourself with $FUGARO settings set layer.jobs.reports.paused true --layer project"
    ;;
  esac
}

# The check's one write, and the update that renders it into the schedule. The
# value read back is what decides whether the restore is owed: a write that
# failed after touching the layer file still has to be put back.
step_power() {
  if [ "$PREVIOUS_PAUSED" = false ]; then
    fact power INFO "the layer already said false; the check still needs a firing after its own update"
  fi
  if ! "$FUGARO" settings set layer.jobs.reports.paused false --layer project >"$SCRATCH/set.log" 2>&1; then
    fact power FAIL "fugaro settings set layer.jobs.reports.paused false --layer project did not land: $(head -c 200 "$SCRATCH/set.log")"
    UPDATE_FAILED=yes
    return 0
  fi
  local now
  now=$("$FUGARO" settings get layer.jobs.reports.paused --json 2>/dev/null |
    python3 -c 'import json,sys
d=json.load(sys.stdin)
rows=[r for r in (d.get("settings") or []) if r.get("key")=="layer.jobs.reports.paused"]
r=rows[0] if rows else {}
v=r.get("value")
if v is None: v=((r.get("default") or {}).get("value"))
print(v or "")' 2>/dev/null) || now=""
  [ "$now" = false ] && UNPAUSED=yes
  if [ "$UNPAUSED" = no ]; then
    fact power FAIL "the layer file still reads ${now:-unreadable} after the write, so the schedule was not started: $(head -c 200 "$SCRATCH/set.log")"
    UPDATE_FAILED=yes
    return 0
  fi
  fact power PASS "layer.jobs.reports.paused is now false in the project layer (it read $PREVIOUS_PAUSED)"
  say "layer.jobs.reports.paused is false; running fugaro update (the schedule is planned from it)"
  if ! "$FUGARO" update >"$SCRATCH/update.log" 2>&1; then
    fact update FAIL "fugaro update did not apply, so the schedule is still what it was: $(tail -c 200 "$SCRATCH/update.log" | tr '\n' ' ')"
    UPDATE_FAILED=yes
    return 0
  fi
  POWERED_AT=$(python3 -c 'import datetime; print(datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))')
  local said
  said=$(grep -i -m 3 'report' "$SCRATCH/update.log" 2>/dev/null | tr '\n' ' ' | head -c 300) || said=""
  fact update PASS "fugaro update applied the layer with the reports job unpaused at $POWERED_AT; it said: ${said:-nothing about reports}"
}

step_scheduler() {
  local line loc state schedule tz uri
  line=$(scheduler_read) || {
    fact scheduler UNVERIFIED "no schedule $SCHED reads in $(scheduler_candidates | tr '\n' ' '): $(head -c 160 "$SCRATCH/scheduler.err" 2>/dev/null || true) -- pass the region the scheduler job really lives in: --scheduler-region <region>"
    return 0
  }
  loc=${line%%$'\t'*}
  line=${line#*$'\t'}
  state=${line%%$'\t'*}
  line=${line#*$'\t'}
  schedule=${line%%$'\t'*}
  line=${line#*$'\t'}
  tz=${line%%$'\t'*}
  uri=${line##*$'\t'}
  case "$state" in
  ENABLED | PAUSED) ;;
  *)
    fact scheduler UNVERIFIED "the schedule $SCHED reads state '${state:-nothing}', which is neither ENABLED nor PAUSED: read it yourself with $GCLOUD scheduler jobs describe $SCHED --location ${SCHEDULER_REGION:-$REGION} --project $GCP_PROJECT"
    return 0
    ;;
  esac
  if [ "$state" = PAUSED ]; then
    fact scheduler FAIL "the schedule $SCHED still reads PAUSED after fugaro update, so nothing will fire: write jobs.reports.paused = false in the project layer and run $FUGARO update"
  elif [ "$uri" != "$JOB" ]; then
    fact scheduler FAIL "the schedule $SCHED fires '${uri:-nothing}' and not the reports job $JOB: run $FUGARO update"
  else
    fact scheduler PASS "the schedule $SCHED in $loc is ENABLED, $schedule ($tz), and fires $JOB"
  fi
}

step_job() {
  local line args account timeout
  if ! line=$(job_read); then
    fact job FAIL "the reports job $JOB is not on this installation: $(head -c 200 "$SCRATCH/job.err") -- it is planned from the layer's jobs.reports.paused, so set that and run $FUGARO update"
    UPDATE_FAILED=yes
    return 0
  fi
  args=${line%%$'\t'*}
  line=${line#*$'\t'}
  account=${line%%$'\t'*}
  timeout=${line##*$'\t'}
  if [ "$args" != "runs reports --job" ]; then
    fact job FAIL "the reports job's argv is '$args' and not 'runs reports --job': run $FUGARO update"
  elif [ "$account" != "$REPORTS_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com" ]; then
    fact job FAIL "the reports job runs as '$account', and the installation's own reports account is $REPORTS_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com: run $FUGARO update"
  else
    fact job PASS "the reports job runs '$args' as $REPORTS_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com, timeout $timeout"
  fi
}

# The job's assignment: every repository it reports (one report each, E1), the
# periods it closes, and the bucket it writes. It is the expectation the store
# is read against.
step_assignment() {
  local line
  line=$(doc_read) || {
    fact assignment FAIL "the reports job carries no readable $JOB_DOC_ENV: run $FUGARO update"
    UPDATE_FAILED=yes
    return 0
  }
  line=${line#*$'\t'}
  DOC_PROJECT=${line%%$'\t'*}
  line=${line#*$'\t'}
  DOC_BUCKET=${line%%$'\t'*}
  line=${line#*$'\t'}
  DOC_REPOS=${line%%$'\t'*}
  DOC_PERIODS=${line##*$'\t'}
  if [ "$DOC_PROJECT" != "$GCP_PROJECT" ] || [ "$DOC_BUCKET" != "$BUCKET" ]; then
    fact assignment FAIL "the job reports into $DOC_PROJECT/$DOC_BUCKET and this check reads $GCP_PROJECT/$BUCKET: run $FUGARO update, and never point this check at another project"
  else
    fact assignment PASS "the job reports $DOC_REPOS repositories ($DOC_PERIODS) into $DOC_BUCKET"
  fi
}

step_grants() {
  local writers invokers
  if ! "$GCLOUD" storage buckets get-iam-policy "gs://$BUCKET" --project "$GCP_PROJECT" --format=json >"$SCRATCH/bucket-iam.json" 2>"$SCRATCH/bucket-iam.err"; then
    fact grants UNVERIFIED "the runs bucket's IAM policy did not answer: $(head -c 200 "$SCRATCH/bucket-iam.err")"
    return 0
  fi
  writers=$(python3 - "$SCRATCH/bucket-iam.json" "$REPORTS_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com" <<'PY'
import json,sys
writable={"roles/storage.objectUser","roles/storage.objectAdmin","roles/storage.admin","roles/storage.legacyBucketWriter"}
d=json.load(open(sys.argv[1]))
me=sys.argv[2]
found=[]
for b in d.get("bindings") or []:
    if b.get("role") not in writable: continue
    if "objects/reports/" not in ((b.get("condition") or {}).get("expression") or ""): continue
    found += list(b.get("members") or [])
print(" ".join(sorted(set(found))) if found else "")
PY
  ) || writers=""
  if [ "$writers" = "serviceAccount:$REPORTS_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com" ]; then
    fact grants PASS "the reports job's account is the only writer of reports/ on the runs bucket (E4)"
  elif [ -z "$writers" ]; then
    fact grants FAIL "nobody may write reports/, so the job cannot store a report: run $FUGARO update"
  else
    fact grants FAIL "the writers of reports/ are '$writers', and E4 makes $REPORTS_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com the prefix's only one: run $FUGARO update"
  fi
  if ! "$GCLOUD" run jobs get-iam-policy "$JOB" --region "$REGION" --project "$GCP_PROJECT" --format=json >"$SCRATCH/job-iam.json" 2>"$SCRATCH/job-iam.err"; then
    fact invoker UNVERIFIED "the job's own IAM policy did not answer: $(head -c 200 "$SCRATCH/job-iam.err")"
    return 0
  fi
  invokers=$(python3 - "$SCRATCH/job-iam.json" "$SCHEDULER_ACCOUNT@$GCP_PROJECT.iam.gserviceaccount.com" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); want="serviceAccount:"+sys.argv[2]
ok=False
for b in d.get("bindings") or []:
    if b.get("role")!="roles/run.invoker": continue
    if want in (b.get("members") or []): ok=True
print("yes" if ok else "no")
PY
  ) || invokers=""
  if [ "$invokers" = yes ]; then
    fact invoker PASS "the installation's scheduler account holds roles/run.invoker on this job only"
  else
    fact invoker FAIL "the scheduler account does not hold roles/run.invoker on $JOB, so a firing would be refused: run $FUGARO update"
  fi
}

step_fired() {
  if [ "$UPDATE_FAILED" = yes ]; then
    fact fired UNVERIFIED "nothing was started, so no firing is expected: read the rows above for the step that did not land"
    return 0
  fi
  local deadline found=no rc=0
  deadline=$(($(date +%s) + WAIT_MINUTES * 60))
  say "waiting up to ${WAIT_MINUTES} min for $SCHED to fire (the schedule is read above)"
  while :; do
    rc=0
    executions_read || rc=$?
    case "$rc" in
    0)
      found=yes
      break
      ;;
    1) : ;;
    *)
      fact fired UNVERIFIED "the job's executions did not read back: $(head -c 200 "$SCRATCH/executions.err")"
      return 0
      ;;
    esac
    [ "$(date +%s)" -lt "$deadline" ] || break
    sleep "$POLL_SECONDS"
  done
  if [ "$found" = no ]; then
    fact fired UNVERIFIED "no execution of $JOB started after $POWERED_AT within ${WAIT_MINUTES} min: run this check shortly before the schedule's own time (read above), raise --wait-minutes, or fire it now with $GCLOUD scheduler jobs run $SCHED --location ${SCHEDULER_REGION:-$REGION} --project $GCP_PROJECT"
    return 0
  fi
  local id start state done_at
  id=$(cut -f1 "$SCRATCH/firing.txt")
  start=$(cut -f2 "$SCRATCH/firing.txt")
  state=$(cut -f3 "$SCRATCH/firing.txt")
  done_at=$(cut -f4 "$SCRATCH/firing.txt")
  case "$state" in
  CONDITION_SUCCEEDED)
    fact fired PASS "execution $id fired at $start and completed (${done_at:-no completion time})"
    ;;
  "")
    fact fired UNVERIFIED "execution $id started at $start and has not reported a terminal state yet: read it with $GCLOUD run jobs executions describe $id --region $REGION --project $GCP_PROJECT"
    ;;
  *)
    fact fired FAIL "execution $id started at $start and ended $state, so nothing was stored: read its log with $GCLOUD run jobs executions describe $id --region $REGION --project $GCP_PROJECT"
    ;;
  esac
}

step_store() {
  if ! "$GCLOUD" storage ls --recursive --project "$GCP_PROJECT" "gs://$BUCKET/reports/" >"$SCRATCH/store.out" 2>"$SCRATCH/store.err"; then
    fact store UNVERIFIED "gs://$BUCKET/reports/ did not list: $(head -c 200 "$SCRATCH/store.err")"
    return 0
  fi
  local summary
  summary=$(python3 - "$SCRATCH/store.out" "$SCRATCH/repos.txt" <<'PY'
import os,sys
prefix="reports/"
names=[]
for line in open(sys.argv[1]):
    line=line.strip()
    if not line.startswith("gs://"): continue
    p=line.split("/",3)
    if len(p)<4: continue
    n=p[3]
    if n.startswith(prefix) and n.endswith(".md"): names.append(n[len(prefix):])
want=set(l for l in open(sys.argv[2]).read().split() if l) if os.path.exists(sys.argv[2]) else set()
repos={}; aggs=set()
for n in names:
    parts=n.split("/")
    if len(parts)==3: repos.setdefault(parts[0],set()).add(parts[2])
    elif len(parts)==2: aggs.add(parts[1])
found=set(repos)
keys=set()
for s in repos.values(): keys |= s
print("\t".join([
    str(len(names)), str(len(found)), str(len(want)),
    ",".join(sorted(want-found)) or "-",
    ",".join(sorted(keys-aggs)) or "-",
    ",".join(sorted(found-want)) or "-"]))
PY
  ) || summary=""
  if [ -z "$summary" ]; then
    fact store FAIL "the listing under gs://$BUCKET/reports/ did not read as report names: read it yourself with $GCLOUD storage ls --recursive gs://$BUCKET/reports/"
    return 0
  fi
  local total nslugs nwant missing_slugs missing_aggs extra
  total=${summary%%$'\t'*}
  summary=${summary#*$'\t'}
  nslugs=${summary%%$'\t'*}
  summary=${summary#*$'\t'}
  nwant=${summary%%$'\t'*}
  summary=${summary#*$'\t'}
  missing_slugs=${summary%%$'\t'*}
  summary=${summary#*$'\t'}
  missing_aggs=${summary%%$'\t'*}
  extra=${summary##*$'\t'}
  if [ "$total" = 0 ]; then
    fact store FAIL "the firing stored nothing under gs://$BUCKET/reports/: read the job's own row and the execution's log above"
    return 0
  fi
  if [ "$missing_slugs" != "-" ]; then
    fact store FAIL "$missing_slugs of the job's own repositories have no stored report while $nslugs do (E1 asks one each): read it with $FUGARO runs digest --stored $KEY --repo <owner/name>"
  elif [ "$missing_aggs" != "-" ]; then
    fact store FAIL "no project aggregate beside the repository reports of $missing_aggs (E2): the aggregate is reports/<YYYY>/<key>.md at the top of the prefix"
  else
    fact store PASS "$total report(s) under reports/ for $nslugs of the assignment's $nwant repositories, and every key a repository holds has the project's aggregate beside it"
  fi
  [ "$extra" = "-" ] ||
    fact store-extra INFO "$extra hold a stored report and are not in the job's assignment: either they ran before they left the project, or the assignment is stale"
}

step_stored() {
  local out
  out=$("$FUGARO" runs digest --stored "$KEY" --repo "$REPO" 2>"$SCRATCH/stored.err") || {
    fact stored FAIL "the stored report of $KEY does not read back with your own account, which is Open item 5's answer no: $(head -c 200 "$SCRATCH/stored.err")"
    return 0
  }
  [ -n "$out" ] || {
    fact stored FAIL "the stored report of $KEY read back empty: read it with $FUGARO runs digest --stored $KEY --repo $REPO"
    return 0
  }
  fact stored PASS "the stored report of $KEY reads back with your own account ($(printf '%s' "$out" | wc -c | tr -d ' ') bytes), so R10's read half holds against what the job wrote"
}

step_retention() {
  local line
  if ! "$GCLOUD" storage buckets describe "gs://$BUCKET" --project "$GCP_PROJECT" --format=json >"$SCRATCH/bucket.json" 2>"$SCRATCH/bucket.err"; then
    fact retention UNVERIFIED "the runs bucket's own rule did not read back: $(head -c 200 "$SCRATCH/bucket.err")"
    return 0
  fi
  line=$(python3 - "$SCRATCH/bucket.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
def age_for(prefix):
    for r in ((d.get("lifecycle") or {}).get("rule") or []):
        c=r.get("condition") or {}
        if (r.get("action") or {}).get("type")!="Delete": continue
        pre=c.get("matchesPrefix") or []
        if any(p==prefix or p==prefix+"/" or p.startswith(prefix) for p in pre):
            a=c.get("age")
            if a is not None: return int(a)
    return None
runs=age_for("runs"); reports=age_for("reports")
print("%s\t%s" % (runs if runs is not None else "-", reports if reports is not None else "-"))
PY
  ) || line=""
  if [ -z "$line" ]; then
    fact retention UNVERIFIED "the bucket's lifecycle rules did not read as rules: read them with $GCLOUD storage buckets describe gs://$BUCKET --format=json"
    return 0
  fi
  local runs_days reports_days
  runs_days=${line%%$'\t'*}
  reports_days=${line##*$'\t'}
  if [ "$reports_days" = "-" ]; then
    fact retention FAIL "the runs bucket deletes nothing under reports/, so the stored reports stay for ever: run $FUGARO update (E3 plans the prefix's own rule)"
  elif [ "$runs_days" = "-" ] || [ "$reports_days" -gt "$runs_days" ]; then
    fact retention PASS "reports/ is deleted at $reports_days days and the run records at ${runs_days} days, so a stored report outlives the records it summarises (E3)"
  else
    fact retention FAIL "reports/ is deleted at $reports_days days, which is not after the run records' ${runs_days} days: the stored reports go with the records they summarise -- run $FUGARO update"
  fi
}

# The schedule goes back to what the check found, and the update that renders it
# again: run from the exit trap, so a stop puts it back too.
restore_now() {
  [ "$UNPAUSED" = yes ] || return 0
  # Back to EXACTLY what the layer said and not merely to a true: a key the
  # project layer did not carry before the check is unset again, and a layer
  # that already said false is written false again -- an owner whose reports
  # schedule was already running is not paused by having this check run.
  local prev=${PREVIOUS_PAUSED:-true} words
  if [ "$PREVIOUS_WINS" = default ]; then
    RESTORE_WORDS=(settings unset layer.jobs.reports.paused --layer project)
  else
    RESTORE_WORDS=(settings set layer.jobs.reports.paused "$prev" --layer project)
  fi
  words="fugaro ${RESTORE_WORDS[*]}"
  if ! "$FUGARO" "${RESTORE_WORDS[@]}" >"$SCRATCH/restore-set.log" 2>&1; then
    fact restore FAIL "the layer's jobs.reports.paused could not be put back ($(head -c 160 "$SCRATCH/restore-set.log")): run $words and fugaro update"
    return 0
  fi
  if ! "$FUGARO" update >"$SCRATCH/restore-update.log" 2>&1; then
    fact restore FAIL "the setting is back but fugaro update did not apply it, so the schedule may still be running: run fugaro update"
    return 0
  fi
  local line state
  if ! line=$(scheduler_read); then
    fact restore UNVERIFIED "the setting is back and fugaro update ran, but the schedule could not be read back: read it with $GCLOUD scheduler jobs describe $SCHED --location ${SCHEDULER_REGION:-$REGION} --project $GCP_PROJECT"
    return 0
  fi
  state=${line#*$'\t'}
  state=${state%%$'\t'*}
  # The schedule must read back what the layer now says: a layer that said false
  # before the check was reporting already, so ENABLED is the restore, not a
  # failure to pause it again.
  local want=PAUSED
  if [ "$PREVIOUS_WINS" != default ] && [ "$prev" = false ]; then want=ENABLED; fi
  if [ "$state" = "$want" ]; then
    if [ "$want" = PAUSED ]; then
      fact restore PASS "the layer is back as it was ($words) and $SCHED reads PAUSED again"
    else
      fact restore PASS "the layer is back as it was ($words) and $SCHED reads ENABLED again, as it did before the check"
    fi
  elif [ "$want" = PAUSED ]; then
    fact restore FAIL "$SCHED reads ${state:-nothing} after the restore, so the reports job keeps firing: run $words and fugaro update"
  else
    fact restore FAIL "$SCHED reads ${state:-nothing} after the restore, not the ENABLED the layer says now, so the schedule the owner had is not back: run $words and fugaro update"
  fi
}

step_preflight() {
  local doc name project region tool sched_line
  doc=$("$FUGARO" doctor --json 2>/dev/null) || {
    preflight_block doctor "fugaro doctor --json did not answer, so the installation cannot be read" "$FUGARO doctor --json"
    return 0
  }
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null)
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  region=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("region",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    preflight_block installation "the installation is ${name:-?} in ${project:-?}, and Check 341 never runs against belong, EdgeWeb or EdgeServer" "run this check on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] || preflight_block installation "the installation is ${name:-nothing}, and this check runs on $INSTALLATION only" "$FUGARO doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  [ "$project" = "$GCP_PROJECT" ] || preflight_block gcp-project "the GCP project is ${project:-nothing}, and this check runs in $GCP_PROJECT only" "$FUGARO doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  [ -n "$region" ] || preflight_block region "the installation names no region, so the reports job's own region cannot be read" "$FUGARO doctor --json on the installation $INSTALLATION"
  REGION=$region
  for tool in "$GCLOUD" python3 git; do
    command -v "$tool" >/dev/null 2>&1 || preflight_block tools "$tool is not on PATH" "put $tool on PATH"
  done
  # The one write the check makes, asked for its answer without writing: a write
  # the CLI would refuse is a blocker, not a failure thirty minutes in.
  if ! "$FUGARO" settings set layer.jobs.reports.paused false --layer project --dry-run --json >"$SCRATCH/preflight-set.json" 2>"$SCRATCH/preflight-set.err"; then
    preflight_block setting "writing layer.jobs.reports.paused into the project layer is refused: $(head -c 160 "$SCRATCH/preflight-set.err")" "$FUGARO settings set layer.jobs.reports.paused false --layer project"
  else
    preflight_note setting "$(python3 -c 'import json,sys
try: d=json.load(open(sys.argv[1]))
except Exception: d={}
print("the write would land in %s (%s)" % (d.get("path","?"), d.get("layer","?")))' "$SCRATCH/preflight-set.json" 2>/dev/null)"
  fi
  if ! "$FUGARO" settings get layer.jobs.reports.paused --json >"$SCRATCH/preflight-get.json" 2>/dev/null; then
    preflight_block setting-read "layer.jobs.reports.paused cannot be read, so the value the check puts back cannot be known" "$FUGARO settings get layer.jobs.reports.paused"
  else
    preflight_note setting-now "layer.jobs.reports.paused reads $(python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
rows=[r for r in (d.get("settings") or []) if r.get("key")=="layer.jobs.reports.paused"]
r=rows[0] if rows else {}
print(r.get("value") if r.get("value") is not None else ((r.get("default") or {}).get("value") or "?"))' "$SCRATCH/preflight-get.json" 2>/dev/null) now"
  fi
  if command -v "$GCLOUD" >/dev/null 2>&1; then
    if "$GCLOUD" run jobs describe "$JOB" --region "$region" --project "$GCP_PROJECT" --format=json >"$SCRATCH/preflight-job.json" 2>"$SCRATCH/preflight-job.err"; then
      preflight_note job "the reports job $JOB is deployed in $region, running $(python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
t=(d.get("template") or {}).get("template") or {}
c=(t.get("containers") or [{}])[0]
print(" ".join(c.get("args") or []) or "?")' "$SCRATCH/preflight-job.json" 2>/dev/null) as $(python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
t=(d.get("template") or {}).get("template") or {}
print(t.get("serviceAccount","?"))' "$SCRATCH/preflight-job.json" 2>/dev/null)"
    else
      preflight_block job "the reports job $JOB is not deployed in $GCP_PROJECT/$region: $(head -c 140 "$SCRATCH/preflight-job.err")" "$FUGARO update (the job is planned from the layer's jobs.reports.paused)"
    fi
    if sched_line=$(scheduler_read); then
      preflight_note schedule "$SCHED in $(cut -f1 <<<"$sched_line") reads $(cut -f2 <<<"$sched_line"), $(cut -f3 <<<"$sched_line") ($(cut -f4 <<<"$sched_line"))"
    else
      preflight_block schedule "no schedule $SCHED reads in $(scheduler_candidates | tr '\n' ' '): $(head -c 140 "$SCRATCH/scheduler.err" 2>/dev/null || true)" "pass --scheduler-region <the region the scheduler job lives in>, or run $FUGARO update"
    fi
  fi
  if [ -n "$REPO" ]; then
    preflight_note repo "--repo $REPO"
  elif git remote get-url origin >/dev/null 2>&1; then
    preflight_note repo "the checkout's origin is $(git remote get-url origin)"
  else
    preflight_block repo "this directory is not a checkout and no --repo was given" "pass --repo <owner/name>, or run this in the repository's checkout"
  fi
  preflight_note stored "the report read back is $KEY's, read with your own account; it exists only once the job has fired"
  preflight_note wait "the check waits up to $WAIT_MINUTES min for the firing, and runs TWO fugaro updates"
}

# ----------------------------------------------------------------------- main
main() {
  if [ "$PREFLIGHT" = yes ]; then
    SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check341-preflight.XXXXXX") || stop "could not make a scratch directory"
    chmod 700 "$SCRATCH"
    FACTS="$SCRATCH/facts"
    : >"$FACTS"
    : "${LIVE_CHECK_LIB:=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib}"
    # shellcheck source=lib/preflight.sh
    . "$LIVE_CHECK_LIB/preflight.sh"
    PREFLIGHT_TOOL="Check 341"
    preflight_agent_session
    guard_xtrace
    if [ -z "$KEY" ]; then
      KEY=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc).date()-datetime.timedelta(days=1)).isoformat())')
    fi
    step_preflight
    preflight_report
  fi

  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check341.XXXXXX") || stop "could not make a scratch directory"
  chmod 700 "$SCRATCH"
  FACTS="$SCRATCH/facts"
  : >"$FACTS"

  guard_xtrace
  guard_agent
  need_tools
  guard_installation
  resolve_repo
  resolve_key
  step_setting
  step_power
  step_scheduler
  step_job
  step_assignment
  step_grants
  step_fired
  step_store
  step_stored
  step_retention
  REACHED_END=yes
}

main "$@"
