#!/usr/bin/env bash
# Tests for scripts/live-checks/check520.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a price table or a run, no agent
# runs a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the six
# calls the check makes (version, `models use --help`, doctor, `models ls`, the
# launch and the runs list) plus `diagnose`, and the fake `gcloud` serves the run
# record. The cases that matter are the ones where that model is bent: a build
# older than the release, a build whose `models use` names no vision slot, a
# doctor whose vision-coder sentence says the coder reads images itself, a slot
# that is unset, a models row that names no vision model, a launch that names no
# run, a run that never ends, a record that is another run's, a stage left on the
# coder's own model, a cost split without the vision model, and a diagnosis that
# drops it.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check520_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check520.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK520_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK520_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK520_TEST_TIMEOUT:-60}

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

project=fugaro-chk520-test
run_id=run-20261113120000-1
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
# The two models the fixtures name: the coder, priced without an image rate, and
# the model the vision slot covers it with. A record's stage that names the
# second is the rewrite; one that names the first is not.
coder=claude-opus-4-1
vision=claude-sonnet-5

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check520.sh's test only. It
# answers the account read the check makes and serves the run record. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk520-owner@belong.example\n' ;;
    *) printf 'chk520-owner@example.com\n' ;;
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
# A stand-in for `fugaro`, for scripts/live-checks/check520.sh's test only. The
# `--json` reads come from files the case wrote, `models use --help` prints the
# usage line the real command prints (the check's offline feature probe), and
# every call is logged with the directory it was made in.
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
models)
  case "${2:-}" in
  use)
    case "$mode" in
    no-feature) printf 'Usage:\n  fugaro models use <repo> <coder|reviewer|background> <id>\n' ;;
    *) printf 'Usage:\n  fugaro models use <repo> <coder|reviewer|background|vision> <id>\n' ;;
    esac
    ;;
  ls)
    cat "$FAKE_JSON_DIR/models.json"
    ;;
  *)
    echo "fugaro: unexpected models subcommand ${2:-}" >&2
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
    sleep 300
    ;;
  esac
  cat "$FAKE_JSON_DIR/launch.json"
  ;;
runs)
  cat "$FAKE_JSON_DIR/rows.json"
  ;;
diagnose)
  case "$mode" in
  diagnose-fails)
    printf 'fugaro: the run could not be read\n' >&2
    exit 1
    ;;
  esac
  cat "$FAKE_JSON_DIR/diagnose.json"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  write_doctor "$dir" "slot-covers"
  write_models "$dir" "$coder" "$vision"
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"2000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261113-120000-abcd","execution":"exec-1","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"finalize","terminal":true,"settled":true,"branch":"fugaro/20261113-120000-abcd","base_branch":"main"}],"totals":{"runs":1},"warnings":[]}
JSON
  write_record "$dir" "$run_id" succeeded "$vision"
  write_diagnose "$dir" "$vision"
}

# write_doctor DIR STATE: the `vision-coder` sentence, in the four states the
# check has to tell apart. The sentences are the program's own (doctor_vision.go).
write_doctor() { # write_doctor DIR STATE
  local state=$2 problem fix
  case "$state" in
  slot-covers)
    problem="the coder model $coder is priced without an image rate, and agent.models.vision is $vision, which a stage that reads a picture runs instead"
    fix="nothing to do: the vision slot is set, and a model it names is priced through the same table as every other pin"
    ;;
  coder-reads)
    problem="the coder model $coder is priced with an image rate, so a stage of this repository reads a picture itself; agent.models.vision is unset"
    fix="nothing to do: a coder that can read an image keeps its own model, and the vision slot is for a stage whose own model cannot read one"
    ;;
  slot-unset)
    problem="the coder model $coder is priced without an image rate, agent.models.vision is unset, and refs/heads/main tracks docs/logo.png: a stage that reads one is refused by the gateway mid-run, after its work is paid for"
    fix="set agent.models.vision to a model that can read images (fugaro models use sandbox vision <id>), and price that model's images with model_prices' image_tokens (0 refuses an image)"
    ;;
  unknown)
    problem="the coder model $coder is priced from a row this build does not know how to describe"
    fix="read the price table"
    ;;
  esac
  if [ "$state" = "no-check" ]; then
    cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk520","gcp_project":"$project","region":"us-east5"}}
JSON
    return 0
  fi
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[{"id":"vision-coder","ok":true,"severity":"info","problem":"$problem","fix":"$fix"}],"project":{"name":"chk520","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_models DIR CODER VISION: the project's own view of the sandbox
# repository, plus one row for a repository the check is not asked about (so a
# case that names the wrong key reads a list that is not empty).
write_models() { # write_models DIR CODER VISION
  cat >"$1/json/models.json" <<JSON
{"version":1,"project":"chk520","routes":[],"models":[],"repositories":[
 {"name":"sandbox","state":"ok","coder":"$2","reviewer":"claude-opus-4-1","vision":"$3"},
 {"name":"other","state":"ok","coder":"claude-opus-4-1","vision":"claude-sonnet-5"}]}
JSON
}

# write_record DIR RUN_ID STATUS STAGE_MODEL: one record, with a coder-role stage
# pinned to STAGE_MODEL and a review stage that is deliberately on another model
# (D2004: a reviewer's stage is never rewritten, so the check may not grade it).
write_record() { # write_record DIR RUN_ID STATUS STAGE_MODEL
  local stages
  if [ "$4" = "none" ]; then
    stages="[]"
  else
    stages="[{\"name\":\"implement\",\"model\":\"$4\",\"model_usd\":0.42},{\"name\":\"review\",\"model\":\"claude-opus-4-1\",\"model_usd\":0.10}]"
  fi
  cat >"$1/json/record.json" <<JSON
{"version":1,"run_id":"$2","repo":"acme/sandbox","workflow":"agent","status":"$3","stage":"finalize","outcome":"pr","stages":$stages,"cost":{"model_usd":0.52,"compute_usd":0.01,"compute_estimated":true,"total_usd":0.53,"estimate":false,"model_basis":"api-list","model_by":{"$vision":0.42,"claude-opus-4-1":0.10}}}
JSON
}

# write_diagnose DIR VISION_MODEL: the same split through the command the owner
# reads a run with. VISION_MODEL "none" drops the vision model from it.
write_diagnose() { # write_diagnose DIR VISION_MODEL
  local by
  if [ "$2" = "none" ]; then
    by="{\"claude-opus-4-1\":0.52}"
  else
    by="{\"$2\":0.42,\"claude-opus-4-1\":0.10}"
  fi
  cat >"$1/json/diagnose.json" <<JSON
{"run_id":"$run_id","repo":"acme/sandbox","status":"succeeded","model_by":$by,"stages":[{"name":"implement","model":"$vision","model_usd":0.42}]}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to. The directory's own name is
# what the check reads the project configuration's key for the repository from,
# which is why it is `sandbox`.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk520\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  run_one "$dir" "$script" "$@"
}

run_copy() { # run_copy DIR COPY ENV... [-- ARGS...]: the same run, another script
  local dir=$1 copy=$2
  shift 2
  run_one "$dir" "$copy" "$@"
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
    CHECK520_SANDBOX="$dir/sandbox" CHECK520_YES=1 \
    CHECK520_RUN_TIMEOUT_SECONDS=10 CHECK520_POLL_SECONDS=1 \
    CHECK520_CLI_TIMEOUT_SECONDS=20 CHECK520_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK520_DOCTOR_TIMEOUT_SECONDS=20 \
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

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check520.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that a run is billable and that the check is theirs.
  expect_contains "$out" "ONE sandbox run and what its task costs" "--help: the cost of a run is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK520_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 520 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 520 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT premise: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT vision-cost: INFO — planned" "dry run: the cost row is planned too"
  expect_contains "$err" "ONE sandbox run" "dry run: the plan names the spend"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "models ls" "dry run: the models are not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
  assert_no_run "$dir" "dry run"
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
  expect_contains "$out" "the vision slot covers it, which is the state V7 rewrites" "happy path: what the premise means"
  expect_contains "$out" "FACT models: PASS" "happy path: the two models"
  expect_contains "$out" "runs coder ${coder} and agent.models.vision ${vision}" "happy path: which two"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the record"
  expect_contains "$out" "FACT run-succeeded: PASS" "happy path: the run passed"
  expect_contains "$out" "FACT vision-stages: PASS" "happy path: the coder-role stage"
  expect_contains "$out" "implement=${vision}" "happy path: which model it named"
  expect_contains "$out" "FACT vision-cost: PASS" "happy path: the split"
  expect_contains "$out" "FACT diagnose-split: PASS" "happy path: the same split through diagnose"
  expect_contains "$out" "CHECK 520 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, and the record was read at
  # the run's own key — the one-segment slug, not the owner/name the launch names.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "models ls --json (in $dir/sandbox)" "happy path: the models are read through the checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the run is launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the run is followed by its own slug"
  expect_contains "$calls" "diagnose $run_id --json (in $dir/sandbox)" "happy path: the diagnosis is read through the checkout"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${run_id}/result.json" "happy path: the record is read at the run's own key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  assert_scratch_removed "$dir" "happy path"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK520_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check520.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK520_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  assert_no_run "$dir" "a directory that is not a checkout"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  assert_no_run "$dir" "bash -x"
}

test_a_coding_agents_session_is_refused() {
  # The check drives commands whose writes a coding agent's session is refused:
  # refusing here is what keeps the block from reporting those refusals as
  # findings about the release.
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the refusal"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: whose check it is"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a coding agent's session: nothing is read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a coding agent's session: the bucket is not read"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belongacct
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$err" "never run there" "an account naming belong: the refusal"
  assert_no_run "$dir" "an account naming belong"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belongproj
  prepare "$dir"
  printf '{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk520-belong","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "a project naming belong: the refusal"
  assert_no_run "$dir" "a project naming belong"
}

test_yes_is_required() {
  # The run is billable, so the check asks for --yes before it launches one.
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK520_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "asks for --yes" "no --yes: the refusal"
  expect_contains "$err" "Nothing has been launched" "no --yes: what it says about the money"
  assert_no_run "$dir" "no --yes"
}

test_a_build_older_than_the_release_is_refused() {
  # V8's own gate: the work is unreleased, so a build that predates it would
  # measure a state nobody shipped.
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "a build older than the release"
  expect_contains "$out" "FACT release: FAIL" "a build older than the release: the row"
  expect_contains "$out" "predates release 0.24.0" "a build older than the release: what it found"
  expect_contains "$out" "fugaro update" "a build older than the release: the one command that fixes it"
  expect_contains "$out" "that one command is the whole upgrade" "a build older than the release: why one command is enough"
  assert_no_run "$dir" "a build older than the release"
}

test_a_build_without_the_slot_is_refused() {
  # The feature probe: a build whose `models use` names no vision slot has
  # neither the slot nor the rewrite, whatever its version line says.
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "a build with no vision slot"
  expect_contains "$out" "FACT release: FAIL" "a build with no vision slot: the row"
  expect_contains "$out" "no agent.models.vision" "a build with no vision slot: what it found"
  expect_contains "$out" "needs release 0.24.0" "a build with no vision slot: the release it names"
  assert_no_run "$dir" "a build with no vision slot"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  # A version line that is not a release number is not a reading of anything:
  # the probe decides, and it said yes.
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "a dev build"
  expect_contains "$out" "FACT release: PASS" "a dev build: the row"
  expect_contains "$out" "not a release number" "a dev build: why the probe decides"
}

test_a_doctor_without_the_vision_check_is_a_finding() {
  local dir=$work/nocheck
  prepare "$dir"
  write_doctor "$dir" no-check
  run_check "$dir"
  expect_status "$status" 1 "a doctor with no vision-coder check"
  expect_contains "$out" "FACT premise: FAIL" "a doctor with no vision-coder check: the row"
  expect_contains "$out" "carries no vision-coder check" "a doctor with no vision-coder check: what it found"
  assert_no_run "$dir" "a doctor with no vision-coder check"
}

test_a_coder_that_reads_images_itself_is_a_finding() {
  # The plan's own sentence reads "a provider model with an image rate", and
  # D2016 makes that the state in which the vision slot is never reached: a run
  # here would measure the coder's own model and nothing about the rewrite.
  local dir=$work/reads
  prepare "$dir"
  write_doctor "$dir" coder-reads
  run_check "$dir"
  expect_status "$status" 1 "a coder that reads images itself"
  expect_contains "$out" "FACT premise: FAIL" "a coder that reads images itself: the row"
  expect_contains "$out" "reads a picture ITSELF" "a coder that reads images itself: what it means"
  expect_contains "$out" "the plan's own sentence" "a coder that reads images itself: the slip is named"
  assert_no_run "$dir" "a coder that reads images itself"
}

test_an_unset_vision_slot_is_a_finding() {
  local dir=$work/unset
  prepare "$dir"
  write_doctor "$dir" slot-unset
  run_check "$dir"
  expect_status "$status" 1 "an unset vision slot"
  expect_contains "$out" "FACT premise: FAIL" "an unset vision slot: the row"
  expect_contains "$out" "the vision slot is UNSET" "an unset vision slot: what it found"
  expect_contains "$out" "fugaro models use <repo> vision <id>" "an unset vision slot: the remedy"
  assert_no_run "$dir" "an unset vision slot"
}

test_an_unrecognised_doctor_sentence_is_a_finding() {
  local dir=$work/unknown
  prepare "$dir"
  write_doctor "$dir" unknown
  run_check "$dir"
  expect_status "$status" 1 "a sentence the check cannot place"
  expect_contains "$out" "FACT premise: FAIL" "a sentence the check cannot place: the row"
  expect_contains "$out" "is not one this check knows" "a sentence the check cannot place: what it found"
  assert_no_run "$dir" "a sentence the check cannot place"
}

test_a_repository_the_models_list_does_not_name_is_not_measured() {
  local dir=$work/norow
  prepare "$dir"
  run_check "$dir" CHECK520_REPO=nosuchrepo
  expect_status "$status" 2 "a repository the list does not name"
  expect_contains "$out" "FACT models: FAIL" "a repository the list does not name: the row"
  expect_contains "$out" "holds no repository named 'nosuchrepo'" "a repository the list does not name: what it found"
  expect_contains "$out" "it holds: sandbox,other" "a repository the list does not name: the keys it does hold"
  expect_contains "$err" "could NOT measure models" "a repository the list does not name: the exit names the row"
  assert_no_run "$dir" "a repository the list does not name"
}

test_a_row_with_no_vision_model_is_a_finding() {
  local dir=$work/novision
  prepare "$dir"
  write_models "$dir" "$coder" ""
  run_check "$dir"
  expect_status "$status" 1 "a row with no vision model"
  expect_contains "$out" "FACT models: FAIL" "a row with no vision model: the row"
  expect_contains "$out" "names no vision model" "a row with no vision model: what it found"
  assert_no_run "$dir" "a row with no vision model"
}

test_a_row_that_names_one_model_twice_is_a_finding() {
  # A stage timing naming the coder could not then tell the rewrite from the
  # coder's own model, so nothing below would be a reading of anything.
  local dir=$work/same
  prepare "$dir"
  write_models "$dir" "$coder" "$coder"
  run_check "$dir"
  expect_status "$status" 1 "one model in both slots"
  expect_contains "$out" "FACT models: FAIL" "one model in both slots: the row"
  expect_contains "$out" "the same model" "one model in both slots: what it found"
  assert_no_run "$dir" "one model in both slots"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launchfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "a launch that failed"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that failed: the row"
  expect_contains "$out" "fugaro run exited 1" "a launch that failed: what it saw"
}

test_a_launch_that_names_no_run_is_a_finding() {
  # The check follows the run it launched and would read its record at the key
  # the row names: a launch that names no run can be neither followed nor read.
  local dir=$work/norun
  prepare "$dir"
  printf '{"project":"%s","run":"2000","repo":"acme/sandbox","status":"launched"}\n' "$project" >"$dir/json/launch.json"
  run_check "$dir"
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: what it found"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a launch that names no run: no record is read"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/notterminal
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"implement","terminal":false}],"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows.json"
  run_check "$dir" CHECK520_RUN_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a run that never ended"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ended: the row"
  expect_contains "$out" "had not ended after" "a run that never ended: what it saw"
  expect_contains "$err" "could NOT measure terminal" "a run that never ended: the exit names the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a run that never ended: no record is read"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT result-json: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read (exit 1)" "a record that cannot be read: what it saw"
}

test_a_record_of_another_run_is_a_finding() {
  # A record read by listing alone could be another run's: the record's own
  # run_id is what ties it to the run this check launched.
  local dir=$work/otherrecord
  prepare "$dir"
  write_record "$dir" run-20261113000000-9 succeeded "$vision"
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20261113000000-9 and this check launched ${run_id}" "a record of another run: the two IDs"
}

test_a_run_that_did_not_succeed_is_a_finding() {
  local dir=$work/failed
  prepare "$dir"
  write_record "$dir" "$run_id" failed "$vision"
  run_check "$dir"
  expect_status "$status" 1 "a run that failed"
  expect_contains "$out" "FACT run-succeeded: FAIL" "a run that failed: the row"
  expect_contains "$out" "the failure this work removes" "a run that failed: what it means"
}

test_a_run_over_the_bound_is_a_finding() {
  # CHECK520_MAX_MODEL_USD turns the figure into a criterion; unset, the row is
  # a reading. The money is spent either way, which the row says.
  local dir=$work/overmax
  prepare "$dir"
  run_check "$dir" CHECK520_MAX_MODEL_USD=0.10
  expect_status "$status" 1 "a run over the bound"
  expect_contains "$out" "FACT run-succeeded: FAIL" "a run over the bound: the row"
  expect_contains "$out" "over CHECK520_MAX_MODEL_USD=0.10" "a run over the bound: the bound it read"
  expect_contains "$out" "the money is spent either way" "a run over the bound: what it says about the money"
}

test_a_stage_left_on_another_model_is_a_finding() {
  # The reading the plan names first: a coder-role stage that does not name the
  # vision model is the rewrite not having happened.
  local dir=$work/stage
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded "$coder"
  run_check "$dir"
  expect_status "$status" 1 "a coder-role stage on another model"
  expect_contains "$out" "FACT vision-stages: FAIL" "a coder-role stage on another model: the row"
  expect_contains "$out" "implement(=${coder})" "a coder-role stage on another model: which stage, and on what"
}

test_a_record_with_no_stage_is_a_finding() {
  local dir=$work/nostage
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded none
  run_check "$dir"
  expect_status "$status" 1 "a record with no stage"
  expect_contains "$out" "FACT vision-stages: FAIL" "a record with no stage: the row"
  expect_contains "$out" "holds no stage at all" "a record with no stage: what it found"
}

test_a_cost_split_without_the_vision_model_is_a_finding() {
  local dir=$work/nocost
  prepare "$dir"
  python3 - "$dir/json/record.json" "$vision" <<'PY'
import json, sys
path, vision = sys.argv[1], sys.argv[2]
doc = json.load(open(path))
doc["cost"]["model_by"] = {"claude-opus-4-1": 0.52}
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a split without the vision model"
  expect_contains "$out" "FACT vision-cost: FAIL" "a split without the vision model: the row"
  expect_contains "$out" "does not carry the vision model ${vision}" "a split without the vision model: what it found"
  expect_contains "$out" "it carries: claude-opus-4-1=0.52" "a split without the vision model: what it does carry"
}

test_a_diagnosis_that_drops_the_vision_model_is_a_finding() {
  # The record's own split is one thing and the command the owner reads a run
  # with is another: a split the diagnosis drops is one nobody sees.
  local dir=$work/nodiagnose
  prepare "$dir"
  write_diagnose "$dir" none
  run_check "$dir"
  expect_status "$status" 1 "a diagnosis that drops the vision model"
  expect_contains "$out" "FACT vision-cost: PASS" "a diagnosis that drops the vision model: the record's own split still holds"
  expect_contains "$out" "FACT diagnose-split: FAIL" "a diagnosis that drops the vision model: the row"
  expect_contains "$out" "prints no ${vision} in its model_by" "a diagnosis that drops the vision model: what it found"
}

test_a_diagnosis_that_cannot_be_read_is_not_measured() {
  local dir=$work/diagnosefail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=diagnose-fails
  expect_status "$status" 2 "a diagnosis that could not be read"
  expect_contains "$out" "FACT diagnose-split: FAIL" "a diagnosis that could not be read: the row"
  expect_contains "$err" "could NOT measure diagnose-split" "a diagnosis that could not be read: the exit names the row"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK520_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK520_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

# THE MUTATION. The row that reads the run's stage timings is the plan's first
# reading, and it is only worth anything if it really fails a stage left on the
# coder's own model. The case is a COPY of the script with that one comparison
# replaced by `false`, run against the same fixture the case above fails: if the
# row were not load-bearing, the copy would read the same and this test would
# report nothing.
test_the_vision_stages_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded "$coder"
  mkdir -p "$dir/copy"
  sed 's/if \[ "\$model" != "\$VISION" \]; then/if false; then/' "$script" >"$dir/copy/check520.sh"
  grep -q "if false; then" "$dir/copy/check520.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check520.sh"
  expect_contains "$out" "FACT vision-stages: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT vision-stages: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT vision-stages: FAIL" "the real script: the row"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone.
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

t test_help
t test_dry_run
t test_happy_path
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_yes_is_required
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_slot_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_doctor_without_the_vision_check_is_a_finding
t test_a_coder_that_reads_images_itself_is_a_finding
t test_an_unset_vision_slot_is_a_finding
t test_an_unrecognised_doctor_sentence_is_a_finding
t test_a_repository_the_models_list_does_not_name_is_not_measured
t test_a_row_with_no_vision_model_is_a_finding
t test_a_row_that_names_one_model_twice_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_run_that_did_not_succeed_is_a_finding
t test_a_run_over_the_bound_is_a_finding
t test_a_stage_left_on_another_model_is_a_finding
t test_a_record_with_no_stage_is_a_finding
t test_a_cost_split_without_the_vision_model_is_a_finding
t test_a_diagnosis_that_drops_the_vision_model_is_a_finding
t test_a_diagnosis_that_cannot_be_read_is_not_measured
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_vision_stages_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check520_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check520_test.sh: ok"
