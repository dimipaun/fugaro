#!/usr/bin/env bash
# scripts/test-time-report.sh -- where a verify step's test time goes, from the
# `go test -json` streams the step itself produced.
#
# The number a run's verify step pays is not one number: it is the compile and
# link of each test binary, the tests that are slow for their own reasons, and
# the tests that sit out a wall clock -- a `time.Sleep`, a 30s or 60s deadline,
# a polling loop. This script reads the streams, so the split is measured
# rather than guessed, and the same script measures it again after a change.
#
# Usage:
#   go test -json ... > stream.jsonl        # once per invocation, as verify runs them
#   bash scripts/test-time-report.sh [flags] stream.jsonl [stream.jsonl ...]
#
# Flags:
#   --source-root DIR  the checkout the tests were read from; the sleep/wait
#                      table is built by reading its *_test.go files (default:
#                      the repository root this script lives in). "-" skips the
#                      scan, for a stream from a tree that is not here.
#   --top N            how many rows the slowest-tests table holds (default 30).
#   --peak-mib N       a peak memory in MiB to print as the recorded figure
#                      (verify's own line reads the cgroup high-water; a number
#                      kept from that run is what a later reader wants).
#
# What it prints: the slowest tests with their package and their file, the time
# per package, the tests that hold a wall-clock wait with their measured time
# and its share of the whole, and the container's peak memory.
#
# The wait scan is a heuristic over the test bodies, and its primitives are
# listed below rather than guessed: a body that holds one is counted whether or
# not it actually sat out the wait, because what the table is for is finding
# the tests whose length is a clock and not work. A test whose deadline never
# fires is in the count and shows a small measured time, which is exactly how a
# reader tells the two apart.
#
# It reads and prints only: no test is run, nothing is written outside a
# temporary directory.
#
# Everything is read from the streams and the tree named on the command line,
# so a case in scripts/test-time-report_test.sh can drive it with fixture lines
# and a two-file tree.
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source_root=$(cd "$here/.." && pwd)
top=30
peak_mib=

while [ $# -gt 0 ]; do
  case "$1" in
    --source-root) source_root=${2:-}; shift 2 ;;
    --top) top=${2:-}; shift 2 ;;
    --peak-mib) peak_mib=${2:-}; shift 2 ;;
    -h|--help) sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    --) shift; break ;;
    -*) echo "test-time-report: unknown flag $1" >&2; exit 2 ;;
    *) break ;;
  esac
done

if [ "$top" -lt 1 ] 2>/dev/null; then echo "test-time-report: --top needs a number" >&2; exit 2; fi
if [ $# -eq 0 ] && [ -t 0 ]; then
  echo "usage: test-time-report.sh [flags] stream.jsonl [...]" >&2
  exit 2
fi

command -v jq >/dev/null 2>&1 || { echo "test-time-report: jq is not installed" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# One pass over every stream, into three tables. Reading them is a streaming
# reduce: a stream of a whole tree is hundreds of megabytes and jq -s would
# hold all of it.
#
#   tests.tsv     elapsed <TAB> package <TAB> test     (one row per test result)
#   packages.tsv  elapsed <TAB> package                (the package's own run)
#   failures.tsv  package <TAB> test                   (the tests that failed)
if [ $# -gt 0 ]; then
  jq -r '
    select(.Action == "pass" or .Action == "fail" or .Action == "skip")
    | [ (.Elapsed // 0), .Action, (.Package // "-"), (.Test // "") ] | @tsv
  ' "$@" > "$work/all.tsv"
else
  jq -r '
    select(.Action == "pass" or .Action == "fail" or .Action == "skip")
    | [ (.Elapsed // 0), .Action, (.Package // "-"), (.Test // "") ] | @tsv
  ' > "$work/all.tsv"
fi

awk -F'\t' '$4 != "" {print $3 "\t" $4 "\t" $1}' "$work/all.tsv" > "$work/tests.tsv"
awk -F'\t' '$4 == "" {print $3 "\t" $1}'          "$work/all.tsv" > "$work/packages.tsv"

# A subtest's name is its parent's plus a slash. Only a top-level test counts as
# one test, and only a top-level test's elapsed is summed: a parent's elapsed
# already holds every subtest's, so summing both would count the same seconds
# twice.
awk -F'\t' '$2 !~ /\// {print}' "$work/tests.tsv" > "$work/toplevel.tsv"

# Test -> file, from the tree the streams came from: the file is the column a
# reader opens, and the wait scan needs the body. Names are the file's own
# `func TestX(` lines, so a test under the root is found without compiling
# anything. Two files may declare the same test name (docs tests do): the first
# in path order wins, which the header says out loud rather than fanning one row
# out into one per declaring file. The scan is sorted before it is read, because
# `find`'s own order is the filesystem's: the same checkout would otherwise name
# a different file on two machines, and a measurement that moves with the
# filesystem is not one.
: > "$work/files.tsv"
if [ "$source_root" != "-" ] && [ -d "$source_root" ]; then
  find "$source_root" -name '*_test.go' -type f | LC_ALL=C sort \
    | xargs -r -d '\n' awk '
        /^func (Test|Example|Benchmark)[A-Za-z0-9_]*\(/ {
          name = $2
          sub(/\(.*/, "", name)
          print name "\t" FILENAME
        }
      ' >> "$work/files.tsv" || true
  awk -F'\t' '!seen[$1]++' "$work/files.tsv" > "$work/files-uniq.tsv"
  mv "$work/files-uniq.tsv" "$work/files.tsv"
fi

# The wait scan: for every test function, whether its own body holds a
# wall-clock wait primitive. The body is the lines from its `func` line to the
# next line that is a lone "}" in column 0, which is how gofmt writes a
# function's end.
: > "$work/waits.tsv"
if [ -s "$work/files.tsv" ]; then
  # The list is separated by NULs, and read with `xargs -0`, as the scan above
  # is: `xargs -d` is GNU's alone, and on a BSD xargs the scan it fed came back
  # empty with nothing said. `tr` makes the same list out of the sorted lines,
  # since a NUL-separated `sort` is GNU's alone too.
  cut -f2 "$work/files.tsv" | sort -u | tr '\n' '\0' > "$work/testfiles.nul"
  xargs -0 -r awk '
    # Wall-clock waits. A body holding one of these is a body whose length is a
    # clock: time.Sleep and time.After are the plain ones, the timer and ticker
    # constructors the same in another shape, and WithTimeout/WithDeadline are
    # the 30s and 60s deadlines a test that polls its context sits out.
    function iswait(line) {
      return line ~ /time\.Sleep\(/ \
          || line ~ /time\.After\(/ \
          || line ~ /time\.NewTimer\(/ \
          || line ~ /time\.NewTicker\(/ \
          || line ~ /time\.AfterFunc\(/ \
          || line ~ /context\.WithTimeout\(/ \
          || line ~ /context\.WithDeadline\(/ \
          || line ~ /time\.Until\(/ \
          || line ~ /time\.Tick\(/
    }
    /^func (Test|Example|Benchmark)[A-Za-z0-9_]*\(/ {
      if (name != "" && wait) print name "\t" file
      name = $2; sub(/\(.*/, "", name); file = FILENAME; body = 1
      # The func line itself is read too: gofmt keeps a body on the lines below
      # it, and a hand-written one-liner must not be missed for that.
      wait = iswait($0)
      next
    }
    body && /^}/ {
      if (name != "" && wait) { print name "\t" file; wait = 0 }
      name = ""; body = 0
      next
    }
    body { if (iswait($0)) wait = 1 }
    END { if (name != "" && wait) print name "\t" file }
  ' < "$work/testfiles.nul" > "$work/waits.tsv" || {
    echo "test-time-report: the wait scan could not read the test file list" >&2
    exit 1
  }
fi

# --- the tables -------------------------------------------------------------

secs() { awk -v v="$1" 'BEGIN { printf "%.2f", v }'; }

total_tests=$(wc -l < "$work/toplevel.tsv" | tr -d ' ')
total_seconds=$(awk -F'\t' '{s += $3} END {printf "%.2f", s + 0}' "$work/toplevel.tsv")
stream_seconds=$(awk -F'\t' '{s += $2} END {printf "%.2f", s + 0}' "$work/packages.tsv")
# A failed test and a failed package are two different things and a reader
# needs them apart: the first names something to fix, the second is the build.
failed_tests=$(awk -F'\t' '$2 == "fail" && $4 != "" {n++} END {print n + 0}' "$work/all.tsv")
failed_pkgs=$(awk -F'\t' '$2 == "fail" && $4 == "" {n++} END {print n + 0}' "$work/all.tsv")

echo "== summary =="
printf '  streams:        %d\n' "$#"
printf '  tests:          %s\n' "$total_tests"
printf '  test seconds:   %s (summed over top-level tests)\n' "$(secs "$total_seconds")"
printf '  package seconds: %s (each package as it reported its own run)\n' "$(secs "$stream_seconds")"
printf '  failed:         %s tests, %s packages\n' "$failed_tests" "$failed_pkgs"

# A test's file, by name, without a join: join's default output order is easy
# to get subtly wrong, and a name declared in two files would fan a row out.
# The sentinel keeps the NR==FNR idiom honest when nothing was scanned: an
# empty first file would make the second one look like the first.
[ -s "$work/files.tsv" ] || printf '\t-\n' > "$work/files.tsv"

# Each table is cut to --top rows by awk and never by head: head closes the
# pipe as soon as it has its rows, the sort behind it dies of SIGPIPE, and
# pipefail turns that into this script's own death partway through the report.
echo
echo "== $top slowest tests =="
printf '  %9s  %-38s %-46s %s\n' seconds package test file
awk -F'\t' '
  NR == FNR { if ($1 != "") file[$1] = $2; next }
  {
    # A subtest is a row of its own, because it can be the slow thing, and it
    # is read in the file of the test that holds it: only the parent has the
    # func line that names a file.
    name = $2; parent = name; sub(/\/.*/, "", parent)
    f = (file[name] != "" ? file[name] : file[parent])
    printf "%s\t%s\t%s\t%s\n", $3, $1, name, (f == "" ? "-" : f)
  }
' "$work/files.tsv" "$work/tests.tsv" | sort -rn \
  | awk -F'\t' -v n="$top" 'NR <= n {printf "  %9.2f  %-38s %-46s %s\n", $1, $2, $3, $4}'

echo
echo "== time per package =="
printf '  %9s  %7s  %12s  %s\n' elapsed tests test-seconds package
awk -F'\t' '
  { count[$1]++; sum[$1] += $3 }
  END { for (p in count) printf "%s\t%d\t%.2f\n", p, count[p], sum[p] }
' "$work/toplevel.tsv" | sort -k1,1 > "$work/agg.tsv"
[ -s "$work/packages.tsv" ] || printf '\t0\n' > "$work/packages.tsv"
awk -F'\t' '
  # A package is named by one row per stream that ran it, and the documented
  # invocation runs internal/cli twice, once per half of its tests. The two
  # columns beside this one are summed over every stream, so this one is
  # summed the same way and the row agrees with the summary line: keeping the
  # last stream alone would put one half of the runs beside both halves of
  # the tests and of the seconds.
  NR == FNR { if ($1 != "") pkg[$1] += $2; next }
  { printf "%.2f\t%d\t%.2f\t%s\n", (pkg[$1] == "" ? 0 : pkg[$1]), $2, $3, $1 }
' "$work/packages.tsv" "$work/agg.tsv" | sort -rn \
  | awk -F'\t' '{printf "  %9.2f  %7d  %12.2f  %s\n", $1, $2, $3, $4}'

echo
echo "== tests that hold a wall-clock wait =="
if [ ! -s "$work/waits.tsv" ]; then
  echo "  (no test body holds a wait primitive, or no source root was scanned)"
else
  awk -F'\t' '
    NR == FNR { file[$1] = $2; next }
    file[$2] != "" { printf "%s\t%s\t%s\t%s\n", file[$2], $2, $1, $3 }
  ' "$work/waits.tsv" "$work/tests.tsv" > "$work/waitrows.tsv"
  wait_count=$(cut -f2 "$work/waitrows.tsv" | sort -u | wc -l | tr -d ' ')
  wait_seconds=$(awk -F'\t' '{s += $4} END {printf "%.2f", s + 0}' "$work/waitrows.tsv")
  printf '  tests holding a wait: %s\n' "$wait_count"
  printf '  measured in them:     %s seconds (%s%% of all test seconds)\n' \
    "$(secs "$wait_seconds")" \
    "$(awk -v w="$wait_seconds" -v t="$total_seconds" 'BEGIN { if (t > 0) printf "%.1f", 100 * w / t; else print "0.0" }')"
  echo
  printf '  %9s  %-38s %-46s %s\n' seconds package test file
  sort -k4,4rn "$work/waitrows.tsv" \
    | awk -F'\t' -v n="$top" 'NR <= n {printf "  %9.2f  %-38s %-46s %s\n", $4, $3, $2, $1}'
fi

echo
echo "== peak memory =="
if [ -n "$peak_mib" ]; then
  printf '  %s MiB (recorded)\n' "$peak_mib"
else
  peak=
  for f in /sys/fs/cgroup/memory.peak /sys/fs/cgroup/memory/memory.max_usage_in_bytes; do
    if [ -r "$f" ]; then peak="$(( $(cat "$f") / 1048576 )) MiB ($f)"; break; fi
  done
  if [ -n "$peak" ]; then printf '  %s\n' "$peak"; else printf '  not reported by this container\n'; fi
fi
