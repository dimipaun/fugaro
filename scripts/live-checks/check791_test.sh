#!/usr/bin/env bash
# Tests for scripts/live-checks/check791.sh. NOTHING here is a cloud, an
# installation, a project or a run: every case builds a REAL local git
# repository in a temporary directory, commits the fugaro.yaml it wants read,
# and drives the check against a fake `fugaro` on PATH whose answers the case
# owns. The check itself never launches a run, so no case here can spend
# anything.
#
# The refusals are the point. Check 791 reads two of them — a model with no price
# under observe (D3020), and a committed `budget.mode: off` (D3022) — and the
# cases that matter are the ones where the reading is bent: a mode in force that
# is not observe, a validation that names the fix without refusing, a repository
# whose committed file does not say off, a launch that is not refused at all.
#
# Usage: bash scripts/live-checks/check791_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check791.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK791_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK791_TEST_HEAD:-900}")" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

# ---------------------------------------------------------------- the fakes
fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"${FAKE791_LOG:?}"
case "${1:-}" in
project)
  # project policy show --project <name> --json
  if [ -f "${FAKE791_DIR:?}/policy.json" ]; then cat "$FAKE791_DIR/policy.json"; fi
  exit "${FAKE791_POLICY_STATUS:-0}"
  ;;
validate)
  # The real `fugaro validate [path]` reads a FILE (`readFugaroYAML`), so a
  # directory here is the mistake a fake that answers anything would hide: the
  # real CLI answers "is not a regular file" and the check would read that as a
  # refusal that never names model_prices.
  if [ -n "${2:-}" ] && [ ! -f "${2:-}" ]; then
    printf 'fake fugaro: validate was given %s, which is not a file (the real CLI reads a fugaro.yaml, never a directory)\n' "$2" >&2
    exit 64
  fi
  if [ -f "${FAKE791_DIR:?}/validate.txt" ]; then cat "$FAKE791_DIR/validate.txt"; fi
  exit "${FAKE791_VALIDATE_STATUS:-0}"
  ;;
run)
  if [ -f "${FAKE791_DIR:?}/run.txt" ]; then cat "$FAKE791_DIR/run.txt"; fi
  exit "${FAKE791_RUN_STATUS:-0}"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# make_sandbox writes a git repository at $1 whose committed fugaro.yaml holds
# $2, with the working tree clean. A case that wants the tree to differ from HEAD
# appends to the file afterwards.
make_sandbox() { # make_sandbox <dir> <fugaro.yaml body>
  local dir=$1 body=$2
  mkdir -p "$dir"
  git -C "$dir" init -q
  git -C "$dir" config user.email check791@example.invalid
  git -C "$dir" config user.name check791
  printf '%s' "$body" >"$dir/fugaro.yaml"
  git -C "$dir" add fugaro.yaml
  git -C "$dir" commit -qm "fugaro.yaml"
}

off_yaml() {
  cat <<'YAML'
agent:
  model: unpriced-model-791
budget:
  mode: off
YAML
}
observe_yaml() {
  cat <<'YAML'
agent:
  model: unpriced-model-791
budget:
  mode: observe
YAML
}

POLICY_OBSERVE='{"budget":{"mode":"observe"}}'
POLICY_ENFORCE='{"budget":{"mode":"enforce"}}'

# case_dir prepares one case's own directory: a sandbox, the fake's answers, and
# a fresh call log. It sets $sandbox and $answers.
case_dir() { # case_dir <name>
  sandbox=$work/$1/repo
  answers=$work/$1/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  printf '%s' "$POLICY_OBSERVE" >"$answers/policy.json"
  printf 'fugaro.yaml: unpriced-model-791 cannot be priced; add a model_prices entry for it\n' >"$answers/validate.txt"
  printf "fugaro.yaml says budget.mode: off, which is not a mode any more: remove the key ('off' is accepted nowhere; the modes are observe and enforce)\n" >"$answers/run.txt"
  FAKE791_VALIDATE_STATUS=1
  FAKE791_RUN_STATUS=1
}

# run791 runs the check with the agent markers stripped (this suite runs inside an
# agent's session, and the check's own first refusal IS those markers — the case
# below sets one back to read it). Sets $status and $out.
run791() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" \
    FAKE791_LOG="$CALLS" FAKE791_DIR="$answers" \
    FAKE791_POLICY_STATUS="${FAKE791_POLICY_STATUS:-0}" \
    FAKE791_VALIDATE_STATUS="${FAKE791_VALIDATE_STATUS:-1}" \
    FAKE791_RUN_STATUS="${FAKE791_RUN_STATUS:-1}" \
    ${RUN791_ENV[@]+"${RUN791_ENV[@]}"} \
    bash "$script" --sandbox "$sandbox" --installation test "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN791_ENV=()
}

test_help() {
  status=0
  out=$(PATH="$fakebin:$PATH" bash "$script" --help 2>&1) || status=$?
  expect_status "$status" 0 "check791.sh --help"
  expect_contains "$out" "Usage:" "--help prints the usage"
  expect_contains "$out" "--sandbox" "--help names the one required flag"
}

test_a_dry_run_calls_nothing() {
  case_dir dry-run
  make_sandbox "$sandbox" "$(off_yaml)"
  run791 --dry-run
  expect_status "$status" 0 "the plan is not a run"
  expect_contains "$out" "Check 791 plan" "--dry-run prints the plan"
  expect_contains "$out" "model_prices" "the plan names the fix of the first refusal"
  expect_contains "$out" "budget.mode: off" "the plan names the second refusal's subject"
  [ ! -s "$CALLS" ] || fail "a dry run made a call: $(cat "$CALLS")"
}

test_an_observed_repository_reads_both_refusals() {
  case_dir happy
  make_sandbox "$sandbox" "$(off_yaml)"
  RUN791_ENV=()
  run791
  expect_status "$status" 0 "both refusals are the states this check wants"
  expect_contains "$out" "premise" "the mode in force is a row"
  expect_contains "$out" "observe" "the row names the mode it read"
  expect_contains "$out" "unpriced" "the unnamed price is a row"
  expect_contains "$out" "model_prices" "the row names the fix"
  expect_contains "$out" "mode-off" "the committed off is a row"
  expect_contains "$out" "CHECK 791 RESULT: PASS (exit 0)" "the verdict is PASS"
}

test_a_mode_that_is_not_observe_is_a_finding() {
  case_dir enforce
  make_sandbox "$sandbox" "$(off_yaml)"
  printf '%s' "$POLICY_ENFORCE" >"$answers/policy.json"
  run791
  expect_status "$status" 1 "D3020 and D3022 are about observe"
  expect_contains "$out" "premise" "the mode row is the finding"
  expect_contains "$out" "not 'observe'" "the finding names what it read"
  expect_contains "$out" "CHECK 791 RESULT: FAIL (exit 1)" "the verdict is FAIL"
}

test_an_unreadable_mode_is_not_a_pass() {
  case_dir unreadable
  make_sandbox "$sandbox" "$(off_yaml)"
  rm -f "$answers/policy.json"
  FAKE791_POLICY_STATUS=1
  run791
  expect_status "$status" 3 "a mode nobody read is not a mode of observe"
  expect_contains "$out" "UNVERIFIED" "the row says it was not measured"
  expect_contains "$out" "CHECK 791 RESULT: INCOMPLETE (exit 3)" "the verdict is INCOMPLETE"
}

test_a_validation_that_does_not_refuse_is_a_finding() {
  case_dir warns
  make_sandbox "$sandbox" "$(off_yaml)"
  printf 'note: add a model_prices entry for unpriced-model-791\n' >"$answers/validate.txt"
  FAKE791_VALIDATE_STATUS=0
  run791
  expect_status "$status" 1 "a warning that names the fix is not the refusal D3020 writes"
  expect_contains "$out" "NAMES 'model_prices' and exits 0" "the finding says what is wrong"
}

test_a_clean_validation_leaves_the_row_unverified() {
  case_dir priced
  make_sandbox "$sandbox" "$(off_yaml)"
  printf 'fugaro.yaml is valid\n' >"$answers/validate.txt"
  FAKE791_VALIDATE_STATUS=0
  run791
  expect_status "$status" 3 "a repository whose model IS priced has no subject for this row"
  expect_contains "$out" "IS priced here" "the row says why it has no subject"
}

test_a_committed_file_without_off_leaves_its_row_unverified() {
  case_dir no-off
  make_sandbox "$sandbox" "$(observe_yaml)"
  run791
  expect_status "$status" 3 "D3022 is about a file that says off"
  expect_contains "$out" "not 'off'" "the row names what the committed file carries"
  expect_contains "$out" "unpriced" "the other row still ran"
}

test_a_launch_that_is_not_refused_is_a_finding() {
  case_dir launched
  make_sandbox "$sandbox" "$(off_yaml)"
  printf '{"run_id": "acme-app/20261120-120000-7910", "status": "running"}\n' >"$answers/run.txt"
  FAKE791_RUN_STATUS=0
  run791
  expect_status "$status" 1 "off is accepted nowhere"
  expect_contains "$out" "was NOT refused" "the finding says the launch went ahead"
  expect_contains "$out" "acme-app/20261120-120000-7910" "the finding names the run it launched"
  expect_contains "$out" "fugaro cancel --now acme-app/20261120-120000-7910" \
    "and the command that stops the run a bug let through"
}

test_a_dirty_working_tree_is_not_read_as_the_committed_file() {
  case_dir dirty
  make_sandbox "$sandbox" "$(observe_yaml)"
  printf 'budget:\n  mode: off\n' >>"$sandbox/fugaro.yaml"
  run791
  expect_status "$status" 3 "the committed file and the validated file must be the same document"
  expect_contains "$out" "differs from its HEAD" "the row says why it stopped reading"
}

test_a_missing_sandbox_is_refused() {
  case_dir missing
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE791_LOG="$CALLS" FAKE791_DIR="$answers" \
    bash "$script" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  expect_status "$status" 2 "the checkout is the one thing this check reads"
  expect_contains "$err" "no --sandbox" "the refusal says which flag is missing"
}

test_a_nonexistent_sandbox_is_refused() {
  case_dir nope
  sandbox=$work/nope/not-there
  run791
  expect_status "$status" 2 "a path with no checkout is refused"
  expect_contains "$err" "not a directory" "the refusal says what the path is"
}

test_an_agent_session_is_refused() {
  case_dir agent
  make_sandbox "$sandbox" "$(off_yaml)"
  RUN791_ENV=(CLAUDECODE=1)
  run791
  expect_status "$status" 2 "no agent runs any step of Check 791"
  expect_contains "$err" "CLAUDECODE" "the refusal names the marker it found"
  expect_contains "$err" "coding agent" "the refusal says what the session is"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK791_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK791_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK791_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_a_dry_run_calls_nothing
t test_an_observed_repository_reads_both_refusals
t test_a_mode_that_is_not_observe_is_a_finding
t test_an_unreadable_mode_is_not_a_pass
t test_a_validation_that_does_not_refuse_is_a_finding
t test_a_clean_validation_leaves_the_row_unverified
t test_a_committed_file_without_off_leaves_its_row_unverified
t test_a_launch_that_is_not_refused_is_a_finding
t test_a_dirty_working_tree_is_not_read_as_the_committed_file
t test_a_missing_sandbox_is_refused
t test_a_nonexistent_sandbox_is_refused
t test_an_agent_session_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check791_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check791_test.sh: ok"
