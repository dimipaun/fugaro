#!/bin/sh
# verify-public.sh REF... proves every published image reference can be pulled
# by someone with no registry login and has a linux/amd64 build, which is what
# `fugaro init` (the mirror) and a plain `docker pull` need. It runs docker with
# an empty config directory so no credentials can leak in, prints each image's
# digest, and exits 1 naming every reference that fails. A new ghcr.io package
# is private until the owner makes it public once on GitHub (docs/release.md).
set -eu
[ "$#" -gt 0 ] || { echo "usage: verify-public.sh REF..." >&2; exit 2; }
anon=$(mktemp -d)
trap 'rm -rf "$anon"' EXIT
export DOCKER_CONFIG=$anon
bad=0
for ref in "$@"; do
  if ! manifest=$(docker manifest inspect "$ref" 2>&1); then
    echo "verify-public: $ref cannot be read without a login (is the package public?): $manifest" >&2
    bad=1
    continue
  fi
  case "$manifest" in
    *'"manifests"'*)
      # A multi-platform index lists its platforms.
      if ! printf '%s' "$manifest" | tr -d ' \n\t' | grep -q '"architecture":"amd64","os":"linux"\|"os":"linux","architecture":"amd64"'; then
        echo "verify-public: $ref has no linux/amd64 entry" >&2
        bad=1
        continue
      fi
      ;;
    *)
      # A single-image manifest names no platform: pulling it for amd64 is the proof.
      if ! out=$(docker pull --quiet --platform linux/amd64 "$ref" 2>&1); then
        echo "verify-public: $ref is not pullable for linux/amd64: $out" >&2
        bad=1
        continue
      fi
      ;;
  esac
  digest=$(docker buildx imagetools inspect "$ref" --format '{{.Manifest.Digest}}' 2>/dev/null || true)
  echo "verify-public: $ref ok (${digest:-digest unknown})"
done
exit "$bad"
