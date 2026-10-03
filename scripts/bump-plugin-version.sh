#!/usr/bin/env bash
# Keep the plugin version in step with the release (design section 12).
#   scripts/bump-plugin-version.sh X.Y.Z          set the version in plugin.json (marketplace.json carries none: the plugin manifest wins)
#   scripts/bump-plugin-version.sh --check X.Y.Z  fail if plugin.json differs from X.Y.Z (used by the release workflow)
set -euo pipefail
cd "$(dirname "$0")/.."

mode=set
if [ "${1:-}" = "--check" ]; then
  mode=check
  shift
fi
v=${1:-}
v=${v#v}
if ! printf '%s\n' "$v" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "usage: $0 [--check] X.Y.Z" >&2
  exit 2
fi

plugin=plugin/.claude-plugin/plugin.json

if [ "$mode" = check ]; then
  rc=0
  got=$(jq -r .version "$plugin")
  [ "$got" = "$v" ] || { echo "$plugin has version $got, want $v" >&2; rc=1; }
  exit $rc
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
sed -E 's/^([[:space:]]*"version":[[:space:]]*)"[^"]*"/\1"'"$v"'"/' "$plugin" > "$tmp" && cat "$tmp" > "$plugin"
"$0" --check "$v"
echo "plugin version set to $v; commit it before tagging v$v"
