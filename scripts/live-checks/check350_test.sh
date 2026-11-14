#!/usr/bin/env bash
# Tests for scripts/live-checks/check350.sh, with a fake `gcloud` and a fake
# `fugaro` on PATH and a REAL, local git repository beside them: NOTHING here
# reaches a cloud, a project, a bucket or a run, no agent runs a live step, and
# both fakes record every call they are given in ONE shared log, so a case can
# say what the check read and — the reading step 4 is about — in what ORDER. The
# git side is real and stays on this machine: a checkout and a bare origin in the
# case's own temporary directory, so the throwaway reference really is cut,
# pushed, deleted and read back by the script under test and by nothing else.
#
# The fakes are a small model of one sandbox installation: the fake `fugaro`
# answers the launch, the two listings, the week's report and the diagnoses, and
# the fake `gcloud` serves the run records and the bucket's listings. The cases
# that matter are the ones where that model is bent, because each one is a
# sentence of the check that would otherwise read as an answer: a run that never
# ends (the check has to cancel it), a record that never appears, a class other
# than config_invalid, a row and a diagnosis that disagree, groups that do not
# add up, a share that follows the spend, totals that move between the two
# listings, a week whose report carries no Failures table, a window with no old
# record in it, a record whose BYTES change between the two readings of step 4, a
# record whose bucket line moves while its bytes do not, a refutation run under
# another class, and a Restore that cannot delete the reference it made.
#
# Two cases are about the test rather than the check: one rewrites a copy of the
# script with step 4's comparison removed, to prove the FAIL that case reads is
# the comparison's and not a coincidence of the fixture, and one reads the shared
# call log to prove the hash of the old record is taken BEFORE the first `fugaro`
# read of the window.
#
# Every command is bounded (see `bounded` below), so a check that hangs fails
# here with the case's own name instead of hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check350_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check350.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK350_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK350_TEST_HEAD:-900}")" ;;
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

# Every command this file runs is bounded, and so is every case: an earlier
# live-check test (check340_test.sh) hung for 25 minutes, and the suite that runs
# this file has one deadline for all of them. `timeout` is coreutils and is not on
# a stock Mac, so the fallback is a watchdog of the same shape.
TIMEOUT_BIN=""
for candidate in timeout gtimeout; do
  if command -v "$candidate" >/dev/null 2>&1; then
    TIMEOUT_BIN=$candidate
    break
  fi
done
CASE_TIMEOUT=${CHECK350_TEST_TIMEOUT:-90}

bounded() { # bounded CMD...
  local rc=0
  if [ -n "$TIMEOUT_BIN" ]; then
    "$TIMEOUT_BIN" "$CASE_TIMEOUT" "$@" || rc=$?
    return "$rc"
  fi
  "$@" &
  local pid=$!
  ( sleep "$CASE_TIMEOUT"; kill -TERM "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true
  return "$rc"
}

# ------------------------------------------------------------------- the world
project=chk350-project
bucket=fugaro-runs-chk350
repo_name=acme/sandbox
slug=acme-sandbox-9f8e7d6c

# The week steps 3 and 4 are about is computed the way the check computes it —
# the last CLOSED week, Monday to Sunday, UTC — because the fixture has to put a
# record inside it whatever day this test runs on. The stable run of a run id is
# its first fifteen characters, which is what `runs ls --since` windows on.
dates=$(python3 - <<'PY'
import datetime
now = datetime.datetime.now(datetime.timezone.utc)
today = now.date()
monday = today - datetime.timedelta(days=today.weekday())
last = monday - datetime.timedelta(days=7)
sun = monday - datetime.timedelta(days=1)
iso = last.isocalendar()
print(last.isoformat())
print(sun.isoformat())
print("%s-W%02d" % (iso[0], iso[1]))
print(last.strftime("%Y%m%d-000000"))
print((last + datetime.timedelta(days=1)).isoformat() + "T10:00:00Z")
print((last + datetime.timedelta(days=3)).strftime("%Y%m%d-000000"))
print((last - datetime.timedelta(days=3)).isoformat() + "T10:00:00Z")
print((last - datetime.timedelta(days=14)).strftime("%Y%m%d-000000"))
print(today.strftime("%Y%m%d-130000"))
print(now.strftime("%Y-%m-%dT%H:%M:%SZ"))
PY
)
week_from=$(printf '%s\n' "$dates" | sed -n 1p)
week_last=$(printf '%s\n' "$dates" | sed -n 2p)
week_key=$(printf '%s\n' "$dates" | sed -n 3p)
target_stamp=$(printf '%s\n' "$dates" | sed -n 4p)
target_finished=$(printf '%s\n' "$dates" | sed -n 5p)
neighbour_stamp=$(printf '%s\n' "$dates" | sed -n 6p)
neighbour_finished=$(printf '%s\n' "$dates" | sed -n 7p)
old_stamp=$(printf '%s\n' "$dates" | sed -n 8p)
our_stamp=$(printf '%s\n' "$dates" | sed -n 9p)
now_stamp=$(printf '%s\n' "$dates" | sed -n 10p)

# The run this check launches. Its own stamp is TODAY, which is at or after the
# Monday the window closes at, so the check can never pick it as the old record.
our_run="$our_stamp-aa"
target_run="$target_stamp-tt"
neighbour_run="$neighbour_stamp-nn"
old_run="$old_stamp-oo"
ok_run="$old_stamp-ok"
step5_run="$our_stamp-55"

refusal="bootstrap: the repository config is not valid: fugaro.yaml: unknown field chk350_invalid_key"

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check350.sh's test only. It
# serves the run records out of the case's own directory (one file per run id),
# the recursive listing of the repository's records, and the bucket's own line
# for one object. Every call is appended to the shared log with the tool's name,
# so a case can read the ORDER the check made its reads in.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/all.calls"
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
if [ "${1:-}" != storage ]; then
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
fi
action=${2:-}
shift 2 || true
url=""
recursive=no
for a in "$@"; do
  case "$a" in
  --recursive) recursive=yes ;;
  gs://*) url=$a ;;
  esac
done
run_of() { basename "$(dirname "$1")"; }
case "$action" in
cat)
  [ "$mode" = record-missing ] && { echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2; exit 1; }
  rid=$(run_of "$url")
  if [ "$mode" = candidates-missing ] && [ "$rid" != "$FAKE_RUN_ID" ]; then
    echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
    exit 1
  fi
  if [ "$mode" = run-running ] && [ "$rid" = "$FAKE_RUN_ID" ]; then
    cat "$FAKE_JSON_DIR/rec-running.json"
    exit 0
  fi
  if [ "$mode" = bytes-change ] && [ "$rid" = "$FAKE_TARGET_RUN" ]; then
    # The second read of that one record is a DIFFERENT record: the bytes a
    # reader wrote back, which is the finding step 4 exists to catch.
    n=$(grep -c "storage cat $url" "$FAKE_LOG_DIR/gcloud.calls" || true)
    if [ "$n" -gt 1 ]; then cat "$FAKE_JSON_DIR/rec-target-after.json"; exit 0; fi
  fi
  f="$FAKE_JSON_DIR/rec-$rid.json"
  [ -f "$f" ] || { echo "gcloud: no such object $url" >&2; exit 1; }
  cat "$f"
  ;;
ls)
  if [ "$recursive" = yes ]; then
    cat "$FAKE_JSON_DIR/listing.txt"
    printf 'TOTAL: %s objects\n' "$(grep -c . "$FAKE_JSON_DIR/listing.txt" || true)"
    exit 0
  fi
  [ "$mode" = line-missing ] && { echo "ERROR: (gcloud.storage.ls) NotFound: No such object" >&2; exit 1; }
  if [ "$mode" = line-changes ]; then
    # The bytes are the same and the object's own line is not: a record rewritten
    # with the same bytes is still an object a read wrote.
    n=$(grep -c "storage ls -l $url" "$FAKE_LOG_DIR/gcloud.calls" || true)
    if [ "$n" -gt 1 ]; then cat "$FAKE_JSON_DIR/one-after.txt"; printf 'TOTAL: 1 objects\n'; exit 0; fi
  fi
  cat "$FAKE_JSON_DIR/one.txt"
  printf 'TOTAL: 1 objects\n'
  ;;
*)
  echo "gcloud: unexpected storage action ${action:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check350.sh's test only. The
# documents are the case's own files: the launch, the two listings, the week's
# report and the diagnoses. A diagnose is answered from the REF it names, which
# is how the check tells the old record from the run it launched. Every call is
# logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/all.calls"
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
run)
  if [ "$mode" = launch-fails ]; then
    printf 'fugaro: the launch was refused\n' >&2
    exit 1
  fi
  cat "$FAKE_JSON_DIR/launch.json"
  ;;
runs)
  case "${2:-}" in
  ls)
    case " $* " in
    *" --group-by "*) cat "$FAKE_JSON_DIR/grouped.json" ;;
    *) cat "$FAKE_JSON_DIR/rows.json" ;;
    esac
    ;;
  digest)
    case " $* " in
    *" --json "*) cat "$FAKE_JSON_DIR/digest.json" ;;
    *) cat "$FAKE_JSON_DIR/digest.md" ;;
    esac
    ;;
  *)
    echo "fugaro: unexpected runs subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
diagnose)
  ref=${2:-}
  if [ -n "${FAKE_STEP5_RUN:-}" ]; then
    case "$ref" in
    *"$FAKE_STEP5_RUN"*)
      cat "$FAKE_JSON_DIR/diagnose-step5.json"
      exit 0
      ;;
    esac
  fi
  case "$ref" in
  *"$FAKE_TARGET_RUN"*) cat "$FAKE_JSON_DIR/diagnose-target.json" ;;
  *) cat "$FAKE_JSON_DIR/diagnose-run.json" ;;
  esac
  ;;
cancel)
  printf 'cancel %s\n' "$*" >>"$FAKE_LOG_DIR/cancel.calls"
  if [ "$mode" = cancel-fails ]; then
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
  fi
  cat "$FAKE_JSON_DIR/cancel.json"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ documents
# write_docs DIR: one installation's worth of documents, consistent with each
# other — the window's rows and the grouping carry the same runs and the same
# totals, the week's report counts the classes the rows carry, and every record
# the check reads out of the bucket is a file here. Each case then bends ONE of
# them, so a FAILed row can be traced to the document that produced it.
write_docs() { # write_docs DIR
  local dir=$1 json=$1/json
  mkdir -p "$json"
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"$slug/$our_run","repo":"$repo_name","run_id":"$our_run","branch":"fugaro/$our_stamp-aa","ref":"chk350-fixture","execution":"exec-1","status":"launched"}
JSON
  # The run this check launched: refused at bootstrap, classed config_invalid.
  cat >"$json/rec-$our_run.json" <<JSON
{"version":1,"run_id":"$our_run","repo":"$repo_name","workflow":"agent","status":"infra_error","stage":"bootstrap","failure_class":"config_invalid","reason":"$refusal","started_at":"$now_stamp","finished_at":"$now_stamp","stages":[],"cost_usd":0}
JSON
  # The old record steps 3 and 4 are about: it failed, it finished INSIDE the
  # week, and its own bytes carry no failure_class at all — a record written
  # before the field shipped.
  cat >"$json/rec-$target_run.json" <<JSON
{"version":1,"run_id":"$target_run","repo":"$repo_name","workflow":"agent","status":"infra_error","stage":"bootstrap","reason":"bootstrap: the repository config is not valid: fugaro.yaml: unknown field old_key","started_at":"${week_from}T09:00:00Z","finished_at":"$target_finished","stages":[],"cost_usd":0.11}
JSON
  # Rewritten with one more field: what a read that writes the record back would
  # leave behind, and what step 4's second reading has to catch.
  cat >"$json/rec-target-after.json" <<JSON
{"version":1,"run_id":"$target_run","repo":"$repo_name","workflow":"agent","status":"infra_error","stage":"bootstrap","reason":"bootstrap: the repository config is not valid: fugaro.yaml: unknown field old_key","started_at":"${week_from}T09:00:00Z","finished_at":"$target_finished","stages":[],"cost_usd":0.11,"failure_class":"config_invalid"}
JSON
  # Minted inside the window and nearest the week, so it is read FIRST: its
  # finish is outside the week, which is the first reason the check skips one.
  cat >"$json/rec-$neighbour_run.json" <<JSON
{"version":1,"run_id":"$neighbour_run","repo":"$repo_name","workflow":"agent","status":"infra_error","stage":"implement","failure_class":"tests_failed","reason":"implement: the tests failed","started_at":"${neighbour_stamp:0:4}-${neighbour_stamp:4:2}-${neighbour_stamp:6:2}T09:00:00Z","finished_at":"$neighbour_finished","stages":[],"cost_usd":0.2}
JSON
  # Minted before the window opens: never read, and the listing's own proof that
  # the window and not the bucket decides what is a candidate.
  cat >"$json/rec-$old_run.json" <<JSON
{"version":1,"run_id":"$old_run","repo":"$repo_name","workflow":"agent","status":"infra_error","stage":"bootstrap","failure_class":"config_invalid","reason":"$refusal","started_at":"${old_stamp:0:4}-${old_stamp:4:2}-${old_stamp:6:2}T09:00:00Z","finished_at":"$target_finished","stages":[],"cost_usd":0.05}
JSON
  # A run that has not ended: the state the check has to cancel rather than
  # leave going.
  cat >"$json/rec-running.json" <<JSON
{"version":1,"run_id":"$our_run","repo":"$repo_name","workflow":"agent","status":"running","stage":"bootstrap","started_at":"$now_stamp","stages":[],"cost_usd":0}
JSON
  cat >"$json/listing.txt" <<LIST
1234 $target_finished gs://$bucket/runs/$slug/$old_run/result.json
1234 $target_finished gs://$bucket/runs/$slug/$target_run/result.json
1234 $neighbour_finished gs://$bucket/runs/$slug/$neighbour_run/result.json
1234 $now_stamp gs://$bucket/runs/$slug/$our_run/result.json
LIST
  cat >"$json/one.txt" <<ONE
1234 2026-01-01T00:00:00Z gs://$bucket/runs/$slug/$target_run/result.json
ONE
  cat >"$json/one-after.txt" <<ONE
2048 2031-01-01T00:00:00Z gs://$bucket/runs/$slug/$target_run/result.json
ONE
  # The window: the run this check launched, the old record, a run that failed
  # under another class, and one that succeeded. Its totals are the plain
  # listing's own, and the grouped document repeats them.
  cat >"$json/rows.json" <<JSON
{"project":"$project","runs":[
{"run":"$slug/$our_run","repo":"$repo_name","run_id":"$our_run","status":"infra_error","stage":"bootstrap","failure_class":"config_invalid","created":"$now_stamp","terminal":true,"settled":true,"cost":{"total_usd":0}},
{"run":"$slug/$target_run","repo":"$repo_name","run_id":"$target_run","status":"infra_error","stage":"bootstrap","failure_class":"config_invalid","created":"${week_from}T09:00:00Z","terminal":true,"settled":true,"cost":{"total_usd":0.11}},
{"run":"$slug/$neighbour_run","repo":"$repo_name","run_id":"$neighbour_run","status":"infra_error","stage":"implement","failure_class":"tests_failed","created":"$neighbour_finished","terminal":true,"settled":true,"cost":{"total_usd":0.2}},
{"run":"$slug/$ok_run","repo":"$repo_name","run_id":"$ok_run","status":"ready","created":"$neighbour_finished","terminal":true,"settled":true,"cost":{"total_usd":0.5}}
],"totals":{"runs":4,"model_usd":0.81,"model_notional_usd":0.2,"compute_usd":0.1,"total_usd":0.91,"compute_not_estimated":0},"warnings":[]}
JSON
  cat >"$json/grouped.json" <<JSON
{"project":"$project","runs":[
{"run":"$slug/$our_run","repo":"$repo_name","run_id":"$our_run","status":"infra_error","stage":"bootstrap","failure_class":"config_invalid","created":"$now_stamp","terminal":true,"settled":true,"cost":{"total_usd":0}},
{"run":"$slug/$target_run","repo":"$repo_name","run_id":"$target_run","status":"infra_error","stage":"bootstrap","failure_class":"config_invalid","created":"${week_from}T09:00:00Z","terminal":true,"settled":true,"cost":{"total_usd":0.11}},
{"run":"$slug/$neighbour_run","repo":"$repo_name","run_id":"$neighbour_run","status":"infra_error","stage":"implement","failure_class":"tests_failed","created":"$neighbour_finished","terminal":true,"settled":true,"cost":{"total_usd":0.2}},
{"run":"$slug/$ok_run","repo":"$repo_name","run_id":"$ok_run","status":"ready","created":"$neighbour_finished","terminal":true,"settled":true,"cost":{"total_usd":0.5}}
],"totals":{"runs":4,"model_usd":0.81,"model_notional_usd":0.2,"compute_usd":0.1,"total_usd":0.91,"compute_not_estimated":0},
"groups":[{"class":"config_invalid","runs":2,"share":0.5,"spend_usd":0.11},{"class":"tests_failed","runs":1,"share":0.25,"spend_usd":0.2}],"warnings":[]}
JSON
  cat >"$json/digest.md" <<MD
# Runs digest — $repo_name — $week_key

## Failures

| class | runs |
| --- | --- |
| config_invalid | 2 |
| tests_failed | 1 |
MD
  cat >"$json/digest.json" <<JSON
{"repo":"$repo_name","period":"$week_key","summary":{"period":"$week_key","start":"$week_from","end":"$week_last","runs":4,"failures":{"config_invalid":2,"tests_failed":1}},"patterns":[],"skipped":[]}
JSON
  cat >"$json/diagnose-run.json" <<JSON
{"project":"$project","failure_class":"config_invalid","reason":"$refusal","row":{"run":"$slug/$our_run","run_id":"$our_run","status":"infra_error","failure_class":"config_invalid","terminal":true}}
JSON
  cat >"$json/diagnose-target.json" <<JSON
{"project":"$project","failure_class":"config_invalid","reason":"bootstrap: the repository config is not valid: fugaro.yaml: unknown field old_key","row":{"run":"$slug/$target_run","run_id":"$target_run","status":"infra_error","failure_class":"config_invalid","terminal":true}}
JSON
  cat >"$json/diagnose-step5.json" <<JSON
{"project":"$project","failure_class":"stage_error_unexplained","reason":"implement: the agent call failed and no class explains it","row":{"run":"$slug/$step5_run","run_id":"$step5_run","status":"infra_error","failure_class":"stage_error_unexplained","terminal":true}}
JSON
  cat >"$json/cancel.json" <<JSON
{"project":"$project","run":"$slug/$our_run","status":"cancelled","marker":true,"hard":true}
JSON
}

# write_checkout DIR: the sandbox repository's checkout and the origin it pushes
# to. Real git, on this machine only: the origin is a bare repository in the
# case's own directory, so the throwaway reference really is created and really
# is deleted.
write_checkout() { # write_checkout DIR
  local dir=$1
  mkdir -p "$dir/repo"
  git -c init.defaultBranch=main init -q "$dir/repo"
  printf 'project: %s\n' "$project" >"$dir/repo/fugaro.yaml"
  printf 'name: %s\n' "$repo_name" >"$dir/repo/README.md"
  git -C "$dir/repo" add -A
  git -C "$dir/repo" -c user.name="Check 350 fixture" -c user.email="fixture@example.invalid" \
    commit -qm "the sandbox repository's own HEAD"
  git -c init.defaultBranch=main init -q --bare "$dir/origin.git"
  git -C "$dir/repo" remote add origin "$dir/origin.git"
  git -C "$dir/repo" push -q origin main
}

prepare() { # prepare DIR
  mkdir -p "$1/home" "$1/logs" "$1/tmp"
  : >"$1/logs/all.calls"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1"
}

run_check() { # run_check DIR [ENV...] [-- ARGS...]
  local dir=$1
  shift
  local envs=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  # The three arguments every case needs come first, and a case's own follow
  # them: parse_args takes the last value it is given, so a case overrides one by
  # naming it again, and the cases that must OMIT an argument call run_one.
  cli=(--repo "$dir/repo" --project "$project" --bucket "$bucket" ${cli[@]+"${cli[@]}"})
  run_one "$dir" "$script" ${envs[@]+"${envs[@]}"} -- "${cli[@]}"
}

run_copy() { # run_copy DIR COPY [ENV...] [-- ARGS...]
  local dir=$1 copy=$2
  shift 2
  local envs=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  cli=(--repo "$dir/repo" --project "$project" --bucket "$bucket" ${cli[@]+"${cli[@]}"})
  run_one "$dir" "$copy" ${envs[@]+"${envs[@]}"} -- "${cli[@]}"
}

run_one() { # run_one DIR SCRIPT [ENV...] [-- ARGS...]
  local dir=$1 target=$2
  shift 2
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
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/cancel.calls"
  : >"$dir/logs/all.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures the refusal of a
  # tracing shell is the only way to start one.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. A variable given as an argument wins over
  # `env -u`, which is how the case that measures that refusal sets it again.
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" \
    FAKE_RUN_ID="$our_run" FAKE_TARGET_RUN="$target_run" \
    FAKE_STEP5_RUN="${FAKE_STEP5_RUN:-}" \
    CHECK350_CALL_SECONDS=10 CHECK350_LAUNCH_SECONDS=15 \
    CHECK350_RUN_TIMEOUT_SECONDS=3 CHECK350_POLL_SECONDS=1 CHECK350_CANDIDATES=4 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

assert_no_run() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "run --repo" "$2: no run is launched"
}

assert_no_cancel() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "cancel " "$2: nothing is cancelled"
}

assert_cancelled() { # dir what
  expect_contains "$(cat "$1/logs/fugaro.calls")" "cancel $our_run --now" "$2: the run is cancelled with its own id"
}

assert_no_push() { # dir what
  expect_absent "$(cat "$1/logs/all.calls")" "push" "$2: nothing is pushed"
  expect_absent "$(git -C "$1/repo" for-each-ref refs/heads/chk350- 2>/dev/null || true)" "chk350-" \
    "$2: no branch of this check is made"
}

assert_branch_gone() { # dir what
  local heads
  heads=$(git -C "$1/repo" ls-remote --heads "$1/origin.git" 2>/dev/null || true)
  expect_contains "$heads" "refs/heads/main" "$2: the origin answered and still holds the repository's own branch"
  expect_absent "$heads" "chk350-" "$2: the throwaway branch is gone from the origin"
  expect_absent "$(git -C "$1/repo" for-each-ref refs/heads/chk350- 2>/dev/null || true)" "chk350-" \
    "$2: the throwaway branch is gone from the checkout"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check350.sh --repo <a sandbox repository checkout>" "--help: the command it documents"
  expect_contains "$out" "--step5-run <run id>" "--help: the refutation step's argument"
  expect_contains "$out" "--dry-run" "--help: the plan-only argument"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/all.calls")" "gcloud" "--help: no cloud call"
  assert_no_run "$dir" "--help"
  assert_no_push "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 350 RESULT" "dry run: the block"
  expect_contains "$out" "FACT project: PASS" "dry run: the project row"
  expect_contains "$out" "FACT repo: PASS" "dry run: the checkout guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT week: INFO" "dry run: the window is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the Restore row"
  expect_contains "$out" "CHECK 350 RESULT END (exit 0)" "dry run: the end line"
  # A dry run measured no step, so its block carries no step row at all: a plan
  # is not a reading, and a row reading PASS beside a command nobody ran is what
  # the dry run exists to make impossible.
  expect_absent "$out" "FACT settled" "dry run: no step is read"
  expect_absent "$out" "FACT readonly" "dry run: no step is read"
  expect_contains "$out" "nothing was pushed, launched, cancelled or deleted" "dry run: the marker row says what it did"
  expect_contains "$err" "fugaro run --repo" "dry run: the plan names the launch"
  expect_contains "$err" "git -C" "dry run: the plan names the Restore"
  # The whole point of the guard: a dry run makes NO call at all, of either tool.
  [ -s "$dir/logs/all.calls" ] && fail "dry run: a call was made: $(cat "$dir/logs/all.calls")"
  assert_no_push "$dir" "dry run"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT repo: PASS" "happy path: the checkout and its origin"
  expect_contains "$out" "FACT branch: PASS" "happy path: the reference is cut and pushed"
  expect_contains "$out" "FACT run: PASS" "happy path: the launch"
  expect_contains "$out" "launched $slug/$our_run" "happy path: the run it follows"
  expect_contains "$out" "FACT settled: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT class: PASS" "happy path: the class"
  expect_contains "$out" "failure_class config_invalid" "happy path: the class it was refused under"
  expect_contains "$out" "FACT window: PASS" "happy path: the listing"
  expect_contains "$out" "FACT agree: PASS" "happy path: the listing and the diagnosis agree"
  expect_contains "$out" "FACT target: PASS" "happy path: the old record"
  expect_contains "$out" "carry NO failure_class" "happy path: what makes it the old record"
  expect_contains "$out" "FACT groups: PASS" "happy path: the groups add up"
  expect_contains "$out" "FACT share: PASS" "happy path: the shares follow the runs"
  expect_contains "$out" "FACT totals: PASS" "happy path: the totals did not move"
  expect_contains "$out" "FACT old-record: PASS" "happy path: the old record is classed from its reason"
  expect_contains "$out" "FACT digest: PASS" "happy path: the week's report"
  expect_contains "$out" "carries the Failures table" "happy path: the table a person reads"
  expect_contains "$out" "FACT readonly: PASS" "happy path: the record is byte-identical"
  expect_contains "$out" "FACT step5: INFO" "happy path: the refutation is the owner's"
  expect_contains "$out" "FACT step6: INFO" "happy path: the HARD RULE is the owner's"
  expect_contains "$out" "FACT restore: PASS" "happy path: the Restore"
  expect_contains "$out" "CHECK 350 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, the record was read at the
  # run's own key, and the launch was made from the checkout with this check's
  # own reference.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "run --repo $dir/repo --ref chk350-" "happy path: the launch names the checkout and its own branch"
  expect_contains "$calls" "runs ls --repo $repo_name --since" "happy path: the window is listed by the repository's slug"
  expect_contains "$calls" "diagnose $slug/$our_run --json (in $dir/repo)" "happy path: the diagnosis is a read of the run in the checkout"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://$bucket/runs/$slug/$our_run/result.json" "happy path: the record is read at the run's own key"
  # Step 4's first reading is taken BEFORE the first `fugaro` read of the window:
  # a hash taken after it would compare a rewritten record with itself.
  local cat_line ls_line
  cat_line=$(grep -n "storage cat gs://$bucket/runs/$slug/$target_run/result.json" "$dir/logs/all.calls" | head -1 | cut -d: -f1)
  ls_line=$(grep -n "^fugaro runs ls" "$dir/logs/all.calls" | head -1 | cut -d: -f1)
  if [ -z "$cat_line" ] || [ -z "$ls_line" ]; then
    fail "happy path: the old record was not read, or the window was not listed: $cat_line/$ls_line"
  elif [ "$cat_line" -ge "$ls_line" ]; then
    fail "happy path: the old record is hashed at line $cat_line, at or after the window's first read at $ls_line: step 4's comparison would then have nothing to hold"
  fi
  # The record the check launched is LEFT in the bucket (it is what step 1 read),
  # and the one reference it made is deleted, in both places, and read back.
  expect_contains "$out" "is left in the runs bucket" "happy path: the run's record stays"
  assert_no_cancel "$dir" "happy path"
  assert_branch_gone "$dir" "happy path"
  assert_scratch_removed "happy path"
}
