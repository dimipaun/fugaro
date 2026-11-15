#!/usr/bin/env bash
# Tests for scripts/live-checks/check-lc4.sh, with a fake `gcloud` and a fake
# `fugaro` on PATH: NOTHING here reaches a cloud, a project, a secret or an
# installation, no agent runs a live command, and both fakes record every call
# they are given, so a case can assert what the check did and what it did not.
#
# The fakes are a small model of one installation: the fake `fugaro` answers
# `doctor --json` from the case's own directory, and the fake `gcloud` serves the
# job list, the job, each secret and each secret's policy out of the same
# directory, keyed by the ID the check asks about. The cases that matter are the
# ones where that model is bent: a doctor that is not green, a project that is
# not the one doctor names, no job, two jobs, no token mount, two token mounts, a
# token mounted out of another account's secret, a job that has lost the
# profile's own App-key mount, an App key mounted out of the wrong secret,
# another account's token mounted beside its own, a job account that cannot read
# its own secret, and one that can read another account's.
#
# Usage: bash scripts/live-checks/check-lc4_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-lc4.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK_LC4_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK_LC4_TEST_HEAD:-1200}")" ;;
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
expect_empty_file() { # file what
  [ -s "$1" ] && fail "$2: $1 should be empty; it holds: $(head -c 400 "$1")"
  return 0
}

project=proj-lc4-test
account=work
# The account's job and the two secrets it may mount, named the way the
# installation names them: the job's name and a secret's ID are hash-derived
# (gcp.ProjectJobName, gcp.ProjectSecretID) and this check never computes either,
# so the case's own names are what says the check read the label and not a name.
job=fugaro-standard-github-work-1a2b3c4d5e6f
sa=fugaro-standard-github-work@$project.iam.gserviceaccount.com
# The triple's other job: the review-only variant `fugaro update` deploys beside
# the coding one, wearing the same four labels and mounting no secret at all
# (internal/infra's reviewVariant), with a service account of its own.
variant_job=fugaro-standard-github-work-review-9b8a7c6d5e4f
variant_sa=fugaro-standard-github-work-review@$project.iam.gserviceaccount.com
token_secret=claude-oauth-token-work-4f2b9a1c
other_secret=claude-oauth-token-other-8e3d5f70
appkey_secret=github-app-key-7c1a2b3d

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
id=""
filter=""
for a in "$@"; do
  case "$a" in --filter=*) filter=${a#--filter=} ;; esac
done
case "${1:-}" in
run)
  case "${2:-}" in
  jobs)
    case "${3:-}" in
    list)
      case "$mode" in joblist-denied) echo "ERROR: (gcloud.run.jobs.list) Permission denied" >&2; exit 1 ;; esac
      # The label filter is applied, and not assumed: a check that filtered on
      # the wrong labels would otherwise read the same job whatever it asked for,
      # which is exactly the mistake this fake exists to catch.
      python3 -c '
import json, sys
want = sys.argv[1]
doc = json.load(sys.stdin)
def keeps(job):
    labels = (job.get("metadata") or {}).get("labels") or {}
    for term in want.split(" AND "):
        term = term.strip()
        if not term:
            continue
        key, _, value = term.partition("=")
        key = key.strip()
        if key.startswith("metadata.labels."):
            key = key[len("metadata.labels."):]
        if labels.get(key) != value:
            return False
    return True
print(json.dumps([j for j in doc if keeps(j)]))
' "$filter" <"$FAKE_JSON_DIR/jobs.json"
      exit 0
      ;;
    describe)
      case "$mode" in describe-denied) echo "ERROR: (gcloud.run.jobs.describe) Permission denied" >&2; exit 1 ;; esac
      cat "$FAKE_JSON_DIR/job.json"
      exit 0
      ;;
    esac
    ;;
  esac
  ;;
secrets)
  case "${2:-}" in
  list)
    case "$mode" in secrets-denied) echo "ERROR: (gcloud.secrets.list) Permission denied" >&2; exit 1 ;; esac
    cat "$FAKE_JSON_DIR/secrets.json"
    exit 0
    ;;
  describe)
    id=${3:-}
    case "$mode" in secret-denied) echo "ERROR: (gcloud.secrets.describe) Permission denied" >&2; exit 1 ;; esac
    if [ -f "$FAKE_JSON_DIR/secret-$id.json" ]; then
      cat "$FAKE_JSON_DIR/secret-$id.json"
      exit 0
    fi
    echo "ERROR: (gcloud.secrets.describe) NOT_FOUND: secret [$id] not found" >&2
    exit 1
    ;;
  get-iam-policy)
    id=${3:-}
    case "$mode" in policy-denied) echo "ERROR: (gcloud.secrets.get-iam-policy) Permission denied" >&2; exit 1 ;; esac
    if [ -f "$FAKE_JSON_DIR/policy-$id.json" ]; then
      cat "$FAKE_JSON_DIR/policy-$id.json"
      exit 0
    fi
    echo "ERROR: (gcloud.secrets.get-iam-policy) NOT_FOUND: secret [$id] not found" >&2
    exit 1
    ;;
  esac
  ;;
esac
echo "fake gcloud: unexpected call: $*" >&2
exit 3
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "$*" in
"doctor --json")
  case "$mode" in
  doctor-empty) exit 0 ;;
  doctor-nonjson)
    echo "this is not the document the check reads"
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/doctor.json"
  case "$mode" in doctor-notgreen) exit 1 ;; esac
  exit 0
  ;;
esac
echo "fake fugaro: unexpected call: $*" >&2
exit 3
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# write_default_case DIR [ACCOUNT] — one installation's own documents: a doctor
# that is green for $project in us-central1, the account's job wearing its
# labels and mounting its own token beside the profile's App key, the account's
# secret and another account's, and the two policies. The job list holds the
# account's coding job AND the review-only variant deployed beside it, which is
# what an installation that deploys the triple actually has: the two wear the
# same labels and only a secret tells them apart, so a list without the variant
# would let a check that cannot tell them apart pass. The job documents below
# are the v1 Job shape `gcloud run jobs list` and `describe --format=json` answer
# with, where the container and the job's account sit at
# spec.template.spec.template.spec — one level below a service's
# (check191_test.sh's own fixtures) — and a secret-backed entry names its secret
# the v1 way too, `valueFrom.secretKeyRef.name` (run/v1's EnvVarSource): a fake
# that nested the containers the way a service does, or spelled the entry the way
# the v2 API does, would agree with a check that read the wrong path and would
# pass while a real run stopped on a job it could not read.
write_default_case() { # dir [account]
  local dir=$1 acct=${2:-work} label secret variant_label
  case "$acct" in "" | default)
    label=none
    secret=claude-oauth-token
    # The variant of the no-account job is built over the policy's spelling of
    # the default account (config.ReviewVariantAccount), so it wears a label the
    # no-account coding job does not: it is the account's own job the labels
    # name here, and the check finds one job rather than two.
    variant_label=default
    ;;
  *)
    label=$acct
    secret=claude-oauth-token-$acct
    variant_label=$acct
    ;;
  esac
  mkdir -p "$dir/json"
  cat >"$dir/json/doctor.json" <<JSON
{"ok":true,"checks":[{"id":"installation","ok":true},{"id":"bucket","ok":true}],
 "project":{"name":"sandbox","gcp_project":"$project","region":"us-central1"}}
JSON
  cat >"$dir/json/jobs.json" <<JSON
[{"metadata":{"name":"$job","labels":{"fugaro":"managed","fugaro_profile":"standard",
  "fugaro_provider":"github","fugaro_account":"$label"}},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"$sa",
    "containers":[{"env":[
      {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}},
      {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueFrom":{"secretKeyRef":{"name":"$appkey_secret","key":"latest"}}}
    ]}]}}}}}},
 {"metadata":{"name":"$variant_job","labels":{"fugaro":"managed","fugaro_profile":"standard",
  "fugaro_provider":"github","fugaro_account":"$variant_label"}},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"$variant_sa",
    "containers":[{"env":[
      {"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credentials.example.invalid"}
    ]}]}}}}}}]
JSON
  cat >"$dir/json/job.json" <<JSON
{"metadata":{"name":"$job"},
 "spec":{"template":{"spec":{"template":{"spec":{
   "serviceAccountName":"$sa",
   "containers":[{"env":[
     {"name":"FUGARO_PROJECT","value":"sandbox"},
     {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}},
     {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueFrom":{"secretKeyRef":{"name":"$appkey_secret","key":"latest"}}}
   ]}]}}}}}}
JSON
  cat >"$dir/json/secrets.json" <<JSON
[{"name":"projects/$project/secrets/$token_secret","labels":{"fugaro_secret":"$secret"}},
 {"name":"projects/$project/secrets/$appkey_secret","labels":{"fugaro_secret":"github-app-key"}},
 {"name":"projects/$project/secrets/$other_secret","labels":{"fugaro_secret":"claude-oauth-token-other"}}]
JSON
  cat >"$dir/json/secret-$token_secret.json" <<JSON
{"name":"projects/$project/secrets/$token_secret","labels":{"fugaro_secret":"$secret","fugaro_project":"sandbox"}}
JSON
  cat >"$dir/json/secret-$appkey_secret.json" <<JSON
{"name":"projects/$project/secrets/$appkey_secret","labels":{"fugaro_secret":"github-app-key"}}
JSON
  cat >"$dir/json/secret-$other_secret.json" <<JSON
{"name":"projects/$project/secrets/$other_secret","labels":{"fugaro_secret":"claude-oauth-token-other"}}
JSON
  cat >"$dir/json/policy-$token_secret.json" <<JSON
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:$sa"]}]}
JSON
  cat >"$dir/json/policy-$other_secret.json" <<JSON
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:other-job@$project.iam.gserviceaccount.com"]}]}
JSON
}

prepare() { # dir [account]
  local dir=$1
  mkdir -p "$dir/home" "$dir/state" "$dir/logs" "$dir/tmp"
  write_fake_gcloud "$dir"
  write_fake_fugaro "$dir"
  write_default_case "$dir" "${2:-work}"
}

run_check() { # run_check DIR [ENV...] [-- ARGS...]
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
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses a
  # shell that arrived tracing, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" FAKE_JSON_DIR="$dir/json" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  calls=$(cat "$dir/logs/gcloud.calls")
  fcalls=$(cat "$dir/logs/fugaro.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# run_default DIR — the ordinary run: the project, the account and the profile
# this case's documents describe.
run_default() { # dir [extra args...]
  local dir=$1
  shift
  run_check "$dir" -- --project "$project" --account "$account" "$@"
}

assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

# --------------------------------------------------------------------- cases
test_help() {
  local dir
  dir=$(mktemp -d "$work/help.XXXX")
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check-lc4.sh --project" "--help prints the usage"
  expect_absent "$out" "CHECK LC4 RESULT" "--help is not a run and prints no block"
  expect_empty_file "$dir/logs/gcloud.calls" "--help makes no gcloud call"
  expect_empty_file "$dir/logs/fugaro.calls" "--help makes no fugaro call"
}

test_unknown_argument_is_refused() {
  local dir
  dir=$(mktemp -d "$work/unknown.XXXX")
  prepare "$dir"
  run_check "$dir" -- --project "$project" --account "$account" --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "the refusal names the argument"
  expect_contains "$out" "FACT aborted: FAIL" "the block records the abort"
  expect_empty_file "$dir/logs/gcloud.calls" "a refused argument makes no gcloud call"
}

test_the_two_required_arguments() {
  local dir
  dir=$(mktemp -d "$work/required.XXXX")
  prepare "$dir"
  run_check "$dir" -- --account "$account"
  expect_status "$status" 2 "no --project"
  expect_contains "$err" "--project is required" "the refusal says which one is missing"
  run_check "$dir" -- --project "$project"
  expect_status "$status" 2 "no --account"
  expect_contains "$err" "--account is required" "the refusal says which one is missing"
}

test_belong_is_refused_by_name() {
  local dir name
  dir=$(mktemp -d "$work/belong.XXXX")
  prepare "$dir"
  for name in belong belong-prod edge-devel-dimi edgeappinc; do
    run_check "$dir" -- --project "$name" --account "$account"
    expect_status "$status" 2 "the project $name"
    expect_contains "$out" "FACT belong: FAIL" "$name is refused by the name guard"
    expect_empty_file "$dir/logs/gcloud.calls" "$name is refused before any gcloud call"
    expect_empty_file "$dir/logs/fugaro.calls" "$name is refused before any fugaro call"
  done
  run_check "$dir" -- --project "$project" --account belong
  expect_status "$status" 2 "the account belong"
  expect_contains "$out" "FACT belong: FAIL" "an account named belong is refused too"
  assert_scratch_removed "the belong refusal"
}

test_xtrace_is_refused() {
  local dir
  dir=$(mktemp -d "$work/xtrace.XXXX")
  prepare "$dir"
  run_check "$dir" -- --xtrace --project "$project" --account "$account"
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "the block records the traced shell"
  expect_contains "$err" "this shell is tracing" "the refusal says what to do"
  expect_empty_file "$dir/logs/gcloud.calls" "a traced shell makes no gcloud call"
}

test_dry_run_makes_no_call() {
  local dir
  dir=$(mktemp -d "$work/dryrun.XXXX")
  prepare "$dir"
  run_default "$dir" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_empty_file "$dir/logs/gcloud.calls" "a dry run makes no gcloud call"
  expect_empty_file "$dir/logs/fugaro.calls" "a dry run makes no fugaro call"
  expect_contains "$out" "FACT dry-run: PASS" "the block says it was a dry run"
  expect_contains "$out" "CHECK LC4 RESULT END (exit 0)" "the dry run ends its own block"
  # The dry run prints the shape of every command it would make, so the owner can
  # read what a real run would do without one being made.
  expect_contains "$err" "gcloud run jobs list --project=$project" "the plan names the job list"
  expect_contains "$err" "gcloud secrets get-iam-policy" "the plan names the policy reads"
  expect_contains "$err" "fugaro doctor --json" "the plan names the doctor read"
  expect_contains "$err" "this dry run made no gcloud call and no fugaro call at all" "the plan says nothing ran"
  # No criterion row is answered by a dry run: the block it prints carries the
  # guards and the arguments alone.
  expect_absent "$out" "FACT job:" "a dry run answers no row about the job"
  expect_absent "$out" "FACT token-mount:" "a dry run answers no row about the mount"
  expect_absent "$out" "FACT sa-accessor:" "a dry run answers no row about the readers"
  expect_contains "$out" "FACT project: PASS" "a dry run settles the arguments it was given"
  assert_scratch_removed "the dry run"
}

test_happy_path() {
  local dir
  dir=$(mktemp -d "$work/happy.XXXX")
  prepare "$dir"
  run_default "$dir"
  expect_status "$status" 0 "the installation this case describes"
  expect_contains "$out" "CHECK LC4 RESULT END (exit 0)" "the block ends with the verdict"
  local key
  for key in xtrace belong project region profile account doctor job token-mount \
    token-secret profile-mounts other-mounts sa-accessor sa-other-account restore; do
    expect_contains "$out" "FACT $key: PASS" "$key passes"
  done
  expect_absent "$out" "not reached" "no row was left unread"
  expect_contains "$out" "FACT job: PASS — $job" "the job is named as it was found"
  expect_contains "$out" "read beside $variant_job" "the row names the review-only variant the triple deploys beside the account's job"
  expect_contains "$out" "runs as $sa" "the job's own account is named"
  expect_contains "$out" "exactly one secret as CLAUDE_CODE_OAUTH_TOKEN: $token_secret" "the mount is the account's own secret"
  expect_contains "$out" "fugaro_secret=claude-oauth-token-work" "the secret's label is the account's derived name"
  expect_contains "$out" "labelled fugaro_secret=github-app-key" "the profile's own mount is read too"
  # The calls: the job list is filtered by the account's labels, the job is
  # described, and the two policies are read — the account's own, and the other
  # account's, which is where the isolation is read.
  expect_contains "$calls" "run jobs list" "the job list is read"
  expect_contains "$calls" "--filter=metadata.labels.fugaro=managed AND metadata.labels.fugaro_profile=standard AND metadata.labels.fugaro_account=work" "the filter is the account axis"
  expect_contains "$calls" "run jobs describe $job" "the job is described"
  expect_absent "$calls" "run jobs describe $variant_job" "the variant is never described: this check reads the job that mounts the token"
  expect_contains "$calls" "secrets get-iam-policy $token_secret" "the account's own secret policy is read"
  expect_contains "$calls" "secrets get-iam-policy $other_secret" "the other account's secret policy is read"
  expect_absent "$calls" "secrets get-iam-policy $appkey_secret" "the App key's policy is not read: this check is about token readers"
  # No value of any secret is asked for anywhere.
  expect_absent "$calls" "versions access" "no secret value is read"
  expect_absent "$calls" "secrets access" "no secret value is read"
  expect_absent "$out" "secretData" "no secret value reaches the block"
  assert_scratch_removed "the happy path"
}

test_the_region_defaults_to_the_one_doctor_records() {
  local dir
  dir=$(mktemp -d "$work/region.XXXX")
  prepare "$dir"
  run_default "$dir"
  expect_contains "$out" "FACT region: PASS — us-central1" "the region comes from doctor"
  expect_contains "$calls" "--region=us-central1" "and it is the region the job is looked up in"
  # Typed, and agreeing, is the same reading with the owner's own word for it.
  run_default "$dir" --region us-central1
  expect_status "$status" 0 "a region that matches doctor's"
  expect_contains "$out" "FACT region: PASS — us-central1 (typed by the owner" "the row says it was typed"
}

test_a_region_that_is_not_the_installations_stops() {
  local dir
  dir=$(mktemp -d "$work/regionbad.XXXX")
  prepare "$dir"
  run_default "$dir" --region europe-west1
  expect_status "$status" 2 "a region doctor does not record"
  expect_contains "$err" "is not the installation's region" "the stop says so"
  expect_contains "$out" "FACT aborted: FAIL" "the block records the abort"
  expect_empty_file "$dir/logs/gcloud.calls" "no job is looked up in the wrong region"
}

test_a_project_that_is_not_the_installations_stops() {
  local dir
  dir=$(mktemp -d "$work/projbad.XXXX")
  prepare "$dir"
  run_check "$dir" -- --project other-proj --account "$account"
  expect_status "$status" 2 "a project doctor does not name"
  expect_contains "$err" "--project other-proj is not the project this installation is on" "the stop names both projects"
  expect_contains "$out" "FACT aborted: FAIL" "the block records the abort"
  expect_empty_file "$dir/logs/gcloud.calls" "no job is looked up in another installation's project"
  assert_scratch_removed "the project refusal"
}

test_a_doctor_that_is_not_green_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/doctor.XXXX")
  prepare "$dir"
  cat >"$dir/json/doctor.json" <<JSON
{"ok":false,"checks":[{"id":"installation","ok":true},
 {"id":"secret-readers","ok":false,"severity":"error","problem":"two jobs may read claude-oauth-token-work"}],
 "project":{"name":"sandbox","gcp_project":"$project","region":"us-central1"}}
JSON
  FAKE_MODE=doctor-notgreen run_default "$dir"
  expect_status "$status" 1 "a doctor that is not green"
  expect_contains "$out" "FACT doctor: FAIL" "the doctor row fails"
  expect_contains "$out" "secret-readers (error)" "the row names the checks that did not pass"
  expect_contains "$out" "FACT job: PASS" "the rest of the check is still read"
  assert_scratch_removed "a red doctor"
}

test_a_doctor_that_answers_nothing_stops() {
  local dir
  dir=$(mktemp -d "$work/doctorempty.XXXX")
  prepare "$dir"
  FAKE_MODE=doctor-empty run_default "$dir"
  expect_status "$status" 2 "doctor with no output"
  expect_contains "$err" "printed nothing" "the stop says doctor printed nothing"
  expect_empty_file "$dir/logs/gcloud.calls" "nothing is read off a doctor that did not answer"
}

test_a_doctor_that_is_not_json_stops() {
  local dir
  dir=$(mktemp -d "$work/doctornonjson.XXXX")
  prepare "$dir"
  FAKE_MODE=doctor-nonjson run_default "$dir"
  expect_status "$status" 2 "doctor that is not JSON"
  expect_contains "$err" "is not the document this check reads" "the stop says so"
}

test_a_job_that_is_not_there_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/nojob.XXXX")
  prepare "$dir"
  echo '[]' >"$dir/json/jobs.json"
  run_default "$dir"
  expect_status "$status" 1 "an account with no job"
  expect_contains "$out" "FACT job: FAIL" "the job row fails"
  expect_contains "$out" "no job in $project/us-central1 wears fugaro_profile=standard and fugaro_account=work" "the row says what was not found"
  expect_contains "$out" "FACT token-mount: FAIL — not reached" "the mount row was not read"
  expect_absent "$calls" "run jobs describe" "no job is described when there is none"
  assert_scratch_removed "no job"
}

test_two_jobs_for_one_account_stop() {
  local dir
  dir=$(mktemp -d "$work/twojobs.XXXX")
  prepare "$dir"
  # Two jobs of the same triple that each mount a secret: neither is the
  # review-only variant, which mounts none, so this is a real leftover of an
  # earlier release rather than the pair the deployment builds.
  cat >"$dir/json/jobs.json" <<JSON
[{"metadata":{"name":"$job","labels":{"fugaro":"managed","fugaro_profile":"standard","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}}]}]}}}}}},
 {"metadata":{"name":"fugaro-standard-github-work-999999999999","labels":{"fugaro":"managed","fugaro_profile":"standard","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}}]}]}}}}}}]
JSON
  run_default "$dir"
  expect_status "$status" 2 "two jobs wearing one account's labels"
  expect_contains "$err" "has 2 jobs" "the stop says how many"
  expect_contains "$err" "that each mount a secret" "the stop says why neither of them is the variant"
  expect_contains "$err" "fugaro-standard-github-work-999999999999" "the stop names them"
  expect_absent "$calls" "run jobs describe" "no job is described when there are two"
}

test_the_review_variant_beside_the_job_is_not_a_second_job() {
  local dir
  dir=$(mktemp -d "$work/variant.XXXX")
  prepare "$dir"
  run_default "$dir"
  # The default case's list holds both: the check reads the one that mounts the
  # account's token and leaves the mountless variant alone. Without this the
  # check stops on every installation that deploys the pair, which is every
  # installation past the release that added them.
  expect_status "$status" 0 "the coding job beside its review-only variant"
  expect_contains "$out" "FACT job: PASS — $job" "the job read is the one wearing the account's labels AND mounting a secret"
  expect_absent "$calls" "run jobs describe $variant_job" "the variant is left unread"
  expect_contains "$calls" "run jobs describe $job" "the coding job is the one described"
}

test_two_mountless_jobs_beside_the_job_stop() {
  local dir
  dir=$(mktemp -d "$work/twovariants.XXXX")
  prepare "$dir"
  # The triple deploys one variant, not two: a second mountless job wearing the
  # same labels is a leftover, and this check will not guess which one the plan
  # is.
  cat >"$dir/json/jobs.json" <<JSON
[{"metadata":{"name":"$job","labels":{"fugaro":"managed","fugaro_profile":"standard","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}}]}]}}}}}},
 {"metadata":{"name":"$variant_job","labels":{"fugaro":"managed","fugaro_profile":"standard","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"env":[]}]}}}}}},
 {"metadata":{"name":"fugaro-standard-github-work-review-111111111111","labels":{"fugaro":"managed","fugaro_profile":"standard","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"env":[]}]}}}}}}]
JSON
  run_default "$dir"
  expect_status "$status" 2 "two mountless jobs beside the account's own"
  expect_contains "$err" "2 of which mount no secret at all" "the stop says how many are mountless"
  expect_contains "$err" "fugaro-standard-github-work-review-111111111111" "the stop names the leftover"
  expect_absent "$calls" "run jobs describe" "no job is described when the listing cannot be resolved"
}

test_a_lone_job_that_mounts_nothing_is_still_read() {
  local dir
  dir=$(mktemp -d "$work/mountless.XXXX")
  prepare "$dir"
  # One job wearing the labels, mounting nothing: there is nothing to tell it
  # apart from, so it is the account's and the rows below report what it lost.
  cat >"$dir/json/jobs.json" <<JSON
[{"metadata":{"name":"$job","labels":{"fugaro":"managed","fugaro_profile":"standard","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"env":[]}]}}}}}}]
JSON
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "a lone job that mounts nothing"
  expect_contains "$out" "FACT job: PASS — $job" "the lone job is read as the account's"
  expect_contains "$out" "FACT token-mount: FAIL" "and the mount it lost is the finding"
}

test_the_other_entry_spelling_gcloud_has_printed_is_read_too() {
  local dir
  dir=$(mktemp -d "$work/v2entry.XXXX")
  prepare "$dir"
  # A mounted secret is the one field this check reads in two spellings: the v1
  # Job shape the other fixtures are in names it valueFrom.secretKeyRef.name, and
  # a gcloud that answered the v2 API names it valueSource.secretKeyRef.secret
  # (run/v2's GoogleCloudRunV2EnvVar). A reader that knew one spelling would read
  # a job mounting its token as a job mounting nothing — which is exactly how the
  # review-only variant beside it looks — so both the list and the describe
  # documents are handed over in the v2 one here.
  cat >"$dir/json/jobs.json" <<JSON
[{"metadata":{"name":"$job","labels":{"fugaro":"managed","fugaro_profile":"standard",
  "fugaro_provider":"github","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"$sa",
    "containers":[{"env":[
      {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueSource":{"secretKeyRef":{"secret":"$token_secret","version":"latest"}}},
      {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueSource":{"secretKeyRef":{"secret":"$appkey_secret","version":"latest"}}}
    ]}]}}}}}},
 {"metadata":{"name":"$variant_job","labels":{"fugaro":"managed","fugaro_profile":"standard",
  "fugaro_provider":"github","fugaro_account":"work"}},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"$variant_sa",
    "containers":[{"env":[
      {"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://credentials.example.invalid"}
    ]}]}}}}}}]
JSON
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueSource":{"secretKeyRef":{"secret":"$token_secret","version":"latest"}}},
    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueSource":{"secretKeyRef":{"secret":"$appkey_secret","version":"latest"}}}
  ]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 0 "documents in the v2 entry spelling"
  expect_contains "$out" "FACT job: PASS — $job" "the listing is resolved to the job that mounts a secret"
  expect_contains "$out" "FACT token-mount: PASS" "and its token mount is read"
  expect_contains "$out" "exactly one secret as CLAUDE_CODE_OAUTH_TOKEN: $token_secret" "as the account's own secret"
  expect_contains "$out" "read beside $variant_job" "the mountless variant is still named beside it"
}

test_a_job_list_that_is_denied_stops() {
  local dir
  dir=$(mktemp -d "$work/joblistdenied.XXXX")
  prepare "$dir"
  FAKE_MODE=joblist-denied run_default "$dir"
  expect_status "$status" 2 "a refused job list"
  expect_contains "$err" "gcloud run jobs list did not answer" "the stop says which read failed"
  expect_contains "$out" "FACT aborted: FAIL" "the block records the abort"
  assert_scratch_removed "a denied job list"
}

test_no_token_mount_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/notoken.XXXX")
  prepare "$dir"
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[{"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueFrom":{"secretKeyRef":{"name":"$appkey_secret","key":"latest"}}}]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "a job with no account-token mount"
  expect_contains "$out" "FACT token-mount: FAIL" "the mount row fails"
  expect_contains "$out" "mounts no secret as CLAUDE_CODE_OAUTH_TOKEN" "the row says what is missing"
  expect_contains "$out" "FACT token-secret: FAIL — not reached" "there is no secret to hold anything to"
  expect_contains "$out" "FACT sa-accessor: FAIL — not reached" "and no policy to read"
  expect_absent "$calls" "secrets get-iam-policy" "no policy is read without a token secret"
}

test_two_token_mounts_are_a_finding() {
  local dir
  dir=$(mktemp -d "$work/twotokens.XXXX")
  prepare "$dir"
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}},
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$other_secret","key":"latest"}}}
  ]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "two token mounts"
  expect_contains "$out" "FACT token-mount: FAIL" "the mount row fails"
  expect_contains "$out" "mounts 2 secrets as CLAUDE_CODE_OAUTH_TOKEN" "the row counts them"
}

test_a_token_mounted_out_of_another_accounts_secret_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/wrongtoken.XXXX")
  prepare "$dir"
  # The right variable, the right shape, and the wrong secret under it: the
  # account's own secret is not what this job mounts.
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$other_secret","key":"latest"}}},
    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueFrom":{"secretKeyRef":{"name":"$appkey_secret","key":"latest"}}}
  ]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "a token mounted out of another account's secret"
  expect_contains "$out" "FACT token-mount: PASS" "the mount itself is there"
  expect_contains "$out" "FACT token-secret: FAIL" "the secret under it fails"
  expect_contains "$out" "is labelled fugaro_secret=claude-oauth-token-other" "the row names the label it found"
  expect_contains "$out" "the account work's derived secret is claude-oauth-token-work" "and the one it wanted"
}

test_a_job_that_lost_the_profile_mount_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/noappkey.XXXX")
  prepare "$dir"
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}}
  ]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "a job with no App-key mount"
  expect_contains "$out" "FACT profile-mounts: FAIL" "the profile's own mount fails"
  expect_contains "$out" "mounts no secret as FUGARO_GITHUB_APP_PRIVATE_KEY" "the row says what is missing"
}

test_an_app_key_mounted_out_of_the_wrong_secret_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/wrongappkey.XXXX")
  prepare "$dir"
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}},
    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueFrom":{"secretKeyRef":{"name":"$other_secret","key":"latest"}}}
  ]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "an App key out of another secret"
  expect_contains "$out" "FACT profile-mounts: FAIL" "the profile's own mount fails"
  expect_contains "$out" "whose fugaro_secret label is claude-oauth-token-other and not github-app-key" "the row names both labels"
}

test_another_accounts_token_mounted_beside_its_own_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/othermount.XXXX")
  prepare "$dir"
  cat >"$dir/json/job.json" <<JSON
{"spec":{"template":{"spec":{"template":{"spec":{
  "serviceAccountName":"$sa",
  "containers":[{"env":[
    {"name":"CLAUDE_CODE_OAUTH_TOKEN","valueFrom":{"secretKeyRef":{"name":"$token_secret","key":"latest"}}},
    {"name":"FUGARO_GITHUB_APP_PRIVATE_KEY","valueFrom":{"secretKeyRef":{"name":"$appkey_secret","key":"latest"}}},
    {"name":"OTHER_TOKEN","valueFrom":{"secretKeyRef":{"name":"$other_secret","key":"latest"}}}
  ]}]}}}}}}
JSON
  run_default "$dir"
  expect_status "$status" 1 "another account's token beside its own"
  expect_contains "$out" "FACT other-mounts: FAIL" "the other-mounts row fails"
  expect_contains "$out" "labelled fugaro_secret=claude-oauth-token-other — another account's token secret" "the row says which"
  # The rows under it are still read: the finding is about what the job mounts,
  # not about whether it can read its own token.
  expect_contains "$out" "FACT sa-accessor: PASS" "the readers are still read"
}

test_a_job_account_that_cannot_read_its_own_secret_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/noaccessor.XXXX")
  prepare "$dir"
  cat >"$dir/json/policy-$token_secret.json" <<JSON
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:another-job@$project.iam.gserviceaccount.com"]}]}
JSON
  run_default "$dir"
  expect_status "$status" 1 "a job account with no accessor"
  expect_contains "$out" "FACT sa-accessor: FAIL" "the reader row fails"
  expect_contains "$out" "does NOT hold roles/secretmanager.secretAccessor on $token_secret" "the row says which secret"
  expect_contains "$out" "FACT sa-other-account: PASS" "the other half is still read"
}

test_a_job_account_that_can_read_another_accounts_secret_is_a_finding() {
  local dir
  dir=$(mktemp -d "$work/otheraccessor.XXXX")
  prepare "$dir"
  cat >"$dir/json/policy-$other_secret.json" <<JSON
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:$sa","serviceAccount:other-job@$project.iam.gserviceaccount.com"]}]}
JSON
  run_default "$dir"
  expect_status "$status" 1 "a job account that may read another account's token"
  expect_contains "$out" "FACT sa-accessor: PASS" "its own secret is still read"
  expect_contains "$out" "FACT sa-other-account: FAIL" "the isolation row fails"
  expect_contains "$out" "holds roles/secretmanager.secretAccessor on another account's token secret: $other_secret" "the row names the secret it may read"
}

test_the_default_account_is_read_by_its_own_names() {
  local dir
  dir=$(mktemp -d "$work/defaultaccount.XXXX")
  prepare "$dir" default
  # The default account's job wears `none` as its label (a GCP label cannot be
  # empty, deploy/terraform/gcp/modules/project/job.tf) and its secret is
  # claude-oauth-token with no suffix (config.ClaudeAccountSecret).
  run_check "$dir" -- --project "$project" --account default
  expect_status "$status" 0 "the default account"
  expect_contains "$out" "FACT account: PASS — default" "the row names the account"
  expect_contains "$out" "wears fugaro_account=none" "the label is the one a label can carry"
  expect_contains "$out" "its derived secret is claude-oauth-token," "the derived secret has no suffix"
  expect_contains "$calls" "metadata.labels.fugaro_account=none" "the filter is the default account's label"
  expect_contains "$out" "FACT token-secret: PASS" "and the secret it mounts is read the same way"
}

test_a_secrets_list_that_is_denied_stops() {
  local dir
  dir=$(mktemp -d "$work/secretsdenied.XXXX")
  prepare "$dir"
  FAKE_MODE=secrets-denied run_default "$dir"
  expect_status "$status" 2 "a refused secret list"
  expect_contains "$err" "gcloud secrets list did not answer" "the stop says which read failed"
  expect_contains "$out" "FACT sa-accessor: PASS" "what was read before it stands"
  expect_contains "$out" "FACT sa-other-account: FAIL — not reached" "and the row that needed it was not read"
}

test_a_secret_describe_that_is_denied_stops() {
  local dir
  dir=$(mktemp -d "$work/secretdenied.XXXX")
  prepare "$dir"
  FAKE_MODE=secret-denied run_default "$dir"
  expect_status "$status" 2 "a refused secret describe"
  expect_contains "$err" "could not be described" "the stop says so"
  expect_contains "$out" "FACT token-mount: PASS" "the mount itself was read"
  expect_contains "$out" "FACT token-secret: FAIL — not reached" "the label under it was not"
}

test_a_policy_read_that_is_denied_stops() {
  local dir
  dir=$(mktemp -d "$work/policydenied.XXXX")
  prepare "$dir"
  FAKE_MODE=policy-denied run_default "$dir"
  expect_status "$status" 2 "a refused policy read"
  expect_contains "$err" "gcloud secrets get-iam-policy did not answer" "the stop says which read failed"
  expect_contains "$out" "FACT sa-accessor: FAIL — not reached" "no row is answered off a policy that was not read"
}

test_the_profile_can_be_named() {
  local dir
  dir=$(mktemp -d "$work/profile.XXXX")
  prepare "$dir"
  run_default "$dir" --profile standard
  expect_status "$status" 0 "a profile that matches"
  expect_contains "$out" "FACT profile: PASS — standard" "the row names it"
  # A profile no job wears is the same reading as no job: the account's job is
  # found by the labels, and this one wears another profile's.
  run_default "$dir" --profile review
  expect_status "$status" 1 "a profile no job wears"
  expect_contains "$out" "FACT job: FAIL" "the job row fails"
  expect_contains "$calls" "metadata.labels.fugaro_profile=review" "the filter is the profile that was named"
}

# ------------------------------------------------------------------- the run
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
t test_unknown_argument_is_refused
t test_the_two_required_arguments
t test_belong_is_refused_by_name
t test_xtrace_is_refused
t test_dry_run_makes_no_call
t test_happy_path
t test_the_region_defaults_to_the_one_doctor_records
t test_a_region_that_is_not_the_installations_stops
t test_a_project_that_is_not_the_installations_stops
t test_a_doctor_that_is_not_green_is_a_finding
t test_a_doctor_that_answers_nothing_stops
t test_a_doctor_that_is_not_json_stops
t test_a_job_that_is_not_there_is_a_finding
t test_two_jobs_for_one_account_stop
t test_the_review_variant_beside_the_job_is_not_a_second_job
t test_two_mountless_jobs_beside_the_job_stop
t test_a_lone_job_that_mounts_nothing_is_still_read
t test_the_other_entry_spelling_gcloud_has_printed_is_read_too
t test_a_job_list_that_is_denied_stops
t test_no_token_mount_is_a_finding
t test_two_token_mounts_are_a_finding
t test_a_token_mounted_out_of_another_accounts_secret_is_a_finding
t test_a_job_that_lost_the_profile_mount_is_a_finding
t test_an_app_key_mounted_out_of_the_wrong_secret_is_a_finding
t test_another_accounts_token_mounted_beside_its_own_is_a_finding
t test_a_job_account_that_cannot_read_its_own_secret_is_a_finding
t test_a_job_account_that_can_read_another_accounts_secret_is_a_finding
t test_the_default_account_is_read_by_its_own_names
t test_a_secrets_list_that_is_denied_stops
t test_a_secret_describe_that_is_denied_stops
t test_a_policy_read_that_is_denied_stops
t test_the_profile_can_be_named

if [ "$failures" -ne 0 ]; then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "all cases passed"
