#!/usr/bin/env bash
# Tests for scripts/live-checks/check310.sh, against a fake `fugaro`, a fake
# `script` and a fake `tee`: nothing here reaches the network, there is no cloud
# project, no installation and no credential, and no live step of the check is
# run.
#
# The fakes are what make this check testable at all, because its whole subject
# is what a real terminal, a real pipe and a real log directory do. So the fake
# `fugaro` IS the reporter's rules, run for real: colour is decided on the
# output writer and NO_COLOR exactly as report.NoColor decides it, the line in
# flight is drawn with \r and taken down with \r\x1b[K, the closing line has
# summaryLine's shape, --json puts one document on stdout and the human lines on
# stderr, a failing run prints the step's line, the error's context, the log's
# path and the troubleshoot line under it, and the log is a 0600 file inside a
# 0700 directory holding the run's own lines, pruned by OpenLog's own rules (14
# days, the newest 50 per command name, matched by the name and nothing else).
# The fake `script` gives the child a REAL pty (python3's pty), in both the
# util-linux and the BSD spelling, so the check's terminal readings are readings
# of a terminal rather than of a stub that claims to be one.
#
# Every rule above has a knob (FAKE310_MODE) that breaks exactly one of them, so
# a case can say which reading of the check catches it, and the check's own
# forced failure is driven by the stub the check writes: the fake `fugaro` runs
# the `terraform` it finds on PATH, and fails the way a stage fails when that
# terraform refuses.
#
# Usage: bash scripts/live-checks/check310_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check310.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK310_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 2000)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|project|belong|output):' | sed 's/^/ ... last run: /')"
}
expect_pass_row() { # key what
  expect_contains "$out" "FACT $1: PASS" "$2"
}
expect_fail_row() { # key what
  expect_contains "$out" "FACT $1: FAIL" "$2"
}
expect_info_row() { # key what
  expect_contains "$out" "FACT $1: INFO" "$2"
}
# expect_block_keys: every key the block is defined to carry is in it, whatever
# the run measured. A block that lost a row cannot be diffed between two runs.
expect_block_keys() { # what
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key: " "$1: the block carries the $key row"
  done
  expect_contains "$out" "CHECK 310 RESULT END" "$1: the block ends with its own line"
}

# The project every case names: a Fugaro project name, and never the owner's.
PROJECT=sandbox-demo
# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(project belong session state-dir log-dir tty-colour tty-spinner
  tty-summary setup-plan no-color piped json spinner fail-step fail-context
  fail-log-path fail-log-mode fail-log-context log-dir-mode log-file-mode
  log-old-pruned log-keep log-other-command teardown output)
# The keys that have to hold in the run where everything is right. `spinner` is
# not one of them: it is the owner's answer, and a run whose stdin is not a
# terminal records it as NOT MEASURED by design (test_the_owner_answers_the_
# spinner_question is the case that answers it).
PASS_KEYS=(project belong session state-dir log-dir tty-colour tty-spinner
  tty-summary setup-plan no-color piped json fail-step fail-context
  fail-log-path fail-log-mode fail-log-context log-dir-mode log-file-mode
  log-old-pruned log-keep log-other-command output teardown)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# The fake fugaro for check310.sh's test: every call is recorded in the case's
# own directory -- with whether its stdout was a terminal and whether NO_COLOR
# was set, which is what the check's own readings are about -- and then answered
# by fake_fugaro.py, which is the reporter's rules run for real.
set -euo pipefail
# `[ -t 1 ]` has to be asked OUTSIDE the command substitution: a substitution
# runs in a subshell whose stdout is a pipe to its parent, so asking it inside
# one answers "not a terminal" however the command was really called -- which is
# exactly the reading this fake exists to take.
if [ -t 1 ]; then
  FAKE310_TTY=yes
else
  FAKE310_TTY=no
fi
printf 'fugaro %s | tty=%s no_color=%s\n' "$*" "$FAKE310_TTY" "${NO_COLOR:+yes}" \
  >>"$FAKE310_DIR/fugaro.calls"
exec python3 "$FAKE310_FAKES/fake_fugaro.py" "$@"
FUGARO_EOF

  cat >"$1/bin/script" <<'SCRIPT_EOF'
#!/usr/bin/env bash
# The fake `script` for check310.sh's test: a REAL pty (python3's pty), in both
# spellings, so the check's colour and spinner readings are readings of a
# terminal. FAKE310_BSD=1 makes it refuse util-linux's `-e`, which is what macOS
# does, so a case can read the other spelling.
set -euo pipefail
printf 'script %s\n' "$*" >>"$FAKE310_DIR/script.calls"
exec python3 "$FAKE310_FAKES/fake_script.py" "$@"
SCRIPT_EOF

  cat >"$1/bin/fake_script.py" <<'FAKESCRIPT_EOF'
#!/usr/bin/env python3
"""A `script` that gives its child a real pty.

Both spellings are accepted -- util-linux's `-q -e -c CMD FILE` and BSD's
`-q FILE CMD...` -- because the check probes for the one this machine has, and
the probe is half of what the test is about. The child's output goes to this
process's own stdout, which the check captures through tee.
"""
import os
import pty
import sys

args = sys.argv[1:]
if os.environ.get("FAKE310_BSD") == "1" and "-e" in args:
    sys.stderr.write("script: illegal option -- e\nusage: script [-adkpqr] [-F pipe] [file [command ...]]\n")
    sys.exit(1)

cmd = None
i = 0
while i < len(args):
    a = args[i]
    if a == "-c":
        cmd = args[i + 1]
        i += 2
        continue
    if a in ("-q", "-e", "-a", "-d", "-k", "-p", "-r", "-f", "-t"):
        i += 1
        continue
    if a == "/dev/null" or a == "-":
        i += 1
        continue
    if a.endswith("sh") and i + 2 < len(args) and args[i + 1] == "-c":
        cmd = args[i + 2]
        break
    i += 1
if cmd is None:
    sys.stderr.write("fake script: no command in %r\n" % (args,))
    sys.exit(2)

status = pty.spawn(["/bin/sh", "-c", cmd])
if os.WIFEXITED(status):
    sys.exit(os.WEXITSTATUS(status))
sys.exit(128 + os.WTERMSIG(status))
FAKESCRIPT_EOF

  cat >"$1/bin/fake_fugaro.py" <<'FAKEFUGARO_EOF'
#!/usr/bin/env python3
"""The fake `fugaro` for check310.sh's test: the reporter's rules, run for real.

Colour is decided on the output writer and NO_COLOR (report.NoColor), the line
in flight is drawn with \\r and taken down with \\r\\x1b[K, the closing line has
summaryLine's shape, --json puts one document on stdout and the human lines on
stderr, and a failing run prints the failed step's line, the error's context,
the log's path and the troubleshoot line under it. The log is a 0600 file inside
a 0700 directory holding the run's own lines, pruned by OpenLog's own rules.

FAKE310_MODE breaks one rule at a time, so a case can say which reading of the
check catches it.
"""
import datetime
import json
import os
import shutil
import subprocess
import sys

MODE = os.environ.get("FAKE310_MODE", "ok")
RETENTION = datetime.timedelta(days=14)
KEEP = 40 if MODE == "keep40" else 50
STATE = os.environ.get("XDG_STATE_HOME") or os.path.join(os.environ.get("HOME", ""), ".local", "state")
LOG_DIR = os.path.join(STATE, "fugaro", "logs")

DONE, FAILED = "✔", "✖"
PLAN_TODO, PLAN_SKIP, PLAN_WAIT = "○", "–", "↳"

argv = sys.argv[1:]
project, verb, as_json, plan_only = "", "", False, False
i = 0
while i < len(argv):
    a = argv[i]
    if a == "--project":
        project = argv[i + 1]
        i += 2
        continue
    if a == "--json":
        as_json = True
    elif a == "--plan-only":
        plan_only = True
    elif not a.startswith("-") and not verb:
        verb = a
    i += 1

isatty = sys.stdout.isatty()
nocolor = os.environ.get("NO_COLOR", "") != "" or os.environ.get("TERM") == "dumb"
colour = isatty and not nocolor and not as_json
if MODE in ("pipecolor", "nocolorignore"):
    colour = True
if MODE == "alwaysplain":
    colour = False
spin = colour and MODE != "nospin"

out = sys.stderr if as_json else sys.stdout


def paint(text, code):
    return "\x1b[%dm%s\x1b[0m" % (code, text) if colour else text


def step(mark, name, reason, code, body):
    if spin:
        out.write("\r" + paint("|", 36) + " " + name)
        out.flush()
        out.write("\r\x1b[K")
    line = paint(mark, code) + " " + name + ": " + reason
    out.write(line + "\n")
    body.append(mark + " " + name + ": " + reason + "\n")


def log_stamp(name, cmd):
    if not name.startswith(cmd + "-") or not name.endswith(".log"):
        return None
    rest = name[len(cmd) + 1:-4]
    stamp, _, pid = rest.partition("-")
    if len(stamp) != 14 or not stamp.isdigit() or not pid.isdigit() or not pid:
        return None
    try:
        return datetime.datetime.strptime(stamp, "%Y%m%d%H%M%S").replace(tzinfo=datetime.timezone.utc)
    except ValueError:
        return None


def prune(cmd, keep):
    if MODE == "noprune":
        return
    if MODE == "pruneall":
        for name in os.listdir(LOG_DIR):
            if name != keep and name.endswith(".log"):
                os.remove(os.path.join(LOG_DIR, name))
        return
    mine = []
    for name in os.listdir(LOG_DIR):
        at = log_stamp(name, cmd)
        if at is None:
            continue
        if not os.path.isfile(os.path.join(LOG_DIR, name)):
            continue
        mine.append((name, at))
    mine.sort(key=lambda t: t[0], reverse=True)
    mine.sort(key=lambda t: t[1], reverse=True)
    mine.sort(key=lambda t: t[0] != keep)
    now = datetime.datetime.now(datetime.timezone.utc)
    for idx, (name, at) in enumerate(mine):
        if idx < KEEP and now - at <= RETENTION:
            continue
        os.remove(os.path.join(LOG_DIR, name))


def log_name(cmd):
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")
    if MODE == "nostamp":
        return "update.log"
    return "%s-%s-%d.log" % (cmd, stamp, os.getpid())


def write_log(cmd, body):
    """The run's log: OpenLog's own file, mode and pruning.

    The name is this process's own (stamp and pid), the file is 0600 inside a
    0700 directory, it holds the lines the run printed, and the directory is
    pruned by the rules OpenLog prunes by -- 14 days, the newest 50 per command
    name, matched by the name and nothing else.
    """
    if MODE == "nolog":
        return None
    os.makedirs(LOG_DIR, mode=0o700, exist_ok=True)
    if MODE == "modes":
        os.chmod(LOG_DIR, 0o755)
    name = log_name(cmd)
    path = os.path.join(LOG_DIR, name)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("".join(body))
    os.chmod(path, 0o644 if MODE == "modes" else 0o600)
    prune(cmd, name)
    return path


def short(reason):
    if len(reason) <= 70:
        return reason
    for sep in (". ", " — ", "; ", ", ", " ("):
        idx = reason.find(sep)
        if 0 < idx < 70:
            return reason[:idx] + "…"
    return reason[:70] + "…"


def stub_terraform():
    """Check 310's own terraform stub, if it is the one on PATH.

    The check writes its stub into its scratch directory and puts it FIRST on
    PATH, so this fake has to be sure the terraform it reads is that one and not
    whatever terraform the machine really has: a real terraform's `init` fails in
    a directory with no configuration, and reading that as the forced failure
    would make every case depend on the machine's own terraform being absent.
    The stub is recognised by the words it writes, which are the check's own.
    """
    tf = shutil.which("terraform")
    if not tf:
        return None
    try:
        with open(tf, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        return None
    return tf if "check310" in text else None


def terraform_failure():
    """The failure the check forces, if its stub is on PATH and refuses.

    tf.New reads `version -json` first and every converge runs `init` after it,
    so a terraform that answers the first and refuses the second is a stage
    failing with the reporter open -- which is the block the check reads.
    """
    tf = stub_terraform()
    if not tf:
        return None
    subprocess.run([tf, "version", "-json"], capture_output=True)
    init = subprocess.run([tf, "init"], capture_output=True, text=True)
    if init.returncode == 0:
        return None
    lines = [ln for ln in init.stderr.strip("\n").split("\n") if ln.strip()]
    if not lines:
        lines = ["terraform init exited %d with no output" % init.returncode]
    return "terraform init exited %d: %s" % (init.returncode, lines[0]), lines[1:]


def run():
    body = []
    if MODE == "noblock" and stub_terraform():
        # A refusal at the door: no step line, no log, nothing to read. It comes
        # before the forced failure, because a refusal never reaches a stage.
        out.write("fugaro: no project config in this checkout, and none was named\n")
        sys.exit(1)

    failure = terraform_failure()
    if failure is not None and verb != "setup":
        reason, context = failure
        cmd = verb or "update"
        path = None if MODE == "nolog" else os.path.join(LOG_DIR, log_name(cmd))
        stem = os.path.basename(path)[:-4] if path else ""
        if MODE == "wrongid":
            # The id on the screen routes to a log that is not the one the path
            # above names: `fugaro troubleshoot <id>` would open nothing.
            stem = "update-19990101000000-1"
        block = [FAILED + " terraform: " + reason + "\n"]
        for line in context:
            block.append("    " + line + "\n")
        if path is not None and MODE != "nologpath":
            block.append("    full output: " + path + "\n")
            block.append("    Something went wrong. Run: fugaro troubleshoot " + stem + "\n")
        block.append("Failed in 0.2s, 2 steps\n")
        if MODE == "logthin":
            # The log holds the closing line and nothing else: the failure was
            # on the terminal, where nobody can read it a day later.
            block = ["Failed in 0.2s, 2 steps\n"]
        write_log(cmd, block)

        out.write(paint(FAILED, 31) + " terraform: " + short(reason) + "\n")
        for line in context:
            out.write("    " + line + "\n")
        if path is not None and MODE != "nologpath":
            out.write("    full output: " + path + "\n")
            out.write("    Something went wrong. Run: fugaro troubleshoot " + stem + "\n")
        out.write("Failed in 0.2s, 2 steps\n")
        sys.exit(2)

    if verb == "setup":
        rows = [("terraform", "will do", PLAN_TODO), ("images", "done", PLAN_SKIP), ("budget", "waiting", PLAN_WAIT)]
        plain = MODE == "planplain"
        if plain:
            # The plan view drawn the way a run that decided it had no terminal
            # would draw it: no colour and none of the plan's own marks.
            rows = [("terraform", "will do", "*"), ("images", "done", "*"), ("budget", "waiting", "*")]
        for name, why, mark in rows:
            out.write((mark if plain else paint(mark, 36)) + " " + name + ": " + why + "\n")
            body.append(mark + " " + name + ": " + why + "\n")
        write_log("setup", body)
        sys.exit(0)

    if as_json:
        if MODE == "jsonplain":
            # The human account went to stdout, so stdout is not one document.
            out.write(DONE + " terraform: done\n")
            sys.exit(0)
        doc = {
            "project": "another-project" if MODE == "jsonother" else project,
            "gcp_project": "sandbox-demo-gcp",
            "region": "us-central1",
            "plan_only": plan_only,
            "stages": [{"name": "terraform", "state": "done"}, {"name": "images", "state": "done"}],
            "left_for_you": [],
        }
        body.append(DONE + " terraform: done\n")
        body.append("Done in 0.3s, 2 steps\n")
        write_log("update", body)
        json.dump(doc, sys.stdout, indent=2)
        sys.stdout.write("\n")
        sys.stderr.write(paint(DONE, 32) + " terraform: done\n")
        sys.exit(0)

    step(DONE, "local config", "done", 32, body)
    step(DONE, "terraform", "done", 32, body)
    step(DONE, "images", "done", 32, body)
    if MODE != "nosummary":
        body.append("Done in 0.3s, 3 steps\n")
    write_log("update", body)
    if MODE != "nosummary":
        out.write("Done in 0.3s, 3 steps\n")
    sys.exit(0)


run()
FAKEFUGARO_EOF

  chmod 755 "$1/bin/fugaro" "$1/bin/script" "$1/bin/fake_script.py" "$1/bin/fake_fugaro.py"
}

# ------------------------------------------------------------------ the case
setup_case() { # setup_case NAME
  CASE=$work/$1
  rm -rf "$CASE"
  mkdir -p "$CASE/bin" "$CASE/log" "$CASE/home" "$CASE/tmp" "$CASE/state/fugaro/logs"
  chmod 700 "$CASE/tmp" "$CASE/state" "$CASE/state/fugaro" "$CASE/state/fugaro/logs"
  write_fakes "$CASE"
  : >"$CASE/log/fugaro.calls"
  : >"$CASE/log/script.calls"
  out=""
  err=""
  status=0
  MODE=ok
  BSD=0
}

# run310 [args...]: the check with the case's own environment. The agent markers
# are taken OUT of the environment on purpose: the check reads them, and a
# developer running this test inside a coding agent's session would otherwise
# see the session row change under them (test_an_agent_session_is_noted_not_
# stopped is the case that sets one).
run310() {
  status=0
  out=""
  err=""
  : >"$CASE/log/fugaro.calls"
  : >"$CASE/log/script.calls"
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    PATH="$CASE/bin:$PATH" HOME="$CASE/home" XDG_STATE_HOME="$CASE/state" TMPDIR="$CASE/tmp" \
    FAKE310_DIR="$CASE/log" FAKE310_FAKES="$CASE/bin" FAKE310_MODE="$MODE" \
    ${BSD:+FAKE310_BSD=$BSD} \
    CHECK310_FUGARO="$CASE/bin/fugaro" CHECK310_SCRIPT="$CASE/bin/script" \
    bash "$script" ${1+"$@"} 2>"$CASE/log/err" </dev/null) || status=$?
  err=$(cat "$CASE/log/err" 2>/dev/null || true)
  unset BSD
}

# run310_pty ANSWER [args...]: the same, but with a REAL terminal on the check's
# own input, so the one question it asks is asked and ANSWER is what you typed.
# The pty is the fake `script`'s own, and the check's inner `script` calls take
# their input from /dev/null, so nothing else can eat the answer.
run310_pty() { # run310_pty ANSWER [args...]
  local answer=$1
  shift
  status=0
  out=""
  err=""
  : >"$CASE/log/fugaro.calls"
  : >"$CASE/log/script.calls"
  out=$(printf '%s\n' "$answer" | env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT \
    -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    PATH="$CASE/bin:$PATH" HOME="$CASE/home" XDG_STATE_HOME="$CASE/state" TMPDIR="$CASE/tmp" \
    FAKE310_DIR="$CASE/log" FAKE310_FAKES="$CASE/bin" FAKE310_MODE="$MODE" \
    CHECK310_FUGARO="$CASE/bin/fugaro" CHECK310_SCRIPT="$CASE/bin/script" \
    "$CASE/bin/script" -q -e -c "bash $script $*" /dev/null 2>"$CASE/log/err") || status=$?
  err=$(cat "$CASE/log/err" 2>/dev/null || true)
}

logs_dir() { printf '%s' "$CASE/state/fugaro/logs"; }

# ------------------------------------------------------------------- the tests
test_the_pinned_set_passes() {
  setup_case pinned
  run310 --project "$PROJECT"
  expect_status "$status" 0 "the whole check, with everything right"
  expect_block_keys "the pinned set"
  local key
  for key in "${PASS_KEYS[@]}"; do
    expect_pass_row "$key" "the pinned set: $key"
  done
  expect_info_row spinner "the pinned set: the question was not asked (no terminal on the input) and says so"
  expect_contains "$out" "CHECK 310 RESULT END (exit 0)" "the pinned set: the block's own exit line"
  # The readings are of the fake's real behaviour, not of a claim: the terminal
  # run is the one whose stdout was a pty.
  expect_contains "$(cat "$CASE/log/fugaro.calls")" "tty=yes" "the pinned set: a run whose stdout was a real terminal"
  expect_contains "$(cat "$CASE/log/fugaro.calls")" "tty=no" "the pinned set: a run whose stdout was a pipe"
  expect_contains "$(cat "$CASE/log/script.calls")" "-e -c" "the pinned set: the util-linux spelling of script"
  # 51 runs and the newest 50 kept: the rule, read on a directory the case owns.
  local n
  n=$(ls -1 "$(logs_dir)" | grep -Ec '^update-[0-9]{14}-[0-9]+\.log$' || true)
  [ "$n" = 50 ] || fail "the pinned set: the fake kept $n update logs, and the rule is the newest 50"
}

test_the_owner_answers_the_spinner_question() {
  setup_case spinner_no
  run310_pty n --project "$PROJECT" --phase look
  expect_status "$status" 1 "a run whose owner says the line in flight does not look right"
  expect_fail_row spinner "the owner's own answer is the reading"
  expect_contains "$out" "you watched the line in flight and said it does NOT look right" "the row says whose reading it is"
  expect_contains "$out" "QUESTION" "the question was asked on the terminal"

  setup_case spinner_yes
  run310_pty y --project "$PROJECT" --phase look
  expect_status "$status" 0 "a run whose owner says the line in flight looked right"
  expect_pass_row spinner "the owner's own answer is the reading"
}

test_a_dry_run_runs_nothing() {
  setup_case dry
  run310 --project "$PROJECT" --dry-run
  expect_status "$status" 0 "the dry run"
  expect_contains "$out" "FACT dry-run: PASS" "the dry run"
  expect_contains "$err" "NOTHING below was done" "the dry run prints the plan on stderr, where the progress goes"
  [ ! -s "$CASE/log/fugaro.calls" ] || fail "the dry run called fugaro: $(cat "$CASE/log/fugaro.calls")"
  [ ! -s "$CASE/log/script.calls" ] || fail "the dry run ran script: $(cat "$CASE/log/script.calls")"
  [ -z "$(ls -1 "$(logs_dir)")" ] || fail "the dry run planted a log in the log directory"
  expect_absent "$out" "FACT tty-colour: PASS" "the dry run concludes nothing about colour"
}

test_belong_is_refused() {
  setup_case belong
  run310 --project belong-sandbox
  expect_status "$status" 2 "the owner's own installation"
  expect_fail_row belong "a name containing belong"
  expect_absent "$(cat "$CASE/log/fugaro.calls")" "fugaro" "belong is refused before a single command is run"
}

test_a_project_that_is_not_a_name_is_refused() {
  setup_case badname
  run310 --project Bad_Name
  expect_status "$status" 2 "a name that is not a Fugaro project name"
  expect_fail_row project "the name's own row"
  [ ! -s "$CASE/log/fugaro.calls" ] || fail "a refused name still ran fugaro"
}

test_no_project_is_refused() {
  setup_case noproject
  run310
  expect_status "$status" 2 "no project was named"
  expect_fail_row project "the project row says the check never lets the CLI pick one"
  expect_contains "$out" "fall back to 'the only project config'" "the row says why the name is required"
}

test_an_agent_session_is_noted_not_stopped() {
  setup_case agent
  status=0
  out=$(env CLAUDECODE=1 PATH="$CASE/bin:$PATH" HOME="$CASE/home" XDG_STATE_HOME="$CASE/state" \
    TMPDIR="$CASE/tmp" FAKE310_DIR="$CASE/log" FAKE310_FAKES="$CASE/bin" FAKE310_MODE=ok \
    CHECK310_FUGARO="$CASE/bin/fugaro" CHECK310_SCRIPT="$CASE/bin/script" \
    bash "$script" --project "$PROJECT" --phase failure </dev/null 2>"$CASE/log/err") || status=$?
  expect_status "$status" 0 "an agent's session is a note, not a stop: the readings still hold there"
  expect_info_row session "the session row names the marker"
  expect_contains "$out" "CLAUDECODE is set" "the row names the marker that was read"
}

test_a_pipe_that_is_painted_fails() {
  setup_case pipecolor
  MODE=pipecolor
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a run that paints a pipe"
  expect_fail_row piped "the pipe's own row"
  expect_fail_row no-color "NO_COLOR is ignored by the same mode"
  expect_pass_row tty-colour "colour at a terminal is still right in that mode"
}

test_no_color_at_a_terminal_that_still_paints_fails() {
  setup_case nocolor
  MODE=nocolorignore
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "NO_COLOR at a terminal"
  expect_fail_row no-color "the rule is read at a terminal, where there is something to turn off"
  expect_contains "$out" "NO_COLOR=1 at a terminal still painted the run" "the row says which reading it is"
}

test_a_terminal_without_colour_fails() {
  setup_case plain
  MODE=alwaysplain
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a terminal with no colour"
  expect_fail_row tty-colour "colour at a terminal is the row"
  expect_pass_row no-color "nothing to turn off is a pass, not a failure"
  expect_pass_row piped "a pipe is plain either way"
}

test_a_run_without_a_line_in_flight_fails() {
  setup_case nospin
  MODE=nospin
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a run that never draws the line in flight"
  expect_fail_row tty-spinner "the spinner's own row"
  expect_pass_row tty-colour "colour is still on"
}

test_a_run_without_a_closing_line_fails() {
  setup_case nosummary
  MODE=nosummary
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a run that prints no closing line"
  expect_fail_row tty-summary "the closing line's own row"
}

test_a_plan_view_that_is_not_painted_fails() {
  setup_case planplain
  MODE=planplain
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a plan view with no colour and no marks"
  expect_fail_row setup-plan "the plan view's own row"
  expect_pass_row tty-colour "the update run beside it is still right"
}

test_a_json_that_is_not_one_document_fails() {
  setup_case jsonplain
  MODE=jsonplain
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a --json run that prints its human lines to stdout"
  expect_fail_row json "the document's own row"
}

test_a_json_naming_another_project_fails() {
  setup_case jsonother
  MODE=jsonother
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a --json run naming another project"
  expect_fail_row json "the document has to be this run's own"
  expect_contains "$out" "names project 'another-project'" "the row says what it read"
}

test_the_forced_failure_is_read_from_the_stub() {
  setup_case forced
  run310 --project "$PROJECT" --phase failure
  expect_status "$status" 0 "the forced failure, read"
  expect_pass_row fail-step "the failed step's line"
  expect_pass_row fail-context "the error's context"
  expect_pass_row fail-log-path "the log's path and the troubleshoot id"
  expect_pass_row fail-log-mode "0600 in a 0700 directory"
  expect_pass_row fail-log-context "the log holds the run's own lines"
  # The failure is the one THIS check forces: the context line is its own stub's.
  expect_contains "$out" "check310's own context line" "the context is the stub's own words"
  expect_contains "$out" "$CASE/state/fugaro/logs/update-" "the path the failure named is the log the run filed"
}

test_a_failure_with_no_log_path_fails() {
  setup_case nologpath
  MODE=nologpath
  run310 --project "$PROJECT" --phase failure
  expect_status "$status" 1 "a failure that prints no log path"
  expect_fail_row fail-log-path "the path's own row"
  expect_fail_row fail-log-mode "there is no file to read a mode off"
  expect_fail_row fail-log-context "there is no file to read"
  expect_pass_row fail-step "the step line is still read"
}

test_a_troubleshoot_id_naming_another_log_fails() {
  setup_case wrongid
  MODE=wrongid
  run310 --project "$PROJECT" --phase failure
  expect_status "$status" 1 "an id that routes to another run's log"
  expect_fail_row fail-log-path "the id has to be the log's own stem"
  expect_contains "$out" "is not that file's stem" "the row says which reading it is"
}

test_a_log_that_does_not_hold_the_run_fails() {
  setup_case logthin
  MODE=logthin
  run310 --project "$PROJECT" --phase failure
  expect_status "$status" 1 "a log that holds only the closing line"
  expect_fail_row fail-log-context "the log is the run's full output (D1352)"
}

test_a_failure_with_no_block_stops() {
  setup_case noblock
  MODE=noblock
  run310 --project "$PROJECT" --phase failure
  expect_status "$status" 2 "a refusal at the door is not a reading of the output"
  expect_contains "$out" "the forced failure produced no failure block at all" "the stop says why"
}

test_a_log_file_that_is_not_0600_fails() {
  setup_case modes
  MODE=modes
  run310 --project "$PROJECT" --phase all
  expect_status "$status" 1 "a log directory of 0755 and a log of 0644"
  expect_fail_row fail-log-mode "the failure's own log"
  expect_fail_row log-file-mode "the newest log after the runs"
  expect_fail_row log-dir-mode "the directory's own mode"
}

test_an_aged_log_that_is_not_pruned_fails() {
  setup_case noprune
  MODE=noprune
  run310 --project "$PROJECT" --phase logs
  expect_status "$status" 1 "a converge that prunes nothing"
  expect_fail_row log-old-pruned "the fourteen days are the row"
  expect_fail_row log-keep "and so is the newest 50"
  expect_pass_row log-other-command "nothing was removed, so the other command's log is untouched"
}

test_another_commands_log_removed_fails() {
  setup_case pruneall
  MODE=pruneall
  run310 --project "$PROJECT" --phase logs
  expect_status "$status" 1 "a converge that removes every *.log it finds"
  expect_fail_row log-other-command "the pruning is per command name"
}

test_fewer_than_fifty_kept_fails() {
  setup_case keep40
  MODE=keep40
  run310 --project "$PROJECT" --phase logs
  expect_status "$status" 1 "a converge that keeps only the newest 40"
  expect_fail_row log-keep "the count's own row"
  expect_pass_row log-old-pruned "the fourteen days still hold"
}

test_fewer_runs_than_the_rule_needs_is_not_measured() {
  setup_case fewruns
  run310 --project "$PROJECT" --phase logs --runs 3
  expect_status "$status" 0 "a run that asked for fewer than the rule needs"
  expect_info_row log-keep "the count is not measured, and says so rather than guessing"
  expect_pass_row log-old-pruned "the fourteen days are still read"
}

test_a_converge_that_files_no_log_fails_the_log_dir_row() {
  setup_case nolog
  MODE=nolog
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a converge that files no log"
  expect_fail_row log-dir "the log directory has no update log after five converges"
}

test_a_converge_that_files_no_log_stops_the_retention_phase() {
  setup_case nolog_retention
  MODE=nolog
  run310 --project "$PROJECT" --phase logs
  expect_status "$status" 2 "there is no retention to read"
  expect_contains "$out" "filed no log" "the stop says why"
}

test_a_log_name_outside_the_rule_fails() {
  setup_case nostamp
  MODE=nostamp
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 1 "a log whose name is not the rule's"
  expect_fail_row log-dir "the name is what the pruning and the count read"
}

test_the_bsd_spelling_of_script_is_detected() {
  setup_case bsd
  BSD=1
  run310 --project "$PROJECT" --phase look
  expect_status "$status" 0 "macOS's script, which has no -e -c"
  expect_pass_row tty-colour "the terminal readings still hold"
  expect_contains "$(cat "$CASE/log/script.calls")" "/bin/sh -c" "the BSD spelling was the one used"
}

test_a_script_that_is_neither_spelling_stops() {
  setup_case noscript
  status=0
  out=$(env PATH="$CASE/bin:$PATH" HOME="$CASE/home" XDG_STATE_HOME="$CASE/state" TMPDIR="$CASE/tmp" \
    FAKE310_DIR="$CASE/log" FAKE310_FAKES="$CASE/bin" FAKE310_MODE=ok \
    CHECK310_FUGARO="$CASE/bin/fugaro" CHECK310_SCRIPT=/bin/false \
    bash "$script" --project "$PROJECT" --phase look </dev/null 2>"$CASE/log/err") || status=$?
  expect_status "$status" 2 "a machine with no usable script"
  expect_contains "$out" "the terminal readings need a 'script'" "the stop names the tool"
}

test_one_phase_leaves_the_others_not_measured() {
  setup_case onephase
  run310 --project "$PROJECT" --phase failure
  expect_status "$status" 0 "one phase alone"
  expect_pass_row fail-step "the phase that was selected"
  expect_info_row tty-colour "a row of another phase is not measured, not failed"
  expect_info_row log-keep "and neither is the retention"
  expect_contains "$out" "belongs to another phase" "the row says which phase it belongs to"
}

test_the_usage_is_printed_and_is_not_a_run() {
  setup_case usage
  run310 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check310.sh" "the usage is printed"
  expect_absent "$out" "CHECK 310 RESULT" "--help is not a run and prints no block"
}

test_an_unknown_flag_is_refused() {
  setup_case unknown
  run310 --project "$PROJECT" --nope
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$out" "unknown argument '--nope'" "the refusal names it"
}

test_a_phase_that_is_not_one_is_refused() {
  setup_case badphase
  run310 --project "$PROJECT" --phase later
  expect_status "$status" 2 "a phase that is not one"
  expect_contains "$out" "--phase takes all, look, failure or logs" "the refusal says what it takes"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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

t test_the_pinned_set_passes
t test_the_owner_answers_the_spinner_question
t test_a_dry_run_runs_nothing
t test_belong_is_refused
t test_a_project_that_is_not_a_name_is_refused
t test_no_project_is_refused
t test_an_agent_session_is_noted_not_stopped
t test_a_pipe_that_is_painted_fails
t test_no_color_at_a_terminal_that_still_paints_fails
t test_a_terminal_without_colour_fails
t test_a_run_without_a_line_in_flight_fails
t test_a_run_without_a_closing_line_fails
t test_a_plan_view_that_is_not_painted_fails
t test_a_json_that_is_not_one_document_fails
t test_a_json_naming_another_project_fails
t test_the_forced_failure_is_read_from_the_stub
t test_a_failure_with_no_log_path_fails
t test_a_troubleshoot_id_naming_another_log_fails
t test_a_log_that_does_not_hold_the_run_fails
t test_a_failure_with_no_block_stops
t test_a_log_file_that_is_not_0600_fails
t test_an_aged_log_that_is_not_pruned_fails
t test_another_commands_log_removed_fails
t test_fewer_than_fifty_kept_fails
t test_fewer_runs_than_the_rule_needs_is_not_measured
t test_a_converge_that_files_no_log_fails_the_log_dir_row
t test_a_converge_that_files_no_log_stops_the_retention_phase
t test_a_log_name_outside_the_rule_fails
t test_the_bsd_spelling_of_script_is_detected
t test_a_script_that_is_neither_spelling_stops
t test_one_phase_leaves_the_others_not_measured
t test_the_usage_is_printed_and_is_not_a_run
t test_an_unknown_flag_is_refused
t test_a_phase_that_is_not_one_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check310_test: $failures failure(s)" >&2
  exit 1
fi
echo "check310_test: all tests passed" >&2
