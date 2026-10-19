#!/bin/bash
# Mutation-proves the text screen's guards (mobile plan, Task P6.9; D167, D164,
# D169, D162, D170).
#
# Half of what this module must do is of the "does NOT do it" kind, which is the
# kind a test can pass by accident: it must NOT send an utterance the router did
# not understand (D164's fail-closed rule), it must NOT send a destructive
# command as text (D169), it must NOT confirm twice or past the deadline or
# against a target that moved, and it must NOT draw one installation's
# questions under another's name or a total across them (D170). Each mutation
# below disables exactly ONE of those guards -- in `input.js` or in `screen.js`
# -- and every one must make that guard's own suite fail. A guard whose mutation
# survives is a guard no test defends.
#
#   mobile/src/text/mutation-proof.sh
#
# It restores the mutated file with `git checkout` after each mutation, so it
# refuses to run against an uncommitted module: `git checkout` on an untracked
# file fails and the mutations would stack. Run it from a clean tree.
#
# Four things keep it from lying, because "killed" is otherwise a claim about a
# command that may never have run:
#
#   1. it finds a working node or stops, rather than reporting mutations it
#      never ran (this repository's base image keeps node off PATH on purpose,
#      docs/base-image.md, so "on PATH" is not the only place to look);
#   2. **the positive control** -- the suite must PASS on the pristine module,
#      with at least one test actually run. `node --test` exits 0 over a glob
#      that matched nothing, so a missing test file would otherwise look exactly
#      like a green suite;
#   3. **the negative control** -- a mutation that must be caught, before any
#      real one is trusted. If the suite cannot fail, nothing below means
#      anything; and
#   4. every mutation asserts that its pattern matched **exactly once** before
#      it is applied, so a stale pattern is APPLY-FAILED and never a verdict.
#
# A mutation that lives in `screen.js` names that file and its own suite as two
# trailing arguments; both default to `input.js` and its tests. A guard may be
# caught by the other suite (the typed read-back is produced by `input.js` and
# drawn by `screen.js`), so a run may name either suite; the file is restored
# with `git checkout` either way, and every file a mutation touches must be
# committed and clean.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no
# "APPLY-FAILED", and exit 0.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd) || exit 1
cd "$root" || exit 1
F=mobile/src/text/input.js
SUITE=src/text/input.test.js
SCREEN_SUITE=src/text/screen.test.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A shim for a tool whose version was never set sits on PATH and fails
# when called, which is a runner that would make every verdict below fiction.
runner_works() {
  node --version >/dev/null 2>&1
}

# A runner, found rather than assumed: PATH first, then mise (the toolchains
# this repository pins live there), then the location this repository's own
# cloud runner keeps node in when it is deliberately not on PATH.
NODE_SOURCE="PATH"
if ! runner_works && command -v mise >/dev/null 2>&1; then
  for tool in node@24 node@22 node; do
    dir=$(mise where "$tool" 2>/dev/null || true)
    if [ -n "$dir" ] && [ -x "$dir/bin/node" ]; then
      PATH="$dir/bin:$PATH"
      NODE_SOURCE="mise $tool"
      break
    fi
  done
fi
if ! runner_works && [ -x /opt/fugaro/node/bin/node ]; then
  PATH="/opt/fugaro/node/bin:$PATH"
  NODE_SOURCE="/opt/fugaro/node/bin (the runner's own node, off PATH)"
fi
if ! runner_works; then
  echo "REFUSING: no working node (on PATH, through mise, or at /opt/fugaro/node);" >&2
  echo "          the suites below would not run, so their verdicts would be fiction." >&2
  exit 2
fi
echo "runner        node $(node --version) from $NODE_SOURCE"

# require_clean refuses to run against a file git cannot restore. `git checkout`
# on an untracked file fails and the mutations would stack.
require_clean() { # file
  if ! git ls-files --error-unmatch "$1" >/dev/null 2>&1; then
    echo "REFUSING: $1 is not committed; git checkout could not restore it" >&2
    exit 2
  fi
  if ! git diff --quiet -- "$1"; then
    echo "REFUSING: $1 has uncommitted changes; commit them first" >&2
    exit 2
  fi
}
require_clean "$F"
require_clean mobile/src/text/screen.js

last_outcome=""
killed=0
survivors=0
unapplied=0
guards=0

# suite runs one of this module's two suites and returns 0 only for a real green
# run: the runner exited 0, the summary reports at least one test, and nothing
# failed. The pattern is quoted so that the glob is node's to expand, exactly as
# mobile/package.json's script does.
suite() { # suite-pattern (module-relative to mobile/)
  local out code tests fails
  out=$( (cd mobile && node --test "${1:-$SUITE}") 2>&1 )
  code=$?
  # The summary is `# tests N` under the TAP reporter and `ℹ tests N` under the
  # spec reporter (node 23+ default); both forms are read.
  tests=$(printf '%s\n' "$out" | sed -En 's/^(#|ℹ) tests ([0-9]+)$/\2/p' | tail -1)
  fails=$(printf '%s\n' "$out" | sed -En 's/^(#|ℹ) fail ([0-9]+)$/\2/p' | tail -1)
  LAST_OUT=$out
  LAST_TESTS=${tests:-0}
  [ "$code" -eq 0 ] && [ "$LAST_TESTS" -ge 1 ] && [ "${fails:-1}" -eq 0 ]
}

# mutate_test applies one mutation, judges the suite against it, and restores
# the module. It counts only the guards it was asked to prove; the caller
# decides what to do with last_outcome.
mutate_test() { # name, old, [new], [file], [suite]
  local name="$1" old="$2" new="${3-}" file="${4-$F}" suite_pattern="${5-$SUITE}"
  git checkout -- "$file"
  if ! python3 - "$file" "$old" "$new" <<'PY'
import sys
p, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(p).read()
assert s.count(old) == 1, ("pattern not found exactly once:", old, s.count(old))
open(p, 'w').write(s.replace(old, new))
PY
  then
    echo "APPLY-FAILED  $name"
    git checkout -- "$file"
    last_outcome=apply-failed
    unapplied=$((unapplied + 1))
    return
  fi
  if git diff --quiet -- "$file"; then
    echo "NO-OP         $name"
    git checkout -- "$file"
    last_outcome=no-op
    unapplied=$((unapplied + 1))
    return
  fi
  if suite "$suite_pattern"; then
    echo "SURVIVED      $name   <-- the tests did not catch this"
    last_outcome=survived
    survivors=$((survivors + 1))
  else
    echo "killed        $name"
    last_outcome=killed
    killed=$((killed + 1))
  fi
  git checkout -- "$file"
}

# run proves one guard and counts it.
run() { # name, old, [new], [file], [suite]
  guards=$((guards + 1))
  mutate_test "$@"
}

# 1. The positive control: the suite passes on the pristine module, over tests
#    that actually ran. Nothing below is measured against anything otherwise.
git checkout -- "$F"
if ! suite "$SUITE"; then
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

# 3. The three routes, and D164's fail-closed rule above all: a wake-word
#    utterance the router did not understand is answered, never forwarded.
run "the router's own refusal is carried, not coerced into a route" \
  "    if (!result.ok) return refuse(result.code, result.field);" \
  "    if (false) return refuse(result.code, result.field);"
run "agent text is the route that sends (and nothing else does)" \
  "    if (result.route === ROUTE.AGENT_TEXT) return send(result.text);" \
  "    if (false) return send(result.text);"
run "a not-understood utterance is answered, never forwarded (D164)" \
  "    if (result.route === ROUTE.NOT_UNDERSTOOD) {" "    if (false) {"
run "the four commands of other screens are named, not answered here" \
  "    return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.ELSEWHERE, command: result.command });" \
  "    return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.PENDING });"
# `UNKNOWN_ROUTE`'s branch is deliberately NOT mutated here: P6.1 has exactly
# three routes and this module compares against all three, so the branch is
# unreachable by construction and no test can kill it. That is said rather than
# hidden -- a mutation that survived because nothing could reach it would read
# as a guard with no test, and this one is a guard with no reachable input.

# 4. What a typed utterance must not do before it is routed or sent.
run "an empty or non-text utterance is refused" \
  "    if (typeof text !== 'string' || text === '') return refuse(INPUT_CODES.BAD_TEXT, 'text');
    if (!isPlainObject(context)) return refuse(INPUT_CODES.BAD_TEXT, 'context');" \
  "    if (false) return refuse(INPUT_CODES.BAD_TEXT, 'text');
    if (!isPlainObject(context)) return refuse(INPUT_CODES.BAD_TEXT, 'context');"
run "a clock that is not a time stops the send" \
  "    const at = clock();
    if (at === null) return refuse(INPUT_CODES.BAD_CLOCK, 'now');

    const result = route(text);" \
  "    const at = clock();
    if (false) return refuse(INPUT_CODES.BAD_CLOCK, 'now');

    const result = route(text);"
run "a context field this module does not read is refused" \
  "      if (!CONTEXT_FIELDS.includes(key)) return refuse(INPUT_CODES.BAD_TEXT, key);" \
  "      if (false) return refuse(INPUT_CODES.BAD_TEXT, key);"

# 5. The client is the one that sends: its id, its stamp, and its refusals.
run "the mailbox client's id and stamp are the ones reported" \
  "    const sent = await mailbox.send(text);
    if (!sent.ok) return refuse(sent.code, sent.field);
    return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.SENT, id: sent.id, at: sent.at });" \
  "    const sent = await mailbox.send(text);
    if (!sent.ok) return refuse(sent.code, sent.field);
    return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.SENT, id: 'made-up', at: at });"
run "a send the client refused keeps the client's code" \
  "    const sent = await mailbox.send(text);
    if (!sent.ok) return refuse(sent.code, sent.field);" \
  "    const sent = await mailbox.send(text);
    if (false) return refuse(sent.code, sent.field);"

# 6. What propose must refuse before the machine ever sees it.
run "a verb the machine reads back is the one that gets an action id" \
  "    const actionId = input.actionId === undefined && destructive ? newId() : input.actionId;" \
  "    const actionId = input.actionId;"
run "propose refuses a field it does not read" \
  "      if (!PROPOSE_FIELDS.includes(key)) return refuse(INPUT_CODES.BAD_TEXT, key);" \
  "      if (false) return refuse(INPUT_CODES.BAD_TEXT, key);"

# 7. The wake-word guard, and D162: a launch passes through the same client.
run "a wake word P6.2's machine cannot confirm with is refused where the app is wired" \
  "    throw new TypeError('createTypedInput: no configured wake word can confirm');" \
  "    void isConfirmPhrase;"
run "a launch goes straight through to the agent (D162)" \
  "    if (stepped.outcome === CONFIRM_OUTCOME.PASS_THROUGH) {
      // D162: a launch goes straight through, and the caps are what bound it.
      return send(text);
    }" \
  "    if (false) {
      return send(text);
    }"

# 8. The confirmation: the phrase confirms the action that was read back, and
#    nothing else -- not a second time, not a target that moved.
run "the phrase is dispatched to the machine, not to another screen" \
  "    if (result.command === COMMAND.CONFIRM) return confirmPhrase(text, at, context);" \
  "    if (false) return confirmPhrase(text, at, context);"
run "the machine's refusal of a confirmation is carried unchanged" \
  "    if (!stepped.ok) return refuse(stepped.code, stepped.field);
    machine = stepped.state;
    return Object.freeze({
      ok: true,
      outcome: INPUT_OUTCOME.CONFIRMED," \
  "    if (false) return refuse(stepped.code, stepped.field);
    machine = stepped.state;
    return Object.freeze({
      ok: true,
      outcome: INPUT_OUTCOME.CONFIRMED,"
run "the target re-stated by the caller is the one the machine compares" \
  "      target: context.target === undefined ? readBackTarget : context.target," \
  "      target: readBackTarget,"
run "the phrase with no action id named takes the LIVE read-back's own" \
  "        actionId = outstanding.actionId;" "        actionId = newId();"
run "and re-states the target that read-back was drawn from" \
  "      readBackTarget = outstanding === undefined ? undefined : outstanding.target;" \
  "      readBackTarget = undefined;"
run "the phrase with nothing outstanding is P6.2's own refusal" \
  "          return refuse(CONFIRM_REFUSAL.NO_PENDING_ACTION, 'actionId');" \
  "          return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.CONFIRMED });"
run "cancel-that with nothing outstanding is a fact, not a discard" \
  "        outcome: stepped.outcome === CONFIRM_OUTCOME.NOTHING_PENDING" \
  "        outcome: false"
run "awaiting() asks the machine, over this module's clock" \
  "    const at = clock();
    if (at === null) return refuse(INPUT_CODES.BAD_CLOCK, 'now');
    const live = confirmPending(machine, at);
    if (!live.ok) return refuse(live.code, live.field);
    return Object.freeze({ ok: true, actions: live.actions });" \
  "    const at = clock();
    if (at === null) return refuse(INPUT_CODES.BAD_CLOCK, 'now');
    return Object.freeze({ ok: true, actions: Object.freeze([]) });"

# 9. D169's gap, asserted rather than described: a confirmed action writes
#    NOTHING, and says so in a named code.
run "a confirmed action is not delivered (the merged client has no action write)" \
  "      delivered: false," "      delivered: true,"
run "and the reason is a named code, not silence" \
  "      code: INPUT_CODES.NO_ACTION_WRITE," "      code: undefined,"
run "the typed cue is the typed one (P6.2's says \"Say\")" \
  "export const TYPE_CUE = 'Type \"Fugaro, confirm\" to confirm.';" \
  "export const TYPE_CUE = CONFIRM_CUE;"
run "the cue is swapped out, not left in place beside a second one" \
  "  const spoken = readBack.slice(0, -CONFIRM_CUE.length).trimEnd();" \
  "  const spoken = readBack;"
run "a read-back that is only the cue is not drawn with a leading space" \
  "  return spoken === '' ? TYPE_CUE : \`\${spoken} \${TYPE_CUE}\`;" \
  "  return \`\${spoken} \${TYPE_CUE}\`;"
run "a value that is not a string is passed through, not coerced or crashed on" \
  "  if (typeof readBack !== 'string') return readBack;" \
  "  if (readBack === null) return readBack;"
run "a sentence this module does not recognise is not one it edits" \
  "  if (!readBack.endsWith(CONFIRM_CUE)) return readBack;" \
  "  if (false) return readBack;"
run "what propose() reads back is the typed sentence, not P6.2's spoken one" \
  "      readBack: typedReadBack(stepped.readBack)," \
  "      readBack: stepped.readBack,"
run "and so is every action awaiting() offers the screen" \
  "        readBack: typedReadBack(one.readBack)," \
  "        readBack: one.readBack,"

# 10. The screen: D170's two halves -- per installation, and never summed.
run "a mutation of one installation's questions is drawn (the section's list is the one given)" \
  "      pending.forEach((one, index) => {" "      [].forEach((one, index) => {" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a destructive question is the only one marked" \
  "        if (one.destructive === true) {" "        if (true) {" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "an installation with nothing waiting says so" \
  "      children.push(el(Text, { key: \`\${account}:nothing\`, type: SCREEN.NOTHING }, NO_PROMPTS));" \
  "" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "nothing is summed across installations (D170)" \
  "    const children = [el(Text, { key: 'title', type: SCREEN.TITLE }, TEXT_TITLE)];" \
  "    const children = [el(Text, { key: 'title', type: SCREEN.TITLE }, \`\${installations.length}\`)];" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "the summaries are the newest first" \
  "    if (a.at !== b.at) return b.at - a.at;" \
  "    if (a.at !== b.at) return a.at - b.at;" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "and no more than the cap of them" \
  "  return Object.freeze(ordered.slice(0, limit));" \
  "  return Object.freeze(ordered.slice(0, ordered.length));" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a row that is not a summary is dropped rather than drawn blank" \
  "      Number.isSafeInteger(one.at)," "      true," \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "the confirmation is one line: a second cue under the read-back is the finding" \
  "          el(Text, { key: 'read-back', type: SCREEN.READ_BACK }, confirmation.readBack)," \
  "          el(Text, { key: 'read-back', type: SCREEN.READ_BACK }, confirmation.readBack),
          el(Text, { key: 'cue', type: SCREEN.CUE }, 'Say it out loud.')," \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a read-back with no sentence is not drawn as an empty confirmation" \
  "      typeof confirmation.readBack === 'string' &&
      confirmation.readBack !== ''" \
  "      typeof confirmation.readBack === 'string'" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "the UNSENT text is shown (D167's first half)" \
  "      if (typeof one.text !== 'string' || one.text === '') return;" "" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a message the database already has is NOT shown as unsent (D167a)" \
  "      if (one.landed === true) return;" "" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a malformed prompt entry is dropped, not a render crash" \
  "    if (prompt === null || typeof prompt !== 'object') return false;" \
  "    if (prompt === null) return false;" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "an entry with no usable prompt id is dropped too" \
  "    return typeof prompt.id === 'string' && prompt.id !== '';" "    return true;" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a malformed installation is skipped rather than crashing the screen" \
  "      if (one === null || typeof one !== 'object') return;
      children.push(section(one));" "      children.push(section(one));" \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "the draft and its handler are the caller's, passed straight through" \
  "        value: typeof props.draft === 'string' ? props.draft : ''," \
  "        value: ''," \
  mobile/src/text/screen.js "$SCREEN_SUITE"
run "a notice is drawn only when there is one" \
  "    if (typeof props.notice === 'string' && props.notice !== '') {" \
  "    if (typeof props.notice === 'string') {" \
  mobile/src/text/screen.js "$SCREEN_SUITE"

echo
echo "guards proved: $killed killed, $survivors survived, $unapplied not applied"
if [ "$survivors" -ne 0 ] || [ "$unapplied" -ne 0 ] || [ "$killed" -ne "$guards" ]; then
  echo "FAILED: a guard above is not proved by any test" >&2
  exit 1
fi
echo "OK: every guard is killed by a test in its own suite"
