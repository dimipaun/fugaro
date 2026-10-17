#!/bin/bash
# Mutation-proves the mailbox-schema guards (mobile plan, Task P0.2).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make `npm test` fail. A guard whose mutation survives is a guard no
# test defends, which for this module -- the protocol P1.1, P3.6 and P4.1 speak
# -- is the difference between a fence and a comment.
#
#   shared/mailbox-schema/mutation-proof.sh
#
# It restores index.js with `git checkout` after each mutation, so it refuses to
# run against an uncommitted module: `git checkout` on an untracked file fails
# and the mutations would stack. Run it from a clean tree.
#
# Three things keep it from lying, because "killed" is otherwise a claim about a
# command that may never have run:
#
#   1. it finds a runner or stops, rather than reporting mutations it never ran;
#   2. **the positive control** -- the suite must PASS on the pristine module,
#      with at least one test actually run. `node --test` exits 0 over a glob
#      that matched nothing (shared/README.md says why), so a missing test file
#      would otherwise look exactly like a green suite; and
#   3. **the negative control** -- a mutation that must be caught, before any
#      real one is trusted. If the suite cannot fail, nothing below means
#      anything.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no "APPLY-FAILED"
# (a pattern that no longer matches means this script is stale, not that the
# guard is proved), and exit 0.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/mailbox-schema/index.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A mise shim for a tool whose version was never set sits on PATH and
# fails when called, which is a runner that would make every verdict below
# fiction.
runner_works() {
  node --version >/dev/null 2>&1 && npm --version >/dev/null 2>&1
}

# A runner, found rather than assumed. A working one on PATH is the ordinary
# case; this repository's base image keeps node off PATH on purpose
# (docs/base-image.md), and mise holds the toolchains, so a machine that has
# mise gets one from there rather than from any one machine's home directory.
# node@22 is the line package.json's engines declares (>=22 <23).
if ! runner_works && command -v mise >/dev/null 2>&1; then
  for tool in node@22 node; do
    dir=$(mise where "$tool" 2>/dev/null || true)
    if [ -n "$dir" ] && [ -x "$dir/bin/npm" ]; then
      PATH="$dir/bin:$PATH"
      break
    fi
  done
fi
if ! runner_works; then
  echo "REFUSING: no working node and npm (on PATH, or through mise);" >&2
  echo "          the suites below would not run, so their verdicts would be fiction." >&2
  exit 2
fi

if ! git ls-files --error-unmatch "$F" >/dev/null 2>&1; then
  echo "REFUSING: $F is not committed; git checkout could not restore it" >&2
  exit 2
fi
if ! git diff --quiet -- "$F"; then
  echo "REFUSING: $F has uncommitted changes; commit them first" >&2
  exit 2
fi

last_outcome=""
killed=0
survivors=0
unapplied=0
guards=0

# suite runs the package's tests and returns 0 only for a real green run: the
# runner exited 0, the summary reports at least one test, and nothing failed.
suite() {
  local out code tests fails
  out=$( (cd shared && npm test) 2>&1 )
  code=$?
  tests=$(printf '%s\n' "$out" | sed -n 's/^# tests \([0-9][0-9]*\)$/\1/p' | tail -1)
  fails=$(printf '%s\n' "$out" | sed -n 's/^# fail \([0-9][0-9]*\)$/\1/p' | tail -1)
  LAST_OUT=$out
  LAST_TESTS=${tests:-0}
  [ "$code" -eq 0 ] && [ "$LAST_TESTS" -ge 1 ] && [ "${fails:-1}" -eq 0 ]
}

# mutate_test applies one mutation, judges the suite against it, and restores
# the module. It counts only the guards it was asked to prove; the caller
# decides what to do with last_outcome.
mutate_test() { # name, old, [new]
  local name="$1" old="$2" new="${3-}"
  git checkout -- "$F"
  if ! python3 - "$F" "$old" "$new" <<'PY'
import sys
p, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(p).read()
assert s.count(old) == 1, ("pattern not found exactly once:", old, s.count(old))
open(p, 'w').write(s.replace(old, new))
PY
  then
    echo "APPLY-FAILED  $name"
    git checkout -- "$F"
    last_outcome=apply-failed
    unapplied=$((unapplied + 1))
    return
  fi
  if git diff --quiet -- "$F"; then
    echo "NO-OP         $name"
    git checkout -- "$F"
    last_outcome=no-op
    unapplied=$((unapplied + 1))
    return
  fi
  if suite; then
    echo "SURVIVED      $name   <-- the tests did not catch this"
    last_outcome=survived
    survivors=$((survivors + 1))
  else
    echo "killed        $name"
    last_outcome=killed
    killed=$((killed + 1))
  fi
  git checkout -- "$F"
}

# run proves one guard and counts it.
run() { # name, old, [new]
  guards=$((guards + 1))
  mutate_test "$@"
}

# 1. The positive control: the suite passes on the pristine module, over tests
#    that actually ran. Nothing below is measured against anything otherwise.
git checkout -- "$F"
if ! suite; then
  echo "REFUSING: the suite does not pass on the pristine $F ($LAST_TESTS tests ran);" >&2
  echo "          a mutation's failure could not be told from the baseline's." >&2
  printf '%s\n' "$LAST_OUT" | tail -20 >&2
  exit 2
fi
echo "control       the pristine suite passes, $LAST_TESTS tests, 0 failures"

# 2. The negative control: a validator that refuses nothing must be caught. If
#    the suite cannot fail, every "killed" below would be a coincidence.
mutate_test "control: refuse() always succeeds (must be killed)" \
  "function refuse(code, field) {" "function refuse(code, field) { return { ok: true, message: {} };"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. The value itself: not an object, an unknown field, a missing field.
run "not-an-object guard (null/array/primitive)" \
  "if (typeof value !== 'object' || value === null || Array.isArray(value)) {" "if (false) {"
run "unknown-field guard" \
  "    if (!FIELDS.includes(key)) {" "    if (false) {"
run "missing-field guard" \
  "    if (!Object.hasOwn(value, key)) {" "    if (false) {"

# The id: its type, its emptiness, its length, the database-key rule, and the
# surrogate rule that keeps it encodable.
run "id: type guard" \
  "  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'id') };" ""
run "id: empty guard" \
  "  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, 'id') };" ""
run "id: length bound" \
  "  if (value.length > ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'id') };" ""
run "id: database-key character guard" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f) {" "    if (false) {"
run "id: lone-surrogate guard" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_KEY, 'id') };" ""

# The bounded strings: type, emptiness, bound, control characters, surrogates.
run "string: type guard" \
  "  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, field) };" ""
run "string: empty guard" \
  "  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, field) };" ""
run "string: length bound" \
  "  if (value.length > max) return { bad: refuse(REFUSALS.TOO_LONG, field) };" ""
run "string: control-character guard" \
  "    if ((code < 0x20 && !line) || code === 0x7f) {" "    if (false) {"
run "string: lone-surrogate guard" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };" ""

# The time, the ack, and the two properties of the accepted object: frozen, and
# a copy with the caller's values verbatim.
run "timestamp: type guard" \
  "  if (typeof value !== 'number') return { bad: refuse(REFUSALS.WRONG_TYPE, 'at') };" ""
run "timestamp: safe-integer/range guard" \
  "  if (!Number.isSafeInteger(value) || value < 0) return { bad: refuse(REFUSALS.BAD_TIMESTAMP, 'at') };" ""
run "acked: boolean guard" \
  "    if (typeof value.acked !== 'boolean') {" "    if (false) {"
run "the message is frozen" \
  "  return { ok: true, message: Object.freeze(message) };" "  return { ok: true, message };"
run "the message is a copy, not the caller's object" \
  "  const message = { id: id.value, sender: sender.value, text: text.value, at: at.value };" "  const message = value;"
run "the id is not trimmed (no coercion)" \
  "  const message = { id: id.value, sender: sender.value, text: text.value, at: at.value };" \
  "  const message = { id: id.value.trim(), sender: sender.value, text: text.value, at: at.value };"

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
