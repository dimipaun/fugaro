#!/usr/bin/env bash
# Tests for scripts/live-checks/check-391.sh, against a fake `fugaro` on PATH:
# nothing here reaches the network, there is no cloud project, no image is
# built, no job is redeployed and no agent runs any live step of the check.
#
# The fake `fugaro` models the calls the check makes and nothing else. It keeps
# the case's own catalog (so `image ls` really answers differently before the
# add and after the teardown), the case's own settings rows (so a key really is
# put back the way it was found) and the case's own pending record — the one
# file that makes this check's subject real: `image add --no-apply` and
# `settings set --no-apply` APPEND step names to it, deduplicated by name, and
# `apply` reads it, prints one row per step it ran and clears it. A case can
# therefore make the record hold `policy` twice, or hold a step that is not the
# record's, and the check's own reading is what answers.
#
# Usage: bash scripts/live-checks/check-391_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-391.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK391_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (claim|saved|status|applied|stages|owed|teardown|keys|entry|aborted):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(xtrace agent-session checkout installation belong entry keys saved status applied stages owed teardown claim)

# The installation every case runs against: its project is not `belong`.
PROJECT=sbx
GCP=sbx-proj
REGION=europe-west1
REPO=acme/app
ENTRY=chk391-catalog
DOCKERFILE=acme/app:Dockerfile
KEY_A=issues.nudge_after_days
VALUE_A=8
KEY_B=issues.nudge_recurrence
VALUE_B=4

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-391.sh's test only. The catalog, the
# settings rows and the pending record are files in the case's own directory,
# so the three changes really change what the next read answers and `apply`
# really answers out of the record the changes left.
set -euo pipefail
dir=${FAKE391_DIR:?FAKE391_DIR is not set}
catalog=$dir/catalog
settings=$dir/settings
pending=$dir/pending
mode_of() { if [ -f "$dir/$1.mode" ]; then cat "$dir/$1.mode"; else printf 'ok\n'; fi; }

# pending_add STEP...: record a step name once, which is the whole point of the
# record the check measures (D1562, D1570): three changes that owe `policy`
# leave ONE entry for it.
pending_add() {
  local step
  for step in "$@"; do
    grep -qx "$step" "$pending" 2>/dev/null || printf '%s\n' "$step" >>"$pending"
  done
}

saved_line() { # saved_line NAMES...
  local names=$*
  printf 'saved, not live yet: %s owed\n' "$names"
}

# stage_row NAME: one stage's own result line, in the shape the run prints it —
# internal/report's end: the result's glyph, a space, the stage's name, then the
# detail the result carries — on stderr, because the command points its own
# stream there for the length of a run so a stage's output can never land on its
# answer. The plan-view table (a mark, a state word, then the name) is NOT this
# line: an applying run sends that table to the log, and a fake that printed it
# here would hide a reader that matched the wrong shape.
stage_row() {
  printf '%s %s %s\n' "✔" "$1" "the $1 stage ran" >&2
}

# render_catalog: the JSON `image ls` answers, one row per line of the catalog
# file, each `name|digest`.
render_catalog() {
  local first=yes line name digest
  printf '{"project":"%s","entries":[' "${FAKE391_PROJECT:-sbx}"
  while IFS='|' read -r name digest; do
    [ -n "$name" ] || continue
    if [ "$first" = yes ]; then first=no; else printf ','; fi
    printf '{"name":"%s","source":"a Dockerfile in acme/app","digest":"%s"}' "$name" "$digest"
  done <"$catalog"
  printf ']}\n'
}

# render_setting KEY: the one row `settings get <key> --json` answers, out of
# the case's settings file (`key|wins|set|value|writable`). A key the file does
# not hold is a key the read answers nothing about.
render_setting() {
  local want=$1 line key wins set value writable
  while IFS='|' read -r key wins set value writable; do
    [ -n "$key" ] || continue
    [ "$key" = "$want" ] || continue
    printf '{"settings":[{"key":"%s","scope":"%s","state":"%s","wins":"%s","default":"7","writable":%s' \
      "$key" "$wins" "$([ "$set" = yes ] && printf set || printf unset)" "$wins" "$writable"
    if [ "$set" = yes ]; then printf ',"value":"%s"' "$value"; fi
    printf '}]}\n'
    return 0
  done <"$settings"
  printf '{"settings":[]}\n'
}

setting_write() { # setting_write KEY WINS SET VALUE
  local key=$1 wins=$2 set=$3 value=$4 line k w s v wr tmp="$settings.tmp"
  : >"$tmp"
  while IFS='|' read -r k w s v wr; do
    [ -n "$k" ] || continue
    if [ "$k" = "$key" ]; then
      printf '%s|%s|%s|%s|%s\n' "$k" "$wins" "$set" "$value" "$wr" >>"$tmp"
    else
      printf '%s|%s|%s|%s|%s\n' "$k" "$w" "$s" "$v" "$wr" >>"$tmp"
    fi
  done <"$settings"
  mv "$tmp" "$settings"
}

# render_pending: what `apply --status --json` says is owed, one row per line of
# the record, in the record's own order.
render_pending() {
  local first=yes step
  printf '{"version":1,"project":"%s","gcp_project":"%s","recorded":true,"current":true,"pending":[' \
    "${FAKE391_PROJECT:-sbx}" "${FAKE391_GCP:-sbx-proj}"
  while IFS= read -r step; do
    [ -n "$step" ] || continue
    if [ "$first" = yes ]; then first=no; else printf ','; fi
    printf '{"step":"%s","kind":"resource","key":""}' "$step"
  done <"$pending"
  printf ']}\n'
}

stdin_kind() {
  if IFS= read -r -t 2 _line; then printf 'line'; else
    rc=$?
    if [ "$rc" -gt 128 ]; then printf 'open'; else printf 'closed'; fi
  fi
}

case "${1:-}" in
doctor)
  printf 'fugaro %s\n' "$*" >>"$dir/calls"
  case "$(mode_of doctor)" in
  fail)
    printf 'Error: the installation could not be read\n' >&2
    exit 1
    ;;
  belong)
    printf '{"ok":true,"project":{"name":"belong","gcp_project":"belong-proj","region":"%s"}}\n' "${FAKE391_REGION:-europe-west1}"
    ;;
  *)
    printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"%s"}}\n' \
      "${FAKE391_PROJECT:-sbx}" "${FAKE391_GCP:-sbx-proj}" "${FAKE391_REGION:-europe-west1}"
    ;;
  esac
  ;;
image)
  case "${2:-}" in
  ls)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of ls)" in
    fail)
      printf 'Error: the catalog could not be read\n' >&2
      exit 1
      ;;
    *) render_catalog ;;
    esac
    ;;
  add)
    printf 'fugaro %s (stdin=%s)\n' "$*" "$(stdin_kind)" >>"$dir/calls"
    case "$(mode_of add)" in
    fail)
      printf 'Error: the build failed: the Dockerfile was not found\n' >&2
      exit 1
      ;;
    silent) exit 0 ;;
    wrongsteps)
      printf 'saved, not live yet: policy owed\n'
      exit 0
      ;;
    *)
      printf '%s|%s\n' "${3:-}" "sha256:1111111111111111111111111111111111111111111111111111111111111111" >>"$catalog"
      if [[ " $* " == *" --no-apply "* ]]; then
        pending_add catalog-build repository policy
        saved_line catalog-build, repository, policy
      else
        printf 'Image %s added and now available to your jobs.\n' "${3:-}"
      fi
      ;;
    esac
    ;;
  rm)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of rm)" in
    fail)
      printf 'Error: the entry could not be removed\n' >&2
      exit 1
      ;;
    *)
      grep -v "^${3:-}|" "$catalog" >"$catalog.tmp" 2>/dev/null || true
      mv "$catalog.tmp" "$catalog"
      if [[ " $* " == *" --no-apply "* ]]; then
        pending_add catalog-build repository policy
        saved_line catalog-build, repository, policy
      else
        printf 'Image %s removed.\n' "${3:-}"
      fi
      ;;
    esac
    ;;
  *) printf 'fake fugaro: unexpected call: %s\n' "$*" >&2; exit 64 ;;
  esac
  ;;
settings)
  case "${2:-}" in
  get)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of get)" in
    fail)
      printf 'Error: the setting could not be read\n' >&2
      exit 1
      ;;
    *) render_setting "${3:-}" ;;
    esac
    ;;
  set)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of set)" in
    fail)
      printf 'Error: the setting could not be written\n' >&2
      exit 1
      ;;
    *)
      setting_write "${3:-}" local yes "${4:-}"
      if [[ " $* " == *" --no-apply "* ]]; then
        pending_add policy
        printf 'saved, not live yet: policy still owed\n'
      else
        printf 'applied policy\n'
      fi
      ;;
    esac
    ;;
  unset)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of set)" in
    fail)
      printf 'Error: the setting could not be written\n' >&2
      exit 1
      ;;
    *)
      setting_write "${3:-}" default no ""
      if [[ " $* " == *" --no-apply "* ]]; then
        pending_add policy
        printf 'saved, not live yet: policy still owed\n'
      else
        printf 'applied policy\n'
      fi
      ;;
    esac
    ;;
  *) printf 'fake fugaro: unexpected call: %s\n' "$*" >&2; exit 64 ;;
  esac
  ;;
apply)
  printf 'fugaro %s\n' "$*" >>"$dir/calls"
  if [ "${2:-}" = --status ]; then
    case "$(mode_of status)" in
    fail)
      printf 'Error: the record could not be read\n' >&2
      exit 1
      ;;
    twice)
      printf '{"version":1,"project":"sbx","gcp_project":"sbx-proj","recorded":true,"current":true,"pending":[{"step":"policy","kind":"resource","key":"a"},{"step":"policy","kind":"resource","key":"b"}]}\n'
      ;;
    extra)
      printf '{"version":1,"project":"sbx","gcp_project":"sbx-proj","recorded":true,"current":true,"pending":[{"step":"policy","kind":"resource","key":""},{"step":"installation","kind":"resource","key":""}]}\n'
      ;;
    *) render_pending ;;
    esac
    exit 0
  fi
  case "$(mode_of apply)" in
  fail)
    stage_row repository
    printf 'Error: the policy stage failed\n' >&2
    exit 1
    ;;
  silent) exit 0 ;;
  dup)
    stage_row repository
    stage_row policy
    printf 'applied catalog-build, repository, policy, policy\n'
    : >"$pending"
    ;;
  norow)
    printf 'applied catalog-build, repository, policy\n'
    : >"$pending"
    ;;
  twicerow)
    stage_row repository
    stage_row policy
    stage_row policy
    printf 'applied catalog-build, repository, policy\n'
    : >"$pending"
    ;;
  keep)
    stage_row repository
    stage_row policy
    printf 'applied catalog-build, repository, policy\n'
    ;;
  *)
    while IFS= read -r step; do
      [ -n "$step" ] || continue
      [ "$step" = catalog-build ] && continue
      stage_row "$step"
    done <"$pending"
    printf 'applied %s\n' "$(paste -sd, "$pending" | sed 's/,/, /g')"
    : >"$pending"
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------- harness
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home" "$dir/repo"
  write_fake "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/catalog"
  : >"$dir/state/pending"
  # Both keys the check writes: nothing sets either of them, and the generic
  # write path carries both. That is the state a run of the check starts from,
  # and the state the teardown has to leave behind.
  printf '%s|default|no||true\n' "$KEY_A" >"$dir/state/settings"
  printf '%s|default|no||true\n' "$KEY_B" >>"$dir/state/settings"
  git -C "$dir/repo" init -q
  git -C "$dir/repo" remote add origin "https://github.com/${REPO}.git"
  status=0
  out=""
  err=""
  scratch=""
  RUN391_ARGS=()
  RUN391_ENV=()
  RUN391_BASH=(bash)
}

run391() {
  status=0
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  out=$(cd "${RUN391_CWD:-$dir/repo}" && env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE391_DIR="$dir/state" FAKE391_PROJECT="$PROJECT" FAKE391_GCP="$GCP" FAKE391_REGION="$REGION" \
    ${RUN391_ENV[@]+"${RUN391_ENV[@]}"} \
    "${RUN391_BASH[@]}" "$script" ${RUN391_ARGS[@]+"${RUN391_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN391_ARGS=()
  RUN391_ENV=()
  RUN391_BASH=(bash)
  RUN391_CWD=""
}
run391_default() { run391 --dockerfile "$DOCKERFILE" "$@"; }

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 391 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 391 RESULT END (exit " "$1: the block's end"
}

calls() { cat "$dir/state/calls"; }

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

expect_call() { # what needle
  expect_contains "$(calls)" "$2" "$1"
}
expect_no_call() { # what needle
  expect_absent "$(calls)" "$2" "$1"
}

# count_calls NEEDLE: how many logged calls contain the needle.
count_calls() {
  grep -c -- "$1" "$dir/state/calls" || true
}

assert_scratch_removed() { # what
  if [ -z "$scratch" ]; then
    fail "$1: the check named no scratch directory"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch was NOT removed"
  return 0
}

# mode NAME VALUE writes a knob the fake reads.
mode() { # mode name value
  printf '%s\n' "$2" >"$dir/state/$1.mode"
}

# -------------------------------------------------------------------- the cases
test_help() {
  setup_case help
  run391 --help
  expect_status "$status" 0 "a help run"
  expect_contains "$out" "usage: check-391.sh" "a help run prints the usage"
  expect_absent "$out" "FACT " "a help run prints no block"
  expect_no_calls "a help run"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry
  run391_default --dry-run
  expect_status "$status" 0 "a dry run"
  expect_block_keys "a dry run"
  expect_contains "$(fact_line claim)" "planned" "a dry run says the verdict is a real run's"
  expect_contains "$(fact_line saved)" "--no-apply" "a dry run says the changes are saved"
  expect_contains "$(fact_line status)" "one entry per step name" "a dry run says what the record must hold"
  expect_no_calls "a dry run"
  assert_scratch_removed "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-belong
  run391_default --dry-run --project belong
  expect_status "$status" 2 "a dry run pointed at belong"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_contains "$(fact_line belong)" "belong" "the belong row names what it refused"
  expect_no_calls "a dry run pointed at belong"
}

test_a_missing_dockerfile_is_refused() {
  setup_case nodockerfile
  run391 --entry "$ENTRY"
  expect_status "$status" 2 "a run with no --dockerfile"
  expect_contains "$err" "--dockerfile" "the refusal names the flag"
  expect_no_calls "a run with no --dockerfile"
}

test_a_dockerfile_that_is_not_owner_name_path_is_refused() {
  setup_case baddockerfile
  run391 --dockerfile Dockerfile
  expect_status "$status" 2 "a --dockerfile that names no repository"
  expect_contains "$err" "owner/name:path" "the refusal says the shape it needs"
  expect_no_calls "a --dockerfile that names no repository"
}

test_the_happy_path_passes() {
  setup_case happy
  run391_default
  expect_status "$status" 0 "the happy path"
  expect_block_keys "the happy path"
  expect_contains "$(fact_line entry)" "PASS" "the entry row"
  expect_contains "$(fact_line keys)" "PASS" "the keys row"
  expect_contains "$(fact_line saved)" "PASS" "the saved row"
  expect_contains "$(fact_line saved)" "three changes" "the saved row counts them"
  expect_contains "$(fact_line status)" "PASS" "the status row"
  expect_contains "$(fact_line status)" "catalog-build, repository, policy" "the status row names the three steps"
  expect_contains "$(fact_line applied)" "PASS" "the applied row"
  expect_contains "$(fact_line applied)" "catalog-build, repository, policy" "the applied row names the three steps"
  expect_contains "$(fact_line stages)" "PASS" "the stages row"
  expect_contains "$(fact_line stages)" "policy x1" "the stages row counts the policy stage once"
  expect_contains "$(fact_line owed)" "owes nothing" "the owed row"
  expect_contains "$(fact_line teardown)" "PASS" "the teardown row"
  expect_contains "$(fact_line claim)" "PASS" "the claim row"
  assert_scratch_removed "the happy path"
}

test_the_three_changes_are_written_with_no_apply() {
  setup_case noapply
  run391_default
  expect_status "$status" 0 "the happy path"
  expect_call "the entry's change" "image add ${ENTRY} --dockerfile ${DOCKERFILE} --no-apply"
  expect_call "the first setting" "settings set ${KEY_A} ${VALUE_A} --layer local --no-apply"
  expect_call "the second setting" "settings set ${KEY_B} ${VALUE_B} --layer local --no-apply"
}

test_the_one_apply_runs_exactly_twice_over_the_run() {
  setup_case oneapply
  run391_default
  expect_status "$status" 0 "the happy path"
  # Two and no more: the one apply this check is about, and the teardown's own.
  # A third would be a change that applied itself, which is what --no-apply
  # forbids (D1565).
  local n
  n=$(grep -c '^fugaro apply$' "$dir/state/calls" || true)
  [ "${n:-0}" = 2 ] || fail "the apply ran ${n:-0} time(s) over the run, and it must run exactly twice (the one apply and the teardown's): $(grep '^fugaro apply' "$dir/state/calls" | tr '\n' ';')"
  expect_no_call "no change applied itself" "image add ${ENTRY} --dockerfile ${DOCKERFILE} --project"
}

test_the_changes_run_with_their_stdin_closed() {
  setup_case stdin
  run391_default
  expect_status "$status" 0 "the happy path"
  expect_call "the add's own stdin" "image add ${ENTRY} --dockerfile ${DOCKERFILE} --no-apply (stdin=closed)"
}

test_a_policy_entry_twice_is_a_fail() {
  setup_case statustwice
  mode status twice
  run391_default
  expect_status "$status" 1 "a record that holds policy twice"
  expect_contains "$(fact_line status)" "FAIL" "the status row"
  expect_contains "$(fact_line status)" "policy recorded 2 time(s)" "the status row counts them"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_step_that_is_not_the_records_is_a_fail() {
  setup_case statusextra
  mode status extra
  run391_default
  expect_status "$status" 1 "a record holding a step the changes do not owe"
  expect_contains "$(fact_line status)" "FAIL" "the status row"
  expect_contains "$(fact_line status)" "installation" "the status row names the extra step"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_record_that_cannot_be_read_is_unmeasured() {
  setup_case statusfail
  mode status fail
  run391_default
  expect_status "$status" 2 "a record that could not be read"
  expect_contains "$(fact_line status)" "could not be read" "the status row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_policy_stage_line_twice_is_a_fail() {
  setup_case stagetwice
  mode apply twicerow
  run391_default
  expect_status "$status" 1 "a policy stage that ran twice"
  expect_contains "$(fact_line stages)" "FAIL" "the stages row"
  expect_contains "$(fact_line stages)" "policy stage's own line 2 time(s)" "the stages row counts the lines"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_missing_stage_line_is_a_fail() {
  setup_case norow
  mode apply norow
  run391_default
  expect_status "$status" 1 "a run that printed no stage line"
  expect_contains "$(fact_line stages)" "FAIL" "the stages row"
  expect_contains "$(fact_line stages)" "policy stage's own line 0 time(s)" "the stages row says nothing was printed"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_an_applied_list_with_a_step_twice_is_a_fail() {
  setup_case dup
  mode apply dup
  run391_default
  expect_status "$status" 1 "an answer that names policy twice"
  expect_contains "$(fact_line applied)" "FAIL" "the applied row"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_an_apply_that_exits_nonzero_is_a_fail() {
  setup_case applyfail
  mode apply fail
  run391_default
  expect_status "$status" 1 "an apply that failed"
  expect_contains "$(fact_line applied)" "FAIL" "the applied row"
  expect_contains "$(fact_line applied)" "the policy stage failed" "the applied row quotes the run's own line"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  # The teardown still runs: whatever the changes wrote is taken back out.
  expect_call "the teardown ran anyway" "image rm ${ENTRY} --no-apply"
}

test_an_apply_that_answers_nothing_is_a_fail() {
  setup_case applysilent
  mode apply silent
  run391_default
  expect_status "$status" 1 "an apply that printed nothing"
  expect_contains "$(fact_line applied)" "without its own 'applied' line" "the applied row"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_step_left_owed_after_the_apply_is_a_fail() {
  setup_case owed
  mode apply keep
  run391_default
  expect_status "$status" 1 "a record the apply did not clear"
  expect_contains "$(fact_line owed)" "FAIL" "the owed row"
  expect_contains "$(fact_line owed)" "still owed after the one apply" "the owed row"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_change_that_fails_is_a_fail() {
  setup_case addfail
  mode add fail
  run391_default
  expect_status "$status" 1 "a change that failed"
  expect_contains "$(fact_line saved)" "FAIL" "the saved row"
  expect_contains "$(fact_line saved)" "the Dockerfile was not found" "the saved row quotes the run's own line"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  # Nothing was saved, so nothing was applied -- not even by the teardown.
  expect_no_call "the apply never ran" "fugaro apply --project"
}

test_a_saved_line_that_names_the_wrong_steps_is_a_fail() {
  setup_case wrongsteps
  mode add wrongsteps
  run391_default
  expect_status "$status" 1 "a saved line naming the wrong steps"
  expect_contains "$(fact_line saved)" "FAIL" "the saved row"
  expect_contains "$(fact_line saved)" "catalog-build, repository, policy" "the saved row says which steps an image add owes"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_second_change_that_fails_is_a_fail() {
  setup_case setfail
  mode set fail
  run391_default
  expect_status "$status" 1 "a second change that failed"
  expect_contains "$(fact_line saved)" "FAIL" "the saved row"
  expect_contains "$(fact_line saved)" "the setting could not be written" "the saved row quotes the run's own line"
  # The entry the first change wrote is taken back out anyway.
  expect_call "the teardown removed the entry" "image rm ${ENTRY} --no-apply"
  expect_contains "$(fact_line teardown)" "PASS" "the teardown row"
}

test_a_key_that_cannot_be_read_is_unmeasured() {
  setup_case keyread
  mode get fail
  run391_default
  expect_status "$status" 2 "a key that could not be read"
  expect_contains "$(fact_line keys)" "FAIL" "the keys row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
  # A key this check cannot read is one it must not write: nothing is written.
  expect_no_call "the add never ran" "image add"
  expect_no_call "no setting was written" "settings set"
}

test_a_key_the_write_path_refuses_is_unmeasured() {
  setup_case keyunwritable
  printf '%s|default|no||false\n%s|default|no||true\n' "$KEY_A" "$KEY_B" >"$dir/state/settings"
  run391_default
  expect_status "$status" 2 "a key the generic write path refuses"
  expect_contains "$(fact_line keys)" "writable=no" "the keys row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
  expect_no_call "nothing was written" "settings set"
  expect_no_call "the add never ran" "image add"
}

test_a_catalog_that_cannot_be_read_is_unmeasured() {
  setup_case nocatalog
  mode ls fail
  run391_default
  expect_status "$status" 2 "a catalog that could not be read"
  expect_contains "$(fact_line entry)" "FAIL" "the entry row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
  # An entry this check cannot read is one it must not add: nothing is written.
  expect_no_call "the add never ran" "image add"
  expect_no_call "no setting was written" "settings set"
}

test_a_leftover_entry_is_removed_first() {
  setup_case leftover
  printf '%s|sha256:1111\n' "$ENTRY" >"$dir/state/catalog"
  run391_default
  expect_status "$status" 0 "a run over a leftover entry"
  expect_contains "$(fact_line entry)" "leftover" "the entry row says it was a leftover"
  expect_call "the leftover was removed" "image rm ${ENTRY}"
  expect_contains "$(fact_line claim)" "PASS" "the claim row"
}

test_a_teardown_that_fails_is_a_fail() {
  setup_case teardownfail
  mode rm fail
  run391_default
  expect_status "$status" 1 "a teardown that failed"
  expect_contains "$(fact_line teardown)" "FAIL" "the teardown row"
  expect_contains "$(fact_line teardown)" "STILL in the project's catalog" "the teardown row names what is left"
  expect_contains "$(fact_line claim)" "teardown did NOT" "the claim row"
}

test_the_teardown_puts_back_a_key_that_was_set() {
  setup_case restore
  printf '%s|local|yes|5|true\n%s|default|no||true\n' "$KEY_A" "$KEY_B" >"$dir/state/settings"
  run391_default
  expect_status "$status" 0 "a key that was set at the local layer"
  expect_call "the key was written over" "settings set ${KEY_A} ${VALUE_A} --layer local --no-apply"
  expect_call "the key was put back" "settings set ${KEY_A} 5 --layer local --no-apply"
  expect_contains "$(cat "$dir/state/settings")" "${KEY_A}|local|yes|5|true" "the settings file holds the value it started with"
  expect_contains "$(fact_line keys)" "set to 5" "the keys row says what it found"
}

test_the_teardown_unsets_a_key_that_was_unset() {
  setup_case unset
  run391_default
  expect_status "$status" 0 "keys nothing set"
  expect_call "the first key was unset" "settings unset ${KEY_A} --layer local --no-apply"
  expect_call "the second key was unset" "settings unset ${KEY_B} --layer local --no-apply"
  expect_contains "$(cat "$dir/state/settings")" "${KEY_A}|default|no||true" "the settings file holds what it started with"
}

test_a_coding_agents_session_is_refused() {
  setup_case agent
  RUN391_ENV=(CLAUDECODE=1)
  run391_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE" "the agent-session row names the marker"
  expect_no_calls "a coding agent's session"
}

test_an_editors_own_marker_is_dropped_not_refused() {
  setup_case ide
  RUN391_ENV=(CLAUDE_CODE_SSE_PORT=1234)
  run391_default
  expect_status "$status" 0 "a shell in an editor's terminal"
  expect_contains "$(fact_line agent-session)" "PASS" "the agent-session row"
}

test_a_shell_that_arrived_tracing_is_refused() {
  setup_case xtrace
  RUN391_BASH=(bash -x)
  run391_default
  expect_status "$status" 2 "a shell that arrived tracing"
  expect_contains "$(fact_line xtrace)" "FAIL" "the xtrace row"
  expect_no_calls "a shell that arrived tracing"
}

test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run391_default --project belong
  expect_status "$status" 2 "belong typed on the command line"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_no_calls "belong typed on the command line"
}

test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-doctor
  mode doctor belong
  run391_default
  expect_status "$status" 2 "belong answered by doctor"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_call "the installation was read" "doctor --json"
  expect_no_call "the add never ran" "image add"
}

test_a_project_you_typed_that_is_not_the_configs_is_refused() {
  setup_case twoprojects
  run391_default --project somethingelse
  expect_status "$status" 2 "two different installations"
  expect_contains "$err" "two different installations" "the refusal"
  expect_no_call "the add never ran" "image add"
}

test_a_run_outside_a_git_work_tree_is_refused() {
  setup_case nogit
  RUN391_CWD=$dir/tmp
  run391_default
  expect_status "$status" 2 "a run outside a git work tree"
  expect_contains "$err" "not a git work tree" "the refusal"
  expect_no_call "the add never ran" "image add"
}

# Sharding, so the package's Go test can run the tests of this file side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and CHECK_TEST_ONLY=name
# runs that one test alone. Neither set: all run.
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

t test_help
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_missing_dockerfile_is_refused
t test_a_dockerfile_that_is_not_owner_name_path_is_refused
t test_the_happy_path_passes
t test_the_three_changes_are_written_with_no_apply
t test_the_one_apply_runs_exactly_twice_over_the_run
t test_the_changes_run_with_their_stdin_closed
t test_a_policy_entry_twice_is_a_fail
t test_a_step_that_is_not_the_records_is_a_fail
t test_a_record_that_cannot_be_read_is_unmeasured
t test_a_policy_stage_line_twice_is_a_fail
t test_a_missing_stage_line_is_a_fail
t test_an_applied_list_with_a_step_twice_is_a_fail
t test_an_apply_that_exits_nonzero_is_a_fail
t test_an_apply_that_answers_nothing_is_a_fail
t test_a_step_left_owed_after_the_apply_is_a_fail
t test_a_change_that_fails_is_a_fail
t test_a_saved_line_that_names_the_wrong_steps_is_a_fail
t test_a_second_change_that_fails_is_a_fail
t test_a_key_that_cannot_be_read_is_unmeasured
t test_a_key_the_write_path_refuses_is_unmeasured
t test_a_catalog_that_cannot_be_read_is_unmeasured
t test_a_leftover_entry_is_removed_first
t test_a_teardown_that_fails_is_a_fail
t test_the_teardown_puts_back_a_key_that_was_set
t test_the_teardown_unsets_a_key_that_was_unset
t test_a_coding_agents_session_is_refused
t test_an_editors_own_marker_is_dropped_not_refused
t test_a_shell_that_arrived_tracing_is_refused
t test_belong_is_refused_in_what_you_typed
t test_belong_is_refused_in_what_doctor_answers
t test_a_project_you_typed_that_is_not_the_configs_is_refused
t test_a_run_outside_a_git_work_tree_is_refused

if [ "$failures" != 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "check-391_test.sh: all cases passed"
