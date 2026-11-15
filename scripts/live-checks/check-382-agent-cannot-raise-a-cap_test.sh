#!/usr/bin/env bash
# Tests for scripts/live-checks/check-382-agent-cannot-raise-a-cap.sh, against a
# fake `fugaro` on PATH: nothing here reaches the network, there is no cloud
# project, no cap is raised on anything and no agent runs any live step of the
# check.
#
# The fake holds the four things this check is made of and nothing else: which
# repository's caps node the project carries, the effective caps `budget show
# --json` reports, a `budget set --help` that names the real flags, and a
# `budget set` that REALLY applies what it is handed when it is not refused --
# so the check's read-back of the caps is a reading of state that changed rather
# than of words the fake was told to say, and a check that forgot to set the
# marker would be caught by the caps moving rather than by a log line.
#
# The fence itself is the fake's own: it refuses exactly when one of the markers
# that make a coding agent's session is set in ITS environment (agentMarker's own
# list), names that marker in the sentence, and prints the command the owner would
# run. It can also be told to bend the sentence -- a part missing, the command
# naming another repository, a value or a project, an invented flag, a program
# that is not this one -- because the check has to tell each of those from a
# refusal its owner can act on.
#
# The checkout is real -- a git work tree with an origin remote -- because the
# repository whose cap the raise is about is the one that remote names, and the
# test has to be able to bend it (no origin, not a work tree).
#
# Usage: bash scripts/live-checks/check-382-agent-cannot-raise-a-cap_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-382-agent-cannot-raise-a-cap.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK382_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (marker|refusal|command|unchanged|fence|aborted):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured. A dry run's block
# carries dry-run as well; expect_block_keys takes it as an extra.
BLOCK_KEYS=(xtrace agent-session project belong checkout caps raise help marker refusal command unchanged fence teardown)

# The installation every case runs against: its project is not `belong`.
PROJECT=sbx
GCP=sbx-proj
REPO=edgeapp/sandbox

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-382-agent-cannot-raise-a-cap.sh's test only.
# Every answer comes from a file in the case's own directory (FAKE382_DIR), every
# call is logged with its whole argv, and `budget set` REALLY applies what it is
# given unless its own fence refuses it -- so the check's read-backs are readings
# of state that changed rather than of words this fake was told to say.
set -euo pipefail
dir=${FAKE382_DIR:?FAKE382_DIR is not set}
knob() { if [ -f "$dir/$1" ]; then cat "$dir/$1"; else printf '%s' "$2"; fi; }
put() { printf '%s' "$1" >"$dir/$2"; }
# The editor extension's marker is logged beside the argv, because the check drops
# it from its own environment: the raise below has no terminal, and the marker the
# fence must read is the one the check puts on that command line, never one this
# shell walked in with.
log() { printf 'fugaro %s (ide-marker=%s)\n' "$*" "${CLAUDE_CODE_SSE_PORT:-none}" >>"$dir/calls"; }

# agentMarker's own list, read out of THIS process's environment: the check sets
# the marker on the raise's own command line, so a marker here is one the check
# put there (or one the suite that started it leaked, which is why the check's own
# environment is guarded before it runs anything).
find_marker() {
  local name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then printf '%s' "$name"; return 0; fi
  done
  return 1
}

# argv_value NAME args...: the value this run was handed for --NAME, so the
# sentence echoes back the value it was given rather than one the fake made up.
argv_value() {
  local want=$1
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
    "--$want")
      printf '%s' "${2:-}"
      return 0
      ;;
    esac
    shift
  done
  return 0
}

# refuse bends ONE part of the sentence (or none), so a case can hold the check to
# the part it reads. The words are raiseOwnerOnly's and AgentRefusal's.
refuse() { # refuse bend
  local bend=$1
  local prog=$0 setcmd="budget set" repo=$FAKE382_REPO value=$RAISE_VALUE proj=$FAKE382_PROJECT extra="" act="raising a cap"
  case "$bend" in
  other-repo) repo=other/thing ;;
  other-value) value=9.99 ;;
  other-project) proj=other ;;
  other-prog) prog=/usr/local/bin/other ;;
  invent-flag) extra=" --force" ;;
  no-project) proj="" ;;
  not-set) setcmd=raise ;;
  other-act) act="choosing the project-wide budget mode" ;;
  esac
  printf 'fugaro: %s is the owner%ss own step: a coding agent%ss session is present (%s is set), so nothing is applied: run this in your own terminal window, not through a coding agent; nothing was changed. Run this yourself at your own terminal: %s %s --repo=%s --daily=%s%s%s\n' \
    "$act" "'" "'" "$MARKER_NAME" "$prog" "$setcmd" "$repo" "$value" "${proj:+ --project=$proj}" "$extra" >&2
  exit 1
}

# apply_set writes what the same invocation would: the check's own reads after the
# raise are readings of this, not of a line the fake printed.
apply_set() {
  while [ $# -gt 0 ]; do
    case "$1" in
    --clear)
      put no node.exists
      put "" node.daily
      put "" node.perrun
      shift
      ;;
    --daily)
      put "${2:-}" node.daily
      put yes node.exists
      shift 2
      ;;
    --per-run)
      put "${2:-}" node.perrun
      put yes node.exists
      shift 2
      ;;
    *) shift ;;
    esac
  done
}

help_out() {
  case "$(knob help.mode ok)" in
  fail)
    printf 'Error: the help could not be printed\n' >&2
    exit 1
    ;;
  empty)
    printf 'Set caps in the project\x27s budget database.\n\nUsage:\n  fugaro budget set\n'
    exit 0
    ;;
  noproject)
    printf 'Set caps in the project\x27s budget database.\n\nFlags:\n      --ask\n      --clear\n      --daily float\n      --per-run float\n      --repo string\n'
    exit 0
    ;;
  esac
  cat <<'HELP'
Set caps in the project's budget database, with an ETag-guarded write.

Usage:
  fugaro budget set (--global | --defaults | --repo [owner/name]) [--daily USD] [--per-run USD] | --clear [flags]

Flags:
      --ask                  ask before a raise, at a terminal that can answer
      --clear                remove the scope's caps
      --config string        the project config file to use
      --daily float          the daily cap, in US dollars
      --defaults             the default caps of repositories without their own
      --gcp-project string   the GCP project ID
      --global               the project's own caps
  -h, --help                 help for set
      --max-reserve float    the largest single lease, in US dollars (with --global)
      --mode string          observe or enforce (with --global)
      --per-run float        the per-run cap, in US dollars
      --project string       the Fugaro project to act on
      --region string        GCP region (overrides the project config's)
      --repo string          one repository's caps, given as owner/name
HELP
  exit 0
}

case "${1:-}" in
doctor)
  log "$*"
  m=$(knob doctor.mode ok)
  case "$m" in
  fail)
    printf 'Error: the installation could not be read\n' >&2
    exit 1
    ;;
  belong) printf '{"ok":true,"project":{"name":"belong","gcp_project":"belong-proj"}}\n' ;;
  *) printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s"}}\n' "$FAKE382_PROJECT" "$FAKE382_GCP" ;;
  esac
  ;;
project)
  log "$*"
  case "${2:-}" in
  show)
    case "$(knob project.mode ok)" in
    fail)
      printf 'Error: the project could not be read\n' >&2
      exit 1
      ;;
    esac
    if [ "$(knob node.exists yes)" = no ]; then
      printf '{"ok":true,"caps":{"repos":{}}}\n'
      exit 0
    fi
    d=$(knob node.daily 5)
    p=$(knob node.perrun "")
    dj=null
    [ -n "$d" ] && dj=$d
    pj=null
    [ -n "$p" ] && pj=$p
    printf '{"ok":true,"caps":{"repos":{"edgeapp%%2Fsandbox":{"repo":"%s","daily_cap_usd":%s,"per_run_cap_usd":%s}}}}\n' \
      "$FAKE382_REPO" "$dj" "$pj"
    ;;
  *)
    printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
    exit 64
    ;;
  esac
  ;;
budget)
  log "$*"
  case "${2:-}" in
  show)
    n=$(cat "$dir/showcount" 2>/dev/null || printf 0)
    n=$((n + 1))
    printf '%s' "$n" >"$dir/showcount"
    m=$(knob budget.mode ok)
    case "$m" in
    fail)
      printf 'Error: the budget could not be read\n' >&2
      exit 1
      ;;
    # fail-second: the first read answers and every read after it does not, which
    # is how a case makes the read AFTER the raise the one that fails.
    fail-second)
      if [ "$n" -ge 2 ]; then
        printf 'Error: the budget could not be read\n' >&2
        exit 1
      fi
      ;;
    norow)
      printf '{"mode":"%s","day":"2026-11-14","max_reserve_usd":20,"global":{},"defaults":{},"repos":[],"running":[],"warnings":[]}\n' "$(knob budgetmode.mode enforce)"
      exit 0
      ;;
    esac
    d=$(knob node.daily 5)
    p=$(knob node.perrun "")
    # budget.daily_source bends the effective daily's source on its own, which is
    # the one state a case has to be able to make: a source of "repo" over a raw
    # node list that carries no node is what the check must refuse to write to.
    if [ -n "$(knob budget.daily_source '')" ]; then
      dsrc=$(knob budget.daily_source '')
      effd=$(knob node.daily 5)
      [ -n "$effd" ] || effd=$(knob default_daily 10)
    elif [ "$(knob node.exists yes)" = no ] || [ -z "$d" ]; then
      effd=$(knob default_daily 10)
      dsrc=default
    else
      effd=$d
      dsrc=repo
    fi
    if [ -n "$p" ]; then
      effp=$p
      psrc=repo
    else
      effp=$(knob default_perrun 3)
      psrc=default
    fi
    row=$(printf '{"slug":"edgeapp%%2Fsandbox","repo":"%s","daily_cap_usd":%s,"daily_cap_source":"%s","per_run_cap_usd":%s,"per_run_cap_source":"%s","counted_usd":%s}' \
      "$FAKE382_REPO" "$effd" "$dsrc" "$effp" "$psrc" "$(knob counted 0)")
    cat <<JSON
{"project":"$FAKE382_PROJECT","gcp_project":"$FAKE382_GCP","mode":"$(knob budgetmode.mode enforce)","day":"2026-11-14","max_reserve_usd":$(knob max_reserve 20),"global":{"daily_cap_usd":$(knob global_daily 50),"per_run_cap_usd":$(knob global_perrun 25),"counted_usd":0},"defaults":{"repo_daily_cap_usd":$(knob default_daily 10),"repo_per_run_cap_usd":$(knob default_perrun 3)},"repos":[$row],"running":[],"warnings":[]}
JSON
    ;;
  set)
    case " $* " in
    *" --help "*)
      help_out
      ;;
    esac
    n=$(cat "$dir/setcount" 2>/dev/null || printf 0)
    n=$((n + 1))
    printf '%s' "$n" >"$dir/setcount"
    RAISE_VALUE=$(argv_value daily "$@")
    MARKER_NAME=$(find_marker || true)
    log "$* (marker=${MARKER_NAME:-unset})"
    m=$(knob set.mode refuse)
    case "$m" in
    noparts)
      printf 'fugaro: that raise is refused here\n' >&2
      exit 1
      ;;
    silent) exit 1 ;;
    odd)
      printf 'Error: the caps could not be written\n' >&2
      exit 3
      ;;
    hang) exec sleep 300 ;;
    # allow: the fence never fires, so the check's own raise lands and the caps
    # move -- the loudest finding this check can read.
    allow)
      apply_set "${@:3}"
      printf 'the caps of %s were updated\n' "$FAKE382_REPO"
      exit 0
      ;;
    # allow-second-fail: the raise lands and the putting back does not.
    allow-second-fail)
      if [ "$n" -ge 2 ]; then
        printf 'Error: the cap could not be written\n' >&2
        exit 1
      fi
      apply_set "${@:3}"
      printf 'the caps of %s were updated\n' "$FAKE382_REPO"
      exit 0
      ;;
    # refuse-then-apply: the sentence is the right one AND the write landed, which
    # is the state the check's unchanged row and its putting back exist for. Only
    # the first call: the putting back that follows carries no marker and must land.
    refuse-then-apply)
      if [ "$n" -eq 1 ]; then
        apply_set "${@:3}"
        refuse "$(knob set.bend none)"
      fi
      ;;
    other-act) refuse other-act ;;
    bend) refuse "$(knob set.bend none)" ;;
    esac
    if [ -n "$MARKER_NAME" ]; then
      refuse "$(knob set.bend none)"
    fi
    apply_set "${@:3}"
    printf 'the caps of %s were updated\n' "$FAKE382_REPO"
    ;;
  *)
    printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# The tools the check itself needs, symlinked into a directory of their own, so a
# case can run it with a PATH that holds everything BUT fugaro.
write_tool_path() { # dir names...
  local dir=$1
  shift
  mkdir -p "$dir"
  local tool path
  for tool in "$@"; do
    path=$(command -v "$tool") || {
      fail "the test needs $tool on PATH"
      continue
    }
    ln -sf "$path" "$dir/$tool"
  done
}

# ------------------------------------------------------------------- harness
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home" "$dir/repo"
  write_fake "$dir"
  (
    cd "$dir/repo" || exit 1
    git init -q .
    git remote add origin "https://github.com/$REPO.git"
  ) >/dev/null 2>&1
  : >"$dir/state/calls"
  state_file node.exists yes
  state_file node.daily 5
  state_file node.perrun ""
  status=0
  out=""
  err=""
  scratch=""
  RUN382_ARGS=()
  RUN382_ENV=()
  RUN382_BASH=(bash)
}

run382() {
  status=0
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  out=$(env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE382_DIR="$dir/state" FAKE382_PROJECT="$PROJECT" FAKE382_GCP="$GCP" FAKE382_REPO="$REPO" \
    ${RUN382_ENV[@]+"${RUN382_ENV[@]}"} \
    "${RUN382_BASH[@]}" "$script" ${RUN382_ARGS[@]+"${RUN382_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN382_ARGS=()
  RUN382_ENV=()
  RUN382_BASH=(bash)
}
run382_default() { run382 --checkout "$dir/repo" --project "$PROJECT" "$@"; }

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what extra...
  local key what=$1
  shift
  expect_contains "$out" "CHECK 382 RESULT" "$what: the block"
  local n
  for key in "${BLOCK_KEYS[@]}" "$@"; do
    [ -n "$key" ] || continue
    expect_contains "$out" "FACT $key:" "$what: the $key row"
    # Exactly one: a key recorded twice must be resolved to the last reading, not
    # printed twice.
    n=$(printf '%s\n' "$out" | grep -c "^FACT $key:")
    [ "$n" = 1 ] || fail "$what: the $key row is printed $n times"
  done
  expect_contains "$out" "CHECK 382 RESULT END (exit " "$what: the block's end"
}

calls() { cat "$dir/state/calls"; }

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}
expect_call() { # what needle
  expect_contains "$(calls)" "$2" "$1"
}
expect_no_call() { # what needle
  expect_absent "$(calls)" "$2" "$1"
}
expect_state() { # what file value
  local got
  got=$(cat "$dir/state/$2" 2>/dev/null || true)
  [ "$got" = "$3" ] || fail "$1: expected $2 to be '$3', got '$got'"
}
expect_no_state() { # what file
  local got
  got=$(cat "$dir/state/$2" 2>/dev/null || true)
  [ -z "$got" ] || fail "$1: $2 says '$got', so a call was made"
}

assert_scratch_removed() { # extra what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

# mode NAME VALUE writes a knob the fake reads as NAME.mode; state_file NAME
# VALUE writes one of the fake's own state files, which carry no suffix.
mode() { # name value
  printf '%s\n' "$2" >"$dir/state/$1.mode"
}
state_file() { # name value
  printf '%s\n' "$2" >"$dir/state/$1"
}

# rewrite_script DEST OLD NEW: a copy of the check with one exact line replaced,
# so a self-test can prove a case's assertion is not vacuous rather than argue it.
rewrite_script() { # dest old new
  python3 - "$script" "$1" "$2" "$3" <<'PY'
import sys

src, dest, old, new = sys.argv[1:5]
with open(src) as fh:
    text = fh.read()
if old not in text:
    raise SystemExit("rewrite_script: the line to replace is not in the script: %r" % old)
with open(dest, "w") as fh:
    fh.write(text.replace(old, new, 1))
PY
  chmod +x "$1"
}

# --------------------------------------------------------------- the readings
# The whole point, end to end: a `budget set` that raises this repository's daily
# cap is refused with exit 1 while the marker is set on the child alone, the
# sentence carries the act, the marker, the two "nothing" claims and the command to
# run instead, and the caps read back exactly as they were -- nothing was written.
test_the_raise_is_refused_and_nothing_moves() {
  setup_case the-raise
  run382_default
  expect_status "$status" 0 "the raise"
  expect_block_keys "the raise"
  expect_contains "$(fact_line xtrace)" "PASS" "the raise: not tracing"
  expect_contains "$(fact_line agent-session)" "PASS" "the raise: the owner's own session"
  expect_contains "$(fact_line project)" "PASS" "the raise: doctor's installation"
  expect_contains "$(fact_line project)" "sbx" "the raise: which project"
  expect_contains "$(fact_line belong)" "PASS" "the raise: not belong"
  expect_contains "$(fact_line checkout)" "PASS" "the raise: the checkout"
  expect_contains "$(fact_line checkout)" "$REPO" "the raise: the repository the origin names"
  expect_contains "$(fact_line caps)" "PASS" "the raise: the caps node was read"
  expect_contains "$(fact_line caps)" "daily \$5" "the raise: the node's own cap"
  expect_contains "$(fact_line raise)" "PASS" "the raise: the value"
  expect_contains "$(fact_line raise)" "--daily 5.01" "the raise: one cent over what it holds"
  expect_contains "$(fact_line help)" "PASS" "the raise: the flags were read"
  expect_contains "$(fact_line marker)" "PASS" "the raise: the marker reached the child"
  expect_contains "$(fact_line marker)" "exit 1" "the raise: the refusal's own code"
  expect_contains "$(fact_line refusal)" "PASS" "the raise: the sentence"
  expect_contains "$(fact_line refusal)" "raising a cap is the owner's own step" "the raise: the act in its own words"
  expect_contains "$(fact_line command)" "PASS" "the raise: the printed command"
  expect_contains "$(fact_line command)" "--daily=5.01" "the raise: the command carries the value"
  expect_contains "$(fact_line command)" "--project=sbx" "the raise: the command carries the project"
  expect_contains "$(fact_line unchanged)" "PASS" "the raise: the read-back"
  expect_contains "$(fact_line unchanged)" "exactly as they were" "the raise: nothing moved"
  expect_contains "$(fact_line fence)" "HOLDS" "the raise: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "the raise: nothing was put back"
  expect_contains "$(fact_line teardown)" "nothing of your project was changed" "the raise: why"
  expect_call "the raise: the marker went on the child's own command line" "(marker=CLAUDECODE)"
  expect_call "the raise: one budget set, the raise itself" "budget set --repo $REPO --daily 5.01"
  expect_state "the raise: exactly one write was attempted" setcount 1
  expect_state "the raise: the cap is exactly what it was" node.daily 5
  assert_scratch_removed "" "the raise"
}

# The value has to be a raise whatever the node holds, or the CLI answers "nothing
# changed" instead of asking the fence at all: one cent over the highest cap the
# node holds of its own.
test_the_value_is_a_raise_over_both_caps() {
  setup_case raise-over-both
  state_file node.perrun 2
  run382_default
  expect_status "$status" 0 "a node with both caps"
  expect_contains "$(fact_line raise)" "--daily 5.01" "a node with both caps: one cent over the daily"
  expect_call "a node with both caps: the raise" "budget set --repo $REPO --daily 5.01"
  expect_contains "$(fact_line caps)" "per-run \$2" "a node with both caps: the per-run is read too"
}

test_a_node_with_only_a_per_run_is_raised_from_it() {
  setup_case per-run-only
  state_file node.daily ""
  state_file node.perrun 7
  run382_default
  expect_status "$status" 0 "a node with only a per-run"
  expect_contains "$(fact_line caps)" "per-run \$7 (no daily of its own)" "a node with only a per-run: what it holds"
  expect_contains "$(fact_line raise)" "--daily 7.01" "a node with only a per-run: one cent over it"
  expect_call "a node with only a per-run: the raise" "budget set --repo $REPO --daily 7.01"
}

test_a_node_with_no_cap_of_its_own_is_raised_from_nothing() {
  setup_case no-node
  state_file node.exists no
  run382_default
  expect_status "$status" 0 "a node with no cap of its own"
  expect_contains "$(fact_line caps)" "no cap at all" "a node with no cap of its own: what it holds"
  expect_contains "$(fact_line raise)" "--daily 0.01" "a node with no cap of its own: the smallest value the flag takes"
  expect_call "a node with no cap of its own: the raise" "budget set --repo $REPO --daily 0.01"
}

# The marker the check sets is the one the refusal has to name: with the knob moved
# to another marker in agentMarker's list, both sides follow it.
test_another_marker_is_read_the_same_way() {
  setup_case another-marker
  RUN382_ENV=(CHECK382_MARKER=AI_AGENT)
  run382_default
  expect_status "$status" 0 "another marker"
  expect_contains "$(fact_line refusal)" "AI_AGENT is set" "another marker: the sentence names it"
  expect_call "another marker: it is the one on the child's command line" "(marker=AI_AGENT)"
}

# A raise that ended 0 is the finding this check exists for: a coding agent's
# session raised a cap. The caps moved, the row says so, and the teardown puts them
# back and reads them back.
test_a_raise_that_is_not_refused_is_a_fail() {
  setup_case not-refused
  mode set allow
  run382_default
  expect_status "$status" 1 "a raise that was not refused"
  expect_contains "$(fact_line marker)" "FAIL" "a raise that was not refused: the marker row"
  expect_contains "$(fact_line marker)" "NOT refused" "a raise that was not refused: what happened"
  expect_contains "$(fact_line refusal)" "FAIL" "a raise that was not refused: there is no sentence"
  expect_contains "$(fact_line unchanged)" "FAIL" "a raise that was not refused: the read-back"
  expect_contains "$(fact_line unchanged)" "MOVED" "a raise that was not refused: it says the caps moved"
  expect_contains "$(fact_line fence)" "does NOT hold" "a raise that was not refused: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a raise that was not refused: the putting back"
  expect_contains "$(fact_line teardown)" "are back" "a raise that was not refused: it says they are back"
  expect_call "a raise that was not refused: the putting back" "budget set --repo $REPO --daily 5"
  expect_state "a raise that was not refused: the cap is back" node.daily 5
  expect_state "a raise that was not refused: the raise and the putting back" setcount 2
}

# A refusal that PRINTED the right sentence and applied the write anyway is the
# same finding read the other way: the check must read the caps rather than trust
# the sentence, and put them back.
test_a_refusal_that_landed_anyway_is_read_back_and_put_back() {
  setup_case refusal-landed
  mode set refuse-then-apply
  run382_default
  expect_status "$status" 1 "a refusal that landed anyway"
  expect_contains "$(fact_line refusal)" "PASS" "a refusal that landed anyway: the sentence is the right one"
  expect_contains "$(fact_line command)" "PASS" "a refusal that landed anyway: the command is runnable"
  expect_contains "$(fact_line unchanged)" "FAIL" "a refusal that landed anyway: the read-back"
  expect_contains "$(fact_line fence)" "does NOT hold" "a refusal that landed anyway: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a refusal that landed anyway: the putting back"
  expect_state "a refusal that landed anyway: the cap is back" node.daily 5
}

# The putting back on a node that held ONLY a per-run cap of its own: the raise this
# check runs is a daily one, so that node now holds a daily this check wrote, and a
# putting back that wrote only the per-run back would leave that daily standing and
# report a project that is not the one it found -- which is what the state read-backs
# at the end are here for.
test_a_landed_raise_on_a_per_run_only_node_is_put_back_whole() {
  setup_case per-run-only-landed
  state_file node.daily ""
  state_file node.perrun 7
  mode set refuse-then-apply
  run382_default
  expect_status "$status" 1 "a landed raise on a node with only a per-run"
  expect_contains "$(fact_line unchanged)" "FAIL" "a landed raise on a node with only a per-run: the read-back"
  expect_contains "$(fact_line fence)" "does NOT hold" "a landed raise on a node with only a per-run: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a landed raise on a node with only a per-run: the putting back"
  expect_contains "$(fact_line teardown)" "per-run \$7" "a landed raise on a node with only a per-run: what went back"
  expect_call "a landed raise on a node with only a per-run: the clear came first" "budget set --repo $REPO --clear"
  expect_call "a landed raise on a node with only a per-run: and the per-run went back after it" "budget set --repo $REPO --per-run 7"
  expect_state "a landed raise on a node with only a per-run: the per-run is back" node.perrun 7
  expect_state "a landed raise on a node with only a per-run: the raised daily is gone" node.daily ""
}

# The parts of the sentence are the whole point: an agent's report has to carry
# something its owner can act on, so a missing part is a FAIL naming it.
test_a_refusal_that_lost_a_part_is_a_fail() {
  setup_case lost-a-part
  mode set noparts
  run382_default
  expect_status "$status" 1 "a refusal that lost a part"
  expect_contains "$(fact_line marker)" "PASS" "a refusal that lost a part: it was refused"
  expect_contains "$(fact_line refusal)" "FAIL" "a refusal that lost a part: the sentence row"
  expect_contains "$(fact_line refusal)" "missing: " "a refusal that lost a part: the row names what is missing"
  expect_contains "$(fact_line refusal)" "raising a cap is the owner's own step:" "a refusal that lost a part: the act is one of them"
  expect_contains "$(fact_line refusal)" "nothing was changed" "a refusal that lost a part: the claim is one of them"
  expect_contains "$(fact_line fence)" "does NOT hold" "a refusal that lost a part: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a refusal that lost a part: nothing to put back"
}

# A refusal about ANOTHER act is not this check's reading: the sentence has to be
# about raising a cap, or a check that bent any refusal into shape would report a
# pass for a command that was refused for some other reason.
test_a_refusal_about_another_act_is_a_fail() {
  setup_case another-act
  mode set other-act
  run382_default
  expect_status "$status" 1 "a refusal about another act"
  expect_contains "$(fact_line refusal)" "FAIL" "a refusal about another act: the sentence row"
  expect_contains "$(fact_line refusal)" "raising a cap is the owner's own step" "a refusal about another act: what it is missing"
  expect_contains "$(fact_line fence)" "does NOT hold" "a refusal about another act: the verdict"
}

test_a_refusal_that_prints_nothing_is_a_fail() {
  setup_case silent-refusal
  mode set silent
  run382_default
  expect_status "$status" 1 "a silent refusal"
  expect_contains "$(fact_line refusal)" "FAIL" "a silent refusal: the sentence row"
  expect_contains "$(fact_line refusal)" "<nothing>" "a silent refusal: it says the command printed nothing"
}

test_an_unexpected_exit_status_is_a_fail() {
  setup_case odd-status
  mode set odd
  run382_default
  expect_status "$status" 1 "an unexpected status"
  expect_contains "$(fact_line marker)" "FAIL" "an unexpected status: the marker row"
  expect_contains "$(fact_line marker)" "exit 3" "an unexpected status: the code"
  expect_contains "$(fact_line refusal)" "FAIL" "an unexpected status: there is no refusal to read"
  expect_contains "$(fact_line fence)" "does NOT hold" "an unexpected status: the verdict"
}

# A command that never ends is its own finding -- a refusal is immediate, and
# nothing in `budget set` waits on a terminal any more -- so the check has to end it
# rather than hang with it, and the caps are read back afterwards.
test_a_raise_that_never_ends_is_a_fail() {
  setup_case never-ends
  mode set hang
  RUN382_ARGS=(--deadline 5)
  run382_default
  expect_status "$status" 1 "a raise that never ends"
  expect_contains "$(fact_line marker)" "FAIL" "a raise that never ends: the marker row"
  expect_contains "$(fact_line marker)" "still running after 5s" "a raise that never ends: the deadline"
  expect_contains "$(fact_line fence)" "does NOT hold" "a raise that never ends: the verdict"
  expect_contains "$(fact_line fence)" "NOT refused" "a raise that never ends: what happened"
  expect_contains "$(fact_line teardown)" "PASS" "a raise that never ends: nothing to put back"
}

# ------------------------------------------------------------------ the command
# Each of these bends ONE part of the printed command: a command an owner cannot
# run is the whole of what an agent's report is worth, so each is a FAIL naming the
# part.
expect_command_fails() { # what needle
  local what=$1 needle=$2
  run382_default
  expect_status "$status" 1 "$what"
  expect_contains "$(fact_line refusal)" "PASS" "$what: the sentence itself is complete"
  expect_contains "$(fact_line command)" "FAIL" "$what: the command row"
  expect_contains "$(fact_line command)" "$needle" "$what: what the row names"
  expect_contains "$(fact_line unchanged)" "PASS" "$what: nothing moved"
  expect_contains "$(fact_line fence)" "does NOT hold" "$what: the verdict"
}

test_a_command_naming_another_repository_is_a_fail() {
  setup_case command-other-repo
  mode set bend
  state_file set.bend other-repo
  expect_command_fails "a command naming another repository" "the repository this check raised the cap of ($REPO)"
}

test_a_command_carrying_another_value_is_a_fail() {
  setup_case command-other-value
  mode set bend
  state_file set.bend other-value
  expect_command_fails "a command carrying another value" "the value this check raised it to (\$5.01)"
}

test_a_command_naming_another_project_is_a_fail() {
  setup_case command-other-project
  mode set bend
  state_file set.bend other-project
  expect_command_fails "a command naming another project" "--project sbx"
}

# A command without the project would act on whatever the owner's terminal happens
# to be set to, which is the one thing the fence prints the name to prevent.
test_a_command_without_the_project_is_a_fail() {
  setup_case command-no-project
  mode set bend
  state_file set.bend no-project
  run382_default
  expect_status "$status" 1 "a command without the project"
  expect_contains "$(fact_line command)" "FAIL" "a command without the project: the command row"
  expect_contains "$(fact_line command)" "--project (the fence prints the project" "a command without the project: why"
}

test_a_command_with_an_invented_flag_is_a_fail() {
  setup_case command-invented-flag
  mode set bend
  state_file set.bend invent-flag
  expect_command_fails "a command with an invented flag" "--force, which"
}

# A sentence that named a command that is not this one is refused as a refusal that
# lost its command: `budget set` is one of the parts this check reads for.
test_a_command_that_is_not_budget_set_is_a_fail() {
  setup_case command-not-set
  mode set bend
  state_file set.bend not-set
  run382_default
  expect_status "$status" 1 "a command that is not budget set"
  expect_contains "$(fact_line refusal)" "FAIL" "a command that is not budget set: the sentence row"
  expect_contains "$(fact_line refusal)" "missing: budget set" "a command that is not budget set: the part it lost"
  expect_contains "$(fact_line command)" "FAIL" "a command that is not budget set: the command row"
  expect_contains "$(fact_line command)" "the command itself (\`budget set\`)" "a command that is not budget set: what the row names"
  expect_contains "$(fact_line fence)" "does NOT hold" "a command that is not budget set: the verdict"
}

test_a_command_from_another_program_is_a_fail() {
  setup_case command-other-program
  mode set bend
  state_file set.bend other-prog
  expect_command_fails "a command from another program" "the program this check ran"
}

# The flags are read against the CLI the check actually ran, so a help that does
# not name --project catches a printed command carrying it -- which is a real
# disagreement between the printer and the command it prints, and not a bent help
# in the wild.
test_a_help_that_does_not_name_the_project_is_a_fail() {
  setup_case help-no-project
  mode help noproject
  run382_default
  expect_status "$status" 1 "a help that does not name the project"
  expect_contains "$(fact_line help)" "PASS" "a help that does not name the project: the help row"
  expect_contains "$(fact_line command)" "FAIL" "a help that does not name the project: the command row"
  expect_contains "$(fact_line command)" "--project, which" "a help that does not name the project: the flag"
}

test_a_help_that_cannot_be_read_is_not_measured() {
  setup_case help-unreadable
  mode help fail
  run382_default
  expect_status "$status" 2 "a help that cannot be read"
  expect_contains "$(fact_line command)" "FAIL" "a help that cannot be read: the command row"
  expect_contains "$(fact_line command)" "did not print the flags" "a help that cannot be read: it is not an answer"
  expect_contains "$(fact_line fence)" "NOT MEASURED" "a help that cannot be read: the verdict"
  expect_contains "$(fact_line fence)" "the command row" "a help that cannot be read: the row that stopped it"
  expect_no_call "a help that cannot be read: no raise was run" "budget set --repo"
  expect_no_state "a help that cannot be read: nothing was written" setcount
}

test_a_help_with_no_flags_is_not_measured() {
  setup_case help-empty
  mode help empty
  run382_default
  expect_status "$status" 2 "a help with no flags"
  expect_contains "$(fact_line command)" "did not print the flags" "a help with no flags: the command row"
  expect_contains "$(fact_line fence)" "NOT MEASURED" "a help with no flags: the verdict"
}

# ------------------------------------------------------------- the reads
test_a_budget_that_cannot_be_read_is_not_measured() {
  setup_case budget-unreadable
  mode budget fail
  run382_default
  expect_status "$status" 2 "a budget that cannot be read"
  expect_contains "$(fact_line caps)" "FAIL" "a budget that cannot be read: the caps row"
  expect_contains "$(fact_line caps)" "cannot see the cap" "a budget that cannot be read: why"
  expect_contains "$(fact_line fence)" "NOT MEASURED" "a budget that cannot be read: the verdict"
  expect_no_call "a budget that cannot be read: nothing was written" "budget set"
}

test_a_caps_document_without_a_row_is_not_measured() {
  setup_case no-row
  mode budget norow
  run382_default
  expect_status "$status" 2 "a caps document with no row"
  expect_contains "$(fact_line caps)" "FAIL" "a caps document with no row: the caps row"
  expect_contains "$(fact_line fence)" "NOT MEASURED" "a caps document with no row: the verdict"
  expect_no_call "a caps document with no row: nothing was written" "budget set"
}

test_a_node_that_cannot_be_read_is_not_measured() {
  setup_case node-unreadable
  mode project fail
  run382_default
  expect_status "$status" 2 "an unreadable node"
  expect_contains "$(fact_line caps)" "FAIL" "an unreadable node: the caps row"
  expect_contains "$(fact_line caps)" "will not raise a cap it cannot put back" "an unreadable node: why"
  expect_contains "$(fact_line fence)" "NOT MEASURED" "an unreadable node: the verdict"
  expect_no_call "an unreadable node: nothing was written" "budget set"
}

# The other way a node is unreadable: the effective caps say the repository's own
# node supplies one, while the raw nodes list no node under that name. A raise on a
# node the check cannot see is a change to the project, not a reading.
test_a_node_the_check_cannot_match_stops() {
  setup_case node-unmatched
  state_file node.exists no
  state_file budget.daily_source repo
  run382_default
  expect_status "$status" 2 "a node the check cannot match"
  expect_contains "$(fact_line caps)" "FAIL" "a node the check cannot match: the caps row"
  expect_contains "$(fact_line caps)" "will not raise a cap it cannot put back" "a node the check cannot match: why"
  expect_no_call "a node the check cannot match: nothing was written" "budget set"
}

# A caps read that fails AFTER the raise is the one state where the check cannot
# say whether the raise landed: it puts the caps back rather than assume, and says
# that the putting back itself could not be read.
test_a_read_back_that_fails_puts_the_caps_back_and_stops() {
  setup_case read-back-lost
  mode set allow
  mode budget fail-second
  run382_default
  expect_status "$status" 2 "a read-back that failed"
  expect_contains "$(fact_line unchanged)" "FAIL" "a read-back that failed: the read-back row"
  expect_contains "$(fact_line unchanged)" "raise may have landed" "a read-back that failed: why the putting back runs"
  expect_contains "$(fact_line teardown)" "FAIL" "a read-back that failed: the teardown row"
  expect_contains "$(fact_line teardown)" "could not be read" "a read-back that failed: what it could not do"
  expect_call "a read-back that failed: the putting back was attempted" "budget set --repo $REPO --daily 5"
}

# The putting back is the one thing an owner has to be able to trust, and a failure
# there is a finding of its own, with the command to run by hand.
test_a_putting_back_that_fails_is_a_finding() {
  setup_case restore-failed
  mode set allow-second-fail
  run382_default
  expect_status "$status" 1 "a putting back that failed"
  expect_contains "$(fact_line unchanged)" "FAIL" "a putting back that failed: the raise landed"
  expect_contains "$(fact_line fence)" "does NOT hold" "a putting back that failed: the verdict"
  expect_contains "$(fact_line teardown)" "FAIL" "a putting back that failed: the teardown row"
  expect_contains "$(fact_line teardown)" "were NOT put back" "a putting back that failed: what happened"
  expect_contains "$(fact_line teardown)" "Run this yourself" "a putting back that failed: the command"
}

# ------------------------------------------------------------------ the guards
test_a_coding_agents_session_is_refused() {
  setup_case agent-session
  RUN382_ENV=(CLAUDECODE=1)
  run382_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "FAIL" "a coding agent's session: the row"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE is set" "a coding agent's session: which marker"
  expect_contains "$(fact_line agent-session)" "No agent runs any step of it" "a coding agent's session: whose check this is"
  expect_no_calls "a coding agent's session: not even doctor was asked"
}

# The editor extension's own marker is not a coding agent's: it says this shell
# sits in an editor's terminal, where the owner is still the one typing, so the
# guard does not refuse it. It must not reach the raise either -- that child has no
# terminal, and the marker the fence has to read is the one this check puts on its
# command line, never one the check's own shell walked in with.
test_an_editors_own_marker_is_dropped_not_refused() {
  setup_case editor-marker
  RUN382_ENV=(CLAUDE_CODE_SSE_PORT=12345)
  run382_default
  expect_status "$status" 0 "an editor's own marker"
  expect_contains "$(fact_line agent-session)" "PASS" "an editor's own marker: the owner's own session, not refused"
  expect_contains "$(fact_line agent-session)" "CLAUDE_CODE_SSE_PORT" "an editor's own marker: the row says what it did with it"
  expect_contains "$(fact_line fence)" "HOLDS" "an editor's own marker: the reading is the check's own"
  expect_absent "$(calls)" "ide-marker=12345" "an editor's own marker: no call saw it"
  expect_call "an editor's own marker: the raise still carried the check's own marker" "(marker=CLAUDECODE)"
  expect_call "an editor's own marker: and no other" "ide-marker=none"
}

test_a_shell_that_arrived_tracing_is_refused() {
  setup_case tracing
  RUN382_BASH=(bash -x)
  run382_default
  expect_status "$status" 2 "a shell that arrived tracing"
  expect_contains "$(fact_line xtrace)" "FAIL" "a shell that arrived tracing: the row"
  expect_contains "$(fact_line xtrace)" "tracing" "a shell that arrived tracing: why"
  expect_no_calls "a shell that arrived tracing: nothing was called"
}

test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run382 --checkout "$dir/repo" --project belong
  expect_status "$status" 2 "belong in what you typed"
  expect_contains "$(fact_line belong)" "FAIL" "belong in what you typed: the row"
  expect_contains "$(fact_line belong)" "which this check must never touch" "belong in what you typed: why"
  expect_no_calls "belong in what you typed: nothing was called"
}

test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-doctor
  mode doctor belong
  run382 --checkout "$dir/repo"
  expect_status "$status" 2 "belong in what doctor answers"
  expect_contains "$(fact_line belong)" "FAIL" "belong in what doctor answers: the row"
  expect_contains "$(fact_line belong)" "'belong' (the config in force)" "belong in what doctor answers: where it was found"
  expect_call "belong in what doctor answers: doctor was asked" "fugaro doctor --json"
  expect_no_call "belong in what doctor answers: nothing was raised" "budget set"
}

# The non-vacuity of that guard, as a reading rather than an argument: with the
# substring test rewritten to match nothing, the owner's own installation gets all
# the way through the check -- so the refusal above IS the guard's.
test_the_belong_guard_is_not_vacuous() {
  setup_case belong-not-vacuous
  local broken=$dir/check-382-broken.sh
  rewrite_script "$broken" '  *belong*) return 0 ;;' '  *belong*) return 1 ;;'
  script=$broken
  RUN382_ENV=(FAKE382_PROJECT=belong)
  run382 --checkout "$dir/repo"
  script=$here/check-382-agent-cannot-raise-a-cap.sh
  expect_status "$status" 0 "the belong guard is not vacuous"
  expect_contains "$(fact_line belong)" "PASS" "the belong guard is not vacuous: the rewritten guard let it through"
  expect_call "the belong guard is not vacuous: the raise ran against it" "budget set --repo $REPO --daily 5.01"
}

test_a_project_that_is_not_doctors_stops() {
  setup_case other-project
  run382 --checkout "$dir/repo" --project other
  expect_status "$status" 2 "a project that is not doctor's"
  expect_contains "$err" "two different installations" "a project that is not doctor's: the message"
  expect_no_call "a project that is not doctor's: nothing was raised" "budget set"
}

test_doctor_refused_with_a_project_typed_carries_on() {
  setup_case doctor-refused
  mode doctor fail
  run382_default
  expect_status "$status" 0 "doctor refused with a project typed"
  expect_contains "$(fact_line project)" "INFO" "doctor refused with a project typed: the row is an INFO"
  expect_contains "$(fact_line project)" "you typed" "doctor refused with a project typed: why"
  expect_contains "$(fact_line command)" "PASS" "doctor refused with a project typed: the project read from the flag"
  expect_contains "$(fact_line fence)" "HOLDS" "doctor refused with a project typed: the verdict"
}

test_doctor_refused_with_no_project_typed_stops() {
  setup_case doctor-refused-no-project
  mode doctor fail
  run382 --checkout "$dir/repo"
  expect_status "$status" 2 "doctor refused with no project typed"
  expect_contains "$(fact_line project)" "FAIL" "doctor refused with no project typed: the row"
  expect_contains "$(fact_line project)" "cannot tell which installation" "doctor refused with no project typed: why"
  expect_no_call "doctor refused with no project typed: nothing was raised" "budget set"
}

test_a_checkout_that_is_not_a_work_tree_stops() {
  setup_case not-a-work-tree
  rm -rf "$dir/repo"
  mkdir -p "$dir/repo"
  run382_default
  expect_status "$status" 2 "a checkout that is not a work tree"
  expect_contains "$(fact_line checkout)" "FAIL" "a checkout that is not a work tree: the row"
  expect_contains "$(fact_line checkout)" "not a git work tree" "a checkout that is not a work tree: why"
  expect_no_call "a checkout that is not a work tree: nothing was raised" "budget set"
}

test_a_checkout_without_an_origin_stops() {
  setup_case no-origin
  git -C "$dir/repo" remote remove origin >/dev/null 2>&1
  run382_default
  expect_status "$status" 2 "a checkout without an origin"
  expect_contains "$(fact_line checkout)" "FAIL" "a checkout without an origin: the row"
  expect_contains "$(fact_line checkout)" "no 'origin' remote" "a checkout without an origin: why"
  expect_no_call "a checkout without an origin: nothing was raised" "budget set"
}

test_a_missing_tool_stops_with_one_line() {
  setup_case missing-tool
  write_tool_path "$dir/toolpath" bash mktemp git grep sed head tail cut python3
  RUN382_ENV=(PATH="$dir/toolpath")
  run382_default
  expect_status "$status" 2 "a missing tool"
  expect_contains "$err" "Check 382 needs 'fugaro' on PATH" "a missing tool: the message"
  expect_no_calls "a missing tool: nothing was called"
}

# ------------------------------------------------------------------ the dry run
test_help() {
  setup_case help
  run382 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check-382-agent-cannot-raise-a-cap.sh" "--help: the usage line"
  expect_no_calls "--help: nothing was called"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry-run
  run382 --checkout "$dir/repo" --project "$PROJECT" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_block_keys "a dry run" dry-run
  expect_contains "$(fact_line dry-run)" "PASS" "a dry run: the row"
  expect_contains "$(fact_line raise)" "planned" "a dry run: the raise is a plan"
  expect_contains "$(fact_line marker)" "CLAUDECODE=1" "a dry run: the marker is named"
  expect_contains "$(fact_line fence)" "a dry run measured nothing" "a dry run: the verdict says so"
  expect_contains "$(fact_line checkout)" "$REPO" "a dry run: it still names the repository"
  expect_contains "$err" "NOTHING below was done" "a dry run: the plan"
  expect_contains "$err" "CLAUDECODE=1 fugaro budget set --repo <that repository> --daily" "a dry run: the plan names the raise"
  expect_contains "$err" "no agent runs any step of it" "a dry run: whose check this is"
  expect_no_calls "a dry run: nothing was called"
  assert_scratch_removed "" "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-run-belong
  run382 --checkout "$dir/repo" --project belong --dry-run
  expect_status "$status" 2 "a dry run pointed at belong"
  expect_contains "$out" "FACT belong: FAIL" "a dry run pointed at belong: the row"
  expect_no_calls "a dry run pointed at belong: nothing was called"
}

test_usage_and_arguments() {
  setup_case usage
  run382 --deadline abc
  expect_status "$status" 2 "a non-numeric deadline"
  expect_contains "$err" "whole number of seconds" "a non-numeric deadline: the message"
  run382 --deadline 2
  expect_status "$status" 2 "too short a deadline"
  expect_contains "$err" "too short" "too short a deadline: the message"
  run382 --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--wat'" "an unknown argument: the message"
  run382 --checkout "$dir/nowhere" --project "$PROJECT"
  expect_status "$status" 2 "a checkout that is not there"
  expect_contains "$err" "is not a directory" "a checkout that is not there: the message"
  expect_no_calls "usage and arguments: nothing was called"
}

test_kept_scratch_is_reported() {
  setup_case kept-scratch
  mode set noparts
  RUN382_ENV=(CHECK382_KEEP_SCRATCH=1)
  run382_default
  expect_contains "$err" "CHECK382_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check named none"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  [ -f "$scratch/raise.err" ] || fail "a kept scratch directory: the raise's own output is not there"
  expect_contains "$(cat "$scratch/raise.err")" "that raise is refused here" "a kept scratch directory: the transcript is the command's own"
  rm -rf "$scratch" 2>/dev/null || true
}

# Sharding, so the package's Go test can run the tests of this file side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and CHECK_TEST_ONLY=name
# runs that one test alone. Neither set: all run.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_the_raise_is_refused_and_nothing_moves
t test_the_value_is_a_raise_over_both_caps
t test_a_node_with_only_a_per_run_is_raised_from_it
t test_a_node_with_no_cap_of_its_own_is_raised_from_nothing
t test_another_marker_is_read_the_same_way
t test_a_raise_that_is_not_refused_is_a_fail
t test_a_refusal_that_landed_anyway_is_read_back_and_put_back
t test_a_landed_raise_on_a_per_run_only_node_is_put_back_whole
t test_a_refusal_that_lost_a_part_is_a_fail
t test_a_refusal_about_another_act_is_a_fail
t test_a_refusal_that_prints_nothing_is_a_fail
t test_an_unexpected_exit_status_is_a_fail
t test_a_raise_that_never_ends_is_a_fail
t test_a_command_naming_another_repository_is_a_fail
t test_a_command_carrying_another_value_is_a_fail
t test_a_command_naming_another_project_is_a_fail
t test_a_command_without_the_project_is_a_fail
t test_a_command_with_an_invented_flag_is_a_fail
t test_a_command_that_is_not_budget_set_is_a_fail
t test_a_command_from_another_program_is_a_fail
t test_a_help_that_does_not_name_the_project_is_a_fail
t test_a_help_that_cannot_be_read_is_not_measured
t test_a_help_with_no_flags_is_not_measured
t test_a_budget_that_cannot_be_read_is_not_measured
t test_a_caps_document_without_a_row_is_not_measured
t test_a_node_that_cannot_be_read_is_not_measured
t test_a_node_the_check_cannot_match_stops
t test_a_read_back_that_fails_puts_the_caps_back_and_stops
t test_a_putting_back_that_fails_is_a_finding
t test_a_coding_agents_session_is_refused
t test_an_editors_own_marker_is_dropped_not_refused
t test_a_shell_that_arrived_tracing_is_refused
t test_belong_is_refused_in_what_you_typed
t test_belong_is_refused_in_what_doctor_answers
t test_the_belong_guard_is_not_vacuous
t test_a_project_that_is_not_doctors_stops
t test_doctor_refused_with_a_project_typed_carries_on
t test_doctor_refused_with_no_project_typed_stops
t test_a_checkout_that_is_not_a_work_tree_stops
t test_a_checkout_without_an_origin_stops
t test_a_missing_tool_stops_with_one_line
t test_usage_and_arguments
t test_kept_scratch_is_reported

if [ "$failures" -ne 0 ]; then
  echo "check-382-agent-cannot-raise-a-cap_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-382-agent-cannot-raise-a-cap_test.sh: all tests passed" >&2
