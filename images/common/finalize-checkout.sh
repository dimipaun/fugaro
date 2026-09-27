#!/bin/sh
# finalize-checkout [DIR] is the last step of every derived image build
# (design §7.2). It strips credential configuration from the baked checkout
# (default /work/repo) and from HOME, then enforces the baked-checkout
# contract: origin is an https URL without credentials, and no git config
# scope (local, global or system) sets a credential helper, an
# http.extraheader (scoped to a URL or not), or a credential-bearing
# url.insteadOf, because Fugaro's runner supplies credentials at run time. A
# file:// or absolute-path origin is allowed for tests. It never prints the
# origin URL or any config value, which could hold a token.
set -eu
repo=${1:-/work/repo}
git -C "$repo" rev-parse --git-dir >/dev/null
strip_pattern='^(credential\..*|http\.(.*\.)?extraheader|url\..*\.insteadof)$'
keys=$(git -C "$repo" config --local --name-only --get-regexp "$strip_pattern" || true)
for key in $keys; do
  git -C "$repo" config --local --unset-all "$key"
done
rm -f "$HOME/.git-credentials"
# Whatever a credential.*, http.extraheader or url.insteadOf key survives
# past the local strip above must come from the system or global git
# config, which the baked image must not carry.
if git -C "$repo" config --get-regexp '^credential\.' >/dev/null 2>&1; then
  echo "finalize-checkout: a git credential helper is still configured (system or global git config)" >&2
  exit 1
fi
if git -C "$repo" config --get-regexp '^http\.(.*\.)?extraheader$' >/dev/null 2>&1; then
  echo "finalize-checkout: an http.extraheader is still configured (system or global git config)" >&2
  exit 1
fi
if git -C "$repo" config --get-regexp '^url\..*\.insteadof$' 2>/dev/null \
    | grep -Eq '://[^/[:space:]]*:[^/[:space:]@]*@'; then
  echo "finalize-checkout: a credential-bearing url.insteadOf is still configured (system or global git config)" >&2
  exit 1
fi
origin=$(git -C "$repo" remote get-url origin 2>/dev/null || true)
case "$origin" in
  "")
    echo "finalize-checkout: $repo has no origin remote" >&2
    exit 1 ;;
  http://*@*|https://*@*)
    echo "finalize-checkout: the origin URL embeds credentials; pass REPO_ORIGIN without them" >&2
    exit 1 ;;
  https://*|file://*|/*) ;;
  *)
    echo "finalize-checkout: the origin must be an https URL (Fugaro adds credentials at run time); pass REPO_ORIGIN" >&2
    exit 1 ;;
esac
