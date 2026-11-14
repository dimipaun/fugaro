#!/usr/bin/env bash
# Tests for scripts/live-checks/check-381-over-cap-build.sh, against a fake
# `fugaro` on PATH: nothing here reaches the network, there is no cloud project,
# no image is built and no agent runs any live step of the check.
#
# The fake holds the four things the refusal is made of and nothing else: which
# repository's caps node the project carries (and whether it sets a per-run cap of
# its own), the effective caps `budget show --json` reports, a `budget set` that
# REALLY applies what it is handed -- so the check's read-back of its own lowering
# and of its own putting back are readings of state that changed -- and an
# `image build` that plays the sentences the check has to tell apart: the refusal
# in each of the two shapes it can take, a refusal about another cap, a refusal
# that lost a part, one that prints nothing, a build that was NOT refused, one
# that ends on an unexpected status, and one that never ends.
#
# The checkout is real -- a git work tree with an origin remote -- because the
# repository the check lowers the caps of is the one that remote names, and the
# test has to be able to bend it (no origin, not a work tree).
#
# Usage: bash scripts/live-checks/check-381-over-cap-build_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-381-over-cap-build.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK381_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (build|refusal|overcap|write|teardown|aborted):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured. A dry run's block
# carries dry-run as well; expect_block_keys takes it as an extra.
BLOCK_KEYS=(xtrace agent-session project belong checkout mode caps write build refusal overcap teardown)

# The installation every case runs against: its project is not `belong`.
PROJECT=sbx
GCP=sbx-proj
REPO=edgeapp/sandbox

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-381-over-cap-build.sh's test only. Every
# answer comes from a file in the case's own directory (FAKE381_DIR), every call
# is logged with its whole argv, and `budget set` REALLY applies what it is given,
# so the check's own read-backs are readings of state that changed rather than of
# words the fake was told to say.
set -euo pipefail
dir=${FAKE381_DIR:?FAKE381_DIR is not set}
knob() { if [ -f "$dir/$1" ]; then cat "$dir/$1"; else printf '%s' "$2"; fi; }
put() { printf '%s' "$1" >"$dir/$2"; }
log() { printf 'fugaro %s\n' "$*" >>"$dir/calls"; }

# The refusal, in the words BuildOverCapError.Error prints: the estimate and where
# its price came from, the predicate's own detail for the cap that was met, the
# figure that cap compared, and the one command that raises it. `to` is the stream
# it goes to, because one case has to say the check reads BOTH.
refuse() { # to detail cmd
  local sentence
  sentence=$(printf 'Error: this image build of e2-standard-4 in us-central1 reserves $0.03 at most (the 3600 s build timeout at the machine%ss rate, read from the Cloud Billing catalog), and %s: raise the cap with `%s`, or build images on a smaller machine type (build.machine_type in fugaro.yaml) so each build reserves less' "'" "$2" "$3")
  case "$1" in
  1) printf '%s\n' "$sentence" ;;
  *) printf '%s\n' "$sentence" >&2 ;;
  esac
  exit 1
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
  name=*) printf '{"ok":true,"project":{"name":"%s","gcp_project":"sbx-proj"}}\n' "${m#name=}" ;;
  *) printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s"}}\n' "$FAKE381_PROJECT" "$FAKE381_GCP" ;;
  esac
  ;;
project)
  log "$*"
  case "${2:-}" in
  show)
    m=$(knob project.mode ok)
    case "$m" in
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
      "$FAKE381_REPO" "$dj" "$pj"
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
    m=$(knob budget.mode ok)
    case "$m" in
    fail)
      printf 'Error: the budget could not be read\n' >&2
      exit 1
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
      "$FAKE381_REPO" "$effd" "$dsrc" "$effp" "$psrc" "$(knob counted 0)")
    cat <<JSON
{"project":"$FAKE381_PROJECT","gcp_project":"$FAKE381_GCP","mode":"$(knob budgetmode.mode enforce)","day":"2026-11-14","max_reserve_usd":$(knob max_reserve 20),"global":{"daily_cap_usd":$(knob global_daily 50),"per_run_cap_usd":$(knob global_perrun 25),"counted_usd":0},"defaults":{"repo_daily_cap_usd":$(knob default_daily 10),"repo_per_run_cap_usd":$(knob default_perrun 3)},"repos":[$row],"running":[],"warnings":[]}
JSON
    ;;
  set)
    n=$(cat "$dir/setcount" 2>/dev/null || printf 0)
    n=$((n + 1))
    printf '%s' "$n" >"$dir/setcount"
    m=$(knob set.mode ok)
    case "$m" in
    fail)
      printf 'Error: the cap could not be written\n' >&2
      exit 1
      ;;
    fail-second)
      if [ "$n" -ge 2 ]; then
        printf 'Error: the cap could not be written\n' >&2
        exit 1
      fi
      ;;
    clearfail)
      case " $* " in
      *" --clear "*)
        printf 'Error: the cap could not be cleared\n' >&2
        exit 1
        ;;
      esac
      ;;
    esac
    if [ "$m" != nofollow ]; then
      shift 2
      while [ $# -gt 0 ]; do
        case "$1" in
        --clear)
          put no node.exists
          put "" node.daily
          put "" node.perrun
          shift
          ;;
        --daily)
          put "$2" node.daily
          put yes node.exists
          shift 2
          ;;
        --per-run)
          put "$2" node.perrun
          put yes node.exists
          shift 2
          ;;
        *) shift ;;
        esac
      done
    fi
    printf 'the caps of %s were updated\n' "$FAKE381_REPO"
    ;;
  *)
    printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
    exit 64
    ;;
  esac
  ;;
image)
  log "$*"
  case "${2:-}" in
  build)
    case "$(knob build.mode refuse-daily)" in
    refuse-daily)
      refuse 2 "this repository's daily cap \$0.00 reached (\$0.00 counted, \$0.25 needed), with \$0.00 counted today by this repository's jobs and its settled builds" "fugaro budget set --repo $FAKE381_REPO --daily <usd>"
      ;;
    refuse-run)
      refuse 2 "run cap \$0.00 reached (\$0.00 held, \$0.25 needed)" "fugaro budget set --repo $FAKE381_REPO --per-run <usd>"
      ;;
    refuse-stdout)
      refuse 1 "this repository's daily cap \$0.00 reached (\$0.00 counted, \$0.25 needed), with \$0.00 counted today by this repository's jobs and its settled builds" "fugaro budget set --repo $FAKE381_REPO --daily <usd>"
      ;;
    refuse-global)
      refuse 2 "the project's daily cap \$0.00 reached (\$0.00 counted, \$0.25 needed)" "fugaro budget set --global --daily <usd>"
      ;;
    noparts)
      printf 'Error: this image build of e2-standard-4 in us-central1 is not submitted\n' >&2
      exit 1
      ;;
    silent) exit 1 ;;
    allow)
      printf 'Submitted build %s to Cloud Build (this command does not wait for it)\n' "b-1234"
      ;;
    odd)
      printf 'Error: the image could not be built\n' >&2
      exit 3
      ;;
    hang) exec sleep 300 ;;
    esac
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
  RUN381_ARGS=()
  RUN381_ENV=()
  RUN381_BASH=(bash)
}

run381() {
  status=0
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  out=$(env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE381_DIR="$dir/state" FAKE381_PROJECT="$PROJECT" FAKE381_GCP="$GCP" FAKE381_REPO="$REPO" \
    ${RUN381_ENV[@]+"${RUN381_ENV[@]}"} \
    "${RUN381_BASH[@]}" "$script" ${RUN381_ARGS[@]+"${RUN381_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN381_ARGS=()
  RUN381_ENV=()
  RUN381_BASH=(bash)
}
run381_default() { run381 --checkout "$dir/repo" --project "$PROJECT" "$@"; }

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what extra...
  local key what=$1
  shift
  expect_contains "$out" "CHECK 381 RESULT" "$what: the block"
  local n
  for key in "${BLOCK_KEYS[@]}" "$@"; do
    [ -n "$key" ] || continue
    expect_contains "$out" "FACT $key:" "$what: the $key row"
    # Exactly one: a key recorded twice must be resolved to the last reading, not
    # printed twice.
    n=$(printf '%s\n' "$out" | grep -c "^FACT $key:")
    [ "$n" = 1 ] || fail "$what: the $key row is printed $n times"
  done
  expect_contains "$out" "CHECK 381 RESULT END (exit " "$what: the block's end"
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
expect_call_count() { # what n
  local n
  n=$(wc -l <"$dir/state/calls" | tr -d ' ')
  [ "$n" = "$2" ] || fail "$1: expected $2 calls, got $n: $(tr '\n' ';' <"$dir/state/calls")"
}
expect_state() { # what file value
  local got
  got=$(cat "$dir/state/$2" 2>/dev/null || true)
  [ "$got" = "$3" ] || fail "$1: expected $2 to be '$3', got '$got'"
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
# The whole point, end to end: the repository's own daily cap is lowered to $0.00,
# the build is refused before it is submitted with all four parts, and the cap is
# put back -- read back, not trusted.
test_the_daily_shape_holds_and_the_caps_go_back() {
  setup_case daily-shape
  mode build refuse-daily
  run381_default
  expect_status "$status" 0 "the daily shape"
  expect_block_keys "the daily shape"
  expect_contains "$(fact_line xtrace)" "PASS" "the daily shape: not tracing"
  expect_contains "$(fact_line agent-session)" "PASS" "the daily shape: the owner's own session"
  expect_contains "$(fact_line project)" "PASS" "the daily shape: doctor's installation"
  expect_contains "$(fact_line project)" "sbx" "the daily shape: which project"
  expect_contains "$(fact_line belong)" "PASS" "the daily shape: not belong"
  expect_contains "$(fact_line checkout)" "PASS" "the daily shape: the checkout"
  expect_contains "$(fact_line checkout)" "$REPO" "the daily shape: the repository the origin names"
  expect_contains "$(fact_line mode)" "PASS" "the daily shape: enforce"
  expect_contains "$(fact_line caps)" "PASS" "the daily shape: the caps node was read"
  expect_contains "$(fact_line write)" "now says this repository's daily cap is \$0.00" "the daily shape: the write, read back"
  expect_contains "$(fact_line build)" "PASS" "the daily shape: the build row"
  expect_contains "$(fact_line build)" "exit 1" "the daily shape: the refusal's own code"
  expect_contains "$(fact_line refusal)" "PASS" "the daily shape: the sentence"
  expect_contains "$(fact_line refusal)" "\$0.25 needed" "the daily shape: the part the sentence carried"
  expect_contains "$(fact_line overcap)" "HOLDS" "the daily shape: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "the daily shape: the putting back"
  expect_contains "$(fact_line teardown)" "daily \$5" "the daily shape: what went back"
  expect_call "the daily shape: the lowering was one budget set" "budget set --repo $REPO --daily 0"
  expect_no_call "the daily shape: no per-run was written" "--per-run"
  expect_call "the daily shape: the build ran with its stdin closed from the checkout" "image build --no-wait"
  expect_call "the daily shape: the putting back" "budget set --repo $REPO --daily 5"
  expect_state "the daily shape: the cap is back" node.daily 5
  assert_scratch_removed "" "the daily shape"
}

# A node that keeps a per-run cap of its own is lowered on both: a per-run cap
# above its own daily is a state the CLI refuses to leave, and a build holds
# nothing of its run, so that is the cap any reservation meets first. The refusal
# then takes the run's shape and names the per-run flag.
test_the_per_run_shape_holds() {
  setup_case per-run-shape
  mode build refuse-run
  state_file node.perrun 2
  run381_default
  expect_status "$status" 0 "the per-run shape"
  expect_contains "$(fact_line caps)" "PASS" "the per-run shape: the caps node was read"
  expect_contains "$(fact_line caps)" "per-run \$2" "the per-run shape: the node's own per-run"
  expect_contains "$(fact_line write)" "PASS" "the per-run shape: the lowering"
  expect_contains "$(fact_line refusal)" "PASS" "the per-run shape: the sentence"
  expect_contains "$(fact_line refusal)" "per-run cap" "the per-run shape: the cap the build met"
  expect_contains "$(fact_line overcap)" "HOLDS" "the per-run shape: the verdict"
  expect_contains "$(fact_line teardown)" "daily \$5 and per-run \$2" "the per-run shape: what went back"
  expect_call "the per-run shape: both caps were lowered in one call" "budget set --repo $REPO --daily 0 --per-run 0"
  expect_call "the per-run shape: the putting back" "budget set --repo $REPO --daily 5 --per-run 2"
  expect_state "the per-run shape: the per-run is back" node.perrun 2
  expect_state "the per-run shape: the daily is back" node.daily 5
}

# No node of the repository's own: the check lends it one to make the over-cap
# state (a tightening, which asks nothing) and removes it again, because a node
# that held no cap of its own must not be left holding one.
test_a_repository_with_no_node_of_its_own_gets_it_removed_again() {
  setup_case no-node
  mode build refuse-daily
  state_file node.exists no
  run381_default
  expect_status "$status" 0 "no node of its own"
  expect_contains "$(fact_line caps)" "PASS" "no node of its own: the caps were read"
  expect_contains "$(fact_line caps)" "no cap at all" "no node of its own: what the node held"
  expect_contains "$(fact_line overcap)" "HOLDS" "no node of its own: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "no node of its own: the putting back"
  expect_call "no node of its own: the lowering" "budget set --repo $REPO --daily 0"
  expect_call "no node of its own: the node was cleared again" "budget set --repo $REPO --clear"
  expect_state "no node of its own: the node is gone" node.exists no
}

# Where a terminal wraps that sentence is not something a check may depend on, and
# neither is which of the two streams it lands on: the refusal is read from the
# collapsed text of both.
test_the_refusal_is_read_from_either_stream() {
  setup_case refusal-on-stdout
  mode build refuse-stdout
  run381_default
  expect_status "$status" 0 "a refusal on stdout"
  expect_contains "$(fact_line refusal)" "PASS" "a refusal on stdout: the sentence was found"
  expect_contains "$(fact_line overcap)" "HOLDS" "a refusal on stdout: the verdict"
}

# The four parts are the whole point: a refusal that lost one is a FAIL naming the
# part, because that sentence is what replaced the confirmation in front of a
# billable build.
test_a_refusal_that_lost_a_part_is_a_fail() {
  setup_case lost-a-part
  mode build noparts
  run381_default
  expect_status "$status" 1 "a refusal that lost a part"
  expect_contains "$(fact_line build)" "PASS" "a refusal that lost a part: it was refused"
  expect_contains "$(fact_line refusal)" "FAIL" "a refusal that lost a part: the sentence row"
  expect_contains "$(fact_line refusal)" "missing: " "a refusal that lost a part: the row names what is missing"
  expect_contains "$(fact_line refusal)" "reserves \$" "a refusal that lost a part: the estimate is one of them"
  expect_contains "$(fact_line refusal)" "needed)" "a refusal that lost a part: the figure compared is one of them"
  expect_contains "$(fact_line overcap)" "does NOT hold" "a refusal that lost a part: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a refusal that lost a part: the caps still went back"
}

# An exit 1 with nothing printed at all is the same finding, and the row has to
# say the command printed nothing rather than quote an empty line.
test_a_refusal_that_prints_nothing_is_a_fail() {
  setup_case silent-refusal
  mode build silent
  run381_default
  expect_status "$status" 1 "a silent refusal"
  expect_contains "$(fact_line refusal)" "FAIL" "a silent refusal: the sentence row"
  expect_contains "$(fact_line refusal)" "<nothing>" "a silent refusal: it says the command printed nothing"
}

# A refusal about ANOTHER cap is not this check's finding: the cap this check set
# is not the one the sentence names, and a check that bent the sentence into shape
# would report a pass for a build that met the project's cap.
test_a_refusal_about_another_cap_is_a_fail() {
  setup_case another-cap
  mode build refuse-global
  run381_default
  expect_status "$status" 1 "a refusal about another cap"
  expect_contains "$(fact_line refusal)" "FAIL" "a refusal about another cap: the sentence row"
  expect_contains "$(fact_line refusal)" "the cap this check set" "a refusal about another cap: the row says which cap"
  expect_contains "$(fact_line overcap)" "does NOT hold" "a refusal about another cap: the verdict"
}

# A build that ended 0 is money: the check had made it impossible and it was
# submitted anyway, and the row has to say so in those words.
test_a_build_that_is_not_refused_is_a_fail() {
  setup_case not-refused
  mode build allow
  run381_default
  expect_status "$status" 1 "a build that is not refused"
  expect_contains "$(fact_line build)" "FAIL" "a build that is not refused: the build row"
  expect_contains "$(fact_line build)" "NOT refused" "a build that is not refused: what happened"
  expect_contains "$(fact_line build)" "exit 0" "a build that is not refused: the code"
  expect_contains "$(fact_line build)" "Submitted build" "a build that is not refused: the command's own line"
  expect_contains "$(fact_line overcap)" "does NOT hold" "a build that is not refused: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a build that is not refused: the caps went back"
}

test_an_unexpected_exit_status_is_a_fail() {
  setup_case odd-status
  mode build odd
  run381_default
  expect_status "$status" 1 "an unexpected status"
  expect_contains "$(fact_line build)" "FAIL" "an unexpected status: the build row"
  expect_contains "$(fact_line build)" "exit 3" "an unexpected status: the code"
  expect_contains "$(fact_line refusal)" "FAIL" "an unexpected status: there is no refusal to read"
  expect_contains "$(fact_line overcap)" "does NOT hold" "an unexpected status: the verdict"
}

# A command that never ends is its own finding -- nothing in this command waits on
# a terminal any more -- and this check has to end it rather than hang with it.
test_a_build_that_never_ends_is_a_fail() {
  setup_case never-ends
  mode build hang
  RUN381_ARGS=(--deadline 5)
  run381_default
  expect_status "$status" 1 "a build that never ends"
  expect_contains "$(fact_line build)" "FAIL" "a build that never ends: the build row"
  expect_contains "$(fact_line build)" "still running after 5s" "a build that never ends: the deadline"
  expect_contains "$(fact_line overcap)" "NOT MEASURED" "a build that never ends: the verdict"
  expect_contains "$(fact_line teardown)" "PASS" "a build that never ends: the caps went back"
  expect_state "a build that never ends: the cap is back" node.daily 5
}

# A cap only REFUSES a build in enforce (D1548): in observe the same decision
# comes back as an advisory and the build is submitted anyway. A run that measured
# this would be a run that spent money, so it stops unmeasured and names the one
# command -- and it does not set the mode itself, because that write is the
# owner's own choice and `fugaro update` is what raises it elsewhere.
test_a_mode_that_is_not_enforce_is_not_measured() {
  setup_case observe
  mode budgetmode observe
  run381_default
  expect_status "$status" 2 "observe mode"
  expect_contains "$(fact_line mode)" "FAIL" "observe mode: the mode row"
  expect_contains "$(fact_line mode)" "observe" "observe mode: which mode"
  expect_contains "$(fact_line mode)" "budget set --global --mode enforce" "observe mode: the one command"
  expect_contains "$(fact_line overcap)" "NOT MEASURED" "observe mode: the verdict"
  expect_contains "$(fact_line overcap)" "the mode row" "observe mode: the verdict names the row that stopped it"
  expect_no_call "observe mode: the cap was not lowered" "budget set"
  expect_no_call "observe mode: no build was run" "image build"
  expect_state "observe mode: the node is untouched" node.daily 5
}

# An unreadable node is a refusal to run: the check lowers a cap and has to be
# able to put back exactly what was there.
test_a_node_that_cannot_be_read_is_not_measured() {
  setup_case node-unreadable
  mode project fail
  run381_default
  expect_status "$status" 2 "an unreadable node"
  expect_contains "$(fact_line caps)" "FAIL" "an unreadable node: the caps row"
  expect_contains "$(fact_line caps)" "could not be read" "an unreadable node: why"
  expect_contains "$(fact_line overcap)" "NOT MEASURED" "an unreadable node: the verdict"
  expect_no_call "an unreadable node: nothing was written" "budget set"
  expect_no_call "an unreadable node: no build was run" "image build"
}

# The other way a node can be unreadable: the effective caps say the repository's
# own node supplies one, while the raw nodes list no node under that name. Clearing
# a node that exists is a change to the project, not a putting back, so this stops.
test_a_node_the_check_cannot_match_stops() {
  setup_case node-unmatched
  state_file node.exists no
  state_file budget.daily_source repo
  run381_default
  expect_status "$status" 2 "a node the check cannot match"
  expect_contains "$(fact_line caps)" "FAIL" "a node the check cannot match: the caps row"
  expect_contains "$(fact_line caps)" "will not write a cap it cannot put back" "a node the check cannot match: why"
  expect_no_call "a node the check cannot match: nothing was written" "budget set"
  expect_no_call "a node the check cannot match: no build was run" "image build"
}

# A write that did not land would leave the next step submitting a real build, so
# the lowering is read back before anything runs.
test_a_lowering_that_does_not_land_is_not_measured() {
  setup_case lowering-lost
  mode set nofollow
  run381_default
  expect_status "$status" 2 "a lowering that did not land"
  expect_contains "$(fact_line write)" "FAIL" "a lowering that did not land: the write row"
  expect_contains "$(fact_line write)" "did not read back" "a lowering that did not land: why"
  expect_contains "$(fact_line overcap)" "NOT MEASURED" "a lowering that did not land: the verdict"
  expect_no_call "a lowering that did not land: no build was run" "image build"
}

test_a_lowering_that_fails_stops() {
  setup_case lowering-failed
  mode set fail
  run381_default
  expect_status "$status" 2 "a lowering that failed"
  expect_contains "$(fact_line write)" "FAIL" "a lowering that failed: the write row"
  expect_contains "$(fact_line write)" "exited 1" "a lowering that failed: the code"
  expect_contains "$(fact_line overcap)" "NOT MEASURED" "a lowering that failed: the verdict"
  expect_contains "$(fact_line teardown)" "FAIL" "a lowering that failed: the putting back was attempted anyway"
  expect_contains "$(fact_line teardown)" "had already failed on its own" "a lowering that failed: it does not claim the project holds a cap nothing wrote"
  expect_no_call "a lowering that failed: no build was run" "image build"
}

# The putting back is the one thing an owner has to be able to trust, and a
# failure there is a finding of its own -- with the command to run by hand.
test_a_putting_back_that_fails_is_a_finding() {
  setup_case restore-failed
  mode build refuse-daily
  mode set fail-second
  run381_default
  expect_status "$status" 1 "a putting back that failed"
  expect_contains "$(fact_line overcap)" "HOLDS" "a putting back that failed: the refusal itself still holds"
  expect_contains "$(fact_line teardown)" "FAIL" "a putting back that failed: the teardown row"
  expect_contains "$(fact_line teardown)" "were NOT put back" "a putting back that failed: what happened"
  expect_contains "$(fact_line teardown)" "Run this yourself" "a putting back that failed: the command"
}

# A reading the check could not take must never stand in for an answer about the
# release, so this one exits 2 with the verdict saying so.
test_a_caps_document_without_a_row_is_not_measured() {
  setup_case no-row
  mode budget norow
  run381_default
  expect_status "$status" 2 "a caps document with no row"
  expect_contains "$(fact_line caps)" "FAIL" "a caps document with no row: the caps row"
  expect_contains "$(fact_line overcap)" "NOT MEASURED" "a caps document with no row: the verdict"
  expect_no_call "a caps document with no row: nothing was written" "budget set"
}

test_a_coding_agents_session_is_refused() {
  setup_case agent-session
  RUN381_ENV=(CLAUDECODE=1)
  run381_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "FAIL" "a coding agent's session: the row"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE is set" "a coding agent's session: which marker"
  expect_contains "$(fact_line agent-session)" "No agent runs any step of it" "a coding agent's session: whose check this is"
  expect_no_calls "a coding agent's session: not even doctor was asked"
}

test_a_shell_that_arrived_tracing_is_refused() {
  setup_case tracing
  RUN381_BASH=(bash -x)
  run381_default
  expect_status "$status" 2 "a shell that arrived tracing"
  expect_contains "$(fact_line xtrace)" "FAIL" "a shell that arrived tracing: the row"
  expect_contains "$(fact_line xtrace)" "tracing" "a shell that arrived tracing: why"
  expect_no_calls "a shell that arrived tracing: nothing was called"
}

test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run381 --checkout "$dir/repo" --project belong
  expect_status "$status" 2 "belong in what you typed"
  expect_contains "$(fact_line belong)" "FAIL" "belong in what you typed: the row"
  expect_contains "$(fact_line belong)" "which this check must never touch" "belong in what you typed: why"
  expect_no_calls "belong in what you typed: nothing was called"
}

test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-doctor
  mode doctor belong
  run381 --checkout "$dir/repo"
  expect_status "$status" 2 "belong in what doctor answers"
  expect_contains "$(fact_line belong)" "FAIL" "belong in what doctor answers: the row"
  expect_contains "$(fact_line belong)" "'belong' (the config in force)" "belong in what doctor answers: where it was found"
  expect_call "belong in what doctor answers: doctor was asked" "fugaro doctor --json"
  expect_no_call "belong in what doctor answers: nothing was written" "budget set"
}

# The non-vacuity of that guard, as a reading rather than an argument: with the
# substring test rewritten to match nothing, the same case runs the whole check
# against the owner's own installation -- so the refusal above IS the guard's.
test_the_belong_guard_is_not_vacuous() {
  setup_case belong-not-vacuous
  local broken=$dir/check-381-broken.sh
  rewrite_script "$broken" '  *belong*) return 0 ;;' '  *belong*) return 1 ;;'
  mode build refuse-daily
  script=$broken
  run381_default
  script=$here/check-381-over-cap-build.sh
  expect_status "$status" 0 "the belong guard is not vacuous"
  expect_contains "$(fact_line belong)" "PASS" "the belong guard is not vacuous: the rewritten guard let it through"
  expect_call "the belong guard is not vacuous: the build ran" "image build"
}

test_a_project_that_is_not_doctors_stops() {
  setup_case other-project
  run381 --checkout "$dir/repo" --project other
  expect_status "$status" 2 "a project that is not doctor's"
  expect_contains "$err" "two different installations" "a project that is not doctor's: the message"
  expect_no_call "a project that is not doctor's: nothing was written" "budget set"
}

test_doctor_refused_with_a_project_typed_carries_on() {
  setup_case doctor-refused
  mode doctor fail
  mode build refuse-daily
  run381_default
  expect_status "$status" 0 "doctor refused with a project typed"
  expect_contains "$(fact_line project)" "INFO" "doctor refused with a project typed: the row is an INFO"
  expect_contains "$(fact_line project)" "you typed" "doctor refused with a project typed: why"
  expect_contains "$(fact_line overcap)" "HOLDS" "doctor refused with a project typed: the verdict"
}

test_doctor_refused_with_no_project_typed_stops() {
  setup_case doctor-refused-no-project
  mode doctor fail
  run381 --checkout "$dir/repo"
  expect_status "$status" 2 "doctor refused with no project typed"
  expect_contains "$(fact_line project)" "FAIL" "doctor refused with no project typed: the row"
  expect_contains "$(fact_line project)" "cannot tell which installation" "doctor refused with no project typed: why"
  expect_no_call "doctor refused with no project typed: nothing was called" "budget set"
}

test_a_checkout_that_is_not_a_work_tree_stops() {
  setup_case not-a-work-tree
  rm -rf "$dir/repo"
  mkdir -p "$dir/repo"
  run381_default
  expect_status "$status" 2 "a checkout that is not a work tree"
  expect_contains "$(fact_line checkout)" "FAIL" "a checkout that is not a work tree: the row"
  expect_contains "$(fact_line checkout)" "not a git work tree" "a checkout that is not a work tree: why"
  expect_no_call "a checkout that is not a work tree: nothing was written" "budget set"
}

test_a_checkout_without_an_origin_stops() {
  setup_case no-origin
  git -C "$dir/repo" remote remove origin >/dev/null 2>&1
  run381_default
  expect_status "$status" 2 "a checkout without an origin"
  expect_contains "$(fact_line checkout)" "FAIL" "a checkout without an origin: the row"
  expect_contains "$(fact_line checkout)" "no 'origin' remote" "a checkout without an origin: why"
  expect_no_call "a checkout without an origin: nothing was written" "budget set"
}

test_a_missing_tool_stops_with_one_line() {
  setup_case missing-tool
  write_tool_path "$dir/toolpath" bash mktemp git grep sed head tail cut python3
  RUN381_ENV=(PATH="$dir/toolpath")
  run381_default
  expect_status "$status" 2 "a missing tool"
  expect_contains "$err" "Check 381 needs 'fugaro' on PATH" "a missing tool: the message"
  expect_no_calls "a missing tool: nothing was called"
}

# ------------------------------------------------------------------ the dry run
test_help() {
  setup_case help
  run381 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check-381-over-cap-build.sh" "--help: the usage line"
  expect_contains "$out" "CHECK 381 RESULT" "--help: still one block on stdout"
  expect_no_calls "--help: nothing was called"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry-run
  run381 --checkout "$dir/repo" --project "$PROJECT" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_block_keys "a dry run" dry-run
  expect_contains "$(fact_line dry-run)" "PASS" "a dry run: the row"
  expect_contains "$(fact_line build)" "planned" "a dry run: the build is a plan"
  expect_contains "$(fact_line build)" "image build --no-wait" "a dry run: the command is named"
  expect_contains "$(fact_line write)" "planned" "a dry run: the write is a plan"
  expect_contains "$(fact_line overcap)" "a dry run measured nothing" "a dry run: the verdict says so"
  expect_contains "$(fact_line checkout)" "$REPO" "a dry run: it still names the repository"
  expect_contains "$err" "NOTHING below was done" "a dry run: the plan"
  expect_contains "$err" "budget set --repo <that repository> --daily 0" "a dry run: the plan names the write"
  expect_contains "$err" "no agent runs any step of it" "a dry run: whose check this is"
  expect_no_calls "a dry run: nothing was called"
  assert_scratch_removed "" "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-run-belong
  run381 --checkout "$dir/repo" --project belong --dry-run
  expect_status "$status" 2 "a dry run pointed at belong"
  expect_contains "$out" "FACT belong: FAIL" "a dry run pointed at belong: the row"
  expect_no_calls "a dry run pointed at belong: nothing was called"
}

test_usage_and_arguments() {
  setup_case usage
  run381 --deadline abc
  expect_status "$status" 2 "a non-numeric deadline"
  expect_contains "$err" "whole number of seconds" "a non-numeric deadline: the message"
  run381 --deadline 2
  expect_status "$status" 2 "too short a deadline"
  expect_contains "$err" "too short" "too short a deadline: the message"
  run381 --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--wat'" "an unknown argument: the message"
  run381 --checkout "$dir/nowhere" --project "$PROJECT"
  expect_status "$status" 2 "a checkout that is not there"
  expect_contains "$err" "is not a directory" "a checkout that is not there: the message"
  expect_no_calls "usage and arguments: nothing was called"
}

test_kept_scratch_is_reported() {
  setup_case kept-scratch
  mode build noparts
  RUN381_ENV=(CHECK381_KEEP_SCRATCH=1)
  run381_default
  expect_contains "$err" "CHECK381_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check named none"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  [ -f "$scratch/build.err" ] || fail "a kept scratch directory: the build's own output is not there"
  expect_contains "$(cat "$scratch/build.err")" "is not submitted" "a kept scratch directory: the transcript is the command's own"
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
t test_the_daily_shape_holds_and_the_caps_go_back
t test_the_per_run_shape_holds
t test_a_repository_with_no_node_of_its_own_gets_it_removed_again
t test_the_refusal_is_read_from_either_stream
t test_a_refusal_that_lost_a_part_is_a_fail
t test_a_refusal_that_prints_nothing_is_a_fail
t test_a_refusal_about_another_cap_is_a_fail
t test_a_build_that_is_not_refused_is_a_fail
t test_an_unexpected_exit_status_is_a_fail
t test_a_build_that_never_ends_is_a_fail
t test_a_mode_that_is_not_enforce_is_not_measured
t test_a_node_that_cannot_be_read_is_not_measured
t test_a_node_the_check_cannot_match_stops
t test_a_lowering_that_does_not_land_is_not_measured
t test_a_lowering_that_fails_stops
t test_a_putting_back_that_fails_is_a_finding
t test_a_caps_document_without_a_row_is_not_measured
t test_a_coding_agents_session_is_refused
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
  echo "check-381-over-cap-build_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-381-over-cap-build_test.sh: all tests passed" >&2
