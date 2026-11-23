#!/usr/bin/env bash
# Tests for scripts/live-checks/check-892-iam.sh. NOTHING here is a cloud, a
# project or a database: every case writes the installation's local config in a
# temporary directory and drives the check against fake `gcloud`, `curl` and
# `fugaro` on PATH, whose answers the case owns and which record their whole
# argv and what their stdin turned out to be.
#
# The readings are the point. Check 892 compares the roles the grants node
# places each principal in with the IAM the principal really holds, and FAILs on
# one rule only: a principal the node places as operator or viewer, or scopes to
# repositories, that holds roles/owner, roles/editor or
# roles/resourcemanager.projectIamAdmin on the GCP project. So the cases bend
# exactly that: an operator with owner, a viewer with editor, an operator with
# many legitimate grants, an owner the node does not place, a budget admin, and
# a local admin list that has fallen behind the node.
#
# Every run has its stdin closed and a timeout, so a check that waited for input
# would end the case as a failure rather than hang it.
#
# Usage: bash scripts/live-checks/check-892-iam_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-892-iam.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK892_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK892_TEST_HEAD:-2400}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; out: $(printf '%s' "$out" | head -c 1600); err: $(printf '%s' "$err" | head -c 800)"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected a row '$2' reading $3; got: $(printf '%s' "$1" | head -c "${CHECK892_TEST_HEAD:-2400}")"
}
expect_no_row_state() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    fail "$4: expected no row '$2' reading $3"
  fi
}

TOKEN_MARKER=chk892-fake-access-token-marker
CONFIG_MARKER=chk892-config-value-marker

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

# Each fake logs its argv and its stdin: a read on /dev/null returns EOF at once
# (status 1), where an open pipe blocks past the bound (a status above 128).
cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'gcloud %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE892_LOG:?}"
dir=${FAKE892_DIR:?}
case "$1 $2" in
"auth print-access-token")
  printf '%s\n' "${FAKE892_TOKEN:?}"
  ;;
"projects get-iam-policy")
  f=$dir/iam-project-$3.json
  [ -f "$f" ] || { printf 'ERROR: (gcloud.projects.get-iam-policy) project %s not found\n' "$3" >&2; exit 1; }
  cat "$f"
  ;;
"iam service-accounts")
  case "$3" in
  list) cat "$dir/sa-list.json" ;;
  get-iam-policy)
    f=$dir/iam-sa-$4.json
    [ -f "$f" ] || { printf 'ERROR: (gcloud.iam.service-accounts.get-iam-policy) %s not found\n' "$4" >&2; exit 1; }
    cat "$f"
    ;;
  *) printf 'fake gcloud: unexpected verb %s\n' "$*" >&2; exit 64 ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected verb %s\n' "$*" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

# The fake curl answers only through the config file the check hands it: the
# token must be in that file and nowhere else, so an Authorization header that
# reached the argv instead is answered 401.
cat >"$fakebin/curl" <<'FAKE'
#!/usr/bin/env bash
set -uo pipefail
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'curl %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE892_LOG:?}"
dir=${FAKE892_DIR:?}
conf="" out="" fmt="" url=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config | -K) conf=$2; shift 2 ;;
  -o | --output) out=$2; shift 2 ;;
  -w | --write-out) fmt=$2; shift 2 ;;
  --max-time | -m) shift 2 ;;
  -q | -s | -S | -sS | --silent | --show-error | --fail) shift ;;
  -*) printf 'fake curl: unexpected flag %s\n' "$1" >&2; exit 64 ;;
  *) url=$1; shift ;;
  esac
done
code=401
body='{"error":"Permission denied"}'
if [ -n "$conf" ] && grep -q "Authorization: Bearer ${FAKE892_TOKEN:?}" "$conf" 2>/dev/null; then
  perm=$(stat -c '%a' "$conf" 2>/dev/null || stat -f '%Lp' "$conf" 2>/dev/null)
  printf 'curl-config-mode %s\n' "$perm" >>"$FAKE892_LOG"
  case "$url" in
  */config/admin/access.json)
    code=$(cat "$dir/grants-status" 2>/dev/null || printf 200)
    body=$(cat "$dir/grants.json")
    ;;
  *) code=404 body=null ;;
  esac
fi
if [ -n "$out" ]; then printf '%s' "$body" >"$out"; else printf '%s' "$body"; fi
if [ -n "$fmt" ]; then printf '%s' "${fmt//%\{http_code\}/$code}"; fi
FAKE
chmod +x "$fakebin/curl"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
printf 'fugaro %s\n' "$*" >>"${FAKE892_LOG:?}"
printf 'fake fugaro: Check 892 calls no fugaro command\n' >&2
exit 64
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

case_dir() { # case_dir <name>
  cdir=$work/$1
  answers=$cdir/answers
  confighome=$cdir/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS=$cdir/calls
  : >"$CALLS"
  write_config
  write_project_iam
  write_firebase_iam
  write_accounts
  write_grants
}

# The installation's own config as `fugaro update` leaves it, in the indented
# shape yaml.v3 writes. The API key's value stands in for anything the check
# must read past without printing.
write_config() { # write_config [admins...]
  local admins=("$@")
  [ ${#admins[@]} -gt 0 ] || admins=(user:ada@example.com)
  {
    printf 'version: 3\n'
    printf 'name: fugaro\n'
    printf 'gcp_project: fugaro-gcp\n'
    printf 'region: us-east5\n'
    printf 'terraform:\n'
    printf '    state_bucket: fugaro-gcp-tfstate\n'
    printf '    operators:\n'
    printf '        - user:ada@example.com\n'
    printf '        - user:otto@example.com\n'
    printf '    admins:\n'
    for a in "${admins[@]}"; do printf '        - %s\n' "$a"; done
    printf '    budget_admins:\n'
    printf '        - user:bea@example.com\n'
    printf 'budget:\n'
    printf '    rtdb_url: https://fugaro-fb-default-rtdb.firebaseio.com\n'
    printf '    firebase_project: fugaro-fb\n'
    printf '    firebase_api_key: %s\n' "$CONFIG_MARKER"
    printf 'repos:\n'
    printf '    acme/app:\n'
    printf '        workflows:\n'
    printf '            - default\n'
  } >"$confighome/fugaro/projects/fugaro.yaml"
}

# The project's policy: Ada is the placed admin and an owner, Olga an unplaced
# owner, and Otto the operator with the grants an operator legitimately holds.
write_project_iam() { # write_project_iam [extra binding json...]
  local extra=""
  for b in "$@"; do extra="${extra},${b}"; done
  cat >"$answers/iam-project-fugaro-gcp.json" <<JSON
{"bindings":[
 {"role":"roles/owner","members":["user:ada@example.com","user:olga@example.com"]},
 {"role":"projects/fugaro-gcp/roles/fugaroLauncher","members":["user:otto@example.com","user:ada@example.com"]},
 {"role":"roles/logging.viewer","members":["user:otto@example.com","user:vic@example.com"]},
 {"role":"roles/secretmanager.secretVersionAdder","members":["user:otto@example.com"]},
 {"role":"roles/cloudbuild.builds.editor","members":["user:otto@example.com"]}${extra}
],"etag":"BwYAAA","version":1}
JSON
}

write_firebase_iam() {
  cat >"$answers/iam-project-fugaro-fb.json" <<'JSON'
{"bindings":[
 {"role":"roles/firebasedatabase.admin","members":["user:ada@example.com","user:olga@example.com","user:bea@example.com"]},
 {"role":"roles/firebasedatabase.viewer","members":["user:otto@example.com"]}
],"etag":"BwYAAB","version":1}
JSON
}

write_accounts() {
  cat >"$answers/sa-list.json" <<'JSON'
[
 {"email":"fugaro-b-acme-app-0123abcd@fugaro-gcp.iam.gserviceaccount.com","displayName":"Fugaro build github-acme-app-0123456789abcdef"},
 {"email":"fugaro-acme-app-default-4567@fugaro-gcp.iam.gserviceaccount.com","displayName":"Fugaro job github-acme-app-0123456789abcdef default"},
 {"email":"fugaro-deployer@fugaro-gcp.iam.gserviceaccount.com","displayName":"Fugaro deployer"}
]
JSON
  cat >"$answers/iam-sa-fugaro-b-acme-app-0123abcd@fugaro-gcp.iam.gserviceaccount.com.json" <<'JSON'
{"bindings":[{"role":"roles/iam.serviceAccountUser","members":["user:otto@example.com","user:ada@example.com"]}],"etag":"BwYAAC"}
JSON
  cat >"$answers/iam-sa-fugaro-acme-app-default-4567@fugaro-gcp.iam.gserviceaccount.com.json" <<'JSON'
{"bindings":[{"role":"roles/iam.serviceAccountUser","members":["user:otto@example.com"]}],"etag":"BwYAAD"}
JSON
}

# The node as fugaro update writes it: Ada admin, Otto operator, Vic viewer,
# and Bea (a budget admin) an operator scoped to one repository.
write_grants() { # write_grants [json]
  if [ $# -gt 0 ]; then
    printf '%s' "$1" >"$answers/grants.json"
    return
  fi
  cat >"$answers/grants.json" <<'JSON'
{"grants":[
 {"user":"user:ada@example.com","role":"admin","scope":"project"},
 {"user":"user:otto@example.com","role":"operator","scope":"project"},
 {"user":"user:vic@example.com","role":"viewer","scope":"project"},
 {"user":"user:bea@example.com","role":"operator","scope":"github/acme/app"}
]}
JSON
}

# ------------------------------------------------------------------ the runs

# run892_with_stdin runs the check with a timeout (perl's alarm, present on
# both macOS and Linux) and the stdin it is handed.
run892_raw() { # run892_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE892_LOG="${CALLS:-/dev/null}" FAKE892_DIR="${answers:-$work}" \
    FAKE892_TOKEN="$TOKEN_MARKER" XDG_CONFIG_HOME="${confighome:-$work/none}" HOME="$work/home" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run892() { # run892 [members...]
  run892_raw fugaro "$@" </dev/null
}

# ------------------------------------------------------------------- the cases

test_an_admin_maps_to_tf_admin() {
  case_dir admin
  run892 ada@example.com
  expect_status "$status" 0 "an admin"
  expect_contains "$out" "CHECK 892 RESULT: PASS (exit 0)" "an admin"
  expect_contains "$out" "user:ada@example.com: TF-admin=yes DB-admin=yes operator=yes" "an admin maps to TF-admin"
  expect_contains "$out" "RC8" "an admin's rows name the TF-admin rows"
  expect_contains "$out" "RC11" "an admin's rows name the DB-admin rows"
  expect_contains "$out" "RC4" "an admin's rows name the operator rows"
  expect_row "$out" admin-in-name PASS "an admin"
  expect_row "$out" local-admins PASS "an admin"
  expect_contains "$out" "MATRIX" "the whole matrix is printed"
  expect_contains "$out" "user:ada@example.com  node=admin@project" "the matrix names the node's role"
}

test_an_operator_with_many_grants_passes() {
  case_dir operator
  run892 otto@example.com
  expect_status "$status" 0 "an operator with many grants"
  expect_row "$out" admin-in-name PASS "an operator with many grants"
  expect_contains "$out" "user:otto@example.com: TF-admin=no DB-admin=no operator=yes" "an operator maps to operator only"
  expect_contains "$out" "roles/secretmanager.secretVersionAdder" "the operator's grants are printed in the matrix"
  expect_contains "$out" "roles/iam.serviceAccountUser@fugaro-acme-app-default-4567" "the operator's account grants are printed"
  expect_not_contains "$out" "RC11" "an operator exercises no DB-admin row"
}

test_an_operator_with_owner_fails() {
  case_dir operator_owner
  write_project_iam '{"role":"roles/owner","members":["user:otto@example.com"]}'
  run892
  expect_status "$status" 1 "an operator with owner"
  expect_contains "$out" "CHECK 892 RESULT: FAIL (exit 1)" "an operator with owner"
  expect_row "$out" admin-in-name FAIL "an operator with owner"
  expect_contains "$out" "user:otto@example.com is placed as operator@project and holds roles/owner on fugaro-gcp" "the finding names the principal and the role"
}

test_a_viewer_with_editor_fails() {
  case_dir viewer_editor
  write_project_iam '{"role":"roles/editor","members":["user:vic@example.com"]}'
  run892
  expect_status "$status" 1 "a viewer with editor"
  expect_row "$out" admin-in-name FAIL "a viewer with editor"
  expect_contains "$out" "user:vic@example.com is placed as viewer@project and holds roles/editor" "the finding names the viewer"
}

test_a_repo_scoped_principal_with_iam_admin_fails() {
  case_dir repo_scoped
  write_grants '{"grants":[{"user":"user:ada@example.com","role":"admin","scope":"project"},{"user":"user:rex@example.com","role":"admin","scope":"github/acme/app"}]}'
  write_project_iam '{"role":"roles/resourcemanager.projectIamAdmin","members":["user:rex@example.com"]}'
  run892
  expect_status "$status" 1 "a repository-scoped admin with projectIamAdmin"
  expect_contains "$out" "user:rex@example.com is placed as admin@github/acme/app and holds roles/resourcemanager.projectIamAdmin" "a repository scope is not a project admin"
}

test_an_unplaced_owner_is_reported_as_implicit_admin() {
  case_dir implicit
  run892 olga@example.com
  expect_status "$status" 0 "an unplaced owner"
  expect_row "$out" implicit-admin INFO "an unplaced owner"
  expect_contains "$out" "user:olga@example.com holds roles/owner on fugaro-gcp and the node does not place it: an implicit admin (D1592)" "the implicit admin is named"
  expect_no_row_state "$out" admin-in-name FAIL "an unplaced owner is never failed"
}

test_a_budget_admin_is_not_flagged() {
  case_dir budget_admin
  run892 bea@example.com
  expect_status "$status" 0 "a budget admin"
  expect_row "$out" admin-in-name PASS "a budget admin"
  expect_contains "$out" "user:bea@example.com: TF-admin=no DB-admin=yes operator=yes" "a budget admin is a DB-admin"
  expect_contains "$out" "local=budget_admins" "the matrix places the budget admin from the local list"
}

test_a_stale_local_admin_list_is_reported() {
  case_dir stale_local
  write_config user:ada@example.com user:gone@example.com
  run892
  expect_status "$status" 0 "a stale local admin list"
  expect_row "$out" local-admins DIFFERS "a stale local admin list"
  expect_contains "$out" "terraform.admins names user:gone@example.com, which the node does not hold as admin@project" "the stale member is named"
}

test_an_unknown_role_in_the_node_fails() {
  case_dir unknown_role
  write_grants '{"grants":[{"user":"user:ada@example.com","role":"admin","scope":"project"},{"user":"user:otto@example.com","role":"superuser","scope":"project"}]}'
  run892
  expect_status "$status" 1 "an unknown role"
  expect_row "$out" grant-roles FAIL "an unknown role"
}

test_a_grant_whose_user_is_not_a_user_member_fails() {
  case_dir bare_user
  write_grants '{"grants":[{"user":"user:ada@example.com","role":"admin","scope":"project"},{"user":"otto@example.com","role":"operator","scope":"project"}]}'
  write_project_iam '{"role":"roles/owner","members":["user:otto@example.com"]}'
  run892
  expect_status "$status" 1 "a grant with a bare address"
  expect_row "$out" grant-roles FAIL "a grant with a bare address"
  expect_contains "$out" "otto@example.com is not a user: member" "the bare address is named"
}

test_a_malformed_node_prints_only_the_fail() {
  case_dir malformed_node
  write_grants '{"grants":"nobody"}'
  run892
  expect_status "$status" 1 "a malformed node"
  expect_row "$out" grants FAIL "a malformed node"
  expect_no_row_state "$out" grants PASS "a malformed node"
}

test_an_unplaced_editor_is_reported_as_implicit_admin() {
  case_dir implicit_editor
  write_project_iam '{"role":"roles/editor","members":["user:ed@example.com"]}'
  run892
  expect_status "$status" 0 "an unplaced editor"
  expect_contains "$out" "user:ed@example.com holds roles/editor on fugaro-gcp and the node does not place it: an implicit admin (D1592)" "the implicit editor is named"
  expect_no_row_state "$out" admin-in-name FAIL "an unplaced editor is never failed"
}

test_an_absent_node_is_unverified() {
  case_dir absent_node
  write_grants 'null'
  run892
  expect_status "$status" 3 "an absent node"
  expect_contains "$out" "CHECK 892 RESULT: INCOMPLETE (exit 3)" "an absent node"
  expect_row "$out" grants UNVERIFIED "an absent node"
  expect_row "$out" implicit-admin INFO "an absent node leaves every owner an implicit admin"
}

test_an_all_repos_admin_with_owner_fails() {
  case_dir all_repos
  write_grants '{"grants":[{"user":"user:ada@example.com","role":"admin","scope":"project"},{"user":"user:ari@example.com","role":"admin","scope":"all-repos"}]}'
  write_project_iam '{"role":"roles/owner","members":["user:ari@example.com"]}'
  run892
  expect_status "$status" 1 "an all-repos admin with owner"
  expect_contains "$out" "user:ari@example.com is placed as admin@all-repos and holds roles/owner" "an all-repos scope is not a project admin"
}

test_an_unreadable_node_is_incomplete() {
  case_dir unreadable_node
  printf 403 >"$answers/grants-status"
  run892
  expect_status "$status" 2 "an unreadable node"
  expect_contains "$out" "CHECK 892 RESULT: INCOMPLETE (exit 2)" "an unreadable node"
}

test_only_reads_are_called() {
  case_dir reads
  run892 ada@example.com otto@example.com
  expect_status "$status" 0 "the call set"
  [ -s "$CALLS" ] || fail "the call set: nothing was called"
  local line bad=""
  while IFS= read -r line; do
    case "$line" in
    "gcloud auth print-access-token --quiet "*) ;;
    "gcloud projects get-iam-policy "*" --format=json --quiet "*) ;;
    "gcloud iam service-accounts list --project fugaro-gcp --format=json --quiet "*) ;;
    "gcloud iam service-accounts get-iam-policy "*" --project fugaro-gcp --format=json --quiet "*) ;;
    "curl -q -sS --max-time 30 --config "*" -o "*" -w %{http_code} https://fugaro-fb-default-rtdb.firebaseio.com/config/admin/access.json "*) ;;
    "curl-config-mode 600") ;;
    *) bad="${bad}${line}"$'\n' ;;
    esac
  done <"$CALLS"
  [ -z "$bad" ] || fail "the call set: calls outside the read set: $bad"
  expect_contains "$(cat "$CALLS")" "gcloud projects get-iam-policy fugaro-gcp " "the GCP project's policy is read"
  expect_contains "$(cat "$CALLS")" "gcloud projects get-iam-policy fugaro-fb " "the Firebase project's policy is read"
  expect_contains "$(cat "$CALLS")" "get-iam-policy fugaro-b-acme-app-0123abcd@" "the build account's policy is read"
  expect_contains "$(cat "$CALLS")" "get-iam-policy fugaro-acme-app-default-4567@" "the job account's policy is read"
  expect_not_contains "$(cat "$CALLS")" "fugaro-deployer@fugaro-gcp.iam.gserviceaccount.com --project" "an account that is not a repository's is not read"
  ! grep -q '^fugaro ' "$CALLS" || fail "the call set: a fugaro command was called: $(grep '^fugaro ' "$CALLS")"
}

test_the_token_is_never_on_argv() {
  case_dir token
  run892 ada@example.com
  expect_status "$status" 0 "the token"
  expect_not_contains "$(cat "$CALLS")" "$TOKEN_MARKER" "the token reached an argv"
  expect_not_contains "$out" "$TOKEN_MARKER" "the token was printed on stdout"
  expect_not_contains "$err" "$TOKEN_MARKER" "the token was printed on stderr"
  expect_not_contains "$out$err" "$CONFIG_MARKER" "a config value the check does not need was printed"
  expect_contains "$(cat "$CALLS")" "curl-config-mode 600" "the curl config file is 0600"
  expect_row "$out" restore PASS "the scratch directory with the token file is removed"
}

test_stdin_is_closed() {
  case_dir stdin
  # An open pipe nobody writes to: a call that inherited it would read it as open.
  run892_raw fugaro ada@example.com < <(sleep 20)
  expect_status "$status" 0 "stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=open)" "a call inherited the check's stdin"
  expect_not_contains "$(cat "$CALLS")" "(stdin=line)" "a call read a line of the check's stdin"
  expect_contains "$(cat "$CALLS")" "(stdin=closed)" "the calls ran with stdin closed"
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE892_LOG="$CALLS" FAKE892_DIR="$answers" \
    FAKE892_TOKEN="$TOKEN_MARKER" XDG_CONFIG_HOME="$confighome" \
    perl -e 'alarm shift; exec @ARGV or die' 60 bash "$script" fugaro </dev/null 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_help() {
  CALLS=
  run892_raw --help </dev/null
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK892_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK892_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_an_admin_maps_to_tf_admin
t test_an_operator_with_many_grants_passes
t test_an_operator_with_owner_fails
t test_a_viewer_with_editor_fails
t test_a_repo_scoped_principal_with_iam_admin_fails
t test_an_unplaced_owner_is_reported_as_implicit_admin
t test_a_budget_admin_is_not_flagged
t test_a_stale_local_admin_list_is_reported
t test_an_unknown_role_in_the_node_fails
t test_a_grant_whose_user_is_not_a_user_member_fails
t test_a_malformed_node_prints_only_the_fail
t test_an_unplaced_editor_is_reported_as_implicit_admin
t test_an_absent_node_is_unverified
t test_an_all_repos_admin_with_owner_fails
t test_an_unreadable_node_is_incomplete
t test_only_reads_are_called
t test_the_token_is_never_on_argv
t test_stdin_is_closed
t test_an_agent_session_is_refused
t test_help

if [ "$failures" -ne 0 ]; then
  echo "check-892-iam_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-892-iam_test.sh: ok"
