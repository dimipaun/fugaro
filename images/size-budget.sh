#!/bin/sh
# size-budget.sh IMAGE [MAX_MB [WARN_MB]] fails when IMAGE's uncompressed size
# (docker image inspect's .Size, in MB of 10^6 bytes) is over MAX_MB (default
# 4000) and warns over WARN_MB (default 3500), so the base grows only by
# decision (design base-image.md section 13). It prints the size either way.
set -eu
image=${1:?usage: size-budget.sh IMAGE [MAX_MB [WARN_MB]]}
max=${2:-4000}
warn=${3:-3500}
bytes=$(docker image inspect --format '{{.Size}}' "$image")
mb=$((bytes / 1000000))
echo "size-budget: $image is $mb MB (budget $max MB, warn-at $warn MB)"
if [ "$mb" -gt "$max" ]; then
  echo "size-budget: $image is over its budget of $max MB: drop something or raise the budget in images.yml on purpose" >&2
  exit 1
fi
if [ "$mb" -gt "$warn" ]; then
  echo "::warning::size-budget: $image is $mb MB, over the $warn MB warning line"
fi
