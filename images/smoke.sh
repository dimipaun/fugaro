#!/bin/sh
# smoke.sh IMAGE BASE checks a built base image with plain docker (design
# §13): fugaro, Claude Code, gh and the toolchain run, and the image runs as
# the non-root fugaro user in /work/repo under tini, with no passwordless
# sudo.
set -eu
image=${1:?usage: smoke.sh IMAGE BASE}
base=${2:?usage: smoke.sh IMAGE BASE}
run() { docker run --rm "$image" "$@"; }
fail() { echo "smoke: $*" >&2; exit 1; }
echo "fugaro $(run fugaro version)"
echo "claude $(run claude --version)"
echo "$(run gh --version | head -n 1)"
case "$base" in
  web-node) echo "node $(run node -v), corepack $(run corepack --version)" ;;
  server-jvm) run java -version ;;
  *) fail "unknown base $base" ;;
esac
[ "$(run id -un)" = fugaro ] || fail "the image does not run as fugaro"
[ "$(run pwd)" = /work/repo ] || fail "the working directory is not /work/repo"
run sh -c 'tr "\000" " " </proc/1/cmdline' | grep -q '^/usr/bin/tini ' || fail "PID 1 is not tini"
if run sudo -n true 2>/dev/null; then fail "fugaro has passwordless sudo"; fi
echo "smoke: $image ok"
