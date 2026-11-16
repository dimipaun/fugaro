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
# The merge call is the person's, at their own hands, in the provider's own
# interface: this is the person's own command for making it for a batch -- their
# own gh, the provider's own CLI, under their own credential -- and it does
# nothing else. It never force-pushes, never rewrites a branch, never runs tests
# and never decides what is safe to merge: the caller has already decided, and
# the script only merges and reports.
#
# A coding agent's session is refused before any call is made, because a merge is
# never an agent's. The markers are internal/agentenv's, the same ones every
# owner-run check refuses on.
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
#      mergeable,isDraft,state,title,headRefOid`, eight at a time -- and one that
#      answers UNKNOWN is polled again after a pause, a bounded number of times;
#   2. one line per pull request is printed before anything is touched;
#   3. every open, MERGEABLE pull request is merged: `gh pr ready N` first when
#      it is a draft, then `gh pr merge N --squash --delete-branch
#      --match-head-commit <the head the read named>`. The merge names the commit
#      the sweep saw, so a push between the sweep and the merge is refused by gh
#      instead of landing commits nobody judged, and a draft this marked ready is
#      put back with `gh pr ready N --undo` when the merge is refused, so a
#      refusal never leaves one looking ready. One that is CONFLICTING, still
#      UNKNOWN after the polls or not open is skipped and named, never an error
#      that stops the batch, and a merge that fails is reported with gh's last
#      line while the batch goes on. A merge moves the base branch, so the pull
#      requests that were not merged and were not CONFLICTING are read once more
#      at the end and the ones that became MERGEABLE are merged too -- one pass
#      is cheap, and the second catches the ones the first merges unblocked;
#   4. the summary is one line per pull request -- `N: MERGED`,
#      `N: CONFLICTING, not merged`, `N: UNKNOWN, not merged`,
#      `N: not open (STATE)` or `N: merge failed: <gh's last line>` -- and then
#      `merged X of Y`.
#
# Exit: 0 when every pull request ended merged, 1 when any did not (the summary
# lines say which), 2 for a usage error, for a coding agent's session or for a
# tool that is not on PATH.
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

# guard_agent refuses a coding agent's session, before any call is made: a merge
# is the person's to make and never an agent's, and this script is written to be
# run by the person. The rule is internal/agentenv's Session -- an agent-set
# marker whatever the terminal, because an agent's own processes set it and its
# tool calls inherit it, and the IDE extension's marker, which a person's own
# integrated terminal sets too, only where no real terminal is attached.
guard_agent() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then
      marker=$name
      break
    fi
  done
  if [ -z "$marker" ] && [ -n "$(printenv CLAUDE_CODE_SSE_PORT 2>/dev/null || true)" ] && [ ! -t 0 ]; then
    marker=CLAUDE_CODE_SSE_PORT
  fi
  if [ -n "$marker" ]; then
    die "$marker says this shell is a coding agent's session, and a merge is a person's to make: run $PROG in your own terminal window, or make the merge in the provider's own interface"
  fi
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
    0*)
      # The per-pull-request slots are bash array subscripts, and a subscript is
      # evaluated arithmetically: 08 is a base-8 number that does not parse, and
      # 011 is 9, which would share a slot with pull request 9 and report and
      # merge it on that one's state.
      die_usage "not a pull request number: $1"
      ;;
    esac
    PRS="$PRS $1"
    shift
    ;;
  esac
done
[ -n "$PRS" ] || die_usage "give at least one pull request number"

guard_agent

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
HEAD=()
VERDICT=()
RESULT=()
MSG=()
WAS_MERGEABLE=()

gh_view() { # gh_view N
  if [ -n "$REPO" ]; then
    gh pr view "$1" --repo "$REPO" --json mergeable,isDraft,state,title,headRefOid
  else
    gh pr view "$1" --json mergeable,isDraft,state,title,headRefOid
  fi
}
gh_ready() { # gh_ready N
  if [ -n "$REPO" ]; then
    gh pr ready "$1" --repo "$REPO"
  else
    gh pr ready "$1"
  fi
}
gh_unready() { # gh_unready N
  if [ -n "$REPO" ]; then
    gh pr ready "$1" --repo "$REPO" --undo
  else
    gh pr ready "$1" --undo
  fi
}
gh_merge() { # gh_merge N
  local args=()
  [ -n "$REPO" ] && args+=(--repo "$REPO")
  args+=(--squash)
  if [ "$DELETE_BRANCH" = yes ]; then args+=(--delete-branch); fi
  args+=(--match-head-commit "${HEAD[$1]:-}")
  gh pr merge "$1" "${args[@]}"
}

# last_line prints the last non-blank line of what gh printed: gh puts its own
# sentence about a refusal there, and the lines above it are a stack of advice
# a summary line has no room for.
last_line() {
  printf '%s\n' "$1" | awk 'NF { line = $0 } END { print line }'
}

parse_one() { # parse_one N
  local n=$1 fields state draft merge head title
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
    print("?|ready|UNKNOWN||")
else:
    state = d.get("state") or "?"
    draft = "draft" if d.get("isDraft") else "ready"
    merge = d.get("mergeable") or "UNKNOWN"
    head = d.get("headRefOid") or ""
    # The head is printed before the title, and the title is flattened: the
    # fields are read from one line, so a title holding a tab or a newline must
    # not be able to shift the head the merge is pinned to, and the report is
    # one line per pull request.
    title = d.get("title") or ""
    for ch in ("\t", "\n", "\r"):
        title = title.replace(ch, " ")
    print("%s|%s|%s|%s|%s" % (state, draft, merge, head, title))
PY
  )
  if [ -z "$fields" ]; then
    fields=$(printf '?|ready|UNKNOWN||')
  fi
  # The separator is not whitespace on purpose: read collapses runs of a
  # whitespace IFS into one, so a tab-separated line with an empty field in it
  # -- a pull request with no title, or a read that named no head -- would shift
  # every field after it.
  IFS='|' read -r state draft merge head title <<<"$fields" || true
  STATE[$n]=$state
  DRAFTW[$n]=$draft
  MERGE[$n]=$merge
  TITLE[$n]=$title
  HEAD[$n]=$head
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
  local n=$1 out undo_out was_draft=no
  # A merge names the commit it lands. Without one from the read there is
  # nothing to pin the merge to, and merging unpinned is the thing the pin is
  # for, so this is not merged at all.
  if [ -z "${HEAD[$n]:-}" ]; then
    RESULT[$n]=failed
    MSG[$n]="no head commit was read for $n"
    return 0
  fi
  if [ "${DRAFTW[$n]:-ready}" = draft ]; then
    was_draft=yes
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
  if [ "$was_draft" = yes ]; then
    # This script is what marked the draft ready, and nothing merged: a refusal
    # must not leave a pull request looking ready for review, so it is put back
    # the way it was found. A pull request that was already ready, or that
    # merged, is never touched, and the merge's own refusal stays first on the
    # line.
    if undo_out=$(gh_unready "$n" 2>&1); then
      MSG[$n]="${MSG[$n]}; put back to draft with gh pr ready $n --undo"
    else
      MSG[$n]="${MSG[$n]}; gh pr ready $n --undo failed: $(last_line "$undo_out")"
    fi
  fi
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
