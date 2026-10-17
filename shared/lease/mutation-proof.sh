#!/bin/bash
# Mutation-proves the consumer lease's guards (mobile plan, Task P1.2).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make `npm test` fail. The harness (runner discovery, the positive
# and negative controls, the restore by `git checkout`) is the one
# shared/mailbox-schema/mutation-proof.sh uses; read it there for the why.
#
#   shared/lease/mutation-proof.sh
#
# Expected output: one "killed" line per guard, no "SURVIVED", no
# "APPLY-FAILED", and exit 0. Run it from a clean tree.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/lease/index.js
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
  # The summary is `# tests N` under the TAP reporter and `ℹ tests N` under the
  # spec reporter (node 23+ default); the runner flag cannot be pinned through
  # `npm test`, whose glob would swallow it, so both forms are read.
  tests=$(printf '%s\n' "$out" | sed -En 's/^(#|ℹ) tests ([0-9]+)$/\2/p' | tail -1)
  fails=$(printf '%s\n' "$out" | sed -En 's/^(#|ℹ) fail ([0-9]+)$/\2/p' | tail -1)
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
  echo "REFUSING: the suite does not pass on the pristine $F ($LAST_TESTS tests ran)" >&2
  printf '%s\n' "$LAST_OUT" | tail -20 >&2
  exit 2
fi
echo "control       the pristine suite passes, $LAST_TESTS tests, 0 failures"

mutate_test "control: every take succeeds (must be killed)" \
  "  if (current !== null && isFresh(current, mine.at, held.ttl)) {" "  if (false) {"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
killed=0

# (1) The far-future lease: the skew bound, its boundary, and the ceilings.
run "skew: a lease far ahead is honoured forever (the DoS)" \
  "  if (current.at - at > MAX_CLOCK_SKEW) return false;" ""
run "skew: the tolerance is exclusive (> becomes >=)" \
  "  if (current.at - at > MAX_CLOCK_SKEW) return false;" "  if (current.at - at >= MAX_CLOCK_SKEW) return false;"
run "bounds: no ceiling on at or ttl" \
  "value >= 1 && value <= max;" "value >= 1;"
# (3) zero and -0.
run "bounds: zero (and -0) accepted" \
  "value >= 1 && value <= max;" "value >= 0 && value <= max;"
# (2) Holder identity.
run "holder: NFC not required" \
  "  if (holder.normalize('NFC') !== holder) {" "  if (false) {"
run "holder: whitespace inside accepted" \
  "\\p{Z}\\p{White_Space}" ""
run "holder: default-ignorable accepted" \
  "\\p{Default_Ignorable_Code_Point}" ""
run "holder: private use accepted" \
  "\\p{Co}" ""
run "holder: unassigned accepted" \
  "\\p{Cn}" ""
run "holder: U+2800 accepted" \
  "\\u2800" ""
# (4) The snapshot: every input read once.
run "snapshot: the state is read again after validation" \
  "  const { claim, ttl } = snap.fields;" "  const { claim, ttl } = { claim: snap.fields.claim, ttl: state.ttl };"
run "snapshot: the held claim is read again" \
  "  const { claim, ttl } = snap.fields;" "  const { claim, ttl } = { claim: state.claim, ttl: snap.fields.ttl };"
run "snapshot: the take's holder is read again" \
  "  const holder = validateHolder(snap.fields.holder);
  if (!holder.ok) return refusalWithState(holder, state);
  const at" "  const holder = validateHolder(snap.fields.holder);
  if (!holder.ok) return refusalWithState(holder, state);
  holder.holder = request.holder;
  const at"
run "snapshot: each field is read twice" \
  "  for (const key of fields) copy[key] = value[key];" \
  "  for (const key of fields) copy[key] = (value[key], value[key]);"
# (5) The release keeps the TTL.
run "release: the TTL is reset to CLAIM_TTL" \
  "state: emptyLeaseState(held.ttl) });" "state: emptyLeaseState() });"

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
