#!/usr/bin/env bash
# Tests for scripts/live-checks/check-912-lift-refusal.sh. NOTHING here is a
# cloud, a project, an installation or a repository that matters: every case
# builds a REAL local git checkout with a bare origin of its own in a temporary
# directory and drives the check against a fake `fugaro` on PATH whose answers
# the case owns — a validate that refuses on the default branch and names on
# another ref, and an update --plan-only that refuses a file still writing a
# lifted key.
#
# The readings are the point. Check 912 says release B refuses a lifted key on
# the default branch with its fix, names it on a follow-up ref, and refuses it in
# the plan, so the cases that matter are the ones where a reading is bent: a
# refusal that names no fix, a validate that accepts the file, a follow-up that
# is refused where it must only be named, a plan that accepts it, a refusal for
# some other reason, and — the one that would make the whole check a lie — a
# checkout the check must not switch and a file it must not write.
#
# Usage: bash scripts/live-checks/check-912-lift-refusal_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-912-lift-refusal.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK912_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK912_TEST_HEAD:-1600}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK912_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
# The release-B binary this check reads, as far as a case needs it: it reads the
# lifted key a repository's own fugaro.yaml writes, refuses it on the default
# branch with the sentence config.LiftedKey.Why carries, names it on any other
# ref, and refuses it in the plan. Each of those is bendable by a marker file in
# the case's answers directory.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE912_LOG:?}"
dir=${FAKE912_DIR:?}

# key_of is the one reading release B does: the first lifted key of
# config.LiftedKeys that this file writes, in that list's own order.
key_of() { # key_of <file>
  local file=$1 line role
  [ -f "$file" ] || return 0
  if grep -qE '^  rate:' "$file"; then printf 'ab.rate'; return 0; fi
  if grep -qE '^  model:' "$file"; then printf 'agent.model'; return 0; fi
  if grep -qE '^  default:' "$file"; then printf 'rigor.default'; return 0; fi
  if grep -qE '^  areas:' "$file"; then printf 'rigor.areas'; return 0; fi
  line=$(grep -m1 -E '^    (coder|reviewer|background|vision|diagnose):' "$file" || true)
  if [ -n "$line" ]; then
    role=${line#"${line%%[![:space:]]*}"}
    printf 'agent.models.%s' "${role%:}"
    return 0
  fi
  if grep -qE '^  strong:' "$file"; then printf 'model_grades.strong.model'; return 0; fi
  if grep -qE '^  strongest:' "$file"; then printf 'model_grades.strongest.model'; return 0; fi
}

sentence_of() { # sentence_of <key> <with-fix>
  local key=$1 fix=$2
  if [ "$fix" = no ]; then
    printf '%s is the project'\''s (D3237): remove it from fugaro.yaml.' "$key"
    return 0
  fi
  case "$key" in
  ab.rate)
    printf '%s is the project'\''s (D3237): remove the ab: block from fugaro.yaml; opt this repository out with `ab.repos.acme/app: 0` in the project layer.' "$key"
    ;;
  rigor.default)
    printf '%s is the project'\''s (D3237): this repository'\''s level is `rigor.repos.<owner/name>` in the project layer; set it with `fugaro rigor set <mode> --repo <owner/name>`.' "$key"
    ;;
  rigor.areas)
    printf '%s is the project'\''s (D3237): remove it from fugaro.yaml; the project states this repository'\''s areas as a member area of its own — `fugaro rigor set <mode> --area <glob> --name <name> --repo <owner/name>`.' "$key"
    ;;
  model_grades.*)
    printf '%s is the project'\''s (D3237): remove it from fugaro.yaml; the project layer states each grade'\''s model in `defaults.model_grades.<grade>.model`.' "$key"
    ;;
  *)
    printf '%s is the project'\''s choice (D3237): remove it from fugaro.yaml; the project sets it with `fugaro models use --layer project`.' "$key"
    ;;
  esac
}

case "${1:-}" in
--version)
  printf 'fugaro version 0.0.0-release-b\n'
  ;;
validate)
  path=${2:-.}
  key=$(key_of "$path/fugaro.yaml")
  [ -n "$key" ] || exit 0
  fix=yes
  [ -f "$dir/no-fix" ] && fix=no
  sentence=$(sentence_of "$key" "$fix")
  if [ -f "$dir/accept" ]; then exit 0; fi
  if [ -f "$dir/note-only" ]; then
    printf 'note: %s\n' "$sentence" >&2
    exit 0
  fi
  def=$(git -C "$path" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null || true)
  def=${def#origin/}
  cur=$(git -C "$path" branch --show-current 2>/dev/null || true)
  if [ "$cur" = "$def" ] || [ -f "$dir/refuse-on-followup" ]; then
    if [ -f "$dir/refuse-status" ]; then
      cat "$dir/refuse.err" >&2 2>/dev/null || true
      exit "$(cat "$dir/refuse-status")"
    fi
    printf 'fugaro validate: refused: %s\n' "$sentence" >&2
    exit 1
  fi
  printf 'note: %s\n' "$sentence" >&2
  ;;
update)
  printf '%s\n' "$*" | grep -q -- '--plan-only' || {
    printf 'fake fugaro: refused: this check runs the update only with --plan-only\n' >&2
    exit 64
  }
  if [ -f "$dir/plan-status" ]; then
    cat "$dir/plan.err" >&2 2>/dev/null || true
    exit "$(cat "$dir/plan-status")"
  fi
  if [ -f "$dir/plan-accepts" ]; then
    printf 'fugaro update --plan-only: the plan is written\n'
    exit 0
  fi
  key=$(key_of "$PWD/fugaro.yaml")
  [ -n "$key" ] || { printf 'fugaro update --plan-only: the plan is written\n'; exit 0; }
  printf 'fugaro update: refused: %s\n' "$(sentence_of "$key" yes)" >&2
  exit 1
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

# make_checkout writes a real git checkout of a repository the project enrolls,
# with a bare origin of its own and one pushed commit whose fugaro.yaml writes
# lifted keys. The local branch is then moved ON PURPOSE and not pushed: the
# second commit takes every lifted key back out of the file, so origin/main is
# what the check must copy and read, the working tree says something else, and a
# case can see which of the two the run is about. --no-lift commits the same
# keyless file to begin with, so both the ref and the working tree write none.
make_checkout() { # make_checkout <checkout> <origin> [--no-lift]
  mkdir -p "$1" "$2"
  git -C "$2" init -q --bare
  git -C "$1" init -q
  git -C "$1" symbolic-ref HEAD refs/heads/main
  git -C "$1" config user.email check912@example.invalid
  git -C "$1" config user.name check912
  if [ "${3:-}" = --no-lift ]; then
    write_keyless "$1"
  else
    cat >"$1/fugaro.yaml" <<'YAML'
version: 1
project: fugaro

agent:
  model: claude-sonnet-5
  models:
    coder: deepseek/deepseek-v4.1-flash
  max_budget_usd: 25

ab:
  rate: 0
YAML
  fi
  git -C "$1" add -A
  git -C "$1" commit -qm initial
  git -C "$1" remote add origin "$2"
  git -C "$1" push -q origin main
  git -C "$1" fetch -q origin
  git -C "$1" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main
  if [ "${3:-}" != --no-lift ]; then
    write_keyless "$1"
    git -C "$1" commit -qam "the local branch says something else"
  fi
}

# write_keyless is the file a repository is left with once every lifted key is
# gone: the values release B refuses are simply not there.
write_keyless() { # write_keyless <checkout>
  cat >"$1/fugaro.yaml" <<'YAML'
version: 1
project: fugaro

agent:
  max_budget_usd: 25
YAML
}

# case_dir builds one case's own world: one real checkout with its origin, the
# installation's own project config, and the fake in the state the happy path
# needs. A case then bends exactly one thing — a marker file in $answers, or one
# of the two arguments.
case_dir() { # case_dir <name> [--no-lift]
  local root=$work/$1
  answers=$root/answers
  confighome=$root/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS="$answers/calls"
  : >"$CALLS"
  app=$root/app
  make_checkout "$app" "$root/app-origin.git" "${2:-}"
  printf 'project: fugaro\nrepos:\n  acme/app:\n    base_branch: main\n' \
    >"$confighome/fugaro/projects/fugaro.yaml"
}

# ------------------------------------------------------------------ the runs

run912_raw() { # run912_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE912_LOG="${CALLS:-/dev/null}" FAKE912_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run912() { # run912 [extra args...]
  run912_raw fugaro "${app:-$work/app}" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run912
  expect_status "$status" 0 "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" refuse PASS "the happy path"
  expect_row "$out" name PASS "the happy path"
  expect_row "$out" plan PASS "the happy path"
  expect_row "$out" writes PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  expect_contains "$out" "CHECK 912 RESULT: PASS (exit 0)" "the happy path"
  # The key judged is the one the ref's own file writes, first in the contract's
  # order, and the premise says where the tree came from.
  expect_contains "$out" "origin/main" "the premise names the ref it copied"
  expect_contains "$out" "ab.rate" "the premise names the key it judges"
  # The copy carries the checkout's own origin remote and the block never prints
  # its URL: an origin can carry a token, and this block is what the owner copies
  # back out of their terminal.
  expect_not_contains "$out" "app-origin.git" "the premise does not print the origin URL"
  expect_not_contains "$err" "app-origin.git" "nothing else prints the origin URL"
  # The three reads really happened, on the copy, and only these three.
  local calls
  calls=$(cat "$CALLS")
  expect_contains "$calls" "fugaro validate" "validate is run"
  expect_contains "$calls" "fugaro update --plan-only --project fugaro" "the plan is run"
  [ "$(grep -c '^fugaro validate' "$CALLS")" = 2 ] ||
    fail "the happy path: expected two validate reads, saw $(grep -c '^fugaro validate' "$CALLS")"
  [ "$(grep -c '^fugaro update --plan-only' "$CALLS")" = 1 ] ||
    fail "the happy path: expected one plan read, saw $(grep -c '^fugaro update --plan-only' "$CALLS")"
}

test_the_owner_s_checkout_is_never_switched_or_written() {
  case_dir writes_nothing
  local before_head before_branch before_status before_file before_project
  before_head=$(git -C "$app" rev-parse HEAD)
  before_branch=$(git -C "$app" branch --show-current)
  before_status=$(git -C "$app" status --porcelain)
  before_file=$(cat "$app/fugaro.yaml")
  before_project=$(cat "$confighome/fugaro/projects/fugaro.yaml")
  run912
  expect_status "$status" 0 "writes nothing"
  [ "$(git -C "$app" rev-parse HEAD)" = "$before_head" ] || fail "writes nothing: the checkout's HEAD moved"
  [ "$(git -C "$app" branch --show-current)" = "$before_branch" ] || fail "writes nothing: the checkout was switched off $before_branch"
  [ "$(git -C "$app" status --porcelain)" = "$before_status" ] || fail "writes nothing: the checkout's status changed"
  [ "$(cat "$app/fugaro.yaml")" = "$before_file" ] || fail "writes nothing: the checkout's fugaro.yaml changed"
  [ "$(cat "$confighome/fugaro/projects/fugaro.yaml")" = "$before_project" ] ||
    fail "writes nothing: the project's local config changed"
  # The deliberate divergence between the working tree and origin/main is what
  # the check must read past: the file on the ref writes the keys, and the
  # working tree's own keyless file is the one left alone.
  if ! git -C "$app" show origin/main:fugaro.yaml | grep -q '^  rate:'; then
    fail "writes nothing: the fixture's own ref does not write a lifted key"
  fi
  if grep -q '^  rate:' "$app/fugaro.yaml"; then
    fail "writes nothing: the working tree's keyless file gained a lifted key"
  fi
  expect_contains "$out" "ab.rate" "the key judged is the ref's own"
  local other
  other=$(grep -v -E '^fugaro (validate|update --plan-only)' "$CALLS" || true)
  [ -z "$other" ] || fail "writes nothing: it ran something else: $other"
}

test_a_refusal_that_names_no_fix_is_a_fail() {
  case_dir no_fix
  printf yes >"$answers/no-fix"
  run912
  expect_status "$status" 1 "a refusal with no fix"
  expect_row "$out" refuse FAIL "a refusal with no fix"
  expect_contains "$out" "names no fix" "the row says what is missing"
  expect_row "$out" name PASS "the follow-up is unaffected"
}

test_a_validate_that_only_notes_the_key_is_a_fail() {
  # What release A does on every ref: a note and exit 0. On the default branch
  # release B refuses, so a note there is the release-A binary and this check is
  # the one that says so.
  case_dir note_only
  printf yes >"$answers/note-only"
  run912
  expect_status "$status" 1 "a note on the default branch"
  expect_row "$out" refuse FAIL "a note on the default branch"
  expect_contains "$out" "ACCEPTED" "the row says what it saw"
  expect_row "$out" name PASS "naming is what it must do on the other ref"
}

test_a_follow_up_that_is_refused_is_a_fail() {
  case_dir followup_refused
  printf yes >"$answers/refuse-on-followup"
  run912
  expect_status "$status" 1 "a follow-up that is refused"
  expect_row "$out" name FAIL "a follow-up that is refused"
  expect_contains "$out" "only NAMES it" "the row says what the ref is for"
  expect_row "$out" refuse PASS "the default branch is unaffected"
}

test_a_plan_that_accepts_the_file_is_a_fail() {
  case_dir plan_accepts
  printf yes >"$answers/plan-accepts"
  run912
  expect_status "$status" 1 "a plan that accepts"
  expect_row "$out" plan FAIL "a plan that accepts"
  expect_contains "$out" "ACCEPTED a repository file" "the row says what it saw"
}

test_a_refusal_for_another_reason_leaves_the_row_unverified() {
  case_dir other_reason
  printf 1 >"$answers/refuse-status"
  printf 'fugaro validate: no repository matches this checkout\n' >"$answers/refuse.err"
  run912
  expect_status "$status" 3 "a refusal for another reason"
  expect_row "$out" refuse UNVERIFIED "a refusal for another reason"
  expect_contains "$out" "does not name ab.rate" "the row says what it could not read"
}

test_a_plan_that_stops_for_another_reason_leaves_the_row_unverified() {
  case_dir plan_other
  printf 1 >"$answers/plan-status"
  printf 'fugaro update: terraform state lock is held\n' >"$answers/plan.err"
  run912
  expect_status "$status" 3 "a plan that stops"
  expect_row "$out" plan UNVERIFIED "a plan that stops"
  expect_contains "$out" "stopped for another reason" "the row says what it could not read"
}

test_a_file_that_writes_no_lifted_key_gets_one_in_the_copy() {
  case_dir no_lift --no-lift
  run912
  expect_status "$status" 0 "a file that writes no lifted key"
  expect_row "$out" premise PASS "a file that writes no lifted key"
  expect_row "$out" refuse PASS "a file that writes no lifted key"
  expect_contains "$out" "agent.models.coder was written into this check's own copy" "the premise says what it wrote"
  expect_not_contains "$(cat "$app/fugaro.yaml")" "coder:" "the checkout's own file was not written"
}

test_the_checkout_is_copied_and_the_ref_is_the_one_read() {
  case_dir copied
  run912
  expect_status "$status" 0 "the copy"
  # The copy carries the tree of origin/main, whose file writes the lifted keys,
  # and not the keyless file the working tree carries: a check that read the
  # working tree would have written a key into the copy and judged that one.
  expect_contains "$out" "the key its own file writes" "the key comes from the ref"
  expect_contains "$out" "ab.rate" "the ref's own first key is the one judged"
  expect_not_contains "$out" "written into this check's own copy" "nothing was written into the copy"
}

test_help() {
  CALLS=
  run912_raw --help
  expect_status "$status" 0 "check-912 --help"
  expect_contains "$out" "Usage:" "check-912 --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run912_raw --dry-run fugaro "$app"
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 912 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_no_project_is_refused() {
  CALLS=
  run912_raw
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no <project> was given" "no project"
}

test_no_checkout_is_refused() {
  case_dir no_checkout
  run912_raw fugaro
  expect_status "$status" 2 "no checkout"
  expect_contains "$err" "no <checkout> was given" "no checkout"
}

test_a_third_argument_is_refused() {
  case_dir three_args
  run912_raw fugaro "$app" "$app"
  expect_status "$status" 2 "a third argument"
  expect_contains "$err" "two arguments only" "a third argument"
}

test_a_checkout_that_is_not_a_git_checkout_is_refused() {
  case_dir not_a_checkout
  mkdir -p "$work/not-a-checkout"
  run912_raw fugaro "$work/not-a-checkout"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "is not a git checkout" "a directory that is not a checkout"
  [ ! -s "$CALLS" ] || fail "a directory that is not a checkout: it called something"
}

test_a_missing_project_config_is_refused() {
  case_dir no_config
  rm "$confighome/fugaro/projects/fugaro.yaml"
  run912
  expect_status "$status" 2 "no project config"
  expect_contains "$err" "is not there" "no project config"
}

test_a_checkout_that_names_no_default_branch_is_refused() {
  case_dir no_default
  git -C "$app" symbolic-ref --delete refs/remotes/origin/HEAD
  git -C "$app" checkout -q --detach
  run912
  expect_status "$status" 2 "no default branch"
  expect_contains "$err" "names no default branch" "no default branch"
}

test_the_default_branch_falls_back_to_the_checkouts_own_branch() {
  case_dir fallback
  git -C "$app" symbolic-ref --delete refs/remotes/origin/HEAD
  run912
  expect_status "$status" 0 "a checkout with no origin/HEAD"
  expect_contains "$out" "its origin/HEAD not being there" "the premise says where the branch came from"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE912_LOG="$CALLS" FAKE912_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash "$script" fugaro "$app" 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE912_LOG="$CALLS" FAKE912_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash -x "$script" fugaro "$app" 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run912_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

test_the_scratch_directory_is_removed() {
  case_dir keeps_nothing
  run912
  expect_row "$out" restore PASS "the scratch directory"
  expect_contains "$out" "the scratch directory is gone" "the restore row says what went"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK912_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK912_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_the_owner_s_checkout_is_never_switched_or_written
t test_a_refusal_that_names_no_fix_is_a_fail
t test_a_validate_that_only_notes_the_key_is_a_fail
t test_a_follow_up_that_is_refused_is_a_fail
t test_a_plan_that_accepts_the_file_is_a_fail
t test_a_refusal_for_another_reason_leaves_the_row_unverified
t test_a_plan_that_stops_for_another_reason_leaves_the_row_unverified
t test_a_file_that_writes_no_lifted_key_gets_one_in_the_copy
t test_the_checkout_is_copied_and_the_ref_is_the_one_read
t test_help
t test_a_dry_run_calls_nothing
t test_no_project_is_refused
t test_no_checkout_is_refused
t test_a_third_argument_is_refused
t test_a_checkout_that_is_not_a_git_checkout_is_refused
t test_a_missing_project_config_is_refused
t test_a_checkout_that_names_no_default_branch_is_refused
t test_the_default_branch_falls_back_to_the_checkouts_own_branch
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_an_unknown_argument_is_refused
t test_the_scratch_directory_is_removed

if [ "$failures" -ne 0 ]; then
  echo "check-912-lift-refusal_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-912-lift-refusal_test.sh: ok"
