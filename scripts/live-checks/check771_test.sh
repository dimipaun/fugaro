#!/usr/bin/env bash
# Tests for scripts/live-checks/check771.sh. NOTHING here is a cloud, a project
# or an installation: every reading the check makes is a fixture in a temporary
# directory, served by a fake gcloud and a fake fugaro on PATH, and the cases
# assert what the script printed and what it refused.
#
# The bends are the point, and they are U3's own: a member-side role that carries
# a permission beside instances.get, a role that is not there at all, a role
# nobody is bound to, a binding that names an account other than the one the
# deployment runs as, and a service whose log holds a refusal on a read the
# get-only role should have carried — which is U3 answered NO. Beside them are
# the readings the check has to be honest about not having taken: no admitted
# call in the window, no refusal at the ceiling, no moment for the removal.
#
# The fake gcloud is PROJECT-AWARE, because this check reads TWO projects: the
# workspace's service and its log are the workspace's own project's and the role
# and its binding are the member's. A read that names the other project is
# refused here exactly as gcloud would refuse it for a service that is not there,
# so a check that read the service from the member's project — the bug this fake
# exists to catch — fails every case rather than passing them.
#
# Usage: bash scripts/live-checks/check771_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check771.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK771_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK771_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK771_TEST_HEAD:-1200}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 600))"
}

[ -f "$script" ] || {
  echo "check771_test.sh: the script $script does not exist" >&2
  exit 1
}

fx=$work/fx
mkdir -p "$work/bin" "$fx"

# ---------------------------------------------------------------- the fakes
cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor)
  echo "release: nothing to move"
  echo "project: fugaro (GCP fugaro-dev, us-east5)"
  echo "secrets: none"
  ;;
*) echo "fake fugaro: unknown invocation $*" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/fugaro"

cat >"$work/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
ws=${FAKE_WS_PROJECT:?}
member=${FAKE_MEMBER_PROJECT:?}
args="$*"
project=""
for arg in "$@"; do
  case "$arg" in
  --project=*) project=${arg#--project=} ;;
  esac
done
# where KIND WANT: the service and its log are the workspace's project's, the
# role and its binding are the member's. gcloud would find nothing at the other
# project, and this fake refuses it the same way — loudly, so a case can assert
# which project a read named.
where() {
  [ "$project" = "$2" ] || {
    echo "fake gcloud: $1 is in $2, not in ${project:-<no project named>}" >&2
    exit 1
  }
}
case "$args" in
*"config get-value account"*) echo "owner@example.com" ;;
*"auth print-access-token"*) echo "fake-access-token" ;;
*"run services describe"*)
  where "the workspace's service" "$ws"
  if [ "$mode" = no-service ]; then echo "fake: no such service" >&2; exit 1; fi
  cat "$fx/service.json"
  ;;
*"logging read"*)
  where "the workspace's service's log" "$ws"
  if [ "$mode" = no-log ]; then echo "fake: logging denied" >&2; exit 1; fi
  cat "$fx/logs.json"
  ;;
*"iam roles describe"*)
  where "the member's role" "$member"
  if [ "$mode" = no-role ]; then echo "fake: no such role" >&2; exit 1; fi
  cat "$fx/role.json"
  ;;
*"projects get-iam-policy"*)
  where "the member's IAM policy" "$member"
  if [ "$mode" = no-policy ]; then echo "fake: permission denied" >&2; exit 1; fi
  cat "$fx/policy.json"
  ;;
*) echo "fake gcloud: unknown invocation $args" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/gcloud"

# ---------------------------------------------------------------- the fixtures
WORKSPACE_PROJECT="fugaro-workspace"
MEMBER_PROJECT="fugaro-dev"
WORKSPACE_SA="fugaro-workspace-credentials@${WORKSPACE_PROJECT}.iam.gserviceaccount.com"
MEMBER_JOB="member-job@${MEMBER_PROJECT}.iam.gserviceaccount.com"

service_json() {
  cat <<JSON
{"status": {"url": "https://fugaro-workspace-credentials-fake.a.run.app",
            "latestReadyRevisionName": "fugaro-workspace-credentials-00042-abc"},
 "spec": {"template": {"spec": {"serviceAccountName": "$WORKSPACE_SA",
                                "containers": [{"concurrency": 40}]}}}}
JSON
}

role_json() { # role_json PERMISSIONS...
  local perms="" one
  for one in "$@"; do
    perms="${perms}${perms:+, }\"$one\""
  done
  printf '{"name": "projects/fugaro-dev/roles/fugaroWorkspaceRead", "stage": "GA", "includedPermissions": [%s]}' "$perms"
}

policy_json() { # policy_json MEMBERS...
  local members="" one
  for one in "$@"; do
    members="${members}${members:+, }\"$one\""
  done
  printf '{"bindings": [{"role": "projects/fugaro-dev/roles/fugaroWorkspaceRead", "members": [%s]}]}' "$members"
}

# stamp SECONDS_FROM_NOW prints an RFC 3339 moment, so a case pins a refusal
# inside the grace rather than at a date that ages out of the window.
stamp() {
  python3 -c '
import datetime, sys
print((datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=int(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))
' "$1"
}

# logs_json writes a log listing: one admitted call naming the member's job
# account, and whatever the caller adds.
logs_json() { # logs_json EXTRA_ENTRIES_JSON
  cat >"$fx/logs.json" <<JSON
[{"timestamp": "$(stamp -120)",
  "textPayload": "credsvc: audit run=20261121-000000-aaaa repository=owner/repo job=$MEMBER_JOB caller=$MEMBER_JOB purpose=messages tier=standard status=ok",
  "resource": {"labels": {"instance_id": "inst-1"}},
  "httpRequest": {"status": 200, "latency": "0.200s", "requestUrl": "https://svc/v1/messages"}}${1:+,$1}]
JSON
}

fixtures_ok() {
  service_json >"$fx/service.json"
  role_json firebasedatabase.instances.get >"$fx/role.json"
  policy_json "serviceAccount:$WORKSPACE_SA" >"$fx/policy.json"
  logs_json ""
}

# Every case runs the check against the workspace's own project; a case that
# bends the project names its own with RUN771_ENV.
all_options=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT)

run771() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" FAKE_MODE="${FAKE_MODE:-ok}" FAKE_FX="$fx" TMPDIR="$work" \
    FAKE_WS_PROJECT="$WORKSPACE_PROJECT" FAKE_MEMBER_PROJECT="$MEMBER_PROJECT" \
    ${RUN771_ENV[@]+"${RUN771_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN771_ENV=()
}

run_check() {
  RUN771_ENV=("${all_options[@]}" ${CHECK771_TEST_ENV[@]+"${CHECK771_TEST_ENV[@]}"})
  run771 "$@"
}

# ------------------------------------------------------------------- the cases
test_help() {
  run771 --help
  expect_status "$status" 0 "check771.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "CHECK771_CEILING_AT" "--help names the moments the check reads"
}

test_dry_run_measures_nothing() {
  fixtures_ok
  run_check --dry-run
  expect_status "$status" 0 "--dry-run is a plan and nothing else"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run says it ran nothing"
  expect_contains "$out" "FACT role: INFO" "a planned row reads INFO, not PASS"
  expect_contains "$err" "iam roles describe" "the plan names the reads it would make"
}

test_an_admitted_member_and_a_refused_ceiling_pass() {
  fixtures_ok
  # The refusal at the ceiling, five seconds after the moment the owner names,
  # and a 4xx five seconds after the removal.
  logs_json "{\"timestamp\": \"$(stamp 5)\",
    \"textPayload\": \"credsvc: refused type=budget_unavailable status=403 read= policy_shared message=the workspace's own ceiling would be passed by this call\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 403, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}},
   {\"timestamp\": \"$(stamp 20)\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 403, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}}"
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_CEILING_AT=$(date -u +%s)" "CHECK771_REMOVED_AT=$(date -u +%s)")
  run771
  expect_status "$status" 0 "every measured criterion held"
  expect_contains "$out" "FACT service: PASS" "the deployment was read"
  expect_contains "$out" "running as $WORKSPACE_SA" "the row names the account the revision runs as"
  expect_contains "$out" "FACT role: PASS" "the role carries instances.get and nothing else"
  expect_contains "$out" "FACT binding: PASS" "the role is bound to the account the deployment runs as"
  expect_contains "$out" "FACT reads: PASS" "a member's call was admitted and no policy read was refused"
  expect_contains "$out" "U3 is settled YES" "the row says what U3 was asked"
  expect_contains "$out" "FACT admitted: PASS" "the member's job account was admitted"
  expect_contains "$out" "FACT ceiling: PASS" "the call at the ceiling was refused"
  expect_contains "$out" "FACT calls-prompt: INFO" "the three calls are the owner's, and the row says so"
}

test_a_widened_role_fails() {
  fixtures_ok
  role_json firebasedatabase.instances.get firebasedatabase.instances.update >"$fx/role.json"
  run_check
  expect_status "$status" 1 "a role that carries a second permission is not instances.get alone"
  expect_contains "$out" "FACT role: FAIL" "the role row is the finding"
  expect_contains "$out" "carries firebasedatabase.instances.get firebasedatabase.instances.update" "the row names what it carries"
  expect_contains "$out" "narrow it back in the Terraform" "the row says what to do"
}

test_a_missing_role_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-role
  run_check
  expect_status "$status" 2 "a role that is not there is not a pass"
  expect_contains "$out" "FACT role: FAIL" "the row reads FAIL"
  expect_contains "$out" "does not exist in fugaro-dev" "the row says what it looked for"
  expect_contains "$out" "workspace_access.tf" "the row names the file that creates it"
}

test_an_unbound_role_fails() {
  fixtures_ok
  # A policy whose only binding is another role: nobody holds this one.
  printf '{"bindings": [{"role": "roles/editor", "members": ["serviceAccount:someone-else@fugaro-dev.iam.gserviceaccount.com"]}]}' >"$fx/policy.json"
  run_check
  expect_status "$status" 1 "a role nobody holds is not a read the service makes"
  expect_contains "$out" "FACT binding: FAIL" "the binding row is the finding"
  expect_contains "$out" "no member of fugaro-dev is bound to fugaroWorkspaceRead" "the row says what it found"
}

test_a_binding_on_another_account_fails() {
  fixtures_ok
  policy_json "serviceAccount:another-service@fugaro-dev.iam.gserviceaccount.com" >"$fx/policy.json"
  run_check
  expect_status "$status" 1 "the account that serves the call has to be the one the role names"
  expect_contains "$out" "FACT binding: FAIL" "the binding row is the finding"
  expect_contains "$out" "does not hold the member-side role" "the row says what is wrong"
}

test_a_refused_policy_read_is_u3_answered_no() {
  fixtures_ok
  logs_json "{\"timestamp\": \"$(stamp -60)\",
    \"textPayload\": \"credsvc: refused type=state_unavailable status=503 read=policy_shared message=the workspace could not read the member's policy\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 503, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}}"
  run_check
  expect_status "$status" 1 "a policy read the get-only role could not carry is the finding U3 asks for"
  expect_contains "$out" "FACT reads: FAIL" "the reads row is the finding"
  expect_contains "$out" "read=policy_shared" "the row names the read that failed"
  expect_contains "$out" "did NOT let the service read" "the row says what U3 is then answered"
}

test_a_window_with_no_admitted_call_is_unmeasured() {
  fixtures_ok
  printf '[]' >"$fx/logs.json"
  run_check
  expect_status "$status" 2 "no admitted call in the window measures nothing"
  expect_contains "$out" "FACT reads: FAIL" "the reads row reads FAIL"
  expect_contains "$out" "hold no credsvc: audit line" "the row says what is missing"
  expect_contains "$out" "launched as the member" "the row says how to produce the evidence"
}

test_a_log_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  FAKE_MODE=no-log
  run_check
  expect_status "$status" 2 "a log that cannot be read measures nothing"
  expect_contains "$out" "FACT reads: FAIL" "the reads row reads FAIL"
  expect_contains "$out" "roles/logging.viewer" "the row names the role the read needs"
  expect_contains "$out" "FACT admitted: FAIL" "the rows that read the same log read FAIL too"
}

test_no_ceiling_moment_and_no_ceiling_refusal_is_unmeasured() {
  fixtures_ok
  run_check
  expect_status "$status" 2 "nothing to read at the ceiling is not a pass"
  expect_contains "$out" "FACT ceiling: FAIL" "the ceiling row reads FAIL"
  expect_contains "$out" "CHECK771_CEILING_AT was not set" "the row names the moment it needs"
  expect_contains "$out" "workspace cap set" "the row says how to put the cap at its ceiling"
}

test_a_served_call_at_the_ceiling_is_unmeasured_with_the_moment() {
  fixtures_ok
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_CEILING_AT=$(date -u +%s)")
  run771
  expect_status "$status" 2 "a call at the ceiling that left no refusal is not a pass"
  expect_contains "$out" "FACT ceiling: FAIL" "the ceiling row reads FAIL"
  expect_contains "$out" "no refusal follows the moment you named" "the row says what it did not find"
}

test_a_removal_with_no_moment_is_unmeasured() {
  fixtures_ok
  run_check
  expect_status "$status" 2 "without the moment the wait cannot be measured"
  expect_contains "$out" "FACT removed: FAIL" "the removed row reads FAIL"
  expect_contains "$out" "CHECK771_REMOVED_AT was not set" "the row names the moment it needs"
  expect_contains "$out" "workspace member rm" "the row says what to do"
}

test_a_removal_refused_within_the_minute_passes() {
  fixtures_ok
  # The ceiling's own refusal five seconds after the moment the owner names, and
  # a 4xx with no refusal line at all twenty seconds after it: the ceiling's is
  # EXCLUDED from the removal's reading, and the removal passes on the other one.
  local at ceiling_at later
  at=$(date -u +%s)
  ceiling_at=$(stamp 5)
  later=$(stamp 20)
  logs_json "{\"timestamp\": \"$ceiling_at\",
    \"textPayload\": \"credsvc: refused type=budget_unavailable status=403 read= message=the workspace's own ceiling would be passed by this call\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 403, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}},
   {\"timestamp\": \"$later\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 403, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}}"
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_CEILING_AT=$at" "CHECK771_REMOVED_AT=$at")
  run771
  expect_status "$status" 0 "a 4xx inside the grace is the removal taking effect"
  expect_contains "$out" "FACT removed: PASS" "the removed row is a pass"
  expect_contains "$out" "NOT the ceiling's own refusal" "the row says which 4xx it read"
  expect_contains "$out" "status 403 at $later" "and it is the one that is not the ceiling's"
  expect_contains "$out" "The service re-reads its own node when it is older than 60 seconds (R13)" "the row names what the minute is"
}

test_a_removal_that_was_served_is_unmeasured() {
  fixtures_ok
  logs_json "{\"timestamp\": \"$(stamp 900)\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 403, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}}"
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_REMOVED_AT=$(date -u +%s)")
  run771
  expect_status "$status" 2 "a refusal fifteen minutes later is not within the minute"
  expect_contains "$out" "FACT removed: FAIL" "the removed row reads FAIL"
  expect_contains "$out" "no 4xx request follows the moment you named" "the row says what it did not find"
}

test_a_job_account_named_by_knob_has_to_appear() {
  fixtures_ok
  service_json >"$fx/service.json"
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_JOB_ACCOUNT=some-other-job@fugaro-dev.iam.gserviceaccount.com")
  run771
  expect_status "$status" 2 "an account that never appears is not an admission"
  expect_contains "$out" "FACT admitted: FAIL" "the admitted row reads FAIL"
  expect_contains "$out" "some-other-job@fugaro-dev.iam.gserviceaccount.com" "the row names the account it looked for"
}

test_a_ceiling_refusal_that_does_not_name_the_ceiling_is_unmeasured() {
  fixtures_ok
  # A refusal inside the grace whose own words are about something else: the
  # service writes every refusal the same way, so a line like this is NOT the
  # ceiling having been checked.
  local at
  at=$(date -u +%s)
  logs_json "{\"timestamp\": \"$(stamp 5)\",
    \"textPayload\": \"credsvc: refused type=identity_unverified status=401 read= message=the caller's identity could not be verified\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 401, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}}"
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_CEILING_AT=$at")
  run771
  expect_status "$status" 2 "a refusal that is not the ceiling's measures nothing at the ceiling"
  expect_contains "$out" "FACT ceiling: FAIL" "the ceiling row reads FAIL"
  expect_contains "$out" "does not name the ceiling" "the row says what the refusal was missing"
  expect_contains "$out" "identity could not be verified" "the row quotes the refusal it found"
}

test_a_removal_with_only_the_ceiling_refusal_is_unmeasured() {
  fixtures_ok
  # The bug this case exists for: the removal's own call was SERVED, and the only
  # 4xx in its window is the ceiling's refusal of a different call. Reading that
  # 4xx as the removal having taken effect would print PASS over a served call.
  local at
  at=$(date -u +%s)
  logs_json "{\"timestamp\": \"$(stamp 5)\",
    \"textPayload\": \"credsvc: refused type=budget_unavailable status=403 read= message=the workspace's own ceiling would be passed by this call\",
    \"resource\": {\"labels\": {\"instance_id\": \"inst-1\"}},
    \"httpRequest\": {\"status\": 403, \"latency\": \"0.010s\", \"requestUrl\": \"https://svc/v1/messages\"}}"
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT "CHECK771_REMOVED_AT=$at")
  run771
  expect_status "$status" 2 "the ceiling's refusal is not the removal taking effect"
  expect_contains "$out" "FACT removed: FAIL" "the removed row reads FAIL, not PASS"
  expect_absent "$out" "FACT removed: PASS" "a served removal is never a pass"
  expect_contains "$out" "every one of them is the CEILING's own refusal" "the row says what it found"
  expect_contains "$out" "which the ceiling row already reads" "the row says why that is not this row's evidence"
}

test_a_member_project_read_is_not_the_workspace_project_read() {
  fixtures_ok
  # The split, made visible: the member's project is where the role and the
  # binding are read, and the workspace's service and log are NOT there. A check
  # that read the service from the member's project would fail here, because the
  # fake refuses the read the way gcloud would.
  CHECK771_TEST_ENV=(CHECK771_MEMBER_PROJECT=another-project)
  run_check
  expect_status "$status" 2 "a role read at another project is not a reading"
  expect_contains "$out" "FACT role: FAIL" "the role row reads FAIL"
  expect_contains "$out" "does not exist in another-project" "the row names the project it read"
  expect_contains "$out" "the member's role is in $MEMBER_PROJECT, not in another-project" "the row quotes the fake's refusal at the wrong project"
  expect_contains "$out" "FACT service: PASS" "the workspace's service was read from the workspace's own project"
}

test_no_workspace_project_is_refused() {
  fixtures_ok
  RUN771_ENV=()
  run771 --dry-run
  expect_status "$status" 2 "the workspace's project is a reading this check cannot guess"
  expect_contains "$err" "CHECK771_WORKSPACE_PROJECT was not set" "the refusal names the knob it needs"
  expect_contains "$err" "roots/workspace/main.tf" "the refusal says where the service is deployed from"
}

test_the_installation_guard_refuses_another_project() {
  fixtures_ok
  cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor) echo "project: other (GCP other-prod, us-central1)" ;;
esac
SH
  chmod +x "$work/bin/fugaro"
  run_check
  expect_status "$status" 1 "another installation is not this check's subject"
  expect_contains "$err" "this is not the fugaro-dev installation" "the refusal says what it read"
  expect_contains "$out" "FACT service: FAIL — not reached" "nothing was measured"
}

test_an_agent_session_is_refused() {
  fixtures_ok
  RUN771_ENV=(CHECK771_WORKSPACE_PROJECT=$WORKSPACE_PROJECT CLAUDECODE=1)
  run771
  expect_status "$status" 1 "no agent runs any step of Check 771"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_absent "$out" "FACT service: PASS" "nothing was read before the refusal"
}

test_an_unknown_argument_is_refused() {
  run771 --nonsense
  expect_status "$status" 2 "an argument this check does not know is refused"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal names it"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK771_TEST_ONLY=name runs that one.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK771_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK771_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  FAKE_MODE=ok
  "$1"
}

t test_help
t test_dry_run_measures_nothing
t test_an_admitted_member_and_a_refused_ceiling_pass
t test_a_widened_role_fails
t test_a_missing_role_is_unmeasured
t test_an_unbound_role_fails
t test_a_binding_on_another_account_fails
t test_a_refused_policy_read_is_u3_answered_no
t test_a_window_with_no_admitted_call_is_unmeasured
t test_a_log_that_cannot_be_read_is_unmeasured
t test_no_ceiling_moment_and_no_ceiling_refusal_is_unmeasured
t test_a_served_call_at_the_ceiling_is_unmeasured_with_the_moment
t test_a_removal_with_no_moment_is_unmeasured
t test_a_removal_refused_within_the_minute_passes
t test_a_removal_that_was_served_is_unmeasured
t test_a_job_account_named_by_knob_has_to_appear
t test_a_ceiling_refusal_that_does_not_name_the_ceiling_is_unmeasured
t test_a_removal_with_only_the_ceiling_refusal_is_unmeasured
t test_a_member_project_read_is_not_the_workspace_project_read
t test_no_workspace_project_is_refused
t test_the_installation_guard_refuses_another_project
t test_an_agent_session_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check771_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check771_test.sh: ok"
