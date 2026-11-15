#!/usr/bin/env bash
# Tests for scripts/land-prs.sh, with a fake `gh` on PATH: NOTHING here reaches
# GitHub, no pull request is merged and no branch is deleted. The fake answers
# each `pr view` out of a file the case wrote -- one file per read, so a case
# can make a pull request answer UNKNOWN once and MERGEABLE the next time it is
# asked -- and records every call it is given, so a case can say what the script
# did and, just as often, what it did not do.
#
# The cases that matter are the ones where a batch is not uniform: one
# CONFLICTING pull request among mergeable ones, an UNKNOWN that settles, an
# UNKNOWN that does not, a draft, a merge gh refuses, a closed pull request, and
# a pull request that only becomes mergeable once the merges before it moved the
# base branch. One more is the session the script refuses: a coding agent's
# markers make it exit 2 without calling gh, since a merge is a person's to make.
#
# Usage: bash scripts/land-prs_test.sh
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/land-prs.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${LAND_PRS_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${LAND_PRS_TEST_HEAD:-900}")" ;;
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
expect_equal() { # got want what
  [ "$1" = "$2" ] || fail "$3: expected '$2', got '$1'"
}

bin=$work/bin
mkdir -p "$bin"
cat >"$bin/gh" <<'FAKE'
#!/usr/bin/env bash
set -Eeuo pipefail
dir=${FAKE_GH_DIR:?}
printf '%s\n' "$*" >>"$dir/calls"
if [ "${1:-}" != pr ]; then
  printf 'fake gh: only `pr` is modelled, got: %s\n' "$*" >&2
  exit 1
fi
case "${2:-}" in
view)
  n=${3:-}
  count_file=$dir/view.$n.count
  count=0
  [ -f "$count_file" ] && count=$(cat "$count_file")
  count=$((count + 1))
  printf '%s\n' "$count" >"$count_file"
  answer=$dir/view.$n.$count.json
  [ -f "$answer" ] || answer=$dir/view.$n.json
  if [ ! -f "$answer" ]; then
    printf 'no pull request found for %s\n' "$n" >&2
    exit 1
  fi
  cat "$answer"
  ;;
ready)
  printf 'Pull request #%s is marked as ready for review\n' "${3:-}"
  ;;
merge)
  n=${3:-}
  if [ -f "$dir/merge.$n.fail" ]; then
    cat "$dir/merge.$n.fail" >&2
    exit 1
  fi
  printf 'Squashed and merged pull request #%s\n' "$n"
  ;;
*)
  printf 'fake gh: unsupported subcommand: %s\n' "${2:-}" >&2
  exit 1
  ;;
esac
FAKE
chmod +x "$bin/gh"

case_dir=""
new_case() { # new_case NAME
  case_dir=$work/case-$1
  mkdir -p "$case_dir"
  : >"$case_dir/calls"
}
view() { # view N STATE DRAFT MERGEABLE TITLE [READ]
  local n=$1 state=$2 draft=$3 merge=$4 title=$5 read=${6:-}
  local file
  if [ -n "$read" ]; then
    file=$case_dir/view.$n.$read.json
  else
    file=$case_dir/view.$n.json
  fi
  printf '{"state":"%s","isDraft":%s,"mergeable":"%s","title":"%s"}\n' \
    "$state" "$draft" "$merge" "$title" >"$file"
}
merge_fails() { # merge_fails N "what gh prints"
  printf '%s\n' "$2" >"$case_dir/merge.$1.fail"
}
calls() { cat "$case_dir/calls"; }
count_calls() { # count_calls PATTERN
  local n
  n=$(grep -c "$1" "$case_dir/calls" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}
line_of() { # line_of PATTERN -> the first line of the call log it matches, or 0
  local n
  n=$(grep -n "$1" "$case_dir/calls" 2>/dev/null | head -n 1 | cut -d: -f1)
  printf '%s' "${n:-0}"
}
views_before_first_merge() {
  local first
  first=$(line_of '^pr merge')
  if [ "$first" -le 1 ]; then
    printf '0'
    return 0
  fi
  sed -n "1,$((first - 1))p" "$case_dir/calls" | grep -c '^pr view' || true
}

OUT=""
RC=0
# The script refuses a coding agent's session, and this suite is usually run from
# one: `go test` under a coding agent inherits CLAUDECODE and AI_AGENT, and an
# IDE window sets CLAUDE_CODE_SSE_PORT. Every case but the refusal's drops those
# markers first, so what a case measures is the script and not the session the
# test was started in. Stdin is /dev/null for the same reason: the IDE marker is
# a refusal only where there is no terminal on the input, and whether this suite
# was started from a terminal must not decide a case.
unset_markers=""
for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT CLAUDE_CODE_SSE_PORT; do
  unset_markers="$unset_markers -u $marker"
done

land() { # land ARGS...
  OUT=$(env $unset_markers PATH="$bin:$PATH" FAKE_GH_DIR="$case_dir" \
    LAND_PRS_POLL_SLEEP_SECONDS="${LAND_PRS_TEST_POLL_SLEEP:-0}" \
    "$script" "$@" 2>&1 </dev/null) && RC=0 || RC=$?
}
agent_land() { # agent_land MARKER ARGS...
  local marker=$1
  shift
  OUT=$(env $unset_markers "$marker=1" PATH="$bin:$PATH" FAKE_GH_DIR="$case_dir" \
    LAND_PRS_POLL_SLEEP_SECONDS="${LAND_PRS_TEST_POLL_SLEEP:-0}" \
    "$script" "$@" 2>&1 </dev/null) && RC=0 || RC=$?
}

# ------------------------------------------------------------- all mergeable
new_case all-merge
view 11 OPEN false MERGEABLE "the first change"
view 12 OPEN false MERGEABLE "the second change"
view 13 OPEN false MERGEABLE "the third change"
land 11 12 13
expect_status "$RC" 0 "three mergeable pull requests exit 0"
for n in 11 12 13; do
  expect_contains "$OUT" "$n: MERGED" "pull request $n is reported merged"
  expect_contains "$(calls)" "pr merge $n --squash --delete-branch" "pull request $n is merged with a branch delete"
done
expect_contains "$OUT" "11: OPEN, ready, MERGEABLE, the first change" "the report line carries state, draft, mergeable and title"
expect_contains "$OUT" "merged 3 of 3" "the summary counts all three"
# The report is printed before anything is touched, so every report line is
# above every summary line.
report_line=$(printf '%s\n' "$OUT" | grep -n '^13: OPEN' | head -n 1 | cut -d: -f1)
summary_line=$(printf '%s\n' "$OUT" | grep -n '^11: MERGED' | head -n 1 | cut -d: -f1)
[ -n "$report_line" ] && [ -n "$summary_line" ] && [ "$report_line" -lt "$summary_line" ] ||
  fail "the report is not printed before the merges ($report_line, $summary_line)"
expect_contains "$(calls)" "pr view 11 --json mergeable,isDraft,state,title" "the read is the one the script documents"
expect_absent "$(calls)" "pr ready" "a pull request that is not a draft is not marked ready"
# No sleeping between merges: every read happened before the first merge, so the
# batch costs one sweep of reads and then one merge after another.
expect_equal "$(views_before_first_merge)" 3 "the reads before the first merge are the pull requests"

# ------------------------------------------------------------- one conflicting
new_case conflicting
view 21 OPEN false MERGEABLE "the mergeable one"
view 22 OPEN false CONFLICTING "the conflicting one"
view 23 OPEN false MERGEABLE "the one after it"
land 21 22 23
expect_status "$RC" 1 "a batch with a conflicting pull request exits 1"
expect_contains "$OUT" "22: CONFLICTING, not merged" "the conflicting pull request is named, not an error"
expect_contains "$OUT" "21: MERGED" "the pull request before it merged"
expect_contains "$OUT" "23: MERGED" "the pull request after it merged, so the batch did not stop"
expect_contains "$OUT" "merged 2 of 3" "the summary counts two of three"
expect_absent "$(calls)" "pr merge 22" "a conflicting pull request is never merged"
expect_contains "$OUT" "22: OPEN, ready, CONFLICTING, the conflicting one" "the report line carries state, draft, mergeable and title"

# -------------------------------------------------- unknown that settles, polled
new_case unknown-then-mergeable
view 31 OPEN false UNKNOWN "the one GitHub was still computing" 1
view 31 OPEN false MERGEABLE "the one GitHub was still computing" 2
land 31
expect_status "$RC" 0 "an UNKNOWN that becomes MERGEABLE is merged"
expect_contains "$OUT" "31: MERGED" "the polled pull request is reported merged"
expect_contains "$OUT" "merged 1 of 1" "the summary counts it"
expect_equal "$(count_calls '^pr view 31')" 2 "the UNKNOWN pull request is read once more, not in a loop"

# ------------------------------------------------------- unknown that never settles
new_case unknown-stays
view 41 OPEN false UNKNOWN "the one GitHub never settled"
land 41
expect_status "$RC" 1 "an UNKNOWN that never settles exits 1"
expect_contains "$OUT" "41: UNKNOWN, not merged" "the unsettled pull request is named"
expect_contains "$OUT" "merged 0 of 1" "nothing was merged"
expect_absent "$(calls)" "pr merge 41" "an UNKNOWN pull request is never merged"

# --------------------------------------------------------------- a draft is ready
new_case draft
view 51 OPEN true MERGEABLE "the draft"
land 51
expect_status "$RC" 0 "a draft that is mergeable is landed"
expect_contains "$OUT" "51: OPEN, draft, MERGEABLE, the draft" "the report says it is a draft"
expect_contains "$(calls)" "pr ready 51" "the draft is marked ready first"
expect_contains "$(calls)" "pr merge 51 --squash --delete-branch" "the draft is then merged"
ready_line=$(line_of '^pr ready 51')
merge_line=$(line_of '^pr merge 51')
[ "$ready_line" -gt 0 ] && [ "$merge_line" -gt "$ready_line" ] ||
  fail "the draft is merged before it is marked ready ($ready_line, $merge_line)"

# ------------------------------------------------------------- a failing merge
new_case failing-merge
view 61 OPEN false MERGEABLE "the one gh refuses"
view 62 OPEN false MERGEABLE "the one after it"
merge_fails 61 "X Pull request is not mergeable
pull request is not mergeable: the merge commit cannot be cleanly created"
land 61 62
expect_status "$RC" 1 "a failed merge exits 1"
expect_contains "$OUT" "61: merge failed: pull request is not mergeable: the merge commit cannot be cleanly created" \
  "a failed merge is reported with gh's last line"
expect_contains "$OUT" "62: MERGED" "the batch goes on after a failed merge"
expect_contains "$OUT" "merged 1 of 2" "the summary counts the one that merged"
expect_equal "$(count_calls '^pr merge 61')" 1 "a merge that failed is not retried in the second pass"

# ------------------------------------------------------------------- check only
new_case check-only
view 71 OPEN false MERGEABLE "the mergeable one"
view 72 OPEN false CONFLICTING "the conflicting one"
land --check-only 71 72
expect_status "$RC" 1 "--check-only with a conflicting pull request exits 1"
expect_contains "$OUT" "71: OPEN, ready, MERGEABLE, the mergeable one" "--check-only reports each pull request"
expect_contains "$OUT" "72: OPEN, ready, CONFLICTING, the conflicting one" "--check-only reports the conflicting one"
expect_absent "$(calls)" "pr merge" "--check-only merges nothing"
expect_absent "$(calls)" "pr ready" "--check-only marks nothing ready"

new_case check-only-ok
view 73 OPEN false MERGEABLE "the mergeable one"
land --check-only 73
expect_status "$RC" 0 "--check-only with everything mergeable exits 0"
expect_absent "$(calls)" "pr merge" "--check-only merges nothing when everything is mergeable"

# ------------------------------------------------------------------ exit codes
new_case usage
land
expect_status "$RC" 2 "no pull request number is a usage error"
expect_contains "$OUT" "give at least one pull request number" "the usage error says what is missing"
land --nope 81
expect_status "$RC" 2 "an unknown option is a usage error"
land 8x
expect_status "$RC" 2 "a number that is not a number is a usage error"
land --repo
expect_status "$RC" 2 "--repo without a value is a usage error"

# ------------------------------------------------- a coding agent's session
# A merge is a person's to make, and the script refuses an agent's session before
# it calls gh at all: no read, no merge, nothing.
new_case agent
view 101 OPEN false MERGEABLE "the one an agent must not merge"
view 102 OPEN true MERGEABLE "the draft an agent must not merge either"
agent_land CLAUDECODE 101 102
expect_status "$RC" 2 "a coding agent's session is refused"
expect_contains "$OUT" "CLAUDECODE" "the refusal names the marker that says so"
expect_contains "$OUT" "a merge is a person's to make" "the refusal says whose the merge is"
expect_absent "$(calls)" "pr view" "an agent's session reads nothing"
expect_absent "$(calls)" "pr merge" "an agent's session merges nothing"
expect_absent "$(calls)" "pr ready" "an agent's session marks nothing ready"

new_case agent-check-only
view 103 OPEN false MERGEABLE "the one an agent must not read"
agent_land AI_AGENT --check-only 103
expect_status "$RC" 2 "a coding agent's session is refused under --check-only too"
expect_absent "$(calls)" "pr view" "the refusal comes before even a read-only sweep"

new_case agent-ide
view 104 OPEN false MERGEABLE "the one the IDE marker alone must not merge"
agent_land CLAUDE_CODE_SSE_PORT 104
expect_status "$RC" 2 "the IDE extension's marker with no terminal is refused"
expect_absent "$(calls)" "pr view" "the IDE marker with no terminal reads nothing"

# The IDE marker is a person's own integrated terminal, and a person typing there
# is not an agent: with a terminal on the input the script runs as it always did.
new_case agent-ide-terminal
view 105 OPEN false MERGEABLE "the one a person in their IDE terminal lands"
if [ -t 0 ]; then
  OUT=$(env $unset_markers CLAUDE_CODE_SSE_PORT=1 PATH="$bin:$PATH" FAKE_GH_DIR="$case_dir" \
    LAND_PRS_POLL_SLEEP_SECONDS=0 "$script" 105 2>&1) && RC=0 || RC=$?
  expect_status "$RC" 0 "a person's own IDE terminal is not an agent's session"
  expect_contains "$OUT" "105: MERGED" "a person's own IDE terminal lands the batch"
else
  # Without a terminal on the input there is nothing to tell a person's IDE
  # terminal from an agent's tool call, so the case can only be run where one is.
  printf 'note: no terminal on stdin, the IDE-terminal case is skipped\n' >&2
fi

# ---------------------------------------------------------------- not open
new_case closed
view 91 CLOSED false MERGEABLE "the closed one"
view 92 OPEN false MERGEABLE "the open one"
land 91 92
expect_status "$RC" 1 "a closed pull request in the batch exits 1"
expect_contains "$OUT" "91: not open (CLOSED)" "a pull request that is not open is named with its state"
expect_absent "$(calls)" "pr merge 91" "a closed pull request is never merged"
expect_contains "$OUT" "92: MERGED" "the open pull request still merged"

# ------------------------------------------------- a read that did not answer
new_case broken-read
view 93 OPEN false MERGEABLE "the one after it"
land 94 93
expect_status "$RC" 1 "a read that did not answer exits 1"
expect_contains "$OUT" "94: UNKNOWN, not merged" "a read that did not answer is UNKNOWN, never 'not open'"
expect_contains "$OUT" "93: MERGED" "the rest of the batch still merged"

# ---------------------------------------------------------------- the flags
new_case no-delete-branch
view 95 OPEN false MERGEABLE "the one whose branch stays"
land --no-delete-branch 95
expect_status "$RC" 0 "--no-delete-branch still lands the batch"
expect_contains "$(calls)" "pr merge 95 --squash" "the merge is a squash"
expect_absent "$(calls)" "--delete-branch" "--no-delete-branch drops the flag"

new_case repo
view 96 OPEN false MERGEABLE "the one in another repository"
land --repo acme/widgets 96
expect_status "$RC" 0 "a batch in a named repository exits 0"
expect_contains "$(calls)" "pr view 96 --repo acme/widgets --json mergeable,isDraft,state,title" "the read names the repository"
expect_contains "$(calls)" "pr merge 96 --repo acme/widgets --squash --delete-branch" "the merge names the repository"

# --------------------------------------------------------------- second pass
# 112 is UNKNOWN through the first read and every poll, and only becomes
# MERGEABLE once 111 has merged: the second pass is what lands it.
new_case second-pass
view 111 OPEN false MERGEABLE "the one that merges first"
for read in 1 2 3 4; do
  view 112 OPEN false UNKNOWN "the one the first merge unblocks" "$read"
done
view 112 OPEN false MERGEABLE "the one the first merge unblocks" 5
land 111 112
expect_status "$RC" 0 "a pull request unblocked by a merge is landed in the second pass"
expect_contains "$OUT" "111: MERGED" "the first pull request merged"
expect_contains "$OUT" "112: MERGED" "the pull request the merge unblocked merged too"
expect_contains "$OUT" "merged 2 of 2" "the summary counts both"

if [ "$failures" -ne 0 ]; then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "land-prs_test: ok"
