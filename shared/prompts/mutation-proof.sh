#!/bin/bash
# Mutation-proves the prompt queue's and the interrupt policy's guards (mobile
# plan, Task P6.5).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make this module's tests fail. A guard whose mutation survives is a
# guard no test defends -- and for this module, which decides whether a question
# is still alive, whether an answer is fresh enough and whether the app may
# speak over someone, a surviving guard is the difference between a fence and a
# comment.
#
#   shared/prompts/mutation-proof.sh
#
# The FIRST guard is the one the task names. P6.5's red step is "an unanswered
# prompt expires at D118's mailbox TTL, as a pure function of the prompt's `at`,
# the TTL and the consumer's clock": the mutation below is the stub that never
# expires anything -- a consumer that holds every question for ever -- and the
# test that names the expiry fails on it. It is first so that the task's own red
# step is re-runnable after this commit instead of living only in a
# pull-request transcript.
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
# `shared/command/mutation-proof.sh` and `shared/mailbox-sim/mutation-proof.sh`
# narrow their suites for the same reason.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no "APPLY-FAILED"
# (a pattern that no longer matches means this script is stale, not that the
# guard is proved), and exit 0.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/prompts/index.js

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
  out=$( (cd shared && node --test "prompts/*.test.js") 2>&1 )
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

# 2. The negative control: a constructor that accepts a `now` it cannot use must
#    be caught. If the suite cannot fail, every "killed" below would be a
#    coincidence.
mutate_test "control: initialQueue() accepts an unusable now (must be killed)" \
  "  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');" \
  "  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');
  if (false) return refuse(REFUSAL.INVALID_NOW, 'now');"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. THE TASK'S RED STEP, first. Expiry never happens -- a consumer that holds
#    every question for ever -- and "an unanswered prompt expires at D118's
#    mailbox TTL, as a pure function of the prompt's at, the TTL and the
#    consumer's clock" must fail on it.
run "RED STEP: expiry is a pure function of at, the TTL and now" \
  "  return now - at > promptTtlMs;" \
  "  return false;"

# The boundary is strict, and it is the same line P1.1 stops accepting at: a
# prompt of age exactly the TTL is alive in one module and expired in the other
# would be two answers to one question.
run "expiry is strictly greater, so age exactly the TTL is alive" \
  "  return now - at > promptTtlMs;" \
  "  return now - at >= promptTtlMs;"

# D118's mailbox TTL, as the owner set it on 2026-10-17: 72 hours. A shorter
# default would expire questions the owner said are still alive.
run "the TTL default is the owner's 72 hours" \
  "export const PROMPT_TTL_MS = 72 * 60 * 60 * 1_000;" \
  "export const PROMPT_TTL_MS = 6 * 60 * 60 * 1_000;"

# An expiry yields the prompt's non-destructive answer, not the stop: D168's
# default is always the safe one, and a stop is what a prompt with no safe
# answer gets.
run "an expiry yields the safe answer, not the stop" \
  "    reply: entry.safe === null ? EXPIRED_REPLY : entry.safe," \
  "    reply: EXPIRED_REPLY,"

run "a prompt with no non-destructive answer stops" \
  "    stop: entry.safe === null," \
  "    stop: false,"

# Expire drops exactly the dead prompts: a queue that expires everything would
# answer questions nobody has stopped waiting for.
run "expire keeps the prompts that are still alive" \
  "    if (isExpired(entry.prompt.at, values.now, current.promptTtlMs)) {" \
  "    if (true) {"

# The two windows (D173). A queued prompt's answer takes the prompt's TTL...
run "an answer to a queued prompt is judged as a prompt, not as a command" \
  "  return destructive ? KIND.COMMAND : KIND.PROMPT;" \
  "  return KIND.COMMAND;"

# ...and an answer to a destructive prompt is a confirmation, which takes the
# command window.
run "a confirmation takes the command window, not the prompt's TTL" \
  "  const windowMs = windowFor(current, kind);" \
  "  const windowMs = windowFor(current, KIND.PROMPT);"

# The confirmation's freshness is its OWN age, not the prompt's: the prompt may
# be days old and the tap a minute ago.
run "a confirmation is judged on the answer's own at" \
  "  if (values.now - reply.at > windowMs) {" \
  "  if (values.now - entry.prompt.at > windowMs) {"

# An answer to a prompt that has already expired is refused, and the expiry is
# what is handed over: an answer to an expired question is an answer to a
# changed world.
run "an answer to a dead prompt is refused, with its expiry" \
  "  if (isExpired(entry.prompt.at, values.now, current.promptTtlMs)) {
    // The prompt died before the answer arrived." \
  "  if (false) {
    // The prompt died before the answer arrived."

# The cap (D168): refused at the boundary, because one prompt past the cap is
# exactly the case the answer to the orchestrator is for.
run "the pending cap refuses the prompt past it" \
  "  if (current.prompts.length >= current.maxPending) {" \
  "  if (false) {"

run "the cap is a fence at the boundary, not one past it" \
  "  if (current.prompts.length >= current.maxPending) {" \
  "  if (current.prompts.length > current.maxPending) {"

# A duplicate is answered, not re-armed: a redelivery must not clear an
# announcement the user has already heard.
run "a redelivery is a duplicate, not a conflict" \
  "    if (held.prompt.sender === prompt.sender && held.prompt.at === prompt.at) {" \
  "    if (false) {"

run "a duplicate leaves the announcement standing" \
  "      return Object.freeze({ ok: true, outcome: OUTCOME.DUPLICATE, id: prompt.id, queue: current });" \
  "      return Object.freeze({ ok: true, outcome: OUTCOME.DUPLICATE, id: prompt.id, queue: freezeQueue({ ...current, now: values.now, prompts: current.prompts.map((entry) => Object.freeze({ ...entry, announcedAt: null })) }) });"

# The local classification: a safe answer the user could never have chosen is
# not an option, and this module will not invent one.
run "a safe answer must be one of the prompt's options" \
  "  if (Array.isArray(prompt.options) && !prompt.options.includes(value)) {" \
  "  if (false) {"

# A prompt is the kind the queue holds, and nothing else.
run "only a prompt is queued" \
  "  if (prompt.kind !== KIND_OF_PROMPT) return refuse(REFUSAL.NOT_A_PROMPT, 'prompt.kind');" \
  "  if (false) return refuse(REFUSAL.NOT_A_PROMPT, 'prompt.kind');"

run "only an answer settles a prompt" \
  "  if (reply.kind !== 'answer') return refuse(REFUSAL.NOT_AN_ANSWER, 'answer.kind');" \
  "  if (false) return refuse(REFUSAL.NOT_AN_ANSWER, 'answer.kind');"

# The clock's bounds: a zero timestamp is an unset counter, and a far-future one
# would otherwise read as "inside the window".
run "a zero timestamp is refused, not carried" \
  "  if (prompt.at < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, 'prompt.at');" \
  "  if (false) return refuse(REFUSAL.ZERO_TIMESTAMP, 'prompt.at');"

run "a negated zero is refused, not carried as a time" \
  "  if (Object.is(prompt.at, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, 'prompt.at');" \
  "  if (false) return refuse(REFUSAL.NEGATIVE_ZERO, 'prompt.at');"

run "a negated zero on an answer is refused too" \
  "  if (Object.is(reply.at, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, 'answer.at');" \
  "  if (false) return refuse(REFUSAL.NEGATIVE_ZERO, 'answer.at');"

run "a prompt from the future beyond the tolerance is refused" \
  "  if (prompt.at - values.now > current.futureToleranceMs) {" \
  "  if (false) {"

run "a clock that went backwards is refused" \
  "  if (read.values.now < current.queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');" \
  "  if (false) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');"

# A queue handed back is re-validated, never trusted: a prompt the schema would
# refuse (a mangled mac, say) is not answered from.
run "a queue's prompts are re-validated on every read" \
  "  const validated = validateMessage(entry.prompt);" \
  "  const validated = { ok: true, message: entry.prompt };"

run "a queue is not a bag: an unknown field is refused" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" \
  "    if (false) return refuse(REFUSAL.UNKNOWN_FIELD, where);"

run "an accessor property is refused without being called" \
  "    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {" \
  "    if (false) {"

run "a handed-back queue may not hold an id twice" \
  "    if (ids.has(id)) return refuse(REFUSAL.DUPLICATE_ENTRY, 'prompts');" \
  "    if (false) return refuse(REFUSAL.DUPLICATE_ENTRY, 'prompts');"

run "a handed-back queue's prompts must be in canonical order" \
  "    if (compareEntries(entries[index - 1], entries[index]) >= 0) {" \
  "    if (false) {"

# The queue's order is total: two prompts asked in the same millisecond cannot
# leave two different queues depending on which arrived first.
run "the canonical order is total, down to the id" \
  "  if (left.prompt.id !== right.prompt.id) return left.prompt.id < right.prompt.id ? -1 : 1;" \
  "  if (left.prompt.id !== right.prompt.id) return 0;"

run "an enqueued prompt is inserted in canonical order, not appended" \
  "  while (index < entries.length && compareEntries(entries[index], entry) < 0) index += 1;" \
  "  while (false) index += 1;"

# Reminder escalation (§3.4): an hour, then once a day, anchored to the prompt.
run "the reminder schedule is an hour, then a day" \
  "  return 1 + Math.floor((waited - REMINDER_AFTER_MS) / REMINDER_INTERVAL_MS);" \
  "  return 1;"

run "the first reminder is due at the hour, inclusive" \
  "  if (waited < REMINDER_AFTER_MS) return 0;" \
  "  if (waited <= REMINDER_AFTER_MS) return 0;"

# What is due: never announced, or this stage's reminder not yet spoken -- and a
# prompt already spoken at its current stage is not spoken again.
run "a prompt never announced is always due" \
  "  if (entry.announcedAt === null) return true;" \
  "  if (entry.announcedAt === null) return false;"

run "a prompt whose reminder has come round is due again" \
  "  return entry.announcedAt < reminderAt(entry.prompt.at, stage);" \
  "  return false;"

# The listing order: the most escalated first, because a re-announced prompt is
# the one the user has been ignoring longest.
run "the listing puts the escalated prompt first" \
  "  if (leftStage !== rightStage) return rightStage - leftStage;" \
  "  if (leftStage !== rightStage) return leftStage - rightStage;"

# The count is across accounts (D168's "Two questions waiting", one sentence for
# the phone), and nothing waiting is an announcement of nothing.
run "the announcement counts every account's prompts" \
  "    count += queue.prompts.length;" \
  "    count = queue.prompts.length;"

run "one question is not spoken as a plural" \
  "  return \`\${word} question\${count === 1 ? '' : 's'} waiting\`;" \
  "  return \`\${word} questions waiting\`;"

run "nothing waiting is no sentence, not a zero" \
  "  if (count === 0) return null;" \
  "  if (false) return null;"

# The interrupt policy (D160, D127): the offline event is a TRANSITION.
run "the offline event is a transition, not a first sighting" \
  "  const offline =
    values.state === PRESENCE.OFFLINE &&
    previous !== undefined &&
    previous !== null &&
    previous !== PRESENCE.OFFLINE;" \
  "  const offline = values.state === PRESENCE.OFFLINE;"

# The loudest event is the one reported, in D160's own order.
run "a kill switch outranks every other event" \
  "  if (kill) return INTERRUPT.KILL;" \
  "  if (false) return INTERRUPT.KILL;"

# Narrowing keeps the two events that mean the work or the money has stopped,
# and drops the offline transition and the local threshold.
run "narrowing keeps only the kill and the reached cap" \
  "  if (setting === SWITCH.NARROW) return null;" \
  "  if (setting === SWITCH.NARROW && !near) return null;"

# Widening adds "any prompt" -- which is the one place a waiting question may
# interrupt, and only when the user has not already heard it.
run "widening adds a due prompt, and the default does not" \
  "  if (setting === SWITCH.WIDEN && due) return INTERRUPT.PROMPT;" \
  "  if (due) return INTERRUPT.PROMPT;"

# The severity ladder is read, never guessed: a value nobody defined must not
# rank as "not a kill".
run "an unknown severity is refused, not ranked" \
  "  try {
    severityRank(severity);
  } catch {" \
  "  try {
    void severity;
  } catch {"

run "the switch must be one of its three settings" \
  "  if (!SWITCH_VALUES.includes(setting)) return refuse(REFUSAL.INVALID_SWITCH, 'switch');" \
  "  if (false) return refuse(REFUSAL.INVALID_SWITCH, 'switch');"

run "the cap signals must be booleans, not truthy values" \
  "    if (values[field] !== undefined && typeof values[field] !== 'boolean') {" \
  "    if (false) {"

# Every refusal is frozen, like every other value this module returns.
run "a refusal is frozen" \
  "  return Object.freeze({ ok: false, code, field });" \
  "  return { ok: false, code, field };"

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
