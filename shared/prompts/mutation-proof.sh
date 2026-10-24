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
# The FIRST guard is the headline case of P6.5's own Verification clause: "an
# unanswered prompt expires at D118's mailbox TTL ... decided as a pure function
# of the prompt's `at`, the TTL and the consumer's clock". The mutation below is
# the stub that never expires anything -- a consumer that holds every question
# for ever -- and the test that names the expiry fails on it.
#
# It is NOT a Red step the plan names: P6.5's card has no Red step line (only
# P6.1, P6.2 and P6.13 have one), and an earlier version of this header called
# it one. It is first because it is the clause the task is verified by, and
# keeping it re-runnable inside the harness costs nothing -- an ordering that
# rested on the misattribution would be the only thing wrong with it.
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
# and the difference is deliberate rather than convenient: another module's
# failure would make the positive control below refuse to run at all, and
# calling it "known" would let a real regression hide behind it. That happened
# on Node 22 until 2026-10-24, when shared/url/index.test.js's invalid
# punycode fixture made Node 22's parser throw (the fixture is valid now).
# Narrowing the suite to the module the mutations are in measures the right
# thing.
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
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');" \
  "  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (false) return refuse(REFUSAL.INVALID_NOW, 'now');"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. The headline of P6.5's Verification clause, first: expiry never happens --
#    a consumer that holds every question for ever -- and "an unanswered prompt
#    expires at D118's mailbox TTL ... as a pure function of the prompt's at,
#    the TTL and the consumer's clock" must fail on it. Not a "Red step" the
#    plan names (P6.5's card has none); see the header.
run "the expiry is a pure function of at, the TTL and now" \
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
  "    if (expiredBy(entry.prompt.at, values.now, current.promptTtlMs)) {" \
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
run "a prompt's answer is judged on its own at, not the prompt's" \
  "  if (values.now - at > windowMs) {" \
  "  if (values.now - entry.prompt.at > windowMs) {"

# An answer to a prompt that has already expired is refused, and the expiry is
# what is handed over: an answer to an expired question is an answer to a
# changed world.
run "an answer to a dead prompt is refused, with its expiry" \
  "  if (expiredBy(entry.prompt.at, values.now, current.promptTtlMs)) {
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
  "  if (reply.kind !== 'answer' && !isConfirmation) {
    return refuse(REFUSAL.NOT_AN_ANSWER, 'answer.kind');
  }" \
  "  if (false) {
    return refuse(REFUSAL.NOT_AN_ANSWER, 'answer.kind');
  }"

# §3.5's shape rule, both ways round: an answer to a destructive prompt is
# accepted only as an action record carrying a confirmation -- never as a plain
# `answer`, which the consumer must refuse -- and a confirmation answers only a
# prompt whose effect is destructive.
run "a destructive prompt is answered only by a confirmation" \
  "  if (entry.destructive !== isConfirmation) {
    return refuse(
      isConfirmation ? REFUSAL.NOT_AN_ANSWER : REFUSAL.NOT_A_CONFIRMATION,
      'answer.kind',
    );
  }" \
  "  if (false) {
    return refuse(
      isConfirmation ? REFUSAL.NOT_AN_ANSWER : REFUSAL.NOT_A_CONFIRMATION,
      'answer.kind',
    );
  }"

# The two ways to be the wrong shape are two different refusals: a caller told
# "send a confirmation" and a caller told "this prompt is not waiting for one"
# are being told different things, and one code would blur them.
run "the two shape refusals keep their own codes" \
  "      isConfirmation ? REFUSAL.NOT_AN_ANSWER : REFUSAL.NOT_A_CONFIRMATION," \
  "      isConfirmation ? REFUSAL.NOT_A_CONFIRMATION : REFUSAL.NOT_AN_ANSWER,"

# Kind `action` is D169's record, and reading it as anything else makes the
# confirmation shape unrepresentable -- which is the fault this replaced.
run "an action record is read as a confirmation, not as a plain answer" \
  "  const isConfirmation = reply.kind === 'action';" \
  "  const isConfirmation = false;"

# The command window is measured from the CONFIRMATION's time (§3.5: six hours
# from the confirmation time), not from the envelope's `at` -- and the
# confirmation's time is a reading like any other, so this module's bounds
# apply to it.
run "a confirmation is judged from the confirmation's own time" \
  "    at = reply.action.confirmation.at;" \
  "    at = reply.at;"

run "a confirmation's time is checked, not trusted because the schema took it" \
  "    if (confirmed !== null) return confirmed;" \
  "    if (false) return confirmed;"


# The clock's bounds: a zero timestamp is an unset counter, and a far-future one
# would otherwise read as "inside the window".
run "a zero timestamp is refused, not carried" \
  "  if (value < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, where);" \
  "  if (false) return refuse(REFUSAL.ZERO_TIMESTAMP, where);"

run "a negated zero is refused, not carried as a time" \
  "  if (Object.is(value, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, where);" \
  "  if (false) return refuse(REFUSAL.NEGATIVE_ZERO, where);"

# The write path and the read path are held to the same ceiling, spelled once:
# `now` may be TIME_MAX and the tolerance reaches past it, so a prompt written
# beyond the ceiling would land in a queue every later read refuses -- the
# bricking the clock's own bound exists to prevent, one call over.
run "a timestamp past the module's ceiling is refused on the way in" \
  "  if (value > TIME_MAX) return refuse(REFUSAL.FUTURE_TIMESTAMP, where);" \
  "  if (false) return refuse(REFUSAL.FUTURE_TIMESTAMP, where);"

run "a timestamp from the future beyond the tolerance is refused" \
  "  if (value - now > toleranceMs) return refuse(REFUSAL.FUTURE_TIMESTAMP, where);" \
  "  if (false) return refuse(REFUSAL.FUTURE_TIMESTAMP, where);"

# ... and the answer's own envelope is still one of the readings it checks.
run "an answer's own \`at\` is checked, not assumed" \
  "  const envelope = checkStamp(reply.at, values.now, current.futureToleranceMs, 'answer.at');" \
  "  const envelope = null;"

run "a clock that went backwards is refused" \
  "  if (read.values.now < current.queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');" \
  "  if (false) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');"

# The rule is not only a check on the way in: an accepted `now` is WRITTEN into
# the queue the operation hands back, and that write is what makes the next
# call's `now-went-backwards` possible. A path that accepted a reading and did
# not record it would let the clock step back afterwards -- re-arming reminders
# and reviving a `due` state -- while looking like a queue that never heard the
# later time. One guard per path that moves the watermark, because one path
# forgetting it is the whole fault.
run "enqueue writes the reading it accepted" \
  "      ...current,
      now: values.now,
      prompts: insert(current.prompts, entry)," \
  "      ...current,
      prompts: insert(current.prompts, entry),"

run "expire writes the reading it accepted" \
  "    queue: freezeQueue({ ...current, now: values.now, prompts: kept })," \
  "    queue: freezeQueue({ ...current, prompts: kept }),"

run "answer writes the reading it accepted" \
  "    destructive: entry.destructive,
    queue: freezeQueue({ ...current, now: values.now, prompts })," \
  "    destructive: entry.destructive,
    queue: freezeQueue({ ...current, prompts }),"

run "announce writes the reading it accepted" \
  "    queue: freezeQueue({ ...current, now: values.now, prompts }),
    spoken: Object.freeze(due.map((entry) => spokenItem(marked.get(entry.prompt.id), values.now)))," \
  "    queue: freezeQueue({ ...current, prompts }),
    spoken: Object.freeze(due.map((entry) => spokenItem(marked.get(entry.prompt.id), values.now))),"

# The cross-account reads hold no watermark, so they apply the backwards rule
# and NOT the jump bound -- but they do apply the backwards rule, per
# installation, over every queue they were handed.
# The constructor's result is a frozen value like every other result an entry
# point hands back: this was the one exception, and a caller that could rewrite
# `ok` or swap the queue on the result it was given is a caller whose next call
# this module could not predict.
run "the constructor's result is frozen, queue and all" \
  "  return Object.freeze({
    ok: true,
    queue: freezeQueue({
      account," \
  "  return {
    ok: true,
    queue: freezeQueue({
      account,"

# The reads do NOT apply the TTL: the queue is one the caller has already
# expired, and a listing that quietly dropped a dead prompt would destroy the
# safe answer or the stop the orchestrator is owed exactly once. These three
# mutations are the "helpful" fix the review offered, planted at each of the
# three place a TTL could be applied to a read, to show the tests refuse it --
# and the third is `pendingAcross`, which draws the cross-account view's
# per-installation rows (D170) and could drop a question from one of them
# without any other read noticing.
run "a listing does not hide a prompt the caller has not expired" \
  "  const list = current.prompts
    .map((entry) => listing(entry, values.now))
    .sort(compareForListing);" \
  "  const list = current.prompts
    .filter((entry) => !expiredBy(entry.prompt.at, values.now, current.promptTtlMs))
    .map((entry) => listing(entry, values.now))
    .sort(compareForListing);"

run "a per-installation listing does not hide a prompt the caller has not expired" \
  "    const list = queue.prompts
      .map((entry) => listing(entry, now))
      .sort(compareForListing);" \
  "    const list = queue.prompts
      .filter((entry) => !expiredBy(entry.prompt.at, now, queue.promptTtlMs))
      .map((entry) => listing(entry, now))
      .sort(compareForListing);"

run "an unswept dead prompt can still raise the widen interrupt" \
  "    due: queue.prompts.some((entry) => isDue(entry, values.now))," \
  "    due: queue.prompts.some(
      (entry) => isDue(entry, values.now) && !expiredBy(entry.prompt.at, values.now, queue.promptTtlMs),
    ),"

# A delivery that changed nothing records no reading: it hands back the queue it
# was given, because there is no state for a new watermark to be written into.
run "a duplicate records no reading" \
  "      return Object.freeze({ ok: true, outcome: OUTCOME.DUPLICATE, id: prompt.id, queue: current });" \
  "      return Object.freeze({
        ok: true,
        outcome: OUTCOME.DUPLICATE,
        id: prompt.id,
        queue: freezeQueue({ ...current, now: values.now }),
      });"

run "the count across installations refuses a clock behind one of them" \
  "    if (now < queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
    const list = queue.prompts" \
  "    if (false) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
    const list = queue.prompts"

# The other direction of the same rule: an unbounded leap forward would be
# written as the watermark, and every honest `now` afterwards would come back
# `now-went-backwards`, so one bad reading would brick the queue.
run "a clock that leaps more than a year forward is refused" \
  "  if (read.values.now - current.queue.now > MAX_JUMP_MS) {
    return refuse(REFUSAL.NOW_JUMPED, 'now');
  }" \
  "  if (false) {
    return refuse(REFUSAL.NOW_JUMPED, 'now');
  }"

run "the leap is measured from the watermark, not from the epoch" \
  "  if (read.values.now - current.queue.now > MAX_JUMP_MS) {" \
  "  if (read.values.now > MAX_JUMP_MS) {"

# The bound is the last watermark's, so a reading exactly at it is still an
# honest advance and must be accepted.
run "a leap of exactly the bound is still accepted" \
  "  if (read.values.now - current.queue.now > MAX_JUMP_MS) {" \
  "  if (read.values.now - current.queue.now >= MAX_JUMP_MS) {"

run "the queue's jump bound is P1.1's, not a number of its own" \
  "export const MAX_JUMP_MS = MAX_FORWARD_JUMP_MS;" \
  "export const MAX_JUMP_MS = MAX_TTL_MS;"

# A queue handed back is re-validated, never trusted: a prompt the schema would
# refuse (a mangled mac, say) is not answered from.
run "a queue's prompts are re-validated on every read" \
  "  const validated = validateMessage(entry.prompt);" \
  "  const validated = { ok: true, message: entry.prompt };"

# The schema accepts `at: 0` and any value up to `MAX_SAFE_INTEGER`, so a stored
# prompt has to be re-checked against the module's own `at` bound on the way in;
# otherwise an entry that could never have been written is trusted as read.
run "a stored prompt's \`at\` is re-checked against the module's bound" \
  "  if (!isIntegerInRange(prompt.at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');" \
  "  if (false) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');"

run "a queue is not a bag: an unknown field is refused" \
  "    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);" \
  "    if (false) return refuse(REFUSAL.UNKNOWN_FIELD, where);"

run "an accessor property is refused without being called" \
  "    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {" \
  "    if (false) {"

# A field that is not carried at all is a different fault from one that is
# carried and unknown: `unknown-field` would tell the caller to look for a key
# it never wrote. P1.1 and P6.2 answer `missing-field` here, and so does this
# module -- on the queue's own required fields and on every operation's event.
run "a missing \`now\` is missing-field, not unknown-field" \
  "  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');" \
  "  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');"

run "a missing \`account\` is missing-field, not unknown-field" \
  "  if (account === undefined) return refuse(REFUSAL.MISSING_FIELD, 'account');" \
  "  if (account === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'account');"

run "an event with no \`now\` is missing-field, not unknown-field" \
  "  if (read.values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');" \
  "  if (read.values.now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');"

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

# A prompt announced at its current stage is not due again until its hour has
# passed. The branch looks redundant and is not: a prompt's `at` may lead the
# clock by up to the future tolerance, so at stage 0 the reminder time can still
# be in the future, and without the branch the question is re-read at every
# pause (and, under `widen`, keeps interrupting).
run "a prompt announced at its current stage is not due again" \
  "  if (stage === 0) return false;" \
  "  if (false) return false;"

# The future tolerance takes P1.1's 24-hour ceiling, not the window's seven
# days: it is the bound that decides how far a prompt's `at` may lead the clock.
run "the future tolerance takes P1.1's ceiling, not the window's" \
  "  if (!isIntegerInRange(futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {" \
  "  if (!isIntegerInRange(futureToleranceMs, 0, MAX_WINDOW_MS)) {"

run "a handed-back queue's future tolerance takes the same ceiling" \
  "  if (!isIntegerInRange(values.futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {" \
  "  if (!isIntegerInRange(values.futureToleranceMs, 0, MAX_WINDOW_MS)) {"

run "a prompt whose reminder has come round is due again" \
  "  return entry.announcedAt < reminderAt(entry.prompt.at, stage);" \
  "  return false;"

# The listing order is the queue's own order -- oldest first, then the id --
# because escalation is monotone in `at` and the queue is already held `at`,id.
# There is no stage rule left to mutate: the guard that used to sit here (flip
# the stage comparison) could not be told apart from the `at` comparison below
# it, which is exactly why the rule was deleted. What defends the equivalence is
# the monotonicity of `reminderStage`, mutated next, and the expired list's
# order, after it.
run "the reminder schedule never goes backwards as a prompt ages" \
  "  return 1 + Math.floor((waited - REMINDER_AFTER_MS) / REMINDER_INTERVAL_MS);" \
  "  return 1 + Math.floor((waited - REMINDER_AFTER_MS) / REMINDER_INTERVAL_MS) - (waited > 3 * REMINDER_INTERVAL_MS ? 2 : 0);"

run "an expiry list is reported oldest first" \
  "      expired.push(expiryOf(entry));" \
  "      expired.unshift(expiryOf(entry));"

# The count is across accounts (D168's "Two questions waiting", one sentence for
# the phone), and nothing waiting is an announcement of nothing.
run "the cross-account view is not a bag: an unknown field is refused" \
  "  const time = readClock(event, 'event');
  if (!time.ok) return time;
  const now = time.values.now;

  const accounts = [];" \
  "  const time = { ok: true, values: { now: event === null || typeof event !== 'object' ? undefined : event.now } };
  const now = time.values.now;
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  const accounts = [];"

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

# An accessor has its own code. Sharing `unknown-field` would tell the caller to
# fix a key rather than a property's shape -- and would make this module's
# vocabulary disagree with P1.1's for the same fact.
run "an accessor property has its own code, not the unknown-field one" \
  "      return refuse(REFUSAL.ACCESSOR_PROPERTY, key);" \
  "      return refuse(REFUSAL.UNKNOWN_FIELD, key);"

# The answer's own age is judged against the window its kind takes, and the
# refusal is the code P1.1 answers for that kind: `expired` for a prompt-window
# delivery, `replay` for a command. One code for both would have the app and the
# mailbox report one delivery two different ways.
run "an over-age answer takes its kind's code, not always replay" \
  "    return refuse(
      kind === KIND.PROMPT ? REFUSAL.EXPIRED : REFUSAL.REPLAY,
      isConfirmation ? 'answer.action.confirmation.at' : 'answer.at',
    );" \
  "    return refuse(
      REFUSAL.REPLAY,
      isConfirmation ? 'answer.action.confirmation.at' : 'answer.at',
    );"

# Answering settles the prompt and drops its entry, so what is outstanding is
# what is held and nothing else. Keeping the answered entry would leave a
# settled question pending, and would make the next delivery of the same id a
# duplicate rather than the fresh question this module's README says it is.
run "an answered prompt leaves the queue" \
  "  const prompts = current.prompts.filter((_, position) => position !== index);" \
  "  const prompts = current.prompts;"

# The module reaches for no global, no clock and no builtin: this mutation is
# semantically inert -- `void globalThis` decides nothing -- and is here to prove
# that the source scan in index.test.js is what catches a reach for a global, a
# clock or a builtin, not a change in behaviour.
run "the module reaches for no global, no clock and no builtin" \
  "  const waited = now - at;" \
  "  const waited = now - at; void globalThis;"

# An exception from a hostile input is a refusal, not an exception: the try/catch
# is the whole of that promise, and deleting it fails the suite on the first
# Proxy. P1.1 and P6.2 answer the same code the same way.
run "a hostile input is refused, not raised" \
  "    try {
      return fn(...args);
    } catch {
      return refuse(REFUSAL.INTERNAL_ERROR, null);
    }" \
  "    return fn(...args);"

# And what was thrown is never echoed: it may carry whatever the input put in
# it, which on these inputs could be a name out of a key or a document.
run "the refusal never echoes what was thrown" \
  "    try {
      return fn(...args);
    } catch {
      return refuse(REFUSAL.INTERNAL_ERROR, null);
    }" \
  "    try {
      return fn(...args);
    } catch (error) {
      return refuse(REFUSAL.INTERNAL_ERROR, String(error && error.message));
    }"

# The digest is another module's result, and the one field this policy reads is
# still read through its own descriptor: a plain `digest.severity` calls a getter
# and believes it, which is the one thing an answer about speaking over the user
# cannot rest on.
run "the digest's severity is read through its own descriptor" \
  "  const descriptor = Object.getOwnPropertyDescriptor(digest, 'severity');
  if (descriptor !== undefined && !Object.hasOwn(descriptor, 'value')) {
    return { bad: refuse(REFUSAL.ACCESSOR_PROPERTY, 'digest.severity') };
  }
  const severity = descriptor === undefined ? undefined : descriptor.value;" \
  "  const severity = digest.severity;"

# A schema refusal names a field by its key, and an UNKNOWN field's key is the
# writer's: echoing it would carry a name this module never read back out to
# whoever wrote it. Only a field the schema itself defines is named -- P1.1's
# rule, from the schema's own `FIELDS` -- and the prompt is otherwise reported
# as a whole.
run "an unknown prompt field is not echoed back" \
  "      MESSAGE_FIELDS.includes(validated.field) ? \`prompt.\${validated.field}\` : 'prompt'," \
  "      validated.field === null ? 'prompt' : \`prompt.\${validated.field}\`,"

run "an unknown answer field is not echoed back" \
  "      MESSAGE_FIELDS.includes(validated.field) ? \`answer.\${validated.field}\` : 'answer'," \
  "      validated.field === null ? 'answer' : \`answer.\${validated.field}\`,"

# `isExpired` checks each of its three arguments before it subtracts anything:
# arithmetic on a value that is not a number answers `false` ("still alive") for
# `undefined`, `'x'` and `NaN` and `true` for `null`, and both would hand the
# user the wrong question. Each check is its own guard.
run "isExpired checks \`at\` rather than coercing it" \
  "  if (!isIntegerInRange(at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_AT, 'at');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {" \
  "  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {"

run "isExpired checks \`now\` rather than coercing it" \
  "  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {" \
  "  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {"

run "isExpired checks the TTL rather than coercing it" \
  "  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {
    return refuse(REFUSAL.INVALID_TTL, 'promptTtlMs');
  }
  return expiredBy(at, now, promptTtlMs);" \
  "  return expiredBy(at, now, promptTtlMs);"

# One fault, one code, wherever the module is entered: a clock that is not
# carried at all is `missing-field` on the policy's own input and on the
# cross-account reads, exactly as it is on the queue operations.
run "a policy with no \`now\` is missing-field, not unknown-field" \
  "  if (values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');" \
  "  if (values.now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');"

run "the cross-account reads: a missing \`now\` is missing-field" \
  "  const now = time.values.now;
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');" \
  "  const now = time.values.now;
  if (now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');"

# `reminderStage` is a predicate like `isExpired`, and the stage it answers is
# rendered: each argument is checked, so a value that is not a time cannot
# become `NaN` -- neither 0 nor any stage.
run "reminderStage checks \`at\` rather than coercing it" \
  "  if (!isIntegerInRange(at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_AT, 'at');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  return stageOf(at, now);" \
  "  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  return stageOf(at, now);"

run "reminderStage checks \`now\` rather than coercing it" \
  "  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  return stageOf(at, now);" \
  "  return stageOf(at, now);"

# Absent and null are the same for all three of the policy's signals: nothing to
# report. Only `undefined` meaning that would make one of them refuse where the
# other two answer.
run "an absent and a null caps signal mean the same thing" \
  "  if (caps === undefined || caps === null) return { value: { reached: false, near: false } };" \
  "  if (caps === undefined) return { value: { reached: false, near: false } };"

# Frozen is a rule over the whole surface, not over `initialQueue` alone. The
# test walks every entry point's answer and every entry point's refusal, so
# these two mutations -- one result left unwrapped, one queue's held prompts
# left thawed -- are killed by it rather than by nothing.
run "a listing is frozen like every other answer" \
  "  return Object.freeze({ ok: true, queue: current, pending: Object.freeze(list) });" \
  "  return { ok: true, queue: current, pending: Object.freeze(list) };"

run "a queue's held prompts are frozen with it" \
  "  for (const entry of queue.prompts) Object.freeze(entry);" \
  ""

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
