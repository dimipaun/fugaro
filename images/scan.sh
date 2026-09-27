#!/bin/sh
# scan.sh IMAGE scans a built image for vulnerabilities with Trivy, run
# through plain docker. It prints HIGH and CRITICAL findings and fails only
# on CRITICAL ones that have a fix available.
set -eu
image=${1:?usage: scan.sh IMAGE}
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
trivy() {
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$cache:/root/.cache/trivy" \
    aquasec/trivy:0.65.0 image --scanners vuln --ignore-unfixed "$@" "$image"
}
trivy --severity HIGH,CRITICAL --exit-code 0
trivy --severity CRITICAL --exit-code 1 --quiet
