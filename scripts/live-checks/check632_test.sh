#!/usr/bin/env bash
# Tests for scripts/live-checks/check632.sh, with fake `fugaro` and fake `gcloud`
# on PATH: NOTHING here reaches a cloud, an installation or a run, no update is
# made anywhere, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do
# (and that the dry run launched no run and ran no update).
#
# The model the fakes carry is one installation at the previous version, holding
# a policy node, a coder craft, a pool and a credential before the update, a
# sandbox run that is going when the update starts, an update that prints its
# one in-flight line and converges the installation, and a run that finishes
# afterwards with its ledger, its counters and its record intact. The cases that
# matter are the ones where that model is bent: a doctor with no project line, a
# doctor behind an installation already current, an update that fails, one that
# times out, one that prints no in-flight line while a run was in flight, one
# that asks the owner for another command, a run that had already ended, a run
# that never ends, a crossing pool left after the update, a record that lost its
# outcome, a counter that reset and a ledger that lost the run.
#
# Usage: bash scripts/live-checks/check632_test.sh
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check632.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK632_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK632_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk632-test
installation=chk632
model=deepseek/deepseek-v4
other_model=openrouter/x-9
run_id=run-20261121120000-1
title="Check 632 task: reply with ok"
other_run=run-20261120110000-9
slug=acme-sandbox-9f8e7d6c
job=fugaro-run-9f8e7d6c
execution="projects/$project/locations/us-east5/jobs/$job/executions/$job-abc123"

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check632.sh's test only. It
# answers the run's record read and nothing else. Nothing here reaches a real
# project.
set -u
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
if [ "${1:-}" = "storage" ] && [ "${2:-}" = "cat" ]; then
  case "$mode" in
  record-fail)
    echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
    exit 1
    ;;
  record-lost) cat "$FAKE_JSON_DIR/record-lost.json" ;;
  record-garbage) printf 'not a document\n' ;;
  *) cat "$FAKE_JSON_DIR/record.json" ;;
  esac
  exit 0
fi
echo "gcloud: unexpected invocation ${1:-}" >&2
exit 1
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check632.sh's test only. The
# doctor reads, the node, the crafts, the pools, the credentials, the listing,
# the chain and the launch all come from files the case wrote; the update writes
# the state marker that switches the later reads to the converged answers.
set -u
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
updated=no
[ -f "$FAKE_STATE/updated" ] && updated=yes

# The real CLI is cobra, and cobra refuses a flag its command does not declare:
# "fugaro: unknown flag: --project" on stderr, exit 1. A fake that shrugged the
# extra argument off is exactly what let a check hand `accounts add` a --project
# it does not take and still look healthy; these are the flags each command this
# check calls declares (read off the release's own --help), and a call carrying
# any other is refused in the same words.
flag_set() { # flag_set SUBCOMMAND...
  case "$1" in
  version) printf '%s' "" ;;
  doctor) printf '%s' "--all --check-bitbucket-token --check-github-app --config --dir --gcp-project --json --offline --plugin --project --region --security --strict" ;;
  craft) printf '%s' "--config --gcp-project --json --project --region" ;;
  accounts)
    case "${2:-}" in
    add) printf '%s' "--ask --claude --profile --repos --type" ;;
    rm) printf '%s' "--no-apply --profile" ;;
    ls) printf '%s' "--config --gcp-project --json --project --region --repo" ;;
    pool)
      case "${3:-}" in
      add) printf '%s' "--allow-user --layer --owner --strategy" ;;
      rm) printf '%s' "--layer" ;;
      ls) printf '%s' "--config --gcp-project --project --region" ;;
      esac
      ;;
    craft)
      case "${3:-}" in
      set) printf '%s' "--access --layer" ;;
      rm) printf '%s' "--layer" ;;
      esac
      ;;
    esac
    ;;
  config) printf '%s' "--json --offline --project-layer --workflow" ;;
  project) printf '%s' "--config --delete --gcp-project --json --project --region --remove-routing --seed-repos" ;;
  run) printf '%s' "--allow-duplicate --attach --attempts --batch --change-set --code-only --commit-reports --config --context --continues --gcp-project --json --max-review-rounds --max-time --max-tokens --max-usd --mode --no-budget-check --no-compression --owner-ruled --path --plan-task --pr --project --recipe --ref --region --repo --retry --review-levels --run-id --security --spec --task-file --tier --title --token --total-timeout --used-review-rounds --used-time --used-tokens --used-usd --workflow" ;;
  runs)
    case "${2:-}" in
    show) printf '%s' "--config --gcp-project --json --project --region --repo" ;;
    *) printf '%s' "--after --all --batch --config --debt --gcp-project --group-by --json --limit --mine --pr --project --region --repo --since --state --stream --watch" ;;
    esac
    ;;
  update) printf '%s' "--admin --alert-email --allow-delete --allow-job-delete --anchor --answers-file --ask --bake-repos --base --base-image --billing-account --budget --budget-admin --budget-currency --budget-mode --check --check-github-app --config --config-only --coordinator --events --expect-digest --firebase-project --forget --gcp-project --github-app-id --image-source --json --location --name --no-build --no-log-isolation --onboard-repo --operator --plan-only --print-vars --project --publish-config --region --registry-cleanup --replace-image --repo --retire --rigor --runs-bucket --scheduler-region --state-bucket" ;;
  esac
}

guard_flags() { # guard_flags — the whole call, command words first, then flags
  local words=() f
  while [ $# -gt 0 ]; do
    case "$1" in
    -*) break ;;
    *) words+=("$1"); shift ;;
    esac
  done
  local set
  set=$(flag_set "${words[@]+"${words[@]}"}")
  for f in "$@"; do
    case "$f" in
    -*) ;;
    *) continue ;;
    esac
    case "$f" in
    --*=*) f=${f%=*} ;;
    esac
    case " $set " in
    *" $f "*) ;;
    *)
      printf 'fugaro: unknown flag: %s\n' "$f" >&2
      exit 1
      ;;
    esac
  done
}
guard_flags "$@"

case "${1:-}" in
version) printf '%s\n' "${FAKE_VERSION:-0.28.0}" ;;
doctor)
  json=no; all=no
  for a in "$@"; do
    case "$a" in
    --json) json=yes ;;
    --all) all=yes ;;
    esac
  done
  case "$mode" in
  doctor-no-project)
    printf 'fugaro %s\nall checks passed\n' "${FAKE_VERSION:-0.28.0}"
    exit 0
    ;;
  doctor-belong)
    printf 'fugaro %s\nproject: chk632 (GCP belong-sandbox, us-east5)\n' "${FAKE_VERSION:-0.28.0}"
    exit 0
    ;;
  esac
  if [ "$json" = yes ]; then
    case "$mode" in
    doctorjson-none) cat "$FAKE_JSON_DIR/doctorjson-none.json" ;;
    doctorjson-fail)
      echo "fugaro: doctor could not run" >&2
      exit 1
      ;;
    doctorjson-garbage) printf 'not a document\n' ;;
    *) cat "$FAKE_JSON_DIR/doctorjson.json" ;;
    esac
  elif [ "$all" = yes ]; then
    cat "$FAKE_JSON_DIR/doctor-all.txt"
  else
    cat "$FAKE_JSON_DIR/doctor-plain.txt"
  fi
  ;;
accounts)
  case "${2:-}" in
  ls)
    case "$mode" in
    creds-none) cat "$FAKE_JSON_DIR/creds-none.json" ;;
    creds-fail)
      echo "fugaro: cannot read the config" >&2
      exit 1
      ;;
    *) cat "$FAKE_JSON_DIR/creds.json" ;;
    esac
    ;;
  pool)
    if [ "$updated" = yes ]; then
      case "$mode" in
      crossing-after) cat "$FAKE_JSON_DIR/pools-after-crossing.txt" ;;
      crossing-before) cat "$FAKE_JSON_DIR/pools-after-split.txt" ;;
      *) cat "$FAKE_JSON_DIR/pools-after.txt" ;;
      esac
    else
      case "$mode" in
      crossing-before) cat "$FAKE_JSON_DIR/pools-before-crossing.txt" ;;
      *) cat "$FAKE_JSON_DIR/pools-before.txt" ;;
      esac
    fi
    ;;
  *)
    echo "fugaro: unexpected accounts subcommand" >&2
    exit 1
    ;;
  esac
  ;;
craft)
  if [ "$updated" = yes ]; then
    case "$mode" in
    craft-none) cat "$FAKE_JSON_DIR/crafts-after-none.json" ;;
    craft-other) cat "$FAKE_JSON_DIR/crafts-after-other.json" ;;
    *) cat "$FAKE_JSON_DIR/crafts-after.json" ;;
    esac
  else
    cat "$FAKE_JSON_DIR/crafts-before.json"
  fi
  ;;
config)
  [ "${2:-}" = "show" ] || { echo "fugaro: unexpected config subcommand" >&2; exit 1; }
  cat "$FAKE_JSON_DIR/configshow.json"
  ;;
project)
  [ "${2:-}" = "policy" ] || { echo "fugaro: unexpected project subcommand" >&2; exit 1; }
  case "${3:-}" in
  show)
    case "$mode" in
    node-fail)
      echo "fugaro: this installation has no published policy node" >&2
      exit 1
      ;;
    node-garbage) printf 'not a document\n' ;;
    esac
    if [ "$updated" = yes ]; then
      case "$mode" in
      node-old) cat "$FAKE_JSON_DIR/node-after-old.json" ;;
      *) cat "$FAKE_JSON_DIR/node-after.json" ;;
      esac
    else
      cat "$FAKE_JSON_DIR/node-before.json"
    fi
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
  case "${2:-}" in
  ls)
    if [ "$updated" = yes ]; then
      case "$mode" in
      run-never-ends) cat "$FAKE_JSON_DIR/rows-running.json" ;;
      counter-reset) cat "$FAKE_JSON_DIR/rows-terminal-reset.json" ;;
      *) cat "$FAKE_JSON_DIR/rows-terminal.json" ;;
      esac
    else
      case "$mode" in
      run-short) cat "$FAKE_JSON_DIR/rows-short.json" ;;
      *) cat "$FAKE_JSON_DIR/rows-running.json" ;;
      esac
    fi
    ;;
  show)
    if [ "$updated" = yes ]; then
      case "$mode" in
      ledger-lost) cat "$FAKE_JSON_DIR/show-after-lost.json" ;;
      *) cat "$FAKE_JSON_DIR/show-after.json" ;;
      esac
    else
      cat "$FAKE_JSON_DIR/show-before.json"
    fi
    ;;
  *)
    echo "fugaro: unexpected runs subcommand" >&2
    exit 1
    ;;
  esac
  ;;
update)
  case "$mode" in
  update-fail)
    printf 'fugaro: the update failed to apply\n' >&2
    exit 4
    ;;
  update-slow)
    sleep 5
    printf 'fugaro: converged slowly\n'
    ;;
  update-no-note)
    printf 'fugaro: converged %s\n' "${FAKE_INSTALLATION:-chk632}"
    ;;
  *)
    printf 'fugaro: 1 run in flight: update waits for none and changes nothing they hold, and no step reaches them\n'
    ;;
  esac
  case "$mode" in
  update-operator-step)
    printf 'fugaro: run "fugaro accounts pool add x y z" to finish the upgrade\n'
    ;;
  esac
  : >"$FAKE_STATE/updated"
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
write_doctor() { # json-dir
  cat >"$1/doctor-all.txt" <<TXT
fugaro 0.28.0
project: $installation (GCP $project, us-east5)
repository: acme/sandbox
TXT
  cat >"$1/doctor-plain.txt" <<TXT
fugaro 0.28.0
all checks passed
TXT
  cat >"$1/doctorjson.json" <<JSON
{"ok":false,"checks":[{"id":"orchestrator-unit","ok":false,"severity":"warning","problem":"the installation is at 0.27.9, this CLI is 0.28.0","fix":"fugaro update"}],"release":{"tag":"0.28.0","commit":"abc1234"}}
JSON
  cat >"$1/doctorjson-none.json" <<JSON
{"ok":true,"checks":[{"id":"plugin-wired","ok":true,"severity":"info"}],"release":{"tag":"0.28.0","commit":"abc1234"}}
JSON
}

write_creds() { # json-dir
  cat >"$1/creds.json" <<JSON
{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":[{"name":"claude-oauth-work","type":"anthropic","serving_path":"credsvc"},{"name":"deepseek-key","type":"openrouter","serving_path":"runner"}]}
JSON
  cat >"$1/creds-none.json" <<JSON
{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":[]}
JSON
}

write_pools() { # json-dir
  cat >"$1/pools-before.txt" <<TXT
pools for project $installation (offline)

NAME        STRATEGY  MEMBERS            SERVED BY
coder-pool  failover  claude-oauth-work  the credentials service
TXT
  cat >"$1/pools-after.txt" <<TXT
pools for project $installation (offline)

NAME        STRATEGY  MEMBERS            SERVED BY
coder-pool  failover  claude-oauth-work  the credentials service
TXT
  cat >"$1/pools-before-crossing.txt" <<TXT
pools for project $installation (offline)

NAME        STRATEGY  MEMBERS                          SERVED BY
coder-pool  failover  deepseek-key, claude-oauth-work  the credentials service and a run's own gateway: failover at a stage boundary
TXT
  cat >"$1/pools-after-crossing.txt" <<TXT
pools for project $installation (offline)

NAME        STRATEGY  MEMBERS                          SERVED BY
coder-pool  failover  deepseek-key, claude-oauth-work  the credentials service and a run's own gateway: failover at a stage boundary
TXT
  cat >"$1/pools-after-split.txt" <<TXT
pools for project $installation (offline)

NAME                 STRATEGY  MEMBERS            SERVED BY
coder-pool-claude    failover  claude-oauth-work  the credentials service
coder-pool-provider  failover  deepseek-key       a run's own gateway
TXT
}

write_crafts() { # json-dir
  cat >"$1/crafts-before.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"$model","model_from":"repo","access":"coder-pool","access_from":"local","inert":false,"model_servable":true}]}
JSON
  cat >"$1/crafts-after.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"$model","model_from":"repo","access":"coder-pool","access_from":"local","inert":false,"model_servable":true}]}
JSON
  cat >"$1/crafts-after-none.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"","model_from":"","access":"coder-pool","access_from":"local","inert":false,"model_servable":true}]}
JSON
  cat >"$1/crafts-after-other.json" <<JSON
{"version":1,"project":"$installation","crafts":[{"craft":"coder","model":"$other_model","model_from":"local","access":"coder-pool","access_from":"local","inert":false,"model_servable":true}]}
JSON
}

# Each node document is what `fugaro project policy show --json` prints: the
# node's blocks are members of `policy`, under the object's project.
write_nodes() { # json-dir
  cat >"$1/node-before.json" <<JSON
{"project":"$installation","policy":{"version":8,"craft_credentials":[{"name":"claude-oauth-work","type":"anthropic"}],"craft_pools":[{"name":"coder-pool","strategy":"failover","members":["claude-oauth-work"]}],"crafts":{"coder":{"model":"$model","access":"coder-pool"}}}}
JSON
  cat >"$1/node-after.json" <<JSON
{"project":"$installation","policy":{"version":9,"craft_credentials":[{"name":"claude-oauth-work","type":"anthropic"}],"craft_pools":[{"name":"coder-pool","strategy":"failover","members":["claude-oauth-work"]}],"crafts":{"coder":{"model":"$model","access":"coder-pool"}}}}
JSON
  cat >"$1/node-after-old.json" <<JSON
{"project":"$installation","policy":{"version":7,"craft_credentials":[{"name":"claude-oauth-work","type":"anthropic"}],"craft_pools":[{"name":"coder-pool","strategy":"failover","members":["claude-oauth-work"]}],"crafts":{"coder":{"model":"$model","access":"coder-pool"}}}}
JSON
}

write_configshow() { # json-dir
  cat >"$1/configshow.json" <<JSON
{"config_sha256":"deadbeef","values":[{"path":"agent.max_output_tokens.reviewer","value":32000,"source":"repo"}]}
JSON
}

write_launch_rows() { # json-dir
  cat >"$1/launch.json" <<JSON
{"project":"$project","repo":"acme/sandbox","run_id":"$run_id","status":"launched"}
JSON
  cat >"$1/rows-running.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"running","stage":"implement","terminal":false,"settled":false,"title":"$title","outcome":"","cost":{"model_usd":0.10,"total_usd":0.10,"compute_usd":0.02,"cache_read_tokens":100,"cache_write_tokens":10}}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$1/rows-short.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true,"title":"$title","outcome":"pr","cost":{"model_usd":0.42,"total_usd":0.42,"compute_usd":0.05,"cache_read_tokens":500,"cache_write_tokens":50}}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$1/rows-terminal.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true,"title":"$title","outcome":"pr","cost":{"model_usd":0.42,"total_usd":0.42,"compute_usd":0.05,"cache_read_tokens":500,"cache_write_tokens":50}}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$1/rows-terminal-reset.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$run_id","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true,"title":"$title","outcome":"pr","cost":{"model_usd":0.02,"total_usd":0.02,"compute_usd":0.01,"cache_read_tokens":5,"cache_write_tokens":1}}],"totals":{"runs":1},"warnings":[]}
JSON
}

write_show() { # json-dir
  cat >"$1/show-before.json" <<JSON
{"version":1,"repo":"acme/sandbox","run":"$slug/$run_id","chain":"$run_id","run_count":1,"enforced":true,"limits_read":true,"limits":[{"dimension":"usd","limit":2000000,"used":100000,"remaining":1900000,"source":"launch"}]}
JSON
  cat >"$1/show-after.json" <<JSON
{"version":1,"repo":"acme/sandbox","run":"$slug/$run_id","chain":"$run_id","run_count":1,"enforced":true,"limits_read":true,"limits":[{"dimension":"usd","limit":2000000,"used":420000,"remaining":1580000,"source":"launch"}]}
JSON
  cat >"$1/show-after-lost.json" <<JSON
{"version":1,"repo":"acme/sandbox","run":"$slug/$run_id","chain":"$other_run","run_count":1,"enforced":true,"limits_read":true,"limits":[{"dimension":"usd","limit":2000000,"used":420000,"remaining":1580000,"source":"launch"}]}
JSON
}

write_record() { # json-dir
  cat >"$1/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","execution":"$execution","branch":"fugaro/$run_id","finished_at":"2026-11-21T12:30:00Z","pr":{"url":"https://example.invalid/acme/sandbox/pull/7"},"cost":{"model_usd":0.42,"total_usd":0.42,"compute_usd":0.05,"cache_read_tokens":500,"cache_write_tokens":50},"stages":[{"name":"implement","duration_s":120.0},{"name":"review","duration_s":60.0}]}
JSON
  cat >"$1/record-lost.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"","execution":"$execution","cost":{"model_usd":0.42,"total_usd":0.42},"stages":[{"name":"implement","duration_s":120.0}]}
JSON
}

write_docs() { # dir
  local json=$1/json
  mkdir -p "$json"
  write_doctor "$json"
  write_creds "$json"
  write_pools "$json"
  write_crafts "$json"
  write_nodes "$json"
  write_configshow "$json"
  write_launch_rows "$json"
  write_show "$json"
  write_record "$json"
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: %s\n' "$installation" >"$1/fugaro.yaml"
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
  rm -f "$dir/state/updated"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  status=0
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
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" FAKE_STATE="$dir/state" \
    FAKE_RUN_ID="$run_id" FAKE_TITLE="$title" FAKE_MODEL="$model" \
    FAKE_INSTALLATION="$installation" \
    CHECK632_SANDBOX="$dir/sandbox" \
    CHECK632_RUN_TIMEOUT_SECONDS="${CHECK632_RUN_TIMEOUT_SECONDS:-10}" \
    CHECK632_POLL_SECONDS=1 \
    CHECK632_UPDATE_TIMEOUT_SECONDS="${CHECK632_UPDATE_TIMEOUT_SECONDS:-10}" \
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
  expect_contains "$out" "Check 632" "help: the header"
  expect_contains "$out" "What it measures" "help: the docblock"
  expect_contains "$out" "check632.sh --sandbox" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK632_*)" "help: the knobs"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK632_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT installation: INFO" "a dry run: the planned rows are not measured"
  expect_contains "$err" "[write] fugaro update" "a dry run: the plan says which write it would make"
  expect_absent "$fugaro_calls" "fugaro run " "a dry run: no run is launched"
  expect_absent "$fugaro_calls" "fugaro update" "a dry run: NO update is made"
  expect_absent "$gcloud_calls" "storage cat" "a dry run: no record is read"
  [ -z "$gcloud_calls" ] || fail "a dry run: gcloud was called at all"
  assert_scratch_removed "$dir" "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in installation version-before run-launched run-in-flight update update-notes-runs \
    no-operator-step converge-model converge-publish converge-crossing converge-key run-terminal \
    ledger-intact counters-intact record-intact restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  for key in fugaro-version xtrace agent-session belong before; do
    case "$key" in
    before) expect_contains "$out" "FACT before: INFO" "the happy path: the before row is informational" ;;
    *) expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row" ;;
    esac
  done
  expect_absent "$out" "FACT installation: FAIL" "the happy path: no row failed"
  expect_contains "$out" "CHECK 632 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "is a checkout of the installation $installation (GCP $project)" "the happy path: the installation is read from the checkout"
  expect_contains "$out" "the installation behind this CLI" "the happy path: the version before row names the check"
  expect_contains "$out" "is in flight (status=running) when the update starts" "the happy path: the run is in flight"
  expect_contains "$out" "fugaro update converged $installation" "the happy path: the update converged"
  expect_contains "$out" "waits for none of them and changes nothing they hold" "the happy path: the in-flight line is read"
  expect_contains "$out" "names the coder craft's model $model" "the happy path: the coder craft is read"
  expect_contains "$out" "craft_credentials (1 entries), craft_pools (1) and crafts (1) at version 9" "the happy path: the node's crafts blocks are read back"
  expect_contains "$out" "did not go down from the 8 read before it" "the happy path: the node version did not go down"
  expect_contains "$out" "reached a terminal state after the update (status=succeeded" "the happy path: the run reached its end"
  expect_contains "$out" "the ledger still accounts for $run_id" "the happy path: the ledger is intact"
  expect_contains "$out" "counters are monotone across the update" "the happy path: the counters are monotone"
  expect_contains "$out" "is the same run the check launched" "the happy path: the record is the same run"
  expect_contains "$fugaro_calls" "fugaro doctor --project $installation --all (in $dir/sandbox)" "the happy path: the installation is read from the checkout"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox" "the happy path: the run is launched from the checkout"
  expect_contains "$fugaro_calls" "fugaro update --project $installation (in $dir/sandbox)" "the happy path: the update is run from the checkout"
  expect_contains "$fugaro_calls" "fugaro project policy show --json (in $dir/sandbox)" "the happy path: the node is read from the checkout"
  expect_contains "$fugaro_calls" "fugaro config show --json (in $dir/sandbox)" "the happy path: the config's pools are read"
  expect_contains "$gcloud_calls" "storage cat gs://fugaro-runs-$project/runs/$slug/$run_id/result.json" "the happy path: the record is the run's own key"
  assert_scratch_removed "$dir" "the happy path"
}

test_the_project_flag_is_passed_through() {
  local dir=$work/with-project
  prepare "$dir"
  run_check "$dir" CHECK632_PROJECT="$installation"
  expect_status "$status" 0 "a named project"
  expect_contains "$fugaro_calls" "fugaro doctor --project $installation --all (in $dir/sandbox)" "a named project: the doctor read names it"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox" "a named project: the run is still launched"
  expect_contains "$fugaro_calls" "fugaro update --project $installation (in $dir/sandbox)" "a named project: the update names it"
}

test_a_doctor_without_a_project_line_is_not_measured() {
  local dir=$work/no-project
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-no-project
  expect_status "$status" 2 "a doctor with no project line"
  expect_contains "$out" "FACT installation: FAIL" "a doctor with no project line: the row"
  expect_contains "$err" "could NOT measure installation" "a doctor with no project line: it is named as unmeasured"
  expect_absent "$fugaro_calls" "fugaro run " "a doctor with no project line: no run is launched"
  expect_absent "$fugaro_calls" "fugaro update" "a doctor with no project line: no update is made"
}

test_an_installation_already_current_is_informational() {
  local dir=$work/current
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctorjson-none
  expect_status "$status" 0 "an installation already current"
  expect_contains "$out" "FACT version-before: INFO" "an installation already current: the row is informational"
  expect_contains "$out" "converges nothing" "an installation already current: the row says the update converges nothing"
  expect_contains "$out" "FACT update: PASS" "an installation already current: the update still runs and passes"
}

test_a_doctor_read_that_does_not_answer_is_not_measured() {
  local dir=$work/doctorjson-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctorjson-fail
  expect_status "$status" 2 "a doctor read that does not answer"
  expect_contains "$err" "could NOT measure version-before" "a doctor read that does not answer: it is named as unmeasured"
  expect_contains "$out" "FACT version-before: FAIL" "a doctor read that does not answer: the row"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launch-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_contains "$out" "fugaro run exited 3" "a launch that fails: the row quotes the exit"
  expect_absent "$fugaro_calls" "fugaro update" "a launch that fails: no update is made without a run in flight"
}

test_a_run_already_ended_before_the_update_is_not_measured() {
  local dir=$work/run-short
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-short
  expect_status "$status" 2 "a run already ended before the update"
  expect_contains "$out" "FACT run-in-flight: FAIL" "a run already ended: the row"
  expect_contains "$out" "Raise CHECK632_TASK" "a run already ended: the row names the fix"
  expect_absent "$fugaro_calls" "fugaro update" "a run already ended: no update is made"
}

test_an_update_that_fails_is_a_finding() {
  local dir=$work/update-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-fail
  expect_status "$status" 1 "an update that fails"
  expect_contains "$out" "FACT update: FAIL" "an update that fails: the row"
  expect_contains "$out" "fugaro update exited 4" "an update that fails: the row quotes the exit"
  expect_contains "$out" "FACT converge-model: FAIL" "an update that fails: the row below is unmeasured, not passed"
  expect_contains "$err" "could NOT measure update-notes-runs" "an update that fails: the rows below are named as unmeasured"
}

test_an_update_that_does_not_finish_is_not_measured() {
  local dir=$work/update-slow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-slow CHECK632_UPDATE_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "an update that does not finish"
  expect_contains "$out" "FACT update: FAIL" "an update that does not finish: the row"
  expect_contains "$out" "did not finish inside 1s" "an update that does not finish: the row names the bound"
}

test_an_update_with_no_in_flight_line_is_a_finding() {
  local dir=$work/update-no-note
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-no-note
  expect_status "$status" 1 "an update with no in-flight line"
  expect_contains "$out" "FACT update-notes-runs: FAIL" "an update with no in-flight line: the row"
  expect_contains "$out" "printed no line carrying the in-flight note" "an update with no in-flight line: the row says what is missing"
}

test_an_update_asking_for_another_command_is_a_finding() {
  local dir=$work/update-operator
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-operator-step
  expect_status "$status" 1 "an update asking for another command"
  expect_contains "$out" "FACT update-notes-runs: PASS" "an update asking for another command: the in-flight line did print"
  expect_contains "$out" "FACT no-operator-step: FAIL" "an update asking for another command: the row"
  expect_contains "$out" "to finish the upgrade" "an update asking for another command: the row quotes the line verbatim"
}

test_a_crossing_pool_left_after_the_update_is_a_finding() {
  local dir=$work/crossing-after
  prepare "$dir"
  run_check "$dir" FAKE_MODE=crossing-after
  expect_status "$status" 1 "a crossing pool left after the update"
  expect_contains "$out" "FACT converge-crossing: FAIL" "a crossing pool left after the update: the row"
  expect_contains "$out" "still names members of both halves after fugaro update" "a crossing pool left after the update: the row says so"
}

test_a_crossing_pool_split_into_two_is_a_pass() {
  local dir=$work/crossing-before
  prepare "$dir"
  run_check "$dir" FAKE_MODE=crossing-before
  expect_status "$status" 0 "a crossing pool split into two"
  expect_contains "$out" "FACT converge-crossing: PASS" "a crossing pool split: the row"
  expect_contains "$out" "the crossing pool coder-pool (deepseek-key,claude-oauth-work) held before the update is two pools now" "a crossing pool split: the row names the crossing and the halves"
}

test_a_node_that_does_not_read_back_is_a_finding() {
  local dir=$work/node-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-fail
  expect_status "$status" 1 "a node that does not read back"
  expect_contains "$out" "FACT converge-publish: FAIL" "a node that does not read back: the row"
}

test_a_node_version_that_went_down_is_a_finding() {
  local dir=$work/node-old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-old
  expect_status "$status" 1 "a node version that went down"
  expect_contains "$out" "FACT converge-publish: FAIL" "a node version that went down: the row"
  expect_contains "$out" "version went DOWN over the update" "a node version that went down: the row says so"
}

test_a_doctor_read_that_is_no_document_is_not_measured() {
  local dir=$work/doctorjson-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctorjson-garbage
  expect_status "$status" 2 "a doctor read that is no document"
  expect_contains "$err" "could NOT measure version-before" "a doctor read that is no document: it is named as unmeasured"
  expect_contains "$out" "FACT version-before: FAIL" "a doctor read that is no document: the row"
}

test_a_node_that_is_no_document_is_a_finding() {
  local dir=$work/node-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-garbage
  expect_status "$status" 1 "a node that is no document"
  expect_contains "$out" "FACT converge-publish: FAIL" "a node that is no document: the row"
  expect_contains "$out" "printed a document this check could not read" "a node that is no document: the row says so"
}

test_a_launch_that_is_no_document_is_a_finding() {
  local dir=$work/launch-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-garbage
  expect_status "$status" 1 "a launch that is no document"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that is no document: the row"
  expect_contains "$out" "printed a document this check could not read" "a launch that is no document: the row says so"
  expect_absent "$fugaro_calls" "fugaro update" "a launch that is no document: no update is made without a run in flight"
}

test_a_record_that_is_no_document_is_a_finding() {
  local dir=$work/record-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-garbage
  expect_status "$status" 1 "a record that is no document"
  expect_contains "$out" "FACT record-intact: FAIL" "a record that is no document: the row"
  expect_contains "$out" "is not a document this check could read" "a record that is no document: the row says so"
}

test_a_credentials_read_that_does_not_answer_is_not_measured() {
  local dir=$work/creds-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=creds-fail
  expect_status "$status" 2 "a credentials read that does not answer"
  expect_contains "$out" "FACT converge-key: FAIL" "a credentials read that does not answer: the row"
  expect_contains "$err" "could NOT measure converge-key" "a credentials read that does not answer: it is named as unmeasured"
}

test_no_credential_declared_is_informational() {
  local dir=$work/creds-none
  prepare "$dir"
  run_check "$dir" FAKE_MODE=creds-none
  expect_status "$status" 0 "no credential declared"
  expect_contains "$out" "FACT converge-key: INFO" "no credential declared: the row is informational"
}

test_no_craft_model_declared_is_informational() {
  local dir=$work/craft-none
  prepare "$dir"
  run_check "$dir" FAKE_MODE=craft-none
  expect_status "$status" 0 "no craft model declared"
  expect_contains "$out" "FACT converge-model: INFO" "no craft model declared: the row is informational"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-never-ends CHECK632_RUN_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$err" "could NOT measure run-terminal" "a run that never ends: it is named as unmeasured"
  expect_contains "$out" "had not reached a terminal state after 2s" "a run that never ends: the row names the bound"
  expect_contains "$out" "is not cancelled by stopping here" "a run that never ends: the row says the run is left alone"
}

test_a_ledger_that_lost_the_run_is_a_finding() {
  local dir=$work/ledger-lost
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ledger-lost
  expect_status "$status" 1 "a ledger that lost the run"
  expect_contains "$out" "FACT ledger-intact: FAIL" "a ledger that lost the run: the row"
  expect_contains "$out" "belonged to chain $run_id before the update and to $other_run" "a ledger that lost the run: the row prints both chains"
}

test_a_counter_reset_is_a_finding() {
  local dir=$work/counter-reset
  prepare "$dir"
  run_check "$dir" FAKE_MODE=counter-reset
  expect_status "$status" 1 "a counter reset"
  expect_contains "$out" "FACT counters-intact: FAIL" "a counter reset: the row"
  expect_contains "$out" "counters went DOWN over the update" "a counter reset: the row says so"
}

test_a_record_that_lost_its_outcome_is_a_finding() {
  local dir=$work/record-lost
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-lost
  expect_status "$status" 1 "a record that lost its outcome"
  expect_contains "$out" "FACT record-intact: FAIL" "a record that lost its outcome: the row"
  expect_contains "$out" "carries no outcome" "a record that lost its outcome: the row says what is missing"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/record-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-fail
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT record-intact: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read" "a record that cannot be read: the row names the key"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK632_SANDBOX=""
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "Check 632 needs --sandbox" "a missing sandbox: the message"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  expect_absent "$fugaro_calls" "fugaro run " "a path that is not a checkout: no run is launched"
}

test_a_missing_project_is_not_measured() {
  local dir=$work/no-project-line
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  printf 'agent:\n  rigor: standard\n' >"$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a checkout with no project line"
  expect_contains "$err" "could NOT measure installation" "a checkout with no project line: it is named as unmeasured"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing" "a traced shell: the message"
  expect_absent "$fugaro_calls" "fugaro run " "a traced shell: no run is launched"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$out" "CLAUDECODE is set" "a coding agent's session: the marker is named"
  expect_absent "$fugaro_calls" "fugaro run " "a coding agent's session: no run is launched"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK632_PROJECT=belong-sandbox
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a project naming belong: the row"
  expect_contains "$out" "names belong" "a project naming belong: the message says so by name"
  expect_absent "$fugaro_calls" "fugaro run " "a project naming belong: no run is launched"
}

test_an_installation_naming_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-belong
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "belongs to belong-sandbox" "an installation naming belong: the message names the project"
  expect_absent "$fugaro_calls" "fugaro run " "an installation naming belong: no run is launched"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK632_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$out" "is still there, at $scratch" "a kept scratch directory: the row names the directory"
  expect_contains "$err" "CHECK632_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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
t test_a_doctor_without_a_project_line_is_not_measured
t test_an_installation_already_current_is_informational
t test_a_doctor_read_that_does_not_answer_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_run_already_ended_before_the_update_is_not_measured
t test_an_update_that_fails_is_a_finding
t test_an_update_that_does_not_finish_is_not_measured
t test_an_update_with_no_in_flight_line_is_a_finding
t test_an_update_asking_for_another_command_is_a_finding
t test_a_crossing_pool_left_after_the_update_is_a_finding
t test_a_crossing_pool_split_into_two_is_a_pass
t test_a_node_that_does_not_read_back_is_a_finding
t test_a_node_version_that_went_down_is_a_finding
t test_a_doctor_read_that_is_no_document_is_not_measured
t test_a_node_that_is_no_document_is_a_finding
t test_a_launch_that_is_no_document_is_a_finding
t test_a_record_that_is_no_document_is_a_finding
t test_a_credentials_read_that_does_not_answer_is_not_measured
t test_no_credential_declared_is_informational
t test_no_craft_model_declared_is_informational
t test_a_run_that_never_ends_is_not_measured
t test_a_ledger_that_lost_the_run_is_a_finding
t test_a_counter_reset_is_a_finding
t test_a_record_that_lost_its_outcome_is_a_finding
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_missing_project_is_not_measured
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_an_installation_naming_belong_stops_the_check
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check632_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check632_test.sh: ok"
