#!/bin/sh
# A fake `fugaro` for scripts/release_test.go and scripts/release_rehearsal_test.go:
# the CLI `scripts/release-rehearsal.sh` builds this repository's own catalog
# entry with. It prints what the real one prints for the build the rehearsal
# reads -- the submission, with the Cloud Build id and its log, then the step's
# own line -- and, when told to, fails after the submission the way a red Cloud
# Build does. Every call is logged, so a test can hold the rehearsal to the one
# invocation it has to make.
#
# Configuration comes from the environment of the process under test:
#   FAKE_FUGARO_DIR     state directory (required): log, one line per call
#   FAKE_FUGARO_BUILD   the Cloud Build id reported (default fake-build-1)
#   FAKE_FUGARO_LOG     the log URL reported (default: that build's console URL,
#                       as --gcp-project's project)
#   FAKE_FUGARO_STATUS  0 (default) builds; anything else fails after the
#                       submission, which is the shape a failed build has (the
#                       submission is the write, so the build is named either
#                       way)
set -eu
dir=$FAKE_FUGARO_DIR
printf '%s\n' "$*" >>"$dir/log"
case $* in
  "image build go --gcp-project "*) ;;
  *)
    echo "fake-fugaro: invoked as '$*': the rehearsal makes one call, 'image build go --gcp-project <project>', and a rehearsal that asked for anything else is not the one this fake answers" >&2
    exit 64
    ;;
esac
build=${FAKE_FUGARO_BUILD:-fake-build-1}
project=""
prev=""
for arg in "$@"; do
  if [ "$prev" = --gcp-project ]; then project=$arg; fi
  prev=$arg
done
log=${FAKE_FUGARO_LOG:-https://console.cloud.google.com/cloud-build/builds/$build?project=$project}
printf 'fugaro: Cloud Build build %s of fake-registry/catalog-go:latest submitted; log: %s\n' "$build" "$log"
if [ "${FAKE_FUGARO_STATUS:-0}" != 0 ]; then
  printf 'FAIL: the catalog build of go did not finish: the smoke step exited 1\n' >&2
  exit 1
fi
printf 'built fake-registry/catalog-go@sha256:fake (Cloud Build build %s)\n' "$build"
