#!/usr/bin/env bash
# Tests for scripts/live-checks/check521.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a price table or a run, no agent
# runs a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the calls
# the check makes (version, `models use --help`, doctor, the launch, the runs
# list and the cancel), and the fake `gcloud` serves the run record. The cases
# that matter are the ones where that model is bent: a build older than the
# release, a doctor whose sentence says the slot is SET, a run that never ends
# (which the check has to cancel — this check may launch nothing it cannot stop),
# a record that is another run's, a run that succeeded instead of being refused,
# a refusal under another failure class, a sentence that names no price member, a
# record with a stage in it, and a record that spent money.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check521_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check521.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK521_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK521_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK521_TEST_TIMEOUT:-60}

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

project=fugaro-chk521-test
run_id=run-20261113130000-1
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
coder=claude-opus-4-1
# The task the check launches with by default names this file, and the refusal's
# own sentence is expected to name it back.
image=docs/__screenshots__/settings.png
# The refusal the run is measured against, in the shape the runner writes it:
# `bootstrap: ` and the refusal's own title, then the problem config.Problem
# formats — the coder pin, the sentence, and the remedy (runner/images.go).
refusal="bootstrap: the coder can't read an image the task names: agent.models.coder: ${coder} is priced without an image rate, and the task names ${image}: set agent.models.vision to a model that can read images, and price that model's images with model_prices' image_tokens (0 refuses an image)"

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check521.sh's test only. It
# answers the account read the check makes and serves the run record. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk521-owner@belong.example\n' ;;
    *) printf 'chk521-owner@example.com\n' ;;
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
# A stand-in for `fugaro`, for scripts/live-checks/check521.sh's test only. The
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
    sleep 5
    ;;
  esac
  cat "$FAKE_JSON_DIR/launch.json"
  ;;
runs)
  cat "$FAKE_JSON_DIR/rows.json"
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

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  write_doctor "$dir" "slot-unset"
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"2000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261113-130000-abcd","execution":"exec-1","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"infra_error","stage":"bootstrap","terminal":true,"settled":true,"branch":"fugaro/20261113-130000-abcd","base_branch":"main"}],"totals":{"runs":1},"warnings":[]}
JSON
  write_record "$dir" "$run_id" infra_error config_invalid "$refusal" none
  cat >"$json/cancel.json" <<JSON
{"project":"$project","run":"$slug/$run_id","status":"cancelled","marker":true,"hard":true}
JSON
}

# write_doctor DIR STATE: the `vision-coder` sentence, in the three states the
# check has to tell apart. The sentences are the program's own (doctor_vision.go).
write_doctor() { # write_doctor DIR STATE
  local state=$2 problem fix
  case "$state" in
  slot-unset)
    problem="the coder model $coder is priced without an image rate, agent.models.vision is unset, and refs/heads/main tracks docs/logo.png: a stage that reads one is refused by the gateway mid-run, after its work is paid for"
    fix="set agent.models.vision to a model that can read images (fugaro models use sandbox vision <id>), and price that model's images with model_prices' image_tokens (0 refuses an image)"
    ;;
  coder-reads)
    problem="the coder model $coder is priced with an image rate, so a stage of this repository reads a picture itself; agent.models.vision is unset"
    fix="nothing to do: a coder that can read an image keeps its own model, and the vision slot is for a stage whose own model cannot read one"
    ;;
  slot-set)
    problem="the coder model $coder is priced without an image rate, and agent.models.vision is claude-sonnet-5, which a stage that reads a picture runs instead"
    fix="nothing to do: the vision slot is set, and a model it names is priced through the same table as every other pin"
    ;;
  unknown)
    problem="the coder model $coder is priced from a row this build does not know how to describe"
    fix="read the price table"
    ;;
  esac
  if [ "$state" = "no-check" ]; then
    cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk521","gcp_project":"$project","region":"us-east5"}}
JSON
    return 0
  fi
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[{"id":"vision-coder","ok":false,"severity":"warning","problem":"$problem","fix":"$fix"}],"project":{"name":"chk521","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_record DIR RUN_ID STATUS CLASS REASON STAGE_MODEL: one record. The reason
# is the run's own refusal sentence; STAGE_MODEL "none" writes no stage at all
# (the state a bootstrap refusal leaves), and "spend" writes a stage and a cost
# split, which is the reading the check exists to catch.
write_record() { # write_record DIR RUN_ID STATUS CLASS REASON STAGE_MODEL
  local stages cost
  case "$6" in
  none)
    stages="[]"
    cost='{"model_usd":0,"compute_usd":0,"compute_estimated":false,"total_usd":0,"estimate":false,"model_basis":"api-list"}'
    ;;
  spend)
    stages="[{\"name\":\"implement\",\"model\":\"$coder\",\"model_usd\":0.42}]"
    cost='{"model_usd":0.42,"compute_usd":0,"compute_estimated":false,"total_usd":0.42,"estimate":false,"model_basis":"api-list","model_by":{"'"$coder"'":0.42}}'
    ;;
  esac
  cat >"$1/json/record.json" <<JSON
{"version":1,"run_id":"$2","repo":"acme/sandbox","workflow":"agent","status":"$3","stage":"bootstrap","failure_class":"$4","reason":"$5","stages":$stages,"cost":$cost}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk521\n' >"$1/fugaro.yaml"
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
    CHECK521_SANDBOX="$dir/sandbox" \
    CHECK521_RUN_TIMEOUT_SECONDS=10 CHECK521_POLL_SECONDS=1 \
    CHECK521_CLI_TIMEOUT_SECONDS=20 CHECK521_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK521_CANCEL_TIMEOUT_SECONDS=20 CHECK521_DOCTOR_TIMEOUT_SECONDS=20 \
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
  expect_contains "$out" "bash scripts/live-checks/check521.sh --sandbox <a sandbox repository checkout>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the run is free and that the check can stop it.
  expect_contains "$out" "NOTHING billable and no model call" "--help: the cost of a run is stated"
  expect_contains "$out" "Nothing it launches can outlive it" "--help: what it does to a run that will not end"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK521_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 521 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the cost is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 521 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT premise: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT cost-zero: INFO — planned" "dry run: the cost row is planned too"
  expect_contains "$err" "FREE" "dry run: the plan says the run costs nothing"
  expect_contains "$err" "fugaro cancel <run> --now" "dry run: the plan says what it does to a run that will not end"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
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
  expect_contains "$out" "the slot is UNSET" "happy path: what the premise means"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT cancelled: INFO" "happy path: nothing had to be cancelled"
  expect_contains "$out" "not needed: the run ended by itself" "happy path: why nothing was cancelled"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the record"
  expect_contains "$out" "FACT refused: PASS" "happy path: the refusal"
  expect_contains "$out" "failure_class config_invalid" "happy path: the class it was refused under"
  expect_contains "$out" "FACT sentence: PASS" "happy path: the refusal's sentence"
  expect_contains "$out" "FACT no-stage: PASS" "happy path: no stage ran"
  expect_contains "$out" "FACT cost-zero: PASS" "happy path: nothing was spent"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore"
  expect_contains "$out" "CHECK 521 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, the record was read at the
  # run's own key — the one-segment slug, not the owner/name the launch names —
  # and nothing was cancelled, because the run ended on its own.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the run is launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the run is followed by its own slug"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${run_id}/result.json" "happy path: the record is read at the run's own key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  assert_no_cancel "$dir" "happy path"
  assert_scratch_removed "$dir" "happy path"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK521_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check521.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK521_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  assert_no_run "$dir" "a directory that is not a checkout"
}

test_a_task_that_names_no_image_is_refused() {
  # The refusal this check measures is triggered by the task's own text: a task
  # that names no picture measures nothing, and the check says so before it
  # launches anything.
  local dir=$work/noimage
  prepare "$dir"
  run_check "$dir" CHECK521_TASK="Tidy up the README and reply with one line."
  expect_status "$status" 2 "a task that names no image"
  expect_contains "$err" "names no image file" "a task that names no image: the refusal"
  expect_contains "$err" "measures nothing" "a task that names no image: why"
  assert_no_run "$dir" "a task that names no image"
}

test_a_task_that_names_an_image_by_its_kind_is_accepted() {
  # A task that says the file is a PNG, rather than naming one, is a name the
  # run's own check refuses on too (imageNamesInTask's second spelling).
  local dir=$work/kindword
  prepare "$dir"
  run_check "$dir" CHECK521_TASK="The golden in docs is a PNG now; say what changed. Do not change any file."
  expect_status "$status" 0 "a task that names the kind"
  expect_contains "$out" "FACT refused: PASS" "a task that names the kind: the run was still refused"
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
  printf '{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk521-belong","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "a project naming belong: the refusal"
  assert_no_run "$dir" "a project naming belong"
}

test_a_build_older_than_the_release_is_refused() {
  # V8's own gate: the work is unreleased, so a build that predates it has no
  # bootstrap refusal to measure.
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "a build older than the release"
  expect_contains "$out" "FACT release: FAIL" "a build older than the release: the row"
  expect_contains "$out" "predates release 0.24.0" "a build older than the release: what it found"
  expect_contains "$out" "fugaro update" "a build older than the release: the one command that fixes it"
  assert_no_run "$dir" "a build older than the release"
}

test_a_build_without_the_slot_is_refused() {
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
  # NOTHING IS LAUNCHED once the premise has failed: the run's whole cost
  # argument is that the refusal at bootstrap makes it free, and a run launched
  # without the premise reaching a stage would spend the sandbox's money.
  expect_contains "$out" "FACT run-launched: FAIL" "a doctor with no vision-coder check: the launch row is not reached"
  expect_contains "$out" "nothing was launched" "a doctor with no vision-coder check: and it says so"
  assert_no_run "$dir" "a doctor with no vision-coder check"
  assert_no_cancel "$dir" "a doctor with no vision-coder check"
}

test_a_coder_that_reads_images_itself_is_a_finding() {
  # D2016: a coder whose own price row carries an image rate reads the picture
  # itself, so the run would never be refused and the check measures nothing.
  local dir=$work/reads
  prepare "$dir"
  write_doctor "$dir" coder-reads
  run_check "$dir"
  expect_status "$status" 1 "a coder that reads images itself"
  expect_contains "$out" "FACT premise: FAIL" "a coder that reads images itself: the row"
  expect_contains "$out" "reads a picture ITSELF" "a coder that reads images itself: what it means"
  assert_no_run "$dir" "a coder that reads images itself"
  assert_no_cancel "$dir" "a coder that reads images itself"
}

test_a_set_vision_slot_is_a_finding() {
  # The slot set is Check 520's state: the run would run the picture on that
  # model rather than being refused, which is a different check's reading.
  local dir=$work/slotset
  prepare "$dir"
  write_doctor "$dir" slot-set
  run_check "$dir"
  expect_status "$status" 1 "a set vision slot"
  expect_contains "$out" "FACT premise: FAIL" "a set vision slot: the row"
  expect_contains "$out" "agent.models.vision is SET" "a set vision slot: what it found"
  expect_contains "$out" "Check 520's state" "a set vision slot: whose state it is"
  assert_no_run "$dir" "a set vision slot"
  assert_no_cancel "$dir" "a set vision slot"
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
  assert_no_cancel "$dir" "a sentence the check cannot place"
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
  local dir=$work/norun
  prepare "$dir"
  printf '{"project":"%s","run":"2000","repo":"acme/sandbox","status":"launched"}\n' "$project" >"$dir/json/launch.json"
  run_check "$dir"
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: what it found"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a launch that names no run: no record is read"
}

test_a_run_that_never_ends_is_cancelled() {
  # THE POINT OF THIS CHECK'S OWN SHAPE: it launches one run and may not leave
  # it going. A run still working after its bound is cancelled here, and the
  # block says so rather than only the log.
  local dir=$work/notterminal
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"bootstrap","terminal":false}],"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows.json"
  run_check "$dir" CHECK521_RUN_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a run that never ended"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ended: the row"
  expect_contains "$out" "had not ended after" "a run that never ended: what it saw"
  expect_contains "$out" "FACT cancelled: PASS" "a run that never ended: the cancel row"
  expect_contains "$out" "nothing this check launched outlives it" "a run that never ended: what the cancel means"
  expect_contains "$(cat "$dir/logs/cancel.calls")" "cancel $run_id --now --json" "a run that never ended: the command it ran"
  expect_contains "$err" "could NOT measure terminal" "a run that never ended: the exit names the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a run that never ended: no record is read"
}

test_a_cancel_that_does_not_answer_is_a_finding() {
  # The one way this check could leave a run behind is a cancel that does not
  # answer: it says so in the block, names the command to run by hand, and ends
  # as a check that found something — a run of its own that may still be going
  # is a finding, not a row it merely could not measure.
  local dir=$work/cancelfail
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"bootstrap","terminal":false}],"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows.json"
  run_check "$dir" CHECK521_RUN_TIMEOUT_SECONDS=1 FAKE_MODE=cancel-fails
  expect_status "$status" 1 "a cancel that did not answer"
  expect_contains "$out" "FACT cancelled: FAIL" "a cancel that did not answer: the row"
  expect_contains "$out" "may still be going" "a cancel that did not answer: what it warns"
  expect_contains "$out" "fugaro runs show ${run_id}" "a cancel that did not answer: what to do by hand"
  expect_contains "$out" "FACT restore: FAIL" "a cancel that did not answer: the restore row too"
}

test_a_cancel_that_does_not_answer_in_time_is_a_finding() {
  # The bound on the cancel is the last thing standing between the check and a
  # run left behind: a cancel that hangs is the same finding as one that fails,
  # and it does not hang the check.
  local dir=$work/cancelhang
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"bootstrap","terminal":false}],"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows.json"
  run_check "$dir" CHECK521_RUN_TIMEOUT_SECONDS=1 CHECK521_CANCEL_TIMEOUT_SECONDS=1 FAKE_MODE=cancel-hangs
  expect_status "$status" 1 "a cancel that did not answer in time"
  expect_contains "$out" "FACT cancelled: FAIL" "a cancel that did not answer in time: the row"
  expect_contains "$out" "may still be going" "a cancel that did not answer in time: what it warns"
}

test_a_launch_that_does_not_answer_in_time_is_a_finding() {
  # The launch is bounded too: a `fugaro run` that never answers must not leave
  # the check waiting, and the row says it could not measure rather than reading
  # the silence as anything at all.
  local dir=$work/launchhang
  prepare "$dir"
  run_check "$dir" CHECK521_LAUNCH_TIMEOUT_SECONDS=1 FAKE_MODE=launch-hangs
  expect_status "$status" 2 "a launch that did not answer in time"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that did not answer in time: the row"
  expect_contains "$out" "did not finish inside 1s" "a launch that did not answer in time: what it found"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a launch that did not answer in time: no record is read"
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
  write_record "$dir" run-20261113000000-9 infra_error config_invalid "$refusal" none
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20261113000000-9 and this check launched ${run_id}" "a record of another run: the two IDs"
}

test_a_run_that_succeeded_is_a_finding() {
  # The failure the work removes: a task naming a picture, no slot, and a run
  # that went ahead anyway.
  local dir=$work/succeeded
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded "" "the run finished" spend
  run_check "$dir"
  expect_status "$status" 1 "a run that succeeded"
  expect_contains "$out" "FACT refused: FAIL" "a run that succeeded: the row"
  expect_contains "$out" "it was not refused at bootstrap" "a run that succeeded: what it means"
  expect_contains "$out" "its stage's work was paid for and thrown away" "a run that succeeded: what it cost"
}

test_a_refusal_under_another_class_is_a_finding() {
  # The class is what says which ending it was: a bootstrap refusal of the
  # resolved configuration is recorded config_invalid, so another class is a
  # different ending and this check may not read it as the refusal.
  local dir=$work/otherclass
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error infra_error "$refusal" none
  run_check "$dir"
  expect_status "$status" 1 "a refusal under another class"
  expect_contains "$out" "FACT refused: FAIL" "a refusal under another class: the row"
  expect_contains "$out" "rather than config_invalid" "a refusal under another class: what it found"
}

test_a_sentence_that_names_no_price_member_is_a_finding() {
  # D2006: the sentence has to name the price member that gives the slot's model
  # an image rate, or an owner sets the slot and meets the same refusal again.
  local dir=$work/thinsentence
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error config_invalid \
    "bootstrap: the coder can't read an image the task names: agent.models.coder: ${coder} is priced without an image rate, and the task names ${image}" none
  run_check "$dir"
  expect_status "$status" 1 "a sentence that names no price member"
  expect_contains "$out" "FACT sentence: FAIL" "a sentence that names no price member: the row"
  expect_contains "$out" "model_prices' image_tokens" "a sentence that names no price member: what is missing"
}

test_a_sentence_that_names_no_slot_is_a_finding() {
  local dir=$work/noslot
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error config_invalid \
    "bootstrap: the coder can't read an image the task names: agent.models.coder: ${coder} is priced without an image rate, and the task names ${image}: price that model's images with model_prices' image_tokens (0 refuses an image)" none
  run_check "$dir"
  expect_status "$status" 1 "a sentence that names no slot"
  expect_contains "$out" "FACT sentence: FAIL" "a sentence that names no slot: the row"
  expect_contains "$out" "agent.models.vision" "a sentence that names no slot: what is missing"
}

test_a_record_with_no_reason_is_a_finding() {
  local dir=$work/noreason
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error config_invalid "" none
  run_check "$dir"
  expect_status "$status" 1 "a record with no reason"
  expect_contains "$out" "FACT sentence: FAIL" "a record with no reason: the row"
  expect_contains "$out" "carries no reason at all" "a record with no reason: what it found"
}

test_a_record_with_a_stage_is_a_finding() {
  # V5's whole claim is that the refusal is EARLY: a record with a stage in it
  # is the run having reached the place the refusal exists to keep it from.
  local dir=$work/stage
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error config_invalid "$refusal" spend
  run_check "$dir"
  expect_status "$status" 1 "a record with a stage"
  expect_contains "$out" "FACT no-stage: FAIL" "a record with a stage: the row"
  expect_contains "$out" "holds 1 stage(s) — implement" "a record with a stage: which stage"
  expect_contains "$out" "which is the failure V5 exists to remove" "a record with a stage: what it means"
}

test_a_run_that_spent_money_is_a_finding() {
  local dir=$work/spend
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error config_invalid "$refusal" spend
  run_check "$dir"
  expect_status "$status" 1 "a run that spent money"
  expect_contains "$out" "FACT cost-zero: FAIL" "a run that spent money: the row"
  expect_contains "$out" "to be told no" "a run that spent money: what it means"
}

test_a_cost_split_with_no_dollars_is_a_finding() {
  # model_usd 0 with a split naming a model is a call that was made: the two
  # readings are separate, and the row grades both.
  local dir=$work/split
  prepare "$dir"
  python3 - "$dir/json/record.json" "$coder" <<'PY'
import json, sys
path, coder = sys.argv[1], sys.argv[2]
doc = json.load(open(path))
doc["cost"]["model_usd"] = 0
doc["cost"]["model_by"] = {coder: 0.0}
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a split with no dollars"
  expect_contains "$out" "FACT cost-zero: FAIL" "a split with no dollars: the row"
  expect_contains "$out" "cost.model_by is not empty" "a split with no dollars: what it found"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK521_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK521_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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

# THE MUTATION. The row that reads the failure class is what tells this refusal
# apart from any other ending a run can have, and it is only worth anything if it
# really fails another class. The case is a COPY of the script with that one
# comparison replaced by `false`, run against the same fixture the case above
# fails: if the row were not load-bearing, the copy would read the same and this
# test would report nothing.
test_the_refusal_class_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  write_record "$dir" "$run_id" infra_error infra_error "$refusal" none
  mkdir -p "$dir/copy"
  sed 's/if \[ "\$class" != config_invalid \]; then/if false; then/' "$script" >"$dir/copy/check521.sh"
  grep -q "if false; then" "$dir/copy/check521.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check521.sh"
  expect_contains "$out" "FACT refused: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT refused: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT refused: FAIL" "the real script: the row"
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
t test_a_task_that_names_no_image_is_refused
t test_a_task_that_names_an_image_by_its_kind_is_accepted
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_slot_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_doctor_without_the_vision_check_is_a_finding
t test_a_coder_that_reads_images_itself_is_a_finding
t test_a_set_vision_slot_is_a_finding
t test_an_unrecognised_doctor_sentence_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_ends_is_cancelled
t test_a_cancel_that_does_not_answer_is_a_finding
t test_a_cancel_that_does_not_answer_in_time_is_a_finding
t test_a_launch_that_does_not_answer_in_time_is_a_finding
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_run_that_succeeded_is_a_finding
t test_a_refusal_under_another_class_is_a_finding
t test_a_sentence_that_names_no_price_member_is_a_finding
t test_a_sentence_that_names_no_slot_is_a_finding
t test_a_record_with_no_reason_is_a_finding
t test_a_record_with_a_stage_is_a_finding
t test_a_run_that_spent_money_is_a_finding
t test_a_cost_split_with_no_dollars_is_a_finding
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_refusal_class_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check521_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check521_test.sh: ok"
