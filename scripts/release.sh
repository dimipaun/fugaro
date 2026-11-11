#!/usr/bin/env bash
# Cuts a release: bumps the plugin through a PR, waits for it to merge, then
# tags the merged commit. See docs/release.md ("Cutting a release" section)
# for the full picture; this script automates every step of it, the tag
# push included: the release gate is the safeguard, there is no prompt.
#
#   scripts/release.sh X.Y.Z [--yes] [--dry-run] [--timeout-minutes N]
#   scripts/release.sh X.Y.Z --hotfix [--fix SHA]... [--dry-run] [--timeout-minutes N]
#
# --yes is accepted and does nothing (there is no tag prompt any more); every
# precondition below still runs. --dry-run applies the version bump to a
# scratch worktree and runs the plugin tests there (the bump rewrites two
# version stamps in every skill file, so it can push one over its byte budget
# on its own; `go` has to be on PATH for it, as it does for the stream read
# below, and its absence is a refusal like a missing `gh`), then stops after
# printing the plan for step 2 (branch/PR),
# creating nothing outside that worktree, which it removes however it ends.
# The plan says what that check did, including that it did not run when the
# checkout holds no module. --timeout-minutes (default 45, a whole number
# >= 1) bounds each of the two waits: step 3 (the release PR
# merging) and step 4 (CI's test, terraform, rules and both console checks finishing on the merge
# commit).
#
# The release reads the CLI stream from docs/release-streams.yaml through a
# small Go helper, internal/streams/cmd/streams (GD7: one file, the graph's
# reader and this one): step 1 runs step 2's write in memory (`set --check`) and
# refuses whatever it would refuse -- a version below the stream's current, one
# off the stream's named line, one with no name after it -- prints the release's
# spoken name, and step 2 records the version being released in the file in its
# release commit, so the file the graph draws never describes a version that has
# shipped (GV1.5, docs/console-dependency-graph).
#
# Re-running after an interruption is safe: the script re-derives its state
# from git and gh (an existing release/vX.Y.Z PR, merged or open, is found
# and continued) rather than keeping any state of its own. It never force-
# pushes, never pushes to main directly, and never deletes a tag. However it
# exits, it leaves the checkout on main.
#
# --hotfix is the other path: steps 2 to 5 of docs/release.md's "Hotfix
# releases", a patch release vX.Y.Z (Z of 1 or more) cut from the maintenance
# branch release/X.Y instead of from main -- the one shape the release gate
# accepts off main. Its preconditions are its own, and each is a refusal that
# names what is missing: Z of 1 or more; vX.Y.0 exists and is on origin/main;
# X.Y is the newest line (the gate's own rule); main's
# plugin/.claude-plugin/plugin.json already says X.Y.Z or newer, so the bump PR
# landed on main first; every --fix commit is on origin/main; the tag does not
# exist and the working tree is clean. Nothing is checked out in the user's
# checkout, which ends on the branch it started on: the branch is built in a
# scratch worktree (created there, removed however the run ends), where the
# --fix commits are cherry-picked with -x and, when the branch does not carry
# them yet, so is the commit on main that set the plugin version to X.Y.Z --
# release.yml checks the tag against that version, so a branch without the bump
# publishes nothing. It then requires the release's own text on the branch --
# docs/releases/vX.Y.Z.md, its line in website/data/release-summaries.yaml, and
# the CLI stream's current in docs/release-streams.yaml -- and prints exactly
# which of the three is missing instead of writing any of them (they are the
# branch's own commits, docs/release.md). With all three there: the branch is
# pushed (plain push, never force), CI is dispatched on it (`gh workflow run ci
# --ref release/X.Y`, which is what vouches for the commit the gate reads), the
# release gate waits for it (--timeout-minutes), and the tag goes on the branch
# tip and is pushed. The last step of both paths is scripts/verify-release.sh,
# which waits for every workflow the tag started.
#
# A dropped connection to GitHub does not end it: read-only gh and git calls,
# and the release gate's polling, are retried a bounded number of times
# (scripts/retry.sh); the auto-merge request and the tag push are repeated only
# after the state they would change has been read again, and the branch push
# and the pull request's creation not at all (a re-run resumes from them).
set -eu

repo_root=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
. scripts/retry.sh
. scripts/versions.sh

yes=0
dry_run=0
timeout_minutes=45
version=""
hotfix=0
# fixes is the `--fix` commits, space-separated by design: this runs under bash
# 3.2 on macOS, where "${#arr[@]}" on an empty array under set -u is an error.
fixes=""
while [ $# -gt 0 ]; do
  case "$1" in
    --yes) yes=1 ;;
    --dry-run) dry_run=1 ;;
    --hotfix) hotfix=1 ;;
    --fix)
      if [ $# -lt 2 ] || [ -z "${2:-}" ]; then
        echo "--fix needs a commit (a sha on origin/main): the fix the hotfix carries, cherry-picked onto the maintenance branch (docs/release.md, \"Hotfix releases\")" >&2
        exit 2
      fi
      shift
      fixes="$fixes $1"
      ;;
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
      if [ -n "$version" ]; then
        echo "unexpected argument: $1" >&2
        exit 2
      fi
      version=$1
      ;;
  esac
  shift
done
if [ -z "$version" ]; then
  echo "usage: $0 X.Y.Z [--yes] [--dry-run] [--timeout-minutes N]
       $0 X.Y.Z --hotfix [--fix SHA]... [--dry-run] [--timeout-minutes N]" >&2
  exit 2
fi
if ! [[ $timeout_minutes =~ ^[1-9][0-9]*$ ]]; then
  echo "--timeout-minutes wants a whole number of minutes, at least 1; got '$timeout_minutes'" >&2
  exit 2
fi
# --fix is --hotfix's alone: the ordinary release authors its one commit (the
# bump) itself, and takes no other (docs/release.md, "Cutting a release").
if [ "$hotfix" = 0 ] && [ -n "$fixes" ]; then
  echo "--fix names a commit --hotfix cherry-picks onto the maintenance branch, and this run has no --hotfix: the ordinary release commits only the plugin bump, which it makes itself" >&2
  exit 2
fi
# fix_flags renders $fixes as the arguments that named them, for the resume
# hint an interruption or a refusal prints.
fix_flags() {
  for f in $fixes; do printf -- '--fix %s ' "$f"; done
}

# restore_main runs on every exit: whatever the script did on the release
# branch, the checkout ends on main so a re-run starts from the state its
# own preconditions demand. Installed only once the "on main" precondition
# has passed, so it never moves a user who started elsewhere.
restore_main() {
  cur=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  if [ -n "$cur" ] && [ "$cur" != main ]; then
    git switch --quiet --discard-changes main >/dev/null 2>&1 ||
      echo "could not switch back to main; run: git switch main" >&2
  fi
}
# plugin_scratch is the scratch worktree plugin_budget_check has created, or
# empty; cleanup_plugin_scratch removes it and is called from the EXIT trap
# below, so the worktree is gone however the run ends -- an error, or a Ctrl-C
# or SIGTERM, which reaches on_signal's exit 130 and so the same trap.
plugin_scratch=""
cleanup_plugin_scratch() {
  [ -n "$plugin_scratch" ] || return 0
  git worktree remove --force "$plugin_scratch/tree" >/dev/null 2>&1 || true
  rm -rf "$plugin_scratch"
  # A worktree whose removal failed half-way (a process still in it, a signal
  # landing mid-delete) leaves a registration behind; prune drops the entries
  # whose directory is gone, so the checkout does not collect them.
  git worktree prune >/dev/null 2>&1 || true
  plugin_scratch=""
}
# rerun is this run's own command line, as far as resuming needs it: the
# refusal messages and the signal handler print it rather than the plain one,
# so the user re-runs with the flags the run had.
if [ "$hotfix" = 1 ]; then
  rerun="scripts/release.sh $version --hotfix"
  if [ -n "$fixes" ]; then
    rerun="$rerun $(fix_flags)"
  fi
else
  rerun="scripts/release.sh $version"
fi
on_signal() {
  echo "
interrupted; nothing was torn down. Re-run \"$rerun\" to resume: it finds an existing release branch or pull request and continues from there, and refuses outright if the tag already exists on origin." >&2
  exit 130
}

# version_gt exits 0 (true) when strict-semver $1 is greater than $2.
version_gt() {
  awk -v a="$1" -v b="$2" '
    BEGIN {
      split(a, A, ".")
      split(b, B, ".")
      for (i = 1; i <= 3; i++) {
        if (A[i] + 0 > B[i] + 0) exit 0
        if (A[i] + 0 < B[i] + 0) exit 1
      }
      exit 1
    }'
}

# latest_released_version prints the highest existing strict vX.Y.Z tag's
# version (other than the one being released; -rc and four-part tags do not
# count), or nothing when there is none. The ordering is scripts/versions.sh's
# (a zero-padded key, so a plain `sort` is enough).
latest_released_version() {
  git tag -l 'v*' | sed -n 's/^v//p' | grep -vxF "$version" | highest_version
}

# released_version_docs check|write X.Y.Z handles the two doc lines that name
# the released version: internal/cli's docs tests
# (TestDocsProjectNamedOAuthTokensStatusNamesTheReleasedCLI,
# TestDocsProjectClaudeAccountsOperatorGuide) pin each to the version
# plugin/.claude-plugin/plugin.json carries, so they can change in no commit but
# the release commit, and a release commit without them goes red on main (0.9.5's
# first bump, 280b2270). Each entry is the file, then the line's fixed text
# around the version, as a prefix and a suffix. `check` refuses a file that does
# not hold exactly one such line -- a reworded line must be noticed, not left
# behind -- and `write` rewrites it to X.Y.Z, byte for byte the same when it
# already says X.Y.Z.
released_version_docs_list=(
  "docs/design/project-named-oauth-tokens.md" "(released; the released CLI is " ")"
  "docs/project-claude-accounts.md" "The released Fugaro is " ","
)
released_version_docs() {
  local mode=$1 want=$2 i file prefix suffix n tmp
  for ((i = 0; i < ${#released_version_docs_list[@]}; i += 3)); do
    file=${released_version_docs_list[i]}
    prefix=${released_version_docs_list[i + 1]}
    suffix=${released_version_docs_list[i + 2]}
    n="no file"
    [ ! -f "$file" ] || n=$(awk -v p="$prefix" -v s="$suffix" '
      { i = index($0, p); if (i && substr($0, i + length(p)) ~ /^[0-9]+\.[0-9]+\.[0-9]+/) { rest = substr($0, i + length(p)); sub(/^[0-9]+\.[0-9]+\.[0-9]+/, "", rest); if (index(rest, s) == 1) n++ } }
      END { print n + 0 }' "$file")
    if [ "$n" != 1 ]; then
      echo "$file must hold exactly one line naming the released version as \"${prefix}X.Y.Z${suffix}\" (the release commit rewrites it to $want, and a docs test in internal/cli pins it to the tree's version); found $n. Restore the sentence, or change it here (released_version_docs_list) and in its docs test together." >&2
      return 1
    fi
    [ "$mode" = write ] || continue
    tmp=$(mktemp)
    awk -v p="$prefix" -v s="$suffix" -v v="$want" '
      { i = index($0, p); if (i) { rest = substr($0, i + length(p)); if (match(rest, /^[0-9]+\.[0-9]+\.[0-9]+/) && index(substr(rest, RLENGTH + 1), s) == 1) $0 = substr($0, 1, i - 1) p v substr(rest, RLENGTH + 1) } print }' "$file" >"$tmp"
    cat "$tmp" >"$file"
    rm -f "$tmp"
  done
}

poll_seconds=${RELEASE_SH_POLL_SECONDS:-30}
# RELEASE_SH_TIMEOUT_SECONDS exists for the tests only (--timeout-minutes has
# a one-minute floor).
timeout_seconds=${RELEASE_SH_TIMEOUT_SECONDS:-$((timeout_minutes * 60))}

# resolve_repo checks that gh is there and authenticated and that it targets the
# repository origin points at, and sets $repo to it: every gh call after this
# one, and the tag workflows' GITHUB_REPOSITORY, has to mean the same repository
# this checkout pushes to.
resolve_repo() {
  if ! command -v gh >/dev/null 2>&1; then
    echo "gh is not installed; see https://cli.github.com" >&2
    return 1
  fi
  if ! retry_read --any gh auth status >/dev/null 2>&1; then
    echo "gh is not authenticated; run 'gh auth login'" >&2
    return 1
  fi
  repo=$(retry_read gh repo view --json nameWithOwner --jq .nameWithOwner) || {
    echo "'gh repo view' failed; run this from inside the fugaro checkout" >&2
    return 1
  }
  # gh resolves its repo from the checkout's remotes (or a "default" setting);
  # the tag and the PR must land where origin points, so the two must agree.
  origin_url=$(git remote get-url origin)
  url_path=${origin_url%/}
  url_path=${url_path%.git}
  url_name=${url_path##*[/:]}
  url_rest=${url_path%[/:]*}
  url_repo="${url_rest##*[/:]}/$url_name"
  if [ "$(printf '%s' "$url_repo" | tr '[:upper:]' '[:lower:]')" != "$(printf '%s' "$repo" | tr '[:upper:]' '[:lower:]')" ]; then
    echo "origin ($origin_url) is $url_repo but gh targets $repo; fix it with 'gh repo set-default' or run from the right checkout" >&2
    return 1
  fi
}

# streams_cmd is how this script reads docs/release-streams.yaml, the CLI
# stream's own record of where the line is at: the package's own reader,
# internal/streams/cmd/streams, rather than a YAML reader of its own, so the
# file is refused here exactly as the console's dependency graph refuses it (the
# long paragraph further down has the rest). The hotfix path needs it before the
# ordinary preconditions below run -- it reads the branch's catalogue from its
# scratch worktree -- so the definition is here. RELEASE_SH_STREAMS_CMD exists
# for the tests only: the fixture checkout holds no go module, so they
# substitute a helper binary built from this checkout (the same reason
# RELEASE_SH_PLUGIN_TEST_CMD exists).
streams_cmd=${RELEASE_SH_STREAMS_CMD:-go run ./internal/streams/cmd/streams}

# plugin_version_at REF prints the plugin version in the tree at REF, or nothing
# when REF holds no plugin/.claude-plugin/plugin.json. It is the same read
# scripts/release-gate.sh makes of main, which is what a patch tag is held to.
plugin_version_at() {
  git show "$1:plugin/.claude-plugin/plugin.json" 2>/dev/null \
    | sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
}

# hotfix_scratch is the scratch worktree --hotfix builds the maintenance branch
# in; cleanup_hotfix_scratch removes it and is called from the EXIT trap, so it
# is gone however the run ends -- an error, a Ctrl-C or SIGTERM included
# (on_signal exits 130 and the same trap runs).
hotfix_scratch=""
cleanup_hotfix_scratch() {
  [ -n "$hotfix_scratch" ] || return 0
  git worktree remove --force "$hotfix_scratch/tree" >/dev/null 2>&1 || true
  rm -rf "$hotfix_scratch"
  git worktree prune >/dev/null 2>&1 || true
  hotfix_scratch=""
}

# hotfix_plugin_version prints the plugin version the scratch worktree's HEAD
# carries: the version release.yml checks the tag against
# (scripts/bump-plugin-version.sh --check "${TAG#v}"), which the tag's own
# required `test` check and that workflow both refuse a mismatch on.
hotfix_plugin_version() {
  ( cd "$hotfix_scratch/tree" && plugin_version_at HEAD )
}

# hotfix_picked SHA succeeds when a commit of the scratch worktree's history
# names SHA in a "(cherry picked from commit ...)" trailer, which is what
# `git cherry-pick -x` writes: that pick is already made, so a re-run does not
# make it twice. verify-release.sh reads the same trailers back.
hotfix_picked() {
  git -C "$hotfix_scratch/tree" log --format=%B | grep -qF "(cherry picked from commit $1)"
}

# bump_commit_on_main prints the commit on origin/main that set the plugin
# version to $1, or nothing when main has none. It is the bump PR's commit --
# the newest one touching plugin.json whose tree says $1 and whose parent's does
# not -- and cherry-picking it carries the whole bump: the skill headers and,
# because the bump commit is the only one allowed to move them, the two doc
# lines that name the released version (released_version_docs_list above,
# docs/release.md "Hotfix releases").
bump_commit_on_main() {
  while read -r sha; do
    [ "$(plugin_version_at "$sha")" = "$1" ] || continue
    if ! parent=$(git rev-parse --quiet --verify "$sha^"); then
      printf '%s' "$sha"
      return 0
    fi
    if [ "$(plugin_version_at "$parent")" = "$1" ]; then
      continue
    fi
    printf '%s' "$sha"
    return 0
  done <<EOF
$(git rev-list refs/remotes/origin/main -- plugin/.claude-plugin/plugin.json)
EOF
}

# hotfix_pick SHA cherry-picks SHA into the scratch worktree with -x, or
# returns 1 having printed git's own output and aborted the pick, leaving the
# worktree at the tip it had.
hotfix_pick() {
  if pick_err=$(git -C "$hotfix_scratch/tree" cherry-pick -x "$1" 2>&1); then
    return 0
  fi
  printf '%s\n' "$pick_err" >&2
  git -C "$hotfix_scratch/tree" cherry-pick --abort >/dev/null 2>&1 || true
  return 1
}

# hotfix_missing_text prints one line per piece of the release's own text that
# the scratch worktree's HEAD does not carry -- the Highlights file GoReleaser
# reads from the tag's tree, the summary line the changelog page prints, and
# the CLI stream's current, the floor the next release is read against -- and
# nothing when the branch holds all three. Empty is not the same as fine for the
# first two only: an empty Highlights file is what a release publishes with no
# Highlights, and the workflows refuse it (scripts/release-policy.sh).
hotfix_missing_text() {
  notes="docs/releases/$tag.md"
  if ! git -C "$hotfix_scratch/tree" cat-file -e "HEAD:$notes" 2>/dev/null; then
    echo "  $notes (the release's Highlights; docs/releases/README.md has the format)"
  elif [ -z "$(git -C "$hotfix_scratch/tree" show "HEAD:$notes" | tr -d '[:space:]')" ]; then
    echo "  $notes is empty (GoReleaser reads the Highlights from the tag's tree, so a release with an empty file publishes none)"
  fi
  summaries=website/data/release-summaries.yaml
  summary_line=$(git -C "$hotfix_scratch/tree" show "HEAD:$summaries" 2>/dev/null | grep -F "  $tag: " | head -1 || true)
  summary_text=${summary_line#"  $tag: "}
  if [ -z "${summary_text// /}" ]; then
    echo "  $summaries carries no non-empty \"  $tag: ...\" line (the changelog page prints one per release, and the site build stops when a release file has none)"
  fi
  stream_get=$(cd "$hotfix_scratch/tree" && $streams_cmd get cli 2>&1) || stream_get=""
  branch_current=$(printf '%s\n' "$stream_get" | sed -n 's/^current=//p')
  if [ "$branch_current" != "$version" ]; then
    echo "  docs/release-streams.yaml's cli current is '${branch_current:-unreadable}', not $version (the file is the stream's own record of where the line is at, so the next release reads its floor from it)"
  fi
}

# run_hotfix is --hotfix: steps 2 to 5 of docs/release.md's "Hotfix releases".
# It never checks anything out in the user's tree -- the branch is built in a
# scratch worktree, whose commits are pushed through an explicit refspec -- so
# the checkout is left on the branch it started on.
run_hotfix() {
  if [ "${version##*.}" = 0 ]; then
    echo "X.Y.0 is a minor's first release and is cut from main: run \"scripts/release.sh $version\". --hotfix is for a patch release vX.Y.Z with Z of 1 or more, the one shape the release gate accepts off main (docs/release.md, \"Hotfix releases\")" >&2
    return 1
  fi
  resolve_repo || return 1
  if [ -z "${RELEASE_SH_STREAMS_CMD:-}" ] && ! command -v go >/dev/null 2>&1; then
    echo "go is not on PATH, and a hotfix reads the branch's docs/release-streams.yaml through internal/streams/cmd/streams with it (the cli stream's current is one of the three things the branch has to carry): install go (https://go.dev/dl)" >&2
    return 1
  fi
  line=${version%.*}
  first="v$line.0"
  branch="release/$line"
  retry_read git fetch --quiet origin main --tags --prune

  # vX.Y.0 exists and is on main: the line was gated on main once, so the patch
  # is not the first release of anything. Both are the gate's own rules, read
  # the same way, so this refuses before a branch exists rather than after the
  # tag's workflows do.
  if ! first_sha=$(git rev-parse --quiet --verify "refs/tags/$first^{commit}"); then
    echo "$first does not exist: a hotfix is a patch of a line that shipped from main, so release v$line.0 from main first (docs/release.md, \"Hotfix releases\")" >&2
    return 1
  fi
  if ! git merge-base --is-ancestor "$first_sha" refs/remotes/origin/main; then
    echo "$first is not on origin/main: a hotfix is a patch of a line that shipped from main, and the gate refuses a patch tag whose line never went through it (docs/release.md, \"Hotfix releases\")" >&2
    return 1
  fi
  # Only the newest line may be patched from a maintenance branch: a patch of an
  # older line is published as GitHub's latest release, and the Homebrew cask
  # follows the tag.
  newest=$(git tag -l 'v*' | sed -n 's/^v//p' | highest_version)
  if [ -z "$newest" ]; then
    echo "no strict vX.Y.Z tag could be read from this checkout, so whether v$line is the newest line cannot be known: fetch the tags (git fetch --tags) and re-run \"$rerun\"" >&2
    return 1
  fi
  if [ "${newest%.*}" != "$line" ]; then
    echo "$tag is a patch of the line v$line, but a newer line exists (v$newest): a maintenance release would be published as the latest release, so only the newest line may be patched from its maintenance branch (docs/release.md, \"Hotfix releases\")" >&2
    return 1
  fi
  # main's plugin version is the tag's or newer, so the bump PR landed on main
  # first: the branch is never merged back, and a plugin version only it carries
  # leaves every installation serving a plugin older than its binary while the
  # gate refuses the tag (0.21.1, 2026-11-09).
  main_plugin=$(plugin_version_at refs/remotes/origin/main)
  if [[ ! $main_plugin =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
    || [ "$(printf '%s\n%s\n' "$main_plugin" "$version" | highest_version)" != "$main_plugin" ]; then
    echo "main's plugin/.claude-plugin/plugin.json says '${main_plugin:-nothing}' and $tag is $version: a hotfix's plugin bump is merged to main first, through a PR as usual, and cherry-picked onto $branch with the fix (docs/release.md, \"Hotfix releases\")" >&2
    return 1
  fi
  # Every --fix commit is on origin/main: a fix lands there first, and the
  # branch carries a copy of it, never the only copy.
  fix_shas=""
  for fix in $fixes; do
    if ! fix_sha=$(git rev-parse --quiet --verify "$fix^{commit}"); then
      echo "--fix $fix is not a commit in this checkout; fetch it (git fetch origin), or name a commit that is on origin/main" >&2
      return 1
    fi
    if ! git merge-base --is-ancestor "$fix_sha" refs/remotes/origin/main; then
      echo "--fix $fix is not on origin/main: a fix is merged to main first, through a PR as usual, and the hotfix cherry-picks it onto $branch (docs/release.md, \"Hotfix releases\")" >&2
      return 1
    fi
    fix_shas="$fix_shas $fix_sha"
  done
  if [ -n "$(retry_read git ls-remote --tags origin "refs/tags/$tag")" ]; then
    echo "tag $tag already exists on origin; a release is never re-tagged (ship the fix as the next patch instead, see docs/release.md#rolling-back)" >&2
    return 1
  fi
  if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then
    echo "tag $tag exists locally; delete it (git tag -d $tag) if it is a leftover, or push it (git push origin refs/tags/$tag) if the release is done, then re-run \"$rerun\"" >&2
    return 1
  fi
  line_latest=$(git tag -l "v$line.*" | sed -n 's/^v//p' | highest_version)
  if [ -z "$line_latest" ]; then
    echo "no strict v$line.* tag could be read from this checkout, so the line's latest release cannot be known: fetch the tags (git fetch --tags) and re-run \"$rerun\"" >&2
    return 1
  fi
  if ! version_gt "$version" "$line_latest"; then
    echo "version $version is not greater than the line's latest tag v$line_latest" >&2
    return 1
  fi

  # The branch is created from the line's last tag (the hand flow's step 2) or,
  # when origin already has release/X.Y, reused -- but only when it descends
  # from vX.Y.0, which is the ancestry the gate reads, so a branch that merely
  # shares the name is refused rather than tagged.
  base_ref="v$line_latest"
  base_desc="v$line_latest"
  if retry_read git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
    retry_read git fetch --quiet origin "refs/heads/$branch:refs/remotes/origin/$branch"
    if ! git merge-base --is-ancestor "$first_sha" "refs/remotes/origin/$branch"; then
      echo "origin's $branch does not descend from $first, so it is not this line's maintenance branch (the gate refuses a tag on it); inspect it, or delete it (git push origin --delete $branch) and re-run \"$rerun\"" >&2
      return 1
    fi
    base_ref="refs/remotes/origin/$branch"
    base_desc="the branch origin already has"
  fi
  hotfix_scratch=$(mktemp -d)
  if ! worktree_err=$(git worktree add --quiet --detach "$hotfix_scratch/tree" "$base_ref" 2>&1); then
    printf '%s\n' "$worktree_err" >&2
    cleanup_hotfix_scratch
    echo "could not create a scratch worktree at $base_ref (the branch is built there, never in this checkout); fix what git reported and re-run \"$rerun\"" >&2
    return 1
  fi

  # The fixes, then the bump: `-x` records where each one came from, which is
  # what verify-release.sh reads back to require every fix to be on main, and a
  # pick already named by a trailer is skipped, so a re-run resumes rather than
  # picking twice.
  picked=""
  for fix_sha in $fix_shas; do
    if hotfix_picked "$fix_sha"; then
      picked="$picked ${fix_sha:0:12}(already there)"
      continue
    fi
    if ! hotfix_pick "$fix_sha"; then
      echo "cherry-picking $fix_sha onto $branch failed (above). Nothing was pushed and nothing in this checkout changed. Resolving the conflict here is not the recovery: the commit would either keep the pick's trailer while carrying a change main does not have, or carry no trailer at all, and scripts/verify-release.sh refuses both -- a trailer is held to the change under it by patch-id, and a commit with no trailer may touch only the release's own three files -- once the tag is pushed, where it cannot be moved and the release is stranded. The change has to be on main in the shape this line needs it, so that the pick applies cleanly to $branch as it stands: land a commit on main that carries the change as this line needs it (a PR as usual) and name that commit, or name a --fix commit that applies, then re-run \"$rerun\" (docs/release.md, \"Hotfix releases\")" >&2
      return 1
    fi
    picked="$picked $fix_sha"
  done
  branch_plugin=$(hotfix_plugin_version)
  if [ "$branch_plugin" != "$version" ]; then
    bump_commit=$(bump_commit_on_main "$version")
    if [ -z "$bump_commit" ]; then
      echo "the plugin version on $branch is '${branch_plugin:-nothing}' and no commit on origin/main sets it to $version: bump it on main first (scripts/bump-plugin-version.sh $version, through a PR), then re-run \"$rerun\"" >&2
      return 1
    fi
    if ! hotfix_pick "$bump_commit"; then
      echo "cherry-picking main's plugin bump $bump_commit onto $branch failed (above). Nothing was pushed and nothing in this checkout changed. Resolving it here is not the recovery either: the commit would keep the pick's trailer while carrying a plugin change main does not have, and scripts/verify-release.sh refuses every commit of the tag that is not a clean pick of a commit on main -- once the tag is pushed, where it cannot be moved and the release is stranded. The bump has to be on main in the shape this line needs it, so that the pick applies cleanly to $branch as it stands. Check what the branch carries that the bump's parent does not: a hand edit outside the release's own three files is a commit scripts/verify-release.sh refuses on the tag anyway, and if main's bump cannot apply to this line at all, the line cannot take it as a pick and the fix has to ship from main instead. Then re-run \"$rerun\" (docs/release.md, \"Hotfix releases\")" >&2
      return 1
    fi
    picked="$picked $bump_commit(bump)"
  fi
  # The check release.yml runs on the tag before it builds anything: a branch
  # whose plugin.json or skill header disagrees with $version publishes nothing,
  # so it is refused here, while the branch is still a branch.
  if [ -f "$hotfix_scratch/tree/scripts/bump-plugin-version.sh" ] \
    && ! bump_check=$(cd "$hotfix_scratch/tree" && scripts/bump-plugin-version.sh --check "$version" 2>&1); then
    printf '%s\n' "$bump_check" >&2
    echo "$branch does not carry the $version plugin bump (above), and release.yml refuses a tag on such a commit (scripts/bump-plugin-version.sh --check), so it is refused here" >&2
    return 1
  fi

  missing_text=$(hotfix_missing_text)
  if [ -n "$missing_text" ]; then
    text_state="these are missing:
$missing_text"
    text_stop="A real run would stop at step 3 until the text above is committed on $branch."
  else
    text_state="all three are there (docs/releases/$tag.md, its line in website/data/release-summaries.yaml, and the cli stream's current)"
    text_stop=""
  fi
  tip=$(git -C "$hotfix_scratch/tree" rev-parse HEAD)
  if [ "$dry_run" = 1 ]; then
    cat <<EOF

Dry run for $tag ($branch): every precondition above passed, and the branch was
built and committed in a scratch worktree, now removed. Nothing was pushed,
tagged or changed anywhere.
  2. the branch would start at $base_desc, cherry-picked with -x:$picked
     tip: $tip
  3. the release's own text would be required on it, and this script writes
     none of it: $text_state
  4. the branch would be pushed (a plain push, never a force) and CI dispatched
     on it: gh workflow run ci --ref $branch
  5. the test, terraform, rules and both console checks would be waited for on
     the branch tip (up to ${timeout_minutes}m, scripts/release-gate.sh), then
     $tag would be tagged there and pushed
  6. every workflow the tag starts would be waited for, and the published
     images and the patch checked (up to ${timeout_minutes}m,
     scripts/verify-release.sh $tag)
$text_stop
EOF
    return 0
  fi

  # Step 3's push comes before the text check, so a first run leaves the branch
  # on origin for the text to be committed to (the hand flow commits it there);
  # the refusal names what is missing and the run resumes from the branch.
  if ! push_err=$(git -C "$hotfix_scratch/tree" push --quiet origin "HEAD:refs/heads/$branch" 2>&1); then
    printf '%s\n' "$push_err" >&2
    echo "pushing $branch failed; fix the cause (permissions, a branch rule) and re-run \"$rerun\": the branch is in a scratch worktree that is removed on exit, so the run starts over from $base_desc" >&2
    return 1
  fi
  echo "pushed $branch ($tip), cherry-picked with -x:$picked"
  if [ -n "$missing_text" ]; then
    echo "$branch is pushed, but the release's own text is not on it. These are committed on the branch by hand (docs/release.md, \"Hotfix releases\"), and this script writes none of them:
$missing_text

Commit them on $branch (git fetch origin $branch && git switch -c $branch origin/$branch), push, then re-run \"$rerun\": it resumes from the pushed branch and picks up from here. The tag is not created until they are there, because the tag's own workflows refuse without them." >&2
    return 1
  fi
  if ! dispatch_err=$(gh workflow run ci --ref "$branch" 2>&1); then
    printf '%s\n' "$dispatch_err" >&2
    echo "could not dispatch CI on $branch (\"gh workflow run ci --ref $branch\"). The branch is pushed and carries the release's text; dispatch it by hand, then re-run \"$rerun\"" >&2
    return 1
  fi
  echo "dispatched CI on $branch; waiting up to ${timeout_minutes}m for the required checks..."
  GITHUB_SHA="$tip" GITHUB_REPOSITORY="$repo" GATE_WAIT_SECONDS="$timeout_seconds" GATE_POLL_SECONDS="$poll_seconds" scripts/release-gate.sh "$tag"

  git tag -a "$tag" -m "Fugaro $tag" "$tip"
  if ! tag_err=$(git push origin "refs/tags/$tag" 2>&1); then
    printf '%s\n' "$tag_err" >&2
    echo "pushing tag $tag failed. It exists locally at $tip; once the cause is fixed, push it with:
  git push origin refs/tags/$tag" >&2
    return 1
  fi
  cat <<EOF

tag $tag pushed on $branch ($tip). Watch:
  https://github.com/$repo/actions/workflows/release.yml
  https://github.com/$repo/actions/workflows/images.yml
  https://github.com/$repo/actions/workflows/console-windows.yml
EOF
  scripts/verify-release.sh "$tag" --timeout-minutes "$timeout_minutes" || return 1
  return 0
}

# 1. Preconditions, checked in order; the first failure stops the script.
if ! [[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "version '$version' is not strict SemVer X.Y.Z (no leading v, no pre-release suffix, no leading zeros in a component)" >&2
  exit 1
fi
tag="v$version"
branch="release/$tag"

if [ -n "$(git status --porcelain)" ]; then
  echo "the working tree is not clean; commit or stash first" >&2
  exit 1
fi

# Both paths end with scripts/verify-release.sh, which refuses without docker
# when the tagged tree holds an images/*/Dockerfile -- every release does -- and
# it runs after the tag is pushed, where a refusal is not recoverable. Checked
# here instead, with the other preconditions, so a machine without docker never
# gets as far as the branch, the pull request or the tag.
if ! command -v docker >/dev/null 2>&1; then
  echo "docker is not on PATH, and the release's last step, scripts/verify-release.sh, pulls the images the release published (images/verify-public.sh) to prove they can be read without a login: install docker, then re-run \"$rerun\"" >&2
  exit 1
fi

# --hotfix is the other path from here: it owns every step from the branch on,
# and it never moves the user's checkout (restore_main is not installed for it:
# the branch is built in a scratch worktree), so it ends on whatever branch it
# started on. A refusal inside run_hotfix returns 1, and set -e ends the script
# with it; the trap removes the worktree either way.
if [ "$hotfix" = 1 ]; then
  trap cleanup_hotfix_scratch EXIT
  trap on_signal INT TERM HUP
  run_hotfix
  exit 0
fi

current_branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$current_branch" != main ]; then
  echo "the current branch is '$current_branch', not main; switch to main first" >&2
  exit 1
fi
trap 'cleanup_plugin_scratch; restore_main' EXIT
trap on_signal INT TERM HUP

retry_read git fetch --quiet origin main --tags --prune
local_main=$(git rev-parse main)
origin_main=$(git rev-parse origin/main)
if [ "$local_main" != "$origin_main" ]; then
  echo "main ($local_main) is not origin/main ($origin_main); pull or push first" >&2
  exit 1
fi

# From 0.4.0 on a release needs its hand-written Highlights, committed on main
# before the release PR: GoReleaser reads docs/releases/vX.Y.Z.md from the
# tag's tree (docs/releases/README.md). Read from origin/main, which the
# checks above tie to the working tree.
if scripts/release-policy.sh highlights-required "$version"; then
  highlights="docs/releases/$tag.md"
  if ! git cat-file -e "origin/main:$highlights" 2>/dev/null; then
    echo "$highlights does not exist on main, and releases from 0.4.0 need it (the Highlights section of the release notes). Write it (see docs/releases/README.md), merge it to main through a PR, then re-run scripts/release.sh $version" >&2
    exit 1
  fi
  if [ -z "$(git show "origin/main:$highlights" | tr -d '[:space:]')" ]; then
    echo "$highlights on main is empty; write the Highlights (see docs/releases/README.md), merge it, then re-run scripts/release.sh $version" >&2
    exit 1
  fi

  # And the release needs its end-user summary, the one line the changelog page
  # prints above the Highlights. The site build reads one per release file and
  # stops, naming the file, when one is missing (website/README.md,
  # website/src/content.ts), so the entry ships in the same PR as the Highlights
  # (docs/release.md). Read from origin/main for the same reason.
  summaries="website/data/release-summaries.yaml"
  summary_line=$(git show "origin/main:$summaries" 2>/dev/null | grep -F "  $tag: " | head -1 || true)
  summary_text=${summary_line#"  $tag: "}
  if [ -z "${summary_text// /}" ]; then
    echo "$summaries on main carries no entry for $tag, and the changelog page prints one per release (the site build stops when a release file has none). Write the line (see the file's header), in the same PR as $highlights, then re-run scripts/release.sh $version" >&2
    exit 1
  fi
fi

resolve_repo
if [ -n "$(retry_read git ls-remote --tags origin "refs/tags/$tag")" ]; then
  echo "tag $tag already exists on origin; a release is never re-tagged (ship the fix as the next patch instead, see docs/release.md#rolling-back)" >&2
  exit 1
fi
# A tag that exists only locally (an earlier run created it and its push
# failed) is allowed: if the release PR is merged and the tag sits on the
# merge commit, step 6 pushes it. Anything else is refused there.
local_tag=0
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  local_tag=1
fi

latest=$(latest_released_version)
if [ -n "$latest" ] && ! version_gt "$version" "$latest"; then
  echo "version $version is not greater than the latest tag v$latest" >&2
  exit 1
fi

# The CLI stream's own record of where the line is at, docs/release-streams.yaml
# (GD7: one file, read by the console's dependency graph and by this script):
# the release takes the version it must not go behind, the name it is spoken
# with, and the version it writes back into the file from it. It is read through
# internal/streams/cmd/streams, which is the package's reader, so this script
# carries no YAML reader of its own and the file is refused here exactly as the
# graph refuses it -- a symlink, an anchor, a tag prefix that is not this
# stream's, a console version below the floor the console has shipped. It runs
# after the clean-tree and main == origin/main checks, so `go run` builds the
# helper from the checkout those checks verified.
#
# `set --check` is step 2's write computed in memory: the floor (a version below
# the stream's current is refused; the version it is already at is allowed, since
# an interrupted release re-runs with it and the tag check above refuses one that
# really did ship), the next name (a version with no name after it is refused),
# the line-targeted rewrite (a `current:` it cannot find is refused) and the
# reader's re-read of the result (a version off the stream's named line -- a
# major bump, a minor below 0.10 -- is refused). So the dry run refuses whatever
# step 2 would, before a branch exists.
#
# RELEASE_SH_STREAMS_CMD exists for the tests only: the fixture checkout holds no
# go module, so they substitute a helper binary built from this checkout (the
# same reason RELEASE_SH_PLUGIN_TEST_CMD exists). streams_cmd itself is set
# above, before the hotfix path, which reads the branch's catalogue through it
# too (and from the branch's own tree, in its scratch worktree).
if [ -z "${RELEASE_SH_STREAMS_CMD:-}" ] && ! command -v go >/dev/null 2>&1; then
  echo "go is not on PATH, and the release reads the CLI stream (docs/release-streams.yaml) through internal/streams/cmd/streams with it, for the version it must not go behind and the name it is spoken with (--dry-run's plugin budget check needs go too). Install go (https://go.dev/dl)." >&2
  exit 1
fi
if ! stream_check=$($streams_cmd set --check cli "$version" 2>&1); then
  echo "docs/release-streams.yaml cannot record $version as the cli stream's current, which step 2 writes in the release commit (docs/console-dependency-graph/reconciliation.md, GD7):
$stream_check" >&2
  exit 1
fi
# The name the release is spoken with: the name of the minor it falls in and the
# patch's word ("Camel two" for 0.11.2), as docs/release-streams.yaml's header
# writes it. A version the check above accepted always has one.
if ! stream_name=$($streams_cmd name cli "$version" 2>&1); then
  echo "docs/release-streams.yaml names no release for $version:
$stream_name" >&2
  exit 1
fi

# The two doc lines the release commit rewrites must still read the way the
# rewrite expects, so a reworded one is refused here, before a branch exists.
released_version_docs check "$version" || exit 1

# Checks still running on main (a just-merged PR) are waited for, up to
# --timeout-minutes; a failed check fails at once.
echo "checking that main's checks are green at $local_main (waiting up to ${timeout_minutes}m for any still running)..."
GITHUB_SHA="$local_main" GITHUB_REPOSITORY="$repo" GATE_WAIT_SECONDS="$timeout_seconds" GATE_POLL_SECONDS="$poll_seconds" scripts/release-gate.sh "$tag"

# plugin_budget_check applies the version bump to a scratch worktree of the
# tree being released and runs the plugin tests there. The bump rewrites two
# version stamps in every skill file, so a version one of whose components
# gains a digit grows each file by a byte or two: 0.9.5 -> 0.10.0 added two
# bytes and pushed plugin/skills/setup/SKILL.md over its 24576-byte budget
# (plugin/skills_lint_test.go's maxSkillBytes), which failed
# TestPluginSkillsLintClean on the bump commit itself (#929). Only applying
# the bump can see that -- the version strings all agree, so
# scripts/bump-plugin-version.sh --check passes on the same tree.
#
# It reports what it did in plugin_check_line, which the dry-run plan prints
# instead of a fixed sentence: a check that did not run must not read as a
# check that passed, and the two ways it can fail to run (a checkout with no
# module, a worktree that could not be created) are not the release's fault.
# A missing `go` is not one of them: it is a refusal, like a missing `gh`,
# because the check the dry run advertises cannot run at all without it.
#
# RELEASE_SH_PLUGIN_TEST_CMD exists for the tests only: they substitute a stub
# for the go command (the test repository holds no module).
plugin_check_line=""
plugin_budget_check() {
  want=$1
  test_cmd=${RELEASE_SH_PLUGIN_TEST_CMD:-go test ./plugin/ -count=1}
  if [ -z "${RELEASE_SH_PLUGIN_TEST_CMD:-}" ]; then
    if [ ! -f go.mod ]; then
      plugin_check_line="the plugin budget check did not run (this checkout holds no go.mod), so nothing has verified that the $want bump leaves every skill file inside its byte budget"
      echo "note: no go.mod here, so the plugin budget check ($test_cmd on a bumped tree) is skipped" >&2
      return 0
    fi
    if ! command -v go >/dev/null 2>&1; then
      echo "go is not on PATH, and the plugin budget check ($test_cmd on a bumped tree) is the check --dry-run exists for: it is what catches a bump that pushes a skill file over its byte budget (plugin/skills_lint_test.go's maxSkillBytes; 0.10.0's plugin/skills/setup/SKILL.md, #929). Install go (https://go.dev/dl), or run the check by hand on a bumped tree (docs/release.md, \"Before you tag\")." >&2
      return 1
    fi
  fi
  plugin_scratch=$(mktemp -d)
  if ! git worktree add --quiet --detach "$plugin_scratch/tree" HEAD >/dev/null 2>&1; then
    rm -rf "$plugin_scratch"
    plugin_scratch=""
    plugin_check_line="the plugin budget check did not run (no scratch worktree could be created), so nothing has verified that the $want bump leaves every skill file inside its byte budget"
    echo "note: could not create a scratch worktree, so the plugin budget check ($test_cmd on a bumped tree) is skipped" >&2
    return 0
  fi
  rc=0
  ( cd "$plugin_scratch/tree" && scripts/bump-plugin-version.sh "$want" >/dev/null && sh -c "$test_cmd" ) || rc=$?
  cleanup_plugin_scratch
  if [ "$rc" != 0 ]; then
    plugin_check_line="the plugin tests fail on a tree with the $want bump applied"
    echo "the plugin tests fail on a tree with the $want bump applied (above), so the bump commit would go red on main. The usual cause is a skill file the bump pushed over its byte budget (plugin/skills_lint_test.go's maxSkillBytes; 0.10.0's plugin/skills/setup/SKILL.md, #929): shorten the file, then tag." >&2
    return 1
  fi
  plugin_check_line="the plugin tests pass on a tree with the $want bump applied (a scratch worktree, since removed)"
  echo "$plugin_check_line"
}

# 2. Branch, bump, PR (skipped by --dry-run; resumed from an existing PR).
if [ "$dry_run" = 1 ]; then
  plugin_budget_check "$version" || exit 1
  cat <<EOF

Dry run for $tag ($stream_name): every precondition above passed; $plugin_check_line.
Nothing else was changed.
Without --dry-run this would:
  2. create $branch from main, bump the plugin to $version (scripts/bump-plugin-version.sh), record $version as the cli stream's current in docs/release-streams.yaml (moving its next name when the release opens a new minor), rewrite the two doc lines that name the released version, commit "release: $tag", push it, open a PR to main, and enable squash auto-merge
  3. wait up to ${timeout_minutes}m for that PR to merge
  4. wait up to ${timeout_minutes}m for the merge commit's test, terraform, rules and console checks, and require them to pass (the release gate)
  5. create and push the tag $tag (no prompt: the green release gate on the verified merge commit is the safeguard)
EOF
  exit 0
fi

commit_body="Bump the plugin version and every skill header to $version (scripts/bump-plugin-version.sh) so the release gate's version check passes once this merges to main, and record $version as the cli stream's current in docs/release-streams.yaml so the file the console's dependency graph draws describes what has shipped. Name $version as the released version in docs/design/project-named-oauth-tokens.md and docs/project-claude-accounts.md, whose status lines a docs test pins to the tree's version.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
pr_body="Bumps plugin/.claude-plugin/plugin.json and every skill header to $version (scripts/bump-plugin-version.sh), per docs/release.md, and records $version as the cli stream's current in docs/release-streams.yaml and as the released version in the two docs that name it. Opened by scripts/release.sh; auto-merge (squash) is enabled."

closed_message() {
  echo "pull request #$1 ($2) for $branch was closed without merging.
To retry: delete the release branch (git push origin --delete $branch, and git branch -D $branch if it exists locally), then re-run scripts/release.sh $version; a fresh pull request is opened. Or reopen the pull request on GitHub and re-run." >&2
}

pr_line=$(retry_read gh pr list --base main --head "$branch" --state all --limit 1 --json number,state --jq '.[0] | select(.) | "\(.number) \(.state)"')
pr_number=""
pr_listed_state=""
if [ -n "$pr_line" ]; then
  pr_number=${pr_line%% *}
  pr_listed_state=${pr_line#* }
fi
remote_branch=0
if retry_read git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
  remote_branch=1
fi
if [ "$pr_listed_state" = CLOSED ]; then
  if [ "$remote_branch" = 1 ]; then
    closed_message "$pr_number" "https://github.com/$repo/pull/$pr_number"
    exit 1
  fi
  # Closed and its branch deleted: that is the documented recovery, start over.
  pr_number=""
fi
if [ "$local_tag" = 1 ] && [ "$pr_listed_state" != MERGED ]; then
  echo "tag $tag already exists locally but the release pull request is not merged; delete the local tag (git tag -d $tag) and re-run, or merge the pull request first" >&2
  exit 1
fi

pr_url=""
if [ -n "$pr_number" ]; then
  pr_url="https://github.com/$repo/pull/$pr_number"
  echo "resuming existing pull request $pr_url"
else
  base_ref=""
  base_desc=""
  if [ "$remote_branch" = 1 ]; then
    retry_read git fetch --quiet origin "$branch"
    base_ref="origin/$branch"
    base_desc="pushed branch $branch (no pull request yet)"
  elif git show-ref --quiet --verify "refs/heads/$branch"; then
    base_ref="refs/heads/$branch"
    base_desc="local branch $branch (not yet pushed)"
  fi

  # A branch from an earlier, interrupted run is only safe to resume if it
  # is still based on the current origin/main; otherwise (main advanced
  # since) rebuild it fresh rather than opening a PR against stale history.
  if [ -n "$base_ref" ] && [ "$(git merge-base "$base_ref" origin/main)" = "$origin_main" ]; then
    echo "resuming $base_desc"
    git switch --quiet -C "$branch" "$base_ref"
    # The precondition read main's catalogue; a branch pushed by hand may carry
    # its own, and that is the one the release commit writes from.
    if ! stream_check=$($streams_cmd set --check cli "$version" 2>&1); then
      echo "docs/release-streams.yaml on $branch cannot record $version as the cli stream's current; delete the branch (git push origin --delete $branch; git branch -D $branch) and re-run:
$stream_check" >&2
      exit 1
    fi
  else
    [ -n "$base_ref" ] && echo "$base_desc predates main ($origin_main); rebuilding $branch from origin/main" >&2
    git switch --quiet -C "$branch" origin/main
  fi

  if ! scripts/bump-plugin-version.sh --check "$version" >/dev/null 2>&1; then
    scripts/bump-plugin-version.sh "$version" >/dev/null
  fi
  # The catalogue's cli row moves with the release, in the release commit
  # itself: the file is what the graph draws and what the next release's floor
  # is read from, so it never describes a version that has shipped (GV1.5).
  # `set` writes both halves of the row -- `current`, and `next` moved on when
  # the release opens a new minor, since `next` is the name the minor after the
  # current one takes -- and refuses a write that does not read back, so a
  # commit carrying a catalogue this build cannot parse, or one that names the
  # release after it wrongly, cannot be made. It runs even when the bump is
  # already on a resumed branch, which may have been pushed without the row
  # moved; at an equal version it rewrites the file byte for byte.
  $streams_cmd set cli "$version"
  # And the two doc lines a docs test pins to the tree's version, for the same
  # reason: only the release commit may move them.
  released_version_docs write "$version"
  if [ -n "$(git status --porcelain)" ]; then
    git add -A
    git commit --quiet -m "release: $tag" -m "$commit_body"
  fi

  if ! git push --quiet -u origin "$branch"; then
    echo "pushing $branch failed; fix the cause (permissions, a branch rule) and re-run scripts/release.sh $version: it resumes from the local branch" >&2
    exit 1
  fi

  if ! pr_url=$(gh pr create --title "release: $tag" --base main --head "$branch" --body "$pr_body"); then
    echo "'gh pr create' failed; $branch is pushed, so re-run scripts/release.sh $version once fixed: it resumes from that branch and opens the pull request" >&2
    exit 1
  fi
  pr_number=${pr_url##*/}
  echo "opened pull request $pr_url"
  git switch --quiet main
fi

# 3. Wait for the merge.

poll_pr() {
  n=$1
  deadline=$(($(date +%s) + timeout_seconds))
  cancelled_seen=""
  while :; do
    state=$(retry_read gh pr view "$n" --json state --jq .state)
    case "$state" in
      MERGED)
        retry_read gh pr view "$n" --json mergeCommit --jq .mergeCommit.oid
        return 0
        ;;
      CLOSED)
        closed_message "$n" "$pr_url"
        return 1
        ;;
    esac
    # Any terminal-but-not-ok state, not just FAILURE: a timed-out or errored
    # required check otherwise keeps this polling silently until
    # --timeout-minutes, instead of stopping right away and naming it. A
    # CANCELLED check is retryable once (a superseded or re-triggered run is
    # the usual cause); seen again on a later poll, it is a failure.
    bad=$(retry_read gh pr checks "$n" --json name,state --jq '([.[] | select(.state == "FAILURE" or .state == "ERROR" or .state == "CANCELLED" or .state == "TIMED_OUT" or .state == "ACTION_REQUIRED" or .state == "STARTUP_FAILURE" or .state == "STALE")][0] | select(.) | "\(.name) \(.state)") // empty' 2>/dev/null || true)
    if [ -n "$bad" ]; then
      bad_name=${bad% *}
      bad_state=${bad##* }
      if [ "$bad_state" = CANCELLED ] && [ -z "$cancelled_seen" ]; then
        cancelled_seen=$bad_name
        echo "check '$bad_name' was cancelled on pull request #$n; waiting once for a re-run" >&2
      else
        echo "check '$bad_name' failed ($bad_state) on pull request #$n ($pr_url)" >&2
        return 1
      fi
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "timed out after ${timeout_minutes}m waiting for pull request #$n ($pr_url) to merge" >&2
      return 1
    fi
    sleep "$poll_seconds"
  done
}

pr_state=$(retry_read gh pr view "$pr_number" --json state --jq .state)
case "$pr_state" in
  MERGED)
    merge_sha=$(retry_read gh pr view "$pr_number" --json mergeCommit --jq .mergeCommit.oid)
    ;;
  CLOSED)
    # Before any 'gh pr merge': a closed pull request cannot be merged.
    closed_message "$pr_number" "$pr_url"
    exit 1
    ;;
  *)
    # Not read-only, so not retry_read: a request that failed on a dropped
    # connection may still have reached GitHub, so the pull request is read
    # again before the request is repeated, and a merged or closed one is left
    # to the wait below.
    merge_try=1
    while ! merge_err=$(gh pr merge --auto --squash "$pr_number" 2>&1 >/dev/null); do
      if [ "$merge_try" -lt "$retry_tries" ] && retry_is_transient "$merge_err"; then
        echo "'gh pr merge' failed (try $merge_try of $retry_tries): $merge_err; reading the pull request again, then retrying in $((retry_seconds * merge_try))s" >&2
        sleep $((retry_seconds * merge_try))
        merge_try=$((merge_try + 1))
        recheck=$(retry_read gh pr view "$pr_number" --json state --jq .state) || recheck=""
        [ "$recheck" != MERGED ] && [ "$recheck" != CLOSED ] || break
        continue
      fi
      echo "could not enable auto-merge on pull request #$pr_number ($pr_url): $merge_err
Auto-merge may be off for this repository. Merge the pull request yourself, then re-run scripts/release.sh $version to continue." >&2
      exit 1
    done
    echo "waiting for $pr_url to merge (checking every ${poll_seconds}s, giving up after ${timeout_minutes}m)..."
    if ! merge_sha=$(poll_pr "$pr_number"); then
      exit 1
    fi
    ;;
esac

# 4. Verify the merged commit on main.
echo "pull request #$pr_number merged as $merge_sha; verifying main"
git switch --quiet main
retry_read git pull --quiet --ff-only origin main
head_sha=$(git rev-parse HEAD)
if [ "$head_sha" != "$merge_sha" ]; then
  echo "HEAD ($head_sha) is not the release merge commit ($merge_sha); something else landed on main first, inspect before tagging" >&2
  exit 1
fi
scripts/bump-plugin-version.sh --check "$version"
# CI starts on the merge commit only now, so its checks are usually missing
# or still running: the gate waits for them (up to --timeout-minutes), naming
# what is pending, and fails at once on a conclusive failure. It does not wait
# for CI on a squash-merge commit whose tree equals the green PR head's: it
# uses the head's checks and says so.
echo "waiting for test, terraform, rules and the console checks to pass on $head_sha (up to ${timeout_minutes}m)..."
GITHUB_SHA="$head_sha" GITHUB_REPOSITORY="$repo" GATE_WAIT_SECONDS="$timeout_seconds" GATE_POLL_SECONDS="$poll_seconds" scripts/release-gate.sh "$tag"

# 5. Summary and confirmation.
subject=$(git log -1 --format=%s "$head_sha")
cat <<EOF

Release summary
  tag:     $tag
  name:    $stream_name
  commit:  $head_sha ($subject)
  checks:  test, terraform, rules and both console checks (console (ubuntu-latest), console (macos-latest)) all succeeded, on $head_sha or on the identical-tree head of the merged PR (scripts/release-gate.sh says which, above)

A pushed tag is permanent: the Go module proxy and the public ghcr.io images
cache vX.Y.Z forever, so it is never moved or re-cut (docs/release.md#rolling-back).
EOF

if [ "$local_tag" = 1 ]; then
  existing=$(git rev-parse "refs/tags/$tag^{commit}")
  if [ "$existing" != "$head_sha" ]; then
    echo "tag $tag already exists locally at $existing, not the verified merge commit $head_sha; delete it (git tag -d $tag) and re-run" >&2
    exit 1
  fi
  echo "Tag $tag already exists locally at this commit but is not on origin; pushing it."
fi

# 6. Tag and push.
if [ "$local_tag" != 1 ]; then
  git tag -a "$tag" -m "Fugaro $tag" "$head_sha"
fi
# A failed push is repeated only on a dropped connection, and only once origin
# has been read again and found without the tag: a push that reached origin
# before the connection dropped is not pushed twice.
# already_there: origin turned out to have the tag at this commit, so there was
# nothing to push and the failure's own output is not printed again below. The
# run goes on to the verification either way: whether this run pushed the tag or
# found it there, what has to hold is the same.
already_there=0
push_try=1
while ! push_out=$(git push origin "refs/tags/$tag" 2>&1); do
  printf '%s\n' "$push_out" >&2
  remote_sha=$(retry_read git ls-remote origin "refs/tags/$tag^{}" | cut -f1)
  if [ "$remote_sha" = "$head_sha" ]; then
    echo "tag $tag is already on origin at $head_sha; nothing to push."
    already_there=1
    break
  fi
  if [ -z "$remote_sha" ] && [ "$push_try" -lt "$retry_tries" ] && retry_is_transient "$push_out"; then
    echo "pushing tag $tag failed (try $push_try of $retry_tries) and origin does not have it; retrying in $((retry_seconds * push_try))s" >&2
    sleep $((retry_seconds * push_try))
    push_try=$((push_try + 1))
    continue
  fi
  echo "pushing tag $tag failed. It exists locally at $head_sha; once the cause is fixed, push it with:
  git push origin refs/tags/$tag
or re-run scripts/release.sh $version, which pushes it." >&2
  exit 1
done
if [ "$already_there" = 0 ]; then
  printf '%s\n' "$push_out"
fi

cat <<EOF
tag $tag pushed. Watch:
  https://github.com/$repo/actions/workflows/release.yml
  https://github.com/$repo/actions/workflows/images.yml
  https://github.com/$repo/actions/workflows/console-windows.yml

Post-release checklist (docs/release.md):
  - the first release of a new image package: make it public once (package settings, Danger Zone, Change visibility)
EOF

# 7. Verify the release: every workflow the tag started has to conclude green,
# the images it published have to be pullable without a login, and -- on a patch
# tag -- the fixes it carries have to be on main. This waits (bounded), so it is
# the last thing the script does; scripts/verify-release.sh runs it on its own
# too, for a tag whose runs are already finished.
scripts/verify-release.sh "$tag" --timeout-minutes "$timeout_minutes"
