#!/usr/bin/env bash
# Tests for scripts/live-checks/check511.sh, with fake `gcloud`, `fugaro` and
# `curl` on PATH: NOTHING here reaches a cloud, a project, a service or the
# network, no agent runs a live command, and the fakes record every call they
# are given so a case can assert what the check did — and did not — do.
#
# The fakes are a small model of one Cloud Run service: the fake `gcloud` holds
# the service document and renders the request log entries out of the requests
# the fake `curl` recorded, the fake `curl` answers the unserved route the way
# the container does (404 with its own route list) and reports the latency it
# was told to, and the cases that matter are the ones where that model is bent —
# a request in the window that costs a cold start, one the front end refuses,
# two instances answering, a log that has not caught up.
#
# Usage: bash scripts/live-checks/check511_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check511.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK511_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK511_TEST_HEAD:-900}")" ;;
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

# The stamp every request this run makes carries, so a case can name the entries
# the log listing is expected to hold.
stamp=test511
service=fugaro-credentials

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check511.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
version) echo "fugaro 0.21.0 (test)" ;;
doctor)
  case "$mode" in
  other-project) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
  *) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
  esac
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check511.sh's test only. It
# holds the service document, mints a stand-in identity token, and renders the
# service's request log entries out of the requests the fake `curl` recorded —
# which is what makes "one instance answered the window" a thing a case can
# arrange. Nothing here reaches a real project.
set -euo pipefail
state=$FAKE_GCLOUD_STATE
mode=${FAKE_MODE:-default}
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"

project=""; format=""; impersonate=""; audiences=""; freshness=""; limit=""
positional=()
while [ $# -gt 0 ]; do
  case "$1" in
  --project) shift; project=${1:-} ;;
  --project=*) project=${1#--project=} ;;
  --format) shift; format=${1:-} ;;
  --format=*) format=${1#--format=} ;;
  --impersonate-service-account=*) impersonate=${1#--impersonate-service-account=} ;;
  --audiences) shift; audiences=${1:-} ;;
  --audiences=*) audiences=${1#--audiences=} ;;
  --freshness) shift; freshness=${1:-} ;;
  --freshness=*) freshness=${1#--freshness=} ;;
  --limit) shift; limit=${1:-} ;;
  --limit=*) limit=${1#--limit=} ;;
  --region) shift ;;
  --region=* | --quiet | -*) ;;
  *) positional+=("$1") ;;
  esac
  shift
done

case "${positional[0]:-}" in
config)
  if [ "${positional[1]:-}" = "get-value" ] && [ "${positional[2]:-}" = "account" ]; then
    case "$mode" in
    no-account) : ;;
    *) cat "$state/account" 2>/dev/null || true ;;
    esac
  fi
  ;;
auth)
  if [ "${positional[1]:-}" = "print-identity-token" ]; then
    [ "$mode" = token-fails ] && exit 1
    # A stand-in token, never a real one and never printed by the check.
    printf 'stand-in-identity-token-for-%s\n' "${audiences:-none}"
  fi
  ;;
run)
  if [ "${positional[1]:-}" = "services" ] && [ "${positional[2]:-}" = "describe" ]; then
    [ "$mode" = no-service ] && {
      echo "ERROR: (gcloud.run.services.describe) NOT_FOUND: the service does not exist" >&2
      exit 1
    }
    cat "$state/service.json"
  fi
  ;;
logging)
  [ "$mode" = logs-unreadable ] && {
    echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED" >&2
    exit 1
  }
  # One entry per request the fake `curl` recorded, each carrying the instance
  # that answered it. `logs-lag` returns only the first: the platform's log is
  # behind the traffic, which is what the wait is for.
  printf '['
  first=yes
  n=0
  while IFS= read -r url; do
    [ -n "$url" ] || continue
    n=$((n + 1))
    case "$mode" in
    logs-lag)
      [ "$n" -eq 1 ] || break
      ;;
    esac
    id=instance-a
    case "$mode" in
    two-instances)
      if [ $((n % 2)) -eq 0 ]; then id=instance-b; fi
      ;;
    no-instance-ids) id="" ;;
    esac
    [ "$first" = yes ] || printf ','
    first=no
    printf '{"resource":{"type":"cloud_run_revision","labels":{"service_name":"%s","revision_name":"fugaro-credentials-00007-abc"' "${FAKE_SERVICE:-fugaro-credentials}"
    if [ -n "$id" ]; then printf ',"instance_id":"%s"' "$id"; fi
    printf '}},"httpRequest":{"requestUrl":"%s"}}' "$url"
  done <"$state/requests.log"
  printf ']\n'
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check511.sh's test only: it
# answers the unserved route the way the container does, reports the latency its
# mode dictates, and records the request so the fake log listing has entries to
# render. It never prints a header, and the token in the Authorization header is
# used only to decide what to answer.
set -euo pipefail
state=$FAKE_GCLOUD_STATE
mode=${FAKE_MODE:-default}
url=""; out=""; auth=""
while [ $# -gt 0 ]; do
  case "$1" in
  -o) shift; out=${1:-} ;;
  -w | --max-time) shift ;;
  -sS | -s | -S) ;;
  -H)
    shift
    case "${1:-}" in Authorization:*) auth=${1#Authorization: } ;; esac
    ;;
  http://* | https://*) url=$1 ;;
  esac
  shift
done
printf 'curl %s\n' "$url" >>"$FAKE_LOG_DIR/curl.calls"
what=${url##*n=}
printf '%s\n' "$url" >>"$state/requests.log"

body='{"error":{"code":404,"message":"this service serves POST /v1/messages, /v1/messages/count_tokens and the run routes, and nothing else"}}'
status=404
seconds=0.010000
case "$auth" in
"" | "Bearer ") status=401 ;;
esac
case "$what" in
first)
  case "$mode" in
  transport-fails) exit 7 ;;
  slow-first) seconds=2.500000 ;;
  esac
  ;;
gap)
  case "$mode" in
  cold-after-gap) seconds=2.500000 ;;
  esac
  ;;
*)
  case "$mode" in
  front-end-refuses-inside-window)
    if [ "$what" = "w2" ]; then
      status=503
      body='{"error":{"code":503,"message":"the service is unavailable"}}'
    fi
    ;;
  transport-fails-in-window)
    # curl's own failure inside the window (a connection that never came up, or
    # --max-time): a request that never completed, and not an answer at all.
    if [ "$what" = "w2" ]; then exit 28; fi
    ;;
  cold-in-window)
    if [ "$what" = "w1" ]; then seconds=2.500000; fi
    ;;
  thin-body)
    if [ "$what" = "w1" ]; then
      body='{"error":{"code":404,"message":"not found"}}'
    fi
    ;;
  esac
  ;;
esac
if [ "$mode" = no-container-body ]; then
  body='{"error":{"code":404,"message":"not found"}}'
fi
if [ -n "$out" ]; then printf '%s' "$body" >"$out"; fi
printf '%s %s' "$status" "$seconds"
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- the runs
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
  : >"$dir/logs/curl.calls"
  : >"$dir/state/requests.log"
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
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_SERVICE="$service" \
    CHECK511_STAMP="$stamp" \
    CHECK511_SERVICE="$service" \
    CHECK511_INTERVAL_SECONDS="${CHECK511_INTERVAL_SECONDS:-1}" \
    CHECK511_HOLD_SECONDS="${CHECK511_HOLD_SECONDS:-2}" \
    CHECK511_WARM_MAX_MS="${CHECK511_WARM_MAX_MS:-100}" \
    CHECK511_LOG_WAIT_SECONDS="${CHECK511_LOG_WAIT_SECONDS:-0}" \
    CHECK511_LOG_POLL_SECONDS=0 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  printf 'check511-owner@example.com\n' >"$1/state/account"
  : >"$1/state/requests.log"
  write_service "$1" 0
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
}

write_service() { # dir MIN_INSTANCES — the document the platform answers with
  cat >"$1/state/service.json" <<SERVICE_EOF
{"metadata":{"name":"$service"},
 "spec":{"template":{"scaling":{"minInstanceCount":$2},"containers":[{"image":"example.invalid/fugaro-credentials@sha256:0000"}]}},
 "status":{"url":"https://$service-abc123.us-east5.run.app","latestReadyRevisionName":"$service-00007-abc"}}
SERVICE_EOF
}

assert_no_writes() { # dir what
  local calls=$1/logs/gcloud.calls
  for verb in "run services update" "run services deploy" "run jobs" "secrets" "projects add-iam-policy-binding" "iam service-accounts create"; do
    expect_absent "$(cat "$calls")" "$verb" "$2: no '$verb' is run"
  done
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check511.sh --dry-run" "--help: the dry run is offered"
  expect_absent "$out" "FACT " "--help: no block is printed"
  assert_no_writes "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK511_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 511 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded sandbox target"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "nothing was created" "dry run: nothing was created"
  expect_contains "$out" "CHECK 511 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT heartbeat-warm: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "requests to <url>/chk511/health" "dry run: the plan says what it would send"
  expect_absent "$(cat "$dir/state/requests.log")" "chk511" "dry run: no request is made"
  assert_no_writes "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT service: PASS" "happy path: the service row"
  expect_contains "$out" "FACT scale-to-zero: PASS" "happy path: the premise"
  expect_contains "$out" "FACT container-answers: PASS" "happy path: the container answered"
  expect_contains "$out" "FACT heartbeat-answered: PASS" "happy path: the window was answered"
  expect_contains "$out" "FACT heartbeat-warm: PASS" "happy path: the window stayed warm"
  expect_contains "$out" "every request in the window was answered WITHOUT a cold start" "happy path: what the warm row says"
  expect_contains "$out" "FACT instance-ids: PASS" "happy path: one instance"
  expect_contains "$out" "name ONE instance (instance-a)" "happy path: the one instance it read"
  expect_contains "$out" "FACT idle-cost: INFO" "happy path: the opening request is a reading"
  expect_contains "$out" "FACT idle-gap: INFO" "happy path: the contrast is not asked for by default"
  expect_contains "$out" "CHECK 511 RESULT END (exit 0)" "happy path: the end line"
  # The requests really went to the service's unserved route, carrying the
  # check's own stamp, and the log listing was really read.
  local calls
  calls=$(cat "$dir/state/requests.log")
  expect_contains "$calls" "https://${service}-abc123.us-east5.run.app/chk511/health?stamp=${stamp}&n=first" "happy path: the opening request"
  expect_contains "$calls" "n=w1" "happy path: the window's first beat"
  expect_contains "$calls" "n=w2" "happy path: the window's last beat"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "logging read" "happy path: the log listing"
  # Nothing of the check's is left, and its own cleanup is what says so.
  [ ! -e "$scratch" ] || fail "happy path: the scratch directory $scratch is still there"
  # The block carries no token: the identity token is never printed.
  expect_absent "$out" "stand-in-identity-token-for-" "happy path: no token in the block"
  expect_absent "$err" "stand-in-identity-token-for-" "happy path: no token on stderr"
  assert_no_writes "$dir" "happy path"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_absent "$(cat "$dir/state/requests.log")" "chk511" "bash -x: no request is made"
}

test_a_foreign_installation_is_refused() {
  local dir=$work/foreign
  prepare "$dir"
  run_check "$dir" FAKE_MODE=other-project
  expect_status "$status" 1 "another installation"
  expect_contains "$err" "not the fugaro-dev installation" "another installation: the guard"
  expect_absent "$(cat "$dir/state/requests.log")" "chk511" "another installation: no request is made"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  printf 'check511-owner@belong.example\n' >"$dir/state/account"
  run_check "$dir"
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  expect_absent "$(cat "$dir/state/requests.log")" "chk511" "belong: no request is made"
}

test_no_account_is_refused() {
  local dir=$work/noaccount
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-account
  expect_status "$status" 2 "no account configured"
  expect_contains "$err" "no account" "no account: the message"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_a_missing_service_is_not_measured() {
  local dir=$work/noservice
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-service
  expect_status "$status" 2 "no service to describe"
  expect_contains "$err" "could not be described" "no service to describe: the message"
  expect_contains "$err" "fugaro update" "no service to describe: what deploys it"
  expect_absent "$(cat "$dir/state/requests.log")" "chk511" "no service to describe: no request is made"
}

test_a_service_with_no_url_is_not_measured() {
  local dir=$work/nourl
  prepare "$dir"
  # The document the platform answers with, without its own address.
  printf '{"metadata":{"name":"%s"},"spec":{"template":{"scaling":{"minInstanceCount":0}}}}\n' "$service" >"$dir/state/service.json"
  run_check "$dir"
  expect_status "$status" 2 "a service with no url"
  expect_contains "$err" "carries no status.url" "a service with no url: the message"
}

test_a_min_instance_above_zero_is_a_failure() {
  # D501 is the premise: if the platform keeps an instance up, the warmth is the
  # configuration's and a heartbeat answers nothing.
  local dir=$work/min1
  prepare "$dir"
  write_service "$dir" 1
  run_check "$dir"
  expect_status "$status" 1 "min_instance_count 1"
  expect_contains "$out" "FACT scale-to-zero: FAIL" "min_instance_count 1: the premise row"
  expect_contains "$out" "does not hold for this service" "min_instance_count 1: what it means"
  expect_contains "$out" "FACT heartbeat-warm: INFO" "min_instance_count 1: the warmth is not read as a criterion"
  expect_contains "$out" "cannot be attributed to the beat" "min_instance_count 1: why"
}

test_an_unknown_min_instance_count_is_not_measured() {
  local dir=$work/minunknown
  prepare "$dir"
  printf '{"metadata":{"name":"%s"},"status":{"url":"https://%s-abc123.us-east5.run.app"}}\n' "$service" "$service" >"$dir/state/service.json"
  run_check "$dir"
  expect_status "$status" 2 "no minimum instance count"
  expect_contains "$out" "FACT scale-to-zero: FAIL" "no minimum instance count: the row"
  expect_contains "$err" "could NOT measure" "no minimum instance count: the exit is a not-measured"
}

test_the_front_end_refusing_the_token_is_not_measured() {
  local dir=$work/refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=token-fails
  expect_status "$status" 2 "a token that could not be minted"
  expect_contains "$err" "could not mint an identity token" "a token that could not be minted: the message"
  expect_absent "$(cat "$dir/state/requests.log")" "chk511" "a token that could not be minted: no request is made"
}

test_a_request_that_never_completed_is_not_measured() {
  local dir=$work/transport
  prepare "$dir"
  run_check "$dir" FAKE_MODE=transport-fails
  expect_status "$status" 2 "a request that did not complete"
  expect_contains "$err" "did not complete" "a request that did not complete: the message"
}

test_an_answer_that_is_not_the_containers_is_not_measured() {
  # An answer that is not the container's own 404 is the front end answering
  # instead of an instance, which is not what the window is about: the check
  # cannot tell the two apart any other way, so it stops rather than reading the
  # front end as an instance.
  local dir=$work/thin
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-container-body
  expect_status "$status" 2 "a 404 that is not the container's"
  expect_contains "$err" "does not recognise as its container's" "a 404 that is not the container's: the message"
}

test_a_cold_start_inside_the_window_is_a_failure() {
  # U4, answered no: the instance was reaped despite the beat.
  local dir=$work/cold
  prepare "$dir"
  run_check "$dir" FAKE_MODE=cold-in-window
  expect_status "$status" 1 "a cold start inside the window"
  expect_contains "$out" "FACT heartbeat-warm: FAIL" "a cold start inside the window: the row"
  expect_contains "$out" "a cold start inside the window" "a cold start inside the window: which request"
  expect_contains "$out" "does NOT keep one up" "a cold start inside the window: what it means"
  expect_contains "$out" "returns to the owner before the heartbeat is written" "a cold start inside the window: what follows"
}

test_a_request_the_container_did_not_answer_is_a_failure() {
  local dir=$work/refused-inside
  prepare "$dir"
  run_check "$dir" FAKE_MODE=front-end-refuses-inside-window
  expect_status "$status" 1 "a request the container did not answer"
  expect_contains "$out" "FACT heartbeat-answered: FAIL" "a request the container did not answer: the row"
  expect_contains "$out" "answered 503" "a request the container did not answer: what it saw"
  expect_contains "$out" "U4 answered no" "a request the container did not answer: what it means"
}

test_a_window_request_that_never_completed_is_not_measured() {
  # A request inside the window that never completed at all — curl's own failure,
  # status 000 — is not the container failing to answer, which is U4 answered no
  # and stops DS1.5: it is a run that could not measure, exactly like the same
  # failure on the opening request. Read as a finding it would answer U4 no on a
  # transport failure, and the owner would go looking for a reaped instance.
  local dir=$work/transport-window
  prepare "$dir"
  run_check "$dir" FAKE_MODE=transport-fails-in-window
  expect_status "$status" 2 "a window request that never completed"
  expect_contains "$out" "FACT heartbeat-answered: FAIL" "a window request that never completed: the row"
  expect_contains "$out" "request 2 of 2 never completed" "a window request that never completed: which request"
  expect_contains "$out" "never completed" "a window request that never completed: what happened"
  expect_contains "$out" "not as U4 answered no" "a window request that never completed: not the finding"
  expect_absent "$out" "the beat did not keep one" "a window request that never completed: U4 is not answered no"
  expect_contains "$out" "FACT heartbeat-warm: FAIL" "a window request that never completed: the warmth row too"
  expect_contains "$err" "could NOT measure heartbeat-answered, heartbeat-warm" "a window request that never completed: the exit names both rows"
  # The request that did complete was still read: the count is of the requests
  # that completed, not of the loop's turns.
  expect_contains "$out" "the 1 of its 2 requests that completed" "a window request that never completed: the count of what was read"
}

test_two_instances_answering_is_a_failure() {
  local dir=$work/two
  prepare "$dir"
  run_check "$dir" FAKE_MODE=two-instances
  expect_status "$status" 1 "two instances answering"
  expect_contains "$out" "FACT instance-ids: FAIL" "two instances answering: the row"
  expect_contains "$out" "name 2 different instances" "two instances answering: the reading"
  expect_contains "$out" "nothing in this run measured ONE instance" "two instances answering: what it means"
}

test_a_log_that_has_not_caught_up_is_not_measured() {
  local dir=$work/lag
  prepare "$dir"
  run_check "$dir" FAKE_MODE=logs-lag
  expect_status "$status" 2 "a log that has not caught up"
  expect_contains "$out" "FACT instance-ids: FAIL" "a log that has not caught up: the row"
  expect_contains "$out" "the log lags the platform" "a log that has not caught up: why"
  expect_contains "$err" "could NOT measure" "a log that has not caught up: the exit"
}

test_a_log_listing_that_cannot_be_read_is_not_measured() {
  local dir=$work/logfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=logs-unreadable
  expect_status "$status" 2 "a log listing that could not be read"
  expect_contains "$out" "could not be read or parsed" "a log listing that could not be read: the row"
  expect_contains "$out" "roles/logging.viewer" "a log listing that could not be read: what it needs"
}

test_entries_without_instance_ids_are_not_measured() {
  local dir=$work/noids
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-instance-ids
  expect_status "$status" 2 "entries with no instance id"
  expect_contains "$out" "carry no instance id" "entries with no instance id: the row"
  expect_contains "$out" "cannot say how many instances answered" "entries with no instance id: what it means"
}

test_the_instance_ids_row_can_be_turned_off() {
  local dir=$work/noids-off
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-instance-ids CHECK511_INSTANCE_IDS=0
  expect_status "$status" 0 "the row not asked for"
  expect_contains "$out" "FACT instance-ids: INFO" "the row not asked for: it is a reading, not a criterion"
  expect_contains "$out" "not asked for (CHECK511_INSTANCE_IDS=0)" "the row not asked for: the reason"
}

test_the_idle_gap_contrast_is_read_when_asked_for() {
  # The contrast is a reading and never a criterion: a gap recorded as a cold
  # start says the beat is what prevents it, and a gap that stays warm says the
  # platform's own idle window covers it.
  local dir=$work/gap
  prepare "$dir"
  run_check "$dir" FAKE_MODE=cold-after-gap CHECK511_IDLE_GAP_SECONDS=1
  expect_status "$status" 0 "the gap that went cold"
  expect_contains "$out" "FACT idle-gap: INFO" "the gap that went cold: the row"
  expect_contains "$out" "the gap cost a cold start" "the gap that went cold: the reading"
  expect_contains "$(cat "$dir/state/requests.log")" "n=gap" "the gap that went cold: the contrast request was made"
}

test_the_idle_gap_contrast_can_stay_warm() {
  local dir=$work/gap-warm
  prepare "$dir"
  run_check "$dir" CHECK511_IDLE_GAP_SECONDS=1
  expect_status "$status" 0 "the gap that stayed warm"
  expect_contains "$out" "the instance survived the gap" "the gap that stayed warm: the reading"
}

test_a_zero_interval_is_refused() {
  local dir=$work/interval
  prepare "$dir"
  run_check "$dir" CHECK511_INTERVAL_SECONDS=0
  expect_status "$status" 2 "a zero interval"
  expect_contains "$err" "has to be a positive number of seconds" "a zero interval: the message"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone (a solo rerun of one that
# looked flaky). Neither set: all of them run.
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
t test_xtrace_refused
t test_a_foreign_installation_is_refused
t test_belong_is_refused_by_name
t test_no_account_is_refused
t test_an_unknown_argument_is_refused
t test_a_missing_service_is_not_measured
t test_a_service_with_no_url_is_not_measured
t test_a_min_instance_above_zero_is_a_failure
t test_an_unknown_min_instance_count_is_not_measured
t test_the_front_end_refusing_the_token_is_not_measured
t test_a_request_that_never_completed_is_not_measured
t test_an_answer_that_is_not_the_containers_is_not_measured
t test_a_cold_start_inside_the_window_is_a_failure
t test_a_request_the_container_did_not_answer_is_a_failure
t test_a_window_request_that_never_completed_is_not_measured
t test_two_instances_answering_is_a_failure
t test_a_log_that_has_not_caught_up_is_not_measured
t test_a_log_listing_that_cannot_be_read_is_not_measured
t test_entries_without_instance_ids_are_not_measured
t test_the_instance_ids_row_can_be_turned_off
t test_the_idle_gap_contrast_is_read_when_asked_for
t test_the_idle_gap_contrast_can_stay_warm
t test_a_zero_interval_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check511_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check511_test.sh: ok"
