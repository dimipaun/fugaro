#!/usr/bin/env bash
# Tests for scripts/live-checks/check571.sh, with a fake `gcloud` on PATH:
# NOTHING here reaches a cloud, a project or an IAM policy, no agent runs a live
# command, and the fake records every call it is given — with the directory it
# was made in — so a case can assert what the check read and what it did NOT read.
# Check 571 is a census of one project's IAM policy, which exists only in the
# cloud: the only way to measure its rows is against documents a case wrote.
#
# The fake `gcloud` answers out of $FAKE571_DIR, one file per document the check
# reads: `census.json` is the project policy, `run-<service>.json` and
# `ring-<ring>.json` are the two Cloud Run and two key ring policies, `sa-*.json`
# are the two accounts' own policies, `role-<role>.json` is a role's permissions
# and `project.json` the project itself. A document that is not there is an error
# the way a missing resource is, and `<name>.exit` makes a call fail with a status
# a case picks — which is how the cases that measure "could not measure" rows are
# written. Every case bends exactly one document.
#
# Usage: bash scripts/live-checks/check571_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check571_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check571_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check571.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK571_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK571_TEST_HEAD:-1600}")" ;;
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
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

project=chk571-test-proj
# The two accounts the plan deploys with, by the names
# ee/keyservice/terraform/services/variables.tf gives them.
api=fugaro-portal-api
minter=fugaro-key-minter
minter_email="$minter@$project.iam.gserviceaccount.com"
api_email="$api@$project.iam.gserviceaccount.com"
website_ci="website-ci@$project.iam.gserviceaccount.com"
signer_role="projects/$project/roles/fugaroKeySigner"
handover_role="projects/$project/roles/fugaroHandoverSealer"

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check571_test.sh only: it
# answers each read out of $FAKE571_DIR and records the call. A document that is
# not there exits 1 with an error the way a missing resource does; `<name>.exit`
# makes the call fail with the status a case picked. Nothing here talks to a
# cloud, and a policy it prints is a case's own file.
set -euo pipefail
dir=${FAKE571_DIR:?}
mkdir -p "$dir/logs"
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$dir/logs/gcloud.calls"

# A copy of the arguments with the values of the flags that are not part of a
# document's identity dropped: which file answers a call must not depend on the
# order a flag was passed in.
args=()
while [ $# -gt 0 ]; do
  case "$1" in
  --format=*) shift ;;
  --project | --region | --location) shift 2 ;;
  *)
    args+=("$1")
    shift
    ;;
  esac
done
case "${args[0]:-}" in
--version) name=version ;;
config) name=account ;;
projects)
  if [ "${args[1]:-}" = describe ]; then name=project; else name=census; fi
  ;;
iam)
  case "${args[1]:-}" in
  roles) name="role-${args[3]##*/}" ;;
  service-accounts) name="sa-${args[3]:-}" ;;
  *)
    echo "fake gcloud: no route for '$*'" >&2
    exit 7
    ;;
  esac
  ;;
run) name="run-${args[3]:-}" ;;
kms) name="ring-${args[3]:-}" ;;
*)
  echo "fake gcloud: no route for '$*'" >&2
  exit 7
  ;;
esac
if [ -f "$dir/$name.exit" ]; then
  [ -f "$dir/$name.err" ] && cat "$dir/$name.err" >&2
  exit "$(cat "$dir/$name.exit")"
fi
if [ ! -f "$dir/$name.json" ] && [ ! -f "$dir/$name.txt" ]; then
  echo "ERROR: (gcloud) NOT_FOUND: $name was not found" >&2
  exit 1
fi
if [ -f "$dir/$name.txt" ]; then cat "$dir/$name.txt"; else cat "$dir/$name.json"; fi
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# ---------------------------------------------------------------- documents
write_role() { # write_role DIR ROLE PERMS...  — a role and the permissions it carries
  local dir=$1 role=$2
  shift 2
  local name=${role##*/}
  python3 - "$role" "$@" >"$dir/role-$name.json" <<'PY'
import json, sys
print(json.dumps({"name": sys.argv[1], "includedPermissions": sys.argv[2:]}))
PY
}

write_policy() { # write_policy FILE ROLE MEMBER...
  local file=$1 role=$2
  shift 2
  python3 - "$role" "$@" >"$file" <<'PY'
import json, sys
members = [m for m in sys.argv[2:] if m != "-"]
doc = {"version": 1, "etag": "chk571", "bindings": []}
if members:
    doc["bindings"].append({"role": sys.argv[1], "members": members})
print(json.dumps(doc))
PY
}

write_multi_policy() { # write_multi_policy FILE ROLE MEMBERS ROLE MEMBERS...
  local file=$1
  shift
  python3 - "$@" >"$file" <<'PY'
import json, sys
pairs = sys.argv[1:]
doc = {"version": 1, "etag": "chk571", "bindings": []}
for i in range(0, len(pairs), 2):
    members = [m for m in pairs[i + 1].split(",") if m and m != "-"]
    if members:
        doc["bindings"].append({"role": pairs[i], "members": members})
print(json.dumps(doc))
PY
}

# prepare DIR: the state every row holds — one project whose policy carries the
# plan's own grants plus the one D2194 accepts (the website's deployer), a minter
# invokable by the API alone, the two rings with one member each, and two accounts
# nobody may impersonate.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/logs" "$dir/tmp" "$dir/home"
  write_fake_gcloud "$dir"
  printf 'v0.0.0-chk571\n' >"$dir/version.json"
  printf 'owner@fugaro.test\n' >"$dir/account.txt"
  python3 - "$project" >"$dir/project.json" <<'PY'
import json, sys
print(json.dumps({"projectId": sys.argv[1], "projectNumber": "571571571571",
                  "lifecycleState": "ACTIVE", "name": "chk571"}))
PY
  write_multi_policy "$dir/census.json" \
    "roles/owner" "user:owner@fugaro.test" \
    "roles/firebasehosting.admin" "serviceAccount:$website_ci" \
    "roles/datastore.user" "serviceAccount:$api_email" \
    "roles/billing.viewer" "user:accountant@fugaro.test"
  write_role "$dir" roles/owner resourcemanager.projects.setIamPolicy iam.serviceAccounts.actAs resourcemanager.projects.get
  write_role "$dir" roles/firebasehosting.admin firebasehosting.sites.update firebasehosting.sites.get
  write_role "$dir" roles/datastore.user datastore.entities.get datastore.entities.create datastore.entities.list
  write_role "$dir" roles/billing.viewer billing.accounts.get
  write_role "$dir" projects/"$project"/roles/fugaroKeySigner cloudkms.cryptoKeyVersions.useToSign cloudkms.cryptoKeys.get
  write_role "$dir" projects/"$project"/roles/fugaroHandoverSealer cloudkms.cryptoKeyVersions.useToEncrypt cloudkms.cryptoKeyVersions.useToDecrypt
  write_policy "$dir/run-fugaro-key-minter.json" roles/run.invoker "serviceAccount:$api_email"
  write_policy "$dir/run-fugaro-portal-api.json" roles/run.invoker allUsers
  write_policy "$dir/ring-fugaro-licence-staging.json" "$signer_role" "serviceAccount:$minter_email"
  write_policy "$dir/ring-fugaro-licence-production.json" "$signer_role" "serviceAccount:$minter_email"
  write_policy "$dir/ring-fugaro-portal-handover.json" "$handover_role" "serviceAccount:$api_email"
  write_policy "$dir/sa-$minter_email.json" roles/iam.serviceAccountUser -
  write_policy "$dir/sa-$api_email.json" roles/iam.serviceAccountUser -
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
  mkdir -p "$dir/tmp" "$dir/home" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and only a traced interpreter reaches the guard.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the suite that
  # runs this file may itself be started from an agent's session, and the case
  # that measures that refusal sets the marker again.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE571_DIR="$dir" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --project "$project" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check571\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "${CHECK571_TEST_DUMP:-}" ]; then printf -- '--- %s (exit %s)\n%s\n' "$dir" "$status" "$out" >&2; fi
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK_TEST_ONLY=name runs that one alone.
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

# --------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "check571.sh --project" "--help: the command it documents"
  expect_absent "$out" "CHECK 571 RESULT" "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "--help: nothing is read"
  expect_contains "$out" "the owner reads rather than one they were told about once" "--help: whose check it is"
  expect_contains "$out" "the minter's invokers: exactly one, the API's account" "--help: what it reads"
  expect_contains "$out" "refused unless" "--help: the production guard"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK571_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "CHECK 571 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "get-iam-policy $project" "dry run: the plan names the census read"
  expect_contains "$err" "kms keyrings get-iam-policy fugaro-licence-staging" "dry run: the plan names the ring"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "dry run: no call is made"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_row gcloud "owner@fugaro.test" "happy path: the gcloud row names the account"
  expect_row census "3 project-level binding(s)" "happy path: the census counts the bindings"
  expect_row census "3 identity/identities" "happy path: the census counts the identities"
  expect_row invokers "can be called by serviceAccount:$api_email alone" "happy path: the minter's fence"
  expect_row invokers "$api (the API) is the internet-facing half" "happy path: the API's own policy"
  expect_row signer "the signer role projects/$project/roles/fugaroKeySigner for serviceAccount:$minter_email alone" "happy path: the signer row"
  expect_row handover "sealed and opened by serviceAccount:$api_email alone" "happy path: the handover row"
  expect_row actas "hold no member on their own policies" "happy path: nobody may impersonate"
  expect_row restore "it made no write" "happy path: nothing to undo"
  # D2194's accepted exposure: the website's CI deployer is IN the list — that is
  # the point of the census — and it is named with the permission that reaches
  # signing, not with the role's name alone.
  expect_row census "roles/firebasehosting.admin>serviceAccount:$website_ci" "happy path: the deployer is listed"
  expect_row census "firebasehosting.sites.update" "happy path: the list says WHAT reaches signing"
  expect_row census "roles/owner>user:owner@fugaro.test" "happy path: the owner's own grant is listed"
  expect_row census "roles/datastore.user>serviceAccount:$api_email" "happy path: the store's own grant is listed"
  # …and a role that reaches none is not in a list of what reaches signing.
  expect_absent "$out" "billing.viewer" "happy path: a harmless role is not in the census"
  # The list is one line per binding above the block, which is what the owner reads.
  expect_contains "$err" "reaches signing: roles/firebasehosting.admin -> serviceAccount:$website_ci" "happy path: the census is printed line by line"
  # What was read, and the order that matters: each policy from the project it is in.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "projects get-iam-policy $project" "happy path: the project's policy is read"
  expect_contains "$calls" "iam roles describe roles/owner" "happy path: a role's permissions are read"
  expect_contains "$calls" "run services get-iam-policy fugaro-key-minter --region us-east5" "happy path: the minter's policy is read in the region"
  expect_contains "$calls" "kms keyrings get-iam-policy fugaro-licence-staging --location us-east5" "happy path: the staging ring is read"
  expect_contains "$calls" "kms keyrings get-iam-policy fugaro-portal-handover" "happy path: the handover ring is read"
  expect_contains "$calls" "iam service-accounts get-iam-policy $minter_email" "happy path: the minter's account policy is read"
  assert_scratch_removed "happy path"
}

test_the_environment_names_the_ring() {
  # D459: one key id per ring, one ring per environment. The ring whose signer
  # grant is read is the one the deployment signs for, so the two have to line up.
  local dir=$work/environment
  prepare "$dir"
  run_check "$dir" -- --environment production
  expect_status "$status" 0 "the production environment"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "kms keyrings get-iam-policy fugaro-licence-production" "the production environment: the production ring is read"
  expect_row signer "fugaro-licence-production" "the production environment: the row names the ring"
}

test_a_project_that_is_not_the_plan_is_read_as_given() {
  # The names the plan deploys with are the defaults, and every one of them is a
  # flag: an installation that named its services otherwise is read, not refused.
  local dir=$work/other-names
  prepare "$dir"
  mv "$dir/run-fugaro-key-minter.json" "$dir/run-my-signer.json"
  write_role "$dir" projects/"$project"/roles/mySigner cloudkms.cryptoKeyVersions.useToSign
  write_policy "$dir/ring-my-ring.json" "projects/$project/roles/mySigner" "serviceAccount:my-minter@$project.iam.gserviceaccount.com"
  write_policy "$dir/sa-my-minter@$project.iam.gserviceaccount.com.json" roles/iam.serviceAccountUser -
  run_check "$dir" -- --minter-service my-signer --minter-account my-minter --ring my-ring --signer-role mySigner
  expect_status "$status" 0 "an installation with its own names"
  expect_row invokers "my-signer (the minter) can be called by serviceAccount:$api_email alone" "an installation with its own names: the minter row"
  expect_row signer "my-ring" "an installation with its own names: the ring row"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_contains "$err" "fugaro-prod, which is refused unless --production is given" "fugaro-prod: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "fugaro-prod: nothing is read"
}

test_fugaro_prod_with_production_is_measured() {
  local dir=$work/prod-by-choice
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod --production --dry-run
  expect_status "$status" 0 "fugaro-prod with --production"
  expect_row project "production, named with --production" "fugaro-prod: the row"
  # D459: the production project signs for production's own key, so the ring is
  # derived rather than taken from the staging default.
  expect_contains "$err" "fugaro-licence-production" "fugaro-prod: the production ring is the one planned"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project belong
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "names belong" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "belong: nothing is read"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "a coding agent's session: nothing is read"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "this shell is tracing (bash -x" "a traced shell: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "a traced shell: nothing is read"
}

test_a_gcloud_that_does_not_answer_is_not_measured() {
  local dir=$work/no-gcloud
  prepare "$dir"
  printf '1\n' >"$dir/version.exit"
  run_check "$dir"
  expect_status "$status" 2 "gcloud that does not answer"
  expect_row gcloud "gcloud did not answer --version" "gcloud that does not answer: the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "get-iam-policy" "gcloud that does not answer: no policy is read"
}

test_an_unauthenticated_gcloud_is_not_measured() {
  # A policy read as nobody answers 403, which is a refusal this check could not
  # tell from a finding: it stops on the account, before it reads anything.
  local dir=$work/no-account
  prepare "$dir"
  printf '(unset)\n' >"$dir/account.txt"
  run_check "$dir"
  expect_status "$status" 2 "gcloud with no account"
  expect_row gcloud "no authenticated account" "gcloud with no account: the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "get-iam-policy" "gcloud with no account: no policy is read"
}

test_a_project_that_cannot_be_described_is_not_measured() {
  local dir=$work/no-project
  prepare "$dir"
  printf '1\n' >"$dir/project.exit"
  printf 'ERROR: (gcloud.projects.describe) PERMISSION_DENIED\n' >"$dir/project.err"
  run_check "$dir"
  expect_status "$status" 2 "a project that cannot be described"
  expect_row gcloud "PERMISSION_DENIED" "a project that cannot be described: the row quotes the refusal"
}

test_a_census_that_cannot_be_read_is_not_measured() {
  local dir=$work/no-census
  prepare "$dir"
  printf '1\n' >"$dir/census.exit"
  printf 'ERROR: (gcloud.projects.get-iam-policy) PERMISSION_DENIED: the caller does not have resourcemanager.projects.getIamPolicy\n' >"$dir/census.err"
  run_check "$dir"
  expect_status "$status" 2 "a census that cannot be read"
  expect_row census "did not answer" "a census that cannot be read: the row"
  expect_row census "resourcemanager.projects.getIamPolicy" "a census that cannot be read: the row quotes the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run services" "a census that cannot be read: nothing below it is read"
}

test_a_role_that_cannot_be_described_is_a_hole_in_the_census() {
  # A role nobody can describe is exactly where an unexpected grant would hide:
  # the census would read as a list that missed it.
  local dir=$work/no-role
  prepare "$dir"
  rm -f "$dir/role-datastore.user.json"
  run_check "$dir"
  expect_status "$status" 2 "a role whose permissions cannot be read"
  expect_row census "the census has a hole in it" "a role that cannot be read: the row"
  expect_row census "roles/datastore.user" "a role that cannot be read: the row names the role"
}

test_a_custom_role_with_use_to_sign_is_in_the_census() {
  # The plan's words are "any project-level role with useToSign": a role named
  # something nobody predicted is caught by its permissions, not its name.
  local dir=$work/use-to-sign
  prepare "$dir"
  write_role "$dir" projects/"$project"/roles/legacyMinter cloudkms.cryptoKeyVersions.useToSign
  write_multi_policy "$dir/census.json" \
    "projects/$project/roles/legacyMinter" "serviceAccount:legacy@$project.iam.gserviceaccount.com" \
    "roles/billing.viewer" "user:accountant@fugaro.test"
  run_check "$dir"
  expect_status "$status" 0 "a custom role with useToSign"
  expect_row census "projects/$project/roles/legacyMinter>serviceAccount:legacy@$project.iam.gserviceaccount.com" "a custom role with useToSign: it is listed"
  expect_row census "cloudkms.cryptoKeyVersions.useToSign" "a custom role with useToSign: the permission that reaches signing is named"
}

test_a_second_invoker_of_the_minter_is_a_finding() {
  local dir=$work/invoker-others
  prepare "$dir"
  write_multi_policy "$dir/run-fugaro-key-minter.json" \
    "roles/run.invoker" "serviceAccount:$api_email,allUsers" \
    "roles/run.viewer" "user:owner@fugaro.test"
  run_check "$dir"
  expect_status "$status" 1 "a second invoker"
  expect_row invokers "is not callable by the API's account alone" "a second invoker: the finding"
  expect_row invokers "allUsers" "a second invoker: the finding names it"
  expect_row invokers "roles/run.viewer>user:owner@fugaro.test" "a second invoker: any other binding there is named too"
}

test_a_minter_nobody_may_call_is_a_finding() {
  local dir=$work/no-invoker
  prepare "$dir"
  write_policy "$dir/run-fugaro-key-minter.json" roles/run.invoker -
  run_check "$dir"
  expect_status "$status" 1 "a minter nobody may call"
  expect_row invokers "held by 'nobody'" "a minter nobody may call: the finding"
}

test_an_api_policy_that_cannot_be_read_is_not_measured() {
  local dir=$work/no-api-policy
  prepare "$dir"
  printf '1\n' >"$dir/run-fugaro-portal-api.exit"
  printf 'ERROR: (gcloud.run.services.get-iam-policy) NOT_FOUND\n' >"$dir/run-fugaro-portal-api.err"
  run_check "$dir"
  expect_status "$status" 2 "the API's own policy unreadable"
  expect_row invokers "the internet-facing half of the pair was not read" "the API's own policy unreadable: the row"
}

test_a_second_member_on_the_signing_ring_is_a_finding() {
  local dir=$work/ring-second-member
  prepare "$dir"
  write_multi_policy "$dir/ring-fugaro-licence-staging.json" \
    "$signer_role" "serviceAccount:$minter_email,serviceAccount:legacy@$project.iam.gserviceaccount.com"
  run_check "$dir"
  expect_status "$status" 1 "a second member on the signer role"
  expect_row signer "is not signable by the minter alone" "a second member: the finding"
  expect_row signer "serviceAccount:legacy@$project.iam.gserviceaccount.com" "a second member: the finding names it"
}

test_another_role_that_can_sign_on_the_ring_is_a_finding() {
  # `roles/cloudkms.signerVerifier` is the predefined role that signs: it is not
  # the plan's custom role and it reaches the same key than the custom role does.
  local dir=$work/ring-other-role
  prepare "$dir"
  write_role "$dir" roles/cloudkms.signerVerifier cloudkms.cryptoKeyVersions.useToSign cloudkms.cryptoKeys.get
  write_multi_policy "$dir/ring-fugaro-licence-staging.json" \
    "$signer_role" "serviceAccount:$minter_email" \
    "roles/cloudkms.signerVerifier" "user:owner@fugaro.test"
  run_check "$dir"
  expect_status "$status" 1 "another signing role on the ring"
  expect_row signer "roles/cloudkms.signerVerifier>user:owner@fugaro.test" "another signing role: the finding names it"
}

test_a_signing_ring_with_no_signer_grant_is_a_finding() {
  local dir=$work/ring-no-grant
  prepare "$dir"
  write_policy "$dir/ring-fugaro-licence-staging.json" roles/cloudkms.viewer "user:owner@fugaro.test"
  write_role "$dir" roles/cloudkms.viewer cloudkms.cryptoKeys.get
  run_check "$dir"
  expect_status "$status" 1 "a ring with no signer grant"
  expect_row signer "held by 'nobody'" "a ring with no signer grant: the finding"
  # A viewer on a ring with no signer grant is not an "other binding that reaches
  # signing" — the row must not pretend it is.
  expect_row signer "nothing can sign through" "a ring with no signer grant: what the empty grant means"
  expect_absent "$out" "roles/cloudkms.viewer" "a ring with no signer grant: a harmless role is not called a signing one"
}

test_a_ring_that_cannot_be_read_is_not_measured() {
  local dir=$work/no-ring
  prepare "$dir"
  printf '1\n' >"$dir/ring-fugaro-licence-staging.exit"
  printf 'ERROR: (gcloud.kms.keyrings.get-iam-policy) NOT_FOUND: the key ring does not exist\n' >"$dir/ring-fugaro-licence-staging.err"
  run_check "$dir"
  expect_status "$status" 2 "a signing ring that is not there"
  expect_row signer "did not answer" "a signing ring that is not there: the row"
  expect_row signer "Is this the ring" "a signing ring that is not there: the row asks the question that fixes it"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "fugaro-portal-handover" "a signing ring that is not there: nothing below it is read"
}

test_a_second_member_on_the_handover_ring_is_a_finding() {
  local dir=$work/handover-second
  prepare "$dir"
  write_multi_policy "$dir/ring-fugaro-portal-handover.json" \
    "$handover_role" "serviceAccount:$api_email,user:owner@fugaro.test"
  run_check "$dir"
  expect_status "$status" 1 "a second member on the handover ring"
  expect_row handover "is not the API's alone" "a second member on the handover ring: the finding"
  expect_row handover "user:owner@fugaro.test" "a second member on the handover ring: the finding names it"
}

test_an_account_that_may_be_impersonated_is_a_finding() {
  # actAs on the minter's account is the whole design's trust boundary: whoever
  # holds it signs with the minter's own identity.
  local dir=$work/actas-minter
  prepare "$dir"
  write_policy "$dir/sa-$minter_email.json" roles/iam.serviceAccountUser "user:owner@fugaro.test"
  run_check "$dir"
  expect_status "$status" 1 "actAs on the minter's account"
  expect_row actas "an account can be impersonated" "actAs on the minter's account: the finding"
  expect_row actas "$minter_email" "actAs on the minter's account: the finding names the account"
}

test_an_api_account_that_may_be_impersonated_is_a_finding() {
  local dir=$work/actas-api
  prepare "$dir"
  write_policy "$dir/sa-$api_email.json" roles/iam.serviceAccountTokenCreator "serviceAccount:deployer@$project.iam.gserviceaccount.com"
  run_check "$dir"
  expect_status "$status" 1 "actAs on the API's account"
  expect_row actas "serviceAccount:deployer@$project.iam.gserviceaccount.com" "actAs on the API's account: the finding names the member"
}

test_an_account_policy_that_cannot_be_read_is_not_measured() {
  local dir=$work/no-account-policy
  prepare "$dir"
  printf '1\n' >"$dir/sa-$minter_email.exit"
  printf 'ERROR: (gcloud.iam.service-accounts.get-iam-policy) PERMISSION_DENIED\n' >"$dir/sa-$minter_email.err"
  run_check "$dir"
  expect_status "$status" 2 "an account policy that cannot be read"
  expect_row actas "did not answer" "an account policy that cannot be read: the row"
  expect_row actas "the one identity that signs" "an account policy that cannot be read: the row says why it matters"
}

test_an_environment_that_is_neither_is_refused() {
  local dir=$work/bad-environment
  prepare "$dir"
  run_check "$dir" -- --environment sandbox
  expect_status "$status" 2 "an environment that is neither"
  expect_contains "$err" "is neither staging nor production" "an environment that is neither: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "an environment that is neither: nothing is read"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown argument: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "an unknown argument: nothing is read"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK571_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch directory"
  expect_contains "$out" "FACT scratch: INFO" "a kept scratch directory: the row"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

t test_help
t test_dry_run
t test_happy_path
t test_the_environment_names_the_ring
t test_a_project_that_is_not_the_plan_is_read_as_given
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_belong_is_refused_by_name
t test_a_coding_agents_session_is_refused
t test_xtrace_is_refused
t test_a_gcloud_that_does_not_answer_is_not_measured
t test_an_unauthenticated_gcloud_is_not_measured
t test_a_project_that_cannot_be_described_is_not_measured
t test_a_census_that_cannot_be_read_is_not_measured
t test_a_role_that_cannot_be_described_is_a_hole_in_the_census
t test_a_custom_role_with_use_to_sign_is_in_the_census
t test_a_second_invoker_of_the_minter_is_a_finding
t test_a_minter_nobody_may_call_is_a_finding
t test_an_api_policy_that_cannot_be_read_is_not_measured
t test_a_second_member_on_the_signing_ring_is_a_finding
t test_another_role_that_can_sign_on_the_ring_is_a_finding
t test_a_signing_ring_with_no_signer_grant_is_a_finding
t test_a_ring_that_cannot_be_read_is_not_measured
t test_a_second_member_on_the_handover_ring_is_a_finding
t test_an_account_that_may_be_impersonated_is_a_finding
t test_an_api_account_that_may_be_impersonated_is_a_finding
t test_an_account_policy_that_cannot_be_read_is_not_measured
t test_an_environment_that_is_neither_is_refused
t test_an_unknown_argument_is_refused
t test_kept_scratch_is_reported

if [ "$failures" -ne 0 ]; then
  echo "check571_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check571_test.sh: ok"
