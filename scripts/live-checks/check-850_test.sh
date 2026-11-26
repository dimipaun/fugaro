#!/usr/bin/env bash
# Tests for scripts/live-checks/check-850.sh. NOTHING here is a cloud, a project,
# an installation, a run or a token: every reading the check makes is a fixture
# in a temporary directory — the person's own calls through a fake fugaro, and
# the admin-side policies, the one accessor read and the service's audit lines
# off the files the owner is told to save — and the cases assert what the script
# printed and what it refused.
#
# The bends are U5, U6, U10 and the refusals R1 to R3 promise. A project policy
# that names the person is a person who is admitted for another reason; a store
# that answers and leaves nothing is worse than one that refuses; a config that
# loads with no project layer is the person reading their own file and not the
# workspace's; a switch written on a disk and never published reaches no run; a
# log with no line for the run is an unread log and NOT an empty one; a halt
# without the remedy's own command leaves a person with a stopped run and
# nothing to do; an unconditioned accessor over the workspace's project is the
# boundary D3141 draws broken. And the refusals themselves: no person named,
# another person's gcloud account, an agent's session, an installation that is
# not this one, and the rows the check has to be honest about not measuring.
#
# This test is what found the check's own defects: `settings get` cannot see the
# published node (its row for the switch is a local-config row, so its `wins` is
# a file or the default), and a run the service wrote no line about read PASS.
#
# Usage: bash scripts/live-checks/check-850_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-850.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK850_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK850_TEST_HEAD:-9000}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK850_TEST_HEAD:-9000}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 800))"
}

[ -f "$script" ] || {
  echo "check-850_test.sh: the script $script does not exist" >&2
  exit 1
}

fx=$work/fx
save=$work/save
mkdir -p "$work/bin" "$fx" "$save"

# ---------------------------------------------------------------- the fakes
write_fake_fugaro() {
  cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode=${FAKE_MODE:-ok}
fx=${FAKE_FX:?}
token=${FAKE_TOKEN_NAME:-claude-oauth-token}

emit_listing() {
  if [ "$mode" = no-listing-after-set ]; then printf '{"secrets":[]}'; return 0; fi
  if [ -f "$fx/stored" ]; then
    printf '{"secrets":[{"id":"fugaro_u_1234567890_%s","name":"%s","scope":"user","created":"2026-11-26T00:00:00Z"}]}' \
      "$token" "$token"
  else
    printf '{"secrets":[]}'
  fi
}

project_of() {
  local p=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --project) p=${2:-}; shift 2 ;;
    *) shift ;;
    esac
  done
  printf '%s' "$p"
}

cmd=${1:-}
shift || true
case "$cmd" in
version) echo "fugaro 9.9.9-test" ;;
doctor)
  project=fugaro
  while [ $# -gt 0 ]; do
    case "$1" in
    --project) project=${2:-fugaro}; shift 2 ;;
    *) shift ;;
    esac
  done
  if [ "$mode" = other-installation ]; then echo "project: $project (GCP other-prod, us-central1)"; exit 0; fi
  echo "project: $project (GCP fugaro-dev, us-east5)"
  ;;
secrets)
  [ "${1:-}" = user ] || { echo "fake: unknown secrets subcommand" >&2; exit 2; }
  shift
  sub=${1:-}
  shift || true
  project=$(project_of "$@")
  case "$sub" in
  ls)
    if [ "$mode" = no-ls ]; then echo "fake: the intake refused the listing" >&2; exit 1; fi
    emit_listing
    ;;
  set)
    if [ "$mode" = set-refused ]; then echo "fake: the intake refused the store" >&2; exit 1; fi
    cat >"$fx/token-value"
    : >"$fx/stored"
    ;;
  rm)
    if [ "$mode" = rm-refused ]; then echo "fake: the removal was refused" >&2; exit 1; fi
    rm -f "$fx/stored"
    ;;
  *) echo "fake: unknown secrets user $sub" >&2; exit 2 ;;
  esac
  ;;
config)
  if [ "$mode" = config-refused ]; then echo "fake: no config is selected for that project" >&2; exit 1; fi
  if [ "$mode" = config-no-layer ]; then echo '{"where":"","sha256":"","generation":""}'; exit 0; fi
  echo '{"where":"gs://fugaro-runs/policy/shared.json","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","generation":"7"}'
  ;;
project)
  if [ "${1:-}" = policy ] && [ "${2:-}" = show ]; then
    shift 2 || true
    project=$(project_of "$@")
    if [ "$mode" = no-policy ]; then echo "fake: the policy could not be read" >&2; exit 1; fi
    if [ ! -r "$fx/policy-$project.json" ]; then echo "fake: no policy for $project" >&2; exit 1; fi
    cat "$fx/policy-$project.json"
    exit 0
  fi
  echo "fake fugaro: unknown invocation project $*" >&2
  exit 2
  ;;
runs)
  project=$(project_of "$@")
  if [ "$mode" = no-runs ]; then echo "fake: the run listing could not be read" >&2; exit 1; fi
  if [ ! -r "$fx/runs-$project.json" ]; then echo "fake: no run listing for $project" >&2; exit 1; fi
  cat "$fx/runs-$project.json"
  ;;
*) echo "fake fugaro: unknown invocation $cmd $*" >&2; exit 2 ;;
esac
SH
  chmod +x "$work/bin/fugaro"
}
write_fake_fugaro

cat >"$work/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
args="$*"
case "$args" in
*"config get-value account"*) echo "${FAKE_ACCOUNT:-person@example.com}" ;;
*) echo "fake gcloud: unknown invocation $args" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/gcloud"

# ---------------------------------------------------------------- the fixtures
person=person@example.com
account=ws-sa@ws.iam.gserviceaccount.com
m_a=m-a
m_b=m-b
m_off=m-off
run_a=20261120-000000-aaaa
run_b=20261120-000000-bbbb
other_run=20261120-000000-cccc
off_run=20261120-000000-dddd
deleted_run=20261120-000000-eeee
removed_run=20261120-000000-ffff

# iam_project_json WHO: the workspace's PROJECT policy. `nobody` names only the
# owner; `person` names the person in a project-level binding, which is a person
# U6's "nothing but the two invoke grants" is not about.
iam_project_json() { # iam_project_json nobody|person
  if [ "$1" = person ]; then
    printf '{"bindings":[{"role":"roles/viewer","members":["user:owner@example.com","user:%s"]}]}' \
      "$person" >"$save/iam-project.json"
  else
    printf '{"bindings":[{"role":"roles/owner","members":["user:owner@example.com"]}]}' \
      >"$save/iam-project.json"
  fi
}

# service_iam_json WHICH holds|nobody: one of the two services' policies.
service_iam_json() { # service_iam_json FILE holds|nobody
  if [ "$2" = holds ]; then
    printf '{"bindings":[{"role":"roles/run.invoker","members":["user:%s"]}]}' \
      "$person" >"$save/$1"
  else
    printf '{"bindings":[{"role":"roles/run.invoker","members":["user:someone@example.com"]}]}' \
      >"$save/$1"
  fi
}

# accessor_json conditioned|plain: the person's container's own policy. `plain`
# is the accessor the prefix rule forbids: the workspace's account reads the
# container with nothing limiting it to the fugaro_u_ prefix.
accessor_json() { # accessor_json conditioned|plain
  if [ "$1" = conditioned ]; then
    printf '{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:%s"],"condition":{"title":"personal only","expression":"resource.name.startsWith(\\"projects/ws/secrets/fugaro_u_\\")"}}]}' \
      "$account" >"$save/secret-accessor.json"
  else
    printf '{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:%s"]}]}' \
      "$account" >"$save/secret-accessor.json"
  fi
}

# policy_json PROJECT true|false|none|unpublished: the member's PUBLISHED node.
policy_json() { # policy_json PROJECT VALUE
  case "$2" in
  unpublished) printf '{"project":"%s","policy":null}' "$1" >"$fx/policy-$1.json" ;;
  none) printf '{"project":"%s","policy":{"version":1}}' "$1" >"$fx/policy-$1.json" ;;
  *) printf '{"project":"%s","policy":{"version":1,"personal_workspace_tokens":%s}}' \
    "$1" "$2" >"$fx/policy-$1.json" ;;
  esac
}

# audit_json RUN:PERSONAL...: the service's own lines the owner saved, in one
# file for the whole workspace. The credential is a NAME, never a value.
audit_json() { # audit_json RUN:PERSONAL ...
  python3 - "$save/audit.json" "$@" <<'PY'
import json
import sys
path, specs = sys.argv[1], sys.argv[2:]
rows = []
for spec in specs:
    fields = (spec.split(":") + ["no"])[:2]
    run, personal = fields
    rows.append({"jsonPayload": {"msg": "credsvc: audit", "run": run,
                                 "credential": "shared:claude-oauth-token",
                                 "status": "ok", "tier": "shared", "purpose": "run"}})
    if personal == "yes":
        rows.append({"jsonPayload": {"msg": "credsvc: audit", "run": run,
                                     "credential": "fugaro_u_1234567890_claude-oauth-token",
                                     "status": "ok", "tier": "A", "purpose": "run"}})
with open(path, "w") as fh:
    json.dump(rows, fh)
PY
}

# runs_json DELETED_DETAIL REMOVED_MARKER REMOVED_AT_EPOCH: the six runs the
# staging describes, in the first member's listing; the second member's is
# empty, so the lookup has to walk both.
runs_json() { # runs_json DELETED_DETAIL REMOVED_MARKER REMOVED_AT
  python3 - "$fx/runs-$m_a.json" "$deleted_run" "$1" "$2" "$3" <<'PY'
import datetime
import json
import sys
path, deleted_run, deleted_detail, removed_marker, removed_at = sys.argv[1:6]

def row(run_id, halt_reason="", detail="", at="", marker=""):
    out = {"run_id": run_id, "status": "halted" if halt_reason else "done",
           "terminal": True, "outcome": "" if halt_reason else "ready"}
    if halt_reason:
        out["halt"] = {"reason": halt_reason, "scope": "run", "at": at,
                       "detail": detail, "workspace_refusal": marker}
    return out

def iso(epoch):
    if not epoch:
        return ""
    return datetime.datetime.fromtimestamp(int(epoch), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

runs = [row("20261120-000000-aaaa"), row("20261120-000000-bbbb"),
        row("20261120-000000-cccc"), row("20261120-000000-dddd")]
# `-` is the row with a halt but nothing written in its detail: the run R3's
# refusal leaves a person with a stopped run and nothing to read.
if deleted_detail:
    runs.append(row(deleted_run, "credentials_unavailable",
                    "" if deleted_detail == "-" else deleted_detail,
                    iso(int(removed_at or 0) + 30)))
if removed_marker:
    runs.append(row("20261120-000000-ffff", "workspace_refused", removed_marker,
                    iso(int(removed_at or 0) + 30), removed_marker))
doc = {"version": 1, "project": "fugaro", "runs": runs, "totals": {}}
with open(path, "w") as fh:
    json.dump(doc, fh)
PY
  printf '{"version":1,"project":"fugaro","runs":[],"totals":{}}' >"$fx/runs-$m_b.json"
}

save_ok() {
  iam_project_json nobody
  service_iam_json iam-intake.json holds
  service_iam_json iam-creds.json holds
  accessor_json conditioned
  printf 'ok' >"$save/accessor-read"
  audit_json "$run_a:yes" "$run_b:yes" "$other_run:no" "$off_run:no" "$deleted_run:no" "$removed_run:no"
}

fx_ok() {
  policy_json "$m_a" true
  policy_json "$m_b" true
  policy_json "$m_off" none
  runs_json "the personal token this call's slip names is not there: a person sets one at the workspace with fugaro secrets user set" slip_owner_removed "$(( $(date -u +%s) - 60 ))"
  rm -f "$fx/stored"
}

token_file=$work/token
printf 'a-test-token-value' >"$token_file"

all_options=(
  CHECK850_PERSON=$person
  CHECK850_WORKSPACE_PROJECT=ws
  CHECK850_MEMBER_A=$m_a
  CHECK850_MEMBER_B=$m_b
  CHECK850_MEMBER_OFF=$m_off
  CHECK850_WORKSPACE_ACCOUNT=$account
  CHECK850_SAVE_DIR=$save
  CHECK850_TOKEN_FILE=$token_file
  CHECK850_RUN_A=aaaa
  CHECK850_RUN_B=bbbb
  CHECK850_OTHER_RUN=cccc
  CHECK850_OFF_RUN=dddd
  CHECK850_DELETED_RUN=eeee
  CHECK850_REMOVED_RUN=ffff
  CHECK850_REMOVED_AT=$(( $(date -u +%s) - 60 ))
)

run850() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" FAKE_MODE="${FAKE_MODE:-ok}" FAKE_FX="$fx" TMPDIR="$work" \
    FAKE_ACCOUNT="${FAKE_ACCOUNT:-$person}" \
    ${RUN850_ENV[@]+"${RUN850_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  CHECK850_TEST_ENV=()
}

run_check() {
  RUN850_ENV=("${all_options[@]}" ${CHECK850_TEST_ENV[@]+"${CHECK850_TEST_ENV[@]}"})
  run850 "$@"
}

# run_check_without VAR: every knob but one, so a row that needs it reads
# unmeasured. The element is dropped rather than blanked: `env` refuses an empty
# argument, and a knob set to the empty string is not the same reading as one
# nobody set.
run_check_without() { # run_check_without VAR
  # The kept knobs are NOT held in a variable named `out`: run850 assigns the
  # run's stdout to `out`, and a local of that name here would take that
  # assignment by dynamic scope and leave the caller's assertion unbound.
  local drop=$1 one kept=()
  for one in "${all_options[@]}"; do
    case "$one" in "$drop"=*) continue ;; esac
    kept+=("$one")
  done
  RUN850_ENV=("${kept[@]}")
  run850
}

# ------------------------------------------------------------------- the cases
test_help() {
  run850 --help
  expect_status "$status" 0 "check-850.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "CHECK850_SAVE_DIR" "--help names the admin-side directory"
  expect_contains "$out" "iam-project.json" "--help names the files to save"
}

test_dry_run_measures_nothing() {
  save_ok
  fx_ok
  run_check --dry-run
  expect_status "$status" 0 "--dry-run is a plan and nothing else"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run says it ran nothing"
  expect_contains "$out" "FACT token-stored: INFO" "a planned row reads INFO, not PASS"
  expect_contains "$err" "fugaro secrets user set claude-oauth-token --project ws" "the plan names the write it would make"
  expect_contains "$err" "network, WRITE" "the plan says which step writes"
}

test_the_whole_staging_holds() {
  save_ok
  fx_ok
  run_check
  expect_status "$status" 0 "every measured criterion held"
  expect_contains "$out" "FACT dry-run: INFO" "a real run is not a dry run"
  expect_contains "$out" "FACT person-identity: PASS" "the person's own account was in use"
  expect_contains "$out" "FACT no-project-role: PASS" "the project names the person in no binding"
  expect_contains "$out" "FACT person-invoke-intake: PASS" "the person holds the intake's invoker role"
  expect_contains "$out" "FACT person-invoke-creds: PASS" "and the credentials service's"
  expect_contains "$out" "FACT intake-admits: PASS" "the platform admitted the person's own call"
  expect_contains "$out" "FACT token-stored: PASS" "the store put the container there"
  expect_contains "$out" "FACT config-named: PASS" "the project's config loaded by naming it"
  expect_contains "$out" "FACT switch-on-a: PASS" "the first member's node reads true"
  expect_contains "$out" "FACT switch-on-b: PASS" "and the second's"
  expect_contains "$out" "FACT served-a: PASS" "the first member's run was served the person's container"
  expect_contains "$out" "FACT served-b: PASS" "and the second's"
  expect_contains "$out" "FACT other-run: PASS" "a second person's run was not"
  expect_contains "$out" "FACT switch-off: PASS" "the member left alone reads off"
  expect_contains "$out" "FACT off-run: PASS" "and its run was served no personal container"
  expect_contains "$out" "FACT accessor-conditioned: PASS" "the accessor is limited to the prefix"
  expect_contains "$out" "FACT accessor-reads: PASS" "and the workspace's account made the read"
  expect_contains "$out" "FACT deleted-token: PASS" "the halt names the command that puts a token back"
  expect_contains "$out" "FACT removed-refusal: PASS" "the removed person's run was refused in the window"
  expect_contains "$out" "FACT restore: PASS" "the token was removed and the removal read back"
  expect_contains "$out" "FACT scratch-removed: PASS" "the scratch directory is gone"
  expect_absent "$out" "FAIL" "a clean run's block carries no FAIL row"
}

test_no_person_named_is_refused() {
  save_ok
  fx_ok
  RUN850_ENV=(CHECK850_WORKSPACE_PROJECT=ws)
  run850
  expect_status "$status" 1 "the person under test is who the check is about"
  expect_contains "$err" "CHECK850_PERSON is not set" "the refusal names the knob"
  expect_contains "$out" "FACT person-identity: FAIL — not reached" "nothing about a person was measured"
}

test_another_account_is_refused() {
  save_ok
  fx_ok
  FAKE_ACCOUNT=someone-else@example.com
  run_check
  expect_status "$status" 1 "a check run under another account measures the wrong person"
  expect_contains "$err" "gcloud account is someone-else@example.com" "the refusal says what it read"
  expect_contains "$err" "U6 and U10 are about what that person can do" "and why it matters"
  expect_absent "$out" "FACT intake-admits: PASS" "nothing was read before the refusal"
}

test_a_project_role_fails_the_u6_row() {
  save_ok
  fx_ok
  iam_project_json person
  run_check
  expect_status "$status" 1 "a project role is a person admitted for another reason"
  expect_contains "$out" "FACT no-project-role: FAIL" "the project-policy row is the finding"
  expect_contains "$out" "$person" "the row names the binding it read"
  expect_contains "$out" "a person with a project role is admitted for reasons other than the roster" "the row says why it matters"
}

test_missing_saved_policies_are_unmeasured() {
  save_ok
  fx_ok
  rm -f "$save/iam-project.json" "$save/iam-intake.json" "$save/iam-creds.json"
  run_check
  expect_status "$status" 2 "a reading the person cannot make is not a reading"
  expect_contains "$out" "FACT no-project-role: FAIL" "the row reads FAIL"
  expect_contains "$out" "needs an admin's credential" "the row says why it cannot be measured here"
  expect_contains "$out" "gcloud projects get-iam-policy ws --format=json" "the row names the command that saves it"
  expect_contains "$out" "FACT person-invoke-intake: FAIL" "and so do the two service rows"
}

test_a_service_without_the_invoke_grant_fails() {
  save_ok
  fx_ok
  service_iam_json iam-intake.json nobody
  run_check
  expect_status "$status" 1 "a service the person is not admitted to is not their access"
  expect_contains "$out" "FACT person-invoke-intake: FAIL" "the intake's policy row is the finding"
  expect_contains "$out" "names $person in no roles/run.invoker binding" "the row says what it read"
  expect_contains "$out" "FACT person-invoke-creds: PASS" "the other service is untouched"
}

test_the_intakes_refusal_fails() {
  save_ok
  fx_ok
  FAKE_MODE=no-ls
  run_check
  expect_status "$status" 1 "the person's own call is the row the check exists for"
  expect_contains "$out" "FACT intake-admits: FAIL" "the admission row is the finding"
  expect_contains "$out" "the person's own call to the workspace's intake was refused" "the row says what it read"
  expect_contains "$out" "the intake refused the listing" "and quotes the refusal"
}

test_a_refused_store_fails() {
  save_ok
  fx_ok
  FAKE_MODE=set-refused
  run_check
  expect_status "$status" 1 "U6's own act is the store, and it did not happen"
  expect_contains "$out" "FACT token-stored: FAIL" "the store row is the finding"
  expect_contains "$out" "storing the token was refused" "the row says what happened"
}

test_a_store_that_leaves_nothing_fails() {
  save_ok
  fx_ok
  FAKE_MODE=no-listing-after-set
  run_check
  expect_status "$status" 1 "a store that answers and leaves nothing is worse than one that refuses"
  expect_contains "$out" "FACT token-stored: FAIL" "the store row is the finding"
  expect_contains "$out" "the listing does not name a claude-oauth-token under the person's own prefix" "the row says what it read back"
}

test_a_token_file_nobody_named_is_unmeasured() {
  save_ok
  fx_ok
  run_check_without CHECK850_TOKEN_FILE
  expect_status "$status" 2 "the check will not invent a token"
  expect_contains "$out" "FACT token-stored: FAIL" "the row reads FAIL"
  expect_contains "$out" "CHECK850_TOKEN_FILE was not set" "the row names the knob"
}

test_an_empty_token_file_is_unmeasured() {
  save_ok
  fx_ok
  : >"$work/empty-token"
  CHECK850_TEST_ENV=(CHECK850_TOKEN_FILE="$work/empty-token")
  run_check
  expect_status "$status" 2 "an empty value is not a token"
  expect_contains "$out" "is empty, and an empty value is not a token" "the row says what it read"
}

test_a_config_that_does_not_load_fails() {
  save_ok
  fx_ok
  FAKE_MODE=config-refused
  run_check
  expect_status "$status" 1 "U10 is the project's config loading, and it did not"
  expect_contains "$out" "FACT config-named: FAIL" "the config row is the finding"
  expect_contains "$out" "the workspace's config did not load with the project named" "the row says what it read"
  expect_contains "$out" "ask the workspace's owner for it" "the row names the remedy"
}

test_a_config_with_no_published_layer_fails() {
  save_ok
  fx_ok
  FAKE_MODE=config-no-layer
  run_check
  expect_status "$status" 1 "the published layer is the read U10 is about"
  expect_contains "$out" "FACT config-named: FAIL" "the config row is the finding"
  expect_contains "$out" "the person can read their own file and not the project's published layer" "the row says what it read"
}

test_the_workspace_config_knob_is_used() {
  save_ok
  fx_ok
  CHECK850_TEST_ENV=(CHECK850_WS_CONFIG="$work/ws.yaml")
  run_check
  expect_contains "$out" "FACT config-named: PASS" "the config loaded with the file named"
  expect_contains "$out" "with FUGARO_CONFIG=$work/ws.yaml" "the row says which knob was used"
}

test_a_switch_that_is_not_on_fails() {
  save_ok
  fx_ok
  policy_json "$m_a" none
  run_check
  expect_status "$status" 1 "a member whose node reads no true serves nothing personal"
  expect_contains "$out" "FACT switch-on-a: FAIL" "the first member's switch row is the finding"
  expect_contains "$out" "reads personal_workspace_tokens=absent" "the row quotes the node"
  expect_contains "$out" "FACT switch-on-b: PASS" "the second member is untouched"
}

test_a_member_left_alone_that_reads_true_fails() {
  save_ok
  fx_ok
  policy_json "$m_off" true
  run_check
  expect_status "$status" 1 "a member nobody turned on must not serve the personal half"
  expect_contains "$out" "FACT switch-off: FAIL" "the switch-off row is the finding"
  expect_contains "$out" "a member left alone (never turned on) has to serve the shared entries and nothing personal" "the row names the rule"
}

test_a_run_not_served_the_token_fails() {
  save_ok
  fx_ok
  audit_json "$run_a:no" "$run_b:yes" "$other_run:no" "$off_run:no" "$deleted_run:no" "$removed_run:no"
  run_check
  expect_status "$status" 1 "a switch-on member's run has to be served the person's container"
  expect_contains "$out" "FACT served-a: FAIL" "the served row is the finding"
  expect_contains "$out" "was served NO personal container" "the row says what it read"
  expect_contains "$out" "the personal route is not wired" "the row names what to look at"
}

test_a_second_persons_run_served_it_fails() {
  save_ok
  fx_ok
  audit_json "$run_a:yes" "$run_b:yes" "$other_run:yes" "$off_run:no" "$deleted_run:no" "$removed_run:no"
  run_check
  expect_status "$status" 1 "a run that is not the person's is never served their token"
  expect_contains "$out" "FACT other-run: FAIL" "the other-run row is the finding"
  expect_contains "$out" "and it must not be" "the row says what it read"
}

test_a_log_with_no_line_for_the_run_is_unmeasured() {
  save_ok
  fx_ok
  audit_json "$run_a:yes" "$run_b:yes" "$other_run:no" "$deleted_run:no" "$removed_run:no"
  run_check
  expect_status "$status" 2 "an unread log is not an empty one"
  expect_contains "$out" "FACT off-run: FAIL" "the row reads FAIL"
  expect_contains "$out" "hold no 'credsvc: audit' entry for the run" "the row says what it looked for"
}

test_a_halt_without_the_remedy_fails() {
  save_ok
  fx_ok
  runs_json "the workspace's credentials service could not be reached" slip_owner_removed "$(( $(date -u +%s) - 60 ))"
  run_check
  expect_status "$status" 1 "the refusal a deleted token meets has to name the command that puts one back"
  expect_contains "$out" "FACT deleted-token: FAIL" "the halt row is the finding"
  expect_contains "$out" "does not name fugaro secrets user set" "the row says what is missing"
}

test_a_run_halted_with_no_detail_fails() {
  save_ok
  fx_ok
  runs_json "-" slip_owner_removed "$(( $(date -u +%s) - 60 ))"
  run_check
  expect_status "$status" 1 "a run stopped with nothing to do about it is the finding"
  expect_contains "$out" "FACT deleted-token: FAIL" "the halt row is the finding"
  expect_contains "$out" "carries no detail at all" "the row says what it read"
  expect_contains "$out" "FACT removed-refusal: PASS" "the other halt is untouched"
}

test_a_removal_outside_the_window_fails() {
  save_ok
  fx_ok
  runs_json "the personal token this call's slip names is not there: a person sets one at the workspace with fugaro secrets user set" slip_owner_removed "$(( $(date -u +%s) - 7200 ))"
  run_check
  expect_status "$status" 1 "the refusal came, but not within a minute"
  expect_contains "$out" "FACT removed-refusal: FAIL" "the removal row is the finding"
  expect_contains "$out" "later than the 90s window" "the row measures how late"
  expect_contains "$out" "it is not a fix" "the row says a wider window is not a remedy"
}

test_an_unconditioned_accessor_fails() {
  save_ok
  fx_ok
  accessor_json plain
  run_check
  expect_status "$status" 1 "an accessor over the project would read the shared entries too"
  expect_contains "$out" "FACT accessor-conditioned: FAIL" "the accessor row is the finding"
  expect_contains "$out" "PREFIX-CONDITIONED accessor" "the row names what U5 asks"
  expect_contains "$out" "the boundary D3141 draws" "and the rule it protects"
}

test_a_read_the_workspace_account_could_not_make_fails() {
  save_ok
  fx_ok
  printf 'denied' >"$save/accessor-read"
  run_check
  expect_status "$status" 1 "a conditioned accessor that does not work is not U5 holding"
  expect_contains "$out" "FACT accessor-reads: FAIL" "the read row is the finding"
  expect_contains "$out" "rather than 'ok'" "the row quotes what it read"
}

test_no_workspace_account_is_unmeasured() {
  save_ok
  fx_ok
  run_check_without CHECK850_WORKSPACE_ACCOUNT
  expect_status "$status" 2 "the read U5 asks about is made AS that account"
  expect_contains "$out" "FACT accessor-conditioned: FAIL" "the row reads FAIL"
  expect_contains "$out" "CHECK850_WORKSPACE_ACCOUNT was not set" "the row names the knob"
  expect_contains "$out" "FACT accessor-reads: FAIL" "and so does the read row"
}

test_a_restore_that_could_not_remove_the_token_fails() {
  save_ok
  fx_ok
  FAKE_MODE=rm-refused
  run_check
  expect_status "$status" 1 "a token this check stored and could not remove is the owner's to remove by hand"
  expect_contains "$out" "FACT restore: FAIL" "the restore row is the finding"
  expect_contains "$out" "could NOT be removed" "the row says what happened"
  expect_contains "$out" "fugaro secrets user rm claude-oauth-token --project ws" "and names the removal"
}

test_keep_token_leaves_it_and_says_so() {
  save_ok
  fx_ok
  CHECK850_TEST_ENV=(CHECK850_KEEP_TOKEN=1)
  run_check
  expect_contains "$out" "FACT restore: INFO" "a kept token is not a passing restore"
  expect_contains "$out" "is LEFT THERE" "the row says the token is still there"
  expect_contains "$out" "Remove it by hand when you are done" "and names what removes it"
}

test_the_installation_guard_refuses_another_project() {
  save_ok
  fx_ok
  FAKE_MODE=other-installation
  run_check
  expect_status "$status" 1 "another installation is not this check's subject"
  expect_contains "$err" "this is not the fugaro-dev installation" "the refusal says what it read"
  expect_contains "$out" "FACT intake-admits: FAIL — not reached" "nothing was measured"
}

test_no_workspace_project_is_unmeasured() {
  save_ok
  fx_ok
  RUN850_ENV=(CHECK850_PERSON=$person CHECK850_TOKEN_FILE=$token_file)
  run850
  expect_status "$status" 2 "the workspace's project is a reading this check cannot guess"
  expect_contains "$out" "FACT intake-admits: FAIL" "the admission row reads FAIL"
  expect_contains "$out" "CHECK850_WORKSPACE_PROJECT was not set" "the row names the knob"
  expect_contains "$out" "FACT token-stored: FAIL" "and so does the store row"
}

test_an_agent_session_is_refused() {
  save_ok
  fx_ok
  RUN850_ENV=(CLAUDECODE=1 CHECK850_PERSON=$person)
  run850
  expect_status "$status" 1 "no agent runs any step of Check 850"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_absent "$out" "FACT intake-admits: PASS" "nothing was read before the refusal"
}

test_a_zero_removal_window_is_refused() {
  save_ok
  fx_ok
  CHECK850_TEST_ENV=("CHECK850_REMOVAL_WINDOW_SECONDS=0")
  run_check
  expect_status "$status" 2 "a window of zero is not a window"
  expect_contains "$err" "CHECK850_REMOVAL_WINDOW_SECONDS is 0" "the refusal says what it read"
}

test_an_unknown_argument_is_refused() {
  run850 --nonsense
  expect_status "$status" 2 "an argument this check does not know is refused"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal names it"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK850_TEST_ONLY=name runs that one.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK850_TEST_ONLY:-}" ]; then
    [ "$1" = "${CHECK850_TEST_ONLY}" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  FAKE_MODE=ok
  FAKE_ACCOUNT=$person
  RUN850_ENV=()
  CHECK850_TEST_ENV=()
  "$1"
}

t test_help
t test_dry_run_measures_nothing
t test_the_whole_staging_holds
t test_no_person_named_is_refused
t test_another_account_is_refused
t test_a_project_role_fails_the_u6_row
t test_missing_saved_policies_are_unmeasured
t test_a_service_without_the_invoke_grant_fails
t test_the_intakes_refusal_fails
t test_a_refused_store_fails
t test_a_store_that_leaves_nothing_fails
t test_a_token_file_nobody_named_is_unmeasured
t test_an_empty_token_file_is_unmeasured
t test_a_config_that_does_not_load_fails
t test_a_config_with_no_published_layer_fails
t test_the_workspace_config_knob_is_used
t test_a_switch_that_is_not_on_fails
t test_a_member_left_alone_that_reads_true_fails
t test_a_run_not_served_the_token_fails
t test_a_second_persons_run_served_it_fails
t test_a_log_with_no_line_for_the_run_is_unmeasured
t test_a_halt_without_the_remedy_fails
t test_a_run_halted_with_no_detail_fails
t test_a_removal_outside_the_window_fails
t test_an_unconditioned_accessor_fails
t test_a_read_the_workspace_account_could_not_make_fails
t test_no_workspace_account_is_unmeasured
t test_a_restore_that_could_not_remove_the_token_fails
t test_keep_token_leaves_it_and_says_so
t test_the_installation_guard_refuses_another_project
t test_no_workspace_project_is_unmeasured
t test_an_agent_session_is_refused
t test_a_zero_removal_window_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-850_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-850_test.sh: ok"
