#!/usr/bin/env bash
# Tests for scripts/live-checks/check303.sh, against a fake fugaro: nothing here
# reaches the network, there is no cloud state, no daemon is started and no
# model call is made. The fake answers doctor, version, the daemon's help, the
# daemon's own cycles, the audit log, the state and the resolved config out of
# files this test writes, so every row of the block is driven by a fact the test
# controls.
#
# Every run is bounded by `timeout`: a check that hangs must fail the suite
# rather than hang it (an earlier check340_test.sh hung for 25 minutes, which is
# what this rule is for), and the whole suite finishes in seconds.
#
# Usage: bash scripts/live-checks/check303_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check303.sh
# By absolute path, because the missing-tools case replaces PATH with a
# directory that holds nothing and `env` would then find no bash at all.
BASH_BIN=$(command -v bash)
# By absolute path too, for the same reason: the missing-tools case empties PATH.
TIMEOUT_BIN=$(command -v timeout || true)
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK303_TEST_KEEP:-}" ]; then
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

# The installation the suite runs against, and the model the policy resolves for
# the routine class: what the daemon's own line is held to.
INSTALLATION=sandbox303
MODEL=claude-model-of-the-routine-class
OTHER_MODEL=claude-some-other-model
CLASS=routine
HOUR_REFUSAL='decide: the decision agent has made all the calls its caps allow: 6 calls in the last hour, the cap is 6'
DAY_REFUSAL='decide: the decision agent has made all the calls its caps allow: 40 calls today, the cap is 40'

BLOCK_KEYS=(installation version decision-agent cost-bound consent calls model cap-hour cap-day breaker cycle-cost restore xtrace)

# --------------------------------------------------------------------- the fake
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check303_test.sh: every answer comes out of $FAKE303_DIR,
# and every call is recorded, so a case can assert what the check did NOT run.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE303_DIR/calls"
case "${1:-}" in
doctor)
  if [ -f "$FAKE303_DIR/doctor.status" ]; then
    printf 'fake fugaro: doctor failed\n' >&2
    exit "$(cat "$FAKE303_DIR/doctor.status")"
  fi
  cat "$FAKE303_DIR/doctor.json"
  ;;
version)
  printf '%s\n' "fugaro 0.24.0 (Uakari)"
  ;;
orchestrator)
  case "${2:-}" in
  run)
    for a in "$@"; do
      if [ "$a" = "--help" ]; then cat "$FAKE303_DIR/run-help.txt"; exit 0; fi
    done
    printf 'run\n' >>"$FAKE303_DIR/runs"
    # The breaker's trip is the node's, and it lands the second time the daemon
    # is driven -- the window first, the longer run after it.
    if [ -f "$FAKE303_DIR/trip" ] && [ "$(wc -l <"$FAKE303_DIR/runs")" -ge 2 ]; then
      : >"$FAKE303_DIR/paused"
    fi
    cat "$FAKE303_DIR/daemon.out"
    cat "$FAKE303_DIR/daemon.err" >&2
    if [ -f "$FAKE303_DIR/daemon.status" ]; then exit "$(cat "$FAKE303_DIR/daemon.status")"; fi
    exit 124
    ;;
  events)
    cat "$FAKE303_DIR/events.json"
    ;;
  state)
    # The brief is generated here from the paused marker, because the trip lands
    # in the middle of a run: a state.json written once by the test would be the
    # node as it was before the breaker ever fired.
    python3 - "$FAKE303_DIR/paused" <<'PY'
import json, os, sys
paused = os.path.exists(sys.argv[1])
brief = "goal: measure the decision agent\n\nwork items: 1, 1 still open\n  work-item-1 [blocked] a judgement case\n"
if paused:
    brief += ("\ndecisions: 1, 1 waiting\n  breaker-tripped-20261113T101500Z waits on a person since "
              "2026-11-13T10:15:00Z: The orchestrator has ended 3 cycles in a row without starting or finishing "
              "anything. What should it do?\n")
else:
    brief += "\ndecisions: 0, 0 waiting\n"
brief += "\nfacts: 0"
print(json.dumps({"version": 1, "kind": "state", "id": "sandbox303", "source": "node", "rev": 7,
                  "schema_version": 1, "brief": brief}))
PY
    ;;
  *)
    printf 'fake fugaro: unexpected orchestrator verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
config)
  cat "$FAKE303_DIR/config.json"
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
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "prj-sandbox303"}}\n' \
    "${2:-$INSTALLATION}" >"$1/state/doctor.json"
}

write_run_help() { # dir [without-decide]
  if [ "${2:-}" = "without-decide" ]; then
    cat >"$1/state/run-help.txt" <<'TXT'
Run the project's orchestrator daemon in the foreground

Usage:
  fugaro orchestrator run [flags]

Flags:
      --binary string     the stable path of the fugaro binary the login unit names
      --interval duration   how long to wait between cycles when the node says nothing moved (default 30s)
      --once              run one cycle and exit, releasing the lease
TXT
  else
    cat >"$1/state/run-help.txt" <<'TXT'
Run the project's orchestrator daemon in the foreground

Usage:
  fugaro orchestrator run [flags]

Flags:
      --binary string     the stable path of the fugaro binary the login unit names
      --decide            wake the bounded decision agent for the cases that need a judgement (off by default)
      --interval duration   how long to wait between cycles when the node says nothing moved (default 30s)
      --once              run one cycle and exit, releasing the lease
TXT
  fi
}

write_daemon_output() { # dir MODEL LINE CLASS LINE REFUSALS...
  local dir=$1 model=$2 class=$3
  shift 3
  {
    printf 'orchestrator %s: cycle 1\n' "$INSTALLATION"
    printf 'decision agent: class=%s model=%s (from orchestrator.decision_model_per_class.%s)\n' "$class" "$model" "$class"
    printf 'decision: wrote 2 ops as a decision-agent for the case it decided about\n'
    local line
    for line in "$@"; do printf '%s\n' "$line"; done
  } >"$dir/state/daemon.out"
}

write_events() { # dir N [SUBJECT]
  local dir=$1 n=$2 subject=${3:-work-item-1}
  if [ "$n" -eq 0 ]; then
    printf '{"version":1,"kind":"events","id":"%s","events":[]}\n' "$INSTALLATION" >"$dir/state/events.json"
    return 0
  fi
  python3 - "$dir/state/events.json" "$n" "$subject" <<'PY'
import json, sys
n, subject = int(sys.argv[2]), sys.argv[3]
events = [{"id": f"{subject}:decision-20261113-10150{i}", "at": "2026-11-13T10:15:00Z",
           "subject": subject, "transition": f"decision-20261113-10150{i}", "text": "the answer"}
          for i in range(n)]
# A log carries more than decisions, and the check must count only its own kind.
events.insert(0, {"id": f"{subject}:run-started", "at": "2026-11-13T10:14:00Z",
                  "subject": subject, "transition": "run-started", "text": ""})
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump({"version": 1, "kind": "events", "id": "sandbox303", "events": events}, fh)
PY
}

write_config() { # dir MODEL
  local model=${2:-$MODEL}
  python3 - "$1/state/config.json" "orchestrator.decision_model_per_class.$CLASS" "$model" <<'PY'
import json, sys
values = [{"path": sys.argv[2], "value": sys.argv[3], "source": "fugaro.yaml"}]
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump({"config_sha256": "0" * 64, "project_layer": None, "layer_unknown": False,
               "values": values}, fh)
PY
}

# setup_case NAME — a fresh case: the fake, a healthy installation whose build
# carries --decide, one decision call in the window's log, the model the policy
# holds, both cap refusals and a breaker that trips on the second run.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/repo"
  write_fake_fugaro "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/runs"
  printf 'project: %s\n' "$INSTALLATION" >"$dir/repo/fugaro.yaml"
  rm -f "$dir/state/paused" "$dir/state/daemon.status"
  # The node's breaker trips on the daemon's second run (the window first, the
  # longer run after it); a case that is about a loop that never got there
  # removes this.
  : >"$dir/state/trip"
  write_doctor "$dir"
  write_run_help "$dir"
  write_daemon_output "$dir" "$MODEL" "$CLASS" "$HOUR_REFUSAL" "$DAY_REFUSAL"
  write_events "$dir" 1
  write_config "$dir"
}

# run303 [ARGS...] — runs the check on the case at hand. RUN303_ARGS adds
# arguments, RUN303_PATH replaces PATH and RUN303_BASH adds flags to the bash
# that runs it. Sets $status, $out, $errtext and $dir/err. Every run is bounded.
run303() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE303_DIR="$dir/state" \
    PATH="${RUN303_PATH:-$dir/bin:$PATH}" \
    "$TIMEOUT_BIN" 60 "$BASH_BIN" ${RUN303_BASH[@]+"${RUN303_BASH[@]}"} "$script" ${RUN303_ARGS[@]+"${RUN303_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN303_ARGS=()
  RUN303_PATH=""
  RUN303_BASH=()
  errtext=$(cat "$dir/err" 2>/dev/null || true)
  # The scratch directory is this check's own litter and the cleanup is a
  # promise its header makes: every case asserts it, whatever it is about.
  local sc
  sc=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" 2>/dev/null | head -n 1)
  if [ -n "$sc" ] && [ -d "$sc" ]; then
    fail "case $dir: the scratch directory survived the run: $sc"
  fi
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 303 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
  expect_contains "$1" "CHECK 303 RESULT END (exit " "$2: the block's end"
}

expect_no_daemon() { # what
  expect_absent "$(cat "$dir/state/runs" 2>/dev/null || true)" "run" "$1: the daemon was started"
}

expect_daemon_runs() { # want what
  local got
  got=$(grep -c '^run$' "$dir/state/runs" 2>/dev/null || true)
  [ "${got:-0}" = "$1" ] || fail "$2: expected the daemon to be driven $1 time(s), got ${got:-0}"
}

# --------------------------------------------------------------- the healthy run
case_healthy() {
  setup_case healthy
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo" --usd-per-call 0.30
  expect_status "$status" 0 "healthy: an installation whose loop met a judgement case and whose breaker tripped"
  expect_block_keys "$out" "healthy"
  expect_fact installation PASS "healthy"
  expect_fact version PASS "healthy"
  expect_fact decision-agent PASS "healthy"
  expect_fact cost-bound PASS "healthy"
  expect_fact consent PASS "healthy"
  expect_fact calls PASS "healthy"
  expect_fact model PASS "healthy"
  expect_fact cap-hour PASS "healthy"
  expect_fact cap-day PASS "healthy"
  expect_fact breaker PASS "healthy"
  expect_fact cycle-cost PASS "healthy"
  expect_fact restore PASS "healthy"
  expect_fact xtrace PASS "healthy"
  expect_contains "$out" "1 decision call(s) are in the audit log" "healthy: the calls row counts the log's decisions only"
  expect_contains "$out" "work-item-1" "healthy: the calls row names the item the call was about"
  expect_contains "$out" "which is what 'orchestrator.decision_model_per_class.$CLASS' holds" "healthy: the model row holds the call's model to O4's key"
  expect_contains "$out" "6 calls in the last hour" "healthy: the hour cap's own words are in the row"
  expect_contains "$out" "40 calls today" "healthy: the day cap's own words are in the row"
  expect_contains "$out" "breaker-tripped-20261113T101500Z" "healthy: the breaker row names the question the node carries"
  expect_contains "$out" "the DAEMON IS PAUSED" "healthy: the restore row says the daemon is paused"
  expect_contains "$out" "fugaro orchestrator patch" "healthy: the restore row names the command that lifts it"
  expect_daemon_runs 2 "healthy"
  # The bound is stated before anything is spent, on the owner's own stderr.
  expect_contains "$errtext" "the bound this run will not pass" "healthy: the bound is printed up front"
  expect_contains "$errtext" "at most 40 decision calls" "healthy: the bound is the day cap"
  expect_contains "$out" "at most 40 decision calls" "healthy: the bound is a fact"
}

case_not_paused() {
  setup_case notpaused
  rm -f "$dir/state/trip"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "notpaused: a window in which the breaker never reached three cycles"
  expect_fact breaker FAIL "notpaused"
  expect_fact restore PASS "notpaused: nothing was written and the daemon is not paused"
  expect_contains "$out" "the node carries no unanswered breaker question" "notpaused: the restore row says why there is nothing to undo"
  expect_absent "$out" "the DAEMON IS PAUSED" "notpaused: the restore row does not claim a pause that is not there"
}

case_paused_before() {
  setup_case pausedbefore
  : >"$dir/state/paused"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 0 "pausedbefore: the pause is reported as INFO rather than claimed as this run's"
  expect_fact breaker INFO "pausedbefore"
  expect_contains "$out" "the node already carried a breaker question before this run started" "pausedbefore: the row says the pause is not this run's"
  expect_contains "$out" "the DAEMON IS PAUSED" "pausedbefore: the restore row still says the daemon is paused"
}

case_no_decide() {
  setup_case nodecide
  write_run_help "$dir" without-decide
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 2 "nodecide: the build carries no decision agent"
  expect_block_keys "$out" "nodecide"
  expect_fact decision-agent FAIL "nodecide"
  expect_contains "$out" "the release that carries O13" "nodecide: the row names the task that has to land"
  expect_contains "$out" "Nothing was spent" "nodecide: the row says nothing was spent"
  expect_absent "$out" "FACT consent: PASS" "nodecide: the money the run would spend was never consented to"
  expect_no_daemon "nodecide"
  # The gate is before the state is read: nothing about a node nobody drove.
  expect_absent "$(cat "$dir/state/calls")" "orchestrator state" "nodecide: the node was read before the gate"
}

case_no_yes() {
  setup_case noyes
  run303 --installation "$INSTALLATION" --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 2 "noyes: no --yes"
  expect_fact consent FAIL "noyes"
  expect_contains "$out" "needs --yes" "noyes: the row says what is missing"
  expect_contains "$errtext" "at most 40 decision calls" "noyes: the bound is stated before the refusal"
  expect_no_daemon "noyes"
}

case_belong() {
  setup_case belong
  run303 --installation belong --yes --window 3s
  expect_status "$status" 2 "belong: the owner's own installation is refused"
  expect_fact installation FAIL "belong"
  expect_contains "$out" "it is not run against belong" "belong: the row says why"
  expect_no_daemon "belong"
}

case_belong_from_doctor() {
  setup_case belongdoctor
  write_doctor "$dir" "belong-2"
  run303 --installation belong-2 --yes --window 3s
  expect_status "$status" 2 "belongdoctor: a belong-* name doctor answers is refused too"
  expect_fact installation FAIL "belongdoctor"
  expect_no_daemon "belongdoctor"
}

case_name_mismatch() {
  setup_case mismatch
  write_doctor "$dir" "another-installation"
  run303 --installation "$INSTALLATION" --yes --window 3s
  expect_status "$status" 2 "mismatch: the typed name is held to doctor"
  expect_fact installation FAIL "mismatch"
  expect_contains "$out" "two different installations" "mismatch: the row says what to do"
  expect_no_daemon "mismatch"
}

case_doctor_refused() {
  setup_case doctorrefused
  printf '1\n' >"$dir/state/doctor.status"
  run303 --installation "$INSTALLATION" --yes --window 3s
  expect_status "$status" 2 "doctorrefused: doctor could not answer"
  expect_contains "$out" "FACT installation: FAIL" "doctorrefused: the run stops before it can tell which installation it is on"
  expect_contains "$out" "FACT aborted: FAIL" "doctorrefused: the stop is a fact"
  expect_no_daemon "doctorrefused"
}

case_no_calls() {
  setup_case nocalls
  write_events "$dir" 0
  # Nothing but an ordinary cycle: the two cap refusals the healthy case carries
  # would be the other reading of a window with no call in it.
  : >"$dir/state/daemon.out"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "nocalls: a window in which the loop met no judgement case"
  expect_fact calls FAIL "nocalls"
  expect_contains "$out" "it met no judgement case" "nocalls: the row says why no call can be read"
  expect_fact cycle-cost FAIL "nocalls"
  expect_contains "$out" "the cost of one cycle cannot be measured" "nocalls: the cost row refuses to give a number"
}

case_model_mismatch() {
  setup_case modelmismatch
  write_daemon_output "$dir" "$OTHER_MODEL" "$CLASS" "$HOUR_REFUSAL" "$DAY_REFUSAL"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "modelmismatch: the model that answered is not the one the class holds"
  expect_fact model FAIL "modelmismatch"
  expect_contains "$out" "$OTHER_MODEL" "modelmismatch: the row names the model that ran"
  expect_contains "$out" "D1234" "modelmismatch: the row names the rule it breaks"
}

case_model_unnamed() {
  setup_case modelunnamed
  : >"$dir/state/daemon.out"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "modelunnamed: the daemon named no model"
  expect_fact model FAIL "modelunnamed"
  expect_contains "$out" "the daemon named no model" "modelunnamed: the row says what was missing"
}

case_class_unnamed() {
  setup_case classunnamed
  printf 'decision agent: model=%s\n' "$MODEL" >"$dir/state/daemon.out"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "classunnamed: the daemon named no class"
  expect_fact model FAIL "classunnamed"
  expect_contains "$out" "the daemon named no class" "classunnamed: the row says what was missing"
}

case_policy_unreadable() {
  setup_case policyunreadable
  rm -f "$dir/repo/fugaro.yaml"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "policyunreadable: no checkout resolves the policy key"
  expect_fact model FAIL "policyunreadable"
  expect_contains "$out" "--repo <the checkout whose fugaro.yaml carries the policy in force>" "policyunreadable: the row says how to make it readable"
}

case_hour_cap_only() {
  setup_case houraponly
  write_daemon_output "$dir" "$MODEL" "$CLASS" "$HOUR_REFUSAL"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "houraponly: the day cap was never reached"
  expect_fact cap-hour PASS "houraponly"
  expect_fact cap-day FAIL "houraponly"
  expect_contains "$out" "the hour's cap bites first" "houraponly: the row says why the day cap was not reached"
}

case_existing_caps_refused() {
  setup_case capsrefused
  write_events "$dir" 0
  # The daemon refused because the node's caps were already spent: a window
  # that reads no call is not the same reading as one that found nothing to do.
  {
    printf 'orchestrator %s: cycle 1\n' "$INSTALLATION"
    printf 'decision agent: %s\n' "$HOUR_REFUSAL"
  } >"$dir/state/daemon.out"
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  # The window read no call, and the caps are why: the rows above the cost are
  # the ones this case is about, and the ones that need a call are FAILs.
  expect_status "$status" 1 "capsrefused: the caps refusing is what the rows read"
  expect_fact calls PASS "capsrefused"
  expect_contains "$out" "this node's caps were already spent" "capsrefused: the row tells the two readings apart"
  expect_fact cap-hour PASS "capsrefused"
}

case_dry_run() {
  setup_case dryrun
  run303 --installation "$INSTALLATION" --dry-run
  expect_status "$status" 0 "dryrun: the plan only"
  expect_contains "$out" "FACT dry-run: PASS" "dryrun: the row"
  expect_contains "$errtext" "dry run: NOTHING below was run" "dryrun: the plan is on stderr"
  expect_contains "$errtext" "fugaro orchestrator run --decide" "dryrun: the plan names the daemon it would start"
  expect_no_daemon "dryrun"
  expect_absent "$(cat "$dir/state/calls")" "orchestrator state" "dryrun: no node was read"
  expect_absent "$out" "FACT calls:" "dryrun: the rows that were never measured are not printed"
}

case_no_installation_arg() {
  setup_case noarg
  run303 --yes
  expect_status "$status" 2 "noarg: --installation is required"
  expect_contains "$errtext" "usage: bash scripts/live-checks/check303.sh" "noarg: the usage line"
  expect_no_daemon "noarg"
}

case_unknown_arg() {
  setup_case unknownarg
  run303 --installation "$INSTALLATION" --yes --what
  expect_status "$status" 2 "unknownarg"
  expect_contains "$errtext" "unknown argument '--what'" "unknownarg: the refusal names it"
}

case_bad_window() {
  setup_case badwindow
  run303 --installation "$INSTALLATION" --yes --window 20minutes
  expect_status "$status" 2 "badwindow: a duration that is not one"
  expect_contains "$errtext" "--window takes a duration such as 20m" "badwindow: the refusal says what it takes"
  expect_no_daemon "badwindow"
}

case_missing_tools() {
  dir=$work/missingtools
  mkdir -p "$dir/empty" "$dir/tmp"
  # Everything the check runs EXCEPT fugaro, so the case is about the one tool
  # that is missing: an empty PATH would stop it at bash and prove nothing about
  # the tool this row names.
  local t
  for t in bash grep sed cut tr head tail date mktemp timeout cat rm chmod wc awk sort dirname python3 find; do
    ln -sf "$(command -v "$t")" "$dir/empty/$t" 2>/dev/null || true
  done
  RUN303_PATH="$dir/empty"
  run303 --installation "$INSTALLATION" --yes
  expect_status "$status" 2 "missingtools: fugaro is not on PATH"
  expect_contains "$errtext" "Check 303 needs 'fugaro' on PATH" "missingtools: the refusal names the tool and where"
}

case_xtrace() {
  setup_case xtrace
  RUN303_BASH=(-x)
  run303 --installation "$INSTALLATION" --yes --window 3s
  expect_status "$status" 2 "xtrace: a shell that arrived tracing is refused"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the fact"
  expect_contains "$errtext" "tracing" "xtrace: the message names it"
  expect_no_daemon "xtrace"
  expect_absent "$out" "CHECK 303 RESULT END (exit 0)" "xtrace: the run did not continue"
  expect_absent "$errtext" "$MODEL" "xtrace: the trace carries no child's output"
}

case_daemon_failed() {
  setup_case daemonfailed
  printf '1\n' >"$dir/state/daemon.status"
  : >"$dir/state/daemon.out"
  write_events "$dir" 0
  run303 --installation "$INSTALLATION" --yes --window 3s --interval 1s --breaker-seconds 3 --repo "$dir/repo"
  expect_status "$status" 1 "daemonfailed: a daemon that exited non-zero is reported, not swallowed"
  expect_contains "$errtext" "the daemon exited 1" "daemonfailed: its exit is named"
  expect_fact calls FAIL "daemonfailed: no call was made, and no cap refusal explains it away"
}

# The mutation: a copy whose cap-hour row always passes. The suite's own healthy
# expectation is a FAIL when the refusal is not there, so a copy that reports
# PASS unconditionally is exactly what the assertions above have to catch.
case_mutation_cap_hour_always_passes() {
  setup_case mutation
  local copy=$dir/mutated303.sh
  sed 's|    fact cap-hour FAIL |    fact cap-hour PASS |' "$script" >"$copy"
  if ! grep -q 'fact cap-hour PASS' "$copy"; then
    fail "mutation: the sed found no place to break the cap-hour row"
    return 0
  fi
  write_daemon_output "$dir" "$MODEL" "$CLASS" "$HOUR_REFUSAL"
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE303_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    timeout 60 "$BASH_BIN" "$copy" --installation "$INSTALLATION" --yes --window 3s --interval 1s \
    --breaker-seconds 3 --repo "$dir/repo" 2>"$dir/err") || status=$?
  expect_contains "$out" "FACT cap-hour: PASS" \
    "mutation: the broken row reports PASS, so the assertion in case_hour_cap_only is not vacuous"
  if [ "$status" -eq 0 ]; then
    fail "mutation: the broken copy still exited 0, so nothing in the suite would catch this row"
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

t case_healthy
t case_not_paused
t case_paused_before
t case_no_decide
t case_no_yes
t case_belong
t case_belong_from_doctor
t case_name_mismatch
t case_doctor_refused
t case_no_calls
t case_model_mismatch
t case_model_unnamed
t case_class_unnamed
t case_policy_unreadable
t case_hour_cap_only
t case_existing_caps_refused
t case_dry_run
t case_no_installation_arg
t case_unknown_arg
t case_bad_window
t case_missing_tools
t case_xtrace
t case_daemon_failed
t case_mutation_cap_hour_always_passes

if [ "$failures" -ne 0 ]; then
  echo "check303_test: $failures failure(s)" >&2
  exit 1
fi
echo "check303_test: all cases passed"
