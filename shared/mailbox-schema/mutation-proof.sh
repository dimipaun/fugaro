#!/bin/bash
# Mutation-proves the mailbox-schema guards (mobile plan, Task P0.2).
#
# Each mutation below disables exactly ONE guard in index.js, and every one of
# them must make `npm test` fail. A guard whose mutation survives is a guard no
# test defends, which for this module -- the protocol P1.1, P3.6 and P4.1 speak
# -- is the difference between a fence and a comment.
#
# It restores index.js with `git checkout` after each mutation, so it refuses to
# run against an uncommitted module: `git checkout` on an untracked file fails
# and the mutations would stack. Run it from a clean tree.
#
#   shared/mailbox-schema/mutation-proof.sh
#
# Expected output: one "killed" line per mutation, no "SURVIVED", no
# "APPLY-FAILED" (a pattern that no longer matches means this script is stale,
# not that the guard is proved).
set -u

cd "$(dirname "$0")/../.." || exit 1
F=shared/mailbox-schema/index.js

export PATH="$HOME/.local/share/mise/installs/node/22/bin:$PATH"

if ! git ls-files --error-unmatch "$F" >/dev/null 2>&1; then
  echo "REFUSING: $F is not committed; git checkout could not restore it" >&2
  exit 2
fi
if ! git diff --quiet -- "$F"; then
  echo "REFUSING: $F has uncommitted changes; commit them first" >&2
  exit 2
fi

fails=0
survivors=0

run() { # name, old, [new]
  local name="$1" old="$2" new="${3-}"
  git checkout -- "$F"
  if ! python3 - "$F" "$old" "$new" <<'PY'
import sys
p, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(p).read()
assert s.count(old) == 1, ("pattern not found exactly once:", old, s.count(old))
open(p, 'w').write(s.replace(old, new))
PY
  then echo "APPLY-FAILED  $name"; git checkout -- "$F"; fails=$((fails + 1)); return; fi
  if git diff --quiet -- "$F"; then echo "NO-OP         $name"; git checkout -- "$F"; fails=$((fails + 1)); return; fi
  if (cd shared && npm test >/dev/null 2>&1); then
    echo "SURVIVED      $name   <-- the tests did not catch this"
    survivors=$((survivors + 1))
  else
    echo "killed        $name"
  fi
  git checkout -- "$F"
}

# The value itself: not an object, an unknown field, a missing field.
run "not-an-object guard (null/array/primitive)" \
  "if (typeof value !== 'object' || value === null || Array.isArray(value)) {" "if (false) {"
run "unknown-field guard" \
  "    if (!FIELDS.includes(key)) {" "    if (false) {"
run "missing-field guard" \
  "    if (!Object.hasOwn(value, key)) {" "    if (false) {"

# The id: its type, its emptiness, its length, the database-key rule, and the
# surrogate rule that keeps it encodable.
run "id: type guard" \
  "  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'id') };" ""
run "id: empty guard" \
  "  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, 'id') };" ""
run "id: length bound" \
  "  if (value.length > ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'id') };" ""
run "id: database-key character guard" \
  "    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f) {" "    if (false) {"
run "id: lone-surrogate guard" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_KEY, 'id') };" ""

# The bounded strings: type, emptiness, bound, control characters, surrogates.
run "string: type guard" \
  "  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, field) };" ""
run "string: empty guard" \
  "  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, field) };" ""
run "string: length bound" \
  "  if (value.length > max) return { bad: refuse(REFUSALS.TOO_LONG, field) };" ""
run "string: control-character guard" \
  "    if ((code < 0x20 && !line) || code === 0x7f) {" "    if (false) {"
run "string: lone-surrogate guard" \
  "  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };" ""

# The time, the ack, and the two properties of the accepted object: frozen, and
# a copy with the caller's values verbatim.
run "timestamp: type guard" \
  "  if (typeof value !== 'number') return { bad: refuse(REFUSALS.WRONG_TYPE, 'at') };" ""
run "timestamp: safe-integer/range guard" \
  "  if (!Number.isSafeInteger(value) || value < 0) return { bad: refuse(REFUSALS.BAD_TIMESTAMP, 'at') };" ""
run "acked: boolean guard" \
  "    if (typeof value.acked !== 'boolean') {" "    if (false) {"
run "the message is frozen" \
  "  return { ok: true, message: Object.freeze(message) };" "  return { ok: true, message };"
run "the message is a copy, not the caller's object" \
  "  const message = { id: id.value, sender: sender.value, text: text.value, at: at.value };" "  const message = value;"
run "the id is not trimmed (no coercion)" \
  "  const message = { id: id.value, sender: sender.value, text: text.value, at: at.value };" \
  "  const message = { id: id.value.trim(), sender: sender.value, text: text.value, at: at.value };"

git checkout -- "$F"
echo "--- $((19 - survivors - fails)) killed, $survivors survived, $fails not applied"
[ "$survivors" -eq 0 ] && [ "$fails" -eq 0 ]
