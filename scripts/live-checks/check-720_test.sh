#!/usr/bin/env bash
# Tests for scripts/live-checks/check-720.sh. NOTHING here is a cloud, a project,
# a folder or an installation: `fugaro` and `gcloud` are scripts in $work whose
# answers each case owns, so no case can spend anything or reach anything. What
# the fakes model is the two facts the check reads and cannot fake for itself:
#
#   * a project's PARENT, per project and per moment. The fake gcloud answers it
#     from the marker the fake fugaro leaves: the workspace's own project hangs
#     under the organization until `workspace init` has run, then under the
#     workspace's folder; the member's hangs under the test folder until
#     `workspace project link` has run, then under the workspace's folder, and
#     back under the test folder once `workspace project unlink` has run. Those
#     two moves are the whole of U1, and of the return U6's row reads; U6's own
#     case (a caller with the folder's rights and none on the member) is a skip
#     the `u6` INFO row records, so no case here bends one, because no call this
#     check can make is that caller.
#   * a REAL TERMINAL. `fugaro setup --create-project` and `--link-billing` are
#     confirmed by being at one, so the check gives each setup a pty
#     (run_at_a_terminal) and the fake logs whether it had one.
#
# The readings are the point, so the cases that matter are the ones where one is
# bent: an organization this credential cannot see, an id that already exists, a
# billing account that cannot be read, a folder that lands outside the
# organization, a folder whose parent was not read, a setup that does not come
# up or writes no config, an init that takes the SA mechanism, a link that fails
# or that moves nothing, a move back that does not happen, a pin that is not
# written, an `rm` that prints no R3 page or another page, the three answers
# Google can give an owner grant by API, the three a `projects move` of a
# parentless project can give, a teardown that fails, and --keep.
#
# Usage: bash scripts/live-checks/check-720_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-720.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK720_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK720_TEST_HEAD:-2000}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK720_TEST_HEAD:-2000}")"
}
expect_count() { # file needle want what
  local got
  got=$(grep -c -- "$2" "$1" 2>/dev/null || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 line(s) matching '$2' in $(basename "$1"), got $got"
}
expect_match() { # haystack regex what: the haystack is a log, and the ids in it carry the second the run started
  if printf '%s\n' "$1" | grep -Eq -- "$2"; then
    return 0
  fi
  fail "$3: expected a line matching '$2'; got: $(printf '%s' "$1" | head -c "${CHECK720_TEST_HEAD:-2000}")"
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'GCLOUD'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE720_DIR:?}
printf 'gcloud %s\n' "$*" >>"${FAKE720_GLOG:?}"

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
org=$(cat "$dir/org" 2>/dev/null || printf '000000000001')
testfolder=$(cat "$dir/testfolder" 2>/dev/null || printf '424242')
wsfolder=$(cat "$dir/wsfolder" 2>/dev/null || printf '777001')
status_of() { cat "$dir/$1" 2>/dev/null || printf 0; }
must() { # must <case-file> <what>
  local st
  st=$(status_of "$1")
  if [ "$st" != 0 ]; then
    if [ -f "$dir/$2.err" ]; then cat "$dir/$2.err" >&2; else printf 'fake gcloud: %s refused\n' "$2" >&2; fi
    exit "$st"
  fi
}
exists() { # exists <project>
  grep -qx -- "$1" "$dir/state/existing" 2>/dev/null && return 0
  grep -qx -- "$1" "$dir/state/created" 2>/dev/null && return 0
  case "$1" in
  chk720*) [ -f "$dir/all-projects-exist" ] && return 0 ;;
  esac
  return 1
}
parent_of() { # parent_of <project>
  case "$1" in
  chk720ws*)
    if [ -f "$dir/state/init-done" ]; then printf 'folders/%s\n' "$wsfolder"; else printf 'organizations/%s\n' "$org"; fi
    ;;
  chk720mem*)
    if [ -f "$dir/state/unlink-done" ]; then
      printf 'folders/%s\n' "$testfolder"
    elif [ -f "$dir/state/link-done" ]; then
      printf 'folders/%s\n' "$wsfolder"
    else
      printf 'folders/%s\n' "$testfolder"
    fi
    ;;
  chk720no*)
    if [ -f "$dir/state/moved" ]; then printf 'folders/%s\n' "$testfolder"; fi
    ;;
  *)
    cat "$dir/parents/$1" 2>/dev/null || true
    ;;
  esac
}

verb=${pos[0]:-}
sub=${pos[1]:-}
arg=${pos[2]:-}
case "$verb $sub" in
"config get-value")
  must account-status account
  cat "$dir/account" 2>/dev/null || printf 'owner@example.invalid'
  ;;
"organizations list")
  must orgs-status orgs
  if [ -f "$dir/orgs" ]; then cat "$dir/orgs"; else printf '%s\n' "$org"; fi
  ;;
"billing accounts")
  must billing-status billing
  ;;
"projects describe")
  must describe-status describe
  if exists "$arg"; then
    parent_of "$arg"
  else
    printf 'ERROR: (gcloud.projects.describe) Project [%s] not found\n' "$arg" >&2
    exit 1
  fi
  ;;
"projects create")
  must create-status create
  mkdir -p "$dir/state"
  printf '%s\n' "$arg" >>"$dir/state/created"
  printf '%s\n' "$arg"
  ;;
"projects move" | "beta projects")
  # `gcloud beta projects move` is the second spelling the check tries when the
  # first answers that this gcloud has no such command: the fake answers both
  # from the same case file, so a case that says "no projects move" says it twice.
  must move-status move
  mkdir -p "$dir/state"
  : >"$dir/state/moved"
  printf '%s\n' "$arg"
  ;;
"projects add-iam-policy-binding")
  must grant-status grant
  printf 'Updated IAM policy for project [%s].\n' "$arg"
  ;;
"projects delete")
  must delete-status delete
  printf 'Deleted [%s].\n' "$arg"
  ;;
"resource-manager folders")
  case "$arg" in
  create)
    must folder-create-status foldercreate
    printf 'folders/%s\n' "$testfolder"
    ;;
  describe)
    desc=${pos[3]:-}
    # A case can refuse the describe of ONE folder: `workspace init`'s own
    # folder is read for its parent on its own, and a test folder read that
    # refused would stop the check before init ever ran.
    if [ -f "$dir/folder-describe-status-$desc" ]; then
      st=$(status_of "folder-describe-status-$desc")
      if [ "$st" != 0 ]; then
        if [ -f "$dir/folderdescribe.err" ]; then cat "$dir/folderdescribe.err" >&2; else printf 'fake gcloud: folderdescribe refused\n' >&2; fi
        exit "$st"
      fi
    fi
    if [ -f "$dir/folder-parent-$desc" ]; then
      cat "$dir/folder-parent-$desc"
    else
      must folder-describe-status folderdescribe
      if [ -f "$dir/folder-parent" ]; then cat "$dir/folder-parent"; else printf 'organizations/%s\n' "$org"; fi
    fi
    ;;
  delete)
    must folder-delete-status folderdelete
    # Google's own answer while a project under the folder is still pending
    # deletion. The fake used to print `Deleted [...]`, which hid a teardown whose
    # folder deletes could never succeed on a real run.
    printf 'ERROR: (gcloud.resource-manager.folders.delete) FAILED_PRECONDITION: the folder %s cannot be deleted while it holds a project pending deletion\n' "${pos[3]:-}" >&2
    exit 1
    ;;
  *)
    printf 'fake gcloud: unexpected folders verb %s\n' "$arg" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected verb %s %s\n' "$verb" "$sub" >&2
  exit 64
  ;;
esac
GCLOUD
chmod +x "$fakebin/gcloud"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE720_DIR:?}
tty=no
[ -t 0 ] && tty=yes
[ -t 1 ] && tty=yes
printf 'fugaro %s | config=%s tty=%s\n' "$*" "${FUGARO_PROJECT:-none}" "$tty" >>"${FAKE720_LOG:?}"
mkdir -p "$dir/state"
cfg() { printf '%s/fugaro/projects/%s.yaml' "${XDG_CONFIG_HOME:?}" "$1"; }
status_of() { cat "$dir/$1" 2>/dev/null || printf 0; }
must() { # must <case-file> <what>
  local st
  st=$(status_of "$1")
  if [ "$st" != 0 ]; then
    if [ -f "$dir/$2.err" ]; then cat "$dir/$2.err" >&2; else printf 'fake fugaro: %s refused\n' "$2" >&2; fi
    exit "$st"
  fi
}
ws_of() { # ws_of <project>: the membership its own config states
  awk '/^workspace:/{print $2; exit}' "$(cfg "$1")" 2>/dev/null || true
}
flag_value() { # flag_value <flag> <args...>
  local want=$1 prev="" a
  shift
  for a in "$@"; do
    [ "$prev" = "$want" ] && {
      printf '%s' "$a"
      return 0
    }
    prev=$a
  done
}

verb=${1:-}
shift || true
case "$verb" in
setup)
  name=$(flag_value --gcp-project "$@")
  # What the run said comes before what it answered, the way a real command
  # prints the steps it left and then exits 1 over them.
  if [ -f "$dir/setup.out" ]; then cat "$dir/setup.out"; else printf 'fugaro setup: %s is ready\n' "$name"; fi
  must setup-status setup
  if [ -f "$dir/setup-writes-nothing" ]; then exit 0; fi
  mkdir -p "$(dirname "$(cfg "$name")")"
  cat >"$(cfg "$name")" <<YAML
version: 1
name: $name
gcp_project: $name
region: us-east5
runs_bucket: $name-runs
bucket_url: gs://$name-runs
budget:
  rtdb_url: https://$name-default-rtdb.firebaseio.com
credentials_service:
  url: https://credsvc-$name.invalid
YAML
  printf '%s\n' "$name" >>"$dir/state/created"
  ;;
workspace)
  sub=${1:-}
  shift || true
  case "$sub" in
  init)
    name=${1:-}
    must init-status init
    # `workspace init` picks its own organization: the first the search answers
    # (workspace_init.go, D2803). A case that writes several into `$dir/orgs`
    # gets the first one named here, as the real command names it.
    chosen=$(cat "$dir/org" 2>/dev/null || printf '000000000001')
    if [ -f "$dir/orgs" ]; then chosen=$(head -n1 "$dir/orgs"); fi
    if [ -f "$dir/init-sa" ]; then
      printf "initialized workspace %s: the SA mechanism — this credential's organization search answered no organization to create a folder in, so %s holds the workspace IAM account's token creator and roles/owner on the workspace's own project (D2803, R8)\n" "$name" "${FUGARO_PROJECT:-}"
    else
      printf "initialized workspace %s: the folder mechanism — the folder folders/777001 created under organizations/%s with %s as its owner, and the workspace's own project placed in it (R8)\n" "$name" "$chosen" "${FUGARO_PROJECT:-}"
      printf "the roster is at workspaceRosters/%s, with %s as the workspace's first admin: what it records is written once and never changed (D2803, R6)\n" "$name" "${FUGARO_PROJECT:-}"
      [ -f "$dir/init-creates-no-marker" ] || : >"$dir/state/init-done"
    fi
    ;;
  add)
    name=${1:-}
    must pin-status pin
    sa=$(flag_value --service-account "$@")
    printf "join project %s to workspace %s: pin its Firebase project %s, its issuer https://securetoken.google.com/%s, an assertion key, and the workspace service account %s\n" \
      "${FUGARO_PROJECT:-}" "$name" "$name" "$name" "$sa"
    if [ ! -f "$dir/pin-writes-nothing" ]; then
      printf "workspace: %s\nworkspace_pin:\n  firebase_project: %s\n  issuer: https://securetoken.google.com/%s\n  assertion_key: chk720-not-a-key\n  service_account: %s\n" \
        "$name" "$name" "$name" "$sa" >>"$(cfg "${FUGARO_PROJECT:-}")"
    fi
    printf "next: run fugaro update to publish this project's own record\n"
    ;;
  project)
    sub2=${1:-}
    shift || true
    case "$sub2" in
    add)
      member=${1:-}
      must add-status add
      n=$(cat "$dir/state/add-count" 2>/dev/null || printf 0)
      n=$((n + 1))
      printf '%s' "$n" >"$dir/state/add-count"
      if [ "$n" = 1 ]; then
        printf "the member's own owner runs fugaro workspace project link on the member's machine: until they do, this project is in workspace %s's table and nothing of the workspace's is written into it (D2819, R11)\n" "${FUGARO_PROJECT:-}"
      else
        # The sentence the real command prints (`adopted <N person(people)> into
        # workspace <ws>'s roster: <addresses>`, workspace_project.go). The fake
        # used to print a form the CLI cannot print, so the check's first pattern
        # was only ever matched here.
        if [ -f "$dir/adopt-plural" ]; then
          printf "adopted 3 people into workspace %s's roster: user:%s, user:b@example.com, user:c@example.com\n" "${FUGARO_PROJECT:-}" "chk720-member@example.com"
        else
          printf "adopted 1 person into workspace %s's roster: user:%s\n" "${FUGARO_PROJECT:-}" "chk720-member@example.com"
        fi
      fi
      ;;
    link)
      member=${FUGARO_PROJECT:-}
      must link-status link
      ws=$(ws_of "$member")
      printf "moving %s into the workspace %s's folder changes the policies they inherit:\n" "$member" "$ws"
      printf "%s now holds %s: the project sits in the workspace's folder, and the member owner's own audit entry is filed.\n" "$ws" "$member"
      printf "the workspace's owner runs \`fugaro workspace project add %s\` on the machine holding the workspace's own config\n" "$member"
      [ -f "$dir/link-moves-nothing" ] || : >"$dir/state/link-done"
      ;;
    unlink)
      member=${FUGARO_PROJECT:-}
      must unlink-status unlink
      ws=$(ws_of "$member")
      printf "%s no longer holds %s: the authority is gone, and only the authority (D2822).\n" "$ws" "$member"
      [ -f "$dir/unlink-moves-nothing" ] || : >"$dir/state/unlink-done"
      ;;
    rm)
      member=${1:-}
      must rm-status rm
      if [ -f "$dir/rm-prints-nothing" ]; then exit 0; fi
      if [ -f "$dir/rm-prints-the-url-alone" ]; then
        printf 'https://console.cloud.google.com/iam-admin/settings?project=%s\n' "$member"
        exit 0
      fi
      printf 'nothing was deleted: %s is still a Google Cloud project with everything deployed in it, and no Firebase resource was touched\n' "$member"
      printf 'what is still deployed there keeps costing money until the project is shut down: the Cloud Scheduler jobs that rebuild its images, and the credentials service\n'
      printf 'shut %s down yourself at https://console.cloud.google.com/iam-admin/settings?project=%s\n' "$member" "$member"
      ;;
    *)
      printf 'fake fugaro: unexpected project verb %s\n' "$sub2" >&2
      exit 64
      ;;
    esac
    ;;
  *)
    printf 'fake fugaro: unexpected workspace verb %s\n' "$sub" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "$verb" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

case_dir() { # case_dir <name>
  dir=$work/$1
  mkdir -p "$dir/state"
  : >"$dir/state/existing"
  : >"$dir/CALLS"
  : >"$dir/GCALLS"
  printf '000000000001' >"$dir/org"
  printf 'owner@example.invalid' >"$dir/account"
}

run720_raw() { # run720_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE720_DIR="$dir" \
    FAKE720_LOG="$dir/CALLS" FAKE720_GLOG="$dir/GCALLS" \
    bash "$script" "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
}

run720() { # run720 [extra args...]
  run720_raw --org 000000000001 --billing 01AB23-CD45 "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run720
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 720 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" folder PASS "the happy path"
  expect_row "$out" installations PASS "the happy path"
  expect_row "$out" init PASS "the happy path"
  expect_row "$out" add PASS "the happy path"
  expect_row "$out" pin PASS "the happy path"
  expect_row "$out" move PASS "the happy path"
  expect_row "$out" adopt PASS "the happy path"
  # The adoption sentence the row passes on is the one the real command prints
  # (`adopted <N people> into workspace <ws>'s roster: <addresses>`), and the fake
  # no longer prints a form the CLI cannot print.
  expect_contains "$out" "adopted 1 person into workspace" "the happy path: the adoption sentence is the real one"
  # U6's own case is a skip this check records rather than claims: one gcloud
  # account holds the member project's owner rights and the folder's owner
  # binding at once, so no call it can make is U6's caller.
  expect_row "$out" u6 INFO "the happy path: U6's own case is recorded as a skip"
  expect_contains "$out" "U6's OWN case is NOT measured here" "the happy path: the u6 row says what it did not measure"
  expect_row "$out" detach PASS "the happy path"
  expect_row "$out" return PASS "the happy path"
  expect_row "$out" noorg PASS "the happy path"
  expect_row "$out" u8 PASS "the happy path"
  expect_row "$out" teardown PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  # U3: R3's page is printed in the block for the owner to open, and it is the
  # page of the member this check made.
  expect_contains "$out" "https://console.cloud.google.com/iam-admin/settings?project=chk720mem" "the happy path"
  expect_contains "$out" "WHAT TO OPEN, BY YOU (R3, U3)" "the happy path"
  # U8's row names Google's own answer.
  expect_contains "$out" "Google ACCEPTED an owner grant made by API" "the happy path"
  # Both installations were created AT A TERMINAL: `setup --create-project` and
  # --link-billing are confirmed by being at one and nothing is typed there.
  expect_count "$dir/CALLS" "fugaro setup .*tty=yes" 2 "the happy path"
  # Each command ran on the machine whose config it needs: the workspace's own
  # project runs init and rm, the member's own runs the link and the move back.
  local calls
  calls=$(cat "$dir/CALLS")
  expect_match "$calls" 'fugaro workspace init chk720ws[0-9]+ \| config=chk720ws[0-9]+' "the workspace's own machine ran init"
  expect_match "$calls" 'fugaro workspace project link \| config=chk720mem[0-9]+' "the member's machine ran the link"
  expect_match "$calls" 'fugaro workspace project unlink \| config=chk720mem[0-9]+' "the member's machine ran the move back"
  expect_match "$calls" 'fugaro workspace project rm chk720mem[0-9]+ --no-save-state \| config=chk720ws[0-9]+' "the workspace's machine ran the rm"
  expect_match "$calls" 'fugaro workspace project add chk720mem[0-9]+ --workspace chk720ws[0-9]+' "the add named the member and the workspace"
  expect_match "$calls" 'fugaro workspace add chk720ws[0-9]+ --firebase-project chk720ws[0-9]+ --issuer https://securetoken.google.com/chk720ws[0-9]+' "the pin named the workspace"
  expect_contains "$calls" "--no-apply" "the add's own follow-up steps were left for the owner"
  # The pin named the workspace's own service account, and the assertion key was
  # the check's own placeholder.
  expect_match "$calls" '--service-account fugaro-workspace@chk720ws[0-9]+' "the pin named the workspace's service account"
  expect_contains "$calls" "--assertion-key chk720-not-a-key" "the pin's key is the check's own placeholder"
  # The add's second run is the one the link hands back.
  expect_count "$dir/CALLS" "workspace project add" 2 "the happy path"
  # The teardown deleted the three projects and issued NO folder delete: a folder
  # delete while the projects under it are still in DELETE_REQUESTED cannot
  # succeed, so the row leaves both folders, says why, and names the commands.
  expect_count "$dir/GCALLS" "gcloud --quiet projects delete" 3 "the happy path"
  expect_count "$dir/GCALLS" "resource-manager folders delete" 0 "the happy path"
  expect_contains "$out" "are left behind, and that is on purpose" "the happy path: the folders are left on purpose"
  expect_contains "$out" "gcloud resource-manager folders delete" "the happy path: the row names the folder commands"
  # The check's own working directory is gone and its config root was its own.
  expect_contains "$out" "its whole XDG_CONFIG_HOME" "the happy path"
}

test_help() {
  case_dir help
  run720_raw --help
  expect_status "$status" 0 "check-720 --help"
  expect_contains "$out" "Usage:" "check-720 --help"
  expect_contains "$out" "Run by YOU" "check-720 --help says who runs it"
  [ ! -s "$dir/CALLS" ] || fail "check-720 --help: it called fugaro: $(head -c 400 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "check-720 --help: it called gcloud: $(head -c 400 "$dir/GCALLS")"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run720_raw --dry-run --org 000000000001 --billing 01AB23-CD45
  expect_status "$status" 0 "a dry run"
  expect_contains "$err" "Check 720 plan" "a dry run"
  expect_contains "$err" "chk720ws" "a dry run names the projects it would make"
  [ ! -s "$dir/CALLS" ] || fail "a dry run: it called fugaro: $(head -c 400 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "a dry run: it called gcloud: $(head -c 400 "$dir/GCALLS")"
  expect_not_contains "$out" "CHECK 720 RESULT" "a dry run prints no block"
}

test_a_missing_org_is_refused() {
  case_dir missing_org
  run720_raw --billing 01AB23-CD45
  expect_status "$status" 2 "a missing --org"
  expect_contains "$err" "no --org was given" "a missing --org"
  [ ! -s "$dir/GCALLS" ] || fail "a missing --org: it called gcloud: $(head -c 400 "$dir/GCALLS")"
}

test_a_missing_billing_account_is_refused() {
  case_dir missing_billing
  run720_raw --org 000000000001
  expect_status "$status" 2 "a missing --billing"
  expect_contains "$err" "no --billing was given" "a missing --billing"
}

test_an_organization_that_is_not_an_id_is_refused() {
  case_dir org_not_an_id
  run720_raw --org my-organization --billing 01AB23-CD45
  expect_status "$status" 2 "an organization that is not an id"
  expect_contains "$err" "is not an organization id" "an organization that is not an id"
}

test_a_region_that_is_not_a_region_is_refused() {
  case_dir region_not_a_region
  run720_raw --org 000000000001 --billing 01AB23-CD45 --region nowhere
  expect_status "$status" 2 "a region that is not a region"
  expect_contains "$err" "is not a GCP region" "a region that is not a region"
}

test_a_rigor_that_is_not_a_mode_is_refused() {
  case_dir rigor_not_a_mode
  run720_raw --org 000000000001 --billing 01AB23-CD45 --rigor maximum
  expect_status "$status" 2 "a rigor that is not a mode"
  expect_contains "$err" "is not one of poc, startup, pro or critical" "a rigor that is not a mode"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE720_DIR="$dir" FAKE720_LOG="$dir/CALLS" \
    FAKE720_GLOG="$dir/GCALLS" bash "$script" --org 000000000001 --billing 01AB23-CD45 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  expect_contains "$out" "no agent runs Check 720" "an agent session"
  [ ! -s "$dir/CALLS" ] || fail "an agent session: it called fugaro: $(head -c 400 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "an agent session: it called gcloud: $(head -c 400 "$dir/GCALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE720_DIR="$dir" FAKE720_LOG="$dir/CALLS" \
    FAKE720_GLOG="$dir/GCALLS" bash -x "$script" --org 000000000001 --billing 01AB23-CD45 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_an_unauthenticated_gcloud_stops_the_check() {
  case_dir no_account
  printf '(unset)' >"$dir/account"
  run720
  expect_status "$status" 2 "an unauthenticated gcloud"
  expect_row "$out" premise FAIL "an unauthenticated gcloud"
  expect_contains "$out" "gcloud auth login" "an unauthenticated gcloud"
}

test_an_organization_this_credential_cannot_see_stops_the_check() {
  case_dir org_not_visible
  printf '000000000009\n' >"$dir/orgs"
  run720
  expect_status "$status" 2 "an organization this credential cannot see"
  expect_row "$out" premise FAIL "an organization this credential cannot see"
  expect_contains "$out" "not one this credential can see" "an organization this credential cannot see"
  expect_contains "$out" "000000000009" "an organization this credential cannot see: it names what it does see"
  expect_count "$dir/GCALLS" "folders create" 0 "an organization this credential cannot see: no folder is created"
}

test_a_project_id_that_already_exists_stops_the_check() {
  case_dir project_exists
  : >"$dir/all-projects-exist"
  run720
  expect_status "$status" 2 "a project id that already exists"
  expect_row "$out" premise FAIL "a project id that already exists"
  expect_contains "$out" "already exists" "a project id that already exists"
}

test_a_billing_account_that_is_not_readable_stops_the_check() {
  case_dir billing_unreadable
  printf 1 >"$dir/billing-status"
  printf 'fake gcloud: the billing account is not one of yours\n' >"$dir/billing.err"
  run720
  expect_status "$status" 2 "a billing account that is not readable"
  expect_row "$out" premise FAIL "a billing account that is not readable"
  expect_contains "$out" "not one of yours" "a billing account that is not readable: Google's own words are named"
}

test_a_folder_that_lands_outside_the_organization_stops_the_check() {
  case_dir folder_elsewhere
  printf 'organizations/000000000009' >"$dir/folder-parent"
  run720
  expect_status "$status" 2 "a folder that lands outside the organization"
  expect_row "$out" folder FAIL "a folder that lands outside the organization"
  expect_contains "$out" "not organizations/000000000001" "a folder that lands outside the organization"
  expect_count "$dir/CALLS" "fugaro setup" 0 "a folder that lands outside the organization: no installation is created"
}

test_a_folder_whose_parent_was_not_read_leaves_the_row_unverified() {
  case_dir folder_parent_unreadable
  printf 1 >"$dir/folder-describe-status"
  printf 'fake gcloud: the folder was not described\n' >"$dir/folderdescribe.err"
  run720
  expect_status "$status" 2 "a folder whose parent was not read"
  expect_row "$out" folder UNVERIFIED "a folder whose parent was not read"
  expect_contains "$out" "its parent was not read" "a folder whose parent was not read"
}

test_an_installation_that_does_not_come_up_stops_the_check() {
  case_dir setup_fails
  printf 1 >"$dir/setup-status"
  printf 'fugaro setup: 3 step(s) left for you, listed above\n' >"$dir/setup.out"
  printf 'fugaro setup: is not finished\n' >"$dir/setup.err"
  run720
  expect_status "$status" 2 "an installation that does not come up"
  expect_row "$out" installations UNVERIFIED "an installation that does not come up"
  expect_contains "$out" "3 step(s) left for you" "an installation that does not come up: the step is named"
  expect_count "$dir/CALLS" "fugaro workspace init" 0 "an installation that does not come up: nothing is initialized"
}

test_a_setup_that_writes_no_config_stops_the_check() {
  case_dir setup_writes_nothing
  : >"$dir/setup-writes-nothing"
  run720
  expect_status "$status" 2 "a setup that writes no config"
  expect_row "$out" installations UNVERIFIED "a setup that writes no config"
  expect_contains "$out" "setup wrote no config" "a setup that writes no config"
}

test_a_setup_that_was_killed_at_its_deadline_names_its_transcript() {
  case_dir setup_slow
  printf 124 >"$dir/setup-status"
  run720
  expect_status "$status" 2 "a setup killed at its deadline"
  expect_row "$out" installations UNVERIFIED "a setup killed at its deadline"
  expect_contains "$out" "did not finish inside an hour" "a setup killed at its deadline"
  expect_contains "$out" "setup-workspace.log" "a setup killed at its deadline: the transcript is named"
}

test_an_init_that_takes_the_sa_mechanism_is_a_finding() {
  case_dir init_sa_mechanism
  : >"$dir/init-sa"
  run720
  expect_status "$status" 2 "an init that takes the SA mechanism"
  expect_row "$out" init FAIL "an init that takes the SA mechanism"
  expect_contains "$out" "did not report the folder mechanism" "an init that takes the SA mechanism"
  expect_contains "$out" "whole subject is the folder" "an init that takes the SA mechanism"
  expect_count "$dir/CALLS" "fugaro workspace project add" 0 "an init that takes the SA mechanism: nothing is added to it"
}

test_an_init_that_does_not_move_the_workspace_project_is_a_finding() {
  case_dir init_without_a_move
  : >"$dir/init-creates-no-marker"
  run720
  expect_status "$status" 2 "an init that does not move the workspace's project"
  expect_row "$out" init FAIL "an init that does not move the workspace's project"
  expect_contains "$out" "hangs under organizations/000000000001" "an init that does not move the workspace's project"
}

test_an_init_that_picks_its_own_organization_is_not_a_finding() {
  case_dir init_other_org
  # The credential sees two organizations, and `workspace init` creates the
  # workspace's folder under the first its own search answers (workspace_init.go,
  # D2803) — not this check's --org. That is not the move U1 states failing: U1's
  # move is the member's project out of the test folder and back into it, both in
  # --org, and Google refuses to move a project between organizations. This row
  # used to read FAIL and abort, which is the false finding the row no longer
  # makes.
  printf '000000000002\n000000000001\n' >"$dir/orgs"
  printf 'organizations/000000000002' >"$dir/folder-parent-777001"
  run720
  expect_status "$status" 2 "an init that picks its own organization"
  expect_row "$out" init UNVERIFIED "an init that picks its own organization"
  expect_contains "$out" "not this check's --org organizations/000000000001" "an init that picks its own organization"
  expect_contains "$out" "Run this check with a credential that sees only organizations/000000000001" "an init that picks its own organization: the fix is named"
  expect_not_contains "$out" "CHECK 720 RESULT: FAIL" "an init that picks its own organization is not a finding"
  expect_count "$dir/CALLS" "fugaro workspace project add" 0 "an init that picks its own organization: nothing later runs"
  expect_contains "$out" "sees more than one organization" "the premise row names the multi-organization credential"
  # The abort happens after both billed setups, so the row says what it left and
  # how to delete it.
  expect_contains "$out" "are still there and still cost money" "an init that picks its own organization: the projects it left are named"
}

test_an_init_whose_folder_parent_was_not_read_leaves_the_row_unverified() {
  case_dir init_parent_unreadable
  printf 1 >"$dir/folder-describe-status-777001"
  printf 'fake gcloud: the folder was not described\n' >"$dir/folderdescribe.err"
  run720
  expect_status "$status" 2 "an init whose folder parent was not read"
  expect_row "$out" init UNVERIFIED "an init whose folder parent was not read"
  expect_contains "$out" "read the folder's parent failed" "an init whose folder parent was not read"
  expect_contains "$out" "are still there and still cost money" "an init whose folder parent was not read: the projects it left are named"
  expect_count "$dir/CALLS" "fugaro workspace project add" 0 "an init whose folder parent was not read: nothing later runs"
}

test_a_workspace_folder_under_another_parent_is_a_finding() {
  case_dir init_folder_elsewhere
  # One organization is visible and the folder init created is not under it, so
  # the organization the row reads is the only one there is: this is a finding,
  # not the multi-organization skip above.
  printf 'folders/999888' >"$dir/folder-parent-777001"
  run720
  expect_status "$status" 2 "a workspace folder under another parent"
  expect_row "$out" init FAIL "a workspace folder under another parent"
  expect_contains "$out" "under folders/999888, and this credential sees only organizations/000000000001" "a workspace folder under another parent"
}

test_a_move_into_the_folder_that_does_not_happen_is_a_finding() {
  case_dir link_without_a_move
  : >"$dir/link-moves-nothing"
  run720
  expect_status "$status" 1 "a move into the folder that does not happen"
  expect_row "$out" move FAIL "a move into the folder that does not happen"
  expect_contains "$out" "hangs under folders/424242 after the link" "a move into the folder that does not happen"
  expect_row "$out" adopt PASS "a move into the folder that does not happen: the rest of the check still reads"
  expect_row "$out" detach PASS "a move into the folder that does not happen: the rest of the check still reads"
}

test_an_adoption_of_several_people_is_the_add_the_link_hands_back() {
  case_dir adopt_plural
  : >"$dir/adopt-plural"
  run720
  expect_status "$status" 0 "an add that adopted several people"
  expect_row "$out" adopt PASS "an add that adopted several people"
  expect_contains "$out" "adopted 3 people into workspace" "an add that adopted several people: the count is not part of the pattern"
}

test_a_link_that_fails_stops_the_check() {
  case_dir link_fails
  printf 1 >"$dir/link-status"
  printf 'fugaro: no project config on this machine pins a workspace\n' >"$dir/link.err"
  run720
  expect_status "$status" 2 "a link that fails"
  expect_row "$out" move FAIL "a link that fails"
  expect_contains "$out" "pins a workspace" "a link that fails: the refusal is named"
}

test_a_move_back_that_does_not_happen_is_a_finding() {
  case_dir unlink_without_a_move
  : >"$dir/unlink-moves-nothing"
  run720
  expect_status "$status" 1 "a move back that does not happen"
  expect_row "$out" return FAIL "a move back that does not happen"
  expect_contains "$out" "after the unlink" "a move back that does not happen"
  expect_row "$out" move PASS "a move back that does not happen: the move in still reads"
}

test_a_pin_that_is_not_written_stops_the_check() {
  case_dir pin_not_written
  : >"$dir/pin-writes-nothing"
  run720
  expect_status "$status" 2 "a pin that is not written"
  expect_row "$out" pin FAIL "a pin that is not written"
  expect_contains "$out" "states no membership and no pin" "a pin that is not written"
}

test_an_rm_that_prints_no_r3_page_is_a_finding() {
  case_dir rm_prints_nothing
  : >"$dir/rm-prints-nothing"
  run720
  expect_status "$status" 1 "an rm that prints no R3 page"
  expect_row "$out" detach FAIL "an rm that prints no R3 page"
  expect_contains "$out" "printed no R3 page" "an rm that prints no R3 page"
  expect_not_contains "$out" "WHAT TO OPEN, BY YOU" "an rm that prints no R3 page: no browser step is offered over it"
}

test_an_rm_that_prints_the_url_outside_the_sentence_leaves_the_row_unverified() {
  case_dir rm_prints_the_url_alone
  : >"$dir/rm-prints-the-url-alone"
  run720
  expect_status "$status" 3 "an rm that prints the URL outside its sentence"
  expect_row "$out" detach UNVERIFIED "an rm that prints the URL outside its sentence"
  expect_contains "$out" "not in the sentence this check reads" "an rm that prints the URL outside its sentence"
  expect_not_contains "$out" "WHAT TO OPEN, BY YOU" "an rm that prints the URL outside its sentence: no browser step is offered over it"
}

test_an_owner_grant_google_refuses_with_an_invitation_is_read_as_such() {
  case_dir grant_invitation
  printf 1 >"$dir/grant-status"
  printf 'ERROR: (gcloud.projects.add-iam-policy-binding) Google refuses an owner grant made by API outside an organization; the grant is an invitation to accept in the console\n' \
    >"$dir/grant.err"
  run720
  expect_status "$status" 0 "an owner grant refused with an invitation"
  expect_row "$out" u8 PASS "an owner grant refused with an invitation"
  expect_contains "$out" "Google REFUSED the owner grant made by API and answered with an invitation" "an owner grant refused with an invitation"
  expect_contains "$out" "invite owner@example.invalid as an owner in the Cloud Console" "an owner grant refused with an invitation: the CLI's own step is beside it"
  expect_contains "$out" "then run this again to confirm the binding" "an owner grant refused with an invitation: the step is the CLI's, verbatim"
}

test_an_owner_grant_that_answers_something_else_leaves_the_row_unverified() {
  case_dir grant_unreadable
  printf 7 >"$dir/grant-status"
  printf 'fake gcloud: the policy write was refused\n' >"$dir/grant.err"
  run720
  expect_status "$status" 3 "an owner grant that answers something else"
  expect_row "$out" u8 UNVERIFIED "an owner grant that answers something else"
  expect_contains "$out" "cannot read as either an acceptance or an invitation" "an owner grant that answers something else"
}

test_a_no_org_project_that_moves_is_read_as_such() {
  case_dir noorg_moves
  run720
  expect_status "$status" 0 "a no-org project that moves"
  expect_row "$out" noorg PASS "a no-org project that moves"
  expect_contains "$out" "WAS moved by an API call" "a no-org project that moves"
  expect_match "$(cat "$dir/GCALLS")" 'gcloud --quiet projects move chk720no[0-9]+ --folder=424242' "a no-org project that moves: the move was attempted"
}

test_a_no_org_project_google_refuses_to_move_is_read_as_such() {
  case_dir noorg_refused
  printf 1 >"$dir/move-status"
  printf 'ERROR: (gcloud.projects.move) A project with no organization cannot be moved by this command; use the migration flow\n' \
    >"$dir/move.err"
  run720
  expect_status "$status" 0 "a no-org project Google refuses to move"
  expect_row "$out" noorg PASS "a no-org project Google refuses to move"
  expect_contains "$out" "was NOT moved by an API call" "a no-org project Google refuses to move"
  expect_contains "$out" "migration flow" "a no-org project Google refuses to move: Google's own words are named"
}

test_a_gcloud_without_projects_move_leaves_the_row_unverified() {
  case_dir no_projects_move
  printf 2 >"$dir/move-status"
  printf 'ERROR: (gcloud.projects) Invalid choice: move\n' >"$dir/move.err"
  run720
  expect_status "$status" 3 "a gcloud without projects move"
  expect_row "$out" noorg UNVERIFIED "a gcloud without projects move"
  expect_contains "$out" "no projects move" "a gcloud without projects move"
}

test_a_teardown_that_fails_is_a_finding() {
  case_dir teardown_fails
  printf 1 >"$dir/delete-status"
  printf 'fake gcloud: the delete was refused\n' >"$dir/delete.err"
  run720
  expect_status "$status" 1 "a teardown that fails"
  expect_row "$out" teardown FAIL "a teardown that fails"
  expect_contains "$out" "still costing money" "a teardown that fails"
}

test_keep_leaves_the_projects_and_says_so() {
  case_dir keep
  run720 --keep
  expect_status "$status" 3 "--keep"
  expect_row "$out" teardown UNVERIFIED "--keep"
  expect_contains "$out" "STILL THERE" "--keep"
  expect_count "$dir/GCALLS" "gcloud projects delete" 0 "--keep: nothing is deleted"
  expect_count "$dir/GCALLS" "folders delete" 0 "--keep: no folder is deleted"
}

test_a_directory_you_named_is_left_as_it_is() {
  case_dir caller_dir
  mkdir -p "$work/caller_dir/kept"
  run720 --dir "$work/caller_dir/kept"
  expect_status "$status" 0 "a directory you named"
  expect_row "$out" restore PASS "a directory you named"
  expect_contains "$out" "the directory you named with --dir was left" "a directory you named"
  [ -d "$work/caller_dir/kept" ] || fail "a directory you named: it was removed"
}

test_a_directory_that_is_not_there_is_refused() {
  case_dir caller_dir_missing
  run720_raw --org 000000000001 --billing 01AB23-CD45 --dir "$work/no-such-dir"
  expect_status "$status" 2 "a directory that is not there"
  expect_contains "$err" "is not a directory" "a directory that is not there"
}

test_an_unknown_argument_is_refused() {
  case_dir unknown_argument
  run720_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK720_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK720_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help
t test_a_dry_run_calls_nothing
t test_a_missing_org_is_refused
t test_a_missing_billing_account_is_refused
t test_an_organization_that_is_not_an_id_is_refused
t test_a_region_that_is_not_a_region_is_refused
t test_a_rigor_that_is_not_a_mode_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_an_unauthenticated_gcloud_stops_the_check
t test_an_organization_this_credential_cannot_see_stops_the_check
t test_a_project_id_that_already_exists_stops_the_check
t test_a_billing_account_that_is_not_readable_stops_the_check
t test_a_folder_that_lands_outside_the_organization_stops_the_check
t test_a_folder_whose_parent_was_not_read_leaves_the_row_unverified
t test_an_installation_that_does_not_come_up_stops_the_check
t test_a_setup_that_writes_no_config_stops_the_check
t test_a_setup_that_was_killed_at_its_deadline_names_its_transcript
t test_an_init_that_takes_the_sa_mechanism_is_a_finding
t test_an_init_that_does_not_move_the_workspace_project_is_a_finding
t test_an_init_that_picks_its_own_organization_is_not_a_finding
t test_an_init_whose_folder_parent_was_not_read_leaves_the_row_unverified
t test_a_workspace_folder_under_another_parent_is_a_finding
t test_a_move_into_the_folder_that_does_not_happen_is_a_finding
t test_an_adoption_of_several_people_is_the_add_the_link_hands_back
t test_a_link_that_fails_stops_the_check
t test_a_move_back_that_does_not_happen_is_a_finding
t test_a_pin_that_is_not_written_stops_the_check
t test_an_rm_that_prints_no_r3_page_is_a_finding
t test_an_rm_that_prints_the_url_outside_the_sentence_leaves_the_row_unverified
t test_an_owner_grant_google_refuses_with_an_invitation_is_read_as_such
t test_an_owner_grant_that_answers_something_else_leaves_the_row_unverified
t test_a_no_org_project_that_moves_is_read_as_such
t test_a_no_org_project_google_refuses_to_move_is_read_as_such
t test_a_gcloud_without_projects_move_leaves_the_row_unverified
t test_a_teardown_that_fails_is_a_finding
t test_keep_leaves_the_projects_and_says_so
t test_a_directory_you_named_is_left_as_it_is
t test_a_directory_that_is_not_there_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-720_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-720_test.sh: ok"
