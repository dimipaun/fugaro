#!/usr/bin/env bash
# Check 343 — the opt-in commit into the designated repository: with
# `reports_commit` designated, ONE
#   fugaro run --recipe commit-reports --commit-reports "commit the stored reports"
# in that repository's checkout carries the project's stored reports into it, a
# second run of the same command with nothing new in the store ends WITHOUT a
# pull request, and a file the folder holds and the store does not is left where
# it is, named in the run's own record and not taken away.
#
# The periodic-reviews plan's §P8 (R9, D2750–D2754) and the failures none of the
# hermetic tests can take of a real repository: what the store holds, what the
# designated folder holds, and the pull request between them. Nothing here is a
# claim that the commit works.
#
# Usage:      bash scripts/live-checks/check343.sh [--stray <path>] [--preflight]
#             bash scripts/live-checks/check343.sh --help
#             --stray <path>   a file the folder holds and the store does not, committed and pushed
#                              on the base branch (the check's third run: see step 5 below). Without
#                              it the check runs steps 2 to 4 and records step 5 as not run.
#             --preflight      reads every precondition and prints the blockers; launches nothing.
# Needs:      bash, mktemp, python3, git, gcloud (the store's listing, with your
#             own launcher account: the read is the launcher's existing
#             bucket-wide objectViewer, D2754), gh (the pull request the run
#             opens) and fugaro.
# Where:      your own machine, on the installation named `fugaro` in the GCP
#             project `fugaro-dev` — both hard-coded here — and never `belong`,
#             EdgeWeb or EdgeServer, in the designated repository's checkout.
#             The check refuses a doctor that names another installation or
#             project, and a designation that names another repository.
# Costs:      THREE runs of a commit-reports recipe. Each is a real Cloud Run
#             job: a clone, the store's objects downloaded, a commit and one
#             senior review — the run's own budget, the policy's ordinary one,
#             and no model but the review's. Every launch spends money.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (check343_test.sh) drives it against fake fugaro, gcloud, gh and
#             git on PATH and never touches a bucket, a cloud or a repository.
# Cleanup:    nothing the check itself creates has to be put back — no account,
#             no binding and no credential is made. Its scratch directory is the
#             exit trap's business, and the branch and pull request each run
#             opens are yours to merge or close.
# Output:     progress on stderr, ONE block on stdout headed CHECK 343 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        every criterion held;
#               FAIL (exit 1)        one did not, and it is named;
#               INCOMPLETE (exit 2)  the run stopped before its last step;
#               INCOMPLETE (exit 3)  a read did not answer (UNVERIFIED).
#
# CHECK343_FUGARO, CHECK343_GCLOUD, CHECK343_GH, CHECK343_GIT, CHECK343_COMMAND,
# CHECK343_STRAY, CHECK343_SETTLE_SECONDS, CHECK343_POLL_SECONDS and
# CHECK343_KEEP_SCRATCH exist for this script's own test and an owner's patience.
set -Eeuo pipefail

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

FUGARO=${CHECK343_FUGARO:-fugaro}
GCLOUD=${CHECK343_GCLOUD:-gcloud}
GH=${CHECK343_GH:-gh}
GIT=${CHECK343_GIT:-git}
INSTALLATION=fugaro
GCP_PROJECT=fugaro-dev
BUCKET=fugaro-runs-fugaro-dev
COMMAND=${CHECK343_COMMAND:-'commit the stored reports'}
STRAY=${CHECK343_STRAY:-}
SETTLE_SECONDS=${CHECK343_SETTLE_SECONDS:-5400}
POLL_SECONDS=${CHECK343_POLL_SECONDS:-20}

REPO=""
DIR=""
RUN1="" RUN2="" RUN3=""
FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
PREFLIGHT=no
KEEP_SCRATCH=${CHECK343_KEEP_SCRATCH:-}

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
  --stray)
    [ $# -ge 2 ] || stop "--stray needs the path of a file the folder holds and the store does not"
    STRAY=$2
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
    say "kept the scratch directory at $SCRATCH (CHECK343_KEEP_SCRATCH is set)"
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
  printf '\nCHECK 343 RESULT\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf 'FACT %s: %s — %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 343 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
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
    facts="${facts}"$'\n'"restore|PASS|nothing the check creates has to be put back, and its scratch directory is gone; the branch and the pull request each run opens are yours to merge or close"
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
    stop "this shell carries $found, so it is a coding agent's session: Check 343 is yours to run at your own terminal, and no agent runs any step of it"
  fact agent-session PASS "no coding agent's marker in this shell"
}

need_tools() {
  local tool missing=""
  for tool in mktemp python3 "$GIT" "$GCLOUD" "$GH" "$FUGARO"; do
    command -v "$tool" >/dev/null 2>&1 || missing="${missing}${missing:+, }$tool"
  done
  [ -z "$missing" ] || stop "these tools are not on PATH: $missing"
  fact tools PASS "mktemp, python3, $GIT, $GCLOUD, $GH and $FUGARO are on PATH"
}

guard_installation() {
  local doc name project
  doc=$("$FUGARO" doctor --json 2>/dev/null) || stop "fugaro doctor --json did not answer, so which installation this is cannot be read: fix it with $FUGARO doctor"
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null) ||
    stop "fugaro doctor --json did not carry a project block: fix it with $FUGARO doctor"
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    stop "this installation is ${name:-?} in ${project:-?}, and Check 343 never runs against belong, EdgeWeb or EdgeServer: run it on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] ||
    stop "fugaro doctor names the installation ${name:-nothing}, and Check 343 runs on $INSTALLATION only: run it on $INSTALLATION"
  [ "$project" = "$GCP_PROJECT" ] ||
    stop "fugaro doctor names the GCP project ${project:-nothing}, and Check 343 runs in $GCP_PROJECT only: run it on the installation $INSTALLATION in $GCP_PROJECT"
  fact installation PASS "installation $INSTALLATION in $GCP_PROJECT"
}

guard_checkout() {
  local url
  url=$("$GIT" remote get-url origin 2>/dev/null) || stop "this directory is not a checkout of a repository, and Check 343 runs in the designated repository's checkout: cd into it and run the check again"
  REPO=$(printf '%s' "$url" | python3 -c 'import sys,re
u=re.sub(r"\.git$","",sys.stdin.read().strip()).rstrip("/")
parts=re.split(r"[/:]",u)
print(parts[-2]+"/"+parts[-1] if len(parts)>=2 and parts[-2] and parts[-1] else "")')
  [ -n "$REPO" ] || stop "this checkout's origin ($url) names no repository as owner/name: run the check in the designated repository's checkout"
  case "$REPO" in
  *belong* | *edgeweb* | *edgeserver*)
    stop "the repository is $REPO, and Check 343 never runs against belong, EdgeWeb or EdgeServer"
    ;;
  esac
  fact repository PASS "repository $REPO"
}

# The designation: reports_commit on the published policy node. A run commits
# into the repository and folder the project designated or into nothing at all,
# so a designation that is absent, that names another repository or that carries
# a folder the path check refuses is the first thing to read.
guard_designation() {
  local doc repo_json
  doc=$("$FUGARO" project policy show --json 2>/dev/null) || doc=""
  [ -n "$doc" ] || doc='{}'
  printf '%s' "$doc" >"${SCRATCH}/policy.json"
  repo_json=$(python3 - "${SCRATCH}/policy.json" <<'PY'
import json,sys
try: d=json.load(open(sys.argv[1],encoding="utf-8"))
except Exception: d={}
p=d.get("policy") or {}
rc=p.get("reports_commit") or {}
print("%s\t%s" % (rc.get("repo",""), rc.get("dir","")))
PY
)
  local designated dir
  IFS=$'\t' read -r designated dir <<<"$repo_json"
  [ -n "$designated" ] ||
    stop "the published policy designates no reports_commit, so no report may be committed anywhere: name reports_commit.repo in your local project config (fugaro update's policy stage carries it) and publish it with $FUGARO project policy publish"
  case "$designated" in
  *belong* | *edgeweb* | *edgeserver*)
    stop "the published policy designates $designated, and Check 343 never runs against belong, EdgeWeb or EdgeServer"
    ;;
  esac
  [ "$designated" = "$REPO" ] ||
    stop "the published policy designates $designated and this checkout is $REPO: the run commits into the repository the project designated, so run the check in $designated's checkout"
  [ -n "$dir" ] || dir=docs/fugaro-reviews-mirror
  DIR=$dir
  fact designation PASS "reports_commit names $REPO, folder $DIR"
}

# ------------------------------------------------------------------- the reads
read_store() {
  : >"${SCRATCH}/store.txt"
  "$GCLOUD" storage ls --recursive --project "$GCP_PROJECT" "gs://${BUCKET}/reports/" >"${SCRATCH}/store.out" 2>"${SCRATCH}/store.err" || {
    fact store UNVERIFIED "the store's listing did not answer (gcloud storage ls --recursive gs://${BUCKET}/reports/): $(head -c 300 "${SCRATCH}/store.err")"
    return 0
  }
  python3 - "${SCRATCH}/store.out" >"${SCRATCH}/store.txt" <<'PY'
import sys
for line in open(sys.argv[1], encoding="utf-8"):
    name=line.strip().split("gs://")[-1].split("/")
    # gs://<bucket>/reports/<rest...>
    if len(name)>=3 and name[1]=="reports":
        rel="/".join(name[2:])
        if rel.endswith(".md") and rel!="README.md":
            print(rel)
PY
  LC_ALL=C sort -o "${SCRATCH}/store.txt" "${SCRATCH}/store.txt"
  local n
  n=$(wc -l <"${SCRATCH}/store.txt" | tr -d ' ')
  [ "$n" != 0 ] ||
    stop "the store holds no stored report, so a commit run has nothing to hand in: run the reports job first (Check 341) and let it write one"
  fact store PASS "the store holds $n stored report(s) under reports/"
}

read_base() {
  "$GIT" fetch --no-tags origin >/dev/null 2>&1 || stop "git fetch origin did not answer, so the base branch the run clones cannot be read: fix the checkout's origin"
  BASE=$("$GIT" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|^origin/||' || true)
  [ -n "$BASE" ] || BASE=$("$GIT" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null | sed 's|^origin/||' || true)
  [ -n "$BASE" ] || stop "this checkout names no base branch (no origin/HEAD and no upstream), and the run commits onto a fresh clone of the ref it was launched on: fix the checkout's origin"
  "$GIT" rev-parse --verify --quiet "origin/${BASE}" >/dev/null && say "base branch origin/${BASE}"
  "$GIT" ls-tree -r --name-only "origin/${BASE}" -- "$DIR" >"${SCRATCH}/base.txt" 2>/dev/null ||
    : >"${SCRATCH}/base.txt"
  LC_ALL=C sort -o "${SCRATCH}/base.txt" "${SCRATCH}/base.txt"
  fact base PASS "$(wc -l <"${SCRATCH}/base.txt" | tr -d ' ') file(s) under $DIR on origin/${BASE}"
  # What the run's own clone lacks, and so what its commit has to carry.
  python3 - "${SCRATCH}/store.txt" "${SCRATCH}/base.txt" "$DIR" >"${SCRATCH}/missing.txt" <<'PY'
import sys
store=[l.strip() for l in open(sys.argv[1]) if l.strip()]
base=set(l.strip() for l in open(sys.argv[2]) if l.strip())
dir=sys.argv[3]
for rel in store:
    if "%s/%s" % (dir, rel) not in base:
        print(rel)
PY
  fact mirror PASS "$(wc -l <"${SCRATCH}/missing.txt" | tr -d ' ') of the store's report(s) the checkout's $DIR lacks on origin/${BASE}"
}

# ------------------------------------------------------------------ the runs
launch() { # launch LABEL -> prints the run id
  local label=$1 out
  out=$("$FUGARO" run --recipe commit-reports --commit-reports "$COMMAND" --json 2>"${SCRATCH}/${label}.err") || {
    fact "$label" FAIL "the launch did not answer: $(head -c 300 "${SCRATCH}/${label}.err") — read it with $FUGARO run --recipe commit-reports --commit-reports \"$COMMAND\""
    return 0
  }
  printf '%s' "$out" >"${SCRATCH}/${label}.json"
  python3 - "${SCRATCH}/${label}.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
print(d.get("run_id",""))
PY
}

wait_for_run() { # wait_for_run RUN_ID -> RUN_STATUS RUN_PR
  local run=$1 deadline=$((SECONDS + SETTLE_SECONDS)) doc row
  RUN_STATUS="" RUN_PR=""
  while :; do
    doc=$("$FUGARO" runs ls --json --repo "$REPO" 2>/dev/null) || doc=""
    printf '%s' "$doc" >"${SCRATCH}/ls.json"
    row=$(python3 - "$run" "${SCRATCH}/ls.json" <<'PY' 2>/dev/null || true
import json,sys
want=sys.argv[1]
try: d=json.load(open(sys.argv[2],encoding="utf-8"))
except Exception: sys.exit(0)
for r in d.get("runs") or []:
    if r.get("run_id")==want:
        print("%s\t%s\t%s" % (r.get("status",""), "yes" if r.get("terminal") else "no", r.get("pr",0) or 0))
        break
PY
)
    if [ -n "$row" ]; then
      local terminal
      IFS=$'\t' read -r RUN_STATUS terminal RUN_PR <<<"$row"
      [ "$terminal" = yes ] && return 0
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      RUN_STATUS=timeout
      return 0
    fi
    say "waiting for run $run to settle (${POLL_SECONDS}s)"
    sleep "$POLL_SECONDS"
  done
}

step_commit() {
  local run
  run=$(launch launch1)
  [ -n "$run" ] || { fact launch1 FAIL "the launch answered no run id: read it with $FUGARO run --recipe commit-reports --commit-reports \"$COMMAND\""; return 0; }
  RUN1=$run
  fact launch1 PASS "the commit run is $run"
  wait_for_run "$run"
  case "$RUN_STATUS" in
  timeout)
    fact settled1 FAIL "run $run did not settle within ${SETTLE_SECONDS}s: read it with $FUGARO watch $run"
    return 0
    ;;
  ready | succeeded) ;;
  *)
    fact settled1 FAIL "run $run settled as $RUN_STATUS: read it with $FUGARO watch $run"
    return 0
    ;;
  esac
  fact settled1 PASS "run $run settled $RUN_STATUS"
  local missing
  missing=$(wc -l <"${SCRATCH}/missing.txt" | tr -d ' ')
  if [ "$RUN_PR" = 0 ]; then
    if [ "$missing" = 0 ]; then
      fact files UNVERIFIED "the checkout already held every stored report, so run $run had nothing to commit and opened no pull request: bring the mirror behind the store (git fetch origin, check out ${BASE}, reset it to a commit before the mirror) or run the check in a fresh clone, then run it again"
    else
      fact files FAIL "run $run opened no pull request, and the checkout's $DIR lacked $missing stored report(s) the run had to commit: read it with $FUGARO watch $run"
    fi
    return 0
  fi
  "$GH" pr view "$RUN_PR" --repo "$REPO" --json number,url,state,isDraft,files >"${SCRATCH}/pr1.json" 2>"${SCRATCH}/pr1.err" || {
    fact files UNVERIFIED "the pull request #$RUN_PR did not read back (gh pr view): $(head -c 300 "${SCRATCH}/pr1.err")"
    return 0
  }
  python3 - "${SCRATCH}/pr1.json" "${SCRATCH}/store.txt" "${SCRATCH}/missing.txt" "$DIR" >"${SCRATCH}/files1.txt" <<'PY'
import json,sys
pr=json.load(open(sys.argv[1],encoding="utf-8"))
store=set(l.strip() for l in open(sys.argv[2]) if l.strip())
missing=[l.strip() for l in open(sys.argv[3]) if l.strip()]
dir=sys.argv[4]
got={f.get("path",""): f.get("changeType","") for f in pr.get("files") or []}
problems=[]
for rel in missing:
    if "%s/%s" % (dir,rel) not in got:
        problems.append("missing %s/%s" % (dir,rel))
extra=[p for p,ct in got.items() if ct=="DELETED"]
for p in sorted(got):
    if p == "%s/README.md" % dir:
        continue
    rel=p[len(dir)+1:] if p.startswith(dir+"/") else None
    if rel not in store:
        extra.append("not in the store: %s" % p)
for p in sorted(set(extra)):
    problems.append(p)
print("; ".join(problems))
PY
  local problems
  problems=$(cat "${SCRATCH}/files1.txt")
  if [ -z "$problems" ]; then
    fact files PASS "the pull request #$RUN_PR carries the $missing report(s) the checkout lacked, the index, and nothing the store does not hold"
  else
    fact files FAIL "the pull request #$RUN_PR does not carry what the commit had to carry: $problems"
  fi
  fact pr1 INFO "the pull request the commit run opened: #$RUN_PR $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8")).get("url",""))' "${SCRATCH}/pr1.json")"
}

step_nothing_new() {
  local run
  run=$(launch launch2)
  [ -n "$run" ] || { fact launch2 FAIL "the second launch answered no run id"; return 0; }
  RUN2=$run
  fact launch2 PASS "the second commit run is $run"
  wait_for_run "$run"
  case "$RUN_STATUS" in
  ready | succeeded) ;;
  timeout)
    fact nothing-new FAIL "the second run $run did not settle within ${SETTLE_SECONDS}s: read it with $FUGARO watch $run"
    return 0
    ;;
  *)
    fact nothing-new FAIL "the second run $run settled as $RUN_STATUS: read it with $FUGARO watch $run"
    return 0
    ;;
  esac
  if [ "$RUN_PR" = 0 ]; then
    fact nothing-new PASS "the second run $run settled $RUN_STATUS with no pull request, over a store whose reports the checkout now holds"
  else
    fact nothing-new FAIL "the second run $run opened pull request #$RUN_PR over a store with nothing new in it"
  fi
}

# Step 5: a file the folder holds and the store does not. It has to be committed
# and pushed, because the run commits onto a fresh clone of the base branch and
# sees only what the branch holds.
step_stray() {
  if [ -z "$STRAY" ]; then
    fact stray INFO "step 5 was not run: leave a file the folder holds and the store does not, commit and push it on ${BASE:-the base branch}, and run the check again with --stray <path>"
    return 0
  fi
  [ -n "$DIR" ] || return 0
  "$GIT" rev-parse --verify --quiet "origin/${BASE}:${STRAY}" >/dev/null || {
    fact stray FAIL "$STRAY is not on origin/${BASE}: the run commits onto a fresh clone of that branch, so a file only this working tree holds is a file it cannot see"
    return 0
  }
  local run
  run=$(launch launch3)
  [ -n "$run" ] || { fact launch3 FAIL "the third launch answered no run id"; return 0; }
  RUN3=$run
  fact launch3 PASS "the third commit run is $run"
  wait_for_run "$run"
  case "$RUN_STATUS" in
  ready | succeeded) ;;
  timeout)
    fact stray FAIL "the third run $run did not settle within ${SETTLE_SECONDS}s: read it with $FUGARO watch $run"
    return 0
    ;;
  *)
    fact stray FAIL "the third run $run settled as $RUN_STATUS: read it with $FUGARO watch $run"
    return 0
    ;;
  esac
  local logs has_logs=no named=no deleted=no
  logs=$("$FUGARO" logs "$run" 2>"${SCRATCH}/logs.err") || logs=""
  printf '%s' "$logs" >"${SCRATCH}/logs.txt"
  [ -n "$logs" ] && has_logs=yes
  grep -qF "$STRAY" "${SCRATCH}/logs.txt" 2>/dev/null && named=yes
  if [ "$RUN_PR" != 0 ]; then
    local verdict3
    "$GH" pr diff "$RUN_PR" --repo "$REPO" >"${SCRATCH}/pr3.diff" 2>/dev/null || : >"${SCRATCH}/pr3.diff"
    verdict3=$(python3 - "${SCRATCH}/pr3.diff" "$STRAY" <<'PY'
import sys
# A deletion of one path shows as its own `diff --git` section whose body says
# `deleted file mode` (a modified file's section never does).
deleted=False
section=False
for line in open(sys.argv[1], encoding="utf-8", errors="replace"):
    if line.startswith("diff --git "):
        section = sys.argv[2] in line
    elif section and (line.startswith("deleted file mode") or line.startswith("+++ /dev/null")):
        deleted=True
print("deleted" if deleted else "kept")
PY
) || verdict3=""
    [ "$verdict3" = deleted ] && deleted=yes
  fi
  if [ "$deleted" = yes ]; then
    fact stray FAIL "run $run deleted $STRAY, a file the store does not hold: pull request #$RUN_PR carries a deletion of it"
    return 0
  fi
  if [ "$has_logs" = no ]; then
    fact stray UNVERIFIED "run $run left $STRAY alone ($([ "$RUN_PR" != 0 ] && echo "pull request #$RUN_PR carries no deletion of it")) and its log did not answer: read it with $FUGARO logs $run, which has to name the file"
    return 0
  fi
  if [ "$named" = yes ]; then
    fact stray PASS "run $run left $STRAY where it was and named it in its log$([ "$RUN_PR" != 0 ] && echo ", and pull request #$RUN_PR carries no deletion of it")"
  else
    fact stray FAIL "run $run's log does not name $STRAY: a file the folder holds and the store does not is left alone AND named in the run's own record, and a run that leaves it silently is not the reading this check is for"
  fi
}

step_preflight() {
  local doc name project policy designated dir
  doc=$("$FUGARO" doctor --json 2>/dev/null) || {
    preflight_block doctor "fugaro doctor --json did not answer, so the installation cannot be read" "$FUGARO doctor --json"
    return 0
  }
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null)
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    preflight_block installation "the installation is ${name:-?} in ${project:-?}, and Check 343 never runs against belong, EdgeWeb or EdgeServer" "run this check on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] || preflight_block installation "the installation is ${name:-nothing}, and this check runs on $INSTALLATION only" "$FUGARO doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  [ "$project" = "$GCP_PROJECT" ] || preflight_block gcp-project "the GCP project is ${project:-nothing}, and this check runs in $GCP_PROJECT only" "$FUGARO doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  local url
  url=$("$GIT" remote get-url origin 2>/dev/null) || url=""
  if [ -n "$url" ]; then
    REPO=$(printf '%s' "$url" | python3 -c 'import sys,re
u=re.sub(r"\.git$","",sys.stdin.read().strip()).rstrip("/")
parts=re.split(r"[/:]",u)
print(parts[-2]+"/"+parts[-1] if len(parts)>=2 and parts[-2] and parts[-1] else "")')
    preflight_note repo "the checkout is $REPO"
  else
    preflight_block repo "this directory is not a checkout of a repository" "cd into the designated repository's checkout and run the check again"
  fi
  policy=$("$FUGARO" project policy show --json 2>/dev/null) || policy='{}'
  read -r designated dir < <(printf '%s' "$policy" | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: d={}
rc=(d.get("policy") or {}).get("reports_commit") or {}
print("%s %s" % (rc.get("repo",""), rc.get("dir","")))' 2>/dev/null) || true
  if [ -z "$designated" ]; then
    preflight_block designation "the published policy designates no reports_commit, so no report may be committed anywhere" "name reports_commit.repo in your local project config, then run $FUGARO project policy publish"
  elif [ "$designated" != "$REPO" ]; then
    preflight_block designation "the policy designates $designated and this checkout is ${REPO:-nothing}" "run the check in $designated's checkout"
  else
    preflight_note designation "reports_commit names $designated, folder ${dir:-docs/fugaro-reviews-mirror}"
  fi
  if ! "$GCLOUD" storage ls --recursive --project "$GCP_PROJECT" "gs://${BUCKET}/reports/" >/dev/null 2>&1; then
    preflight_block store "the store's listing did not answer (gcloud storage ls --recursive gs://${BUCKET}/reports/)" "$GCLOUD auth login, then $GCLOUD storage ls --recursive gs://${BUCKET}/reports/"
  else
    preflight_note store "the store lists, and the read is your launcher's own"
  fi
}

# ----------------------------------------------------------------------- main
main() {
  if [ "$PREFLIGHT" = yes ]; then
    : "${LIVE_CHECK_LIB:=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib}"
    # shellcheck source=lib/preflight.sh
    . "$LIVE_CHECK_LIB/preflight.sh"
    PREFLIGHT_TOOL="Check 343"
    preflight_agent_session
    guard_xtrace
    need_tools
    SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check343-preflight.XXXXXX")
    step_preflight
    rm -rf "$SCRATCH" 2>/dev/null || true
    SCRATCH=""
    preflight_report
  fi

  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check343.XXXXXX") || stop "could not make a scratch directory"
  chmod 700 "$SCRATCH"
  FACTS="$SCRATCH/facts"
  : >"$FACTS"

  guard_xtrace
  guard_agent
  need_tools
  guard_installation
  guard_checkout
  guard_designation
  read_store
  read_base
  step_commit
  step_nothing_new
  step_stray
  REACHED_END=yes
}

main "$@"
