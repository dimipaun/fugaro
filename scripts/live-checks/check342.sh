#!/usr/bin/env bash
# Check 342 — the failures report reproduces a window a person built by hand: the
# live `runs ls --group-by failure_class` table and the stored report's Failures
# table name the same runs under the same classes. The classes a legacy reader
# derived from `reason` (FC5) read as the same classes and cannot be told apart
# through a read, so the check counts them in and says so.
#
# The periodic-reviews plan's §P8 and docs/failure-classes/design.md. It is the
# one check that says whether the failure vocabulary means what the design says
# it means: a vocabulary can be internally consistent and every hermetic test can
# pass while the word it puts on a run is not the word a person who watched the
# run would use. Nothing here is a claim that it does.
#
# Usage:      bash scripts/live-checks/check342.sh [--key <period>] [--repo <owner/name>] [--since <Nd>] [--preflight]
#             bash scripts/live-checks/check342.sh --help
#             --key    the period of the stored report (2026-11-20, 2026-W47, 2026-11 or 2026); default is yesterday's day.
#             --repo   the repository reported; default is this checkout's origin.
#             --since  the live listing's window (e.g. 8d); default is wide enough to cover the period.
#             --preflight  reads every precondition and prints the blockers; launches nothing.
# Needs:      bash, mktemp, python3 (every `--json` document and the key's own clock are read with
#             it), git (the checkout's origin, when --repo is not given) and fugaro.
# Where:      your own machine, on the installation named `fugaro` in the GCP
#             project `fugaro-dev` — both hard-coded here — and never `belong`,
#             EdgeWeb or EdgeServer. The check refuses a doctor that names
#             another installation or project.
# Costs:      NOTHING billable: two reads — the stored report and the grouped
#             listing — with no run, no model call, no write and no pull request.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (check342_test.sh) drives it against fakes on PATH and never
#             touches a bucket, a cloud or a Fugaro installation.
# Cleanup:    nothing is created, so there is nothing to undo: the scratch
#             directory is the exit trap's only business.
# Output:     progress on stderr, ONE block on stdout headed CHECK 342 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        the two tables agree;
#               FAIL (exit 1)        they disagree, and the difference is named;
#               INCOMPLETE (exit 2)  the run stopped before it could read both;
#               INCOMPLETE (exit 3)  a read did not answer (UNVERIFIED).
#
# CHECK342_FUGARO, CHECK342_KEY, CHECK342_REPO, CHECK342_SINCE and
# CHECK342_KEEP_SCRATCH exist for this script's own test and an owner's patience.
set -Eeuo pipefail

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

FUGARO=${CHECK342_FUGARO:-fugaro}
INSTALLATION=fugaro
GCP_PROJECT=fugaro-dev
KEY=${CHECK342_KEY:-}
REPO=${CHECK342_REPO:-}
SINCE=${CHECK342_SINCE:-}

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
LIVE_OK=no
PREFLIGHT=no
KEEP_SCRATCH=${CHECK342_KEEP_SCRATCH:-}

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
  --key)
    [ $# -ge 2 ] || stop "--key needs a period key"
    KEY=$2
    shift
    ;;
  --repo)
    [ $# -ge 2 ] || stop "--repo needs owner/name"
    REPO=$2
    shift
    ;;
  --since)
    [ $# -ge 2 ] || stop "--since needs a window such as 8d"
    SINCE=$2
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
    say "kept the scratch directory at $SCRATCH (CHECK342_KEEP_SCRATCH is set)"
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
  printf '\nCHECK 342 RESULT\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf 'FACT %s: %s — %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 342 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  if [ "$PREFLIGHT" = yes ]; then
    exit "$status"
  fi
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT judged"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created, and the scratch directory this check read into is gone"
  elif [ -n "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|INFO|the scratch directory is kept at ${SCRATCH}, by --keep-scratch"
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
    stop "this shell carries $found, so it is a coding agent's session: Check 342 is yours to run at your own terminal, and no agent runs any step of it"
  fact agent-session PASS "no coding agent's marker in this shell"
}

need_tools() {
  local tool missing=""
  for tool in mktemp python3 "$FUGARO"; do
    command -v "$tool" >/dev/null 2>&1 || missing="${missing}${missing:+, }$tool"
  done
  [ -n "$REPO" ] || command -v git >/dev/null 2>&1 || missing="${missing}${missing:+, }git"
  [ -z "$missing" ] || stop "these tools are not on PATH: $missing"
  fact tools PASS "mktemp, python3 and $FUGARO are on PATH"
}

guard_installation() {
  local doc name project
  doc=$("$FUGARO" doctor --json 2>/dev/null) || stop "fugaro doctor --json did not answer, so which installation this is cannot be read: fix it with $FUGARO doctor"
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null) ||
    stop "fugaro doctor --json did not carry a project block: fix it with $FUGARO doctor"
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    stop "this installation is ${name:-?} in ${project:-?}, and Check 342 never runs against belong, EdgeWeb or EdgeServer: run it on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] ||
    stop "fugaro doctor names the installation ${name:-nothing}, and Check 342 runs on $INSTALLATION only: run it on $INSTALLATION"
  [ "$project" = "$GCP_PROJECT" ] ||
    stop "fugaro doctor names the GCP project ${project:-nothing}, and Check 342 runs in $GCP_PROJECT only: run it on the installation $INSTALLATION in $GCP_PROJECT"
  fact installation PASS "installation $INSTALLATION in $GCP_PROJECT"
}

# ------------------------------------------------------------------- the reads
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
    stop "the repository is $REPO, and Check 342 never runs against belong, EdgeWeb or EdgeServer"
    ;;
  esac
  fact repository PASS "repository $REPO"
}

# The period and the live listing's window: --since bounds a run's START while a
# report buckets by the record's FINISH, so the default window is the period plus
# the two days that gap can span. An owner who knows the window passes --since.
resolve_window() {
  local derived
  [ -n "$KEY" ] || KEY=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc).date()-datetime.timedelta(days=1)).isoformat())')
  derived=$(python3 - "$KEY" <<'PY'
import datetime,sys
key=sys.argv[1].strip()
now=datetime.datetime.now(datetime.timezone.utc).date()
def days_ago(d): return (now-d).days
try:
    if len(key)==10 and key[4]=="-":
        d=datetime.date.fromisoformat(key); since=days_ago(d)+2; dim="day"
    elif "W" in key:
        y,w=key.split("-W"); d=datetime.date.fromisocalendar(int(y),int(w),1); since=days_ago(d)+9; dim="week"
    elif len(key)==7:
        y,m=key.split("-"); d=datetime.date(int(y),int(m),1)
        nxt=datetime.date(int(y)+(m==12),1 if m==12 else m+1,1); since=days_ago(d)+(nxt-d).days+2; dim="month"
    elif len(key)==4:
        since=days_ago(datetime.date(int(key),1,1))+368; dim="year"
    else:
        print(""); sys.exit(0)
except Exception:
    print(""); sys.exit(0)
print("%dd %s" % (since,dim))
PY
)
  [ -n "$derived" ] || stop "--key $KEY is not a period key this CLI writes (2026-11-20, 2026-W47, 2026-11 or 2026)"
  KEY_DIM=${derived##* }
  [ -n "$SINCE" ] || SINCE=${derived%% *}
  fact window PASS "period $KEY ($KEY_DIM), live window --since $SINCE"
}

step_stored() {
  "$FUGARO" runs digest --stored "$KEY" --repo "$REPO" >"${SCRATCH}/stored.md" 2>"${SCRATCH}/stored.err" ||
    { fact stored FAIL "no stored report reads back for $KEY: $(head -c 300 "${SCRATCH}/stored.err")"; return 0; }
  local parsed=ok
  python3 - "${SCRATCH}/stored.md" >"${SCRATCH}/stored-table.txt" <<'PY' || parsed=no
import re,sys
body=open(sys.argv[1],encoding="utf-8").read()
m=re.search(r"^## Failures\n(.*?)(?=^## |\Z)", body, re.M|re.S)
rows={}
for line in (m.group(1) if m else "").splitlines():
    r=re.match(r"^\|\s*`([^`]+)`\s*\|\s*(\d+)\s*\|", line)
    if r: rows[r.group(1)]=int(r.group(2))
for k in sorted(rows): print("%s\t%d" % (k,rows[k]))
PY
  [ "$parsed" = ok ] || {
    fact stored FAIL "the stored report for $KEY did not read as a report: its Failures table cannot be read"
    return 0
  }
  fact stored PASS "the stored report for $KEY names $(wc -l <"${SCRATCH}/stored-table.txt" | tr -d ' ') failure class(es): $(cut -f1 "${SCRATCH}/stored-table.txt" | tr '\n' ' ')"
}

step_live() {
  "$FUGARO" runs ls --json --repo "$REPO" --since "$SINCE" --group-by failure_class >"${SCRATCH}/live.json" 2>"${SCRATCH}/live.err" ||
    { fact live UNVERIFIED "the grouped listing did not answer (runs ls --group-by failure_class --since $SINCE): $(head -c 300 "${SCRATCH}/live.err")"; return 0; }
  local parsed=ok
  LIVE_OK=yes
  python3 - "${SCRATCH}/live.json" >"${SCRATCH}/live-table.txt" <<'PY' || parsed=no
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
for g in d.get("groups") or []:
    print("%s\t%d" % (g.get("class",""), g.get("runs",0)))
PY
  [ "$parsed" = ok ] || {
    fact live UNVERIFIED "the grouped listing did not read as a listing: runs ls --group-by failure_class --since $SINCE"
    return 0
  }
  fact live PASS "the live listing names $(wc -l <"${SCRATCH}/live-table.txt" | tr -d ' ') class(es) over --since $SINCE: $(cut -f1 "${SCRATCH}/live-table.txt" | tr '\n' ' ')"
}

# FC5: a run whose record predates the failure_class field carries no class of
# its own, and the reader derives one from its `reason` instead of writing one
# back. No read tells a derived class from a written one -- the two arrive in the
# same field -- so the check says how many of the window's runs ended in a class
# at all, and names the hermetic test that pins the derivation itself.
step_legacy() {
  local n classed
  [ -s "${SCRATCH}/live.json" ] || {
    fact classes UNVERIFIED "the listing did not answer, so the window's runs cannot be counted"
    return 0
  }
  read -r n classed < <(python3 - "${SCRATCH}/live.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
runs=d.get("runs") or []
print(len(runs), len([r for r in runs if r.get("failure_class")]))
PY
)
  fact classes INFO "$classed of the window's $n run(s) ended in a class of the vocabulary; a class the legacy reader derived from reason (FC5) reads as that same class and is not told apart by any read -- TestLegacyClassFromReasonTable pins the derivation"
}

step_compare() {
  [ "$LIVE_OK" = yes ] || { fact compare UNVERIFIED "the grouped listing did not answer, so there is nothing to compare it with"; return 0; }
  [ -s "${SCRATCH}/stored-table.txt" ] || { fact compare UNVERIFIED "the stored report names no failure, so there is nothing to compare"; return 0; }
  if diff -u "${SCRATCH}/stored-table.txt" "${SCRATCH}/live-table.txt" >"${SCRATCH}/tables.diff" 2>&1; then
    fact compare PASS "the stored Failures table and the live grouping name the same classes with the same counts"
  else
    fact compare FAIL "the stored Failures table and the live grouping differ: $(grep -E '^[+-][^+-]' "${SCRATCH}/tables.diff" | tr '\n' ' ' | head -c 400)"
  fi
  fact your-account INFO "the third table is yours: read the two above against the causes you know, and a class whose runs you would not put there is the finding"
}

step_preflight() {
  local doc name project key
  doc=$("$FUGARO" doctor --json 2>/dev/null) || {
    preflight_block doctor "fugaro doctor --json did not answer, so the installation cannot be read" "$FUGARO doctor --json"
    return 0
  }
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null)
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    preflight_block installation "the installation is ${name:-?} in ${project:-?}, and Check 342 never runs against belong, EdgeWeb or EdgeServer" "run this check on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] || preflight_block installation "the installation is ${name:-nothing}, and this check runs on $INSTALLATION only" "$FUGARO doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  [ "$project" = "$GCP_PROJECT" ] || preflight_block gcp-project "the GCP project is ${project:-nothing}, and this check runs in $GCP_PROJECT only" "$FUGARO doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  if [ -n "$REPO" ]; then
    preflight_note repo "--repo $REPO"
  elif git remote get-url origin >/dev/null 2>&1; then
    preflight_note repo "the checkout's origin is $(git remote get-url origin)"
  else
    preflight_block repo "this directory is not a checkout and no --repo was given" "pass --repo <owner/name>, or run this in the repository's checkout"
  fi
  local key=$KEY
  [ -n "$key" ] || key=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc).date()-datetime.timedelta(days=1)).isoformat())')
  if ! "$FUGARO" runs digest --stored "$key" --repo "${REPO:-owner/name}" >/dev/null 2>&1; then
    preflight_block stored "no stored report reads back for $key (runs digest --stored $key)" "$FUGARO runs digest --stored $key --repo ${REPO:-<owner/name>}"
  fi
}

# ----------------------------------------------------------------------- main
main() {
  if [ "$PREFLIGHT" = yes ]; then
    : "${LIVE_CHECK_LIB:=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib}"
    # shellcheck source=lib/preflight.sh
    . "$LIVE_CHECK_LIB/preflight.sh"
    PREFLIGHT_TOOL="Check 342"
    preflight_agent_session
    guard_xtrace
    need_tools
    if [ -z "$KEY" ]; then
      KEY=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc).date()-datetime.timedelta(days=1)).isoformat())')
    fi
    step_preflight
    preflight_report
  fi

  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check342.XXXXXX") || stop "could not make a scratch directory"
  chmod 700 "$SCRATCH"
  FACTS="$SCRATCH/facts"
  : >"$FACTS"

  guard_xtrace
  guard_agent
  need_tools
  guard_installation
  resolve_repo
  resolve_window
  step_stored
  step_live
  step_legacy
  step_compare
  REACHED_END=yes
}

main "$@"
