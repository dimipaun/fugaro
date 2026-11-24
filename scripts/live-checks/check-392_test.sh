#!/usr/bin/env bash
# Tests for scripts/live-checks/check-392.sh, against a fake `fugaro` on PATH:
# nothing here reaches the network, there is no cloud project, no policy is
# published, no run is launched and no agent runs any live step of the check.
#
# The fake `fugaro` models the calls the check makes and nothing else. It keeps
# the case's own settings rows (so a key really is put back the way it was
# found), the case's own pending record (the one file that makes this check's
# subject real: `settings set --no-apply` APPENDS `policy` to it, a run with a
# coding agent's marker leaves it where it is and exits non-zero, and a plain
# `apply` answers the step and clears it) and the case's own marks (so a managed
# project really is refused). Which arm of `apply` runs is decided the way the
# real command decides it: by the marker in its own environment, which the check
# sets on that one child's command line.
#
# Usage: bash scripts/live-checks/check-392_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-392.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK392_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (claim|managed|sync|key|saved|drift|forced|owed|finish|closed|teardown|aborted):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(xtrace agent-session installation belong managed sync key saved drift forced owed finish closed teardown claim)

# The installation every case runs against: its project is not `belong` and its
# marks do not answer that it is managed.
PROJECT=sbx
GCP=sbx-proj
REGION=europe-west1
KEY=bake_repos
OTHER_KEY=issues.nudge_after_days
OLD=true
NEW=false

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-392.sh's test only. The settings rows and
# the pending record are files in the case's own directory, so the change really
# changes what the next read answers, the forced run really leaves the step
# owed, and the recovery really finishes it.
set -euo pipefail
dir=${FAKE392_DIR:?FAKE392_DIR is not set}
settings=$dir/settings
pending=$dir/pending
mode_of() { if [ -f "$dir/$1.mode" ]; then cat "$dir/$1.mode"; else printf 'ok\n'; fi; }
# mode_or_empty NAME: the knob's value, or nothing at all when the case did not
# set it -- for the knobs whose ABSENCE is the ordinary case (a value to fail one
# write on, and no other).
mode_or_empty() { if [ -f "$dir/$1.mode" ]; then cat "$dir/$1.mode"; fi; }

# setting_write KEY WINS SET VALUE WRITABLE STATE: replace one row of the case's
# settings, or add it. The next read answers what was written, which is what
# makes the drift row and the teardown readings real.
setting_write() {
  local key=$1 wins=$2 set=$3 value=$4 writable=$5 state=$6 tmp="$settings.tmp" seen=no k w s v wr st
  : >"$tmp"
  while IFS='|' read -r k w s v wr st; do
    [ -n "$k" ] || continue
    if [ "$k" = "$key" ]; then
      printf '%s|%s|%s|%s|%s|%s\n' "$key" "$wins" "$set" "$value" "$writable" "$state" >>"$tmp"
      seen=yes
    else
      printf '%s|%s|%s|%s|%s|%s\n' "$k" "$w" "$s" "$v" "$wr" "$st" >>"$tmp"
    fi
  done <"$settings"
  if [ "$seen" = no ]; then
    printf '%s|%s|%s|%s|%s|%s\n' "$key" "$wins" "$set" "$value" "$writable" "$state" >>"$tmp"
  fi
  mv "$tmp" "$settings"
}

# pending_add STEP...: record a step name once, which is what the record the
# check reads is (D1562, D1570).
pending_add() {
  local step
  for step in "$@"; do
    grep -qx "$step" "$pending" 2>/dev/null || printf '%s\n' "$step" >>"$pending"
  done
}

# saved_line: what `fugaro apply` prints for a step it could not answer, word for
# word (applyKeptLine): the sentence, then the line naming the command that
# finishes it.
saved_line() {
  printf 'saved, not live yet: %s still owed\n' "$1"
  printf 'Run `fugaro apply` again to finish the steps it can answer: every step checks the state first, so the ones that already ran cost nothing.\n'
}

# stage_row NAME [WORD]: one stage's own line, in the shape the run prints it
# (the result's glyph, a space, the stage's name, then the detail that result
# carries), on stderr -- the command points its own output there for the length
# of a run so a stage's output can never land on its answer. The plan-view table
# (a mark, a state word, then the name) is NOT this line: an applying run sends
# that table to the log, and a fake that printed it here would hide a reader that
# matched the wrong shape.
stage_row() {
  if [ "${2:-done}" = "left for you" ]; then
    printf '%s %s: %s\n' "!" "$1" "the $1 stage is left for you" >&2
    return
  fi
  printf '%s %s %s\n' "✔" "$1" "the $1 stage ran" >&2
}

# publish: what a run that really answered the policy step leaves behind -- the
# published node carries what the file says, so no row reads unpublished.
publish() {
  local key wins set value writable state tmp="$settings.tmp"
  : >"$tmp"
  while IFS='|' read -r key wins set value writable state; do
    [ -n "$key" ] || continue
    printf '%s|%s|%s|%s|%s|ok\n' "$key" "$wins" "$set" "$value" "$writable" >>"$tmp"
  done <"$settings"
  mv "$tmp" "$settings"
}

# render_rows [ONLY_KEY]: the settings rows, all of them or the one asked for.
render_rows() {
  local want=${1:-} first=yes key wins set value writable state
  while IFS='|' read -r key wins set value writable state; do
    [ -n "$key" ] || continue
    if [ -n "$want" ] && [ "$key" != "$want" ]; then continue; fi
    # A case can make the ONE key's own read answer a state the project's read
    # does not carry (get-state): a key already out of step is what the key row's
    # guard is for, and the project read above it would stop the run first.
    if [ -n "$want" ] && [ -f "$dir/get-state.mode" ]; then state=$(cat "$dir/get-state.mode"); fi
    if [ "$first" = yes ]; then first=no; else printf ','; fi
    printf '{"key":"%s","scope":"local","wins":"%s","default":{"unset":"off unless you say so"},"state":"%s","writable":%s,"owner":"settings set","target_layer":"local"' \
      "$key" "$wins" "$state" "$writable"
    if [ "$set" = yes ]; then printf ',"value":"%s"' "$value"; fi
    printf '}'
  done <"$settings"
}

# render_settings [ONLY_KEY]: the `fugaro settings --json` document, with the
# policy layer's own status, which is what the check's sync row reads.
render_settings() {
  local status
  status=$(mode_of sources)
  case "$status" in
  ok) status=ok ;;
  unread) status="unread: the policy database could not be read" ;;
  esac
  printf '{"version":1,"project":"%s","generated_at":"2026-11-22T00:00:00Z","sources":[{"layer":"local","duration_ms":1,"status":"ok"},{"layer":"policy","duration_ms":1,"status":"%s"}],"settings":[%s]}\n' \
    "${FAKE392_PROJECT:-sbx}" "$status" "$(render_rows "${1:-}")"
}

# render_pending: what `apply --status --json` says is owed, one row per line of
# the record, in the record's own order.
render_pending() {
  local first=yes step
  printf '{"version":1,"project":"%s","gcp_project":"%s","recorded":true,"current":true,"pending":[' \
    "${FAKE392_PROJECT:-sbx}" "${FAKE392_GCP:-sbx-proj}"
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
    printf '{"ok":true,"project":{"name":"belong","gcp_project":"belong-proj","region":"%s"}}\n' "${FAKE392_REGION:-europe-west1}"
    ;;
  *)
    printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"%s"}}\n' \
      "${FAKE392_PROJECT:-sbx}" "${FAKE392_GCP:-sbx-proj}" "${FAKE392_REGION:-europe-west1}"
    ;;
  esac
  ;;
managed)
  printf 'fugaro %s\n' "$*" >>"$dir/calls"
  case "$(mode_of managed)" in
  fail)
    printf 'Error: the marks could not be read\n' >&2
    exit 1
    ;;
  managed)
    printf '{"project":"%s","gcp_project":"%s","managed":true,"reason":"","version":3,"generation":"7","audit":null,"audit_day":"","audit_count":0,"audit_state":"","retirements":null,"retirements_state":""}\n' \
      "${FAKE392_PROJECT:-sbx}" "${FAKE392_GCP:-sbx-proj}"
    ;;
  *)
    printf '{"project":"%s","gcp_project":"%s","managed":false,"reason":"the runs bucket carries no creation fact","version":0,"generation":"","audit":null,"audit_day":"","audit_count":0,"audit_state":"","retirements":null,"retirements_state":""}\n' \
      "${FAKE392_PROJECT:-sbx}" "${FAKE392_GCP:-sbx-proj}"
    ;;
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
    norow) printf '{"version":1,"project":"sbx","generated_at":"2026-11-22T00:00:00Z","sources":[],"settings":[]}\n' ;;
    *) render_settings "${3:-}" ;;
    esac
    ;;
  set)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    # setfail carries a VALUE: the write of that one value fails and every other
    # write goes through, which is how a case fails the teardown's write alone.
    fail_on=$(mode_or_empty setfail)
    if [ "$(mode_of set)" = fail ] || { [ -n "$fail_on" ] && [ "$fail_on" = "${4:-}" ]; }; then
      printf 'Error: the setting could not be written\n' >&2
      exit 1
    fi
    case "$(mode_of set)" in
    wrongsteps)
      printf 'saved, not live yet: policy, repository still owed\n'
      printf 'Run `fugaro apply` again to finish the steps it can answer: every step checks the state first, so the ones that already ran cost nothing.\n'
      ;;
    *)
      if [ "$(mode_of set)" = nodrift ]; then
        setting_state=ok
      else
        setting_state=unpublished
      fi
      setting_write "${3:-}" local yes "${4:-}" true "$setting_state"
      if [[ " $* " == *" --no-apply "* ]]; then
        pending_add policy
        saved_line policy
      else
        publish
        printf 'applied policy\n'
      fi
      ;;
    esac
    ;;
  *)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "${2:-}" in
    --json) render_settings ;;
    *) printf 'settings: a table nobody reads here\n' ;;
    esac
    ;;
  esac
  ;;
apply)
  if [ "${2:-}" = --status ]; then
    printf 'fugaro apply --status --json (stdin=%s)\n' "$(stdin_kind)" >>"$dir/calls"
  else
    printf 'fugaro apply (marker=%s stdin=%s)\n' "${CLAUDECODE:-none}" "$(stdin_kind)" >>"$dir/calls"
  fi
  if [ "${2:-}" = --status ]; then
    case "$(mode_of status)" in
    fail)
      printf 'Error: the record could not be read\n' >&2
      exit 1
      ;;
    empty) printf '{"version":1,"project":"sbx","gcp_project":"sbx-proj","recorded":true,"current":true,"pending":[]}\n' ;;
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
  if [ -n "${CLAUDECODE:-}" ]; then
    # The forced run: a coding agent's session may not publish the policy, so
    # the step is left for the person and the command exits non-zero.
    case "$(mode_of forced)" in
    exit0)
      stage_row policy
      publish
      : >"$pending"
      printf 'applied policy\n'
      exit 0
      ;;
    silent) exit 1 ;;
    other)
      saved_line "repository"
      exit 1
      ;;
    norefusal)
      saved_line policy
      exit 1
      ;;
    publishes)
      stage_row policy
      publish
      : >"$pending"
      saved_line policy
      exit 1
      ;;
    *)
      stage_row policy "left for you"
      printf "a coding agent's session is present (CLAUDECODE is set), so nothing is applied: run this in your own terminal window, not through a coding agent\n" >&2
      saved_line policy
      exit 1
      ;;
    esac
  fi
  case "$(mode_of finish)" in
  fail)
    stage_row policy
    printf 'Error: the policy stage failed\n' >&2
    exit 1
    ;;
  silent)
    stage_row policy
    publish
    : >"$pending"
    exit 0
    ;;
  nopublish)
    stage_row policy
    : >"$pending"
    printf 'applied policy\n'
    exit 0
    ;;
  *)
    if [ ! -s "$pending" ]; then
      printf 'Nothing to apply.\n'
      exit 0
    fi
    while IFS= read -r step; do
      [ -n "$step" ] || continue
      stage_row "$step"
    done <"$pending"
    printf 'applied %s\n' "$(paste -sd, "$pending" | sed 's/,/, /g')"
    publish
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
  : >"$dir/state/pending"
  # The key the check writes: stated by this machine's own config at the local
  # layer, carried by the generic write path, a boolean, and in step with the
  # published node. That is the state a run of the check starts from and the
  # state the teardown has to leave behind. A second key is here so a case can
  # make a change of the operator's own wait to be published.
  printf '%s|local|yes|%s|true|ok\n' "$KEY" "$OLD" >"$dir/state/settings"
  printf '%s|local|yes|8|true|ok\n' "$OTHER_KEY" >>"$dir/state/settings"
  status=0
  out=""
  err=""
  scratch=""
  RUN392_ARGS=()
  RUN392_ENV=()
  RUN392_BASH=(bash)
}

run392() {
  status=0
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  out=$(cd "${RUN392_CWD:-$dir/repo}" && env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE392_DIR="$dir/state" FAKE392_PROJECT="$PROJECT" FAKE392_GCP="$GCP" FAKE392_REGION="$REGION" \
    ${RUN392_ENV[@]+"${RUN392_ENV[@]}"} \
    "${RUN392_BASH[@]}" "$script" ${RUN392_ARGS[@]+"${RUN392_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN392_ARGS=()
  RUN392_ENV=()
  RUN392_BASH=(bash)
  RUN392_CWD=""
}

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 392 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 392 RESULT END (exit " "$1: the block's end"
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

# setting_of KEY: the case's row for one key, as the file holds it.
setting_of() {
  grep "^$1|" "$dir/state/settings" || true
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
  run392 --help
  expect_status "$status" 0 "a help run"
  expect_contains "$out" "usage: check-392.sh" "a help run prints the usage"
  expect_absent "$out" "FACT " "a help run prints no block"
  expect_no_calls "a help run"
}

test_an_unknown_argument_is_refused() {
  setup_case badarg
  run392 --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument" "the refusal"
  expect_no_calls "an unknown argument"
}

test_a_deadline_that_is_not_a_number_is_refused() {
  setup_case baddeadline
  run392 --deadline soon
  expect_status "$status" 2 "a --deadline that is not a number"
  expect_contains "$err" "--deadline" "the refusal names the flag"
  expect_no_calls "a --deadline that is not a number"
}

test_a_value_that_is_not_boolean_is_refused() {
  setup_case badvalue
  run392 --value yes
  expect_status "$status" 2 "a --value that is not true or false"
  expect_contains "$err" "true or false" "the refusal says the shape it needs"
  expect_no_calls "a --value that is not true or false"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry
  run392 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_block_keys "a dry run"
  expect_contains "$(fact_line claim)" "planned" "a dry run says the verdict is a real run's"
  expect_contains "$(fact_line forced)" "CLAUDECODE=1" "a dry run says how the failure is forced"
  expect_contains "$(fact_line managed)" "managed" "a dry run says the marks are read"
  expect_no_calls "a dry run"
  assert_scratch_removed "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-belong
  run392 --dry-run --project belong
  expect_status "$status" 2 "a dry run pointed at belong"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_contains "$(fact_line belong)" "belong" "the belong row names what it refused"
  expect_no_calls "a dry run pointed at belong"
}

test_the_happy_path_passes() {
  setup_case happy
  run392
  expect_status "$status" 0 "the happy path"
  expect_block_keys "the happy path"
  expect_contains "$(fact_line managed)" "PASS" "the managed row"
  expect_contains "$(fact_line sync)" "PASS" "the sync row"
  expect_contains "$(fact_line key)" "PASS" "the key row"
  expect_contains "$(fact_line key)" "$NEW" "the key row names the answer it writes"
  expect_contains "$(fact_line saved)" "PASS" "the saved row"
  expect_contains "$(fact_line drift)" "PASS" "the drift row"
  expect_contains "$(fact_line drift)" "unpublished" "the drift row names the state the write leaves"
  expect_contains "$(fact_line forced)" "PASS" "the forced row"
  expect_contains "$(fact_line forced)" "did not run" "the forced row says the step did not run"
  expect_contains "$(fact_line owed)" "PASS" "the owed row"
  expect_contains "$(fact_line finish)" "PASS" "the finish row"
  expect_contains "$(fact_line closed)" "PASS" "the closed row"
  expect_contains "$(fact_line teardown)" "PASS" "the teardown row"
  expect_contains "$(fact_line claim)" "PASS" "the claim row"
  assert_scratch_removed "the happy path"
}

test_the_change_is_written_with_no_apply() {
  setup_case noapply
  run392
  expect_status "$status" 0 "the happy path"
  expect_call "the change" "settings set ${KEY} ${NEW} --layer local --no-apply"
  # Every write this check makes is saved and not live (D1565): a `settings set`
  # without --no-apply would apply the step itself, which is the one thing the
  # change must not do.
  local line bad=""
  while IFS= read -r line; do
    case "$line" in
    "fugaro settings set "*) case "$line" in *" --no-apply"*) ;; *) bad="$line" ;; esac ;;
    esac
  done <"$dir/state/calls"
  [ -z "$bad" ] || fail "a settings write ran without --no-apply: $bad"
}

test_the_forced_run_carries_the_marker_and_the_recovery_does_not() {
  setup_case marker
  run392
  expect_status "$status" 0 "the happy path"
  expect_call "the forced run" "fugaro apply (marker=1"
  expect_call "the recovery" "fugaro apply (marker=none"
}

test_the_three_applies_run_with_their_stdin_closed() {
  setup_case stdin
  run392
  expect_status "$status" 0 "the happy path"
  local n
  n=$(grep -c '^fugaro apply (marker=.*stdin=closed)' "$dir/state/calls" || true)
  [ "${n:-0}" = 3 ] || fail "the three applies (the forced one, the recovery and the teardown's) ran with stdin closed ${n:-0} time(s), not three: $(calls | tr '\n' ';')"
  expect_no_call "nothing was fed to an apply" "stdin=line"
}

test_the_apply_runs_three_times_over_the_run() {
  setup_case applycount
  run392
  expect_status "$status" 0 "the happy path"
  # Three and no more: the forced run, the recovery this check is about, and the
  # teardown's own. A fourth would be a change that applied itself, which is what
  # --no-apply forbids (D1565).
  local n
  n=$(grep -c '^fugaro apply (marker=' "$dir/state/calls" || true)
  [ "${n:-0}" = 3 ] || fail "apply ran ${n:-0} time(s) over the run, and it must run exactly three (the forced run, the recovery and the teardown's): $(grep '^fugaro apply (marker=' "$dir/state/calls" | tr '\n' ';')"
}

test_a_key_this_machines_config_does_not_state_is_unmeasured() {
  setup_case unsetkey
  # The key is stated by the default layer and not by this machine's own config,
  # so the write would have nothing to put back: the check refuses it.
  printf '%s|default|no||true|ok\n' "$KEY" >"$dir/state/settings"
  run392
  expect_status "$status" 2 "a key this machine's config does not state"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_no_call "nothing was written" "settings set"
}

test_an_apply_that_exits_zero_is_a_fail() {
  setup_case forcedzero
  mode forced exit0
  run392
  expect_status "$status" 1 "a forced apply that exited 0"
  expect_contains "$(fact_line forced)" "FAIL" "the forced row"
  expect_contains "$(fact_line forced)" "RAN in a coding agent's session" "the forced row names what went wrong"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  # The teardown still runs: what this check wrote has to go back whatever the
  # forced run did.
  expect_call "the teardown" "settings set ${KEY} ${OLD} --layer local"
}

test_a_forced_run_that_says_nothing_is_a_fail() {
  setup_case forcedsilent
  mode forced silent
  run392
  expect_status "$status" 1 "a forced apply that said nothing"
  expect_contains "$(fact_line forced)" "FAIL" "the forced row"
  expect_contains "$(fact_line forced)" "saved, not live yet" "the forced row says which sentence is missing"
}

test_a_kept_line_that_names_another_step_is_a_fail() {
  setup_case forcedother
  mode forced other
  run392
  expect_status "$status" 1 "a forced apply that kept another step"
  expect_contains "$(fact_line forced)" "FAIL" "the forced row"
  expect_contains "$(fact_line forced)" "repository" "the forced row names what it read"
}

test_a_forced_run_without_the_refusal_is_a_fail() {
  setup_case forcednorefusal
  mode forced norefusal
  run392
  expect_status "$status" 1 "a forced apply with no refusal"
  expect_contains "$(fact_line forced)" "FAIL" "the forced row"
  expect_contains "$(fact_line forced)" "session's refusal" "the forced row says what is missing"
}

test_a_step_that_is_not_left_owed_is_a_fail() {
  setup_case owedempty
  mode status empty
  run392
  expect_status "$status" 1 "a record that owes nothing after the forced run"
  expect_contains "$(fact_line owed)" "FAIL" "the owed row"
  expect_contains "$(fact_line owed)" "owes 0 step(s)" "the owed row says what it read"
  expect_contains "$(fact_line owed)" "still be owed exactly once" "the owed row says what it required"
}

test_a_step_left_owed_twice_is_a_fail() {
  setup_case owedtwice
  mode status twice
  run392
  expect_status "$status" 1 "a record that owes the step twice"
  expect_contains "$(fact_line owed)" "FAIL" "the owed row"
}

test_a_step_that_is_not_the_records_is_a_fail() {
  setup_case owedextra
  mode status extra
  run392
  expect_status "$status" 1 "a record that owes another step too"
  expect_contains "$(fact_line owed)" "FAIL" "the owed row"
  expect_contains "$(fact_line owed)" "installation" "the owed row names the extra step"
}

test_a_forced_run_that_published_anyway_is_a_fail() {
  setup_case forcedpublishes
  mode forced publishes
  run392
  expect_status "$status" 1 "a forced run that published the change anyway"
  expect_contains "$(fact_line owed)" "FAIL" "the owed row"
  expect_contains "$(fact_line owed)" "owes 0 step(s)" "the owed row says the record was cleared"
}

test_a_recovery_that_fails_is_a_fail() {
  setup_case finishfail
  mode finish fail
  run392
  expect_status "$status" 1 "a recovery that failed"
  expect_contains "$(fact_line finish)" "FAIL" "the finish row"
  expect_contains "$(fact_line finish)" "did not finish the step" "the finish row says what it required"
}

test_a_recovery_that_answers_nothing_is_a_fail() {
  setup_case finishsilent
  mode finish silent
  run392
  expect_status "$status" 1 "a recovery that said nothing"
  expect_contains "$(fact_line finish)" "FAIL" "the finish row"
  expect_contains "$(fact_line finish)" "applied" "the finish row says which line is missing"
}

test_a_change_that_stays_unpublished_is_a_fail() {
  setup_case finishnopublish
  mode finish nopublish
  run392
  expect_status "$status" 1 "a recovery that published nothing"
  expect_contains "$(fact_line closed)" "FAIL" "the closed row"
  expect_contains "$(fact_line closed)" "state=unpublished" "the closed row says what it read"
}

test_a_record_that_cannot_be_read_is_unmeasured() {
  setup_case statusfail
  mode status fail
  run392
  expect_status "$status" 2 "a record that cannot be read"
  expect_contains "$(fact_line owed)" "FAIL" "the owed row"
  expect_contains "$(fact_line owed)" "could not be read" "the owed row says why"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_change_that_fails_is_a_fail() {
  setup_case setfail
  mode set fail
  run392
  expect_status "$status" 1 "a change that could not be saved"
  expect_contains "$(fact_line saved)" "FAIL" "the saved row"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  expect_no_call "the forced run never happened" "marker=1"
  expect_no_call "the teardown had nothing to put back" "settings set ${KEY} ${OLD}"
}

test_a_saved_line_that_names_the_wrong_steps_is_a_fail() {
  setup_case setwrongsteps
  mode set wrongsteps
  run392
  expect_status "$status" 1 "a saved line that names the wrong steps"
  expect_contains "$(fact_line saved)" "FAIL" "the saved row"
  expect_contains "$(fact_line saved)" "policy, repository" "the saved row names what it read"
  expect_no_call "the forced run never happened" "marker=1"
}

test_a_key_that_cannot_be_read_is_unmeasured() {
  setup_case getfail
  mode get fail
  run392
  expect_status "$status" 2 "a key that cannot be read"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_contains "$(fact_line key)" "must not write" "the key row says why it stops"
  expect_no_call "nothing was written" "settings set"
}

test_a_key_with_no_row_is_unmeasured() {
  setup_case getnorow
  mode get norow
  run392
  expect_status "$status" 2 "a key the read answers no row for"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_no_call "nothing was written" "settings set"
}

test_a_key_the_write_path_refuses_is_unmeasured() {
  setup_case getnotwritable
  printf '%s|local|yes|%s|false|ok\n' "$KEY" "$OLD" >"$dir/state/settings"
  run392
  expect_status "$status" 2 "a key the generic write path does not carry"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_contains "$(fact_line key)" "writable=no" "the key row says what it read"
  expect_no_call "nothing was written" "settings set"
}

test_a_key_nothing_states_is_unmeasured() {
  setup_case getunset
  printf '%s|default|no||true|ok\n' "$KEY" >"$dir/state/settings"
  run392
  expect_status "$status" 2 "a key no layer states"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_contains "$(fact_line key)" "for ever" "the key row says why it must not be written"
  expect_no_call "nothing was written" "settings set"
}

test_a_key_that_is_not_a_boolean_is_unmeasured() {
  setup_case getnotbool
  printf '%s|local|yes|7|true|ok\n' "$KEY" >"$dir/state/settings"
  run392
  expect_status "$status" 2 "a key that is not a boolean"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_contains "$(fact_line key)" "not a boolean" "the key row says what it read"
  expect_no_call "nothing was written" "settings set"
}

test_a_key_that_is_already_out_of_step_is_unmeasured() {
  setup_case getdrifted
  mode get-state unpublished
  run392
  expect_status "$status" 2 "a key that is already not in step"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_contains "$(fact_line key)" "state=unpublished" "the key row says what it read"
  expect_no_call "nothing was written" "settings set"
}

test_the_answer_you_typed_is_written() {
  setup_case value
  run392 --value "$NEW"
  expect_status "$status" 0 "the happy path with --value"
  expect_call "the change" "settings set ${KEY} ${NEW} --layer local --no-apply"
}

test_an_answer_that_is_already_held_is_unmeasured() {
  setup_case valueheld
  run392 --value "$OLD"
  expect_status "$status" 2 "--value is the answer the key already holds"
  expect_contains "$(fact_line key)" "FAIL" "the key row"
  expect_contains "$(fact_line key)" "already holds" "the key row says why"
  expect_no_call "nothing was written" "settings set"
}

test_a_managed_project_is_unmeasured() {
  setup_case managed
  mode managed managed
  run392
  expect_status "$status" 2 "a managed project"
  expect_contains "$(fact_line managed)" "FAIL" "the managed row"
  expect_contains "$(fact_line managed)" "D1016" "the managed row says why the marker is no refusal there"
  expect_no_call "nothing was written" "settings set"
}

test_a_project_whose_marks_cannot_be_read_is_unmeasured() {
  setup_case managedfail
  mode managed fail
  run392
  expect_status "$status" 2 "marks that cannot be read"
  expect_contains "$(fact_line managed)" "FAIL" "the managed row"
  expect_contains "$(fact_line managed)" "premise" "the managed row says what it could not tell"
  expect_no_call "nothing was written" "settings set"
}

test_a_policy_source_that_was_not_read_is_unmeasured() {
  setup_case sourcesunread
  mode sources unread
  run392
  expect_status "$status" 2 "a policy layer that was not read"
  expect_contains "$(fact_line sync)" "FAIL" "the sync row"
  expect_contains "$(fact_line sync)" "unread" "the sync row says what the read answered"
  expect_no_call "nothing was written" "settings set"
}

test_a_change_of_yours_waiting_to_be_published_is_unmeasured() {
  setup_case sourcesdrift
  printf '%s|local|yes|%s|true|ok\n' "$KEY" "$OLD" >"$dir/state/settings"
  printf '%s|local|yes|8|true|unpublished\n' "$OTHER_KEY" >>"$dir/state/settings"
  run392
  expect_status "$status" 2 "a change of the operator's waiting to be published"
  expect_contains "$(fact_line sync)" "FAIL" "the sync row"
  expect_contains "$(fact_line sync)" "$OTHER_KEY" "the sync row names the key"
  expect_no_call "nothing was written" "settings set"
}

test_a_drift_that_does_not_appear_is_unmeasured() {
  setup_case nodrift
  mode set nodrift
  run392
  expect_status "$status" 2 "a write that leaves no drift"
  expect_contains "$(fact_line drift)" "FAIL" "the drift row"
  expect_contains "$(fact_line drift)" "No changes" "the drift row says what a stage with no work would answer"
  expect_no_call "the forced run never happened" "marker=1"
  # The change WAS saved, so it has to go back even though nothing was forced.
  expect_call "the teardown" "settings set ${KEY} ${OLD} --layer local"
}

test_a_teardown_that_fails_is_a_fail() {
  setup_case teardownfail
  # Only the write of the value the teardown puts back fails, so this case is
  # about the teardown and nothing else.
  mode setfail "$OLD"
  run392
  expect_status "$status" 1 "a teardown that could not put the setting back"
  expect_contains "$(fact_line teardown)" "FAIL" "the teardown row"
  expect_contains "$(fact_line teardown)" "could not be put back" "the teardown row says what it read"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  expect_contains "$(fact_line claim)" "teardown" "the claim row names the row that did not hold"
}

test_the_teardown_puts_the_setting_back() {
  setup_case teardown
  run392
  expect_status "$status" 0 "the happy path"
  expect_call "the teardown" "settings set ${KEY} ${OLD} --layer local"
  expect_contains "$(setting_of "$KEY")" "|$OLD|" "the key is back to the answer it held"
  expect_contains "$(setting_of "$KEY")" "|ok" "and the published node carries it"
  expect_contains "$(fact_line teardown)" "back to $OLD" "the teardown row says so"
}

test_a_forced_run_that_published_anyway_is_still_undone() {
  setup_case forcedpublishes-undo
  mode forced publishes
  run392
  expect_status "$status" 1 "a forced run that published the change anyway"
  expect_contains "$(setting_of "$KEY")" "|$OLD|" "the key is back to the answer it held"
  expect_contains "$(fact_line teardown)" "PASS" "the teardown row"
}

test_a_coding_agents_session_is_refused() {
  setup_case agent
  RUN392_ENV=(CLAUDECODE=1)
  run392
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE" "the agent-session row names the marker"
  expect_no_calls "a coding agent's session"
}

test_an_editors_own_marker_is_dropped_not_refused() {
  setup_case ide
  RUN392_ENV=(CLAUDE_CODE_SSE_PORT=1234)
  run392
  expect_status "$status" 0 "a shell in an editor's terminal"
  expect_contains "$(fact_line agent-session)" "PASS" "the agent-session row"
}

test_a_shell_that_arrived_tracing_is_refused() {
  setup_case xtrace
  RUN392_BASH=(bash -x)
  run392
  expect_status "$status" 2 "a shell that arrived tracing"
  expect_contains "$(fact_line xtrace)" "FAIL" "the xtrace row"
  expect_no_calls "a shell that arrived tracing"
}

test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run392 --project belong
  expect_status "$status" 2 "belong typed on the command line"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_no_calls "belong typed on the command line"
}

test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-doctor
  mode doctor belong
  run392
  expect_status "$status" 2 "belong answered by doctor"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_call "the installation was read" "doctor --json"
  expect_no_call "nothing was written" "settings set"
}

test_a_project_you_typed_that_is_not_the_configs_is_refused() {
  setup_case twoprojects
  run392 --project somethingelse
  expect_status "$status" 2 "two different installations"
  expect_contains "$err" "two different installations" "the refusal"
  expect_no_call "nothing was written" "settings set"
}

test_an_installation_that_cannot_be_read_stops_the_run() {
  setup_case doctorfail
  mode doctor fail
  run392
  expect_status "$status" 2 "an installation that cannot be read"
  expect_contains "$err" "cannot tell which installation" "the refusal names what it could not read"
  expect_contains "$(fact_line installation)" "not reached" "the installation row"
  expect_no_call "nothing was written" "settings set"
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
t test_an_unknown_argument_is_refused
t test_a_deadline_that_is_not_a_number_is_refused
t test_a_value_that_is_not_boolean_is_refused
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_the_happy_path_passes
t test_the_change_is_written_with_no_apply
t test_the_forced_run_carries_the_marker_and_the_recovery_does_not
t test_the_three_applies_run_with_their_stdin_closed
t test_the_apply_runs_three_times_over_the_run
t test_a_key_this_machines_config_does_not_state_is_unmeasured
t test_an_apply_that_exits_zero_is_a_fail
t test_a_forced_run_that_says_nothing_is_a_fail
t test_a_kept_line_that_names_another_step_is_a_fail
t test_a_forced_run_without_the_refusal_is_a_fail
t test_a_step_that_is_not_left_owed_is_a_fail
t test_a_step_left_owed_twice_is_a_fail
t test_a_step_that_is_not_the_records_is_a_fail
t test_a_forced_run_that_published_anyway_is_a_fail
t test_a_recovery_that_fails_is_a_fail
t test_a_recovery_that_answers_nothing_is_a_fail
t test_a_change_that_stays_unpublished_is_a_fail
t test_a_record_that_cannot_be_read_is_unmeasured
t test_a_change_that_fails_is_a_fail
t test_a_saved_line_that_names_the_wrong_steps_is_a_fail
t test_a_key_that_cannot_be_read_is_unmeasured
t test_a_key_with_no_row_is_unmeasured
t test_a_key_the_write_path_refuses_is_unmeasured
t test_a_key_nothing_states_is_unmeasured
t test_a_key_that_is_not_a_boolean_is_unmeasured
t test_a_key_that_is_already_out_of_step_is_unmeasured
t test_the_answer_you_typed_is_written
t test_an_answer_that_is_already_held_is_unmeasured
t test_a_managed_project_is_unmeasured
t test_a_project_whose_marks_cannot_be_read_is_unmeasured
t test_a_policy_source_that_was_not_read_is_unmeasured
t test_a_change_of_yours_waiting_to_be_published_is_unmeasured
t test_a_drift_that_does_not_appear_is_unmeasured
t test_a_teardown_that_fails_is_a_fail
t test_the_teardown_puts_the_setting_back
t test_a_forced_run_that_published_anyway_is_still_undone
t test_a_coding_agents_session_is_refused
t test_an_editors_own_marker_is_dropped_not_refused
t test_a_shell_that_arrived_tracing_is_refused
t test_belong_is_refused_in_what_you_typed
t test_belong_is_refused_in_what_doctor_answers
t test_a_project_you_typed_that_is_not_the_configs_is_refused
t test_an_installation_that_cannot_be_read_stops_the_run

if [ "$failures" != 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "check-392_test.sh: all cases passed"
