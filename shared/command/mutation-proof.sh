#!/bin/bash
# Mutation-proves the wake-word router's guards (mobile plan, Task P6.1).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make this module's tests fail. A guard whose mutation survives is a
# guard no test defends -- and for this module, which decides what counts as an
# app command and what counts as "confirm", a surviving guard is the difference
# between a fence and a comment.
#
#   shared/command/mutation-proof.sh
#
# The FIRST guard is the one the task names. P6.1's red step is "the 'unmatched
# Fugaro utterance is not forwarded' test, failing against a stub router that
# forwards": the mutation below replaces that branch's answer with agent text --
# the stub's behaviour, planted in the real module -- and the test that names it
# fails. It is first so that the task's own red step is re-runnable after this
# commit instead of living only in a pull-request transcript.
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
# `shared/mailbox-sim/mutation-proof.sh` narrows its suite for the same reason.
#
# Expected output: one "killed" line per guard, no "SURVIVED", no "APPLY-FAILED"
# (a pattern that no longer matches means this script is stale, not that the
# guard is proved), and exit 0.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/command/index.js

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
  out=$( (cd shared && node --test "command/*.test.js") 2>&1 )
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

# 2. The negative control: a router that refuses nothing must be caught. If the
#    suite cannot fail, every "killed" below would be a coincidence.
mutate_test "control: refuse() always succeeds (must be killed)" \
  "function refuse(code, field, detail) {" "function refuse(code, field, detail) { return { ok: true, message: {} };"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# 3. THE TASK'S RED STEP, first. The unmatched wake-word branch answers agent
#    text -- a stub router that forwards -- and "an unmatched Fugaro utterance is
#    never forwarded" must fail on it.
run "RED STEP: an unmatched wake-word utterance is not forwarded" \
  "    return notUnderstood(null, REASON.UNMATCHED);" \
  "    return Object.freeze({ ok: true, route: ROUTE.AGENT_TEXT, forwarded: true, command: null, text: utterance });"

# Fail closed: a wake word with an unconfigured name is not understood, and the
# name is never guessed from the list.
run "a name that is not configured is not resolved to the first entry" \
  "    const entry = list.byName.get(match.name);" \
  "    const entry = list.byName.get(match.name) ?? list.values[0];"

# The wake word is the FIRST token, not any token: a mention later in a sentence
# belongs to the agent.
run "the wake word is the utterance's first token, not any token" \
  "  if (!aliases.values.includes(first)) {" \
  "  if (!aliases.values.some((alias) => words.includes(alias))) {"

# The closed grammar: a phrase matches whole words, never a prefix of them, so
# "confirm it" is not "confirm".
run "a command's words match exactly, never as a prefix" \
  "    if (hasSlot ? words.length <= head.length : words.length !== pattern.length) {" \
  "    if (hasSlot ? words.length <= head.length : words.length < pattern.length) {"

# The confirmation vocabulary and the wake-word vocabulary are disjoint by
# construction: a wake word of "yes" would make "yes confirm" a confirmation.
run "a wake word may not be a confirmation word or a yes-word" \
  "const FORBIDDEN_ALIASES = new Set(
  [...CONFIRMATION_PHRASES, ...NOT_CONFIRMATIONS].map((phrase) => fold(phrase)),
);" \
  "const FORBIDDEN_ALIASES = new Set();"

# An empty alias set is refused, never read as "no wake word, so forward
# everything".
run "an empty alias set is refused, not honoured" \
  "  if (raw.length === 0) {" "  if (false) {"

run "a malformed alias is refused" \
  "    if (typeof alias !== 'string' || alias.length === 0 || alias.length > MAX_NAME_LENGTH) {" \
  "    if (false) {"

# The alias set is data, and data a caller can edit at run time is not a closed
# set.
run "the alias set is frozen" \
  "export const WAKE_ALIASES = Object.freeze(['fugaro', 'figaro']);" \
  "export const WAKE_ALIASES = ['fugaro', 'figaro'];"

# Two configured names that fold together are an ambiguity, and an ambiguity is
# refused rather than resolved to whichever entry came last.
run "two names that fold together are refused, not resolved to one" \
  "    if (byName.has(key)) {" "    if (false) {"

run "an entry with an unknown field is refused" \
  "      if (!ENTRY_FIELDS.includes(key)) {" "      if (false) {"

run "an entry id must be a database key" \
  "      !ID_PATTERN.test(id)" "      false"

run "a spoken name with no words in it is refused" \
  "    const key = fold(spokenName);
    if (key === '') {" \
  "    const key = fold(spokenName);
    if (false) {"

# The forwarded payload is the transcript verbatim: folding it would send the
# agent a lowercased, punctuation-stripped copy of what the user said.
run "agent text is forwarded verbatim, not folded" \
  "      text: utterance," "      text: words.join(' '),"

# Review findings on PR #585: the wake word is recognised whatever punctuation
# follows it, invisible format characters are refused, and folding stays
# case-only.
run "punctuation is stripped from both ends of a word, not only a trailing ,.!?" \
  "const EDGE_PUNCTUATION = /^\p{P}+|\p{P}+\$/gu;" \
  "const EDGE_PUNCTUATION = /[,.!?]+\$/g;"

run "clause marks separate words, so 'Fugaro,confirm' begins with the wake word" \
  "const WORD_SEPARATORS = /[\s,;:…–—]+/u;" \
  "const WORD_SEPARATORS = /\s+/u;"

run "an invisible format character is refused, not routed" \
  "  if (FORMAT_CHARACTER.test(value)) {" "  if (false) {"

run "folding is case only: no accent stripping or compatibility normalisation" \
  "    .toLowerCase()
    .split(WORD_SEPARATORS)" \
  "    .normalize('NFKD').replace(/\p{M}/gu, '').normalize('NFKC')
    .toLowerCase()
    .split(WORD_SEPARATORS)"

# The input checks that stand between a transcript and a route.
run "a control character is refused, not routed" \
  "  const bad = badCharacter(utterance, true);
  if (bad === 'control') {" \
  "  const bad = badCharacter(utterance, true);
  if (false) {"

run "an over-long utterance is refused" \
  "  if (utterance.length > MAX_UTTERANCE_LENGTH) {" "  if (false) {"

run "an utterance with no words in it is refused" \
  "  if (words.length === 0) {
    return refuse(REFUSAL.UTTERANCE_BLANK," \
  "  if (false) {
    return refuse(REFUSAL.UTTERANCE_BLANK,"

run "the input is not a bag: an unknown field is refused" \
  "    if (!ROUTE_FIELDS.includes(key)) {" "    if (false) {"

# Refusals are frozen and never carry a route; results are frozen too.
run "a refusal is frozen" \
  "  return Object.freeze({ ok: false, code, field, detail });" \
  "  return { ok: false, code, field, detail };"

run "the agent-text result is frozen" \
  "    return Object.freeze({
      ok: true,
      route: ROUTE.AGENT_TEXT," \
  "    return ({
      ok: true,
      route: ROUTE.AGENT_TEXT,"

run "a resolved entry is a copy, never the caller's object" \
  "    const copy = Object.freeze({ id, spokenName });" \
  "    const copy = entry;"

# What counts as a confirmation. Both halves are load-bearing: the command kind
# alone would make "cancel that" a confirmation, and the forwarded flag alone
# would let a hand-built object stand in for a routed utterance.
run "only the confirm command is a confirmation, not every app command" \
  "    result.command === COMMAND.CONFIRM &&" "    true &&"

run "a confirmation is a result the router forwarded nowhere" \
  "    result.forwarded === false
  );" \
  "    true
  );"

# D163's default agent: the remembered agent is looked up under THIS project.
run "the remembered agent is per project, not the store's first entry" \
  "  const remembered = Object.hasOwn(lastUsedByProject, project) ? lastUsedByProject[project] : undefined;" \
  "  const remembered = Object.values(lastUsedByProject)[0];"

run "nothing advertised is a refusal, not a nameless agent" \
  "  if (agents.values.length === 0) {" "  if (false) {"

run "a malformed local store is refused, not ignored" \
  "  if (!isPlainObject(lastUsedByProject)) {" "  if (false) {"

git checkout -- "$F"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
