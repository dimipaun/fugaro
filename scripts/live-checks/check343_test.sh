#!/usr/bin/env bash
# Tests for scripts/live-checks/check343.sh, with fake `fugaro`, `gcloud`, `gh`
# and `git` on PATH: NOTHING here reaches a bucket, a cloud, GitHub or a network.
#
# The fakes are STRICT: the fake `fugaro` validates every command and flag
# against the real CLI's own surface (lib/strict.sh over lib/fugaro-surface.tsv,
# generated from cli.NewRootCmd by scripts/live_check_surface_test.go), and the
# fake gcloud and gh refuse a subcommand or flag their allow-lists do not name
# (lib/allow/check343.gcloud, lib/allow/check343.gh). A check that called
# `fugaro run --commit-reports` on a recipe that moved, or `gcloud storage cat`,
# fails HERE rather than on your terminal.
#
# Usage: bash scripts/live-checks/check343_test.sh
#        CHECK_TEST_ONLY=test_the_whole_check_passes bash scripts/live-checks/check343_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check343.sh
export LIVE_CHECK_LIB=$here/lib
# shellcheck source=lib/strict.sh
. "$LIVE_CHECK_LIB/strict.sh"

work=$(mktemp -d)
state=$work/state
cleanup_all() {
  if [ -n "${CHECK343_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() {
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 800)" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() {
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; block: $(printf '%s' "${out:-}" | head -c 700) stderr: $(printf '%s' "${err:-}" | head -c 400)"
}

# --------------------------------------------------------------------- fakes
mkdir -p "$work/bin" "$work/home" "$work/tmp"
chmod 700 "$work/tmp"

cat >"$work/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check343.sh's test only. It
# validates its argv against the REAL CLI's surface first, then answers the read
# out of the case's own state directory.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s\n' "$*" >>"${FAKE343_STATE:?}/calls"
case "${1:-} ${2:-}" in
"doctor --json") cat "$FAKE343_STATE/doctor.json" ;;
"project policy") cat "$FAKE343_STATE/policy.json" ;;
"run --recipe")
  # The run a launch answers for, in the order the case's base state lets the
  # check make them: a base that still lacks a stored report never reaches step
  # 4's launch, so the case's own order is what the check sees.
  n=$(grep -c '^fugaro run ' "$FAKE343_STATE/calls")
  pick=$(awk -v n="$n" '{print $n; exit}' "$FAKE343_STATE/launch_order")
  cat "$FAKE343_STATE/launch${pick:-$n}.json" ;;
"runs ls") cat "$FAKE343_STATE/ls.json" ;;
"logs "*) cat "$FAKE343_STATE/logs.txt" ;;
*)
  echo "fugaro: unexpected read: $*" >&2
  exit 1
  ;;
esac
FUGARO_EOF
chmod +x "$work/bin/fugaro"

cat >"$work/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check343.sh's test only: it
# refuses an unknown subcommand or flag through the strict allow-list.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_gcloud_verify "${FAKE343_ALLOW:?}/check343.gcloud" "$@" || exit $?
printf 'gcloud %s\n' "$*" >>"${FAKE343_STATE:?}/calls"
case "${1:-} ${2:-}" in
"storage ls") cat "$FAKE343_STATE/store.out" ;;
*) echo "gcloud: unexpected read: $*" >&2; exit 1 ;;
esac
GCLOUD_EOF
chmod +x "$work/bin/gcloud"

cat >"$work/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check343.sh's test only.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_gh_verify "${FAKE343_ALLOW:?}/check343.gh" "$@" || exit $?
printf 'gh %s\n' "$*" >>"${FAKE343_STATE:?}/calls"
case "${1:-} ${2:-}" in
"pr view")
  n=$3
  if [ -s "$FAKE343_STATE/pr$n.json" ]; then cat "$FAKE343_STATE/pr$n.json"; else cat "$FAKE343_STATE/pr1.json"; fi
  ;;
"pr diff")
  n=$3
  if [ -s "$FAKE343_STATE/pr$n.diff" ]; then cat "$FAKE343_STATE/pr$n.diff"; else cat "$FAKE343_STATE/empty.diff"; fi
  ;;
*) echo "gh: unexpected read: $*" >&2; exit 1 ;;
esac
GH_EOF
chmod +x "$work/bin/gh"

cat >"$work/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check343.sh's test only: it
# answers a read out of the case's own state directory and says no to anything
# else, so a check that fetched or walked the tree would fail here.
set -euo pipefail
printf 'git %s\n' "$*" >>"${FAKE343_STATE:?}/calls"
case "${1:-}" in
remote) cat "$FAKE343_STATE/origin" ;;
fetch) exit 0 ;;
symbolic-ref) echo "origin/main" ;;
rev-parse)
  last=${!#}
  case "$last" in
  *:*) path=${last#*:} ;;
  *) exit 0 ;;
  esac
  if grep -qxF "$path" "$FAKE343_STATE/onbranch.txt"; then exit 0; fi
  exit 1
  ;;
ls-tree) cat "$FAKE343_STATE/base.txt" ;;
*) exit 1 ;;
esac
GIT_EOF
chmod +x "$work/bin/git"

# ------------------------------------------------------------------- fixtures
REPO=acme/reports-mirror
DIR=docs/fugaro-reviews-mirror
RUN1=20261124-101010-aaaa
RUN2=20261124-111111-bbbb
RUN3=20261124-121212-cccc
STRAY=$DIR/2026/2026-11-21.md

reset_state() {
  rm -rf "$state"
  mkdir -p "$state" "$work/tmp"
  : >"$state/calls"
  echo "1 2 3" >"$state/launch_order"
  echo "git@github.com:acme/reports-mirror.git" >"$state/origin"
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  cat >"$state/policy.json" <<EOF
{"project":"fugaro","policy":{"version":10,"reports_commit":{"repo":"$REPO","dir":"$DIR"}}}
EOF
  cat >"$state/store.out" <<'EOF'
gs://fugaro-runs-fugaro-dev/reports/acme-sandbox/2026/2026-11-22.md
gs://fugaro-runs-fugaro-dev/reports/2026/2026-W46.md
EOF
  cat >"$state/base.txt" <<EOF
$DIR/acme-sandbox/2026/2026-11-22.md
$STRAY
$DIR/README.md
EOF
  cat >"$state/onbranch.txt" <<EOF
$DIR/acme-sandbox/2026/2026-11-22.md
$STRAY
$DIR/README.md
EOF
  cat >"$state/launch1.json" <<EOF
{"project":"fugaro","run_id":"$RUN1","repo":"$REPO","branch":"fugaro/$RUN1","status":"launched","recipe":{"name":"commit-reports","source":"catalog"}}
EOF
  cat >"$state/launch2.json" <<EOF
{"project":"fugaro","run_id":"$RUN2","repo":"$REPO","branch":"fugaro/$RUN2","status":"launched","recipe":{"name":"commit-reports","source":"catalog"}}
EOF
  cat >"$state/launch3.json" <<EOF
{"project":"fugaro","run_id":"$RUN3","repo":"$REPO","branch":"fugaro/$RUN3","status":"launched","recipe":{"name":"commit-reports","source":"catalog"}}
EOF
  ls_doc "$RUN1 ready 41" "$RUN2 ready 0" "$RUN3 ready 42"
  pr_doc 41 "$DIR/2026/2026-W46.md added" "$DIR/README.md modified"
  pr_doc 42 "$DIR/README.md modified"
  : >"$state/empty.diff"
  cat >"$state/logs.txt" <<EOF
the report folder holds reports this run was not handed; they are left alone, because the store only adds and overwrites and a run never takes a report away paths=$STRAY
EOF
}

ls_doc() { # ls_doc "RUN STATUS PR" ...
  python3 - "$state" "$@" <<'PY'
import json,os,sys
state=sys.argv[1]
runs=[]
for spec in sys.argv[2:]:
    run,status,pr=spec.split()
    runs.append({"run_id":run,"status":status,"terminal":True,"pr":int(pr)})
json.dump({"project":"fugaro","repo":"acme/reports-mirror","runs":runs,"totals":{"runs":len(runs)}},
          open(os.path.join(state,"ls.json"),"w"))
PY
}

pr_doc() { # pr_doc NUMBER "PATH TYPE" ...
  python3 - "$state" "$@" <<'PY'
import json,os,sys
state,n=sys.argv[1],sys.argv[2]
files=[]
for spec in sys.argv[3:]:
    p,ct=spec.rsplit(" ",1)
    files.append({"path":p,"changeType":ct})
json.dump({"number":int(n),"url":"https://github.com/acme/reports-mirror/pull/%s"%n,"state":"OPEN","isDraft":False,"files":files},
          open(os.path.join(state,"pr%s.json"%n),"w"))
PY
}

run_check() { # run_check [args...] -> sets $out $status $err
  local name=$$
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" HOME="$work/home" TMPDIR="$work/tmp" \
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE343_STATE="$state" FAKE343_ALLOW="$LIVE_CHECK_LIB/allow" \
    CHECK343_SETTLE_SECONDS=5 CHECK343_POLL_SECONDS=0 \
    ${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/stderr.$name")
  status=$?
  set -e
  err=$(cat "$work/stderr.$name")
}

calls() { cat "$state/calls" 2>/dev/null || true; }

# The runs a case's base state lets the check launch: over a base that still
# lacks a stored report the check makes step 2's launch and step 5's, and step
# 4's is not judged.
launches() { echo "$*" >"$state/launch_order"; }

# ---------------------------------------------------------------------- cases
test_the_help_lists_the_one_command() {
  reset_state
  run_check --help
  expect_status "$status" 0 "--help exits 0"
  expect_contains "$out" "bash scripts/live-checks/check343.sh" "--help prints the usage line"
  expect_contains "$out" '--commit-reports "commit the stored reports"' "--help names the one command"
  expect_contains "$out" "CHECK 343 RESULT" "--help's header names the block"
}

test_refuses_a_coding_agents_session() {
  reset_state
  EXTRA_ENV=(CLAUDECODE=1)
  run_check
  unset EXTRA_ENV
  expect_status "$status" 2 "an agent session is refused with a stop"
  expect_contains "$err" "coding agent's session" "the refusal names what it is"
  expect_absent "$(calls)" "fugaro run " "nothing was launched"
}

test_refuses_belong() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"belong-prod","region":"us-east5"}}
EOF
  run_check
  expect_status "$status" 2 "belong is refused"
  expect_contains "$err" "never runs against belong" "the refusal names belong"
}

test_refuses_another_installation() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"scratch","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  run_check
  expect_status "$status" 2 "another installation is refused"
  expect_contains "$err" "runs on fugaro only" "the refusal names the sandbox installation"
}

test_refuses_when_no_reports_commit_is_designated() {
  reset_state
  cat >"$state/policy.json" <<'EOF'
{"project":"fugaro","policy":{"version":10}}
EOF
  run_check
  expect_status "$status" 2 "a project that designates nothing commits nowhere"
  expect_contains "$err" "designates no reports_commit" "the refusal names the key"
  expect_contains "$err" "project policy publish" "with the fix"
  expect_absent "$(calls)" "fugaro run " "nothing was launched"
}

test_refuses_a_designation_that_names_another_repository() {
  reset_state
  cat >"$state/policy.json" <<'EOF'
{"project":"fugaro","policy":{"version":10,"reports_commit":{"repo":"acme/elsewhere","dir":"docs/x"}}}
EOF
  run_check
  expect_status "$status" 2 "the run commits into the designated repository only"
  expect_contains "$err" "designates acme/elsewhere" "the refusal names the designation"
  expect_absent "$(calls)" "fugaro run " "nothing was launched"
}

test_stops_when_the_store_holds_nothing() {
  reset_state
  : >"$state/store.out"
  run_check
  expect_status "$status" 2 "an empty store has nothing to hand in"
  expect_contains "$err" "holds no stored report" "the refusal says so"
  expect_absent "$(calls)" "fugaro run " "nothing was launched"
}

test_the_commit_run_passes_and_the_empty_case_waits_for_the_merge() {
  reset_state
  launches 1 3
  run_check --stray "$STRAY"
  expect_status "$status" 3 "step 4 is not judged over a base that still lacks a stored report"
  expect_contains "$out" "CHECK 343 RESULT: INCOMPLETE (exit 3)" "the block's verdict"
  expect_contains "$out" "FACT designation: PASS" "the designation is the checkout's repository"
  expect_contains "$out" "FACT store: PASS" "the store's listing reads back"
  expect_contains "$out" "FACT mirror: PASS" "the store and the checkout's folder are compared"
  expect_contains "$out" "FACT files: PASS" "the pull request carries exactly what the commit had to"
  expect_contains "$out" "FACT nothing-new: UNVERIFIED" "step 4 says it was not judged here"
  expect_contains "$out" "not judged here" "and that the base is why"
  expect_contains "$out" "merge pull request #41" "with the line that reaches it"
  expect_contains "$out" "FACT stray: PASS" "the folder's own file survives and is named"
  expect_contains "$out" "FACT restore: PASS" "nothing the check creates has to be put back"
  expect_absent "$out" "not reached" "every row was reached"
  [ "$(grep -c '^fugaro run ' "$state/calls")" = 2 ] ||
    fail "a base behind the store costs step 2's run and step 5's, and step 4's was not launched: calls: $(calls)"
}

test_the_empty_case_passes_over_a_base_that_holds_the_reports() {
  reset_state
  cat >"$state/base.txt" <<EOF
$DIR/acme-sandbox/2026/2026-11-22.md
$DIR/2026/2026-W46.md
$DIR/README.md
EOF
  ls_doc "$RUN1 ready 0" "$RUN2 ready 0"
  run_check
  expect_status "$status" 3 "step 3 verifies nothing over a base that already holds the reports"
  expect_contains "$out" "FACT nothing-new: PASS" "the run over a store with nothing new opened no pull request"
  expect_contains "$out" "FACT files: UNVERIFIED" "step 3 says the commit had nothing to do"
  [ "$(grep -c '^fugaro run ' "$state/calls")" = 2 ] ||
    fail "a base that holds the reports costs step 2's run and step 4's: calls: $(calls)"
}

test_fails_on_a_file_the_store_does_not_hold() {
  reset_state
  pr_doc 41 "$DIR/2026/2026-W46.md added" "$DIR/README.md modified" "$DIR/2026/2026-11-01.md added"
  run_check
  expect_status "$status" 1 "a report the store does not have is a finding"
  expect_contains "$out" "FACT files: FAIL" "the row fails"
  expect_contains "$out" "not in the store" "and says why"
}

test_fails_on_a_report_the_checkout_lacked() {
  reset_state
  pr_doc 41 "$DIR/README.md modified"
  run_check
  expect_status "$status" 1 "a stored report the folder lacks and the branch does not gain is a finding"
  expect_contains "$out" "FACT files: FAIL" "the row fails"
  expect_contains "$out" "missing $DIR/2026/2026-W46.md" "and names it"
}

test_fails_when_the_first_run_opens_no_pull_request() {
  reset_state
  ls_doc "$RUN1 ready 0" "$RUN2 ready 0"
  run_check
  expect_status "$status" 1 "a checked-out-behind mirror with no pull request is a finding"
  expect_contains "$out" "FACT files: FAIL" "the row fails"
  expect_contains "$out" "opened no pull request" "and says so"
  expect_contains "$out" "FACT nothing-new: UNVERIFIED" "and step 4 is still not judged over this base"
  expect_contains "$out" "bring origin/main up to the store yourself" "with a line that does not name a pull request there is none of"
  [ "$(grep -c '^fugaro run ' "$state/calls")" = 1 ] || fail "step 4's run is not launched: calls: $(calls)"
}

test_a_rewritten_report_is_not_a_file_the_store_does_not_hold() {
  reset_state
  pr_doc 41 "$DIR/README.md modified" "$DIR/acme-sandbox/2026/2026-11-22.md modified" "$DIR/2026/2026-W46.md added"
  run_check
  expect_status "$status" 3 "a report the store overwrote is a report the store holds"
  expect_contains "$out" "FACT files: PASS" "the row passes"
}

test_is_unverified_when_the_mirror_already_holds_everything() {
  reset_state
  cat >"$state/base.txt" <<EOF
$DIR/acme-sandbox/2026/2026-11-22.md
$DIR/2026/2026-W46.md
$DIR/README.md
EOF
  ls_doc "$RUN1 ready 0" "$RUN2 ready 0"
  run_check
  expect_status "$status" 3 "a caught-up checkout verifies nothing, and says so"
  expect_contains "$out" "FACT files: UNVERIFIED" "the row says the check could not see the commit"
  expect_contains "$out" "FACT nothing-new: PASS" "and the run over it opened no pull request"
}

test_fails_when_the_second_run_opens_a_pull_request() {
  reset_state
  cat >"$state/base.txt" <<EOF
$DIR/acme-sandbox/2026/2026-11-22.md
$DIR/2026/2026-W46.md
$DIR/README.md
EOF
  ls_doc "$RUN1 ready 0" "$RUN2 ready 43"
  run_check
  expect_status "$status" 1 "a second run over nothing new is a finding"
  expect_contains "$out" "FACT nothing-new: FAIL" "the row fails"
  expect_contains "$out" "nothing new in it" "and says why"
}

test_fails_when_the_stray_file_is_not_on_the_base_branch() {
  reset_state
  run_check --stray "$DIR/2026/2026-11-19.md"
  expect_status "$status" 1 "a file only the working tree holds cannot be seen by the run"
  expect_contains "$out" "FACT stray: FAIL" "the row fails"
  expect_contains "$out" "not on origin/main" "and says why"
}

test_fails_when_the_run_deletes_the_stray_file() {
  reset_state
  launches 1 3
  cat >"$state/pr42.diff" <<EOF
diff --git a/$STRAY b/$STRAY
deleted file mode 100644
index 1111111..0000000
--- a/$STRAY
+++ /dev/null
@@ -1 +0,0 @@
-# a report by hand
EOF
  run_check --stray "$STRAY"
  expect_status "$status" 1 "a file the store does not hold is never taken away"
  expect_contains "$out" "FACT stray: FAIL" "the row fails"
  expect_contains "$out" "deleted" "and says what it read"
}

test_is_unverified_when_the_run_log_does_not_answer() {
  reset_state
  launches 1 3
  : >"$state/logs.txt"
  run_check --stray "$STRAY"
  expect_status "$status" 3 "a log that answered nothing is UNVERIFIED, not a finding"
  expect_contains "$out" "FACT stray: UNVERIFIED" "the row says the log did not answer"
}

test_fails_when_the_run_does_not_name_the_stray_file() {
  reset_state
  launches 1 3
  echo "fugaro: committed docs/fugaro-reviews-mirror/README.md" >"$state/logs.txt"
  run_check --stray "$STRAY"
  expect_status "$status" 1 "a folder-only file the run leaves unnamed is a finding"
  expect_contains "$out" "FACT stray: FAIL" "the row fails"
  expect_contains "$out" "does not name" "and says so"
}

test_records_step_five_as_not_run_without_a_stray_file() {
  reset_state
  run_check
  expect_status "$status" 3 "steps 2 to 4 ran, with step 4 not judged over this base"
  expect_contains "$out" "FACT stray: INFO" "step 5 is recorded as not run"
  expect_contains "$out" "step 5 was not run" "with how to run it"
}

test_the_fake_refuses_a_flag_the_cli_does_not_have() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE343_STATE="$state" \
    "$work/bin/fugaro" run --nope 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a flag the real CLI does not take"
  expect_contains "$out" "unknown flag: --nope" "with the CLI's own wording"
}

test_the_gcloud_fake_refuses_a_flag_its_allow_list_does_not_name() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE343_STATE="$state" \
    FAKE343_ALLOW="$LIVE_CHECK_LIB/allow" "$work/bin/gcloud" storage ls --recursive --nope gs://x/y 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a flag the check does not make"
  expect_contains "$out" "unknown flag: --nope" "and says which"
}

test_preflight_blocks_on_a_missing_designation_and_launches_nothing() {
  reset_state
  cat >"$state/policy.json" <<'EOF'
{"project":"fugaro","policy":{"version":10}}
EOF
  run_check --preflight
  expect_status "$status" 1 "a blocker exits non-zero"
  expect_contains "$err" "BLOCKED designation" "the blocker is printed"
  expect_contains "$err" "fix:" "with the fix"
  expect_absent "$(calls)" "fugaro run " "nothing was launched"
}

test_preflight_passes_and_launches_nothing() {
  reset_state
  run_check --preflight
  expect_status "$status" 0 "a good snapshot passes"
  expect_contains "$err" "preflight passed" "and says so"
  expect_absent "$(calls)" "fugaro run " "nothing was launched"
  expect_absent "$(calls)" "gh pr view" "nothing was read from GitHub"
}

# ----------------------------------------------------------------------- runner
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_help_lists_the_one_command
t test_refuses_a_coding_agents_session
t test_refuses_belong
t test_refuses_another_installation
t test_refuses_when_no_reports_commit_is_designated
t test_refuses_a_designation_that_names_another_repository
t test_stops_when_the_store_holds_nothing
t test_the_commit_run_passes_and_the_empty_case_waits_for_the_merge
t test_the_empty_case_passes_over_a_base_that_holds_the_reports
t test_fails_on_a_file_the_store_does_not_hold
t test_fails_on_a_report_the_checkout_lacked
t test_fails_when_the_first_run_opens_no_pull_request
t test_a_rewritten_report_is_not_a_file_the_store_does_not_hold
t test_is_unverified_when_the_mirror_already_holds_everything
t test_fails_when_the_second_run_opens_a_pull_request
t test_fails_when_the_stray_file_is_not_on_the_base_branch
t test_fails_when_the_run_deletes_the_stray_file
t test_is_unverified_when_the_run_log_does_not_answer
t test_fails_when_the_run_does_not_name_the_stray_file
t test_records_step_five_as_not_run_without_a_stray_file
t test_the_fake_refuses_a_flag_the_cli_does_not_have
t test_the_gcloud_fake_refuses_a_flag_its_allow_list_does_not_name
t test_preflight_blocks_on_a_missing_designation_and_launches_nothing
t test_preflight_passes_and_launches_nothing

if [ "$failures" -ne 0 ]; then
  echo "check343_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check343_test.sh: ok"
