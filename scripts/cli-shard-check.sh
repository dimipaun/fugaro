#!/usr/bin/env bash
# cli-shard-check.sh runs one internal/cli shard the way CI's test-shard job
# does — the package's tests listed, filtered by a stable hash of the name mod
# N, and the selected names handed to `go test -run` as one anchored
# alternation — so a subset that hangs is found here rather than on a runner
# that has to be waited out for forty minutes.
#
# Usage: scripts/cli-shard-check.sh N INDEX [TIMEOUT] [--race]
#        scripts/cli-shard-check.sh N all   [TIMEOUT] [--race]
#
# N is the shard count, INDEX one shard (or `all` for every shard in turn),
# TIMEOUT the `go test -timeout` bound (default 10m), and --race adds the race
# detector, which CI always passes and a timing run leaves off.
#
# The hash is the CI job's own: `printf '%s' "$name" | cksum | cut -d' ' -f1`
# modulo N, so a run of this script selects exactly the names CI would, and a
# new test lands in a shard on its own with none dropped or run twice.
set -euo pipefail

pkg=./internal/cli

if [ "$#" -lt 2 ]; then
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

n=$1
index=$2
timeout=${3:-10m}
race=""
for arg in "$@"; do
  if [ "$arg" = "--race" ]; then race="-race"; fi
done

case "$n" in
  ''|*[!0-9]*) echo "shard count must be a number, got '$n'" >&2; exit 2 ;;
esac
[ "$n" -ge 1 ] || { echo "shard count must be at least 1" >&2; exit 2; }
if [ "$index" != "all" ]; then
  case "$index" in
    ''|*[!0-9]*) echo "shard index must be a number or 'all', got '$index'" >&2; exit 2 ;;
  esac
  [ "$index" -lt "$n" ] || { echo "shard index $index is outside 0..$((n - 1))" >&2; exit 2; }
fi

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

all=$(mktemp)
trap 'rm -f "$all"' EXIT
go test -list '.*' "$pkg" | grep -E '^(Test|Example|Fuzz)' | sort > "$all"
total=$(wc -l < "$all")
echo "internal/cli: $total tests, $n shard(s)"

run_one() {
  local idx=$1
  local mine
  mine=$(mktemp)
  while read -r name; do
    if [ $(( $(printf '%s' "$name" | cksum | cut -d' ' -f1) % n )) -eq "$idx" ]; then
      echo "$name"
    fi
  done < "$all" > "$mine"
  local count
  count=$(wc -l < "$mine")
  echo "shard $idx of $n: $count of $total tests"
  if [ "$count" -eq 0 ]; then
    rm -f "$mine"
    return 0
  fi
  local run
  run="^($(paste -sd'|' "$mine"))\$"
  rm -f "$mine"
  local start end rc=0
  start=$(date +%s)
  go test $race -count=1 -timeout "$timeout" -run "$run" "$pkg" || rc=$?
  end=$(date +%s)
  if [ "$rc" -eq 0 ]; then
    echo "shard $idx of $n: PASS in $((end - start))s"
  else
    echo "shard $idx of $n: FAIL (exit $rc) after $((end - start))s" >&2
  fi
  return "$rc"
}

if [ "$index" = "all" ]; then
  failed=0
  for i in $(seq 0 $((n - 1))); do
    run_one "$i" || failed=1
  done
  exit "$failed"
fi
run_one "$index"
