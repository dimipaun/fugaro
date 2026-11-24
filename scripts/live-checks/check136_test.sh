#!/usr/bin/env bash
# Tests for scripts/live-checks/check136.sh, with fake `fugaro`, `git` and `gh`
# on PATH: NOTHING here reaches a repository, GitHub, a cloud or the network.
#
# The fakes are STRICT: the fake `fugaro` validates every command and flag
# against the real CLI's own surface (lib/strict.sh over lib/fugaro-surface.tsv,
# generated from cli.NewRootCmd by scripts/live_check_surface_test.go), and the
# fake `gh` refuses a subcommand or flag its allow-list does not name
# (lib/allow/check136.gh). A check that called `fugaro runs ls --since 8d` with
# a flag that moved, or `gh pr files`, fails HERE rather than on your terminal.
#
# Usage: bash scripts/live-checks/check136_test.sh
#        CHECK_TEST_ONLY=test_the_whole_check_passes bash scripts/live-checks/check136_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check136.sh
export LIVE_CHECK_LIB=$here/lib
# shellcheck source=lib/strict.sh
. "$LIVE_CHECK_LIB/strict.sh"

work=$(mktemp -d)
state=$work/state
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK136_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 800)" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; block: $(printf '%s' "${out:-}" | head -c 500)"
}

# --------------------------------------------------------------------- fakes
mkdir -p "$work/bin" "$state/files" "$work/home" "$work/tmp"
chmod 700 "$work/tmp"

cat >"$work/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check136.sh's test only. It
# validates its argv against the REAL CLI's surface first, then answers the read
# out of the case's own state directory.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s\n' "$*" >>"${FAKE136_STATE:?}/calls"
case "${1:-} ${2:-}" in
"doctor --json") cat "$FAKE136_STATE/doctor.json" ;;
"recipes show") cat "$FAKE136_STATE/recipe.txt" ;;
"run --recipe") cat "$FAKE136_STATE/launch.json" ;;
"runs ls") cat "$FAKE136_STATE/ls.json" ;;
"runs digest")
  case " $* " in
  *" --due "*) cat "$FAKE136_STATE/due.json" ;;
  *" --stored "*) cat "$FAKE136_STATE/stored.md" ;;
  *) cat "$FAKE136_STATE/live.json" ;;
  esac
  ;;
"diagnose "*) cat "$FAKE136_STATE/diagnose.json" ;;
*)
  echo "fugaro: unexpected read: $*" >&2
  exit 1
  ;;
esac
FUGARO_EOF
chmod +x "$work/bin/fugaro"

cat >"$work/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check136.sh's test only.
set -euo pipefail
printf 'git %s\n' "$*" >>"${FAKE136_STATE:?}/calls"
case "${1:-}" in
remote) cat "$FAKE136_STATE/origin" ;;
archive) exit 0 ;;
fetch) exit 0 ;;
ls-tree) cat "$FAKE136_STATE/tree" ;;
grep)
  case "$*" in
  *-nF*) f=$FAKE136_STATE/task ;;
  *) f=$FAKE136_STATE/address ;;
  esac
  if [ -s "$f" ]; then cat "$f"; exit 0; fi
  exit 1
  ;;
show)
  p=${2#FETCH_HEAD:}
  cat "$FAKE136_STATE/files/$p"
  ;;
*) exit 1 ;;
esac
GIT_EOF
chmod +x "$work/bin/git"

cat >"$work/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check136.sh's test only: it
# refuses an unknown subcommand or flag through the strict allow-list.
set -euo pipefail
# shellcheck source=strict.sh
. "${LIVE_CHECK_LIB:?LIVE_CHECK_LIB must name scripts/live-checks/lib}/strict.sh"
strict_gh_verify "${FAKE136_ALLOW:?}/check136.gh" "$@" || exit $?
printf 'gh %s\n' "$*" >>"${FAKE136_STATE:?}/calls"
case "${1:-} ${2:-}" in
"pr view") cat "$FAKE136_STATE/pr.json" ;;
*) echo "gh: unexpected read: $*" >&2; exit 1 ;;
esac
GH_EOF
chmod +x "$work/bin/gh"

# ------------------------------------------------------------------- fixtures
DAY=2026-11-23
SHORT=abcd
FULL_RUN=20261123-101010-abcd
REPO=acme/sandbox

report_files() { # report_files -> the day files and the index on the branch
  : >"$state/tree"
  mkdir -p "$state/files/docs/fugaro-reviews/2026"
  echo "docs/fugaro-reviews/README.md" >>"$state/tree"
  echo "# the generated index" >"$state/files/docs/fugaro-reviews/README.md"
  local day
  for day in 2026-11-17 2026-11-18 2026-11-19 2026-11-20 2026-11-21 2026-11-22 2026-11-23; do
    echo "docs/fugaro-reviews/2026/$day.md" >>"$state/tree"
    write_day_file "$day" >"$state/files/docs/fugaro-reviews/2026/$day.md"
  done
}

write_day_file() { # write_day_file DAY
  cat <<EOF
# Report $1

| run | status | pr | base | reviews | billed | notional | flags |
| --- | --- | --- | --- | --- | --- | --- | --- |
| \`$SHORT\` | ready | #42 | - | 1 | \$0.500000 | \$0.000000 | - |

## Summary

\`\`\`json
{"period":"$1","start":"$1T00:00:00Z","end":"$1T23:59:59Z","runs":1,"shown":1,"flagged":0,"billed":"\$0.500000","notional":"\$0.000000","review":"\$0.000000","halts":0}
\`\`\`
EOF
}

reset_state() {
  rm -rf "$state"
  mkdir -p "$state/files" "$work/tmp"
  : >"$state/calls"
  : >"$state/address"
  : >"$state/task"
  echo "git@github.com:acme/sandbox.git" >"$state/origin"
  cat >"$state/doctor.json" <<EOF
{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  cat >"$state/recipe.txt" <<EOF
recipe run-digest from catalog (the catalog)
sha256 1111111111111111111111111111111111111111111111111111111111111111

steps:
- digest: {}
EOF
  cat >"$state/launch.json" <<EOF
{"project":"fugaro","run":"$FULL_RUN","repo":"$REPO","run_id":"$FULL_RUN","branch":"fugaro/$FULL_RUN","status":"launched","recipe":{"name":"run-digest","source":"catalog"}}
EOF
  cat >"$state/ls.json" <<EOF
{"project":"fugaro","runs":[{"run_id":"$FULL_RUN","status":"ready","terminal":true,"pr":42,"cost":{"total_usd":0.5}}],"totals":{"runs":1,"total_usd":0.5}}
EOF
  cat >"$state/due.json" <<EOF
{"version":1,"dir":"x","due":[{"dim":"day","key":"2026-11-23"},{"dim":"day","key":"2026-11-22"},{"dim":"day","key":"2026-11-21"},{"dim":"day","key":"2026-11-20"},{"dim":"day","key":"2026-11-19"},{"dim":"day","key":"2026-11-18"},{"dim":"day","key":"2026-11-17"}],"newest":null}
EOF
  mkdir -p "$state/files/docs/fugaro-reviews/2026"
  report_files
  cat >"$state/live.json" <<EOF
{"repo":"$REPO","period":"$DAY","summary":{"period":"$DAY","start":"${DAY}T00:00:00Z","end":"${DAY}T23:59:59Z","runs":1,"shown":1,"flagged":0,"billed":"\$0.500000","notional":"\$0.000000","review":"\$0.000000","halts":0}}
EOF
  cat >"$state/diagnose.json" <<EOF
{"project":"fugaro","row":{"run_id":"$FULL_RUN","status":"ready","cost":{"total_usd":0.5}},"stages":[{"name":"review","calls":3,"agents":1}]}
EOF
  python3 - "$state" "$DAY" <<'PY'
import json,os,sys
state,day=sys.argv[1],sys.argv[2]
files=[{"path":"docs/fugaro-reviews/README.md"}]
for d in ("2026-11-17","2026-11-18","2026-11-19","2026-11-20","2026-11-21","2026-11-22","2026-11-23"):
    files.append({"path":"docs/fugaro-reviews/2026/%s.md"%d})
json.dump({"number":42,"url":"https://github.com/acme/sandbox/pull/42","state":"OPEN","isDraft":False,"files":files},
          open(os.path.join(state,"pr.json"),"w"))
PY
  echo "the stored report" >"$state/stored.md"
}

run_check() { # run_check [args...] -> sets $out $status
  local name=$$
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" HOME="$work/home" TMPDIR="$work/tmp" \
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE136_STATE="$state" FAKE136_ALLOW="$LIVE_CHECK_LIB/allow" \
    CHECK136_AFTER_T33=yes CHECK136_SETTLE_SECONDS=2 CHECK136_POLL_SECONDS=0 \
    ${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/stderr.$name")
  status=$?
  set -e
  err=$(cat "$work/stderr.$name")
}

calls() { cat "$state/calls" 2>/dev/null || true; }

# ---------------------------------------------------------------------- cases
test_the_help_lists_the_one_command() {
  reset_state
  run_check --help
  expect_status "$status" 0 "--help exits 0"
  expect_contains "$out" "bash scripts/live-checks/check136.sh" "--help prints the usage line"
  expect_contains "$out" "CHECK 136 RESULT" "--help's header names the block"
}

test_refuses_a_coding_agents_session() {
  reset_state
  EXTRA_ENV=(CLAUDECODE=1)
  run_check
  unset EXTRA_ENV
  expect_status "$status" 2 "an agent session is refused with a stop"
  expect_contains "$err" "coding agent's session" "the refusal names what it is"
}

test_refuses_belong() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"belong-prod","region":"us-east5"}}
EOF
  run_check
  expect_status "$status" 2 "belong is refused"
  expect_contains "$err" "never runs against belong" "the refusal names belong"
}

test_refuses_another_installation() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"scratch","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  run_check
  expect_status "$status" 2 "another installation is refused"
  expect_contains "$err" "runs on fugaro only" "the refusal names the sandbox installation"
}

test_the_whole_check_passes() {
  reset_state
  run_check
  expect_status "$status" 0 "every criterion holds"
  expect_contains "$out" "CHECK 136 RESULT: PASS (exit 0)" "the block's verdict"
  expect_contains "$out" "FACT files: PASS" "the file set is one per due period plus the index"
  expect_contains "$out" "FACT address: PASS" "no address in any report"
  expect_contains "$out" "FACT figures: PASS" "the day's figures equal the CLI's"
  expect_contains "$out" "FACT samples: PASS" "the sampled runs read back"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory is gone"
  expect_absent "$out" "not reached" "every row was reached"
}

test_fails_on_a_file_outside_the_folder() {
  reset_state
  python3 - "$state" <<'PY'
import json,os,sys
p=os.path.join(sys.argv[1],"pr.json")
d=json.load(open(p)); d["files"].append({"path":"docs/notes.md"}); json.dump(d,open(p,"w"))
PY
  run_check
  expect_status "$status" 1 "a file outside the folder is a finding"
  expect_contains "$out" "FACT files: FAIL" "the row fails"
  expect_contains "$out" "outside docs/fugaro-reviews/" "and says why"
}

test_fails_on_a_missing_due_period() {
  reset_state
  python3 - "$state" <<'PY'
import json,os,sys
p=os.path.join(sys.argv[1],"pr.json")
d=json.load(open(p)); d["files"]=[f for f in d["files"] if "2026-11-19" not in f["path"]]; json.dump(d,open(p,"w"))
PY
  run_check
  expect_status "$status" 1 "a due period with no file is a finding"
  expect_contains "$out" "FACT files: FAIL" "the row fails"
}

test_fails_on_an_address() {
  reset_state
  echo "docs/fugaro-reviews/2026/$DAY.md:1:contact person@example.com" >"$state/address"
  run_check
  expect_status "$status" 1 "an address in a report is a finding"
  expect_contains "$out" "FACT address: FAIL" "the row fails"
  expect_contains "$out" "D323" "and cites the rule"
}

test_fails_on_task_text() {
  reset_state
  echo "docs/fugaro-reviews/2026/$DAY.md:2:periodic run review" >"$state/task"
  run_check
  expect_status "$status" 1 "task text in a report is a finding"
  expect_contains "$out" "FACT task-text: FAIL" "the row fails"
}

test_fails_when_the_figures_differ() {
  reset_state
  python3 - "$state" <<'PY'
import json,os,sys
p=os.path.join(sys.argv[1],"live.json")
d=json.load(open(p)); d["summary"]["billed"]="$0.900000"; json.dump(d,open(p,"w"))
PY
  run_check
  expect_status "$status" 1 "a dollar figure the CLI does not print is a finding"
  expect_contains "$out" "FACT figures: FAIL" "the row fails"
  expect_contains "$out" "billed" "and names the figure"
}

test_fails_when_the_run_does_not_settle_ready() {
  reset_state
  python3 - "$state" <<'PY'
import json,os,sys
p=os.path.join(sys.argv[1],"ls.json")
d=json.load(open(p)); d["runs"][0]["status"]="halted"; d["runs"][0]["terminal"]=True; d["runs"][0]["pr"]=0; json.dump(d,open(p,"w"))
PY
  run_check
  expect_status "$status" 1 "a report run that halts is a FAIL"
  expect_contains "$out" "FACT settled: FAIL" "the row fails"
}

test_a_recipe_shadowed_by_a_repository_fails() {
  reset_state
  cat >"$state/recipe.txt" <<'EOF'
recipe run-digest from repository (fugaro.yaml)

steps:
- digest: {}
EOF
  run_check
  expect_status "$status" 2 "a shadowed recipe stops the check before it launches anything"
  expect_contains "$out" "FACT recipe: FAIL" "the row fails"
  expect_absent "$(calls)" "fugaro run --recipe" "nothing was launched"
}

test_the_fake_refuses_a_flag_the_cli_does_not_have() {
  reset_state
  set +e
  out=$(env -i PATH="$work/bin:/usr/bin:/bin" LIVE_CHECK_LIB="$LIVE_CHECK_LIB" FAKE136_STATE="$state" \
    "$work/bin/fugaro" runs ls --nope 2>&1)
  status=$?
  set -e
  expect_status "$status" 1 "the strict fake refuses a flag the real CLI does not take"
  expect_contains "$out" "unknown flag: --nope" "with the CLI's own wording"
}

test_preflight_blocks_on_another_installation_and_writes_nothing() {
  reset_state
  cat >"$state/doctor.json" <<'EOF'
{"ok":true,"checks":[],"project":{"name":"scratch","gcp_project":"fugaro-dev","region":"us-east5"}}
EOF
  run_check --preflight
  expect_status "$status" 1 "a blocker exits non-zero"
  expect_contains "$err" "BLOCKED installation" "the blocker is printed"
  expect_contains "$err" "fix:" "with the fix"
  expect_absent "$(calls)" "fugaro run --recipe" "nothing was launched"
  expect_absent "$(calls)" "git fetch" "nothing was fetched"
}

test_preflight_passes_and_launches_nothing() {
  reset_state
  run_check --preflight
  expect_status "$status" 0 "a good snapshot passes"
  expect_contains "$err" "preflight passed" "and says so"
  expect_absent "$(calls)" "fugaro run --recipe" "nothing was launched"
  expect_absent "$(calls)" "gh pr view" "nothing was read from GitHub"
}

# ----------------------------------------------------------------------- runner
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

t test_the_help_lists_the_one_command
t test_refuses_a_coding_agents_session
t test_refuses_belong
t test_refuses_another_installation
t test_the_whole_check_passes
t test_fails_on_a_file_outside_the_folder
t test_fails_on_a_missing_due_period
t test_fails_on_an_address
t test_fails_on_task_text
t test_fails_when_the_figures_differ
t test_fails_when_the_run_does_not_settle_ready
t test_a_recipe_shadowed_by_a_repository_fails
t test_the_fake_refuses_a_flag_the_cli_does_not_have
t test_preflight_blocks_on_another_installation_and_writes_nothing
t test_preflight_passes_and_launches_nothing

if [ "$failures" -ne 0 ]; then
  echo "check136_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check136_test.sh: ok"
