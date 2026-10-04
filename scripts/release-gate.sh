#!/usr/bin/env bash
# The gate every publishing workflow runs first (release.yml and images.yml):
# the tag is a strict vX.Y.Z, its commit is reachable from origin/main, and the
# GitHub Actions checks test, terraform and rules all succeeded on that commit.
# Needs: a full-history checkout, GH_TOKEN (checks: read), GITHUB_SHA,
# GITHUB_REPOSITORY. Usage: scripts/release-gate.sh vX.Y.Z
# Optional: GATE_WAIT_SECONDS=N (default 0: no waiting, as in the workflows)
# polls every GATE_POLL_SECONDS (default 30) while a check is missing, still
# running or cancelled, so scripts/release.sh can run it right after a merge,
# before CI has finished. A conclusive failure always fails at once.
set -eu
tag=${1:-}
if ! [[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "::error::tag '$tag' is not a strict vX.Y.Z release tag" >&2
  exit 1
fi
git fetch --quiet origin main
if ! git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then
  echo "::error::$tag ($GITHUB_SHA) is not on main; tag a commit that is reachable from main" >&2
  exit 1
fi
wait_seconds=${GATE_WAIT_SECONDS:-0}
poll_seconds=${GATE_POLL_SECONDS:-30}
deadline=$(($(date +%s) + wait_seconds))
while :; do
  pending=""
  for name in test terraform rules; do
    # Only the GitHub Actions app counts: a third-party check named like a
    # required one must not satisfy the gate. Cancelled runs (superseded by
    # a newer one) are ignored while another run of the name exists; every
    # other run must have completed successfully.
    state=$(gh api "repos/$GITHUB_REPOSITORY/commits/$GITHUB_SHA/check-runs?per_page=100" \
      --jq "[.check_runs[] | select(.name == \"$name\" and .app.slug == \"github-actions\")] as \$r | if (\$r | length) == 0 then \"missing\" else [\$r[] | select(.conclusion != \"cancelled\")] as \$live | if (\$live | length) == 0 then \"cancelled\" elif any(\$live[]; .status == \"completed\" and .conclusion != \"success\") then \"failed\" elif any(\$live[]; .status != \"completed\") then \"pending\" else \"success\" end end")
    case "$state" in
      success) ;;
      missing | pending | cancelled)
        [ -n "$pending" ] || pending="$name $state"
        ;;
      *)
        echo "::error::required check '$name' is $state on $GITHUB_SHA" >&2
        exit 1
        ;;
    esac
  done
  [ -n "$pending" ] || break
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "::error::required check '${pending% *}' is ${pending#* } on $GITHUB_SHA (if CI is still running, re-run this job when it is green)" >&2
    exit 1
  fi
  echo "still waiting: check '${pending% *}' is ${pending#* } on $GITHUB_SHA"
  sleep "$poll_seconds"
done
echo "gate passed for $tag at $GITHUB_SHA"
