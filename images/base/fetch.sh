#!/bin/sh
# fetch.sh URL SHA256 OUT downloads URL to OUT over https and keeps it only
# if its sha256 is SHA256 (design base-image.md section 7). The base image
# build bind-mounts it; it is never in the image.
set -eu
usage="usage: fetch.sh URL SHA256 OUT"
url=${1:?$usage}
sum=${2:?$usage}
out=${3:?$usage}
case "$url" in
  https://*) ;;
  *) echo "fetch: $url is not https" >&2; exit 1 ;;
esac
case "$sum" in
  *[!0-9a-f]*) echo "fetch: bad sha256 '$sum' for $url" >&2; exit 1 ;;
esac
[ "${#sum}" -eq 64 ] || { echo "fetch: bad sha256 '$sum' for $url" >&2; exit 1; }

# download URL OUT fetches URL to OUT. The first attempt is HTTP/2 and is
# retried: --retry-all-errors so that a reset connection, and the "curl: (92)
# HTTP/2 stream was not closed cleanly" that failed the amd64 base build on
# #903, are retried too, which plain --retry does not. The last attempt
# speaks HTTP/1.1, which has no stream framing left to lose. A failing
# download is still a failing build: the sha256 check below reads whatever
# the retries left in OUT and is unchanged.
download() {
  curl -fsSL --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors --retry-delay 2 -o "$2" "$1" \
    || curl -fsSL --proto '=https' --tlsv1.2 --http1.1 --retry 5 --retry-all-errors --retry-delay 2 -o "$2" "$1"
}
download "$url" "$out"
if ! echo "$sum  $out" | sha256sum -c - >/dev/null 2>&1; then
  rm -f "$out"
  echo "fetch: $url does not match its pinned sha256 $sum" >&2
  exit 1
fi
