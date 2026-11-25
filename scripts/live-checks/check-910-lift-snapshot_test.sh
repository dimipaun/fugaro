#!/usr/bin/env bash
# Tests for scripts/live-checks/check-910-lift-snapshot.sh. NOTHING here is a
# cloud, a project, an installation or a repository that matters: every case
# builds REAL local git checkouts with a bare origin of their own in a temporary
# directory and drives the check against a fake `fugaro` on PATH whose answers
# the case owns — the installation list, each repository's settings rows, its
# recipes and the update's plan lines. No case can spend anything or reach
# anything, because the only thing that reaches the cloud is `fugaro`, and here
# that is a script in $work.
#
# The readings are the point. Check 910 records what a repository means before
# L8 moves it into the project layer, so the cases that matter are the ones where
# a reading is bent: a project `config list` does not list, a repository the
# project enrolls with no checkout on this machine, a checkout that is not
# usable, a settings read that refuses, a ref that is not there, and — the one
# that would make the whole record a lie — a working tree that differs from the
# ref, which must NOT be what the snapshot holds.
#
# Usage: bash scripts/live-checks/check-910-lift-snapshot_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-910-lift-snapshot.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK910_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK910_TEST_HEAD:-1600}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK910_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE910_LOG:?}"
dir=${FAKE910_DIR:?}
name=${PWD##*/}
case "${1:-}" in
--version)
  printf 'fugaro version 0.0.0-pre-l8\n'
  ;;
config)
  cat "$dir/config.json"
  ;;
settings)
  repo=
  prev=
  for a in "$@"; do
    if [ "$prev" = --repo ]; then repo=$a; fi
    prev=$a
  done
  if [ -f "$dir/settings-status" ]; then
    cat "$dir/settings.err" >&2 2>/dev/null || true
    exit "$(cat "$dir/settings-status")"
  fi
  cat "$dir/settings-$(printf '%s' "$repo" | tr '/' '-').json"
  ;;
recipes)
  case "${2:-}" in
  ls)
    if [ -f "$dir/recipes-ls-status" ]; then
      cat "$dir/recipes.err" >&2 2>/dev/null || true
      exit "$(cat "$dir/recipes-ls-status")"
    fi
    cat "$dir/recipes-$name.json"
    ;;
  show)
    if [ -f "$dir/show-status" ]; then
      cat "$dir/show.err" >&2 2>/dev/null || true
      exit "$(cat "$dir/show-status")"
    fi
    cat "$dir/show-$name-${3:-}.json"
    ;;
  *)
    printf 'fake fugaro: unexpected recipes verb %s\n' "${2:-<none>}" >&2
    exit 64
    ;;
  esac
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
  cat "$dir/plan.out"
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
# every lifted key, a `.fugaro/recipes/default.yaml`, a bare origin of its own
# and one pushed commit. The local branch is then moved ON PURPOSE and not
# pushed: origin/<branch> stays where the snapshot must read, the working tree
# and HEAD say something else, and a case can see which of the two the record
# holds.
make_sandbox() { # make_sandbox <checkout> <origin> <owner/name> <model>
  mkdir -p "$1" "$2"
  git -C "$2" init -q --bare
  git -C "$1" init -q
  git -C "$1" symbolic-ref HEAD refs/heads/main
  git -C "$1" config user.email check910@example.invalid
  git -C "$1" config user.name check910
  mkdir -p "$1/.fugaro/recipes"
  cat >"$1/fugaro.yaml" <<YAML
version: 1
project: fugaro

agent:
  model: $4
  models:
    coder: deepseek/deepseek-v4.1-flash
    reviewer: claude-opus-5-5
  crafts:
    coder:
      model: deepseek/deepseek-v4.1-flash
    senior-review:
      model: claude-opus-5-5
  levels:
    l2:
      model: claude-opus-5-5
  max_budget_usd: 500  # kept, not lifted

ab:
  rate: 0

rigor:
  default: standard
  areas:
    - name: docs
      paths: ["docs/**"]
      level: high

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
  git -C "$1" fetch -q origin
  perl -pi -e "s/^  model: .*/  model: from-the-working-tree/" "$1/fugaro.yaml"
  git -C "$1" commit -qam "the local branch says something else"
}

config_json() { # config_json <out> <row>...
  python3 - "$@" <<'PY'
import json, sys
out, rows = sys.argv[1], sys.argv[2:]
checkouts = []
for spec in rows:
    kind, rest = spec.split("=", 1)
    if kind == "ok":
        repo, path = rest.split("|")
        checkouts.append({"repository": "github/" + repo, "path": path, "base_branch": "main",
                          "enrolled": True, "valid": True})
    elif kind == "invalid":
        repo, path, reason = rest.split("|")
        checkouts.append({"repository": "github/" + repo, "path": path, "base_branch": "main",
                          "enrolled": True, "valid": False, "reason": reason})
    elif kind == "notenrolled":
        repo, path = rest.split("|")
        checkouts.append({"repository": "github/" + repo, "path": path, "base_branch": "main",
                          "enrolled": False, "valid": True})
doc = {"installations": [{"name": "fugaro", "gcp_project": "fugaro-dev", "base_branch": "main",
                          "checkouts": checkouts}], "warnings": []}
json.dump(doc, open(out, "w"))
PY
}

settings_json() { # settings_json <out> <owner/name> [migrated]
  python3 - "$@" <<'PY'
import json, sys
out, repo = sys.argv[1], sys.argv[2]
migrated = len(sys.argv) > 3 and sys.argv[3] == "migrated"
vals = {
    "agent.model": ("claude-sonnet-5", "repo"),
    "agent.models.coder": ("deepseek/deepseek-v4.1-flash", "repo"),
    "agent.models.reviewer": ("claude-opus-5-5", "repo"),
    "agent.crafts.coder.model": ("deepseek/deepseek-v4.1-flash", "repo"),
    "agent.crafts.senior-review.model": ("claude-opus-5-5", "repo"),
    "agent.levels.l1.model": ("deepseek/deepseek-v4.1-flash", "repo"),
    "agent.levels.l2.model": ("claude-opus-5-5", "repo"),
    "ab.rate": ("0", "repo"),
    "rigor.default": ("standard", "repo"),
    "rigor.areas": ('{"docs": "high"}', "repo"),
    "model_grades.strong.model": ("claude-opus-5-5", "repo"),
    "agent.max_budget_usd": ("500", "repo"),
}
if migrated:
    vals = {k: (v, "policy" if w == "repo" else w) for k, (v, w) in vals.items()}
rows = []
for scope in ("project", "repo:" + repo):
    for k, (v, w) in sorted(vals.items()):
        row = {"key": k, "scope": scope, "wins": w, "state": "ok", "default": {"unset": "none"}}
        if v is not None:
            row["value"] = v
        rows.append(row)
json.dump({"version": 1, "project": "fugaro", "generated_at": "2026-11-24T00:00:00Z",
           "sources": [], "settings": rows}, open(out, "w"))
PY
}

recipes_json() { # recipes_json <out>
  python3 - "$1" <<'PY'
import json, sys
json.dump({"recipes": [{"name": "default", "source": "fugaro/recipes/default.yaml", "launchable": True}],
           "notes": []}, open(sys.argv[1], "w"))
PY
}

show_json() { # show_json <out>
  python3 - "$1" <<'PY'
import json, sys
json.dump({"name": "default", "source": "repo", "where": ".fugaro/recipes/default.yaml",
           "sha256": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
           "yaml": "name: default\n"}, open(sys.argv[1], "w"))
PY
}

# case_dir builds one case's own world: two real checkouts with their origins,
# the installation's own project config, and every answer the fake gives in the
# state the happy path needs. A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  confighome=$root/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS="$answers/calls"
  : >"$CALLS"
  app=$root/app
  svc=$root/svc
  make_sandbox "$app" "$root/app-origin.git" acme/app claude-sonnet-5
  make_sandbox "$svc" "$root/svc-origin.git" acme/svc claude-sonnet-5
  printf 'project: fugaro\nrepos:\n  acme/app:\n    base_branch: main\n  acme/svc:\n    base_branch: main\n' \
    >"$confighome/fugaro/projects/fugaro.yaml"
  config_json "$answers/config.json" "ok=acme/app|$app" "ok=acme/svc|$svc"
  settings_json "$answers/settings-acme-app.json" acme/app
  settings_json "$answers/settings-acme-svc.json" acme/svc
  recipes_json "$answers/recipes-app.json"
  recipes_json "$answers/recipes-svc.json"
  show_json "$answers/show-app-default.json"
  show_json "$answers/show-svc-default.json"
  printf 'fugaro update --plan-only: acme/app: coder deepseek/deepseek-v4.1-flash -> deepseek/deepseek-v4.1-flash (the project\x27s)\n' \
    >"$answers/plan.out"
  snapfile=$root/snapshot
}

# ------------------------------------------------------------------ the runs

run910_raw() { # run910_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE910_LOG="${CALLS:-/dev/null}" FAKE910_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run910() { # run910 [extra args...]
  run910_raw fugaro --out "${snapfile:-$work/snapshot}" "$@"
}

snap() { cat "${snapfile:-$work/snapshot}" 2>/dev/null || true; }

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run910
  expect_status "$status" 3 "the happy path: the update's lines are not captured yet"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" raw PASS "the happy path"
  expect_row "$out" resolved PASS "the happy path"
  expect_row "$out" recipes PASS "the happy path"
  expect_row "$out" plan UNVERIFIED "the happy path"
  expect_row "$out" writes PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  expect_contains "$out" "check-910-lift-snapshot.sh fugaro --plan $snapfile" "the plan row names the one command left"
  # The snapshot is the record, and it holds both repositories.
  local s
  s=$(snap)
  expect_contains "$s" 'meta "project" "fugaro"' "the snapshot names its project"
  expect_contains "$s" 'meta "binary" "fugaro version 0.0.0-pre-l8"' "the snapshot names the binary it was taken with"
  expect_contains "$s" "repo \"acme/app\" \"origin/main\" \"$app\"" "the snapshot names the ref it read"
  expect_contains "$s" "repo \"acme/svc\" \"origin/main\" \"$svc\"" "the snapshot names both repositories"
  # Every lifted key, with the repository's own text for it.
  expect_contains "$s" 'raw "acme/app" "agent.model" "  model: claude-sonnet-5"' "agent.model"
  expect_contains "$s" 'raw "acme/app" "agent.models.coder" "    coder: deepseek/deepseek-v4.1-flash"' "agent.models.*"
  expect_contains "$s" 'raw "acme/app" "agent.crafts.senior-review.model" "      model: claude-opus-5-5"' "agent.crafts.*.model"
  expect_contains "$s" 'raw "acme/app" "agent.levels.l2.model" "      model: claude-opus-5-5"' "agent.levels.lN.model"
  expect_contains "$s" 'raw "acme/app" "ab.rate" "  rate: 0"' "ab.rate"
  expect_contains "$s" 'raw "acme/app" "rigor.default" "  default: standard"' "rigor.default"
  expect_contains "$s" 'raw "acme/app" "rigor.areas" "  areas:\n    - name: docs\n      paths: [\"docs/**\"]\n      level: high"' "rigor.areas"
  expect_contains "$s" 'raw "acme/app" "model_grades.strong.model" "    model: claude-opus-5-5"' "model_grades.*.model"
  expect_contains "$s" 'raw "acme/app" "model_grades.strongest.model" "<absent>"' "a lifted key the file does not set"
  expect_contains "$s" 'raw "acme/app" "agent.levels.l3.model" "<absent>"' "an absent level"
  expect_not_contains "$s" 'agent.max_budget_usd' "a kept key is not part of the record"
  # The raw recipe files and the resolved recipe sums.
  expect_contains "$s" 'recipe-file "acme/app" ".fugaro/recipes/default.yaml" "sha256:' "the recipe file at the ref"
  expect_contains "$s" 'recipe "acme/app" "default" "repo" "sha256:1111' "the resolved recipe and its sum"
  # What a run resolves.
  expect_contains "$s" 'resolved "acme/app" "repo:acme/app" "agent.model" "claude-sonnet-5" "repo" "ok"' "the resolved model"
  expect_contains "$s" 'resolved "acme/app" "repo:acme/app" "rigor.areas" "{\"docs\": \"high\"}" "repo" "ok"' "the resolved areas"
  # The readings really happened, one read per repository.
  expect_contains "$(cat "$CALLS")" "fugaro config list --json" "the repository list is read from the CLI"
  expect_contains "$(cat "$CALLS")" "fugaro settings --repo acme/app --json" "the resolved rows are read per repository"
  expect_contains "$(cat "$CALLS")" "fugaro recipes show default --json" "each recipe's sum is read"
}

test_the_raw_values_come_from_the_ref_and_not_the_working_tree() {
  case_dir the_ref
  # The fixture's local branch says `from-the-working-tree`; origin/main says
  # `claude-sonnet-5`. The record must hold the ref's value: the migration reads
  # the default branch, and a snapshot of whatever happens to be checked out
  # would make the comparison about the owner's working tree.
  run910
  local s
  s=$(snap)
  expect_contains "$s" 'raw "acme/app" "agent.model" "  model: claude-sonnet-5"' "the ref's value"
  expect_not_contains "$s" "from-the-working-tree" "nothing comes from the working tree"
  expect_contains "$(cat "$app/fugaro.yaml")" "from-the-working-tree" "the fixture still says the working tree differs"
}

test_the_snapshot_run_writes_nothing_but_the_snapshot() {
  case_dir writes_nothing
  local before_config after_config
  before_config=$(cat "$confighome/fugaro/projects/fugaro.yaml")
  run910
  after_config=$(cat "$confighome/fugaro/projects/fugaro.yaml")
  [ "$before_config" = "$after_config" ] || fail "writes nothing: the installation's project config changed"
  [ -z "$(git -C "$app" status --porcelain)" ] || fail "writes nothing: acme/app has changes: $(git -C "$app" status --porcelain)"
  [ -z "$(git -C "$svc" status --porcelain)" ] || fail "writes nothing: acme/svc has changes: $(git -C "$svc" status --porcelain)"
  [ "$(git -C "$app" rev-parse refs/remotes/origin/main)" = "$(git -C "$app" rev-parse HEAD~1)" ] ||
    fail "writes nothing: the ref the check read moved"
  # Every call is a read. The only writing verb this check owns is
  # `update --plan-only`, and no case of the snapshot run may run it.
  local other
  other=$(grep -v -E '^fugaro (config list|settings|recipes|--version)' "$CALLS" || true)
  [ -z "$other" ] || fail "writes nothing: it ran something else: $other"
  [ ! -e "$work/snapshot.check910" ] || fail "writes nothing: a temporary file was left beside the snapshot"
}

test_help() {
  CALLS=
  run910_raw --help
  expect_status "$status" 0 "check-910 --help"
  expect_contains "$out" "Usage:" "check-910 --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run910_raw --dry-run fugaro --out "$work/snapshot"
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 910 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
  [ ! -e "$work/snapshot" ] || fail "a dry run: it wrote the snapshot"
}

test_no_project_is_refused() {
  CALLS=
  run910_raw --out "$work/snapshot"
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no <project> was given" "no project"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE910_LOG="$CALLS" FAKE910_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash "$script" fugaro --out "$work/snapshot" 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE910_LOG="$CALLS" FAKE910_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash -x "$script" fugaro --out "$work/snapshot" 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run910_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

test_ref_with_plan_is_refused() {
  case_dir two_runs
  run910_raw fugaro --ref origin/main --plan "$work/nothing-here"
  expect_status "$status" 2 "--ref with --plan"
  expect_contains "$err" "two different runs" "--ref with --plan"
}

test_a_project_config_list_does_not_list_stops_the_check() {
  case_dir project_not_listed
  config_json "$answers/config.json" "ok=acme/app|$app"
  python3 - "$answers/config.json" <<'PY'
import json, sys
path = sys.argv[1]
doc = json.load(open(path))
doc["installations"][0]["name"] = "other"
json.dump(doc, open(path, "w"))
PY
  run910
  expect_status "$status" 2 "a project config list does not list"
  expect_contains "$err" "lists no installation named fugaro" "a project config list does not list"
  expect_contains "$err" "other" "the refusal names what it does list"
}

test_a_repository_without_a_checkout_is_unverified() {
  case_dir no_checkout
  # The project enrolls acme/gone and this machine has no checkout of it:
  # `config list` cannot know it at all, so the project config file is where the
  # check learns it is missing rather than measured.
  printf 'project: fugaro\nrepos:\n  acme/app:\n    base_branch: main\n  acme/svc:\n    base_branch: main\n  acme/gone:\n    base_branch: main\n' \
    >"$confighome/fugaro/projects/fugaro.yaml"
  run910
  expect_status "$status" 3 "a repository without a checkout"
  expect_row "$out" premise UNVERIFIED "a repository without a checkout"
  expect_contains "$out" "acme/gone" "the row names it"
  expect_contains "$out" "has no checkout of it" "the row says why"
  expect_row "$out" raw PASS "the repositories it could read are still measured"
}

test_a_checkout_that_is_not_usable_is_a_skip() {
  case_dir unusable_checkout
  config_json "$answers/config.json" "ok=acme/app|$app" "invalid=acme/svc|$svc|missing"
  run910
  expect_status "$status" 3 "a checkout that is not usable"
  expect_row "$out" premise UNVERIFIED "a checkout that is not usable"
  expect_contains "$out" "acme/svc" "the row names it"
  expect_contains "$out" "missing" "the row carries the reason code"
  expect_contains "$(snap)" "repo \"acme/app\"" "the usable repository is still recorded"
}

test_a_settings_read_that_refuses_leaves_the_row_unverified() {
  case_dir settings_refused
  printf 1 >"$answers/settings-status"
  printf 'fugaro settings --repo acme/svc: the project layer was not published\n' >"$answers/settings.err"
  run910
  expect_status "$status" 3 "a settings read that refuses"
  expect_row "$out" resolved UNVERIFIED "a settings read that refuses"
  expect_contains "$out" "the project layer was not published" "the row carries the command's own message"
  expect_row "$out" raw PASS "a settings read that refuses"
  expect_contains "$(snap)" 'raw "acme/app" "agent.model"' "the raw record is complete either way"
}

test_a_missing_ref_leaves_the_raw_row_unverified() {
  case_dir missing_ref
  git -C "$svc" update-ref -d refs/remotes/origin/main
  run910
  expect_status "$status" 3 "a ref that is not there"
  expect_row "$out" raw UNVERIFIED "a ref that is not there"
  expect_contains "$out" "git show origin/main:fugaro.yaml exited" "the row says what failed"
  expect_contains "$out" "fetch origin" "the row names the fix"
  expect_row "$out" resolved PASS "a ref that is not there: the resolved read still answers"
}

test_the_records_are_written_even_where_a_read_failed() {
  case_dir partial
  config_json "$answers/config.json" "ok=acme/app|$app" "invalid=acme/svc|$svc|missing"
  run910
  local s
  s=$(snap)
  expect_contains "$s" 'raw "acme/app" "ab.rate"' "the repository that was read is recorded"
  expect_not_contains "$s" 'raw "acme/svc"' "the one that was skipped is not"
  expect_contains "$s" "repo \"acme/app\"" "the recorded repository names its ref"
}

test_the_plan_step_captures_the_updates_lines() {
  case_dir plan_step
  run910
  expect_status "$status" 3 "the first run"
  run910_raw fugaro --plan "$snapfile"
  expect_status "$status" 0 "the plan step"
  expect_row "$out" plan PASS "the plan step"
  expect_row "$out" writes PASS "the plan step"
  expect_not_contains "$out" "|FAIL|" "the plan step"
  local s
  s=$(snap)
  expect_contains "$s" 'plan "fugaro update --plan-only: acme/app: coder' "the update's own line is in the snapshot"
  expect_contains "$s" 'raw "acme/app" "agent.model"' "every earlier record was kept"
  [ "$(grep -c '^plan ' <<<"$s")" = 1 ] || fail "the plan step: expected one plan line, saw $(grep -c '^plan ' <<<"$s")"
  # Running it again replaces the lines rather than stacking them.
  printf 'fugaro update --plan-only: acme/svc: rigor.default standard -> standard (the project\x27s)\n' >"$answers/plan.out"
  run910_raw fugaro --plan "$snapfile"
  expect_status "$status" 0 "the plan step again"
  s=$(snap)
  [ "$(grep -c '^plan ' <<<"$s")" = 1 ] || fail "the plan step again: expected one plan line, saw $(grep -c '^plan ' <<<"$s")"
  expect_contains "$s" 'plan "fugaro update --plan-only: acme/svc' "the new line replaced the old one"
  expect_contains "$s" 'raw "acme/app" "agent.model"' "the records are still there"
}

test_a_plan_that_refuses_leaves_the_row_unverified() {
  case_dir plan_refused
  run910
  printf 1 >"$answers/plan-status"
  printf 'fugaro update: terraform apply failed\n' >"$answers/plan.err"
  run910_raw fugaro --plan "$snapfile"
  expect_status "$status" 3 "a plan that refuses"
  expect_row "$out" plan UNVERIFIED "a plan that refuses"
  expect_contains "$out" "the update's own naming" "the row says what is lost"
}

test_the_snapshot_run_is_not_allowed_to_drop_captured_lines() {
  case_dir no_clobber
  run910
  run910_raw fugaro --plan "$snapfile"
  expect_status "$status" 0 "the plan step"
  run910
  expect_status "$status" 2 "a second snapshot run over captured lines"
  expect_contains "$err" "already carries the update's lines" "the refusal names what it protects"
  expect_contains "$(snap)" 'plan "fugaro update' "the captured lines are untouched"
}

test_a_file_that_is_not_a_snapshot_is_not_written_over() {
  case_dir not_a_snapshot
  snapfile=$work/somebody-elses-file
  printf 'not a snapshot\n' >"$snapfile"
  run910
  expect_status "$status" 2 "a file that is not a snapshot"
  expect_contains "$err" "is not a Check 910 snapshot" "the refusal says what it found"
  [ "$(cat "$snapfile")" = "not a snapshot" ] || fail "a file that is not a snapshot: it was overwritten"
}

test_the_ref_can_be_named() {
  case_dir named_ref
  run910 --ref "origin/main"
  expect_status "$status" 3 "a named ref"
  expect_contains "$(snap)" "repo \"acme/app\" \"origin/main\"" "a named ref"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK910_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK910_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_the_raw_values_come_from_the_ref_and_not_the_working_tree
t test_the_snapshot_run_writes_nothing_but_the_snapshot
t test_help
t test_a_dry_run_calls_nothing
t test_no_project_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_an_unknown_argument_is_refused
t test_a_project_config_list_does_not_list_stops_the_check
t test_a_repository_without_a_checkout_is_unverified
t test_a_checkout_that_is_not_usable_is_a_skip
t test_a_settings_read_that_refuses_leaves_the_row_unverified
t test_a_missing_ref_leaves_the_raw_row_unverified
t test_the_records_are_written_even_where_a_read_failed
t test_the_plan_step_captures_the_updates_lines
t test_a_plan_that_refuses_leaves_the_row_unverified
t test_the_snapshot_run_is_not_allowed_to_drop_captured_lines
t test_a_file_that_is_not_a_snapshot_is_not_written_over
t test_the_ref_can_be_named

if [ "$failures" -ne 0 ]; then
  echo "check-910-lift-snapshot_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-910-lift-snapshot_test.sh: ok"
