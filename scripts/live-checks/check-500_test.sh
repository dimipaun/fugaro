#!/usr/bin/env bash
# Tests for scripts/live-checks/check-500.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a catalog, a build or a run, no
# agent runs a live command, and the fakes record every call they are given — in
# which directory it was made — so a case can assert what the check did and did
# not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the five
# `--json` reads the check makes (doctor, the catalog, the survey, the launch and
# the runs list) from the case's own directory, builds an entry the way the real
# one does, and refuses a build for the checkout it is not run in; the fake
# `gcloud` serves the run record. The cases that matter are the ones where that
# model is bent: a catalog that is empty, five entries, a build that fails, a run
# that never ends, a record that is another run's, an image with no baked
# checkout, and a warning that never printed.
#
# Usage: bash scripts/live-checks/check-500_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-500.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK500_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK500_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk500-test
run_id=run-20261112120000-1

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-500.sh's test only. It
# answers the account read the check makes and serves the run record. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf 'chk500-owner@example.com\n'
  fi
  ;;
storage)
  if [ "${2:-}" = "cat" ]; then
    case "$mode" in
    record-missing)
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
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
# A stand-in for `fugaro`, for scripts/live-checks/check-500.sh's test only. The
# five `--json` reads come from files the case wrote, a build is refused when it
# is not run in the checkout the entry belongs to and otherwise records a
# digest, and every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version) printf '0.24.0\n' ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
image)
  case "${2:-}" in
  ls)
    cat "$FAKE_JSON_DIR/catalog.json"
    ;;
  survey)
    cat "$FAKE_JSON_DIR/survey.json"
    ;;
  build)
    entry=${3:-}
    if [ "$mode" = build-wrong-checkout ] && [ "$entry" = "${FAKE_FOREIGN_ENTRY:-web}" ] && [ "$PWD" != "$FAKE_WRONG_DIR" ]; then
      printf 'fugaro: the entry %s belongs to another repository of the project: run its build from that repository'"'"'s checkout, or the build account is not the one its jobs run as\n' "$entry" >&2
      exit 2
    fi
    if [ "$mode" = build-fails ] && [ "$entry" = "${FAKE_BAD_ENTRY:-web}" ]; then
      printf 'fugaro: the build of %s failed in step 3 of the Dockerfile\n' "$entry" >&2
      exit 1
    fi
    if [ "$mode" = build-slow ]; then
      sleep 30
    fi
    printf 'build %s: 4 steps, digest sha256:%s\n' "$entry" "${entry}digest"
    ;;
  *)
    echo "fugaro: unexpected image subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
run)
  cat "$FAKE_JSON_DIR/launch.json"
  ;;
runs)
  cat "$FAKE_JSON_DIR/rows.json"
  ;;
update)
  case "$mode" in
  no-stale-warning)
    printf 'Plan: 3 to add, 0 to change, 0 to destroy.\n'
    ;;
  stale-warning-without-rule)
    printf 'warning: this workflow'"'"'s `image:` is ignored: the project'"'"'s image catalog gives its job the image of the entry web\n'
    ;;
  update-hangs)
    sleep 30
    ;;
  *)
    printf 'warning: this workflow'"'"'s `image:` is ignored: the project'"'"'s image catalog gives its job the image of the entry web (D1900)\n'
    printf 'Plan: 3 to add, 0 to change, 0 to destroy.\n'
    ;;
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
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk500","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"us-east5-docker.pkg.dev/p/fugaro-base:0.24.0"}}}
JSON
  cat >"$json/catalog.json" <<'JSON'
{"project":"fugaro-chk500-test","entries":[
 {"name":"web","tags":["node"],"default":true,"source":"github.com/acme/sandbox:.fugaro/web.Dockerfile","description":"the Node repositories","digest":"sha256:1111111111"},
 {"name":"data","tags":["postgres"],"default":false,"source":"inline","description":"the data repositories","digest":"sha256:2222222222"}]}
JSON
  cat >"$json/survey.json" <<'JSON'
{"version":1,"project":"fugaro-chk500-test","repos":[
 {"repo":"acme/sandbox","provider":"github","signals":[{"kind":"lockfile","path":"package-lock.json"},{"kind":"version","path":".nvmrc","value":"22"}]},
 {"repo":"acme/data","provider":"github","signals":[{"kind":"dockerfile","path":"Dockerfile"}]}],
 "setup":{"catalog":[{"name":"web"},{"name":"data"}],"image_map":{"acme/sandbox":"web","acme/data":"data"},"bake_repos":true}}
JSON
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"2000","repo":"acme/sandbox","run_id":"$run_id","branch":"fugaro/20261112-120000-abcd","execution":"exec-1","status":"launched"}
JSON
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"2000","repo":"acme/sandbox","run_id":"$run_id","status":"succeeded","stage":"writeback","terminal":true,"settled":true,"branch":"fugaro/20261112-120000-abcd","base_branch":"main"}],"totals":{"runs":1},"warnings":[]}
JSON
  cat >"$json/record.json" <<JSON
{"version":1,"run_id":"$run_id","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"writeback","outcome":"pr","image":{"built_at":"2026-11-12T11:00:00Z","baked_commit":"9f8e7d6"},"pushed_head":"9f8e7d6"}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk500\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
  write_checkout "$1/stale"
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
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_WRONG_DIR="$dir/other" \
    CHECK500_SANDBOX="$dir/sandbox" \
    CHECK500_RUN_TIMEOUT_SECONDS="${CHECK500_RUN_TIMEOUT_SECONDS:-10}" \
    CHECK500_POLL_SECONDS=1 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
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

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check-500.sh --sandbox <a sandbox repository checkout>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "--help: no build is run"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the builds are billable and that the run is theirs.
  expect_contains "$out" "one Cloud Build per catalog entry (billable" "--help: the cost of a build is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK500_DRY_RUN=1 CHECK500_STALE_REPO="$dir/stale"
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 500 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 500 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$out" "FACT catalog: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "one Cloud Build per entry" "dry run: the plan names the builds"
  expect_contains "$err" "the stale image: block" "dry run: the plan names the warning"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "dry run: no build is run"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "run --repo" "dry run: no run is launched"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update --repo" "dry run: no update is run"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage cat" "dry run: the bucket is not read"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir" CHECK500_STALE_REPO="$dir/stale"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT catalog: PASS" "happy path: the catalog"
  expect_contains "$out" "the default is web" "happy path: which entry decides"
  expect_contains "$out" "FACT cap: PASS" "happy path: the cap"
  expect_contains "$out" "2 of the four the skill allows (D1903)" "happy path: the number, and whose rule it is"
  expect_contains "$out" "FACT survey: PASS" "happy path: the survey"
  expect_contains "$out" "FACT builds: PASS" "happy path: the builds"
  expect_contains "$out" "built through Cloud Build" "happy path: what was built, and where"
  expect_contains "$out" "FACT digests: PASS" "happy path: the digests"
  expect_contains "$out" "web=sha256:1111111111" "happy path: the digest read back"
  expect_contains "$out" "FACT run-launched: PASS" "happy path: the launch"
  expect_contains "$out" "launched ${run_id} on acme/sandbox" "happy path: the run it follows"
  expect_contains "$out" "FACT terminal: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT result-json: PASS" "happy path: the record"
  expect_contains "$out" "FACT baked: PASS" "happy path: the baked checkout"
  expect_contains "$out" "baked checkout was at 9f8e7d6" "happy path: what the image carried"
  expect_contains "$out" "FACT stale-warning: PASS" "happy path: D1900's warning"
  expect_contains "$out" "D1900" "happy path: the rule the warning names"
  expect_contains "$out" "CHECK 500 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, and the reads are the ones
  # the check documents.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "image ls --json (in $dir/sandbox)" "happy path: the catalog is read through the checkout"
  expect_contains "$calls" "image survey --json (in $dir/sandbox)" "happy path: the survey is read through the checkout"
  expect_contains "$calls" "image build web (in $dir/sandbox)" "happy path: an entry of the sandbox is built from the sandbox"
  expect_contains "$calls" "image build data (in $dir/sandbox)" "happy path: an inline entry is built from the sandbox too"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the run is launched from the sandbox checkout"
  expect_contains "$calls" "runs ls --repo acme/sandbox --json --limit 1" "happy path: the run is followed by its own slug"
  expect_contains "$calls" "update --repo $dir/stale --plan-only (in $dir/stale)" "happy path: the stale checkout is planned in its own directory"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/acme/sandbox/${run_id}/result.json" "happy path: the record is read at the run's own key"
  expect_absent "$calls" "--yes" "happy path: nothing is confirmed for the owner"
  assert_scratch_removed "$dir" "happy path"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK500_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check-500.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK500_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "a directory that is not a checkout: no build is run"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "bash -x: no build is run"
}

test_a_coding_agents_session_is_refused() {
  # The check drives commands whose writes a coding agent's session is refused:
  # refusing here is what keeps the block from reporting those refusals as
  # findings about the release.
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the refusal"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: whose check it is"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a coding agent's session: nothing is read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "a coding agent's session: the bucket is not read"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  cat >"$dir/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"fugaro-chk500-belong","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"us-east5-docker.pkg.dev/p/fugaro-base:0.24.0"}}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "belong: no build is run"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "belong: the bucket is not read"
}

test_an_empty_catalog_is_a_finding() {
  local dir=$work/nocatalog
  prepare "$dir"
  printf '{"project":"%s","entries":[]}\n' "$project" >"$dir/json/catalog.json"
  run_check "$dir"
  expect_status "$status" 1 "a project with no catalog entry"
  expect_contains "$out" "FACT catalog: FAIL" "a project with no catalog entry: the row"
  expect_contains "$out" "has no catalog entry" "a project with no catalog entry: what it found"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "a project with no catalog entry: no build is run"
}

test_five_entries_break_the_skills_cap() {
  # D1903: nothing in the CLI refuses a fifth entry, so the number is read here
  # or nowhere.
  local dir=$work/cap
  prepare "$dir"
  printf '{"project":"%s","entries":[%s]}\n' "$project" \
    '{"name":"a","tags":[],"default":true,"source":"inline","digest":"sha256:a"},{"name":"b","tags":[],"default":false,"source":"inline","digest":"sha256:b"},{"name":"c","tags":[],"default":false,"source":"inline","digest":"sha256:c"},{"name":"d","tags":[],"default":false,"source":"inline","digest":"sha256:d"},{"name":"e","tags":[],"default":false,"source":"inline","digest":"sha256:e"}' \
    >"$dir/json/catalog.json"
  run_check "$dir"
  expect_status "$status" 1 "five catalog entries"
  expect_contains "$out" "FACT cap: FAIL" "five catalog entries: the row"
  expect_contains "$out" "the cap the fugaro:images skill keeps is four (D1903)" "five catalog entries: whose rule it is"
}

test_a_catalog_with_no_default_is_a_finding() {
  # The default is what makes a repository's own `image:` ignored at all, so a
  # catalog without one cannot measure D1900's warning either.
  local dir=$work/nodefault
  prepare "$dir"
  printf '{"project":"%s","entries":[{"name":"web","tags":["node"],"default":false,"source":"inline","digest":"sha256:1"}]}\n' "$project" >"$dir/json/catalog.json"
  run_check "$dir"
  expect_status "$status" 1 "a catalog with no default"
  expect_contains "$out" "FACT catalog: FAIL" "a catalog with no default: the row"
  expect_contains "$out" "none marked default" "a catalog with no default: what it found"
  expect_contains "$out" "FACT stale-warning: INFO" "a catalog with no default: the warning row is not asked for"
}

test_a_build_that_fails_is_a_finding() {
  local dir=$work/builfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=build-fails FAKE_BAD_ENTRY=web
  expect_status "$status" 1 "a build that failed"
  expect_contains "$out" "FACT builds: FAIL" "a build that failed: the row"
  expect_contains "$out" "the build(s) of web(exit 1) did not succeed" "a build that failed: which entry"
  expect_contains "$out" "CHECK500_CHECKOUT_<entry>" "a build that failed: the knob a refused checkout needs"
}

test_a_build_that_is_refused_for_the_checkout_says_so() {
  # The CLI refuses an entry's build from another repository by name — the fake
  # here refuses web's exactly as the real one would — and the row has to name
  # the knob that gives that entry its own checkout, because the failure alone
  # says nothing about what to do next.
  local dir=$work/wrongco
  prepare "$dir"
  run_check "$dir" FAKE_MODE=build-wrong-checkout
  expect_status "$status" 1 "a build refused for the checkout"
  expect_contains "$out" "FACT builds: FAIL" "a build refused for the checkout: the row"
  expect_contains "$out" "CHECK500_CHECKOUT_<entry>" "a build refused for the checkout: the knob that fixes it"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image build web (in $dir/sandbox)" "a build refused for the checkout: web was built from the sandbox checkout it does not belong to"
}

test_an_entry_can_be_given_its_own_checkout() {
  # The knob the row above names, spent: web is built from the checkout it
  # belongs to and every other entry keeps the sandbox's.
  local dir=$work/ownco
  prepare "$dir"
  run_check "$dir" FAKE_MODE=build-wrong-checkout CHECK500_CHECKOUT_web="$dir/other"
  expect_status "$status" 0 "an entry with its own checkout"
  expect_contains "$out" "FACT builds: PASS" "an entry with its own checkout: the row"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image build web (in $dir/other)" "an entry with its own checkout: the entry was built from the checkout it was given"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image build data (in $dir/sandbox)" "an entry with its own checkout: every other entry keeps the sandbox checkout"
}

test_a_build_can_be_left_out() {
  local dir=$work/skipbuilds
  prepare "$dir"
  run_check "$dir" CHECK500_SKIP_BUILDS=1
  expect_status "$status" 0 "the builds left out"
  expect_contains "$out" "FACT builds: INFO" "the builds left out: the row"
  expect_contains "$out" "not asked for (CHECK500_SKIP_BUILDS)" "the builds left out: the reason"
  expect_contains "$out" "FACT digests: PASS" "the builds left out: the digests are still read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "the builds left out: no build is run"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/notterminal
  prepare "$dir"
  printf '{"project":"%s","runs":[{"run":"2000","repo":"acme/sandbox","run_id":"%s","status":"running","stage":"implement","terminal":false}],"warnings":[]}\n' "$project" "$run_id" >"$dir/json/rows.json"
  run_check "$dir" CHECK500_RUN_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a run that never ended"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ended: the row"
  expect_contains "$out" "had not ended after" "a run that never ended: what it saw"
  expect_contains "$err" "could NOT measure terminal" "a run that never ended: the exit names the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage cat" "a run that never ended: no record is read"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT result-json: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read (exit 1)" "a record that cannot be read: what it saw"
}

test_a_record_of_another_run_is_a_finding() {
  # A record read by listing alone could be another run's: the record's own
  # run_id is what ties it to the run this check launched.
  local dir=$work/otherrecord
  prepare "$dir"
  printf '{"version":1,"run_id":"run-20261112000000-9","repo":"acme/sandbox","status":"succeeded","stage":"writeback","image":{"built_at":"2026-11-12T11:00:00Z","baked_commit":"9f8e7d6"}}\n' >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 1 "a record of another run"
  expect_contains "$out" "FACT result-json: FAIL" "a record of another run: the row"
  expect_contains "$out" "records run_id=run-20261112000000-9 and this check launched ${run_id}" "a record of another run: the two IDs"
}

test_an_image_with_no_baked_checkout_is_a_finding() {
  local dir=$work/notbaked
  prepare "$dir"
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stage":"writeback","image":{"built_at":"2026-11-12T11:00:00Z","baked_commit":""}}\n' "$run_id" >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 1 "a run whose image carried no checkout"
  expect_contains "$out" "FACT baked: FAIL" "a run whose image carried no checkout: the row"
  expect_contains "$out" "while this project bakes" "a run whose image carried no checkout: why it was expected"
}

test_a_project_that_bakes_nothing_says_so() {
  local dir=$work/nobake
  prepare "$dir"
  python3 - "$dir/json/survey.json" <<'PY'
import json, sys
path = sys.argv[1]
doc = json.load(open(path))
doc["setup"]["bake_repos"] = False
json.dump(doc, open(path, "w"))
PY
  printf '{"version":1,"run_id":"%s","repo":"acme/sandbox","status":"succeeded","stage":"writeback","image":{"built_at":"2026-11-12T11:00:00Z","baked_commit":""}}\n' "$run_id" >"$dir/json/record.json"
  run_check "$dir"
  expect_status "$status" 0 "a project that bakes nothing"
  expect_contains "$out" "FACT baked: INFO" "a project that bakes nothing: the row"
  expect_contains "$out" "does not bake (setup.bake_repos is false)" "a project that bakes nothing: the reason"
}

test_a_missing_warning_is_a_finding() {
  local dir=$work/nowarning
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-stale-warning
  expect_status "$status" 1 "a warning that never printed"
  expect_contains "$out" "FACT stale-warning: FAIL" "a warning that never printed: the row"
  expect_contains "$out" "printed no 'is ignored' warning" "a warning that never printed: what it looked for"
  expect_contains "$out" "D1900" "a warning that never printed: the rule"
}

test_a_warning_without_the_rule_is_a_finding() {
  local dir=$work/norule
  prepare "$dir"
  run_check "$dir" FAKE_MODE=stale-warning-without-rule
  expect_status "$status" 1 "a warning that names no rule"
  expect_contains "$out" "does not name D1900" "a warning that names no rule: the row"
}

test_a_stale_repo_that_is_not_a_checkout_is_not_measured() {
  local dir=$work/nostale
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK500_STALE_REPO="$dir/empty"
  expect_status "$status" 2 "a stale checkout that is not one"
  expect_contains "$out" "FACT stale-warning: FAIL" "a stale checkout that is not one: the row"
  expect_contains "$out" "carries no fugaro.yaml" "a stale checkout that is not one: what it found"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "update --repo" "a stale checkout that is not one: no update is run"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK500_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK500_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
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
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_an_empty_catalog_is_a_finding
t test_five_entries_break_the_skills_cap
t test_a_catalog_with_no_default_is_a_finding
t test_a_build_that_fails_is_a_finding
t test_a_build_that_is_refused_for_the_checkout_says_so
t test_an_entry_can_be_given_its_own_checkout
t test_a_build_can_be_left_out
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_an_image_with_no_baked_checkout_is_a_finding
t test_a_project_that_bakes_nothing_says_so
t test_a_missing_warning_is_a_finding
t test_a_warning_without_the_rule_is_a_finding
t test_a_stale_repo_that_is_not_a_checkout_is_not_measured
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-500_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-500_test.sh: ok"
