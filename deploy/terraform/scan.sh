#!/bin/sh
# scan.sh checks the Terraform under deploy/terraform for misconfigurations
# with Trivy, run through plain docker, and fails on any HIGH or CRITICAL
# finding that .trivyignore doesn't justify. Run it from the repository
# root. It uses the Trivy image images/scan.sh pins, so the two never drift.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
trivy_image=$(sed -n 's/^trivy_image=//p' "$here/../../images/scan.sh")
[ -n "$trivy_image" ] || { echo "scan.sh: no trivy_image pin in images/scan.sh" >&2; exit 1; }
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
docker run --rm -v "$here:/src:ro" -v "$cache:/root/.cache/trivy" "$trivy_image" \
  config --skip-version-check --exit-code 1 --severity HIGH,CRITICAL --ignorefile /src/.trivyignore \
  --skip-dirs '**/.terraform' /src
