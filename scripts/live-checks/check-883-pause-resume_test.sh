#!/usr/bin/env bash
# Tests for scripts/live-checks/check-883-pause-resume.sh. NOTHING here is a
# cloud, a project, an installation or a run: every case writes the answers the
# check reads — `fugaro doctor --json`, the published policy, the pool listing,
# a launch's own document, a sequence of `fugaro runs ls --json` documents and a
# run record read with `gcloud storage cat` — and drives the check against a
# fake `fugaro` and a fake `gcloud` on PATH that the case owns. No case can
# spend anything or reach anything.
#
# The readings are the point. Check 883 asks three questions of one pause: did
# the run pause, did the resident orchestrator resume it, and did the resumed
# run draw on the pool the launch named? So the cases that matter are the ones
# where a reading is bent: an installation with no resident orchestrator, a
# policy that carries no pause settings, a pool the installation does not
# define, a run that ends without pausing, a pause nobody resumes, a resumed
# record with no source at all, and a resumed run that drew on another pool.
#
# Usage: bash scripts/live-checks/check-883-pause-resume_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-883-pause-resume.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK883_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK883_TEST_HEAD:-1600}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK883_TEST_HEAD:-1600}")"
}

# ------------------------------------------------------------ the two fakes

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE883_LOG:?}"
dir=${FAKE883_DIR:?}
case "${1:-}" in
doctor)
  # doctor writes its whole document and THEN exits 1 whenever any of its checks
  # failed (emitDoctor returns an ExitError for !o.OK), so a case bends the exit
  # status on its own: the document is the answer, the status is not.
  status=$(cat "$dir/doctor-status" 2>/dev/null || printf 0)
  cat "$dir/doctor.json"
  if [ "$status" != 0 ]; then
    printf 'fugaro doctor found a problem; see above\n' >&2
    exit "$status"
  fi
  ;;
project)
  status=$(cat "$dir/policy-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    cat "$dir/policy.err" >&2 2>/dev/null || printf 'fake fugaro: the policy read was refused\n' >&2
    exit "$status"
  fi
  cat "$dir/policy.json"
  ;;
accounts)
  cat "$dir/pools.txt"
  ;;
run)
  status=$(cat "$dir/run-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    cat "$dir/run.err" >&2 2>/dev/null || printf 'fake fugaro: the launch was refused\n' >&2
    exit "$status"
  fi
  cat "$dir/run.json"
  ;;
runs)
  # The listing the check polls: the nth call answers runs-<n>.json, and every
  # call after the last file the case wrote answers that file again.
  n=$(( $(cat "$dir/runs-ls-calls" 2>/dev/null || printf 0) + 1 ))
  printf '%s' "$n" >"$dir/runs-ls-calls"
  i=$n
  while [ "$i" -ge 1 ]; do
    if [ -f "$dir/runs-$i.json" ]; then
      cat "$dir/runs-$i.json"
      exit 0
    fi
    i=$((i - 1))
  done
  printf '{"runs":[]}\n'
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"${FAKE883_GCLOUD_LOG:?}"
dir=${FAKE883_GCLOUD_DIR:?}
case "${1:-} ${2:-}" in
"storage cat")
  uri=${3:?}
  status=$(cat "$dir/record-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake gcloud: the object was not found\n' >&2
    exit "$status"
  fi
  id=$(basename "$(dirname "$uri")")
  if [ -f "$dir/record-$id.json" ]; then cat "$dir/record-$id.json"; else printf '{}\n'; fi
  ;;
*)
  printf 'fake gcloud: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

# --------------------------------------------------------------- the fixtures

# LAUNCHED and RESUMED are the two run ids every case's documents are about.
LAUNCHED=run-aaaa1111
RESUMED=run-bbbb2222
SLUG=acme__sub-app
BUCKET=fugaro-runs-test-project

# doctor_json: what `fugaro doctor --json` answers, with the resident
# orchestrator's own row (internal/cli/doctor_orchestrator_identity.go, id
# orchestrator-identity).
doctor_json() { # doctor_json <out> [bend]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
check = {"id": "orchestrator-identity", "ok": True}
if sys.argv[2] == "no-orchestrator":
    check = {"id": "orchestrator-identity", "ok": True, "severity": "info",
             "problem": "this installation has no resident orchestrator: fugaro-orchestrator@test-project.iam.gserviceaccount.com is not in test-project, so the installation has not opted in and no row of the design's section 4.3 table exists to check",
             "fix": "no action: an installation that did not ask for a resident orchestrator plans exactly as it did before"}
doc = {"ok": True, "checks": [check],
       "project": {"name": "test", "gcp_project": "test-project", "region": "us-east5"}}
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# policy_json: the published policy, carrying the pause block PR9 publishes.
policy_json() { # policy_json <out> [bend]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
policy = {"version": 3, "model_routing": {"coder": [{"kind": "pool", "pool": "team-a"}]},
          "pause": {"wait_in_job": "5m", "min_pause": "30m", "max_pause": "6h"}}
if sys.argv[2] == "no-pause":
    policy = {"version": 3, "model_routing": {"coder": [{"kind": "pool", "pool": "team-a"}]}}
doc = {"project": "test", "policy": policy}
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# pools: the installation's own pool listing (`fugaro accounts pool ls`), whose
# NAME column is what says a pool exists.
pools() { # pools <out> [name]
  local name=${2:-team-a}
  {
    printf 'pools for project test (offline)\n\n'
    printf 'NAME\tSTRATEGY\tMEMBERS\tSERVED BY\n'
    printf '%s\tordered\tpersonal\tthe credentials service\n' "$name"
  } >"$1"
}

row() { # row <status> [extra json members...]
  printf '{"run":"%s/%s","repo":"acme/sub-app","run_id":"%s","status":"%s"%s}' \
    "$SLUG" "$LAUNCHED" "$LAUNCHED" "$1" "${2:-}"
}

# case_dir builds one case's own world in the state the happy path needs: the
# installation has a resident orchestrator, its policy carries the pause
# settings, the pool exists, the launch names a run, the run's row reads
# `running` and then `paused`, the paused row names the run that resumed it, and
# that run's record drew on the named pool. A case then bends exactly one of
# them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  GCLOUD_CALLS="$answers/gcloud-calls"
  : >"$CALLS"
  : >"$GCLOUD_CALLS"
  doctor_json "$answers/doctor.json"
  policy_json "$answers/policy.json"
  pools "$answers/pools.txt"
  printf '{"project":"test","run":"%s/%s","repo":"acme/sub-app","run_id":"%s","status":"launched"}\n' \
    "$SLUG" "$LAUNCHED" "$LAUNCHED" >"$answers/run.json"
  printf '{"runs":[%s]}\n' "$(row running)" >"$answers/runs-1.json"
  printf '{"runs":[%s]}\n' "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" >"$answers/runs-2.json"
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z","resumed_by":"'"$RESUMED"'"')" >"$answers/runs-3.json"
  # The resumed run's own record: its stage was served from the launcher's pool.
  printf '{"version":1,"run_id":"%s","stages":[{"stage":"implement","source":{"kind":"launcher-pool","pool":"team-a"}}]}\n' \
    "$RESUMED" >"$answers/record-$RESUMED.json"
}

# ------------------------------------------------------------------ the runs

run883_raw() { # run883_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE883_LOG="${CALLS:-/dev/null}" FAKE883_DIR="${answers:-$work}" \
    FAKE883_GCLOUD_LOG="${GCLOUD_CALLS:-/dev/null}" FAKE883_GCLOUD_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="${confighome:-$work}" \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

# Every check of this family runs with the shortest intervals that still
# exercise the polls, so a case that never pauses never waits.
run883() { # run883 [extra args...]
  run883_raw --repo acme/sub-app --pool team-a --poll-seconds 0 --pause-wait 2 --resume-wait 2 "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run883
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 883 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" launch PASS "the happy path"
  expect_row "$out" pause PASS "the happy path"
  expect_row "$out" resume PASS "the happy path"
  expect_row "$out" pool PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  case "$out" in *UNVERIFIED*) fail "the happy path: unexpected UNVERIFIED row" ;; esac
  case "$out" in *"|FAIL|"*) fail "the happy path: unexpected FAIL row" ;; esac
  # The rows really read what they say they read.
  expect_contains "$(cat "$CALLS")" "fugaro doctor --json" "the happy path: the premise read the installation"
  expect_contains "$(cat "$CALLS")" "fugaro project policy show --json --project test" "the happy path: the premise read the pause settings"
  expect_contains "$(cat "$CALLS")" "fugaro accounts pool ls --project test" "the happy path: the premise read the pool"
  expect_contains "$(cat "$CALLS")" "fugaro run --repo acme/sub-app --pool team-a" "the happy path: the launch named the pool"
  expect_contains "$(cat "$CALLS")" "fugaro runs ls --repo acme/sub-app --project test --json" "the happy path: the pause and the resume were read from runs ls"
  expect_contains "$(cat "$GCLOUD_CALLS")" "gcloud storage cat gs://$BUCKET/runs/$SLUG/$RESUMED/result.json" \
    "the happy path: the pool row read the resumed run's own record"
}

test_help() {
  CALLS=
  run883_raw --help
  expect_status "$status" 0 "check-883 --help"
  expect_contains "$out" "Usage:" "check-883 --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run883_raw --dry-run --repo acme/sub-app --pool team-a
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 883 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called fugaro: $(head -c 400 "$CALLS")"
  [ ! -s "$GCLOUD_CALLS" ] || fail "a dry run: it called gcloud: $(head -c 400 "$GCLOUD_CALLS")"
}

test_a_missing_repository_is_refused() {
  CALLS=
  run883_raw --pool team-a
  expect_status "$status" 2 "a missing repository"
  expect_contains "$err" "no --repo was given" "a missing repository"
}

test_a_missing_pool_is_refused() {
  CALLS=
  run883_raw --repo acme/sub-app
  expect_status "$status" 2 "a missing pool"
  expect_contains "$err" "no --pool was given" "a missing pool"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE883_LOG="$CALLS" FAKE883_DIR="$answers" \
    FAKE883_GCLOUD_LOG="$GCLOUD_CALLS" FAKE883_GCLOUD_DIR="$answers" \
    bash "$script" --repo acme/sub-app --pool team-a 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called fugaro: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE883_LOG="$CALLS" FAKE883_DIR="$answers" \
    FAKE883_GCLOUD_LOG="$GCLOUD_CALLS" FAKE883_GCLOUD_DIR="$answers" \
    bash -x "$script" --repo acme/sub-app --pool team-a 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_an_installation_with_no_resident_orchestrator_stops_the_check() {
  case_dir no_orchestrator
  doctor_json "$answers/doctor.json" no-orchestrator
  run883
  expect_status "$status" 2 "an installation with no resident orchestrator"
  expect_row "$out" premise FAIL "an installation with no resident orchestrator"
  expect_contains "$out" "has no resident orchestrator" "an installation with no resident orchestrator"
  [ ! -s "$CALLS" ] || case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "an installation with no resident orchestrator: it launched a run anyway" ;;
  esac
}

test_a_policy_without_pause_settings_stops_the_check() {
  case_dir no_pause_settings
  policy_json "$answers/policy.json" no-pause
  run883
  expect_status "$status" 2 "a policy without pause settings"
  expect_row "$out" premise FAIL "a policy without pause settings"
  expect_contains "$out" "carries no pause" "a policy without pause settings"
}

test_a_policy_read_that_does_not_answer_stops_the_check() {
  case_dir policy_unreadable
  printf 7 >"$answers/policy-status"
  run883
  expect_status "$status" 2 "a policy read that does not answer"
  expect_row "$out" premise FAIL "a policy read that does not answer"
  expect_contains "$out" "could not be read" "a policy read that does not answer"
}

test_a_pool_the_installation_does_not_define_stops_the_check() {
  case_dir pool_missing
  pools "$answers/pools.txt" other-pool
  run883
  expect_status "$status" 2 "a pool the installation does not define"
  expect_row "$out" premise FAIL "a pool the installation does not define"
  expect_contains "$out" "does not define the pool team-a" "a pool the installation does not define"
}

test_a_launch_that_is_refused_stops_the_check() {
  case_dir launch_refused
  printf 1 >"$answers/run-status"
  printf 'fugaro: this repository has no coder stage the chain can serve\n' >"$answers/run.err"
  run883
  expect_status "$status" 2 "a launch that is refused"
  expect_row "$out" launch FAIL "a launch that is refused"
  expect_contains "$out" "no coder stage the chain can serve" "a launch that is refused"
}

test_a_run_that_ends_without_pausing_leaves_the_row_unverified() {
  case_dir never_pauses
  printf '{"runs":[%s]}\n' "$(row running)" >"$answers/runs-1.json"
  printf '{"runs":[%s]}\n' "$(row failed ',"reason":"the model call failed"')" >"$answers/runs-2.json"
  rm -f "$answers/runs-3.json"
  run883
  expect_status "$status" 3 "a run that ends without pausing"
  expect_row "$out" pause UNVERIFIED "a run that ends without pausing"
  expect_contains "$out" "ended failed" "a run that ends without pausing"
  expect_row "$out" resume UNVERIFIED "a run that ends without pausing"
}

test_a_pause_nobody_resumes_leaves_the_row_unverified() {
  case_dir never_resumes
  printf '{"runs":[%s]}\n' "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" >"$answers/runs-2.json"
  rm -f "$answers/runs-3.json"
  run883
  expect_status "$status" 3 "a pause nobody resumes"
  expect_row "$out" pause PASS "a pause nobody resumes"
  expect_row "$out" resume UNVERIFIED "a pause nobody resumes"
  expect_contains "$out" "names the run that resumed" "a pause nobody resumes"
  expect_row "$out" pool UNVERIFIED "a pause nobody resumes"
}

test_a_resumed_record_without_a_source_leaves_the_row_unverified() {
  case_dir record_without_source
  printf '{"version":1,"run_id":"%s","stages":[{"stage":"implement"}]}\n' "$RESUMED" \
    >"$answers/record-$RESUMED.json"
  run883
  expect_status "$status" 3 "a resumed record without a source"
  expect_row "$out" resume PASS "a resumed record without a source"
  expect_row "$out" pool UNVERIFIED "a resumed record without a source"
  expect_contains "$out" "carries no stage source" "a resumed record without a source"
}

test_a_resumed_run_on_another_pool_is_a_finding() {
  case_dir other_pool
  printf '{"version":1,"run_id":"%s","stages":[{"stage":"implement","source":{"kind":"launcher-pool","pool":"other-pool"}}]}\n' \
    "$RESUMED" >"$answers/record-$RESUMED.json"
  run883
  expect_status "$status" 1 "a resumed run on another pool"
  expect_row "$out" pool FAIL "a resumed run on another pool"
  expect_contains "$out" "other-pool" "a resumed run on another pool"
  expect_row "$out" pause PASS "a resumed run on another pool"
}

test_a_record_that_names_the_pool_as_name_passes() {
  case_dir record_names_the_pool
  # The run record carries a stage's source as "kind and name, never a value"
  # (C1), so a record that holds the pool's name under `name` is the same
  # reading as one that holds it under `pool`.
  printf '{"version":1,"run_id":"%s","stages":[{"stage":"implement","source":{"kind":"launcher-pool","name":"team-a"}}]}\n' \
    "$RESUMED" >"$answers/record-$RESUMED.json"
  run883
  expect_status "$status" 0 "a record that names the pool as name"
  expect_row "$out" pool PASS "a record that names the pool as name"
}

test_a_record_whose_kind_is_not_the_launchers_leaves_the_row_unverified() {
  case_dir record_other_kind
  # The pool is the one the launch named, but the record calls the kind the
  # plain `pool` word: whether that pool is the launcher's own is not something
  # the record says, so the row reads it rather than calling it either way.
  printf '{"version":1,"run_id":"%s","stages":[{"stage":"implement","source":{"kind":"pool","pool":"team-a"}}]}\n' \
    "$RESUMED" >"$answers/record-$RESUMED.json"
  run883
  expect_status "$status" 3 "a record whose kind is not the launcher's"
  expect_row "$out" pool UNVERIFIED "a record whose kind is not the launcher's"
  expect_contains "$out" "calls the kind pool" "a record whose kind is not the launcher's"
}

test_a_record_that_cannot_be_read_leaves_the_row_unverified() {
  case_dir record_unreadable
  printf 1 >"$answers/record-status"
  run883
  expect_status "$status" 3 "a record that cannot be read"
  expect_row "$out" pool UNVERIFIED "a record that cannot be read"
  expect_contains "$out" "could not be read" "a record that cannot be read"
}

test_a_doctor_that_exits_one_with_its_document_is_still_read() {
  case_dir doctor_not_ok
  # doctor writes its whole document and then exits 1 whenever any of its checks
  # failed (emitDoctor's ExitError for !o.OK): the document is the answer to
  # which installation this is, and the status is another reading this check
  # does not make. Reading the status as no answer would call an installation
  # with any unrelated problem an installation this check cannot read.
  printf 1 >"$answers/doctor-status"
  run883
  expect_status "$status" 0 "a doctor that exits 1 with its document"
  expect_row "$out" premise PASS "a doctor that exits 1 with its document"
  expect_not_contains "$out" "did not answer with a document" "a doctor that exits 1 with its document"
}

test_a_doctor_that_answers_no_document_stops_the_check() {
  case_dir doctor_no_document
  : >"$answers/doctor.json"
  printf 1 >"$answers/doctor-status"
  run883
  expect_status "$status" 2 "a doctor that answers no document"
  expect_row "$out" premise FAIL "a doctor that answers no document"
  expect_contains "$out" "did not answer with a document this check reads" "a doctor that answers no document"
  case "$(cat "$CALLS" 2>/dev/null)" in
  *"fugaro run"*) fail "a doctor that answers no document: it launched a run anyway" ;;
  esac
}

test_the_installation_flag_names_the_project_every_call_is_about() {
  case_dir installation_named
  run883 --installation test
  expect_status "$status" 0 "an installation that is named"
  expect_row "$out" premise PASS "an installation that is named"
  expect_contains "$(cat "$CALLS")" "fugaro doctor --json --project test" "an installation that is named: doctor was asked for it"
  expect_contains "$(cat "$CALLS")" "fugaro run --repo acme/sub-app --pool team-a --task-file" "an installation that is named: the launch"
  expect_contains "$(cat "$CALLS")" "--project test --json" "an installation that is named: the launch and every listing name it"
  expect_contains "$(cat "$CALLS")" "fugaro runs ls --repo acme/sub-app --project test --json" "an installation that is named: the listings name it"
  expect_contains "$(cat "$CALLS")" "fugaro project policy show --json --project test" "an installation that is named: the policy is read for it"
}

test_an_installation_flag_whose_project_is_another_stops_the_check() {
  case_dir installation_other_project
  # doctor answers about the project it resolved: a check pointed at one
  # installation that reads another installation's policy, pools and runs would
  # measure the wrong thing, and launch a real run of the wrong project.
  python3 - "$answers/doctor.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["project"]["name"] = "other"
json.dump(doc, open(sys.argv[1], "w"))
PY
  run883 --installation test
  expect_status "$status" 2 "an installation flag whose project is another"
  expect_row "$out" premise FAIL "an installation flag whose project is another"
  expect_contains "$out" "answered about the project 'other'" "an installation flag whose project is another"
  case "$(cat "$CALLS" 2>/dev/null)" in
  *"fugaro run"*) fail "an installation flag whose project is another: it launched a run anyway" ;;
  esac
}

test_a_resumed_row_that_points_back_at_the_pause_is_read() {
  case_dir resumed_row_placement
  # The design fixes the member's place (the paused row) and not its name, so a
  # listing that links the two from the OTHER side — a row whose own run is the
  # resumed one and whose `previous_run` is the pause — is the same reading.
  printf '{"runs":[%s]}\n' "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" >"$answers/runs-2.json"
  printf '{"runs":[%s,{"run":"%s/%s","repo":"acme/sub-app","run_id":"%s","status":"running","previous_run":"%s"}]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" "$SLUG" "$RESUMED" "$RESUMED" "$LAUNCHED" \
    >"$answers/runs-3.json"
  run883
  expect_status "$status" 0 "a resumed row that points back at the pause"
  expect_row "$out" resume PASS "a resumed row that points back at the pause"
  expect_row "$out" pool PASS "a resumed row that points back at the pause"
  expect_contains "$out" "run=$RESUMED" "a resumed row that points back at the pause: the row's own run is the resumer"
}

test_a_follow_up_runs_previous_run_is_not_the_resumer() {
  case_dir unrelated_previous_run
  # A row for another run of the same repository carries `previous_run` naming a
  # THIRD run: it is that run's own continuation and says nothing about this
  # pause. Reading it as the resumer would PASS the resume and then read the
  # wrong run's record for the pool, without the paused row ever being read.
  OTHER=run-cccc3333
  EARLIER=run-dddd4444
  printf '{"runs":[%s]}\n' "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" >"$answers/runs-2.json"
  printf '{"runs":[%s,{"run":"%s/%s","repo":"acme/sub-app","run_id":"%s","status":"failed","previous_run":"%s"}]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" "$SLUG" "$OTHER" "$OTHER" "$EARLIER" \
    >"$answers/runs-3.json"
  run883
  expect_status "$status" 3 "a follow-up run's previous_run"
  expect_row "$out" resume UNVERIFIED "a follow-up run's previous_run"
  expect_row "$out" pool UNVERIFIED "a follow-up run's previous_run"
  expect_contains "$out" "names the run that resumed ${LAUNCHED}" "a follow-up run's previous_run: the row says what it was waiting for"
}

test_a_paused_rows_own_previous_run_is_not_the_resumer() {
  case_dir paused_row_previous_run
  # On the PAUSED run's own row, `previous_run` names the run the paused run
  # itself continued — the opposite direction — so it is not the run that
  # resumed it either.
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z","previous_run":"run-dddd4444"')" >"$answers/runs-2.json"
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z","previous_run":"run-dddd4444"')" >"$answers/runs-3.json"
  run883
  expect_status "$status" 3 "a paused row's own previous_run"
  expect_row "$out" pause PASS "a paused row's own previous_run: the pause was still read"
  expect_row "$out" resume UNVERIFIED "a paused row's own previous_run"
  expect_row "$out" pool UNVERIFIED "a paused row's own previous_run"
}

test_a_paused_rows_resume_time_is_not_read_as_the_resumer() {
  case_dir paused_row_resume_time
  # The paused row carries the resume TIME beside the run that resumed it, and
  # the plan's own name for the time is `resume_at` (C3, the exhausted answer's
  # wire member) — a name mentioning resuming. A row that has the time and no
  # resumer yet, or a listing that spells the resumer otherwise, must not read
  # the timestamp as the run that resumed the pause: doing so would PASS the
  # resume row naming a clock reading and send the pool row after a record that
  # cannot exist.
  printf '{"runs":[%s]}\n' "$(row paused ',"resume_at":"2026-11-22T06:00:00Z"')" >"$answers/runs-2.json"
  printf '{"runs":[%s]}\n' "$(row paused ',"resume_at":"2026-11-22T06:00:00Z"')" >"$answers/runs-3.json"
  run883
  expect_status "$status" 3 "a paused row's resume time"
  expect_row "$out" pause PASS "a paused row's resume time: the pause was still read"
  expect_row "$out" resume UNVERIFIED "a paused row's resume time"
  expect_row "$out" pool UNVERIFIED "a paused row's resume time"
  # No record is looked for under a run id that is a clock reading: the pool row
  # read nothing at all, rather than chasing the timestamp as if it were a run.
  expect_not_contains "$(cat "$GCLOUD_CALLS")" "2026-11-22T06:00:00Z" \
    "a paused row's resume time: the timestamp is never named as a run"
}

test_the_resume_time_before_the_resumer_still_names_the_resumer() {
  case_dir resume_time_first
  # The same row written the other way round: the resume time first, the run
  # that resumed it after. The reading is the member, not the order it is
  # written in, so the resumer is read and the pool row reads ITS record.
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"resume_at":"2026-11-22T06:00:00Z","resumed_by":"'"$RESUMED"'"')" >"$answers/runs-2.json"
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"resume_at":"2026-11-22T06:00:00Z","resumed_by":"'"$RESUMED"'"')" >"$answers/runs-3.json"
  run883
  expect_status "$status" 0 "the resume time before the resumer"
  expect_row "$out" resume PASS "the resume time before the resumer"
  expect_row "$out" pool PASS "the resume time before the resumer"
  expect_contains "$out" "resumed_by=$RESUMED" "the resume time before the resumer: the run that resumed it, not the time"
  expect_contains "$(cat "$GCLOUD_CALLS")" "gcloud storage cat gs://$BUCKET/runs/$SLUG/$RESUMED/result.json" \
    "the resume time before the resumer: the pool row read the resumed run's record"
}

test_a_resumed_rows_resumes_member_is_read() {
  case_dir resumed_row_resumes
  # The plan names the back-link `resumes`: a run reissued over a pause carries
  # it, naming the paused run (C3's task member; PR4's reissue, VS3's reader).
  # It contains "resum", so it also has to be read as what it is — the resumed
  # row pointing back — and not skipped for its name.
  printf '{"runs":[%s]}\n' "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" >"$answers/runs-2.json"
  printf '{"runs":[%s,{"run":"%s/%s","repo":"acme/sub-app","run_id":"%s","status":"running","resumes":"%s"}]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z"')" "$SLUG" "$RESUMED" "$RESUMED" "$LAUNCHED" \
    >"$answers/runs-3.json"
  run883
  expect_status "$status" 0 "a resumed row's resumes member"
  expect_row "$out" resume PASS "a resumed row's resumes member"
  expect_row "$out" pool PASS "a resumed row's resumes member"
  expect_contains "$out" "run=$RESUMED" "a resumed row's resumes member: that row's own run is the resumer"
}

test_a_paused_rows_own_resumes_member_is_not_the_resumer() {
  case_dir paused_row_resumes
  # `resumes` names the run a row RESUMES, not one that resumed it, so on the
  # PAUSED row it points the other way — the paused run's own predecessor in the
  # chain — and is not the run that resumed this pause.
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z","resumes":"run-dddd4444"')" >"$answers/runs-2.json"
  printf '{"runs":[%s]}\n' \
    "$(row paused ',"paused_until":"2026-11-22T06:00:00Z","resumes":"run-dddd4444"')" >"$answers/runs-3.json"
  run883
  expect_status "$status" 3 "a paused row's own resumes member"
  expect_row "$out" resume UNVERIFIED "a paused row's own resumes member"
  expect_row "$out" pool UNVERIFIED "a paused row's own resumes member"
}

test_a_pool_that_is_not_the_launchers_is_refused_before_anything_runs() {
  case_dir bad_pool_name
  run883_raw --repo acme/sub-app --pool 'team a' --dry-run
  expect_status "$status" 2 "a pool name that is not a name"
  expect_contains "$err" "not a pool name" "a pool name that is not a name"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run883_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK883_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK883_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help
t test_a_dry_run_calls_nothing
t test_a_missing_repository_is_refused
t test_a_missing_pool_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_an_installation_with_no_resident_orchestrator_stops_the_check
t test_a_doctor_that_exits_one_with_its_document_is_still_read
t test_a_doctor_that_answers_no_document_stops_the_check
t test_the_installation_flag_names_the_project_every_call_is_about
t test_an_installation_flag_whose_project_is_another_stops_the_check
t test_a_policy_without_pause_settings_stops_the_check
t test_a_policy_read_that_does_not_answer_stops_the_check
t test_a_pool_the_installation_does_not_define_stops_the_check
t test_a_launch_that_is_refused_stops_the_check
t test_a_run_that_ends_without_pausing_leaves_the_row_unverified
t test_a_pause_nobody_resumes_leaves_the_row_unverified
t test_a_resumed_record_without_a_source_leaves_the_row_unverified
t test_a_resumed_run_on_another_pool_is_a_finding
t test_a_record_that_names_the_pool_as_name_passes
t test_a_record_whose_kind_is_not_the_launchers_leaves_the_row_unverified
t test_a_record_that_cannot_be_read_leaves_the_row_unverified
t test_a_resumed_row_that_points_back_at_the_pause_is_read
t test_a_follow_up_runs_previous_run_is_not_the_resumer
t test_a_paused_rows_own_previous_run_is_not_the_resumer
t test_a_paused_rows_resume_time_is_not_read_as_the_resumer
t test_the_resume_time_before_the_resumer_still_names_the_resumer
t test_a_resumed_rows_resumes_member_is_read
t test_a_paused_rows_own_resumes_member_is_not_the_resumer
t test_a_pool_that_is_not_the_launchers_is_refused_before_anything_runs
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-883-pause-resume_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-883-pause-resume_test.sh: ok"
