#!/usr/bin/env bash
# Tests for scripts/live-checks/check-c39.sh, with fake `gcloud`, fake `fugaro`
# and a fake `curl` on PATH: NOTHING here reaches a cloud, a project, a service
# or a model, no agent runs a live command, and every fake records the calls it
# was given so a case can assert what the check did — and did not — do.
#
# The fakes are a small model of one sandbox installation: `fugaro doctor`
# reports a credentials service and a census line naming a review-only job,
# `gcloud` answers the review job's describe (RUN_… with no secret at all), the
# project's IAM policy (nothing that reads a secret), the signature the review
# run left and that run's record, the service's audit line for the signing and
# the service's URL, and the fake `curl` refuses the two calls the check makes.
# The cases that matter are the ones where that model is bent: a job that mounts
# a secret, an account that holds a secretAccessor, a signature that is not the
# service's own, an audit line that names a pull request, a route that admits a
# caller it must refuse.
#
# The fake `gcloud` mints its ID tokens as `sentinel-for-<account>` — a value
# that is obviously not a credential and is still distinctive enough to be
# searched for. Every case asserts it appears in the fake `curl`'s record of the
# header it was handed and NEVER in the check's own output, which is the one
# property this script is written to keep.
#
# Usage: bash scripts/live-checks/check-c39_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-c39.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK530_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK530_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk530-test
region=us-east5
bucket=fugaro-runs-$project
review_job=fugaro-review
coding_job=fugaro-run
review_sa=fugaro-review@$project.iam.gserviceaccount.com
coding_sa=fugaro-run@$project.iam.gserviceaccount.com
run_id=20261110-120000-b3f1
slug=edge-demo
sentinel_prefix=sentinel-for-

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-c39.sh's test only. It
# answers the reads the check makes about one sandbox installation and mints its
# ID tokens as a value that is not a credential. Nothing here reaches a project.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-ok}
case "${1:-}" in
config)
  printf 'chk530-owner@example.com\n'
  ;;
run)
  case "${2:-}" in
  jobs)
    [ "${3:-}" = describe ] || exit 1
    job=${4:-}
    secrets='[]'
    case "$mode" in
    job-mounts-a-secret)
      secrets='[{"name":"ANTHROPIC_API_KEY","valueFrom":{"secretKeyRef":{"name":"fugaro-anthropic-token","key":"latest"}}}]'
      ;;
    esac
    case "$job" in
    "$FAKE_REVIEW_JOB") sa=$FAKE_REVIEW_SA ;;
    *) sa=$FAKE_CODING_SA ;;
    esac
    if [ "$mode" = job-describe-fails ]; then
      echo "ERROR: (gcloud.run.jobs.describe) NOT_FOUND: the job is not there" >&2
      exit 1
    fi
    printf '{"spec":{"template":{"spec":{"serviceAccountName":"%s","containers":[{"env":%s}]}}}}\n' "$sa" "$secrets"
    ;;
  services)
    [ "${3:-}" = describe ] || exit 1
    if [ "$mode" = service-url-unreadable ]; then
      echo "ERROR: (gcloud.run.services.describe) NOT_FOUND: the service is not there" >&2
      exit 1
    fi
    printf 'https://fugaro-credentials-chk530-uc.a.run.app\n'
    ;;
  *) exit 1 ;;
  esac
  ;;
projects)
  [ "${2:-}" = get-iam-policy ] || exit 1
  members='[]'
  case "$mode" in
  account-holds-an-accessor)
    members='[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:'"$FAKE_REVIEW_SA"'"]}]'
    ;;
  esac
  printf '{"bindings":%s}\n' "$members"
  ;;
storage)
  case "${2:-}" in
  ls)
    if [ "$mode" = no-signature ]; then
      echo "ERROR: (gcloud.storage.ls) One or more URLs matched no objects." >&2
      exit 1
    fi
    printf 'gs://%s/runs/%s/%s/artifacts/review.json.sig\n' "$FAKE_BUCKET" "$FAKE_SLUG" "$FAKE_RUN"
    ;;
  cat)
    object=${3:-}
    case "$object" in
    *.sig)
      case "$mode" in
      signature-not-hex) printf 'not-a-signature-at-all\n' ;;
      signature-two-lines) printf 'aaaa\nbbbb\n' ;;
      signature-short) printf 'abcdef\n' ;;
      *) printf '%s\n' "$FAKE_SIGNATURE" ;;
      esac
      ;;
    *result.json)
      printf '{"run_id":"%s","repo":"%s","status":"done","outcome":"ready","cost":{"model_usd":1.25}}\n' "$FAKE_RUN" "$FAKE_SLUG"
      ;;
    *)
      printf '{"verdict":"proceed","summary":"the review of the sandbox pull request"}\n'
      ;;
    esac
    ;;
  *) exit 1 ;;
  esac
  ;;
logging)
  case "$mode" in
  no-audit-line) printf '[]\n' ;;
  audit-names-another-run)
    printf '[{"jsonPayload":{"msg":"credsvc: audit","purpose":"review_sign","run":"20261109-090000-0001","repository":"%s","job":"%s","caller":"%s","credential":"projects/p/secrets/fugaro-signing-key","tier":"run","status":"ok"}}]\n' \
      "$FAKE_SLUG" "$FAKE_REVIEW_JOB" "$FAKE_REVIEW_SA"
    ;;
  audit-carries-a-pull-request)
    printf '[{"jsonPayload":{"msg":"credsvc: audit","purpose":"review_sign","run":"%s","repository":"%s","job":"%s","caller":"%s","credential":"projects/p/secrets/fugaro-signing-key","tier":"run","status":"ok","pull_request":"17"}}]\n' \
      "$FAKE_RUN" "$FAKE_SLUG" "$FAKE_REVIEW_JOB" "$FAKE_REVIEW_SA"
    ;;
  audit-wrong-caller)
    printf '[{"jsonPayload":{"msg":"credsvc: audit","purpose":"review_sign","run":"%s","repository":"%s","job":"%s","caller":"someone-else@example.com","credential":"projects/p/secrets/fugaro-signing-key","tier":"run","status":"ok"}}]\n' \
      "$FAKE_RUN" "$FAKE_SLUG" "$FAKE_REVIEW_JOB"
    ;;
  *)
    printf '[{"jsonPayload":{"msg":"credsvc: audit","purpose":"review_sign","run":"%s","repository":"%s","job":"%s","caller":"%s","credential":"projects/p/secrets/fugaro-signing-key","tier":"run","status":"ok"}}]\n' \
      "$FAKE_RUN" "$FAKE_SLUG" "$FAKE_REVIEW_JOB" "$FAKE_REVIEW_SA"
    ;;
  esac
  ;;
auth)
  [ "${2:-}" = print-identity-token ] || exit 1
  account=""
  for arg in "$@"; do
    case "$arg" in
    --impersonate-service-account=*) account=${arg#*=} ;;
    esac
  done
  [ -n "$account" ] || exit 1
  printf '%s%s\n' "$FAKE_SENTINEL_PREFIX" "$account"
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
# A stand-in for `fugaro`, for scripts/live-checks/check-c39.sh's test only: the
# build's version and the two facts `doctor --json` carries that the check reads.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-ok}
case "${1:-}" in
version) printf '0.25.0\n' ;;
doctor)
  case "$mode" in
  doctor-fails) echo "fugaro: there is no installation here" >&2; exit 2 ;;
  esac
  census="the review job ${FAKE_REVIEW_JOB} mounts no secret"
  case "$mode" in
  no-review-job) census="every shared project job holds its credential" ;;
  no-census-row) census="" ;;
  esac
  service='{"id":"credentials-service-deployed","ok":true,"severity":"info","problem":"the credentials service is deployed and answering","fix":""}'
  case "$mode" in
  service-warning)
    service='{"id":"credentials-service-deployed","ok":false,"severity":"warning","problem":"the credentials service is not deployed","fix":"run fugaro update"}'
    ;;
  no-service-row) service="" ;;
  esac
  checks=""
  [ -n "$service" ] && checks=$service
  if [ -n "$census" ]; then
    [ -n "$checks" ] && checks="$checks,"
    checks="$checks{\"id\":\"project-account-stored\",\"ok\":true,\"severity\":\"info\",\"problem\":\"$census\",\"fix\":\"\"}"
  fi
  [ "$mode" = no-census-row ] && checks="$checks"
  printf '{"project":{"name":"chk530","gcp_project":"%s","region":"%s"},"checks":[%s]}\n' \
    "$FAKE_PROJECT" "$FAKE_REGION" "$checks"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check-c39.sh's test only. It
# reads the Authorization header out of the `--config` file the check handed it
# — recording which ACCOUNT's token travelled that way, and never the token
# itself — and answers one of the two review routes with a canned body. A mode
# decides which caller the route admits, so a case can say what the check did
# with an answer it must refuse.
set -euo pipefail
mode=${FAKE_MODE:-ok}
config="" outfile="" url="" data=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config) config=$2; shift 2 ;;
  -o) outfile=$2; shift 2 ;;
  --data | --data-binary)
    data=${2#@}
    shift 2
    ;;
  -H | -X | -w) shift 2 ;;
  -sS) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
account=none
if [ -n "$config" ]; then
  token=$(sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$config" | head -n 1)
  printf '%s\n' "$token" >>"$FAKE_LOG_DIR/curl.tokens"
  chmod_check=$(stat -c '%a' "$config" 2>/dev/null || echo '?')
  printf '%s %s\n' "$chmod_check" "$config" >>"$FAKE_LOG_DIR/curl.configs"
  case "$token" in
  "$FAKE_SENTINEL_PREFIX"*) account=${token#"$FAKE_SENTINEL_PREFIX"} ;;
  esac
fi
printf 'curl %s %s\n' "$account" "$url" >>"$FAKE_LOG_DIR/curl.calls"
printf '%s\n' "$(cat "$data" 2>/dev/null || true)" >>"$FAKE_LOG_DIR/curl.bodies"
route=other
case "$url" in
*/v1/review/verify) route=verify ;;
*/v1/review/sign) route=sign ;;
esac
body='{"error":"the caller is refused this route"}'
code=403
case "$mode:$route:$account" in
ok:*:none) ;;
admit-no-token:verify:none) body='{"valid":false}'; code=200 ;;
admit-review:verify:"$FAKE_REVIEW_SA") body='{"valid":true}'; code=200 ;;
admit-coding-verify:verify:"$FAKE_CODING_SA") body='{"valid":false}'; code=200 ;;
admit-coding-sign:sign:"$FAKE_CODING_SA") body='{"valid":false}'; code=200 ;;
esac
if [ -n "$outfile" ]; then printf '%s\n' "$body" >"$outfile"; fi
printf '%s' "$code"
CURL_EOF
  # The mode is compared against the account inside the fake, so the two
  # variables the comparison needs have to reach it.
  chmod +x "$1/bin/curl"
}

run_check() { # run_check DIR [VAR=VAL...] [-- ARGS...]
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
  : >"$dir/logs/curl.tokens"
  : >"$dir/logs/curl.configs"
  : >"$dir/logs/curl.bodies"
  status=0
  # `-x` is bash's own flag rather than the check's, so it goes before the
  # script wherever the case put it: the check refuses an argument it does not
  # know, and the case that measures that refusal is the only way to trace a run
  # that is refusing to be traced.
  local bashargs=() kept=()
  local arg
  for arg in ${cli[@]+"${cli[@]}"}; do
    case "$arg" in
    -x | --xtrace) bashargs=(-x) ;;
    *) kept+=("$arg") ;;
    esac
  done
  cli=(${kept[@]+"${kept[@]}"})
  # The agent markers are unset here: this test IS a coding agent's session, and
  # the guard that refuses one is measured by the case that sets a marker back.
  # `env -u` on a name that is already absent is not an error.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-ok}" \
    FAKE_PROJECT="$project" FAKE_REGION="$region" FAKE_BUCKET="$bucket" \
    FAKE_SLUG="$slug" FAKE_RUN="$run_id" \
    FAKE_REVIEW_JOB="$review_job" FAKE_CODING_JOB="$coding_job" \
    FAKE_REVIEW_SA="$review_sa" FAKE_CODING_SA="$coding_sa" \
    FAKE_SENTINEL_PREFIX="$sentinel_prefix" \
    FAKE_SIGNATURE="$signature" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/sbx"
  printf 'version: 1\n' >"$1/sbx/fugaro.yaml"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_fake_curl "$1"
}

run_ok() { # run_ok DIR [VAR=VAL...] [-- EXTRA ARGS...]
  # The ordinary invocation: the sandbox checkout and the review run, plus
  # whatever the case adds.
  local dir=$1
  shift
  local vars=() extra=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    vars+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    extra=("$@")
  fi
  run_check "$dir" ${vars[@]+"${vars[@]}"} -- --sandbox "$dir/sbx" --run "$run_id" ${extra[@]+"${extra[@]}"}
}

signature=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

# assert_nothing_written DIR WHAT: the check creates nothing in the cloud, and
# the property is worth asserting on every case rather than only the happy one.
assert_nothing_written() {
  local calls
  calls=$(cat "$1/logs/gcloud.calls" 2>/dev/null || true)
  for verb in "storage cp" "storage rm" "storage mv" "iam service-accounts create" \
    "add-iam-policy-binding" "remove-iam-policy-binding" "run jobs execute" \
    "run jobs update" "run deploy" "secrets versions add" "projects delete"; do
    expect_absent "$calls" "$verb" "$2: the check ran '$verb', which creates or removes something"
  done
}

# assert_no_token_in_output WHAT: the token an impersonated mint answers with is
# the one thing that must never reach the check's own output or its block.
assert_no_token_in_output() {
  expect_absent "$out" "$sentinel_prefix" "$1: the block carries an ID token's value"
  expect_absent "$err" "$sentinel_prefix" "$1: the progress output carries an ID token's value"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Check 530 —" "--help: the check's own name"
  expect_contains "$out" "bash scripts/live-checks/check-c39.sh --sandbox" "--help: the command the owner types"
  expect_contains "$out" "steps 1, 2, 6 and 11 are measured here" "--help: which steps the script runs"
  expect_absent "$out" "FACT " "--help: no block and no verdict"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run jobs describe" "--help: no cloud call is made"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "--help: nothing is asked of the installation"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_ok "$dir" CHECK530_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 530 RESULT" "dry run: the block"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the tracing guard"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "CHECK 530 RESULT END (exit 0)" "dry run: the end line"
  # A dry run measured nothing about the review class, so no criterion row is in
  # its block: a FAIL row there would read as a verdict about a check nobody ran.
  expect_absent "$out" "FACT verdict-sig" "dry run: no criterion row"
  expect_absent "$out" "FACT job-no-secret" "dry run: no criterion row"
  expect_contains "$err" "dry run: nothing below is run" "dry run: the plan says so"
  expect_contains "$err" "gcloud storage ls gs://" "dry run: the plan names the commands"
  expect_contains "$err" "steps 3, 4, 5, 7, 8, 9 and 10 of the check" "dry run: the plan says which steps are yours"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud " "dry run: no gcloud call at all"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro " "dry run: no fugaro call either"
  expect_absent "$(cat "$dir/logs/curl.calls")" "curl " "dry run: no POST"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_ok "$dir"
  expect_status "$status" 0 "happy path"
  for key in fugaro-version sandbox project region bucket doctor-service review-jobs \
    job-no-secret variant-sa-accessor verdict-sig repo sign-line verify-unauthenticated \
    verify-review-refused; do
    expect_contains "$out" "FACT ${key}: PASS" "happy path: the ${key} row"
  done
  expect_contains "$out" "FACT cost: INFO" "happy path: the spend §8's step 11 reports"
  expect_contains "$out" "cost.model_usd=1.25" "happy path: what the run's record says it cost"
  expect_contains "$out" "CHECK 530 RESULT END (exit 0)" "happy path: the end line"
  # The steps that are a person's are printed and recorded as INFO, never faked.
  for n in 3 4 5 7 8 9 10; do
    expect_contains "$out" "FACT step-${n}: INFO" "happy path: step ${n} is the owner's"
  done
  expect_contains "$err" "OPEN ITEM 5 IS" "happy path: the open item the steps wait on is named"
  # The reads really were the ones §8 names.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "run jobs describe $review_job --project=$project --region=$region --format=json" "happy path: the review job's own describe"
  expect_contains "$calls" "projects get-iam-policy $project --format=json" "happy path: the account's IAM"
  expect_contains "$calls" "storage ls gs://$bucket/runs/*/$run_id/artifacts/review.json.sig" "happy path: the signature's path"
  expect_contains "$calls" "logging read" "happy path: the service's log"
  expect_contains "$calls" "jsonPayload.purpose=\"review_sign\"" "happy path: the signing's own line"
  expect_contains "$calls" "run services describe fugaro-credentials" "happy path: the service's URL"
  # Both refusal rows asked the route, and the review one did it with a token
  # that travelled through a mode-0600 config file rather than a command line.
  local curl_calls
  curl_calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$curl_calls" "curl none https://fugaro-credentials-chk530-uc.a.run.app/v1/review/verify" "happy path: the unauthenticated call"
  expect_contains "$curl_calls" "curl $review_sa https://fugaro-credentials-chk530-uc.a.run.app/v1/review/verify" "happy path: the review account's call"
  expect_contains "$(cat "$dir/logs/curl.configs")" "600 " "happy path: the config holding the token is mode 0600"
  expect_contains "$(cat "$dir/logs/curl.tokens")" "${sentinel_prefix}${review_sa}" "happy path: the token travelled in the config file"
  assert_no_token_in_output "happy path"
  assert_nothing_written "$dir" "happy path"
  assert_scratch_removed "happy path"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_ok "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard says why"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud " "bash -x: nothing is asked of the cloud"
}

test_an_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1 -- --sandbox "$dir/sbx" --run "$run_id"
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the guard names the marker"
  expect_contains "$out" "FACT aborted: FAIL" "a coding agent's session: the block says it stopped"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud " "a coding agent's session: nothing is read"
}

test_the_refused_installations_are_refused_by_name() {
  # `belong` is the owner's own installation and the two other names are the
  # ones check340.sh refuses: a name this check must never read is refused
  # before the installation is asked anything, so the stop is the guard's and
  # not gcloud's.
  local name
  for name in belong edge-devel-dimi edgeappinc; do
    local dir=$work/refused-$name
    prepare "$dir"
    run_check "$dir" -- --sandbox "$dir/sbx" --run "$run_id" --bucket "gs://fugaro-runs-$name"
    expect_status "$status" 1 "a bucket naming $name"
    expect_contains "$err" "must never touch" "a bucket naming $name: the refusal"
    expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a bucket naming $name: the installation is never read"
    expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud " "a bucket naming $name: nothing is asked of the cloud"
  done
}

test_an_installation_naming_belong_stops_the_check() {
  # The project comes from doctor, so this arm is the one the pre-tool guard
  # cannot see: a checkout whose installation IS the owner's own is a stop
  # before any of its jobs, its bucket or its service is read. The case's own
  # directory is named without the word, or the guard would stop it earlier and
  # measure nothing of what this case is about.
  local dir=$work/installation-from-doctor
  prepare "$dir"
  run_ok "$dir" FAKE_PROJECT=fugaro-chk530-belong
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "never run there" "an installation naming belong: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run jobs describe" "an installation naming belong: no job is read"
}

test_a_warning_service_stops_the_check() {
  local dir=$work/warning
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=service-warning
  expect_status "$status" 1 "a service doctor warns about"
  expect_contains "$out" "FACT doctor-service: FAIL" "a service doctor warns about: the row"
  expect_contains "$err" "the credentials service is not deployed" "a service doctor warns about: what doctor said"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run jobs describe" "a service doctor warns about: the rows below are not read"
}

test_a_doctor_without_the_census_row_is_not_measured() {
  local dir=$work/nocensus
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=no-census-row
  expect_status "$status" 2 "a doctor with no census row"
  expect_contains "$out" "FACT review-jobs: FAIL" "a doctor with no census row: the row"
  expect_contains "$err" "could NOT measure review-jobs" "a doctor with no census row: the exit names it"
}

test_a_census_naming_no_review_job_is_a_finding() {
  local dir=$work/nojob
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=no-review-job
  expect_status "$status" 1 "a census naming no review job"
  expect_contains "$out" "FACT review-jobs: FAIL" "a census naming no review job: the row"
  expect_contains "$out" "names no review job" "a census naming no review job: what it read"
}

test_a_review_job_that_mounts_a_secret_is_a_finding() {
  local dir=$work/mounts
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=job-mounts-a-secret
  expect_status "$status" 1 "a review job that mounts a secret"
  expect_contains "$out" "FACT job-no-secret: FAIL" "a review job that mounts a secret: the row"
  expect_contains "$out" "ANTHROPIC_API_KEY -> fugaro-anthropic-token" "a review job that mounts a secret: which secret"
  expect_contains "$out" "holds NO credential at all" "a review job that mounts a secret: ruling O1"
}

test_a_review_account_that_reads_secrets_is_a_finding() {
  local dir=$work/accessor
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=account-holds-an-accessor
  expect_status "$status" 1 "an account holding secretAccessor"
  expect_contains "$out" "FACT variant-sa-accessor: FAIL" "an account holding secretAccessor: the row"
  expect_contains "$out" "$review_sa holds roles/secretmanager.secretAccessor" "an account holding secretAccessor: what it holds"
}

test_a_missing_signature_is_not_measured() {
  local dir=$work/nosig
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=no-signature
  expect_status "$status" 1 "no signature under the run"
  expect_contains "$out" "FACT verdict-sig: FAIL" "no signature under the run: the row"
  expect_contains "$out" "runs/*/$run_id/artifacts/" "no signature under the run: the prefix it looked under"
  expect_contains "$err" "could NOT measure repo, cost" "no signature under the run: the note names the rows it could not measure"
  expect_absent "$(cat "$dir/logs/curl.calls")" "curl " "no signature under the run: no POST is made about a pair nobody has"
}

test_a_signature_that_is_not_hex_is_a_finding() {
  for bent in signature-not-hex signature-two-lines signature-short; do
    local dir=$work/sig-$bent
    prepare "$dir"
    run_ok "$dir" FAKE_MODE=$bent
    expect_status "$status" 1 "a signature that is $bent"
    expect_contains "$out" "FACT verdict-sig: FAIL" "a signature that is $bent: the row"
    expect_contains "$out" "not one line of lowercase hex" "a signature that is $bent: what it found"
  done
}

test_an_audit_line_that_is_wrong_is_a_finding() {
  local dir=$work/audit-extra
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=audit-carries-a-pull-request
  expect_status "$status" 1 "an audit line carrying a pull request"
  expect_contains "$out" "FACT sign-line: FAIL" "an audit line carrying a pull request: the row"
  expect_contains "$out" "carries fields this check does not know (pull_request)" "an audit line carrying a pull request: the field"

  dir=$work/audit-caller
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=audit-wrong-caller
  expect_status "$status" 1 "an audit line naming another caller"
  expect_contains "$out" "FACT sign-line: FAIL" "an audit line naming another caller: the row"
  expect_contains "$out" "names caller someone-else@example.com and the review job" "an audit line naming another caller: what it read"
}

test_no_audit_line_is_a_finding() {
  local dir=$work/noaudit
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=no-audit-line
  expect_status "$status" 1 "no audit line"
  expect_contains "$out" "FACT sign-line: FAIL" "no audit line: the row"
  expect_contains "$out" "wrote no review_sign audit line" "no audit line: what it found"
}

test_a_line_for_another_run_is_a_finding() {
  local dir=$work/otherrun
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=audit-names-another-run
  expect_status "$status" 1 "a line for another run"
  expect_contains "$out" "FACT sign-line: FAIL" "a line for another run: the row"
  expect_contains "$out" "none of them names run $run_id" "a line for another run: what it read"
  expect_contains "$out" "20261109-090000-0001" "a line for another run: the runs the log does name"
}

test_a_route_that_admits_a_call_with_no_token_is_a_finding() {
  local dir=$work/admit-no-token
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=admit-no-token
  expect_status "$status" 1 "a route that answers an unauthenticated call"
  expect_contains "$out" "FACT verify-unauthenticated: FAIL" "a route that answers an unauthenticated call: the row"
  expect_contains "$out" "carried NO token" "a route that answers an unauthenticated call: what it did"
}

test_a_route_that_admits_the_review_account_is_a_finding() {
  local dir=$work/admit-review
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=admit-review
  expect_status "$status" 1 "a route that admits the review class"
  expect_contains "$out" "FACT verify-review-refused: FAIL" "a route that admits the review class: the row"
  expect_contains "$out" "$review_sa" "a route that admits the review class: whose token it was"
}

test_the_coding_gate_is_measured_only_when_asked_for() {
  local dir=$work/gate
  prepare "$dir"
  run_ok "$dir" -- --coding-job "$coding_job"
  expect_status "$status" 0 "the coding gate"
  expect_contains "$out" "FACT gate-coding: PASS" "the coding gate: the row"
  expect_contains "$out" "refused the verify route" "the coding gate: both refusals are carried"
  expect_contains "$out" "and the sign route" "the coding gate: the sign route's own sentence"
  local curl_calls
  curl_calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$curl_calls" "curl $coding_sa https://fugaro-credentials-chk530-uc.a.run.app/v1/review/verify" "the coding gate: the verify call"
  expect_contains "$curl_calls" "curl $coding_sa https://fugaro-credentials-chk530-uc.a.run.app/v1/review/sign" "the coding gate: the sign call"
  assert_no_token_in_output "the coding gate"
}

test_a_coding_account_admitted_on_one_route_is_a_finding() {
  local dir=$work/gate-sign
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=admit-coding-sign -- --coding-job "$coding_job"
  expect_status "$status" 1 "a coding account admitted on the sign route"
  expect_contains "$out" "FACT gate-coding: FAIL" "a coding account admitted on the sign route: the row"
  expect_contains "$out" "refused on EVERY route" "a coding account admitted on the sign route: the gate"
}

test_an_unreadable_service_url_is_not_measured() {
  local dir=$work/nourl
  prepare "$dir"
  run_ok "$dir" FAKE_MODE=service-url-unreadable
  expect_status "$status" 2 "a service whose URL gcloud will not answer"
  expect_contains "$out" "FACT verify-unauthenticated: FAIL" "a service whose URL gcloud will not answer: the row"
  expect_contains "$err" "could NOT measure service-url, verify-unauthenticated, verify-review-refused" "a service whose URL gcloud will not answer: the note names them"
  expect_absent "$(cat "$dir/logs/curl.calls")" "curl " "a service whose URL gcloud will not answer: no call is made"
}

test_the_service_url_can_be_given() {
  local dir=$work/manual-url
  prepare "$dir"
  run_ok "$dir" -- --service-url https://review.example.test
  expect_status "$status" 0 "a URL given by hand"
  expect_contains "$out" "given with --service-url" "a URL given by hand: the row says where it came from"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run services describe" "a URL given by hand: the service is not described"
}

test_a_run_id_that_is_not_one_is_refused() {
  local dir=$work/badrun
  prepare "$dir"
  run_check "$dir" -- --sandbox "$dir/sbx" --run "the-review-run"
  expect_status "$status" 2 "a run name that is not an ID"
  expect_contains "$err" "has no run ID's form" "a run name that is not an ID: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud " "a run name that is not an ID: nothing is read"
}

test_a_sandbox_without_a_config_is_refused() {
  local dir=$work/noconfig
  prepare "$dir"
  rm -f "$dir/sbx/fugaro.yaml"
  run_check "$dir" -- --sandbox "$dir/sbx" --run "$run_id"
  expect_status "$status" 2 "a checkout with no fugaro.yaml"
  expect_contains "$err" "carries no fugaro.yaml" "a checkout with no fugaro.yaml: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a checkout with no fugaro.yaml: nothing is asked of it"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_ok "$dir" CHECK530_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK530_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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

# Sharding, so the Go test in scripts/live_checks_test.go can run these side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i (each case
# works in its own directory under $work), CHECK_TEST_ONLY=name runs that one.
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
t test_xtrace_is_refused
t test_an_agents_session_is_refused
t test_the_refused_installations_are_refused_by_name
t test_an_installation_naming_belong_stops_the_check
t test_a_warning_service_stops_the_check
t test_a_doctor_without_the_census_row_is_not_measured
t test_a_census_naming_no_review_job_is_a_finding
t test_a_review_job_that_mounts_a_secret_is_a_finding
t test_a_review_account_that_reads_secrets_is_a_finding
t test_a_missing_signature_is_not_measured
t test_a_signature_that_is_not_hex_is_a_finding
t test_an_audit_line_that_is_wrong_is_a_finding
t test_no_audit_line_is_a_finding
t test_a_line_for_another_run_is_a_finding
t test_a_route_that_admits_a_call_with_no_token_is_a_finding
t test_a_route_that_admits_the_review_account_is_a_finding
t test_the_coding_gate_is_measured_only_when_asked_for
t test_a_coding_account_admitted_on_one_route_is_a_finding
t test_an_unreadable_service_url_is_not_measured
t test_the_service_url_can_be_given
t test_a_run_id_that_is_not_one_is_refused
t test_a_sandbox_without_a_config_is_refused
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-c39_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-c39_test.sh: ok"
