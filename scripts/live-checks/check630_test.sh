#!/usr/bin/env bash
# Tests for scripts/live-checks/check630.sh, with a fake `fugaro` on PATH:
# NOTHING here reaches a cloud, an installation or a policy node, no pool is
# declared anywhere, no update runs, and the fake records every call it is given
# — in which directory it was made — so a case can assert what the check did and
# did not do (and that the dry run neither asked the write door nor updated
# anything).
#
# The model the fake carries is one installation at the current release: its
# config declares a pool per half and no crossing, its node reads back, and both
# crossing doors refuse the list the check hands them with the one sentence. The
# pools listing and the node's read answer differently once `update` has been
# called, because the check reads each of them before and after it. The cases
# that matter are the ones where that model is bent: an installation whose node
# still carries a crossing pool and whose config does too (so the update has
# something to split), a write door that accepts the crossing list, a refusal
# that names neither member, an installation with no member of one half to
# cross, a node that reads back still crossing, a node whose version goes down,
# a split missing a half and a split whose members are not in the order the
# crossed pool listed them.
#
# Usage: bash scripts/live-checks/check630_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$here/lib}
script=$here/check630.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK630_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK630_TEST_HEAD:-2000}")" ;;
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

project=fugaro-chk630-test
installation=chk630
runner_member=openrouter-x
credsvc_member=anthropic-y
oauth_member=claude-oauth-work
pool_name=chk630-crossing
crossed_pool=mixed

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check630.sh's test only. The
# doctor read, the credentials listing, the pools listing, the node's read (and
# its refusal), the write door's answer and the update all come from files the
# case wrote or from the mode it set, and every call is logged with the
# directory it was made in.
#
# The FIRST thing it does is the strict check: the command and every flag are
# held against the real CLI's own surface (scripts/live-checks/lib/), so a case
# that passes this fake a flag `fugaro` does not take is refused here rather
# than at the owner's terminal.
#
# The pools listing and the node's read answer differently once `update` has been
# called — the check reads each of them before and after it — and the mode names
# which side is bent: an `*-after*` mode bends the read that follows the update,
# a `*-before*` one bends the read before it.
set -euo pipefail
. "$LIVE_CHECK_LIB/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
pools_mode=${FAKE_POOLS_MODE:-$mode}
runner=${FAKE_RUNNER:?the test passes the member names}
credsvc=${FAKE_CREDSVCC:?}
pool=${FAKE_POOL:?}
updated=no
[ -f "$FAKE_STATE/updated" ] && updated=yes

crossing_sentence() { # crossing_sentence SVC RUN
  printf "fugaro: pools.%s.members[1]: members %s and %s are served by different halves of a run (%s's type is served by the credentials service, %s's by the run's own gateway), and a pool's order can therefore mean nothing: name two pools, one per half, and let each craft's access name the one its stages need\n" "$pool" "$1" "$2" "$1" "$2" >&2
}

node_read() {
  case "$mode" in
  node-fail-before)
    if [ "$updated" = no ]; then
      echo "fugaro: this installation has no published policy node" >&2
      exit 1
    fi
    cat "$FAKE_JSON_DIR/node.json"
    ;;
  node-garbage-before)
    if [ "$updated" = no ]; then printf 'not a document\n'; exit 0; fi
    cat "$FAKE_JSON_DIR/node.json"
    ;;
  node-refuse-other)
    if [ "$updated" = no ]; then
      printf 'fugaro: the policy node of this installation is not one this release reads\n' >&2
      exit 1
    fi
    cat "$FAKE_JSON_DIR/node.json"
    ;;
  node-crossing-ok) cat "$FAKE_JSON_DIR/node-still-crossing.json" ;;
  node-premise)
    if [ "$updated" = yes ]; then
      cat "$FAKE_JSON_DIR/node-after-split.json"
    else
      crossing_sentence "$credsvc" "$runner"
      exit 1
    fi
    ;;
  node-fail-after)
    if [ "$updated" = yes ]; then
      echo "fugaro: the policy node was written by another release" >&2
      exit 1
    fi
    cat "$FAKE_JSON_DIR/node.json"
    ;;
  node-still-crossing)
    if [ "$updated" = yes ]; then cat "$FAKE_JSON_DIR/node-still-crossing.json"; else cat "$FAKE_JSON_DIR/node.json"; fi
    ;;
  node-version-down)
    if [ "$updated" = yes ]; then cat "$FAKE_JSON_DIR/node-version-down.json"; else cat "$FAKE_JSON_DIR/node.json"; fi
    ;;
  *) cat "$FAKE_JSON_DIR/node.json" ;;
  esac
}

pools_read() {
  case "$pools_mode" in
  pools-fail)
    echo "fugaro: cannot read the config" >&2
    exit 1
    ;;
  pool-premise) [ "$updated" = yes ] && cat "$FAKE_JSON_DIR/pools-split.txt" || cat "$FAKE_JSON_DIR/pools-pre.txt" ;;
  pools-split-one-half) [ "$updated" = yes ] && cat "$FAKE_JSON_DIR/pools-split-one-half.txt" || cat "$FAKE_JSON_DIR/pools-pre.txt" ;;
  pools-split-reordered) [ "$updated" = yes ] && cat "$FAKE_JSON_DIR/pools-split-reordered.txt" || cat "$FAKE_JSON_DIR/pools-pre.txt" ;;
  pools-still-crossing) [ "$updated" = yes ] && cat "$FAKE_JSON_DIR/pools-still-crossing.txt" || cat "$FAKE_JSON_DIR/pools-pre.txt" ;;
  *) cat "$FAKE_JSON_DIR/pools.txt" ;;
  esac
  [ -f "$FAKE_JSON_DIR/pools-written-on.json" ] && cat "$FAKE_JSON_DIR/pools-written-on.json"
  return 0
}

case "${1:-}" in
version) printf '0.28.0\n' ;;
doctor)
  case "$mode" in
  doctor-belong)
    printf 'fugaro 0.28.0\nproject: chk630 (GCP belong-sandbox, us-east5)\n'
    exit 0
    ;;
  doctor-fail)
    echo "fugaro: cannot read the installation" >&2
    exit 1
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
    creds-no-pair) cat "$FAKE_JSON_DIR/creds-no-pair.json" ;;
    *) cat "$FAKE_JSON_DIR/creds.json" ;;
    esac
    ;;
  pool)
    case "${3:-}" in
    add)
      case "$mode" in
      add-accepts)
        printf 'pool %s declared\n' "${4:-}"
        cp "$FAKE_JSON_DIR/pools-written.txt" "$FAKE_JSON_DIR/pools-written-on.json"
        exit 0
        ;;
      add-refuses-partial)
        printf 'fugaro: pools.%s: a pool is served on one path, and this list names both\n' "${4:-}" >&2
        exit 1
        ;;
      *)
        crossing_sentence "${6:-}" "${5:-}"
        exit 1
        ;;
      esac
      ;;
    ls) pools_read ;;
    *)
      echo "fugaro: unexpected pool subcommand" >&2
      exit 1
      ;;
    esac
    ;;
  *)
    echo "fugaro: unexpected accounts subcommand" >&2
    exit 1
    ;;
  esac
  ;;
project)
  [ "${2:-}" = "policy" ] && [ "${3:-}" = "show" ] || { echo "fugaro: unexpected project subcommand" >&2; exit 1; }
  node_read
  ;;
validate)
  case "$mode" in
  validate-problems)
    printf '{"valid":false,"problems":[{"path":"agent.models.coder","message":"names a model the project does not define"}]}\n'
    exit 1
    ;;
  esac
  printf '{"valid":true,"problems":[]}\n'
  ;;
update)
  case "$mode" in
  update-fail)
    echo "fugaro: Terraform refused the apply" >&2
    exit 1
    ;;
  update-slow)
    sleep 30
    printf 'converged\n'
    exit 0
    ;;
  esac
  : >"$FAKE_STATE/updated"
  printf 'update: 12 steps converged\n'
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
# `fugaro accounts ls --json` (internal/cli/accountsListDoc): the repository, the
# accounts, and the credentials block with the half that serves each type, spelled
# as the config constant (config.CredentialServingPath): "credsvc" or "runner".
write_creds() { # json-dir
  cat >"$1/creds.json" <<JSON
{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":[{"name":"$runner_member","type":"openrouter","serving_path":"runner"},{"name":"$credsvc_member","type":"anthropic","serving_path":"credsvc"},{"name":"$oauth_member","type":"claude-oauth","serving_path":"credsvc"}]}
JSON
  cat >"$1/creds-no-pair.json" <<JSON
{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":[{"name":"$oauth_member","type":"claude-oauth","serving_path":"credsvc"}]}
JSON
}

# The pools listing, in the shape `writeAccountsPoolLs` prints: a tabwriter
# table padded with spaces, members joined with ", " and the crossing's own
# SERVED BY text ("... and ...") for a pool whose members span the two halves.
write_pools() { # json-dir
  cat >"$1/pools.txt" <<TXT
pools for project $installation (offline)

NAME         STRATEGY  MEMBERS            SERVED BY
coder-pool   failover  $runner_member     a run's own gateway
claude-pool  failover  $oauth_member  the credentials service
TXT
  cat >"$1/pools-pre.txt" <<TXT
pools for project $installation (offline)

NAME   STRATEGY     MEMBERS                                           SERVED BY
$crossed_pool  round-robin  $runner_member, $credsvc_member, $oauth_member  the credentials service and a run's own gateway: failover at a stage boundary
TXT
  cat >"$1/pools-written.txt" <<TXT
$pool_name  failover  $runner_member, $credsvc_member  the credentials service and a run's own gateway: failover at a stage boundary
TXT
  cat >"$1/pools-split.txt" <<TXT
pools for project $installation (offline)

NAME             STRATEGY     MEMBERS                            SERVED BY
$crossed_pool-claude    round-robin  $credsvc_member, $oauth_member  the credentials service
$crossed_pool-provider  round-robin  $runner_member                 a run's own gateway
TXT
  cat >"$1/pools-split-one-half.txt" <<TXT
pools for project $installation (offline)

NAME             STRATEGY     MEMBERS         SERVED BY
$crossed_pool-claude    round-robin  $credsvc_member, $oauth_member  the credentials service
TXT
  cat >"$1/pools-split-reordered.txt" <<TXT
pools for project $installation (offline)

NAME             STRATEGY     MEMBERS                            SERVED BY
$crossed_pool-claude    round-robin  $oauth_member, $credsvc_member  the credentials service
$crossed_pool-provider  round-robin  $runner_member                 a run's own gateway
TXT
  cat >"$1/pools-still-crossing.txt" <<TXT
pools for project $installation (offline)

NAME             STRATEGY     MEMBERS                            SERVED BY
$crossed_pool-claude    round-robin  $credsvc_member, $oauth_member  the credentials service
$crossed_pool-provider  round-robin  $runner_member                 a run's own gateway
$crossed_pool           round-robin  $runner_member, $credsvc_member  the credentials service and a run's own gateway: failover at a stage boundary
TXT
}

# Each node document is what `fugaro project policy show --json` prints: the
# node's blocks are members of `policy`, under the object's project.
write_nodes() { # json-dir
  cat >"$1/node.json" <<JSON
{"project":"$installation","policy":{"version":10,"craft_credentials":[{"name":"$runner_member","type":"openrouter"},{"name":"$credsvc_member","type":"anthropic"},{"name":"$oauth_member","type":"claude-oauth"}],"craft_pools":[{"name":"coder-pool","strategy":"failover","members":["$runner_member"]},{"name":"claude-pool","strategy":"failover","members":["$oauth_member"]}],"crafts":{"coder":{"model":"deepseek/deepseek-v4","access":"coder-pool"}}}}
JSON
  cat >"$1/node-after-split.json" <<JSON
{"project":"$installation","policy":{"version":11,"craft_credentials":[{"name":"$runner_member","type":"openrouter"},{"name":"$credsvc_member","type":"anthropic"},{"name":"$oauth_member","type":"claude-oauth"}],"craft_pools":[{"name":"$crossed_pool-claude","strategy":"round-robin","members":["$credsvc_member","$oauth_member"]},{"name":"$crossed_pool-provider","strategy":"round-robin","members":["$runner_member"]}],"crafts":{"coder":{"model":"deepseek/deepseek-v4","access":"$crossed_pool-provider"}}}}
JSON
  cat >"$1/node-still-crossing.json" <<JSON
{"project":"$installation","policy":{"version":11,"craft_credentials":[{"name":"$runner_member","type":"openrouter"},{"name":"$credsvc_member","type":"anthropic"}],"craft_pools":[{"name":"$crossed_pool","strategy":"failover","members":["$runner_member","$credsvc_member"]}],"crafts":{"coder":{"model":"deepseek/deepseek-v4","access":"$crossed_pool"}}}}
JSON
  cat >"$1/node-version-down.json" <<JSON
{"project":"$installation","policy":{"version":8,"craft_credentials":[{"name":"$runner_member","type":"openrouter"},{"name":"$credsvc_member","type":"anthropic"}],"craft_pools":[{"name":"coder-pool","strategy":"failover","members":["$runner_member"]}],"crafts":{"coder":{"model":"deepseek/deepseek-v4","access":"coder-pool"}}}}
JSON
}

write_docs() { # dir
  local json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.txt" <<TXT
fugaro 0.28.0
project: $installation (GCP $project, us-east5)
repository: acme/sandbox
TXT
  write_creds "$json"
  write_pools "$json"
  write_nodes "$json"
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir [PROJECT]
  mkdir -p "$1"
  [ -z "${2:-}" ] || printf 'project: %s\n' "$2" >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox" "$installation"
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
  rm -f "$dir/state"/* "$dir/json/pools-written-on.json"
  : >"$dir/logs/fugaro.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to start a traced shell.
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
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" FAKE_STATE="$dir/state" \
    FAKE_RUNNER="$runner_member" FAKE_CREDSVCC="$credsvc_member" FAKE_POOL="$crossed_pool" \
    CHECK630_SANDBOX="$dir/sandbox" \
    CHECK630_POOL_NAME="$pool_name" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  fugaro_calls=$(cat "$dir/logs/fugaro.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 630" "help: the header"
  expect_contains "$out" "What it measures" "help: the docblock"
  expect_contains "$out" "check630.sh --sandbox" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK630_*)" "help: the knobs"
  expect_contains "$out" "a coding agent's session is refused by" "help: the session guard"
}

test_dry_run() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK630_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT refused-at-add: INFO" "a dry run: the planned rows are not measured"
  expect_contains "$err" "[write] fugaro update" "a dry run: the plan names the update it would make"
  expect_absent "$fugaro_calls" "accounts pool add" "a dry run: the write door is not asked"
  expect_absent "$fugaro_calls" "fugaro update" "a dry run: nothing is updated"
  assert_scratch_removed "a dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in fugaro-version xtrace agent-session belong installation refused-at-add update converged-node restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_contains "$out" "FACT converged-pools: INFO" "the happy path: with no crossing before it, the update had nothing to split"
  expect_contains "$out" "There was no crossing pool before the update" "the happy path: the converged-pools row says why the clause had no subject"
  expect_contains "$out" "FACT node-crossing: INFO" "the happy path: a node with no crossing has nothing for its door to refuse"
  expect_contains "$out" "carries no crossing pool, so this door had nothing to refuse" "the happy path: the node's read says why it had nothing to refuse"
  expect_contains "$out" "The rule itself is held by config.ValidatePublishedPools, which projectpolicy.Decode runs" "the happy path: the row names the real door"
  expect_contains "$out" "FACT validate: INFO" "the happy path: the command the acceptance names is not the crossing's door, and the row says so"
  expect_contains "$out" "it reads a repository's fugaro.yaml" "the validate row says what the command really reads"
  expect_contains "$out" "The crossing's second door in this build is the read of a PUBLISHED node" "the validate row names the crossing's real second door"
  expect_contains "$out" "FACT config-crossing: INFO" "the happy path: a config with no crossing has no premise"
  expect_absent "$out" ": FAIL" "the happy path: no row failed"
  expect_contains "$out" "refused a pool naming $runner_member (an OpenRouter member) and $credsvc_member (an Anthropic member) with the crossing's one sentence" "the happy path: the write door refused with the one sentence"
  expect_contains "$out" "and the pool is not in the listing afterwards" "the happy path: the refusal wrote nothing"
  expect_contains "$out" "CHECK 630 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$fugaro_calls" "fugaro accounts pool add $pool_name $runner_member $credsvc_member (in $dir/sandbox)" "the happy path: the door is asked for the acceptance's own pair"
  expect_contains "$fugaro_calls" "fugaro update (in $dir/sandbox)" "the happy path: the update runs from the checkout"
  expect_contains "$fugaro_calls" "fugaro doctor --all (in $dir/sandbox)" "the happy path: the installation is read from the checkout"
  expect_contains "$fugaro_calls" "fugaro validate --json (in $dir/sandbox)" "the happy path: validate is run from the checkout"
  assert_scratch_removed "the happy path"
}

test_the_project_flag_is_passed_through() {
  local dir=$work/with-project
  prepare "$dir"
  run_check "$dir" CHECK630_PROJECT="$installation"
  expect_status "$status" 0 "a named project"
  expect_contains "$fugaro_calls" "fugaro accounts pool ls --project $installation" "a named project: the pools read names it"
  expect_contains "$fugaro_calls" "fugaro update --project $installation" "a named project: the update names it"
}

test_an_installation_that_held_a_crossing_is_converged() {
  local dir=$work/premise
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-premise FAKE_POOLS_MODE=pool-premise
  expect_status "$status" 0 "an installation that held a crossing"
  expect_contains "$out" "FACT node-crossing: PASS" "an installation that held a crossing: the node's own door refused it"
  expect_contains "$out" "FACT config-crossing: PASS" "an installation that held a crossing: the config still holds one"
  expect_contains "$out" "the read of the published node refused it, and it is the crossing's one sentence" "an installation that held a crossing: the refusal is the sentence"
  expect_contains "$out" "still holds a crossing pool: $crossed_pool" "an installation that held a crossing: the config-side premise"
  expect_contains "$out" "is two pools after the update: $crossed_pool-claude carries $credsvc_member,$oauth_member" "an installation that held a crossing: the credentials service's half keeps its own members"
  expect_contains "$out" "and $crossed_pool-provider carries $runner_member (the run's own gateway's), each member on its own path" "an installation that held a crossing: the runner's half keeps its own"
  expect_contains "$out" "the published node of $installation reads back after the update with no crossing pool" "an installation that held a crossing: the node reads back clean"
  expect_contains "$out" "split the crossing pool $crossed_pool" "an installation that held a crossing: the restore row names the split"
  expect_absent "$out" "FACT converged-pools: FAIL" "an installation that held a crossing: the split held"
}

test_a_write_door_that_accepts_the_crossing_is_a_finding() {
  local dir=$work/add-accepts
  prepare "$dir"
  run_check "$dir" FAKE_MODE=add-accepts
  expect_status "$status" 1 "a write door that accepts the crossing"
  expect_contains "$out" "FACT refused-at-add: FAIL" "a write door that accepts the crossing: the row"
  expect_contains "$out" "ACCEPTED the crossing list and wrote the pool" "a write door that accepts the crossing: the row says so"
  expect_contains "$out" "fugaro accounts pool rm $pool_name" "a write door that accepts the crossing: the remedy is a command"
}

test_a_refusal_without_the_sentence_is_a_finding() {
  local dir=$work/add-partial
  prepare "$dir"
  run_check "$dir" FAKE_MODE=add-refuses-partial
  expect_status "$status" 1 "a refusal without the one sentence"
  expect_contains "$out" "FACT refused-at-add: FAIL" "a refusal without the one sentence: the row"
  expect_contains "$out" "does not name the one sentence ('name two pools, one per half')" "a refusal without the one sentence: the row names what is missing"
  expect_contains "$out" "the OpenRouter member $runner_member" "a refusal without the one sentence: the row names the member it should have named"
}

test_an_installation_with_no_pair_to_cross_is_not_measured() {
  local dir=$work/no-pair
  prepare "$dir"
  run_check "$dir" FAKE_MODE=creds-no-pair
  expect_status "$status" 2 "an installation with no pair to cross"
  expect_contains "$out" "FACT refused-at-add: FAIL" "an installation with no pair to cross: the row"
  expect_contains "$out" "declares no pair to cross" "an installation with no pair to cross: the row says what is missing"
  expect_contains "$err" "could NOT measure refused-at-add" "an installation with no pair to cross: it is named as unmeasured"
  expect_absent "$fugaro_calls" "accounts pool add" "an installation with no pair to cross: the door is not asked"
}

test_a_credentials_read_that_does_not_answer_is_not_measured() {
  local dir=$work/creds-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=creds-fail
  expect_status "$status" 2 "a credentials read that does not answer"
  expect_contains "$err" "could NOT measure refused-at-add" "a credentials read that does not answer: it is named as unmeasured"
  expect_absent "$fugaro_calls" "accounts pool add" "a credentials read that does not answer: the door is not asked"
}

test_a_node_that_crosses_without_being_refused_is_a_finding() {
  local dir=$work/node-crossing-ok
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-crossing-ok
  expect_status "$status" 1 "a node that crosses and is not refused"
  expect_contains "$out" "FACT node-crossing: FAIL" "a node that crosses and is not refused: the row"
  expect_contains "$out" "still carries a crossing pool ($crossed_pool($credsvc_member, $runner_member) ) and this build's read of it did NOT refuse it" "a node that crosses and is not refused: the row names the members in path order"
}

test_a_node_read_that_does_not_answer_is_not_measured() {
  local dir=$work/node-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-fail-before
  expect_status "$status" 2 "a node read that does not answer"
  expect_contains "$err" "could NOT measure node-crossing" "a node read that does not answer: it is named as unmeasured"
}

test_a_node_that_is_no_document_is_not_measured() {
  local dir=$work/node-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-garbage-before
  expect_status "$status" 2 "a node that is no document"
  expect_contains "$err" "could NOT measure node-crossing" "a node that is no document: it is named as unmeasured"
}

test_a_node_refusal_that_is_not_the_crossing_is_not_measured() {
  local dir=$work/node-refuse-other
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-refuse-other
  expect_status "$status" 2 "a node refusal that is not the crossing"
  expect_contains "$err" "could NOT measure node-crossing" "a node refusal that is not the crossing: it is named as unmeasured"
  expect_contains "$out" "its words are not the crossing's own sentence" "a node refusal that is not the crossing: the row says why"
}

test_a_pools_read_that_does_not_answer_is_not_measured() {
  local dir=$work/pools-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=pools-fail
  expect_status "$status" 2 "a pools read that does not answer"
  expect_contains "$err" "could NOT measure config-crossing, converged-pools" "a pools read that does not answer: both config-side rows are named as unmeasured"
}

test_a_config_still_crossing_after_the_update_is_a_finding() {
  local dir=$work/pools-still-crossing
  prepare "$dir"
  run_check "$dir" FAKE_POOLS_MODE=pools-still-crossing
  expect_status "$status" 1 "a config still crossing after the update"
  expect_contains "$out" "FACT converged-pools: FAIL" "a config still crossing after the update: the row"
  expect_contains "$out" "still holds a pool whose members span the two halves after fugaro update" "a config still crossing after the update: the row says so"
}

test_a_split_missing_a_half_is_a_finding() {
  local dir=$work/split-one-half
  prepare "$dir"
  run_check "$dir" FAKE_POOLS_MODE=pools-split-one-half
  expect_status "$status" 1 "a split missing a half"
  expect_contains "$out" "FACT converged-pools: FAIL" "a split missing a half: the row"
  expect_contains "$out" "$crossed_pool-provider (the run's own gateway's half)" "a split missing a half: the row names the half that is missing"
}

test_a_split_whose_members_are_reordered_is_a_finding() {
  local dir=$work/split-reordered
  prepare "$dir"
  run_check "$dir" FAKE_POOLS_MODE=pools-split-reordered
  expect_status "$status" 1 "a split whose members are reordered"
  expect_contains "$out" "FACT converged-pools: FAIL" "a split whose members are reordered: the row"
  expect_contains "$out" "$crossed_pool-claude lists $oauth_member,$credsvc_member where the crossed pool's own members on that path are $credsvc_member,$oauth_member" "a split whose members are reordered: the row prints both orders"
}

test_a_node_still_crossing_after_the_update_is_a_finding() {
  local dir=$work/node-after-crossing
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-still-crossing
  expect_status "$status" 1 "a node still crossing after the update"
  expect_contains "$out" "FACT converged-node: FAIL" "a node still crossing after the update: the row"
  expect_contains "$out" "still carries a crossing pool after the update" "a node still crossing after the update: the row says so"
  expect_contains "$out" "migrateV8ToV9" "a node still crossing after the update: the row names the step that should have split it"
}

test_a_node_whose_version_moves_down_is_a_finding() {
  local dir=$work/node-version-down
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-version-down
  expect_status "$status" 1 "a node whose version moves down"
  expect_contains "$out" "FACT converged-node: FAIL" "a node whose version moves down: the row"
  expect_contains "$out" "went DOWN over the update (10 before, 8 after)" "a node whose version moves down: the row prints both versions"
}

test_a_node_that_does_not_read_back_after_the_update_is_a_finding() {
  local dir=$work/node-fail-after
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-fail-after
  expect_status "$status" 1 "a node that does not read back"
  expect_contains "$out" "FACT converged-node: FAIL" "a node that does not read back: the row"
  expect_contains "$out" "does not read back after fugaro update" "a node that does not read back: the row says so"
}

test_an_update_that_fails_is_a_finding() {
  local dir=$work/update-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-fail
  expect_status "$status" 1 "an update that fails"
  expect_contains "$out" "FACT update: FAIL" "an update that fails: the row"
  expect_contains "$out" "fugaro update exited 1" "an update that fails: the row quotes the exit"
  expect_contains "$err" "could NOT measure converged-pools, converged-node" "an update that fails: the rows below are unmeasured"
}

test_an_update_that_does_not_finish_is_not_measured() {
  local dir=$work/update-slow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-slow CHECK630_UPDATE_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "an update that does not finish"
  expect_contains "$out" "FACT update: FAIL" "an update that does not finish: the row"
  expect_contains "$out" "did not finish inside 1s" "an update that does not finish: the row names the bound"
}

test_a_doctor_read_that_does_not_answer_still_reads_the_checkout() {
  local dir=$work/doctor-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-fail
  expect_status "$status" 0 "a doctor read that does not answer"
  expect_contains "$out" "FACT installation: PASS" "a doctor read that does not answer: the installation still comes from the checkout"
  expect_contains "$out" "and its node read refused, which the node-crossing row below reads" "a doctor read that does not answer: the row says doctor refused"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK630_SANDBOX=""
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "Check 630 needs --sandbox" "a missing sandbox: the message"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  expect_absent "$fugaro_calls" "accounts pool add" "a path that is not a checkout: the door is not asked"
}

test_a_checkout_naming_no_project_is_not_measured() {
  local dir=$work/no-project
  prepare "$dir"
  : >"$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a checkout naming no project"
  expect_contains "$err" "could NOT measure installation" "a checkout naming no project: it is named as unmeasured"
  expect_absent "$fugaro_calls" "fugaro update" "a checkout naming no project: nothing is updated"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK630_POOL_NAME=belong-pool
  expect_status "$status" 1 "a pool naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a pool naming belong: the row"
  expect_contains "$out" "FACT aborted: FAIL" "a pool naming belong: the run is aborted, not measured"
  expect_contains "$out" "names belong: this check is never run there" "a pool naming belong: the message says so by name"
  expect_absent "$fugaro_calls" "fugaro update" "a pool naming belong: nothing is updated"
}

test_an_installation_naming_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-belong
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$out" "FACT aborted: FAIL" "an installation naming belong: the run is aborted"
  expect_contains "$out" "belongs to belong-sandbox" "an installation naming belong: the message names the project"
  expect_absent "$fugaro_calls" "fugaro update" "an installation naming belong: nothing is updated"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing" "a traced shell: the message"
  expect_contains "$out" "it without -x" "a traced shell: the message says what to do"
  expect_absent "$fugaro_calls" "fugaro update" "a traced shell: nothing is updated"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$out" "CLAUDECODE is set" "a coding agent's session: the marker is named"
  expect_absent "$fugaro_calls" "fugaro update" "a coding agent's session: nothing is updated"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK630_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$out" "is still there, at $scratch" "a kept scratch directory: the row names the directory"
  expect_contains "$err" "CHECK630_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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

test_preflight_passes_and_writes_nothing() {
  local dir=$work/preflight-ok
  prepare "$dir"
  run_check "$dir" -- --preflight
  expect_status "$status" 0 "a preflight with nothing blocking"
  expect_contains "$err" "preflight passed" "a preflight: the pass line"
  expect_contains "$err" "read: the installation is $installation" "a preflight: the installation it read"
  expect_contains "$err" "read: the crossing list would name" "a preflight: the pair it would hand the write door"
  expect_absent "$fugaro_calls" "accounts pool add" "a preflight: the write door is not asked"
  expect_absent "$fugaro_calls" "fugaro update" "a preflight: nothing is updated"
  expect_absent "$out" "CHECK 630 RESULT" "a preflight: no result block is printed"
  assert_scratch_removed "a preflight"
}

test_preflight_reports_every_blocker_at_once() {
  local dir=$work/preflight-blocked
  prepare "$dir"
  # The pool the write door would be asked for is already there, and the config
  # declares no member of the run's own path: two blockers, printed together.
  cat >"$dir/json/pools.txt" <<TXT
pools for project $installation (offline)

NAME         STRATEGY   MEMBERS       SERVED BY
$pool_name   failover   $oauth_member  the credentials service
TXT
  run_check "$dir" FAKE_MODE=creds-no-pair -- --preflight
  expect_status "$status" 1 "a preflight with blockers"
  expect_contains "$err" "BLOCKED refused-at-add: the config declares no OpenRouter or vertex member" "a preflight: the missing member"
  expect_contains "$err" "BLOCKED refused-at-add: the pool $pool_name is already declared" "a preflight: the pool that is already there"
  expect_contains "$err" "fix: fugaro accounts add openrouter-key --type openrouter" "a preflight: the command that declares the member"
  expect_contains "$err" "fix: fugaro accounts pool rm $pool_name" "a preflight: the command that clears the pool"
  expect_contains "$err" "found 2 blocker(s)" "a preflight: both blockers at once"
  expect_absent "$fugaro_calls" "accounts pool add" "a preflight: the write door is not asked"
  expect_absent "$fugaro_calls" "fugaro update" "a preflight: nothing is updated"
}

test_preflight_names_a_node_the_read_does_not_refuse() {
  local dir=$work/preflight-node
  prepare "$dir"
  run_check "$dir" FAKE_MODE=node-crossing-ok -- --preflight
  expect_status "$status" 1 "a preflight on a node that crosses unreffused"
  expect_contains "$err" "BLOCKED node-crossing: the published node (version 11) carries a crossing pool and this build's read did NOT refuse it" "a preflight: the node blocker"
  expect_contains "$err" "fix: fugaro update" "a preflight: the fix names the update"
  expect_absent "$fugaro_calls" "fugaro update (in" "a preflight: the update is named, never run"
}

test_preflight_is_refused_for_a_coding_agents_session() {
  local dir=$work/preflight-agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1 -- --preflight
  expect_status "$status" 1 "a coding agent's preflight"
  expect_contains "$err" "a coding agent's session" "a preflight: the session refusal"
  expect_absent "$fugaro_calls" "accounts ls --json" "a preflight: no live read is made"
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
t test_an_installation_that_held_a_crossing_is_converged
t test_a_write_door_that_accepts_the_crossing_is_a_finding
t test_a_refusal_without_the_sentence_is_a_finding
t test_an_installation_with_no_pair_to_cross_is_not_measured
t test_a_credentials_read_that_does_not_answer_is_not_measured
t test_a_node_that_crosses_without_being_refused_is_a_finding
t test_a_node_read_that_does_not_answer_is_not_measured
t test_a_node_that_is_no_document_is_not_measured
t test_a_node_refusal_that_is_not_the_crossing_is_not_measured
t test_a_pools_read_that_does_not_answer_is_not_measured
t test_a_config_still_crossing_after_the_update_is_a_finding
t test_a_split_missing_a_half_is_a_finding
t test_a_split_whose_members_are_reordered_is_a_finding
t test_a_node_still_crossing_after_the_update_is_a_finding
t test_a_node_whose_version_moves_down_is_a_finding
t test_a_node_that_does_not_read_back_after_the_update_is_a_finding
t test_an_update_that_fails_is_a_finding
t test_an_update_that_does_not_finish_is_not_measured
t test_a_doctor_read_that_does_not_answer_still_reads_the_checkout
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_checkout_naming_no_project_is_not_measured
t test_belong_is_refused_by_name
t test_an_installation_naming_belong_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_preflight_passes_and_writes_nothing
t test_preflight_reports_every_blocker_at_once
t test_preflight_names_a_node_the_read_does_not_refuse
t test_preflight_is_refused_for_a_coding_agents_session
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check630_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check630_test.sh: ok"
