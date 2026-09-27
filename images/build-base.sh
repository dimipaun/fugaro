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
tags=""
for tag in "$@"; do tags="$tags --tag $tag"; done
# $tags is deliberately unquoted: it is a list of flags.
DOCKER_BUILDKIT=1 docker build --progress plain \
  --platform "${PLATFORM:-linux/amd64}" \
  --build-arg "FUGARO_VERSION=${FUGARO_VERSION:-dev}" \
  --file "$root/images/$base/Dockerfile" $tags "$root"
