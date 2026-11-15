#!/usr/bin/env bash
# Tests for scripts/live-checks/check310.sh, against a fake `fugaro` on PATH:
# nothing here reaches the network, no installation is converged, no Terraform
# runs and no step of the live check is performed for real.
#
# The fake is a small but faithful `fugaro`: it draws the account the way
# internal/report draws it (the line in flight, the step line, the closing
# line), honours the colour rule the CLI honours (a terminal, NO_COLOR,
# TERM=dumb, --json), sends the document to stdout and the account to stderr
# under --json, files a log with the sink's own name shape and mode, and prunes
# the directory the way pruneLogs prunes it. Every call it answers is recorded,
# so a case can say exactly which commands the check ran, in which order, and
# that a run it refuses makes none at all.
#
# The forced failure is not faked twice: the fake `fugaro` runs the real
# `terraform` on its PATH — the shim check310.sh generates — and builds the
# failure's message and context out of that shim's own stderr, so the case
# exercises the shim end to end.
#
# Each case then bends one thing (the spinner, the colour, the closing line,
# the document, the context, the log's mode, the retention rule, doctor's log,
# the seeds) and asserts the block names THAT fact and no other, which is how
# this test says the check's rows are not vacuous.
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 800)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
# expect_fact FILE KEY VERDICT: the block's own row, in the block's own shape.
expect_fact() {
  case "$1" in
  *"FACT $2: $3 "*) ;;
  *) fail "$4: the block has no 'FACT $2: $3' row; the block was:
$(printf '%s\n' "$1" | sed -n '/^CHECK 310 RESULT/,/^CHECK 310 RESULT END/p')" ;;
  esac
}
expect_no_fail() { # block what
  case "$1" in
  *"FACT "*": FAIL "*)
    fail "$2: the block carries a FAILed row:
$(printf '%s\n' "$1" | grep 'FACT .*: FAIL' | head -20)"
    ;;
  esac
}
expect_calls() { # file want
  local got
  got=$(cat "$1" 2>/dev/null || true)
  [ "$got" = "$2" ] || fail "the fugaro calls were:
$got
wanted:
$2"
}

INSTALLATION=check310demo

# --------------------------------------------------------------------- the fake
write_fake() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env python3
"""A fake `fugaro` for check310.sh's test.

It answers the four commands the check runs, in the shapes the real one prints
them (internal/report, internal/initflow), and files a log the way
internal/report/logsink does — name shape, mode, and the retention rule. Every
call is appended to $CHECK310_TEST_CALLS. CHECK310_TEST_MODE bends one thing at
a time, which is what the cases assert against; MODE=scratch is a --scratch run
of the check, where the project it answers with is the check's own made-up one
and every call records the plumbing it was handed (scratch_notes).
"""
import calendar
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request

MODE = os.environ.get("CHECK310_TEST_MODE", "happy")
CALLS = os.environ["CHECK310_TEST_CALLS"]
INSTALLATION = os.environ.get("CHECK310_TEST_INSTALLATION", "check310demo")
# MODE=scratch is a --scratch run of the check: it drives its own installation,
# so the project this fake answers with is the check's made-up one.
SCRATCH_PROJECT = "chk310-scratch"
PROJECT = SCRATCH_PROJECT if MODE == "scratch" else INSTALLATION
GCP = SCRATCH_PROJECT if MODE == "scratch" else "check310-gcp"
REGION = "europe-west1"
STAGES = ["installation", "bootstrap", "images", "converge", "gateway"]
REASONS = {
    "installation": "the installation root is already at this release",
    "bootstrap": "the bucket and the service account are in place",
    "images": "the job image is the one this release builds",
    "converge": "the project converged",
    "gateway": "the gateway is serving this project",
}
LOG_NAME = re.compile(r"^([a-z][a-z0-9-]{0,40})-(\d{14})-(\d+)\.log$")
RETENTION = 14 * 24 * 3600
KEEP_PER_COMMAND = 50

with open(CALLS, "a") as fh:
    line = " ".join(sys.argv[1:])
    # The project the check selected for its one read, so a case can pin that
    # the installation it names is the one doctor is asked about.
    if sys.argv[1:2] == ["doctor"] and os.environ.get("FUGARO_PROJECT"):
        line += " [FUGARO_PROJECT=%s]" % os.environ["FUGARO_PROJECT"]
    fh.write(line + "\n")


def terraform_kind():
    """The terraform first on PATH, as this call sees it: the check's own fake,
    or the shim it puts there for the one forced failure."""
    tf = shutil.which("terraform")
    if not tf:
        return "none", "none"
    try:
        r = subprocess.run([tf, "version", "-json"], capture_output=True, text=True, timeout=30)
    except Exception:  # noqa: BLE001
        return "unrunnable", "none"
    if "check310" in r.stdout:
        # The shim: its plan IS the forced failure, and running it here would
        # write the marker the check reads its 'the shim ran' off.
        return "shim", "shim"
    if r.returncode != 0 or "terraform_version" not in r.stdout:
        return "other", "none"
    fd, out = tempfile.mkstemp(prefix="chk310-plan-")
    os.close(fd)
    try:
        p = subprocess.run([tf, "plan", "-no-color", "-out=" + out], capture_output=True, text=True, timeout=60)
        return "fake", str(p.returncode)
    except Exception:  # noqa: BLE001
        return "fake", "unrunnable"
    finally:
        try:
            os.remove(out)
        except OSError:
            pass


def scratch_notes():
    """What a --scratch run's own installation looks like, read from inside the
    call: the project the CLI was pointed at, the config it was handed, the
    directory it runs in, and the terraform first on PATH.

    The endpoints are checked by asking one of them: the check's own fake API
    root is the only thing that can answer a project read, and a config naming
    the real Google roots would leave every URL in it non-loopback. A missing
    endpoint is worse than a wrong one — an empty field IS the real endpoint —
    so the case pins the count against the URL fields of localcfg.Endpoints.

    The file is $CHECK310_TEST_SCRATCH, one line per call, and only the cases
    that set it read it.
    """
    path = os.environ.get("CHECK310_TEST_SCRATCH", "")
    if not path:
        return
    cfg = os.environ.get("FUGARO_CONFIG", "")
    try:
        with open(cfg) as fh:
            text = fh.read()
    except OSError:
        text = ""
    urls = re.findall(r"^    [a-z_]+: (http\S+)$", text, re.M)
    loopback = [u for u in urls if u.startswith("http://127.0.0.1:")]
    answer = "no"
    if loopback:
        try:
            with urllib.request.urlopen(loopback[0].rstrip("/") + "/v1/projects/" + SCRATCH_PROJECT, timeout=30) as r:
                answer = "yes" if r.status == 200 else str(r.status)
        except Exception:  # noqa: BLE001
            answer = "no"
    cwd_yaml = "no"
    try:
        with open("fugaro.yaml") as fh:
            cwd_yaml = "yes" if SCRATCH_PROJECT in fh.read() else "elsewhere"
    except OSError:
        cwd_yaml = "no"
    tf, plan = terraform_kind()
    with open(path, "a") as fh:
        fh.write("project=%s config=%s named=%s endpoints=%d loopback=%d no_auth=%s cwd_yaml=%s api=%s terraform=%s plan=%s\n" % (
            os.environ.get("FUGARO_PROJECT", "unset"),
            "yes" if text else "no",
            "yes" if ("name: " + SCRATCH_PROJECT) in text else "no",
            len(urls), len(loopback),
            "yes" if "no_auth: true" in text else "no",
            cwd_yaml, answer, tf, plan))


# `fugaro version` is the check's first call and the one that carries no
# installation: it asks which binary is on PATH, before anything is selected, so
# there is no plumbing to read on it.
if MODE == "scratch" and sys.argv[1:2] != ["version"]:
    scratch_notes()


def state_dir():
    x = os.environ.get("XDG_STATE_HOME", "")
    if x.startswith("/"):
        return x
    return os.path.join(os.environ.get("HOME", "/"), ".local", "state")


def log_dir():
    return os.path.join(state_dir(), "fugaro", "logs")


def prune(d, cmd, keep):
    """pruneLogs: the newest 50 of this command's logs, inside 14 days."""
    if MODE == "retention-keep":
        return
    try:
        names = sorted(os.listdir(d))
    except OSError:
        return
    if MODE == "retention-eat-guard":
        # A pruner that reaches everything, links and directories included.
        for n in names:
            p = os.path.join(d, n)
            try:
                if os.path.isdir(p) and not os.path.islink(p):
                    os.rmdir(p)
                else:
                    os.remove(p)
            except OSError:
                pass
        return
    cands = []
    for n in names:
        p = os.path.join(d, n)
        if not os.path.isfile(p) or os.path.islink(p):
            continue
        m = LOG_NAME.match(n)
        if not m or m.group(1) != cmd:
            continue
        cands.append((n, m.group(2)))
    keep_first = [c for c in cands if c[0] == keep]
    # Time, then name largest first where two logs share a stamp: the fake stands
    # in for the CLI's own pruneLogs, whose tie-break decides which of the two
    # goes when the count boundary falls between them.
    rest = sorted([c for c in cands if c[0] != keep], key=lambda c: c[0], reverse=True)
    rest.sort(key=lambda c: c[1], reverse=True)
    ordered = keep_first + rest
    now = time.time()
    for i, (n, stamp) in enumerate(ordered):
        at = calendar.timegm(time.strptime(stamp, "%Y%m%d%H%M%S"))
        if i < KEEP_PER_COMMAND and (now - at) <= RETENTION:
            continue
        try:
            os.remove(os.path.join(d, n))
        except OSError:
            pass
    if MODE in ("retention-eat-owner", "restore-eat-owner"):
        for n, _ in ordered:
            if n == keep:
                continue
            try:
                os.remove(os.path.join(d, n))
            except OSError:
                pass
            break


def open_log(cmd):
    d = log_dir()
    os.makedirs(d, exist_ok=True)
    try:
        os.chmod(d, 0o700)
    except OSError:
        pass
    name = "%s-%s-%d.log" % (cmd, time.strftime("%Y%m%d%H%M%S", time.gmtime()), os.getpid())
    path = os.path.join(d, name)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    if MODE == "log-mode-0644":
        os.chmod(path, 0o644)
    prune(d, cmd, name)
    return name, path, os.fdopen(fd, "w")


def shim_terraform():
    """The terraform the check put first on PATH, and only that one.

    A machine with a real terraform must not turn every case into a failure, so
    the shim is recognised by the answer it gives to the one question the CLI
    asks before it converges: its version.
    """
    tf = shutil.which("terraform")
    if not tf:
        return None
    try:
        r = subprocess.run([tf, "version", "-json"], capture_output=True, text=True, timeout=30)
    except Exception:  # noqa: BLE001
        return None
    if r.returncode == 0 and "check310" in r.stdout:
        return tf
    return None


def colour_on(stream, json_mode):
    if json_mode:
        return False
    if MODE == "ansi-on-pipe":
        # A run that draws its colour wherever its output goes: the pipe gets
        # ANSI, which is what G4 forbids. NO_COLOR still turns it off, so the
        # case bends one rule and not two.
        return not os.environ.get("NO_COLOR")
    if MODE == "nocolor-ignored":
        return stream.isatty()
    if os.environ.get("NO_COLOR"):
        return False
    if os.environ.get("TERM", "") == "dumb":
        return False
    return stream.isatty()


def paint(text, code, colour):
    if not colour:
        return text
    return "\x1b[%sm%s\x1b[0m" % (code, text)


def status_cell(glyph, word, colour):
    return paint("%s %s" % (glyph, word), "32", colour)


def version():
    sys.stdout.write("fugaro 0.9.0-check310\n")
    return 0


def doctor():
    if MODE == "doctor-logs":
        _, _, log = open_log("doctor")
        log.write("the doctor read\n")
        log.close()
    name, gcp = PROJECT, GCP
    if MODE == "doctor-other-project":
        # A checkout whose fugaro.yaml names another project: the read is the
        # one the check asked for (FUGARO_PROJECT) and answers with another name.
        name = "check310other"
    if MODE == "gcp-refused":
        # A named installation whose GCP project is one the register refuses.
        gcp = "edge-devel-dimi"
    sys.stdout.write(json.dumps({
        "project": {"name": name, "gcp_project": gcp, "region": REGION},
    }) + "\n")
    return 0


def plan_only():
    name, path, log = open_log("setup")
    if MODE != "plan-no-rows":
        sys.stdout.write("fugaro setup: project %s (GCP %s, %s)\n" % (PROJECT, GCP, REGION))
        colour = colour_on(sys.stdout, False)
        for s in STAGES:
            row = "  " + status_cell("✔", "done", colour) + " " + "%-16s" % s + " " + REASONS[s]
            if MODE != "plan-log-empty":
                log.write("  " + status_cell("✔", "done", False) + " " + "%-16s" % s + " " + REASONS[s] + "\n")
            sys.stdout.write(row + "\n")
    log.close()
    return 0


def update(argv):
    json_mode = "--json" in argv
    human = sys.stderr if json_mode else sys.stdout
    colour = colour_on(sys.stdout, json_mode)
    spin = colour and MODE != "no-spinner"
    _, path, log = open_log("update")

    # The forced failure is the CHECK's own: it puts its fake terraform first on
    # PATH for this one run, and the CLI's converge then fails inside a stage.
    # So the fake `fugaro` fails exactly when that shim is the terraform on its
    # PATH, and MODE only bends how the failure is presented.
    msg, context, shim_out = "", [], ""
    failing = shim_terraform() is not None
    if failing:
        tf = shim_terraform()
        r = subprocess.run([tf, "plan", "-no-color"], capture_output=True, text=True)
        lines = r.stderr.strip().splitlines()
        msg = "terraform plan exited %d: %s" % (r.returncode, lines[0] if lines else "")
        context = lines[1:]
        shim_out = r.stdout

    for s in STAGES:
        state = "done"
        if failing and s == "converge":
            state = "failed"
        elif MODE == "left" and s == "gateway":
            state = "left"
        if spin:
            for frame in ("|", "/"):
                human.write("\r" + paint(frame, "36", True) + " " + s)
                human.flush()
            human.write("\r\x1b[K")
        glyph, code = {"done": ("✔", "32"), "failed": ("✖", "31"),
                       "left": ("○", "33")}[state]
        sep = " " if state == "done" else ": "
        reason = msg if state == "failed" else REASONS[s]
        # fail-bare: the failed step line keeps no error text on the screen
        # either, so nothing a person sees names what failed or where its log
        # is; the log itself still holds the text, as a run's own log does.
        human_reason = "" if MODE == "fail-bare" and state == "failed" else reason
        human.write(paint(glyph, code, colour) + " " + s + sep + human_reason + "\n")
        log.write(paint(glyph, code, MODE == "log-ansi") + " " + s + sep + reason + "\n")
        if state == "failed" and MODE not in ("fail-no-context", "fail-bare"):
            for line in context:
                human.write("    " + line + "\n")
                log.write("    " + line + "\n")

    if failing:
        if MODE != "fail-screen":
            log.write(shim_out)
        if MODE in ("fail-screen", "fail-both"):
            human.write(shim_out)
        if MODE not in ("fail-no-full-output", "fail-bare"):
            human.write("    full output: " + path + "\n")
            log.write("    full output: " + path + "\n")
        if MODE != "fail-no-diagnose":
            ident = os.path.basename(path)[:-len(".log")]
            if MODE == "fail-wrong-id":
                ident = "update-19990101000000-1"
            human.write("    Something went wrong. Run: fugaro troubleshoot " + ident + "\n")
            log.write("    Something went wrong. Run: fugaro troubleshoot " + ident + "\n")

    if failing:
        closing, code = "Failed in 1.2s, 5 steps", 2
    elif MODE == "left":
        closing, code = "Left for you in 1.2s, 5 steps", 1
    elif MODE == "summary-mismatch":
        closing, code = "Done in 1.2s, 5 steps", 1
    else:
        closing, code = "Done in 1.2s, 5 steps", 0
    human.write(closing + "\n")
    log.write(closing + "\n")
    log.close()

    if json_mode:
        # The document's own outcome is the run's own: a run that left a step
        # for you says so, and a run whose stage failed names it (G3).
        doc = {"project": PROJECT, "plan_only": False,
               "stages": [{"name": s, "state": "done"} for s in STAGES],
               "left_for_you": ["gateway"] if MODE == "left" else [],
               "failed": {"stage": "converge"} if failing else None,
               "error": False}
        if MODE == "json-extra":
            # A run that writes its account onto stdout as well: the document is
            # then not one document any more.
            sys.stdout.write(json.dumps(doc) + "\n")
            sys.stdout.write("Done in 1.2s, 5 steps\n")
        else:
            sys.stdout.write(json.dumps(doc) + "\n")
    return code


def main():
    argv = sys.argv[1:]
    if not argv:
        sys.stderr.write("fake fugaro: a command is required\n")
        return 64
    if argv[0] == "version":
        return version()
    if argv[0] == "doctor":
        return doctor()
    if argv[0] == "setup" and "--plan-only" in argv:
        return plan_only()
    if argv[0] == "update":
        return update(argv)
    sys.stderr.write("fake fugaro: unexpected %s\n" % " ".join(argv))
    return 64


if __name__ == "__main__":
    sys.exit(main())
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}
write_fake "$work"

# ------------------------------------------------------------------ the fixtures
# seed_owner DIR N LIST [DAYS] [TIE]: N logs of the owner's own in the directory,
# 13 days old unless DAYS says otherwise, so the rule removes them while the seeds
# stand in for 51 runs and the check has to put them back. A log past the 14 days
# is removed by the FIRST run of the check instead, before the seeds exist — the
# machine whose last fugaro run is a fortnight old — which is what DAYS is for.
# TIE gives the two oldest one stamp between them, which is the case the rule's
# own tie-break — the name, largest first — decides. Their names are written to
# LIST for the assertions.
seed_owner() {
  python3 - "$1" "$2" "$3" "${4:-13}" "${5:-0}" <<'PY'
import os
import sys
import time

d, n, listing, days = sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4])
tie = int(sys.argv[5])
names = []
for i in range(n):
    step = n - 2 if tie and i == n - 1 else i
    stamp = time.strftime("%Y%m%d%H%M%S", time.gmtime(time.time() - days * 24 * 3600 - step * 3600))
    name = "update-%s-%d.log" % (stamp, 600000 + i)
    path = os.path.join(d, name)
    with open(path, "w") as fh:
        fh.write("a log of the owner's own, %d\n" % i)
    os.chmod(path, 0o600)
    names.append(name)
with open(listing, "w") as fh:
    fh.write("\n".join(names) + "\n")
PY
}

# --------------------------------------------------------------- the case runner
CASE_MODE=happy
CASE_ARGS=()
CASE_ENV=()
# The installation the fake `fugaro` answers with: a case that needs a refused
# name read out of doctor — the default mode's guard — bends this rather than
# passing --installation, which is the other path to the same guard.
CASE_INSTALLATION=$INSTALLATION
# The flags bash itself is started with: the guard over a traced shell is only
# read with -x really passed, which no argument or variable of the check can do.
CASE_BASH=()
status=0

setup_case() { # name mode
  CASE_MODE=$2
  CASE_ARGS=()
  CASE_ENV=()
  CASE_INSTALLATION=$INSTALLATION
  CASE_BASH=()
  case_dir="$work/$1"
  rm -rf "$case_dir"
  mkdir -p "$case_dir/home" "$case_dir/tmp" "$case_dir/state/fugaro/logs"
  chmod 700 "$case_dir/state/fugaro/logs"
  : >"$case_dir/calls"
  seed_owner "$case_dir/state/fugaro/logs" 2 "$case_dir/ownerlogs"
}

run310() {
  set +e
  # FUGARO_PROJECT is cleared with the agent markers: which installation the
  # check selects is what the cases below assert, so the developer's own
  # selection must not reach the run.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT -u CLAUDE_CODE_SSE_PORT -u NO_COLOR -u CLICOLOR_FORCE -u FORCE_COLOR \
    -u FUGARO_PROJECT \
    ${CASE_ENV[@]+"${CASE_ENV[@]}"} \
    PATH="$work/bin:$PATH" \
    HOME="$case_dir/home" \
    XDG_STATE_HOME="$case_dir/state" \
    TMPDIR="$case_dir/tmp" \
    TERM="${CASE_TERM:-xterm-256color}" \
    CHECK310_TEST_MODE="$CASE_MODE" \
    CHECK310_TEST_CALLS="$case_dir/calls" \
    CHECK310_TEST_INSTALLATION="$CASE_INSTALLATION" \
    CHECK310_RUN_TIMEOUT=60 \
    bash ${CASE_BASH[@]+"${CASE_BASH[@]}"} "$script" ${CASE_ARGS[@]+"${CASE_ARGS[@]}"} \
    >"$case_dir/stdout" 2>"$case_dir/stderr"
  status=$?
  set -e
}

# case_output KEY: what the case's run printed, for the assertions below.
block() { cat "$case_dir/stdout"; }
errout() { cat "$case_dir/stderr"; }
calls() { cat "$case_dir/calls" 2>/dev/null || true; }

# no_seeds_left: the log directory holds none of this check's own seeds. The
# seeds are the names the reader builds (update-<14 digits>-7000NN.log inside
# the 14 days, -800NNN past them), which is what the glob below is: the six
# digits of the pid, not a first digit of 7 or 8 — a fake `fugaro` of this test
# gets pids of its own, and one of those starts with an 8 often enough.
no_seeds_left() {
  local f left=""
  for f in "$case_dir/state/fugaro/logs"/update-??????????????-[78]00[0-9][0-9].log; do
    [ -e "$f" ] || continue
    left="$left${f##*/} "
  done
  [ -z "$left" ] || fail "the check left its own seeded logs in the log directory: $left"
}

# owner_logs_kept: every log of the owner's own is back, with its content.
owner_logs_kept() {
  local name
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    [ -f "$case_dir/state/fugaro/logs/$name" ] || fail "the check did not put the owner's log $name back"
  done <"$case_dir/ownerlogs"
}

THE_CALLS='version
doctor --json
update
setup --plan-only
update
update
update --json
update'

# The same calls where you name the installation: the one read the check makes
# about it is made under FUGARO_PROJECT, so the project and the region it reads
# are the named installation's own rather than whatever is selected by default.
THE_CALLS_NAMED='version
doctor --json [FUGARO_PROJECT=check310demo]
update
setup --plan-only
update
update
update --json
update'

# A --scratch run makes the same calls as the happy one; the project it reads is
# the installation the check made itself.
THE_CALLS_SCRATCH='version
doctor --json [FUGARO_PROJECT=chk310-scratch]
update
setup --plan-only
update
update
update --json
update'

# expect_scratch_seen FILE WHAT: every call of a --scratch run that carries an
# installation was made against the one the check built, and none against an
# installation of yours. The plumbing is read from inside the fake's own calls
# rather than from the block, because the block is written by the thing under
# test: the run makes 8 calls and the one that carries no installation is
# `fugaro version`, which answers only which binary is on PATH.
expect_scratch_seen() { # file what
  local want="project=chk310-scratch config=yes named=yes endpoints=21 loopback=21 no_auth=yes cwd_yaml=yes api=yes"
  local n bad other
  n=$(grep -c . "$1" 2>/dev/null || true)
  [ "$n" = 7 ] || fail "$2: $n call(s) of the run reported their plumbing, wanted the 7 the run makes besides 'fugaro version'"
  bad=$(grep -v "^$want terraform=" "$1" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$2: a call did not see the check's own installation (every endpoint loopback and answering, no auth, the project selected, a fugaro.yaml in the directory it runs in):
$bad"
  # endpoints=21: the URL fields of internal/localcfg.Endpoints (22 keys with
  # no_auth). An endpoint left empty is not a mistake the CLI reports — an empty
  # field IS the real Google root — so the count is pinned here.
  other=$(grep -v 'terraform=fake plan=0$' "$1" 2>/dev/null | grep -vc 'terraform=shim plan=shim$' || true)
  [ "$other" = 0 ] || fail "$2: a call found a terraform on PATH that is neither the check's fake (which plans cleanly) nor the shim of the forced failure:
$(grep -v 'terraform=fake plan=0$' "$1")"
  grep -q 'terraform=shim plan=shim$' "$1" 2>/dev/null \
    || fail "$2: no call of the run saw the shim the forced failure puts first on PATH"
}

# ------------------------------------------------------------------- the cases
case_happy() {
  setup_case happy happy
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "the happy run"
  expect_fact "$out" update-tty PASS "the happy run"
  expect_fact "$out" tty-colour PASS "the happy run"
  expect_fact "$out" tty-spin PASS "the happy run"
  expect_fact "$out" plan-only PASS "the happy run"
  expect_fact "$out" plan-log PASS "the happy run"
  expect_fact "$out" no-color PASS "the happy run"
  expect_fact "$out" piped PASS "the happy run"
  expect_fact "$out" json PASS "the happy run"
  expect_fact "$out" json-stderr PASS "the happy run"
  expect_fact "$out" failure PASS "the happy run"
  expect_fact "$out" failure-id PASS "the happy run"
  expect_fact "$out" failure-log PASS "the happy run"
  expect_fact "$out" log-plain PASS "the happy run"
  expect_fact "$out" log-mode PASS "the happy run"
  expect_fact "$out" retention PASS "the happy run"
  expect_fact "$out" retention-guards PASS "the happy run"
  expect_fact "$out" doctor PASS "the happy run"
  expect_fact "$out" restore PASS "the happy run"
  expect_fact "$out" scratch PASS "the happy run"
  # The seeds list is read back out of the directory rather than assumed: a row
  # that names them is a row that could only be written with the list still in
  # hand, which is what the scratch directory going takes away.
  expect_contains "$out" "seeded log(s) and the five guard entries are out of" "the happy run"
  expect_no_fail "$out" "the happy run"
  expect_calls "$case_dir/calls" "$THE_CALLS"
  no_seeds_left
  owner_logs_kept
  # The log directory as `ls -l` prints it is part of what the check hands over.
  expect_contains "$out" "The log directory, as \`ls -l " "the happy run"
  expect_contains "$out" "CHECK 310 RESULT END (exit 0)" "the happy run"
}

case_left() {
  setup_case left left
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "a run that leaves a step for you"
  expect_fact "$out" update-tty PASS "a run that leaves a step for you"
  expect_contains "$out" "Left for you in 1.2s, 5 steps" "the block's own reading of the closing line"
}

case_no_spinner() {
  setup_case no_spinner no-spinner
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a run that never draws the line in flight"
  expect_fact "$out" tty-spin FAIL "a run that never draws the line in flight"
  expect_fact "$out" tty-colour PASS "a run that never draws the line in flight"
}

case_ansi_on_pipe() {
  setup_case ansi_on_pipe ansi-on-pipe
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a run that colours a pipe"
  expect_fact "$out" piped FAIL "a run that colours a pipe"
  expect_fact "$out" no-color PASS "a run that colours a pipe"
}

case_nocolor_ignored() {
  setup_case nocolor_ignored nocolor-ignored
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a run that ignores NO_COLOR"
  expect_fact "$out" no-color FAIL "a run that ignores NO_COLOR"
  expect_fact "$out" piped PASS "a run that ignores NO_COLOR"
}

case_summary_mismatch() {
  setup_case summary_mismatch summary-mismatch
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a closing line that disagrees with the exit status"
  expect_fact "$out" update-tty FAIL "a closing line that disagrees with the exit status"
}

case_json_extra() {
  setup_case json_extra json-extra
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a --json run that writes its account onto stdout"
  expect_fact "$out" json FAIL "a --json run that writes its account onto stdout"
}

case_fail_no_context() {
  setup_case fail_no_context fail-no-context
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure with no context on the screen"
  expect_fact "$out" failure FAIL "a failure with no context on the screen"
  expect_fact "$out" failure-log PASS "a failure with no context on the screen"
}

# A failure screen that keeps nothing at all: no error text on the failed step
# line, no context under it and no log path. The shim really ran, so this is a
# failure the check has to read as a screen that says nothing — reading the word
# 'terraform' off the screen instead made it a shim that never started, and such
# a run ended 0 with log-plain never measured at all.
case_fail_bare() {
  setup_case fail_bare fail-bare
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure screen that keeps nothing"
  expect_fact "$out" failure FAIL "a failure screen that keeps nothing"
  expect_fact "$out" failure-log PASS "a failure screen that keeps nothing"
  expect_fact "$out" log-plain PASS "a failure screen that keeps nothing"
}

case_fail_no_full_output() {
  setup_case fail_no_full_output fail-no-full-output
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure that names no log"
  expect_fact "$out" failure FAIL "a failure that names no log"
}

case_fail_no_diagnose() {
  setup_case fail_no_diagnose fail-no-diagnose
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure with no troubleshoot line"
  expect_fact "$out" failure FAIL "a failure with no troubleshoot line"
}

case_fail_wrong_id() {
  setup_case fail_wrong_id fail-wrong-id
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure whose run id is another run's"
  expect_fact "$out" failure-id FAIL "a failure whose run id is another run's"
  expect_fact "$out" failure PASS "a failure whose run id is another run's"
}

case_fail_screen_output() {
  setup_case fail_screen_output fail-screen
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure whose stage output went to the screen"
  expect_fact "$out" failure-log FAIL "a failure whose stage output went to the screen"
}

case_fail_both() {
  setup_case fail_both fail-both
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a failure whose stage output went to both"
  expect_fact "$out" failure-log FAIL "a failure whose stage output went to both"
}

case_log_mode_0644() {
  setup_case log_mode_0644 log-mode-0644
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a log that is not 0600"
  expect_fact "$out" log-mode FAIL "a log that is not 0600"
  expect_fact "$out" retention PASS "a log that is not 0600"
}

case_log_ansi() {
  setup_case log_ansi log-ansi
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a log with colour in it"
  expect_fact "$out" log-plain FAIL "a log with colour in it"
}

case_retention_keep() {
  setup_case retention_keep retention-keep
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a directory that is never pruned"
  expect_fact "$out" retention FAIL "a directory that is never pruned"
  expect_fact "$out" retention-guards PASS "a directory that is never pruned"
}

case_retention_eat_guard() {
  setup_case retention_eat_guard retention-eat-guard
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a pruner that eats everything"
  expect_fact "$out" retention-guards FAIL "a pruner that eats everything"
}

case_retention_eat_owner() {
  setup_case retention_eat_owner retention-eat-owner
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a pruner that removes a log the rule keeps"
  expect_fact "$out" retention FAIL "a pruner that removes a log the rule keeps"
}

case_doctor_logs() {
  setup_case doctor_logs doctor-logs
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a read that files a log"
  expect_fact "$out" doctor FAIL "a read that files a log"
}

case_plan_no_rows() {
  setup_case plan_no_rows plan-no-rows
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "a plan view that prints no row"
  expect_fact "$out" plan-only FAIL "a plan view that prints no row"
  expect_contains "$out" "FACT failure: FAIL — not reached" "a run stopped at the plan view"
}

case_plan_log_empty() {
  setup_case plan_log_empty plan-log-empty
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a plan view whose log holds no row"
  expect_fact "$out" plan-log FAIL "a plan view whose log holds no row"
  expect_fact "$out" plan-only PASS "a plan view whose log holds no row"
}

case_restore_eat_owner() {
  setup_case restore_eat_owner restore-eat-owner
  run310
  local out
  out=$(block)
  expect_status "$status" 1 "a run that loses one of the owner's logs"
  expect_fact "$out" restore FAIL "a run that loses one of the owner's logs"
}

# A machine whose last fugaro run is more than 14 days ago: the rule removes the
# owner's logs at the FIRST run of the check, before the retention step's seeds
# exist, so a copy taken there would be a copy of a directory the check had
# already emptied — and the teardown would report the owner's logs lost.
case_owner_logs_removed_early() {
  setup_case owner_logs_removed_early happy
  rm -f "$case_dir/state/fugaro/logs"/*
  seed_owner "$case_dir/state/fugaro/logs" 2 "$case_dir/ownerlogs" 20
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "logs the check's own runs remove"
  expect_fact "$out" restore PASS "logs the check's own runs remove"
  expect_contains "$(errout)" "of your own log(s) fall inside what the rule removes" "logs the check's own runs remove"
  no_seeds_left
  owner_logs_kept
}

# Two logs of the owner's own in the same second, and the count boundary falls
# between them: the rule's own tie-break — the name, largest first — decides
# which of the two goes, so a prediction that ordered them the other way copies
# the wrong one aside and leaves the other removed for good.
case_retention_tie() {
  setup_case retention_tie happy
  rm -f "$case_dir/state/fugaro/logs"/*
  seed_owner "$case_dir/state/fugaro/logs" 46 "$case_dir/ownerlogs" 1 1
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "two logs of one second at the count boundary"
  expect_fact "$out" retention PASS "two logs of one second at the count boundary"
  expect_fact "$out" restore PASS "two logs of one second at the count boundary"
  expect_contains "$(errout)" "of your own log(s) fall inside what the rule removes" "two logs of one second at the count boundary"
  no_seeds_left
  owner_logs_kept
}

case_belong() {
  setup_case belong happy
  CASE_ARGS=(--installation belong)
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "an installation named belong"
  expect_fact "$out" belong FAIL "an installation named belong"
  [ -z "$(calls)" ] || fail "the check ran fugaro against 'belong': $(calls)"
  expect_contains "$(errout)" "never runs against 'belong'" "the refusal on the terminal"
}

# The rest of the register's own guard list: `edge-devel-dimi` is the owner's
# own installation and `fugaro-dev` is the sandbox its rows run on, so a check
# that re-converges its installation four times is never pointed at either — and
# a refusal that named only `belong` would let both through.
case_other_refused_names() {
  local name
  for name in edge-devel-dimi fugaro-dev fugaro-belong belong-prod; do
    setup_case "other_refused_${name}" happy
    CASE_ARGS=(--installation "$name")
    run310
    local out
    out=$(block)
    expect_status "$status" 2 "an installation named $name"
    expect_fact "$out" belong FAIL "an installation named $name"
    [ -z "$(calls)" ] || fail "the check ran fugaro against '$name': $(calls)"
    expect_contains "$(errout)" "never runs against 'belong'" "the refusal on the terminal"
  done
}

# An installation you name, whose GCP project is one the register refuses: the
# project is not known before the first read, so the guard has to run again once
# doctor has answered. A guard that ran only once would let the four converges
# below touch the owner's own GCP project while the block read PASS.
case_gcp_refused() {
  setup_case gcp_refused gcp-refused
  CASE_ARGS=(--installation check310demo)
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "an installation whose GCP project is refused"
  expect_fact "$out" belong FAIL "an installation whose GCP project is refused"
  expect_contains "$(errout)" "names an installation this check must never touch" "the refusal on the terminal"
  expect_calls "$case_dir/calls" 'version
doctor --json [FUGARO_PROJECT=check310demo]'
}

# An installation you name, where the read answers with another project: the
# readings below would be the named one's and the converges another's, so the
# check stops instead of converging a project it did not read.
case_project_mismatch() {
  setup_case project_mismatch doctor-other-project
  CASE_ARGS=(--installation check310demo)
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "an installation the read disagrees with"
  expect_fact "$out" installation FAIL "an installation the read disagrees with"
  expect_contains "$(errout)" "nothing was converged" "the stop on the terminal"
  expect_calls "$case_dir/calls" 'version
doctor --json [FUGARO_PROJECT=check310demo]'
}

# The same installation, read and driven to the end: the one read the check
# makes about it is the named installation's own.
case_named_installation() {
  setup_case named_installation happy
  CASE_ARGS=(--installation check310demo)
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "an installation you name"
  expect_fact "$out" installation PASS "an installation you name"
  expect_fact "$out" belong PASS "an installation you name"
  expect_no_fail "$out" "an installation you name"
  expect_calls "$case_dir/calls" "$THE_CALLS_NAMED"
}

# The four converges are what the owner pastes back, so a shell that arrived
# tracing is refused before any of them runs: `bash -x` prints every command as
# it runs, and the trace would be in the block. The case starts bash with -x
# really passed — the guard reads the flag the shell was started with, which no
# argument of the check can set — so it fails if the guard stops noticing one.
case_xtrace() {
  setup_case xtrace happy
  CASE_BASH=(-x)
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "a shell started with -x"
  expect_fact "$out" xtrace FAIL "a shell started with -x"
  expect_contains "$(errout)" "+ " "a shell started with -x: the trace really was on"
  expect_contains "$(errout)" "tracing" "a shell started with -x: the stop says why"
  expect_contains "$out" "FACT update-tty: FAIL — not reached" "a shell started with -x: no converge ran"
  [ -z "$(calls)" ] || fail "a traced shell made a fugaro call: $(calls)"
}

# What this check writes is four converges' transcripts and 54 seeded logs, so a
# scratch directory inside a git work tree is refused: a `git add -A` of yours
# would commit the check's own output. TMPDIR here is a work tree of the test's
# own, which is the shape the refusal exists for.
case_work_tree() {
  setup_case work_tree happy
  if ! command -v git >/dev/null 2>&1; then
    echo "check310_test: git is not installed, so the work-tree refusal has no case — that one case was not run" >&2
    return 0
  fi
  (cd "$case_dir/tmp" && git init -q .) || { fail "work tree: git init failed, so the case proves nothing"; return 0; }
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "a scratch directory inside a work tree"
  expect_contains "$(errout)" "git work tree" "a scratch directory inside a work tree: the stop says why"
  expect_contains "$out" "FACT aborted: FAIL — " "a scratch directory inside a work tree: the block says the run was refused"
  expect_contains "$out" "FACT update-tty: FAIL — not reached" "a scratch directory inside a work tree: no converge ran"
  [ -z "$(calls)" ] || fail "a scratch directory inside a work tree made a fugaro call: $(calls)"
  [ -z "$(find "$case_dir/tmp" -mindepth 1 -maxdepth 1 -name 'chk310.*' 2>/dev/null)" ] \
    || fail "a scratch directory inside a work tree: the scratch directory survived the refusal"
}

case_agent() {
  setup_case agent happy
  CASE_ENV=(CLAUDECODE=1)
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "an agent's session"
  expect_fact "$out" agent FAIL "an agent's session"
  [ -z "$(calls)" ] || fail "the check ran fugaro in an agent's session: $(calls)"
}

case_dry_run() {
  setup_case dry_run happy
  CASE_ARGS=(--dry-run)
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "a dry run"
  expect_fact "$out" dry-run PASS "a dry run"
  expect_fact "$out" belong INFO "a dry run"
  expect_contains "$(errout)" "NOTHING below was run" "a dry run"
  [ -z "$(calls)" ] || fail "a dry run made a fugaro call: $(calls)"
  expect_absent "$out" "FACT update-tty: PASS" "a dry run"
  # A dry run of an installation you name: the guard reads the name it is given
  # and the row is written for it, with no project to name — a dry run reads
  # none.
  setup_case dry_run_named happy
  CASE_ARGS=(--dry-run --installation check310demo)
  run310
  local named
  named=$(block)
  expect_status "$status" 0 "a dry run of a named installation"
  expect_fact "$named" belong PASS "a dry run of a named installation"
  [ -z "$(calls)" ] || fail "a dry run of a named installation made a fugaro call: $(calls)"
}

# A --scratch run drives no installation of yours: the check makes one of its
# own — a fugaro.yaml and a local config naming chk310-scratch, every API root of
# that config on the loopback fake the check starts, a fake terraform first on
# PATH — and the readings are the CLI's own output all the same. The two facts
# that are about an installation of yours say 'not read in --scratch' rather than
# passing on the check's own, and nothing of the owner's is read: this case's own
# log directory holds exactly the two logs it was seeded with, and the block
# never names it.
case_scratch() {
  setup_case scratch scratch
  CASE_ARGS=(--scratch)
  CASE_ENV=(CHECK310_TEST_SCRATCH="$case_dir/scratch-seen")
  run310
  local out left want
  out=$(block)
  expect_status "$status" 0 "a --scratch run"
  expect_no_fail "$out" "a --scratch run"
  expect_fact "$out" installation INFO "a --scratch run"
  expect_fact "$out" belong INFO "a --scratch run"
  expect_fact "$out" update-tty PASS "a --scratch run"
  expect_fact "$out" tty-colour PASS "a --scratch run"
  expect_fact "$out" scratch PASS "a --scratch run"
  expect_contains "$out" "not read in --scratch" "a --scratch run"
  expect_absent "$out" "$case_dir/state" "a --scratch run: the block names no directory of yours"
  expect_contains "$out" "no installation of yours" "a --scratch run"
  expect_calls "$case_dir/calls" "$THE_CALLS_SCRATCH"
  expect_scratch_seen "$case_dir/scratch-seen" "a --scratch run"

  # The log directory this case seeded is the owner's here: the scratch run has
  # a directory of its own, so the seeds and the pruning below are never in this
  # one.
  left=$(ls -A "$case_dir/state/fugaro/logs" 2>/dev/null | sort)
  want=$(sort "$case_dir/ownerlogs")
  [ "$left" = "$want" ] || fail "a --scratch run changed the log directory of this case: it holds
$left
wanted
$want"
  no_seeds_left
  owner_logs_kept
  [ -z "$(find "$case_dir/tmp" -mindepth 1 -maxdepth 1 -name 'chk310.*' 2>/dev/null)" ] \
    || fail "a --scratch run left its scratch directory behind"
}

# The two are the same question asked twice — one drives an installation of
# yours and the other drives none — so a run given both refuses before it makes
# anything: no scratch directory, no API root, no fugaro call.
case_scratch_conflict() {
  setup_case scratch_conflict scratch
  CASE_ARGS=(--scratch --installation fugaro-dev)
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "a run given --scratch and --installation"
  expect_contains "$(errout)" "give one or the other" "a run given --scratch and --installation: the refusal says why"
  [ -z "$(calls)" ] || fail "a run given --scratch and --installation made a fugaro call: $(calls)"
  [ -z "$(find "$case_dir/tmp" -mindepth 1 -maxdepth 1 -name 'chk310.*' 2>/dev/null)" ] \
    || fail "a run given --scratch and --installation made a scratch directory"
}

# A dry run of a --scratch run: the plan only, and the rows for the installation
# and the register say what a scratch run cannot read rather than passing.
case_scratch_dry_run() {
  setup_case scratch_dry_run happy
  CASE_ARGS=(--dry-run --scratch)
  run310
  local out
  out=$(block)
  expect_status "$status" 0 "a --scratch dry run"
  expect_fact "$out" dry-run PASS "a --scratch dry run"
  expect_fact "$out" installation INFO "a --scratch dry run"
  expect_fact "$out" belong INFO "a --scratch dry run"
  expect_contains "$out" "not read in --scratch" "a --scratch dry run"
  [ -z "$(calls)" ] || fail "a --scratch dry run made a fugaro call: $(calls)"
  expect_absent "$out" "FACT update-tty: PASS" "a --scratch dry run"
}

# The guard over the register's names reads the installation in force, so the
# default mode — the one that asks doctor and converges what it names — refuses
# one of them before any converge. A --scratch run never reaches this read: it
# drives an installation of the check's own, and the row says so.
case_default_refused() {
  setup_case default_refused happy
  CASE_INSTALLATION=fugaro-dev
  run310
  local out
  out=$(block)
  expect_status "$status" 2 "the default mode with a refused installation selected"
  expect_fact "$out" belong FAIL "the default mode with a refused installation selected"
  expect_contains "$(errout)" "never runs against 'belong'" "the default mode's refusal"
  expect_calls "$case_dir/calls" 'version
doctor --json'
  expect_contains "$out" "FACT update-tty: FAIL — not reached" "the default mode's refusal: no converge ran"
}

case_help() {
  setup_case help happy
  CASE_ARGS=(--help)
  run310
  expect_status "$status" 0 "the usage"
  expect_contains "$(block)" "usage: check310.sh" "the usage"
  expect_absent "$(block)" "CHECK 310 RESULT" "the usage"
  [ -z "$(calls)" ] || fail "the usage made a fugaro call: $(calls)"
}

case_unknown_flag() {
  setup_case unknown_flag happy
  CASE_ARGS=(--nonsense)
  run310
  expect_status "$status" 2 "an unknown flag"
  [ -z "$(calls)" ] || fail "an unknown flag made a fugaro call: $(calls)"
}

# ------------------------------------------------------------------- the runner
tests=(
  happy
  left
  no_spinner
  ansi_on_pipe
  nocolor_ignored
  summary_mismatch
  json_extra
  fail_no_context
  fail_bare
  fail_no_full_output
  fail_no_diagnose
  fail_wrong_id
  fail_screen_output
  fail_both
  log_mode_0644
  log_ansi
  retention_keep
  retention_eat_guard
  retention_eat_owner
  doctor_logs
  plan_no_rows
  plan_log_empty
  restore_eat_owner
  owner_logs_removed_early
  retention_tie
  belong
  other_refused_names
  gcp_refused
  project_mismatch
  named_installation
  scratch
  scratch_conflict
  scratch_dry_run
  default_refused
  xtrace
  work_tree
  agent
  dry_run
  help
  unknown_flag
)

index=0
for name in "${tests[@]}"; do
  if [ -n "${CHECK_TEST_ONLY:-}" ] && [ "$CHECK_TEST_ONLY" != "$name" ]; then
    index=$((index + 1))
    continue
  fi
  if [ -n "${CHECK_TEST_SHARD:-}" ]; then
    shard=${CHECK_TEST_SHARD%%/*}
    of=${CHECK_TEST_SHARD##*/}
    if [ $((index % of)) != "$shard" ]; then
      index=$((index + 1))
      continue
    fi
  fi
  index=$((index + 1))
  "case_$name"
done

if [ "$failures" -gt 0 ]; then
  echo "check310_test: $failures failure(s)" >&2
  exit 1
fi
echo "check310_test: ok"
