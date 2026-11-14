#!/usr/bin/env bash
# Tests for scripts/live-checks/check314.sh, against a fake gcloud and a fake
# fugaro on PATH: nothing here reaches the network, there is no cloud project and
# no credential, and no agent runs any live step of the check.
#
# The two fakes model the four reads the check makes and nothing else. The fake
# gcloud answers `config get-value account` and `projects get-iam-policy` — the
# policy document, the account and the exit status each come out of the case's own
# files, so a case can say what the policy binds and to whom — and the fake fugaro
# answers `doctor --json` and `orchestrator ls --json`, so a case can say which
# installation is in force, whether doctor answers at all, and what the read T2 is
# turns on. Both log every call with its whole argv before answering, so a case
# can pin what the check read and, just as important, what it did not: a run that
# stops on the credential, the account or the policy must make no later call.
#
# Usage: bash scripts/live-checks/check314_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check314.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK314_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|t2|credential|viewer|listing):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(xtrace credential project belong database account policy viewer
  broader listing node rules t2 teardown)

# The sandbox installation every case runs against: its project, the GCP project
# it lives in, the Firebase project its database belongs to, and the person whose
# roles are read. None of them names `belong`.
PROJECT=sbx
GCP=sbx-proj
FIREBASE=sbx-proj
ACCOUNT=person@sandbox.example

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check314.sh's test: it answers the two reads that check makes
# and refuses to answer anything else, so a call this check should not have made
# is a failure rather than a silence. Every call is recorded with its whole argv
# on the first line of the run, before anything else, so a case can prove what
# was read -- and that a run which stopped earlier read nothing more.
set -euo pipefail
dir=${FAKE314_DIR:?FAKE314_DIR is not set}
printf 'gcloud %s\n' "$*" >>"$dir/calls"
status_of() { # file default
  if [ -f "$1" ]; then cat "$1"; else printf '%s\n' "$2"; fi
}
case "${1:-} ${2:-}" in
"config get-value")
  status=$(status_of "$dir/account.status" 0)
  if [ "$status" != 0 ]; then
    if [ -f "$dir/account.err" ]; then cat "$dir/account.err" >&2; fi
    exit "$status"
  fi
  if [ -f "$dir/account" ]; then cat "$dir/account"; fi
  ;;
"projects get-iam-policy")
  status=$(status_of "$dir/policy.status" 0)
  if [ "$status" != 0 ]; then
    if [ -f "$dir/policy.err" ]; then cat "$dir/policy.err" >&2; fi
    exit "$status"
  fi
  if [ -f "$dir/policy.json" ]; then cat "$dir/policy.json"; fi
  ;;
*)
  printf 'fake gcloud: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
GCLOUD_EOF
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check314.sh's test: `doctor --json` and `orchestrator ls
# --json` answer from the case's own files, every other call is an error, and
# every call is recorded with its whole argv -- so a case can say which
# installation doctor answered, what the read T2 is printed, and that a run which
# stopped earlier called nothing.
set -euo pipefail
dir=${FAKE314_DIR:?FAKE314_DIR is not set}
printf 'fugaro %s\n' "$*" >>"$dir/calls"
status_of() { # file default
  if [ -f "$1" ]; then cat "$1"; else printf '%s\n' "$2"; fi
}
case "${1:-}" in
doctor)
  # doctor prints its document and exits non-zero when a check failed, so the
  # fake does the same: the exit status is not how the premise is read.
  status=$(status_of "$dir/doctor.status" 0)
  if [ "$status" != 0 ]; then
    if [ -f "$dir/doctor.err" ]; then cat "$dir/doctor.err" >&2; fi
  fi
  if [ -f "$dir/doctor.json" ]; then cat "$dir/doctor.json"; fi
  exit "$status"
  ;;
orchestrator)
  status=$(status_of "$dir/listing.status" 0)
  if [ "$status" != 0 ]; then
    if [ -f "$dir/listing.err" ]; then cat "$dir/listing.err" >&2; fi
    exit "$status"
  fi
  if [ -f "$dir/listing.json" ]; then cat "$dir/listing.json"; fi
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/gcloud" "$1/bin/fugaro"
}

# The tools the check itself needs, symlinked into a directory of their own, so a
# case can run it with a PATH that holds everything BUT gcloud (or but fugaro).
write_tool_path() { # dir names...
  local dir=$1
  shift
  mkdir -p "$dir"
  local tool path
  for tool in "$@"; do
    path=$(command -v "$tool") || {
      fail "the test needs $tool on PATH"
      continue
    }
    ln -sf "$path" "$dir/$tool"
  done
}

# A person's credential, the shape gcloud writes for `auth application-default
# login`: a refresh token and no key, which is the point of T2. No real token is
# in it -- the value is a placeholder, and nothing here reads it.
write_adc() { # file
  cat >"$1" <<'JSON'
{"type":"authorized_user","client_id":"redacted.apps.googleusercontent.com","client_secret":"redacted","refresh_token":"redacted","quota_project_id":"sbx-proj"}
JSON
}

write_config() { # file
  cat >"$1" <<YAML
name: $PROJECT
gcp_project: $GCP
budget:
  rtdb_url: https://sbx-default-rtdb.firebaseio.com
  firebase_project: $FIREBASE
YAML
}

# The policy list a case hands the fake gcloud: MEMBER-ROLE pairs, one per line.
write_policy() { # file pairs...
  local file=$1
  shift
  python3 - "$file" "$@" <<'PY'
import json, shutil, sys

# A project's own IAM policy, built from the pairs a case names, plus one binding
# of another kind so the reader is never reading a document that holds only what
# it looks for.
out, pairs = sys.argv[1], sys.argv[2:]
bindings = []
for pair in pairs:
    member, _, role = pair.partition("=")
    bindings.append({"role": role, "members": [member]})
bindings.append({"role": "roles/run.invoker", "members": ["serviceAccount:jobs@sbx-proj.iam.gserviceaccount.com"]})
with open(out, "w") as fh:
    json.dump({"bindings": bindings, "etag": "redacted", "version": 3}, fh)
PY
}

write_listing() { # file ids...
  local file=$1
  shift
  python3 - "$file" "$@" <<'PY'
import json, sys

out, ids = sys.argv[1], sys.argv[2:]
entries = [{"kind": "local", "project": i, "lease_holder": "dogfood",
            "updated_at": "2026-11-14T10:00:00Z", "cached_at": "2026-11-14T10:00:00Z"} for i in ids]
with open(out, "w") as fh:
    json.dump({"version": 1, "kind": "ls", "orchestrators": entries}, fh)
PY
}

# ------------------------------------------------------------------- harness
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home/.config/gcloud" "$dir/home/.config/fugaro/projects"
  write_fakes "$dir"
  : >"$dir/state/calls"
  printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"us-central1"}}\n' "$PROJECT" "$GCP" >"$dir/state/doctor.json"
  printf '%s\n' "$ACCOUNT" >"$dir/state/account"
  write_adc "$dir/home/.config/gcloud/application_default_credentials.json"
  write_config "$dir/home/.config/fugaro/projects/$PROJECT.yaml"
  write_policy "$dir/state/policy.json" "user:$ACCOUNT=roles/firebasedatabase.viewer"
  write_listing "$dir/state/listing.json" "$PROJECT"
  status=0
  out=""
  RUN314_ARGS=()
  RUN314_ENV=()
  RUN314_BASH=(bash)
}

run314() {
  status=0
  out=$(env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE314_DIR="$dir/state" \
    ${RUN314_ENV[@]+"${RUN314_ENV[@]}"} \
    "${RUN314_BASH[@]}" "$script" ${RUN314_ARGS[@]+"${RUN314_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN314_ARGS=()
  RUN314_ENV=()
  RUN314_BASH=(bash)
}
run314_default() { run314 --project "$PROJECT" "$@"; }

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 314 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 314 RESULT END (exit " "$1: the block's end"
}

calls() { cat "$dir/state/calls"; }

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

expect_call() { # what needle
  expect_contains "$(calls)" "$2" "$1"
}
expect_no_call() { # what needle
  expect_absent "$(calls)" "$2" "$1"
}

# One call per line of the log, so a case can say how many were made instead of
# only which: a check that reads something twice is a different reading.
expect_call_count() { # what n
  local n
  n=$(wc -l <"$dir/state/calls" | tr -d ' ')
  [ "$n" = "$2" ] || fail "$1: expected $2 calls, got $n: $(tr '\n' ';' <"$dir/state/calls")"
}

# The CLI's own refusal, verbatim (internal/cli/budget.go's dbErr): the sentence a
# viewer-only person really gets when the subtree is out of the role's reach.
read_refusal() {
  printf '%s\n' "Error: project $PROJECT's budget database refused the read: you need the Viewer role on its Firebase project (GCP owners and editors have it); gcloud auth application-default login may be needed (rtdb: GET /orchestrator: 401 Unauthorized)"
}

# rewrite_script DEST OLD NEW: a copy of the check with one exact line replaced,
# so a self-test can prove a case's assertion is not vacuous rather than argue it.
rewrite_script() { # dest old new
  python3 - "$script" "$1" "$2" "$3" <<'PY'
import sys

src, dest, old, new = sys.argv[1:5]
with open(src) as fh:
    text = fh.read()
if old not in text:
    raise SystemExit("rewrite_script: the line to replace is not in the script: %r" % old)
with open(dest, "w") as fh:
    fh.write(text.replace(old, new, 1))
PY
  chmod +x "$1"
}

# --------------------------------------------------------------- the readings
# The whole point, end to end: a person whose own grant is the viewer role reads
# an orchestrator/ node, and the four reads are the four the check says they are.
test_the_person_with_viewer_reading_a_node_passes() {
  setup_case person-with-viewer
  run314_default
  expect_status "$status" 0 "person with viewer"
  expect_block_keys "person with viewer"
  expect_contains "$(fact_line xtrace)" "PASS" "person with viewer: not tracing"
  expect_contains "$(fact_line credential)" "authorized_user" "person with viewer: the credential is a person's"
  expect_contains "$(fact_line project)" "PASS — " "person with viewer: the installation is doctor's"
  expect_contains "$(fact_line belong)" "PASS — " "person with viewer: not belong"
  expect_contains "$(fact_line database)" "$FIREBASE" "person with viewer: the database's project"
  expect_contains "$(fact_line account)" "user:$ACCOUNT" "person with viewer: the person"
  expect_contains "$(fact_line policy)" "PASS — " "person with viewer: the policy answered"
  expect_contains "$(fact_line viewer)" "roles/firebasedatabase.viewer" "person with viewer: the viewer binding is the premise"
  expect_contains "$(fact_line broader)" "PASS — " "person with viewer: nothing wider is bound"
  expect_contains "$(fact_line listing)" "kind 'ls'" "person with viewer: the listing document"
  expect_contains "$(fact_line node)" "returned 1 orchestrator(s) ($PROJECT" "person with viewer: the node the read returned"
  expect_contains "$(fact_line rules)" "INFO" "person with viewer: the rules half is named as CG6's"
  expect_contains "$(fact_line t2)" "T2 HOLDS" "person with viewer: the verdict"
  expect_contains "$(fact_line teardown)" "wrote nothing" "person with viewer: nothing was left behind"
  expect_call "person with viewer: doctor" "fugaro doctor --json --project $PROJECT"
  expect_call "person with viewer: the person" "gcloud config get-value account"
  expect_call "person with viewer: the policy" "gcloud projects get-iam-policy $FIREBASE --format=json"
  expect_call "person with viewer: the read T2 is" "fugaro orchestrator ls --json --project $PROJECT"
  expect_call_count "person with viewer" 4
  expect_absent "$(calls)" "add-iam-policy-binding" "person with viewer: nothing is granted"
}

# The policy is read on the FIREBASE project, which is the one the database (and
# orchestrator/ with it) belongs to -- and which is not the installation's GCP
# project in the split layout.
test_the_policy_is_read_on_the_databases_own_project() {
  setup_case split-layout
  sed -i.bak "s|^  firebase_project: .*|  firebase_project: sbx-db-proj|" "$dir/home/.config/fugaro/projects/$PROJECT.yaml"
  rm -f "$dir/home/.config/fugaro/projects/$PROJECT.yaml.bak"
  run314_default
  expect_status "$status" 0 "split layout"
  expect_call "split layout: the policy is on the Firebase project" "projects get-iam-policy sbx-db-proj --format=json"
  expect_no_call "split layout: not on the installation's own project" "projects get-iam-policy $GCP --format=json"
  expect_contains "$(fact_line database)" "sbx-db-proj" "split layout: the row names the database's project"
  expect_contains "$(fact_line database)" "budget.firebase_project" "split layout: and where it was read"
}

# A role that covers the whole project makes the read unattributable to viewer:
# the run says T2 is NOT MEASURED instead of passing on the owner's read.
test_a_project_wide_role_means_t2_is_not_measured() {
  setup_case owner-too
  write_policy "$dir/state/policy.json" "user:$ACCOUNT=roles/firebasedatabase.viewer" "user:$ACCOUNT=roles/owner"
  run314_default
  expect_status "$status" 2 "owner too"
  expect_contains "$(fact_line viewer)" "PASS — " "owner too: viewer is still there"
  expect_contains "$(fact_line broader)" "INFO" "owner too: the wider role is recorded"
  expect_contains "$(fact_line broader)" "roles/owner" "owner too: named"
  expect_contains "$(fact_line broader)" "NOT MEASURED" "owner too: and it says what the run cannot conclude"
  expect_contains "$(fact_line listing)" "PASS — " "owner too: the read answered"
  expect_contains "$(fact_line t2)" "INFO" "owner too: no verdict about T2"
  expect_contains "$(fact_line t2)" "NOT MEASURED" "owner too: said in the verdict row"
  expect_contains "$(fact_line t2)" "roles/owner" "owner too: with the reason"
}

# The same, granted to the account's domain rather than to the account: the one
# cover a policy document can show without naming the person, so it is read.
test_a_role_on_the_domain_means_t2_is_not_measured() {
  setup_case domain-grant
  write_policy "$dir/state/policy.json" "user:$ACCOUNT=roles/firebasedatabase.viewer" "domain:sandbox.example=roles/editor"
  run314_default
  expect_status "$status" 2 "domain grant"
  expect_contains "$(fact_line viewer)" "PASS — " "domain grant: viewer is there"
  expect_contains "$(fact_line broader)" "roles/editor" "domain grant: the domain's role is read"
  expect_contains "$(fact_line broader)" "domain:sandbox.example" "domain grant: and the domain is named"
  expect_contains "$(fact_line t2)" "NOT MEASURED" "domain grant: T2 is not measured"
}

# No viewer binding at all: the premise T2 rests on is missing, and the row says
# both what was bound and the command that grants the role.
test_a_missing_viewer_binding_is_a_fail() {
  setup_case no-viewer
  write_policy "$dir/state/policy.json" "user:$ACCOUNT=roles/browser"
  run314_default
  expect_status "$status" 1 "no viewer"
  expect_contains "$(fact_line viewer)" "FAIL — " "no viewer: the row fails"
  expect_contains "$(fact_line viewer)" "roles/browser" "no viewer: it names what was bound"
  expect_contains "$(fact_line viewer)" "add-iam-policy-binding $FIREBASE --member=user:$ACCOUNT --role=roles/firebasedatabase.viewer" \
    "no viewer: and the command that grants it"
  expect_contains "$(fact_line t2)" "T2 does NOT hold" "no viewer: the verdict"
  expect_contains "$(fact_line t2)" "viewer" "no viewer: the verdict names the row"
}

# A grant to somebody else is not a grant to this person, and the check reads the
# member and not only the role.
test_a_viewer_binding_for_somebody_else_is_not_a_pass() {
  setup_case other-person
  write_policy "$dir/state/policy.json" "user:colleague@sandbox.example=roles/firebasedatabase.viewer"
  run314_default
  expect_status "$status" 1 "another person holds viewer"
  expect_contains "$(fact_line viewer)" "FAIL — " "another person holds viewer: the row fails"
  expect_absent "$(fact_line viewer)" "binds roles/firebasedatabase.viewer to user:$ACCOUNT" \
    "another person holds viewer: the other person's binding is not read as this one's"
}

# The read T2 is, refused: the CLI's own sentence is quoted and read as T2
# answered NO, because that sentence is the permissions refusal and not an outage.
test_a_refused_read_is_the_answer_t2_turns_on() {
  setup_case refused-read
  read_refusal >"$dir/state/listing.err"
  printf '1\n' >"$dir/state/listing.status"
  run314_default
  expect_status "$status" 1 "refused read"
  expect_contains "$(fact_line listing)" "FAIL — " "refused read: the row fails"
  expect_contains "$(fact_line listing)" "refused the read" "refused read: the CLI's sentence is quoted"
  expect_contains "$(fact_line listing)" "T2 is answered NO" "refused read: and read as T2's answer"
  expect_contains "$(fact_line node)" "INFO" "refused read: nothing is said about the subtree's contents"
  expect_contains "$(fact_line t2)" "T2 does NOT hold" "refused read: the verdict"
}

# A command that failed for another reason is quoted too, and never read as the
# permissions refusal.
test_a_read_that_failed_for_another_reason_is_not_read_as_the_refusal() {
  setup_case read-did-not-answer
  printf 'Error: the connection was reset before the read could be made\n' >"$dir/state/listing.err"
  printf '2\n' >"$dir/state/listing.status"
  run314_default
  expect_status "$status" 1 "read that did not answer"
  expect_contains "$(fact_line listing)" "the connection was reset" "read that did not answer: the sentence is quoted"
  expect_absent "$(fact_line listing)" "T2 is answered NO" "read that did not answer: an outage is not the permissions answer"
}

# The listing document is CG4's, and a build that printed something else cannot
# be read as T2's answer even though the command exited 0.
test_a_listing_that_is_not_the_document_fails() {
  setup_case wrong-document
  printf '{"version":1,"kind":"plan","orchestrators":[]}\n' >"$dir/state/listing.json"
  run314_default
  expect_status "$status" 1 "wrong document"
  expect_contains "$(fact_line listing)" "FAIL — " "wrong document: the row fails"
  expect_contains "$(fact_line listing)" "not the listing document" "wrong document: it says which document it wanted"
  expect_contains "$(fact_line node)" "INFO" "wrong document: no contents are read out of it"
}

# An empty subtree settles the permission and not the thing T2 is about, so the
# run says NOT MEASURED rather than passing on a read that returned nothing.
test_an_empty_subtree_is_not_measured() {
  setup_case empty-subtree
  write_listing "$dir/state/listing.json"
  run314_default
  expect_status "$status" 2 "empty subtree"
  expect_contains "$(fact_line listing)" "PASS — " "empty subtree: the read answered"
  expect_contains "$(fact_line node)" "INFO" "empty subtree: the node row says what was not observed"
  expect_contains "$(fact_line node)" "orchestrator run" "empty subtree: and how to put a node there"
  expect_contains "$(fact_line t2)" "NOT MEASURED" "empty subtree: no verdict about T2"
}

# The credential T2 is about is a person's: a service account's key answers a
# different question, and the run stops before it reads anything else.
test_a_service_account_credential_stops_before_any_call() {
  setup_case service-account-credential
  printf '{"type":"service_account","private_key_id":"redacted","client_email":"jobs@sbx-proj.iam.gserviceaccount.com"}\n' \
    >"$dir/home/.config/gcloud/application_default_credentials.json"
  run314_default
  expect_status "$status" 2 "service account credential"
  expect_contains "$(fact_line credential)" "FAIL — " "service account credential: the row fails"
  expect_contains "$(fact_line credential)" "service_account" "service account credential: the type is named"
  expect_contains "$(fact_line credential)" "T2 is about a PERSON's credential" "service account credential: and why it cannot measure T2"
  expect_no_calls "service account credential"
}

# No application-default credential at all: the CLI's own read would fail, so the
# check says what to run rather than reporting somebody else's roles.
test_no_application_default_credential_stops() {
  setup_case no-credential
  rm -f "$dir/home/.config/gcloud/application_default_credentials.json"
  run314_default
  expect_status "$status" 2 "no credential"
  expect_contains "$(fact_line credential)" "FAIL — " "no credential: the row fails"
  expect_contains "$(fact_line credential)" "gcloud auth application-default login" "no credential: the command that fixes it"
  expect_no_calls "no credential"
}

# GOOGLE_APPLICATION_CREDENTIALS is what the CLI reads first, so it is what this
# check reads first: a federation's credential file is not a person's either.
test_google_application_credentials_names_the_file() {
  setup_case named-credential
  printf '{"type":"external_account","audience":"redacted"}\n' >"$dir/federated.json"
  RUN314_ENV=(GOOGLE_APPLICATION_CREDENTIALS="$dir/federated.json")
  run314_default
  expect_status "$status" 2 "named credential"
  expect_contains "$(fact_line credential)" "$dir/federated.json" "named credential: the path the variable names"
  expect_contains "$(fact_line credential)" "external_account" "named credential: the type is named"
  expect_no_calls "named credential"
}

# A credential file that is not the document the CLI would read is a stop: this
# check does not guess at a type it could not read.
test_a_credential_file_that_is_not_json_stops() {
  setup_case credential-not-json
  printf 'not json at all\n' >"$dir/home/.config/gcloud/application_default_credentials.json"
  run314_default
  expect_status "$status" 2 "credential that is not JSON"
  expect_contains "$(fact_line credential)" "not JSON" "credential that is not JSON: the row says what it got"
  expect_no_calls "credential that is not JSON"
}

# A credential whose type is absent is not a person's either: the check reads the
# field and never assumes it.
test_a_credential_with_no_type_is_a_fail() {
  setup_case credential-without-type
  printf '{"refresh_token":"redacted"}\n' >"$dir/home/.config/gcloud/application_default_credentials.json"
  run314_default
  expect_status "$status" 2 "credential without a type"
  expect_contains "$(fact_line credential)" "FAIL — " "credential without a type: the row fails"
  expect_contains "$(fact_line credential)" "'empty'" "credential without a type: the empty type is shown"
  expect_no_calls "credential without a type"
}

# The self-test for the three above: with the person's type also matching the
# service account's, the same case's row passes -- so the case is about the type
# and not about the file being read at all.
test_the_credential_type_check_is_not_vacuous() {
  setup_case credential-type-self-test
  printf '{"type":"service_account","private_key_id":"redacted"}\n' \
    >"$dir/home/.config/gcloud/application_default_credentials.json"
  local copy=$dir/check314-service-account.sh
  # A rewrite that cannot find its line fails the case rather than skipping it:
  # a silent skip is the vacuity this self-test exists to deny.
  if ! rewrite_script "$copy" "  authorized_user)" "  authorized_user | service_account)"; then
    fail "self-test: the credential type line is not in the script to rewrite"
    return 0
  fi
  script=$copy
  run314_default
  script=$here/check314.sh
  expect_status "$status" 0 "self-test: the mutated check passes a service account's credential"
  expect_contains "$(fact_line credential)" "PASS — " "self-test: the row is the type test and nothing else"
}

# gcloud naming nobody: the check says which command puts that right instead of
# reading a policy for an account it does not know.
test_gcloud_naming_no_account_stops() {
  setup_case no-account
  printf '(unset)\n' >"$dir/state/account"
  run314_default
  expect_status "$status" 2 "no account"
  expect_contains "$(fact_line account)" "FAIL — " "no account: the row fails"
  expect_contains "$(fact_line account)" "gcloud auth login" "no account: the command that fixes it"
  expect_no_call "no account: the policy is not read" "get-iam-policy"
  expect_no_call "no account: and the listing is not read" "orchestrator ls"
}

# An account that is a service account's is not the person T2 is about, whatever
# gcloud is configured with.
test_a_service_account_address_stops() {
  setup_case service-account-address
  printf 'jobs@%s.iam.gserviceaccount.com\n' "$GCP" >"$dir/state/account"
  run314_default
  expect_status "$status" 2 "service account address"
  expect_contains "$(fact_line account)" "FAIL — " "service account address: the row fails"
  expect_contains "$(fact_line account)" "service account's address" "service account address: and says why"
  expect_no_call "service account address: the policy is not read" "get-iam-policy"
}

# --account is the owner's own answer to "which person is this about", and where
# it is given gcloud's configured account is not asked at all.
test_account_flag_overrides_gclouds_answer() {
  setup_case account-flag
  write_policy "$dir/state/policy.json" "user:other@sandbox.example=roles/firebasedatabase.viewer"
  run314 "--project" "$PROJECT" "--account" "other@sandbox.example"
  expect_status "$status" 0 "account flag"
  expect_contains "$(fact_line account)" "user:other@sandbox.example" "account flag: the person is the one typed"
  expect_contains "$(fact_line account)" "--account" "account flag: and the row says where it came from"
  expect_no_call "account flag: gcloud's own answer is not asked" "config get-value"
}

# A policy the person may not read is a stop with gcloud's own sentence: it says
# nothing about the viewer role, and the rows after it are not offered as answers.
test_a_refused_policy_read_stops() {
  setup_case refused-policy
  printf 'ERROR: (gcloud.projects.get-iam-policy) PERMISSION_DENIED: caller does not have permission\n' >"$dir/state/policy.err"
  printf '1\n' >"$dir/state/policy.status"
  run314_default
  expect_status "$status" 2 "refused policy"
  expect_contains "$(fact_line policy)" "FAIL — " "refused policy: the row fails"
  expect_contains "$(fact_line policy)" "PERMISSION_DENIED" "refused policy: gcloud's sentence is quoted"
  expect_contains "$(fact_line policy)" "getIamPolicy" "refused policy: and the permission it needs is named"
  expect_no_call "refused policy: the read T2 is is not made" "orchestrator ls"
}

# A policy that is not a policy is a stop too: the reader will not read roles out
# of a document it does not recognise.
test_a_policy_that_is_not_a_policy_stops() {
  setup_case policy-not-a-policy
  printf '{"bindings":"none"}\n' >"$dir/state/policy.json"
  run314_default
  expect_status "$status" 2 "policy that is not a policy"
  expect_contains "$(fact_line policy)" "not a project's IAM policy" "policy that is not a policy: the row says what it got"
  expect_no_call "policy that is not a policy: the read T2 is is not made" "orchestrator ls"
}

# doctor's exit status is not what the premise is read from: a run that reports
# warnings still answers which installation is in force, and the check goes on.
test_a_doctor_that_exits_nonzero_still_answers_the_project() {
  setup_case doctor-warns
  printf '1\n' >"$dir/state/doctor.status"
  printf 'some check failed\n' >"$dir/state/doctor.err"
  run314_default
  expect_status "$status" 0 "doctor that exits non-zero"
  expect_contains "$(fact_line project)" "PASS — " "doctor that exits non-zero: the answer is still read"
}

# doctor not answering at all is a stop when nothing was typed, and a reading of
# what was typed when a project was: the CLI then refuses a config that is not it.
test_doctor_refused_with_a_project_typed_carries_on() {
  setup_case doctor-refused-with-project
  printf '1\n' >"$dir/state/doctor.status"
  printf 'Error: no project config\n' >"$dir/state/doctor.err"
  printf '{"ok":false,"checks":[]}\n' >"$dir/state/doctor.json"
  run314_default
  expect_status "$status" 0 "doctor refused, project typed"
  expect_contains "$(fact_line project)" "INFO" "doctor refused, project typed: the row says where the name came from"
  expect_contains "$(fact_line project)" "no project config" "doctor refused, project typed: and quotes doctor"
  expect_contains "$(fact_line project)" "$PROJECT" "doctor refused, project typed: naming the project typed"
  expect_call "doctor refused, project typed: the listing still carries --project" "orchestrator ls --json --project $PROJECT"
}

test_doctor_refused_with_no_project_typed_stops() {
  setup_case doctor-refused-no-project
  printf '1\n' >"$dir/state/doctor.status"
  printf 'Error: several project configs are in force\n' >"$dir/state/doctor.err"
  printf '{"ok":false,"checks":[]}\n' >"$dir/state/doctor.json"
  run314
  expect_status "$status" 2 "doctor refused, nothing typed"
  expect_contains "$(fact_line project)" "FAIL — " "doctor refused, nothing typed: the row fails"
  expect_contains "$(fact_line project)" "several project configs" "doctor refused, nothing typed: doctor's sentence is quoted"
  expect_call_count "doctor refused, nothing typed" 1
}

# A project that is not the one in force is two installations, and the check
# picks neither: it would otherwise read one project's policy and another's
# database.
test_a_project_that_is_not_doctors_stops() {
  setup_case project-mismatch
  printf '{"ok":true,"project":{"name":"other","gcp_project":"other-proj"}}\n' >"$dir/state/doctor.json"
  run314_default
  expect_status "$status" 2 "project mismatch"
  expect_contains "$(fact_line project)" "FAIL — " "project mismatch: the row fails"
  expect_contains "$(fact_line project)" "you typed --project $PROJECT" "project mismatch: what was typed"
  expect_contains "$(fact_line project)" "answers other" "project mismatch: and what is in force"
  expect_no_call "project mismatch: no policy is read" "get-iam-policy"
}

# belong is refused in what you typed, before anything is read at all.
test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run314 --project belong
  expect_status "$status" 2 "belong typed"
  expect_contains "$(fact_line belong)" "FAIL — " "belong typed: the row fails"
  expect_contains "$(fact_line belong)" "owner's own installation" "belong typed: and says whose it is"
  expect_no_calls "belong typed"
}

# And in what doctor answers: the check may be pointed at belong by the config in
# force instead of by the command line, which is the case the guard is really for.
test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-answered
  printf '{"ok":true,"project":{"name":"belong","gcp_project":"belong-prod"}}\n' >"$dir/state/doctor.json"
  run314
  expect_status "$status" 2 "belong answered"
  expect_contains "$(fact_line belong)" "FAIL — " "belong answered: the row fails"
  expect_contains "$(fact_line belong)" "belong" "belong answered: the name is shown"
  expect_call_count "belong answered: doctor was asked, nothing else" 1
}

# The config file this check reads the database's project out of is the one the
# CLI would read, --config included -- otherwise it would name a database nobody
# is reading.
test_the_config_can_be_named_by_fugaro_flags() {
  setup_case named-config
  local cfg=$dir/elsewhere.yaml
  cat >"$cfg" <<YAML
name: $PROJECT
gcp_project: $GCP
budget:
  rtdb_url: https://elsewhere.firebaseio.com
  firebase_project: named-db-proj
YAML
  RUN314_ARGS=(--fugaro-flags "--config $cfg")
  run314_default
  expect_status "$status" 0 "named config"
  expect_call "named config: the policy is read on the named database's project" "projects get-iam-policy named-db-proj --format=json"
  expect_contains "$(fact_line database)" "$cfg" "named config: the row names the file it read"
  expect_contains "$(fact_line database)" "named-db-proj" "named config: and the project it named"
  expect_call "named config: the flags reach fugaro" "fugaro doctor --json --config $cfg --project $PROJECT"
}

test_a_config_without_a_gcp_project_stops() {
  setup_case config-without-gcp-project
  printf 'name: %s\nbudget:\n  rtdb_url: https://x.firebaseio.com\n  firebase_project: x\n' "$PROJECT" \
    >"$dir/home/.config/fugaro/projects/$PROJECT.yaml"
  run314_default
  expect_status "$status" 2 "config without gcp_project"
  expect_contains "$(fact_line database)" "FAIL — " "config without gcp_project: the row fails"
  expect_no_call "config without gcp_project: no policy is read" "get-iam-policy"
}

# A dry run reads nothing and calls nothing -- and still refuses belong, which is
# a string test on what was typed.
test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry-run
  RUN314_ARGS=(--dry-run)
  run314 --project "$PROJECT"
  expect_status "$status" 0 "dry run"
  expect_contains "$(cat "$dir/err")" "dry run: NOTHING below was done" "dry run: the plan"
  expect_contains "$(cat "$dir/err")" "orchestrator ls --json" "dry run: the read T2 is is in the plan"
  expect_absent "$out" "FACT credential" "dry run: no reading of the credential is offered"
  expect_contains "$(fact_line dry-run)" "PASS" "dry run: its own row"
  expect_no_calls "dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-run-belong
  RUN314_ARGS=(--dry-run)
  run314 --project belong
  expect_status "$status" 2 "dry run, belong"
  expect_contains "$(fact_line belong)" "FAIL — " "dry run, belong: the row fails"
  expect_no_calls "dry run, belong"
}

# A shell that arrived tracing prints every line of the run, and this run opens
# the file that holds the application-default refresh token: it is refused.
test_a_shell_that_arrived_tracing_is_refused() {
  setup_case tracing
  RUN314_BASH=(bash -x)
  run314_default
  expect_status "$status" 2 "tracing"
  expect_contains "$(fact_line xtrace)" "FAIL — " "tracing: the row fails"
  expect_contains "$(fact_line xtrace)" "tracing" "tracing: and says why"
  expect_no_calls "tracing: nothing is read before the guard"
}

# A tool that is not there stops the run with one line naming it, rather than
# reading part of the story.
test_a_missing_tool_stops_with_one_line() {
  setup_case missing-tool
  write_tool_path "$dir/tools" bash mktemp sed grep head cut tr python3 rm chmod cat mkdir ls
  status=0
  out=$(env -i PATH="$dir/tools" HOME="$dir/home" TMPDIR="$dir/tmp" \
    bash "$script" --project "$PROJECT" 2>"$dir/err") || status=$?
  expect_status "$status" 2 "missing gcloud"
  expect_contains "$(cat "$dir/err")" "Check 314 needs 'gcloud' on PATH" "missing gcloud: the line names it"
  expect_no_calls "missing gcloud"
}

# The self-test for the wider-role reading: with no role covering the project,
# the same case's read is attributed to viewer and T2 passes -- so the NOT
# MEASURED verdict above is the covering role doing it, and not the run failing
# for another reason.
test_the_wider_role_check_is_not_vacuous() {
  setup_case wider-role-self-test
  write_policy "$dir/state/policy.json" "user:$ACCOUNT=roles/firebasedatabase.viewer" "user:$ACCOUNT=roles/owner"
  local copy=$dir/check314-no-covering-roles.sh
  if ! rewrite_script "$copy" "COVERING_ROLES=(roles/owner roles/editor roles/firebasedatabase.admin)" \
    "COVERING_ROLES=(roles/nothing-covers-this)"; then
    fail "self-test: the covering-roles line is not in the script to rewrite"
    return 0
  fi
  script=$copy
  run314_default
  script=$here/check314.sh
  expect_status "$status" 0 "self-test: the mutated check passes a project-wide role"
  expect_contains "$(fact_line t2)" "T2 HOLDS" "self-test: the covering role is what made the run say NOT MEASURED"
}

test_usage_and_arguments() {
  setup_case usage
  run314 --help
  expect_status "$status" 0 "usage"
  expect_contains "$out" "usage: check314.sh" "usage: the line"
  expect_absent "$out" "CHECK 314 RESULT" "usage: no block for a help that made nothing"
  run314 --project
  expect_status "$status" 2 "usage: a flag with no value"
  expect_contains "$(cat "$dir/err")" "--project needs a Fugaro project name" "usage: the flag's own message"
  run314 --nonsense
  expect_status "$status" 2 "usage: an unknown argument"
  expect_contains "$(cat "$dir/err")" "unknown argument '--nonsense'" "usage: the unknown argument is named"
  expect_no_calls "usage"
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

t test_the_person_with_viewer_reading_a_node_passes
t test_the_policy_is_read_on_the_databases_own_project
t test_a_project_wide_role_means_t2_is_not_measured
t test_a_role_on_the_domain_means_t2_is_not_measured
t test_a_missing_viewer_binding_is_a_fail
t test_a_viewer_binding_for_somebody_else_is_not_a_pass
t test_a_refused_read_is_the_answer_t2_turns_on
t test_a_read_that_failed_for_another_reason_is_not_read_as_the_refusal
t test_a_listing_that_is_not_the_document_fails
t test_an_empty_subtree_is_not_measured
t test_a_service_account_credential_stops_before_any_call
t test_no_application_default_credential_stops
t test_google_application_credentials_names_the_file
t test_a_credential_file_that_is_not_json_stops
t test_a_credential_with_no_type_is_a_fail
t test_the_credential_type_check_is_not_vacuous
t test_gcloud_naming_no_account_stops
t test_a_service_account_address_stops
t test_account_flag_overrides_gclouds_answer
t test_a_refused_policy_read_stops
t test_a_policy_that_is_not_a_policy_stops
t test_a_doctor_that_exits_nonzero_still_answers_the_project
t test_doctor_refused_with_a_project_typed_carries_on
t test_doctor_refused_with_no_project_typed_stops
t test_a_project_that_is_not_doctors_stops
t test_belong_is_refused_in_what_you_typed
t test_belong_is_refused_in_what_doctor_answers
t test_the_config_can_be_named_by_fugaro_flags
t test_a_config_without_a_gcp_project_stops
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_shell_that_arrived_tracing_is_refused
t test_a_missing_tool_stops_with_one_line
t test_the_wider_role_check_is_not_vacuous
t test_usage_and_arguments

if [ "$failures" -ne 0 ]; then
  echo "check314_test: $failures failure(s)" >&2
  exit 1
fi
echo "check314_test: all tests passed" >&2
