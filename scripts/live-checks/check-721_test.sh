#!/usr/bin/env bash
# Tests for scripts/live-checks/check-721.sh. NOTHING here is a cloud, a project,
# an installation or a log: `fugaro` and `gcloud` are scripts in $work whose
# answers each case owns, so no case can spend anything or reach anything. What
# the fakes model is the three facts the check reads and cannot fake for itself:
#
#   * a WAIT-STATING CHECKLIST and a member table, as `fugaro workspace show
#     --json` prints them: whether this machine holds the workspace's own config,
#     the member's job service accounts (a domain is where the member's GCP
#     project is read from) and whether a config here names the member.
#   * the DRIFT ROW, as `fugaro workspace project status --json` prints it: the
#     fake builds the row from what the fake gcloud did, so the row names the
#     entry of the hand-made console change, the entry of the member's own
#     update, both or neither, and each bent row is a case of its own.
#   * GOOGLE'S OWN LOG, as Admin Activity holds it: one entry per SetIamPolicy
#     binding ADDED, keyed by insertId, with the actor Google recorded. The fake
#     writes the update's entry when its `fugaro update` ran, the check's own
#     entry when the binding was added, and a third party's when the case asks
#     for one.
#
# The readings are the point, so the cases that matter are the ones where one is
# bent: a row that names nothing, a row that names the update's own change, a row
# that names a change somebody else made, a row that names another role, another
# entry or another project, a row that reports the log as not measured, a log the
# check cannot read, a log whose answer cannot be parsed, a change that was never
# logged, an update that failed or did not run, a binding Google refuses, a
# restore that fails, --keep, and every flag this check refuses.
#
# Usage: bash scripts/live-checks/check-721_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-721.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK721_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK721_TEST_HEAD:-2000}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK721_TEST_HEAD:-2000}")"
}
expect_count() { # file needle want what
  local got
  got=$(grep -c -- "$2" "$1" 2>/dev/null || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 line(s) matching '$2' in $(basename "$1"), got $got"
}
expect_match() { # haystack regex what
  if printf '%s\n' "$1" | grep -Eq -- "$2"; then
    return 0
  fi
  fail "$3: expected a line matching '$2'; got: $(printf '%s' "$1" | head -c "${CHECK721_TEST_HEAD:-2000}")"
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'GCLOUD'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE721_DIR:?}
printf 'gcloud %s\n' "$*" >>"${FAKE721_GLOG:?}"

pos=()
flags=" "
while [ $# -gt 0 ]; do
  case "$1" in
  --quiet | -q) shift ;;
  --format=value*) shift ;;
  --*=*) flags="$flags$1 "; shift ;;
  *) pos+=("$1"); shift ;;
  esac
done
flag_value() { # flag_value <--name>
  local want=$1 item
  for item in $flags; do
    case "$item" in
    "$want"=*) printf '%s' "${item#"$want"=}" ;;
    esac
  done
}
status_of() { cat "$dir/$1" 2>/dev/null || printf 0; }
must() { # must <case-file> <what>
  local st
  st=$(status_of "$1")
  if [ "$st" != 0 ]; then
    if [ -f "$dir/$2.err" ]; then cat "$dir/$2.err" >&2; else printf 'fake gcloud: %s refused\n' "$2" >&2; fi
    exit "$st"
  fi
}
grant_field() { cut -f"$1" "$dir/state/grant" 2>/dev/null || true; }
now_stamp() { date -u +%Y-%m-%dT%H:%M:%S.000000000Z; }
entry_json() { # entry_json <id> <at> <actor> <role> <member>
  printf '{"insertId":"%s","timestamp":"%s","protoPayload":{"methodName":"SetIamPolicy","authenticationInfo":{"principalEmail":"%s"},"serviceData":{"policyDelta":{"bindingDeltas":[{"action":"ADD","role":"%s","member":"%s"}]}}}}' \
    "$1" "$2" "$3" "$4" "$5"
}

verb=${pos[0]:-}
sub=${pos[1]:-}
arg=${pos[2]:-}
case "$verb $sub" in
"auth list")
  must auth-status auth
  cat "$dir/account" 2>/dev/null || printf 'owner@example.invalid'
  ;;
"auth application-default")
  must adc-status adc
  printf 'chk721-fake-access-token'
  ;;
"projects describe")
  must describe-status describe
  case "$arg" in
  "$(cat "$dir/describe-reply" 2>/dev/null || printf 'chk721mem')") printf '%s\n' "$arg" ;;
  *) printf '%s\n' "$arg" ;;
  esac
  ;;
"projects add-iam-policy-binding")
  must grant-status grant
  printf '%s\t%s\t%s\t%s\n' "$arg" "$(flag_value --role)" "$(flag_value --member)" chk721-grant-1 >"$dir/state/grant"
  now_stamp >"$dir/state/granted-at"
  printf 'granted\n' >"$dir/state/granted"
  printf 'Updated IAM policy for project [%s].\n' "$arg"
  ;;
"projects remove-iam-policy-binding")
  must ungrant-status ungrant
  rm -f "$dir/state/granted"
  printf 'Updated IAM policy for project [%s].\n' "$arg"
  ;;
"logging read")
  must log-status log
  if [ -f "$dir/log.out" ]; then cat "$dir/log.out"; printf '\n'; exit 0; fi
  if [ -f "$dir/log-empty" ]; then printf '[]\n'; exit 0; fi
  sep=""
  printf '['
  if [ -f "$dir/state/updated" ] && [ ! -f "$dir/no-update-entry" ]; then
    printf '%s' "$sep$(entry_json chk721-upd-1 "$(cat "$dir/state/updated-at" 2>/dev/null || printf '2026-01-01T00:00:00.000000000Z')" "$(cat "$dir/account" 2>/dev/null || printf 'owner@example.invalid')" roles/editor user:member-owner@example.invalid)"
    sep=","
  fi
  if [ -f "$dir/other-change" ]; then
    printf '%s' "$sep$(entry_json chk721-other-1 "$(cat "$dir/state/updated-at" 2>/dev/null || printf '2026-01-01T00:00:00.000000000Z')" someone@example.invalid roles/browser user:colleague@example.invalid)"
    sep=","
  fi
  if [ -f "$dir/state/granted" ]; then
    printf '%s' "$sep$(entry_json "$(grant_field 4)" "$(cat "$dir/state/granted-at")" "$(cat "$dir/account" 2>/dev/null || printf 'owner@example.invalid')" "$(grant_field 2)" "$(grant_field 3)")"
    sep=","
  fi
  printf ']\n'
  ;;
*)
  printf 'fake gcloud: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
GCLOUD

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE721_DIR:?}
printf 'fugaro %s | config=%s\n' "$*" "${FUGARO_PROJECT:-}" >>"${FAKE721_LOG:?}"

pos=()
while [ $# -gt 0 ]; do
  case "$1" in
  --json) shift ;;
  *) pos+=("$1"); shift ;;
  esac
done
verb=${pos[0]:-}
sub=${pos[1]:-}
third=${pos[2]:-}
grant_field() { cut -f"$1" "$dir/state/grant" 2>/dev/null || true; }
status_of() { cat "$dir/$1" 2>/dev/null || printf 0; }
must() { # must <case-file> <what>
  local st
  st=$(status_of "$1")
  if [ "$st" != 0 ]; then
    if [ -f "$dir/$2.err" ]; then cat "$dir/$2.err" >&2; else printf 'fake fugaro: %s refused\n' "$2" >&2; fi
    exit "$st"
  fi
}
show_doc() {
  local owner=true membership=yes
  [ -f "$dir/no-owner" ] && owner=false
  [ -f "$dir/no-membership" ] && membership=no
  printf '{"version":2,"workspaces":[{"name":"chk721ws","owner":%s,"members":[{"project":"chk721mem","job_service_accounts":[{"account":"%s","repos":["chk721/repo"]}],"firebase_project":"chk721mem","fp":"chk721-fp","exchange_url":"https://exchange.example.invalid/chk721ws","repos":["chk721/repo"]}],"memberships":[%s],"problems":[],"shared":[]}],"warnings":[]}\n' \
    "$owner" "${JOB_ACCOUNT-fugaro-job@chk721mem.iam.gserviceaccount.com}" \
    "$([ "$membership" = yes ] && printf '{"project":"chk721mem","firebase_project":"chk721mem","issuer":"https://issuer.example.invalid","service_account":"pin@chk721mem.iam.gserviceaccount.com","service_address":""}' || printf '')"
}
drift_row() { # a status row: "<status>\t<detail>"
  local project role member entry addr detail st findings
  if [ -f "$dir/no-drift-row" ]; then return 0; fi
  project=$(grant_field 1)
  role=$(grant_field 2)
  member=$(grant_field 3)
  entry=$(grant_field 4)
  [ -n "$project" ] || project=chk721mem
  [ -n "$role" ] || role=roles/viewer
  [ -n "$member" ] || member="user:chk721-1@example.com"
  [ -n "$entry" ] || entry=chk721-grant-1
  addr=${member#user:}
  if [ -f "$dir/drift-unmeasured" ]; then
    printf 'warn\tno difference was found and part of the member was not compared; not measured: Cloud Audit Logs of %s: googleapi: Error 403: The caller does not have permission\n' "$project"
    return 0
  fi
  if [ -f "$dir/drift-none" ]; then
    printf "pass\tthe member's live IAM matches the record\n"
    return 0
  fi
  findings="extra $addr as $role on $project"
  if [ ! -f "$dir/drift-names-nothing" ]; then
    local named_role=$role named_entry=$entry named_project=$project
    [ -f "$dir/drift-wrong-role" ] && named_role=roles/editor
    [ -f "$dir/drift-wrong-entry" ] && named_entry=chk721-grant-9
    [ -f "$dir/drift-wrong-project" ] && named_project=chk721-other
    findings="$findings, changed in the console: $addr as $named_role on $named_project ($named_entry)"
  fi
  if [ -f "$dir/drift-names-update" ]; then
    findings="$findings, changed in the console: member-owner@example.invalid as roles/editor on $project (chk721-upd-1)"
  fi
  if [ -f "$dir/drift-names-other" ]; then
    findings="$findings, changed in the console: colleague@example.invalid as roles/browser on $project (chk721-other-1)"
  fi
  printf 'warn\t2 difference(s) between this member%s live IAM and the record: %s\n' "'" "$findings"
}
status_doc() {
  local counts pending drift st detail
  counts=$(cat "$dir/state/status-calls" 2>/dev/null || printf 0)
  counts=$((counts + 1))
  printf '%s\n' "$counts" >"$dir/state/status-calls"
  if [ -f "$dir/status-bad-json" ]; then
    printf 'fugaro: this is not a document\n'
    exit 0
  fi
  if [ -f "$dir/status-after-bad-json" ] && [ "$counts" -gt 1 ]; then
    printf 'fugaro: this is not a document\n'
    exit 0
  fi
  pending="pass\tnothing the record states is waiting for this member's update"
  [ -f "$dir/nothing-pending" ] || pending="warn\t2 grant(s) of this member are waiting for its own update: launcher someone@example.invalid, operator somebody@example.invalid"
  rows=$(printf '{"id":"reachable","status":"pass","detail":"the member answered"},{"id":"pending-apply","status":"%s","detail":"%s"}' \
    "$(printf '%s' "$pending" | cut -f1)" "$(printf '%s' "$pending" | cut -f2-)")
  # no-drift-row is a document with no drift row at all: drift_row's own answer
  # stands for a row that is there, so the marker has to drop the row.
  if [ ! -f "$dir/no-drift-row" ]; then
    drift=$(drift_row)
    if [ -n "$drift" ]; then
      st=$(printf '%s' "$drift" | cut -f1)
      detail=$(printf '%s' "$drift" | cut -f2-)
    else
      st=pass
      detail="the member's live IAM matches the record"
    fi
    rows="$rows,{\"id\":\"drift\",\"status\":\"$st\",\"detail\":\"$detail\"}"
  fi
  printf '{"version":1,"workspace":"chk721ws","checked_credentials":false,"members":[{"project":"chk721mem","worst":"warn","rows":[%s]}],"rows":[]}\n' \
    "$rows"
}

case "$verb" in
update)
  must update-status update
  if [ -f "$dir/update-sleeps" ]; then sleep 30; fi
  if [ -f "$dir/update.out" ]; then cat "$dir/update.out"; else printf 'update: nothing to do\n'; fi
  date -u +%Y-%m-%dT%H:%M:%S.000000000Z >"$dir/state/updated-at"
  : >"$dir/state/updated"
  ;;
workspace)
  case "$sub" in
  show)
    must show-status show
    show_doc
    ;;
  project)
    [ "$third" = status ] || {
      printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
      exit 64
    }
    must status-status status
    status_doc
    ;;
  *)
    printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud" "$fakebin/fugaro"

case_dir() { # case_dir <name>
  dir=$work/$1
  mkdir -p "$dir/state"
  : >"$dir/CALLS"
  : >"$dir/GCALLS"
  printf 'owner@example.invalid' >"$dir/account"
}

run721_raw() { # run721_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE721_DIR="$dir" \
    FAKE721_LOG="$dir/CALLS" FAKE721_GLOG="$dir/GCALLS" \
    bash "$script" "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
}

run721() { # run721 [extra args...]
  run721_raw --workspace chk721ws --project chk721mem "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run721
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 721 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" member PASS "the happy path"
  expect_row "$out" status PASS "the happy path"
  expect_row "$out" update PASS "the happy path"
  expect_row "$out" grant PASS "the happy path"
  expect_row "$out" log PASS "the happy path"
  expect_row "$out" entry PASS "the happy path"
  expect_row "$out" names PASS "the happy path"
  expect_row "$out" silent PASS "the happy path"
  expect_row "$out" ungrant PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  # The row the check is built on: this check's change named with Google's own
  # entry, and the member's own update's change left out.
  expect_contains "$out" "changed in the console: chk721-1@example.com as roles/viewer on chk721mem (chk721-grant-1)" "the happy path"
  expect_contains "$out" "names none of the 1 change(s) the member's own update made in the window (chk721-upd-1)" "the happy path"
  # Every call it makes, and which installation each one used.
  expect_count "$dir/CALLS" "fugaro workspace show chk721ws --json | config=" 1 "the happy path"
  expect_count "$dir/CALLS" "fugaro workspace project status chk721mem --json | config=chk721ws" 2 "the happy path"
  expect_count "$dir/CALLS" "fugaro update | config=chk721mem" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud --quiet projects add-iam-policy-binding chk721mem --member=user:chk721-" 1 "the happy path"
  expect_match "$(cat "$dir/GCALLS")" 'gcloud --quiet projects add-iam-policy-binding chk721mem --member=user:chk721-[0-9]+@example\.com --role=roles/viewer --condition=None --format=none' "the happy path"
  expect_count "$dir/GCALLS" "gcloud --quiet projects remove-iam-policy-binding chk721mem --member=user:chk721-" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud --quiet logging read" 1 "the happy path"
  expect_contains "$(cat "$dir/GCALLS")" 'cloudaudit.googleapis.com%2Factivity' "the happy path"
  expect_contains "$out" "WHAT TO OPEN, BY YOU (U5)" "the happy path"
  expect_contains "$out" "https://console.cloud.google.com/iam-admin/iam?project=chk721mem" "the happy path"
}

test_a_row_that_names_nothing_is_a_finding() {
  case_dir names_nothing
  : >"$dir/drift-names-nothing"
  run721
  expect_status "$status" 1 "a row that names nothing"
  expect_row "$out" names FAIL "a row that names nothing"
  expect_contains "$out" "the drift row does not name this check's own change as" "a row that names nothing"
}

test_a_row_that_names_the_update_is_a_finding() {
  case_dir names_the_update
  : >"$dir/drift-names-update"
  run721
  expect_status "$status" 1 "a row that names the update"
  expect_row "$out" names PASS "a row that names the update"
  expect_row "$out" silent FAIL "a row that names the update"
  expect_contains "$out" "the drift row names change(s) the member's own update made" "a row that names the update"
  expect_contains "$out" "chk721-upd-1 (roles/editor for user:member-owner@example.invalid)" "a row that names the update"
}

test_a_row_that_names_somebody_elses_change_still_passes() {
  case_dir names_other
  : >"$dir/other-change"
  : >"$dir/drift-names-other"
  run721
  expect_status "$status" 0 "a row that names somebody else's change"
  expect_row "$out" names PASS "a row that names somebody else's change"
  expect_row "$out" silent PASS "a row that names somebody else's change"
  expect_contains "$out" "which is the cross-check working" "a row that names somebody else's change"
  expect_contains "$out" "chk721-other-1 by someone@example.invalid" "a row that names somebody else's change"
}

test_a_row_that_names_another_role_is_a_finding() {
  case_dir wrong_role
  : >"$dir/drift-wrong-role"
  run721
  expect_status "$status" 1 "a row that names another role"
  expect_row "$out" names FAIL "a row that names another role"
}

test_a_row_that_names_another_entry_is_a_finding() {
  case_dir wrong_entry
  : >"$dir/drift-wrong-entry"
  run721
  expect_status "$status" 1 "a row that names another entry"
  expect_row "$out" names FAIL "a row that names another entry"
  expect_contains "$out" "(chk721-grant-1)" "a row that names another entry"
}

test_a_row_that_names_another_project_is_a_finding() {
  case_dir wrong_project
  : >"$dir/drift-wrong-project"
  run721
  expect_status "$status" 1 "a row that names another project"
  expect_row "$out" names FAIL "a row that names another project"
}

test_a_row_with_no_findings_is_a_finding() {
  case_dir no_findings
  : >"$dir/drift-none"
  run721
  expect_status "$status" 1 "a row with no findings"
  expect_row "$out" names FAIL "a row with no findings"
  expect_contains "$out" "the member's live IAM matches the record" "a row with no findings"
}

test_a_row_that_did_not_measure_the_log_is_the_answer_to_u5() {
  case_dir unmeasured
  : >"$dir/drift-unmeasured"
  run721
  expect_status "$status" 3 "a row that did not measure the log"
  expect_row "$out" names UNVERIFIED "a row that did not measure the log"
  expect_row "$out" silent UNVERIFIED "a row that did not measure the log"
  expect_contains "$out" "which is U5's answer here (no)" "a row that did not measure the log"
  expect_contains "$out" "googleapi: Error 403: The caller does not have permission" "a row that did not measure the log"
}

test_a_member_with_no_drift_row_is_a_finding() {
  case_dir no_drift_row
  : >"$dir/no-drift-row"
  run721
  expect_status "$status" 1 "a member with no drift row"
  # The before-read is where this stops: a document that carries no drift row
  # carries none on the second read either, so the check says so before it
  # writes anything rather than after.
  expect_row "$out" status FAIL "a member with no drift row"
  expect_contains "$out" "carries no drift row" "a member with no drift row"
  expect_count "$dir/GCALLS" "add-iam-policy-binding" 0 "a member with no drift row"
}

test_a_status_that_is_not_a_document_stops_the_check() {
  case_dir status_bad_json
  : >"$dir/status-bad-json"
  run721
  expect_status "$status" 1 "a status that is not a document"
  expect_row "$out" status FAIL "a status that is not a document"
  expect_contains "$out" "did not print the document" "a status that is not a document"
  expect_count "$dir/GCALLS" "add-iam-policy-binding" 0 "a status that is not a document"
}

test_a_second_read_that_is_not_a_document_is_a_finding() {
  case_dir status_after_bad
  : >"$dir/status-after-bad-json"
  run721
  expect_status "$status" 1 "a second read that is not a document"
  expect_row "$out" names FAIL "a second read that is not a document"
  expect_contains "$out" "did not print the document on the second read" "a second read that is not a document"
  expect_row "$out" ungrant PASS "a second read that is not a document"
}

test_a_log_that_cannot_be_read_is_the_answer_to_u5() {
  case_dir log_unreadable
  printf '7' >"$dir/log-status"
  printf 'ERROR: (gcloud.logging.read) PERMISSION_DENIED: The caller does not have permission\n' >"$dir/log.err"
  run721
  expect_status "$status" 3 "a log that cannot be read"
  expect_row "$out" log UNVERIFIED "a log that cannot be read"
  expect_row "$out" names UNVERIFIED "a log that cannot be read"
  expect_contains "$out" "PERMISSION_DENIED: The caller does not have permission" "a log that cannot be read"
  expect_row "$out" ungrant PASS "a log that cannot be read"
}

test_a_log_answer_that_cannot_be_parsed_is_unverified() {
  case_dir log_unparsable
  printf 'this is not json\n' >"$dir/log.out"
  run721
  expect_status "$status" 3 "a log answer that cannot be parsed"
  expect_row "$out" log UNVERIFIED "a log answer that cannot be parsed"
  expect_contains "$out" "could not read the answer" "a log answer that cannot be parsed"
}

test_a_change_that_was_never_logged_is_a_finding() {
  case_dir log_empty
  : >"$dir/log-empty"
  run721 --wait 1
  expect_status "$status" 1 "a change that was never logged"
  expect_row "$out" entry FAIL "a change that was never logged"
  expect_contains "$out" "Admin Activity holds no SetIamPolicy entry for this check's own change" "a change that was never logged"
}

test_a_log_entry_with_no_rule_is_a_finding() {
  case_dir log_other_shape
  printf '[{"insertId":"chk721-x","timestamp":"2026-01-01T00:00:00.000000000Z","protoPayload":{"methodName":"SetIamPolicy","authenticationInfo":{"principalEmail":"someone@example.invalid"}}}]\n' >"$dir/log.out"
  run721 --wait 1
  expect_status "$status" 1 "a log entry with no rule"
  expect_row "$out" entry FAIL "a log entry with no rule"
}

test_an_update_that_failed_is_unverified() {
  case_dir update_failed
  printf '3' >"$dir/update-status"
  printf 'terraform: this apply could not be made\n' >"$dir/update.err"
  run721
  expect_status "$status" 3 "an update that failed"
  expect_row "$out" update UNVERIFIED "an update that failed"
  expect_contains "$out" "exited 3, so what it applied is not known" "an update that failed"
  expect_contains "$out" "terraform: this apply could not be made" "an update that failed"
  # Nothing of the update is in the log, so the second half could not be shown.
  expect_row "$out" names PASS "an update that failed"
  expect_row "$out" silent UNVERIFIED "an update that failed"
}

test_an_update_that_was_killed_at_its_deadline_says_so() {
  case_dir update_killed
  : >"$dir/update-sleeps"
  run721 --timeout 0
  expect_status "$status" 3 "an update that was killed"
  expect_row "$out" update UNVERIFIED "an update that was killed"
  expect_contains "$out" "did not finish inside 0 minutes and was killed" "an update that was killed"
}

test_a_member_whose_own_config_is_not_here_is_unverified() {
  case_dir no_membership
  : >"$dir/no-membership"
  run721
  expect_status "$status" 3 "a member whose own config is not here"
  expect_row "$out" update UNVERIFIED "a member whose own config is not here"
  expect_contains "$out" "this machine holds no config for the member chk721mem" "a member whose own config is not here"
  expect_count "$dir/CALLS" "fugaro update" 0 "a member whose own config is not here"
  expect_row "$out" silent UNVERIFIED "a member whose own config is not here"
}

test_no_update_skips_the_members_own_update() {
  case_dir no_update
  run721 --no-update
  expect_status "$status" 3 "no-update"
  expect_row "$out" update UNVERIFIED "no-update"
  expect_contains "$out" "the member's own update was skipped (--no-update)" "no-update"
  expect_count "$dir/CALLS" "fugaro update" 0 "no-update"
  expect_row "$out" names PASS "no-update"
}

test_an_empty_window_is_unverified() {
  case_dir empty_window
  # The member's update ran and wrote nothing Google logged: the window holds no
  # change of the member's own to show the row leaving out.
  : >"$dir/no-update-entry"
  run721
  expect_status "$status" 3 "an empty window"
  expect_row "$out" update PASS "an empty window"
  expect_row "$out" names PASS "an empty window"
  expect_row "$out" silent UNVERIFIED "an empty window"
  expect_contains "$out" "no SetIamPolicy change of the member's own update is in the window" "an empty window"
}

test_a_binding_google_refuses_is_a_finding() {
  case_dir grant_refused
  printf '1' >"$dir/grant-status"
  printf 'ERROR: Policy modification failed: googleapi: Error 403: The caller does not have permission\n' >"$dir/grant.err"
  run721
  expect_status "$status" 1 "a binding Google refuses"
  expect_row "$out" grant FAIL "a binding Google refuses"
  expect_contains "$out" "googleapi: Error 403" "a binding Google refuses"
  expect_count "$dir/GCALLS" "logging read" 0 "a binding Google refuses"
}

test_grantee_and_role_are_the_ones_you_named() {
  case_dir named_grantee
  run721 --grantee person@corp.example.com --role roles/editor
  expect_status "$status" 0 "grantee and role"
  expect_match "$(cat "$dir/GCALLS")" 'add-iam-policy-binding chk721mem --member=user:person@corp\.example\.com --role=roles/editor' "grantee and role"
  expect_contains "$out" "changed in the console: person@corp.example.com as roles/editor on chk721mem (chk721-grant-1)" "grantee and role"
  expect_match "$(cat "$dir/GCALLS")" 'remove-iam-policy-binding chk721mem --member=user:person@corp\.example\.com --role=roles/editor' "grantee and role"
}

test_the_window_and_the_filter_are_the_ones_named() {
  case_dir window
  run721 --since 5
  expect_status "$status" 0 "the window"
  expect_contains "$out" "Admin Activity from " "the window"
  expect_match "$(cat "$dir/GCALLS")" 'logging read logName="projects/chk721mem/logs/cloudaudit\.googleapis\.com%2Factivity" AND protoPayload\.methodName="SetIamPolicy" AND timestamp>=' "the window"
}

test_the_member_project_can_be_named() {
  case_dir named_project
  run721 --member-project chk721-prod
  expect_status "$status" 0 "the member project can be named"
  expect_contains "$out" "the member's GCP project is chk721-prod" "the member project can be named"
  expect_match "$(cat "$dir/GCALLS")" 'add-iam-policy-binding chk721-prod' "the member project can be named"
  expect_contains "$out" "iam-admin/iam?project=chk721-prod" "the member project can be named"
}

test_a_member_with_no_job_account_needs_the_project_named() {
  case_dir no_job_account
  # The fake's member table answers with a job account unless JOB_ACCOUNT is set,
  # and an empty one is a member whose GCP project cannot be read from it.
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE721_DIR="$dir" FAKE721_LOG="$dir/CALLS" FAKE721_GLOG="$dir/GCALLS" \
    JOB_ACCOUNT="" bash "$script" --workspace chk721ws --project chk721mem 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a member with no job account"
  expect_contains "$err" "names no job service account" "a member with no job account"
  expect_contains "$err" "pass --member-project" "a member with no job account"
}

test_a_description_google_refuses_is_a_finding() {
  case_dir describe_refused
  printf '3' >"$dir/describe-status"
  printf 'ERROR: (gcloud.projects.describe) PERMISSION_DENIED\n' >"$dir/describe.err"
  run721
  expect_status "$status" 1 "a description Google refuses"
  expect_row "$out" member FAIL "a description Google refuses"
  expect_contains "$out" "PERMISSION_DENIED" "a description Google refuses"
  expect_count "$dir/GCALLS" "add-iam-policy-binding" 0 "a description Google refuses"
}

test_a_restore_that_fails_is_a_finding() {
  case_dir ungrant_refused
  printf '1' >"$dir/ungrant-status"
  printf 'ERROR: (gcloud.projects.remove-iam-policy-binding) PERMISSION_DENIED\n' >"$dir/ungrant.err"
  run721
  expect_status "$status" 1 "a restore that fails"
  expect_row "$out" ungrant FAIL "a restore that fails"
  expect_contains "$out" "STILL THERE" "a restore that fails"
  expect_contains "$out" "remove it yourself with" "a restore that fails"
}

test_keep_leaves_the_binding_and_says_so() {
  case_dir keep
  run721 --keep
  expect_status "$status" 3 "keep"
  expect_row "$out" ungrant UNVERIFIED "keep"
  expect_contains "$out" "because --keep was given" "keep"
  expect_count "$dir/GCALLS" "remove-iam-policy-binding" 0 "keep"
  expect_row "$out" names PASS "keep"
}

test_an_unauthenticated_gcloud_stops_the_check() {
  case_dir no_account
  printf '1' >"$dir/auth-status"
  run721
  expect_status "$status" 2 "an unauthenticated gcloud"
  expect_contains "$err" "gcloud auth login" "an unauthenticated gcloud"
  expect_count "$dir/GCALLS" "add-iam-policy-binding" 0 "an unauthenticated gcloud"
}

test_credentials_without_application_default_stop_the_check() {
  case_dir no_adc
  printf '1' >"$dir/adc-status"
  run721
  expect_status "$status" 2 "credentials without Application Default"
  expect_contains "$err" "Application Default Credentials do not answer" "credentials without Application Default"
  expect_contains "$err" "gcloud auth application-default login" "credentials without Application Default"
}

test_a_workspace_this_machine_does_not_know_stops_the_check() {
  case_dir no_workspace
  run721 --workspace chk721-other
  expect_status "$status" 2 "a workspace this machine does not know"
  expect_contains "$err" "no workspace chk721-other is known to this machine" "a workspace this machine does not know"
}

test_a_workspace_whose_own_config_is_not_here_stops_the_check() {
  case_dir not_the_owner
  : >"$dir/no-owner"
  run721
  expect_status "$status" 2 "a workspace whose own config is not here"
  expect_contains "$err" "holds no config for the workspace chk721ws's own project" "a workspace whose own config is not here"
}

test_a_show_that_is_not_the_document_stops_the_check() {
  case_dir show_bad_json
  printf '2' >"$dir/show-status"
  run721
  expect_status "$status" 2 "a show that is not the document"
  expect_contains "$err" "fugaro workspace show chk721ws --json" "a show that is not the document"
}

test_a_member_the_table_does_not_list_stops_the_check() {
  case_dir not_a_member
  run721 --project chk721-nobody
  expect_status "$status" 2 "a member the table does not list"
  expect_contains "$err" "member table does not list chk721-nobody" "a member the table does not list"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE721_DIR="$dir" FAKE721_LOG="$dir/CALLS" \
    FAKE721_GLOG="$dir/GCALLS" bash "$script" --workspace chk721ws --project chk721mem 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  expect_contains "$out" "no agent runs Check 721" "an agent session"
  [ ! -s "$dir/CALLS" ] || fail "an agent session: it called fugaro: $(head -c 400 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "an agent session: it called gcloud: $(head -c 400 "$dir/GCALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE721_DIR="$dir" FAKE721_LOG="$dir/CALLS" \
    FAKE721_GLOG="$dir/GCALLS" bash -x "$script" --workspace chk721ws --project chk721mem 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_help_prints_the_usage_and_calls_nothing() {
  case_dir help
  status=0
  out=$(PATH="$fakebin:$PATH" FAKE721_DIR="$dir" FAKE721_LOG="$dir/CALLS" FAKE721_GLOG="$dir/GCALLS" \
    bash "$script" --help 2>&1) || status=$?
  expect_status "$status" 0 "help"
  expect_contains "$out" "Run by YOU; no agent runs any step of it" "help"
  expect_contains "$out" "--workspace <name>" "help"
  expect_contains "$out" "--no-update" "help"
  [ ! -s "$dir/CALLS" ] || fail "help: it called fugaro"
  [ ! -s "$dir/GCALLS" ] || fail "help: it called gcloud"
}

test_a_dry_run_prints_the_plan_and_calls_nothing() {
  case_dir dry_run
  run721 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$err" "Check 721 plan" "a dry run"
  expect_contains "$err" "fugaro workspace project status chk721mem --project chk721ws --json" "a dry run"
  expect_contains "$err" "run=yes (the member's own" "a dry run"
  [ ! -s "$dir/CALLS" ] || fail "a dry run: it called fugaro: $(head -c 400 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "a dry run: it called gcloud: $(head -c 400 "$dir/GCALLS")"
  expect_not_contains "$out" "CHECK 721 RESULT" "a dry run"
}

test_a_missing_workspace_is_refused() {
  case_dir no_workspace_flag
  run721_raw --project chk721mem
  expect_status "$status" 2 "a missing --workspace"
  expect_contains "$err" "no --workspace was given" "a missing --workspace"
}

test_a_missing_project_is_refused() {
  case_dir no_project_flag
  run721_raw --workspace chk721ws
  expect_status "$status" 2 "a missing --project"
  expect_contains "$err" "no --project was given" "a missing --project"
}

test_a_number_that_is_not_a_number_is_refused() {
  case_dir bad_number
  run721 --since soon
  expect_status "$status" 2 "a number that is not a number"
  expect_contains "$err" "soon is not a number" "a number that is not a number"
}

test_a_window_of_zero_is_refused() {
  case_dir zero_window
  run721 --since 0
  expect_status "$status" 2 "a window of zero"
  expect_contains "$err" "--since 0 is not a window" "a window of zero"
}

test_a_role_that_is_not_a_role_is_refused() {
  case_dir bad_role
  run721 --role viewer
  expect_status "$status" 2 "a role that is not a role"
  expect_contains "$err" "is not an IAM role name" "a role that is not a role"
}

test_a_grantee_that_is_not_an_address_is_refused() {
  case_dir bad_grantee
  run721 --grantee nobody
  expect_status "$status" 2 "a grantee that is not an address"
  expect_contains "$err" "is not an address" "a grantee that is not an address"
}

test_a_directory_you_named_is_left_as_it_is() {
  case_dir caller_dir
  mkdir -p "$work/caller_dir/kept"
  run721 --dir "$work/caller_dir/kept"
  expect_status "$status" 0 "a directory you named"
  expect_row "$out" restore PASS "a directory you named"
  expect_contains "$out" "the directory you named with --dir was left" "a directory you named"
  [ -d "$work/caller_dir/kept" ] || fail "a directory you named: it was removed"
}

test_a_directory_that_is_not_there_is_refused() {
  case_dir caller_dir_missing
  run721_raw --workspace chk721ws --project chk721mem --dir "$work/no-such-dir"
  expect_status "$status" 2 "a directory that is not there"
  expect_contains "$err" "is not a directory" "a directory that is not there"
}

test_an_unknown_argument_is_refused() {
  case_dir unknown_argument
  run721_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK721_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK721_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_a_row_that_names_nothing_is_a_finding
t test_a_row_that_names_the_update_is_a_finding
t test_a_row_that_names_somebody_elses_change_still_passes
t test_a_row_that_names_another_role_is_a_finding
t test_a_row_that_names_another_entry_is_a_finding
t test_a_row_that_names_another_project_is_a_finding
t test_a_row_with_no_findings_is_a_finding
t test_a_row_that_did_not_measure_the_log_is_the_answer_to_u5
t test_a_member_with_no_drift_row_is_a_finding
t test_a_status_that_is_not_a_document_stops_the_check
t test_a_second_read_that_is_not_a_document_is_a_finding
t test_a_log_that_cannot_be_read_is_the_answer_to_u5
t test_a_log_answer_that_cannot_be_parsed_is_unverified
t test_a_change_that_was_never_logged_is_a_finding
t test_a_log_entry_with_no_rule_is_a_finding
t test_an_update_that_failed_is_unverified
t test_an_update_that_was_killed_at_its_deadline_says_so
t test_a_member_whose_own_config_is_not_here_is_unverified
t test_no_update_skips_the_members_own_update
t test_an_empty_window_is_unverified
t test_a_binding_google_refuses_is_a_finding
t test_grantee_and_role_are_the_ones_you_named
t test_the_window_and_the_filter_are_the_ones_named
t test_the_member_project_can_be_named
t test_a_member_with_no_job_account_needs_the_project_named
t test_a_description_google_refuses_is_a_finding
t test_a_restore_that_fails_is_a_finding
t test_keep_leaves_the_binding_and_says_so
t test_an_unauthenticated_gcloud_stops_the_check
t test_credentials_without_application_default_stop_the_check
t test_a_workspace_this_machine_does_not_know_stops_the_check
t test_a_workspace_whose_own_config_is_not_here_stops_the_check
t test_a_show_that_is_not_the_document_stops_the_check
t test_a_member_the_table_does_not_list_stops_the_check
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_help_prints_the_usage_and_calls_nothing
t test_a_dry_run_prints_the_plan_and_calls_nothing
t test_a_missing_workspace_is_refused
t test_a_missing_project_is_refused
t test_a_number_that_is_not_a_number_is_refused
t test_a_window_of_zero_is_refused
t test_a_role_that_is_not_a_role_is_refused
t test_a_grantee_that_is_not_an_address_is_refused
t test_a_directory_you_named_is_left_as_it_is
t test_a_directory_that_is_not_there_is_refused
t test_an_unknown_argument_is_refused

echo
if [ "$failures" -eq 0 ]; then
  echo "check-721_test.sh: ok"
else
  echo "check-721_test.sh: $failures failure(s)" >&2
  exit 1
fi
