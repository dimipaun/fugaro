#!/usr/bin/env bash
# Tests for scripts/live-checks/check146.sh, against a fake gcloud and a fake
# fugaro on PATH: nothing here reaches the network, there is no cloud state, and
# the sweep's log the fake returns carries a sentinel — the title of a pull
# request somebody else opened — that must never reach the block.
#
# The fake gcloud answers the four reads the check makes (the project's jobs,
# the job, an execution of it, that execution's log) from files this test
# writes, and the fake fugaro answers `doctor --json` and the stored view of a
# repository: the FIRST read of a repository is what the sweep found, the second
# what it left, so a case says what the tick did by writing two files.
#
# Usage: bash scripts/live-checks/check146_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check146.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK146_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|installation|job|log):' | sed 's/^/ ... last run: /')"
}

# The installation the whole suite runs against: a sandbox, never the owner's.
PROJECT=prj-sandbox-a
SERVICE_URL=https://credentials.sandbox-a.example/v1/bitbucket/read-token
# What a sweep's log carries that is somebody else's text. A log line is
# arbitrary: anything upstream can write into it, and the check reads the log to
# classify three words — so the sentinel stands for all of it.
TITLE_SENTINEL='SENTINEL-pull-request-title-from-outside-this-run'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation job repositories read-mint sweep log bitbucket-view github-views xtrace restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check146.sh's test: it answers from $FAKE146_DIR and reaches
# nothing. Every call is recorded, so a test can tell a run that swept from one
# that stopped before it did.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE146_DIR/calls"
case "${1:-}" in
run)
  case "${2:-}" in
  jobs)
    case "${3:-}" in
    list) cat "$FAKE146_DIR/jobs.json" ;;
    describe) cat "$FAKE146_DIR/job.json" ;;
    execute)
      if [ -s "$FAKE146_DIR/exec.status" ]; then
        printf 'fake gcloud: the execution failed\n' >&2
        exit "$(cat "$FAKE146_DIR/exec.status")"
      fi
      cat "$FAKE146_DIR/exec.txt"
      ;;
    *)
      printf 'fake gcloud: unknown jobs verb %s\n' "${3:-}" >&2
      exit 64
      ;;
    esac
    ;;
  *)
    printf 'fake gcloud: unknown run verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
logging)
  cat "$FAKE146_DIR/log.json"
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check146.sh's test: doctor's answer and the stored views
# come from $FAKE146_DIR. The first read of a repository is the state the sweep
# found it in, the second the state it left, so a case writes the two files the
# tick is supposed to have made a difference between.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE146_DIR/calls"
case "${1:-}" in
doctor)
  cat "$FAKE146_DIR/doctor.json"
  ;;
coordinator)
  shift
  repo=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --repo)
      repo=${2:-}
      shift 2
      ;;
    *) shift ;;
    esac
  done
  if [ -z "$repo" ]; then
    printf 'fake fugaro: no --repo\n' >&2
    exit 64
  fi
  slug=$(printf '%s' "$repo" | sed 's|/|__|g')
  if [ -e "$FAKE146_DIR/seen-$slug" ]; then phase=after; else
    phase=before
    : >"$FAKE146_DIR/seen-$slug"
  fi
  file="$FAKE146_DIR/$phase-$slug.json"
  # No after file means the tick left the view exactly as it found it, which is
  # what the GitHub half of the check is about: the second read then returns the
  # first read's document, byte for byte.
  [ -f "$file" ] || file="$FAKE146_DIR/before-$slug.json"
  if [ ! -f "$file" ]; then
    printf 'fake fugaro: no stored view for %s (%s)\n' "$repo" "$phase" >&2
    exit 1
  fi
  cat "$file"
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/fugaro"
}

# ---------------------------------------------------------------- the fixtures
write_doctor() { # dir PROJECT [NAME] [REGION]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "%s", "region": "%s"}}\n' \
    "${3:-sandbox-a}" "$2" "${4:-us-east5}" >"$1/state/doctor.json"
}

# write_job dir PROVIDER:REPO... — the job's own document, with its repository
# list in FUGARO_COORDINATOR_REPOS. JOB_COORD_URL, JOB_RUN_URL (empty for
# absent), JOB_NAME and JOB_LABEL say what else its environment and metadata
# carry, so a case can put the run path's variable on it or take its label away.
write_job() {
  local dir=$1
  shift
  FAKE146_REPOS="$*" \
    JOB_COORD_URL="${JOB_COORD_URL:-}" \
    JOB_RUN_URL="${JOB_RUN_URL:-}" \
    JOB_NAME="${JOB_NAME:-fugaro-coordinator}" \
    JOB_LABEL="${JOB_LABEL-coordinator}" \
    python3 - "$dir/state/job.json" "$dir/state/jobs.json" <<'PY'
import json, os, sys

repos = []
for pair in os.environ["FAKE146_REPOS"].split():
    provider, _, repo = pair.partition(":")
    repos.append({"provider": provider, "repo": repo})
env = [{"name": "FUGARO_COORDINATOR_REPOS", "value": json.dumps(repos)}]
if os.environ["JOB_COORD_URL"]:
    env.append({"name": "FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL",
                "value": os.environ["JOB_COORD_URL"]})
if os.environ["JOB_RUN_URL"]:
    env.append({"name": "FUGARO_CREDENTIALS_SERVICE_URL", "value": os.environ["JOB_RUN_URL"]})
labels = {}
if os.environ["JOB_LABEL"]:
    labels["fugaro_role"] = os.environ["JOB_LABEL"]
job = {"metadata": {"name": os.environ["JOB_NAME"], "labels": labels},
       "spec": {"template": {"template": {"containers": [{"env": env}]}}}}
open(sys.argv[1], "w", encoding="utf-8").write(json.dumps(job))
open(sys.argv[2], "w", encoding="utf-8").write(json.dumps([job]))
PY
}

# write_view FILE REPO PROVIDER STORED STALE RECORDS [WHEN] — one repository's
# stored view, in the shape `fugaro coordinator status --from-store --repo R
# --json` prints. A record carries the title sentinel, which is what a view
# holds that the check must not print.
write_view() {
  local file=$1 repo=$2 provider=$3 stored=$4 stale=$5 records=$6 when=${7:-2026-11-03T10:00:00Z}
  local rec="" i
  for ((i = 1; i <= records; i++)); do
    rec="$rec${rec:+,}{\"number\": $i, \"branch\": \"topic/$i\", \"title\": \"$TITLE_SENTINEL\"}"
  done
  local stale_doc='{}'
  if [ "$stale" = yes ]; then
    stale_doc='{"since": "2026-11-03T09:00:00Z", "reason": "the project-wide read serves github only, so this repository was not read"}'
  fi
  local stored_json=false
  [ "$stored" = yes ] && stored_json=true
  printf '{"repo": "%s", "provider": "%s", "stored": %s, "view": {"records": [%s], "generated_at": "%s"}, "stale": %s}\n' \
    "$repo" "$provider" "$stored_json" "$rec" "$when" "$stale_doc" >"$file"
}

# write_log FILE LINE... — an execution's log as gcloud logging read prints it
# in JSON, one line per entry, in the order given.
write_log() {
  local file=$1
  shift
  local entries="" line
  for line in "$@"; do
    entries="$entries${entries:+,}{\"textPayload\": \"$line\"}"
  done
  printf '[%s]\n' "$entries" >"$file"
}

# A sweep that read the Bitbucket repository, and the tick's own summary.
write_healthy_log() { # FILE
  write_log "$1" \
    "$TITLE_SENTINEL" \
    "acme/legacy: 2 records, 0 in flight, 0 collisions, 3 pull requests read, 0 unread, 0 records pruned" \
    "acme/webapp: 3 records, 0 in flight, 0 collisions, 5 pull requests read, 0 unread, 1 records pruned" \
    "swept 2 repositories: 2 reconciled, 0 stale"
}

# The tick that still marks the Bitbucket repository unsupported: the failure
# the whole Bitbucket-credentials work exists to remove, and this suite's red
# step.
write_stale_log() { # FILE
  write_log "$1" \
    "$TITLE_SENTINEL" \
    "acme/legacy: stale: the project-wide read serves github only, so this repository was not read" \
    "acme/webapp: 3 records, 0 in flight, 0 collisions, 5 pull requests read, 0 unread, 1 records pruned" \
    "swept 2 repositories: 1 reconciled, 1 stale"
}

# setup_case NAME — a fresh case: the fakes, a sandbox installation, a
# coordinator job that reconciles one GitHub and one Bitbucket repository, a
# healthy tick, and views in which the Bitbucket repository is not stored yet
# and the GitHub one is what it always was. A case changes what it is about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  write_doctor "$dir" "$PROJECT"
  JOB_COORD_URL=$SERVICE_URL JOB_RUN_URL='' write_job "$dir" github:acme/webapp bitbucket:acme/legacy
  printf 'fugaro-coordinator-abc12\n' >"$dir/state/exec.txt"
  : >"$dir/state/exec.status"
  write_healthy_log "$dir/state/log.json"
  write_view "$dir/state/before-acme__legacy.json" acme/legacy bitbucket no no 0
  write_view "$dir/state/after-acme__legacy.json" acme/legacy bitbucket yes no 2
  write_view "$dir/state/before-acme__webapp.json" acme/webapp github yes no 3
}

# run146 [ARGS...] — runs the check on the case at hand. RUN146_ARGS adds
# arguments, RUN146_PATH prefixes PATH (for a fake that has to come first), and
# RUN146_ENV names environment variables (NAME=value). Sets $status, $out and
# $dir/err.
run146() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE146_DIR="$dir/state" \
    PATH="${RUN146_PATH:+$RUN146_PATH:}$dir/bin:$PATH" \
    ${RUN146_ENV[@]+"${RUN146_ENV[@]}"} \
    bash "$script" ${RUN146_ARGS[@]+"${RUN146_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN146_ARGS=()
  RUN146_ENV=()
  RUN146_PATH=""
}

# assert_no_document DIR WHAT — nothing the run printed carries the text a
# document it read holds. The check reads other people's words (a pull request's
# title in a stored view, anything at all in a sweep's log) and prints three
# classified words about them; this is the assertion that says so.
assert_no_document() {
  local dir=$1 what=$2
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$TITLE_SENTINEL" \
    "$what: another person's text in the block"
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 146 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

expect_swept() { # what
  grep -q '^gcloud run jobs execute ' "$dir/state/calls" \
    || fail "$1: the sweep never ran (no execution in $(tr '\n' ';' <"$dir/state/calls"))"
}
expect_no_sweep() { # what
  grep -q '^gcloud run jobs execute ' "$dir/state/calls" \
    && fail "$1: an execution ran before the check had an answer to give" || true
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  if [ -z "$scratch" ]; then
    fail "$2: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$2: the scratch directory $scratch survived the run"
  [ -z "$(find "$1/tmp" -mindepth 1 2>/dev/null)" ] || fail "$2: files were left in TMPDIR: $(find "$1/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  run146 --installation "$PROJECT"
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_contains "$out" "CHECK 146 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT installation: PASS — " "happy path: the installation"
  expect_contains "$out" "$PROJECT" "happy path: the project the config names"
  expect_contains "$out" "FACT job: PASS — " "happy path: the job"
  expect_contains "$out" "fugaro-coordinator" "happy path: the job it found"
  expect_contains "$out" "FACT repositories: PASS — " "happy path: the repositories"
  expect_contains "$out" "1 github and 1 bitbucket" "happy path: what the sweep reconciles"
  expect_contains "$out" "FACT read-mint: PASS — " "happy path: the read mint's variable"
  expect_contains "$out" "F23" "happy path: the fact quotes the variable split"
  expect_contains "$out" "FACT sweep: PASS — " "happy path: the tick ran"
  expect_contains "$out" "fugaro-coordinator-abc12" "happy path: the execution it waited for"
  expect_contains "$out" "FACT log: PASS — " "happy path: the log is read"
  expect_contains "$out" "FACT bitbucket-view: PASS — " "happy path: the Bitbucket view"
  expect_contains "$out" "2 records" "happy path: the records the sweep stored"
  expect_contains "$out" "FACT github-views: PASS — " "happy path: the GitHub views"
  expect_contains "$out" "FACT xtrace: PASS — " "happy path: not tracing"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the Restore"
  expect_swept "happy path"
  assert_no_document "$dir" "happy path"
  expect_scratch_gone "$dir" "happy path"
}

# THE RED STEP, kept as a test: a sweep that still marks the Bitbucket
# repository unsupported prints `<repo>: stale: ...` with the marker's sentence
# behind it, and the check FAILs on exactly that. Its block also has to carry
# none of the log's own text while it does.
test_a_sweep_that_still_marks_it_unsupported_is_a_fail() {
  setup_case stale
  write_stale_log "$dir/state/log.json"
  write_view "$dir/state/after-acme__legacy.json" acme/legacy bitbucket no yes 0
  run146 --installation "$PROJECT"
  expect_status "$status" 1 "stale sweep"
  expect_contains "$out" "FACT log: FAIL — " "stale sweep: the log row fails"
  expect_contains "$out" "acme/legacy" "stale sweep: the repository that was not read is named"
  expect_contains "$out" "it was NOT read" "stale sweep: the row says what the line means"
  expect_contains "$out" "FACT bitbucket-view: FAIL — " "stale sweep: the stored view fails with it"
  expect_contains "$out" "CHECK 146 RESULT END (exit 1)" "stale sweep: the block says the run failed"
  expect_contains "$out" "FACT sweep: PASS — " "stale sweep: the tick itself did run"
  assert_no_document "$dir" "stale sweep"
}

# The Bitbucket half on its own: the tick reconciles it (the log says so) and
# leaves it unstored all the same. The log row passes and the view row is what
# catches it, so a check that only read the log would call this a pass.
test_a_view_that_was_not_stored_is_a_fail() {
  setup_case unstored
  write_view "$dir/state/after-acme__legacy.json" acme/legacy bitbucket no no 0
  run146 --installation "$PROJECT"
  expect_status "$status" 1 "unstored view"
  expect_contains "$out" "FACT log: PASS — " "unstored view: the log row passes"
  expect_contains "$out" "FACT bitbucket-view: FAIL — " "unstored view: the stored view fails"
  expect_contains "$out" "stored=no" "unstored view: the row says what it found"
  assert_no_document "$dir" "unstored view"
}

# The GitHub half: a stored view that is not the one the sweep found. The
# Bitbucket read still passes, and the row that fails is the one about what the
# sweep must not have changed (G8).
test_a_github_view_that_changed_is_a_fail() {
  setup_case changed
  write_view "$dir/state/after-acme__webapp.json" acme/webapp github yes no 4
  run146 --installation "$PROJECT"
  expect_status "$status" 1 "changed github view"
  expect_contains "$out" "FACT bitbucket-view: PASS — " "changed github view: the Bitbucket read passed"
  expect_contains "$out" "FACT github-views: FAIL — " "changed github view: the row fails"
  expect_contains "$out" "acme/webapp" "changed github view: the repository that moved is named"
  assert_no_document "$dir" "changed github view"
}

# A GitHub view that could not be read at all before the sweep: the row is a
# FAIL rather than a pass, because a run that could not compare anything has
# measured nothing.
test_a_github_view_that_could_not_be_read_is_a_fail() {
  setup_case unreadable
  rm -f "$dir/state/before-acme__webapp.json"
  run146 --installation "$PROJECT"
  expect_status "$status" 1 "unreadable github view"
  expect_contains "$out" "FACT github-views: FAIL — " "unreadable github view: the row fails"
  expect_contains "$out" "could not be compared" "unreadable github view: the row says why"
  assert_no_document "$dir" "unreadable github view"
}

# F23, live: the run path's variable on the job. The check stops before it
# executes anything, because a sweep whose token is minted at whatever a run's
# environment names is the thing this half of the work removed.
test_the_run_path_variable_on_the_job_is_refused() {
  setup_case runpath
  JOB_COORD_URL='' JOB_RUN_URL=https://service.sandbox-a.example write_job "$dir" github:acme/webapp bitbucket:acme/legacy
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "run path variable"
  expect_contains "$out" "FACT read-mint: FAIL — " "run path variable: the row fails"
  expect_contains "$out" "F23" "run path variable: the row names the fact"
  expect_contains "$out" "FACT aborted: FAIL — " "run path variable: the stop is recorded"
  expect_no_sweep "run path variable"
  assert_no_document "$dir" "run path variable"
}

# No read mint at all: the service is off, or the job predates BB5. Its
# Bitbucket repositories would stay StaleUnsupported, which is a FAIL and not a
# pass, and the check stops rather than sweeping to watch it happen.
test_a_job_with_no_read_mint_is_a_fail() {
  setup_case nomint
  JOB_COORD_URL='' JOB_RUN_URL='' write_job "$dir" github:acme/webapp bitbucket:acme/legacy
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "no read mint"
  expect_contains "$out" "FACT read-mint: FAIL — " "no read mint: the row fails"
  expect_contains "$out" "StaleUnsupported" "no read mint: the row says what the repositories get instead"
  expect_contains "$out" "FACT aborted: FAIL — " "no read mint: the stop is recorded"
  expect_no_sweep "no read mint"
  assert_no_document "$dir" "no read mint"
}

# An installation whose coordinator reconciles no Bitbucket repository: the
# check would measure nothing, so it stops and says so instead of passing.
test_a_job_with_no_bitbucket_repository_stops() {
  setup_case nobitbucket
  JOB_COORD_URL=$SERVICE_URL write_job "$dir" github:acme/webapp
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "no bitbucket repository"
  expect_contains "$out" "FACT repositories: FAIL — " "no bitbucket repository: the row fails"
  expect_contains "$out" "FACT aborted: FAIL — " "no bitbucket repository: the stop is recorded"
  expect_no_sweep "no bitbucket repository"
  assert_no_document "$dir" "no bitbucket repository"
}

# The owner's own installation, refused by name before anything is read.
test_belong_is_refused() {
  setup_case belong
  run146 --installation belong
  expect_status "$status" 2 "belong"
  expect_contains "$out" "FACT installation: FAIL — " "belong: the row fails"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "belong" "belong: the guard names it"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "gcloud" "belong: nothing was read from the project"
  assert_no_document "$dir" "belong"
}

# A sandbox named, and a config in force that describes a different one: the
# check reads the installation it was pointed at and no other.
test_the_name_and_the_config_have_to_agree() {
  setup_case mismatch
  write_doctor "$dir" prj-sandbox-b
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "mismatch"
  expect_contains "$out" "FACT installation: FAIL — " "mismatch: the row fails"
  expect_contains "$out" "prj-sandbox-b" "mismatch: the config's own project is named"
  expect_no_sweep "mismatch"
  assert_no_document "$dir" "mismatch"
}

# No coordinator job in the project: the opt-in is off, or it was never
# deployed, and there is nothing to sweep.
test_a_project_without_a_coordinator_job_stops() {
  setup_case nojob
  JOB_LABEL='' JOB_COORD_URL=$SERVICE_URL write_job "$dir" github:acme/webapp bitbucket:acme/legacy
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "no job"
  expect_contains "$out" "FACT job: FAIL — " "no job: the row fails"
  expect_contains "$out" "coordinator" "no job: the label it looked for is named"
  expect_no_sweep "no job"
  assert_no_document "$dir" "no job"
}

# More than one coordinator job: the check cannot tell which one the
# installation reconciles with, and it does not pick.
test_two_coordinator_jobs_stop() {
  setup_case twojobs
  write_job "$dir" github:acme/webapp bitbucket:acme/legacy
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
jobs = json.load(open(sys.argv[1]))
jobs.append(dict(jobs[0]))
open(sys.argv[1], "w").write(json.dumps(jobs))
PY
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "two jobs"
  expect_contains "$out" "FACT job: FAIL — " "two jobs: the row fails"
  expect_no_sweep "two jobs"
  assert_no_document "$dir" "two jobs"
}

# An execution that does not finish: the check learns nothing about the read,
# and the rows it never reached say so rather than reading as passes.
test_a_sweep_that_does_not_run_stops() {
  setup_case sweepless
  printf '1\n' >"$dir/state/exec.status"
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "sweep did not run"
  expect_contains "$out" "FACT sweep: FAIL — " "sweep did not run: the row fails"
  expect_contains "$out" "FACT log: FAIL — not reached" "sweep did not run: the log row was never measured"
  expect_contains "$out" "FACT bitbucket-view: FAIL — not reached" "sweep did not run: the view row was never measured"
  expect_contains "$out" "FACT restore: PASS — " "sweep did not run: the Restore still reports"
  assert_no_document "$dir" "sweep did not run"
  expect_scratch_gone "$dir" "sweep did not run"
}

# An execution with no name: without it the log cannot be addressed, and a run
# that guessed would read the previous tick's.
test_an_execution_with_no_name_stops() {
  setup_case noname
  : >"$dir/state/exec.txt"
  run146 --installation "$PROJECT"
  expect_status "$status" 2 "no execution name"
  expect_contains "$out" "FACT sweep: FAIL — " "no execution name: the row fails"
  expect_contains "$out" "FACT log: FAIL — not reached" "no execution name: the log row was never measured"
  assert_no_document "$dir" "no execution name"
}

# A shell that arrived tracing is refused: `bash -x` prints every document the
# check reads as it reads it.
test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE146_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    bash -x "$script" --installation "$PROJECT" 2>"$dir/err") || status=$?
  expect_status "$status" 2 "xtrace"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "+ " "xtrace: the trace really was on"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "tracing" "xtrace: the guard names why"
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "$TITLE_SENTINEL" "xtrace: the guard got in before a document was read"
  expect_no_sweep "xtrace"
}

# The arguments: the installation is required, an unknown one is refused, and
# --help prints the usage without reading anything.
test_usage() {
  setup_case usage
  run146
  expect_status "$status" 2 "no --installation"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "usage" "no --installation: the usage line"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "--installation" "no --installation: the flag is named"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "gcloud" "no --installation: nothing was read"

  run146 --installation "$PROJECT" --sandbox
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$(cat "$dir/err" 2>/dev/null || true)" "unknown argument" "an unknown argument: the refusal"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "gcloud" "an unknown argument: nothing was read"
  assert_no_document "$dir" "usage"
}

# The suite's own control, mirroring the check's red step: the assertions above
# are worth nothing if a run that PRINTS a document it read goes unnoticed. This
# one prints the log and the stored view it was handed, and the three assertions
# have to catch it.
#
# The counter is a variable of the subshell, not the suite's failures: the FAIL
# lines this provokes on purpose are counted rather than printed, because output
# that reads 'FAIL' beside a suite that passed is output nobody trusts.
test_the_document_assertions_catch_an_echoing_script() {
  local dir=$work/detector caught
  mkdir -p "$dir/tmp"
  cat >"$dir/echoing.sh" <<'ECHO_EOF'
#!/usr/bin/env bash
# A script that prints the documents it was given: the failure check146.sh's own
# test exists to catch.
echo "$TITLE_SENTINEL"
ECHO_EOF
  TITLE_SENTINEL=$TITLE_SENTINEL bash "$dir/echoing.sh" >"$dir/out" 2>"$dir/err" || true
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    assert_no_document "$dir" "detector"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -eq 1 ] \
    || fail "detector: an echoing script raised $caught of the one assertion that holds other people's text out of the block, so it is vacuous"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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

t test_happy_path
t test_a_sweep_that_still_marks_it_unsupported_is_a_fail
t test_a_view_that_was_not_stored_is_a_fail
t test_a_github_view_that_changed_is_a_fail
t test_a_github_view_that_could_not_be_read_is_a_fail
t test_the_run_path_variable_on_the_job_is_refused
t test_a_job_with_no_read_mint_is_a_fail
t test_a_job_with_no_bitbucket_repository_stops
t test_belong_is_refused
t test_the_name_and_the_config_have_to_agree
t test_a_project_without_a_coordinator_job_stops
t test_two_coordinator_jobs_stop
t test_a_sweep_that_does_not_run_stops
t test_an_execution_with_no_name_stops
t test_xtrace_is_refused
t test_usage
t test_the_document_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check146_test: $failures failure(s)" >&2
  exit 1
fi
echo "check146_test: all tests passed" >&2
