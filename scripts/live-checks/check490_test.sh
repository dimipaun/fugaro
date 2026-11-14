#!/usr/bin/env bash
# Tests for scripts/live-checks/check490.sh, with fake `gcloud`, `fugaro` and
# `gh` on PATH: NOTHING here reaches a cloud, a project, a run or a repository,
# no agent runs a live command, and the fakes record every call they are given —
# in which directory it was made — so a case can assert what the check did and
# did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the calls
# the check makes (version, the two `--help` probes, doctor, the launch, the runs
# list and the cancel), the fake `gcloud` serves the run record and the fake `gh`
# the pull request's body. The cases that matter are the ones where that model is
# bent: a build older than the release, a repository that turned the step off, a
# run that never ends (which the check has to cancel — this check may launch
# nothing it cannot stop), a record of another run, a mode the step does not
# govern, a run that ended a draft or after two review rounds, a review that
# reported nothing, a record with no triage block, a block that blocked the
# finding instead of deferring it, a deferral that disagrees with the verdict,
# and a pull request whose Deferred section is missing or names another finding.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check490_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check490.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK490_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK490_TEST_HEAD:-900}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# Every command this file runs is bounded, and so is every case: an earlier
# live-check test (check340_test.sh) hung for 25 minutes, and the suite that runs
# this file has one deadline for all of them. `timeout` is coreutils and is not
# on a stock Mac, so the fallback is a watchdog of the same shape.
TIMEOUT_BIN=""
for candidate in timeout gtimeout; do
  if command -v "$candidate" >/dev/null 2>&1; then
    TIMEOUT_BIN=$candidate
    break
  fi
done
CASE_TIMEOUT=${CHECK490_TEST_TIMEOUT:-60}

bounded() { # bounded CMD...
  local rc=0
  if [ -n "$TIMEOUT_BIN" ]; then
    "$TIMEOUT_BIN" "$CASE_TIMEOUT" "$@" || rc=$?
    return "$rc"
  fi
  "$@" &
  local pid=$!
  ( sleep "$CASE_TIMEOUT"; kill -TERM "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true
  return "$rc"
}

project=fugaro-chk490-test
run_id=run-20261114100000-1
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
pr_url="https://github.com/acme/sandbox/pull/77"
# The finding the fake record defers: the triage verdict names this key, the
# record's deferral names it, and the pull request's section names it, so the
# case that bends one of the three is bending exactly one reading of one finding.
finding_key=deferredkey0001
finding_summary="docs/notes.md says five where the table lists four"
finding_reason="a one-word documentation correction the run was not asked for"

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check490.sh's test only. It
# answers the account read the check makes and serves the run record. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk490-owner@belong.example\n' ;;
    *) printf 'chk490-owner@example.com\n' ;;
    esac
  fi
  ;;
storage)
  if [ "${2:-}" = "cat" ]; then
    case "$mode" in
    record-missing)
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
      ;;
    *)
      cat "$FAKE_JSON_DIR/record.json"
      ;;
    esac
  fi
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check490.sh's test only. The
# `--json` reads come from files the case wrote, the two `--help` probes print
# the usage lines the real commands print (the check's offline feature probe),
# and every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version)
  case "$mode" in
  old-version) printf '0.23.0\n' ;;
  dev-build) printf 'dev-9f8e7d6\n' ;;
  *) printf '0.24.0\n' ;;
  esac
  ;;
followups)
  case "$mode" in
  no-feature)
    printf 'fugaro: unknown command "followups"\n' >&2
    exit 1
    ;;
  *) printf 'Usage:\n  fugaro followups RUN [--launch] [--max N] [--json]\n' ;;
  esac
  ;;
runs)
  case "${2:-}" in
  ls)
    # `--help` is the check's own offline feature probe; every other shape is
    # the listing the check follows the run with.
    case " $* " in
    *" --help "*)
      case "$mode" in
      no-feature) printf 'Usage:\n  fugaro runs ls [--repo R] [--json] [--limit N]\n' ;;
      *) printf 'Usage:\n  fugaro runs ls [--repo R] [--json] [--limit N] [--debt]\n' ;;
      esac
      ;;
    *) cat "$FAKE_JSON_DIR/rows.json" ;;
    esac
    ;;
  *)
    echo "fugaro: unexpected runs subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
run)
  case "$mode" in
  run-fails)
    printf 'fugaro: the launch was refused\n' >&2
    exit 1
    ;;
  launch-hangs)
    sleep 5
    ;;
  esac
  cat "$FAKE_JSON_DIR/launch.json"
  ;;
cancel)
  printf 'cancel %s\n' "$*" >>"$FAKE_LOG_DIR/cancel.calls"
  case "$mode" in
  cancel-fails)
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
    ;;
  cancel-hangs)
    sleep 5
    ;;
  esac
  cat "$FAKE_JSON_DIR/cancel.json"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check490.sh's test only: it serves
# the pull request's body from the case's own directory and reaches no host.
set -euo pipefail
printf 'gh %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gh.calls"
mode=${FAKE_MODE:-default}
case "${1:-}${2:-}" in
prview)
  case "$mode" in
  pr-missing)
    echo "gh: could not resolve to a PullRequest" >&2
    exit 1
    ;;
  esac
  cat "$FAKE_JSON_DIR/prdoc.json"
  ;;
*)
  echo "gh: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GH_EOF
  chmod +x "$1/bin/gh"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk490","gcp_project":"$project","region":"us-east5"}}
JSON
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"2000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261114-100000-abcd","execution":"exec-1","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"finalize","terminal":true,"settled":true,"branch":"fugaro/20261114-100000-abcd","base_branch":"main","pr":77,"pr_url":"$pr_url","outcome":"ready"}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/cancel.json" <<JSON
{"project":"$project","run":"$slug/$run_id","status":"cancelled","marker":true,"hard":true}
JSON
  write_record "$dir" ok
  write_prdoc "$dir" ok
}

# write_record DIR STATE: one `result.json`. The state is the reading a case
# bends: the outcome, the number of review rounds, what the round reported, the
# triage block, the deferrals, and the mode the run resolved.
write_record() { # write_record DIR STATE
  local state=$2 status=succeeded outcome=ready mode=startup from=request
  local reviews triage deferred
  reviews='[{"round":1,"tier":"standard","verdict":"pass","findings":1,"head":"abc1234"}]'
  triage='{"rounds":[{"round":1,"mode":"startup","level":"l2","account":"acct","model":"a-model","entries":[{"index":0,"key":"'"$finding_key"'","verdict":"defer","reason":"'"$finding_reason"'","source":"agent"}],"total":1}]}'
  deferred='{"findings":[{"key":"'"$finding_key"'","summary":"'"$finding_summary"'","reason":"'"$finding_reason"'","source":"agent"}],"total":1}'
  case "$state" in
  ok) ;;
  draft) outcome=draft ;;
  stopped)
    status=failed
    outcome=none
    ;;
  two-rounds)
    reviews='[{"round":1,"tier":"standard","verdict":"revise","findings":1,"head":"abc1234"},{"round":2,"tier":"standard","verdict":"pass","findings":0,"head":"def5678"}]'
    ;;
  no-findings)
    reviews='[{"round":1,"tier":"standard","verdict":"pass","findings":0,"head":"abc1234"}]'
    ;;
  hotfix) mode=hotfix ;;
  no-mode) mode="" ;;
  no-triage) triage="" ;;
  triage-block)
    triage='{"rounds":[{"round":1,"mode":"startup","level":"l2","entries":[{"index":0,"key":"'"$finding_key"'","verdict":"block","reason":"the floor holds this finding: no verdict may defer it","source":"agent"}],"total":1}]}'
    deferred=""
    ;;
  no-deferred) deferred="" ;;
  deferred-mismatch)
    deferred='{"findings":[{"key":"anotherkey0002","summary":"'"$finding_summary"'","reason":"'"$finding_reason"'","source":"agent"}],"total":1}'
    ;;
  esac
  local triage_field="" deferred_field=""
  if [ -n "$triage" ]; then triage_field=",\"triage\":$triage"; fi
  if [ -n "$deferred" ]; then deferred_field=",\"deferred\":$deferred"; fi
  local mode_field=""
  if [ -n "$mode" ]; then mode_field=",\"rigor\":{\"mode\":\"$mode\",\"from\":\"$from\",\"at\":\"2026-11-14T10:00:00Z\"}"; fi
  cat >"$1/json/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"$status","stage":"finalize","outcome":"$outcome","reviews":$reviews$triage_field$deferred_field$mode_field,"cost":{"model_usd":0.31,"compute_usd":0,"compute_estimated":false,"total_usd":0.31,"estimate":false,"model_basis":"api-list"},"stages":[{"name":"implement","model":"a-model","model_usd":0.2},{"name":"review","model":"a-model","model_usd":0.06},{"name":"triage","model":"a-model","model_usd":0.05}]}
JSON
}

# write_prdoc DIR STATE: the pull request `gh pr view` answers with. The section
# and the key in it are the two readings the check grades.
write_prdoc() { # write_prdoc DIR STATE
  local state=$2 section="# Changelog

One word changed in \`docs/notes.md\`.

**Deferred (follow-ups)**

- \`$finding_key\`: $finding_summary — $finding_reason

Run \`fugaro followups $run_id\` after merging: it turns these findings into follow-up tasks, and refuses until this pull request is merged."
  case "$state" in
  no-section)
    section="# Changelog

One word changed in \`docs/notes.md\`."
    ;;
  no-key)
    section="# Changelog

**Deferred (follow-ups)**

- \`someotherkey0009\`: a different finding — its own reason"
    ;;
  esac
  python3 - "$1/json/prdoc.json" "$pr_url" "$section" <<'PY'
import json
import sys

path, url, body = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, "w", encoding="utf-8") as fh:
    json.dump({"number": 77, "state": "OPEN", "isDraft": False, "body": body, "url": url}, fh)
PY
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to. The switch the premise reads is
# written here so a case can bend it.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk490\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_fake_gh "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  run_one "$dir" "$script" "$@"
}

run_one() { # run_one DIR SCRIPT ENV... [-- ARGS...]
  local dir=$1 target=$2
  shift 2
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gh.calls"
  : >"$dir/logs/cancel.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" \
    CHECK490_SANDBOX="$dir/sandbox" CHECK490_YES=1 \
    CHECK490_RUN_TIMEOUT_SECONDS=10 CHECK490_POLL_SECONDS=1 \
    CHECK490_CLI_TIMEOUT_SECONDS=20 CHECK490_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK490_CANCEL_TIMEOUT_SECONDS=20 CHECK490_DOCTOR_TIMEOUT_SECONDS=20 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_no_run() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "run --repo" "$2: no run is launched"
}

assert_no_cancel() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "cancel " "$2: nothing is cancelled"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check490.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the run is billable and that the check can stop it.
  expect_contains "$out" "ONE sandbox run and what its task costs" "--help: the cost of a run is stated"
  expect_contains "$out" "Nothing it launches can outlive it" "--help: what it does to a run that will not end"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK490_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 490 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the cost is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 490 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT premise: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT pr-deferred: INFO — planned" "dry run: the pull request row is planned too"
  expect_contains "$err" "BILLABLE" "dry run: the plan says the run costs money"
  expect_contains "$err" "fugaro cancel <run> --now" "dry run: the plan says what it does to a run that will not end"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
  expect_absent "$(cat "$dir/logs/gh.calls")" "pr view" "dry run: no pull request is read"
  assert_no_run "$dir" "dry run"
  assert_no_cancel "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT premise: PASS" "happy path: the premise"
  expect_contains "$out" "does not turn the step off" "happy path: what the premise means"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT cancelled: INFO" "happy path: nothing had to be cancelled"
  expect_contains "$out" "not needed: the run ended by itself" "happy path: why nothing was cancelled"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the record"
  expect_contains "$out" "FACT mode: PASS" "happy path: the mode the step governs"
  expect_contains "$out" "FACT outcome: PASS" "happy path: ready after one round"
  expect_contains "$out" "FACT triage-block: PASS" "happy path: the triage block"
  expect_contains "$out" "FACT deferred: PASS" "happy path: the deferral that took effect"
  expect_contains "$out" "FACT pr-deferred: PASS" "happy path: the pull request's section"
  expect_contains "$out" "FACT cost: PASS" "happy path: the run's own figure"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore"
  expect_contains "$out" "CHECK 490 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, the record was read at the
  # run's own key — the one-segment slug, not the owner/name the launch names —
  # and nothing was cancelled, because the run ended on its own.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --mode startup --json" "happy path: the run is launched in startup mode from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the run is followed by its own slug"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${run_id}/result.json" "happy path: the record is read at the run's own key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  expect_contains "$(cat "$dir/logs/gh.calls")" "pr view $pr_url --json body,state,isDraft,number" "happy path: the pull request is read by its URL"
  # The pull request is left open on purpose: Check 491 files from it.
  expect_contains "$out" "LEFT OPEN on purpose: Check 491" "happy path: the restore says the pull request stays"
  assert_no_cancel "$dir" "happy path"
  assert_scratch_removed "$dir" "happy path"
}

test_the_launch_is_billable_and_needs_yes() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK490_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "asks for --yes" "no --yes: the check says what it wants"
  assert_no_run "$dir" "no --yes"
  assert_no_cancel "$dir" "no --yes"
  assert_scratch_removed "$dir" "no --yes"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK490_SANDBOX=
  expect_status "$status" 2 "no sandbox"
  expect_contains "$err" "Check 490 needs --sandbox" "no sandbox: the refusal"
  expect_absent "$out" "FACT " "no sandbox: no block is printed for a run that never started"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/nothing"
  run_check "$dir" CHECK490_SANDBOX="$dir/nothing"
  expect_status "$status" 2 "not a repository"
  expect_contains "$err" "carries no fugaro.yaml" "not a repository: the refusal"
  assert_no_run "$dir" "not a repository"
  assert_scratch_removed "$dir" "not a repository"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_contains "$err" "this shell is tracing" "xtrace: the refusal"
  assert_no_run "$dir" "xtrace"
  assert_scratch_removed "$dir" "xtrace"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "agent session"
  expect_contains "$err" "coding agent's session" "agent session: the refusal"
  expect_contains "$out" "CHECK 490 RESULT END (exit 1)" "agent session: the block"
  assert_no_run "$dir" "agent session"
  assert_scratch_removed "$dir" "agent session"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belongacct
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "belong account"
  expect_contains "$err" "names belong" "belong account: the refusal"
  assert_no_run "$dir" "belong account"
  assert_scratch_removed "$dir" "belong account"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belongproject
  prepare "$dir"
  python3 - "$dir/json/doctor.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as fh:
    doc = json.load(fh)
doc["project"]["gcp_project"] = "belong-prod"
with open(path, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
  run_check "$dir"
  expect_status "$status" 1 "belong project"
  expect_contains "$err" "belongs to belong-prod" "belong project: the refusal"
  assert_no_run "$dir" "belong project"
  assert_scratch_removed "$dir" "belong project"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "old build"
  expect_contains "$out" "FACT release: FAIL" "old build: the row"
  expect_contains "$err" "older than 0.24.0" "old build: the refusal"
  assert_no_run "$dir" "old build"
  assert_scratch_removed "$dir" "old build"
}

test_a_build_without_the_feature_is_refused() {
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "no feature"
  expect_contains "$out" "FACT release: FAIL" "no feature: the row"
  expect_contains "$out" "no follow-ups" "no feature: why the probe failed"
  assert_no_run "$dir" "no feature"
  assert_scratch_removed "$dir" "no feature"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "dev build"
  expect_contains "$out" "FACT release: PASS" "dev build: the probe decides"
  expect_contains "$out" "not a release number" "dev build: why the version did not decide"
  assert_scratch_removed "$dir" "dev build"
}

test_a_repository_that_turned_the_step_off_stops_the_check() {
  local dir=$work/triageoff
  prepare "$dir"
  printf 'project: chk490\nagent:\n  triage: false\n' >"$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 1 "triage off"
  expect_contains "$out" "FACT premise: FAIL" "triage off: the row"
  expect_contains "$out" "turns the step off" "triage off: the refusal"
  assert_no_run "$dir" "triage off"
  assert_scratch_removed "$dir" "triage off"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "launch fails"
  expect_contains "$out" "FACT run-launched: FAIL" "launch fails: the row"
  assert_no_cancel "$dir" "launch fails"
  assert_scratch_removed "$dir" "launch fails"
}

test_a_launch_that_names_no_run_is_a_finding() {
  local dir=$work/norun
  prepare "$dir"
  printf '{"project":"%s","status":"launched"}\n' "$project" >"$dir/json/launch.json"
  run_check "$dir"
  expect_status "$status" 1 "no run named"
  expect_contains "$out" "FACT run-launched: FAIL" "no run named: the row"
  expect_contains "$out" "named no run" "no run named: why"
  assert_no_cancel "$dir" "no run named"
  assert_scratch_removed "$dir" "no run named"
}

test_a_run_that_never_ends_is_cancelled() {
  local dir=$work/neverends
  prepare "$dir"
  printf '{"project":"%s","runs":[]}\n' "$project" >"$dir/json/rows.json"
  run_check "$dir"
  expect_status "$status" 2 "never ends"
  expect_contains "$out" "FACT terminal: FAIL" "never ends: the row"
  expect_contains "$out" "FACT cancelled: PASS" "never ends: it was cancelled"
  expect_contains "$out" "did not end on its own" "never ends: why"
  expect_contains "$(cat "$dir/logs/cancel.calls")" "cancel ${run_id} --now" "never ends: the cancel command"
  expect_contains "$out" "FACT restore: PASS" "never ends: the restore"
  assert_scratch_removed "$dir" "never ends"
}

test_a_cancel_that_does_not_answer_is_a_finding() {
  local dir=$work/cancelfails
  prepare "$dir"
  printf '{"project":"%s","runs":[]}\n' "$project" >"$dir/json/rows.json"
  run_check "$dir" FAKE_MODE=cancel-fails
  expect_status "$status" 1 "cancel fails"
  expect_contains "$out" "FACT cancelled: FAIL" "cancel fails: the row"
  expect_contains "$out" "FACT restore: FAIL" "cancel fails: the restore names it"
  expect_contains "$out" "cancel it yourself" "cancel fails: what the owner does"
  assert_scratch_removed "$dir" "cancel fails"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "no record"
  expect_contains "$out" "FACT result-json: FAIL" "no record: the row"
  expect_contains "$out" "could not be read" "no record: why"
  assert_scratch_removed "$dir" "no record"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/otherrecord
  prepare "$dir"
  python3 - "$dir/json/record.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as fh:
    doc = json.load(fh)
doc["run_id"] = "run-20261114000000-9"
with open(path, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
  run_check "$dir"
  expect_status "$status" 1 "another run's record"
  expect_contains "$out" "FACT result-json: FAIL" "another run's record: the row"
  expect_contains "$out" "is another run's" "another run's record: why"
  expect_absent "$out" "FACT outcome: PASS" "another run's record: nothing below is read"
  assert_scratch_removed "$dir" "another run's record"
}

test_a_mode_the_step_does_not_govern_is_a_finding() {
  local dir=$work/hotfix
  prepare "$dir"
  write_record "$dir" hotfix
  run_check "$dir"
  expect_status "$status" 1 "hotfix mode"
  expect_contains "$out" "FACT mode: FAIL" "hotfix mode: the row"
  expect_contains "$out" "rather than startup or pro" "hotfix mode: why"
  expect_absent "$out" "FACT outcome: PASS" "hotfix mode: the run's reading is not graded"
  assert_scratch_removed "$dir" "hotfix mode"
}

test_a_run_that_ended_a_draft_is_a_finding() {
  local dir=$work/draft
  prepare "$dir"
  write_record "$dir" draft
  run_check "$dir"
  expect_status "$status" 1 "draft"
  expect_contains "$out" "FACT outcome: FAIL" "draft: the row"
  expect_contains "$out" "finishes ready" "draft: what the check claims"
  assert_scratch_removed "$dir" "draft"
}

test_a_second_review_round_is_a_finding() {
  local dir=$work/tworounds
  prepare "$dir"
  write_record "$dir" two-rounds
  run_check "$dir"
  expect_status "$status" 1 "two rounds"
  expect_contains "$out" "FACT outcome: FAIL" "two rounds: the row"
  expect_contains "$out" "after 2 review rounds rather than one" "two rounds: why"
  expect_contains "$out" "the fix round the triage step exists to avoid" "two rounds: what it means"
  assert_scratch_removed "$dir" "two rounds"
}

test_a_review_that_reported_nothing_is_a_finding() {
  local dir=$work/nofindings
  prepare "$dir"
  write_record "$dir" no-findings
  run_check "$dir"
  expect_status "$status" 1 "no findings"
  expect_contains "$out" "FACT outcome: FAIL" "no findings: the row"
  expect_contains "$out" "reported no finding at all" "no findings: why"
  assert_scratch_removed "$dir" "no findings"
}

test_a_record_with_no_triage_block_is_a_finding() {
  local dir=$work/notriage
  prepare "$dir"
  write_record "$dir" no-triage
  run_check "$dir"
  expect_status "$status" 1 "no triage block"
  expect_contains "$out" "FACT triage-block: FAIL" "no triage block: the row"
  expect_contains "$out" "no triage block at all" "no triage block: why"
  assert_scratch_removed "$dir" "no triage block"
}

test_a_finding_the_step_blocked_is_a_finding() {
  local dir=$work/blocked
  prepare "$dir"
  # A blocked finding is one the run must fix, so nothing was deferred and the
  # pull request carries no section: the two documents are written together
  # because that is the state the record and the pull request are in when this
  # happens.
  write_record "$dir" triage-block
  write_prdoc "$dir" no-section
  run_check "$dir"
  expect_status "$status" 1 "blocked finding"
  expect_contains "$out" "FACT triage-block: FAIL" "blocked finding: the row"
  expect_contains "$out" "rather than defer" "blocked finding: why"
  expect_absent "$out" "FACT pr-deferred: PASS" "blocked finding: the pull request cannot carry it"
  assert_scratch_removed "$dir" "blocked finding"
}

test_a_record_with_no_deferrals_is_a_finding() {
  local dir=$work/nodeferred
  prepare "$dir"
  write_record "$dir" no-deferred
  run_check "$dir"
  expect_status "$status" 1 "no deferrals"
  expect_contains "$out" "FACT deferred: FAIL" "no deferrals: the row"
  expect_contains "$out" "the verdict did not take effect" "no deferrals: why"
  assert_scratch_removed "$dir" "no deferrals"
}

test_a_deferral_that_disagrees_with_the_verdict_is_a_finding() {
  local dir=$work/mismatch
  prepare "$dir"
  write_record "$dir" deferred-mismatch
  run_check "$dir"
  expect_status "$status" 1 "keys disagree"
  expect_contains "$out" "FACT deferred: FAIL" "keys disagree: the row"
  expect_contains "$out" "they are not the same finding" "keys disagree: why"
  assert_scratch_removed "$dir" "keys disagree"
}

test_a_pull_request_without_the_section_is_a_finding() {
  local dir=$work/nosection
  prepare "$dir"
  write_prdoc "$dir" no-section
  run_check "$dir"
  expect_status "$status" 1 "no section"
  expect_contains "$out" "FACT pr-deferred: FAIL" "no section: the row"
  expect_contains "$out" "carries no 'Deferred (follow-ups)' section" "no section: why"
  assert_scratch_removed "$dir" "no section"
}

test_a_section_naming_another_finding_is_a_finding() {
  local dir=$work/otherkey
  prepare "$dir"
  write_prdoc "$dir" no-key
  run_check "$dir"
  expect_status "$status" 1 "another key"
  expect_contains "$out" "FACT pr-deferred: FAIL" "another key: the row"
  expect_contains "$out" "does not name the key ${finding_key}" "another key: why"
  assert_scratch_removed "$dir" "another key"
}

test_a_pull_request_that_cannot_be_read_is_unmeasured() {
  local dir=$work/nopr
  prepare "$dir"
  run_check "$dir" FAKE_MODE=pr-missing
  expect_status "$status" 2 "gh failed"
  expect_contains "$out" "FACT pr-deferred: FAIL" "gh failed: the row"
  expect_contains "$err" "could NOT measure" "gh failed: the check says so"
  assert_scratch_removed "$dir" "gh failed"
}

test_a_run_over_the_bound_is_a_finding() {
  local dir=$work/overbudget
  prepare "$dir"
  run_check "$dir" CHECK490_MAX_MODEL_USD=0.10
  expect_status "$status" 1 "over the bound"
  expect_contains "$out" "FACT cost: FAIL" "over the bound: the row"
  expect_contains "$out" "over CHECK490_MAX_MODEL_USD=0.10" "over the bound: why"
  assert_scratch_removed "$dir" "over the bound"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK490_KEEP_SCRATCH=1
  expect_status "$status" 1 "kept scratch"
  expect_contains "$out" "FACT scratch-removed: FAIL" "kept scratch: the row"
  [ -d "$scratch" ] || fail "kept scratch: the directory is not there"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "unknown argument"
  expect_contains "$err" "unknown argument: --nonsense" "unknown argument: the refusal"
  assert_no_run "$dir" "unknown argument"
}

# The two rows below are the check's own claim, read back from the block: the
# verdict row has to be the one the record's Triage block holds, and the
# deferral row has to name the same key. A case that made either vacuous would
# leave a check that passes a run whose finding was never deferred.
test_the_verdict_and_the_deferral_rows_are_load_bearing() {
  local dir=$work/loadbearing
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "load-bearing: the happy path holds"
  expect_contains "$out" "key ${finding_key}" "load-bearing: the verdict names the record's key"
  expect_contains "$out" "${finding_summary}" "load-bearing: the deferral names the reviewer's summary"
}

t() { "$1"; }
t test_help
t test_dry_run
t test_happy_path
t test_the_launch_is_billable_and_needs_yes
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_feature_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_repository_that_turned_the_step_off_stops_the_check
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_ends_is_cancelled
t test_a_cancel_that_does_not_answer_is_a_finding
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_mode_the_step_does_not_govern_is_a_finding
t test_a_run_that_ended_a_draft_is_a_finding
t test_a_second_review_round_is_a_finding
t test_a_review_that_reported_nothing_is_a_finding
t test_a_record_with_no_triage_block_is_a_finding
t test_a_finding_the_step_blocked_is_a_finding
t test_a_record_with_no_deferrals_is_a_finding
t test_a_deferral_that_disagrees_with_the_verdict_is_a_finding
t test_a_pull_request_without_the_section_is_a_finding
t test_a_section_naming_another_finding_is_a_finding
t test_a_pull_request_that_cannot_be_read_is_unmeasured
t test_a_run_over_the_bound_is_a_finding
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_verdict_and_the_deferral_rows_are_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check490_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check490_test.sh: ok"
