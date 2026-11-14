#!/usr/bin/env bash
# Tests for scripts/live-checks/check440.sh, against a fake `fugaro` on PATH (and
# a fake `gcloud` for the two bucket rows): nothing here reaches the network,
# there is no installation and no bucket, no run is launched and no credential is
# read, made or printed.
#
# The fake models the one thing this check is about: the short-form rules of
# docs/short-run-ids, over a run set the case seeds. It resolves an ending the
# way the CLI does — the active runs and the window first, the whole bucket only
# on a miss (D1702) — shows the numbered pick list and one question ONLY at a
# real terminal (D1700), never cancels anything an ending names (D1710), and
# writes a cancel marker only for a full run ID. So a case says what the check
# measured by seeding runs, and what it concluded by reading the block: a check
# that read the wrong run, took the refusal for a pass, or cancelled through an
# ending is a FAILed row here.
#
# Usage: bash scripts/live-checks/check440_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check440.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK440_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|restore|runs-launched):' | sed 's/^/ ... last run: /')"
}

# The installation and the repository a case runs against. Neither names
# `belong`, which is the installation this check must never touch.
INSTALLATION=sandbox-a
SLUG=acme-web
# The run older than the window, and one inside it: the two ends of D1702.
OLD_ID=20261001-120000-aaaa
RECENT_ID=20261113-090000-bbbb

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(agent-session belong installation checkout runs-launched
  cancel-active-refuses cancel-marker diagnose-too-short diagnose-refuses-piped
  diagnose-pick-terminal logs-collision logs-ending old-run
  cancel-finished-refuses window-widens latency restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check440.sh's test.
#
# It holds the run set the case seeded ($FAKE440_DIR/runs, one line per run:
# run_id|created|terminal|title|in-the-window) and answers the four reads the
# check makes the way the CLI does:
#   * `run --run-id ID --json` appends the run it was told to launch;
#   * `runs ls --json --since W` prints the window (7d) or the bucket's own
#     history (90d), with the log_url and execution the launch records carry;
#   * `diagnose` and `logs --url` resolve an ending against the active runs and
#     the window, widen to the whole bucket only on a miss, and show the pick
#     list and one question only at a real terminal;
#   * `cancel` NEVER acts on an ending: it reads it against the active runs and
#     answers the full IDs to paste (D1710), and writes the cancel marker only
#     for a `<slug>/<run-id>` — with a `cancel-slow` mode that does not answer a
#     graceful cancel at all, so the Restore's own bound is a reading rather
#     than a claim.
# Every call is recorded on $FAKE440_DIR/logs/fugaro.calls, with the directory it
# was made in, so a case can say what the check read and what it did not.
set -u
D=${FAKE440_DIR:?}
SLUG=${FAKE440_SLUG:-acme-web}
MODE=${FAKE440_MODE:-ok}
mkdir -p "$D/logs" "$D/markers"
printf '%s|%s\n' "$PWD" "$*" >>"$D/logs/fugaro.calls"

logurl() { printf 'https://console.cloud.google.com/run/detail/us-east5/job-acme-web/executions/exec-%s?project=prj-fake\n' "$1"; }
exec_of() { printf 'projects/prj-fake/locations/us-east5/jobs/job-acme-web/executions/exec-%s\n' "$1"; }
now_iso() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# rows_for window|wide|active: the runs the listing holds, as
# run_id|created|terminal|title.
rows_for() {
  local want=$1 id created terminal title inwin
  while IFS='|' read -r id created terminal title inwin; do
    [ -n "$id" ] || continue
    case "$want" in
    window) [ "$inwin" = yes ] || continue ;;
    active) [ "$terminal" = false ] || continue ;;
    esac
    printf '%s|%s|%s|%s\n' "$id" "$created" "$terminal" "$title"
  done <"$D/runs"
}

ids_of() { rows_for "$1" | cut -d'|' -f1; }
title_of() { awk -F'|' -v id="$2" '$1==id {print $4; exit}' "$D/runs"; }
created_of() { awk -F'|' -v id="$2" '$1==id {print $2; exit}' "$D/runs"; }

# match_in REF LISTFILE: the ids of the list that end with REF.
match_in() {
  local ref=$1 file=$2 id
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    case "$id" in *"$ref") printf '%s\n' "$id" ;; esac
  done <"$file"
}

# shortest_ending ID FILE: the CLI's own collision rule (D1706) over the ids in
# FILE — the shortest ending no other id there also ends with, at least four
# characters.
shortest_ending() {
  local id=$1 file=$2 n=4 other len=${#1} clash
  while [ "$n" -lt "$len" ]; do
    clash=""
    while IFS= read -r other; do
      [ -n "$other" ] || continue
      [ "$other" = "$id" ] && continue
      if [ "${other:${#other}-n:n}" = "${id:${len}-n:n}" ]; then clash=$other; break; fi
    done <"$file"
    [ -n "$clash" ] || break
    n=$((n + 1))
  done
  printf '%s' "${id:${len}-n:n}"
}

when_cell() { # created
  printf '%s' "$1" | sed 's/T\(..:..\):..Z/ \1Z/'
}

# ending_rows FILE: the numbered candidates both answers show (D1700).
ending_rows() {
  local file=$1 i=0 id ending status
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    i=$((i + 1))
    ending=$(shortest_ending "$id" "$file")
    status=running
    case "$(rows_for wide)" in *"$id|"*"|true|"*) status=succeeded ;; esac
    printf '  %d) %s/%s  %s  %s  %s\n' "$i" "$SLUG" "$ending" "$status" \
      "$(when_cell "$(created_of "" "$id")")" "$(title_of "" "$id")"
  done <"$file"
}

full_rows() { # FILE — the same candidates as cancel offers them (D1710).
  local file=$1 id
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    printf '  %s/%s  %s\n' "$SLUG" "$id" "$(title_of "" "$id")"
  done <"$file"
}

diagnose_of() { # id
  printf 'Run:      %s/%s\n' "$SLUG" "$1"
  printf 'Status:   succeeded\n'
  printf 'Cost:     $0.00\n'
}

# resolve_pick REF: the run REF names, in CHOSEN, or nothing with the refusal
# already printed on stderr. The candidate set is the active runs and the window,
# widened to the whole bucket on a miss alone (D1702); several candidates are
# answered by the pick list at a real terminal and refused anywhere else (D1700).
resolve_pick() {
  local ref=$1 n chosen answer
  CHOSEN=""
  if [ "${#ref}" -lt 4 ]; then
    if [ "$MODE" = too-short-ok ]; then CHOSEN=$ref; return 0; fi
    printf '"%s" is %d characters; an ending needs at least 4, the shortest form a run is shown in\n' \
      "$ref" "${#ref}" >&2
    return 1
  fi
  { ids_of active; ids_of window; } | awk '!seen[$0]++' >"$D/.cands"
  match_in "$ref" "$D/.cands" >"$D/.matches"
  if [ ! -s "$D/.matches" ] && [ "$MODE" != no-widen ]; then
    ids_of wide >"$D/.cands"
    match_in "$ref" "$D/.cands" >"$D/.matches"
  fi
  n=$(wc -l <"$D/.matches" | tr -d ' ')
  if [ "$n" -eq 0 ]; then
    printf 'no run ending "%s" among the active runs or the runs the bucket still holds; pass the full ID\n' "$ref" >&2
    return 1
  fi
  if [ "$n" -eq 1 ]; then
    CHOSEN=$(sed -n 1p "$D/.matches")
    return 0
  fi
  # A command that resolves a collision by itself AT A TERMINAL: no list, no
  # question, and one of the two runs picked silently. Away from a terminal the
  # refusal below is unchanged, which is what makes this the D1700 case alone.
  if [ "$MODE" = no-prompt ] && [ -t 0 ] && [ -t 1 ]; then
    CHOSEN=$(sed -n 1p "$D/.matches")
    return 0
  fi
  if [ -t 0 ] && [ -t 1 ]; then
    printf '"%s" names %d runs:\n' "$ref" "$n"
    ending_rows "$D/.matches"
    printf 'which run? [1-%d] ' "$n"
    IFS= read -r answer || answer=""
    chosen=$(sed -n "${answer}p" "$D/.matches")
    if [ -z "$chosen" ]; then
      printf '"%s" is not one of the %d runs listed; pass a longer ending or the full run ID\n' "$answer" "$n" >&2
      return 1
    fi
    if [ "$MODE" = wrong-answer ]; then
      chosen=$(grep -v "^$chosen\$" "$D/.matches" | head -n1)
    fi
    CHOSEN=$chosen
    return 0
  fi
  printf '"%s" names %d runs:\n' "$ref" "$n" >&2
  ending_rows "$D/.matches" >&2
  if [ "$MODE" = piped-no-way-out ]; then
    printf 'that ending names more than one run\n' >&2
  else
    printf 'pass a longer ending or the full run ID\n' >&2
  fi
  return 1
}

cmd_run() { # --run-id ID --json TASK
  local id="" a
  a=("$@")
  local i=0
  while [ "$i" -lt "${#a[@]}" ]; do
    case "${a[$i]}" in
    --run-id)
      id=${a[$((i + 1))]}
      i=$((i + 2))
      ;;
    *) i=$((i + 1)) ;;
    esac
  done
  case "$MODE" in
  launch-fail)
    printf 'run: the launch was refused: the sandbox repository is not configured\n' >&2
    return 1
    ;;
  launch-sleep)
    sleep 30
    return 0
    ;;
  esac
  local terminal=false
  [ "$MODE" = launch-terminal ] && terminal=true
  printf '%s|%s|%s|check 440 launched run|yes\n' "$id" "$(now_iso)" "$terminal" >>"$D/runs"
  printf '%s\n' "$id" >>"$D/launched"
  if [ "$MODE" = launch-no-slug ]; then
    printf '{"project":"sandbox-a","run":"","run_id":"%s","status":"launched"}\n' "$id"
    return 0
  fi
  printf '{"project":"sandbox-a","run":"%s/%s","run_id":"%s","branch":"fugaro/%s","execution":"%s","log_url":"%s","status":"launched"}\n' \
    "$SLUG" "$id" "$id" "$id" "$(exec_of "$id")" "$(logurl "$id")"
}

cmd_runs_ls() { # --json --since W
  local since=7d set=window a i=0 first=1
  a=("$@")
  while [ "$i" -lt "${#a[@]}" ]; do
    case "${a[$i]}" in
    --since)
      since=${a[$((i + 1))]}
      i=$((i + 2))
      ;;
    *) i=$((i + 1)) ;;
    esac
  done
  case "$since" in
  90d | 0) set=wide ;;
  esac
  case "$MODE" in
  list-fails)
    printf 'runs ls: the bucket could not be listed\n' >&2
    return 1
    ;;
  list-bad-json)
    printf 'this is not JSON\n'
    return 0
    ;;
  list-sleep)
    sleep 30
    return 0
    ;;
  esac
  rows_for "$set" >"$D/.rows"
  printf '{"project":"sandbox-a","runs":['
  while IFS='|' read -r id created terminal title; do
    [ -n "$id" ] || continue
    [ "$first" = 1 ] || printf ','
    first=0
    printf '{"run":"%s/%s","run_id":"%s","created":"%s","status":"%s","title":"%s","log_url":"%s","execution":"%s","terminal":%s}' \
      "$SLUG" "$id" "$id" "$created" \
      "$([ "$terminal" = true ] && printf succeeded || printf running)" \
      "$title" "$(logurl "$id")" "$(exec_of "$id")" "$terminal"
  done <"$D/.rows"
  printf '],"totals":{"runs":%s},"warnings":[]}\n' "$(wc -l <"$D/.rows" | tr -d ' ')"
}

cmd_cancel() { # REF [--now] [--json]
  local ref=$1 id="" n now=no a slow
  shift
  for a in "$@"; do
    [ "$a" = --now ] && now=yes
  done
  case "$ref" in
  */*) id=${ref##*/} ;;
  esac
  if [ -z "$id" ]; then
    ids_of active >"$D/.cands"
    match_in "$ref" "$D/.cands" >"$D/.matches"
    n=$(wc -l <"$D/.matches" | tr -d ' ')
    if [ "$n" -eq 0 ]; then
      printf 'no active run ending "%s"; pass the full run ID\n' "$ref" >&2
      return 1
    fi
    if [ "$MODE" = cancel-acts ]; then
      id=$(sed -n 1p "$D/.matches")
      printf '%s\n' "$id" >"$D/markers/$id"
      printf '{"run":"%s/%s","status":"cancelled","marker":true}\n' "$SLUG" "$id"
      return 0
    fi
    printf '"%s" names %d active runs:\n' "$ref" "$n" >&2
    full_rows "$D/.matches" >&2
    printf 'pass one of the full run IDs above\n' >&2
    return 1
  fi
  if [ "$MODE" = cancel-fails ]; then
    printf 'cancel: the backend refused the cancel of %s\n' "$ref" >&2
    return 1
  fi
  if [ "$MODE" = cancel-slow ] && [ "$now" = no ]; then
    # A graceful cancel waits the run's finalize reserve before it cancels
    # anything, so the case that reads the Restore's bound needs one that does
    # not answer: this waits to be killed, and `--now` below answers at once.
    # The trap is what makes the kill immediate — without one bash finishes the
    # wait before it acts on the signal, and the case sits through the sleep.
    sleep 30 &
    slow=$!
    trap 'kill "$slow" 2>/dev/null; exit 143' TERM
    wait "$slow"
  fi
  printf '%s\n' "$id" >"$D/markers/$id"
  printf '{"run":"%s/%s","status":"cancelled","marker":true}\n' "$SLUG" "$id"
}

main() {
  local cmd=$1
  shift
  case "$cmd" in
  run) cmd_run "$@" ;;
  runs)
    [ "${1:-}" = ls ] || {
      printf 'fugaro: unsupported: runs %s\n' "${1:-}" >&2
      return 2
    }
    shift
    cmd_runs_ls "$@"
    ;;
  diagnose)
    resolve_pick "$1" || exit 1
    diagnose_of "$CHOSEN"
    ;;
  logs)
    [ "${1:-}" = --url ] || {
      printf 'fugaro: unsupported: logs %s\n' "${1:-}" >&2
      return 2
    }
    resolve_pick "$2" || exit 1
    case "$MODE" in
    logs-no-url)
      printf 'no console link was recorded for this run'"'"'s execution\n' >&2
      exit 1
      ;;
    logs-wrong-url)
      printf '%s\n' "$(logurl "00000000-000000-dead")"
      ;;
    *) printf '%s\n' "$(logurl "$CHOSEN")" ;;
    esac
    ;;
  cancel) cmd_cancel "$@" ;;
  *)
    printf 'fugaro: unsupported command: %s\n' "$cmd" >&2
    return 2
    ;;
  esac
}

main "$@"
FUGARO_EOF
  chmod 700 "$1/bin/fugaro"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check440.sh's test: it records the call and answers the one
# read the check makes — the listing of a run's own prefix, which names the
# run's records and, in the marker mode, a cancel marker the check must never
# find there.
set -u
D=${FAKE440_DIR:?}
printf '%s\n' "$*" >>"$D/logs/gcloud.calls"
if [ "${1:-}" != storage ] || [ "${2:-}" != ls ]; then
  printf 'gcloud: unsupported: %s\n' "$*" >&2
  exit 2
fi
if [ "${FAKE440_MODE:-ok}" = bucket-fail ]; then
  printf 'ERROR: (gcloud.storage.ls) 403 storage.objects.list denied\n' >&2
  exit 1
fi
url=${!#}
printf '%s\n' "$url/launch.json" "$url/task.json" "$url/result.json"
if [ "${FAKE440_MODE:-ok}" = bucket-marker ]; then
  printf '%s\n' "$url/cancel"
fi
GCLOUD_EOF
  chmod 700 "$1/bin/gcloud"
}

# ------------------------------------------------------------------ the cases
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state/logs" "$dir/state/markers" "$dir/tmp" "$dir/repo"
  write_fakes "$dir"
  : >"$dir/state/runs"
  : >"$dir/state/launched"
  : >"$dir/state/logs/fugaro.calls"
  : >"$dir/state/logs/gcloud.calls"
  printf 'repositories: {}\n' >"$dir/repo/fugaro.yaml"
  status=0
  out=""
  RUN440_ARGS=()
  RUN440_ENV=()
  MODE=ok
  ACTIVE_TIMEOUT=3
  CLI_TIMEOUT=10
  LAUNCH_TIMEOUT=10
}

# iso_ago DAYS: an ISO stamp DAYS days ago. python3 is the clock because the two
# machines this runs on disagree about `date -d`.
iso_ago() {
  python3 - "$1" <<'PY'
import datetime, sys
print((datetime.datetime.now(datetime.timezone.utc)
       - datetime.timedelta(days=float(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))
PY
}

seed_run() { # id created terminal title in-window
  printf '%s|%s|%s|%s|%s\n' "$1" "$2" "$3" "$4" "$5" >>"$dir/state/runs"
}

# seed_history: the run older than the window and one inside it, which is the
# pair D1702 is read with.
seed_history() {
  seed_run "$OLD_ID" "$(iso_ago 9)" true "an old run" no
  seed_run "$RECENT_ID" "$(iso_ago 1)" true "a recent run" yes
}

# run440 [ARGS...]: runs the check on the case at hand, from the checkout.
# RUN440_ARGS adds arguments, RUN440_ENV names environment variables
# (NAME=value), and the waits are seconds so a case about a run that never
# reaches the feed does not sit through the real ones.
run440() {
  status=0
  out=$(cd "$dir/repo" && env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    TMPDIR="$dir/tmp" FAKE440_DIR="$dir/state" FAKE440_SLUG="$SLUG" FAKE440_MODE="$MODE" \
    PATH="$dir/bin:$PATH" \
    CHECK440_ACTIVE_TIMEOUT_SECONDS="$ACTIVE_TIMEOUT" CHECK440_POLL_SECONDS=1 \
    CHECK440_CLI_TIMEOUT_SECONDS="$CLI_TIMEOUT" CHECK440_LAUNCH_TIMEOUT_SECONDS="$LAUNCH_TIMEOUT" \
    ${RUN440_ENV[@]+"${RUN440_ENV[@]}"} \
    bash "$script" ${RUN440_ARGS[@]+"${RUN440_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN440_ARGS=()
  RUN440_ENV=()
  MODE=ok
  ACTIVE_TIMEOUT=3
  CLI_TIMEOUT=10
  LAUNCH_TIMEOUT=10
}
run440_default() { run440 --installation "$INSTALLATION" --yes "$@"; }

# launched_ids: the two IDs the check minted for itself, from the fake's own
# record of the launches.
launched_ids() { cat "$dir/state/launched"; }
launched_count() { wc -l <"$dir/state/launched" | tr -d ' '; }
fugaro_calls() { cat "$dir/state/logs/fugaro.calls"; }
gcloud_calls() { cat "$dir/state/logs/gcloud.calls"; }
fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}
expect_fact() { # key VERDICT what
  local line
  line=$(fact_line "$1")
  [ -n "$line" ] || {
    fail "$3: the block carries no $1 row"
    return 0
  }
  expect_contains "$line" "$1: $2" "$3"
}
expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 440 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}
expect_no_calls() { # what
  if [ -s "$dir/state/logs/fugaro.calls" ]; then
    fail "$1: a fugaro call was made: $(tr '\n' ';' <"$dir/state/logs/fugaro.calls")"
  fi
  if [ -s "$dir/state/logs/gcloud.calls" ]; then
    fail "$1: a gcloud call was made: $(tr '\n' ';' <"$dir/state/logs/gcloud.calls")"
  fi
}
scratch_of() { sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1; }
expect_scratch_gone() { # what
  local scratch
  scratch=$(scratch_of)
  if [ -z "$scratch" ]; then
    fail "$1: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}
# expect_cancelled WHAT: the Restore really cancelled the two runs the check
# launched — a marker per run, written only for a full ID.
expect_cancelled() {
  local what=$1 id
  for id in $(launched_ids); do
    [ -f "$dir/state/markers/$id" ] || fail "$what: the run $id carries no cancel marker"
  done
}

# --------------------------------------------------------------- the happy path
test_a_clean_run_settles_every_rule() {
  setup_case happy
  seed_history
  run440_default
  expect_status "$status" 0 "a clean run"
  expect_block_keys "a clean run"
  local ids a b
  ids=$(launched_ids)
  a=$(printf '%s\n' "$ids" | sed -n 1p)
  b=$(printf '%s\n' "$ids" | sed -n 2p)
  expect_contains "$(fact_line runs-launched)" "PASS" "the two launches"
  expect_contains "$(fact_line runs-launched)" "$a" "the two launches: the first ID"
  expect_contains "$(fact_line runs-launched)" "$b" "the two launches: the second ID"
  expect_contains "$(fact_line runs-launched)" "${a: -4}" "the two launches: the ending they share"
  expect_contains "$(fact_line cancel-active-refuses)" "PASS" "D1710's active-only list"
  expect_contains "$(fact_line cancel-active-refuses)" "cancelled nothing" "D1710: what the ending did"
  expect_contains "$(fact_line diagnose-too-short)" "PASS" "the minimum"
  expect_contains "$(fact_line diagnose-refuses-piped)" "PASS" "the piped refusal"
  expect_contains "$(fact_line diagnose-pick-terminal)" "PASS" "the pick list"
  expect_contains "$(fact_line logs-collision)" "PASS" "logs at the collision"
  expect_contains "$(fact_line logs-ending)" "PASS" "logs by a longer ending"
  expect_contains "$(fact_line old-run)" "PASS" "the run older than the window"
  expect_contains "$(fact_line cancel-finished-refuses)" "PASS" "cancel of a finished run"
  expect_contains "$(fact_line window-widens)" "PASS" "D1702's widening"
  expect_contains "$(fact_line latency)" "INFO" "F5's figure, which is a figure and not a criterion"
  expect_contains "$(fact_line restore)" "PASS" "the Restore"
  expect_contains "$(fact_line cancel-marker)" "not measured" "the bucket row without --bucket"
  expect_cancelled "a clean run"
  expect_scratch_gone "a clean run"
}

test_the_pick_list_is_read_at_a_terminal_and_answered() {
  setup_case pick
  seed_history
  run440_default
  expect_status "$status" 0 "the pick list"
  local ids a
  ids=$(launched_ids)
  a=$(printf '%s\n' "$ids" | sed -n 1p)
  # The pty transcript is the check's own: the row it answered is the run the
  # ending was chosen for, and the diagnosis carries that run in full (D1705).
  expect_contains "$(fact_line diagnose-pick-terminal)" "which run? [1-2]" "the pick list: the question"
  expect_contains "$(fact_line diagnose-pick-terminal)" "$a" "the pick list: the run the answer named"
  expect_contains "$(fact_line logs-collision)" "PASS" "the same list through logs"
}

test_the_bucket_rows_are_read_when_the_bucket_is_given() {
  setup_case bucket
  seed_history
  run440 --installation "$INSTALLATION" --yes --bucket gs://bkt-sandbox-a
  expect_status "$status" 0 "the bucket rows"
  expect_fact cancel-marker PASS "the bucket row"
  expect_contains "$(fact_line cancel-marker)" "NO cancel marker" "the bucket row: what it read"
  local calls
  calls=$(gcloud_calls)
  expect_contains "$calls" "storage ls gs://bkt-sandbox-a/runs/$SLUG/" "the bucket row: the read"
  [ "$(printf '%s\n' "$calls" | wc -l | tr -d ' ')" = 2 ] || fail "the bucket row: expected two gcloud reads, got: $calls"
}

test_a_cancel_marker_in_the_bucket_is_a_fail() {
  setup_case bucket-marker
  seed_history
  MODE=bucket-marker
  run440 --installation "$INSTALLATION" --yes --bucket bkt-sandbox-a
  expect_status "$status" 1 "a cancel marker in the bucket"
  expect_fact cancel-marker FAIL "a cancel marker in the bucket"
  expect_contains "$(fact_line cancel-marker)" "must never write" "a cancel marker: what it means"
}

test_a_bucket_that_cannot_be_read_is_a_fail() {
  setup_case bucket-fail
  seed_history
  MODE=bucket-fail
  run440 --installation "$INSTALLATION" --yes --bucket bkt-sandbox-a
  expect_status "$status" 1 "a bucket that cannot be listed"
  expect_fact cancel-marker FAIL "a bucket that cannot be listed"
}

# --------------------------------------------------------------- the guards
test_a_dry_run_makes_no_call_at_all() {
  setup_case dry-run
  seed_history
  run440 --installation "$INSTALLATION" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_no_calls "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: its own row"
  expect_contains "$out" "CHECK 440 RESULT END (exit 0)" "a dry run: the block ends at 0"
  expect_absent "$out" "FACT runs-launched:" "a dry run: no launch row"
  expect_contains "$(cat "$dir/err")" "fugaro run --run-id" "a dry run: the launch it would make"
  expect_contains "$(cat "$dir/err")" "Nothing was launched, read or cancelled" "a dry run: what it did"
  expect_scratch_gone "a dry run"
}

test_the_installation_named_belong_is_refused() {
  setup_case belong
  seed_history
  run440 --installation belong --yes
  expect_status "$status" 2 "the belong guard"
  expect_no_calls "the belong guard"
  expect_fact belong FAIL "the belong guard"
  expect_contains "$(cat "$dir/err")" "matches the installation 'belong'" "the belong guard: the message"
}

test_a_coding_agents_session_is_refused() {
  setup_case agent
  seed_history
  RUN440_ENV=(CLAUDECODE=1)
  run440_default
  expect_status "$status" 2 "a coding agent's session"
  expect_no_calls "a coding agent's session"
  expect_fact agent-session FAIL "a coding agent's session"
  expect_contains "$(cat "$dir/err")" "no agent runs any step of it" "a coding agent's session: whose check it is"
}

test_yes_is_required_before_a_run_is_launched() {
  setup_case no-yes
  seed_history
  run440 --installation "$INSTALLATION"
  expect_status "$status" 2 "no --yes"
  expect_fact yes FAIL "no --yes"
  expect_absent "$(fugaro_calls)" "run --run-id" "no --yes: nothing was launched"
  expect_contains "$(fact_line yes)" "billable" "no --yes: why"
  expect_contains "$(cat "$dir/err")" "stopped: --yes was not given" "no --yes: where it stopped"
}

test_a_directory_that_is_not_a_checkout_is_refused() {
  setup_case no-checkout
  seed_history
  rm -f "$dir/repo/fugaro.yaml"
  run440_default
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_fact checkout FAIL "a directory that is not a checkout"
  expect_absent "$(fugaro_calls)" "run --run-id" "a directory that is not a checkout: nothing was launched"
}

test_help_prints_no_block() {
  setup_case help
  run440 --help
  expect_status "$status" 0 "--help"
  expect_absent "$out" "CHECK 440 RESULT" "--help: no block"
  expect_no_calls "--help"
}

# --------------------------------------------------------------- the readings
test_a_launch_that_fails_stops_the_run() {
  setup_case launch-fail
  seed_history
  MODE=launch-fail
  run440_default
  expect_status "$status" 2 "a launch that fails"
  expect_contains "$(fact_line aborted)" "the launch of" "a launch that fails: the reason"
  expect_fact restore FAIL "a launch that fails: the Restore"
  expect_contains "$(fact_line restore)" "may still be going" "a launch that fails: what to do"
}

test_a_launch_that_hangs_is_bounded() {
  setup_case launch-sleep
  seed_history
  MODE=launch-sleep
  LAUNCH_TIMEOUT=1
  run440_default
  expect_status "$status" 2 "a launch that hangs"
  expect_contains "$(fact_line aborted)" "did not finish inside" "a launch that hangs: the bound"
}

test_a_listing_that_fails_stops_the_run() {
  setup_case list-fails
  seed_history
  MODE=list-fails
  run440_default
  expect_status "$status" 2 "a listing that fails"
  expect_contains "$(fact_line aborted)" "runs ls --json --since 7d failed" "a listing that fails: the reason"
}

test_a_listing_whose_json_cannot_be_read_stops_the_run() {
  setup_case list-json
  seed_history
  MODE=list-bad-json
  run440_default
  expect_status "$status" 2 "a listing that is not JSON"
  expect_contains "$(fact_line aborted)" "JSON could not be read" "a listing that is not JSON: the reason"
}

test_a_run_that_ended_before_the_listing_is_a_fail() {
  setup_case launch-terminal
  seed_history
  MODE=launch-terminal
  run440_default
  expect_status "$status" 1 "a run that ended at once"
  expect_fact runs-launched FAIL "a run that ended at once"
  expect_contains "$(fact_line runs-launched)" "terminal=true" "a run that ended at once: what the listing said"
}

test_a_launch_that_names_no_run_stops_the_run() {
  setup_case no-slug
  seed_history
  MODE=launch-no-slug
  run440_default
  expect_status "$status" 2 "a launch that names no run"
  expect_contains "$(fact_line aborted)" "named no run" "a launch that names no run: the reason"
}

test_a_minimum_that_is_not_refused_is_a_fail() {
  setup_case too-short
  seed_history
  MODE=too-short-ok
  run440_default
  expect_status "$status" 1 "a minimum that is not refused"
  expect_fact diagnose-too-short FAIL "a minimum that is not refused"
  expect_contains "$(fact_line diagnose-too-short)" "want 1" "a minimum that is not refused: what it saw"
}

test_a_piped_refusal_without_the_way_out_is_a_fail() {
  setup_case piped
  seed_history
  MODE=piped-no-way-out
  run440_default
  expect_status "$status" 1 "a piped refusal with no way out"
  expect_fact diagnose-refuses-piped FAIL "a piped refusal with no way out"
  expect_contains "$(fact_line diagnose-refuses-piped)" "pass a longer ending" "a piped refusal: what is missing"
}

test_a_command_that_never_asks_is_a_fail() {
  setup_case no-prompt
  seed_history
  MODE=no-prompt
  run440_default
  expect_status "$status" 1 "a command that never asks"
  expect_fact diagnose-pick-terminal FAIL "a command that never asks"
  expect_contains "$(fact_line diagnose-pick-terminal)" "never asked" "a command that never asks: the reading"
}

test_an_answer_that_reads_another_run_is_a_fail() {
  setup_case wrong-answer
  seed_history
  MODE=wrong-answer
  run440_default
  expect_status "$status" 1 "an answer that reads another run"
  expect_fact diagnose-pick-terminal FAIL "an answer that reads another run"
  expect_contains "$(fact_line diagnose-pick-terminal)" "did not diagnose" "an answer that reads another run: the reading"
}

test_a_log_read_that_names_another_run_is_a_fail() {
  setup_case logs-wrong
  seed_history
  MODE=logs-wrong-url
  run440_default
  expect_status "$status" 1 "a log read of another run"
  expect_fact logs-ending FAIL "a log read of another run"
  expect_fact logs-collision FAIL "a log read of another run: through the pick list"
}

test_a_run_with_no_console_link_is_a_fail() {
  setup_case logs-no-url
  seed_history
  MODE=logs-no-url
  run440_default
  expect_status "$status" 1 "a run with no console link"
  expect_fact logs-ending FAIL "a run with no console link"
  expect_contains "$(fact_line logs-ending)" "exited 1" "a run with no console link: what it saw"
}

test_an_ending_that_never_reaches_the_feed_is_a_fail() {
  setup_case no-feed
  seed_history
  MODE=launch-terminal
  ACTIVE_TIMEOUT=2
  run440_default
  expect_status "$status" 1 "an ending that never reaches the feed"
  expect_fact cancel-active-refuses FAIL "an ending that never reaches the feed"
  expect_contains "$(fact_line cancel-active-refuses)" "NOT measured" "an ending that never reaches the feed: what the row says"
}

test_an_ending_that_cancels_something_is_a_fail() {
  setup_case cancel-acts
  seed_history
  MODE=cancel-acts
  run440_default
  expect_status "$status" 1 "an ending that cancels"
  expect_fact cancel-active-refuses FAIL "an ending that cancels"
  expect_contains "$(fact_line cancel-active-refuses)" "CANCELLED a run" "an ending that cancels: what it saw"
  # The run the ending acted on is the finding, so the row says which one.
  expect_contains "$(fact_line cancel-active-refuses)" "$(sed -n 1p "$dir/state/launched")" "an ending that cancels: the run it acted on"
}

test_a_bucket_with_no_old_run_stops_the_run() {
  setup_case no-old-run
  seed_run "$RECENT_ID" "$(iso_ago 1)" true "a recent run" yes
  run440_default
  expect_status "$status" 2 "a bucket with no old run"
  expect_contains "$(fact_line aborted)" "--old-run" "a bucket with no old run: the way out"
  expect_absent "$out" "FACT window-widens: PASS" "a bucket with no old run: nothing widened"
}

test_an_old_run_inside_the_window_is_a_fail() {
  setup_case old-inside
  seed_history
  run440_default --old-run "$SLUG/$RECENT_ID"
  expect_status "$status" 1 "an old run inside the window"
  expect_fact old-run FAIL "an old run inside the window"
  expect_contains "$(fact_line old-run)" "is in the last 7d" "an old run inside the window: what it read"
}

test_an_ending_of_an_active_run_is_not_the_finished_refusal() {
  setup_case old-active
  # The old run is in the feed and outside the window: `cancel` then names it as
  # an ACTIVE run, which is not the finished run's refusal.
  seed_run "$OLD_ID" "$(iso_ago 9)" false "an old run" no
  seed_run "$RECENT_ID" "$(iso_ago 1)" true "a recent run" yes
  run440_default
  expect_status "$status" 1 "an old run that is active"
  expect_fact cancel-finished-refuses FAIL "an old run that is active"
  expect_contains "$(fact_line cancel-finished-refuses)" "not with the finished run's own sentence" "an old run that is active: what the sentence has to be"
}

test_a_lookup_that_does_not_widen_is_a_fail() {
  setup_case no-widen
  seed_history
  MODE=no-widen
  run440_default
  expect_status "$status" 1 "a lookup that does not widen"
  expect_fact window-widens FAIL "a lookup that does not widen"
  expect_contains "$(fact_line window-widens)" "exited 1" "a lookup that does not widen: what it saw"
}

test_a_restore_that_cannot_cancel_is_a_fail() {
  setup_case restore-fails
  seed_history
  MODE=cancel-fails
  run440_default
  expect_status "$status" 1 "a Restore that cannot cancel"
  expect_fact restore FAIL "a Restore that cannot cancel"
  expect_contains "$(fact_line restore)" "the backend refused" "a Restore that cannot cancel: what it saw"
}

test_a_restore_whose_graceful_cancel_does_not_answer_still_stops_the_runs() {
  setup_case restore-slow
  seed_history
  MODE=cancel-slow
  ACTIVE_TIMEOUT=2
  run440_default
  expect_status "$status" 0 "a Restore whose graceful cancel does not answer"
  expect_fact restore PASS "a Restore whose graceful cancel does not answer"
  # The two runs are the point: a bound that gives up on the graceful cancel
  # without cancelling the execution leaves exactly what this trap exists to
  # prevent, so the case reads the markers the `--now` fallback wrote.
  expect_cancelled "a Restore whose graceful cancel does not answer"
  expect_contains "$(cat "$dir/err")" "cancelling the execution at once with --now" "a Restore whose graceful cancel does not answer: the fallback it took"
}

test_the_scratch_directory_can_be_kept() {
  setup_case keep-scratch
  seed_history
  RUN440_ENV=(CHECK440_KEEP_SCRATCH=1)
  run440_default
  expect_status "$status" 0 "a kept scratch directory"
  local scratch
  scratch=$(scratch_of)
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch is gone"
  expect_contains "$(cat "$dir/err")" "kept: $scratch" "a kept scratch directory: the message"
  expect_absent "$out" "FACT scratch: FAIL" "a kept scratch directory: it is not a failure"
  rm -rf "$scratch"
}

test_the_block_never_carries_a_run_of_the_owners_own() {
  setup_case no-owner-runs
  seed_history
  run440_default
  # The block is what the owner pastes back: it carries this run's own two IDs
  # and the seeded ones, and nothing else that looks like a run.
  local ids
  ids=$(launched_ids)
  expect_contains "$out" "$(printf '%s\n' "$ids" | sed -n 1p)" "the block names the first run"
  expect_contains "$out" "$(printf '%s\n' "$ids" | sed -n 2p)" "the block names the second run"
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

t test_a_clean_run_settles_every_rule
t test_the_pick_list_is_read_at_a_terminal_and_answered
t test_the_bucket_rows_are_read_when_the_bucket_is_given
t test_a_cancel_marker_in_the_bucket_is_a_fail
t test_a_bucket_that_cannot_be_read_is_a_fail
t test_a_dry_run_makes_no_call_at_all
t test_the_installation_named_belong_is_refused
t test_a_coding_agents_session_is_refused
t test_yes_is_required_before_a_run_is_launched
t test_a_directory_that_is_not_a_checkout_is_refused
t test_help_prints_no_block
t test_a_launch_that_fails_stops_the_run
t test_a_launch_that_hangs_is_bounded
t test_a_listing_that_fails_stops_the_run
t test_a_listing_whose_json_cannot_be_read_stops_the_run
t test_a_run_that_ended_before_the_listing_is_a_fail
t test_a_launch_that_names_no_run_stops_the_run
t test_a_minimum_that_is_not_refused_is_a_fail
t test_a_piped_refusal_without_the_way_out_is_a_fail
t test_a_command_that_never_asks_is_a_fail
t test_an_answer_that_reads_another_run_is_a_fail
t test_a_log_read_that_names_another_run_is_a_fail
t test_a_run_with_no_console_link_is_a_fail
t test_an_ending_that_never_reaches_the_feed_is_a_fail
t test_an_ending_that_cancels_something_is_a_fail
t test_a_bucket_with_no_old_run_stops_the_run
t test_an_old_run_inside_the_window_is_a_fail
t test_an_ending_of_an_active_run_is_not_the_finished_refusal
t test_a_lookup_that_does_not_widen_is_a_fail
t test_a_restore_that_cannot_cancel_is_a_fail
t test_a_restore_whose_graceful_cancel_does_not_answer_still_stops_the_runs
t test_the_scratch_directory_can_be_kept
t test_the_block_never_carries_a_run_of_the_owners_own

if [ "$failures" -ne 0 ]; then
  echo "check440_test: $failures test(s) failed" >&2
  exit 1
fi
echo "check440_test: all tests passed"
