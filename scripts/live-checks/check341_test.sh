#!/usr/bin/env bash
# Tests for scripts/live-checks/check341.sh, with fake `fugaro`, `gcloud` and
# `git` on PATH: NOTHING here reaches a cloud, a bucket, Terraform or a Fugaro
# installation.
#
# The fakes are STRICT: the fake `fugaro` validates every command and flag
# against the real CLI's own surface (lib/strict.sh over lib/fugaro-surface.tsv,
# generated from cli.NewRootCmd by scripts/live_check_surface_test.go), and the
# fake gcloud refuses a subcommand or flag its allow-list does not name
# (lib/allow/check341.gcloud). The gcloud fake also REFUSES to answer the
# scheduler, the job or the bucket except through the commands the check really
# makes, so a check that read the schedule with the wrong command fails HERE
# rather than on your terminal.
#
# Usage: bash scripts/live-checks/check341_test.sh
#        CHECK_TEST_ONLY=test_the_whole_check_passes bash scripts/live-checks/check341_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check341.sh
export LIVE_CHECK_LIB=$here/lib
# shellcheck source=lib/strict.sh
. "$LIVE_CHECK_LIB/strict.sh"

work=$(mktemp -d)
state=$work/state
cleanup_all() {
  if [ -n "${CHECK341_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() {
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() {
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; block: $(printf '%s' "${out:-}" | head -c 800) stderr: $(printf '%s' "${err:-}" | head -c 500)"
}

# --------------------------------------------------------------------- fakes
mkdir -p "$work/bin" "$work/home" "$work/tmp"
chmod 700 "$work/tmp"

cat >"$work/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check341.sh's test only. It
# validates its argv against the REAL CLI's surface first, then answers out of
# the case's own state directory: the project layer's jobs.reports.paused is one
# file this fake reads and writes, so the write the check makes is visible in
# what it reads next.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s\n' "$*" >>"${FAKE341_STATE:?}/calls"
setting_doc() {
  python3 - "$FAKE341_STATE" <<'PY'
import json,os,sys
st=sys.argv[1]
raw=open(os.path.join(st,"paused")).read().strip()
val="true" if raw in ("","default","true") else raw
wins="default" if raw in ("","default") else "project"
print(json.dumps({"version":1,"project":"fugaro","generated_at":"2026-11-24T00:00:00Z","sources":[],
                  "settings":[{"key":"layer.jobs.reports.paused","scope":"project","value":val,"wins":wins,
                               "default":{"value":"true"},"state":"ok","writable":True,"owner":"settings set"}]}))
PY
}
case "${1:-} ${2:-}" in
"doctor --json") cat "$FAKE341_STATE/doctor.json" ;;
"settings get")
  [ -e "$FAKE341_STATE/get.fail" ] && { echo "fugaro: the settings read could not answer" >&2; exit 1; }
  setting_doc
  ;;
"settings set")
  [ -e "$FAKE341_STATE/set.fail" ] && { echo "fugaro: the write to the project layer was refused" >&2; exit 1; }
  case "$*" in
  *--dry-run*)
    printf '{"version":1,"key":"%s","layer":"project","path":"%s/layer.yaml","changed":true}\n' "$3" "$FAKE341_STATE"
    ;;
  *) printf '%s\n' "$4" >"$FAKE341_STATE/paused" ;;
  esac
  ;;
"settings unset") printf 'default\n' >"$FAKE341_STATE/paused" ;;
"update" | "update ")
  [ -e "$FAKE341_STATE/update.fail" ] && { echo "fugaro: apply failed: the plan could not be read" >&2; exit 1; }
  cat "$FAKE341_STATE/update.log"
  ;;
"runs digest")
  [ -e "$FAKE341_STATE/stored.fail" ] && { echo "fugaro: no stored report reads back for this key" >&2; exit 1; }
  cat "$FAKE341_STATE/stored.md"
  ;;
*)
  echo "fugaro: unexpected read: $*" >&2
  exit 1
  ;;
esac
FUGARO_EOF
chmod +x "$work/bin/fugaro"

cat >"$work/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check341.sh's test only: it
# refuses an unknown subcommand or flag through the strict allow-list, and
# otherwise answers the reads the check makes out of the case's state.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_gcloud_verify "${FAKE341_ALLOW:?}/check341.gcloud" "$@" || exit $?
printf 'gcloud %s\n' "$*" >>"${FAKE341_STATE:?}/calls"
scheduler_doc() {
  python3 - "$FAKE341_STATE" <<'PY'
import json,os,sys
st=sys.argv[1]
raw=open(os.path.join(st,"paused")).read().strip()
state="ENABLED" if raw=="false" else "PAUSED"
p=os.path.join(st,"scheduler.state")
if os.path.exists(p): state=open(p).read().strip()
job="another-job" if os.path.exists(os.path.join(st,"scheduler.other")) else "fugarorpt-reports"
print(json.dumps({"name":"projects/fugaro-dev/locations/us-east4/jobs/fugaro-reports",
                  "state":state,"schedule":"37 5 * * *","timeZone":"Etc/UTC",
                  "httpTarget":{"uri":"https://run.googleapis.com/v2/projects/fugaro-dev/locations/us-east5/jobs/%s:run"%job,
                                "oauthToken":{"serviceAccountEmail":"fugaro-scheduler@fugaro-dev.iam.gserviceaccount.com"}}}))
PY
}
case "${1:-}" in
scheduler)
  [ -e "$FAKE341_STATE/scheduler.fail" ] && { echo "gcloud: NOT_FOUND: job not found" >&2; exit 1; }
  scheduler_doc
  ;;
run)
  case "${2:-} ${3:-}" in
  "jobs describe")
    [ -e "$FAKE341_STATE/job.missing" ] && { echo "gcloud: NOT_FOUND: the job was not found" >&2; exit 1; }
    cat "$FAKE341_STATE/job.json"
    ;;
  "jobs get-iam-policy") cat "$FAKE341_STATE/jobiam.json" ;;
  "jobs executions")
    [ "${4:-}" = list ] || { echo "gcloud: unexpected read: $*" >&2; exit 1; }
    n=$(( $(cat "$FAKE341_STATE/exec-calls" 2>/dev/null || echo 0) + 1 ))
    printf '%s\n' "$n" >"$FAKE341_STATE/exec-calls"
    [ -e "$FAKE341_STATE/executions.fail" ] && { echo "gcloud: the executions could not be listed" >&2; exit 1; }
    if [ "$n" -le "${FAKE341_FIRE_AFTER:-1}" ]; then cat "$FAKE341_STATE/executions-empty.json"; else cat "$FAKE341_STATE/executions.json"; fi
    ;;
  *) echo "gcloud: unexpected read: $*" >&2; exit 1 ;;
  esac
  ;;
storage)
  case "${2:-}" in
  ls)
    [ -e "$FAKE341_STATE/store.fail" ] && { echo "gcloud: the listing was refused" >&2; exit 1; }
    cat "$FAKE341_STATE/store.out"
    ;;
  buckets)
    case "${3:-}" in
    describe) cat "$FAKE341_STATE/bucket.json" ;;
    get-iam-policy)
      [ -e "$FAKE341_STATE/bucketiam.fail" ] && { echo "gcloud: the IAM policy was refused" >&2; exit 1; }
      cat "$FAKE341_STATE/bucketiam.json"
      ;;
    *) echo "gcloud: unexpected read: $*" >&2; exit 1 ;;
    esac
    ;;
  *) echo "gcloud: unexpected read: $*" >&2; exit 1 ;;
  esac
  ;;
*)
  echo "gcloud: unexpected read: $*" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
chmod +x "$work/bin/gcloud"

cat >"$work/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check341.sh's test only.
set -euo pipefail
printf 'git %s\n' "$*" >>"${FAKE341_STATE:?}/calls"
case "${1:-}" in
remote) cat "$FAKE341_STATE/origin" ;;
*) exit 1 ;;
esac
GIT_EOF
chmod +x "$work/bin/git"

# ------------------------------------------------------------------- fixtures
REPO=acme/sandbox
RUN=20261124-101010-aaaa

reset_state() {
  rm -rf "$state"
  mkdir -p "$state" "$work/tmp"
  : >"$state/calls"
  echo "git@github.com:acme/sandbox.git" >"$state/origin"
  printf 'true\n' >"$state/paused"
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  cat >"$state/update.log" <<'EOF'
fugaro: applying the installation
the reports job fugarorpt-reports is planned with its account and schedule
EOF
  cat >"$state/stored.md" <<'EOF'
# 2026-11-23

## Summary

| Runs | Failures |
| --- | --- |
| 3 | 1 |

## Failures

| Class | Runs |
| --- | --- |
| `tests_failed` | 1 |
EOF
  store_doc acme-sandbox/2026/2026-11-23.md 2026/2026-11-23.md acme-web/2026/2026-11-23.md
  bucket_doc 90 548
  bucketiam_doc fugaro-reports
  jobiam_doc yes
  job_doc fugaro-dev fugaro-runs-fugaro-dev acme-sandbox acme-web
  echo '[]' >"$state/executions.json"
  executions_doc 2027-01-01T05:37:00Z CONDITION_SUCCEEDED 2027-01-01T05:41:00Z
  echo '[]' >"$state/executions-empty.json"
}

job_doc() { # job_doc PROJECT BUCKET SLUG...
  python3 - "$state" "$@" <<'PY'
import json,os,sys
st,project,bucket=sys.argv[1],sys.argv[2],sys.argv[3]
repos=sys.argv[4:]
doc={"version":1,"project":project,"bucket":bucket,"repositories":repos,
     "periods":["day","week","month","year"]}
job={"template":{"template":{"serviceAccount":"fugaro-reports@%s.iam.gserviceaccount.com"%project,
                            "timeout":"1800s",
                            "containers":[{"args":["runs","reports","--job"],
                                           "env":[{"name":"FUGARO_REPORTS_JOB_SPEC","value":json.dumps(doc)}]}]}}}
json.dump(job,open(os.path.join(st,"job.json"),"w"))
PY
}

store_doc() { # store_doc NAME...
  : >"$state/store.out"
  local n
  for n in "$@"; do
    printf 'gs://fugaro-runs-fugaro-dev/reports/%s\n' "$n" >>"$state/store.out"
  done
}

bucket_doc() { # bucket_doc RUNS_DAYS REPORTS_DAYS
  python3 - "$state" "$1" "$2" <<'PY'
import json,os,sys
st,runs,reports=sys.argv[1],sys.argv[2],sys.argv[3]
rules=[]
if runs!="-": rules.append({"action":{"type":"Delete"},"condition":{"age":int(runs),"matchesPrefix":["runs/"]}})
if reports!="-": rules.append({"action":{"type":"Delete"},"condition":{"age":int(reports),"matchesPrefix":["reports/"]}})
json.dump({"name":"fugaro-runs-fugaro-dev","lifecycle":{"rule":rules}},open(os.path.join(st,"bucket.json"),"w"))
PY
}

bucketiam_doc() { # bucketiam_doc SCHEMELESS_ACCOUNT_ID | OTHER
  python3 - "$state" "$1" <<'PY'
import json,os,sys
st,who=sys.argv[1],sys.argv[2]
member="serviceAccount:%s@fugaro-dev.iam.gserviceaccount.com"%who
expr='resource.name.startsWith("projects/_/buckets/fugaro-runs-fugaro-dev/objects/reports/") || api.getAttribute(\'storage.googleapis.com/objectListPrefix\', \'\').startsWith("reports/")'
bindings=[{"role":"roles/storage.objectViewer","members":["user:owner@example.com"]},
          {"role":"roles/storage.objectUser","members":[member],
           "condition":{"title":"fugaro-reports-write-fugaro-reports","expression":expr}}]
json.dump({"bindings":bindings},open(os.path.join(st,"bucketiam.json"),"w"))
PY
}

jobiam_doc() { # jobiam_doc yes|no
  python3 - "$state" "$1" <<'PY'
import json,os,sys
st,want=sys.argv[1],sys.argv[2]
members=["serviceAccount:fugaro-scheduler@fugaro-dev.iam.gserviceaccount.com"] if want=="yes" else ["serviceAccount:someone-else@fugaro-dev.iam.gserviceaccount.com"]
json.dump({"bindings":[{"role":"roles/run.invoker","members":members}]},open(os.path.join(st,"jobiam.json"),"w"))
PY
}

executions_doc() { # executions_doc START STATE [COMPLETION]
  python3 - "$state" "$@" <<'PY'
import json,os,sys
st,start,cond=sys.argv[1],sys.argv[2],sys.argv[3]
done=sys.argv[4] if len(sys.argv)>4 else ""
e={"name":"projects/fugaro-dev/locations/us-east5/jobs/fugarorpt-reports/executions/fugarorpt-reports-abc12",
   "startTime":start,"status":{"conditions":[{"type":"Completed","state":cond}]}}
if done: e["completionTime"]=done
json.dump([e],open(os.path.join(st,"executions.json"),"w"))
PY
}

run_check() { # run_check [args...] -> sets $out $status $err
  local name=$$
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" HOME="$work/home" TMPDIR="$work/tmp" \
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE341_STATE="$state" FAKE341_ALLOW="$LIVE_CHECK_LIB/allow" \
    CHECK341_WAIT_MINUTES=1 CHECK341_POLL_SECONDS=0 FAKE341_FIRE_AFTER=0 \
    ${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/stderr.$name")
  status=$?
  set -e
  err=$(cat "$work/stderr.$name")
}

calls() { cat "$state/calls" 2>/dev/null || true; }

# ---------------------------------------------------------------------- cases
test_the_help_lists_the_one_command() {
  reset_state
  run_check --help
  expect_status "$status" 0 "--help exits 0"
  expect_contains "$out" "bash scripts/live-checks/check341.sh" "--help prints the usage line"
  expect_contains "$out" "jobs.reports.paused: false" "--help names the one setting"
  expect_contains "$out" "CHECK 341 RESULT" "--help's header names the block"
}

test_refuses_a_coding_agents_session() {
  reset_state
  EXTRA_ENV=(CLAUDECODE=1)
  run_check
  unset EXTRA_ENV
  expect_status "$status" 2 "an agent session is refused with a stop"
  expect_contains "$err" "coding agent's session" "the refusal names what it is"
  expect_absent "$(calls)" "settings set" "nothing was written"
  expect_absent "$(calls)" "fugaro update" "nothing was updated"
}

test_refuses_belong() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"belong-prod","region":"us-east5"}}
EOF
  run_check
  expect_status "$status" 2 "belong is refused"
  expect_contains "$err" "never runs against belong" "the refusal names belong"
  expect_absent "$(calls)" "settings set" "nothing was written"
}

test_refuses_another_installation() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"scratch","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  run_check
  expect_status "$status" 2 "another installation is refused"
  expect_contains "$err" "runs on fugaro only" "the refusal names the sandbox installation"
}

test_stops_on_a_key_this_cli_does_not_write() {
  reset_state
  run_check --key 2026-13-99
  expect_status "$status" 2 "a period key that is not one is a stop"
  expect_contains "$err" "not a period key" "the stop says so"
  expect_absent "$(calls)" "settings set" "nothing was written"
}

test_the_whole_check_passes() {
  reset_state
  run_check
  expect_status "$status" 0 "every criterion holds"
  expect_contains "$out" "CHECK 341 RESULT: PASS (exit 0)" "the block's verdict"
  expect_contains "$out" "FACT setting: PASS" "the setting is read before the write"
  expect_contains "$out" "FACT power: PASS" "the layer is written"
  expect_contains "$out" "FACT update: PASS" "the update ran"
  expect_contains "$out" "FACT scheduler: PASS" "the schedule reads ENABLED and fires the job"
  expect_contains "$out" "FACT job: PASS" "the job runs 'runs reports --job' as its own account"
  expect_contains "$out" "FACT assignment: PASS" "the job's own assignment reads"
  expect_contains "$out" "FACT grants: PASS" "the reports account is reports/'s only writer"
  expect_contains "$out" "FACT invoker: PASS" "the scheduler account may start the job"
  expect_contains "$out" "FACT fired: PASS" "an execution started after the setting went on"
  expect_contains "$out" "FACT store: PASS" "one report per repository, with the aggregate beside it"
  expect_contains "$out" "FACT stored: PASS" "the stored report reads back"
  expect_contains "$out" "FACT retention: PASS" "reports/ outlives the run records"
  expect_contains "$out" "FACT restore: PASS" "the setting is back and the schedule reads PAUSED"
  expect_absent "$out" "not reached" "every row was reached"
  [ "$(grep -c '^fugaro update$' <<<"$(calls)")" = 2 ] ||
    fail "the check runs fugaro update twice (start and restore); calls: $(calls)"
}

test_it_requires_a_firing_after_its_own_update() {
  reset_state
  executions_doc 2020-01-01T05:37:00Z CONDITION_SUCCEEDED 2020-01-01T05:41:00Z
  EXTRA_ENV=(FAKE341_FIRE_AFTER=999 CHECK341_WAIT_MINUTES=0)
  run_check
  unset EXTRA_ENV
  expect_status "$status" 3 "a firing from before the check is not the check's firing"
  expect_contains "$out" "FACT fired: UNVERIFIED" "the row says no firing was seen"
  expect_contains "$out" "scheduler jobs run" "with how to fire it now"
}

test_the_wait_retries_and_finds_the_firing() {
  reset_state
  EXTRA_ENV=(FAKE341_FIRE_AFTER=1 CHECK341_WAIT_MINUTES=1 CHECK341_POLL_SECONDS=0)
  run_check
  unset EXTRA_ENV
  expect_status "$status" 0 "the second read finds it"
  expect_contains "$out" "FACT fired: PASS" "the row passes"
  [ "$(grep -c 'executions list' <<<"$(calls)")" -ge 2 ] ||
    fail "the wait polls until the firing appears; calls: $(calls)"
}

test_the_executions_read_refusing_is_unverified() {
  reset_state
  : >"$state/executions.fail"
  run_check
  expect_status "$status" 3 "a read that did not answer is UNVERIFIED"
  expect_contains "$out" "FACT fired: UNVERIFIED" "the row says so"
  expect_contains "$out" "did not read back" "and says what happened"
}

test_a_failed_execution_is_a_finding() {
  reset_state
  executions_doc 2027-01-01T05:37:00Z CONDITION_FAILED 2027-01-01T05:39:00Z
  run_check
  expect_status "$status" 1 "a firing that failed stored nothing"
  expect_contains "$out" "FACT fired: FAIL" "the row fails"
  expect_contains "$out" "CONDITION_FAILED" "and names the state it read"
}

test_the_schedule_still_paused_after_the_update_is_a_finding() {
  reset_state
  printf 'PAUSED\n' >"$state/scheduler.state"
  run_check
  expect_status "$status" 1 "the setting did not reach the schedule"
  expect_contains "$out" "FACT scheduler: FAIL" "the row fails"
  expect_contains "$out" "still reads PAUSED" "and says why"
}

test_the_schedule_pointing_at_another_job_is_a_finding() {
  reset_state
  : >"$state/scheduler.other"
  run_check
  expect_status "$status" 1 "a schedule that fires another job is a finding"
  expect_contains "$out" "FACT scheduler: FAIL" "the row fails"
  expect_contains "$out" "another-job" "and names what it fires"
}

test_a_missing_schedule_is_unverified_with_the_region_flag() {
  reset_state
  : >"$state/scheduler.fail"
  run_check
  expect_status "$status" 3 "a schedule that cannot be read is UNVERIFIED"
  expect_contains "$out" "FACT scheduler: UNVERIFIED" "the row says so"
  expect_contains "$out" "--scheduler-region" "with the flag that resolves it"
}

test_a_job_that_is_not_deployed_is_a_finding() {
  reset_state
  : >"$state/job.missing"
  run_check
  expect_status "$status" 1 "no reports job means nothing fires"
  expect_contains "$out" "FACT job: FAIL" "the row fails"
  expect_contains "$out" "is not on this installation" "and says so"
  expect_contains "$out" "FACT assignment: FAIL" "and the assignment cannot be read either"
}

test_a_job_with_another_argv_is_a_finding() {
  reset_state
  job_doc fugaro-dev fugaro-runs-fugaro-dev acme-sandbox acme-web
  python3 - "$state" <<'PY'
import json,os,sys
st=sys.argv[1]
d=json.load(open(os.path.join(st,"job.json")))
d["template"]["template"]["containers"][0]["args"]=["runs","reports"]
json.dump(d,open(os.path.join(st,"job.json"),"w"))
PY
  run_check
  expect_status "$status" 1 "the job's argv is R11's"
  expect_contains "$out" "FACT job: FAIL" "the row fails"
  expect_contains "$out" "runs reports --job" "and names the argv it wants"
}

test_an_assignment_writing_another_bucket_is_a_finding() {
  reset_state
  job_doc fugaro-dev someone-elses-bucket acme-sandbox acme-web
  run_check
  expect_status "$status" 1 "a job writing another bucket is never read by this check"
  expect_contains "$out" "FACT assignment: FAIL" "the row fails"
  expect_contains "$out" "someone-elses-bucket" "and names it"
}

test_a_repository_with_no_stored_report_is_a_finding() {
  reset_state
  store_doc acme-sandbox/2026/2026-11-23.md 2026/2026-11-23.md
  run_check
  expect_status "$status" 1 "one report per repository the job's assignment names"
  expect_contains "$out" "FACT store: FAIL" "the row fails"
  expect_contains "$out" "acme-web" "and names the repository"
}

test_a_key_with_no_aggregate_beside_it_is_a_finding() {
  reset_state
  store_doc acme-sandbox/2026/2026-11-23.md acme-web/2026/2026-11-23.md
  run_check
  expect_status "$status" 1 "the project aggregate is written beside them"
  expect_contains "$out" "FACT store: FAIL" "the row fails"
  expect_contains "$out" "aggregate beside" "and says what is missing"
}

test_a_firing_that_stored_nothing_is_a_finding() {
  reset_state
  : >"$state/store.out"
  run_check
  expect_status "$status" 1 "an empty prefix after a firing is the finding"
  expect_contains "$out" "FACT store: FAIL" "the row fails"
  expect_contains "$out" "stored nothing" "and says so"
}

test_a_store_that_does_not_list_is_unverified() {
  reset_state
  : >"$state/store.fail"
  run_check
  expect_status "$status" 3 "a prefix that cannot be listed is UNVERIFIED"
  expect_contains "$out" "FACT store: UNVERIFIED" "the row says so"
}

test_a_report_that_does_not_read_back_is_a_finding() {
  reset_state
  : >"$state/stored.fail"
  run_check
  expect_status "$status" 1 "the read-back is the check's own question"
  expect_contains "$out" "FACT stored: FAIL" "the row fails"
  expect_contains "$out" "Open item 5" "and names the question it answers"
}

test_an_empty_read_back_is_a_finding() {
  reset_state
  : >"$state/stored.md"
  run_check
  expect_status "$status" 1 "an empty report is not the report the job wrote"
  expect_contains "$out" "FACT stored: FAIL" "the row fails"
}

test_no_retention_rule_under_reports_is_a_finding() {
  reset_state
  bucket_doc 90 -
  run_check
  expect_status "$status" 1 "reports/ must be reaped by a rule of its own"
  expect_contains "$out" "FACT retention: FAIL" "the row fails"
  expect_contains "$out" "deletes nothing under reports/" "and says so"
}

test_a_reports_rule_that_does_not_outlive_the_records_is_a_finding() {
  reset_state
  bucket_doc 90 90
  run_check
  expect_status "$status" 1 "a report deleted with its records is not stored"
  expect_contains "$out" "FACT retention: FAIL" "the row fails"
  expect_contains "$out" "not after the run records" "and says why"
}

test_a_second_writer_of_reports_is_a_finding() {
  reset_state
  bucketiam_doc someone-else
  run_check
  expect_status "$status" 1 "E4 makes the job the prefix's only writer"
  expect_contains "$out" "FACT grants: FAIL" "the row fails"
  expect_contains "$out" "someone-else" "and names the other writer"
}

test_a_missing_invoker_grant_is_a_finding() {
  reset_state
  jobiam_doc no
  run_check
  expect_status "$status" 1 "a firing the scheduler may not start never happens"
  expect_contains "$out" "FACT invoker: FAIL" "the row fails"
  expect_contains "$out" "run.invoker" "and names the role"
}

test_a_write_that_does_not_land_is_a_finding() {
  reset_state
  : >"$state/set.fail"
  run_check
  expect_status "$status" 1 "a layer write that did not land starts nothing"
  expect_contains "$out" "FACT power: FAIL" "the row fails"
  expect_contains "$out" "did not land" "and says so"
  expect_contains "$out" "FACT fired: UNVERIFIED" "and no firing is claimed"
}

test_an_update_that_does_not_apply_is_a_finding() {
  reset_state
  : >"$state/update.fail"
  run_check
  expect_status "$status" 1 "the update is what renders the setting into the plan"
  expect_contains "$out" "FACT update: FAIL" "the row fails"
  expect_contains "$out" "FACT fired: UNVERIFIED" "and no firing is claimed"
}

test_a_setting_that_cannot_be_read_is_a_stop() {
  reset_state
  : >"$state/get.fail"
  run_check
  expect_status "$status" 2 "the value the check puts back has to be readable first"
  expect_contains "$err" "did not answer" "the stop says so"
  expect_absent "$(calls)" "settings set" "nothing was written"
}

test_the_setting_the_layer_did_not_carry_is_unset_again() {
  reset_state
  printf 'default\n' >"$state/paused"
  run_check
  expect_status "$status" 0 "an unset key restored to unset is a pass"
  expect_contains "$out" "FACT restore: PASS" "the row passes"
  expect_contains "$(calls)" "settings unset layer.jobs.reports.paused --layer project" "the key is removed, not written out"
}

test_a_schedule_that_stays_on_after_the_restore_is_a_finding() {
  reset_state
  # a schedule that answers ENABLED however the layer reads is what a restore
  # that never reached the plan looks like
  printf 'ENABLED\n' >"$state/scheduler.state"
  run_check
  expect_status "$status" 1 "a check that left the job firing fails"
  expect_contains "$out" "FACT restore: FAIL" "the row fails"
  expect_contains "$out" "keeps firing" "and says so"
}

test_the_fake_refuses_a_flag_the_cli_does_not_have() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE341_STATE="$state" \
    "$work/bin/fugaro" settings set layer.jobs.reports.paused false --nope 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a flag the real CLI does not take"
  expect_contains "$out" "unknown flag: --nope" "with the CLI's own wording"
}

test_the_gcloud_fake_refuses_a_command_its_allow_list_does_not_name() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE341_STATE="$state" \
    FAKE341_ALLOW="$LIVE_CHECK_LIB/allow" "$work/bin/gcloud" builds submit . 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a subcommand the check does not make"
  expect_contains "$out" "unknown command builds" "and says which"
}

test_the_gcloud_fake_refuses_a_flag_its_allow_list_does_not_name() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE341_STATE="$state" \
    FAKE341_ALLOW="$LIVE_CHECK_LIB/allow" "$work/bin/gcloud" scheduler jobs describe fugaro-reports --location us-east4 --nope 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a flag the check does not make"
  expect_contains "$out" "unknown flag: --nope" "and says which"
}

test_preflight_passes_and_writes_nothing() {
  reset_state
  run_check --preflight
  expect_status "$status" 0 "a good snapshot passes"
  expect_contains "$err" "preflight passed" "and says so"
  expect_contains "$err" "read: the reports job fugarorpt-reports is deployed" "the job is read, not assumed"
  expect_contains "$err" "read: fugaro-reports in us-east5 reads" "the schedule is read in the installation's own region"
  expect_absent "$(calls)" "fugaro update" "nothing was updated"
  [ "$(cat "$state/paused")" = true ] || fail "the preflight left the layer changed"
  if grep -qx 'fugaro settings set layer.jobs.reports.paused false --layer project' <<<"$(calls)"; then
    fail "the preflight made the write itself"
  fi
}

test_preflight_blocks_on_a_missing_job_and_launches_nothing() {
  reset_state
  : >"$state/job.missing"
  run_check --preflight
  expect_status "$status" 1 "a blocker exits non-zero"
  expect_contains "$err" "BLOCKED job" "the blocker is printed"
  expect_contains "$err" "fix: fugaro update" "with the fix"
  expect_absent "$(calls)" "fugaro update" "nothing was updated"
}

test_preflight_blocks_on_a_schedule_that_is_not_deployed() {
  reset_state
  : >"$state/scheduler.fail"
  run_check --preflight
  expect_status "$status" 1 "no schedule means nothing fires"
  expect_contains "$err" "BLOCKED schedule" "the blocker is printed"
  expect_contains "$err" "--scheduler-region" "with the flag that resolves it"
}

test_preflight_blocks_on_a_refused_layer_write() {
  reset_state
  : >"$state/set.fail"
  run_check --preflight
  expect_status "$status" 1 "a write the CLI would refuse is a blocker"
  expect_contains "$err" "BLOCKED setting" "the blocker is printed"
  expect_absent "$(calls)" "fugaro update" "nothing was updated"
}

test_preflight_refuses_a_coding_agents_session() {
  reset_state
  EXTRA_ENV=(CLAUDECODE=1)
  run_check --preflight
  unset EXTRA_ENV
  expect_status "$status" 2 "a preflight reads the live installation, which is the owner's own"
  expect_contains "$err" "coding agent's session" "the refusal names what it is"
  expect_absent "$(calls)" "settings get" "nothing was read"
}

# ----------------------------------------------------------------------- runner
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

t test_the_help_lists_the_one_command
t test_refuses_a_coding_agents_session
t test_refuses_belong
t test_refuses_another_installation
t test_stops_on_a_key_this_cli_does_not_write
t test_the_whole_check_passes
t test_it_requires_a_firing_after_its_own_update
t test_the_wait_retries_and_finds_the_firing
t test_the_executions_read_refusing_is_unverified
t test_a_failed_execution_is_a_finding
t test_the_schedule_still_paused_after_the_update_is_a_finding
t test_the_schedule_pointing_at_another_job_is_a_finding
t test_a_missing_schedule_is_unverified_with_the_region_flag
t test_a_job_that_is_not_deployed_is_a_finding
t test_a_job_with_another_argv_is_a_finding
t test_an_assignment_writing_another_bucket_is_a_finding
t test_a_repository_with_no_stored_report_is_a_finding
t test_a_key_with_no_aggregate_beside_it_is_a_finding
t test_a_firing_that_stored_nothing_is_a_finding
t test_a_store_that_does_not_list_is_unverified
t test_a_report_that_does_not_read_back_is_a_finding
t test_an_empty_read_back_is_a_finding
t test_no_retention_rule_under_reports_is_a_finding
t test_a_reports_rule_that_does_not_outlive_the_records_is_a_finding
t test_a_second_writer_of_reports_is_a_finding
t test_a_missing_invoker_grant_is_a_finding
t test_a_write_that_does_not_land_is_a_finding
t test_an_update_that_does_not_apply_is_a_finding
t test_a_setting_that_cannot_be_read_is_a_stop
t test_the_setting_the_layer_did_not_carry_is_unset_again
t test_a_schedule_that_stays_on_after_the_restore_is_a_finding
t test_the_fake_refuses_a_flag_the_cli_does_not_have
t test_the_gcloud_fake_refuses_a_command_its_allow_list_does_not_name
t test_the_gcloud_fake_refuses_a_flag_its_allow_list_does_not_name
t test_preflight_passes_and_writes_nothing
t test_preflight_blocks_on_a_missing_job_and_launches_nothing
t test_preflight_blocks_on_a_schedule_that_is_not_deployed
t test_preflight_blocks_on_a_refused_layer_write
t test_preflight_refuses_a_coding_agents_session

if [ "$failures" -ne 0 ]; then
  echo "check341_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check341_test.sh: ok"
