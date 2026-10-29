#!/bin/bash
# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See mobile/LICENSE.md for the terms.
# Mutation-proves the voice screen's guards (mobile plan, Tasks P3.5 and P6.12).
#
# Almost every rule this task has is of the "does not do it" kind -- nothing is
# spoken over the user, nothing is accepted while offline, nothing is dropped
# to make room, another consumer's lease is not taken over, and (P6.12) an
# action whose read-back nobody heard cannot be confirmed -- and that is the
# kind of rule a test can pass by accident. Each mutation below disables exactly
# ONE guard, in `interrupt.js`, `screen.js`, `speaker.js`, `index.js`,
# `dialogue.js` or the README table, and every one of them must make its own
# suite fail. A guard whose mutation survives is a guard no test defends.
#
# P6.12 is security-sensitive, and its guards are proved at the same depth as the
# rest of the module's: the headline fail-closed rule (a read-back nobody heard
# cannot be confirmed) and then every other rule the dialogue is answerable for
# -- an app command is never forwarded as text, a launch still travels, a command
# that carried its own confirmation is refused, an expired read-back is not
# offered again, a moved target is re-proposed, and a question is marked heard
# only when it was. P6.9, the typed sibling over the same machine, proves 46
# guards the same way; this module is not proved more thinly than its neighbour.
#
#   mobile/src/voice/mutation-proof.sh
#
# It restores each file with `git checkout` after every mutation, so it refuses
# to run against an uncommitted module: `git checkout` on an untracked file
# fails and the mutations would stack. Run it from a clean tree.
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
# The suite is THIS module's tests (`src/voice/*.test.js`) and not mobile's
# package-wide `npm test`: the mutations are all in this module, and a failure
# in a neighbouring module would here be indistinguishable from a killed guard.
# `npm test` is what the pull request reports as the module's verification;
# this script measures the guards, and it says which of the two runners it used
# rather than assuming one.
#
# A guard whose mutation lives outside `screen.js` (the ladder in
# `interrupt.js`, the port mapping in `speaker.js`, the export list in
# `index.js`, the rule table in `mobile/README.md`) names its own file and its
# own suite as two trailing arguments; both default to `screen.js` and the
# module's whole suite. The file is restored with `git checkout` either way, so
# every file a mutation touches must be committed and clean.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no
# "APPLY-FAILED", and exit 0.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd) || exit 1
cd "$root" || exit 1
F=mobile/src/voice/screen.js
SUITE='src/voice/*.test.js'

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
require_clean mobile/src/voice/dialogue.js
require_clean mobile/src/voice/interrupt.js
require_clean mobile/src/voice/speaker.js
require_clean mobile/src/voice/index.js
require_clean mobile/src/voice/index.test.js
require_clean mobile/README.md

last_outcome=""
killed=0
survivors=0
unapplied=0
guards=0

# suite runs this module's tests and returns 0 only for a real green run: the
# runner exited 0, the summary reports at least one test, and nothing failed.
# The pattern is the module's own, run from the package directory so that the
# glob is node's to expand (quoted), exactly as mobile/package.json's script
# does.
#
# It also keeps the names of the tests that failed, because "the suite went red"
# is a claim a reader has to be able to check: the README's mutation table names
# the tests, and the harness prints the same names next to the verdict, so the
# two can be compared rather than trusted.
suite() { # suite-pattern (module-relative to mobile/)
  local out code tests fails
  out=$( (cd mobile && node --test "${1:-$SUITE}") 2>&1 )
  code=$?
  # The summary is `# tests N` under the TAP reporter and `ℹ tests N` under the
  # spec reporter (node 23+ default); both forms are read.
  tests=$(printf '%s\n' "$out" | sed -En 's/^(#|ℹ) tests ([0-9]+)$/\2/p' | tail -1)
  fails=$(printf '%s\n' "$out" | sed -En 's/^(#|ℹ) fail ([0-9]+)$/\2/p' | tail -1)
  # `not ok 3 - the name` under TAP, `✖ the name` under the spec reporter.
  # (`paste -d'; '` would alternate `;` and ` `, which reads as a missing
  # separator, so the join is spelled out.)
  #
  # The spec reporter (node 23+) also prints a **summary header**, `✖ failing
  # tests:`, above the failures it lists in full -- and it is not a test, so
  # reading it as one put a name in the table that no suite has. It is the one
  # `✖` line with no timing beside it and with the reporter's own wording, so
  # it is dropped by name; a test that were really called that would have to be
  # named something else, which is a cheaper price than a table that lies.
  LAST_FAILS=$(printf '%s\n' "$out" \
    | sed -En -e 's/^not ok [0-9]+ - (.*)$/\1/p' -e 's/^[[:space:]]*✖ (.*)$/\1/p' \
    | sed -E 's/ \([0-9.]+m?s\)$//' \
    | sed -E '/^failing tests:$/d' \
    | awk '!seen[$0]++ {printf "%s%s", (n++ ? "; " : ""), $0}')
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
    echo "              went red: ${LAST_FAILS:-<the runner failed before naming a test>}"
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

# 3. The decision, and the order of its judgements (interrupt.js).
run "the ladder is ranked, not compared as a word" \
  "  const mine = severityRank(severity);
  const top = severityRank(highest);" \
  "  const mine = severity;
  const top = highest;" \
  mobile/src/voice/interrupt.js
run "the boundary is judged after the severity" \
  "  if (mine !== top) return decide(false, INTERRUPT_REASON.NOT_HIGHEST);
  if (speaking === true) return decide(false, INTERRUPT_REASON.SPEAKING);
  if (turn === true) return decide(false, INTERRUPT_REASON.USER_TURN);" \
  "  if (speaking === true) return decide(false, INTERRUPT_REASON.SPEAKING);
  if (turn === true) return decide(false, INTERRUPT_REASON.USER_TURN);
  if (mine !== top) return decide(false, INTERRUPT_REASON.NOT_HIGHEST);" \
  mobile/src/voice/interrupt.js
run "the switch is judged first, before the ladder" \
  "  if (setting === INTERRUPT_SWITCH.OFF) return decide(false, INTERRUPT_REASON.SWITCH_OFF);
  if (mine !== top) return decide(false, INTERRUPT_REASON.NOT_HIGHEST);" \
  "  if (mine !== top) return decide(false, INTERRUPT_REASON.NOT_HIGHEST);
  if (setting === INTERRUPT_SWITCH.OFF) return decide(false, INTERRUPT_REASON.SWITCH_OFF);" \
  mobile/src/voice/interrupt.js
run "an utterance in flight is a boundary" \
  "  if (speaking === true) return decide(false, INTERRUPT_REASON.SPEAKING);" "" \
  mobile/src/voice/interrupt.js
run "the user's turn is a boundary" \
  "  if (turn === true) return decide(false, INTERRUPT_REASON.USER_TURN);" "" \
  mobile/src/voice/interrupt.js
run "the switch is validated" \
  "  if (setting !== INTERRUPT_SWITCH.OFF && setting !== INTERRUPT_SWITCH.ON) {
    throw new TypeError('the interrupt switch is INTERRUPT_SWITCH.OFF or .ON');
  }" "" \
  mobile/src/voice/interrupt.js

# 4. The queue, and the four sentences of the plan's verification (screen.js).
#    The patterns carry the lines around them because `drain`'s decision and the
#    `state()` snapshot read alike: a mutation must land in the one it names.
run "the queue is drained only when the switch is on" \
  "      const verdict = interruptDecision({
        switch: switchValue,
        severity: queue[i].severity," \
  "      const verdict = interruptDecision({
        switch: INTERRUPT_SWITCH.ON,
        severity: queue[i].severity,"
run "an update below the digest's top is not spoken" \
  "        highest," "        highest: queue[i].severity,"
run "the pipeline's own speaking() and turn() are what the decision sees" \
  "        highest,
        speaking: pipeline.speaking(),
        turn: pipeline.turn()," \
  "        highest,
        speaking: false,
        turn: false,"
run "an update is queued only against a digest that has arrived" \
  "      if (highest === null) return refuse(SCREEN_CODES.NO_DIGEST, 'digest');" ""
run "an update below the digest's top is queued, not dropped" \
  "      queue.push(Object.freeze({ text: update.text, severity: update.severity }));" \
  "      if (update.severity !== highest) return Object.freeze({ ok: true, queued: queue.length });
      queue.push(Object.freeze({ text: update.text, severity: update.severity }));"
run "a full queue refuses rather than evicting" \
  "      if (queue.length >= queueCap) return refuse(SCREEN_CODES.QUEUE_FULL, 'queue');" \
  "      if (queue.length >= queueCap) queue.shift();"
# NOT here, deliberately: `drain`'s `return` after the one utterance it starts.
# Removing it does not make the suite go red, and the harness says so rather
# than claiming a kill: the loop asks `interruptDecision` again on its next
# pass, and P3.4's fake flips `speaking()` synchronously inside `say`, so the
# second update is refused for exactly the reason the tests already prove. The
# `return` is there for an engine that answers asynchronously, and no test
# written against a synchronous fake can tell the two apart. `mobile/README.md`
# records it in the same words.
run "the utterance ending is a boundary" \
  "  pipeline.on('utterance', () => drain());" ""
run "the turn's final is a boundary" \
  "    if (dialogue === null) void accept(event.message.text);
    else void dialogue(event.message.text);
    drain();" \
  "    if (dialogue === null) void accept(event.message.text);
    else void dialogue(event.message.text);"
run "the turn's final is a message the app accepts" \
  "    if (dialogue === null) void accept(event.message.text);
    else void dialogue(event.message.text);
    drain();" \
  "    drain();"
run "the OS's refusal of the microphone is recorded" \
  "  pipeline.on('unavailable', (event) => {
    microphone = Object.freeze({ ok: false, code: event.code });
  });" ""

# 5. The offline refusal: the plan's own sentence, and the one state it names.
run "the app refuses to accept a message while offline" \
  "    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));" ""
run "the refusal is shown, not only returned" \
  "    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));" \
  "    if (offline()) return refuse(SCREEN_CODES.OFFLINE, 'presence');"
run "only offline refuses: stale is still an orchestrator" \
  "  const offline = () => presence !== null && presence.state === STATE.OFFLINE;" \
  "  const offline = () => presence !== null && presence.state !== STATE.LIVE;"
run "the message is judged before the presence is" \
  "    if (typeof text !== 'string' || text.length === 0) return noteRefusal(refuse(SCREEN_CODES.BAD_TEXT, 'text'));
    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));" \
  "    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));
    if (typeof text !== 'string' || text.length === 0) return noteRefusal(refuse(SCREEN_CODES.BAD_TEXT, 'text'));"
run "an empty message is refused as an empty message" \
  "    if (typeof text !== 'string' || text.length === 0) return noteRefusal(refuse(SCREEN_CODES.BAD_TEXT, 'text'));" ""

# 5b. The refusal's lifetime, and the presence guard on the path the app takes.
#     A review found both of these: `refused` was only ever set, so a snapshot
#     went on rendering `offline` over a message that had just left the phone,
#     and `poll` stored the client's presence without the guard `setPresence`
#     applies -- so the module's own rule was enforced on a path the app does
#     not take.
run "an accepted message clears the refusal it followed" \
  "    if (sent.ok) lastRefused = null;" ""
run "the poll's presence goes through the same guard a caller's does" \
  "        const adopted = setPresence(report.presence);" \
  "        const adopted = Object.freeze({ ok: true });
        presence = report.presence;"
run "a presence outside P1.4's four is refused rather than stored" \
  "    if (!PRESENCE_STATES.includes(value.state)) return refuse(SCREEN_CODES.BAD_PRESENCE, 'state');" ""

# 6. The lease: surfaced, and never enforced (screen.js).
run "a lease another consumer holds is surfaced" \
  "      const taken = take(leaseState ?? emptyLeaseState(), { holder, at: now() });" \
  "      const taken = { ok: true, lease: { holder, at: now() }, state: leaseState ?? emptyLeaseState() };"
run "the state P1.2 hands back is adopted, refused or not" \
  "      leaseState = taken.state;
      leaseRefused = taken.ok ? null : Object.freeze({ code: taken.code, holder: taken.existing?.holder ?? null });" \
  "      leaseRefused = taken.ok ? null : Object.freeze({ code: taken.code, holder: taken.existing?.holder ?? null });"
run "a release is never a takeover" \
  "      const released = release(leaseState, { holder });" \
  "      const released = release(leaseState, { holder: leaseState.claim?.holder ?? holder });"
run "a lease state P1.2 will not read is refused" \
  "      if (!read.ok) return refuse(SCREEN_CODES.BAD_LEASE, 'lease');" ""
run "the lease does not gate the app" \
  "    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));" \
  "    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));
    if (leaseRefused !== null && leaseRefused.code === 'already-claimed') {
      return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'lease'));
    }"

# 7. The guards on the settings themselves.
run "the switch has two values and no third" \
  "      if (value !== INTERRUPT_SWITCH.OFF && value !== INTERRUPT_SWITCH.ON) {
        return refuse(SCREEN_CODES.BAD_SWITCH, 'switch');
      }" ""
run "the digest's severity is on P1.3's ladder" \
  "      if (!SEVERITY_ORDER.includes(value.severity)) return refuse(SCREEN_CODES.BAD_SEVERITY, 'severity');
      highest = value.severity;" \
  "      highest = value.severity;"

# 8. The speaker seam (speaker.js).
run "an interrupted utterance is not a spoken one" \
  "      if (said.ok) return Object.freeze({ ok: true });" \
  "      if (said.ok || said.code === TTS_CODES.INTERRUPTED) return Object.freeze({ ok: true });" \
  mobile/src/voice/speaker.js src/voice/speaker.test.js
run "the one refusal a caller can fix is carried through as itself" \
  "      const code = said.code === TTS_CODES.BAD_TEXT ? SPEAK_CODES.BAD_TEXT : SPEAK_CODES.SPEAK_FAILED;" \
  "      const code = SPEAK_CODES.SPEAK_FAILED;" \
  mobile/src/voice/speaker.js src/voice/speaker.test.js
run "the port refuses a pipeline that is not one" \
  "  if (!isVoicePipeline(pipeline)) throw new TypeError('pipeline is a VoicePipeline');" "" \
  mobile/src/voice/speaker.js src/voice/speaker.test.js

# 9. The door, the walk and the scan (index.js, index.test.js).
run "the door's export list is the module's surface" \
  "export { createPipelineSpeaker } from './speaker.js';" \
  "export { createPipelineSpeaker } from './speaker.js';
export { QUEUE_CAP as QUEUE_LIMIT } from './screen.js';" \
  mobile/src/voice/index.js src/voice/index.test.js
run "the file walk is recursive" \
  "    if (entry.isDirectory()) {
      found.push(...voiceFiles(path, \`\${prefix}\${entry.name}/\`));
    } else if (entry.name.endsWith('.js')) {" \
  "    if (entry.isDirectory()) {
      continue;
    } else if (entry.name.endsWith('.js')) {" \
  mobile/src/voice/index.test.js src/voice/index.test.js
run "the module reads no clock" \
  "  const offline = () => presence !== null && presence.state === STATE.OFFLINE;" \
  "  const offline = () => Date.now() > 0 && presence !== null && presence.state === STATE.OFFLINE;"
run "the module reaches no database of its own" \
  "const UPDATE_FIELDS = Object.freeze(['text', 'severity']);" \
  "const UPDATE_FIELDS = Object.freeze(['text', 'severity']);
const db = () => getDatabase;"

# 10. The README's table, which is the scan's list (the drift this cross-check
#     exists for). P3.4's table is above it in the same file and shares most of
#     its rows, so a pattern here names the voice table's own wording -- the
#     header is deliberately not P3.4's, and the rows that are word-for-word the
#     same carry the row above them as context.
run "the README's rule table is the scan's own header" \
  "| Rule | Exactly what the voice scan forbids | Why |" \
  "| Rule | What the scan forbids | Why |" \
  mobile/README.md src/voice/index.test.js
run "the README's tokens are in the scan's order" \
  "| no clock | \`Date\`, \`performance.now\`, \`hrtime\` | D117: the clock is the server-anchored one the screen is handed, so this module reads none |" \
  "| no clock | \`performance.now\`, \`Date\`, \`hrtime\` | D117: the clock is the server-anchored one the screen is handed, so this module reads none |" \
  mobile/README.md src/voice/index.test.js
run "the README names every token the scan carries" \
  "| no database of its own | \`firebase\`, \`Firestore\`, \`getDatabase\`, \`reference(\`, \`AsyncStorage\` |" \
  "| no database of its own | \`firebase\`, \`Firestore\`, \`getDatabase\`, \`AsyncStorage\` |" \
  mobile/README.md src/voice/index.test.js
run "the README loses no rule row" \
  "| no logging | \`console.\` | a transcript in a log line is a transcript outside the mailbox |
| nothing is read from the environment | \`process.env\` | a screen configured by an environment variable is a screen no test can build the same way twice |" \
  "| nothing is read from the environment | \`process.env\` | a screen configured by an environment variable is a screen no test can build the same way twice |" \
  mobile/README.md src/voice/index.test.js

# 11. P6.12's headline guard (dialogue.js): the read-back nobody heard.
#     The plan's fail-closed rule. A read-back that did not play to the end must
#     be dropped before `propose` answers: leaving it pending makes the fixed
#     phrase a confirmation of something the speaker was never told, which is
#     what D169 is against. Inverting the condition keeps the unspoken read-back
#     confirmable and breaks every read-back that WAS spoken, so the dialogue's
#     suite goes red in both directions.
run "an unheard read-back is dropped, and cannot be confirmed later" \
  "    if (heard === false) {" \
  "    if (heard === true) {" \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

# 12. P6.12 (dialogue.js): the three routes are three, and only agent text (and
#     D162's launch) is ever sent. Widening the branch to the app commands is the
#     failure D164 and D169 are both against: a destructive command -- or the
#     wake-word utterance P6.1 could not route -- reaching the agent as its own
#     text.
run "an app command is never forwarded as text" \
  "      if (result.route === ROUTE.AGENT_TEXT) return await deliver(result.text);" \
  "      if (result.route !== ROUTE.NOT_UNDERSTOOD) return await deliver(result.text);" \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

# 13. P6.12 (dialogue.js): a launch travels (D162). Dropping the send is the gap
#     the typed sibling P6.9 does not have -- it sends the same verb the same way
#     -- and a `propose` that answered a launch without sending it would leave the
#     owner's command nowhere.
run "a launch is sent as the owner's own words" \
  "        const sent = await deliver(text);
        if (sent.ok !== true) return sent;" \
  "        const sent = Object.freeze({ ok: true, outcome: DIALOGUE_OUTCOME.SENT, id: null, at });" \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

# 14. P6.12 (dialogue.js): the command's own words are what the machine reads.
#     P6.2 refuses an utterance that carries a command AND the phrase (a single
#     breath cannot authorise what it just said). Replacing the text with a
#     sentence that cannot contain the phrase disables that check from this side.
run "a command that carried its own confirmation is refused" \
  "        text,
        verb," \
  "        text: 'the command as the app resolved it',
        verb," \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

# 15. P6.12 (dialogue.js): an expired read-back is named and dropped. The machine
#     answers `confirmation-expired`; this module is what keeps the difference
#     from `no-pending-action` (the timeout says so) and what clears the
#     reference, so the phrase said afterwards cannot be about it at all.
run "a timed-out read-back is not offered again" \
  "      if (
        stepped.code === CONFIRM_REFUSAL.CONFIRMATION_EXPIRED
        || stepped.code === CONFIRM_REFUSAL.NO_PENDING_ACTION
      ) {" \
  "      if (stepped.code === CONFIRM_REFUSAL.NO_PENDING_ACTION) {" \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

# 16. P6.12 (dialogue.js): a target that moved is re-proposed, not confirmed.
#     D168's amendment: the phrase may arrive days later, so the sentence that
#     was read back may name a head that is no longer there. Refusing without
#     re-speaking leaves the speaker confirming a head they never heard.
run "a moved target is re-proposed and read back again" \
  "        return rePropose(at, context.target);" \
  "        return refuse(stepped.code, stepped.field);" \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

# 17. P6.12 (dialogue.js): a question is marked heard only when it was. Marking
#     on every real pause records the queued prompts as told to a speaker who
#     heard "A kill switch is set." -- `announcedAt` is when the user last heard
#     the question, and a barge-in means they did not.
run "a question is marked heard only when its announcement played out" \
  "      const marks = announcesQuestions && heard === true;" \
  "      const marks = true;" \
  mobile/src/voice/dialogue.js src/voice/dialogue.test.js

echo
echo "guards proved: $killed killed, $survivors survived, $unapplied not applied"
if [ "$survivors" -ne 0 ] || [ "$unapplied" -ne 0 ] || [ "$killed" -ne "$guards" ]; then
  echo "FAILED: a guard above is not proved by any test" >&2
  exit 1
fi
echo "OK: every guard is killed by a test in its own suite"
