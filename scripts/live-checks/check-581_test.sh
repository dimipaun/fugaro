#!/usr/bin/env bash
# Tests for scripts/live-checks/check-581.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a catalog or a build, no agent
# runs a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the reads
# the check makes (version, doctor, the catalog, the survey), builds an entry the
# way the real one does — the submitted line on stdout and the bake note on
# stderr — and refuses a build for the checkout it is not run in; the fake
# `gcloud` serves the build's own steps. The cases that matter are the ones where
# that model is bent: a catalog that is empty, a map that puts no entry on two
# repositories, a project that bakes nothing, a build that fails or is refused, a
# note that names one repository or leaves one out, a build whose steps have no
# scan or put it after the promote, a build that cannot be described, a build
# that reports another digest than the entry records, a repeated build that
# records nothing, an entry that records no digest, and a checkout the billable
# build would be submitted from that belongs to another project — or to one
# naming belong — while the sandbox checkout belongs to the project this check
# is about.
#
# Usage: bash scripts/live-checks/check-581_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-581.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK581_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK581_TEST_HEAD:-900}")" ;;
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

project=fugaro-chk581-test

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-581.sh's test only. It
# answers the account read the check makes and serves the build's own steps.
# Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf '%s\n' "${FAKE_ACCOUNT:-chk581-owner@example.com}"
  fi
  ;;
builds)
  if [ "${2:-}" != "describe" ]; then
    echo "gcloud: unexpected builds subcommand ${2:-}" >&2
    exit 1
  fi
  # A build is submitted to projects/<project>/locations/<region>/builds, so a
  # describe that names no region reads the global location, and one naming
  # another region reads a location the build is not in: either way the real
  # gcloud answers NOT_FOUND. The fake serves the build only in the region it was
  # submitted to, which is what pins the check's --region.
  region=""
  for arg in "$@"; do
    case "$arg" in --region=*) region=${arg#--region=} ;; esac
  done
  if [ -z "$region" ] || [ "$region" != "${FAKE_BUILD_REGION:-us-east5}" ]; then
    echo "ERROR: (gcloud.builds.describe) NOT_FOUND: Requested entity was not found." >&2
    exit 1
  fi
  case "$mode" in
  describe-fails)
    echo "ERROR: (gcloud.builds.describe) NOT_FOUND: Requested entity was not found." >&2
    exit 1
    ;;
  describe-garbage) printf 'not a document\n' ;;
  *) cat "$FAKE_JSON_DIR/${FAKE_DESCRIBE:-build.json}" ;;
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
# A stand-in for `fugaro`, for scripts/live-checks/check-581.sh's test only. The
# reads come from files the case wrote, a build prints the submitted line and the
# bake note the real one prints and marks the entry as built, and every call is
# logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

note_baked() { # note_baked ENTRY REPO...
  local entry=$1
  shift
  local list="" repo dir
  for repo in "$@"; do
    dir="/work/baked/$(printf '%s' "$repo" | tr '/' '-')-1a2b3c4d"
    list="${list:+$list, }${repo} at ${dir}"
  done
  printf 'note: the catalog entry %s bakes the checkouts of %s, so the image carries the source of every repository the project'"'"'s map puts on it\n' "$entry" "$list" >&2
}

case "${1:-}" in
version) printf '0.24.0\n' ;;
doctor)
  # The checkout the billable build is submitted from can belong to another
  # project: when the case names a directory and a project, the fake answers that
  # project in that directory and the sandbox checkout's own document anywhere
  # else, which is what a --checkout pointing at another project's repository
  # looks like on a real machine.
  if [ -n "${FAKE_CHECKOUT_PROJECT:-}" ] && [ "$PWD" = "${FAKE_CHECKOUT_DIR:-}" ]; then
    printf '{"version":1,"ok":true,"checks":[],"project":{"name":"other","gcp_project":"%s","region":"us-east5"}}\n' "$FAKE_CHECKOUT_PROJECT"
    # ... and it can fail to answer there at all, which is no project to name.
    [ -z "${FAKE_CHECKOUT_DOCTOR_STATUS:-}" ] || exit "$FAKE_CHECKOUT_DOCTOR_STATUS"
  else
    cat "$FAKE_JSON_DIR/doctor.json"
  fi
  # exits 1 with its document, as the real doctor does when a check is a warning
  [ ! -f "$FAKE_JSON_DIR/doctor.exit" ] || exit "$(cat "$FAKE_JSON_DIR/doctor.exit")"
  ;;
image)
  case "${2:-}" in
  ls)
    if [ "$mode" = catalog-fails ]; then
      printf 'fugaro: could not read the image catalog: the catalog object is not there\n' >&2
      exit 1
    fi
    # The second read is the one after the build: a case that wrote no
    # catalog-after.json gets the catalog it had, which is a build that recorded
    # no digest.
    if [ -f "$FAKE_STATE_DIR/built" ] && [ -f "$FAKE_JSON_DIR/catalog-after.json" ]; then
      cat "$FAKE_JSON_DIR/catalog-after.json"
    else
      cat "$FAKE_JSON_DIR/catalog.json"
    fi
    ;;
  survey)
    case "$mode" in
    survey-garbage) printf 'not a document\n' ;;
    survey-fails)
      printf 'fugaro: could not read the project%s published policy\n' "'" >&2
      exit 1
      ;;
    *) cat "$FAKE_JSON_DIR/survey.json" ;;
    esac
    ;;
  build)
    entry=${3:-}
    case "$mode" in
    build-refused)
      printf 'fugaro: the catalog entry %s is built from acme/sandbox'"'"'s Dockerfile, and a build runs as the build account of the repository it is submitted from, with that repository'"'"'s credential: run this from acme/sandbox'"'"'s checkout (or from acme/data, which is already there)\n' "$entry" >&2
      exit 1
      ;;
    build-fails)
      note_baked "$entry" acme/sandbox acme/data
      printf 'fugaro: the build of %s failed in step 5 (build)\n' "$entry" >&2
      exit 1
      ;;
    build-slow) sleep 30 ;;
    esac
    if [ "$mode" != no-id ]; then
      printf 'fugaro: Cloud Build build fake-build-1 of us-east5-docker.pkg.dev/fugaro-chk581-test/fugaro/%s:20261117 submitted; log: https://console.cloud.google.com/cloud-build/builds/fake-build-1\n' "$entry"
      # What the build says it made: the digest the entry's record step writes,
      # unless the case bends it — a build that reports no digest of its own, or
      # one that reports a digest the catalog does not carry.
      case "$mode" in
      no-digest-report)
        printf 'built us-east5-docker.pkg.dev/fugaro-chk581-test/fugaro/%s, digest unknown: Cloud Build reported no pushed image (Cloud Build build fake-build-1)\n' "$entry"
        ;;
      digest-other)
        printf 'built us-east5-docker.pkg.dev/fugaro-chk581-test/fugaro/%s@sha256:9999999999 (Cloud Build build fake-build-1)\n' "$entry"
        ;;
      *) printf 'built us-east5-docker.pkg.dev/fugaro-chk581-test/fugaro/%s@sha256:3333333333 (Cloud Build build fake-build-1)\n' "$entry" ;;
      esac
    fi
    case "$mode" in
    one-repo)
      printf 'note: the catalog entry %s bakes the checkout of acme/sandbox at /work/baked/acme-sandbox-1a2b3c4d, so the image carries that repository'"'"'s source\n' "$entry" >&2
      ;;
    partial-bake) note_baked "$entry" acme/sandbox acme/other ;;
    no-bake)
      printf 'note: %s: no checkout baked (the repository this build runs for is not one the entry'"'"'s workflow runs). To bake: fugaro image map set acme/sandbox %s\n' "$entry" "$entry" >&2
      ;;
    *) note_baked "$entry" acme/sandbox acme/data ;;
    esac
    touch "$FAKE_STATE_DIR/built"
    ;;
  *)
    echo "fugaro: unexpected image subcommand ${2:-}" >&2
    exit 1
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
{"version":1,"ok":true,"checks":[],"project":{"name":"chk581","gcp_project":"$project","region":"us-east5","registry_host":"us-east5-docker.pkg.dev","base_images":{"base":"us-east5-docker.pkg.dev/p/fugaro-base:0.24.0"}}}
JSON
  cat >"$json/catalog.json" <<'JSON'
{"project":"fugaro-chk581-test","entries":[
 {"name":"web","tags":["node"],"default":true,"source":"dockerfile acme/sandbox:.fugaro/web.Dockerfile","description":"the Node repositories","digest":"sha256:1111111111"},
 {"name":"data","tags":["postgres"],"default":false,"source":"inline","description":"the data repositories","digest":"sha256:2222222222"}]}
JSON
  cat >"$json/catalog-after.json" <<'JSON'
{"project":"fugaro-chk581-test","entries":[
 {"name":"web","tags":["node"],"default":true,"source":"dockerfile acme/sandbox:.fugaro/web.Dockerfile","description":"the Node repositories","digest":"sha256:3333333333"},
 {"name":"data","tags":["postgres"],"default":false,"source":"inline","description":"the data repositories","digest":"sha256:2222222222"}]}
JSON
  # The map the check picks the entry from: web serves two repositories, data one.
  cat >"$json/survey.json" <<'JSON'
{"version":2,"project":"fugaro-chk581-test","repos":[
 {"repo":"acme/sandbox","provider":"github","signals":[{"kind":"lockfile","path":"package-lock.json"}]},
 {"repo":"acme/data","provider":"github","signals":[{"kind":"dockerfile","path":"Dockerfile"}]}],
 "setup":{"catalog":[{"name":"web"},{"name":"data"}],"repo_images":{"github/acme/sandbox":"web","github/acme/data":"web","github/acme/solo":"data"},"bake_repos":true}}
JSON
  # The build's own record of itself: the steps a catalog build runs, with B3's
  # scan between the smoke and the promote.
  cat >"$json/build.json" <<'JSON'
{"id":"fake-build-1","status":"SUCCESS","steps":[
 {"id":"prep","status":"SUCCESS"},
 {"id":"credential","status":"SUCCESS"},
 {"id":"dockerfile","status":"SUCCESS"},
 {"id":"catalog-clone","status":"SUCCESS"},
 {"id":"catalog-pin","status":"SUCCESS"},
 {"id":"build","status":"SUCCESS"},
 {"id":"smoke","status":"SUCCESS"},
 {"id":"catalog-scan","status":"SUCCESS"},
 {"id":"promote","status":"SUCCESS"},
 {"id":"catalog-record","status":"SUCCESS"},
 {"id":"catalog-tag","status":"SUCCESS"},
 {"id":"untag","status":"SUCCESS"}]}
JSON
  cat >"$json/build-noscan.json" <<'JSON'
{"id":"fake-build-1","status":"SUCCESS","steps":[
 {"id":"prep","status":"SUCCESS"},
 {"id":"build","status":"SUCCESS"},
 {"id":"smoke","status":"SUCCESS"},
 {"id":"promote","status":"SUCCESS"},
 {"id":"catalog-record","status":"SUCCESS"}]}
JSON
  cat >"$json/build-late-scan.json" <<'JSON'
{"id":"fake-build-1","status":"SUCCESS","steps":[
 {"id":"prep","status":"SUCCESS"},
 {"id":"build","status":"SUCCESS"},
 {"id":"smoke","status":"SUCCESS"},
 {"id":"promote","status":"SUCCESS"},
 {"id":"catalog-scan","status":"SUCCESS"},
 {"id":"catalog-record","status":"SUCCESS"}]}
JSON
  cat >"$json/build-scan-failed.json" <<'JSON'
{"id":"fake-build-1","status":"FAILURE","failureInfo":{"detail":"the scan found a credential in the image"},"steps":[
 {"id":"prep","status":"SUCCESS"},
 {"id":"build","status":"SUCCESS"},
 {"id":"smoke","status":"SUCCESS"},
 {"id":"catalog-scan","status":"FAILURE"},
 {"id":"promote","status":""}]}
JSON
  cat >"$json/build-failed.json" <<'JSON'
{"id":"fake-build-1","status":"FAILURE","failureInfo":{"detail":"Step #5 build: the Dockerfile failed"},"steps":[
 {"id":"prep","status":"SUCCESS"},
 {"id":"build","status":"FAILURE"}]}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk581\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
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
    FAKE_JSON_DIR="$dir/json" FAKE_STATE_DIR="$dir/state" \
    FAKE_DESCRIBE="${FAKE_DESCRIBE:-build.json}" FAKE_ACCOUNT="${FAKE_ACCOUNT:-}" \
    CHECK581_SANDBOX="$dir/sandbox" \
    CHECK581_BUILD_TIMEOUT_SECONDS="${CHECK581_BUILD_TIMEOUT_SECONDS:-20}" \
    CHECK581_CLI_TIMEOUT_SECONDS="${CHECK581_CLI_TIMEOUT_SECONDS:-20}" \
    CHECK581_DOCTOR_TIMEOUT_SECONDS="${CHECK581_DOCTOR_TIMEOUT_SECONDS:-20}" \
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

assert_no_build() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "image build" "$2: no build is run"
  expect_absent "$(cat "$1/logs/gcloud.calls")" "builds describe" "$2: no build is described"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check-581.sh --sandbox <a sandbox repository checkout>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "--help: no build is run"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the build is billable and that the run is theirs.
  expect_contains "$out" "billable" "--help: the cost of a build is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "gcloud builds describe" "--help: how the scan is read back"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK581_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 581 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 581 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$out" "FACT map: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "BILLABLE" "dry run: the plan names the cost"
  expect_contains "$err" "catalog-scan, before promote" "dry run: the plan names what the scan row reads"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image build" "dry run: no build is run"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "image survey" "dry run: the map is not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds describe" "dry run: no build is described"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT catalog: PASS" "happy path: the catalog"
  expect_contains "$out" "web(tags=node,digest=sha256:1111111111)" "happy path: the entries it read"
  expect_contains "$out" "FACT map: PASS" "happy path: the map"
  expect_contains "$out" "the entry web is over 2 repositories — acme/data,acme/sandbox" "happy path: the entry it picked, and why"
  expect_contains "$out" "read from the project's own map (setup.repo_images)" "happy path: the map is read from the version-2 member"
  expect_contains "$out" "FACT build: PASS" "happy path: the build"
  expect_contains "$out" "built through Cloud Build as build fake-build-1" "happy path: the build it read back"
  expect_contains "$out" "FACT baked: PASS" "happy path: the bake note"
  expect_contains "$out" "bakes the checkouts of acme/sandbox,acme/data" "happy path: the repositories the build says it baked"
  expect_contains "$out" "FACT scan: PASS" "happy path: the scan"
  expect_contains "$out" "'catalog-scan' at step 7 with status SUCCESS, before 'promote' at step 8" "happy path: where the scan stands"
  expect_contains "$out" "FACT digest: PASS" "happy path: the digest"
  expect_contains "$out" "records the digest sha256:3333333333 the build fake-build-1 reported it made" "happy path: the digest the build made, read back from the entry"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore row"
  expect_contains "$out" "CHECK 581 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, and the reads are the ones
  # the check documents.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "image ls --json (in $dir/sandbox)" "happy path: the catalog is read through the checkout"
  expect_contains "$calls" "image survey --json (in $dir/sandbox)" "happy path: the map is read through the checkout"
  expect_contains "$calls" "image build web (in $dir/sandbox)" "happy path: the entry is built from the checkout the map names"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "builds describe fake-build-1 --region=us-east5 --project=${project} --format=json (in $dir/sandbox)" "happy path: the build is described in the project and region the checkout belongs to"
  expect_absent "$calls" "--yes" "happy path: nothing is confirmed for the owner"
  expect_absent "$calls" "image map set" "happy path: the check writes no mapping"
  expect_absent "$calls" "fugaro run" "happy path: no run is launched"
  assert_scratch_removed "$dir" "happy path"
}

test_a_legacy_map_is_read_from_its_own_member() {
  local dir=$work/legacymap
  prepare "$dir"
  # A project whose map an update has not migrated yet: `image survey` prints
  # the bare-name legacy_image_map and no repo_images entry (R4), and the entry
  # this check builds is picked from the map the document holds.
  cat >"$dir/json/survey.json" <<'JSON'
{"version":2,"project":"fugaro-chk581-test","repos":[
 {"repo":"acme/sandbox","provider":"github","signals":[{"kind":"lockfile","path":"package-lock.json"}]}],
 "setup":{"catalog":[{"name":"web"},{"name":"data"}],"repo_images":{},"legacy_image_map":{"acme/sandbox":"web","acme/data":"web"},"bake_repos":true}}
JSON
  run_check "$dir"
  expect_status "$status" 0 "a legacy map"
  expect_contains "$out" "FACT map: PASS" "a legacy map: the map row"
  expect_contains "$out" "read from the project's own map (setup.legacy_image_map)" "a legacy map: the member read"
}

test_one_repository_at_two_providers_is_one_repository() {
  local dir=$work/twoproviders
  prepare "$dir"
  # D3373 maps one owner/name at two providers with two keys. That is one
  # repository, and the build's own note names it once, so an entry serving it
  # twice is not the entry over two repositories this check is about.
  cat >"$dir/json/survey.json" <<'JSON'
{"version":2,"project":"fugaro-chk581-test","repos":[
 {"repo":"acme/sandbox","provider":"github","signals":[{"kind":"lockfile","path":"package-lock.json"}]}],
 "setup":{"catalog":[{"name":"web"},{"name":"data"}],"repo_images":{"github/acme/sandbox":"web","bitbucket/acme/sandbox":"web","github/acme/data":"web"},"bake_repos":true}}
JSON
  run_check "$dir"
  expect_status "$status" 0 "one repository at two providers"
  expect_contains "$out" "the entry web is over 2 repositories — acme/data,acme/sandbox" "one repository at two providers: the keys of one name are one repository"
}

test_doctor_exit_1_is_an_answer() {
  local dir=$work/doctor-exit-1
  prepare "$dir"
  printf '1\n' >"$dir/json/doctor.exit"
  run_check "$dir"
  expect_status "$status" 0 "doctor exits 1: the document it printed is the answer"
  expect_contains "$out" "FACT sandbox: PASS" "doctor exits 1: the first row is measured"
  expect_absent "$out" "did not answer" "doctor exits 1: it did answer"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK581_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check-581.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK581_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  assert_no_build "$dir" "a directory that is not a checkout"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_contains "$out" "No image was promoted by this run" "bash -x: the restore row does not claim a build that never ran"
  assert_no_build "$dir" "bash -x"
}

test_a_large_catalog_is_summarised() {
  local dir=$work/big-catalog
  prepare "$dir"
  python3 - "$dir/json/catalog.json" <<'PY'
import json
import sys

entries = [{"name": "web", "tags": ["node"], "default": True,
            "source": "dockerfile acme/sandbox:.fugaro/web.Dockerfile",
            "digest": "sha256:1111111111"}]
for i in range(9):
    entries.append({"name": "extra%d" % i, "tags": ["tag%d" % i], "default": False,
                    "source": "inline", "digest": "sha256:%064d" % i})
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump({"project": "fugaro-chk581-test", "entries": entries}, fh)
PY
  run_check "$dir"
  expect_status "$status" 0 "a large catalog"
  expect_contains "$out" "has 10 catalog entries" "a large catalog: the count is the whole catalog"
  expect_contains "$out" "and 2 more" "a large catalog: the list of names is capped"
  expect_absent "$out" "extra8(" "a large catalog: entries past the cap are not printed"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "CLAUDECODE"
  expect_contains "$err" "CLAUDECODE is set" "CLAUDECODE: the guard names the marker"
  expect_contains "$out" "FACT agent-session: FAIL" "CLAUDECODE: the row is a failure"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "CLAUDECODE: nothing is read"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong-account
  prepare "$dir"
  run_check "$dir" FAKE_ACCOUNT=owner@belong-corp.example
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$err" "names belong" "an account naming belong: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "an account naming belong: nothing is read"
}

test_a_project_naming_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir"
  cat >"$dir/json/doctor.json" <<'JSON'
{"version":1,"ok":true,"checks":[],"project":{"name":"prod","gcp_project":"belong-prod","region":"europe-west1"}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "a checkout of a belong project"
  expect_contains "$err" "belongs to belong-prod" "a checkout of a belong project: the refusal names it"
  assert_no_build "$dir" "a checkout of a belong project"
}

test_an_empty_catalog_is_a_finding() {
  local dir=$work/nocatalog
  prepare "$dir"
  cat >"$dir/json/catalog.json" <<'JSON'
{"project":"fugaro-chk581-test","entries":[]}
JSON
  run_check "$dir"
  expect_status "$status" 1 "an empty catalog"
  expect_contains "$out" "FACT catalog: FAIL" "an empty catalog: the row is a failure"
  expect_contains "$out" "has no catalog entry" "an empty catalog: what it says"
  expect_contains "$out" "FACT map: FAIL" "an empty catalog: the map row is a failure too"
  expect_contains "$out" "the catalog does not list the entry web" "an empty catalog: the entry the map names has nowhere to be built from"
  expect_contains "$out" "CHECK 581 RESULT END (exit 1)" "an empty catalog: the exit"
  assert_no_build "$dir" "an empty catalog"
}

test_a_catalog_that_cannot_be_read_is_not_measured() {
  local dir=$work/catalog-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=catalog-fails
  expect_status "$status" 2 "a catalog that cannot be read"
  expect_contains "$out" "FACT catalog: FAIL — fugaro image ls --json did not answer" "a catalog that cannot be read: the row says why"
  assert_no_build "$dir" "a catalog that cannot be read"
}

test_a_map_with_no_entry_over_two_repositories_is_a_finding() {
  local dir=$work/map-one-each
  prepare "$dir"
  cat >"$dir/json/survey.json" <<'JSON'
{"version":2,"project":"fugaro-chk581-test","repos":[],
 "setup":{"catalog":[{"name":"web"},{"name":"data"}],"repo_images":{"github/acme/sandbox":"web","github/acme/data":"data"},"bake_repos":true}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "a map that puts one repository on every entry"
  expect_contains "$out" "FACT map: FAIL" "a map that puts one repository on every entry: the row is a failure"
  expect_contains "$out" "no catalog entry of ${project} is over two repositories" "a map that puts one repository on every entry: what it says"
  expect_contains "$out" "fugaro image map" "a map that puts one repository on every entry: the fix"
  assert_no_build "$dir" "a map that puts one repository on every entry"
}

test_a_map_that_cannot_be_read_is_not_measured() {
  local dir=$work/survey-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=survey-fails
  expect_status "$status" 2 "a map that cannot be read"
  expect_contains "$out" "FACT map: FAIL — fugaro image survey --json did not answer" "a map that cannot be read: the row says why"
  expect_contains "$out" "FACT build: FAIL — there is no entry to build" "a map that cannot be read: no entry was picked"
  assert_no_build "$dir" "a map that cannot be read"
}

test_a_survey_that_is_not_a_document_is_not_measured() {
  local dir=$work/survey-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=survey-garbage
  expect_status "$status" 2 "a survey that is not a document"
  expect_contains "$out" "FACT map: FAIL — fugaro image survey --json printed a document this check could not read" "a survey that is not a document: the row says why"
}

test_a_project_that_bakes_nothing_says_so() {
  local dir=$work/nobake
  prepare "$dir"
  cat >"$dir/json/survey.json" <<'JSON'
{"version":2,"project":"fugaro-chk581-test","repos":[],
 "setup":{"catalog":[{"name":"web"},{"name":"data"}],"repo_images":{"github/acme/sandbox":"web","github/acme/data":"web"},"bake_repos":false}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "bake_repos=false"
  expect_contains "$out" "FACT map: FAIL" "bake_repos=false: the row is a failure"
  expect_contains "$out" "has bake_repos=false" "bake_repos=false: what it says"
  expect_contains "$out" "fugaro settings set bake_repos true" "bake_repos=false: the fix"
  assert_no_build "$dir" "bake_repos=false"
}

test_an_entry_can_be_named() {
  local dir=$work/named
  prepare "$dir"
  run_check "$dir" CHECK581_ENTRY=web
  expect_status "$status" 0 "an entry named with CHECK581_ENTRY"
  expect_contains "$out" "FACT map: PASS" "an entry named: the row"
  expect_contains "$out" "CHECK581_ENTRY named this entry" "an entry named: the row says it was named"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image build web" "an entry named: that entry is built"
}

test_an_entry_over_one_repository_is_a_finding() {
  local dir=$work/named-one
  prepare "$dir"
  run_check "$dir" CHECK581_ENTRY=data
  expect_status "$status" 1 "an entry the map puts one repository on"
  expect_contains "$out" "FACT map: FAIL" "an entry over one repository: the row is a failure"
  expect_contains "$out" "puts 1 repository on the entry data" "an entry over one repository: the count"
  expect_contains "$out" "The entry the project's map puts the most repositories on is web" "an entry over one repository: the entry that is over two"
  assert_no_build "$dir" "an entry over one repository"
}

test_a_named_entry_the_catalog_lacks_is_a_finding() {
  local dir=$work/named-missing
  prepare "$dir"
  run_check "$dir" CHECK581_ENTRY=nosuch
  expect_status "$status" 1 "an entry the catalog has not got"
  expect_contains "$out" "FACT map: FAIL" "an entry the catalog has not got: the row is a failure"
  expect_contains "$out" "catalog has no entry nosuch" "an entry the catalog has not got: what it says"
  assert_no_build "$dir" "an entry the catalog has not got"
}

test_a_build_that_fails_is_a_finding() {
  local dir=$work/build-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=build-fails
  expect_status "$status" 1 "a build that fails"
  expect_contains "$out" "FACT build: FAIL" "a build that fails: the row is a failure"
  expect_contains "$out" "exited 1 in $dir/sandbox" "a build that fails: the status and the checkout"
  expect_contains "$out" "FACT baked: FAIL — no build ran" "a build that fails: nothing below is measured"
  expect_contains "$out" "FACT scan: FAIL — no build ran" "a build that fails: the scan is not measured"
  expect_contains "$out" "FACT digest: FAIL — no build ran" "a build that fails: the digest is not measured"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds describe" "a build that fails: no build is described"
}

test_a_build_refused_for_the_checkout_says_so() {
  local dir=$work/build-refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=build-refused
  expect_status "$status" 1 "a build refused for the checkout"
  expect_contains "$out" "FACT build: FAIL" "a build refused for the checkout: the row is a failure"
  expect_contains "$out" "CHECK581_CHECKOUT set to that repository's checkout" "a build refused for the checkout: the fix it names"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds describe" "a build refused for the checkout: no build is described"
}

test_an_entry_can_be_built_from_its_own_checkout() {
  local dir=$work/checkout
  prepare "$dir"
  run_check "$dir" CHECK581_CHECKOUT="$dir/other"
  expect_status "$status" 0 "a checkout named with CHECK581_CHECKOUT"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json (in $dir/other)" "a checkout named: that checkout's own project is read before the build"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image build web (in $dir/other)" "a checkout named: the build runs in it"
  expect_contains "$out" "run from $dir/other" "a checkout named: the row says where from"
}

test_a_checkout_of_a_belong_project_is_refused() {
  local dir=$work/checkout-belong
  prepare "$dir"
  # The sandbox checkout belongs to the project this check is about; the checkout
  # the billable build would be submitted from answers belong's. Every guard
  # above passes — nothing is refused by name until this checkout's own project is
  # read — so a check that read only the --sandbox checkout would submit a real
  # Cloud Build in belong and promote that entry's latest.
  run_check "$dir" CHECK581_CHECKOUT="$dir/other" \
    FAKE_CHECKOUT_DIR="$dir/other" FAKE_CHECKOUT_PROJECT=belong-prod
  expect_status "$status" 1 "a checkout of a belong project named with CHECK581_CHECKOUT"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json (in $dir/other)" "a checkout of a belong project: that checkout's own project is read"
  expect_contains "$out" "FACT build: FAIL — not reached" "a checkout of a belong project: no build row is measured"
  expect_contains "$out" "FACT aborted: FAIL" "a checkout of a belong project: the run is stopped"
  expect_contains "$err" "belongs to belong-prod" "a checkout of a belong project: the refusal names it"
  assert_no_build "$dir" "a checkout of a belong project"
}

test_a_checkout_of_another_project_than_the_sandbox_one_is_refused() {
  local dir=$work/checkout-other-project
  prepare "$dir"
  run_check "$dir" CHECK581_CHECKOUT="$dir/other" \
    FAKE_CHECKOUT_DIR="$dir/other" FAKE_CHECKOUT_PROJECT=another-chk581-project
  expect_status "$status" 1 "a checkout of a project that is not the sandbox one"
  expect_contains "$out" "FACT build: FAIL — not reached" "a checkout of another project: no build row is measured"
  expect_contains "$err" "belongs to another-chk581-project and the sandbox checkout at $dir/sandbox belongs to ${project}" "a checkout of another project: the refusal names both projects"
  assert_no_build "$dir" "a checkout of another project"
}

test_a_checkout_whose_project_cannot_be_read_is_not_measured() {
  local dir=$work/checkout-unreadable
  prepare "$dir"
  # A doctor that does not answer is no answer, not an empty project: the check
  # cannot say which project the build would run in, so it runs none.
  run_check "$dir" CHECK581_CHECKOUT="$dir/other" \
    FAKE_CHECKOUT_DIR="$dir/other" FAKE_CHECKOUT_PROJECT=another-chk581-project \
    FAKE_CHECKOUT_DOCTOR_STATUS=2
  expect_status "$status" 2 "a checkout whose project cannot be read"
  expect_contains "$out" "FACT build: FAIL — fugaro doctor --json did not answer in $dir/other" "a checkout whose project cannot be read: the row says why"
  assert_no_build "$dir" "a checkout whose project cannot be read"
}

test_a_checkout_that_is_not_one_is_not_measured() {
  local dir=$work/badcheckout
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK581_CHECKOUT="$dir/empty"
  expect_status "$status" 2 "a --checkout that is not a checkout"
  expect_contains "$out" "FACT build: FAIL — --checkout carries no fugaro.yaml" "a --checkout that is not a checkout: the row says why"
  assert_no_build "$dir" "a --checkout that is not a checkout"
}

test_a_build_with_no_build_id_is_a_finding() {
  local dir=$work/no-id
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-id
  expect_status "$status" 1 "a build whose output names no build id"
  expect_contains "$out" "FACT build: FAIL" "a build with no id: the row is a failure"
  expect_contains "$out" "names no Cloud Build build id" "a build with no id: what it says"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds describe" "a build with no id: no build is described"
}

test_a_note_that_names_one_repository_is_a_finding() {
  local dir=$work/one-repo
  prepare "$dir"
  run_check "$dir" FAKE_MODE=one-repo
  expect_status "$status" 1 "a build that baked one repository"
  expect_contains "$out" "FACT baked: FAIL" "a build that baked one repository: the row is a failure"
  expect_contains "$out" "says it baked one repository — acme/sandbox — while the project's map puts acme/data,acme/sandbox on it" "a build that baked one repository: what it says"
  expect_contains "$out" "FACT scan: PASS" "a build that baked one repository: the scan is still read"
}

test_a_note_that_leaves_one_repository_out_is_a_finding() {
  local dir=$work/partial-bake
  prepare "$dir"
  run_check "$dir" FAKE_MODE=partial-bake
  expect_status "$status" 1 "a note that leaves a repository out"
  expect_contains "$out" "FACT baked: FAIL" "a note that leaves a repository out: the row is a failure"
  expect_contains "$out" "acme/data is not among them" "a note that leaves a repository out: the repository it names"
  expect_contains "$out" "publish the map ('fugaro update') and rebuild" "a note that leaves a repository out: the fix"
}

test_a_build_that_bakes_nothing_says_so() {
  local dir=$work/no-bake
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-bake
  expect_status "$status" 1 "a build that baked nothing"
  expect_contains "$out" "FACT baked: FAIL" "a build that baked nothing: the row is a failure"
  expect_contains "$out" "baked no checkout at all" "a build that baked nothing: what it says"
  expect_contains "$out" "imagebake.Decide" "a build that baked nothing: the rule it names"
}

test_a_build_with_no_scan_step_is_a_finding() {
  local dir=$work/no-scan
  prepare "$dir"
  run_check "$dir" FAKE_DESCRIBE=build-noscan.json
  expect_status "$status" 1 "a build with no scan step"
  expect_contains "$out" "FACT scan: FAIL" "a build with no scan step: the row is a failure"
  expect_contains "$out" "SUCCESS with no 'catalog-scan' step" "a build with no scan step: what it says"
  expect_contains "$out" "between the smoke and the promote" "a build with no scan step: the rule it names"
}

test_a_scan_after_the_promote_is_a_finding() {
  local dir=$work/late-scan
  prepare "$dir"
  run_check "$dir" FAKE_DESCRIBE=build-late-scan.json
  expect_status "$status" 1 "a scan after the promote"
  expect_contains "$out" "FACT scan: FAIL" "a scan after the promote: the row is a failure"
  expect_contains "$out" "'catalog-scan' stands at position 4 and 'promote' at 3" "a scan after the promote: the positions"
  expect_contains "$out" "has to run BEFORE the image is moved to latest" "a scan after the promote: what it says"
}

test_a_scan_that_did_not_succeed_is_a_finding() {
  local dir=$work/scan-failed
  prepare "$dir"
  run_check "$dir" FAKE_DESCRIBE=build-scan-failed.json
  expect_status "$status" 1 "a scan that did not succeed"
  expect_contains "$out" "FACT scan: FAIL" "a scan that did not succeed: the row is a failure"
  expect_contains "$out" "ended FAILURE" "a scan that did not succeed: the build's own status"
  expect_contains "$out" "the scan found a credential in the image" "a scan that did not succeed: the build's own detail"
}

test_a_build_that_ended_in_failure_is_a_finding() {
  local dir=$work/build-failed
  prepare "$dir"
  run_check "$dir" FAKE_DESCRIBE=build-failed.json
  expect_status "$status" 1 "a build that ended FAILURE"
  expect_contains "$out" "FACT scan: FAIL" "a build that ended FAILURE: the row is a failure"
  expect_contains "$out" "Step #5 build: the Dockerfile failed" "a build that ended FAILURE: the build's own detail"
}

test_a_build_that_cannot_be_described_is_not_measured() {
  local dir=$work/describe-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=describe-fails
  expect_status "$status" 2 "a build that cannot be described"
  expect_contains "$out" "FACT scan: FAIL — gcloud builds describe fake-build-1 --region=us-east5 did not answer" "a build that cannot be described: the row says why"
  expect_contains "$out" "FACT digest: PASS" "a build that cannot be described: the digest is still read"
}

test_a_describe_that_is_not_a_document_is_not_measured() {
  local dir=$work/describe-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=describe-garbage
  expect_status "$status" 2 "a describe that is not a document"
  expect_contains "$out" "FACT scan: FAIL — gcloud builds describe fake-build-1 printed a document this check could not read" "a describe that is not a document: the row says why"
}

test_an_entry_that_records_no_digest_is_a_finding() {
  local dir=$work/no-digest
  prepare "$dir"
  cat >"$dir/json/catalog-after.json" <<'JSON'
{"project":"fugaro-chk581-test","entries":[
 {"name":"web","tags":["node"],"default":true,"source":"dockerfile acme/sandbox:.fugaro/web.Dockerfile","description":"the Node repositories","digest":""},
 {"name":"data","tags":["postgres"],"default":false,"source":"inline","description":"the data repositories","digest":"sha256:2222222222"}]}
JSON
  run_check "$dir"
  expect_status "$status" 1 "an entry that records no digest"
  expect_contains "$out" "FACT digest: FAIL" "an entry that records no digest: the row is a failure"
  expect_contains "$out" "recorded no digest after the build" "an entry that records no digest: what it says"
  expect_contains "$out" "before it: sha256:1111111111" "an entry that records no digest: the digest it had"
}

test_a_build_that_reports_another_digest_than_the_entry_records_is_a_finding() {
  local dir=$work/digest-other
  prepare "$dir"
  run_check "$dir" FAKE_MODE=digest-other
  expect_status "$status" 1 "a build that reports another digest"
  expect_contains "$out" "FACT digest: FAIL" "a build that reports another digest: the row is a failure"
  expect_contains "$out" "reported the digest sha256:9999999999 and the entry records sha256:3333333333" "a build that reports another digest: both digests are named"
}

test_a_repeated_build_that_records_nothing_is_not_measured() {
  local dir=$work/repeat
  prepare "$dir"
  # The entry is the one it was before the build, and the build reported no
  # digest of its own: a build that recorded nothing and one that recorded the
  # digest the entry already carried look the same here, so the row says so
  # rather than passing on the digest that was already there.
  cp "$dir/json/catalog.json" "$dir/json/catalog-after.json"
  run_check "$dir" FAKE_MODE=no-digest-report
  expect_status "$status" 2 "a repeated build that records nothing"
  expect_contains "$out" "FACT digest: FAIL — the build fake-build-1 of web printed no digest of its own, and the entry records the same digest it had before the build (sha256:1111111111)" "a repeated build that records nothing: the row says what it cannot tell"
}

test_a_build_that_reports_no_digest_and_a_changed_entry_is_read_from_the_change() {
  local dir=$work/no-report-changed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-digest-report
  expect_status "$status" 0 "a build that reports no digest, and an entry that changed"
  expect_contains "$out" "FACT digest: PASS" "a build that reports no digest: the row passes on the change"
  expect_contains "$out" "records the digest sha256:3333333333, where it recorded sha256:1111111111 before this check's build" "a build that reports no digest: the digests it read"
}

test_a_build_that_never_ends_is_not_measured() {
  local dir=$work/slow
  prepare "$dir"
  run_check "$dir" CHECK581_BUILD_TIMEOUT_SECONDS=1 FAKE_MODE=build-slow
  expect_status "$status" 2 "a build that never ends"
  expect_contains "$out" "FACT build: FAIL — the build of web did not finish inside 1s" "a build that never ends: the row says why"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "builds describe" "a build that never ends: no build is described"
  assert_scratch_removed "$dir" "a build that never ends"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK581_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row is a failure"
  expect_contains "$err" "CHECK581_KEEP_SCRATCH is set" "a kept scratch directory: the note"
  [ -n "$scratch" ] && [ -d "$scratch" ] && rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_the_build_is_described_in_the_region_the_project_names() {
  local dir=$work/region
  prepare "$dir"
  # A project whose region is not the check's own default: a describe without
  # --region, or one naming the wrong region, finds no build (the fake gcloud
  # answers NOT_FOUND as the real one does), so the scan row would never be
  # measured.
  sed 's/"region":"us-east5"/"region":"europe-west1"/' "$dir/json/doctor.json" >"$dir/json/doctor.tmp"
  mv "$dir/json/doctor.tmp" "$dir/json/doctor.json"
  run_check "$dir" FAKE_BUILD_REGION=europe-west1
  expect_status "$status" 0 "a project in another region"
  expect_contains "$out" "belongs to ${project} (region europe-west1" "a project in another region: the region it read from the checkout"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "builds describe fake-build-1 --region=europe-west1 --project=${project}" "a project in another region: the build is described in that region"
  expect_contains "$out" "FACT scan: PASS" "a project in another region: the scan is measured"
}

test_the_region_can_be_named() {
  local dir=$work/region-knob
  prepare "$dir"
  # An installation whose builds run in a region of their own (localcfg's
  # BuildRegion): the doctor document carries the project's region, so the knob
  # is how the owner names the one the build was submitted to.
  run_check "$dir" CHECK581_REGION=europe-west1 FAKE_BUILD_REGION=europe-west1
  expect_status "$status" 0 "a region named with the knob"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "builds describe fake-build-1 --region=europe-west1 --project=${project}" "a region named with the knob: the build is described there"
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
t test_a_legacy_map_is_read_from_its_own_member
t test_one_repository_at_two_providers_is_one_repository
t test_doctor_exit_1_is_an_answer
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_a_project_naming_belong_stops_the_check
t test_an_empty_catalog_is_a_finding
t test_a_large_catalog_is_summarised
t test_a_catalog_that_cannot_be_read_is_not_measured
t test_a_map_with_no_entry_over_two_repositories_is_a_finding
t test_a_map_that_cannot_be_read_is_not_measured
t test_a_survey_that_is_not_a_document_is_not_measured
t test_a_project_that_bakes_nothing_says_so
t test_an_entry_can_be_named
t test_an_entry_over_one_repository_is_a_finding
t test_a_named_entry_the_catalog_lacks_is_a_finding
t test_a_build_that_fails_is_a_finding
t test_a_build_refused_for_the_checkout_says_so
t test_an_entry_can_be_built_from_its_own_checkout
t test_a_checkout_of_a_belong_project_is_refused
t test_a_checkout_of_another_project_than_the_sandbox_one_is_refused
t test_a_checkout_whose_project_cannot_be_read_is_not_measured
t test_a_checkout_that_is_not_one_is_not_measured
t test_a_build_with_no_build_id_is_a_finding
t test_a_note_that_names_one_repository_is_a_finding
t test_a_note_that_leaves_one_repository_out_is_a_finding
t test_a_build_that_bakes_nothing_says_so
t test_a_build_with_no_scan_step_is_a_finding
t test_a_scan_after_the_promote_is_a_finding
t test_a_scan_that_did_not_succeed_is_a_finding
t test_a_build_that_ended_in_failure_is_a_finding
t test_a_build_that_cannot_be_described_is_not_measured
t test_a_describe_that_is_not_a_document_is_not_measured
t test_an_entry_that_records_no_digest_is_a_finding
t test_a_build_that_reports_another_digest_than_the_entry_records_is_a_finding
t test_a_repeated_build_that_records_nothing_is_not_measured
t test_a_build_that_reports_no_digest_and_a_changed_entry_is_read_from_the_change
t test_a_build_that_never_ends_is_not_measured
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused
t test_the_build_is_described_in_the_region_the_project_names
t test_the_region_can_be_named

if [ "$failures" -ne 0 ]; then
  echo "check-581_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-581_test.sh: ok"
