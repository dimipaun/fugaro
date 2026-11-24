#!/usr/bin/env bash
# Tests for scripts/live-checks/check640-queue-probe.sh, against fake `fugaro` and
# `go` on PATH: nothing here reaches a network or an installation. The fake
# `fugaro` keeps a revision in a file, answers `orchestrator state --json` with
# it, and on `orchestrator patch -f` records the document it was given (and the
# file's mode), checks its base_rev against the revision, and bumps it, as the
# real state does. The fake `go` prints a fixed digest and records its stdin.
#
# Usage: bash scripts/live-checks/check640-queue-probe_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check640-queue-probe.sh
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf "$work"' EXIT

failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }
expect_contains() { case "$1" in *"$2"*) ;; *) fail "$3: expected '$2' in: $(printf '%s' "$1" | head -c 600)" ;; esac; }
expect_absent() { case "$1" in *"$2"*) fail "$3: found '$2'" ;; esac; }

DIGEST=sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

bindir=$work/bin
mkdir -p "$bindir"
cat >"$bindir/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
log=$FAKE_DIR/calls
echo "$*" >>"$log"
rev=$(cat "$FAKE_DIR/rev")
case "$1 $2" in
"orchestrator state")
  if [ -n "${FAKE_NO_REV:-}" ]; then echo '{"kind":"state","source":"cache","brief":"x"}'; exit 0; fi
  printf '{"version":1,"kind":"state","id":"x","source":"node","rev":%s,"schema_version":5,"brief":"x","items":[%s]}\n' "$rev" "${FAKE_ITEMS:-}"
  ;;
"orchestrator patch")
  while [ $# -gt 0 ]; do [ "$1" = "-f" ] && file=$2; shift; done
  n=$(grep -c '^orchestrator patch' "$log")
  cp "$file" "$FAKE_DIR/patch$n.json"
  stat -f %Lp "$file" >"$FAKE_DIR/mode$n" 2>/dev/null || stat -c %a "$file" >"$FAKE_DIR/mode$n"
  [ "$n" != "${FAKE_REFUSE_PATCH:-0}" ] || { echo "refused: nope" >&2; exit 1; }
  base=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["base_rev"])' "$file")
  [ "$base" = "$rev" ] || { echo "stale revision" >&2; exit 1; }
  echo $((rev + 1)) >"$FAKE_DIR/rev"
  echo "applied (rev $((rev + 1)))"
  ;;
*) echo "unexpected fugaro call: $*" >&2; exit 9 ;;
esac
FAKE
cat >"$bindir/go" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >>"$FAKE_DIR/gocalls"
cat >"$FAKE_DIR/gostdin"
echo "${FAKE_DIGEST}"
FAKE
chmod +x "$bindir/fugaro" "$bindir/go"

# run NAME [ARGS...]: a fresh fake directory at revision 41; sets out, err, status
run() {
  local name=$1
  shift
  FAKE_DIR=$work/$name
  rm -rf "$FAKE_DIR"
  mkdir -p "$FAKE_DIR" "$FAKE_DIR/tmp"
  echo 41 >"$FAKE_DIR/rev"
  : >"$FAKE_DIR/calls"
  status=0
  out=$(cd "$work" && env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$bindir:$PATH" FAKE_DIR="$FAKE_DIR" FAKE_DIGEST="${FAKE_DIGEST:-$DIGEST}" TMPDIR="$FAKE_DIR/tmp" \
    CHECK640_PROBE_DATE=20261122 "$@" 2>"$FAKE_DIR/err.txt") || status=$?
  err=$(cat "$FAKE_DIR/err.txt")
}
calls() { cat "$FAKE_DIR/calls"; }
npatches() { grep -c '^orchestrator patch' "$FAKE_DIR/calls" || true; }

test_happy_path() {
  run happy bash "$script"
  [ "$status" = 0 ] || fail "happy: exit $status: $err"
  [ "$(npatches)" = 2 ] || fail "happy: $(npatches) patches, want 2"
  local up ap
  up=$(cat "$FAKE_DIR/patch1.json")
  ap=$(cat "$FAKE_DIR/patch2.json")
  expect_contains "$up" '"work_item_upsert"' "happy: first patch"
  expect_contains "$up" '"id": "chk640-probe-20261122"' "happy: item id"
  expect_contains "$up" '"repo": "dimipaun/fugaro"' "happy: repo"
  expect_contains "$up" '"spec": "forge-labels"' "happy: spec"
  expect_contains "$up" '"base_rev": 41' "happy: upsert base rev"
  expect_contains "$ap" '"work_item_approve"' "happy: second patch"
  expect_contains "$ap" "\"spec_digest\": \"$DIGEST\"" "happy: the digest the tool printed"
  expect_contains "$ap" '"base_rev": 42' "happy: approve base rev is the one after the upsert"
  expect_contains "$(cat "$FAKE_DIR/gostdin")" '"work_item_upsert"' "happy: the digest tool read the upsert document"
  expect_contains "$(cat "$FAKE_DIR/gocalls")" "run ./scripts/orchdigest" "happy: the tool is run with go run"
  [ "$(cat "$FAKE_DIR/mode1")" = 600 ] && [ "$(cat "$FAKE_DIR/mode2")" = 600 ] || fail "happy: patch files are not mode 0600"
  expect_contains "$out" "bash scripts/live-checks/check640.sh --installation fugaro" "happy: the next command"
  expect_contains "$out" "within a few minutes" "happy: what to expect"
  expect_contains "$(calls)" "--project fugaro" "happy: default project"
  case "$(grep '^orchestrator patch' "$FAKE_DIR/calls")" in *"--json"*) fail "happy: a patch ran with --json" ;; esac
  [ -z "$(ls -A "$FAKE_DIR/tmp")" ] || fail "happy: the scratch directory was left behind"
}

test_the_tier_clears_a_docs_line_and_the_brief_asks_for_one_line() {
  run tier bash "$script"
  local up
  up=$(cat "$FAKE_DIR/patch1.json")
  expect_contains "$up" '"rigor_tier": "small"' "tier: small, not trivial (which expects 0.25 USD)"
  expect_contains "$up" "Change exactly one line" "tier: the brief asks for one line"
}

test_a_suffix_makes_a_new_id() {
  run suffix bash "$script" --suffix b2
  [ "$status" = 0 ] || fail "suffix: exit $status: $err"
  expect_contains "$(cat "$FAKE_DIR/patch1.json")" '"id": "chk640-probe-20261122-b2"' "suffix: item id"
  expect_contains "$(cat "$FAKE_DIR/patch2.json")" '"id": "chk640-probe-20261122-b2"' "suffix: approved id"
  run suffixeq bash "$script" --suffix=c
  expect_contains "$(cat "$FAKE_DIR/patch1.json")" '"id": "chk640-probe-20261122-c"' "suffix=: item id"
}

test_a_bad_suffix_is_refused() {
  local bad
  for bad in "B" "a-b" "a_b" "abcdefghijklm" "a b"; do
    run badsuffix bash "$script" --suffix "$bad"
    [ "$status" = 2 ] || fail "suffix '$bad': exit $status, want 2"
    [ -z "$(calls)" ] || fail "suffix '$bad': fugaro was called"
  done
  run twelve bash "$script" --suffix abcdefghijkl --dry-run
  [ "$status" = 0 ] || fail "a 12-character suffix: exit $status: $err"
}

test_a_spent_id_is_refused() {
  local item='{"id":"chk640-probe-20261122-b","phase":"done","run_id":"r","attempts":1,"repo":"x"}'
  FAKE_ITEMS=$item run spent bash "$script" --suffix b
  [ "$status" = 2 ] || fail "spent suffix: exit $status, want 2"
  [ "$(npatches)" = 0 ] || fail "spent suffix: a patch was sent"
  expect_contains "$err" "is spent" "spent suffix: the reason"

  item='{"id":"chk640-probe-20261122","phase":"running","run_id":"r","attempts":1,"repo":"x"}'
  FAKE_ITEMS=$item run spentplain bash "$script"
  [ "$status" = 2 ] || fail "spent plain id: exit $status, want 2"
  expect_contains "$err" "pass --suffix" "spent plain id: says what to do"

  # An id that is there but still queued is the idempotent rerun of today.
  item='{"id":"chk640-probe-20261122","phase":"queued","attempts":0,"repo":"x"}'
  FAKE_ITEMS=$item run rerun bash "$script"
  [ "$status" = 0 ] || fail "queued rerun: exit $status: $err"

  # Another suffix is free even when the day's own id is spent.
  item='{"id":"chk640-probe-20261122","phase":"done","run_id":"r","attempts":1,"repo":"x"}'
  FAKE_ITEMS=$item run freesuffix bash "$script" --suffix b
  [ "$status" = 0 ] || fail "free suffix: exit $status: $err"
}

test_the_brief_names_no_cost_cap() {
  run brief bash "$script"
  local up
  up=$(cat "$FAKE_DIR/patch1.json")
  case "$up" in *'$'*) fail "brief: carries a dollar figure" ;; esac
  case "$(printf '%s' "$up" | tr '[:upper:]' '[:lower:]')" in *"cap"*) fail "brief: names a cap" ;; esac
  expect_contains "$up" "docs/forge-labels/implementation-plan.md" "brief: names the file"
}

test_dry_run_writes_nothing() {
  run dry bash "$script" --dry-run
  [ "$status" = 0 ] || fail "dry: exit $status: $err"
  [ "$(npatches)" = 0 ] || fail "dry: a patch was sent"
  expect_contains "$err" "$DIGEST" "dry: prints the digest"
  expect_contains "$err" "nothing was written" "dry: says so"
}

test_project_flag() {
  run proj bash "$script" --project other
  [ "$status" = 0 ] || fail "project: exit $status"
  expect_contains "$(calls)" "orchestrator patch --project other -f" "project: passed through"
}

test_an_agent_session_is_refused() {
  local m
  for m in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CURSOR_AGENT; do
    run "agent-$m" env "$m=1" bash "$script"
    [ "$status" = 2 ] || fail "agent $m: exit $status, want 2"
    [ -z "$(calls)" ] || fail "agent $m: fugaro was called"
    expect_contains "$err" "coding agent's session" "agent $m: the reason"
  done
}

test_belong_is_refused() {
  run belong bash "$script" --project belong
  [ "$status" = 2 ] || fail "belong: exit $status"
  run belong2 bash "$script" --project=BELONG
  [ "$status" = 2 ] || fail "belong upper: exit $status"
  [ -z "$(calls)" ] || fail "belong: fugaro was called"
}

test_tracing_is_refused() {
  run trace bash -x "$script"
  [ "$status" = 2 ] || fail "trace: exit $status"
  [ -z "$(calls)" ] || fail "trace: fugaro was called"
}

test_a_refused_upsert_stops_before_the_approval() {
  run refuse env FAKE_REFUSE_PATCH=1 bash "$script"
  [ "$status" = 2 ] || fail "refused upsert: exit $status"
  [ "$(npatches)" = 1 ] || fail "refused upsert: $(npatches) patches, want 1"
}

test_a_refused_approval_says_to_run_again() {
  run noapprove env FAKE_REFUSE_PATCH=2 bash "$script"
  [ "$status" = 2 ] || fail "refused approval: exit $status"
  expect_contains "$err" "queued but not approved" "refused approval: the state it left"
  expect_absent "$out" "Queued and approved" "refused approval: no success line"
}

test_a_state_without_a_revision_writes_nothing() {
  run norev env FAKE_NO_REV=1 bash "$script"
  [ "$status" = 2 ] || fail "no rev: exit $status"
  [ "$(npatches)" = 0 ] || fail "no rev: a patch was sent"
}

test_a_bad_digest_writes_nothing() {
  FAKE_DIGEST=garbage run baddigest bash "$script"
  [ "$status" = 2 ] || fail "bad digest: exit $status"
  [ "$(npatches)" = 0 ] || fail "bad digest: a patch was sent"
}

test_a_missing_tool_is_named() {
  run notool env PATH="/usr/bin:/bin" "$(command -v bash)" "$script"
  [ "$status" = 2 ] || fail "no tool: exit $status"
  expect_contains "$err" "needs" "no tool: the reason"
}

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

test_index=0
t test_happy_path
t test_the_tier_clears_a_docs_line_and_the_brief_asks_for_one_line
t test_a_suffix_makes_a_new_id
t test_a_bad_suffix_is_refused
t test_a_spent_id_is_refused
t test_the_brief_names_no_cost_cap
t test_dry_run_writes_nothing
t test_project_flag
t test_an_agent_session_is_refused
t test_belong_is_refused
t test_tracing_is_refused
t test_a_refused_upsert_stops_before_the_approval
t test_a_refused_approval_says_to_run_again
t test_a_state_without_a_revision_writes_nothing
t test_a_bad_digest_writes_nothing
t test_a_missing_tool_is_named

if [ "$failures" -ne 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "ok"
