# shellcheck shell=bash
# Sourced by scripts/verify-release.sh and scripts/release.sh --hotfix: the rule
# a patch release's own commits are held to, in one place because both run it --
# the verifier after the tag is pushed, where a refusal cannot be undone, and
# --hotfix before it is, where it can. What the rule is and why it exists is in
# scripts/verify-release.sh's header (the branch is never merged back, so a
# commit of its own is a change main loses); this file is the check, so the two
# runs cannot disagree about what a patch release may carry.

# patch_of prints the git patch-id of one commit's diff: what "the same change"
# means below. -U0 is what makes it the change alone. The three lines of context
# a diff carries by default are part of the hashed content, and a pick onto the
# maintenance branch -- cut from the line's last tag, which is older than the
# commit being picked -- sits in different surrounding lines as soon as main
# moved on beside it. That is the ordinary case rather than the exception, and
# the pick is still the change the trailer names, so the context is left out and
# the changed lines are compared; a pick whose own lines differ is refused
# either way. `--stable` is spelled out because the other algorithm varies with
# the git version; awk keeps the id alone, dropping the commit column git
# patch-id prints beside it. A diff with nothing in it -- a merge, an empty
# commit -- prints nothing at all, which compares equal on both sides and is
# right: there is no change there that could have been lost.
patch_of() {
  git diff-tree --no-commit-id -p -U0 -r "$1" | git patch-id --stable | awk '{print $1}'
}

# hotfix_commits_check TAG TIP prints the number of cherry-picked commits the tag
# carries and origin/main does not, and returns 0, when every commit TIP carries
# that origin/main does not is one of exactly two things: a cherry-pick of a
# commit on origin/main, whose patch-id is the branch commit's, or a commit that
# touches the release's own text and nothing else. Otherwise it prints the
# refusal to stderr and returns 1. A TIP origin/main already carries has nothing
# of its own and prints 0.
hotfix_commits_check() {
  local tag=$1 tip=$2
  if [ "$(printf '%s\n%s\n' "$tip" "$(git rev-parse refs/remotes/origin/main)" | sort -u | wc -l)" = 1 ] \
    || git merge-base --is-ancestor "$tip" refs/remotes/origin/main; then
    echo 0
    return 0
  fi
  # A tree with not one trailer in it is refused before any of it is read: a
  # patch release carries the plugin bump main holds, so a branch whose own
  # commits are all hand-written is a branch nothing holds to main.
  local trailers
  trailers=$(git log --format=%B "$tip" --not refs/remotes/origin/main \
    | sed -n 's/.*(cherry picked from commit \([0-9a-f]\{7,40\}\)).*/\1/p' | sort -u)
  if [ -z "$trailers" ]; then
    echo "$tag is not on main and none of its own commits carries a \"(cherry picked from commit <sha>)\" trailer: a patch release cherry-picks its fixes onto release/X.Y with -x (git cherry-pick -x), and without one there is nothing to hold the release to main (docs/release.md, \"Hotfix releases\")" >&2
    return 1
  fi
  local n=0 commit picked sha path
  for commit in $(git log --format=%H "$tip" --not refs/remotes/origin/main); do
    picked=$(git log -1 --format=%B "$commit" \
      | sed -n 's/.*(cherry picked from commit \([0-9a-f]\{7,40\}\)).*/\1/p')
    if [ -n "$picked" ]; then
      for sha in $picked; do
        if ! git merge-base --is-ancestor "$sha" refs/remotes/origin/main 2>/dev/null; then
          echo "$tag carries a commit that is not on origin/main: the commit $sha, which some commit of this tag names in its \"(cherry picked from commit ...)\" trailer. A fix has to land on main first, through a PR as usual, so main never loses it (docs/release.md, \"Hotfix releases\")" >&2
          return 1
        fi
        if [ "$(patch_of "$sha")" != "$(patch_of "$commit")" ]; then
          echo "$tag carries a commit whose trailer names $sha, and the change under it is not the change $sha makes ($(git log -1 --format=%h "$commit") is not the pick it says it is, or the change was resolved by hand rather than applied). A hotfix's cherry-picks carry the fix as main has it: a trailer vouches for a main commit only when the lines it changes are that commit's -- the surrounding lines are the branch's own, which is why they are not compared (docs/release.md, \"Hotfix releases\")" >&2
          return 1
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
          return 1
          ;;
      esac
    done
  done
  echo "$n"
}
