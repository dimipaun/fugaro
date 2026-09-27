#!/bin/sh
# scan.sh IMAGE scans a built image for vulnerabilities with Trivy, run
# through plain docker. It prints HIGH and CRITICAL findings and fails only
# on CRITICAL ones that have a fix available.
set -eu
image=${1:?usage: scan.sh IMAGE}
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
# Pinned by digest (not just the 0.65.0 tag, which is mutable): checked with
# `docker inspect aquasec/trivy:0.65.0 --format '{{index .RepoDigests 0}}'`
# on 2026-09-27.
trivy_image=aquasec/trivy@sha256:a22415a38938a56c379387a8163fcb0ce38b10ace73e593475d3658d578b2436
trivy() {
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$cache:/root/.cache/trivy" \
    "$trivy_image" image --scanners vuln --ignore-unfixed "$@" "$image"
}
trivy --severity HIGH,CRITICAL --exit-code 0
trivy --severity CRITICAL --exit-code 1 --quiet
