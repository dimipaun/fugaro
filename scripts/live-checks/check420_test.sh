#!/usr/bin/env bash
# Tests for scripts/live-checks/check420.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a pool or a run, no agent runs a
# live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one installation: the fake `fugaro` answers the
# calls the check makes (version, the four `--help` probes that are the feature
# gate, doctor, `craft ls`, `project policy show`, `creds ls`, the launch and the
# runs list), and the fake `gcloud` serves the run record. The cases that matter
# are the ones where that model is bent, because each of them is a state the
# check exists to tell apart from a failover: a build without the crafts
# vocabulary, a coder craft that resolves to no pool, a pool of one member, a
# round-robin pool, a pool whose member the credentials service cannot serve, a
# subscription member with the project's spread switch off, a member the bundle
# does not hold, a run that never ends, a record that is another run's, a run
# that failed, a run with no switch at all, a switch onto something that is not
# the pool's second member, and a record that names no member of the pool.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check420_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check420.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK420_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK420_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK420_TEST_TIMEOUT:-60}

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

project=fugaro-chk420-test
run_id=run-20261114120000-1
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
# The pool the coder craft's access names, its two members in the order the node
# lists them, and the models the crafts run: the first member is the one whose cap
# at the provider is exhausted, so the second is what a failover serves the run
# with.
pool=pool-burst
first_member=claude-oauth-token-burst
second_member=anthropic-api-key
coder_model=claude-opus-4-1

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check420.sh's test only. It
# answers the account read the check makes and serves the run record. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk420-owner@belong.example\n' ;;
    *) printf 'chk420-owner@example.com\n' ;;
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
    record-unreadable)
      printf 'not json at all\n'
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
# A stand-in for `fugaro`, for scripts/live-checks/check420.sh's test only. The
# `--help` probes are the check's feature gate, the `--json` reads come from files
# the case wrote, and every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

usage() { printf 'Usage:\n  fugaro %s\n' "$1" ; }

case "${1:-}" in
version)
  printf '0.24.0\n'
  ;;
craft)
  if [ "${2:-}" != "ls" ]; then
    echo "fugaro: unexpected craft subcommand ${2:-}" >&2
    exit 1
  fi
  case "${3:-}" in
  --help)
    case "$mode" in
    no-feature) echo "fugaro: unknown command \"craft\" for \"fugaro\"" >&2; exit 1 ;;
    esac
    usage "craft ls"
    ;;
  --json)
    case "$mode" in
    crafts-fails) printf 'fugaro: the crafts could not be read\n' >&2; exit 1 ;;
    crafts-unreadable) printf 'not json at all\n' ;;
    esac
    cat "$FAKE_JSON_DIR/crafts.json"
    ;;
  *)
    echo "fugaro: unexpected craft ls argument ${3:-}" >&2
    exit 1
    ;;
  esac
  ;;
accounts)
  if [ "${2:-}" != "pool" ] || [ "${3:-}" != "ls" ]; then
    echo "fugaro: unexpected accounts subcommand ${2:-}" >&2
    exit 1
  fi
  case "${4:-}" in
  --help)
    case "$mode" in
    no-feature) echo "fugaro: unknown command \"accounts\" for \"fugaro\"" >&2; exit 1 ;;
    esac
    usage "accounts pool ls"
    ;;
  *)
    printf 'NAME\tSTRATEGY\tMEMBERS\tSERVED BY\n'
    ;;
  esac
  ;;
project)
  if [ "${2:-}" != "policy" ] || [ "${3:-}" != "show" ]; then
    echo "fugaro: unexpected project subcommand ${2:-}" >&2
    exit 1
  fi
  case "${4:-}" in
  --help)
    case "$mode" in
    no-feature) echo "fugaro: unknown command \"project\" for \"fugaro\"" >&2; exit 1 ;;
    esac
    usage "project policy show"
    ;;
  --json)
    case "$mode" in
    policy-fails) printf 'fugaro: the published node could not be read\n' >&2; exit 1 ;;
    policy-unreadable) printf 'not json at all\n' ;;
    esac
    cat "$FAKE_JSON_DIR/policy.json"
    ;;
  *)
    echo "fugaro: unexpected project policy show argument ${4:-}" >&2
    exit 1
    ;;
  esac
  ;;
creds)
  if [ "${2:-}" != "ls" ]; then
    echo "fugaro: unexpected creds subcommand ${2:-}" >&2
    exit 1
  fi
  case "${3:-}" in
  --help)
    case "$mode" in
    no-feature) echo "fugaro: unknown command \"creds\" for \"fugaro\"" >&2; exit 1 ;;
    esac
    usage "creds ls"
    ;;
  --json)
    case "$mode" in
    creds-fails) printf 'fugaro: the bundle could not be read\n' >&2; exit 1 ;;
    esac
    cat "$FAKE_JSON_DIR/creds.json"
    ;;
  *)
    echo "fugaro: unexpected creds ls argument ${3:-}" >&2
    exit 1
    ;;
  esac
  ;;
doctor)
  case "$mode" in
  doctor-fails) printf 'fugaro: no installation here\n' >&2; exit 1 ;;
  doctor-unreadable) printf 'not json at all\n' ;;
  esac
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
  write_doctor "$dir"
  write_crafts "$dir" "$coder_model" "$pool" ""
  write_policy "$dir" failover
  write_creds "$dir" both
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"2000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261114-120000-abcd","execution":"exec-1","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"finalize","terminal":true,"settled":true,"branch":"fugaro/20261114-120000-abcd","base_branch":"main"}],"totals":{"runs":1},"warnings":[]}
JSON
  write_record "$dir" "$run_id" succeeded default
}

write_doctor() { # write_doctor DIR
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk420","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_crafts DIR MODEL ACCESS PROBLEMS: `craft ls --json`'s document, with the
# coder row the check reads and the other crafts beside it (so a case that names
# the wrong craft reads a list that is not empty).
write_crafts() { # write_crafts DIR MODEL ACCESS PROBLEMS
  local model=$2 access=$3 problems=${4:-[]} rows
  rows=$(cat <<JSON
{"craft":"coder","model":"$model","model_from":"repo","access":"$access","access_from":"repo","inert":false}
JSON
)
  cat >"$1/json/crafts.json" <<JSON
{"version":1,"project":"chk420","crafts":[$rows],"problems":$problems}
JSON
}

# write_policy DIR STATE: the published node in the states the check has to tell
# apart. `failover` is the state Check 420 measures; every other state is one a
# run would be a reading of something else in, and the check's own row names it.
write_policy() { # write_policy DIR STATE
  local state=$2 spread=true strategy=failover members="\"$first_member\",\"$second_member\"" creds pools
  case "$state" in
  failover) ;;
  round-robin) strategy=round-robin ;;
  one-member) members="\"$first_member\"" ;;
  spread-off) spread=false ;;
  openrouter-member) members="\"$first_member\",\"openrouter-key\"" ;;
  unpublished)
    cat >"$1/json/policy.json" <<JSON
{"project":"chk420","policy":null}
JSON
    return 0
    ;;
  esac
  creds=$(cat <<JSON
{"name":"$first_member","type":"claude-oauth"},{"name":"$second_member","type":"anthropic"}
JSON
)
  if [ "$state" = openrouter-member ]; then
    creds="$creds"',{"name":"openrouter-key","type":"openrouter"}'
  fi
  cat >"$1/json/policy.json" <<JSON
{"project":"chk420","policy":{"subscription_spread":$spread,"crafts":{"coder":{"model":"$coder_model","access":"$pool"}},"craft_credentials":[$creds],"craft_pools":[{"name":"$pool","strategy":"$strategy","members":[$members]}]}}
JSON
}

# write_policy_single_credential DIR: the coder craft whose access is a CREDENTIAL
# and not a pool — a state a run measures nothing about failover in.
write_policy_single_credential() { # write_policy_single_credential DIR
  cat >"$1/json/policy.json" <<JSON
{"project":"chk420","policy":{"subscription_spread":true,"crafts":{"coder":{"model":"$coder_model","access":"$second_member"}},"craft_credentials":[{"name":"$first_member","type":"claude-oauth"},{"name":"$second_member","type":"anthropic"}]}}
JSON
}

# write_policy_unknown_access DIR: the node's coder access names neither a pool nor
# a credential the node holds.
write_policy_unknown_access() { # write_policy_unknown_access DIR
  cat >"$1/json/policy.json" <<JSON
{"project":"chk420","policy":{"subscription_spread":true,"crafts":{"coder":{"model":"$coder_model","access":"$pool"}},"craft_credentials":[{"name":"$first_member","type":"claude-oauth"},{"name":"$second_member","type":"anthropic"}],"craft_pools":[{"name":"another-pool","strategy":"failover","members":["$first_member","$second_member"]}]}}
JSON
}

# write_creds DIR WHICH: the credential names the credentials service's bundle
# holds. `both` is the state a failover needs; `first-only` is a pool whose second
# member the service cannot hand to a call at all.
write_creds() { # write_creds DIR WHICH
  local rows
  case "$2" in
  first-only) rows="{\"name\":\"$first_member\",\"present\":true,\"changed\":\"2026-11-14T12:00:00Z\"}" ;;
  *) rows="{\"name\":\"$first_member\",\"present\":true,\"changed\":\"2026-11-14T12:00:00Z\"},{\"name\":\"$second_member\",\"present\":true,\"changed\":\"2026-11-14T12:00:00Z\"}" ;;
  esac
  cat >"$1/json/creds.json" <<JSON
{"project":"chk420","secret":"fugaro-creds","version":"7","changed":"2026-11-14T12:00:00Z","credentials":[$rows]}
JSON
}

# write_record DIR RUN_ID STATUS STATE: the run's record. `default` is a run the
# pool failed over inside — the switch the check exists to read — and every other
# state is a shape the check has to tell apart from it.
write_record() { # write_record DIR RUN_ID STATUS STATE
  local dir=$1 rid=$2 status=$3 state=$4 switches oauth stages craft_creds outcome=failed
  case "$state" in
  no-switch)
    switches="[]"
    oauth=$second_member
    stages="{\"implement\":\"$second_member\"}"
    craft_creds="{\"coder\":\"$second_member\"}"
    outcome=succeeded
    ;;
  other-switch)
    switches="[{\"from\":\"$first_member\",\"to\":\"another-credential\",\"reason\":\"the account answered 429\"}]"
    oauth=another-credential
    stages="{\"implement\":\"another-credential\"}"
    craft_creds="{\"coder\":\"another-credential\"}"
    outcome=succeeded
    ;;
  no-pool-member)
    switches="[{\"from\":\"$first_member\",\"to\":\"$second_member\",\"reason\":\"the account answered 429\"}]"
    oauth=some-other-credential
    stages="{\"implement\":\"some-other-credential\"}"
    craft_creds="{\"coder\":\"$second_member\"}"
    outcome=succeeded
    ;;
  no-craft-credentials)
    switches="[{\"from\":\"$first_member\",\"to\":\"$second_member\",\"reason\":\"the account answered 429\"}]"
    oauth=$second_member
    stages="{\"implement\":\"$first_member\",\"review\":\"$second_member\"}"
    craft_creds="__ABSENT__"
    outcome=succeeded
    ;;
  empty-craft-credentials)
    switches="[{\"from\":\"$first_member\",\"to\":\"$second_member\",\"reason\":\"the account answered 429\"}]"
    oauth=$second_member
    stages="{\"implement\":\"$second_member\"}"
    craft_creds="{}"
    outcome=succeeded
    ;;
  *)
    switches="[{\"from\":\"$first_member\",\"to\":\"$second_member\",\"reason\":\"the account answered 429\"}]"
    oauth=$second_member
    stages="{\"implement\":\"$first_member\",\"review\":\"$second_member\"}"
    craft_creds="{\"coder\":\"$second_member\"}"
    outcome=succeeded
    ;;
  esac
  local cc=",\"craft_credentials\":$craft_creds"
  if [ "$craft_creds" = "__ABSENT__" ]; then cc=""; fi
  cat >"$dir/json/record.json" <<JSON
{"version":1,"run_id":"$rid","repo":"acme/sandbox","status":"$status","outcome":"$outcome","cost":{"model_usd":0.42},"oauth_account":"$oauth","stage_accounts":$stages,"switches":$switches$cc}
JSON
}

# write_checkout DIR: a directory that looks like a Fugaro repository's checkout,
# which is all the check reads before it runs a command in it.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk420\n' >"$1/fugaro.yaml"
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
    CHECK420_SANDBOX="$dir/sandbox" CHECK420_YES=1 \
    CHECK420_RUN_TIMEOUT_SECONDS=10 CHECK420_POLL_SECONDS=1 \
    CHECK420_CLI_TIMEOUT_SECONDS=20 CHECK420_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK420_DOCTOR_TIMEOUT_SECONDS=20 \
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
  expect_contains "$out" "bash scripts/live-checks/check420.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that a run is billable and that the check is theirs.
  expect_contains "$out" "ONE sandbox run and what its task costs" "--help: the cost of a run is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "never a value" "--help: nothing secret is printed"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK420_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 420 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: INFO" "dry run: the version row is a reading"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the trace guard"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 420 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT pool: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT failover: INFO — planned" "dry run: the failover row is planned too"
  expect_contains "$err" "ONE sandbox run" "dry run: the plan names the spend"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "craft ls --json" "dry run: the crafts are not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
  assert_no_run "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT features: PASS" "happy path: the build carries the vocabulary"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT crafts: PASS" "happy path: the coder craft's access"
  expect_contains "$out" "the coder craft's access is '${pool}'" "happy path: the access it read"
  expect_contains "$out" "FACT pool: PASS" "happy path: the premise"
  expect_contains "$out" "is a failover pool of ${first_member}, ${second_member}" "happy path: the members it read"
  expect_contains "$out" "FACT bundle: PASS" "happy path: the members are in the bundle"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the record"
  expect_contains "$out" "FACT run-succeeded: PASS" "happy path: the run passed"
  expect_contains "$out" "FACT budget: INFO" "happy path: the cost is read and not graded"
  expect_contains "$out" "FACT failover: PASS" "happy path: the failover"
  expect_contains "$out" "logs the switch ${first_member} → ${second_member}" "happy path: which switch it read"
  expect_contains "$out" "FACT served: PASS" "happy path: the credential the record names"
  expect_contains "$out" "names '${second_member}' — the pool's SECOND member" "happy path: which credential"
  expect_contains "$out" "FACT craft-credentials: PASS" "happy path: the additive field"
  expect_contains "$out" "CHECK 420 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, and the record was read at
  # the run's own key — the one-segment slug, not the owner/name the launch names.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "craft ls --json (in $dir/sandbox)" "happy path: the crafts are read through the checkout"
  expect_contains "$calls" "project policy show --json (in $dir/sandbox)" "happy path: the node is read through the checkout"
  expect_contains "$calls" "creds ls --json (in $dir/sandbox)" "happy path: the bundle is read through the checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the run is launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the run is followed by its own slug"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${run_id}/result.json" "happy path: the record is read at the run's own key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  assert_scratch_removed "$dir" "happy path"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK420_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check420.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK420_SANDBOX="$dir/empty"
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
  # The check drives commands whose writes a coding agent's session is refused
  # (`fugaro creds ls` among them): refusing here is what keeps the block from
  # reporting those refusals as findings about the release.
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
  printf '{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk420-belong","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "a project naming belong: the refusal"
  assert_no_run "$dir" "a project naming belong"
}

test_yes_is_required() {
  # The run is billable, so the check asks for --yes before it launches one.
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK420_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "asks for --yes" "no --yes: the refusal"
  expect_contains "$err" "Nothing has been launched" "no --yes: what it says about the money"
  assert_no_run "$dir" "no --yes"
}

test_a_build_without_the_crafts_vocabulary_is_refused() {
  # The feature gate: a build whose `craft ls` is not a command has no craft, no
  # pool and no way to read one, whatever its version line says.
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "a build without the crafts vocabulary"
  expect_contains "$out" "FACT features: FAIL" "a build without the crafts vocabulary: the row"
  expect_contains "$out" "no 'fugaro craft ls'" "a build without the crafts vocabulary: the command it names"
  expect_contains "$out" "fugaro update" "a build without the crafts vocabulary: the one command that fixes it"
  expect_contains "$out" "that one command is the whole upgrade" "a build without the crafts vocabulary: why one command is enough"
  assert_no_run "$dir" "a build without the crafts vocabulary"
}

test_a_doctor_that_cannot_be_read_is_not_measured() {
  local dir=$work/nodoctor
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-fails
  expect_status "$status" 2 "a doctor that could not be read"
  expect_contains "$out" "FACT sandbox: FAIL" "a doctor that could not be read: the row"
  expect_contains "$err" "could NOT measure sandbox" "a doctor that could not be read: the exit names the row"
  assert_no_run "$dir" "a doctor that could not be read"
}

test_a_doctor_that_is_not_a_document_is_not_measured() {
  local dir=$work/baddoctor
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-unreadable
  expect_status "$status" 2 "a doctor that is not a document"
  expect_contains "$out" "FACT sandbox: FAIL" "a doctor that is not a document: the row"
  expect_contains "$out" "could not read" "a doctor that is not a document: what it found"
}

test_a_craft_that_resolves_to_no_pool_is_a_finding() {
  # The default credential is the built-in vertex, and no pool of it exists: a run
  # here would measure nothing about failover.
  local dir=$work/nopool
  prepare "$dir"
  write_crafts "$dir" "$coder_model" vertex ""
  run_check "$dir"
  expect_status "$status" 1 "a craft on the built-in credential"
  expect_contains "$out" "FACT crafts: FAIL" "a craft on the built-in credential: the row"
  expect_contains "$out" "the built-in vertex credential" "a craft on the built-in credential: what it means"
  assert_no_run "$dir" "a craft on the built-in credential"
}

test_a_craft_list_with_problems_is_a_finding() {
  local dir=$work/problems
  prepare "$dir"
  write_crafts "$dir" "$coder_model" "$pool" '["agent.crafts.coder.access: no credential of this name is defined"]'
  run_check "$dir"
  expect_status "$status" 1 "a craft list with problems"
  expect_contains "$out" "FACT crafts: FAIL" "a craft list with problems: the row"
  expect_contains "$out" "reports 1 problem(s)" "a craft list with problems: how many"
  assert_no_run "$dir" "a craft list with problems"
}

test_a_pool_of_one_member_is_a_finding() {
  local dir=$work/onemember
  prepare "$dir"
  write_policy "$dir" one-member
  run_check "$dir"
  expect_status "$status" 1 "a pool of one member"
  expect_contains "$out" "FACT pool: FAIL" "a pool of one member: the row"
  expect_contains "$out" "lists one member" "a pool of one member: what it found"
  assert_no_run "$dir" "a pool of one member"
}

test_a_round_robin_pool_is_a_finding() {
  # Round-robin shares the load out instead of moving to the next member when one
  # is exhausted: it is Check 421's reading, not this one's.
  local dir=$work/roundrobin
  prepare "$dir"
  write_policy "$dir" round-robin
  run_check "$dir"
  expect_status "$status" 1 "a round-robin pool"
  expect_contains "$out" "FACT pool: FAIL" "a round-robin pool: the row"
  expect_contains "$out" "walks its members with strategy 'round-robin'" "a round-robin pool: what it found"
  expect_contains "$out" "Check 421's reading" "a round-robin pool: where that reading lives"
  assert_no_run "$dir" "a round-robin pool"
}

test_a_member_the_service_cannot_serve_is_a_finding() {
  # The runner path SELECTS one member for a stage and cannot move to the next
  # (F23, D1637): a pool whose member is served on that path cannot fail over
  # inside a run, however many members it lists.
  local dir=$work/openrouter
  prepare "$dir"
  write_policy "$dir" openrouter-member
  run_check "$dir"
  expect_status "$status" 1 "a member the service cannot serve"
  expect_contains "$out" "FACT pool: FAIL" "a member the service cannot serve: the row"
  expect_contains "$out" "a openrouter credential" "a member the service cannot serve: the type it found"
  expect_contains "$out" "cannot move to the next" "a member the service cannot serve: why"
  assert_no_run "$dir" "a member the service cannot serve"
}

test_a_subscription_member_with_spread_off_is_a_finding() {
  # With the project's subscription_spread switch off, a subscription inside a
  # pool that also holds another kind is stepped over (D1622, D1626): the pool is
  # left with fewer candidates than it lists.
  local dir=$work/spreadoff
  prepare "$dir"
  write_policy "$dir" spread-off
  run_check "$dir"
  expect_status "$status" 1 "a subscription member with spread off"
  expect_contains "$out" "FACT pool: FAIL" "a subscription member with spread off: the row"
  expect_contains "$out" "subscription_spread switch is off" "a subscription member with spread off: the switch it read"
  assert_no_run "$dir" "a subscription member with spread off"
}

test_a_pool_the_node_does_not_hold_is_a_finding() {
  local dir=$work/nopoolnode
  prepare "$dir"
  write_policy_unknown_access "$dir"
  run_check "$dir"
  expect_status "$status" 1 "a pool the node does not hold"
  expect_contains "$out" "FACT pool: FAIL" "a pool the node does not hold: the row"
  expect_contains "$out" "neither a pool nor a credential the published node holds" "a pool the node does not hold: what it found"
  assert_no_run "$dir" "a pool the node does not hold"
}

test_a_craft_on_a_single_credential_is_a_finding() {
  local dir=$work/singlecred
  prepare "$dir"
  # The craft read and the node have to agree, or the check would be reading a
  # name the node does not hold at all rather than a credential instead of a pool.
  write_crafts "$dir" "$coder_model" "$second_member" ""
  write_policy_single_credential "$dir"
  run_check "$dir"
  expect_status "$status" 1 "a craft on a single credential"
  expect_contains "$out" "FACT pool: FAIL" "a craft on a single credential: the row"
  expect_contains "$out" "nothing to fail over TO" "a craft on a single credential: what it means"
  assert_no_run "$dir" "a craft on a single credential"
}

test_an_unpublished_node_is_a_finding() {
  local dir=$work/unpublished
  prepare "$dir"
  write_policy "$dir" unpublished
  run_check "$dir"
  expect_status "$status" 1 "an unpublished node"
  expect_contains "$out" "FACT pool: FAIL" "an unpublished node: the row"
  expect_contains "$out" "no project policy is published" "an unpublished node: what it found"
  assert_no_run "$dir" "an unpublished node"
}

test_a_node_that_cannot_be_read_is_not_measured() {
  local dir=$work/nopolicy
  prepare "$dir"
  run_check "$dir" FAKE_MODE=policy-fails
  expect_status "$status" 2 "a node that could not be read"
  expect_contains "$out" "FACT pool: FAIL" "a node that could not be read: the row"
  expect_contains "$err" "could NOT measure pool" "a node that could not be read: the exit names the row"
  assert_no_run "$dir" "a node that could not be read"
}

test_a_member_missing_from_the_bundle_is_a_finding() {
  # A member the bundle does not hold is one the service cannot hand to a call at
  # all: the pool would be refused rather than failed over.
  local dir=$work/nobundle
  prepare "$dir"
  write_creds "$dir" first-only
  run_check "$dir"
  expect_status "$status" 1 "a member missing from the bundle"
  expect_contains "$out" "FACT bundle: FAIL" "a member missing from the bundle: the row"
  expect_contains "$out" "does not hold ${second_member}" "a member missing from the bundle: which one"
  expect_contains "$out" "fugaro creds set <member>" "a member missing from the bundle: the remedy"
  # The bundle is part of the premise, so nothing is launched: a run on a pool the
  # service cannot hand in full would be refused rather than failed over. The row
  # above it is a finding about the installation and not a reading the check
  # could not take, so the block ends 1 rather than 2.
  expect_contains "$out" "FACT run-launched: FAIL" "a member missing from the bundle: the row that follows"
  expect_contains "$out" "nothing was launched" "a member missing from the bundle: why no run"
  assert_no_run "$dir" "a member missing from the bundle"
}

test_a_bundle_that_cannot_be_read_is_not_measured() {
  local dir=$work/nocreds
  prepare "$dir"
  run_check "$dir" FAKE_MODE=creds-fails
  expect_status "$status" 2 "a bundle that could not be read"
  expect_contains "$out" "FACT bundle: FAIL" "a bundle that could not be read: the row"
  expect_contains "$err" "could NOT measure bundle" "a bundle that could not be read: the exit names the row"
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
  run_check "$dir" CHECK420_RUN_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a run that never ended"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ended: the row"
  expect_contains "$out" "had not ended after" "a run that never ended: what it saw"
  expect_contains "$out" "fugaro watch ${run_id}" "a run that never ended: how to watch it"
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

test_a_record_that_is_not_a_document_is_a_finding() {
  local dir=$work/badrecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-unreadable
  expect_status "$status" 1 "a record that is not a document"
  expect_contains "$out" "FACT result-json: FAIL" "a record that is not a document: the row"
  expect_contains "$out" "is not a document this check could read" "a record that is not a document: what it found"
}

test_a_record_of_another_run_is_a_finding() {
  # A record read by listing alone could be another run's: the record's own
  # run_id is what ties it to the run this check launched.
  local dir=$work/otherrecord
  prepare "$dir"
  write_record "$dir" run-20261114000000-9 succeeded default
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20261114000000-9 and this check launched ${run_id}" "a record of another run: the two IDs"
}

test_a_run_that_did_not_succeed_is_a_finding() {
  local dir=$work/failed
  prepare "$dir"
  write_record "$dir" "$run_id" failed default
  run_check "$dir"
  expect_status "$status" 1 "a run that failed"
  expect_contains "$out" "FACT run-succeeded: FAIL" "a run that failed: the row"
  expect_contains "$out" "the failover below is read from a run that ran" "a run that failed: what it means"
  expect_contains "$out" "fugaro diagnose ${run_id}" "a run that failed: how to read it"
}

test_a_run_over_the_bound_is_a_finding() {
  # CHECK420_MAX_MODEL_USD turns the figure into a criterion; unset, the row is a
  # reading. The money is spent either way, which the row says.
  local dir=$work/overmax
  prepare "$dir"
  run_check "$dir" CHECK420_MAX_MODEL_USD=0.10
  expect_status "$status" 1 "a run over the bound"
  expect_contains "$out" "FACT budget: FAIL" "a run over the bound: the row"
  expect_contains "$out" "cost 0.42 USD of model spend and CHECK420_MAX_MODEL_USD=0.10" "a run over the bound: the figures it read"
  expect_contains "$out" "the money is spent" "a run over the bound: what it says about the money"
}

test_a_run_with_no_switch_is_a_finding() {
  # The row the whole check exists for: a run whose record carries no switch never
  # moved, whatever else it says.
  local dir=$work/noswitch
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded no-switch
  run_check "$dir"
  expect_status "$status" 1 "a run with no switch"
  expect_contains "$out" "FACT failover: FAIL" "a run with no switch: the row"
  expect_contains "$out" "carries NO switch at all" "a run with no switch: what it found"
  expect_contains "$out" "the member the pool lists FIRST" "a run with no switch: what to check"
}

test_a_switch_onto_another_member_is_a_finding() {
  local dir=$work/otherswitch
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded other-switch
  run_check "$dir"
  expect_status "$status" 1 "a switch onto another member"
  expect_contains "$out" "FACT failover: FAIL" "a switch onto another member: the row"
  expect_contains "$out" "none of them lands on '${second_member}'" "a switch onto another member: what it found"
  expect_contains "$out" "${first_member}→another-credential" "a switch onto another member: the log it printed"
}

test_a_record_that_names_no_pool_member_is_a_finding() {
  local dir=$work/nomember
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded no-pool-member
  run_check "$dir"
  expect_status "$status" 1 "a record that names no pool member"
  expect_contains "$out" "FACT served: FAIL" "a record that names no pool member: the row"
  expect_contains "$out" "names no member of '${pool}'" "a record that names no pool member: what it found"
}

test_a_record_without_craft_credentials_is_a_finding() {
  # Check 420's own sentence is that `craft_credentials` names the member that
  # served, and D1629 gives the record that additive field (MC8). A build whose
  # record does not carry it is a finding rather than a pass: reading
  # stage_accounts and calling it craft_credentials would be this script asserting
  # a field the release does not have.
  local dir=$work/nocraftcreds
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded no-craft-credentials
  run_check "$dir"
  expect_status "$status" 1 "a record without craft_credentials"
  expect_contains "$out" "FACT served: PASS" "a record without craft_credentials: the fields the release has still read"
  expect_contains "$out" "FACT craft-credentials: FAIL" "a record without craft_credentials: the row"
  expect_contains "$out" "carries no craft_credentials" "a record without craft_credentials: what it found"
  expect_contains "$out" "MC8 is the task that writes it" "a record without craft_credentials: the task it waits on"
}

test_an_empty_craft_credentials_block_is_a_finding() {
  local dir=$work/emptycreds
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded empty-craft-credentials
  run_check "$dir"
  expect_status "$status" 1 "an empty craft_credentials block"
  expect_contains "$out" "FACT craft-credentials: FAIL" "an empty craft_credentials block: the row"
  expect_contains "$out" "names no credential" "an empty craft_credentials block: what it found"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK420_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK420_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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

# THE MUTATION. The row that reads the run's switch log is the reading Check 420
# names, and it is only worth anything if it really fails a run that never moved.
# The case is a COPY of the script with that one guard replaced by `false`, run
# against the same fixture the case above fails: if the row were not
# load-bearing, the copy would report nothing and this test would say so.
test_the_failover_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  write_record "$dir" "$run_id" succeeded no-switch
  mkdir -p "$dir/copy"
  sed 's/if \[ -z "\$to" \]; then/if false; then/' "$script" >"$dir/copy/check420.sh"
  grep -q "if false; then" "$dir/copy/check420.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check420.sh"
  expect_contains "$out" "FACT failover: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT failover: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT failover: FAIL" "the real script: the row"
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
t test_a_build_without_the_crafts_vocabulary_is_refused
t test_a_doctor_that_cannot_be_read_is_not_measured
t test_a_doctor_that_is_not_a_document_is_not_measured
t test_a_craft_that_resolves_to_no_pool_is_a_finding
t test_a_craft_list_with_problems_is_a_finding
t test_a_pool_of_one_member_is_a_finding
t test_a_round_robin_pool_is_a_finding
t test_a_member_the_service_cannot_serve_is_a_finding
t test_a_subscription_member_with_spread_off_is_a_finding
t test_a_pool_the_node_does_not_hold_is_a_finding
t test_a_craft_on_a_single_credential_is_a_finding
t test_an_unpublished_node_is_a_finding
t test_a_node_that_cannot_be_read_is_not_measured
t test_a_member_missing_from_the_bundle_is_a_finding
t test_a_bundle_that_cannot_be_read_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_that_is_not_a_document_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_run_that_did_not_succeed_is_a_finding
t test_a_run_over_the_bound_is_a_finding
t test_a_run_with_no_switch_is_a_finding
t test_a_switch_onto_another_member_is_a_finding
t test_a_record_that_names_no_pool_member_is_a_finding
t test_a_record_without_craft_credentials_is_a_finding
t test_an_empty_craft_credentials_block_is_a_finding
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_failover_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check420_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check420_test.sh: ok"
