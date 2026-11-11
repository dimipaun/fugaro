#!/usr/bin/env bash
# Tests for scripts/live-checks/check-411.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, an installation or a model, no
# agent runs a live command, and the fakes record every call they are given so a
# case can assert what the check did — and did not — do.
#
# The subject of this check is a machine with no git, so the test gives it one to
# lose: `git` is put in the fakes' own bin directory, which is on the PATH the
# check mirrors, and a case asserts the mirror left it out. The fake `fugaro`
# answers an update the way the real one answers a failure, and a troubleshoot by
# writing the record and the tree a source fetch leaves — with each case bending
# one field of it (another transport, a newer release's version, a tree that is
# not there) so the rows can be read.
#
# Usage: bash scripts/live-checks/check-411_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-411.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK411_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK411_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk411-test
probe_id=update-20261111172639-48213
version=0.21.0

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-411.sh's test only. It
# answers the two reads the check makes and nothing else. Nothing here reaches a
# real project.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
case "${FAKE_MODE:-default}" in
project-exists)
  if [ "${1:-}" = "projects" ] && [ "${2:-}" = "describe" ]; then
    printf '{"projectId":"%s","lifecycleState":"ACTIVE"}\n' "${3:-}"
    exit 0
  fi
  ;;
esac
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk411-owner@example.com\n'
  fi
  ;;
projects)
  if [ "${2:-}" = "describe" ]; then
    echo "ERROR: (gcloud.projects.describe) NOT_FOUND: Requested entity was not found." >&2
    exit 1
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

# write_fake_fugaro also writes the `git` the check has to lose, into the same
# bin directory: the check mirrors PATH and must leave that one name out.
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# The git the check-411 test gives the check to lose: the mirror must not carry
# this name into the PATH it builds, and the check asks that PATH for git.
echo "fake git: this must never be reached by check-411.sh"
exit 97
GIT_EOF
  chmod +x "$1/bin/git"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check-411.sh's test only. An
# update fails the way the real one fails and leaves the log of that failure; a
# troubleshoot writes the record, the tree and the answer a source fetch and a
# diagnosis leave — on the git-less PATH the check built, which it records.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
printf 'path=%s\n' "${PATH:-}" >>"$FAKE_LOG_DIR/paths"
# Only the diagnosis is recorded here: it is the command the check builds the
# git-less, token-less environment for, and the probe beside it runs with the
# environment this shell has.
if [ "${1:-}" = troubleshoot ]; then
  printf 'token=%s\n' "${GH_TOKEN:-unset}${GITHUB_TOKEN:+ and a second one}" >>"$FAKE_LOG_DIR/gh"
fi
mode=${FAKE_MODE:-default}
cache=${XDG_CACHE_HOME:-$HOME/.cache}/fugaro/source
logs=${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/logs

case "${1:-}" in
version) printf '%s\n' "${FAKE_VERSION:-0.21.0}" ;;
update)
  if [ "$mode" = update-succeeds ]; then
    echo "Done in 1.0s, 3 steps"
    exit 0
  fi
  if [ "$mode" = update-slow ]; then
    sleep 30
    exit 2
  fi
  mkdir -p "$logs"
  if [ "$mode" != update-no-log ]; then
    {
      printf '  the state bucket did not answer\n'
      printf 'Failed in 1.2s, 2 steps\n'
    } >"$logs/${FAKE_RUN_ID}.log"
  fi
  printf 'Something went wrong. Run: fugaro troubleshoot %s\n' "$FAKE_RUN_ID"
  exit 2
  ;;
troubleshoot)
  # The record comes first, and it comes even for a diagnosis that ends without
  # an answer: the real command fetches the source before it acts on the
  # credential, which is why this check can measure the fetch either way.
  if [ "$mode" != no-record ]; then
    mkdir -p "$cache"
    kind=tarball
    case "$mode" in
    used-git) kind=git ;;
    esac
    recorded=${FAKE_VERSION:-0.21.0}
    case "$mode" in
    tag-version) recorded="v${recorded}" ;;
    newest-release) recorded=v0.9.9 ;;
    esac
    anchored=false
    case "$mode" in
    may-not-match) anchored=true ;;
    esac
    printf '{"version":"%s","commit":"","kind":"%s","may_not_match":%s}\n' \
      "$recorded" "$kind" "$anchored" >"$cache/record.json"
    if [ "$mode" != no-tree ]; then
      mkdir -p "$cache/tree"
      : >"$cache/tree/go.mod"
    fi
  fi
  if [ "$mode" = diag-fails ]; then
    echo "fugaro: no credential is available here" >&2
    exit 2
  fi
  if [ "$mode" = no-outcome ]; then exit 0; fi
  printf 'Run:       %s (update)\n' "${2:-}"
  printf 'Outcome:   configuration\n'
  exit 0
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
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
  : >"$dir/logs/paths"
  : >"$dir/logs/gh"
  status=0
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # PATH is the check's own PATH with the fakes first, and the environment a
  # token is normally in is filled in, so that a case can say the check removed
  # it: `should-not-be-used` is what the fake would record if it were still there.
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_RUN_ID="$probe_id" FAKE_VERSION="$version" \
    GH_TOKEN=should-not-be-used GITHUB_TOKEN=should-not-be-used \
    GH_CONFIG_DIR="$dir/home/.config/gh" GITHUB_ENTERPRISE_TOKEN=should-not-be-used \
    CHECK411_PROJECT="$project" \
    CHECK411_NAME=chk411-test \
    CHECK411_REGION=us-east5 \
    CHECK411_UPDATE_TIMEOUT_SECONDS="${CHECK411_UPDATE_TIMEOUT_SECONDS:-30}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
}

# assert_scratch_removed: the check names its scratch directory on stderr and
# removes it in its exit trap, whatever happened inside.
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
  expect_contains "$out" "bash scripts/live-checks/check-411.sh --dry-run" "--help: the dry run is offered"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "--help: no update is run"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK411_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 411 RESULT" "dry run: the block"
  expect_contains "$out" "FACT release-build: PASS" "dry run: the build row"
  expect_contains "$out" "FACT source-kind: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "CHECK 411 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "the tarball of the installed version is fetched now" "dry run: the plan names the tarball"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "dry run: no update is run"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "projects describe" "dry run: the project is not read either"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT release-build: PASS" "happy path: the build row"
  expect_contains "$out" "FACT project-absent: PASS" "happy path: the premise"
  expect_contains "$out" "FACT update-failed: PASS" "happy path: a real failed run"
  expect_contains "$out" "FACT run-logged: PASS" "happy path: the run to diagnose"
  expect_contains "$out" "${probe_id} in the state directory" "happy path: the run ID"
  expect_contains "$out" "FACT no-git: PASS" "happy path: the git-less PATH"
  expect_contains "$out" "and no git at all" "happy path: what the PATH was asked and answered"
  expect_contains "$out" "FACT no-account: PASS" "happy path: the account row"
  expect_contains "$out" "FACT source-kind: PASS" "happy path: the transport"
  expect_contains "$out" "the record says kind=tarball" "happy path: which transport"
  expect_contains "$out" "FACT source-version: PASS" "happy path: the version"
  expect_contains "$out" "the version this machine is running" "happy path: what matched"
  expect_contains "$out" "FACT source-anchored: PASS" "happy path: the anchoring"
  expect_contains "$out" "FACT source-tree: PASS" "happy path: the tree"
  expect_contains "$out" "FACT diagnosis: PASS" "happy path: the whole command ran"
  expect_contains "$out" "CHECK 411 RESULT END (exit 0)" "happy path: the end line"
  # The diagnosis really ran on the PATH the check built — no git in it — and
  # with the token variables gone.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "troubleshoot ${probe_id}" "happy path: the diagnosis command"
  expect_contains "$(cat "$dir/logs/paths")" "path=${scratch}/path" "happy path: the child ran with the mirrored PATH"
  expect_absent "$(cat "$dir/logs/paths")" ":${dir}/bin" "happy path: the mirrored PATH is the only one the child got"
  expect_absent "$(cat "$dir/logs/gh")" "should-not-be-used" "happy path: no GitHub token reached the child"
  expect_contains "$(cat "$dir/logs/gh")" "token=unset" "happy path: the token variables were removed"
  assert_scratch_removed "$dir" "happy path"
}

test_the_mirrored_path_leaves_git_out() {
  # The premise of the whole check, so it is asserted on what the child was given:
  # the fake git fails loudly, and no case may reach it.
  local dir=$work/mirror
  prepare "$dir"
  run_check "$dir"
  expect_absent "$out" "fake git" "the mirrored PATH: the check never ran the git it was given to lose"
}

test_a_build_that_names_no_release_is_not_measured() {
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_VERSION=dev
  expect_status "$status" 2 "a dev build"
  expect_contains "$out" "FACT release-build: FAIL" "a dev build: the row"
  expect_contains "$out" "D1611" "a dev build: which decision it is"
  expect_contains "$err" "could NOT measure release-build" "a dev build: the exit names the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a dev build: it stops before the probe"
}

test_a_version_that_is_not_a_release_is_not_measured() {
  local dir=$work/odd
  prepare "$dir"
  run_check "$dir" FAKE_VERSION=1.2
  expect_status "$status" 2 "a version that is not a release"
  expect_contains "$out" "FACT release-build: FAIL" "a version that is not a release: the row"
  expect_contains "$out" "cannot read as a release" "a version that is not a release: what it saw"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a version that is not a release: it stops before the probe"
}

test_the_xtrace_guard() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "bash -x: no update is run"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK411_PROJECT=fugaro-chk411-belong
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "belong: no update is run"
}

test_a_project_that_exists_stops_the_check() {
  local dir=$work/exists
  prepare "$dir"
  run_check "$dir" FAKE_MODE=project-exists
  expect_status "$status" 2 "a project that exists"
  expect_contains "$err" "needs a project that does not exist" "a project that exists: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update" "a project that exists: no update is run"
}

test_a_successful_update_is_a_finding() {
  local dir=$work/succeeded
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-succeeds
  expect_status "$status" 1 "an update that succeeded"
  expect_contains "$err" "SUCCEEDED" "an update that succeeded: the finding"
}

test_an_update_with_no_log_is_not_measured() {
  local dir=$work/nolog
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-no-log
  expect_status "$status" 2 "an update with no log"
  expect_contains "$out" "FACT run-logged: FAIL" "an update with no log: the row"
  expect_contains "$out" "left no log under" "an update with no log: what it found"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "troubleshoot" "an update with no log: no diagnosis is run"
}

test_a_probe_that_hangs_is_not_measured() {
  local dir=$work/slow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-slow CHECK411_UPDATE_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a probe that hangs"
  expect_contains "$out" "did not finish within 1s and was ended" "a probe that hangs: what it saw"
  expect_contains "$err" "could NOT measure update-failed" "a probe that hangs: the exit names the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "troubleshoot" "a probe that hangs: no diagnosis is run"
}

test_the_git_transport_is_a_finding() {
  # The check exists to say the tarball was taken on a machine without git: a
  # record that says git is one this check reads as wrong, whatever the machine.
  local dir=$work/usedgit
  prepare "$dir"
  run_check "$dir" FAKE_MODE=used-git
  expect_status "$status" 1 "the git transport"
  expect_contains "$out" "FACT source-kind: FAIL" "the git transport: the row"
  expect_contains "$out" "the record says kind=git" "the git transport: what it read"
}

test_another_versions_tree_is_a_finding() {
  local dir=$work/newest
  prepare "$dir"
  run_check "$dir" FAKE_MODE=newest-release
  expect_status "$status" 1 "another version's tree"
  expect_contains "$out" "FACT source-version: FAIL" "another version's tree: the row"
  expect_contains "$out" "the record says version=v0.9.9 and this machine runs ${version}" "another version's tree: the two versions"
}

test_a_tree_that_may_not_match_is_a_finding() {
  local dir=$work/unanchored
  prepare "$dir"
  run_check "$dir" FAKE_MODE=may-not-match
  expect_status "$status" 1 "a tree that may not match"
  expect_contains "$out" "FACT source-anchored: FAIL" "a tree that may not match: the row"
  expect_contains "$out" "may_not_match=True" "a tree that may not match: what it read"
}

test_a_tagged_version_records_the_tag() {
  # A release built with a version but no commit is fetched at its own tag, and
  # the record holds the tag: the row accepts both spellings of one version.
  local dir=$work/tag
  prepare "$dir"
  run_check "$dir" FAKE_MODE=tag-version
  expect_status "$status" 0 "a version recorded as its tag"
  expect_contains "$out" "FACT source-version: PASS" "a version recorded as its tag: the row"
  expect_contains "$out" "the record says version=v${version}" "a version recorded as its tag: what it read"
}

test_no_record_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-record
  expect_status "$status" 1 "no record"
  expect_contains "$out" "FACT source-kind: FAIL" "no record: the row"
  expect_contains "$out" "left no record at" "no record: what it found"
  expect_contains "$out" "FACT source-tree: FAIL" "no record: no tree either"
}

test_a_tree_that_is_not_there_is_a_finding() {
  local dir=$work/notree
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-tree
  expect_status "$status" 1 "a tree that is not there"
  expect_contains "$out" "FACT source-kind: PASS" "a tree that is not there: the record is still read"
  expect_contains "$out" "FACT source-tree: FAIL" "a tree that is not there: the row"
  expect_contains "$out" "left no go.mod" "a tree that is not there: what it found"
}

test_a_diagnosis_that_did_not_run_is_not_measured() {
  local dir=$work/diagfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=diag-fails
  expect_status "$status" 2 "a diagnosis that did not run"
  expect_contains "$out" "FACT diagnosis: FAIL" "a diagnosis that did not run: the row"
  expect_contains "$out" "exited 2 with the git-less PATH" "a diagnosis that did not run: what it saw"
  # The fetch happens before the credential is acted on, so the source rows are
  # measured either way — that is the whole point of this check.
  expect_contains "$out" "FACT source-kind: PASS" "a diagnosis that did not run: the fetch is still measured"
  expect_contains "$out" "FACT source-version: PASS" "a diagnosis that did not run: and its version too"
  expect_contains "$err" "could NOT measure diagnosis" "a diagnosis that did not run: the exit names the row"
}

test_a_diagnosis_without_an_outcome_is_not_measured() {
  local dir=$work/nooutcome
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-outcome
  expect_status "$status" 2 "a diagnosis with no outcome"
  expect_contains "$out" "exited 0 without printing an outcome line" "a diagnosis with no outcome: what it saw"
  expect_contains "$out" "FACT source-kind: PASS" "a diagnosis with no outcome: the source rows still stand"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK411_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK411_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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
t test_the_mirrored_path_leaves_git_out
t test_a_build_that_names_no_release_is_not_measured
t test_a_version_that_is_not_a_release_is_not_measured
t test_the_xtrace_guard
t test_belong_is_refused_by_name
t test_a_project_that_exists_stops_the_check
t test_a_successful_update_is_a_finding
t test_an_update_with_no_log_is_not_measured
t test_a_probe_that_hangs_is_not_measured
t test_the_git_transport_is_a_finding
t test_another_versions_tree_is_a_finding
t test_a_tree_that_may_not_match_is_a_finding
t test_a_tagged_version_records_the_tag
t test_no_record_is_a_finding
t test_a_tree_that_is_not_there_is_a_finding
t test_a_diagnosis_that_did_not_run_is_not_measured
t test_a_diagnosis_without_an_outcome_is_not_measured
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-411_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-411_test.sh: ok"
