#!/usr/bin/env bash
# Tests for scripts/live-checks/check629.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, an installation or a run, no policy is
# published anywhere, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do
# (and that the dry run published nothing at all).
#
# The model the fakes carry is one installation whose config declares two
# credentials and a pool that walks one of them then the other, a policy node
# that carries craft_credentials, craft_pools and crafts, and a run the
# credentials service served with the pool's SECOND member after the first was
# refused as exhausted. The cases that matter are the ones where that model is
# bent: a pool the config does not declare, a pool of one member, a member that
# is no declared credential, a publish that refuses, a node that carries only
# two of the three blocks, a run still going, a record that cannot be read, an
# audit line for the first member only, and a record that carries the move but
# no audit line at all.
#
# Usage: bash scripts/live-checks/check629_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$here/lib}
script=$here/check629.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK629_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK629_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk629-test
installation=chk629
pool=coder-pool
first_member=deepseek-key
second_member=openrouter-x
model=deepseek/deepseek-v4
run_id=run-20261120150000-1
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
job=fugaro-run-9f8e7d6c
execution="projects/$project/locations/us-east5/jobs/$job/executions/$job-abc123"
other_run=run-20261119120000-9

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
. "$LIVE_CHECK_LIB/strict.sh"
strict_gcloud_verify "$LIVE_CHECK_LIB/allow/check629.gcloud" "$@" || exit $?
# A stand-in for `gcloud`, for scripts/live-checks/check629.sh's test only. It
# answers the account read, the run's record and the credentials service's own
# audit lines — the last two from whatever files the case wrote. Nothing here
# reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk629-owner@example.com\n'
  fi
  ;;
storage)
  if [ "${2:-}" = "cat" ]; then
    case "$mode" in
    record-fail)
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
      ;;
    record-other-run) cat "$FAKE_JSON_DIR/record-other-run.json" ;;
    *) cat "$FAKE_JSON_DIR/record.json" ;;
    esac
  fi
  ;;
logging)
  case "$mode" in
  audit-fail)
    echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED: caller does not have permission" >&2
    exit 1
    ;;
  audit-none) printf '[]\n' ;;
  audit-second-only) cat "$FAKE_JSON_DIR/audit-second-only.json" ;;
  audit-first-ok) cat "$FAKE_JSON_DIR/audit-first-ok.json" ;;
  *) cat "$FAKE_JSON_DIR/audit.json" ;;
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
# A stand-in for `fugaro`, for scripts/live-checks/check629.sh's test only. The
# doctor read (the TEXT the check parses its `project:` line out of), the
# credentials listing, the pools listing, the publish, the node, the launch and
# the runs list all come from files the case wrote, and every call is logged
# with the directory it was made in.
set -euo pipefail
. "$LIVE_CHECK_LIB/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version) printf '0.28.0\n' ;;
doctor)
  case "$mode" in
  doctor-no-project)
    printf 'fugaro dev (0.28.0)\nall checks passed\n'
    exit 0
    ;;
  doctor-belong)
    printf 'fugaro 0.28.0\nproject: chk629 (GCP belong-sandbox, us-east5)\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/doctor.txt"
  ;;
accounts)
  case "${2:-}" in
  ls)
    case "$mode" in
    creds-fail)
      echo "fugaro: cannot read the config" >&2
      exit 1
      ;;
    creds-garbage)
      printf 'not a document\n'
      exit 0
      ;;
    creds-missing-member) cat "$FAKE_JSON_DIR/creds-missing-member.json" ;;
    *) cat "$FAKE_JSON_DIR/creds.json" ;;
    esac
    ;;
  pool)
    case "$mode" in
    pools-fail)
      echo "fugaro: cannot read the config" >&2
      exit 1
      ;;
    pool-none) cat "$FAKE_JSON_DIR/pools-none.txt" ;;
    pool-one-member) cat "$FAKE_JSON_DIR/pools-one-member.txt" ;;
    *) cat "$FAKE_JSON_DIR/pools.txt" ;;
    esac
    ;;
  *)
    echo "fugaro: unexpected accounts subcommand" >&2
    exit 1
    ;;
  esac
  ;;
project)
  [ "${2:-}" = "policy" ] || { echo "fugaro: unexpected project subcommand" >&2; exit 1; }
  case "${3:-}" in
  publish)
    case "$mode" in
    publish-fail)
      echo "fugaro: this account is not an admin of the project" >&2
      exit 3
      ;;
    publish-slow)
      sleep 30
      printf 'published\n'
      ;;
    *) printf 'policy node published (version 10)\n' ;;
    esac
    ;;
  show)
    case "$mode" in
    node-fail)
      echo "fugaro: this installation has no published policy node" >&2
      exit 1
      ;;
    node-garbage)
      printf 'not a document\n'
      exit 0
      ;;
    node-no-pools) cat "$FAKE_JSON_DIR/node-no-pools.json" ;;
    node-no-credential) cat "$FAKE_JSON_DIR/node-no-credential.json" ;;
    node-no-crafts) cat "$FAKE_JSON_DIR/node-no-crafts.json" ;;
    node-other-order) cat "$FAKE_JSON_DIR/node-other-order.json" ;;
    *) cat "$FAKE_JSON_DIR/node.json" ;;
    esac
    ;;
  *)
    echo "fugaro: unexpected policy subcommand" >&2
    exit 1
    ;;
  esac
  ;;
run)
  case "$mode" in
  launch-fails)
    printf 'fugaro: the sandbox repository is not enrolled in this installation\n' >&2
    exit 3
    ;;
  launch-garbage)
    printf 'launched\n'
    exit 0
    ;;
  *) cat "$FAKE_JSON_DIR/launch.json" ;;
  esac
  ;;
runs)
  case "$mode" in
  run-never-ends) cat "$FAKE_JSON_DIR/rows-running.json" ;;
  # A run that is still working on the first poll and ended by the next one: the
  # check has to read each poll's answer as its own document — an answer read on
  # top of the one before it never looks ended.
  run-ends-later)
    polls=0
    if [ -f "$FAKE_LOG_DIR/runs-polls" ]; then polls=$(cat "$FAKE_LOG_DIR/runs-polls"); fi
    polls=$((polls + 1))
    printf '%s\n' "$polls" >"$FAKE_LOG_DIR/runs-polls"
    if [ "$polls" -le 1 ]; then
      cat "$FAKE_JSON_DIR/rows-running.json"
    else
      cat "$FAKE_JSON_DIR/rows.json"
    fi
    ;;
  *) cat "$FAKE_JSON_DIR/rows.json" ;;
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

# ------------------------------------------------------------------ documents
# The credentials listing: the names, the types and the half that serves each —
# never a value. `serving_path` is the config's own spelling of that half
# (config.CredentialServingPath): "credsvc" or "runner", as `fugaro accounts ls
# --json` prints it, and the pool listing below reads the same half as prose.
write_creds() { # json-dir MODE
  case "$2" in
  missing-member)
    cat >"$1/creds-missing-member.json" <<JSON
{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":[{"name":"$first_member","type":"openrouter","serving_path":"runner"}]}
JSON
    ;;
  esac
  cat >"$1/creds.json" <<JSON
{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":[{"name":"$first_member","type":"openrouter","serving_path":"runner"},{"name":"$second_member","type":"openrouter","serving_path":"runner"},{"name":"claude-oauth-work","type":"anthropic","serving_path":"credsvc"}]}
JSON
}

# The pools listing, in the shape `writeAccountsPoolLs` prints: a tabwriter
# table padded with spaces, members joined with ", ".
write_pools() { # json-dir MODE
  case "$2" in
  pool-one-member)
    cat >"$1/pools-one-member.txt" <<TXT
pools for project $installation (offline)

NAME        STRATEGY  MEMBERS       SERVED BY
coder-pool  failover  $first_member  a run's own gateway
TXT
    ;;
  pool-none)
    cat >"$1/pools-none.txt" <<TXT
pools for project $installation (offline)

no pools are defined: a craft's access may name a credential on its own, or a pool you declare with fugaro accounts pool add
TXT
    ;;
  esac
  cat >"$1/pools.txt" <<TXT
pools for project $installation (offline)

NAME        STRATEGY  MEMBERS                    SERVED BY
coder-pool  failover  $first_member, $second_member  a run's own gateway
TXT
}

# Each node document is what `fugaro project policy show --json` prints: the
# node's blocks are members of `policy`, under the object's project.
write_nodes() { # json-dir
  cat >"$1/node.json" <<JSON
{"project":"$installation","policy":{"version":10,"craft_credentials":[{"name":"$first_member","type":"openrouter"},{"name":"$second_member","type":"openrouter"}],"craft_pools":[{"name":"$pool","strategy":"failover","members":["$first_member","$second_member"]}],"crafts":{"coder":{"model":"$model","access":"$pool"}}}}
JSON
  cat >"$1/node-other-order.json" <<JSON
{"project":"$installation","policy":{"version":10,"craft_credentials":[{"name":"$first_member","type":"openrouter"},{"name":"$second_member","type":"openrouter"}],"craft_pools":[{"name":"$pool","strategy":"failover","members":["$second_member","$first_member"]}],"crafts":{"coder":{"model":"$model","access":"$pool"}}}}
JSON
  cat >"$1/node-no-pools.json" <<JSON
{"project":"$installation","policy":{"version":10,"craft_credentials":[{"name":"$first_member","type":"openrouter"},{"name":"$second_member","type":"openrouter"}],"crafts":{"coder":{"model":"$model","access":"$pool"}}}}
JSON
  cat >"$1/node-no-credential.json" <<JSON
{"project":"$installation","policy":{"version":10,"craft_credentials":[{"name":"$first_member","type":"openrouter"}],"craft_pools":[{"name":"$pool","strategy":"failover","members":["$first_member","$second_member"]}],"crafts":{"coder":{"model":"$model","access":"$pool"}}}}
JSON
  cat >"$1/node-no-crafts.json" <<JSON
{"project":"$installation","policy":{"version":10,"craft_credentials":[{"name":"$first_member","type":"openrouter"},{"name":"$second_member","type":"openrouter"}],"craft_pools":[{"name":"$pool","strategy":"failover","members":["$first_member","$second_member"]}],"crafts":{}}}
JSON
}

# The service's own audit lines: NAMES for the credentials it served, their tier
# and their status, never a value. The default is the acceptance's own shape —
# the first member reached and refused (its error type is the service's own
# `exhausted` word), then the second member serving the call.
write_audit() { # json-dir MODE
  case "$2" in
  audit-second-only)
    cat >"$1/audit-second-only.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$second_member","purpose":"forward","tier":"B","status":"ok"}}]
JSON
    ;;
  audit-first-ok)
    cat >"$1/audit-first-ok.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$first_member","purpose":"forward","tier":"B","status":"ok"}},{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$first_member","purpose":"exchange","tier":"B","status":"ok"}}]
JSON
    ;;
  esac
  cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$first_member","purpose":"forward","tier":"B","status":"denied","error_type":"exhausted"}},{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$second_member","purpose":"forward","tier":"B","status":"ok"}}]
JSON
}

# The run's own record: the stage the pool served, and the switch log that says
# which member it moved off and why.
write_record() { # json-dir MODE
  local switches='[{"from":"'$first_member'","to":"'$second_member'","reason":"exhausted"}]'
  local craft_credentials=""
  case "$2" in
  record-no-switch) switches='[]' ;;
  record-other-switch) switches='[{"from":"claude-oauth-work","to":"'$second_member'","reason":"broken"}]' ;;
  record-craft-credentials) craft_credentials=',"craft_credentials":{"implement":"'$second_member'"}' ;;
  record-craft-credentials-other) craft_credentials=',"craft_credentials":{"implement":"'$first_member'"}' ;;
  esac
  cat >"$1/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.42,"total_usd":0.42},"stages":[{"name":"implement","model":"$model","credential":"$second_member","served_by":"api-key","model_usd":0.42}],"stage_accounts":{"implement":"$second_member"},"switches":$switches$craft_credentials}
JSON
  sed "s|$run_id|$other_run|" "$1/record.json" >"$1/record-other-run.json"
}

write_docs() { # dir MODE
  local json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.txt" <<TXT
fugaro 0.28.0
project: $installation (GCP $project, us-east5)
repository: acme/sandbox
TXT
  cat >"$json/launch.json" <<JSON
{"project":"$project","repo":"acme/sandbox","run_id":"$run_id","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/rows-running.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"running","stage":"implement","terminal":false,"settled":false}],"totals":{"runs":1},"warnings":[]}
JSON
  write_creds "$json" "$2"
  write_pools "$json" "$2"
  write_nodes "$json"
  write_audit "$json" "$2"
  write_record "$json" "$2"
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: %s\n' "$installation" >"$1/fugaro.yaml"
}

prepare() { # dir [mode]
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1" "${2:-}"
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
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" \
    CHECK629_SANDBOX="$dir/sandbox" \
    CHECK629_POOL="$pool" \
    CHECK629_RUN_TIMEOUT_SECONDS="${CHECK629_RUN_TIMEOUT_SECONDS:-10}" \
    CHECK629_POLL_SECONDS=1 \
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
  expect_contains "$out" "Check 629" "help: the header"
  expect_contains "$out" "What it measures" "help: the docblock"
  expect_contains "$out" "check629.sh --sandbox" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK629_*)" "help: the knobs"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK629_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT declaration: INFO" "a dry run: the planned rows are not measured"
  expect_contains "$err" "[write] fugaro project policy publish" "a dry run: the plan says which write it would make"
  expect_absent "$fugaro_calls" "policy publish" "a dry run: NOTHING is published"
  expect_absent "$fugaro_calls" "fugaro run " "a dry run: no run is launched"
  expect_absent "$gcloud_calls" "storage cat" "a dry run: no record is read"
  expect_absent "$gcloud_calls" "logging read" "a dry run: no log is read"
  assert_scratch_removed "$dir" "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in fugaro-version xtrace agent-session belong installation declaration publish node-blocks run-launched terminal result-json served-by-second first-exhausted restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_contains "$out" "FACT craft-credentials: INFO" "the happy path: this build's record carries no craft_credentials, and the row says so rather than passing"
  expect_contains "$out" "MC8's record half has not landed in this build" "the happy path: the row names the task that owns the field"
  expect_absent "$out" ": FAIL" "the happy path: no row failed"
  expect_contains "$out" "CHECK 629 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "the pool $pool is declared in $installation's config with its members in order ($first_member,$second_member" "the happy path: the declaration row reads the pool's order"
  expect_contains "$out" "fugaro project policy publish wrote the policy node of $installation" "the happy path: the publish row"
  expect_contains "$out" "the node of $installation read back from the publish carries craft_credentials" "the happy path: the three blocks are read back"
  expect_contains "$out" "craft_pools ($pool with its members in the config's own order: $first_member,$second_member)" "the happy path: the pools block carries the config's own order"
  expect_contains "$out" "the service served $run_id as $second_member, the second member of the pool $pool" "the happy path: the second member served the run"
  expect_contains "$out" "name the pool's first member $first_member(denied), its status not ok" "the happy path: the first member was reached and refused"
  expect_contains "$fugaro_calls" "fugaro doctor --all (in $dir/sandbox)" "the happy path: the installation is read from the checkout"
  expect_contains "$fugaro_calls" "fugaro accounts ls --json (in $dir/sandbox)" "the happy path: the credentials are read from the checkout"
  expect_contains "$fugaro_calls" "fugaro accounts pool ls (in $dir/sandbox)" "the happy path: the pools are read from the checkout"
  expect_contains "$fugaro_calls" "fugaro project policy publish (in $dir/sandbox)" "the happy path: the publish is made from the checkout"
  expect_contains "$fugaro_calls" "fugaro project policy show --json (in $dir/sandbox)" "the happy path: the node is read back from the checkout"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox" "the happy path: the run is launched from the checkout"
  expect_contains "$gcloud_calls" "storage cat gs://fugaro-runs-$project/runs/$slug/$run_id/result.json" "the happy path: the record is the run's own key"
  expect_contains "$gcloud_calls" "jsonPayload.run=\"$run_id\"" "the happy path: the audit read is this run's"
  assert_scratch_removed "$dir" "the happy path"
}

test_the_project_flag_is_passed_through() {
  local dir=$work/with-project
  prepare "$dir"
  run_check "$dir" CHECK629_PROJECT="$installation"
  expect_status "$status" 0 "a named project"
  expect_contains "$fugaro_calls" "fugaro doctor --project $installation --all (in $dir/sandbox)" "a named project: the doctor read names it"
  expect_contains "$fugaro_calls" "fugaro accounts pool ls --project $installation (in $dir/sandbox)" "a named project: the pools read names it"
  expect_contains "$fugaro_calls" "fugaro project policy publish --project $installation" "a named project: the publish names it"
}

test_a_pool_the_config_does_not_declare_is_a_finding() {
  local dir=$work/pool-none
  prepare "$dir" pool-none
  run_check "$dir" FAKE_MODE=pool-none
  expect_status "$status" 1 "a pool the config does not declare"
  expect_contains "$out" "FACT declaration: FAIL" "a pool the config does not declare: the row"
  expect_contains "$out" "names no pool '$pool' among the pools of $installation" "a pool the config does not declare: the row names the pool"
  expect_contains "$out" "fugaro accounts pool add $pool <member> <member>" "a pool the config does not declare: the remedy is a command"
  expect_absent "$fugaro_calls" "policy publish" "a pool the config does not declare: no node is published for a pool nothing declares"
  expect_absent "$fugaro_calls" "fugaro run " "a pool the config does not declare: no run is launched"
  assert_scratch_removed "$dir" "a pool the config does not declare"
}

test_a_pool_of_one_member_is_a_finding() {
  local dir=$work/pool-one
  prepare "$dir" pool-one-member
  run_check "$dir" FAKE_MODE=pool-one-member
  expect_status "$status" 1 "a pool of one member"
  expect_contains "$out" "FACT declaration: FAIL" "a pool of one member: the row"
  expect_contains "$out" "the acceptance is about a run served by the pool's SECOND member" "a pool of one member: the row says why two are needed"
}

test_a_member_the_config_does_not_declare_is_a_finding() {
  local dir=$work/missing-member
  prepare "$dir" missing-member
  run_check "$dir" FAKE_MODE=creds-missing-member
  expect_status "$status" 1 "a member that is no declared credential"
  expect_contains "$out" "FACT declaration: FAIL" "a member that is no declared credential: the row"
  expect_contains "$out" "does not declare $second_member as a credential" "a member that is no declared credential: the row names it"
}

test_a_credentials_read_that_does_not_answer_is_not_measured() {
  local dir=$work/creds-fail
  prepare "$dir" creds-fail
  run_check "$dir" FAKE_MODE=creds-fail
  expect_status "$status" 2 "a credentials read that does not answer"
  expect_contains "$out" "FACT declaration: FAIL" "a credentials read that does not answer: the row"
  expect_contains "$err" "could NOT measure declaration" "a credentials read that does not answer: it is named as unmeasured"
  expect_absent "$fugaro_calls" "policy publish" "a credentials read that does not answer: no node is published"
}

test_a_pools_read_that_does_not_answer_is_not_measured() {
  local dir=$work/pools-fail
  prepare "$dir" pools-fail
  run_check "$dir" FAKE_MODE=pools-fail
  expect_status "$status" 2 "a pools read that does not answer"
  expect_contains "$err" "could NOT measure declaration" "a pools read that does not answer: it is named as unmeasured"
}

test_a_credentials_listing_with_no_document_is_not_measured() {
  local dir=$work/creds-garbage
  prepare "$dir" creds-garbage
  run_check "$dir" FAKE_MODE=creds-garbage
  expect_status "$status" 2 "a credentials listing that is no document"
  expect_contains "$out" "FACT declaration: FAIL" "a credentials listing that is no document: the row"
}

test_a_publish_that_refuses_is_a_finding() {
  local dir=$work/publish-fail
  prepare "$dir" publish-fail
  run_check "$dir" FAKE_MODE=publish-fail
  expect_status "$status" 1 "a publish that refuses"
  expect_contains "$out" "FACT publish: FAIL" "a publish that refuses: the row"
  expect_contains "$out" "fugaro project policy publish exited 3" "a publish that refuses: the row quotes the exit"
  expect_contains "$out" "FACT node-blocks: FAIL" "a publish that refuses: the node read is unmeasured, not passed"
  expect_contains "$err" "could NOT measure node-blocks" "a publish that refuses: it is named as unmeasured"
}

test_a_publish_that_does_not_finish_is_not_measured() {
  local dir=$work/publish-slow
  prepare "$dir" publish-slow
  run_check "$dir" FAKE_MODE=publish-slow CHECK629_PUBLISH_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a publish that does not finish"
  expect_contains "$out" "FACT publish: FAIL" "a publish that does not finish: the row"
  expect_contains "$out" "did not finish inside 1s" "a publish that does not finish: the row names the bound"
}

test_a_node_missing_a_block_is_a_finding() {
  local dir=$work/node-no-pools
  prepare "$dir" node-no-pools
  run_check "$dir" FAKE_MODE=node-no-pools
  expect_status "$status" 1 "a node with no craft_pools"
  expect_contains "$out" "FACT node-blocks: FAIL" "a node with no craft_pools: the row"
  expect_contains "$out" "craft_pools:$pool" "a node with no craft_pools: the row names what is missing"
}

test_a_node_credential_missing_a_member_is_a_finding() {
  local dir=$work/node-no-credential
  prepare "$dir" node-no-credential
  run_check "$dir" FAKE_MODE=node-no-credential
  expect_status "$status" 1 "a node whose craft_credentials misses a member"
  expect_contains "$out" "craft_credentials:$second_member" "a node whose craft_credentials misses a member: the row names it"
}

test_a_node_with_no_crafts_is_a_finding() {
  local dir=$work/node-no-crafts
  prepare "$dir" node-no-crafts
  run_check "$dir" FAKE_MODE=node-no-crafts
  expect_status "$status" 1 "a node with no crafts"
  expect_contains "$out" "crafts(empty)" "a node with no crafts: the row names the block"
}

test_a_node_whose_pool_walks_another_order_is_a_finding() {
  local dir=$work/node-other-order
  prepare "$dir" node-other-order
  run_check "$dir" FAKE_MODE=node-other-order
  expect_status "$status" 1 "a node whose pool walks another order"
  expect_contains "$out" "craft_pools:$pool(members $second_member,$first_member, want $first_member,$second_member)" "a node whose pool walks another order: the row prints both orders"
}

test_a_node_read_that_does_not_answer_is_not_measured() {
  local dir=$work/node-fail
  prepare "$dir" node-fail
  run_check "$dir" FAKE_MODE=node-fail
  expect_status "$status" 2 "a node read that does not answer"
  expect_contains "$err" "could NOT measure node-blocks" "a node read that does not answer: it is named as unmeasured"
}

test_a_node_that_is_no_document_is_not_measured() {
  local dir=$work/node-garbage
  prepare "$dir" node-garbage
  run_check "$dir" FAKE_MODE=node-garbage
  expect_status "$status" 2 "a node that is no document"
  expect_contains "$out" "FACT node-blocks: FAIL" "a node that is no document: the row"
}

test_a_missing_pool_argument_is_refused() {
  local dir=$work/no-pool
  prepare "$dir"
  run_check "$dir" CHECK629_POOL=""
  expect_status "$status" 2 "a missing pool"
  expect_contains "$err" "Check 629 needs --pool" "a missing pool: the message"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK629_SANDBOX=""
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "Check 629 needs --sandbox" "a missing sandbox: the message"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  expect_absent "$fugaro_calls" "policy publish" "a path that is not a checkout: no node is published"
}

test_a_doctor_without_a_project_line_is_not_measured() {
  local dir=$work/doctor-no-project
  prepare "$dir" doctor-no-project
  run_check "$dir" FAKE_MODE=doctor-no-project
  expect_status "$status" 2 "a doctor with no project line"
  expect_contains "$err" "could NOT measure installation" "a doctor with no project line: it is named as unmeasured"
  expect_absent "$fugaro_calls" "policy publish" "a doctor with no project line: no node is published"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launch-fails
  prepare "$dir" launch-fails
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_contains "$out" "fugaro run exited 3" "a launch that fails: the row quotes the exit"
  expect_contains "$out" "FACT served-by-second: FAIL" "a launch that fails: the row below it is unmeasured, not passed"
  expect_contains "$err" "could NOT measure terminal, result-json, served-by-second" "a launch that fails: it is named as unmeasured"
}

test_a_run_that_ends_between_polls_settles() {
  local dir=$work/ends-later
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-ends-later
  expect_status "$status" 0 "a run that ends between polls"
  expect_contains "$out" "FACT terminal: PASS" "a run that ends between polls: the second poll is read as the run's end"
  expect_contains "$gcloud_calls" "storage cat" "a run that ends between polls: its record is read"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir" run-never-ends
  run_check "$dir" FAKE_MODE=run-never-ends CHECK629_RUN_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$err" "could NOT measure terminal" "a run that never ends: it is named as unmeasured"
  expect_contains "$out" "had not ended after 2s" "a run that never ends: the row names the bound"
  expect_contains "$out" "is not cancelled by stopping here" "a run that never ends: the row says the run is left alone"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/record-fail
  prepare "$dir" record-fail
  run_check "$dir" FAKE_MODE=record-fail
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT result-json: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "runs/$slug/$run_id/result.json could not be read" "a record that cannot be read: the row names the key"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/record-other-run
  prepare "$dir" record-other-run
  run_check "$dir" FAKE_MODE=record-other-run
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "records run_id=$other_run and this check launched $run_id" "a record of another run: the row prints both"
}

test_a_record_that_is_no_document_is_a_finding() {
  local dir=$work/record-garbage
  prepare "$dir" record-garbage
  printf 'not a document\n' >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 1 "a record that is no document"
  expect_contains "$out" "is not a document this check could read" "a record that is no document: the row"
}

test_the_move_from_the_record_alone_passes() {
  local dir=$work/record-switch
  prepare "$dir" audit-second-only
  run_check "$dir" FAKE_MODE=audit-second-only
  expect_status "$status" 0 "the move read from the record alone"
  expect_contains "$out" "FACT served-by-second: PASS" "the move read from the record alone: the second member served"
  expect_contains "$out" "FACT first-exhausted: PASS" "the move read from the record alone: the first member was left"
  expect_contains "$out" "carries the move off the pool's first member: $first_member->$second_member(exhausted)" "the move read from the record alone: the switch log is what says it"
}

test_an_audit_read_that_does_not_answer_still_reads_the_record() {
  local dir=$work/audit-fail
  prepare "$dir" audit-fail
  run_check "$dir" FAKE_MODE=audit-fail
  expect_status "$status" 2 "an audit read that does not answer"
  expect_contains "$err" "could NOT measure served-by-second" "an audit read that does not answer: it is named as unmeasured"
  expect_contains "$out" "FACT first-exhausted: PASS" "an audit read that does not answer: the record still shows the move"
}

test_an_audit_with_no_forward_line_is_a_finding() {
  local dir=$work/audit-none
  prepare "$dir" audit-none
  run_check "$dir" FAKE_MODE=audit-none
  expect_status "$status" 1 "an audit with no forward line"
  expect_contains "$out" "FACT served-by-second: FAIL" "an audit with no forward line: the row"
  expect_contains "$out" "wrote no forward audit line for $run_id" "an audit with no forward line: the row says what is missing"
  expect_contains "$out" "FACT first-exhausted: PASS" "an audit with no forward line: the record still shows the move"
}

test_a_first_member_that_answered_too_is_a_finding() {
  local dir=$work/audit-first-ok
  prepare "$dir" audit-first-ok
  write_record "$dir/json" record-no-switch
  run_check "$dir" FAKE_MODE=audit-first-ok
  expect_status "$status" 1 "a first member that answered too"
  expect_contains "$out" "FACT served-by-second: FAIL" "a first member that answered too: the serving row"
  expect_contains "$out" "the credentials that answered $run_id are $first_member" "a first member that answered too: the row names the member that did answer"
  expect_contains "$out" "FACT first-exhausted: FAIL" "a first member that answered too: the exhaustion row"
  expect_contains "$out" "name the pool's first member $first_member with status ok" "a first member that answered too: the row says the first member answered"
}

test_a_run_with_no_trace_of_the_first_member_is_a_finding() {
  local dir=$work/audit-second-only
  prepare "$dir" audit-second-only
  write_record "$dir/json" record-no-switch
  run_check "$dir" FAKE_MODE=audit-second-only
  expect_status "$status" 1 "a run with no trace of the first member"
  expect_contains "$out" "FACT served-by-second: PASS" "a run with no trace of the first member: the second member did serve"
  expect_contains "$out" "FACT first-exhausted: FAIL" "a run with no trace of the first member: the row"
  expect_contains "$out" "carry any trace of the pool's first member $first_member" "a run with no trace of the first member: the row says what is missing"
}

test_a_record_carrying_craft_credentials_is_measured() {
  local dir=$work/record-craft-credentials
  prepare "$dir" record-craft-credentials
  run_check "$dir"
  expect_status "$status" 0 "a record carrying craft_credentials"
  expect_contains "$out" "FACT craft-credentials: PASS" "a record carrying craft_credentials: the row"
  expect_contains "$out" "carries craft_credentials naming $second_member" "a record carrying craft_credentials: the row names the member"
}

test_craft_credentials_naming_another_member_is_a_finding() {
  local dir=$work/record-craft-credentials-other
  prepare "$dir" record-craft-credentials-other
  run_check "$dir"
  expect_status "$status" 1 "craft_credentials naming another member"
  expect_contains "$out" "FACT craft-credentials: FAIL" "craft_credentials naming another member: the row"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing" "a traced shell: the message"
  expect_contains "$out" "it without -x" "a traced shell: the message says what to do"
  expect_absent "$fugaro_calls" "policy publish" "a traced shell: no node is published"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$out" "CLAUDECODE is set" "a coding agent's session: the marker is named"
  expect_absent "$fugaro_calls" "policy publish" "a coding agent's session: no node is published"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK629_POOL=belong-pool
  expect_status "$status" 1 "a pool naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a pool naming belong: the row"
  expect_contains "$out" "names belong" "a pool naming belong: the message says so by name"
  expect_absent "$fugaro_calls" "policy publish" "a pool naming belong: no node is published"
}

test_an_installation_naming_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir" doctor-belong
  run_check "$dir" FAKE_MODE=doctor-belong
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "belongs to belong-sandbox" "an installation naming belong: the message names the project"
  expect_absent "$fugaro_calls" "policy publish" "an installation naming belong: no node is published"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK629_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$out" "is still there, at $scratch" "a kept scratch directory: the row names the directory"
  expect_contains "$err" "CHECK629_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: the directory was removed anyway"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
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
t test_the_project_flag_is_passed_through
t test_a_pool_the_config_does_not_declare_is_a_finding
t test_a_pool_of_one_member_is_a_finding
t test_a_member_the_config_does_not_declare_is_a_finding
t test_a_credentials_read_that_does_not_answer_is_not_measured
t test_a_pools_read_that_does_not_answer_is_not_measured
t test_a_credentials_listing_with_no_document_is_not_measured
t test_a_publish_that_refuses_is_a_finding
t test_a_publish_that_does_not_finish_is_not_measured
t test_a_node_missing_a_block_is_a_finding
t test_a_node_credential_missing_a_member_is_a_finding
t test_a_node_with_no_crafts_is_a_finding
t test_a_node_whose_pool_walks_another_order_is_a_finding
t test_a_node_read_that_does_not_answer_is_not_measured
t test_a_node_that_is_no_document_is_not_measured
t test_a_missing_pool_argument_is_refused
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_doctor_without_a_project_line_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_run_that_ends_between_polls_settles
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_record_that_is_no_document_is_a_finding
t test_the_move_from_the_record_alone_passes
t test_an_audit_read_that_does_not_answer_still_reads_the_record
t test_an_audit_with_no_forward_line_is_a_finding
t test_a_first_member_that_answered_too_is_a_finding
t test_a_run_with_no_trace_of_the_first_member_is_a_finding
t test_a_record_carrying_craft_credentials_is_measured
t test_craft_credentials_naming_another_member_is_a_finding
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_an_installation_naming_belong_stops_the_check
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check629_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check629_test.sh: ok"
