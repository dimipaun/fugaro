#!/usr/bin/env bash
# Tests for scripts/live-checks/check628.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, an installation or a run, an agent runs no
# live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The model the fakes carry is one installation whose project layer names the
# coder's model and whose coder craft names a credential for it: the fake
# `fugaro` answers `doctor --project <name> --all` (the text whose `project:`
# line the check reads), `config show --json`, `craft ls --json`, `project
# policy show --json`, launches a run and answers the runs list; the fake
# `gcloud` serves the run's record and the two log reads (the gateway's `model
# call` and `budget: request refused` lines and the credentials service's own
# `credsvc: audit` line). The cases that matter are the ones where that model is
# bent: a layer that names no model, a layer whose model came from the
# repository's own file, a project layer that wrote the ROLE SLOT as well, a
# craft that resolves another model, an allow-list that does not admit it, a
# stage that records another model, a gateway that refused a model as not pinned
# for a stage, an audit line that names a credential the craft's access is not.
#
# Usage: bash scripts/live-checks/check628_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$here/lib}
script=$here/check628.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK628_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK628_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk628-test
installation=chk628
model=deepseek/deepseek-v4
credential=deepseek-key
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
strict_gcloud_verify "$LIVE_CHECK_LIB/allow/check628.gcloud" "$@" || exit $?
# A stand-in for `gcloud`, for scripts/live-checks/check628.sh's test only. It
# answers the account read, the run's record and the two log reads — the last
# two from whatever files the case wrote. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk628-owner@example.com\n'
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
  case "$*" in
  *"credsvc: audit"*)
    case "$mode" in
    audit-fail)
      echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED: caller does not have permission" >&2
      exit 1
      ;;
    audit-none) printf '[]\n' ;;
    *) cat "$FAKE_JSON_DIR/audit.json" ;;
    esac
    ;;
  *)
    case "$mode" in
    calls-fail)
      echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED: caller does not have permission" >&2
      exit 1
      ;;
    calls-none) printf '[]\n' ;;
    calls-refused) cat "$FAKE_JSON_DIR/calls-refused.json" ;;
    *) cat "$FAKE_JSON_DIR/calls.json" ;;
    esac
    ;;
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
# A stand-in for `fugaro`, for scripts/live-checks/check628.sh's test only. The
# doctor read (the TEXT the check parses its `project:` line out of), the config
# document, the crafts, the published node, the launch and the runs list all
# come from files the case wrote, and every call is logged with the directory it
# was made in.
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
  esac
  cat "$FAKE_JSON_DIR/doctor.txt"
  ;;
config)
  [ "${2:-}" = "show" ] || { echo "fugaro: unexpected config subcommand" >&2; exit 1; }
  case "$mode" in
  config-fail)
    echo "fugaro: cannot read the config" >&2
    exit 1
    ;;
  config-garbage)
    printf 'not a document\n'
    exit 0
    ;;
  *) cat "$FAKE_JSON_DIR/config.json" ;;
  esac
  ;;
craft)
  [ "${2:-}" = "ls" ] || { echo "fugaro: unexpected craft subcommand" >&2; exit 1; }
  case "$mode" in
  craft-fail)
    echo "fugaro: this build has no craft command" >&2
    exit 1
    ;;
  *) cat "$FAKE_JSON_DIR/crafts.json" ;;
  esac
  ;;
project)
  [ "${2:-}" = "policy" ] && [ "${3:-}" = "show" ] || { echo "fugaro: unexpected project subcommand" >&2; exit 1; }
  case "$mode" in
  node-fail)
    echo "fugaro: this installation has no published policy node" >&2
    exit 1
    ;;
  node-no-allow-list) cat "$FAKE_JSON_DIR/node-no-allow-list.json" ;;
  node-other-allow-list) cat "$FAKE_JSON_DIR/node-other-allow-list.json" ;;
  node-pool) cat "$FAKE_JSON_DIR/node-pool.json" ;;
  *) cat "$FAKE_JSON_DIR/node.json" ;;
  esac
  ;;
run)
  case "$mode" in
  launch-fails)
    printf 'fugaro: the sandbox repository is not enrolled in this installation\n' >&2
    exit 3
    ;;
  launch-no-run)
    printf '{"project":"p","status":"launched"}\n'
    exit 0
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
write_config() { # json-dir MODE
  case "$2" in
  no-craft-model)
    cat >"$1/config.json" <<'JSON'
{"project_layer":"chk628","layer_unknown":false,"checked_at":"2026-11-21T00:00:00Z","config_sha256":"deadbeef","values":[{"path":"agent.models.coder","value":"","source":"default"},{"path":"agent.crafts.review.model","value":"claude-sonnet-5","source":"project"}],"notes":[]}
JSON
    ;;
  repo-source)
    cat >"$1/config.json" <<JSON
{"project_layer":"$installation","layer_unknown":false,"checked_at":"2026-11-21T00:00:00Z","config_sha256":"deadbeef","values":[{"path":"agent.crafts.coder.model","value":"$model","source":"repo"},{"path":"agent.models.coder","value":"","source":"default"}],"notes":[]}
JSON
    ;;
  project-role-slot)
    cat >"$1/config.json" <<JSON
{"project_layer":"$installation","layer_unknown":false,"checked_at":"2026-11-21T00:00:00Z","config_sha256":"deadbeef","values":[{"path":"agent.crafts.coder.model","value":"$model","source":"project"},{"path":"agent.models.coder","value":"$model","source":"project"}],"notes":[]}
JSON
    ;;
  *)
    cat >"$1/config.json" <<JSON
{"project_layer":"$installation","layer_unknown":false,"checked_at":"2026-11-21T00:00:00Z","config_sha256":"deadbeef","values":[{"path":"agent.crafts.coder.model","value":"$model","source":"project"},{"path":"agent.models.coder","value":"claude-sonnet-5","source":"default"},{"path":"agent.crafts.review.model","value":"claude-sonnet-5","source":"project"}],"notes":[]}
JSON
    ;;
  esac
}

write_crafts() { # json-dir MODE
  case "$2" in
  other-model)
    cat >"$1/crafts.json" <<'JSON'
{"version":1,"project":"chk628","crafts":[{"craft":"coder","model":"claude-sonnet-5","model_from":"project","access":"deepseek-key","access_from":"project","inert":false,"model_servable":true,"model_problem":""}]}
JSON
    ;;
  repo-source)
    cat >"$1/crafts.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"$model","model_from":"repo","access":"$credential","access_from":"project","inert":false,"model_servable":true,"model_problem":""}]}
JSON
    ;;
  pool-member)
    cat >"$1/crafts.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"$model","model_from":"project","access":"coder-pool","access_from":"project","inert":false,"model_servable":true,"model_problem":""},{"craft":"review","model":"claude-sonnet-5","model_from":"project","access":"claude-oauth-work","access_from":"project","inert":false,"model_servable":true,"model_problem":""}]}
JSON
    ;;
  *)
    cat >"$1/crafts.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"$model","model_from":"project","access":"$credential","access_from":"project","inert":false,"model_servable":true,"model_problem":""},{"craft":"review","model":"claude-sonnet-5","model_from":"project","access":"claude-oauth-work","access_from":"project","inert":false,"model_servable":true,"model_problem":""}]}
JSON
    ;;
  esac
}

# The node documents are what `fugaro project policy show --json` prints: an
# object whose only members are the project and the policy, with every block of
# the node a member of `policy` — the allow-list under its budget.
write_nodes() { # json-dir
  cat >"$1/node.json" <<JSON
{"project":"$installation","policy":{"version":10,"budget":{"allowed_models":["$model","claude-sonnet-5"]},"craft_credentials":[{"name":"$credential","type":"openrouter"}],"craft_pools":[],"crafts":{"coder":{"model":"$model","access":"$credential"}},"subscription_spread":"allowed"}}
JSON
  cat >"$1/node-no-allow-list.json" <<JSON
{"project":"$installation","policy":{"version":10,"budget":{},"craft_credentials":[{"name":"$credential","type":"openrouter"}],"craft_pools":[],"crafts":{"coder":{"model":"$model","access":"$credential"}}}}
JSON
  cat >"$1/node-other-allow-list.json" <<'JSON'
{"project":"sandbox","policy":{"version":10,"budget":{"allowed_models":["claude-sonnet-5"]},"craft_credentials":[{"name":"deepseek-key","type":"openrouter"}],"craft_pools":[],"crafts":{"coder":{"model":"deepseek/deepseek-v4","access":"deepseek-key"}}}}
JSON
  cat >"$1/node-pool.json" <<JSON
{"project":"$installation","policy":{"version":10,"budget":{"allowed_models":["$model"]},"craft_credentials":[{"name":"$credential","type":"openrouter"},{"name":"openrouter-x","type":"openrouter"}],"craft_pools":[{"name":"coder-pool","strategy":"failover","members":["$credential","openrouter-x"]}],"crafts":{"coder":{"model":"$model","access":"coder-pool"}}}}
JSON
}

# The gateway's `model call` lines for the execution, and the one refusal this
# check is about: a model the gateway's pin does not carry for a stage.
write_calls() { # json-dir
  cat >"$1/calls.json" <<JSON
[{"jsonPayload":{"message":"model call","model":"$model","serving_model":"deepseek-v4","status":"ok","charged_micros":70000}},{"jsonPayload":{"message":"model call","model":"$model","serving_model":"deepseek-v4","status":"ok","charged_micros":30000}}]
JSON
  cat >"$1/calls-refused.json" <<JSON
[{"jsonPayload":{"message":"model call","model":"$model","serving_model":"deepseek-v4","status":"ok","charged_micros":70000}},{"jsonPayload":{"message":"budget: request refused","stage":"implement","violation":"model claude-sonnet-5 is not pinned for implement","status":"refused"}}]
JSON
}

# The service's own audit line: a NAME for the credential it served, its tier
# and its status, and never a value.
write_audit() { # json-dir MODE
  case "$2" in
  none)
    printf '[]\n' >"$1/audit.json"
    ;;
  other-run)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$other_run","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$credential","purpose":"forward","tier":"B","status":"ok"}}]
JSON
    ;;
  other-credential)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"claude-oauth-work","purpose":"forward","tier":"A","status":"ok"}}]
JSON
    ;;
  pool-member)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"openrouter-x","purpose":"forward","tier":"B","status":"ok"}}]
JSON
    ;;
  *)
    cat >"$1/audit.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$credential","purpose":"forward","tier":"B","status":"ok"}},{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"$job","caller":"runner","credential":"$credential","purpose":"exchange","tier":"B","status":"ok"}}]
JSON
    ;;
  esac
}

# The run's own record: the coder craft's stages name the layer's model and the
# account that served them, and the review stage names another model — which is
# not this check's business and must not be read as one.
write_record() { # json-dir MODE
  case "$2" in
  other-model)
    cat >"$1/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.42,"total_usd":0.42},"stages":[{"name":"plan","model":"claude-sonnet-5","credential":"","served_by":"","model_usd":0.1},{"name":"implement","model":"claude-sonnet-5","credential":"$credential","served_by":"api-key","model_usd":0.3},{"name":"review","model":"claude-sonnet-5","credential":"","served_by":"","model_usd":0.02}],"stage_accounts":{"plan":"$credential"}}
JSON
    ;;
  no-coder-stage)
    cat >"$1/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.02,"total_usd":0.02},"stages":[{"name":"review","model":"claude-sonnet-5","credential":"","served_by":"","model_usd":0.02}],"stage_accounts":{}}
JSON
    ;;
  no-account)
    cat >"$1/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.42,"total_usd":0.42},"stages":[{"name":"implement","model":"$model","credential":"","served_by":"","model_usd":0.42}],"stage_accounts":{}}
JSON
    ;;
  *)
    cat >"$1/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.42,"total_usd":0.42},"stages":[{"name":"plan","model":"$model","credential":"","served_by":"","model_usd":0.1},{"name":"implement","model":"$model","credential":"$credential","served_by":"api-key","model_usd":0.3},{"name":"review","model":"claude-sonnet-5","credential":"claude-oauth-work","served_by":"oauth","model_usd":0.02}],"stage_accounts":{"plan":"$credential","implement":"$credential"}}
JSON
    ;;
  esac
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
{"project":"$project","run":"3000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261120-150000-aaaa","execution":"$execution","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/rows-running.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"running","stage":"implement","terminal":false,"settled":false}],"totals":{"runs":1},"warnings":[]}
JSON
  write_config "$json" "$2"
  write_crafts "$json" "$2"
  write_nodes "$json"
  write_calls "$json"
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
    CHECK628_SANDBOX="$dir/sandbox" \
    CHECK628_RUN_TIMEOUT_SECONDS="${CHECK628_RUN_TIMEOUT_SECONDS:-10}" \
    CHECK628_POLL_SECONDS=1 \
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
  expect_contains "$out" "Check 628" "help: the header"
  expect_contains "$out" "What it measures" "help: the docblock"
  expect_contains "$out" "check628.sh --sandbox" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK628_*)" "help: the knobs"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK628_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT installation: INFO" "a dry run: the planned rows are not measured"
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
  for key in fugaro-version xtrace agent-session belong installation layer role-slot craft-ls allow-list run-launched terminal result-json stage-model credential not-pinned restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_absent "$out" ": FAIL" "the happy path: no row failed"
  expect_contains "$out" "CHECK 628 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "the project layer sets agent.crafts.coder.model = $model" "the happy path: the layer row names the model and its source"
  expect_contains "$out" "every coder stage of $run_id records model $model" "the happy path: the stages are the coder craft's"
  expect_contains "$out" "FACT credential: PASS" "the happy path: the credential row"
  expect_contains "$out" "was served by $credential — the access the coder craft names" "the happy path: the credential row names the access and the credential that answered"
  expect_contains "$out" "read from the credentials service's own audit line" "the happy path: the audit line is what named it"
  expect_contains "$out" "per-stage credential/served_by: implement=$credential/api-key" "the happy path: the record's own stage rows are read beside it"
  expect_contains "$out" "no 'budget: request refused' line at all" "the happy path: the gateway raised no refusal"
  expect_contains "$fugaro_calls" "fugaro doctor --all (in $dir/sandbox)" "the happy path: the installation is read from the checkout"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox" "the happy path: the run is launched from the checkout"
  expect_contains "$fugaro_calls" "fugaro config show --json" "the happy path: the resolved config is read"
  expect_contains "$fugaro_calls" "fugaro craft ls --json" "the happy path: the crafts are read"
  expect_contains "$gcloud_calls" "storage cat gs://fugaro-runs-$project/runs/$slug/$run_id/result.json" "the happy path: the record is the run's own key"
  expect_contains "$gcloud_calls" "jsonPayload.run=\"$run_id\"" "the happy path: the audit read is this run's"
  expect_contains "$gcloud_calls" "budget: request refused" "the happy path: the refusals are read for the execution"
  assert_scratch_removed "$dir" "the happy path"
}

test_an_installation_whose_layer_names_no_model_is_a_finding() {
  local dir=$work/no-craft-model
  prepare "$dir" no-craft-model
  run_check "$dir"
  expect_status "$status" 1 "a layer with no craft model"
  expect_contains "$out" "FACT layer: FAIL" "a layer with no craft model: the row"
  expect_contains "$out" "names no model for agent.crafts.coder.model" "a layer with no craft model: the message says what is missing"
}

test_a_craft_model_from_the_repository_file_is_a_finding() {
  local dir=$work/repo-source
  prepare "$dir" repo-source
  run_check "$dir"
  expect_status "$status" 1 "a craft model the repository's file wrote"
  expect_contains "$out" "FACT layer: FAIL" "a craft model from the repository's file: the resolve row"
  expect_contains "$out" "with source 'repo'" "a craft model from the repository's file: the source is named"
  expect_contains "$out" "FACT craft-ls: FAIL" "a craft model from the repository's file: the craft row agrees it is not the layer's"
}

test_a_project_layer_that_wrote_the_role_slot_is_a_finding() {
  local dir=$work/role-slot
  prepare "$dir" project-role-slot
  run_check "$dir"
  expect_status "$status" 1 "a project layer that wrote the role slot"
  expect_contains "$out" "FACT role-slot: FAIL" "a project layer that wrote the role slot: the row"
  expect_contains "$out" "agent.models.coder resolves to $model" "a project layer that wrote the role slot: the slot is named"
}

test_a_craft_that_resolves_another_model_is_a_finding() {
  local dir=$work/craft-other-model
  prepare "$dir" other-model
  run_check "$dir"
  expect_status "$status" 1 "a craft that resolves another model"
  expect_contains "$out" "FACT craft-ls: FAIL" "a craft that resolves another model: the row"
  expect_contains "$out" "resolves the coder craft's model to claude-sonnet-5" "a craft that resolves another model: the model is named"
}

test_an_allow_list_that_does_not_admit_the_model_is_a_finding() {
  local dir=$work/allow-list
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-other-allow-list
  expect_status "$status" 1 "an allow-list without the model"
  expect_contains "$out" "FACT allow-list: FAIL" "an allow-list without the model: the row"
  expect_contains "$out" "does not admit" "an allow-list without the model: the message says why"
}

test_a_node_with_no_allow_list_reads_the_crafts_own_answer() {
  local dir=$work/no-allow-list
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-no-allow-list
  expect_status "$status" 0 "a node that names no allow-list"
  expect_contains "$out" "FACT allow-list: PASS" "a node that names no allow-list: the row"
  expect_contains "$out" "names no allowed_models" "a node that names no allow-list: the reading is named"
}

test_a_craft_read_that_does_not_answer_is_not_measured() {
  local dir=$work/craft-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=craft-fail
  expect_status "$status" 2 "a craft command that does not answer"
  expect_contains "$out" "FACT craft-ls: FAIL" "a craft command that does not answer: the row"
  expect_contains "$err" "could NOT measure craft-ls" "a craft command that does not answer: the reason"
}

test_a_config_that_does_not_answer_is_not_measured() {
  local dir=$work/config-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=config-fail
  expect_status "$status" 2 "a config read that does not answer"
  expect_contains "$err" "could NOT measure layer" "a config read that does not answer: the reason"
}

test_a_config_that_prints_no_document_is_not_measured() {
  local dir=$work/config-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=config-garbage
  expect_status "$status" 2 "a config read that printed no document"
  expect_contains "$err" "could NOT measure layer" "a config read that printed no document: the reason"
}

test_a_node_read_that_does_not_answer_is_not_measured() {
  local dir=$work/node-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-fail
  expect_status "$status" 2 "a policy node that does not answer"
  expect_contains "$err" "could NOT measure allow-list" "a policy node that does not answer: the reason"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK628_SANDBOX=
  expect_status "$status" 2 "no sandbox"
  expect_contains "$err" "needs --sandbox" "no sandbox: the refusal names the flag"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "a path that is not a checkout: nothing is launched"
}

test_a_doctor_without_a_project_line_is_not_measured() {
  local dir=$work/doctor-no-project
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-no-project
  expect_status "$status" 2 "a doctor with no project line"
  expect_contains "$err" "could NOT measure installation" "a doctor with no project line: the reason"
  expect_absent "$fugaro_calls" "fugaro run " "a doctor with no project line: nothing is launched"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launch-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_absent "$gcloud_calls" "storage cat" "a launch that fails: no record is read"
}

test_a_launch_that_names_no_run_is_a_finding() {
  local dir=$work/launch-no-run
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-no-run
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: the message says why"
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
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-never-ends
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ends: the row"
  expect_contains "$err" "could NOT measure terminal" "a run that never ends: the reason"
  expect_absent "$gcloud_calls" "storage cat" "a run that never ends: no record is read"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/record-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-fail
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT result-json: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read" "a record that cannot be read: the message"
  expect_absent "$gcloud_calls" "logging read" "a record that cannot be read: no log is read"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/record-other-run
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-other-run
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=$other_run" "a record of another run: the record's own run is named"
}

test_a_stage_that_records_another_model_is_a_finding() {
  local dir=$work/stage-other-model
  prepare "$dir" other-model
  run_check "$dir"
  expect_status "$status" 1 "a stage that ran another model"
  expect_contains "$out" "FACT stage-model: FAIL" "a stage that ran another model: the row"
  expect_contains "$out" "plan=claude-sonnet-5, implement=claude-sonnet-5" "a stage that ran another model: the stages are named"
}

test_a_run_that_reached_no_coder_stage_is_a_finding() {
  local dir=$work/no-coder-stage
  prepare "$dir" no-coder-stage
  run_check "$dir"
  expect_status "$status" 1 "a run that reached no coder stage"
  expect_contains "$out" "FACT stage-model: FAIL" "a run that reached no coder stage: the row"
  expect_contains "$out" "carries no row for plan, implement or fix" "a run that reached no coder stage: the message says why"
}

test_a_run_whose_credential_nobody_names_is_a_finding() {
  local dir=$work/no-account
  prepare "$dir" no-account
  run_check "$dir" FAKE_MODE=audit-none
  expect_status "$status" 1 "a run whose credential nobody names"
  expect_contains "$out" "FACT credential: FAIL" "a run whose credential nobody names: the row"
  expect_contains "$out" "carries no craft_credentials" "a run whose credential nobody names: the missing field is reported"
}

test_a_credential_the_craft_does_not_name_is_a_finding() {
  local dir=$work/other-credential
  prepare "$dir" other-credential
  run_check "$dir"
  expect_status "$status" 1 "a credential the craft does not name"
  expect_contains "$out" "FACT credential: FAIL" "a credential the craft does not name: the row"
  expect_contains "$out" "these two disagree" "a credential the craft does not name: the message says why"
}

test_a_pools_member_that_served_the_run_passes() {
  local dir=$work/pool-member
  prepare "$dir" pool-member
  run_check "$dir" FAKE_MODE=node-pool
  expect_status "$status" 0 "a pool's second member served the run"
  expect_contains "$out" "FACT credential: PASS" "a pool's second member: the row"
  expect_contains "$out" "which is a member of the pool coder-pool (members $credential,openrouter-x)" "a pool's second member: the pool is named with its members"
}

test_not_pinned_refusals_are_a_finding() {
  local dir=$work/not-pinned
  prepare "$dir"
  run_check "$dir" FAKE_MODE=calls-refused
  expect_status "$status" 1 "a gateway that refused a model as not pinned"
  expect_contains "$out" "FACT not-pinned: FAIL" "a gateway that refused a model as not pinned: the row"
  expect_contains "$out" "model claude-sonnet-5 is not pinned for implement" "a gateway that refused a model as not pinned: the violation is quoted"
}

test_a_log_read_that_does_not_answer_is_not_measured() {
  local dir=$work/calls-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=calls-fail
  expect_status "$status" 2 "a log read that does not answer"
  expect_contains "$out" "FACT not-pinned: FAIL" "a log read that does not answer: the row"
  expect_contains "$err" "could NOT measure not-pinned" "a log read that does not answer: the reason"
}

test_a_log_with_no_gateway_line_is_not_measured() {
  local dir=$work/calls-none
  prepare "$dir"
  run_check "$dir" FAKE_MODE=calls-none
  expect_status "$status" 2 "a log with no gateway line at all"
  expect_contains "$err" "could NOT measure not-pinned" "a log with no gateway line at all: the reason"
  expect_contains "$out" "CHECK628_LOG_VIEW" "a log with no gateway line at all: the knob to read it again with is named"
}

test_an_audit_read_that_does_not_answer_still_reads_the_record() {
  local dir=$work/audit-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=audit-fail
  expect_status "$status" 0 "an audit read that does not answer"
  expect_contains "$out" "FACT credential: PASS" "an audit read that does not answer: the record's own stage accounts answer"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a traced run"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced run: the row"
  expect_contains "$err" "this shell is tracing" "a traced run: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "a traced run: nothing is launched"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent-session
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the refusal names the marker"
  expect_absent "$fugaro_calls" "fugaro run " "a coding agent's session: nothing is launched"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK628_PROJECT=belong-sandbox
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "names belong" "a project naming belong: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "a project naming belong: nothing is launched"
}

test_an_installation_naming_belong_stops_the_check() {
  local dir=$work/belong-installation
  prepare "$dir"
  printf 'project: belong-dev (GCP belong-dev, us-east5)\n' >"$dir/json/doctor.txt"
  run_check "$dir"
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "is never run there" "an installation naming belong: the refusal says why"
  expect_absent "$fugaro_calls" "fugaro run " "an installation naming belong: nothing is launched"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK628_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK628_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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
t test_an_installation_whose_layer_names_no_model_is_a_finding
t test_a_craft_model_from_the_repository_file_is_a_finding
t test_a_project_layer_that_wrote_the_role_slot_is_a_finding
t test_a_craft_that_resolves_another_model_is_a_finding
t test_an_allow_list_that_does_not_admit_the_model_is_a_finding
t test_a_node_with_no_allow_list_reads_the_crafts_own_answer
t test_a_craft_read_that_does_not_answer_is_not_measured
t test_a_config_that_does_not_answer_is_not_measured
t test_a_config_that_prints_no_document_is_not_measured
t test_a_node_read_that_does_not_answer_is_not_measured
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_doctor_without_a_project_line_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_run_that_ends_between_polls_settles
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_stage_that_records_another_model_is_a_finding
t test_a_run_that_reached_no_coder_stage_is_a_finding
t test_a_run_whose_credential_nobody_names_is_a_finding
t test_a_credential_the_craft_does_not_name_is_a_finding
t test_a_pools_member_that_served_the_run_passes
t test_not_pinned_refusals_are_a_finding
t test_a_log_read_that_does_not_answer_is_not_measured
t test_a_log_with_no_gateway_line_is_not_measured
t test_an_audit_read_that_does_not_answer_still_reads_the_record
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_an_installation_naming_belong_stops_the_check
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check628_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check628_test.sh: ok"
