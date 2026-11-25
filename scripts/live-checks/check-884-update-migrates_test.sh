#!/usr/bin/env bash
# Tests for scripts/live-checks/check-884-update-migrates.sh. NOTHING here is a
# cloud, a project, an installation or a repository that matters: every case
# builds REAL local git checkouts in a temporary directory and drives the check
# against a fake `fugaro` on PATH whose answers the case owns — the
# installation's own update, each checkout's `update --repo`, the published
# policy and every refusal. No case can spend anything or reach anything,
# because the only thing that reaches the cloud is `fugaro`, and here that is a
# script in $work.
#
# The readings are the point. Check 884 asks two questions of one update: did it
# migrate the dev installation, and did any repository's source move? So the
# cases that matter are the ones where a reading is bent: an installation update
# that fails, one that left a step for you (not a failure — the command is
# unfinished, not refused), a published policy this CLI refuses or cannot read, a
# repository update that fails or is left for you, one that moves a ref of the
# checkout, and one that leaves a working-tree change: the last is the diff the
# update offers, which is its own behaviour and not a repository's source moving.
#
# Usage: bash scripts/live-checks/check-884-update-migrates_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-884-update-migrates.sh
work=$(mktemp -d)
confighome=$work/config
cleanup_all() {
  if [ -n "${CHECK884_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK884_TEST_HEAD:-1600}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK884_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE884_LOG:?}"
dir=${FAKE884_DIR:?}
case "${1:-}" in
update)
  if printf '%s\n' "$*" | grep -q -- '--repo'; then
    # The checkout is the last argument of `update --repo ... <path>`.
    for last in "$@"; do path=$last; done
    name=$(basename "$path")
    status=$(cat "$dir/repoupdate-status-$name" 2>/dev/null || printf 0)
    # What the update said, and what it did to the checkout when the case says
    # it did anything: a script run inside it, which is how a case moves a ref
    # or leaves a change behind. A nonzero answer prints the case's own stdout
    # too, so a case can answer with the sentence a step left for the owner
    # brings and still exit 1.
    if [ -f "$dir/repoupdate-does-$name" ]; then
      (cd "$path" && bash "$dir/repoupdate-does-$name") >/dev/null 2>&1 || true
    fi
    cat "$dir/repoupdate-$name.out" 2>/dev/null || true
    if [ "$status" != 0 ]; then
      if [ -f "$dir/repoupdate-$name.err" ]; then
        cat "$dir/repoupdate-$name.err" >&2
      else
        printf 'fake fugaro: the repository update was refused\n' >&2
      fi
      exit "$status"
    fi
    exit 0
  fi
  status=$(cat "$dir/install-status" 2>/dev/null || printf 0)
  cat "$dir/install.out" 2>/dev/null || true
  if [ "$status" != 0 ]; then
    if [ -f "$dir/install.err" ]; then cat "$dir/install.err" >&2; else printf 'fake fugaro: the update was refused\n' >&2; fi
    exit "$status"
  fi
  ;;
project)
  status=$(cat "$dir/policy-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    if [ -f "$dir/policy.err" ]; then cat "$dir/policy.err" >&2; else printf 'fake fugaro: the policy read was refused\n' >&2; fi
    exit "$status"
  fi
  cat "$dir/policy.json"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

# make_sandbox writes a real git checkout with a committed fugaro.yaml, an
# origin of its own and one commit.
make_sandbox() { # make_sandbox <dir> <name>
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" config user.email check884@example.invalid
  git -C "$1" config user.name check884
  cat >"$1/fugaro.yaml" <<'YAML'
version: 1
project: test
agent:
  model: claude-sonnet-5
YAML
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add -A
  git -C "$1" commit -qm sandbox
  git -C "$1" remote remove origin 2>/dev/null || true
  git -C "$1" remote add origin "git@github.com:acme/$2.git"
}

# policy_json writes the published policy this CLI reads, with `model_routing`
# and `pause` — the two top-level keys the plan's release 1 publishes.
policy_json() { # policy_json <out> [bend]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
doc = {"project": "test", "policy": {"version": 3,
                                     "model_routing": {"coder": [{"kind": "pool", "pool": "team"}]},
                                     "pause": {"wait_in_job": "5m", "min_pause": "30m", "max_pause": "6h"}}}
if sys.argv[2] == "no-new-keys":
    doc["policy"] = {"version": 3}
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# case_dir builds one case's own world: the two checkouts, the installation's
# own config and every answer the fake gives, all in the state the happy path
# needs. A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  sub=$root/sub
  other=$root/other
  make_sandbox "$sub" sub-app
  make_sandbox "$other" other-app
  confighome=$root/config
  mkdir -p "$confighome/fugaro/projects"
  printf 'project: test\nbudget:\n  rtdb_url: https://db.invalid\n' \
    >"$confighome/fugaro/projects/test.yaml"
  printf 'fugaro update: the installation test converged\n' >"$answers/install.out"
  policy_json "$answers/policy.json"
  printf 'fugaro update: acme/sub-app converged\n' >"$answers/repoupdate-sub.out"
  printf 'fugaro update: acme/other converged\n' >"$answers/repoupdate-other.out"
}

# ------------------------------------------------------------------ the runs

run884_raw() { # run884_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE884_LOG="${CALLS:-/dev/null}" FAKE884_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run884() { # run884 [extra args...]
  run884_raw --installation test --repo "$sub" --repo "$other" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run884
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 884 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" update PASS "the happy path"
  expect_row "$out" policy PASS "the happy path"
  expect_row "$out" source PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  # The rows really read what they say they read: the installation's own
  # update, one update per checkout, and the published policy of the project
  # this check was pointed at.
  local updates
  updates=$(grep -c '^fugaro update ' "$CALLS" || true)
  [ "$updates" = 3 ] || fail "the happy path: expected 3 updates, saw $updates"
  expect_contains "$(cat "$CALLS")" "fugaro update --project test" "the update row ran the installation's update"
  expect_contains "$(cat "$CALLS")" "--repo --project test $sub" "the source row updated the checkout it was pointed at"
  expect_contains "$(cat "$CALLS")" "fugaro project policy show --json --project test" "the policy row read the published policy"
}

test_help() {
  CALLS=
  run884_raw --help
  expect_status "$status" 0 "check-884 --help"
  expect_contains "$out" "Usage:" "check-884 --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run884_raw --dry-run --installation test --repo "$sub" --repo "$other"
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 884 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_a_missing_installation_is_refused() {
  CALLS=
  run884_raw --repo /tmp
  expect_status "$status" 2 "a missing installation"
  expect_contains "$err" "no --installation was given" "a missing installation"
}

test_a_missing_repository_is_refused() {
  CALLS=
  run884_raw --installation test
  expect_status "$status" 2 "a missing repository"
  expect_contains "$err" "no --repo was given" "a missing repository"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE884_LOG="$CALLS" FAKE884_DIR="$answers" \
    bash "$script" --installation test --repo "$sub" 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE884_LOG="$CALLS" FAKE884_DIR="$answers" \
    bash -x "$script" --installation test --repo "$sub" 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_a_repository_that_is_not_a_checkout_stops_the_check() {
  case_dir not_a_checkout
  run884 --repo "$work/not-a-repo"
  expect_status "$status" 2 "a repository that is not a checkout"
  expect_row "$out" premise FAIL "a repository that is not a checkout"
  expect_contains "$out" "is not a git checkout" "a repository that is not a checkout"
}

test_a_dirty_checkout_stops_the_check() {
  case_dir dirty_checkout
  # A checkout with uncommitted changes before the check starts is one whose
  # "did the update move anything" cannot be read: the change is already there.
  printf 'work in progress\n' >>"$sub/README.md"
  run884
  expect_status "$status" 2 "a dirty checkout"
  expect_row "$out" premise FAIL "a dirty checkout"
  expect_contains "$out" "has uncommitted changes" "a dirty checkout"
}

test_an_installation_update_that_fails_is_a_finding() {
  case_dir install_update_fails
  printf 1 >"$answers/install-status"
  printf 'fugaro update: terraform apply failed\n' >"$answers/install.err"
  run884
  expect_status "$status" 1 "an installation update that fails"
  expect_row "$out" update FAIL "an installation update that fails"
  expect_contains "$out" "did not exit 0" "an installation update that fails"
  expect_row "$out" source PASS "an installation update that fails"
}

test_a_policy_this_cli_refuses_is_a_finding() {
  case_dir policy_unknown_key
  # The pair did not converge: the published policy carries a key this CLI does
  # not know, which is the state `project policy show` refuses by name.
  printf 1 >"$answers/policy-status"
  printf 'fugaro: config/policy/v1 lists model_routing, which this Fugaro does not know: a key nobody reads is a policy that never takes effect; fix the policy, or upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run fugaro update\n' \
    >"$answers/policy.err"
  run884
  expect_status "$status" 1 "a policy this CLI refuses"
  expect_row "$out" policy FAIL "a policy this CLI refuses"
  expect_contains "$out" "does not know" "a policy this CLI refuses"
}

test_a_policy_read_that_does_not_answer_leaves_the_row_unverified() {
  case_dir policy_unreadable
  printf 7 >"$answers/policy-status"
  : >"$answers/policy.err"
  run884
  expect_status "$status" 3 "a policy read that does not answer"
  expect_row "$out" policy UNVERIFIED "a policy read that does not answer"
  expect_contains "$out" "could not be read" "a policy read that does not answer"
}

test_a_policy_without_the_new_keys_still_passes_and_says_so() {
  case_dir policy_without_keys
  policy_json "$answers/policy.json" no-new-keys
  run884
  expect_status "$status" 0 "a policy without the new keys"
  expect_row "$out" policy PASS "a policy without the new keys"
  expect_contains "$out" "carries neither" "a policy without the new keys"
}

test_a_repository_update_that_fails_is_a_finding() {
  case_dir repo_update_fails
  printf 1 >"$answers/repoupdate-status-other"
  run884
  expect_status "$status" 1 "a repository update that fails"
  expect_row "$out" source FAIL "a repository update that fails"
  expect_contains "$out" "acme/other" "a repository update that fails"
}

test_an_installation_update_that_leaves_a_step_for_you_is_unverified() {
  case_dir install_update_left_for_you
  # The update converged what asks nothing and stopped for a step only the owner
  # can take: it exits 1 (init_stages' notFinished) and says so, which is the
  # opposite of a refusal. The check runs the door with its output captured, so
  # that step is left rather than asked, and the migration is not measured.
  printf 1 >"$answers/install-status"
  printf 'fugaro update: test is not finished: 3 step(s) left for you, listed above\n' >"$answers/install.out"
  run884
  expect_status "$status" 3 "an installation update that leaves a step for you"
  expect_row "$out" update UNVERIFIED "an installation update that leaves a step for you"
  expect_contains "$out" "3 step(s) left for you" "an installation update that leaves a step for you"
  expect_contains "$out" "run 'fugaro update --project test' yourself" "an installation update that leaves a step for you"
  expect_row "$out" source PASS "an installation update that leaves a step for you"
  expect_not_contains "$out" "|FAIL|" "an installation update that leaves a step for you"
}

test_a_repository_update_that_leaves_a_step_for_you_is_unverified() {
  case_dir repo_update_left_for_you
  # A checkout whose update did not finish is one whose before and after this
  # check cannot read as "nothing moved": UNVERIFIED, naming the checkout and
  # the step, and NOT a source that moved.
  printf 1 >"$answers/repoupdate-status-other"
  printf 'fugaro update: other-app is not finished: 1 step(s) left for you, listed above\n' \
    >"$answers/repoupdate-other.out"
  run884
  expect_status "$status" 3 "a repository update that leaves a step for you"
  expect_row "$out" source UNVERIFIED "a repository update that leaves a step for you"
  expect_contains "$out" "other-app (exit 1, 1 step(s) left for you)" "a repository update that leaves a step for you"
  expect_contains "$out" "the MIGRATION IS NOT MEASURED there" "a repository update that leaves a step for you"
  expect_contains "$out" "Every checkout it did finish, it left alone" "a repository update that leaves a step for you"
  expect_contains "$out" "fugaro update --repo <checkout>' yourself" "a repository update that leaves a step for you"
  expect_not_contains "$out" "|FAIL|" "a repository update that leaves a step for you"
}

test_a_repository_update_that_moves_a_ref_is_a_finding() {
  case_dir repo_update_moves_a_ref
  cat >"$answers/repoupdate-does-other" <<'SH'
git tag release-1-migrated
SH
  run884
  expect_status "$status" 1 "a repository update that moves a ref"
  expect_row "$out" source FAIL "a repository update that moves a ref"
  expect_contains "$out" "moved a reference of acme/other" "a repository update that moves a ref"
}

test_a_repository_update_that_leaves_a_diff_is_not_a_source_moving() {
  case_dir repo_update_leaves_a_diff
  # The update offers the plugin pin's removal as a diff the owner commits or
  # does not (CLAUDE.md's HARD RULE): a change in the working tree is the
  # update's offer, not a repository's source moving.
  cat >"$answers/repoupdate-does-sub" <<'SH'
printf 'the removal the update offers\n' >>README.md
SH
  run884
  expect_status "$status" 0 "a repository update that leaves a diff"
  expect_row "$out" source PASS "a repository update that leaves a diff"
  expect_contains "$out" "left 1 working-tree change of acme/sub-app" "a repository update that leaves a diff"
  expect_contains "$out" "commits nothing" "a repository update that leaves a diff"
}

test_a_checkout_whose_head_moves_is_a_finding() {
  case_dir head_moves
  cat >"$answers/repoupdate-does-sub" <<'SH'
git commit -q --allow-empty -m "the update committed something"
SH
  run884
  expect_status "$status" 1 "a checkout whose head moves"
  expect_row "$out" source FAIL "a checkout whose head moves"
  expect_contains "$out" "moved a reference of acme/sub-app" "a checkout whose head moves"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run884_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK884_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK884_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help
t test_a_dry_run_calls_nothing
t test_a_missing_installation_is_refused
t test_a_missing_repository_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_repository_that_is_not_a_checkout_stops_the_check
t test_a_dirty_checkout_stops_the_check
t test_an_installation_update_that_fails_is_a_finding
t test_an_installation_update_that_leaves_a_step_for_you_is_unverified
t test_a_policy_this_cli_refuses_is_a_finding
t test_a_policy_read_that_does_not_answer_leaves_the_row_unverified
t test_a_policy_without_the_new_keys_still_passes_and_says_so
t test_a_repository_update_that_fails_is_a_finding
t test_a_repository_update_that_leaves_a_step_for_you_is_unverified
t test_a_repository_update_that_moves_a_ref_is_a_finding
t test_a_repository_update_that_leaves_a_diff_is_not_a_source_moving
t test_a_checkout_whose_head_moves_is_a_finding
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-884-update-migrates_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-884-update-migrates_test.sh: ok"
