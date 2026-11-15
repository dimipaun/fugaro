#!/usr/bin/env bash
# scripts/land-prs.sh — merge a batch of pull requests, one command per batch.
#
# The merge helper a landing ends with, run by a person in their own terminal
# for the pull requests they have decided to merge. Every pull request's state
# is read in one parallel sweep, the ones that are open and MERGEABLE are merged
# back to back in the order given, and one line per pull request says what
# happened. It exists so a landing of many pull requests is one command instead
# of a merge per pull request with a wait in between.
#
# The merge call is the person's, at their own hands: this script is how they
# make it for a batch, and it does nothing else. It never force-pushes, never
# rewrites a branch, never runs tests and never decides what is safe to merge:
# the caller has already decided, and the script only merges and reports.
#
# Usage: bash scripts/land-prs.sh [--repo OWNER/NAME] [--check-only]
#                                 [--no-delete-branch] N [N...]
#
#   --repo OWNER/NAME   the repository the numbers belong to; without it gh
#                       answers for the repository of the current checkout
#   --check-only        print the report and merge nothing; exit 0 when every
#                       pull request is open and MERGEABLE, 1 otherwise
#   --no-delete-branch  merge without --delete-branch, leaving each branch
#   N                   a pull request number; the merges happen in the order
#                       the numbers are given
#
# What it does, in order:
#
#   1. every pull request is read at once -- `gh pr view N --json
#      mergeable,isDraft,state,title`, eight at a time -- and one that answers
#      UNKNOWN is polled again after a pause, a bounded number of times;
#   2. one line per pull request is printed before anything is touched;
#   3. every open, MERGEABLE pull request is merged: `gh pr ready N` first when
#      it is a draft, then `gh pr merge N --squash --delete-branch`. One that is
#      CONFLICTING, still UNKNOWN after the polls or not open is skipped and
#      named, never an error that stops the batch, and a merge that fails is
#      reported with gh's last line while the batch goes on. A merge moves the
#      base branch, so the pull requests that were not merged and were not
#      CONFLICTING are read once more at the end and the ones that became
#      MERGEABLE are merged too -- one pass is cheap, and the second catches the
#      ones the first merges unblocked;
#   4. the summary is one line per pull request -- `N: MERGED`,
#      `N: CONFLICTING, not merged`, `N: UNKNOWN, not merged`,
#      `N: not open (STATE)` or `N: merge failed: <gh's last line>` -- and then
#      `merged X of Y`.
#
# Exit: 0 when every pull request ended merged, 1 when any did not (the summary
# lines say which), 2 for a usage error or a tool that is not on PATH.
#
# Environment: LAND_PRS_POLL_SLEEP_SECONDS (default 3) is the pause before a
# poll and LAND_PRS_POLLS (default 3) is how many polls an UNKNOWN gets.
set -Eeuo pipefail

PROG=${0##*/}
POLL_SLEEP=${LAND_PRS_POLL_SLEEP_SECONDS:-3}
POLLS=${LAND_PRS_POLLS:-3}
# Eight reads at a time: enough to make a batch of twenty pull requests one
# round trip rather than twenty, and few enough that gh's own rate limit is not
# the thing that answers.
BATCH=8

usage() {
  cat >&2 <<'USAGE'
usage: land-prs.sh [--repo OWNER/NAME] [--check-only] [--no-delete-branch] N [N...]
  --repo OWNER/NAME    the repository the numbers belong to
  --check-only         report each pull request's state and merge nothing
  --no-delete-branch   merge without --delete-branch
  N                    pull request numbers, merged in the order given
USAGE
}
die() { printf '%s: %s\n' "$PROG" "$*" >&2; exit 2; }
die_usage() {
  printf '%s: %s\n' "$PROG" "$*" >&2
  usage
  exit 2
}

REPO=""
CHECK_ONLY=no
DELETE_BRANCH=yes
PRS=""
while [ $# -gt 0 ]; do
  case "$1" in
  --repo)
    [ $# -ge 2 ] || die_usage "--repo needs OWNER/NAME"
    REPO=$2
    shift 2
    ;;
  --repo=*)
    REPO=${1#--repo=}
    [ -n "$REPO" ] || die_usage "--repo needs OWNER/NAME"
    shift
    ;;
  --check-only)
    CHECK_ONLY=yes
    shift
    ;;
  --no-delete-branch)
    DELETE_BRANCH=no
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  --*) die_usage "unknown option $1" ;;
  *)
    case "$1" in
    "" | *[!0-9]*) die_usage "not a pull request number: $1" ;;
    esac
    PRS="$PRS $1"
    shift
    ;;
  esac
done
[ -n "$PRS" ] || die_usage "give at least one pull request number"

# gh is the only thing this script drives, and python3 is how the JSON it
# answers with is read: without either, every read would come back as a pull
# request that cannot be told apart from a broken one, so the run stops here
# instead.
for tool in gh python3 mktemp; do
  command -v "$tool" >/dev/null 2>&1 || die "land-prs needs '$tool' on PATH and it is not there"
done

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# One slot per pull request, indexed by its number: bash 3.2 has no associative
# arrays, and a number is already a small integer.
STATE=()
DRAFTW=()
MERGE=()
TITLE=()
VERDICT=()
RESULT=()
MSG=()
WAS_MERGEABLE=()

gh_view() { # gh_view N
  if [ -n "$REPO" ]; then
    gh pr view "$1" --repo "$REPO" --json mergeable,isDraft,state,title
  else
    gh pr view "$1" --json mergeable,isDraft,state,title
  fi
}
gh_ready() { # gh_ready N
  if [ -n "$REPO" ]; then
    gh pr ready "$1" --repo "$REPO"
  else
    gh pr ready "$1"
  fi
}
gh_merge() { # gh_merge N
  if [ "$DELETE_BRANCH" = yes ]; then
    if [ -n "$REPO" ]; then
      gh pr merge "$1" --repo "$REPO" --squash --delete-branch
    else
      gh pr merge "$1" --squash --delete-branch
    fi
  else
    if [ -n "$REPO" ]; then
      gh pr merge "$1" --repo "$REPO" --squash
    else
      gh pr merge "$1" --squash
    fi
  fi
}

# last_line prints the last non-blank line of what gh printed: gh puts its own
# sentence about a refusal there, and the lines above it are a stack of advice
# a summary line has no room for.
last_line() {
  printf '%s\n' "$1" | awk 'NF { line = $0 } END { print line }'
}

parse_one() { # parse_one N
  local n=$1 fields state draft merge title
  fields=$(python3 - "$TMP/view-$n.json" <<'PY' || true
import json, sys
try:
    with open(sys.argv[1]) as fh:
        d = json.load(fh)
except Exception:
    d = None
if not isinstance(d, dict):
    # A read that did not answer is not a pull request that is closed: the
    # state is left unknown so nothing is concluded about it.
    print("?\tready\tUNKNOWN\t")
else:
    state = d.get("state") or "?"
    draft = "draft" if d.get("isDraft") else "ready"
    merge = d.get("mergeable") or "UNKNOWN"
    title = d.get("title") or ""
    print("%s\t%s\t%s\t%s" % (state, draft, merge, title))
PY
  )
  if [ -z "$fields" ]; then
    fields=$(printf '?\tready\tUNKNOWN\t')
  fi
  IFS=$'\t' read -r state draft merge title <<<"$fields" || true
  STATE[$n]=$state
  DRAFTW[$n]=$draft
  MERGE[$n]=$merge
  TITLE[$n]=$title
}

read_all() { # read_all "N N N"
  local n=0 pr
  for pr in $1; do
    gh_view "$pr" >"$TMP/view-$pr.json" 2>"$TMP/view-$pr.err" &
    n=$((n + 1))
    if [ "$n" -ge "$BATCH" ]; then
      wait || true
      n=0
    fi
  done
  wait || true
  for pr in $1; do
    parse_one "$pr"
  done
}

# classify turns a read into what the batch will do with it. A read that did not
# answer is UNKNOWN rather than "not open", so a broken call never reads as a
# pull request somebody closed.
classify() { # classify N
  local n=$1
  case "${STATE[$n]:-?}" in
  OPEN)
    case "${MERGE[$n]:-UNKNOWN}" in
    MERGEABLE) VERDICT[$n]=mergeable ;;
    CONFLICTING) VERDICT[$n]=conflicting ;;
    *) VERDICT[$n]=unknown ;;
    esac
    ;;
  "?") VERDICT[$n]=unknown ;;
  *) VERDICT[$n]=notopen ;;
  esac
}

report_one() { # report_one N
  printf '%s: %s, %s, %s, %s\n' "$1" "${STATE[$1]:-?}" "${DRAFTW[$1]:-ready}" "${MERGE[$1]:-UNKNOWN}" "${TITLE[$1]:-}"
}

merge_one() { # merge_one N
  local n=$1 out
  if [ "${DRAFTW[$n]:-ready}" = draft ]; then
    if ! out=$(gh_ready "$n" 2>&1); then
      RESULT[$n]=failed
      MSG[$n]=$(last_line "$out")
      [ -n "${MSG[$n]}" ] || MSG[$n]="gh pr ready $n failed"
      return 0
    fi
  fi
  if out=$(gh_merge "$n" 2>&1); then
    RESULT[$n]=MERGED
    return 0
  fi
  RESULT[$n]=failed
  MSG[$n]=$(last_line "$out")
  [ -n "${MSG[$n]}" ] || MSG[$n]="gh pr merge $n failed"
}

read_all "$PRS"

# An UNKNOWN is GitHub still computing the merge, not a refusal: it is asked
# again after a pause, a bounded number of times, and whatever it answers last
# is what the batch acts on.
attempt=1
while [ "$attempt" -le "$POLLS" ]; do
  unknown=""
  for pr in $PRS; do
    case "${MERGE[$pr]:-UNKNOWN}" in
    UNKNOWN) unknown="$unknown $pr" ;;
    esac
  done
  [ -n "$unknown" ] || break
  sleep "$POLL_SLEEP"
  read_all "$unknown"
  attempt=$((attempt + 1))
done

for pr in $PRS; do
  report_one "$pr"
done

if [ "$CHECK_ONLY" = yes ]; then
  # Nothing is merged, so the question the exit code answers is the one the
  # report above answers: would this batch merge as it stands?
  for pr in $PRS; do
    case "${STATE[$pr]:-?}:${MERGE[$pr]:-UNKNOWN}" in
    OPEN:MERGEABLE) ;;
    *) exit 1 ;;
    esac
  done
  exit 0
fi

for pr in $PRS; do
  classify "$pr"
  case "${VERDICT[$pr]}" in
  mergeable)
    WAS_MERGEABLE[$pr]=yes
    merge_one "$pr"
    ;;
  conflicting) RESULT[$pr]=conflicting ;;
  unknown) RESULT[$pr]=unknown ;;
  notopen) RESULT[$pr]=notopen ;;
  esac
done

# The merges above moved the base branch. A pull request that was not merged and
# was not CONFLICTING is read once more, because the merge before it may be what
# unblocked it; the ones that became MERGEABLE -- were not mergeable when the
# batch started -- are merged, and a merge that failed is not retried here,
# since it was already mergeable and nothing a later merge does changes that.
reread=""
for pr in $PRS; do
  case "${RESULT[$pr]:-unknown}" in
  MERGED | conflicting) ;;
  *) reread="$reread $pr" ;;
  esac
done
if [ -n "$reread" ]; then
  read_all "$reread"
  for pr in $reread; do
    classify "$pr"
    case "${VERDICT[$pr]}" in
    mergeable)
      if [ "${WAS_MERGEABLE[$pr]:-no}" != yes ]; then
        merge_one "$pr"
      fi
      ;;
    *)
      # A merge that already failed is the news about this pull request, so a
      # later read does not overwrite it with a state it was not in before.
      if [ "${RESULT[$pr]:-}" != failed ]; then
        RESULT[$pr]=${VERDICT[$pr]}
      fi
      ;;
    esac
  done
fi

merged=0
total=0
for pr in $PRS; do
  total=$((total + 1))
  case "${RESULT[$pr]:-unknown}" in
  MERGED)
    merged=$((merged + 1))
    printf '%s: MERGED\n' "$pr"
    ;;
  conflicting) printf '%s: CONFLICTING, not merged\n' "$pr" ;;
  unknown) printf '%s: UNKNOWN, not merged\n' "$pr" ;;
  notopen) printf '%s: not open (%s)\n' "$pr" "${STATE[$pr]:-?}" ;;
  failed) printf '%s: merge failed: %s\n' "$pr" "${MSG[$pr]:-}" ;;
  esac
done
printf 'merged %s of %s\n' "$merged" "$total"
[ "$merged" -eq "$total" ]
