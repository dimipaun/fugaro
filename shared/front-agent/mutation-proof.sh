#!/bin/bash
# Mutation-proves the front agent's turn and its go (mobile plan, Task P5.4.4).
#
# Each mutation below disables exactly ONE guard in turn.js, and every one of them
# must make this module's tests fail. A guard whose mutation survives is a guard
# no test defends -- and this module decides whether a brief may be launched and
# who authorised it, so a surviving guard is a launch nothing holds back.
#
#   shared/front-agent/mutation-proof.sh
#
# The FIRST TWO guards are the ones the task names. P5.4.4's red step is "write
# the 'replayed go authorises nothing' test against a function that counts any
# go, watch it fail; then remove the timeout and watch the 'go after the timeout
# is refused' test fail": the first mutation below leaves the phase rule that
# refuses a second go, and the second deletes the deadline, which is the module
# the red step was written against. They are first so the task's own red step is
# re-runnable after this commit instead of living only in a pull-request
# transcript.
#
# It restores turn.js with `git checkout` after each mutation, so it refuses to
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
# and the difference is deliberate rather than convenient: another module's
# failure would make the positive control below refuse to run at all, and
# calling it "known" would let a real regression hide behind it.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no "APPLY-FAILED"
# (a pattern that no longer matches means this script is stale, not that the
# guard is proved), and exit 0.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/front-agent/turn.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A mise shim for a tool whose version was never set sits on PATH and
# fails when called, which is a runner that would make every verdict below
# fiction.
runner_works() {
  node --version >/dev/null 2>&1
}

# A runner, found rather than assumed. This repository's toolchains come from
# mise (docs/base-image.md: mise's shims are first on PATH, and `mise install`
# runs in the checkout before the build), so a machine that has mise gets node
# from there rather than from any one machine's home directory. node@22 is the
# line package.json's engines declares (>=22 <23).
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
  out=$( (cd shared && node --test "front-agent/*.test.js") 2>&1 )
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
  "function refuse(code, field) {" "function refuse(code, field) { return { ok: true };"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. THE TASK'S RED STEP, first. The phase rule that refuses a second go is
#    disabled -- a module that counts any "go" -- and "a replayed go authorises
#    nothing the second time" must fail on it.
run "RED STEP: a replayed go authorises nothing" \
  "      if (before.spent) return refuse(REFUSAL.REPLAYED_GO, null);" \
  "      if (false) return refuse(REFUSAL.REPLAYED_GO, null);"

# 4. THE TASK'S RED STEP, second: the deadline is deleted, and "a go after the
#    conversation's timeout is refused" must fail on it.
run "RED STEP: a go past the deadline is refused" \
  "      if (before.deadline !== null && now > before.deadline) return refuse(REFUSAL.GO_EXPIRED, null);" \
  "      if (false) return refuse(REFUSAL.GO_EXPIRED, null);"

# The go itself: the event is read off the record, the transcript is the ledger,
# and the authorisation is built from the conversation rather than the utterance.
run "the event is the action id a go carries, not the person's role" \
  "  return record.role === ROLE.PERSON && record.actionId !== undefined;" \
  "  return record.role === ROLE.PERSON;"

run "a conversation that holds a go is spent" \
  "  const spent = turns.some(isGo);" \
  "  const spent = false;"

run "the authorised target is the one that was read back, not one the go names" \
  "      target: Object.freeze({ repo: before.target.repo })," \
  "      target: Object.freeze({ repo: go.text }),"

run "a go carrying a target of its own is refused, not believed" \
  "    if (values.target !== undefined) return refuse(REFUSAL.UNEXPECTED_FIELD, 'target');" \
  "    if (false) return refuse(REFUSAL.UNEXPECTED_FIELD, 'target');"

run "a go with no resolved repository has nothing to launch in" \
  "      if (before.target === null) return refuse(REFUSAL.UNRESOLVED_TARGET, 'target');" \
  "      if (false) return refuse(REFUSAL.UNRESOLVED_TARGET, 'target');"

run "a go with no brief has nothing to launch" \
  "      if (before.brief === null) return refuse(REFUSAL.NO_BRIEF, 'brief');" \
  "      if (false) return refuse(REFUSAL.NO_BRIEF, 'brief');"

run "a launch runs in a mode, and none is minted here" \
  "      if (options.mode === null) return refuse(REFUSAL.MISSING_FIELD, 'mode');" \
  "      if (false) return refuse(REFUSAL.MISSING_FIELD, 'mode');"

run "nothing is answered once the person said go" \
  "      if (reply !== null) return refuse(REFUSAL.UNEXPECTED_REPLY, 'reply');
      if (before.spent) return refuse(REFUSAL.REPLAYED_GO, null);" \
  "      if (before.spent) return refuse(REFUSAL.REPLAYED_GO, null);"

run "a turn in a conversation that took its go is closed" \
  "    if (reply === null) return refuse(REFUSAL.MISSING_REPLY, 'reply');
    if (before.spent) return refuse(REFUSAL.CONVERSATION_CLOSED, null);" \
  "    if (reply === null) return refuse(REFUSAL.MISSING_REPLY, 'reply');"

run "the agent answers every turn, so a turn with no reply is unfinished" \
  "    if (reply === null) return refuse(REFUSAL.MISSING_REPLY, 'reply');" \
  "    if (false) return refuse(REFUSAL.MISSING_REPLY, 'reply');"

# What the router says an utterance is. Every one of these is P6.1's answer read
# rather than a string test: a second reading of the phrase is a second grammar.
run "an utterance the router refuses is never agent text" \
  "  if (!routed.ok) return { code: REFUSAL.NOT_UNDERSTOOD };" \
  "  if (false) return { code: REFUSAL.NOT_UNDERSTOOD };"

run "an app command is not this conversation's to read" \
  "  if (routed.route === ROUTE.APP_COMMAND) return { code: REFUSAL.NOT_AGENT_TEXT };" \
  "  if (false) return { code: REFUSAL.NOT_AGENT_TEXT };"

run "a wake word the grammar does not know is not forwarded" \
  "  if (routed.route === ROUTE.NOT_UNDERSTOOD) {" \
  "  if (false) {"

run "an utterance carrying a command beside the go is refused, not taken" \
  "    .some((word) => CONFIRMATION_PHRASES.includes(word));" \
  "    .some(() => false);"

# The transcript, read: one utterance is one turn, the order is the
# conversation, and a reply is stamped with the clock the call was judged at.
run "one utterance is one turn: a repeated turn id is refused" \
  '    if (ids.has(turn.id)) return refuse(REFUSAL.REPLAYED_TURN, `transcript[${index}].id`);' \
  '    if (false) return refuse(REFUSAL.REPLAYED_TURN, `transcript[${index}].id`);'

run "the order of the transcript is the conversation" \
  '    if (turn.at < lastAt) return refuse(REFUSAL.UNSORTED_TRANSCRIPT, `transcript[${index}].at`);' \
  '    if (false) return refuse(REFUSAL.UNSORTED_TRANSCRIPT, `transcript[${index}].at`);'

run "a reply is stamped with the clock the call was judged at" \
  "  return { ok: true, values: Object.freeze({ id: id.value, role: ROLE.AGENT, at: now, text: text.value, brief }) };" \
  "  return { ok: true, values: Object.freeze({ id: id.value, role: ROLE.AGENT, at: 1, text: text.value, brief }) };"

run "a turn stamped after the clock judging it is refused, not clamped" \
  '  if (!isIntegerInRange(at, 1, now)) return refuse(REFUSAL.INVALID_NOW, `${where}.at`);' \
  '  if (!isIntegerInRange(at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, `${where}.at`);'

run "the transcript is bounded by maxTurns" \
  "  if (length > options.maxTurns) return refuse(REFUSAL.TOO_MANY_TURNS, 'transcript');" \
  "  if (false) return refuse(REFUSAL.TOO_MANY_TURNS, 'transcript');"

# The brief. Its first line is the run's title, and a run with no title is
# refused where it would be launched.
run "the brief's first line must be the run's title" \
  "  if (!line.startsWith(TASK_MARKER)) return { bad: refuse(REFUSAL.BAD_BRIEF, field) };" \
  "  if (false) return { bad: refuse(REFUSAL.BAD_BRIEF, field) };"

run "a brief whose title is empty is refused: a run with no title never leaves" \
  "  if (line.slice(TASK_MARKER.length).trim() === '') return { bad: refuse(REFUSAL.BAD_BRIEF, field) };" \
  "  if (false) return { bad: refuse(REFUSAL.BAD_BRIEF, field) };"

run "a repository is owner/name, shaped so neither half can be a flag" \
  "    if (part.length > 100 || !REPO_SEGMENT_PATTERN.test(part)) return { bad: refuse(REFUSAL.BAD_REPO, 'target.repo') };" \
  "    if (part.length > 100) return { bad: refuse(REFUSAL.BAD_REPO, 'target.repo') };"

# The read-back is recomputed from the resolved values, never stored, and the
# sentence it speaks is the one a person is asked to trust: the repository and
# the mode are this module's values and are spoken first, and the model's title
# is spoken as data.
run "the read-back is spoken only where the go is live" \
  "    outcome: action === null ? OUTCOME.SHAPING : OUTCOME.GO,
    phase: derived.phase,
    target: derived.target,
    brief: derived.brief,
    mode: options.mode,
    readBack: derived.phase === PHASE.READY ? readBack(derived.target, derived.brief, options.mode) : null," \
  "    outcome: action === null ? OUTCOME.SHAPING : OUTCOME.GO,
    phase: derived.phase,
    target: derived.target,
    brief: derived.brief,
    mode: options.mode,
    readBack: null,"

run "the repository and the mode are spoken before the title, out of the values the action carries" \
  'Start in ${target.repo} (${mode} mode): "${title}". ' \
  'Start "${title}" in ${target.repo} (${mode} mode). '

run "a title's quote and parenthesis characters are not spoken as the sentence's own" \
  "  const plain = title.replace(/\"/gu, \"'\").replace(/\(/gu, '[').replace(/\)/gu, ']');" \
  "  const plain = title;"

run "the spoken title is cut to its bound" \
  "  if (characters.length <= SPOKEN_TITLE_MAX) return plain;" \
  "  if (true) return plain;"

run "the spoken title is cut by character, not by code unit" \
  "  return \`\${characters.slice(0, SPOKEN_TITLE_MAX).join('')}...\`;" \
  "  return \`\${plain.slice(0, SPOKEN_TITLE_MAX)}...\`;"

# The input checks that stand between a transcript and a decision.
run "a field read through a getter is refused without ever being called" \
  "    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, where);
    }" \
  "    values[key] = value[key];"

run "a field this module does not name is refused" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" \
  "    if (false) return refuse(REFUSAL.UNKNOWN_FIELD, where);"

run "an invisible format character is refused, not routed" \
  "  return FORMAT_CHARACTER.test(value);" \
  "  return false;"

# The encoding guards, each reached by its own value rather than through another
# field's refusal: an id is a database key, and a text or a brief is what a screen
# shows and the read-back speaks.
run "an id's control character is refused, not stored" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[index]) || code < 0x20 || code === 0x7f || isSeparator(code)) {" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[index]) || isSeparator(code)) {"

run "an id carrying a line separator is refused" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[index]) || code < 0x20 || code === 0x7f || isSeparator(code)) {" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[index]) || code < 0x20 || code === 0x7f) {"

run "an id that is half a character is refused" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSAL.BAD_KEY, field) };" \
  "  if (false) return { bad: refuse(REFUSAL.BAD_KEY, field) };"

run "a text that is half a character is refused" \
  "  if (hasLoneSurrogate(value)) return true;" \
  "  if (false) return true;"

run "a line separator in a text or a brief is refused" \
  "    if (code < 0x20 || code === 0x7f || isSeparator(code)) return true;" \
  "    if (code < 0x20 || code === 0x7f) return true;"

run "the timeout is bounded on both sides" \
  "  if (!isIntegerInRange(ttlMs, MIN_TTL_MS, MAX_TTL_MS)) return refuse(REFUSAL.INVALID_TIMEOUT, 'options.ttlMs');" \
  "  if (false) return refuse(REFUSAL.INVALID_TIMEOUT, 'options.ttlMs');"

run "the biometric switch is a boolean, not a truthy word" \
  "    if (typeof values.biometricOnCostly !== 'boolean') return refuse(REFUSAL.WRONG_TYPE, 'options.biometricOnCostly');" \
  "    if (false) return refuse(REFUSAL.WRONG_TYPE, 'options.biometricOnCostly');"

run "a refusal is frozen" \
  "  return Object.freeze({ ok: false, code, field });" \
  "  return { ok: false, code, field };"

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
