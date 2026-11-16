#!/bin/bash
# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See mobile/LICENSE.md for the terms.
# Mutation-proves the voice switch's guards (mobile plan, Task P5.3.6; D123,
# D127, D111).
#
# Everything this module is trusted for is of the "does NOT do it" kind, which
# is the kind a test can pass by accident: it must NOT build a badge out of a
# consent the device does not hold (the design's §4.3 division, and the plan's
# own sentence), it must NOT say `send` while a call is already going, it must
# NOT let a badge be dismissed or hidden, it must NOT read a broken store as an
# account with no settings, and it must NOT write anywhere but the one settings
# key it owns. Each mutation below disables exactly ONE of those guards, in one
# of the module's four files, and every one must make that file's own suite
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
for file in "$F" "$CONSENT" "$SETTINGS" "$INDICATOR"; do
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

echo
echo "guards proved: $killed killed, $survivors survived, $unapplied not applied"
if [ "$survivors" -ne 0 ] || [ "$unapplied" -ne 0 ] || [ "$killed" -ne "$guards" ]; then
  echo "FAILED: a guard above is not proved by any test" >&2
  exit 1
fi
echo "OK: every guard is killed by a test in its own suite"
