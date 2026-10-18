#!/bin/bash
# Mutation-proves the mailbox simulator's guards (mobile plan, Task P1.5).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make the module's tests fail. A guard whose mutation survives is a
# guard no test defends -- and for this module, whose whole purpose is to make
# the at-most-once and at-least-once properties checkable rather than asserted,
# a surviving guard is the difference between a proof and a diagram.
#
#   shared/mailbox-sim/mutation-proof.sh
#
# The FIRST guard is the one the task names: **the dedupe in the harness's
# consumer loop**. The consumer carries the persisted ledger from pass to pass;
# the mutation replaces that carried ledger with a freshly built one, so every
# redelivery is accepted again and acted on again. The at-most-once property
# test must fail on it, and it prints the seed it failed on -- which is what
# makes the failure replayable rather than merely red.
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
F=shared/mailbox-sim/index.js

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
  out=$( (cd shared && node --test "mailbox-sim/*.test.js") 2>&1 )
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

# 3. THE DEDUPE (the guard the task names). The consumer's whole defence against
#    a redelivery is that it carries the ledger it committed last pass; rebuild
#    that ledger from nothing and every redelivery is a new delivery, which is a
#    second action on the same (sender, id).
run "the consumer's dedupe: the persisted ledger is carried across passes" \
  "        // The persisted ledger, carried across passes: this is the dedupe. A
        // consumer that re-derived the ledger from nothing would re-accept
        // every redelivery and act on it a second time.
        let state = snapshot.mailbox;" \
  "        let state = initialState({ now: clock.now(), promptTtlMs: config.values.promptTtlMs }).state;"

# 4. The store: one value, replaced only by compare-and-swap. The lease's README
#    makes this the caller's duty, and it is the fence two racing consumers meet.
run "the compare-and-swap refuses a stale generation" \
  "  if (expected !== store.generation) return refuse(REFUSAL.CAS_CONFLICT, 'generation');" ""
run "the compare-and-swap writes the ledger as well as the lease" \
  "  store.lease = next.lease;
  store.mailbox = next.mailbox;" \
  "  store.lease = next.lease;"

# 5. Holding the lease: a holder's own take is answered already-claimed, and
#    that is how a holder renews; anyone else's answer means it writes nothing.
run "a holder's own already-claimed take counts as holding" \
  "    if (result.code === LEASE_CODES.ALREADY_CLAIMED && result.existing.holder === id) {" \
  "    if (false) {"
run "another holder's claim is not this consumer's to act under" \
  "    if (result.code === LEASE_CODES.ALREADY_CLAIMED && result.existing.holder === id) {" \
  "    if (result.code === LEASE_CODES.ALREADY_CLAIMED) {"

# 6. The action, and the two writes that follow it.
#
#    The first of these is a log field, and it is kept because the log is what a
#    failing run prints: a record the ledger consumed as stale must not be
#    recorded as one the harness performed. The behaviour behind it is the third
#    guard below, which is the one that can lose an answer.
run "an expired prompt performs nothing (the safe path)" \
  "                outcome: OUTCOME.STALE_PENDING,
                action: null," \
  "                outcome: OUTCOME.STALE_PENDING,
                action: 'reply',"
run "the ledger acks only the message the store shows acked" \
  "        if (acted !== undefined && isAcked(snapshot.inbox, acted.id)) {" \
  "        if (acted !== undefined) {"
run "the phone reads the replies it can see" \
  "        for (const reply of store.outbox.values()) spoken.add(reply.id);" ""
#    A reply is owed for a record the harness *performed* on. The ledger cannot
#    tell a performed record from a stale one -- both are `acted` -- so a reply
#    for every consumed record is an answer to an expired prompt that no
#    approved rule of this project has an answer for.
run "a stale-pending record is consumed, not performed on, so it is not replied to" \
  "          if (acting.outcome !== OUTCOME.ACTED) continue;" \
  "          if (false) continue;"

# 7. The retry duty the mailbox's README states: the same message, `at` included.
run "a retry re-sends the same message, its at included" \
  "            send('inbox-write', 'inbox', PHONE, message.at, message);" \
  "            send('inbox-write', 'inbox', PHONE, clock.now(), message);"

#    And the store's side of the same duty: a write is a **merge**, not a
#    replacement. The ack in the record is not the phone's field to clear, so a
#    retry that lands after the ack must leave it where it is. Clearing it makes
#    the store say "unacked" about a message the ledger has already consumed,
#    and nothing repairs that.
run "a re-sent message does not clear the ack the inbox already carries" \
  "      const acked = held !== undefined && held.acked === true;" \
  "      const acked = false;"

# 8. The wire: the four faults, each a property of one frame.
run "a reorder swaps a frame with the frame beside it" \
  "        if (due[index].reorder && !due[index + 1].reorder) {
          const swapped = due[index];
          due[index] = due[index + 1];
          due[index + 1] = swapped;
          index += 1;
        }" ""
run "a delay holds a frame until the clock reaches it" \
  "        entry.arriveAt += applied.delayMs;" "        entry.arriveAt += 0;"
run "a drop drops" \
  "      } else if (applied.op === FAULT.DROP) {
        entry.dropped = true;
      }" \
  "      } else if (applied.op === FAULT.DROP) {
        queue.push(entry);
      }"
run "a schedule's clock skew is applied to the writer's own time" \
  "    return clock.now() + (schedule.clocks[actor] === undefined ? 0 : schedule.clocks[actor]);" \
  "    return clock.now();"
run "the fault for a frame is looked up by that frame's number" \
  "    const found = plan.faults.find((fault) => fault.frame === frame);" \
  "    const found = plan.faults[0];"
run "a run hands back the schedule it actually applied" \
  "      faults: Object.freeze(logs.faults.map((fault) => fault.applied))," \
  "      faults: schedule.faults,"

# 9. The schedule, as data: what it may say, and what it may not.
run "a generated skew stays inside the tolerance the modules name" \
  "    const skew = rng.nextInt(2 * MAX_CLOCK_SKEW + 1) - MAX_CLOCK_SKEW;" \
  "    const skew = rng.nextInt(2 * MAX_SKEW_MS + 1) - MAX_SKEW_MS;"
run "a schedule is explicit or generated, never both" \
  "  if (faults.length > 0 && fields.rate > 0) return refuse(REFUSAL.MIXED_SCHEDULE, 'rate');" ""
#    The printed schedule is canonical: two schedules that differ only in the
#    order their faults were written are the same schedule, so they must print
#    the same line -- otherwise the line a failure prints is not a replay of the
#    run that failed.
run "the printed schedule's faults are in frame order" \
  "  const faults = [...plan.faults].sort((left, right) => left.frame - right.frame);" \
  "  const faults = plan.faults;"
run "a delay fault carries a delay, inside the schedule's bound" \
  "    if (!isDelay(fields.delayMs) || fields.delayMs > maxDelayMs) {" "    if (false) {"
run "a fault's field set is closed" \
  "  const read = readOptional(value, FAULT_FIELDS, 'faults');" \
  "  const read = readOptional(value, [...FAULT_FIELDS, 'extra'], 'faults');"
run "a frame's number starts at one" \
  "  if (!Number.isSafeInteger(fields.frame) || fields.frame < 1) {" \
  "  if (!Number.isSafeInteger(fields.frame)) {"
run "every schedule field is required" \
  "    if (fields[field] === undefined) return refuse(REFUSAL.MISSING_FIELD, field);" ""
run "the fault vocabulary is closed" \
  "    if (typeof op !== 'string' || !WIRE_OPS.includes(op)) return refuse(REFUSAL.UNKNOWN_OP, where);" ""
run "an op is named once" \
  "    if (ops.includes(op)) return refuse(REFUSAL.BAD_OPS, where);" ""
run "a skew is bounded by the schedule's own ceiling" \
  "    if (Math.abs(skew) > MAX_SKEW_MS) return refuse(REFUSAL.BAD_SKEW, 'clocks');" ""
run "a rate is a fraction of one" \
  "  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1;" \
  "  return typeof value === 'number' && Number.isFinite(value) && value >= 0;"
run "a seed is a whole number" \
  "  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 0xffff_ffff;" \
  "  return typeof value === 'number' && Number.isSafeInteger(value);"
run "a generator refuses a seed it could not replay" \
  "  if (!isSeed(seed)) return refuse(REFUSAL.BAD_SEED, 'seed');
  return Object.freeze({ ok: true, ...rngOf(seed) });" \
  "  return Object.freeze({ ok: true, ...rngOf(seed) });"

# 10. The configuration, the actors, and the harness's own refusals.
#
# There is deliberately no mutation for the prompt TTL, the start time or the
# lease TTL. This harness hands all three to the modules that own them, and the
# refusals a caller sees are the modules' own codes ('missing-field' from
# `initialState`, 'invalid-now' from `initialState`, 'bad-ttl' from
# `readLease`): a guard here would be re-implementing a rule the simulator
# exists to compose, so there is no harness guard to mutate. The tests assert
# those codes, which is what keeps the delegation honest rather than incidental.
run "the injected clock is a clock" \
  "  if (clock === null || typeof clock !== 'object' || typeof clock.now !== 'function') {" \
  "  if (false) {"
run "an unknown field is refused, not dropped" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" ""
run "a run is bounded" \
  "    if (!Number.isSafeInteger(ticks) || ticks < 0 || ticks > MAX_TICKS) {" \
  "    if (!Number.isSafeInteger(ticks) || ticks < 0) {"
run "an advance only goes forward" \
  "    if (!Number.isSafeInteger(ms) || ms < 0) return refuse(REFUSAL.BAD_MILLISECONDS, 'ms');" \
  "    if (!Number.isSafeInteger(ms)) return refuse(REFUSAL.BAD_MILLISECONDS, 'ms');"
run "an interval is not negative" \
  "    if (!Number.isSafeInteger(intervalMs) || intervalMs < 0) {" "    if (false) {"
run "the phone is not a consumer" \
  "    if (id === PHONE) return refuse(REFUSAL.DUPLICATE_ACTOR, 'id');" ""
run "an actor is named once" \
  "    if (actors.some((actor) => actor.id === id)) return refuse(REFUSAL.DUPLICATE_ACTOR, 'id');" ""
run "a consumer needs a name" \
  "    if (typeof id !== 'string' || id === '') return refuse(REFUSAL.BAD_TYPE, 'id');" \
  "    if (typeof id !== 'string') return refuse(REFUSAL.BAD_TYPE, 'id');"
run "a written message is the schema's to accept" \
  "        if (!validated.ok) return refuse(validated.code, validated.field);" ""
run "the kind is a closed vocabulary" \
  "        if (kind !== KIND.COMMAND && kind !== KIND.PROMPT) return refuse(REFUSAL.BAD_TYPE, 'kind');" ""
run "a write names only the fields the harness knows" \
  "        const readMessage = readOptional(input, ['id', 'text', 'kind'], 'message');" \
  "        const readMessage = readOptional(input, ['id', 'text', 'kind', 'nope'], 'message');"

# The independent review's planted bugs, kept so they stay planted: each is a
# way the harness could quietly stop exercising a rule it composes.
run "a skew is added to the writer's clock, not subtracted from it" \
  "    return clock.now() + (schedule.clocks[actor] === undefined ? 0 : schedule.clocks[actor]);" \
  "    return clock.now() - (schedule.clocks[actor] === undefined ? 0 : schedule.clocks[actor]);"
run "a delivery's state transition is carried into the plan" \
  "          state = result.state;
          delivered.push(" \
  "          delivered.push("
run "the ledger's ack is taken only for a record it holds as acted, never one already acked" \
  "        const acted = state.seen.find((entry) => entry.status === STATUS.ACTED);" \
  "        const acted = state.seen.find((entry) => entry.status === STATUS.ACTED || entry.status === STATUS.ACKED);"
run "a delivery is judged at the consumer's now, so the replay window applies" \
  "step(state, { type: EVENT.DELIVER, now: clock.now(), message, kind })" \
  "step(state, { type: EVENT.DELIVER, now: message.at, message, kind })"

# The lease's skew direction and the mailbox's ack-twice refusal live in
# ../lease/ and ../mailbox/, whose own mutation proofs cover them; this suite
# also kills the lease's skew-direction mutation (the 'treat as expired' test),
# while a mailbox that re-acks an acked record is unreachable from here, because
# the harness only asks for an ack when the ledger holds an acted record.

# There is deliberately no mutation for `replyId`. The link between a reply and
# the message it answers is the harness's own convention (the schema has no
# `reply_to`: P6.3 proposes one, and P6.3 is NOT APPROVED), and the tests build
# their expectation by calling the same exported function -- so a mutation of it
# would move the expectation with the behaviour and no test could catch it. That
# is a property of the convention being the harness's, not a gap in the tests,
# and it is stated here rather than left as a mutation that always survives.

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
