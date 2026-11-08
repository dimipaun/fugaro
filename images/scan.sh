#!/bin/sh
# scan.sh IMAGE scans a built image for vulnerabilities with Trivy, run
# through plain docker. It prints HIGH and CRITICAL findings and fails only
# on CRITICAL ones that have a fix available.
#
# scan.sh --secrets IMAGE scans every layer (each one separately, so a file a
# later layer deletes is still seen) and the image config (its history and
# environment, where a leaked build argument would show) for secrets, and
# fails on any finding not allowed by images/trivy-secret.yaml.
set -eu
mode=vuln
if [ "${1:-}" = --secrets ]; then
  mode=secrets
  shift
fi
image=${1:?usage: scan.sh [--secrets] IMAGE}
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
here=$(cd "$(dirname "$0")" && pwd)
# Pinned by digest (not just the 0.76.2 tag, which is mutable): checked on
# 2026-11-08 through the registry's manifest API, the read `docker inspect
# aquasec/trivy:0.76.2 --format '{{index .RepoDigests 0}}'` makes -- the same
# one that produced the 0.65.0 pin this replaced. 0.65.0 moved because GitHub
# no longer serves a release for it, and mise installs Trivy from the release,
# so the version mise.toml pins (the Trivy a run's own PATH carries) cannot be
# it; images/repo_pins_test.go holds this tag's version and that pin equal.
# Dependabot doesn't cover this pin (it's a shell variable, not a Dockerfile
# FROM): bump it by hand monthly, alongside the base refreshes, to the latest
# Trivy release's digest, and move mise.toml's trivy pin with it.
trivy_image=aquasec/trivy@sha256:8f6bb947dbc528902e11f2f5be52f7b519f6a39d5d148f41166f2040499768fb
trivy() {
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$cache:/root/.cache/trivy" \
    -v "$here/trivy-secret.yaml:/trivy-secret.yaml:ro" "$trivy_image" image "$@" "$image"
}
if [ "$mode" = secrets ]; then
  trivy --scanners secret --image-config-scanners secret --secret-config /trivy-secret.yaml --exit-code 1
  exit 0
fi
trivy --scanners vuln --ignore-unfixed --severity HIGH,CRITICAL --exit-code 0
trivy --scanners vuln --ignore-unfixed --severity CRITICAL --exit-code 1 --quiet
