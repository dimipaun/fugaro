#!/usr/bin/env bash
# Tests for scripts/live-checks/check421.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a pool or a run, no agent runs a
# live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one installation: the fake `fugaro` answers the
# calls the check makes (version, the four `--help` probes that are the feature
# gate, doctor, `craft ls`, `project policy show`, `creds ls`, N launches and the
# runs list), and the fake `gcloud` serves each run's record from the case's own
# directory, keyed by the run the object name carries. The cases that matter are
# the ones where that model is bent, because each of them is a state the check
# exists to tell apart from a spread: a build without the crafts vocabulary, a
# coder craft that resolves to no pool, a FAILOVER pool (Check 420's reading), a
# pool of one member, a pool whose member the credentials service cannot serve, a
# subscription member with the project's spread switch off, another craft pinned
# to a credential, a member the bundle does not hold, a launch that fails, a run
# that never ends, a record that is another run's, a record that cannot be read, a
# run that failed, and — THE row — a batch of runs the SAME member served.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check421_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check421.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK421_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK421_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK421_TEST_TIMEOUT:-90}

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

project=fugaro-chk421-test
# The two runs the batch launches, in the order the fake's launch counter answers
# them. Each is followed by its own id — the list is read for the run this check
# launched and not by position — and each record is served from the object name.
run1=run-20261114120000-1
run2=run-20261114120000-2
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
# The pool the coder craft's access names, its two members in the order the node
# lists them, and the model the crafts run. Both members are served by the
# credentials service, which is what a rotation can happen between.
pool=pool-spread
first_member=claude-oauth-token-spread
second_member=anthropic-api-key
coder_model=claude-opus-4-1

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check421.sh's test only. It
# answers the account read the check makes and serves each run's record out of
# the case's own directory, keyed by the run id in the object name — the batch has
# more than one, so the object name is the only thing that says which is which.
# Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk421-owner@belong.example\n' ;;
    *) printf 'chk421-owner@example.com\n' ;;
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
      obj=${3:-}
      rid=${obj%/result.json}
      rid=${rid##*/}
      if [ -f "$FAKE_JSON_DIR/record-$rid.json" ]; then
        cat "$FAKE_JSON_DIR/record-$rid.json"
      else
        cat "$FAKE_JSON_DIR/record.json"
      fi
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
# A stand-in for `fugaro`, for scripts/live-checks/check421.sh's test only. The
# `--help` probes are the check's feature gate, the `--json` reads come from files
# the case wrote, and every call is logged with the directory it was made in. The
# launch answers one document per call, counted in a file: the batch launches N
# runs, and each one has to come back as a DIFFERENT run for the check to follow
# N of them at all.
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
  n=1
  if [ -f "$FAKE_LOG_DIR/launch.count" ]; then
    n=$(( $(cat "$FAKE_LOG_DIR/launch.count") + 1 ))
  fi
  printf '%s\n' "$n" >"$FAKE_LOG_DIR/launch.count"
  case "$mode" in
  # The SECOND launch of the batch is refused: the first one is a run the check
  # follows and reads a record for, so a case can say what a short batch does to
  # the rows read over the whole of it.
  run-fails)
    if [ "$n" -ge 2 ]; then
      printf 'fugaro: the launch was refused\n' >&2
      exit 1
    fi
    ;;
  launch-hangs)
    sleep 300
    ;;
  esac
  if [ -f "$FAKE_JSON_DIR/launch$n.json" ]; then
    cat "$FAKE_JSON_DIR/launch$n.json"
  else
    cat "$FAKE_JSON_DIR/launch.json"
  fi
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
  write_policy "$dir" round-robin
  write_creds "$dir" both
  write_launch "$dir" 1 "$run1"
  write_launch "$dir" 2 "$run2"
  write_rows "$dir" both
  write_record "$dir" "$run1" "$first_member" default
  write_record "$dir" "$run2" "$second_member" default
}

write_doctor() { # write_doctor DIR
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk421","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_launch DIR N RUN_ID: the N-th launch the fake answers, which is the run
# the check follows and reads a record for.
write_launch() { # write_launch DIR N RUN_ID
  cat >"$1/json/launch$2.json" <<JSON
{"project":"$project","run":"2000","repo":"acme/sandbox","run_id":"$3","branch":"fugaro/20261114-120000-abcd","execution":"exec-$2","status":"launched"}
JSON
}

# write_rows DIR STATE: `fugaro runs ls --json`'s document. `both` is the batch
# the check reads a spread over, listed with each row's own ref (the one-segment
# slug and the run's id) — the check follows the run it launched by its id and
# takes the record's key from that row. `not-terminal` is a run that is still
# working, which is a reading the check refuses to take.
write_rows() { # write_rows DIR STATE
  local state=$2 second_terminal=true second_status=succeeded
  case "$state" in
  not-terminal)
    second_terminal=false
    second_status=running
    ;;
  esac
  cat >"$1/json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run1","repo":"acme/sandbox","run_id":"$run1","status":"succeeded","stage":"finalize","terminal":true,"settled":true,"branch":"fugaro/20261114-120000-abcd","base_branch":"main"},{"run":"$slug/$run2","repo":"acme/sandbox","run_id":"$run2","status":"$second_status","stage":"implement","terminal":$second_terminal,"settled":$second_terminal,"branch":"fugaro/20261114-120001-efgh","base_branch":"main"}],"totals":{"runs":2},"warnings":[]}
JSON
}

# write_crafts DIR MODEL ACCESS PROBLEMS EXTRA: `craft ls --json`'s document, with
# the coder row the check reads, any other craft's row a case adds beside it (the
# rotation is only taken up when none of them is pinned), and the problems list.
write_crafts() { # write_crafts DIR MODEL ACCESS PROBLEMS [EXTRA]
  local model=$2 access=$3 problems=${4:-[]} extra=${5:-} rows
  rows=$(cat <<JSON
{"craft":"coder","model":"$model","model_from":"repo","access":"$access","access_from":"repo","inert":false}
JSON
)
  if [ -n "$extra" ]; then
    rows="$rows,$extra"
  fi
  cat >"$1/json/crafts.json" <<JSON
{"version":1,"project":"chk421","crafts":[$rows],"problems":$problems}
JSON
}

# write_policy DIR STATE: the published node in the states the check has to tell
# apart. `round-robin` is the state Check 421 measures; every other state is one a
# batch of runs would be a reading of something else in, and the check's own row
# names it.
write_policy() { # write_policy DIR STATE
  local state=$2 spread=true strategy=round-robin members="\"$first_member\",\"$second_member\"" creds pools
  case "$state" in
  round-robin) ;;
  failover) strategy=failover ;;
  one-member) members="\"$first_member\"" ;;
  spread-off) spread=false ;;
  openrouter-member) members="\"$first_member\",\"openrouter-key\"" ;;
  pinned-review)
    cat >"$1/json/policy.json" <<JSON
{"project":"chk421","policy":{"subscription_spread":true,"crafts":{"coder":{"model":"$coder_model","access":"$pool"},"review":{"model":"$coder_model","access":"$second_member"}},"craft_credentials":[{"name":"$first_member","type":"claude-oauth"},{"name":"$second_member","type":"anthropic"}],"craft_pools":[{"name":"$pool","strategy":"round-robin","members":["$first_member","$second_member"]}]}}
JSON
    return 0
    ;;
  unpublished)
    cat >"$1/json/policy.json" <<JSON
{"project":"chk421","policy":null}
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
{"project":"chk421","policy":{"subscription_spread":$spread,"crafts":{"coder":{"model":"$coder_model","access":"$pool"}},"craft_credentials":[$creds],"craft_pools":[{"name":"$pool","strategy":"$strategy","members":[$members]}]}}
JSON
}

# write_policy_single_credential DIR: the coder craft whose access is a CREDENTIAL
# and not a pool — a state a batch of runs measures nothing about a rotation in.
write_policy_single_credential() { # write_policy_single_credential DIR
  cat >"$1/json/policy.json" <<JSON
{"project":"chk421","policy":{"subscription_spread":true,"crafts":{"coder":{"model":"$coder_model","access":"$second_member"}},"craft_credentials":[{"name":"$first_member","type":"claude-oauth"},{"name":"$second_member","type":"anthropic"}]}}
JSON
}

# write_policy_unknown_access DIR: the node's coder access names neither a pool nor
# a credential the node holds.
write_policy_unknown_access() { # write_policy_unknown_access DIR
  cat >"$1/json/policy.json" <<JSON
{"project":"chk421","policy":{"subscription_spread":true,"crafts":{"coder":{"model":"$coder_model","access":"$pool"}},"craft_credentials":[{"name":"$first_member","type":"claude-oauth"},{"name":"$second_member","type":"anthropic"}],"craft_pools":[{"name":"another-pool","strategy":"round-robin","members":["$first_member","$second_member"]}]}}
JSON
}

# write_creds DIR WHICH: the credential names the credentials service's bundle
# holds. `both` is the state a rotation needs; `first-only` is a pool whose second
# member the service cannot hand to a call at all.
write_creds() { # write_creds DIR WHICH
  local rows
  case "$2" in
  first-only) rows="{\"name\":\"$first_member\",\"present\":true,\"changed\":\"2026-11-14T12:00:00Z\"}" ;;
  *) rows="{\"name\":\"$first_member\",\"present\":true,\"changed\":\"2026-11-14T12:00:00Z\"},{\"name\":\"$second_member\",\"present\":true,\"changed\":\"2026-11-14T12:00:00Z\"}" ;;
  esac
  cat >"$1/json/creds.json" <<JSON
{"project":"chk421","secret":"fugaro-creds","version":"7","changed":"2026-11-14T12:00:00Z","credentials":[$rows]}
JSON
}

# write_record DIR RUN_ID MEMBER STATE: one run's record, at `record-<run_id>.json`
# — the object the check reads for that run and no other. `default` is a run whose
# own account and stages name the member it was served by, which is the reading
# every other state is told apart from.
write_record() { # write_record DIR RUN_ID MEMBER STATE
  local dir=$1 rid=$2 member=$3 state=$4 status=succeeded oauth="" stages="{}" switches="[]" craft_creds="" outcome=succeeded
  oauth=$member
  stages="{\"implement\":\"$member\"}"
  case "$state" in
  oauth-only)
    stages="{}"
    ;;
  stage-only)
    oauth=""
    ;;
  switch-only)
    oauth=""
    stages="{}"
    switches="[{\"from\":\"$first_member\",\"to\":\"$member\",\"reason\":\"the pool rotated to the next member\"}]"
    ;;
  craft-credentials)
    # MC8's additive field (D1629) names the credential the craft was served by,
    # and it is read BEFORE the run's own account: the account is who the run
    # ended as, which a rotation inside it need not equal.
    oauth=$first_member
    stages="{\"implement\":\"$first_member\"}"
    craft_creds="{\"coder\":\"$member\"}"
    ;;
  no-member)
    oauth=some-other-credential
    stages="{\"implement\":\"some-other-credential\"}"
    ;;
  failed)
    status=failed
    outcome=failed
    ;;
  esac
  # Every state but `craft-credentials` is a record a build without MC8 writes:
  # the additive field is left out of them entirely, so a case that leans on it
  # is leaning on the fixture rather than on a field the release does not have.
  local cc=""
  if [ "$state" = craft-credentials ]; then cc=",\"craft_credentials\":$craft_creds"; fi
  cat >"$dir/json/record-$rid.json" <<JSON
{"version":1,"run_id":"$rid","repo":"acme/sandbox","status":"$status","outcome":"$outcome","cost":{"model_usd":0.42},"oauth_account":"$oauth","stage_accounts":$stages,"switches":$switches$cc}
JSON
}

# write_checkout DIR: a directory that looks like a Fugaro repository's checkout,
# which is all the check reads before it runs a command in it.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk421\n' >"$1/fugaro.yaml"
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
  # The launch counter is this case's, not the directory's: a case that runs the
  # check twice (the mutation, which runs a copy and then the real script) has to
  # see the batch from its first launch both times.
  rm -f "$dir/logs/launch.count"
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
    CHECK421_SANDBOX="$dir/sandbox" CHECK421_YES=1 CHECK421_RUNS=2 \
    CHECK421_RUN_TIMEOUT_SECONDS=10 CHECK421_POLL_SECONDS=1 \
    CHECK421_CLI_TIMEOUT_SECONDS=20 CHECK421_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK421_DOCTOR_TIMEOUT_SECONDS=20 \
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

# write_record_of_another_run DIR RID: the object the check reads for RID, holding
# another run's record — the state a key built from anything but the run the check
# launched would read.
write_record_of_another_run() { # write_record_of_another_run DIR RID
  cat >"$1/json/record-$2.json" <<JSON
{"version":1,"run_id":"run-20261114000000-9","repo":"acme/sandbox","status":"succeeded","outcome":"succeeded","cost":{"model_usd":0.42},"oauth_account":"$first_member","stage_accounts":{"implement":"$first_member"},"switches":[]}
JSON
}

assert_run_count() { # dir want what
  local got
  got=$(grep -c "run --repo" "$1/logs/fugaro.calls" || true)
  [ "$got" = "$2" ] || fail "$3: expected $2 launch(es), got $got"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check421.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the batch is billable and that the check is theirs.
  expect_contains "$out" "N sandbox runs and what their tasks cost" "--help: the cost of the batch is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "never a value" "--help: nothing secret is printed"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK421_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 421 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: INFO" "dry run: the version row is a reading"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the trace guard"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 421 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT pool: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT spread: INFO — planned" "dry run: the spread row is planned too"
  expect_contains "$err" "2 sandbox runs" "dry run: the plan names the spend"
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
  expect_contains "$out" "goes out on the round-robin pool '${pool}' of ${first_member}, ${second_member}" "happy path: the members it read"
  expect_contains "$out" "FACT bundle: PASS" "happy path: the members are in the bundle"
  expect_contains "$out" "FACT runs-launched: PASS" "happy path: the batch"
  expect_contains "$out" "${run1} ${run2}" "happy path: the runs it launched"
  expect_contains "$out" "FACT runs-ended: PASS" "happy path: the runs ended"
  expect_contains "$out" "FACT records: PASS" "happy path: the records"
  expect_contains "$out" "FACT succeeded: PASS" "happy path: the runs passed"
  expect_contains "$out" "FACT budget: INFO" "happy path: the cost is read and not graded"
  expect_contains "$out" "the 2 runs cost 0.8400 USD of model spend in total" "happy path: the total it added up"
  expect_contains "$out" "FACT spread: PASS" "happy path: THE row"
  expect_contains "$out" "2 different members of '${pool}' served the 2 runs" "happy path: the spread it read"
  expect_contains "$out" "#1→${first_member}(oauth_account) #2→${second_member}(oauth_account)" "happy path: which member each run's own record named"
  expect_contains "$out" "CHECK 421 RESULT END (exit 0)" "happy path: the end line"
  # Both runs were launched, both were followed by their own id, and each record
  # was read at its own key — the one-segment slug, not the owner/name the launch
  # names.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "craft ls --json (in $dir/sandbox)" "happy path: the crafts are read through the checkout"
  expect_contains "$calls" "project policy show --json (in $dir/sandbox)" "happy path: the node is read through the checkout"
  expect_contains "$calls" "creds ls --json (in $dir/sandbox)" "happy path: the bundle is read through the checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the runs are launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the runs are followed by the repository's slug"
  assert_run_count "$dir" 2 "happy path"
  local gcloud
  gcloud=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$gcloud" "gs://fugaro-runs-${project}/runs/${slug}/${run1}/result.json" "happy path: the first run's record"
  expect_contains "$gcloud" "gs://fugaro-runs-${project}/runs/${slug}/${run2}/result.json" "happy path: the second run's record"
  expect_absent "$gcloud" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: the scratch is gone"
  assert_scratch_removed "$dir" "happy path"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK421_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check421.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK421_SANDBOX="$dir/empty"
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
  expect_contains "$err" "check421.sh --sandbox" "bash -x: the command it tells you to type instead"
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
  printf '{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk421-belong","region":"us-east5"}}\n' >"$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "a project naming belong: the refusal"
  assert_no_run "$dir" "a project naming belong"
}

test_yes_is_required() {
  # The batch is billable, so the check asks for --yes before it launches it.
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK421_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "launches 2 sandbox runs" "no --yes: how many it would launch"
  expect_contains "$err" "Nothing has been launched" "no --yes: what it says about the money"
  assert_no_run "$dir" "no --yes"
}

test_a_run_count_below_two_is_refused() {
  # One run has no choice of member, so it cannot show a spread at all: the check
  # refuses the batch rather than reporting a rotation it could not read.
  local dir=$work/onerun
  prepare "$dir"
  run_check "$dir" CHECK421_RUNS=1
  expect_status "$status" 2 "one run"
  expect_contains "$err" "needs at least 2 runs" "one run: the refusal"
  assert_no_run "$dir" "one run"
}

test_a_run_count_that_is_not_a_number_is_refused() {
  local dir=$work/badruns
  prepare "$dir"
  run_check "$dir" CHECK421_RUNS=two
  expect_status "$status" 2 "a run count that is not a number"
  expect_contains "$err" "must be a whole number of runs" "a run count that is not a number: the refusal"
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
  # The default credential is the built-in vertex, and no pool of it exists: the
  # runs here would measure nothing about a rotation.
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

test_a_failover_pool_is_a_finding() {
  # A failover pool hands every run the first member that answers: it is Check
  # 420's reading, not this one's, and the spread row would read one member.
  local dir=$work/failover
  prepare "$dir"
  write_policy "$dir" failover
  run_check "$dir"
  expect_status "$status" 1 "a failover pool"
  expect_contains "$out" "FACT pool: FAIL" "a failover pool: the row"
  expect_contains "$out" "walks its members with strategy 'failover'" "a failover pool: what it found"
  expect_contains "$out" "Check 420's reading" "a failover pool: where that reading lives"
  assert_no_run "$dir" "a failover pool"
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

test_a_member_the_service_cannot_serve_is_a_finding() {
  # An OpenRouter key is served on the runner path, which never reaches the
  # credentials service's counter: a pool holding one cannot be rotated between.
  local dir=$work/openrouter
  prepare "$dir"
  write_policy "$dir" openrouter-member
  run_check "$dir"
  expect_status "$status" 1 "a member the service cannot serve"
  expect_contains "$out" "FACT pool: FAIL" "a member the service cannot serve: the row"
  expect_contains "$out" "a openrouter credential" "a member the service cannot serve: the type it found"
  expect_contains "$out" "never reaches the counter" "a member the service cannot serve: why"
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
  expect_contains "$out" "no members to rotate between" "a craft on a single credential: what it means"
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

test_a_craft_pinned_to_another_access_is_a_finding() {
  # MC7's own rule (internal/credsvc/roundrobin.go): the wire carries no craft, so
  # one call's candidate list is the union of the crafts' accesses, and a rotation
  # is taken up only when EVERY craft that contributed goes out on a pool asking
  # for it. Here the review craft sits on a single credential.
  local dir=$work/pinned
  prepare "$dir"
  write_crafts "$dir" "$coder_model" "$pool" "" "{\"craft\":\"review\",\"model\":\"$coder_model\",\"model_from\":\"project\",\"access\":\"$second_member\",\"access_from\":\"project\",\"inert\":false}"
  write_policy "$dir" pinned-review
  run_check "$dir"
  expect_status "$status" 1 "a craft pinned to another access"
  expect_contains "$out" "FACT pool: FAIL" "a craft pinned to another access: the row"
  expect_contains "$out" "asks for the rotation, and this call would not get it" "a craft pinned to another access: what it found"
  expect_contains "$out" "review goes out on the single credential" "a craft pinned to another access: which craft"
  assert_no_run "$dir" "a craft pinned to another access"
}

test_a_craft_on_vertex_is_named_and_is_not_a_pin() {
  # A craft on the built-in vertex contributes no candidate to that list: it stops
  # nothing, and the row names it so the reading is not taken on trust.
  local dir=$work/vertexreview
  prepare "$dir"
  write_crafts "$dir" "$coder_model" "$pool" "" "{\"craft\":\"review\",\"model\":\"$coder_model\",\"model_from\":\"project\",\"access\":\"vertex\",\"access_from\":\"project\",\"inert\":false}"
  run_check "$dir"
  expect_status "$status" 0 "a craft on vertex"
  expect_contains "$out" "FACT pool: PASS" "a craft on vertex: the row"
  expect_contains "$out" "contributes nothing from: review=vertex" "a craft on vertex: it is named as the one that contributes nothing"
  expect_contains "$out" "FACT spread: PASS" "a craft on vertex: the batch still reads"
}

test_a_member_missing_from_the_bundle_is_a_finding() {
  # A member the bundle does not hold is one the service cannot hand to a call at
  # all: the pool would be refused rather than rotated.
  local dir=$work/nobundle
  prepare "$dir"
  write_creds "$dir" first-only
  run_check "$dir"
  expect_status "$status" 1 "a member missing from the bundle"
  expect_contains "$out" "FACT bundle: FAIL" "a member missing from the bundle: the row"
  expect_contains "$out" "does not hold ${second_member}" "a member missing from the bundle: which one"
  expect_contains "$out" "fugaro creds set <member>" "a member missing from the bundle: the remedy"
  # The bundle is part of the premise, so nothing is launched: a batch on a pool
  # the service cannot hand in full would be refused rather than rotated. The row
  # above it is a finding about the installation and not a reading the check could
  # not take, so the block ends 1 rather than 2.
  expect_contains "$out" "FACT runs-launched: FAIL" "a member missing from the bundle: the row that follows"
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
  # The FIRST launch succeeds and its record is read; the second is refused. The
  # batch is short, so every row read over the whole of it is one the check could
  # not take — and the block still ends 1, on the finding that stopped it.
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "a launch that failed"
  expect_contains "$out" "FACT runs-launched: FAIL" "a launch that failed: the row"
  expect_contains "$out" "run 2 of 2 could not be launched" "a launch that failed: which run it was"
  expect_contains "$out" "fugaro run exited 1" "a launch that failed: the exit it saw"
  expect_contains "$out" "FACT spread: FAIL" "a launch that failed: the spread cannot be read"
  expect_contains "$out" "there is no whole batch to read a spread over" "a launch that failed: why the row is not a reading"
  # The first run is the check's own and was read: a short batch is short of the
  # second one, not of both.
  expect_contains "$out" "FACT runs-launched: FAIL" "a launch that failed: the batch row"
  assert_run_count "$dir" 2 "a launch that failed"
}

test_a_launch_that_names_no_run_is_a_finding() {
  # The check follows the run it launched and would read its record at the key
  # that run's row names: a launch that names no run can be neither followed nor
  # read.
  local dir=$work/norun
  prepare "$dir"
  rm -f "$dir/json/launch2.json"
  printf '{"project":"%s","run":"2000","repo":"acme/sandbox","status":"launched"}\n' "$project" >"$dir/json/launch.json"
  run_check "$dir"
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT runs-launched: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: what it found"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "${run2}/result.json" "a launch that names no run: the run it cannot name has no record read"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/notterminal
  prepare "$dir"
  write_rows "$dir" not-terminal
  run_check "$dir" CHECK421_RUN_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a run that never ended"
  expect_contains "$out" "FACT runs-ended: FAIL" "a run that never ended: the row"
  expect_contains "$out" "run 2 of 2 (${run2}) had not ended after 1s" "a run that never ended: which run"
  expect_contains "$out" "fugaro watch ${run2}" "a run that never ended: how to watch it"
  expect_contains "$err" "could NOT measure runs-ended" "a run that never ended: the exit names the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "${run2}/result.json" "a run that never ended: its record is not read"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT records: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read (exit 1)" "a record that cannot be read: what it saw"
}

test_a_record_that_is_not_a_document_is_a_finding() {
  local dir=$work/badrecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-unreadable
  expect_status "$status" 1 "a record that is not a document"
  expect_contains "$out" "FACT records: FAIL" "a record that is not a document: the row"
  expect_contains "$out" "is not a document this check could read" "a record that is not a document: what it found"
}

test_a_record_of_another_run_is_a_finding() {
  # A record read by object name alone could be another run's: the record's own
  # run_id is what ties it to the run this check launched.
  local dir=$work/otherrecord
  prepare "$dir"
  write_record_of_another_run "$dir" "$run1"
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT records: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20261114000000-9 and this check launched ${run1}" "a record of another run: the two IDs"
}

test_a_run_that_did_not_succeed_is_a_finding() {
  local dir=$work/failed
  prepare "$dir"
  write_record "$dir" "$run1" "$first_member" failed
  run_check "$dir"
  expect_status "$status" 1 "a run that failed"
  expect_contains "$out" "FACT succeeded: FAIL" "a run that failed: the row"
  expect_contains "$out" "1 of the 2 runs this check read ended succeeded" "a run that failed: how many did not"
  expect_contains "$out" "#1=failed" "a run that failed: which one, and how it ended"
  expect_contains "$out" "fugaro diagnose <run-id>" "a run that failed: how to read it"
  # The batch is whole and both its records were read, so the spread IS read: a
  # run that failed is a reading of whatever stopped it, not one this check could
  # not take.
  expect_contains "$out" "FACT spread: PASS" "a run that failed: the spread is still read"
}

test_a_batch_over_the_bound_is_a_finding() {
  # CHECK421_MAX_MODEL_USD turns the batch's figure into a criterion; unset, the
  # row is a reading. The money is spent either way, which the row says.
  local dir=$work/overmax
  prepare "$dir"
  run_check "$dir" CHECK421_MAX_MODEL_USD=0.50
  expect_status "$status" 1 "a batch over the bound"
  expect_contains "$out" "FACT budget: FAIL" "a batch over the bound: the row"
  expect_contains "$out" "the 2 runs cost 0.8400 USD of model spend and CHECK421_MAX_MODEL_USD=0.50" "a batch over the bound: the figures it read"
  expect_contains "$out" "the money is spent" "a batch over the bound: what it says about the money"
}

test_a_batch_that_did_not_spread_is_a_finding() {
  # THE row the whole check exists for: every run served by the same member is the
  # pool not rotating, whatever else the records say.
  local dir=$work/nospread
  prepare "$dir"
  write_record "$dir" "$run2" "$first_member" default
  run_check "$dir"
  expect_status "$status" 1 "a batch that did not spread"
  expect_contains "$out" "FACT spread: FAIL" "a batch that did not spread: the row"
  expect_contains "$out" "every one of the 2 runs was served by the SAME member of '${pool}'" "a batch that did not spread: what it found"
  expect_contains "$out" "three selections must not all pick member 1" "a batch that did not spread: MC7's own red step"
  # The batch itself is fine: the runs launched, ended, succeeded and were read,
  # so the finding is about the pool and not about the runs.
  expect_contains "$out" "FACT runs-launched: PASS" "a batch that did not spread: the batch is not the finding"
  expect_contains "$out" "FACT succeeded: PASS" "a batch that did not spread: the runs are not the finding"
}

test_a_record_that_names_no_pool_member_is_a_finding() {
  local dir=$work/nomember
  prepare "$dir"
  write_record "$dir" "$run1" some-other-credential no-member
  run_check "$dir"
  expect_status "$status" 1 "a record that names no pool member"
  expect_contains "$out" "FACT spread: FAIL" "a record that names no pool member: the row"
  expect_contains "$out" "name NO member of '${pool}'" "a record that names no pool member: what it found"
  expect_contains "$out" "#1→none(none)" "a record that names no pool member: how the batch is printed"
}

test_craft_credentials_is_read_before_the_runs_own_account() {
  # MC8's additive field (D1629) is the record's own answer, and it is read first:
  # here run 1's account says the FIRST member while its craft_credentials says the
  # second, so a check that read the account first would see one member serving
  # both runs and report no spread.
  local dir=$work/craftcreds
  prepare "$dir"
  write_record "$dir" "$run1" "$second_member" craft-credentials
  write_record "$dir" "$run2" "$first_member" default
  run_check "$dir"
  expect_status "$status" 0 "craft_credentials first"
  expect_contains "$out" "FACT spread: PASS" "craft_credentials first: the row"
  expect_contains "$out" "#1→${second_member}(craft_credentials)" "craft_credentials first: the field it read"
}

test_the_records_other_fields_answer_when_craft_credentials_is_absent() {
  # A build without MC8 writes neither craft_credentials nor an account for every
  # run: the stages' accounts and the switch log are the fields that answer, and
  # the row says which one it read each member out of.
  local dir=$work/fallback
  prepare "$dir"
  write_record "$dir" "$run1" "$second_member" stage-only
  write_record "$dir" "$run2" "$first_member" switch-only
  run_check "$dir"
  expect_status "$status" 0 "the record's other fields"
  expect_contains "$out" "FACT spread: PASS" "the record's other fields: the row"
  expect_contains "$out" "#1→${second_member}(stage_accounts) #2→${first_member}(switch)" "the record's other fields: where each member was read from"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK421_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK421_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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

# THE MUTATION. The row that counts the members the batch was served by is the
# reading Check 421 names, and it is only worth anything if it really fails a
# batch that did not spread. The case is a COPY of the script with that one guard
# replaced by `false`, run against the same fixture the case above fails: if the
# row were not load-bearing, the copy would report nothing and this test would say
# so.
test_the_spread_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  write_record "$dir" "$run2" "$first_member" default
  mkdir -p "$dir/copy"
  sed 's/if \[ "\$distinct" -lt 2 \]; then/if false; then/' "$script" >"$dir/copy/check421.sh"
  grep -q "if false; then" "$dir/copy/check421.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check421.sh"
  expect_contains "$out" "FACT spread: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT spread: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT spread: FAIL" "the real script: the row"
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
t test_a_run_count_below_two_is_refused
t test_a_run_count_that_is_not_a_number_is_refused
t test_a_build_without_the_crafts_vocabulary_is_refused
t test_a_doctor_that_cannot_be_read_is_not_measured
t test_a_doctor_that_is_not_a_document_is_not_measured
t test_a_craft_that_resolves_to_no_pool_is_a_finding
t test_a_craft_list_with_problems_is_a_finding
t test_a_failover_pool_is_a_finding
t test_a_pool_of_one_member_is_a_finding
t test_a_member_the_service_cannot_serve_is_a_finding
t test_a_subscription_member_with_spread_off_is_a_finding
t test_a_pool_the_node_does_not_hold_is_a_finding
t test_a_craft_on_a_single_credential_is_a_finding
t test_an_unpublished_node_is_a_finding
t test_a_node_that_cannot_be_read_is_not_measured
t test_a_craft_pinned_to_another_access_is_a_finding
t test_a_craft_on_vertex_is_named_and_is_not_a_pin
t test_a_member_missing_from_the_bundle_is_a_finding
t test_a_bundle_that_cannot_be_read_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_that_is_not_a_document_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_run_that_did_not_succeed_is_a_finding
t test_a_batch_over_the_bound_is_a_finding
t test_a_batch_that_did_not_spread_is_a_finding
t test_a_record_that_names_no_pool_member_is_a_finding
t test_craft_credentials_is_read_before_the_runs_own_account
t test_the_records_other_fields_answer_when_craft_credentials_is_absent
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_spread_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check421_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check421_test.sh: ok"
