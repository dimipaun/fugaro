#!/usr/bin/env bash
# Tests for scripts/live-checks/check-850.sh. NOTHING here is a cloud, a project,
# a person, a token, a run or a log: every reading the check makes is a fixture
# in a temporary directory, served by a fake gcloud and a fake fugaro on PATH,
# and the cases assert what the script printed and what it refused.
#
# The bends are the row's own. A person named in the workspace's project IAM is
# the "nothing but the two invoke grants" premise broken; the workspace's account
# holding no conditioned accessor on the project's own IAM — a condition naming
# the fugaro_u_ prefix alone, one that names something else, no condition at all,
# or one held by another account — is U5 answered NO; an intake line written by
# another sub, or none at all, is the person's own write missing; a run served a
# shared entry where R7 places the
# person's token, or a second person's run served the first's container, is the
# serving half wrong. Beside them: the four refusals the release writes (the
# switch off, the deleted token, the removal in and out of its minute, a run that
# did not halt), and the readings the check has to be honest about not having
# taken -- no workspace project, no account, a policy, a listing or a log that
# cannot be read, a run id nobody has.
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK850_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK850_TEST_HEAD:-1200}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 600))"
}

[ -f "$script" ] || {
  echo "check-850_test.sh: the script $script does not exist" >&2
  exit 1
}

fx=$work/fx
mkdir -p "$work/bin" "$fx"
: >"$fx/fail"

# The names the check is read against: one person, the container their token is
# stored in, and one run id per row. The container's shape is the one
# internal/secretname gives it, so the prefix the accessor's condition names is
# the one the check looks for.
PERSON=person@example.com
OTHER_PERSON=other@example.com
SUB=1234
PREFIX=fugaro_u_
CONTAINER=${PREFIX}${SUB}-claude-oauth-token
SHARED=shared-claude-oauth-token
WORKSPACE=ws
SA=fugaro-workspace@ws.iam.gserviceaccount.com
# R6's accessor as the workspace's own root writes it: a PROJECT-level member
# whose condition names the prefix AND the name the token's container ends in
# (gcp.WorkspacePersonTokenCondition). The prefix alone is the widening the row
# exists to catch.
ACCESSOR_COND='resource.name.extract("/secrets/{secret}").startsWith("fugaro_u_") && resource.name.extract("/secrets/{secret}").endsWith("_claude-oauth-token")'
PREFIX_ONLY_COND='resource.name.extract("/secrets/{secret}").startsWith("fugaro_u_")'
ACCESSOR_TITLE=fugaro-workspace-person-token
RID_ONE=20261126-000000-per1
RID_TWO=20261126-000000-per2
RID_OTHER=20261126-000000-oth1
RID_OFF=20261126-000000-off1
RID_DEL=20261126-000000-del1
RID_REM=20261126-000000-rem1
RID_NOBODY=20261126-000000-none

# ---------------------------------------------------------------- the fakes
cat >"$work/bin/fugaro" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
fx=${FAKE_FX:?}
failing() { [ -f "$fx/fail" ] && grep -qx "$1" "$fx/fail"; }
case "${1:-}" in
version) echo "fugaro 9.9.9-test" ;;
doctor)
  failing doctor && exit 0
  echo "release: nothing to move"
  echo "project: fugaro (GCP fugaro-dev, us-east5)"
  echo "secrets: none"
  ;;
runs)
  [ "${2:-}" = "ls" ] || { echo "fake fugaro: unknown invocation $*" >&2; exit 2; }
  failing runs && { echo "fake fugaro: the run listing could not be read" >&2; exit 1; }
  cat "$fx/runs.json"
  ;;
*) echo "fake fugaro: unknown invocation $*" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/fugaro"

cat >"$work/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
fx=${FAKE_FX:?}
failing() { [ -f "$fx/fail" ] && grep -qx "$1" "$fx/fail"; }
args="$*"
case "${1:-} ${2:-}" in
"config get-value")
  failing account && exit 0
  echo "owner@example.com"
  ;;
"projects get-iam-policy")
  failing project && { echo "fake gcloud: the project policy could not be read" >&2; exit 1; }
  cat "$fx/project.json"
  ;;
"run services")
  case "${3:-}" in
  list)
    failing services && { echo "fake gcloud: the service listing could not be read" >&2; exit 1; }
    case "$args" in
    *"--region"*) ;;
    *) echo "fake gcloud: a Cloud Run call with no --region would prompt" >&2; exit 2 ;;
    esac
    cat "$fx/services.json"
    ;;
  get-iam-policy)
    failing service-policy && { echo "fake gcloud: the service policy could not be read" >&2; exit 1; }
    name=${4:-}
    case "$args" in
    *"--region"*) ;;
    *) echo "fake gcloud: a Cloud Run call with no --region would prompt" >&2; exit 2 ;;
    esac
    policy=$fx/policy-$name.json
    [ -r "$policy" ] || { echo "fake gcloud: no policy for service $name" >&2; exit 1; }
    cat "$policy"
    ;;
  *) echo "fake gcloud: unknown invocation $args" >&2; exit 2 ;;
  esac
  ;;
"secrets list")
  failing secrets && { echo "fake gcloud: the container listing could not be read" >&2; exit 1; }
  cat "$fx/secrets.json"
  ;;
"logging read")
  case "$args" in
  *'service_name="fugaro-intake"'*)
    failing intake-log && { echo "fake gcloud: the intake log could not be read" >&2; exit 1; }
    cat "$fx/log-intake.json"
    ;;
  *)
    failing log && { echo "fake gcloud: the service log could not be read" >&2; exit 1; }
    rid=$(printf '%s' "$args" | sed -n 's/.*jsonPayload.run="\([^"]*\)".*/\1/p')
    [ -n "$rid" ] && [ -r "$fx/log-$rid.json" ] || { echo "fake gcloud: no log fixture for run '${rid}'" >&2; exit 1; }
    cat "$fx/log-$rid.json"
    ;;
  esac
  ;;
*) echo "fake gcloud: unknown invocation $args" >&2; exit 2 ;;
esac
SH
chmod +x "$work/bin/gcloud"

# ------------------------------------------------------------ the fixtures
# A fixture generator is one python program per shape, so what the fakes serve is
# JSON a parser accepts and never a string a bash case matched.

policy_json() { # policy_json OUT "ROLE|MEMBERS|CONDITION-OR--[|TITLE]"...
  python3 - "$@" <<'PY'
import json, sys
out, specs = sys.argv[1], sys.argv[2:]
bindings = []
for spec in specs:
    parts = spec.split("|")
    role, members, cond = parts[0], parts[1], parts[2]
    binding = {"role": role, "members": [m for m in members.split(",") if m]}
    if cond != "-":
        binding["condition"] = {"title": parts[3] if len(parts) > 3 else "c",
                                "expression": cond}
    bindings.append(binding)
with open(out, "w") as fh:
    json.dump({"bindings": bindings}, fh)
PY
}

listing_json() { # listing_json OUT FULL-NAME...
  python3 - "$@" <<'PY'
import json, sys
out, names = sys.argv[1], sys.argv[2:]
with open(out, "w") as fh:
    json.dump([{"name": n} for n in names], fh)
PY
}

log_json() { # log_json OUT "KIND|TS|FIELDS..."...
  python3 - "$@" <<'PY'
import json, sys
out, specs = sys.argv[1], sys.argv[2:]
rows = []
for spec in specs:
    parts = spec.split("|")
    kind, ts = parts[0], parts[1]
    if kind == "audit":
        _, _, status, cred, tier, purpose, run, slip, owner = parts
        payload = {"msg": "credsvc: audit", "status": status, "credential": cred,
                   "tier": tier, "purpose": purpose, "run": run, "slip": slip, "owner": owner}
    elif kind == "refused":
        _, _, run, status, message = parts
        payload = {"msg": "credsvc: refused", "run": run, "status": status, "message": message}
    elif kind == "intake":
        _, _, op, scope, status, caller, sub, ident = parts
        payload = {"msg": "intake: audit", "op": op, "scope": scope, "status": status,
                   "caller": caller, "sub": sub, "id": ident}
    elif kind == "note":
        _, _, run, message = parts
        payload = {"msg": message, "run": run}
    else:
        raise SystemExit("log_json: unknown kind " + kind)
    entry = {"jsonPayload": payload}
    if ts:
        entry["timestamp"] = ts
    rows.append(entry)
with open(out, "w") as fh:
    json.dump(rows, fh)
PY
}

runs_json() { # runs_json OUT RID TERMINAL REASON DETAIL
  python3 - "$@" <<'PY'
import json, sys
out, rid, terminal, reason, detail = sys.argv[1:6]
row = {"run_id": rid, "status": "done" if not reason else "halted",
       "terminal": terminal == "yes", "outcome": "" if reason else "ready"}
if reason:
    row["halt"] = {"reason": reason, "scope": "run", "at": "2026-11-26T00:00:30Z", "detail": detail}
with open(out, "w") as fh:
    json.dump({"version": 1, "project": "mem-one", "runs": [row], "totals": {}}, fh)
PY
}

now_iso() { python3 -c 'import datetime,sys;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=int(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"; }

# fixtures_ok: a workspace that answers every row the way the release promises,
# and one run per row that did. The removal moment and the refusal line are two
# readings of one instant (0s and 20s), and both are made here, with every case:
# a moment captured once when this file started is a moment the fixture's own
# line drifts away from as the file runs, so a loaded machine made the check
# read its own correct refusal as outside the minute — a FAIL, and an exit 1
# where the case expects the 2 an unmeasured row is.
fixtures_ok() {
  rm -f "$fx/fail"
  : >"$fx/fail"
  NOW=$(now_iso 0)
  REMOVED_AT=$NOW
  policy_json "$fx/project.json" \
    "roles/viewer|user:someone@example.com|-" \
    "roles/secretmanager.secretAccessor|serviceAccount:$SA|$ACCESSOR_COND|$ACCESSOR_TITLE"
  listing_json "$fx/services.json" \
    "projects/$WORKSPACE/locations/us-east5/services/fugaro-credentials" \
    "projects/$WORKSPACE/locations/us-east5/services/fugaro-intake"
  policy_json "$fx/policy-fugaro-credentials.json" "roles/run.invoker|user:$PERSON|-"
  policy_json "$fx/policy-fugaro-intake.json" "roles/run.invoker|user:$PERSON|-"
  listing_json "$fx/secrets.json" "projects/$WORKSPACE/secrets/$CONTAINER"
  log_json "$fx/log-intake.json" "intake|$NOW|create|user|ok|user:$PERSON|$SUB|$CONTAINER"
  log_json "$fx/log-$RID_ONE.json" "audit|$NOW|ok|$CONTAINER|A|personal|$RID_ONE|slip-1|$PERSON"
  log_json "$fx/log-$RID_TWO.json" "audit|$NOW|ok|$CONTAINER|A|personal|$RID_TWO|slip-2|$PERSON"
  log_json "$fx/log-$RID_OTHER.json" "audit|$NOW|ok|$SHARED|C|shared|$RID_OTHER|slip-3|$OTHER_PERSON"
  log_json "$fx/log-$RID_OFF.json" \
    "refused|$NOW|$RID_OFF|state_unavailable|this call's member has the personal workspace token switch off, so the run is served the shared entries"
  log_json "$fx/log-$RID_DEL.json" \
    "refused|$NOW|$RID_DEL|state_unavailable|the personal token this call's slip names is not there: a person sets one at the workspace with 'fugaro secrets user set'"
  log_json "$fx/log-$RID_REM.json" \
    "refused|$(now_iso 20)|$RID_REM|state_unavailable|the slip's owner has left the workspace's roster (slip_owner_removed): the run halts and is served no other credential (D3146)"
  runs_json "$fx/runs.json" "$RID_REM" yes credentials \
    "the slip's owner has left the workspace's roster (slip_owner_removed): the run halts and is served no other credential (D3146)"
}

# all_options: the twelve knobs, rebuilt for each case and not once for the
# file: CHECK850_REMOVED_AT is the moment the case's own fixture wrote, so a
# case that runs late still hands the check the pair it built.
all_options() {
  OPTIONS=(
    CHECK850_WORKSPACE_PROJECT=$WORKSPACE
    CHECK850_PERSON=$PERSON
    CHECK850_PERSON_SUB=$SUB
    CHECK850_CONTAINER=$CONTAINER
    CHECK850_MEMBER_ONE_PROJECT=mem-one
    CHECK850_MEMBER_ONE_RUN=$RID_ONE
    CHECK850_MEMBER_TWO_PROJECT=mem-two
    CHECK850_MEMBER_TWO_RUN=$RID_TWO
    CHECK850_OTHER_PERSON=$OTHER_PERSON
    CHECK850_OTHER_PROJECT=mem-one
    CHECK850_OTHER_RUN=$RID_OTHER
    CHECK850_OFF_PROJECT=mem-one
    CHECK850_OFF_RUN=$RID_OFF
    CHECK850_DELETED_PROJECT=mem-one
    CHECK850_DELETED_RUN=$RID_DEL
    CHECK850_REMOVED_PROJECT=mem-one
    CHECK850_REMOVED_RUN=$RID_REM
    CHECK850_REMOVED_AT=$REMOVED_AT
  )
}

run850() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$work/bin:$PATH" FAKE_FX="$fx" TMPDIR="$work" \
    ${RUN850_ENV[@]+"${RUN850_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run_check() {
  all_options
  RUN850_ENV=("${OPTIONS[@]}" ${CHECK850_TEST_ENV[@]+"${CHECK850_TEST_ENV[@]}"})
  run850 "$@"
}

# ------------------------------------------------------------------- the cases
test_help() {
  run850 --help
  expect_status "$status" 0 "check-850.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "WORKSPACE_PROJECT" "--help names the project every read lands in"
  expect_contains "$out" "REMOVED_AT" "--help names the moment the removal was made"
  expect_contains "$out" "CHECK850_REGION" "--help names the region the two services are read in"
}

test_dry_run_measures_nothing() {
  fixtures_ok
  run_check --dry-run
  expect_status "$status" 0 "--dry-run is a plan and nothing else"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run says it ran nothing"
  expect_contains "$out" "FACT invoke-grants: INFO" "a planned row reads INFO, not PASS"
  expect_contains "$err" "gcloud projects get-iam-policy" "the plan names the reads it would make"
  expect_contains "$err" "every launch, the switch, the deletion and the removal: the owner's" "the plan says what a script never does"
}

test_a_clean_workspace_passes() {
  fixtures_ok
  run_check
  expect_status "$status" 0 "every measured criterion held"
  expect_contains "$out" "FACT invoke-grants: PASS" "the two grants are there and the person holds nothing else"
  expect_contains "$out" "holds the two invoke grants and nothing else" "the row says what it read"
  expect_contains "$out" "FACT person-container: PASS" "the container is read under a prefix-conditioned accessor"
  expect_contains "$out" "U5 answered YES" "the row names the question it settles"
  expect_contains "$out" "FACT served-one: PASS" "the first member's run was served the person's token"
  expect_contains "$out" "FACT served-two: PASS" "and so was the second's"
  expect_contains "$out" "FACT other-person: PASS" "a second person's run was served something else"
  expect_contains "$out" "FACT switch-off: PASS" "the switch off refuses as written"
  expect_contains "$out" "FACT token-deleted: PASS" "a deleted token refuses as written"
  expect_contains "$out" "FACT removed: PASS" "the removal halts the run in flight"
  expect_contains "$out" "FACT owner-steps: INFO" "the rows only the owner can produce are said out loud"
  expect_contains "$out" "FACT restore: PASS" "nothing was created and nothing deleted"
  expect_contains "$out" "FACT scratch-removed: PASS" "the scratch directory is gone"
}

test_a_missing_invoke_grant_fails() {
  fixtures_ok
  policy_json "$fx/policy-fugaro-intake.json" "roles/run.invoker|user:$OTHER_PERSON|-"
  run_check
  expect_status "$status" 1 "a person with no grant on the intake is not the U6 premise"
  expect_contains "$out" "FACT invoke-grants: FAIL" "the grants row is the finding"
  expect_contains "$out" "the two invoke grants are not both there" "the row says which half is missing"
  expect_contains "$out" "no roles/run.invoker binding names user:$PERSON" "the row quotes what it read instead"
}

test_a_person_named_in_the_project_iam_is_unmeasured() {
  fixtures_ok
  policy_json "$fx/project.json" "roles/viewer|user:$PERSON|-" "roles/run.invoker|user:$PERSON|-" \
    "roles/secretmanager.secretAccessor|serviceAccount:$SA|$ACCESSOR_COND|$ACCESSOR_TITLE"
  run_check
  expect_status "$status" 2 "a person who holds a role on the project is not the premise U6 is read on"
  expect_contains "$out" "FACT invoke-grants: FAIL" "the grants row is unmeasured"
  expect_contains "$out" "nothing but the two invoke grants" "the row names the premise that broke"
}

test_the_workspace_serving_no_such_service_fails() {
  fixtures_ok
  listing_json "$fx/services.json" "projects/$WORKSPACE/locations/us-east5/services/something-else"
  run_check
  expect_status "$status" 1 "a workspace without its two services is a finding"
  expect_contains "$out" "FACT invoke-grants: FAIL" "the grants row is the finding"
  expect_contains "$out" "serves no service" "the row says neither service is there"
}

test_a_project_policy_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  echo project >>"$fx/fail"
  run_check
  expect_status "$status" 2 "a policy that cannot be read measures nothing"
  expect_contains "$out" "FACT invoke-grants: FAIL" "the grants row is unmeasured"
  expect_contains "$out" "gcloud projects get-iam-policy" "the row names the read to run yourself"
}

test_an_unconditional_accessor_fails() {
  fixtures_ok
  policy_json "$fx/project.json" "roles/secretmanager.secretAccessor|serviceAccount:$SA|-"
  run_check
  expect_status "$status" 1 "an accessor with no condition reads every secret of the project"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is the finding"
  expect_contains "$out" "with NO condition" "the row says what is wrong with the accessor"
  expect_contains "$out" "reads EVERY secret of the workspace's project" "the row says what that costs"
}

test_an_accessor_whose_condition_names_the_prefix_alone_fails() {
  fixtures_ok
  policy_json "$fx/project.json" \
    "roles/secretmanager.secretAccessor|serviceAccount:$SA|$PREFIX_ONLY_COND|$ACCESSOR_TITLE"
  run_check
  expect_status "$status" 1 "a prefix-only condition reads every other person's container"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is the finding"
  expect_contains "$out" "prefix ALONE" "the row says which half is missing"
  expect_contains "$out" "every other person's container among them" "the row says what the widening costs"
}

test_an_accessor_whose_condition_never_names_the_prefix_fails() {
  fixtures_ok
  policy_json "$fx/project.json" \
    "roles/secretmanager.secretAccessor|serviceAccount:$SA|resource.name.startsWith(\"projects/-/secrets/elsewhere_\")"
  run_check
  expect_status "$status" 1 "a condition that never names the prefix is not R6's accessor"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is the finding"
  expect_contains "$out" "condition naming the fugaro_u_ prefix" "the row names what the condition had to say"
  expect_contains "$out" "is the answer NO" "the row says U5 is answered by this read"
}

test_an_accessor_held_by_another_account_fails() {
  fixtures_ok
  policy_json "$fx/project.json" \
    "roles/secretmanager.secretAccessor|serviceAccount:someone-else@ws.iam.gserviceaccount.com|$ACCESSOR_COND|$ACCESSOR_TITLE"
  run_check
  expect_status "$status" 1 "R6's accessor is the workspace's own account's"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is the finding"
  expect_contains "$out" "gives $SA" "the row names the account it looked for"
}

test_two_containers_is_unmeasured() {
  fixtures_ok
  listing_json "$fx/secrets.json" \
    "projects/$WORKSPACE/secrets/$CONTAINER" "projects/$WORKSPACE/secrets/${PREFIX}5678-claude-oauth-token"
  CHECK850_TEST_ENV=(CHECK850_CONTAINER=)
  run_check
  expect_status "$status" 2 "a person's token is stored once, so two containers measure nothing"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is unmeasured"
  expect_contains "$out" "holds 2 containers under fugaro_u_" "the row says what it found"
  expect_contains "$out" "stored once" "the row names the rule"
}

test_an_intake_line_written_by_another_sub_fails() {
  fixtures_ok
  log_json "$fx/log-intake.json" "intake|$NOW|create|user|ok|user:$OTHER_PERSON|9999|${PREFIX}9999-claude-oauth-token"
  run_check
  expect_status "$status" 1 "the write has to be the person's own"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is the finding"
  expect_contains "$out" "no line says the person's own identity wrote it" "the row says what is missing"
  expect_contains "$out" "fugaro secrets user set" "the row says how to produce it"
}

test_an_intake_log_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  echo intake-log >>"$fx/fail"
  run_check
  expect_status "$status" 2 "a log that cannot be read measures nothing"
  expect_contains "$out" "FACT person-container: FAIL" "the container row is unmeasured"
  expect_contains "$out" "could not be read" "the row says the read failed"
}

test_a_run_served_a_shared_entry_fails() {
  fixtures_ok
  log_json "$fx/log-$RID_ONE.json" "audit|$NOW|ok|$SHARED|C|shared|$RID_ONE|slip-1|$PERSON"
  run_check
  expect_status "$status" 1 "R7 places the person's token first where the switch is on"
  expect_contains "$out" "FACT served-one: FAIL" "the first member's row is the finding"
  expect_contains "$out" "FACT served-two: PASS" "the second member's run is untouched by the first's finding"
  expect_contains "$out" "was NOT served $CONTAINER" "the row says which credential it read"
  expect_contains "$out" "R7 places the person's token first" "the row names the rule"
}

test_another_persons_run_served_the_container_fails() {
  fixtures_ok
  log_json "$fx/log-$RID_OTHER.json" "audit|$NOW|ok|$CONTAINER|A|personal|$RID_OTHER|slip-3|$OTHER_PERSON"
  run_check
  expect_status "$status" 1 "a slip entitles its own owner's token and no other"
  expect_contains "$out" "FACT other-person: FAIL" "the second person's row is the finding"
  expect_contains "$out" "which is another person's token" "the row says what it read"
  expect_contains "$out" "entitles its OWNER's token and no other" "the row names the rule"
}

test_a_second_persons_run_the_service_wrote_nothing_for_is_unmeasured() {
  fixtures_ok
  log_json "$fx/log-$RID_OTHER.json"
  run_check
  expect_status "$status" 2 "an id nobody called under measures nothing"
  expect_contains "$out" "FACT other-person: FAIL" "the second person's row is unmeasured"
  expect_contains "$out" "wrote no line at all" "the row says what it found"
}

test_a_switch_off_run_served_instead_fails() {
  fixtures_ok
  log_json "$fx/log-$RID_OFF.json" "audit|$NOW|ok|$SHARED|C|shared|$RID_OFF|slip-4|$PERSON"
  run_check
  expect_status "$status" 1 "the switch off has to refuse"
  expect_contains "$out" "FACT switch-off: FAIL" "the switch-off row is the finding"
  expect_contains "$out" "was served instead" "the row says the run was served"
}

test_a_deleted_token_refused_with_another_sentence_fails() {
  fixtures_ok
  log_json "$fx/log-$RID_DEL.json" \
    "refused|$NOW|$RID_DEL|state_unavailable|the workspace's roster does not name this caller"
  run_check
  expect_status "$status" 1 "the release's own sentence for a deleted token is what the row reads"
  expect_contains "$out" "FACT token-deleted: FAIL" "the deleted-token row is the finding"
  expect_contains "$out" "no line carrying its own sentence" "the row says the sentence is missing"
}

test_a_removal_refusal_outside_the_minute_fails() {
  fixtures_ok
  log_json "$fx/log-$RID_REM.json" \
    "refused|$(now_iso 600)|$RID_REM|state_unavailable|the slip's owner has left the workspace's roster (slip_owner_removed): the run halts and is served no other credential (D3146)"
  run_check
  expect_status "$status" 1 "a refusal that arrives ten minutes later is not 'within a minute'"
  expect_contains "$out" "FACT removed: FAIL" "the removal row is the finding"
  expect_contains "$out" "is NOT within 60s of the removal" "the row names the bound and the gap"
}

test_a_removed_persons_run_that_did_not_halt_fails() {
  fixtures_ok
  runs_json "$fx/runs.json" "$RID_REM" no "" ""
  run_check
  expect_status "$status" 1 "a run still going after its owner left is what D3146 forbids"
  expect_contains "$out" "FACT removed: FAIL" "the removal row is the finding"
  expect_contains "$out" "is not terminal" "the row says the run did not stop"
}

test_a_removal_without_the_halt_detail_fails() {
  fixtures_ok
  runs_json "$fx/runs.json" "$RID_REM" yes credentials "the credentials service could not be reached"
  run_check
  expect_status "$status" 1 "the halt has to name the removal"
  expect_contains "$out" "FACT removed: FAIL" "the removal row is the finding"
  expect_contains "$out" "does not name the removal" "the row says the detail is silent"
}

test_a_run_id_nobody_has_is_unmeasured() {
  fixtures_ok
  runs_json "$fx/runs.json" "$RID_NOBODY" yes credentials "slip_owner_removed"
  run_check
  expect_status "$status" 2 "a run id nobody has measures nothing"
  expect_contains "$out" "FACT removed: FAIL" "the removal row is unmeasured"
  expect_contains "$out" "no run in the listing has an id ending in" "the row says what it read"
}

test_the_run_listing_that_cannot_be_read_is_unmeasured() {
  fixtures_ok
  echo runs >>"$fx/fail"
  run_check
  expect_status "$status" 2 "a listing that cannot be read measures nothing"
  expect_contains "$out" "FACT removed: FAIL" "the removal row is unmeasured"
  expect_contains "$out" "the run's own record could not be read" "the row says what failed"
}

test_no_run_id_is_unmeasured_with_what_to_do() {
  fixtures_ok
  CHECK850_TEST_ENV=(CHECK850_MEMBER_ONE_RUN= CHECK850_OFF_RUN=)
  run_check
  expect_status "$status" 2 "a row with no run measures nothing"
  expect_contains "$out" "FACT served-one: FAIL" "the row with no run is unmeasured"
  expect_contains "$out" "no run was named" "the row says what it needs"
  expect_contains "$out" "FACT switch-off: FAIL" "and so is the switch-off row"
  expect_contains "$out" "FACT served-two: PASS" "a row with its run still passes"
}

test_no_workspace_project_is_unmeasured() {
  fixtures_ok
  RUN850_ENV=(CHECK850_PERSON=$PERSON)
  run850
  expect_status "$status" 2 "nothing can be read without the workspace's project"
  expect_contains "$out" "CHECK850_WORKSPACE_PROJECT was not set" "the row says what to set"
  expect_contains "$out" "FACT served-one: FAIL" "every row that read the workspace is unmeasured"
  expect_contains "$out" "where every container, policy and line of this check lives" "the rows say why"
}

test_no_account_is_refused() {
  fixtures_ok
  echo account >>"$fx/fail"
  run_check
  expect_status "$status" 2 "no account, no reads"
  expect_contains "$err" "gcloud prints no account" "the refusal says what it read"
}

test_the_installation_guard_refuses_another_project() {
  fixtures_ok
  echo doctor >>"$fx/fail"
  run_check
  expect_status "$status" 1 "this check is never run against belong"
  expect_contains "$err" "not the fugaro-dev installation" "the refusal says what it read"
  expect_absent "$out" "FACT invoke-grants: PASS" "nothing was read before the refusal"
}

test_an_agent_session_is_refused() {
  fixtures_ok
  RUN850_ENV=(CLAUDECODE=1 CHECK850_WORKSPACE_PROJECT=$WORKSPACE)
  run850
  expect_status "$status" 1 "no agent runs any step of Check 850"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_absent "$out" "FACT person-container: PASS" "nothing was read before the refusal"
}

test_a_grace_of_zero_is_refused() {
  fixtures_ok
  CHECK850_TEST_ENV=(CHECK850_GRACE_SECONDS=0)
  run_check
  expect_status "$status" 2 "a bound of zero is not a bound"
  expect_contains "$err" "CHECK850_GRACE_SECONDS is 0" "the refusal says what it read"
}

test_an_unknown_argument_is_refused() {
  run850 --nonsense
  expect_status "$status" 2 "an argument this check does not know is refused"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal names it"
}

test_a_real_run_does_not_report_the_plan_row_as_a_failure() {
  fixtures_ok
  run_check
  expect_status "$status" 0 "the clean workspace passes, whatever the block says of the plan"
  expect_contains "$out" "FACT dry-run: INFO" "a real run says the plan row is not one of its own"
  expect_absent "$out" "FACT dry-run: FAIL" "and never brands it a row that broke"
  expect_absent "$out" "not reached (the run stopped before this step)" "no row reads as one the run stopped before"
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
  RUN850_ENV=()
  CHECK850_TEST_ENV=()
  "$1"
}

t test_help
t test_dry_run_measures_nothing
t test_a_clean_workspace_passes
t test_a_missing_invoke_grant_fails
t test_a_person_named_in_the_project_iam_is_unmeasured
t test_the_workspace_serving_no_such_service_fails
t test_a_project_policy_that_cannot_be_read_is_unmeasured
t test_an_unconditional_accessor_fails
t test_an_accessor_whose_condition_names_the_prefix_alone_fails
t test_an_accessor_whose_condition_never_names_the_prefix_fails
t test_an_accessor_held_by_another_account_fails
t test_two_containers_is_unmeasured
t test_an_intake_line_written_by_another_sub_fails
t test_an_intake_log_that_cannot_be_read_is_unmeasured
t test_a_run_served_a_shared_entry_fails
t test_another_persons_run_served_the_container_fails
t test_a_second_persons_run_the_service_wrote_nothing_for_is_unmeasured
t test_a_switch_off_run_served_instead_fails
t test_a_deleted_token_refused_with_another_sentence_fails
t test_a_removal_refusal_outside_the_minute_fails
t test_a_removed_persons_run_that_did_not_halt_fails
t test_a_removal_without_the_halt_detail_fails
t test_a_run_id_nobody_has_is_unmeasured
t test_the_run_listing_that_cannot_be_read_is_unmeasured
t test_no_run_id_is_unmeasured_with_what_to_do
t test_no_workspace_project_is_unmeasured
t test_no_account_is_refused
t test_the_installation_guard_refuses_another_project
t test_an_agent_session_is_refused
t test_a_grace_of_zero_is_refused
t test_an_unknown_argument_is_refused
t test_a_real_run_does_not_report_the_plan_row_as_a_failure

if [ "$failures" -ne 0 ]; then
  echo "check-850_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-850_test.sh: ok"
