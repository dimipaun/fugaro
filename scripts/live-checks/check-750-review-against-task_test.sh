#!/usr/bin/env bash
# Tests for scripts/live-checks/check-750-review-against-task.sh, against a fake
# fugaro: nothing here reaches the network, there is no cloud state, and no
# review is launched. Every document the check reads comes out of $FAKE750_DIR,
# and every call it makes is recorded there, so a case can tell a run that read
# an artifact from one that stopped before it.
#
# The fakes answer, and never decide: the against line the check reads is the
# fixture's, the verdict and the issues are the fixture's, and the rows of a
# pull request are the fixture's -- so a case that expects a row to FAIL writes
# the document that makes it fail, rather than an expectation the fake was built
# to meet.
#
# Two things every case is held to, wherever the case is about something else:
#
#  o the TEXT of a task never reaches the block. The artifact this suite seeds
#    carries a model's own prose (the overview and the issues' summaries, which
#    is where a real artifact repeats the task it judged against), and the block
#    must not print it: what the check reads is a verdict, a scope, a count and
#    the launcher's own line, never a person's words.
#  o no credential is read on any path: the recorded calls carry no
#    `print-access-token` and no token argument, because the check has no use
#    for one -- and no cloud tool at all is on its PATH.
#
# Usage: bash scripts/live-checks/check-750-review-against-task_test.sh
#        CHECK_TEST_ONLY=case_healthy bash scripts/live-checks/check-750-review-against-task_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-750-review-against-task.sh
BASH_BIN=$(command -v bash)
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK750_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... block: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2" "$3 (FACT $1)"
}
expect_calls() { # needle what
  expect_contains "$(cat "$dir/state/calls" 2>/dev/null || true)" "$1" "$2"
}
expect_no_calls() { # needle what
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "$1" "$2"
}

INSTALLATION=sandbox750
GCP_PROJECT=prj-sandbox750
REGION=europe-west1
REPO=acme/widgets
SLUG=acme-widgets
PR=7
UNMET_PR=9
# A run id looks like 20260926-221530-a1b2 (task.ValidRunID), which is the shape
# the opening run's id has to hold: the check compares it against the bare id in
# the launcher's own against line.
OPENING_RUN=20260602-090000-b1c2
RV_AGAINST=20260601-110000-c3d4
RV_UNMET=20260601-120000-e5f6
RV_CODE=20260601-130000-a7b8
# The model's own prose in the artifact, which repeats the task it judged
# against. It must never reach the block: the check reads a verdict and a scope,
# never a person's words.
TASK_SENTINEL='SENTINEL-a-persons-own-task-words-in-the-artifact'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation belong release pr-opened bound against-run unmet code-only agent-session xtrace restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check-750's test. Every answer comes out of $FAKE750_DIR and
# every call is recorded, so a case can tell a run that read an artifact from one
# that stopped before it. A review LAUNCH is keyed on the pull request and the
# flag rather than on a sequence -- the review of the check's own pull request is
# rv750a-shaped, the review of the --unmet-pr one is rv750b-shaped, and a
# --code-only launch is rv750c-shaped -- so a fixture names its file once and the
# cases that leave --unmet-pr out still read the same ids.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE750_DIR/calls"
D=$FAKE750_DIR
serve() { # serve FILE STATUSFILE
  [ ! -f "$2" ] || exit "$(cat "$2")"
  cat "$1"
}
shown() { # shown FILE — the id the launched run has, from the run's own id in
  # the reference the check passes (`<slug>/<run-id>`) and the id the fake
  # printed at launch time. The check passes `run` from the launch document, so
  # the id is its last path segment.
  printf '%s' "${1##*/}"
}
case "${1:-}" in
doctor) serve "$D/doctor.json" "$D/doctor.exit" ;;
version) cat "$D/version.out" ;;
project)
  [ "${2:-}" = show ] || { printf 'fake fugaro: unexpected project %s\n' "${2:-}" >&2; exit 64; }
  serve "$D/project.json" "$D/project.exit"
  ;;
runs)
  [ "${2:-}" = ls ] || { printf 'fake fugaro: unexpected runs %s\n' "${2:-}" >&2; exit 64; }
  if grep -q -- ' --pr ' <<<" $* "; then
    # The CLI refuses a `runs ls --pr` with no `--repo` unless the local config
    # names exactly one repository, so the check passes `--repo .` — the
    # checkout's own repository, which the CLI resolves from its origin. The
    # refusal is mirrored here: a case whose premise read lost that argument
    # fails rather than reading rows out of a repository nobody named.
    if ! grep -q -- ' --repo ' <<<" $* "; then
      printf 'runs ls --pr needs --repo: the local config lists 2 repositories\n' >&2
      exit 1
    fi
    serve "$D/prrows.json" "$D/prrows.exit"
    exit 0
  fi
  # The polling read: every run this fake launched, with the terminal flag its
  # case gave it (yes unless the case wrote state/terminal.<id>).
  [ ! -f "$D/rows.exit" ] || exit "$(cat "$D/rows.exit")"
  printf '{"runs":['
  first=1
  if [ -s "$D/launched" ]; then
    while read -r id; do
      [ -n "$id" ] || continue
      t=$(cat "$D/terminal.$id" 2>/dev/null || printf yes)
      [ "$first" = 1 ] || printf ','
      first=0
      printf '{"run_id":"%s","run":"%s/%s","repo":"%s","status":"succeeded","stage":"finalize","terminal":%s}' \
        "$id" "$FAKE750_SLUG" "$id" "$FAKE750_REPO" "$([ "$t" = yes ] && printf true || printf false)"
    done <"$D/launched"
  fi
  printf ']}\n'
  ;;
review)
  if [ "${2:-}" = "--help" ]; then cat "$D/reviewhelp.out"; exit 0; fi
  if [ "${2:-}" = show ]; then
    id=$(shown "${3:-}")
    if grep -q -- ' --json ' <<<" $* "; then
      serve "$D/artifact.$id.json" "$D/show.exit"
      exit 0
    fi
    serve "$D/show.$id.out" "$D/show.exit"
    exit 0
  fi
  # A launch: `review --pr N [--code-only] --json`.
  pr=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--pr" ]; then pr=$a; fi
    case "$a" in --pr=*) pr=${a#--pr=} ;; esac
    prev=$a
  done
  id=$FAKE750_RV_AGAINST
  case " $* " in *" --code-only "*) id=$FAKE750_RV_CODE ;; esac
  if [ "$pr" = "$FAKE750_UNMET_PR" ]; then id=$FAKE750_RV_UNMET; fi
  # Two records of one launch: the id the polling read must find (the file the
  # `runs ls` branch above builds its rows from), and the line a case reads to
  # see WHICH review the check launched and in what order.
  printf '%s\n' "$id" >>"$D/launched"
  printf 'fugaro review --pr %s counted as %s\n' "$pr" "$id" >>"$D/launches"
  # A launch that is refused is refused for whichever review the case names, and
  # the id becomes numbered so two refusals are two rows.
  if [ -f "$D/launch.status.$id" ]; then
    printf 'fake fugaro: the launch was refused\n' >&2
    exit "$(cat "$D/launch.status.$id")"
  fi
  if [ -f "$D/launch.status" ]; then
    printf 'fake fugaro: the launch was refused\n' >&2
    exit "$(cat "$D/launch.status")"
  fi
  if [ -f "$D/nameless" ]; then
    printf '{"project":"%s","run":"","run_id":"","repo":"%s","status":"launched","job":"%s-review"}\n' \
      "$FAKE750_PROJECT" "$FAKE750_REPO" "$FAKE750_REPO"
    exit 0
  fi
  printf '{"project":"%s","run_id":"%s","run":"%s/%s","repo":"%s","status":"launched","job":"%s-review"}\n' \
    "$FAKE750_PROJECT" "$id" "$FAKE750_SLUG" "$id" "$FAKE750_REPO" "$FAKE750_SLUG"
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ fixtures
write_doctor() { # dir [name] [project]
  local name=${2:-$INSTALLATION} project=${3:-$GCP_PROJECT}
  cat >"$1/state/doctor.json" <<EOF
{"version":1,"project":{"name":"$name","gcp_project":"$project","region":"$REGION"},"checks":[]}
EOF
}

write_version() { # dir VERSION
  printf '%s\n' "$2" >"$1/state/version.out"
}

write_reviewhelp() { # dir yes|no
  if [ "$2" = yes ]; then
    cat >"$1/state/reviewhelp.out" <<'EOF'
Review a pull request in a job of its own, on a named account.

Usage:
  fugaro review --pr N [flags]

Flags:
      --pr int         the pull request to review (required)
      --code-only      review the pull request's code alone, with no task
      --spec string    review against the task in this FILE instead
      --json           print machine-readable output
EOF
  else
    cat >"$1/state/reviewhelp.out" <<'EOF'
Review a pull request in a job of its own, on a named account.

Usage:
  fugaro review --pr N [flags]

Flags:
      --pr int    the pull request to review (required)
      --token     named Claude account this review runs as
EOF
  fi
}

write_project() { # dir yes|no-policy|flat
  case "$2" in
  yes)
    cat >"$1/state/project.json" <<EOF
{"project":"$INSTALLATION",
 "caps":{"global":{"per_run_cap_usd":25,"daily_cap_usd":100},
         "defaults":{"per_run_cap_usd":5,"daily_cap_usd":20},
         "repos":{"$SLUG":{"repo":"$REPO","per_run_cap_usd":3,"daily_cap_usd":10}}},
 "policy":{"version":1,"budget":{"mode":"enforce","per_run_usd":2,"allowed_models":["claude-sonnet-5"],"max_run_tokens":0},"allow_repos":["$REPO"]}}
EOF
    ;;
  no-policy)
    cat >"$1/state/project.json" <<EOF
{"project":"$INSTALLATION","caps":{"global":{"per_run_cap_usd":null,"daily_cap_usd":null},"defaults":{"per_run_cap_usd":null,"daily_cap_usd":null}},"policy":null}
EOF
    ;;
  esac
}

# write_prrows MODE — the rows of pull request $PR, as `runs ls --pr 7 --json`
# answers them.
write_prrows() { # dir opened|follow-up|review-run|two|none
  case "$2" in
  opened)
    cat >"$1/state/prrows.json" <<EOF
{"runs":[{"run_id":"$OPENING_RUN","run":"$SLUG/$OPENING_RUN","repo":"$REPO","status":"succeeded","stage":"finalize","terminal":true,"branch":"fugaro/$OPENING_RUN","follow_up":false,"pr":$PR}]}
EOF
    ;;
  follow-up)
    # The only row on the pull request is a follow-up: its branch names the ROOT
    # run (that is the branch it pushed to), so it did not open the pull request.
    cat >"$1/state/prrows.json" <<EOF
{"runs":[{"run_id":"20260603-140000-d9e0","run":"$SLUG/20260603-140000-d9e0","repo":"$REPO","status":"succeeded","stage":"finalize","terminal":true,"branch":"fugaro/$OPENING_RUN","follow_up":true,"pr":$PR,"previous_run":"$OPENING_RUN"}]}
EOF
    ;;
  review-run)
    # A finished review's own row: its record names the pull request it read and
    # the artifacts it wrote, and its branch is its own lock key.
    cat >"$1/state/prrows.json" <<EOF
{"runs":[{"run_id":"$RV_AGAINST","run":"$SLUG/$RV_AGAINST","repo":"$REPO","status":"succeeded","stage":"review","terminal":true,"branch":"fugaro/$RV_AGAINST","follow_up":false,"pr":$PR,"artifact_count":3,"artifact_bytes":9000}]}
EOF
    ;;
  two)
    cat >"$1/state/prrows.json" <<EOF
{"runs":[{"run_id":"$OPENING_RUN","run":"$SLUG/$OPENING_RUN","repo":"$REPO","status":"succeeded","stage":"finalize","terminal":true,"branch":"fugaro/$OPENING_RUN","follow_up":false,"pr":$PR},
         {"run_id":"20260604-150000-f1a2","run":"$SLUG/20260604-150000-f1a2","repo":"$REPO","status":"running","stage":"implement","terminal":false,"branch":"fugaro/20260604-150000-f1a2","follow_up":false,"pr":$PR}]}
EOF
    ;;
  none)
    printf '{"runs":[]}\n' >"$1/state/prrows.json"
    ;;
  esac
}

# write_show ID KIND — what `fugaro review show <id>` prints: the against line
# first (R8), then the artifact's own bytes. The kinds are the launcher's three
# sources and the two ways the line can be wrong.
write_show() { # dir ID run|none-code-only|none-lost|other-run|file|no-line|absent
  local line
  case "$3" in
  run) line="against: run $OPENING_RUN" ;;
  none-code-only) line="against: none (--code-only)" ;;
  none-lost) line="against: none (no run found)" ;;
  other-run) line="against: run 20260101-000000-ffff" ;;
  file) line="against: file 9f2c0b4e5a6d7c8b9a0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6e5f4a3b" ;;
  no-line | absent) line="" ;;
  esac
  {
    [ -z "$line" ] || printf '%s\n' "$line"
    if [ "$3" = absent ]; then
      printf 'fugaro: %s has no review artifact\n' "$SLUG/$2"
      exit 0
    fi
    cat <<EOF
{"schema":1,"version":1,"run_id":"$2","repository":"$SLUG","pr":$PR,"head_sha":"deadbeef",
 "reviewer":{"model":"claude-sonnet-5","account":"default"},
 "against":{"source":"run","run":"$OPENING_RUN"},
 "verdict":"fix first","overview":"$TASK_SENTINEL",
 "strengths":["$TASK_SENTINEL"],
 "issues":[{"severity":"major","class":"correctness","scope":"unmet","file":"main.go","line":3,"summary":"$TASK_SENTINEL","fix":"$TASK_SENTINEL","evidence":"$TASK_SENTINEL"}],
 "security":"ok","performance":"ok","test_coverage":"ok","mutation_checks":[]}
EOF
  } >"$1/state/show.$2.out"
}

# write_artifact ID VERDICT UNMET_SEVERITIES... — the --json read: the artifact's
# own bytes, which is where the verdict and the issues' scopes are taken from.
# The prose fields carry the task sentinel, as a real artifact's do.
write_artifact() { # dir ID verdict [severity...]
  local dir=$1 id=$2 verdict=$3
  shift 3
  local issues="" sev
  for sev in "$@"; do
    issues="${issues}${issues:+,}{\"severity\":\"$sev\",\"class\":\"correctness\",\"scope\":\"unmet\",\"file\":\"main.go\",\"line\":3,\"summary\":\"$TASK_SENTINEL\",\"fix\":\"$TASK_SENTINEL\",\"evidence\":\"$TASK_SENTINEL\"}"
  done
  cat >"$dir/state/artifact.$id.json" <<EOF
{"schema":1,"version":1,"run_id":"$id","repository":"$SLUG","pr":$UNMET_PR,"head_sha":"deadbeef",
 "reviewer":{"model":"claude-sonnet-5","account":"default"},
 "against":{"source":"run","run":"$OPENING_RUN"},
 "verdict":"$verdict","overview":"$TASK_SENTINEL","strengths":["$TASK_SENTINEL"],
 "issues":[$issues],
 "security":"ok","performance":"ok","test_coverage":"ok","mutation_checks":[]}
EOF
}

# ------------------------------------------------------------------ the case
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/launched"
  : >"$dir/state/launches"
  write_doctor "$dir"
  write_version "$dir" 0.27.0
  write_reviewhelp "$dir" yes
  write_project "$dir" yes
  write_prrows "$dir" opened
  # The three reviews, as the launcher would answer them on a healthy
  # installation: the review of this pull request names the run that opened it,
  # the --code-only one says so, and the review of the second pull request is
  # `fix first` with one unmet issue.
  write_show "$dir" "$RV_AGAINST" run
  write_show "$dir" "$RV_CODE" none-code-only
  write_show "$dir" "$RV_UNMET" run
  write_artifact "$dir" "$RV_UNMET" "fix first" major
}

# run750 [ARGS...] — runs the check on the case at hand. The knobs are the
# test's: every wait is a second or two, so a case whose review never ends still
# ends, and the launch deadline is nothing.
run750() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    TMPDIR="$dir/tmp" FAKE750_DIR="$dir/state" FAKE750_PROJECT="$GCP_PROJECT" \
    FAKE750_REPO="$REPO" FAKE750_SLUG="$SLUG" FAKE750_UNMET_PR="$UNMET_PR" \
    FAKE750_RV_AGAINST="$RV_AGAINST" FAKE750_RV_UNMET="$RV_UNMET" FAKE750_RV_CODE="$RV_CODE" \
    CHECK750_CALL_SECONDS=10 CHECK750_LAUNCH_SECONDS=10 CHECK750_RUN_SECONDS=2 \
    CHECK750_POLL_SECONDS=1 CHECK750_RUNS_LIMIT=20 \
    PATH="${RUN750_PATH:-$dir/bin:$PATH}" \
    ${RUN750_ENV[@]+"${RUN750_ENV[@]}"} \
    "$BASH_BIN" ${RUN750_BASH[@]+"${RUN750_BASH[@]}"} "$script" ${RUN750_ARGS[@]+"${RUN750_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN750_ARGS=()
  RUN750_ENV=()
  RUN750_PATH=""
  RUN750_BASH=()
  # Every case's own assertion, wherever the case is about something else: the
  # model's own prose never reaches the block, and no credential is read.
  expect_absent "$out$(cat "$dir/err" 2>/dev/null || true)" "$TASK_SENTINEL" \
    "case $dir: an artifact's own prose reached the block or the log"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "print-access-token" \
    "case $dir: the check asked for an access token, which it has no use for"
  # Nothing but fugaro is called: the check needs no cloud tool of its own.
  local line
  while read -r line; do
    [ -z "$line" ] && continue
    case "$line" in
    fugaro\ *) ;;
    *) fail "case $dir: a call that is not fugaro's: $line" ;;
    esac
  done <"$dir/state/calls"
}

expect_block_keys() { # what
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1 (FACT $key must be in every block)"
  done
}

expect_clean() { # what — the scratch directory the check made is gone
  local left
  left=$(ls -A "$dir/tmp" 2>/dev/null || true)
  [ -z "$left" ] || fail "$1: the check left $dir/tmp holding: $(printf '%s' "$left" | tr '\n' ' ')"
  expect_fact restore PASS "$1 (restore)"
}

expect_launched() { # count what — how many reviews the check launched
  local got
  got=$(grep -c 'counted as' "$dir/state/launches" 2>/dev/null || true)
  [ "${got:-0}" = "$1" ] || fail "$2: expected $1 review launch(es), got ${got:-0}"
}

# ------------------------------------------------------------------- the cases
case_healthy() {
  setup_case healthy
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR"
  expect_status "$status" 0 "healthy: a review that names its task, an unmet issue, and --code-only"
  expect_block_keys "healthy"
  expect_fact installation PASS "healthy"
  expect_fact belong PASS "healthy"
  expect_fact release PASS "healthy"
  expect_fact pr-opened PASS "healthy"
  expect_contains "$out" "its opening run is $OPENING_RUN" "healthy: the opening run is found from the rows"
  expect_fact bound PASS "healthy"
  expect_fact cap PASS "healthy"
  expect_contains "$out" "mode 'enforce' with a per-run ceiling of \$2" \
    "healthy: the ceiling the launches are bounded by is read from the project"
  expect_contains "$out" "\$3 for $REPO" "healthy: the repository's own cap is quoted"
  expect_fact against-run PASS "healthy"
  expect_contains "$out" "against: run $OPENING_RUN" "healthy: the row quotes the launcher's own line"
  expect_fact unmet PASS "healthy"
  expect_contains "$out" "1 of its 1 issues scoped unmet" "healthy: the row counts the unmet issues"
  expect_fact code-only PASS "healthy"
  expect_contains "$out" "against: none (--code-only)" "healthy: the row quotes the exact line"
  expect_fact agent-session PASS "healthy"
  expect_fact xtrace PASS "healthy"
  expect_contains "$out" "CHECK 750 RESULT: PASS (exit 0)" "healthy: the verdict line"
  expect_clean "healthy"
  expect_launched 3 "healthy"
  # The three launches, in the plan's own order, each a review of its own.
  local calls
  calls=$(cat "$dir/state/launches")
  expect_contains "$(printf '%s\n' "$calls" | sed -n '1p')" "review --pr $PR counted as $RV_AGAINST" "healthy: the first review is of the pull request, without --code-only"
  expect_contains "$(printf '%s\n' "$calls" | sed -n '2p')" "review --pr $UNMET_PR counted as $RV_UNMET" "healthy: the second review is of the --unmet-pr pull request"
  expect_contains "$(printf '%s\n' "$calls" | sed -n '3p')" "review --pr $PR counted as $RV_CODE" "healthy: the third is the pull request again, --code-only"
  expect_calls "fugaro review --pr $PR --code-only --json" "healthy: --code-only is what the third launch asks for"
  # The reads, each by the command the section names.
  expect_calls "fugaro doctor --json" "healthy: the installation is read first"
  expect_calls "fugaro runs ls --pr $PR --repo . --limit 20 --json" "healthy: the rows of the pull request are read, with the repository named so a multi-repo local config cannot refuse the read"
  expect_calls "fugaro project show --json" "healthy: the ceiling is read from the project"
  expect_calls "fugaro review show $SLUG/$RV_AGAINST" "healthy: the first review's own output is read"
  expect_calls "fugaro review show $SLUG/$RV_UNMET --json" "healthy: the second review's artifact is read as JSON"
  expect_calls "fugaro review show $SLUG/$RV_CODE" "healthy: the --code-only review's own output is read"
  # The ceiling is read BEFORE the first launch, and the launch is announced
  # before it spends anything: the owner's log says which review it is paying
  # for, and nothing ever asks a question.
  local shown_at launched_at
  shown_at=$(grep -n 'fugaro project show' "$dir/state/calls" | head -n 1 | cut -d: -f1)
  launched_at=$(grep -n 'fugaro review --pr' "$dir/state/calls" | head -n 1 | cut -d: -f1)
  if [ -z "$shown_at" ] || [ -z "$launched_at" ] || [ "$shown_at" -ge "$launched_at" ]; then
    fail "healthy: the ceiling (line ${shown_at:-none}) must be read before the first launch (line ${launched_at:-none})"
  fi
  expect_contains "$(cat "$dir/err")" "is a billable review job of its own" "healthy: each launch says what it costs"
  expect_absent "$(cat "$dir/err")" "y/N" "healthy: nothing asks a question"
}

case_no_unmet_pr() {
  setup_case no-unmet-pr
  run750 --installation "$INSTALLATION" --pr "$PR"
  # The half of the pass condition that needs a second pull request is NOT
  # measured, so the check is INCOMPLETE and never a pass.
  expect_status "$status" 3 "no-unmet-pr: an unmeasured criterion is not a pass"
  expect_block_keys "no-unmet-pr"
  expect_fact against-run PASS "no-unmet-pr"
  expect_fact code-only PASS "no-unmet-pr"
  expect_fact unmet UNVERIFIED "no-unmet-pr: the row says it was not measured"
  expect_contains "$out" "--unmet-pr <N>" "no-unmet-pr: the row says how to measure it"
  expect_launched 2 "no-unmet-pr: nothing is launched for a reading nobody asked for"
  expect_no_calls "review --pr $UNMET_PR" "no-unmet-pr"
  expect_contains "$out" "CHECK 750 RESULT: INCOMPLETE (exit 3)" "no-unmet-pr: the verdict line"
}

case_against_names_another_run() {
  setup_case against-other
  write_show "$dir" "$RV_AGAINST" other-run
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR"
  expect_status "$status" 1 "against-other"
  expect_fact against-run FAIL "against-other"
  expect_contains "$out" "naming run 20260101-000000-ffff" "against-other: the row quotes the run the launcher named"
  expect_contains "$out" "the run that opened it is $OPENING_RUN" "against-other: the row says which run the rows name"
  # The other two readings are independent of it and still run.
  expect_fact code-only PASS "against-other"
  expect_fact unmet PASS "against-other"
}

case_against_no_run_found() {
  setup_case against-lost
  write_show "$dir" "$RV_AGAINST" none-lost
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 1 "against-lost"
  expect_fact against-run FAIL "against-lost"
  expect_contains "$out" "no run found" "against-lost: the row quotes the launcher's own reason"
  expect_contains "$out" "D2933" "against-lost: the row names the skew this reason is"
}

case_against_file_source() {
  setup_case against-file
  write_show "$dir" "$RV_AGAINST" file
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 1 "against-file"
  expect_contains "$out" "names a source this check does not read" "against-file: a file is not the run's task"
}

case_against_no_line() {
  setup_case against-noline
  # An artifact written before the field existed prints no against line at all.
  write_show "$dir" "$RV_AGAINST" no-line
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 1 "against-noline"
  expect_fact against-run FAIL "against-noline"
  expect_contains "$out" "printed no against line at all" "against-noline"
}

case_against_show_refused() {
  setup_case against-noshow
  printf '1\n' >"$dir/state/show.exit"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 1 "against-noshow"
  expect_fact against-run FAIL "against-noshow"
  # The fake's own last stderr line is what the row quotes, and the fake writes
  # nothing to stderr on this path, so the row says the command printed nothing.
  expect_contains "$out" "exited non-zero" "against-noshow"
}

case_code_only_line_wrong() {
  setup_case code-wrong
  # The flag was given and the launcher answered as if it had not.
  write_show "$dir" "$RV_CODE" run
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 1 "code-wrong"
  expect_fact code-only FAIL "code-wrong"
  expect_contains "$out" "against: none (--code-only)" "code-wrong: the row states the pass condition"
}

case_code_only_none_other_reason() {
  setup_case code-other
  # `none` with the launcher's other reason: the reader cannot tell a code-only
  # review from one whose launcher lost the task, which is the whole point of the
  # flag.
  write_show "$dir" "$RV_CODE" none-lost
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 1 "code-other"
  expect_fact code-only FAIL "code-other"
  expect_contains "$out" "no run found" "code-other: the row quotes what it found instead"
}

case_unmet_verdict_ship() {
  setup_case unmet-ship
  # The reviewer saw the missing item and approved the change anyway: exactly what
  # the second half of the pass condition exists to catch.
  write_artifact "$dir" "$RV_UNMET" "ship" major
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR"
  expect_status "$status" 1 "unmet-ship"
  expect_fact unmet FAIL "unmet-ship"
  expect_contains "$out" "and the change was not held back" "unmet-ship"
}

case_unmet_issue_missing() {
  setup_case unmet-none
  write_artifact "$dir" "$RV_UNMET" "ship"
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR"
  expect_status "$status" 1 "unmet-none"
  expect_fact unmet FAIL "unmet-none"
  expect_contains "$out" "lists no issue with scope 'unmet'" "unmet-none"
  expect_contains "$out" "--unmet-pr must be one a Fugaro run opened" "unmet-none: the row says how to make the reading possible"
}

case_unmet_pr_is_the_same_pr() {
  setup_case unmet-same
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$PR"
  # A pull request that does its task reports no unmet issue by construction, so
  # asking for the same one twice is refused before anything is called.
  expect_status "$status" 2 "unmet-same"
  expect_contains "$out" "the second reading needs a DIFFERENT pull request" "unmet-same: the refusal says why"
  expect_no_calls "fugaro doctor" "unmet-same: refused before any call"
}

case_pr_not_opened() {
  setup_case pr-none
  write_prrows "$dir" none
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "pr-none: a pull request no run opened is refused"
  expect_fact pr-opened FAIL "pr-none"
  expect_contains "$out" "is not one a Fugaro run opened" "pr-none"
  expect_block_keys "pr-none"
  expect_launched 0 "pr-none: nothing is launched for a pull request that is not the subject"
}

case_pr_follow_up_only() {
  setup_case pr-followup
  write_prrows "$dir" follow-up
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "pr-followup"
  expect_fact pr-opened FAIL "pr-followup"
  expect_launched 0 "pr-followup"
}

case_pr_review_run_row() {
  setup_case pr-review
  # The pull request has a finished review's row and no opening run: the artifact
  # in its record is what tells the two apart, and a review did not open it.
  write_prrows "$dir" review-run
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "pr-review"
  expect_fact pr-opened FAIL "pr-review"
  expect_launched 0 "pr-review"
}

case_pr_two_candidates() {
  setup_case pr-two
  write_prrows "$dir" two
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "pr-two: the check refuses rather than picking one"
  expect_fact pr-opened FAIL "pr-two"
  expect_contains "$out" "names 2 runs" "pr-two: the row says how many it found"
  expect_contains "$out" "$OPENING_RUN" "pr-two: the row names the rows it found"
  expect_launched 0 "pr-two"
}

case_pr_rows_unreadable() {
  setup_case pr-unreadable
  printf '1\n' >"$dir/state/prrows.exit"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "pr-unreadable"
  expect_fact pr-opened FAIL "pr-unreadable"
  expect_contains "$out" "from a checkout of the repository" "pr-unreadable: the row says where to run it"
  expect_launched 0 "pr-unreadable"
}

case_review_never_terminal() {
  setup_case never-terminal
  printf 'no\n' >"$dir/state/terminal.$RV_AGAINST"
  run750 --installation "$INSTALLATION" --pr "$PR"
  # The review job is still going: its artifact does not exist yet, so the
  # reading was not taken — UNVERIFIED, and the check is INCOMPLETE.
  expect_status "$status" 3 "never-terminal"
  expect_fact against-run UNVERIFIED "never-terminal"
  expect_contains "$out" "had not finished after 2s" "never-terminal: the row says how long it waited"
  expect_contains "$out" "fugaro runs show" "never-terminal: the row says where the run is"
  # The --code-only review is its own job and its own row.
  expect_fact code-only PASS "never-terminal"
}

case_launch_refused() {
  setup_case launch-refused
  printf '1\n' >"$dir/state/launch.status.$RV_AGAINST"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 3 "launch-refused"
  expect_fact against-run UNVERIFIED "launch-refused"
  expect_contains "$out" "exited 1" "launch-refused: the row says what the launch answered"
  expect_fact code-only PASS "launch-refused"
}

case_launch_cap_refusal() {
  setup_case cap-refusal
  # What the gateway's refusal looks like from here: the launch exits non-zero
  # with the reason on its stderr, and the row quotes it rather than retrying.
  cat >"$dir/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE750_DIR/calls"
case "${1:-}" in
doctor) cat "$FAKE750_DIR/doctor.json" ;;
version) cat "$FAKE750_DIR/version.out" ;;
review)
  if [ "${2:-}" = "--help" ]; then cat "$FAKE750_DIR/reviewhelp.out"; exit 0; fi
  if [ "${2:-}" = show ]; then exit 1; fi
  printf 'fugaro: the run would spend past the project'"'"'s per-run ceiling, so the gateway refused it\n' >&2
  exit 1
  ;;
project) cat "$FAKE750_DIR/project.json" ;;
runs) cat "$FAKE750_DIR/prrows.json" ;;
esac
FUGARO_EOF
  chmod +x "$dir/bin/fugaro"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 3 "cap-refusal"
  expect_fact against-run UNVERIFIED "cap-refusal"
  expect_contains "$out" "the gateway refused it" "cap-refusal: the gateway's own sentence is quoted"
  expect_launched 0 "cap-refusal: the recording fake is not this one"
}

case_launch_nameless() {
  setup_case launch-nameless
  : >"$dir/state/nameless"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 3 "launch-nameless"
  expect_fact against-run UNVERIFIED "launch-nameless"
  expect_contains "$out" "named no run" "launch-nameless"
}

case_cap_unreadable() {
  setup_case cap-unreadable
  printf '1\n' >"$dir/state/project.exit"
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR"
  # The ceiling could not be read: the check says so rather than guessing, and
  # the three readings themselves still run.
  expect_status "$status" 3 "cap-unreadable"
  expect_fact cap UNVERIFIED "cap-unreadable"
  expect_fact against-run PASS "cap-unreadable"
  expect_fact unmet PASS "cap-unreadable"
  expect_fact code-only PASS "cap-unreadable"
}

case_no_policy_published() {
  setup_case no-policy
  write_project "$dir" no-policy
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR"
  expect_status "$status" 0 "no-policy: an installation with nothing published still passes"
  expect_fact cap PASS "no-policy"
  expect_contains "$out" "no policy is published" "no-policy: the row says what is in force instead"
  expect_fact bound PASS "no-policy"
}

case_release_too_old() {
  setup_case release-old
  write_version "$dir" 0.26.1
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "release-old: a build older than the release is stopped"
  expect_fact release FAIL "release-old"
  expect_contains "$out" "fugaro update" "release-old: the row says the one command to run"
  expect_launched 0 "release-old: nothing is launched on a build without the work"
}

case_release_no_flags() {
  setup_case release-noflags
  # A build that reports no release number and whose review has neither flag:
  # the feature probe is what decides, and it says no.
  write_version "$dir" "dev (b594956)"
  write_reviewhelp "$dir" no
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "release-noflags"
  expect_fact release FAIL "release-noflags"
  expect_contains "$out" "names neither --code-only nor --spec" "release-noflags"
  expect_launched 0 "release-noflags"
}

case_release_dev_build() {
  setup_case release-dev
  write_version "$dir" "0.28.0-dev.3+b594956"
  run750 --installation "$INSTALLATION" --pr "$PR"
  # A build that names no release number is decided by the feature probe, which
  # says yes here: the release row passes and the readings are taken.
  expect_status "$status" 3 "release-dev: the readings ran, --unmet-pr was not given"
  expect_fact release PASS "release-dev"
  expect_contains "$out" "not a release number" "release-dev: the row says which answer decided it"
  expect_fact against-run PASS "release-dev"
}

case_belong_installation() {
  setup_case belong-installation
  run750 --installation belong --pr "$PR"
  expect_status "$status" 2 "belong-installation"
  expect_fact belong FAIL "belong-installation"
  expect_no_calls "fugaro doctor" "belong-installation: refused before any call"
}

case_belong_project() {
  setup_case belong-project
  write_doctor "$dir" "$INSTALLATION" "prj-belong-dev"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "belong-project"
  expect_fact belong FAIL "belong-project"
  expect_contains "$out" "prj-belong-dev" "belong-project: the row names the project it refused"
  expect_launched 0 "belong-project"
}

case_name_mismatch() {
  setup_case name-mismatch
  write_doctor "$dir" other-installation
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "name-mismatch"
  expect_fact installation FAIL "name-mismatch"
  expect_contains "$out" "other-installation" "name-mismatch: the row names what the config in force answered"
}

case_doctor_refused() {
  setup_case doctor-refused
  printf '2\n' >"$dir/state/doctor.exit"
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "doctor-refused"
  expect_contains "$out" "did not answer" "doctor-refused"
}

case_agent_session() {
  setup_case agent
  RUN750_ENV=(CLAUDECODE=1)
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "agent: an agent's session is refused"
  expect_fact agent-session FAIL "agent"
  expect_contains "$out" "yours to run at your own terminal" "agent"
  expect_no_calls "fugaro doctor" "agent: refused before any call"
}

case_editor_terminal_is_allowed() {
  setup_case editor
  # The editor extension's own marker says a person is typing in an editor's
  # terminal, not that an agent is driving: it is dropped, and the check runs.
  RUN750_ENV=(CLAUDE_CODE_SSE_PORT=12345)
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 3 "editor: a person in an editor's terminal is allowed"
  expect_fact agent-session PASS "editor"
  expect_contains "$out" "CLAUDE_CODE_SSE_PORT" "editor: the row says which marker it dropped"
}

case_xtrace() {
  setup_case xtrace
  # On its own line: `RUN750_BASH=(-x) run750` would pass the string "(-x)".
  RUN750_BASH=(-x)
  run750 --installation "$INSTALLATION" --pr "$PR"
  expect_status "$status" 2 "xtrace: a tracing shell is refused"
  expect_fact xtrace FAIL "xtrace"
  expect_no_calls "fugaro doctor" "xtrace: refused before any call"
}

case_missing_tools() {
  setup_case missing-tools
  mkdir -p "$dir/empty"
  RUN750_PATH="$dir/empty"
  run750 --installation "$INSTALLATION" --pr "$PR"
  # bash itself is absolute, so the first tool it looks for is the one it names.
  expect_status "$status" 2 "missing-tools"
  expect_contains "$out" "needs" "missing-tools: the row names the tool it needs"
  expect_no_calls "fugaro doctor" "missing-tools"
}

case_no_args() {
  setup_case no-args
  run750
  expect_status "$status" 2 "no-args"
  expect_contains "$out" "--installation is required" "no-args"
  expect_no_calls "fugaro doctor" "no-args: nothing is called without an installation"
}

case_pr_is_required() {
  setup_case no-pr
  run750 --installation "$INSTALLATION"
  expect_status "$status" 2 "no-pr"
  expect_contains "$out" "--pr is required" "no-pr"
}

case_pr_needs_a_number() {
  setup_case pr-number
  run750 --installation "$INSTALLATION" --pr abc
  expect_status "$status" 2 "pr-number"
  expect_contains "$out" "not a pull request number" "pr-number"
  expect_no_calls "fugaro doctor" "pr-number"
}

case_unknown_argument() {
  setup_case unknown
  run750 --installation "$INSTALLATION" --pr "$PR" --repo "$REPO"
  # The check names the repository itself — `--repo .` on the rows read, the
  # checkout it is run from, which is the repository `fugaro review --pr` takes
  # too — so a `--repo` of your own would let the rows and the launcher read two
  # different repositories, and a flag that would do nothing of its own is
  # refused.
  expect_status "$status" 2 "unknown"
  expect_contains "$out" "unknown argument '--repo'" "unknown"
  expect_no_calls "fugaro doctor" "unknown"
}

case_dry_run() {
  setup_case dry-run
  run750 --installation "$INSTALLATION" --pr "$PR" --unmet-pr "$UNMET_PR" --dry-run
  expect_status "$status" 0 "dry-run"
  expect_contains "$out" "This is the PLAN" "dry-run: the plan is what it prints"
  expect_contains "$out" "$INSTALLATION" "dry-run: the plan names the installation"
  expect_contains "$out" "pull request  $PR" "dry-run: the plan names the pull request"
  expect_contains "$out" "pull request $UNMET_PR" "dry-run: the plan names the second pull request"
  expect_contains "$out" "touches" "dry-run: the plan says it touches nothing"
  expect_contains "$out" "--code-only" "dry-run: the plan says what the third review asks for"
  expect_absent "$out" "CHECK 750 RESULT" "dry-run: a plan is not a result block"
  # Nothing at all was called, no scratch directory was made, and no block was
  # printed: a plan is the whole of a dry run.
  if [ -s "$dir/state/calls" ]; then fail "dry-run: the check made a call: $(head -3 "$dir/state/calls")"; fi
  [ -z "$(ls -A "$dir/tmp" 2>/dev/null || true)" ] || fail "dry-run: a scratch directory was left behind"
}

case_help() {
  setup_case help
  run750 --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check-750-review-against-task.sh" "help"
  expect_contains "$out" "--unmet-pr" "help: the second reading's flag is in the usage"
  if [ -s "$dir/state/calls" ]; then fail "help: the check made a call: $(head -3 "$dir/state/calls")"; fi
}

case_knob_installation() {
  setup_case knob-installation
  RUN750_ENV=(CHECK750_INSTALLATION="$INSTALLATION" CHECK750_PR="$PR" CHECK750_UNMET_PR="$UNMET_PR")
  run750
  expect_status "$status" 0 "knob-installation: the environment carries the same three names"
  expect_fact against-run PASS "knob-installation"
}

# ------------------------------------------------------------------ the runner
# Cases are independent, each in its own directory, so a shard can be taken from
# either side: CHECK_TEST_SHARD="i/n" runs every n-th case starting at i, and
# CHECK_TEST_ONLY=name runs that one case alone. Neither set: all run.
case_index=0
t() {
  local i=$case_index
  case_index=$((case_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t case_healthy
t case_no_unmet_pr
t case_against_names_another_run
t case_against_no_run_found
t case_against_file_source
t case_against_no_line
t case_against_show_refused
t case_code_only_line_wrong
t case_code_only_none_other_reason
t case_unmet_verdict_ship
t case_unmet_issue_missing
t case_unmet_pr_is_the_same_pr
t case_pr_not_opened
t case_pr_follow_up_only
t case_pr_review_run_row
t case_pr_two_candidates
t case_pr_rows_unreadable
t case_review_never_terminal
t case_launch_refused
t case_launch_cap_refusal
t case_launch_nameless
t case_cap_unreadable
t case_no_policy_published
t case_release_too_old
t case_release_no_flags
t case_release_dev_build
t case_belong_installation
t case_belong_project
t case_name_mismatch
t case_doctor_refused
t case_agent_session
t case_editor_terminal_is_allowed
t case_xtrace
t case_missing_tools
t case_no_args
t case_pr_is_required
t case_pr_needs_a_number
t case_unknown_argument
t case_dry_run
t case_help
t case_knob_installation

if [ "$failures" -gt 0 ]; then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "check-750-review-against-task_test.sh: all cases passed"
