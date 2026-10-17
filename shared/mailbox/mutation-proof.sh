#!/bin/bash
# Mutation-proves the mailbox state machine's guards (mobile plan, Task P1.1).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make the module's tests fail. A guard whose mutation survives is a
# guard no test defends, which for this module -- the protocol that decides
# whether a message is acted on twice -- is the difference between a fence and
# a comment.
#
#   shared/mailbox/mutation-proof.sh
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
# The suite it runs is THIS module's tests, not the whole package's `npm test`,
# and the difference is deliberate rather than convenient. On the node line
# package.json's engines declares (22), the package suite has one PRE-EXISTING
# failure that is not this module's: shared/url/index.test.js refuses
# 'https://xn--github-9d0c.com/a' as 'not-a-url' where it expects
# 'punycode-host' (P0.3's module, untouched by this task). A package-wide suite
# would make the positive control below refuse to run at all, and calling that
# failure "known" would let a real regression hide behind it. Narrowing the
# suite to the module the mutations are in measures the right thing; the
# pre-existing failure is reported in the pull request instead.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no "APPLY-FAILED"
# (a pattern that no longer matches means this script is stale, not that the
# guard is proved), and exit 0.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/mailbox/index.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A mise shim for a tool whose version was never set sits on PATH and
# fails when called, which is a runner that would make every verdict below
# fiction.
runner_works() {
  node --version >/dev/null 2>&1
}

# A runner, found rather than assumed. A working one on PATH is the ordinary
# case; this repository's base image keeps node off PATH on purpose
# (docs/base-image.md), and mise holds the toolchains, so a machine that has
# mise gets one from there rather than from any one machine's home directory.
# node@22 is the line package.json's engines declares (>=22 <23).
if ! runner_works && command -v mise >/dev/null 2>&1; then
  for tool in node@22 node; do
    dir=$(mise where "$tool" 2>/dev/null || true)
    if [ -n "$dir" ] && [ -x "$dir/bin/node" ]; then
      PATH="$dir/bin:$PATH"
      break
    fi
  done
fi
if ! runner_works; then
  echo "REFUSING: no working node (on PATH, or through mise);" >&2
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

# suite runs this module's tests and returns 0 only for a real green run: the
# runner exited 0, the summary reports at least one test, and nothing failed.
# The pattern is the module's own, run from the package directory so that the
# glob is node's to expand (quoted), exactly as package.json's script does.
suite() {
  local out code tests fails
  out=$( (cd shared && node --test "mailbox/*.test.js") 2>&1 )
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
  echo "REFUSING: the suite does not pass on the pristine $F ($LAST_TESTS tests ran);" >&2
  echo "          a mutation's failure could not be told from the baseline's." >&2
  printf '%s\n' "$LAST_OUT" | tail -20 >&2
  exit 2
fi
echo "control       the pristine suite passes, $LAST_TESTS tests, 0 failures"

# 2. The negative control: a refusal that refuses nothing must be caught. If the
#    suite cannot fail, every "killed" below would be a coincidence.
mutate_test "control: refuse() always succeeds (must be killed)" \
  "  return Object.freeze({ ok: false, code, field });" \
  "  return Object.freeze({ ok: true, code, field });"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. Reading the inputs: the value itself, its fields, and their descriptors.
run "not-an-object guard (null/array/primitive)" \
  "  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, null);" ""
run "plain-object guard: a prototype of its own" \
  "  return prototype === Object.prototype || prototype === null;" "  return true;"
# There is deliberately no mutation for the `Array.isArray` clause of
# isPlainObject: the prototype check above subsumes it (an array's prototype is
# Array.prototype, which is neither Object.prototype nor null), so a mutation of
# it cannot be killed by any test -- a claim, not a proof. The clause is kept
# for parity with the schema's own isPlainObject, and the array case is proved
# by the prototype mutation instead. The `null` clause below IS load-bearing:
# without it, Object.getPrototypeOf(null) throws where the module promises a
# refusal.
run "plain-object guard: null is not an object" \
  "  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;" \
  "  if (typeof value !== 'object' || Array.isArray(value)) return false;"
run "unknown-field guard" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, key);" ""
run "accessor-property guard (never called)" \
  "    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {" "    if (false) {"
run "missing-field guard" \
  "    if (values[field] === undefined) return refuse(REFUSAL.MISSING_FIELD, field);" ""

# 4. The state: its phase, the ledger's summary, and the four numbers.
run "unknown-state guard" \
  "  if (!Object.values(STATE).includes(values.phase)) return refuse(REFUSAL.UNKNOWN_STATE, 'phase');" ""
run "phase-vs-ledger guard (invalid-state)" \
  "  if (values.phase !== STATE.CLOSED && values.phase !== derivePhase(seen.entries)) {" "  if (false) {"
run "state: now range" \
  "  if (!isIntegerInRange(values.now, 1, AT_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');" ""
run "state: window range" \
  "  if (!isIntegerInRange(values.windowMs, MIN_WINDOW_MS, MAX_WINDOW_MS)) {" "  if (false) {"
run "state: cap range" \
  "  if (!isIntegerInRange(values.maxSeen, MIN_MAX_SEEN, MAX_MAX_SEEN)) {" "  if (false) {"
run "state: future-tolerance range" \
  "  if (!isIntegerInRange(values.futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {" "  if (false) {"
run "the number rule: negative zero" \
  "    !Object.is(value, -0) &&" ""
run "the number rule: the safe-integer line" \
  "    Number.isSafeInteger(value) &&" ""

# 5. initialState's own four, which are a second entry point to the same numbers.
run "initialState: now range" \
  "  if (!isIntegerInRange(now, 1, AT_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');" ""
run "initialState: window range" \
  "  if (!isIntegerInRange(windowMs, MIN_WINDOW_MS, MAX_WINDOW_MS)) {" "  if (false) {"
run "initialState: cap range" \
  "  if (!isIntegerInRange(maxSeen, MIN_MAX_SEEN, MAX_MAX_SEEN)) {" "  if (false) {"
run "initialState: future-tolerance range" \
  "  if (!isIntegerInRange(futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {" "  if (false) {"

# 6. The ledger's shape: the container, its cap, each record's fields, one
#    record per pair, and the canonical order.
run "ledger: the array guard" \
  "  if (!Array.isArray(value) || value.length > maxSeen) {" "  if (value.length > maxSeen) {"
run "ledger: the cap" \
  "  if (!Array.isArray(value) || value.length > maxSeen) {" "  if (!Array.isArray(value)) {"
run "ledger record: sender is a string" \
  "      typeof entry.sender !== 'string' ||" ""
run "ledger record: sender is not empty" \
  "      entry.sender === '' ||" ""
run "ledger record: sender's bound" \
  "      entry.sender.length > SENDER_MAX ||" ""
run "ledger record: id is a string" \
  "      typeof entry.id !== 'string' ||" ""
run "ledger record: id is not empty" \
  "      entry.id === '' ||" ""
run "ledger record: id's bound" \
  "      entry.id.length > ID_MAX ||" ""
run "ledger record: at is a time" \
  "      !isIntegerInRange(entry.at, 1, AT_MAX) ||" ""
run "ledger record: the status is a status" \
  "      !STATUS_VALUES.includes(entry.status)" "      false"
run "ledger: one record per (sender, id)" \
  "    if (keys.has(key)) return refuse(REFUSAL.DUPLICATE_SEEN_ENTRY, 'seen');" ""
run "ledger: the canonical order" \
  "    if (compareEntries(entries[index - 1], entries[index]) >= 0) {" "    if (false) {"
run "the ledger key: (sender, id) is a pair, not a concatenation" \
  "  return \`\${sender.length}:\${sender}\${id}\`;" "  return \`\${sender}\${id}\`;"
run "insert keeps the canonical order" \
  "  while (index < entries.length && compareEntries(entries[index], entry) < 0) index += 1;" \
  "  while (false) index += 1;"
run "the canonical order compares senders" \
  "  if (left.sender !== right.sender) return left.sender < right.sender ? -1 : 1;" ""

# 7. The event, and the one guard the dispatch table owns.
run "event: the injected now is a time" \
  "  if (!isIntegerInRange(values.now, 1, AT_MAX)) {" "  if (false) {"
run "event: a delivery carries a message" \
  "  if (values.type === EVENT.DELIVER && !hasMessage) {" "  if (false) {"
run "event: only a delivery carries a message" \
  "  if (values.type !== EVENT.DELIVER && hasMessage && EVENT_VALUES.includes(values.type)) {" \
  "  if (false) {"
run "unknown-event guard (the dispatch table's one lookup)" \
  "  if (typeof type !== 'string' || !Object.hasOwn(row, type)) {" "  if (false) {"
run "the monotonic now (now-went-backwards)" \
  "  if (now < current.state.now) {" "  if (false) {"

# 8. The delivery itself: the message's time, the dedupe pair, the duplicate,
#    the conflict, and the cap.
run "delivery: negative zero in a message" \
  "  if (Object.is(at, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, 'message.at');" ""
run "delivery: a zero timestamp is not a time" \
  "  if (at < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, 'message.at');" ""
run "delivery: the future bound" \
  "  if (at - values.now > state.futureToleranceMs) {" "  if (false) {"
run "delivery: the replay window" \
  "  if (values.now - at > state.windowMs) return refuse(REFUSAL.REPLAY, 'message.at');" ""
run "dedupe: the pair is (sender, id), not the id alone" \
  "  const known = state.seen.find((entry) => entry.sender === sender && entry.id === id);" \
  "  const known = state.seen.find((entry) => entry.id === id);"
run "dedupe: only inside the window is a record consulted" \
  "  if (known !== undefined && values.now - known.at <= state.windowMs) {" "  if (known !== undefined) {"
run "the id-conflict guard" \
  "    if (known.at !== at) return refuse(REFUSAL.ID_CONFLICT, 'message.at');" ""
run "a duplicate does not re-arm the record" \
  "    return answered(OUTCOME.DUPLICATE, state.phase, state, values.now, state.seen, sender, id);" \
  "    return answered(OUTCOME.DUPLICATE, state.phase, state, values.now, state.seen.map((entry) => Object.freeze({ sender: entry.sender, id: entry.id, at: entry.at, status: STATUS.ACCEPTED })), sender, id);"
run "the cap refuses rather than evicting a live record" \
  "  if (kept.length >= state.maxSeen) {" "  if (false) {"

# 9. The eviction rule: both halves of the predicate, and the id being reused.
run "eviction: only outside the window is evictable" \
  "    if (now - entry.at <= state.windowMs) return true;" "    if (false) return true;"
run "eviction: only an acked record is evictable" \
  "    if (entry.status === STATUS.ACKED) return false;" "    if (false) return false;"
run "eviction: a reused id's old record goes" \
  "    return !(entry.sender === sender && entry.id === id);" "    return true;"

# 10. The dispatch table, the derived phase, and the two freezes.
run "dispatch: the closed row is not the open row" \
  "  [STATE.CLOSED]: closedRow()," "  [STATE.CLOSED]: openRow(),"
run "dispatch: a closed mailbox refuses a delivery" \
  "    [EVENT.DELIVER]: refuseClosed," "    [EVENT.DELIVER]: deliver,"
run "derivePhase: an acted record is not idle" \
  "  if (entries.some((entry) => entry.status === STATUS.ACTED)) return STATE.ACTED;" ""
run "the answer is frozen" \
  "  return Object.freeze({
    ok: true,
    outcome," \
  "  return ({
    ok: true,
    outcome,"
run "the answer's ledger is frozen" \
  "      seen: Object.freeze(entries)," "      seen: entries,"
run "the state's ledger is rebuilt, not the caller's array" \
  "      seen: seen.entries," "      seen: values.seen,"

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
