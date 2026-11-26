#!/usr/bin/env bash
# Tests for scripts/live-checks/check-912-lift-refusal.sh. NOTHING here is a
# cloud, a project, an installation or a repository that matters: every case
# builds REAL local git checkouts with a bare origin of their own in a temporary
# directory and drives the check against a fake `fugaro` on PATH whose answers
# the case owns. The fake answers by the directory it is run in — the default
# branch's worktree or the follow-up ref's — so a case can bend exactly one of
# the two reads and see whether the check catches it.
#
# The readings are the point. Check 912 is the only thing that says whether
# release B refuses a lifted key where it must and stays a drop where it must
# not, so the cases that matter are the ones where the two halves disagree: a
# default branch that does not refuse, a follow-up ref that does, a sentence
# with no fix in it, one document refused at one ref and named at the other but
# not for the same keys, a recipes folder without PR5's cleanup line, an update
# that names nothing, and an update that cannot plan at all.
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

# The fake answers `config list --json`, `validate --json` — by the worktree it
# is run in — and `update --plan-only`, out of the case's own directory. Its
# stdin is read and logged, because no step of the check may wait on one, and
# every call is logged with the directory it was made in.
cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
kind=closed
if IFS= read -r -t 2 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
printf 'fugaro %s (in %s, stdin=%s)\n' "$*" "$PWD" "$kind" >>"${FAKE912_LOG:?}"
dir=${FAKE912_DIR:?}
case "${1:-}" in
--version)
  cat "$dir/version" 2>/dev/null || printf 'fugaro version 0.30.0\r\n'
  ;;
config)
  [ "${2:-}" = list ] || { printf 'fake fugaro: unexpected config verb %s\n' "${2:-<none>}" >&2; exit 64; }
  cat "$dir/config.json"
  ;;
validate)
  case "${PWD##*/}" in
  wt-default) side=default ;;
  wt-followup) side=older ;;
  *)
    printf 'fake fugaro: validate in an unexpected directory %s\n' "$PWD" >&2
    exit 64
    ;;
  esac
  [ -f "$dir/validate-$side.json" ] || { printf 'fake fugaro: no validate answer for %s\n' "$side" >&2; exit 64; }
  cat "$dir/validate-$side.json"
  exit "$(cat "$dir/validate-$side.rc" 2>/dev/null || printf 1)"
  ;;
update)
  printf '%s\n' "$*" | grep -q -- '--plan-only' || {
    printf 'fake fugaro: refused: Check 912 never runs the update without --plan-only\n' >&2
    exit 64
  }
  cat "$dir/plan.err" >&2 2>/dev/null || true
  cat "$dir/plan.out"
  exit "$(cat "$dir/plan.rc" 2>/dev/null || printf 0)"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

# --------------------------------------------------------------- the fixtures

# make_sandbox writes a real git checkout with a committed fugaro.yaml carrying
# every lifted key and a `.fugaro/recipes/default.yaml`, a bare origin of its
# own and TWO pushed commits — so the default branch has a parent, which is the
# follow-up ref the check reads by default — and then moves the local branch on
# purpose, with a dirty fugaro.yaml in the working tree, so a case can see
# whether a read came from a ref or from the tree.
make_sandbox() { # make_sandbox <checkout> <origin>
  mkdir -p "$1" "$2"
  git -C "$2" init -q --bare
  git -C "$1" init -q
  git -C "$1" symbolic-ref HEAD refs/heads/main
  git -C "$1" config user.email check912@example.invalid
  git -C "$1" config user.name check912
  mkdir -p "$1/.fugaro/recipes"
  cat >"$1/fugaro.yaml" <<'YAML'
version: 1
project: fugaro

agent:
  model: claude-sonnet-5
  models:
    coder: deepseek/deepseek-v4.1-flash

ab:
  rate: 0

rigor:
  default: standard

model_grades:
  strong:
    model: claude-opus-5-5
YAML
  printf 'recipe default\n' >"$1/.fugaro/recipes/default.yaml"
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add -A
  git -C "$1" commit -qm initial
  git -C "$1" remote add origin "$2"
  git -C "$1" push -q origin main
  printf 'a second commit, so the default branch has a parent\n' >"$1/NOTES.md"
  git -C "$1" add -A
  git -C "$1" commit -qm second
  git -C "$1" push -q origin main
  git -C "$1" fetch -q origin
  # The local branch is moved ON PURPOSE and NOT pushed: `origin/main` and its
  # parent are then the refs the check reads, and a read of the working tree
  # would answer something else.
  printf 'a local commit nobody pushed\n' >"$1/LOCAL.md"
  git -C "$1" add -A
  git -C "$1" commit -qm local-only
  perl -pi -e "s/^  model: .*/  model: from-the-working-tree/" "$1/fugaro.yaml"
}

config_json() { # config_json <out> <path> [notenrolled|unknown-path]
  python3 - "$@" <<'PY'
import json, sys
out, path = sys.argv[1], sys.argv[2]
kind = sys.argv[3] if len(sys.argv) > 3 else "ok"
listed = "/nowhere/else" if kind == "unknown-path" else path
doc = {"installations": [{"name": "fugaro", "gcp_project": "fugaro-dev", "base_branch": "main",
                          "checkouts": [{"repository": "github/acme/app", "path": listed,
                                         "base_branch": "main",
                                         "enrolled": kind != "notenrolled", "valid": True}]}],
       "warnings": []}
json.dump(doc, open(out, "w"))
PY
}

# sentence is one lifted key's own sentence, C1's, with the fix it pins. The two
# here that do not read "... is the project's choice" are the ones the script's
# own reader has to recognise by the phrase alone rather than by the words after
# it, which is why a case can bend one of them into a sentence with no fix.
sentence() { # sentence <key>
  case "$1" in
  agent.model) cat <<'EOF'
agent.model is the project's choice (D3237): remove it from fugaro.yaml; the project sets it with `fugaro models use --layer project`.
EOF
    ;;
  ab.rate) cat <<'EOF'
ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml; opt this repository out with `ab.repos.<owner/name>: 0` in the project layer.
EOF
    ;;
  rigor.default) cat <<'EOF'
rigor.default is the project's (D3237): this repository's level is `rigor.repos.<owner/name>` in the project layer; set it with `fugaro rigor set <mode> --repo <owner/name>`.
EOF
    ;;
  esac
}

cleanup_line() { printf 'git rm -r .fugaro/recipes removes the folder: every file in it is stored as the project recipe of the same name'; }

# drop_ab_block removes the `ab:` block from a checkout's fugaro.yaml and commits
# it on the local branch, which is what an owner part-way through the migration
# looks like: the tip of the default branch no longer writes that key while its
# parent still does. It is how a case makes the two refs carry different
# documents, which is the state in which the two halves are judged each on its
# own rather than against each other.
drop_ab_block() { # drop_ab_block <checkout>
  python3 - "$1/fugaro.yaml" <<'PY'
import sys

path = sys.argv[1]
keep, drop = [], False
for line in open(path):
    if line.startswith("ab:"):
        drop = True
        continue
    if drop and line[:1] in (" ", "\t"):
        continue
    drop = False
    keep.append(line)
open(path, "w").writelines(keep)
PY
  git -C "$1" add fugaro.yaml
  git -C "$1" commit -qm "the ab: block is gone on the default branch"
}

# validate_json writes one side's answer: <out> <valid:true|false> <channel:problems|notes> <key>...
validate_json() { # validate_json <out> <valid> <channel> <key|line>...
  python3 - "$@" <<'PY'
import json, sys
out, valid, channel = sys.argv[1], sys.argv[2] == "true", sys.argv[3]
sentences = {
    "agent.model": "agent.model is the project's choice (D3237): remove it from fugaro.yaml; the project sets it with `fugaro models use --layer project`.",
    "ab.rate": "ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml; opt this repository out with `ab.repos.<owner/name>: 0` in the project layer.",
    "rigor.default": "rigor.default is the project's (D3237): this repository's level is `rigor.repos.<owner/name>` in the project layer; set it with `fugaro rigor set <mode> --repo <owner/name>`.",
    "no-fix": "agent.models.coder is the project's choice (D3237).",
}
rows = []
for spec in sys.argv[4:]:
    if spec.startswith("@"):
        rows.append({"path": ".fugaro/recipes", "message": spec[1:]})
        continue
    rows.append({"path": spec, "message": sentences.get(spec, spec)})
doc = {"valid": valid, "problems": [], "warnings": [], "notes": []}
doc[channel] = rows
json.dump(doc, open(out, "w"))
PY
}

# case_dir builds one case's own world: a real checkout with its own bare
# origin, the installation's own project config, and the answers every side of
# the check gives. A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  confighome=$root/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS="$answers/calls"
  : >"$CALLS"
  app=$root/app
  make_sandbox "$app" "$root/app-origin.git"
  printf 'project: fugaro\nrepos:\n  acme/app:\n    base_branch: main\n' \
    >"$confighome/fugaro/projects/fugaro.yaml"
  config_json "$answers/config.json" "$app"
  printf 'fugaro version 0.31.0-release-b\n' >"$answers/version"
  # The default branch refuses both keys; the follow-up ref names them and
  # refuses nothing; the update names the repository. This is release B working.
  validate_json "$answers/validate-default.json" false problems agent.model ab.rate \
    "@$(cleanup_line)"
  printf 1 >"$answers/validate-default.rc"
  validate_json "$answers/validate-older.json" true notes agent.model ab.rate \
    "@$(cleanup_line)"
  printf 0 >"$answers/validate-older.rc"
  printf 'acme/app: writes agent.model and ab.rate; %s\n' "$(sentence agent.model)" \
    >"$answers/plan.out"
  : >"$answers/plan.err"
  printf 0 >"$answers/plan.rc"
}

# ------------------------------------------------------------------ the runs

run912_raw() { # run912_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE912_LOG="${CALLS:-/dev/null}" FAKE912_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" \
    bash "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run912() { # run912 [extra args...]
  run912_raw "$script" fugaro "${app:-$work/no-checkout}" "$@"
}

calls() { cat "${CALLS:-/dev/null}" 2>/dev/null || true; }

# ------------------------------------------------------------------- the cases

test_the_default_branch_is_refused_and_the_follow_up_ref_named() {
  case_dir happy_path
  run912
  expect_status "$status" 0 "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" default PASS "the happy path"
  expect_row "$out" older PASS "the happy path"
  expect_row "$out" recipes PASS "the happy path"
  expect_row "$out" update PASS "the happy path"
  expect_row "$out" writes PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  expect_contains "$out" "acme/app" "the premise row names the repository"
  # The check reads the two refs, not the working tree: one side is refused, the
  # other is named, and the two carry the same fugaro.yaml.
  expect_contains "$out" "the same fugaro.yaml" "the older row says the two refs carry one document"
  expect_contains "$(calls)" "wt-default, stdin=closed" "the default branch is read in its own worktree"
  expect_contains "$(calls)" "wt-followup, stdin=closed" "the follow-up ref is read in its own worktree"
}

test_the_worktrees_and_the_branch_are_taken_back() {
  case_dir taken_back
  run912
  expect_status "$status" 0 "the worktrees are taken back"
  expect_row "$out" restore PASS "the worktrees are taken back"
  local left
  left=$(git -C "$app" worktree list | grep -c . || true)
  [ "$left" = 1 ] || fail "the worktrees are taken back: git worktree list has $left entries: $(git -C "$app" worktree list)"
  if git -C "$app" rev-parse --verify --quiet refs/heads/chk912-followup-fugaro >/dev/null 2>&1; then
    fail "the worktrees are taken back: the follow-up branch is still there"
  fi
  [ ! -d "$app/wt-default" ] || fail "the worktrees are taken back: a worktree is still in the checkout"
}

test_the_working_tree_of_your_checkout_does_not_move() {
  case_dir untouched_tree
  local before after
  before=$(sha256sum "$app/fugaro.yaml" | cut -d' ' -f1)
  run912
  expect_status "$status" 0 "the working tree does not move"
  after=$(sha256sum "$app/fugaro.yaml" | cut -d' ' -f1)
  [ "$before" = "$after" ] || fail "the working tree does not move: fugaro.yaml changed"
  expect_contains "$(cat "$app/fugaro.yaml")" "from-the-working-tree" "the checkout's own dirty file is left alone"
}

test_a_default_branch_that_does_not_refuse_fails() {
  case_dir no_refusal
  # The owner has already removed the ab: block on the default branch, so the tip
  # and its parent carry different documents and the follow-up half is judged on
  # its own document: what this case isolates is the half that did not refuse.
  drop_ab_block "$app"
  validate_json "$answers/validate-default.json" true notes agent.model
  printf 0 >"$answers/validate-default.rc"
  run912
  expect_status "$status" 1 "a default branch that does not refuse"
  expect_row "$out" default FAIL "a default branch that does not refuse"
  expect_contains "$out" "did not refuse" "the row says what is wrong"
  expect_contains "$out" "brew upgrade" "the row names the fix"
  expect_contains "$out" "0.31.0-release-b" "the row names the binary that did not refuse"
  expect_row "$out" older PASS "a default branch that does not refuse: the other half still measured"
}

test_a_sentence_without_the_fix_fails() {
  case_dir no_fix
  validate_json "$answers/validate-default.json" false problems no-fix
  printf 1 >"$answers/validate-default.rc"
  run912
  expect_status "$status" 1 "a sentence without the fix"
  expect_row "$out" default FAIL "a sentence without the fix"
  expect_contains "$out" "carries no fix" "the row says what is missing"
}

test_a_key_refused_on_the_follow_up_ref_fails() {
  case_dir refused_on_the_older_ref
  validate_json "$answers/validate-older.json" false problems agent.model ab.rate
  printf 1 >"$answers/validate-older.rc"
  run912
  expect_status "$status" 1 "a key refused on the follow-up ref"
  expect_row "$out" older FAIL "a key refused on the follow-up ref"
  expect_contains "$out" "must be named and not refused" "the row says what release B promises"
}

test_a_follow_up_ref_that_names_nothing_fails() {
  case_dir older_names_nothing
  validate_json "$answers/validate-older.json" true notes
  run912
  expect_status "$status" 1 "a follow-up ref that names nothing"
  expect_row "$out" older FAIL "a follow-up ref that names nothing"
  expect_contains "$out" "named no lifted key at all" "the row says what is missing"
}

test_one_document_refused_and_named_for_different_keys_fails() {
  case_dir different_keys
  # The default branch refuses agent.model alone while the follow-up ref names
  # both: one document read at two refs, and the two halves do not agree, so the
  # refusal is not the mode of the ref following the file.
  validate_json "$answers/validate-default.json" false problems agent.model "@$(cleanup_line)"
  printf 1 >"$answers/validate-default.rc"
  run912
  expect_status "$status" 1 "one document, two key sets"
  expect_row "$out" older FAIL "one document, two key sets"
  expect_contains "$out" "the mode must follow the ref, not the file" "the row says why it matters"
}

test_two_refs_with_different_documents_are_not_compared_to_each_other() {
  case_dir different_documents
  # The tip of the default branch no longer writes ab.rate while its parent still
  # does, so the two documents differ: the key sets are then not compared against
  # each other, and the row says so rather than failing. This is what an owner
  # part-way through the migration of one file looks like.
  drop_ab_block "$app"
  validate_json "$answers/validate-default.json" false problems agent.model "@$(cleanup_line)"
  printf 1 >"$answers/validate-default.rc"
  run912
  expect_status "$status" 0 "two refs with different documents"
  expect_row "$out" default PASS "two refs with different documents"
  expect_row "$out" older PASS "two refs with different documents"
  expect_contains "$out" "the two refs' fugaro.yaml differ" "the row says the key sets were not compared"
}

test_a_recipes_folder_without_the_cleanup_line_fails() {
  case_dir no_cleanup_line
  validate_json "$answers/validate-default.json" false problems agent.model ab.rate
  printf 1 >"$answers/validate-default.rc"
  run912
  expect_status "$status" 1 "a recipes folder with no cleanup line"
  expect_row "$out" recipes FAIL "a recipes folder with no cleanup line"
  expect_contains "$out" "carries no cleanup line" "the row says what is missing"
}

test_a_ref_without_a_recipes_folder_says_so() {
  case_dir no_recipes_folder
  git -C "$app" rm -q -r --cached .fugaro/recipes >/dev/null
  git -C "$app" commit -qm "the folder is gone"
  run912
  expect_status "$status" 0 "a ref without a recipes folder"
  expect_row "$out" recipes PASS "a ref without a recipes folder"
  expect_contains "$out" "holds no .fugaro/recipes/" "the row says there was nothing to measure"
}

test_an_update_that_names_nothing_fails() {
  case_dir update_names_nothing
  printf 'the project layer is published; nothing else\n' >"$answers/plan.out"
  run912
  expect_status "$status" 1 "an update that names nothing"
  expect_row "$out" update FAIL "an update that names nothing"
  expect_contains "$out" "names acme/app nowhere" "the row says what is missing"
}

test_an_update_that_names_another_repository_does_not_name_this_one() {
  case_dir update_names_a_prefix
  # `acme/app-console` is not `acme/app`: a line that leads with another
  # repository's name names nothing about this one.
  printf 'acme/app-console: writes ab.rate; %s\n' "$(sentence ab.rate)" >"$answers/plan.out"
  run912
  expect_status "$status" 1 "an update that names another repository"
  expect_row "$out" update FAIL "an update that names another repository"
}

test_an_update_that_cannot_plan_is_unverified() {
  case_dir update_refused
  printf 'fugaro update: the project is not configured\n' >"$answers/plan.err"
  printf 1 >"$answers/plan.rc"
  run912
  expect_status "$status" 3 "an update that cannot plan"
  expect_row "$out" update UNVERIFIED "an update that cannot plan"
  expect_contains "$out" "the project is not configured" "the row carries the update's own message"
  expect_row "$out" default PASS "an update that cannot plan: the read still stands"
}

test_a_checkout_the_project_does_not_enroll_is_refused() {
  case_dir not_enrolled
  config_json "$answers/config.json" "$app" notenrolled
  run912
  expect_status "$status" 2 "a checkout the project does not enroll"
  expect_contains "$err" "does not enroll that repository" "a checkout the project does not enroll"
  expect_not_contains "$(calls)" "validate" "a checkout the project does not enroll: nothing was read"
}

test_a_checkout_the_installation_does_not_list_is_refused() {
  case_dir unknown_checkout
  config_json "$answers/config.json" "$app" unknown-path
  run912
  expect_status "$status" 2 "a checkout the installation does not list"
  expect_contains "$err" "is not a checkout fugaro config list --json knows" "a checkout the installation does not list"
}

test_a_missing_follow_up_ref_is_unverified() {
  case_dir no_older_ref
  run912 --older-ref origin/main~9
  expect_status "$status" 3 "a follow-up ref that is not there"
  expect_row "$out" premise UNVERIFIED "a follow-up ref that is not there"
  expect_contains "$out" "--older-ref" "the row names the flag's fix"
  expect_not_contains "$(calls)" "validate" "a follow-up ref that is not there: neither side is read"
  expect_row "$out" update PASS "a follow-up ref that is not there: the update is still read"
}

test_a_helper_branch_that_is_already_there_is_refused() {
  case_dir helper_branch_taken
  git -C "$app" branch chk912-followup-fugaro
  run912
  expect_status "$status" 2 "a helper branch that is already there"
  expect_contains "$err" "is already in" "a helper branch that is already there"
  expect_not_contains "$(calls)" "validate" "a helper branch that is already there: nothing was read"
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

test_help() {
  CALLS=
  run912_raw "$script" --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 912 — after release B" "help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run912 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 912 plan" "a dry run"
  expect_not_contains "$out" "CHECK 912 RESULT" "a dry run prints no block"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_no_project_is_refused() {
  CALLS=
  run912_raw "$script"
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no <project> was given" "no project"
}

test_no_checkout_is_refused() {
  CALLS=
  run912_raw "$script" fugaro
  expect_status "$status" 2 "no checkout"
  expect_contains "$err" "no <checkout> was given" "no checkout"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run912_raw "$script" --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

test_no_call_leaves_its_stdin_open_and_no_argument_carries_a_secret() {
  case_dir no_waiting
  run912
  expect_status "$status" 0 "no call waits on a stdin"
  local log
  log=$(calls)
  expect_contains "$log" "stdin=closed" "every call has its stdin closed"
  expect_not_contains "$log" "stdin=open" "no call waits on a stdin"
  expect_not_contains "$log" "stdin=line" "no call is fed a line"
  expect_not_contains "$log" "token" "no argument carries a token"
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

t test_the_default_branch_is_refused_and_the_follow_up_ref_named
t test_the_worktrees_and_the_branch_are_taken_back
t test_the_working_tree_of_your_checkout_does_not_move
t test_a_default_branch_that_does_not_refuse_fails
t test_a_sentence_without_the_fix_fails
t test_a_key_refused_on_the_follow_up_ref_fails
t test_a_follow_up_ref_that_names_nothing_fails
t test_one_document_refused_and_named_for_different_keys_fails
t test_two_refs_with_different_documents_are_not_compared_to_each_other
t test_a_recipes_folder_without_the_cleanup_line_fails
t test_a_ref_without_a_recipes_folder_says_so
t test_an_update_that_names_nothing_fails
t test_an_update_that_names_another_repository_does_not_name_this_one
t test_an_update_that_cannot_plan_is_unverified
t test_a_checkout_the_project_does_not_enroll_is_refused
t test_a_checkout_the_installation_does_not_list_is_refused
t test_a_missing_follow_up_ref_is_unverified
t test_a_helper_branch_that_is_already_there_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_help
t test_a_dry_run_calls_nothing
t test_no_project_is_refused
t test_no_checkout_is_refused
t test_an_unknown_argument_is_refused
t test_no_call_leaves_its_stdin_open_and_no_argument_carries_a_secret

if [ "$failures" -ne 0 ]; then
  echo "check-912-lift-refusal_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-912-lift-refusal_test.sh: ok"
