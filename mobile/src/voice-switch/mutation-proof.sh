#!/bin/bash
# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See mobile/LICENSE.md for the terms.
# Mutation-proves the voice switch's guards (mobile plan, Tasks P5.3.6 and
# P5.3.7; D111, D118, D123, D127).
#
# Everything this module is trusted for is of the "does NOT do it" kind, which
# is the kind a test can pass by accident: it must NOT build a badge out of a
# consent the device does not hold (the design's §4.3 division, and the plan's
# own sentence), it must NOT say `send` while a call is already going, it must
# NOT let a badge be dismissed or hidden, it must NOT read a broken store as an
# account with no settings, and it must NOT write anywhere but the one settings
# key it owns. P5.3.7 adds the egress half's own list: it must NOT open a URL the
# allow-list refused, it must NOT send a token it cannot present, it must NOT
# follow a redirect, it must NOT send while a confirmation is pending or on a
# destructive route or for a project that is not this account's, it must NOT
# send raw text where the redactor's output belongs, and it must NOT retry what
# failed. The voice screen's two additions are mutated here too -- the badge that
# is in the snapshot before anything is spoken, and the engine tag read when the
# utterance arrives. Each mutation below disables exactly ONE of those guards, in
# one of the files named below, and every one must make that file's own suite
# fail. A guard whose mutation survives is a guard no test defends.
#
#   mobile/src/voice-switch/mutation-proof.sh
#
# It restores the mutated file with `git checkout` after each mutation, so it
# refuses to run against uncommitted changes: `git checkout` on an untracked
# file fails and the mutations would stack. Run it from a clean tree, after
# committing the module.
#
# Four things keep it from lying, because "killed" is otherwise a claim about a
# command that may never have run:
#
#   1. it finds a working node or stops, rather than reporting mutations it
#      never ran (the base carries no node of its own, docs/base-image.md: a
#      project's runtimes come from mise, so "on PATH" is not the only place
#      to look);
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
# A mutation names the file it lives in and that file's suite as two trailing
# arguments; both default to `switch.js` and `switch.test.js`. A guard may be
# caught by a neighbouring suite -- `settings.js` judges a record through
# `consent.js`, and `switch.js` renders through `indicator.js` -- so a run may
# name either one; the file is restored with `git checkout` either way, and
# every file a mutation touches must be committed and clean.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no
# "APPLY-FAILED", and exit 0.
set -u

root=$(cd "$(dirname "$0")/../../.." && pwd) || exit 1
cd "$root" || exit 1
F=mobile/src/voice-switch/switch.js
SUITE=src/voice-switch/switch.test.js
CONSENT=mobile/src/voice-switch/consent.js
CONSENT_SUITE=src/voice-switch/consent.test.js
SETTINGS=mobile/src/voice-switch/settings.js
SETTINGS_SUITE=src/voice-switch/settings.test.js
INDICATOR=mobile/src/voice-switch/indicator.js
INDICATOR_SUITE=src/voice-switch/indicator.test.js
HOP=mobile/src/voice-switch/hop.js
HOP_SUITE=src/voice-switch/hop.test.js
EGRESS=mobile/src/voice-switch/egress.js
EGRESS_SUITE=src/voice-switch/egress.test.js
SCREEN=mobile/src/voice/screen.js
SCREEN_SUITE=src/voice/screen.test.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A shim for a tool whose version was never set sits on PATH and fails
# when called, which is a runner that would make every verdict below fiction.
runner_works() {
  node --version >/dev/null 2>&1
}

# A runner, found rather than assumed: PATH first, then mise, where a project
# pins its runtimes. The base carries no node of its own (docs/base-image.md),
# so there is no third place to look.
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
if ! runner_works; then
  echo "REFUSING: no working node (on PATH or through mise);" >&2
  echo "          the base carries none (docs/base-image.md): 'mise use -g node@22' provides" >&2
  echo "          one -- and without a runner the verdicts below would be fiction." >&2
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
for file in "$F" "$CONSENT" "$SETTINGS" "$INDICATOR" "$HOP" "$EGRESS" "$SCREEN"; do
  require_clean "$file"
done

last_outcome=""
killed=0
survivors=0
unapplied=0
guards=0

# suite runs one of this module's suites and returns 0 only for a real green
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

# ---------------------------------------------------------------------------
# The guard this task turns on: the consent the badge is built from is the
# device's own, read from the record (design §4.3, and the plan's sentence that
# a remote call is not attempted without a recorded consent)
# ---------------------------------------------------------------------------

run "RED STEP: the badge trusts the consent the caller asserts, not the record's" \
  "    return renderIndicator({ ...asked, settings: record.settings, consent: record.consent });" \
  "    return renderIndicator({ ...asked, settings: record.settings, consent: context.consent ?? record.consent });"
run "and the switch it reads is the record's too" \
  "    return renderIndicator({ ...asked, settings: record.settings, consent: record.consent });" \
  "    return renderIndicator({ ...asked, settings: context.settings ?? record.settings, consent: record.consent });"
run "a record that cannot be read is the fail-closed badge, not a render of the refusal" \
  "    if (!record.ok) return failClosedIndicator(asked, record);" \
  "    if (!record.ok) return renderIndicator({ ...asked, settings: record.settings, consent: record.consent });"
run "the switch is per direction: setting one leaves the other alone" \
  "      settings: { ...record.settings, [direction]: engine }," \
  "      settings: { stt: engine, tts: engine },"
run "an engine that is not one of the two is refused before anything is written" \
  "    if (!ENGINES.includes(engine)) return refuse(REFUSAL.UNKNOWN_ENGINE, 'engine');" \
  "    if (false) return refuse(REFUSAL.UNKNOWN_ENGINE, 'engine');"
run "and so is a direction the app does not have" \
  "    if (!DIRECTIONS.includes(direction)) return refuse(REFUSAL.UNKNOWN_DIRECTION, 'direction');" \
  "    if (false) return refuse(REFUSAL.UNKNOWN_DIRECTION, 'direction');"
run "a revoke and a set at once take turns over the record (the queue is a chain)" \
  "  const run = previous.then(work, work);" \
  "  const run = Promise.resolve().then(work);"

# ---------------------------------------------------------------------------
# The consent list: one entry per name, judged by the shared rule
# ---------------------------------------------------------------------------

run "re-recording a name replaces the entry instead of appending a second one" \
  "  const kept = asList(consent).filter((entry) => entry.provider !== provider);" \
  "  const kept = asList(consent);" \
  "$CONSENT" "$CONSENT_SUITE"
run "the date is the caller's, not the probe's own" \
  "  const next = [...kept, { provider, at }];" \
  "  const next = [...kept, { provider, at: PROBE_AT }];" \
  "$CONSENT" "$CONSENT_SUITE"
run "a list handed in is judged before it is edited" \
  "  const bad = judgeConsent(consent);
  if (bad !== null) return bad;
  const kept = asList(consent)" \
  "  const bad = judgeConsent(consent);
  if (false) return bad;
  const kept = asList(consent)" \
  "$CONSENT" "$CONSENT_SUITE"
run "revoking judges the list too, rather than emptying what it cannot read" \
  "  const bad = judgeConsent(consent);
  if (bad !== null) return bad;
  const notAName = judgeName(provider);" \
  "  const bad = judgeConsent(consent);
  if (false) return bad;
  const notAName = judgeName(provider);" \
  "$CONSENT" "$CONSENT_SUITE"
run "revoking a name that is not a name is refused, not reported as an edit" \
  "  const notAName = judgeName(provider);
  if (notAName !== null) return notAName;" \
  "  const notAName = judgeName(provider);
  if (false) return notAName;" \
  "$CONSENT" "$CONSENT_SUITE"
run "revoking removes the name it was asked about" \
  "  return Object.freeze({ ok: true, consent: freeze(asList(consent).filter((entry) => entry.provider !== provider)) });" \
  "  return Object.freeze({ ok: true, consent: freeze(asList(consent)) });" \
  "$CONSENT" "$CONSENT_SUITE"

# ---------------------------------------------------------------------------
# The record: one key per account, defaults, and no failure read as a default
# ---------------------------------------------------------------------------

run "an unset switch is local on both directions" \
  "  [DIRECTION.STT]: ENGINE.LOCAL," \
  "  [DIRECTION.STT]: ENGINE.REMOTE," \
  "$SETTINGS" "$SETTINGS_SUITE"
run "a record field this module does not have is refused" \
  "    if (!VOICE_RECORD_FIELDS.includes(key)) return refuse(VOICE_CODES.UNKNOWN_FIELD, 'record');" \
  "    if (false) return refuse(VOICE_CODES.UNKNOWN_FIELD, 'record');" \
  "$SETTINGS" "$SETTINGS_SUITE"
run "the stored values are judged by the shared rule, not copied" \
  "  if (!probe.ok) return refuse(probe.code, probe.field);" \
  "  if (false) return refuse(probe.code, probe.field);" \
  "$SETTINGS" "$SETTINGS_SUITE"
run "a record with no consent list is an empty one, not a dropped field" \
  "    consent: freezeConsent(value.consent ?? [])," \
  "    consent: freezeConsent([])," \
  "$SETTINGS" "$SETTINGS_SUITE"
run "the key cannot be composed from something that is not an account id" \
  "  if (!isAccountId(accountId)) throw new TypeError('not an account id');" \
  "  if (false) throw new TypeError('not an account id');" \
  "$SETTINGS" "$SETTINGS_SUITE"
run "only an absent record is read as the defaults; a refusal is a refusal" \
  "    if (answer.code === SETTINGS_CODES.ABSENT) {" \
  "    if (true) {" \
  "$SETTINGS" "$SETTINGS_SUITE"
run "a refused record is not reported as a written one" \
  "  if (!judged.ok) return judged;" \
  "  if (!judged.ok) return Object.freeze({ ok: true, settings: DEFAULT_VOICE_SETTINGS, consent: Object.freeze([]) });" \
  "$SETTINGS" "$SETTINGS_SUITE"

# ---------------------------------------------------------------------------
# The indicator: persistent, not dismissible, and never a reason to send
# ---------------------------------------------------------------------------

run "a call in flight is not a reason to start a second one" \
  "    send: answer.engine === ENGINE.REMOTE && answer.inFlight === false," \
  "    send: answer.engine === ENGINE.REMOTE," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "only a remote route sends at all" \
  "    send: answer.engine === ENGINE.REMOTE && answer.inFlight === false," \
  "    send: true," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "the badge cannot be dismissed" \
  "  dismissible: false," \
  "  dismissible: true," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "and it is persistent" \
  "  persistent: true," \
  "  persistent: false," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "a refusal is not an answer: a badge that could not be built sends nothing" \
  "    send: false,
    refusal: Object.freeze({ code: refusal.code, field: refusal.field })," \
  "    send: true,
    refusal: Object.freeze({ code: refusal.code, field: refusal.field })," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "and serves locally while it says so" \
  "    engine: ENGINE.LOCAL,
    provider: null,
    reason: REASON.NO_ANSWER,
    send: false," \
  "    engine: ENGINE.REMOTE,
    provider: null,
    reason: REASON.NO_ANSWER,
    send: false," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "there is no answer, which is what the fail-closed badge says" \
  "    reason: REASON.NO_ANSWER,
    send: false," \
  "    reason: REASON.SWITCH_LOCAL,
    send: false," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "the refusal travels with the badge, so a screen can report it" \
  "    refusal: Object.freeze({ code: refusal.code, field: refusal.field })," \
  "    refusal: null," \
  "$INDICATOR" "$INDICATOR_SUITE"
run "a refused input is drawn rather than thrown" \
  "  if (!answer.ok) return failClosedIndicator(input, answer);" \
  "  if (false) return failClosedIndicator(input, answer);" \
  "$INDICATOR" "$INDICATOR_SUITE"

# ---------------------------------------------------------------------------
# The hop client (P5.3.7): one request, checked, with the ID token and nothing
# else -- and a failure that is an answer rather than a retry.
# ---------------------------------------------------------------------------

run "a URL the allow-list refuses is not opened" \
  "    if (!target.ok) return refuse(HOP_CODES.BAD_URL, 'baseUrl', target.code);" \
  "    if (false) return refuse(HOP_CODES.BAD_URL, 'baseUrl', target.code);" \
  "$HOP" "$HOP_SUITE"
run "a token that cannot be presented is not sent" \
  "    if (typeof value !== 'string' || value.length === 0) return refuse(HOP_CODES.NO_TOKEN, 'token');" \
  "    if (false) return refuse(HOP_CODES.NO_TOKEN, 'token');" \
  "$HOP" "$HOP_SUITE"
run "no ambient credential rides along" \
  "        credentials: 'omit'," \
  "        credentials: 'include'," \
  "$HOP" "$HOP_SUITE"
run "a redirect is a failed call, not a call to a host nothing checked" \
  "        redirect: 'error'," \
  "        redirect: 'follow'," \
  "$HOP" "$HOP_SUITE"
run "an oversized answer is refused before it is read" \
  "    if (claimed(response)) return refuse(HOP_CODES.BAD_ANSWER, 'answer');" \
  "    if (false) return refuse(HOP_CODES.BAD_ANSWER, 'answer');" \
  "$HOP" "$HOP_SUITE"
run "an engine name is checked before it can reach the URL" \
  "    if (typeof engine !== 'string' || !ENGINE_NAME.test(engine)) return refuse(HOP_CODES.BAD_ANSWER, 'engine');" \
  "    if (false) return refuse(HOP_CODES.BAD_ANSWER, 'engine');" \
  "$HOP" "$HOP_SUITE"
run "a reason the hop sent and this tree does not know is not adopted" \
  "      if (isPlainObject(parsed) && typeof parsed.error === 'string' && REASONS.includes(parsed.error)) {" \
  "      if (isPlainObject(parsed) && typeof parsed.error === 'string') {" \
  "$HOP" "$HOP_SUITE"
run "a revoked device is the badge's own word, and not a query that failed" \
  "    if (called.detail === HOP_REASON.DEVICE_UNKNOWN) return STATUS_FAILURE.REVOKED_DEVICE;" \
  "    if (called.detail === HOP_REASON.DEVICE_UNKNOWN) return STATUS_FAILURE.QUERY_FAILED;" \
  "$HOP" "$HOP_SUITE"
run "a token that will not verify is an expired token, said so" \
  "    if (called.detail === HOP_REASON.TOKEN_UNVERIFIED) return STATUS_FAILURE.EXPIRED_TOKEN;" \
  "    if (called.detail === HOP_REASON.TOKEN_UNVERIFIED) return STATUS_FAILURE.QUERY_FAILED;" \
  "$HOP" "$HOP_SUITE"
run "an unreachable hop is unreachable, and not a failed query" \
  "  if (called.code === HOP_CODES.UNREACHABLE || called.code === HOP_CODES.BAD_URL) {
    return STATUS_FAILURE.UNREACHABLE;
  }" \
  "  if (false) {
    return STATUS_FAILURE.UNREACHABLE;
  }" \
  "$HOP" "$HOP_SUITE"
# A status answer decorated with this client's own `field` and `detail` is a
# status answer `shared/voice-switch` refuses, and a refused status is the
# fail-closed `local -- checking` badge rather than §4.5's revoked-device row.
run "a status answer is the badge's input and nothing beside it" \
  "      if (!called.ok) return statusFailed(statusFailure(called));" \
  "      if (!called.ok) return refuse(statusFailure(called), 'hop', called.detail ?? called.code);" \
  "$HOP" "$HOP_SUITE"

# ---------------------------------------------------------------------------
# The ten rules (P5.3.7). Each of these is a rule of §5 turned off in one line:
# every one of them must be caught by `egress.test.js`, which is one test per
# rule and the plan's own instruction that the design's list is the test list.
# ---------------------------------------------------------------------------

run "nothing goes while a confirmation is pending" \
  "    if (live.actions.length > 0) return local(direction, EGRESS_CODE.PENDING);" \
  "    if (false) return local(direction, EGRESS_CODE.PENDING);" \
  "$EGRESS" "$EGRESS_SUITE"
run "a state that cannot be read is not a state with nothing pending" \
  "    if (!live.ok) return local(direction, EGRESS_CODE.STATE_UNREADABLE);" \
  "    if (false) return local(direction, EGRESS_CODE.STATE_UNREADABLE);" \
  "$EGRESS" "$EGRESS_SUITE"
run "a destructive route is served locally" \
  "      ACTION_COMMANDS.includes(route.command) ? EGRESS_CODE.DESTRUCTIVE : EGRESS_CODE.NO_COMMAND," \
  "      EGRESS_CODE.NO_COMMAND," \
  "$EGRESS" "$EGRESS_SUITE"
run "an utterance the router did not understand is not a command" \
  "  } else if (route.route === ROUTE.NOT_UNDERSTOOD) {
    return local(direction, EGRESS_CODE.NO_COMMAND);" \
  "  } else if (false) {
    return local(direction, EGRESS_CODE.NO_COMMAND);" \
  "$EGRESS" "$EGRESS_SUITE"
run "a reply the app composed with no route is not a turn to send" \
  "    if (direction === DIRECTION.TTS) return local(direction, EGRESS_CODE.ROUTE_UNREADABLE);" \
  "    if (false) return local(direction, EGRESS_CODE.ROUTE_UNREADABLE);" \
  "$EGRESS" "$EGRESS_SUITE"
run "the app's own other installation is refused by name" \
  "  if (RESERVED_PROJECTS.includes(project)) return local(direction, EGRESS_CODE.OTHER_INSTALLATION);" \
  "  if (false) return local(direction, EGRESS_CODE.OTHER_INSTALLATION);" \
  "$EGRESS" "$EGRESS_SUITE"
run "only the project's own speech goes" \
  "  if (!projects.includes(project)) return local(direction, EGRESS_CODE.FOREIGN_PROJECT);" \
  "  if (false) return local(direction, EGRESS_CODE.FOREIGN_PROJECT);" \
  "$EGRESS" "$EGRESS_SUITE"
run "a badge that did not say send sends nothing" \
  "  if (badge.send !== true) return local(direction, badge.reason ?? REASON.NO_ANSWER);" \
  "  if (false) return local(direction, badge.reason ?? REASON.NO_ANSWER);" \
  "$EGRESS" "$EGRESS_SUITE"
run "a remote route with no provider name is not a route" \
  "  if (typeof badge.provider !== 'string' || badge.provider.length === 0) {" \
  "  if (false) {" \
  "$EGRESS" "$EGRESS_SUITE"
run "the text path sends the redactor's output and never the raw text" \
  "        outgoing = redact(text, secrets);" \
  "        outgoing = text;" \
  "$EGRESS" "$EGRESS_SUITE"
run "a switch that answered what is not a badge is a local turn" \
  "      if (answer.field !== 'badge') return answer;" \
  "      if (true) return answer;" \
  "$EGRESS" "$EGRESS_SUITE"

# ---------------------------------------------------------------------------
# The screen's two additions (P5.3.7): the indicator's place, and the engine on
# each transcript line.
# ---------------------------------------------------------------------------

run "the indicator is in the snapshot before anything is spoken" \
  "  for (const direction of DIRECTIONS) badges[direction] = noBadge(direction);" \
  "  for (const direction of DIRECTIONS) badges[direction] = null;" \
  "$SCREEN" "$SCREEN_SUITE"
run "and the screen asks the switch as it is built" \
  "  void refreshIndicator();" \
  "" \
  "$SCREEN" "$SCREEN_SUITE"
run "a badge source that threw, or answered what is not a badge, keeps the fail-closed badge" \
  "      badges[direction] = isBadge(answer) ? answer : noBadge(direction);" \
  "      badges[direction] = answer;" \
  "$SCREEN" "$SCREEN_SUITE"
run "an engine that cannot be named is no tag at all, and never local" \
  "    return typeof tag === 'string' && tag.length > 0 ? tag : null;" \
  "    return typeof tag === 'string' && tag.length > 0 ? tag : ENGINE.LOCAL;" \
  "$SCREEN" "$SCREEN_SUITE"
run "with no engine port the tag is this device's own engine" \
  "    if (engineOf === null) return ENGINE.LOCAL;" \
  "    if (engineOf === null) return null;" \
  "$SCREEN" "$SCREEN_SUITE"
run "the tag is read when the utterance arrives, and not when it is rendered" \
  "    transcript = Object.freeze({ text: event.text, final: event.final, engine: readEngineTag() });" \
  "    transcript = Object.freeze({ text: event.text, final: event.final, engine: ENGINE.LOCAL });" \
  "$SCREEN" "$SCREEN_SUITE"

echo
echo "guards proved: $killed killed, $survivors survived, $unapplied not applied"
if [ "$survivors" -ne 0 ] || [ "$unapplied" -ne 0 ] || [ "$killed" -ne "$guards" ]; then
  echo "FAILED: a guard above is not proved by any test" >&2
  exit 1
fi
echo "OK: every guard is killed by a test in its own suite"
