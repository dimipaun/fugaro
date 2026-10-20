#!/bin/bash
# Mutation-proves the mailbox-schema guards (mobile plan, Task P0.2; extended
# by P6.3).
#
# Each mutation below disables exactly ONE guard in index.js — or, for the four
# at the end, in mac.js, the tag's own module — and every one of them must make
# `npm test` fail. A guard whose mutation survives is a guard no test defends,
# which for this module -- the protocol P1.1, P3.6 and P4.1 speak -- is the
# difference between a fence and a comment. Section 6 is the group about the
# bounds: most of its rows move a comparison by one, which is the mutation this
# module has actually been wrong about, and three of them narrow a rule by
# removing one of its terms instead (DEL from the two character rules, and the
# second head-SHA length from the SHA rule).
#
#   shared/mailbox-schema/mutation-proof.sh
#
# It restores the module with `git checkout` after each mutation, so it refuses
# to run against an uncommitted one: `git checkout` on an untracked file fails
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
#
# P6.3's guards are the second half: the four kinds and their (kind, field)
# table, the agent/option/action shapes, the argv-shaped targets, the required
# tag, and the canonical serialisation the tag covers. They are the ones a
# reviewer should read twice, because they are the ones an impostor holding a
# minted token (G13) but not the per-pairing key (D171) would attack.
#
# Two files, since P6.3's review: the tag lives in mac.js, which imports node's
# crypto, and index.js imports NOTHING — a static import of a node builtin
# there would make the schema unimportable by the phone (Metro resolves no
# `node:` builtin, Hermes has no Buffer). The entry point's freedom from imports
# is itself guarded below, because nothing else in the suite would notice it.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
F=shared/mailbox-schema/index.js
M=shared/mailbox-schema/mac.js

# runner_works asks the question that matters: not "is node on PATH" but "does
# it run". A mise shim for a tool whose version was never set sits on PATH and
# fails when called, which is a runner that would make every verdict below
# fiction.
runner_works() {
  node --version >/dev/null 2>&1 && npm --version >/dev/null 2>&1
}

# A runner, found rather than assumed. A working one on PATH is the ordinary
# case; this repository's base image keeps node off PATH on purpose
# (docs/base-image.md), and mise holds the toolchains, so a machine that has
# mise gets one from there rather than from any one machine's home directory.
# node@22 is the line package.json's engines declares (>=22 <23).
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
  echo "REFUSING: no working node and npm (on PATH, or through mise);" >&2
  echo "          the suites below would not run, so their verdicts would be fiction." >&2
  exit 2
fi

if ! git ls-files --error-unmatch "$F" >/dev/null 2>&1; then
  echo "REFUSING: $F is not committed; git checkout could not restore it" >&2
  exit 2
fi
if ! git ls-files --error-unmatch "$M" >/dev/null 2>&1; then
  echo "REFUSING: $M is not committed; git checkout could not restore it" >&2
  exit 2
fi
if ! git diff --quiet -- "$F" "$M"; then
  echo "REFUSING: $F or $M has uncommitted changes; commit them first" >&2
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
# decides what to do with last_outcome. The fourth argument is the file to
# mutate: index.js by default, mac.js for the tag's own guards.
mutate_test() { # name, old, [new], [file]
  local name="$1" old="$2" new="${3-}" file="${4-$F}"
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
  if suite; then
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
run() { # name, old, [new], [file]
  guards=$((guards + 1))
  mutate_test "$1" "$2" "${3-}" "${4-$F}"
}

# 1. The positive control: the suite passes on the pristine module, over tests
#    that actually ran. Nothing below is measured against anything otherwise.
git checkout -- "$F" "$M"
if ! suite; then
  echo "REFUSING: the suite does not pass on the pristine $F ($LAST_TESTS tests ran);" >&2
  echo "          a mutation's failure could not be told from the baseline's." >&2
  printf '%s\n' "$LAST_OUT" | tail -20 >&2
  exit 2
fi
echo "control       the pristine suite passes, $LAST_TESTS tests, 0 failures"

# 2. The negative control: a validator that refuses nothing must be caught. If
#    the suite cannot fail, every "killed" below would be a coincidence.
mutate_test "control: refuse() always succeeds (must be killed)" \
  "function refuse(code, field) {" "function refuse(code, field) { return { ok: true, message: {} };"
if [ "$last_outcome" != killed ]; then
  echo "REFUSING: the control mutation was not caught, so this harness is not measuring anything" >&2
  exit 2
fi
# The control's own kill is its evidence; the tallies below are the guards'.
killed=0

# ---------------------------------------------------------------------------
# 3. P0.2's guards: the value itself, the id, the strings, the time, the ack,
#    and the two properties of the accepted object.
# ---------------------------------------------------------------------------

# The value itself: not an object, an unknown field, a missing field.
run "not-an-object guard (null/array/primitive)" \
  "export function validateMessage(value) {
  if (!isPlainObject(value)) {" "export function validateMessage(value) {
  if (false) {"
run "unknown-field guard" \
  "    if (!FIELDS.includes(key)) {" "    if (false) {"
run "missing-field guard" \
  "  for (const key of REQUIRED_FIELDS) {
    if (!Object.hasOwn(value, key)) {" "  for (const key of REQUIRED_FIELDS) {
    if (false) {"

# The id: its type, its emptiness, its length, the database-key rule, and the
# surrogate rule that keeps it encodable.
run "id: type guard" \
  "function keyField(value, field) {
  if (typeof value !== 'string')" "function keyField(value, field) {
  if (false)"
run "id: empty guard" \
  "  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, field) };
  if (value.length > ID_MAX)" "  if (value.length > ID_MAX)"
run "id: length bound" \
  "  if (value.length > ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, field) };" ""
run "id: database-key character guard" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f || isSeparator(value, i)) {" "    if (false) {"
run "id: lone-surrogate guard" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_KEY, field) };" ""

# The bounded strings: type, emptiness, bound, control characters, surrogates.
run "string: type guard" \
  "function stringField(value, field, max, lineBreaks) {
  if (typeof value !== 'string')" "function stringField(value, field, max, lineBreaks) {
  if (false)"
run "string: empty guard" \
  "  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, field) };
  if (value.length > max)" "  if (value.length > max)"
run "string: length bound" \
  "  if (value.length > max) return { bad: refuse(REFUSALS.TOO_LONG, field) };" ""
run "string: control-character guard" \
  "    if ((code < 0x20 && !line) || code === 0x7f || isSeparator(value, i)) {" "    if (false) {"
run "string: lone-surrogate guard" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };" ""

# The time, the ack, and the two properties of the accepted object: frozen, and
# a copy with the caller's values verbatim.
run "timestamp: type guard" \
  "  if (typeof value !== 'number') return { bad: refuse(REFUSALS.WRONG_TYPE, field) };" ""
run "timestamp: safe-integer/range guard" \
  "  if (!Number.isSafeInteger(value) || value < 0) return { bad: refuse(REFUSALS.BAD_TIMESTAMP, field) };" ""
run "acked: boolean guard" \
  "    if (typeof acked !== 'boolean') {" "    if (false) {"
run "the message is frozen" \
  "  return { ok: true, message: deepFreeze(message) };" "  return { ok: true, message };"
run "the copy is DEEP: a nested record is frozen too (P6.3)" \
  "function deepFreeze(value) {
  if (value === null || typeof value !== 'object') return value;
  for (const key of Object.keys(value)) {
    deepFreeze(value[key]);
  }
  return Object.freeze(value);
}" "function deepFreeze(value) {
  return Object.freeze(value);
}"
run "the message is a copy, not the caller's object" \
  "  const message = {
    id: id.value," "  const message = value;
  const ignored = {
    id: id.value,"
run "the id is not trimmed (no coercion)" \
  "  const message = {
    id: id.value,
    sender: sender.value," "  const message = {
    id: id.value.trim(),
    sender: sender.value,"

# ---------------------------------------------------------------------------
# 4. P6.3's guards: the kinds and their (kind, field) table.
# ---------------------------------------------------------------------------

run "kind: the vocabulary is closed" \
  "  if (!KINDS.includes(value)) return { bad: refuse(REFUSALS.UNKNOWN_KIND, 'kind') };" ""
run "kind: the type is checked, not coerced" \
  "function kindField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'kind') };" "function kindField(value) {
  if (false) return { bad: refuse(REFUSALS.WRONG_TYPE, 'kind') };"
run "kind: a field on the wrong kind is refused, not ignored" \
  "    if (Object.hasOwn(value, field) && !KIND_OPTIONAL[kind].includes(field)) {" "    if (false) {"
run "kind: a field the kind requires is required" \
  "  for (const field of KIND_REQUIRED[kind]) {
    if (!Object.hasOwn(value, field)) {" "  for (const field of KIND_REQUIRED[kind]) {
    if (false) {"
run "kind: the defaults are the table's, not a fallthrough" \
  "  message: Object.freeze([]),
  prompt: Object.freeze(['options']),
  answer: Object.freeze(['reply_to']),
  action: Object.freeze(['action', 'reply_to'])," "  message: Object.freeze(['options']),
  prompt: Object.freeze(['options']),
  answer: Object.freeze(['reply_to']),
  action: Object.freeze(['action', 'reply_to']),"

# The agent and the strings that are named aloud.
run "agent: the id shape is the heartbeat's own" \
  "  if (!IDENTITY_PATTERN.test(text.value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };" ""

# A prompt's options: a present list is non-empty, bounded and unambiguous.
run "options: a present list is not empty" \
  "  if (value.length === 0) return { bad: refuse(REFUSALS.TOO_FEW, 'options') };" ""
run "options: the count bound" \
  "  if (value.length > OPTIONS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'options') };" ""
run "options: two options that read alike are refused" \
  "    if (seen.has(option.value)) return { bad: refuse(REFUSALS.DUPLICATE_ENTRY," \
  "    if (false) return { bad: refuse(REFUSALS.DUPLICATE_ENTRY,"

# A heartbeat's advertised agents: non-empty, bounded, distinct.
run "agents: a present list is not empty" \
  "  if (value.length === 0) return { bad: refuse(REFUSALS.TOO_FEW, 'agents') };" ""
run "agents: the count bound" \
  "  if (value.length > AGENTS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'agents') };" ""
run "agents: two agents under one id are refused" \
  "    if (seen.has(id.value)) return { bad: refuse(REFUSALS.DUPLICATE_ENTRY," \
  "    if (false) return { bad: refuse(REFUSALS.DUPLICATE_ENTRY,"
run "agents: only id and name, nothing else" \
  "      if (!AGENT_FIELDS.includes(key)) return { bad: refuse(REFUSALS.UNKNOWN_FIELD, \`agents[\${i}].\${key}\`) };" "      if (false) {"
run "heartbeat: the tag is required (O7)" \
  "const HEARTBEAT_REQUIRED = Object.freeze(['at', 'boot', 'mac']);" \
  "const HEARTBEAT_REQUIRED = Object.freeze(['at', 'boot']);"

# An action record: complete, or refused.
run "action: the unknown-field guard" \
  "    if (!ACTION_FIELDS.includes(key)) return { bad: refuse(REFUSALS.UNKNOWN_FIELD," \
  "    if (false) return { bad: refuse(REFUSALS.UNKNOWN_FIELD,"
run "action: every part is required" \
  "  for (const key of ACTION_REQUIRED) {
    if (!Object.hasOwn(value, key)) return" "  for (const key of ACTION_REQUIRED) {
    if (false) return"
run "action: the verb vocabulary is the ruling's three" \
  "  if (!ACTIONS.includes(value.verb)) return { bad: refuse(REFUSALS.UNKNOWN_VERB, 'action.verb') };" ""
run "action: a verb and its target agree (both shapes at once)" \
  "  if (isRun && isRepo) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };" ""
run "action: a verb and its target agree (neither shape)" \
  "  if (!isRun && !isRepo) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };" ""
run "action: a run id is not a command-line flag" \
  "    if (!RUN_PATTERN.test(run)) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.run') };" ""
run "action: a run id's characters are checked" \
  "    if (!RUN_CHARS.test(run)) return { bad: refuse(REFUSALS.BAD_CHARACTER, 'action.target.run') };" ""
run "action: a repository is owner/name or it is not a repository" \
  "  if (!REPO_PATTERN.test(repo)) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.repo') };" ""
run "action: a repository's characters are checked" \
  "  if (!REPO_CHARS.test(repo)) return { bad: refuse(REFUSALS.BAD_CHARACTER, 'action.target.repo') };" ""
run "action: a cancel does not resolve to a repository" \
  "  if (verb === 'cancel') return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };" ""
run "action: a merge does not resolve to a run" \
  "    if (verb === 'merge') return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };" ""
run "action: a repository target needs a pull request number" \
  "  for (const key of REPO_TARGET_FIELDS) {
    if (!Object.hasOwn(value, key))" "  for (const key of REPO_TARGET_FIELDS) {
    if (false)"
run "action: the pull request number is a positive integer" \
  "  if (!Number.isSafeInteger(pr) || pr < 1) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.pr') };" ""
run "action: a merge carries the head it was confirmed against" \
  "  if (verb === 'merge' && !Object.hasOwn(value, 'head_sha')) {" "  if (false) {"
run "action: only a merge carries a head SHA" \
  "    if (verb !== 'merge') return { bad: refuse(REFUSALS.FIELD_NOT_ALLOWED, 'action.head_sha') };" ""
run "action: the head SHA is one of the two object-name lengths" \
  "  if (value.length !== SHA_MIN && value.length !== SHA_MAX) {" "  if (false) {"
run "action: the head SHA is hex" \
  "  if (!HEX_PATTERN.test(value)) return { bad: refuse(REFUSALS.BAD_SHA, 'action.head_sha') };" ""

# The tag itself: required, and 32 bytes of lowercase hex.
run "mac: required on every message" \
  "export const REQUIRED_FIELDS = Object.freeze(['id', 'sender', 'text', 'at', 'mac']);" \
  "export const REQUIRED_FIELDS = Object.freeze(['id', 'sender', 'text', 'at']);"
run "mac: 64 hex characters, or it is not a tag" \
  "  if (value.length !== MAC_MAX || !HEX_PATTERN.test(value)) {" "  if (false) {"
run "mac: lowercase only (one tag, one spelling)" \
  "const HEX_PATTERN = /^[0-9a-f]+\$/;" "const HEX_PATTERN = /^[0-9a-fA-F]+\$/;"

# ---------------------------------------------------------------------------
# 5. P6.3's canonical serialisation: the bytes the tag covers.
#
# These are the mutations a reviewer should be most suspicious of, because a
# canonical form that differs between the two ends is a tag that verifies on
# one and not the other -- and the two ends are JavaScript and Go.
# ---------------------------------------------------------------------------

run "the two encoders' separators are refused (Go escapes them, JavaScript does not)" \
  "  const code = s.charCodeAt(i);
  return code === 0x2028 || code === 0x2029;" "  return false;"
run "canonicalBytes: the tag itself is not covered" \
  "    if (key !== 'mac' && key !== 'acked') covered[key] = record[key];" "    if (key !== 'acked') covered[key] = record[key];"
run "canonicalBytes: the ack is outside the bytes" \
  "    if (key !== 'mac' && key !== 'acked') covered[key] = record[key];" "    if (key !== 'mac') covered[key] = record[key];"
run "canonicalBytes: a covered field is never dropped (reply_to)" \
  "    if (key !== 'mac' && key !== 'acked') covered[key] = record[key];" "    if (key !== 'mac' && key !== 'acked' && key !== 'reply_to') covered[key] = record[key];"
run "canonicalBytes: the direction is inside the covered bytes" \
  "  covered[DIRECTION_FIELD] = direction;" ""
run "canonicalBytes: the direction is one of the three" \
  "  if (!DIRECTIONS.includes(direction)) {" "  if (false) {"
run "canonicalBytes: a record may not carry its own direction" \
  "  if (Object.hasOwn(record, DIRECTION_FIELD)) {" "  if (false) {"
run "canonicalBytes: the covered copy has no prototype (__proto__ stays a field)" \
  "  const covered = Object.create(null);" "  const covered = {};"
run "canonicalBytes: keys are sorted at all" \
  "  for (const key of Object.keys(value).sort(compareKeys)) {" "  for (const key of Object.keys(value)) {"
run "canonicalBytes: the sort is by code point, not UTF-16 code unit" \
  "  for (const key of Object.keys(value).sort(compareKeys)) {" "  for (const key of Object.keys(value).sort()) {"
run "canonicalBytes: the bytes are written, not rebuilt (__proto__, integer-like keys)" \
  "  return \`{\${members.join(',')}}\`;" \
  "  return JSON.stringify(Object.assign({}, ...members.map((m) => JSON.parse(\`{\${m}}\`))));"
run "canonicalBytes: only plain objects have a canonical form" \
  "  if (proto !== Object.prototype && proto !== null) {" "  if (false) {"
run "canonicalBytes: a string the two encoders spell differently is refused" \
  "function canonicalString(value, where) {
  if (hasLoneSurrogate(value)) {" "function canonicalString(value, where) {
  if (false) {"
run "canonicalBytes: the separators are refused inside a string too" \
  "  for (let i = 0; i < value.length; i++) {
    if (isSeparator(value, i)) {" "  for (let i = 0; i < 0; i++) {
    if (isSeparator(value, i)) {"
run "canonicalBytes: a null or undefined is refused, never dropped" \
  "  if (value === undefined || value === null) {" "  if (false) {"
run "canonicalBytes: a number is an integer, because two languages spell floats differently" \
  "      if (!Number.isSafeInteger(value) || Object.is(value, -0)) {" "      if (!Number.isFinite(value) || Object.is(value, -0)) {"
run "canonicalBytes: an integer past the exact-double line is refused" \
  "      if (!Number.isSafeInteger(value) || Object.is(value, -0)) {" "      if (!Number.isInteger(value) || Object.is(value, -0)) {"
run "canonicalBytes: -0 is refused" \
  "      if (!Number.isSafeInteger(value) || Object.is(value, -0)) {" "      if (!Number.isSafeInteger(value)) {"

# ---------------------------------------------------------------------------
# 6. The bounds, one step at a time.
#
# Every guard above is DISABLED: a check deleted, a pattern replaced by `false`.
# None of them moves a comparison by one, and that is the mutation this module
# was actually wrong about. Most of the rows below move one instead, and three
# of them do not: `string: DEL is no longer refused` and `id: DEL is no longer
# refused` drop the `|| code === 0x7f` term of a character rule, and `action: the
# second head-SHA length is dropped` drops the `&& value.length !== SHA_MAX`
# conjunct of the SHA rule. They are here for the reason the group exists: a
# rule narrowed by one character or one length is not what a mutant that deletes
# the whole check beside it measures. The mutation this group was written for is
# the other one: `repo.length > TARGET_MAX` narrowed to `>= TARGET_MAX` refused a
# repository of exactly the bound; widened to `> TARGET_MAX + 1` it accepted one
# code unit past it. Both survived every test
# in this package AND every test on the Go side, in both directions, until the
# T1 follow-up of PR #715 added the rows that catch them.
#
# Those rows are not in vectors.json: that file sits on exactly two lengths by
# accident (every case carries a tag of MAC_MAX and every action case a head SHA
# of SHA_MIN) and on no other bound, which vectors.bounds.json states. They are
# in bounds.test.js, the parity test for the shared artifact both ends consume,
# and in internal/mailbox's TestTheSharedBoundVectorsHold on the Go side.
#
# So: add a bound to the schema, and it needs a row in vectors.bounds.json AND a
# mutant here. A bound with a row and no mutant is a row that can be deleted by
# an edit to bounds.test.js without this harness noticing.
# ---------------------------------------------------------------------------
run "id: the length bound, narrowed by one (the bound itself refused)" \
  "  if (value.length > ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, field) };" \
  "  if (value.length >= ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, field) };"
run "string: the length bound, narrowed by one" \
  "  if (value.length > max) return { bad: refuse(REFUSALS.TOO_LONG, field) };" \
  "  if (value.length >= max) return { bad: refuse(REFUSALS.TOO_LONG, field) };"
run "string: the control-character rule stops at U+001E" \
  "    if ((code < 0x20 && !line) || code === 0x7f || isSeparator(value, i)) {" \
  "    if ((code < 0x1f && !line) || code === 0x7f || isSeparator(value, i)) {"
run "string: DEL is no longer refused" \
  "    if ((code < 0x20 && !line) || code === 0x7f || isSeparator(value, i)) {" \
  "    if ((code < 0x20 && !line) || isSeparator(value, i)) {"
run "string: a body loses the carriage return" \
  "  const line = lineBreaks && (code === 0x09 || code === 0x0a || code === 0x0d);" \
  "  const line = lineBreaks && (code === 0x09 || code === 0x0a);"
run "id: the database-key rule stops at U+001E" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f || isSeparator(value, i)) {" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x1f || code === 0x7f || isSeparator(value, i)) {"
run "id: DEL is no longer refused" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f || isSeparator(value, i)) {" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || isSeparator(value, i)) {"
run "options: the count bound, narrowed by one" \
  "  if (value.length > OPTIONS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'options') };" \
  "  if (value.length >= OPTIONS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'options') };"
run "options: the count bound, widened by one" \
  "  if (value.length > OPTIONS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'options') };" \
  "  if (value.length > OPTIONS_MAX + 1) return { bad: refuse(REFUSALS.TOO_MANY, 'options') };"
run "agents: the count bound, narrowed by one" \
  "  if (value.length > AGENTS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'agents') };" \
  "  if (value.length >= AGENTS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'agents') };"
run "agents: the count bound, widened by one" \
  "  if (value.length > AGENTS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'agents') };" \
  "  if (value.length > AGENTS_MAX + 1) return { bad: refuse(REFUSALS.TOO_MANY, 'agents') };"
run "action: the run bound, narrowed by one" \
  "    if (run.length > TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.run') };" \
  "    if (run.length >= TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.run') };"
run "action: the run bound, widened by one" \
  "    if (run.length > TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.run') };" \
  "    if (run.length > TARGET_MAX + 1) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.run') };"
run "action: the repository bound, narrowed by one" \
  "  if (repo.length > TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.repo') };" \
  "  if (repo.length >= TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.repo') };"
run "action: the repository bound, widened by one" \
  "  if (repo.length > TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.repo') };" \
  "  if (repo.length > TARGET_MAX + 1) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.repo') };"
run "action: the head SHA may be any length from SHA_MIN to SHA_MAX" \
  "  if (value.length !== SHA_MIN && value.length !== SHA_MAX) {" \
  "  if (value.length < SHA_MIN || value.length > SHA_MAX) {"
run "action: the second head-SHA length is dropped" \
  "  if (value.length !== SHA_MIN && value.length !== SHA_MAX) {" \
  "  if (value.length !== SHA_MIN) {"
run "action: a pull request may be numbered nought" \
  "  if (!Number.isSafeInteger(pr) || pr < 1) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.pr') };" \
  "  if (!Number.isSafeInteger(pr) || pr < 0) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.pr') };"

# ---------------------------------------------------------------------------
# 7. mac.js, and the boundary that keeps the entry point loadable by the phone.
#
# macOf and verifyMac moved out of index.js in P6.3's review: a static import
# of node's crypto in the entry point costs React Native far more than the tag
# (validateMessage and canonicalBytes go with it), so the import lives in
# mac.js, which index.js never imports. These first two guards protect that
# boundary, and nothing but the entry-point test would notice it breaking.
# ---------------------------------------------------------------------------
run "the entry point stays importable: no import in index.js" \
  "// The mailbox's message schema and its validation" \
  "import { createHmac } from 'node:crypto';

// The mailbox's message schema and its validation"
run "the entry point carries no node-only buffer type" \
  "const IDENTITY_PATTERN = /^[A-Za-z0-9._:-]+\$/;" \
  "const NODE_ONLY = typeof Buffer;

const IDENTITY_PATTERN = /^[A-Za-z0-9._:-]+\$/;"
run "macOf: the bytes are the canonical ones, not the object's own order" \
  "  const bytes = Buffer.from(canonicalBytes(record, direction), 'utf8');" \
  "  const bytes = Buffer.from(JSON.stringify(record), 'utf8');" "$M"
run "macOf: the direction reaches the bytes" \
  "  const bytes = Buffer.from(canonicalBytes(record, direction), 'utf8');" \
  "  const bytes = Buffer.from(canonicalBytes(record, 'inbox'), 'utf8');" "$M"
run "verifyMac: the reader's direction is the one checked" \
  "    expected = macOf(record, key, direction);" "    expected = macOf(record, key, 'inbox');" "$M"
run "verifyMac: a wrong length is a false, not a throw" \
  "  if (got.length !== want.length) return false;" "  if (false) return false;" "$M"

git checkout -- "$F" "$M"
echo "--- control killed; $guards guards: $killed killed, $survivors survived, $unapplied not applied"
[ "$survivors" -eq 0 ] && [ "$unapplied" -eq 0 ] && [ "$guards" -eq "$killed" ]
