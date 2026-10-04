#!/bin/sh
# build-base.sh BASE [TAG...] builds the Fugaro base image images/BASE from
# this checkout with plain docker, tagged TAG (default fugaro-BASE:dev).
# PLATFORM (default linux/amd64, what Cloud Run runs) and FUGARO_VERSION
# (default dev) come from the environment. CI and the Docker tests both
# build through this script.
set -eu
base=${1:?usage: build-base.sh BASE [TAG...]}
shift
root=$(cd "$(dirname "$0")/.." && pwd)
if [ ! -f "$root/images/$base/Dockerfile" ]; then
  echo "build-base.sh: there is no base image $base under images/" >&2
  exit 1
fi
[ $# -gt 0 ] || set -- "fugaro-$base:dev"
# Turn the tags into --tag flags in place: the positional parameters are
# POSIX sh's only list, and each flag stays one word.
n=$#
for tag in "$@"; do set -- "$@" --tag "$tag"; done
shift "$n"
DOCKER_BUILDKIT=1 docker build --progress plain \
  --platform "${PLATFORM:-linux/amd64}" \
  --build-arg "FUGARO_VERSION=${FUGARO_VERSION:-dev}" \
  --build-arg "FUGARO_COMMIT=${FUGARO_COMMIT:-$(git -C "$root" rev-parse HEAD 2>/dev/null || echo unknown)}" \
  --file "$root/images/$base/Dockerfile" "$@" "$root"
