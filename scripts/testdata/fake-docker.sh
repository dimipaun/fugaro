#!/bin/sh
# A fake `docker` for scripts/release_test.go: the three calls
# images/verify-public.sh makes, answered the way a public ghcr.io package
# answers them. Configuration comes from the environment of the script under
# test, which this inherits:
#   FAKE_DOCKER_FAIL  a substring of a reference that cannot be read without a
#                     login (the package is private, or the tag was never
#                     published): "docker manifest inspect" and "docker pull"
#                     fail for it, as verify-public.sh sees a 403 or a 404
#   FAKE_DOCKER_LOG   a file every invocation is appended to (default: nowhere)
# A fugaro-base reference is answered as a multi-platform index (linux/amd64
# and linux/arm64, which is what --arm64 requires); every other reference as a
# single-image manifest, whose linux/amd64 pull is the proof verify-public.sh
# asks of it. Nothing here talks to a registry: a release verification reads
# what this answers, and it is scripts/testdata/fake-gh.sh that decides whether
# the workflows that publish those images were green.
set -eu
printf '%s\n' "$*" >>"${FAKE_DOCKER_LOG:-/dev/null}"

# fails REF exits 0 when REF is the reference configured to be unreadable.
fails() {
  [ -n "${FAKE_DOCKER_FAIL:-}" ] || return 1
  case "$1" in
    *"$FAKE_DOCKER_FAIL"*) return 0 ;;
  esac
  return 1
}

case "$1 ${2:-}" in
  "manifest inspect")
    ref=${3:-}
    if fails "$ref"; then
      echo "manifest unknown: $ref is not readable without a login" >&2
      exit 1
    fi
    case "$ref" in
      *fugaro-base*)
        printf '%s\n' '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"platform":{"architecture":"amd64","os":"linux"}},{"platform":{"architecture":"arm64","os":"linux"}}]}'
        ;;
      *)
        printf '%s\n' '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","size":1}}'
        ;;
    esac
    exit 0
    ;;
  "pull --quiet")
    ref=$(printf '%s' "$*" | awk '{print $NF}')
    if fails "$ref"; then
      echo "pull access denied for $ref" >&2
      exit 1
    fi
    printf '%s\n' 'sha256:fake'
    exit 0
    ;;
  "buildx imagetools")
    ref=${4:-}
    printf 'sha256:fake-%s\n' "${ref##*:}"
    exit 0
    ;;
esac

echo "fake-docker.sh: unhandled invocation: $*" >&2
exit 1
