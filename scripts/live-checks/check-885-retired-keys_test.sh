#!/usr/bin/env bash
# Tests for scripts/live-checks/check-885-retired-keys.sh. NOTHING here is a
# cloud, a project, an installation or a real repository: every case builds REAL
# local git checkouts and the installation's own local config in a temporary
# directory, and drives the check against a fake `fugaro` on PATH whose answers
# the case owns. No case can reach anything, because the only thing that reaches
# a cloud is `fugaro`, and every row of Check 885 is offline by design.
#
# The readings are the point. Check 885 asks whether release 2 rejects the keys
# it retired, with the fix, one row per shape: a `fugaro.yaml` writing
# agent.auth, agent.oauth_token, a craft's access or a grade's access, a project
# layer writing defaults.agent.auth, and the installation's OWN config writing
# default_claude_account or a stage's account key. So the cases that matter are
# the ones where a reading is bent: a document that is ACCEPTED, a refusal that
# names no key, a refusal that names no fix, a refusal that is about the copy
# rather than the key, an installation config that still carries a key, and the
# premises (a directory that is not a git checkout, a checkout of another
# project, a checkout whose own file is not the migrated state).
#
# Usage: bash scripts/live-checks/check-885-retired-keys_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-885-retired-keys.sh
work=$(mktemp -d)
confighome=$work/config
cleanup_all() {
  if [ -n "${CHECK885_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK885_TEST_HEAD:-1600}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK885_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE885_LOG:?}"
dir=${FAKE885_DIR:?}
case "${1:-}" in
validate)
  # `fugaro validate [path]` takes the fugaro.yaml to READ — config.ReadRegular
  # refuses a directory ("is not a regular file: not read") — so the fake takes
  # the last argument that is a regular file named fugaro.yaml, and answers per
  # checkout by the name of the directory it is in (`clean`, `agent-auth`,
  # `sub`, ...). An invocation with no such argument is answered as the refusal
  # it would really be, loudly: a check that passed the directory would read
  # this instead of the case's own answer.
  last=""
  for a in "$@"; do
    case "$a" in
    */fugaro.yaml | fugaro.yaml)
      if [ -f "$a" ]; then last=$a; fi
      ;;
    esac
  done
  if [ -z "$last" ]; then
    printf 'fake fugaro: validate reads a fugaro.yaml, and no argument was one\n' >&2
    exit 64
  fi
  key=$(basename "$(dirname "$last")")
  status=$(cat "$dir/validate-status-$key" 2>/dev/null || printf 0)
  cat "$dir/validate-$key.out" 2>/dev/null || true
  exit "$status"
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
  git -C "$1" config user.email check885@example.invalid
  git -C "$1" config user.name check885
  cat >"$1/fugaro.yaml" <<'YAML'
version: 1
project: test
agent:
  model: claude-sonnet-5
  crafts:
    coder:
      model: claude-sonnet-5
YAML
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add -A
  git -C "$1" commit -qm sandbox
  git -C "$1" remote remove origin 2>/dev/null || true
  git -C "$1" remote add origin "git@github.com:acme/$2.git"
}

# refusal writes a `fugaro validate` refusal for one checkout, in the shape
# release 2 answers with: the key it names and the fix it offers.
refusal() { # refusal <answers> <checkout-basename> <key> [bend]
  local answers=$1 key_name=$2 dotted=$3 bend=${4:-}
  printf 1 >"$answers/validate-status-$key_name"
  {
    printf 'fugaro.yaml: %s is retired (model-credentials-routing X2): a repository no\n' "$dotted"
    printf 'longer writes its own model credential, and a key nothing reads is a setting\n'
    printf "that never takes effect. Run 'fugaro update' in this checkout to be offered the\n"
    printf 'removal as a diff you commit yourself.\n'
  } >"$answers/validate-$key_name.out"
  case "$bend" in
  no-key)
    printf 'fugaro.yaml: this file is not readable by this release\n' >"$answers/validate-$key_name.out"
    ;;
  no-fix)
    printf 'fugaro.yaml: %s is retired\n' "$dotted" >"$answers/validate-$key_name.out"
    ;;
  esac
}

# case_dir builds one case's own world: the checkout, the installation's own
# config and every answer the fake gives, all in the state the happy path needs.
# A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  sub=$root/sub
  make_sandbox "$sub" sub-app
  confighome=$root/config
  mkdir -p "$confighome/fugaro/projects"
  # The installation's own config as `fugaro update` leaves it: no key of
  # either kind X2 retires.
  cat >"$confighome/fugaro/projects/test.yaml" <<'YAML'
project: test
gcp_project: test-project
agent:
  model: claude-sonnet-5
claude_accounts:
  personal:
    allow_data_to:
      - acme/sub-app
YAML
  config=$confighome/fugaro/projects/test.yaml
  # The owner's own checkout validates cleanly: it is the release-2 state. Its
  # answers are keyed by the checkout's own directory name (`sub`), which is how
  # the fake resolves the fugaro.yaml it was pointed at.
  printf 0 >"$answers/validate-status-sub"
  # The check's own copies, one per shape it measures.
  printf 0 >"$answers/validate-status-clean"
  : >"$answers/validate-clean.out"
  refusal "$answers" bent-agent-auth agent.auth
  refusal "$answers" bent-oauth-token agent.oauth_token
  refusal "$answers" bent-craft-access agent.crafts.coder.access
  refusal "$answers" bent-grade-access model_grades.strong.access
  # The two installation-config rows are resolved in a scratch checkout named
  # after the row, and the layer row in one named `layer`.
  refusal "$answers" stage-account stages.implement.claude_account
  refusal "$answers" default-account default_claude_account
  refusal "$answers" layer defaults.agent.auth
}

# ------------------------------------------------------------------ the runs

run885_raw() { # run885_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE885_LOG="${CALLS:-/dev/null}" FAKE885_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run885() { # run885 [extra args...]
  run885_raw --installation test --repo "$sub" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run885
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 885 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" clean PASS "the happy path"
  expect_row "$out" agent-auth PASS "the happy path"
  expect_row "$out" oauth-token PASS "the happy path"
  expect_row "$out" craft-access PASS "the happy path"
  expect_row "$out" grade-access PASS "the happy path"
  expect_row "$out" stage-account PASS "the happy path"
  expect_row "$out" default-account PASS "the happy path"
  expect_row "$out" layer-keys PASS "the happy path"
  expect_row "$out" config PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  # The rows really read what they say they read: one validation per shape, one
  # for the clean control, one per installation-config key, one for the layer,
  # and one in the owner's own checkout for the premise — all of them offline.
  local runs
  runs=$(grep -c '^fugaro validate ' "$CALLS" || true)
  [ "$runs" = 9 ] || fail "the happy path: expected 9 validations, saw $runs"
  expect_contains "$(cat "$CALLS")" "--offline" "the happy path: every validation is offline"
  expect_contains "$(cat "$CALLS")" "fugaro validate --offline --project-layer" "the layer row resolved its own layer file"
  expect_contains "$(cat "$CALLS")" "$sub" "the premise validated the owner's own checkout"
  # The positional argument is the FILE: `fugaro validate [path]` reads the
  # fugaro.yaml and refuses a directory, so a check that passed the checkout
  # itself would be measuring that refusal and not this release's key gate.
  expect_contains "$(cat "$CALLS")" "fugaro validate --offline $sub/fugaro.yaml" "the premise validated the checkout's fugaro.yaml"
  case "$(cat "$CALLS")" in
  *"validate --offline $sub "*) fail "the premise validated a directory: $(cat "$CALLS")" ;;
  esac
}

test_help() {
  CALLS=
  run885_raw --help
  expect_status "$status" 0 "check-885 --help"
  expect_contains "$out" "Usage:" "check-885 --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run885_raw --dry-run --installation test --repo "$sub"
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 885 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_a_missing_installation_is_refused() {
  CALLS=
  run885_raw --repo /tmp
  expect_status "$status" 2 "a missing installation"
  expect_contains "$err" "no --installation was given" "a missing installation"
}

test_a_missing_repository_is_refused() {
  CALLS=
  run885_raw --installation test
  expect_status "$status" 2 "a missing repository"
  expect_contains "$err" "no --repo was given" "a missing repository"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE885_LOG="$CALLS" FAKE885_DIR="$answers" \
    bash "$script" --installation test --repo "$sub" 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE885_LOG="$CALLS" FAKE885_DIR="$answers" \
    bash -x "$script" --installation test --repo "$sub" 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_a_repository_that_is_not_a_checkout_stops_the_check() {
  case_dir not_a_checkout
  run885 --repo "$work/not-a-repo"
  expect_status "$status" 2 "a repository that is not a checkout"
  expect_row "$out" premise FAIL "a repository that is not a checkout"
  expect_contains "$out" "is not a git checkout" "a repository that is not a checkout"
}

test_a_checkout_that_writes_a_retired_key_stops_the_check() {
  case_dir checkout_retired
  # The check's own copies are made from this file: one that already writes a
  # retired key is no control for a refusal.
  printf 'agent:\n  auth: oauth\n' >"$sub/fugaro.yaml"
  run885
  expect_status "$status" 2 "a checkout that writes a retired key"
  expect_row "$out" premise FAIL "a checkout that writes a retired key"
  expect_contains "$out" "still writes a retired key" "a checkout that writes a retired key"
}

test_a_checkout_that_does_not_validate_stops_the_check() {
  case_dir checkout_not_clean
  printf 1 >"$answers/validate-status-sub"
  printf 'fugaro.yaml: the file names a project this machine has no config for\n' \
    >"$answers/validate-sub.out"
  run885
  expect_status "$status" 2 "a checkout that does not validate"
  expect_row "$out" premise FAIL "a checkout that does not validate"
  expect_contains "$out" "does not validate" "a checkout that does not validate"
}

test_a_key_that_is_accepted_is_a_finding() {
  case_dir key_accepted
  printf 0 >"$answers/validate-status-bent-oauth-token"
  : >"$answers/validate-bent-oauth-token.out"
  run885
  expect_status "$status" 1 "a key that is accepted"
  expect_row "$out" oauth-token FAIL "a key that is accepted"
  expect_contains "$out" "ACCEPTED it (exit 0)" "a key that is accepted"
  expect_row "$out" agent-auth PASS "a key that is accepted"
}

test_a_refusal_that_names_no_key_leaves_the_row_unverified() {
  case_dir refusal_without_key
  refusal "$answers" bent-craft-access agent.crafts.coder.access no-key
  run885
  expect_status "$status" 3 "a refusal that names no key"
  expect_row "$out" craft-access UNVERIFIED "a refusal that names no key"
  expect_contains "$out" "names none of the retired keys" "a refusal that names no key"
}

test_a_refusal_that_names_no_fix_is_a_finding() {
  case_dir refusal_without_fix
  refusal "$answers" bent-grade-access model_grades.strong.access no-fix
  run885
  expect_status "$status" 1 "a refusal that names no fix"
  expect_row "$out" grade-access FAIL "a refusal that names no fix"
  expect_contains "$out" "names no fix" "a refusal that names no fix"
}

test_a_checkout_of_another_project_stops_the_check() {
  case_dir checkout_other_project
  # The two rows that bend the installation's own config are read by a command
  # that selects that config by the project the checkout names, so a checkout of
  # another project is one where those rows would measure nothing.
  printf 'version: 1\nproject: other\n' >"$sub/fugaro.yaml"
  run885
  expect_status "$status" 2 "a checkout of another project"
  expect_row "$out" premise FAIL "a checkout of another project"
  expect_contains "$out" "names 'other'" "a checkout of another project"
  [ ! -s "$CALLS" ] || fail "a checkout of another project: it called something: $(head -c 400 "$CALLS")"
}

test_an_account_key_that_is_accepted_is_a_finding() {
  case_dir account_key_accepted
  printf 0 >"$answers/validate-status-stage-account"
  : >"$answers/validate-stage-account.out"
  run885
  expect_status "$status" 1 "an account key that is accepted"
  expect_row "$out" stage-account FAIL "an account key that is accepted"
  expect_contains "$out" "ACCEPTED it (exit 0)" "an account key that is accepted"
  expect_row "$out" default-account PASS "an account key that is accepted"
}

test_a_config_key_refused_without_naming_a_key_leaves_the_row_unverified() {
  case_dir config_key_unnamed
  refusal "$answers" default-account default_claude_account no-key
  run885
  expect_status "$status" 3 "a config key refused without naming a key"
  expect_row "$out" default-account UNVERIFIED "a config key refused without naming a key"
  expect_contains "$out" "names none of the retired keys" "a config key refused without naming a key"
}

test_a_config_that_already_carries_a_key_leaves_the_rows_that_copy_it_unverified() {
  case_dir config_already_bent
  cat >"$config" <<'YAML'
project: test
gcp_project: test-project
default_claude_account: personal
YAML
  run885
  expect_status "$status" 1 "a config that already carries a key"
  expect_row "$out" config FAIL "a config that already carries a key"
  expect_row "$out" stage-account UNVERIFIED "a config that already carries a key"
  expect_row "$out" default-account UNVERIFIED "a config that already carries a key"
  expect_contains "$out" "already carries" "a config that already carries a key"
}

test_a_layer_that_is_refused_for_another_reason_leaves_the_row_unverified() {
  case_dir layer_other_reason
  printf 1 >"$answers/validate-status-layer"
  printf 'project layer: this document names no version\n' >"$answers/validate-layer.out"
  run885
  expect_status "$status" 3 "a layer refused for another reason"
  expect_row "$out" layer-keys UNVERIFIED "a layer refused for another reason"
  expect_contains "$out" "names none of the retired keys" "a layer refused for another reason"
}

test_an_installation_config_that_still_carries_routing_claude_is_a_finding() {
  case_dir config_routing_claude
  cat >"$config" <<'YAML'
project: test
gcp_project: test-project
routing:
  claude:
    via: cascade
YAML
  run885
  expect_status "$status" 1 "a config that still carries routing.claude"
  expect_row "$out" config FAIL "a config that still carries routing.claude"
  expect_contains "$out" "routing.claude" "a config that still carries routing.claude"
}

test_an_installation_config_that_still_carries_an_account_key_is_a_finding() {
  case_dir config_account_key
  cat >"$config" <<'YAML'
project: test
gcp_project: test-project
default_claude_account: personal
YAML
  run885
  expect_status "$status" 1 "a config that still carries an account key"
  expect_row "$out" config FAIL "a config that still carries an account key"
  expect_contains "$out" "default_claude_account" "a config that still carries an account key"
}

test_an_installation_config_that_cannot_be_read_leaves_the_row_unverified() {
  case_dir config_unreadable
  rm -f "$config"
  run885
  expect_status "$status" 3 "a config that cannot be read"
  expect_row "$out" config UNVERIFIED "a config that cannot be read"
  expect_contains "$out" "could not be read" "a config that cannot be read"
}

test_a_config_path_that_is_given_is_used() {
  case_dir config_given
  cat >"$confighome/fugaro/projects/other.yaml" <<'YAML'
project: other
routing:
  claude:
    via: cascade
YAML
  run885 --config "$confighome/fugaro/projects/other.yaml"
  expect_status "$status" 1 "a config path that is given"
  expect_row "$out" config FAIL "a config path that is given"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run885_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK885_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK885_TEST_ONLY" ] || return 0
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
t test_a_checkout_that_writes_a_retired_key_stops_the_check
t test_a_checkout_that_does_not_validate_stops_the_check
t test_a_key_that_is_accepted_is_a_finding
t test_a_refusal_that_names_no_key_leaves_the_row_unverified
t test_a_refusal_that_names_no_fix_is_a_finding
t test_a_checkout_of_another_project_stops_the_check
t test_an_account_key_that_is_accepted_is_a_finding
t test_a_config_key_refused_without_naming_a_key_leaves_the_row_unverified
t test_a_config_that_already_carries_a_key_leaves_the_rows_that_copy_it_unverified
t test_a_layer_that_is_refused_for_another_reason_leaves_the_row_unverified
t test_an_installation_config_that_still_carries_routing_claude_is_a_finding
t test_an_installation_config_that_still_carries_an_account_key_is_a_finding
t test_an_installation_config_that_cannot_be_read_leaves_the_row_unverified
t test_a_config_path_that_is_given_is_used
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check-885-retired-keys_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-885-retired-keys_test.sh: ok"
