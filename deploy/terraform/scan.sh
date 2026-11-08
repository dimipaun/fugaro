#!/bin/sh
# scan.sh checks the Terraform under deploy/terraform for misconfigurations
# with Trivy and fails on any HIGH or CRITICAL finding that .trivyignore
# doesn't justify. Run it from the repository root.
#
# Trivy is the one on PATH when there is one: a run's job image bakes it, and
# a Cloud Run job has no Docker daemon to run the image route with. Everywhere
# else -- CI, a developer's machine -- it is the Trivy image images/scan.sh
# pins, through plain docker. The two routes are one command line: the same
# flags, the same .trivyignore, the same cache, so a finding one reports the
# other reports too.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
# The route's two differences: the docker route scans the mount it makes at
# /src, and names the ignore file inside it.
if command -v trivy >/dev/null 2>&1; then
  route=direct
  ignorefile=$here/.trivyignore
  target=$here
else
  route=docker
  ignorefile=/src/.trivyignore
  target=/src
fi
set -- config --skip-version-check --exit-code 1 --severity HIGH,CRITICAL \
  --ignorefile "$ignorefile" --skip-dirs '**/.terraform' "$target"
if [ "$route" = direct ]; then
  TRIVY_CACHE_DIR=$cache exec trivy "$@"
fi
trivy_image=$(sed -n 's/^trivy_image=//p' "$here/../../images/scan.sh")
[ -n "$trivy_image" ] || { echo "scan.sh: no trivy_image pin in images/scan.sh" >&2; exit 1; }
docker run --rm -v "$here:/src:ro" -v "$cache:/root/.cache/trivy" "$trivy_image" "$@"
