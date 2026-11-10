#!/usr/bin/env bash
# Post-tag verification of one CLI release (docs/release.md, "Verifying a
# release"). scripts/release.sh calls this at the end of both of its paths --
# the ordinary one and --hotfix -- and it runs on its own too:
#
#   scripts/verify-release.sh vX.Y.Z [--timeout-minutes N]
#
# What it holds, in order. The release rule it exists for is the one recorded
# in docs/release.md: a release is not done until every workflow on its tag is
# green.
#
#   1. The tag is a strict CLI vX.Y.Z and is on origin at the commit this
#      checkout holds. The console's and the app's streams release on their own
#      tags, console-vX.Y.Z and mobile-vX.Y.Z, with their own paths; this
#      script verifies neither.
#   2. Every workflow run the tag push started -- the runs whose event is
#      `push` and whose head branch is the tag: release.yml, images.yml,
#      console-windows.yml and any other workflow a strict vX.Y.Z tag triggers
#      -- has concluded, and
#      each concluded successfully. It waits for up to --timeout-minutes
#      (default 45) for a run still running or not yet registered, and fails at
#      once on a conclusive failure, like the release gate.
#   3. The images the release published can be pulled without a login, checked
#      by calling images/verify-public.sh -- the same script, on the same
#      collection of references images.yml's own verify-public job builds for
#      the tag (<version> for every kind whose Dockerfile the tagged tree
#      holds, <major> too when the tag is the top of its major, base with
#      --arm64). This script carries no registry logic and no second opinion
#      about what a release publishes: a change to either belongs in images.yml
#      and reaches this check with it.
#   4. For a patch tag (Z of 1 or more, the shape docs/release.md's "Hotfix
#      releases" cut from release/X.Y): every commit the tag carries and main
#      does not is one of exactly two things. It is a cherry-pick of a commit on
#      main -- the fix and the plugin bump the branch was cut with are both
#      picked from it with -x (`git cherry-pick -x`, which records
#      "(cherry picked from commit <sha>)"), the commit the trailer names has to
#      be an ancestor of origin/main, and its patch-id has to be this commit's,
#      so a trailer cannot vouch for a change that is not the one it names -- or
#      it is one of the three files the release's own text lives in (the notes
#      for this version, its line in the release summaries and the cli stream's
#      current), which the branch's hand-written commits are. A commit that is
#      neither is a change of its own that no commit of main carries, and the
#      branch is never merged back, so main loses it: that, and a tree whose own
#      commits carry no trailer at all (nothing to hold them to main by), are
#      refused. main's own plugin version must be the tag's or newer too, the
#      same rule scripts/release-gate.sh refuses on.
#
# Read-only: it fetches, reads the GitHub API and pulls image manifests, and
# changes nothing on origin -- no tag, no branch, no release. Needs gh
# (authenticated), git, docker, and a checkout of this repository with its
# origin remote. GITHUB_REPOSITORY names the repository when it is set (the
# workflows set it, and scripts/release.sh passes it); otherwise `gh repo view`
# answers from the checkout.
set -eu

repo_root=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
# A dropped connection to GitHub while waiting is retried, a bounded number of
# times, rather than failing the verification (scripts/retry.sh).
. scripts/retry.sh
. scripts/versions.sh

timeout_minutes=45
tag=""
while [ $# -gt 0 ]; do
  case "$1" in
    --timeout-minutes)
      if [ $# -lt 2 ]; then
        echo "--timeout-minutes needs a value (a whole number of minutes, at least 1)" >&2
        exit 2
      fi
      shift
      timeout_minutes=$1
      ;;
    -*)
      echo "unknown flag: $1" >&2
      exit 2
      ;;
    *)
      if [ -n "$tag" ]; then
        echo "unexpected argument: $1" >&2
        exit 2
      fi
      tag=$1
      ;;
  esac
  shift
done
if [ -z "$tag" ]; then
  echo "usage: $0 vX.Y.Z [--timeout-minutes N]" >&2
  exit 2
fi
if ! [[ $timeout_minutes =~ ^[1-9][0-9]*$ ]]; then
  echo "--timeout-minutes wants a whole number of minutes, at least 1; got '$timeout_minutes'" >&2
  exit 2
fi
if ! [[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "'$tag' is not a strict vX.Y.Z: this verifies the CLI stream's releases (a console release is console-vX.Y.Z and an app release mobile-vX.Y.Z, each with its own path; docs/release.md, \"There are three release streams\")" >&2
  exit 1
fi
version=${tag#v}
# z is the patch component: a tag with Z of 1 or more is the one shape the
# release gate accepts off main, on the maintenance branch release/X.Y, and it
# is the shape that carries cherry-picked fixes to check.
z=${version##*.}
poll_seconds=${VERIFY_RELEASE_POLL_SECONDS:-30}
# VERIFY_RELEASE_TIMEOUT_SECONDS exists for the tests only (--timeout-minutes
# has a one-minute floor).
timeout_seconds=${VERIFY_RELEASE_TIMEOUT_SECONDS:-$((timeout_minutes * 60))}

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is not installed; see https://cli.github.com" >&2
  exit 1
fi
if ! retry_read --any gh auth status >/dev/null 2>&1; then
  echo "gh is not authenticated; run 'gh auth login'" >&2
  exit 1
fi
repo=${GITHUB_REPOSITORY:-}
if [ -z "$repo" ]; then
  repo=$(retry_read gh repo view --json nameWithOwner --jq .nameWithOwner) || {
    echo "'gh repo view' failed; run this from inside the fugaro checkout, or set GITHUB_REPOSITORY" >&2
    exit 1
  }
fi
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "not inside a git checkout; this reads the tagged tree and main from it" >&2
  exit 1
fi

# ---- 1. the tag, on origin, at the commit this checkout holds --------------
retry_read git fetch --quiet origin main --tags --prune
if ! retry_read git fetch --quiet origin "refs/tags/$tag:refs/tags/$tag"; then
  echo "tag $tag is not on origin, or this checkout's own $tag points somewhere else; delete a stale local tag (git tag -d $tag) and re-run" >&2
  exit 1
fi
tip=$(git rev-parse --quiet --verify "refs/tags/$tag^{commit}") || {
  echo "tag $tag does not resolve to a commit" >&2
  exit 1
}
# ls-remote is asked for the peeled commit ("^{}"): the tag name on its own
# resolves to the tag object of an annotated tag, whose sha is not the commit
# the tag points at (which is what this compares against). A lightweight tag has
# no peeled line, so the plain ref answers for that shape.
remote_tip=$(retry_read git ls-remote origin "refs/tags/$tag^{}" | cut -f1 | head -n1)
if [ -z "$remote_tip" ]; then
  remote_tip=$(retry_read git ls-remote origin "refs/tags/$tag" | cut -f1 | head -n1)
fi
if [ -z "$remote_tip" ]; then
  echo "tag $tag is not on origin" >&2
  exit 1
fi
if [ "$remote_tip" != "$tip" ]; then
  echo "origin's $tag is $remote_tip but this checkout's is $tip; the tag moved, or the fetch did not land" >&2
  exit 1
fi

# patch_of prints the git patch-id of one commit's diff: what "the same change"
# means below. `--stable` is spelled out because the other algorithm varies with
# the git version; awk keeps the id alone, dropping the commit column git
# patch-id prints beside it. A diff with nothing in it -- a merge, an empty
# commit -- prints nothing at all, which compares equal on both sides and is
# right: there is no change there that could have been lost.
patch_of() {
  git diff-tree --no-commit-id -p -r "$1" | git patch-id --stable | awk '{print $1}'
}

# ---- 4 (before the wait: a missing fix is worth knowing at once) ----------
# patch_note is the paragraph the summary prints for a patch tag; empty when
# the tag is not one.
patch_note=""
if [ "$z" -ge 1 ]; then
  if [ "$(printf '%s\n%s\n' "$tip" "$(git rev-parse refs/remotes/origin/main)" | sort -u | wc -l)" = 1 ] \
    || git merge-base --is-ancestor "$tip" refs/remotes/origin/main; then
    # The tag is on main: a patch cut from main (the gate accepts that shape
    # too), with no branch history of its own to check.
    patch_note="on main"
  else
    # The tag's own commits: what the maintenance branch carries and main does
    # not. A tree with not one trailer in it is refused before any of it is
    # read: a patch release carries the plugin bump main holds, so a branch
    # whose own commits are all hand-written is a branch nothing holds to main.
    # Then each commit, which is a cherry-pick of a commit on main or one of the
    # three files the release's own text lives in; the header comment of this
    # file says why, and what a commit that is neither means.
    trailers=$(git log --format=%B "$tip" --not refs/remotes/origin/main \
      | sed -n 's/.*(cherry picked from commit \([0-9a-f]\{7,40\}\)).*/\1/p' | sort -u)
    if [ -z "$trailers" ]; then
      echo "$tag is not on main and none of its own commits carries a \"(cherry picked from commit <sha>)\" trailer: a patch release cherry-picks its fixes onto release/X.Y with -x (git cherry-pick -x), and without one there is nothing to hold the release to main (docs/release.md, \"Hotfix releases\")" >&2
      exit 1
    fi
    n=0
    for commit in $(git log --format=%H "$tip" --not refs/remotes/origin/main); do
      picked=$(git log -1 --format=%B "$commit" \
        | sed -n 's/.*(cherry picked from commit \([0-9a-f]\{7,40\}\)).*/\1/p')
      if [ -n "$picked" ]; then
        for sha in $picked; do
          if ! git merge-base --is-ancestor "$sha" refs/remotes/origin/main 2>/dev/null; then
            echo "$tag carries a commit that is not on origin/main: the commit $sha, which some commit of this tag names in its \"(cherry picked from commit ...)\" trailer. A fix has to land on main first, through a PR as usual, so main never loses it (docs/release.md, \"Hotfix releases\")" >&2
            exit 1
          fi
          if [ "$(patch_of "$sha")" != "$(patch_of "$commit")" ]; then
            echo "$tag carries a commit whose trailer names $sha, and the change under it is not the change $sha makes ($(git log -1 --format=%h "$commit") is not the pick it says it is, or the pick was applied over different content than main's). A hotfix's cherry-picks carry the fix as main has it: a trailer vouches for a main commit only when the diff under it is that commit's (docs/release.md, \"Hotfix releases\")" >&2
            exit 1
          fi
          n=$((n + 1))
        done
        continue
      fi
      # No trailer: the only thing a hotfix writes by hand on the branch is the
      # release's own text, so a commit with no trailer may touch those three
      # paths and nothing else.
      for path in $(git diff-tree --no-commit-id --name-only -r "$commit"); do
        case "$path" in
          "docs/releases/$tag.md" | website/data/release-summaries.yaml | docs/release-streams.yaml) ;;
          *)
            echo "$tag carries a commit that is not on origin/main and is not a cherry-pick of one that is: $(git log -1 --format=%h "$commit") \"$(git log -1 --format=%s "$commit")\" changes $path. A hotfix's commits are the fix and the plugin bump cherry-picked from main with -x, or the release's own text -- docs/releases/$tag.md, its line in website/data/release-summaries.yaml and the cli stream's current in docs/release-streams.yaml -- and nothing else: a fix committed straight onto the branch is on no commit of main, and the branch is never merged back, so main loses it (docs/release.md, \"Hotfix releases\")" >&2
            exit 1
            ;;
        esac
      done
    done
    patch_note="$n cherry-picked commit(s), each on origin/main"
  fi
  # main's plugin version must be the tag's or newer: the branch is never
  # merged back, so a plugin version only the branch carries leaves main
  # serving a plugin older than a published tag (scripts/release-gate.sh
  # refuses such a tag with this same rule, read the same way).
  main_plugin=$(git show refs/remotes/origin/main:plugin/.claude-plugin/plugin.json 2>/dev/null \
    | sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
  if [[ ! $main_plugin =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
    || [ "$(printf '%s\n%s\n' "$main_plugin" "$version" | highest_version)" != "$main_plugin" ]; then
    echo "main's plugin/.claude-plugin/plugin.json says '${main_plugin:-nothing}' and $tag is $version: a patch release's plugin bump is made on main first (docs/release.md, \"Hotfix releases\"), and scripts/release-gate.sh refuses such a tag too" >&2
    exit 1
  fi
fi

# ---- 2. every workflow run the tag started --------------------------------
# runs_jq is the --jq this asks the API for: one tab-separated line per run --
# name, status, conclusion, head branch, event. scripts/testdata/fake-gh.sh
# answers this same shape for the tests; keep the two in step.
runs_jq='.workflow_runs[] | [.name, .status, (.conclusion // "-"), (.head_branch // "-"), .event] | @tsv'
# required_runs is every workflow a strict vX.Y.Z tag starts: those whose
# `on: push: tags:` filter is `v[0-9]+.[0-9]+.[0-9]+` -- release.yml, images.yml
# and console-windows.yml (the console's Windows pack, which runs at release
# time only) -- and a fourth added to that filter belongs here too. The wait
# below is why they are named rather than inferred: a run registers a few
# seconds after the push, and a verification that read the API once and found
# no run would pass a release whose workflows never started. Any other run the
# tag started is held to the same rule, from the same read.
required_runs="release images console-windows"
echo "waiting for every workflow the $tag push started (up to ${timeout_minutes}m)..."
deadline=$(($(date +%s) + timeout_seconds))
while :; do
  runs=$(retry_read gh api "repos/$repo/actions/runs?head_sha=$tip&per_page=100" --jq "$runs_jq") || {
    echo "reading the tag's workflow runs failed (repos/$repo/actions/runs?head_sha=$tip)" >&2
    exit 1
  }
  seen=""
  waiting=""
  failed=""
  while IFS=$'\t' read -r name status conclusion head_branch event; do
    [ -n "${name:-}" ] || continue
    [ "$head_branch" = "$tag" ] || continue
    [ "$event" = push ] || continue
    seen="$seen $name"
    if [ "$status" != completed ]; then
      [ -n "$waiting" ] || waiting="$name is $status"
      continue
    fi
    [ "$conclusion" = success ] || { [ -n "$failed" ] || failed="$name concluded $conclusion"; }
  done <<<"$runs"
  if [ -n "$failed" ]; then
    echo "::error::the release is not done: $failed on $tag ($tip). A green run on every workflow the tag started is what a finished release means (docs/release.md); fix the cause and re-run the failed job, then re-run scripts/verify-release.sh $tag" >&2
    exit 1
  fi
  missing=""
  for name in $required_runs; do
    case " $seen " in
      *" $name "*) ;;
      *) [ -n "$missing" ] || missing="$name has no run on $tag yet" ;;
    esac
  done
  if [ -z "$waiting" ] && [ -z "$missing" ]; then
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "::error::$tag is not released: ${waiting:-${missing:-a workflow run}} after ${timeout_minutes}m (every workflow the tag started has to be green before the release is done; re-run scripts/verify-release.sh $tag when they are)" >&2
    exit 1
  fi
  echo "still waiting: ${waiting:-$missing}"
  sleep "$poll_seconds"
done

# ---- 3. the images the release published ----------------------------------
# images.yml's own two rules, read from the tagged tree the same way that
# workflow reads them: "Which images this tree builds" probes images/$k/
# Dockerfile for the three kinds (a kind the tree does not hold has nothing to
# build, publish or verify, which is how an older tag -- or a backport -- is
# asked for exactly what it published), and `<major>` moves only to the top of
# a major, so a backport tag does not move it (v1.1.5 after v1.2.0 publishes
# :1.1.5 alone). The refs are then the refs verify-public checks there, base's
# --arm64 included; only the reporting differs.
refs=""
base_refs=""
move_major=0
top=$(git tag -l "v${version%%.*}.*" | sed -n 's/^v//p' | highest_version)
if [ -z "$top" ]; then
  # The moving <major> ref is checked only when this tag is the top of its
  # major, so a read that answers nothing would skip it and still report the
  # release verified: refuse rather than verify less than the summary says.
  echo "no strict v${version%%.*}.* tag could be read from this checkout, so whether $tag is the top of its major cannot be known (the moving <major> image ref is published only at the top of one): fetch the tags (git fetch --tags) and re-run scripts/verify-release.sh $tag" >&2
  exit 1
fi
top="v$top"
[ "$top" = "$tag" ] && move_major=1
for kind in history base credentials; do
  git cat-file -e "refs/tags/$tag:images/$kind/Dockerfile" 2>/dev/null || continue
  if [ "$kind" = base ]; then
    base_refs="$base_refs ghcr.io/dimipaun/fugaro-base:${version}"
    [ "$move_major" = 0 ] || base_refs="$base_refs ghcr.io/dimipaun/fugaro-base:${version%%.*}"
  else
    refs="$refs ghcr.io/dimipaun/fugaro-$kind:${version}"
    [ "$move_major" = 0 ] || refs="$refs ghcr.io/dimipaun/fugaro-$kind:${version%%.*}"
  fi
done
if [ -n "$refs" ] || [ -n "$base_refs" ]; then
  if ! command -v docker >/dev/null 2>&1; then
    echo "docker is not on PATH, and images/verify-public.sh is what checks a released image can be pulled without a login (the mirror and a plain docker pull both need that): install docker, then re-run scripts/verify-release.sh $tag" >&2
    exit 1
  fi
  # shellcheck disable=SC2086
  [ -z "$refs" ] || images/verify-public.sh $refs || exit 1
  # shellcheck disable=SC2086
  [ -z "$base_refs" ] || images/verify-public.sh --arm64 $base_refs || exit 1
else
  echo "no image of this release's tree was published from $tag: nothing to verify"
fi

cat <<EOF

verify-release: $tag ($tip) is verified
  runs:   every workflow the tag started ($required_runs, and any other run on the tag) concluded successfully
  images: ${refs}${base_refs}
  patch:  ${patch_note:-this is not a patch tag}
EOF
