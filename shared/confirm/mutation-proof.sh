#!/bin/bash
# Mutation-proves the spoken-confirmation machine (mobile plan, Task P6.2).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make `npm test` fail. A guard whose mutation survives is a guard no
# test defends -- and this module is the fence between "the speaker said
# something that sounded like yes" and a destructive command actually running,
# so a surviving guard here is the difference between a refusal and an action.
#
#   shared/confirm/mutation-proof.sh
#
# The FIRST guard is the task's named red step: "the replayed-confirm test
# against a machine without an action-id check". It is run first so that the
# plan's own red step is the first thing proved, not an afterthought.
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
F=shared/confirm/index.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A mise shim for a tool whose version was never set sits on PATH and
# fails when called, which is a runner that would make every verdict below
# fiction.
runner_works() {
  node --version >/dev/null 2>&1 && npm --version >/dev/null 2>&1
}

# A runner, found rather than assumed. A working one on PATH is the ordinary
# case; this repository's base image keeps node off PATH on purpose
# (docs/base-image.md) and holds it under /opt/fugaro, and mise holds the
# toolchains, so both are tried rather than any one machine's home directory.
# node@22 is the line package.json's engines declares (>=22 <23).
if ! runner_works; then
  for dir in /opt/fugaro/node /opt/node; do
    if [ -x "$dir/bin/npm" ]; then
      PATH="$dir/bin:$PATH"
      break
    fi
  done
fi
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
  echo "REFUSING: no working node and npm (on PATH, under /opt/fugaro, or through mise);" >&2
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
  echo "REFUSING: the suite does not pass on the pristine $F ($LAST_TESTS tests ran);" >&2
  echo "          a mutation's failure could not be told from the baseline's." >&2
  printf '%s\n' "$LAST_OUT" | tail -20 >&2
  exit 2
fi
echo "control       the pristine suite passes, $LAST_TESTS tests, 0 failures"

# 2. The negative control: a machine that refuses nothing must be caught. If the
#    suite cannot fail, every "killed" below would be a coincidence.
mutate_test "control: refuse() always succeeds (must be killed)" \
  "function refuse(code, field) {" "function refuse(code, field) { return { ok: true, message: {} };"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. The named red step, and the rest of the value. One confirmation authorises
#    exactly one action: the ledger remembers the confirmation id it spent, and
#    the confirmation names the action id it authorises.
run "RED STEP: a spent confirmation id is spent (replayed confirm)" \
  "  if (hasLedgerId(state, LEDGER_KIND.CONFIRMATION, confirmationId)) {" "  if (false) {"
run "the confirmation must name the outstanding action id" \
  "  if (entry === undefined || entry.actionId !== actionId) {" "  if (entry === undefined) {"
run "a confirmation must be the fixed phrase, and nothing else" \
  "  if (typeof text !== 'string') return false;
  return isConfirmation(routeUtterance({ utterance: text }));" "  return true;"
run "a bare \"confirm\" (P6.1's agent text) is not the phrase" \
  "  return isConfirmation(routeUtterance({ utterance: text }));" \
  "  return isConfirmation(routeUtterance({ utterance: text })) || CONFIRM_PHRASES.includes(text.trim().toLowerCase());"
run "the confirmation must name an outstanding read-back (confirm without pending)" \
  "  if (entry === undefined || entry.actionId !== actionId) {" "  if (entry !== undefined && entry.actionId !== actionId) {"
run "a confirmation one millisecond past the deadline is refused (off by one)" \
  "  if (values.now > entry.deadline) {" "  if (values.now > entry.deadline + 1) {"
run "the head SHA read back is bound: a moved head is a different target" \
  "  return left.repo === right.repo && left.pr === right.pr && left.headSha === right.headSha;" \
  "  return left.repo === right.repo && left.pr === right.pr;"
run "the merge record carries the head SHA read back (P6.3's head_sha)" \
  "    ...(t.kind === TARGET_KIND.MERGE ? { head_sha: t.headSha } : {})," ""
run "the record carries the confirmation's own time" \
  "    confirmation: Object.freeze({ id: confirmationId, at: confirmedAt })," \
  "    confirmation: Object.freeze({ id: confirmationId, at: entry.at }),"
run "the read-back is built from the target, never the transcript" \
  "    readBack: readBack(verb, resolved)," "    readBack: \`\${text} \${CONFIRM_CUE}\`,"
run "a launch is not destructive (D162)" \
  "  [VERB.MERGE]: Object.freeze([TARGET_KIND.MERGE]),
});" "  [VERB.MERGE]: Object.freeze([TARGET_KIND.MERGE]),
  [VERB.LAUNCH]: Object.freeze([TARGET_KIND.RUN]),
});"
run "a run id P6.3 refuses (a flag, a path) is refused" \
  "    if (!RUN_CHARS.test(runId.value) || !RUN_PATTERN.test(runId.value)) {" "    if (false) {"
run "an invisible format character is refused in a text" \
  "  if (FORMAT_CHARACTER.test(value)) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };" ""
run "a confirm word glued to punctuation is still a confirm word in a command" \
  "    .split(/[\\s\\p{P}\\p{S}]+/u)" "    .split(/\\s+/u)"
run "a second pending action makes a confirm ambiguous" \
  "  if (state.pending.length > 1) {" "  if (false) {"
run "the utterance that issued a command can never confirm it" \
  "  if (entry.by === utteranceId) {" "  if (false) {"
run "a confirmation after the timeout is refused" \
  "  if (values.now > entry.deadline) {" "  if (false) {"
run "a confirmation for a changed target is refused" \
  "  if (!sameTarget(entry.target, readTarget.target)) {" "  if (false) {"
run "a confirm from a spent utterance authorises nothing" \
  "  if (hasLedgerId(state, LEDGER_KIND.UTTERANCE, utteranceId)) {
    // A spent utterance is spent" "  if (false) {
    // A spent utterance is spent"
run "a command from a spent utterance is refused" \
  "  if (hasLedgerId(state, LEDGER_KIND.UTTERANCE, utteranceId)) {
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (kinds !== undefined && hasLedgerId(state, LEDGER_KIND.ACTION, actionId)) {" \
  "  if (false) {
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (kinds !== undefined && hasLedgerId(state, LEDGER_KIND.ACTION, actionId)) {"
run "a reused action id is refused" \
  "  if (kinds !== undefined && hasLedgerId(state, LEDGER_KIND.ACTION, actionId)) {" "  if (false) {"
run "an abort from a spent utterance is refused" \
  "  if (hasLedgerId(state, LEDGER_KIND.UTTERANCE, utteranceId)) {
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (state.ledger.length + 1 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');" \
  "  if (false) {
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (state.ledger.length + 1 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');"

# A command and the phrase in one utterance: D47's Rule, in the command handler
# and in the abort, so one misrecognition cannot satisfy both halves.
run "command-and-confirm in one utterance (command)" \
  "  if (carriesConfirmToken(text)) {
    // D47's Rule" "  if (false) {
    // D47's Rule"
run "command-and-confirm in one utterance (abort)" \
  "  if (carriesConfirmToken(text)) return refuse(REFUSAL.COMMAND_AND_CONFIRM, 'text');" ""

# What the machine is allowed to do at all: the closed verb set, the verbs the
# 2026-10-17 ruling refuses outright, and the target a destructive verb needs.
run "a verb outside the closed set is refused" \
  "  if (!VERB_VALUES.includes(verb)) return refuse(REFUSAL.UNKNOWN_VERB, 'verb');" ""
run "the kill switch, the budget writes and the deletes are refused outright" \
  "  if (REFUSED_VERBS.includes(verb)) {" "  if (false) {"
run "a destructive verb with no action id is refused" \
  "  } else if (actionId === undefined) {" "  } else if (false) {"
run "an unresolved target is refused, so it can never be read back" \
  "  if (value === undefined || value === null) return refuse(REFUSAL.UNRESOLVED_TARGET, 'target');" \
  "  if (value === undefined) return refuse(REFUSAL.UNRESOLVED_TARGET, 'target');"
run "a launch takes no target" \
  "    if (target !== undefined) return refuse(REFUSAL.VERB_TARGET_MISMATCH, 'target');" ""
run "a launch takes no action id" \
  "    if (actionId !== undefined) return refuse(REFUSAL.VERB_TARGET_MISMATCH, 'actionId');" ""
run "the target's kind must be the verb's kind" \
  "  if (!kinds.includes(kindDescriptor.value)) return refuse(REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');" ""
run "a target with no kind at all is missing a field, not of the wrong kind" \
  "  if (kindDescriptor === undefined) return refuse(REFUSAL.MISSING_FIELD, 'target.kind');" ""

# The ledger and the pending set: bounded, and a refusal rather than an amnesia.
run "the ledger refuses rather than forgetting (command)" \
  "  if (state.ledger.length + 2 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  // A read-back that timed out is dropped here" \
  "  if (false) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  // A read-back that timed out is dropped here"
run "the ledger refuses rather than forgetting (confirm)" \
  "  if (state.ledger.length + 2 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  const pendings = state.pending.filter" \
  "  if (false) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  const pendings = state.pending.filter"
run "the ledger refuses rather than forgetting (launch)" \
  "    if (state.ledger.length + 1 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');" ""
run "the ledger refuses rather than forgetting (abort)" \
  "  if (state.ledger.length + 1 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  const live = sweep(state.pending, values.now);" \
  "  if (false) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  const live = sweep(state.pending, values.now);"
run "the pending set refuses rather than growing past its cap" \
  "  if (live.length + 1 > state.maxPending) return refuse(REFUSAL.PENDING_FULL, 'pending');" ""
run "a read-back at its deadline exactly is still inside it" \
  "  return pendings.filter((one) => now <= one.deadline);" "  return pendings.filter((one) => now < one.deadline);"
run "the ledger is ordered, so a replay cannot be hidden by its position" \
  "  return ordered([...ledger, ...entries.map((entry) => Object.freeze({ ...entry }))], compareLedger);" \
  "  return Object.freeze([...ledger, ...entries.map((entry) => Object.freeze({ ...entry }))]);"

# The read-back: built from the resolved target, naming exactly what will happen.
run "the read-back names the resolved run id" \
  '      what = `Cancel run ${resolved.runId}.`;' "      what = 'Cancel that run.';"
run "the read-back names the repository and the PR" \
  '      what = `Merge ${resolved.repo}#${resolved.pr} at head ${resolved.headSha}.`;' \
  '      what = `Merge ${resolved.repo}#${resolved.pr} at head whatever it is now.`;'
run "the read-back for a confirm names the repository and PR" \
  '      what = `Confirm ${resolved.repo}#${resolved.pr}.`;' "      what = 'Confirm that.';"
run "the read-back for a confirm names the run id" \
  '      what = `Confirm run ${resolved.runId}.`;' "      what = 'Confirm that run.';"
run "the read-back carries the confirming cue" \
  "    return \`\${what} \${CONFIRM_CUE}\`;" "    return what;"

# A state that is not a state: it must agree with its own read-backs, and its
# two sets must be plain, bounded, unique and canonical.
run "the phase must agree with the pending read-backs" \
  "  if (values.phase !== phaseOf(pendings.entries)) return refuse(REFUSAL.INVALID_STATE, 'phase');" ""
run "two pending records for one action id are refused" \
  "    if (seen.has(entry.actionId)) return refuse(REFUSAL.DUPLICATE_PENDING_ENTRY, 'pending');" ""
run "pending records must be in the canonical order" \
  "    if (comparePending(entries[index - 1], entries[index]) >= 0) {" "    if (false) {"
run "two ledger records for one (kind, id) are refused" \
  "    if (seen.has(key)) return refuse(REFUSAL.DUPLICATE_LEDGER_ENTRY, 'ledger');" ""
run "ledger records must be in the canonical order" \
  "    if (compareLedger(entries[index - 1], entries[index]) >= 0) {" "    if (false) {"
run "the pending set is bounded by the state's own cap" \
  "  const items = readArray(value, maxPending);" "  const items = readArray(value, Number.MAX_SAFE_INTEGER);"
run "the ledger is bounded by the state's own cap" \
  "  const items = readArray(value, maxLedger);" "  const items = readArray(value, Number.MAX_SAFE_INTEGER);"
run "a pending record's issuing utterance id must be a key" \
  "      !isKeyId(entry.by) ||" ""
run "a state's pending deadline must be one this module could have minted" \
  "      !isIntegerInRange(entry.deadline - entry.at, MIN_CONFIRM_TIMEOUT_MS, MAX_CONFIRM_TIMEOUT_MS) ||" ""

# Time: it only moves forward, and it does not leap.
run "a now that goes backwards is refused" \
  "  if (values.now < current.state.now) {" "  if (false) {"
run "a now that leaps forward is refused" \
  "  if (values.now - current.state.now > MAX_FORWARD_JUMP_MS) {" "  if (false) {"
run "an event type outside the three is refused" \
  "  if (!Object.hasOwn(HANDLERS, values.type)) return refuse(REFUSAL.UNKNOWN_EVENT, 'type');" ""

# Input hygiene: the field set is the version, a getter is never called, and an
# id is a database key.
run "a key the module does not name is refused" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" "    if (false) {"
run "an accessor property is refused without calling the getter" \
  "    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, where);" \
  "    if (descriptor === undefined) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, where);"
run "a value that is not a plain object is refused" \
  "  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, where);" ""
run "an id is not a database key when it carries a key character" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f) {" "    if (false) {"
run "an id is not a database key when it carries a lone surrogate" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSAL.BAD_KEY, field) };" ""
run "a text may not carry a control character" \
  "    if (code < 0x20 || code === 0x7f) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };" ""
run "an id past its bound is refused" \
  "  if (value.length > ID_MAX) return { bad: refuse(REFUSAL.TOO_LONG, field) };" ""
run "a text past its bound is refused" \
  "  if (value.length > max) return { bad: refuse(REFUSAL.TOO_LONG, field) };" ""
run "an empty id is refused" \
  "  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > ID_MAX)" "  if (value.length > ID_MAX)"
run "an empty text is refused" \
  "  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > max)" "  if (value.length > max)"
run "an absent id is missing, not merely wrong" \
  "  // same failure here: the event did not carry the id it must carry.
  if (value === undefined) return { bad: refuse(REFUSAL.MISSING_FIELD, field) };" \
  "  // same failure here: the event did not carry the id it must carry."
run "an absent text is missing, not merely wrong" \
  "  // carry the text it must carry.
  if (value === undefined) return { bad: refuse(REFUSAL.MISSING_FIELD, field) };" \
  "  // carry the text it must carry."
run "an id of the wrong type is refused, not coerced" \
  "  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > ID_MAX) return { bad: refuse(REFUSAL.TOO_LONG, field) };" \
  "  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > ID_MAX) return { bad: refuse(REFUSAL.TOO_LONG, field) };"
run "a text of the wrong type is refused, not coerced" \
  "  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > max) return { bad: refuse(REFUSAL.TOO_LONG, field) };" \
  "  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > max) return { bad: refuse(REFUSAL.TOO_LONG, field) };"

# NOT proved here, and why: `!Object.is(value, -0)` in isIntegerInRange. Every
# field this module bounds has a minimum of 1, so `-0` is refused by the range
# whether that clause is there or not: removing it cannot change any answer, and
# a mutation that cannot be caught is not evidence about anything. The clause
# stays because shared/lease's own isIntegerInRange has it and this module must
# read a time the same way the rest of the package does.

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
