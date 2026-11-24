#!/usr/bin/env bash
# Tests for scripts/live-checks/check-881-chain-falls-through.sh, with fake
# `fugaro` and fake `gcloud` on PATH: NOTHING here reaches a cloud, an
# installation, a run or a model, no agent runs a live command, and the fakes
# record every call they are given so a case can assert what the check did — and
# did not — do.
#
# The fakes are a small model of one installation after release 1: a routing
# table whose entry for one model lists the launcher's pool first and the
# project's Anthropic key after it, a pool that has nothing usable left, a run
# the check launches, and the run's own `result.json` in the runs bucket with
# the source, the fallback flag and the fallback dollars on it. Every case below
# bends one of those: a chain that does not start with the pool, a pool with a
# member still good, a member whose headroom nobody reported, a member whose
# window has reopened, an available member's next reset, a member inside a window
# still closed, a run the pool served, a step nobody flagged, fallback dollars of
# zero, a record that carries no source at all.
#
# Usage: bash scripts/live-checks/check-881-chain-falls-through_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-881-chain-falls-through.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK881_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK881_TEST_HEAD:-900}")" ;;
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
expect_row() { # out key verdict what
  local line
  line=$(printf '%s\n' "$1" | grep -m1 "^FACT $2:" || true)
  [ -n "$line" ] || {
    fail "$4: the block carries no $2 row"
    return 0
  }
  expect_contains "$line" ": $3 " "$4: the $2 row reads $3"
}

project=fugaro-chk881-test
bucket=fugaro-runs-${project}
run_id=20261122090000-b881
run_ref=chk881/${run_id}
model=claude-opus-5-5
pool=launchers-pool

# --------------------------------------------------------------------- fakes
# write_fake_fugaro DIR: a stand-in for `fugaro` that answers the reads this
# check makes out of the case's own files under DIR/answers, and records every
# call. `run` writes the launch answer (or refuses, when the case says so).
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-881's test only. Every answer is a file of
# the case's own under $FAKE881_ANSWERS; a document the case did not write is a
# refusal, so a case cannot pass by the fake inventing an answer.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE881_LOG/fugaro.calls"
mode=${FAKE881_MODE:-default}
answers=${FAKE881_ANSWERS:?}
case "${1:-}" in
version) printf '%s\n' "${FAKE881_VERSION:-0.27.0}" ;;
doctor)
  cat "$answers/doctor.json"
  ;;
routing)
  cat "$answers/table.json"
  ;;
accounts)
  cat "$answers/pools.json"
  ;;
budget)
  # The real command narrows its listing to the repository `--repo` names
  # (internal/cli/budget.go: `keys[budget.Key(wantSlug)] = true`), so the fake
  # does too: a check that forgets --repo reads whatever row the listing happens
  # to carry first, and a case can catch it.
  repo_arg=""
  prev=""
  for a in "$@"; do
    case "$a" in
    --repo=*) repo_arg=${a#--repo=} ;;
    esac
    if [ "$prev" = "--repo" ]; then repo_arg=$a; fi
    prev=$a
  done
  if [ -z "$repo_arg" ]; then
    cat "$answers/budget.json"
  else
    python3 - "$answers/budget.json" "$repo_arg" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
want = sys.argv[2]
rows = []
for r in doc.get("repos") or []:
    if not isinstance(r, dict):
        continue
    if want in (r.get("slug") or "", r.get("repo") or "") or (r.get("repo") or "").endswith("/" + want):
        rows.append(r)
doc["repos"] = rows
print(json.dumps(doc))
PY
  fi
  ;;
project)
  cat "$answers/policy.json"
  ;;
runs)
  # A case may model a run that is still going when the check first polls it:
  # with answers/runs.inflight present the FIRST poll answers that row and every
  # later poll answers runs.json. The real CLI answers a run in flight with
  # outcome "none" and terminal false — the runner writes OutcomeNone at
  # bootstrap — so the ended signal a reader may stop on is `terminal`.
  polls=$(grep -c . "$FAKE881_LOG/runs.calls" 2>/dev/null || true)
  printf 'poll\n' >>"$FAKE881_LOG/runs.calls"
  if [ -f "$answers/runs.inflight" ] && [ "${polls:-0}" -eq 0 ]; then
    cat "$answers/runs.inflight"
  else
    cat "$answers/runs.json"
  fi
  ;;
run)
  case "$mode" in
  launch-refused)
    echo "fugaro: no per-run cap is set for this repository: set one with 'fugaro budget set --repo <owner/name> --run <usd>'" >&2
    exit 2
    ;;
  esac
  cat "$answers/launch.json"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# write_fake_gcloud DIR: a stand-in for `gcloud` that answers the account read
# and the one `storage cat` of the run's record. It prints no value of anything:
# the record is a file of the case's own.
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for check-881's test only. It answers `config
# get-value account` and `storage cat` of the run's record, and nothing else.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE881_LOG/gcloud.calls"
mode=${FAKE881_MODE:-default}
case "${1:-}" in
config)
  printf 'chk881-owner@example.com\n'
  ;;
storage)
  case "$mode" in
  record-absent)
    echo "ERROR: (gcloud.storage.cat) NotFoundException: 404 no such object" >&2
    exit 1
    ;;
  esac
  cat "$FAKE881_ANSWERS/record.json"
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# write_answers DIR: the documents every case starts from, each of them the
# answer the check is about. A case overwrites the one it bends.
write_answers() { # dir
  mkdir -p "$1/answers"
  local a=$1/answers
  cat >"$a/doctor.json" <<JSON
{"ok":true,"project":{"name":"chk881","gcp_project":"${project}","region":"us-east5","registry_host":"us-east5-docker.pkg.dev"}}
JSON
  cat >"$a/table.json" <<JSON
{"entries":[
  {"match":"${model}","chain":[{"kind":"launcher-pool"},{"kind":"anthropic","name":"project-key"}]},
  {"match":"claude-*","chain":[{"kind":"anthropic","name":"project-key"}]}
]}
JSON
  cat >"$a/pools.json" <<JSON
{"pools":[
  {"name":"${pool}","rule":"failover","principal":{"kind":"person","id":"1122334455"},
   "members":[{"state":"exhausted","usage":1.0,"headroom":0,"exhausted_until":"2026-11-22T12:00:00Z"}]}
]}
JSON
  # Two repositories, and the OTHER one comes first with a cap of its own: the
  # check must read the row of the repository the checkout's origin remote names
  # (`acme/chk881`, cap $10), not the first capped row in the listing — which is
  # what a multi-repository installation would otherwise be reported as.
  cat >"$a/budget.json" <<JSON
{"project":"chk881","gcp_project":"${project}","mode":"enforce","day":"2026-11-22",
 "defaults":{"repo_daily_cap_usd":20,"repo_per_run_cap_usd":10},
 "repos":[{"slug":"chk881-other","repo":"acme/chk881-other","per_run_cap_usd":1,"per_run_cap_source":"repo",
   "daily_cap_usd":1,"daily_cap_source":"repo","counted_usd":0,"spent_usd":0,"notional_usd":0,
   "leased_usd":0,"headroom_usd":1,"running":[]},
  {"slug":"chk881","repo":"acme/chk881","per_run_cap_usd":10,"per_run_cap_source":"default",
   "daily_cap_usd":20,"daily_cap_source":"default","counted_usd":0,"spent_usd":0,"notional_usd":0,
   "leased_usd":0,"headroom_usd":20,"running":[]}],
 "global":{"daily_cap_usd":100,"per_run_cap_usd":10,"counted_usd":0,"spent_usd":0,"notional_usd":0,"leased_usd":0,"headroom_usd":100},
 "running":[],"warnings":[]}
JSON
  cat >"$a/policy.json" <<JSON
{"budget":{"mode":"enforce","caps":{"defaults":{"repo_per_run_usd":10,"repo_daily_usd":20}}}}
JSON
  cat >"$a/launch.json" <<JSON
{"run":"${run_ref}","run_id":"${run_id}"}
JSON
  cat >"$a/runs.json" <<JSON
[{"run":"${run_ref}","run_id":"${run_id}","outcome":"ready","terminal":true,"settled":true}]
JSON
  cat >"$a/record.json" <<JSON
{"repo":"acme/chk881","run":"${run_id}","status":"succeeded",
 "cost":{"model_usd":0.42,"model_notional_usd":0.0,"model_source":"gateway","fallback_usd":0.42,"total_usd":0.42},
 "policy":{"effective":{"per_run_usd":10,"mode":"enforce"}},
 "stages":[
   {"stage":"implement","model":"${model}","model_usd":0.30,"source":{"kind":"anthropic","name":"project-key"},"fallback":true},
   {"stage":"review","model":"${model}","model_usd":0.12,"source":{"kind":"anthropic","name":"project-key"},"fallback":true}
 ]}
JSON
}

run_check() { # run_check DIR [K=V ...] [-- ARGS...]
  local dir=$1
  shift
  local envs=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/runs.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures the tracing refusal is
  # the only way to start a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The check refuses a coding agent's session, and this test may itself run in
  # one: the markers are dropped for the child, and the case that measures the
  # refusal sets one back.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE881_LOG="$dir/logs" FAKE881_ANSWERS="$dir/answers" \
    FAKE881_MODE="${FAKE881_MODE:-default}" \
    ${envs[@]+"${envs[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# setup_case DIR [K=V ...]: a checkout, the two fakes and the answers, plus any
# environment the case wants written beside them.
setup_case() { # dir
  local dir=$1
  shift
  mkdir -p "$dir/home" "$dir/tmp" "$dir/checkout"
  write_fake_fugaro "$dir"
  write_fake_gcloud "$dir"
  write_answers "$dir"
  git -C "$dir/checkout" init -q
  git -C "$dir/checkout" -c user.email=t@example.com -c user.name=t commit -q --allow-empty -m init
  cat >"$dir/checkout/fugaro.yaml" <<'YAML'
version: 1
YAML
  # The origin remote is what names the repository the run is charged to and
  # whose per-run cap the budget row reads; `fugaro run` resolves it the same way.
  git -C "$dir/checkout" remote add origin git@github.com:acme/chk881.git
}

default_args() {
  printf '%s\n' --checkout "$1/checkout" --pool "$pool" --gcp-project "$project" --region us-east5
}

assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

assert_no_launch() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "run --task-file" "$2: no run is launched"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  setup_case "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "check-881-chain-falls-through.sh --dry-run" "--help: the dry run is offered"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --task-file" "--help: no run is launched"
}

test_dry_run() {
  local dir=$work/dry
  setup_case "$dir"
  run_check "$dir" CHECK881_DRY_RUN=1 -- $(default_args "$dir")
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 881 RESULT" "dry run: the block"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT table: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "CHECK 881 RESULT: PASS (exit 0)" "dry run: the verdict line"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --task-file" "dry run: no run is launched"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  # The whole check: an exhausted launcher's pool, a run whose steps the key
  # served, every one of them flagged, and the fallback money counted.
  local dir=$work/happy
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT installation: PASS" "happy path: the installation"
  expect_contains "$out" "FACT table: PASS" "happy path: the table"
  expect_contains "$out" "FACT budget: PASS" "happy path: the cap is in force"
  expect_contains "$out" "FACT first-source: PASS" "happy path: the first source cannot serve"
  expect_contains "$out" "every member of ${pool} is at its limit" "happy path: what made it unable to serve"
  expect_contains "$out" "FACT launch: PASS" "happy path: the run was launched"
  expect_contains "$out" "${run_id}" "happy path: the run it launched"
  expect_contains "$out" "FACT served-by-the-paid-source: PASS" "happy path: the key served"
  expect_contains "$out" "FACT flagged: PASS" "happy path: the step is flagged"
  expect_contains "$out" "FACT counted: PASS" "happy path: the money is counted"
  expect_contains "$out" "CHECK 881 RESULT: PASS (exit 0)" "happy path: the verdict line"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "run --task-file" "happy path: the launch command"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "storage cat gs://${bucket}/runs/${run_ref}/result.json" "happy path: the record is read out of the runs bucket"
  assert_scratch_removed "happy path"
}

test_the_block_never_carries_a_value() {
  # A source is named and never valued (CS8): the pools document carries a
  # stand-in `value`, and the block must not print it.
  local dir=$work/novalue
  setup_case "$dir"
  python3 - "$dir/answers/pools.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["pools"][0]["members"][0]["value"] = "STANDIN-NOT-A-KEY"
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_absent "$out" "STANDIN-NOT-A-KEY" "no value: the member's value"
  expect_absent "$err" "STANDIN-NOT-A-KEY" "no value: not on stderr either"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  setup_case "$dir"
  run_check "$dir" CLAUDECODE=1 -- $(default_args "$dir")
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$err" "coding agent's session" "an agent's session: the refusal"
  assert_no_launch "$dir" "an agent's session"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  setup_case "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 2 "bash -x"
  expect_contains "$err" "is tracing" "bash -x: the guard names what it refuses"
  assert_no_launch "$dir" "bash -x"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  setup_case "$dir"
  run_check "$dir" -- --checkout "$dir/checkout" --gcp-project fugaro-belong --region us-east5
  expect_status "$status" 2 "a project naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  assert_no_launch "$dir" "belong"
}

test_a_belong_installation_in_the_checkout_is_refused() {
  # The project this check goes on to use is doctor's own when --gcp-project is
  # not given, so a checkout whose installation names belong must be refused on
  # WHAT DOCTOR ANSWERS — guarding only what was typed guards the empty string
  # and would launch the paid run of the owner's own installation.
  local dir=$work/belongdoctor
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["name"] = "belong"
doc["project"]["gcp_project"] = "fugaro-belong"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout" --pool "$pool"
  expect_status "$status" 2 "a checkout whose doctor names belong"
  expect_contains "$err" "never run there" "belong in the checkout: the refusal"
  expect_contains "$out" "FACT belong: FAIL" "belong in the checkout: the row"
  expect_contains "$out" "fugaro doctor --json" "belong in the checkout: where the name came from"
  assert_no_launch "$dir" "belong in the checkout"
}

test_a_region_that_disagrees_with_the_installation_stops() {
  # Every read this check makes carries the region, so a region that is not the
  # installation's reads another installation's documents.
  local dir=$work/regiondisagree
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["region"] = "us-central1"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout" --pool "$pool" --gcp-project "$project" --region us-east5
  expect_status "$status" 2 "a region that is not the installation's"
  expect_contains "$err" "us-central1" "a disagreeing region: the installation's own"
  expect_contains "$err" "us-east5" "a disagreeing region: what was typed"
  assert_no_launch "$dir" "a disagreeing region"
}

test_the_region_is_the_installations_own() {
  # No --region and no --gcp-project: both are the checkout's own, read out of
  # its `fugaro doctor --json` (which answers under `project`), never defaulted.
  local dir=$work/regionown
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["region"] = "europe-west1"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout" --pool "$pool"
  expect_status "$status" 0 "the installation's own region"
  expect_contains "$out" "FACT installation: PASS" "the installation's own region: the row"
  expect_contains "$out" "europe-west1" "the installation's own region: the region it read"
  expect_contains "$out" "$project" "the installation's own region: the project it read"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "run --task-file" "the installation's own region: the run still goes out"
}

test_no_region_anywhere_stops() {
  local dir=$work/noregion
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"].pop("region", None)
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout" --pool "$pool"
  expect_status "$status" 2 "an installation that names no region"
  expect_contains "$err" "named no region" "an installation that names no region: the refusal"
  assert_no_launch "$dir" "an installation that names no region"
}

test_a_checkout_with_no_origin_remote_stops() {
  # Without the origin remote there is no way to name the repository the run is
  # charged to, and the cap row could read another repository's cap.
  local dir=$work/noorigin
  setup_case "$dir"
  git -C "$dir/checkout" remote remove origin
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a checkout with no origin remote"
  expect_contains "$err" "origin" "a checkout with no origin remote: the refusal"
  assert_no_launch "$dir" "a checkout with no origin remote"
}

test_the_cap_read_is_the_checkouts_own() {
  # The listing carries two capped repositories and the other one is first, so a
  # reader that takes the first capped row reports the wrong cap; the check names
  # the repository on the command line and says whose cap it read.
  local dir=$work/capown
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "the checkout's own cap"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "budget show --json --repo acme/chk881" "the checkout's own cap: the repository is named on the call"
  expect_contains "$out" "acme/chk881" "the checkout's own cap: the row names it"
  expect_contains "$out" '$10' "the checkout's own cap: its own figure, not the other repository's"
  # The row's `(from <source>)` is read off the same row as the figure: reading the
  # figure first would leave the source to be taken from the truncated string and
  # print the figure as its own source ("$10 (from 10)").
  expect_contains "$out" '$10 (from default)' "the checkout's own cap: the source the cap came from"
}

test_another_repositorys_cap_is_not_this_ones() {
  # The false PASS the scoping exists to stop: this repository has no cap while
  # another one in the listing does.
  local dir=$work/capother
  setup_case "$dir"
  python3 - "$dir/answers/budget.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
for r in doc["repos"]:
    if r.get("repo") == "acme/chk881":
        r.pop("per_run_cap_usd", None)
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "another repository's cap"
  expect_contains "$out" "FACT budget: FAIL" "another repository's cap: the row"
  expect_contains "$out" "acme/chk881" "another repository's cap: the repository it named"
  assert_no_launch "$dir" "another repository's cap"
}

test_a_checkout_that_is_not_a_work_tree_stops() {
  local dir=$work/notree
  setup_case "$dir"
  rm -rf "$dir/checkout/.git"
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a checkout that is not a work tree"
  expect_contains "$err" "is not a git work tree" "a checkout that is not a work tree: the refusal"
  assert_no_launch "$dir" "a checkout that is not a work tree"
}

test_a_missing_checkout_is_refused() {
  local dir=$work/nocheckout
  setup_case "$dir"
  run_check "$dir" -- --pool "$pool" --gcp-project "$project" --region us-east5
  expect_status "$status" 2 "no --checkout"
  expect_contains "$err" "--checkout" "no --checkout: the fix"
  assert_no_launch "$dir" "no --checkout"
}

test_a_chain_that_does_not_start_with_the_pool_stops() {
  # Nothing to measure: the check is about a fall-through FROM the launcher's
  # pool, and a chain that starts at the key never falls through from it.
  local dir=$work/notfirst
  setup_case "$dir"
  cat >"$dir/answers/table.json" <<JSON
{"entries":[{"match":"${model}","chain":[{"kind":"anthropic","name":"project-key"}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a chain that does not start with the pool"
  expect_contains "$out" "FACT table: FAIL" "a chain that does not start with the pool: the row"
  expect_contains "$out" "starts at anthropic" "a chain that does not start with the pool: what it found"
  assert_no_launch "$dir" "a chain that does not start with the pool"
}

test_a_chain_of_only_the_pool_stops() {
  # D3180: a chain whose only source is the launcher's pool is refused with the
  # fix, so there is no key to fall through to.
  local dir=$work/onlypool
  setup_case "$dir"
  cat >"$dir/answers/table.json" <<JSON
{"entries":[{"match":"${model}","chain":[{"kind":"launcher-pool"}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a chain of only the pool"
  expect_contains "$out" "names no source after" "a chain of only the pool: what it found"
  assert_no_launch "$dir" "a chain of only the pool"
}

test_a_pool_with_a_member_still_good_stops_before_spending() {
  # The run would be served by the pool, so the fall-through rows would measure
  # nothing — and the check says so instead of spending a run on it.
  local dir=$work/usable
  setup_case "$dir"
  cat >"$dir/answers/pools.json" <<JSON
{"pools":[{"name":"${pool}","rule":"failover",
  "members":[{"state":"usable","usage":0.1,"headroom":0.9}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a pool with a member still good"
  expect_contains "$out" "FACT first-source: FAIL" "a pool with a member still good: the row"
  expect_contains "$out" "still has a member that can serve" "a pool with a member still good: what it found"
  assert_no_launch "$dir" "a pool with a member still good"
}

test_a_member_with_no_headroom_figure_can_serve() {
  # PL10: headroom is absent where the provider reported no utilisation figure,
  # and absent is not zero. A member whose headroom nobody reported is one this
  # check may not call unable to serve: reading it as unable passes the gate and
  # spends a run on a pool that could have served it.
  local dir=$work/noheadroom
  setup_case "$dir"
  cat >"$dir/answers/pools.json" <<JSON
{"pools":[{"name":"${pool}","rule":"failover",
  "members":[{"state":"available","usage":0.1}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a member with no headroom figure"
  expect_contains "$out" "FACT first-source: FAIL" "a member with no headroom figure: the row"
  expect_contains "$out" "still has a member that can serve" "a member with no headroom figure: what it found"
  assert_no_launch "$dir" "a member with no headroom figure"
}

test_a_member_whose_window_has_reopened_can_serve() {
  # An exhausted mark whose hold has lapsed serves again (internal/creds
  # Mark.Usable): the state word stays "exhausted", so only the instant says
  # whether the window is closed, and a past exhausted_until is not "unable".
  local dir=$work/reopened
  setup_case "$dir"
  cat >"$dir/answers/pools.json" <<JSON
{"pools":[{"name":"${pool}","rule":"failover",
  "members":[{"state":"exhausted","usage":0.9,"exhausted_until":"2000-01-01T00:00:00Z"}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a member whose window has reopened"
  expect_contains "$out" "FACT first-source: FAIL" "a member whose window has reopened: the row"
  assert_no_launch "$dir" "a member whose window has reopened"
}

test_an_available_members_next_reset_can_serve() {
  # PL10 prints a next reset for every member, not only an exhausted one, and an
  # available member's reset is when its window rolls over: a member that is not
  # exhausted is not waiting for a clock, so its reset is no closed window, and
  # reading it as one would call a pool that can serve unable.
  local dir=$work/nextreset
  setup_case "$dir"
  cat >"$dir/answers/pools.json" <<JSON
{"pools":[{"name":"${pool}","rule":"failover",
  "members":[{"state":"available","usage":0.1,"reset":"2999-01-01T00:00:00Z"}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "an available member's next reset"
  expect_contains "$out" "FACT first-source: FAIL" "an available member's next reset: the row"
  expect_contains "$out" "still has a member that can serve" "an available member's next reset: what it found"
  assert_no_launch "$dir" "an available member's next reset"
}

test_a_member_inside_a_closed_window_cannot_serve() {
  # The state the check needs: the only member's window is still closed, so the
  # pool cannot serve and the run below falls through to the key.
  local dir=$work/closedwindow
  setup_case "$dir"
  cat >"$dir/answers/pools.json" <<JSON
{"pools":[{"name":"${pool}","rule":"failover",
  "members":[{"state":"exhausted","usage":1.0,"exhausted_until":"2999-01-01T00:00:00Z"}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "a member inside a closed window"
  expect_contains "$out" "FACT first-source: PASS" "a member inside a closed window: the row"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "run --task-file" "a member inside a closed window: the run still goes out"
}

test_no_designated_pool_is_the_skip_it_measures() {
  # D3180's other half: no pool designated means the launcher's pool is none, so
  # the chain skips it. The row passes and says which of the two it read.
  local dir=$work/nopool
  setup_case "$dir"
  cat >"$dir/answers/pools.json" <<'JSON'
{"pools":[]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "no designated pool"
  expect_contains "$out" "FACT first-source: PASS" "no designated pool: the row"
  expect_contains "$out" "no pool of that name is designated" "no designated pool: what it read"
}

test_a_project_that_is_not_enforced_stops() {
  local dir=$work/observe
  setup_case "$dir"
  cat >"$dir/answers/budget.json" <<JSON
{"project":"chk881","gcp_project":"${project}","mode":"observe","repos":[],"global":{},"running":[],"warnings":[]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a project that is not enforced"
  expect_contains "$out" "FACT budget: FAIL" "a project that is not enforced: the row"
  expect_contains "$out" "R17" "a project that is not enforced: the rule it rests on"
  assert_no_launch "$dir" "a project that is not enforced"
}

test_a_repository_with_no_per_run_cap_stops() {
  local dir=$work/nocap
  setup_case "$dir"
  python3 - "$dir/answers/budget.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for r in doc["repos"]:
    if r.get("repo") == "acme/chk881":
        r["per_run_cap_usd"] = None
        r["per_run_cap_source"] = "none"
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a repository with no per-run cap"
  expect_contains "$out" "FACT budget: FAIL" "a repository with no per-run cap: the row"
  expect_contains "$out" "acme/chk881" "a repository with no per-run cap: the repository it named"
  assert_no_launch "$dir" "a repository with no per-run cap"
}

test_a_refused_launch_is_not_measured() {
  local dir=$work/refused
  setup_case "$dir"
  run_check "$dir" FAKE881_MODE=launch-refused -- $(default_args "$dir")
  expect_status "$status" 2 "a launch that is refused"
  expect_contains "$out" "FACT launch: FAIL" "a launch that is refused: the row"
  expect_contains "$out" "could not be launched" "a launch that is refused: what it saw"
  expect_contains "$out" "fugaro budget set --repo" "a launch that is refused: the refusal's own fix is quoted"
}

test_a_run_served_by_the_pool_fails_the_fall_through_row() {
  # The finding this check exists for, seen from the other side: the pool served
  # the steps, so the chain did not fall through.
  local dir=$work/servedbypool
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for s in doc["stages"]:
    s["source"] = {"kind": "launcher-pool", "name": "launchers-pool"}
    s["fallback"] = False
doc["cost"]["fallback_usd"] = 0.0
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a run the pool served"
  expect_contains "$out" "FACT served-by-the-paid-source: FAIL" "a run the pool served: the row"
  expect_contains "$out" "launcher-pool" "a run the pool served: the source that served"
  expect_contains "$out" "FACT flagged: FAIL" "a run the pool served: nothing was flagged"
  expect_contains "$out" "FACT counted: FAIL" "a run the pool served: nothing was counted"
}

test_a_step_nobody_flagged_is_a_finding() {
  local dir=$work/noflag
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for s in doc["stages"]:
    s["fallback"] = False
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a step nobody flagged"
  expect_contains "$out" "FACT served-by-the-paid-source: PASS" "a step nobody flagged: the key still served"
  expect_contains "$out" "FACT flagged: FAIL" "a step nobody flagged: the row"
}

test_fallback_dollars_of_zero_are_a_finding() {
  local dir=$work/nomoney
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["cost"]["fallback_usd"] = 0.0
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "fallback dollars of zero"
  expect_contains "$out" "FACT counted: FAIL" "fallback dollars of zero: the row"
  expect_contains "$out" "cost.fallback_usd" "fallback dollars of zero: the member it read"
}

test_a_record_with_no_source_is_a_finding() {
  # The member CS8 adds: a record whose stages carry no source at all is the
  # finding, not a row the check could not measure.
  local dir=$work/nosource
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for s in doc["stages"]:
    s.pop("source", None)
    s.pop("fallback", None)
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a record with no source"
  expect_contains "$out" "FACT served-by-the-paid-source: FAIL" "a record with no source: the row"
  expect_contains "$out" "carries no source" "a record with no source: what it found"
}

test_a_record_that_cannot_be_read_is_not_measured() {
  local dir=$work/norecord
  setup_case "$dir"
  run_check "$dir" FAKE881_MODE=record-absent -- $(default_args "$dir")
  expect_status "$status" 2 "a record that cannot be read"
  expect_contains "$out" "FACT served-by-the-paid-source: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read" "a record that cannot be read: what it saw"
  expect_contains "$err" "could NOT measure" "a record that cannot be read: the exit names the rows"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/neverends
  setup_case "$dir"
  # The shape the real CLI answers for a run that has bootstrapped but not
  # ended: outcome "none", terminal false.
  cat >"$dir/answers/runs.json" <<JSON
[{"run":"${run_ref}","run_id":"${run_id}","outcome":"none","terminal":false,"settled":false}]
JSON
  run_check "$dir" CHECK881_WAIT_SECONDS=1 CHECK881_POLL_SECONDS=1 -- $(default_args "$dir")
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT run: FAIL" "a run that never ends: the row"
  expect_contains "$out" "still not terminal" "a run that never ends: what it saw"
  # The row says what `runs ls` answered with, not whether it answered at all: the
  # owner reading the block needs the row's own terminal and outcome.
  expect_contains "$out" "terminal=false, outcome=none" "a run that never ends: the row it read is printed"
  expect_absent "$out" "was read yes" "a run that never ends: the yes/no it used to print is gone"
}

test_a_run_still_going_is_not_read_as_ended() {
  # A run in flight already answers an outcome — `none`, written at bootstrap —
  # so a check that stops on the first non-empty outcome reads the record while
  # the run is still spending and prints a false FAIL. The ended signal is
  # `terminal`, and the row it prints carries the run's PR outcome.
  local dir=$work/inflight
  setup_case "$dir"
  cat >"$dir/answers/runs.inflight" <<JSON
[{"run":"${run_ref}","run_id":"${run_id}","outcome":"none","terminal":false,"settled":false}]
JSON
  run_check "$dir" CHECK881_POLL_SECONDS=1 -- $(default_args "$dir")
  expect_status "$status" 0 "a run still going at the first poll"
  expect_contains "$out" "FACT run: PASS" "a run still going at the first poll: the row"
  local row
  row=$(printf '%s\n' "$out" | grep -m1 '^FACT run:' || true)
  expect_contains "$row" "ended as 'ready'" "a run still going at the first poll: the outcome it waited for"
  expect_absent "$row" "none" "a run still going at the first poll: the in-flight outcome is not reported as the run's"
  [ "$(grep -c . "$dir/logs/runs.calls")" -ge 2 ] ||
    fail "a run still going at the first poll: the check polled once, so it read the in-flight answer as the end"
}

test_a_model_the_table_does_not_match_stops() {
  local dir=$work/nomatch
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir") --model gemini-nonexistent-9
  expect_status "$status" 2 "a model the table does not match"
  expect_contains "$err" "no entry matches" "a model the table does not match: the refusal"
  assert_no_launch "$dir" "a model the table does not match"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  setup_case "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  setup_case "$dir"
  run_check "$dir" CHECK881_KEEP_SCRATCH=1 -- $(default_args "$dir")
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK_TEST_ONLY=name runs that one alone.
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
t test_the_block_never_carries_a_value
t test_an_agent_session_is_refused
t test_xtrace_refused
t test_belong_is_refused_by_name
t test_a_belong_installation_in_the_checkout_is_refused
t test_a_region_that_disagrees_with_the_installation_stops
t test_the_region_is_the_installations_own
t test_no_region_anywhere_stops
t test_a_checkout_with_no_origin_remote_stops
t test_a_checkout_that_is_not_a_work_tree_stops
t test_a_missing_checkout_is_refused
t test_a_chain_that_does_not_start_with_the_pool_stops
t test_a_chain_of_only_the_pool_stops
t test_a_pool_with_a_member_still_good_stops_before_spending
t test_a_member_with_no_headroom_figure_can_serve
t test_a_member_whose_window_has_reopened_can_serve
t test_an_available_members_next_reset_can_serve
t test_a_member_inside_a_closed_window_cannot_serve
t test_no_designated_pool_is_the_skip_it_measures
t test_a_project_that_is_not_enforced_stops
t test_a_repository_with_no_per_run_cap_stops
t test_the_cap_read_is_the_checkouts_own
t test_another_repositorys_cap_is_not_this_ones
t test_a_refused_launch_is_not_measured
t test_a_run_served_by_the_pool_fails_the_fall_through_row
t test_a_step_nobody_flagged_is_a_finding
t test_fallback_dollars_of_zero_are_a_finding
t test_a_record_with_no_source_is_a_finding
t test_a_record_that_cannot_be_read_is_not_measured
t test_a_run_that_never_ends_is_not_measured
t test_a_run_still_going_is_not_read_as_ended
t test_a_model_the_table_does_not_match_stops
t test_an_unknown_argument_is_refused
t test_kept_scratch_is_reported

if [ "$failures" -ne 0 ]; then
  echo "check-881-chain-falls-through_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-881-chain-falls-through_test.sh: ok"
