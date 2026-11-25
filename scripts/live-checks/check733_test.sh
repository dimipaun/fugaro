#!/usr/bin/env bash
# Tests for scripts/live-checks/check733.sh against a fake fugaro on PATH:
# nothing here reaches a cloud or an installation.
#
# Usage: bash scripts/live-checks/check733_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check733.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in *"$2"*) ;; *) fail "$3: expected '$2'; got: $(printf '%s' "$1" | head -c 800)" ;; esac
}
expect_absent() { # haystack needle what
  case "$1" in *"$2"*) fail "$3: found '$2'" ;; esac
}
expect_status() { [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 400))"; }

[ -f "$script" ] || {
  echo "check733_test.sh: $script does not exist" >&2
  exit 1
}

mkdir -p "$work/bin"
cat >"$work/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# Answers `pair ls --json` from the case's own files; records every call.
set -euo pipefail
dir=${FAKE733_DIR:?}
printf '%s\n' "$*" >>"$dir/calls"
[ "$*" = "pair ls --json" ] || { echo "unexpected call: $*" >&2; exit 64; }
if [ -f "$dir/fail" ]; then echo "refused" >&2; exit 3; fi
cat "$dir/pair.json"
FUGARO_EOF
chmod +x "$work/bin/fugaro"

EVENT='{"v":1,"event":"listed","list":{"project":"p","devices":[{"uid":"u1","name":"Dimi iPhone","email":"a@example.test","role":"admin","at":"2026-11-01T00:00:00Z"},{"uid":"u2","name":"Dimi Pixel","email":"a@example.test","role":"admin","at":"2026-11-02T00:00:00Z"}],"warnings":[]}}'
ONE='{"v":1,"event":"listed","list":{"project":"p","devices":[{"uid":"u1","name":"Only Phone","email":"a@example.test","role":"admin","at":"t"}],"warnings":[]}}'
NONE='{"v":1,"event":"listed","list":{"project":"p","devices":[],"warnings":[]}}'

run_case() { # name json [env...]
  local name=$1 json=$2
  shift 2
  cdir=$work/$name
  mkdir -p "$cdir"
  printf '%s' "$json" >"$cdir/pair.json"
  : >"$cdir/calls"
  set +e
  out=$(cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE733_DIR="$cdir" "$@" bash "$script" 2>"$cdir/err")
  status_code=$?
  set -e
  err=$(cat "$cdir/err")
}

test_agent_sessions_are_refused() {
  local m
  for m in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    run_case "agent-$m" "$EVENT" "$m=1"
    expect_status "$status_code" 2 "$m refused"
    expect_contains "$err" "coding agent's session" "$m message"
    [ ! -s "$cdir/calls" ] || fail "$m: fugaro ran in an agent session"
    [ -z "$(ls "$cdir"/*DRAFT* 2>/dev/null)" ] || fail "$m: a draft was written"
  done
}

test_two_devices_are_named_and_the_tables_are_unverified() {
  run_case two "$EVENT"
  expect_status "$status_code" 0 "two devices"
  expect_contains "$out" "paired devices: 2" "count"
  expect_contains "$out" "Draft written: ./app-spike-DRAFT.md" "draft named"
  expect_contains "$out" "send that file back" "next step"
  [ "$(cat "$cdir/calls")" = "pair ls --json" ] || fail "fugaro was called with something else: $(cat "$cdir/calls")"
  local d; d=$(cat "$cdir/app-spike-DRAFT.md")
  expect_contains "$d" "Devices paired: 2" "count in draft"
  expect_contains "$d" "Dimi iPhone" "first device"
  expect_contains "$d" "Dimi Pixel" "second device"
  expect_contains "$d" "## iPhone" "iPhone table"
  expect_contains "$d" "## Android" "Android table"
  local item
  for item in U4 U5 U7 U10; do expect_contains "$d" "| $item |" "$item row"; done
  expect_contains "$d" "UNVERIFIED" "unverified marks"
  expect_contains "$d" "Share extension" "iOS share row"
  expect_contains "$d" "Intent filter" "Android share row"
  expect_absent "$d" "| U4 | Google token accepted by \`signInWithIdp\`? Refusal if not | OK" "no answer is pre-filled"
  [ -z "$(ls "$cdir"/check733.* 2>/dev/null)" ] || fail "the scratch directory was left behind"
}

test_one_and_no_devices_are_recorded_not_refused() {
  run_case one "$ONE"
  expect_status "$status_code" 0 "one device"
  expect_contains "$out" "paired devices: 1" "count one"
  expect_contains "$(cat "$cdir/app-spike-DRAFT.md")" "Only Phone" "named"
  run_case none "$NONE"
  expect_status "$status_code" 0 "no device"
  expect_contains "$out" "paired devices: 0" "count zero"
  expect_contains "$(cat "$cdir/app-spike-DRAFT.md")" "Devices paired: 0" "draft count zero"
}

test_a_bare_array_is_read_too() {
  run_case bare '[{"uid":"u9","name":"Bare Phone","role":"admin","email":"e","at":"t"}]'
  expect_status "$status_code" 0 "bare array"
  expect_contains "$out" "paired devices: 1" "count bare"
}

test_an_unreadable_listing_writes_the_draft_and_exits_1() {
  cdir=$work/failcase
  mkdir -p "$cdir"
  : >"$cdir/fail"
  : >"$cdir/calls"
  set +e
  out=$(cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE733_DIR="$cdir" bash "$script" 2>"$cdir/err")
  status_code=$?
  set -e
  err=$(cat "$cdir/err")
  expect_status "$status_code" 1 "a failed read"
  expect_contains "$out" "could not be read" "says so"
  expect_contains "$(cat "$cdir/app-spike-DRAFT.md")" "The read failed" "draft says so"
}

test_the_draft_is_never_overwritten() {
  run_case keep "$EVENT"
  echo precious >"$cdir/app-spike-DRAFT.md"
  (cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE733_DIR="$cdir" bash "$script" >/dev/null 2>&1)
  [ "$(cat "$cdir/app-spike-DRAFT.md")" = precious ] || fail "the default draft was overwritten"
  [ -f "$cdir/app-spike-DRAFT-2.md" ] || fail "the next free name was not used"
  echo precious >"$cdir/mine.md"
  set +e
  err=$(cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE733_DIR="$cdir" bash "$script" --out mine.md 2>&1 >/dev/null)
  status_code=$?
  set -e
  expect_status "$status_code" 2 "explicit --out that exists"
  [ "$(cat "$cdir/mine.md")" = precious ] || fail "--out file was overwritten"
}

test_agent_sessions_are_refused
test_two_devices_are_named_and_the_tables_are_unverified
test_one_and_no_devices_are_recorded_not_refused
test_a_bare_array_is_read_too
test_an_unreadable_listing_writes_the_draft_and_exits_1
test_the_draft_is_never_overwritten

if [ "$failures" -ne 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "check733_test.sh: all cases passed"
