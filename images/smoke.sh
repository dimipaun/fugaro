#!/bin/sh
# smoke.sh IMAGE BASE checks a built base image with plain docker (design
# §13): fugaro, Claude Code, gh and the toolchain run, and the image runs as
# the non-root fugaro user in /work/repo under tini, with no passwordless
# sudo, no leftover credential files, and no file under $HOME owned by
# anyone but fugaro. The image's Claude Code, gh and (for web-node) Node are
# checked against CLAUDE_CODE_VERSION, GH_VERSION and NODE_VERSION, which
# default to the ARG lines in images/BASE/Dockerfile, so CI and release
# builds always assert the pins; set one in the environment to override it.
set -eu
image=${1:?usage: smoke.sh IMAGE BASE}
base=${2:?usage: smoke.sh IMAGE BASE}
run() { docker run --rm "$image" "$@"; }
fail() { echo "smoke: $*" >&2; exit 1; }
first_line() { printf '%s\n' "$1" | sed -n '1p'; }

dockerfile="$(dirname "$0")/$base/Dockerfile"
[ -f "$dockerfile" ] || fail "there is no $dockerfile to read the pinned versions from"
# arg_default NAME prints the default of the Dockerfile's `ARG NAME=value`.
arg_default() { sed -n "s/^ARG $1=\([^[:space:]]*\)[[:space:]]*\$/\1/p" "$dockerfile" | sed -n '1p'; }
CLAUDE_CODE_VERSION=${CLAUDE_CODE_VERSION:-$(arg_default CLAUDE_CODE_VERSION)}
GH_VERSION=${GH_VERSION:-$(arg_default GH_VERSION)}
[ -n "$CLAUDE_CODE_VERSION" ] || fail "$dockerfile has no ARG CLAUDE_CODE_VERSION=... pin"
[ -n "$GH_VERSION" ] || fail "$dockerfile has no ARG GH_VERSION=... pin"

fugaro_version=$(run fugaro version) || fail "fugaro version failed"
echo "fugaro $fugaro_version"

claude_version=$(run claude --version) || fail "claude --version failed"
echo "claude $claude_version"
case "$claude_version" in
  "$CLAUDE_CODE_VERSION "*) ;;
  *) fail "claude --version reports '$claude_version', not pinned CLAUDE_CODE_VERSION=$CLAUDE_CODE_VERSION" ;;
esac

gh_version_full=$(run gh --version) || fail "gh --version failed"
gh_version=$(first_line "$gh_version_full")
echo "$gh_version"
case "$gh_version" in
  *" $GH_VERSION "*) ;;
  *) fail "gh --version reports '$gh_version', not pinned GH_VERSION=$GH_VERSION" ;;
esac

case "$base" in
  web-node)
    node_version=$(run node -v) || fail "node -v failed"
    corepack_version=$(run corepack --version) || fail "corepack --version failed"
    echo "node $node_version, corepack $corepack_version"
    NODE_VERSION=${NODE_VERSION:-$(arg_default NODE_VERSION)}
    [ -n "$NODE_VERSION" ] || fail "$dockerfile has no ARG NODE_VERSION=... pin"
    case "$node_version" in
      v"$NODE_VERSION".*) ;;
      *) fail "node -v reports '$node_version', not pinned NODE_VERSION major=$NODE_VERSION" ;;
    esac
    ;;
  server-jvm)
    run java -version || fail "java -version failed"
    ;;
  *) fail "unknown base $base" ;;
esac

user=$(run id -un) || fail "id -un failed"
[ "$user" = fugaro ] || fail "the image does not run as fugaro"

wd=$(run pwd) || fail "pwd failed"
[ "$wd" = /work/repo ] || fail "the working directory is not /work/repo"

pid1=$(run sh -c 'tr "\000" " " </proc/1/cmdline') || fail "reading PID 1's cmdline failed"
case "$pid1" in
  "/usr/bin/tini "*) ;;
  *) fail "PID 1 is not tini (got '$pid1')" ;;
esac

if run sudo -n true 2>/dev/null; then fail "fugaro has passwordless sudo"; fi

creds_mode=$(run stat -c '%a' /work/creds) || fail "stat /work/creds failed"
[ "$creds_mode" = 700 ] || fail "/work/creds is not mode 700 (got $creds_mode)"

foreign=$(run sh -c 'find "$HOME" ! -user fugaro 2>/dev/null') || fail "checking \$HOME ownership failed"
[ -z "$foreign" ] || fail "files under \$HOME are not owned by fugaro: $foreign"

if run sh -c 'test -e "$HOME/.git-credentials"' 2>/dev/null; then
  fail "~/.git-credentials exists in the image"
fi
if run sh -c 'test -e "$HOME/.claude.json"' 2>/dev/null; then
  fail "~/.claude.json exists in the image"
fi

echo "smoke: $image ok"
