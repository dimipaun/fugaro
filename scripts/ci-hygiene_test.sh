#!/usr/bin/env bash
# Tests for scripts/ci-hygiene.sh. Every case runs the script in a git
# repository of its own, built in a temp directory and holding nothing but the
# fixtures the case is about, so no case reads this repository's tree and no
# case depends on another: the script is run under `timeout`, and the whole
# file finishes in seconds.
#
# The last three cases are mutation checks, one per rule: each runs a copy of
# the script whose rule body has been replaced by `:` and requires the case
# that covers that rule to stop seeing its violation. That is what says the
# cases above test the rules rather than the fixture being in the way.
#
# Usage: bash scripts/ci-hygiene_test.sh   (also run by scripts/ci_hygiene_test.go)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

# A fresh fixture repository holding a copy of the script under test and one
# tracked file that no rule has anything to say about.
setup() {
  rm -rf "${work:?}/repo" "${work:?}/nowhere"
  mkdir -p "$work/repo/scripts" "$work/nowhere"
  cp "$here/ci-hygiene.sh" "$work/repo/scripts/"
  printf 'package fixture\n' >"$work/repo/plain.go"
  git -C "$work/repo" -c init.defaultBranch=main init -q
}

# write PATH CONTENT: writes a file into the fixture repository.
write() {
  mkdir -p "$(dirname "$work/repo/$1")"
  printf '%s\n' "$2" >"$work/repo/$1"
}

# track: stages everything, which is what the check reads: `git ls-files` lists
# the index, and staging needs no identity where committing would.
track() { git -C "$work/repo" add -A; }

# run [DIR] [SCRIPT]: runs the script, from the fixture repository unless a
# directory is named; sets $status and $out.
run() {
  local dir=${1:-$work/repo} script=${2:-scripts/ci-hygiene.sh}
  set +e
  out=$(cd "$dir" && timeout 20 bash "$script" 2>&1)
  status=$?
  set -e
}

# mutate FUNCTION DEST: writes a copy of the script in which FUNCTION's body is
# `:`, so the rule it implements is off. The function has to open on a line of
# its own and close with `}` at column 0, which is how they are written.
mutate() {
  awk -v fn="$2" '
    $0 == fn "() {" { skip = 1; print fn "() { :; }"; next }
    skip && $0 == "}" { skip = 0; next }
    !skip { print }
  ' "$here/ci-hygiene.sh" >"$1"
}

# 1. A tree with nothing to report: rule 1 passes clean, and the report-only
# rules are silent because there is no Go test here at all.
setup
track
run
[[ $status -eq 0 ]] || fail "a clean tree exited $status: $out"
[[ $out == *"ci-hygiene: clean"* ]] || fail "a clean tree does not say so: $out"
[[ $out != *"warn:"* ]] || fail "a clean tree warned: $out"

# 2. Rule 1: every leftover shape, tracked, is a violation -- one line naming
# the path and the rule, and an exit that fails the build.
for name in "pkg/edited.go-E" "pkg/edited_test.go-E" "notes.orig" "notes.bak" "notes.rej" "notes~"; do
  setup
  write "$name" "leftover"
  track
  run
  [[ $status -eq 1 ]] || fail "a tracked $name exited $status: $out"
  [[ $out == *"$name:0: stray-backup-file:"* ]] || fail "a tracked $name was not named as a stray-backup-file violation: $out"
done

# 3. Rule 1 reads the suffix at the end of the name: a file that merely
# mentions one is a file someone meant to commit.
setup
for name in "pkg/a-E.txt" "pkg/b.bak.go" "pkg/c~d.go" "pkg/notes.rej.txt" "pkg/orig"; do
  write "$name" "kept"
done
track
run
[[ $status -eq 0 ]] || fail "a name holding a leftover suffix in the middle exited $status: $out"
! grep -q stray-backup-file <<<"$out" || fail "a name holding a leftover suffix in the middle was flagged: $out"

# 4. Rule 2: a literal-year instant in a function that reads the process clock
# warns (and does not fail), naming the line and the signal it found.
setup
write "pkg/clock_test.go" 'package pkg

func TestAged(t *testing.T) {
	start := time.Now()
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if at.Before(start) {
		t.Fatal("stale")
	}
}'
track
run
[[ $status -eq 0 ]] || fail "a rule 2 hit exited $status: $out"
[[ $out == *"warn: pkg/clock_test.go:5: fixed-clock-instant:"* ]] || fail "a literal instant beside time.Now() was not warned about at its line: $out"

# 5. Rule 2 is modeled on the failure of 2026-11-13, where the fixture was
# built with no clock ABOVE the instant: that shape warns too, which is the
# whole point of reading the window on both sides of the line.
setup
write "pkg/rotate_test.go" 'package pkg

func TestRotateReturnsAFreshPair(t *testing.T) {
	s := newFakeStore(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	RotateAt(s, now)
}'
track
run
[[ $out == *"warn: pkg/rotate_test.go:5: fixed-clock-instant:"* ]] || fail "a clock-less fixture above the instant was not warned about: $out"

# 6. Rule 2 says nothing about an instant that no signal in the function ties
# to the process clock, and nothing about the fix: a fixture handed its clock.
setup
write "pkg/quiet_test.go" 'package pkg

func TestComparesTwoInstants(t *testing.T) {
	from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	if !from.Before(to) {
		t.Fatal("the fixture is backwards")
	}
}

func TestOwnsItsClock(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := newFakeStoreOver(t, func() time.Time { return now }, nil)
	RotateAt(s, now)
}'
track
run
! grep -q fixed-clock-instant <<<"$out" || fail "a literal instant with no process clock in its function was warned about: $out"

# 7. Rule 3: a bound under 60s warns, at or above it does not -- an assertion
# waiting for a stall is not the wall-clock bound this rule is about.
setup
write "pkg/bounded_test.go" 'package pkg

func TestBounded(t *testing.T) {
	start := time.Now()
	if d := time.Since(start); d > 4*time.Second {
		t.Fatal(d)
	}
	if e := time.Since(start); e > 1500*time.Millisecond {
		t.Fatal(e)
	}
	if f := time.Since(start); f >= 60*time.Second {
		t.Fatal(f)
	}
	if g := time.Since(start); g > 90*time.Second {
		t.Fatal(g)
	}
}'
track
run
[[ $status -eq 0 ]] || fail "a rule 3 hit exited $status: $out"
[[ $out == *"warn: pkg/bounded_test.go:5: wall-clock-bound: an assertion bounds time.Since(...) by 4*time.Second"* ]] || fail "a bound of 4s was not warned about at its line: $out"
[[ $out == *"warn: pkg/bounded_test.go:8: wall-clock-bound: an assertion bounds time.Since(...) by 1500*time.Millisecond"* ]] || fail "a bound of 1500ms was not warned about: $out"
! grep -q "bounded_test.go:11" <<<"$out" || fail "a bound of 60s was warned about: $out"
! grep -q "bounded_test.go:14" <<<"$out" || fail "a bound of 90s was warned about: $out"

# 8. Rules 2 and 3 are about tests: the same lines in a file Go builds for real
# are a program's own business.
setup
write "pkg/worker.go" 'package pkg

func work() {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	start := time.Now()
	if time.Since(start) > 4*time.Second {
		_ = now
	}
}'
track
run
! grep -qE 'fixed-clock-instant|wall-clock-bound' <<<"$out" || fail "a non-test file was reported on: $out"

# 9. Outside a git work tree there is nothing to check, and that is not a pass:
# the exit status says the check did not run.
setup
run "$work/nowhere" "$work/repo/scripts/ci-hygiene.sh"
[[ $status -eq 2 ]] || fail "a directory that is not a work tree exited $status: $out"
[[ $out == *"not inside a git work tree"* ]] || fail "a directory that is not a work tree was not reported as such: $out"

# 10-12. Mutation checks: with the rule's body replaced by `:`, the case above
# stops seeing what it asserts. Each of those cases therefore fails when its
# rule is off, which is what makes them the rule's tests.
setup
write "pkg/edited.go-E" "leftover"
write "pkg/clock_test.go" 'package pkg

func TestAged(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	_ = time.Now()
	_ = now
}'
write "pkg/bounded_test.go" 'package pkg

func TestBounded(t *testing.T) {
	start := time.Now()
	if time.Since(start) > 4*time.Second {
		t.Fatal("slow")
	}
}'
track
for rule in rule_stray_backup_files rule_fixed_clock_instants rule_wall_clock_bounds; do
  mutate "$work/repo/scripts/ci-hygiene.sh" "$rule"
  run
  case "$rule" in
    rule_stray_backup_files)
      [[ $status -eq 0 ]] || fail "with $rule disabled the run still exited $status: $out"
      ! grep -q stray-backup-file <<<"$out" || fail "with $rule disabled a stray file was still a violation: the case for it does not test the rule"
      ;;
    rule_fixed_clock_instants)
      ! grep -q fixed-clock-instant <<<"$out" || fail "with $rule disabled a fixed instant was still warned about: the case for it does not test the rule"
      ;;
    rule_wall_clock_bounds)
      ! grep -q wall-clock-bound <<<"$out" || fail "with $rule disabled a wall-clock bound was still warned about: the case for it does not test the rule"
      ;;
  esac
  # The other rules keep working on the same tree, so the mutation is the one
  # function and not the whole script.
  case "$rule" in
    rule_stray_backup_files) grep -q wall-clock-bound <<<"$out" || fail "disabling $rule silenced rule 3 as well: the mutation is not the one function" ;;
    rule_fixed_clock_instants) grep -q stray-backup-file <<<"$out" || fail "disabling $rule silenced rule 1 as well: the mutation is not the one function" ;;
    rule_wall_clock_bounds) grep -q stray-backup-file <<<"$out" || fail "disabling $rule silenced rule 1 as well: the mutation is not the one function" ;;
  esac
  cp "$here/ci-hygiene.sh" "$work/repo/scripts/ci-hygiene.sh"
done

if [[ $failures -gt 0 ]]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "ci-hygiene_test: ok"
