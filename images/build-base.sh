#!/bin/sh
# build-base.sh BASE [TAG...] builds the Fugaro base image images/BASE from
# this checkout with plain docker, tagged TAG (default fugaro-BASE:dev).
# PLATFORM (default linux/amd64, what Cloud Run runs), FUGARO_VERSION
# (default dev) and SOURCE_DATE_EPOCH (default: the last commit's date, and
# required: a checkout whose log carries none is refused rather than dated 0)
# come from the environment. CI and the Docker tests both build through this
# script.
#
# Two builds of the same tree must produce the same image (issue #1745: the
# catalog check compares the base's digest, internal/imagecheck, so a nightly
# that changes nothing must not publish a new one), and what the build cannot
# read from the tree is decided here:
#
#   * SOURCE_DATE_EPOCH is the date the build is taken to have happened on,
#     which the Dockerfile passes to images/base/byproducts.sh for the files a
#     package install stamps with the clock, and which BuildKit's dockerfile
#     frontend uses for the image's and its own layers' timestamps (from
#     Dockerfile 1.5 / BuildKit 0.11, so an older builder ignores it and
#     builds a dated image instead of failing);
#   * rewrite-timestamp makes the exporter write that same date on every file
#     of every layer it exports, which is what covers the files that arrive
#     with a timestamp of their own: COPY's, and the ones the build took from
#     an image the tree does not contain. BuildKit warns and exports unchanged
#     when it finds no source-date-epoch, so the two stand or fall together.
#
# A date the *tree* does not carry would be the wrong answer to "are these two
# builds the same tree?", which is why the default is the last commit's date
# and not the wall clock: an unchanged checkout rebuilt later is dated the
# same as its first build.
set -eu
base=${1:?usage: build-base.sh BASE [TAG...]}
shift
root=$(cd "$(dirname "$0")/.." && pwd)
if [ ! -f "$root/images/$base/Dockerfile" ]; then
  echo "build-base.sh: there is no base image $base under images/" >&2
  exit 1
fi
[ $# -gt 0 ] || set -- "fugaro-$base:dev"
epoch=${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct 2>/dev/null || echo 0)}
# A build with no date is not a build dated 1970: `chage -d 1970-01-01` writes
# the fugaro account a last-change day PAM reads as "the password must be
# changed", so `su` and every PAM account check against the image fail, and
# nothing else in the build notices. A checkout with no commit has no date to
# take, and that is a refusal rather than a silent 0.
if [ -z "$epoch" ] || [ "$epoch" = 0 ]; then
  echo "build-base.sh: SOURCE_DATE_EPOCH is ${epoch:-empty} and this checkout has no commit date to take: pass SOURCE_DATE_EPOCH=<unix seconds>, because 0 dates the image 1970 and PAM reads the fugaro account's day-0 last-change as a required password change" >&2
  exit 1
fi
# The tags, in the order given: the docker exporter below names the image
# after the first one, and the rest are applied to it afterwards, so a caller
# asking for several still gets them all. A tag cannot contain a space (a
# docker reference cannot), so the list survives the word splitting.
tags=$*
first=$1
# Turn the tags into --tag flags in place: the positional parameters are
# POSIX sh's only list, and each flag stays one word.
n=$#
for tag in "$@"; do set -- "$@" --tag "$tag"; done
shift "$n"
# The default docker driver cannot take rewrite-timestamp beside the image
# store's unpack (BuildKit refuses the pair), so the build runs on a
# docker-container builder, which exports a tarball for docker to load.
docker buildx inspect fugaro-base >/dev/null 2>&1 || docker buildx create --name fugaro-base --driver docker-container >/dev/null
docker buildx build --builder fugaro-base --progress plain \
  --platform "${PLATFORM:-linux/amd64}" \
  --build-arg "SOURCE_DATE_EPOCH=$epoch" \
  --build-arg "FUGARO_VERSION=${FUGARO_VERSION:-dev}" \
  --build-arg "FUGARO_COMMIT=${FUGARO_COMMIT:-$(git -C "$root" rev-parse HEAD 2>/dev/null || echo unknown)}" \
  --file "$root/images/$base/Dockerfile" \
  --output "type=docker,rewrite-timestamp=true" \
  "$@" "$root"
for tag in $tags; do
  [ "$tag" = "$first" ] || docker tag "$first" "$tag"
done
