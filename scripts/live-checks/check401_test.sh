#!/usr/bin/env bash
# Tests for scripts/live-checks/check401.sh, against a fake gcloud, a fake curl,
# a fake fugaro and a real python3: nothing here reaches the network, there is
# no cloud project and no Realtime Database, and no step of the real check is
# run against anything.
#
# The fakes are the whole of the world the check sees. `fugaro doctor --json`
# answers from a file; `fugaro update` PRETENDS to be the migration — it writes
# the config and the node document a case wants the converge to have left
# behind, prints the line a converge prints, and is otherwise the CLI's own
# business, which no test can run; `fugaro update --plan-only` prints a case's
# plan; `gcloud auth print-access-token` prints a token; and `curl` serves the
# node document from the case's own directory. A case therefore says what the
# migration did by writing files, and the check is measured against that.
#
# Usage: bash scripts/live-checks/check401_test.sh
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check401.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK401_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | tr '\n' '|' | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 — stdout: $(printf '%s' "$out" | tr '\n' '|' | head -c 600)"
}

# The installation the whole suite runs against: a sandbox, never the owner's.
PROJECT=prj-sandbox-a
# The member addresses are sentinels: the block is pasted into a pull request,
# and no address of this installation's people may reach it, so the suite
# asserts that over every run — which is why they are spelled to be unmistakable.
ANA=user:SENTINEL401a@example.invalid
BO=user:SENTINEL401b@example.invalid
CY=user:SENTINEL401c@example.invalid
GROUP=group:SENTINEL401ops@example.invalid
SENTINEL=SENTINEL401

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(xtrace installation config node-before update node-after config-after legacy plan)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check401.sh's test: the access token the node read needs, and
# nothing else. Every call is recorded, so a test can tell a run that read the
# node from one that stopped before it did.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE401_DIR/calls"
case "${1:-}" in
auth)
  case "${2:-}" in
  print-access-token)
    if [ -f "$FAKE401_DIR/token.status" ]; then exit "$(cat "$FAKE401_DIR/token.status")"; fi
    printf 'fake-access-token\n'
    ;;
  *)
    printf 'fake gcloud: unknown auth verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check401.sh's test: it answers the one read the check makes —
# the grants node — from $FAKE401_DIR, and prints the status code it was asked to
# print. A case writes the document; FAKE401_HTTP forces the status.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE401_DIR/calls"
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
  -o)
    out=${2:-}
    shift 2
    ;;
  -w | -H) shift 2 ;;
  -*) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
case "$url" in
*/config/admin/access.json) ;;
*)
  printf 'fake curl: unexpected url %s\n' "$url" >&2
  exit 64
  ;;
esac
if [ -n "$out" ]; then
  if [ -f "$FAKE401_DIR/node.json" ]; then
    cat "$FAKE401_DIR/node.json" >"$out"
  else
    printf 'null\n' >"$out"
  fi
fi
printf '%s' "${FAKE401_HTTP:-200}"
CURL_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check401.sh's test. doctor's answer comes from a file. The
# converge PRETENDS to migrate: it prints the line a case wants printed and
# rewrites the config and the node document a case wants it to have left behind,
# which is what makes the check's comparisons testable without Terraform and
# without a database. Nothing here is the CLI.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE401_DIR/calls"
case "${1:-}" in
doctor)
  cat "$FAKE401_DIR/doctor.json"
  ;;
update)
  shift
  config=""
  plan=no
  while [ $# -gt 0 ]; do
    case "$1" in
    --config)
      config=${2:-}
      shift 2
      ;;
    --plan-only)
      plan=yes
      shift
      ;;
    *) shift ;;
    esac
  done
  if [ "$plan" = yes ]; then
    if [ -f "$FAKE401_DIR/plan.status" ]; then exit "$(cat "$FAKE401_DIR/plan.status")"; fi
    if [ -f "$FAKE401_DIR/plan-output" ]; then cat "$FAKE401_DIR/plan-output"; fi
    exit 0
  fi
  if [ "$(cat "$FAKE401_DIR/update.status" 2>/dev/null || echo 0)" != 0 ]; then
    printf 'fake fugaro: the converge failed\n' >&2
    exit "$(cat "$FAKE401_DIR/update.status")"
  fi
  if [ -f "$FAKE401_DIR/update-output" ]; then cat "$FAKE401_DIR/update-output"; fi
  if [ -n "$config" ] && [ -f "$FAKE401_DIR/after-config" ]; then
    cp "$FAKE401_DIR/after-config" "$config"
  fi
  if [ -f "$FAKE401_DIR/after-node-absent" ]; then
    rm -f "$FAKE401_DIR/node.json"
  elif [ -f "$FAKE401_DIR/after-node" ]; then
    cp "$FAKE401_DIR/after-node" "$FAKE401_DIR/node.json"
  fi
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/curl" "$1/bin/fugaro"
}

# ---------------------------------------------------------------- the fixtures
write_doctor() { # dir PROJECT [NAME] [REGION]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "%s", "region": "%s"}}\n' \
    "${3:-sandbox-a}" "$2" "${4:-us-east5}" >"$1/state/doctor.json"
}

write_config() { # FILE VERSION LAUNCHERS OPERATORS ADMINS PROJECT [no-rtdb]
  local file=$1 version=$2 launchers=$3 operators=$4 admins=$5 project=$6 nortdb=${7:-}
  local pair members
  {
    printf 'version: %s\n' "$version"
    printf 'name: %s\n' "$project"
    printf 'gcp_project: %s\n' "$project"
    printf 'region: us-east5\n'
    printf 'runs_bucket: fugaro-runs-%s\n' "$project"
    if [ -z "$nortdb" ]; then
      printf 'budget:\n  rtdb_url: https://%s-default-rtdb.firebaseio.com\n' "$project"
    fi
    printf 'terraform:\n'
    # A version-1 config spells the two lists launchers/operators; a version-2 one
    # spells them operators/admins. The reader tells the two apart by the version,
    # which is the point of writing both shapes here.
    if [ "$version" = 1 ]; then
      for pair in "launchers:$launchers" "operators:$operators"; do
        printf '  %s:\n' "${pair%%:*}"
        for members in ${pair#*:}; do
          printf '  - %s\n' "$members"
        done
      done
    else
      for pair in "operators:$operators" "admins:$admins"; do
        printf '  %s:\n' "${pair%%:*}"
        for members in ${pair#*:}; do
          printf '  - %s\n' "$members"
        done
      done
    fi
  } >"$file"
}

# write_node FILE OPERATOR_MEMBERS ADMIN_MEMBERS — the grants document the node
# holds, in the shape internal/access's Encode writes: one grant per member per
# role, at project scope.
write_node() {
  local file=$1 operators=$2 admins=$3
  FAKE401_OPS="$operators" FAKE401_ADMS="$admins" python3 - "$file" <<'PY'
import json, os, sys

grants = []
for role, members in (("operator", os.environ["FAKE401_OPS"]), ("admin", os.environ["FAKE401_ADMS"])):
    for member in members.split():
        grants.append({"user": member, "role": role, "scope": "project"})
with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump({"grants": grants}, f)
PY
}

# migration_line OPERATOR_MEMBERS ADMIN_MEMBERS — the line a converge prints when
# it writes the node, in the shape internal/cli's accessMigrated renders: the
# roster it wrote, then the sentence about the keys the file now derives from it.
migration_line() {
  local operators=$1 admins=$2 member line=""
  for member in $operators; do line="$line$member (operator), "; done
  for member in $admins; do line="$line$member (admin), "; done
  printf "fugaro update: migrated the installation's member lists into the grants node: %s; the local config's terraform.operators and terraform.admins are now derived from it\n" "${line%, }"
}

# the_migration DIR OPS ADMS — what the converge is to have done: the version-2
# config it writes, the node it leaves, and the line it prints. A case that wants
# the migration to have gone wrong writes those pieces itself instead.
the_migration() {
  local dir=$1 operators=$2 admins=$3
  write_config "$dir/state/after-config" 2 "" "$operators" "$admins" "$PROJECT"
  write_node "$dir/state/after-node" "$operators" "$admins"
  migration_line "$operators" "$admins" >"$dir/state/update-output"
}

# no_changes DIR — the plan of a converge with nothing to apply.
no_changes() {
  printf 'No changes: the installation matches the plan.\n' >"$1/state/plan-output"
}

# setup_case NAME — a fresh case: the fakes, a sandbox installation whose config
# is still the version-1 shape (the lists this check is about), no grants node
# yet, a converge that migrates the lists into it, and a plan with nothing in it.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  write_doctor "$dir" "$PROJECT"
  write_config "$dir/config.yaml" 1 "$ANA $BO" "$ANA" "" "$PROJECT"
  rm -f "$dir/state/node.json"
  the_migration "$dir" "$ANA $BO" "$ANA"
  no_changes "$dir"
}

# ------------------------------------------------------------- running the check
run401() { # [ARGS...] — sets $status, $out (stdout) and $errtext (stderr)
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE401_DIR="$dir/state" \
    PATH="$dir/bin:$PATH" \
    ${RUN401_ENV[@]+"${RUN401_ENV[@]}"} \
    bash "$script" ${RUN401_ARGS[@]+"${RUN401_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN401_ARGS=()
  RUN401_ENV=()
  errtext=$(cat "$dir/err")
  printf '%s\n' "$out" >"$dir/out"
}
RUN401_ARGS=()
RUN401_ENV=()

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 401 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

# assert_no_address DIR WHAT — nothing the run printed carries a member address.
# The two lists are this installation's people and the block is pasted into a
# pull request: this is the assertion that says the check's own words about them
# are counts and role words. stderr is read too, because the run's progress is
# what the owner sees on the terminal.
assert_no_address() {
  local dir=$1 what=$2
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$SENTINEL" \
    "$what: a member address in the run's own output"
}

expect_scratch_gone() { # dir what
  local scratch
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1)
  if [ -z "$scratch" ]; then
    fail "$2: the script did not say where its scratch directory is"
    return 0
  fi
  if [ -d "$scratch" ]; then fail "$2: the scratch directory $scratch survived the run"; fi
  if [ -n "$(find "$1/tmp" -mindepth 1 2>/dev/null)" ]; then
    fail "$2: files were left in TMPDIR: $(find "$1/tmp" -mindepth 1)"
  fi
  return 0
}

converged() { # what — the converge ran
  grep -q '^fugaro update --yes --config ' "$dir/state/calls" ||
    fail "$1: the converge never ran (calls: $(tr '\n' ';' <"$dir/state/calls"))"
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_contains "$out" "CHECK 401 RESULT END (exit 0)" "happy path: the end line"
  expect_contains "$out" "FACT xtrace: PASS — " "happy path: the tracing guard"
  expect_contains "$out" "FACT installation: PASS — " "happy path: the installation"
  expect_contains "$out" "project prj-sandbox-a" "happy path: the project doctor answers"
  expect_contains "$out" "FACT config: PASS — " "happy path: the config it starts from"
  expect_contains "$out" "version 1" "happy path: the pre-migration shape"
  expect_contains "$out" "FACT node-before: INFO — " "happy path: the node before"
  expect_contains "$out" "is absent" "happy path: there was no node yet"
  expect_contains "$out" "FACT update: PASS — " "happy path: the converge"
  expect_contains "$out" "3 grants written" "happy path: the roster it printed, as a count"
  expect_contains "$out" "2 operator and 1 admin" "happy path: the roles it printed"
  expect_contains "$out" "FACT node-after: PASS — " "happy path: the node after"
  expect_contains "$out" "member for member" "happy path: the node is this file's lists"
  expect_contains "$out" "FACT config-after: PASS — " "happy path: the file after"
  expect_contains "$out" "version 2" "happy path: the file is written as version 2"
  expect_contains "$out" "terraform.launchers is gone" "happy path: the retired key"
  expect_contains "$out" "FACT legacy: PASS — " "happy path: the legacy row"
  expect_contains "$out" "FACT plan: PASS — " "happy path: the plan"
  expect_contains "$out" "rename is pure" "happy path: an empty plan means a pure rename"
  converged "happy path"
  assert_no_address "$dir" "happy path"
  expect_scratch_gone "$dir" "happy path"
}

# THE RED STEP, kept as a test: a converge that writes no node prints no
# migration line, and the check FAILs on exactly that — an installation whose
# roles are still the file's is the state this whole task exists to leave.
test_a_converge_that_migrates_nothing_is_a_fail() {
  setup_case nomigration
  : >"$dir/state/update-output"
  rm -f "$dir/state/after-config" "$dir/state/after-node"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 1 "no migration"
  expect_contains "$out" "FACT update: FAIL — " "no migration: the update row fails"
  expect_contains "$out" "printed no migration line" "no migration: the row says what is missing"
  expect_contains "$out" "still the file's" "no migration: the row says what that leaves"
  expect_contains "$out" "FACT config-after: FAIL — " "no migration: the file never reached version 2"
  assert_no_address "$dir" "no migration"
}

# A node that is not what the run's own line says it wrote: the counts can agree
# and the members not, which is what this case is for.
test_a_node_that_is_not_what_the_line_named_is_a_fail() {
  setup_case wrongmembers
  write_node "$dir/state/after-node" "$ANA $CY" "$ANA"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 1 "wrong members"
  expect_contains "$out" "FACT update: PASS — " "wrong members: the line still says what was written"
  expect_contains "$out" "FACT node-after: FAIL — " "wrong members: the node row fails"
  expect_contains "$out" "not its members" "wrong members: the row says which comparison failed"
  assert_no_address "$dir" "wrong members"
}

# The retired key still in the file: an older CLI reading it would read a roster
# with another meaning, which is the one thing the version exists for.
test_a_retired_key_still_in_the_file_is_a_fail() {
  setup_case retiredkey
  {
    cat "$dir/state/after-config"
    printf '  launchers:\n  - %s\n' "$ANA"
  } >"$dir/state/after-config.tmp"
  mv "$dir/state/after-config.tmp" "$dir/state/after-config"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 1 "retired key"
  expect_contains "$out" "FACT config-after: FAIL — " "retired key: the file row fails"
  expect_contains "$out" "still carries terraform.launchers" "retired key: the row names the key"
  assert_no_address "$dir" "retired key"
}

# A plan with something in it: the rename is not pure, or the installation was
# not at the previous shape, and either way D1589's claim is not what happened.
test_a_plan_that_would_change_anything_is_a_fail() {
  setup_case planchange
  printf 'Plan: 1 to add, 0 to change, 1 to destroy.\n' >"$dir/state/plan-output"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 1 "plan change"
  expect_contains "$out" "FACT plan: FAIL — " "plan change: the plan row fails"
  expect_contains "$out" "planned a change" "plan change: the row says what it saw"
  expect_contains "$errtext" "Plan: 1 to add" "plan change: the plan itself is shown on the terminal"
  assert_no_address "$dir" "plan change"
}

# F25: a group: member is what a list may hold and a grant may not. The converge
# writes the node from the user: members and keeps the other one where it was, so
# the node is the user: members' and the file still carries the legacy member —
# which the check has to say is not a finding. A node that DID hold it, and a
# converge that DROPPED it, are the two failures either side of that.
test_the_legacy_forms_stay_out_of_the_node() {
  setup_case legacy
  write_config "$dir/config.yaml" 1 "$ANA $GROUP" "$ANA" "" "$PROJECT"
  write_config "$dir/state/after-config" 2 "" "$ANA $GROUP" "$ANA" "$PROJECT"
  write_node "$dir/state/after-node" "$ANA" "$ANA"
  migration_line "$ANA" "$ANA" >"$dir/state/update-output"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 0 "legacy forms"
  expect_contains "$out" "FACT config-after: PASS — " "legacy forms: the file row"
  expect_contains "$out" "no grant can hold" "legacy forms: the file row counts the legacy member"
  expect_contains "$out" "FACT legacy: PASS — " "legacy forms: the row"
  expect_contains "$out" "LEGACY IAM-ONLY" "legacy forms: the row quotes F25"
  expect_contains "$out" "the node holds none of them" "legacy forms: the node is the user: members'"
  assert_no_address "$dir" "legacy forms"

  # The other side of it, and the worse one: the member fell out of the list the
  # derive writes, which is exactly the revocation F25 is about.
  setup_case legacydropped
  write_config "$dir/config.yaml" 1 "$ANA $GROUP" "$ANA" "" "$PROJECT"
  the_migration "$dir" "$ANA" "$ANA"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 1 "legacy member dropped"
  expect_contains "$out" "FACT legacy: FAIL — " "legacy member dropped: the row fails"
  expect_contains "$out" "a revocation this rename must not cause" "legacy member dropped: the row says why"
  assert_no_address "$dir" "legacy member dropped"

  # And a grant the node may not hold.
  setup_case legacyinode
  write_config "$dir/config.yaml" 1 "$ANA $GROUP" "$ANA" "" "$PROJECT"
  the_migration "$dir" "$ANA" "$ANA"
  python3 - "$dir/state/after-node" "$GROUP" <<'PY'
import json, sys

path, group = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as f:
    doc = json.load(f)
doc["grants"].append({"user": group, "role": "operator", "scope": "project"})
with open(path, "w", encoding="utf-8") as f:
    json.dump(doc, f)
PY
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 1 "grant in the node"
  expect_contains "$out" "FACT legacy: FAIL — " "grant in the node: the row fails"
  expect_contains "$out" "never a grant" "grant in the node: the row says why"
  assert_no_address "$dir" "grant in the node"
}

# An installation that already migrated: the node is there, the file is version
# 2, the converge derives rather than writes, and the plan is still empty. A
# second run of the check has to be this, or the check could only be run once.
test_an_installation_that_already_migrated() {
  setup_case migrated
  write_config "$dir/config.yaml" 2 "" "$ANA $BO" "$ANA" "$PROJECT"
  write_node "$dir/state/node.json" "$ANA $BO" "$ANA"
  : >"$dir/state/update-output"
  rm -f "$dir/state/after-config" "$dir/state/after-node"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 0 "already migrated"
  expect_contains "$out" "FACT node-before: INFO — " "already migrated: the node before"
  expect_contains "$out" "migrated already" "already migrated: the row says so"
  expect_contains "$out" "FACT update: INFO — " "already migrated: the update row"
  expect_contains "$out" "derives the two lists from the node" "already migrated: the row says what the step does"
  expect_contains "$out" "FACT node-after: PASS — " "already migrated: the node after"
  expect_contains "$out" "as it did before this run" "already migrated: nothing was written"
  expect_contains "$out" "FACT plan: PASS — " "already migrated: the plan"
  assert_no_address "$dir" "already migrated"
}

# The drift case, live: this machine's copy is not the installation's own lists.
# The installation's roster wins and the converge says so out loud — the check
# reports it and does not call it a failure, because the run it is measuring is
# the one that has to move the roles off the file.
test_a_stale_copy_is_said_out_loud_and_is_not_a_fail() {
  setup_case drift
  write_config "$dir/config.yaml" 1 "$ANA $BO" "$ANA $CY" "" "$PROJECT"
  the_migration "$dir" "$ANA $BO" "$ANA"
  printf "fugaro update: this machine's terraform.admins is not what the installation's own outputs list, so this run migrates the installation's: the config lists two, the installation lists one\n" \
    >>"$dir/state/update-output"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 0 "drift"
  expect_contains "$out" "FACT node-after: PASS — " "drift: the node row passes"
  expect_contains "$out" "the installation's roster won" "drift: the row says which roster won"
  assert_no_address "$dir" "drift"
}

# A converge that does not finish: nothing below it is a verdict, and the run
# says so rather than offering the rows it never measured as passes.
test_a_converge_that_fails_stops() {
  setup_case fails
  printf '3\n' >"$dir/state/update.status"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "failed converge"
  expect_contains "$out" "FACT update: FAIL — " "failed converge: the update row"
  expect_contains "$out" "exited 3" "failed converge: the exit code is named"
  expect_contains "$out" "FACT node-after: FAIL — not reached" "failed converge: the rows below were not measured"
  expect_contains "$out" "FACT aborted: FAIL — " "failed converge: the abort row"
  expect_scratch_gone "$dir" "failed converge"
}

# The node cannot be read at all: the check has no before and therefore no
# answer, which is a stop rather than a block full of passes — and it must not
# converge anything while it cannot see what it would be measuring.
test_a_node_that_cannot_be_read_stops() {
  setup_case unreadable
  RUN401_ENV=(FAKE401_HTTP=403)
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "unreadable node"
  expect_contains "$out" "FACT node-before: FAIL — " "unreadable node: the row fails"
  expect_contains "$out" "could not be read" "unreadable node: the row says what failed"
  expect_contains "$errtext" "may not read" "unreadable node: the reason the database gave"
  expect_absent "$(cat "$dir/state/calls")" "fugaro update" "unreadable node: the converge still ran"
  expect_scratch_gone "$dir" "unreadable node"
}

# A read that fails the other way — no token at all — is the same stop.
test_a_token_that_cannot_be_had_stops() {
  setup_case notoken
  printf '1\n' >"$dir/state/token.status"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "no token"
  expect_contains "$out" "FACT node-before: FAIL — " "no token: the row fails"
  expect_absent "$(cat "$dir/state/calls")" "curl " "no token: the node was still read"
  expect_scratch_gone "$dir" "no token"
}

# `belong` is the owner's own installation: refused before doctor is asked.
test_belong_is_refused() {
  setup_case belong
  run401 --installation belong --config "$dir/config.yaml"
  expect_status "$status" 2 "belong"
  expect_contains "$out" "FACT installation: FAIL — " "belong: the installation row"
  expect_contains "$out" "the owner's own" "belong: the row says whose installation it is"
  expect_contains "$errtext" "never runs against 'belong'" "belong: the refusal names it"
  expect_absent "$(cat "$dir/state/calls")" "doctor" "belong: doctor was asked"
  assert_no_address "$dir" "belong"
}

# The name typed and the config in force are held to each other twice: the
# config's own project here, and doctor's answer in the test below.
test_the_name_and_the_config_have_to_agree() {
  setup_case otherproject
  write_config "$dir/config.yaml" 1 "$ANA" "$ANA" "" prj-sandbox-b
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "other project"
  expect_contains "$out" "FACT config: FAIL — " "other project: the config row"
  expect_contains "$out" "the prj-sandbox-b installation's" "other project: the row says which"
}

# doctor's answer disagreeing with the name typed is the same refusal: the check
# would otherwise read one installation's node and compare another's config.
test_doctor_disagreeing_with_the_name_stops() {
  setup_case doctorother
  write_doctor "$dir" prj-sandbox-b
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "doctor disagrees"
  expect_contains "$out" "FACT installation: FAIL — " "doctor disagrees: the installation row"
  expect_contains "$out" "never decides for itself" "doctor disagrees: the row says why"
}

# A config this check does not know: a version whose keys it cannot read is a
# stop, because guessing which list means which role is what the version is for.
test_a_config_version_this_check_does_not_know_stops() {
  setup_case version3
  write_config "$dir/config.yaml" 3 "" "$ANA" "$ANA" "$PROJECT"
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "version 3"
  expect_contains "$out" "FACT config: FAIL — " "version 3: the config row"
  expect_contains "$out" "which this check does not know" "version 3: the row says why"
}

# An installation with no Realtime Database has no grants node, so the check's
# whole subject is absent and it stops before converging anything.
test_a_config_without_a_database_stops() {
  setup_case nodatabase
  write_config "$dir/config.yaml" 1 "$ANA" "$ANA" "" "$PROJECT" no-rtdb
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 2 "no database"
  expect_contains "$out" "FACT config: FAIL — " "no database: the config row"
  expect_contains "$out" "no budget.rtdb_url" "no database: the row names the missing key"
}

# --dry-run: the guards run and the plan is printed, and nothing else is — no
# converge, no plan-only run, no read of the node.
test_dry_run_runs_nothing() {
  setup_case dryrun
  RUN401_ARGS=(--dry-run)
  run401 --installation "$PROJECT" --config "$dir/config.yaml"
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 401 DRY RUN" "dry run: the plan"
  expect_contains "$out" "fugaro update --yes --config $dir/config.yaml" "dry run: the converge it would run"
  expect_contains "$out" "fugaro update --plan-only --config $dir/config.yaml" "dry run: the plan-only line"
  expect_contains "$out" "what it leaves behind" "dry run: the note about the migration staying"
  expect_contains "$out" "FACT dry-run: PASS — " "dry run: recorded as its own row"
  expect_absent "$out" "not reached" "dry run: a wall of rows nothing measured"
  expect_absent "$(cat "$dir/state/calls")" "fugaro update" "dry run: a converge ran"
  expect_absent "$(cat "$dir/state/calls")" "curl" "dry run: the node was read"
  assert_no_address "$dir" "dry run"
  expect_scratch_gone "$dir" "dry run"
}

test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE401_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    bash -x "$script" --installation "$PROJECT" --config "$dir/config.yaml" 2>"$dir/err") || status=$?
  errtext=$(cat "$dir/err")
  expect_status "$status" 2 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL — " "xtrace: the row fails"
  expect_contains "$errtext" "this shell is tracing" "xtrace: the refusal"
  expect_absent "$(cat "$dir/state/calls")" "fugaro doctor" "xtrace: doctor was asked"
  assert_no_address "$dir" "xtrace"
  expect_scratch_gone "$dir" "xtrace"
}

test_usage() {
  setup_case usage
  run401
  expect_status "$status" 2 "no arguments"
  expect_contains "$errtext" "usage: check401.sh" "no arguments: the usage line"
  status=0
  out=$(env TMPDIR="$dir/tmp" PATH="$dir/bin:$PATH" bash "$script" --help 2>"$dir/err") || status=$?
  expect_status "$status" 0 "--help"
  expect_contains "$out" "--installation <project>" "--help: the flags"
  status=0
  out=$(env TMPDIR="$dir/tmp" PATH="$dir/bin:$PATH" bash "$script" --installation "$PROJECT" 2>"$dir/err") || status=$?
  expect_status "$status" 2 "no config"
  expect_contains "$(cat "$dir/err")" "FUGARO_CONFIG" "no config: the usage points at the default it reads"
}

# The assertion that holds the addresses out is worth only as much as its
# catching a script that leaks one. This is the mutation: a copy of the check
# whose block prints the roster it compared, which the assertion the suite uses
# on every other case has to catch.
test_the_address_assertions_catch_a_leaking_script() {
  setup_case leaking
  local copy=$dir/leak401.sh
  sed 's|^    print_block "\$facts" "\$verdict"$|    echo "LEAK ${AFTER_ROLES_OP:-}" >\&3; print_block "$facts" "$verdict"|' \
    "$script" >"$copy"
  grep -q 'LEAK' "$copy" || fail "mutation: the copy does not print the roster (the sed found no place to put it)"
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE401_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    bash "$copy" --installation "$PROJECT" --config "$dir/config.yaml" 2>"$dir/err") || status=$?
  errtext=$(cat "$dir/err")
  expect_status "$status" 0 "mutation: the leaking run itself"
  expect_contains "$out$errtext" "$SENTINEL" \
    "mutation: an address-printing script was NOT caught, so the assertion the suite uses is vacuous"
}

# Sharding, so the Go test in scripts/ can run this file's tests side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
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

t test_happy_path
t test_a_converge_that_migrates_nothing_is_a_fail
t test_a_node_that_is_not_what_the_line_named_is_a_fail
t test_a_retired_key_still_in_the_file_is_a_fail
t test_a_plan_that_would_change_anything_is_a_fail
t test_the_legacy_forms_stay_out_of_the_node
t test_an_installation_that_already_migrated
t test_a_stale_copy_is_said_out_loud_and_is_not_a_fail
t test_a_converge_that_fails_stops
t test_a_node_that_cannot_be_read_stops
t test_a_token_that_cannot_be_had_stops
t test_belong_is_refused
t test_the_name_and_the_config_have_to_agree
t test_doctor_disagreeing_with_the_name_stops
t test_a_config_version_this_check_does_not_know_stops
t test_a_config_without_a_database_stops
t test_dry_run_runs_nothing
t test_xtrace_is_refused
t test_usage
t test_the_address_assertions_catch_a_leaking_script

if [ "$failures" -ne 0 ]; then
  echo "check401_test: $failures failure(s)" >&2
  exit 1
fi
echo "check401_test: all tests passed" >&2
