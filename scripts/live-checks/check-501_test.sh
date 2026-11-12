#!/usr/bin/env bash
# Tests for scripts/live-checks/check-501.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, an installation or a run, no
# agent runs a live command, and the fakes record every call they are given — in
# which directory it was made — so a case can assert what the check did and did
# not do.
#
# The fakes are a small model of one installation: the fake `fugaro` answers the
# two `doctor --json` reads (before and after the update, from the case's own
# files), pretends the update, and answers the launch and the runs list; the fake
# `gcloud` serves the run record. The cases that matter are the ones where that
# model is bent: an installation with no base pin, an update that fails, an update
# that never answers, a pin the update leaves empty, a run that never ends, a
# record that is another run's, and an image built before the update.
#
# The check runs `fugaro update` in the FOREGROUND with this shell's stdin, so
# the fake is asked nothing here and reads nothing: what a case measures is the
# row, not the answer — that the update asks YOU is asserted from the fake's
# transcript and from the absence of --yes.
#
# Usage: bash scripts/live-checks/check-501_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-501.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK501_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK501_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk501-test
run_id=run-20261112130000-1
before_ref=us-east5-docker.pkg.dev/p/fugaro-base:0.23.0
after_ref='us-east5-docker.pkg.dev/p/fugaro-base@sha256:aaaa'
# The two build times sit far outside the moment the update ends — one well
# before it, one well after — so the image row's comparison is the same on any
# clock this suite is run on, today or later.
old_image=2001-01-01T00:00:00Z
new_image=2099-01-01T00:00:00Z

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-501.sh's test only. It
# answers the account read the check makes and serves the run record. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk501-owner@example.com\n'
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
# A stand-in for `fugaro`, for scripts/live-checks/check-501.sh's test only. The
# two doctor reads come from files the case wrote — the second one the pin the
# update left — and every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version) printf '0.24.0\n' ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
update)
  case "$mode" in
  update-fails)
    printf 'fugaro: the installation stage failed: the terraform binary is not on PATH\n' >&2
    exit 1
    ;;
  *)
    printf 'fugaro: 1 to add, 0 to change, 0 to destroy.\n'
    printf 'fugaro: rebuilding the workflow images of %s\n' "$PWD"
    if [ -f "$FAKE_JSON_DIR/pin-after.json" ]; then
      cp "$FAKE_JSON_DIR/pin-after.json" "$FAKE_JSON_DIR/doctor.json"
    fi
    printf 'fugaro: updated to 0.24.0\n'
    ;;
  esac
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
{"version":1,"ok":true,"checks":[],"project":{"name":"chk501","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"$before_ref"}}}
JSON
  cat >"$json/pin-after.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk501","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"$after_ref"}}}
JSON
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"3000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261112-130000-abcd","execution":"exec-2","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"3000","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","image":{"built_at":"$new_image","baked_commit":"9f8e7d6"},"pushed_head":"9f8e7d6"}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk501\n' >"$1/fugaro.yaml"
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
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" \
    CHECK501_SANDBOX="$dir/sandbox" \
    CHECK501_RUN_TIMEOUT_SECONDS="${CHECK501_RUN_TIMEOUT_SECONDS:-10}" \
    CHECK501_POLL_SECONDS=1 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
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

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check-501.sh --sandbox <a sandbox repository checkout>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "--help: no update is run"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the update is theirs to answer.
  expect_contains "$out" "it is launched with no \`--yes\`" "--help: who answers the update"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK501_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 501 RESULT" "dry run: the block"
  expect_contains "$out" "FACT installation: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT update: INFO — planned" "dry run: the update is planned"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 501 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "no --yes at your own terminal" "dry run: the plan says who answers the update"
  expect_contains "$err" "the base pin after the update" "dry run: the plan names the second read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "dry run: no update is run"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "dry run: no run is launched"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage cat" "dry run: the bucket is not read"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT installation: PASS" "happy path: the installation and its project"
  expect_contains "$out" "checkout of the installation ${project} (region us-east5)" "happy path: the project it read"
  expect_contains "$out" "FACT base-pin: PASS" "happy path: the pin before the update"
  expect_contains "$out" "before the update the installation's base pin is ${before_ref}" "happy path: the ref it read"
  expect_contains "$out" "FACT update: PASS" "happy path: the update"
  expect_contains "$out" "with no --yes" "happy path: the update was not answered by the check"
  expect_contains "$out" "and it named a build" "happy path: the rebuild the update asks for is read from its own transcript"
  expect_contains "$out" "FACT pin-after: PASS" "happy path: the pin after the update"
  expect_contains "$out" "moved from ${before_ref}" "happy path: that the pin moved"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the record"
  expect_contains "$out" "FACT image: PASS" "happy path: the image was built after the update"
  expect_contains "$out" "built at ${new_image}" "happy path: the build time it read"
  expect_contains "$out" "CHECK 501 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, and the reads are the ones
  # the check documents.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "update (in $dir/sandbox)" "happy path: the update runs in the sandbox checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the run is launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the run is followed by its own slug"
  expect_absent "$calls" "--yes" "happy path: nothing is confirmed for the owner"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/acme/sandbox/${run_id}/result.json" "happy path: the record is read at the run's own key"
  assert_scratch_removed "$dir" "happy path"
}

test_the_update_is_run_without_yes() {
  # The whole point of the row: the upgrade asks its questions of the person in
  # front of it, so the check must never answer one for them. Nothing here reads
  # the fake's stdin either — the check has no answer to give.
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the update's own argv"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "fugaro update (in $dir/sandbox)" "the update's own argv: it is the bare command"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "--yes" "the update's own argv: nothing is confirmed for the owner"
}

test_skip_update_reads_the_pin_and_runs() {
  local dir=$work/skip
  prepare "$dir"
  run_check "$dir" CHECK501_SKIP_UPDATE=1
  expect_status "$status" 0 "the update left out"
  expect_contains "$out" "FACT update: INFO" "the update left out: the row"
  expect_contains "$out" "not asked for (CHECK501_SKIP_UPDATE)" "the update left out: the reason"
  expect_contains "$out" "FACT pin-after: PASS" "the update left out: the pin is still read"
  expect_contains "$out" "FACT image: INFO" "the update left out: the image row is not dated"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "the update left out: no update is run"
}

test_an_update_that_fails_is_a_finding() {
  local dir=$work/updatefails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-fails
  expect_status "$status" 1 "an update that failed"
  expect_contains "$out" "FACT update: FAIL" "an update that failed: the row"
  expect_contains "$out" "fugaro update exited 1" "an update that failed: the status"
  expect_contains "$out" "FACT pin-after: PASS" "an update that failed: the pin is still read"
  expect_contains "$out" "FACT run-launched: PASS" "an update that failed: the run is still launched, since the rows below are about the installation it left"
}

test_an_installation_with_no_base_pin_is_a_finding() {
  local dir=$work/nopin
  prepare "$dir"
  printf '{"version":1,"ok":true,"project":{"name":"chk501","gcp_project":"%s","region":"us-east5"}}\n' "$project" >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "an installation with no base pin"
  expect_contains "$out" "FACT base-pin: FAIL" "an installation with no base pin: the row"
  expect_contains "$out" "records no base_images.base" "an installation with no base pin: what it found"
}

test_a_pin_the_update_leaves_empty_is_a_finding() {
  local dir=$work/nopinafter
  prepare "$dir"
  printf '{"version":1,"ok":true,"project":{"name":"chk501","gcp_project":"%s","region":"us-east5","base_images":{}}}\n' "$project" >"$dir/json/pin-after.json"
  run_check "$dir"
  expect_status "$status" 1 "a pin the update left empty"
  expect_contains "$out" "FACT pin-after: FAIL" "a pin the update left empty: the row"
  expect_contains "$out" "records no base_images.base after the update" "a pin the update left empty: what it found"
}

test_a_pin_that_did_not_move_says_so() {
  # An installation already on this release's base is not a finding: the row says
  # which of the two happened rather than assuming the update moved something.
  local dir=$work/samepin
  prepare "$dir"
  printf '{"version":1,"ok":true,"project":{"name":"chk501","gcp_project":"%s","region":"us-east5","base_images":{"base":"%s"}}}\n' "$project" "$before_ref" >"$dir/json/pin-after.json"
  run_check "$dir"
  expect_status "$status" 0 "a pin that did not move"
  expect_contains "$out" "the same ref the row above read" "a pin that did not move: the row"
  expect_contains "$out" "already on this release's base" "a pin that did not move: the reason"
}

test_an_image_built_before_the_update_is_a_finding() {
  # The reading the check exists for: the update moving a pin says nothing until
  # a job starts on an image built after it.
  local dir=$work/oldimage
  prepare "$dir"
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stage":"writeback","image":{"built_at":"%s","baked_commit":"9f8e7d6"}}\n' "$run_id" "$old_image" >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 1 "an image built before the update"
  expect_contains "$out" "FACT image: FAIL" "an image built before the update: the row"
  expect_contains "$out" "started on an image built at ${old_image}, before the update ended" "an image built before the update: the two times"
  expect_contains "$out" "declined the rebuild" "an image built before the update: the answer the row offers"
}

test_a_record_with_no_build_time_is_not_a_finding() {
  # An image built before the field existed, and a workflow running the base
  # image directly, both leave it empty: the row says so rather than failing.
  local dir=$work/nobuiltat
  prepare "$dir"
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stage":"writeback","image":{"baked_commit":"9f8e7d6"}}\n' "$run_id" >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 0 "a record with no build time"
  expect_contains "$out" "FACT image: INFO" "a record with no build time: the row"
  expect_contains "$out" "carries no image.built_at" "a record with no build time: what it found"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/notterminal
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"3000","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"implement","terminal":false}],"warnings":[]}\n' "$project" "$run_id" >"$dir/json/rows.json"
  run_check "$dir" CHECK501_RUN_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a run that never ended"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ended: the row"
  expect_contains "$out" "had not ended after" "a run that never ended: what it saw"
  expect_contains "$out" "FACT result-json: FAIL" "a run that never ended: the record is not read"
  expect_contains "$out" "did not read its record" "a run that never ended: the reason"
  expect_contains "$err" "could NOT measure terminal, result-json" "a run that never ended: the exit names the rows"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage cat" "a run that never ended: no record is read"
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
  local dir=$work/otherrecord
  prepare "$dir"
  printf '{"version":1,"run_id":"run-20261112000000-9","repo":"acme/sandbox","status":"succeeded","stage":"writeback","image":{"built_at":"%s"}}\n' "$new_image" >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20261112000000-9 and this check launched ${run_id}" "a record of another run: the two IDs"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that failed"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that failed: the row"
  expect_contains "$out" "fugaro run exited 3" "a launch that failed: the status"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage cat" "a launch that failed: no record is read"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK501_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check-501.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK501_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a directory that is not a checkout: no update is run"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "bash -x: no update is run"
}

test_a_coding_agents_session_is_refused() {
  # The update asks the person in front of it questions a coding agent's session
  # must not answer, so refusing here is what keeps the block from reporting the
  # refusals as findings about the release.
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the refusal"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: whose check it is"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a coding agent's session: nothing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a coding agent's session: no update is run"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  cat >"$dir/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk501-belong","region":"us-east5","base_images":{"base":"$before_ref"}}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "this check is never run there" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "belong: no update is run"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK501_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK501_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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
t test_the_update_is_run_without_yes
t test_skip_update_reads_the_pin_and_runs
t test_an_update_that_fails_is_a_finding
t test_an_installation_with_no_base_pin_is_a_finding
t test_a_pin_the_update_leaves_empty_is_a_finding
t test_a_pin_that_did_not_move_says_so
t test_an_image_built_before_the_update_is_a_finding
t test_a_record_with_no_build_time_is_not_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-501_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-501_test.sh: ok"
