#!/usr/bin/env bash
# Tests for scripts/live-checks/check781.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, an installation or a run, an agent runs no
# live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one installation whose credentials service
# serves every model call: the fake `fugaro` answers `doctor --json`, launches a
# run and answers the runs list; the fake `gcloud` serves the secrets list with
# the one bundle, the run's record, the job's environment and the two log reads
# (the gateway's `model call` lines and the service's `credsvc: audit` line). The
# cases that matter are the ones where that model is bent: no bundle, a job that
# still mounts a provider key, a job with no service URL, calls that all went
# direct, a routed call nobody reported, an audit line whose forward was denied,
# a run that never ends, a record that is another run's.
#
# Usage: bash scripts/live-checks/check781_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check781.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK781_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK781_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk781-test
run_id=run-20261120150000-1
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
job=fugaro-run-9f8e7d6c
execution="projects/$project/locations/us-east5/jobs/$job/executions/$job-abc123"
bundle=fugaro-project-$project-creds-bundle-9f8e7d6c5a4b3c2d
stale_run=run-20261119120000-9

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check781.sh's test only. It
# answers the account read, the secrets list, the run record, the job's
# environment and the two log reads — the last two from whatever files the case
# wrote. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk781-owner@example.com\n'
  fi
  ;;
secrets)
  if [ "${2:-}" = "list" ]; then
    case "$mode" in
    no-bundle)
      printf '[{"name":"projects/p/secrets/fugaro-base-image-token-0123456789abcdef"}]\n'
      ;;
    secrets-fail)
      echo "ERROR: (gcloud.secrets.list) PERMISSION_DENIED" >&2
      exit 1
      ;;
    *)
      printf '[{"name":"projects/p/secrets/%s"},{"name":"projects/p/secrets/fugaro-base-image-token-0123456789abcdef"}]\n' "$FAKE_BUNDLE"
      ;;
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
run)
  if [ "${2:-}" = "jobs" ] && [ "${3:-}" = "describe" ]; then
    cat "$FAKE_JSON_DIR/job.json"
  fi
  ;;
logging)
  case "$*" in
  *"credsvc: audit"*) cat "$FAKE_JSON_DIR/audit.json" ;;
  *)
    case "$mode" in
    calls-fail)
      echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED: caller does not have permission" >&2
      exit 1
      ;;
    *) cat "$FAKE_JSON_DIR/calls.json" ;;
    esac
    ;;
  esac
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
# A stand-in for `fugaro`, for scripts/live-checks/check781.sh's test only. The
# doctor read, the launch and the runs list all come from files the case wrote,
# and every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version) printf '0.28.0\n' ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
  # exits 1 with its document, as the real doctor does when a check is a warning
  [ ! -f "$FAKE_JSON_DIR/doctor.exit" ] || exit "$(cat "$FAKE_JSON_DIR/doctor.exit")"
  ;;
run)
  case "$mode" in
  launch-fails)
    printf 'fugaro: the sandbox repository is not enrolled in this installation\n' >&2
    exit 3
    ;;
  esac
  cat "$FAKE_JSON_DIR/launch.json"
  ;;
runs)
  cat "$FAKE_JSON_DIR/rows.json"
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
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk781","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"us-east5-docker.pkg.dev/p/fugaro-base:0.28.0"}}}
JSON
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"3000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261120-150000-aaaa","execution":"$execution","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.42,"total_usd":0.42,"model_basis":"reported","model_source":"gateway","reported_usd":0.39,"unreported_calls":0,"route_by":{"deepseek/deepseek-v4":3},"reported_by":{"deepseek/deepseek-v4":3}}}
JSON
  write_job "$json" clean
  write_calls "$json" routed
  write_audit "$json" ok
}

# The job the run ran as: no model key mounted anywhere, the credentials
# service's URL in its environment.
write_job() { # json-dir clean|key-mount|no-url|key-var
  case "$2" in
  key-mount)
    cat >"$1/job.json" <<'JSON'
{"metadata":{"name":"fugaro-run-9f8e7d6c"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credsvc.invalid"},{"name":"FUGARO_MODEL_KEY","valueSource":{"secretKeyRef":{"secret":"anthropic-api-key-0123456789abcdef","key":"latest"}}}]}]}}}}}}
JSON
    ;;
  key-var)
    cat >"$1/job.json" <<'JSON'
{"metadata":{"name":"fugaro-run-9f8e7d6c"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credsvc.invalid"},{"name":"FUGARO_PROVIDER_KEY_DEEPSEEK","value":"x"}]}]}}}}}}
JSON
    ;;
  no-url)
    cat >"$1/job.json" <<'JSON'
{"metadata":{"name":"fugaro-run-9f8e7d6c"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_TASK","value":"hello"}]}]}}}}}}
JSON
    ;;
  *)
    cat >"$1/job.json" <<'JSON'
{"metadata":{"name":"fugaro-run-9f8e7d6c"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credsvc.invalid"},{"name":"FUGARO_TASK","value":"hello"}]}]}}}}}}
JSON
    ;;
  esac
}

# The gateway's `model call` lines for the execution: `route` is emitted only
# for a call that went to a provider, so `direct` is the run whose calls did not.
write_calls() { # json-dir routed|direct|unreported
  case "$2" in
  direct)
    cat >"$1/calls.json" <<'JSON'
[{"jsonPayload":{"message":"model call","model":"claude-sonnet-5","serving_model":"claude-sonnet-5","status":"ok","charged_micros":120000,"cache_read":900}},{"jsonPayload":{"message":"model call","model":"claude-sonnet-5","serving_model":"claude-sonnet-5","status":"ok","charged_micros":80000}}]
JSON
    ;;
  unreported)
    cat >"$1/calls.json" <<'JSON'
[{"jsonPayload":{"message":"model call","route":"deepseek/deepseek-v4","model":"deepseek/deepseek-v4","serving_model":"deepseek-v4","priced_as":"deepseek/deepseek-v4","status":"ok","charged_micros":70000,"reported_micros":0}}]
JSON
    ;;
  *)
    cat >"$1/calls.json" <<'JSON'
[{"jsonPayload":{"message":"model call","route":"deepseek/deepseek-v4","model":"deepseek/deepseek-v4","serving_model":"deepseek-v4","priced_as":"deepseek/deepseek-v4","status":"ok","charged_micros":70000,"reported_micros":61000}},{"jsonPayload":{"message":"model call","route":"deepseek/deepseek-v4","model":"deepseek/deepseek-v4","serving_model":"deepseek-v4","priced_as":"deepseek/deepseek-v4","status":"ok","charged_micros":30000,"reported_micros":28000}},{"jsonPayload":{"message":"model call","route":"deepseek/deepseek-v4","model":"deepseek/deepseek-v4","serving_model":"deepseek-v4","priced_as":"deepseek/deepseek-v4","status":"ok","charged_micros":10000,"reported_micros":9000}}]
JSON
    ;;
  esac
}

# The service's own audit line: a NAME for the credential it served, its tier
# and its status, and never a value.
write_audit() { # json-dir ok|denied|other-run|none
  case "$2" in
  denied)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"deepseek-key","purpose":"forward","tier":"B","status":"denied","slip":"policy"}}]
JSON
    ;;
  other-run)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$stale_run","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"deepseek-key","purpose":"forward","tier":"B","status":"ok"}}]
JSON
    ;;
  none)
    printf '[]\n' >"$1/audit.json"
    ;;
  *)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"deepseek-key","purpose":"forward","tier":"B","status":"ok"}},{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"deepseek-key","purpose":"exchange","tier":"B","status":"ok"}}]
JSON
    ;;
  esac
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk781\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
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
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" FAKE_BUNDLE="$bundle" \
    CHECK781_SANDBOX="$dir/sandbox" \
    CHECK781_RUN_TIMEOUT_SECONDS="${CHECK781_RUN_TIMEOUT_SECONDS:-10}" \
    CHECK781_POLL_SECONDS=1 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  gcloud_calls=$(cat "$dir/logs/gcloud.calls")
  fugaro_calls=$(cat "$dir/logs/fugaro.calls")
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

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 781" "help: the header"
  expect_contains "$out" "What it measures" "help: the docblock"
  expect_contains "$out" "check781.sh --sandbox" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK781_*)" "help: the knobs"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK781_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT installation: INFO" "a dry run: the planned rows are not measured"
  expect_absent "$fugaro_calls" "fugaro run " "a dry run: no run is launched"
  expect_absent "$gcloud_calls" "logging read" "a dry run: no log is read"
  assert_scratch_removed "$dir" "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in installation bundle run-launched terminal result-json job-env calls charged key-from-bundle restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_absent "$out" "FACT $key: FAIL" "the happy path: no row failed"
  expect_contains "$out" "CHECK 781 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "$bundle" "the happy path: the bundle is named by its derived name"
  expect_contains "$out" "serving_model=deepseek-v4" "the happy path: the model that served the calls"
  expect_contains "$out" "credential=deepseek-key" "the happy path: the audit row names the credential"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox" "the happy path: the run is launched from the checkout"
  expect_contains "$gcloud_calls" "gcloud secrets list --project=$project" "the happy path: the secrets list is the installation's"
  expect_contains "$gcloud_calls" "gcloud run jobs describe $job" "the happy path: the job the record named is read"
  expect_contains "$gcloud_calls" "gcloud logging read" "the happy path: the logs are read"
  expect_contains "$gcloud_calls" "jsonPayload.run=\"$run_id\"" "the happy path: the audit read is this run's"
  assert_scratch_removed "$dir" "the happy path"
}

test_doctor_exit_1_is_an_answer() {
  local dir=$work/doctor-exit-1
  prepare "$dir"
  printf '1\n' >"$dir/json/doctor.exit"
  run_check "$dir"
  expect_status "$status" 0 "doctor exits 1: the document it printed is the answer"
  expect_contains "$out" "FACT installation: PASS" "doctor exits 1: the first row is measured"
}

test_a_doctor_that_prints_nothing_readable_is_not_measured() {
  local dir=$work/doctor-garbage
  prepare "$dir"
  printf 'not a document\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor that printed no document"
  expect_contains "$out" "FACT installation: FAIL" "a doctor that printed no document: the row"
  expect_contains "$err" "could NOT measure installation" "a doctor that printed no document: the reason"
}

test_a_doctor_without_a_project_is_not_measured() {
  local dir=$work/doctor-no-project
  prepare "$dir"
  printf '{"version":1,"ok":true,"checks":[]}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor without a project block"
  expect_contains "$err" "could NOT measure installation" "a doctor without a project block: the reason"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK781_SANDBOX=
  expect_status "$status" 2 "no sandbox"
  expect_contains "$err" "needs --sandbox" "no sandbox: the refusal names the flag"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "a path that is not a checkout: nothing is launched"
}

test_a_missing_bundle_is_a_finding() {
  local dir=$work/no-bundle
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-bundle
  expect_status "$status" 1 "an installation with no bundle"
  expect_contains "$out" "FACT bundle: FAIL" "an installation with no bundle: the row"
  expect_contains "$out" "creds-bundle-" "an installation with no bundle: the message names the derived name"
  # The run below is still launched, and that is deliberate: the bundle row is a
  # premise this check reads for itself, and the rows that answer the acceptance
  # — the calls that went to a provider, the service's audit line that names the
  # credential — are measured from the RUN, not from the secrets list.
  expect_contains "$fugaro_calls" "fugaro run " "an installation with no bundle: the run is still launched"
  expect_contains "$out" "FACT key-from-bundle: PASS" "an installation with no bundle: the rows the run answers are still measured"
}

test_a_secrets_read_that_does_not_answer_is_not_measured() {
  local dir=$work/secrets-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=secrets-fail
  expect_status "$status" 2 "a secrets list that does not answer"
  expect_contains "$out" "FACT bundle: FAIL" "a secrets list that does not answer: the row"
  expect_contains "$err" "could NOT measure bundle" "a secrets list that does not answer: the reason"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launch-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_absent "$gcloud_calls" "storage cat" "a launch that fails: no record is read"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"implement","terminal":false,"settled":false}],"totals":{"runs":1},"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows.json"
  run_check "$dir"
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ends: the row"
  expect_contains "$err" "could NOT measure terminal" "a run that never ends: the reason"
  expect_contains "$out" "fugaro watch $run_id" "a run that never ends: the run is not cancelled, the owner is told to watch it"
  expect_absent "$gcloud_calls" "storage cat" "a run that never ends: no record is read while it is going"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/record-missing
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT result-json: FAIL" "a record that cannot be read: the row"
  expect_contains "$gcloud_calls" "/runs/$slug/$run_id/result.json" "a record that cannot be read: the key the check reads"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/record-other
  prepare "$dir"
  printf '{"version":1,"run_id":"%s","status":"succeeded","cost":{}}\n' "$stale_run" >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_absent "$gcloud_calls" "run jobs describe" "a record of another run: no job is read"
}

test_a_job_that_still_mounts_a_provider_key_is_a_finding() {
  local dir=$work/job-key-mount
  prepare "$dir"
  write_job "$dir/json" key-mount
  run_check "$dir"
  expect_status "$status" 1 "a job that still mounts a key"
  expect_contains "$out" "FACT job-env: FAIL" "a job that still mounts a key: the row"
  expect_contains "$out" "anthropic-api-key" "a job that still mounts a key: the mount is named"
  # The log reads below it still happen: a job that holds a key does not say the
  # calls did not go through the service, and the audit row is what says it did.
  expect_contains "$gcloud_calls" "logging read" "a job that still mounts a key: the call rows are still read"
}

test_a_job_with_a_provider_key_variable_is_a_finding() {
  local dir=$work/job-key-var
  prepare "$dir"
  write_job "$dir/json" key-var
  run_check "$dir"
  expect_status "$status" 1 "a job with a provider key variable"
  expect_contains "$out" "FACT job-env: FAIL" "a job with a provider key variable: the row"
  expect_contains "$out" "FUGARO_PROVIDER_KEY_DEEPSEEK" "a job with a provider key variable: the variable is named"
}

test_a_job_with_no_service_url_is_a_finding() {
  local dir=$work/job-no-url
  prepare "$dir"
  write_job "$dir/json" no-url
  run_check "$dir"
  expect_status "$status" 1 "a job with no service URL"
  expect_contains "$out" "FACT job-env: FAIL" "a job with no service URL: the row"
  expect_contains "$out" "FUGARO_CREDENTIALS_SERVICE_URL absent" "a job with no service URL: the message says what is missing"
}

test_a_run_whose_calls_all_went_direct_is_a_finding() {
  local dir=$work/calls-direct
  prepare "$dir"
  write_calls "$dir/json" direct
  run_check "$dir"
  expect_status "$status" 1 "a run whose calls went direct"
  expect_contains "$out" "FACT calls: FAIL" "a run whose calls went direct: the row"
  expect_contains "$out" "went to Anthropic" "a run whose calls went direct: the message says what an empty route means"
}

test_a_routed_call_nobody_reported_is_a_finding() {
  local dir=$work/calls-unreported
  prepare "$dir"
  write_calls "$dir/json" unreported
  run_check "$dir"
  expect_status "$status" 1 "a routed call nobody reported"
  expect_contains "$out" "FACT calls: PASS" "a routed call nobody reported: the calls row still holds"
  expect_contains "$out" "FACT charged: FAIL" "a routed call nobody reported: the charged row"
}

test_a_log_read_that_does_not_answer_is_not_measured() {
  local dir=$work/calls-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=calls-fail
  expect_status "$status" 2 "a log read that does not answer"
  expect_contains "$out" "FACT calls: FAIL" "a log read that does not answer: the row"
  expect_contains "$err" "could NOT measure calls" "a log read that does not answer: the reason"
}

test_a_log_with_no_model_call_line_is_not_measured() {
  local dir=$work/calls-empty
  prepare "$dir"
  printf '[]\n' >"$dir/json/calls.json"
  run_check "$dir"
  expect_status "$status" 2 "a log with no model call line"
  expect_contains "$err" "could NOT measure calls" "a log with no model call line: the reason"
  expect_contains "$out" "CHECK781_LOG_VIEW" "a log with no model call line: the knob that scopes the read is named"
}

test_an_audit_line_for_another_run_is_not_measured() {
  local dir=$work/audit-other
  prepare "$dir"
  write_audit "$dir/json" other-run
  run_check "$dir"
  expect_status "$status" 2 "an audit line for another run"
  expect_contains "$out" "FACT key-from-bundle: FAIL" "an audit line for another run: the row"
  expect_contains "$err" "could NOT measure key-from-bundle" "an audit line for another run: the reason"
}

test_a_forward_that_did_not_end_ok_is_a_finding() {
  local dir=$work/audit-denied
  prepare "$dir"
  write_audit "$dir/json" denied
  run_check "$dir"
  expect_status "$status" 1 "a forward that did not end ok"
  expect_contains "$out" "FACT key-from-bundle: FAIL" "a forward that did not end ok: the row"
  expect_contains "$out" "status=denied" "a forward that did not end ok: the status is named"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "an xtrace refusal"
  expect_contains "$out" "FACT xtrace: FAIL" "an xtrace refusal: the row"
  expect_contains "$err" "this shell is tracing" "an xtrace refusal: the reason"
  expect_absent "$fugaro_calls" "fugaro run " "an xtrace refusal: nothing is launched"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the marker is named"
  expect_absent "$fugaro_calls" "fugaro run " "a coding agent's session: nothing is launched"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK781_REGION=europe-belong
  expect_status "$status" 1 "a region naming belong"
  expect_contains "$err" "names belong" "a region naming belong: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "a region naming belong: nothing is launched"
}

test_an_installation_naming_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir"
  printf '{"version":1,"ok":true,"checks":[],"project":{"gcp_project":"belong-something","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "is never run there" "an installation naming belong: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "an installation naming belong: nothing is launched"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK781_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK781_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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
t test_doctor_exit_1_is_an_answer
t test_a_doctor_that_prints_nothing_readable_is_not_measured
t test_a_doctor_without_a_project_is_not_measured
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_missing_bundle_is_a_finding
t test_a_secrets_read_that_does_not_answer_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_job_that_still_mounts_a_provider_key_is_a_finding
t test_a_job_with_a_provider_key_variable_is_a_finding
t test_a_job_with_no_service_url_is_a_finding
t test_a_run_whose_calls_all_went_direct_is_a_finding
t test_a_routed_call_nobody_reported_is_a_finding
t test_a_log_read_that_does_not_answer_is_not_measured
t test_a_log_with_no_model_call_line_is_not_measured
t test_an_audit_line_for_another_run_is_not_measured
t test_a_forward_that_did_not_end_ok_is_a_finding
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_an_installation_naming_belong_stops_the_check
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check781_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check781_test.sh: ok"
