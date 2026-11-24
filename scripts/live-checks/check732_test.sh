#!/usr/bin/env bash
# Tests for scripts/live-checks/check732.sh against fake curl on PATH: nothing
# here reaches the network or reads a real token. The fake curl records its argv
# and the config file it was handed, so the cases can assert that the token never
# appears on a command line, in the output or in the draft.
#
# Usage: bash scripts/live-checks/check732_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check732.sh
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
  echo "check732_test.sh: $script does not exist" >&2
  exit 1
}

TOKEN=tok-for-the-test-0123456789
mkdir -p "$work/bin"
cat >"$work/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# Records argv and the --config file's content; answers the case's status/body.
set -euo pipefail
dir=${FAKE732_DIR:?}
printf '%s\n' "$*" >>"$dir/argv"
cfg="" outf="" want_code=no
while [ $# -gt 0 ]; do
  case "$1" in
  --config) cfg=$2; shift 2 ;;
  -o) outf=$2; shift 2 ;;
  -w) want_code=yes; shift 2 ;;
  *) shift ;;
  esac
done
[ -z "$cfg" ] || cat "$cfg" >>"$dir/config-seen"
[ -z "$outf" ] || cat "$dir/body" >"$outf"
if [ "$want_code" = yes ]; then cat "$dir/status"; fi
CURL_EOF
chmod +x "$work/bin/curl"

# run_case NAME STATUS BODY [env...]: runs the script in a fresh directory.
run_case() {
  local name=$1 status=$2 body=$3
  shift 3
  cdir=$work/$name
  mkdir -p "$cdir"
  printf '%s' "$status" >"$cdir/status"
  printf '%s' "$body" >"$cdir/body"
  : >"$cdir/argv"
  : >"$cdir/config-seen"
  set +e
  out=$(cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE732_DIR="$cdir" "$@" bash "$script" 2>"$cdir/err")
  status_code=$?
  set -e
  err=$(cat "$cdir/err")
}

BOTH='{"five_hour":{"utilization":12},"seven_day":{"utilization":3},"x":1}'

test_agent_sessions_are_refused() {
  local m
  for m in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    run_case "agent-$m" 200 "$BOTH" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN "$m=1"
    expect_status "$status_code" 2 "$m refused"
    expect_contains "$err" "coding agent's session" "$m message"
    [ ! -s "$cdir/argv" ] || fail "$m: curl ran in an agent session"
    [ -z "$(ls "$cdir"/*DRAFT* 2>/dev/null)" ] || fail "$m: a draft was written"
  done
}

test_a_missing_token_is_refused() {
  run_case notoken 200 "$BOTH"
  expect_status "$status_code" 2 "no token"
  expect_contains "$err" "CLAUDE_CODE_OAUTH_TOKEN is not set" "names the variable"
  [ ! -s "$cdir/argv" ] || fail "curl ran with no token"
}

test_the_happy_path_fills_the_windows_and_hides_the_token() {
  run_case ok 200 "$BOTH" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  expect_status "$status_code" 0 "happy path"
  expect_contains "$out" '{"five_hour":{"utilization":12},"seven_day":{"utilization":3}}' "windows printed"
  expect_contains "$out" "Draft written: ./context-spike-DRAFT.md" "names the draft"
  expect_contains "$out" "send that file back" "next step"
  local draft; draft=$(cat "$cdir/context-spike-DRAFT.md")
  expect_contains "$draft" "readable by this route" "draft result"
  expect_contains "$draft" "OWNER: FILL IN" "owner fields"
  expect_contains "$draft" "## U1" "U1 section"
  expect_contains "$draft" "## U2" "U2 section"
  local all=$out$err$draft$(cat "$cdir/argv")
  expect_absent "$all" "$TOKEN" "the token never appears in output, draft or argv"
  expect_contains "$(cat "$cdir/config-seen")" "$TOKEN" "the token reached curl through the config file"
  expect_contains "$(cat "$cdir/argv")" "--config" "config file used"
  [ -z "$(ls "$cdir"/check732.* 2>/dev/null)" ] || fail "the scratch directory was left behind"
}

test_401_and_403_are_a_finding_not_a_failure() {
  local code
  for code in 401 403; do
    run_case "http$code" "$code" '{"error":"nope"}' CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
    expect_status "$status_code" 0 "$code is not a failure"
    expect_contains "$out" "not readable by this route (HTTP $code)" "$code recorded"
    expect_contains "$(cat "$cdir/context-spike-DRAFT.md")" "not readable by this route" "$code in draft"
    expect_absent "$out$err$(cat "$cdir/context-spike-DRAFT.md")" "$TOKEN" "$code: no token"
  done
}

test_a_missing_window_is_not_readable() {
  run_case missing 200 '{"five_hour":{"utilization":1}}' CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  expect_status "$status_code" 0 "missing window"
  expect_contains "$out" "not readable by this route (HTTP 200)" "missing seven_day"
  run_case nulls 200 '{"five_hour":null,"seven_day":null}' CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  expect_contains "$out" "not readable by this route" "null windows"
}

test_the_draft_is_never_overwritten() {
  run_case keep 200 "$BOTH" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  echo precious >"$cdir/context-spike-DRAFT.md"
  (cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE732_DIR="$cdir" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN bash "$script" >/dev/null 2>&1)
  [ "$(cat "$cdir/context-spike-DRAFT.md")" = precious ] || fail "the default draft was overwritten"
  [ -f "$cdir/context-spike-DRAFT-2.md" ] || fail "the next free name was not used"
  run_case explicit 200 "$BOTH" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  echo precious >"$cdir/mine.md"
  set +e
  err=$(cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE732_DIR="$cdir" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN bash "$script" --out mine.md 2>&1 >/dev/null)
  status_code=$?
  set -e
  expect_status "$status_code" 2 "explicit --out that exists"
  expect_contains "$err" "never overwrites" "message"
  [ "$(cat "$cdir/mine.md")" = precious ] || fail "--out file was overwritten"
}

test_out_writes_where_asked() {
  run_case outpath 200 "$BOTH" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  rm -f "$cdir"/context-spike-DRAFT*.md
  (cd "$cdir" && env -i HOME="$cdir" TMPDIR="$cdir" PATH="$work/bin:/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" FAKE732_DIR="$cdir" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN bash "$script" --out "$cdir/sub.md" >/dev/null 2>&1)
  [ -f "$cdir/sub.md" ] || fail "--out was not honoured"
}

test_an_unknown_argument_is_refused() {
  run_case badarg 200 "$BOTH" CLAUDE_CODE_OAUTH_TOKEN=$TOKEN
  set +e
  err=$(cd "$cdir" && env -i PATH="$work/bin:/usr/bin:/bin" bash "$script" --nope 2>&1 >/dev/null)
  status_code=$?
  set -e
  expect_status "$status_code" 2 "unknown argument"
}

test_agent_sessions_are_refused
test_a_missing_token_is_refused
test_the_happy_path_fills_the_windows_and_hides_the_token
test_401_and_403_are_a_finding_not_a_failure
test_a_missing_window_is_not_readable
test_the_draft_is_never_overwritten
test_out_writes_where_asked
test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "check732_test.sh: all cases passed"
