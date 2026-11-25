#!/usr/bin/env bash
# Tests for scripts/live-checks/check342.sh, with a fake `fugaro` on PATH:
# NOTHING here reaches a bucket, a cloud, GitHub or the network.
#
# The fake is STRICT: it validates every command and flag against the real CLI's
# own surface (lib/strict.sh over lib/fugaro-surface.tsv, generated from
# cli.NewRootCmd by scripts/live_check_surface_test.go). A check that called
# `fugaro runs digest --stored K --period day` -- a pair the real command
# refuses -- fails HERE rather than on your terminal.
#
# Usage: bash scripts/live-checks/check342_test.sh
#        CHECK_TEST_ONLY=test_the_whole_check_passes bash scripts/live-checks/check342_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check342.sh
export LIVE_CHECK_LIB=$here/lib
# shellcheck source=lib/strict.sh
. "$LIVE_CHECK_LIB/strict.sh"

work=$(mktemp -d)
state=$work/state
cleanup_all() {
  if [ -n "${CHECK342_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; block: $(printf '%s' "${out:-}" | head -c 500) stderr: $(printf '%s' "${err:-}" | head -c 500)"
}

# --------------------------------------------------------------------- fakes
mkdir -p "$work/bin" "$work/home" "$work/tmp"
chmod 700 "$work/tmp"

cat >"$work/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check342.sh's test only. It
# validates its argv against the REAL CLI's surface first, then answers the read
# out of the case's own state directory.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s\n' "$*" >>"${FAKE342_STATE:?}/calls"
case "${1:-} ${2:-}" in
"doctor --json") cat "$FAKE342_STATE/doctor.json" ;;
"runs digest")
  [ -s "$FAKE342_STATE/stored.md" ] || { echo "fugaro: no report of $4 is stored for acme/sandbox" >&2; exit 1; }
  cat "$FAKE342_STATE/stored.md"
  ;;
"runs ls") cat "$FAKE342_STATE/live.json" ;;
*)
  echo "fugaro: unexpected read: $*" >&2
  exit 1
  ;;
esac
FUGARO_EOF
chmod +x "$work/bin/fugaro"

# ------------------------------------------------------------------- fixtures
KEY=2026-11-23
REPO=acme/sandbox

report() { # report CLASS RUNS -> a stored report carrying one Failures row
  cat <<EOF
# Report $KEY

## Failures

| class | runs | billed | example |
| --- | --- | --- | --- |
| \`$1\` | $2 | \$0.400000 | \`abcd\` |

## Summary

\`\`\`json
{"period":"$KEY","start":"${KEY}T00:00:00Z","end":"${KEY}T23:59:59Z","runs":3,"shown":3,"flagged":0,"billed":"\$0.400000","notional":"\$0.000000","review":"\$0.000000","halts":0,"failures":{"$1":$2}}
\`\`\`
EOF
}

live() { # live CLASS RUNS [DERIVED]
  python3 - "$state" "$1" "$2" "${3:-}" <<'PY'
import json,os,sys
state,cls,runs,derived=sys.argv[1],sys.argv[2],int(sys.argv[3]),sys.argv[4]
r=[{"run_id":"20261123-101010-abcd","status":"failed","terminal":True,"failure_class":cls}]
if derived: r[0]["derived"]=derived
rows=[{"run_id":"20261123-090000-bbbb","status":"ready","terminal":True},
      {"run_id":"20261123-080000-cccc","status":"halted","terminal":True}]
json.dump({"project":"fugaro","repo":"acme/sandbox","runs":r+rows,
           "totals":{"runs":3},
           "groups":[{"class":cls,"runs":runs,"share":0.34,"spend_usd":0.4}]},
          open(os.path.join(state,"live.json"),"w"))
PY
}

reset_state() {
  rm -rf "$state"
  mkdir -p "$state" "$work/tmp"
  : >"$state/calls"
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  report tests_failed 2 >"$state/stored.md"
  live tests_failed 2
}

run_check() { # run_check [args...] -> sets $out $status $err
  local name=$$
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" HOME="$work/home" TMPDIR="$work/tmp" \
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE342_STATE="$state" \
    CHECK342_KEY="$KEY" CHECK342_REPO="$REPO" \
    ${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"} \
    bash "$script" --key "$KEY" --repo "$REPO" "$@" 2>"$work/stderr.$name")
  status=$?
  set -e
  err=$(cat "$work/stderr.$name")
}

calls() { cat "$state/calls" 2>/dev/null || true; }

# ---------------------------------------------------------------------- cases
test_the_help_lists_the_one_command() {
  reset_state
  run_check --help
  expect_status "$status" 0 "--help exits 0"
  expect_contains "$out" "bash scripts/live-checks/check342.sh" "--help prints the usage line"
  expect_contains "$out" "CHECK 342 RESULT" "--help's header names the block"
}

test_refuses_a_coding_agents_session() {
  reset_state
  EXTRA_ENV=(CLAUDECODE=1)
  run_check
  unset EXTRA_ENV
  expect_status "$status" 2 "an agent session is refused with a stop"
  expect_contains "$err" "coding agent's session" "the refusal names what it is"
  expect_absent "$(calls)" "fugaro runs" "nothing was read before the refusal"
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

test_refuses_a_bad_period_key() {
  reset_state
  run_check --key 2026-13-99
  expect_status "$status" 2 "a key this CLI does not write stops the check"
  expect_contains "$err" "not a period key this CLI writes" "and says so"
}

test_the_whole_check_passes() {
  reset_state
  run_check
  expect_status "$status" 0 "the two tables agree"
  expect_contains "$out" "CHECK 342 RESULT: PASS (exit 0)" "the block's verdict"
  expect_contains "$out" "FACT window: PASS" "the period and the live window are named"
  expect_contains "$out" "FACT stored: PASS" "the stored report reads back"
  expect_contains "$out" "FACT live: PASS" "the grouped listing reads back"
  expect_contains "$out" "FACT compare: PASS" "the tables agree"
  expect_contains "$out" "FACT your-account: INFO" "the third table is the owner's"
  expect_contains "$out" "FACT restore: PASS" "nothing was created and the scratch is gone"
  expect_absent "$out" "not reached" "every row was reached"
}

test_fails_when_the_window_holds_fewer_runs_than_the_report() {
  reset_state
  live tests_failed 1
  run_check
  expect_status "$status" 1 "the window has to account for every run the report counted"
  expect_contains "$out" "FACT compare: FAIL" "the row fails"
  expect_contains "$out" "tests_failed" "and names the class"
}

test_fails_when_the_window_names_no_run_of_a_reports_class() {
  reset_state
  live branch_moved 1
  run_check
  expect_status "$status" 1 "a class the report counted and the window cannot see is a finding"
  expect_contains "$out" "FACT compare: FAIL" "the row fails"
}

test_a_class_the_window_holds_alone_is_not_a_finding() {
  reset_state
  # The window bounds by a run's start and the report by its finish, so the
  # window holds the runs that have finished since the period closed too: a
  # class only the window names is the window's own, not a disagreement.
  python3 - "$state" <<'PY'
import json,os,sys
state=sys.argv[1]
d=json.load(open(os.path.join(state,"live.json")))
d["groups"].append({"class":"branch_moved","runs":1,"share":0.1,"spend_usd":0.1})
json.dump(d,open(os.path.join(state,"live.json"),"w"))
PY
  run_check
  expect_status "$status" 0 "a class the window holds alone is the window's own"
  expect_contains "$out" "FACT compare: PASS" "the row passes"
}

test_fails_when_no_report_is_stored_for_the_period() {
  reset_state
  : >"$state/stored.md"
  run_check
  expect_status "$status" 1 "a period with no stored report is a finding"
  expect_contains "$out" "FACT stored: FAIL" "the row fails"
  expect_contains "$out" "no report of" "with the CLI's own words"
}

test_is_unverified_when_the_listing_does_not_answer() {
  reset_state
  rm -f "$state/live.json"
  run_check
  expect_status "$status" 3 "a read that did not answer is UNVERIFIED, not a finding"
  expect_contains "$out" "FACT live: UNVERIFIED" "the row says the read did not answer"
}

test_the_legacy_row_counts_the_classed_runs() {
  reset_state
  live tests_failed 2 preempted
  run_check
  expect_status "$status" 0 "the derived run reads as the same class"
  expect_contains "$out" "ended in a class of the vocabulary" "the row counts the classed runs"
  expect_contains "$out" "FC5" "and names the derivation's own pin"
}

test_the_fake_refuses_a_flag_the_cli_does_not_have() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE342_STATE="$state" \
    "$work/bin/fugaro" runs digest --stored "$KEY" --nope 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a pair the real CLI refuses"
  expect_contains "$out" "unknown flag: --nope" "with the CLI's own wording"
}

test_the_fake_accepts_the_reads_the_check_makes() {
  reset_state
  run_check
  expect_status "$status" 0 "the reads are on the real CLI's surface"
  expect_contains "$(calls)" "fugaro runs digest --stored $KEY --repo $REPO" "the stored read"
  expect_contains "$(calls)" "fugaro runs ls --json --repo $REPO --since" "the grouped listing"
  expect_contains "$(calls)" "--group-by failure_class" "with the vocabulary's own flag"
}

test_preflight_blocks_without_a_stored_report_and_writes_nothing() {
  reset_state
  : >"$state/stored.md"
  run_check --preflight
  expect_status "$status" 1 "a blocker exits non-zero"
  expect_contains "$err" "BLOCKED stored" "the blocker is printed"
  expect_contains "$err" "fix:" "with the fix"
  expect_absent "$(calls)" "fugaro runs ls" "the listing was not read"
}

test_preflight_blocks_on_a_repository_of_belong() {
  reset_state
  run_check --preflight --repo belong/sandbox
  expect_status "$status" 1 "a repository of belong is a blocker"
  expect_contains "$err" "BLOCKED repo" "the blocker is printed"
  expect_absent "$(calls)" "fugaro runs ls" "the listing was not read"
}

test_preflight_passes_and_reads_nothing_beyond_the_preconditions() {
  reset_state
  run_check --preflight
  expect_status "$status" 0 "a good snapshot passes"
  expect_contains "$err" "preflight passed" "and says so"
  expect_absent "$(calls)" "fugaro runs ls" "the listing is not read by a preflight"
}

test_preflight_refuses_an_agent_session() {
  reset_state
  EXTRA_ENV=(CLAUDECODE=1)
  run_check --preflight
  unset EXTRA_ENV
  expect_status "$status" 2 "an agent session has no preflight"
  expect_contains "$err" "coding agent's session" "the refusal names what it is"
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
t test_refuses_a_bad_period_key
t test_the_whole_check_passes
t test_fails_when_the_window_holds_fewer_runs_than_the_report
t test_a_class_the_window_holds_alone_is_not_a_finding
t test_fails_when_the_window_names_no_run_of_a_reports_class
t test_fails_when_no_report_is_stored_for_the_period
t test_is_unverified_when_the_listing_does_not_answer
t test_the_legacy_row_counts_the_classed_runs
t test_the_fake_refuses_a_flag_the_cli_does_not_have
t test_the_fake_accepts_the_reads_the_check_makes
t test_preflight_blocks_without_a_stored_report_and_writes_nothing
t test_preflight_blocks_on_a_repository_of_belong
t test_preflight_passes_and_reads_nothing_beyond_the_preconditions
t test_preflight_refuses_an_agent_session

if [ "$failures" -ne 0 ]; then
  echo "check342_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check342_test.sh: ok"
