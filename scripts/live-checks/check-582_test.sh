#!/usr/bin/env bash
# Tests for scripts/live-checks/check-582.sh. NOTHING here is a cloud, a project,
# an installation or a repository that matters: every case builds REAL local git
# checkouts in a temporary directory and drives the check against a fake `gcloud`
# and a fake `fugaro` on PATH whose answers the case owns — the doctor document,
# the published policy, the job list before and after the update, the update's own
# exit status and words, and the run's row. No case can spend anything or reach
# anything, because the only things that reach the cloud are `gcloud` and
# `fugaro`, and here both are scripts in $work.
#
# The readings are the point. Check 582 asks five questions of one migration: did
# the published policy admit the repository to one profile, does the profile's
# shared job carry an image, is the Bitbucket proxy on for it, did the update
# retire the repository's own root, and does a run land on the shared job. So the
# cases that matter are the ones where a reading is bent: a project that is not the
# sandbox, a dirty checkout, an origin that is not Bitbucket, a policy that admits
# no profile or two, a proxy that is off, a shared job with no image or no switch,
# a repository with no jobs of its own, an update that fails, one that holds the
# retirement back by name, one that leaves a step for the owner, one that leaves a
# job behind, a job list that does not answer, a run that did not push, and a run
# that landed on a per-repository job.
#
# The fake `fugaro` models M2's own behaviour where it must: an update that exits
# 0 and prints no hold note retires the repository's root, so the repository's own
# jobs are gone from the next job list. Every other reading is the case's.
#
# Usage: bash scripts/live-checks/check-582_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-582.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK582_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK582_TEST_HEAD:-1600}")" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_fact() { # out key state what
  if printf '%s\n' "$1" | grep -qE "^FACT $2: $3 — "; then return 0; fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK582_TEST_HEAD:-1600}")"
}
expect_no_fail() { # out what
  local n
  n=$(printf '%s\n' "$1" | grep -cE '^FACT [a-z-]+: FAIL — ' || true)
  [ "${n:-0}" = 0 ] || fail "$2: $n row(s) read FAIL: $(printf '%s' "$1" | grep -E '^FACT [a-z-]+: FAIL — ' | head -c 800)"
}
# expect_nothing_read LOG what: the check gave the installation nothing. The two
# reads made before any step are not this — the account `gcloud` reports at parse
# time and `fugaro version` — so what is asserted absent is every read and write
# that is about the installation and the repository.
expect_nothing_read() { # log what
  local line
  for line in "fugaro doctor" "fugaro project" "fugaro update" "fugaro run " "gcloud run jobs"; do
    case "$1" in
    *"$line"*) fail "$2: it reached the installation before it should have: $(printf '%s' "$1" | grep -F "$line" | head -c 400)" ;;
    esac
  done
}

# expect_no_update LOG what: the check never drove `fugaro update --repo`, so the
# repository's own root was never retired — a row above the retirement stopped the
# chain.
expect_no_update() { # log what
  case "$1" in
  *"fugaro update --repo"*) fail "$2: it drove the update before it should have: $(printf '%s' "$1" | grep -F "fugaro update --repo" | head -c 400)" ;;
  esac
}
# expect_no_launch LOG what: the check launched no run.
expect_no_launch() { # log what
  case "$1" in
  *"fugaro run "*) fail "$2: it launched a run before it should have: $(printf '%s' "$1" | grep -F "fugaro run " | head -c 400)" ;;
  esac
}

# ------------------------------------------------------------- the fixtures

# The one repository every case migrates, its derived label and the jobs of the
# installation. The label is gcp.RepoLabel(task.Slug("bitbucket", repo)) — the
# value internal/infra puts on a per-repository job's fugaro_repo label — and it is
# hard-coded here rather than derived, so that a change to that derivation breaks
# this test instead of quietly making every case measure a repository the check
# cannot name.
repo=bb-owner/chk582-sandbox
repo_label=bb-owner-chk582-sandbox-55a7c3074da354fa
shared_job=fugaro-p-team-bitbucket-ab12cd34ef56
repo_job_a=chk582-sandbox-111122223333
repo_job_b=chk582-sandbox-444455556666
install=fugaro
project=fugaro-dev
region=us-east5
run_base=20260101-000000
run_id=$run_base-0582

# make_sandbox writes a real git checkout with a committed fugaro.yaml, an origin
# of its own and one commit.
make_sandbox() { # make_sandbox <dir> [origin]
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" config user.email check582@example.invalid
  git -C "$1" config user.name check582
  cat >"$1/fugaro.yaml" <<'YAML'
version: 1
project: test
agent:
  model: claude-sonnet-5
YAML
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add -A
  git -C "$1" commit -qm sandbox
  git -C "$1" remote remove origin 2>/dev/null || true
  git -C "$1" remote add origin "${2:-git@bitbucket.org:$repo.git}"
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'GCLOUD'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-582.sh's test only. It
# answers the account read the check makes at parse time and the job list it reads
# before and after the update: the FIRST list is the state file as it stands, and
# every later one is the same file — which the fake `fugaro`'s own update rewrites
# when it retires the root. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"${FAKE582_LOG:?}"
dir=${FAKE582_DIR:?}
case "${1:-}" in
config)
  if [ "${2:-}" = get-value ] && [ "${3:-}" = account ]; then
    printf '%s\n' "${FAKE582_ACCOUNT:-chk582-owner@example.invalid}"
  fi
  ;;
run)
  case "${2:-}" in
  jobs)
    n=$(cat "$dir/jobs-calls" 2>/dev/null || printf 0)
    n=$((n + 1))
    printf '%s' "$n" >"$dir/jobs-calls"
    if [ "$n" = 1 ]; then
      status=$(cat "$dir/jobs-status" 2>/dev/null || printf 0)
    else
      status=$(cat "$dir/jobs-after-status" 2>/dev/null || printf 0)
    fi
    if [ "$status" != 0 ]; then
      printf 'fake gcloud: the job list was refused\n' >&2
      exit "$status"
    fi
    cat "$dir/jobs.json"
    ;;
  *)
    printf 'fake gcloud: unexpected run subcommand %s\n' "${2:-<none>}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
GCLOUD
chmod +x "$fakebin/gcloud"

cat >"$fakebin/fugaro" <<'FUGARO'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check-582.sh's test only. It
# answers version, doctor, the published policy, the update, the launch and the
# run listing, and it models the ONE piece of real behaviour a case cannot state
# as an answer: an update that exits 0 and prints no hold note retires the
# repository's whole root, so the repository's own jobs are gone from the next job
# list. Nothing here reaches a real installation.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE582_LOG:?}"
dir=${FAKE582_DIR:?}
case "${1:-}" in
version)
  printf 'fugaro 0.9.0-test\n'
  ;;
doctor)
  cat "$dir/doctor.json"
  exit "$(cat "$dir/doctor-status" 2>/dev/null || printf 0)"
  ;;
project)
  cat "$dir/policy.json"
  ;;
update)
  status=$(cat "$dir/update-status" 2>/dev/null || printf 0)
  cat "$dir/update.out" 2>/dev/null || true
  if [ -f "$dir/update.err" ]; then cat "$dir/update.err" >&2; fi
  if [ "$status" = 0 ] && [ -f "$dir/jobs-after.json" ] &&
    ! grep -q "own root is not retired here" "$dir/update.out" 2>/dev/null; then
    cp "$dir/jobs-after.json" "$dir/jobs.json"
  fi
  exit "$status"
  ;;
run)
  if [ -f "$dir/run-hang" ]; then sleep 8; fi
  status=$(cat "$dir/run-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake fugaro: the launch was refused\n' >&2
    exit "$status"
  fi
  printf '{"run_id":"%s"}\n' "${FAKE582_RUN_ID:-}"
  ;;
runs)
  cat "$dir/runs.json"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FUGARO
chmod +x "$fakebin/fugaro"

# ---------------------------------------------------------------- the answers

# answer_dir builds one case's own world: the sandbox checkout, the doctor
# document, the published policy, the job list before and after the update, the
# update's own words and the run's row, all in the state the happy path needs. A
# case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  sub=$root/sub
  make_sandbox "$sub"
  doctor_json "$answers/doctor.json"
  policy_json "$answers/policy.json"
  jobs_json "$answers/jobs-before.json" both
  jobs_json "$answers/jobs-after.json" shared
  cp "$answers/jobs-before.json" "$answers/jobs.json"
  runs_json "$answers/runs.json" succeeded "$shared_job"
  printf 'fugaro update: the installation %s converged\n' "$install" >"$answers/update.out"
}

doctor_json() { # doctor_json <out> [project]
  python3 - "$1" "${2:-$project}" <<PY
import json, sys
doc = {"project": {"name": "$install", "gcp_project": sys.argv[2], "region": "$region"}}
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

policy_json() { # policy_json <out> [bend]
  python3 - "$1" "${2:-}" <<PY
import json, sys
canon = "bitbucket/$repo"
doc = {"project": "test", "policy": {
    "version": 3,
    "allow_repos": [canon],
    "profiles": {"team": {"provider": "bitbucket", "allow_repos": ["bitbucket/bb-owner/*"],
                          "claude_accounts": ["team-a"]}},
    "bitbucket_token": {"proxy": True, "allow_repos": ["bitbucket/bb-owner/*"]},
    "budget": {"mode": "enforce"},
}}
bend = sys.argv[2]
if bend == "no-profile":
    doc["policy"]["profiles"]["team"]["allow_repos"] = ["bitbucket/someone-else/*"]
if bend == "two-profiles":
    doc["policy"]["profiles"]["team-b"] = {"provider": "bitbucket", "allow_repos": ["bitbucket/bb-owner/*"]}
if bend == "proxy-off":
    doc["policy"]["bitbucket_token"]["proxy"] = False
if bend == "proxy-does-not-admit":
    doc["policy"]["bitbucket_token"]["allow_repos"] = ["bitbucket/someone-else/*"]
if bend == "github-beside":
    # A profile of ANOTHER provider beside the one that admits the repository: it
    # writes no allow_repos of its own, so it falls back to the project's list —
    # and projectpolicy.Admits gates on the provider first, so it admits nothing
    # here and must not be read as the profile this check is about.
    doc["policy"]["profiles"]["github-only"] = {"provider": "github"}
if bend == "github-only":
    doc["policy"]["profiles"] = {"github-only": {"provider": "github"}}
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# jobs_json OUT WHICH — the installation's jobs. `both` is the state before the
# update: the profile's shared job and the repository's own two. `shared` is the
# state after it: the shared job alone. `left` is the state of a retirement that
# left one of the repository's jobs behind. `no-image` and `no-switch` are the
# state before the update with the shared job's image emptied and its
# credentials-service switch removed.
jobs_json() { # jobs_json <out> both|shared|left|no-image|no-switch
  python3 - "$1" "$2" <<PY
import json, sys
shared = {
    "metadata": {"name": "$shared_job",
                 "labels": {"fugaro": "managed", "fugaro_profile": "team",
                            "fugaro_provider": "bitbucket", "fugaro_account": "default"}},
    "spec": {"template": {"spec": {"template": {"spec": {"containers": [{
        "image": "us-east5-docker.pkg.dev/$project/fugaro/entry@sha256:0000000000000000",
        "env": [{"name": "FUGARO_CREDENTIALS_SERVICE_URL",
                 "value": "https://fugaro-credentials-abc.a.run.app"}]}]}}}}},
}
def repo_job(name, workflow):
    return {
        "metadata": {"name": name,
                     "labels": {"fugaro": "managed", "fugaro_repo": "$repo_label",
                                "fugaro_workflow": workflow}},
        "spec": {"template": {"spec": {"template": {"spec": {"containers": [{
            "image": "us-east5-docker.pkg.dev/$project/fugaro/$repo_label@sha256:1111111111111111"}]}}}}},
    }
which = sys.argv[2]
if which == "no-image":
    shared["spec"]["template"]["spec"]["template"]["spec"]["containers"][0]["image"] = ""
if which == "no-switch":
    shared["spec"]["template"]["spec"]["template"]["spec"]["containers"][0]["env"] = []
jobs = [shared]
if which in ("both", "left", "no-image", "no-switch"):
    jobs.append(repo_job("$repo_job_a", "implement"))
if which == "both":
    jobs.append(repo_job("$repo_job_b", "review"))
open(sys.argv[1], "w").write(json.dumps(jobs))
PY
}

# runs_json OUT STATUS JOB — the listing's one row for this check's run.
runs_json() { # runs_json <out> <status> <job> [pushed] [pr]
  python3 - "$1" "$2" "$3" "${4:-yes}" "${5:-7}" <<PY
import json, sys
out, status, job, pushed, pr = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
row = {"run_id": "$run_id", "repo": "$repo", "status": status, "terminal": True,
       "outcome": "ready", "pushed": pushed == "yes", "pr": int(pr),
       "pr_url": "https://bitbucket.org/$repo/pull-requests/%s" % pr,
       "execution": "projects/$project/locations/$region/jobs/%s/executions/chk582-0001" % job}
open(out, "w").write(json.dumps([row]))
PY
}

# ------------------------------------------------------------------ the runs

run_check_raw() { # run_check_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE582_LOG="${CALLS:-/dev/null}" FAKE582_DIR="${answers:-$work}" \
    CHECK582_SANDBOX_PROJECT="${CHECK582_SANDBOX_PROJECT:-$project}" \
    CHECK582_RUN_ID="$run_base" CHECK582_POLL_SECONDS=1 CHECK582_RUN_SECONDS=3 \
    CHECK582_UPDATE_SECONDS=30 CHECK582_DOCTOR_TIMEOUT_SECONDS=30 CHECK582_CLI_TIMEOUT_SECONDS=30 \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run_check() { # run_check [extra args...]
  run_check_raw --sandbox "$sub" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run_check
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 582 RESULT END (exit 0)" "the happy path"
  expect_fact "$out" sandbox PASS "the happy path"
  expect_fact "$out" profile PASS "the happy path"
  expect_fact "$out" premise PASS "the happy path"
  expect_fact "$out" image PASS "the happy path"
  expect_fact "$out" proxy PASS "the happy path"
  expect_fact "$out" retired PASS "the happy path"
  expect_fact "$out" run PASS "the happy path"
  expect_fact "$out" restore PASS "the happy path"
  expect_fact "$out" scratch-removed PASS "the happy path"
  expect_no_fail "$out" "the happy path"
  # The rows really read what they say they read: the repository's own jobs by
  # label, the profile's shared job, and the run on it.
  expect_contains "$out" "$repo_label" "the happy path: the repository's label is read"
  expect_contains "$out" "$repo_job_a" "the happy path: the repository's own jobs are read"
  expect_contains "$out" "$shared_job" "the happy path: the shared job is read"
  # And what it called: the installation's doctor, the published policy, the job
  # list twice, ONE update of the checkout, ONE run and the listing.
  expect_contains "$(cat "$CALLS")" "fugaro doctor --json" "the happy path: the doctor was read"
  expect_contains "$(cat "$CALLS")" "fugaro project policy show --project $install --json" "the happy path: the policy was read"
  expect_contains "$(cat "$CALLS")" "gcloud run jobs list --project=$project --region=$region --format=json" "the happy path: the job list was read"
  expect_contains "$(cat "$CALLS")" "fugaro update --repo --project $install $sub" "the happy path: the update was run on the checkout"
  expect_contains "$(cat "$CALLS")" "fugaro run --repo $repo --project $install" "the happy path: the run was launched on the repository"
  expect_contains "$(cat "$CALLS")" "fugaro runs ls --repo $repo --project $install --no-cache --json" "the happy path: the listing was polled"
  local lists updates launches
  lists=$(grep -c '^gcloud run jobs list ' "$CALLS" || true)
  updates=$(grep -c '^fugaro update ' "$CALLS" || true)
  launches=$(grep -c '^fugaro run ' "$CALLS" || true)
  [ "$lists" = 2 ] || fail "the happy path: expected the job list twice (before and after the update), saw $lists"
  [ "$updates" = 1 ] || fail "the happy path: expected ONE update, saw $updates"
  [ "$launches" = 1 ] || fail "the happy path: expected ONE launch, saw $launches"
  expect_absent "$(cat "$CALLS")" "belong" "the happy path: nothing touches belong"
  # The scratch directory the check made is gone.
  local scratch
  scratch=$(printf '%s\n' "$err" | sed -n 's/^check-582\.sh: scratch directory: \([^ ]*\) .*/\1/p' | head -n 1)
  [ -n "$scratch" ] || fail "the happy path: the check named no scratch directory"
  [ ! -e "$scratch" ] || fail "the happy path: the scratch directory $scratch is still there"
}

test_help() {
  CALLS=
  run_check_raw --help
  expect_status "$status" 0 "check-582 --help"
  expect_contains "$out" "Usage:" "check-582 --help"
  expect_absent "$out" "FACT " "check-582 --help: no block is printed"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run_check_raw --dry-run --sandbox "$sub"
  expect_status "$status" 0 "a dry run"
  expect_contains "$err" "CHECK 582 plan" "a dry run: the plan is on the terminal"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the marker row"
  expect_contains "$out" "FACT retired: INFO — planned" "a dry run: a planned row is INFO"
  expect_nothing_read "$(cat "$CALLS")" "a dry run"
}

test_a_missing_sandbox_is_refused() {
  CALLS=
  run_check_raw
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "needs --sandbox" "a missing sandbox"
}

test_a_sandbox_that_is_not_a_checkout_is_refused() {
  CALLS=
  mkdir -p "$work/not-a-checkout"
  run_check_raw --sandbox "$work/not-a-checkout"
  expect_status "$status" 2 "a sandbox that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a sandbox that is not a checkout"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE582_LOG="$CALLS" FAKE582_DIR="$answers" \
    bash "$script" --sandbox "$sub" 2>&1) || status=$?
  expect_status "$status" 1 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  expect_nothing_read "$(cat "$CALLS")" "an agent session"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE582_LOG="$CALLS" FAKE582_DIR="$answers" \
    bash -x "$script" --sandbox "$sub" 2>&1) || status=$?
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_a_dirty_checkout_is_refused() {
  case_dir dirty_checkout
  printf 'work in progress\n' >>"$sub/README.md"
  run_check
  expect_status "$status" 1 "a dirty checkout"
  expect_contains "$out" "has uncommitted changes" "a dirty checkout"
  expect_nothing_read "$(cat "$CALLS")" "a dirty checkout"
}

test_a_non_bitbucket_origin_is_refused() {
  case_dir non_bitbucket
  git -C "$sub" remote set-url origin git@github.com:bb-owner/chk582-sandbox.git
  run_check
  expect_status "$status" 1 "a non-Bitbucket origin"
  expect_contains "$out" "not a Bitbucket repository" "a non-Bitbucket origin"
}

test_a_project_that_is_not_the_sandbox_is_refused() {
  case_dir not_the_sandbox
  doctor_json "$answers/doctor.json" some-other-project
  run_check
  expect_status "$status" 1 "a project that is not the sandbox"
  expect_contains "$out" "runs on the sandbox project only ($project)" "a project that is not the sandbox"
  expect_no_update "$(cat "$CALLS")" "a project that is not the sandbox"
  expect_no_launch "$(cat "$CALLS")" "a project that is not the sandbox"
}

test_a_belong_project_is_refused() {
  case_dir belong_project
  doctor_json "$answers/doctor.json" belong
  run_check
  expect_status "$status" 1 "a belong project"
  expect_contains "$out" "names belong" "a belong project"
  expect_no_update "$(cat "$CALLS")" "a belong project"
  expect_no_launch "$(cat "$CALLS")" "a belong project"
}

test_a_doctor_that_does_not_answer_leaves_the_rows_unmeasured() {
  case_dir doctor_unreadable
  printf 7 >"$answers/doctor-status"
  run_check
  expect_status "$status" 2 "a doctor that does not answer"
  expect_fact "$out" sandbox FAIL "a doctor that does not answer"
  expect_contains "$out" "did not answer" "a doctor that does not answer"
  expect_no_update "$(cat "$CALLS")" "a doctor that does not answer"
  expect_no_launch "$(cat "$CALLS")" "a doctor that does not answer"
}

test_a_policy_that_admits_no_profile_is_a_finding() {
  case_dir no_profile
  policy_json "$answers/policy.json" no-profile
  run_check
  expect_status "$status" 1 "a policy that admits no profile"
  expect_fact "$out" profile FAIL "a policy that admits no profile"
  expect_contains "$out" "NO profile" "a policy that admits no profile"
  expect_no_update "$(cat "$CALLS")" "a policy that admits no profile"
  expect_no_launch "$(cat "$CALLS")" "a policy that admits no profile"
}

test_a_policy_that_admits_two_profiles_leaves_the_row_unmeasured() {
  case_dir two_profiles
  policy_json "$answers/policy.json" two-profiles
  run_check
  expect_status "$status" 2 "a policy that admits two profiles"
  expect_fact "$out" profile FAIL "a policy that admits two profiles"
  expect_contains "$out" "2 profiles" "a policy that admits two profiles"
}

test_a_profile_of_another_provider_does_not_admit_the_repository() {
  case_dir other_provider
  policy_json "$answers/policy.json" github-beside
  run_check
  expect_status "$status" 0 "a profile of another provider beside the one that admits it"
  expect_fact "$out" profile PASS "a profile of another provider beside the one that admits it"
  expect_contains "$out" "exactly ONE profile" "a profile of another provider beside the one that admits it"
  expect_absent "$out" "github-only" "a profile of another provider beside the one that admits it: it is not read as the one that admits the repository"
}

test_a_policy_whose_only_profile_serves_another_provider_is_a_finding() {
  case_dir only_other_provider
  policy_json "$answers/policy.json" github-only
  run_check
  expect_status "$status" 1 "a policy whose only profile serves another provider"
  expect_fact "$out" profile FAIL "a policy whose only profile serves another provider"
  expect_contains "$out" "NO profile" "a policy whose only profile serves another provider"
}

test_a_proxy_that_is_off_is_a_finding() {
  case_dir proxy_off
  policy_json "$answers/policy.json" proxy-off
  run_check
  expect_status "$status" 1 "a proxy that is off"
  expect_fact "$out" proxy FAIL "a proxy that is off"
  expect_contains "$out" "does not broker" "a proxy that is off"
  expect_no_update "$(cat "$CALLS")" "a proxy that is off"
  expect_no_launch "$(cat "$CALLS")" "a proxy that is off"
}

test_a_proxy_that_does_not_admit_the_repository_is_a_finding() {
  case_dir proxy_does_not_admit
  policy_json "$answers/policy.json" proxy-does-not-admit
  run_check
  expect_status "$status" 1 "a proxy that does not admit the repository"
  expect_fact "$out" proxy FAIL "a proxy that does not admit the repository"
}

test_a_shared_job_without_an_image_is_a_finding() {
  case_dir no_image
  jobs_json "$answers/jobs.json" no-image
  run_check
  expect_status "$status" 1 "a shared job without an image"
  expect_fact "$out" image FAIL "a shared job without an image"
  expect_contains "$out" "empty image" "a shared job without an image"
  expect_no_update "$(cat "$CALLS")" "a shared job without an image"
  expect_no_launch "$(cat "$CALLS")" "a shared job without an image"
}

test_a_shared_job_without_the_switch_is_a_finding() {
  case_dir no_switch
  jobs_json "$answers/jobs.json" no-switch
  run_check
  expect_status "$status" 1 "a shared job without the switch"
  expect_fact "$out" proxy FAIL "a shared job without the switch"
  expect_contains "$out" "FUGARO_CREDENTIALS_SERVICE_URL" "a shared job without the switch"
  expect_no_update "$(cat "$CALLS")" "a shared job without the switch"
  expect_no_launch "$(cat "$CALLS")" "a shared job without the switch"
}

test_a_repository_with_no_jobs_of_its_own_leaves_the_rows_unmeasured() {
  case_dir no_repo_jobs
  jobs_json "$answers/jobs.json" shared
  run_check
  expect_status "$status" 2 "a repository with no jobs of its own"
  expect_fact "$out" premise FAIL "a repository with no jobs of its own"
  expect_contains "$out" "NO per-repository job" "a repository with no jobs of its own"
  expect_no_update "$(cat "$CALLS")" "a repository with no jobs of its own"
  expect_no_launch "$(cat "$CALLS")" "a repository with no jobs of its own"
}

test_a_job_list_that_does_not_answer_leaves_the_rows_unmeasured() {
  case_dir jobs_unreadable
  printf 7 >"$answers/jobs-status"
  run_check
  expect_status "$status" 2 "a job list that does not answer"
  expect_fact "$out" premise FAIL "a job list that does not answer"
  expect_fact "$out" image FAIL "a job list that does not answer"
  expect_fact "$out" proxy FAIL "a job list that does not answer"
  expect_no_update "$(cat "$CALLS")" "a job list that does not answer"
  expect_no_launch "$(cat "$CALLS")" "a job list that does not answer"
}

test_an_update_that_holds_the_retirement_leaves_the_row_unmeasured() {
  case_dir retirement_held
  printf 'note: %s'\''s own root is not retired here: the Bitbucket proxy is off; a later update retires it once that holds\n' "$repo" >"$answers/update.out"
  run_check
  expect_status "$status" 2 "an update that holds the retirement"
  expect_fact "$out" retired FAIL "an update that holds the retirement"
  expect_contains "$out" "own root is not retired here" "an update that holds the retirement"
  expect_contains "$out" "a retirement that did not happen yet" "an update that holds the retirement"
  # Everything the update's own note did not touch was measured: the rows above
  # the retirement are read before it, and the run is not launched on a
  # migration that did not happen.
  expect_fact "$out" premise PASS "an update that holds the retirement: the premise"
  expect_fact "$out" image PASS "an update that holds the retirement: the image"
  expect_fact "$out" proxy PASS "an update that holds the retirement: the proxy"
  expect_absent "$(cat "$CALLS")" "fugaro run " "an update that holds the retirement: no run was launched"
}

test_an_update_that_leaves_a_step_for_you_leaves_the_row_unmeasured() {
  case_dir update_left_for_you
  printf 1 >"$answers/update-status"
  printf 'fugaro update: %s is not finished: 2 step(s) left for you, listed above\n' "$repo" >"$answers/update.out"
  run_check
  expect_status "$status" 2 "an update that leaves a step for you"
  expect_fact "$out" retired FAIL "an update that leaves a step for you"
  expect_contains "$out" "2 step(s) left for you" "an update that leaves a step for you"
  expect_contains "$out" "settle that step" "an update that leaves a step for you"
}

test_an_update_that_fails_is_a_finding() {
  case_dir update_fails
  printf 1 >"$answers/update-status"
  printf 'fugaro update: the repository root failed to apply\n' >"$answers/update.err"
  run_check
  expect_status "$status" 1 "an update that fails"
  expect_fact "$out" retired FAIL "an update that fails"
  expect_contains "$out" "exited 1" "an update that fails"
  expect_contains "$out" "repository root failed to apply" "an update that fails"
}

test_a_retirement_that_leaves_a_job_behind_is_a_finding() {
  case_dir job_left_behind
  jobs_json "$answers/jobs-after.json" left
  run_check
  expect_status "$status" 1 "a retirement that leaves a job behind"
  expect_fact "$out" retired FAIL "a retirement that leaves a job behind"
  expect_contains "$out" "STILL deployed" "a retirement that leaves a job behind"
  expect_contains "$out" "$repo_job_a" "a retirement that leaves a job behind: the job is named"
}

test_a_job_list_that_does_not_answer_after_the_update_leaves_the_row_unmeasured() {
  case_dir jobs_after_unreadable
  printf 7 >"$answers/jobs-after-status"
  run_check
  expect_status "$status" 2 "a job list that does not answer after the update"
  expect_fact "$out" retired FAIL "a job list that does not answer after the update"
  expect_contains "$out" "did not answer afterwards" "a job list that does not answer after the update"
  expect_no_launch "$(cat "$CALLS")" "a job list that does not answer after the update"
}

test_a_run_that_did_not_push_is_a_finding() {
  case_dir run_did_not_push
  runs_json "$answers/runs.json" succeeded "$shared_job" no 7
  run_check
  expect_status "$status" 1 "a run that did not push"
  expect_fact "$out" run FAIL "a run that did not push"
  expect_contains "$out" "did not end with a push" "a run that did not push"
}

test_a_run_on_a_per_repository_job_is_a_finding() {
  case_dir run_on_a_repo_job
  runs_json "$answers/runs.json" succeeded "$repo_job_a"
  run_check
  expect_status "$status" 1 "a run on a per-repository job"
  expect_fact "$out" run FAIL "a run on a per-repository job"
  expect_contains "$out" "the migration is not done" "a run on a per-repository job"
}

test_a_run_that_never_appears_leaves_the_row_unmeasured() {
  case_dir run_never_appears
  printf '[]' >"$answers/runs.json"
  run_check
  expect_status "$status" 2 "a run that never appears"
  expect_fact "$out" run FAIL "a run that never appears"
  expect_contains "$out" "does not appear in" "a run that never appears"
}

test_a_launch_that_is_refused_leaves_the_row_unmeasured() {
  case_dir launch_refused
  printf 1 >"$answers/run-status"
  run_check
  expect_status "$status" 2 "a launch that is refused"
  expect_fact "$out" run FAIL "a launch that is refused"
  expect_contains "$out" "never launched a run" "a launch that is refused"
  expect_contains "$out" "No run was launched" "a launch that is refused: the restore row does not claim one"
}

test_a_launch_that_does_not_answer_leaves_the_row_unmeasured() {
  case_dir launch_hangs
  : >"$answers/run-hang"
  run_check
  expect_status "$status" 2 "a launch that does not answer"
  expect_fact "$out" run FAIL "a launch that does not answer"
  expect_contains "$out" "did not answer inside" "a launch that does not answer"
  expect_contains "$out" "may have been launched" "a launch that does not answer: the restore row says it may exist"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run_check_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK582_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK582_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help
t test_a_dry_run_calls_nothing
t test_a_missing_sandbox_is_refused
t test_a_sandbox_that_is_not_a_checkout_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_dirty_checkout_is_refused
t test_a_non_bitbucket_origin_is_refused
t test_a_project_that_is_not_the_sandbox_is_refused
t test_a_belong_project_is_refused
t test_a_doctor_that_does_not_answer_leaves_the_rows_unmeasured
t test_a_policy_that_admits_no_profile_is_a_finding
t test_a_policy_that_admits_two_profiles_leaves_the_row_unmeasured
t test_a_profile_of_another_provider_does_not_admit_the_repository
t test_a_policy_whose_only_profile_serves_another_provider_is_a_finding
t test_a_proxy_that_is_off_is_a_finding
t test_a_proxy_that_does_not_admit_the_repository_is_a_finding
t test_a_shared_job_without_an_image_is_a_finding
t test_a_shared_job_without_the_switch_is_a_finding
t test_a_repository_with_no_jobs_of_its_own_leaves_the_rows_unmeasured
t test_a_job_list_that_does_not_answer_leaves_the_rows_unmeasured
t test_an_update_that_holds_the_retirement_leaves_the_row_unmeasured
t test_an_update_that_leaves_a_step_for_you_leaves_the_row_unmeasured
t test_an_update_that_fails_is_a_finding
t test_a_retirement_that_leaves_a_job_behind_is_a_finding
t test_a_job_list_that_does_not_answer_after_the_update_leaves_the_row_unmeasured
t test_a_run_that_did_not_push_is_a_finding
t test_a_run_on_a_per_repository_job_is_a_finding
t test_a_run_that_never_appears_leaves_the_row_unmeasured
t test_a_launch_that_is_refused_leaves_the_row_unmeasured
t test_a_launch_that_does_not_answer_leaves_the_row_unmeasured
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-582_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-582_test.sh: ok"
