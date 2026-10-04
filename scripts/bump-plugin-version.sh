#!/usr/bin/env bash
# Keep the plugin version, and every skill's header (design section 4.4), in
# step with the release (design section 12).
#   scripts/bump-plugin-version.sh X.Y.Z          set the version in plugin.json and every skill header (marketplace.json carries none: the plugin manifest wins)
#   scripts/bump-plugin-version.sh --check X.Y.Z  fail if plugin.json or any skill header differs from X.Y.Z (used by the release workflow)
set -euo pipefail
cd "$(dirname "$0")/.."

mode=set
if [ "${1:-}" = "--check" ]; then
  mode=check
  shift
fi
v=${1:-}
v=${v#v}
if ! [[ $v =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "usage: $0 [--check] X.Y.Z" >&2
  exit 2
fi

plugin=plugin/.claude-plugin/plugin.json

# No jq: the runner's own base image doesn't carry it, and this script also
# runs inside fugaro's own CI container.
plugin_version() {
  sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$plugin" | head -1
}

# check_header prints nothing and returns non-zero when a skill file's header
# comment or its "(Fugaro X.Y.Z)" sentence is missing or differs from $v.
check_header() {
  local f=$1 tag body
  tag=$(sed -n 's/.*fugaro-version=\([0-9][0-9.]*\).*/\1/p' "$f" | head -1)
  body=$(sed -n 's/.*(Fugaro \([0-9][0-9.]*\)).*/\1/p' "$f" | head -1)
  [ -n "$tag" ] && [ -n "$body" ] && [ "$tag" = "$v" ] && [ "$body" = "$v" ]
}

skill_files=$(find plugin/skills -type f -name '*.md' | sort)

if [ "$mode" = check ]; then
  rc=0
  got=$(plugin_version)
  [ "$got" = "$v" ] || { echo "$plugin has version $got, want $v" >&2; rc=1; }
  for f in $skill_files; do
    check_header "$f" || { echo "$f header is missing or stale, want $v" >&2; rc=1; }
  done
  exit $rc
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
sed -E 's/^([[:space:]]*"version":[[:space:]]*)"[^"]*"/\1"'"$v"'"/' "$plugin" > "$tmp" && cat "$tmp" > "$plugin"
for f in $skill_files; do
  sed -E -e 's/(fugaro-version=)[0-9][0-9.]*/\1'"$v"'/' -e 's/(\(Fugaro )[0-9][0-9.]*(\))/\1'"$v"'\2/' "$f" > "$tmp" && cat "$tmp" > "$f"
done
"$0" --check "$v"
echo "plugin version set to $v; commit it before tagging v$v"
