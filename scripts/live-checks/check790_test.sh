#!/usr/bin/env bash
# Tests for scripts/live-checks/check790.sh. NOTHING here is a cloud, a project,
# an installation or a run: every case builds a REAL local git repository in a
# temporary directory and drives the check against a fake `gcloud` and a fake
# `fugaro` on PATH whose answers the case owns — the job's own describe, the
# published policy, two launches' answers, the runs listing, the update's output
# and the second installation's config. No case can spend anything, because the
# only thing that spends is `fugaro run`, and here that is a script in $work.
#
# The readings are the point. Check 790 watches ONE upgrade across a run that is
# already in flight, and the cases that matter are the ones where a reading is
# bent: an installation already migrated (a premise that must STOP the check), an
# update that fails, an in-flight run the database's rules killed, an execution
# that still holds a model key, a pre-release image that records another mode, an
# operator's update that changes something.
#
# Usage: bash scripts/live-checks/check790_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check790.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK790_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK790_TEST_HEAD:-1200}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# ---------------------------------------------------------------- the fakes
fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"${FAKE790_LOG:?}"
dir=${FAKE790_DIR:?}
case "${1:-} ${2:-}" in
"run jobs")
  case "${3:-}" in
  describe)
    job=${4:-}
    if [ -n "${FAKE790_OPERATOR_JOB:-}" ] && [ "$job" = "$FAKE790_OPERATOR_JOB" ]; then
      cat "$dir/operator-job.json"
    else
      state=$(cat "$dir/state" 2>/dev/null || printf 'before')
      case "$state" in
      after) cat "$dir/job-after.json" ;;
      *) cat "$dir/job-before.json" ;;
      esac
    fi
    ;;
  executions)
    cat "$dir/exec.json"
    ;;
  *)
    printf 'fake gcloud: unexpected run jobs verb %s\n' "${3:-<none>}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected verb %s %s\n' "${1:-<none>}" "${2:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"${FAKE790_LOG:?}"
dir=${FAKE790_DIR:?}
first=${1:-}
second=${2:-}
case "$first" in
project)
  cat "$dir/policy.json"
  exit "${FAKE790_POLICY_STATUS:-0}"
  ;;
run)
  n=$(cat "$dir/launches" 2>/dev/null || printf 0)
  n=$((n + 1))
  printf '%s' "$n" >"$dir/launches"
  printf '{"run_id":"acme-app/20261120-1200%02d-abcd"}\n' "$n"
  exit "${FAKE790_RUN_STATUS:-0}"
  ;;
runs)
  # The FIRST poll after the update answers $dir/runs.json; every later one
  # answers $dir/runs2.json when the case wrote it, so a run still in flight at
  # the first read and terminal at the second is a state a case can build. The
  # runs the check launched itself are appended to whatever the case wrote: a
  # real `fugaro runs ls` on this repository lists them too, and the row that
  # reads a pre-release runner's mode looks the post-update run up by its id.
  n=$(cat "$dir/polls" 2>/dev/null || printf 0)
  n=$((n + 1))
  printf '%s' "$n" >"$dir/polls"
  if [ "$n" -ge 2 ] && [ -f "$dir/runs2.json" ]; then src="$dir/runs2.json"; else src="$dir/runs.json"; fi
  python3 - "$src" "$(cat "$dir/launches" 2>/dev/null || printf 0)" \
    "$(cat "$dir/new-mode" 2>/dev/null || printf observe)" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
launches, mode = int(sys.argv[2]), sys.argv[3]

rows = doc.get("runs", []) if isinstance(doc, dict) else doc
for i in range(2, launches + 1):
    rid = "acme-app/20261120-1200%02d-abcd" % i
    rows.append({"id": rid, "run_id": rid, "status": "running", "budget": {"mode": mode}})
print(json.dumps({"runs": rows}))
PY
  exit 0
  ;;
update)
  if [ "$second" = "--config" ]; then
    cat "$dir/operator.out"
    exit "${FAKE790_OPERATOR_STATUS:-0}"
  fi
  cat "$dir/update.out"
  printf 'after' >"$dir/state"
  exit "${FAKE790_UPDATE_STATUS:-0}"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the cases
make_sandbox() { # make_sandbox <dir>
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" config user.email check790@example.invalid
  git -C "$1" config user.name check790
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add README.md
  git -C "$1" commit -qm "sandbox"
}

# job_json writes a Cloud Run job document with the given container image and one
# environment entry per argument after it. An entry of the form `secret:NAME` is
# written as a secret reference, which is how the row that reads a mounted model
# key is bent.
job_json() { # job_json <outfile> <image> [ENTRY...]
  local out=$1 image=$2
  shift 2
  python3 - "$out" "$image" "$@" <<'PY'
import json, sys
out, image = sys.argv[1], sys.argv[2]
env = []
for raw in sys.argv[3:]:
    if raw.startswith("secret:"):
        env.append({"name": raw[len("secret:"):], "valueSource": {"secretKeyRef": {"name": raw[len("secret:"):]}}})
    else:
        name, _, value = raw.partition("=")
        env.append({"name": name, "value": value})
doc = {"spec": {"template": {"spec": {"template": {"spec": {"containers": [{"image": image, "env": env}]}}}}}}
open(out, "w").write(json.dumps(doc))
PY
}

# exec_json writes a Cloud Run EXECUTION document, in the shape an execution
# really has (`spec.template.spec.containers`).
exec_json() { # exec_json <outfile> <image> [ENTRY...]
  local out=$1 image=$2
  shift 2
  python3 - "$out" "$image" "$@" <<'PY'
import json, sys
out, image = sys.argv[1], sys.argv[2]
env = []
for raw in sys.argv[3:]:
    if raw.startswith("secret:"):
        env.append({"name": raw[len("secret:"):], "valueSource": {"secretKeyRef": {"name": raw[len("secret:"):]}}})
    else:
        name, _, value = raw.partition("=")
        env.append({"name": name, "value": value})
doc = {"spec": {"template": {"spec": {"containers": [{"image": image, "env": env}]}}}}
open(out, "w").write(json.dumps(doc))
PY
}

runs_json() { # runs_json <outfile> <run-id> <outcome> <mode>
  python3 - "$1" "$2" "$3" "$4" <<'PY'
import json, sys
out, rid, outcome, mode = sys.argv[1:5]
row = {"id": rid, "run_id": rid, "status": "running" if not outcome else "done", "budget": {"mode": mode}}
if outcome:
    row["outcome"] = outcome
open(out, "w").write(json.dumps({"runs": [row]}))
PY
}

# case_dir builds one case's own world: a sandbox checkout, a job that starts
# with no database, a policy that carries no URL, and the fakes' answers.
case_dir() { # case_dir <name>
  sandbox=$work/$1/repo
  answers=$work/$1/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  printf 'before' >"$answers/state"
  printf 0 >"$answers/launches"
  printf 0 >"$answers/polls"
  make_sandbox "$sandbox"
  job_json "$answers/job-before.json" "us-docker.pkg.dev/p/jobs/app:0.34.0" "FUGARO_BUCKET=b"
  job_json "$answers/job-after.json" "us-docker.pkg.dev/p/jobs/app:0.34.0" "FUGARO_BUCKET=b" "FUGARO_RTDB_URL=https://db.invalid"
  job_json "$answers/operator-job.json" "us-docker.pkg.dev/p/jobs/app:0.34.0" "FUGARO_BUCKET=b"
  exec_json "$answers/exec.json" "us-docker.pkg.dev/p/jobs/app:0.34.0" "FUGARO_BUCKET=b" "FUGARO_RTDB_URL=https://db.invalid"
  printf '{"budget":{"mode":"observe"}}' >"$answers/policy.json"
  printf 'fugaro update: created the budget database at https://db.invalid and re-applied the jobs\n' >"$answers/update.out"
  printf "fugaro update: this installation has no budget database yet, and only a project owner can create it: ask a project owner to run 'fugaro update'\n" >"$answers/operator.out"
  printf 'name: second-install\njob: fugaro-app-operator\n' >"$answers/operator-config.yaml"
  runs_json "$answers/runs.json" "acme-app/20261120-120001-abcd" "" observe
  runs_json "$answers/runs2.json" "acme-app/20261120-120001-abcd" succeeded observe
  FAKE790_UPDATE_STATUS=0
  FAKE790_OPERATOR_STATUS=0
  FAKE790_RUN_STATUS=0
  FAKE790_POLICY_STATUS=0
}

run790() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" \
    FAKE790_LOG="$CALLS" FAKE790_DIR="$answers" \
    FAKE790_OPERATOR_JOB="${FAKE790_OPERATOR_JOB:-}" \
    FAKE790_UPDATE_STATUS="${FAKE790_UPDATE_STATUS:-0}" \
    FAKE790_OPERATOR_STATUS="${FAKE790_OPERATOR_STATUS:-0}" \
    FAKE790_RUN_STATUS="${FAKE790_RUN_STATUS:-0}" \
    FAKE790_POLICY_STATUS="${FAKE790_POLICY_STATUS:-0}" \
    CHECK790_POLL_SECONDS=0 \
    ${RUN790_ENV[@]+"${RUN790_ENV[@]}"} \
    bash "$script" --installation test --sandbox "$sandbox" --job "$(basename "$work")-job" \
    --gcp-project test-project --region us-test1 "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN790_ENV=()
}

test_help() {
  status=0
  out=$(PATH="$fakebin:$PATH" bash "$script" --help 2>&1) || status=$?
  expect_status "$status" 0 "check790.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "--installation" "--help names the installation flag"
  expect_contains "$out" "--job" "--help names the job flag"
}

test_a_dry_run_calls_nothing() {
  case_dir dry-run
  run790 --dry-run
  expect_status "$status" 0 "the plan is not a run"
  expect_contains "$out" "Check 790 plan" "--dry-run prints the plan"
  expect_contains "$out" "FUGARO_RTDB_URL" "the plan names what the premise reads"
  expect_contains "$out" "model spend" "the plan says what it costs"
  [ ! -s "$CALLS" ] || fail "a dry run made a call: $(cat "$CALLS")"
}

test_the_happy_path_reads_every_row() {
  case_dir happy
  # The operator-config names the second installation's job; the fake answers
  # that job name out of operator-job.json.
  printf 'name: second-install\njob: operator-job\n' >"$answers/operator-config.yaml"
  FAKE790_OPERATOR_JOB="operator-job"
  run790 --operator-config "$answers/operator-config.yaml"
  expect_status "$status" 0 "every row of a clean upgrade passes"
  expect_contains "$out" "premise" "the premise is a row"
  expect_contains "$out" "in-flight" "the run launched before the update is a row"
  expect_contains "$out" "update" "the owner's update is a row"
  expect_contains "$out" "database" "the database the update created is a row"
  expect_contains "$out" "in-flight-2" "the run across the update is a row"
  expect_contains "$out" "gateway" "the post-update run's gateway is a row"
  expect_contains "$out" "old-image" "the pre-release image is a row"
  expect_contains "$out" "operator" "the second installation is a row"
  expect_contains "$out" "CHECK 790 RESULT: PASS (exit 0)" "the verdict is PASS"
}

test_an_installation_already_migrated_stops_the_check() {
  case_dir migrated
  job_json "$answers/job-before.json" "us-docker.pkg.dev/p/jobs/app:0.35.0" "FUGARO_RTDB_URL=https://db.invalid"
  run790
  expect_status "$status" 2 "this check is about an installation AT THE PREVIOUS RELEASE"
  expect_contains "$out" "already carries a database address" "the premise names what it read"
  expect_contains "$out" "CHECK 790 RESULT: INCOMPLETE (exit 2)" "the verdict is INCOMPLETE"
  case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "a run was launched on an installation the premise had already refused" ;;
  esac
}

test_a_policy_that_already_names_a_database_stops_the_check() {
  case_dir policy-migrated
  printf '{"budget":{"mode":"observe","rtdb_url":"https://db.invalid"}}' >"$answers/policy.json"
  run790
  expect_status "$status" 2 "a published policy that names a database is the release having arrived"
  expect_contains "$out" "published policy carries a budget database URL" "the premise names what it read"
}

test_an_update_that_fails_is_a_finding() {
  case_dir update-fails
  FAKE790_UPDATE_STATUS=1
  printf 'fugaro update: terraform apply failed\n' >"$answers/update.out"
  run790
  expect_status "$status" 2 "the check stops when the upgrade did not finish"
  expect_contains "$out" "update" "the update row is the finding"
  expect_contains "$out" "did not exit 0" "the finding says the command failed"
}

test_a_job_that_never_learned_its_database_is_a_finding() {
  case_dir no-database
  # The update exits 0 but leaves the job without a database address.
  job_json "$answers/job-after.json" "us-docker.pkg.dev/p/jobs/app:0.34.0" "FUGARO_BUCKET=b"
  run790
  expect_status "$status" 1 "a job that was not re-applied is what this row catches"
  expect_contains "$out" "still carries no database address" "the finding says what is wrong"
  expect_contains "$out" "CHECK 790 RESULT: FAIL (exit 1)" "the verdict is FAIL"
}

test_an_in_flight_run_that_dies_is_a_finding() {
  case_dir inflight-dies
  runs_json "$answers/runs.json" "acme-app/20261120-120001-abcd" failed observe
  runs_json "$answers/runs2.json" "acme-app/20261120-120001-abcd" failed observe
  run790
  expect_status "$status" 1 "the database's rules must not kill a run already going"
  expect_contains "$out" "in-flight-2" "the row is the finding"
  expect_contains "$out" "ended 'failed'" "the finding names the outcome"
}

test_an_in_flight_run_that_never_ends_leaves_the_row_unverified() {
  case_dir inflight-forever
  rm -f "$answers/runs2.json"
  run790
  expect_status "$status" 3 "a run the check could not watch to an end is not a pass"
  expect_contains "$out" "still not terminal" "the row says what it could not read"
}

test_an_execution_that_still_mounts_a_model_key_is_a_finding() {
  case_dir still-mounts
  exec_json "$answers/exec.json" "us-docker.pkg.dev/p/jobs/app:0.34.0" "FUGARO_BUCKET=b" "FUGARO_RTDB_URL=https://db.invalid" "secret:anthropic-api-key"
  run790
  expect_status "$status" 1 "the agent's environment must hold no model credential"
  expect_contains "$out" "gateway" "the row is the finding"
  expect_contains "$out" "WITHOUT the gateway" "the finding says why it matters"
}

test_a_pre_release_image_that_records_another_mode_is_a_finding() {
  case_dir wrong-mode
  # The post-update run is the one that landed on the pre-release image, and its
  # own record says enforce: the run before it is left alone, so the finding can
  # only come from the row that reads the post-update run.
  printf 'enforce' >"$answers/new-mode"
  run790
  expect_status "$status" 1 "an unset mode IS observe"
  expect_contains "$out" "old-image" "the row is the finding"
  expect_contains "$out" "not 'observe'" "the finding names what it read"
}

test_an_image_that_carries_the_checkout_head_is_not_a_pre_release_image() {
  case_dir head-image
  head=$(git -C "$sandbox" rev-parse HEAD)
  job_json "$answers/job-after.json" "us-docker.pkg.dev/p/jobs/app:$head" "FUGARO_BUCKET=b" "FUGARO_RTDB_URL=https://db.invalid"
  run790
  expect_status "$status" 3 "an image built from this checkout is not a runner from before the release"
  expect_contains "$out" "does NOT predate the release" "the row says why it has no subject"
}

test_an_operator_update_that_changes_things_is_a_finding() {
  case_dir operator-changes
  printf 'name: second-install\njob: operator-job\n' >"$answers/operator-config.yaml"
  job_json "$answers/operator-job.json" "us-docker.pkg.dev/p/jobs/app:0.35.0" "FUGARO_RTDB_URL=https://db.invalid"
  FAKE790_OPERATOR_JOB="operator-job"
  run790 --operator-config "$answers/operator-config.yaml"
  expect_status "$status" 1 "an operator's roles must not create the database"
  expect_contains "$out" "operator" "the row is the finding"
  expect_contains "$out" "CHANGED the second installation" "the finding says what moved"
}

test_an_operator_update_that_says_nothing_is_a_finding() {
  case_dir operator-silent
  printf 'name: second-install\njob: operator-job\n' >"$answers/operator-config.yaml"
  printf 'fugaro update: done\n' >"$answers/operator.out"
  run790 --operator-config "$answers/operator-config.yaml"
  expect_status "$status" 1 "an installation that launches nothing until a project owner acts must say so"
  expect_contains "$out" "without naming a project owner" "the finding says what is missing"
}

test_no_operator_config_leaves_that_row_unverified() {
  case_dir no-operator
  run790
  expect_status "$status" 3 "a second installation nobody named is not measured"
  expect_contains "$out" "no --operator-config" "the row says what it needs"
}

test_a_missing_flag_is_refused() {
  case_dir no-job
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE790_LOG="$CALLS" FAKE790_DIR="$answers" \
    bash "$script" --installation test --sandbox "$sandbox" --gcp-project p --region r 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "the job is one of the things this check reads"
  expect_contains "$err" "no --job" "the refusal says which flag is missing"
}

test_a_sandbox_that_is_not_a_checkout_is_refused() {
  case_dir not-git
  sandbox=$work/not-git/plain
  mkdir -p "$sandbox"
  run790
  expect_status "$status" 2 "the runs are launched from a checkout"
  expect_contains "$err" "not a git checkout" "the refusal says what the path is"
}

test_an_agent_session_is_refused() {
  case_dir agent
  RUN790_ENV=(CLAUDECODE=1)
  run790
  expect_status "$status" 2 "no agent runs any step of Check 790"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_contains "$err" "coding agent" "the refusal says what the session is"
  case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "an agent's session launched a run before the refusal" ;;
  esac
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK790_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK790_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK790_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_a_dry_run_calls_nothing
t test_the_happy_path_reads_every_row
t test_an_installation_already_migrated_stops_the_check
t test_a_policy_that_already_names_a_database_stops_the_check
t test_an_update_that_fails_is_a_finding
t test_a_job_that_never_learned_its_database_is_a_finding
t test_an_in_flight_run_that_dies_is_a_finding
t test_an_in_flight_run_that_never_ends_leaves_the_row_unverified
t test_an_execution_that_still_mounts_a_model_key_is_a_finding
t test_a_pre_release_image_that_records_another_mode_is_a_finding
t test_an_image_that_carries_the_checkout_head_is_not_a_pre_release_image
t test_an_operator_update_that_changes_things_is_a_finding
t test_an_operator_update_that_says_nothing_is_a_finding
t test_no_operator_config_leaves_that_row_unverified
t test_a_missing_flag_is_refused
t test_a_sandbox_that_is_not_a_checkout_is_refused
t test_an_agent_session_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check790_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check790_test.sh: ok"
