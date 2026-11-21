#!/usr/bin/env bash
# Tests for scripts/live-checks/check-760-runs-ls-speed.sh, against fake tools on
# PATH: nothing here reaches the network, no credential exists, no Fugaro
# installation is read and no runs bucket is touched. The fake `fugaro` is a few
# lines of bash answering `doctor --json` and `runs ls` from FAKE760_* knobs and
# recording every call; the fake `git` fails every call, so a check that ran git
# of its own (beyond the one guard on its own scratch directory) would show up
# here as a failure.
#
# What the fakes model is the SHAPE of the reads the check makes, because that is
# what the check is: which query each `runs ls` call is (`--state running`,
# `--pr N` with and without `--no-cache`, the window's `--since 7d`), whether it
# was run once untimed first (the cache warm-up) and how long it took — the fake
# can sleep a fixed plan, so a case can say the median printed is the MIDDLE
# sample and not the first, the mean or the last, and that a call which never
# answers is held to CHECK760_CALL_SECONDS and killed with its process group.
# The pull-request reads answer a table of rows and write the CLI's own stderr
# note for a pull request whose index entry is missing, which is how a case says
# which of the two paths (`pr-rows`) each read took.
#
# Usage: bash scripts/live-checks/check-760-runs-ls-speed_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-760-runs-ls-speed.sh
bash_bin=$(command -v bash)
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'if [ -n "${CHECK760_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi' EXIT

failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }

# rows_of_block TEXT — the block's own rows, one short line each, so a failure
# names what the run recorded instead of pasting the whole block into the log.
rows_of_block() {
  printf '%s\n' "$1" | grep '^FACT ' | cut -c1-110 | tr '\n' '|' | head -c 1400
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; the rows read: $(rows_of_block "$1")" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; the rows read: $(rows_of_block "${out:-}")"
}
# expect_last_line OUT WANT WHAT — the block's own last line, exactly. It is the
# line a reader takes as the answer, and the one the exit status has to agree
# with.
expect_last_line() { # out want what
  local last
  last=$(printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1)
  [ "$last" = "$2" ] || fail "$3: the block's last line reads '$last', want '$2'"
}
# expect_block_keys OUT WHAT — every key, exactly once, in the block's own order,
# with the head line and the verdict. The order is part of the promise (two runs'
# blocks are meant to be diffable), so it is checked here rather than by eye.
expect_block_keys() { # out what
  local key count want="" got
  expect_contains "$1" "CHECK 760 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
    count=$(printf '%s\n' "$1" | grep -c "^FACT $key:" || true)
    [ "${count:-0}" -eq 1 ] || fail "$2: the $key row appears ${count:-0} time(s), want exactly one"
    want="${want}${want:+,}${key}"
  done
  got=$(printf '%s\n' "$1" | sed -n 's/^FACT \([^:]*\):.*/\1/p' | tr '\n' ',' )
  got=${got%,}
  [ "$got" = "$want" ] || fail "$2: the rows are printed as '$got', want '${want}' (the block's own order)"
  count=$(printf '%s\n' "$1" | grep -c '^CHECK 760 RESULT' || true)
  [ "${count:-0}" -eq 2 ] || fail "$2: the block has ${count:-0} CHECK 760 RESULT lines, want 2 (the head and the verdict)"
}
scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}
expect_scratch_removed() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  [ -n "$scratch" ] || return 0
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}
# expect_no_reads DIR WHAT — the fake `fugaro` was never called: a guard that
# refused only after a read would leave a line in its log, and a guard checked by
# a file nothing writes proves nothing. The check's own scratch guard calls `git`
# before the guards run, so only `fugaro` is asserted here; a case that must hold
# before the scratch directory exists uses expect_nothing_read.
expect_no_reads() {
  local dir=$1 what=$2
  [ ! -s "$dir/fugaro.log" ] || fail "$what: the check called fugaro before it should have: $(tr '\n' ' ' <"$dir/fugaro.log")"
}
# expect_nothing_read DIR WHAT — not even the scratch guard ran: the run stopped
# before it made a scratch directory at all.
expect_nothing_read() {
  local dir=$1 what=$2
  expect_no_reads "$dir" "$what"
  [ ! -s "$dir/git.log" ] || fail "$what: the check ran git before it should have: $(tr '\n' ' ' <"$dir/git.log")"
}
# expect_only_reads DIR WHAT — every call the check made is a read. This is the
# check's whole promise: it writes nothing on the installation, in the project or
# in the runs bucket. `doctor` and `runs ls` are the only two verbs it knows.
expect_only_reads() {
  local dir=$1 what=$2 bad
  bad=$(grep -v -e '^doctor ' -e '^runs ls ' "$dir/fugaro.log" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$what: fugaro was asked to do something else: $(printf '%s' "$bad" | tr '\n' ' ')"
  # The ONE git call the check makes is the guard on its own scratch directory
  # (`git rev-parse --show-toplevel`, to refuse a scratch inside a work tree);
  # anything else means the check needed a checkout.
  bad=$(grep -v -x 'rev-parse --show-toplevel' "$dir/git.log" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$what: the check ran git of its own: $(printf '%s' "$bad" | tr '\n' ' ')"
}
expect_called() { # dir tool what
  local dir=$1 tool=$2
  [ -s "$dir/$tool.log" ] || fail "$3: the check never called $tool"
}
expect_calls() { # dir pattern want what — how many calls match the pattern
  local dir=$1 pattern=$2 want=$3 got
  got=$(grep -c -- "$pattern" "$dir/fugaro.log" 2>/dev/null || true)
  [ "${got:-0}" -eq "$want" ] || fail "$4: ${got:-0} call(s) match '$pattern', want $want: $(tr '\n' ' ' <"$dir/fugaro.log")"
}
expect_no_calls() { # dir pattern what
  local dir=$1 pattern=$2 got
  got=$(grep -c -- "$pattern" "$dir/fugaro.log" 2>/dev/null || true)
  [ "${got:-0}" -eq 0 ] || fail "$3: the check made ${got:-0} call(s) matching '$pattern': $(tr '\n' ' ' <"$dir/fugaro.log")"
}

# expect_median_is_the_middle_sample OUT KEY WHAT — the figure a row prints is the
# median of the samples it prints beside it, and not the first, the last or the
# mean. The fake sleeps a plan (0.05 s, 0.05 s, 0.45 s for the three timed runs),
# so the mean and the last sample are both far from the median and this cannot
# pass by accident.
expect_median_is_the_middle_sample() { # out key what
  local row median samples mid last spread
  row=$(printf '%s\n' "$1" | grep -m1 "^FACT $2:" || true)
  [ -n "$row" ] || {
    fail "$3: there is no $2 row"
    return 0
  }
  median=$(printf '%s' "$row" | sed -n 's/.*median \([0-9][0-9.]*\) s over.*/\1/p')
  samples=$(printf '%s' "$row" | sed -n 's/.*runs (\(.*\)) for.*/\1/p' | grep -o '[0-9][0-9]*\.[0-9][0-9]')
  mid=$(printf '%s\n' "$samples" | sort -n | sed -n '2p')
  last=$(printf '%s\n' "$samples" | tail -n 1)
  spread=$(printf '%s\n' "$samples" | sort -n | awk 'NR == 1 { lo = $1 } { hi = $1 } END { printf "%.2f", hi - lo }')
  awk -v m="$median" -v mid="$mid" 'BEGIN { exit (m > mid - 0.03 && m < mid + 0.03) ? 0 : 1 }' ||
    fail "$3: the $2 row prints the median $median and the middle of its samples is $mid"
  # With a spread the row's last sample is far from the median, so a row printing
  # the last one (or the mean) is caught here rather than looked at by eye.
  awk -v sp="$spread" 'BEGIN { exit (sp > 0.2) ? 0 : 1 }' || return 0
  awk -v m="$median" -v last="$last" 'BEGIN { exit (m < last - 0.2) ? 0 : 1 }' ||
    fail "$3: the $2 row prints $median, which is its LAST sample ($last) rather than the median of the three"
}

# Every fact key the run's block must carry, whatever it measured. This is the
# script's own BLOCK_KEYS, in its own order.
BLOCK_KEYS=(xtrace agent-session installation belong warmup
  query-state-running query-pr query-pr-no-cache query-since-7d
  rl4-gate pr-rows restore)

# The installation, the repository and the pull requests the fakes answer with.
# #41 is the newest the window names (two runs of its own, so the sanity reading
# has a count above one), and #200 is the oldest OTHER one — the pull request
# whose index entry the fake says is missing, which is what makes its read fall
# back to the scan.
INST='acme'
REPO='acme/app'
PR_NEW='41'
PR_OLD='200'
RUN_NEW1='20261118-090909-cccc'
RUN_NEW2='20261118-080808-dddd'
RUN_OLD='20261118-070707-eeee'

# The knobs a case sets before it calls run760; every one of them is reset after
# the run, so a case that forgets to clear one cannot leak into the next.
RUN760_ARGS=()
RUN760_DOCTOR=""
RUN760_RUNS=""
RUN760_NOTE_OLD=""
RUN760_NOTE_NEW=""
RUN760_SLEEP=""
RUN760_ENV=()
RUN760_SHELL=""
RUN760_PATH=""
RUN760_SCRIPT=""
RUN760_RUNS_COUNT=""
RUN760_CALL_SECONDS=""
RUN760_TARGET_MS=""
RUN760_GATE_MS=""
RUN760_BASELINE_MS=""

# --------------------------------------------------------------------- the fakes
write_fakes() { # dir
  local dir=$1
  cat >"$dir/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro`, for check-760-runs-ls-speed.sh's test only: it answers
# `doctor --json` and the four `runs ls` reads the check makes, and records every
# call. It writes nothing anywhere, reads no credential and reaches no cloud.
set -u
printf '%s\n' "$*" >>"${FAKE760_LOG:-/dev/null}"

json_string() { # TEXT — the text as the body of a JSON string
  local text=$1
  text=${text//\\/\\\\}
  text=${text//\"/\\\"}
  text=${text//$'\n'/\\n}
  printf '%s' "$text"
}

# row JSON PR RUN_ID REPO — one row of a `runs ls --json` document, carrying only
# the three fields the check's reader looks at.
row() {
  printf '{"run":"%s","run_id":"%s","repo":"%s","pr":%s,"status":"succeeded"}' "$2" "$2" "$3" "$1"
}

# rows_of PR — the runs of one pull request, newest first. A pull request the
# table does not know has no runs at all.
rows_of() {
  case "$1" in
  41) printf '%s,%s' "$(row 41 "$FAKE760_RUN_NEW1" "$FAKE760_REPO")" "$(row 41 "$FAKE760_RUN_NEW2" "$FAKE760_REPO")" ;;
  200) row 200 "$FAKE760_RUN_OLD" "$FAKE760_REPO" ;;
  *) printf '' ;;
  esac
}

# snooze KIND — how long this call takes. `plan` makes ONE call of each kind the
# slow one, chosen so that it is the LAST of the three timed runs of that query:
# the queries warmed once (the live state, the pull request, the default listing —
# whose kind the window read shares) have four or five calls, and the no-cache
# read, which by definition is never warmed, has three. The three samples of every
# query therefore come out 0.05 s, 0.05 s, 0.45 s: a row that printed the last
# sample or the mean instead of the median could not match itself. `slow-pr` is a
# pull-request read that never answers, for the deadline case.
snooze() {
  local secs=0 n=0 slow=4
  case "$1" in
  pr-nocache) slow=3 ;;
  window) slow=5 ;;
  esac
  case "${FAKE760_SLEEP:-none}" in
  plan)
    n=$(cat "$FAKE760_COUNT/$1.n" 2>/dev/null || echo 0)
    n=$((n + 1))
    printf '%s' "$n" >"$FAKE760_COUNT/$1.n"
    case "$n" in "$slow") secs=0.45 ;; *) secs=0.05 ;; esac
    ;;
  slow-pr)
    case "$1" in pr-cache) secs=30 ;; esac
    ;;
  esac
  [ "$secs" = 0 ] || python3 -c "import time; time.sleep($secs)"
}

case "${1:-}" in
doctor)
  case "${FAKE760_DOCTOR:-ok}" in
  fail)
    echo "doctor: no usable credentials on this machine" >&2
    exit 2
    ;;
  no-name) printf '{"ok":true,"project":{"name":"","gcp_project":"fugaro-dev","region":"us-east5"}}\n' ;;
  belongs) printf '{"ok":true,"project":{"name":"belong","gcp_project":"fugaro-dev","region":"us-east5"}}\n' ;;
  bad-json) printf 'not a document at all\n' ;;
  *)
    printf '{"ok":true,"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}\n'
    ;;
  esac
  ;;
runs)
  kind=other
  pr=""
  nocache=no
  want_json=no
  have_since=no
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
    --state) kind=state ;;
    --since) have_since=yes ;;
    --pr)
      kind=pr
      shift
      pr=${1:-}
      ;;
    --no-cache) nocache=yes ;;
    --json) want_json=yes ;;
    esac
    shift
  done
  # The window read is the one with --since and no --pr; the no-cache read is the
  # one pull-request query that is never warmed, so it is counted on its own.
  if [ "$kind" = other ] && [ "$have_since" = yes ]; then kind=window; fi
  if [ "$kind" = pr ] && [ "$nocache" = yes ]; then kind=pr-nocache; fi
  if [ "$kind" = pr ]; then kind=pr-cache; fi
  snooze "$kind"
  case "${FAKE760_RUNS:-ok}" in
  fail)
    echo "runs ls: the bucket refused the call" >&2
    exit 3
    ;;
  pr-fail)
    case "$kind" in
    pr-cache | pr-nocache)
      echo "runs ls: the bucket refused the call" >&2
      exit 3
      ;;
    esac
    ;;
  since-fail)
    # The default listing's own timed read, and only it: the window read that
    # finds the pull requests is the same query with --json, and it answers.
    if [ "$kind" = window ] && [ "$want_json" = no ]; then
      echo "runs ls: the bucket refused the call" >&2
      exit 3
    fi
    ;;
  window-bad-json)
    if [ "$kind" = window ]; then
      printf 'this is not the JSON this check knows\n'
      exit 0
    fi
    ;;
  esac
  case "$kind" in
  window)
    case "${FAKE760_RUNS:-ok}" in
    empty-window) printf '{"version":1,"kind":"runs","runs":[]}\n' ;;
    one-pr)
      printf '{"version":1,"kind":"runs","runs":[%s,%s]}\n' \
        "$(row 41 "$FAKE760_RUN_NEW1" "$FAKE760_REPO")" "$(row 41 "$FAKE760_RUN_NEW2" "$FAKE760_REPO")"
      ;;
    *)
      printf '{"version":1,"kind":"runs","runs":[%s,%s,%s]}\n' \
        "$(row 41 "$FAKE760_RUN_NEW1" "$FAKE760_REPO")" \
        "$(row 41 "$FAKE760_RUN_NEW2" "$FAKE760_REPO")" \
        "$(row 200 "$FAKE760_RUN_OLD" "$FAKE760_REPO")"
      ;;
    esac
    ;;
  state)
    printf '{"version":1,"kind":"runs","runs":[]}\n'
    ;;
  pr-cache | pr-nocache)
    # Which path this read took is the CLI's own note on stderr, written for a
    # pull request whose index entry is missing (or unreadable) — the same two
    # sentences the real `runs ls --pr` prints.
    if [ "$pr" = "$FAKE760_PR_OLD" ]; then
      case "${FAKE760_NOTE_OLD:-no-entry}" in
      no-entry) printf 'note: no pull-request index for %s#%s: reading every run of the window, as it did before the index existed\n' "$FAKE760_REPO" "$pr" >&2 ;;
      unreadable) printf 'note: the pull-request index of %s#%s could not be read: the object is gone; reading every run of the window instead\n' "$FAKE760_REPO" "$pr" >&2 ;;
      esac
    else
      case "${FAKE760_NOTE_NEW:-none}" in
      no-entry) printf 'note: no pull-request index for %s#%s: reading every run of the window, as it did before the index existed\n' "$FAKE760_REPO" "$pr" >&2 ;;
      unreadable) printf 'note: the pull-request index of %s#%s could not be read: the object is gone; reading every run of the window instead\n' "$FAKE760_REPO" "$pr" >&2 ;;
      esac
    fi
    if [ "$want_json" = yes ]; then
      printf '{"version":1,"kind":"runs","runs":[%s]}\n' "$(rows_of "$pr")"
    else
      printf '{"version":1,"kind":"runs","runs":[%s]}\n' "$(rows_of "$pr")"
    fi
    ;;
  *)
    printf '{"version":1,"kind":"runs","runs":[]}\n'
    ;;
  esac
  ;;
*)
  echo "fugaro: unknown call" >&2
  exit 2
  ;;
esac
FUGARO_EOF

  cat >"$dir/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A fake `git` that fails every call: the check runs no git of its own and needs
# no checkout, and this is where that would show. Its ONE expected call is the
# guard on its own scratch directory (`rev-parse --show-toplevel`).
set -u
printf '%s\n' "$*" >>"${FAKE760_GIT_LOG:-/dev/null}"
echo "git: this check runs no git of its own" >&2
exit 128
GIT_EOF
  chmod +x "$dir/bin/fugaro" "$dir/bin/git"
}

# run760 DIR — runs the check once against the fakes. Sets $status, $out and
# $err. RUN760_ARGS appends arguments, RUN760_ENV adds environment entries,
# RUN760_SHELL replaces the interpreter the check is started with (the one place
# a tracing shell can be arranged), RUN760_PATH=none takes the fakes off PATH
# (the case that has to hold with no `fugaro` on PATH at all), RUN760_SCRIPT
# runs a rewritten copy of the check, and the RUN760_* knobs pick what each fake
# answers and what the check is told.
run760() {
  local dir=$work/$1 path f n
  mkdir -p "$dir/bin" "$dir/tmp" "$dir/notools" "$dir/home" "$dir/counts"
  : >"$dir/fugaro.log"
  : >"$dir/git.log"
  write_fakes "$dir"
  path="$dir/bin:$PATH"
  # Every case names its installation, the way an owner runs this check: the
  # default (`fugaro`) is what the argument-less case and the plan's own line
  # cover, and a case that gives --installation of its own overrides this one.
  local -a args=(--installation "$INST")
  args+=(${RUN760_ARGS[@]+"${RUN760_ARGS[@]}"})
  if [ "${RUN760_PATH:-}" = none ]; then
    # "No cloud tool on PATH" means exactly that: a PATH that holds every system
    # tool the check needs and NO `fugaro`. An empty PATH would take `tr`, `grep`
    # and `cat` away too, and the case would then be measuring the shell rather
    # than the check's own tool guard.
    for f in /usr/bin/* /bin/* /usr/local/bin/*; do
      n=${f##*/}
      case "$n" in
      fugaro | git) continue ;;
      esac
      [ -e "$dir/notools/$n" ] || [ -L "$dir/notools/$n" ] || ln -s "$f" "$dir/notools/$n"
    done
    path="$dir/notools"
  fi
  status=0
  out=""
  err=""
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  env -i PATH="$path" TMPDIR="$dir/tmp" HOME="$dir/home" \
    ${RUN760_ENV[@]+"${RUN760_ENV[@]}"} \
    CHECK760_RUNS="${RUN760_RUNS_COUNT:-3}" \
    CHECK760_CALL_SECONDS="${RUN760_CALL_SECONDS:-20}" \
    ${RUN760_TARGET_MS:+CHECK760_TARGET_MS="$RUN760_TARGET_MS"} \
    ${RUN760_GATE_MS:+CHECK760_GATE_MS="$RUN760_GATE_MS"} \
    ${RUN760_BASELINE_MS:+CHECK760_BASELINE_STATE_RUNNING_MS="$RUN760_BASELINE_MS" CHECK760_BASELINE_PR_MS="$RUN760_BASELINE_MS" CHECK760_BASELINE_PR_NO_CACHE_MS="$RUN760_BASELINE_MS" CHECK760_BASELINE_SINCE_7D_MS="$RUN760_BASELINE_MS"} \
    FAKE760_LOG="$dir/fugaro.log" FAKE760_GIT_LOG="$dir/git.log" \
    FAKE760_COUNT="$dir/counts" FAKE760_REPO="$REPO" \
    FAKE760_PR_OLD="$PR_OLD" FAKE760_RUN_NEW1="$RUN_NEW1" \
    FAKE760_RUN_NEW2="$RUN_NEW2" FAKE760_RUN_OLD="$RUN_OLD" \
    FAKE760_DOCTOR="${RUN760_DOCTOR:-ok}" FAKE760_RUNS="${RUN760_RUNS:-ok}" \
    FAKE760_NOTE_OLD="${RUN760_NOTE_OLD:-no-entry}" FAKE760_NOTE_NEW="${RUN760_NOTE_NEW:-none}" \
    FAKE760_SLEEP="${RUN760_SLEEP:-none}" \
    ${RUN760_SHELL:-$bash_bin} "${RUN760_SCRIPT:-$script}" ${args[@]+"${args[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  # A row's own text is TEXT: the check quotes and never evaluates it, so a
  # backtick (or a $( in a sentence) that the shell ran would show up here as a
  # command the check never meant to run. The check's own rows are the only thing
  # that writes to stderr, so this catches the whole class.
  case "$err" in
  *"command not found"*) fail "the check ran a command out of its own text: $(printf '%s' "$err" | grep -m1 'command not found')" ;;
  esac
  RUN760_ARGS=()
  RUN760_DOCTOR=""
  RUN760_RUNS=""
  RUN760_NOTE_OLD=""
  RUN760_NOTE_NEW=""
  RUN760_SLEEP=""
  RUN760_ENV=()
  RUN760_SHELL=""
  RUN760_PATH=""
  RUN760_SCRIPT=""
  RUN760_RUNS_COUNT=""
  RUN760_CALL_SECONDS=""
  RUN760_TARGET_MS=""
  RUN760_GATE_MS=""
  RUN760_BASELINE_MS=""
}

# rewrite_script DEST OLD NEW — a copy of the check with one exact line replaced,
# so a case can prove the check's own reporting is not vacuous (a step that never
# ran is INCOMPLETE, not a pass) rather than argue it.
rewrite_script() { # dest old new
  python3 - "$script" "$1" "$2" "$3" <<'PY'
import sys

src, dest, old, new = sys.argv[1:5]
with open(src) as fh:
    text = fh.read()
if old not in text:
    raise SystemExit("rewrite_script: the line to replace is not in the script: %r" % old)
with open(dest, "w") as fh:
    fh.write(text.replace(old, new, 1))
PY
  chmod +x "$1"
}

# ------------------------------------------------------------------ the tests
# The all-PASS run: every read answers, the window names #41 (newest) and #200
# (the oldest other), #41 is read through its index entry and #200 falls back to
# the scan, and every figure is under its target.
test_happy_path() {
  run760 happy
  expect_status "$status" 0 "the happy path"
  expect_last_line "$out" "CHECK 760 RESULT: PASS (exit 0)" "the happy path"
  expect_block_keys "$out" "the happy path"
  expect_contains "$out" "FACT xtrace: PASS" "the shell is not tracing"
  expect_contains "$out" "FACT agent-session: PASS" "the owner's own session"
  expect_contains "$out" "FACT installation: PASS — the installation 'fugaro' in GCP project fugaro-dev (region us-east5), read from 'fugaro doctor --project ${INST}" "the installation is read"
  expect_contains "$out" "FACT belong: PASS" "belong is refused by name"
  expect_contains "$out" "FACT warmup: PASS" "the window read is the warm-up"
  expect_contains "$out" "ran once untimed and answered 3 row(s)" "the warmup row says what the window answered"
  expect_contains "$out" "the newest pull request it names is #${PR_NEW}, the oldest other one it names is #${PR_OLD}" "the warmup row names both pull requests"
  expect_contains "$out" "every query below is then run once untimed before its 3 timed runs" "the warm-up is part of the row"
  expect_contains "$out" "except '--pr N --no-cache', which reads the bucket by definition" "the no-cache query is the one nothing can warm"
  expect_contains "$out" "The pull-request queries read --repo ${REPO}" "the pull-request reads are scoped"
  expect_contains "$out" "FACT query-state-running: PASS" "the live state is timed"
  expect_contains "$out" "for 'fugaro runs ls --project ${INST} --state running'" "the state row names the call it timed"
  expect_contains "$out" "beside the 2026-11-19 baseline 20.00 s: FASTER" "the state row reads the baseline"
  expect_contains "$out" "TARGET under 5.00 s: MET" "the state row reads the plan's target"
  expect_contains "$out" "FACT query-pr: PASS" "the pull-request read is timed"
  expect_contains "$out" "beside the 2026-11-19 baseline 23.00 s" "the pull-request row reads its own baseline"
  expect_contains "$out" "FACT query-pr-no-cache: PASS" "the same read without the cache is timed"
  expect_contains "$out" "beside the 2026-11-19 baseline 32.00 s" "the no-cache row reads its own baseline"
  expect_contains "$out" "FACT query-since-7d: PASS" "the default listing is timed"
  expect_contains "$out" "beside the 2026-11-19 baseline 15.00 s" "the default listing reads its own baseline"
  expect_contains "$out" "FACT rl4-gate: PASS" "the gate is read off the default listing"
  expect_contains "$out" "RL4 IS NOT WANTED" "a listing under the gate means RL4 is never built"
  expect_contains "$out" "under 8 seconds means never" "the gate row carries the plan's own words"
  expect_contains "$out" "FACT pr-rows: PASS" "the sanity reading is taken"
  expect_contains "$out" "#${PR_NEW} in ${REPO}: 2 row(s) — run ids ${RUN_NEW2} ${RUN_NEW1} — read through the INDEX" "the timed pull request's rows are read off the index"
  expect_contains "$out" "#${PR_OLD} in ${REPO}, the oldest other one the window holds: 1 row(s) — run ids ${RUN_OLD} — read through the FALLBACK SCAN" "the second pull request's rows come from the scan"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory is removed"
  expect_scratch_removed "$work/happy" "the happy path"
  expect_only_reads "$work/happy" "the happy path"
}

test_the_check_reads_and_writes_nothing() {
  run760 reads
  expect_status "$status" 0 "reads only"
  expect_only_reads "$work/reads" "reads only"
  expect_called "$work/reads" fugaro "reads only"
  # The reads, spelled out: one doctor, the window once untimed, and each query
  # warmed once and then timed three times — except the no-cache read, which by
  # definition cannot be warmed, and the two pull-request reads of the sanity row.
  expect_calls "$work/reads" '^doctor --project acme --json' 1 "reads only: the installation is read once"
  expect_calls "$work/reads" '^runs ls --project acme --since 7d --json' 1 "reads only: the window is read once untimed"
  expect_calls "$work/reads" '^runs ls --project acme --since 7d$' 4 "reads only: the default listing is warmed once and timed three times"
  expect_calls "$work/reads" '^runs ls --project acme --state running$' 4 "reads only: the live state is warmed once and timed three times"
  expect_calls "$work/reads" '^runs ls --project acme --repo acme/app --pr 41$' 4 "reads only: the pull request is warmed once and timed three times"
  expect_calls "$work/reads" '^runs ls --project acme --repo acme/app --pr 41 --no-cache$' 3 "reads only: the no-cache read is timed and never warmed"
  expect_calls "$work/reads" '^runs ls --project acme --repo acme/app --pr 200 --json$' 1 "reads only: the second pull request is read once, for the sanity row"
  expect_calls "$work/reads" 'runs ls .*--json' 3 "reads only: exactly three listings ask for JSON (the window and the sanity row's two)"
  # Nothing that could start a run, write a bucket object or change the config.
  local verb
  for verb in run launch cancel update image budget setup policy secret; do
    expect_no_calls "$work/reads" "^$verb " "reads only: fugaro was never asked to $verb"
  done
}

test_the_check_runs_no_git_of_its_own() {
  run760 no-git
  expect_status "$status" 0 "no git"
  local calls
  calls=$(cat "$work/no-git/git.log" 2>/dev/null || true)
  [ "$calls" = "rev-parse --show-toplevel" ] || fail "no git: git was called with '$calls', want only 'rev-parse --show-toplevel' (the guard on the check's own scratch directory)"
}

# The figures are the whole point of this check, so they have to be the MEDIAN of
# the timed runs: the fake sleeps 0.05 s, 0.05 s and 0.45 s for the three timed
# runs of each query, and a row that printed its first sample, its last sample or
# their mean would not match the middle of its own list.
test_the_medians_are_the_middle_sample() {
  RUN760_SLEEP=plan
  run760 medians
  expect_status "$status" 0 "the medians"
  expect_contains "$out" "over 3 runs (" "the row prints every sample it took"
  expect_median_is_the_middle_sample "$out" query-state-running "the medians"
  expect_median_is_the_middle_sample "$out" query-pr "the medians"
  expect_median_is_the_middle_sample "$out" query-pr-no-cache "the medians"
  expect_median_is_the_middle_sample "$out" query-since-7d "the medians"
  expect_contains "$out" "FACT query-pr: PASS" "a query whose three runs are not alike still has a figure"
}

# A figure over a target is a READING, not a failure: the check says OVER, says
# what over means, and still exits 0 — a check that failed here would turn the
# plan's own baseline into a criterion the owner could not report through.
test_a_row_over_its_target_says_so_and_still_passes() {
  RUN760_TARGET_MS=1
  run760 over-target
  expect_status "$status" 0 "a figure over its target"
  expect_last_line "$out" "CHECK 760 RESULT: PASS (exit 0)" "a figure over its target"
  expect_contains "$out" "TARGET under 1ms: OVER" "the target is read in the smaller unit when it is under a second"
  expect_contains "$out" "over is a READING and not a failure" "the row says what over means"
  expect_contains "$out" "what the plan asks for here is the number" "the row says why it does not fail"
  expect_contains "$out" "FACT query-state-running: PASS" "the row is still a reading"
  expect_absent "$out" "|FAIL|" "nothing failed"
  # A figure over the plan's gate is the whole reason RL4 exists, and it is a
  # PASS too: the decision is the owner's, taken from this row.
  RUN760_GATE_MS=1
  run760 over-gate
  expect_status "$status" 0 "a figure over the gate"
  expect_contains "$out" "OVER the 1ms the plan's D2966 makes the gate: RL4 IS WANTED" "the gate row says which way the reading went"
  expect_contains "$out" "the optional task is built, and this reading, kept beside the query-since-7d row, is what asks for it" "the gate row says what over means for RL4"
}

# The other side of the same sentence: a figure slower than the baseline it is
# compared against says so, and one faster says that (the happy path).
test_a_row_beside_its_baseline_says_slower() {
  RUN760_BASELINE_MS=1
  run760 slower
  expect_status "$status" 0 "a figure slower than every baseline"
  expect_contains "$out" "beside the 2026-11-19 baseline 1ms: SLOWER" "a figure above its baseline says so"
  expect_contains "$out" "FACT rl4-gate: PASS" "the gate still reads the listing's own figure"
}

test_the_gate_reads_the_default_listing() {
  run760 gate
  expect_status "$status" 0 "the gate"
  local listing gate
  listing=$(printf '%s\n' "$out" | grep -m1 '^FACT query-since-7d:')
  gate=$(printf '%s\n' "$out" | grep -m1 '^FACT rl4-gate:')
  # The figure the gate holds is the one the default listing's own row printed,
  # not a second reading of its own.
  local median
  median=$(printf '%s' "$listing" | sed -n 's/.*median \([0-9][0-9.]*\) s over.*/\1/p')
  case "$gate" in
  *"the default listing's median is ${median} s"*) ;;
  *) fail "the gate: the gate row reads '$gate', want the default listing's own median ${median} s" ;;
  esac
  expect_contains "$gate" "the 8.00 s the plan's D2966 makes the gate" "the gate is the plan's 8 s"
  expect_contains "$gate" "UNDER" "a fast listing is under the gate"
  expect_contains "$gate" "so the optional task is not built and this reading is the whole of the decision" "the gate row says what under means"
  # And when the default listing is the query that could not be measured, the
  # gate says so rather than guessing from another row's figure.
  RUN760_RUNS=since-fail
  run760 gate-unmeasured
  expect_status "$status" 2 "a gate with no listing to read"
  expect_contains "$out" "FACT query-since-7d: FAIL" "the default listing has no figure"
  expect_contains "$out" "FACT rl4-gate: FAIL" "an unread gate is a missing reading"
  expect_contains "$out" "the RL4 gate cannot be read" "the gate row says why"
  expect_contains "$out" "missing for the reason the query-since-7d row gives" "the gate row points at the row that explains it"
  expect_contains "$out" "FACT query-state-running: PASS" "the readings that could be taken were taken"
  expect_contains "$out" "FACT pr-rows: PASS" "the sanity reading is still taken"
}

test_the_no_cache_row_has_no_target() {
  run760 no-cache
  expect_status "$status" 0 "the no-cache read"
  expect_contains "$out" "FACT query-pr-no-cache: PASS" "the read is timed"
  expect_contains "$out" "the plan sets no target for this read: the figure is read against its own baseline and nothing else" "the plan gives this read no target"
  expect_contains "$out" "FACT query-since-7d: PASS" "the default listing has no target either"
}

test_the_sanity_row_reads_the_index_and_the_fallback_scan() {
  run760 sanity
  expect_status "$status" 0 "the sanity reading"
  expect_contains "$out" "FACT pr-rows: PASS" "the row is taken"
  expect_contains "$out" "#${PR_NEW} in ${REPO}: 2 row(s) — run ids ${RUN_NEW2} ${RUN_NEW1} — read through the INDEX (no fallback note on its stderr)" "the timed pull request came from the index"
  expect_contains "$out" "#${PR_OLD} in ${REPO}, the oldest other one the window holds: 1 row(s) — run ids ${RUN_OLD} — read through the FALLBACK SCAN (its stderr carried the CLI note that there is no pull-request index entry for that pull request)" "the second one came from the scan"
  expect_contains "$out" "The counts and the run ids are the reading and the whole of it" "the row says what it compared"
  expect_contains "$out" "Nothing of a row's content is read or compared" "the row says what it did not compare"
  # The index that could not be read is the other fallback, and it is named as
  # such rather than reported as an index read.
  RUN760_NOTE_OLD=unreadable
  run760 sanity-unreadable
  expect_status "$status" 0 "an index that could not be read"
  expect_contains "$out" "read through the FALLBACK SCAN (its stderr carried the CLI note that the pull-request index could not be read)" "the unreadable index is named"
}

test_the_sanity_row_when_the_timed_pull_request_fell_back() {
  RUN760_NOTE_NEW=no-entry
  run760 sanity-fell-back
  expect_status "$status" 0 "a timed pull request with no index entry"
  expect_contains "$out" "#${PR_NEW} in ${REPO}: 2 row(s) — run ids ${RUN_NEW2} ${RUN_NEW1} — read through the FALLBACK SCAN" "the timed read's own path is read off its stderr"
  expect_contains "$out" "The second pull request, #${PR_OLD} in ${REPO}" "the second read is still reported"
}

test_the_sanity_row_with_no_second_pull_request() {
  RUN760_RUNS=one-pr
  run760 one-pr
  expect_status "$status" 0 "a window that names one pull request"
  expect_contains "$out" "FACT pr-rows: PASS" "the timed pull request's own rows are still read"
  expect_contains "$out" "No second pull request was read: the window names one pull request only" "the row says there is nothing to compare against"
  expect_contains "$out" "Give --old-pr <N> — one whose runs are older than the backfill" "the row says how to get the comparison"
  expect_contains "$out" "FACT warmup: PASS" "the window was still read"
}

# The owner's own second pull request: named by hand, it is the one read, and the
# row says so rather than claiming the window found it.
test_the_second_pull_request_named_by_hand() {
  RUN760_ARGS=(--old-pr "$PR_OLD" --repo "$REPO")
  run760 old-pr
  expect_status "$status" 0 "a second pull request named by hand"
  expect_contains "$out" "the newest pull request it names is #${PR_NEW}, the second pull request is #${PR_OLD}, the one you named" "the warmup row says which second pull request it will read"
  expect_absent "$out" "named${PR_OLD}" "the value is not printed twice"
  expect_contains "$out" "The second pull request, #${PR_OLD} in ${REPO}, whose runs are the ones you named" "the sanity row says where it came from"
  expect_contains "$out" "#${PR_NEW} in ${REPO}: 2 row(s)" "the timed pull request is still the newest the window names"
}

test_a_pull_request_outside_the_window_needs_a_repository() {
  RUN760_ARGS=(--pr 999)
  run760 pr-unknown
  expect_status "$status" 2 "a pull request the window does not name, with no repository"
  expect_last_line "$out" "CHECK 760 RESULT: INCOMPLETE (exit 2)" "a pull request the window does not name, with no repository"
  expect_contains "$out" "FACT query-pr: FAIL" "the read is missing"
  expect_contains "$out" "the pull request 999 is not among the ones the window (--since 7d) just named" "the row says why"
  expect_contains "$out" "give --repo <owner/name> and run it again" "the row says what to do"
  expect_contains "$out" "FACT query-state-running: PASS" "the readings that could be taken were taken"
  expect_contains "$out" "FACT query-since-7d: PASS" "the default listing is still timed"
  # The check refused to guess a repository, so it never read that pull request.
  expect_no_calls "$work/pr-unknown" '--pr 999' "a pull request outside the window"
}

test_a_pull_request_outside_the_window_with_a_repository() {
  RUN760_ARGS=(--pr 999 --repo "$REPO")
  run760 pr-known
  expect_status "$status" 0 "a pull request the owner named with its repository"
  expect_contains "$out" "FACT query-pr: PASS" "the read is timed"
  expect_contains "$out" "for 'fugaro runs ls --project ${INST} --repo ${REPO} --pr 999'" "the row names the call"
  expect_contains "$out" "FACT pr-rows: PASS" "the sanity read is taken"
  expect_contains "$out" "#999 in ${REPO}: 0 row(s) — run ids none — read through the INDEX" "a pull request with no runs reads as none"
}

test_a_window_that_names_no_pull_request() {
  RUN760_RUNS=empty-window
  run760 no-window
  expect_status "$status" 2 "a window that names no pull request"
  expect_contains "$out" "FACT warmup: PASS" "the window read itself worked"
  expect_contains "$out" "the window names no pull request, and no second pull request is named either" "the warmup row says the window is empty"
  expect_contains "$out" "FACT query-pr: FAIL" "the pull-request reads could not be taken"
  expect_contains "$out" "there is no pull request to time: the window (--since 7d) names none, and --pr was not given" "the row says why"
  expect_contains "$out" "Give one: bash scripts/live-checks/check-760-runs-ls-speed.sh --installation ${INST} --pr <N> [--repo <owner/name>]" "the row says what to give"
  expect_contains "$out" "FACT query-state-running: PASS" "the state read does not need a pull request"
  expect_contains "$out" "FACT query-since-7d: PASS" "the default listing does not either"
  expect_contains "$out" "FACT rl4-gate: PASS" "the gate is still read"
  expect_contains "$out" "FACT pr-rows: FAIL" "there is no row set to read"
  expect_contains "$out" "no pull request was timed, so there is no row set to read" "the sanity row says why"
}

# A refused call is a reading that was not taken, and nothing else: the rows that
# could be read are read, and the block says which figure is missing and what the
# call said.
test_a_refused_call_is_a_missing_reading() {
  RUN760_RUNS=fail
  run760 refused
  expect_status "$status" 2 "every read refused"
  expect_last_line "$out" "CHECK 760 RESULT: INCOMPLETE (exit 2)" "every read refused"
  expect_block_keys "$out" "every read refused"
  expect_contains "$out" "FACT warmup: FAIL" "the window read is missing"
  expect_contains "$out" "the window listing was refused (exit 3): runs ls: the bucket refused the call" "the row carries the call's own exit and words"
  expect_contains "$out" "FACT query-state-running: FAIL" "the state read is missing"
  expect_contains "$out" "FACT query-since-7d: FAIL" "the default listing is missing"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory is still removed"
  expect_scratch_removed "$work/refused" "every read refused"

  RUN760_RUNS=pr-fail
  run760 refused-pr
  expect_status "$status" 2 "only the pull-request reads refused"
  expect_contains "$out" "FACT query-state-running: PASS" "a reading taken is taken"
  expect_contains "$out" "FACT query-since-7d: PASS" "the default listing is still timed"
  expect_contains "$out" "FACT query-pr: FAIL" "the pull-request read is the missing one"
  expect_contains "$out" "FACT rl4-gate: PASS" "the gate still reads what it can"
  expect_contains "$out" "FACT pr-rows: FAIL" "the sanity read is missing too"
}

test_a_call_that_never_answers_is_held_to_its_deadline() {
  RUN760_SLEEP=slow-pr RUN760_CALL_SECONDS=1
  run760 slow
  expect_status "$status" 2 "a pull-request read that never answers"
  expect_contains "$out" "FACT query-pr: FAIL" "the query has no figure"
  expect_contains "$out" "the untimed read that warms the cache never answered within 1s" "the row names the deadline it was held to"
  expect_contains "$out" "FACT query-state-running: PASS" "the queries that answered were timed"
  expect_contains "$out" "FACT query-since-7d: PASS" "the default listing was timed"
  expect_contains "$out" "FACT rl4-gate: PASS" "the gate is still read"
  expect_contains "$out" "FACT pr-rows: FAIL" "the sanity read could not be taken either"
  expect_contains "$out" "FACT restore: PASS" "the killed call's process group left nothing behind"
}

test_a_listing_the_reader_cannot_read_is_a_missing_reading() {
  RUN760_RUNS=window-bad-json
  run760 bad-json
  expect_status "$status" 2 "a window that is not the JSON the reader knows"
  expect_contains "$out" "FACT warmup: FAIL" "the window is a missing reading"
  expect_contains "$out" "could not be read as the JSON this check knows (reader exit 3)" "the row says the reader refused it"
  expect_contains "$out" "FACT query-state-running: PASS" "the other reads are unaffected"
}

test_the_installation_must_be_readable() {
  RUN760_DOCTOR=fail
  run760 doctor-fail
  expect_status "$status" 2 "an installation that cannot be read"
  expect_contains "$out" "FACT installation: FAIL" "the installation row is the failure"
  expect_contains "$out" "answered something this check cannot read (the document is not the JSON the reader expects)" "the row says what came back"
  expect_contains "$out" "doctor: no usable credentials on this machine" "the row carries the call's own words"
  expect_contains "$out" "FACT belong: FAIL — not reached" "belong was not read"
  expect_contains "$out" "Check the name, that this machine's credentials reach that installation" "the refusal says what to check"
  expect_calls "$work/doctor-fail" '^doctor ' 1 "an unreadable installation: the doctor was asked once"
  expect_no_calls "$work/doctor-fail" '^runs ls ' "an unreadable installation: no listing was read at all"

  RUN760_DOCTOR=no-name
  run760 doctor-no-name
  expect_status "$status" 2 "a doctor that answers no installation name"
  expect_contains "$out" "FACT installation: FAIL" "the row fails"
  expect_contains "$out" "answered no installation name this check can read (the reader printed 'name=-')" "the row says what it read"
  expect_no_calls "$work/doctor-no-name" '^runs ls ' "no installation name: no listing was read"
}

test_the_installation_doctor_reads_must_not_name_belong() {
  RUN760_DOCTOR=belongs
  run760 belong-read
  expect_status "$status" 2 "a local config whose installation names belong"
  expect_contains "$out" "FACT belong: FAIL" "the read value has a row of its own"
  expect_contains "$out" "names belong (installation 'belong', GCP project 'fugaro-dev')" "the row says what it read"
  expect_contains "$out" "this check is never run against belong, whatever it was asked for" "the row says why"
  expect_no_calls "$work/belong-read" '^runs ls ' "an installation that names belong: no listing was read"
}

test_belong_is_refused() {
  RUN760_ARGS=(--installation belong)
  run760 belong-install
  expect_status "$status" 2 "belong as the installation"
  expect_contains "$out" "the installation 'belong' names 'belong', the installation this check must never touch" "the refusal says why"
  expect_contains "$out" "Nothing was read anywhere" "the refusal says nothing was read"
  expect_nothing_read "$work/belong-install" "belong as the installation"
  expect_contains "$out" "FACT restore: PASS — nothing was created" "no scratch directory was made either"

  RUN760_ARGS=(--repo belong-repo/app)
  run760 belong-repo
  expect_status "$status" 2 "a repository that names belong"
  expect_contains "$out" "the repository 'belong-repo/app' names 'belong'" "any value that names belong is refused"
  expect_nothing_read "$work/belong-repo" "a repository that names belong"

  RUN760_ARGS=(--installation Belong-Dev)
  run760 belong-case
  expect_status "$status" 2 "belong in another case"
  expect_nothing_read "$work/belong-case" "belong in another case"
}

test_dry_run_touches_nothing() {
  RUN760_ARGS=(--dry-run)
  run760 dry
  expect_status "$status" 0 "--dry-run"
  expect_contains "$out" "This is the PLAN" "the plan is printed"
  expect_contains "$out" "installation   acme" "the plan names the installation"
  expect_contains "$out" "pull request   the newest the 7-day window names" "the plan says which pull request it would time"
  expect_contains "$out" "second one     the oldest other pull request the window names" "the plan says which second one it would read"
  expect_contains "$out" "baseline 20 s, target 5 s" "the plan carries the plan's own figures"
  expect_contains "$out" "read the RL4 gate off the default listing's median" "the plan says what the decision is"
  expect_absent "$out" "CHECK 760 RESULT" "no block is printed for a plan"
  expect_nothing_read "$work/dry" "--dry-run"

  # The plan is printed for the installation it was given, and belong is refused
  # before the plan (the guard runs first).
  RUN760_ARGS=(--dry-run --installation othername --pr 7 --repo acme/app --old-pr 3)
  run760 dry-named
  expect_status "$status" 0 "--dry-run of a named installation"
  expect_contains "$out" "installation   othername" "the plan names the installation it was given"
  expect_contains "$out" "pull request   7" "the plan names the pull request it was given"
  expect_contains "$out" "second one     3" "the plan names the second pull request"
  expect_nothing_read "$work/dry-named" "--dry-run of a named installation"

  RUN760_ARGS=(--dry-run --installation belong)
  run760 dry-belong
  expect_status "$status" 2 "--dry-run pointed at belong"
  expect_contains "$out" "names 'belong'" "belong is refused before the plan is printed"
  expect_absent "$out" "This is the PLAN" "no plan is printed for belong"
  expect_nothing_read "$work/dry-belong" "--dry-run pointed at belong"
}

test_the_plan_and_the_usage_need_no_tools() {
  RUN760_ARGS=(--dry-run) RUN760_PATH=none
  run760 dry-nopath
  expect_status "$status" 0 "--dry-run with no fugaro on PATH"
  expect_contains "$out" "This is the PLAN" "the plan needs no tool"
  expect_nothing_read "$work/dry-nopath" "--dry-run with no fugaro on PATH"

  RUN760_ARGS=(--help) RUN760_PATH=none
  run760 help-nopath
  expect_status "$status" 0 "--help with no fugaro on PATH"
  expect_contains "$out" "usage: check-760-runs-ls-speed.sh" "the usage is printed"
  expect_contains "$out" "--old-pr N" "the usage names the arguments"
  expect_contains "$out" "the comment" "the usage points at the header for the rest"
  expect_nothing_read "$work/help-nopath" "--help with no fugaro on PATH"

  # A real run with no `fugaro` on PATH refuses, naming the tool, before it reads
  # or writes anything at all.
  RUN760_PATH=none
  run760 run-nopath
  expect_status "$status" 2 "a real run with no fugaro on PATH"
  expect_contains "$out" "Check 760 needs 'fugaro' on PATH and it is not there" "the refusal names the missing tool"
  expect_contains "$out" "the executable under test" "the refusal says what it is for"
  expect_nothing_read "$work/run-nopath" "a real run with no fugaro on PATH"
}

test_argument_complaints() {
  RUN760_ARGS=(--nope)
  run760 bad-flag
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$out" "unknown argument '--nope'" "the refusal names it"
  expect_contains "$out" "usage: check-760-runs-ls-speed.sh" "the refusal carries the usage"
  expect_nothing_read "$work/bad-flag" "an unknown argument"

  RUN760_ARGS=(--installation)
  run760 missing-value
  expect_status "$status" 2 "an argument with no value"
  expect_contains "$out" "--installation needs an installation name" "the refusal says what is missing"
  expect_nothing_read "$work/missing-value" "an argument with no value"

  RUN760_ARGS=(--pr 0)
  run760 zero-pr
  expect_status "$status" 2 "a pull request number of zero"
  expect_contains "$out" "--pr '0' is not a pull request number: it must be positive" "the refusal says why"
  expect_nothing_read "$work/zero-pr" "a pull request number of zero"

  RUN760_ARGS=(--old-pr abc)
  run760 bad-pr
  expect_status "$status" 2 "a pull request number that is not a number"
  expect_contains "$out" "--old-pr 'abc' is not a pull request number" "the refusal names the flag"
  expect_nothing_read "$work/bad-pr" "a pull request number that is not a number"

  RUN760_ARGS=(--repo acme)
  run760 bad-repo
  expect_status "$status" 2 "a repository with no owner"
  expect_contains "$out" "--repo 'acme' is not a repository name: it is OWNER/NAME, with one slash" "the refusal says the shape"
  expect_nothing_read "$work/bad-repo" "a repository with no owner"

  RUN760_ARGS=(--repo acme/app/extra)
  run760 long-repo
  expect_status "$status" 2 "a repository with two slashes"
  expect_contains "$out" "--repo 'acme/app/extra' is not a repository name" "the refusal says the shape"
  expect_nothing_read "$work/long-repo" "a repository with two slashes"

  RUN760_ARGS=(--installation 'bad name')
  run760 bad-installation
  expect_status "$status" 2 "an installation name with a space"
  expect_contains "$out" "holds a character an installation name may not have" "the refusal says why"
  expect_nothing_read "$work/bad-installation" "an installation name with a space"
}

# The knobs exist for this test and for an owner's own patience, and a value they
# cannot take is refused rather than silently treated as the default.
test_the_knobs_are_held() {
  RUN760_RUNS_COUNT=0
  run760 runs-zero
  expect_status "$status" 2 "a median of no runs"
  expect_contains "$out" "CHECK760_RUNS must be at least 1" "the refusal says why"
  expect_nothing_read "$work/runs-zero" "a median of no runs"

  RUN760_CALL_SECONDS=abc
  run760 call-seconds
  expect_status "$status" 2 "a deadline that is not a number"
  expect_contains "$out" "CHECK760_CALL_SECONDS 'abc' is not a number of seconds" "the refusal names the knob"
  expect_nothing_read "$work/call-seconds" "a deadline that is not a number"

  # A single timed run is allowed: one sample is its own median.
  RUN760_RUNS_COUNT=1
  run760 one-run
  expect_status "$status" 0 "one timed run"
  expect_contains "$out" "over 1 run (" "the row says one run, not one runs"
  expect_contains "$out" "FACT query-state-running: PASS" "a single run is a reading"
}

test_xtrace_is_refused() {
  RUN760_SHELL='bash -x'
  run760 xtrace
  expect_status "$status" 2 "a shell that arrived with tracing on"
  expect_contains "$out" "FACT xtrace: FAIL" "the trace is the failure"
  expect_contains "$out" "started with tracing on" "the row says what it read"
  expect_contains "$out" "the check prints its own RESULT block, so a trace is not needed" "the row says what to do"
  expect_contains "$out" "FACT installation: FAIL — not reached" "nothing was measured"
  expect_no_reads "$work/xtrace" "a shell that arrived with tracing on"
}

# The check is the owner's: a coding agent's session is refused before anything
# is read, and the refusal says which marker said so.
test_a_coding_agents_session_is_refused() {
  RUN760_ENV=(CLAUDECODE=1)
  run760 agent-session
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "the marker is the failure"
  expect_contains "$out" "CLAUDECODE is set" "the row names the marker it read"
  expect_contains "$out" "no agent runs any step of it" "the row says whose check this is"
  expect_contains "$out" "FACT xtrace: PASS" "the guards before it ran"
  expect_contains "$out" "FACT installation: FAIL — not reached" "nothing was measured"
  expect_no_reads "$work/agent-session" "a coding agent's session"

  RUN760_ENV=(CLAUDE_CODE_REMOTE=1)
  run760 agent-session-remote
  expect_status "$status" 2 "another coding agent's marker"
  expect_contains "$out" "CLAUDE_CODE_REMOTE is set" "each marker is read"

  # The editor extension's marker is NOT one of those: it says this shell sits in
  # an editor's terminal, where the person is still the one typing. It is dropped
  # rather than refused, so the check runs for someone in their editor.
  RUN760_ENV=(CLAUDE_CODE_SSE_PORT=12345)
  run760 editor-terminal
  expect_status "$status" 0 "an editor's terminal, not an agent"
  expect_contains "$out" "FACT agent-session: PASS" "the editor's marker is not a refusal"
  expect_contains "$out" "dropped from this script's environment" "the row says what it did with it"
  expect_contains "$out" "FACT installation: PASS" "the check went on to read the installation"
}

test_cleanup_leaves_nothing_behind() {
  run760 cleanup
  expect_status "$status" 0 "the cleanup case"
  local scratch
  scratch=$(scratch_of "$work/cleanup")
  [ -n "$scratch" ] || fail "the cleanup case: the run announced no scratch directory"
  expect_scratch_removed "$work/cleanup" "the cleanup case"
  # Nothing else is left in the scratch's own parent either.
  local left
  left=$(find "$work/cleanup/tmp" -mindepth 1 -maxdepth 1 2>/dev/null | head -n 5 | tr '\n' ' ')
  [ -z "$left" ] || fail "the cleanup case: the scratch's parent still holds $left"
  expect_contains "$err" "removed at the end" "the progress line says the directory is temporary"
}

# A kept scratch directory is the owner's own request, not a failure: the row
# says it was kept and where, and the verdict is still PASS.
test_a_kept_scratch_directory_is_not_a_failure() {
  RUN760_ENV=(CHECK760_KEEP_SCRATCH=yes)
  run760 kept
  expect_status "$status" 0 "a kept scratch directory"
  expect_last_line "$out" "CHECK 760 RESULT: PASS (exit 0)" "a kept scratch directory"
  expect_contains "$out" "FACT restore: PASS" "keeping the directory is not a failure"
  expect_contains "$out" "the scratch directory is KEPT at" "the row says where it is"
  expect_contains "$out" "because CHECK760_KEEP_SCRATCH=yes asked for that" "the row says who asked"
  expect_contains "$err" "CHECK760_KEEP_SCRATCH is set" "the progress says so too"
  local scratch
  scratch=$(scratch_of "$work/kept")
  [ -e "$scratch" ] || fail "a kept scratch directory: the directory is gone"
  rm -rf "$scratch"
}

# The check's own reporting, against a check that really did miss a step: with the
# timing step taken out, the rows it writes read 'not reached' and the run is
# INCOMPLETE — a check that printed a PASS here would be reporting figures it
# never took.
test_a_step_that_never_ran_reads_as_not_reached() {
  rewrite_script "$work/no-timing.sh" '  step_timing' '  : '
  RUN760_SCRIPT="$work/no-timing.sh"
  run760 no-timing
  expect_status "$status" 2 "a step that never ran"
  expect_block_keys "$out" "a step that never ran"
  expect_contains "$out" "FACT query-pr: FAIL — not reached" "the missing step's rows say so"
  expect_contains "$out" "the step that writes it did not run" "the row says why"
  expect_contains "$out" "FACT query-state-running: FAIL — not reached" "every row of the missing step says so"
  expect_contains "$out" "FACT query-since-7d: FAIL — not reached" "the default listing was not timed either"
  expect_contains "$out" "FACT rl4-gate: FAIL" "the row that reads the missing figure has no figure"
  expect_contains "$out" "FACT warmup: PASS" "the steps that did run are still reported"
  # The sanity row is written by a step of its own, which did run: it reads the
  # pull request's rows itself, so it is a reading even when the timing is not.
  expect_contains "$out" "FACT pr-rows: PASS" "the sanity read does not depend on the timing step"
  # The check reached its last step, so this is not an internal error.
  expect_absent "$out" "an internal error ended the run" "a rewritten step is not an internal error"
}

test_the_block_is_one_block() {
  run760 one-block
  expect_status "$status" 0 "one block"
  local count
  count=$(printf '%s\n' "$out" | grep -c '^CHECK 760 RESULT' || true)
  [ "${count:-0}" -eq 2 ] || fail "one block: the output carries ${count:-0} CHECK 760 RESULT lines, want 2"
  count=$(printf '%s\n' "$out" | grep -c '^FACT ' || true)
  [ "${count:-0}" -eq "${#BLOCK_KEYS[@]}" ] || fail "one block: the block carries ${count:-0} FACT rows, want ${#BLOCK_KEYS[@]}"
  # The block is on stdout, and the progress on stderr.
  expect_absent "$out" "scratch directory:" "the block is stdout only"
  expect_contains "$err" "scratch directory:" "the progress is stderr"
  expect_contains "$err" "warming the cache for" "the progress says which query is being run"
  expect_contains "$err" "timing runs ls --state running, run 1 of 3" "the progress counts the timed runs"
  # Nothing a document carried reaches the block: the rows are the check's own
  # words and the values its reader allowed.
  expect_absent "$out" "reading every run of the window, as it did before the index existed" "the CLI's note is summarised, not pasted"
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
t test_the_check_reads_and_writes_nothing
t test_the_check_runs_no_git_of_its_own
t test_the_medians_are_the_middle_sample
t test_a_row_over_its_target_says_so_and_still_passes
t test_a_row_beside_its_baseline_says_slower
t test_the_gate_reads_the_default_listing
t test_the_no_cache_row_has_no_target
t test_the_sanity_row_reads_the_index_and_the_fallback_scan
t test_the_sanity_row_when_the_timed_pull_request_fell_back
t test_the_sanity_row_with_no_second_pull_request
t test_the_second_pull_request_named_by_hand
t test_a_pull_request_outside_the_window_needs_a_repository
t test_a_pull_request_outside_the_window_with_a_repository
t test_a_window_that_names_no_pull_request
t test_a_refused_call_is_a_missing_reading
t test_a_call_that_never_answers_is_held_to_its_deadline
t test_a_listing_the_reader_cannot_read_is_a_missing_reading
t test_the_installation_must_be_readable
t test_the_installation_doctor_reads_must_not_name_belong
t test_belong_is_refused
t test_dry_run_touches_nothing
t test_the_plan_and_the_usage_need_no_tools
t test_argument_complaints
t test_the_knobs_are_held
t test_xtrace_is_refused
t test_a_coding_agents_session_is_refused
t test_cleanup_leaves_nothing_behind
t test_a_kept_scratch_directory_is_not_a_failure
t test_a_step_that_never_ran_reads_as_not_reached
t test_the_block_is_one_block

if [ "$failures" -ne 0 ]; then
  echo "check-760-runs-ls-speed_test: $failures failure(s)" >&2
  exit 1
fi
echo "check-760-runs-ls-speed_test: all tests passed" >&2
