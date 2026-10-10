#!/bin/sh
# size-budget.sh IMAGE [MAX_MB [WARN_MB]] fails when IMAGE's uncompressed size
# (docker image inspect's .Size, in MB of 10^6 bytes) is over MAX_MB and
# warns over WARN_MB, so the base grows only by decision (design
# base-image.md section 13). It prints the size either way. MAX_MB and
# WARN_MB default to the constants below; base image Task 6 wires the images
# workflow's own CI value by passing $2/$3, not by a setting read from here.
#
# .Size is exact under Docker's classic (overlay2) image store; containerd's
# image store reports the size differently (it has no single "uncompressed
# size" the classic store's flattened image config gives), so a host running
# the containerd store would need a different measurement, not this script.
set -eu
MAX_MB=4000
WARN_MB=3500
image=${1:?usage: size-budget.sh IMAGE [MAX_MB [WARN_MB]]}
max=${2:-$MAX_MB}
warn=${3:-$WARN_MB}
bytes=$(docker image inspect --format '{{.Size}}' "$image")
case $bytes in
  ''|*[!0-9]*)
    echo "size-budget: docker image inspect --format '{{.Size}}' $image printed '$bytes', not a byte count" >&2
    exit 1 ;;
esac
mb=$((bytes / 1000000))
echo "size-budget: $image is $mb MB (budget $max MB, warn-at $warn MB)"
if [ "$mb" -gt "$max" ]; then
  echo "size-budget: $image is over its budget of $max MB: drop something, or raise MAX_MB here if the growth is a decision" >&2
  exit 1
fi
if [ "$mb" -gt "$warn" ]; then
  echo "::warning::size-budget: $image is $mb MB, over the $warn MB warning line"
fi
