#!/bin/bash
# Mutation-proves the voice switch and its indicator (mobile plan, Task P5.3.4).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make `npm test` fail. A guard whose mutation survives is a guard no
# test defends -- and this module is the app's own answer to "where does this
# utterance go", so a surviving guard here is the difference between a badge that
# says local and audio that left the device.
#
#   shared/voice-switch/mutation-proof.sh
#
# The FIRST guard is the one this task turns on: a badge that trusts the switch
# while a remote call is already in flight, which is how the indicator comes to
# say `local` while audio is going out.
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
F=shared/voice-switch/index.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A mise shim for a tool whose version was never set sits on PATH and
# fails when called, which is a runner that would make every verdict below
# fiction.
runner_works() {
  node --version >/dev/null 2>&1 && npm --version >/dev/null 2>&1
}

# A runner, found rather than assumed. A working one on PATH is the ordinary
# case; the base carries no node of its own (docs/base-image.md: a project's
# runtimes come from mise), so mise is tried next rather than any one machine's
# home directory. node@22 is the line package.json's engines declares (>=22 <23).
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
  echo "REFUSING: no working node and npm (on PATH or through mise);" >&2
  echo "          the base carries none (docs/base-image.md): 'mise use -g node@22' provides" >&2
  echo "          one -- and without a runner the verdicts below would be fiction." >&2
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
  echo "REFUSING: the suite does not pass on the pristine $F ($LAST_TESTS tests ran);" >&2
  echo "          a mutation's failure could not be told from the baseline's." >&2
  printf '%s\n' "$LAST_OUT" | tail -20 >&2
  exit 2
fi
echo "control       the pristine suite passes, $LAST_TESTS tests, 0 failures"

# 2. The negative control: a machine that refuses nothing must be caught. If the
#    suite cannot fail, every "killed" below would be a coincidence.
mutate_test "control: refuse() always succeeds (must be killed)" \
  "function refuse(code, field) {" "function refuse(code, field) { return Object.freeze({ ok: true });"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# ---------------------------------------------------------------------------
# The guard this task turns on: a call in flight is what the badge shows
# ---------------------------------------------------------------------------

run "RED STEP: the badge trusts the switch while a remote call is in flight" \
  "      state: going ? STATE.REMOTE : STATE.LOCAL,
      inFlight: true," \
  "      state: decision.state,
      inFlight: true,"
run "the egress decision follows the call in flight (the badge may not widen it)" \
  "      engine: decision.engine,
      provider: decision.provider,
      state: going ? STATE.REMOTE : STATE.LOCAL,
      inFlight: true," \
  "      engine: going ? ENGINE.REMOTE : ENGINE.LOCAL,
      provider: decision.provider,
      state: going ? STATE.REMOTE : STATE.LOCAL,
      inFlight: true,"
run "a call in flight in the OTHER direction is not this direction's business" \
  "  const last = call.values !== null && call.values.direction === direction ? call.values : null;" \
  "  const last = call.values;"
run "a call that finished does not change the badge" \
  "  if (last === null || last.state === CALL_STATE.DONE) {" \
  "  if (last === null) {"

# ---------------------------------------------------------------------------
# A failed call falls back to local
# ---------------------------------------------------------------------------

run "a failed remote call is local, not the decision" \
  "    state: STATE.LOCAL,
    inFlight: false,
    reason: failureReason(last)," \
  "    state: decision.state,
    inFlight: false,
    reason: failureReason(last),"
run "a failed local call is local-error, not the remote default" \
  "  return last.engine === ENGINE.REMOTE ? CALL_FAILURE.PROVIDER_ERROR : CALL_FAILURE.LOCAL_ERROR;" \
  "  return CALL_FAILURE.PROVIDER_ERROR;"
run "a failed call's own reason is carried, not dropped" \
  "  if (last.reason !== null) return last.reason;
  return last.engine" \
  "  return last.engine"

# ---------------------------------------------------------------------------
# The order of the branches is the rule: device first, then the hop
# ---------------------------------------------------------------------------

run "an unset switch is local even when the project admits remote" \
  "  if (switches[direction] !== ENGINE.REMOTE) return local(REASON.SWITCH_LOCAL);" \
  "  if (switches[direction] !== ENGINE.REMOTE && status === null) return local(REASON.SWITCH_LOCAL);"
run "no recorded consent is the device's own gap, and it is local" \
  "  if (consented.length === 0) return local(REASON.NO_CONSENT);" \
  "  if (consented.length === 0) return local(REASON.NO_ANSWER);"

# ---------------------------------------------------------------------------
# The project's half: the hop's answer, and only the hop's answer
# ---------------------------------------------------------------------------

run "no project-status answer yet is checking, and NEVER a guess" \
  "  if (status === null) return checking(REASON.NO_ANSWER);" \
  "  if (status === null) return remote(consented[0]);"
run "a failed query is checking; the hop's other failures are local" \
  "    if (status.code === STATUS_FAILURE.QUERY_FAILED) return checking(status.code);
    return local(status.code);" \
  "    return checking(status.code);"
run "the hop's own failure reason is carried, not replaced" \
  "    return local(status.code);" \
  "    return local(REASON.SWITCH_LOCAL);"
run "blocked comes only from an answer the hop gave, and from its own two halves" \
  "  if (status.direction[direction] !== true || status.providers.length === 0) {" \
  "  if (status.direction[direction] !== true) {"
run "the direction map is read for the direction being asked about" \
  "  if (status.direction[direction] !== true || status.providers.length === 0) {" \
  "  if (status.direction.stt !== true || status.providers.length === 0) {"
run "an answer with no reason of its own is no-admission, the default deny" \
  "    return blocked(status.reason === null ? POLICY_REASON.NO_ADMISSION : status.reason);" \
  "    return blocked(POLICY_REASON.FORCE_LOCAL);"

# ---------------------------------------------------------------------------
# remote (<provider name>): a name the project admits AND the device consented to
# ---------------------------------------------------------------------------

run "the name must be one the project admits, not any consented name" \
  "  const provider = status.providers.find((name) => consented.includes(name));" \
  "  const provider = status.providers.find(() => true);"
run "the name must be one the device consented to, not merely any consent" \
  "  const provider = status.providers.find((name) => consented.includes(name));" \
  "  const provider = consented.length > 0 ? status.providers[0] : undefined;"

# ---------------------------------------------------------------------------
# The badge's words
# ---------------------------------------------------------------------------

run "checking's badge is the design's own wording" \
  "  [STATE.CHECKING]: 'local — checking'," "  [STATE.CHECKING]: 'checking',"
run "blocked's badge is the design's own wording" \
  "  [STATE.BLOCKED]: 'blocked by project policy'," "  [STATE.BLOCKED]: 'blocked',"
run "the badge names the provider it went to" \
  "  const text = badge.state === STATE.REMOTE ? \`remote (\${badge.badgeName})\` : BADGE[badge.state];" \
  "  const text = badge.state === STATE.REMOTE ? 'remote' : BADGE[badge.state];"
run "a remote badge always names a name, never null" \
  "      badgeName: going ? last.provider : null," \
  "      badgeName: null,"
run "the badge says a call is in flight" \
  "const IN_FLIGHT = ' — in flight';" "const IN_FLIGHT = '';"

# ---------------------------------------------------------------------------
# The last call's route is read strictly
# ---------------------------------------------------------------------------

run "a remote call that names no provider is refused" \
  "  if (engine === ENGINE.REMOTE && provider === null) {
    return refuse(REFUSAL.MISSING_FIELD, 'call.provider');
  }" ""
run "a local call that names a provider is refused" \
  "  if (engine === ENGINE.LOCAL && provider !== null) {
    return refuse(REFUSAL.UNEXPECTED_PROVIDER, 'call.provider');
  }" ""
run "a failure reason on a call that did not fail is refused" \
  "  if (state !== CALL_STATE.FAILED && reason !== null) {
    return refuse(REFUSAL.UNEXPECTED_REASON, 'call.reason');
  }" ""

# ---------------------------------------------------------------------------
# A provider name is a name, never a URL
# ---------------------------------------------------------------------------

run "a provider name is a name, never a URL" \
  "  return typeof value === 'string' && value.length <= PROVIDER_MAX && PROVIDER_PATTERN.test(value);" \
  "  return typeof value === 'string' && value.length <= PROVIDER_MAX;"
run "a provider name is not empty" \
  "const PROVIDER_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]*\$/;" \
  "const PROVIDER_PATTERN = /^[A-Za-z0-9._-]*\$/;"

# ---------------------------------------------------------------------------
# The consent record, and its time
# ---------------------------------------------------------------------------

run "a consent carries the name it is about" \
  "    if (provider === undefined) return refuse(REFUSAL.MISSING_FIELD, 'consent.provider');" ""
run "a consent is dated, not a bare flag" \
  "    if (at === undefined) return refuse(REFUSAL.MISSING_FIELD, 'consent.at');" ""
run "a zero consent time is the shape of an unset counter, and is refused" \
  "    value >= 1 &&
    value <= MAX_TIMESTAMP" "    value <= MAX_TIMESTAMP"
run "one name carries one consent" \
  "    if (seen.has(provider)) return refuse(REFUSAL.DUPLICATE_CONSENT, 'consent.provider');" ""
run "one admitted name appears once in an answer" \
  "    if (seen.has(name)) return refuse(REFUSAL.DUPLICATE_PROVIDER, 'status.providers');" ""

# ---------------------------------------------------------------------------
# The bounds, and the strict readers
# ---------------------------------------------------------------------------

run "the consent list has a bound" \
  "  if (length > MAX_CONSENT) return refuse(REFUSAL.INVALID_CONSENT, 'consent');" ""
run "the admitted list has a bound" \
  "  if (length > MAX_PROVIDERS) return refuse(REFUSAL.INVALID_PROVIDERS, 'status.providers');" ""
run "a direction admission is a boolean, not a truthy value" \
  "    if (typeof admitted !== 'boolean') return refuse(REFUSAL.WRONG_TYPE, \`status.direction.\${direction}\`);" ""
run "a status discriminates on ok, and reads the shape ok names" \
  "  return descriptor.value === false ? readStatusFailure(value) : readStatusAnswer(value);" \
  "  return readStatusAnswer(value);"
run "an answer with no ok at all is a missing field, not an answer" \
  "  if (descriptor === undefined) return refuse(REFUSAL.MISSING_FIELD, 'status.ok');" \
  "  if (descriptor === undefined) return readStatusAnswer(value);"
run "an unknown field is refused rather than read past" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" ""
run "an unknown field is reported by the container, never by the caller's key" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, key);"
run "a getter is refused without ever being called" \
  "    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, where);
    }" \
  "    if (descriptor === undefined) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, where);
    }"
run "a value that is not a plain object is refused, not read" \
  "  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, where);" ""
run "an input that throws while it is read is a refusal, not a throw" \
  "    return refuse(REFUSAL.INTERNAL_ERROR, null);" \
  "    throw new Error('unhandled');"
run "an answer is frozen" \
  "  return Object.freeze({
    ok: true," "  return {
    ok: true,"
run "a refusal is frozen" \
  "  return Object.freeze({ ok: false, code, field });" \
  "  return { ok: false, code, field };"

# NOT proved here, and why: `isProvider`'s `PROVIDER_MAX` bound and the
# `PROVIDER_MAX`-shaped half of the pattern are one guard with two spellings
# above; and the `!Object.is(value, -0)` clause in `isTimestamp` is reached only
# through `at`, whose minimum is 1, so removing it alone changes no answer -- the
# `-0` probe in the suite passes either way. The clause stays because
# shared/lease's own timestamp reader has it and this module must read a time the
# way the rest of the package does.

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
