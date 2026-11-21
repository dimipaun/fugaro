#!/usr/bin/env bash
# Tests for scripts/live-checks/check780.sh, with a fake `gcloud`, `git` and
# `fugaro` on PATH: NOTHING here reaches a cloud, an installation, an update or a
# run, an agent runs no live command, and the fakes record every call they are
# given — in which directory it was made — so a case can assert what the check
# did and what it did not do.
#
# The fakes model one installation that is still at the release before Release A:
# a repository whose jobs are fed from a model credential, the container behind
# that mount, no bundle yet. The fake `fugaro update` is the one that changes it
# — it writes the post-update state (the run's row terminal, the job without its
# mount, the container gone, a bundle version created now) and prints the lines
# the real one prints. The cases that matter are the ones where that model is
# bent: an update that fails, one that copies nothing, a job that keeps its
# mount, a container left behind, one another job still mounts, a deletion the
# transcript never names, a run that never finishes, one that started after the
# update, an operator's transcript that claims a deletion.
#
# Usage: bash scripts/live-checks/check780_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check780_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check780.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK780_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK780_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk780-test
run_id=run-20261120160000-1
slug=acme-sandbox-9f8e7d6c
key_container=fugaro-acme-sandbox-9f8e7d6c-anthropic-api-key-0123456789abcdef
bundle=fugaro-project-$project-creds-bundle-9f8e7d6c5a4b3c2d
origin=https://github.com/acme/sandbox.git

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check780.sh's test only. The
# job list, the secrets and the bundle's versions come from files the case wrote
# (or that the fake `fugaro update` rewrote); nothing here reaches a project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk780-owner@example.com\n'
  fi
  ;;
secrets)
  case "${2:-}" in
  list)
    if [ -f "$FAKE_JSON_DIR/after.ready" ]; then
      cat "$FAKE_JSON_DIR/secrets-after.json"
    else
      cat "$FAKE_JSON_DIR/secrets-before.json"
    fi
    ;;
  describe)
    case "$mode" in
    soft-deleted)
      printf '{"name":"projects/p/secrets/%s","deleteTime":"2026-11-20T16:00:00Z"}\n' "${3:-}"
      ;;
    describe-fails)
      echo "ERROR: (gcloud.secrets.describe) PERMISSION_DENIED" >&2
      exit 1
      ;;
    *)
      printf '{"name":"projects/p/secrets/%s"}\n' "${3:-}"
      ;;
    esac
    ;;
  versions)
    cat "$FAKE_JSON_DIR/bundle-versions.json"
    ;;
  esac
  ;;
run)
  if [ "${2:-}" = "jobs" ] && [ "${3:-}" = "list" ]; then
    if [ -f "$FAKE_JSON_DIR/after.ready" ]; then
      cat "$FAKE_JSON_DIR/jobs-after.json"
    else
      cat "$FAKE_JSON_DIR/jobs-before.json"
    fi
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

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check780.sh's test only: the
# check reads exactly one thing from git, the checkout's origin.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
case "${1:-}" in
-C)
  if [ "${3:-}" = "remote" ] && [ "${4:-}" = "get-url" ]; then
    [ -n "${FAKE_ORIGIN:-}" ] || exit 1
    printf '%s\n' "$FAKE_ORIGIN"
  fi
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check780.sh's test only. The
# doctor read, the launch and the runs list come from files the case wrote; the
# `update` is the one that changes the installation, and it changes exactly what
# the real one changes in this model: the run's row becomes terminal, the job
# loses its mount, the container is gone, the bundle has a version created now.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
json=$FAKE_JSON_DIR

case "${1:-}" in
version) printf '0.29.0\n' ;;
doctor)
  cat "$json/doctor.json"
  [ ! -f "$json/doctor.exit" ] || exit "$(cat "$json/doctor.exit")"
  ;;
run)
  case "$mode" in
  launch-fails)
    printf 'fugaro: the sandbox repository is not enrolled in this installation\n' >&2
    exit 3
    ;;
  esac
  cat "$json/launch.json"
  ;;
runs)
  if [ -f "$json/after.ready" ]; then cat "$json/rows-after.json"; else cat "$json/rows-before.json"; fi
  ;;
update)
  case "$mode" in
  update-fails)
    printf 'fugaro: the installation stage failed: the terraform binary is not on PATH\n' >&2
    exit 1
    ;;
  esac
  : >"$json/after.ready"
  if [ "$mode" = update-no-copy ]; then
    printf '[{"name":"projects/p/secrets/%s/versions/1","createTime":"2001-01-01T00:00:00Z"}]\n' "$FAKE_BUNDLE" >"$json/bundle-versions.json"
  else
    printf '[{"name":"projects/p/secrets/%s/versions/1","createTime":"2001-01-01T00:00:00Z"},{"name":"projects/p/secrets/%s/versions/2","createTime":"%s"}]\n' \
      "$FAKE_BUNDLE" "$FAKE_BUNDLE" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$json/bundle-versions.json"
  fi
  printf 'fugaro: 3 to add, 0 to change, 0 to destroy.\n'
  printf 'fugaro: the model keys are copied into the bundle: %s\n' "$FAKE_KEY_CONTAINER"
  printf 'fugaro: 1 run is in flight; the update waits for none of them and changes nothing they hold\n'
  [ "$mode" = unnamed-deletion ] ||
    printf 'fugaro: deleted %s (its value is in the bundle and no job mounts it)\n' "$FAKE_KEY_CONTAINER"
  [ "$mode" = leaky-update ] ||
    printf 'fugaro: 0 files changed\n'
  [ "$mode" != leaky-update ] ||
    printf 'fugaro: the sandbox run answered with token skabcdefghijklmnopqrstuvwxyz0123456789\n'
  printf 'fugaro: updated to 0.29.0\n'
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
  local json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk780","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"us-east5-docker.pkg.dev/p/fugaro-base:0.29.0"}}}
JSON
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"4000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261120-160000-bbbb","status":"launched"}
JSON
  cat >"$json/rows-before.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"running","stage":"implement","terminal":false,"settled":false,"started_at":"2026-11-20T15:59:00Z"}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/rows-after.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true,"started_at":"2026-11-20T15:59:00Z"}],"totals":{"runs":1},"warnings":[]}
JSON
  write_secrets_before "$json"
  write_secrets_after "$json"
  printf '[]\n' >"$json/bundle-versions.json"
  cat >"$json/operator.txt" <<JSON
fugaro: the model keys are not copied into the bundle: this account is not a project owner of $project, so 'fugaro update' has to be run by one
fugaro: the model secrets are not deleted: a project owner's update does that
fugaro: updated to 0.29.0
JSON
}

# The repository's job. Before the update it is fed from the model container and
# carries the credentials service's URL beside it (Release A's shape, the state
# this check is about); after it, the mount is gone and the URL is what is left.
write_jobs_before() { # json-dir mount|no-mount
  local json=$1 keep=${2:-mount} env
  env='{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credsvc.invalid"}'
  if [ "$keep" = mount ]; then
    env='{"name":"ANTHROPIC_API_KEY","valueSource":{"secretKeyRef":{"secret":"'"$key_container"'","key":"latest"}}},{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credsvc.invalid"}'
  fi
  printf '[{"metadata":{"name":"fugaro-run-9f8e7d6c","labels":{"fugaro_repo":"%s"},"updateTime":"2026-11-20T15:00:00Z"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_REMOTE","value":"%s"},%s]}]}}}}}}]\n' \
    "$slug" "$origin" "$env" >"$json/jobs-before.json"
}

write_jobs_after() { # json-dir [other-job-mounting-the-key]
  local json=$1 extra=${2:-}
  printf '[{"metadata":{"name":"fugaro-run-9f8e7d6c","labels":{"fugaro_repo":"%s"},"updateTime":"2026-11-20T16:00:30Z"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_REMOTE","value":"%s"},{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credsvc.invalid"}]}]}}}}}}%s]\n' \
    "$slug" "$origin" "${extra:+,$extra}" >"$json/jobs-after.json"
}

# A job of ANOTHER repository that still mounts the same container: by KS15's own
# rule a container a job mounts is kept, so this is a container the check has to
# record as kept rather than as a deletion that was not made.
other_job_mounting_the_key() {
  printf '{"metadata":{"name":"fugaro-run-other1111","labels":{"fugaro_repo":"acme-other-1111111111111111"},"updateTime":"2026-11-20T15:00:00Z"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"img","env":[{"name":"FUGARO_REMOTE","value":"https://github.com/acme/other.git"},{"name":"ANTHROPIC_API_KEY","valueSource":{"secretKeyRef":{"secret":"%s","key":"latest"}}}]}]}}}}}}' \
    "$key_container"
}

write_secrets_before() { # json-dir
  printf '[{"name":"projects/p/secrets/%s"}]\n' "$key_container" >"$1/secrets-before.json"
}

write_secrets_after() { # json-dir
  printf '[{"name":"projects/p/secrets/%s"}]\n' "$bundle" >"$1/secrets-after.json"
}

# A repository checkout: the check refuses a path that is not one, and takes the
# repository's name from its origin.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk780\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_git "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_jobs_before "$1/json" mount
  write_jobs_after "$1/json"
  write_checkout "$1/sandbox"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=() bashargs=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/git.calls"
  status=0
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and whatever started this file may
  # itself be one.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" \
    FAKE_ORIGIN="$origin" FAKE_BUNDLE="$bundle" FAKE_KEY_CONTAINER="$key_container" \
    CHECK780_SANDBOX="$dir/sandbox" \
    CHECK780_OPERATOR_TRANSCRIPT="$dir/json/operator.txt" \
    CHECK780_RUN_TIMEOUT_SECONDS="${CHECK780_RUN_TIMEOUT_SECONDS:-5}" \
    CHECK780_POLL_SECONDS=1 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  gcloud_calls=$(cat "$dir/logs/gcloud.calls")
  fugaro_calls=$(cat "$dir/logs/fugaro.calls")
  git_calls=$(cat "$dir/logs/git.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  if [ -z "$scratch" ]; then
    fail "$2: the check never named a scratch directory"
    return 0
  fi
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_no_update() { # what
  expect_absent "$fugaro_calls" "fugaro update" "$1: no update is run"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 780" "help: the header"
  expect_contains "$out" "an operator's update does none of" "help: what it measures"
  expect_contains "$out" "check780.sh --sandbox <a sandbox repository checkout>" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK780_*)" "help: the knobs"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK780_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT state-before: INFO" "a dry run: the planned rows are not measured"
  expect_absent "$fugaro_calls" "fugaro run " "a dry run: no run is launched"
  assert_no_update "a dry run"
  expect_absent "$gcloud_calls" "jobs list" "a dry run: no job is listed"
  assert_scratch_removed "$dir" "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in installation state-before run-in-flight update bundle-version mounts-dropped \
    secrets-deleted deletions-named run-finished operator-update restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_contains "$out" "FACT transcript-scan: INFO" "the happy path: the scan row is a report"
  expect_contains "$out" "0 run(s) of 32 or more characters" "the happy path: nothing credentials-shaped in the transcript"
  expect_contains "$out" "CHECK 780 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "$key_container" "the happy path: the container is named"
  expect_contains "$out" "$bundle" "the happy path: the bundle is named"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox" "the happy path: the run is launched from the checkout"
  expect_contains "$fugaro_calls" "fugaro update (in $dir/sandbox)" "the happy path: the update is run in the checkout"
  expect_contains "$git_calls" "git -C $dir/sandbox remote get-url origin" "the happy path: the repository comes from the origin"
  expect_contains "$gcloud_calls" "gcloud secrets versions list $bundle" "the happy path: the bundle's versions are read"
  expect_contains "$out" "kept because a job still mounts it — none" "the happy path: nothing was kept, and the row says so"
  assert_scratch_removed "$dir" "the happy path"
}

test_the_transcript_scan_reports_a_credential_shaped_run() {
  local dir=$work/scan
  prepare "$dir"
  # The one thing the update must never print: a value-shaped run of characters.
  # The row reports it (a commit hash looks the same) and never fails on it.
  run_check "$dir" FAKE_MODE=leaky-update
  expect_status "$status" 0 "a transcript with a credential-shaped run"
  expect_contains "$out" "FACT transcript-scan: INFO" "a transcript with a credential-shaped run: the row"
  expect_absent "$out" "FACT transcript-scan: FAIL" "a transcript with a credential-shaped run: the row is not a finding"
  expect_contains "$out" "skabcdefghij" "a transcript with a credential-shaped run: the sample is in the row"
}

test_a_doctor_that_prints_nothing_readable_is_not_measured() {
  local dir=$work/doctor-garbage
  prepare "$dir"
  printf 'not a document\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor that printed no document"
  expect_contains "$out" "FACT installation: FAIL" "a doctor that printed no document: the row"
  expect_contains "$err" "could NOT measure installation" "a doctor that printed no document: the reason"
  assert_no_update "a doctor that printed no document"
}

test_a_doctor_without_a_project_is_not_measured() {
  local dir=$work/doctor-no-project
  prepare "$dir"
  printf '{"version":1,"ok":true,"checks":[]}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 2 "a doctor without a project block"
  expect_contains "$out" "FACT installation: FAIL" "a doctor without a project block: the row"
  expect_contains "$err" "could NOT measure installation" "a doctor without a project block: the reason"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK780_SANDBOX=
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
}

test_a_checkout_with_no_origin_stops_the_check() {
  local dir=$work/no-origin
  prepare "$dir"
  run_check "$dir" FAKE_ORIGIN=
  expect_status "$status" 1 "a checkout with no origin"
  expect_contains "$err" "no origin naming a repository" "a checkout with no origin: the refusal says why"
  assert_no_update "a checkout with no origin"
}

test_an_installation_that_mounts_nothing_is_not_measured() {
  local dir=$work/no-mount
  prepare "$dir"
  write_jobs_before "$dir/json" no-mount
  run_check "$dir"
  expect_status "$status" 2 "an installation whose job mounts nothing"
  expect_contains "$out" "FACT state-before: FAIL" "an installation whose job mounts nothing: the row"
  expect_contains "$err" "could NOT measure state-before" "an installation whose job mounts nothing: the reason"
  expect_contains "$out" "still at the release before Release A" "an installation whose job mounts nothing: the row says what state it needs"
  expect_absent "$fugaro_calls" "fugaro run " "an installation whose job mounts nothing: no run is launched"
  assert_no_update "an installation whose job mounts nothing"
}

test_a_repository_with_no_jobs_is_not_measured() {
  local dir=$work/no-jobs
  prepare "$dir"
  printf '[]\n' >"$dir/json/jobs-before.json"
  run_check "$dir"
  expect_status "$status" 2 "a repository with no jobs"
  expect_contains "$out" "FACT state-before: FAIL" "a repository with no jobs: the row"
  expect_contains "$out" "no job of acme/sandbox" "a repository with no jobs: the row names the repository"
  expect_absent "$fugaro_calls" "fugaro run " "a repository with no jobs: no run is launched"
  assert_no_update "a repository with no jobs"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launch-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-in-flight: FAIL" "a launch that fails: the row"
  expect_contains "$out" "fugaro run exited 3" "a launch that fails: the status is named"
  assert_no_update "a launch that fails: with no run there is nothing for it to be in flight across"
}

test_a_run_that_already_ended_is_a_finding() {
  local dir=$work/run-ended
  prepare "$dir"
  cp "$dir/json/rows-after.json" "$dir/json/rows-before.json"
  run_check "$dir"
  expect_status "$status" 1 "a run that had already ended"
  expect_contains "$out" "FACT run-in-flight: FAIL" "a run that had already ended: the row"
  expect_contains "$out" "already reads terminal" "a run that had already ended: the message says what it read"
  # The launch is a real one and is not cancelled, and the update still runs: the
  # rows it measures are the update's own three effects, and those are worth
  # reading even when U3's own run is not the one this check needed.
  expect_contains "$fugaro_calls" "fugaro update" "a run that had already ended: the update still runs"
}

test_an_update_that_fails_is_a_finding() {
  local dir=$work/update-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-fails
  expect_status "$status" 1 "an update that fails"
  expect_contains "$out" "FACT update: FAIL" "an update that fails: the row"
  expect_contains "$out" "fugaro update exited 1" "an update that fails: the status is named"
  expect_contains "$out" "the terraform binary is not on PATH" "an update that fails: what the tool said is in the row"
  expect_absent "$gcloud_calls" "secrets versions list" "an update that fails: the rows below it read nothing"
}

test_an_update_that_copies_nothing_is_a_finding() {
  local dir=$work/update-no-copy
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-no-copy
  expect_status "$status" 1 "an update that wrote no bundle version"
  expect_contains "$out" "FACT bundle-version: FAIL" "an update that wrote no bundle version: the row"
  expect_contains "$out" "NONE of them was created inside the update's window" "an update that wrote no bundle version: the message says what it compared"
}

test_an_installation_with_no_bundle_is_a_finding() {
  local dir=$work/no-bundle
  prepare "$dir"
  printf '[{"name":"projects/p/secrets/fugaro-base-image-token-0123456789abcdef"}]\n' >"$dir/json/secrets-after.json"
  run_check "$dir"
  expect_status "$status" 1 "an installation left with no bundle"
  expect_contains "$out" "FACT bundle-version: FAIL" "an installation left with no bundle: the row"
  expect_contains "$out" "no secret whose derived name ends in creds-bundle-" "an installation left with no bundle: the message names the derived name"
}

test_a_job_that_keeps_its_mount_is_a_finding() {
  local dir=$work/keeps-mount
  prepare "$dir"
  cp "$dir/json/jobs-before.json" "$dir/json/jobs-after.json"
  run_check "$dir"
  expect_status "$status" 1 "a job that keeps its mount"
  expect_contains "$out" "FACT mounts-dropped: FAIL" "a job that keeps its mount: the row"
  expect_contains "$out" "$key_container" "a job that keeps its mount: the mount is named"
}

test_a_container_left_behind_is_a_finding() {
  local dir=$work/still-there
  prepare "$dir"
  printf '[{"name":"projects/p/secrets/%s"},{"name":"projects/p/secrets/%s"}]\n' \
    "$bundle" "$key_container" >"$dir/json/secrets-after.json"
  run_check "$dir"
  expect_status "$status" 1 "a container left in Secret Manager"
  expect_contains "$out" "FACT secrets-deleted: FAIL" "a container left in Secret Manager: the row"
  expect_contains "$out" "STILL in Secret Manager" "a container left in Secret Manager: the message says what it found"
}

test_a_softly_deleted_container_is_gone() {
  local dir=$work/soft-deleted
  prepare "$dir"
  # Listed, and `gcloud secrets describe` answers that it carries a deleteTime:
  # Secret Manager keeps a deleted secret for thirty days, so this is the shape a
  # deletion has on the list, and describe is what settles it.
  printf '[{"name":"projects/p/secrets/%s"},{"name":"projects/p/secrets/%s"}]\n' \
    "$bundle" "$key_container" >"$dir/json/secrets-after.json"
  run_check "$dir" FAKE_MODE=soft-deleted
  expect_status "$status" 0 "a softly deleted container"
  expect_contains "$out" "FACT secrets-deleted: PASS" "a softly deleted container: the row"
  expect_contains "$gcloud_calls" "gcloud secrets describe $key_container" "a softly deleted container: describe is what settled it"
}

test_a_container_a_job_still_mounts_is_kept() {
  local dir=$work/shared
  prepare "$dir"
  write_jobs_after "$dir/json" "$(other_job_mounting_the_key)"
  printf '[{"name":"projects/p/secrets/%s"},{"name":"projects/p/secrets/%s"}]\n' \
    "$bundle" "$key_container" >"$dir/json/secrets-after.json"
  run_check "$dir"
  # Nothing was deleted — by KS15's own rule the container is kept — so the row
  # that reads the deletions back has no subject and says so: the check ends as
  # one that could not measure that row, not as one that found nothing wrong.
  expect_status "$status" 2 "a container another job still mounts"
  expect_contains "$out" "FACT secrets-deleted: PASS" "a container another job still mounts: the row"
  expect_contains "$out" "kept because a job still mounts it — $key_container (1 job(s) still mount it)" \
    "a container another job still mounts: the row says it was kept, and who mounts it"
  expect_absent "$gcloud_calls" "gcloud secrets describe $key_container" \
    "a container another job still mounts: it is not read for a deletion at all"
  # Nothing was deleted, so nothing is named: that row says so rather than
  # passing on an empty list.
  expect_contains "$out" "FACT deletions-named: FAIL" "a container another job still mounts: nothing was deleted, so nothing is named"
  expect_contains "$err" "could NOT measure deletions-named" "a container another job still mounts: the row is one the check could not measure"
}

test_a_deletion_the_transcript_never_names_is_a_finding() {
  local dir=$work/unnamed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=unnamed-deletion
  expect_status "$status" 1 "a deletion the transcript never names"
  expect_contains "$out" "FACT secrets-deleted: PASS" "a deletion the transcript never names: the container really went"
  expect_contains "$out" "FACT deletions-named: FAIL" "a deletion the transcript never names: the row"
  expect_contains "$out" "and NOT $key_container" "a deletion the transcript never names: the container it could not find"
}

test_a_run_that_never_finishes_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir"
  cp "$dir/json/rows-before.json" "$dir/json/rows-after.json"
  run_check "$dir"
  expect_status "$status" 2 "a run that never finishes"
  expect_contains "$out" "FACT run-finished: FAIL" "a run that never finishes: the row"
  expect_contains "$err" "could NOT measure run-finished" "a run that never finishes: the reason"
  expect_contains "$out" "fugaro watch $run_id" "a run that never finishes: the run is not cancelled, the owner is told to watch it"
}

test_a_run_that_ended_without_succeeding_is_a_finding() {
  local dir=$work/run-failed
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"failed","stage":"implement","terminal":true,"settled":true,"started_at":"2026-11-20T15:59:00Z"}],"totals":{"runs":1},"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows-after.json"
  run_check "$dir"
  expect_status "$status" 1 "a run that ended without succeeding"
  expect_contains "$out" "FACT run-finished: FAIL" "a run that ended without succeeding: the row"
  expect_contains "$out" "ended 'failed'" "a run that ended without succeeding: the status is named"
}

test_a_run_launched_after_the_update_is_a_finding() {
  local dir=$work/run-after
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"succeeded","stage":"writeback","terminal":true,"settled":true,"started_at":"2099-01-01T00:00:00Z"}],"totals":{"runs":1},"warnings":[]}\n' \
    "$project" "$slug" "$run_id" "$run_id" >"$dir/json/rows-after.json"
  run_check "$dir"
  expect_status "$status" 1 "a run that started after the update reached the repository"
  expect_contains "$out" "FACT run-finished: FAIL" "a run that started after the update: the row"
  expect_contains "$out" "AFTER the moment" "a run that started after the update: the message says what it compared"
}

test_no_operator_transcript_is_not_measured() {
  local dir=$work/no-operator
  prepare "$dir"
  run_check "$dir" CHECK780_OPERATOR_TRANSCRIPT=
  expect_status "$status" 2 "no operator's transcript"
  expect_contains "$out" "FACT operator-update: FAIL" "no operator's transcript: the row"
  expect_contains "$out" "--operator-transcript <file>" "no operator's transcript: the row is the instruction"
  expect_contains "$err" "could NOT measure operator-update" "no operator's transcript: the reason"
}

test_an_operator_transcript_that_is_missing_is_not_measured() {
  local dir=$work/operator-missing
  prepare "$dir"
  run_check "$dir" CHECK780_OPERATOR_TRANSCRIPT="$dir/json/not-there.txt"
  expect_status "$status" 2 "an operator's transcript that is not there"
  expect_contains "$err" "could NOT measure operator-update" "an operator's transcript that is not there: the reason"
}

test_an_operator_transcript_that_claims_a_deletion_is_a_finding() {
  local dir=$work/operator-deletes
  prepare "$dir"
  printf "fugaro: deleted %s\nfugaro: this account is not a project owner of %s, so the copy is skipped\n" \
    "$key_container" "$project" >"$dir/json/operator.txt"
  run_check "$dir"
  expect_status "$status" 1 "an operator's transcript that claims a deletion"
  expect_contains "$out" "FACT operator-update: FAIL" "an operator's transcript that claims a deletion: the row"
  expect_contains "$out" "deletions of $key_container" "an operator's transcript that claims a deletion: what it read"
}

test_an_operator_transcript_with_no_skip_line_is_a_finding() {
  local dir=$work/operator-silent
  prepare "$dir"
  printf 'fugaro: 3 to add, 0 to change, 0 to destroy.\nfugaro: updated to 0.29.0\n' >"$dir/json/operator.txt"
  run_check "$dir"
  expect_status "$status" 1 "an operator's transcript with no skip line"
  expect_contains "$out" "FACT operator-update: FAIL" "an operator's transcript with no skip line: the row"
  expect_contains "$out" "no line saying the stage was skipped" "an operator's transcript with no skip line: the message says what it looked for"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "an xtrace refusal"
  expect_contains "$out" "FACT xtrace: FAIL" "an xtrace refusal: the row"
  expect_contains "$err" "this shell is tracing" "an xtrace refusal: the reason"
  assert_no_update "an xtrace refusal"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the marker is named"
  assert_no_update "a coding agent's session"
}

test_a_region_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK780_REGION=europe-belong
  expect_status "$status" 1 "a region naming belong"
  expect_contains "$err" "names belong" "a region naming belong: the refusal says why"
  assert_no_update "a region naming belong"
}

test_an_installation_naming_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir"
  printf '{"version":1,"ok":true,"checks":[],"project":{"gcp_project":"belong-something","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "is never run there" "an installation naming belong: the refusal says why"
  assert_no_update "an installation naming belong"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK780_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK780_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check never named one"
  [ -e "${scratch:-/nonexistent}" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "${scratch:-}" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n" runs
# every n-th case starting at i (each works in its own directory under $work),
# CHECK_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$@"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_transcript_scan_reports_a_credential_shaped_run
t test_a_doctor_that_prints_nothing_readable_is_not_measured
t test_a_doctor_without_a_project_is_not_measured
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_checkout_with_no_origin_stops_the_check
t test_an_installation_that_mounts_nothing_is_not_measured
t test_a_repository_with_no_jobs_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_run_that_already_ended_is_a_finding
t test_an_update_that_fails_is_a_finding
t test_an_update_that_copies_nothing_is_a_finding
t test_an_installation_with_no_bundle_is_a_finding
t test_a_job_that_keeps_its_mount_is_a_finding
t test_a_container_left_behind_is_a_finding
t test_a_softly_deleted_container_is_gone
t test_a_container_a_job_still_mounts_is_kept
t test_a_deletion_the_transcript_never_names_is_a_finding
t test_a_run_that_never_finishes_is_not_measured
t test_a_run_that_ended_without_succeeding_is_a_finding
t test_a_run_launched_after_the_update_is_a_finding
t test_no_operator_transcript_is_not_measured
t test_an_operator_transcript_that_is_missing_is_not_measured
t test_an_operator_transcript_that_claims_a_deletion_is_a_finding
t test_an_operator_transcript_with_no_skip_line_is_a_finding
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_a_region_naming_belong_is_refused
t test_an_installation_naming_belong_stops_the_check
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check780_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check780_test.sh: ok"
