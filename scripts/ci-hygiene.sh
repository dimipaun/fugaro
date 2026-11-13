#!/usr/bin/env bash
# ci-hygiene.sh: the mechanical hygiene checks on the current tree, run from
# the repository root (the `hygiene` step of CI's `check` job runs it there).
# It exists because two of the three ways `main`'s CI went red over
# 2026-11-10..13 were cheap to catch without compiling anything:
#
#   - A pull request committed macOS `sed -i`'s backup files (`portalauth.go-E`,
#     `portalauth_test.go-E`), which are tracked, carry no licence notice and
#     turned `TestEEFilesCarryTheNotice` and `TestDocsEELicensed` red on `main`
#     for a file nobody meant to commit. Rule 1 catches every shape of that
#     leftover.
#   - `TestRotateReturnsAFreshPair` minted and rotated at a fixed
#     `time.Date(2026, 11, 6, ...)` instant while its store read the process
#     clock, so it passed for a week and then failed once the family's 7-day
#     idle bound had passed for real (2026-11-13, #2543). Rule 2 points at a
#     literal-year instant in a test whose function reaches the process clock.
#   - `TestRedactPassesAreBounded` measured wall time against an absolute limit
#     and failed under `-race` on a shared runner. Rule 3 points at an assertion
#     that bounds `time.Since(...)` by a literal duration under a minute.
#
# Exit status: 0 when the tree is clean, 1 when a rule is violated (one line
# per violation, `path:line: rule: detail`, the rule's own name in the third
# field), 2 when it cannot run at all. Rules 2 and 3 print `warn:` lines and do
# not change the exit status: both are heuristics whose hits today are mostly
# fixtures that DO pin their clock (a `newX(t)` line beside a literal instant
# says nothing about the clock the fixture reads), so they are a report for the
# owner to read rather than a build to fail. Rule 1 is exact and does fail the
# build. Promote either of the other two by making its function count into
# `violations` instead of `warnings`, once the tree it reports on is empty.
#
# A rule 1 violation has no line, and prints 0 there.
set -uo pipefail

root=$(git rev-parse --show-toplevel 2>/dev/null)
if [ -z "$root" ]; then
  echo "ci-hygiene: not inside a git work tree; run it from the repository root" >&2
  exit 2
fi
cd "$root" || exit 2

violations=0
warnings=0

# violation PATH LINE RULE DETAIL: one line the CI log can be read for, with
# the rule's own name so a reader knows which check to look up here.
violation() {
  printf '%s:%s: %s: %s\n' "$1" "$2" "$3" "$4"
  violations=$((violations + 1))
}

# warn PATH LINE RULE DETAIL: the same line, marked as a report-only one.
warn() {
  printf 'warn: %s:%s: %s: %s\n' "$1" "$2" "$3" "$4"
  warnings=$((warnings + 1))
}

# Rule 1: no tracked editor or patch leftover. `git ls-files` reads the index,
# so this is exactly what a commit would carry: an untracked leftover on a
# developer's disk is their own business, and one that is staged is this rule's.
# `-z` because a file name can hold a newline and the suffix is at its end,
# which is the one place a line-oriented read cannot tell.
rule_stray_backup_files() {
  local path
  while IFS= read -r -d '' path; do
    case "$path" in
      *-E|*.orig|*.bak|*.rej|*~)
        violation "$path" 0 stray-backup-file \
          "a tracked leftover of a local edit (its name ends in -E, .orig, .bak, .rej or ~), not a file anyone meant to commit"
        ;;
    esac
  done < <(git ls-files -z)
}

# Rule 2: no test that builds a literal-year instant into a function that also
# reaches the process clock. The window is the enclosing top-level function,
# cut to 40 lines either side of the instant: the `next 40 lines` this rule was
# asked for is the half of it that follows, and the other half is there because
# the failure it is modeled on put the clock-less fixture ABOVE the instant
# (`s := newFakeStore(t)` one line above `now := time.Date(2026, 11, 6, ...)`).
# Two signals count as reaching the process clock, and the printed line says
# which one fired:
#
#   - `time.Now()` in that window, which is the clock itself;
#   - a fixture built with nothing but the test (`newX(t)`), which is how the
#     failing test handed its store a clock it never chose. `newFakeStoreOver(t,
#     func() time.Time { return now }, nil)` is not one: a constructor given a
#     clock is the fix, not the bug.
#
# What it cannot see is a constructor that takes a clock and is handed one that
# reads the process clock somewhere else, which is why it reports rather than
# fails.
rule_fixed_clock_instants() {
  local file
  local prog='
    { line[NR] = $0 }
    END {
      n = 0
      for (i = 1; i <= NR; i++) {
        if (line[i] ~ /^func /) { n++; first[n] = i; last[n] = NR }
      }
      for (k = 1; k <= n; k++) {
        if (line[first[k]] ~ /\}/) { last[k] = first[k]; continue }
        for (j = first[k] + 1; j <= NR; j++) {
          if (line[j] == "}") { last[k] = j; break }
        }
      }
      for (k = 1; k <= n; k++) {
        for (j = first[k]; j <= last[k]; j++) {
          if (line[j] !~ /time\.Date\(20[0-9][0-9],/) continue
          lo = j - 40; if (lo < first[k]) lo = first[k]
          hi = j + 40; if (hi > last[k]) hi = last[k]
          signal = ""
          for (m = lo; m <= hi; m++) {
            if (line[m] ~ /time\.Now\(\)/) { signal = "time.Now()"; break }
          }
          if (signal == "") {
            for (m = lo; m <= hi; m++) {
              if (line[m] ~ /new[A-Za-z0-9_]*\(t\)/) { signal = "a fixture built with no clock (newX(t))"; break }
            }
          }
          if (signal == "") continue
          printf "%s\t%d\ta literal-year time.Date in a function that reaches the process clock through %s\n", path, j, signal
        }
      }
    }
  '
  while IFS= read -r file; do
    awk -v path="$file" "$prog" "$file"
  done < <(git grep -l -E 'time\.Date\(20[0-9][0-9],' -- '*_test.go' || true)
}

# Rule 3: no assertion that bounds `time.Since(...)` by a literal duration under
# 60s. Every Go package of this module runs under `-race` in CI (the `shard`
# job's `go test -race`), where a bound tight enough to be meaningful is also
# tight enough for a busy shared runner to miss; the fix, when one of these
# fails, is the shape `TestRedactPassesAreBounded` took -- compare two costs
# rather than one cost against a number. 60s is the line because a bound at or
# above it is a stall detector, which is what the longer ones here are.
rule_wall_clock_bounds() {
  local file
  local prog='
    {
      if ($0 !~ /time\.Since\(/) next
      if ($0 !~ /(<=|>=|<|>)[ \t]*[0-9]+(\.[0-9]+)?[ \t]*\*[ \t]*time\.(Nanosecond|Microsecond|Millisecond|Second)/) next
      if (!match($0, /[0-9]+(\.[0-9]+)?[ \t]*\*[ \t]*time\.(Nanosecond|Microsecond|Millisecond|Second)/)) next
      token = substr($0, RSTART, RLENGTH)
      n = token; sub(/[^0-9.].*/, "", n)
      u = token; sub(/.*time\./, "", u)
      ms = (u == "Nanosecond") ? n / 1000000 : (u == "Microsecond") ? n / 1000 : (u == "Millisecond") ? n : n * 1000
      if (ms < 60000) printf "%s\t%d\tan assertion bounds time.Since(...) by %s, under the 60s a -race runner needs\n", path, FNR, token
    }
  '
  while IFS= read -r file; do
    awk -v path="$file" "$prog" "$file"
  done < <(git grep -l -E 'time\.Since\(' -- '*_test.go' || true)
}

# A rule reports `path<TAB>line<TAB>detail` and this is what turns that into a
# line: rules 2 and 3 go through the `warn` one, so promoting either is the
# one-line change the header describes.
report_only() {  # RULE
  local path num detail
  while IFS=$'\t' read -r path num detail; do
    [ -n "${path:-}" ] || continue
    warn "$path" "$num" "$1" "$detail"
  done
}

rule_stray_backup_files
report_only fixed-clock-instant < <(rule_fixed_clock_instants)
report_only wall-clock-bound < <(rule_wall_clock_bounds)

if [ "$violations" -gt 0 ]; then
  printf 'ci-hygiene: %d violation(s), %d report-only warning(s)\n' "$violations" "$warnings"
  exit 1
fi
printf 'ci-hygiene: clean (%d report-only warning(s))\n' "$warnings"
