#!/usr/bin/env bash
# Tests for scripts/live-checks/check-911-lift-compare.sh. NOTHING here is a
# cloud, a project, an installation or a repository that matters: every case
# builds REAL local git checkouts with a bare origin of their own in a temporary
# directory and drives BOTH checks against a fake `fugaro` on PATH whose answers
# the case owns. The fake answers in one of two phases — `before`, the pre-L8
# binary Check 910 took the snapshot with, and `after`, the release-A binary this
# check reads the values again with — so a case can bend exactly one reading
# between the two and see whether the comparison catches it.
#
# The readings are the point. Check 911 is the only thing that decides whether
# release A's update left an installation as it found it, so the cases that
# matter are the ones where a value moves: a value that moved with a line of the
# update's plan naming it, one that moved with nothing naming it, a line that
# looks like it names one and does not (another repository, another recipe), a
# raw file rewritten at the ref, a recipe whose text changed, a snapshot taken
# by the same binary twice, an update whose lines were never captured, and a
# repository the second read no longer lists.
#
# Usage: bash scripts/live-checks/check-911-lift-compare_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-911-lift-compare.sh
nine_ten=$here/check-910-lift-snapshot.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK911_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK911_TEST_HEAD:-1600}")" ;;
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
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK911_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fake

fakebin=$work/bin
mkdir -p "$fakebin"

# The fake answers in the phase a case has written into $dir/phase: `before` is
# the pre-L8 binary the snapshot was taken with, `after` the release-A one. Its
# stdin is read and logged, because no step of either check may wait on one.
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
printf 'fugaro %s (in %s, stdin=%s)\n' "$*" "$PWD" "$kind" >>"${FAKE911_LOG:?}"
dir=${FAKE911_DIR:?}
phase=$(cat "$dir/phase" 2>/dev/null || printf before)
name=${PWD##*/}
case "${1:-}" in
--version)
  if [ -f "$dir/version-$phase" ]; then
    cat "$dir/version-$phase"
  elif [ "$phase" = after ]; then
    printf 'fugaro version 0.1.0-release-a\n'
  else
    printf 'fugaro version 0.0.0-pre-l8\n'
  fi
  ;;
config)
  if [ -f "$dir/config-status-$phase" ]; then
    cat "$dir/config-$phase.err" >&2 2>/dev/null || true
    exit "$(cat "$dir/config-status-$phase")"
  fi
  if [ -f "$dir/config-$phase.json" ]; then cat "$dir/config-$phase.json"; else cat "$dir/config.json"; fi
  ;;
settings)
  repo=
  prev=
  for a in "$@"; do
    if [ "$prev" = --repo ]; then repo=$a; fi
    prev=$a
  done
  file="$dir/settings-$phase-$(printf '%s' "$repo" | tr '/' '-').json"
  [ -f "$file" ] || { printf 'fake fugaro: no settings answer for %s in the %s phase\n' "$repo" "$phase" >&2; exit 64; }
  cat "$file"
  ;;
recipes)
  case "${2:-}" in
  ls)
    file="$dir/recipes-$phase-$name.json"
    [ -f "$file" ] || { printf 'fake fugaro: no recipes answer in %s in the %s phase\n' "$name" "$phase" >&2; exit 64; }
    cat "$file"
    ;;
  show)
    file="$dir/show-$phase-$name-${3:-}.json"
    [ -f "$file" ] || { printf 'fake fugaro: no show answer for %s in the %s phase\n' "${3:-<none>}" "$phase" >&2; exit 64; }
    cat "$file"
    ;;
  *)
    printf 'fake fugaro: unexpected recipes verb %s\n' "${2:-<none>}" >&2
    exit 64
    ;;
  esac
  ;;
update)
  printf '%s\n' "$*" | grep -q -- '--plan-only' || {
    printf 'fake fugaro: refused: neither check runs the update without --plan-only\n' >&2
    exit 64
  }
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
# pushed, so a case can see whether a read came from the ref or from the working
# tree.
make_sandbox() { # make_sandbox <checkout> <origin> <model>
  mkdir -p "$1" "$2"
  git -C "$2" init -q --bare
  git -C "$1" init -q
  git -C "$1" symbolic-ref HEAD refs/heads/main
  git -C "$1" config user.email check911@example.invalid
  git -C "$1" config user.name check911
  mkdir -p "$1/.fugaro/recipes"
  cat >"$1/fugaro.yaml" <<YAML
version: 1
project: fugaro

agent:
  model: $3
  models:
    coder: deepseek/deepseek-v4.1-flash
  crafts:
    coder:
      model: deepseek/deepseek-v4.1-flash
  levels:
    l2:
      model: claude-opus-5-5
  max_budget_usd: 500  # kept, not lifted

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
    repo, path = rest.split("|")
    checkouts.append({"repository": "github/" + repo, "path": path, "base_branch": "main",
                      "enrolled": kind != "notenrolled", "valid": kind != "invalid"})
doc = {"installations": [{"name": "fugaro", "gcp_project": "fugaro-dev", "base_branch": "main",
                          "checkouts": checkouts}], "warnings": []}
json.dump(doc, open(out, "w"))
PY
}

# settings_json is what `fugaro settings --repo <owner/name> --json` answers: one
# row per key and scope, with the value a run gets and the layer that decided it.
# The migration moves the deciding layer from the repository to the project and
# leaves the value alone, so `before` says `repo` and `after` says `project`.
settings_json() { # settings_json <out> <owner/name> <before|after>
  python3 - "$@" <<'PY'
import json, sys
out, repo, phase = sys.argv[1], sys.argv[2], sys.argv[3]
vals = {
    "agent.model": "claude-sonnet-5",
    "agent.models.coder": "deepseek/deepseek-v4.1-flash",
    "agent.crafts.coder.model": "deepseek/deepseek-v4.1-flash",
    "agent.levels.l2.model": "claude-opus-5-5",
    "ab.rate": "0",
    "rigor.default": "standard",
    "model_grades.strong.model": "claude-opus-5-5",
    "agent.max_budget_usd": "500",
}
wins = "project" if phase == "after" else "repo"
rows = []
for scope in ("project", "repo:" + repo):
    for k, v in sorted(vals.items()):
        rows.append({"key": k, "scope": scope, "wins": wins, "state": "ok",
                     "default": {"unset": "none"}, "value": v})
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

show_json() { # show_json <out> [sha]
  python3 - "$@" <<'PY'
import json, sys
sha = sys.argv[2] if len(sys.argv) > 2 else "1111111111111111111111111111111111111111111111111111111111111111"
json.dump({"name": "default", "source": "repo", "where": ".fugaro/recipes/default.yaml",
           "sha256": "sha256:" + sha, "yaml": "name: default\n"}, open(sys.argv[1], "w"))
PY
}

# settings_set rewrites one key's value in a settings answer, on every scope, so
# a case can say what a run resolves after the update.
settings_set() { # settings_set <settings json> <key> <value>
  python3 - "$@" <<'PY'
import json, sys
path, key, value = sys.argv[1], sys.argv[2], sys.argv[3]
doc = json.load(open(path))
for row in doc["settings"]:
    if row["key"] == key:
        row["value"] = value
json.dump(doc, open(path, "w"))
PY
}

# case_dir builds one case's own world: two real checkouts with their origins,
# the installation's own project config, and every answer the fake gives in both
# phases. A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  confighome=$root/config
  mkdir -p "$answers" "$confighome/fugaro/projects"
  CALLS="$answers/calls"
  : >"$CALLS"
  app=$root/app
  svc=$root/svc
  make_sandbox "$app" "$root/app-origin.git" claude-sonnet-5
  make_sandbox "$svc" "$root/svc-origin.git" claude-sonnet-5
  printf 'project: fugaro\nrepos:\n  acme/app:\n    base_branch: main\n  acme/svc:\n    base_branch: main\n' \
    >"$confighome/fugaro/projects/fugaro.yaml"
  config_json "$answers/config.json" "ok=acme/app|$app" "ok=acme/svc|$svc"
  local phase repo
  for phase in before after; do
    for repo in acme/app acme/svc; do
      settings_json "$answers/settings-$phase-$(printf '%s' "$repo" | tr '/' '-').json" "$repo" "$phase"
    done
    recipes_json "$answers/recipes-$phase-app.json"
    recipes_json "$answers/recipes-$phase-svc.json"
    show_json "$answers/show-$phase-app-default.json"
    show_json "$answers/show-$phase-svc-default.json"
  done
  printf 'the project layer is published; no repository\x27s lifted value changes\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  snapfile=$root/snapshot
}

# ------------------------------------------------------------------ the runs

run_both() { # run_both <script> <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE911_LOG="${CALLS:-/dev/null}" FAKE911_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" \
    bash "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
}

run910_raw() { # run910_raw <args...>
  run_both "$nine_ten" "$@"
}

run911_raw() { # run911_raw <args...>
  run_both "$script" "$@"
}

run911() { # run911 [extra args...]
  run911_raw fugaro "${snapfile:-$work/snapshot}" "$@"
}

# before_snapshot is the three owner steps that come first: Check 910 with the
# pre-L8 binary, and its --plan step with the release-A one. The fake then
# answers `after` for everything this check reads.
before_snapshot() {
  printf 'before' >"$answers/phase"
  run910_raw fugaro --out "$snapfile"
  expect_status "$status" 3 "the snapshot step"
  run910_raw fugaro --plan "$snapfile"
  expect_status "$status" 0 "the plan step"
  printf 'after' >"$answers/phase"
  : >"$CALLS"
}

snap() { cat "${snapfile:-$work/snapshot}" 2>/dev/null || true; }

# ------------------------------------------------------------------- the cases

test_the_values_read_the_same_after_the_update() {
  case_dir happy_path
  before_snapshot
  run911
  expect_status "$status" 0 "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" binary PASS "the happy path: the second read used another binary"
  expect_row "$out" plan PASS "the happy path"
  expect_row "$out" raw PASS "the happy path"
  expect_row "$out" resolved PASS "the happy path"
  expect_row "$out" recipes PASS "the happy path"
  expect_row "$out" writes PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
  expect_contains "$out" "the release-A binary (fugaro version 0.0.0-pre-l8 -> fugaro version 0.1.0-release-a)" "the row names both binaries"
  # The layer a key is decided by moved from the repository to the project: that
  # is the migration, and the values it must not move read the same.
  expect_contains "$(snap)" 'resolved "acme/app" "repo:acme/app" "agent.model" "claude-sonnet-5" "repo" "ok"' "the snapshot is the pre-update read"
}

test_the_snapshot_is_not_written_to() {
  case_dir nothing_written
  before_snapshot
  local before after
  before=$(sha256sum "$snapfile" | cut -d' ' -f1)
  run911
  expect_status "$status" 0 "nothing is written"
  after=$(sha256sum "$snapfile" | cut -d' ' -f1)
  [ "$before" = "$after" ] || fail "nothing is written: the snapshot changed"
  expect_contains "$out" "byte-identical" "the writes row says the snapshot was not touched"
  # Nothing is written outside the scratch either: the work directory holds the
  # snapshot and the fixture's own checkouts, and this check adds no file to it.
  local strays
  strays=$(find "$work/nothing_written" -maxdepth 1 -newer "$snapfile" -not -name err -not -name answers -not -name config 2>/dev/null | head -3)
  [ -z "$strays" ] || fail "nothing is written: it left $strays"
}

test_a_value_that_moved_with_the_update_naming_it_passes() {
  case_dir named_change
  before_snapshot
  # The project had no value, so the repository's model became the project's and
  # the repository's own file was left alone: the raw read is identical, and the
  # update's line names the change the resolved read shows. The row for the
  # project's own value and the repository's now differ, which is exactly the
  # change a line must name.
  settings_set "$answers/settings-after-acme-app.json" "rigor.default" "high"
  printf 'acme/app: rigor.default standard -> high (the project\x27s)\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  expect_status "$status" 0 "the plan step again"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 0 "a change the update named"
  expect_row "$out" resolved PASS "a change the update named"
  expect_row "$out" plan PASS "a change the update named"
  expect_contains "$out" "the update's plan names each of the 1 that moved" "the row counts what the plan named"
  expect_not_contains "$out" "|FAIL|" "a change the update named"
}

test_a_value_that_moved_with_nothing_naming_it_fails() {
  case_dir unnamed_change
  before_snapshot
  settings_set "$answers/settings-after-acme-app.json" "agent.model" "claude-opus-5-5"
  run911
  expect_status "$status" 1 "a change nobody named"
  expect_row "$out" resolved FAIL "a change nobody named"
  expect_contains "$out" "acme/app agent.model (claude-sonnet-5 -> claude-opus-5-5)" "the row names the value that moved"
  expect_contains "$out" "NOTHING naming it" "the row says what is wrong"
  expect_row "$out" raw PASS "a change nobody named: the raw file did not move"
}

test_a_value_moved_by_a_line_for_another_repository_fails() {
  case_dir named_for_another_repo
  before_snapshot
  settings_set "$answers/settings-after-acme-app.json" "agent.model" "claude-opus-5-5"
  # The update's line names acme/svc, not acme/app: a reader of the plan cannot
  # tell that acme/app's model was meant, so it is not named.
  printf 'acme/svc: model claude-sonnet-5 -> claude-opus-5-5 (the project\x27s)\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line that names another repository"
  expect_row "$out" resolved FAIL "a line that names another repository"
}

test_a_line_for_a_repository_whose_name_starts_with_this_one_does_not_name_it() {
  case_dir prefix_sharing_pair
  before_snapshot
  # Two ways a line can look like it names a change and does not, both of them
  # the same trap: a repository whose name starts with this one's, and a word a
  # short token sits inside. acme/app-console is not acme/app, and `its stable
  # table` carries neither the key nor a value of the ab.rate change.
  settings_set "$answers/settings-after-acme-app.json" "ab.rate" "0.25"
  settings_set "$answers/settings-after-acme-app.json" "rigor.default" "high"
  printf 'acme/app-console: ab.rate 0 -> 0.25 (the project\x27s)\nacme/app-console: rigor.default standard -> high (the project\x27s)\nacme/app: its stable table is unchanged\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line for a repository whose name starts with this one"
  expect_row "$out" resolved FAIL "a line for a repository whose name starts with this one"
  expect_contains "$out" "acme/app ab.rate (0 -> 0.25)" "the row names the A/B rate that moved"
  expect_contains "$out" "acme/app rigor.default (standard -> high)" "the row names the rigor level that moved"
  expect_contains "$out" "NOTHING naming it" "the row says the lines did not name them"
  expect_row "$out" raw PASS "a line for a repository whose name starts with this one: the raw file did not move"
}

test_a_raw_file_rewritten_at_the_ref_is_compared_too() {
  case_dir raw_moved
  before_snapshot
  # The migration is not supposed to touch the repository's own file. Here it
  # did: the default branch carries another model, so the raw read moved, and
  # nothing in the update's plan names it.
  perl -pi -e "s/^  model: .*/  model: claude-opus-5-5/" "$app/fugaro.yaml"
  git -C "$app" commit -qam "the migration wrote the repository file"
  git -C "$app" push -q origin main
  git -C "$app" fetch -q origin
  run911
  expect_status "$status" 1 "a raw file rewritten at the ref"
  expect_row "$out" raw FAIL "a raw file rewritten at the ref"
  expect_contains "$out" 'acme/app agent.model' "the row names the raw key that moved"
  expect_contains "$out" "CHECK 911 RESULT: FAIL (exit 1)" "the verdict is the exit status"
}

test_a_recipe_whose_text_changed_is_compared_too() {
  case_dir recipe_moved
  before_snapshot
  show_json "$answers/show-after-app-default.json" "2222222222222222222222222222222222222222222222222222222222222222"
  run911
  expect_status "$status" 1 "a recipe whose text changed"
  expect_row "$out" recipes FAIL "a recipe whose text changed"
  expect_contains "$out" "acme/app default" "the row names the recipe"
}

test_a_recipe_the_update_renamed_is_named() {
  case_dir recipe_named
  before_snapshot
  show_json "$answers/show-after-app-default.json" "2222222222222222222222222222222222222222222222222222222222222222"
  # The line internal/lift's recipe planner prints, verbatim: the file the
  # recipe is read at, then the name it is published under.
  printf 'acme/app: .fugaro/recipes/default.yaml is published as the project recipe default\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 0 "a recipe the update named"
  expect_row "$out" recipes PASS "a recipe the update named"
  expect_contains "$out" "the update's plan names each of the 1 that moved" "the row counts what the plan named"
}

test_a_level_line_does_not_name_the_model() {
  case_dir level_line_model
  before_snapshot
  settings_set "$answers/settings-after-acme-app.json" "agent.model" "claude-opus-5-5"
  # A line for one port says nothing about another: `level l3` is not `model`,
  # and the value it carries is no way in — a value is what confirms a line the
  # label has matched, never what matches it.
  printf 'acme/app: level l3 none -> claude-opus-5-5 (the project\x27s)\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line for the level"
  expect_row "$out" resolved FAIL "a line for the level"
  expect_contains "$out" "acme/app agent.model (claude-sonnet-5 -> claude-opus-5-5)" \
    "the row names the value that moved"
  expect_contains "$out" "NOTHING naming it" "the row says the line did not name it"
}

test_a_craft_line_does_not_name_the_role_model() {
  case_dir craft_line_role
  before_snapshot
  settings_set "$answers/settings-after-acme-app.json" "agent.models.coder" "claude-haiku-4-5"
  # `craft coder` carries the word `coder` and is no line for the role's own
  # model: the label is what the update writes after the repository, whole.
  printf 'acme/app: craft coder claude-x -> claude-y (the project\x27s)\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line for the craft"
  expect_row "$out" resolved FAIL "a line for the craft"
  expect_contains "$out" "acme/app agent.models.coder (deepseek/deepseek-v4.1-flash -> claude-haiku-4-5)" \
    "the row names the value that moved"
  expect_contains "$out" "NOTHING naming it" "the row says the line did not name it"
}

test_a_line_for_the_same_port_with_other_values_does_not_name_it() {
  case_dir same_port_other_values
  before_snapshot
  settings_set "$answers/settings-after-acme-app.json" "agent.model" "claude-opus-5-5"
  # The label is this port's, and the values are another change's: the line
  # names the change it states, which is not this one.
  printf 'acme/app: model claude-a -> claude-b (the project\x27s)\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line for the port with other values"
  expect_row "$out" resolved FAIL "a line for the port with other values"
  expect_contains "$out" "acme/app agent.model (claude-sonnet-5 -> claude-opus-5-5)" \
    "the row names the value that moved"
  expect_contains "$out" "NOTHING naming it" "the row says the line did not name it"
}

test_a_line_about_another_grade_does_not_name_this_one() {
  case_dir other_grade
  before_snapshot
  settings_set "$answers/settings-after-acme-app.json" "model_grades.strong.model" "claude-haiku-4-5"
  # No planner prints a line for L13's grades yet, so nothing names a grade that
  # moved: a grade-shaped line is no line the update writes.
  printf 'acme/app: model grade strongest claude-x -> claude-y (the project\x27s)\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line about another grade"
  expect_row "$out" resolved FAIL "a line about another grade"
  expect_contains "$out" "acme/app model_grades.strong.model (claude-opus-5-5 -> claude-haiku-4-5)" \
    "the row names the grade that moved"
  expect_contains "$out" "NOTHING naming it" "the row says the line did not name it"
}

test_a_line_about_another_recipe_does_not_name_this_one() {
  case_dir recipe_other
  before_snapshot
  show_json "$answers/show-after-app-default.json" "2222222222222222222222222222222222222222222222222222222222222222"
  # The word a line about any recipe carries names nothing about this one, the
  # same trap as the repository whose name starts with this one's: the plan
  # line is the repository's and is about a recipe, but not this recipe.
  printf 'acme/app: .fugaro/recipes/other.yaml is published as the project recipe other\n' \
    >"$answers/plan.out"
  printf 'before' >"$answers/phase"
  run910_raw fugaro --plan "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 1 "a line about another recipe"
  expect_row "$out" recipes FAIL "a line about another recipe"
  expect_contains "$out" "acme/app default" "the row names the recipe that moved"
  expect_contains "$out" "NOTHING naming it" "the row says the line did not name it"
}

test_a_repository_the_second_read_no_longer_lists_is_unverified() {
  case_dir repo_gone
  before_snapshot
  config_json "$answers/config.json" "ok=acme/app|$app" "notenrolled=acme/svc|$svc"
  run911
  expect_status "$status" 3 "a repository the second read no longer lists"
  expect_row "$out" premise UNVERIFIED "a repository the second read no longer lists"
  expect_contains "$out" "acme/svc" "the row names the repository"
}

test_the_same_binary_twice_is_unverified() {
  case_dir same_binary
  before_snapshot
  # The owner ran this check without upgrading: the read is not the release-A
  # binary's, so the comparison shows nothing about the migration.
  printf 'fugaro version 0.0.0-pre-l8\n' >"$answers/version-after"
  run911
  expect_status "$status" 3 "the same binary twice"
  expect_row "$out" binary UNVERIFIED "the same binary twice"
  expect_contains "$out" "brew upgrade" "the row names the fix"
  expect_row "$out" resolved PASS "the same binary twice: the values still compared"
}

test_an_update_whose_lines_were_never_captured_is_unverified() {
  case_dir no_plan_lines
  printf 'before' >"$answers/phase"
  run910_raw fugaro --out "$snapfile"
  expect_status "$status" 3 "the snapshot step"
  printf 'after' >"$answers/phase"
  settings_set "$answers/settings-after-acme-app.json" "agent.model" "claude-opus-5-5"
  run911
  expect_status "$status" 1 "an update whose lines were never captured"
  expect_row "$out" plan UNVERIFIED "an update whose lines were never captured"
  expect_contains "$out" "--plan step belongs BEFORE the update" "the row says what is missing"
  expect_row "$out" resolved FAIL "a value moved with no plan to name it"
}

test_nothing_moved_and_no_plan_lines_is_still_a_pass() {
  case_dir no_plan_no_change
  printf 'before' >"$answers/phase"
  run910_raw fugaro --out "$snapfile"
  printf 'after' >"$answers/phase"
  run911
  expect_status "$status" 0 "nothing moved and no plan lines"
  expect_row "$out" plan PASS "nothing moved and no plan lines"
  expect_contains "$out" "nothing needed naming" "the row says why the plan does not matter here"
  expect_not_contains "$out" "|FAIL|" "nothing moved and no plan lines"
}

test_the_refs_of_the_snapshot_are_read_again_not_the_branch_now() {
  case_dir refs_from_snapshot
  before_snapshot
  # The installation's base branch is renamed after the snapshot was taken. The
  # comparison must read the ref the snapshot recorded: deriving the ref again
  # would read origin/release, which is not the branch the snapshot photographed.
  python3 - "$answers/config.json" <<'PY'
import json, sys
path = sys.argv[1]
doc = json.load(open(path))
doc["installations"][0]["base_branch"] = "release"
for row in doc["installations"][0]["checkouts"]:
    row["base_branch"] = "release"
json.dump(doc, open(path, "w"))
PY
  run911
  expect_status "$status" 0 "the refs of the snapshot"
  expect_row "$out" raw PASS "the refs of the snapshot"
  expect_contains "$(snap)" '"origin/main"' "the snapshot still names the ref it read"
}

test_the_second_read_that_does_not_answer_is_unverified() {
  case_dir after_refused
  before_snapshot
  printf 1 >"$answers/config-status-after"
  printf 'fugaro config list: the project is not configured\n' >"$answers/config-after.err"
  run911
  expect_status "$status" 3 "a second read that does not answer"
  expect_row "$out" after UNVERIFIED "a second read that does not answer"
  expect_contains "$out" "the project is not configured" "the row carries the read's own message"
  expect_not_contains "$out" "|FAIL|" "a second read that does not answer"
}

test_a_file_that_is_not_a_snapshot_is_refused() {
  case_dir not_a_snapshot
  snapfile=$work/somebody-elses-file
  printf 'not a snapshot\n' >"$snapfile"
  CALLS=
  run911_raw fugaro "$snapfile"
  expect_status "$status" 2 "a file that is not a snapshot"
  expect_contains "$err" "not a Check 910 snapshot" "the refusal says what it found"
  [ "$(cat "$snapfile")" = "not a snapshot" ] || fail "a file that is not a snapshot: it was written to"
}

test_a_snapshot_of_another_project_is_refused() {
  case_dir another_project
  before_snapshot
  CALLS=
  run911_raw other "$snapfile"
  expect_status "$status" 2 "a snapshot of another project"
  expect_contains "$err" "is the snapshot of project fugaro, not of other" "the refusal names both projects"
}

test_a_snapshot_that_is_not_there_is_refused() {
  CALLS=
  run911_raw fugaro "$work/no-such-snapshot"
  expect_status "$status" 2 "a snapshot that is not there"
  expect_contains "$err" "is not there" "a snapshot that is not there"
}

test_a_missing_snapshot_argument_is_refused() {
  CALLS=
  run911_raw fugaro
  expect_status "$status" 2 "no snapshot argument"
  expect_contains "$err" "no <snapshot> was given" "no snapshot argument"
}

test_no_project_is_refused() {
  CALLS=
  run911_raw
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no <project> was given" "no project"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run911_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE911_LOG="$CALLS" FAKE911_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash "$script" fugaro "$work/nothing" 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE911_LOG="$CALLS" FAKE911_DIR="$answers" \
    XDG_CONFIG_HOME="$confighome" bash -x "$script" fugaro "$work/nothing" 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_help() {
  CALLS=
  run911_raw --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 911 — compare the values" "help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  before_snapshot
  run911_raw fugaro "$snapfile" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 911 plan" "a dry run"
  expect_not_contains "$out" "CHECK 911 RESULT" "a dry run prints no block"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_no_call_leaves_its_stdin_open_and_no_argument_carries_a_secret() {
  case_dir no_waiting
  before_snapshot
  run911
  expect_status "$status" 0 "no call waits on a stdin"
  local calls
  calls=$(cat "$CALLS")
  expect_contains "$calls" "stdin=closed" "every call has its stdin closed"
  expect_not_contains "$calls" "stdin=open" "no call waits on a stdin"
  expect_not_contains "$calls" "stdin=line" "no call is fed a line"
  expect_not_contains "$calls" "token" "no argument carries a token"
  expect_not_contains "$calls" "fugaro update " "the comparison never runs the update"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK911_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK911_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_values_read_the_same_after_the_update
t test_the_snapshot_is_not_written_to
t test_a_value_that_moved_with_the_update_naming_it_passes
t test_a_value_that_moved_with_nothing_naming_it_fails
t test_a_value_moved_by_a_line_for_another_repository_fails
t test_a_line_for_a_repository_whose_name_starts_with_this_one_does_not_name_it
t test_a_raw_file_rewritten_at_the_ref_is_compared_too
t test_a_recipe_whose_text_changed_is_compared_too
t test_a_recipe_the_update_renamed_is_named
t test_a_level_line_does_not_name_the_model
t test_a_craft_line_does_not_name_the_role_model
t test_a_line_for_the_same_port_with_other_values_does_not_name_it
t test_a_line_about_another_grade_does_not_name_this_one
t test_a_line_about_another_recipe_does_not_name_this_one
t test_a_repository_the_second_read_no_longer_lists_is_unverified
t test_the_same_binary_twice_is_unverified
t test_an_update_whose_lines_were_never_captured_is_unverified
t test_nothing_moved_and_no_plan_lines_is_still_a_pass
t test_the_refs_of_the_snapshot_are_read_again_not_the_branch_now
t test_the_second_read_that_does_not_answer_is_unverified
t test_a_file_that_is_not_a_snapshot_is_refused
t test_a_snapshot_of_another_project_is_refused
t test_a_snapshot_that_is_not_there_is_refused
t test_a_missing_snapshot_argument_is_refused
t test_no_project_is_refused
t test_an_unknown_argument_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_help
t test_a_dry_run_calls_nothing
t test_no_call_leaves_its_stdin_open_and_no_argument_carries_a_secret

if [ "$failures" -ne 0 ]; then
  echo "check-911-lift-compare_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-911-lift-compare_test.sh: ok"
