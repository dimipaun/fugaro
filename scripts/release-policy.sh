#!/usr/bin/env bash
# Release rules that depend on the version, in one place so release.sh, the
# release workflow and the tests agree (docs/release.md).
#
#   scripts/release-policy.sh prerelease X.Y.Z            exit 0: mark the release as a pre-release
#   scripts/release-policy.sh highlights-required X.Y.Z   exit 0: docs/releases/vX.Y.Z.md is required
#
# Exit 1 means "no", exit 2 a usage error or a version that is not strict
# SemVer X.Y.Z. v0.1.0 to v0.3.x are pre-releases and need no highlights file;
# from 0.4.0 on a release is a normal one and the file is required.
set -eu

if [ $# -ne 2 ]; then
  echo "usage: $0 prerelease|highlights-required X.Y.Z" >&2
  exit 2
fi
cmd=$1
version=$2
if ! [[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "version '$version' is not strict SemVer X.Y.Z" >&2
  exit 2
fi

major=${version%%.*}
rest=${version#*.}
minor=${rest%%.*}

# from_040 is 1 when the version is 0.4.0 or later (numeric, so 0.10.0 is).
from_040=0
if [ "$major" -gt 0 ] || [ "$minor" -ge 4 ]; then
  from_040=1
fi

case "$cmd" in
  prerelease) [ "$from_040" = 0 ] ;;
  highlights-required) [ "$from_040" = 1 ] ;;
  *)
    echo "unknown command '$cmd' (want prerelease or highlights-required)" >&2
    exit 2
    ;;
esac
