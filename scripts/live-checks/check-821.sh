#!/usr/bin/env bash
# Check 821 — `fugaro update` on the dogfood installation with a run in flight:
# the locations record it converges, the plan that follows it, and the run it
# left alone.
#
# Plan task LP13 of docs/location-preference/implementation-plan.md writes this
# check, with Check 820. What it settles is read from the outside and nowhere
# else:
#
#   * **U9** — whether the pinned provider's `ignore_changes` holds, read the one
#     way a live installation answers it: a `fugaro update --plan-only` after the
#     converge must plan no change to any root at all. A plan that creates,
#     updates, deletes or replaces anything on an installation that has just
#     converged is U9 answering no, and the row names the counts;
#   * the installation's locations record (LP12, D3087) on the dogfood
#     installation: after the update it reads `us-east5` for the Firestore
#     database, `us-central1` for the Realtime Database, Google's automatic
#     replication for the secret containers and `global` for the run log bucket —
#     the four things that installation really has, which is the record's own
#     reason to exist;
#   * **the update in flight** (docs/design/update-in-flight.md, the owner's
#     requirement of 2026-11-09): a run launched BEFORE the update finishes, and
#     every write it makes still lands. This check launches a real run of the
#     checkout, waits until its record reads `running`, converges the
#     installation while that run is live, and then follows the run to its
#     terminal status. The run's records are read back from the runs bucket
#     afterwards: a run whose record is gone, or is not what it was, is the
#     finding this row exists for;
#   * **nothing is asked** (D3090, docs/no-prompt-policy T7): the update runs
#     with its stdin on /dev/null and its streams captured, under a deadline, and
#     not one line of its output may be a question. This is Check 380's reading
#     of the same rule on this installation, taken again here because 821 is
#     already running that update.
#
# What it measures, row by row:
#
#   * premise: you are in a Fugaro checkout whose `fugaro.yaml` is valid, the
#     local config in force is the dogfood installation (region `us-east5` — the
#     one installation this check is about; another region stops it by name), and
#     the installation's run history is readable (`fugaro runs ls --json`);
#   * run: ONE throwaway run of that checkout, launched with `fugaro run` before
#     the installation is converged, under a title and a task file this check
#     writes. The task asks for a report and changes no file: the run exists to
#     be in flight, not to land a change. The row waits until `fugaro runs ls`
#     shows it `running`, which is what makes the update below an update with a
#     run in flight;
#   * update: the real `fugaro update --project <name>`, with its stdin on
#     /dev/null, its stdout and stderr captured, and a deadline (TERM, then KILL).
#     Exit 0 is a converge. Exit 1 is a refusal OR a step left for you: the row
#     tells them apart by the CLI's own sentence ("N step(s) left for you") — a
#     step left for you is UNVERIFIED and names the line to run, because only you
#     can run it, and a refusal without it is FAIL;
#   * asked: every line the update printed, read for the shapes a question leaves
#     behind ([y/N] in any casing, (y/n), (yes/no), a `to confirm:` left open, an
#     enter-prompt, a question left waiting on a blank). One is FAIL;
#   * record: `fugaro doctor --json`'s locations row, which is the record's only
#     reader (R5): the four things above must read what the dogfood installation
#     really has, each named;
#   * plan: `fugaro update --project <name> --plan-only --json` immediately after
#     the converge. Every count must be zero. A plan that plans a change is FAIL
#     (U9), one that only imports is UNVERIFIED (nothing in the cloud changes,
#     but the plan is not empty);
#   * inflight: the run launched in the `run` row, followed with
#     `fugaro runs ls --json` until its record is terminal. It has to still be
#     there, its status has to be one a run reaches (`succeeded` above all), and
#     its branch, pull request and outcome have to be readable. A run that was
#     already terminal before the update started is UNVERIFIED: nothing was in
#     flight across it, so the row is no answer;
#   * restore: the scratch directory, removed from the exit trap. What this check
#     created is said rather than assumed: the launched run's branch and pull
#     request stay on the repository (this check removes nothing from a
#     repository), and the update wrote the installation's own config and cloud
#     state because that is what an update is.
#
# What it creates: ONE throwaway run of the checkout (**billable**: its model
# calls are real and the installation's own budget governs them), its branch and
# its pull request, which stay on the repository — this check holds no
# credential and cannot remove them, so their rows are yours to settle. And it
# converges the installation, which is a real `fugaro update`: the plan it makes
# is read afterwards, and nothing it plans may be a change. It writes nothing
# else: no cloud resource of its own, no record, no object and no node.
#
# Usage:      bash scripts/live-checks/check-821.sh
#             bash scripts/live-checks/check-821.sh --project <name> --repo <checkout>
#             bash scripts/live-checks/check-821.sh --dry-run
#             bash scripts/live-checks/check-821.sh --help
# Needs:      bash, mktemp, python3 (every `--json` document is read with it) and
#             fugaro (the installed release — the update this check runs and the
#             run it launches are the ones you have). No gcloud and no curl: every
#             reading here is the CLI's own.
# Where:      your own machine, from the checkout of the installation you are
#             converging — the dogfood one, whose region is us-east5. Nothing here
#             points at a project you did not name, and a coding agent's session
#             is refused: every command this check runs is one you could type
#             yourself.
# Costs:      wall clock, not a fixed fee: about 40 minutes of the run at the
#             terminal (the converge itself, Terraform's own applies, and the run
#             that is in flight across it), and that run's model calls
#             (**billable** — it is an ordinary run of the installation's agent).
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-821_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's branch and pull request are NOT
#             removed: this check holds no credential of its own.
# Output:     progress on stderr, ONE block on stdout headed CHECK 821 RESULT, and
#             nothing else. Exit 0: every criterion that was measured held. 1: a
#             measured criterion did not hold, or a guard stopped the check before
#             it could run. 2: a row could not be measured for a reason that is
#             this check's own setup or the installation's configuration rather
#             than a break (the block says which, by name). 3: a criterion was
#             read and its answer is UNVERIFIED — a step left for you, a plan that
#             only imports, a run that was not in flight. ONLY an all-PASS block
#             is a PASS.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# authorization header. Every fact below is a NAME — a project's, a repository's,
# a run's, a status, a location — and every line the CLI prints and this check
# reads is reduced to one bounded line before it is printed. This check holds no
# credential at all.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK821_REPO (or --repo: the checkout to run in, default the working
# directory), CHECK821_PROJECT (or --project: the installation's name, read from
# the checkout's own doctor answer when unset), CHECK821_DEADLINE (or --deadline:
# how long `fugaro update` may take, in seconds; 1800), CHECK821_START_SECONDS
# (or --start-wait: how long the launched run may take to appear `running`, in
# seconds; 900), CHECK821_WAIT_SECONDS (or --wait: how long it may then take to
# reach its terminal status, in seconds; 3600), CHECK821_POLL_SECONDS (20),
# CHECK821_NO_RUN (or --no-run: do not launch a run at all — the record, plan and
# update rows are still measured, and the inflight row reads UNVERIFIED),
# CHECK821_RUN_TITLE, CHECK821_KEEP_SCRATCH and CHECK821_DRY_RUN. None of them can
# point this check at another installation's own release: the project and the
# region are read from the checkout.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

# No xtrace, whatever the shell it was started from had: this check prints its
# own RESULT block, and a trace of a run that converges an installation and
# launches a run would copy that run's own output to the terminal.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

one_line() { printf '%s' "$1" | tr '\n\r\t' '   ' | tr -s ' ' | sed 's/^ *//; s/ *$//' | cut -c1-500; }

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check is built on it"
}

# field FILE KEY prints the value of one `key=value` line, the empty string when
# the key is absent.
field() { # field <file> <key>
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# last_line FILE: the last non-blank line of a file, bounded, for a row a reader
# has to be able to scan. The CLI's own account of how a command ended is the
# last thing it printed.
last_line() {
  if [ -f "$1" ]; then
    grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | cut -c1-300 || true
  fi
}

# left_for_you FILE...: the CLI's own sentence that a converge stopped with steps
# only you can run — "N step(s) left for you" — or the empty string. It is what
# tells a refusal from a hand-off on exit 1 (internal/cli/init_stages.go), and
# Check 380 reads the same line for the same reason.
left_for_you() {
  grep -h -o -E '[0-9]+ step\(s\) left for you' "$@" 2>/dev/null | tail -1
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 821: it converges a real installation and launches a real run. Run it yourself: bash scripts/live-checks/check-821.sh"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-821.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 821 — `fugaro update` on the dogfood installation with a run in flight: the
locations record it converges, the plan that follows it (U9), and the run it left
alone. Run by YOU, from the checkout of that installation.

Usage:
  bash scripts/live-checks/check-821.sh [--project <name>] [--repo <checkout>] [--deadline <seconds>]
  bash scripts/live-checks/check-821.sh --help

It launches one throwaway run of the checkout (billable model calls), waits until
its record reads `running`, converges the installation with the real
`fugaro update` (stdin on /dev/null, under a deadline), and then reads back what
the update wrote and what the run became. Every row it prints is one criterion of
the register's Check 821; the last line of the block is the verdict and the exit
status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED

It asks you nothing and waits on no input: every flag has a default, and the
installation it converges is the one the checkout names.

  --project <name>      the installation's name (default: the one the checkout's
                        own `fugaro doctor --json` answers)
  --repo <checkout>     the checkout to run in (default: the working directory)
  --deadline <seconds>  how long `fugaro update` may take (default: 1800)
  --start-wait <secs>   how long the launched run may take to appear `running`
                        (default: 900)
  --wait <seconds>      how long it may then take to reach its terminal status
                        (default: 3600)
  --no-run              launch no run: the update, asked, record and plan rows are
                        still measured, and the inflight row reads UNVERIFIED
  --dry-run             print the plan and call nothing
USAGE
}

REPO=${CHECK821_REPO:-$PWD}
NAME=${CHECK821_PROJECT:-}
DRY_RUN=no
NO_RUN=no
DEADLINE=${CHECK821_DEADLINE:-1800}
START_SECONDS=${CHECK821_START_SECONDS:-900}
WAIT_SECONDS=${CHECK821_WAIT_SECONDS:-3600}
POLL_SECONDS=${CHECK821_POLL_SECONDS:-20}
TITLE=${CHECK821_RUN_TITLE:-"Check 821: a run in flight across the update"}
KEEP_SCRATCH=${CHECK821_KEEP_SCRATCH:-}
if [ "${CHECK821_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi
if [ "${CHECK821_NO_RUN:-}" = "1" ]; then NO_RUN=yes; fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --project)
    [ $# -ge 2 ] || stop "--project needs the installation's name"
    NAME=$2
    shift 2
    ;;
  --repo)
    [ $# -ge 2 ] || stop "--repo needs the path of a checkout"
    REPO=$2
    shift 2
    ;;
  --deadline)
    [ $# -ge 2 ] || stop "--deadline needs a number of seconds"
    DEADLINE=$2
    shift 2
    ;;
  --start-wait)
    [ $# -ge 2 ] || stop "--start-wait needs a number of seconds"
    START_SECONDS=$2
    shift 2
    ;;
  --wait)
    [ $# -ge 2 ] || stop "--wait needs a number of seconds"
    WAIT_SECONDS=$2
    shift 2
    ;;
  --no-run)
    NO_RUN=yes
    shift
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  -*)
    stop "unknown argument $1 (usage: check-821.sh [--project <name>] [--repo <checkout>] [--deadline <seconds>], --help, or --dry-run)"
    ;;
  *)
    stop "unexpected argument $1: this check takes no positional argument — the installation is the one the checkout names, and --project names it when it does not"
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 821 plan (nothing is called: no fugaro at all)

  checkout: ${REPO}
  project:  ${NAME:-<read from the doctor answer of the checkout>}
  run:      $(if [ "$NO_RUN" = yes ]; then printf 'no run at all'; else printf 'one throwaway run of the checkout'; fi)
  deadline: ${DEADLINE}s for the update, ${START_SECONDS}s for the launched run to
            appear running, ${WAIT_SECONDS}s for it to finish

What it would do, in order:

  premise      fugaro doctor --json   (run from ${REPO})
               -> the local config in force must be the dogfood installation: region us-east5, a valid
               fugaro.yaml in the checkout, and a readable run history (fugaro runs ls --json).
               A premise that does not hold STOPS the check (exit 2).
  run          fugaro run --repo ${REPO} --project <name> --title ${TITLE} --task-file <scratch task> --json
               fugaro runs ls --json --project <name> --limit 200   (polled until it reads running)
               -> ONE throwaway run, in flight before the installation is converged. The task asks for
               a report and changes no file.
  update       fugaro update --project <name>   (stdin /dev/null, streams captured, deadline ${DEADLINE}s, TERM then KILL)
               -> exit 0 is a converge; exit 1 with "N step(s) left for you" is a hand-off
               (UNVERIFIED, and the steps are named); anything else is a finding.
  asked        every line the update printed, read for the shapes a question leaves behind
               -> [y/N] in any casing, (y/n), (yes/no), a 'to confirm:' left open, an enter-prompt, a
               question left waiting on a blank. One is a finding.
  record       fugaro doctor --json --project <name>
               -> the locations row (the record's only reader): its Firestore database in us-east5, its
               Realtime Database in us-central1, its secret containers replicated with Google's
               automatic replication, its run log bucket in global.
  plan         fugaro update --project <name> --plan-only --json
               -> U9: no root plans a change. Every count zero; an import alone is UNVERIFIED.
  inflight     fugaro runs ls --json --project <name> --limit 200   (polled until the run is terminal)
               -> the run launched above finished with its record whole: status, branch, pull request
               and outcome readable. A run that had already finished before the update started is
               UNVERIFIED, and one whose record is gone is a finding.
  restore      the check's own scratch directory is removed. The run's branch and pull request are NOT
               removed; the update's own writes to the installation stay, because that is an update.

Costs: the launched run's model calls (billable, the installation's budget governs) and about 40
minutes of wall clock. Cleanup: the check's scratch directory only.
PLAN
}

guard_xtrace
guard_agent

if [ "$DRY_RUN" = yes ]; then
  dry_run_plan
  exit 0
fi

need bash
need mktemp
need python3
need fugaro

# guard_seconds NAME VALUE: the flags that bound a wait are numbers, and a
# mistyped one must stop the check rather than reach `sleep` or `[`.
guard_seconds() { # guard_seconds <flag> <value>
  case "$2" in
  '' | *[!0-9]*) stop "$1 needs a number of seconds and was given '$2'" ;;
  esac
  [ "$2" -gt 0 ] || stop "$1 needs a number of seconds greater than zero and was given '$2'"
}
guard_seconds --deadline "$DEADLINE"
guard_seconds --start-wait "$START_SECONDS"
guard_seconds --wait "$WAIT_SECONDS"
guard_seconds CHECK821_POLL_SECONDS "$POLL_SECONDS"

[ -d "$REPO" ] || stop "--repo ${REPO} is not a directory: this check runs the update and launches the run from a checkout"
[ -f "$REPO/fugaro.yaml" ] || stop "${REPO} holds no fugaro.yaml, so it is not a Fugaro checkout: run this check from the checkout of the installation you are converging (--repo names another)"

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk821.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk821-mine"
: >"$MARKER"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
  if [ -n "$KEEP_SCRATCH" ]; then
    SCRATCH_REMOVED=kept
    say "keeping $SCRATCH (CHECK821_KEEP_SCRATCH is set)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

verdict() { # verdict <facts>
  if printf '%s\n' "$1" | grep -q '^aborted|'; then
    printf 'INCOMPLETE'
  elif printf '%s\n' "$1" | grep -q '|FAIL|'; then
    printf 'FAIL'
  elif printf '%s\n' "$1" | grep -q '|UNVERIFIED|'; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  local facts=$1 status=$2
  printf '\nCHECK 821 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-11s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 821 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  # The facts are read BEFORE the scratch directory holding them is removed.
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the installation"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the check's own scratch directory is gone (its documents: the launched run's task file, the update's transcript and every read this check made). What it created besides that is said in the rows above: the run's branch and pull request stay on the repository, and the update's own writes to the installation stay, because that is what an update is"
  elif [ "$SCRATCH_REMOVED" = kept ]; then
    facts="${facts}"$'\n'"restore|UNVERIFIED|the check's own scratch directory was KEPT on request (CHECK821_KEEP_SCRATCH is set), at ${SCRATCH}: nothing of yours is in it beyond what the rows above name, and it is yours to remove"
  else
    facts="${facts}"$'\n'"restore|FAIL|the check's own scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself"
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# ------------------------------------------------------------------- readers

# The documents this check reads, in one program: doctor's answer, the launch's
# own --json, the run's row in `runs ls --json`, the plan's counts, and the
# update's transcript read for the shapes a question leaves behind. A machine
# running a live check has python3 and no jq, and a row that read a transcript
# with grep would miss a question the day one is printed in another shape.
# Every query prints `key=value` lines, so the shell holds them without a second
# parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys

# The shapes a prompt leaves in a transcript, all of them in lower case: the
# answer a read waits for ([y/N] in any casing, (y/n), (yes/no)), the typed
# confirmation's own line left open, an enter-prompt, and a question mark
# followed by a blank -- a question left waiting for an answer. This CLI's own
# text has none of them: its refusals are one sentence ending in a full stop.
# Check 380 reads the same list for the same reason.
NEEDLES = ("[y/n]", "(y/n)", "(yes/no)", "to confirm:", "press enter", "hit enter", "press return", "hit return")


def load(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:
        return None


def pick(row, key):
    value = row.get(key)
    if value is None:
        return ""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (dict, list)):
        return json.dumps(value)
    return value


def kv(key, value):
    print("%s=%s" % (key, str(value).replace("\n", " ")))


what = sys.argv[1]

if what == "doctor":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    project = doc.get("project") if isinstance(doc.get("project"), dict) else {}
    checkout = doc.get("fugaro_yaml") if isinstance(doc.get("fugaro_yaml"), dict) else {}
    row = ""
    for check in doc.get("checks") or []:
        if isinstance(check, dict) and check.get("id") == "locations":
            row = check.get("problem") or ""
            break
    kv("name", project.get("name") or "")
    kv("gcp", project.get("gcp_project") or "")
    kv("region", project.get("region") or "")
    kv("yaml_path", checkout.get("path") or "")
    kv("yaml_valid", "true" if checkout.get("valid") else "false")
    kv("row", row)
elif what == "launch":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    kv("run_id", doc.get("run_id") or "")
    kv("run", doc.get("run") or "")
    kv("repo", doc.get("repo") or "")
    kv("status", doc.get("status") or "")
    kv("error", doc.get("error") or "")
elif what == "runs":
    doc = load(sys.argv[2])
    want = sys.argv[3]
    found = None
    if isinstance(doc, dict):
        for row in doc.get("runs") or []:
            if isinstance(row, dict) and want in (row.get("run_id"), row.get("run")):
                found = row
                break
    if found is None:
        kv("found", "no")
        sys.exit(0)
    kv("found", "yes")
    for key in ("status", "terminal", "outcome", "branch", "pr", "pushed", "title", "repo", "created", "cost"):
        kv(key, pick(found, key))
elif what == "plan":
    doc = load(sys.argv[2])
    if not isinstance(doc, dict):
        sys.exit(1)
    kv("error", doc.get("error") or "")
    changes = doc.get("changes")
    if isinstance(changes, dict):
        kv("present", "yes")
        for key in ("imports", "creates", "updates", "deletes", "replaces"):
            kv(key, changes.get(key, 0))
    else:
        kv("present", "no")
    kv("left", len(doc.get("left_for_you") or []))
elif what == "prompts":
    bad = []
    for path in sys.argv[2:]:
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as fh:
                text = fh.read()
        except OSError:
            continue
        for line in text.splitlines():
            if any(needle in line.lower() for needle in NEEDLES) or line.endswith("? "):
                bad.append(line)
    kv("count", len(bad))
    for line in bad[:5]:
        print("prompt=%s" % line.strip()[:240])
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json DEST QUERY ARGS...
  local dest=$1 query=$2
  shift 2
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err"
}

# doctor_read refreshes $SCRATCH/doctor.txt (the answer about the installation
# and the locations row inside it) and leaves the exit status in DOCTOR_STATUS.
DOCTOR_STATUS=0
doctor_read() {
  local args=(doctor --json)
  if [ -n "$NAME" ]; then args+=(--project "$NAME"); fi
  DOCTOR_STATUS=0
  (cd "$REPO" && fugaro "${args[@]}") >"$SCRATCH/doctor.json" 2>"$SCRATCH/doctor.err" || DOCTOR_STATUS=$?
  read_json "$SCRATCH/doctor.txt" doctor "$SCRATCH/doctor.json" || true
}

# runs_read refreshes $SCRATCH/runs.txt for one run id and leaves the read's exit
# status in RUNS_STATUS.
RUNS_STATUS=0
runs_read() { # runs_read <run-id>
  RUNS_STATUS=0
  (cd "$REPO" && fugaro runs ls --json --project "$NAME" --limit 200) >"$SCRATCH/runs.json" 2>"$SCRATCH/runs.err" || RUNS_STATUS=$?
  if [ "$RUNS_STATUS" -eq 0 ]; then
    read_json "$SCRATCH/runs.txt" runs "$SCRATCH/runs.json" "$1" || true
  else
    : >"$SCRATCH/runs.txt"
  fi
}

# run_closed_stdin SECONDS KEY CMD...: run one command with its stdin on
# /dev/null and its two streams in the scratch directory, from the checkout, and
# end it at the deadline. The child execs, so the pid the deadline kills IS the
# command: a converge waiting on a terminal would otherwise outlive the check
# that gave up on it, which is the one thing the deadline exists to prevent. The
# ERR trap is dropped in it too, because this check reads the run's failing exit
# status as one of its own readings and a trap inherited here would turn it into
# "an internal error" before the row could report it.
RUN_STATUS=0
RUN_TIMED_OUT=no
RUN_SECONDS=0
run_closed_stdin() { # run_closed_stdin SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  RUN_TIMED_OUT=no
  local started=$SECONDS
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  ( trap - ERR; cd "$REPO" || exit 1; exec "$@" ) </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      RUN_TIMED_OUT=yes
      say "the run is still going after ${secs}s: killing it (TERM, then KILL). A converge waiting on an answer looks exactly like this"
      kill -TERM "$pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 1
    waited=$((waited + 1))
    if [ $((waited % 60)) -eq 0 ]; then
      say "still running after ${waited}s of ${secs}s ..."
    fi
  done
  RUN_STATUS=0
  wait "$pid" 2>/dev/null || RUN_STATUS=$?
  RUN_SECONDS=$((SECONDS - started))
}

# ------------------------------------------------------------- the check's rows

# DOGFOOD_REGION is the installation this check is about. The register's Check
# 821 is the dogfood installation's own — the one whose record reads us-east5,
# us-central1, automatic and global — so a checkout whose config is in another
# region is refused by name rather than measured against the wrong four.
DOGFOOD_REGION=us-east5

# step_premise reads the installation this check is about and refuses to measure
# anything until it is the one Check 821 names.
step_premise() {
  say "reading the installation in ${REPO} (premise)"
  write_reader
  doctor_read
  local name region gcp valid
  name=$(field "$SCRATCH/doctor.txt" name)
  region=$(field "$SCRATCH/doctor.txt" region)
  gcp=$(field "$SCRATCH/doctor.txt" gcp)
  valid=$(field "$SCRATCH/doctor.txt" yaml_valid)
  if [ -z "$name" ]; then
    fact premise FAIL "'fugaro doctor --json' in ${REPO} named no installation (exit ${DOCTOR_STATUS}): $(last_line "$SCRATCH/doctor.err"). This check converges the installation the checkout belongs to, and one that cannot be named is not one to converge. Run 'fugaro doctor' yourself in that checkout and read what it says"
    stop "the checkout's own doctor named no installation"
  fi
  if [ -n "${CHECK821_PROJECT:-}" ] && [ "$CHECK821_PROJECT" != "$name" ]; then
    fact premise FAIL "you named the installation ${CHECK821_PROJECT} and this checkout answers ${name} (fugaro doctor --json): two different installations, and this check will not pick one"
    stop "the installation you named is not the checkout's"
  fi
  NAME=$name
  if [ "$region" != "$DOGFOOD_REGION" ]; then
    fact premise FAIL "the installation ${NAME} is in ${region:-<no region>}, and Check 821 is the dogfood installation's: the one whose record reads Firestore in ${DOGFOOD_REGION}, the Realtime Database in us-central1, automatic replication and a global log bucket. A checkout in another region is measured by its own check (Check 820 covers a new EU installation) rather than against those four"
    stop "the checkout's installation is not the one Check 821 measures"
  fi
  if [ "$valid" != "true" ]; then
    fact premise FAIL "${REPO}/fugaro.yaml is not valid (fugaro doctor --json, $(field "$SCRATCH/doctor.txt" yaml_path)): the run this check launches would be refused by it, and the rows below would read a converge of a repository that cannot run"
    stop "the checkout's fugaro.yaml is not valid"
  fi
  fact premise PASS "the checkout ${REPO} belongs to the installation ${NAME} (GCP project ${gcp}, region ${region}) and its fugaro.yaml is valid: this is the installation Check 821 is about"
}

# step_run launches ONE throwaway run of the checkout and waits until its record
# reads running, which is what makes the update below an update with a run in
# flight.
RUN_ID=""
RUN_SEEN_LIVE=no
step_run() {
  if [ "$NO_RUN" = yes ]; then
    fact run UNVERIFIED "no run was launched (--no-run), so nothing was in flight across the update below: the run-launched-before-the-update half of Check 821 is NOT measured, and the inflight row says the same"
    return 0
  fi
  local task="$SCRATCH/task.md"
  cat >"$task" <<'TASK'
# Task: Check 821: a run in flight across the update

A live check launched this run to watch `fugaro update` converge this
installation's own cloud state while a run is in flight. The check writes no
file and opens no pull request on your behalf: report only.

Read `internal/runner` and `docs/design/v1.md` and report, in your final message,
the loop's stages in the order the runner runs them, with the file and line where
each one starts. Change nothing, commit nothing, open no pull request.
TASK
  say "launching ONE run of ${REPO} before the update (billable): ${TITLE}"
  local rc=0
  (cd "$REPO" && fugaro run --repo "$REPO" --project "$NAME" --title "$TITLE" --task-file "$task" --json) \
    >"$SCRATCH/launch.json" 2>"$SCRATCH/launch.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact run FAIL "'fugaro run --repo ${REPO} --project ${NAME} --title ... --task-file ... --json' exited ${rc}, so no run was launched and nothing is in flight across the update: $(last_line "$SCRATCH/launch.err"). The run is the half of Check 821 that measures the update in flight; a launch that fails is that half not measured. Fix what the line names and run the check again"
    return 0
  fi
  read_json "$SCRATCH/launch.txt" launch "$SCRATCH/launch.json" || true
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  if [ -z "$RUN_ID" ]; then
    fact run FAIL "the launch printed no run id (fugaro run --json): $(one_line "$(cat "$SCRATCH/launch.json" 2>/dev/null)"). Nothing below can follow a run that was not named, so the update-in-flight half of this check is not measured"
    return 0
  fi
  say "launched ${RUN_ID}; waiting for its record to read running"
  local waited=0 status
  while [ "$waited" -lt "$START_SECONDS" ]; do
    runs_read "$RUN_ID"
    if [ "$RUNS_STATUS" -ne 0 ]; then
      fact run FAIL "the launched run ${RUN_ID} could not be read back: 'fugaro runs ls --json --project ${NAME}' exited ${RUNS_STATUS}: $(last_line "$SCRATCH/runs.err"). The record of the run this check launched is what the inflight row reads, and one that cannot be listed is not one to converge an installation under"
      return 0
    fi
    status=$(field "$SCRATCH/runs.txt" status)
    if [ "$status" = running ]; then
      RUN_SEEN_LIVE=yes
      break
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$RUN_SEEN_LIVE" = yes ]; then
    fact run PASS "one throwaway run of ${REPO} was launched before the update (${RUN_ID}) and its record read running after ${waited}s: the update below is an update with this run in flight, and the inflight row follows it to its end"
    return 0
  fi
  if [ "$(field "$SCRATCH/runs.txt" found)" = yes ]; then
    fact run UNVERIFIED "the launched run ${RUN_ID} was readable after ${waited}s but never read running: its status is '$(one_line "$(field "$SCRATCH/runs.txt" status)")'. A run that reached a terminal status before the update started means nothing was in flight across it, so the row below is no answer about a run in flight — the launch worked and the run is simply over"
    return 0
  fi
  fact run UNVERIFIED "the launched run ${RUN_ID} was not listed by 'fugaro runs ls --json --project ${NAME}' after ${waited}s (limit 200): the launch printed a run id, and its row is not there to read. Nothing below can say what it became"
}

# step_update runs the real converge, with its stdin closed and its output read
# back. This is the one row whose exit status is the CLI's own hand-off, so it
# tells a converge, a refusal and a step left for you apart by their own words.
UPDATE_STATUS=0
UPDATE_TIMED_OUT=no
UPDATE_SECONDS=0
step_update() {
  say "running: fugaro update --project ${NAME} < /dev/null  (deadline ${DEADLINE}s)"
  run_closed_stdin "$DEADLINE" update fugaro update --project "$NAME"
  UPDATE_STATUS=$RUN_STATUS
  UPDATE_TIMED_OUT=$RUN_TIMED_OUT
  UPDATE_SECONDS=$RUN_SECONDS
  local out="$SCRATCH/update.out" err="$SCRATCH/update.err"
  local last
  last=$(last_line "$err")
  local from=stderr
  if [ -z "$last" ]; then
    last=$(last_line "$out")
    from=stdout
  fi
  if [ "$UPDATE_TIMED_OUT" = yes ]; then
    fact update UNVERIFIED "'fugaro update --project ${NAME}' was still running after ${DEADLINE}s with its stdin on /dev/null and this check killed it (TERM, then KILL): a converge that needs longer and a run waiting on an answer look alike, and neither is an answer about the converge. Its last line (${from}): ${last:-<nothing>}. Raise --deadline and run the check again; the transcript is in ${out} and ${err} (CHECK821_KEEP_SCRATCH=1 keeps the directory)"
    return 0
  fi
  local left
  left=$(left_for_you "$out" "$err" || true)
  case "$UPDATE_STATUS" in
  0)
    fact update PASS "'fugaro update --project ${NAME}' ended by itself after ${UPDATE_SECONDS}s with exit 0${left:+, and it still left ${left}}: it converged what it had to do, or it had nothing to do. Its last line (${from}): ${last:-<nothing>}"
    ;;
  1)
    if [ -n "$left" ]; then
      local steps
      steps=$(grep -h -m 3 '^left for you ' "$out" 2>/dev/null | while IFS= read -r line; do one_line "$line"; printf ' | '; done || true)
      fact update UNVERIFIED "'fugaro update --project ${NAME}' ended after ${UPDATE_SECONDS}s with exit 1 and its own sentence for a hand-off — ${left} — which is the CLI's answer for a step only you can run and not a failure: the converge did what it could without a terminal. What it left: ${steps:-<not printed>} Its last line (${from}): ${last:-<nothing>}. Run the step(s) at your own terminal and run the check again: the record and plan rows below were measured on the installation the update did converge"
      return 0
    fi
    fact update FAIL "'fugaro update --project ${NAME}' ended after ${UPDATE_SECONDS}s with exit 1 and no 'step(s) left for you' sentence, so this is a refusal and the installation was NOT converged: its last line (${from}): ${last:-<nothing>}. The whole transcript is in ${out} and ${err} (CHECK821_KEEP_SCRATCH=1 keeps the directory); the record and plan rows below read an installation this update did not converge"
    ;;
  2)
    fact update FAIL "'fugaro update --project ${NAME}' ended after ${UPDATE_SECONDS}s with exit 2 — a cloud failure, so the converge did not finish: its last line (${from}): ${last:-<nothing>}. Fix what the line names and run the check again; the record and plan rows below read an installation this update left part-way"
    ;;
  *)
    fact update FAIL "'fugaro update --project ${NAME}' ended after ${UPDATE_SECONDS}s with exit ${UPDATE_STATUS}, which is not one of update's own codes (0 done, 1 a refusal or a step left for you, 2 a cloud failure): its last line (${from}): ${last:-<nothing>}"
    ;;
  esac
}

# step_asked reads every line the update printed for the shapes a question leaves
# behind. It is Check 380's reading of the no-prompt policy (T7), taken here
# because this check is already running that update.
step_asked() {
  local out="$SCRATCH/update.out" err="$SCRATCH/update.err"
  if [ ! -s "$out" ] && [ ! -s "$err" ]; then
    fact asked UNVERIFIED "the update printed nothing at all (exit ${UPDATE_STATUS} after ${UPDATE_SECONDS}s), so there is no transcript to read for a question: an empty transcript is neither evidence that it asked nothing nor evidence of a converge. Its output files are in ${out} and ${err} (CHECK821_KEEP_SCRATCH=1 keeps the directory)"
    return 0
  fi
  read_json "$SCRATCH/prompts.txt" prompts "$out" "$err" || true
  local count listed
  count=$(field "$SCRATCH/prompts.txt" count)
  listed=$(awk -F'=' '$1 == "prompt" { printf "%s | ", substr($0, 8) }' "$SCRATCH/prompts.txt" 2>/dev/null || true)
  if [ "${count:-0}" != 0 ]; then
    fact asked FAIL "${count} line(s) of the update's own output ask for an answer: ${listed}A command that puts a question to a run with no terminal is the one prompt the policy removes (D3090, T7). The whole transcript is in ${out} and ${err} (CHECK821_KEEP_SCRATCH=1 keeps the directory)"
    return 0
  fi
  fact asked PASS "none of the update's output asks for an answer: no [y/N] in any casing, no (y/n) or (yes/no), no 'to confirm:' left open, no enter-prompt and no question left waiting on a blank. Its streams: stdin was /dev/null (a read meets EOF at once and never a person's answer), stdout and stderr were files in the scratch directory, and no pty was attached to either"
}

# step_record reads the record the update wrote, through its only reader.
step_record() {
  say "reading the locations record through 'fugaro doctor --json' (record)"
  doctor_read
  local row
  row=$(field "$SCRATCH/doctor.txt" row)
  if [ -z "$row" ]; then
    fact record UNVERIFIED "'fugaro doctor --json --project ${NAME}' (exit ${DOCTOR_STATUS}) carries no locations row, so the record this update wrote is NOT measured: $(last_line "$SCRATCH/doctor.err"). Run 'fugaro doctor --project ${NAME}' yourself and read its locations row"
    return 0
  fi
  printf '%s\n' "$row" >"$SCRATCH/locations-row.txt"
  local missing="" want
  for want in \
    "its Firestore database is in us-east5" \
    "its Realtime Database is in us-central1" \
    "replicated with Google's automatic replication" \
    "its run log bucket is in global"; do
    printf '%s' "$row" | grep -qF "$want" || missing="${missing}${missing:+; }${want}"
  done
  if [ -n "$missing" ]; then
    fact record FAIL "the locations record of ${NAME} does not read what that installation really has. What is missing from doctor's row: ${missing}. The row it printed: $(one_line "$row")"
    return 0
  fi
  fact record PASS "the record 'fugaro update' wrote from what it found reads the dogfood installation's four things: $(one_line "$row")"
}

# step_plan reads the plan that follows the converge: U9's own reading, whether
# the pinned provider's ignore_changes holds on an installation that has just
# been converged.
step_plan() {
  say "reading the plan that follows the converge (plan, --plan-only)"
  local rc=0
  (cd "$REPO" && fugaro update --project "$NAME" --plan-only --json) >"$SCRATCH/plan.json" 2>"$SCRATCH/plan.err" || rc=$?
  read_json "$SCRATCH/plan.txt" plan "$SCRATCH/plan.json" || true
  local present err changing counts
  present=$(field "$SCRATCH/plan.txt" present)
  err=$(field "$SCRATCH/plan.txt" error)
  counts="imports=$(field "$SCRATCH/plan.txt" imports), creates=$(field "$SCRATCH/plan.txt" creates), updates=$(field "$SCRATCH/plan.txt" updates), deletes=$(field "$SCRATCH/plan.txt" deletes), replaces=$(field "$SCRATCH/plan.txt" replaces)"
  if [ "$present" != yes ]; then
    fact plan UNVERIFIED "'fugaro update --plan-only --json' (exit ${rc}) carried no plan: ${err:-$(last_line "$SCRATCH/plan.err")}. Whether a converged installation plans a change (U9) is NOT measured — a --plan-only that cannot read the plan is no answer either way. The document is in ${SCRATCH}/plan.json (CHECK821_KEEP_SCRATCH=1 keeps it)"
    return 0
  fi
  changing=$(awk -F'=' '$1=="creates"||$1=="updates"||$1=="deletes"||$1=="replaces"{s+=$2} END{print s+0}' "$SCRATCH/plan.txt")
  if [ "${changing:-0}" != 0 ]; then
    fact plan FAIL "the plan after the converge is not empty: ${counts}. A converged installation has nothing left to do, and a plan that creates, updates, deletes or replaces anything means the provider's ignore_changes does not hold there (U9) — or that this update did not converge what the plan reads. The plan is in ${SCRATCH}/plan.json (CHECK821_KEEP_SCRATCH=1 keeps it); 'fugaro update --plan-only' at your terminal prints which resource it is"
    return 0
  fi
  local imports
  imports=$(field "$SCRATCH/plan.txt" imports)
  if [ "${imports:-0}" != 0 ]; then
    fact plan UNVERIFIED "the plan after the converge changes nothing in the cloud, but it adopts ${imports} existing resource(s) into the state: ${counts}. U9's own reading — no change to any root — holds, and a plan that is not empty is not what a converge should leave behind either: run 'fugaro update --plan-only' at your terminal to see which addresses it would import"
    return 0
  fi
  fact plan PASS "the plan after the converge is empty: ${counts}, and the update's own left-for-you count is $(field "$SCRATCH/plan.txt" left). U9 holds on this installation: nothing of a converged installation is planned as a change"
}

# step_inflight follows the run launched before the update to its terminal
# status and reads its record back. docs/design/update-in-flight.md: the update
# asks no run to finish first and holds nothing a run writes.
step_inflight() {
  if [ -z "$RUN_ID" ]; then
    fact inflight UNVERIFIED "no run was launched by this check (--no-run), so nothing was in flight across the update: the criterion 'a run launched before the update finishes' is NOT measured. Nothing here says an update disturbs a run in flight, and nothing says it does not"
    return 0
  fi
  say "following the run launched before the update (${RUN_ID})"
  local waited=0 status found
  while :; do
    runs_read "$RUN_ID"
    if [ "$RUNS_STATUS" -ne 0 ]; then
      fact inflight FAIL "'fugaro runs ls --json --project ${NAME}' exited ${RUNS_STATUS} while following ${RUN_ID}: $(last_line "$SCRATCH/runs.err"). The record of the run this check launched cannot be read at all, which is the one thing this row exists to catch"
      return 0
    fi
    found=$(field "$SCRATCH/runs.txt" found)
    status=$(field "$SCRATCH/runs.txt" status)
    if [ "$found" != yes ] || [ "$status" != running ]; then
      break
    fi
    if [ "$waited" -ge "$WAIT_SECONDS" ]; then
      fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) is still running after ${WAIT_SECONDS}s, and this check stops waiting here: its record is whole and it was in flight across the update, but nothing here says it FINISHES — raise --wait and run the check again to read its terminal status"
      return 0
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
    if [ $((waited % 300)) -eq 0 ]; then say "still following ${RUN_ID} after ${waited}s of ${WAIT_SECONDS}s ..."; fi
  done
  if [ "$found" != yes ]; then
    fact inflight FAIL "the run launched before the update (${RUN_ID}) is NOT in 'fugaro runs ls --json --project ${NAME}' (limit 200) after the converge: a run's record is in the runs bucket, and one that is gone after an update is the finding this row exists for. Look for it yourself: fugaro runs show ${RUN_ID} --project ${NAME}"
    return 0
  fi
  local outcome pr pushed branch
  outcome=$(field "$SCRATCH/runs.txt" outcome)
  pr=$(field "$SCRATCH/runs.txt" pr)
  pushed=$(field "$SCRATCH/runs.txt" pushed)
  branch=$(field "$SCRATCH/runs.txt" branch)
  if [ "$RUN_SEEN_LIVE" != yes ]; then
    fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) reached '${status}' and its record is whole (branch ${branch:-<none>}, pushed ${pushed}, pull request ${pr:-<none>}, outcome ${outcome:-<none>}), but it was never seen running: it had already finished when the update started, so nothing was in flight across it and this row is no answer about a run in flight"
    return 0
  fi
  case "$status" in
  succeeded)
    fact inflight PASS "the run launched before the update (${RUN_ID}) was in flight across it and finished: status succeeded, branch ${branch:-<none>}, pushed ${pushed}, pull request ${pr:-<none>}, outcome ${outcome:-<none>}. Its record is whole after the converge, which is what docs/design/update-in-flight.md promises: every write a run makes still lands"
    ;;
  failed | infra_error | cancelled | halted | unreviewed)
    fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) was in flight across it and ended '${status}' (branch ${branch:-<none>}, pull request ${pr:-<none>}, outcome ${outcome:-<none>}): its record is whole and it was not disturbed out of existence, but a run that did not finish succeeded is not the reading this row wants — read its own history (fugaro runs show ${RUN_ID} --project ${NAME}) before treating it as an answer about the update"
    ;;
  *)
    fact inflight UNVERIFIED "the run launched before the update (${RUN_ID}) read back as '${status}', which is not a status this check knows how to read: its record is in ${SCRATCH}/runs.json (CHECK821_KEEP_SCRATCH=1 keeps the directory). Read it yourself: fugaro runs show ${RUN_ID} --project ${NAME}"
    ;;
  esac
}

# ------------------------------------------------------------------ the check

say "${PROG}: converging the installation ${NAME:-<read from ${REPO}>} with a run in flight (--help for the flags)"

step_premise
step_run
step_update
step_asked
step_record
step_plan
step_inflight

REACHED_END=yes
