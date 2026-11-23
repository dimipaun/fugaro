#!/usr/bin/env bash
# Tests for scripts/live-checks/check-891-update-trace.sh. Nothing here reaches
# a cloud, a project or an installation: the check is driven against a fake
# `fugaro`, `gcloud` and `curl` on PATH whose answers each case owns, and every
# fake records its whole argv, the directory it ran in and what its stdin was.
#
# The feed the fake `fugaro update` answers with is the real shape of
# `fugaro update --json --events` (internal/cli/setup_events.go): `stage` lines
# carrying name, state, detail, after and undone, and a last `done` or `error`
# line. A `launch.json` is runstore.Launch's own shape, stored at
# runs/<slug>/<run-id>/launch.json in the runs bucket.
#
# Every case runs the check with its stdin closed and under a timeout, so a
# check that waits for anything fails here instead of hanging the suite.
#
# Usage: bash scripts/live-checks/check-891-update-trace_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-891-update-trace.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK891_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

TIMEOUT_BIN=$(command -v timeout || command -v gtimeout || true)
DEADLINE=${CHECK891_TEST_DEADLINE:-120}

# Built at run time so no fixture holds a literal that looks like a credential;
# each is plainly fake and still has the opaque, mixed-case shape the check
# has to keep out of its output.
FAKE_SECRET="Fake${RANDOM}Value$(printf '%05d' 891)_Not_A_Real_One"
FAKE_TOKEN="Fake${RANDOM}Access$(printf '%05d' 891)_Token_For_Tests"

rand_from() { # rand_from <tr-set> <length>
  LC_ALL=C tr -dc "$1" </dev/urandom | head -c "$2" || true
}

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK891_TEST_HEAD:-2400}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "${err:-}" | tail -c 600))"
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
mkdir -p "$fakebin"

# stdin_kind reads the fake's own stdin the way Check 380's fake does: EOF at
# once is a closed stdin, a read that times out is one left open.
cat >"$fakebin/stdin_kind" <<'FAKE'
kind=closed
if IFS= read -r -t 1 _line; then
  kind=line
else
  rc=$?
  if [ "$rc" -gt 128 ]; then kind=open; fi
fi
FAKE

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/stdin_kind"
printf 'fugaro %s (in %s) (stdin=%s)\n' "$*" "$PWD" "$kind" >>"${FAKE891_LOG:?}"
dir=${FAKE891_DIR:?}
case "${1:-}" in
update)
  place=empty
  if [ -f "$PWD/fugaro.yaml" ]; then place=$(basename "$PWD"); fi
  printf 'fake fugaro: planning (%s)\n' "$place" >&2
  cat "$dir/feed-$place.ndjson"
  exit "$(cat "$dir/feed-$place.status" 2>/dev/null || printf 0)"
  ;;
doctor)
  cat "$dir/doctor.json"
  exit "$(cat "$dir/doctor.status" 2>/dev/null || printf 0)"
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE

cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/stdin_kind"
printf 'gcloud %s (stdin=%s)\n' "$*" "$kind" >>"${FAKE891_LOG:?}"
dir=${FAKE891_DIR:?}
if [ "${1:-}" = auth ] && [ "${2:-}" = print-access-token ]; then
  printf '%s\n' "${FAKE891_TOKEN:?}"
  exit 0
fi
if [ "${1:-}" = storage ] && [ -n "${FAKE891_LOGIN_EXPIRED:-}" ]; then
  printf 'ERROR: (gcloud.storage.%s) There was a problem refreshing your current auth tokens: Reauthentication failed. cannot prompt during non-interactive execution.\n' "${2:-}" >&2
  exit 1
fi
if [ "${1:-}" = storage ]; then
  url=${3:-}
  key=${url#gs://*/}
  case "${2:-}" in
  ls)
    file="$dir/ls/$(printf '%s' "$key" | tr '/' '_')"
    if [ -f "$file" ]; then
      bucket=${url#gs://}
      bucket=${bucket%%/*}
      while IFS= read -r entry; do
        [ -n "$entry" ] && printf 'gs://%s/%s%s\n' "$bucket" "$key" "$entry"
      done <"$file"
      exit 0
    fi
    printf 'ERROR: (gcloud.storage.ls) One or more URLs matched no objects.\n' >&2
    exit 1
    ;;
  cat)
    file="$dir/obj/$(printf '%s' "$key" | tr '/' '_')"
    if [ -f "$file" ]; then cat "$file"; exit 0; fi
    printf 'ERROR: (gcloud.storage.cat) The following URLs matched no objects or files: %s\n' "$url" >&2
    exit 1
    ;;
  esac
fi
printf 'fake gcloud: unexpected call %s\n' "$*" >&2
exit 64
FAKE

cat >"$fakebin/curl" <<'FAKE'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >>"${FAKE891_LOG:?}"
exit 7
FAKE
chmod +x "$fakebin/fugaro" "$fakebin/gcloud" "$fakebin/curl"

# --------------------------------------------------------------- the fixtures

# stage_line prints one `stage` event exactly as setup_events.go encodes it.
stage_line() { # stage_line <name> <state> <detail>
  python3 - "$1" "$2" "$3" <<'PY'
import json, sys
print(json.dumps({"type": "stage", "name": sys.argv[1], "state": sys.argv[2],
                  "detail": sys.argv[3], "after": "", "undone": False},
                 separators=(",", ":")))
PY
}

# base_feed writes the stages every place shares; a case then bends a place.
base_feed() { # base_feed <out> <repository-state> <repository-detail> [preflight-detail] [secrets-state] [secrets-detail]
  {
    printf '{"type":"step","stage":"preflight","name":"preflight","detail":""}\n'
    stage_line preflight "done" "${4:-gcloud, terraform and the credentials are in place}"
    stage_line installation "done" "No changes"
    stage_line firebase "done" "No changes"
    stage_line images "done" "the base image is pinned"
    stage_line plugin "done" "the plugin is installed at user scope"
    stage_line secrets "${5:-skipped}" "${6:-not in a checkout of a repository: run fugaro update there to take its secrets}"
    stage_line repository "$2" "$3"
    stage_line policy "done" "the published policy is this release's"
    stage_line checkout skipped "not in a checkout: the checkout's job images are refreshed where the checkout is"
    printf '{"type":"done","exit_code":0}\n'
  } >"$1"
}

launch_json() { # launch_json <out> <run-id> <job> [launched_by]
  python3 - "$1" "$2" "$3" "${4:-owner@example.invalid}" <<'PY'
import json, sys
out, run, job, by = sys.argv[1:5]
doc = {"version": 1, "run_id": run, "backend": "cloud-run",
       "execution": "projects/sbx-proj/locations/us-east5/jobs/%s/executions/%s-x" % (job, job),
       "job": job, "log_url": "https://console.invalid/logs?run=" + run,
       "launched_by": by, "launched_at": "2026-11-22T10:00:00Z"}
open(out, "w").write(json.dumps(doc, indent=2))
PY
}

# case_dir builds one case's world: two checkouts of project `test`, the feeds
# of the empty directory and of each checkout, doctor's answer, and a runs
# bucket with two repositories. The newest run of the first has no launch.json,
# so the check has to step back to the one before it.
case_dir() { # case_dir <name>
  root=$work/$1
  answers=$root/answers
  mkdir -p "$answers/ls" "$answers/obj"
  CALLS=$answers/calls
  : >"$CALLS"
  sub=$root/sub
  other=$root/other
  mkdir -p "$sub" "$other"
  printf 'version: 1\nproject: test\n' >"$sub/fugaro.yaml"
  printf "version: 1\nproject: 'test' # this project\n" >"$other/fugaro.yaml"
  base_feed "$answers/feed-empty.ndjson" skipped "not in a checkout of this project's repositories"
  base_feed "$answers/feed-sub.ndjson" will-do "would apply the repository root of acme/sub"
  base_feed "$answers/feed-other.ndjson" will-do "would apply the repository root of acme/other"
  printf '{"ok":true,"project":{"name":"test","gcp_project":"sbx-proj","region":"us-east5"}}\n' >"$answers/doctor.json"
  printf 'acme-sub-1a2b3c/\nacme-other-4d5e6f/\n' >"$answers/ls/runs_"
  printf '20261120-090000-aaaa/\n20261122-100000-bbbb/\n20261123-080000-cccc/\nindex.json\n' >"$answers/ls/runs_acme-sub-1a2b3c_"
  printf '20261121-120000-dddd/\n' >"$answers/ls/runs_acme-other-4d5e6f_"
  launch_json "$answers/obj/runs_acme-sub-1a2b3c_20261122-100000-bbbb_launch.json" 20261122-100000-bbbb fugaro-p-default-github-team-0a1b2c3d
  launch_json "$answers/obj/runs_acme-other-4d5e6f_20261121-120000-dddd_launch.json" 20261121-120000-dddd fugaro-acme-other-coder-9f8e7d6c
}

# ------------------------------------------------------------------ the runs

run891() { # run891 <args...>
  status=0
  if [ -n "$TIMEOUT_BIN" ]; then
    out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
      -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
      PATH="$fakebin:$PATH" FAKE891_LOG="$CALLS" FAKE891_DIR="$answers" FAKE891_TOKEN="$FAKE_TOKEN" \
      HOME="$root/home" TMPDIR="$root" \
      "$TIMEOUT_BIN" "$DEADLINE" bash "$script" "$@" </dev/null 2>"$root/err") || status=$?
  else
    out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
      -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
      PATH="$fakebin:$PATH" FAKE891_LOG="$CALLS" FAKE891_DIR="$answers" FAKE891_TOKEN="$FAKE_TOKEN" \
      HOME="$root/home" TMPDIR="$root" \
      bash "$script" "$@" </dev/null 2>"$root/err") || status=$?
  fi
  err=$(cat "$root/err" 2>/dev/null || true)
  calls=$(cat "$CALLS" 2>/dev/null || true)
}

update_calls() { printf '%s\n' "$calls" | grep '^fugaro update' || true; }

# ------------------------------------------------------------------- the cases

test_matching_stages_pass() {
  case_dir matching
  run891 test "$sub" "$other"
  [ -z "${CHECK891_TEST_SHOW:-}" ] || printf "%s\n" "$out" >&2
  expect_status "$status" 0 "matching stages"
  expect_contains "$out" "CHECK 891 RESULT: PASS (exit 0)" "matching stages"
  expect_contains "$out" "repository" "matching stages: the differing stage is printed"
  expect_contains "$out" "skipped" "matching stages: the empty directory's state"
  expect_contains "$out" "will-do" "matching stages: the checkout's state"
  expect_contains "$out" "DIFFERS, a blocker of report.md §4" "matching stages: the repository stage is marked"
  expect_contains "$out" "RC8" "matching stages: the blocker's rows are named"
  expect_contains "$out" "fugaro-p-default-github-team-0a1b2c3d" "matching stages: the shared job of acme-sub"
  expect_contains "$out" "20261122-100000-bbbb" "matching stages: the newest run with a launch.json"
  expect_contains "$out" "fugaro-acme-other-coder-9f8e7d6c" "matching stages: the per-repository job of acme-other"
  expect_contains "$out" "RC76" "matching stages: the per-repository launch names its row"
  expect_not_contains "$out" "NOT A BLOCKER" "matching stages"
}

test_an_unlisted_differing_stage_fails() {
  case_dir unlisted
  base_feed "$answers/feed-sub.ndjson" will-do "would apply the repository root of acme/sub" \
    "gcloud, terraform and the credentials are in place; the checkout's account is used"
  run891 test "$sub" "$other"
  expect_status "$status" 1 "an unlisted differing stage"
  expect_contains "$out" "CHECK 891 RESULT: FAIL (exit 1)" "an unlisted differing stage"
  expect_contains "$out" "DIFFERS, NOT A BLOCKER" "an unlisted differing stage: the mark"
  expect_contains "$out" "preflight" "an unlisted differing stage: named"
}

test_plan_only_is_always_passed() {
  case_dir plan_only
  run891 test "$sub" "$other"
  local n line
  n=$(update_calls | grep -c . || true)
  [ "${n:-0}" -eq 3 ] || fail "plan-only: expected 3 update calls (the empty directory and two checkouts), got ${n:-0}: $calls"
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    expect_contains "$line" " --plan-only " "plan-only: every update"
    expect_contains "$line" " --json " "plan-only: every update is --json"
    expect_contains "$line" " --events " "plan-only: every update is --events"
  done <<<"$(update_calls)"
}

test_project_is_always_passed() {
  case_dir project
  run891 test "$sub" "$other"
  local line n
  n=$(update_calls | grep -c . || true)
  [ "${n:-0}" -gt 0 ] || fail "project: no update was run at all"
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    expect_contains "$line" "--project test " "project: every update names the project"
  done <<<"$(update_calls)"
  expect_contains "$(printf '%s\n' "$calls" | grep '^fugaro doctor' || true)" "--project test" "project: doctor names the project"
  expect_contains "$(update_calls)" "(in $sub)" "project: the update ran in the checkout"
}

test_a_checkout_of_another_project_is_skipped() {
  case_dir another
  printf 'version: 1\nproject: elsewhere\n' >"$other/fugaro.yaml"
  run891 test "$sub" "$other"
  expect_status "$status" 0 "another project's checkout"
  expect_not_contains "$(update_calls)" "(in $other)" "another project's checkout: no update ran in it"
  expect_contains "$out" "elsewhere" "another project's checkout: the project it names is reported"
  expect_contains "$out" "skipped" "another project's checkout: reported as skipped"
}

test_only_reads_are_called() {
  case_dir reads
  run891 test "$sub" "$other"
  local line
  [ -n "$calls" ] || fail "reads: nothing was called"
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    "fugaro update --plan-only --json --events --project test "*) ;;
    "fugaro doctor --json --project test "*) ;;
    "gcloud storage ls gs://"*" --quiet "*) ;;
    "gcloud storage cat gs://"*"/launch.json "*"--quiet "*) ;;
    *) fail "reads: a call outside the check's call set: $line" ;;
    esac
  done <<<"$calls"
  expect_not_contains "$calls" "curl " "reads: no curl at all"
}

test_stdin_is_closed() {
  case_dir stdin
  status=0
  # The check is handed an OPEN stdin here, so a call it does not close itself
  # would read it as open.
  out=$({ sleep 10; } | env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    PATH="$fakebin:$PATH" FAKE891_LOG="$CALLS" FAKE891_DIR="$answers" FAKE891_TOKEN="$FAKE_TOKEN" \
    HOME="$root/home" TMPDIR="$root" \
    ${TIMEOUT_BIN:+"$TIMEOUT_BIN" "$DEADLINE"} bash "$script" test "$sub" 2>"$root/err") || status=$?
  calls=$(cat "$CALLS")
  [ -n "$calls" ] || fail "stdin: nothing was called"
  expect_not_contains "$calls" "(stdin=open)" "stdin: a call inherited the open stdin"
  expect_not_contains "$calls" "(stdin=line)" "stdin: a call read a line"
  expect_status "$status" 0 "stdin"
}

test_a_differing_secrets_stage_passes() {
  case_dir secrets
  base_feed "$answers/feed-sub.ndjson" will-do "would apply the repository root of acme/sub" "" \
    will-do "would create the secret container of acme/sub"
  run891 test "$sub"
  expect_status "$status" 0 "a differing secrets stage"
  expect_contains "$out" "CHECK 891 RESULT: PASS (exit 0)" "a differing secrets stage"
  expect_contains "$out" "RC98" "a differing secrets stage: its row is named"
  expect_not_contains "$out" "NOT A BLOCKER" "a differing secrets stage"
}

test_values_and_tokens_are_never_printed() {
  case_dir values
  # Lower-case credentials have no case to give them away: a forge-style
  # prefixed token, a long plain run and a short hex key.
  local ghp lower32 hex26 digest
  ghp="ghp_$(rand_from 'a-z0-9' 40)"
  lower32=$(rand_from 'a-z' 32)
  hex26=$(rand_from '0-9a-f' 26)
  digest="sha256:$(rand_from '0-9a-f' 64)"
  base_feed "$answers/feed-empty.ndjson" skipped "not in a checkout; token=$FAKE_SECRET; base $digest in gs://fugaro-runs-sbx-proj/runs/"
  base_feed "$answers/feed-sub.ndjson" will-do "would apply the repository root, holding $FAKE_SECRET $ghp $lower32 key $hex26; base $digest in gs://fugaro-runs-sbx-proj/runs/"
  launch_json "$answers/obj/runs_acme-sub-1a2b3c_20261122-100000-bbbb_launch.json" 20261122-100000-bbbb \
    fugaro-p-default-github-team-0a1b2c3d "$FAKE_SECRET"
  run891 test "$sub"
  expect_status "$status" 0 "values"
  expect_contains "$out" "repository" "values: the stage is still printed"
  expect_contains "$out" "fugaro-p-default-github-team-0a1b2c3d" "values: the job is still printed"
  expect_not_contains "$out" "$FAKE_SECRET" "values: stdout"
  expect_not_contains "$err" "$FAKE_SECRET" "values: stderr"
  expect_not_contains "$out" "$FAKE_TOKEN" "values: the token on stdout"
  expect_not_contains "$err" "$FAKE_TOKEN" "values: the token on stderr"
  expect_not_contains "$out" "owner@example.invalid" "values: launch.json's other fields"
  expect_not_contains "$out" "$ghp" "values: a lower-case prefixed token"
  expect_not_contains "$out" "$lower32" "values: a long lower-case run"
  expect_not_contains "$out" "$hex26" "values: a 26-character hex key"
  expect_contains "$out" "$digest" "values: an image digest is kept"
  expect_contains "$out" "gs://fugaro-runs-sbx-proj/runs/" "values: a bucket path is kept"
}

test_the_token_is_never_on_argv() {
  case_dir token
  run891 test "$sub" "$other"
  [ -n "$calls" ] || fail "token: nothing was called"
  expect_not_contains "$calls" "$FAKE_TOKEN" "token: a recorded argv"
  expect_not_contains "$calls" "Authorization" "token: a header on a recorded argv"
}

# ------------------------------------------------------------------- the footer

test_an_expired_gcloud_login_is_named() {
  case_dir login_expired
  export FAKE891_LOGIN_EXPIRED=1
  run891 test "$sub" "$other"
  unset FAKE891_LOGIN_EXPIRED
  expect_status "$status" 3 "an expired gcloud login"
  expect_contains "$out" "gcloud storage ls gs://fugaro-runs-sbx-proj/runs/ failed (exit 1)" "an expired gcloud login: the listing's failure is named"
  expect_contains "$out" "Reauthentication failed" "an expired gcloud login: gcloud's own message"
  expect_not_contains "$out" "listed no repository" "an expired gcloud login: not read as an empty bucket"
}

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK891_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK891_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_matching_stages_pass
t test_an_unlisted_differing_stage_fails
t test_plan_only_is_always_passed
t test_project_is_always_passed
t test_a_checkout_of_another_project_is_skipped
t test_only_reads_are_called
t test_stdin_is_closed
t test_values_and_tokens_are_never_printed
t test_a_differing_secrets_stage_passes
t test_the_token_is_never_on_argv
t test_an_expired_gcloud_login_is_named

if [ "$failures" -ne 0 ]; then
  echo "check-891-update-trace_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-891-update-trace_test.sh: ok"
