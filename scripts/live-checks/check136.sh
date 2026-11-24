#!/usr/bin/env bash
# Check 136 — a report run opens the pull request the due reports need: one file
# per due period under docs/fugaro-reviews/<YYYY>/, the index regenerated, and
# no address and no task text in any of them.
#
# The release behind it: T3.1's `run-digest` recipe, T3.6's launcher manifest,
# T3.2's runner digest step and T3.3's finalize guard, all merged. What only a
# live installation settles is the deployed half: that the job image runs the
# digest step, that the launcher's manifest reaches it, that the pull request
# carries exactly the DUE periods' files and nothing else, and that none of
# them holds an address or a task's text (D321, D322, D323, G3, G5).
#
# Usage:      bash scripts/live-checks/check136.sh [--preflight] [--run-id <id>] [--keep-scratch]
#             bash scripts/live-checks/check136.sh --help
#             --preflight  reads every precondition and prints the blockers; launches nothing.
#             --run-id     reads a report run already launched (the launch is skipped).
#             --keep-scratch  keeps the scratch directory instead of removing it.
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             git (the checkout's origin and the run's branch), gh (the pull
#             request's file list) and fugaro (the release you have installed:
#             this check drives IT, it does not build one).
# Where:      the SANDBOX repository's checkout only, on the installation named
#             `fugaro` in the GCP project `fugaro-dev` — both hard-coded here —
#             and never `belong`, EdgeWeb or EdgeServer. The check refuses a
#             doctor that names another installation or project.
# Costs:      ONE billable report run: one digest step (no model call of its
#             own), one review stage's model calls, one job execution and one
#             pull request you merge or decline yourself.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (check136_test.sh) drives it against fakes on PATH and never
#             touches a repository, GitHub or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed
#             from the exit trap. The run, its branch and its pull request are
#             yours to decide (the `pull-request` row records which way).
# Output:     progress on stderr, ONE block on stdout headed CHECK 136 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        every criterion held;
#               FAIL (exit 1)        a criterion was measured and failed;
#               INCOMPLETE (exit 2)  the run stopped before it could measure everything;
#               INCOMPLETE (exit 3)  it ran to the end and a row read UNVERIFIED.
#
# CHECK136_FUGARO, CHECK136_SETTLE_SECONDS, CHECK136_POLL_SECONDS,
# CHECK136_RUN_ID, CHECK136_KEEP_SCRATCH, CHECK136_AFTER_T33 and
# CHECK136_PREFLIGHT exist for this script's own test and an owner's patience.
set -Eeuo pipefail

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

FUGARO=${CHECK136_FUGARO:-fugaro}
INSTALLATION=fugaro
GCP_PROJECT=fugaro-dev
SETTLE_SECONDS=${CHECK136_SETTLE_SECONDS:-5400}
POLL_SECONDS=${CHECK136_POLL_SECONDS:-20}
BRIEF="periodic run review"

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
PREFLIGHT=no
RUN_ID=${CHECK136_RUN_ID:-}
KEEP_SCRATCH=${CHECK136_KEEP_SCRATCH:-}

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

# ------------------------------------------------------------------ arguments
while [ $# -gt 0 ]; do
  case "$1" in
  -h | --help)
    usage
    exit 0
    ;;
  --preflight)
    PREFLIGHT=yes
    ;;
  --run-id)
    [ $# -ge 2 ] || stop "--run-id needs a run id"
    RUN_ID=$2
    shift
    ;;
  --run-id=*)
    RUN_ID=${1#--run-id=}
    ;;
  --keep-scratch)
    KEEP_SCRATCH=1
    ;;
  *)
    stop "unknown argument: $1 (see --help)"
    ;;
  esac
  shift
done
export CHECK136_PREFLIGHT=$PREFLIGHT

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ -n "$KEEP_SCRATCH" ]; then
    say "kept the scratch directory at $SCRATCH (CHECK136_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
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
  printf '\nCHECK 136 RESULT\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf 'FACT %s: %s — %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 136 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  # A preflight is a read with its own report and its own exit status: it
  # prints no CHECK block and nothing was created, so it ends where it says.
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
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, and everything this check wrote into it, is gone"
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
    stop "this shell arrived with tracing on (bash -x): a trace prints every command and every document this check reads, so run it without -x"
  fact xtrace PASS "no xtrace in this shell"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!marker:-}" ]; then found="${found}${found:+, }$marker"; fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session: Check 136 is yours to run at your own terminal, and no agent runs any step of it"
  fact agent-session PASS "no coding agent's marker in this shell"
}

need_tools() {
  local tool missing=""
  for tool in mktemp python3 git gh "$FUGARO"; do
    command -v "$tool" >/dev/null 2>&1 || missing="${missing}${missing:+, }$tool"
  done
  [ -z "$missing" ] || stop "these tools are not on PATH: $missing"
  fact tools PASS "mktemp, python3, git, gh and $FUGARO are on PATH"
}

# doctor reads the installation's own identity: the project it is installed in
# and the GCP project it writes to. A check that launches a run must know which
# installation it is aiming at, and `belong` is the one it may never aim at.
doctor_identity() {
  "$FUGARO" doctor --json 2>/dev/null
}

guard_installation() {
  local doc name project
  doc=$(doctor_identity) || stop "fugaro doctor --json did not answer, so which installation this is cannot be read: fix it with $FUGARO doctor"
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("project") or {}).get("name",""))' 2>/dev/null) ||
    stop "fugaro doctor --json did not carry a project block: fix it with $FUGARO doctor"
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    stop "this installation is ${name:-?} in ${project:-?}, and Check 136 never runs against belong, EdgeWeb or EdgeServer: run it on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] ||
    stop "fugaro doctor names the installation ${name:-nothing}, and Check 136 runs on $INSTALLATION only: run it on $INSTALLATION"
  [ "$project" = "$GCP_PROJECT" ] ||
    stop "fugaro doctor names the GCP project ${project:-nothing}, and Check 136 runs in $GCP_PROJECT only: run it on the installation $INSTALLATION in $GCP_PROJECT"
  fact installation PASS "installation $INSTALLATION in $GCP_PROJECT"
}

guard_t33() {
  [ "${CHECK136_AFTER_T33:-}" = yes ] ||
    stop "this check reads the pull request task T3.3's finalize guard produces — the run's diff against its base is exactly the generated files under docs/fugaro-reviews/, byte for byte — and a runner built before it holds that guard for no pull request. Export CHECK136_AFTER_T33=yes only once T3.3 is in the release your job image runs"
  fact t33 PASS "CHECK136_AFTER_T33=yes: the job image runs a release carrying T3.3's guard"
}

# --------------------------------------------------------------- the check
sandbox_repo() {
  local url
  url=$(git remote get-url origin 2>/dev/null) || stop "this directory is not a checkout of a repository (git remote get-url origin failed): run the check in the sandbox repository's checkout"
  REPO_URL=$url
  REPO=$(printf '%s' "$url" | python3 -c 'import sys,re
u=re.sub(r"\.git$","",sys.stdin.read().strip()).rstrip("/")
parts=re.split(r"[/:]",u)
print(parts[-2]+"/"+parts[-1] if len(parts)>=2 and parts[-2] and parts[-1] else "")')
  [ -n "$REPO" ] || stop "this checkout's origin ($url) names no repository as owner/name, so which repository the report is of is unknown"
  SLUG=$(printf '%s' "$REPO" | tr '/' '-')
  case "$REPO$SLUG" in
  *belong* | *edgeweb* | *edgeserver*)
    stop "this checkout is $REPO, and Check 136 never runs against belong, EdgeWeb or EdgeServer: run it in the sandbox repository's checkout"
    ;;
  esac
  fact repository PASS "sandbox repository $REPO (slug $SLUG)"
}

step_recipe() {
  local out
  out=$("$FUGARO" recipes show run-digest 2>&1) ||
    stop "this CLI cannot resolve the recipe run-digest, so the launch would refuse it: $out"
  case "$out" in
  *"from catalog"*)
    fact recipe PASS "$(printf '%s' "$out" | head -1)"
    ;;
  *)
    fact recipe FAIL "run-digest does not resolve from the catalog here — a repository or project recipe shadows it: $(printf '%s' "$out" | head -1)"
    stop "run-digest does not resolve from the catalog here, so the launch would be a different run: $FUGARO recipes show run-digest names its source, and a repository or project recipe of the same name shadows the catalog's"
    ;;
  esac
}

step_launch() {
  if [ -n "$RUN_ID" ]; then
    BRANCH="fugaro/$RUN_ID"
    fact launch INFO "resumed: reading the report run $RUN_ID, launched already"
    return 0
  fi
  local out
  out=$("$FUGARO" run --recipe run-digest "$BRIEF" --json 2>"${SCRATCH}/launch.err") ||
    stop "the launch was refused: $(head -c 400 "${SCRATCH}/launch.err")"
  RUN_ID=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("run_id",""))') ||
    stop "the launch's --json document did not read"
  BRANCH=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("branch",""))')
  [ -n "$RUN_ID" ] || stop "the launch printed no run id, so nothing can be read back"
  [ -n "$BRANCH" ] || BRANCH="fugaro/$RUN_ID"
  fact launch PASS "launched report run $RUN_ID (branch $BRANCH)"
}

# step_settle follows the run's own row until it is terminal: `runs ls --json`
# is the CLI's own reading of the index, and `terminal` is the row's own word.
step_settle() {
  local deadline=$((SECONDS + SETTLE_SECONDS)) row doc
  while :; do
    doc=$("$FUGARO" runs ls --json --repo "$REPO" 2>/dev/null) || doc=""
    printf '%s' "$doc" >"${SCRATCH}/ls.json"
    row=$(python3 - "$RUN_ID" "${SCRATCH}/ls.json" <<'PY' 2>/dev/null || true
import json,sys
want=sys.argv[1]
try: d=json.load(open(sys.argv[2],encoding="utf-8"))
except Exception: sys.exit(0)
for r in d.get("runs") or []:
    if r.get("run_id")==want:
        print("%s\t%s\t%s\t%s\t%s" % (r.get("status",""), "yes" if r.get("terminal") else "no",
              r.get("pr",0) or 0, (r.get("cost") or {}).get("total_usd",0) or 0, r.get("failure_class","")))
        break
PY
)
    if [ -n "$row" ]; then
      local status terminal pr cost
      IFS=$'\t' read -r status terminal pr cost _ <<<"$row"
      if [ "$terminal" = yes ]; then
        RUN_STATUS=$status
        RUN_PR=$pr
        RUN_COST=$cost
        if [ "$status" = ready ] || [ "$status" = succeeded ]; then
          fact settled PASS "run $RUN_ID settled: status $status, PR #${pr:-0}, \$${cost}"
        else
          fact settled FAIL "run $RUN_ID settled as $status, and a report run that does not end ready opens no pull request: this check is not a repair step"
        fi
        return 0
      fi
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      fact settled FAIL "run $RUN_ID did not settle within ${SETTLE_SECONDS}s: read it with $FUGARO watch"
      return 0
    fi
    say "waiting for run $RUN_ID to settle (${POLL_SECONDS}s)"
    sleep "$POLL_SECONDS"
  done
}

# step_files reads the pull request's file list and the DUE set the CLI computes
# over the base tree. `runs digest --due` is the step's own rule — the seven
# closed days, the week, the month, the year — so the expected set is the CLI's
# answer for this folder rather than a second implementation of it here (G3).
step_files() {
  [ "${RUN_PR:-0}" -gt 0 ] || {
    fact pull-request FAIL "run $RUN_ID opened no pull request (status ${RUN_STATUS:-unknown}), so there is no file set to read"
    return 0
  }
  local prjson
  prjson=$(gh pr view "$RUN_PR" --repo "$REPO" --json number,url,state,isDraft,files 2>"${SCRATCH}/pr.err") ||
    stop "gh pr view $RUN_PR failed: $(head -c 400 "${SCRATCH}/pr.err")"
  PR_URL=$(printf '%s' "$prjson" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("url",""))')
  PR_STATE=$(printf '%s' "$prjson" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("state","")+(" draft" if d.get("isDraft") else ""))')
  printf '%s' "$prjson" | python3 -c 'import json,sys
d=json.load(sys.stdin)
for f in d.get("files") or []: print(f.get("path",""))' >"${SCRATCH}/pr-files.txt"

  mkdir -p "${SCRATCH}/base"
  git archive HEAD docs/fugaro-reviews 2>/dev/null | tar -x -C "${SCRATCH}/base" 2>/dev/null || mkdir -p "${SCRATCH}/base/docs/fugaro-reviews"
  local due
  due=$("$FUGARO" runs digest --due --dir "${SCRATCH}/base/docs/fugaro-reviews" --json 2>"${SCRATCH}/due.err") ||
    stop "the CLI could not compute which periods are due (runs digest --due --json): $(head -c 400 "${SCRATCH}/due.err")"
  printf '%s' "$due" | python3 -c 'import json,sys
d=json.load(sys.stdin)
for r in d.get("due") or []: print(r.get("key",""))' >"${SCRATCH}/due.txt"
  {
    printf 'docs/fugaro-reviews/README.md\n'
    while IFS= read -r key; do
      [ -n "$key" ] || continue
      printf 'docs/fugaro-reviews/%s/%s.md\n' "${key:0:4}" "$key"
    done <"${SCRATCH}/due.txt"
  } | LC_ALL=C sort >"${SCRATCH}/due-paths.txt"
  LC_ALL=C sort "${SCRATCH}/pr-files.txt" >"${SCRATCH}/pr-sorted.txt"

  local outside
  outside=$(grep -v '^docs/fugaro-reviews/' "${SCRATCH}/pr-files.txt" || true)
  if [ -n "$outside" ]; then
    fact files FAIL "the pull request changes files outside docs/fugaro-reviews/: $(printf '%s' "$outside" | tr '\n' ' ')"
  elif ! diff -u "${SCRATCH}/due-paths.txt" "${SCRATCH}/pr-sorted.txt" >"${SCRATCH}/files.diff" 2>&1; then
    fact files FAIL "the pull request is not one file per due period plus the index: $(grep -E '^[+-][^+-]' "${SCRATCH}/files.diff" | tr '\n' ' ' | head -c 400)"
  else
    fact files PASS "the pull request is exactly the $(wc -l <"${SCRATCH}/pr-sorted.txt" | tr -d ' ') generated files: the index and one file per due period ($(tr '\n' ' ' <"${SCRATCH}/due.txt"))"
  fi
}

# step_branch fetches the run's branch by name and reads it as a tree, so the
# committed reports are read without a checkout and without touching this one.
step_branch() {
  git fetch --no-tags origin "$BRANCH" 2>"${SCRATCH}/fetch.err" ||
    stop "git could not fetch the run's branch $BRANCH: $(head -c 400 "${SCRATCH}/fetch.err")"
  git ls-tree -r --name-only FETCH_HEAD -- docs/fugaro-reviews 2>/dev/null | LC_ALL=C sort >"${SCRATCH}/tree.txt"
  if [ -s "${SCRATCH}/tree.txt" ]; then
    fact branch PASS "the run's branch $BRANCH carries $(wc -l <"${SCRATCH}/tree.txt" | tr -d ' ') report files"
  else
    fact branch FAIL "the run's branch $BRANCH carries nothing under docs/fugaro-reviews/, so the digest step wrote no report"
  fi
}

# D323: a report holds run IDs, statuses, recipes, stage figures, dollars and
# flags — never an address, and never a task's text. Both readings are greps
# over the committed tree, so neither reads a file into this shell.
step_text() {
  if git grep -nE '[[:alnum:]._%+-]+@[[:alnum:].-]+\.[A-Za-z]{2,}' FETCH_HEAD -- docs/fugaro-reviews >"${SCRATCH}/address.txt" 2>/dev/null; then
    fact address FAIL "an address is in a committed report, and D323 says none may be: $(head -3 "${SCRATCH}/address.txt" | tr '\n' ' ')"
  else
    fact address PASS "no address in any committed report (D323)"
  fi
  # The brief this check launched with is the only task text in play, so a
  # report that carried task text would carry this line.
  if git grep -nF "$BRIEF" FETCH_HEAD -- docs/fugaro-reviews >"${SCRATCH}/task.txt" 2>/dev/null; then
    fact task-text FAIL "a committed report carries the launch's task text: $(head -3 "${SCRATCH}/task.txt" | tr '\n' ' ')"
  else
    fact task-text PASS "no committed report carries the launch's task text"
  fi
}

# step_figures compares one due DAY file's own summary block with the CLI's live
# report for the same day. Both are internal/digest's Summary, marshalled the
# same way, so the comparison is by value and not by prose (G3).
step_figures() {
  local day
  day=$(grep -E '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' "${SCRATCH}/due.txt" | LC_ALL=C sort | tail -1 || true)
  if [ -z "$day" ]; then
    fact figures UNVERIFIED "no day period is due, so there is no day file to compare: the CLI's due rule wrote $(tr '\n' ' ' <"${SCRATCH}/due.txt")"
    return 0
  fi
  git show "FETCH_HEAD:docs/fugaro-reviews/${day:0:4}/${day}.md" >"${SCRATCH}/day.md" 2>/dev/null ||
    { fact figures FAIL "the day file docs/fugaro-reviews/${day:0:4}/${day}.md is not on the branch"; return 0; }
  "$FUGARO" runs digest --period day --date "$day" --repo "$REPO" --json --dir "${SCRATCH}/base/docs/fugaro-reviews" >"${SCRATCH}/live.json" 2>"${SCRATCH}/live.err" ||
    { fact figures UNVERIFIED "the CLI could not report the same day (runs digest --period day --date $day --json): $(head -c 300 "${SCRATCH}/live.err")"; return 0; }
  local cmp
  cmp=$(python3 - "${SCRATCH}/day.md" "${SCRATCH}/live.json" "$day" <<'PY'
import json,re,sys
body=open(sys.argv[1],encoding="utf-8").read()
day=sys.argv[3]
m=re.search(r"```json\n(.*?)\n```", body, re.S)
if not m:
    print("FAIL the committed day file carries no fenced json summary block")
    sys.exit(0)
try: have=json.loads(m.group(1))
except Exception as e:
    print("FAIL the committed day file's summary block does not parse: %s" % e); sys.exit(0)
live=json.load(open(sys.argv[2],encoding="utf-8")).get("summary") or {}
keys=("period","runs","billed","notional","review","halts")
bad=[k for k in keys if k in live and have.get(k)!=live.get(k)]
if bad:
    print("FAIL the committed day file's figures differ from the CLI's own for the same day: %s" % ", ".join("%s committed=%r cli=%r" % (k,have.get(k),live.get(k)) for k in bad))
else:
    print("PASS the day file's figures equal the CLI's own for %s: runs %s, billed %s" % (day,have.get("runs"),have.get("billed")))
PY
)
  case "$cmp" in
  PASS*) fact figures PASS "${cmp#PASS }" ;;
  *) fact figures FAIL "${cmp#FAIL }" ;;
  esac
}

# step_samples takes up to two runs the day file names and reads each one back
# with `fugaro diagnose`: the report prints a run's short ID, and diagnose takes
# the last four or more characters of one, refusing an ending two runs share —
# which a busy day reaches, and which is a refusal about the reading, not a
# finding about the day.
step_samples() {
  local ids id full out bad="" seen=0
  ids=$(grep -oE '`[0-9a-f]{4,}`' "${SCRATCH}/day.md" 2>/dev/null | tr -d '`' | awk '!seen[$0]++' | head -2 || true)
  [ -n "$ids" ] || { fact samples UNVERIFIED "the day file names no run, so there is no row to read back with $FUGARO diagnose"; return 0; }
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    out=$("$FUGARO" diagnose "$id" --json 2>/dev/null) || { bad="${bad}${bad:+, }$id (diagnose refused)"; continue; }
    full=$(printf '%s' "$out" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("row") or {}).get("run_id",""))' 2>/dev/null)
    if [ -z "$full" ]; then
      bad="${bad}${bad:+, }$id (no row)"
    else
      seen=$((seen + 1))
    fi
  done <<<"$ids"
  if [ -n "$bad" ]; then
    fact samples UNVERIFIED "the day file names runs $FUGARO diagnose will not read back by their short id: $bad — a short id two runs share is refused by design. Read the day's own run ids with $FUGARO runs ls --json --repo $REPO and diagnose one in full: $FUGARO diagnose <run-id> --json"
  else
    fact samples PASS "$seen run(s) the day file names read back with $FUGARO diagnose --json"
  fi
}

step_restore() {
  if [ "${RUN_PR:-0}" -gt 0 ]; then
    fact pull-request INFO "PR #${RUN_PR} ${PR_URL:-} is yours to decide: merge it, or decline it and delete the branch $BRANCH"
  else
    fact pull-request INFO "no pull request was opened"
  fi
}

# ------------------------------------------------------------------ preflight
# --preflight reads every precondition the check reads and prints all blockers
# at once, with the fix; it launches nothing, writes nothing and creates nothing.
step_preflight() {
  local doc name project out
  doc=$(doctor_identity) || {
    preflight_block doctor "fugaro doctor --json did not answer, so the installation cannot be read" "$FUGARO doctor --json"
    return 0
  }
  name=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("name",""))' 2>/dev/null)
  project=$(printf '%s' "$doc" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("project") or {}).get("gcp_project",""))' 2>/dev/null)
  case "$name${project}" in
  *belong* | *edgeweb* | *edgeserver* | *Belong* | *EdgeWeb* | *EdgeServer*)
    preflight_block installation "the installation is ${name:-?} in ${project:-?}, and Check 136 never runs against belong, EdgeWeb or EdgeServer" "run this check on the sandbox installation $INSTALLATION in $GCP_PROJECT"
    ;;
  esac
  [ "$name" = "$INSTALLATION" ] || preflight_block installation "the installation is ${name:-nothing}, and this check runs on $INSTALLATION only" "fugaro doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  [ "$project" = "$GCP_PROJECT" ] || preflight_block gcp-project "the GCP project is ${project:-nothing}, and this check runs in $GCP_PROJECT only" "fugaro doctor on the installation $INSTALLATION (GCP $GCP_PROJECT)"
  if git remote get-url origin >/dev/null 2>&1; then
    preflight_note repository "$(git remote get-url origin) is the checkout's origin"
  else
    preflight_block repository "this directory is not a checkout of a repository" "run this check in the sandbox repository's checkout"
  fi
  out=$("$FUGARO" recipes show run-digest 2>&1) || {
    preflight_block recipe "this CLI cannot resolve the recipe run-digest" "$FUGARO recipes show run-digest"
    out=""
  }
  case "$out" in
  *"from catalog"*) preflight_note recipe "run-digest resolves from the catalog" ;;
  "") ;;
  *) preflight_block recipe "run-digest does not resolve from the catalog here (a recipe shadows it)" "$FUGARO recipes list" ;;
  esac
  [ "${CHECK136_AFTER_T33:-}" = yes ] || preflight_block t33 "T3.3's finalize guard is not confirmed for the job image your runs use" "export CHECK136_AFTER_T33=yes (only once T3.3 is in the release your job image runs)"
}

# ----------------------------------------------------------------------- main
main() {
  if [ "$PREFLIGHT" = yes ]; then
    # The preflight makes the same live reads the check does, so it refuses a
    # coding agent's session the way the check does.
    : "${LIVE_CHECK_LIB:=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib}"
    # shellcheck source=lib/preflight.sh
    . "$LIVE_CHECK_LIB/preflight.sh"
    PREFLIGHT_TOOL="Check 136"
    preflight_agent_session
    guard_xtrace
    need_tools
    step_preflight
    preflight_report
  fi

  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check136.XXXXXX") || stop "could not make a scratch directory"
  chmod 700 "$SCRATCH"
  FACTS="$SCRATCH/facts"
  : >"$FACTS"

  guard_xtrace
  guard_agent
  need_tools
  guard_installation
  guard_t33
  sandbox_repo
  step_recipe
  step_launch
  step_settle
  step_files
  step_branch
  step_text
  step_figures
  step_samples
  step_restore
  REACHED_END=yes
}

main "$@"
