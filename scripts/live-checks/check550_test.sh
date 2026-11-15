#!/usr/bin/env bash
# Tests for scripts/live-checks/check550.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a run or an account, no agent
# runs a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one installation: the fake `fugaro` answers the
# calls the check makes (version, `accounts alias --help`, `accounts ls --json`,
# the two `settings get` reads, `doctor --json`, the two launches, the runs list
# and `accounts alias set`), the fake `gcloud` serves the two runs' records, and
# the alias the installation holds is a file the fake reads and writes, so the
# repoint and the move back are readings of state rather than of a script's own
# echo. The cases that matter are the ones where that model is bent: a build
# older than the release, a block with no alias, an alias whose target is not
# admitted, no second account to repoint onto, a choice key that names the
# account rather than the alias, a run that ended before the repoint landed, a
# repoint that refuses, a repoint that exits 0 and writes nothing, a record whose
# review stage was served by the NEW account (the claim failing), a record whose
# review stage was served by the OLD account after the repoint (the repoint not
# reaching new launches), and a move back that does not land.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check550_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check550.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK550_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK550_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK550_TEST_TIMEOUT:-60}

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

project=fugaro-chk550-test
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
run_id=run-20261114131000-1
run2_id=run-20261114131000-2
# The alias the block declares and the two accounts it can stand for: the one it
# stands for when the check starts, and the one a repoint moves it onto.
alias_name=fast
team_a=team-a
team_b=team-b

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check550.sh's test only. It
# answers the account read the check makes and serves the run record at the key
# the check built, out of the case's own directory. Nothing here reaches a real
# project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk550-owner@belong.example\n' ;;
    *) printf 'chk550-owner@example.com\n' ;;
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
      printf 'not a record at all\n'
      exit 0
      ;;
    esac
    key=${3:-}
    dir=$(dirname "${key#*runs/}")
    id=${dir##*/}
    n=${id##*-}
    if [ "$mode" = "record-of-another-run" ]; then n=other; fi
    cat "$FAKE_JSON_DIR/record-$n.json"
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

write_fake_fugaro() { # write_fake_fugaro DIR [DEST]: DEST is where the fake is
  # written, `$1/bin/fugaro` by default — a case that needs to wrap it writes
  # the real fake beside its own wrapper.
  mkdir -p "$1/bin"
  cat >"${2:-$1/bin/fugaro}" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check550.sh's test only. The
# `--json` reads come from files the case wrote, `accounts alias --help` prints
# the usage line the real command prints (the check's offline feature probe),
# `accounts alias set` moves the alias in the case's own state directory, and
# every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
[ -f "$FAKE_STATE/alias" ] || printf '%s' "${FAKE_ALIAS_START:-team-a}" >"$FAKE_STATE/alias"
[ -f "$FAKE_STATE/launches" ] || printf '0' >"$FAKE_STATE/launches"

case "${1:-}" in
version)
  case "$mode" in
  old-version) printf '0.24.0\n' ;;
  dev-build) printf 'dev-9f8e7d6\n' ;;
  *) printf '0.25.0\n' ;;
  esac
  ;;
accounts)
  case "${2:-}" in
  alias)
    case "${3:-}" in
    --help)
      case "$mode" in
      no-feature) printf 'Usage:\n  fugaro accounts alias ls\n' ;;
      *) printf 'Usage:\n  fugaro accounts alias set NAME ACCOUNT\n  fugaro accounts alias rm NAME\n' ;;
      esac
      ;;
    set)
      case "$mode" in
      repoint-fails)
        printf 'fugaro: the alias was refused\n' >&2
        exit 1
        ;;
      repoint-hangs)
        # Only the repoint hangs: the move back at the end of the check is a
        # second call to the same command, and a fake that hung on both would
        # decide the restore row as well.
        if [ ! -f "$FAKE_STATE/hung" ]; then
          : >"$FAKE_STATE/hung"
          sleep 300
        fi
        printf '%s' "${5:-}" >"$FAKE_STATE/alias"
        ;;
      repoint-lies)
        : # exits 0 and writes nothing: the command reported a move it did not make
        ;;
      *)
        printf '%s' "${5:-}" >"$FAKE_STATE/alias"
        ;;
      esac
      printf 'aliases: %s -> %s\n' "${4:-}" "${5:-}"
      ;;
    *)
      printf 'fugaro: unexpected alias subcommand %s\n' "${3:-}" >&2
      exit 1
      ;;
    esac
    ;;
  ls)
    case "$mode" in
    accounts-unreadable)
      printf 'accounts: this is not a document\n'
      ;;
    *)
      sed -e "s/@ALIAS@/$(cat "$FAKE_STATE/alias")/" \
        -e "s/@COMPLETE@/$([ "$mode" = accounts-incomplete ] && printf false || printf true)/" \
        "$FAKE_JSON_DIR/accounts.json"
      ;;
    esac
    ;;
  *)
    printf 'fugaro: unexpected accounts subcommand %s\n' "${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
settings)
  case "${2:-}${3:-}" in
  getstages.review.claude_account) cat "$FAKE_JSON_DIR/settings-stages.txt" ;;
  getdefault_claude_account) cat "$FAKE_JSON_DIR/settings-default.txt" ;;
  *)
    printf 'fugaro: unexpected settings read %s %s\n' "${2:-}" "${3:-}" >&2
    exit 1
    ;;
  esac
  ;;
doctor)
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
  n=$(( $(cat "$FAKE_STATE/launches") + 1 ))
  printf '%s' "$n" >"$FAKE_STATE/launches"
  if [ "$mode" = "run2-fails" ] && [ "$n" = "2" ]; then
    printf 'fugaro: the launch was refused\n' >&2
    exit 1
  fi
  if [ "$mode" = "launch-same-run" ]; then n=1; fi
  case "$mode" in
  launch-names-no-run)
    printf '{"project":"%s","repo":"acme/sandbox","status":"launched"}\n' "${FAKE_PROJECT:-}"
    ;;
  *)
    printf '{"project":"%s","repo":"acme/sandbox","run_id":"run-20261114131000-%s","branch":"fugaro/20261114-131000-abcd","status":"launched"}\n' "${FAKE_PROJECT:-}" "$n"
    ;;
  esac
  ;;
runs)
  id=$(cat "$FAKE_STATE/launches")
  polls=$(( $(cat "$FAKE_STATE/polls.$id" 2>/dev/null || printf '0') + 1 ))
  printf '%s' "$polls" >"$FAKE_STATE/polls.$id"
  term=false
  stage=implement
  case "$mode" in
  run-never-stages)
    stage=
    ;;
  run-ended-early)
    term=true
    stage=
    ;;
  run-never-ends)
    : # in a stage, and never terminal
    ;;
  run2-never-ends)
    if [ "$id" = "1" ] && [ "$polls" -ge 3 ]; then term=true; stage=; fi
    ;;
  *)
    if [ "$polls" -ge 3 ]; then term=true; stage=; fi
    ;;
  esac
  printf '{"project":"%s","runs":[{"run":"%s/run-20261114131000-%s","repo":"acme/sandbox","run_id":"run-20261114131000-%s","status":"%s","stage":"%s","terminal":%s}],"totals":{"runs":1}}\n' \
    "${FAKE_PROJECT:-}" "${FAKE_SLUG:-}" "$id" "$id" "$([ "$term" = true ] && printf succeeded || printf running)" "$stage" "$term"
  ;;
*)
  printf 'fugaro: unexpected subcommand %s\n' "${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "${2:-$1/bin/fugaro}"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  write_doctor "$dir"
  write_accounts "$dir" "$alias_name" "$team_a"
  write_settings "$dir" "$alias_name"
  cat >"$json/record-1.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","outcome":"ready","oauth_account":"$team_a","stages":[{"name":"implement","model":"claude-opus-4-1"},{"name":"review","model":"claude-opus-4-1"}],"stage_accounts":{"implement":"$team_a","review":"$team_a"},"cost":{"model_usd":0.52}}
JSON
  cat >"$json/record-2.json" <<JSON
{"version":1,"run_id":"$run2_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","outcome":"ready","oauth_account":"$team_b","stages":[{"name":"implement","model":"claude-opus-4-1"},{"name":"review","model":"claude-opus-4-1"}],"stage_accounts":{"implement":"$team_b","review":"$team_b"},"cost":{"model_usd":0.52}}
JSON
  cat >"$json/record-other.json" <<JSON
{"version":1,"run_id":"run-20260101000000-9","repo":"acme/sandbox","status":"succeeded","stages":[],"stage_accounts":{}}
JSON
}

write_doctor() { # write_doctor DIR
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk550","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_accounts DIR ALIAS TARGET: the block, as `accounts ls --json` prints it.
# The alias's target is the `@ALIAS@` the fake substitutes the installation's
# own state into, so a repoint really changes what the check reads back.
write_accounts() { # write_accounts DIR ALIAS TARGET
  cat >"$1/json/accounts.json" <<JSON
{"version":1,"repo":"acme/sandbox","accounts":["default","$team_a","$team_b"],"complete":@COMPLETE@,"aliases":{"$2":"@ALIAS@"}}
JSON
}

# write_settings DIR ALIAS: what the two `settings get` reads print. The real
# command prints the key, the value and the layer; only the value matters here.
write_settings() { # write_settings DIR ALIAS
  cat >"$1/json/settings-stages.txt" <<TXT
stages.review.claude_account = $2
layer: local
TXT
  cat >"$1/json/settings-default.txt" <<TXT
default_claude_account = default
layer: local
TXT
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk550\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
  reset_state "$1"
}

# reset_state DIR: the installation back to the state the check starts on — the
# alias on its first account, no launch made and no poll counted — so a case
# that runs the check twice reads the same fixture both times.
reset_state() { # reset_state DIR
  printf '%s' "$team_a" >"$1/state/alias"
  printf '0' >"$1/state/launches"
  rm -f "$1/state/polls."* "$1/state/moved" "$1/state/hung"
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
    FAKE_JSON_DIR="$dir/json" FAKE_STATE="$dir/state" \
    FAKE_PROJECT="$project" FAKE_SLUG="$slug" \
    CHECK550_SANDBOX="$dir/sandbox" CHECK550_YES=1 \
    CHECK550_RUN_TIMEOUT_SECONDS=10 CHECK550_POLL_SECONDS=1 \
    CHECK550_IMPLEMENT_WAIT_SECONDS=3 \
    CHECK550_CLI_TIMEOUT_SECONDS=20 CHECK550_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK550_REPOINT_TIMEOUT_SECONDS=20 CHECK550_DOCTOR_TIMEOUT_SECONDS=20 \
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

assert_no_repoint() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "accounts alias set" "$2: the alias is not moved"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check550.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that two runs are billable and that the check is theirs.
  expect_contains "$out" "TWO sandbox runs and what their tasks cost" "--help: the cost of the two runs is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK550_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 550 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 550 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT premise: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT kept-account: INFO — planned" "dry run: the claim's row is planned too"
  expect_contains "$out" "TWO sandbox runs" "dry run: the plan names the spend"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "accounts ls" "dry run: the block is not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
  assert_no_run "$dir" "dry run"
  assert_no_repoint "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT premise: PASS" "happy path: the premise"
  expect_contains "$out" "${alias_name} stands for ${team_a}" "happy path: the alias and the account it stands for"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the first launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT repointed: PASS" "happy path: the repoint"
  expect_contains "$out" "moved ${alias_name} from ${team_a} to ${team_b} while ${run_id} was in 'implement'" "happy path: what the repoint did, and under what"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the first run ended"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the first record"
  expect_contains "$out" "FACT kept-account: PASS" "happy path: the claim"
  expect_contains "$out" "review stage was served by ${team_a}" "happy path: the launch's own account survived"
  expect_contains "$out" "FACT relaunched: PASS" "happy path: the second launch"
  expect_contains "$out" "launched ${run2_id} on acme/sandbox after the alias moved to ${team_b}" "happy path: the second run"
  expect_contains "$out" "FACT relaunch-record: PASS" "happy path: the second record"
  expect_contains "$out" "FACT new-target: PASS" "happy path: the repoint moved new launches"
  expect_contains "$out" "review stage was served by ${team_b}" "happy path: the new target"
  expect_contains "$out" "FACT restore: PASS" "happy path: the move back"
  expect_contains "$out" "CHECK 550 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, the alias was moved twice
  # (onto the new account and back), and the records were read at the run's own
  # key — the one-segment slug, not the owner/name the launch names.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "accounts ls --json (in $dir/sandbox)" "happy path: the block is read through the checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the runs are launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the runs are followed by their own slug"
  expect_contains "$calls" "accounts alias set $alias_name $team_b --yes (in $dir/sandbox)" "happy path: the repoint is one command, through the checkout"
  expect_contains "$calls" "accounts alias set $alias_name $team_a --yes (in $dir/sandbox)" "happy path: the move back is the same command"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${run_id}/result.json" "happy path: the first record is read at its own key"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${run2_id}/result.json" "happy path: the second record is read at its own key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  [ "$(cat "$dir/state/alias")" = "$team_a" ] || fail "happy path: the alias was not put back (it reads $(cat "$dir/state/alias"))"
  assert_scratch_removed "$dir" "happy path"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/notacheckout
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK550_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the refusal"
  assert_no_run "$dir" "a path that is not a checkout"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a tracing shell"
  expect_contains "$err" "this shell is tracing" "a tracing shell: the refusal"
  assert_no_run "$dir" "a tracing shell"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the refusal names the marker"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: why"
  assert_no_run "$dir" "a coding agent's session"
  assert_no_repoint "$dir" "a coding agent's session"
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
  printf '{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk550-belong","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "a project naming belong: the refusal"
  assert_no_run "$dir" "a project naming belong"
}

test_yes_is_required() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK550_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "asks for --yes" "no --yes: the refusal"
  expect_contains "$err" "Nothing has been launched and nothing has been moved" "no --yes: what it did not do"
  assert_no_run "$dir" "no --yes"
  assert_no_repoint "$dir" "no --yes"
  assert_scratch_removed "$dir" "no --yes"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/oldbuild
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "a build older than the release"
  expect_contains "$out" "FACT fugaro-version: PASS — 0.24.0" "a build older than the release: the version it read"
  expect_contains "$out" "FACT release: FAIL" "a build older than the release: the gate"
  expect_contains "$out" "Run 'fugaro update' at your terminal" "a build older than the release: the one command that fixes it"
  assert_no_run "$dir" "a build older than the release"
}

test_a_build_without_the_command_is_refused() {
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "a build without the command"
  expect_contains "$out" "FACT release: FAIL" "a build without the command: the gate"
  expect_contains "$err" "has no 'accounts alias'" "a build without the command: the refusal"
  assert_no_run "$dir" "a build without the command"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/devbuild
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build CHECK550_RELEASE=0.25.0
  expect_status "$status" 0 "a dev build"
  expect_contains "$out" "FACT fugaro-version: PASS — dev-9f8e7d6" "a dev build: the version it read"
  expect_contains "$out" "not a release number" "a dev build: the probe decides"
  expect_contains "$out" "FACT release: PASS" "a dev build: the gate passes on the probe"
}

test_a_block_with_no_alias_is_a_finding() {
  local dir=$work/noalias
  prepare "$dir"
  printf '{"version":1,"repo":"acme/sandbox","accounts":["default","%s","%s"],"complete":true,"aliases":{}}\n' "$team_a" "$team_b" >"$dir/json/accounts.json"
  run_check "$dir"
  expect_status "$status" 1 "a block with no alias"
  expect_contains "$out" "FACT premise: FAIL" "a block with no alias: the premise"
  expect_contains "$out" "declares no alias at all" "a block with no alias: why"
  assert_no_run "$dir" "a block with no alias"
  assert_no_repoint "$dir" "a block with no alias"
}

test_an_alias_the_block_does_not_declare_is_a_finding() {
  local dir=$work/unknownalias
  prepare "$dir"
  run_check "$dir" CHECK550_ALIAS=ghost
  expect_status "$status" 1 "an alias the block does not declare"
  expect_contains "$out" "FACT premise: FAIL" "an alias the block does not declare: the premise"
  expect_contains "$out" "declares no alias named 'ghost'" "an alias the block does not declare: why"
  assert_no_run "$dir" "an alias the block does not declare"
}

test_an_alias_whose_target_is_not_admitted_is_a_finding() {
  local dir=$work/notadmitted
  prepare "$dir"
  printf '{"version":1,"repo":"acme/sandbox","accounts":["default","%s"],"complete":true,"aliases":{"%s":"team-z"}}\n' "$team_a" "$alias_name" >"$dir/json/accounts.json"
  run_check "$dir"
  expect_status "$status" 1 "an alias whose target is not admitted"
  expect_contains "$out" "FACT premise: FAIL" "an alias whose target is not admitted: the premise"
  expect_contains "$out" "which is not one of the accounts this repository may choose among" "an alias whose target is not admitted: why"
  assert_no_run "$dir" "an alias whose target is not admitted"
}

test_no_second_account_to_repoint_onto_is_a_finding() {
  local dir=$work/oneaccount
  prepare "$dir"
  # `default` is always a candidate, so the only way this repository has no
  # second account to repoint onto is for the alias to stand for `default`
  # already and for no named account to be admitted beside it.
  printf '{"version":1,"repo":"acme/sandbox","accounts":["default"],"complete":true,"aliases":{"%s":"default"}}\n' "$alias_name" >"$dir/json/accounts.json"
  run_check "$dir"
  expect_status "$status" 1 "no second account"
  expect_contains "$out" "FACT premise: FAIL" "no second account: the premise"
  expect_contains "$out" "may choose no account but" "no second account: why"
  assert_no_run "$dir" "no second account"
}

test_an_account_to_repoint_onto_that_is_not_admitted_is_a_finding() {
  local dir=$work/targetnotadmitted
  prepare "$dir"
  run_check "$dir" CHECK550_TARGET=team-z
  expect_status "$status" 1 "a target that is not admitted"
  expect_contains "$out" "FACT premise: FAIL" "a target that is not admitted: the premise"
  expect_contains "$out" "is not one this repository may choose among" "a target that is not admitted: why"
  assert_no_run "$dir" "a target that is not admitted"
}

test_a_target_the_alias_already_stands_for_is_a_finding() {
  local dir=$work/sametarget
  prepare "$dir"
  run_check "$dir" CHECK550_TARGET="$team_a"
  expect_status "$status" 1 "the alias's own target"
  expect_contains "$out" "FACT premise: FAIL" "the alias's own target: the premise"
  expect_contains "$out" "a repoint onto the same account moves nothing" "the alias's own target: why"
  assert_no_run "$dir" "the alias's own target"
}

test_a_block_that_could_not_be_read_completely_is_not_measured() {
  local dir=$work/incomplete
  prepare "$dir"
  run_check "$dir" FAKE_MODE=accounts-incomplete
  expect_status "$status" 2 "a block that is not complete"
  expect_contains "$out" "FACT premise: FAIL" "a block that is not complete: the row"
  expect_contains "$out" "complete=false" "a block that is not complete: why"
  assert_no_run "$dir" "a block that is not complete"
}

test_a_block_that_is_not_a_document_is_not_measured() {
  local dir=$work/unreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=accounts-unreadable
  expect_status "$status" 2 "a block that is not a document"
  expect_contains "$out" "FACT premise: FAIL" "a block that is not a document: the row"
  expect_contains "$out" "could not read" "a block that is not a document: why"
  assert_no_run "$dir" "a block that is not a document"
}

test_a_choice_key_that_names_the_account_is_a_finding() {
  local dir=$work/nokey
  prepare "$dir"
  write_settings "$dir" "$team_a"
  run_check "$dir"
  expect_status "$status" 1 "a choice key that names the account"
  expect_contains "$out" "FACT premise: FAIL" "a choice key that names the account: the premise"
  expect_contains "$out" "no choice key names the alias" "a choice key that names the account: why"
  assert_no_run "$dir" "a choice key that names the account"
}

test_a_repository_the_check_was_not_asked_about_is_a_finding() {
  local dir=$work/wrongrepo
  prepare "$dir"
  run_check "$dir" CHECK550_REPO=acme/other
  expect_status "$status" 1 "a pinned repository that does not match"
  expect_contains "$out" "FACT premise: FAIL" "a pinned repository: the premise"
  expect_contains "$out" "CHECK550_REPO names acme/other" "a pinned repository: why"
  assert_no_run "$dir" "a pinned repository"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_contains "$out" "the check needs a run to repoint under" "a launch that fails: why"
  assert_no_repoint "$dir" "a launch that fails"
}

test_a_launch_that_names_no_run_is_a_finding() {
  local dir=$work/norun
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-names-no-run
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: why"
  assert_no_repoint "$dir" "a launch that names no run"
}

test_a_run_that_never_reaches_a_stage_is_not_measured() {
  local dir=$work/neversages
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-never-stages
  expect_status "$status" 2 "a run that never reaches a stage"
  expect_contains "$out" "FACT repointed: FAIL" "a run that never reaches a stage: the row"
  expect_contains "$out" "was still in no stage after" "a run that never reaches a stage: why"
  # And no second run: a launch after a repoint that never landed would resolve
  # the same account as the first, and would spend the sandbox's money saying so.
  expect_contains "$out" "FACT relaunched: FAIL — nothing was launched" "a run that never reaches a stage: no second launch"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "run --repo" "a run that never reaches a stage: the first run was launched"
  [ "$(grep -c 'run --repo' "$dir/logs/fugaro.calls")" = "1" ] || fail "a run that never reaches a stage: a second run was launched"
  assert_no_repoint "$dir" "a run that never reaches a stage"
}

test_a_run_that_ended_before_the_repoint_is_a_finding() {
  local dir=$work/endedearly
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-ended-early
  expect_status "$status" 1 "a run that ended too soon"
  expect_contains "$out" "FACT repointed: FAIL" "a run that ended too soon: the row"
  expect_contains "$out" "had already ended when the alias was to be moved" "a run that ended too soon: why"
  assert_no_repoint "$dir" "a run that ended too soon"
}

test_a_repoint_that_fails_is_a_finding() {
  local dir=$work/repointfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=repoint-fails
  expect_status "$status" 1 "a repoint that fails"
  expect_contains "$out" "FACT repointed: FAIL" "a repoint that fails: the row"
  expect_contains "$out" "exited 1" "a repoint that fails: why"
}

test_a_repoint_that_writes_nothing_is_a_finding() {
  local dir=$work/repointlies
  prepare "$dir"
  run_check "$dir" FAKE_MODE=repoint-lies
  expect_status "$status" 1 "a repoint that writes nothing"
  expect_contains "$out" "FACT repointed: FAIL" "a repoint that writes nothing: the row"
  expect_contains "$out" "the command reported a move the installation does not hold" "a repoint that writes nothing: why"
}

test_a_repoint_that_never_answers_is_not_measured() {
  local dir=$work/repointhangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=repoint-hangs CHECK550_REPOINT_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a repoint that hangs"
  expect_contains "$out" "FACT repointed: FAIL" "a repoint that hangs: the row"
  expect_contains "$out" "did not finish inside 2s" "a repoint that hangs: why"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/neverends
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-never-ends
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT repointed: PASS" "a run that never ends: the repoint still landed"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ends: the row"
  expect_contains "$out" "had not ended after" "a run that never ends: why"
  # The alias is still put back: a run that outlives the check is not a reason
  # to leave the owner's installation on a target the check chose.
  expect_contains "$out" "FACT restore: PASS" "a run that never ends: the move back"
  [ "$(cat "$dir/state/alias")" = "$team_a" ] || fail "a run that never ends: the alias was not put back"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT result-json: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read (exit 1)" "a record that cannot be read: why"
}

test_a_record_that_is_not_a_document_is_a_finding() {
  local dir=$work/badrecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-unreadable
  expect_status "$status" 1 "a record that is not a document"
  expect_contains "$out" "FACT result-json: FAIL" "a record that is not a document: the row"
  expect_contains "$out" "is not a document this check could read" "a record that is not a document: why"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/otherrecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-of-another-run
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20260101000000-9" "a record of another run: why"
}

test_a_repoint_that_reached_the_run_in_flight_is_a_finding() {
  local dir=$work/reached
  prepare "$dir"
  # The claim failing: the run was launched on team-a and its review stage was
  # served by team-b — the account the alias was moved to while it was working.
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","outcome":"ready","stages":[{"name":"implement"},{"name":"review"}],"stage_accounts":{"implement":"%s","review":"%s"}}\n' "$run_id" "$team_b" "$team_b" >"$dir/json/record-1.json"
  run_check "$dir"
  expect_status "$status" 1 "a repoint that reached the run in flight"
  expect_contains "$out" "FACT kept-account: FAIL" "a repoint that reached the run in flight: the claim"
  expect_contains "$out" "did NOT keep the account it was launched on" "a repoint that reached the run in flight: why"
}

test_a_record_without_the_review_stage_is_a_finding() {
  local dir=$work/nostage
  prepare "$dir"
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stages":[{"name":"implement"}],"stage_accounts":{"implement":"%s"}}\n' "$run_id" "$team_a" >"$dir/json/record-1.json"
  run_check "$dir"
  expect_status "$status" 1 "a record with no review stage"
  expect_contains "$out" "FACT kept-account: FAIL" "a record with no review stage: the row"
  expect_contains "$out" "carries no stage_accounts entry for 'review'" "a record with no review stage: why"
}

test_a_second_launch_that_fails_is_a_finding() {
  local dir=$work/run2fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run2-fails
  expect_status "$status" 1 "a second launch that fails"
  expect_contains "$out" "FACT kept-account: PASS" "a second launch that fails: the claim still held"
  expect_contains "$out" "FACT relaunched: FAIL" "a second launch that fails: the row"
  expect_contains "$out" "the repoint left the installation in a state no run starts on" "a second launch that fails: why"
}

test_a_second_launch_that_names_the_same_run_is_a_finding() {
  local dir=$work/samerun
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-same-run
  expect_status "$status" 1 "a second launch naming the first run"
  expect_contains "$out" "FACT relaunched: FAIL" "a second launch naming the first run: the row"
  expect_contains "$out" "the same run as the first" "a second launch naming the first run: why"
}

test_a_second_run_that_never_ends_is_not_measured() {
  local dir=$work/run2neverends
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run2-never-ends
  expect_status "$status" 2 "a second run that never ends"
  expect_contains "$out" "FACT kept-account: PASS" "a second run that never ends: the claim held"
  expect_contains "$out" "FACT relaunch-record: FAIL" "a second run that never ends: the row"
  expect_contains "$out" "${run2_id} had not ended after" "a second run that never ends: why"
}

test_a_repoint_that_did_not_reach_new_launches_is_a_finding() {
  local dir=$work/notreached
  prepare "$dir"
  # The other half of the claim failing: the second run was launched after the
  # alias moved to team-b and its review stage was still served by team-a.
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stages":[{"name":"implement"},{"name":"review"}],"stage_accounts":{"implement":"%s","review":"%s"}}\n' "$run2_id" "$team_a" "$team_a" >"$dir/json/record-2.json"
  run_check "$dir"
  expect_status "$status" 1 "a repoint that did not reach new launches"
  expect_contains "$out" "FACT new-target: FAIL" "a repoint that did not reach new launches: the row"
  expect_contains "$out" "a repoint moves NEW launches" "a repoint that did not reach new launches: why"
}

test_a_move_back_that_does_not_land_is_a_finding() {
  local dir=$work/norestore
  prepare "$dir"
  # The first `accounts alias set` moves the alias; the second — the move back —
  # exits 0 and writes nothing, so the installation is left on the new target.
  write_fake_fugaro "$dir" "$dir/state/real-fugaro"
  cat >"$dir/bin/fugaro" <<'SH'
#!/usr/bin/env bash
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
if [ "${1:-} ${2:-} ${3:-}" = "accounts alias set" ]; then
  if [ -f "$FAKE_STATE/moved" ]; then exit 0; fi
  printf '%s' "${5:-}" >"$FAKE_STATE/alias"
  : >"$FAKE_STATE/moved"
  exit 0
fi
exec "$FAKE_STATE/real-fugaro" "$@"
SH
  chmod +x "$dir/bin/fugaro"
  run_check "$dir"
  expect_status "$status" 1 "a move back that does not land"
  expect_contains "$out" "FACT restore: FAIL" "a move back that does not land: the row"
  expect_contains "$out" "reads ${team_b} after the move back was asked for and exited 0" "a move back that does not land: why"
  expect_contains "$out" "fugaro accounts alias set ${alias_name} ${team_a} --yes" "a move back that does not land: the one command to run"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK550_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK550_KEEP_SCRATCH is set" "a kept scratch directory: why"
  [ -d "$scratch" ] || fail "a kept scratch directory: it was removed anyway"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --nonsense" "an unknown argument: the refusal"
  assert_no_run "$dir" "an unknown argument"
}

test_the_kept_account_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  # The claim failing, and a copy of the script with its comparison replaced by
  # `false`: the copy passes the row the real script fails, which is the
  # assertion above shown not to be vacuous.
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stages":[{"name":"implement"},{"name":"review"}],"stage_accounts":{"implement":"%s","review":"%s"}}\n' "$run_id" "$team_b" "$team_b" >"$dir/json/record-1.json"
  mkdir -p "$dir/copy"
  sed 's/if \[ "\$served" != "\$CURRENT" \]; then/if false; then/' "$script" >"$dir/copy/check550.sh"
  grep -q "if false; then" "$dir/copy/check550.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check550.sh"
  expect_contains "$out" "FACT kept-account: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT kept-account: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed. The fixture is reset first: the
  # run above spent the two launches its fakes serve records for.
  reset_state "$dir"
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT kept-account: FAIL" "the real script: the row"
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
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_yes_is_required
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_command_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_block_with_no_alias_is_a_finding
t test_an_alias_the_block_does_not_declare_is_a_finding
t test_an_alias_whose_target_is_not_admitted_is_a_finding
t test_no_second_account_to_repoint_onto_is_a_finding
t test_an_account_to_repoint_onto_that_is_not_admitted_is_a_finding
t test_a_target_the_alias_already_stands_for_is_a_finding
t test_a_block_that_could_not_be_read_completely_is_not_measured
t test_a_block_that_is_not_a_document_is_not_measured
t test_a_choice_key_that_names_the_account_is_a_finding
t test_a_repository_the_check_was_not_asked_about_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_reaches_a_stage_is_not_measured
t test_a_run_that_ended_before_the_repoint_is_a_finding
t test_a_repoint_that_fails_is_a_finding
t test_a_repoint_that_writes_nothing_is_a_finding
t test_a_repoint_that_never_answers_is_not_measured
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_that_is_not_a_document_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_repoint_that_reached_the_run_in_flight_is_a_finding
t test_a_record_without_the_review_stage_is_a_finding
t test_a_second_launch_that_fails_is_a_finding
t test_a_second_launch_that_names_the_same_run_is_a_finding
t test_a_second_run_that_never_ends_is_not_measured
t test_a_repoint_that_did_not_reach_new_launches_is_a_finding
t test_a_move_back_that_does_not_land_is_a_finding
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_kept_account_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check550_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check550_test.sh: ok"
