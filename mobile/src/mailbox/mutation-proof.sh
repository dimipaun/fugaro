#!/bin/bash
# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See mobile/LICENSE.md for the terms.
# Mutation-proves the mailbox client's guards (mobile plan, Task P3.6).
#
# This module is SECURITY-SENSITIVE and its rules are almost all of the "does
# not do it" kind, which is the kind a test can pass by accident. Each mutation
# below disables exactly ONE guard -- in client.js, or in one of the two files
# the guards outside it live in -- and every one of them must make that guard's
# own suite fail. A guard whose mutation survives is a guard no test
# defends -- and the guards here are the app's whole write path: which paths it
# writes, what it signs, what it verifies, and when it acks.
#
#   mobile/src/mailbox/mutation-proof.sh
#
# It restores client.js with `git checkout` after each mutation, so it refuses
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
# The suite is THIS module's client tests and not mobile's package-wide
# `npm test`: the mutations are all in client.js, `src/**/*.test.js` is a glob
# node expands (which is what makes the package script work at all), and a
# failure in a neighbouring module would here be indistinguishable from a
# killed guard. `npm test` is what the pull request reports as the module's
# verification; this script measures the guards, and it says which of the two
# runners it used rather than assuming one.
#
# A guard whose mutation lives outside `client.js` (the tag comparison in
# `mac.js`, the one-write-path scan in `write-path.test.js`) names its own file
# and its own suite as two trailing arguments; both default to `client.js` and
# its tests. The file is restored with `git checkout` either way, so every file
# a mutation touches must be committed and clean.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no
# "APPLY-FAILED", and exit 0.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd) || exit 1
cd "$root" || exit 1
F=mobile/src/mailbox/client.js
SUITE=src/mailbox/client.test.js

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
require_clean mobile/src/mailbox/gate.js
require_clean mobile/src/mailbox/connection.js
require_clean mobile/src/mailbox/mac.js
require_clean mobile/src/mailbox/store.js
require_clean mobile/src/accounts/secure-store.js
require_clean mobile/src/app/root.js

last_outcome=""
killed=0
survivors=0
unapplied=0
guards=0

# suite runs the client's tests and returns 0 only for a real green run: the
# runner exited 0, the summary reports at least one test, and nothing failed.
# The pattern is the module's own, run from the package directory so that the
# glob is node's to expand (quoted), exactly as mobile/package.json's script
# does.
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
run() { # name, old, [new]
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

# 3. The write path: one message per utterance, on one path, signed, redacted.
run "the fresh-id guard (an id already minted is refused)" \
  "    if (minted.has(id)) return refuse(CLIENT_CODES.ID_CONFLICT, 'newId');" ""
run "the direction the phone signs for" \
  "const tag = await withKey((key) => macOf(base, key, WRITE_DIRECTION));" \
  "const tag = await withKey((key) => macOf(base, key, 'outbox'));"
run "the tag is the keyed one, not a placeholder" \
  "    const record = Object.freeze({ ...base, mac: tag.result });" \
  "    const record = Object.freeze({ ...base, mac: 'f'.repeat(64) });"
run "the text is redacted before it is signed and written" \
  "      ? { id, sender: from, text: body.text, at, kind: 'action', action: opts.action }
      : { id, sender: from, text: body.text, at });" \
  "      ? { id, sender: from, text, at, kind: 'action', action: opts.action }
      : { id, sender: from, text, at });"
run "the message goes to the inbox, and not to the outbox" \
  "    const written = await store.write(paths.inboxMessage(id), record);" \
  "    const written = await store.write(paths.outboxMessage(id), record);"
run "a message that is already there is landed, not a failure" \
  "    if (written.ok || written.code === STORE_CODES.EXISTS) {" \
  "    if (written.ok) {"

# 4. The retry: the same record, until the outbox answers or the TTL passes.
run "the re-send is the same record, never re-stamped" \
  "      const again = await store.write(paths.inboxMessage(id), entry.record);" \
  "      const again = await store.write(paths.inboxMessage(id), { ...entry.record, at: nowMs });"
run "D118's TTL stops the retry" \
  "      if (nowMs - entry.record.at > mailboxTtlMs) {" "      if (false) {"

# 5. What it verifies before it speaks.
run "the outbox tag is verified before anything is spoken" \
  "      if (verdicts.result.outbox[index] !== true) {" "      if (false) {"
run "the heartbeat tag is verified before presence is reported" \
  "    if (verified !== true) {" "    if (false) {"
run "the sender is this mailbox (P2.1's template)" \
  "      if (entry.record.sender !== from) {" "      if (false) {"

# 6. The ack: after the speak, and only once.
run "a failed speak does NOT ack" \
  "          unspoken.push(Object.freeze({ id, code: said.code }));
          continue;" \
  "          unspoken.push(Object.freeze({ id, code: said.code }));"
run "a message already spoken is not spoken again" \
  "      if (!spoken.has(id)) {" "      if (true) {"
run "a message the phone already acked is not spoken again" \
  "      if (entry.record.acked === true) return;" ""
run "the queue: two passes in flight at once do not speak a message twice" \
  "    const run = queue.then(() => fn(), () => fn());" \
  "    const run = fn();"

# 7. What the phone will not write, and the two guards outside client.js: the
#    tag comparison itself, and the scan that keeps D113's one write path.
run "a text nobody can sign is refused before the key is touched" \
  "    if (unsignable !== null) return refuse(CLIENT_CODES.UNSIGNABLE, unsignable);" \
  "    if (false) return refuse(CLIENT_CODES.UNSIGNABLE, unsignable);"
run "the schema validates the record before it is written" \
  "    if (!valid.ok) return refuse(valid.code, valid.field);" \
  "    if (false) return refuse(valid.code, valid.field);"
run "the client object carries its seven names and nothing else" \
  "    pending,
    unsent: unsentText,
  });" \
  "    pending,
    unsent: unsentText,
    raw: (path, value) => store.write(path, value),
  });"
run "the tag comparison is over the whole tag (mac.js)" \
  "  return sameTag(offered, expected);" \
  "  return offered.slice(0, 8) === expected.slice(0, 8);" \
  mobile/src/mailbox/mac.js src/mailbox/mac.test.js
run "a read's node is an own key, whatever it is named (store.js)" \
  "      own(at, rest[rest.length - 1], copy(value));" \
  "      at[rest[rest.length - 1]] = copy(value);" \
  mobile/src/mailbox/store.js src/mailbox/store.test.js
run "an intermediate container is an own key too (store.js)" \
  "        if (child === null || typeof child !== 'object') own(at, rest[i], {});" \
  "        if (child === null || typeof child !== 'object') at[rest[i]] = {};" \
  mobile/src/mailbox/store.js src/mailbox/store.test.js
run "and the client does not lose an outbox message named \`__proto__\` (store.js)" \
  "      own(at, rest[rest.length - 1], copy(value));" \
  "      at[rest[rest.length - 1]] = copy(value);" \
  mobile/src/mailbox/store.js src/mailbox/client.test.js
# 8. The guards the review's last round found with no test behind them: the two
#    clocks, the poll's key refusal, the flush's `exists`-as-landed (the send's
#    has its own above), the read's schema validation, and the mapping's own
#    `@throws` contract.
run "flush refuses a clock that is not a time" \
  "  async function flush() {
    const nowMs = clock();
    if (nowMs === null) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');" \
  "  async function flush() {
    const nowMs = clock();
    if (false) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');"
run "poll refuses a clock that is not a time" \
  "  async function poll() {
    const nowMs = clock();
    if (nowMs === null) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');" \
  "  async function poll() {
    const nowMs = clock();
    if (false) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');"
run "poll refuses an unusable key before it speaks" \
  "    if (!verdicts.ok) return keyRefusal(verdicts);" \
  "    if (false) return keyRefusal(verdicts);"
run "a re-send the rules deny is landed in flush too" \
  "      if (again.ok || again.code === STORE_CODES.EXISTS) {" \
  "      if (again.ok) {"
run "an outbox record the schema refuses is dropped, not verified" \
  "    entries.push({ id, record, valid: validateMessage(record) });" \
  "    entries.push({ id, record, valid: { ok: true } });"
run "the heartbeat mapping demands the fields it maps" \
  "  if (typeof heartbeat.at !== 'number' || typeof heartbeat.boot !== 'string') {" \
  "  if (false) {"
# 9. The three guards this round's review found: the presence reason for a
#    heartbeat P1.4 will not judge, the double's ack at the one position the
#    rules have a rule for, and P3.1's leak fence told apart from a coincidence.
run "a heartbeat P1.4 refuses is reported with this module's reason" \
  "        reason: PRESENCE_REASON.REFUSED," \
  "        reason: state.code,"
run "the store's ack is the one the rules have a rule for" \
  "  return parts.length >= 3 && parts[last] === 'acked' && parts[last - 2] === 'outbox';" \
  "  return parts[last] === 'acked';" \
  mobile/src/mailbox/store.js src/mailbox/store.test.js
run "the store's ack is the one the rules have a rule for (the client's own suite)" \
  "  return parts.length >= 3 && parts[last] === 'acked' && parts[last - 2] === 'outbox';" \
  "  return parts[last] === 'acked';" \
  mobile/src/mailbox/store.js src/mailbox/client.test.js
run "the leak fence searches only for material a match is evidence about" \
  "  return material.length >= LEAK_SEARCH_MIN || delimited(text, material);" \
  "  return true;" \
  mobile/src/accounts/secure-store.js src/accounts/secure-store.test.js
run "the leak fence searches only for material a match is evidence about (the client's own suite)" \
  "  return material.length >= LEAK_SEARCH_MIN || delimited(text, material);" \
  "  return true;" \
  mobile/src/accounts/secure-store.js src/mailbox/client.test.js

run "the store is written in one file only (the scan)" \
  "export function createRoot({ React, store, View, Text, Pressable, useDemo, screens, onLeaveDemo }) {" \
  "export async function aSecondWritePath(store) { return store.write('mailbox/u/inbox/x', 1); }
export function createRoot({ React, store, View, Text, Pressable, useDemo, screens, onLeaveDemo }) {" \
  mobile/src/app/root.js src/mailbox/write-path.test.js

# 10. The offline gate (P6.10; D167, G21). Sixteen guards in client.js, four in
#     gate.js, one in connection.js and one in the store's SDK model -- and the store's is proved
#     twice, once against its own suite and once against the client's, because
#     "the SDK queue would have noticed" is the control the client's claim rests
#     on and a fake that stopped modelling the queue would quietly prove nothing.
run "the connection gate in send is asked at all" \
  "    if (!verdict.ok) {
      return gateRefusal(verdict, { text: body.text, at, keepText: !isAction });" \
  "    if (false) {
      return gateRefusal(verdict, { text: body.text, at, keepText: !isAction });"
run "send judges the LAST poll's heartbeat again, at the time the owner speaks" \
  "evaluateGate({ online: connection.isOnline(), presence: consumerPresence(at) });" \
  "evaluateGate({ online: connection.isOnline(), presence: null });"
run "the poll's observation is kept for the gate" \
  "    observation = Object.freeze({ beat, heartbeat, verified: verdicts.result.heartbeat });" \
  "    observation = null;"
run "the owner is TOLD: the refusal speaks" \
  "    const message = offlineSpeech(verdict.code);
    const said = await speaker.speak(message);" \
  "    const message = offlineSpeech(verdict.code);
    const said = { ok: true };"
run "a gated utterance is kept as unsent text" \
  "    if (keepText) unsent.push(Object.freeze({ text, at, code: verdict.code }));" \
  "    if (false) unsent.push(Object.freeze({ text, at, code: verdict.code }));"
run "a gated ACTION is not kept as unsent text" \
  "      return gateRefusal(verdict, { text: body.text, at, keepText: !isAction });" \
  "      return gateRefusal(verdict, { text: body.text, at, keepText: true });"
run "an action record is recognised as one" \
  "    const isAction = Object.hasOwn(opts, 'action');" \
  "    const isAction = false;"
run "an action that landed is not put in the outbox queue" \
  "      if (!isAction) outbound.set(id, { record, landed: true });" \
  "      outbound.set(id, { record, landed: true });"
run "an action whose write failed is not kept for a retry" \
  "    if (!isAction) outbound.set(id, { record, landed: false });" \
  "    outbound.set(id, { record, landed: false });"
run "flush writes nothing with no connection" \
  "    const verdict = evaluateConnection(connection.isOnline());
    if (!verdict.ok) return refuse(verdict.code, verdict.field);" \
  "    const verdict = evaluateConnection(connection.isOnline());
    if (false) return refuse(verdict.code, verdict.field);"
run "poll does nothing at all with no connection" \
  "    const online = evaluateConnection(connection.isOnline());
    if (!online.ok) return refuse(online.code, online.field);" \
  "    const online = evaluateConnection(connection.isOnline());
    if (false) return refuse(online.code, online.field);"
run "a send refusal the gate did not make carries no sentence either" \
  "    if (text.length > TEXT_MAX) return refuse(CLIENT_CODES.TOO_LONG, 'text');" \
  "    if (text.length > TEXT_MAX) return Object.freeze({ ok: false, code: CLIENT_CODES.TOO_LONG, field: 'text', message: 'that is too long.', told: false });"
run "a refused flush carries no sentence to render or to speak" \
  "    if (!verdict.ok) return refuse(verdict.code, verdict.field);" \
  "    if (!verdict.ok) return Object.freeze({ ok: false, code: verdict.code, field: verdict.field, message: offlineSpeech(verdict.code), told: false });"
run "a refused poll carries no sentence to render or to speak" \
  "    if (!online.ok) return refuse(online.code, online.field);" \
  "    if (!online.ok) return Object.freeze({ ok: false, code: online.code, field: online.field, message: offlineSpeech(online.code), told: false });"
run "the double's constructor is no more generous than the gate (connection.js)" \
  "    this.#online = options.online === undefined ? true : options.online === true;" \
  "    this.#online = options.online !== false;" \
  mobile/src/mailbox/connection.js src/mailbox/connection.test.js
run "a connection that is not \`true\` is offline (gate.js)" \
  "  if (online !== true) return refuse(GATE_CODES.NO_CONNECTION, 'connection');" \
  "  if (online !== true && online !== false) return refuse(GATE_CODES.NO_CONNECTION, 'connection');" \
  mobile/src/mailbox/gate.js src/mailbox/gate.test.js
run "a live consumer must be a VERIFIED one (gate.js)" \
  "  if (presence.state === STATE.LIVE && presence.verified === true) return Object.freeze({ ok: true });" \
  "  if (presence.state === STATE.LIVE) return Object.freeze({ ok: true });" \
  mobile/src/mailbox/gate.js src/mailbox/gate.test.js
run "an unobserved consumer IS a refusal, not a licence (gate.js; the review's M2)" \
  "  if (presence === null || presence === undefined) {
    return refuse(GATE_CODES.CONSUMER_UNOBSERVED, 'presence');
  }" \
  "  if (presence === null || presence === undefined) return Object.freeze({ ok: true });" \
  mobile/src/mailbox/gate.js src/mailbox/gate.test.js
run "the gate is asked AGAIN at the write, so a connection that goes while the Keychain is up is caught (client.js; the review's M1)" \
  "    if (!still.ok) {
      return gateRefusal(still, { text: body.text, at, keepText: !isAction });" \
  "    if (false) {
      return gateRefusal(still, { text: body.text, at, keepText: !isAction });"
run "a client with no connection port is built and REFUSES to send (client.js; the review's BLOCKER)" \
  "  const { keys, store, connection = UNWIRED_CONNECTION, speaker, now, newId, secrets } = options;" \
  "  const { keys, store, connection = { isOnline: () => true }, speaker, now, newId, secrets } = options;"
run "a clock that disagrees is its own reason, not offline's (gate.js)" \
  "    case STATE.CLOCK_DIFFERS:
      return refuse(GATE_CODES.CONSUMER_CLOCK_DIFFERS, 'presence');" \
  "    case STATE.CLOCK_DIFFERS:
      return refuse(GATE_CODES.CONSUMER_OFFLINE, 'presence');" \
  mobile/src/mailbox/gate.js src/mailbox/gate.test.js
run "the store's SDK holds a write made with no connection (store.js)" \
  "    if (this.#offline) {
      this.#queued.set(path, copy(value));
      return Object.freeze({ ok: true });
    }" \
  "    if (false) {
      this.#queued.set(path, copy(value));
      return Object.freeze({ ok: true });
    }" \
  mobile/src/mailbox/store.js src/mailbox/store.test.js
run "the SDK's queue is real, which is what the client's claim rests on (store.js)" \
  "    if (this.#offline) {
      this.#queued.set(path, copy(value));
      return Object.freeze({ ok: true });
    }" \
  "    if (false) {
      this.#queued.set(path, copy(value));
      return Object.freeze({ ok: true });
    }" \
  mobile/src/mailbox/store.js src/mailbox/client.test.js

echo
echo "guards proved: $killed killed, $survivors survived, $unapplied not applied"
if [ "$survivors" -ne 0 ] || [ "$unapplied" -ne 0 ] || [ "$killed" -ne "$guards" ]; then
  echo "FAILED: a guard above is not proved by any test" >&2
  exit 1
fi
echo "OK: every guard is killed by a test in its own suite"
