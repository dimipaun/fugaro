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
  case "$mode" in *record-missing*) echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2; exit 1 ;; esac
  rid=$(run_of "$url")
  case "$mode" in
  *candidates-missing*)
    if [ "$rid" != "$FAKE_RUN_ID" ]; then
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
    fi
    ;;
  esac
  case "$mode" in
  *run-running*)
    if [ "$rid" = "$FAKE_RUN_ID" ]; then
      cat "$FAKE_JSON_DIR/rec-running.json"
      exit 0
    fi
    ;;
  esac
  case "$mode" in
  *bytes-change*)
    if [ "$rid" = "$FAKE_TARGET_RUN" ]; then
    # The second read of that one record is a DIFFERENT record: the bytes a
    # reader wrote back, which is the finding step 4 exists to catch.
      n=$(grep -c "storage cat $url" "$FAKE_LOG_DIR/gcloud.calls" || true)
      if [ "$n" -gt 1 ]; then cat "$FAKE_JSON_DIR/rec-target-after.json"; exit 0; fi
    fi
    ;;
  esac
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
  case "$mode" in *line-missing*) echo "ERROR: (gcloud.storage.ls) NotFound: No such object" >&2; exit 1 ;; esac
  case "$mode" in
  *line-changes*)
    # The bytes are the same and the object's own line is not: a record rewritten
    # with the same bytes is still an object a read wrote.
    n=$(grep -c "storage ls -l $url" "$FAKE_LOG_DIR/gcloud.calls" || true)
    if [ "$n" -gt 1 ]; then cat "$FAKE_JSON_DIR/one-after.txt"; printf 'TOTAL: 1 objects\n'; exit 0; fi
    ;;
  esac
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
  case "$mode" in
  *launch-fails*)
    printf 'fugaro: the launch was refused\n' >&2
    exit 1
    ;;
  esac
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
  case "$mode" in
  *cancel-fails*)
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
    ;;
  esac
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

test_belong_in_the_project() {
  local dir=$work/belongproject
  prepare "$dir"
  run_check "$dir" -- --project belong-sandbox
  expect_status "$status" 2 "the project named belong"
  expect_contains "$out" "FACT belong: FAIL" "the project named belong: the row"
  expect_contains "$err" "matches the installation 'belong'" "the project named belong: the refusal"
  assert_no_run "$dir" "the project named belong"
  assert_no_push "$dir" "the project named belong"
  assert_scratch_removed "the project named belong"
}

test_belong_in_the_gcp_project() {
  local dir=$work/belonggcp
  prepare "$dir"
  run_check "$dir" -- --gcp-project belong-gcp
  expect_status "$status" 2 "the GCP project named belong"
  expect_contains "$out" "FACT belong: FAIL" "the GCP project named belong: the row"
  expect_contains "$err" "'belong-gcp' matches" "the GCP project named belong: which name matched"
  assert_no_push "$dir" "the GCP project named belong"
}

test_belong_in_the_bucket() {
  local dir=$work/belongbucket
  prepare "$dir"
  run_check "$dir" -- --bucket gs://belong-runs/
  expect_status "$status" 2 "the bucket named belong"
  expect_contains "$out" "FACT belong: FAIL" "the bucket named belong: the row"
  assert_no_push "$dir" "the bucket named belong"
}

test_belong_in_the_checkouts_own_config() {
  local dir=$work/belongconfig
  prepare "$dir"
  # The checkout is a sandbox one and the file it CARRIES names the owner's own
  # installation: the run launched from here would land there.
  printf 'project: belong\n' >"$dir/repo/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a checkout that names belong"
  expect_contains "$out" "FACT belong: FAIL" "a checkout that names belong: the row"
  expect_contains "$out" "names the project 'belong'" "a checkout that names belong: which reading matched"
  assert_no_push "$dir" "a checkout that names belong"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "CLAUDECODE is set" "a coding agent's session: the marker that stopped it"
  assert_no_run "$dir" "a coding agent's session"
  assert_no_push "$dir" "a coding agent's session"
}

test_an_xtrace_shell_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  # `-x` is bash's own flag and has to come before the script: appended after it,
  # it would be an argument the check refuses as unknown, which is a different
  # refusal entirely.
  run_one "$dir" "$script" -- -x --repo "$dir/repo" --project "$project" --bucket "$bucket"
  expect_status "$status" 2 "a shell started with -x"
  expect_contains "$out" "FACT xtrace: FAIL" "a shell started with -x: the row"
  expect_contains "$err" "Run it without -x" "a shell started with -x: the refusal"
  assert_no_run "$dir" "a shell started with -x"
}

test_missing_arguments() {
  local dir=$work/args
  prepare "$dir"
  run_one "$dir" "$script" -- --project "$project" --bucket "$bucket"
  expect_status "$status" 2 "no --repo"
  expect_contains "$err" "--repo is required" "no --repo: the refusal"
  run_one "$dir" "$script" -- --repo "$dir/repo" --bucket "$bucket"
  expect_status "$status" 2 "no --project"
  expect_contains "$err" "--project is required" "no --project: the refusal"
  run_one "$dir" "$script" -- --repo "$dir/repo" --project "$project"
  expect_status "$status" 2 "no --bucket"
  expect_contains "$err" "--bucket is required" "no --bucket: the refusal"
  run_check "$dir" -- --since nonsense
  expect_status "$status" 2 "a window that is not one"
  expect_contains "$err" "--since 'nonsense' is not a window" "a window that is not one: the refusal"
  run_check "$dir" -- --step5-run
  expect_status "$status" 2 "--step5-run with no value"
  expect_contains "$err" "--step5-run needs a run id" "--step5-run with no value: the refusal"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an argument it does not know"
  expect_contains "$err" "unknown argument '--nonsense'" "an argument it does not know: the refusal"
  assert_no_run "$dir" "the argument refusals"
  assert_no_push "$dir" "the argument refusals"
}

test_a_path_that_is_not_a_checkout() {
  local dir=$work/notacheckout
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" -- --repo "$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$out" "FACT repo: FAIL" "a directory that is not a checkout: the row"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  assert_no_run "$dir" "a directory that is not a checkout"
}

test_a_checkout_with_no_origin() {
  local dir=$work/noorigin
  prepare "$dir"
  # A checkout of a repository with no origin: the reference could be cut and
  # could be pushed nowhere, so the run could never be started from it.
  mkdir -p "$dir/plain"
  printf 'project: %s\n' "$project" >"$dir/plain/fugaro.yaml"
  git -c init.defaultBranch=main init -q "$dir/plain"
  run_check "$dir" -- --repo "$dir/plain"
  expect_status "$status" 2 "a checkout with no origin"
  expect_contains "$out" "FACT repo: FAIL" "a checkout with no origin: the row"
  expect_contains "$err" "has no 'origin' remote" "a checkout with no origin: the refusal"
  assert_no_run "$dir" "a checkout with no origin"
}

test_a_launch_that_fails() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launcher that refuses"
  expect_contains "$out" "FACT branch: PASS" "a launcher that refuses: the reference was still cut and pushed"
  expect_contains "$out" "FACT run: FAIL" "a launcher that refuses: the row"
  expect_contains "$out" "fugaro run exited 1" "a launcher that refuses: what it read"
  expect_contains "$out" "FACT settled: FAIL" "a launcher that refuses: nothing to follow"
  expect_contains "$out" "there is no run to follow" "a launcher that refuses: why nothing was followed"
  expect_contains "$out" "FACT readonly: FAIL" "a launcher that refuses: step 4 measured nothing"
  expect_contains "$out" "FACT restore: PASS" "a launcher that refuses: the Restore still ran"
  assert_no_cancel "$dir" "a launcher that refuses"
  assert_branch_gone "$dir" "a launcher that refuses"
  assert_scratch_removed "a launcher that refuses"
}

test_a_launch_that_names_no_run() {
  local dir=$work/norunname
  prepare "$dir"
  printf '{"project":"%s","status":"launched"}\n' "$project" >"$dir/json/launch.json"
  run_check "$dir"
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT run: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run this check could take a bucket key from" "a launch that names no run: what it read"
  assert_no_cancel "$dir" "a launch that names no run"
  assert_branch_gone "$dir" "a launch that names no run"
}

test_a_run_that_never_ends() {
  local dir=$work/neverends
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-running
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT settled: FAIL" "a run that never ends: the row"
  expect_contains "$out" "was still running after" "a run that never ends: what it read"
  expect_contains "$out" "FACT cancelled: PASS" "a run that never ends: it is cancelled rather than left going"
  expect_contains "$out" "FACT aborted: FAIL" "a run that never ends: nothing is concluded about the classes"
  assert_cancelled "$dir" "a run that never ends"
  assert_branch_gone "$dir" "a run that never ends"
  assert_scratch_removed "a run that never ends"
}

test_a_cancel_that_does_not_answer() {
  local dir=$work/cancelfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-running+cancel-fails
  expect_status "$status" 2 "a cancel that does not answer"
  expect_contains "$out" "FACT cancelled: FAIL" "a cancel that does not answer: the row"
  expect_contains "$out" "the run may still be going" "a cancel that does not answer: what it tells the owner"
  expect_contains "$out" "fugaro runs show $our_run" "a cancel that does not answer: how to read it by hand"
  assert_cancelled "$dir" "a cancel that does not answer"
}

test_a_record_that_never_appears() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 2 "a run that never bootstrapped"
  expect_contains "$out" "FACT settled: FAIL" "a run that never bootstrapped: the row"
  expect_contains "$out" "never bootstrapped" "a run that never bootstrapped: what it read"
  expect_contains "$out" "FACT cancelled: PASS" "a run that never bootstrapped: the run is still cancelled"
  assert_cancelled "$dir" "a run that never bootstrapped"
  assert_branch_gone "$dir" "a run that never bootstrapped"
}

test_a_class_that_is_not_config_invalid() {
  local dir=$work/wrongclass
  prepare "$dir"
  # The record the check launched, ended for another reason: a task that failed
  # its tests is a different ending from a configuration the decoder refuses.
  sed 's/"failure_class":"config_invalid"/"failure_class":"tests_failed"/' \
    "$dir/json/rec-$our_run.json" >"$dir/json/rec-$our_run.json.new"
  mv "$dir/json/rec-$our_run.json.new" "$dir/json/rec-$our_run.json"
  run_check "$dir"
  expect_status "$status" 1 "a class that is not config_invalid"
  expect_contains "$out" "FACT class: FAIL" "a class that is not config_invalid: the row"
  expect_contains "$out" "rather than config_invalid" "a class that is not config_invalid: what it read"
  expect_contains "$out" "The record is in" "a class that is not config_invalid: where to look"
  assert_branch_gone "$dir" "a class that is not config_invalid"
}

test_the_listing_and_the_diagnosis_disagree() {
  local dir=$work/disagree
  prepare "$dir"
  sed 's/"failure_class":"config_invalid"/"failure_class":"tests_failed"/g' \
    "$dir/json/diagnose-run.json" >"$dir/json/diagnose-run.json.new"
  mv "$dir/json/diagnose-run.json.new" "$dir/json/diagnose-run.json"
  run_check "$dir"
  expect_status "$status" 1 "a row and a diagnosis that disagree"
  expect_contains "$out" "FACT class: PASS" "a row and a diagnosis that disagree: the record itself still says config_invalid"
  expect_contains "$out" "FACT agree: FAIL" "a row and a diagnosis that disagree: the row"
  expect_contains "$out" "the two commands read one record" "a row and a diagnosis that disagree: why it matters"
}

test_the_diagnosis_contradicts_its_own_row() {
  local dir=$work/selfcontradiction
  prepare "$dir"
  printf '{"project":"%s","failure_class":"config_invalid","row":{"run":"%s/%s","run_id":"%s","status":"infra_error","failure_class":"tests_failed"}}\n' \
    "$project" "$slug" "$our_run" "$our_run" >"$dir/json/diagnose-run.json"
  run_check "$dir"
  expect_status "$status" 1 "a diagnosis that contradicts its own row"
  expect_contains "$out" "FACT agree: FAIL" "a diagnosis that contradicts its own row: the row"
  expect_contains "$out" "at the top and" "a diagnosis that contradicts its own row: the two places it names"
}

test_two_readings_of_a_wrong_class() {
  local dir=$work/twowrong
  prepare "$dir"
  # The row and the diagnosis agree, and both are wrong: two readings of one
  # wrong class are still a wrong class.
  sed 's/"failure_class":"config_invalid"/"failure_class":"tests_failed"/g' \
    "$dir/json/diagnose-run.json" >"$dir/json/diagnose-run.json.new"
  mv "$dir/json/diagnose-run.json.new" "$dir/json/diagnose-run.json"
  sed 's/"failure_class":"config_invalid"/"failure_class":"tests_failed"/' \
    "$dir/json/rows.json" >"$dir/json/rows.json.new"
  mv "$dir/json/rows.json.new" "$dir/json/rows.json"
  run_check "$dir"
  expect_status "$status" 1 "two readings of a wrong class"
  expect_contains "$out" "FACT agree: FAIL" "two readings of a wrong class: the row"
  expect_contains "$out" "two readings of one wrong class are still a wrong class" "two readings of a wrong class: why"
}

test_the_run_is_not_in_the_window() {
  local dir=$work/notinwindow
  prepare "$dir"
  # The listing is of a window that does not hold the run this check launched:
  # the row it reads the class out of is picked by the run's own id, and there is
  # none.
  python3 - "$dir/json/rows.json" "$our_run" <<'PY'
import json, sys
path, rid = sys.argv[1], sys.argv[2]
doc = json.load(open(path))
doc["runs"] = [r for r in doc["runs"] if r.get("run_id") != rid]
doc["totals"]["runs"] = len(doc["runs"])
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a window that does not hold the run"
  expect_contains "$out" "FACT agree: FAIL" "a window that does not hold the run: the row"
  expect_contains "$out" "printed no row for $our_run" "a window that does not hold the run: what it read"
}

test_groups_that_do_not_add_up() {
  local dir=$work/badgroups
  prepare "$dir"
  sed 's/"class":"config_invalid","runs":2/"class":"config_invalid","runs":1/' \
    "$dir/json/grouped.json" >"$dir/json/grouped.json.new"
  mv "$dir/json/grouped.json.new" "$dir/json/grouped.json"
  run_check "$dir"
  expect_status "$status" 1 "groups that do not add up"
  expect_contains "$out" "FACT groups: FAIL" "groups that do not add up: the row"
  expect_contains "$out" "do not follow the window's own listing" "groups that do not add up: what it read"
  expect_contains "$out" "config_invalid=1(rows=2)" "groups that do not add up: the group that does not follow"
}

test_a_group_that_holds_too_many_runs() {
  local dir=$work/widedroup
  prepare "$dir"
  sed 's/"class":"tests_failed","runs":1/"class":"tests_failed","runs":2/' \
    "$dir/json/grouped.json" >"$dir/json/grouped.json.new"
  mv "$dir/json/grouped.json.new" "$dir/json/grouped.json"
  run_check "$dir"
  expect_status "$status" 1 "a group that holds too many runs"
  expect_contains "$out" "FACT groups: FAIL" "a group that holds too many runs: the row"
  expect_contains "$out" "tests_failed=2(rows=1)" "a group that holds too many runs: the group that does not follow"
}

test_a_share_that_follows_the_spend() {
  local dir=$work/spendshare
  prepare "$dir"
  # 0.11 is the class's share of the SPEND, not of the runs: the block's own
  # decision is that share follows the runs, with the spend beside it.
  sed 's/"class":"config_invalid","runs":2,"share":0.5/"class":"config_invalid","runs":2,"share":0.11/' \
    "$dir/json/grouped.json" >"$dir/json/grouped.json.new"
  mv "$dir/json/grouped.json.new" "$dir/json/grouped.json"
  run_check "$dir"
  expect_status "$status" 1 "a share that follows the spend"
  expect_contains "$out" "FACT share: FAIL" "a share that follows the spend: the row"
  expect_contains "$out" "config_invalid=0.11(want 0.5)" "a share that follows the spend: the share it should be"
  expect_contains "$out" "a share of the spend under a runs column" "a share that follows the spend: why it matters"
}

test_totals_that_move() {
  local dir=$work/movedtotals
  prepare "$dir"
  sed 's/"total_usd":0.91/"total_usd":0.9/' \
    "$dir/json/grouped.json" >"$dir/json/grouped.json.new"
  mv "$dir/json/grouped.json.new" "$dir/json/grouped.json"
  run_check "$dir"
  expect_status "$status" 1 "totals that move between the two listings"
  expect_contains "$out" "FACT totals: FAIL" "totals that move: the row"
  expect_contains "$out" "total_usd(plain=0.91, grouped=0.9)" "totals that move: the figure that moved"
}

test_a_window_with_no_old_record() {
  local dir=$work/nooldrecord
  prepare "$dir"
  # The candidates are readable and every one of them is ineligible: the nearest
  # finished outside the week, and the one inside it carries the field already.
  sed 's/"stage":"bootstrap","reason"/"stage":"bootstrap","failure_class":"config_invalid","reason"/' \
    "$dir/json/rec-$target_run.json" >"$dir/json/rec-$target_run.json.new"
  mv "$dir/json/rec-$target_run.json.new" "$dir/json/rec-$target_run.json"
  run_check "$dir"
  expect_status "$status" 2 "a window with no old record in it"
  expect_contains "$out" "FACT target: FAIL" "a window with no old record in it: the row"
  expect_contains "$out" "already carries failure_class" "a window with no old record in it: the reason it was skipped"
  expect_contains "$out" "finished outside the week" "a window with no old record in it: both reasons are named"
  expect_contains "$out" "FACT aborted: FAIL" "a window with no old record in it: nothing is concluded about the classes"
  assert_branch_gone "$dir" "a window with no old record in it"
}

test_a_bucket_with_no_record_in_the_window() {
  local dir=$work/emptywindow
  prepare "$dir"
  # Only the run this check launched is in the bucket: nothing was minted before
  # the week's last day, so there is no candidate at all.
  printf '1234 %s gs://%s/runs/%s/%s/result.json\n' "$now_stamp" "$bucket" "$slug" "$our_run" >"$dir/json/listing.txt"
  run_check "$dir"
  expect_status "$status" 2 "a bucket with no record in the window"
  expect_contains "$out" "FACT target: FAIL" "a bucket with no record in the window: the row"
  expect_contains "$out" "holds 1 run record(s) and none of them was minted inside the window" \
    "a bucket with no record in the window: what it read"
  expect_contains "$out" "widen the window with --since 60d" "a bucket with no record in the window: what the owner can do"
}

test_the_old_records_bytes_change() {
  local dir=$work/byteschange
  prepare "$dir"
  run_check "$dir" FAKE_MODE=bytes-change
  expect_status "$status" 1 "a record a read wrote back"
  expect_contains "$out" "FACT target: PASS" "a record a read wrote back: the old record was found"
  expect_contains "$out" "FACT readonly: FAIL" "a record a read wrote back: the row"
  expect_contains "$out" "CHANGED between the reading taken before" "a record a read wrote back: the two hashes"
  expect_contains "$out" "This is the finding, not a flake" "a record a read wrote back: what it means"
}

test_the_old_records_line_moves() {
  local dir=$work/linechanges
  prepare "$dir"
  run_check "$dir" FAKE_MODE=line-changes
  expect_status "$status" 1 "an object rewritten with the same bytes"
  expect_contains "$out" "FACT readonly: FAIL" "an object rewritten with the same bytes: the row"
  expect_contains "$out" "the bucket's own line for the object is not" "an object rewritten with the same bytes: what moved"
  expect_contains "$out" "before '1234 " "an object rewritten with the same bytes: the line before"
  expect_contains "$out" "after '2048 " "an object rewritten with the same bytes: the line after"
}

test_a_week_with_no_failures_table() {
  local dir=$work/notable
  prepare "$dir"
  printf '# Runs digest — %s — %s\n\n## Successes\n\n| class | runs |\n| --- | --- |\n' \
    "$repo_name" "$week_key" >"$dir/json/digest.md"
  run_check "$dir"
  expect_status "$status" 1 "a report with no Failures table"
  expect_contains "$out" "FACT digest: FAIL" "a report with no Failures table: the row"
  expect_contains "$out" "carries no '## Failures' table" "a report with no Failures table: what it read"
}

test_a_report_of_another_week() {
  local dir=$work/otherweek
  prepare "$dir"
  sed 's/"start":"[^"]*","end":"[^"]*"/"start":"1999-01-04","end":"1999-01-10"/' \
    "$dir/json/digest.json" >"$dir/json/digest.json.new"
  mv "$dir/json/digest.json.new" "$dir/json/digest.json"
  run_check "$dir"
  expect_status "$status" 1 "a report of another week"
  expect_contains "$out" "FACT digest: FAIL" "a report of another week: the row"
  expect_contains "$out" "is not the week this check read the bucket for" "a report of another week: what it read"
}

test_a_week_that_counts_no_class() {
  local dir=$work/noclass
  prepare "$dir"
  sed 's/"failures":{"config_invalid":2,"tests_failed":1}/"failures":{"tests_failed":3}/' \
    "$dir/json/digest.json" >"$dir/json/digest.json.new"
  mv "$dir/json/digest.json.new" "$dir/json/digest.json"
  run_check "$dir"
  expect_status "$status" 1 "a week that does not count the old record's class"
  expect_contains "$out" "FACT digest: FAIL" "a week that counts no class: the row"
  expect_contains "$out" "does not count the class the old record is read as" "a week that counts no class: what it read"
}

test_the_old_records_row_carries_no_class() {
  local dir=$work/oldrowempty
  prepare "$dir"
  # The record written before the field shipped, whose row the READER could not
  # class: the week is then still split into before and after, which is the whole
  # of what step 3 exists to falsify.
  python3 - "$dir/json/rows.json" "$target_run" <<'PY'
import json, sys
path, rid = sys.argv[1], sys.argv[2]
doc = json.load(open(path))
for row in doc["runs"]:
    if row.get("run_id") == rid:
        row["failure_class"] = ""
json.dump(doc, open(path, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "an old record no reader classed"
  expect_contains "$out" "FACT old-record: FAIL" "an old record no reader classed: the row"
  expect_contains "$out" "the week IS split into before and after" "an old record no reader classed: why it matters"
  expect_contains "$out" "FACT digest: FAIL" "an old record no reader classed: the week's report cannot count it either"
  expect_contains "$out" "FACT readonly: PASS" "an old record no reader classed: the record itself was still not written back"
}

test_step5_reads_the_refutation() {
  local dir=$work/step5
  prepare "$dir"
  run_check "$dir" FAKE_STEP5_RUN="$step5_run" -- --step5-run "$step5_run"
  expect_status "$status" 0 "the refutation run"
  expect_contains "$out" "FACT step5: PASS" "the refutation run: the row"
  expect_contains "$out" "carries failure_class stage_error_unexplained" "the refutation run: the class the plan expects"
  expect_contains "$out" "which is O6 unanswered" "the refutation run: what that class means"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "diagnose $step5_run --json" "the refutation run: it is read, never made"
  # The check launches a run of its own, so what says the refutation was READ is
  # that its id is never named to anything but a diagnose: it is a run the owner
  # made by hand, and this check neither starts nor stops one.
  local others
  others=$(grep "$step5_run" "$dir/logs/fugaro.calls" | grep -v '^fugaro diagnose ' || true)
  [ -z "$others" ] || fail "the refutation run: its id is named to something other than a diagnose: $others"
}

test_step5_under_another_class_is_a_finding() {
  local dir=$work/step5other
  prepare "$dir"
  sed 's/stage_error_unexplained/config_invalid/g' \
    "$dir/json/diagnose-step5.json" >"$dir/json/diagnose-step5.json.new"
  mv "$dir/json/diagnose-step5.json.new" "$dir/json/diagnose-step5.json"
  run_check "$dir" FAKE_STEP5_RUN="$step5_run" -- --step5-run "$step5_run"
  expect_status "$status" 1 "the refutation run under another class"
  expect_contains "$out" "FACT step5: FAIL" "the refutation run under another class: the row"
  expect_contains "$out" "rather than stage_error_unexplained" "the refutation run under another class: what it read"
  expect_contains "$out" "a finding about the classifier" "the refutation run under another class: what it means"
}

test_a_restore_that_cannot_delete_is_a_finding() {
  local dir=$work/norestore
  prepare "$dir"
  # An origin whose hook refuses the DELETE and accepts the push: the reference
  # is created and cannot be removed again, which is the state the Restore row
  # exists to read rather than to assume away.
  cat >"$dir/origin.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
while read -r old _new _ref; do
  case "$old" in
  0000000000000000000000000000000000000000) ;;
  *)
    echo "this origin refuses deletions" >&2
    exit 1
    ;;
  esac
done
exit 0
HOOK
  chmod +x "$dir/origin.git/hooks/pre-receive"
  run_check "$dir"
  expect_status "$status" 1 "a Restore that cannot delete"
  expect_contains "$out" "FACT readonly: PASS" "a Restore that cannot delete: the steps themselves still held"
  expect_contains "$out" "FACT restore: FAIL" "a Restore that cannot delete: the row"
  expect_contains "$out" "the origin still lists it" "a Restore that cannot delete: what it read back"
  expect_contains "$out" "Delete it by hand" "a Restore that cannot delete: what the owner has to do"
  expect_contains "$(git -C "$dir/repo" ls-remote --heads "$dir/origin.git" 2>/dev/null)" "chk350-" \
    "a Restore that cannot delete: the branch really is still on the origin"
}

test_a_comparison_that_never_runs_does_not_pass() {
  local dir=$work/regressed
  prepare "$dir"
  local copy=$dir/check350-regressed.sh
  # The same script with step 4's second reading removed: it takes the hash from
  # before the reads instead of reading the record again. A record a read wrote
  # back is then indistinguishable from one nobody touched — so the FAIL the
  # previous case reads is the comparison's, and not a coincidence of the fixture.
  sed -e 's|^  T1_HASH=$(hash_of "$out")$|  T1_HASH=$T0_HASH|' \
      -e 's|^  T1_LINE=$(object_line "$SCRATCH/target-after-ls.txt")$|  T1_LINE=$T0_LINE|' \
      "$script" >"$copy"
  [ "$(grep -c 'T1_HASH=\$T0_HASH' "$copy")" = 1 ] || fail "the regressed copy was not made"
  run_copy "$dir" "$copy" FAKE_MODE=bytes-change
  expect_status "$status" 0 "a check whose comparison never runs"
  expect_contains "$out" "FACT readonly: PASS" "a check whose comparison never runs: it cannot tell, and it does not"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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
t test_belong_in_the_project
t test_belong_in_the_gcp_project
t test_belong_in_the_bucket
t test_belong_in_the_checkouts_own_config
t test_an_agent_session_is_refused
t test_an_xtrace_shell_is_refused
t test_missing_arguments
t test_a_path_that_is_not_a_checkout
t test_a_checkout_with_no_origin
t test_a_launch_that_fails
t test_a_launch_that_names_no_run
t test_a_run_that_never_ends
t test_a_cancel_that_does_not_answer
t test_a_record_that_never_appears
t test_a_class_that_is_not_config_invalid
t test_the_listing_and_the_diagnosis_disagree
t test_the_diagnosis_contradicts_its_own_row
t test_two_readings_of_a_wrong_class
t test_the_run_is_not_in_the_window
t test_groups_that_do_not_add_up
t test_a_group_that_holds_too_many_runs
t test_a_share_that_follows_the_spend
t test_totals_that_move
t test_a_window_with_no_old_record
t test_a_bucket_with_no_record_in_the_window
t test_the_old_records_bytes_change
t test_the_old_records_line_moves
t test_a_week_with_no_failures_table
t test_a_report_of_another_week
t test_a_week_that_counts_no_class
t test_the_old_records_row_carries_no_class
t test_step5_reads_the_refutation
t test_step5_under_another_class_is_a_finding
t test_a_restore_that_cannot_delete_is_a_finding
t test_a_comparison_that_never_runs_does_not_pass

if [ "$failures" -ne 0 ]; then
  echo "check350_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check350_test.sh: ok"
