#!/usr/bin/env bash
# Tests for scripts/live-checks/check-913-retry-image.sh. NOTHING here is a
# cloud, a project or an installation: every case writes the documents a fake
# `gcloud` on PATH answers with — a job listing, one job's executions and the
# digest a reference resolves to — and the readings that matter are the ones
# where a digest or a retry is bent: an execution that still runs the image it
# was created with, one that runs its job's rebuilt image after a retry (U1
# settled the wrong way), one that runs the job's image without having retried
# (which settles nothing), a digest the registry would not answer, a job whose
# executions could not be read at all, and the moment this check is run at — no
# execution in flight.
#
# The calls are pinned too: every one of them is a read, none of them is
# `gcloud auth`, and every one of them ran with stdin off the terminal.
#
# Usage: bash scripts/live-checks/check-913-retry-image_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-913-retry-image.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK913_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK913_TEST_HEAD:-1600}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK913_TEST_HEAD:-1600}")"
}
expect_calls() { # calls needle count what
  local n
  n=$(printf '%s\n' "$1" | grep -c -- "$2" || true)
  [ "$n" = "$3" ] || fail "$4: expected $3 call(s) matching '$2', saw $n: $(printf '%s' "$1" | head -c 400)"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
# The Cloud Run and Artifact Registry this check reads, as far as a case needs
# them: a listing of jobs, a listing of one job's executions and the digest a
# reference resolves to, each from a file the case writes. Every call is
# recorded, with whether stdin was still the terminal: a call that left it
# attached could wait on a question nobody is there to answer.
set -euo pipefail
dir=${FAKE913_DIR:?}
printf 'gcloud %s\n' "$*" >>"${FAKE913_LOG:?}"
if [ -t 0 ]; then
  printf 'gcloud @stdin=tty\n' >>"${FAKE913_LOG:?}"
else
  printf 'gcloud @stdin=notty\n' >>"${FAKE913_LOG:?}"
fi

die() {
  printf 'ERROR: %s\n' "$1" >&2
  exit 1
}

jobs_json() {
  [ -s "$dir/jobs" ] || { printf '[]\n'; return 0; }
  local first=yes name image
  printf '['
  while IFS='|' read -r name image; do
    [ -n "$name" ] || continue
    if [ "$first" = yes ]; then first=no; else printf ','; fi
    printf '{"metadata":{"name":"%s"},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"%s"}]}}}}}}' \
      "$name" "$image"
  done <"$dir/jobs"
  printf ']\n'
}

execs_json() { # execs_json <job>
  local job=$1 first=yes name image retried running completed done_json
  local file=$dir/execs-$job
  [ -s "$file" ] || { printf '[]\n'; return 0; }
  printf '['
  while IFS='|' read -r name image retried running completed; do
    [ -n "$name" ] || continue
    if [ "$first" = yes ]; then first=no; else printf ','; fi
    done_json=""
    [ "$completed" = yes ] && done_json=',"completionTime":"2026-11-26T09:00:00Z"'
    printf '{"metadata":{"name":"projects/p/locations/us-east5/jobs/%s/executions/%s"},"status":{"retriedCount":%s,"runningCount":%s%s},"spec":{"template":{"spec":{"containers":[{"image":"%s"}]}}}}' \
      "$job" "$name" "$retried" "$running" "$done_json" "$image"
  done <"$file"
  printf ']\n'
}

registry_json() { # registry_json <ref>
  [ -f "$dir/registry-fail" ] && die "(gcloud.artifacts.docker.images.describe) PERMISSION_DENIED on $1"
  # The digest is keyed by the reference the documents carry, which is how the
  # check looks it up too; the call may carry the `:latest` the check appends to
  # a reference that names no tag, so that tag is taken off here exactly as the
  # real registry would resolve it.
  local got="" tail_tag=""
  case "${1##*/}" in
  *:*) tail_tag=${1%:*} ;;
  esac
  if [ -s "$dir/digests" ]; then
    got=$(awk -F'|' -v r="$1" '$1 == r { print $2 }' "$dir/digests" | head -n1)
    [ -n "$got" ] || [ -z "$tail_tag" ] ||
      got=$(awk -F'|' -v r="$tail_tag" '$1 == r { print $2 }' "$dir/digests" | head -n1)
  fi
  [ -n "$got" ] || die "(gcloud.artifacts.docker.images.describe) NOT_FOUND: $1"
  if [ -f "$dir/nosha" ]; then
    printf '{"image_summary":{"fully_qualified_digest":"%s"}}\n' "$1"
    return 0
  fi
  printf '{"image_summary":{"digest":"%s","fully_qualified_digest":"%s"}}\n' "$got" "$got"
}

verb="$1 ${2:-}"
case "$verb" in
"run jobs")
  case "${3:-}" in
  list)
    [ -f "$dir/jobs-fail" ] && die "(gcloud.run.jobs.list) PERMISSION_DENIED on the region"
    jobs_json
    ;;
  executions)
    [ -f "$dir/execs-fail" ] && die "(gcloud.run.jobs.executions.list) PERMISSION_DENIED"
    job=""
    for a in "$@"; do
      case "$a" in --job=*) job=${a#--job=} ;; esac
    done
    [ -n "$job" ] || die "executions list without --job: the real gcloud refuses this too"
    execs_json "$job"
    ;;
  *) die "fake gcloud: unexpected call: $*" ;;
  esac
  ;;
"artifacts docker")
  [ "${3:-}" = images ] && [ "${4:-}" = describe ] || die "fake gcloud: unexpected call: $*"
  registry_json "${5:-}"
  ;;
*)
  die "fake gcloud: unexpected call: $*"
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

# --------------------------------------------------------------- the fixtures

REGISTRY=us-east5-docker.pkg.dev/demo/fugaro-acme-app/acme-app-run
JOB=fugaro-acme-app-run
JOB_DIGEST="sha256:$(printf 'a%.0s' $(seq 64))"
OLD_DIGEST="sha256:$(printf 'b%.0s' $(seq 64))"

# case_dir makes one case's world: a project's local config naming the GCP
# project and region, and the state the happy path needs — one job deployed with
# a moving tag, and one RUNNING execution of it that records the digest it was
# created with, beside a completed one.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  confighome=$root/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS="$answers/calls"
  : >"$CALLS"
  printf 'version: 3\nname: fugaro\ngcp_project: demo-project\nregion: us-east5\nrepos:\n  acme/app:\n    base_branch: main\n' \
    >"$confighome/fugaro/projects/fugaro.yaml"
  printf '%s\n' "$JOB|$REGISTRY" >"$answers/jobs"
  printf '%s|%s\n' "$REGISTRY" "$JOB_DIGEST" >"$answers/digests"
  printf '%s|%s@%s|0|1|no\n' "$JOB-9m4k2" "$JOB-9m4k2" "$OLD_DIGEST" >"$answers/execs-$JOB"
  printf '%s-old1x|%s|0|0|yes\n' "$JOB" "$REGISTRY" >>"$answers/execs-$JOB"
}

# ------------------------------------------------------------------ the runs

run913_raw() { # run913_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE913_LOG="${CALLS:-/dev/null}" FAKE913_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="${confighome:-$work}" \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run913() { # run913 [extra args...]
  run913_raw fugaro "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run913
  expect_status "$status" 0 "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" exec PASS "the happy path"
  expect_row "$out" writes PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  expect_contains "$out" "CHECK 913 RESULT: PASS (exit 0)" "the happy path"
  expect_contains "$out" "keeps its own image" "the row says what it read"
  expect_contains "$out" "$JOB/$JOB-9m4k2" "the row names the execution"
  expect_contains "$out" "demo-project" "the premise names the GCP project it read"
  expect_contains "$out" "us-east5" "the premise names the region"
  expect_contains "$out" "1 job(s)" "the premise counts the jobs listed"
  expect_contains "$out" "1 execution(s)" "the premise counts the running executions"
  # The completed execution is not judged: this check is about what is still
  # running after the update.
  expect_not_contains "$out" "$JOB-old1x" "a completed execution is not judged"
  # The reads really happened, and the registry was asked only for the JOB's
  # reference: the execution records a digest, and a digest needs no resolving.
  local calls
  calls=$(cat "$CALLS")
  expect_calls "$calls" "run jobs list --project=demo-project --region=us-east5" 1 "the job listing"
  expect_calls "$calls" "run jobs executions list --job=$JOB --project=demo-project --region=us-east5" 1 "the executions listing"
  # Newest first: a job whose window fills with its own old sweeps would
  # otherwise cut off the running execution this check is about (check680's
  # pattern).
  expect_calls "$calls" "--sort-by=~metadata.creationTimestamp --limit=" 1 "the executions listing is read newest first"
  expect_calls "$calls" "artifacts docker images describe $REGISTRY:latest --project=demo-project" 1 "the registry read"
  expect_calls "$calls" "auth" 0 "no gcloud auth at all"
  expect_not_contains "$calls" "stdin=tty" "every call ran with stdin off the terminal"
  expect_not_contains "$calls" "print-access-token" "no token is minted anywhere"
}

test_an_execution_that_retried_its_rebuilt_image_is_a_fail() {
  # U1 settled the other way: the execution runs the image the update put on its
  # job AND its status says a task has retried, so the retry booted the rebuilt
  # image and D3304's refusal can meet this run mid retry.
  case_dir retry_took_the_new_image
  printf '%s|%s@%s|2|1|no\n' "$JOB-9m4k2" "$JOB-9m4k2" "$JOB_DIGEST" >"$answers/execs-$JOB"
  run913
  expect_status "$status" 1 "an execution that retried the rebuilt image"
  expect_row "$out" exec FAIL "an execution that retried the rebuilt image"
  expect_contains "$out" "$JOB/$JOB-9m4k2" "the FAIL row names the execution"
  expect_contains "$out" "RETRIED" "the row names the retry it turns on"
  expect_contains "$out" "CHECK 913 RESULT: FAIL (exit 1)" "the verdict"
}

test_an_execution_on_the_job_s_image_with_no_retry_settles_nothing() {
  case_dir same_image_no_retry
  printf '%s|%s@%s|0|1|no\n' "$JOB-9m4k2" "$JOB-9m4k2" "$JOB_DIGEST" >"$answers/execs-$JOB"
  run913
  expect_status "$status" 3 "the job's image with no retry"
  expect_row "$out" exec UNVERIFIED "the job's image with no retry"
  expect_contains "$out" "has retried NOTHING" "the row says what is missing"
  expect_contains "$out" "CHECK 913 RESULT: INCOMPLETE (exit 3)" "the verdict"
}

test_an_execution_that_records_only_a_tag_is_unverified() {
  # A Fugaro launch copies its job's image and overrides only its args and env,
  # so an execution that records the job's own moving tag records no digest of
  # its own: resolving that tag would answer the job's digest and read FAIL
  # whichever image the execution booted.
  case_dir exec_records_a_tag
  printf '%s|%s-exec:latest|0|1|no\n' "$JOB-9m4k2" "$REGISTRY" >"$answers/execs-$JOB"
  run913
  expect_status "$status" 3 "an execution that records a tag"
  expect_row "$out" exec UNVERIFIED "an execution that records a tag"
  expect_contains "$out" "records the reference" "the row says what the execution records"
  expect_contains "$out" "no digest of its own" "the row says the digest is missing"
  expect_not_contains "$out" "CHECK 913 RESULT: FAIL" "a tag an execution records is never a FAIL"
  # The execution's own reference is never resolved: the only registry read is
  # the JOB template's.
  expect_calls "$(cat "$CALLS")" "artifacts docker" 1 "only the job's reference is resolved"
  expect_not_contains "$(cat "$CALLS")" "$REGISTRY-exec" "the execution's reference is never sent to the registry"
}

test_a_job_digest_the_registry_would_not_answer_is_unverified() {
  # The job's template is a tag the registry answers NOT_FOUND for: the digest it
  # resolves to was not read, so no execution of it can be held to it.
  case_dir digest_unread
  : >"$answers/digests"
  run913
  expect_status "$status" 3 "a job digest the registry would not answer"
  expect_row "$out" exec UNVERIFIED "a job digest the registry would not answer"
  expect_contains "$out" "artifacts docker images describe" "the row names the read that failed"
}

test_a_registry_answer_with_no_sha256_is_unverified() {
  case_dir registry_nosha
  printf yes >"$answers/nosha"
  printf '%s|%s-exec@%s|0|1|no\n' "$JOB-9m4k2" "$REGISTRY-exec" "$OLD_DIGEST" >"$answers/execs-$JOB"
  run913
  expect_status "$status" 3 "a registry answer carrying no digest"
  expect_row "$out" exec UNVERIFIED "a registry answer carrying no digest"
  expect_contains "$out" "was not read" "the row says the job's digest was not read"
}

test_a_job_whose_executions_could_not_be_read_is_unverified() {
  case_dir executions_unread
  printf yes >"$answers/execs-fail"
  run913
  expect_status "$status" 3 "a job whose executions could not be read"
  expect_row "$out" job UNVERIFIED "a job whose executions could not be read"
  expect_contains "$out" "$JOB" "the row names the job"
  expect_contains "$out" "not a job with none running" "the row says why it matters"
  # Nothing was in flight, as far as this run could tell: that is UNVERIFIED too,
  # and it is not read as "no retry happened".
  expect_row "$out" premise UNVERIFIED "a job whose executions could not be read"
}

test_a_full_window_is_unverified() {
  # The window the check asked for is two, and the listing answered exactly two:
  # a running execution may sit outside it.
  case_dir window_full
  printf '%s-88f3c|%s@%s|0|1|no\n' "$JOB" "$JOB" "$OLD_DIGEST" >>"$answers/execs-$JOB"
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE913_LOG="$CALLS" FAKE913_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" CHECK913_LIMIT=3 \
    bash "$script" fugaro 2>"$work/err") || status=$?
  expect_status "$status" 3 "a full window"
  expect_row "$out" job UNVERIFIED "a full window"
  expect_contains "$out" "CHECK913_LIMIT" "the row names the fix"
}

test_nothing_in_flight_settles_nothing() {
  case_dir nothing_in_flight
  printf '%s-old1x|%s|0|0|yes\n' "$JOB" "$REGISTRY" >"$answers/execs-$JOB"
  run913
  expect_status "$status" 3 "nothing in flight"
  expect_row "$out" premise UNVERIFIED "nothing in flight"
  expect_contains "$out" "NO running execution" "the premise says what it found"
  expect_contains "$out" "with a run in flight" "the premise names the moment to run it at"
  expect_calls "$(cat "$CALLS")" "artifacts docker" 0 "nothing in flight: no digest is resolved"
}

test_the_failing_job_is_the_one_named() {
  case_dir two_jobs
  local second=fugaro-acme-app-review
  printf '%s\n' "$second|$REGISTRY-review" >>"$answers/jobs"
  printf '%s|%s\n' "$REGISTRY-review" "$JOB_DIGEST" >>"$answers/digests"
  # The second job's execution records a DIGEST, and it is the one its job now
  # resolves to, with a task retried: the retry took the rebuilt image. (An
  # execution recording the job's own tag records no digest and could not be
  # judged at all, which is the case below.)
  printf '%s-77a1b|%s-review@%s|3|0|no\n' "$second" "$REGISTRY" "$JOB_DIGEST" >"$answers/execs-$second"
  run913
  expect_status "$status" 1 "one job of two failing"
  expect_row "$out" exec FAIL "one job of two failing"
  expect_row "$out" exec PASS "one job of two failing"
  expect_contains "$out" "$second/$second-77a1b" "the FAIL row names the second job's execution"
  expect_contains "$out" "3 task(s) RETRIED" "the row carries the retry count"
}

test_help() {
  CALLS=
  run913_raw --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "check-913-retry-image.sh <project>" "--help prints the usage"
  [ ! -s "$work/err" ] || fail "--help: something went to stderr: $(head -c 200 "$work/err")"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run913 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 913 plan" "a dry run prints the plan"
  expect_contains "$out" "run jobs executions list" "the plan names the reads"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_no_project_is_refused() {
  CALLS=
  run913_raw
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no <project> was given" "no project"
}

test_a_second_argument_is_refused() {
  case_dir two_arguments
  run913_raw fugaro other
  expect_status "$status" 2 "a second argument"
  expect_contains "$err" "one argument only" "a second argument"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run913_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

test_a_missing_project_config_is_refused() {
  case_dir no_config
  rm "$confighome/fugaro/projects/fugaro.yaml"
  run913
  expect_status "$status" 2 "no project config"
  expect_contains "$err" "is not there" "no project config"
  [ ! -s "$CALLS" ] || fail "no project config: it called gcloud anyway: $(head -c 400 "$CALLS")"
}

test_a_config_without_a_gcp_project_is_refused() {
  case_dir config_no_project
  printf 'version: 3\nname: fugaro\nregion: us-east5\n' >"$confighome/fugaro/projects/fugaro.yaml"
  run913
  expect_status "$status" 2 "a config naming no GCP project"
  expect_contains "$err" "names no gcp_project" "a config naming no GCP project"
  [ ! -s "$CALLS" ] || fail "a config naming no GCP project: it called gcloud anyway"
}

test_a_config_without_a_region_is_refused() {
  case_dir config_no_region
  printf 'version: 3\nname: fugaro\ngcp_project: demo-project\n' >"$confighome/fugaro/projects/fugaro.yaml"
  run913
  expect_status "$status" 2 "a config naming no region"
  expect_contains "$err" "names no region" "a config naming no region"
}

test_a_gcloud_that_is_not_there_is_refused() {
  case_dir no_gcloud
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE913_LOG="$CALLS" FAKE913_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" CHECK913_GCLOUD=chk913-no-such-gcloud \
    bash "$script" fugaro 2>&1) || status=$?
  expect_status "$status" 2 "a gcloud that is not there"
  expect_contains "$out" "is not on PATH" "a gcloud that is not there"
}

test_a_job_listing_that_refuses_stops_the_run() {
  case_dir listing_refused
  printf yes >"$answers/jobs-fail"
  run913
  expect_status "$status" 2 "a job listing that refuses"
  expect_contains "$err" "gcloud run jobs list" "a job listing that refuses"
  expect_contains "$err" "roles/run.viewer" "the refusal names the read the owner needs"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE913_LOG="$CALLS" FAKE913_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash "$script" fugaro 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE913_LOG="$CALLS" FAKE913_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash -x "$script" fugaro 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
  [ ! -s "$CALLS" ] || fail "a traced shell: it called something: $(head -c 400 "$CALLS")"
}

test_the_scratch_directory_is_removed() {
  case_dir keeps_nothing
  run913
  expect_row "$out" restore PASS "the scratch directory"
  expect_contains "$out" "the scratch directory is gone" "the restore row says what went"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK913_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK913_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_an_execution_that_retried_its_rebuilt_image_is_a_fail
t test_an_execution_on_the_job_s_image_with_no_retry_settles_nothing
t test_an_execution_that_records_only_a_tag_is_unverified
t test_a_job_digest_the_registry_would_not_answer_is_unverified
t test_a_registry_answer_with_no_sha256_is_unverified
t test_a_job_whose_executions_could_not_be_read_is_unverified
t test_a_full_window_is_unverified
t test_nothing_in_flight_settles_nothing
t test_the_failing_job_is_the_one_named
t test_help
t test_a_dry_run_calls_nothing
t test_no_project_is_refused
t test_a_second_argument_is_refused
t test_an_unknown_argument_is_refused
t test_a_missing_project_config_is_refused
t test_a_config_without_a_gcp_project_is_refused
t test_a_config_without_a_region_is_refused
t test_a_gcloud_that_is_not_there_is_refused
t test_a_job_listing_that_refuses_stops_the_run
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_the_scratch_directory_is_removed

if [ "$failures" -ne 0 ]; then
  echo "check-913-retry-image_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-913-retry-image_test.sh: ok"
