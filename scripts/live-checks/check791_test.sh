#!/usr/bin/env bash
# Tests for scripts/live-checks/check791.sh. NOTHING here is a cloud, an
# installation, a project or a run: every case builds a REAL local git
# repository in a temporary directory, and drives the check against a fake
# `fugaro` on PATH whose answers the case owns. The check builds its own
# subjects — a scratch fugaro.yaml and project layer of its own writing, and a
# scratch checkout holding the sandbox's committed file with the mode off — so
# no case here touches the sandbox's committed state, and the check never
# launches a run that is not refused: no case can spend anything.
#
# The refusals are the point. Check 791 reads two of them — a model with no
# price in EVERY mode (D3020), and a repository's committed `budget.mode: off`
# (D3022) — and the cases that matter are the ones where the reading is bent: a
# validation that names the fix without refusing, one that refuses without
# naming it, a model the price table turns out to carry, a launch that is not
# refused at all, and a policy that says off and would refuse the launch for its
# own reason. Both rows hold under every mode, so an installation in enforce is
# one of the passing cases and not a finding.
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK791_TEST_HEAD:-1200}")" ;;
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
printf '%s\n' "$PWD" >>"${FAKE791_WHERE:?}"
# Every .yaml argument a command is handed must be a file the caller really
# wrote: the real CLI reads them, and a check that forgot to build its own
# subject would otherwise be answered by this fake all the same.
for arg in "$@"; do
  case "$arg" in
  *.yaml)
    [ -f "$arg" ] || {
      printf 'fake fugaro: %s is not a file (the check did not write its own subject)\n' "$arg" >&2
      exit 64
    }
    ;;
  esac
done
case "${1:-}" in
project)
  # project policy show [--project <name>] --json
  if [ -f "${FAKE791_DIR:?}/policy.json" ]; then cat "$FAKE791_DIR/policy.json"; fi
  exit "${FAKE791_POLICY_STATUS:-0}"
  ;;
budget)
  # budget prices --json: the effective price table, read offline
  if [ -f "${FAKE791_DIR:?}/prices.json" ]; then cat "$FAKE791_DIR/prices.json"; fi
  exit "${FAKE791_PRICES_STATUS:-0}"
  ;;
validate)
  # The row validates the same subject twice, once per mode, so a case can say
  # that the two legs answered differently.
  nth=$(( $(cat "${FAKE791_DIR:?}/validate.n" 2>/dev/null || echo 0) + 1 ))
  printf '%s' "$nth" >"${FAKE791_DIR}/validate.n"
  if [ "$nth" -ge 2 ] && [ -n "${FAKE791_VALIDATE2_STATUS:-}" ]; then
    if [ -f "${FAKE791_DIR}/validate2.txt" ]; then cat "${FAKE791_DIR}/validate2.txt"; fi
    exit "$FAKE791_VALIDATE2_STATUS"
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
# $2, with the working tree clean.
make_sandbox() { # make_sandbox <dir> <fugaro.yaml body>
  local dir=$1 body=$2
  mkdir -p "$dir"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email check791@example.invalid
  git -C "$dir" config user.name check791
  printf '%s' "$body" >"$dir/fugaro.yaml"
  git -C "$dir" remote add origin https://github.com/acme/sandbox-791.git
  git -C "$dir" add fugaro.yaml
  git -C "$dir" commit -qm "fugaro.yaml"
}

repo_yaml() { # repo_yaml [extra top-level line]
  cat <<YAML
version: 1
project: sandbox
gcp_project: sandbox-project
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
${1:-}
workflows:
  app:
    commands: { build: "true", test: "true" }
YAML
}

POLICY_OBSERVE='{"budget":{"mode":"observe"}}'
POLICY_ENFORCE='{"budget":{"mode":"enforce"}}'
POLICY_OFF='{"budget":{"mode":"off"}}'
PRICES='{"models":[{"id":"claude-priced-791"},{"id":"claude-other-791"}]}'
UNPRICED_ID=claude-check791-unpriced

# case_dir prepares one case's own directory: a sandbox, the fake's answers, and
# a fresh call log. It sets $sandbox and $answers.
case_dir() { # case_dir <name>
  sandbox=$work/$1/repo
  answers=$work/$1/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  WHERE="$answers/where"
  : >"$CALLS"
  : >"$WHERE"
  printf '%s' "$POLICY_OBSERVE" >"$answers/policy.json"
  printf '%s' "$PRICES" >"$answers/prices.json"
  printf 'agent.models.coder: %s has no price: add it under model_prices in the project config\n' "$UNPRICED_ID" >"$answers/validate.txt"
  printf "fugaro.yaml says budget.mode: off, which is not a mode any more: remove the key ('off' is accepted nowhere; the modes are observe and enforce)\n" >"$answers/run.txt"
  FAKE791_POLICY_STATUS=0
  FAKE791_PRICES_STATUS=0
  FAKE791_VALIDATE_STATUS=1
  FAKE791_VALIDATE2_STATUS=
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
    FAKE791_LOG="$CALLS" FAKE791_WHERE="$WHERE" FAKE791_DIR="$answers" \
    FAKE791_POLICY_STATUS="${FAKE791_POLICY_STATUS:-0}" \
    FAKE791_PRICES_STATUS="${FAKE791_PRICES_STATUS:-0}" \
    FAKE791_VALIDATE_STATUS="${FAKE791_VALIDATE_STATUS:-1}" \
    FAKE791_VALIDATE2_STATUS="${FAKE791_VALIDATE2_STATUS:-}" \
    FAKE791_RUN_STATUS="${FAKE791_RUN_STATUS:-1}" \
    ${RUN791_ENV[@]+"${RUN791_ENV[@]}"} \
    bash "$script" --sandbox "$sandbox" --installation test "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN791_ENV=()
}

# cwd_of_last prints the working directory the fake was last called from, which
# is where the command it stood in for really ran.
cwd_of_last() {
  tail -n 1 "$WHERE"
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
  make_sandbox "$sandbox" "$(repo_yaml)"
  run791 --dry-run
  expect_status "$status" 0 "the plan is not a run"
  expect_contains "$out" "Check 791 plan" "--dry-run prints the plan"
  expect_contains "$out" "model_prices" "the plan names the fix of the first refusal"
  expect_contains "$out" "budget.mode: off" "the plan names the second refusal's subject"
  expect_contains "$out" "every mode" "the plan says both rows hold under every mode"
  [ ! -s "$CALLS" ] || fail "a dry run made a call: $(cat "$CALLS")"
}

test_a_dry_run_does_not_claim_a_run_pass() {
  case_dir dry-run-honest
  make_sandbox "$sandbox" "$(repo_yaml)"
  run791 --dry-run
  expect_status "$status" 0 "the plan is not a run"
  expect_contains "$out" "NOT settled" "the plan says what this dry run cannot settle"
  expect_contains "$out" "only a real run" "and that a real run is what settles them"
  # Everything the mode-off subject depends on is on the list: the file it is
  # built from and the remote it is placed by. The base branch is not, and must
  # not be: the launch names the branch the scratch checkout holds, so no base
  # branch is resolved at all.
  expect_contains "$out" "'origin' remote" "the plan says the sandbox needs an origin"
  expect_contains "$out" "run --ref main" "the plan says which branch the launch is cut from"
  case "$out" in
  *git.base_branch*) fail "the plan lists a base branch premise the launch does not read" ;;
  esac
  [ ! -s "$CALLS" ] || fail "a dry run made a call: $(cat "$CALLS")"
}

test_an_observed_repository_reads_both_refusals() {
  case_dir happy
  make_sandbox "$sandbox" "$(repo_yaml)"
  RUN791_ENV=()
  run791
  expect_status "$status" 0 "both refusals are the states this check wants"
  expect_contains "$out" "premise" "the mode in force is a row"
  expect_contains "$out" "observe" "the row names the mode it read"
  expect_contains "$out" "unpriced" "the unnamed price is a row"
  expect_contains "$out" "model_prices" "the row names the fix"
  expect_contains "$out" "mode-off" "the committed off is a row"
  expect_contains "$out" "CHECK 791 RESULT: PASS (exit 0)" "the verdict is PASS"
  # D3020's row is measured under both modes, and the price table is read first:
  # the row is about every mode, so one mode is not the reading.
  got=$(grep -c '^fugaro validate ' "$CALLS" || true)
  [ "$got" = 2 ] || fail "the unpriced row measured one mode only: $got validate calls"
  grep -q 'fugaro budget prices ' "$CALLS" || fail "the price table was never read: $(cat "$CALLS")"
}

test_an_enforcing_installation_is_not_a_finding() {
  case_dir enforce
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf '%s' "$POLICY_ENFORCE" >"$answers/policy.json"
  run791
  expect_status "$status" 0 "D3020 and D3022 hold under every mode, enforce included"
  expect_contains "$out" "CHECK 791 RESULT: PASS (exit 0)" "an enforcing installation is a PASS"
}

test_a_policy_that_says_off_is_not_read_as_the_subject() {
  case_dir policy-off
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf '%s' "$POLICY_OFF" >"$answers/policy.json"
  run791
  expect_status "$status" 3 "a node that says off refuses a launch for its own reason"
  expect_contains "$out" "UNVERIFIED" "the mode row says it was not settled"
  expect_contains "$out" "published policy" "the row names the document it read"
}

test_an_unreadable_mode_is_not_a_pass() {
  case_dir unreadable
  make_sandbox "$sandbox" "$(repo_yaml)"
  rm -f "$answers/policy.json"
  FAKE791_POLICY_STATUS=1
  run791
  expect_status "$status" 3 "a mode nobody read is not a mode"
  expect_contains "$out" "UNVERIFIED" "the row says it was not measured"
  expect_contains "$out" "CHECK 791 RESULT: INCOMPLETE (exit 3)" "the verdict is INCOMPLETE"
}

test_a_validation_that_does_not_refuse_is_a_finding() {
  case_dir warns
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf 'note: add a model_prices entry for %s\n' "$UNPRICED_ID" >"$answers/validate.txt"
  FAKE791_VALIDATE_STATUS=0
  run791
  expect_status "$status" 1 "a warning that names the fix is not the refusal D3020 writes"
  expect_contains "$out" "exits 0" "the finding says what is wrong"
}

test_a_validation_that_passes_under_enforce_is_a_finding() {
  case_dir enforce-passes
  make_sandbox "$sandbox" "$(repo_yaml)"
  # The observe leg refuses (the case's default) and the enforce leg exits 0: a
  # status carried over from the leg before would read that exit 0 as the first
  # leg's refusal, and the row would pass a validation D3020 does not write.
  printf 'note: add a model_prices entry for %s\n' "$UNPRICED_ID" >"$answers/validate2.txt"
  FAKE791_VALIDATE2_STATUS=0
  run791
  expect_status "$status" 1 "one mode's refusal is not the refusal of every mode"
  expect_contains "$out" "under 'enforce' exits 0" "the finding names the mode that passed"
}

test_a_price_table_that_cannot_be_read_is_not_a_pass() {
  case_dir prices-unreadable
  make_sandbox "$sandbox" "$(repo_yaml)"
  rm -f "$answers/prices.json"
  FAKE791_PRICES_STATUS=1
  run791
  expect_status "$status" 3 "a table read that failed says nothing about whether the subject is priced"
  expect_contains "$out" "price table could not be read" "the row names the read that failed"
  expect_contains "$out" "UNVERIFIED" "the row says it was not measured"
}

test_a_price_table_with_no_models_is_not_a_pass() {
  case_dir prices-empty
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf '{}' >"$answers/prices.json"
  run791
  expect_status "$status" 3 "an empty table is not a table that does not carry the subject"
  expect_contains "$out" "no models at all" "the row says what it read"
}

test_a_refusal_without_the_fix_is_a_finding() {
  case_dir no-fix
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf 'agent.models.coder: %s is not a model this release knows\n' "$UNPRICED_ID" >"$answers/validate.txt"
  run791
  expect_status "$status" 1 "the person meeting the refusal has to be told which entry clears it"
  expect_contains "$out" "model_prices" "the finding names the fix the refusal left out"
}

test_a_table_that_carries_the_subject_is_not_a_pass() {
  case_dir priced-subject
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf '{"models":[{"id":"%s"},{"id":"claude-other-791"}]}' "$UNPRICED_ID" >"$answers/prices.json"
  run791
  expect_status "$status" 3 "a model the table carries has no subject for this row"
  expect_contains "$out" "IS priced here" "the row says why it has no subject"
}

test_a_launch_that_is_not_refused_is_a_finding() {
  case_dir launched
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf '{"run_id": "acme-app/20261120-120000-7910", "status": "running"}\n' >"$answers/run.txt"
  FAKE791_RUN_STATUS=0
  run791
  expect_status "$status" 1 "off is accepted nowhere"
  expect_contains "$out" "was NOT refused" "the finding says the launch went ahead"
  expect_contains "$out" "acme-app/20261120-120000-7910" "the finding names the run it launched"
  expect_contains "$out" "fugaro cancel --now acme-app/20261120-120000-7910" \
    "and the command that stops the run a bug let through"
}

test_a_refusal_that_never_names_off_is_a_finding() {
  case_dir unnamed
  make_sandbox "$sandbox" "$(repo_yaml)"
  printf 'fugaro.yaml: the budget block is not one this release reads\n' >"$answers/run.txt"
  run791
  expect_status "$status" 1 "the person meeting it has to be told which key to remove"
  expect_contains "$out" "never names the word 'off'" "the finding says what the refusal left out"
}

test_an_unreadable_committed_file_leaves_its_row_unverified() {
  case_dir no-committed
  mkdir -p "$sandbox"
  git -C "$sandbox" init -q -b main
  git -C "$sandbox" config user.email check791@example.invalid
  git -C "$sandbox" config user.name check791
  git -C "$sandbox" remote add origin https://github.com/acme/sandbox-791.git
  run791
  expect_status "$status" 3 "a repository with no committed file has no subject for D3022"
  expect_contains "$out" "committed" "the row names the file it could not read"
  expect_contains "$out" "unpriced" "the other row still ran"
}

test_the_launch_runs_from_the_checks_own_checkout() {
  case_dir subject
  make_sandbox "$sandbox" "$(repo_yaml)"
  before=$(git -C "$sandbox" status --porcelain)
  run791
  expect_status "$status" 0 "the check passes on a sandbox it never writes to"
  # The launch is made in the scratch checkout the check built, never in the
  # sandbox: the sandbox's own committed state is what a real run must not be
  # launched from, and it is left exactly as it was.
  cwd=$(cwd_of_last)
  [ "$cwd" = "$sandbox" ] && fail "the check launched in the sandbox itself"
  [ "$(git -C "$sandbox" status --porcelain)" = "$before" ] ||
    fail "the check changed the sandbox: $(git -C "$sandbox" status --porcelain)"
  git -C "$sandbox" log --format=%s -n 1 | grep -qx "fugaro.yaml" ||
    fail "the check added a commit to the sandbox"
  # The launch is cut from the branch the scratch checkout holds, so the base
  # branch the installation would otherwise resolve — and refuse over, naming
  # that branch rather than off — is never read.
  grep -q 'fugaro run --ref main --task-file' "$CALLS" ||
    fail "the launch did not name the branch the scratch checkout holds: $(grep 'fugaro run' "$CALLS" || true)"
}

test_a_missing_sandbox_is_refused() {
  case_dir missing
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE791_LOG="$CALLS" FAKE791_WHERE="$WHERE" FAKE791_DIR="$answers" \
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
  make_sandbox "$sandbox" "$(repo_yaml)"
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
t test_a_dry_run_does_not_claim_a_run_pass
t test_an_observed_repository_reads_both_refusals
t test_an_enforcing_installation_is_not_a_finding
t test_a_policy_that_says_off_is_not_read_as_the_subject
t test_an_unreadable_mode_is_not_a_pass
t test_a_validation_that_does_not_refuse_is_a_finding
t test_a_validation_that_passes_under_enforce_is_a_finding
t test_a_price_table_that_cannot_be_read_is_not_a_pass
t test_a_price_table_with_no_models_is_not_a_pass
t test_a_refusal_without_the_fix_is_a_finding
t test_a_table_that_carries_the_subject_is_not_a_pass
t test_a_launch_that_is_not_refused_is_a_finding
t test_a_refusal_that_never_names_off_is_a_finding
t test_an_unreadable_committed_file_leaves_its_row_unverified
t test_the_launch_runs_from_the_checks_own_checkout
t test_a_missing_sandbox_is_refused
t test_a_nonexistent_sandbox_is_refused
t test_an_agent_session_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check791_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check791_test.sh: ok"
