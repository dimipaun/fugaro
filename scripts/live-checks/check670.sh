#!/usr/bin/env bash
# Check 670 — the enrolment, end to end, on a real installation: `fugaro project
# repos add` prints its six steps and converges them, the empty `github-app-key`
# container is named by `status` with the command that fixes it and a nonzero
# exit (F11, the failure the whole order answers), filling it turns the row green
# and the exit zero, a run starts, a removal under a run in flight takes the
# STRICT confirmation and the run finishes anyway, and the six things `rm`
# deliberately leaves are still there afterwards.
#
# Task RE6 of docs/repos-management/implementation-plan.md writes this check, and
# the number is that work's own: it was written as Check 620 and the owner ruled
# on 2026-11-21 that it renumbers to **670**, because 620 to 627 are the
# mobile-auth work's (the register's own paragraph and docs/status.md both say
# so). What it settles is read from the outside and nowhere else: **D2400** (the
# six steps, in the order the plan prints them), **F11/D2411** (an empty
# credential container is what makes an enrolment read as complete while the
# readiness gate plans no job, and `status` says so and names the fix),
# **D2405/D2406** (what `rm` leaves) and **D2407** (a run in flight is never
# disturbed: the typed project name is the only thing that confirms the removal
# while one is going).
#
# What it measures, in the order the plan fixes it:
#
#   * (1) the enrolment itself, on a fresh throwaway checkout: `fugaro project
#     repos add <repo> --project <installation>` prints the SIX steps it is about
#     to run, one numbered line each — the repository's own fugaro.yaml, its
#     local config entry, its image map, the allow-list, the publish and the job
#     with its invoker grant — names the first image build as billable and NOT
#     part of it, asks for the project's name, and then closes with the
#     `enrolled ... is ready` line. Every one of those lines is a row: it is the
#     plan's step (1), and a converge that ran fewer steps than it printed is the
#     thing this check exists to catch;
#   * (2) **F11**: the `github-app-key` container is left EMPTY on the first
#     attempt — a Terraform-created container holds no version, and this check
#     refuses to measure the failure if a version is already stored, because then
#     there is no empty container to read. `fugaro project repos status <repo>`
#     must name that row (`credential-version`), say the container holds no
#     enabled version, and exit **nonzero**; the fix it names is read from the
#     same command's `--json` document, because the screen clips a long row at
#     160 columns and the fix is the tail of it — the document is where the fix
#     survives whole, and it is the same document the console reads;
#   * (3) the fill: `fugaro secrets set github-app-key --repo <repo>` with the
#     App's private key REDIRECTED from the file (never on a command line, never
#     in a fact), the converge run again, and `status` then exits **zero** with a
#     green row — the same read, the other answer;
#   * (4) a run: `fugaro run <brief> --repo <repo> --json` names a run, and the
#     project's listing holds its row. This is the first of the check's TWO
#     billable runs;
#   * (5) the removal under a run in flight: a second run is launched and watched
#     until it is going, `fugaro project repos rm <repo>` is run WHILE it goes,
#     and the removal must ask the STRICT confirmation — the sentence that says
#     typing the project's name is the only thing that confirms it while a run is
#     in flight, naming the run. That sentence is printed in the command's own
#     REFUSAL and nowhere else, so the check reads it there: the same removal is
#     run once with no terminal at all and must refuse, name the run and write
#     nothing before the question. Then it is run at your terminal, where it
#     prints the question itself — the project whose name it asks for, and the run
#     it is about — and proceeds; and the run must END anyway (D2407: nothing
#     rewrites what a run in flight holds);
#   * (6) the six things `rm` leaves (D2406), each read from the outside after
#     the removal: the provider's **App installation** (`gh api
#     /repos/<repo>/installation` — the one fact only the provider holds, and the
#     reason this check's fakes include `gh`), the **built image** (the shared
#     lock's own entry through `fugaro image ls --shared --json`, then Artifact
#     Registry through `gcloud`), the **secret and its version** (`gcloud secrets
#     describe` and its enabled versions), the **run history** in the runs bucket,
#     the **pull request** the run opened (`gh pr view`) and the **remote branch**
#     (`git ls-remote --heads`). And then that `fugaro update` afterwards writes
#     nothing new: it exits 0, fails no stage, leaves nothing for the owner, and
#     the allow-list and the enrolment it reads afterwards are the ones it read
#     before.
#
# What it never does. It never reads or prints a secret value: the private key is
# redirected into `fugaro secrets set` from a 0600 file, never reaches the block,
# and every read here is metadata (an id, a version, a state, a count). It never
# touches `belong`, the one installation a check is never run against. And no
# agent runs any step of it.
#
# Usage:      bash scripts/live-checks/check670.sh --installation <name> --app-key-file <pem>
#             bash scripts/live-checks/check670.sh --installation <name> --app-key-file <pem> --preflight
#             bash scripts/live-checks/check670.sh --installation <name> --app-key-file <pem> --dry-run
#             bash scripts/live-checks/check670.sh --help
#             --preflight makes the check's own READS and evaluates every
#             precondition before anything is written or launched (the checkout,
#             the installation `fugaro doctor --json` reads, the enrolment and
#             the empty github-app-key container F11 turns on), prints every
#             blocker at once with the command that clears it, writes nothing and
#             launches nothing, and exits non-zero when anything blocks.
# Needs:      bash, mktemp, python3 (every `--json` document is read with it),
#             git (the throwaway checkout and the remote branch), gcloud (the
#             image, the secret and the runs bucket), gh (the App installation
#             and the pull request) and fugaro (the release you have installed:
#             this check drives IT, it does not build one).
# Where:      your own machine, at YOUR terminal, against a THROWAWAY installation
#             and the GitHub sandbox repository. The typed confirmations are the
#             CLI's own (`add` asks for the project's name) and they stand in for
#             everything this script does not ask: it asks you NOTHING of its own,
#             and it prints their output on your terminal as it happens. A value
#             naming belong is refused by name, and so is a coding agent's session.
# Costs:      TWO billable runs of the installed agent on the sandbox, plus the
#             `fugaro update` and the deployment the enrolment drives. Whatever
#             the first image build costs is NOT part of this: `add` says so
#             itself and leaves the build to `fugaro image build <entry>`. The
#             image row is measured only if the installation's shared lock already
#             serves the repository; with none the row says so rather than
#             failing. Everything else this check runs is a read or a config write.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check670_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory (and the throwaway checkout in it), removed
#             from the exit trap whatever happened. What is NOT undone: the
#             repository is removed from the installation, by this check's own
#             point, and the six things `rm` leaves are left — the block's restore
#             row names what you may still want to take away by hand (the branch,
#             the pull request it opened, and the App's installation on the
#             repository).
# Output:     progress on stderr, ONE block on stdout headed CHECK 670 RESULT,
#             then CHECK 670 RESULT END (exit N) — to copy back into the
#             register's Check 670 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold, or a guard stopped the check before it could run. 2: a row
#             could not be measured for a reason that is this check's own setup
#             rather than a break — a missing tool, a container that already
#             holds a version (so F11 has nothing to read), a run that ended
#             before the removal could be run under it, an image nothing built,
#             a run that opened no pull request. A key the run never reached
#             reads `FAIL — not reached`, which is a row that was NOT measured.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK670_INSTALLATION, CHECK670_REPO (dimipaun/fugarosandbox — the name must
# contain `sandbox`), CHECK670_APP_KEY_FILE, CHECK670_TASK (the brief the two
# runs are launched with; with none, the check writes a throwaway one of its
# own), CHECK670_BASE (the --ref the runs are launched on; with none the
# repository's own default branch is used), CHECK670_GCP_PROJECT (with none, the
# project is read from the checkout's `fugaro doctor --json`),
# CHECK670_RUNS_BUCKET (with none, `gs://fugaro-runs-<project>`),
# CHECK670_PRODUCTION (a project named fugaro-prod is refused unless it is set),
# CHECK670_DRY_RUN, CHECK670_PREFLIGHT, CHECK670_KEEP_SCRATCH, CHECK670_RUN_SECONDS (2400, the bound
# on waiting for one run to end), CHECK670_POLL_SECONDS (20),
# CHECK670_LAUNCH_SECONDS (300), CHECK670_CLI_TIMEOUT_SECONDS (300),
# CHECK670_CLONE_SECONDS (300) and CHECK670_LOG_LIMIT (50, how many rows of the
# project's run listing are read).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a
# converge and two runs, and it reads a listing of every `--json` document it is
# given — a trace of them is a second copy of what they printed.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block and the two
# interactive commands' own output are written to the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and removes the scratch directory. A stop right after an `unmeasured` row
# ends the run as one that could not measure (exit 2): the difference matters to
# whoever reads the block, because 1 is a finding about the release and 2 is a run
# that never got to measure it.
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}

FACTS_FILE=""
LAST_ROW=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in
  FAIL) LAST_ROW=fail ;;
  *) LAST_ROW="" ;;
  esac
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release — an installation whose container is already filled,
# a run that ended before the removal could be run under it, an image nothing
# built. The row reads FAIL in the block (it is not a pass) and the check ends as
# one that could not measure (exit 2), whether it stopped there or ran on.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  LAST_ROW=unmeasured
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

# run_bounded SECONDS KEY CMD...: run one command under a bound, capturing its
# output into $SCRATCH/KEY.{out,err}, from the checkout and with its stdin on
# BOUNDED_STDIN, which is /dev/null unless a caller sets it. The child execs, so
# the pid the bound kills IS the command — a hung
# cloud read would otherwise outlive the check that gave up on it. Its exit
# status is the command's, and it is also left in BOUNDED_STATUS for a caller
# that runs it for its status alone. The ERR trap is dropped in the child: this
# check runs commands whose failing exit status is one of its own readings, and a
# trap inherited there would turn each of them into "an internal error".
#
# stdin on /dev/null is deliberate for two of these commands: every read here is
# non-interactive (`--json` confirms nothing), and step (5) reads the STRICT
# confirmation by running `rm` with no terminal at all — the sentence that says
# typing the project's name is the only thing that confirms it is in THAT
# refusal, and a run that fielded a prompt instead would hang here. The one
# command that is FED rather than read is step (3)'s `secrets set`, which takes
# the App's key on its stdin and refuses an empty one: a redirection written on
# THAT call would land on run_bounded and be thrown away by the /dev/null below,
# so the file is named in BOUNDED_STDIN and reaches the child here.
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  # Truncated, not appended: the listing is read again on every poll below, and
  # this file is handed to python3 as ONE JSON document — a second document
  # appended to the first is a file that parses as neither.
  ( trap - ERR; cd "${BOUNDED_CWD:-$CHECKOUT}" || exit 1; exec "$@" ) <"${BOUNDED_STDIN:-/dev/null}" \
    >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    [ "$waited" -lt "$secs" ] || break
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    BOUNDED_TIMED_OUT=yes
    kill -TERM "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
  fi
  local st=0
  wait "$pid" 2>/dev/null || st=$?
  BOUNDED_STATUS=$st
  return "$st"
}

# run_bounded_in DIR SECONDS KEY CMD...: run_bounded from DIR rather than from
# the checkout. The clone is the one command that runs before the checkout
# exists — it is what makes it — and a command run from wherever the shell
# happened to be standing would read another directory's fugaro.yaml.
run_bounded_in() { # run_bounded_in DIR SECONDS KEY CMD...
  local dir=$1
  shift
  local saved=$BOUNDED_CWD st=0
  BOUNDED_CWD=$dir
  run_bounded "$@" || st=$?
  BOUNDED_CWD=$saved
  return "$st"
}

# run_tee KEY CMD...: run a command that TALKS TO THE PERSON in front of it —
# `add`, which asks for the project's name — from the checkout, with its output on
# your own terminal (descriptor 4, the stderr this check was started with) and a
# copy in $SCRATCH/KEY.out for the rows. It is not bounded: the thing it waits for
# is you, and a bound would end a converge the instant you were slow. Nothing of it
# goes to stdout, where the block belongs. Its exit status is the command's (the
# first stage of the pipe), left in BOUNDED_STATUS and returned.
run_tee() { # run_tee KEY CMD...
  local key=$1
  shift
  BOUNDED_TIMED_OUT=no
  BOUNDED_STATUS=0
  local st=0
  ( trap - ERR; cd "$CHECKOUT" || exit 1; exec "$@" ) 2>&1 | tee "$SCRATCH/$key.out" >&4 || st=${PIPESTATUS[0]:-1}
  BOUNDED_STATUS=$st
  return "$st"
}

# The shared `--preflight` driver (scripts/live-checks/lib/preflight.sh).
live_check_lib_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$live_check_lib_dir}
# shellcheck source=lib/preflight.sh
. "$LIVE_CHECK_LIB/preflight.sh"
PREFLIGHT_TOOL=check670

need_tools() {
  local t
  for t in bash mktemp python3 git gcloud gh; do
    command -v "$t" >/dev/null 2>&1 || die "Check 670 needs '$t' and it is not on PATH"
  done
}

last_lines() { tail -n 6 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400; }

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk670-mine"
SCRATCH_REMOVED=no
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""
BOUNDED_STATUS=0
BOUNDED_TIMED_OUT=no
BOUNDED_CWD=""
BOUNDED_STDIN=""

INSTALLATION=""
REPO=""
CANONICAL=""
APP_KEY_FILE=""
TASK_FILE=""
BASE=""
GCP_PROJECT=""
RUNS_BUCKET=""
PRODUCTION=""
DRY_RUN=""
KEEP_SCRATCH=""
FUGARO_BIN=""
GIT_BIN=""
GCLOUD_BIN=""
GH_BIN=""
CHECKOUT=""
REGION=""
DOCTOR_INSTALL=""
SECRET_ID=""
SECRET_LATEST=""
IMAGE_REF=""
RUN1_ID=""
RUN2_ID=""
RUN1_REF=""
RUN2_REF=""
RUN1_BRANCH=""
PR_URL=""
APP_INSTALL=""
LAUNCHED_ID=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check670.XXXXXX") || die "Check 670 could not make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK670_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  CHECKOUT="$SCRATCH/checkout"
}

scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "$KEEP_SCRATCH" = 1 ]; then
    SCRATCH_KEPT=yes
    return 0
  fi
  # The marker is what says the directory is this check's own: `rm -rf` over a
  # path a variable made is the one destructive line here, and it only ever runs
  # over a directory this check created and marked.
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it does not carry this check's own marker"
    SCRATCH_KEPT=yes
    return 0
  fi
  rm -rf "$SCRATCH"
  SCRATCH_REMOVED=yes
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  if [ "$PREFLIGHT_MODE" = yes ]; then
    # A preflight wrote nothing and launched nothing: it prints its own lines
    # and no RESULT block.
    scratch_down
    exit "$status"
  fi
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK670_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory and the throwaway checkout in it were removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    if [ "$status" -eq 0 ]; then status=2; fi
  fi
  # A run that reached the end with a FAILed row is a run with a finding: exit 1
  # says so without anyone having to read the block for it. An unmeasured row is
  # the exception — it is a FAIL in the block and a 2 above.
  if [ "$status" -eq 0 ]; then
    local failed=no key
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
    done <<<"$facts"
    if [ "$failed" = yes ]; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version installation sandbox app-key checkout xtrace agent-session belong
  enrol empty-container status-screen status-fix status-exit filled status-green
  run-started strict-confirmation confirmation removal run-finished
  kept-app kept-image kept-secret kept-history kept-pull-request kept-branch kept-update
  restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=$'\n'
  printf '\nCHECK 670 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached (the run stopped before this step)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $printed in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 670 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------- the readers
# Every document below is read with python3 rather than by pattern, so a member
# this check does not know about cannot be mistaken for one it does. Each reader
# prints tab-separated lines and exits non-zero on a document it cannot read: the
# caller reads that as "the document did not answer", which is a row about the
# installation rather than this check's crash. The document is a PATH in argv and
# never a pipe.
read_json() { # read_json WHAT FILE OUTFILE
  python3 - "$1" "$2" >"$3" 2>/dev/null <<'PY'
import json, sys

what, path = sys.argv[1], sys.argv[2]


def text(v):
    return "" if v is None else str(v)


try:
    with open(path) as fh:
        doc = json.load(fh)
except Exception:
    sys.exit(1)

if what == "secrets":
    # `fugaro secrets ls --json` prints an ARRAY of entries; each carries the
    # container's metadata (id, versions, latest) and never a value.
    entries = doc if isinstance(doc, list) else (doc.get("secrets") or [])
    for e in entries:
        print("\t".join([
            "secret", text(e.get("name")), text(e.get("id")), text(e.get("scope")),
            text(e.get("latest")), text(e.get("versions") or 0),
        ]))
elif what == "doctor":
    p = doc.get("project") or {}
    print("install\t%s" % text(p.get("name")))
    print("project\t%s" % text(p.get("gcp_project")))
    print("region\t%s" % text(p.get("region")))
elif what == "launch":
    print("run_id\t%s" % text(doc.get("run_id")))
    print("status\t%s" % text(doc.get("status")))
elif what == "rows":
    for row in (doc.get("runs") or []):
        print("\t".join([
            "row", text(row.get("run_id")), text(row.get("run")), text(row.get("status")),
            "true" if row.get("terminal") else "false", text(row.get("stage")),
            text(row.get("branch")), text(row.get("pr_url")), text(row.get("outcome")),
        ]))
elif what == "shared":
    for e in (doc.get("images") or []):
        print("\t".join([
            "entry", text(e.get("key")), text(e.get("image")),
            ",".join(e.get("repositories") or []),
        ]))
elif what == "status":
    for r in (doc.get("repos") or []):
        for row in (r.get("rows") or []):
            print("\t".join([
                "statusrow", text(r.get("canonical")), text(row.get("id")),
                "true" if row.get("ok") else "false",
                text(row.get("problem")), text(row.get("fix")),
            ]))
elif what == "update":
    print("failed\t%s" % ("yes" if doc.get("failed") else "no"))
    print("left_for_you\t%d" % len(doc.get("left_for_you") or []))
    print("stages\t%d" % len(doc.get("stages") or []))
elif what == "show":
    print("allow_repos\t%s" % ",".join(sorted(doc.get("allow_repos") or [])))
    enrolled = doc.get("enrolled") or {}
    print("enrolled\t%s" % ",".join(sorted(enrolled.keys())))
PY
}

field() { # field FILE KEY — the second field of the first KEY line, "" when absent
  awk -F'\t' -v k="$2" '$1 == k { print $2; exit }' "$1" 2>/dev/null
}

row_of() { # row_of FILE RUN-ID — the row line of one run, empty when absent
  grep -m1 "^row	$2	" "$1" 2>/dev/null || true
}

status_row() { # status_row FILE ROW-ID — the statusrow line of one row, "" when absent
  awk -F'\t' -v id="$2" '$1 == "statusrow" && $3 == id { print; exit }' "$1" 2>/dev/null || true
}

row_field() { # row_field ROW N — the Nth tab-separated field of a line
  printf '%s' "$1" | awk -F'\t' -v n="$2" '{ print $n }'
}

contains() { # contains HAYSTACK NEEDLE
  case "$1" in
  *"$2"*) return 0 ;;
  *) return 1 ;;
  esac
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check670.sh --installation <name> --app-key-file <pem> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace would copy the converge's own output to the terminal a second time"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy what the converge or a run printed to the terminal a second time"
}

# guard_agent_session: `add` and `rm` refuse a coding agent's session by name
# before any read, and this check runs them. Refusing here rather than letting
# each command refuse is what makes the block say why.
#
# The loop is the five markers that say an agent, and not a person, is driving
# this shell: agentenv.AgentMarkers. agentenv.Markers holds a sixth,
# CLAUDE_CODE_SSE_PORT, and it is not one of those -- it says this shell sits in
# an editor's terminal, where the person is still the one typing, so refusing on
# it would lock you out of this check in your own editor. It is DROPPED from this
# script's environment instead: every child below has no terminal (its stdin is
# on /dev/null), and in a run with no terminal agentenv.Session reads that marker
# as a coding agent's session -- `add`, `rm`, `update` and the runs would take
# the agent-refusal path, and the block would record those refusals as findings
# about the release after this check had already enrolled the repository and
# spent its billable runs.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 670 is yours to run at your own terminal, on a throwaway installation, and no agent runs any step of it"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for — the editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused: it says this shell sits in an editor's terminal, not that an agent is driving it, and every command below has no terminal, where the CLI would read it as an agent's own session"
}

# guard_inputs: the installation, the repository and the key file, read before
# anything is cloned or run. `belong` is refused by name because it is the one
# installation and the one workspace a check is never pointed at, and the
# repository has to BE a sandbox: this check enrols a repository and then removes
# it, so a real one is not something it may be aimed at by accident.
guard_inputs() {
  local all="${INSTALLATION:-}${REPO:-}${APP_KEY_FILE:-}${GCP_PROJECT:-}${RUNS_BUCKET:-}${TASK_FILE:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there" ;;
  esac
  fact belong PASS "nothing this check was given names belong (the installation, the repository, the project and every path are read here before anything is cloned or run)"
  if [ -z "${REPO%%/*}" ] || [ -z "${REPO##*/}" ] || ! printf '%s' "$REPO" | grep -q '^[A-Za-z0-9._-]*/[A-Za-z0-9._-]*$'; then
    die "--repo '$REPO' is not a repository name (owner/name)"
  fi
  if ! contains "$REPO" sandbox; then
    stop "--repo $REPO is not a sandbox repository (its name has to contain 'sandbox'): this check enrols a repository into an installation and then REMOVES it, so it is only ever aimed at a sandbox one"
  fi
  fact sandbox PASS "$REPO is a sandbox repository and nothing here names belong: this check removes it from the installation, which is what it must never do to a real one"
  if [ -n "$GCP_PROJECT" ] && [ "$GCP_PROJECT" = fugaro-prod ] && [ "$PRODUCTION" != 1 ]; then
    stop "this check was given --gcp-project fugaro-prod, which is refused unless --production is given: the steps below converge an installation and remove a repository from it, and production is only ever measured on purpose. Nothing was run"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    fact app-key PASS "planned: $APP_KEY_FILE is read for its mode and its shape, never for its value, and it is redirected into 'fugaro secrets set' and nowhere else"
    fact installation PASS "planned: $INSTALLATION, with the GCP project read from the checkout's own 'fugaro doctor --json'${GCP_PROJECT:+ (--gcp-project names $GCP_PROJECT)}"
    return 0
  fi
  if [ ! -f "$APP_KEY_FILE" ]; then
    unmeasured app-key "the App key file $APP_KEY_FILE is not there: the fill in step (3) redirects it into 'fugaro secrets set github-app-key --repo $REPO' (the PEM comes from the file, never from this script), and a file that is not there is a row this check cannot measure. Nothing was cloned or run"
    stop "the App key file $APP_KEY_FILE is not there, so the container could not be filled. Write the App's private key to a file of its own (mode 0600) and run the check again"
  fi
  local mode bytes lines
  mode=$(python3 -c 'import os,sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$APP_KEY_FILE" 2>/dev/null || true)
  if [ "$mode" != 600 ]; then
    unmeasured app-key "the App key file $APP_KEY_FILE is mode ${mode:-unknown}, not 600: this check will not carry a private key out of a world-readable file. Nothing was cloned or run"
    stop "the App key file $APP_KEY_FILE is mode ${mode:-unknown} and has to be 0600. Nothing was read from it"
  fi
  bytes=$(wc -c <"$APP_KEY_FILE" | tr -d ' ')
  lines=$(wc -l <"$APP_KEY_FILE" | tr -d ' ')
  if [ "$bytes" -eq 0 ]; then
    unmeasured app-key "the App key file $APP_KEY_FILE is empty: 'fugaro secrets set' refuses a value of fewer than four bytes, so the fill in step (3) could not be measured"
    stop "the App key file $APP_KEY_FILE is empty. Nothing was read from it"
  fi
  if ! head -c 64 "$APP_KEY_FILE" | grep -q -- '-----BEGIN'; then
    unmeasured app-key "the App key file $APP_KEY_FILE does not begin with a PEM header: it is the GitHub App's private key file (.pem) that step (3) redirects into 'fugaro secrets set', and a file that is something else is a row this check cannot measure"
    stop "the App key file $APP_KEY_FILE does not look like a PEM. Nothing was read from it"
  fi
  fact app-key PASS "$APP_KEY_FILE is one PEM at mode 0600 ($bytes bytes on $lines lines; its value is never read into a fact, a command line or the block — it goes into 'fugaro secrets set' by redirection and nowhere else)"
}

# version_row: the release this check drives. `add`, `rm`, `status` and `update`
# are the ones you have installed, and a check that drove another build would
# measure that build's contract rather than this repository's.
version_row() {
  local v=""
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact fugaro-version PASS "${v:-fugaro version printed nothing} (the build this check drives: the enrolment, the four verbs and the update are the ones this release makes)"
}

# ---------------------------------------------------------------- preflight
# step_preflight: --preflight. The reads this check makes, every precondition it
# has evaluated before its first write, nothing written and nothing launched. It
# clones the repository into this check's own scratch directory — the one write
# it makes, and it goes with the exit trap — because the checkout is where the
# installation is read from and the enrolment's own first step runs.
step_preflight() {
  if [ -e "$CHECKOUT" ]; then
    die "$CHECKOUT is already there, so this check cannot make its own throwaway checkout from it"
  fi
  say "preflight: cloning https://github.com/$REPO.git to read the checkout it runs from"
  if ! run_bounded_in "$SCRATCH" "$CLONE_SECONDS" pf-clone "$GIT_BIN" clone --depth 1 "https://github.com/$REPO.git" "$CHECKOUT"; then
    preflight_block checkout \
      "the clone of $REPO did not answer (exit ${BOUNDED_STATUS}), so the checkout this check runs from cannot be read" \
      "git clone --depth 1 https://github.com/$REPO.git"
    preflight_report
  fi
  if [ ! -f "$CHECKOUT/fugaro.yaml" ]; then
    say "preflight: $REPO has no fugaro.yaml; the enrolment writes the starter one into the checkout"
  fi

  # doctor exits 1 when it finds a problem and still prints its document, and the
  # empty container this check needs is exactly such a problem: an exit 1 with a
  # readable document is an answer, anything else is not.
  say "preflight: reading the installation: fugaro doctor --json, in ${CHECKOUT}"
  local doctor_ok=yes
  if ! run_bounded "$CLI_TIMEOUT" pf-doctor "$FUGARO_BIN" doctor --json; then
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 1 ]; then doctor_ok=no; fi
  fi
  if [ "$doctor_ok" = yes ] && ! read_json doctor "$SCRATCH/pf-doctor.out" "$SCRATCH/pf-doctor.txt"; then
    doctor_ok=no
  fi
  if [ "$doctor_ok" = no ]; then
    preflight_block installation \
      "fugaro doctor --json does not answer in the checkout (exit ${BOUNDED_STATUS}), so the installation this check would enrol into cannot be read" \
      "git clone --depth 1 https://github.com/$REPO.git && cd ${REPO##*/} && fugaro doctor --json"
  elif [ -z "$(field "$SCRATCH/pf-doctor.txt" project)" ]; then
    preflight_block installation \
      "the checkout's doctor names no GCP project, so the registry, the secrets and the runs bucket this check reads from the outside have no project" \
      "git clone --depth 1 https://github.com/$REPO.git && cd ${REPO##*/} && fugaro doctor --json"
  else
    GCP_PROJECT=${GCP_PROJECT:-$(field "$SCRATCH/pf-doctor.txt" project)}
    preflight_note installation \
      "the installation is $(field "$SCRATCH/pf-doctor.txt" install) in $GCP_PROJECT (the checkout's own doctor; the runs bucket is ${RUNS_BUCKET:-gs://fugaro-runs-$GCP_PROJECT})"
  fi

  say "preflight: reading $INSTALLATION's enrolment: fugaro project show --json"
  if run_bounded "$CLI_TIMEOUT" pf-show "$FUGARO_BIN" project show --json --project "$INSTALLATION" &&
    read_json show "$SCRATCH/pf-show.out" "$SCRATCH/pf-show.txt"; then
    local enrolled
    enrolled=$(field "$SCRATCH/pf-show.txt" enrolled)
    case ",$enrolled," in
    *",$REPO,"*)
      preflight_note enrolment "$REPO is already enrolled in $INSTALLATION; the enrolment's own step converges what is there" ;;
    *)
      preflight_note enrolment "$REPO is not yet enrolled in $INSTALLATION, which is what the enrolment's own step reads" ;;
    esac
  else
    preflight_note enrolment "the enrolment of $REPO could not be read (exit ${BOUNDED_STATUS}); this check enrols it itself, so nothing is assumed"
  fi

  # F11's premise, read from the outside: the github-app-key container EXISTS and
  # holds no enabled version. A listing that names no row, or a row with no
  # container, is a repository Terraform has not created the container for; a row
  # that already holds a version has no empty container to read.
  say "preflight: reading $REPO's credentials: fugaro secrets ls --repo $REPO --json"
  if ! run_bounded "$CLI_TIMEOUT" pf-secrets "$FUGARO_BIN" secrets ls --repo "$REPO" --json; then
    preflight_block container \
      "fugaro secrets ls --repo $REPO --json does not answer (exit ${BOUNDED_STATUS}), so F11's empty container cannot be read" \
      "fugaro secrets ls --repo $REPO --json"
  elif ! read_json secrets "$SCRATCH/pf-secrets.out" "$SCRATCH/pf-secrets.txt"; then
    preflight_block container \
      "fugaro secrets ls --repo $REPO --json prints a document this check cannot read" \
      "fugaro secrets ls --repo $REPO --json"
  else
    local row id latest
    row=$(grep -m1 '^secret	github-app-key	' "$SCRATCH/pf-secrets.txt" || true)
    id=$(printf '%s' "$row" | awk -F'\t' '{ print $3 }')
    latest=$(printf '%s' "$row" | awk -F'\t' '{ print $5 }')
    if [ -z "$row" ] || [ -z "$id" ]; then
      preflight_block container \
        "$REPO has no github-app-key container yet, and F11 is about the empty container Terraform creates for an enrolled repository" \
        "fugaro project repos add $REPO --project $INSTALLATION"
    elif [ -n "$latest" ]; then
      preflight_block container \
        "$REPO's github-app-key container ($id) already holds an enabled version ('$latest'), so there is no empty container to read" \
        "gcloud secrets versions disable $latest --secret=$id"
    else
      preflight_note container "$REPO's github-app-key container ($id) exists and holds no enabled version, which is F11's premise"
    fi
  fi
}

# ------------------------------------------------------------------ the steps
# step_checkout: the throwaway checkout and the installation it belongs to. The
# enrolment's own step 6 converges the repository's root from a checkout of it
# (`add` refuses without one), so the checkout is where every command below is run
# from: a fresh clone of the sandbox repository, in this check's scratch
# directory, which is why nothing of the owner's working tree is touched.
step_checkout() {
  if [ -e "$CHECKOUT" ]; then
    stop "$CHECKOUT is already there, so this check cannot make its own throwaway checkout from it"
  fi
  say "cloning https://github.com/$REPO.git into $CHECKOUT"
  if ! run_bounded_in "$SCRATCH" "$CLONE_SECONDS" clone "$GIT_BIN" clone --depth 1 "https://github.com/$REPO.git" "$CHECKOUT"; then
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      stop "the clone of $REPO did not finish inside ${CLONE_SECONDS}s and was ended"
    fi
    stop "git clone of $REPO exited $BOUNDED_STATUS: $(last_lines "$SCRATCH/clone.err") — the checkout this check runs from is the repository's own, and nothing was run without one"
  fi
  local head
  head=$(git -C "$CHECKOUT" rev-parse --short HEAD 2>/dev/null || true)
  fact checkout PASS "https://github.com/$REPO.git cloned at ${head:-unknown} into a throwaway checkout in this check's scratch directory (nothing of your own working tree was read or changed)"

  # doctor exits 1 when it finds a problem and still prints its document, and the
  # state this check builds on purpose (an empty github-app-key container, step 2)
  # is a problem doctor reports: an exit 1 with a readable document is an
  # answer, anything else (a timeout, another status, nothing readable) is not.
  if ! run_bounded "$CLI_TIMEOUT" doctor "$FUGARO_BIN" doctor --json; then
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 1 ] || ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
      unmeasured installation "the checkout's own 'fugaro doctor --json' exited $BOUNDED_STATUS, so the installation this check is aimed at could not be read: $(last_lines "$SCRATCH/doctor.err")"
      stop "the checkout's own doctor did not answer, so this check does not know which installation it is looking at"
    fi
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured installation "'fugaro doctor --json' printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    stop "the checkout's doctor printed a document this check could not read"
  fi
  DOCTOR_INSTALL=$(field "$SCRATCH/doctor.txt" install)
  local doctor_project
  doctor_project=$(field "$SCRATCH/doctor.txt" project)
  REGION=$(field "$SCRATCH/doctor.txt" region)
  if [ -z "$doctor_project" ]; then
    unmeasured installation "the checkout's doctor names no GCP project, so the registry, the secrets and the runs bucket this check reads from the outside have no project to be read from"
    stop "the checkout's doctor names no GCP project"
  fi
  if [ -n "$GCP_PROJECT" ] && [ "$GCP_PROJECT" != "$doctor_project" ]; then
    stop "this check was given --gcp-project $GCP_PROJECT and the checkout's own doctor reads $doctor_project: the reads below would be about another project than the installation's"
  fi
  GCP_PROJECT=$doctor_project
  RUNS_BUCKET=${RUNS_BUCKET:-gs://fugaro-runs-$GCP_PROJECT}
  if [ -n "$DOCTOR_INSTALL" ] && [ "$DOCTOR_INSTALL" != "$INSTALLATION" ]; then
    unmeasured installation "the checkout's doctor reads the installation '${DOCTOR_INSTALL}' and this check was given --installation $INSTALLATION: the rows below are about the installation the checkout belongs to, and this check measures the one you work in rather than a name on the command line"
    stop "the checkout belongs to the installation '${DOCTOR_INSTALL}' and --installation names $INSTALLATION"
  fi
  fact installation PASS "$INSTALLATION in $GCP_PROJECT${REGION:+, region $REGION} (the installation the checkout's own doctor reads; the runs bucket is $RUNS_BUCKET)"
}

# step_enrol: the order itself. `add` prints the six steps it is about to run,
# names the first image build as billable and NOT part of it, asks for the
# project's name, and then converges each one. It talks to the person at the
# terminal, so its output goes to your stderr (descriptor 4) and a copy stays
# here for the rows.
step_enrol() {
  say "enrolling $REPO in project $INSTALLATION — the CLI asks you for the project's name at your terminal"
  run_tee enrol "$FUGARO_BIN" project repos add "$REPO" --project "$INSTALLATION" || true
  local status=$BOUNDED_STATUS out="$SCRATCH/enrol.out"
  if [ "$status" != 0 ]; then
    stop "'fugaro project repos add $REPO --project $INSTALLATION' exited $status: $(last_lines "$out") — the enrolment is the whole of this check's first step, and the rest of the block would be about a repository nothing enrolled"
  fi
  local numbered want missing=""
  numbered=$(grep -c '^  [0-9]\. ' "$out" 2>/dev/null || true)
  for want in \
    "enrolling $CANONICAL in project $INSTALLATION: this will change" \
    "  1. the repository's own fugaro.yaml: read for its workflows (" \
    "  2. project config: " \
    "  3. project config: " \
    "  4. allow-list: " \
    "  5. publish: the policy node config/policy/shared" \
    "  6. job: the repository's job set and its roles/run.invoker grant on the credentials service" \
    "the first image build is not part of this: it is billable and it waits for " \
    "⚠ CONFIRM (project $INSTALLATION): " \
    "enrolled $CANONICAL in project $INSTALLATION: "; do
    if ! grep -qF -- "$want" "$out" 2>/dev/null; then missing="$missing [$want]"; fi
  done
  if [ -n "$missing" ]; then
    stop "the enrolment's own output is missing a line the plan's step (1) names:$missing — read $out, and read the six steps the command prints against what it then reports"
  fi
  if [ "$numbered" != 6 ]; then
    stop "the enrolment printed $numbered numbered steps and the order has six ($out): a converge that prints a number of steps other than six, or runs a number other than the six it printed, is the reading this row exists for"
  fi
  if ! grep -q 'fugaro image build' "$out" 2>/dev/null; then
    stop "the enrolment does not name 'fugaro image build' as the one step it does NOT do: the build is billable and it is deliberately outside this command ($out)"
  fi
  fact enrol PASS "the six steps were printed in the order the plan fixes — fugaro.yaml, the local config entry, the image map, the allow-list, the publish and the job with its roles/run.invoker grant — the first image build was named as billable and NOT part of this, the project's name was confirmed and the report closed with 'enrolled $CANONICAL in project $INSTALLATION: ' (all of it in $out; D2400's order read from its own output)"
}

# step_empty_container: F11 — the failure this whole order answers. A container
# Terraform created holds no version, the readiness gate then plans no job for a
# workflow mounting it, and the enrolment reads as complete while the repository
# has no job. The premise is asserted FIRST: if a version is already stored there
# is no empty container, and this check says so instead of pretending to read one.
step_empty_container() {
  say "reading $REPO's github-app-key container before anything fills it"
  read_secret_rows || return 0
  local row id latest
  row=$(grep -m1 '^secret	github-app-key	' "$SCRATCH/secrets-before.txt" || true)
  if [ -z "$row" ]; then
    unmeasured empty-container "the listing of $REPO's credentials names no github-app-key row at all: F11's failure is a container that EXISTS and holds no version, so this check has nothing to read. What the listing answered is in $SCRATCH/secrets-before.out"
    stop "no github-app-key row is listed for $REPO, so the empty container this check is about could not be read"
  fi
  id=$(printf '%s' "$row" | awk -F'\t' '{ print $3 }')
  latest=$(printf '%s' "$row" | awk -F'\t' '{ print $5 }')
  if [ -z "$id" ]; then
    unmeasured empty-container "$REPO has no github-app-key container at all — the row is the required-name row a listing writes when nothing is stored, and F11 is about the container Terraform creates and leaves empty. Run 'fugaro project repos add $REPO' so the container exists, then run the check again on an installation whose container was never filled"
    stop "no github-app-key container exists for $REPO, so there is no empty container to read"
  fi
  if [ -n "$latest" ]; then
    unmeasured empty-container "$REPO's github-app-key container ($id) already holds an enabled version ('$latest'): F11's failure is an EMPTY container, and this installation has none, so step (2) cannot be measured. Use a throwaway installation whose container was never filled, or disable the version, and run the check again"
    stop "$REPO's github-app-key container already holds an enabled version, so there is no empty container to read"
  fi
  SECRET_ID=$id
  fact empty-container PASS "F11's premise held: $REPO's github-app-key container ($SECRET_ID) exists and holds no enabled version, which is the state Terraform leaves and the readiness gate plans no job for"

  say "reading 'fugaro project repos status $REPO' with the container empty"
  run_bounded "$CLI_TIMEOUT" status-empty "$FUGARO_BIN" project repos status "$REPO_CANON" --project "$INSTALLATION" || true
  local status=$BOUNDED_STATUS out="$SCRATCH/status-empty.out"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured status-exit "'fugaro project repos status $REPO' did not answer inside ${CLI_TIMEOUT}s and was ended"
  elif [ "$status" = 0 ]; then
    fact status-exit FAIL "'fugaro project repos status $REPO' exited 0 with the github-app-key container empty: D2412's rule is that the exit status is nonzero when a row that blocks a launch fails, and an empty credential container is the row that blocks every run of a workflow mounting it. What it printed is in $out"
  else
    fact status-exit PASS "'fugaro project repos status $REPO' exited $status (nonzero) with the container empty, which is checksFail's own rule read from the outside (D2412)"
  fi
  local missing=""
  if ! grep -q 'credential-version' "$out" 2>/dev/null; then missing="$missing [the credential-version row]"; fi
  if ! grep -q 'holds no enabled version' "$out" 2>/dev/null; then missing="$missing [that the container holds no enabled version]"; fi
  if [ -n "$missing" ]; then
    fact status-screen FAIL "the status screen with the container empty does not name:$missing — F11 is exactly this: the screen has to name the row and say what it found. What it printed is in $out"
  else
    fact status-screen PASS "the status screen names the credential-version row of $REPO and says the github-app-key container holds no enabled version (the row is clipped at 160 columns, which is why the fix below is read from the document; the whole screen is in $out)"
  fi

  # The fix itself is read from the same command's document: the screen clips a
  # row at 160 columns and the fix is the tail of this one, so the document is
  # where it survives whole — and it is the document the console reads. The
  # document's own exit status is the same failing one the screen's read gave
  # above, so it is not read again here: what is read is the row.
  run_bounded "$CLI_TIMEOUT" status-empty-json "$FUGARO_BIN" project repos status "$REPO_CANON" --json --project "$INSTALLATION" || true
  if ! read_json status "$SCRATCH/status-empty-json.out" "$SCRATCH/status-empty-json.txt"; then
    unmeasured status-fix "the status document is one this check could not read: what it printed is in $SCRATCH/status-empty-json.out"
    stop "the status command printed a document this check could not read"
  fi
  local srow ok fix
  srow=$(status_row "$SCRATCH/status-empty-json.txt" credential-version)
  if [ -z "$srow" ]; then
    fact status-fix FAIL "the status document holds no credential-version row for $REPO: F11's whole point is that the row exists and is where the operator looks. What the document holds is in $SCRATCH/status-empty-json.txt"
  else
    ok=$(row_field "$srow" 4)
    fix=$(row_field "$srow" 6)
    local bad=""
    if [ "$ok" != false ]; then bad="$bad [the row reads ok]"; fi
    if ! contains "$fix" "secrets set github-app-key --repo $REPO"; then bad="$bad [the fix it names is '$fix']"; fi
    if [ -n "$bad" ]; then
      fact status-fix FAIL "the status document's credential-version row is not F11's:$bad — the empty container has to be a failing row whose fix is 'fugaro secrets set github-app-key --repo $REPO', and a row that says what is wrong without what to type is the state the empty container already put the operator in. The document is in $SCRATCH/status-empty-json.out"
    else
      fact status-fix PASS "status names the credential-version row of $REPO as failing and names 'fugaro secrets set github-app-key --repo $REPO' as its fix — read from its own --json document, where the fix survives whole (the screen clips the row at 160 columns; F11 is D2411's answer to that failure)"
    fi
  fi
}

# read_secret_rows: the listing of the repository's credentials, read with
# metadata only (a name, a container id, a scope, the newest enabled version's
# name and a count — never a value).
read_secret_rows() {
  if ! run_bounded "$CLI_TIMEOUT" secrets-before "$FUGARO_BIN" secrets ls --repo "$REPO" --json; then
    unmeasured empty-container "'fugaro secrets ls --repo $REPO --json' exited $BOUNDED_STATUS: $(last_lines "$SCRATCH/secrets-before.err")"
    stop "the listing of $REPO's credentials did not answer"
  fi
  if ! read_json secrets "$SCRATCH/secrets-before.out" "$SCRATCH/secrets-before.txt"; then
    unmeasured empty-container "'fugaro secrets ls --repo $REPO --json' printed a document this check could not read: what it printed is in $SCRATCH/secrets-before.out"
    stop "the listing of $REPO's credentials printed a document this check could not read"
  fi
}

# step_fill: store the App's private key in the repository's own container — by
# REDIRECTION, so the value reaches neither a command line nor the block — then
# converge again and read the same screen, which now has to be green and exit 0.
step_fill() {
  say "storing the App's private key in $REPO's github-app-key container (fed on stdin from $APP_KEY_FILE, never on a command line)"
  local saved_stdin=$BOUNDED_STDIN
  BOUNDED_STDIN=$APP_KEY_FILE
  run_bounded "$CLI_TIMEOUT" fill "$FUGARO_BIN" secrets set github-app-key --repo "$REPO" || true
  BOUNDED_STDIN=$saved_stdin
  if [ "$BOUNDED_STATUS" != 0 ]; then
    stop "'fugaro secrets set github-app-key --repo $REPO' exited $BOUNDED_STATUS: $(last_lines "$SCRATCH/fill.err") — the container stays empty and every row after this one would be about the failure step (2) already read"
  fi
  if read_secret_rows_after; then
    local row latest
    row=$(grep -m1 '^secret	github-app-key	' "$SCRATCH/secrets-after.txt" || true)
    latest=$(printf '%s' "$row" | awk -F'\t' '{ print $5 }')
    if [ -z "$latest" ]; then
      fact filled FAIL "'fugaro secrets set github-app-key --repo $REPO' exited 0 and the listing still names no enabled version for the container ($SECRET_ID): the value was stored for another scope, or the container is another one. What the listing answered is in $SCRATCH/secrets-after.out"
    else
      SECRET_LATEST=$latest
      fact filled PASS "$REPO's github-app-key container now holds an enabled version ('$SECRET_LATEST'), stored from $APP_KEY_FILE by redirection: the value reached neither a command line nor this block"
    fi
  fi

  say "converging again, then reading 'fugaro project repos status $REPO' with the container filled"
  run_tee enrol-again "$FUGARO_BIN" project repos add "$REPO" --project "$INSTALLATION" || true
  if [ "$BOUNDED_STATUS" != 0 ]; then
    stop "'fugaro project repos add $REPO --project $INSTALLATION' run again after the fill exited $BOUNDED_STATUS: $(last_lines "$SCRATCH/enrol-again.out") — the plan's step (3) is the same converge over a filled container, and an enrolment that only works once is not resumable"
  fi
  if ! grep -qF -- "enrolled $CANONICAL in project $INSTALLATION: " "$SCRATCH/enrol-again.out" 2>/dev/null; then
    stop "the second converge printed no 'enrolled $CANONICAL in project $INSTALLATION: ' closing line: $(last_lines "$SCRATCH/enrol-again.out")"
  fi
  run_bounded "$CLI_TIMEOUT" status-green "$FUGARO_BIN" project repos status "$REPO_CANON" --project "$INSTALLATION" || true
  local status=$BOUNDED_STATUS out="$SCRATCH/status-green.out"
  local bad=""
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then bad="$bad [it did not answer inside ${CLI_TIMEOUT}s]"; fi
  if [ "$status" != 0 ]; then bad="$bad [exit $status]"; fi
  if grep -q 'holds no enabled version' "$out" 2>/dev/null; then bad="$bad [the credential row still reads empty]"; fi
  if ! grep -q 'rows checked' "$out" 2>/dev/null; then bad="$bad [no 'rows checked' line]"; fi
  if [ -n "$bad" ]; then
    fact status-green FAIL "'fugaro project repos status $REPO' after the fill is not green:$bad — the same read that failed in step (2) has to pass here, and its exit status has to become zero (D2412). What it printed is in $out"
  else
    fact status-green PASS "'fugaro project repos status $REPO' exits 0 and every row passes once the container holds a version: the row step (2) read as failing is the same row read green (the whole screen is in $out)"
  fi
}

read_secret_rows_after() {
  if ! run_bounded "$CLI_TIMEOUT" secrets-after "$FUGARO_BIN" secrets ls --repo "$REPO" --json; then
    fact filled FAIL "'fugaro secrets ls --repo $REPO --json' exited $BOUNDED_STATUS after the fill: $(last_lines "$SCRATCH/secrets-after.err")"
    return 1
  fi
  if ! read_json secrets "$SCRATCH/secrets-after.out" "$SCRATCH/secrets-after.txt"; then
    fact filled FAIL "'fugaro secrets ls --repo $REPO --json' printed a document this check could not read after the fill: what it printed is in $SCRATCH/secrets-after.out"
    return 1
  fi
  return 0
}

# launch_run KEY: one run, started from the checkout with the brief this check
# was given (or the throwaway one it writes itself), with its own `--json`
# document naming the run. Sets LAUNCHED_ID.
launch_run() { # launch_run KEY
  local key=$1
  LAUNCHED_ID=""
  say "launching a run of $REPO — this is billable, on the installation's own budget"
  run_bounded "$LAUNCH_SECONDS" "$key" "$FUGARO_BIN" run "$TASK_FILE" --repo "$REPO" --json --project "$INSTALLATION" ${BASE:+--ref "$BASE"} || true
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured "$key" "the launch of the run did not finish inside ${LAUNCH_SECONDS}s and was ended"
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured "$key" "'fugaro run --json' exited $BOUNDED_STATUS: $(last_lines "$SCRATCH/$key.err") — what it printed is in $SCRATCH/$key.out"
    return 1
  fi
  if ! read_json launch "$SCRATCH/$key.out" "$SCRATCH/$key.txt"; then
    unmeasured "$key" "'fugaro run --json' printed a document this check could not read: what it printed is in $SCRATCH/$key.out"
    return 1
  fi
  LAUNCHED_ID=$(field "$SCRATCH/$key.txt" run_id)
  if [ -z "$LAUNCHED_ID" ]; then
    unmeasured "$key" "'fugaro run --json' named no run: a launch this check cannot follow is one it cannot read the removal under. What it printed is in $SCRATCH/$key.out"
    return 1
  fi
  return 0
}

# read_runs: the project-wide listing — every repository, because after the
# removal this machine's config no longer lists the repository and a listing
# narrowed to it would answer nothing. Leaves the rows in $SCRATCH/runs.txt.
read_runs() {
  if ! run_bounded "$CLI_TIMEOUT" runs "$FUGARO_BIN" runs ls --all --json --limit "$LOG_LIMIT"; then
    return 1
  fi
  read_json rows "$SCRATCH/runs.out" "$SCRATCH/runs.txt"
}

# step_run: the plan's step (4) — one run, launched and read back from the
# project's own listing. It is not waited for here: step (5) is the removal under
# the OTHER run, and this one only has to be real.
step_run() {
  if ! launch_run run1; then
    unmeasured run-started "the first run could not be launched, so nothing after this step has a subject"
    stop "the first run could not be launched"
  fi
  RUN1_ID=$LAUNCHED_ID
  local row=""
  if read_runs; then row=$(row_of "$SCRATCH/runs.txt" "$RUN1_ID"); fi
  if [ -z "$row" ]; then
    fact run-started FAIL "'fugaro run --json' named the run $RUN1_ID and the project's listing holds no row for it (what the listing answered is in $SCRATCH/runs.txt): a launch whose run the listing does not know is a run that did not start"
    return 0
  fi
  RUN1_REF=$(row_field "$row" 3)
  local status stage
  status=$(row_field "$row" 4)
  stage=$(row_field "$row" 6)
  fact run-started PASS "run $RUN1_ID started and the project's listing holds its row (status '${status:-unknown}'${stage:+, stage $stage}; its own ref is '${RUN1_REF:-unknown}', the bucket key its history is read at in step (6))"
}

# step_removal: the plan's step (5). A second run is launched and WATCHED until
# it is going, the repository is removed from the installation while it goes, and
# the removal has to ask the strict confirmation (D2407) and then the run has to
# end anyway.
step_removal() {
  if ! launch_run run2; then
    unmeasured strict-confirmation "the second run could not be launched, so the removal could not be run under a run in flight"
    stop "the second run could not be launched"
  fi
  RUN2_ID=$LAUNCHED_ID
  local waited=0 row="" status="" terminal=""
  while [ "$waited" -lt "$RUN_SECONDS" ]; do
    if read_runs; then
      row=$(row_of "$SCRATCH/runs.txt" "$RUN2_ID")
      terminal=$(row_field "$row" 5)
      status=$(row_field "$row" 4)
      if [ -n "$row" ] && [ "$terminal" = false ]; then break; fi
      if [ "$terminal" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ -z "$row" ]; then
    unmeasured strict-confirmation "the run $RUN2_ID is not in the project's listing, so this check cannot say it was in flight when the removal ran (what the listing answered is in $SCRATCH/runs.txt)"
    stop "the second run's own row was never read, so the removal was not run under it"
  fi
  if [ "$terminal" = true ]; then
    unmeasured strict-confirmation "the run $RUN2_ID ended ('${status:-unknown}') before the removal could be run while it was going: the strict confirmation is the row this check exists for, and an installation where a run ends this fast cannot measure it. Run the check again"
    stop "the second run ended before the removal, so the strict confirmation never had a run in flight to fire under"
  fi

  # The strict confirmation is read from its REFUSAL, and that refusal is the
  # only place it can be read from: at a terminal this command prints the
  # question and nothing that names the strictness, while with no terminal at
  # all it refuses and says why. That refusal is D2407 itself — the sentence
  # saying the typed project name is the only thing that confirms this one while
  # a run is in flight — and it is also the proof that nothing on the command
  # line stands in for the typing (there is no flag here that could; F5/D2403
  # took --yes away with the group's own edit). stdin is /dev/null for it, so it
  # cannot wait for an answer, and its exit is nonzero with nothing written: the
  # question comes after every read and before the first write.
  say "reading the strict confirmation: the same removal, with no terminal at all, has to refuse it"
  run_bounded "$CLI_TIMEOUT" rm-strict "$FUGARO_BIN" project repos rm "$REPO_CANON" --project "$INSTALLATION" || true
  local status_strict=$BOUNDED_STATUS
  local out="$SCRATCH/rm-strict.txt"
  cat "$SCRATCH/rm-strict.out" "$SCRATCH/rm-strict.err" >"$out" 2>/dev/null || true
  local missing=""
  for want in \
    "the typed confirmation of this step needs a real terminal" \
    "typing the project's name is the only thing that confirms this one while a run is in flight" \
    "wait for it (fugaro runs ls), then confirm it at your own terminal" \
    "removes $CANONICAL from the allow-list" \
    "are going on " \
    "it leaves, and nothing here touches:" \
    "the provider's App installation" \
    "the built images" \
    "the secrets and their versions" \
    "the run history in the runs bucket" \
    "every pull request a run opened" \
    "the remote branch" \
    "it keeps " \
    "docs/how-to/offboarding.md"; do
    if ! grep -qF -- "$want" "$out" 2>/dev/null; then missing="$missing [$want]"; fi
  done
  if ! grep -qF -- "$RUN2_ID" "$out" 2>/dev/null; then missing="$missing [the run in flight, $RUN2_ID]"; fi
  if [ "$status_strict" = 0 ]; then
    missing="$missing [the refusal's exit status: it exited 0 with no terminal to confirm at]"
  fi
  if grep -qF -- ": removed" "$out" 2>/dev/null || grep -qF -- "allow_repos: " "$out" 2>/dev/null; then
    missing="$missing [nothing was written before the confirmation — its output already names a store it took away]"
  fi
  if [ -n "$missing" ]; then
    fact strict-confirmation FAIL "the removal, run while $RUN2_ID was going and with no terminal to confirm at, is missing:$missing — D2407's confirmation is strict: it asks for the project's name at your terminal and nothing on the command line answers it, and it names the run it is about. What it printed is in $out"
  else
    fact strict-confirmation PASS "the removal asked the STRICT confirmation — 'typing the project's name is the only thing that confirms this one while a run is in flight; wait for it (fugaro runs ls), then confirm it at your own terminal' — refused with no terminal to type it at, named run $RUN2_ID as the one going on $REPO, and wrote nothing before the question (D2407; the refusal, not the banner, is where the strictness is printed, and it is in $out)"
  fi

  say "removing $REPO from project $INSTALLATION while run $RUN2_ID is going — the CLI asks you for the project's name at your terminal"
  run_tee rm "$FUGARO_BIN" project repos rm "$REPO_CANON" --project "$INSTALLATION" || true
  local status_rm=$BOUNDED_STATUS
  out="$SCRATCH/rm.out"
  missing=""
  for want in \
    "⚠ CONFIRM (project $INSTALLATION): " \
    "Type $INSTALLATION to confirm: " \
    "removes $CANONICAL from the allow-list" \
    "are going on " \
    "it leaves, and nothing here touches:"; do
    if ! grep -qF -- "$want" "$out" 2>/dev/null; then missing="$missing [$want]"; fi
  done
  if ! grep -qF -- "$RUN2_ID" "$out" 2>/dev/null; then missing="$missing [the run in flight, $RUN2_ID]"; fi
  if [ -n "$missing" ]; then
    fact confirmation FAIL "the removal at your terminal is missing:$missing — the question names the project whose name it asks for and the run whose removal it is. What it printed is in $out"
  else
    fact confirmation PASS "the removal at your terminal asked for the project's name ('Type $INSTALLATION to confirm: ') and named run $RUN2_ID as the one going on $REPO, so what you typed is the removal of a repository whose run was in flight (D2407)"
  fi

  if [ "$status_rm" != 0 ]; then
    fact removal FAIL "'fugaro project repos rm $REPO_CANON --project $INSTALLATION' exited $status_rm: $(last_lines "$out") — the removal is the whole of this step, and the six rows after it would be about a repository nothing removed"
  else
    local removed=""
    if grep -qF -- ": removed" "$out" 2>/dev/null; then removed="$removed [this machine's config: the repos: entry and the image map]"; fi
    if grep -qF -- "allow_repos: " "$out" 2>/dev/null; then removed="$removed [the published allow-list]"; fi
    if grep -qF -- "deleted the " "$out" 2>/dev/null; then removed="$removed [the repository's Cloud Run jobs]"; fi
    if [ -z "$removed" ]; then
      fact removal FAIL "the removal exited 0 and its own output names none of the stores it took away (no ': removed', no 'allow_repos: ', no 'deleted the '): what it printed is in $out"
    else
      fact removal PASS "the removal exited 0 and its own report names what it took away —$removed — this machine's config entry and image map, the published allow-list and the repository's Cloud Run jobs (all of it in $out)"
    fi
  fi

  say "waiting for run $RUN2_ID to end — the removal must not have stopped it"
  waited=0
  row=""
  terminal=""
  while [ "$waited" -lt "$RUN_SECONDS" ]; do
    if read_runs; then
      row=$(row_of "$SCRATCH/runs.txt" "$RUN2_ID")
      terminal=$(row_field "$row" 5)
      if [ "$terminal" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$terminal" != true ]; then
    unmeasured run-finished "run $RUN2_ID had not ended after ${RUN_SECONDS}s (its last row is in $SCRATCH/runs.txt): it is a real run and this check does not cancel it. Watch it with 'fugaro watch $RUN2_ID' and read its row again"
    stop "the run under the removal had not ended when this check stopped waiting for it"
  fi
  status=$(row_field "$row" 4)
  local outcome
  outcome=$(row_field "$row" 9)
  RUN2_REF=$(row_field "$row" 3)
  fact run-finished PASS "run $RUN2_ID ended '${status:-unknown}'${outcome:+, outcome $outcome} after the repository was removed from the installation while it was going: D2407 read from the outside — the removal took the repository's job away and the run that was executing it still finished"

  # The first run's own row carries the branch and the pull request the rows
  # below are about, so it is read here rather than assumed.
  local row1=""
  if read_runs; then row1=$(row_of "$SCRATCH/runs.txt" "$RUN1_ID"); fi
  if [ -z "$row1" ]; then
    unmeasured kept-branch "the first run's own row ($RUN1_ID) is not in the project's listing, so the branch and the pull request it opened could not be read"
    return 0
  fi
  RUN1_BRANCH=$(row_field "$row1" 7)
  PR_URL=$(row_field "$row1" 8)
  RUN1_REF=$(row_field "$row1" 3)
}

# step_leaves: the plan's step (6) — the six things `rm` deliberately leaves
# (D2406), each read from the OUTSIDE with the tool that holds it, and then that
# `fugaro update` afterwards writes nothing new.
step_leaves() {
  kept_app
  read_shared_images || true
  kept_image
  kept_secret
  kept_history
  kept_pull_request
  kept_branch
  kept_update
}

kept_app() {
  local out=""
  out=$("$GH_BIN" api "/repos/$REPO/installation" --jq .id 2>/dev/null || true)
  APP_INSTALL=$(printf '%s' "$out" | tr -d ' \n')
  if [ -z "$APP_INSTALL" ]; then
    fact kept-app FAIL "gh api /repos/$REPO/installation answers nothing after the removal: the App's installation is the provider's own and D2406 says the removal never uninstalls it. Read it by hand ('gh api /repos/$REPO/installation') before reading this row as a change on the provider's side"
  else
    fact kept-app PASS "the provider still holds the App's installation on $REPO (installation $APP_INSTALL, read with 'gh api /repos/$REPO/installation' — the one fact only the provider holds, and the reason this check's fakes include gh): rm never uninstalls an App"
  fi
}

# read_shared_images: which built image serves the repository, read from the
# installation's shared-image lock. It is the lock and not the catalog because the
# lock is what records which repositories and jobs an image serves, and it is
# written by the build — an installation that never built the repository's entry
# has no lock entry, and the row says so rather than inventing an image.
read_shared_images() {
  IMAGE_REF=""
  if ! run_bounded "$CLI_TIMEOUT" shared "$FUGARO_BIN" image ls --shared --json; then
    return 1
  fi
  if ! read_json shared "$SCRATCH/shared.out" "$SCRATCH/shared.txt"; then
    return 1
  fi
  IMAGE_REF=$(awk -F'\t' -v repo="$REPO" '$1 == "entry" && ("," $4 ",") ~ ("," repo ",") { print $3; exit }' "$SCRATCH/shared.txt" 2>/dev/null || true)
  [ -n "$IMAGE_REF" ]
}

kept_image() {
  if [ -z "$IMAGE_REF" ]; then
    unmeasured kept-image "no entry of this installation's shared-image lock serves $REPO, so nothing was ever built for it and there is no image for this row to check is still there: 'fugaro image build <entry>' comes first, and the enrolment's own output says the build is not part of it (D2406's second leaf is about an image that exists)"
    return 0
  fi
  local out=""
  out=$("$GCLOUD_BIN" artifacts docker images list "$IMAGE_REF" --project "$GCP_PROJECT" --format='value(package)' 2>/dev/null || true)
  if [ -z "$(printf '%s' "$out" | tr -d ' \n')" ]; then
    fact kept-image FAIL "Artifact Registry lists no image at $IMAGE_REF after the removal: D2406 says Fugaro never deletes an image and 'fugaro image rm' does not either, so an image that is gone is a finding about this installation, not a leaf"
  else
    fact kept-image PASS "the built image $IMAGE_REF (the entry serving $REPO in the shared-image lock) is still in Artifact Registry after the removal: D2406's second leaf, and no command of Fugaro's deletes one"
  fi
}

kept_secret() {
  if [ -z "$SECRET_ID" ]; then
    unmeasured kept-secret "this check never read the github-app-key container's id, so it cannot say the container is still there"
    return 0
  fi
  local describe versions
  describe=$("$GCLOUD_BIN" secrets describe "$SECRET_ID" --project "$GCP_PROJECT" --format='value(name)' 2>/dev/null || true)
  versions=$("$GCLOUD_BIN" secrets versions list "$SECRET_ID" --project "$GCP_PROJECT" --filter='state:ENABLED' --format='value(name)' 2>/dev/null || true)
  if [ -z "$(printf '%s' "$describe" | tr -d ' \n')" ]; then
    fact kept-secret FAIL "Secret Manager no longer holds the container $SECRET_ID after the removal: D2406 says the secrets and their versions are left, and a container that is gone is a finding about this installation"
  elif [ -z "$(printf '%s' "$versions" | tr -d ' \n')" ]; then
    fact kept-secret FAIL "the container $SECRET_ID is still there and holds no ENABLED version: the value stored in step (3) is the point of the leaf, and a version that was disabled is a finding about this installation"
  else
    fact kept-secret PASS "the container $SECRET_ID is still there and still holds an enabled version ('${SECRET_LATEST:-unknown}' was the newest when it was filled; the read is metadata — a name, a state and a count, never a value): D2406's third leaf, read from Secret Manager itself"
  fi
}

kept_history() {
  if [ -z "$RUN1_REF" ]; then
    unmeasured kept-history "the first run's own row names no ref, so this check does not know which objects in the runs bucket are its history"
    return 0
  fi
  local out=""
  out=$("$GCLOUD_BIN" storage ls "$RUNS_BUCKET/runs/$RUN1_REF/" --project "$GCP_PROJECT" 2>/dev/null || true)
  if [ -z "$(printf '%s' "$out" | tr -d ' \n')" ]; then
    fact kept-history FAIL "$RUNS_BUCKET holds nothing under runs/$RUN1_REF/ after the removal: D2406 says the run history is left and deleted only at its own expiry, so history that is gone is a finding about this installation"
  else
    fact kept-history PASS "run $RUN1_ID's own objects are still in $RUNS_BUCKET under runs/$RUN1_REF/ after the removal (D2406's fourth leaf: the history goes at its own expiry and never by this command)"
  fi
}

kept_pull_request() {
  if [ -z "$PR_URL" ]; then
    unmeasured kept-pull-request "the first run's own row names no pull request, so there is none for this row to check is still there: it is the run's own outcome, not a leaf this check moved (its record is under runs/$RUN1_REF/)"
    return 0
  fi
  local state=""
  state=$("$GH_BIN" pr view "$PR_URL" --json state --jq .state 2>/dev/null || true)
  state=$(printf '%s' "$state" | tr -d ' \n')
  if [ -z "$state" ]; then
    fact kept-pull-request FAIL "gh pr view $PR_URL answers nothing after the removal: D2406 says every pull request a run opened is left, and one the provider no longer holds is a finding about this installation"
  else
    fact kept-pull-request PASS "the pull request the run opened is still there ($PR_URL, state $state): D2406's fifth leaf — the removal leaves every pull request a run opened where it is"
  fi
}

kept_branch() {
  if [ -z "$RUN1_BRANCH" ]; then
    unmeasured kept-branch "the first run's own row names no branch, so there is none for this row to check is still there (a run that pushed nothing leaves no branch to leave)"
    return 0
  fi
  local out=""
  out=$("$GIT_BIN" ls-remote --heads "https://github.com/$REPO.git" "$RUN1_BRANCH" 2>/dev/null || true)
  if [ -z "$(printf '%s' "$out" | tr -d ' \n')" ]; then
    fact kept-branch FAIL "the remote no longer holds the branch $RUN1_BRANCH after the removal: D2406's sixth leaf leaves it, and a branch that is gone is a finding about this installation (delete it yourself if you want it gone)"
  else
    fact kept-branch PASS "the remote $REPO still holds the run's branch $RUN1_BRANCH ('git ls-remote --heads'): D2406's sixth leaf, and the one this check's own restore row suggests you take away by hand"
  fi
}

# kept_update: `fugaro update` afterwards writes nothing new. The reading is the
# document's own (it exits 0, fails no stage and leaves nothing for the owner)
# held against the two things an update would rewrite if it thought the repository
# were still enrolled: the published allow-list and the set of repositories the
# installation and this machine agree are enrolled.
kept_update() {
  if ! read_show before; then
    unmeasured kept-update "'fugaro project show --json' did not answer before the update, so this check cannot say the update wrote nothing new"
    return 0
  fi
  local allow_before enrolled_before
  allow_before=$(field "$SCRATCH/show-before.txt" allow_repos)
  enrolled_before=$(field "$SCRATCH/show-before.txt" enrolled)
  say "'fugaro update --json --project $INSTALLATION' after the removal — this is a read of the installation, and the row is about what it writes"
  run_bounded "$CLI_TIMEOUT" update "$FUGARO_BIN" update --json --project "$INSTALLATION" || true
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact kept-update FAIL "'fugaro update --json --project $INSTALLATION' exited $BOUNDED_STATUS after the removal: $(last_lines "$SCRATCH/update.err") — an installation a removal left is one a plain update still converges, so a refusal here is a finding (what it printed is in $SCRATCH/update.out)"
    return 0
  fi
  if ! read_json update "$SCRATCH/update.out" "$SCRATCH/update.txt"; then
    fact kept-update FAIL "'fugaro update --json' printed a document this check could not read: what it printed is in $SCRATCH/update.out"
    return 0
  fi
  local failed left stages
  failed=$(field "$SCRATCH/update.txt" failed)
  left=$(field "$SCRATCH/update.txt" left_for_you)
  stages=$(field "$SCRATCH/update.txt" stages)
  local bad=""
  if [ "$failed" = yes ]; then bad="$bad [a stage failed]"; fi
  if [ -n "$left" ] && [ "$left" != 0 ]; then bad="$bad [$left step(s) left for you, which is a change it wanted to make]"; fi
  if [ -z "$stages" ] || [ "$stages" = 0 ]; then bad="$bad [no stage answered]"; fi
  if ! read_show after; then
    bad="$bad ['fugaro project show --json' did not answer after the update]"
  else
    local allow_after enrolled_after
    allow_after=$(field "$SCRATCH/show-after.txt" allow_repos)
    enrolled_after=$(field "$SCRATCH/show-after.txt" enrolled)
    if [ "$allow_after" != "$allow_before" ]; then bad="$bad [the allow-list moved: '$allow_before' → '$allow_after']"; fi
    if [ "$enrolled_after" != "$enrolled_before" ]; then bad="$bad [the enrolled set moved: '$enrolled_before' → '$enrolled_after']"; fi
  fi
  if [ -n "$bad" ]; then
    fact kept-update FAIL "'fugaro update --json --project $INSTALLATION' run after the removal is not a no-op:$bad — a repository that is no longer enrolled is one the update must not try to write back, and the stages it applied are in $SCRATCH/update.out"
  else
    fact kept-update PASS "'fugaro update --json --project $INSTALLATION' exits 0 after the removal, fails no stage and leaves nothing for you, and the allow-list ('${allow_before:-<empty>}') and the enrolled set ('${enrolled_before:-<empty>}') it reads afterwards are the ones it read before: the update writes nothing new about a repository the removal took away"
  fi
}

read_show() { # read_show before|after
  local key="show-$1"
  if ! run_bounded "$CLI_TIMEOUT" "$key" "$FUGARO_BIN" project show --json --project "$INSTALLATION"; then
    return 1
  fi
  read_json show "$SCRATCH/$key.out" "$SCRATCH/$key.txt"
}

plan() {
  say "CHECK 670 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [git, read-only] clone https://github.com/${REPO}.git into a throwaway checkout"
  say "  [read] fugaro doctor --json in that checkout — the installation, its GCP project and its region"
  say "  [cloud, WRITE] fugaro project repos add ${REPO} --project ${INSTALLATION} — the six steps: the repository's fugaro.yaml, this machine's config entry, the image map, the published allow-list, the policy node, and the repository's job set with its invoker grant; it also converges and deploys the repository's own root. It asks you for the project's name"
  say "  [read] fugaro secrets ls --repo ${REPO} --json and fugaro project repos status ${REPO} (and --json) — F11's empty github-app-key container, the row that names it, the fix it names, and the nonzero exit"
  say "  [cloud, WRITE] fugaro secrets set github-app-key --repo ${REPO} < ${APP_KEY_FILE} — the fill, by redirection"
  say "  [cloud, WRITE] fugaro project repos add ${REPO} again, then status again — the row green and the exit 0"
  say "  [cloud, WRITE, BILLABLE] fugaro run <brief> --repo ${REPO} --json — the first run"
  say "  [cloud, WRITE, BILLABLE] a second run, watched until it is going"
  say "  [cloud, WRITE] fugaro project repos rm ${REPO} --project ${INSTALLATION} while it goes — the strict confirmation, the removal, and the run finishing anyway"
  say "  [read] gh api /repos/${REPO}/installation, fugaro image ls --shared --json then gcloud artifacts docker images list, gcloud secrets describe/versions list, gcloud storage ls ${RUNS_BUCKET}/runs/<ref>/, gh pr view and git ls-remote --heads — the six things rm leaves"
  say "  [cloud, read-mostly] fugaro project show --json, fugaro update --json --project ${INSTALLATION}, project show again — the update writes nothing new"
  fact dry-run PASS "the plan was printed and nothing was run: no clone, no converge, no secret, no run, no removal, no update, and no cloud tool was called"
}

main() {
  need_tools
  FUGARO_BIN=$(command -v fugaro || true)
  if [ -z "$FUGARO_BIN" ]; then
    die "Check 670 needs 'fugaro' on PATH: the check drives the enrolment, the four verbs and the update YOU have installed, and it is not there"
  fi
  GIT_BIN=$(command -v git)
  GCLOUD_BIN=$(command -v gcloud)
  GH_BIN=$(command -v gh)
  CANONICAL="github/$REPO"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_inputs
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -n "$PREFLIGHT" ]; then
    step_preflight
    preflight_report
  fi
  if [ -z "$TASK_FILE" ]; then
    TASK_FILE="$SCRATCH/task.md"
    cat >"$TASK_FILE" <<'TASK'
# Task: check670 sandbox run

Reply with one line saying which repository you are in. Change nothing else: this
run exists only so that Check 670 has two runs to launch and one to remove the
repository under.
TASK
  fi
  step_checkout
  step_enrol
  step_empty_container
  step_fill
  step_run
  step_removal
  step_leaves
  fact restore PASS "the enrolment is undone — $REPO is no longer enrolled in $INSTALLATION — and what is left is what rm leaves by design: the App's installation (${APP_INSTALL:-unknown}), the built image (${IMAGE_REF:-none built}), the container $SECRET_ID and its version (${SECRET_LATEST:-unknown}), the run history, the pull request (${PR_URL:-none opened}) and the branch (${RUN1_BRANCH:-none pushed}). The scratch directory and the throwaway checkout go with the exit trap; take the branch and the pull request away by hand ('git push <remote> --delete $RUN1_BRANCH' and 'gh pr close $PR_URL') if you do not want them, and remember that uninstalling the App on $REPO is a person's step on the provider's side"
}

# ---------------------------------------------------------------------- inputs
DRY_RUN_CLI=""
PREFLIGHT_CLI=""
INSTALLATION_CLI=""
REPO_CLI=""
APP_KEY_CLI=""
TASK_CLI=""
BASE_CLI=""
GCP_PROJECT_CLI=""
USAGE="check670.sh --installation <name> --app-key-file <path to the App's .pem> [--repo <owner/name>] [--task <brief>] [--base <branch>] [--gcp-project <id>] [--production] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --installation)
    [ $# -ge 2 ] || die "--installation needs a name. usage: $USAGE"
    INSTALLATION_CLI=$2
    shift 2
    ;;
  --repo)
    [ $# -ge 2 ] || die "--repo needs owner/name. usage: $USAGE"
    REPO_CLI=$2
    shift 2
    ;;
  --app-key-file)
    [ $# -ge 2 ] || die "--app-key-file needs a path. usage: $USAGE"
    APP_KEY_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs a path. usage: $USAGE"
    TASK_CLI=$2
    shift 2
    ;;
  --base)
    [ $# -ge 2 ] || die "--base needs a branch. usage: $USAGE"
    BASE_CLI=$2
    shift 2
    ;;
  --gcp-project)
    [ $# -ge 2 ] || die "--gcp-project needs a project id. usage: $USAGE"
    GCP_PROJECT_CLI=$2
    shift 2
    ;;
  --production)
    PRODUCTION=1
    shift
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  --preflight)
    PREFLIGHT_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    die "unknown argument '$1'. usage: $USAGE"
    ;;
  esac
done

INSTALLATION=${CHECK670_INSTALLATION:-${INSTALLATION_CLI:-}}
REPO=${CHECK670_REPO:-${REPO_CLI:-dimipaun/fugarosandbox}}
case "$REPO" in */*/*) REPO_CANON=$REPO ;; *) REPO_CANON=github/$REPO ;; esac
APP_KEY_FILE=${CHECK670_APP_KEY_FILE:-${APP_KEY_CLI:-}}
TASK_FILE=${CHECK670_TASK:-${TASK_CLI:-}}
BASE=${CHECK670_BASE:-${BASE_CLI:-}}
GCP_PROJECT=${CHECK670_GCP_PROJECT:-${GCP_PROJECT_CLI:-}}
RUNS_BUCKET=${CHECK670_RUNS_BUCKET:-}
PRODUCTION=${CHECK670_PRODUCTION:-${PRODUCTION:-}}
DRY_RUN=${CHECK670_DRY_RUN:-${DRY_RUN_CLI:-}}
PREFLIGHT=${CHECK670_PREFLIGHT:-${PREFLIGHT_CLI:-}}
PREFLIGHT_MODE=no
[ -z "$PREFLIGHT" ] || PREFLIGHT_MODE=yes
KEEP_SCRATCH=${CHECK670_KEEP_SCRATCH:-}
RUN_SECONDS=${CHECK670_RUN_SECONDS:-2400}
POLL_SECONDS=${CHECK670_POLL_SECONDS:-20}
LAUNCH_SECONDS=${CHECK670_LAUNCH_SECONDS:-300}
CLI_TIMEOUT=${CHECK670_CLI_TIMEOUT_SECONDS:-300}
CLONE_SECONDS=${CHECK670_CLONE_SECONDS:-300}
LOG_LIMIT=${CHECK670_LOG_LIMIT:-50}

if [ -z "$INSTALLATION" ]; then
  die "Check 670 needs --installation: the name of the throwaway installation this check enrols into and removes from (fugaro doctor --json's own project.name, not the GCP project id). Usage: $USAGE"
fi
if [ -z "$APP_KEY_FILE" ]; then
  die "Check 670 needs --app-key-file: the GitHub App's private key (.pem), which step (3) redirects into 'fugaro secrets set github-app-key'. Write it to a file of its own at mode 0600. Usage: $USAGE"
fi
case "$RUN_SECONDS$POLL_SECONDS$LAUNCH_SECONDS$CLI_TIMEOUT$CLONE_SECONDS$LOG_LIMIT" in
*[!0-9]*) die "the time bounds and the listing limit this check reads have to be whole numbers" ;;
esac
if [ "$POLL_SECONDS" -lt 1 ]; then
  die "CHECK670_POLL_SECONDS is ${POLL_SECONDS}: this check polls the run listing for a run to go and to end, and a zero interval would spin on it"
fi
# Absolute, before anything changes directory: every command below is run from
# the checkout, and a relative path would name another file there.
case "$APP_KEY_FILE" in
/*) ;;
*) APP_KEY_FILE="$PWD/$APP_KEY_FILE" ;;
esac
case "$TASK_FILE" in
"" | /*) ;;
*) TASK_FILE="$PWD/$TASK_FILE" ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
