#!/usr/bin/env bash
# Tests for scripts/test-time-report.sh, on fixture streams and a two-file
# tree: NOTHING here runs a Go test, reads the repository's own test files or
# touches the cgroup. The streams are the lines `go test -json` prints, written
# out by hand, and the tree is four small _test.go files in a temporary
# directory.
#
# What matters here is that a subtest is not counted as a test and not summed
# twice, that a test holding a wall-clock wait is found by reading its body and
# one that does not is left out, that a name declared in two files yields one
# row rather than one per file, and that a stream with nothing in it prints
# zeroes instead of failing.
#
# Usage: bash scripts/test-time-report_test.sh
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/test-time-report.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${TEST_TIME_REPORT_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

for tool in jq awk sort cut wc find xargs; do
  command -v "$tool" >/dev/null 2>&1 || { echo "skip: $tool is not installed" >&2; exit 0; }
done

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
    *"$2"*) ;;
    *) fail "$3: output does not hold '$2':
$1" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
    *"$2"*) fail "$3: output holds '$2' and should not:
$1" ;;
  esac
}

tree=$work/tree
mkdir -p "$tree/pkg_a" "$tree/pkg_b"
cat > "$tree/pkg_a/alpha_test.go" <<'GO'
package pkg_a

import (
	"context"
	"testing"
	"time"
)

func TestSlowWaiter(t *testing.T) {
	time.Sleep(30 * time.Second)
}

func TestQuick(t *testing.T) {
	if 1 != 1 {
		t.Fatal("unreachable")
	}
}

func TestDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = ctx
}

func TestWithSubtest(t *testing.T) {
	t.Run("inner", func(t *testing.T) {
		if 1 != 1 {
			t.Fatal("unreachable")
		}
	})
}

func TestBroken(t *testing.T) {
	t.Fatal("no")
}
GO

# The same name as pkg_a's: one row, not one per declaring file.
cat > "$tree/pkg_b/beta_test.go" <<'GO'
package pkg_b

import "testing"

func TestSlowWaiter(t *testing.T) {
	t.Fatal("the other one")
}

func TestNoisy(t *testing.T) {
	t.Log("hello")
}
GO

cat > "$work/stream.jsonl" <<'JSON'
{"Action":"start","Package":"example.com/a"}
{"Action":"run","Package":"example.com/a","Test":"TestSlowWaiter"}
{"Action":"output","Package":"example.com/a","Test":"TestSlowWaiter","Output":"=== RUN   TestSlowWaiter\n"}
{"Action":"pass","Package":"example.com/a","Test":"TestSlowWaiter","Elapsed":30.10}
{"Action":"run","Package":"example.com/a","Test":"TestQuick"}
{"Action":"pass","Package":"example.com/a","Test":"TestQuick","Elapsed":0.01}
{"Action":"run","Package":"example.com/a","Test":"TestDeadline"}
{"Action":"pass","Package":"example.com/a","Test":"TestDeadline","Elapsed":0.02}
{"Action":"run","Package":"example.com/a","Test":"TestWithSubtest"}
{"Action":"run","Package":"example.com/a","Test":"TestWithSubtest/inner"}
{"Action":"pass","Package":"example.com/a","Test":"TestWithSubtest/inner","Elapsed":0.50}
{"Action":"pass","Package":"example.com/a","Test":"TestWithSubtest","Elapsed":0.51}
{"Action":"fail","Package":"example.com/a","Test":"TestBroken","Elapsed":1.00}
{"Action":"fail","Package":"example.com/a","Elapsed":32.00}
{"Action":"pass","Package":"example.com/b","Test":"TestNoisy","Elapsed":2.00}
{"Action":"pass","Package":"example.com/b","Elapsed":2.10}
JSON

# --- the whole report, on the fixture stream and tree -----------------------

out=$(bash "$script" --source-root "$tree" --peak-mib 4096 "$work/stream.jsonl")

# section NAME prints the lines of one "== ... ==" block, so a name checked in
# one table cannot be an accident of another.
section() { # output marker
  printf '%s\n' "$1" | awk -v m="$2" '/^== / {f = index($0, m) > 0; next} f'
}

expect_contains "$out" "tests:          6" "six top-level tests, subtests not counted"
expect_contains "$out" "test seconds:   33.64" "top-level elapsed summed, a subtest not added twice"
expect_contains "$out" "package seconds: 34.10" "the packages' own reported run time"
expect_contains "$out" "failed:         1 tests, 1 packages" "the failed test and the failed package"

# The slowest table: the waiter first, with its file.
slowest=$(section "$out" "slowest tests")
first_row=$(printf '%s\n' "$slowest" | grep -m 1 -E '^ +[0-9]' || true)
expect_contains "$first_row" "TestSlowWaiter" "the slowest test is the waiter"
expect_contains "$first_row" "30.10" "with its measured time"
expect_contains "$first_row" "example.com/a" "and its package"
expect_contains "$first_row" "pkg_a/alpha_test.go" "and its file"

# A subtest is a row -- it can be the slow thing -- and it is read in its
# parent's file, since only the parent has a `func` line. It is not a test of
# its own: the counts above hold the parent alone.
subtest_row=$(printf '%s\n' "$slowest" | grep 'TestWithSubtest/inner' || true)
expect_contains "$subtest_row" "0.50" "the subtest's own measured time is a row"
expect_contains "$subtest_row" "pkg_a/alpha_test.go" "the subtest is read in its parent's file"

# The same name in two files is one row: the file column names one of them.
slowest_name_rows=$(printf '%s\n' "$slowest" | grep -c 'TestSlowWaiter' || true)
[ "$slowest_name_rows" -eq 1 ] || fail "a name declared in two files yields $slowest_name_rows rows, want 1"

# Time per package.
packages=$(section "$out" "time per package")
expect_contains "$packages" "example.com/a" "the first package is listed"
expect_contains "$packages" "example.com/b" "the second package is listed"
pkg_a_row=$(printf '%s\n' "$packages" | grep 'example.com/a$' || true)
expect_contains "$pkg_a_row" "5" "example.com/a's five tests are counted"
expect_contains "$pkg_a_row" "31.64" "example.com/a's five test seconds are summed"
expect_contains "$pkg_a_row" "32.00" "example.com/a's own reported run time is its column"

# The waits: the sleeper and the deadline holder, by reading their bodies.
expect_contains "$out" "tests holding a wait: 2" "TestSlowWaiter and TestDeadline hold a wait"
expect_contains "$out" "measured in them:     30.12 seconds (89.5% of all test seconds)" "the wait share is computed"
expect_contains "$out" "4096 MiB (recorded)" "a recorded peak is printed"

# A test with no wait primitive is not in the waits table, though the deadline
# holder is: the count above is what pins that, and here each row is checked to
# carry its measured time.
waits=$(section "$out" "tests that hold a wall-clock wait")
wait_rows=$(printf '%s\n' "$waits" | grep -c -E '^ +[0-9]' || true)
[ "$wait_rows" -eq 2 ] || fail "the waits table holds $wait_rows rows, want 2"
expect_contains "$(printf '%s\n' "$waits" | grep TestSlowWaiter)" "30.10" "the waiter's measured time is in the waits table"
expect_contains "$(printf '%s\n' "$waits" | grep TestDeadline)" "0.02" "the deadline holder's measured time is in the waits table"

# --- the flags --------------------------------------------------------------

top1=$(bash "$script" --source-root "$tree" --peak-mib 1 --top 1 "$work/stream.jsonl")
rows=$(printf '%s\n' "$(section "$top1" "slowest tests")" | grep -cE 'alpha_test.go|beta_test.go' || true)
[ "$rows" -eq 1 ] || fail "--top 1 printed $rows test rows, want 1"

skipped=$(bash "$script" --source-root - --peak-mib 1 "$work/stream.jsonl")
expect_contains "$skipped" "(no test body holds a wait primitive, or no source root was scanned)" "--source-root - skips the scan"
expect_contains "$skipped" "tests:          6" "the tables are still built without a source root"

# --- a stream with nothing in it -------------------------------------------

: > "$work/empty.jsonl"
empty=$(bash "$script" --source-root "$tree" --peak-mib 1 "$work/empty.jsonl")
expect_contains "$empty" "tests:          0" "an empty stream reports no tests"
expect_contains "$empty" "test seconds:   0.00" "an empty stream reports no seconds"
expect_contains "$empty" "failed:         0 tests, 0 packages" "an empty stream reports no failures"

# A stream whose package never ran a test -- a build failure -- still reports.
printf '%s\n' '{"Action":"build-fail","Package":"example.com/a"}' > "$work/broken.jsonl"
broken=$(bash "$script" --source-root "$tree" --peak-mib 1 "$work/broken.jsonl")
expect_contains "$broken" "tests:          0" "a build failure has no tests"
expect_contains "$broken" "1 MiB (recorded)" "the recorded peak is printed for a stream with no tests, too"

# --- two streams at once, as verify produces three --------------------------

printf '%s\n' \
  '{"Action":"fail","Package":"example.com/b","Test":"TestNoisy","Elapsed":1.00}' \
  '{"Action":"fail","Package":"example.com/b","Elapsed":1.10}' > "$work/second.jsonl"
two=$(bash "$script" --source-root "$tree" --peak-mib 1 "$work/stream.jsonl" "$work/second.jsonl")
expect_contains "$two" "streams:        2" "both streams are read"
expect_contains "$two" "failed:         2 tests, 2 packages" "failures from both are counted"

if [ "$failures" -ne 0 ]; then
  echo "test-time-report_test: $failures case(s) failed" >&2
  exit 1
fi
echo "test-time-report_test: ok"
