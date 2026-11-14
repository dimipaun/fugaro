#!/usr/bin/env bash
# Tests for scripts/live-checks/check304.sh, against a fake fugaro: nothing here
# reaches the network, there is no cloud state, no daemon is started and no
# model call is made. The fake answers doctor, version, the paid-command probe
# the edition is read from, the orchestrator's own help, the orchestrator
# listing and the state out of files this test writes, so every row of the block
# is driven by a fact the test controls — including the two-session reading,
# which the listing's lease holder answers.
#
# The fake never performs a handoff: the check prints the build's own verb help
# and the two people run the command, so a case asserts that the check did not
# run one itself.
#
# Every run is bounded by `timeout`: a check that hangs must fail the suite
# rather than hang it (an earlier check340_test.sh hung for 25 minutes, which is
# what this rule is for), and the whole suite finishes in seconds.
#
# Usage: bash scripts/live-checks/check304_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check304.sh
# By absolute path, because the missing-tools case replaces PATH with a
# directory that holds nothing, and `env` would then find no bash at all.
BASH_BIN=$(command -v bash)
TIMEOUT_BIN=$(command -v timeout || true)
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK304_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 700)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... block: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2" "$3 (FACT $1)"
}
expect_file() { # path what
  [ -f "$1" ] || fail "$2: $1 is not there"
}

INSTALLATION=sandbox304
AGENT=sandbox304
FIRST="user:first@example.com"
SECOND="user:second@example.com"
THIRD="user:third@example.com"
HOLDER_BEFORE=20261113-170000-abcd
HOLDER_AFTER=20261113-171500-beef
BEAT_BEFORE=2026-11-13T17:00:00Z
BEAT_AFTER=2026-11-13T17:15:00Z

BLOCK_KEYS_1=(phase installation version edition handoff-command agent node handoff refusals restore xtrace)
BLOCK_KEYS_2=(phase installation version edition handoff-command record node sessions project-owned-agent refusals restore xtrace)

# --------------------------------------------------------------------- the fake
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check304_test.sh: every answer comes out of $FAKE304_DIR,
# and every call is recorded, so a case can assert what the check did NOT run.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE304_DIR/calls"
case "${1:-}" in
doctor)
  if [ -f "$FAKE304_DIR/doctor.status" ]; then
    printf 'fake fugaro: doctor failed\n' >&2
    exit "$(cat "$FAKE304_DIR/doctor.status")"
  fi
  cat "$FAKE304_DIR/doctor.json"
  ;;
version)
  printf 'fugaro 0.24.0 (Uakari)\n'
  ;;
coordinator | report | ab)
  # The paid names: the Subscription Edition answers with its own help, a core
  # build answers with D522's stub, and a third state answers neither.
  case "$(cat "$FAKE304_DIR/edition" 2>/dev/null || echo ee)" in
  core)
    printf '%s is part of Fugaro Subscription Edition; this build does not include it\n' "$1" >&2
    exit 1
    ;;
  silent)
    printf 'fake fugaro: %s is not a command this build answers\n' "$1" >&2
    exit 1
    ;;
  *)
    printf 'Usage:\n  fugaro %s [flags]\n\nFlags:\n  -h, --help   help for %s\n' "$1" "$1"
    ;;
  esac
  ;;
orchestrator)
  case "${2:-}" in
  --help)
    cat "$FAKE304_DIR/orchestrator-help.txt"
    ;;
  handoff)
    for a in "$@"; do
      if [ "$a" = "--help" ]; then
        cat "$FAKE304_DIR/verb-help.txt"
        exit 0
      fi
    done
    # The handoff is the two people's own act: the check prints the verb's help
    # and must never run it.
    printf 'fake fugaro: a handoff was RUN by the check\n' >&2
    exit 64
    ;;
  ls)
    # The listing the second person reads is the one after the handoff, which a
    # case turns on by creating the marker: the lease is what says who holds the
    # agent.
    if [ -f "$FAKE304_DIR/after" ]; then
      cat "$FAKE304_DIR/ls-after.json"
    else
      cat "$FAKE304_DIR/ls.json"
    fi
    ;;
  state)
    cat "$FAKE304_DIR/state.json"
    ;;
  *)
    printf 'fake fugaro: unexpected orchestrator verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ---------------------------------------------------------------- the fixtures
write_doctor() { # dir [NAME]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "prj-sandbox304"}}\n' \
    "${2:-$INSTALLATION}" >"$1/state/doctor.json"
}

write_edition() { # dir ee|core|silent
  printf '%s\n' "$2" >"$1/state/edition"
}

write_orchestrator_help() { # dir [noverb|verb]
  local verb=${2:-handoff}
  if [ "$verb" = "noverb" ]; then
    cat >"$1/state/orchestrator-help.txt" <<'TXT'
Read and patch the project's orchestrator state

Usage:
  fugaro orchestrator [command]

Available Commands:
  events      Read the orchestrator's audit log
  ls          List the orchestrators of this installation
  patch       Apply one patch document to the state
  run         Run the project's orchestrator daemon in the foreground
  state       Print the orchestrator's state
TXT
    return 0
  fi
  cat >"$1/state/orchestrator-help.txt" <<TXT
Read and patch the project's orchestrator state

Usage:
  fugaro orchestrator [command]

Available Commands:
  events      Read the orchestrator's audit log
  ls          List the orchestrators of this installation
  $verb     Hand an agent to another developer
  patch       Apply one patch document to the state
  run         Run the project's orchestrator daemon in the foreground
  state       Print the orchestrator's state
TXT
}

write_verb_help() { # dir
  cat >"$1/state/verb-help.txt" <<'TXT'
Hand an agent to another developer

Usage:
  fugaro orchestrator handoff <agent> --to <user:address> [flags]

Flags:
      --to string   the member receiving the agent
TXT
}

write_ls() { # FILE HOLDER BEAT [ID]
  python3 - "$1" "$2" "$3" "${4:-$AGENT}" <<'PY'
import json, sys
entry = {"kind": "local", "project": sys.argv[4], "cached_at": "2026-11-13T17:00:00Z"}
if sys.argv[2]:
    entry["lease_holder"] = sys.argv[2]
if sys.argv[3]:
    entry["updated_at"] = sys.argv[3]
doc = {"version": 1, "kind": "ls", "orchestrators": [entry]}
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
}

write_state() { # dir [REV] [ID]
  python3 - "$1/state/state.json" "${2:-7}" "${3:-$AGENT}" <<'PY'
import json, sys
brief = "goal: measure the handoff between two people\n\nwork items: 1, 1 still open\n  work-item-1 [ready] a piece of work\n\ndecisions: 0, 0 waiting\n\nfacts: 0"
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump({"version": 1, "kind": "state", "id": sys.argv[3], "source": "node", "rev": int(sys.argv[2]),
               "schema_version": 1, "brief": brief}, fh)
PY
}

# write_repo DIR [LEAK] — a checkout holding the two files the extension-point
# reading names. LEAK=leak writes the core file importing ee/, which is what the
# reading must refuse.
write_repo() { # dir [leak]
  mkdir -p "$1/repo/internal/orchestrator" "$1/repo/ee/register"
  if [ "${2:-}" = leak ]; then
    cat >"$1/repo/internal/orchestrator/hooks.go" <<'GO'
package orchestrator

import (
	"github.com/dimipaun/fugaro/ee/orchestrator"
)
GO
  else
    cat >"$1/repo/internal/orchestrator/hooks.go" <<'GO'
package orchestrator

// The seam ee/register fills; nothing here names ee/.
type Owner struct{ Member string }
GO
  fi
  cat >"$1/repo/ee/register/orchestrator.go" <<'GO'
package register

import (
	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

func init() {
	if err := orchestrator.SetHandoff(orchestrator.HandoffFuncs{Transfer: eeorch.Transfer}); err != nil {
		panic(err)
	}
}
GO
}

# setup_case NAME — a fresh case: the fake, a healthy Subscription Edition
# installation whose build carries a handoff verb, one agent whose lease the
# giving session holds, and the node that agent names.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fake_fugaro "$dir"
  : >"$dir/state/calls"
  rm -f "$dir/state/after" "$dir/state/doctor.status"
  write_doctor "$dir"
  write_edition "$dir" ee
  write_orchestrator_help "$dir"
  write_verb_help "$dir"
  write_ls "$dir/state/ls.json" "$HOLDER_BEFORE" "$BEAT_BEFORE"
  write_ls "$dir/state/ls-after.json" "$HOLDER_AFTER" "$BEAT_AFTER"
  write_state "$dir"
  write_repo "$dir"
}

# run304 [ARGS...] — runs the check on the case at hand. RUN304_PATH replaces
# PATH and RUN304_BASH adds flags to the bash that runs it. Sets $status, $out,
# $errtext and $dir/err. Every run is bounded.
run304() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE304_DIR="$dir/state" \
    PATH="${RUN304_PATH:-$dir/bin:$PATH}" \
    "$TIMEOUT_BIN" 60 "$BASH_BIN" ${RUN304_BASH[@]+"${RUN304_BASH[@]}"} "$script" ${RUN304_ARGS[@]+"${RUN304_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN304_ARGS=()
  RUN304_PATH=""
  RUN304_BASH=()
  errtext=$(cat "$dir/err" 2>/dev/null || true)
  # The scratch directory is this check's own litter and the cleanup is a
  # promise its header makes: every case asserts it, whatever it is about.
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" 2>/dev/null | head -n 1)
  if [ -n "$sc" ] && [ -d "$sc" ]; then
    fail "case $dir: the scratch directory survived the run: $sc"
  fi
}

expect_block_keys() { # out what KEYS...
  local text=$1 what=$2
  shift 2
  expect_contains "$text" "CHECK 304 RESULT" "$what: the block"
  local key
  for key in "$@"; do
    expect_contains "$text" "FACT $key:" "$what: the $key row"
  done
  expect_contains "$text" "CHECK 304 RESULT END (exit " "$what: the block's end"
}

# expect_no_handoff_run WHAT — the handoff is the two people's own act: the
# check reads the verb's help and never runs the verb.
expect_no_handoff_run() { # what
  local ran
  ran=$(grep 'orchestrator handoff' "$dir/state/calls" 2>/dev/null | grep -vc -- '--help' || true)
  [ "${ran:-0}" -eq 0 ] || fail "$1: the check ran the handoff itself"
}

expect_no_writes() { # what
  local wrote
  wrote=$(grep -cE 'orchestrator (patch|import|run|install|uninstall)' "$dir/state/calls" 2>/dev/null || true)
  [ "${wrote:-0}" -eq 0 ] || fail "$1: the check ran a writing orchestrator command: $(grep -E 'orchestrator (patch|import|run|install|uninstall)' "$dir/state/calls" | tr '\n' ' ')"
}

expect_no_fugaro() { # what
  local n
  n=$(grep -c '^fugaro ' "$dir/state/calls" 2>/dev/null || true)
  [ "${n:-0}" -eq 0 ] || fail "$1: fugaro was called where it must not be: $(tr '\n' ' ' <"$dir/state/calls")"
}

# ------------------------------------------------------- the first person's phase
case_first_person() {
  setup_case first
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 0 "first-person: a Subscription Edition build with a handoff verb, one agent, two members"
  expect_block_keys "$out" "first-person" "${BLOCK_KEYS_1[@]}"
  expect_fact installation PASS "first-person"
  expect_fact version PASS "first-person"
  expect_fact edition PASS "first-person"
  expect_contains "$out" "Subscription Edition" "first-person: the edition row names the edition the handoff needs"
  expect_fact handoff-command PASS "first-person"
  expect_contains "$out" "fugaro orchestrator handoff" "first-person: the row names the verb the build carries"
  expect_fact agent PASS "first-person"
  expect_contains "$out" "$HOLDER_BEFORE" "first-person: the agent row names the lease holder before the handoff"
  expect_fact node PASS "first-person"
  expect_contains "$out" "revision 7" "first-person: the node row names the revision the giving session read"
  expect_fact handoff PASS "first-person"
  expect_fact refusals INFO "first-person: the refusal half is not read by a script"
  expect_fact restore PASS "first-person"
  expect_fact xtrace PASS "first-person"
  # The record: mode 0600, the two members, the agent and the lease before it.
  expect_file "$dir/tmp/fugaro-check304-handoff.json" "first-person"
  if [ -f "$dir/tmp/fugaro-check304-handoff.json" ]; then
    local mode
    mode=$(stat -c '%a' "$dir/tmp/fugaro-check304-handoff.json" 2>/dev/null || stat -f '%Lp' "$dir/tmp/fugaro-check304-handoff.json")
    [ "$mode" = "600" ] || fail "first-person: the record is mode $mode, want 600"
    local rec
    rec=$(cat "$dir/tmp/fugaro-check304-handoff.json")
    expect_contains "$rec" '"check": "304"' "first-person: the record names the check"
    expect_contains "$rec" "\"agent\": \"$AGENT\"" "first-person: the record names the agent"
    expect_contains "$rec" "\"from\": \"$FIRST\"" "first-person: the record names the giving member"
    expect_contains "$rec" "\"to\": \"$SECOND\"" "first-person: the record names the receiving member"
    expect_contains "$rec" "$HOLDER_BEFORE" "first-person: the record carries the lease holder before"
    expect_absent "$rec" "token" "first-person: no credential-shaped key in the record"
  fi
  # What the two people run: the second person's line, the daemon that makes the
  # receiving session the holder, and the refusal reading.
  expect_contains "$errtext" "check304.sh second-person --installation $INSTALLATION --as $SECOND --handoff" "first-person: the second person's own command line"
  expect_contains "$errtext" "fugaro orchestrator run $AGENT" "first-person: the daemon the receiving session starts"
  expect_contains "$errtext" "it is refused" "first-person: the refusal half is printed for the two of them"
  expect_no_handoff_run "first-person"
  expect_no_writes "first-person"
}

case_first_person_verb_spelling() {
  setup_case verbspelling
  write_orchestrator_help "$dir" transfer
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 0 "verb spelling: a build whose handoff verb is spelled transfer"
  expect_fact handoff-command PASS "verb spelling"
  expect_contains "$out" "fugaro orchestrator transfer" "verb spelling: the row names the verb this build carries"
}

case_first_person_named_agent() {
  setup_case namedagent
  write_ls "$dir/state/ls.json" "$HOLDER_BEFORE" "$BEAT_BEFORE" other-project
  write_state "$dir" 7 other-project
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND" --agent other-project
  expect_status "$status" 0 "namedagent: an agent named on the command line"
  expect_fact agent PASS "namedagent"
  expect_contains "$out" "the agent is other-project" "namedagent: the row names the agent that was asked for"
}

# ------------------------------------------------------ the second person's phase
case_second_person() {
  setup_case second
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 0 "second: the first person's phase has to succeed first"
  : >"$dir/state/after"
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" --handoff "$dir/tmp/fugaro-check304-handoff.json"
  expect_status "$status" 0 "second: the receiving session holds the agent now"
  expect_block_keys "$out" "second" "${BLOCK_KEYS_2[@]}"
  expect_fact phase PASS "second"
  expect_contains "$out" "phase: PASS — second-person" "second: the block names the phase it ran"
  expect_fact edition PASS "second"
  expect_fact handoff-command PASS "second"
  expect_fact record PASS "second"
  expect_fact node PASS "second"
  expect_contains "$out" "one agent, two sessions" "second: the node row is the two-session reading"
  expect_fact sessions PASS "second"
  expect_contains "$out" "$HOLDER_AFTER" "second: the sessions row names the holder after the handoff"
  expect_contains "$out" "the GIVING session no longer holds it" "second: the sessions row says what moved"
  expect_fact project-owned-agent INFO "second: without --repo the tree half is not read"
  expect_fact refusals INFO "second"
  expect_fact restore PASS "second"
  expect_contains "$out" "fugaro orchestrator run $AGENT" "second: the restore row says how the agent is handed back"
  expect_no_handoff_run "second"
  expect_no_writes "second"
}

case_second_person_repo() {
  setup_case secondrepo
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  : >"$dir/state/after"
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" \
    --handoff "$dir/tmp/fugaro-check304-handoff.json" --repo "$dir/repo"
  expect_status "$status" 0 "secondrepo: the extension point read from a checkout"
  expect_fact project-owned-agent PASS "secondrepo"
  expect_contains "$out" "ee/register/orchestrator.go installs Transfer" "secondrepo: the row names the registration it read"
  expect_contains "$out" "no owner and no member on it" "secondrepo: the row carries the live half (F28)"
}

case_second_person_repo_leak() {
  setup_case secondrepoleak
  write_repo "$dir" leak
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  : >"$dir/state/after"
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" \
    --handoff "$dir/tmp/fugaro-check304-handoff.json" --repo "$dir/repo"
  expect_status "$status" 1 "secondrepoleak: a core that imports ee/ fails the reading"
  expect_fact project-owned-agent FAIL "secondrepoleak"
  expect_contains "$out" "imports ee/" "secondrepoleak: the row says what it found"
}

case_second_person_still_the_giver() {
  setup_case stillgiver
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  # The lease is untouched: the giving session is still the one driving the agent.
  : >"$dir/state/after"
  write_ls "$dir/state/ls-after.json" "$HOLDER_BEFORE" "$BEAT_AFTER"
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" --handoff "$dir/tmp/fugaro-check304-handoff.json"
  expect_status "$status" 1 "stillgiver: the handoff did not move the lease"
  expect_fact sessions FAIL "stillgiver"
  expect_contains "$out" "the GIVING session is still the one driving this agent" "stillgiver: the row says which session holds it"
}

case_second_person_nobody_holds_it() {
  setup_case nobody
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  : >"$dir/state/after"
  write_ls "$dir/state/ls-after.json" "" ""
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" --handoff "$dir/tmp/fugaro-check304-handoff.json"
  expect_status "$status" 1 "nobody: no daemon holds the agent"
  expect_fact sessions FAIL "nobody"
  expect_contains "$out" "nobody holds $AGENT now" "nobody: the row says the lease names nobody"
}

case_second_person_third_member() {
  setup_case third
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  : >"$dir/state/after"
  run304 second-person --installation "$INSTALLATION" --as "$THIRD" --handoff "$dir/tmp/fugaro-check304-handoff.json"
  expect_status "$status" 2 "third: a member the handoff was not made to runs the second phase"
  expect_contains "$out" "the receiving person runs the second phase" "third: the refusal says why"
  expect_contains "$out" "FACT sessions: FAIL — not reached" "third: nothing was read from a third session"
}

case_second_person_no_record() {
  setup_case norecord
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" --handoff "$dir/tmp/absent.json"
  expect_status "$status" 2 "norecord: there is no handoff record"
  expect_fact record FAIL "norecord"
}

case_second_person_other_installation() {
  setup_case otherinstallation
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  python3 - "$dir/tmp/fugaro-check304-handoff.json" <<'PY'
import json, sys
path = sys.argv[1]
doc = json.load(open(path, encoding="utf-8"))
doc["installation"] = "sandbox-elsewhere"
with open(path, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" --handoff "$dir/tmp/fugaro-check304-handoff.json"
  expect_status "$status" 2 "otherinstallation: the record is one handoff of another installation"
  expect_contains "$out" "the two of you are looking at different installations" "otherinstallation: the refusal says why"
}

# ------------------------------------------------------------------ the gates
case_core_build() {
  setup_case core
  write_edition "$dir" core
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "core: the build carries the seam and not the rule"
  expect_fact edition FAIL "core"
  expect_contains "$out" "core build" "core: the row names the build it found"
  expect_contains "$out" "coreHandoffFuncs" "core: the row names the rule that refuses every handoff there"
  expect_no_fugaro_orchestrator "core"
}

case_edition_unreadable() {
  setup_case unreadable
  write_edition "$dir" silent
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "unreadable: the build answers neither the paid surface nor the stub"
  expect_fact edition FAIL "unreadable"
  expect_contains "$out" "the build's edition could not be read" "unreadable: the row says what it could not tell"
}

case_no_handoff_surface() {
  setup_case nosurface
  write_orchestrator_help "$dir" noverb
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "nosurface: the build carries no command that hands an agent over"
  expect_fact handoff-command FAIL "nosurface"
  expect_contains "$out" "HandoffAgent is called from nowhere" "nosurface: the row names the finding"
  expect_contains "$out" "needs the release that gives the handoff a command" "nosurface: the row names what it needs"
  expect_no_listing "nosurface"
}

expect_no_fugaro_orchestrator() { # what
  local n
  n=$(grep -c 'orchestrator' "$dir/state/calls" 2>/dev/null || true)
  [ "${n:-0}" -eq 0 ] || fail "$1: an orchestrator command was run before the edition gate passed"
}
expect_no_listing() { # what
  local n
  n=$(grep -c 'orchestrator ls' "$dir/state/calls" 2>/dev/null || true)
  [ "${n:-0}" -eq 0 ] || fail "$1: the listing was read before the handoff gate passed"
}

case_belong() {
  setup_case belong
  run304 first-person --installation belong --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "belong: the owner's own installation is refused by name"
  expect_fact installation FAIL "belong"
  expect_contains "$out" "never runs against 'belong'" "belong: the row says so"
  expect_no_fugaro "belong"
}

case_belong_from_doctor() {
  setup_case belongdoctor
  write_doctor "$dir" belong-dev
  run304 first-person --installation belong-dev --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "belongdoctor: a belong-* name is refused too"
  expect_fact installation FAIL "belongdoctor"
  expect_no_fugaro "belongdoctor"
}

case_name_mismatch() {
  setup_case mismatch
  write_doctor "$dir" other-sandbox
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "mismatch: the name typed and the config in force disagree"
  expect_fact installation FAIL "mismatch"
  expect_no_fugaro_orchestrator "mismatch"
}

case_doctor_refused() {
  setup_case doctorrefused
  printf '1\n' >"$dir/state/doctor.status"
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "doctorrefused: doctor did not answer"
  expect_contains "$out" "doctor --json did not answer" "doctorrefused: the row says so"
}

# ------------------------------------------------------------- the arguments
case_no_phase() {
  setup_case nophase
  run304 --installation "$INSTALLATION"
  expect_status "$status" 2 "nophase: the phase is required"
  expect_contains "$errtext" "the phase is required" "nophase: the message names it"
}

case_unknown_phase() {
  setup_case unknownphase
  run304 third-person --installation "$INSTALLATION"
  expect_status "$status" 2 "unknownphase: an unknown phase"
  expect_contains "$errtext" "unknown phase 'third-person'" "unknownphase: the message names it"
}

case_no_installation() {
  setup_case noinstallation
  run304 first-person --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "noinstallation: --installation is required"
  expect_contains "$errtext" "--installation is required" "noinstallation: the message says it"
}

case_unknown_arg() {
  setup_case unknownarg
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND" --wat
  expect_status "$status" 2 "unknownarg: an unknown flag"
  expect_contains "$errtext" "unknown argument '--wat'" "unknownarg: the message names it"
}

case_member_spelling() {
  setup_case member
  run304 first-person --installation "$INSTALLATION" --from first@example.com --to "$SECOND"
  expect_status "$status" 2 "member: a member is spelled user:<address>"
  expect_contains "$errtext" "a member is spelled 'user:<address>'" "member: the refusal names the spelling"
  # A credential pasted here by accident is refused the same way, which is what
  # keeps it out of the record and out of the block.
  run304 first-person --installation "$INSTALLATION" --from "$(printf '1//09aBcD-this-is-not-a-member')" --to "$SECOND"
  expect_status "$status" 2 "member: a token-shaped value is refused"
  expect_absent "$out" "1//09aBcD" "member: the value never reaches the block"
  expect_absent "$errtext" "1//09aBcD" "member: the value never reaches the message"
}

case_same_member() {
  setup_case samemember
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$FIRST"
  expect_status "$status" 2 "samemember: a handoff moves an agent between two people"
  expect_contains "$errtext" "a handoff moves an agent between two people" "samemember: the refusal says why"
}

case_agent_id_path() {
  setup_case agentpath
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND" --agent ../../etc/passwd
  expect_status "$status" 2 "agentpath: an id that is a path is refused"
  expect_contains "$errtext" "is not an orchestrator id" "agentpath: the refusal names what an id may carry"
  expect_absent "$errtext" "passwd" "agentpath: the value that is a path is not echoed"
  expect_no_fugaro "agentpath"
}

# ------------------------------------------------------------ tools and trace
case_missing_tools() {
  setup_case missing
  # A PATH with every tool the check needs and no fugaro: the run must stop by
  # name rather than fail half way through with a command-not-found.
  mkdir -p "$dir/nofugaro"
  local t
  for t in bash env mktemp timeout python3 date stat sed grep cut head tr rm chmod git cat awk; do
    p=$(command -v "$t" 2>/dev/null || true)
    [ -n "$p" ] && ln -sf "$p" "$dir/nofugaro/$t"
  done
  RUN304_PATH=$dir/nofugaro
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "missing: fugaro is not on PATH"
  expect_contains "$errtext" "Check 304 needs 'fugaro' on PATH" "missing: the refusal names the tool"
}

case_xtrace() {
  setup_case xtrace
  RUN304_BASH=(-x)
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  expect_status "$status" 2 "xtrace: a shell that arrived tracing is refused"
  expect_fact xtrace FAIL "xtrace"
  expect_contains "$out" "this shell is tracing" "xtrace: the row says so"
  expect_absent "$out" "$FIRST" "xtrace: the members typed are not in the block"
}

# ------------------------------------------------------------------- dry run
case_dry_run_first() {
  setup_case dryfirst
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND" --dry-run
  expect_status "$status" 0 "dryfirst: a dry run"
  expect_block_keys "$out" "dryfirst" phase installation dry-run xtrace
  expect_contains "$errtext" "dry run: NOTHING below was run" "dryfirst: the plan says so"
  expect_contains "$errtext" "the handoff verb: a build with none stops here" "dryfirst: the plan names the gate"
  [ -f "$dir/tmp/fugaro-check304-handoff.json" ] && fail "dryfirst: the dry run wrote the handoff record"
  expect_no_fugaro "dryfirst"
}

case_dry_run_second() {
  setup_case drysecond
  run304 second-person --installation "$INSTALLATION" --as "$SECOND" --handoff "$dir/tmp/absent.json" --dry-run
  expect_status "$status" 0 "drysecond: a dry run of the second phase"
  expect_contains "$errtext" "the same node, from your own session" "drysecond: the plan names the read"
  [ -f "$dir/tmp/absent.json" ] && fail "drysecond: the dry run wrote a record"
  expect_no_fugaro "drysecond"
}

# --------------------------------------------------------------- the mutation
# The mutation breaks the row the two-session reading rests on: a check whose
# `sessions` row can be made to pass without the lease having moved would report
# a handoff that never happened, and this suite would not catch it.
case_mutation_sessions_always_pass() {
  setup_case mutation
  run304 first-person --installation "$INSTALLATION" --from "$FIRST" --to "$SECOND"
  : >"$dir/state/after"
  write_ls "$dir/state/ls-after.json" "$HOLDER_BEFORE" "$BEAT_AFTER"
  local copy=$dir/mutated304.sh
  sed 's|    fact sessions FAIL "the lease still names|    fact sessions PASS "the lease still names|' "$script" >"$copy"
  if ! grep -q 'fact sessions PASS "the lease still names' "$copy"; then
    fail "mutation: the sed found no place to break the sessions row"
    return 0
  fi
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE304_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    timeout 60 "$BASH_BIN" "$copy" second-person --installation "$INSTALLATION" --as "$SECOND" \
    --handoff "$dir/tmp/fugaro-check304-handoff.json" 2>"$dir/err") || status=$?
  expect_contains "$out" "FACT sessions: PASS" \
    "mutation: the broken row reports PASS, so the assertion in case_second_person_still_the_giver is not vacuous"
  if [ "$status" -ne 0 ]; then
    fail "mutation: the broken copy did not exit 0 (it exited $status), so the mutation changed no verdict"
  fi
}

# --------------------------------------------------------------- the suite
# CHECK_TEST_SHARD="i/n" runs every n-th case starting at i, and
# CHECK_TEST_ONLY=name runs that one case alone. Neither set: all run.
case_index=0
t() {
  local i=$case_index
  case_index=$((case_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t case_first_person
t case_first_person_verb_spelling
t case_first_person_named_agent
t case_second_person
t case_second_person_repo
t case_second_person_repo_leak
t case_second_person_still_the_giver
t case_second_person_nobody_holds_it
t case_second_person_third_member
t case_second_person_no_record
t case_second_person_other_installation
t case_core_build
t case_edition_unreadable
t case_no_handoff_surface
t case_belong
t case_belong_from_doctor
t case_name_mismatch
t case_doctor_refused
t case_no_phase
t case_unknown_phase
t case_no_installation
t case_unknown_arg
t case_member_spelling
t case_same_member
t case_agent_id_path
t case_missing_tools
t case_xtrace
t case_dry_run_first
t case_dry_run_second
t case_mutation_sessions_always_pass

if [ "$failures" -ne 0 ]; then
  echo "check304_test: $failures failure(s)" >&2
  exit 1
fi
echo "check304_test: all cases passed"
