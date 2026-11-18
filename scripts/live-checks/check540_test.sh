#!/usr/bin/env bash
# Tests for scripts/live-checks/check540.sh, with fake `gh`, `fugaro` and
# `gcloud` on PATH: NOTHING here reaches a cloud, a project, an installation, a
# model or a repository, no agent runs a live command, and the fakes record every
# call they are given — with the directory they were called FROM — so a case can
# assert what the check did, and did not, do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers a
# diagnosis, the settings read and the walk's own reads, and its
# `orchestrator run --once` moves a cycle counter the ledger read is built from —
# so a walk really does walk, rung by rung, and the cases that matter are the
# ones where that model is bent: a ladder that never enters the item, a rung 3
# that never merges, a typed write the fence refuses, a pull request that is not
# a draft.
#
# Two of the cases below are MUTATION checks (the shape check195_test.sh uses):
# the script is copied with one line taken out, and the case asserts the run does
# NOT pass — the guard is load-bearing, and a run that PASSED with the guard
# removed would be this test's own failure. The mutated line is always a guard
# this check depends on, never a row: (1) the agent-session guard, whose removal
# puts the typed write in front of the fence behind it, and (2) the restore,
# whose removal leaves a block that has to say nothing was put back.
#
# Usage: bash scripts/live-checks/check540_test.sh
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check540.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK540_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK540_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

project=chk540-test
item=it-1
head_one=1111111111111111111111111111111111111111
head_two=2222222222222222222222222222222222222222

# --------------------------------------------------------------------- fakes
write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check540.sh's test only. It
# answers the three reads the check makes — the repository's name, the pull
# request, and the pull request again as the walk goes — out of the fixture
# directory it was given, and reaches no host at all.
set -euo pipefail
printf '%s\t%s\n' "$PWD" "$*" >>"$FAKE_LOG_DIR/gh.calls"
mode=${FAKE_MODE:-default}
json=$FAKE_JSON_DIR
cycles=$(cat "$FAKE_LOG_DIR/cycles" 2>/dev/null || echo 0)

case "${1:-}" in
repo)
  cat "$json/name.json"
  ;;
pr)
  case "$mode" in
  ready-pr) cat "$json/pr-ready.json" ;;
  merged-pr) cat "$json/pr-merged.json" ;;
  r3-no-merge) cat "$json/pr-draft.json" ;;
  r3-then-merge)
    if [ "$cycles" -ge 5 ]; then cat "$json/pr-merged.json"; else cat "$json/pr-draft.json"; fi
    ;;
  no-walk) cat "$json/pr-draft.json" ;;
  *)
    if [ "$cycles" -ge 3 ]; then cat "$json/pr-merged.json"; else cat "$json/pr-draft.json"; fi
    ;;
  esac
  ;;
*)
  echo "gh: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
GH_EOF
  chmod +x "$1/bin/gh"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check540.sh's test only: the
# account the check reads at start-up and nothing else. Nothing here reaches a
# real project.
set -euo pipefail
printf '%s\t%s\n' "$PWD" "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
case "${1:-} ${2:-} ${3:-}" in
"config get-value account") printf 'chk540-owner@example.com\n' ;;
*) echo "gcloud: unexpected $*" >&2; exit 1 ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check540.sh's test only. The
# one part of it that is more than a fixture is `orchestrator run --once`: each
# call moves a cycle counter, and the ledger read and the pull request read are
# built from that counter — so the walk this check drives really does move, one
# rung per cycle, and a case can stop it wherever it wants. Nothing here reaches
# a project, a database or a model.
set -euo pipefail
printf '%s\t%s\n' "$PWD" "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
json=$FAKE_JSON_DIR
keyfile=$(printf '%s' "${1:-}${2:-}${3:-}" | tr -c 'A-Za-z0-9' '_')

case "${1:-}" in
version) printf 'fugaro 0.26.0\n' ;;
doctor)
  case "$mode" in
  wrong-installation) cat "$json/doctor-belong.json" ;;
  *) cat "$json/doctor.json" ;;
  esac
  ;;
settings)
  case "${2:-}" in
  get)
    case "${3:-}" in
    escalation.merge_judge)
      case "$mode" in
      no-judge) cat "$json/judge-unset.json" ;;
      *) cat "$json/judge.json" ;;
      esac
      ;;
    escalation.ready_drafts)
      case "$mode" in
      settings-before-set) cat "$json/ready-drafts-set.json" ;;
      *) cat "$json/ready-drafts-unset.json" ;;
      esac
      ;;
    *) cat "$json/ready-drafts-unset.json" ;;
    esac
    ;;
  set)
    if [ "$mode" = write-refused ]; then
      printf 'fugaro: this lets the orchestrator ready a stalled draft on its own, which is the owner'"'"'s own decision, and a coding agent'"'"'s session is refused it whatever --yes says\n' >&2
      exit 2
    fi
    ;;
  unset)
    if [ "$mode" = restore-refused ]; then
      printf 'fugaro: the state bucket did not answer\n' >&2
      exit 2
    fi
    ;;
  *) echo "fugaro: unexpected settings ${2:-}" >&2; exit 1 ;;
  esac
  ;;
orchestrator)
  case "${2:-}" in
  run)
    cycles=$(cat "$FAKE_LOG_DIR/cycles" 2>/dev/null || echo 0)
    cycles=$((cycles + 1))
    printf '%s\n' "$cycles" >"$FAKE_LOG_DIR/cycles"
    printf 'orchestrator chk540: cycle %s: 0 folded, 1 escalated\n' "$cycles"
    ;;
  events)
    # The audit log of the cycles the fake has run: the rung-3 wait line the
    # ladder writes when it stops and asks the owner (the `r3-no-merge` shape),
    # and an empty log while it is still walking.
    cycles=$(cat "$FAKE_LOG_DIR/cycles" 2>/dev/null || echo 0)
    if [ "$mode" = r3-no-merge ] && [ "$cycles" -ge 3 ]; then cat "$json/events-wait.json"; else cat "$json/events-none.json"; fi
    ;;
  *)
    echo "fugaro: unexpected orchestrator ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
escalation)
  if [ "${2:-}" != "show" ]; then echo "fugaro: unexpected escalation ${2:-}" >&2; exit 1; fi
  cycles=$(cat "$FAKE_LOG_DIR/cycles" 2>/dev/null || echo 0)
  if [ "$mode" = no-walk ] || [ "$cycles" -lt 1 ]; then
    printf 'fugaro: work item %s has never entered the escalation ladder, so it has no rung and no transitions: fugaro escalation ls lists the pull requests that have\n' "${3:-}" >&2
    exit 2
  fi
  if [ "$cycles" -ge 3 ]; then cat "$json/show-3.json"; elif [ "$cycles" -eq 2 ]; then cat "$json/show-2.json"; else cat "$json/show-1.json"; fi
  ;;
backlog)
  if [ "${2:-}" != "list" ]; then echo "fugaro: unexpected backlog ${2:-}" >&2; exit 1; fi
  case "$mode" in
  no-followups) cat "$json/backlog-no-fu.json" ;;
  approved-followup) cat "$json/backlog-approved.json" ;;
  *) cat "$json/backlog.json" ;;
  esac
  ;;
*)
  echo "fugaro: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# The documents the check reads and the rows it forms a verdict on. One file per
# shape, so a case bends exactly the one it is about.
write_json() { # dir
  mkdir -p "$1/json"
  cat >"$1/json/name.json" <<JSON
{"nameWithOwner":"chk540-owner/sandbox"}
JSON
  cat >"$1/json/doctor.json" <<JSON
{"ok":true,"checks":[{"name":"credentials","status":"ok"}],"project":{"name":"chk540","gcp_project":"${project}","region":"europe-west1"}}
JSON
  cat >"$1/json/doctor-belong.json" <<JSON
{"ok":true,"project":{"name":"belong","gcp_project":"belong-prod","region":"europe-west1"}}
JSON
  # The judge the ladder's rung 3 needs named, and the two states of the switch
  # this check turns on: unset at the local layer (so the restore answers
  # `settings unset`) and set to false there (so the restore answers `settings
  # set … false`).
  cat >"$1/json/judge.json" <<JSON
{"version":1,"kind":"settings","settings":[{"key":"escalation.merge_judge","value":"strong","wins":"local","levels":[{"layer":"local","value":"strong","outcome":"wins"}]}]}
JSON
  cat >"$1/json/judge-unset.json" <<JSON
{"version":1,"kind":"settings","settings":[{"key":"escalation.merge_judge","value":null,"wins":"default","levels":[]}]}
JSON
  cat >"$1/json/ready-drafts-unset.json" <<JSON
{"version":1,"kind":"settings","settings":[{"key":"escalation.ready_drafts","value":"false","wins":"default","levels":[]}]}
JSON
  cat >"$1/json/ready-drafts-set.json" <<JSON
{"version":1,"kind":"settings","settings":[{"key":"escalation.ready_drafts","value":"false","wins":"local","levels":[{"layer":"local","value":"false","outcome":"wins"}]}]}
JSON
  # The pull request, in the three states the check has a reading for: the stalled
  # draft the walk is about, the same pull request already readied, and merged.
  cat >"$1/json/pr-draft.json" <<JSON
{"number":7,"state":"OPEN","isDraft":true,"headRefOid":"${head_one}","mergedAt":null,"url":"https://github.com/chk540-owner/sandbox/pull/7"}
JSON
  cat >"$1/json/pr-ready.json" <<JSON
{"number":7,"state":"OPEN","isDraft":false,"headRefOid":"${head_one}","mergedAt":null,"url":"https://github.com/chk540-owner/sandbox/pull/7"}
JSON
  cat >"$1/json/pr-merged.json" <<JSON
{"number":7,"state":"MERGED","isDraft":false,"headRefOid":"${head_two}","mergedAt":"2026-11-18T00:03:30Z","url":"https://github.com/chk540-owner/sandbox/pull/7"}
JSON
  # The ledger, one document per cycle the fake has run: rung 0 (the rebase) at
  # the first, rung 2 (the one stronger pass) at the second, rung 3 (the merge
  # rung) at the third. The stamps are what the time-to-merge row measures
  # against the host's mergedAt.
  cat >"$1/json/show-1.json" <<JSON
{"version":1,"kind":"escalation","project":"${project}","now":"2026-11-18T00:10:00Z","item":{"id":"${item}","repo":"chk540-owner/sandbox","rung":"R0","reason":"rebase","entered_at":"2026-11-18T00:01:00Z","age_seconds":3600,"outer_attempts":1,"cost_usd":1.25,
 "transitions":[{"to":"R0","reason":"rebase","at":"2026-11-18T00:01:00Z","head_sha":"${head_one}"}]}}
JSON
  cat >"$1/json/show-2.json" <<JSON
{"version":1,"kind":"escalation","project":"${project}","now":"2026-11-18T00:10:00Z","item":{"id":"${item}","repo":"chk540-owner/sandbox","rung":"R2","reason":"attempts","entered_at":"2026-11-18T00:02:00Z","age_seconds":3660,"outer_attempts":2,"cost_usd":2.5,
 "transitions":[{"to":"R0","reason":"rebase","at":"2026-11-18T00:01:00Z","head_sha":"${head_one}"},
   {"from":"R0","to":"R2","reason":"attempts","at":"2026-11-18T00:02:00Z","head_sha":"${head_two}"}]}}
JSON
  cat >"$1/json/show-3.json" <<JSON
{"version":1,"kind":"escalation","project":"${project}","now":"2026-11-18T00:10:00Z","item":{"id":"${item}","repo":"chk540-owner/sandbox","rung":"R3","reason":"r2_clean","entered_at":"2026-11-18T00:03:00Z","age_seconds":3720,"outer_attempts":3,"cost_usd":3.75,
 "transitions":[{"to":"R0","reason":"rebase","at":"2026-11-18T00:01:00Z","head_sha":"${head_one}"},
   {"from":"R0","to":"R2","reason":"attempts","at":"2026-11-18T00:02:00Z","head_sha":"${head_two}"},
   {"from":"R2","to":"R3","reason":"r2_clean","at":"2026-11-18T00:03:00Z","head_sha":"${head_two}"}]}}
JSON
  # The backlog the landing leaves behind: one follow-up of this item, queued and
  # unapproved, beside another item's work.
  cat >"$1/json/backlog.json" <<JSON
{"version":1,"kind":"backlog","project":"${project}","waiting":1,
 "items":[{"id":"${item}-fu-9f2c1a4b7d8e0c33","title":"a finding left open by it-1","phase":"queued","repo":"chk540-owner/sandbox","node":"","approved":false},
   {"id":"other-1","title":"somebody else's work","phase":"running","repo":"chk540-owner/sandbox","node":"","approved":true}]}
JSON
  cat >"$1/json/backlog-approved.json" <<JSON
{"version":1,"kind":"backlog","project":"${project}","waiting":0,
 "items":[{"id":"${item}-fu-9f2c1a4b7d8e0c33","title":"a finding left open by it-1","phase":"queued","repo":"chk540-owner/sandbox","node":"","approved":true}]}
JSON
  cat >"$1/json/backlog-no-fu.json" <<JSON
{"version":1,"kind":"backlog","project":"${project}","waiting":0,
 "items":[{"id":"other-1","title":"somebody else's work","phase":"queued","repo":"chk540-owner/sandbox","node":"","approved":false}]}
JSON
  # The audit log the walk's own signal is read from: empty while the ladder is
  # still walking, and the rung-3 wait line the ladder writes the moment it stops
  # and asks the owner something.
  cat >"$1/json/events-none.json" <<JSON
{"version":1,"kind":"events","id":"chk540","events":[]}
JSON
  cat >"$1/json/events-wait.json" <<JSON
{"version":1,"kind":"events","id":"chk540","events":[
  {"id":"e1","at":"2026-11-18T00:03:00Z","subject":"${item}","transition":"R2->R3","text":"escalation: rung R3 (r2_clean), run run-1 wait: draft_held"},
  {"id":"e2","at":"2026-11-18T00:03:00Z","subject":"${item}","transition":"R3","text":"escalation: rung R3 (r2_clean), run run-1 wait: paths_not_routine"}]}
JSON
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gh.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/cycles"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The five markers an agent's session carries are stripped first, so the empty
  # environment is the harness's own default: a case that means to be refused
  # puts one back by name, and a case that does not must not inherit this
  # container's. They are unset BEFORE the assignments, so an assignment for one
  # of them still lands.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" FAKE_JSON_DIR="$dir/json" \
    CHECK540_POLL_SECONDS=0 CHECK540_CYCLES="${CHECK540_CYCLES:-6}" \
    CHECK540_WALK_TIMEOUT_SECONDS="${CHECK540_WALK_TIMEOUT_SECONDS:-120}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/sandbox" --item "$item" --pr 7 ${cli[@]+"${cli[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run_copy() { # run_copy DIR SCRIPT ENV...
  local dir=$1 copy=$2
  shift 2
  mkdir -p "$dir/copy" "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gh.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/cycles"
  status=0
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" FAKE_JSON_DIR="$dir/json" \
    CHECK540_POLL_SECONDS=0 CHECK540_CYCLES="${CHECK540_CYCLES:-6}" \
    CHECK540_WALK_TIMEOUT_SECONDS="${CHECK540_WALK_TIMEOUT_SECONDS:-120}" \
    ${1+"$@"} \
    bash "$copy" --sandbox "$dir/sandbox" --item "$item" --pr 7 \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/sandbox"
  printf 'name: sandbox\n' >"$1/sandbox/fugaro.yaml"
  write_fake_gh "$1"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_json "$1"
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_ran_in_the_sandbox() { # dir what
  local calls line
  calls=$(cat "$1/logs/fugaro.calls" 2>/dev/null || true)
  [ -n "$calls" ] || {
    fail "$2: no command the check ran recorded a call"
    return 0
  }
  while IFS= read -r line; do
    case "$line" in
    "$1/sandbox"$'\t'*) ;;
    *) fail "$2: a command the check ran was called from ${line%%$'\t'*} and not from the sandbox checkout" ;;
    esac
  done <<<"$calls"
}

assert_launched_nothing() { # dir what
  local calls
  calls=$(cat "$1/logs/fugaro.calls" 2>/dev/null || true)
  case "$calls" in
  *$'\t'run" "* | *$'\t'run) fail "$2: the check launched a run of its own; every step of the walk is the daemon's" ;;
  esac
}

# Only the argument lists this check means to make, and nothing else: a row that
# writes `\`fugaro …\`` inside a double-quoted string runs that command, and the
# hint it meant to print becomes a call the owner never asked for.
assert_only_known_calls() { # dir what
  local line n=0
  while IFS= read -r line; do
    n=$((n + 1))
    case "${line#*$'\t'}" in
    version | doctor\ --json | "settings get "* | "settings set "* | "settings unset "* \
      | "orchestrator run --once" | "orchestrator events --json" | "escalation show "* | "backlog list --json") ;;
    *) fail "$2: the check ran 'fugaro ${line#*$'\t'}' from ${line%%$'\t'*}, an argument list this check does not make" ;;
    esac
  done <"$1/logs/fugaro.calls"
  [ "$n" -gt 0 ] || fail "$2: no fugaro call was recorded at all"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check540.sh --sandbox" "--help: the form you type"
  expect_contains "$out" "escalation.ready_drafts" "--help: the switch it turns on is named"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "--help: nothing is read"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK540_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 540 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 540 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS.
  expect_contains "$out" "FACT walk: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "up to 6 cycle(s)" "dry run: the plan names the bound it would drive"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "dry run: no cycle is driven"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings set" "dry run: no setting is written"
  expect_absent "$(cat "$dir/logs/gh.calls")" "pr view" "dry run: no pull request is read"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT installation: PASS" "happy path: the installation row"
  expect_contains "$out" "${project} (region europe-west1), and its repository is chk540-owner/sandbox" "happy path: what was read"
  expect_contains "$out" "FACT pull-request: PASS" "happy path: the premise"
  expect_contains "$out" "OPEN and a DRAFT at head ${head_one:0:8}" "happy path: the draft, at the head it is at"
  expect_contains "$out" "FACT settings: PASS" "happy path: the switches"
  expect_contains "$out" "escalation.merge_judge is 'strong'" "happy path: the judge it found named"
  expect_contains "$out" "FACT cycles: PASS" "happy path: the cycles"
  expect_contains "$out" "FACT walk: PASS" "happy path: the walk"
  expect_contains "$out" "R0->R2(attempts)" "happy path: the transitions it read"
  expect_contains "$out" "FACT merge: PASS" "happy path: the merge"
  expect_contains "$out" "MERGED at 2026-11-18T00:03:30Z" "happy path: the host's own stamp"
  expect_contains "$out" "FACT time-to-merge: PASS" "happy path: the figure the check is named for"
  expect_contains "$out" "(150s)" "happy path: the duration it measured"
  expect_contains "$out" "FACT follow-ups: PASS" "happy path: the follow-ups"
  expect_contains "$out" "${item}-fu-9f2c1a4b7d8e0c33" "happy path: the item the landing filed"
  expect_contains "$out" "queued and unapproved" "happy path: and that it waits for a person"
  expect_contains "$out" "CHECK 540 RESULT END (exit 0)" "happy path: the end line"
  # The commands really ran, from the sandbox checkout and nowhere else, and the
  # check launched nothing itself.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" $'\t'"orchestrator run --once" "happy path: the daemon's own cycle"
  expect_contains "$calls" $'\t'"escalation show ${item} --json" "happy path: the ledger read"
  expect_contains "$calls" $'\t'"backlog list --json" "happy path: the backlog read"
  expect_contains "$(cat "$dir/logs/gh.calls")" $'\t'"pr view 7 --repo chk540-owner/sandbox" "happy path: the host read"
  assert_ran_in_the_sandbox "$dir" "happy path"
  assert_only_known_calls "$dir" "happy path"
  assert_launched_nothing "$dir" "happy path"
  assert_scratch_removed "$dir" "happy path"
  # A hint a row prints is prose: the row above went through the reading and the
  # block carries the sentence, never a document a backtick ran into it.
  expect_absent "$out" '{"version":1,"kind":"backlog"' "happy path: no document leaked into a row"
}

test_the_settings_are_written_and_put_back() {
  # The two writes are the whole of what this check changes on the installation,
  # and the restore is what makes them safe to make: each one goes back to the
  # value the local layer held, or is unset when it held none.
  local dir=$work/restore
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the settings: the verdict"
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" $'\t'"settings set escalation.ready_drafts true --layer local" "the settings: the switch is turned on"
  expect_contains "$calls" $'\t'"settings set escalation.r2_after_attempts 1 --layer local" "the settings: the clock is shortened for the check"
  expect_contains "$calls" $'\t'"settings unset escalation.ready_drafts --layer local" "the settings: the switch is unset again"
  expect_contains "$calls" $'\t'"settings unset escalation.r2_after_attempts --layer local" "the settings: the clock is unset again"
  expect_contains "$out" "FACT restore: PASS" "the settings: the restore row"
  expect_absent "$calls" "--yes" "the settings: a typed write is the owner's own hand and passes no --yes"
  # The write is the check's own and it is ordered before the walk: a cycle
  # driven before the switch was on would measure the shipped default.
  local wrote cycle
  wrote=$(grep -n 'settings set escalation.ready_drafts true' "$dir/logs/fugaro.calls" | head -n 1 | cut -d: -f1)
  cycle=$(grep -n 'orchestrator run --once' "$dir/logs/fugaro.calls" | head -n 1 | cut -d: -f1)
  [ -n "$wrote" ] && [ -n "$cycle" ] && [ "$wrote" -lt "$cycle" ] ||
    fail "the settings: the switch was written after the first cycle, so the walk did not run under it"
}

test_a_setting_already_set_is_put_back_to_its_value() {
  # The before-value is read rather than assumed: a switch the owner had already
  # set to false locally is put back to false, not unset — unsetting it would
  # hand the decision to a looser layer, which is a different installation from
  # the one the check found.
  local dir=$work/before
  prepare "$dir"
  run_check "$dir" FAKE_MODE=settings-before-set
  expect_status "$status" 0 "a switch already set: the verdict"
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" $'\t'"settings set escalation.ready_drafts false --layer local" "a switch already set: put back to false"
  expect_absent "$calls" "settings unset escalation.ready_drafts" "a switch already set: not unset"
  expect_contains "$out" "it held escalation.ready_drafts=false escalation.r2_after_attempts=unset" "a switch already set: the row says what it found"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "bash -x"
  expect_contains "$err" "this shell is tracing" "bash -x: the guard names what it refuses"
  expect_contains "$out" "FACT xtrace: FAIL" "bash -x: the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings set" "bash -x: no setting is written"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "bash -x: no cycle is driven"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the refusal names the marker"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: why"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings set" "a coding agent's session: no setting is written"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a coding agent's session: nothing is read either"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" FAKE_MODE=wrong-installation
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings" "belong: no setting is read or written"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "belong: no cycle is driven"
}

test_a_ready_pull_request_is_a_finding() {
  # ready_drafts is what readies a stalled DRAFT: a pull request that is already
  # ready measures nothing about it, and the walk is not driven over a premise
  # the check cannot use.
  local dir=$work/ready
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ready-pr
  expect_status "$status" 1 "a pull request that is not a draft"
  expect_contains "$out" "FACT pull-request: FAIL" "a pull request that is not a draft: the row"
  expect_contains "$out" "open and NOT a draft" "a pull request that is not a draft: what it saw"
  expect_contains "$out" "convertPullRequestToDraft" "a pull request that is not a draft: how to get one"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings set" "a pull request that is not a draft: the switch is not turned on"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "a pull request that is not a draft: no cycle is driven"
  expect_contains "$out" "FACT walk: FAIL — not reached" "a pull request that is not a draft: the walk row says it was not reached"
}

test_a_merged_pull_request_is_a_finding() {
  local dir=$work/merged
  prepare "$dir"
  run_check "$dir" FAKE_MODE=merged-pr
  expect_status "$status" 1 "a pull request already merged"
  expect_contains "$out" "FACT pull-request: FAIL" "a pull request already merged: the row"
  expect_contains "$out" "MERGED at 2026-11-18T00:03:30Z" "a pull request already merged: the host's stamp"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "a pull request already merged: no cycle is driven"
}

test_an_unset_judge_is_a_finding() {
  # A merge at rung 3 needs a judge named: with none the ladder raises a question
  # for the owner instead of landing, so the walk this check measures cannot
  # reach a merge and it stops before driving one.
  local dir=$work/nojudge
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-judge
  expect_status "$status" 1 "no judge named"
  expect_contains "$out" "FACT settings: FAIL" "no judge named: the row"
  expect_contains "$out" "escalation.merge_judge is unset" "no judge named: what is missing"
  expect_contains "$out" "strong or strongest" "no judge named: the closed list it may name"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings set" "no judge named: nothing is written"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "no judge named: no cycle is driven"
}

test_a_walk_that_never_enters_the_ladder_is_a_finding() {
  # The reading the released daemon gives: the cycle carries no ladder seam, no
  # rung is walked, and `escalation show` says the item never entered the ladder.
  # The row has to name BOTH readings — the build that cannot walk it and the
  # item the ladder may not walk — because they are different findings.
  local dir=$work/nowalk
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-walk CHECK540_CYCLES=3
  expect_status "$status" 1 "a ladder that never enters the item"
  expect_contains "$out" "FACT cycles: PASS" "a ladder that never enters the item: the cycles were driven"
  expect_contains "$out" "3 cycle(s) of \`fugaro orchestrator run --once\`" "a ladder that never enters the item: how many"
  expect_contains "$out" "FACT walk: FAIL" "a ladder that never enters the item: the row"
  expect_contains "$out" "never entered the escalation ladder" "a ladder that never enters the item: what the ledger said"
  expect_contains "$out" "internal/cli/orchestrator_run.go" "a ladder that never enters the item: the first reading names the seam"
  expect_contains "$out" "Fugaro merges nothing today" "a ladder that never enters the item: and the release's own limit"
  expect_contains "$out" "the item is APPROVED" "a ladder that never enters the item: the second reading says what to check"
  expect_contains "$out" "FACT merge: FAIL" "a ladder that never enters the item: nothing merged"
}

test_a_merge_that_comes_after_rung_3_is_not_a_false_finding() {
  # Rung 3 is where the ladder launches the review it merges on, and the landing
  # itself comes in a later cycle. A walk stopped at the first sight of rung 3
  # would report a working ladder as a pull request that never merged, so the
  # signal the walk waits for is the ladder's OWN audit line saying it is waiting,
  # and this case is the one that pins it: the ledger is at rung 3 from cycle 3,
  # no wait line is ever written, and the merge arrives at cycle 5.
  local dir=$work/r3merge
  prepare "$dir"
  run_check "$dir" FAKE_MODE=r3-then-merge CHECK540_CYCLES=20
  expect_status "$status" 0 "a merge after rung 3"
  expect_contains "$out" "FACT walk: PASS" "a merge after rung 3: the walk reached the merge rung"
  expect_contains "$out" "FACT merge: PASS" "a merge after rung 3: the merge is read at the host, not from the rung alone"
  expect_contains "$out" "FACT time-to-merge: PASS" "a merge after rung 3: the duration"
  [ "$(grep -c 'orchestrator run --once' "$dir/logs/fugaro.calls")" -eq 5 ] ||
    fail "a merge after rung 3: the check drove $(grep -c 'orchestrator run --once' "$dir/logs/fugaro.calls") cycle(s) and not the five the merge arrives on"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" $'\t'"orchestrator events --json" "a merge after rung 3: the audit log is what the walk reads each cycle"
}

test_a_rung_3_that_does_not_merge_is_a_finding() {
  # Rung 3 is the merge rung: a walk that reaches it and leaves the pull request
  # open is the ladder asking the owner something, and the check stops driving
  # cycles the moment it sees that rather than spending the bound on it.
  local dir=$work/r3
  prepare "$dir"
  run_check "$dir" FAKE_MODE=r3-no-merge CHECK540_CYCLES=20
  expect_status "$status" 1 "rung 3 without a merge"
  expect_contains "$out" "FACT walk: PASS" "rung 3 without a merge: the walk reached the merge rung"
  expect_contains "$out" "FACT merge: FAIL" "rung 3 without a merge: the merge row"
  expect_contains "$out" "is the ladder asking YOU something" "rung 3 without a merge: what it means"
  expect_contains "$out" "wait: paths_not_routine" "rung 3 without a merge: the ladder's own line is what stopped the walk"
  expect_contains "$out" "FACT cycles: PASS" "rung 3 without a merge: the cycles row"
  # Three cycles is where the fake's ledger reaches rung 3: the check stops there
  # and does not drive the other seventeen.
  [ "$(grep -c 'orchestrator run --once' "$dir/logs/fugaro.calls")" -eq 3 ] ||
    fail "rung 3 without a merge: the check drove $(grep -c 'orchestrator run --once' "$dir/logs/fugaro.calls") cycles instead of stopping at rung 3"
  expect_contains "$out" "FACT time-to-merge: FAIL" "rung 3 without a merge: no duration is measurable"
}

test_an_approved_follow_up_is_a_finding() {
  # Nothing the ladder files may be approved by anything but the owner: a landing
  # that approved its own follow-up would spend the owner's word on work nobody
  # read (D1226, D1245, G15).
  local dir=$work/approved
  prepare "$dir"
  run_check "$dir" FAKE_MODE=approved-followup
  expect_status "$status" 1 "an approved follow-up"
  expect_contains "$out" "FACT follow-ups: FAIL" "an approved follow-up: the row"
  expect_contains "$out" "is APPROVED" "an approved follow-up: what it found"
  expect_contains "$out" "spends your word on work you did not read" "an approved follow-up: why it matters"
}

test_a_clean_landing_cannot_say_whether_a_follow_up_waits() {
  # A landing that left nothing open files nothing, and that is not a pending
  # follow-up: the row says it could not measure rather than passing on a run
  # that never exercised the path, and it names the two ways out.
  local dir=$work/clean
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-followups
  expect_status "$status" 2 "a landing that left nothing open"
  expect_contains "$out" "FACT follow-ups: FAIL" "a landing that left nothing open: the row is not a pass"
  expect_contains "$out" "filed no follow-up" "a landing that left nothing open: what it found"
  expect_contains "$out" "CHECK540_EXPECT_FOLLOWUPS=0" "a landing that left nothing open: the knob that records the expectation"
  expect_contains "$err" "could NOT measure follow-ups" "a landing that left nothing open: the exit names the row"
}

test_a_clean_landing_can_be_expected() {
  local dir=$work/cleanok
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-followups CHECK540_EXPECT_FOLLOWUPS=0
  expect_status "$status" 0 "a clean landing expected"
  expect_contains "$out" "FACT follow-ups: PASS" "a clean landing expected: the row"
  expect_contains "$out" "a clean landing is what you expected" "a clean landing expected: why it passes"
}

test_the_settings_can_be_left_alone() {
  # A walk the owner is already watching: the switch and the clock are theirs,
  # and the check reads the judge and drives the cycles without writing either.
  local dir=$work/skip
  prepare "$dir"
  run_check "$dir" CHECK540_SKIP_SETTINGS=1
  expect_status "$status" 0 "the settings left alone"
  expect_contains "$out" "FACT settings: INFO" "the settings left alone: the row"
  expect_contains "$out" "not written (CHECK540_SKIP_SETTINGS)" "the settings left alone: the reason"
  expect_contains "$out" "FACT walk: PASS" "the settings left alone: the walk is still driven"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings set" "the settings left alone: nothing is written"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings unset" "the settings left alone: and nothing is unset"
}

test_a_typed_write_the_fence_refuses_is_a_finding() {
  # The write is the owner's own hand: the CLI's own fence is what refuses it in
  # a session, and this check does not work around that — the row carries the
  # refusal and the walk is not driven behind it.
  local dir=$work/fence
  prepare "$dir"
  run_check "$dir" FAKE_MODE=write-refused
  expect_status "$status" 1 "a write the fence refuses"
  expect_contains "$out" "FACT settings: FAIL" "a write the fence refuses: the row"
  expect_contains "$out" "exited 2" "a write the fence refuses: what it saw"
  expect_contains "$out" "run this at your own terminal" "a write the fence refuses: what to do"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "a write the fence refuses: no cycle is driven"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings unset" "a write the fence refuses: nothing is put back, because nothing was written"
}

test_a_restore_that_did_not_happen_is_a_finding() {
  local dir=$work/restorefail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=restore-refused
  expect_status "$status" 1 "a restore that did not happen"
  expect_contains "$out" "FACT settings: PASS" "a restore that did not happen: the walk still ran"
  expect_contains "$out" "FACT walk: PASS" "a restore that did not happen: the walk row"
  expect_contains "$out" "FACT restore: FAIL" "a restore that did not happen: the row"
  expect_contains "$out" "could NOT be put back" "a restore that did not happen: what it found"
  expect_contains "$out" "put escalation.ready_drafts" "a restore that did not happen: which key, by name"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK540_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK540_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_a_missing_item_is_die_not_a_prompt() {
  # Fugaro asks for nothing: the flag this check cannot work without is a refusal
  # with the fix on the same line, never a question and never a wait on stdin.
  local dir=$work/noitem
  prepare "$dir"
  mkdir -p "$dir/logs"
  : >"$dir/logs/fugaro.calls"
  status=0
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" \
    bash "$script" --sandbox "$dir/sandbox" --pr 7 <"/dev/null" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "no --item"
  expect_contains "$err" "Check 540 needs --item" "no --item: the refusal"
  expect_contains "$err" "fugaro backlog list prints the ids" "no --item: where the id comes from"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "no --item: nothing is read"
}

# The MUTATION half: the guard is taken out of a copy of the script, and the run
# must NOT pass — a PASS with the guard removed would be this test's own failure,
# because it would mean the guard is reporting something it does not cause.
test_mutation_without_the_agent_guard_the_fence_still_refuses() {
  local dir=$work/mutagent
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed 's/^  guard_agent_session$/  : guard_agent_session removed by the test/' "$script" >"$dir/copy/check540.sh"
  expect_contains "$(cat "$dir/copy/check540.sh")" "removed by the test" "mutation: the guard is really out of the copy"
  FAKE_MODE=write-refused run_copy "$dir" "$dir/copy/check540.sh" CLAUDECODE=1
  expect_status "$status" 1 "mutation: the fence behind the guard"
  expect_contains "$out" "FACT settings: FAIL" "mutation: the row the fence produces"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "settings set escalation.ready_drafts true" "mutation: the write was attempted"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" $'\t'orchestrator "mutation: and no cycle was driven behind a refused write"
}

test_mutation_without_the_restore_the_block_says_so() {
  local dir=$work/mutrestore
  prepare "$dir"
  mkdir -p "$dir/copy"
  # `cleanup` calls `restore`; the mutated copy's cleanup does not, which is the
  # one line a check must not lose: a block that PASSED while the installation
  # kept a switch this check turned on would be the defect.
  sed 's/^  restore$/  : restore removed by the test/' "$script" >"$dir/copy/check540.sh"
  expect_contains "$(cat "$dir/copy/check540.sh")" "removed by the test" "mutation: the restore is really out of the copy"
  run_copy "$dir" "$dir/copy/check540.sh"
  expect_status "$status" 1 "mutation: the restore's absence"
  expect_contains "$out" "FACT restore: FAIL" "mutation: the row"
  expect_contains "$out" "the restore did not run" "mutation: what the row says"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "settings unset" "mutation: nothing was put back"
}

# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone.
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
t test_dry_run
t test_happy_path
t test_the_settings_are_written_and_put_back
t test_a_setting_already_set_is_put_back_to_its_value
t test_xtrace_refused
t test_an_agent_session_is_refused
t test_a_project_naming_belong_is_refused
t test_a_ready_pull_request_is_a_finding
t test_a_merged_pull_request_is_a_finding
t test_an_unset_judge_is_a_finding
t test_a_walk_that_never_enters_the_ladder_is_a_finding
t test_a_merge_that_comes_after_rung_3_is_not_a_false_finding
t test_a_rung_3_that_does_not_merge_is_a_finding
t test_an_approved_follow_up_is_a_finding
t test_a_clean_landing_cannot_say_whether_a_follow_up_waits
t test_a_clean_landing_can_be_expected
t test_the_settings_can_be_left_alone
t test_a_typed_write_the_fence_refuses_is_a_finding
t test_a_restore_that_did_not_happen_is_a_finding
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused
t test_a_missing_item_is_die_not_a_prompt
t test_mutation_without_the_agent_guard_the_fence_still_refuses
t test_mutation_without_the_restore_the_block_says_so

if [ "$failures" -ne 0 ]; then
  echo "check540_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check540_test.sh: ok"
